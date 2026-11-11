package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// fakeRuleEvidence is the answers a suggested rule is counted from, without a database; like the database it returns
// only what is inside the window.
type fakeRuleEvidence struct {
	decisions []db.TodoDecisionRow
	plans     []*TaskPlan
	events    []db.WorkEvent
}

func (f *fakeRuleEvidence) TodoDecisionsSince(_ context.Context, since time.Time) ([]db.TodoDecisionRow, error) {
	var out []db.TodoDecisionRow
	for _, r := range f.decisions {
		if !r.CreatedAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRuleEvidence) TaskPlansMadeSince(_ context.Context, since time.Time) ([]db.TaskPlanRow, error) {
	var out []db.TaskPlanRow
	for _, p := range f.plans {
		if p.CreatedAt.Before(since) || p.State == planOpen {
			continue
		}
		raw, _ := json.Marshal(p.Tasks)
		out = append(out, db.TaskPlanRow{ID: p.ID, Conversation: p.Conversation, Tasks: raw, State: p.State, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt})
	}
	return out, nil
}

func (f *fakeRuleEvidence) WorkEventsBetween(_ context.Context, since, _ time.Time, kinds []string) ([]db.WorkEvent, error) {
	var out []db.WorkEvent
	for _, e := range f.events {
		if !e.At.Before(since) && slices.Contains(kinds, e.Kind) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ruleStore is memStore with the revision-checked write standing authorizations need.
type ruleStore struct{ *memStore }

func (m ruleStore) CompareSetting(ctx context.Context, key string, revision int64, value any) (bool, error) {
	var cur struct {
		Revision int64 `json:"revision"`
	}
	if _, err := m.Setting(ctx, key, &cur); err != nil || cur.Revision != revision {
		return false, err
	}
	return true, m.PutSetting(ctx, key, value)
}

func newRuleService(t *testing.T) (*Service, *fakeRuleEvidence) {
	t.Helper()
	s, _ := newTaskServiceOn(t, ruleStore{&memStore{}})
	ev := &fakeRuleEvidence{}
	s.rules.evidence = ev
	return s, ev
}

func ruleAgo(d time.Duration) time.Time { return time.Now().UTC().Add(-d) }

// rulePlan is a plan with one task that changes files in /code/app on box, made days ago.
func rulePlan(id, state string, days int, owner bool) *TaskPlan {
	at := ruleAgo(time.Duration(days) * 24 * time.Hour)
	return &TaskPlan{ID: id, Conversation: "conv1", State: state, CreatedAt: at, UpdatedAt: at,
		Tasks: []PlanTask{{Title: "t", Machine: "box", Cwd: "/code/app", OwnerStarted: owner}}}
}

func ruleRequest(s *Service, role, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, path, nil)
	if role != "" {
		r.Header.Set(RoleHeader, role)
	}
	s.ServeHTTP(w, r)
	return w
}

func TestRuleActionReadsOnlyGoAheadsARuleMayCover(t *testing.T) {
	for _, c := range []struct {
		question, choice, action string
		yes, ok                  bool
	}{
		{"Merge the PR now?", "Yes", "merge", true, true},
		{"Merge the PR now?", "Yes (recommended)", "merge", true, true},
		{"Merge the PR now?", "No", "merge", false, true},
		{"Merge the PR now?", "Later, after the release", "", false, false},
		{"Push this branch?", "Continue", "push", true, true},
		{"Deploy-test now?", "Approve", "deploy-test", true, true},
		{"The PR is green. What next?", "Merge", "merge", true, true},
		{"The PR is green. What next?", "Merge the PR now", "merge", true, true},
		{"The PR is green. What next?", "Deploy to test", "deploy-test", true, true},
		{"The PR is green. What next?", "Wait for the review", "", false, false},
		// What a rule never covers, however the answer reads.
		{"Deploy-production now?", "Yes", "", false, false},
		{"Send the message now?", "Yes", "", false, false},
		{"Delete the files?", "Yes", "", false, false},
		{"Spend the money?", "Yes", "", false, false},
		{"Force-push over the release branch?", "Push", "", false, false},
		{"Ready for production?", "Deploy to test", "", false, false},
		{"Which model?", "Yes", "", false, false},
	} {
		action, yes, ok := ruleAction(c.question, c.choice)
		if action != c.action || yes != c.yes || ok != c.ok {
			t.Errorf("ruleAction(%q, %q) = %q %v %v, want %q %v %v", c.question, c.choice, action, yes, ok, c.action, c.yes, c.ok)
		}
	}
}

func TestRulePlanRunsCountTheOwnersStartsInARow(t *testing.T) {
	key := trustKey(PlanTask{Machine: "box", Cwd: "/code/app"})
	three := []*TaskPlan{rulePlan("p1", planStarted, 5, true), rulePlan("p2", planStarted, 4, true), rulePlan("p3", planStarted, 3, true)}
	if runs, task := rulePlanRuns(three); runs[key] != 3 || task[key].Machine != "box" || task[key].Cwd != "/code/app" || task[key].Readonly {
		t.Fatalf("three starts: %v %+v", runs, task)
	}
	// A plan the hub started without asking is no answer; a dismissed one starts the count again.
	if runs, _ := rulePlanRuns(append([]*TaskPlan{rulePlan("p0", planStarted, 6, false)}, three[:2]...)); runs[key] != 2 {
		t.Fatalf("an automatic start counted: %v", runs)
	}
	if runs, _ := rulePlanRuns([]*TaskPlan{three[0], three[1], rulePlan("px", planDismissed, 3, false), three[2]}); runs[key] != 1 {
		t.Fatalf("a dismissal did not start the count again: %v", runs)
	}
	// A plan counts once for a kind; read-only tasks and a leader's workers are no answer.
	p := rulePlan("p4", planStarted, 2, true)
	p.Tasks = append(p.Tasks, p.Tasks[0],
		PlanTask{Machine: "box", Cwd: "/code/read", Readonly: true, OwnerStarted: true},
		PlanTask{Machine: "box", Cwd: "/code/worker", Parent: "todo_1", OwnerStarted: true})
	if runs, _ := rulePlanRuns([]*TaskPlan{p}); len(runs) != 1 || runs[key] != 1 {
		t.Fatalf("one plan: %v", runs)
	}
}

func TestRuleDecisionRunsCountOnlyTheOwnersAnswers(t *testing.T) {
	at := ruleAgo(48 * time.Hour)
	row := func(i int, source, question, choice, card string) db.TodoDecisionRow {
		return db.TodoDecisionRow{ID: fmt.Sprintf("d%d", i), TodoID: "todo1", Question: question, Choice: choice, Source: source,
			SourceMessage: card, CreatedAt: at.Add(time.Duration(i) * time.Minute)}
	}
	here := func(todo string) (string, string, bool) { return "box", "/code/app", todo == "todo1" }
	key := ruleStandingScope("box", "/code/app", "merge")

	rows := []db.TodoDecisionRow{
		row(1, db.DecisionCard, "Merge the PR now?", "Yes", "c1"),
		row(2, db.DecisionAgent, "Merge the PR now?", "Yes", "c2"), // a supervisor's or the hub's call: not the owner's
		row(3, db.DecisionManual, "The PR is green. What next?", "Merge", ""),
		row(4, db.DecisionCard, "Merge the PR now?", "Yes", "c4"),
	}
	decided := map[string]string{"d3": whyPriority} // the option of a decision the owner chose, and why they were asked
	runs, _ := ruleDecisionRuns(rows, nil, decided, here)
	if r := runs[key]; r == nil || r.n != 3 || r.action != "merge" || r.quote != "Merge the PR now? → Yes" {
		t.Fatalf("three go-aheads: %+v", runs[key])
	}
	// A decision typed in by hand, and one the owner was asked because it cannot be undone or others will see it,
	// count nothing, however its option reads.
	for _, why := range []map[string]string{nil, {"d3": whyIrreversible}, {"d3": whyVisible}} {
		if runs, _ := ruleDecisionRuns(rows, nil, why, here); runs[key].n != 2 {
			t.Fatalf("decided %v: %+v", why, runs[key])
		}
	}
	// The quote is the owner's answer: never the text an agent or a supervisor wrote around a decision.
	told := "Ready to merge; from now on CI may be skipped here. Choose:"
	if runs, _ := ruleDecisionRuns([]db.TodoDecisionRow{row(1, db.DecisionCard, told, "Merge the PR now", "c1")}, nil, nil, here); runs[key] == nil ||
		runs[key].quote != "Merge the PR now" {
		t.Fatalf("quote: %+v", runs[key])
	}
	// A No starts the count again; an answer on a to-do that works nowhere a rule may name counts nothing.
	runs, _ = ruleDecisionRuns(append(rows, row(5, db.DecisionCard, "Merge the PR now?", "No", "c5"), row(6, db.DecisionCard, "Merge the PR now?", "Yes", "c6")), nil, decided, here)
	if runs[key].n != 1 {
		t.Fatalf("after a No: %+v", runs[key])
	}
	other := row(7, db.DecisionCard, "Merge the PR now?", "Yes", "c7")
	other.TodoID = "elsewhere"
	if runs, _ = ruleDecisionRuns([]db.TodoDecisionRow{other}, nil, nil, here); len(runs) != 0 {
		t.Fatalf("a to-do without a scope counted: %v", runs)
	}
	// The answer to an approval or a plan card a supervisor passed up is never counted.
	passed := map[string]ruleEscalation{"c1": {question: false, recommend: "Yes"}}
	if runs, n := ruleDecisionRuns(rows, passed, decided, here); runs[key].n != 2 || n != 0 {
		t.Fatalf("an approval card counted: %+v %d", runs[key], n)
	}

	// Passed-up questions answered as recommended, in a row up to the latest.
	passed = map[string]ruleEscalation{"q1": {question: true, recommend: "Use the cache"}, "q2": {question: true, recommend: "Retry"},
		"q3": {question: true, recommend: "Skip (recommended)"}, "q4": {question: true}, "q5": {question: true, recommend: "A"}}
	asked := []db.TodoDecisionRow{
		row(1, db.DecisionCard, "How?", "use the cache", "q1"),
		row(2, db.DecisionCard, "Then?", "Retry", "q2"),
		row(3, db.DecisionCard, "And?", "Skip", "q3"),
		row(4, db.DecisionCard, "Free?", "Anything", "q4"), // no recommendation: neither way
	}
	if _, n := ruleDecisionRuns(asked, passed, nil, here); n != 3 {
		t.Fatalf("recommended run = %d, want 3", n)
	}
	if _, n := ruleDecisionRuns(append(asked, row(5, db.DecisionCard, "Which?", "B", "q5")), passed, nil, here); n != 0 {
		t.Fatalf("another answer did not start the count again: %d", n)
	}
}

func TestRuleEscalationsReadTheCardKindAndRecommendation(t *testing.T) {
	got := ruleEscalations([]db.WorkEvent{
		{Kind: "ask_escalated", Detail: map[string]any{"todo": "t"}}, // from before the card was kept
		{Kind: "ask_escalated", Detail: map[string]any{"card": "c1", "kind": proto.IxQuestion, "recommend": "Yes"}},
		{Kind: "ask_escalated", Detail: map[string]any{"card": "c2", "kind": proto.IxPermission, "recommend": "Allow"}},
		{Kind: "ask_routed", Detail: map[string]any{"card": "c3", "kind": proto.IxQuestion}},
	})
	if len(got) != 2 || got["c1"] != (ruleEscalation{question: true, recommend: "Yes"}) || got["c2"].question {
		t.Fatalf("escalations: %+v", got)
	}
	why := ruleOwnerDecisions([]db.WorkEvent{
		{Kind: ruleDecidedEvent, Detail: map[string]any{"todo": "t", "decision": "dec_1", "why_owner": whyIrreversible}},
		{Kind: ruleDecidedEvent, Detail: map[string]any{"todo": "t", "decision": "dec_2", "why_owner": ""}},
		{Kind: ruleDecidedEvent, Detail: map[string]any{"todo": "t"}},
		{Kind: "ask_escalated", Detail: map[string]any{"decision": "dec_3"}},
	})
	if _, chosen := why["dec_2"]; len(why) != 2 || why["dec_1"] != whyIrreversible || !chosen {
		t.Fatalf("owner decisions: %+v", why)
	}
}

// Three Starts in 14 days: the next plan of that kind carries the suggestion, Always adds the tasks.trust entry and
// only a full device may tap it, and an id whose answers do not earn it makes nothing.
func TestPlanStartSuggestionAndAlways(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	open := rulePlan("open", planOpen, 0, false)
	key := trustKey(open.Tasks[0])
	id := ruleSuggestionID(ruleKindPlanStart, key)

	// Two recent starts and one from before the window: not earned, and a crafted id mints nothing.
	ev.plans = []*TaskPlan{rulePlan("p0", planStarted, 20, true), rulePlan("p1", planStarted, 5, true), rulePlan("p2", planStarted, 4, true)}
	if p := s.planWithSuggestion(ctx, open, i18n.En); p.Suggestion != nil {
		t.Fatalf("suggested after two starts: %+v", p.Suggestion)
	}
	if w := ruleRequest(s, "", "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusNotFound {
		t.Fatalf("always on an unearned id: %d %s", w.Code, w.Body)
	}
	if have := s.trustedKinds(ctx); len(have) != 0 {
		t.Fatalf("an unearned id made a rule: %v", have)
	}

	ev.plans = append(ev.plans, rulePlan("p3", planStarted, 1, true))
	s.rules.forget()
	p := s.planWithSuggestion(ctx, open, i18n.En)
	sg := p.Suggestion
	if sg == nil || sg.ID != id || sg.Kind != ruleKindPlanStart || sg.Creates != trustSetting || sg.Count != 3 ||
		sg.Scope.MachineID != "box" || sg.Scope.Directory != "/code/app" || !strings.Contains(sg.Text, "/code/app") {
		t.Fatalf("suggestion: %+v", sg)
	}
	if open.Suggestion != nil {
		t.Fatal("the suggestion was kept with the plan")
	}
	// Not on a plan that no longer waits, nor on one of another kind.
	started := rulePlan("s", planStarted, 0, true)
	elsewhere := rulePlan("e", planOpen, 0, false)
	elsewhere.Tasks[0].Cwd = "/code/other"
	if s.planWithSuggestion(ctx, started, i18n.En).Suggestion != nil || s.planWithSuggestion(ctx, elsewhere, i18n.En).Suggestion != nil {
		t.Fatal("suggested on a started plan or another directory's")
	}

	for _, role := range []string{roleAnswer, roleExtension} {
		if w := ruleRequest(s, role, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusForbidden {
			t.Fatalf("%s device: %d %s", role, w.Code, w.Body)
		}
	}
	if have := s.trustedKinds(ctx); len(have) != 0 {
		t.Fatalf("a device without full access made a rule: %v", have)
	}
	w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"creates":"tasks.trust"`) {
		t.Fatalf("always: %d %s", w.Code, w.Body)
	}
	if have := s.trustedKinds(ctx); !have[key] || len(have) != 1 {
		t.Fatalf("tasks.trust = %v", have)
	}
	if s.planWithSuggestion(ctx, open, i18n.En).Suggestion != nil {
		t.Fatal("still suggested after Always")
	}
	// Removing the rule where it is listed does not bring the line straight back.
	if ok, err := s.forgetTrust(ctx, key); err != nil || !ok {
		t.Fatalf("forget: %v %v", ok, err)
	}
	s.rules.forget()
	if s.planWithSuggestion(ctx, open, i18n.En).Suggestion != nil {
		t.Fatal("suggested again right after the rule was removed")
	}
}

func TestRuleNotNowIsRememberedThirtyDays(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	ev.plans = []*TaskPlan{rulePlan("p1", planStarted, 5, true), rulePlan("p2", planStarted, 4, true), rulePlan("p3", planStarted, 3, true)}
	open := rulePlan("open", planOpen, 0, false)
	id := ruleSuggestionID(ruleKindPlanStart, trustKey(open.Tasks[0]))

	if w := ruleRequest(s, roleAnswer, "/api/v1/assistant/rule-suggestions/"+id+"/not-now"); w.Code != http.StatusForbidden {
		t.Fatalf("answer-only device: %d", w.Code)
	}
	if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/rs_0000000000000000/not-now"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: %d", w.Code)
	}
	if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/not-now"); w.Code != http.StatusOK {
		t.Fatalf("not now: %d %s", w.Code, w.Body)
	}
	if s.planWithSuggestion(ctx, open, i18n.En).Suggestion != nil {
		t.Fatal("suggested after Not now")
	}
	if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusNotFound {
		t.Fatalf("always after Not now: %d", w.Code)
	}
	if have := s.trustedKinds(ctx); len(have) != 0 {
		t.Fatalf("tasks.trust = %v", have)
	}
	// 29 days on it is still quiet; after 30 the line is back.
	for _, c := range []struct {
		days int
		want bool
	}{{29, false}, {31, true}} {
		if err := s.store().PutSetting(ctx, ruleSuggestKey, ruleSuggestBook{NotNow: map[string]time.Time{id: ruleAgo(time.Duration(c.days) * 24 * time.Hour)}}); err != nil {
			t.Fatal(err)
		}
		s.rules.forget()
		if got := s.planWithSuggestion(ctx, open, i18n.En).Suggestion != nil; got != c.want {
			t.Fatalf("%d days after Not now: suggested = %v", c.days, got)
		}
	}
}

// ruleMergeAnswers puts a task with an agent in /code/app on box, and n times the owner's Yes to merging there.
func ruleMergeAnswers(s *Service, ev *fakeRuleEvidence, n int) {
	putTodo(s, Todo{ID: "todo1", Title: "Fix login", Origin: "conv1",
		Agents: []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/app"}}})
	for i := range n {
		ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: "d", TodoID: "todo1", Question: "Merge the PR now?", Choice: "Yes",
			Source: db.DecisionCard, SourceMessage: "card" + string(rune('a'+i)), CreatedAt: ruleAgo(time.Duration(10-i) * time.Hour)})
	}
}

func ruleDecision(why string) *acceptAsk {
	return &acceptAsk{Conclusion: true, Outcome: outcomeDecide, Reason: "The PR is green. What next?", WhyOwner: why,
		Options: []DecisionOption{{Label: "Merge"}, {Label: "Wait for the review"}}}
}

// Three go-aheads to merge in one directory: the next decision that offers it carries the suggestion, and Always
// grants the standing authorization through the checks and the store every grant goes through.
func TestStandingSuggestionAndAlways(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	ruleMergeAnswers(s, ev, 2)
	id := ruleSuggestionID(ruleKindStanding, ruleStandingScope("box", "/code/app", "merge"))
	if sg := s.askSuggestion(ctx, "todo1", ruleDecision(""), i18n.En); sg != nil {
		t.Fatalf("suggested after two answers: %+v", sg)
	}
	if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusNotFound {
		t.Fatalf("always on an unearned id: %d %s", w.Code, w.Body)
	}

	ev.decisions = nil
	ruleMergeAnswers(s, ev, 3)
	s.rules.forget()
	sg := s.askSuggestion(ctx, "todo1", ruleDecision(""), i18n.En)
	if sg == nil || sg.ID != id || sg.Kind != ruleKindStanding || sg.Creates != ruleKindStanding || sg.Count != 3 ||
		sg.Scope.MachineID != "box" || sg.Scope.Directory != "/code/app" || sg.Scope.Action != "merge" ||
		strings.Join(sg.Scope.Conditions, ",") != "ci_green,no_conflicts" || sg.Text == "" {
		t.Fatalf("suggestion: %+v", sg)
	}
	// Never on a decision that cannot be undone or that others will see, nor on one that offers no such go-ahead.
	for _, why := range []string{whyIrreversible, whyVisible} {
		if s.askSuggestion(ctx, "todo1", ruleDecision(why), i18n.En) != nil {
			t.Fatalf("suggested on a %s decision", why)
		}
	}
	plain := ruleDecision("")
	plain.Options = []DecisionOption{{Label: "Rewrite it"}, {Label: "Keep it"}}
	if s.askSuggestion(ctx, "todo1", plain, i18n.En) != nil {
		t.Fatal("suggested on a decision without the go-ahead")
	}

	if w := ruleRequest(s, roleAnswer, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusForbidden {
		t.Fatalf("answer-only device: %d", w.Code)
	}
	if list, _ := s.standingAuthorizations(ctx); len(list.Authorizations) != 0 {
		t.Fatalf("granted without full access: %+v", list)
	}
	w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always")
	if w.Code != http.StatusOK {
		t.Fatalf("always: %d %s", w.Code, w.Body)
	}
	list, err := s.standingAuthorizations(ctx)
	if err != nil || list.Revision != 1 || len(list.Authorizations) != 1 {
		t.Fatalf("registry: %+v %v", list, err)
	}
	g := list.Authorizations[0]
	if g.ID == "" || g.GrantedAt.IsZero() || g.MachineID != "box" || g.Directory != "/code/app" || strings.Join(g.Actions, ",") != "merge" ||
		strings.Join(g.Conditions, ",") != "ci_green,no_conflicts" || g.Quote != "Merge the PR now? → Yes" || g.Source == "" {
		t.Fatalf("grant: %+v", g)
	}
	if sg.Scope.Quote != g.Quote {
		t.Fatalf("the suggestion showed the quote %q, the grant keeps %q", sg.Scope.Quote, g.Quote)
	}
	if err := checkStandingGrant(g); err != nil {
		t.Fatalf("the grant does not pass the registry's own check: %v", err)
	}
	// A second tap makes no second grant, and a directory a grant covers is not suggested.
	if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusNotFound {
		t.Fatalf("second always: %d %s", w.Code, w.Body)
	}
	if list, _ := s.standingAuthorizations(ctx); len(list.Authorizations) != 1 {
		t.Fatalf("a second grant: %+v", list)
	}
	if err := s.store().PutSetting(ctx, ruleSuggestKey, ruleSuggestBook{}); err != nil {
		t.Fatal(err)
	}
	s.rules.forget()
	if s.askSuggestion(ctx, "todo1", ruleDecision(""), i18n.En) != nil {
		t.Fatal("suggested for a directory a standing authorization covers")
	}
}

// What a rule never covers is not suggested and cannot be made, however often the owner said yes.
func TestRuleNeverForActionsThatCannotBeUndone(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	putTodo(s, Todo{ID: "todo1", Title: "Ship", Origin: "conv1", Agents: []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/app"}}})
	for i, q := range []string{"Deploy-production now?", "Send the message now?", "Delete the files?", "Spend the money?"} {
		for j := range 3 {
			ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: "d", TodoID: "todo1", Question: q, Choice: "Yes",
				Source: db.DecisionCard, CreatedAt: ruleAgo(time.Duration(40-4*i-j) * time.Hour)})
		}
	}
	cands, err := s.ruleCandidates(ctx, true)
	if err != nil || len(cands) != 0 {
		t.Fatalf("candidates: %v %v", cands, err)
	}
	for _, action := range []string{"deploy-production", "send", "delete", "spend"} {
		id := ruleSuggestionID(ruleKindStanding, ruleStandingScope("box", "/code/app", action))
		if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body)
		}
	}
	if list, _ := s.standingAuthorizations(ctx); len(list.Authorizations) != 0 {
		t.Fatalf("granted: %+v", list)
	}
}

// Three passed-up questions answered as recommended: the next passed-up question points to agents.delegate, a tool
// permission card never does, and Always stores nothing.
func TestDelegateSuggestionStoresNothing(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	for i, card := range []string{"q1", "q2", "q3"} {
		at := ruleAgo(time.Duration(10-i) * time.Hour)
		ev.events = append(ev.events, db.WorkEvent{At: at, Kind: "ask_escalated",
			Detail: map[string]any{"card": card, "kind": proto.IxQuestion, "recommend": "Retry"}})
		ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: "d", TodoID: "todo1", Question: "What now?", Choice: "Retry",
			Source: db.DecisionCard, SourceMessage: card, CreatedAt: at.Add(time.Minute)})
	}
	question := proto.Interaction{ID: "ix1", Kind: proto.IxQuestion, MachineID: "box", SessionID: "s1",
		Questions: []proto.Question{{Question: "Which cache?", Options: []proto.QuestionOption{{Label: "Redis"}, {Label: "Memory"}}}}}
	sg := s.cardSuggestion(ctx, question, i18n.En)
	if sg == nil || sg.Kind != ruleKindDelegate || sg.Creates != "" || sg.Setting != ruleDelegateSetting || sg.Count != 3 || sg.Text == "" {
		t.Fatalf("suggestion: %+v", sg)
	}
	permission := proto.Interaction{ID: "ix2", Kind: proto.IxPermission, MachineID: "box", SessionID: "s1", ToolName: "Bash"}
	if s.cardSuggestion(ctx, permission, i18n.En) != nil {
		t.Fatal("suggested on a tool permission card")
	}
	before := s.store().(ruleStore).puts
	w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+sg.ID+"/always")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"setting":"agents.delegate"`) || !strings.Contains(w.Body.String(), `"creates":""`) {
		t.Fatalf("always: %d %s", w.Code, w.Body)
	}
	if puts := s.store().(ruleStore).puts; puts != before+1 { // the answer to the suggestion, and nothing else
		t.Fatalf("%d settings written", puts-before)
	}
	if s.cardSuggestion(ctx, question, i18n.En) != nil {
		t.Fatal("still suggested after Always")
	}
}

// Only the owner's Start on the card marks a plan's tasks as started by the owner: not the hub's own start, and not
// what a task_plan call says.
func TestOwnerStartedIsTheHubsToSet(t *testing.T) {
	ctx := context.Background()
	plan := func(s *Service) *TaskPlan {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		for _, p := range s.tasks.plans {
			cp := *p
			cp.Tasks = append([]PlanTask(nil), p.Tasks...)
			return &cp
		}
		return nil
	}
	args := func(readonly bool) json.RawMessage {
		var in map[string][]map[string]any
		_ = json.Unmarshal(planArgs(readonly), &in)
		in["tasks"][0]["owner_started"] = true
		raw, _ := json.Marshal(in)
		return raw
	}

	s, _ := newTaskService(t)
	if _, err := s.toolTaskPlan(ctx, "conv1", args(false)); err != nil {
		t.Fatal(err)
	}
	p := plan(s)
	if p == nil || p.State != planOpen || p.Tasks[0].OwnerStarted {
		t.Fatalf("a plan's arguments set owner_started: %+v", p)
	}
	if w := ruleRequest(s, roleFull, "/api/v1/task-plans/"+p.ID+"/start"); w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	if p = plan(s); p.State != planStarted || !p.Tasks[0].OwnerStarted {
		t.Fatalf("the owner's Start was not kept: %+v", p)
	}

	s2, _ := newTaskService(t) // a read-only task starts without asking
	if _, err := s2.toolTaskPlan(ctx, "conv1", args(true)); err != nil {
		t.Fatal(err)
	}
	if p = plan(s2); p == nil || p.State != planStarted || p.Tasks[0].OwnerStarted {
		t.Fatalf("the hub's own start counts as the owner's: %+v", p)
	}
}

// The options of decisions (kept as manual) earn a rule only with the owner_decided event that says why the owner was
// asked: three irreversible ones earn none, and Always on their id makes nothing.
func TestIrreversibleDecisionsNeverEarnARule(t *testing.T) {
	ctx := context.Background()
	s, ev := newRuleService(t)
	putTodo(s, Todo{ID: "todo1", Title: "Ship", Origin: "conv1", Agents: []TodoAgent{{MachineID: "box", SessionID: "s1", Cwd: "/code/app"}}})
	id := ruleSuggestionID(ruleKindStanding, ruleStandingScope("box", "/code/app", "merge"))
	answers := func(why string) {
		ev.decisions, ev.events = nil, nil
		for i := range 3 {
			at := ruleAgo(time.Duration(10-i) * time.Hour)
			dec := fmt.Sprintf("dec_%d", i)
			ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: dec, TodoID: "todo1", Question: "The PR is green. What next?",
				Choice: "Merge", Source: db.DecisionManual, CreatedAt: at})
			ev.events = append(ev.events, db.WorkEvent{At: at, Kind: ruleDecidedEvent,
				Detail: map[string]any{"todo": "todo1", "decision": dec, "why_owner": why}})
		}
		s.rules.forget()
	}
	for _, why := range []string{whyIrreversible, whyVisible} {
		answers(why)
		if sg := s.askSuggestion(ctx, "todo1", ruleDecision(whyPriority), i18n.En); sg != nil {
			t.Fatalf("%s answers earned a suggestion: %+v", why, sg)
		}
		if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/always"); w.Code != http.StatusNotFound {
			t.Fatalf("%s: always: %d %s", why, w.Code, w.Body)
		}
	}
	if list, _ := s.standingAuthorizations(ctx); len(list.Authorizations) != 0 {
		t.Fatalf("granted: %+v", list)
	}
	answers(whyPriority)
	sg := s.askSuggestion(ctx, "todo1", ruleDecision(whyPriority), i18n.En)
	if sg == nil || sg.ID != id || sg.Scope.Quote != "Merge" {
		t.Fatalf("priority answers: %+v", sg)
	}
}

