package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The assistant's settings are its own (internal/assistant, profile.go): the hub keeps the profile it last reported
// (Snapshot.Profile) for its own features — the model configuration runs the main chat on the assistant's machine, a phone's
// message to the main conversation, the user's time zone, the conversations the nodes keep listing — and asks the
// assistant about its conversations (assistant.start, assistant.send_params, assistant.rename). The Feishu bot and the
// channels are the assistant's own (assistant_channels.go): they do not go by it. Before
// the first report since it started the hub goes by the copy it kept (loadAssistantProfile); with none, by hub.json's
// assistant section: its machine (else the hub's own, else the first), its launcher, no main conversation, the hub's
// own time zone, and nothing said to the nodes about what they keep.

// assistantProfileKey is the hub's own copy of the profile the assistant last reported (the hub writes and reads it,
// the assistant never does): what the hub's features go by after a restart, before the assistant reports again. The
// knowledge base and the plugins start before it connects, and they need the user's time zone. Without it they went by
// the hub's own zone for those seconds (the knowledge base filed itself into the hub's zone and back).
const assistantProfileKey = "hub.assistant_profile"

// loadAssistantProfile takes up the profile the hub kept (assistantProfileKey, its own settings) before the assistant
// reports one. Without one, hub.json's defaults stand until the assistant reports (the hub never reads the
// assistant's own settings: they are the manager's).
func (h *Hub) loadAssistantProfile(ctx context.Context) {
	if h.store == nil {
		return
	}
	var p assistantwire.Profile
	ok, err := h.store.Setting(ctx, assistantProfileKey, &p)
	if err != nil {
		h.logf("assistant: the profile kept: %v", err)
		return
	}
	if !ok {
		return
	}
	a := &h.assistant
	a.mu.Lock()
	if a.profile == nil { // a report that came in meanwhile wins
		a.profile = &p
	}
	a.mu.Unlock()
}

// keepAssistantProfile stores p as the hub's copy (assistantProfileKey).
func (h *Hub) keepAssistantProfile(ctx context.Context, p *assistantwire.Profile) {
	if h.store == nil || p == nil {
		return
	}
	if err := h.store.PutSetting(ctx, assistantProfileKey, p); err != nil {
		h.logf("assistant: keep the profile: %v", err)
	}
}

// assistantProfile is the assistant's settings as it last reported them (or as the hub kept them from before a
// restart, loadAssistantProfile), else hub.json's defaults.
func (h *Hub) assistantProfile() assistantwire.Profile {
	a := &h.assistant
	a.mu.RLock()
	p := a.profile
	a.mu.RUnlock()
	if p != nil {
		return *p
	}
	return assistantwire.Profile{MachineID: h.defaultAssistantMachine(), Launcher: "claude"}
}

// assistantProfileOf is the selected manager's last reported profile, falling back to the built-in's while it
// has not reported one. A reported profile is used as a whole, including fields the manager left empty.
func (h *Hub) assistantProfileOf(id string) assistantwire.Profile {
	a := h.assistantOf(id)
	a.mu.RLock()
	p := a.profile
	a.mu.RUnlock()
	if p != nil {
		return *p
	}
	return h.assistantProfile()
}

// isMainOn: session on machine is the assistant's main conversation, as it reported.
func (h *Hub) isMainOn(machine, session string) bool { return h.mainOf(machine, session) }

// defaultAssistantMachine is where the hub would run the assistant: the hub's own machine, else the first one; never a
// computer waiting to be confirmed (machine_trust.go). The manager's manager.json assistant.machine goes over it.
func (h *Hub) defaultAssistantMachine() string {
	machines := slices.DeleteFunc(h.machinesNow(), func(m *Machine) bool { return h.machineUntrusted(m.cfg.ID) || h.machineIsWork(m.cfg.ID) })
	for _, m := range machines {
		if m.cfg.Kind == "local" {
			return m.cfg.ID
		}
	}
	if len(machines) > 0 {
		return machines[0].cfg.ID
	}
	return ""
}

// ---- asking the assistant about its conversations ----

// Start asks the assistant to start a conversation (assistant.start).
func (a assistantRemote) Start(ctx context.Context, p assistantwire.StartParams) (*proto.Session, error) {
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := a.call(ctx, assistantwire.MethodStart, p, &res); err != nil {
		return nil, err
	}
	return &res.Session, nil
}

// SendParams fills params (a message for the assistant's conversation session on machine) with what the assistant
// runs it on (assistant.send_params).
func (a assistantRemote) SendParams(ctx context.Context, machine, session string, params map[string]any) (map[string]any, error) {
	var res struct {
		Params       map[string]any `json:"params"`
		LauncherNote string         `json:"launcher_note"` // the main chat ran on another launcher, and why (#4659)
	}
	who := principalOf(ctx) // a person's own app: a full device's credential (secret_handoff.go)
	err := a.call(ctx, assistantwire.MethodSendParams, assistantwire.SendParams{MachineID: machine, SessionID: session, Params: params,
		Lang: string(a.h.contextLang(ctx)), Human: who.Role == roleFull && who.ID != ""}, &res)
	if err != nil {
		return nil, err
	}
	if res.Params == nil {
		res.Params = map[string]any{}
	}
	if res.LauncherNote != "" {
		res.Params[launcherNoteParam] = res.LauncherNote // taken out again by assistantSend: never sent to the node
	}
	return res.Params, nil
}

