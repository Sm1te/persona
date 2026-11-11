package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// noticesTo are the texts sent into assistant conversation id.
func (f *fakeHub) noticesTo(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.sends {
		if a["session_id"] == id {
			text, _ := a["text"].(string)
			out = append(out, text)
		}
	}
	return out
}

// newLeaderService is newQuietService with side conversation lead1 made a leader, and a task lead1 handed out with
// task_plan: its agent s1 on box at work.
func newLeaderService(t *testing.T) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f := newQuietService(t)
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title": "修登录崩溃", "prompt": "iPad 横屏时登录页崩溃，修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": true,
		"why": "这个仓库最近在 box 上", "done_when": []string{"PR 合并到 main"}, "expect_minutes": 30,
	}}})
	res, err := s.toolTaskPlan(context.Background(), "lead1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "You supervise them") {
		t.Fatalf("the leader is not told it supervises them: %s", res)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "lead1" || len(v.DoneWhen) != 1 {
		t.Fatalf("supervision = %+v", todo.Supervision)
	}
	return s, f, todo
}

// A leader supervises what it hands out: the supervision message goes to it, not the main conversation; it sends the
// work back and accepts it with task_review. Its needs_user goes up to the main conversation, which can close the task
// without taking it over.
func TestLeaderSupervisesItsTasks(t *testing.T) {
	s, f, todo := newLeaderService(t)
	ctx := context.Background()
	if !strings.Contains(s.Context(ctx, ContextParams{SessionID: "lead1"}).Text, "这是 leader 对话") {
		t.Fatal("the leader's context does not say it is one")
	}
	if strings.Contains(s.Context(ctx, ContextParams{SessionID: "conv1"}).Text, "这是 leader 对话") {
		t.Fatal("the main conversation's context says it is a leader")
	}

	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	waitFor(t, "the leader's message", func() bool { return len(f.noticesTo("lead1")) == 1 })
	n := f.noticesTo("lead1")[0]
	for _, want := range []string{"📋 督办", "你是这些任务的 leader 对话", "hub 判断做完了，等你验收", "pull/7"} {
		if !strings.Contains(n, want) {
			t.Errorf("the leader's message lacks %q:\n%s", want, n)
		}
	}
	if got := f.noticesTo("conv1"); len(got) != 0 {
		t.Fatalf("the main conversation was told too: %q", got)
	}

	review := func(self, verdict, note string, extra map[string]any) (string, error) {
		args := map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": note}
		for k, v := range extra {
			args[k] = v
		}
		return runJSON(t, s, self, "task_review", args)
	}
	if _, err := review("conv2", "rework", "x", nil); err == nil {
		t.Fatal("another side conversation reviewed the leader's task")
	}
	res, err := review("lead1", "rework", "补测试", nil)
	if err != nil || !strings.Contains(res, "Rework round 1") {
		t.Fatalf("the leader's rework: %s %v", res, err)
	}
	if sent := sentToAgent(f); len(sent) != 1 || !strings.Contains(sent[0], "补测试") {
		t.Fatalf("sent to the agent: %q", sent)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "补了测试", 0)
	waitFor(t, "the review again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	waitFor(t, "the leader's second message", func() bool { return len(f.noticesTo("lead1")) == 2 })

	// What the user has to decide goes up to the main conversation too, with the leader's recommendation.
	res, err = review("lead1", "needs_user", "", map[string]any{"reason": "要不要顺手改 iPhone 横屏", "recommend": "先只修 iPad",
		"why_owner": "priority", "cost_of_waiting": "iPad 的修复晚一天发",
		"options": []map[string]string{{"label": "先只修 iPad", "leads_to": "今天就能发"}, {"label": "一起改 iPhone", "leads_to": "多花一天"}}})
	if err != nil || !strings.Contains(res, "waits for the user") {
		t.Fatalf("the leader's needs_user: %s %v", res, err)
	}
	waitFor(t, "the main conversation's message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	n = f.noticesTo("conv1")[0]
	for _, want := range []string{"负责它的 leader 对话判断要用户决定", "要不要顺手改 iPhone 横屏", "leader 推荐：先只修 iPad", "leader 升级上来的"} {
		if !strings.Contains(n, want) {
			t.Errorf("the main conversation's message lacks %q:\n%s", want, n)
		}
	}
	if strings.Contains(n, "你是这些任务的 leader 对话") {
		t.Fatalf("the main conversation is told it is the leader:\n%s", n)
	}

	// The leader closes it; the main conversation could too, and the task stays the leader's.
	res, err = review("lead1", "accepted", "用户选了只修 iPad，已合并", nil)
	if err != nil || !strings.Contains(res, "accepted and closed") {
		t.Fatalf("the leader's accept: %s %v", res, err)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.Supervision.State != reviewAccepted || done.Supervision.Supervisor != "lead1" {
		t.Fatalf("accepted: %+v %+v", done, done.Supervision)
	}
}

// The main conversation reviewing a leader's task (the user answered it there) leaves the task the leader's.
func TestMainReviewKeepsLeaderSupervisor(t *testing.T) {
	s, f, todo := newLeaderService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	waitFor(t, "the review", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	if res, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "补截图"}); err != nil ||
		!strings.Contains(res, "Rework round 1") {
		t.Fatalf("main's rework: %s %v", res, err)
	}
	if v := todoNow(s, todo.ID).Supervision; v.Supervisor != "lead1" {
		t.Fatalf("the main conversation took the leader's task over: %+v", v)
	}
}

// Without leaders nothing changes: a task supervised by a side conversation that is not (or no longer) a leader is
// told in the main conversation, and a side conversation's task_plan task gets no ledger.
func TestDigestGoesToMainWithoutLeader(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	todo, err := s.createTodo(ctx, "甲", "做甲", "lead1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, todo.ID, "lead1", []string{"PR 合并"}, 0)
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	s.signal(superviseSignal{Todo: todo.ID, Kind: sigReview, Key: "1", Detail: "做完了"})
	waitFor(t, "the message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	if n := f.noticesTo("conv1")[0]; strings.Contains(n, "leader") {
		t.Fatalf("the default message changed:\n%s", n)
	}
	s.supervise.dedupMu.Lock()
	s.supervise.dedupMu.Unlock() // the digest send has finished before checking the non-recipient
	if got := f.noticesTo("lead1"); len(got) != 0 {
		t.Fatalf("a conversation that is not a leader was told: %q", got)
	}
	if strings.Contains(s.Context(ctx, ContextParams{SessionID: "lead1"}).Text, "leader") {
		t.Fatal("a side conversation's context says it is a leader")
	}

	// task_plan from a side conversation that is not a leader: no ledger, as before.
	res, err := s.toolTaskPlan(ctx, "lead1", planArgs(true))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res, "You supervise them") {
		t.Fatalf("a side conversation is told it supervises: %s", res)
	}
	var planned Todo
	waitFor(t, "the task", func() bool {
		for _, t := range s.todosOf(ctx, "*") {
			if t.ID != todo.ID {
				planned = t
				return true
			}
		}
		return false
	})
	if planned.Supervision != nil {
		t.Fatalf("a side conversation's task is supervised: %+v", planned.Supervision)
	}
}

// Where each signal goes.
func TestDigestTo(t *testing.T) {
	s, _ := newQuietService(t)
	ctx := context.Background()
	mk := func(supervisor, state string) string {
		todo, err := s.createTodo(ctx, "x", "x", supervisor, "")
		if err != nil {
			t.Fatal(err)
		}
		s.superviseTodo(ctx, todo.ID, supervisor, nil, 0)
		if state != "" {
			if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.State = state; return true }); err != nil {
				t.Fatal(err)
			}
		}
		return todo.ID
	}
	leaders := Settings{Main: "conv1", Leaders: []string{"lead1"}}
	none := Settings{Main: "conv1"}
	mains, leads, waits := mk("conv1", ""), mk("lead1", ""), mk("lead1", reviewNeedsUser)
	for _, c := range []struct {
		name string
		cfg  Settings
		g    superviseSignal
		want string
	}{
		{"main's task", leaders, superviseSignal{Todo: mains, Kind: sigReview}, "conv1"},
		{"leader's task", leaders, superviseSignal{Todo: leads, Kind: sigReview}, "lead1"},
		{"leader's report", leaders, superviseSignal{Todo: leads, Kind: sigReport, Report: upReportQuestion}, "lead1"},
		{"no leaders", none, superviseSignal{Todo: leads, Kind: sigReview}, "conv1"},
		{"leader's task waits for the user", leaders, superviseSignal{Todo: waits, Kind: sigIdle}, "conv1"},
		{"leader's escalation", leaders, superviseSignal{Todo: waits, Kind: sigEscalated}, "conv1"},
		{"leader's failed start", leaders, superviseSignal{Todo: leads, Kind: sigFailed}, "conv1"},
		{"gone", leaders, superviseSignal{Todo: "nope", Kind: sigReview}, "conv1"},
	} {
		if got := s.digestTo(c.cfg, c.g); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// An agent handed out as a task's agent hands out no Persona tasks (task_plan, agent_start), even when it calls them
// as an assistant conversation; the main conversation and leaders are not limited.
func TestWorkerCannotHandOutTasks(t *testing.T) {
	s, _, todo := newSupervisedService(t)
	ctx := context.Background()
	for _, name := range []string{"task_plan", "agent_start"} {
		args := planArgs(true)
		if name == "agent_start" {
			args, _ = json.Marshal(map[string]any{"machine": "box", "cwd": "/code/app", "prompt": "再开一个"})
		}
		res := s.Tool(ctx, ToolCall{Name: name, MachineID: "box", SessionID: "s1", Arguments: args})
		text := ""
		for _, c := range res.Content {
			text += c.Text
		}
		if !res.IsError || !strings.Contains(text, "does not hand out Persona tasks") || !strings.Contains(text, todo.ID) {
			t.Fatalf("%s from the task's agent: %v %s", name, res.IsError, text)
		}
		// As an agent (not an assistant conversation) it has no such tool at all.
		res = s.mcpCall(ctx, "box", "s1", name, args, false)
		if !res.IsError {
			t.Fatalf("%s through the agent's tools went through", name)
		}
	}
	if got := len(s.todosOf(ctx, "*")); got != 1 {
		t.Fatalf("%d todos: the worker handed one out", got)
	}
	for _, self := range []string{"conv1", "lead1"} {
		setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
		if err := s.workerRefusal(ctx, "server", self); err != nil {
			t.Fatalf("%s refused: %v", self, err)
		}
	}
	if err := s.workerRefusal(ctx, "box", "s9"); err != nil {
		t.Fatalf("an agent of no task refused: %v", err)
	}
}

// Cancelling a task stops its agent (session.stop: its terminal stays, #4754), ends the card it waits on and archives
// it (reversible); another task's agent with a card waiting is left as it is.
func TestCancelStopsAndArchivesAgents(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) {
		s.Status, s.State, s.PendingInteractions = proto.StatusBlocked, proto.StateNeedsYou, 1
	})
	other := row
	other.SessionID, other.Title = "other-codex", "other"
	ft.d.setSessions("mac", []proto.Session{row, other})
	ft.f.setSessions("mac", row, other)
	var closed []string
	ft.f.answerNode("session.close", func(_ string, raw json.RawMessage) (any, error) {
		t.Errorf("closed the agent's terminal: %s", raw)
		return map[string]any{"ok": true}, nil
	})
	ft.f.answerNode(deviceSessionStop, func(_ string, raw json.RawMessage) (any, error) {
		var a struct {
			SessionID string `json:"session_id"`
			Retire    bool   `json:"retire"`
		}
		_ = json.Unmarshal(raw, &a)
		if a.Retire {
			t.Errorf("a cancelled task's agent is stopped whatever it does: %s", raw)
		}
		closed = append(closed, a.SessionID)
		gone := row // its node lists it closed a moment later
		gone.Status, gone.State, gone.PendingInteractions, gone.PaneID = proto.StatusOffline, proto.StateOffline, 0, ""
		ft.d.setSessions("mac", []proto.Session{gone, other})
		return map[string]any{"stopped": true}, nil
	})
	s := ft.s
	ctx := context.Background()
	todos := map[string]string{}
	for _, sid := range []string{row.SessionID, other.SessionID} {
		todo, err := s.createTodo(ctx, "做 "+sid, "做", "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.linkTodoAgent(ctx, todo.ID, "mac", sid, sid, "/code", "claude"); err != nil {
			t.Fatal(err)
		}
		todos[sid] = todo.ID
		s.fleet.mu.Lock()
		s.ixMapLocked()["ix-"+sid] = &pending{ix: proto.Interaction{ID: "ix-" + sid, MachineID: "mac", SessionID: sid, Kind: proto.IxPermission,
			ToolName: "Bash", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, done: make(chan proto.Resolution, 1),
			resolved: make(chan struct{})}
		s.fleet.mu.Unlock()
	}
	res, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todos[row.SessionID], "verdict": "cancelled", "note": "不做了"})
	if err != nil || !strings.Contains(res, "cancelled and closed") {
		t.Fatalf("cancel: %s %v", res, err)
	}
	waitFor(t, "the archive", func() bool { return archivedIDs(t, s)[row.SessionID] })
	ft.f.mu.Lock()
	gotClosed := append([]string(nil), closed...)
	ft.f.mu.Unlock()
	if len(gotClosed) != 1 || gotClosed[0] != row.SessionID {
		t.Fatalf("closed: %v", gotClosed)
	}
	if _, waiting := s.pendingIx("ix-" + row.SessionID); waiting {
		t.Fatal("the cancelled task's card still waits")
	}
	if _, waiting := s.pendingIx("ix-" + other.SessionID); !waiting {
		t.Fatal("the other task's card ended")
	}
	if archivedIDs(t, s)[other.SessionID] {
		t.Fatal("the other task's agent was archived")
	}
	if todoNow(s, todos[other.SessionID]).DoneAt != nil {
		t.Fatal("the other task was closed")
	}
	// Reversible: restoring it brings it back to the list.
	if _, err := runJSON(t, s, "conv1", "agent_unarchive", map[string]any{"machine": "mac", "session_id": row.SessionID}); err != nil {
		t.Fatal(err)
	}
	if archivedIDs(t, s)[row.SessionID] {
		t.Fatal("still archived after agent_unarchive")
	}
}

