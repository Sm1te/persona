package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// newQuietService is newTaskService with the main conversation conv1 (idle, on machine server), a short notice
// delay and supervision messages (supervise_digest.go) that come at once and often.
func newQuietService(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	return newQuietServiceOn(t, nil)
}

// newQuietServiceOn is newQuietService with st as its database (nil: none).
func newQuietServiceOn(t *testing.T, st Store) (*Service, *fakeHub) {
	t.Helper()
	old := quietNoticeDelay
	quietNoticeDelay = 30 * time.Millisecond
	t.Cleanup(func() { quietNoticeDelay = old })
	reachA, reachB, reachM := agentReachAttempts, agentReachBackoff, agentReachMax
	agentReachAttempts, agentReachBackoff, agentReachMax = 2, time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { agentReachAttempts, agentReachBackoff, agentReachMax = reachA, reachB, reachM })
	s, f := newTaskServiceOn(t, st)
	shortSupervise(t, s)
	f.hello.Assistant.MachineID = "server"
	setSettings(t, s, func(v *Settings) { v.Main = "conv1" })
	f.known["conv1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	return s, f
}

// sessionsListCalls counts the sessions.list calls one machine got.
func (f *fakeHub) sessionsListCalls(machine string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Device == machine && c.Method == DeviceSessionsList {
			n++
		}
	}
	return n
}

// notices are the texts sent into the main conversation.
func (f *fakeHub) notices() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.sends {
		if a["session_id"] == "conv1" {
			text, _ := a["text"].(string)
			out = append(out, text)
		}
	}
	return out
}

// agentTurn sets the task agent s1 on box and tells the assistant its turn ended (or started).
func agentTurn(s *Service, f *fakeHub, status, text string, pending int) {
	ctx := context.Background()
	st := proto.StateUnread
	if pending > 0 {
		st = proto.StateNeedsYou
	}
	if status == proto.StatusWorking {
		st = proto.StateWorking
	}
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Cwd: "/code/app", Agent: "claude", Status: status, State: st,
		LastText: text, PendingInteractions: pending, UpdatedAt: time.Now()})
	if status == proto.StatusWorking {
		s.taskWorking(ctx, "box", "s1")
	} else {
		s.taskTurnEnded(ctx, "box", "s1")
	}
}

// A task the main conversation opens is quiet and supervised: when it is done, the main conversation gets one supervision
// message with what its agent said, as is (not wrapped like a message from another conversation), and the task waits
// for review (not closed); the receipt is there as always.
func TestQuietTaskTellsTheMainConversation(t *testing.T) {
	s, f := newQuietService(t)
	f.judge = `{"verdict":"done","reason":"修好了"}`
	todo := startedTask(t, s, f)
	if !todo.Quiet || todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" {
		t.Fatalf("a task from the main conversation is not quiet and supervised: %+v", todo)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "修好了，提了 https://github.com/o/r/pull/901", 0)
	waitFor(t, "the notice", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	for _, want := range []string{"📋 督办", "任务「修登录崩溃」（" + todo.ID + "）：hub 判断做完了，等你验收（修好了）；在 box", "修好了，提了 https://github.com/o/r/pull/901", "不是用户发的"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice lacks %q:\n%s", want, n)
		}
	}
	if strings.Contains(n, "来自对话") {
		t.Errorf("the notice went in as a relay:\n%s", n)
	}
	if !strings.Contains(feedKinds(s, "conv1"), "receipt") {
		t.Error("no receipt")
	}
	if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Supervision.State != reviewPending {
		t.Fatalf("todo = %+v, %+v", now, now.Supervision)
	}
	time.Sleep(3 * quietNoticeDelay)
	if got := len(f.notices()); got != 1 {
		t.Fatalf("%d notices", got)
	}
}

