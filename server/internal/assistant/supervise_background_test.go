package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// newBackgroundService is newSupervisedService with the background supervisor on, as it is by default: the main
// conversation conv1 handed out one task (agent s1 on box). The conversations the fake starts get ids from s101 on, so
// the background supervisor's is not the agent's.
func newBackgroundService(t *testing.T) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f, todo := newSupervisedService(t)
	s.noBackground = false
	f.mu.Lock()
	f.made = 100
	f.mu.Unlock()
	return s, f, todo
}

// backgroundID waits for the background supervisor to be made and gives its id.
func backgroundID(t *testing.T, s *Service) string {
	t.Helper()
	var id string
	waitFor(t, "the background supervisor", func() bool {
		id = s.settings(context.Background()).BackgroundSupervisor
		return id != ""
	})
	return id
}

// listBackground has the assistant's machine list the background supervisor, idle after its first turn, and delivers
// what was queued behind that turn (it waits for the supervision message to be queued there first).
func listBackground(t *testing.T, s *Service, f *fakeHub, id string) {
	t.Helper()
	waitFor(t, "the message queued for the background supervisor", func() bool { return s.queuedFor(id) > 0 })
	list := []proto.Session{{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		{SessionID: id, MachineID: "server", Assistant: true, Status: proto.StatusIdle}}
	f.setSessions("server", list...)
	s.drainRelays("server", list)
}

// conversationStarts are the assistant.send calls that started a conversation (no session id).
func conversationStarts(f *fakeHub) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, a := range f.sends {
		if id, _ := a["session_id"].(string); id == "" {
			out = append(out, a)
		}
	}
	return out
}

