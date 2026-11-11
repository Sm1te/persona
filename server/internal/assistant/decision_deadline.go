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
	"github.com/nwverse/persona/server/internal/proto"
)

// Decision deadlines (#3127, docs/assistant/todos.md "Decision deadlines"): a decision that waits for the owner — a
// supervised task's needs_user, or an ask_user question — may carry a recommended default and a deadline, and say
// whether it can be undone. When the deadline passes with no answer, a reversible one takes the recommendation as the
// owner's OK would and records on its to-do that it was taken by default; one that cannot be undone (deleting,
// sending to other people, spending money, production changes) is never taken: the owner is reminded once and it keeps
// waiting. Without a deadline nothing changes.

const (
	// decisionDeadlineMin and decisionDeadlineMax bound a deadline from when it is set.
	decisionDeadlineMin = time.Minute
	decisionDeadlineMax = 30 * 24 * time.Hour
)

// decisionSweepEvery is how often deadlines are looked at (acceptLoop). A variable for the tests.
var decisionSweepEvery = time.Minute

// askInlineSlack is how early before its deadline an ask_user card may end and still be answered with its default by
// the tool itself (the card is set to end at the deadline). A variable for the tests.
var askInlineSlack = 5 * time.Second

// parseDeadline reads a tool's deadline: a duration from now ("2h", "90m") or an RFC 3339 time. "" is none.
func parseDeadline(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	var at time.Time
	if d, err := time.ParseDuration(raw); err == nil {
		at = now.Add(d)
	} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
		at = t
	} else {
		return time.Time{}, fmt.Errorf("deadline %q: give a duration such as 2h or 90m, or an RFC 3339 time", raw)
	}
	if d := at.Sub(now); d < decisionDeadlineMin || d > decisionDeadlineMax {
		return time.Time{}, fmt.Errorf("deadline %q: must be between 1 minute and 30 days from now", raw)
	}
	return at.UTC(), nil
}

// deadlineText is the line a decision with a deadline adds to its card and push ("" without one): until when it waits,
// and what happens then. Past the deadline, one that cannot be undone says it still waits.
func deadlineText(l i18n.Lang, loc *time.Location, deadline time.Time, reversible bool, now time.Time) string {
	if deadline.IsZero() {
		return ""
	}
	args := i18n.Args{"when": deadline.In(loc).Format("01-02 15:04")}
	switch {
	case reversible:
		return i18n.T(l, "assistant-reports.deadline_default", args)
	case now.Before(deadline):
		return i18n.T(l, "assistant-reports.deadline_irreversible", args)
	}
	return i18n.T(l, "assistant-reports.deadline_overdue", args)
}

// defaultReason is the reason a decision taken by default at deadline records, in the owner's language.
func defaultReason(l i18n.Lang, loc *time.Location, deadline time.Time) string {
	return i18n.T(l, "assistant-reports.deadline_taken_reason", i18n.Args{"when": deadline.In(loc).Format("01-02 15:04")})
}

// sweepDecisionDeadlines handles every decision whose deadline passed unanswered: the supervised tasks' and the
// questions' that became to-dos.
func (s *Service) sweepDecisionDeadlines(now time.Time) {
	s.sweepTaskDeadlines(now)
	s.sweepAskDeadlines(now)
	s.sweepDecisionAges(now) // after the deadlines: a decision taken by default is not rechecked (decision.go)
}

// goSweepDecisionDeadlines runs sweepDecisionDeadlines off acceptLoop (a send to an agent being resumed can take
// minutes), one sweep at a time: a tick that finds one still running is skipped.
func (s *Service) goSweepDecisionDeadlines(now time.Time) {
	if !s.deadlineSweeping.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.deadlineSweeping.Store(false)
		s.sweepDecisionDeadlines(now)
	}()
}

// taskDeadlineDue: decision a waits past its deadline and was not handled yet.
func taskDeadlineDue(a *acceptAsk, now time.Time) bool {
	return a != nil && decideAsk(a) && !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() && !a.Deadline.IsZero() && a.DueAt.IsZero() &&
		!now.Before(a.Deadline)
}

