package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The weekly recommended-setup block (setup_suggest.go). All ids are placeholders.

var setupNow = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC) // a Monday

func setupAt(daysAgo float64) time.Time {
	return setupNow.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
}

func setupEvent(kind string, daysAgo float64, detail map[string]any) db.WorkEvent {
	return db.WorkEvent{At: setupAt(daysAgo), Kind: kind, Detail: detail}
}

func setupCounted(ev setupEvidence) map[string]setupNumber {
	return setupCount(ev, setupNow.Add(-setupWindow), setupNow)
}

// boxScope puts every to-do in /code/app on box.
func boxScope(string) (string, string, bool) { return "box", "/code/app", true }

func TestSetupCountLeadersIsTheMainChatsSupervisionRounds(t *testing.T) {
	round := func(daysAgo float64, source string, main bool, seconds any) db.WorkEvent {
		return setupEvent(roundEvent, daysAgo, map[string]any{"source": source, "main": main, "seconds": seconds})
	}
	got := setupCounted(setupEvidence{events: []db.WorkEvent{
		round(1, proto.SourceSupervision, true, 120.0),
		round(2, proto.SourceSupervision, true, 60), // as recorded, before the store's JSON
		round(2, roundOwner, true, 500.0),           // the owner's own round
		round(2, proto.SourceFollowup, true, 500.0), // a scheduled round: no leader takes it
		round(3, proto.SourceSupervision, false, 90.0),
		round(8, proto.SourceSupervision, true, 90.0), // before the window
	}})
	if c := got[setupLeaders]; c.n != 2 || c.seconds != 180 {
		t.Fatalf("leaders: %+v", c)
	}
}

func TestSetupCountDelegateIsRecommendationsTheOwnerTook(t *testing.T) {
	passed := func(card string, daysAgo float64, kind, recommend string) db.WorkEvent {
		return setupEvent("ask_escalated", daysAgo, map[string]any{"card": card, "kind": kind, "recommend": recommend})
	}
	answer := func(card, choice string, daysAgo float64) db.TodoDecisionRow {
		return db.TodoDecisionRow{ID: "d_" + card + choice, TodoID: "todo1", Question: "Which cache?", Choice: choice,
			Source: db.DecisionCard, SourceMessage: card, CreatedAt: setupAt(daysAgo)}
	}
	got := setupCounted(setupEvidence{
		events: []db.WorkEvent{
			passed("q1", 2, proto.IxQuestion, "Redis"), passed("q2", 2, proto.IxQuestion, "Redis (recommended)"),
			passed("q3", 2, proto.IxQuestion, "Redis"), passed("q4", 2, proto.IxQuestion, ""),
			passed("q5", 2, proto.IxPermission, "Allow"), passed("q6", 12, proto.IxQuestion, "Redis"),
			passed("q7", 2, proto.IxQuestion, "Redis"),
		},
		decisions: []db.TodoDecisionRow{
			answer("q1", "Redis", 1.9), answer("q2", "redis", 1.9), // taken, the mark aside
			answer("q3", "Memory", 1.9),                             // answered another way
			answer("q4", "Redis", 1.9),                              // nothing was recommended
			answer("q5", "Allow", 1.9),                              // an approval, not a question
			answer("q6", "Redis", 11),                               // before the window
			answer("q7", "Redis", 1.9), answer("q7", "Memory", 1.8), // answered twice: not a plain take
			answer("q8", "Redis", 1.9), // never passed up by a supervisor
		},
	})
	if c := got[setupDelegate]; c.n != 2 {
		t.Fatalf("delegate: %+v", c)
	}
}

