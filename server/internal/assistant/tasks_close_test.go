package assistant

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Closing the agents of finished tasks (tasks_close.go).

// idleAgent is s1 as a finished agent in a pane Persona started, last active at.
func idleAgent(at time.Time) proto.Session {
	return proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle, PaneID: "w1:p1",
		StartedByPersona: true, UpdatedAt: at, LastText: "做完了"}
}

func (f *fakeHub) retires() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.retired...)
}

// A done task's agent closes once it has been still long enough; one working, waiting for the user, running
// something in the background, or not running is left as it is.
func TestRetirable(t *testing.T) {
	now := time.Now()
	done := now.Add(-time.Hour)
	old := now.Add(-time.Hour)
	cases := []struct {
		name string
		edit func(*proto.Session)
		done time.Time
		want bool
	}{
		{"idle long enough", func(*proto.Session) {}, done, true},
		{"done, unread", func(s *proto.Session) { s.State = proto.StateUnread }, done, true},
		{"codex", func(s *proto.Session) { s.Agent = "codex" }, done, true},
		{"task done just now", func(*proto.Session) {}, now.Add(-time.Second), false},
		{"active just now", func(s *proto.Session) { s.UpdatedAt = now.Add(-time.Second) }, done, false},
		{"working", func(s *proto.Session) { s.Status = proto.StatusWorking }, done, false},
		{"blocked", func(s *proto.Session) { s.Status = proto.StatusBlocked }, done, false},
		{"unknown status", func(s *proto.Session) { s.Status = proto.StatusUnknown }, done, false},
		{"needs you", func(s *proto.Session) { s.State = proto.StateNeedsYou }, done, false},
		{"pending question", func(s *proto.Session) { s.PendingInteractions = 1 }, done, false},
		{"queued", func(s *proto.Session) { s.Queued = []string{"next"} }, done, false},
		{"background shell", func(s *proto.Session) { s.Background = []proto.BackgroundTask{{}} }, done, false},
		{"subagent running", func(s *proto.Session) { s.Subagents = []proto.Subagent{{Running: true}} }, done, false},
		{"subagent finished", func(s *proto.Session) { s.Subagents = []proto.Subagent{{}} }, done, true},
		{"workflow running", func(s *proto.Session) { s.Workflows = []proto.Workflow{{TaskID: "w", Running: true}} }, done, false},
		{"workflow ended", func(s *proto.Session) { s.Workflows = []proto.Workflow{{TaskID: "w", Status: proto.WorkflowCompleted}} }, done, true},
		{"loop", func(s *proto.Session) { s.Loop = &proto.LoopInfo{} }, done, false},
		{"serving a port", func(s *proto.Session) { s.Ports = []proto.Port{{}} }, done, false},
		{"offline", func(s *proto.Session) { s.Status, s.PaneID = proto.StatusOffline, "" }, done, false},
		{"starting", func(s *proto.Session) { s.Starting = true }, done, false},
		{"assistant", func(s *proto.Session) { s.Assistant = true }, done, false},
		{"another tool's", func(s *proto.Session) { s.SourceKind = "codex_app_server" }, done, false},
		{"not started by the hub", func(s *proto.Session) { s.StartedByPersona = false }, done, false},
		{"a runtime's agent", func(s *proto.Session) { s.Agent = "dsh" }, done, false},
	}
	for _, c := range cases {
		s := idleAgent(old)
		c.edit(&s)
		if got := retirable(s, c.done, now); got != c.want {
			t.Errorf("%s: retirable = %v, want %v", c.name, got, c.want)
		}
	}
}

