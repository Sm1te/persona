package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- turns ----

// retireTodoWork withdraws the manager's pending follow-ups for the agents of a todo that just ended (completed or a
// failed start): the scheduled wake — one postponed while the machine was offline included — and the open work card
// are cleared, so neither can wake the finished agent nor let a card answer back into the finished turn. The turn's
// own work state is kept, as workStartedObserved keeps it: with no wake and no card the tick never acts on the entry,
// and triage never redoes a turn it already triaged.
func (s *Service) retireTodoWork(keys ...[2]string) {
	now := time.Now().UTC()
	for _, k := range keys {
		s.work.mu.Lock()
		s.work.init()
		if s.work.triaging[k] {
			s.work.generation[k]++ // a late transcript result must not register another wake
		}
		e, ok := s.work.entries[k]
		if !ok {
			e = &db.SessionWork{MachineID: k[0], SessionID: k[1]}
			s.work.entries[k] = e
		}
		if e.InteractionID != "" {
			delete(s.work.cards, e.InteractionID)
		}
		e.WakeAt, e.Work.WakeAt, e.Work.CI, e.InteractionID, e.Work.InteractionID = nil, nil, nil, "", ""
		e.Work.RetiredAt, e.Work.RetiredTurnAt = &now, nil
		e.UpdatedAt = now
		saved := *e
		s.work.mu.Unlock()
		s.saveWork(saved)
		s.publishSoon()
		s.bindRetiredTurn(k, now)
	}
}

// bindRetiredTurn attaches the node's own stopped-turn timestamp to a durable
// retirement. If it cannot be read, the marker stays until a new TurnStarted.
func (s *Service) bindRetiredTurn(k [2]string, retiredAt time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, ok := s.session(ctx, k[0], k[1])
	if !ok || c.Status != proto.StatusDone && c.Status != proto.StatusIdle || c.UpdatedAt.IsZero() {
		return
	}
	s.work.mu.Lock()
	current := s.work.entries[k]
	if current == nil || current.Work.RetiredAt == nil || !current.Work.RetiredAt.Equal(retiredAt) {
		s.work.mu.Unlock()
		return
	}
	turn := c.UpdatedAt.UTC()
	current.Work.RetiredTurnAt = &turn
	current.UpdatedAt = time.Now().UTC()
	saved := *current
	s.work.mu.Unlock()
	s.saveWork(saved)
}

// workStartedObserved: a session began working (again). Whatever the manager was going to do for its last turn is
// off; a card for it is withdrawn. How long the turn waited is recorded.
//
// A first or reconnected working list observes a running turn; it does not
// prove that the turn started while this manager was connected.
func (s *Service) workStartedObserved(ctx context.Context, machine, sid string, now time.Time, newTurn bool) {
	k := [2]string{machine, sid}
	if newTurn {
		s.clearToolLoop(k, loopTurnChanged)
	}
	s.work.mu.Lock()
	s.work.init()
	s.work.started[k] = now
	delete(s.work.viewed, k)
	if s.work.triaging[k] {
		s.work.generation[k]++
	}
	e, ok := s.work.entries[k]
	if !ok || e.Work.Phase == proto.WorkWorking && e.Work.RetiredAt == nil {
		s.work.mu.Unlock()
		return
	}
	e.Work.RetiredAt, e.Work.RetiredTurnAt = nil, nil // a later turn can work on a reopened task
	by := "user"
	if now.Sub(s.work.sent[k]) < workSentWindow {
		by = "hub"
	}
	if by == "user" {
		delete(s.work.approved, k) // the user took over: their earlier yes is not for what comes next
	}
	card := e.InteractionID
	prev := *e
	would, managed := s.work.would[k], now.Sub(s.work.told[k]) < workSentWindow
	delete(s.work.would, k)
	if card != "" {
		delete(s.work.cards, card)
	}
	changed := e.WakeAt != nil || card != ""
	e.WakeAt, e.Work.WakeAt, e.InteractionID, e.Work.InteractionID = nil, nil, "", ""
	e.UpdatedAt = now.UTC()
	saved := *e
	s.work.mu.Unlock()
	if card != "" {
		s.workEvent(machine, sid, "withdrawn", map[string]any{"by": by})
	}
	if prev.Work.Phase != "" {
		detail := map[string]any{"after": now.Sub(prev.TurnAt).Seconds(), "phase": prev.Work.Phase, "on": prev.Work.On, "by": by}
		if by == "user" && !managed && would != "" && s.startedByHand(ctx, machine, sid) {
			detail["hand"] = would
		}
		s.workEvent(machine, sid, "resumed", detail)
	}
	s.saveWork(saved)
	if changed {
		s.publishSoon()
	}
}

