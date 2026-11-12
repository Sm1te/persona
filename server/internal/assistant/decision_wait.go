package assistant

import (
	"context"
	"slices"
	"time"
)

// Decisions that can wait (#4930, docs/assistant/todos.md "Decisions that can wait"): a supervisor's needs_user review
// may say its decision can wait (task_review can_wait). Such a decision is raised without a push to the owner's phone
// and without a card: it is on its to-do at once (Needs you in the apps, the board), and the next briefing with facts
// lists it under "To verify" with its recommendation, where it is answered. It is the same ask as any decision's
// (acceptAsk, ForBriefing set while it waits for a briefing), so its default, its recheck and the owner's fixed
// answers are unchanged. A decision that cannot be undone or that other people will see, and one no daily briefing
// with facts is due for in time (decisionWaits), is pushed as before. One that no briefing brought to the owner is
// pushed after all (sweepWaitingDecisions), so it is late at worst, never lost.

const (
	// decisionWaitDeadline: a decision is held only when a briefing is due at least this long before its deadline, so
	// one whose deadline is nearer than this when it is raised is always pushed.
	decisionWaitDeadline = 12 * time.Hour
	// decisionWaitNotice: a held decision that no briefing brought to the owner is pushed once its deadline is this
	// near: an hour after the latest briefing it could count on, and with this long left to answer.
	decisionWaitNotice = 11 * time.Hour
	// decisionWaitFor: a decision is held only when a briefing is due within this long, and one that no briefing
	// brought to the owner this long after it was raised is pushed.
	decisionWaitFor = 24 * time.Hour
)

// Why a decision marked can_wait is pushed all the same, as task_review's answer says it.
const (
	waitNoNeverDefault = "it cannot be undone or other people will see it"
	waitNoDeadline     = "its deadline is within 12 hours"
	waitNoBriefing     = "no daily briefing with facts is on"
	waitNoBriefingSoon = "no daily briefing is due within 24 hours"
	waitNoBriefingTime = "no daily briefing is due at least 12 hours before its deadline"
	waitNotKept        = "it could not be kept as held"
)

// decisionWaits says a decision marked can_wait is held for the briefing when raised at now: why only the owner decides
// (why), its deadline (zero: none) and whether taking its recommendation at the deadline can be undone. Else why not,
// in the words task_review answers with. The briefing has to come while the owner still has decisionWaitDeadline to
// answer: the deadline is counted from when the decision is raised, not from when they are told.
func (s *Service) decisionWaits(ctx context.Context, why string, deadline time.Time, reversible bool, now time.Time) (bool, string) {
	switch {
	case neverDefault(why) || !deadline.IsZero() && !reversible:
		return false, waitNoNeverDefault
	case !deadline.IsZero() && deadline.Sub(now) < decisionWaitDeadline:
		return false, waitNoDeadline
	}
	switch due := s.briefingDue(ctx, now); {
	case due.IsZero():
		return false, waitNoBriefing
	case due.Sub(now) > decisionWaitFor:
		return false, waitNoBriefingSoon
	case !deadline.IsZero() && due.After(deadline.Add(-decisionWaitDeadline)):
		return false, waitNoBriefingTime
	}
	return true, ""
}

// briefingDue is when the next daily routine with facts runs after now, zero when none is on: the routines the
// questions that can wait count on (laterBriefed).
func (s *Service) briefingDue(ctx context.Context, now time.Time) time.Time {
	loc := s.location(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return time.Time{}
	}
	defer sc.mu.Unlock()
	var due time.Time
	for _, t := range sc.tasks {
		if !t.Enabled || !t.Facts || t.Repeat != repeatDaily {
			continue
		}
		if at := nextRun(t, now, loc); !at.IsZero() && (due.IsZero() || at.Before(due)) {
			due = at
		}
	}
	return due
}

// waitOver says why decision a, held for a briefing since it was raised, is pushed after all at now ("": it still
// waits). briefed: a daily briefing with facts is still on.
func waitOver(a *acceptAsk, briefed bool, now time.Time) string {
	switch {
	case !briefed:
		return "no daily briefing with facts is on any more"
	case now.Sub(a.AskedAt) >= decisionWaitFor:
		return "no briefing brought it to the owner within 24 hours"
	case !a.Deadline.IsZero() && a.DueAt.IsZero() && !now.Before(a.Deadline.Add(-decisionWaitNotice)):
		return "no briefing brought it to the owner and its deadline is within 11 hours"
	}
	return ""
}

