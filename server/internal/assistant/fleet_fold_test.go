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
	if under, _ := s.foldedUnder("m1", "reviewer"); again[0].Hierarchy != nil || under != "" {
		t.Fatalf("a reviewer folded: %+v", again[0].Hierarchy)
	}
	other := []proto.Session{again[0], foldRow("reviewer", proto.StateNeedsYou, func(s *proto.Session) { s.IsSubagent, s.ParentSessionID = true, "lead" })}
	s.foldInventory("m2", other)
	if under, _ := s.foldedUnder("m2", "reviewer"); under != "lead" {
		t.Fatal("another machine's session of the reviewer's id was left out")
	}
}

// inTab places a row in pane n of a herdr tab (space, slot); a negative n leaves where it sits unsaid.
func inTab(space, slot string, n int, more ...func(*proto.Session)) func(*proto.Session) {
	return func(s *proto.Session) {
		s.SpaceKind, s.SpaceID, s.SlotID = "herdr", space, slot
		if n >= 0 {
			s.PaneIndex = &n
		}
		for _, f := range more {
			f(s)
		}
	}
}

func foldedAs(list []proto.Session) map[string]string {
	out := map[string]string{}
	for i := range list {
		if a := list[i].FoldedAs(); a != "" {
			out[list[i].SessionID] = a
		}
	}
	return out
}

// A tab's lead is the Agent in its first pane; every other running Agent of the tab is its worker, folded under it and
// marked so, and the lead counts them, by their own states, in its rollup (#5180). Another tab, a row with no tab and
// a row in a tab of another space kind fold nowhere.
func TestFoldSessionsTabTeam(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, inTab("w1", "t1", 0)),
		foldRow("busy", proto.StateWorking, inTab("w1", "t1", 1)),
		foldRow("asks", proto.StateNeedsYou, inTab("w1", "t1", 2)),
		foldRow("next-tab", proto.StateWorking, inTab("w1", "t2", 1)),
		foldRow("other-kind", proto.StateWorking, inTab("w1", "t1", 3, func(s *proto.Session) { s.SpaceKind = "orca" })),
		foldRow("no-tab", proto.StateWorking, nil),
	}
	if got := foldOf(list); len(got) != 2 || got["busy"] != "lead" || got["asks"] != "lead" {
		t.Fatalf("folded %v", got)
	}
	if got := foldedAs(list); len(got) != 2 || got["busy"] != proto.FoldedAsWorker || got["asks"] != proto.FoldedAsWorker {
		t.Fatalf("folded as %v", got)
	}
	lead := rowNamed(list, "lead")
	if c := lead.Children(); c != (proto.SessionChildren{Total: 2, Working: 1, NeedsYou: 1, Workers: 2}) || lead.FoldedState() != proto.StateNeedsYou {
		t.Fatalf("rollup %+v, state %s", c, lead.FoldedState())
	}
	if lead.FoldedUnder() != "" || lead.FoldedAs() != "" {
		t.Fatalf("the lead folds: %+v", lead.Hierarchy)
	}
	if busy := rowNamed(list, "busy"); busy.IsSubtask() || busy.Children() != (proto.SessionChildren{}) {
		t.Fatalf("a worker is a subtask or carries a rollup: %+v", busy.Hierarchy)
	}
}