// A question and the end of the work within the delay are one message: the question answered meanwhile is left out.
// A question answered before the message goes out is no message.
func TestQuietNoticesMerge(t *testing.T) {
	s, f := newQuietService(t)
	f.judge = `{"verdict":"done","reason":"做完了"}`
	startedTask(t, s, f)
	ix := proto.Interaction{ID: "ix1", MachineID: "box", SessionID: "s1", Kind: proto.IxQuestion,
		Questions: []proto.Question{{Question: "用哪个分支？", Options: []proto.QuestionOption{{Label: "main"}, {Label: "dev"}}}}}
	agentTurn(s, f, proto.StatusBlocked, "", 1)
	s.InteractionRaised(context.Background(), InteractionRaised{Interaction: ix})
	agentTurn(s, f, proto.StatusDone, "用 main 做完了", 0)
	waitFor(t, "the notice", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	if !strings.Contains(n, "做完了，等你验收") || strings.Contains(n, "用哪个分支") || strings.Count(n, "任务「") != 1 {
		t.Fatalf("notice:\n%s", n)
	}
	time.Sleep(3 * quietNoticeDelay)
	if got := len(f.notices()); got != 1 {
		t.Fatalf("%d notices", got)
	}

	s2, f2 := newQuietService(t)
	startedTask(t, s2, f2)
	agentTurn(s2, f2, proto.StatusBlocked, "", 1)
	s2.InteractionRaised(context.Background(), InteractionRaised{Interaction: ix})
	agentTurn(s2, f2, proto.StatusWorking, "", 0) // answered on the watch: working again
	time.Sleep(4 * quietNoticeDelay)
	if got := f2.notices(); len(got) != 0 {
		t.Fatalf("a question answered meanwhile was told: %q", got)
	}

	s3, f3 := newQuietService(t)
	startedTask(t, s3, f3)
	agentTurn(s3, f3, proto.StatusBlocked, "", 1)
	s3.InteractionRaised(context.Background(), InteractionRaised{Interaction: ix})
	waitFor(t, "the question's notice", func() bool { return len(f3.notices()) == 1 })
	if n := f3.notices()[0]; !strings.Contains(n, "Agent 在等回答") || !strings.Contains(n, "用户没收到推送") ||
		!strings.Contains(n, "用哪个分支？（选项：main / dev）") {
		t.Fatalf("notice:\n%s", n)
	}
}

// While the main conversation runs, the notice waits behind its turn; a newer one about the same task takes its
// place in the queue instead of following it.
func TestQuietNoticeWaitsForTheTurn(t *testing.T) {
	s, f := newQuietService(t)
	f.judge = `{"verdict":"done","reason":"做完了"}`
	startedTask(t, s, f)
	busy := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", busy)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "第一次做完", 0)
	waitFor(t, "the notice to queue", func() bool { return s.queuedFor("conv1") == 1 })
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "第二次做完", 0)
	time.Sleep(4 * shortSuperviseTimings.every) // the next message is due: it takes the queued one's place
	if n := s.queuedFor("conv1"); n != 1 {
		t.Fatalf("%d queued", n)
	}
	if q, _ := s.queuedRelays(); len(q["conv1"]) != 1 || q["conv1"][0] != "督办消息" {
		t.Fatalf("shown waiting: %q", q["conv1"])
	}
	if len(f.notices()) != 0 {
		t.Fatal("sent while the main conversation runs")
	}
	idle := busy
	idle.Status = proto.StatusIdle
	f.setSessions("server", idle)
	s.drainRelays("server", []proto.Session{idle})
	waitFor(t, "the queued notice", func() bool { return len(f.notices()) == 1 })
	if n := f.notices()[0]; !strings.Contains(n, "第二次做完") || strings.Contains(n, "第一次做完") || strings.Contains(n, "来自对话") {
		t.Fatalf("notice:\n%s", n)
	}
	if !s.supervising("conv1") {
		t.Fatal("the turn the message started is pushed")
	}
}