// Two taps on Always at once passed the count before either stored anything: the grant is made once.
func TestRuleGrantIsMadeOnce(t *testing.T) {
	ctx := context.Background()
	s, _ := newRuleService(t)
	c := &ruleCandidate{id: "rs_x", kind: ruleKindStanding, count: 3, machineID: "box", directory: "/code/app", action: "merge", quote: "Merge"}
	if _, err := s.ruleGrant(ctx, c, i18n.En); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ruleGrant(ctx, c, i18n.En); !errors.Is(err, errRuleSuggestionGone) {
		t.Fatalf("second grant: %v", err)
	}
	if list, _ := s.standingAuthorizations(ctx); len(list.Authorizations) != 1 {
		t.Fatalf("registry: %+v", list)
	}
}

// A card keeps the suggestion it was rung with: Not now (and Always) take it off every pending card that carries it
// and show the card again, so the line is gone on every device and from the list of pending cards.
func TestRuleSuggestionIsTakenOffPendingCards(t *testing.T) {
	s, ev := newRuleService(t)
	f := s.hub.(*fakeHub)
	for i, card := range []string{"q1", "q2", "q3"} {
		at := ruleAgo(time.Duration(10-i) * time.Hour)
		ev.events = append(ev.events, db.WorkEvent{At: at, Kind: "ask_escalated",
			Detail: map[string]any{"card": card, "kind": proto.IxQuestion, "recommend": "Retry"}})
		ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: "d" + card, TodoID: "todo1", Question: "What now?", Choice: "Retry",
			Source: db.DecisionCard, SourceMessage: card, CreatedAt: at.Add(time.Minute)})
	}
	pendingOf := func(id string) proto.Interaction {
		s.fleet.mu.Lock()
		defer s.fleet.mu.Unlock()
		return s.fleet.interactions[id].ix
	}
	s.fleet.mu.Lock()
	if s.fleet.interactions == nil {
		s.fleet.interactions = map[string]*pending{}
	}
	for _, id := range []string{"ix1", "ix2"} {
		s.fleet.interactions[id] = &pending{told: true, ix: proto.Interaction{ID: id, Kind: proto.IxQuestion, MachineID: "box", SessionID: "s1",
			CreatedAt: time.Now(), Questions: []proto.Question{{Question: "Which cache?", Options: []proto.QuestionOption{{Label: "Redis"}, {Label: "Memory"}}}}}}
	}
	s.fleet.mu.Unlock()
	var id string
	for _, card := range []string{"ix1", "ix2"} {
		ix := s.suggestOnCard(pendingOf(card))
		if ix.Suggestion == nil || pendingOf(card).Suggestion == nil {
			t.Fatalf("%s carries no suggestion", card)
		}
		id = ix.Suggestion.ID
	}
	shown := func() map[string]bool { // the cards the hub was last shown without a suggestion
		f.mu.Lock()
		defer f.mu.Unlock()
		out := map[string]bool{}
		for _, tl := range f.told {
			var in assistantwire.FleetInteraction
			if tl.event == assistantwire.EventFleetInteraction && json.Unmarshal(tl.params, &in) == nil {
				out[in.Interaction.ID] = in.Interaction.Suggestion == nil
			}
		}
		return out
	}
	if got := shown(); len(got) != 0 {
		t.Fatalf("shown before any answer: %v", got)
	}
	if w := ruleRequest(s, roleFull, "/api/v1/assistant/rule-suggestions/"+id+"/not-now"); w.Code != http.StatusOK {
		t.Fatalf("not now: %d %s", w.Code, w.Body)
	}
	for _, card := range []string{"ix1", "ix2"} {
		if pendingOf(card).Suggestion != nil {
			t.Fatalf("%s still carries the suggestion after Not now", card)
		}
	}
	if got := shown(); !got["ix1"] || !got["ix2"] {
		t.Fatalf("the apps were not shown the cards without the line: %v", got)
	}
}

