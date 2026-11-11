package assistant

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Conclusion cards (#1226, docs/extensions/bots.md "Conclusions"): the owner hands out many tasks and wants one thing of
// each, its conclusion. When the main conversation's review of a supervised task gives a result (task_review accepted, or
// needs_user), the task gets one card: a verdict (done / cannot be done / needs your decision), one sentence of why and
// one link (the PR, the page, the document or the agent's conversation). A decision carries the option the supervisor
// recommends, and "OK" takes it. The conclusion is kept as the task's ask in the acceptance book, the same as a deployed
// task's "Please verify" (acceptance.go), so the briefing, the board and the web app list it the same way, with the same
// verdict line; a deployed task keeps its "Please verify" card, which says the verdict on top of its three lines.

// The verdicts of a conclusion (acceptAsk.Outcome).
const (
	outcomeDone   = "done"
	outcomeCannot = "cannot"
	outcomeDecide = "decide"
)

// decideAsk says a is a conclusion that waits for the owner's decision.
func decideAsk(a *acceptAsk) bool { return a.Conclusion && a.Outcome == outcomeDecide }

// verdictText is an ask's verdict line: the same words on its Lark card, the briefing's card and `brief` item, the board
// and the snapshot. A deployed task's ask is done, deployed.
func verdictText(l i18n.Lang, a *acceptAsk) string {
	label := i18n.T(l, "assistant-reports.verdict_"+cmp.Or(a.Outcome, outcomeDone))
	reason := a.Reason
	if !a.Conclusion {
		reason = i18n.T(l, "assistant-reports.verdict_deployed")
	}
	if reason == "" {
		return label
	}
	return i18n.T(l, "assistant-reports.verdict_line", i18n.Args{"verdict": label, "reason": reason})
}

// recommendText is a decision's recommended option as a line ("" when it is not a decision or recommends nothing).
func recommendText(l i18n.Lang, a *acceptAsk) string {
	if !decideAsk(a) || a.Recommend == "" {
		return ""
	}
	return i18n.T(l, "assistant-reports.verdict_recommend", i18n.Args{"option": a.Recommend})
}

// askSummary is the plain line a card of ask a falls back to, and what the chat list shows of it.
func askSummary(l i18n.Lang, a *acceptAsk) string {
	if a.Conclusion {
		return verdictText(l, a)
	}
	return a.Changed
}

// acceptedNote is what the bot answers under a card the owner passed.
func acceptedNote(l i18n.Lang, a *acceptAsk) string {
	switch {
	case decideAsk(a) && a.ByDefault:
		return i18n.T(l, "assistant-reports.verdict_taken_default")
	case decideAsk(a) && a.Chosen != "":
		return i18n.T(l, "assistant-reports.decision_chosen_noted", i18n.Args{"option": a.Chosen})
	case decideAsk(a) && a.Response != "":
		return i18n.T(l, "assistant-reports.decision_response_noted", i18n.Args{"response": i18n.T(l, "assistant-reports.decision_response_"+a.Response)})
	case decideAsk(a):
		return i18n.T(l, "assistant-reports.verdict_taken")
	case a.BySupervisor:
		return i18n.T(l, "assistant-reports.accepted_by_supervisor")
	case a.Conclusion:
		return i18n.T(l, "assistant-reports.verdict_acked")
	}
	return i18n.T(l, "assistant-reports.accepted")
}

// conclusionCard is a conclusion's card: the verdict line, the recommended option of a decision, the link, and the button
// ("Got it", or for a decision "Take the recommendation"); once passed, what came of it instead of the button.
func (s *Service) conclusionCard(ctx context.Context, t Todo, a acceptAsk) *feishubot.Card {
	l := s.botLang(ctx)
	title := i18n.T(l, "assistant-reports.verdict_title", i18n.Args{"task": clip(oneLine(t.Title), 30)})
	icon, color := "📌", feishubot.Green
	switch a.Outcome {
	case outcomeCannot:
		color = feishubot.Red
	case outcomeDecide:
		color = feishubot.Orange
	}
	c := feishubot.NewCard(icon+" "+title, color, verdictText(l, &a))
	// The goal, where it is now (with how far the work went out) and the evidence below (#3218).
	now := feishubot.EscapeMarkdown(verdictText(l, &a))
	if live := liveText(l, s.todoStages(ctx)[t.ID]); live != "" && a.Outcome != outcomeDecide {
		now += " · " + feishubot.EscapeMarkdown(live)
	}
	c.Markdown(i18n.T(l, "assistant-reports.goal_head", i18n.Args{"goal": feishubot.EscapeMarkdown(clip(oneLine(t.Title), 80)), "now": now}))
	if r := recommendText(l, &a); r != "" {
		c.Markdown(feishubot.EscapeMarkdown(r))
	}
	// What was asked, criterion by criterion (#3822): only once the work ended against its brief, never on a decision.
	if a.Outcome != outcomeDecide {
		if against := briefResultMarkdown(l, s.briefResultOfTodo(t)); against != "" {
			c.Markdown(against)
		}
	}
	if d := deadlineText(l, s.location(ctx), a.Deadline, a.Reversible, time.Now()); d != "" && decideAsk(&a) && a.AcceptedAt.IsZero() {
		c.Markdown(feishubot.EscapeMarkdown(d))
	}
	// Self-contained (#3804): where it came from and stands, what each option leads to, what waiting costs.
	for _, line := range decisionLines(l, s.location(ctx), &a) {
		c.Markdown(feishubot.EscapeMarkdown(line))
	}
	if a.Link != "" {
		c.Markdown(i18n.T(l, "assistant-reports.verdict_link", i18n.Args{"link": "[" + feishubot.EscapeMarkdown(clip(a.Link, 80)) + "](" + a.Link + ")"}))
	}
	if !a.AcceptedAt.IsZero() {
		return c.Note(acceptedNote(l, &a))
	}
	var bs []feishubot.Button
	hint := "assistant-reports.verdict_reply_hint"
	switch {
	case decideAsk(&a) && a.Recommend != "":
		bs = append(bs, feishubot.Btn(i18n.T(l, "assistant-reports.verdict_take"), feishubot.Primary, map[string]any{"a": "accept", "id": t.ID}))
		hint = "assistant-reports.verdict_decide_hint"
	case decideAsk(&a):
		hint = "assistant-reports.verdict_decide_hint_none"
	default:
		bs = append(bs, feishubot.Btn(i18n.T(l, "assistant-reports.verdict_ack"), feishubot.Primary, map[string]any{"a": "accept", "id": t.ID}))
	}
	if a.Link != "" {
		bs = append(bs, feishubot.LinkBtn(i18n.T(l, "assistant-reports.accept_open"), a.Link))
	}
	if len(bs) > 0 {
		c.Buttons(bs...)
	}
	if decideAsk(&a) {
		// The other options, then the fixed answers for a decision they cannot answer as it is (decision.go).
		var more []feishubot.Button
		for i, o := range a.Options {
			if i > 0 {
				more = append(more, feishubot.Btn(clip(o.Label, 20), feishubot.Default, map[string]any{"a": "accept", "id": t.ID, "opt": o.Label}))
			}
		}
		if len(more) > 0 {
			c.Buttons(more...)
		}
		var cannot []feishubot.Button
		for _, r := range ownerResponses {
			cannot = append(cannot, feishubot.Btn(i18n.T(l, "assistant-reports.decision_response_"+r), feishubot.Default,
				map[string]any{"a": "accept", "id": t.ID, "resp": r}))
		}
		c.Buttons(cannot...)
	}
	return c.Note(i18n.T(l, hint))
}

// liveText says how far a task's work went out, from its delivery (delivery.go) or else its stage; "" when not known.
func liveText(l i18n.Lang, st todoStage) string {
	if d := st.Delivery; d != nil {
		switch d.State {
		case deliveryLive:
			return i18n.T(l, "assistant-reports.live_live")
		case deliveryNone:
			return i18n.T(l, "assistant-reports.live_none")
		case deliveryPR, deliveryMerged, deliveryOff:
			return i18n.T(l, "assistant-reports.live_held_"+d.State)
		}
	}
	switch st.Stage {
	case "verify":
		return i18n.T(l, "assistant-reports.live_live")
	case "deploy":
		return i18n.T(l, "assistant-reports.live_unchecked")
	case "review":
		return i18n.T(l, "assistant-reports.live_held_pr")
	case "merge":
		return i18n.T(l, "assistant-reports.live_held_merged")
	}
	return ""
}

// conclusionInput is what a review says of a task's result, for its conclusion card.
type conclusionInput struct {
	Outcome   string // outcomeDone, outcomeCannot or outcomeDecide
	Reason    string // one sentence of why (the review's note when it gave none)
	Link      string // the link it gave ("" or not a web address: one is found, see conclusionLink)
	Recommend string // a decision's recommended option
	Note      string
	Checks    []ReviewCheck
	// Deadline and Reversible: a decision's deadline and whether it can be undone (#3127, decision_deadline.go).
	Deadline   time.Time
	Reversible bool
	// Decision: a decision's why_owner, options and cost of waiting (#3804, decision.go); the zero value for the rest.
	Decision reviewDecision
	// ForBriefing: the decision can wait and is held for the next briefing: no card now (#4930, decision_wait.go).
	ForBriefing bool
	// Verified: the supervisor checked the evidence (supervisorVerified): a done or cannot conclusion tells the owner and
	// is not left for them to acknowledge (#3240).
	Verified bool
}

// askConclusion keeps supervised task t's conclusion as its ask and posts its card when the bot can; true when the card
// went out (false: none is due, the bot could not take it yet and retryAcceptance tries it again, or no card was made).
// A later conclusion takes the place of the one before, whose cards are dead from then on (Earlier). A task whose deployed
// work asks to be verified this round gets no second card for a done one: that "Please verify" card says the verdict on
// top (a decision still asks, as its own card).
func (s *Service) askConclusion(ctx context.Context, t Todo, in conclusionInput) bool {
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return false
	}
	var earlier []string
	if old := b.asks[t.ID]; old != nil {
		if in.Outcome != outcomeDecide && (old.AskedAt.IsZero() || !old.Conclusion && !acceptAskStale(old, t)) {
			// A "Please verify" card is going out, or is up for this round: it is this conclusion. A decision is not: it
			// stands in for a "Please verify" still being written, which then gives way (postAsk, #3804).
			b.mu.Unlock()
			return false
		}
		earlier = lastOf(slices.Concat(old.Earlier, old.Messages), acceptEarlierMost)
	}
	b.asks[t.ID] = &acceptAsk{Earlier: earlier} // claimed, as askAcceptance does
	b.mu.Unlock()
	l := s.botLang(ctx)
	note, _, _ := strings.Cut(strings.TrimSpace(in.Note), "\n")
	a := acceptAsk{Conclusion: true, Outcome: in.Outcome, Reason: clip(oneLine(cmp.Or(strings.TrimSpace(in.Reason), note)), 120),
		Recommend: clip(oneLine(in.Recommend), 120), Link: s.conclusionLink(ctx, t, in), AskedAt: time.Now().UTC(), Earlier: earlier}
	if in.Outcome == outcomeDecide && a.Recommend != "" {
		a.Deadline, a.Reversible = in.Deadline, in.Reversible
	}
	if in.Outcome == outcomeDecide {
		a.WhyOwner, a.Options, a.CostOfWaiting = in.Decision.WhyOwner, in.Decision.Options, in.Decision.CostOfWaiting
		a.From, a.Stands = s.decisionBackground(ctx, t) // the hub's, not the model's: where it came from, where it stands
		a.ForBriefing = in.ForBriefing
	}
	if a.Reason == "" && in.Outcome == outcomeDone {
		a.Reason = i18n.T(l, "assistant-reports.verdict_done_default")
	}
	if in.Verified && in.Outcome != outcomeDecide {
		a.AcceptedAt, a.BySupervisor = a.AskedAt, true
	}
	if ag := lastAgent(t); ag.SessionID != "" {
		a.MachineID, a.SessionID = ag.MachineID, ag.SessionID
	}
	return s.postAsk(ctx, t, a)
}