// By default a task the main conversation hands out is followed up by the background supervisor: it is made on first
// use, gets the supervision message in the leader's variant, and reviews the task. The task's ledger and origin stay
// the main conversation's, which gets no message and no turn for the acceptance, only the entry in its feed.
func TestBackgroundSupervisorFollowsUpTheMainConversationsTasks(t *testing.T) {
	s, f, todo := newBackgroundService(t)
	ctx := context.Background()
	if got := s.settings(ctx).BackgroundSupervisor; got != "" {
		t.Fatalf("the background supervisor %q was made before anything was for it", got)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	bg := backgroundID(t, s)
	cfg := s.settings(ctx)
	if bg == "conv1" || cfg.BackgroundSupervisorMachineID != "server" || cfg.BackgroundSupervisorAt == nil {
		t.Fatalf("the background supervisor: %+v", cfg)
	}
	starts := conversationStarts(f)
	if len(starts) != 1 {
		t.Fatalf("%d conversations were started", len(starts))
	}
	if text, _ := starts[0]["text"].(string); !strings.Contains(text, "这是后台督办对话") || starts[0]["launch"] != "headless" {
		t.Fatalf("the background supervisor's start: %+v", starts[0])
	}
	if slices.Contains(cfg.Leaders, bg) || s.isLeader(ctx, bg) || s.supervises(ctx, bg) {
		t.Fatal("the background supervisor is a leader")
	}
	if !slices.Contains(keptSessions(cfg, nil, nil), SessionRef{MachineID: "server", SessionID: bg}) {
		t.Fatal("the background supervisor is not kept listed")
	}
	if !s.supervising(bg) {
		t.Fatal("the background supervisor's first turn is not held from the pushes")
	}

	// Its machine lists it once its first turn ended: the message queued behind that turn goes in.
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })
	n := f.noticesTo(bg)[0]
	for _, want := range []string{"📋 督办：你派出去的任务有 1 个要你处理", "这是 Persona 的督办消息", "你是后台督办对话", "hub 判断做完了，等你验收", "pull/7"} {
		if !strings.Contains(n, want) {
			t.Errorf("the background supervisor's message lacks %q:\n%s", want, n)
		}
	}
	if strings.Contains(n, "你是这些任务的 leader 对话") {
		t.Errorf("the background supervisor is told the tasks are its own:\n%s", n)
	}
	if got := f.noticesTo("conv1"); len(got) != 0 {
		t.Fatalf("the main conversation was told too: %q", got)
	}
	if !strings.Contains(s.Context(ctx, ContextParams{SessionID: bg}).Text, "这是后台督办对话") {
		t.Fatal("the background supervisor's context does not say what it is")
	}

	// Nothing is written on the task for it.
	if cur := todoNow(s, todo.ID); cur.Origin != "conv1" || cur.Supervision.Supervisor != "conv1" {
		t.Fatalf("the task moved: origin %q, supervisor %q", cur.Origin, cur.Supervision.Supervisor)
	}

	review := func(self, verdict, note string) (string, error) {
		return runJSON(t, s, self, "task_review", map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": note})
	}
	if _, err := review("conv2", "rework", "x"); err == nil {
		t.Fatal("another side conversation reviewed the task")
	}
	if res, err := review(bg, "rework", "补测试"); err != nil || !strings.Contains(res, "Rework round 1") {
		t.Fatalf("the background supervisor's rework: %s %v", res, err)
	}
	if sent := sentToAgent(f); len(sent) != 1 || !strings.Contains(sent[0], "补测试") {
		t.Fatalf("sent to the agent: %q", sent)
	}
	// The main conversation may still review it, as it may a leader's task.
	if res, err := review("conv1", "rework", "再补深色截图"); err != nil || !strings.Contains(res, "Rework round 2") {
		t.Fatalf("the main conversation's rework: %s %v", res, err)
	}
	res, err := review(bg, "accepted", "已合并，测试补了")
	if err != nil || !strings.Contains(res, "accepted and closed") {
		t.Fatalf("the background supervisor's accept: %s %v", res, err)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.Supervision.State != reviewAccepted || done.Supervision.Supervisor != "conv1" || done.Origin != "conv1" {
		t.Fatalf("accepted: %+v %+v", done, done.Supervision)
	}
	// The main conversation's feed has the review, and its next snapshot the outcome; no message went into it.
	items, _, err := s.feed(ctx, "conv1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// The entry says the background supervisor wrote it; the main conversation's own review says nothing.
	var accepted bool
	by := map[string]string{} // each review's author, by its note
	for _, it := range items {
		var body reviewFeedBody
		if it.Kind != feedReview || it.Ref != todo.ID || json.Unmarshal(it.Body, &body) != nil {
			continue
		}
		by[body.Note] = body.By
		accepted = accepted || body.Verdict == reviewAccepted
	}
	if !accepted {
		t.Fatalf("the main conversation's feed has no accepted review: %+v", items)
	}
	if by["补测试"] != reviewByBackground || by["已合并，测试补了"] != reviewByBackground || by["再补深色截图"] != "" {
		t.Fatalf("who wrote each review, background supervisor's twice and the main conversation's once: %+v", by)
	}
	if tasks := s.snapshotTasks(ctx, ""); len(tasks) != 1 || tasks[0].ID != todo.ID || !strings.Contains(tasks[0].Health, "已验收") {
		t.Fatalf("the snapshot's tasks: %+v", tasks)
	}
	settleService(t, s)
	if got := f.noticesTo("conv1"); len(got) != 0 {
		t.Fatalf("the acceptance took a turn of the main conversation: %q", got)
	}
}

// The background supervisor's needs_user is told to the main conversation as a leader's is (the escalated entry), with
// the decision card as ever; and it stays out of the leaders, the directions and the archive.
func TestBackgroundSupervisorEscalatesAndStaysBuiltIn(t *testing.T) {
	s, f, todo := newBackgroundService(t)
	ctx := context.Background()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	bg := backgroundID(t, s)
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })

	res, err := runJSON(t, s, bg, "task_review", map[string]any{"todo_id": todo.ID, "verdict": "needs_user",
		"reason": "要不要顺手改 iPhone 横屏", "recommend": "先只修 iPad", "why_owner": "priority", "cost_of_waiting": "iPad 的修复晚一天发",
		"options": []map[string]string{{"label": "先只修 iPad", "leads_to": "今天就能发"}, {"label": "一起改 iPhone", "leads_to": "多花一天"}}})
	if err != nil || !strings.Contains(res, "waits for the user") {
		t.Fatalf("the background supervisor's needs_user: %s %v", res, err)
	}
	waitFor(t, "the main conversation's message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	n := f.noticesTo("conv1")[0]
	for _, want := range []string{"跟进它的后台督办对话判断要用户决定", "要不要顺手改 iPhone 横屏", "后台督办对话推荐：先只修 iPad", "后台督办对话或 leader 升级上来的"} {
		if !strings.Contains(n, want) {
			t.Errorf("the main conversation's message lacks %q:\n%s", want, n)
		}
	}
	for _, not := range []string{"leader 推荐", "那个 leader"} { // no leader is on this task
		if strings.Contains(n, not) {
			t.Errorf("the main conversation's message names a leader (%q):\n%s", not, n)
		}
	}
	if strings.Contains(n, "你是后台督办对话") {
		t.Fatalf("the main conversation is told it is the background supervisor:\n%s", n)
	}

	if _, err := s.setArchived(ctx, "server", bg, true, titleByUser); !errors.Is(err, errArchiveBackground) {
		t.Fatalf("archiving the background supervisor: %v", err)
	}
	raw, _ := json.Marshal([]string{bg})
	if _, err := s.setLeaders(ctx, raw); err == nil || !strings.Contains(err.Error(), "background supervisor") {
		t.Fatalf("the background supervisor as a leader: %v", err)
	}
	if _, err := s.makeDirection(ctx, bg, "", "", ""); err == nil || !strings.Contains(err.Error(), "background supervisor") {
		t.Fatalf("the background supervisor as a direction: %v", err)
	}
}

// The main conversation is told once that its tasks are followed up in the background, and only once there is a
// background supervisor.
func TestBackgroundSupervisorIsAnnouncedOnce(t *testing.T) {
	s, f, _ := newBackgroundService(t)
	ctx := context.Background()
	const said = "由后台督办对话"
	if strings.Contains(s.Context(ctx, ContextParams{SessionID: "conv1"}).Text, said) {
		t.Fatal("announced before there is a background supervisor")
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	bg := backgroundID(t, s)
	if strings.Contains(s.Context(ctx, ContextParams{SessionID: bg}).Text, said) {
		t.Fatal("announced to the background supervisor itself")
	}
	first := s.Context(ctx, ContextParams{SessionID: "conv1"})
	if !strings.Contains(first.Text, said) || !strings.Contains(first.Text, bg) || !strings.Contains(first.Text, "assistant.main_supervises_own_tasks") {
		t.Fatalf("the main conversation is not told:\n%s", first.Text)
	}
	// Asking for the context tells nobody: the message may fail to go out, or go without it. It is said again until
	// the node confirms a message that carried it.
	again := s.Context(ctx, ContextParams{SessionID: "conv1"})
	if !strings.Contains(again.Text, said) || s.settings(ctx).BackgroundSupervisorAnnounced {
		t.Fatal("counted as said before any message carrying it was delivered")
	}
	receipt := assistantwire.ViewErrorReceipt{ItemID: backgroundAnnounceReceipt, Digest: bg}
	if !slices.Contains(again.Receipts, receipt) {
		t.Fatalf("no receipt for the node to confirm: %+v", again.Receipts)
	}
	// Not by another conversation's message, nor for a background supervisor that is not the one there is.
	s.Context(ctx, ContextParams{SessionID: bg, Ack: []assistantwire.ViewErrorReceipt{receipt}})
	s.Context(ctx, ContextParams{SessionID: "conv1", Ack: []assistantwire.ViewErrorReceipt{{ItemID: backgroundAnnounceReceipt, Digest: "another"}}})
	if s.settings(ctx).BackgroundSupervisorAnnounced {
		t.Fatal("confirmed by a receipt that is not the main conversation's own")
	}
	s.Context(ctx, ContextParams{SessionID: "conv1", Ack: again.Receipts})
	if !s.settings(ctx).BackgroundSupervisorAnnounced {
		t.Fatal("not said after the node confirmed the message")
	}
	if last := s.Context(ctx, ContextParams{SessionID: "conv1"}); strings.Contains(last.Text, said) || len(last.Receipts) != 0 {
		t.Fatal("announced again after it was delivered")
	}
}

// With the setting on (the main conversation supervises its own tasks) nothing changes: no background supervisor is
// made and the main conversation gets the message. Switched on later, the signals raised from then on go to the main
// conversation at once, and the task keeps its ledger.
func TestMainSupervisesOwnTasksSetting(t *testing.T) {
	s, f, todo := newBackgroundService(t)
	ctx := context.Background()
	if v := s.view(ctx, false); v.MainSupervisesOwnTasks {
		t.Fatal("the default is that the main conversation supervises its own tasks")
	}
	on := true
	w, v := permissionSettingsRequest(t, s, "PUT", "/api/v1/assistant", "", "full", `{"main_supervises_own_tasks":true,"background_supervisor":"conv2"}`)
	if w.Code != 200 || !v.MainSupervisesOwnTasks || v.BackgroundSupervisor != "" {
		t.Fatalf("PUT /api/v1/assistant: %d %s", w.Code, w.Body.String())
	}
	if !s.mainSupervisesOwnTasks(ctx) {
		t.Fatal("the setting was not saved")
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	waitFor(t, "the main conversation's message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	if n := f.noticesTo("conv1")[0]; strings.Contains(n, "后台督办") || !strings.Contains(n, "hub 判断做完了，等你验收") {
		t.Fatalf("the main conversation's message:\n%s", n)
	}
	if got := s.settings(ctx).BackgroundSupervisor; got != "" || len(conversationStarts(f)) != 0 {
		t.Fatalf("a background supervisor %q was made with the setting on", got)
	}

	// Off again: the next signal makes the background supervisor; on again: the one after goes to the main conversation.
	off := false
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &off })
	if _, err := runJSON(t, s, "conv1", "task_review", map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "补测试"}); err != nil {
		t.Fatal(err)
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "补了测试", 0)
	bg := backgroundID(t, s)
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &on })
	if got := s.digestTo(s.settings(ctx), superviseSignal{Todo: todo.ID, Kind: sigStalled}); got != "conv1" {
		t.Fatalf("with the setting on a signal goes to %q", got)
	}
	if cur := todoNow(s, todo.ID); cur.DoneAt != nil || cur.Supervision.Supervisor != "conv1" {
		t.Fatalf("the task changed with the setting: %+v %+v", cur, cur.Supervision)
	}
	// What the background supervisor was sent before the switch it may still finish.
	if _, err := runJSON(t, s, bg, "task_review", map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "好了"}); err != nil {
		t.Fatalf("the background supervisor finishing what it was sent: %v", err)
	}
}