// Tasks from another conversation are not quiet: the hub pushes as always, and the planning conversation supervises
// them itself (a supervision message, not a quiet notice). With the setting off a task from the main conversation is
// not quiet (the hub pushes) but is still supervised.
func TestNotQuietTasksAreNotTold(t *testing.T) {
	for _, c := range []struct {
		name       string
		set        func(*Settings)
		supervisor string
		told       bool
	}{
		{"side conversation", func(v *Settings) { v.Main = "conv-main" }, "conv1", true},
		{"setting off", func(v *Settings) { v.MainTasksQuiet = new(false) }, "conv1", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, f := newQuietService(t)
			f.judge = `{"verdict":"done","reason":"好了"}`
			setSettings(t, s, c.set)
			if todo := startedTask(t, s, f); todo.Quiet || todo.Supervision == nil || todo.Supervision.Supervisor != c.supervisor {
				t.Fatalf("todo = %+v (supervision %+v)", todo, todo.Supervision)
			}
			agentTurn(s, f, proto.StatusWorking, "", 0)
			agentTurn(s, f, proto.StatusDone, "好了", 0)
			time.Sleep(4 * quietNoticeDelay)
			if told := len(f.notices()) > 0; told != c.told {
				t.Fatalf("told = %v: %q", told, f.notices())
			}
			if c.name == "side conversation" {
				for _, n := range f.notices() {
					if !strings.Contains(n, "📋 督办") {
						t.Fatalf("a side conversation's message is not its supervision message:\n%s", n)
					}
				}
			}
		})
	}
}

// A task whose agent does not start is told too.
func TestQuietTaskFailedStart(t *testing.T) {
	s, f := newQuietService(t)
	s.taskRetry = 50 * time.Millisecond // the start fails again on the retry; the notice comes after it
	f.failStart = errLauncherGone
	if _, err := s.toolTaskPlan(context.Background(), "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the notice", func() bool { return len(f.notices()) == 1 })
	if n := f.notices()[0]; !strings.Contains(n, "没开起来") || !strings.Contains(n, "no such launcher") {
		t.Fatalf("notice:\n%s", n)
	}
}

var errLauncherGone = errors.New("session.start: no such launcher")

// agent_start from the main conversation is a quiet task (card, link); from another conversation or with the setting
// off it stays an agent of its own.
func TestAgentStartFromTheMainConversation(t *testing.T) {
	args := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI 为什么挂了"}`)
	s, _ := newQuietService(t)
	res, err := s.runTool(context.Background(), "server", "conv1", "agent_start", args)
	if err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	if !todo.Quiet || todo.Origin != "conv1" || len(todo.Agents) != 1 || todo.Agents[0].SessionID != "s1" || todo.Title != "看看 CI 为什么挂了" ||
		todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" {
		t.Fatalf("todo = %+v", todo)
	}
	if !strings.Contains(res, "a task of this conversation") || feedKinds(s, "conv1") != "task" {
		t.Fatalf("result %q, feed %q", res, feedKinds(s, "conv1"))
	}

	for _, c := range []struct {
		conv string
		off  bool
	}{{"conv2", false}, {"conv1", true}} {
		s, _ := newQuietService(t)
		if c.off {
			setSettings(t, s, func(v *Settings) { v.MainTasksQuiet = new(false) })
		}
		if _, err := s.runTool(context.Background(), "server", c.conv, "agent_start", args); err != nil {
			t.Fatal(err)
		}
		if todoCount(s) != 0 || feedKinds(s, c.conv) != "" {
			t.Fatalf("%s (off %v): todos %d, feed %q", c.conv, c.off, todoCount(s), feedKinds(s, c.conv))
		}
	}
}

func TestGoldenTaskNotice(t *testing.T) {
	for _, c := range []struct {
		name string
		n    quietNotice
	}{
		{"done", quietNotice{todo: Todo{Title: "修登录崩溃"}, machine: "box", state: "done", text: "修好了，PR：https://github.com/o/r/pull/1"}},
		{"needs", quietNotice{todo: Todo{Title: "修登录崩溃"}, machine: "box", state: "needs", ask: "- 用哪个分支？（选项：main / dev）"}},
		{"interrupted_after_needs", quietNotice{todo: Todo{Title: "整理材料"}, state: "interrupted", earlier: []string{"等过用户"}, text: "API Error: 529 overloaded"}},
		{"failed", quietNotice{todo: Todo{Title: "整理材料"}, machine: "box", state: "failed", text: "box 现在离线"}},
	} {
		golden(t, "task_notice."+c.name, renderNotice(&c.n, false))
	}
}

