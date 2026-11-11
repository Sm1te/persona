package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// Decisions that can wait (#4930, decision_wait.go). All names, ids and texts are placeholders.

// briefingOn gives s one daily routine with facts on (enabled as given), due within the hour: what a held decision
// counts on.
func briefingOn(s *Service, enabled bool) {
	briefingIn(s, time.Hour, enabled)
}

// briefingIn is briefingOn with the routine's next run in (to the minute, earlier), and every day at that time.
func briefingIn(s *Service, in time.Duration, enabled bool) {
	at := time.Now().Add(in).In(s.location(context.Background()))
	setTasks(s, Task{ID: "morning", Title: "morning", Prompt: "p", Repeat: repeatDaily, Time: at.Format("15:04"), Enabled: enabled, Facts: true})
}

// waitingDecision is rolloutDecision marked can_wait.
func waitingDecision() map[string]any {
	a := rolloutDecision()
	a["can_wait"] = true
	return a
}

const rolloutReason = "先灰度还是直接全量"

// A decision that can wait is raised without a push and without a card, and is in Needs you at once with its default.
func TestDecisionThatCanWaitIsNotPushed(t *testing.T) {
	s, f, todo, posts, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法：A 先灰度，B 直接全量。")
	res := reviewTask(t, s, todo.ID, waitingDecision())
	if !strings.Contains(res, "can wait (can_wait)") || strings.Contains(res, "A push with your recommendation") {
		t.Fatalf("task_review's answer: %s", res)
	}
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("%d pushes, want none", n)
	}
	if cards := cardsWith(posts(), rolloutReason); len(cards) != 0 {
		t.Fatalf("a card went out: %q", cards)
	}
	a := askOf(s, todo.ID)
	if a == nil || !a.ForBriefing || !a.NoCard || a.MessageID != "" || !a.Reversible {
		t.Fatalf("ask %+v", a)
	}
	if d := time.Until(a.Deadline); d < 23*time.Hour || d > 24*time.Hour {
		t.Fatalf("its 24-hour default moved: in %v", d)
	}
	ask, ok := s.todoAsks(context.Background())[todo.ID]
	if !ok || ask.Recommend != "先灰度 10%" || ask.Default != defaultTaken || !ask.ForBriefing {
		t.Fatalf("not in Needs you as any decision, marked as held: %+v", ask)
	}
	s.retryAcceptance(time.Now().Add(20 * time.Minute)) // no card is due while it waits
	if cards := cardsWith(posts(), rolloutReason); len(cards) != 0 {
		t.Fatalf("the retry posted its card: %q", cards)
	}
}

