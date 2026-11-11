package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the assistant's task_review tool ----

func (s *Service) toolTaskReview(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID  string        `json:"todo_id"`
		Verdict string        `json:"verdict"`
		Checks  []ReviewCheck `json:"checks"`
		Note    string        `json:"note"`
		// The conclusion card's (conclusion.go): accepted's outcome (done, or cannot be done), its one sentence of why and
		// its one link; needs_user's recommended option.
		Outcome   string `json:"outcome"`
		Reason    string `json:"reason"`
		Link      string `json:"link"`
		Recommend string `json:"recommend"`
		// needs_user's deadline and whether its recommendation can be undone (#3127, decision_deadline.go).
		Deadline   string `json:"deadline"`
		Reversible *bool  `json:"reversible"`
		// accepted: the owner is still to look at the result themselves (a UI change someone has to see); without it the
		// supervisor's acceptance passes the task's "Please verify" (#3240).
		UserCheck bool `json:"user_check"`
		// needs_user's decision (#3804, decision.go): why only the owner can decide, the options (the recommended first,
		// each with what it leads to) and what waiting costs.
		WhyOwner      string           `json:"why_owner"`
		Options       []DecisionOption `json:"options"`
		CostOfWaiting string           `json:"cost_of_waiting"`
		// needs_user: the decision can wait for the next daily briefing instead of a push now (#4930, decision_wait.go).
		CanWait bool `json:"can_wait"`
		// accepted: what was done beyond the brief (#3822, task_brief_result.go).
		AlsoDone []string `json:"also_done"`
		// parked (parked.go): until when it waits (a duration or a time; reason says for what), and on what kind of thing.
		Until     string `json:"until"`
		WaitingOn string `json:"waiting_on"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	a.Note = strings.TrimSpace(a.Note)
	switch a.Verdict {
	case reviewAccepted, reviewRework, reviewNeedsUser, reviewParked, reviewCancelled:
	default:
		return "", fmt.Errorf("verdict must be accepted, rework, needs_user, parked or cancelled")
	}
	switch a.Outcome = strings.TrimSpace(a.Outcome); a.Outcome {
	case "", outcomeDone, outcomeCannot:
	default:
		return "", fmt.Errorf("outcome must be done or cannot")
	}
	if a.Verdict == reviewRework && a.Note == "" && !slices.ContainsFunc(a.Checks, func(c ReviewCheck) bool { return !c.Met }) {
		return "", fmt.Errorf("rework: say what is missing (a check that is not met, or note)")
	}
	// A needs_user is a decision the owner can answer, or it is refused (#3804). A rework past its rounds becomes one
	// too, so it may carry the decision; whether it has to is known once the task is read (below).
	var decision reviewDecision
	decided := false
	if a.Verdict == reviewNeedsUser || a.Verdict == reviewRework && (a.WhyOwner != "" || len(a.Options) > 0) {
		d, err := readDecision(a.WhyOwner, a.Reason, a.Recommend, a.CostOfWaiting, a.Options)
		if err != nil {
			return "", err
		}
		decision, decided = d, true
	}
	if a.CanWait && !decided {
		return "", fmt.Errorf("can_wait applies to needs_user only")
	}
	// A parked task says what it waits for, on what kind of thing, and until when, or it is refused (parked.go).
	var park Parked
	if a.Verdict == reviewParked {
		p, err := readPark(a.Reason, a.Until, a.WaitingOn, time.Now())
		if err != nil {
			return "", err
		}
		park = p
	} else if strings.TrimSpace(a.Until) != "" || strings.TrimSpace(a.WaitingOn) != "" {
		return "", fmt.Errorf("until and waiting_on apply to parked only")
	}
	deadline, err := parseDeadline(a.Deadline, time.Now())
	switch {
	case err != nil:
		return "", err
	case deadline.IsZero():
	case !decided:
		return "", fmt.Errorf("deadline applies to needs_user only")
	case a.Reversible == nil:
		return "", fmt.Errorf("deadline: say whether the recommendation can be undone (reversible); deleting, sending to other people, spending money and production changes cannot")
	case *a.Reversible && neverDefault(decision.WhyOwner):
		return "", fmt.Errorf("deadline: a decision whose why_owner is %s is never taken by default: reversible must be false (the user is only reminded at the deadline)", decision.WhyOwner)
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", err
	}
	t, ok := s.todoNow(strings.TrimSpace(a.TodoID))
	if !ok {
		return "", fmt.Errorf("no task %q", a.TodoID)
	}
	// The main conversation may review any supervised task: after the main conversation changes, the old tasks'
	// supervision messages come to the new one, which has to be able to close them (#4262).
	isMain := s.isMain(ctx, self)
	if t.Origin == "" || t.Origin != self && (t.Supervision == nil || t.Supervision.Supervisor != self && !isMain) {
		if !s.mayReviewForLeader(ctx, self, t) {
			return "", fmt.Errorf("task %s was not handed out from this conversation", t.ID)
		}
	}
	if t.DoneAt != nil && (a.Verdict == reviewRework || a.Verdict == reviewNeedsUser || a.Verdict == reviewParked) {
		return "", fmt.Errorf("task %s is done already (%s): hand out a new task for what is left", t.ID, cmp.Or(t.DoneReason, "closed"))
	}
	if err := restated(t, a.Verdict, decision); err != nil {
		return "", err
	}
	if a.Verdict == reviewRework && !decided && t.Supervision != nil && t.Supervision.Round >= maxReworkRounds {
		return "", reworkOverError(t.ID)
	}
	checks := make([]ReviewCheck, 0, len(a.Checks))
	for _, c := range a.Checks {
		c.Criterion, c.Evidence = clip(oneLine(c.Criterion), 200), clip(strings.TrimSpace(c.Evidence), 600)
		if c.Criterion != "" {
			checks = append(checks, c)
		}
	}
	verdict, note := a.Verdict, clip(a.Note, 2000)
	if a.UserCheck && verdict != reviewAccepted {
		return "", fmt.Errorf("user_check applies to accepted only")
	}
	// A conversation supervises what it handed out (task_plan, quietAgentTask): a task without a ledger
	// gets none here either, or its signals would go into the main conversation's supervision messages (#4262). It can still
	// be closed; rework, needs_user and parked are what supervision is for.
	ledger := t.Supervision != nil || isMain
	if !ledger && (verdict == reviewRework || verdict == reviewNeedsUser || verdict == reviewParked) {
		return "", fmt.Errorf("task %s is not supervised: send what is missing to its agent with agent_send, or ask the user in your answer", t.ID)
	}
	if verdict == reviewParked && s.agentSupervised(t) {
		// Its supervisor is a task leader's agent, whose supervision messages do not know parked_due (task_leader.go).
		return "", fmt.Errorf("task %s is supervised by a task leader agent: parking applies to tasks a conversation supervises", t.ID)
	}
	over, refused, parks := false, false, 0
	var waiting *OwnerWait // accepted while the work stopped on the owner's decision: refused, nothing written (#1277)
	judgedDone := false    // it was waiting for review: the judge had found it done
	var v Supervision
	if ledger {
		v, _, err = s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if verdict == reviewAccepted && t.DoneAt == nil && v.Waits != nil {
				w := *v.Waits
				waiting = &w
				return false
			}
			judgedDone = v.State == reviewPending
			if isMain && !s.isLeader(ctx, v.Supervisor) && !s.isLeaderRef(v.Supervisor) {
				v.Supervisor = self // the main conversation takes over an old main conversation's task, not a leader's
			} else if v.Supervisor == "" {
				v.Supervisor = t.Origin
			}
			if verdict == reviewRework && v.Round >= maxReworkRounds {
				if !decided {
					refused = true // another rework took the last round meanwhile: nothing is written
					return false
				}
				verdict, over = reviewNeedsUser, true
			}
			if verdict == reviewParked {
				// Parked again while it still is (its agent's turn would have ended that): one more in a row.
				park.Count = 1
				if prev := v.parking(); prev != nil {
					if prev.Parks > maxReparks {
						parks = prev.Parks // refused: nothing is written
						return false
					}
					park.Count = max(1, prev.Parks) + 1
				}
			}
			r := TaskReview{At: time.Now().UTC(), Verdict: verdict, Checks: checks, Note: note, UserCheck: a.UserCheck}
			v.applyChecks(checks, r.At, "supervisor:"+self)
			if verdict == reviewNeedsUser {
				r.WhyOwner, r.Reason, r.Recommend = decision.WhyOwner, decision.Reason, decision.Recommend
				r.Options, r.CostOfWaiting = decision.Options, decision.CostOfWaiting
			}
			if verdict == reviewRework {
				v.Round++
				r.Round = v.Round
			}
			if verdict == reviewParked {
				r.Reason, r.Until, r.WaitingOn, r.Parks = park.Reason, park.Until, park.WaitingOn, park.Count
			}
			v.State, v.Waits = verdict, nil // reviewed: the supervisor answered what the judge found
			v.Reviews = append(v.Reviews, r)
			if len(v.Reviews) > 20 {
				v.Reviews = v.Reviews[len(v.Reviews)-20:]
			}
			return true
		})
	}
	if err != nil {
		return "", err
	}
	if refused {
		return "", reworkOverError(t.ID)
	}
	if parks > 0 {
		return "", reparkError(t.ID, parks)
	}
	if waiting != nil {
		return "", ownerWaitError(t.ID, waiting)
	}
	if a.Verdict == reviewRework { // after the judge's done, a wrong "done" in the work stats (agents_work_wrong_done.go)
		s.workEvent("", "", reviewReworkEvent, map[string]any{"todo": t.ID, "judged_done": judgedDone})
	}
	if verdict == reviewRework || verdict == reviewNeedsUser || a.UserCheck || a.Outcome == outcomeCannot {
		s.setTaskStageKind(ctx, t.ID, "unverified", "", "", false)
	}
	b := &s.supervise
	b.mu.Lock()
	b.init()
	for _, k := range []string{sigReview, sigIdle, sigUnjudged, sigNeedsYou, sigStalled, sigOrphaned, sigOverdue, sigRecheck, sigUndecidable, sigOwnerCannot, sigParkedDue} {
		delete(b.pending, t.ID+"|"+k) // reviewed: what waited is answered
	}
	b.mu.Unlock()
	// How it ended against what was asked (task_brief_result.go): each criterion of the brief, as scope changes left them.
	var result *BriefResult
	if verdict == reviewAccepted || verdict == reviewCancelled {
		result = s.recordBriefResult(ctx, t, checks, a.AlsoDone, "", verdict)
	}
	body := reviewFeedBody{Title: t.Title, Verdict: verdict, Round: v.Round, Note: note, Checks: checks, Result: result}
	if verdict == reviewParked { // what it waits for is what the line says; an older app shows it as the note
		body.Note, body.Until, body.WaitingOn = cmp.Or(note, park.Reason), park.Until, park.WaitingOn
	}
	_, _ = s.appendFeed(ctx, t.Origin, feedReview, t.ID, body)
	if verdict != reviewNeedsUser && verdict != reviewAccepted {
		s.dropDecision(ctx, t.ID) // the review moved on: a decision still waiting on the owner is over (conclusion.go)
	}
	conclusion := conclusionInput{Outcome: cmp.Or(a.Outcome, outcomeDone), Reason: a.Reason, Link: a.Link, Note: note, Checks: checks}
	switch verdict {
	case reviewAccepted, reviewCancelled:
		reason := cmp.Or(clip(oneLine(note), 80), "验收通过")
		if verdict == reviewCancelled {
			reason = "取消：" + cmp.Or(clip(oneLine(note), 80), "不做了")
		}
		carded := false
		if t.DoneAt == nil {
			closed, err := s.reviewDoneTodo(ctx, t.ID, reason, verdict == reviewAccepted)
			if err != nil {
				return "", err
			}
			switch {
			case closed && ledger && verdict == reviewAccepted && a.UserCheck:
				// The owner is to look themselves: a "Please verify" asks them (one still waiting stays; one the assistant
				// passed when the work was deployed gives way to a new one).
				s.dropTaskReport(t.ID)
				if cur, ok := s.todoNow(t.ID); ok {
					ag := lastAgent(cur)
					s.askAcceptanceRound(cur, ag.MachineID, ag.SessionID, reviewReply(note, checks), true)
				}
			case closed && ledger && verdict == reviewAccepted:
				// The conclusion is the one push about it: its card, not a line in the task report (#1226).
				s.dropTaskReport(t.ID)
				if cur, ok := s.todoNow(t.ID); ok {
					conclusion.Verified = supervisorVerified(cur)
					carded = s.askConclusion(ctx, cur, conclusion)
				}
			case closed:
				s.taskClosedReport(t, reason) // the bot's task report says it is closed, as for the judge's (reports.go)
			}
		}
		if verdict == reviewCancelled {
			s.stopTaskAgents(ctx, t) // its cards stop asking the user (supervise_leader.go)
			return fmt.Sprintf("Task %s is cancelled and closed; its agents are being stopped and archived (their terminals stay; archived, not deleted: "+
				"agent_unarchive brings one back), and the cards they had waiting end. Tell the user only if they need to know.", t.ID), nil
		}
		against := result.words()
		if gaps := result.unsettled(); len(gaps) > 0 {
			against += " You accepted it with criteria of its brief not shown met: if one no longer applies, say so with task_scope before accepting next time."
		}
		if ledger && verdict == reviewAccepted && a.UserCheck {
			return fmt.Sprintf("Task %s is accepted and closed; its agent is closed once it stays still. A \"Please verify\" card asks the "+
				"user to look at it themselves (user_check), and it waits for their \"Pass\": don't ask them again.", t.ID) + against, nil
		}
		if ledger && verdict == reviewAccepted {
			// Checked by the supervisor: its "Please verify" is passed, not left for the owner to press (#3240).
			if ok, err := s.verifyBySupervisor(ctx, t.ID); err != nil {
				s.logf("task %s: supervisor verify: %v", t.ID, err)
			} else if ok {
				return fmt.Sprintf("Task %s is accepted and closed, and marked verified by you: its \"Please verify\" card now says so and the "+
					"user is not asked to pass it. Its agent is closed once it stays still. Tell the user the result in a line only if "+
					"they are waiting for it, and hand out or schedule anything that follows (TestFlight, closing an issue).", t.ID) + against, nil
			}
		}
		if carded {
			return fmt.Sprintf("Task %s is accepted and closed; its agent is closed once it stays still. Its conclusion card (the verdict, "+
				"why in a sentence, the link) went to the user in Lark, marked checked by you (nothing waits for them): don't push the result again. Hand out or schedule anything that "+
				"follows (deploy, TestFlight, closing an issue).", t.ID) + against, nil
		}
		return fmt.Sprintf("Task %s is accepted and closed; its agent is closed once it stays still. Tell the user the result in a line or two "+
			"if they are waiting for it, and hand out or schedule anything that follows (deploy, TestFlight, closing an issue).", t.ID) + against, nil
	case reviewNeedsUser:
		conclusion.Outcome, conclusion.Reason, conclusion.Recommend, conclusion.Decision = outcomeDecide, decision.Reason, decision.Recommend, decision
		reversible := false
		switch {
		case !deadline.IsZero():
			reversible = *a.Reversible
		case !neverDefault(decision.WhyOwner):
			// A default and a date (#3804): one that can be undone takes its recommendation after a day unless the review
			// set its own deadline.
			deadline, reversible = time.Now().Add(decisionDefaultAfter).UTC(), true
		}
		if !deadline.IsZero() {
			conclusion.Deadline, conclusion.Reversible = deadline, reversible
		}
		if over { // the note is the rework asked for, written to the agent: not the owner's question
			conclusion.Note = ""
		}
		// One that can wait is held for the next briefing: no card and no push now (#4930, decision_wait.go).
		held, notHeld := false, ""
		if a.CanWait {
			held, notHeld = s.decisionWaits(ctx, decision.WhyOwner, deadline, reversible, time.Now())
			conclusion.ForBriefing = held
		}
		carded := s.askConclusion(ctx, t, conclusion)
		if held && !s.waitsForBriefing(t.ID) { // the ask was not kept as held: nothing would push it later
			held, notHeld = false, waitNotKept
		}
		// A leader's: the main conversation hears of it too (supervise_leader.go); of one that is held, when it is pushed.
		if !held && !isMain && s.isLeader(ctx, v.Supervisor) {
			s.escalateToMain(t, v.Round, cmp.Or(conclusion.Reason, note), decision.Recommend)
		}
		// A recommendation is also pushed to their phone, whose OK takes it (todo_ask.go), with the bot on or off. The hub
		// taking the push does not say a device got it, nor that the app shows its OK: without a card the supervisor still
		// asks, and is told OK may come first.
		pushed := ""
		switch {
		case deadline.IsZero():
			pushed = " It is never taken by default (" + decision.WhyOwner + "): the hub raises it to the user again once a day, and hands it " +
				"back to you after a day to confirm its facts still hold."
		case reversible:
			pushed = fmt.Sprintf(" If the user has not answered by %s, the hub takes the recommendation by default (it goes to the agent and is "+
				"recorded on the to-do as taken at the deadline). Their answer on the card, the push or the to-do ends that; if they answer you "+
				"anywhere else (in this chat), review the task with their decision (task_review) before then, or the recommendation is still taken.",
				deadline.In(s.location(ctx)).Format(toolTimeLayout))
		default:
			pushed = fmt.Sprintf(" It cannot be undone: at the deadline (%s) the user is only reminded again, nothing is taken.",
				deadline.In(s.location(ctx)).Format(toolTimeLayout))
		}
		if held {
			was := ""
			if over {
				was = fmt.Sprintf(" was sent back %d times already: it now", maxReworkRounds)
			}
			return fmt.Sprintf("Task %s%s waits for the user (needs_user), and its decision can wait (can_wait): no push and no card went out. "+
				"It is in their Needs you now, and the next daily briefing lists it with your recommendation, where they answer it; if no "+
				"briefing reaches them with it within 24 hours, or by 11 hours before its deadline, the hub pushes it after all. Don't ask "+
				"them or push it yourself; review it once the agent stops.%s", t.ID, was, pushed), nil
		}
		if notHeld != "" {
			pushed += " can_wait was ignored (" + notHeld + "): it is pushed as usual."
		}
		if s.pushDecision(ctx, t.ID) {
			pushed += " A push with your recommendation also went to their phone; its \"OK\" takes the recommendation and sends it " +
				"to the task's agent, which ends the decision too (drop a question of yours about it then)."
		}
		if carded {
			said := "a card with what they are to decide went to them in Lark, and their answer goes to the task's agent"
			if conclusion.Recommend != "" {
				said = "a card with what they are to decide and your recommendation went to them in Lark; \"OK\" takes the " +
					"recommendation, and their answer goes to the task's agent"
			}
			if over {
				return fmt.Sprintf("Task %s was sent back %d times already: it now waits for the user (needs_user): %s. Don't ask them "+
					"again; review it once the agent stops.%s", t.ID, maxReworkRounds, said, pushed), nil
			}
			return fmt.Sprintf("Task %s waits for the user: %s. Don't ask them again; review it once the agent stops.%s", t.ID, said, pushed), nil
		}
		if over {
			return fmt.Sprintf("Task %s was sent back %d times already: it now waits for the user (needs_user). Tell the user what is "+
				"still missing and ask how to go on (ask_user or in your answer); review it again once they decide (that also ends the decision "+
				"the briefing lists for it).%s", t.ID, maxReworkRounds, pushed), nil
		}
		return fmt.Sprintf("Task %s waits for the user. Ask them (ask_user, or in your answer, pushed with notify_user if they are away); "+
			"review it again once they decide (that also ends the decision the briefing lists for it).%s", t.ID, pushed), nil
	case reviewParked:
		return parkedAnswer(t.ID, park, s.location(ctx)), nil
	}
	text := prompts.RenderSuperviseRework(prompts.SuperviseRework{Round: v.Round, Max: maxReworkRounds, Checks: reworkChecks(checks), Note: note})
	sent, err := s.sendToTask(ctx, t, text)
	if err != nil {
		return "", fmt.Errorf("recorded rework round %d, but sending it to the agent failed: %v (send it with agent_send)", v.Round, err)
	}
	return fmt.Sprintf("Rework round %d of %d is %s. When the agent stops again you get a 督办 message to review it again.", v.Round,
		maxReworkRounds, sent), nil
}

// reviewReply is what a review says of the work, as the reply a "Please verify" card's three lines are read from.
func reviewReply(note string, checks []ReviewCheck) string {
	lines := []string{note}
	for _, c := range checks {
		lines = append(lines, c.Criterion+": "+c.Evidence)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func reworkChecks(list []ReviewCheck) []prompts.SuperviseCheck {
	out := make([]prompts.SuperviseCheck, 0, len(list))
	for _, c := range list {
		out = append(out, prompts.SuperviseCheck{Criterion: c.Criterion, Met: c.Met, Evidence: c.Evidence})
	}
	return out
}

// feedReview is a review in the conversation's feed: ref the todo id, body reviewFeedBody.
const feedReview = "review"

type reviewFeedBody struct {
	Title   string        `json:"title"`
	Verdict string        `json:"verdict"`
	Round   int           `json:"round,omitempty"`
	Note    string        `json:"note,omitempty"`
	Checks  []ReviewCheck `json:"checks,omitempty"`
	// A parked review's (parked.go): until when it waits and on what kind of thing; Note says for what.
	Until     time.Time `json:"until,omitzero"`
	WaitingOn string    `json:"waiting_on,omitempty"`
	// Result: an accepted or cancelled review against the task's brief (task_brief_result.go): each criterion met,
	// not_met, unchecked or dropped, then what was done beyond it.
	Result *BriefResult `json:"result,omitempty"`
}

// reviewDoneTodo closes todo id after its review (done_by review or cancelled, with reason); false when it was done already. An
// accepted task's outcome is recorded, as when it closes by itself or the user closes it (todo_outcome.go, #2820); a
// cancelled one came to nothing and records none.
func (s *Service) reviewDoneTodo(ctx context.Context, id, reason string, accepted bool) (bool, error) {
	if t, ok := s.todoNow(id); ok && t.isConcern() {
		return false, nil // only its report closes a concern (concerns.go)
	}
	s.todos.writing.Lock() // memory takes the rows in the order the store committed them (todoBook.writing)
	now := time.Now().UTC()
	var row db.TodoRow
	if st := s.supervisionDB(); st != nil && s.todoDB() != nil {
		r, ok, err := st.ReviewDoneTodo(ctx, id, reason, accepted, now)
		if err != nil || !ok {
			s.todos.writing.Unlock()
			return false, err
		}
		row = *r
	} else {
		t, ok := s.todoNow(id)
		if !ok {
			s.todos.writing.Unlock()
			return false, db.ErrTodoNotFound
		}
		row = db.TodoRow{ID: t.ID, Title: t.Title, Notes: t.Notes, CreatedAt: t.CreatedAt, Origin: t.Origin, PlanID: t.PlanID,
			ReopenedAt: t.ReopenedAt, Quiet: t.Quiet}
		row.DoneAt, row.DoneBy, row.DoneReason, row.UpdatedAt = &now, db.DoneByReview, reason, now
		if !accepted {
			row.DoneBy = db.DoneByCancelled
		}
		t.keepPlan(&row)
	}
	var before Todo
	_, doneKeys, ok := s.setTodoRow(id, row, func(t *Todo) bool {
		if t.DoneAt == nil || s.supervisionDB() != nil {
			before = copyTodoLocked(t) // its agents as they worked on it, before done releases them
			return true
		}
		return false
	})
	s.todos.writing.Unlock()
	if ok {
		s.todoRowSet(ctx, doneKeys)
	}
	if ok && accepted && row.DoneAt != nil {
		s.goRecordTaskOutcome(before, *row.DoneAt)
	}
	if t, now := s.todoNow(id); ok && now {
		// A side conversation's task the main conversation supervised: the planning conversation hears it ended (#4669).
		go s.taskEndedNotice(context.WithoutCancel(ctx), t, accepted, reason)
	}
	return ok, nil
}

// pickTaskAgent is the agent of a task that most needs what is sent to it: one waiting for the user, then working,
// then done unseen, then the latest.
func pickTaskAgent(agents []proto.Session) proto.Session {
	rank := map[string]int{proto.StateNeedsYou: 0, proto.StateWorking: 1, proto.StateUnread: 2}
	list := slices.Clone(agents)
	sort.SliceStable(list, func(i, j int) bool {
		ri, ok := rank[list[i].State]
		if !ok {
			ri = 3
		}
		rj, ok := rank[list[j].State]
		if !ok {
			rj = 3
		}
		if ri != rj {
			return ri < rj
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	return list[0]
}

// sendToTask sends text to task t's agent (pickTaskAgent) and keeps it in the conversation's feed as Persona's
// (routedFromAssistant); an agent closed after a stretch of work is resumed first (that can take minutes: it goes on in
// the background). What it says about it: "sent to the agent" or "being sent".
func (s *Service) sendToTask(ctx context.Context, t Todo, text string) (string, error) {
	agents := s.taskAgents(ctx, t)
	if len(agents) == 0 {
		return "", errors.New("the task has no agent")
	}
	live, _ := liveTaskAgents(agents)
	if len(live) == 0 { // stopped agents are read-only (#4754): Persona's follow-ups start nothing (tasks_successor.go)
		return "", errTaskAgentsStopped
	}
	c := pickTaskAgent(live)
	if _, ok := s.device(ctx, c.MachineID); !ok {
		return "", errors.New("its machine is offline")
	}
	send := func(ctx context.Context) error {
		// The user may complete or reopen the task during session lookup or before an offline send starts.
		cur, ok := s.todoNow(t.ID)
		if !ok {
			return db.ErrTodoNotFound
		}
		if cur.DoneAt != nil || cur.ReopenedAt != nil && (t.ReopenedAt == nil || !cur.ReopenedAt.Equal(*t.ReopenedAt)) {
			return db.ErrTodoCompleted
		}
		if err := s.call(ctx, c.MachineID, DeviceSessionSend, map[string]any{"session_id": c.SessionID, "text": text}, nil); err != nil {
			return err
		}
		_, err := s.appendFeed(ctx, t.Origin, feedRouted, t.ID, map[string]string{"text": text, "from": routedFromAssistant})
		return err
	}
	if c.Status == proto.StatusOffline {
		go func() {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			if err := send(sctx); err != nil {
				s.logf("task %s: rework: %v", t.ID, err)
			}
		}()
		return "being sent: its agent was closed and is resuming first", nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	if err := send(sctx); err != nil {
		return "", err
	}
	return "sent to the agent", nil
}

// ownerWaitError refuses accepting task id while its latest stretch of work stopped on a decision only the owner can
// make (OwnerWait, #1277): closing it would drop the decision, the way a task whose drafts wait for the owner to pick one
// was once accepted as done.
func ownerWaitError(id string, w *OwnerWait) error {
	why := ""
	if w.Reason != "" {
		why = fmt.Sprintf(" (the done judge: %q)", w.Reason)
	}
	return fmt.Errorf("task %s stopped on a decision only the owner can make%s: accepting it would close the task and lose "+
		"that decision. Review it needs_user as a decision they can answer (why_owner, reason, recommend, options, "+
		"cost_of_waiting); if the owner already decided, send their answer to its agent with agent_send and review the "+
		"work it does next; or cancelled when it is no longer wanted", id, why)
}

// reworkOverError refuses a rework past maxReworkRounds that states no decision: the task goes to the owner only as a
// decision they can answer (#3804), so the supervisor states one, or closes it.
func reworkOverError(id string) error {
	return fmt.Errorf("task %s was sent back %d times already: a third rework goes to the user, and only as a decision they can "+
		"answer. Review it needs_user with why_owner, reason (what is to be decided: how it should go on), recommend, options "+
		"(2 to 4, each {label, leads_to}: one more round, taking it as it is, dropping it) and cost_of_waiting; or accept it as it "+
		"is (accepted), or end it (cancelled)", id, maxReworkRounds)
}

// restated checks a needs_user against what the owner answered the task's decision before (decision.go): after "You
// decide" the same question is not asked again, and one that cannot be undone or that others will see comes back only
// as a plain approval (two options: go ahead or not).
func restated(t Todo, verdict string, d reviewDecision) error {
	v := t.Supervision
	if verdict != reviewNeedsUser || v == nil || len(v.Reviews) == 0 {
		return nil
	}
	prev := v.Reviews[len(v.Reviews)-1]
	if prev.Verdict != reviewNeedsUser || prev.OwnerResponse != respondDecideYourself {
		return nil
	}
	if neverDefault(prev.WhyOwner) {
		if len(d.Options) != 2 {
			return fmt.Errorf("the user answered this decision \"You decide\", and it cannot be taken for them (%s): restate it as a "+
				"plain approval with exactly two options (go ahead, or not), each leads_to spelling out the consequence", prev.WhyOwner)
		}
		return nil
	}
	if strings.EqualFold(d.Reason, prev.Reason) {
		return errors.New("the user answered this decision \"You decide\": don't ask them the same thing again. Decide it, tell the " +
			"task's agent (agent_send) and say in a line what you assumed. " + decisionElse)
	}
	return nil
}