// workSentWindow is how long after the manager sent a session something a turn it starts is the manager's doing.
const workSentWindow = 2 * time.Minute

// The triage actions a resumed event's `hand` names (workWould): the switch that alone would have gone on with the stop.
const (
	handDecide       = "decide"
	handAutoContinue = "auto_continue"
	handWake         = "wake"
)

// workTold notes that this manager sent machine's session sid a message now.
func (s *Service) workTold(machine, sid string) {
	s.work.mu.Lock()
	s.work.init()
	s.work.told[[2]string{machine, sid}] = time.Now()
	s.work.mu.Unlock()
}

// workWould notes, for the session's stop the triage just acted on, the action that was left undone only because its
// switch is off ("" clears it): what a resumed event's `hand` says when the session then goes on without Persona.
func (s *Service) workWould(k [2]string, action string) {
	s.work.mu.Lock()
	s.work.init()
	if action == "" {
		delete(s.work.would, k)
	} else {
		s.work.would[k] = action
	}
	s.work.mu.Unlock()
}

// startedByHand: nothing says the turn machine's session sid just started was the agent's own doing (a background
// task reporting back, a /loop). With no message of this manager's before it (workStartedObserved), that leaves a
// message from outside Persona: the owner's, in an app or typed into the agent's terminal. A session that cannot be
// read is not counted.
func (s *Service) startedByHand(ctx context.Context, machine, sid string) bool {
	sess, ok := s.nodeSession(machine, sid) // as the node listed it; asked only when this manager holds no list
	if !ok {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		sess, ok = s.session(ctx, machine, sid)
	}
	return ok && !sess.WokeByTask && sess.Loop == nil
}

// workSeen: the user looked at a session. The first look after a turn ended is how long it waited for them.
func (s *Service) workSeen(machine, sid string, at time.Time) {
	k := [2]string{machine, sid}
	s.work.mu.Lock()
	s.work.init()
	previousSeen := s.work.seen[k]
	if at.After(previousSeen) {
		s.work.seen[k] = at
	}
	started := s.work.started[k]
	currentView := started.IsZero() || !at.Before(started)
	if currentView {
		s.work.viewed[k] = true
		if s.work.triaging[k] {
			s.work.generation[k]++
		}
	}
	e, ok := s.work.entries[k]
	if !ok || e.Work.Phase == "" {
		s.work.mu.Unlock()
		return
	}
	first := previousSeen.Before(e.TurnAt) && !at.Before(e.TurnAt)
	phase, on, turn := e.Work.Phase, e.Work.On, e.TurnAt
	var cancelled *db.SessionWork
	if currentView && e.Work.CI != nil && e.WakeAt != nil {
		e.Work.CI, e.Work.WakeAt, e.WakeAt = nil, nil, nil
		e.UpdatedAt = time.Now().UTC()
		saved := *e
		cancelled = &saved
	}
	s.work.mu.Unlock()
	if cancelled != nil {
		s.saveWork(*cancelled)
		s.publishSoon()
	}
	if first {
		s.workEvent(machine, sid, "seen", map[string]any{"after": at.Sub(turn).Seconds(), "phase": phase, "on": on})
	}
}

// workTurnEnded: a session on machine stopped. Once it stays stopped a moment, its turn is triaged; agents waiting
// for this one are woken.
func (s *Service) workTurnEnded(ctx context.Context, machine, sid string) {
	if !s.workActive(ctx) || !s.loadWork(ctx) {
		return
	}
	s.wakeWaitersOn(machine, sid)
	// On a timer: a goroutine asleep through the window would hold a stack per pending turn end (#2472).
	time.AfterFunc(settleOf(s.triageCfg(ctx)), func() { s.workTurnSettled(ctx, machine, sid) })
}