// sweepTaskDeadlines: a reversible decision past its deadline takes its recommendation (acceptTryAs, by default); one that
// cannot be undone is marked handled and reminded once (remindOverdue).
func (s *Service) sweepTaskDeadlines(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var due []string
	for id, a := range b.asks {
		if taskDeadlineDue(a, now) {
			due = append(due, id)
		}
	}
	b.mu.Unlock()
	slices.Sort(due)
	for _, id := range due {
		if t, ok := s.todoNow(id); ok {
			dismissed, err := s.decisionDismissed(ctx, t)
			if err != nil || dismissed {
				continue // Hidden is not answered: neither take its default nor remind it.
			}
		}
		a, ok := s.askCopy(id)
		if !ok || !taskDeadlineDue(&a, now) {
			continue
		}
		if a.Reversible && a.Recommend != "" {
			took, err := s.acceptTryAs(ctx, id, "", true)
			switch {
			case err != nil:
				// Not retried (acceptTryAs keeps the deadline handled): the owner decides instead.
				s.logf("task %s: deadline passed, the recommendation did not reach the agent: %v; the owner is reminded", id, err)
				if cur, ok := s.askCopy(id); ok {
					s.remindOverdue(ctx, id, cur)
				}
			case took:
				s.logf("task %s: deadline passed unanswered: recommendation taken by default", id)
			}
			continue
		}
		b.mu.Lock()
		cur := b.asks[id]
		if cur == nil || !cur.AskedAt.Equal(a.AskedAt) || !taskDeadlineDue(cur, now) {
			b.mu.Unlock()
			continue
		}
		cur.DueAt = now.UTC()
		if err := s.saveAcceptErrLocked(ctx); err != nil {
			cur.DueAt = time.Time{}
			b.mu.Unlock()
			s.logf("task %s: deadline: save: %v", id, err)
			continue
		}
		cp := *cur
		b.mu.Unlock()
		s.logf("task %s: deadline passed on a decision that cannot be undone: reminded, not taken", id)
		s.remindOverdue(ctx, id, cp)
	}
}

// deadlineAnswered marks the deadline of task id's decision asked at handled, because the owner answered it with a
// choice of their own: no default is taken at the deadline, whether or not that answer reaches the agent.
func (s *Service) deadlineAnswered(ctx context.Context, id string, asked time.Time) {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if a := b.asks[id]; a != nil && a.AskedAt.Equal(asked) && !a.Deadline.IsZero() && a.DueAt.IsZero() {
		a.DueAt = time.Now().UTC()
		s.saveAcceptLocked(ctx)
	}
}

// remindOverdue reminds the owner of task id's decision that cannot be undone and is past its deadline: the decision
// push again (it says it is overdue), and its card again when it had one.
func (s *Service) remindOverdue(ctx context.Context, id string, a acceptAsk) {
	s.pushDecision(ctx, id)
	t, ok := s.todoNow(id)
	if !ok || a.MessageID == "" {
		return
	}
	if f, ready := s.feishuFeatures(ctx); !ready || !f.Reports {
		return
	}
	mid, err := s.botPost(ctx, "", s.acceptCard(ctx, t, a, true), askSummary(s.botLang(ctx), &a))
	if err != nil {
		s.logf("task %s: deadline reminder: %v", id, err)
		return
	}
	b := &s.accept
	b.mu.Lock()
	if cur := b.asks[id]; cur != nil && cur.AskedAt.Equal(a.AskedAt) {
		cur.Messages = append(cur.Messages, mid)
		s.saveAcceptLocked(ctx)
	}
	b.mu.Unlock()
}

// ---- ask_user ----

// askDeadline is what an ask_user's arguments say of its deadline: when, whether it can be undone, and each question's
// recommended option (in the questions' order).
type askDeadline struct {
	At         time.Time
	Reversible bool
	Recommends []string
}

// askDeadlineArgs are ask_user's deadline arguments.
type askDeadlineArgs struct {
	Deadline   string `json:"deadline"`
	Reversible *bool  `json:"reversible"`
	Questions  []struct {
		Question  string                 `json:"question"`
		Recommend string                 `json:"recommend"`
		Options   []proto.QuestionOption `json:"options"`
	} `json:"questions"`
}

// readAskDeadline checks ask_user's deadline arguments: none, or a deadline with reversible said and every question
// recommending one of its options.
func readAskDeadline(a askDeadlineArgs, now time.Time) (askDeadline, error) {
	at, err := parseDeadline(a.Deadline, now)
	if err != nil || at.IsZero() {
		return askDeadline{}, err
	}
	if a.Reversible == nil {
		return askDeadline{}, errors.New("deadline: say whether the decision can be undone (reversible); deleting, sending to other people, spending money and production changes cannot")
	}
	d := askDeadline{At: at, Reversible: *a.Reversible}
	for _, q := range a.Questions {
		rec := strings.TrimSpace(q.Recommend)
		if !slices.ContainsFunc(q.Options, func(o proto.QuestionOption) bool { return strings.TrimSpace(o.Label) == rec }) || rec == "" {
			return askDeadline{}, fmt.Errorf("deadline: question %q needs recommend, the label of one of its options", strings.TrimSpace(q.Question))
		}
		d.Recommends = append(d.Recommends, rec)
	}
	return d, nil
}

