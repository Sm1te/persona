package assistant

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ---- acting ----

// actOn decides what the manager does about e's work state and records it on e (wake_at, a card). It sends nothing:
// the tick does, after checking again.
func (s *Service) actOn(ctx context.Context, v *workView, machine string, c proto.Session, e *db.SessionWork) {
	cfg, sw := s.triageCfg(ctx), s.triageSwitches(ctx)
	l := s.lang(ctx)
	w := &e.Work
	now := time.Now()
	k := [2]string{e.MachineID, e.SessionID}
	s.workWould(k, "") // of the stop before this one
	if w.Phase != proto.WorkBlocked || c.ReadOnlyReason != "" {
		return // nothing to do, or nothing Persona may send it
	}
	s.approveByTask(ctx, k, w, sw.Wake) // a step its task asked for: no card, the wake below (agents_work_authorized.go)
	switch w.On {
	case proto.WaitTransient:
		if !sw.transientOn() {
			w.Line = clip(oneLine(workT(l, "model_error", i18n.Args{"error": w.Wait})), 60)
			return
		}
		if e.Retries >= maxRetries(cfg) {
			s.logf("work %s/%s: auto-continue: transient model error persists after %d automatic continues, left to the user: %s",
				e.MachineID, short(e.SessionID), e.Retries, clip(w.Wait, 120))
			*w = proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, TurnAt: w.TurnAt, At: w.At, By: w.By,
				Line: workT(l, "retries_line", i18n.Args{"n": e.Retries}),
				Questions: []proto.Question{{Question: workT(l, "retries_ask", i18n.Args{"n": e.Retries, "error": clip(w.Wait, 300)}),
					Header: workT(l, "header_model"), Options: []proto.QuestionOption{{Label: answerLabel(l, answerRetry)}, {Label: answerLabel(l, answerSkip)}}}}}
			break
		}
		after := retryBackoff(cfg, e.Retries, rateLimitError(c.TurnError))
		at := now.Add(after).UTC()
		e.WakeAt, w.WakeAt, w.Attempts, w.Max = &at, &at, e.Retries, maxRetries(cfg)
		s.logf("work %s/%s: auto-continue: transient model error, attempt %d/%d in %s: %s", e.MachineID, short(e.SessionID),
			e.Retries+1, maxRetries(cfg), after, clip(w.Wait, 120))
		return
	case proto.WaitExternal, proto.WaitAgent:
		if w.ResetsAt != nil {
			if !actOnLimit(c, e, sw.AutoContinue, l, now) {
				if sw.Enabled && !sw.AutoContinue && !c.TurnErrorResumes {
					s.workWould(k, handAutoContinue) // only the switch kept it from being continued at the reset
				}
				return
			}
			break
		}
		if w.Wait == "" && w.Agent == nil {
			return // a background task or a /loop: it wakes itself
		}
		s.work.mu.Lock()
		a, approved := s.work.approved[k]
		s.work.mu.Unlock()
		if risky(w.Then) && !(approved && a.covers(w)) {
			return // no automatic external action, and no card for a step the agent did not ask about
		}
		if !sw.Wake {
			if sw.Enabled {
				s.workWould(k, handWake) // a named wait with no risky step: only the switch kept it from being woken
			}
			return
		}
		if e.Wakes >= maxWakes(cfg) {
			*w = proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, TurnAt: w.TurnAt, At: w.At, By: w.By, Wait: w.Wait, Then: w.Then,
				Line: clip(oneLine(workT(l, "wakes_line", i18n.Args{"wait": w.Wait, "n": e.Wakes})), 60),
				Questions: []proto.Question{{Question: workT(l, "wakes_ask", i18n.Args{"wait": w.Wait, "n": e.Wakes}), Header: workT(l, "header_wakes"),
					Options: []proto.QuestionOption{{Label: answerLabel(l, answerWait)}, {Label: answerLabel(l, answerStop)}}}}}
			break
		}
		at := now.Add(wakeAfter(w.Every, e.Wakes)).UTC()
		e.WakeAt, w.WakeAt, w.Attempts, w.Max = &at, &at, e.Wakes, maxWakes(cfg)
		return
	}
	// Missing evidence is not a user decision. Authorization links have their own relay and manual card path.
	if w.On == proto.WaitUser && w.By == "model" && len(w.Questions) == 0 && w.Link == nil {
		*w = proto.WorkState{TurnAt: w.TurnAt, At: w.At, By: w.By, Artifacts: w.Artifacts}
		return
	}
	// On the user: a card. Without the triage none: the session shows its error and the apps' Retry as before.
	if w.On != proto.WaitUser || !sw.Enabled || !sw.Decide || v.isMuted(machine, c.SessionID) {
		if w.On == proto.WaitUser && sw.Enabled && !sw.Decide && w.Link == nil && !v.isMuted(machine, c.SessionID) {
			s.workWould(k, handDecide) // only the switch kept this stop from being a card
		}
		return
	}
	if len(w.Questions) == 0 {
		w.Questions = []proto.Question{{Question: w.Line, Options: []proto.QuestionOption{{Label: answerLabel(l, answerOK)}, {Label: answerLabel(l, answerSkip)}}}}
	}
	why := ""
	if w.Link != nil {
		var done bool
		if done, why = s.relayAuth(ctx, machine, c, w); done {
			return // authorized with the user's account, and the agent told (agents_auth_relay.go)
		}
	}
	if w.Link != nil {
		w.Questions = append([]proto.Question{{Question: linkQuestion(w, s.location(ctx), l) + why, Header: workT(l, "header_auth"),
			Options: []proto.QuestionOption{{Label: answerLabel(l, answerAuthed)}, {Label: answerLabel(l, answerNotNow)}}}}, w.Questions...)
		if len(w.Questions) > 4 {
			w.Questions = w.Questions[:4]
		}
	}
	e.InteractionID = s.openCard(machine, c, *w)
	w.InteractionID = e.InteractionID
}