// assistant.leaders takes side conversations only, and says what it is.
func TestSetLeaders(t *testing.T) {
	s, f := newQuietServiceOn(t, newFleetMem())
	ctx := context.Background()
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusIdle})
	if got, _ := s.getLeaders(ctx); !strings.HasPrefix(got, "[]") {
		t.Fatalf("default: %s", got)
	}
	for _, bad := range []string{`["conv1"]`, `["nope"]`, `"lead1"`} {
		if _, err := s.setLeaders(ctx, json.RawMessage(bad)); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if c, err := s.setLeaders(ctx, json.RawMessage(`[]`)); err != nil || c.apply != nil {
		t.Fatalf("no change: %+v %v", c, err)
	}
	c, err := s.setLeaders(ctx, json.RawMessage(`["lead1", "lead1"]`))
	if err != nil || c.apply == nil {
		t.Fatalf("set: %+v %v", c, err)
	}
	if _, err := c.apply(ctx); err != nil {
		t.Fatal(err)
	}
	if !s.isLeader(ctx, "lead1") || s.isLeader(ctx, "conv1") || !s.supervises(ctx, "conv1") || s.supervises(ctx, "conv2") {
		t.Fatal("leaders after set")
	}
	if got, _ := s.getLeaders(ctx); got != `["lead1"]` {
		t.Fatalf("get: %s", got)
	}
}