func TestSetupCountTriageReadsTheOwnersHandResumes(t *testing.T) {
	resumed := func(daysAgo float64, by, on string, more map[string]any) db.WorkEvent {
		detail := map[string]any{"by": by, "on": on, "phase": proto.WorkBlocked, "limit": false, "card": false}
		for k, v := range more {
			detail[k] = v
		}
		return setupEvent("resumed", daysAgo, detail)
	}
	got := setupCounted(setupEvidence{events: []db.WorkEvent{
		resumed(1, "user", proto.WaitUser, nil),                               // waited on the owner, no card
		resumed(1, "user", proto.WaitUser, map[string]any{"card": true}),      // a card was up: decide was on for it
		resumed(1, "user", proto.WaitExternal, map[string]any{"limit": true}), // a usage limit
		resumed(1, "user", proto.WaitExternal, map[string]any{"limit": true}),
		resumed(1, "user", proto.WaitExternal, nil), // waited on CI
		resumed(1, "user", proto.WaitAgent, nil),    // waited on another agent
		resumed(1, "user", proto.WaitAgent, nil),
		resumed(1, "hub", proto.WaitExternal, nil),   // the manager woke it
		resumed(1, "user", proto.WaitTransient, nil), // transient_retry's, on by default
		setupEvent("resumed", 1, map[string]any{"by": "user", "on": proto.WaitExternal, "phase": proto.WorkDone, "limit": false, "card": false}),
		setupEvent("resumed", 1, map[string]any{"by": "user", "on": proto.WaitExternal, "phase": proto.WorkBlocked}), // from before limit and card
		resumed(9, "user", proto.WaitExternal, nil), // before the window
	}})
	if got[setupDecide].n != 1 || got[setupAutoContinue].n != 2 || got[setupWake].n != 3 {
		t.Fatalf("triage: decide %+v auto_continue %+v wake %+v", got[setupDecide], got[setupAutoContinue], got[setupWake])
	}
}

func setupGoAhead(id, choice string, daysAgo float64) db.TodoDecisionRow {
	return db.TodoDecisionRow{ID: id, TodoID: "todo1", Question: "Merge the PR now?", Choice: choice, Source: db.DecisionCard,
		SourceMessage: "card_" + id, CreatedAt: setupAt(daysAgo)}
}

func TestSetupCountStandingIsRepeatedGoAheadsNoGrantCovers(t *testing.T) {
	rows := []db.TodoDecisionRow{setupGoAhead("d1", "Yes", 20), setupGoAhead("d2", "Yes", 5), setupGoAhead("d3", "Yes", 3), setupGoAhead("d4", "Yes", 1)}
	if c := setupCounted(setupEvidence{decisions: rows, scopeOf: boxScope})[setupStanding]; c.n != 3 {
		t.Fatalf("three repeats in the window: %+v", c)
	}
	// The first go-ahead there is not a repeat, even inside the window.
	if c := setupCounted(setupEvidence{decisions: rows[1:], scopeOf: boxScope})[setupStanding]; c.n != 2 {
		t.Fatalf("the first one counted: %+v", c)
	}
	// A no to the same step: the owner does not always approve it.
	refused := append(slices.Clone(rows), setupGoAhead("d5", "No", 10))
	slices.SortFunc(refused, func(a, b db.TodoDecisionRow) int { return a.CreatedAt.Compare(b.CreatedAt) })
	if c := setupCounted(setupEvidence{decisions: refused, scopeOf: boxScope})[setupStanding]; c.n != 0 {
		t.Fatalf("counted after a no: %+v", c)
	}
	// A grant covers the directory already.
	grants := StandingAuthorizations{Authorizations: []StandingAuthorization{{MachineID: "box", Directory: "/code/app", Actions: []string{"merge"}}}}
	if c := setupCounted(setupEvidence{decisions: rows, scopeOf: boxScope, grants: grants})[setupStanding]; c.n != 0 {
		t.Fatalf("counted what a grant covers: %+v", c)
	}
	// What a rule never covers, and a decision typed in by hand, are no go-aheads.
	never := []db.TodoDecisionRow{
		{ID: "n1", TodoID: "todo1", Question: "Deploy-production now?", Choice: "Yes", Source: db.DecisionCard, CreatedAt: setupAt(5)},
		{ID: "n2", TodoID: "todo1", Question: "Deploy-production now?", Choice: "Yes", Source: db.DecisionCard, CreatedAt: setupAt(4)},
		{ID: "n3", TodoID: "todo1", Question: "Merge the PR now?", Choice: "Yes", Source: db.DecisionManual, CreatedAt: setupAt(3)},
		{ID: "n4", TodoID: "todo1", Question: "Merge the PR now?", Choice: "Yes", Source: db.DecisionManual, CreatedAt: setupAt(2)},
	}
	if c := setupCounted(setupEvidence{decisions: never, scopeOf: boxScope})[setupStanding]; c.n != 0 {
		t.Fatalf("counted what no rule covers: %+v", c)
	}
	// A to-do that works nowhere a grant may name.
	nowhere := func(string) (string, string, bool) { return "", "", false }
	if c := setupCounted(setupEvidence{decisions: rows, scopeOf: nowhere})[setupStanding]; c.n != 0 {
		t.Fatalf("counted without a scope: %+v", c)
	}
}