// workTurnSettled checks the turn after the settle window.
func (s *Service) workTurnSettled(ctx context.Context, machine, sid string) {
	v := s.newWorkView(ctx)
	c, ok := v.session(machine, sid)
	if !ok || c.Status == proto.StatusWorking {
		return
	}
	s.triageTurn(ctx, v, machine, c)
}

// triageTurn works out where c stands after its turn and acts on it. Once per turn.
func (s *Service) triageTurn(ctx context.Context, v *workView, machine string, c proto.Session) {
	if c.Assistant || c.Status == proto.StatusOffline || c.Status == proto.StatusWorking || c.Starting {
		return
	}
	if s.isReviewer(machine, c.SessionID) {
		return // a review's reviewer: its turn is the review's (supervise_autoreview.go), never the user's to decide
	}
	if s.meetingTurnNow(machine, c.SessionID) {
		return // a meeting's batch started it: a side exchange, no work state (meeting_turns.go)
	}
	k := [2]string{machine, c.SessionID}
	s.work.mu.Lock()
	s.work.init()
	prev, had := s.work.entries[k]
	if had && prev.Work.RetiredAt != nil && (prev.Work.RetiredTurnAt == nil || sameTurn(c, *prev.Work.RetiredTurnAt) || !c.UpdatedAt.After(*prev.Work.RetiredTurnAt)) {
		s.work.mu.Unlock()
		return // the todo ended before this turn settled, or its identity is unknown
	}
	if s.work.triaging[k] {
		s.work.mu.Unlock()
		return
	}
	s.work.tried[k] = triedTurn{c.UpdatedAt, time.Now()}
	if had && sameTurn(c, prev.TurnAt) && (prev.Work.Phase != "" || prev.Work.By != "") {
		s.work.mu.Unlock()
		return
	}
	s.work.triaging[k] = true
	generation := s.work.generation[k]
	var last db.SessionWork
	if had {
		last = *prev
	}
	s.work.mu.Unlock()
	defer func() {
		s.work.mu.Lock()
		delete(s.work.triaging, k)
		delete(s.work.generation, k)
		s.work.mu.Unlock()
	}()

	start := time.Now()
	var same bool // the model says it still waits on the step the user approved
	w, by, skip := triageRules(c, s.location(ctx), s.lang(ctx))
	if skip {
		return // the agent asks itself (or its terminal does): the list shows that
	}
	if !s.workOn(ctx) && (w == nil || w.On != proto.WaitTransient) {
		// The triage is off and only transient errors are continued (transient_retry): no model call, nothing else
		// decided. A turn that ended otherwise ends the run of transient errors: the next one counts from 1 again.
		if had && last.Retries > 0 {
			last.Retries = 0
			s.putWork(ctx, last)
		}
		return
	}
	if w == nil {
		mctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		var err error
		w, same, err = s.triageTurnModel(mctx, v, machine, c)
		if err != nil {
			s.logf("triage %s/%s: %v", machine, short(c.SessionID), err)
			s.workEvent(machine, c.SessionID, "failed", map[string]any{"error": clip(err.Error(), 200)})
			return
		}
		by = "model"
		if w.By != "" {
			by = w.By // decided from the agent's closeout, without the model
		}
	}
	s.applyApproval(k, w, same)
	w.TurnAt, w.At, w.By = c.UpdatedAt, time.Now().UTC(), by
	e := db.SessionWork{MachineID: machine, SessionID: c.SessionID, TurnAt: c.UpdatedAt, Work: *w}
	carryCounts(&e, last, c)
	if now, ok := s.session(ctx, machine, c.SessionID); ok && !sameTurn(now, e.TurnAt) {
		return // it went on while the model read it: the next end decides
	}
	closed, err := s.completedTodo(ctx, machine, c.SessionID)
	if err != nil {
		s.logf("triage %s/%s: cannot read todos: %v", machine, short(c.SessionID), err)
		s.workEvent(machine, c.SessionID, "failed", map[string]any{"error": "todo state unavailable"})
		return // leave the turn retryable when its task state is unknown
	}
	if !closed {
		s.actOn(ctx, v, machine, c, &e)
	}
	// The turn after a status question is its answer (agents_closeout_ask.go): never asked again, whatever it says.
	answersAsk := had && last.Work.CloseoutAsk == closeoutAskSent && !sameTurn(c, last.TurnAt)
	switch {
	case answersAsk && continues(e.Work):
		// Cut off before it answered: the turn that goes on from here is the answer.
		e.Work.CloseoutAsk, answersAsk = closeoutAskSent, false
	case !answersAsk && !closed:
		s.planCloseoutAsk(ctx, machine, c, &e)
	}
	if e.WakeAt != nil && e.Work.On == proto.WaitExternal && e.Work.ResetsAt == nil {
		if ciWaitIntent(&e.Work) {
			ask, reply := s.lastTurnOf(ctx, machine, c.SessionID)
			startCIWatch(c, &e, time.Now(), ask, reply)
		}
		if e.Work.CI != nil {
			closed, proof, err := s.ciClosedTodo(ctx, machine, c.SessionID, "", last.Work, c.UpdatedAt)
			if err != nil {
				s.logf("triage %s/%s: cannot read todos: %v", machine, short(c.SessionID), err)
				s.workEvent(machine, c.SessionID, "failed", map[string]any{"error": "todo state unavailable"})
				return // do not mark this turn triaged; backfill retries after backoff
			}
			if closed {
				e.WakeAt, e.Work.WakeAt, e.Work.CI = nil, nil, nil
			} else {
				e.Work.CI.TodoProof = proof
			}
		}
		latest, ok := s.session(ctx, machine, c.SessionID)
		if !ok || latest.Status == proto.StatusWorking || !sameTurn(latest, e.TurnAt) {
			return // activity during the transcript read invalidated this turn
		}
		if latest.Status != proto.StatusDone || latest.PendingInteractions > 0 || len(latest.Queued) > 0 {
			e.WakeAt, e.Work.WakeAt, e.Work.CI = nil, nil, nil
		}
		if !s.putTriageWork(e, generation) {
			return
		}
	} else {
		s.putWork(ctx, e)
	}
	s.logf("triage %s/%s: %s%s (%s %.1fs): %s", machine, short(c.SessionID), e.Work.Phase, orEmpty("/", e.Work.On), by,
		time.Since(start).Seconds(), e.Work.Line)
	s.workEvent(machine, c.SessionID, "triaged", map[string]any{"phase": e.Work.Phase, "on": e.Work.On, "by": by,
		"line": e.Work.Line, "ms": time.Since(start).Milliseconds()})
	if answersAsk {
		s.workEvent(machine, c.SessionID, closeoutAskOutcome(e.Work), map[string]any{"phase": e.Work.Phase})
	}
}