// actOnLimit decides about a usage-limit wait (docs/assistant/agent-work.md "Usage limits"): none of a wait's risky
// checks or wakes, nothing its next step could set off — it goes on with what the limit stopped. The CLI that goes on
// by itself at the reset is left to it: typing into it would cancel that. Otherwise, with auto_continue on, the
// manager continues it a minute or so after the reset, up to maxLimitContinues times with no productive turn between
// (a reset read too early is refused again at once); then the user is asked. card: e's work is the user's now.
func actOnLimit(c proto.Session, e *db.SessionWork, autoContinue bool, l i18n.Lang, now time.Time) (card bool) {
	w := &e.Work
	if c.TurnErrorResumes || !autoContinue {
		return false
	}
	if e.Retries >= maxLimitContinues {
		*w = proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, TurnAt: w.TurnAt, At: w.At, By: w.By, ResetsAt: w.ResetsAt,
			Line: clip(oneLine(workT(l, "limit_again_line", i18n.Args{"n": e.Retries})), 60),
			Questions: []proto.Question{{Question: workT(l, "limit_again_ask", i18n.Args{"n": e.Retries, "error": clip(oneLine(c.TurnError), 300)}),
				Header: workT(l, "header_limit"), Options: []proto.QuestionOption{{Label: answerLabel(l, answerRetry)}, {Label: answerLabel(l, answerSkip)}}}}}
		return true
	}
	at := limitContinueAt(*w.ResetsAt, now)
	e.WakeAt, w.WakeAt, w.Attempts, w.Max = &at, &at, e.Retries, maxLimitContinues
	return false
}

// wakeAfter: the first wake after checkMinutes (2–240, else 10), then each one twice as late, at most an hour (or
// checkMinutes, when that is longer).
func wakeAfter(checkMinutes, wakes int) time.Duration {
	m := checkMinutes
	if m < 2 || m > 240 {
		m = 10
	}
	d := time.Duration(m) * time.Minute * time.Duration(math.Pow(2, float64(wakes)))
	if wakes > 0 && d > time.Hour {
		d = max(time.Hour, time.Duration(m)*time.Minute)
	}
	return d
}

func linkQuestion(w *proto.WorkState, loc *time.Location, l i18n.Lang) string {
	if w.Link.ExpiresAt != nil {
		return workT(l, "auth_link_until", i18n.Args{"url": w.Link.URL, "time": w.Link.ExpiresAt.In(loc).Format("15:04")})
	}
	return workT(l, "auth_link", i18n.Args{"url": w.Link.URL})
}

