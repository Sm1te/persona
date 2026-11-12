package assistant

import (
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func foldRow(id, state string, set func(*proto.Session)) proto.Session {
	s := proto.Session{SessionID: id, State: state}
	if set != nil {
		set(&s)
	}
	return s
}

func foldOf(list []proto.Session) map[string]string {
	f := foldSessions(list, nil)
	f.apply(list)
	out := map[string]string{}
	for i := range list {
		if u := list[i].FoldedUnder(); u != "" {
			out[list[i].SessionID] = u
		}
	}
	return out
}

func rowNamed(list []proto.Session, id string) *proto.Session {
	for i := range list {
		if list[i].SessionID == id {
			return &list[i]
		}
	}
	return nil
}

// Every child folds under its topmost listed ancestor, however deep, and that ancestor alone carries the rollup of all
// of them by their own states (#5120).
func TestFoldSessionsTwoLevels(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, nil),
		foldRow("mid", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" }),
		foldRow("leaf", proto.StateNeedsYou, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID, s.RootSessionID = true, "mid", "lead" }),
		foldRow("leaf2", proto.StateUnread, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "mid" }),
		foldRow("alone", proto.StateWorking, nil),
	}
	got := foldOf(list)
	if len(got) != 3 || got["mid"] != "lead" || got["leaf"] != "lead" || got["leaf2"] != "lead" {
		t.Fatalf("folded %v", got)
	}
	if c := rowNamed(list, "lead").Children(); c != (proto.SessionChildren{Total: 3, Working: 1, NeedsYou: 1}) {
		t.Fatalf("rollup %+v", c)
	}
	for _, id := range []string{"mid", "leaf", "leaf2", "alone"} {
		if c := rowNamed(list, id).Hierarchy; c != nil && c.Children != nil {
			t.Fatalf("%s carries a rollup: %+v", id, c.Children)
		}
	}
	if s := rowNamed(list, "lead"); s.FoldedState() != proto.StateNeedsYou {
		t.Fatalf("the parent shows %s", s.FoldedState())
	}
}

// A child whose parent is not listed (archived, never listed) is an orphan: it stays a row of its own, unless its
// root is listed. Rows that go round in a cycle fold under nothing; an archived row is in no fold.
func TestFoldSessionsOrphansCyclesArchived(t *testing.T) {
	list := []proto.Session{
		foldRow("archived-lead", proto.StateIdle, func(s *proto.Session) { s.Archived = true }),
		foldRow("orphan", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "archived-lead" }),
		foldRow("gone", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "nobody" }),
		foldRow("rooted", proto.StateIdle, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID, s.RootSessionID = true, "nobody", "top" }),
		foldRow("top", proto.StateIdle, nil),
		foldRow("a", proto.StateIdle, func(s *proto.Session) { s.ParentSessionID = "b" }),
		foldRow("b", proto.StateIdle, func(s *proto.Session) { s.ParentSessionID = "a" }),
		foldRow("into-cycle", proto.StateIdle, func(s *proto.Session) { s.ParentSessionID = "a" }),
		foldRow("self", proto.StateIdle, func(s *proto.Session) { s.ParentSessionID, s.RootSessionID = "self", "self" }),
		foldRow("archived-kid", proto.StateWorking, func(s *proto.Session) { s.Archived, s.IsSubagent, s.ParentSessionID = true, true, "top" }),
	}
	got := foldOf(list)
	if len(got) != 1 || got["rooted"] != "top" {
		t.Fatalf("folded %v", got)
	}
	if c := rowNamed(list, "top").Children(); c != (proto.SessionChildren{Total: 1}) {
		t.Fatalf("an archived child is counted: %+v", c)
	}
}

// A runtime child that waits for the user names its parent in asking_parent_session_id only (the node clears the
// rest for the apps that fold every child away, #4494): it folds under its parent all the same, and its parent shows
// that it needs the user.
func TestFoldSessionsBlockedRuntimeChild(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, func(s *proto.Session) { s.SourceKind = proto.SourceRuntime }),
		foldRow("mid", proto.StateWorking, func(s *proto.Session) {
			s.SourceKind, s.IsSubagent, s.ParentSessionID, s.RootSessionID = proto.SourceRuntime, true, "lead", "lead"
		}),
		foldRow("asks", proto.StateNeedsYou, func(s *proto.Session) {
			s.SourceKind = proto.SourceRuntime
			s.SetAskingParentSessionID("mid")
		}),
	}
	if got := foldOf(list); got["asks"] != "lead" || got["mid"] != "lead" {
		t.Fatalf("folded %v", got)
	}
	lead := rowNamed(list, "lead")
	if c := lead.Children(); c != (proto.SessionChildren{Total: 2, Working: 1, NeedsYou: 1}) || lead.FoldedState() != proto.StateNeedsYou {
		t.Fatalf("rollup %+v, state %s", c, lead.FoldedState())
	}
	if asks := rowNamed(list, "asks"); asks.IsSubtask() {
		t.Fatal("folding changed what counts as a subtask")
	}
}

