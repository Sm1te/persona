package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// Asking Persona during an attended meeting (POST /api/v1/recordings/{id}/ask, docs/assistant/meeting-minutes.md
// "Attending a meeting"): every question is kept in the recording's live log (kb.LiveAsk), so a retry with the same id
// is answered from it and never makes a second answer. It is answered in the background by one model call (purpose
// meeting.answer, no tools) whose input is the meeting material and nothing else (kb.MeetingMaterial, "What the hub
// gives the answering model"); the request waits askHold for it. A question pending in a live log that this process
// is not answering was being answered by one that is gone: it reads as failed, interrupted (settleAsksLocked), and
// asking it again tries it again.

const purposeMeetingAnswer = "meeting.answer"

// A question's states, how it was asked, and why it failed (the contract's; the errors are an open set).
const (
	askPending  = "pending"
	askAnswered = "answered"
	askFailed   = "failed"

	askTyped = "typed"
	askHeard = "heard"

	askModelUnavailable = "model_unavailable"
	askModelError       = "model_error"
	askModelTimeout     = "model_timeout"
	askInterrupted      = "interrupted"
	askNothingHeard     = "nothing_heard"
)

// Limits of the questions.
const (
	maxAsks             = 100 // in one meeting
	maxAsksAnswering    = 2   // being answered at once in one meeting
	maxAskQuestionRunes = 1000
	maxAnswerRunes      = 400
	heardSeconds        = 45.0 // a question not typed is what was said this long before the tap
)

// The questions' times; tests shorten them.
var (
	askHold     = 20 * time.Second // the request waits this long for its answer, then answers 202 pending
	askLiveWait = 15 * time.Second // a heard question waits this long for the live sentences up to the tap
	askTimeout  = 3 * time.Minute  // an answer not there by then failed: model_timeout
	askLivePoll = 250 * time.Millisecond
)

var (
	errOpIDReused = errors.New("this id was used for another question")
	errAsksFull   = errors.New("this meeting has all the questions it can keep")
	errAskBusy    = errors.New("two questions of this meeting are being answered")
	errAskGone    = errors.New("the question was replaced or deleted")
)

// meetingAsks are the questions this process is answering: by recording id and question id, a channel closed once the
// answer (or the failure) is stored. mu is held while the questions of a live log are read and changed (before the
// knowledge base's own live lock), so one id never starts twice and a question being answered is never taken for an
// interrupted one.
type meetingAsks struct {
	mu      sync.Mutex
	running map[string]map[string]chan struct{}
}

// finished: the attempt at question askID of recording id that done belongs to stored its outcome. Its slot is
// dropped only while it still holds done: a failed question asked again meanwhile (after the outcome was stored, before
// this) holds the retry's channel there, which must stay or the retry would read as interrupted.
func (b *meetingAsks) finished(id, askID string, done chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running[id][askID] != done {
		return
	}
	delete(b.running[id], askID)
	if len(b.running[id]) == 0 {
		delete(b.running, id)
	}
}

// askView is a question as the routes answer it: kb.LiveAsk without the hub's own fields.
type askView struct {
	ID             string         `json:"id"`
	Question       string         `json:"question"`
	QuestionSource string         `json:"question_source"`
	At             *float64       `json:"at,omitempty"`
	State          string         `json:"state"`
	Answer         string         `json:"answer"`
	Lang           string         `json:"lang"`
	Speak          bool           `json:"speak"`
	Sources        []kb.AskSource `json:"sources"`
	Error          string         `json:"error,omitempty"`
	AskedAt        time.Time      `json:"asked_at"`
	AnsweredAt     *time.Time     `json:"answered_at,omitempty"`
	// Room meetings (meeting_room_ask.go): how it is answered once its first attempt went out, and a heard
	// question's lines.
	Via   string           `json:"via,omitempty"`
	Heard []kb.LiveSegment `json:"heard,omitempty"`
}