// A background supervisor that cannot start drops nothing: its tasks' signals go to the main conversation, and the
// start is not tried again at once.
func TestBackgroundSupervisorThatCannotStartFallsBackToMain(t *testing.T) {
	s, f, _ := newBackgroundService(t)
	ctx := context.Background()
	f.mu.Lock()
	f.onAssistantSend = func(f *fakeHub, a map[string]any) {
		f.failAssistant = nil
		if id, _ := a["session_id"].(string); id == "" {
			f.failAssistant = errors.New("claude is not signed in")
		}
	}
	f.mu.Unlock()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	waitFor(t, "the main conversation's message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	if n := f.noticesTo("conv1")[0]; !strings.Contains(n, "hub 判断做完了，等你验收") || strings.Contains(n, "你是后台督办对话") {
		t.Fatalf("the main conversation's message:\n%s", n)
	}
	if got := s.settings(ctx).BackgroundSupervisor; got != "" {
		t.Fatalf("a background supervisor %q is kept though it did not start", got)
	}
	if id, _ := s.ensureBackground(ctx); id != "" {
		t.Fatalf("the start was tried again at once: %q", id)
	}
}

// A background supervisor its machine no longer lists is replaced, and what was still queued for it is told again to
// the new one instead of waiting for ever.
func TestBackgroundSupervisorGoneIsReplaced(t *testing.T) {
	s, f, todo := newBackgroundService(t)
	ctx := context.Background()
	old := time.Now().Add(-time.Hour).UTC()
	setSettings(t, s, func(v *Settings) {
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID, v.BackgroundSupervisorAt = "gone1", "server", &old
	})
	g := superviseSignal{Todo: todo.ID, Kind: sigStalled, Key: "k1", Minutes: 50, At: time.Now()}
	b := &s.supervise
	b.mu.Lock()
	b.init()
	b.setQueuedLocked("gone1", []superviseSignal{g})
	b.sent[g.id()] = g.Key
	b.mu.Unlock()
	if s.queueRelay("gone1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "📋 督办", At: time.Now().UTC()}) == 0 {
		t.Fatal("could not queue for the old background supervisor")
	}

	s.backgroundStranded(ctx)
	bg := ""
	waitFor(t, "a new background supervisor", func() bool {
		bg = s.settings(ctx).BackgroundSupervisor
		return bg != "" && bg != "gone1"
	})
	if s.queuedFor("gone1") != 0 {
		t.Fatal("the old background supervisor's queue was kept")
	}
	listBackground(t, s, f, bg)
	waitFor(t, "the signal told again", func() bool {
		for _, n := range f.noticesTo(bg) {
			if strings.Contains(n, "50 分钟没有进展") {
				return true
			}
		}
		return false
	})
	if got := f.noticesTo("gone1"); len(got) != 0 {
		t.Fatalf("something was sent to the conversation that is gone: %q", got)
	}
}

