package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The recommended setup, said once (#5085, docs/assistant/assistant.md "Recommended setup"; step R3, section 4c of
// docs/design/proposals/main-chat-availability.md). The mechanisms that cut asks and background rounds ship off or
// empty because each spends tokens or acts for the owner. This block says, from the install's own records, which of
// them would have removed how many of the last setupWindow's asks or background rounds, with what it costs and where
// it is turned on.
//
//   - Computed: every number is counted here from what is recorded anyway (the availability metrics' round events,
//     the triage's resumed events and their `hand`, review_rework, ask_escalated and owner_decided in session_work_events; the
//     decisions kept on to-dos; the plans; the supervision ledgers). No model writes or changes a number, and there
//     is no table of its own.
//   - Conservative: a count takes only what the mechanism certainly covers (setupCount says what, per mechanism), a
//     mechanism that is on is never named, and a count under its threshold is not said.
//   - Once a month: a mechanism that was named is not named again for setupQuietFor (setting setupKey).
//   - Never acted on: nothing here writes any setting but setupKey; the owner turns a mechanism on themselves.
//
// There is no weekly routine: the block goes with the first briefing (a routine with facts on, briefing.go) that
// reaches the owner in a week of the owner's time zone, Monday first. Reached: the run's turn ended normally, with its
// `brief` item, which carries the block, in the conversation (briefReached), or its Lark card went out (briefAsked).
const (
	// setupWindow is "last week": the time before the run the counts are over.
	setupWindow = 7 * 24 * time.Hour
	// setupLookback is how far back "the owner did this before" looks (the work events are kept 30 days).
	setupLookback = 30 * 24 * time.Hour
	// setupMin is the count under which a mechanism is not named; setupLeaderMin is the same for leader
	// conversations, counted in rounds of the main conversation.
	setupMin       = 3
	setupLeaderMin = 10
	// setupQuietFor is how long a mechanism that was named is not named again.
	setupQuietFor = 30 * 24 * time.Hour

	setupKey = "assistant.setup_suggestions"
)

// The mechanisms, by the id the `brief` item carries.
const (
	setupLeaders      = "leaders"
	setupDelegate     = "delegate"
	setupDecide       = "triage_decide"
	setupAutoContinue = "triage_auto_continue"
	setupWake         = "triage_wake"
	setupStanding     = "standing_authorizations"
	setupTrust        = "task_trust"
	setupSample       = "review_sample"
	setupReview       = "independent_review"
)

// setupOrder is the order the block names them in: what removes asks first, then what removes background rounds.
var setupOrder = []string{setupDelegate, setupStanding, setupTrust, setupDecide, setupAutoContinue, setupWake,
	setupLeaders, setupSample, setupReview}

// setupSettings is the setting each mechanism is turned on with.
var setupSettings = map[string]string{
	setupLeaders: "assistant.leaders", setupDelegate: ruleDelegateSetting,
	setupDecide: triageSettingKey + ".decide", setupAutoContinue: triageSettingKey + ".auto_continue", setupWake: triageSettingKey + ".wake",
	setupStanding: standingAuthorizationsKey, setupTrust: trustSetting,
	setupSample: "assistant.review_sample_percent", setupReview: "agents.review.launcher",
}

// setupPages is the web app's settings page a mechanism is on, for those that have one: the assistant's own page
// (web/src/views/settings/nav.ts, tab persona) holds Session triage, the standing authorizations and the remembered
// kinds of task. The others are named in their line (a conversation's menu, asking Persona, the configuration file).
var setupPages = map[string]string{
	setupDecide: "/settings?tab=persona", setupAutoContinue: "/settings?tab=persona", setupWake: "/settings?tab=persona",
	setupStanding: "/settings?tab=persona", setupTrust: "/settings?tab=persona",
}

// setupBook is the setting setupKey: the week whose first briefing reached the owner, and when each mechanism was
// last named.
type setupBook struct {
	Week  string               `json:"week"`
	Shown map[string]time.Time `json:"shown"`
}

// setupSuggestion is one mechanism named in the block (docs/protocol/protocol.md, the `brief` item's `setup`).
type setupSuggestion struct {
	ID      string `json:"id"`
	Setting string `json:"setting"`
	// Count is what the mechanism would have removed (setupCount); Minutes, for leaders, the time those rounds took.
	Count   int    `json:"count"`
	Minutes int    `json:"minutes,omitempty"`
	Text    string `json:"text"`           // the whole line, in the bot's language
	Link    string `json:"link,omitempty"` // its settings page in the web app, when it has one and the hub a public address
}

// setupBlock is the block of one run: the week it is the first briefing of, and what it names (possibly nothing).
type setupBlock struct {
	Week        string
	Suggestions []setupSuggestion
}

// lines are the block's lines as the model is given them.
func (b *setupBlock) lines() []string {
	if b == nil {
		return nil
	}
	out := make([]string, 0, len(b.Suggestions))
	for _, sg := range b.Suggestions {
		out = append(out, sg.Text)
	}
	return out
}

// setupWeek names the week of now in loc: its ISO year and week (Monday first).
func setupWeek(now time.Time, loc *time.Location) string {
	y, w := now.In(loc).ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// ---- counting ----

// setupNumber is one mechanism's count: n things, and for rounds the seconds they took.
type setupNumber struct {
	n       int
	seconds float64
}

// setupEvidence is what the counts are read from: the work events and decisions since the lookback (oldest first),
// the plans made since (oldest first) and the to-dos as they are.
type setupEvidence struct {
	events    []db.WorkEvent
	decisions []db.TodoDecisionRow
	plans     []*TaskPlan
	todos     []Todo
	// scopeOf says where a to-do works, as a standing authorization names it (ruleTodoScope); health: the plan's
	// conversation is in the health space, whose plans wait for Start whatever is trusted.
	scopeOf func(todo string) (machineID, directory string, ok bool)
	health  func(conversation string) bool
	// What is already granted: a kind tasks.trust holds or a scope a standing authorization covers is not counted.
	trusted map[string]bool
	grants  StandingAuthorizations
}

// setupCount counts, per mechanism, what it would have removed in [since, now). Each definition takes only what the
// mechanism certainly covers:
//
//   - leaders: the main conversation's rounds that a supervision message started (the round events of
//     availability.go), and their time: a leader conversation supervises its direction's tasks in its own rounds.
//   - delegate: question cards a supervisor passed up with a recommendation (ask_escalated) that the owner answered
//     once, with exactly that recommendation. Only a leader conversation's supervisor passes a question up
//     (ask_route.go), so without leader conversations this is always 0.
//   - triage_decide, triage_auto_continue, triage_wake: stops that only that switch, being off, kept the triage from
//     going on with (actOn: a stop on the owner that would have been a card; a usage limit whose CLI does not go on by
//     itself; a named wait with no risky next step), and that then went on by hand: with no message of this manager's
//     before the turn, and not by the agent itself (a resumed event's hand, workStartedObserved).
//   - standing_authorizations: the owner's go-aheads for merge, push or deploy-test (ruleAction) in a directory no
//     grant covers, where they had given the same go-ahead there before within setupLookback and never said no to it.
//   - task_trust: plans made in the window that the owner started, every startable kind of which (trustKey) they had
//     started before within setupLookback, never dismissed, and tasks.trust does not hold.
//   - review_sample: supervised tasks whose first and only review, in the window, was the supervisor's own accepted
//     with every done_when criterion met with evidence, no user_check, the work live, and never reopened: what the
//     done judge closes itself below 100 percent.
//   - independent_review: reworks a supervisor sent after the judge had called the work done (review_rework,
//     judged_done).
func setupCount(ev setupEvidence, since, now time.Time) map[string]setupNumber {
	out := map[string]setupNumber{}
	add := func(id string, seconds float64) {
		c := out[id]
		c.n++
		c.seconds += seconds
		out[id] = c
	}
	in := func(at time.Time) bool { return !at.Before(since) && at.Before(now) }

	for _, e := range ev.events {
		if !in(e.At) {
			continue
		}
		str := func(k string) string { v, _ := e.Detail[k].(string); return v }
		flag := func(k string) bool { v, _ := e.Detail[k].(bool); return v }
		num := func(k string) float64 {
			switch v := e.Detail[k].(type) {
			case float64:
				return v
			case int: // as recorded, before the store's JSON
				return float64(v)
			}
			return 0
		}
		switch e.Kind {
		case roundEvent:
			if flag("main") && str("source") == proto.SourceSupervision {
				add(setupLeaders, num("seconds"))
			}
		case "resumed":
			switch str("hand") { // a row without it does not say the stop was one a switch would have gone on with
			case handAutoContinue:
				add(setupAutoContinue, 0)
			case handDecide:
				add(setupDecide, 0)
			case handWake:
				add(setupWake, 0)
			}
		case reviewReworkEvent:
			if flag("judged_done") {
				add(setupReview, 0)
			}
		}
	}

	passed, decided := ruleEscalations(ev.events), ruleOwnerDecisions(ev.events)
	// Delegate: the answers by passed-up card; standing: the go-aheads by scope, in the order they were given.
	answers := map[string][]db.TodoDecisionRow{}
	type goAheads struct {
		machineID, directory, action string
		at                           []time.Time
		refused                      bool
	}
	scopes := map[string]*goAheads{}
	for _, row := range ev.decisions {
		if !ruleOwnAnswer(row, passed, decided) {
			continue
		}
		if e, up := passed[row.SourceMessage]; up && row.Source == db.DecisionCard && e.recommend != "" {
			answers[row.SourceMessage] = append(answers[row.SourceMessage], row)
		}
		action, yes, ok := ruleAction(row.Question, row.Choice)
		if !ok || ev.scopeOf == nil {
			continue
		}
		machineID, directory, ok := ev.scopeOf(row.TodoID)
		if !ok {
			continue
		}
		k := ruleStandingScope(machineID, directory, action)
		if scopes[k] == nil {
			scopes[k] = &goAheads{machineID: machineID, directory: directory, action: action}
		}
		if yes {
			scopes[k].at = append(scopes[k].at, row.CreatedAt)
		} else {
			scopes[k].refused = true
		}
	}
	for card, rows := range answers {
		if len(rows) == 1 && in(rows[0].CreatedAt) && ruleSameAnswer(rows[0].Choice, passed[card].recommend) {
			add(setupDelegate, 0)
		}
	}
	for _, g := range scopes {
		if g.refused || ruleCovered(ev.grants, g.machineID, g.directory, g.action) {
			continue
		}
		for i, at := range g.at {
			if i > 0 && in(at) { // the first one in the lookback is not a repeat
				add(setupStanding, 0)
			}
		}
	}

	// A kind the owner dismissed a plan of is not one they always start.
	dismissed := map[string]bool{}
	for _, p := range ev.plans {
		if p.State != planDismissed {
			continue
		}
		for _, t := range p.Tasks {
			if ruleStartable(t) {
				dismissed[trustKey(t)] = true
			}
		}
	}
	started := map[string]bool{} // the kinds the owner started before
	for _, p := range ev.plans {
		if p.State != planStarted || ev.health != nil && ev.health(p.Conversation) {
			continue
		}
		kinds := map[string]bool{}
		owner := true
		for _, t := range p.Tasks {
			if !ruleStartable(t) {
				continue
			}
			kinds[trustKey(t)] = true
			owner = owner && t.OwnerStarted
		}
		if len(kinds) == 0 || !owner {
			continue // nothing in it waited for Start, or the hub started it without asking
		}
		repeat := true
		for k := range kinds {
			repeat = repeat && started[k] && !dismissed[k] && !ev.trusted[k]
		}
		if repeat && in(p.CreatedAt) {
			add(setupTrust, 0)
		}
		for k := range kinds {
			started[k] = true
		}
	}

	for _, t := range ev.todos {
		v := t.Supervision
		if v == nil || t.ReopenedAt != nil || v.Round != 0 || len(v.Reviews) != 1 || t.Delivery == nil || t.Delivery.State != deliveryLive {
			continue
		}
		r := v.Reviews[0]
		if r.Verdict != reviewAccepted || r.By != "" || r.UserCheck || !in(r.At) || len(v.DoneWhen) == 0 || len(r.Checks) != len(v.DoneWhen) {
			continue
		}
		met := true
		for _, c := range r.Checks {
			met = met && c.Met && strings.TrimSpace(c.Evidence) != ""
		}
		if met {
			add(setupSample, 0)
		}
	}
	return out
}

// setupState is which mechanisms are on now.
type setupState struct {
	leaders, delegate, review bool
	helloKnown                bool // the hub's hello could be read: delegate and review are as it says
	triage                    TriageSwitches
	triageKnown               bool // the triage's switches could be read
	samplePercent             int
}

// suggests: mechanism id is off and c reaches its threshold. tasks.trust and the standing authorizations are never
// "on" as a whole: what is already granted is left out of their counts (setupCount). The triage's three actions are
// counted only while the triage itself is on: without it nothing about a stopped turn is recorded, and they do
// nothing.
func (st setupState) suggests(id string, c setupNumber) bool {
	switch id {
	case setupLeaders:
		return !st.leaders && c.n >= setupLeaderMin
	case setupDelegate:
		return st.helloKnown && !st.delegate && c.n >= setupMin
	case setupDecide:
		return st.triageKnown && st.triage.Enabled && !st.triage.Decide && c.n >= setupMin
	case setupAutoContinue:
		return st.triageKnown && st.triage.Enabled && !st.triage.AutoContinue && c.n >= setupMin
	case setupWake:
		return st.triageKnown && st.triage.Enabled && !st.triage.Wake && c.n >= setupMin
	case setupSample:
		return st.samplePercent >= 100 && c.n >= setupMin
	case setupReview:
		return st.helloKnown && !st.review && c.n >= setupMin
	case setupStanding, setupTrust:
		return c.n >= setupMin
	}
	return false
}

func (s *Service) setupStateNow(ctx context.Context) setupState {
	// Not s.hello: a hello that failed reads as everything off, and a mechanism that is on must not be named.
	h, err := s.hub.Hello(ctx)
	st := setupState{leaders: len(s.settings(ctx).Leaders) > 0, helloKnown: err == nil, delegate: h.Agents.Delegate.On(),
		review: h.Agents.Review.On(), samplePercent: s.reviewSamplePercent(ctx)}
	if sw, err := s.readTriageSwitches(ctx); err == nil {
		st.triage, st.triageKnown = sw, true
	}
	return st
}

// setupRead reads what the counts are made from for a run at now. An error: nothing is counted or named this time.
func (s *Service) setupRead(ctx context.Context, now time.Time) (setupEvidence, error) {
	var ev setupEvidence
	st := s.ruleEvidence()
	if st == nil {
		return ev, errors.New("no database")
	}
	back := now.Add(-setupLookback)
	// What is counted in the window alone, and the marks the decisions are read with over the whole lookback.
	recent, err := st.WorkEventsBetween(ctx, now.Add(-setupWindow), now, []string{roundEvent, "resumed", reviewReworkEvent})
	if err != nil {
		return ev, err
	}
	marks, err := st.WorkEventsBetween(ctx, back, now, []string{"ask_escalated", ruleDecidedEvent})
	if err != nil {
		return ev, err
	}
	ev.events = append(recent, marks...)
	if ev.decisions, err = st.TodoDecisionsSince(ctx, back); err != nil {
		return ev, err
	}
	rows, err := st.TaskPlansMadeSince(ctx, back)
	if err != nil {
		return ev, err
	}
	for _, row := range rows {
		ev.plans = append(ev.plans, planFromRow(row))
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return ev, err
	}
	ev.todos = s.todosOf(ctx, "*")
	if ev.trusted, err = s.trustedKindsErr(ctx); err != nil {
		return ev, err
	}
	if ev.grants, err = s.standingAuthorizations(ctx); err != nil {
		return ev, err
	}
	seen := map[string]*TaskPlan{}
	ev.scopeOf = func(todo string) (string, string, bool) {
		t, found := s.todoNow(todo)
		if !found {
			return "", "", false
		}
		return s.ruleTodoScope(ctx, t, seen)
	}
	ev.health = func(conversation string) bool { return s.inHealth(ctx, conversation) }
	return ev, nil
}

// ---- the block of a run ----

// setupFor is the block of the briefing run that starts at now, worded in l: nil when this week's first briefing
// already reached the owner, or the records cannot be read (the next briefing tries again). A block that names
// nothing is still the week's: setupTold marks the week with it. Nothing is written here.
func (s *Service) setupFor(ctx context.Context, now time.Time, loc *time.Location, l i18n.Lang, web string) *setupBlock {
	st := s.store()
	if st == nil {
		return nil
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	var book setupBook
	if _, err := st.Setting(ctx, setupKey, &book); err != nil {
		s.logf("setup suggestions: %v", err)
		return nil
	}
	b := &setupBlock{Week: setupWeek(now, loc)}
	if book.Week == b.Week {
		return nil
	}
	ev, err := s.setupRead(ctx, now)
	if err != nil {
		s.logf("setup suggestions: %v", err)
		return nil
	}
	counts, state := setupCount(ev, now.Add(-setupWindow), now), s.setupStateNow(ctx)
	for _, id := range setupOrder {
		c := counts[id]
		if !state.suggests(id, c) {
			continue
		}
		if at, shown := book.Shown[id]; shown && now.Sub(at) < setupQuietFor {
			continue
		}
		b.Suggestions = append(b.Suggestions, setupWord(l, web, id, c))
	}
	return b
}

// setupWord words mechanism id's line in l: what it would have removed, what it costs in one sentence, and where it
// is turned on.
func setupWord(l i18n.Lang, web, id string, c setupNumber) setupSuggestion {
	sg := setupSuggestion{ID: id, Setting: setupSettings[id], Count: c.n}
	args := i18n.Args{"n": c.n}
	if id == setupLeaders {
		sg.Minutes = int(c.seconds/60 + 0.5)
		args["minutes"] = sg.Minutes
	}
	sg.Text = i18n.T(l, "assistant-setup_suggest."+id, args)
	if page := setupPages[id]; page != "" && web != "" {
		sg.Link = web + page
	}
	return sg
}

// setupTold: the briefing whose facts carry block b reached the owner at at (its run's turn ended normally with the
// `brief` item in its conversation, or its Lark card went out). The week is marked, so no later briefing of it carries a block, and each mechanism it named is
// quiet for setupQuietFor. This is the only write the block ever makes.
func (s *Service) setupTold(b *setupBlock, at time.Time) {
	st := s.store()
	if b == nil || st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	var book setupBook
	if _, err := st.Setting(ctx, setupKey, &book); err != nil {
		s.logf("setup suggestions: %v", err)
		return
	}
	if book.Week == b.Week {
		return // told already: the run's end and the card both say so
	}
	kept := map[string]time.Time{}
	for id, shown := range book.Shown {
		if at.Sub(shown) < setupQuietFor {
			kept[id] = shown
		}
	}
	for _, sg := range b.Suggestions {
		kept[sg.ID] = at.UTC()
	}
	if err := st.PutSetting(ctx, setupKey, setupBook{Week: b.Week, Shown: kept}); err != nil {
		s.logf("setup suggestions: %v", err) // at worst the next briefing of the week carries it once more
	}
}