// viewAsk is a as the routes show it: an answer and sources only once answered, an error only once failed.
func viewAsk(a kb.LiveAsk) askView {
	v := askView{ID: a.ID, Question: a.Question, QuestionSource: a.QuestionSource, At: a.At, State: a.State, Lang: a.Lang,
		Speak: a.Speak, Sources: []kb.AskSource{}, AskedAt: a.AskedAt.UTC(), Via: a.Via, Heard: a.Heard}
	switch a.State {
	case askAnswered:
		v.Answer, v.Sources, v.AnsweredAt = a.Answer, append(v.Sources, a.Sources...), a.AnsweredAt
	case askFailed:
		v.Error = a.Error
	}
	return v
}

// askRequest is a POST …/ask body: the question's id (lowercase), the question as sent ("" without one), the tap's
// time and the phone's live-log count once the lines up to the tap were sent.
type askRequest struct {
	ID       string
	Question string
	At       *float64
	LiveNext *int
	Spoken   bool // the phone transcribed the question from a Talk span (#3862)
}

// parseAsk reads a POST …/ask body: 400 for an id that is not a UUID, a question over maxAskQuestionRunes, neither a
// question nor at, or a field of the wrong type.
func parseAsk(raw []byte) (askRequest, error) {
	var in struct {
		ID       json.RawMessage `json:"id"`
		Question json.RawMessage `json:"question"`
		At       json.RawMessage `json:"at"`
		LiveNext json.RawMessage `json:"live_next"`
		Spoken   json.RawMessage `json:"spoken"`
	}
	var req askRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return req, i18n.Err("assistant-meeting.bad_json", i18n.Args{"error": err.Error()})
	}
	var id string
	if json.Unmarshal(in.ID, &id) != nil {
		return req, i18n.Err("assistant-meeting.bad_ask_id")
	}
	var ok bool
	if req.ID, ok = kb.RecordingID(id); !ok {
		return req, i18n.Err("assistant-meeting.bad_ask_id")
	}
	bad := func(field string) error {
		return i18n.Err("assistant-meeting.bad_ask_field", i18n.Args{"field": field})
	}
	if given(in.Question) && json.Unmarshal(in.Question, &req.Question) != nil {
		return req, bad("question")
	}
	if strings.TrimSpace(req.Question) == "" {
		req.Question = "" // absent: what was just said
	}
	if utf8.RuneCountInString(strings.TrimSpace(req.Question)) > maxAskQuestionRunes {
		return req, i18n.Err("assistant-meeting.question_too_long", i18n.Args{"max": maxAskQuestionRunes})
	}
	if given(in.At) {
		var at float64
		if json.Unmarshal(in.At, &at) != nil {
			return req, bad("at")
		}
		req.At = &at
	}
	if given(in.LiveNext) {
		n, ok := wholeNumber(in.LiveNext)
		if !ok || n < 0 {
			return req, bad("live_next")
		}
		next := int(n)
		req.LiveNext = &next
	}
	if given(in.Spoken) && json.Unmarshal(in.Spoken, &req.Spoken) != nil {
		return req, bad("spoken")
	}
	if req.Question == "" && req.At == nil {
		return req, i18n.Err("assistant-meeting.no_question")
	}
	req.Spoken = req.Spoken && req.Question != "" // a question heard in the room is never spoken
	return req, nil
}

// given: a JSON field present and not null.
func given(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func (s *Service) askRoute() {
	s.handle("POST /api/v1/recordings/{id}/ask", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r)
		if e == nil {
			return
		}
		raw, ok := meetingBody(w, r, maxAskBody, "64 KiB")
		if !ok {
			return
		}
		id := r.PathValue("id")
		if _, _, ok := meetingRecording(w, e, id); !ok {
			return
		}
		req, err := parseAsk(raw)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		a, done, err := s.askStart(r.Context(), e, id, req, string(i18n.FromRequest(r).Or(s.lang(r.Context()))))
		switch {
		case errors.Is(err, errOpIDReused):
			apierr.WriteCode(w, http.StatusConflict, codeOpIDReused, i18n.Err("assistant-meeting.op_id_reused"))
			return
		case errors.Is(err, errAsksFull):
			apierr.WriteCode(w, http.StatusConflict, codeAsksFull, i18n.Err("assistant-meeting.asks_full", i18n.Args{"max": maxAsks}))
			return
		case errors.Is(err, errAskBusy):
			apierr.WriteCode(w, http.StatusTooManyRequests, codeAskBusy, i18n.Err("assistant-meeting.ask_busy"))
			return
		case errors.Is(err, errMeetingEnded):
			apierr.WriteCode(w, http.StatusConflict, codeMeetingEnded, i18n.Err("assistant-meeting.meeting_ended"))
			return
		case err != nil:
			writeMeetingError(w, err)
			return
		}
		if done != nil {
			hold := time.NewTimer(askHold - time.Since(start))
			select {
			case <-done:
			case <-hold.C:
			case <-r.Context().Done():
			}
			hold.Stop()
			if now, ok := s.askNow(e, id, a.ID); ok {
				a = now
			}
		}
		status := http.StatusOK
		if a.State == askPending {
			status = http.StatusAccepted
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"ask": viewAsk(a)})
	})
}