// launcherNoteParam carries the manager's launcher_note from SendParams to assistantSend.
const launcherNoteParam = "launcher_note"

// Rename has the assistant rename its conversation session on machine (assistant.rename): for the user, or for
// extension ext ("" for anyone else), whose title the assistant takes as a hint.
func (a assistantRemote) Rename(ctx context.Context, machine, session string, params map[string]any, ext string) (json.RawMessage, error) {
	var out json.RawMessage
	err := a.call(ctx, assistantwire.MethodRename, assistantwire.RenameParams{MachineID: machine, SessionID: session, Params: params,
		Extension: ext}, &out)
	return out, err
}

// assistantSend fills p, a message for the assistant's conversation sid on m, as the assistant runs it. note is the
// manager's word that it runs on another launcher than the one configured (#4659), for the app.
func (h *Hub) assistantSend(ctx context.Context, m *Machine, sid string, p map[string]any) (note string, err error) {
	out, err := h.asstOf(h.sessionOwner(m.cfg.ID, sid)).SendParams(ctx, m.cfg.ID, sid, p)
	if err != nil {
		return "", err
	}
	note, _ = out[launcherNoteParam].(string)
	delete(out, launcherNoteParam)
	clear(p)
	for k, v := range out {
		p[k] = v
	}
	return note, nil
}

// withLauncherNote adds the manager's launcher_note to a delivered send's answer (a JSON object), for the app to show.
func withLauncherNote(status int, body []byte, note string) []byte {
	var out map[string]any
	if note == "" || status != http.StatusOK || json.Unmarshal(body, &out) != nil {
		return body
	}
	if out == nil {
		out = map[string]any{}
	}
	out["launcher_note"] = note
	b, err := json.Marshal(out)
	if err != nil {
		return body
	}
	return append(b, '\n')
}

// writeAssistantErr answers an app's request the assistant could not take: 503 assistant_offline while it is not
// connected, 400 when it says the request is wrong, else as a node call's error.
func writeAssistantErr(w http.ResponseWriter, err error) {
	var re *rpc.Error
	if errors.As(err, &re) {
		switch re.Code {
		case assistantwire.CodeAssistantOffline:
			writeErrCode(w, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
			return
		case assistantwire.CodeBadRequest:
			writeErr(w, http.StatusBadRequest, errors.New(re.Message))
			return
		case assistantwire.CodeMeetingEnded: // a message into a meeting's room that ended: write in its follow-up
			writeErrCode(w, http.StatusConflict, assistantwire.CodeMeetingEnded, errors.New(re.Message))
			return
		case assistantwire.CodeMachineOffline: // as the hub's own call to that machine would answer
			callErr(w, fmt.Errorf("%s: %w", re.Message, errOffline))
			return
		case assistantwire.CodeNodeClosed:
			callErr(w, fmt.Errorf("%s: %w", re.Message, rpc.ErrClosed))
			return
		}
	}
	callErr(w, err)
}

// renameSession is POST /api/v1/sessions/{mid}/{sid}/rename ({"title", "tab_name"}): the node renames the tab and /
// or the session; an assistant conversation is the assistant's to rename (assistant.rename), told when an extension's
// token asks, whose title is then only a hint.
func (h *Hub) renameSession(w http.ResponseWriter, r *http.Request, m *Machine) {
	p := map[string]any{}
	if err := decodeJSONBody(w, r, &p, 1<<20); err != nil && !errors.Is(err, io.EOF) {
		writeBodyErr(w, err, err)
		return
	}
	if p == nil {
		writeErr(w, http.StatusBadRequest, errors.New("request body must be a JSON object"))
		return
	}
	sid := r.PathValue("sid")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out := json.RawMessage(`{"ok":true}`)
	owner := h.sessionOwner(m.cfg.ID, sid)
	sess, _ := m.session(sid)
	// Ordinary extension calls keep the existing node authorization boundary.
	// Older managers cannot record ordinary Agent titles; preserve their native path.
	recordAgent := principalOf(r.Context()).Role != roleExtension && h.archiveManagerHas(owner, assistantwire.CapabilityAgentNaming) == http.StatusOK
	if (p["title"] != nil || p["tab_name"] != nil) && (m.isAssistant(sess) || recordAgent) {
		ext := ""
		if pr := principalOf(r.Context()); pr.Role == roleExtension {
			ext = pr.Extension
		}
		res, err := h.asstOf(h.sessionOwner(m.cfg.ID, sid)).Rename(ctx, m.cfg.ID, sid, p, ext)
		if err != nil {
			writeAssistantErr(w, err)
			return
		}
		if len(res) > 0 {
			out = res
		}
	} else if p["title"] != nil || p["tab_name"] != nil {
		p["session_id"] = sid
		if err := m.call(ctx, "session.rename", p, &out); err != nil {
			callErr(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}

// jsonSame: a and b say the same in JSON (a nil profile is not the same as any).
func jsonSame(a, b *assistantwire.Profile) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
