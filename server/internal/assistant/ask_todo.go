package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// An ask_user nobody answers becomes a to-do (#1617, docs/assistant/todos.md "Questions nobody answered"). When a card of
// the assistant's ask_user expires unanswered (only then: not a dismiss, not "replied in the chat", not the conversation
// stopping), the assistant opens a to-do for it: the question its title, the options and when and where it was asked its
// notes, no Agent and no origin: it is no task of the asking conversation (an origin is what makes a to-do one, for the
// hub and the apps alike, #1852, #1909); that conversation is in its record. It waits for the owner's call in the to-do
// list, the live snapshot and the next briefing's "Can wait" rows (later.go). The first decision the owner records on it (the apps' decision
// form, or a reply in the topic it is bound to) goes back to the asking conversation as a new message, and the to-do
// closes; closing it (or deleting it) without one tells the conversation, once, that the question was dropped.
//
// Its id is made from the card's (askTodoID), so the same card never makes two, across a restart or two assistants on
// one store. What the message needs (who asked, when, what) and whether it was told are kept in the assistant's settings
// (askTodosKey), not on the to-do: no new column.

const (
	askTodosKey = "assistant.ask_todos"
	// askTodoKeep is how long a question's record is kept after it was asked; a to-do still open after it is an ordinary
	// to-do from then on (its decision goes to no conversation).
	askTodoKeep = 30 * 24 * time.Hour
	// askTodoMax bounds the records; past it the oldest go first.
	askTodoMax = 200
	// askAnswerTool marks a queued answer to such a question (relayMsg.Tool); relayMsg.From is then the to-do id (the
	// card's id for a question asked without waiting, ask_later.go).
	askAnswerTool = "ask_answer"
	askTitleRunes = 200
	// askSnapshotMax bounds the snapshot's line (the newest ones).
	askSnapshotMax = 8
)

// askClaimHold is how long a claimed answer holds off another before it counts as lost: longer than a send takes. A
// variable for the tests.
var askClaimHold = 5 * time.Minute

// askTodo is the record of a question that became a to-do.
type askTodo struct {
	TodoID      string `json:"todo_id"`
	Interaction string `json:"interaction_id"`
	MachineID   string `json:"machine_id"`
	SessionID   string `json:"session_id"`
	// Questions are the questions as asked.
	Questions []string  `json:"questions"`
	AskedAt   time.Time `json:"asked_at"`
	// ClaimedAt is when the owner's call was claimed for the conversation and is being sent; AnsweredAt when it went,
	// DroppedAt when closing the to-do without one was told: each is told once. Zero until then. A claim that did not
	// end in AnsweredAt within askClaimHold (the assistant restarted in between) no longer holds.
	ClaimedAt  time.Time `json:"claimed_at,omitzero"`
	AnsweredAt time.Time `json:"answered_at,omitzero"`
	DroppedAt  time.Time `json:"dropped_at,omitzero"`
	// Deadline, Reversible and Recommends are the question's deadline, whether it can be undone, and each question's
	// recommended option (#3127, decision_deadline.go); DueAt is when the deadline was handled (taken by default:
	// ByDefault, or the owner reminded). Zero: no deadline.
	Deadline   time.Time `json:"deadline,omitzero"`
	Reversible bool      `json:"reversible,omitempty"`
	Recommends []string  `json:"recommends,omitempty"`
	DueAt      time.Time `json:"due_at,omitzero"`
	ByDefault  bool      `json:"by_default,omitempty"`
}

// held: an answer of a's is being sent.
func (a askTodo) held(now time.Time) bool {
	return !a.ClaimedAt.IsZero() && now.Sub(a.ClaimedAt) < askClaimHold
}

type askTodoBook struct {
	mu     sync.Mutex
	loaded bool
	list   []askTodo
	// making is held while a question's to-do is made, so two hand-overs of one expiry make one.
	making sync.Mutex
}

// askTodoPrefix starts the id of every to-do a question became.
const askTodoPrefix = "todo_ask_"

// askTodoID is the id of the to-do card ix becomes.
func askTodoID(ix proto.Interaction) string {
	return askTodoPrefix + strings.TrimPrefix(ix.ID, "ix_")
}

// isAskTodo: to-do id came from a question.
func isAskTodo(id string) bool { return strings.HasPrefix(id, askTodoPrefix) }

