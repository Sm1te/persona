package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A supervised task end to end (#3124): the hub's HTTP stack, the assistant over the bus with its state in Postgres,
// fake nodes for the main conversation's machine and the agent's, a judge model whose verdict the test sets. The main
// conversation hands out a task with done_when; its agent stops asking which way to go and the main conversation
// gets a supervise message whose turn is not pushed; the agent is done and the task waits for review (not closed); rework
// goes back to the agent; the next stop waits for review again; accepted closes it, done by review.
func TestSupervisedTaskOverTheBus(t *testing.T) {
	h, srv, _ := newBusHub(t)
	store := managerDB(t, h) // the assistant's state: its settings and tables
	ctx := context.Background()
	keys := []string{"assistant", "assistant.tasks", "assistant.routines", "assistant.followups", "assistant.runs",
		"assistant.titles", "assistant.archived", "assistant.relay"}
	clear := func() {
		for _, k := range keys {
			_ = store.PutSetting(context.Background(), k, nil)
		}
	}
	clear()
	t.Cleanup(clear)
	if list, err := store.ListTodos(ctx); err == nil { // other tests' tasks in the same database
		for _, r := range list {
			_ = store.DeleteTodo(ctx, r.ID)
		}
	}
	old := taskSettleDelay
	taskSettleDelay = func(NotifyConfig) time.Duration { return 0 }
	t.Cleanup(func() { taskSettleDelay = old })
	t.Cleanup(assistant.SetSuperviseTimings(50*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond))

	var verdict atomic.Value
	verdict.Store(`{"verdict":"needs_user","reason":"问用两种部署走法里的哪一种"}`)
	judge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": verdict.Load().(string)}}})
	}))
	t.Cleanup(judge.Close)
	key := filepath.Join(t.TempDir(), "key")
	_ = os.WriteFile(key, []byte("k"), 0o600)
	setManagerJSON(t, h, "inbox", map[string]any{"triage_ai": ai.Config{BaseURL: judge.URL, APIKeyFile: key, Model: "m"}})

	n := newAssistantNode(t, h) // the main conversation's machine, "server"
	n.m.info.Home = "/home/me"
	editSessions(n.m, func([]proto.Session) []proto.Session {
		return []proto.Session{{SessionID: "main-1", Cwd: convDir, Title: "Persona", UpdatedAt: time.Now().UTC()}}
	})
	n.known["main-1"] = true
	box := connectTaskNode(t, h, "box")
	// The main conversation supervises its own tasks here: the background supervisor that takes them by default (#4927)
	// is internal/assistant/supervise_background_test.go's.
	mainSupervises := true
	if err := store.PutSetting(ctx, "assistant", assistant.Settings{MachineID: "server", Main: "main-1", Timezone: "Asia/Shanghai",
		MainSupervisesOwnTasks: &mainSupervises}); err != nil {
		t.Fatal(err)
	}
	dialAssistant(t, h, srv.URL, true)

	mcp := func(name string, args any) string {
		t.Helper()
		raw, _ := json.Marshal(args)
		params, _ := json.Marshal(map[string]any{"name": name, "args": json.RawMessage(raw), "session_id": "main-1", "assistant": true})
		res, err := nodeAsks(ctx, n.m, "mcp.call", params)
		if err != nil {
			t.Fatal(err)
		}
		r := res.(proto.ToolResult)
		if r.IsError {
			t.Fatalf("%s: %s", name, textOf(r))
		}
		return textOf(r)
	}
	// The supervise messages sent to the main conversation, recognized the way the hub reads them back: the supervise
	// template behind the message-source block (#2156), whose source is supervision. The text no longer starts with
	// the template, so a prefix match finds none.
	digests := func() []string {
		n.mu.Lock()
		defer n.mu.Unlock()
		var out []string
		for _, s := range n.sends {
			text, _ := s["text"].(string)
			in := prompts.ParseInjected(text)
			source := prompts.ItemSource(proto.Item{Role: "user", Kind: proto.KindText, Text: text})
			if in != nil && in.State == "supervise" && source != nil && source.Kind == proto.SourceSupervision {
				out = append(out, text)
			}
		}
		return out
	}
	todoOf := func() Todo {
		t.Helper()
		ts := taskTodos(h)
		if len(ts) != 1 {
			t.Fatalf("%d tasks", len(ts))
		}
		return ts[0]
	}
	agent := func(status, text string) {
		box := h.byID["box"]
		st := proto.StateUnread
		if status == proto.StatusWorking {
			st = proto.StateWorking
		}
		box.setSessions([]proto.Session{{SessionID: "s1", Cwd: "/code/app", Agent: "claude", Status: status, State: st, LastText: text,
			UpdatedAt: time.Now()}})
	}

	// Handed out with what done means: the agent is told it.
	res := mcp("task_plan", map[string]any{"tasks": []map[string]any{{"title": "部署 hub", "prompt": "把 hub 部署到测试环境并验证",
		"machine": "box", "cwd": "/code/app", "readonly": false, "permission": "bypassPermissions", "user_requested": true, "why": "部署脚本在 box 上",
		"done_when": []string{"测试 hub 已部署并验证"}, "expect_minutes": 20}}})
	if !strings.Contains(res, "You supervise them") {
		t.Fatalf("task_plan: %s", res)
	}
	eventually(t, 10*time.Second, "the agent", func() bool { s, _ := box.calls(); return len(s) == 1 })
	if starts, _ := box.calls(); !strings.Contains(starts[0]["prompt"].(string), "- 测试 hub 已部署并验证") {
		t.Fatalf("prompt: %v", starts[0]["prompt"])
	}
	eventually(t, 10*time.Second, "the link", func() bool { ts := taskTodos(h); return len(ts) == 1 && len(ts[0].Agents) == 1 })

	// It stops to ask which way: the main conversation hears it, and its answer is not pushed.
	agent(proto.StatusWorking, "")
	agent(proto.StatusDone, "两种走法：A 先灰度，B 直接全量。你选一个？")
	eventually(t, 10*time.Second, "the 督办 message", func() bool { return len(digests()) == 1 })
	if d := digests()[0]; !strings.Contains(d, "Agent 停下了，但没做完（要用户决定：问用两种部署走法里的哪一种）") ||
		!strings.Contains(d, "验收标准：测试 hub 已部署并验证") || !strings.Contains(d, "A 先灰度") {
		t.Fatalf("message:\n%s", d)
	}
	eventually(t, 10*time.Second, "the hold on its pushes", func() bool { return shown(h, "main-1").Supervising })

	// The assistant decided (it tells the agent; the fake node's ids are too short for agent_send); the agent is done:
	// waits for review, not closed.
	verdict.Store(`{"verdict":"done","reason":"已部署"}`)
	agent(proto.StatusWorking, "")
	agent(proto.StatusDone, "按 A 部署好了")
	eventually(t, 10*time.Second, "the review message", func() bool { return len(digests()) == 2 })
	if d := digests()[1]; !strings.Contains(d, "hub 判断做完了，等你验收（已部署）") {
		t.Fatalf("message:\n%s", d)
	}
	todo := todoOf()
	if todo.DoneAt != nil {
		t.Fatalf("closed by the judge: %+v", todo)
	}
	// The apps get the ledger on the todo.
	var view struct {
		Todos []struct {
			ID          string `json:"id"`
			Supervision *struct {
				State    string   `json:"state"`
				DoneWhen []string `json:"done_when"`
				Round    int      `json:"round"`
			} `json:"supervision"`
		} `json:"todos"`
	}
	if code := assistantGet(h, "/api/v1/todos", &view); code != http.StatusOK || len(view.Todos) != 1 || view.Todos[0].Supervision == nil ||
		view.Todos[0].Supervision.State != "pending_review" || len(view.Todos[0].Supervision.DoneWhen) != 1 {
		t.Fatalf("GET /todos: %d %+v", code, view)
	}

	// Rework: the gap goes back to the agent.
	res = mcp("task_review", map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "没给验证的输出，贴上 /healthz 的结果",
		"checks": []map[string]any{{"criterion": "测试 hub 已部署并验证", "met": false, "evidence": "只说部署好了"}}})
	if !strings.Contains(res, "Rework round 1 of 2") {
		t.Fatalf("rework: %s", res)
	}
	eventually(t, 10*time.Second, "the rework at the agent", func() bool {
		_, sends := box.calls()
		return len(sends) == 1 && strings.Contains(sends[0]["text"].(string), "第 1 轮返工") &&
			strings.Contains(sends[0]["text"].(string), "贴上 /healthz 的结果")
	})
	agent(proto.StatusWorking, "")
	agent(proto.StatusDone, "部署好了，/healthz 返回 ok")
	eventually(t, 10*time.Second, "the review again", func() bool { return len(digests()) == 3 })

	// Accepted: closed, by review; the hub's view says so.
	if res := mcp("task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "测试 hub 已部署，/healthz ok",
		"checks": []map[string]any{{"criterion": "测试 hub 已部署并验证", "met": true, "evidence": "/healthz 返回 ok"}}}); !strings.Contains(res, "accepted and closed") {
		t.Fatalf("accepted: %s", res)
	}
	eventually(t, 10*time.Second, "closed", func() bool { d := todoOf(); return d.DoneAt != nil && d.DoneBy == "review" })
	rows, err := store.ListTodoSupervision(ctx)
	if err != nil || len(rows) != 1 || rows[0].State != "accepted" || rows[0].Round != 1 {
		t.Fatalf("stored: %+v, %v", rows, err)
	}
	if got := len(digests()); got != 3 {
		t.Fatalf("%d messages", got)
	}
}