// sweepWaitingDecisions pushes each decision held for a briefing that none brought to the owner in time (waitOver).
// The hold ends once the hub took the push (or none is due: a muted or health conversation's); a push the hub did not
// take leaves it held, and the next sweep tries again. Its card is then due as when a decision is raised
// (retryAcceptance, counted from the push), and a leader's is told to the main conversation now, not when it was
// raised. One the owner hid has been seen: its wait ends without a push.
func (s *Service) sweepWaitingDecisions(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	held := map[string]acceptAsk{}
	for id, a := range b.asks {
		if heldForBriefing(a) {
			held[id] = *a
		}
	}
	b.mu.Unlock()
	if len(held) == 0 {
		return // nothing is held: the schedule is not even read
	}
	briefed := s.laterBriefed(ctx)
	ids := make([]string, 0, len(held))
	for id := range held {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		a := held[id]
		why := waitOver(&a, briefed, now)
		t, ok := s.todoNow(id)
		if why == "" || !ok || acceptAskEnded(&a, t) || acceptAskStale(&a, t) {
			continue // it still waits, or it is over: nothing to push
		}
		dismissed, err := s.decisionDismissed(ctx, t)
		if err != nil {
			continue // not known: the next sweep asks again
		}
		if dismissed {
			s.endWait(ctx, id, a.AskedAt, time.Time{})
			continue
		}
		if cur, ok := s.askCopy(id); !ok || !heldForBriefing(&cur) || !cur.AskedAt.Equal(a.AskedAt) {
			continue // answered, or a briefing brought it to the owner, while the others were pushed
		}
		if _, err := s.pushDecisionErr(ctx, id); err != nil {
			s.logf("task %s: its decision could wait, but %s: its push did not go out (tried again on the next sweep)", id, why)
			continue
		}
		if !s.endWait(ctx, id, a.AskedAt, now) {
			continue
		}
		s.logf("task %s: its decision could wait, but %s: pushed", id, why)
		if s.heldEscalates(ctx, t, &a) { // held back when it was raised (supervise_review.go)
			s.escalateToMain(t, t.Supervision.Round, a.Reason, a.Recommend)
		}
	}
}

// heldEscalates: the main conversation is told of task t's held decision a now that it is pushed: a leader conversation
// or the background supervisor raised it (RaisedBy; the background supervisor's tasks name the main conversation in
// their ledgers, so the ledger alone does not say). One kept by a manager from before RaisedBy is told when its task
// is a leader's, as it was then.
func (s *Service) heldEscalates(ctx context.Context, t Todo, a *acceptAsk) bool {
	if t.Supervision == nil {
		return false
	}
	if a.RaisedBy != "" {
		return !s.isMain(ctx, a.RaisedBy)
	}
	return s.isLeader(ctx, t.Supervision.Supervisor)
}

// heldForBriefing: a is a decision that waits for a briefing to bring it to the owner.
func heldForBriefing(a *acceptAsk) bool {
	return a != nil && decideAsk(a) && a.ForBriefing && !a.AskedAt.IsZero() && a.AcceptedAt.IsZero()
}

// waitsForBriefing: task id's decision is kept and held for a briefing.
func (s *Service) waitsForBriefing(id string) bool {
	a, ok := s.askCopy(id)
	return ok && heldForBriefing(&a)
}

// endWait marks task id's decision asked at as no longer held for a briefing; false when it is not held (answered,
// brought to the owner by a briefing meanwhile, or another round asks). pushed (zero: it was not) is when it was
// pushed after all: its card is due from then. A write that fails is logged and the hold stays ended in memory: the
// push went, and it is not sent every minute while the store is away (after a restart it goes once more).
func (s *Service) endWait(ctx context.Context, id string, asked, pushed time.Time) bool {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	cur := b.asks[id]
	if !heldForBriefing(cur) || !cur.AskedAt.Equal(asked) {
		return false
	}
	cur.ForBriefing, cur.RaisedBy = false, ""
	if !pushed.IsZero() {
		cur.PushedAt, cur.NoCard = pushed.UTC(), false
	}
	s.saveAcceptLocked(ctx)
	return true
}

// briefListed: a briefing with the rows of f reached the owner: its run ended and its finished push says its counts,
// and the run's `brief` item lists every row (briefPush). The decisions among them that were held for a briefing have
// now been put to the owner: they are not pushed later (sweepWaitingDecisions). Only the round the row was made for
// counts. The rows a briefing's Lark card shows end their hold the same way (briefAsked).
func (s *Service) briefListed(f *briefFacts) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(ctx) {
		return
	}
	changed := false
	for _, r := range f.Accept {
		if a := b.asks[r.id]; a != nil && a.ForBriefing && a.AskedAt.Equal(r.asked) {
			a.ForBriefing, changed = false, true
		}
	}
	if changed {
		s.saveAcceptLocked(ctx)
	}
}
