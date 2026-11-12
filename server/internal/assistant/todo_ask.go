package assistant

import (
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The ask a to-do waits on, on the to-do itself (#2056, docs/assistant/todos.md "Waiting for the owner"): the apps list
// the to-dos, not the briefing, and a to-do whose conclusion needs the owner's decision is where they show the
// recommendation and take it with one tap (POST /api/v1/todos/{id}/accept, briefing_feed.go). It is the briefing's
// `accept[]` row (briefing_feed.go), read from the same book (awaitingAcceptance): a "Please verify" ask, or a supervised
// task's conclusion (conclusion.go). A decision that recommends an option is also pushed with that option, and the
// push's OK takes it the same way (pushDecision).

// todoAsk is a to-do's open ask, as GET /api/v1/todos carries it: the verdict line in the words of its Lark card; outcome a
// conclusion's verdict (done, cannot or decide; none for a deployed task's "Please verify"); recommend a decision's
// recommended option, which OK takes; asked_at the round, which the accept route takes.
//
// A decision also carries what makes it answerable where it is listed (#3804, decision.go): question, what is to be
// decided; why_owner, why only the owner can decide; options, the recommended first, each with what it leads to;
// cost_of_waiting; from and stands, where the task came from and where it stands, as the hub knows them, and background,
// both in one line in the request's language; deadline, and default: "taken" when the recommendation is taken at the
// deadline, "never" when it never is; responses, the fixed answers the accept route takes besides an option. A
// decision from before #3804 has none of them.
type todoAsk struct {
	Outcome   string    `json:"outcome,omitempty"`
	Verdict   string    `json:"verdict"`
	Recommend string    `json:"recommend,omitempty"`
	AskedAt   time.Time `json:"asked_at"`

	Question      string           `json:"question,omitempty"`
	WhyOwner      string           `json:"why_owner,omitempty"`
	Options       []DecisionOption `json:"options,omitempty"`
	CostOfWaiting string           `json:"cost_of_waiting,omitempty"`
	From          *askFrom         `json:"from,omitempty"`
	Stands        string           `json:"stands,omitempty"`
	Background    string           `json:"background,omitempty"`
	Deadline      *time.Time       `json:"deadline,omitempty"`
	Default       string           `json:"default,omitempty"`
	Responses     []string         `json:"responses,omitempty"`
	// Response: the fixed answer the owner already gave it while it still waits (explain_first).
	Response string `json:"response,omitempty"`
	// Suggestion: the owner gave this go-ahead for this directory three times; the decision offers a standing
	// authorization for it (rule_suggest.go, #4929).
	Suggestion *proto.RuleSuggestion `json:"suggestion,omitempty"`
	// ForBriefing: it can wait and nothing was pushed for it yet: the next briefing lists it (decision_wait.go).
	ForBriefing bool `json:"for_briefing,omitempty"`
}

// How a decision's default reads on its to-do (todoAsk.Default).
const (
	defaultTaken = "taken"
	defaultNever = "never"
)

// listedTodo is a to-do as GET /api/v1/todos lists it: with its ask while one waits (none: no `ask`, which a client that
// does not know it never reads), and, while it is open and nobody works on it, why (`orphaned`, tasks_orphan.go).
type listedTodo struct {
	Todo
	Ask            *todoAsk        `json:"ask,omitempty"`
	DecisionReview *decisionReview `json:"decision_review,omitempty"`
	Orphaned       *TodoOrphan     `json:"orphaned,omitempty"`
}

// todoAsks are the open asks by to-do id, in the request's language: the ones the briefing lists (not passed, not ended,
// not from before the to-do's latest reopen).
func (s *Service) todoAsks(ctx context.Context) map[string]todoAsk {
	l, loc := s.botLang(ctx), s.location(ctx)
	out := map[string]todoAsk{}
	for _, w := range s.awaitingAcceptance(ctx) {
		a := todoAsk{Outcome: w.ask.Outcome, Verdict: verdictText(l, &w.ask), AskedAt: w.ask.AskedAt}
		if decideAsk(&w.ask) {
			a.Recommend = w.ask.Recommend
			a.Question, a.Responses, a.Response = w.ask.Reason, ownerResponses, w.ask.Response
			a.ForBriefing = w.ask.ForBriefing
			a.WhyOwner, a.Options, a.CostOfWaiting = w.ask.WhyOwner, w.ask.Options, w.ask.CostOfWaiting
			a.From, a.Stands, a.Background = w.ask.From, w.ask.Stands, backgroundText(l, loc, &w.ask)
			a.Suggestion = s.askSuggestion(ctx, w.todo, &w.ask, l)
			switch {
			case !w.ask.Deadline.IsZero() && w.ask.Reversible && w.ask.DueAt.IsZero() && a.Recommend != "":
				d := w.ask.Deadline
				a.Deadline, a.Default = &d, defaultTaken
			case w.ask.WhyOwner != "" || !w.ask.Deadline.IsZero():
				// Never taken: it cannot be undone, others will see it, the owner asked for the background first, or its
				// default did not reach the agent.
				a.Default = defaultNever
				if d := w.ask.Deadline; !d.IsZero() && w.ask.DueAt.IsZero() {
					a.Deadline = &d
				}
			}
		}
		out[w.todo] = a
	}
	return out
}

// askChanged: a to-do's ask came up, was taken or ended: the apps read the to-dos again ({"type":"todos"}) and the board
// follows.
func (s *Service) askChanged(ctx context.Context) {
	s.event(ctx, map[string]any{"type": "todos"})
	s.boardChanged()
}

// pushDecision pushes task id's decision that waits for the owner and recommends an option (a "decide" push): what is to
// be decided and the option, in the words of its card, and what the notification's OK needs to take it (the to-do, the
// round). True when the hub took it. None for a decision without a recommendation (nothing for OK to take), for a task
// from a health conversation (the lock screen says nothing of those) or a muted one.
func (s *Service) pushDecision(ctx context.Context, id string) bool {
	sent, _ := s.pushDecisionErr(ctx, id)
	return sent
}

// pushDecisionErr is pushDecision that tells a push the hub did not take (the error: it may take it later) from one
// that is not due (false, nil).
func (s *Service) pushDecisionErr(ctx context.Context, id string) (bool, error) {
	a, ok := s.askCopy(id)
	t, found := s.todoNow(id)
	if !ok || !found || !decideAsk(&a) || a.Recommend == "" || !a.AcceptedAt.IsZero() || t.Origin == "" {
		return false, nil
	}
	here := s.settings(ctx).MachineID
	if s.muted(here, t.Origin) || s.inHealth(ctx, t.Origin) {
		return false, nil
	}
	l := s.lang(ctx)
	n := Notification{ID: "ntf_" + randomHex(8), Kind: assistantwire.NotifyDecide, Subtitle: "Persona", DeviceID: here, SessionID: t.Origin,
		Title: i18n.T(l, "assistant-reports.verdict_title", i18n.Args{"task": clip(oneLine(t.Title), 30)}),
		Body:  verdictText(l, &a) + "\n" + recommendText(l, &a),
		Todo:  &assistantwire.NotifyTodo{ID: id, AskedAt: a.AskedAt, Recommend: a.Recommend, Responses: ownerResponses}}
	for _, o := range a.Options {
		n.Todo.Options = append(n.Todo.Options, o.Label)
	}
	if d := deadlineText(l, s.location(ctx), a.Deadline, a.Reversible, time.Now()); d != "" {
		n.Body += "\n" + d
	}
	// Self-contained (#3804): where it came from and stands, what each option leads to, what waiting costs.
	if lines := decisionLines(l, s.location(ctx), &a); len(lines) > 0 {
		n.Body += "\n" + strings.Join(lines, "\n")
	}
	if err := s.hub.NotifyRequest(ctx, NotifyRequest{Notification: n}); err != nil {
		s.logf("task %s: decision push: %v", id, err)
		return false, err
	}
	return true, nil
}
