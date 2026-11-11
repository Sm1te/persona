package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

var planIDRe = regexp.MustCompile(`plan_[0-9a-f]+`)

// leaderPlanArgs is a main-conversation task_plan for one writable task leader.
func leaderPlanArgs(role string) json.RawMessage {
	task := map[string]any{"title": "拆开做登录改造", "prompt": "把登录改造拆成几块交给 worker 做", "machine": "box", "cwd": "/code/app",
		"readonly": false, "permission": "bypassPermissions", "why": "这个仓库在 box 上", "done_when": []string{"所有 worker 的改动合进一个 PR"}}
	if role != "" {
		task["role"] = role
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
	return raw
}

// workerArgs is a task leader's task_plan of one worker.
func workerArgs(extra map[string]any) json.RawMessage {
	task := map[string]any{"title": "改登录页", "prompt": "只改登录页的布局，补测试", "readonly": false, "permission": "bypassPermissions",
		"done_when": []string{"登录页布局改好", "测试通过"}}
	for k, v := range extra {
		task[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
	return raw
}

// startTaskLeader makes a task leader of the main conversation's task (its agent s1 on box, listed and idle) and waits for
// the link.
func startTaskLeader(t *testing.T, s *Service, f *fakeHub) Todo {
	t.Helper()
	ctx := context.Background()
	res, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("leader"))
	if err != nil {
		t.Fatal(err)
	}
	// A writable task waits for the user's Start on its card.
	if _, err := s.startPlan(ctx, planIDRe.FindString(res), nil, false); err != nil {
		t.Fatalf("%s: %v", res, err)
	}
	waitFor(t, "the leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Title: "leader",
		Status: proto.StatusIdle, UpdatedAt: time.Now()})
	return onlyTodo(s)
}

func todosOpenedBy(s *Service, origin string) []Todo {
	return s.todosOf(context.Background(), origin)
}

// planWorker has the leader (s1 on box) plan one worker, waits for its agent, and lists it as session sN on box.
func planWorker(t *testing.T, s *Service, f *fakeHub, extra map[string]any) Todo {
	t.Helper()
	known := map[string]bool{}
	for _, o := range todosOpenedBy(s, "box/s1") {
		known[o.ID] = true
	}
	res := s.mcpCall(context.Background(), "box", "s1", "task_plan", workerArgs(extra), false)
	if res.IsError {
		t.Fatalf("the leader's task_plan: %s", res.Content[0].Text)
	}
	var w Todo
	waitFor(t, "the worker's link", func() bool {
		for _, o := range todosOpenedBy(s, "box/s1") {
			if !known[o.ID] && len(o.Agents) == 1 {
				w = o
				return true
			}
		}
		return false
	})
	list := todosOpenedBy(s, "box/s1")
	sessions := []proto.Session{{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle, UpdatedAt: time.Now()}}
	for _, o := range list {
		for _, a := range o.Agents {
			sessions = append(sessions, proto.Session{SessionID: a.SessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
				Status: proto.StatusWorking, UpdatedAt: time.Now()})
		}
	}
	f.setSessions("box", sessions...)
	return w
}

// sendsTo are the texts session.send put into machine's session.
func sendsTo(f *fakeHub, machine, session string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.Device != machine || c.Method != DeviceSessionSend {
			continue
		}
		var p struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
		}
		raw, _ := json.Marshal(c.Params)
		if json.Unmarshal(raw, &p) == nil && p.SessionID == session {
			out = append(out, p.Text)
		}
	}
	return out
}

// deliveredTo are the texts session.send put into session and the fake hub took (sendsTo also has those it refused
// with f.failSend).
func deliveredTo(f *fakeHub, session string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.agentSent {
		if a["session_id"] == session {
			text, _ := a["text"].(string)
			out = append(out, text)
		}
	}
	return out
}

func leaderToolNames(tools []proto.Tool) []string {
	var out []string
	for _, tl := range tools {
		out = append(out, tl.Name)
	}
	return out
}

// The main conversation's task_plan marks a task's agent as a task leader (role leader): the agent's todo_agents row
// says so, and only that agent's tool list has the restricted task_plan; any other agent, a worker included, has none.
func TestTaskLeaderGetsRestrictedTaskPlanAndWorkersDoNot(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	if a := todo.Agents[0]; a.Source != sourceLeader || a.SessionID != "s1" || a.MachineID != "box" {
		t.Fatalf("the leader's link: %+v", a)
	}
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" {
		t.Fatalf("the leader's own task is supervised by the conversation that handed it out: %+v", v)
	}
	if prompt, _ := f.starts[0]["prompt"].(string); !strings.Contains(prompt, "you are the leader of this task") {
		t.Fatalf("the leader's first message does not say it leads: %s", prompt)
	}

	var restricted []proto.Tool
	for _, tl := range s.sessionMCPTools(ctx, "box", "s1", false, false) {
		if tl.Name == "task_plan" {
			restricted = append(restricted, tl)
		}
	}
	if len(restricted) != 1 || !strings.Contains(restricted[0].Description, "Hand part of your own task to workers") ||
		strings.Contains(string(restricted[0].InputSchema), "todo_id") || strings.Contains(string(restricted[0].InputSchema), `"role"`) {
		t.Fatalf("the leader's tools: %+v", restricted)
	}
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", "someone-else", false, false)); slices.Contains(got, "task_plan") {
		t.Fatalf("an agent of no task got task_plan: %v", got)
	}

	w := planWorker(t, s, f, nil)
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", w.Agents[0].SessionID, false, false)); slices.Contains(got, "task_plan") {
		t.Fatalf("a worker got task_plan: %v", got)
	}
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", "s1", false, false)); !slices.Contains(got, "report_to_supervisor") {
		t.Fatalf("the leader lost the agents' own tools: %v", got)
	}
}