// asksRead is recording id's live log and its stored upload, its questions settled (settleAsksLocked).
func (s *Service) asksRead(e *kb.Engine, id string) (kb.Live, kb.Recording, error) {
	b := &s.asks
	b.mu.Lock()
	defer b.mu.Unlock()
	l, rec, err := e.LiveRead(id)
	if err == nil {
		s.settleAsksLocked(e, rec, &l)
	}
	return l, rec, err
}

// askNow is question askID of recording id as it is now.
func (s *Service) askNow(e *kb.Engine, id, askID string) (kb.LiveAsk, bool) {
	l, _, err := s.asksRead(e, id)
	if err != nil {
		return kb.LiveAsk{}, false
	}
	i := slices.IndexFunc(l.Asks, func(a kb.LiveAsk) bool { return a.ID == askID })
	if i < 0 {
		return kb.LiveAsk{}, false
	}
	return l.Asks[i], true
}

// settleAsksLocked fails, as interrupted, the questions of recording rec's live log l that are pending while this
// process is not answering them (it restarted meanwhile), in l and, when it can, in the live log. s.asks.mu is held.
// In a room meeting such a question is taken up again instead (meeting_room_ask.go): its attempt may be in the room,
// whose turn settles it, and one never sent goes again under the same id.
func (s *Service) settleAsksLocked(e *kb.Engine, rec kb.Recording, l *kb.Live) {
	id := rec.ID
	running := s.asks.running[id]
	stale := func(a kb.LiveAsk) bool { return a.State == askPending && running[a.ID] == nil }
	if !slices.ContainsFunc(l.Asks, stale) {
		return
	}
	if rec.RoomMeeting() && !e.Store.ReadOnly() {
		for _, a := range l.Asks {
			if stale(a) && a.Via != kb.AskViaMaterial {
				s.askRunLocked(e, id, a) // the room's: taken up again
			}
		}
		running = s.asks.running[id]
	}
	settle := func(asks []kb.LiveAsk) {
		for i, a := range asks {
			if stale(a) {
				asks[i] = failAsk(a, askInterrupted)
			}
		}
	}
	settle(l.Asks)
	if e.Store.ReadOnly() {
		return // shown as interrupted; written once it can be
	}
	if _, err := e.LiveUpdate(id, func(l *kb.Live, _ kb.Recording) error { settle(l.Asks); return nil }); err != nil {
		s.logf("recording %s: interrupted questions: %v", id, err)
	}
}

// failAsk is a, failed for why.
func failAsk(a kb.LiveAsk, why string) kb.LiveAsk {
	a.State, a.Error, a.Answer, a.Sources, a.AnsweredAt = askFailed, why, "", []kb.AskSource{}, nil
	return a
}