// A conversation started with report_tasks (a plugin's /agent, #3192) is told about the tasks it opens, as the main
// conversation is about its quiet ones: the task is quiet, its notice goes into that conversation (not the main one)
// and says to hand the result on as that conversation was told to; agent_start from it is a task too.
func TestReportingConversationIsToldItsTasks(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lark1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	s.markReporter(ctx, "server", "lark1")
	if _, err := s.toolTaskPlan(ctx, "lark1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	if todo := onlyTodo(s); !todo.Quiet || todo.Origin != "lark1" {
		t.Fatalf("todo = %+v", todo)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "MR 提好了：https://example.com/mr/7", 0)
	sentTo := func(id string) []string {
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
	waitFor(t, "the notice", func() bool { return len(sentTo("lark1")) == 1 })
	n := sentTo("lark1")[0]
	for _, want := range []string{"🔔 任务「修登录崩溃」做完了（box）", "https://example.com/mr/7", "从这段对话派出去的", "照这段对话一开始说的方式交代"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice lacks %q:\n%s", want, n)
		}
	}
	if strings.Contains(n, "主对话") {
		t.Errorf("the notice talks of the main conversation:\n%s", n)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told too: %q", got)
	}

	s2, _ := newQuietService(t)
	setSettings(t, s2, func(v *Settings) { v.MainTasksQuiet = new(false) }) // the main conversation's setting: not this one's
	s2.markReporter(ctx, "server", "lark1")
	args := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"提个 MR 修掉它"}`)
	if _, err := s2.runTool(ctx, "server", "lark1", "agent_start", args); err != nil {
		t.Fatal(err)
	}
	if todo := onlyTodo(s2); !todo.Quiet || todo.Origin != "lark1" || len(todo.Agents) != 1 {
		t.Fatalf("agent_start from a reporting conversation: todo = %+v", todo)
	}
}

// POST /api/v1/assistant/conversations with report_tasks marks the new conversation; without it, none is.
func TestStartWithReportTasks(t *testing.T) {
	s, _ := newQuietService(t)
	if w := serve(s, "POST", "/api/v1/assistant/conversations", `{"prompt":"/agent 看看","report_tasks":true}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if m, ok := s.reporterOf(context.Background(), "s1"); !ok || m != "server" {
		t.Fatalf("reporter = %q, %v", m, ok)
	}
	if w := serve(s, "POST", "/api/v1/assistant/conversations", `{"prompt":"随便聊聊"}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if _, ok := s.reporterOf(context.Background(), "s2"); ok {
		t.Fatal("a conversation started without report_tasks reports")
	}
}

func TestGoldenTaskNoticeReporting(t *testing.T) {
	golden(t, "task_notice.reporting_done", renderNotice(&quietNotice{todo: Todo{Title: "提 MR"}, machine: "box", state: "done", text: "MR：https://example.com/mr/7"}, true))
	golden(t, "task_notice.reporting_needs", renderNotice(&quietNotice{todo: Todo{Title: "提 MR"}, machine: "box", state: "needs", ask: "- 合到哪个分支？"}, true))
}

// A needs notice already queued behind its conversation's turn is dropped when the user deals with the card
// elsewhere (phone, watch): an answered question never pings. Another card of the same task still waiting keeps
// the notice; an answer inside the delay window takes the waiting notice with it.
func TestQuietNeedsNoticeDroppedWhenAnswered(t *testing.T) {
	ix := proto.Interaction{ID: "ix1", MachineID: "box", SessionID: "s1", Kind: proto.IxQuestion,
		Questions: []proto.Question{{Question: "用哪个分支？", Options: []proto.QuestionOption{{Label: "main"}, {Label: "dev"}}}}}
	resolution := proto.Resolution{Answers: map[string]string{"用哪个分支？": "main"}}
	for _, tc := range []struct {
		name       string
		queued     bool // let the notice enter the relay queue first (otherwise the answer comes inside the delay window)
		stillWaits bool // another card of the same task still waits when the answer arrives
		wantSent   int
	}{
		{"queued notice answered", true, false, 0},
		{"queued notice, another card still waits", true, true, 1},
		{"answered inside the delay window", false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newQuietService(t)
			ctx := context.Background()
			f.known["lark1"] = true
			idle1 := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			busy1 := proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
			f.setSessions("server", idle1, busy1)
			s.markReporter(ctx, "server", "lark1")
			if _, err := s.runTool(ctx, "server", "lark1", "agent_start",
				json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`)); err != nil {
				t.Fatal(err)
			}
			todo := onlyTodo(s)
			agentTurn(s, f, proto.StatusBlocked, "", 1)
			s.InteractionRaised(ctx, InteractionRaised{Interaction: ix})
			if tc.queued {
				waitFor(t, "the notice to queue", func() bool { return s.queuedFor("lark1") == 1 })
			}
			pending, status := 0, proto.StatusWorking
			if tc.stillWaits {
				pending, status = 1, proto.StatusBlocked
			}
			agentTurn(s, f, status, "", pending) // the card is dealt with, or another one still waits
			s.InteractionResolved(ctx, InteractionResolved{Interaction: ix, Resolution: resolution, Status: "resolved"})
			if !tc.queued {
				s.quiet.mu.Lock()
				_, waiting := s.quiet.pending[todo.ID]
				s.quiet.mu.Unlock()
				if waiting {
					t.Fatal("the waiting notice stayed in its delay window after the answer")
				}
			}
			if q := s.queuedFor("lark1"); q != tc.wantSent {
				t.Fatalf("%d queued, want %d", q, tc.wantSent)
			}
			idleLark := busy1
			idleLark.Status = proto.StatusIdle
			f.setSessions("server", idle1, idleLark)
			s.drainRelays("server", []proto.Session{idleLark})
			time.Sleep(4 * quietNoticeDelay)
			if got := f.sentTo("lark1"); got != tc.wantSent {
				t.Fatalf("%d notice(s) sent, want %d", got, tc.wantSent)
			}
		})
	}
}

