package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A task an ordinary conversation (neither the main one nor a leader) hands out with task_plan is that conversation's
// to supervise: it has a ledger, its supervision messages go back to it, and it reviews it with task_review.
func TestSideConversationSupervisesItsOwnTask(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	f.known["side1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title": "修登录崩溃", "prompt": "iPad 横屏时登录页崩溃，修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": true,
		"why": "这个仓库最近在 box 上", "done_when": []string{"PR 合并到 main"}, "expect_minutes": 30,
	}}})
	res, err := s.toolTaskPlan(ctx, "side1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "You supervise them: a supervision message comes when one needs you") {
		t.Fatalf("the planning conversation is not told it supervises: %s", res)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || v.Supervisor != "side1" || todo.Origin != "side1" || len(v.DoneWhen) != 1 {
		t.Fatalf("ledger = %+v (origin %q)", todo.Supervision, todo.Origin)
	}

	// Its supervision message goes back to the side conversation, not to the main one.
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	waitFor(t, "the side conversation's supervision message", func() bool {
		for _, n := range f.noticesTo("side1") {
			if strings.Contains(n, "📋 督办") {
				return true
			}
		}
		return false
	})
	for _, n := range f.noticesTo("conv1") {
		if strings.Contains(n, "📋 督办") {
			t.Fatalf("the main conversation got the side conversation's supervision message:\n%s", n)
		}
	}

	// The side conversation reviews it; nobody gets an end notice (it supervised it itself).
	if _, err := runJSON(t, s, "side1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "PR 已合并",
		"checks": []map[string]any{{"criterion": "PR 合并到 main", "met": true, "evidence": "pull/7"}}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task closed", func() bool {
		t, ok := s.todoNow(todo.ID)
		return ok && t.DoneAt != nil
	})
	if n := endNotices(f, "side1"); len(n) != 0 {
		t.Fatalf("the side conversation got an end notice for its own task:\n%s", strings.Join(n, "\n"))
	}
}

// The main conversation's own tasks, and a leader's, are as before: no end notice anywhere, the supervisor is the
// conversation that planned them. A hub with no main conversation changes nothing for a side conversation's own task.
func TestPlanSupervisorDefaults(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	for _, c := range []struct {
		conv, parent, want string
		ok                 bool
	}{
		{"side1", "", "side1", true},   // a live side conversation supervises its own task
		{"gone", "", "conv1", true},    // no live conversation to tell: the main conversation as before (#4669)
		{"conv1", "", "", false},       // the main conversation supervises its own (supervises)
		{"side1", "box/s1", "", false}, // a worker's leader supervises it
		{"", "", "", false},            // no conversation at all
		{"lead1", "", "", false},       // a leader supervises its own (supervises)
	} {
		if got, ok := s.defaultPlanSupervisor(ctx, c.conv, c.parent); got != c.want && ok || ok != c.ok {
			t.Errorf("defaultPlanSupervisor(%q, %q) = %q %v, want %q %v", c.conv, c.parent, got, ok, c.want, c.ok)
		}
	}
	setSettings(t, s, func(v *Settings) { v.Main = "" })
	if got, ok := s.defaultPlanSupervisor(ctx, "side1", ""); !ok || got != "side1" {
		t.Fatalf("no main conversation, a live side conversation: %q %v, want side1", got, ok)
	}
	if _, ok := s.defaultPlanSupervisor(ctx, "gone", ""); ok {
		t.Fatal("no main conversation and no live conversation, yet a supervisor")
	}
}

// endNotices are the task-end notices sent into conversation id.
func endNotices(f *fakeHub, id string) []string {
	var out []string
	for _, n := range f.noticesTo(id) {
		if strings.Contains(n, "这段对话派出去的任务") {
			out = append(out, n)
		}
	}
	return out
}