// askExpiredResolution: r, the end of ask_user ix, is its card expiring unanswered. The manager says so
// (proto.DismissedExpired, askOnPhone); a hub that keeps the cards itself sends a bare dismiss, which counts once the
// card's time is up.
func (s *Service) askExpiredResolution(ix proto.Interaction, r proto.Resolution) bool {
	if !r.Dismiss || r.Cancel || len(r.Answers) > 0 || r.AnsweredBy != "" {
		return false
	}
	if r.Reason == proto.DismissedExpired {
		return true
	}
	return r.Reason == "" && !s.fleetOn() && !time.Now().Before(ix.ExpiresAt)
}

// ownQuestionExpired: the assistant's question ix ended unanswered because its card expired (the history says so; with
// no history, its time is up).
func (s *Service) ownQuestionExpired(ix proto.Interaction) bool {
	if ix.ToolName != "ask_user" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	switch s.interactionOutcome(ctx, ix.ID) {
	case "expired":
		return true
	case "resolved", "dismissed", "cancelled":
		return false
	}
	// No history, or its end was not written (still "pending"): its time being up says it.
	return !time.Now().Before(ix.ExpiresAt)
}

// askExpired opens the to-do ask_user ix becomes now that its card expired (or finds the one it became already).
func (s *Service) askExpired(ctx context.Context, ix proto.Interaction) (Todo, error) {
	b := &s.askTodos
	b.making.Lock()
	defer b.making.Unlock()
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return Todo{}, err
	}
	id := askTodoID(ix)
	var questions []string
	for _, q := range ix.Questions {
		questions = append(questions, strings.TrimSpace(q.Question))
	}
	rec := askTodo{TodoID: id, Interaction: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID, Questions: questions, AskedAt: ix.CreatedAt}
	if d, ok := askDeadlineOf(ix); ok {
		rec.Deadline, rec.Reversible, rec.Recommends = d.At, d.Reversible, d.Recommends
	}
	if err := s.keepAskTodo(ctx, rec); err != nil {
		return Todo{}, fmt.Errorf("keep its record: %w", err) // a to-do whose answer could go nowhere after a restart is not made
	}
	if t, ok := s.todoNow(id); ok {
		return t, nil
	}
	title := clip(oneLine(questions[0]), askTitleRunes)
	t, err := s.newTodoAs(ctx, id, title, s.askTodoNotes(ctx, ix), "", "", false) // no origin: it is no task
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" { // another assistant on the store made it a moment ago
		return Todo{ID: id, Title: title}, nil
	}
	if err != nil {
		return Todo{}, err
	}
	if _, queued, _ := s.laterQueue(ctx, id, ix.MachineID, ix.SessionID, questionLine(ix), time.Now()); !queued {
		s.logf("ask_user %s: to-do %s is not in the briefing's queue (it is full or can't be written)", ix.ID, id)
	}
	s.logf("ask_user %s: expired unanswered, now to-do %s", ix.ID, id)
	return t, nil
}

// questionLine is what a briefing row quotes of ix: its questions.
func questionLine(ix proto.Interaction) string {
	var qs []string
	for _, q := range ix.Questions {
		qs = append(qs, strings.TrimSpace(q.Question))
	}
	return strings.Join(qs, " / ")
}

// askTodoNotes are the notes of the to-do ix becomes, in the owner's language: where and when it was asked, each question
// with its options as written (so a "(Recommended)" stays), and how to answer.
func (s *Service) askTodoNotes(ctx context.Context, ix proto.Interaction) string {
	l := s.lang(ctx)
	var b strings.Builder
	b.WriteString(i18n.T(l, "assistant-ask_todo.notes_asked", i18n.Args{"conversation": ix.SessionTitle,
		"when": ix.CreatedAt.In(s.location(ctx)).Format("01-02 15:04")}))
	for _, q := range ix.Questions {
		b.WriteString("\n\n")
		if len(ix.Questions) > 1 {
			b.WriteString(i18n.T(l, "assistant-ask_todo.notes_question", i18n.Args{"question": strings.TrimSpace(q.Question)}))
			b.WriteString("\n")
		}
		key := "assistant-ask_todo.notes_options"
		if q.MultiSelect {
			key = "assistant-ask_todo.notes_options_multi"
		}
		b.WriteString(i18n.T(l, key))
		for _, o := range q.Options {
			b.WriteString("\n")
			if d := strings.TrimSpace(o.Description); d != "" {
				b.WriteString(i18n.T(l, "assistant-ask_todo.notes_option_described", i18n.Args{"label": strings.TrimSpace(o.Label), "description": d}))
			} else {
				b.WriteString(i18n.T(l, "assistant-ask_todo.notes_option", i18n.Args{"label": strings.TrimSpace(o.Label)}))
			}
		}
	}
	b.WriteString("\n\n")
	b.WriteString(i18n.T(l, "assistant-ask_todo.notes_how"))
	return b.String()
}

// ---- the records ----