// The tool list is only the listing: a call must pass the same check, for the leader's own task only.
func TestTaskLeaderTaskPlanCallEnforcement(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	other, err := s.createTaskTodo(ctx, "别的任务", "", "conv1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(session string, args json.RawMessage) proto.ToolResult {
		return s.mcpCall(ctx, "box", session, "task_plan", args, false)
	}
	refused := func(what string, res proto.ToolResult, want ...string) {
		t.Helper()
		if !res.IsError {
			t.Fatalf("%s was accepted: %+v", what, res)
		}
		for _, w := range want {
			if !strings.Contains(res.Content[0].Text, w) {
				t.Fatalf("%s: %q lacks %q", what, res.Content[0].Text, w)
			}
		}
	}

	refused("an agent of no task", call("someone-else", workerArgs(nil)), "unknown tool task_plan")
	refused("a call with no session", call("", workerArgs(nil)), "unknown tool task_plan")
	refused("a leader taking another todo", call("s1", workerArgs(map[string]any{"todo_id": other.ID})), "todo_id")
	refused("a leader making a leader", call("s1", workerArgs(map[string]any{"role": "leader"})), "role")
	refused("a leader attaching files", call("s1", workerArgs(map[string]any{"attachments": []string{"/tmp/a.png"}})), "attachments")
	if got := len(todosOpenedBy(s, "box/s1")); got != 0 {
		t.Fatalf("refused calls opened %d todos", got)
	}

	// Left-out machine and cwd are the leader's own place; the worker starts at once, with no plan card or push.
	feedBefore, pushesBefore := feedKinds(s, "conv1"), len(f.pushed())
	res := call("s1", workerArgs(nil))
	if res.IsError || !strings.Contains(res.Content[0].Text, "beneath your task "+todo.ID) {
		t.Fatalf("the leader's worker: %+v", res)
	}
	var starts []map[string]any
	waitFor(t, "the worker's start", func() bool { starts = f.started(); return len(starts) == 2 })
	if cwd, _ := starts[1]["cwd"].(string); cwd != "/code/app" {
		t.Fatalf("the worker's directory: %v", starts[1])
	}
	if mode, _ := starts[1]["permission_mode"].(string); mode != "bypassPermissions" {
		t.Fatalf("the worker's permission: %v", starts[1])
	}
	if prompt, _ := starts[1]["prompt"].(string); !strings.Contains(prompt, "handed to you by the leader") {
		t.Fatalf("the worker is not told who handed it the task: %s", prompt)
	}
	waitFor(t, "the worker's link", func() bool {
		l := todosOpenedBy(s, "box/s1")
		return len(l) == 1 && len(l[0].Agents) == 1
	})
	w := todosOpenedBy(s, "box/s1")[0]
	a := w.Agents[0]
	if a.Source != sourceWorker || a.Via != todo.ID || w.Quiet {
		t.Fatalf("the worker's link: %+v, todo %+v", a, w)
	}
	// Its supervisor is the leader's agent session, kept in the ledger with the task's done_when, and the API shows it.
	if v := w.Supervision; v == nil || v.Supervisor != "box/s1" || !slices.Equal(v.DoneWhen, []string{"登录页布局改好", "测试通过"}) {
		t.Fatalf("the worker's ledger: %+v", v)
	}
	if body := request(s, "GET", "/api/v1/todos?all=1", "").Body.String(); !strings.Contains(body, `"supervisor":"box/s1"`) {
		t.Fatalf("GET /api/v1/todos does not show the worker's supervisor: %s", body)
	}
	if lead, supervised, ok := s.leaderSupervisor(w); !supervised || !ok || lead.SessionID != "s1" || lead.MachineID != "box" {
		t.Fatalf("the worker's supervisor: %+v supervised %v ok %v", lead, supervised, ok)
	}
	if got := feedKinds(s, "conv1"); got != feedBefore {
		t.Fatalf("the worker's plan came to the main conversation's feed: %s -> %s", feedBefore, got)
	}
	if n := len(f.pushed()); n != pushesBefore {
		t.Fatalf("the user was pushed %d notifications for the leader's workers", n-pushesBefore)
	}

	// A worker has no dispatch right, and is told why; neither has anyone else's agent.
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: a.SessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking})
	refused("a worker's task_plan", call(a.SessionID, workerArgs(nil)), "does not hand out Persona tasks")
	refused("a worker's task_plan, with the leader's role", call(a.SessionID, leaderPlanArgs("leader")), "does not hand out Persona tasks")
	if got := len(todosOpenedBy(s, a.SessionID)); got != 0 {
		t.Fatalf("a worker opened %d todos", got)
	}

	// The leader's task closes: its tool goes, and it cannot plan more.
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	refused("a closed task's leader", call("s1", workerArgs(nil)))
}