func setupPlan(id, state string, daysAgo float64, owner bool) *TaskPlan {
	return &TaskPlan{ID: id, Conversation: "conv1", State: state, CreatedAt: setupAt(daysAgo), UpdatedAt: setupAt(daysAgo),
		Tasks: []PlanTask{{Title: "t", Machine: "box", Cwd: "/code/app", OwnerStarted: owner}}}
}

func TestSetupCountTrustIsStartsOfAKindAlreadyStarted(t *testing.T) {
	plans := []*TaskPlan{setupPlan("p1", planStarted, 20, true), setupPlan("p2", planStarted, 5, true), setupPlan("p3", planStarted, 2, true)}
	if c := setupCounted(setupEvidence{plans: plans})[setupTrust]; c.n != 2 {
		t.Fatalf("two repeats in the window: %+v", c)
	}
	// The first start of a kind is not a repeat; a start the hub made without asking is no answer either way.
	fresh := []*TaskPlan{setupPlan("p0", planStarted, 6, false), setupPlan("p2", planStarted, 5, true), setupPlan("p3", planStarted, 2, true)}
	if c := setupCounted(setupEvidence{plans: fresh})[setupTrust]; c.n != 1 {
		t.Fatalf("first start or an automatic one counted: %+v", c)
	}
	// A kind the owner dismissed a plan of.
	if c := setupCounted(setupEvidence{plans: append(slices.Clone(plans), setupPlan("p4", planDismissed, 1, false))})[setupTrust]; c.n != 0 {
		t.Fatalf("counted a kind the owner dismissed: %+v", c)
	}
	// A kind tasks.trust already holds.
	trusted := map[string]bool{trustKey(PlanTask{Machine: "box", Cwd: "/code/app"}): true}
	if c := setupCounted(setupEvidence{plans: plans, trusted: trusted})[setupTrust]; c.n != 0 {
		t.Fatalf("counted a trusted kind: %+v", c)
	}
	// A health plan waits for Start whatever is trusted.
	if c := setupCounted(setupEvidence{plans: plans, health: func(string) bool { return true }})[setupTrust]; c.n != 0 {
		t.Fatalf("counted health plans: %+v", c)
	}
	// A plan with a kind never started before is not one that would have started by itself.
	mixed := setupPlan("p5", planStarted, 1, true)
	mixed.Tasks = append(mixed.Tasks, PlanTask{Title: "u", Machine: "box", Cwd: "/code/other", OwnerStarted: true})
	// A read-only task never waits for Start: it neither counts nor holds a plan back.
	readonly := setupPlan("p6", planStarted, 1, true)
	readonly.Tasks = append(readonly.Tasks, PlanTask{Title: "r", Machine: "box", Cwd: "/code/other", Readonly: true})
	if c := setupCounted(setupEvidence{plans: append(slices.Clone(plans), mixed, readonly)})[setupTrust]; c.n != 3 {
		t.Fatalf("mixed and read-only plans: %+v", c)
	}
}

func setupReviewed(id string, daysAgo float64, change func(*Todo)) Todo {
	t := Todo{ID: id, Title: id, Origin: "conv1", Delivery: &TodoDelivery{State: deliveryLive},
		Supervision: &Supervision{Supervisor: "conv1", DoneWhen: []string{"tests pass", "deployed"}, State: reviewAccepted,
			Reviews: []TaskReview{{At: setupAt(daysAgo), Verdict: reviewAccepted, Checks: []ReviewCheck{
				{Criterion: "tests pass", Met: true, Evidence: "CI run"}, {Criterion: "deployed", Met: true, Evidence: "version page"}}}}}}
	if change != nil {
		change(&t)
	}
	return t
}

