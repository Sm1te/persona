package assistant

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ask_user without holding the turn (#4928, docs/assistant/assistant.md "Asking without holding the turn"). With
// wait false the card goes up exactly as a waiting ask_user's does (the same interaction, push, channels, expiry and
// deadline), the tool returns at once, and whatever ends the card is told to the asking conversation as one message
// that quotes the questions: the owner's answers, that the card was closed unanswered, or that it expired (it then
// becomes a to-do as any expired ask_user does, ask_todo.go, and the owner's later call takes that path).
//
// Nothing waits in memory for the answer, so what a manager needs to take it after a restart is kept in the
// assistant's settings (askLaterKey): the card as raised, and, once it ended, the message, so a send that failed says
// the same again. The card is resumable: a restart does not cancel it (db ExpireStaleInteractions), and the next
// manager puts it back on the phone from the record (loadAskLater) without a second push. The owner writing in the
// conversation does not take it back (withdrawAskUser): no turn is held behind it. Their words may have answered it
// all the same, so that they wrote is kept (askLaterSpoke), and a card that then runs out unanswered makes no to-do
// and takes no default: the conversation is told and asks again if it still needs the answer.

const (
	askLaterKey = "assistant.ask_later"
	// askLaterMax is how many questions asked without waiting may be out at once; one more is refused.
	askLaterMax = 20
)

// askLaterKeep is how long after its card's time is up a question whose end could not be told is still tried; past
// it the record is dropped. A variable for the tests.
var askLaterKeep = 24 * time.Hour

// askLater is the record of a question asked without waiting, until its conversation was told how it ended.
type askLater struct {
	Interaction proto.Interaction `json:"interaction"`
	// Message is what the conversation is told, set once the card ended; EndedAt is when.
	Message string    `json:"message,omitempty"`
	EndedAt time.Time `json:"ended_at,omitzero"`
	// SpokeAt is when the owner first wrote in the asking conversation while the card was up (askLaterSpoke).
	SpokeAt time.Time `json:"spoke_at,omitzero"`
}

type askLaterBook struct {
	mu sync.Mutex
	// loaded: list was read from the settings. restored: the questions a manager before this one left were put back
	// among the questions out (loadAskLater); reading the list for a new question does not do that.
	loaded, restored bool
	list             []askLater
}

// askLaterStart puts ix up without waiting for its answer: its record first (a card whose answer could reach nobody
// after a restart is not raised), then the card. It returns what the tool says.
func (s *Service) askLaterStart(ctx context.Context, ix proto.Interaction) (string, error) {
	ix.Resumable = true // it outlives a manager restart and its node's connection: no call waits on it
	ix = s.ownCard(ctx, ix)
	s.loadAskLater(ctx) // the ones a manager before this one left go back first: this read must not stand in for theirs
	b := &s.askLaters
	b.mu.Lock()
	if !s.loadAskLaterLocked(ctx) {
		b.mu.Unlock()
		return "", errors.New("the question could not be kept, so it was not asked; ask again, or without wait false")
	}
	if len(b.list) >= askLaterMax {
		b.mu.Unlock()
		return "", fmt.Errorf("not asked: %d questions asked with wait false still wait for the user (at most %d)", len(b.list), askLaterMax)
	}
	b.list = append(b.list, askLater{Interaction: ix})
	if err := s.saveAskLaterLocked(ctx); err != nil {
		b.list = slices.DeleteFunc(b.list, func(a askLater) bool { return a.Interaction.ID == ix.ID })
		b.mu.Unlock()
		return "", fmt.Errorf("the question could not be kept (%v), so it was not asked; ask again, or without wait false", err)
	}
	b.mu.Unlock()
	s.questions.mu.Lock()
	if s.questions.m == nil {
		s.questions.m = map[string]*question{}
	}
	s.questions.m[ix.ID] = &question{ix: ix, later: true}
	s.questions.mu.Unlock()
	s.publish(context.WithoutCancel(ctx))
	s.logf("ask_user %s: asked without waiting (%s)", ix.ID, short(ix.SessionID))
	return prompts.RenderQuestionAskedLater(prompts.QuestionAskedLater{ID: ix.ID,
		Until: ix.ExpiresAt.In(s.location(ctx)).Format("01-02 15:04")}), nil
}

// loadAskLater puts the questions asked without waiting that the last manager left back among the questions out, once
// (a failed read is tried again with the next snapshot): the snapshot reports them, so their cards are claimed again
// (waitOwnedQuestion), or the end they came to meanwhile is replayed from the history.
func (s *Service) loadAskLater(ctx context.Context) {
	b := &s.askLaters
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.restored || !s.loadAskLaterLocked(ctx) {
		return
	}
	b.restored = true
	s.questions.mu.Lock()
	defer s.questions.mu.Unlock()
	for _, a := range b.list {
		if s.questions.m == nil {
			s.questions.m = map[string]*question{}
		}
		if s.questions.m[a.Interaction.ID] == nil {
			s.questions.m[a.Interaction.ID] = &question{ix: a.Interaction, later: true}
		}
	}
}

// loadAskLaterLocked reads the records once; s.askLaters.mu is held. A failed read leaves them unloaded.
func (s *Service) loadAskLaterLocked(ctx context.Context) bool {
	b := &s.askLaters
	if b.loaded {
		return true
	}
	var list []askLater
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, askLaterKey, &list); err != nil {
			s.logf("ask_user without waiting: load: %v", err)
			return false
		}
	}
	b.list, b.loaded = list, true
	return true
}