// A leader whose conversation is gone (archived, deleted) does not swallow its tasks' signals: they go to the main one.
func TestDigestFallsBackWhenLeaderGone(t *testing.T) {
	s, f, todo := newLeaderService(t)
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	waitFor(t, "the main conversation's message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	if n := f.noticesTo("conv1")[0]; !strings.Contains(n, todo.ID) {
		t.Fatalf("message:\n%s", n)
	}
	if got := f.noticesTo("lead1"); len(got) != 0 {
		t.Fatalf("told the gone leader: %q", got)
	}
}

// An agent that works for another open task by now is not closed or archived when the old task is cancelled.
func TestCancelLeavesAnotherTasksAgent(t *testing.T) {
	ft, row := closeTestService(t, nil)
	ft.f.setSessions("mac", row)
	var closed []string
	for _, m := range []string{"session.close", deviceSessionStop} {
		ft.f.answerNode(m, func(_ string, raw json.RawMessage) (any, error) {
			closed = append(closed, string(raw))
			return map[string]any{"ok": true}, nil
		})
	}
	s, ctx := ft.s, context.Background()
	todo, err := s.createTodo(ctx, "新任务", "做", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, todo.ID, "mac", row.SessionID, "open", "/code", "claude"); err != nil {
		t.Fatal(err)
	}
	s.stopCancelledAgent(ctx, "todo_old", "mac", row.SessionID)
	ft.f.mu.Lock()
	n := len(closed)
	ft.f.mu.Unlock()
	if n != 0 || archivedIDs(t, s)[row.SessionID] {
		t.Fatalf("another task's agent was closed (%d) or archived", n)
	}
}