// can_wait is ignored, and the answer says why, for a decision that is never taken by default, one whose deadline is
// near, without a daily briefing with facts, and when none is due while the owner would still have 12 hours to
// answer; without can_wait nothing changes.
func TestDecisionThatCannotWaitIsPushedAsBefore(t *testing.T) {
	on := func(s *Service) { briefingOn(s, true) }
	for _, c := range []struct {
		name     string
		args     func() map[string]any
		briefing func(*Service)
		ignored  string
	}{
		{"cannot be undone", func() map[string]any { a := dropTableDecision(); a["can_wait"] = true; return a }, on, waitNoNeverDefault},
		{"others will see it", func() map[string]any { a := waitingDecision(); a["why_owner"] = whyVisible; return a }, on, waitNoNeverDefault},
		{"a deadline that is not taken by default", func() map[string]any {
			a := waitingDecision()
			a["deadline"], a["reversible"] = "20h", false
			return a
		}, on, waitNoNeverDefault},
		{"a deadline within 12 hours", func() map[string]any {
			a := waitingDecision()
			a["deadline"], a["reversible"] = "11h", true
			return a
		}, on, waitNoDeadline},
		{"the briefing is off", waitingDecision, func(s *Service) { briefingOn(s, false) }, waitNoBriefing},
		// The default is taken 24 hours after it is raised: a briefing 14 hours from now leaves the owner 10.
		{"the briefing comes too late for its default", waitingDecision, func(s *Service) { briefingIn(s, 14*time.Hour, true) }, waitNoBriefingTime},
		{"the briefing comes too late for its own deadline", func() map[string]any {
			a := waitingDecision()
			a["deadline"], a["reversible"] = "13h", true
			return a
		}, func(s *Service) { briefingIn(s, 3*time.Hour, true) }, waitNoBriefingTime},
		{"no briefing within 24 hours", func() map[string]any {
			a := waitingDecision()
			a["deadline"], a["reversible"] = "120h", true
			return a
		}, func(s *Service) {
			day := int(time.Now().Add(48 * time.Hour).In(s.location(context.Background())).Weekday())
			if day == 0 {
				day = 7
			}
			setTasks(s, Task{ID: "morning", Title: "morning", Prompt: "p", Repeat: repeatDaily, Time: "12:00", Weekdays: []int{day}, Enabled: true, Facts: true})
		}, waitNoBriefingSoon},
		{"not marked", rolloutDecision, on, ""},
	} {
		s, f, todo, posts, _ := conclusionService(t, false)
		c.briefing(s)
		waitsForReview(t, s, f, todo.ID, "两种走法。")
		args := c.args()
		res := reviewTask(t, s, todo.ID, args)
		if c.ignored != "" && !strings.Contains(res, "can_wait was ignored ("+c.ignored+")") || c.ignored == "" && strings.Contains(res, "can_wait") {
			t.Errorf("%s: task_review's answer: %s", c.name, res)
		}
		a := askOf(s, todo.ID)
		if a == nil || a.ForBriefing || a.NoCard || a.MessageID == "" {
			t.Errorf("%s: ask %+v", c.name, a)
		}
		if n := deadlinePushes(f, todo.ID); n != 1 {
			t.Errorf("%s: %d pushes, want 1", c.name, n)
		}
		if cards := cardsWith(posts(), args["reason"].(string)); len(cards) != 1 {
			t.Errorf("%s: %d cards, want 1", c.name, len(cards))
		}
	}
	// A deadline of its own waits when a briefing is due 12 hours before it.
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	args := waitingDecision()
	args["deadline"], args["reversible"] = "13h", true
	reviewTask(t, s, todo.ID, args)
	if a := askOf(s, todo.ID); !a.ForBriefing || deadlinePushes(f, todo.ID) != 0 {
		t.Errorf("a 13-hour deadline with a briefing within the hour did not wait: %+v", a)
	}
}

// acceptPutFails is a store that cannot write the asks.
type acceptPutFails struct {
	*memStore
	fail bool
}

func (m *acceptPutFails) PutSetting(ctx context.Context, key string, v any) error {
	if key == acceptanceKey && m.fail {
		return errors.New("the store is away")
	}
	return m.memStore.PutSetting(ctx, key, v)
}

// A decision whose hold could not be written is not held in memory only: it is pushed when it is raised, and the
// supervisor is not told that it waits for a briefing.
func TestDecisionThatCannotBeKeptAsHeldIsPushed(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	s.accept.mu.Lock()
	s.loadAcceptLocked(context.Background())
	s.accept.mu.Unlock()
	st := &acceptPutFails{memStore: &memStore{}, fail: true}
	s.store = func() Store { return st }
	res := reviewTask(t, s, todo.ID, waitingDecision())
	if strings.Contains(res, "can wait (can_wait)") || !strings.Contains(res, "can_wait was ignored ("+waitNotKept+")") ||
		!strings.Contains(res, "A push with your recommendation") {
		t.Fatalf("task_review's answer: %s", res)
	}
	if a := askOf(s, todo.ID); a == nil || a.ForBriefing || a.NoCard {
		t.Fatalf("ask %+v", a)
	}
	if n := deadlinePushes(f, todo.ID); n != 1 {
		t.Fatalf("%d pushes, want 1", n)
	}
}

// can_wait is the decision's: any other verdict with it is refused.
func TestCanWaitIsForDecisionsOnly(t *testing.T) {
	s, f, todo := newSupervisedService(t)
	waitsForReview(t, s, f, todo.ID, "做完了。")
	if _, err := reviewErr(s, todo.ID, map[string]any{"verdict": "accepted", "can_wait": true}); err == nil ||
		!strings.Contains(err.Error(), "can_wait applies to needs_user only") {
		t.Fatalf("accepted with can_wait: %v", err)
	}
}