// carryCounts: the same wait goes on (the manager woke or continued it, it still waits), so e's counts are last's
// (none: a zero last). A usage limit's count is its own: it carries from the turn before only when that one stopped
// on a usage limit too (its card after the continues included), with no later reset (a later one is the next window),
// and this one did no work before the limit refused it again; a turn that did work starts it again. An ordinary wait never takes a usage limit's count, nor the other way.
func carryCounts(e *db.SessionWork, last db.SessionWork, c proto.Session) {
	w := &e.Work
	switch limit := w.ResetsAt != nil; {
	case last.Work.Phase != proto.WorkBlocked || w.Phase != proto.WorkBlocked:
	case limit != (last.Work.ResetsAt != nil):
	case limit:
		// The same reset still (a later one is another window: the earlier was read right) and no work since.
		if !c.TurnErrorAfterWork && !w.ResetsAt.After(*last.Work.ResetsAt) {
			e.Retries = last.Retries
		}
	case last.Work.On == w.On:
		e.Retries, e.Wakes = last.Retries, last.Wakes
	}
}

// applyApproval: a turn still waiting on the step the user already said yes to (same: the model says so, and the
// words agree) is a wait the manager ends: no second card for it. A turn that is no such wait ends the approval, so
// what is left in s.work.approved always held for the latest triaged turn.
func (s *Service) applyApproval(k [2]string, w *proto.WorkState, same bool) {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	a, ok := s.work.approved[k]
	if !ok {
		return
	}
	if !same || !a.covers(w) || len(w.Questions) > 1 { // a second question is the user's all the same
		delete(s.work.approved, k)
		return
	}
	if w.On == proto.WaitUser && risky(w.Then) {
		w.On, w.Questions = proto.WaitExternal, nil // only the approved step made it the user's
	}
}