// workCard is the card asking w's questions about c (shown and pushed like any question, answered with
// manager.action); its answer goes to the agent.
func workCard(machine string, c proto.Session, w proto.WorkState, id string) proto.Interaction {
	ix := proto.Interaction{
		ID: id, MachineID: machine, SessionID: c.SessionID, SessionTitle: agentLabel(c), Cwd: c.Cwd,
		Kind: proto.IxQuestion, ToolName: workCardTool, Summary: w.Line, Questions: w.Questions, CanCancel: true,
		CreatedAt: time.Now().UTC(), ExpiresAt: w.TurnAt.Add(workCardTTL).UTC(),
	}
	if c.Agent != "" && c.Agent != "claude" {
		ix.Agent, ix.AgentLabel = c.Agent, c.AgentLabel
	}
	return ix
}

// openCard puts w's questions on a card for c (the next snapshot carries it). Returns the card's id.
func (s *Service) openCard(machine string, c proto.Session, w proto.WorkState) string {
	ix := workCard(machine, c, w, workCardPrefix+randomHex(6))
	s.work.mu.Lock()
	s.work.init()
	s.work.cards[ix.ID] = ix
	s.work.mu.Unlock()
	s.workEvent(machine, c.SessionID, "asked", map[string]any{"line": w.Line, "questions": len(w.Questions)})
	return ix.ID
}

// workCardPrefix starts the id of every card the manager opens.
const workCardPrefix = "work_"

// errGone is an answer to a card or question that is not there any more (answered, withdrawn, expired): final, the
// hub does not keep it to try again.
func errGone(id string) error {
	return &rpc.Error{Code: CodeGone, Message: "no card or question " + id + " any more"}
}