func TestSetupCountSampleIsFirstReviewsTheJudgeCouldHaveMade(t *testing.T) {
	reopened := setupAt(1)
	got := setupCounted(setupEvidence{todos: []Todo{
		setupReviewed("t1", 1, nil),
		setupReviewed("t2", 6, nil),
		setupReviewed("t3", 9, nil), // before the window
		setupReviewed("t4", 1, func(t *Todo) { t.Supervision.Reviews[0].Checks[1].Met = false }),
		setupReviewed("t5", 1, func(t *Todo) { t.Supervision.Reviews[0].Checks[1].Evidence = " " }),
		setupReviewed("t6", 1, func(t *Todo) { t.Supervision.Reviews[0].Checks = t.Supervision.Reviews[0].Checks[:1] }),
		setupReviewed("t7", 1, func(t *Todo) { t.Supervision.Reviews[0].UserCheck = true }),
		setupReviewed("t8", 1, func(t *Todo) { t.Supervision.Reviews[0].By = reviewerJudge }), // the judge closed it already
		setupReviewed("t9", 1, func(t *Todo) { t.Delivery = &TodoDelivery{State: deliveryPR} }),
		setupReviewed("t10", 1, func(t *Todo) { t.Delivery = nil }),
		setupReviewed("t11", 1, func(t *Todo) { t.ReopenedAt = &reopened }),
		setupReviewed("t12", 1, func(t *Todo) { // accepted after a rework: the judge closes only a first round
			t.Supervision.Round = 1
			t.Supervision.Reviews = append([]TaskReview{{At: setupAt(2), Verdict: reviewRework}}, t.Supervision.Reviews...)
		}),
		setupReviewed("t13", 1, func(t *Todo) { t.Supervision.DoneWhen, t.Supervision.Reviews[0].Checks = nil, nil }),
		{ID: "t14", Title: "unsupervised", Origin: "conv1"},
	}})
	if c := got[setupSample]; c.n != 2 {
		t.Fatalf("review sample: %+v", c)
	}
}

func TestSetupCountIndependentReviewIsReworksAfterTheJudgesDone(t *testing.T) {
	got := setupCounted(setupEvidence{events: []db.WorkEvent{
		setupEvent(reviewReworkEvent, 1, map[string]any{"todo": "t1", "judged_done": true}),
		setupEvent(reviewReworkEvent, 2, map[string]any{"todo": "t2", "judged_done": true}),
		setupEvent(reviewReworkEvent, 2, map[string]any{"todo": "t3", "judged_done": false}), // nothing finished to review
		setupEvent(reviewReworkEvent, 8, map[string]any{"todo": "t4", "judged_done": true}),  // before the window
	}})
	if c := got[setupReview]; c.n != 2 {
		t.Fatalf("independent review: %+v", c)
	}
}

// A mechanism that is on, or whose count is under its threshold, is never named.
func TestSetupSuggestsOnlyWhatIsOffAndCounted(t *testing.T) {
	off := setupState{samplePercent: 100, triage: TriageSwitches{Enabled: true}, triageKnown: true}
	for _, id := range setupOrder {
		enough := setupNumber{n: setupMin}
		if id == setupLeaders {
			enough.n = setupLeaderMin
		}
		if !off.suggests(id, enough) {
			t.Errorf("%s: not suggested at its threshold", id)
		}
		if off.suggests(id, setupNumber{n: enough.n - 1}) {
			t.Errorf("%s: suggested under its threshold", id)
		}
	}
	on := setupState{leaders: true, delegate: true, review: true, samplePercent: 20, triageKnown: true,
		triage: TriageSwitches{Enabled: true, Decide: true, AutoContinue: true, Wake: true}}
	many := setupNumber{n: 100}
	for _, id := range []string{setupLeaders, setupDelegate, setupReview, setupSample, setupDecide, setupAutoContinue, setupWake} {
		if on.suggests(id, many) {
			t.Errorf("%s: suggested while it is on", id)
		}
	}
	// The triage's actions do nothing without the triage, and are not guessed at when its switches cannot be read.
	for _, st := range []setupState{{triageKnown: true}, {triage: TriageSwitches{Enabled: true}}} {
		for _, id := range []string{setupDecide, setupAutoContinue, setupWake} {
			if st.suggests(id, many) {
				t.Errorf("%s: suggested with %+v", id, st)
			}
		}
	}
	if off.suggests("tier_down", many) {
		t.Error("a mechanism that is not counted was suggested")
	}
}