// The room is bounded: a leader cannot keep more than leaderWorkersMost workers open. Workers count by the task they
// belong to, so a leader started again (a new session) does not get a new allowance.
func TestTaskLeaderWorkersAreBounded(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	leader := startTaskLeader(t, s, f)
	for i := 0; i < leaderWorkersMost; i++ {
		w, err := s.createTaskTodo(ctx, "worker", "", "box/s1", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 { // ledger names this leader
			s.superviseTodo(ctx, w.ID, "box/s1", nil, 0)
		} else { // an older leader session, linked to the same leader task
			s.superviseTodo(ctx, w.ID, "box/old", nil, 0)
			if err := s.linkTodoAgentWithSource(ctx, w.ID, "box", "old-worker-"+w.ID, "worker", "/code/app", "", "", sourceWorker, leader.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	res := s.mcpCall(ctx, "box", "s1", "task_plan", workerArgs(nil), false)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "at most 8 workers") {
		t.Fatalf("a ninth open worker: %+v", res)
	}
}

// An agent that asks for its tools with no session (a pane the node cannot name) gets none and does not wait for a
// leader being started.
func TestToolListWithNoSessionDoesNotWait(t *testing.T) {
	s, _ := newQuietService(t)
	old := leaderListWait
	leaderListWait = 3 * time.Second
	t.Cleanup(func() { leaderListWait = old })
	s.leaderStarts.Add(1)
	t.Cleanup(func() { s.leaderStarts.Add(-1) })
	start := time.Now()
	if got := s.leaderTools(context.Background(), "box", ""); got != nil || time.Since(start) > time.Second {
		t.Fatalf("no session: %v after %s", got, time.Since(start))
	}
}

// The same session id on another computer is another agent: it is not the leader, is not reached as its workers'
// supervisor and does not review for it.
func TestLeaderIdentityIsMachineAndSession(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	if _, _, ok := s.leaderTask(ctx, "other", "s1"); ok {
		t.Fatal("the same session id on another computer is a leader")
	}
	if s.isLeaderSession("other", "s1") || !s.isLeaderSession("box", "s1") {
		t.Fatal("isLeaderSession ignores the machine")
	}
	review, _ := json.Marshal(map[string]any{"todo_id": w.ID, "verdict": "accepted"})
	if res := s.mcpCall(ctx, "other", "s1", "task_review", review, false); !res.IsError {
		t.Fatalf("an agent with the leader's session id on another computer reviewed: %+v", res)
	}
	if lead, _, ok := s.leaderSupervisor(todoNow(s, w.ID)); !ok || lead.MachineID != "box" {
		t.Fatalf("the worker's leader: %+v", lead)
	}
}

// A writable task needs no explicit permission: without one it takes the usual default, and any mode is accepted. A
// read-only task is unaffected.
func TestWritableTaskPlanNeedsNoExplicitPermission(t *testing.T) {
	s, f := newQuietService(t)
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	ctx := context.Background()
	plan := func(self string, readonly bool, permission string) (string, error) {
		task := map[string]any{"title": "修登录崩溃", "prompt": "修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": readonly,
			"why": "这个仓库在 box 上"}
		if permission != "" {
			task["permission"] = permission
		}
		raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
		return s.toolTaskPlan(ctx, self, raw)
	}
	for _, self := range []string{"conv1", "lead1"} {
		for _, permission := range []string{"", "default", "acceptEdits", "auto", "plan", "bypassPermissions"} {
			if _, err := plan(self, false, permission); err != nil {
				t.Fatalf("%s, permission %q: %v", self, permission, err)
			}
		}
		if _, err := plan(self, true, ""); err != nil {
			t.Fatalf("%s, read-only: %v", self, err)
		}
	}
	if got := len(s.tasks.plans); got != 14 {
		t.Fatalf("plans saved: %d", got)
	}
}

// Any conversation may give role leader: the conversation that plans it supervises its task. A made-up role is refused
// with the rest, and a card edit still cannot change the role.
func TestTaskPlanRoleFromAnyConversation(t *testing.T) {
	s, f := newQuietService(t)
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	ctx := context.Background()
	res, err := s.toolTaskPlan(ctx, "conv2", leaderPlanArgs("leader"))
	if err != nil {
		t.Fatalf("a side conversation's leader: %v", err)
	}
	if _, err := s.startPlan(ctx, planIDRe.FindString(res), nil, false); err != nil {
		t.Fatalf("%s: %v", res, err)
	}
	waitFor(t, "the leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "conv2" || todo.Origin != "conv2" {
		t.Fatalf("the side conversation supervises its own leader: %+v (origin %q)", todo.Supervision, todo.Origin)
	}
	if a := todo.Agents[0]; a.Source != sourceLeader || a.SessionID != "s1" || a.MachineID != "box" {
		t.Fatalf("the leader's link: %+v", a)
	}
	if _, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("boss")); err == nil || !strings.Contains(err.Error(), `"boss" is not one`) {
		t.Fatalf("a made-up role: %v", err)
	}
	if _, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("leader")); err != nil {
		t.Fatalf("the main conversation's leader: %v", err)
	}
	// A client's edit on the card can change what the user may, never the role.
	p := &TaskPlan{}
	for _, pl := range s.tasks.plans {
		p = pl
	}
	task := p.Tasks[0]
	applyPlanEdit(&task, PlanTask{Role: "", Parent: "todo_x", Machine: "box"})
	if task.Role != roleLeader || task.Parent != "" {
		t.Fatalf("an edit changed the role or the parent: %+v", task)
	}
}

// gotMessage waits for a supervision message into the leader's session s1 that has all of words, and gives it.
func gotMessage(t *testing.T, f *fakeHub, words ...string) string {
	t.Helper()
	var found string
	waitFor(t, "a supervision message to the leader with "+strings.Join(words, ", "), func() bool {
		for _, m := range sendsTo(f, "box", "s1") {
			ok := true
			for _, w := range words {
				ok = ok && strings.Contains(m, w)
			}
			if ok {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

// A worker's reports, urgent or not, are supervision signals to its leader's agent session on the leader's machine; the
// leader's own report goes to the conversation that handed it its task. Nothing of the worker's reaches the main chat.
func TestWorkerReportGoesToLeaderOnly(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID

	res := s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"登录页卡在设计评审","evidence":["见 PR 评论"]}`))
	if res.IsError {
		t.Fatalf("the worker's report: %+v", res)
	}
	m := gotMessage(t, f, "Persona · supervision", "改登录页", "登录页卡在设计评审", "见 PR 评论", "登录页布局改好")
	if !strings.Contains(m, "blocked") {
		t.Errorf("the leader's message does not say it is blocked:\n%s", m)
	}
	// A question that says it can wait is no briefing item either: it goes to the leader as any report does.
	res = s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"question","text":"用哪个分支？","urgent":false}`))
	if res.IsError || strings.Contains(res.Content[0].Text, "Queued") {
		t.Fatalf("the worker's urgent:false question: %+v", res)
	}
	gotMessage(t, f, "用哪个分支？")
	if n := sendsTo(f, "box", worker); len(n) != 0 {
		t.Fatalf("the worker was sent %q", n)
	}

	// The leader's own report is the conversation's, and carries nothing of the worker's.
	res = s.agentTool(ctx, "box", "s1", "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"leader 自己卡在评审"}`))
	if res.IsError {
		t.Fatalf("the leader's report: %+v", res)
	}
	waitFor(t, "the main conversation's message", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	if !strings.Contains(n, "leader 自己卡在评审") || strings.Contains(n, "登录页卡在设计评审") || strings.Contains(n, "改登录页") || strings.Contains(n, "用哪个分支") {
		t.Fatalf("the main conversation's message:\n%s", n)
	}
	items, _, _ := s.feed(ctx, "conv1", time.Time{})
	for _, it := range items {
		if it.Kind == feedReport && it.Ref != onlyLeaderTask(s).ID {
			t.Fatalf("a worker's report was put in the main conversation's feed: %+v", it)
		}
	}
}

func onlyLeaderTask(s *Service) Todo {
	for _, t := range s.todosOf(context.Background(), "conv1") {
		return t
	}
	return Todo{}
}

// A worker whose leader's task is closed: its active report is not lost. The worker is rehomed to the conversation above
// the leader first (supervisor and origin), and the report is told to that conversation; with nobody above, the worker is
// cancelled and told so readably.
func TestWorkerReportAfterLeaderClosedIsRehomedNotLost(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	res := s.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"leader 走了之后的汇报"}`))
	if res.IsError || strings.Contains(res.Content[0].Text, "Nobody receives") {
		t.Fatalf("a worker of a closed leader task: %+v", res)
	}
	cur := todoNow(s, w.ID)
	if cur.Supervision.Supervisor != "conv1" || cur.Origin != "conv1" {
		t.Fatalf("the worker was not rehomed (supervisor, origin): %+v origin %q", cur.Supervision, cur.Origin)
	}
	waitFor(t, "the report to reach the conversation above", func() bool { return len(f.notices()) >= 1 })
	if n := f.notices()[0]; !strings.Contains(n, "leader 走了之后的汇报") {
		t.Fatalf("the conversation above did not get the report:\n%s", n)
	}
	// Idempotent: another pass changes nothing and tells nothing twice.
	updated := cur.Supervision.UpdatedAt
	s.rehomeLeaderlessWorkers(ctx)
	s.rehomeLeaderlessWorkers(ctx)
	if got := todoNow(s, w.ID).Supervision.UpdatedAt; !got.Equal(updated) {
		t.Fatalf("rehoming again changed the ledger: %v -> %v", updated, got)
	}
	if got := sendsTo(f, "box", "s1"); len(got) != 0 {
		t.Fatalf("the closed leader was messaged: %q", got)
	}
	// The to-do is the conversation's task now: its notices and listings follow the origin.
	found := false
	for _, o := range s.todosOf(ctx, "conv1") {
		found = found || o.ID == w.ID
	}
	if !found {
		t.Fatal("the rehomed worker is not among the conversation's tasks")
	}
}

// With nobody above the closed leader, a worker's report cancels it with a readable result.
func TestWorkerReportWithNobodyAboveCancelsReadably(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.Supervisor = ""; return true }); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	res := s.agentTool(ctx, "box", w.Agents[0].SessionID, "report_to_supervisor", json.RawMessage(`{"kind":"progress","text":"x"}`))
	if res.IsError || !strings.Contains(res.Content[0].Text, "was cancelled") {
		t.Fatalf("the report of a worker with nobody above: %+v", res)
	}
	if cur := todoNow(s, w.ID); cur.DoneAt == nil || !cur.Cancelled() {
		t.Fatalf("the worker: %+v", cur)
	}
}

// A worker that does not start is told to its leader as a supervision signal, not to a conversation, and no start remedy
// (a main-chat tool) is begun for it.
func TestFailedWorkerStartIsTheLeadersToHear(t *testing.T) {
	s, f := newQuietService(t)
	startTaskLeader(t, s, f)
	f.mu.Lock()
	f.failStart = context.DeadlineExceeded
	f.mu.Unlock()
	res := s.mcpCall(context.Background(), "box", "s1", "task_plan", workerArgs(nil), false)
	if res.IsError {
		t.Fatalf("%+v", res)
	}
	gotMessage(t, f, "its agent did not start")
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
}

// The leader, its workers and where a report goes are kept in the database's rows: a restarted manager reads them back.
func TestTaskLeaderSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	database := taskRecoveryDatabase(t)
	s, f := newQuietServiceOn(t, database)
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID

	restarted, hub := newQuietServiceOn(t, database)
	hub.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking})
	lead, agent, ok := restarted.leaderTask(ctx, "box", "s1")
	if !ok || lead.ID != todo.ID || agent.Source != sourceLeader {
		t.Fatalf("the leader after a restart: %+v %+v %v", lead, agent, ok)
	}
	if got := leaderToolNames(restarted.sessionMCPTools(ctx, "box", "s1", false, false)); !slices.Contains(got, "task_plan") {
		t.Fatalf("the leader's tools after a restart: %v", got)
	}
	if got := leaderToolNames(restarted.sessionMCPTools(ctx, "box", worker, false, false)); slices.Contains(got, "task_plan") {
		t.Fatalf("a worker's tools after a restart: %v", got)
	}
	wr := todoNow(restarted, w.ID)
	if v := wr.Supervision; v == nil || v.Supervisor != "box/s1" || len(v.DoneWhen) != 2 {
		t.Fatalf("the worker's ledger after a restart: %+v", v)
	}
	if lead, supervised, ok := restarted.leaderSupervisor(wr); !supervised || !ok || lead.SessionID != "s1" {
		t.Fatalf("the worker's supervisor after a restart: %+v %v %v", lead, supervised, ok)
	}
	res := restarted.agentTool(ctx, "box", worker, "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"重启后还在跑"}`))
	if res.IsError {
		t.Fatalf("the report after a restart: %+v", res)
	}
	gotMessage(t, hub, "重启后还在跑")
	if got := hub.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
	// The saved plans keep the role and the parent, so starting a task again (a retry) links the same way.
	if p, err := restarted.loadPlan(ctx, todo.PlanID); err != nil || p.Tasks[0].Role != roleLeader || p.Tasks[0].Parent != "" {
		t.Fatalf("the leader's plan after a restart: %+v %v", p, err)
	}
	if p, err := restarted.loadPlan(ctx, w.PlanID); err != nil || p.Conversation != "box/s1" || p.Tasks[0].Role != "" || p.Tasks[0].Parent != todo.ID {
		t.Fatalf("the worker's plan after a restart: %+v %v", p, err)
	}
	// And in the rows the link is read from:
	rows, err := database.ListAllTodoAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, r := range rows {
		kinds[r.SessionID] = r.Source + "/" + r.Via
	}
	if kinds["s1"] != "leader/" || kinds[worker] != "worker/"+todo.ID {
		t.Fatalf("todo_agents: %v", kinds)
	}
}

// An agent being started as a leader asks for its tools before it is linked: its list waits for the link, a list of any
// other unlinked agent waits no longer than the bound, and neither is given the tool by the wait alone.
func TestTaskLeaderListWaitsForItsLink(t *testing.T) {
	s, _ := newQuietService(t)
	old := leaderListWait
	leaderListWait = 400 * time.Millisecond
	t.Cleanup(func() { leaderListWait = old })
	ctx := context.Background()

	if got := s.leaderTools(ctx, "box", "sx"); got != nil {
		t.Fatalf("no leader is starting: %v", got)
	}
	s.leaderStarts.Add(1)
	start := time.Now()
	if got := s.leaderTools(ctx, "box", "sx"); got != nil || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("an unlinked agent while a leader starts: %v after %s", got, time.Since(start))
	}
	todo, err := s.createTaskTodo(ctx, "leader", "", "conv1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, func() {
		_ = s.linkTodoAgentWithSource(ctx, todo.ID, "box", "sx", "leader", "/code/app", "", "", sourceLeader, "")
		s.leaderStarts.Add(-1)
	})
	if got := leaderToolNames(s.leaderTools(ctx, "box", "sx")); !slices.Equal(got, []string{"task_plan", "task_review"}) {
		t.Fatalf("the leader's list after its link: %v", got)
	}
}