// The answer arrives while the node still reports blocked (the production order: the resolve goroutine runs before
// the node flips its session): the hook cannot confirm the answer, so the stale notice lives on until the drain
// asks once more right before delivery. A node unreachable then is not an answer — the notice waits for the next
// drain, which drops it once the node is back and no card waits. ErrNoDevice is a definite answer too, not a
// flaky node: that stale notice is dropped at the same drain.
func TestQueuedNeedsNoticeRecheckedAtDrain(t *testing.T) {
	ix := proto.Interaction{ID: "ix1", MachineID: "box", SessionID: "s1", Kind: proto.IxQuestion,
		Questions: []proto.Question{{Question: "用哪个分支？"}}}
	resolution := proto.Resolution{Answers: map[string]string{"用哪个分支？": "main"}}
	for _, tc := range []struct {
		name string
		mode string // caught: the node caught up; unreachable: offline at first, back later; removed: hub forgot it
	}{
		{"node caught up before the drain", "caught"},
		{"node unreachable at the drain, back and answered later", "unreachable"},
		{"the hub no longer knows the machine", "removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newQuietService(t)
			ctx := context.Background()
			f.known["lark1"] = true
			idle1 := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
			busy1 := proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
			f.setSessions("server", idle1, busy1)
			s.markReporter(ctx, "server", "lark1")
			if _, err := s.runTool(ctx, "server", "lark1", "agent_start",
				json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`)); err != nil {
				t.Fatal(err)
			}
			agentTurn(s, f, proto.StatusBlocked, "", 1)
			s.InteractionRaised(ctx, InteractionRaised{Interaction: ix})
			waitFor(t, "the notice to queue", func() bool { return s.queuedFor("lark1") == 1 })
			s.InteractionResolved(ctx, InteractionResolved{Interaction: ix, Resolution: resolution, Status: "resolved"})
			if q := s.queuedFor("lark1"); q != 1 {
				t.Fatalf("a still-blocked node was read as answered: %d queued", q)
			}
			idleLark := busy1
			idleLark.Status = proto.StatusIdle
			switch tc.mode {
			case "unreachable":
				f.sessionsFail["box"] = 100
				s.drainRelays("server", []proto.Session{idleLark})
				time.Sleep(50 * time.Millisecond) // longer than the bounded retries, far below their cap
				if q, sent := s.queuedFor("lark1"), f.sentTo("lark1"); q != 1 || sent != 0 {
					t.Fatalf("an unreachable node was read as answered: %d queued, %d sent", q, sent)
				}
				f.sessionsFail["box"] = 0
				agentTurn(s, f, proto.StatusWorking, "", 0) // the node is back and the agent continues
				s.drainRelays("server", []proto.Session{idleLark})
			case "removed":
				f.mu.Lock()
				delete(f.sessions, "box")
				f.mu.Unlock()
				s.drainRelays("server", []proto.Session{idleLark})
			default:
				agentTurn(s, f, proto.StatusWorking, "", 0) // the node caught up with the answer
				s.drainRelays("server", []proto.Session{idleLark})
			}
			waitFor(t, "the stale notice to leave the queue", func() bool { return s.queuedFor("lark1") == 0 })
			time.Sleep(2 * quietNoticeDelay)
			if got := f.sentTo("lark1"); got != 0 {
				t.Fatalf("%d stale notice(s) sent", got)
			}
		})
	}
}

// A done notice that replaced the task's earlier needs notice in the queue is no stale question: answering the
// card after the agent finished must not take that done receipt with it.
func TestQueuedDoneNoticeSurvivesAnsweredCard(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lark1"] = true
	idle1 := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	busy1 := proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", idle1, busy1)
	s.markReporter(ctx, "server", "lark1")
	if _, err := s.runTool(ctx, "server", "lark1", "agent_start",
		json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"看看 CI"}`)); err != nil {
		t.Fatal(err)
	}
	ix := proto.Interaction{ID: "ix1", MachineID: "box", SessionID: "s1", Kind: proto.IxQuestion,
		Questions: []proto.Question{{Question: "用哪个分支？"}}}
	agentTurn(s, f, proto.StatusBlocked, "", 1)
	s.InteractionRaised(ctx, InteractionRaised{Interaction: ix})
	waitFor(t, "the needs notice to queue", func() bool { return s.queuedFor("lark1") == 1 })
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	waitFor(t, "the done notice to take its place", func() bool {
		s.relays.mu.Lock()
		defer s.relays.mu.Unlock()
		q := s.relays.st.Queue["lark1"]
		return len(q) == 1 && q[0].State == "done"
	})
	s.InteractionResolved(ctx, InteractionResolved{Interaction: ix,
		Resolution: proto.Resolution{Answers: map[string]string{"用哪个分支？": "main"}}, Status: "resolved"})
	if q := s.queuedFor("lark1"); q != 1 {
		t.Fatalf("the done notice was dropped as a stale question: %d queued", q)
	}
	idleLark := busy1
	idleLark.Status = proto.StatusIdle
	f.setSessions("server", idle1, idleLark)
	s.drainRelays("server", []proto.Session{idleLark})
	waitFor(t, "the done notice to go out", func() bool { return f.sentTo("lark1") == 1 })
	if q := s.queuedFor("lark1"); q != 0 {
		t.Fatalf("%d notice(s) still queued", q)
	}
}

// A needs recheck keeps "the node says the card is gone" apart from "the node could not be asked": a node that
// stayed unreachable is an unknown (notice kept, taskWaits true), retried within the hard cap; a machine the hub
// no longer knows is a definite "not waiting"; each distinct machine is asked once.
func TestTaskNeedsRecheckReachability(t *testing.T) {
	ctx := context.Background()
	// Not registered with the service, so no background loop asks box.
	waitingTodo := Todo{ID: "needs", Agents: []TodoAgent{{MachineID: "box", SessionID: "s1"}}}

	t.Run("unreachable is unknown, retried and bounded", func(t *testing.T) {
		s, f := newQuietService(t)
		f.sessionsFail["box"] = 100
		before, start := f.sessionsListCalls("box"), time.Now()
		waits, known := s.taskNeedsRecheck(ctx, waitingTodo)
		calls, elapsed := f.sessionsListCalls("box")-before, time.Since(start)
		if waits || known || calls != agentReachAttempts || elapsed >= agentReachMax {
			t.Fatalf("waits=%v known=%v calls=%d elapsed=%s", waits, known, calls, elapsed)
		}
		if !s.taskWaits(ctx, waitingTodo) {
			t.Fatal("taskWaits read an unreachable node as answered")
		}
	})

	t.Run("the node recovers within the retries", func(t *testing.T) {
		s, f := newQuietService(t)
		f.sessionsFail["box"] = agentReachAttempts - 1 // every try but the last fails
		f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box",
			Status: proto.StatusWorking, State: proto.StateWorking})
		before := f.sessionsListCalls("box")
		if waits, known := s.taskNeedsRecheck(ctx, waitingTodo); waits || !known {
			t.Fatalf("waits=%v known=%v, want false/true (answered)", waits, known)
		}
		if n := f.sessionsListCalls("box") - before; n != agentReachAttempts {
			t.Fatalf("%d list calls, want %d", n, agentReachAttempts)
		}
	})

	t.Run("a machine the hub no longer knows is definite not-waiting", func(t *testing.T) {
		s, f := newQuietService(t)
		f.mu.Lock()
		delete(f.sessions, "box") // the hub answers ErrNoDevice: it no longer knows this machine
		f.mu.Unlock()
		if waits, known := s.taskNeedsRecheck(ctx, waitingTodo); waits || !known || s.taskWaits(ctx, waitingTodo) {
			t.Fatalf("waits=%v known=%v taskWaits=true, want false/true/false", waits, known)
		}
	})

	t.Run("two agents on one machine are one list call", func(t *testing.T) {
		s, f := newQuietService(t)
		f.setSessions("box",
			proto.Session{SessionID: "s1", MachineID: "box"},
			proto.Session{SessionID: "s2", MachineID: "box"})
		todo := Todo{ID: "two-agents", Agents: []TodoAgent{
			{MachineID: "box", SessionID: "s1"}, {MachineID: "box", SessionID: "s2"}}}
		before := f.sessionsListCalls("box")
		agents, reached := s.taskAgentsReached(ctx, todo)
		if !reached || len(agents) != 2 || f.sessionsListCalls("box")-before != 1 {
			t.Fatalf("reached=%v agents=%d calls=%d, want true/2/1", reached, len(agents), f.sessionsListCalls("box")-before)
		}
	})
}