// A row decorated on its own (a push of one session) keeps what the machine's last complete list folded it as, and a
// card sent to the apps names the session its agent folds under.
func TestFoldSubsetAndInteractions(t *testing.T) {
	s := &Service{}
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, nil),
		foldRow("kid", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" }),
	}
	s.foldInventory("m1", list)
	one := []proto.Session{foldRow("kid", proto.StateNeedsYou, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" })}
	s.foldSubset("m1", one)
	if one[0].FoldedUnder() != "lead" {
		t.Fatalf("a row on its own lost its fold: %+v", one[0].Hierarchy)
	}
	parent := []proto.Session{foldRow("lead", proto.StateIdle, nil)}
	s.foldSubset("m1", parent)
	if parent[0].Children() != (proto.SessionChildren{Total: 1, Working: 1}) {
		t.Fatalf("a parent on its own lost its rollup: %+v", parent[0].Hierarchy)
	}
	other := []proto.Session{{SessionID: "kid"}}
	if s.foldSubset("m2", other); other[0].Hierarchy != nil {
		t.Fatal("another machine's fold applied")
	}
	if ix := s.withTask(proto.Interaction{MachineID: "m1", SessionID: "kid"}); ix.FoldedUnder != "lead" {
		t.Fatalf("card folded under %q", ix.FoldedUnder)
	}
	if ix := s.withTask(proto.Interaction{MachineID: "m1", SessionID: "lead"}); ix.FoldedUnder != "" {
		t.Fatalf("a parent's card folded under %q", ix.FoldedUnder)
	}
	// The next complete list says otherwise: the kid is gone.
	s.foldInventory("m1", []proto.Session{foldRow("lead", proto.StateIdle, nil)})
	if ix := s.withTask(proto.Interaction{MachineID: "m1", SessionID: "kid"}); ix.FoldedUnder != "" {
		t.Fatalf("a stale fold: %q", ix.FoldedUnder)
	}
}

// A row folded the same way twice is not given a new hierarchy: the list's bytes, and the rows the hub keeps, stay.
func TestFoldSessionsStable(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, nil),
		foldRow("kid", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" }),
	}
	foldSessions(list, nil).apply(list)
	lead, kid := list[0].Hierarchy, list[1].Hierarchy
	foldSessions(list, nil).apply(list)
	if list[0].Hierarchy != lead || list[1].Hierarchy != kid {
		t.Fatal("an unchanged fold made new hierarchies")
	}
}

// The list the hub is told carries the fold: the child names the session it folds under, its parent the rollup, and
// both say so again in the next list once the child's state changed (#5120).
func TestPublishedListFoldsChildren(t *testing.T) {
	ft := newFleetTestNoStore(t)
	lead := proto.Session{SessionID: "lead", Status: proto.StatusIdle, Cwd: "/w/app", Agent: "codex"}
	kid := proto.Session{SessionID: "kid", Status: proto.StatusWorking, Cwd: "/w/app", Agent: "codex", IsSubagent: true,
		ParentSessionID: "lead", RootSessionID: "lead"}
	ft.sessions("mac", lead, kid)
	waitFolded := func(want proto.SessionChildren) []proto.Session {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			list := ft.lastList("mac")
			if l, k := rowNamed(list, "lead"), rowNamed(list, "kid"); l != nil && k != nil && k.FoldedUnder() == "lead" && l.Children() == want {
				return list
			}
			if time.Now().After(deadline) {
				t.Fatalf("not folded with %+v: %+v", want, list)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	list := waitFolded(proto.SessionChildren{Total: 1, Working: 1})
	if l := rowNamed(list, "lead"); l.FoldedUnder() != "" || l.FoldedState() != proto.StateWorking {
		t.Fatalf("parent %+v shows %s", l.Hierarchy, l.FoldedState())
	}
	kid.Status = proto.StatusDone
	ft.sessions("mac", lead, kid)
	waitFolded(proto.SessionChildren{Total: 1})
}

// A review's reviewer is no row the apps are shown (publishList leaves it out): it is no parent, and it is not counted in
// a parent's children or the state the parent shows.
func TestFoldLeavesReviewersOut(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, nil),
		foldRow("kid", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" }),
		foldRow("reviewer", proto.StateNeedsYou, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" }),
		foldRow("under-reviewer", proto.StateWorking, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "reviewer" }),
	}
	f := foldSessions(list, map[string]bool{"reviewer": true})
	f.apply(list)
	if got := rowNamed(list, "lead").Children(); got != (proto.SessionChildren{Total: 1, Working: 1}) {
		t.Fatalf("rollup %+v", got)
	}
	if lead := rowNamed(list, "lead"); lead.FoldedState() != proto.StateWorking {
		t.Fatalf("the parent shows %s", lead.FoldedState())
	}
	if r, u := rowNamed(list, "reviewer"), rowNamed(list, "under-reviewer"); r.Hierarchy != nil || u.FoldedUnder() != "" {
		t.Fatalf("reviewer %+v, its child under %q", r.Hierarchy, u.FoldedUnder())
	}

	// The manager reads its reviewers off the to-dos, for the machine it folds only.
	s := &Service{}
	s.todos.todos = map[string]*Todo{"todo_r": {ID: "todo_r", Title: "x",
		Agents: []TodoAgent{{MachineID: "m1", SessionID: "reviewer", Source: sourceReview}}}}
	again := []proto.Session{
		foldRow("lead", proto.StateIdle, nil),
		foldRow("reviewer", proto.StateNeedsYou, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" }),
	}
	s.foldInventory("m1", again)
	if again[0].Hierarchy != nil || s.foldedUnder("m1", "reviewer") != "" {
		t.Fatalf("a reviewer folded: %+v", again[0].Hierarchy)
	}
	other := []proto.Session{again[0], foldRow("reviewer", proto.StateNeedsYou, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" })}
	s.foldInventory("m2", other)
	if s.foldedUnder("m2", "reviewer") != "lead" {
		t.Fatal("another machine's session of the reviewer's id was left out")
	}
}