// The messages the leader and its workers are given.
func TestTaskLeaderMessages(t *testing.T) {
	for _, want := range []string{"you are the leader of this task", "task_plan", "default permission mode", "Persona · supervision", "task_review", "stop and ask the user"} {
		if !strings.Contains(prompts.RenderTaskLeader(), want) {
			t.Errorf("the leader's brief lacks %q", want)
		}
	}
	for _, want := range []string{"handed to you by the leader", "report_to_supervisor", "cannot hand it on", "stop and ask the user"} {
		if !strings.Contains(prompts.RenderTaskWorker(), want) {
			t.Errorf("the worker's brief lacks %q", want)
		}
	}
	entries := []prompts.SuperviseEntry{{ID: "todo_1", Title: "改登录页", Kind: "report", Report: "question", Said: "用哪个分支？", Step: 2, Steps: 5,
		DoneWhen: []string{"测试通过"}, Round: 1}}
	got := prompts.RenderWorkerSupervise(entries)
	for _, want := range []string{"Persona · supervision: 1 of the worker tasks", `1. "改登录页" (todo_1): the worker reports a question for you (step 2 of 5)`,
		"sent back 1 time(s)", "Done when: 测试通过", "It said:\n   用哪个分支？", "task_review", "You can review your own workers only"} {
		if !strings.Contains(got, want) {
			t.Errorf("the worker supervision message lacks %q:\n%s", want, got)
		}
	}
}

// The hub's own signals about a worker (its turn ending, a judge's verdict, a question waiting on the user) go to its
// leader's session, never to the main conversation; and the done judge does not close a supervised worker: it waits for
// the leader's review.
func TestWorkerSignalsGoToTheLeaderAndTheJudgeDoesNotCloseIt(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID
	turn := func(status, text string, pending int) {
		state := proto.StateUnread
		if pending > 0 {
			state = proto.StateNeedsYou
		}
		if status == proto.StatusWorking {
			state = proto.StateWorking
		}
		f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle},
			proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: status, State: state, LastText: text,
				PendingInteractions: pending, UpdatedAt: time.Now()})
		if status == proto.StatusWorking {
			s.taskWorking(ctx, "box", worker)
		} else {
			s.taskTurnEnded(ctx, "box", worker)
		}
	}
	turn(proto.StatusWorking, "", 0)
	turn(proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	m := gotMessage(t, f, "waits for your review", "改登录页", "pull/7")
	if !strings.Contains(m, "登录页布局改好") {
		t.Errorf("the worker's done_when is not in the message:\n%s", m)
	}
	if cur := todoNow(s, w.ID); cur.DoneAt != nil || cur.Supervision.State != reviewPending {
		t.Fatalf("the judge closed a supervised worker, or it does not wait for review: %+v %+v", cur, cur.Supervision)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told about a worker: %q", got)
	}
	items, _, _ := s.feed(ctx, "conv1", time.Time{})
	for _, it := range items {
		if it.Ref == w.ID {
			t.Fatalf("the worker's to-do is in the main conversation's feed: %+v", it)
		}
	}
}