// A tab folds only with exactly one lead: its first pane a shell (no Agent there), two rows claiming the first pane,
// an offline or hidden Agent there, or a first pane that is a child's, leave its Agents peers (#5180).
func TestFoldSessionsTabWithoutLead(t *testing.T) {
	for name, lead := range map[string][]proto.Session{
		"shell first": nil,
		"two firsts": {
			foldRow("one", proto.StateIdle, inTab("w1", "t1", 0)),
			foldRow("two", proto.StateIdle, inTab("w1", "t1", 0)),
		},
		"offline lead":  {foldRow("lead", proto.StateOffline, inTab("w1", "t1", 0))},
		"reviewer lead": {foldRow("reviewer", proto.StateIdle, inTab("w1", "t1", 0))},
		"child first": {foldRow("kid", proto.StateWorking, inTab("w1", "t1", 0, func(s *proto.Session) {
			s.IsSubagent, s.ParentSessionID = true, "elsewhere"
		}))},
		"assistant first": {foldRow("chat", proto.StateIdle, inTab("w1", "t1", 0, func(s *proto.Session) { s.Assistant = true }))},
		"pane unknown":    {foldRow("lead", proto.StateIdle, inTab("w1", "t1", -1))},
	} {
		list := append(lead,
			foldRow("a", proto.StateWorking, inTab("w1", "t1", 1)),
			foldRow("b", proto.StateNeedsYou, inTab("w1", "t1", 2)),
		)
		f := foldSessions(list, map[string]bool{"reviewer": true})
		f.apply(list)
		for i := range list {
			if list[i].Hierarchy != nil {
				t.Fatalf("%s: %s folds %+v", name, list[i].SessionID, list[i].Hierarchy)
			}
		}
	}
}

// A stopped pane has left the team: it stays a row of its own and is not counted. An assistant row in the tab is in
// no team either (#5180).
func TestFoldSessionsTabLeavesOfflineAndAssistantOut(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateWorking, inTab("w1", "t1", 0)),
		foldRow("stopped", proto.StateOffline, inTab("w1", "t1", 1)),
		foldRow("chat", proto.StateNeedsYou, inTab("w1", "t1", 2, func(s *proto.Session) { s.Assistant = true })),
		foldRow("worker", proto.StateIdle, inTab("w1", "t1", 3)),
	}
	if got := foldOf(list); len(got) != 1 || got["worker"] != "lead" {
		t.Fatalf("folded %v", got)
	}
	if c := rowNamed(list, "lead").Children(); c != (proto.SessionChildren{Total: 1, Workers: 1}) {
		t.Fatalf("rollup %+v", c)
	}
}

// A worker's child (a Codex subagent it started) folds under the worker's lead, its topmost listed ancestor, with no
// folded_as of its own, and the lead counts it beside its workers. A child sitting in the tab is no worker: it keeps
// folding by its parent (#5180).
func TestFoldSessionsTabWorkerChild(t *testing.T) {
	list := []proto.Session{
		foldRow("lead", proto.StateIdle, inTab("w1", "t1", 0)),
		foldRow("worker", proto.StateIdle, inTab("w1", "t1", 1)),
		foldRow("other", proto.StateWorking, inTab("w1", "t1", 2)),
		foldRow("sub", proto.StateWorking, func(s *proto.Session) {
			s.Agent, s.IsSubagent, s.ParentSessionID, s.RootSessionID = "codex", true, "worker", "worker"
		}),
		foldRow("solo", proto.StateIdle, nil),
		foldRow("kid-in-tab", proto.StateNeedsYou, inTab("w1", "t1", 3, func(s *proto.Session) {
			s.IsSubagent, s.ParentSessionID = true, "solo"
		})),
	}
	got := foldOf(list)
	if len(got) != 4 || got["worker"] != "lead" || got["other"] != "lead" || got["sub"] != "lead" || got["kid-in-tab"] != "solo" {
		t.Fatalf("folded %v", got)
	}
	if as := foldedAs(list); len(as) != 2 || as["worker"] != proto.FoldedAsWorker || as["other"] != proto.FoldedAsWorker {
		t.Fatalf("folded as %v", as)
	}
	if c := rowNamed(list, "lead").Children(); c != (proto.SessionChildren{Total: 3, Working: 2, Workers: 2}) {
		t.Fatalf("lead rollup %+v", c)
	}
	if c := rowNamed(list, "solo").Children(); c != (proto.SessionChildren{Total: 1, NeedsYou: 1}) {
		t.Fatalf("solo rollup %+v", c)
	}
	if rowNamed(list, "worker").Children() != (proto.SessionChildren{}) {
		t.Fatal("a worker carries its child's rollup")
	}
}