// The briefing lists a held decision with its recommendation; once one reached the owner with it, it is not pushed later.
func TestBriefingListsADecisionThatWaited(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	now := time.Now()
	facts := s.briefFactsAt(context.Background(), now, "morning")
	if len(facts.Accept) != 1 || facts.Accept[0].id != todo.ID {
		t.Fatalf("the briefing's rows: %+v", facts.Accept)
	}
	if row := facts.body(s, "morning").Accept[0]; row.Outcome != outcomeDecide || row.Recommend != "先灰度 10%" || row.AskedAt.IsZero() {
		t.Fatalf("the brief item's row: %+v", row)
	}
	// A run whose facts were made for another round changes nothing.
	old := *facts
	old.Accept = []boardRow{{id: todo.ID, asked: now.Add(-time.Hour)}}
	s.briefListed(&old)
	if !askOf(s, todo.ID).ForBriefing {
		t.Fatal("another round's row ended the wait")
	}
	s.briefListed(facts)
	if a := askOf(s, todo.ID); a.ForBriefing || !a.AcceptedAt.IsZero() {
		t.Fatalf("after the briefing: %+v", a)
	}
	s.sweepWaitingDecisions(now.Add(48 * time.Hour))
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("pushed %d times after a briefing listed it", n)
	}
}

// A held decision no briefing will list is pushed after all, once, with its card: the routine was switched off, or
// none ran within 24 hours.
func TestHeldDecisionIsPushedWhenNoBriefingListsIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		off   bool
		after time.Duration
	}{
		{"the routine was switched off", true, time.Minute},
		{"no briefing ran within 24 hours", false, 24 * time.Hour},
	} {
		s, f, todo, posts, _ := conclusionService(t, false)
		briefingOn(s, true)
		waitsForReview(t, s, f, todo.ID, "两种走法。")
		reviewTask(t, s, todo.ID, waitingDecision())
		s.accept.mu.Lock()
		s.accept.asks[todo.ID].Deadline = time.Time{} // only what is tested ends the wait
		s.accept.mu.Unlock()
		s.sweepWaitingDecisions(time.Now().Add(time.Minute))
		if n := deadlinePushes(f, todo.ID); n != 0 {
			t.Fatalf("%s: pushed while a briefing was still to come", c.name)
		}
		if c.off {
			briefingOn(s, false)
		}
		at := time.Now().Add(c.after)
		f.mu.Lock()
		f.failNotify = 1 // the hub does not take the first push: the decision stays held and the next sweep pushes it
		f.mu.Unlock()
		s.sweepWaitingDecisions(at)
		if a := askOf(s, todo.ID); !a.ForBriefing || deadlinePushes(f, todo.ID) != 0 {
			t.Fatalf("%s: a push the hub did not take ended the wait: %+v", c.name, a)
		}
		at = at.Add(time.Minute)
		s.sweepWaitingDecisions(at)
		s.sweepWaitingDecisions(at.Add(time.Hour))
		if n := deadlinePushes(f, todo.ID); n != 1 {
			t.Errorf("%s: %d pushes, want 1", c.name, n)
		}
		// Its card is due from the push, as a raised decision's is from its ask: the next tick posts it, once.
		if a := askOf(s, todo.ID); a.ForBriefing || a.NoCard || !a.PushedAt.Equal(at) {
			t.Fatalf("%s: ask %+v", c.name, a)
		}
		s.retryAcceptance(at.Add(10 * time.Minute))
		s.retryAcceptance(at.Add(20 * time.Minute))
		if cards := cardsWith(posts(), rolloutReason); len(cards) != 1 {
			t.Errorf("%s: %d cards, want 1", c.name, len(cards))
		}
		if a := askOf(s, todo.ID); a.NoCard || a.MessageID == "" || !a.AcceptedAt.IsZero() {
			t.Errorf("%s: ask %+v", c.name, a)
		}
	}
}

// A card that cannot go out when the held decision is pushed is tried again for acceptPostWindow from the push, not
// from when the decision was raised a day before.
func TestHeldDecisionsCardIsRetriedFromThePush(t *testing.T) {
	s, f, todo, posts, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	s.accept.mu.Lock()
	s.accept.asks[todo.ID].Deadline = time.Time{}
	s.accept.mu.Unlock()
	at := time.Now().Add(decisionWaitFor)
	s.sweepWaitingDecisions(at)
	away := true
	f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		if away {
			return nil, errors.New("the manager is away")
		}
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_me", Features: FeishuFeatures{Main: true, Cards: true, Reports: true}}, nil
	})
	s.retryAcceptance(at.Add(10 * time.Minute))
	if a := askOf(s, todo.ID); a.NoCard || a.MessageID != "" {
		t.Fatalf("a status that could not be read gave the card up: %+v", a)
	}
	away = false
	s.retryAcceptance(at.Add(20 * time.Minute))
	if cards := cardsWith(posts(), rolloutReason); len(cards) != 1 || askOf(s, todo.ID).MessageID == "" {
		t.Fatalf("%d cards after the retry, want 1", len(cards))
	}
}