// accepted, rework and cancelled by the leader, and what a leader may and may not review. The leader's task_review is
// listed for the leader only and reaches its direct workers only; no card goes to the user.
func TestTaskLeaderReviewsItsWorkers(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	leader := startTaskLeader(t, s, f)
	w1 := planWorker(t, s, f, nil)
	w2 := planWorker(t, s, f, map[string]any{"title": "改注册页"})
	w3 := planWorker(t, s, f, map[string]any{"title": "改设置页"})
	review := func(session string, args map[string]any) proto.ToolResult {
		raw, _ := json.Marshal(args)
		return s.mcpCall(ctx, "box", session, "task_review", raw, false)
	}
	refused := func(what string, res proto.ToolResult, want string) {
		t.Helper()
		if !res.IsError || !strings.Contains(res.Content[0].Text, want) {
			t.Fatalf("%s: %+v", what, res)
		}
	}
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", "s1", false, false)); !slices.Contains(got, "task_review") || !slices.Contains(got, "task_plan") {
		t.Fatalf("the leader's tools: %v", got)
	}
	if got := leaderToolNames(s.sessionMCPTools(ctx, "box", w1.Agents[0].SessionID, false, false)); slices.Contains(got, "task_review") {
		t.Fatalf("a worker got task_review: %v", got)
	}
	// Not the leader's to review: its own task, another task, a to-do of the main chat; and nobody else may review a worker.
	other, err := s.createTaskTodo(ctx, "别的任务", "", "conv1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	refused("the leader's own task", review("s1", map[string]any{"todo_id": leader.ID, "verdict": "accepted"}), "not one of your workers")
	refused("another task", review("s1", map[string]any{"todo_id": other.ID, "verdict": "accepted"}), "not one of your workers")
	refused("an agent of no task", review("someone-else", map[string]any{"todo_id": w1.ID, "verdict": "accepted"}), "unknown tool task_review")
	refused("a worker's review of a sibling", review(w1.Agents[0].SessionID, map[string]any{"todo_id": w2.ID, "verdict": "accepted"}), "unknown tool task_review")
	refused("a field outside the schema", review("s1", map[string]any{"todo_id": w1.ID, "verdict": "accepted", "outcome": "cannot"}), "only the fields")
	refused("rework with nothing said", review("s1", map[string]any{"todo_id": w1.ID, "verdict": "rework"}), "say what is missing")
	if cur := todoNow(s, w1.ID); cur.DoneAt != nil || cur.Supervision.State != "" {
		t.Fatalf("a refused review changed the worker: %+v", cur.Supervision)
	}

	// rework: the gaps go to the worker; the third goes up instead.
	pushes := len(f.pushed())
	res := review("s1", map[string]any{"todo_id": w1.ID, "verdict": "rework", "note": "补一个深色模式截图"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "Rework round 1 of 2") {
		t.Fatalf("rework: %+v", res)
	}
	if got := sendsTo(f, "box", w1.Agents[0].SessionID); len(got) != 1 || !strings.Contains(got[0], "补一个深色模式截图") {
		t.Fatalf("what the worker was sent: %q", got)
	}
	if v := todoNow(s, w1.ID).Supervision; v.State != reviewRework || v.Round != 1 || v.Supervisor != "box/s1" {
		t.Fatalf("the ledger after a rework: %+v", v)
	}
	// accepted: closed by the leader's review, with no card to the user.
	res = review("s1", map[string]any{"todo_id": w2.ID, "verdict": "accepted", "note": "测试通过"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "accepted and closed") {
		t.Fatalf("accepted: %+v", res)
	}
	if cur := todoNow(s, w2.ID); cur.DoneAt == nil || cur.DoneBy != "review" || cur.Supervision.State != reviewAccepted || cur.Supervision.Supervisor != "box/s1" {
		t.Fatalf("an accepted worker: %+v %+v", cur, cur.Supervision)
	}
	// cancelled: closed, its agent is stopped.
	res = review("s1", map[string]any{"todo_id": w3.ID, "verdict": "cancelled", "note": "不用做了"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "cancelled and closed") {
		t.Fatalf("cancelled: %+v", res)
	}
	if cur := todoNow(s, w3.ID); cur.DoneAt == nil || !cur.Cancelled() {
		t.Fatalf("a cancelled worker: %+v", cur)
	}
	if n := len(f.pushed()); n != pushes {
		t.Fatalf("the leader's reviews pushed the user %d times", n-pushes)
	}
	// A closed worker takes no more reviews that need it open.
	refused("rework of a closed worker", review("s1", map[string]any{"todo_id": w2.ID, "verdict": "rework", "note": "x"}), "is done already")
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
}

// needs_user goes up to the conversation that handed the leader its task (the main one here) as a decision, with the
// leader's reason and recommendation, and no card goes to the user; a third rework goes up the same way. The main
// conversation reviewing the worker afterwards does not take the worker over.
func TestTaskLeaderNeedsUserGoesToItsOwnSupervisor(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	review := func(self string, args map[string]any) proto.ToolResult {
		raw, _ := json.Marshal(args)
		return s.mcpCall(ctx, "box", self, "task_review", raw, false)
	}
	pushes := len(f.pushed())
	res := review("s1", map[string]any{"todo_id": w.ID, "verdict": "needs_user", "reason": "要不要顺手改注册页", "recommend": "先只改登录页"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "no card went to the user") {
		t.Fatalf("needs_user: %+v", res)
	}
	waitFor(t, "the decision to reach the main conversation", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	for _, want := range []string{"改登录页", "要不要顺手改注册页", "leader 推荐：先只改登录页", "leader（一个编码 Agent"} {
		if !strings.Contains(n, want) {
			t.Errorf("the main conversation's message lacks %q:\n%s", want, n)
		}
	}
	if len(f.pushed()) != pushes {
		t.Fatalf("a card or push went to the user: %+v", f.pushed()[pushes:])
	}
	if cur := todoNow(s, w.ID); cur.Supervision.State != reviewNeedsUser || cur.Supervision.Supervisor != "box/s1" {
		t.Fatalf("the ledger: %+v", cur.Supervision)
	}
	// The main conversation decides with its own task_review; the worker stays the leader's.
	if res, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": w.ID, "verdict": "rework", "note": "只改登录页"}); err != nil {
		t.Fatalf("main's rework: %s %v", res, err)
	}
	if cur := todoNow(s, w.ID); cur.Supervision.Supervisor != "box/s1" {
		t.Fatalf("the main conversation took the worker over: %+v", cur.Supervision)
	}

	// Two reworks, then the third goes up.
	w2 := planWorker(t, s, f, map[string]any{"title": "改注册页"})
	for i := 1; i <= maxReworkRounds; i++ {
		if res := review("s1", map[string]any{"todo_id": w2.ID, "verdict": "rework", "note": "还不够"}); res.IsError {
			t.Fatalf("rework %d: %+v", i, res)
		}
	}
	res = review("s1", map[string]any{"todo_id": w2.ID, "verdict": "rework", "note": "还是不够"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "sent back 2 times already") {
		t.Fatalf("the third rework: %+v", res)
	}
	if v := todoNow(s, w2.ID).Supervision; v.State != reviewNeedsUser {
		t.Fatalf("the ledger after the third rework: %+v", v)
	}
}

// A worker's own card stays with the user (askRoute is a main-chat ladder this task does not extend), and no start
// remedy is begun for a worker's failed start.
func TestWorkerCardsStayWithTheUser(t *testing.T) {
	s, f := newQuietService(t)
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	if !s.agentSupervisedSession("box", w.Agents[0].SessionID) {
		t.Fatal("a worker is not agent-supervised")
	}
	if s.agentSupervisedSession("box", "s1") {
		t.Fatal("the leader's own task counts as agent-supervised")
	}
	_ = f
}

// dot (the main conversation) hands a task leader to a leader conversation with task_plan's supervisor: the leader's own
// task is that leader conversation's to supervise, its report goes there, and its workers still report to the task
// leader's agent: main → leader conversation → task leader → worker, and no deeper.
func TestTaskLeaderUnderALeaderConversation(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	var in map[string]any
	_ = json.Unmarshal(leaderPlanArgs("leader"), &in)
	in["supervisor"] = "lead1"
	raw, _ := json.Marshal(in)
	res, err := s.toolTaskPlan(ctx, "conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.startPlan(ctx, planIDRe.FindString(res), nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "lead1" || todo.Agents[0].Source != sourceLeader {
		t.Fatalf("the task leader's task: %+v %+v", todo.Supervision, todo.Agents)
	}
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle})
	w := planWorker(t, s, f, nil)

	if res := s.agentTool(ctx, "box", w.Agents[0].SessionID, "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"worker 的进度"}`)); res.IsError {
		t.Fatalf("%+v", res)
	}
	if res := s.agentTool(ctx, "box", "s1", "report_to_supervisor", json.RawMessage(`{"kind":"blocked","text":"leader 的阻塞"}`)); res.IsError {
		t.Fatalf("%+v", res)
	}
	waitFor(t, "the leader conversation's message", func() bool { return len(f.noticesTo("lead1")) == 1 })
	if n := f.noticesTo("lead1")[0]; !strings.Contains(n, "leader 的阻塞") || strings.Contains(n, "worker 的进度") {
		t.Fatalf("the leader conversation's message:\n%s", n)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
	if m := gotMessage(t, f, "worker 的进度"); strings.Contains(m, "leader 的阻塞") {
		t.Fatalf("the task leader's message carries its own report:\n%s", m)
	}
}

// Even where the judge closes a main-chat task that passed its own check (spot checks off), it does not close a
// leader's worker: the worker waits for its leader's review.
func TestJudgeDoesNotCloseALeadersWorkerThatPassedItsCheck(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	worker := w.Agents[0].SessionID
	sampleAt(t, s, 0)
	f.mu.Lock()
	f.judge = `{"verdict":"done","reason":"已合并并上线","delivery":"live","checks":[` +
		`{"criterion":"登录页布局改好","checkable":true,"met":true,"evidence":"PR 里有截图"},` +
		`{"criterion":"测试通过","checkable":true,"met":true,"evidence":"CI 绿"}]}`
	f.mu.Unlock()
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now()})
	s.taskWorking(ctx, "box", worker)
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusIdle},
		proto.Session{SessionID: worker, MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusDone, State: proto.StateUnread,
			LastText: "做完了，证据见 PR\n交付：已上线并实测；卡点：无；谁来解：无", UpdatedAt: time.Now()})
	s.taskTurnEnded(ctx, "box", worker)
	gotMessage(t, f, "waits for your review")
	if cur := todoNow(s, w.ID); cur.DoneAt != nil || cur.Supervision.State != reviewPending {
		t.Fatalf("the judge closed a leader's worker: %+v %+v", cur, cur.Supervision)
	}
}

