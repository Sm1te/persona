package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ---- scope changes and the clock (#3822) ----
//
// After a task is handed out, what done means changes in one way only: an entry in its brief's log (task_scope, or
// agent_supervise / agent_send with a task on one's own task, which go through the same code). The entry says what
// changed and why, and leaves the criteria and expected time in effect; the brief itself stays as it was written. A
// change that adds a step only the owner may allow (irreversible, or seen by other people) is refused unless the
// owner's own message started the turn that makes it, and that message is kept on the entry.
//
// The clock: a brief past max(expect_minutes × 3, a day) since it was handed out (or since its latest scope change)
// with no end raises one overdue signal to its supervisor, recorded in the log; a scope change starts the clock again.

// sigOverdue: the task's brief passed its clock with no end (briefClock).
const sigOverdue = "overdue"

// scopeRisk are the risky words text adds that were not part of the work as known: the hub's risky words (riskyWords;
// the agent's own reversible pushes aside) that text names to be done (not after a negation) and known does not.
func scopeRisk(text, known string) []string {
	text, known = strings.ToLower(text), strings.ToLower(known)
	var out []string
	for _, w := range riskyWordsIn(text) {
		if containsAny(w, ownWriteWords) || !mentionedToDo(text, w) || mentionedToDo(known, w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// scopeChange is one change to a task's scope, as applyScope takes it. DoneWhen nil keeps the criteria, Expect 0 the
// expected time. Approval: the owner's message that started the turn making the change, nil when there is none.
type scopeChange struct {
	By, Change, Why string
	DoneWhen        []string
	Expect          int
	Approval        *BriefRequest
}

// errScopeNothing: the change changes nothing (the same criteria and time, no words).
var errScopeNothing = errors.New("nothing to change")

// applyScope appends c to the log of to-do t's brief (reconstructed first when it has none) and applies the criteria
// and expected time it leaves to the ledger of every open to-do on that brief. The brief as it is then.
func (s *Service) applyScope(ctx context.Context, t Todo, c scopeChange) (TaskBrief, error) {
	if t.DoneAt != nil {
		return TaskBrief{}, fmt.Errorf("task %s is done already: its brief is closed; hand out a new task for what is left", t.ID)
	}
	brief, err := s.ensureTaskBrief(ctx, t)
	if err != nil {
		return TaskBrief{}, err
	}
	if brief.ID == "" {
		return TaskBrief{}, fmt.Errorf("task %s has no brief", t.ID)
	}
	next := brief.Criteria
	if c.DoneWhen != nil {
		next = cleanDoneWhen(c.DoneWhen)
	}
	expect := max(0, min(c.Expect, 24*60))
	var added, dropped []string
	for _, x := range next {
		if !slices.Contains(brief.Criteria, x) {
			added = append(added, x)
		}
	}
	for _, x := range brief.Criteria {
		if !slices.Contains(next, x) {
			dropped = append(dropped, x)
		}
	}
	if len(added)+len(dropped) == 0 && (expect == 0 || expect == brief.ExpectNow) && strings.TrimSpace(c.Change) == "" {
		return brief, errScopeNothing
	}
	known := strings.Join(append(append([]string{brief.Goal, t.Title, brief.Handover}, brief.DoneWhen...), brief.Criteria...), "\n")
	if brief.Request != nil {
		known += "\n" + brief.Request.Text
	}
	risk := scopeRisk(c.Change+"\n"+strings.Join(added, "\n"), known)
	if words := risk; len(words) > 0 && c.Approval == nil {
		return TaskBrief{}, fmt.Errorf("this change adds a step only the owner may allow (%s): it was not applied. Ask the owner "+
			"(ask_user, or task_review needs_user with what you recommend), and call task_scope again in the turn their answer starts",
			strings.Join(words, ", "))
	}
	scopes := 0
	for _, e := range brief.Log {
		if e.Kind == briefEventScope {
			scopes++
		}
	}
	if scopes >= briefLogMax {
		return TaskBrief{}, fmt.Errorf("task %s's scope changed %d times already: this is other work by now; close it (task_review) and hand out a new task", t.ID, scopes)
	}
	ev := BriefEvent{At: time.Now().UTC(), Kind: briefEventScope, Todo: t.ID, By: c.By, Change: clip(oneLine(c.Change), 300),
		Why: clip(oneLine(c.Why), 300), Added: added, Dropped: dropped, ExpectMinutes: expect}
	if len(added)+len(dropped) > 0 {
		ev.DoneWhen = next
		if ev.DoneWhen == nil {
			ev.DoneWhen = []string{}
		}
	}
	if len(risk) > 0 { // the owner's message that allowed it stays on the entry
		ev.Approval = c.Approval
	}
	brief, _, err = s.changeBrief(ctx, brief.ID, func(tb *taskBrief) bool {
		tb.st.Log = append(tb.st.Log, ev)
		return true
	})
	if err != nil {
		return TaskBrief{}, err
	}
	for _, id := range s.briefTodos(brief.ID) {
		o, ok := s.todoNow(id)
		// A task with no ledger gets none here: a ledger moves its notices into supervision messages (#4101).
		if !ok || o.DoneAt != nil || o.Supervision == nil {
			continue
		}
		if _, _, err := s.updateSupervision(ctx, id, func(v *Supervision) bool {
			v.DoneWhen, v.ExpectMinutes = slices.Clone(brief.Criteria), brief.ExpectNow
			return true
		}); err != nil {
			s.logf("task %s: scope: ledger: %v", id, err)
		}
	}
	s.todosChanged(ctx)
	return brief, nil
}

// rescope is what agent_supervise and agent_send with a task do to a task of conversation self that exists already:
// self supervises it, and new criteria or a new expected time (none: kept) go through the brief's log as a scope
// change made through via. Its ledger as it is then.
func (s *Service) rescope(ctx context.Context, t Todo, self, via string, doneWhen []string, expect int) (Supervision, error) {
	if len(doneWhen) > 0 || expect > 0 {
		c := scopeChange{By: self, Why: "set through " + via, Expect: expect} // the tool is named in why, which is not read for risky words
		if len(doneWhen) > 0 {
			c.DoneWhen = doneWhen
			c.Change = "done_when replaced"
		}
		if o := s.callerOrigin(ctx, self); o.Request != nil {
			c.Approval = o.Request
		}
		if _, err := s.applyScope(ctx, t, c); err != nil && !errors.Is(err, errScopeNothing) {
			return Supervision{}, err
		}
	}
	// self supervises it from now on, by the criteria in effect (a task of self's that had no ledger gets one, as before).
	brief, briefed := s.briefOfTodo(t.ID)
	v, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		same := v.Supervisor == self
		v.Supervisor = self
		if briefed && (!slices.Equal(v.DoneWhen, brief.Criteria) || v.ExpectMinutes != brief.ExpectNow) {
			v.DoneWhen, v.ExpectMinutes, same = slices.Clone(brief.Criteria), brief.ExpectNow, false
		}
		return !same
	})
	return v, err
}

// toolTaskScope is the assistant's task_scope: the supervisor of a task changes what it is to do or what done means,
// as an entry in its brief's log.
func (s *Service) toolTaskScope(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID        string    `json:"todo_id"`
		Change        string    `json:"change"`
		Why           string    `json:"why"`
		DoneWhen      *[]string `json:"done_when"`
		ExpectMinutes int       `json:"expect_minutes"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	a.Change, a.Why = strings.TrimSpace(a.Change), strings.TrimSpace(a.Why)
	if a.Change == "" || a.Why == "" {
		return "", errors.New("task_scope needs change (what changes, in a sentence the owner can read) and why")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", err
	}
	t, ok := s.todoNow(strings.TrimSpace(a.TodoID))
	if !ok {
		return "", fmt.Errorf("no task %q", a.TodoID)
	}
	if t.Origin == "" {
		return "", fmt.Errorf("%s is the user's own to-do, not a task that was handed out", t.ID)
	}
	supervisor := t.Origin
	if t.Supervision != nil && t.Supervision.Supervisor != "" {
		supervisor = t.Supervision.Supervisor
	}
	// Its supervisor only; the main conversation also for a task an earlier main conversation supervised (as task_review),
	// and the background supervisor for the tasks no leader supervises (supervise_background.go).
	if supervisor != self && !(s.isMain(ctx, self) && !s.isLeader(ctx, supervisor)) && !s.backgroundSupervises(ctx, self, t) {
		return "", fmt.Errorf("task %s is supervised by another conversation: only its supervisor changes its scope", t.ID)
	}
	c := scopeChange{By: self, Change: a.Change, Why: a.Why, Expect: a.ExpectMinutes}
	if a.DoneWhen != nil {
		c.DoneWhen = cleanDoneWhen(*a.DoneWhen)
		if len(c.DoneWhen) == 0 {
			return "", errors.New("done_when: give the criteria as they are after the change (the whole list); a task is never left with none")
		}
	}
	if o := s.callerOrigin(ctx, self); o.Request != nil {
		c.Approval = o.Request
	}
	brief, err := s.applyScope(ctx, t, c)
	if errors.Is(err, errScopeNothing) {
		return "", errors.New("nothing changes: done_when and expect_minutes are as they were")
	}
	if err != nil {
		return "", err
	}
	s.clearOverdue(t.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "Logged on task %s's brief (the owner sees it in the next digest); the brief itself is unchanged. done_when now: %s; expect_minutes %d; its clock restarts and runs to %s.",
		t.ID, criteriaWords(brief.Criteria, nil), brief.ExpectNow, brief.DueAt.In(s.location(ctx)).Format("2006-01-02 15:04"))
	b.WriteString(" The agent was not told: send it what changed with agent_send.")
	return b.String(), nil
}

// clearOverdue forgets a task's waiting overdue signal: it was answered (re-scoped or closed).
func (s *Service) clearOverdue(todoID string) {
	b := &s.supervise
	b.mu.Lock()
	b.init()
	delete(b.pending, todoID+"|"+sigOverdue)
	b.mu.Unlock()
}

// briefClock raises supervised open task t's overdue signal when its brief's clock ran out with no end: once per
// clock (the mark is on the brief, so a restart does not raise it again), and only for the to-do that carries the
// work now. A task waiting for the owner's decision or for review has reached its end for now, and a parked one waits
// on purpose (its own time brings it back, parked.go).
func (s *Service) briefClock(ctx context.Context, t Todo, now time.Time) {
	v := t.Supervision
	if v == nil || t.DoneAt != nil {
		return
	}
	if st := v.state(t); st == reviewNeedsUser || st == reviewPending || st == reviewParked {
		return
	}
	brief, ok := s.briefOfTodo(t.ID)
	if !ok || brief.Overdue != nil || now.Before(brief.DueAt) {
		return
	}
	if s.briefTip(brief.ID) != t.ID {
		return // superseded: the to-do that took over carries the clock
	}
	clock := brief.ClockAt
	_, marked, err := s.changeBrief(ctx, brief.ID, func(tb *taskBrief) bool {
		if tb.overdueAt() != nil {
			return false
		}
		tb.st.Log = append(tb.st.Log, BriefEvent{At: now.UTC(), Kind: briefEventOverdue, Todo: t.ID})
		return true
	})
	if err != nil || !marked {
		if err != nil {
			s.logf("task %s: overdue: %v", t.ID, err)
		}
		return
	}
	s.todosChanged(ctx)
	s.signal(superviseSignal{Todo: t.ID, Kind: sigOverdue, Key: brief.ID + "@" + clock.UTC().Format(time.RFC3339),
		Minutes: int(now.Sub(clock).Minutes()), At: now})
}