// workAnswer is manager.action for one of the cards (false: id is none of them). What the agent is to hear goes to it;
// the answers the manager acts on itself ("Not now", "Wait a bit"…) do not. The work is read back first: an answer the hub kept
// while the manager was away comes right after it is back. When the agent cannot be sent the answer (its machine just
// went away) the card goes up again, under a new id. A repeated callback while an answer is being sent is answered OK
// and delivered once: the send can take until its timeout, and the entry still names the card until it comes back.
func (s *Service) workAnswer(ctx context.Context, id string, r proto.Resolution) (bool, error) {
	if !strings.HasPrefix(id, workCardPrefix) {
		return false, nil
	}
	if r.Cancel && !r.IsQuestionCancel() {
		return true, errInvalidQuestionCancel
	}
	if !s.loadWork(ctx) {
		return true, errors.New("the agents' work could not be read yet") // not final: the hub tries again
	}
	s.work.mu.Lock()
	s.work.init()
	if s.work.answering[id] { // this callback's answer is already on its way to the agent
		s.work.mu.Unlock()
		return true, nil
	}
	ix, ok := s.work.cards[id]
	delete(s.work.cards, id)
	if !ok { // read back but not up again yet (its machine was away): its turn's work still has it
		for _, e := range s.work.entries {
			if e.InteractionID == id {
				ix, ok = workCard(e.MachineID, proto.Session{SessionID: e.SessionID}, e.Work, id), true
				break
			}
		}
	}
	if ok { // claimed until the answer is delivered, so a repeated callback cannot send it again
		s.work.answering[id] = true
	}
	s.work.mu.Unlock()
	if !ok {
		return true, errGone(id)
	}
	defer func() {
		s.work.mu.Lock()
		delete(s.work.answering, id)
		s.work.mu.Unlock()
	}()
	key := [2]string{ix.MachineID, ix.SessionID}
	turn := ix.ExpiresAt.Add(-workCardTTL)
	after := time.Since(turn).Seconds()
	// done takes the card off its turn's work as the work is now (the agent may have gone on meanwhile), with change.
	done := func(change func(e *db.SessionWork)) {
		s.work.mu.Lock()
		delete(s.work.cards, ix.ID) // a tick may have put it back while the answer was on its way to the agent
		s.work.mu.Unlock()
		e, ok := s.workEntry(key)
		if ok && e.InteractionID == ix.ID {
			e.InteractionID, e.Work.InteractionID = "", ""
			if change != nil {
				change(&e)
			}
			s.putWork(ctx, e)
			return
		}
		s.publishSoon() // the card goes from the snapshot all the same
	}
	if r.Cancel || r.Dismiss {
		if err := s.deferWorkCard(ctx, ix, ""); err != nil {
			return true, err
		}
		s.workEvent(ix.MachineID, ix.SessionID, "answered", map[string]any{"after": after, "dismissed": true})
		return true, nil
	}
	type item struct{ question, answer string }
	var items []item
	only := map[string]bool{}
	for _, q := range ix.Questions {
		a := strings.TrimSpace(r.Answers[q.Question])
		if a == "" {
			continue
		}
		items = append(items, item{q.Question, a})
		if deferredWorkOption(q, a) {
			only[answerSkip] = true
		} else {
			only[canonicalAnswer(a)] = true
		}
	}
	s.workEvent(ix.MachineID, ix.SessionID, "answered", map[string]any{"after": after, "answers": len(items)})
	render := func() string {
		var out []agentAnswerItem
		for _, it := range items {
			out = append(out, agentAnswerItem{it.question, it.answer})
		}
		if r.AnsweredBy == proto.AnsweredByAssistant {
			return renderDelegatedAnswer(out, r.Reason)
		}
		return renderAnswer(out)
	}
	if !only[answerGo] {
		s.work.mu.Lock()
		delete(s.work.approved, key) // any other answer ("Don't do it yet", "Not now"…) takes an earlier yes back
		s.work.mu.Unlock()
	}
	var text string
	var sent func(e *db.SessionWork)
	switch {
	case len(items) == 0:
		done(nil)
		return true, nil
	case len(only) == 1 && (only[answerSkip] || only[answerNotNow] || only[answerStop] || only[answerHold]):
		line := workT(s.lang(ctx), "you_skipped")
		if err := s.deferWorkCard(ctx, ix, line); err != nil {
			return true, err
		}
		return true, nil
	case len(only) == 1 && only[answerWait]:
		l := s.lang(ctx)
		done(func(e *db.SessionWork) {
			at := time.Now().Add(wakeAfter(e.Work.Every, 0)).UTC()
			e.Wakes, e.WakeAt = 0, &at
			e.Work.On, e.Work.WakeAt, e.Work.Attempts, e.Work.Questions = proto.WaitExternal, &at, 0, nil
			e.Work.Line = clip(oneLine(workT(l, "waiting_on", i18n.Args{"wait": e.Work.Wait})), 60)
		})
		return true, nil
	case len(only) == 1 && (only[answerRetry] || only[answerHandled] || only[answerAuthed]):
		text, sent = continueText, func(e *db.SessionWork) { e.Retries = 0 }
	case only[answerGo]:
		if e, ok := s.workEntry(key); ok {
			s.work.mu.Lock()
			s.work.approved[key] = approval{wait: e.Work.Wait, then: e.Work.Then}
			s.work.mu.Unlock()
		}
		text = render()
	default:
		text = render()
	}
	if err := s.sendToSession(ctx, ix.MachineID, ix.SessionID, text); err != nil {
		s.logf("work %s/%s: the answer did not go, the card is up again: %v", ix.MachineID, short(ix.SessionID), err)
		s.workEvent(ix.MachineID, ix.SessionID, "answer_failed", map[string]any{"error": clip(err.Error(), 200)})
		s.work.mu.Lock()
		delete(s.work.approved, key) // the card is asked again: the yes goes with the answer that did not
		s.work.mu.Unlock()
		if e, ok := s.workEntry(key); ok && e.InteractionID == id {
			ix.ID, ix.CreatedAt = workCardPrefix+randomHex(6), time.Now().UTC()
			s.work.mu.Lock()
			delete(s.work.cards, id) // a tick may have put the old card back while the send was failing
			s.work.cards[ix.ID] = ix
			s.work.mu.Unlock()
			e.InteractionID, e.Work.InteractionID = ix.ID, ix.ID
			s.putWork(ctx, e)
		} else {
			s.publishSoon()
		}
		return true, nil
	}
	done(sent)
	return true, nil
}

type agentAnswerItem struct{ question, answer string }

// sendToSession sends text to sid as the manager (the next working is its doing, not the user's).
func (s *Service) sendToSession(ctx context.Context, machine, sid, text string) error {
	s.work.mu.Lock()
	s.work.init()
	s.work.sent[[2]string{machine, sid}] = time.Now()
	s.work.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.call(ctx, machine, DeviceSessionSend, map[string]any{"session_id": sid, "text": text}, nil)
}