// loadAskTodosLocked reads the records once; s.askTodos.mu is held. A failed read leaves them unloaded.
func (s *Service) loadAskTodosLocked(ctx context.Context) bool {
	b := &s.askTodos
	if b.loaded {
		return true
	}
	var list []askTodo
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, askTodosKey, &list); err != nil {
			s.logf("ask_user to-dos: load: %v", err)
			return false
		}
	}
	b.list, b.loaded = list, true
	return true
}

// saveAskTodosLocked writes the records, the ones past askTodoKeep (and past askTodoMax) dropped first.
func (s *Service) saveAskTodosLocked(ctx context.Context) error {
	b := &s.askTodos
	now := time.Now()
	b.list = slices.DeleteFunc(b.list, func(a askTodo) bool { return now.Sub(a.AskedAt) > askTodoKeep })
	if n := len(b.list) - askTodoMax; n > 0 {
		slices.SortStableFunc(b.list, func(x, y askTodo) int { return x.AskedAt.Compare(y.AskedAt) })
		b.list = slices.Delete(b.list, 0, n)
	}
	st := s.store()
	if st == nil {
		return nil
	}
	list := b.list
	if list == nil {
		list = []askTodo{}
	}
	return st.PutSetting(ctx, askTodosKey, list)
}

// keepAskTodo records rec unless its to-do has a record already.
func (s *Service) keepAskTodo(ctx context.Context, rec askTodo) error {
	b := &s.askTodos
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAskTodosLocked(ctx) {
		return errors.New("the records can't be read")
	}
	if slices.ContainsFunc(b.list, func(a askTodo) bool { return a.TodoID == rec.TodoID }) {
		return nil
	}
	b.list = append(b.list, rec)
	if err := s.saveAskTodosLocked(ctx); err != nil {
		b.list = slices.DeleteFunc(b.list, func(a askTodo) bool { return a.TodoID == rec.TodoID })
		return err
	}
	return nil
}

// askTodoOf is the record of to-do id when it came from a question.
func (s *Service) askTodoOf(ctx context.Context, id string) (askTodo, bool) {
	if !isAskTodo(id) {
		return askTodo{}, false
	}
	b := &s.askTodos
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAskTodosLocked(ctx) {
		return askTodo{}, false
	}
	i := slices.IndexFunc(b.list, func(a askTodo) bool { return a.TodoID == id })
	if i < 0 {
		return askTodo{}, false
	}
	return b.list[i], true
}

// claimAskTodo claims the telling of to-do id's question (answered: the owner's call, else that it was dropped), written
// before it is told; false when it is no such to-do, it was answered (or an answer is being sent), or, for a drop, that
// was told already.
func (s *Service) claimAskTodo(ctx context.Context, id string, answered bool) (askTodo, bool) {
	if !isAskTodo(id) {
		return askTodo{}, false
	}
	b := &s.askTodos
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAskTodosLocked(ctx) {
		return askTodo{}, false
	}
	i := slices.IndexFunc(b.list, func(a askTodo) bool { return a.TodoID == id })
	now := time.Now()
	if i < 0 || !b.list[i].AnsweredAt.IsZero() || b.list[i].held(now) || !answered && !b.list[i].DroppedAt.IsZero() {
		return askTodo{}, false
	}
	prev := b.list[i]
	if answered {
		b.list[i].ClaimedAt = now.UTC()
	} else {
		b.list[i].DroppedAt = now.UTC()
	}
	if err := s.saveAskTodosLocked(ctx); err != nil {
		s.logf("ask_user to-do %s: save: %v", id, err)
		if j := slices.IndexFunc(b.list, func(a askTodo) bool { return a.TodoID == id }); j >= 0 {
			b.list[j] = prev
		}
		return askTodo{}, false
	}
	s.snapshotStale() // the turn the answer starts must not read "still waiting for the user's call"
	return prev, true
}

// settleAskClaim ends an answer's claim on to-do id: sent (it was answered), or given back (it could not go anywhere).
func (s *Service) settleAskClaim(ctx context.Context, id string, sent bool) {
	b := &s.askTodos
	b.mu.Lock()
	defer b.mu.Unlock()
	if i := slices.IndexFunc(b.list, func(a askTodo) bool { return a.TodoID == id }); i >= 0 {
		if sent {
			b.list[i].AnsweredAt = time.Now().UTC()
		}
		b.list[i].ClaimedAt = time.Time{}
		if err := s.saveAskTodosLocked(ctx); err != nil {
			s.logf("ask_user to-do %s: save: %v", id, err)
		}
	}
}

// ---- telling the conversation ----