// Automatic signals about a worker (idle, stalled, orphaned, interrupted) go to the leader's session like a report.
func TestWorkerAutomaticSignalsGoToLeader(t *testing.T) {
	for _, kind := range []string{sigIdle, sigStalled, sigOrphaned, sigInterrupted} {
		t.Run(kind, func(t *testing.T) {
			s, f := newQuietService(t)
			ctx := context.Background()
			startTaskLeader(t, s, f)
			w := planWorker(t, s, f, nil)
			switch kind {
			case sigIdle, sigInterrupted:
				s.signal(superviseSignal{Todo: w.ID, Kind: kind, Key: "k1", Machine: "box", Session: w.Agents[0].SessionID, Detail: "停下了"})
			case sigStalled:
				old := w.Agents[0]
				_ = old
				// watchSignals: an agent working without progress past the stall bound.
				stall := proto.Session{SessionID: w.Agents[0].SessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
					Status: proto.StatusWorking, State: proto.StateWorking, UpdatedAt: time.Now().Add(-superviseStallMin - time.Minute)}
				f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusIdle}, stall)
				s.superviseWatch(ctx, time.Now())
			case sigOrphaned:
				// A session its computer no longer lists: the orphan pass marks it closed, and the worker's signal goes to the leader.
				f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusIdle})
				s.orphanWatch(ctx, time.Now())
				if g, ok := s.orphanSignal(todoNow(s, w.ID)); ok {
					s.signal(g)
				}
			}
			m := gotMessage(t, f, "Persona · supervision", "改登录页")
			want := map[string]string{sigIdle: "stopped, but the work is not finished", sigStalled: "no progress for",
				sigOrphaned: "nobody works on it any more", sigInterrupted: "cut its turn off"}[kind]
			if !strings.Contains(m, want) {
				t.Errorf("the leader's message for %s lacks %q:\n%s", kind, want, m)
			}
			if got := f.notices(); len(got) != 0 {
				t.Fatalf("the main conversation was told: %q", got)
			}
		})
	}
}

// A leader that cannot be reached: the signals are not dropped. While its task is open they become a decision for the
// conversation above it (who to tell, with the reason), so the worker is not left unreviewed; with nobody above it they
// stay pending and are tried again. A closed agent whose resume fails is handled the same way.
func TestUnreachableLeaderEscalatesInsteadOfDropping(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	// The leader's agent row is released (it was replaced): there is no leader agent to reach any more.
	leadRow, _, _ := s.leaderSupervisor(todoNow(s, w.ID))
	if leadRow.SessionID != "s1" {
		t.Fatalf("setup: %v", leadRow)
	}
	s.todos.mu.Lock()
	for i := range s.todos.todos[todo.ID].Agents {
		a := &s.todos.todos[todo.ID].Agents[i]
		if a.SessionID == "s1" {
			now := time.Now().UTC()
			a.ReleasedAt = &now
		}
	}
	s.todos.mu.Unlock()
	if lead, _, ok := s.leaderSupervisor(todoNow(s, w.ID)); ok {
		t.Fatalf("setup: the leader is still reachable: %+v", lead)
	}
	if up := s.supervisorAbove(ctx, leadRow); up != "conv1" {
		t.Fatalf("setup: the conversation above the released leader is %q", up)
	}
	f.setSessions("box", proto.Session{SessionID: w.Agents[0].SessionID, MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusDone, State: proto.StateUnread, LastText: "做完了", UpdatedAt: time.Now()})
	s.signal(superviseSignal{Todo: w.ID, Kind: sigReview, Key: "review@1", Detail: "做完了，等你验收"})
	waitFor(t, "the decision to go up to the main conversation", func() bool {
		return len(f.notices()) == 1
	})
	n := f.notices()[0]
	if !strings.Contains(n, "leader（一个编码 Agent") || !strings.Contains(n, "cannot be reached") {
		t.Fatalf("the main conversation's escalation:\n%s", n)
	}
	// The worker's own task stays open; the leader's task is named in the decision so the conversation can act.
	if cur := todoNow(s, w.ID); cur.DoneAt != nil {
		t.Fatalf("the worker was closed by an escalation: %+v", cur)
	}
}

// A leader whose own task closed: its open workers are rehomed to the conversation above it (which becomes their
// supervisor and can review them), not left pending for ever.
func TestClosedLeaderRehomesItsWorkers(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	if _, _, err := s.updateSupervision(ctx, w.ID, func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	s.rehomeLeaderlessWorkers(ctx)
	cur := todoNow(s, w.ID)
	if cur.Supervision.Supervisor != "conv1" || cur.Supervision.State != reviewPending || cur.DoneAt != nil {
		t.Fatalf("the rehomed worker: %+v %+v", cur, cur.Supervision)
	}
	// The conversation can review it, and does not take over a worker whose leader is alive.
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": w.ID, "verdict": "accepted", "note": "测试通过"}); err != nil {
		t.Fatalf("the main conversation reviewing a rehomed worker: %v", err)
	}
	if got := todoNow(s, w.ID); got.DoneAt == nil || got.DoneBy != "review" {
		t.Fatalf("the accepted rehomed worker: %+v", got)
	}
}

// With no conversation above it at all the worker is cancelled with a reason, not left with nobody to review it.
func TestLeaderlessWorkerWithNobodyAboveIsCancelled(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.Supervisor = ""; return true }); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	s.rehomeLeaderlessWorkers(ctx)
	if cur := todoNow(s, w.ID); cur.DoneAt == nil || !cur.Cancelled() {
		t.Fatalf("the worker with nobody above it: %+v", cur)
	}
}