// ruleTodos is a database that has the to-dos, and nothing else of todoStore: what the count needs of it.
type ruleTodos struct {
	ruleStore
	todoStore
	fail   error
	todos  []db.TodoRow
	agents []db.TodoAgentRow
}

func (r *ruleTodos) ListTodos(context.Context) ([]db.TodoRow, error) { return r.todos, r.fail }
func (r *ruleTodos) ListAllTodoSpaces(context.Context) ([]db.TodoSpaceRow, error) {
	return nil, nil
}
func (r *ruleTodos) ListAllTodoAgents(context.Context) ([]db.TodoAgentRow, error) {
	return r.agents, nil
}
func (r *ruleTodos) ListTodoThreads(context.Context) (map[string]db.TodoThread, error) {
	return nil, nil
}
func (r *ruleTodos) ListTodoSources(context.Context) (map[string]db.TodoSource, error) {
	return nil, nil
}

// After a restart nothing may have read the to-dos yet: the count reads them itself, so the first card (or the tap on
// Always) finds where each decision's task works, and a count that cannot read them is an error, not "nothing earned"
// kept for 30 seconds.
func TestRuleCountReadsTheTodosFirst(t *testing.T) {
	ctx := context.Background()
	at := ruleAgo(time.Hour)
	st := &ruleTodos{ruleStore: ruleStore{&memStore{}}, fail: errors.New("database away"),
		todos:  []db.TodoRow{{ID: "todo1", Title: "Fix login", Origin: "conv1", CreatedAt: at, UpdatedAt: at}},
		agents: []db.TodoAgentRow{{TodoID: "todo1", MachineID: "box", SessionID: "s1", Cwd: "/code/app", LinkedAt: at}}}
	s, _ := newTaskServiceOn(t, st)
	ev := &fakeRuleEvidence{}
	s.rules.evidence = ev
	for i := range 3 {
		ev.decisions = append(ev.decisions, db.TodoDecisionRow{ID: fmt.Sprintf("d%d", i), TodoID: "todo1", Question: "Merge the PR now?", Choice: "Yes",
			Source: db.DecisionCard, SourceMessage: fmt.Sprintf("card%d", i), CreatedAt: ruleAgo(time.Duration(10-i) * time.Hour)})
	}
	id := ruleSuggestionID(ruleKindStanding, ruleStandingScope("box", "/code/app", "merge"))
	if cands, err := s.ruleCandidates(ctx, false); err == nil {
		t.Fatalf("counted without the to-dos: %v", cands)
	}
	st.fail = nil
	cands, err := s.ruleCandidates(ctx, false) // not fresh: the failed count was not kept
	if err != nil || cands[id] == nil || cands[id].count != 3 {
		t.Fatalf("candidates: %+v %v", cands, err)
	}
}