// A leader's held decision is not told to the main conversation when it is raised (nothing went to the owner, and the
// main conversation is not woken for what can wait); it is told once the decision is pushed after all.
func TestLeadersHeldDecisionEscalatesWhenItIsPushed(t *testing.T) {
	s, f, todo := newLeaderService(t)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	args := waitingDecision()
	args["todo_id"] = todo.ID
	res, err := runJSON(t, s, "lead1", "task_review", args)
	if err != nil || !strings.Contains(res, "can wait (can_wait)") {
		t.Fatalf("the leader's needs_user: %s %v", res, err)
	}
	escalated := func() bool {
		id := superviseSignal{Todo: todo.ID, Kind: sigEscalated}.id()
		s.supervise.mu.Lock()
		defer s.supervise.mu.Unlock()
		_, waits := s.supervise.pending[id]
		return waits || s.supervise.sent[id] != ""
	}
	if escalated() || len(f.noticesTo("conv1")) != 0 {
		t.Fatalf("the main conversation is told of a held decision: %q", f.noticesTo("conv1"))
	}
	s.sweepWaitingDecisions(time.Now().Add(decisionWaitFor))
	if n := deadlinePushes(f, todo.ID); n != 1 {
		t.Fatalf("%d pushes, want 1", n)
	}
	waitFor(t, "the main conversation's message", func() bool { return len(f.noticesTo("conv1")) == 1 })
	if n := f.noticesTo("conv1")[0]; !strings.Contains(n, "负责它的 leader 对话判断要用户决定") || !strings.Contains(n, "leader 推荐：先灰度 10%") {
		t.Fatalf("the main conversation's message:\n%s", n)
	}
}

// A held decision that no briefing brought to the owner is pushed while they still have 11 hours to answer, and its
// default is taken at the deadline as any decision's.
func TestHeldDecisionIsPushedBeforeItsDefaultIsTaken(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	sent := len(sentToAgent(f))
	deadline := askOf(s, todo.ID).Deadline
	s.sweepDecisionDeadlines(deadline.Add(-decisionWaitNotice - time.Minute))
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("pushed %d times with more than 11 hours to its deadline", n)
	}
	s.sweepDecisionDeadlines(deadline.Add(-decisionWaitNotice))
	if n := deadlinePushes(f, todo.ID); n != 1 || len(sentToAgent(f)) != sent {
		t.Fatalf("%d pushes 11 hours before its deadline, want 1; sent %q", n, sentToAgent(f)[sent:])
	}
	pastDeadline(t, s, todo.ID) // its default is unchanged
	s.sweepDecisionDeadlines(time.Now())
	if a := askOf(s, todo.ID); !a.ByDefault || len(sentToAgent(f)) != sent+1 {
		t.Fatalf("its default was not taken at the deadline: %+v", a)
	}
}

// Answered from Needs you while it waits, a held decision is taken as any other and never pushed.
func TestHeldDecisionAnsweredInNeedsYouIsNotPushed(t *testing.T) {
	s, f, todo, _, _ := conclusionService(t, false)
	briefingOn(s, true)
	waitsForReview(t, s, f, todo.ID, "两种走法。")
	reviewTask(t, s, todo.ID, waitingDecision())
	sent := len(sentToAgent(f))
	if rec := answerAsk(s, todo.ID, map[string]string{}); rec.Code != 200 {
		t.Fatalf("OK: %d %s", rec.Code, rec.Body.String())
	}
	if got := sentToAgent(f)[sent:]; len(got) != 1 || !strings.Contains(got[0], "先灰度 10%") {
		t.Fatalf("to the agent: %q", got)
	}
	s.sweepWaitingDecisions(time.Now().Add(48 * time.Hour))
	if n := deadlinePushes(f, todo.ID); n != 0 {
		t.Fatalf("pushed %d times after it was answered", n)
	}
}