// A closed leader agent whose resume fails: the worker's signal goes up as a decision that names the worker and says the
// leader could not be reached, and (here with nobody above) the message is tried again on the leader's own context,
// not a cancelled one, until the leader takes it.
func TestLeaderResumeFailureEscalatesAndRetries(t *testing.T) {
	s, f := newQuietService(t)
	s.leaderResume = 40 * time.Millisecond
	ctx := context.Background()
	startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	lead, _, _ := s.leaderSupervisor(todoNow(s, w.ID))
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude",
		Status: proto.StatusOffline, UpdatedAt: time.Now()},
		proto.Session{SessionID: w.Agents[0].SessionID, MachineID: "box", Agent: "claude", Status: proto.StatusWorking})
	f.mu.Lock()
	f.failSend = context.DeadlineExceeded
	f.mu.Unlock()
	kept := []superviseSignal{{Todo: w.ID, Kind: sigReview, Key: "review@1", Detail: "做完了"}}
	text := prompts.RenderWorkerSupervise([]prompts.SuperviseEntry{{ID: w.ID, Title: w.Title, Kind: "review"}})
	if err := s.sendToLeader(ctx, lead, text, kept); err != nil {
		t.Fatalf("the resume is scheduled, not failed synchronously: %v", err)
	}
	waitFor(t, "the failed resume to go up", func() bool { return len(f.notices()) >= 1 })
	n := f.notices()[0]
	if !strings.Contains(n, "改登录页") || !strings.Contains(n, w.ID) || !strings.Contains(n, "联系不上") || strings.Contains(n, "判断要用户决定") {
		t.Fatalf("the escalation must name the worker and say the leader is unreachable:\n%s", n)
	}
	// The leader comes back: the retry (own context, several attempts) delivers it.
	f.mu.Lock()
	f.failSend = nil
	f.mu.Unlock()
	waitFor(t, "the retry to reach the leader", func() bool { return slices.Contains(deliveredTo(f, "s1"), text) })
}

// With nobody above the leader nothing is lost either: no conversation to tell, so the leader itself is retried.
func TestUndeliveredMessageWithNobodyAboveIsRetried(t *testing.T) {
	s, f := newQuietService(t)
	s.leaderResume = 30 * time.Millisecond
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	if _, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool { v.Supervisor = ""; return true }); err != nil {
		t.Fatal(err)
	}
	lead, _, _ := s.leaderSupervisor(todoNow(s, w.ID))
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Agent: "claude", Status: proto.StatusOffline})
	f.mu.Lock()
	f.failSend = context.DeadlineExceeded
	f.mu.Unlock()
	kept := []superviseSignal{{Todo: w.ID, Kind: sigReview, Key: "review@1", Detail: "做完了"}}
	const text = "Persona · supervision: undelivered"
	if err := s.sendToLeader(ctx, lead, text, kept); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(150*time.Millisecond, func() { f.mu.Lock(); f.failSend = nil; f.mu.Unlock() })
	waitFor(t, "the undelivered message to reach the leader after a few tries", func() bool {
		return slices.Contains(deliveredTo(f, "s1"), text)
	})
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("with nobody above, something was told to the main conversation: %q", got)
	}
}

// A flexible task leader started again on another computer: the worker's fallback by the leader task's link reaches its
// current agent there, not only one on the old computer.
func TestLeaderRestartedOnAnotherComputerIsStillReached(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	// The old agent is released; the task leader's new agent runs on another computer.
	s.todos.mu.Lock()
	for i := range s.todos.todos[todo.ID].Agents {
		a := &s.todos.todos[todo.ID].Agents[i]
		if a.SessionID == "s1" {
			now := time.Now().UTC()
			a.ReleasedAt = &now
		}
	}
	s.todos.mu.Unlock()
	if err := s.linkTodoAgentWithSource(ctx, todo.ID, "box2", "s9", "leader", "/code/app", "", "", sourceLeader, ""); err != nil {
		t.Fatal(err)
	}
	lead, supervised, ok := s.leaderSupervisor(todoNow(s, w.ID))
	if !supervised || !ok || lead.MachineID != "box2" || lead.SessionID != "s9" {
		t.Fatalf("the worker's leader after the leader moved: %+v supervised %v ok %v", lead, supervised, ok)
	}
}

// A leader of a writable task on a work-trust computer is refused at planning and at start, readably, because that
// computer's agents get no task_plan / task_review.
func TestTaskLeaderRefusedOnWorkTrustMachine(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.hello.Assistant.WorkMachines = []string{"box"}
	args := leaderPlanArgs("leader")
	if _, err := s.toolTaskPlan(ctx, "conv1", args); err == nil || !strings.Contains(err.Error(), "work-trust computer") {
		t.Fatalf("planning a leader on a work-trust computer: %v", err)
	}
	// A saved plan that names one is held at Start too.
	f.hello.Assistant.WorkMachines = nil
	res, err := s.toolTaskPlan(ctx, "conv1", args)
	if err != nil {
		t.Fatal(err)
	}
	f.hello.Assistant.WorkMachines = []string{"box"}
	_, err = s.startPlan(ctx, planIDRe.FindString(res), nil, false)
	var invalid planInvalid
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "work-trust computer") {
		t.Fatalf("starting a saved leader plan on a work-trust computer: %v", err)
	}
	if len(f.starts) != 0 {
		t.Fatalf("it started: %v", f.starts)
	}
}

// A task leader never lands on a work-trust computer by any other road either: a retry of a saved plan, a recovered or
// remedy start (startTaskAgent), nor a flexible task moved by the health check.
func TestTaskLeaderNeverLandsOnWorkTrustMachine(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.hello.Assistant.WorkMachines = []string{"box"}
	_, _, err := s.startTaskAgent(ctx, &PlanTask{Title: "L", Prompt: "p", Machine: "box", Cwd: "/code/app", Role: roleLeader, Permission: "bypassPermissions"}, true)
	if err == nil || !strings.Contains(err.Error(), "work-trust computer") {
		t.Fatalf("starting a task leader on a work-trust computer: %v", err)
	}
	f.mu.Lock()
	starts := len(f.starts)
	f.mu.Unlock()
	if starts != 0 {
		t.Fatalf("a start went out: %v", f.starts)
	}
	// Retry of a saved leader plan on a computer that is work-trust now.
	f.hello.Assistant.WorkMachines = nil
	res, err := s.toolTaskPlan(ctx, "conv1", leaderPlanArgs("leader"))
	if err != nil {
		t.Fatal(err)
	}
	id := planIDRe.FindString(res)
	started, err := s.startPlan(ctx, id, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	todo := started.Tasks[0].TodoID
	waitFor(t, "the first start", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.starts) == 1 })
	waitFor(t, "its link", func() bool { return len(todoNow(s, todo).Agents) == 1 })
	if err := s.failTodo(ctx, todo, "did not start"); err != nil {
		t.Fatal(err)
	}
	f.hello.Assistant.WorkMachines = []string{"box"}
	if _, err := s.retryTask(ctx, todo); err == nil || !strings.Contains(err.Error(), "work-trust computer") {
		t.Fatalf("retrying a leader plan on a work-trust computer: %v", err)
	}
}

// healthyPlace never moves a flexible task leader to a work-trust computer, while an ordinary task may go there.
func TestHealthDetourSkipsWorkTrustForTaskLeaders(t *testing.T) {
	s, f := rerouteHub(t)
	f.hello.Assistant.WorkMachines = []string{"box"}
	blockWorkLauncher(s, "l4")
	failWorkLauncherCheck(s, "server") // box is the only healthy alternative
	ctx := context.Background()
	leader := PlanTask{Title: "L", Machine: "l4", Cwd: "~/code/app", Launcher: "work", MachinePolicy: machineFlexible, Role: roleLeader}
	if s.healthyPlace(ctx, &leader) || leader.Machine != "l4" {
		t.Fatalf("a task leader was moved to a work-trust computer: %+v", leader)
	}
	ordinary := PlanTask{Title: "O", Machine: "l4", Cwd: "~/code/app", Launcher: "work", MachinePolicy: machineFlexible}
	if !s.healthyPlace(ctx, &ordinary) || ordinary.Machine != "box" {
		t.Fatalf("control: an ordinary task is moved as before: %+v", ordinary)
	}
}

