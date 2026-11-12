package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// shortSuperviseTimings are the supervision timings shortSupervise gives a test's Service.
var shortSuperviseTimings = superviseTimings{gather: 20 * time.Millisecond, every: 150 * time.Millisecond, urgent: 60 * time.Millisecond}

// shortSupervise makes s's supervision messages come at once and often, for a test. The timings are s's own, not the
// package's: a digest timer still running after the test ends reads only them, never another test's. The timer is
// stopped when the test ends.
func shortSupervise(t *testing.T, s *Service) {
	t.Helper()
	s.superviseTimes = shortSuperviseTimings
	t.Cleanup(func() {
		b := &s.supervise
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.timer != nil {
			b.timer.Stop()
			b.timer, b.timerAt = nil, time.Time{}
		}
	})
}

// The first signal waits superviseGather; after a message, a signal waits superviseEvery (an urgent one
// superviseUrgentEvery) from it.
func TestDigestWait(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	review := superviseSignal{Kind: sigReview, At: now}
	needs := superviseSignal{Kind: sigNeedsYou, At: now}
	for _, c := range []struct {
		name    string
		last    time.Time
		pending []superviseSignal
		want    time.Duration
		ok      bool
	}{
		{"none", time.Time{}, nil, 0, false},
		{"first", time.Time{}, []superviseSignal{review}, superviseGather, true},
		{"gathered", time.Time{}, []superviseSignal{{Kind: sigReview, At: now.Add(-time.Minute)}}, 0, true},
		{"review after a message", now.Add(-time.Minute), []superviseSignal{review}, superviseEvery - time.Minute, true},
		{"urgent after a message", now.Add(-time.Minute), []superviseSignal{needs}, superviseUrgentEvery - time.Minute, true},
		{"the soonest of them", now.Add(-time.Minute), []superviseSignal{review, needs}, superviseUrgentEvery - time.Minute, true},
		{"long after", now.Add(-time.Hour), []superviseSignal{review}, superviseGather, true},
	} {
		got, ok := digestWait(now, c.last, c.pending, (&Service{}).superviseTimings())
		if got != c.want || ok != c.ok {
			t.Errorf("%s: digestWait = %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// A Service's own supervision timings take the place of the package's, each one it sets; the rest stay the package's.
func TestSuperviseTimingsOnService(t *testing.T) {
	want := superviseTimings{gather: superviseGather, every: superviseEvery, urgent: superviseUrgentEvery}
	if got := (&Service{}).superviseTimings(); got != want {
		t.Fatalf("defaults = %+v; want %+v", got, want)
	}
	s := &Service{superviseTimes: superviseTimings{every: time.Second}}
	want.every = time.Second
	if got := s.superviseTimings(); got != want {
		t.Fatalf("with every set = %+v; want %+v", got, want)
	}
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	short := &Service{superviseTimes: shortSuperviseTimings}
	got, ok := digestWait(now, now, []superviseSignal{{Kind: sigNeedsYou, At: now}}, short.superviseTimings())
	if !ok || got != shortSuperviseTimings.urgent {
		t.Fatalf("digestWait with short timings = %v, %v; want %v, true", got, ok, shortSuperviseTimings.urgent)
	}
}

// What the agents' state alone says: waiting for an answer for 3 minutes (a quiet task's at once), working with
// nothing moving for max(20 minutes, 1.5× the expected time); nothing for a task waiting for review or the user, done,
// or not supervised. The key is the stretch of state: the same wait is the same key.
func TestWatchSignals(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	sup := &Supervision{Supervisor: "main", ExpectMinutes: 40}
	waiting := func(ago time.Duration) proto.Session {
		return proto.Session{MachineID: "box", SessionID: "s1", State: proto.StateNeedsYou, PendingInteractions: 1, StateSince: now.Add(-ago)}
	}
	working := func(ago time.Duration) proto.Session {
		return proto.Session{MachineID: "box", SessionID: "s1", State: proto.StateWorking, UpdatedAt: now.Add(-ago), Activity: "在编译"}
	}
	kinds := func(list []superviseSignal) string {
		var out []string
		for _, g := range list {
			out = append(out, g.Kind)
		}
		return strings.Join(out, ",")
	}
	// #5124: a supervised agent waiting on its background work (or stopped) whose transcript stays silent is told past
	// max(2h, 1.5 × expect), whatever its state says.
	background := func(ago time.Duration) proto.Session {
		return proto.Session{MachineID: "box", SessionID: "s1", State: proto.StateIdle, Status: proto.StatusInterrupted, UpdatedAt: now.Add(-ago),
			Work: &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Line: "1 background task running"}}
	}
	long := &Supervision{Supervisor: "main", ExpectMinutes: 180} // 180 × 1.5 = 4.5 h
	leader := proto.Session{MachineID: "box", SessionID: "s1", Assistant: true, State: proto.StateIdle, UpdatedAt: now.Add(-10 * time.Hour)}
	done := now
	for _, c := range []struct {
		name   string
		todo   Todo
		agents []proto.Session
		want   string
	}{
		{"waits a minute", Todo{ID: "t", Supervision: sup}, []proto.Session{waiting(time.Minute)}, ""},
		{"waits long", Todo{ID: "t", Supervision: sup}, []proto.Session{waiting(4 * time.Minute)}, sigNeedsYou},
		{"quiet waits", Todo{ID: "t", Supervision: sup, Quiet: true}, []proto.Session{waiting(time.Second)}, sigNeedsYou},
		{"works", Todo{ID: "t", Supervision: sup}, []proto.Session{working(30 * time.Minute)}, ""},         // 40 × 1.5 = 60
		{"stuck", Todo{ID: "t", Supervision: sup}, []proto.Session{working(61 * time.Minute)}, sigStalled}, // over 60
		{"stuck, no estimate", Todo{ID: "t", Supervision: &Supervision{}}, []proto.Session{working(21 * time.Minute)}, sigStalled},
		{"waits for review", Todo{ID: "t", Supervision: &Supervision{State: reviewPending}}, []proto.Session{waiting(time.Hour)}, sigReview},
		{"waits for the user", Todo{ID: "t", Supervision: &Supervision{State: reviewNeedsUser}}, []proto.Session{working(time.Hour)}, ""},
		{"done", Todo{ID: "t", Supervision: sup, DoneAt: &done}, []proto.Session{waiting(time.Hour)}, ""},
		{"not supervised", Todo{ID: "t"}, []proto.Session{waiting(time.Hour)}, ""},
		{"waits, transcript silent under 2h", Todo{ID: "t", Supervision: sup}, []proto.Session{background(119 * time.Minute)}, ""},
		{"waits, transcript silent past 2h", Todo{ID: "t", Supervision: sup}, []proto.Session{background(2 * time.Hour)}, sigStalled},
		{"waits, under 1.5 × expect", Todo{ID: "t", Supervision: long}, []proto.Session{background(4 * time.Hour)}, ""},
		{"waits, past 1.5 × expect", Todo{ID: "t", Supervision: long}, []proto.Session{background(270 * time.Minute)}, sigStalled},
		{"a leader conversation waits on its workers", Todo{ID: "t", Supervision: sup}, []proto.Session{leader}, ""},
	} {
		if got := kinds(watchSignals(c.todo, c.agents, now)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	a := watchSignals(Todo{ID: "t", Supervision: sup}, []proto.Session{waiting(4 * time.Minute)}, now)
	b := watchSignals(Todo{ID: "t", Supervision: sup}, []proto.Session{waiting(4 * time.Minute)}, now.Add(time.Minute))
	if a[0].Key != b[0].Key || a[0].Minutes != 4 || b[0].Minutes != 5 {
		t.Fatalf("the same wait: %+v / %+v", a[0], b[0])
	}
}

// newSupervisedService is newQuietService with a task handed out from the main conversation, its agent s1 on box
// at work, and the criteria given.
func newSupervisedService(t *testing.T) (*Service, *fakeHub, Todo) {
	t.Helper()
	return newSupervisedServiceOn(t, nil)
}

// newSupervisedServiceOn is newSupervisedService with st as its database (nil: none).
func newSupervisedServiceOn(t *testing.T, st Store) (*Service, *fakeHub, Todo) {
	t.Helper()
	s, f := newQuietServiceOn(t, st)
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	raw, _ := json.Marshal(map[string]any{"tasks": []map[string]any{{
		"title": "修登录崩溃", "prompt": "iPad 横屏时登录页崩溃，修好并补测试", "machine": "box", "cwd": "/code/app", "readonly": true,
		"why": "这个仓库最近在 box 上", "done_when": []string{"PR 合并到 master", " ", "浅色 / 深色截图贴进 PR", "PR 合并到 master"},
		"expect_minutes": 30,
	}}})
	if _, err := s.toolTaskPlan(context.Background(), "conv1", raw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	todo := onlyTodo(s)
	if v := todo.Supervision; v == nil || len(v.DoneWhen) != 2 || v.ExpectMinutes != 30 || v.Supervisor != "conv1" {
		t.Fatalf("supervision = %+v", todo.Supervision)
	}
	return s, f, todo
}

func sentToAgent(f *fakeHub) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, a := range f.agentSent {
		text, _ := a["text"].(string)
		out = append(out, text)
	}
	return out
}

// The whole round: the agent is told the criteria; it stops asking which way to go and the main conversation hears
// it (one message, not pushed); it answers, stops with the work done: waits for review; rework sends the gaps back
// (round 1, 2); a third rework goes to the user; accepted closes the todo by review.
func TestSupervisedTaskRound(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	presenceTimes(t, 0, 0) // the user speaks below; what comes after is not held for them (relay_presence.go)
	ctx := context.Background()
	f.mu.Lock()
	prompt, _ := f.starts[0]["prompt"].(string)
	f.mu.Unlock()
	if !strings.Contains(prompt, "验收标准") || !strings.Contains(prompt, "- 浅色 / 深色截图贴进 PR") {
		t.Fatalf("the agent was not told the criteria:\n%s", prompt)
	}

	// It stops to ask: the judge says it waits for the user; the main conversation is told so.
	f.judge = `{"verdict":"needs_user","reason":"问用两种部署走法里的哪一种"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "两种走法：A 先灰度，B 直接全量。你选一个？", 0)
	waitFor(t, "the message", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	for _, want := range []string{"📋 督办", "Agent 停下了，但没做完（要用户决定：问用两种部署走法里的哪一种）", "验收标准：PR 合并到 master；浅色 / 深色截图贴进 PR",
		"A 先灰度，B 直接全量", "能自己拍板的就自己拍"} {
		if !strings.Contains(n, want) {
			t.Errorf("message lacks %q:\n%s", want, n)
		}
	}
	if !s.supervising("conv1") {
		t.Fatal("its turn would be pushed")
	}
	var held bool
	for _, v := range s.snapshot(ctx).Sessions {
		held = held || v.SessionID == "conv1" && v.Supervising
	}
	if !held {
		t.Fatal("the snapshot does not say the turn is held")
	}
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	if s.supervising("conv1") {
		t.Fatal("still held after the user spoke")
	}

	// The same stop is told once.
	s.taskTurnEnded(ctx, "box", "s1")
	time.Sleep(4 * shortSuperviseTimings.every)
	if got := len(f.notices()); got != 1 {
		t.Fatalf("%d messages", got)
	}

	// The assistant decided; the agent works and is done: waits for review, told again.
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "按 A 做完了，PR https://github.com/o/r/pull/901 已合并", 0)
	waitFor(t, "the review message", func() bool { return len(f.notices()) == 2 })
	if n := f.notices()[1]; !strings.Contains(n, "hub 判断做完了，等你验收（PR 已合并）") || !strings.Contains(n, "pull/901") {
		t.Fatalf("message:\n%s", n)
	}
	if now := todoNow(s, todo.ID); now.DoneAt != nil || now.Supervision.State != reviewPending {
		t.Fatalf("not waiting for review: %+v", now.Supervision)
	}

	review := func(verdict, note string, checks ...ReviewCheck) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": note, "checks": checks})
		res, err := s.runTool(ctx, "server", "conv1", "task_review", raw)
		if err != nil {
			t.Fatalf("%s: %v", verdict, err)
		}
		return res
	}
	for round := 1; round <= maxReworkRounds; round++ {
		res := review("rework", "截图只有浅色，补深色", ReviewCheck{Criterion: "PR 合并到 master", Met: true, Evidence: "pull/901 merged"},
			ReviewCheck{Criterion: "浅色 / 深色截图贴进 PR", Met: false, Evidence: "只有浅色"})
		if !strings.Contains(res, "Rework round") {
			t.Fatalf("round %d: %s", round, res)
		}
		sent := sentToAgent(f)
		if len(sent) != round || !strings.Contains(sent[round-1], "第 "+string(rune('0'+round))+" 轮返工") ||
			!strings.Contains(sent[round-1], "❌ 浅色 / 深色截图贴进 PR：只有浅色") || !strings.Contains(sent[round-1], "补深色") {
			t.Fatalf("round %d sent: %q", round, sent)
		}
		if v := todoNow(s, todo.ID).Supervision; v.State != reviewRework || v.Round != round {
			t.Fatalf("round %d: %+v", round, v)
		}
		agentTurn(s, f, proto.StatusWorking, "", 0)
		if v := todoNow(s, todo.ID).Supervision; v.State != reviewRework {
			t.Fatalf("working on round %d: %+v", round, v)
		}
		agentTurn(s, f, proto.StatusDone, "补了深色截图", 0)
		waitFor(t, "the review again", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })
	}
	// A third rework goes to the owner, and only as a decision they can answer (#3804): without one it is refused.
	raw3, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "还是不对"})
	if _, err := s.runTool(ctx, "server", "conv1", "task_review", raw3); err == nil || !strings.Contains(err.Error(), "sent back 2 times") {
		t.Fatalf("third rework with no decision: %v", err)
	}
	raw3, _ = json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "还是不对", "why_owner": "priority",
		"reason": "两轮返工后截图还是不对，怎么办", "recommend": "再返工一轮", "cost_of_waiting": "PR 一直开着",
		"options": []map[string]string{{"label": "再返工一轮", "leads_to": "再等半小时"}, {"label": "不做了", "leads_to": "关掉任务"}}})
	if res, err := s.runTool(ctx, "server", "conv1", "task_review", raw3); err != nil || !strings.Contains(res, "waits for the user") {
		t.Fatalf("third rework: %s %v", res, err)
	}
	if v := todoNow(s, todo.ID).Supervision; v.State != reviewNeedsUser || v.Round != maxReworkRounds || len(sentToAgent(f)) != maxReworkRounds {
		t.Fatalf("after the third: %+v, sent %d", v, len(sentToAgent(f)))
	}
	if res := review("accepted", "用户看过截图，可以"); !strings.Contains(res, "accepted and closed") {
		t.Fatalf("accepted: %s", res)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.DoneBy != db.DoneByReview || done.DoneReason != "用户看过截图，可以" || done.Supervision.State != reviewAccepted ||
		len(done.Supervision.Reviews) != maxReworkRounds+2 {
		t.Fatalf("accepted: %+v %+v", done, done.Supervision)
	}
	if !strings.Contains(feedKinds(s, "conv1"), "review") {
		t.Fatalf("feed: %s", feedKinds(s, "conv1"))
	}
	// Done: rework is refused (a new task for what is left).
	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "rework", "note": "x"})
	if _, err := s.runTool(ctx, "server", "conv1", "task_review", raw); err == nil {
		t.Fatal("rework on a done task")
	}
	// Another conversation does not review it.
	raw, _ = json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted"})
	if _, err := s.runTool(ctx, "server", "conv2", "task_review", raw); err == nil {
		t.Fatal("reviewed from another conversation")
	}
}

// Several tasks' signals within the gather time are one message, what waits on a decision first; a message still
// queued behind the main conversation's turn takes the next ones in instead of being followed by another.
func TestDigestBatches(t *testing.T) {
	s, f := newQuietService(t)
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"甲", "乙"} {
		todo, err := s.createTodo(ctx, title, "做"+title, "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		s.superviseTodo(ctx, todo.ID, "conv1", []string{"PR 合并"}, 0)
		ids = append(ids, todo.ID)
	}
	if _, _, err := s.updateSupervision(ctx, ids[0], func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	s.signal(superviseSignal{Todo: ids[0], Kind: sigReview, Key: "1", Detail: "做完了"})
	s.signal(superviseSignal{Todo: ids[1], Kind: sigFailed, Key: "x", Detail: "box 离线"})
	waitFor(t, "the message", func() bool { return len(f.notices()) == 1 })
	n := f.notices()[0]
	if !strings.Contains(n, "有 2 个要你处理") || strings.Index(n, "任务「乙」") > strings.Index(n, "任务「甲」") {
		t.Fatalf("message:\n%s", n)
	}

	busy := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", busy)
	s.signal(superviseSignal{Todo: ids[0], Kind: sigStalled, Key: "a", Minutes: 25})
	waitFor(t, "the queued message", func() bool { return s.queuedFor("conv1") == 1 })
	s.signal(superviseSignal{Todo: ids[1], Kind: sigIdle, Key: "b", Detail: "没做完"})
	time.Sleep(3 * shortSuperviseTimings.every)
	if got := s.queuedFor("conv1"); got != 1 {
		t.Fatalf("%d queued", got)
	}
	idle := busy
	idle.Status = proto.StatusIdle
	f.setSessions("server", idle)
	s.drainRelays("server", []proto.Session{idle})
	waitFor(t, "the merged message", func() bool { return len(f.notices()) == 2 })
	if n := f.notices()[1]; !strings.Contains(n, "25 分钟没有进展") || !strings.Contains(n, "Agent 停下了，但没做完（没做完）") {
		t.Fatalf("merged:\n%s", n)
	}
}

func TestTaskHealth(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	done := now
	for _, c := range []struct {
		todo   Todo
		agents []proto.Session
		want   string
	}{
		{Todo{}, nil, ""},
		{Todo{Supervision: &Supervision{}}, []proto.Session{{State: proto.StateWorking, UpdatedAt: now}}, "正常"},
		{Todo{Supervision: &Supervision{}}, []proto.Session{{State: proto.StateWorking, UpdatedAt: now.Add(-25 * time.Minute)}}, "卡住 25 分钟"},
		{Todo{Supervision: &Supervision{}}, []proto.Session{{State: proto.StateNeedsYou, StateSince: now.Add(-5 * time.Minute)}}, "等回答 5 分钟"},
		{Todo{Supervision: &Supervision{State: reviewPending}}, nil, "待验收"},
		{Todo{Supervision: &Supervision{State: reviewRework, Round: 2}}, []proto.Session{{State: proto.StateWorking, UpdatedAt: now}}, "返工第 2 轮"},
		{Todo{Supervision: &Supervision{State: reviewNeedsUser}}, nil, "等你决定"},
		{Todo{Supervision: &Supervision{State: reviewAccepted}, DoneAt: &done}, nil, "已验收"},
		{Todo{Supervision: &Supervision{State: reviewAccepted}}, nil, "正常"}, // reopened by the user
		{Todo{Supervision: &Supervision{Waits: &OwnerWait{At: now}}}, nil, "停下等用户拍板，别直接验收"},
	} {
		if got, _ := taskHealth(c.todo, c.agents, now); got != c.want {
			t.Errorf("%+v: %q, want %q", c.todo.Supervision, got, c.want)
		}
	}
}

func TestDuplicateHints(t *testing.T) {
	task := PlanTask{Title: "iOS 侧边栏显示扩展页面", Prompt: "做 #2813", Machine: "mac", Cwd: "/code/app"}
	todos := []Todo{
		{ID: "t1", Title: "iPad 侧边栏", Notes: "见 issues/2813", Agents: []TodoAgent{{MachineID: "box", Cwd: "/x"}}},
		{ID: "t2", Title: "iOS 侧边栏显示扩展的页面", Agents: []TodoAgent{{MachineID: "box", Cwd: "/y"}}},
		{ID: "t3", Title: "修登录崩溃", Agents: []TodoAgent{{MachineID: "mac", Cwd: "/code/app/ios"}}},
		{ID: "t4", Title: "iOS 侧边栏显示扩展页面", DoneAt: new(time.Now())},
	}
	sessions := []proto.Session{
		{MachineID: "mac", SessionID: "abcdef123456", Cwd: "/code/app", Title: "侧边栏扩展页面", State: proto.StateWorking},
		{MachineID: "mac", SessionID: "zzz", Cwd: "/code/app", Title: "无关的事", State: proto.StateWorking},
		{MachineID: "mac", SessionID: "yyy", Cwd: "/code/app", Title: "侧边栏扩展页面", State: proto.StateIdle},
	}
	got := strings.Join(duplicateHints(task, todos, sessions), "\n")
	for _, want := range []string{"任务「iPad 侧边栏」（t1）也提到 #2813", "任务「iOS 侧边栏显示扩展的页面」（t2）标题相近", "Agent「侧边栏扩展页面」"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"t3", "t4", "无关", "yyy"} {
		if strings.Contains(got, not) {
			t.Errorf("has %q:\n%s", not, got)
		}
	}
	if refs := issueRefs("修 #12 和 #2813，见 https://github.com/o/r/pull/3001"); strings.Join(refs, ",") != "#2813,#3001" {
		t.Fatalf("refs = %q", refs)
	}
}

// task_plan says what looks like work already going on, and plans it all the same.
func TestTaskPlanSaysDuplicates(t *testing.T) {
	s, f := newQuietService(t)
	f.setSessions("box", proto.Session{SessionID: "busy", MachineID: "box", Cwd: "/code/app", Title: "修登录崩溃的问题", State: proto.StateWorking,
		Status: proto.StatusWorking})
	res, err := s.toolTaskPlan(context.Background(), "conv1", planArgs(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "Possible duplicates") || !strings.Contains(res, "「修登录崩溃」可能和这些重复") {
		t.Fatalf("result: %s", res)
	}
	waitFor(t, "the task", func() bool { return todoCount(s) == 1 })
}

// When the main conversation changes, the new main conversation can review older supervised tasks (#4262).
func TestSupervisedTaskNewMainConversationCanReview(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	ctx := context.Background()
	f.known["conv2"] = true
	f.setSessions("server",
		proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv3", MachineID: "server", Assistant: true, Status: proto.StatusIdle})

	// Make the agent finish its work and wait for review.
	f.judge = `{"verdict":"done","reason":"PR 已合并"}`
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "修好了，提了 PR", 0)
	waitFor(t, "the review state", func() bool { return todoNow(s, todo.ID).Supervision.State == reviewPending })

	// Switch main conversation to conv2.
	setSettings(t, s, func(v *Settings) { v.Main = "conv2" })

	// conv3 is neither origin, nor supervisor, nor main -> refused.
	raw, _ := json.Marshal(map[string]any{"todo_id": todo.ID, "verdict": "accepted", "note": "验收通过"})
	if _, err := s.runTool(ctx, "server", "conv3", "task_review", raw); err == nil {
		t.Fatal("conv3 should not be allowed to review")
	}

	// conv2 is the new main conversation -> succeeds!
	res, err := s.runTool(ctx, "server", "conv2", "task_review", raw)
	if err != nil {
		t.Fatalf("conv2 review failed: %v", err)
	}
	if !strings.Contains(res, "accepted and closed") {
		t.Fatalf("unexpected review result: %s", res)
	}
	done := todoNow(s, todo.ID)
	if done.DoneAt == nil || done.Supervision.State != reviewAccepted || done.Supervision.Supervisor != "conv2" {
		t.Fatalf("unexpected done todo: %+v, supervision: %+v", done, done.Supervision)
	}
}

func TestGoldenSupervise(t *testing.T) {
	golden(t, "supervise.digest", prompts.RenderSupervise(prompts.Supervise{Entries: sortEntries([]prompts.SuperviseEntry{
		{ID: "todo_1", Title: "修登录崩溃", Kind: sigReview, Detail: "PR 已合并", DoneWhen: []string{"PR 合并到 master", "浅色 / 深色截图贴进 PR"},
			Machine: "box", Session: "a1b2c3d4", Last: "修好了，PR https://github.com/o/r/pull/901 已合并", Link: "https://hub.example.com/todos?id=todo_1",
			Review: &prompts.SuperviseReviewed{State: autoClean, Count: 1, Findings: "1. P2 login/view.swift:12 变量名可以更清楚"}},
		{ID: "todo_2", Title: "部署 hub", Kind: sigIdle, Detail: "要用户决定：两种走法选一个", Machine: "box", Round: 1, Last: "A 先灰度，B 直接全量。选哪个？",
			Closeout: &prompts.SuperviseCloseout{Status: "blocked", Waiting: "用户选走法", Merged: "yes", Deployed: "no",
				Did: []string{"部署脚本加了灰度开关"}, Artifacts: []string{"pr https://github.com/o/r/pull/902"}, Verified: "untested", Missing: "选 A 还是 B", CheckedMerged: "yes", CheckedDeployed: "no"}},
		{ID: "todo_3", Title: "整理材料", Kind: sigNeedsYou, Minutes: 4, Quiet: true, Ask: "- 用哪个分支？（选项：main / dev）"},
		{ID: "todo_4", Title: "跑 UI 测试", Kind: sigStalled, Minutes: 35, Detail: "在等编译"},
		{ID: "todo_5", Title: "迁移数据库", Kind: sigReport, Report: upReportQuestion, Said: "要不要先停写？\n停写约 2 分钟", Step: 2, Steps: 4,
			Evidence: []string{"https://github.com/o/r/pull/902"}, Machine: "box", Session: "e5f6a7b8"},
		{ID: "todo_6", Title: "整理文档", Kind: sigReport, Report: upReportProgress, Said: "写完了一半"},
		// #2570: a start whose tab the computer keeps, one that failed for good, one that started after a retry
		{ID: "todo_7", Title: "修崩溃", Kind: sigFailed, Machine: "box", KeptUntil: "10:12", Question: "[oh-my-zsh] Would you like to update? [Y/n]",
			Screen: "Last login: Mon Nov  9 22:01:13 on ttys004\n[oh-my-zsh] Would you like to update? [Y/n]", RetriesLeft: 3},
		{ID: "todo_8", Title: "跑基准", Kind: sigFailed, Detail: "新开的终端标签两次都没接住启动命令", Machine: "box"},
		{ID: "todo_9", Title: "整理日志", Kind: sigStarted, Detail: "新标签里的 shell 停在一个提问上", Machine: "box"},
		// #3618: nobody works on it any more: closed after a model error, closed, its computer away
		{ID: "todo_10", Title: "升级依赖", Kind: sigOrphaned, Orphan: orphanError, Detail: "API Error: 400 prompt is too long", Machine: "box", Session: "c3d4e5f6",
			Last: "API Error: 400 prompt is too long", DoneWhen: []string{"CI 通过"}},
		{ID: "todo_11", Title: "清理旧分支", Kind: sigOrphaned, Orphan: orphanClosed, Machine: "box", Session: "d4e5f6a7", Last: "删了 12 个，还剩 3 个要确认"},
		{ID: "todo_12", Title: "跑回归", Kind: sigOrphaned, Orphan: orphanOffline, Machine: "laptop"},
	})}))
	golden(t, "supervise.rework", prompts.RenderSuperviseRework(prompts.SuperviseRework{Round: 1, Max: 2, Note: "补深色截图",
		Checks: []prompts.SuperviseCheck{{Criterion: "PR 合并到 master", Met: true, Evidence: "pull/901 merged"}, {Criterion: "浅色 / 深色截图贴进 PR", Evidence: "只有浅色"}}}))
	golden(t, "supervise.done_when", prompts.RenderTaskDoneWhen([]string{"PR 合并到 master", "线上 hub 已部署并验证"}))
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	golden(t, "assistant_snapshot.supervised", prompts.RenderSnapshot(prompts.Snapshot{Now: now, Zone: "UTC", Tasks: []prompts.SnapshotTask{
		{ID: "todo_2", Title: "部署 hub", State: "停着", Health: "等你决定", DoneWhen: 1},
		{ID: "todo_1", Title: "修登录崩溃", State: "做完了（用户还没看）", Health: "待验收", DoneWhen: 2},
		{ID: "todo_3", Title: "跑 UI 测试", State: "在做", Health: "卡住 35 分钟"},
		{ID: "todo_4", Title: "迁移数据库", State: "在做", Health: "正常", DoneWhen: 1,
			Reported: &prompts.SnapshotReport{Kind: "progress", Step: 3, Steps: 5, Text: "在跑迁移脚本", Minutes: 4}},
		{ID: "todo_5", Title: "整理文档", State: "停着", Health: "正常", Reported: &prompts.SnapshotReport{Kind: "question", Text: "放 docs 还是 wiki？"}},
		{ID: "todo_6", Title: "升级依赖", State: "被打断了", Health: "没人在做", Orphan: orphanError, Machine: "box", Session: "c3d4e5f6", Last: "API Error: 400 prompt is too long"},
		{ID: "todo_7", Title: "清理旧分支", State: "停着", Orphan: orphanClosed},
		{ID: "todo_8", Title: "跑回归", State: "停着", Orphan: orphanOffline},
	}}))
}