// askStart answers req for recording id from the question stored with its id, else makes the question and starts
// answering it, in the contract's order: errOpIDReused for an id stored with another question or at (only a failed
// one goes on and is tried again), kb.ErrReadOnly, kb.ErrNotAttending, errAsksFull, errAskBusy. done is closed once
// the answer is stored (nil when nothing is answering it). lang is the request's language, for a recording without
// its own.
func (s *Service) askStart(ctx context.Context, e *kb.Engine, id string, req askRequest, lang string) (a kb.LiveAsk, done <-chan struct{}, err error) {
	b := &s.asks
	b.mu.Lock()
	defer b.mu.Unlock()
	l, rec, err := e.LiveRead(id)
	if err != nil {
		return a, nil, err
	}
	id = rec.ID
	s.settleAsksLocked(e, rec, &l)
	retry := false
	var failed kb.LiveAsk
	if i := slices.IndexFunc(l.Asks, func(x kb.LiveAsk) bool { return strings.EqualFold(x.ID, req.ID) }); i >= 0 {
		stored := l.Asks[i]
		if stored.QuestionSent != req.Question || !sameAt(stored.At, req.At) || stored.Spoken != req.Spoken {
			return stored, nil, errOpIDReused
		}
		if stored.State != askFailed {
			if ch, ok := b.running[id][stored.ID]; ok {
				return stored, ch, nil
			}
			return stored, nil, nil
		}
		retry, failed = true, stored
	}
	kept := len(l.Asks)
	if retry {
		kept--
	}
	switch {
	case !s.meetingWritable(ctx, e):
		return a, nil, kb.ErrReadOnly // and no model call
	case !rec.Attending():
		return a, nil, kb.ErrNotAttending
	case rec.RoomMeeting() && rec.Conv.Room.State == kb.RoomEnded, rec.RoomMeeting() && rec.Conv.Room.State == kb.RoomDeleted:
		return a, nil, errMeetingEnded // ask in the follow-up
	case kept >= maxAsks:
		return a, nil, errAsksFull
	case len(b.running[id]) >= maxAsksAnswering:
		return a, nil, errAskBusy
	}
	a = kb.LiveAsk{ID: req.ID, QuestionSent: req.Question, QuestionSource: askTyped, At: req.At, LiveNext: req.LiveNext,
		State: askPending, Sources: []kb.AskSource{}, AskedAt: time.Now().UTC().Truncate(time.Second), Spoken: req.Spoken}
	switch {
	case req.Question == "":
		a.QuestionSource = askHeard
	case req.Spoken:
		a.QuestionSource, a.Question = askSpoken, oneLine(req.Question)
	default:
		a.Question = oneLine(req.Question)
	}
	if retry && rec.RoomMeeting() {
		// Asked again: the same way as its first attempt, the next attempt's id so the room's send-once check does not
		// swallow it. A room ask while the room is unavailable fails at once: the phone asks a new question.
		a.Via, a.Attempts = failed.Via, slices.Clone(failed.Attempts)
		// Only attempts that went out count: one never sent is sent now under its own id.
		a.Attempts = slices.DeleteFunc(a.Attempts, func(x kb.AskAttempt) bool { return x.SentAt.IsZero() })
	}
	roomDown := retry && a.Via == kb.AskViaRoom && rec.Conv.Room.State != kb.RoomReady
	if retry && a.Via == kb.AskViaRoom && !roomDown {
		k := len(a.Attempts) + 1
		a.Attempts = append(a.Attempts, kb.AskAttempt{K: k, MsgID: askMsgID(a.ID, k)})
	}
	if roomDown {
		a = failAsk(a, askRoomUnavailble)
	}
	if _, err := e.LiveUpdate(id, func(l *kb.Live, r kb.Recording) error {
		if !r.Attending() {
			return kb.ErrNotAttending // an upload turned it off meanwhile
		}
		l.Asks = slices.DeleteFunc(l.Asks, func(x kb.LiveAsk) bool { return x.ID == a.ID }) // the failed one asked again
		if len(l.Asks) >= maxAsks {
			return errAsksFull
		}
		// Fixed now, from the role and the recording then: what the phone says aloud and in which voice.
		a.Speak, a.Lang = r.AttendanceRole() == kb.AttendanceParticipant, cmp.Or(strings.TrimSpace(r.Language), lang)
		l.Asks = append(l.Asks, a)
		return nil
	}); err != nil {
		return kb.LiveAsk{}, nil, err
	}
	if roomDown {
		return a, nil, nil
	}
	return a, s.askRunLocked(e, id, a), nil
}