// setupDelegateEvidence gives ev n question cards from the hours before now, answered with their supervisor's recommendation.
func setupDelegateEvidence(ev *fakeRuleEvidence, now time.Time, n int) {
	for i := range n {
		card := fmt.Sprintf("q%d_%d", now.Unix(), i)
		at := now.Add(-time.Duration(i+1) * time.Hour)
		ev.events = append(ev.events, db.WorkEvent{At: at.Add(-time.Minute), Kind: "ask_escalated",
			Detail: map[string]any{"card": card, "kind": proto.IxQuestion, "recommend": "Retry"}})
		ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: "d" + card, TodoID: "todo1", Question: "What now?", Choice: "Retry",
			Source: db.DecisionCard, SourceMessage: card, CreatedAt: at})
	}
	slices.SortFunc(ev.decisions, func(a, b db.TodoDecisionRow) int { return a.CreatedAt.Compare(b.CreatedAt) })
}

func setupIDs(b *setupBlock) []string {
	var out []string
	if b != nil {
		for _, sg := range b.Suggestions {
			out = append(out, sg.ID)
		}
	}
	return out
}

func setupBookOf(t *testing.T, s *Service) setupBook {
	t.Helper()
	var book setupBook
	if _, err := s.store().Setting(context.Background(), setupKey, &book); err != nil {
		t.Fatal(err)
	}
	return book
}

// The block goes with the first briefing of a week that reaches the owner, and a mechanism it named is not named
// again for a month.
func TestSetupBlockOncePerWeekAndEachSuggestionOnceAMonth(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	loc := s.location(ctx)
	monday := time.Now()
	setupDelegateEvidence(ev, monday, 3)

	b := s.setupFor(ctx, monday, loc, i18n.En, testWeb)
	if got := setupIDs(b); !slices.Equal(got, []string{setupDelegate}) {
		t.Fatalf("first briefing of the week: %v", got)
	}
	sg := b.Suggestions[0]
	if sg.Count != 3 || sg.Setting != "agents.delegate" || !strings.Contains(sg.Text, "3 times") || !strings.Contains(sg.Text, "Cost:") ||
		!strings.Contains(sg.Text, "agents.delegate") || sg.Link != "" {
		t.Fatalf("suggestion: %+v", sg)
	}
	// Computing it marks nothing: a run that never reaches the owner leaves the week's block for the next briefing.
	if book := setupBookOf(t, s); book.Week != "" || len(book.Shown) != 0 {
		t.Fatalf("marked before the briefing reached the owner: %+v", book)
	}
	if got := setupIDs(s.setupFor(ctx, monday.Add(time.Hour), loc, i18n.En, testWeb)); !slices.Equal(got, []string{setupDelegate}) {
		t.Fatalf("the retry of a run that failed: %v", got)
	}

	s.setupTold(b, monday)
	s.setupTold(b, monday.Add(time.Minute)) // the card and the push both say so: one mark
	book := setupBookOf(t, s)
	if book.Week != setupWeek(monday, loc) || !book.Shown[setupDelegate].Equal(monday.UTC()) {
		t.Fatalf("book after the briefing: %+v", book)
	}
	if again := s.setupFor(ctx, monday.Add(10*time.Hour), loc, i18n.En, testWeb); again != nil && again.Week == b.Week {
		t.Fatalf("a second block in the same week: %+v", again)
	}

	// The weeks after: the count still holds, but the mechanism was named within the month.
	for _, weeks := range []int{1, 2, 3, 4} {
		at := monday.Add(time.Duration(weeks) * 7 * 24 * time.Hour)
		setupDelegateEvidence(ev, at, 3)
		next := s.setupFor(ctx, at, loc, i18n.En, testWeb)
		if next == nil || len(next.Suggestions) != 0 {
			t.Fatalf("week +%d: %+v", weeks, next)
		}
		s.setupTold(next, at) // a block that names nothing still takes the week
		if got := setupBookOf(t, s); got.Week != setupWeek(at, loc) || !got.Shown[setupDelegate].Equal(monday.UTC()) {
			t.Fatalf("week +%d book: %+v", weeks, got)
		}
	}
	later := monday.Add(5 * 7 * 24 * time.Hour)
	setupDelegateEvidence(ev, later, 3)
	if got := setupIDs(s.setupFor(ctx, later, loc, i18n.En, testWeb)); !slices.Equal(got, []string{setupDelegate}) {
		t.Fatalf("after a month: %v", got)
	}
}

