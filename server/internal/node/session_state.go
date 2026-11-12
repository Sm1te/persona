package node

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// stateFile remembers which sessions Persona started and with which launcher. It lives in the hub's Postgres:
// loaded with `state.load` when the node starts and written through with `state.put` events. A session in Sessions
// is Persona's (takesReport, close and decorate's StartedByPersona go by that): what the node only observes of the
// user's own sessions never goes in (ultracode.go keeps that in memory, #5510).
type stateFile struct {
	mu       sync.Mutex
	Sessions map[string]sessionState `json:"sessions"`
	persist  func(id string, v sessionState)
	// loading: the hub's copy has yet to come (Serve opens it before loadState runs, which closes it). Until then a
	// session missing from Sessions may still be Persona's. Nil once loaded.
	loading chan struct{}
}

type sessionState struct {
	CreationID       string    `json:"creation_id,omitempty"`
	Incarnation      string    `json:"incarnation,omitempty"`
	Pane             string    `json:"pane,omitempty"`
	Launcher         string    `json:"launcher"`
	Model            string    `json:"model,omitempty"`
	ModelFingerprint string    `json:"model_fingerprint,omitempty"`
	ModelEffort      string    `json:"model_effort,omitempty"`
	Runtime          string    `json:"runtime,omitempty"` // the assistant runtime it was asked to run on (assistant_sdk.go)
	ConfigDir        string    `json:"config_dir,omitempty"`
	Cwd              string    `json:"cwd"`
	IsMain           bool      `json:"is_main,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	// AppMode / AppEffort: the permission mode and effort set on an assistant conversation (modes.go). AppMode
	// also retains a terminal Codex start constraint for later resumes. They
	// win over what its transcript recorded and outlive the node: an old "default" there no longer locks it in.
	// PlanThen: the mode this session, started in plan mode, goes on in once its plan is approved (planthen.go).
	PlanThen  string `json:"plan_then,omitempty"`
	AppMode   string `json:"app_mode,omitempty"`
	AppEffort string `json:"app_effort,omitempty"`
	// Tasks: the assistant conversation's background tasks and monitors running right now (node/assistant.go).
	Tasks []string `json:"tasks,omitempty"`
	// Compaction: the last compaction the user asked for (assistant_compact.go).
	Compaction *proto.Compaction `json:"compaction,omitempty"`
	// Guest: a guest conversation's tools (assistant_guest.go): every process it runs in has no built-in tools and
	// only these of Persona's. Nil is an ordinary conversation.
	Guest []string `json:"guest,omitempty"`
	// GuestKind is a guest conversation's kind: "" for an extension's, guestMeeting for a meeting's room
	// (assistant_guest.go), whose every process loads none of the owner's Claude Code setup either.
	GuestKind string `json:"guest_kind,omitempty"`
	// Ultracode: what was last seen of ultracode in the process Persona runs this session in (ultracode.go). Written
	// by setUltracode only, which never makes a record for a session that has none.
	Ultracode *ultracodeRecord `json:"ultracode_process,omitempty"`
	// PendingSwitch: a switch to another launcher waiting for the next message (launcher_switch.go), kept with the
	// record so a node restart neither loses it nor carries it out without the message. Written by setPendingSwitch,
	// which never makes a record; cleared by the start that carries it out (which writes a new record).
	PendingSwitch *proto.PendingSwitch `json:"pending_switch,omitempty"`
}

func newState() *stateFile { return &stateFile{Sessions: map[string]sessionState{}} }

// legacyState reads the pre-Postgres ~/.persona/sessions.json, if any.
// A corrupt legacy file is quarantined to path.corrupt and logged.
func legacyState(path string, logf func(string, ...any)) map[string]sessionState {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var f struct {
		Sessions map[string]sessionState `json:"sessions"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		corruptPath := path + ".corrupt"
		_ = os.Rename(path, corruptPath)
		if logf != nil {
			logf("legacy sessions file %s is corrupt (quarantined to %s): %v", path, corruptPath, err)
		}
		return nil
	}
	return f.Sessions
}

// merge adds sessions loaded from the hub without overwriting ones touched since the node started.
func (s *stateFile) merge(m map[string]sessionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, v := range m {
		if observationOnly(v) {
			continue // not Persona's, and what it says of the user's session is not kept either (observationOnly)
		}
		if _, ok := s.Sessions[id]; !ok {
			s.Sessions[id] = v
		}
	}
}

// observationOnly: a record holding nothing but an ultracode observation, as a node of #3782 wrote for a session the
// user started. Persona's own records always have a directory and a launcher (checkStart fills both in), and the hub
// gives back a zero start time only for a record stored without one next to an ultracode observation
// (db.PutNodeSession's started_at_unknown). Such a record is dropped when loaded, observation and all: nothing would
// ever write it again (the user's sessions get no record), so a resume notice dismissed now, or a value of a process
// long gone, would come back from it with every node restart. What the node sees of the session from then on stays in
// memory, as for any session the user started.
func observationOnly(v sessionState) bool {
	return v.Ultracode != nil && v.StartedAt.IsZero() && v.Launcher == "" && v.Cwd == ""
}