// askRunLocked starts answering question a of recording id in the background; the channel closed once its answer is
// stored. s.asks.mu is held.
func (s *Service) askRunLocked(e *kb.Engine, id string, a kb.LiveAsk) chan struct{} {
	b := &s.asks
	ch := make(chan struct{})
	if b.running == nil {
		b.running = map[string]map[string]chan struct{}{}
	}
	if b.running[id] == nil {
		b.running[id] = map[string]chan struct{}{}
	}
	b.running[id][a.ID] = ch
	go s.answerAsk(e, id, a, ch)
	return ch
}

var errMeetingEnded = errors.New("the meeting has ended: ask in its follow-up")

// sameAt: two requests' at are the same (both absent, or equal).
func sameAt(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// answerAsk answers question a of recording id and stores the answer (or why it failed), then closes done. It runs
// past the request, at most askTimeout; if the service stops first nothing is stored, and the question reads as
// interrupted after the restart.
func (s *Service) answerAsk(e *kb.Engine, id string, a kb.LiveAsk, done chan struct{}) {
	defer func() {
		s.asks.finished(id, a.ID, done)
		close(done)
	}()
	var out kb.LiveAsk
	ctx := s.runCtx()
	if _, rec, err := e.LiveRead(id); err == nil && rec.RoomMeeting() {
		out = s.answerInRoom(ctx, e, id, a) // the room answers it (meeting_room_ask.go)
	} else {
		tctx, cancel := context.WithTimeout(ctx, askTimeout)
		out = s.answerMeetingAsk(tctx, e, id, a)
		cancel()
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	_, err := e.LiveUpdate(id, func(l *kb.Live, _ kb.Recording) error {
		i := slices.IndexFunc(l.Asks, func(x kb.LiveAsk) bool { return x.ID == a.ID })
		if i < 0 || l.Asks[i].State != askPending || !l.Asks[i].AskedAt.Equal(a.AskedAt) {
			return errAskGone
		}
		x := &l.Asks[i]
		x.Question, x.State, x.Answer, x.Sources, x.Error, x.AnsweredAt = out.Question, out.State, out.Answer, out.Sources, out.Error, out.AnsweredAt
		if out.Via != "" {
			x.Via = out.Via
		}
		if out.Heard != nil {
			x.Heard = out.Heard
		}
		return nil
	})
	switch {
	case err == nil && out.State == askAnswered:
		s.meetingKick(id) // the attending session hears of the question and its answer with the next batch
	case err != nil && !errors.Is(err, errAskGone):
		s.logf("recording %s: question %s: %v", id, a.ID, err)
	}
}

// meetingAnswerModel is the model answers are asked of: meeting.answer's (tests give their own).
func (s *Service) meetingAnswerModel() kb.Model {
	if s.meetingModel != nil {
		return s.meetingModel
	}
	return s.purposeModel(purposeMeetingAnswer)
}

// meetingReply is what the answering model returns.
type meetingReply struct {
	Question string            `json:"question"`
	Answer   string            `json:"answer"`
	Sources  []json.RawMessage `json:"sources"`
}

// answerMeetingAsk is question a of recording id answered from the meeting material, or failed. A heard question first
// waits for the live sentences up to the tap, and is what was said in the heardSeconds before it.
func (s *Service) answerMeetingAsk(ctx context.Context, e *kb.Engine, id string, a kb.LiveAsk) kb.LiveAsk {
	failed := func(why string) kb.LiveAsk {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			why = askModelTimeout
		}
		return failAsk(a, why)
	}
	question := a.Question
	var heard []kb.LiveSegment
	if a.QuestionSource == askHeard {
		l, err := awaitLive(ctx, e, id, a.LiveNext)
		if err != nil {
			return failed(askModelError)
		}
		if heard = heardLines(l.Segments, *a.At); len(heard) == 0 {
			return failAsk(a, askNothingHeard)
		}
		texts := make([]string, len(heard))
		for i, sg := range heard {
			texts[i] = sg.Text
		}
		question = strings.Join(texts, " ")
	}
	m, err := e.MeetingMaterial(id, question)
	if err != nil {
		return failed(askModelError)
	}
	system := prompts.RenderMeetingAnswer(prompts.MeetingAnswer{Owner: s.owner(ctx),
		Today: time.Now().In(s.location(ctx)).Format("2006-01-02 (Monday)"), Language: a.Lang, EarlierAvailable: m.EarlierAvailable})
	var out meetingReply
	err = s.meetingAnswerModel().JSONMax(ctx, system, prompts.RenderMeetingAnswerInput(s.answerInput(ctx, m, a, heard)), 1024, &out)
	switch {
	case err == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return failAsk(a, askModelTimeout)
	case ai.IsNotConfigured(err) || ai.IsPaused(err):
		return failAsk(a, askModelUnavailable)
	default:
		s.logf("recording %s: question %s: %v", id, a.ID, err)
		return failAsk(a, askModelError)
	}
	answer := plainAnswer(out.Answer)
	if answer == "" {
		return failAsk(a, askModelError)
	}
	if a.QuestionSource == askHeard {
		a.Question = textfmt.Cut(oneLine(out.Question), maxAskQuestionRunes)
		if a.Question == "" { // the model found none: what was said last stands for it
			a.Question = textfmt.Cut(heard[len(heard)-1].Text, maxAskQuestionRunes)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	a.State, a.Answer, a.Error, a.Sources, a.AnsweredAt = askAnswered, answer, "", answerSources(m, out.Sources), &now
	return a
}

// awaitLive is recording id's live log once it holds next positions, waiting at most askLiveWait; without next at once.
func awaitLive(ctx context.Context, e *kb.Engine, id string, next *int) (kb.Live, error) {
	l, _, err := e.LiveRead(id)
	if err != nil || next == nil {
		return l, err
	}
	deadline := time.Now().Add(askLiveWait)
	for len(l.Segments) < *next && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return l, ctx.Err()
		case <-time.After(askLivePoll):
		}
		if l, _, err = e.LiveRead(id); err != nil {
			return l, err
		}
	}
	return l, nil
}

// heardLines are the live sentences with text said in the heardSeconds up to at: any that overlaps them, so a
// sentence still being said at the tap (its end can fall a little past at once the phone cuts its audio) is kept.
func heardLines(segs []kb.LiveSegment, at float64) []kb.LiveSegment {
	var out []kb.LiveSegment
	for _, sg := range segs {
		if sg.Text != "" && sg.Start <= at && sg.End >= at-heardSeconds {
			out = append(out, sg)
		}
	}
	return out
}

// answerInput is material m as the answering model reads it, for question a (heard: the lines before the tap). The
// to-dos of the earlier meetings' action items say only whether they are open or done.
func (s *Service) answerInput(ctx context.Context, m kb.MeetingMaterial, a kb.LiveAsk, heard []kb.LiveSegment) prompts.MeetingAnswerInput {
	lines := func(segs []kb.LiveSegment) []prompts.MeetingLine {
		out := make([]prompts.MeetingLine, 0, len(segs))
		for _, sg := range segs {
			out = append(out, prompts.MeetingLine{At: meetingClock(sg.Start), Text: oneLine(sg.Text)})
		}
		return out
	}
	each := func(list []string) []string {
		out := make([]string, 0, len(list))
		for _, x := range list {
			if x = oneLine(x); x != "" {
				out = append(out, x)
			}
		}
		return out
	}
	in := prompts.MeetingAnswerInput{Title: oneLine(m.Title), Language: oneLine(m.Language), Lines: lines(m.Lines),
		EarlierAvailable: m.EarlierAvailable}
	if c := m.Calendar; c != nil {
		in.Calendar = &prompts.MeetingCalendar{Title: oneLine(c.Title), Location: oneLine(c.Location), Attendees: each(c.Attendees)}
	}
	for _, q := range m.Asks {
		in.Asks = append(in.Asks, prompts.MeetingQA{Question: oneLine(q.Question), Answer: oneLine(q.Answer)})
	}
	for _, em := range m.Earlier {
		mm := em.Minutes
		pe := prompts.MeetingEarlier{ID: em.ID, Title: oneLine(em.Name), When: oneLine(em.When), Attendees: each(mm.Attendees)}
		for _, p := range mm.Points {
			pe.Points = append(pe.Points, prompts.MeetingPoint{Text: oneLine(p.Text), Who: oneLine(p.Who)})
		}
		for _, d := range mm.Decisions {
			pe.Decisions = append(pe.Decisions, oneLine(d.Text))
		}
		for _, act := range mm.Actions {
			pe.Actions = append(pe.Actions, prompts.MeetingAction{Who: oneLine(act.Who), What: oneLine(act.What), By: oneLine(act.By),
				DueDate: act.DueDate, Todo: s.actionTodo(ctx, act.TodoID)})
		}
		for _, o := range mm.Open {
			pe.Open = append(pe.Open, oneLine(o.Text))
		}
		for _, p := range em.Projects {
			pe.Projects = append(pe.Projects, prompts.MeetingProject{Slug: p.Slug, Name: oneLine(p.Name), Status: p.Status})
		}
		in.Earlier = append(in.Earlier, pe)
	}
	if a.QuestionSource == askHeard {
		in.HeardAt, in.Heard = meetingClock(*a.At), lines(heard)
	} else {
		in.Question = oneLine(a.Question)
	}
	return in
}

// actionTodo is whether the to-do made of an action item is open or done: only that of it, never its title, notes or
// thread. suggestion: the action was never made a to-do; "": its to-do is not known (deleted, or unreadable now).
func (s *Service) actionTodo(ctx context.Context, todoID string) string {
	if todoID == "" {
		return "suggestion"
	}
	t, ok, err := s.todo(ctx, todoID)
	switch {
	case err != nil || !ok:
		return ""
	case t.DoneAt != nil:
		return "done"
	}
	return "open"
}

// meetingClock is seconds from a recording's start as a clock: m:ss, or h:mm:ss from the first hour on.
func meetingClock(sec float64) string {
	n := int(max(sec, 0))
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

// plainAnswer is the model's answer as the phone shows and says it: one paragraph without Markdown emphasis or code
// marks, at most maxAnswerRunes, cut at the end of a sentence when one ends in the latter half.
func plainAnswer(s string) string {
	s = oneLine(strings.NewReplacer("**", "", "__", "", "`", "").Replace(s))
	if utf8.RuneCountInString(s) <= maxAnswerRunes {
		return s
	}
	head := textfmt.Cut(s, maxAnswerRunes-1)
	if i := strings.LastIndexAny(head, ".!?。！？"); i >= len(head)/2 { // i18n-ignore: sentence ends, not text
		_, size := utf8.DecodeRuneInString(head[i:])
		return head[:i+size]
	}
	return head + "…"
}

// answerSources are the sources the model named that the material holds: its earlier meetings (by id) and their
// projects (by slug), each once, in the model's order. Anything else is dropped.
func answerSources(m kb.MeetingMaterial, named []json.RawMessage) []kb.AskSource {
	out := []kb.AskSource{}
	add := func(src kb.AskSource) {
		if !slices.ContainsFunc(out, func(x kb.AskSource) bool { return x.Kind == src.Kind && x.ID == src.ID }) {
			out = append(out, src)
		}
	}
	for _, raw := range named {
		var ref string
		if json.Unmarshal(raw, &ref) != nil {
			var obj struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			}
			if json.Unmarshal(raw, &obj) != nil {
				continue
			}
			ref = cmp.Or(obj.ID, obj.Slug)
		}
		ref = strings.TrimSpace(ref)
		ref = strings.TrimPrefix(strings.TrimPrefix(ref, kb.KindRecording+"/"), kb.KindProject+"/")
		if ref == "" {
			continue
		}
		for _, em := range m.Earlier {
			if strings.EqualFold(em.ID, ref) {
				add(kb.AskSource{Kind: kb.KindRecording, ID: em.ID, Name: em.Name, When: em.When, Start: em.Start, End: em.End})
			}
			for _, p := range em.Projects {
				if p.Slug == ref {
					add(kb.AskSource{Kind: kb.KindProject, ID: p.Slug, Name: p.Name})
				}
			}
		}
	}
	return out
}