// An agent a reporting conversation opens with agent_start is its task as one from task_plan is (#4101): not
// supervised by the main conversation, so when it is done the reporting conversation gets the notice and the main
// conversation hears nothing of it.
func TestReportingConversationAgentStartIsToldThere(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.judge = `{"verdict":"done","reason":"MR 提好了"}`
	f.known["lark1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	s.markReporter(ctx, "server", "lark1")
	args := json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"提个 MR 修掉它"}`)
	if _, err := s.runTool(ctx, "server", "lark1", "agent_start", args); err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	if !todo.Quiet || todo.Origin != "lark1" || todo.Supervision != nil {
		t.Fatalf("todo = %+v, supervision %+v", todo, todo.Supervision)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "MR 提好了：https://example.com/mr/8", 0)
	sentTo := func(id string) []string {
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
	waitFor(t, "the notice", func() bool { return len(sentTo("lark1")) == 1 })
	if n := sentTo("lark1")[0]; !strings.Contains(n, "做完了（box）") || !strings.Contains(n, "https://example.com/mr/8") {
		t.Fatalf("notice:\n%s", n)
	}
	time.Sleep(4 * shortSuperviseTimings.every)
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told: %q", got)
	}
}

// A reporting conversation's task_review on its own unsupervised task closes it without making a ledger, so the task
// never joins the main conversation's supervision messages; rework and needs_user are refused (#4262).
func TestReportingConversationTaskReviewMakesNoLedger(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lark1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	s.markReporter(ctx, "server", "lark1")
	if _, err := s.runTool(ctx, "server", "lark1", "agent_start", json.RawMessage(`{"machine":"box","cwd":"/code/app","prompt":"提个 MR 修掉它"}`)); err != nil {
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	review := func(verdict string) error {
		args := map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": "重复了"}
		if verdict == "needs_user" { // a complete decision (#3804): what refuses it here is that nothing supervises the task
			args = rolloutDecision()
			args["todo_id"] = todo.ID
		}
		raw, _ := json.Marshal(args)
		_, err := s.runTool(ctx, "server", "lark1", "task_review", raw)
		return err
	}
	for _, v := range []string{"rework", "needs_user"} {
		if err := review(v); err == nil || !strings.Contains(err.Error(), "not supervised") {
			t.Fatalf("%s: %v", v, err)
		}
	}
	if err := review("cancelled"); err != nil {
		t.Fatal(err)
	}
	now := todoNow(s, todo.ID)
	if now.DoneAt == nil || now.DoneBy != db.DoneByCancelled || now.Supervision != nil {
		t.Fatalf("after cancelled: %+v, supervision %+v", now, now.Supervision)
	}
}

// A quiet task's notice for a busy main conversation whose relay queue can't be read is kept and tried again, not
// dropped: it goes out once the queue reads again.
func TestQuietNoticeKeptWhenRelayUnreadable(t *testing.T) {
	st := &memStore{}
	st.failKey(relayKey, errors.New("database hiccup"))
	s, f := newQuietServiceOn(t, st)
	busy := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", busy)
	todo := Todo{ID: "t1", Title: "甲", Origin: "conv1", Quiet: true}
	b := &s.quiet
	b.mu.Lock()
	b.pending = map[string]*quietNotice{todo.ID: {todo: todo, machine: "box", state: "done", text: "修好了"}}
	b.mu.Unlock()

	s.sendQuietNotice(todo.ID)
	// It is tried again while the queue stays unreadable: between tries the notice waits.
	waitFor(t, "the notice kept", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.pending[todo.ID] != nil
	})
	if n := f.notices(); len(n) != 0 {
		t.Fatalf("sent into a busy conversation: %q", n)
	}

	st.failKey(relayKey, nil)
	waitFor(t, "the notice queued once the queue reads", func() bool { return s.queuedFor("conv1") == 1 })
	idle := busy
	idle.Status = proto.StatusIdle
	f.setSessions("server", idle)
	s.drainRelays("server", []proto.Session{idle})
	waitFor(t, "the notice", func() bool { return len(f.notices()) == 1 })
	if n := f.notices()[0]; !strings.Contains(n, "任务「甲」") || !strings.Contains(n, "修好了") {
		t.Fatalf("notice:\n%s", n)
	}
	// Nothing is left to send it again: the notice no longer waits (a retry that fires finds nothing) and the queue
	// is empty.
	b.mu.Lock()
	left := b.pending[todo.ID]
	b.mu.Unlock()
	if left != nil || s.queuedFor("conv1") != 0 {
		t.Fatalf("after it went out: pending %+v, %d queued", left, s.queuedFor("conv1"))
	}
}

// A notice that could not be queued while a newer change of its task waits folds into it: the newer one goes out and
// says the earlier state.
func TestQuietNoticeUnqueuedFoldsIntoANewerOne(t *testing.T) {
	st := &memStore{}
	st.failKey(relayKey, errors.New("database hiccup"))
	s, _ := newQuietServiceOn(t, st)
	todo := Todo{ID: "t1", Title: "甲", Origin: "conv1", Quiet: true}
	newer := &quietNotice{todo: todo, state: "done"}
	b := &s.quiet
	b.mu.Lock()
	b.pending = map[string]*quietNotice{todo.ID: newer}
	b.mu.Unlock()
	lost := &quietNotice{todo: todo, state: "needs", ask: "用哪个分支？"}
	if s.queueNotice(todo.ID, lost, "conv1", relayMsg{From: todo.ID, Tool: noticeTool}) {
		t.Fatal("queued with the relay queue unreadable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n := b.pending[todo.ID]; n != newer || !slices.Equal(n.earlier, []string{noticeStates["needs"]}) {
		t.Fatalf("pending = %+v; want the newer one, saying it waited", n)
	}
}
