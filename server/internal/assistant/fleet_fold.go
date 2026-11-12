package assistant

import (
	"slices"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- children folded into their parent (docs/protocol/protocol.md "Session hierarchy", #5120) ----

// sessionFold is what folding says of one machine's list: the session each folded child folds under (its topmost
// listed ancestor), and the rollup each such ancestor carries of all the children folded under it.
type sessionFold struct {
	under    map[string]string
	children map[string]*proto.SessionChildren
}

// foldSessions folds a machine's complete list (its rows as decorated, their states computed). Only the rows the apps
// are shown are listed: not an archived Agent (the hub leaves those out, deleted_sessions.go), nor one in hidden (a
// review's reviewer, which publishList leaves out). A child (a subagent, or a session that names a parent or another
// root, proto.Session.IsChild) has as its parent the listed row its parent_session_id names, else the one its
// asking_parent_session_id names, else its root_session_id's; it folds under the topmost listed ancestor that parent
// leads to. An orphan (no listed parent) and a row whose ancestors go round in a cycle fold under nothing. Each
// ancestor that children fold under counts them all, whatever their depth, by their own states.
func foldSessions(list []proto.Session, hidden map[string]bool) sessionFold {
	listed := func(s *proto.Session) bool { return (!s.Archived || s.Assistant) && !hidden[s.SessionID] }
	at := make(map[string]int, len(list))
	for i := range list {
		if _, dup := at[list[i].SessionID]; !dup && listed(&list[i]) {
			at[list[i].SessionID] = i
		}
	}
	parent := func(i int) int {
		s := &list[i]
		if !s.IsChild() {
			return -1
		}
		root := s.RootSessionID
		if root == s.SessionID {
			root = ""
		}
		for _, id := range [...]string{s.ParentSessionID, s.AskingParentSessionID(), root} {
			if j, ok := at[id]; ok && id != "" && j != i {
				return j
			}
		}
		return -1
	}
	var f sessionFold
	var path []int // the rows walked up from one child, to find a cycle
	for i := range list {
		if at[list[i].SessionID] != i || !listed(&list[i]) {
			continue
		}
		top := i
		path = append(path[:0], i)
		for {
			p := parent(top)
			if p < 0 {
				break
			}
			if slices.Contains(path, p) {
				top = i // a cycle: no topmost ancestor
				break
			}
			path = append(path, p)
			top = p
		}
		if top == i {
			continue
		}
		if f.under == nil {
			f.under, f.children = map[string]string{}, map[string]*proto.SessionChildren{}
		}
		root := list[top].SessionID
		f.under[list[i].SessionID] = root
		c := f.children[root]
		if c == nil {
			c = &proto.SessionChildren{}
			f.children[root] = c
		}
		c.Total++
		switch list[i].State {
		case proto.StateWorking:
			c.Working++
		case proto.StateNeedsYou:
			c.NeedsYou++
		}
	}
	return f
}

// apply puts the fold on the rows: what each folds under and the rollup it carries, nothing on the others. A row is
// given a new hierarchy only where it changes.
func (f sessionFold) apply(list []proto.Session) {
	for i := range list {
		id := list[i].SessionID
		list[i].SetFold(f.under[id], f.children[id])
	}
}

// foldInventory folds machine's complete list (foldSessions, without the reviewers publishList hides), puts the fold on
// its rows and keeps it for the rows decorated on their own (foldSubset) and the cards sent to the apps (withTask)
// until the next complete list.
func (s *Service) foldInventory(machine string, list []proto.Session) {
	var hidden map[string]bool
	for k := range s.reviewerSessions() {
		if k[0] == machine {
			if hidden == nil {
				hidden = map[string]bool{}
			}
			hidden[k[1]] = true
		}
	}
	f := foldSessions(list, hidden)
	f.apply(list)
	s.fleet.foldMu.Lock()
	defer s.fleet.foldMu.Unlock()
	if s.fleet.folds == nil {
		s.fleet.folds = map[string]sessionFold{}
	}
	s.fleet.folds[machine] = f
}

// foldSubset puts on rows decorated without the rest of their machine's list what its last complete list folded them
// as: a single row pushed on its own keeps its folded_under and children.
func (s *Service) foldSubset(machine string, list []proto.Session) {
	s.fleet.foldMu.Lock()
	f := s.fleet.folds[machine]
	s.fleet.foldMu.Unlock()
	f.apply(list)
}

// foldedUnder is the session machine's session sid folded under in its last complete list; "" for none.
func (s *Service) foldedUnder(machine, sid string) string {
	s.fleet.foldMu.Lock()
	defer s.fleet.foldMu.Unlock()
	return s.fleet.folds[machine].under[sid]
}