// A worker pushed on its own keeps folded_as, its lead the workers count, and a card a worker asks names its lead and
// that it comes from a worker; the next complete list without the team drops both (#5180).
func TestFoldSubsetAndInteractionsTabTeam(t *testing.T) {
	s := &Service{}
	team := []proto.Session{
		foldRow("lead", proto.StateIdle, inTab("w1", "t1", 0)),
		foldRow("worker", proto.StateWorking, inTab("w1", "t1", 1)),
	}
	s.foldInventory("m1", team)
	one := []proto.Session{foldRow("worker", proto.StateNeedsYou, inTab("w1", "t1", 1))}
	s.foldSubset("m1", one)
	if one[0].FoldedUnder() != "lead" || one[0].FoldedAs() != proto.FoldedAsWorker {
		t.Fatalf("a worker on its own lost its fold: %+v", one[0].Hierarchy)
	}
	lead := []proto.Session{foldRow("lead", proto.StateIdle, inTab("w1", "t1", 0))}
	if s.foldSubset("m1", lead); lead[0].Children() != (proto.SessionChildren{Total: 1, Working: 1, Workers: 1}) {
		t.Fatalf("a lead on its own lost its rollup: %+v", lead[0].Hierarchy)
	}
	if ix := s.withTask(proto.Interaction{MachineID: "m1", SessionID: "worker"}); ix.FoldedUnder != "lead" || ix.FoldedAs != proto.FoldedAsWorker {
		t.Fatalf("a worker's card: under %q as %q", ix.FoldedUnder, ix.FoldedAs)
	}
	if ix := s.withTask(proto.Interaction{MachineID: "m1", SessionID: "lead"}); ix.FoldedUnder != "" || ix.FoldedAs != "" {
		t.Fatalf("the lead's card: under %q as %q", ix.FoldedUnder, ix.FoldedAs)
	}
	s.foldInventory("m1", []proto.Session{
		foldRow("lead", proto.StateIdle, inTab("w1", "t1", 0)),
		foldRow("worker", proto.StateOffline, inTab("w1", "t1", 1)),
	})
	if ix := s.withTask(proto.Interaction{MachineID: "m1", SessionID: "worker"}); ix.FoldedUnder != "" || ix.FoldedAs != "" {
		t.Fatalf("a stopped worker's card: under %q as %q", ix.FoldedUnder, ix.FoldedAs)
	}
}

// The list the hub is told folds a tab's workers under its lead, from the rows the node sends (#5180).
func TestPublishedListFoldsTabTeam(t *testing.T) {
	ft := newFleetTestNoStore(t)
	zero, one := 0, 1
	lead := proto.Session{SessionID: "lead", Status: proto.StatusIdle, Cwd: "/w/app", Agent: "claude",
		SpaceKind: "herdr", SpaceID: "w1", SlotID: "t1", PaneIndex: &zero}
	worker := proto.Session{SessionID: "worker", Status: proto.StatusWorking, Cwd: "/w/app", Agent: "codex",
		SpaceKind: "herdr", SpaceID: "w1", SlotID: "t1", PaneIndex: &one}
	ft.sessions("mac", lead, worker)
	var l *proto.Session
	waitFor(t, "the team folded", func() bool {
		list := ft.lastList("mac")
		w := rowNamed(list, "worker")
		l = rowNamed(list, "lead")
		return l != nil && w != nil && w.FoldedUnder() == "lead" && w.FoldedAs() == proto.FoldedAsWorker &&
			l.Children() == (proto.SessionChildren{Total: 1, Working: 1, Workers: 1})
	})
	if l.FoldedState() != proto.StateWorking {
		t.Fatalf("the lead shows %s", l.FoldedState())
	}
}