// lastAgent is the agent that worked on t last (none: zero).
func lastAgent(t Todo) TodoAgent {
	if len(t.Agents) == 0 {
		return TodoAgent{}
	}
	return t.Agents[len(t.Agents)-1]
}

// conclusionLink is the one link of a conclusion: the one the review gave, when it is a web address; else the task's PR;
// else the first web address in the review's note and evidence; else the agent's conversation; else the task's page.
func (s *Service) conclusionLink(ctx context.Context, t Todo, in conclusionInput) string {
	if u := strings.TrimSpace(in.Link); u != "" && urlRe.FindString(u) == u {
		return u
	}
	if pr := s.todoStages(ctx)[t.ID].PR; pr != "" {
		return pr
	}
	texts := []string{in.Note}
	for _, c := range in.Checks {
		texts = append(texts, c.Evidence)
	}
	if u := urlRe.FindString(strings.Join(texts, "\n")); u != "" {
		return strings.TrimRight(u, ".,;:!?")
	}
	web := s.webBase(ctx)
	if ag := lastAgent(t); web != "" && ag.SessionID != "" {
		return sessionLink(web, ag.MachineID, ag.SessionID, false)
	}
	return todoLink(web, t.ID)
}

// dropDecision ends task id's decision still waiting, once its review moved on (rework, cancelled): it is no longer
// listed, its button takes nothing, and a reply to its card is answered "already handled".
func (s *Service) dropDecision(ctx context.Context, id string) {
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	a := b.asks[id]
	if a == nil || !decideAsk(a) || !a.AcceptedAt.IsZero() {
		b.mu.Unlock()
		return
	}
	a.AcceptedAt = time.Now().UTC()
	s.saveAcceptLocked(ctx)
	b.mu.Unlock()
	s.askChanged(ctx)
}