// The mechanisms that are already on are left out of the block, whatever was counted.
func TestSetupBlockLeavesOutWhatIsOn(t *testing.T) {
	ctx := context.Background()
	s, f := newTaskServiceOn(t, ruleStore{&memStore{}})
	ev := &fakeRuleEvidence{}
	s.rules.evidence = ev
	now := time.Now()
	setupDelegateEvidence(ev, now, 4)
	for i := range setupLeaderMin {
		ev.events = append(ev.events, db.WorkEvent{At: now.Add(-time.Duration(i+1) * time.Hour), Kind: roundEvent,
			Detail: map[string]any{"source": proto.SourceSupervision, "main": true, "seconds": 90.0}})
	}
	for i := range 3 {
		at := now.Add(-time.Duration(i+1) * time.Hour)
		ev.events = append(ev.events,
			db.WorkEvent{At: at, Kind: reviewReworkEvent, Detail: map[string]any{"todo": "t", "judged_done": true}},
			db.WorkEvent{At: at, Kind: "resumed", Detail: map[string]any{"by": "user", "on": proto.WaitExternal, "phase": proto.WorkBlocked, "limit": false, "card": false}})
	}
	loc := s.location(ctx)

	// Everything off, the triage itself off too: its actions cannot be counted on, the rest is named.
	if got := setupIDs(s.setupFor(ctx, now, loc, i18n.En, testWeb)); !slices.Equal(got, []string{setupDelegate, setupLeaders, setupReview}) {
		t.Fatalf("all off: %v", got)
	}
	b := s.setupFor(ctx, now, loc, i18n.En, testWeb)
	if sg := b.Suggestions[1]; sg.Count != setupLeaderMin || sg.Minutes != 15 || !strings.Contains(sg.Text, "15 min") {
		t.Fatalf("leaders: %+v", sg)
	}

	// With the triage on and wake off, the hand resumes name wake, with its settings page.
	if err := s.putTriageSwitches(ctx, TriageSwitches{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	b = s.setupFor(ctx, now, loc, i18n.En, testWeb)
	if got := setupIDs(b); !slices.Equal(got, []string{setupDelegate, setupWake, setupLeaders, setupReview}) {
		t.Fatalf("triage on: %v", got)
	}
	if sg := b.Suggestions[1]; sg.Setting != "agents.triage.wake" || sg.Link != testWeb+"/settings?tab=agents" {
		t.Fatalf("wake: %+v", sg)
	}
	if got := s.setupFor(ctx, now, loc, i18n.En, ""); got.Suggestions[1].Link != "" {
		t.Fatalf("a link without a public address: %+v", got.Suggestions[1])
	}

	// Each one turned on drops out.
	on := true
	f.hello.Agents.Delegate.Enabled = &on
	f.hello.Agents.Review.Launcher = "reviewer"
	setSettings(t, s, func(v *Settings) { v.Leaders = []string{"lead1"} })
	if err := s.putTriageSwitches(ctx, TriageSwitches{Enabled: true, Wake: true}); err != nil {
		t.Fatal(err)
	}
	if got := s.setupFor(ctx, now, loc, i18n.En, testWeb); got == nil || len(got.Suggestions) != 0 {
		t.Fatalf("everything on: %+v", got)
	}
}

// The block never turns anything on: computing it writes nothing, and telling it writes its own book only.
func TestSetupBlockSwitchesNothingOn(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	now := time.Now()
	setupDelegateEvidence(ev, now, 5)
	if err := s.putTriageSwitches(ctx, TriageSwitches{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		ev.events = append(ev.events, db.WorkEvent{At: now.Add(-time.Duration(i+1) * time.Hour), Kind: "resumed",
			Detail: map[string]any{"by": "user", "on": proto.WaitExternal, "phase": proto.WorkBlocked, "limit": true, "card": false}})
	}
	store := s.store().(ruleStore)
	snapshot := func() map[string]string {
		store.mu.Lock()
		defer store.mu.Unlock()
		out := map[string]string{}
		for k, v := range store.vals {
			if k != setupKey {
				out[k] = string(v)
			}
		}
		return out
	}
	before, puts := snapshot(), store.puts

	b := s.setupFor(ctx, now, s.location(ctx), i18n.En, testWeb)
	if got := setupIDs(b); !slices.Equal(got, []string{setupDelegate, setupAutoContinue}) {
		t.Fatalf("block: %v", got)
	}
	if store.puts != puts {
		t.Fatalf("computing the block wrote %d settings", store.puts-puts)
	}
	s.setupTold(b, now)
	if store.puts != puts+1 {
		t.Fatalf("telling the block wrote %d settings, want its own book only", store.puts-puts)
	}
	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("settings changed: %v → %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("setting %s changed: %s → %s", k, v, after[k])
		}
	}
	if sw := s.triageSwitches(ctx); sw.AutoContinue || sw.Wake || sw.Decide {
		t.Fatalf("a triage switch was turned on: %+v", sw)
	}
	if s.hello(ctx).Agents.Delegate.On() || len(s.settings(ctx).Leaders) != 0 || s.reviewSamplePercent(ctx) != 100 {
		t.Fatal("a mechanism was turned on")
	}
}

// The resumed row says what the block counts by: whether the stop was a usage limit and whether a card was up, and a
// resume the owner made by hand reads "user".
func TestResumedRowSaysLimitAndCard(t *testing.T) {
	s, _ := workService(t)
	resets := time.Now().Add(time.Hour)
	turn := time.Now().Add(-10 * time.Minute)
	s.putWork(context.Background(), db.SessionWork{MachineID: "box", SessionID: "s1", TurnAt: turn,
		Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, ResetsAt: &resets}})
	s.putWork(context.Background(), db.SessionWork{MachineID: "box", SessionID: "s2", TurnAt: turn,
		Work: proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Wait: "CI"}})
	s.workStarted(context.Background(), "box", "s1", time.Now())
	s.workStarted(context.Background(), "box", "s2", time.Now())
	rows := map[string]map[string]any{}
	var events []db.WorkEvent
	for _, e := range testEvents(s) {
		if e.Kind == "resumed" {
			rows[e.SessionID] = e.Detail
		}
		e.At = setupAt(1)
		events = append(events, e)
	}
	if d := rows["s1"]; d["limit"] != true || d["card"] != false || d["by"] != "user" || d["on"] != proto.WaitExternal {
		t.Fatalf("a usage limit's resume: %v", d)
	}
	if d := rows["s2"]; d["limit"] != false || d["card"] != false || d["by"] != "user" {
		t.Fatalf("a wait's resume: %v", d)
	}
	if got := setupCounted(setupEvidence{events: events}); got[setupAutoContinue].n != 1 || got[setupWake].n != 1 {
		t.Fatalf("counted from the rows as recorded: %+v", got)
	}
}

