package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// taskNode connects a fake node named id whose session.start answers a new session and records what it was asked.
type taskNode struct {
	mu     sync.Mutex
	starts []map[string]any
	sends  []map[string]any
	n      int
	// failStart: session.start answers this error instead of a session.
	failStart error
	// gate: when set, session.start waits for it to close (a start that takes a while).
	gate chan struct{}
	// items: what session.items answers (the agent's latest turn).
	items []proto.Item
}

func (tn *taskNode) calls() (starts, sends []map[string]any) {
	tn.mu.Lock()
	defer tn.mu.Unlock()
	return append([]map[string]any(nil), tn.starts...), append([]map[string]any(nil), tn.sends...)
}

func connectTaskNode(t *testing.T, h *Hub, id string) *taskNode {
	t.Helper()
	tn := &taskNode{}
	m := newMachine(h, MachineConfig{ID: id, Name: id})
	registerMachine(h, m)
	hubIn, nodeOut := io.Pipe()
	nodeIn, hubOut := io.Pipe()
	hubPeer, nodePeer := rpc.New("h", hubOut), rpc.New("n", nodeOut)
	nodePeer.OnRequest = func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		tn.mu.Lock()
		gate := tn.gate
		tn.mu.Unlock()
		if gate != nil && method == "session.start" {
			<-gate
		}
		tn.mu.Lock()
		defer tn.mu.Unlock()
		switch method {
		case "session.start":
			tn.starts = append(tn.starts, p)
			if tn.failStart != nil {
				return nil, tn.failStart
			}
			tn.n++
			sid := "s" + string(rune('0'+tn.n))
			return map[string]any{"session": proto.Session{SessionID: sid, Cwd: p["cwd"].(string), Agent: "claude"}}, nil
		case "session.send":
			tn.sends = append(tn.sends, p)
		case "session.items":
			return map[string]any{"items": tn.items}, nil
		case "launchers.check":
			return proto.LauncherCheck{OK: true, Agent: "claude", Answer: "ok"}, nil
		}
		return map[string]any{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = hubPeer.Serve(ctx, hubIn) }()
	go func() { _ = nodePeer.Serve(ctx, nodeIn) }()
	m.mu.Lock()
	m.peer, m.online = hubPeer, true
	m.mu.Unlock()
	return tn
}

func newTaskHub(t *testing.T) (*Hub, *taskNode) {
	t.Helper()
	h := newTestHub(t)
	tn := connectTaskNode(t, h, "box")
	old := taskSettleDelay
	taskSettleDelay = func(NotifyConfig) time.Duration { return 0 }
	t.Cleanup(func() { taskSettleDelay = old })
	connectAssistant(t, h)
	return h, tn
}

// taskPlan is the assistant's task_plan tool, called from conversation conv.
func taskPlan(h *Hub, conv string, args json.RawMessage) (string, error) {
	r := h.asst().Tool(context.Background(), assistant.ToolCall{Name: "task_plan", Arguments: args, MachineID: "box", SessionID: conv})
	if r.IsError {
		return "", errors.New(r.Content[0].Text)
	}
	return r.Content[0].Text, nil
}