func TestWaitOver(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name    string
		ask     acceptAsk
		briefed bool
		over    bool
	}{
		{"just raised", acceptAsk{AskedAt: now.Add(-time.Hour), Deadline: now.Add(23 * time.Hour)}, true, false},
		{"the briefing is off", acceptAsk{AskedAt: now.Add(-time.Hour)}, false, true},
		{"a day old", acceptAsk{AskedAt: now.Add(-decisionWaitFor)}, true, true},
		{"12 hours to its deadline", acceptAsk{AskedAt: now.Add(-2 * time.Hour), Deadline: now.Add(12 * time.Hour)}, true, false},
		{"11 hours to its deadline", acceptAsk{AskedAt: now.Add(-2 * time.Hour), Deadline: now.Add(decisionWaitNotice)}, true, true},
		{"its deadline already handled", acceptAsk{AskedAt: now.Add(-2 * time.Hour), Deadline: now.Add(time.Minute), DueAt: now}, true, false},
	} {
		if got := waitOver(&c.ask, c.briefed, now) != ""; got != c.over {
			t.Errorf("%s: over %v, want %v", c.name, got, c.over)
		}
	}
}

// A run that was only sent has not reached the owner: the hold stays, through a turn that failed, and the decision
// is pushed after all. The run's finished push (with the `brief` item listing every row) ends it, and so does the
// briefing's Lark card for the rows it shows.
func TestOnlyABriefingThatReachedTheOwnerEndsTheWait(t *testing.T) {
	start := func(t *testing.T) (*Service, *fakeHub, Todo, *briefRun) {
		s, f, todo, _, _ := conclusionService(t, false)
		briefingOn(s, true)
		waitsForReview(t, s, f, todo.ID, "两种走法。")
		reviewTask(t, s, todo.ID, waitingDecision())
		run := &briefRun{facts: s.briefFactsAt(context.Background(), time.Now(), "morning"), label: "morning"}
		s.brief.mu.Lock()
		s.brief.byTask = map[string]*briefRun{"morning": run}
		s.brief.mu.Unlock()
		s.briefSent("morning", "conv-briefing", true)
		if a := askOf(s, todo.ID); !a.ForBriefing {
			t.Fatalf("sending the run ended the wait: %+v", a)
		}
		return s, f, todo, run
	}
	t.Run("the turn failed", func(t *testing.T) {
		s, f, todo, _ := start(t)
		if run := s.briefClaim("conv-briefing", "⏰ morning", true); run != nil {
			t.Fatal("a failed turn got the briefing card")
		}
		if got := s.briefPush("conv-briefing", i18n.ZhHans, false); got != "" || !askOf(s, todo.ID).ForBriefing {
			t.Fatalf("a turn that is not the run's ended the wait (%q)", got)
		}
		s.sweepWaitingDecisions(askOf(s, todo.ID).Deadline.Add(-decisionWaitNotice))
		if n := deadlinePushes(f, todo.ID); n != 1 {
			t.Fatalf("%d pushes after a briefing that failed, want 1", n)
		}
	})
	t.Run("the finished push", func(t *testing.T) {
		s, f, todo, _ := start(t)
		if got := s.briefPush("conv-briefing", i18n.ZhHans, true); got == "" {
			t.Fatal("no counts for the run's push")
		}
		if a := askOf(s, todo.ID); a.ForBriefing || !a.AcceptedAt.IsZero() {
			t.Fatalf("after the run's push: %+v", a)
		}
		s.sweepWaitingDecisions(time.Now().Add(48 * time.Hour))
		if n := deadlinePushes(f, todo.ID); n != 0 {
			t.Fatalf("pushed %d times after the briefing reached the owner", n)
		}
	})
	t.Run("the card", func(t *testing.T) {
		s, _, todo, run := start(t)
		s.briefAsked(run.facts, nil) // the card had no room for its row
		if !askOf(s, todo.ID).ForBriefing {
			t.Fatal("a card that did not show it ended the wait")
		}
		_, _, shown := s.briefCard(run.facts, run.label, "早上好")
		s.briefAsked(run.facts, shown)
		if a := askOf(s, todo.ID); a.ForBriefing || !a.AcceptedAt.IsZero() {
			t.Fatalf("after the card: shown %q, ask %+v", shown, a)
		}
	})
}
