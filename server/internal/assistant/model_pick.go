package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A model picked for one of the assistant's conversations (#5033, docs/assistant/model-config.md "A conversation's own
// model"): the user switches a conversation to another launcher, model or reasoning effort in the chat's model panel,
// and its next messages run on that until they switch again or go back to the default. The pick lives with the
// conversation on the hub side (settings assistant.model_picks), so every device sees the same one (model_pick on the
// conversation) and a message carries none of it. With no pick, the main conversation and a scheduled task's follow
// Model configuration › Main chat and the others keep what they run, as before (sendModel). A launcher or model picked
// with a message (an older app) still wins for that message.
//
// Unlike the default, a pick is never replaced behind the user's back: a message to a conversation whose picked
// launcher is gone from the computer, out of its provider's device scope or unable to run the assistant is refused with
// why (modelPickRefusal), and the user picks again.

const (
	modelPicksKey = "assistant.model_picks"
	// maxModelPicks bounds the book (conversations come and go): the oldest picks go first.
	maxModelPicks = 512
)

// modelPick is a conversation's pick as kept.
type modelPick struct {
	MachineID string    `json:"machine_id"`
	Launcher  string    `json:"launcher"`
	Model     string    `json:"model,omitempty"`
	Effort    string    `json:"effort,omitempty"`
	At        time.Time `json:"at"`
}

// wire is the pick as the apps get it.
func (p modelPick) wire() *proto.ModelPick {
	return &proto.ModelPick{Launcher: p.Launcher, Model: p.Model, Effort: p.Effort}
}

// modelPickBook is the book in memory. m is replaced, never changed in place, so a reader keeps the one it took; mu
// guards m and loaded, write makes changes one at a time and is held over the database write, which readers (every
// snapshot, every message) never wait on.
type modelPickBook struct {
	mu     sync.Mutex
	write  sync.Mutex
	loaded bool
	m      map[string]modelPick // by session id
}

// pickEfforts are the reasoning efforts a pick may carry: "auto" (the model's own default), Claude Code's levels and
// Codex's (node/codex_effort.go). Whether the model takes one is the node's to say when it runs.
var pickEfforts = append([]string{"auto", "none", "minimal", "ultra"}, proto.EffortLevels...)