// digestTo: a task's own signals go to the background supervisor where they went to the main conversation; what the
// main conversation always handled stays with it, and a leader's tasks stay the leader's.
func TestDigestToWithBackgroundSupervisor(t *testing.T) {
	s, f, todo := newBackgroundService(t)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	setSettings(t, s, func(v *Settings) {
		v.Leaders = []string{"lead1"}
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg1", "server"
	})
	led, err := s.createTodo(ctx, "主管的任务", "", "lead1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, led.ID, "lead1", []string{"做完"}, 30)
	// A side conversation supervises what it hands out itself (#5002): the background supervisor takes the tasks whose
	// ledger names the main conversation, not these.
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "side1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "bg1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	sided, err := s.createTodo(ctx, "别的对话的任务", "", "side1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, sided.ID, "side1", []string{"做完"}, 30)
	waits, err := s.createTodo(ctx, "别的对话等用户的任务", "", "side1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, waits.ID, "side1", []string{"做完"}, 30)
	if _, _, err := s.updateSupervision(ctx, waits.ID, func(v *Supervision) bool { v.State = reviewNeedsUser; return true }); err != nil {
		t.Fatal(err)
	}
	for conv, want := range map[string]string{"side1": "side1", "bg1": "conv1", "gone": "conv1"} {
		// What the background supervisor hands out keeps the main conversation on its ledger, as every task it follows up.
		if got, ok := s.defaultPlanSupervisor(ctx, conv, ""); !ok || got != want {
			t.Errorf("a task %s plans is supervised by %q %v, want %q", conv, got, ok, want)
		}
	}
	cfg := s.settings(ctx)
	for _, c := range []struct {
		name string
		g    superviseSignal
		want string
	}{
		{"a review", superviseSignal{Todo: todo.ID, Kind: sigReview}, "bg1"},
		{"a report", superviseSignal{Todo: todo.ID, Kind: sigReport}, "bg1"},
		{"a stall", superviseSignal{Todo: todo.ID, Kind: sigStalled}, "bg1"},
		{"a stop short", superviseSignal{Todo: todo.ID, Kind: sigIdle}, "bg1"},
		{"a stop nobody judged", superviseSignal{Todo: todo.ID, Kind: sigUnjudged}, "bg1"},
		{"a waiting agent", superviseSignal{Todo: todo.ID, Kind: sigNeedsYou}, "bg1"},
		{"an interrupted turn", superviseSignal{Todo: todo.ID, Kind: sigInterrupted}, "bg1"},
		{"a task nobody works on", superviseSignal{Todo: todo.ID, Kind: sigOrphaned}, "bg1"},
		{"a task past its clock", superviseSignal{Todo: todo.ID, Kind: sigOverdue}, "bg1"},
		{"a parking that is due", superviseSignal{Todo: todo.ID, Kind: sigParkedDue}, "bg1"},
		{"an inbox message", superviseSignal{Todo: todo.ID, Kind: sigInbox}, "bg1"},
		{"a decision to recheck", superviseSignal{Todo: todo.ID, Kind: sigRecheck}, "bg1"},
		{"a decision nobody can answer", superviseSignal{Todo: todo.ID, Kind: sigUndecidable}, "bg1"},
		{"a decision the owner cannot answer", superviseSignal{Todo: todo.ID, Kind: sigOwnerCannot}, "bg1"},
		{"a failed start", superviseSignal{Todo: todo.ID, Kind: sigFailed}, "conv1"},
		{"a retried start", superviseSignal{Todo: todo.ID, Kind: sigStarted}, "conv1"},
		{"an escalation", superviseSignal{Todo: todo.ID, Kind: sigEscalated}, "conv1"},
		{"a leader stuck", superviseSignal{Todo: led.ID, Kind: sigLeaderStuck}, "conv1"},
		{"a leader's review", superviseSignal{Todo: led.ID, Kind: sigReview}, "lead1"},
		{"a side conversation's review", superviseSignal{Todo: sided.ID, Kind: sigReview}, "side1"},
		{"a side conversation's task waiting for the user", superviseSignal{Todo: waits.ID, Kind: sigIdle}, "conv1"},
		{"a card's rung", superviseSignal{Todo: led.ID, Kind: sigAsk, To: "lead1"}, "lead1"},
		{"a card raised to the main conversation", superviseSignal{Todo: led.ID, Kind: sigAsk, To: "conv1"}, "conv1"},
		{"a task leader's decision for the main conversation", superviseSignal{Todo: todo.ID, Kind: sigDecide, To: "conv1"}, "bg1"},
		{"a task leader's decision for a leader", superviseSignal{Todo: led.ID, Kind: sigDecide, To: "lead1"}, "lead1"},
		{"a task that is gone", superviseSignal{Todo: "none", Kind: sigReview}, "conv1"},
	} {
		if got := s.digestTo(cfg, c.g); got != c.want {
			t.Errorf("%s goes to %q, want %q", c.name, got, c.want)
		}
	}
	// Off, or on another machine than the assistant's: the main conversation, also for what named the background supervisor.
	on := true
	off := cfg
	off.MainSupervisesOwnTasks = &on
	moved := cfg
	moved.BackgroundSupervisorMachineID = "laptop"
	for name, c := range map[string]Settings{"off": off, "moved": moved} {
		if got := s.digestTo(c, superviseSignal{Todo: todo.ID, Kind: sigReview}); got != "conv1" {
			t.Errorf("%s: a review goes to %q", name, got)
		}
		if got := s.digestTo(c, superviseSignal{Todo: todo.ID, Kind: sigAsk, To: "bg1"}); got != "conv1" {
			t.Errorf("%s: a card held by the background supervisor goes to %q", name, got)
		}
	}
}

// Cards go to their supervisor first only with leader conversations, as before. Then a card of a task no leader
// supervises goes to the background supervisor and from there to the user, not through the main conversation; the
// background supervisor escalates it as a leader does. With the setting off the main conversation is its rung again.
func TestAsksOfUnledTasksGoToTheBackgroundSupervisor(t *testing.T) {
	e := newRouteEnv(t, nil, true)
	ctx := context.Background()
	e.s.noBackground = false
	e.f.known["bg1"] = true
	convs := []proto.Session{{SessionID: "conv1", MachineID: "server", Cwd: assistantHome, Assistant: true, Status: proto.StatusIdle},
		{SessionID: "lead1", MachineID: "server", Cwd: assistantHome, Assistant: true, Status: proto.StatusIdle},
		{SessionID: "bg1", MachineID: "server", Cwd: assistantHome, Assistant: true, Status: proto.StatusIdle}}
	e.sessions("server", convs...)
	e.f.setSessions("server", convs...)
	setSettings(t, e.s, func(v *Settings) { v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg1", "server" })
	// The task is the main conversation's, not the leader's.
	if _, _, err := e.s.updateSupervision(ctx, e.todo.ID, func(v *Supervision) bool { v.Supervisor = "conv1"; return true }); err != nil {
		t.Fatal(err)
	}
	if ladder, _, _, ok := e.s.askLadder(ctx, "mac", "worker-1"); !ok || !slices.Equal(ladder, []string{"bg1"}) {
		t.Fatalf("ladder = %v %v", ladder, ok)
	}
	if _, _, _, ok := e.s.askLadder(ctx, "server", "bg1"); ok {
		t.Fatal("the background supervisor's own cards are routed")
	}

	e.raise(askPermission("ix1", "worker-1"))
	n := e.noticeWith("bg1", "interaction_id=ix1")
	for _, want := range []string{"你是后台督办对话", "先交给你处理，用户还没收到", "主对话也会知道", "自动上交给用户"} {
		if !strings.Contains(n, want) {
			t.Errorf("the background supervisor's message lacks %q:\n%s", want, n)
		}
	}
	e.settle()
	if got := e.pushesOf("ix1"); got != 0 {
		t.Fatalf("the user was pushed %d times", got)
	}
	if _, ok := e.s.routedTo("ix1", "bg1", false); !ok {
		t.Fatal("the card is not with the background supervisor")
	}
	if _, ok := e.s.routedTo("ix1", "conv1", true); !ok {
		t.Fatal("the main conversation may not answer a card the background supervisor holds")
	}
	if _, ok := e.s.routedTo("ix1", "lead1", false); ok {
		t.Fatal("a leader may answer a card of a task that is not its own")
	}
	res, err := runJSON(t, e.s, "bg1", "ask_escalate", map[string]any{"todo_id": e.todo.ID, "reason": "要推到远端分支，需要你同意", "recommend": "允许"})
	if err != nil || !strings.Contains(res, "1 card(s)") {
		t.Fatalf("escalate: %s %v", res, err)
	}
	waitFor(t, "the push", func() bool { return e.pushesOf("ix1") == 1 })
	m := e.noticeWith("conv1", "跟进它的后台督办对话判断要用户决定")
	if !strings.Contains(m, "它的 Agent 在等的卡片已经发给用户") || !strings.Contains(m, "interaction_id=ix1") {
		t.Fatalf("the main conversation's message:\n%s", m)
	}

	// A card nobody handles goes from the background supervisor to the user, not through the main conversation.
	e.raise(askPermission("ix2", "worker-2"))
	e.noticeWith("bg1", "interaction_id=ix2")
	e.s.askTick(time.Now().Add(2 * askHoldFor))
	waitFor(t, "the push", func() bool { return e.pushesOf("ix2") == 1 })
	e.settle()
	for _, got := range e.f.noticesTo("conv1") {
		if strings.Contains(got, "interaction_id=ix2") {
			t.Fatalf("the card went through the main conversation:\n%s", got)
		}
	}

	on := true
	setSettings(t, e.s, func(v *Settings) { v.MainSupervisesOwnTasks = &on })
	if ladder, _, _, ok := e.s.askLadder(ctx, "mac", "worker-1"); !ok || !slices.Equal(ladder, []string{"conv1"}) {
		t.Fatalf("with the setting off the ladder = %v %v", ladder, ok)
	}
}

// Without leader conversations a card is the user's as it comes, background supervisor or not.
func TestBackgroundSupervisorAloneDoesNotRouteAsks(t *testing.T) {
	e := newRouteEnv(t, nil, false)
	e.s.noBackground = false
	setSettings(t, e.s, func(v *Settings) { v.BackgroundSupervisor, v.BackgroundSupervisorMachineID = "bg1", "server" })
	if _, _, _, ok := e.s.askLadder(context.Background(), "mac", "worker-1"); ok {
		t.Fatal("a card is routed with no leader conversation")
	}
	e.raise(askPermission("ix1", "worker-1"))
	waitFor(t, "the push", func() bool { return e.pushesOf("ix1") == 1 })
	if e.lastShown("ix1").Delegating {
		t.Fatal("the card is shown as delegating")
	}
}

// The background supervisor's model purpose follows the main chat's until it is given its own.
func TestBackgroundSupervisorModelPurpose(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	p, err := s.modelPurposeDef(ctx, purposeSupervise)
	if err != nil {
		t.Fatal(err)
	}
	if p.Parent != "assistant" || p.CategoryID != "assistant" || p.launch() != "headless" || modelRecordID(p) != purposeSupervise {
		t.Fatalf("purpose = %+v, record %q", p, modelRecordID(p))
	}
	main, err := s.resolveModel(ctx, "assistant")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.resolveModel(ctx, purposeSupervise); err != nil || got != main {
		t.Fatalf("with nothing saved it runs %+v, the main chat %+v (%v)", got, main, err)
	}
	if got := purposeOf(t, s, purposeSupervise); len(got.Fields) == 0 || got.Effective != purposeOf(t, s, "assistant").Effective {
		t.Fatalf("the purpose as the apps read it: %+v", got)
	}
	if r := modelRequest(s, "PATCH", purposeSupervise, `{"revision":0,"model":"small-model"}`); r.Code != 200 {
		t.Fatalf("giving it its own model: %d %s", r.Code, r.Body.String())
	}
	if got, err := s.resolveModel(ctx, purposeSupervise); err != nil || got.Model != "small-model" || got.Provider != main.Provider {
		t.Fatalf("its own model: %+v %v", got, err)
	}
	if again, _ := s.resolveModel(ctx, "assistant"); again != main {
		t.Fatalf("the main chat's model moved with it: %+v", again)
	}
	if slices.Contains(modelConfigIDs, purposeSupervise) {
		t.Fatal("the apps' catalog lists a purpose they do not know yet")
	}
}

// The prompts the background supervisor adds: its start, its context, its supervision message, the main
// conversation's one-time line and the CLAUDE.md that no longer says the main conversation reviews everything.
func TestGoldenBackgroundSupervisor(t *testing.T) {
	golden(t, "supervisor_start", prompts.RenderSupervisorStart(prompts.SupervisorStart{Owner: "Example user"}))
	golden(t, "context.background", prompts.RenderAssistantContext(prompts.AssistantContext{Background: true, Owner: "Example user", Snapshot: "（快照）"}))
	golden(t, "context.main_background_started", prompts.RenderAssistantContext(prompts.AssistantContext{Main: true, Owner: "Example user",
		BackgroundStarted: "a1b2c3d4-0000-4000-8000-000000000009", Snapshot: "（快照）"}))
	golden(t, "supervise.background", prompts.RenderSupervise(prompts.Supervise{Leader: true, Background: true, Asked: true, AskMinutes: 5,
		Entries: sortEntries([]prompts.SuperviseEntry{
			{ID: "t1", Title: "修登录崩溃", Kind: sigReview, Machine: "Mac", Session: "a1b2c3d4", DoneWhen: []string{"PR 合并到 main"}, Last: "做完了，PR 已合并"},
			{ID: "t2", Title: "补测试", Kind: sigAsk, Asks: []prompts.SuperviseAsk{{ID: "ix1", Kind: proto.IxQuestion, Machine: "Mac", Session: "b2c3d4e5", Text: "用哪个分支？"}}},
		})}))
	golden(t, "supervise.background_escalated", prompts.RenderSupervise(prompts.Supervise{Escalated: true, BackgroundEscalated: true, Entries: []prompts.SuperviseEntry{
		{ID: "t1", Title: "修登录崩溃", Kind: sigEscalated, Background: true, Detail: "要不要顺手改 iPhone 横屏", Recommend: "先只修 iPad"}}}))

	bare := claudeMDHub(Device{ID: "server", Name: "服务器", Kind: "local"})
	s := newTestService(t, bare)
	s.kb.dir = "<hub>/data"
	off := s.claudeMD(context.Background(), "server", false)
	s.noBackground = false
	on := s.claudeMD(context.Background(), "server", false)
	golden(t, "assistant_claude_md.background", on)
	if !strings.Contains(off, "主对话派出去的任务由你督办") || strings.Contains(off, "后台督办") {
		t.Fatal("with the background supervisor off the CLAUDE.md changed")
	}
	if strings.Contains(on, "主对话派出去的任务由你督办") || !strings.Contains(on, "主对话派出去的任务由后台督办对话跟进") {
		t.Fatal("with the background supervisor on the CLAUDE.md still says the main conversation reviews its tasks")
	}
	yes := true
	setSettings(t, s, func(v *Settings) { v.MainSupervisesOwnTasks = &yes })
	if got := s.claudeMD(context.Background(), "server", false); got != off {
		t.Fatal("with the setting on the CLAUDE.md is not what it was before the background supervisor")
	}
}

// A manager that just started answers from an empty session list until the assistant's machine has connected and
// listed: that is no word on the background supervisor. It is not replaced, and what is for it queues until the
// machine lists it; the main conversation gets nothing.
func TestBackgroundSupervisorIsNotGoneBeforeItsMachineListed(t *testing.T) {
	s, f, _ := newBackgroundService(t)
	ctx := context.Background()
	old := time.Now().Add(-time.Hour).UTC()
	setSettings(t, s, func(v *Settings) {
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID, v.BackgroundSupervisorAt = "bg1", "server", &old
	})
	f.setSessions("server") // nothing listed yet
	if !s.backgroundThere(ctx, s.settings(ctx), "bg1") {
		t.Fatal("gone by a list its machine has not sent yet")
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	waitFor(t, "the message queued for the background supervisor", func() bool { return s.queuedFor("bg1") > 0 })
	s.backgroundStranded(ctx)
	if got := s.settings(ctx).BackgroundSupervisor; got != "bg1" {
		t.Fatalf("the background supervisor was replaced by %q", got)
	}
	if n := len(conversationStarts(f)); n != 0 {
		t.Fatalf("%d conversation(s) started", n)
	}
	if got := f.noticesTo("conv1"); len(got) != 0 || s.queuedFor("conv1") != 0 {
		t.Fatalf("the main conversation got its message: %q", got)
	}
	listBackground(t, s, f, "bg1")
	waitFor(t, "the message delivered once it is listed", func() bool { return len(f.noticesTo("bg1")) == 1 })

	// Listed without it by a machine that lists the main conversation: now it is gone.
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	if s.backgroundThere(ctx, s.settings(ctx), "bg1") {
		t.Fatal("still there though its machine lists without it")
	}
	// A machine that is not connected says nothing either.
	f.mu.Lock()
	f.devices = []Device{{ID: "server", Name: "server"}, {ID: "box", Name: "box", Online: true, Reachable: true}}
	f.mu.Unlock()
	if !s.backgroundThere(ctx, s.settings(ctx), "bg1") {
		t.Fatal("gone by the list of a machine that is not connected")
	}
}

// A supervision message queued for a background supervisor before the manager restarted has no signals in memory to
// tell again, and its deduplication entries would keep them from going out: when that background supervisor is gone,
// the message itself moves to the main conversation instead of being dropped.
func TestBackgroundSupervisorGoneAfterRestartKeepsItsQueuedMessage(t *testing.T) {
	s, f, _ := newBackgroundService(t)
	ctx := context.Background()
	old := time.Now().Add(-time.Hour).UTC()
	setSettings(t, s, func(v *Settings) {
		v.BackgroundSupervisor, v.BackgroundSupervisorMachineID, v.BackgroundSupervisorAt = "gone1", "server", &old
	})
	if s.queueRelay("gone1", relayMsg{From: superviseTool, Tool: superviseTool, Text: "📋 督办：重启前排队的", At: time.Now().UTC()}) == 0 {
		t.Fatal("could not queue for the old background supervisor")
	}
	s.backgroundStranded(ctx)
	waitFor(t, "the message in the main conversation", func() bool {
		return slices.ContainsFunc(f.noticesTo("conv1"), func(n string) bool { return strings.Contains(n, "重启前排队的") })
	})
	if s.queuedFor("gone1") != 0 {
		t.Fatal("the old background supervisor's queue was kept")
	}
	if got := f.noticesTo("gone1"); len(got) != 0 {
		t.Fatalf("something was sent to the conversation that is gone: %q", got)
	}
}

// The owner comes first in the background supervisor's own queue as in any conversation's (#4925, relay_presence.go),
// and they are present there only when they write there: talking in the main conversation holds nothing that is for
// the background supervisor, and writing to the background supervisor holds its next supervision message until they
// are quiet.
func TestOwnerPresenceAndTheBackgroundSupervisorsQueue(t *testing.T) {
	presenceTimes(t, 400*time.Millisecond, time.Hour)
	s, f, _ := newBackgroundService(t)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"}) // talking in the main conversation
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	bg := backgroundID(t, s)
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })
	if _, present := s.ownersPresent(time.Now())[bg]; present {
		t.Fatal("the owner counts as present in the background supervisor without having written there")
	}

	// The owner writes to the background supervisor: what the system has for it waits, by rank, until they are quiet.
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: bg})
	other := relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "hello", At: time.Now().UTC()}
	urgent := relayMsg{From: superviseTool, FromTitle: "督办", Tool: superviseTool, Text: "Synthetic supervision.", Urgent: true, At: time.Now().UTC()}
	s.relayOrQueue(ctx, bg, other)
	s.relayOrQueue(ctx, bg, urgent)
	if n := s.queuedFor(bg); n != 2 || f.sentTo(bg) != 1 {
		t.Fatalf("while the owner writes there: %d queued, %d sent", n, f.sentTo(bg))
	}
	// Quiet again: one goes in per turn, and the one that waits on a decision goes before the one that came first.
	waitFor(t, "the first held message once the owner is quiet", func() bool { return f.sentTo(bg) == 2 })
	b := &s.relays
	b.mu.Lock()
	left := slices.Clone(b.st.Queue[bg])
	b.mu.Unlock()
	if len(left) != 1 || left[0].Tool != "conversation_send" {
		t.Fatalf("the message that waits on a decision did not go first; still queued: %+v", left)
	}
}

// The background supervisor's rounds are supervision rounds in the availability metrics (#4924): the one its start
// begins, whether the round is seen starting before or after the start answered, and each a supervision message starts.
func TestBackgroundSupervisorsRoundsAreSupervisionRounds(t *testing.T) {
	s, f, _ := newBackgroundService(t)
	ctx := context.Background()
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	bg := backgroundID(t, s)
	ref := SessionRef{MachineID: "server", SessionID: bg}
	rounds := func() []map[string]any {
		var out []map[string]any
		for _, e := range testEvents(s) {
			if e.Kind == roundEvent && e.SessionID == bg {
				out = append(out, e.Detail)
			}
		}
		return out
	}
	s.TurnStarted(ctx, ref)
	s.TurnEnded(ctx, ref)
	waitFor(t, "its first round", func() bool { return len(rounds()) == 1 })
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })
	s.TurnStarted(ctx, ref)
	s.TurnEnded(ctx, ref)
	waitFor(t, "its second round", func() bool { return len(rounds()) == 2 })
	for i, r := range rounds() {
		if r["source"] != proto.SourceSupervision || r["main"] != false {
			t.Errorf("round %d: %v", i+1, r)
		}
	}

	// A round seen starting before the start answered is named once it has.
	s.avail.mu.Lock()
	s.avail.rounds[[2]string{"server", "bg-late"}] = availRound{source: roundOther, started: time.Now()}
	s.avail.mu.Unlock()
	s.availStartedBy("server", "bg-late", proto.SourceSupervision, time.Now())
	s.avail.mu.Lock()
	got := s.avail.rounds[[2]string{"server", "bg-late"}].source
	s.avail.mu.Unlock()
	if got != proto.SourceSupervision {
		t.Fatalf("the round already under way is %q", got)
	}
}