// askChoice is one call of the owner's as the message gives it.
type askChoice struct{ Question, Choice, Reason string }

// renderAskAnswer is the message to the conversation that asked a: the owner's choices (thread: said in the to-do's
// topic), or that the to-do was closed without one (dropped). When it was asked is given in loc.
func renderAskAnswer(a askTodo, loc *time.Location, choices []askChoice, thread, dropped bool) string {
	m := prompts.AskTodoAnswer{When: a.AskedAt.In(loc).Format("01-02 15:04"), TodoID: a.TodoID, Thread: thread, Dropped: dropped,
		Several: len(a.Questions) > 1}
	for _, q := range a.Questions {
		m.Questions = append(m.Questions, contextText(q))
	}
	for _, c := range choices {
		m.Choices = append(m.Choices, prompts.AskTodoChoice{Question: contextText(c.Question), Choice: contextText(c.Choice), Reason: contextText(c.Reason)})
	}
	return prompts.RenderAskTodoAnswer(m)
}

// renderAskDefault is the message to the conversation that asked a when nobody decided by its deadline and its
// recommended options were taken by default (#3127).
func renderAskDefault(a askTodo, loc *time.Location, choices []askChoice) string {
	m := prompts.AskTodoAnswer{When: a.AskedAt.In(loc).Format("01-02 15:04"), TodoID: a.TodoID, Defaulted: true,
		Deadline: a.Deadline.In(loc).Format("01-02 15:04"), Several: len(a.Questions) > 1}
	for _, q := range a.Questions {
		m.Questions = append(m.Questions, contextText(q))
	}
	for _, c := range choices {
		m.Choices = append(m.Choices, prompts.AskTodoChoice{Question: contextText(c.Question), Choice: contextText(c.Choice)})
	}
	return prompts.RenderAskTodoAnswer(m)
}

// askTarget is where a's message goes: the conversation that asked, else (its node says it is gone) the main
// conversation of now. A node that can't be reached is no answer: the message waits in that conversation's queue.
func (s *Service) askTarget(ctx context.Context, a askTodo) (machine, to string, err error) {
	var res struct {
		OK bool `json:"ok"`
	}
	if err := s.call(ctx, a.MachineID, DeviceSessionGet, map[string]any{"session_id": a.SessionID}, &res); err != nil || res.OK {
		return a.MachineID, a.SessionID, nil
	}
	if here := s.settings(ctx); here.Main != "" && here.MachineID != "" {
		return here.MachineID, here.Main, nil
	}
	return "", "", fmt.Errorf("conversation %s is gone and there is no main conversation", short(a.SessionID))
}

// tellAsker puts text into the conversation a came from: now, or queued behind its turn (the queue conversation_send
// uses, kept across a restart).
func (s *Service) tellAsker(ctx context.Context, a askTodo, title, text string) error {
	return s.relayToAsker(ctx, a, "待办「"+title+"」", text, false) // i18n-ignore: a label of the relay queue, as a task notice's
}

// relayToAsker is tellAsker with the queue's label for the message given (from): a question asked without waiting has
// no to-do to name (ask_later.go; a.TodoID is then its card's id). owner: text is the answer the owner just gave on
// that card, which their presence in the conversation does not hold (relayMsg.Owner).
func (s *Service) relayToAsker(ctx context.Context, a askTodo, from, text string, owner bool) error {
	machine, to, err := s.askTarget(ctx, a)
	if err != nil {
		return err
	}
	msg := relayMsg{From: a.TodoID, FromTitle: from, Tool: askAnswerTool, Text: text, At: time.Now().UTC(), Owner: owner}
	c, ok := s.session(ctx, machine, to)
	if !ok || c.Status == proto.StatusWorking || c.Starting || s.relayWaits(to, msg) {
		if s.queueRelay(to, msg) == 0 {
			return errors.New("the relay queue can't be read")
		}
		s.publish(ctx) // shown waiting above the composer
		if ok && c.Status != proto.StatusWorking && !c.Starting {
			s.drainRelays(machine, []proto.Session{c})
		}
		return nil
	}
	if err := s.deliverRelay(ctx, machine, to, msg); err != nil {
		if s.queueRelay(to, msg) == 0 {
			return fmt.Errorf("%v, and the relay queue can't be read", err)
		}
		s.logf("ask_user %s: %v; queued", a.TodoID, err)
		s.publish(ctx)
	}
	return nil
}