// askDeadlineOf reads back the deadline toolAskUser wrote into ask_user ix's arguments (always an RFC 3339 time there,
// checked when it was asked); false when it has none.
func askDeadlineOf(ix proto.Interaction) (askDeadline, bool) {
	var a askDeadlineArgs
	if ix.ToolName != "ask_user" || json.Unmarshal(ix.ToolInput, &a) != nil || a.Deadline == "" || a.Reversible == nil {
		return askDeadline{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, a.Deadline)
	if err != nil {
		return askDeadline{}, false
	}
	d := askDeadline{At: at.UTC(), Reversible: *a.Reversible}
	for _, q := range a.Questions {
		d.Recommends = append(d.Recommends, strings.TrimSpace(q.Recommend))
	}
	return d, true
}

// askTodoDeadlineDue: question a's to-do is past its deadline, unanswered and not handled yet, or its default was
// being sent when the assistant stopped (ByDefault, not answered, no claim held any more).
func askTodoDeadlineDue(a askTodo, now time.Time) bool {
	if a.Deadline.IsZero() || !a.AnsweredAt.IsZero() || !a.DroppedAt.IsZero() || a.held(now) || now.Before(a.Deadline) {
		return false
	}
	return a.DueAt.IsZero() || a.ByDefault
}

// sweepAskDeadlines handles the questions' to-dos past their deadline (askTodoDue): a question whose deadline came
// after its card's wait, or one the hub restarted over.
func (s *Service) sweepAskDeadlines(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b := &s.askTodos
	b.mu.Lock()
	if !s.loadAskTodosLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var due []string
	for _, a := range b.list {
		// A sweep's period after the deadline: until then ask_user, if it still waits, answers it itself (askExpiredResult).
		if askTodoDeadlineDue(a, now) && !now.Before(a.Deadline.Add(decisionSweepEvery)) {
			due = append(due, a.TodoID)
		}
	}
	b.mu.Unlock()
	for _, id := range due {
		if t, ok := s.todoNow(id); !ok || t.DoneAt != nil {
			continue // closed by the owner meanwhile: askTodoClosed told the conversation
		}
		s.askTodoDue(ctx, id, false, now)
	}
}

// askTodoDue handles question to-do id past its deadline. Reversible: each question's recommended option is recorded
// on the to-do as taken by default, the to-do closes, and the choices go to the conversation that asked (inline: the
// caller returns them as ask_user's answer instead, the tool still waiting). Not reversible: the owner is reminded once
// and it keeps waiting. The choices taken (nil: none were).
func (s *Service) askTodoDue(ctx context.Context, id string, inline bool, now time.Time) []askChoice {
	b := &s.askTodos
	b.mu.Lock()
	if !s.loadAskTodosLocked(ctx) {
		b.mu.Unlock()
		return nil
	}
	i := slices.IndexFunc(b.list, func(a askTodo) bool { return a.TodoID == id })
	if i < 0 || !askTodoDeadlineDue(b.list[i], now) {
		b.mu.Unlock()
		return nil
	}
	prev := b.list[i]
	if prev.DueAt.IsZero() {
		b.list[i].DueAt = now.UTC()
	}
	if prev.Reversible {
		b.list[i].ClaimedAt, b.list[i].ByDefault = now.UTC(), true // claimed: an answer of the owner's now is not sent too
	}
	if err := s.saveAskTodosLocked(ctx); err != nil {
		b.list[i] = prev
		b.mu.Unlock()
		s.logf("ask_user to-do %s: deadline: save: %v", id, err)
		return nil
	}
	a := b.list[i]
	b.mu.Unlock()
	s.snapshotStale()
	t, _ := s.todoNow(id)
	l, loc := s.lang(ctx), s.location(ctx)
	if !a.Reversible {
		s.logf("ask_user to-do %s: deadline passed on a question that cannot be undone: reminded, not taken", id)
		s.push(ctx, Notification{Subtitle: "Persona", DeviceID: s.settings(ctx).MachineID,
			Title: i18n.T(l, "assistant-ask_todo.overdue_title"),
			Body:  clip(oneLine(t.Title), 120) + "\n" + deadlineText(l, loc, a.Deadline, false, now)})
		return nil
	}
	reason := defaultReason(l, loc, a.Deadline)
	var choices []askChoice
	for j, q := range a.Questions {
		if j < len(a.Recommends) {
			choices = append(choices, askChoice{Question: q, Choice: a.Recommends[j], Reason: reason})
		}
	}
	// The decisions are written once the default went (inline: it goes as the tool's answer), so a send that fails or
	// is cut off leaves none behind.
	record := func(ctx context.Context) {
		if st := s.todoDB(); st != nil {
			for _, c := range choices {
				at := decisionNow()
				// The hub's call at the deadline, not the owner's (an agent's source): not handed to askTodoDecided again.
				if ok, err := s.insertDecision(ctx, st, db.TodoDecisionRow{ID: "dec_" + randomHex(8), TodoID: id, Question: decisionText(c.Question),
					Choice: decisionText(c.Choice), Reason: reason, Source: db.DecisionAgent, CreatedAt: at}); err != nil {
					s.logf("ask_user to-do %s: default decision: %v", id, err)
				} else if ok {
					s.decisionsChanged(ctx, id, at)
				}
			}
		}
		s.settleAskClaim(ctx, id, true)
		s.closeAskTodo(ctx, id)
		s.logf("ask_user to-do %s: deadline passed unanswered: recommended options taken by default", id)
	}
	if inline {
		record(ctx)
		return choices
	}
	go func() { // the send may wait on the node
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := s.tellAsker(ctx, a, cmp.Or(t.Title, id), renderAskDefault(a, s.location(ctx), choices)); err != nil {
			// Not taken after all, and not tried again: the owner decides instead.
			s.logf("ask_user to-do %s: the default goes nowhere: %v; the owner is reminded", id, err)
			s.askDefaultFailed(ctx, id)
			s.push(ctx, Notification{Subtitle: "Persona", DeviceID: s.settings(ctx).MachineID,
				Title: i18n.T(l, "assistant-ask_todo.overdue_title"), Body: clip(oneLine(t.Title), 120)})
			return
		}
		record(ctx)
	}()
	return choices
}

// askDefaultFailed: question to-do id's default could not be sent: it is no default any more (its deadline stays
// handled) and the claim is given back, so the owner's own decision still goes.
func (s *Service) askDefaultFailed(ctx context.Context, id string) {
	b := &s.askTodos
	b.mu.Lock()
	if i := slices.IndexFunc(b.list, func(a askTodo) bool { return a.TodoID == id }); i >= 0 {
		b.list[i].ByDefault = false
	}
	b.mu.Unlock()
	s.settleAskClaim(ctx, id, false)
}

// askExpiredResult is ask_user's answer once its card expired unanswered and became question to-do t. Past a reversible
// deadline: its recommended options, taken by default. Otherwise that it waits as a to-do, saying when its options are
// taken by default (a deadline after the card's wait), or that its deadline passed and, as it cannot be undone, nothing
// was taken.
func (s *Service) askExpiredResult(ctx context.Context, ix proto.Interaction, t Todo, d askDeadline) string {
	moved := prompts.QuestionMovedToTodo{TodoID: t.ID, Title: t.Title}
	if d.At.IsZero() {
		return prompts.RenderQuestionMovedToTodo(moved)
	}
	now, loc := time.Now(), s.location(ctx)
	if now.Add(askInlineSlack).Before(d.At) { // the card's wait ended first: the sweep takes it at the deadline
		if d.Reversible {
			moved.DefaultAt = d.At.In(loc).Format("01-02 15:04")
		}
		return prompts.RenderQuestionMovedToTodo(moved)
	}
	// The card waited until the deadline (it may end a moment before it).
	choices := s.askTodoDue(ctx, t.ID, true, maxTime(now, d.At))
	if choices == nil {
		moved.Overdue = !d.Reversible
		return prompts.RenderQuestionMovedToTodo(moved)
	}
	q := prompts.QuestionDefaulted{Deadline: d.At.In(loc).Format("01-02 15:04"), TodoID: t.ID}
	for _, c := range choices {
		q.Choices = append(q.Choices, prompts.AskTodoChoice{Question: c.Question, Choice: c.Choice})
	}
	s.logf("ask_user %s: deadline passed unanswered: answered with its recommended options", ix.ID)
	return prompts.RenderQuestionDefaulted(q)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// markRecommended notes on each question's recommended option that it is the recommendation and what its deadline
// does, in the owner's language, so the card says it (the label, which is the answer, is untouched).
func markRecommended(l i18n.Lang, loc *time.Location, qs []proto.Question, d askDeadline) {
	if d.At.IsZero() {
		return
	}
	key := "assistant-ask_todo.option_recommended_default"
	if !d.Reversible {
		key = "assistant-ask_todo.option_recommended_irreversible"
	}
	note := i18n.T(l, key, i18n.Args{"when": d.At.In(loc).Format("01-02 15:04")})
	for i := range qs {
		if i >= len(d.Recommends) {
			return
		}
		qs[i].Options = slices.Clone(qs[i].Options) // the caller's arguments are not written
		for j := range qs[i].Options {
			o := &qs[i].Options[j]
			if strings.TrimSpace(o.Label) != d.Recommends[i] {
				continue
			}
			if desc := strings.TrimSpace(o.Description); desc != "" {
				o.Description = note + "\n" + desc
			} else {
				o.Description = note
			}
			break
		}
	}
}