// Every mechanism has its line in the three languages, with its number, its cost and where it is turned on.
func TestSetupLinesAreLocalized(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		for _, key := range []string{"heading", "intro"} {
			if !i18n.Has("assistant-setup_suggest."+key) || i18n.T(l, "assistant-setup_suggest."+key) == "" {
				t.Errorf("%s: no %s", l, key)
			}
		}
		seen := map[string]bool{}
		for _, id := range setupOrder {
			if setupSettings[id] == "" {
				t.Errorf("%s: no setting", id)
			}
			sg := setupWord(l, testWeb, id, setupNumber{n: 37, seconds: 41 * 60})
			if !strings.Contains(sg.Text, "37") || !strings.Contains(sg.Text, setupSettings[id]) || strings.Contains(sg.Text, "{") {
				t.Errorf("%s %s: %q", l, id, sg.Text)
			}
			if id == setupLeaders && !strings.Contains(sg.Text, "41") {
				t.Errorf("%s leaders: no minutes in %q", l, sg.Text)
			}
			if seen[sg.Text] {
				t.Errorf("%s %s: the same line as another mechanism", l, id)
			}
			seen[sg.Text] = true
		}
	}
}

// The block is on the Lark card, in the `brief` item and in the model's facts, word for word; a briefing without one
// has none of the three.
func TestSetupBlockIsOnTheCardTheItemAndTheFacts(t *testing.T) {
	e := newBriefEnv(t)
	now := e.clk.Now()
	f := e.s.briefFactsAt(context.Background(), now, "")
	plainCard, _, _ := e.s.briefCard(f, "Morning", "")
	if strings.Contains(string(plainCard.JSON()), i18n.T(f.l, "assistant-setup_suggest.heading")) || f.body(e.s, "Morning").Setup != nil ||
		strings.Contains(f.modelText(), "建议的设置") {
		t.Fatal("a briefing without a block shows one")
	}

	f.Setup = &setupBlock{Week: "2026-W46", Suggestions: []setupSuggestion{
		setupWord(f.l, testWeb, setupDelegate, setupNumber{n: 4}), setupWord(f.l, testWeb, setupWake, setupNumber{n: 3})}}
	card, fallback, _ := e.s.briefCard(f, "Morning", "")
	raw := string(card.JSON())
	if !strings.Contains(raw, i18n.T(f.l, "assistant-setup_suggest.heading")) || !strings.Contains(fallback, f.Setup.Suggestions[0].Text) ||
		!strings.Contains(fallback, f.Setup.Suggestions[1].Text) || !strings.Contains(fallback, i18n.T(f.l, "assistant-setup_suggest.intro")) {
		t.Fatalf("card without the block: %s\n%s", raw, fallback)
	}
	if !strings.Contains(raw, testWeb+"/settings?tab=agents") {
		t.Fatalf("card without the settings link: %s", raw)
	}
	body := f.body(e.s, "Morning")
	if len(body.Setup) != 2 || body.Setup[0].ID != setupDelegate || body.Setup[0].Count != 4 || body.Setup[1].Link == "" {
		t.Fatalf("brief item: %+v", body.Setup)
	}
	text := f.modelText()
	if !strings.Contains(text, f.Setup.Suggestions[0].Text) || !strings.Contains(text, f.Setup.Suggestions[1].Text) || !strings.Contains(text, "不要替用户打开") {
		t.Fatalf("model facts: %s", text)
	}
	// A block that names nothing adds no section anywhere.
	f.Setup = &setupBlock{Week: "2026-W46"}
	empty, _, _ := e.s.briefCard(f, "Morning", "")
	if strings.Contains(string(empty.JSON()), i18n.T(f.l, "assistant-setup_suggest.heading")) || f.body(e.s, "Morning").Setup != nil ||
		strings.Contains(f.modelText(), "建议的设置") {
		t.Fatal("an empty block shows a section")
	}
}