func orEmpty(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

// triageRules settles what needs no model. skip: the agent waits for the user on its own card or dialog.
// What it says is in l (the user's language).
func triageRules(c proto.Session, loc *time.Location, l i18n.Lang) (w *proto.WorkState, by string, skip bool) {
	if c.PendingInteractions > 0 || c.Status == proto.StatusBlocked {
		return nil, "", true
	}
	if e := c.TurnError; e != "" {
		if sessionTurnErrorCode(c) == proto.TurnErrorModelFallbackUnavailable {
			return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser,
				Line: i18n.T(l, "assistant-readiness.model_fallback_unavailable_title"),
				Questions: []proto.Question{{Question: i18n.T(l, "assistant-readiness.model_fallback_unavailable_detail"), Header: workT(l, "header_model"),
					Options: []proto.QuestionOption{{Label: answerLabel(l, answerSkip)}}}}}, "rule", false
		}
		if c.TurnErrorKind == proto.TurnErrorUsageLimit {
			return limitWork(c, time.Now(), loc, l), "rule", false
		}
		if turnErrorTransient(c) {
			return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitTransient, Line: workT(l, "transient_retrying"),
				Wait: clip(oneLine(e), 200)}, "rule", false
		}
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: clip(oneLine(workT(l, "model_error", i18n.Args{"error": e})), 60),
			Questions: []proto.Question{{Question: workT(l, "model_error_ask", i18n.Args{"error": clip(oneLine(e), 300)}), Header: workT(l, "header_model"),
				Options: []proto.QuestionOption{{Label: answerLabel(l, answerHandled)}, {Label: answerLabel(l, answerSkip)}}}}}, "rule", false
	}
	if n := runningInBackground(c); n > 0 {
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Line: workT(l, "background_running", i18n.Args{"n": n})},
			"rule", false
	}
	if c.Loop != nil {
		line := workT(l, "loop_running")
		if !c.Loop.NextAt.IsZero() {
			line = workT(l, "loop_running_next", i18n.Args{"time": c.Loop.NextAt.In(loc).Format("15:04")})
		}
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Line: line}, "rule", false
	}
	return nil, "", false
}

// limitWork is where c stands after a subscription's usage limit ended its turn (docs/assistant/agent-work.md "Usage
// limits"): with its reset known, an external wait for it (ResetsAt), the CLI going on by itself then or the manager
// continuing it (actOn); without, the user's, as other errors they must see to are.
func limitWork(c proto.Session, now time.Time, loc *time.Location, l i18n.Lang) *proto.WorkState {
	e := c.TurnError
	reset, ok := usageLimitReset(c)
	if !ok && c.TurnErrorResumes {
		// Its CLI goes on by itself after the reset, whenever that is: nothing to ask, and nothing may be typed into it.
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, Line: workT(l, "limit_resumes_later")}
	}
	if !ok {
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: clip(oneLine(workT(l, "limit_line", i18n.Args{"error": e})), 60),
			Questions: []proto.Question{{Question: workT(l, "limit_ask", i18n.Args{"error": clip(oneLine(e), 300)}), Header: workT(l, "header_limit"),
				Options: []proto.QuestionOption{{Label: answerLabel(l, answerRetry)}, {Label: answerLabel(l, answerSkip)}}}}}
	}
	// No Wait: the error is the session's (TurnError), and an external wait without one is one that ends by itself, as
	// the apps list it (their hubWaits): the CLI goes on, or the manager continues it (wake_at).
	return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitExternal, ResetsAt: &reset,
		Line: limitLine(l, reset, c.TurnErrorResumes, now, loc)}
}

// limitLine is a usage-limit wait's line in l: when it goes on by itself (resumes), else when the limit resets.
func limitLine(l i18n.Lang, reset time.Time, resumes bool, now time.Time, loc *time.Location) string {
	if resumes {
		return workT(l, "limit_resumes", i18n.Args{"time": limitClock(reset, now, loc)})
	}
	return workT(l, "limit_resets", i18n.Args{"time": limitClock(reset, now, loc)})
}

// limitClock is when a usage limit resets, as a line says it in loc: the time of day when that is today, with the
// month and day when it is another day (tomorrow morning, a weekly limit). The same in every language.
func limitClock(reset, now time.Time, loc *time.Location) string {
	r, n := reset.In(loc), now.In(loc)
	if r.YearDay() == n.YearDay() && r.Year() == n.Year() {
		return r.Format("15:04")
	}
	return r.Format("01-02 15:04")
}