// assistantGet is a GET on the assistant's routes, as the hub relays it, once the assistant took what the hub told it.
func assistantGet(h *Hub, path string, out any) int {
	settleAssistant(h)
	mux := http.NewServeMux()
	h.assistantRoutesRelayed(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	_ = json.Unmarshal(w.Body.Bytes(), out)
	return w.Code
}

// feed is a conversation's feed and the plans it names, as the app reads them.
func feed(h *Hub, conv string) ([]assistant.FeedItem, map[string]*assistant.TaskPlan) {
	var got struct {
		Items []assistant.FeedItem
		Plans map[string]*assistant.TaskPlan
	}
	assistantGet(h, "/api/v1/assistant/conversations/"+conv+"/feed", &got)
	return got.Items, got.Plans
}

// loadPlan is a plan as the app reads it (nil: none).
func loadPlan(h *Hub, id string) *assistant.TaskPlan {
	var got struct{ Plan *assistant.TaskPlan }
	assistantGet(h, "/api/v1/task-plans/"+id, &got)
	return got.Plan
}

// taskLine is the task's line in the live context a message to conv1 starts with: "title (id): state…" (in Chinese).
func taskLine(h *Hub, todo Todo) string {
	if a, ok := testAssistants.Load(h); ok {
		a.(*testAssistant).svc.ProfileChanged(context.Background()) // a fresh snapshot, not the one kept for 45 s
	}
	res, _ := h.asst().Context(context.Background(), assistant.ContextParams{SessionID: "conv1"})
	text := res.Text
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, todo.Title+"（"+todo.ID+"）：") || strings.Contains(l, todo.Title+"（"+todo.ID+"，链接 ") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

func planArgs(readonly bool) json.RawMessage {
	task := map[string]any{
		"title": "修登录崩溃", "prompt": "iPad 横屏时登录页崩溃，修好并补测试", "machine": "box", "cwd": "/code/app",
		"readonly": readonly, "why": "这个仓库最近在 box 上", "permission": "bypassPermissions",
		// What task_plan requires of a brief (#3822): a read-only task's goal and how long it should take.
		"goal": "登录页横屏不再崩溃", "expect_minutes": 10,
	}
	if !readonly { // a writable task also says what done means
		task["done_when"] = []string{"修好并补了测试"}
	}
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{task}})
	return raw
}

func feedKinds(h *Hub, conv string) []string {
	items, _ := feed(h, conv)
	var out []string
	for _, it := range items {
		out = append(out, it.Kind)
	}
	return out
}

// A read-only plan starts at once: a todo that remembers the conversation, its agent started with the prompt, and
// the plan and the task in the conversation's feed.
func TestTaskPlanReadOnlyStartsAtOnce(t *testing.T) {
	h, tn := newTaskHub(t)
	res, err := taskPlan(h, "conv1", planArgs(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "Queued 1 task") {
		t.Fatalf("result = %q", res)
	}
	waitFor(t, "the agent to start and link", func() bool {
		ts := taskTodos(h)
		return len(ts) == 1 && len(ts[0].Agents) == 1
	})
	todo := taskTodos(h)[0]
	if todo.Origin != "conv1" || todo.PlanID == "" || todo.Title != "修登录崩溃" || todo.Agents[0].SessionID != "s1" {
		t.Fatalf("todo = %+v", todo)
	}
	starts, _ := tn.calls()
	wantPrompt := "iPad 横屏时登录页崩溃，修好并补测试\n\n本任务只读：不改项目文件、不提交、不对外写入"
	if len(starts) != 1 || starts[0]["prompt"] != wantPrompt || starts[0]["cwd"] != "/code/app" ||
		starts[0]["permission_mode"] != "bypassPermissions" {
		t.Fatalf("session.start = %+v", starts)
	}
	if got := strings.Join(feedKinds(h, "conv1"), ","); got != "plan,task" {
		t.Fatalf("feed = %s", got)
	}
	p := loadPlan(h, todo.PlanID)
	if p.State != "started" || p.Tasks[0].TodoID != todo.ID || p.Tasks[0].Permission != "bypassPermissions" {
		t.Fatalf("plan = %+v", p)
	}
}

// failedStart checks a task whose agent did not start: its todo is done with why and no agent, the snapshot and the
// conversation's live counts see it as ended (#1077), the plan keeps the error and the conversation has a failed receipt.
func failedStart(t *testing.T, h *Hub, want string) Todo {
	t.Helper()
	waitFor(t, "the todo to end", func() bool { ts := taskTodos(h); return len(ts) == 1 && ts[0].DoneAt != nil })
	todo := taskTodos(h)[0]
	if !strings.Contains(todo.StartError, want) || len(todo.Agents) != 0 {
		t.Fatalf("todo = %+v", todo)
	}
	if line := taskLine(h, todo); !strings.Contains(line, "：没开起来：") || !strings.Contains(line, want) { // and why (#4124)
		t.Fatalf("the live context = %q", line)
	}
	if got := inboxTasks(t, h); len(got) != 0 {
		t.Fatalf("the inbox still sees a failed task: %+v", got)
	}
	waitFor(t, "the receipt", func() bool { return strings.Contains(strings.Join(feedKinds(h, "conv1"), ","), "receipt") })
	items, _ := feed(h, "conv1")
	var rb struct{ State, Summary string }
	_ = json.Unmarshal(items[len(items)-1].Body, &rb)
	if rb.State != "failed" || !strings.Contains(rb.Summary, want) {
		t.Fatalf("receipt = %+v", rb)
	}
	waitFor(t, "the plan's error", func() bool {
		p := loadPlan(h, todo.PlanID)
		return p != nil && strings.Contains(p.Tasks[0].Error, want)
	})
	return todo
}

// A task whose agent the node refuses to start ends at once (no retry): no todo stays open without an agent.
func TestTaskStartFailedEndsTheTodo(t *testing.T) {
	h, tn := newTaskHub(t)
	tn.failStart = errors.New("launcher not found")
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	todo := failedStart(t, h, "launcher not found")
	if starts, _ := tn.calls(); len(starts) != 1 {
		t.Fatalf("a refused start was retried: %d starts", len(starts))
	}
	if _, ok := taskOfAgent(h, "box", "s1"); ok {
		t.Fatal("a failed task still follows an agent")
	}
	// Reopening it by hand clears the failure.
	mux := h.routes()
	r := todoRequest("PATCH", "/api/v1/todos/"+todo.ID, strings.NewReader(`{"done":false}`))
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reopen: %d %s", w.Code, w.Body)
	}
	if got := taskTodos(h)[0]; got.DoneAt != nil || got.StartError != "" {
		t.Fatalf("reopened todo = %+v", got)
	}
}