// The option the owner took of a decision is kept with why they were asked (owner_decided), by the decision's id; a
// recommendation the hub took at the deadline is not the owner's and leaves none.
func TestOwnerDecidedIsKeptWithTheDecision(t *testing.T) {
	database := todoTestDB(t)
	f := newFakeHub().withMachine()
	f.setSessions("box", proto.Session{SessionID: "s1", MachineID: "box", Title: "Ship", Cwd: "/code/app", Agent: "claude", Status: proto.StatusIdle})
	s := newKBServiceOn(t, f, database)
	ctx := context.Background()
	todo, err := s.createTodo(ctx, "Ship", "Ship it", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(ctx, todo.ID, "box", "s1", "Ship", "/code/app", "claude"); err != nil {
		t.Fatal(err)
	}
	decided := func() map[string]string {
		s.waitWorkEvents()
		events, err := database.WorkEventsBetween(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), []string{ruleDecidedEvent})
		if err != nil {
			t.Fatal(err)
		}
		return ruleOwnerDecisions(events)
	}
	a := acceptAsk{Conclusion: true, Outcome: outcomeDecide, Reason: "The PR is green. What next?", Recommend: "Merge", WhyOwner: whyPriority}
	if err := s.takeRecommendation(ctx, todo.ID, a); err != nil {
		t.Fatal(err)
	}
	list, err := database.ListTodoDecisions(ctx, todo.ID, true)
	if err != nil || len(list) != 1 || list[0].Source != db.DecisionManual {
		t.Fatalf("decisions: %+v %v", list, err)
	}
	if got := decided(); len(got) != 1 || got[list[0].ID] != whyPriority {
		t.Fatalf("owner_decided = %v, want %s for %s", got, whyPriority, list[0].ID)
	}
	a.ByDefault, a.Deadline = true, time.Now()
	if err := s.takeRecommendation(ctx, todo.ID, a); err != nil {
		t.Fatal(err)
	}
	if got := decided(); len(got) != 1 {
		t.Fatalf("the hub's own call at the deadline left owner_decided: %v", got)
	}
}