// saveAskLaterLocked writes the records; s.askLaters.mu is held.
func (s *Service) saveAskLaterLocked(ctx context.Context) error {
	st := s.store()
	if st == nil {
		return nil
	}
	list := s.askLaters.list
	if list == nil {
		list = []askLater{}
	}
	return st.PutSetting(ctx, askLaterKey, list)
}

// askLaterSpoke notes that the owner wrote in session while its questions asked without waiting were up (UserSpoke).
// The cards stay up and can still be answered; one that runs out unanswered after this is told as such
// (renderAskLater), since what the owner wrote may have been the answer.
func (s *Service) askLaterSpoke(ctx context.Context, session string) {
	if session == "" {
		return
	}
	b := &s.askLaters
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAskLaterLocked(ctx) {
		return
	}
	marked := false
	for i := range b.list {
		if a := &b.list[i]; a.Interaction.SessionID == session && a.SpokeAt.IsZero() && a.Message == "" {
			a.SpokeAt, marked = time.Now().UTC(), true
		}
	}
	if !marked {
		return
	}
	if err := s.saveAskLaterLocked(context.WithoutCancel(ctx)); err != nil {
		s.logf("ask_user without waiting: keep that the user wrote in %s: %v", short(session), err) // kept in memory
	}
}

// askLaterSpokeOf: the owner wrote in the asking conversation while the question asked without waiting on card id
// was up.
func (s *Service) askLaterSpokeOf(id string) bool {
	b := &s.askLaters
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.ContainsFunc(b.list, func(a askLater) bool { return a.Interaction.ID == id && !a.SpokeAt.IsZero() })
}

// askLaterEnded takes r, the end of q's card (a question asked without waiting), and tells the conversation that
// asked, once: the question is out until the message went (or waits in the conversation's queue), so an end that could
// not be told is handed over again (keepAnswer, askOnPhone) and says the same.
func (s *Service) askLaterEnded(ctx context.Context, q *question, r proto.Resolution) error {
	ix := q.ix
	s.questions.mu.Lock()
	if q.telling || s.questions.m[ix.ID] != q {
		s.questions.mu.Unlock()
		return nil // being told, or told already
	}
	q.telling = true
	s.questions.mu.Unlock()
	told := false
	defer func() {
		s.questions.mu.Lock()
		q.telling = false
		if told {
			delete(s.questions.m, ix.ID)
		}
		s.questions.mu.Unlock()
		if told {
			s.publish(context.WithoutCancel(ctx))
		}
	}()
	ctx = context.WithoutCancel(ctx)
	text, ok := s.askLaterMessage(ctx, ix, r)
	if !ok {
		return errors.New("the question's record can't be read or written")
	}
	a := askTodo{TodoID: ix.ID, MachineID: ix.MachineID, SessionID: ix.SessionID}
	title := "提问「" + clip(oneLine(questionLine(ix)), 40) + "」" // i18n-ignore: a label of the relay queue, as a task notice's
	// The owner's answer goes in though they are talking in the conversation (relayMsg.Owner); a card that closed or
	// ran out is the system's to tell and waits for them as any late answer does (relay_presence.go).
	answered := !r.Cancel && !r.Dismiss && len(r.Answers) > 0
	if err := s.relayToAsker(ctx, a, title, text, answered); err != nil {
		if time.Since(ix.ExpiresAt) < askLaterKeep {
			return err
		}
		s.logf("ask_user %s: how it ended goes nowhere (%v) and its card's time is long up: dropped", ix.ID, err)
	}
	told = true
	b := &s.askLaters
	b.mu.Lock()
	b.list = slices.DeleteFunc(b.list, func(a askLater) bool { return a.Interaction.ID == ix.ID })
	if err := s.saveAskLaterLocked(ctx); err != nil {
		s.logf("ask_user %s: forget its record: %v", ix.ID, err) // a later manager finds the card ended and tells it again
	}
	b.mu.Unlock()
	return nil
}