func runningInBackground(c proto.Session) int {
	n := len(c.Background)
	for _, a := range c.Subagents {
		if a.Running {
			n++
		}
	}
	return n
}

// turnErrorTransient: c's turn error is worth a "Continue". The agent's record says so when it gives a kind (Codex's error
// kinds, #2185); otherwise the text does. A usage limit never is: a "Continue" before its reset is refused at once.
func turnErrorTransient(c proto.Session) bool {
	if sessionTurnErrorCode(c) == proto.TurnErrorModelFallbackUnavailable {
		return false
	}
	switch c.TurnErrorKind {
	case proto.TurnErrorTransient:
		return true
	case proto.TurnErrorUser, proto.TurnErrorUsageLimit:
		return false
	}
	return transientError(c.TurnError)
}

// transientError: a model API error worth a "Continue" (the service failed for a moment), not one the user must fix
// (sign-in, key, the model picked, a request the service refuses as it is, a context too long, an interrupt). What the
// user must fix wins when the text says both.
func transientError(text string) bool {
	t := strings.ToLower(text)
	if userErrorStatus.MatchString(t) {
		return false
	}
	for _, w := range userErrorWords {
		if strings.Contains(t, w) {
			return false
		}
	}
	if transientStatus.MatchString(t) {
		return true
	}
	for _, w := range transientWords {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

// rateLimitError: a transient error that is a rate limit (429): its "Continue" waits longer (retryBackoff).
func rateLimitError(text string) bool {
	t := strings.ToLower(text)
	return rateLimitStatus.MatchString(t) || strings.Contains(t, "rate limit") || strings.Contains(t, "rate_limit") ||
		strings.Contains(t, "too many requests")
}

var (
	// userErrorStatus: HTTP statuses a "Continue" cannot fix: a bad request (400), sign-in or permission (401, 403),
	// a request too large (413).
	userErrorStatus = lazyre.New(`\b(400|401|403|413)\b`)
	// transientStatus: HTTP statuses of a service failing for a moment: a timeout (408), a rate limit (429), a server
	// error (500, 502, 503, 504) or overloaded (529).
	transientStatus = lazyre.New(`\b(408|429|500|502|503|504|529)\b`)
	rateLimitStatus = lazyre.New(`\b429\b`)
	// userErrorWords: the auth words shared with the node's failed-turn check (assistantwire.AuthErrorWords, #4669),
	// then a request the service refuses as it is, a context too long, an interrupt.
	userErrorWords = append(slices.Clone(assistantwire.AuthErrorWords),
		"invalid_request_error", "invalid request", "prompt is too long", "context length", "context window", "context_length_exceeded",
		"maximum context", "too many tokens", "request too large", "request_too_large",
		"interrupted by user", "request was aborted", "aborted by user", "user aborted")
	transientWords = []string{"overloaded", "rate limit", "rate_limit", "too many requests",
		"connection", "timed out", "timeout", "socket hang up", "econnreset", "econnrefused", "fetch failed", "no eligible upstream",
		"internal server error", "service unavailable", "bad gateway", "gateway timeout", "network", "api_error", "upstream",
		"can't reach the api server", "temporarily unavailable", "no response from api", "unable to connect to api",
		"enotfound", "eai_again", "failedtoopensocket"}
)

// riskyWords mark a next step the manager must not set off itself (docs/assistant/agent-work.md "Safety boundary"): it is only ever a
// card.
var riskyWords = []string{"push", "deploy", "部署", "发布", "上线", "merge", "合并", "合入", "发消息", "回复", "评论", "testflight",
	"release", "推送", "推到", "comment", "发版", "提审", "send",
	"发送", "删除", "delete", "购买", "付费", "续费", "充值", "生产", "production"} // i18n-ignore: matched against what the agent said

func risky(texts ...string) bool {
	for _, t := range texts {
		if len(riskyWordsIn(t)) > 0 {
			return true
		}
	}
	return false
}

// riskyWordsIn are the risky words in t (lower case).
func riskyWordsIn(t string) []string {
	t = strings.ToLower(t)
	var out []string
	for _, w := range riskyWords {
		if strings.Contains(t, w) {
			out = append(out, w)
		}
	}
	return out
}