// A task found done: its agent, still for long enough, is stopped on its machine and keeps its terminal; the todo
// keeps it.
func TestDoneTaskAgentIsClosed(t *testing.T) {
	ctx := context.Background()
	s, f := newTaskService(t)
	todo := startedTask(t, s, f)
	f.setSessions("box", idleAgent(time.Now().Add(-time.Hour)))
	refused := taskCloseRefused{}

	s.closeDoneTaskAgents(ctx, refused)
	if got := f.retires(); len(got) != 0 {
		t.Fatalf("closed the agent of an open task: %v", got)
	}
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "做完了", time.Time{}); err != nil || !ok {
		t.Fatalf("auto done: %v %v", ok, err)
	}
	s.closeDoneTaskAgents(ctx, refused) // done just now: not yet
	if got := f.retires(); len(got) != 0 {
		t.Fatalf("closed at once: %v", got)
	}
	old := taskCloseIdle
	taskCloseIdle = 0
	t.Cleanup(func() { taskCloseIdle = old })
	s.closeDoneTaskAgents(ctx, refused)
	if got := f.retires(); len(got) != 1 || got[0] != "s1" {
		t.Fatalf("retired %v", got)
	}
	f.mu.Lock()
	args := f.retireArgs[0]
	f.mu.Unlock()
	if args["method"] != deviceSessionStop || args["retire"] != true { // its terminal stays (#4754)
		t.Fatalf("asked %v", args)
	}
	if a := todoNow(s, todo.ID).Agents; len(a) != 1 || a[0].SessionID != "s1" || a[0].ReleasedAt != nil {
		t.Fatalf("the todo lost its agent: %+v", a)
	}

	// Closed, it is offline: not asked again.
	c := idleAgent(time.Now().Add(-time.Hour))
	c.Status, c.PaneID = proto.StatusOffline, ""
	f.setSessions("box", c)
	s.closeDoneTaskAgents(ctx, refused)
	if got := f.retires(); len(got) != 1 {
		t.Fatalf("asked again: %v", got)
	}
}

// A node that will not stop an agent (another tool owns it, or the node is too old to know session.stop) is not asked
// again every pass, only once the agent changed or a while later (the node may have been updated since).
func TestRefusedRetireIsNotRepeated(t *testing.T) {
	ctx := context.Background()
	s, f := newTaskService(t)
	todo := startedTask(t, s, f)
	old := taskCloseIdle
	taskCloseIdle = 0
	t.Cleanup(func() { taskCloseIdle = old })
	f.mu.Lock()
	f.retireErr = &rpc.Error{Code: "not_retirable", Message: "this session belongs to another tool"}
	f.mu.Unlock()
	asked := 0
	count := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		n := 0
		for _, c := range f.calls {
			if c.Method == deviceSessionStop {
				n++
			}
		}
		return n
	}
	at := time.Now().Add(-time.Hour)
	f.setSessions("box", idleAgent(at))
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "做完了", time.Time{}); err != nil || !ok {
		t.Fatalf("auto done: %v %v", ok, err)
	}
	refused := taskCloseRefused{}
	s.closeDoneTaskAgents(ctx, refused)
	s.closeDoneTaskAgents(ctx, refused)
	if asked = count(); asked != 1 {
		t.Fatalf("asked %d times", asked)
	}
	f.setSessions("box", idleAgent(at.Add(time.Minute))) // it did something since
	s.closeDoneTaskAgents(ctx, refused)
	if asked = count(); asked != 2 {
		t.Fatalf("not asked again after a change: %d", asked)
	}

	// Its node is updated: the refusal it gave before does not stand for ever.
	f.mu.Lock()
	f.retireErr = nil
	f.mu.Unlock()
	s.closeDoneTaskAgents(ctx, refused)
	if asked = count(); asked != 2 {
		t.Fatalf("asked again at once: %d", asked)
	}
	oldRetry := taskCloseRetry
	taskCloseRetry = 0
	t.Cleanup(func() { taskCloseRetry = oldRetry })
	s.closeDoneTaskAgents(ctx, refused)
	if got := f.retires(); count() != 3 || len(got) != 1 {
		t.Fatalf("not asked again after a while: %d (%v closed)", count(), got)
	}
	if _, ok := refused[[2]string{"box", "s1"}]; ok {
		t.Fatal("the refusal outlived the close")
	}
}