// A cancelled task's agent with no terminal (another tool's, an engine that runs agents itself: the node answers
// session.stop with no_terminal) is closed as before; one the node cannot stop for another reason is left running,
// never closed.
func TestCancelClosesOnlyAnAgentWithoutTerminal(t *testing.T) {
	for _, tc := range []struct {
		code  string
		close bool
	}{{"no_terminal", true}, {"not_stopped", false}} {
		ft, row := closeTestService(t, nil)
		ft.f.setSessions("mac", row)
		var calls []string
		ft.f.answerNode(deviceSessionStop, func(_ string, _ json.RawMessage) (any, error) {
			calls = append(calls, deviceSessionStop)
			return nil, &rpc.Error{Code: tc.code, Message: "no"}
		})
		ft.f.answerNode("session.close", func(_ string, _ json.RawMessage) (any, error) {
			calls = append(calls, "session.close")
			gone := row
			gone.Status, gone.State, gone.PaneID = proto.StatusOffline, proto.StateOffline, ""
			ft.d.setSessions("mac", []proto.Session{gone})
			return map[string]any{"ok": true}, nil
		})
		s, ctx := ft.s, context.Background()
		todo, err := s.createTodo(ctx, "做", "做", "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.linkTodoAgent(ctx, todo.ID, "mac", row.SessionID, "x", "/code", "claude"); err != nil {
			t.Fatal(err)
		}
		s.stopCancelledAgent(ctx, todo.ID, "mac", row.SessionID)
		ft.f.mu.Lock()
		got := append([]string(nil), calls...)
		ft.f.mu.Unlock()
		want := []string{deviceSessionStop}
		if tc.close {
			want = append(want, "session.close")
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: calls %v, want %v", tc.code, got, want)
		}
	}
}