// askTodoDecided: decision row was just written by hand on its to-do (the apps' form). When the to-do came from a
// question, is open and was not answered yet, the call is claimed now; it goes to the conversation that asked, and the
// to-do closes, in the background (the send may wait on the node: the request that wrote the decision does not).
func (s *Service) askTodoDecided(ctx context.Context, row db.TodoDecisionRow) {
	t, ok := s.todoNow(row.TodoID)
	if !ok || t.DoneAt != nil {
		return
	}
	a, ok := s.claimAskTodo(ctx, row.TodoID, true)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		text := renderAskAnswer(a, s.location(ctx), []askChoice{{Question: row.Question, Choice: row.Choice, Reason: row.Reason}}, false, false)
		if err := s.tellAsker(ctx, a, t.Title, text); err != nil {
			s.logf("ask_user to-do %s: the decision goes nowhere: %v", row.TodoID, err)
			s.settleAskClaim(ctx, row.TodoID, false)
			return
		}
		s.settleAskClaim(ctx, row.TodoID, true)
		s.closeAskTodo(ctx, row.TodoID)
	}()
}

// askTodoTopicAnswer is the message a reply in the topic of to-do t hands its conversation when t came from a question
// not answered yet: the claim is made (ok) and the caller sends it, then settles the claim (settleAskClaim) and, sent,
// closes the to-do.
func (s *Service) askTodoTopicAnswer(ctx context.Context, t Todo, text string) (string, bool) {
	if t.DoneAt != nil {
		return "", false
	}
	a, ok := s.claimAskTodo(ctx, t.ID, true)
	if !ok {
		return "", false
	}
	return renderAskAnswer(a, s.location(ctx), []askChoice{{Choice: text}}, true, false), true
}

// askTodoClosed: to-do id was closed or deleted by the owner. When it came from a question not answered, the conversation
// that asked is told, once, that it was dropped.
func (s *Service) askTodoClosed(ctx context.Context, id, title string) {
	a, ok := s.claimAskTodo(ctx, id, false)
	if !ok {
		return
	}
	go func() { // the send may wait on the node: the request that closed it does not
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := s.tellAsker(ctx, a, title, renderAskAnswer(a, s.location(ctx), nil, false, true)); err != nil {
			s.logf("ask_user to-do %s: closing it goes nowhere: %v", id, err)
		}
	}()
}

// closeAskTodo closes to-do id once its question's answer went to the conversation, as the owner's PATCH done would.
func (s *Service) closeAskTodo(ctx context.Context, id string) {
	s.todos.writing.Lock()
	cur, ok := s.todoNow(id)
	if !ok || cur.DoneAt != nil {
		s.todos.writing.Unlock()
		return
	}
	now := time.Now().UTC()
	var row *db.TodoRow
	if st := s.todoDB(); st != nil {
		done := true
		var err error
		if row, err = st.UpdateTodo(ctx, id, nil, nil, &done, now); err != nil {
			s.todos.writing.Unlock()
			s.logf("ask_user to-do %s: close: %v", id, err)
			return
		}
	} else {
		row = &db.TodoRow{ID: cur.ID, Title: cur.Title, Notes: cur.Notes, DoneAt: &now, CreatedAt: cur.CreatedAt, UpdatedAt: now,
			Origin: cur.Origin, PlanID: cur.PlanID, Quiet: cur.Quiet, ReopenedAt: cur.ReopenedAt}
		cur.keepPlan(row)
	}
	_, doneKeys, applied := s.setTodoRow(id, *row, nil)
	s.todos.writing.Unlock()
	if applied {
		s.todoRowSet(ctx, doneKeys)
	}
}

// snapshotAsked are the open to-dos that came from questions, for the live snapshot, the oldest first.
func (s *Service) snapshotAsked(ctx context.Context) []prompts.SnapshotAsked {
	b := &s.askTodos
	b.mu.Lock()
	if !s.loadAskTodosLocked(ctx) {
		b.mu.Unlock()
		return nil
	}
	list := slices.Clone(b.list)
	b.mu.Unlock()
	loc := s.location(ctx)
	var out []prompts.SnapshotAsked
	for _, a := range list {
		t, ok := s.todoNow(a.TodoID)
		if !ok || t.DoneAt != nil || !a.ClaimedAt.IsZero() || !a.AnsweredAt.IsZero() || !a.DroppedAt.IsZero() {
			continue // over, or its answer is on its way (the to-do closes once it went)
		}
		out = append(out, prompts.SnapshotAsked{TodoID: a.TodoID, Question: clip(oneLine(strings.Join(a.Questions, " / ")), 160),
			At: a.AskedAt.In(loc)})
	}
	slices.SortStableFunc(out, func(x, y prompts.SnapshotAsked) int { return x.At.Compare(y.At) })
	if len(out) > askSnapshotMax {
		out = out[len(out)-askSnapshotMax:]
	}
	return out
}
