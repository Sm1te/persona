package assistant

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Archiving closed sessions by themselves (agent_archive_auto.go).

func TestAutoArchivable(t *testing.T) {
	now := time.Now()
	idle := 3 * 24 * time.Hour
	old := now.Add(-4 * 24 * time.Hour)
	wake := now.Add(time.Hour)
	type link struct {
		open, onTodo, reviewer bool
		lastDone               time.Time
	}
	none := link{}
	doneLong := link{onTodo: true, lastDone: old}
	cases := []struct {
		name string
		edit func(*proto.Session)
		link link
		idle time.Duration
		want bool
	}{
		{"closed, idle, on no to-do", func(*proto.Session) {}, none, idle, true},
		{"closed, idle, its to-dos done", func(*proto.Session) {}, doneLong, idle, true},
		{"unread result", func(s *proto.Session) { s.State = proto.StateUnread }, doneLong, idle, true},
		{"asks something, its to-dos done", func(s *proto.Session) { s.AwaitsReply = true }, doneLong, idle, true},
		{"work done", func(s *proto.Session) { s.Work = &proto.WorkState{Phase: proto.WorkDone} }, none, idle, true},
		// The shapes closed sessions have in a real list: no host and no pane, whatever the status says.
		{"hostless, offline / offline", func(s *proto.Session) { s.Status, s.State = proto.StatusOffline, proto.StateOffline }, doneLong, idle, true},
		{"hostless, interrupted / unknown (cut off by a node restart)", func(s *proto.Session) { s.Status, s.State = proto.StatusInterrupted, proto.StateUnknown }, doneLong, idle, true},
		{"hostless, idle / idle", func(s *proto.Session) { s.Status, s.State = proto.StatusIdle, proto.StateIdle }, doneLong, idle, true},
		{"hostless, unknown / unknown", func(s *proto.Session) { s.Status, s.State = proto.StatusUnknown, proto.StateUnknown }, doneLong, idle, true},
		{"hostless, interrupted, on no to-do", func(s *proto.Session) { s.Status, s.State = proto.StatusInterrupted, proto.StateUnknown }, none, idle, true},

		{"switched off", func(*proto.Session) {}, none, 0, false},
		{"active two days ago", func(s *proto.Session) { s.UpdatedAt = now.Add(-2 * 24 * time.Hour) }, none, idle, false},
		{"its to-do done two days ago", func(*proto.Session) {}, link{onTodo: true, lastDone: now.Add(-2 * 24 * time.Hour)}, idle, false},
		{"restored by the user yesterday", func(s *proto.Session) { s.ArchiveChangedAt = archiveChangedAt(now.Add(-24 * time.Hour)) }, none, idle, false},
		{"no last step known", func(s *proto.Session) { s.UpdatedAt = time.Time{} }, none, idle, false},
		{"archived already", func(s *proto.Session) { s.Archived = true }, none, idle, false},
		{"linked to an open to-do", func(*proto.Session) {}, link{open: true, onTodo: true}, idle, false},
		{"linked to an open to-do and a done one", func(*proto.Session) {}, link{open: true, onTodo: true, lastDone: old}, idle, false},
		{"assistant conversation", func(s *proto.Session) { s.Assistant = true }, none, idle, false},
		{"a review's reviewer", func(*proto.Session) {}, link{reviewer: true, onTodo: true, lastDone: old}, idle, false},
		{"a subagent's row", func(s *proto.Session) { s.IsSubagent = true }, none, idle, false},
		{"another tool's session", func(s *proto.Session) { s.SourceKind = "codex_app_server" }, none, idle, false},
		{"still open in a tab", func(s *proto.Session) { s.Status, s.PaneID = proto.StatusIdle, "w1:p1" }, none, idle, false},
		{"a tab without a status", func(s *proto.Session) { s.PaneID = "w1:p1" }, none, idle, false},
		{"working", func(s *proto.Session) { s.Status, s.State = proto.StatusWorking, proto.StateWorking }, none, idle, false},
		{"working by its state", func(s *proto.Session) { s.State = proto.StateWorking }, none, idle, false},
		{"working in the background", func(s *proto.Session) { s.BackgroundWorking = true }, none, idle, false},
		{"state unknown in a pane", func(s *proto.Session) { s.Status, s.State, s.PaneID = proto.StatusUnknown, proto.StateUnknown, "w1:p1" }, none, idle, false},
		{"state unknown in a host", func(s *proto.Session) { s.Status, s.State, s.Host = proto.StatusUnknown, proto.StateUnknown, "herdr" }, none, idle, false},
		{"idle in a host without a pane", func(s *proto.Session) { s.Status, s.State, s.Host = proto.StatusIdle, proto.StateIdle, "paseo" }, none, idle, false},
		{"hostless, interrupted, a card waits", func(s *proto.Session) {
			s.Status, s.State, s.PendingInteractions = proto.StatusInterrupted, proto.StateNeedsYou, 1
		}, doneLong, idle, false},
		{"hostless, interrupted, linked to an open to-do", func(s *proto.Session) { s.Status, s.State = proto.StatusInterrupted, proto.StateUnknown },
			link{open: true, onTodo: true}, idle, false},
		{"hostless, blocked", func(s *proto.Session) { s.Status = proto.StatusBlocked }, none, idle, false},
		{"another tool's, idle without a pane", func(s *proto.Session) {
			s.SourceKind, s.Status, s.State = "codex_app_server", proto.StatusIdle, proto.StateIdle
		}, none, idle, false},
		{"starting", func(s *proto.Session) { s.Starting = true }, none, idle, false},
		{"a card waits", func(s *proto.Session) { s.PendingInteractions = 1 }, doneLong, idle, false},
		{"needs you", func(s *proto.Session) { s.State = proto.StateNeedsYou }, doneLong, idle, false},
		{"queued input", func(s *proto.Session) { s.Queued = []string{"next"} }, doneLong, idle, false},
		{"background shell", func(s *proto.Session) { s.Background = []proto.BackgroundTask{{}} }, none, idle, false},
		{"subagent running", func(s *proto.Session) { s.Subagents = []proto.Subagent{{Running: true}} }, none, idle, false},
		{"workflow running", func(s *proto.Session) { s.Workflows = []proto.Workflow{{TaskID: "w", Running: true}} }, none, idle, false},
		{"loop", func(s *proto.Session) { s.Loop = &proto.LoopInfo{} }, none, idle, false},
		{"serving a port", func(s *proto.Session) { s.Ports = []proto.Port{{}} }, none, idle, false},
		{"a wake is due", func(s *proto.Session) { s.Work = &proto.WorkState{Phase: proto.WorkDone, WakeAt: &wake} }, doneLong, idle, false},
		{"waits on the user's decision", func(s *proto.Session) { s.Work = &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser} }, doneLong, idle, false},
		{"waits for CI", func(s *proto.Session) { s.Work = &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal} }, doneLong, idle, false},
		{"asks something, on no to-do", func(s *proto.Session) { s.AwaitsReply = true }, none, idle, false},
	}
	for _, c := range cases {
		s := closedAgent(old)
		s.LastText = "done"
		c.edit(&s)
		if got := autoArchivable(s, now, c.idle, c.link.open, c.link.onTodo, c.link.reviewer, c.link.lastDone); got != c.want {
			t.Errorf("%s: autoArchivable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestArchiveClosedAfterSetting(t *testing.T) {
	ctx := context.Background()
	s, _ := newTaskService(t)
	if got := s.archiveClosedAfter(ctx); got != 3*24*time.Hour {
		t.Fatalf("default = %v", got)
	}
	for _, c := range []struct {
		days int
		want time.Duration
	}{{0, 0}, {7, 7 * 24 * time.Hour}, {-1, 3 * 24 * time.Hour}} {
		d := c.days
		setSettings(t, s, func(v *Settings) { v.ArchiveClosedDays = &d })
		if got := s.archiveClosedAfter(ctx); got != c.want {
			t.Errorf("days %d = %v, want %v", c.days, got, c.want)
		}
	}
	if v := s.view(ctx, false); v.ArchiveClosedDays != 3 {
		t.Fatalf("view says %d after an invalid value", v.ArchiveClosedDays)
	}
}

// What the to-dos say decides the candidates: the closed agent of an open task is kept however long it is idle, taken
// once its task has been done for long enough, and a review's reviewer never; off means none.
func TestAutoArchiveCandidatesFollowTheTodos(t *testing.T) {
	ctx := context.Background()
	s, f := newTaskService(t)
	todo := startedTask(t, s, f) // its agent is s1 of an open task
	old := time.Now().Add(-10 * 24 * time.Hour)
	free := closedAgent(old)
	free.SessionID = "free"
	reviewer := closedAgent(old)
	reviewer.SessionID = "rv"
	open := closedAgent(old)
	open.SessionID, open.Status, open.PaneID = "tab", proto.StatusIdle, "w2:p1"
	f.setSessions("box", closedAgent(old), free, reviewer, open)
	s.todos.mu.Lock() // the reviewer's link, as linkReviewer leaves it: released, source review
	at := time.Now()
	s.todos.todos[todo.ID].Agents = append(s.todos.todos[todo.ID].Agents,
		TodoAgent{MachineID: "box", SessionID: "rv", Source: sourceReview, ReleasedAt: &at})
	s.todos.mu.Unlock()

	ids := func(now time.Time) []string {
		var out []string
		for _, k := range s.autoArchiveCandidates(ctx, now) {
			out = append(out, k[1])
		}
		slices.Sort(out)
		return out
	}
	if got := ids(time.Now()); !slices.Equal(got, []string{"free"}) {
		t.Fatalf("candidates with the task open: %v", got)
	}
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Time{}); err != nil || !ok {
		t.Fatalf("auto done: %v %v", ok, err)
	}
	if got := ids(time.Now()); !slices.Equal(got, []string{"free"}) {
		t.Fatalf("candidates with the task done just now: %v", got)
	}
	if got := ids(time.Now().Add(4 * 24 * time.Hour)); !slices.Equal(got, []string{"free", "s1"}) {
		t.Fatalf("candidates three days after the task was done: %v", got)
	}
	off := 0
	setSettings(t, s, func(v *Settings) { v.ArchiveClosedDays = &off })
	if got := ids(time.Now().Add(4 * 24 * time.Hour)); len(got) != 0 {
		t.Fatalf("candidates while off: %v", got)
	}
}

// The pass archives a closed session idle for days with the apps' own archive (listed under Archived, restored by new
// activity), leaves the ones still open or recently active, and does nothing while off.
func TestClosedSessionArchivesItself(t *testing.T) {
	ctx := context.Background()
	ft := newFleetTest(t)
	ft.f.mu.Lock()
	ft.f.sessions["mac"] = nil // the fake hub reaches mac's node
	ft.f.mu.Unlock()
	if err := ft.s.ensureTodosLoaded(ctx); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * 24 * time.Hour).UTC()
	row := func(id string, at time.Time) proto.Session {
		return proto.Session{MachineID: "mac", SessionID: id, Agent: "claude", Title: id, Status: proto.StatusOffline,
			State: proto.StateOffline, StartedByPersona: true, UpdatedAt: at}
	}
	open := row("open", old)
	open.Status, open.State, open.PaneID = proto.StatusIdle, proto.StateIdle, "w1:p1"
	asked := row("asked", old)
	asked.Queued = []string{"next"} // input waits to go in
	cut := row("cut-off", old)      // closed, as a node restart leaves it: interrupted, which the list reads as unknown
	cut.Status, cut.State = proto.StatusInterrupted, ""
	list := []proto.Session{row("closed-old", old), row("closed-new", time.Now().Add(-time.Hour).UTC()), open, asked, cut}
	ft.d.setSessions("mac", slices.Clone(list))

	off, days := 0, 3
	setSettings(t, ft.s, func(v *Settings) { v.ArchiveClosedDays = &off })
	if n := ft.s.archiveClosedSessions(ctx, time.Now()); n != 0 || len(archivedIDs(t, ft.s)) != 0 {
		t.Fatalf("archived %d while off: %v", n, archivedIDs(t, ft.s))
	}
	setSettings(t, ft.s, func(v *Settings) { v.ArchiveClosedDays = &days })
	for _, c := range ft.s.decorated("mac") {
		if c.SessionID == "cut-off" && c.State != proto.StateUnknown {
			t.Fatalf("an interrupted row reads %q in the list, not unknown: the case this test is for", c.State)
		}
	}
	if n := ft.s.archiveClosedSessions(ctx, time.Now()); n != 2 {
		t.Fatalf("archived %d sessions, want 2", n)
	}
	if got := archivedIDs(t, ft.s); len(got) != 2 || !got["closed-old"] || !got["cut-off"] {
		t.Fatalf("archived: %v", got)
	}
	if n := ft.s.archiveClosedSessions(ctx, time.Now()); n != 0 {
		t.Fatalf("archived %d more on the next pass", n)
	}
	// A message to it resumes it: new activity restores it, and the pass does not take it straight back.
	list[0].UpdatedAt = time.Now().UTC().Add(time.Second)
	ft.d.setSessions("mac", slices.Clone(list))
	if archivedIDs(t, ft.s)["closed-old"] {
		t.Fatal("new activity did not restore the archive")
	}
	if n := ft.s.archiveClosedSessions(ctx, time.Now().Add(2*time.Second)); n != 0 {
		t.Fatalf("archived %d right after it was restored", n)
	}
}