// retryReq posts a task's retry as the app does, through mux (the test's one h.routes(): building the routes
// writes Hub fields, so concurrent retries must share one).
func retryReq(h *Hub, mux http.Handler, todoID string) *httptest.ResponseRecorder {
	r := todoRequest("POST", "/api/v1/todos/"+todoID+"/retry", nil)
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// The task node answers the health probe separately from session startup. A fixed
// retry must observe that recovery before it dispatches; an empty RPC response
// is not a passing health check.
func waitForTaskLauncherRecovery(t *testing.T, h *Hub) {
	t.Helper()
	mux := h.routes()
	waitFor(t, "the launcher's passing health check", func() bool {
		r := todoRequest("GET", "/api/v1/launchers/health?machine=box", nil)
		r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("health: %d %s", w.Code, w.Body)
		}
		var res struct {
			Records []struct {
				State string `json:"state"`
			} `json:"records"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		return len(res.Records) > 0 && res.Records[0].State == "ok"
	})
}

// Retrying a task that did not start starts the same task again in the same todo (#1396): reopened and starting at
// once, the plan's error gone, the agent started with what the first start had, and linked; one receipt in all.
func TestTaskRetryStartsTheSameTask(t *testing.T) {
	h, tn := newTaskHub(t)
	tn.failStart = errors.New("claude did not become ready within 1m0s")
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	todo := failedStart(t, h, "did not become ready")
	waitForTaskLauncherRecovery(t, h)
	tn.mu.Lock()
	tn.failStart, tn.gate = nil, make(chan struct{})
	tn.mu.Unlock()

	mux := h.routes()
	w := retryReq(h, mux, todo.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	var res struct{ Todo Todo }
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Todo.ID != todo.ID || res.Todo.DoneAt != nil || res.Todo.StartError != "" {
		t.Fatalf("answered %+v", res.Todo)
	}
	if st := todoState(taskTodos(h)[0]); st != "not_started" {
		t.Fatalf("while it starts again the task is %q, not starting", st)
	}
	if p := loadPlan(h, todo.PlanID); p.Tasks[0].Error != "" {
		t.Fatalf("the plan still says %q", p.Tasks[0].Error)
	}
	close(tn.gate)
	waitFor(t, "the agent to link", func() bool { ts := taskTodos(h); return len(ts) == 1 && len(ts[0].Agents) == 1 })
	if got := taskTodos(h)[0]; got.ID != todo.ID || got.DoneAt != nil || got.StartError != "" {
		t.Fatalf("todo = %+v", got)
	}
	starts, _ := tn.calls()
	if len(starts) != 2 {
		t.Fatalf("%d starts", len(starts))
	}
	for _, k := range []string{"prompt", "cwd", "permission_mode", "launcher", "tab_name"} {
		if fmt.Sprint(starts[1][k]) != fmt.Sprint(starts[0][k]) {
			t.Errorf("retry's %s = %v, first start's %v", k, starts[1][k], starts[0][k])
		}
	}
	if got := strings.Join(feedKinds(h, "conv1"), ","); got != "plan,task,receipt" {
		t.Fatalf("feed = %s", got)
	}
	// Retrying a task that runs is nothing to do.
	if w := retryReq(h, mux, todo.ID); w.Code != http.StatusOK {
		t.Fatalf("retry of a running task: %d %s", w.Code, w.Body)
	}
	if starts, _ := tn.calls(); len(starts) != 2 {
		t.Fatalf("a running task was started again: %d starts", len(starts))
	}
}

// A retry that fails again ends the task again with the new error (todo and plan), and adds no second receipt.
func TestTaskRetryFailsAgain(t *testing.T) {
	h, tn := newTaskHub(t)
	tn.failStart = errors.New("claude did not become ready within 1m0s")
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	todo := failedStart(t, h, "did not become ready")
	waitForTaskLauncherRecovery(t, h)
	tn.mu.Lock()
	tn.failStart = errors.New("launcher not found")
	tn.mu.Unlock()
	mux := h.routes()
	if w := retryReq(h, mux, todo.ID); w.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	waitFor(t, "the retry to fail", func() bool {
		got := taskTodos(h)[0]
		return got.DoneAt != nil && strings.Contains(got.StartError, "launcher not found")
	})
	waitFor(t, "the plan's new error", func() bool {
		p := loadPlan(h, todo.PlanID)
		return p != nil && strings.Contains(p.Tasks[0].Error, "launcher not found")
	})
	if st := todoState(taskTodos(h)[0]); st != "failed" {
		t.Fatalf("taskState = %q", st)
	}
	time.Sleep(50 * time.Millisecond)
	if n := receipts(h); n != 1 {
		t.Fatalf("%d receipts", n)
	}
	// It can be retried again after the check recovers; a finished retry is not still marked as starting.
	// The second failure is within the automatic recheck's throttle window.
	// Exercise the supported explicit health check instead of waiting for a
	// second automatic probe that the policy deliberately does not schedule.
	r := todoRequest("POST", "/api/v1/launchers/health/check?machine=box&launcher=claude", nil)
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("explicit recovery check: %d %s", w.Code, w.Body)
	}
	waitForTaskLauncherRecovery(t, h)
	if w := retryReq(h, mux, todo.ID); w.Code != http.StatusOK {
		t.Fatalf("retry again: %d %s", w.Code, w.Body)
	}
	waitFor(t, "a third start", func() bool { starts, _ := tn.calls(); return len(starts) == 3 })
}

// Tapping "Retry" again and again while the agent starts starts it once: every tap answers the task as starting.
func TestTaskRetryTwiceStartsOnce(t *testing.T) {
	h, tn := newTaskHub(t)
	tn.failStart = errors.New("claude did not become ready within 1m0s")
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	todo := failedStart(t, h, "did not become ready")
	waitForTaskLauncherRecovery(t, h)
	tn.mu.Lock()
	tn.failStart, tn.gate = nil, make(chan struct{})
	tn.mu.Unlock()
	mux := h.routes()
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- retryReq(h, mux, todo.ID).Code
		}()
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != http.StatusOK {
			t.Fatalf("a tap answered %d", c)
		}
	}
	close(tn.gate)
	waitFor(t, "the agent to link", func() bool { ts := taskTodos(h); return len(ts) == 1 && len(ts[0].Agents) == 1 })
	time.Sleep(50 * time.Millisecond)
	if starts, _ := tn.calls(); len(starts) != 2 {
		t.Fatalf("%d starts: the taps started more than one agent", len(starts))
	}
}

// The machine offline: the retry is refused saying so, and the task stays failed as it was.
func TestTaskRetryWithTheMachineOffline(t *testing.T) {
	h, tn := newTaskHub(t)
	tn.failStart = errors.New("claude did not become ready within 1m0s")
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	todo := failedStart(t, h, "did not become ready")
	waitForTaskLauncherRecovery(t, h)
	m := h.byID["box"]
	m.mu.Lock()
	tnPeer := m.peer
	m.peer = nil
	m.mu.Unlock()
	mux := h.routes()
	w := retryReq(h, mux, todo.ID)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "离线") {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	if got := taskTodos(h)[0]; got.DoneAt == nil || !strings.Contains(got.StartError, "did not become ready") {
		t.Fatalf("todo = %+v", got)
	}
	// A refused retry is not marked as starting: back online, the next one starts it.
	m.mu.Lock()
	m.peer = tnPeer
	m.mu.Unlock()
	if w := retryReq(h, mux, todo.ID); w.Code != http.StatusOK {
		t.Fatalf("retry back online: %d %s", w.Code, w.Body)
	}
	waitFor(t, "the retried start", func() bool { starts, _ := tn.calls(); return len(starts) == 2 })
	// Not a failed start: a task done by hand, or a todo nobody dispatched, is not retried.
	tn.mu.Lock()
	tn.failStart = nil
	tn.mu.Unlock()
	m.mu.Lock()
	m.peer = nil
	m.mu.Unlock()
	plain := plainTodo(t, h, "买牛奶")
	if w := retryReq(h, mux, plain.ID); w.Code != http.StatusConflict {
		t.Fatalf("retry of a plain todo: %d %s", w.Code, w.Body)
	}
	if w := retryReq(h, mux, "todo_nope"); w.Code != http.StatusNotFound {
		t.Fatalf("retry of no todo: %d %s", w.Code, w.Body)
	}
}

// With the store: the retry reopens the stored todo once however many taps come in, and the stored plan loses its error.
func TestTaskRetryWithTheStore(t *testing.T) {
	database := setupTestDB(t)
	h := newTestHub(t)
	h.store = database
	tn := connectTaskNode(t, h, "box")
	connectAssistant(t, h)
	tn.failStart = errors.New("claude did not become ready within 1m0s")
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	todo := failedStart(t, h, "did not become ready")
	waitForTaskLauncherRecovery(t, h)
	tn.mu.Lock()
	tn.failStart, tn.gate = nil, make(chan struct{})
	tn.mu.Unlock()
	mux := h.routes()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := retryReq(h, mux, todo.ID); w.Code != http.StatusOK {
				t.Errorf("retry: %d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	if row, err := database.GetTodo(context.Background(), todo.ID); err != nil || row.DoneAt != nil || row.StartError != "" {
		t.Fatalf("stored todo = %+v %v", row, err)
	}
	if p := loadPlan(h, todo.PlanID); p.Tasks[0].Error != "" {
		t.Fatalf("the stored plan still says %q", p.Tasks[0].Error)
	}
	close(tn.gate)
	waitFor(t, "the agent to link", func() bool { ts := taskTodos(h); return len(ts) == 1 && len(ts[0].Agents) == 1 })
	if starts, _ := tn.calls(); len(starts) != 2 {
		t.Fatalf("%d starts", len(starts))
	}
}

// A plan that edits waits for the user; starting it takes their edits once, a second start or a dismiss is refused.
func TestTaskPlanWaitsForTheUser(t *testing.T) {
	h, tn := newTaskHub(t)
	res, err := taskPlan(h, "conv1", planArgs(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "nothing has started") {
		t.Fatalf("result = %q", res)
	}
	if len(taskTodos(h)) != 0 {
		t.Fatal("a plan that edits started without the user")
	}
	items, plans := feed(h, "conv1")
	if len(items) != 1 || items[0].Kind != "plan" || plans[items[0].Ref] == nil || plans[items[0].Ref].State != "open" {
		t.Fatalf("feed = %+v plans = %+v", items, plans)
	}
	id := items[0].Ref

	mux := h.routes()
	start := func(body string) *httptest.ResponseRecorder {
		r := todoRequest("POST", "/api/v1/task-plans/"+id+"/start", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := start(`{"tasks":[{"title":"修 iPad 横屏崩溃","model":"opus"}]}`); w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	waitFor(t, "the agent", func() bool { s, _ := tn.calls(); return len(s) == 1 })
	starts, _ := tn.calls()
	if starts[0]["model"] != "opus" {
		t.Fatalf("the edit did not reach the start: %+v", starts[0])
	}
	if ts := taskTodos(h); len(ts) != 1 || ts[0].Title != "修 iPad 横屏崩溃" {
		t.Fatalf("todos = %+v", ts)
	}
	if w := start(""); w.Code != http.StatusConflict {
		t.Fatalf("second start: %d", w.Code)
	}
	r := todoRequest("POST", "/api/v1/task-plans/"+id+"/dismiss", nil)
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("dismiss after start: %d", w.Code)
	}
}

// Answering a task's card leaves a line in the conversation; a message to a task goes to its agent.
func TestTaskAnsweredAndRouted(t *testing.T) {
	h, tn := newTaskHub(t)
	if _, err := taskPlan(h, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { ts := taskTodos(h); return len(ts) == 1 && len(ts[0].Agents) == 1 })
	todo := taskTodos(h)[0]
	h.byID["box"].setSessions([]proto.Session{{SessionID: "s1", Cwd: "/code/app", Agent: "claude", Status: proto.StatusWorking, UpdatedAt: time.Now()}})

	yes := true
	// The manager tells its own Agents business a card was answered (fleet_ix.go there), as the hub did.
	h.asst().InteractionResolved(context.Background(), assistantwire.InteractionResolved{Interaction: proto.Interaction{MachineID: "box",
		SessionID: "s1", Kind: proto.IxPermission, ToolName: "Bash"}, Resolution: proto.Resolution{Allow: &yes, Always: true}, Status: "resolved"})
	settleAssistant(h)
	items, _ := feed(h, "conv1")
	var ab struct{ Question, Answer string }
	_ = json.Unmarshal(items[len(items)-1].Body, &ab)
	if items[len(items)-1].Kind != "answered" || ab.Question != "使用 Bash" || ab.Answer != "批准了 · 以后同类不再问" {
		t.Fatalf("answered = %+v %+v", items[len(items)-1], ab)
	}

	mux := h.routes()
	body, _ := json.Marshal(map[string]string{"todo_id": todo.ID, "text": "第 5 条也改了"})
	r := todoRequest("POST", "/api/v1/assistant/conversations/conv1/route", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("route: %d %s", w.Code, w.Body)
	}
	_, sends := tn.calls()
	if len(sends) != 1 || sends[0]["session_id"] != "s1" || sends[0]["text"] != "第 5 条也改了" {
		t.Fatalf("session.send = %+v", sends)
	}
	// Another conversation cannot reach this task.
	r = todoRequest("POST", "/api/v1/assistant/conversations/other/route", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("route from another conversation: %d", w.Code)
	}

	r = todoRequest("GET", "/api/v1/assistant/conversations/conv1/feed", nil)
	r.Header.Set("Authorization", "Bearer "+h.cfg.Token)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var got struct {
		Items []assistant.FeedItem
		Plans map[string]*assistant.TaskPlan
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	kinds := []string{}
	for _, it := range got.Items {
		kinds = append(kinds, it.Kind)
	}
	if strings.Join(kinds, ",") != "plan,task,answered,routed" || len(got.Plans) != 1 {
		t.Fatalf("feed = %v plans = %d", kinds, len(got.Plans))
	}

	if line := taskLine(h, todo); !strings.HasSuffix(line, "：在做，box session_id=s1") {
		t.Fatalf("the live context = %q", line)
	}
}

// todoState is a task's state as far as its todo tells it (docs/assistant/todos.md "State calculation rules"): failed, done, not started.
func todoState(t Todo) string {
	switch {
	case t.DoneAt != nil && t.StartError != "":
		return "failed"
	case t.DoneAt != nil:
		return "done"
	case len(t.Agents) == 0:
		return "not_started"
	}
	return "open"
}