// beginLoad and endLoad bracket loading the hub's copy (loading).
func (s *stateFile) beginLoad() {
	s.mu.Lock()
	if s.loading == nil {
		s.loading = make(chan struct{})
	}
	s.mu.Unlock()
}

func (s *stateFile) endLoad() {
	s.mu.Lock()
	if s.loading != nil {
		close(s.loading)
		s.loading = nil
	}
	s.mu.Unlock()
}

func (s *stateFile) isLoading() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loading != nil
}

// loaded waits until the hub's copy is loaded (or failed to load: loadState gives up after 20 seconds).
func (s *stateFile) loaded(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	ch := s.loading
	s.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ultracode is the stored ultracode observation of a session Persona started.
func (s *stateFile) ultracode(id string) (ultracodeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Sessions[id]
	if !ok || v.Ultracode == nil {
		return ultracodeRecord{}, false
	}
	return *v.Ultracode, true
}

// setUltracode changes the ultracode observation of a session Persona started and nothing else of its record, under
// the lock, so a copy read before another writer's change is never written back over it. A session without a record
// is not Persona's, or not loaded yet: nothing is written for it (false).
func (s *stateFile) setUltracode(id string, rec ultracodeRecord) bool {
	s.mu.Lock()
	v, ok := s.Sessions[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	v.Ultracode = &rec
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
	return true
}

// setPendingSwitch changes the pending launcher switch of a session Persona started (nil: none) and nothing else of its
// record, under the lock, as setUltracode does; false when the session has no record (not Persona's, or not loaded
// yet): nothing is written for it.
func (s *stateFile) setPendingSwitch(id string, ps *proto.PendingSwitch) bool {
	s.mu.Lock()
	v, ok := s.Sessions[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	if v.PendingSwitch == nil && ps == nil {
		s.mu.Unlock()
		return true
	}
	if ps != nil {
		c := *ps
		ps = &c
	}
	v.PendingSwitch = ps
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
	return true
}

// restoreLaunch puts back what a start that failed to carry out a launcher switch wrote over (start writes a whole new
// record before the agent is up): the launcher, model, directory, start time and plan mode of prev, the record as it
// was, with the rest as it is now (an ultracode observation made since stays), and no pending switch: a failed switch
// is dropped (launcher_switch.go). launcher is the one to put back when there was no record before (the start made one).
func (s *stateFile) restoreLaunch(id string, prev sessionState, had bool, launcher string) {
	s.mu.Lock()
	v, ok := s.Sessions[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	if had {
		v.Launcher, v.Model, v.ModelFingerprint, v.ModelEffort = prev.Launcher, prev.Model, prev.ModelFingerprint, prev.ModelEffort
		v.Cwd, v.StartedAt, v.PlanThen = prev.Cwd, prev.StartedAt, prev.PlanThen
	} else {
		v.Launcher, v.Model, v.ModelFingerprint = launcher, "", ""
	}
	v.PendingSwitch = nil
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
}

// adoptSwitch takes v, what a launcher switch carried out on another Node wrote of session id (switch_share.go), and
// writes it through this Node's hub: the fields the switch owns (launcher, model, directory, start time, plan mode,
// pane, pending switch) over this Node's record, the rest of it as this Node has it (an ultracode observation the new
// claude's hooks made on this connection); v whole when there is no record.
func (s *stateFile) adoptSwitch(id string, v sessionState) {
	s.mu.Lock()
	if cur, ok := s.Sessions[id]; ok {
		cur.Launcher, cur.Model, cur.ModelFingerprint, cur.ModelEffort = v.Launcher, v.Model, v.ModelFingerprint, v.ModelEffort
		cur.Cwd, cur.StartedAt, cur.PlanThen, cur.Pane, cur.PendingSwitch = v.Cwd, v.StartedAt, v.PlanThen, v.Pane, v.PendingSwitch
		v = cur
	}
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
}

func (s *stateFile) get(id string) (sessionState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Sessions[id]
	return v, ok
}

// panes returns the remembered pane of every session (herdr pane ids are not reused while the server lives).
func (s *stateFile) panes() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for id, v := range s.Sessions {
		if v.Pane != "" {
			out[v.Pane] = id
		}
	}
	return out
}

func (s *stateFile) setPane(id, pane string) {
	s.mu.Lock()
	v, ok := s.Sessions[id]
	if !ok || v.Pane == pane {
		s.mu.Unlock()
		return
	}
	v.Pane = pane
	s.Sessions[id] = v
	s.mu.Unlock()
	s.put(id, v)
}

func (s *stateFile) put(id string, v sessionState) {
	s.mu.Lock()
	if old, ok := s.Sessions[id]; ok && old.CreationID != "" {
		v.CreationID, v.Incarnation = old.CreationID, old.Incarnation
	}
	s.Sessions[id] = v
	persist := s.persist
	s.mu.Unlock()
	if persist != nil {
		persist(id, v)
	}
}
