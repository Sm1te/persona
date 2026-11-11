package assistant

import (
	"context"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the machines, as the devices have them ----

// fleetMachine is one machine as the moved code needs it; manager is the manager this assistant is (Service.id).
type fleetMachine struct {
	ID, Name, Home, DataDir string
	manager                 string
}

// assistantCwd is this manager's assistant directory on the machine, or "" while it has not reported its home.
func (m fleetMachine) assistantCwd() string {
	return stateDirOn(m.Home, m.DataDir, identity.AssistantDirName(m.manager))
}

// isAssistant: sess is one of this manager's assistant's conversations on the machine (isAssistantSession).
func (m fleetMachine) isAssistant(sess proto.Session) bool {
	return isAssistantSession(&sess, m.assistantCwd(), m.manager)
}

// fleetMachines are the machines the devices carry.
func (s *Service) fleetMachines() []fleetMachine {
	if s.devs == nil {
		return nil
	}
	var out []fleetMachine
	for _, d := range s.devs.List() {
		out = append(out, fleetMachine{ID: d.ID, Name: d.Name, Home: d.Home, DataDir: d.DataDir, manager: s.id})
	}
	return out
}

// fleetMachine is the machine id names (an id another one absorbed: that one); false when there is none. The devices
// are asked for that one machine: every list a node sends asks several times, and building every machine's summary
// to pick one read each machine's trust and connection for nothing.
func (s *Service) fleetMachine(id string) (fleetMachine, bool) {
	if s.devs == nil || id == "" {
		return fleetMachine{}, false
	}
	d, ok := s.devs.Summary(id)
	if !ok {
		return fleetMachine{}, false
	}
	return fleetMachine{ID: d.ID, Name: d.Name, Home: d.Home, DataDir: d.DataDir, manager: s.id}, true
}

// machineName is the machine's name, its id when there is none.
func (s *Service) machineName(id string) string {
	if m, ok := s.fleetMachine(id); ok && m.Name != "" {
		return m.Name
	}
	return id
}

// nodeCall calls a machine's node directly (the devices).
func (s *Service) nodeCall(ctx context.Context, machine, method string, params, out any) error {
	if s.devs == nil {
		return ErrNoDevice
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	res, err := s.devs.CallNode(ctx, machine, method, raw)
	if err != nil || out == nil || len(res) == 0 {
		return err
	}
	return json.Unmarshal(res, out)
}

// nodeSession is one of a machine's sessions as its node listed it.
func (s *Service) nodeSession(machine, id string) (proto.Session, bool) {
	if s.devs == nil {
		return proto.Session{}, false
	}
	return s.devs.Session(machine, id)
}

// nodeSessions are a machine's sessions as its node listed them: a copy of the whole list, the caller's own. A caller
// that keeps a few rows, or only asks whether one is there, reads them with nodeSessionsWhere instead.
func (s *Service) nodeSessions(machine string) []proto.Session {
	if s.devs == nil {
		return nil
	}
	list, _ := s.devs.Sessions(machine)
	return list
}

// nodeSessionsWhere are those of a machine's sessions keep accepts, copied out without the rest (Devices.SessionsWhere:
// keep reads each in place, under the devices' lock), and how many its node listed.
func (s *Service) nodeSessionsWhere(machine string, keep func(*proto.Session) bool) (kept []proto.Session, listed int) {
	if s.devs == nil {
		return nil, 0
	}
	kept, _ = s.devs.SessionsWhere(machine, func(sess *proto.Session) bool {
		listed++
		return keep(sess)
	})
	return kept, listed
}

// assistantSessions are this manager's assistant's conversations on machine m (isAssistant), copied out of its node's
// list without the rest: a machine can list thousands of coding sessions beside them.
func (s *Service) assistantSessions(m fleetMachine) []proto.Session {
	if s.devs == nil {
		return nil
	}
	dir := m.assistantCwd()
	list, _ := s.devs.SessionsWhere(m.ID, func(sess *proto.Session) bool { return isAssistantSession(sess, dir, m.manager) })
	return list
}

// placeCard places a card of one of this manager's headless conversations in the conversation's directory, where
// it started (its row), rather than where the hook ran: Claude Code gives a hook the shell's directory after the
// model's `cd`, and a health conversation's card from health/reports or /tmp went to the lock screen and the channels
// in full (#2368). Every node lists a running headless conversation in its start directory, so this holds for older
// nodes too. A hook directory inside a health space stays (identity.CardCwd): a mix-up can only hide more. A card
// with no listed row, or an agent's, keeps its own.
func (s *Service) placeCard(ix *proto.Interaction) {
	if ix.SessionID == "" {
		return
	}
	row, ok := s.nodeSession(ix.MachineID, ix.SessionID)
	if !ok || row.PaneID != "" || row.Cwd == "" {
		return
	}
	if m, ok := s.fleetMachine(ix.MachineID); ok && m.isAssistant(row) {
		ix.Cwd = identity.CardCwd(row.Cwd, ix.Cwd)
	}
}

// isAssistantSession identifies manager's headless conversations in its directory dir, or its health space's. A host
// pane is stronger evidence than a shared working directory. Another manager's directory, left on a machine moved
// away from it, is not this one's (docs/assistant/managers.md "Directories on a computer").
func isAssistantSession(sess *proto.Session, dir, manager string) bool {
	if sess.PaneID != "" || dir == "" || sess.Cwd == "" {
		return false
	}
	// The working directory may go through a symlinked home; runtimes and offline assistants have no host pane.
	return sess.Cwd == dir || identity.IsAssistantDirOf(sess.Cwd, manager)
}