// modelPicksLocked reads the book once; b.mu is held. A failed read leaves it unloaded (nothing is saved over it).
func (s *Service) modelPicksLocked(ctx context.Context) bool {
	b := &s.modelPicks
	if b.loaded {
		return true
	}
	m := map[string]modelPick{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, modelPicksKey, &m); err != nil {
			s.logf("load conversation model picks: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]modelPick{}
	}
	b.m, b.loaded = m, true
	return true
}

// modelPicksSet is every pick by session id; false while the book could not be read.
func (s *Service) modelPicksSet() (map[string]modelPick, bool) {
	b := &s.modelPicks
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.modelPicksLocked(context.Background()) {
		return nil, false
	}
	return maps.Clone(b.m), true
}

// modelPickOf is conversation sid's pick on machine; false when it has none (or the book can't be read: the message
// then runs as if there were none, as a pick that is not known can't be honored or refused).
func (s *Service) modelPickOf(ctx context.Context, machine, sid string) (modelPick, bool) {
	b := &s.modelPicks
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.modelPicksLocked(ctx) {
		return modelPick{}, false
	}
	p, ok := b.m[sid]
	return p, ok && p.MachineID == machine
}

// setModelPick keeps p as conversation sid's pick (nil: drops it) and tells the apps. Dropping a pick that isn't there
// is no error.
func (s *Service) setModelPick(ctx context.Context, sid string, p *modelPick) error {
	return s.updateModelPick(ctx, sid, func(_ modelPick, had bool) (*modelPick, bool) {
		if p == nil {
			return nil, had
		}
		return p, true
	})
}

// updateModelPick changes conversation sid's pick to what change makes of it (its pick, and whether it had one): the
// next pick (nil: none) and whether anything changed. A change is saved, then the apps are told.
func (s *Service) updateModelPick(ctx context.Context, sid string, change func(p modelPick, had bool) (*modelPick, bool)) error {
	st := s.store()
	if st == nil {
		return i18n.Err("assistant-models.err_no_database")
	}
	b := &s.modelPicks
	b.write.Lock()
	b.mu.Lock()
	if !s.modelPicksLocked(ctx) {
		b.mu.Unlock()
		b.write.Unlock()
		return i18n.Err("assistant-model_pick.err_unreadable")
	}
	cur := b.m
	b.mu.Unlock()
	old, had := cur[sid]
	p, changed := change(old, had)
	if !changed {
		b.write.Unlock()
		return nil
	}
	next := maps.Clone(cur)
	if p == nil {
		delete(next, sid)
	} else {
		next[sid] = *p
		s.evictModelPicks(ctx, next, sid)
	}
	if err := st.PutSetting(ctx, modelPicksKey, next); err != nil {
		b.write.Unlock()
		return err
	}
	b.mu.Lock()
	b.m = next
	b.mu.Unlock()
	b.write.Unlock() // before publishing: the snapshot reads the book, and the next change need not wait for it
	s.publish(ctx)   // the apps get the conversation with its model_pick now
	return nil
}

// evictModelPicks bounds picks to maxModelPicks: the oldest go, but never the main conversation's, however long it has
// kept it, nor kept's (the one just set).
func (s *Service) evictModelPicks(ctx context.Context, picks map[string]modelPick, kept string) {
	if len(picks) <= maxModelPicks {
		return
	}
	main := s.settings(ctx).Main
	for len(picks) > maxModelPicks {
		oldest := ""
		for id, p := range picks {
			if id != main && id != kept && (oldest == "" || p.At.Before(picks[oldest].At)) {
				oldest = id
			}
		}
		if oldest == "" {
			return
		}
		delete(picks, oldest)
	}
}

// applyPick puts conversation pick into p, a message's (or warm's) params, as a start with it runs: its launcher, its
// model, its effort unless p has one, checked and fingerprinted. A pick without a model runs what the start ran: Model
// configuration › Main chat's model when the launcher is the one configured there (applyModel, later changes there
// too), else the launcher's own default.
func (s *Service) applyPick(ctx context.Context, p map[string]any, pick modelPick) error {
	p["launcher"] = pick.Launcher
	if e, _ := p["effort"].(string); e == "" && pick.Effort != "" {
		p["effort"] = pick.Effort
	}
	if pick.Model == "" {
		delete(p, "model")
		return s.applyModel(ctx, "assistant", p)
	}
	p["model"] = pick.Model
	return s.applyPickedModel(ctx, p)
}

// pickEffortSet makes effort the pick's effort of conversation sid on machine, when it has a pick: the apps set it
// through POST …/modes (#2416), and the next message would otherwise put the pick's back.
func (s *Service) pickEffortSet(ctx context.Context, machine, sid, effort string) {
	if !slices.Contains(pickEfforts, effort) {
		return
	}
	err := s.updateModelPick(ctx, sid, func(p modelPick, had bool) (*modelPick, bool) {
		if !had || p.MachineID != machine || p.Effort == effort {
			return nil, false
		}
		p.Effort, p.At = effort, time.Now().UTC()
		return &p, true
	})
	if err != nil {
		s.logf("[%s] %s: the pick's effort: %v", machine, short(sid), err)
	}
}

// nodeCallDone sees a node call the hub made through this manager's devices (devices.call, params) once it went
// through: an effort set on a conversation with a pick (session.modes) becomes the pick's (pickEffortSet). Anything else
// is left alone, without being decoded.
func (s *Service) nodeCallDone(ctx context.Context, params json.RawMessage) {
	if !bytes.Contains(params, []byte(`"session.modes"`)) {
		return
	}
	var c devicewire.NodeCall
	if json.Unmarshal(params, &c) != nil || c.Method != "session.modes" {
		return
	}
	var m struct {
		SessionID string `json:"session_id"`
		Effort    string `json:"effort"`
	}
	if json.Unmarshal(c.Params, &m) != nil || m.SessionID == "" || m.Effort == "" {
		return
	}
	s.pickEffortSet(ctx, c.MachineID, m.SessionID, m.Effort)
}

// conversationMachine is where conversation sid runs: machine when the app named one, the main conversation's
// machine for the main one, else the computer that lists it as the assistant's, else the assistant's own.
func (s *Service) conversationMachine(ctx context.Context, sid, machine string) string {
	if machine != "" {
		return machine
	}
	cfg := s.settings(ctx)
	if cfg.Main != "" && cfg.Main == sid {
		return cfg.mainRouteMachine()
	}
	for _, m := range s.fleetMachines() {
		if sess, ok := s.devs.Session(m.ID, sid); ok && m.isAssistant(sess) {
			return m.ID
		}
	}
	return cfg.MachineID
}

// pickError is a refused pick with the status the route answers.
type pickError struct {
	status int
	err    error
}

func (e *pickError) Error() string { return e.err.Error() }
func (e *pickError) Unwrap() error { return e.err }

func refusePick(status int, err error) error { return &pickError{status: status, err: err} }

// pickModel checks a pick for conversation sid (on machine, "" to find it) and keeps it: the launcher can run the
// assistant there (modelPickRefusal) and runs the conversation's agent kind (otherKindPick), the model is a model id
// the launcher takes, the effort one a pick may carry.
// lang is the app's, for the refusals that are worded here.
func (s *Service) pickModel(ctx context.Context, sid, machine string, in proto.ModelPick, lang i18n.Lang) (modelPick, error) {
	in.Launcher, in.Model, in.Effort = strings.TrimSpace(in.Launcher), strings.TrimSpace(in.Model), strings.TrimSpace(in.Effort)
	switch {
	case in.Launcher == "":
		return modelPick{}, refusePick(http.StatusBadRequest, i18n.Err("assistant-model_pick.err_launcher_required"))
	case in.Effort != "" && !slices.Contains(pickEfforts, in.Effort):
		return modelPick{}, refusePick(http.StatusBadRequest, i18n.Err("assistant-model_pick.err_effort", i18n.Args{"effort": in.Effort}))
	}
	if err := checkModelID(in.Model); err != nil {
		return modelPick{}, refusePick(http.StatusBadRequest, err)
	}
	machine = s.conversationMachine(ctx, sid, machine)
	d, ok := s.device(ctx, machine)
	if !ok {
		return modelPick{}, refusePick(http.StatusNotFound, errUnknownMachine)
	}
	if cfg := s.settings(ctx); cfg.Main == "" || cfg.Main != sid || cfg.mainRouteMachine() != machine {
		// Not the main conversation (which may not have run yet): one of the assistant's that its machine lists.
		if c, known := s.session(ctx, machine, sid); !known || !c.Assistant {
			return modelPick{}, refusePick(http.StatusNotFound, i18n.Err("assistant-model_pick.err_no_conversation", i18n.Args{"machine": firstNonEmpty(d.Name, machine)}))
		}
	}
	if s.isGuest(ctx, machine, sid) { // it keeps what it started on (sendParams)
		return modelPick{}, refusePick(http.StatusConflict, i18n.Err("assistant-model_pick.err_guest"))
	}
	if err := s.modelPickRefusal(ctx, machine, in.Launcher, lang, false); err != nil {
		return modelPick{}, err
	}
	var other *launcherOtherKind
	if err := s.otherKindPick(ctx, machine, sid, in.Launcher); errors.As(err, &other) {
		return modelPick{}, other.rpcError(lang) // Claude Code can't resume a Codex thread, nor Codex a Claude transcript
	}
	if in.Model != "" {
		if err := s.pickedModelRefusal(ctx, machine, in.Launcher, in.Model); err != nil {
			return modelPick{}, refusePick(http.StatusBadRequest, err)
		}
	}
	p := modelPick{MachineID: machine, Launcher: in.Launcher, Model: in.Model, Effort: in.Effort, At: time.Now().UTC()}
	if err := s.setModelPick(ctx, sid, &p); err != nil {
		return modelPick{}, err
	}
	return p, nil
}

// modelPickRefusal is why launcher name can't be a conversation's pick on machine, nil when it can: it can't run the
// assistant there, or it is not there (a Persona-managed provider out of the computer's device scope, or skipping a
// computer with its own sign-in; any other launcher missing from the computer's list). sending: the refusal of a
// message that would have run on it (the words say the message was not sent). As mainChatFit, a list that can't be
// read refuses nothing.
func (s *Service) modelPickRefusal(ctx context.Context, machine, name string, lang i18n.Lang, sending bool) error {
	fit, refusal := s.mainChatFit(ctx, machine, name)
	switch fit {
	case mainChatFits:
		return nil
	case mainChatRefused:
		if sending {
			return refusal
		}
		d, _ := s.device(ctx, machine)
		return refusePick(http.StatusBadRequest, i18n.Err("assistant-model_pick.err_not_main_chat",
			i18n.Args{"launcher": name, "machine": firstNonEmpty(d.Name, machine)}))
	}
	if l, managed := s.managedNamed(ctx, name); managed {
		if key := s.scopeReason(l, machine); key != "" {
			return &rpc.Error{Code: proto.CodeLauncherOutOfScope, Message: s.scopeText(ctx, lang, key, l, machine)}
		}
		if l.SkipsOwnService() {
			return s.ownServiceErr(ctx, lang, l, machine)
		}
	}
	d, _ := s.device(ctx, machine)
	data, _ := json.Marshal(map[string]string{"launcher": name, "machine_name": d.Name}) // the hub words it (apierr.Sentence)
	return &rpc.Error{Code: proto.CodeLauncherMissing, Message: fmt.Sprintf("launcher %s is not on %s", name, machine), Data: data}
}

// pickedModelRefusal: model can't be picked with launcher on machine because the launcher would not pass it to its
// agent (it sets its own --model, or its agent takes no model). A model its list lacks is a typed one and is kept, as a
// message's pick (#1644); a list that can't be read refuses nothing.
func (s *Service) pickedModelRefusal(ctx context.Context, machine, launcher, model string) error {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := s.askLauncherModels(cctx, machine, launcher, false) // the node keeps its list a while
	switch {
	case err != nil:
		return nil
	case res.Locked != "":
		return i18n.Err("assistant-model_pick.err_locked", i18n.Args{"launcher": launcher})
	case !takesTypedModel(res) && !listsModel(res, model):
		return i18n.Err("assistant-model_pick.err_no_model", i18n.Args{"launcher": launcher})
	}
	return nil
}

// keepStartPick keeps the launcher / model / effort a new conversation was started with (POST
// /api/v1/assistant/conversations) as its pick, so its later messages keep them, and shows it on sess. Nothing is kept
// when neither a launcher nor a model was named, or when the start fell back to another launcher than the one named (or than the one the model
// was for); an effort a pick can't carry is left out.
func (s *Service) keepStartPick(ctx context.Context, sess *proto.Session, launcher, model, effort string) {
	launcher, effort = strings.TrimSpace(launcher), strings.TrimSpace(effort)
	switch {
	case launcher == "" && model == "", sess.Launcher == "":
		return // an effort alone (iOS sends its last one with every start) is no pick: Model configuration still holds
	case launcher != "" && launcher != sess.Launcher, model != "" && model != sess.Model:
		return // it fell back: the pick is not what runs
	}
	if !slices.Contains(pickEfforts, effort) {
		effort = ""
	}
	p := modelPick{MachineID: sess.MachineID, Launcher: sess.Launcher, Model: model, Effort: effort, At: time.Now().UTC()}
	if err := s.setModelPick(ctx, sess.SessionID, &p); err != nil {
		s.logf("[%s] %s: keep the model it started on: %v", sess.MachineID, short(sess.SessionID), err)
		return
	}
	sess.ModelPick = p.wire()
}

// storedPickRefused: err refuses a pick, or a message on its conversation's pick, and carries a code the hub words and
// the apps act on (launcher_out_of_scope, launcher_own_service, launcher_missing, launcher_other_kind): it goes on as it
// is (409 with the code).
func storedPickRefused(err error) bool {
	var re *rpc.Error
	return errors.As(err, &re) && slices.Contains([]string{proto.CodeLauncherOutOfScope, proto.CodeLauncherOwnService,
		proto.CodeLauncherMissing, assistantwire.CodeLauncherOtherKind}, re.Code)
}

// modelPickRoutes: PUT /api/v1/assistant/conversations/{id}/model {launcher, model?, effort?, machine_id?} keeps the pick
// ({ok, model_pick}); DELETE there drops it (the conversation follows the default again; {ok}).
func (s *Service) modelPickRoutes() {
	s.handle("PUT /api/v1/assistant/conversations/{id}/model", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-model_pick.err_role"))
			return
		}
		id := r.PathValue("id")
		if !validSessionID(id) {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-model_pick.err_invalid_id"))
			return
		}
		var in struct {
			proto.ModelPick
			MachineID string `json:"machine_id"` // "": the conversation's machine, found (conversationMachine)
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		p, err := s.pickModel(ctx, id, in.MachineID, in.ModelPick, s.requestLang(r))
		var pe *pickError
		switch {
		case errors.As(err, &pe):
			apierr.Write(w, pe.status, pe.err)
		case storedPickRefused(err):
			apierr.Call(w, err) // 409 with its code; launcher_missing worded by apierr in the app's language
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
		default:
			apierr.WriteJSON(w, map[string]any{"ok": true, "model_pick": p.wire()})
		}
	})
	s.handle("DELETE /api/v1/assistant/conversations/{id}/model", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-model_pick.err_role"))
			return
		}
		id := r.PathValue("id")
		if !validSessionID(id) {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-model_pick.err_invalid_id"))
			return
		}
		if err := s.setModelPick(r.Context(), id, nil); err != nil {
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
}
