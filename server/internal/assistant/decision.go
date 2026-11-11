package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Decisions the owner can answer (#3804, docs/assistant/todos.md "Decisions the owner can answer"). A supervised task
// reviewed needs_user reaches the owner only as a decision: it names why only they can decide (why_owner), what is to
// be decided in one sentence, the option recommended, two to four options with what each leads to, and what waiting
// costs; anything less is refused and the supervisor decides, sends the work back or closes it. The hub, not the
// model, adds where the task came from and where it stands. A decision that can be undone takes its recommendation
// after decisionDefaultAfter unless the review set its own deadline (decision_deadline.go); one that cannot, or that
// other people will see, is never taken and is raised again once a day. One older than decisionRecheckAfter goes back
// to its supervisor once to confirm its facts (recheck), and a needs_user task with nothing the owner can answer goes
// back once to be restated (undecidable). Besides its options every decision takes three fixed answers that say the
// owner cannot answer it; each goes to the supervisor as a signal (owner_cannot_answer) and is counted.

// Why only the owner can decide (task_review why_owner).
const (
	whyIrreversible  = "irreversible"
	whyVisible       = "visible_to_others"
	whyPriority      = "priority"
	whyAcceptance    = "acceptance"
	whyOnlyOwnerKnow = "only_owner_knows"
)

var whyOwners = []string{whyIrreversible, whyVisible, whyPriority, whyAcceptance, whyOnlyOwnerKnow}

// The fixed answers every decision takes besides its options: the owner cannot answer it as asked.
const (
	respondDecideYourself = "decide_yourself"
	respondNotAsked       = "not_what_i_asked"
	respondExplainFirst   = "explain_first"
)

var ownerResponses = []string{respondDecideYourself, respondNotAsked, respondExplainFirst}

// The supervision signals of a decision (supervise_signals.go).
const (
	sigRecheck      = "recheck"             // a decision waited over a day: confirm its facts and re-issue it, or close it
	sigUndecidable  = "undecidable"         // a needs_user task with nothing the owner can answer: restate it, or close it
	sigOwnerCannot  = "owner_cannot_answer" // the owner answered a decision with one of ownerResponses
	workOwnerCannot = "owner_cannot_answer" // the work event that counts it (agents_routes.go)
)

var (
	// decisionDefaultAfter: a decision that can be undone and has no deadline of its own takes its recommendation after
	// this long.
	decisionDefaultAfter = 24 * time.Hour
	// decisionRecheckAfter: a decision that waited this long goes back to its supervisor once; decisionRaiseEvery: how
	// often one that is never taken by default is raised to the owner again.
	decisionRecheckAfter = 24 * time.Hour
	decisionRaiseEvery   = 24 * time.Hour
	// decisionRecheckGrace: how long the supervisor has with a recheck before the owner is raised the decision again.
	decisionRecheckGrace = time.Hour
	// decisionSettle: how long after its review (or after its ask was taken) a needs_user task may be without an ask
	// before that counts as undecidable: the ask is being written, or the answer is on its way to the agent.
	decisionSettle = 10 * time.Minute
)

// DecisionOption is one option of a decision: its label, and what choosing it leads to.
type DecisionOption struct {
	Label   string `json:"label"`
	LeadsTo string `json:"leads_to"`
}

// reviewDecision is what a needs_user review says of its decision, as checked (readDecision).
type reviewDecision struct {
	WhyOwner, Reason, Recommend, CostOfWaiting string
	Options                                    []DecisionOption
}

// neverDefault: the decision is never taken by default (it cannot be undone, or other people will see it).
func neverDefault(why string) bool { return why == whyIrreversible || why == whyVisible }

// decisionElse is what a refused needs_user is told to do instead.
const decisionElse = "When nothing needs the user, don't use needs_user: decide it yourself and send the agent on (agent_send), " +
	"send the work back (rework), or close it (accepted or cancelled). A task that only waits for something or someone is no " +
	"decision either: park it (parked, with reason, waiting_on and until), and it comes back to you at that time."

// readDecision checks a review's decision: every part there, the recommended option first among two to four. The
// error names everything missing at once, and what to do when nothing needs the owner.
func readDecision(why, reason, recommend, cost string, options []DecisionOption) (reviewDecision, error) {
	d := reviewDecision{WhyOwner: strings.TrimSpace(why), Reason: clip(oneLine(reason), 200), Recommend: clip(oneLine(recommend), 120),
		CostOfWaiting: clip(oneLine(cost), 200)}
	for _, o := range options {
		o.Label, o.LeadsTo = clip(oneLine(o.Label), 120), clip(oneLine(o.LeadsTo), 200)
		if o.Label != "" || o.LeadsTo != "" {
			d.Options = append(d.Options, o)
		}
	}
	var missing []string
	if !slices.Contains(whyOwners, d.WhyOwner) {
		missing = append(missing, "why_owner (why only the user can decide: "+strings.Join(whyOwners, ", ")+")")
	}
	if d.Reason == "" {
		missing = append(missing, "reason (one sentence: what is to be decided)")
	}
	if d.Recommend == "" {
		missing = append(missing, "recommend (the option you recommend)")
	}
	switch n := len(d.Options); {
	case n < 2 || n > 4:
		missing = append(missing, "options (2 to 4, the recommended first, each {label, leads_to})")
	case slices.ContainsFunc(d.Options, func(o DecisionOption) bool { return o.Label == "" || o.LeadsTo == "" }):
		missing = append(missing, "options (each needs a label and leads_to: what choosing it leads to)")
	case d.Recommend != "":
		i := slices.IndexFunc(d.Options, func(o DecisionOption) bool { return o.Label == d.Recommend })
		if i < 0 {
			missing = append(missing, "options (recommend must be the label of one of them)")
		} else if i > 0 { // the recommended one goes first
			o := d.Options[i]
			d.Options = slices.Insert(slices.Delete(d.Options, i, i+1), 0, o)
		}
	}
	if d.CostOfWaiting == "" {
		missing = append(missing, "cost_of_waiting (one sentence: what waiting costs)")
	}
	if len(missing) > 0 {
		return reviewDecision{}, fmt.Errorf("needs_user is refused: it is not yet a decision the user can answer. Missing: %s. %s",
			strings.Join(missing, "; "), decisionElse)
	}
	return d, nil
}

// askFrom is where a decision's task came from, as the hub knows it: the conversation that opened it (Main: the main
// conversation; Title its name, "" when it has none), when, and the owner's message that led to it (the one its brief
// froze, else the first one they typed to the task). ByAssistant: the
// to-do records no message of the owner's, so it is told as started by the assistant, not guessed.
type askFrom struct {
	Conversation string    `json:"conversation,omitempty"`
	Title        string    `json:"title,omitempty"`
	Main         bool      `json:"main,omitempty"`
	At           time.Time `json:"at"`
	Message      string    `json:"message,omitempty"`
	ByAssistant  bool      `json:"by_assistant,omitempty"`
	// StartedBy: who started it when the task's brief says so and it was not the owner (assistant, leader, routine, hub).
	StartedBy string `json:"started_by,omitempty"`
}

// decisionBackground is what the hub adds to task t's decision: where it came from, and where it stands (its latest
// agent's closeout when it wrote one, else its work line; "" when neither is known).
func (s *Service) decisionBackground(ctx context.Context, t Todo) (*askFrom, string) {
	from := &askFrom{Conversation: t.Origin, At: t.CreatedAt, ByAssistant: true}
	here := s.settings(ctx)
	if t.Origin != "" && t.Origin == here.Main {
		from.Main = true
	} else if c, ok := s.session(ctx, here.MachineID, t.Origin); ok && t.Origin != "" {
		if name := conversationName(c, ""); name != "" && strings.TrimSpace(c.Title) == name {
			from.Title = clip(oneLine(name), 60)
		}
	}
	// The task's brief (task_brief.go) froze the owner's message that led to the work when it was handed out: that is
	// where it came from. A brief without one says who started it instead; only one that does not know (a brief
	// reconstructed for an older task) leaves it to the chat's own record below.
	if brief, ok := s.briefOfTodo(t.ID); ok {
		switch {
		case brief.Request != nil && strings.TrimSpace(brief.Request.Text) != "":
			from.Message, from.At, from.ByAssistant = clip(oneLine(brief.Request.Text), 200), brief.Request.At, false
			return from, s.decisionStands(t)
		case brief.StartedBy != briefByUnknown && brief.StartedBy != briefByOwner && brief.StartedBy != "":
			from.StartedBy = brief.StartedBy
			return from, s.decisionStands(t)
		}
	}
	// The first thing the owner typed to this task: a routed item that is neither Persona's (from) nor an answer the hub
	// worded for them (relay). Items from before relays were marked cannot be told apart once the task was first
	// reviewed (a decision's answer is routed the same way), so only what came before its first review counts.
	var firstReview time.Time
	if v := t.Supervision; v != nil && len(v.Reviews) > 0 {
		firstReview = v.Reviews[0].At
	}
	if items, err := s.feedItems(ctx, t.Origin, t.CreatedAt.Add(-time.Minute)); err == nil && t.Origin != "" {
		for _, it := range items {
			var body struct {
				Text  string `json:"text"`
				From  string `json:"from"`
				Relay string `json:"relay"`
			}
			if it.Kind != feedRouted || it.Ref != t.ID || json.Unmarshal(it.Body, &body) != nil || body.From != "" || body.Relay != "" ||
				!firstReview.IsZero() && !it.At.Before(firstReview) {
				continue
			}
			if text := clip(oneLine(body.Text), 200); text != "" {
				from.Message, from.ByAssistant = text, false
				break
			}
		}
	}
	return from, s.decisionStands(t)
}

// decisionStands is where task t stands: its latest agent's closeout when it wrote one, else its work line.
func (s *Service) decisionStands(t Todo) string {
	stands := ""
	if ag := lastAgent(t); ag.SessionID != "" {
		if e, ok := s.workEntry([2]string{ag.MachineID, ag.SessionID}); ok {
			stands = e.Work.Line
			if c := e.Work.Closeout; c != nil {
				stands = cmp.Or(strings.Join(c.Did, " "), c.Missing, c.Waiting, c.Verified, stands)
			}
		}
	}
	return clip(oneLine(stands), 200)
}

// backgroundText is a decision's one line of background: where its task came from, when, and where it stands. "" for
// an ask that has none (not a decision, or one from before #3804).
func backgroundText(l i18n.Lang, loc *time.Location, a *acceptAsk) string {
	f := a.From
	if f == nil {
		return ""
	}
	where := i18n.T(l, "assistant-reports.decision_where_other")
	switch {
	case f.Main:
		where = i18n.T(l, "assistant-reports.decision_where_main")
	case f.Title != "":
		where = i18n.T(l, "assistant-reports.decision_where_named", i18n.Args{"title": f.Title})
	}
	args := i18n.Args{"where": where, "date": f.At.In(loc).Format("2006-01-02"), "message": f.Message}
	line := i18n.T(l, "assistant-reports.decision_from_assistant", args)
	if !f.ByAssistant && f.Message != "" {
		line = i18n.T(l, "assistant-reports.decision_from_owner", args)
	}
	if a.Stands != "" {
		line += " · " + i18n.T(l, "assistant-reports.decision_stands", i18n.Args{"stands": a.Stands})
	}
	return line
}

// decisionLines are what a decision adds to its push and its card below the verdict and the recommendation: the
// background, each option with what it leads to, what waiting costs, and that it is never taken by default.
func decisionLines(l i18n.Lang, loc *time.Location, a *acceptAsk) []string {
	if !decideAsk(a) {
		return nil
	}
	var out []string
	if b := backgroundText(l, loc, a); b != "" {
		out = append(out, b)
	}
	for i, o := range a.Options {
		out = append(out, i18n.T(l, "assistant-reports.decision_option", i18n.Args{"n": i + 1, "label": o.Label, "leads_to": o.LeadsTo}))
	}
	if a.CostOfWaiting != "" {
		out = append(out, i18n.T(l, "assistant-reports.decision_cost", i18n.Args{"cost": a.CostOfWaiting}))
	}
	if neverDefault(a.WhyOwner) && a.Deadline.IsZero() {
		out = append(out, i18n.T(l, "assistant-reports.decision_never_default"))
	}
	return out
}

// ---- answering ----

// errDecisionOption: the option named is none of the decision's.
var errDecisionOption = errors.New("option is not one of the decision's options")

// askRound is task id's decision still waiting when asked is its round ("" from a card of its own: the round now); nil
// when it is over or another round asks. errAcceptStore when the asks cannot be read.
func (s *Service) askRound(id, asked string) (*acceptAsk, error) {
	a, err := s.acceptAskLocked(id)
	if err != nil || a == nil {
		return nil, err
	}
	cp, ok := s.askCopy(id)
	if !ok || !decideAsk(&cp) || !cp.AcceptedAt.IsZero() {
		return nil, nil
	}
	if asked != "" {
		if at, err := time.Parse(timeKeyLayout, asked); err != nil || !cp.AskedAt.Equal(at) {
			return nil, nil
		}
	}
	if t, ok := s.todoNow(id); !ok || acceptAskEnded(&cp, t) || acceptAskStale(&cp, t) {
		return nil, nil
	}
	return &cp, nil
}

// chooseOption: the owner chose option label of task id's decision (asked: its round). The recommended one is taken as
// "OK" takes it; another is their own choice, kept in the to-do's decisions and sent to the task's agent, which ends
// the decision. False: the round is over. errDecisionOption: no such option; else acceptTry's errors, or the send's.
func (s *Service) chooseOption(ctx context.Context, id, asked, label string) (bool, error) {
	s.reviewDismissals.actions.acquire(id)
	defer s.reviewDismissals.actions.release(id)
	if t, ok := s.todoNow(id); ok {
		if dismissed, err := s.decisionDismissed(ctx, t); err != nil || dismissed {
			return false, err
		}
	}
	a, err := s.askRound(id, asked)
	if err != nil || a == nil {
		return false, err
	}
	label = strings.TrimSpace(label)
	i := slices.IndexFunc(a.Options, func(o DecisionOption) bool { return o.Label == label })
	switch {
	case label == a.Recommend && label != "":
		return s.acceptTryAsDismissalLocked(ctx, id, "", false)
	case i < 0:
		return false, errDecisionOption
	}
	t, _ := s.todoNow(id)
	l := s.botLang(ctx)
	s.deadlineAnswered(ctx, id, a.AskedAt) // their own answer: no default at its deadline, even if the send fails
	msg := i18n.T(l, "assistant-reports.decision_chosen_to_agent", i18n.Args{"option": label, "leads_to": a.Options[i].LeadsTo})
	if _, err := s.relayToTask(ctx, t.Origin, id, msg, func(cur Todo) bool {
		return cur.DoneAt == nil && !acceptAskStale(a, cur)
	}); err != nil {
		s.logf("task %s: decision to agent: %v", id, err)
		return false, err
	}
	if st := s.todoDB(); st != nil {
		at := decisionNow()
		if ok, err := s.insertDecision(ctx, st, db.TodoDecisionRow{ID: "dec_" + randomHex(8), TodoID: id,
			Question: cmp.Or(a.Reason, i18n.T(l, "assistant-reports.verdict_decide")), Choice: decisionText(label),
			Source: db.DecisionManual, CreatedAt: at}); err != nil {
			s.logf("task %s: decision: %v", id, err)
		} else if ok {
			s.decisionsChanged(ctx, id, at)
		}
	}
	s.userDecided(ctx, id)
	s.endAsk(ctx, id, a.AskedAt, "", label)
	return true, nil
}

// askCardNow: card is one of the cards of task id's ask as it is now ("" when Feishu does not say: taken as one), so a
// press on an earlier round's card answers nothing (#4853).
func (s *Service) askCardNow(id, card string) bool {
	a, ok := s.askCopy(id)
	return ok && (card == "" || card == a.MessageID || slices.Contains(a.Messages, card))
}

// endAsk marks task id's decision asked at as answered (response: the fixed answer it got, or chosen: the option
// taken that was not the recommended one), and redraws its cards.
func (s *Service) endAsk(ctx context.Context, id string, asked time.Time, response, chosen string) {
	b := &s.accept
	b.mu.Lock()
	a := b.asks[id]
	if a == nil || !a.AskedAt.Equal(asked) {
		b.mu.Unlock()
		return
	}
	a.AcceptedAt, a.Response, a.Chosen = time.Now().UTC(), response, chosen
	s.saveAcceptLocked(ctx)
	cp := *a
	b.mu.Unlock()
	s.askChanged(ctx)
	s.updateAskCards(ctx, id, cp)
}

// ownerCannotAnswer: the owner answered task id's decision (asked: its round) with one of ownerResponses instead of an
// option. Nothing is taken, whatever the decision recommends. The task's supervisor hears it once as a signal carrying
// the answer and the decision, and it is counted (a work event, summed per supervisor in the work statistics):
//
//   - decide_yourself: the supervisor decides and records its assumption. A decision that cannot be undone, or that
//     others will see, still waits for the owner (the task stays needs_user): the supervisor restates it as a yes/no.
//   - not_what_i_asked: the task's agent gets no automatic follow-up (followUpsHeld) until the supervisor reviews the
//     task again or sends its agent on.
//   - explain_first: the decision stays as it is, no longer taken by default; the supervisor re-issues it with the
//     background.
//
// False: the round is over, or response is none of them.
func (s *Service) ownerCannotAnswer(ctx context.Context, id, asked, response string) (bool, error) {
	s.reviewDismissals.actions.acquire(id)
	defer s.reviewDismissals.actions.release(id)
	if t, ok := s.todoNow(id); ok {
		if dismissed, err := s.decisionDismissed(ctx, t); err != nil || dismissed {
			return false, err
		}
	}
	if !slices.Contains(ownerResponses, response) {
		return false, nil
	}
	a, err := s.askRound(id, asked)
	if err != nil || a == nil {
		return false, err
	}
	t, _ := s.todoNow(id)
	now := time.Now().UTC()
	stays := response == respondExplainFirst || response == respondDecideYourself && neverDefault(a.WhyOwner)
	v, _, err := s.updateSupervision(ctx, id, func(v *Supervision) bool {
		if n := len(v.Reviews); n > 0 && v.Reviews[n-1].Verdict == reviewNeedsUser {
			r := &v.Reviews[n-1]
			r.OwnerResponse, r.OwnerResponseAt = response, now
			r.RestateAt = now // told with this signal: not also as undecidable
		}
		if !stays && v.State == reviewNeedsUser {
			v.State = ""
			if v.Round > 0 {
				v.State = reviewRework
			}
		}
		return true
	})
	if err != nil {
		return false, fmt.Errorf("%w: %w", errAcceptStore, err)
	}
	if response == respondExplainFirst {
		s.deadlineAnswered(ctx, id, a.AskedAt) // they asked for more first: its default is not taken meanwhile
		b := &s.accept
		b.mu.Lock()
		if cur := b.asks[id]; cur != nil && cur.AskedAt.Equal(a.AskedAt) {
			cur.Response = response
			s.saveAcceptLocked(ctx)
		}
		b.mu.Unlock()
		s.askChanged(ctx)
	} else {
		s.endAsk(ctx, id, a.AskedAt, response, "")
	}
	ag := lastAgent(t)
	s.workEvent(ag.MachineID, ag.SessionID, workOwnerCannot, map[string]any{"todo": id, "response": response,
		"supervisor": v.Supervisor, "why_owner": a.WhyOwner})
	s.logf("task %s: the owner answered its decision %q: told its supervisor", id, response)
	s.signal(superviseSignal{Todo: id, Kind: sigOwnerCannot, Key: response + "@" + a.AskedAt.UTC().Format(time.RFC3339Nano),
		Detail: response, Decision: s.signalDecision(ctx, a)})
	return true, nil
}

// signalDecision is ask a as a signal carries it.
func (s *Service) signalDecision(ctx context.Context, a *acceptAsk) *prompts.SuperviseDecision {
	d := &prompts.SuperviseDecision{WhyOwner: a.WhyOwner, Question: a.Reason, Recommend: a.Recommend, Cost: a.CostOfWaiting,
		NeverDefault: neverDefault(a.WhyOwner), Background: backgroundText(s.botLang(ctx), s.location(ctx), a)}
	for _, o := range a.Options {
		d.Options = append(d.Options, prompts.SuperviseOption{Label: o.Label, LeadsTo: o.LeadsTo})
	}
	return d
}

// followUpsHeld: the owner said the open task this session works for is not what they asked (not_what_i_asked), and
// its supervisor has not looked at it since: the hub sends its agent nothing on its own (workGuard).
func (s *Service) followUpsHeld(machine, session string) bool {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	t := s.todos.todos[s.todos.activeBySession[[2]string{machine, session}]]
	return t != nil && ownerHeld(t.Supervision)
}

// releaseHold ends the hold "This isn't what I asked for" put on task id's agent, when its supervisor sent the agent
// on without reviewing the task again.
func (s *Service) releaseHold(ctx context.Context, id string) {
	if _, _, err := s.updateSupervision(ctx, id, func(v *Supervision) bool {
		n := len(v.Reviews)
		if n == 0 || v.Reviews[n-1].OwnerResponse != respondNotAsked || v.Reviews[n-1].HoldReleased {
			return false
		}
		v.Reviews[n-1].HoldReleased = true
		return true
	}); err != nil {
		s.logf("task %s: supervision: %v", id, err)
	}
}

// ownerHeld: ledger v's latest review was answered not_what_i_asked and nothing released it since.
func ownerHeld(v *Supervision) bool {
	if v == nil || len(v.Reviews) == 0 {
		return false
	}
	r := v.Reviews[len(v.Reviews)-1]
	return r.OwnerResponse == respondNotAsked && !r.HoldReleased
}

// ---- old and unanswerable decisions ----

// sweepDecisionAges looks at the decisions still waiting: one older than decisionRecheckAfter goes back to its
// supervisor once (recheck), and one that is never taken by default is raised to the owner again every
// decisionRaiseEvery, after the supervisor had decisionRecheckGrace with its recheck.
func (s *Service) sweepDecisionAges(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, w := range s.awaitingAcceptance(ctx) {
		a := w.ask
		if !decideAsk(&a) {
			continue
		}
		// One that recommends nothing goes back as undecidable instead (undecidableSignal): not both.
		recheck := a.RecheckAt.IsZero() && a.Recommend != "" && now.Sub(a.AskedAt) >= decisionRecheckAfter
		raise := neverDefault(a.WhyOwner) && a.Response == "" && now.Sub(maxTime(a.AskedAt, a.RaisedAt)) >= decisionRaiseEvery &&
			!a.RecheckAt.IsZero() && now.Sub(a.RecheckAt) >= decisionRecheckGrace
		if !recheck && !raise {
			continue
		}
		b := &s.accept
		b.mu.Lock()
		cur := b.asks[w.todo]
		if cur == nil || !cur.AskedAt.Equal(a.AskedAt) || !cur.AcceptedAt.IsZero() {
			b.mu.Unlock()
			continue
		}
		prev := *cur
		if recheck {
			cur.RecheckAt = now.UTC()
		} else {
			cur.RaisedAt = now.UTC()
		}
		if err := s.saveAcceptErrLocked(ctx); err != nil {
			*cur = prev
			b.mu.Unlock()
			s.logf("task %s: decision: save: %v", w.todo, err)
			continue
		}
		cp := *cur
		b.mu.Unlock()
		if recheck {
			s.logf("task %s: its decision waited %s: back to its supervisor to confirm it", w.todo, humanDuration(now.Sub(a.AskedAt)))
			s.signal(superviseSignal{Todo: w.todo, Kind: sigRecheck, Key: "recheck@" + a.AskedAt.UTC().Format(time.RFC3339Nano),
				Minutes: int(now.Sub(a.AskedAt).Minutes()), Decision: s.signalDecision(ctx, &a)})
			continue
		}
		s.logf("task %s: a decision never taken by default still waits: raised again", w.todo)
		s.remindOverdue(ctx, w.todo, cp)
	}
}

// undecidableSignal: supervised task t waits for the owner (needs_user) with nothing they can answer — no decision of
// its is up, or the one up recommends nothing — and its supervisor was not told yet: the signal that hands it back, once
// (kept on its latest review, so a restart does not tell it again).
func (s *Service) undecidableSignal(ctx context.Context, t Todo, now time.Time) (superviseSignal, bool) {
	v := t.Supervision
	if v == nil || t.DoneAt != nil || v.state(t) != reviewNeedsUser || len(v.Reviews) == 0 {
		return superviseSignal{}, false
	}
	r := v.Reviews[len(v.Reviews)-1]
	if !r.RestateAt.IsZero() || now.Sub(r.At) < decisionSettle {
		return superviseSignal{}, false
	}
	a, ok := s.askCopy(t.ID)
	if ok && decideAsk(&a) && !acceptAskStale(&a, t) {
		if a.AcceptedAt.IsZero() && a.Recommend != "" || !a.AcceptedAt.IsZero() && now.Sub(a.AcceptedAt) < decisionSettle {
			return superviseSignal{}, false // answerable, or its answer is on its way to the agent
		}
	}
	if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		n := len(v.Reviews)
		if n == 0 || !v.Reviews[n-1].At.Equal(r.At) || !v.Reviews[n-1].RestateAt.IsZero() {
			return false
		}
		v.Reviews[n-1].RestateAt = now.UTC()
		return true
	}); err != nil {
		s.logf("task %s: undecidable: %v", t.ID, err)
		return superviseSignal{}, false
	}
	s.logf("task %s: waits for the owner with nothing they can answer: back to its supervisor to restate it", t.ID)
	return superviseSignal{Todo: t.ID, Kind: sigUndecidable, Key: "undecidable@" + r.At.UTC().Format(time.RFC3339Nano),
		Minutes: int(now.Sub(r.At).Minutes()), Detail: clip(oneLine(cmp.Or(r.Reason, r.Note)), 300)}, true
}

// ---- counting ----

// ownerCannotStats sums the owner_cannot_answer events by the supervisor conversation that asked, then by answer.
func ownerCannotStats(events []db.WorkEvent) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, e := range events {
		if e.Kind != workOwnerCannot {
			continue
		}
		sup, _ := e.Detail["supervisor"].(string)
		resp, _ := e.Detail["response"].(string)
		if out[sup] == nil {
			out[sup] = map[string]int{}
		}
		out[sup][resp]++
	}
	return out
}