// The run's finished push is when its briefing reached the owner: the week and the mechanisms are marked then.
func TestSetupBlockIsMarkedWhenTheBriefingReachesTheOwner(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	now := time.Now()
	setupDelegateEvidence(ev, now, 3)
	b := s.setupFor(ctx, now, s.location(ctx), i18n.En, testWeb)
	if len(setupIDs(b)) != 1 {
		t.Fatalf("block: %+v", b)
	}
	s.brief.bySession = map[string]*briefRun{"sess1": {facts: &briefFacts{At: now, l: i18n.En, loc: s.location(ctx), Setup: b}}}
	if s.briefPush("sess1", i18n.En, false) != "" { // a message the owner sent into the conversation later: no briefing
		t.Fatal("a turn that is not the run's was pushed as a briefing")
	}
	if book := setupBookOf(t, s); book.Week != "" {
		t.Fatalf("marked by a turn that is not the run's: %+v", book)
	}
	if s.briefPush("sess1", i18n.En, true) == "" {
		t.Fatal("no push text")
	}
	if book := setupBookOf(t, s); book.Week != b.Week || book.Shown[setupDelegate].IsZero() {
		t.Fatalf("not marked when the briefing reached the owner: %+v", book)
	}
}

func TestGoldenBriefFactsSetup(t *testing.T) {
	lines := []string{
		setupWord(i18n.ZhHans, "", setupDelegate, setupNumber{n: 4}).Text,
		setupWord(i18n.ZhHans, "", setupLeaders, setupNumber{n: 12, seconds: 31 * 60}).Text,
	}
	golden(t, "routine_facts.setup", prompts.RenderBriefFacts(prompts.BriefFacts{Hours: 12, Setup: lines}))
	golden(t, "routine_facts.setup_card", prompts.RenderBriefFacts(prompts.BriefFacts{Card: true, Hours: 12, Setup: lines}))
}