// A plan saved without a permission retries as it is: no permission is required of a writable task.
func TestRetryNeedsNoPermission(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	res, err := s.toolTaskPlan(ctx, "conv1", json.RawMessage(`{"tasks":[{"title":"Fix","permission":"bypassPermissions","prompt":"Fix it","machine":"box","cwd":"/code/app"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := planIDRe.FindString(res)
	started, err := s.startPlan(ctx, id, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	todo := started.Tasks[0].TodoID
	waitFor(t, "the first start", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.starts) == 1 })
	waitFor(t, "its link", func() bool { return len(todoNow(s, todo).Agents) == 1 })
	if err := s.failTodo(ctx, todo, "did not start"); err != nil {
		t.Fatal(err)
	}
	// The plan as an older one saved it: a writable task with no permission.
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p.Tasks[0].Permission = ""
	if err := s.savePlan(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.retryTask(ctx, todo); err != nil {
		t.Fatalf("retrying a saved plan with no permission: %v", err)
	}
	waitFor(t, "the retry's start", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.starts) == 2 })
	if cur := todoNow(s, todo); cur.DoneAt != nil {
		t.Fatalf("the failed todo was not reopened: %+v", cur)
	}
}

// main → leader conversation → task leader → worker: the task leader's needs_user reaches the leader conversation (lead1),
// which reviews the worker with its own task_review (rework, then accepted) without taking the worker over; and when the
// task leader's own task closes the open worker is rehomed to lead1, which can close it.
func TestNeedsUserFromTaskLeaderIsReviewedByLeaderConversation(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	var in map[string]any
	_ = json.Unmarshal(leaderPlanArgs("leader"), &in)
	in["supervisor"] = "lead1"
	raw, _ := json.Marshal(in)
	res, err := s.toolTaskPlan(ctx, "conv1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.startPlan(ctx, planIDRe.FindString(res), nil, false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the leader's link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	leaderTodo := onlyTodo(s)
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle})
	w := planWorker(t, s, f, nil)
	review := func(self string, args map[string]any) (string, error) {
		return runJSON(t, s, self, "task_review", args)
	}

	if r := s.mcpCall(ctx, "box", "s1", "task_review", mustJSON(map[string]any{"todo_id": w.ID, "verdict": "needs_user", "reason": "要不要顺手改注册页", "recommend": "先只改登录页"}), false); r.IsError {
		t.Fatalf("needs_user: %+v", r)
	}
	waitFor(t, "the decision to reach lead1", func() bool { return len(f.noticesTo("lead1")) == 1 })
	if n := f.noticesTo("lead1")[0]; !strings.Contains(n, "要不要顺手改注册页") {
		t.Fatalf("lead1's message:\n%s", n)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
	// Not its own to review: another conversation, and a task that no leader of lead1 leads.
	if _, err := review("conv2", map[string]any{"todo_id": w.ID, "verdict": "accepted"}); err == nil {
		t.Fatal("a conversation that is not above the task leader reviewed its worker")
	}
	if res, err := review("lead1", map[string]any{"todo_id": w.ID, "verdict": "rework", "note": "只改登录页"}); err != nil || !strings.Contains(res, "Rework round 1") {
		t.Fatalf("lead1's rework: %s %v", res, err)
	}
	if cur := todoNow(s, w.ID); cur.Supervision.Supervisor != "box/s1" || cur.Supervision.State != reviewRework {
		t.Fatalf("lead1 took the worker over, or its state: %+v", cur.Supervision)
	}
	if res, err := review("lead1", map[string]any{"todo_id": w.ID, "verdict": "accepted", "note": "好了"}); err != nil || !strings.Contains(res, "accepted and closed") {
		t.Fatalf("lead1's accepted: %s %v", res, err)
	}
	if cur := todoNow(s, w.ID); cur.DoneAt == nil || cur.Supervision.State != reviewAccepted || cur.Supervision.Supervisor != "box/s1" {
		t.Fatalf("the accepted worker: %+v %+v", cur, cur.Supervision)
	}

	// H1: another open worker when the task leader's own task closes is rehomed to lead1, which can close it.
	w2 := planWorker(t, s, f, map[string]any{"title": "改注册页"})
	if ok, err := s.autoDoneTodo(ctx, leaderTodo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	s.rehomeLeaderlessWorkers(ctx)
	if cur := todoNow(s, w2.ID); cur.Supervision.Supervisor != "lead1" || cur.DoneAt != nil {
		t.Fatalf("the rehomed worker: %+v", cur.Supervision)
	}
	if res, err := review("lead1", map[string]any{"todo_id": w2.ID, "verdict": "cancelled", "note": "不用做了"}); err != nil || !strings.Contains(res, "cancelled and closed") {
		t.Fatalf("lead1's cancel of the rehomed worker: %s %v", res, err)
	}
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

// Every start of a plan task (runPlanTaskStart) needs no permission: without one the task takes the default mode.
func TestPlanTaskStartNeedsNoPermission(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	res, err := s.toolTaskPlan(ctx, "conv1", json.RawMessage(`{"tasks":[{"title":"Fix","permission":"bypassPermissions","prompt":"Fix it","machine":"box","cwd":"/code/app"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := planIDRe.FindString(res)
	started, err := s.startPlan(ctx, id, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	todo := started.Tasks[0].TodoID
	waitFor(t, "the first start", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.starts) == 1 })
	waitFor(t, "its link", func() bool { return len(todoNow(s, todo).Agents) == 1 })
	for _, perm := range []string{"", "default", "acceptEdits"} {
		task := started.Tasks[0]
		task.Permission = perm
		want := len(f.starts) + 1
		s.startPlanTask(id, "conv1", 0, task, true)
		waitFor(t, "the start with permission "+perm, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.starts) == want })
		if cur := todoNow(s, todo); cur.StartError != "" {
			t.Fatalf("permission %q: the to-do after the start: %+v", perm, cur)
		}
	}
}

// A rehomed worker's supervisor and origin are stored: a restarted manager reads both back.
func TestRehomedWorkerSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	database := taskRecoveryDatabase(t)
	s, f := newQuietServiceOn(t, database)
	todo := startTaskLeader(t, s, f)
	w := planWorker(t, s, f, nil)
	if ok, err := s.autoDoneTodo(ctx, todo.ID, "done", time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("closing the leader's task: %v %v", ok, err)
	}
	s.rehomeLeaderlessWorkers(ctx)
	restarted, _ := newQuietServiceOn(t, database)
	cur := todoNow(restarted, w.ID)
	if cur.Origin != "conv1" || cur.Supervision == nil || cur.Supervision.Supervisor != "conv1" || cur.DoneAt != nil {
		t.Fatalf("the rehomed worker after a restart: origin %q %+v", cur.Origin, cur.Supervision)
	}
}

// A leader's workers are held to the brief rule (goal for a read-only worker, done_when for a writable one, and
// expect_minutes for both) as any task_plan task is, and the leader's schema can say them.
func TestLeaderWorkersNeedABrief(t *testing.T) {
	s, f := newQuietService(t)
	startTaskLeader(t, s, f)
	s.briefsOptional = false
	call := func(task map[string]any) proto.ToolResult {
		raw, _ := json.Marshal(map[string]any{"tasks": []any{task}})
		return s.mcpCall(context.Background(), "box", "s1", "task_plan", raw, false)
	}
	res := call(map[string]any{"title": "改登录页", "prompt": "改", "readonly": false, "permission": "bypassPermissions", "done_when": []string{"测试通过"}})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "expect_minutes") {
		t.Fatalf("a writable worker without expect_minutes: %+v", res)
	}
	res = call(map[string]any{"title": "查一下", "prompt": "查", "readonly": true, "expect_minutes": 10})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "goal") {
		t.Fatalf("a read-only worker without a goal: %+v", res)
	}
	if res = call(map[string]any{"title": "查一下", "prompt": "查", "readonly": true, "goal": "知道登录页为什么崩", "expect_minutes": 10}); res.IsError {
		t.Fatalf("a read-only worker with its brief: %+v", res)
	}
}