// askLaterMessage is the message for r, the end of ix's card: the one kept when the card ended before, else made now
// and kept (an expiry makes its to-do here). false when the record can't be read or written.
func (s *Service) askLaterMessage(ctx context.Context, ix proto.Interaction, r proto.Resolution) (string, bool) {
	b := &s.askLaters
	find := func() int {
		return slices.IndexFunc(b.list, func(a askLater) bool { return a.Interaction.ID == ix.ID })
	}
	b.mu.Lock()
	if !s.loadAskLaterLocked(ctx) {
		b.mu.Unlock()
		return "", false
	}
	if i := find(); i >= 0 && b.list[i].Message != "" {
		defer b.mu.Unlock()
		return b.list[i].Message, true
	}
	b.mu.Unlock()
	text := s.renderAskLater(ctx, ix, r)
	b.mu.Lock()
	defer b.mu.Unlock()
	i := find()
	if i < 0 { // a manager without its record (no store): told from memory
		return text, true
	}
	b.list[i].Message, b.list[i].EndedAt = text, time.Now().UTC()
	if err := s.saveAskLaterLocked(ctx); err != nil {
		s.logf("ask_user %s: keep how it ended: %v", ix.ID, err)
		b.list[i].Message, b.list[i].EndedAt = "", time.Time{}
		return "", false
	}
	return text, true
}

// renderAskLater writes the message for r, the end of ix's card.
func (s *Service) renderAskLater(ctx context.Context, ix proto.Interaction, r proto.Resolution) string {
	m := prompts.AskLater{When: ix.CreatedAt.In(s.location(ctx)).Format("01-02 15:04")}
	for _, q := range ix.Questions {
		m.Questions = append(m.Questions, contextText(q.Question))
	}
	switch {
	case r.Cancel:
	case !r.Dismiss && len(r.Answers) > 0:
		for _, q := range ix.Questions {
			if ans, ok := r.Answers[q.Question]; ok {
				m.Answers = append(m.Answers, prompts.AskTodoChoice{Question: contextText(q.Question), Choice: contextText(ans)})
			}
		}
		m.Missing = len(m.Answers) < len(ix.Questions)
	case s.askLaterExpired(ix, r) && s.askLaterSpokeOf(ix.ID):
		// The owner wrote in the conversation while the card was up, which may have been their answer (a waiting
		// question is taken back at that point): no to-do, and no default taken at a deadline.
		m.Spoke = true
		s.logf("ask_user %s: ran out unanswered after the user wrote in the conversation: no to-do", ix.ID)
	case s.askLaterExpired(ix, r):
		t, err := s.askExpired(ctx, ix)
		if err != nil {
			s.logf("ask_user %s: expired, and no to-do could be made: %v", ix.ID, err)
			break // told as closed unanswered
		}
		d, _ := askDeadlineOf(ix)
		m.Expired = strings.TrimSpace(s.askExpiredResult(ctx, ix, t, d))
	}
	return prompts.RenderAskLater(m)
}

// askLaterExpired: r, the end of ix (asked without waiting), is its card expiring unanswered. As a waiting question's
// (askExpiredResolution), and a bare dismiss once its time is up: a card whose time ran out while no manager was
// there to expire it is cancelled by the next one (db ExpireStaleInteractions), which is an expiry all the same.
func (s *Service) askLaterExpired(ix proto.Interaction, r proto.Resolution) bool {
	if s.askExpiredResolution(ix, r) {
		return true
	}
	return r.Dismiss && r.Reason == "" && r.AnsweredBy == "" && len(r.Answers) == 0 && !time.Now().Before(ix.ExpiresAt)
}