// askCopy is task id's ask as it is now (false: none, or a claim still going out).
func (s *Service) askCopy(id string) (acceptAsk, bool) {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(context.Background()) {
		return acceptAsk{}, false
	}
	a := b.asks[id]
	if a == nil || a.AskedAt.IsZero() {
		return acceptAsk{}, false
	}
	return *a, true
}

// takeRecommendation: the owner took decision a's recommended option for task id (or, a.ByDefault, nobody answered by its
// deadline and the hub took it, #3127): it is kept in the todo's decisions and sent to the task's agent, whose next stop
// comes back to the supervisor (userDecided). An error: it did not reach the agent.
func (s *Service) takeRecommendation(ctx context.Context, id string, a acceptAsk) error {
	t, ok := s.todoNow(id)
	if !ok {
		return db.ErrTodoNotFound
	}
	l := s.botLang(ctx)
	msg := i18n.T(l, "assistant-reports.verdict_take_to_agent", i18n.Args{"option": a.Recommend})
	row := db.TodoDecisionRow{Question: cmp.Or(a.Reason, i18n.T(l, "assistant-reports.verdict_decide")), Choice: decisionText(a.Recommend),
		Source: db.DecisionManual}
	if a.ByDefault {
		msg = i18n.T(l, "assistant-reports.verdict_default_to_agent", i18n.Args{"option": a.Recommend})
		// The hub's call at the deadline, not the owner's: an agent's source, with the reason saying so.
		row.Reason, row.Source = defaultReason(l, s.location(ctx), a.Deadline), db.DecisionAgent
	}
	if _, err := s.relayToTask(ctx, t.Origin, id, msg, func(cur Todo) bool {
		return cur.DoneAt == nil && !acceptAskStale(&a, cur)
	}); err != nil {
		s.logf("task %s: conclusion to agent: %v", id, err)
		return err // not recorded either: the decision is not made until it reaches the agent
	}
	if st := s.todoDB(); st != nil {
		at := decisionNow()
		row.ID, row.TodoID, row.CreatedAt = "dec_"+randomHex(8), id, at
		if ok, err := s.insertDecision(ctx, st, row); err != nil {
			s.logf("task %s: conclusion decision: %v", id, err)
		} else if ok {
			if !a.ByDefault {
				s.ownerDecided(id, row.ID, a.WhyOwner)
			}
			s.decisionsChanged(ctx, id, at)
		}
	}
	s.userDecided(ctx, id)
	return nil
}

// userDecided: the owner answered supervised task id's decision, and the answer went to its agent: the task no longer
// waits for them, so its agent's next stop comes back to the supervisor as any stop does (superviseJudged), whether it
// finished or not, and the supervision messages are about it again.
func (s *Service) userDecided(ctx context.Context, id string) {
	if _, _, err := s.updateSupervision(ctx, id, func(v *Supervision) bool {
		if v.State != reviewNeedsUser {
			return false
		}
		v.State = ""
		if v.Round > 0 {
			v.State = reviewRework
		}
		return true
	}); err != nil {
		s.logf("task %s: supervision: %v", id, err)
	}
}
