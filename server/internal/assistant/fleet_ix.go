package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The interactions (docs/protocol/protocol.md "Interactions"): the cards the user answers on the phone — a hook's permission, question
// or plan (a node's interaction.create, waited for until it is answered, expires or its node goes; a resumable one
// outlives its node for its hook to claim again), a TUI dialog read off the screen (a "screen" card the manager
// answers itself with keys), a Codex question or approval the node lists, the assistant's own questions and cards,
// and what the hub's own features ask (fleet.ask). They are kept here, recorded in the interactions table, told the
// hub (fleet.interaction / fleet.interaction_resolved), pushed (fleet_push.go), and answered through the hub's
// relayed route (fleet_routes.go).

type pending struct {
	ix   proto.Interaction
	done chan proto.Resolution
	// resolved broadcasts a claimed answer to every overlapping resumable waiter.
	resolved       chan struct{}
	resolution     *proto.Resolution
	persisting     *questionPersistence
	persistenceErr error
	// local is set for interactions the manager resolves itself (screen dialogs, Codex cards): it acts on the answer,
	// and an error goes back to whoever answered (prompt_changed: nothing was pressed).
	local func(proto.Resolution) error
	// orphaned is set while no hook waits on a resumable interaction (its node went away, or the manager restarted):
	// it ends the interaction unless the hook claims it again in time.
	orphaned *time.Timer
	// checkedAt / checking bound transcript reads for a plan answered outside Persona.
	checkedAt time.Time
	checking  bool
	// answeredBy is who resolved it (Resolution.AnsweredBy), so the hub can say the assistant answered, with why
	// (Resolution.Reason) and its answers then (a question's: never a secret).
	answeredBy, answeredReason string
	answeredWith               map[string]string
	// codex: a card a node listed for a Codex TUI; it lasts as long as the node lists it.
	codex bool
	// claimed: an answer was taken for this hook-waited card (resolve). It stays in the map until await or
	// finishInteraction ends it, but a second answer finds it no longer pending.
	claimed bool
	// graceEnded: its grace period ran out (orphanLocked) and it is being ended: a hook back now hands over.
	graceEnded bool
	// expired: it ended because its time ran out, unanswered (await); finished() reads it.
	expired bool
	// announced: a screen card that rang (announce), carried through a detach (screenGone.announced), so the card
	// read again once the node is back rings at the window's end only if its predecessor never did (#4979).
	announced bool
	// told: it is the user's: it went through tell, when the phone is pushed, or it is a screen card back from a
	// detach whose predecessor rang. Not while it is on its way there (recorded, then announced), while the assistant
	// decides on it, nor when it is only put back on the apps (show) by a way back that cannot tell whether it rang
	// before (a restart, a hook asking again for a card only the history has, a Codex card listed again): those count
	// once tell rings them. A hook claiming its card again in the same manager resumes this pending, and keeps it.
	// Persona Link's summary notifies by it (fleet_attention.go).
	told bool
	// route: on its way up to the user through its task's supervisors (ask_route.go); nil once it is the user's.
	route *askRoute
}

// resumeGrace is how long a resumable interaction outlives its node: a little longer than hooks keep reconnecting
// (nodeclient.ResumeWait).
var resumeGrace = 3 * time.Minute

// ixMapLocked is the pending interactions, made on first use. Callers hold s.fleet.mu.
func (s *Service) ixMapLocked() map[string]*pending {
	if s.fleet.interactions == nil {
		s.fleet.interactions = map[string]*pending{}
	}
	return s.fleet.interactions
}

// hasHooks: Persona's hooks run in the session — it sent a hook event since the manager started, or Persona started
// it (with its hooks) — so they ask for most dialogs themselves and a screen card waits for them a little.
func (s *Service) hasHooks(machine string, sess proto.Session) bool {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	return s.fleet.hooked[[2]string{machine, sess.SessionID}] || sess.StartedByPersona
}

// elicitationReachable: an MCP form or link ix can reach the user, as the hub says (fleet.clients): an app that takes
// one is connected, or a phone that registered for them would be pushed one (#2433), and this card would be pushed (no
// muted agent, no quiet task's). Otherwise nothing could show it and it goes back to the terminal at once.
func (s *Service) elicitationReachable(ix proto.Interaction) bool {
	s.fleet.mu.Lock()
	c := s.fleet.clients
	s.fleet.mu.Unlock()
	return c.Elicitation || c.ElicitationPush && !s.quietSession(ix.MachineID, ix.SessionID) && !s.muted(ix.MachineID, ix.SessionID)
}

// ---- telling ----

// announce puts a new card on the apps (through the hub), pushes it, and gives it to the channels. A supervised task's
// card goes to its supervisor first when leaders are configured (ask_route.go); a question the assistant may answer for
// the user goes to it first (fleet_delegate.go).
func (s *Service) announce(ix proto.Interaction) {
	if s.approveStandingInteraction(ix) {
		return
	}
	if !s.agentSupervisedSession(ix.MachineID, ix.SessionID) && s.routeAsk(ix) { // a worker's card stays the user's (task_leader.go)
		return
	}
	if forget := s.delegable(ix); forget != nil {
		s.delegate(ix, forget)
		return
	}
	s.tell(ix)
}

// tell is announce once nothing is in the way: the card on the apps, the push, the channels, or the main conversation
// for a quiet task.
func (s *Service) tell(ix proto.Interaction) {
	ix = s.tellApps(ix)
	if s.quietSession(ix.MachineID, ix.SessionID) {
		// A quiet task's question is no push, bot card or channel message: the main conversation is told (#1731).
		go s.InteractionRaised(context.Background(), InteractionRaised{Interaction: ix})
		return
	}
	s.tellUser(ix, true)
}

// tellApps makes card ix the user's on the apps (pending.told), named with its task; the card as told.
func (s *Service) tellApps(ix proto.Interaction) proto.Interaction {
	s.taskPlanApproval()
	ix = s.withTask(ix)
	s.markTold(ix)
	s.hub.Tell(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: ix})
	s.publishSessions(ix.MachineID)
	s.liveChanged(false) // whether it is told counts for Persona Link's summary, not for the list
	return ix
}

// tellUser rings card ix, already on the apps: the push (push false: one of several going out as one push, ask_route.go)
// and the topic of the agent's todo unless the agent is muted, and the Feishu bot and the channels.
func (s *Service) tellUser(ix proto.Interaction, push bool) {
	s.recordConfirmationNotification(ix)
	if push && !s.muted(ix.MachineID, ix.SessionID) {
		s.notifyInteraction(ix, s.machineName(ix.MachineID), s.linkCovers(ix))
		go s.threadAgentAsks(ix) // and the topic of the todo this agent works on, if it has one
	}
	s.InteractionAnnounced(context.Background(), InteractionAnnounced{Interaction: ix}) // the Feishu bot, the channels
}

// show puts a card on every app without a push: one they were told about already (a way back), or one that is not the
// user's yet (the assistant decides on it; a screen card whose dialog was not read, #4450). It never makes a card the
// user's (pending.told): only tell does.
func (s *Service) show(ix proto.Interaction) {
	s.taskPlanApproval()
	s.hub.Tell(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: s.withTask(ix)})
	s.publishSessions(ix.MachineID)
	s.liveChanged(false) // the card, and whether the assistant decides on it, count for Persona Link's summary
}

// markTold notes that pending card ix is the user's: tell is ringing it (pending.told). Only that card: a newer one
// under the same id (a screen card made again after blocked -> working -> blocked) was not told yet.
func (s *Service) markTold(ix proto.Interaction) {
	s.fleet.mu.Lock()
	if p := s.fleet.interactions[ix.ID]; p != nil && p.ix.CreatedAt.Equal(ix.CreatedAt) {
		p.told = true
	}
	s.fleet.mu.Unlock()
}

// ---- waiting ----

// nodeInteraction is a node's interaction.create: allowed right away when "Always allow in this session" already covers it, else
// shown and waited for.
func (s *Service) nodeInteraction(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	if reservedCard(ix) {
		return proto.Resolution{}, errReservedCard
	}
	// Defense behind the node hook: question-text-keyed answers could not be kept apart on a duplicate card.
	if ix.Kind == proto.IxQuestion && proto.HasDuplicateQuestionText(ix.Questions) {
		return proto.Resolution{}, i18n.Err("hub-hub.duplicate_question")
	}
	// Plan mode enforces a read-only task's launch constraint. Its supervisor report may run once,
	// but exiting plan mode must still be denied. Neither path registers a card or grants permissions.
	report := ix.Kind == proto.IxPermission && slices.Contains(compat.MCPToolNames([]string{"report_to_supervisor"}), ix.ToolName)
	if ix.Kind == proto.IxPlan && ix.ToolName == "ExitPlanMode" || report {
		if todo, ok := s.taskOf(ctx, ix.MachineID, ix.SessionID); ok {
			plans, err := s.conversationPlans(ctx, todo.Origin)
			if err != nil {
				return proto.Resolution{}, err
			}
			for _, plan := range plans {
				if plan.State != planStarted {
					continue
				}
				for _, task := range plan.Tasks {
					if task.TodoID == todo.ID && task.Readonly {
						if report {
							if todo.Supervision == nil || todo.Supervision.Supervisor == "" {
								continue
							}
							allow := true
							return proto.Resolution{Allow: &allow}, nil
						}
						approve := false
						return proto.Resolution{Approve: &approve, Feedback: prompts.RenderReadonlyTaskPlanExit()}, nil
					}
				}
			}
		}
	}
	ix = nodeCard(ix, s.fleetStore() != nil, s.ixInHealth(ctx, ix))
	if r, ok := s.alwaysAllowed(ctx, ix); ok {
		if ix.Resumable { // its hook's earlier attempt left the card pending: it is answered too
			_ = s.resolveRemembered(ix.ID, r, false)
		}
		return r, nil
	}
	return s.waitInteraction(ctx, ix)
}

// waitInteraction registers a hook interaction and blocks until it is resolved, cancelled or expires.
// A resumable one asked again with the same id (its hook reconnected after a manager or node restart) is the same
// interaction: the wait resumes on it, or returns the answer given meanwhile. One the history has as still pending
// (the manager restarted with it on the phone) is shown again without a second push.
func (s *Service) waitInteraction(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	ix = cleanTitle(ix)
	// A Just me turn's card, the node's or the manager's own (ask_user, a confirmation): pushed without its content,
	// never to Lark or a channel (meeting_justme.go).
	ix.Private = ix.Private || s.justMeNow(ix.MachineID, ix.SessionID)
	if ix.ExpiresAt.IsZero() {
		ix.ExpiresAt = time.Now().Add(s.notifyTimings().hookTimeout()).UTC()
	}
	again := false
	if ix.Resumable {
		s.fleet.mu.Lock()
		if p := s.fleet.interactions[ix.ID]; p != nil && p.local == nil {
			if p.graceEnded {
				// The grace period ended and the card is being ended outside the lock (the history, the apps): the
				// hook hands over, as one back just after would (#5043's kind of gap).
				s.fleet.mu.Unlock()
				return proto.Resolution{Dismiss: true}, nil
			}
			if p.orphaned != nil {
				p.orphaned.Stop() // fired but not decided yet: it finds p.orphaned nil and leaves the card to the hook
				p.orphaned = nil
			}
			s.fleet.mu.Unlock()
			if ix.Kind == proto.IxElicitation && !s.elicitationReachable(ix) {
				_ = s.resolve(ix.ID, proto.Resolution{Dismiss: true})
			}
			return s.await(ctx, p)
		}
		s.fleet.mu.Unlock()
		status, r, ended := s.outcome(ix.ID)
		if ended {
			return r, nil
		}
		again = status == "pending"
	}
	if ix.Kind == proto.IxElicitation && !s.elicitationReachable(ix) {
		return proto.Resolution{Dismiss: true}, nil
	}
	p := &pending{ix: ix, done: make(chan proto.Resolution, 1)}
	s.fleet.mu.Lock()
	// A screen interaction for the same session was a premature guess; the hook knows better.
	// Tell the app now: the wait below can last until the hook times out.
	var dropped []*pending
	m := s.ixMapLocked()
	for id, old := range m {
		if old.ix.Kind == proto.IxScreen && old.ix.MachineID == ix.MachineID && old.ix.SessionID == ix.SessionID {
			delete(m, id)
			dropped = append(dropped, old)
		}
	}
	m[ix.ID] = p
	s.fleet.mu.Unlock()
	for _, old := range dropped { // as a Codex card withdraws a screen card
		s.recordEnd(old.ix.ID, "cancelled", nil)
		s.finished(old)
	}
	if again {
		// A card the history has as pending and this manager does not hold was left by a manager that restarted: its
		// node may claim it before the hub's handshake restores the others (restoreInteractions), so it finds its route
		// here too (restoreRoute), or the assistant goes on deciding on it (resumeDelegations). Any other is only shown
		// again, as before.
		if !s.restoreRoute(ctx, ix) && !s.resumeDelegations(ctx, ix) {
			s.show(ix)
		}
	} else {
		s.record(ix)
		s.announce(ix)
	}
	return s.await(ctx, p)
}

// await blocks on a registered interaction. When ctx ends — the node's connection is gone — a resumable
// interaction stays pending for its hook to claim again.
func (s *Service) await(ctx context.Context, p *pending) (proto.Resolution, error) {
	s.fleet.mu.Lock()
	if p.resolved == nil {
		p.resolved = make(chan struct{})
	}
	resolved := p.resolved
	s.fleet.mu.Unlock()
	id := p.ix.ID
	timer := time.NewTimer(time.Until(p.ix.ExpiresAt))
	defer timer.Stop()
	status := "cancelled"
	select {
	case <-resolved:
		s.finishInteraction(id)
		return *p.resolution, nil
	case r := <-p.done:
		s.finishInteraction(id)
		return r, nil
	case <-timer.C:
		status = "expired"
	case <-ctx.Done():
	}
	// Decide under the same lock as resolve: an answer already claimed must be
	// persisted before any waiter can record a competing timeout or disconnect.
	s.fleet.mu.Lock()
	if p.persisting != nil {
		attempt := p.persisting
		s.fleet.mu.Unlock()
		<-attempt.done
		if attempt.err != nil {
			return proto.Resolution{}, attempt.err
		}
		<-resolved
		s.finishInteraction(id)
		return *p.resolution, nil
	}
	if p.persistenceErr != nil {
		err := p.persistenceErr
		s.fleet.mu.Unlock()
		return proto.Resolution{}, err
	}
	if p.claimed {
		hasAnswer := p.resolution != nil
		s.fleet.mu.Unlock()
		if hasAnswer {
			<-resolved
			s.finishInteraction(id)
			return *p.resolution, nil
		}
		return proto.Resolution{Dismiss: true}, nil
	}
	if s.fleet.interactions[id] != p {
		s.fleet.mu.Unlock()
		return proto.Resolution{Dismiss: true}, nil
	}
	if status == "cancelled" && p.ix.Resumable && s.orphanLocked(p) {
		s.fleet.mu.Unlock()
		return proto.Resolution{Dismiss: true}, nil // disconnected hook; a new one can resume
	}
	p.claimed, p.graceEnded = true, true
	p.expired = status == "expired"
	s.fleet.mu.Unlock()
	s.recordEnd(id, status, nil)
	s.finishInteraction(id)
	return proto.Resolution{Dismiss: true}, nil
}

// orphanLocked starts p's grace period: it ends (cancelled) unless its hook claims it again first. Callers hold
// s.fleet.mu.
func (s *Service) orphanLocked(p *pending) bool {
	if p.orphaned != nil {
		return true
	}
	wait := min(resumeGrace, time.Until(p.ix.ExpiresAt))
	if wait <= 0 {
		return false
	}
	p.orphaned = time.AfterFunc(wait, func() {
		s.fleet.mu.Lock()
		// Claimed under the lock that decides it, as resolve claims an answer: an answer being written now is not
		// cancelled over, and one given while this end is written finds the card no longer pending.
		still := s.fleet.interactions[p.ix.ID] == p && p.orphaned != nil && !p.claimed
		if still {
			p.claimed, p.graceEnded = true, true
		}
		s.fleet.mu.Unlock()
		if still {
			s.recordEnd(p.ix.ID, "cancelled", nil)
			s.finishInteraction(p.ix.ID)
		}
	})
	return true
}

// outcome reports how a resumable interaction no longer pending here ended: the answer given before its hook could
// read it (the manager or node restarted right then), or a dismiss when it was cancelled or expired. status is the
// history's ("pending": recorded, never ended).
func (s *Service) outcome(id string) (status string, r proto.Resolution, ended bool) {
	st := s.fleetStore()
	if st == nil {
		return "", proto.Resolution{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	status, res, err := st.InteractionOutcome(ctx, id)
	switch {
	case err != nil || status == "" || status == "pending":
		return status, proto.Resolution{}, false
	case res != nil && (status == "resolved" || status == "cancelled" && res.Cancel):
		return status, *res, true
	default:
		return status, proto.Resolution{Dismiss: true}, true
	}
}

// historyTimeout bounds each interaction history read or write. They run before the apps see a new card and before
// a hook hears its answer, so a slow or locked database must not hold either back for longer than this.
var historyTimeout = 5 * time.Second

// record / recordEnd keep the interaction history in the database (best effort, within historyTimeout).
func (s *Service) record(ix proto.Interaction) {
	st := s.fleetStore()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	if err := st.InsertInteraction(ctx, ix); err != nil {
		s.logf("record interaction: %v", err)
	}
}

func (s *Service) recordEnd(id, status string, r *proto.Resolution) {
	st := s.fleetStore()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	if err := st.FinishInteraction(ctx, id, status, r); err != nil {
		s.logf("record interaction outcome: %v", err)
	}
}

// errNotPending: the interaction was answered, withdrawn or expired already.
var errNotPending = errors.New("no longer pending")

// answeredJustNow is errNotPending for a card another answer claimed a moment ago. That answer reaches the history
// only after the claim (recordEnd), so whoever asks in between is told it from the claim (#1230).
type answeredJustNow struct {
	id     string
	status string
	kept   proto.Resolution // as the history keeps it
}

func (e *answeredJustNow) Error() string {
	return fmt.Sprintf("interaction %s is %v", e.id, errNotPending)
}
func (e *answeredJustNow) Unwrap() error { return errNotPending }

// endStatus is how an answer ends its card in the history.
func endStatus(r proto.Resolution) string {
	switch {
	case r.Cancel:
		return "cancelled"
	case r.Dismiss:
		return "dismissed"
	}
	return "resolved"
}

var errInvalidElicitation = errors.New("elicitation requires action accept, decline or cancel")

// resolve answers an interaction. A screen card acts right away and its error comes back (the dialog changed:
// nothing pressed, a new card follows).
func (s *Service) resolve(id string, r proto.Resolution) error {
	return s.resolveRemembered(id, r, true)
}

// Automatically replayed grants must not write themselves back after a concurrent revocation.
func (s *Service) resolveRemembered(id string, r proto.Resolution, remember bool) error {
	return s.resolveWithStanding(id, r, remember, nil)
}

func (s *Service) resolveWithStanding(id string, r proto.Resolution, remember bool, authorization *standingResolution) error {
	s.fleet.mu.Lock()
	p := s.fleet.interactions[id]
	if p != nil && r.Cancel && (!p.ix.CanCancel || p.ix.Kind != proto.IxQuestion || !r.IsQuestionCancel()) {
		s.fleet.mu.Unlock()
		return errInvalidQuestionCancel
	}
	if p != nil && p.ix.Kind == proto.IxElicitation && !r.Dismiss &&
		r.Action != "accept" && r.Action != "decline" && r.Action != "cancel" {
		s.fleet.mu.Unlock()
		return errInvalidElicitation
	}
	if p != nil && r.Allow != nil && *r.Allow {
		for _, f := range p.ix.Fields {
			if !f.Optional && strings.TrimSpace(r.Values[f.Key]) == "" {
				s.fleet.mu.Unlock()
				return i18n.Err("hub-hub.fill_in", i18n.Args{"field": f.Label})
			}
		}
	}
	if p != nil {
		r.Values = declaredValues(p.ix, r.Values)
		r = honest(p.ix, r)
	}
	if p != nil && p.persisting != nil {
		attempt := p.persisting
		s.fleet.mu.Unlock()
		<-attempt.done
		if attempt.err != nil {
			return attempt.err
		}
		return fmt.Errorf("interaction %s is %w", id, errNotPending)
	}
	var first *answeredJustNow
	if p != nil && p.claimed {
		if p.resolution != nil {
			first = &answeredJustNow{id: id, status: endStatus(*p.resolution), kept: redacted(p.ix, *p.resolution)}
		}
		p = nil // answered a moment ago: the first answer is the one the hook and the history get
	}
	if p != nil && p.local != nil && authorization != nil {
		s.fleet.mu.Unlock()
		return fmt.Errorf("standing authorizations require a hook-waited card")
	}
	if p != nil && p.local != nil {
		delete(s.fleet.interactions, id) // claimed: a second answer to the same card is not pending any more
		if p.ix.Kind == proto.IxScreen && !r.Dismiss {
			// Marked before the keys go: the node's next list may come while they are on the way (#4391).
			if s.fleet.screenAnswered == nil {
				s.fleet.screenAnswered = map[string]answeredScreen{}
			}
			s.fleet.screenAnswered[id] = answeredScreen{machine: p.ix.MachineID, session: p.ix.SessionID,
				until: time.Now().Add(screenAnsweredQuiet)}
		} else if p.ix.Kind == proto.IxScreen {
			// Handed back to the computer: no key goes, the dialog stays, and the next list must not ask it again.
			if s.fleet.screenHandedBack == nil {
				s.fleet.screenHandedBack = map[string]screenGone{}
			}
			s.fleet.screenHandedBack[id] = screenGone{machine: p.ix.MachineID, session: p.ix.SessionID, at: time.Now()}
		}
	} else if p != nil {
		p.claimed = true
		p.resolution = &r
		if durableQuestion(p.ix) || authorization != nil {
			p.persisting = &questionPersistence{done: make(chan struct{})}
			p.persistenceErr = nil
		}
		if p.resolved == nil {
			p.resolved = make(chan struct{})
		}
	}
	if p != nil {
		p.answeredBy, p.answeredReason = r.AnsweredBy, r.Reason // read by finished(), under this lock
		if r.AnsweredBy != "" {
			p.answeredWith = r.Answers
		}
	}
	s.fleet.mu.Unlock()
	if first != nil {
		return first
	}
	if p == nil {
		return fmt.Errorf("interaction %s is %w", id, errNotPending)
	}
	status := endStatus(r)
	// Everyone told of the answer (the channels, the task feeds) gets its secret fields masked, as the history keeps them;
	// only the one waiting on the card (p.local, p.done) gets what was typed into them.
	told := redacted(p.ix, r)
	if p.local != nil {
		err := p.local(r)
		if err != nil {
			status = "failed"
			s.fleet.mu.Lock()
			back := s.fleet.interactions[id] == p
			s.fleet.mu.Unlock()
			if back { // a Codex card the node could not answer is open again (codexPending): it is not over
				go s.InteractionResolved(context.Background(), InteractionResolved{Interaction: p.ix, Resolution: told, Status: status})
				return err
			}
		} else {
			s.channelsEnded(p.ix, &told)
		}
		// What was typed into a terminal may be a password: the history keeps that something was, not what.
		kept := told
		if kept.Text != "" {
			kept.Text = redactedValue
		}
		s.recordEnd(id, status, &kept)
		s.finished(p)
		go s.InteractionResolved(context.Background(), InteractionResolved{Interaction: p.ix, Resolution: told, Status: status})
		return err
	}
	if authorization != nil {
		if err := s.persistStandingInteraction(p, told, authorization); err != nil {
			return err
		}
	} else if durableQuestion(p.ix) {
		if err := s.persistQuestion(p, status, told); err != nil {
			return err
		}
	} else {
		s.recordEnd(id, status, &told)
	}
	s.channelsEnded(p.ix, &told)
	go s.InteractionResolved(context.Background(), InteractionResolved{Interaction: p.ix, Resolution: told, Status: status})
	if remember {
		s.keepAlways(p.ix, r) // kept before the hook hears: its next card may follow at once
	}
	close(p.resolved)
	select {
	case p.done <- r:
	default:
	}
	s.finishInteraction(id)
	return nil
}

// channelsEnded tells the channels a card is over: r is how it was answered (nil: withdrawn, expired, or over after
// the answer that was told already).
func (s *Service) channelsEnded(ix proto.Interaction, r *proto.Resolution) {
	if f := s.fleetEnded; f != nil {
		f(ix, r)
	}
	s.InteractionEnded(context.Background(), InteractionEnded{Interaction: ix, Resolution: r})
}

// redactedValue stands for what was typed into a secret field (or a terminal) wherever the answer is kept or told.
const redactedValue = "<redacted>"

// redacted is the resolution as the history keeps it: secret fields say they were filled in, not what with. A secret
// card's typed text is masked here too; a terminal's other text reaches the channels as typed and only the history masks
// it (resolveRemembered's kept).
func redacted(ix proto.Interaction, r proto.Resolution) proto.Resolution {
	if ix.Secret && r.Text != "" {
		r.Text = redactedValue
	}
	if len(r.Values) > 0 {
		secret := map[string]bool{}
		for _, f := range ix.Fields {
			secret[f.Key] = f.Secret
		}
		vals := make(map[string]string, len(r.Values))
		for k, v := range r.Values {
			if secret[k] || secretName.MatchString(k) {
				v = redactedValue
			}
			vals[k] = v
		}
		r.Values = vals
	}
	if len(r.Content) > 0 {
		var schema struct {
			Properties map[string]struct {
				Format    string `json:"format"`
				WriteOnly bool   `json:"writeOnly"`
			} `json:"properties"`
		}
		if ix.Elicitation != nil {
			_ = json.Unmarshal(ix.Elicitation.RequestedSchema, &schema)
		}
		content := make(map[string]any, len(r.Content))
		for k, v := range r.Content {
			p := schema.Properties[k]
			if p.Format == "password" || p.WriteOnly || secretName.MatchString(k) {
				v = redactedValue
			}
			content[k] = v
		}
		r.Content = content
	}
	return r
}

// declaredValues are the values of the fields ix declares; any other key is dropped as the answer comes in: nothing
// reads one, and the history and everyone told of the answer would get it as typed.
func declaredValues(ix proto.Interaction, values map[string]string) map[string]string {
	if len(values) == 0 {
		return values
	}
	var out map[string]string
	for _, f := range ix.Fields {
		if v, ok := values[f.Key]; ok {
			if out == nil {
				out = map[string]string{}
			}
			out[f.Key] = v
		}
	}
	return out
}

// hasSecretField: ix asks for something the history redacts (a password).
func hasSecretField(ix proto.Interaction) bool {
	for _, f := range ix.Fields {
		if f.Secret {
			return true
		}
	}
	return false
}

func (s *Service) finishInteraction(id string) {
	s.fleet.mu.Lock()
	p, ok := s.fleet.interactions[id]
	delete(s.fleet.interactions, id)
	if ok && p.orphaned != nil {
		p.orphaned.Stop()
	}
	s.fleet.mu.Unlock()
	if ok {
		s.finished(p)
	}
}

// finished tells everyone an interaction, already out of the pending set, is over.
func (s *Service) finished(p *pending) {
	s.taskPlanApproval()
	s.fleet.mu.Lock()
	done := assistantwire.FleetResolved{ID: p.ix.ID, AnsweredBy: p.answeredBy}
	if done.AnsweredBy != "" {
		done.Reason, done.Answers = p.answeredReason, p.answeredWith
	}
	var how *proto.Resolution
	if p.expired && p.ix.ToolName == "ask_user" { // the assistant's question becomes a to-do (ask_todo.go): its card says so
		how = &proto.Resolution{Dismiss: true, Reason: proto.DismissedExpired}
	}
	s.fleet.mu.Unlock()
	s.channelsEnded(p.ix, how)
	// the app marks a question the assistant answered, and why (docs/assistant/assistant.md)
	s.hub.Tell(assistantwire.EventFleetResolved, done)
	s.publishSessions(p.ix.MachineID)
}

// pendingInteractions are the pending interactions, oldest first, with the task each agent works for.
func (s *Service) pendingInteractions() []proto.Interaction {
	out, _ := s.pendingCards()
	return out
}

// pendingCards is pendingInteractions and, read with them, the ids of those that are the user's (pending.told).
func (s *Service) pendingCards() ([]proto.Interaction, map[string]bool) {
	s.fleet.mu.Lock()
	out := []proto.Interaction{}
	told := map[string]bool{}
	for _, p := range s.fleet.interactions {
		out = append(out, p.ix)
		if p.told {
			told[p.ix.ID] = true
		}
	}
	s.fleet.mu.Unlock()
	for i := range out {
		out[i] = s.withTask(out[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, told
}

// pendingIx is one pending interaction; false when it is not pending.
func (s *Service) pendingIx(id string) (proto.Interaction, bool) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	if p := s.fleet.interactions[id]; p != nil {
		return p.ix, true
	}
	return proto.Interaction{}, false
}

func (s *Service) expireLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.retrySeen(ctx) // a database that failed when the manager started
		s.fleet.mu.Lock()
		var plans []string
		seen := map[string]bool{}
		for _, p := range s.fleet.interactions {
			if terminalReconciled(p) && !seen[p.ix.MachineID] {
				plans = append(plans, p.ix.MachineID)
				seen[p.ix.MachineID] = true
			}
		}
		s.fleet.mu.Unlock()
		for _, m := range plans {
			s.syncPlanInteractions(m) // also converge after a lost rev/status event
		}
		s.fleet.mu.Lock()
		var ids []string
		for id, p := range s.fleet.interactions {
			if p.local != nil && time.Now().After(p.ix.ExpiresAt) {
				ids = append(ids, id)
			}
		}
		s.fleet.mu.Unlock()
		for _, id := range ids {
			s.finishInteraction(id)
		}
	}
}

// errReservedCard: a card from a node (a hook, a runtime, a daemon, a Codex list) or an extension may not pass for the
// password card or the hand-off card: only the assistant's secret_run tool makes the one (secret_run.go) and only the
// manager's Agent start the other, and the clients show them as those cards, the password card with no Agent's name on it.
var errReservedCard = errors.New("the tool names " + proto.SecretRunTool + " and " + proto.SecretHandoffTool +
	" are reserved for the manager's password and hand-off cards")

// reservedCard: ix calls itself the password card or the hand-off card.
func reservedCard(ix proto.Interaction) bool {
	return ix.ToolName == proto.SecretRunTool || ix.ToolName == proto.SecretHandoffTool
}

// askHub is fleet.ask: a card one of the hub's own features asks (a mail send, Cua's apps, persona_settings),
// waited for like a hook's.
func (s *Service) askHub(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	if reservedCard(ix) {
		return proto.Resolution{}, errReservedCard
	}
	if ix.ID == "" {
		ix.ID = "ix_" + randomHex(6)
	}
	if ix.CreatedAt.IsZero() {
		ix.CreatedAt = time.Now().UTC()
	}
	if m, ok := s.fleetMachine(ix.MachineID); ok {
		ix.MachineID = m.ID
	}
	return s.waitInteraction(ctx, s.ownCard(ctx, ix))
}

// ---- "Always allow in this session" (#2202, docs/assistant/assistant.md "Always allow in this session") ----

// The node names what a card grants (proto.Interaction.AlwaysKeys, only on assistant conversations). The manager
// remembers explicit Always answers and rechecks every later card against the stored keys, including after a
// process restart. Nodes must not cache session grants that would bypass a subsequent revocation (#5867).

// maxAlwaysKeys bounds what one card may name (a file change's paths, a gated command's calls).
const maxAlwaysKeys = 32

// Whether a card's "Always allow" is kept is the manager's word (proto.Interaction.AlwaysHonored, #3590), never a
// node's: the apps offer it only where it is.

// nodeCard is a node's card as the apps get it. On an assistant conversation only the manager keeps Always (its
// agent is given no session rule): honored when the manager will remember the keys the card names (remembering is a
// conversation's store), not when it names keys it will not (too many, a card with fields, no store) or is a health
// conversation's (confirmed each time). Any other node card keeps the apps' own rule (an Agents session's agent keeps
// it itself). A node names no tool for people, and has no say in either: only the manager's own cards do.
func nodeCard(ix proto.Interaction, remembers, health bool) proto.Interaction {
	ix.AlwaysHonored, ix.ToolTitle = nil, ""
	if ix.Kind != proto.IxPermission || ix.SessionID == "" || (len(ix.AlwaysKeys) == 0 && !health) {
		return ix
	}
	kept := remembers && !health && alwaysCard(ix)
	ix.AlwaysHonored = &kept
	return ix
}

// honest is r without an Always the card does not keep (AlwaysHonored false): an older app, or one that does not
// read the field, may still send it, and the answer's history must not say "always allowed" for a grant no one keeps.
func honest(ix proto.Interaction, r proto.Resolution) proto.Resolution {
	if ix.AlwaysHonored != nil && !*ix.AlwaysHonored {
		r.Always = false
	}
	return r
}

// ownCard is a permission card the manager asks itself (its tools', the extensions', the hub's persona_settings): its
// Always is kept only where the card says what it keeps (AlwaysLabel: the apps a computer may use, LCU's own scopes),
// nowhere else, and the tool gets its name for people when the catalog has one.
func (s *Service) ownCard(ctx context.Context, ix proto.Interaction) proto.Interaction {
	if ix.Kind != proto.IxPermission {
		return ix
	}
	kept := strings.TrimSpace(ix.AlwaysLabel) != ""
	ix.AlwaysHonored = &kept
	if ix.ToolTitle == "" {
		ix.ToolTitle = toolTitle(s.lang(ctx), ix.ToolName)
	}
	return ix
}

// toolTitle is one of the manager's own tools by its name for people ("iPhone control"), "" for any other.
func toolTitle(l i18n.Lang, tool string) string {
	key := "assistant-tool_titles." + strings.ReplaceAll(tool, ".", "_")
	if tool == "" || !i18n.Has(key) {
		return ""
	}
	return i18n.T(l, key)
}

func alwaysCard(ix proto.Interaction) bool {
	return ix.Kind == proto.IxPermission && ix.SessionID != "" && len(ix.AlwaysKeys) > 0 &&
		len(ix.AlwaysKeys) <= maxAlwaysKeys && len(ix.Fields) == 0
}

// alwaysAllowed: every key ix names was granted with "Always allow in this session" in its conversation.
func (s *Service) alwaysAllowed(ctx context.Context, ix proto.Interaction) (proto.Resolution, bool) {
	st := s.fleetStore()
	if st == nil || !alwaysCard(ix) {
		return proto.Resolution{}, false
	}
	ok, err := st.AlwaysAllowed(ctx, ix.SessionID, ix.AlwaysKeys)
	if err != nil {
		s.logf("[%s] 本会话总是允许: %v", ix.MachineID, err)
		return proto.Resolution{}, false
	}
	if !ok {
		return proto.Resolution{}, false
	}
	s.logf("[%s] %s allowed: 本会话总是允许 in this conversation", ix.MachineID, ix.ToolName)
	yes := true
	return proto.Resolution{Allow: &yes, Always: true, Remembered: true}, true
}

// keepAlways keeps what a card answered with "Always allow in this session" grants, then answers the conversation's other cards
// waiting meanwhile that it covers now (parallel tool calls ask at once). It runs before the hook hears the answer,
// so each database call is bounded by historyTimeout: a grant that cannot be kept is only logged.
func (s *Service) keepAlways(ix proto.Interaction, r proto.Resolution) {
	st := s.fleetStore()
	if st == nil || !alwaysCard(ix) || r.Allow == nil || !*r.Allow || !r.Always {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	err := st.AllowAlways(ctx, ix.SessionID, ix.ToolName, ix.AlwaysKeys)
	cancel()
	if err != nil {
		s.logf("[%s] keep 本会话总是允许: %v", ix.MachineID, err)
		return
	}
	s.fleet.mu.Lock()
	var waiting []proto.Interaction
	for _, p := range s.fleet.interactions {
		if p.ix.ID != ix.ID && p.ix.SessionID == ix.SessionID && p.ix.MachineID == ix.MachineID &&
			p.local == nil && !p.codex && !p.claimed && alwaysCard(p.ix) {
			waiting = append(waiting, p.ix)
		}
	}
	s.fleet.mu.Unlock()
	for _, w := range waiting {
		ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
		res, ok := s.alwaysAllowed(ctx, w)
		cancel()
		if ok {
			_ = s.resolveRemembered(w.ID, res, false)
		}
	}
}

// ---- a node gone, the manager restarted ----

// NodeDetached: a machine's node connection ended (devices.Options.Detached): the cards waiting on it that cannot be
// claimed again end (resumable ones wait for their hook through the next connection), its list shows it.
func (s *Service) NodeDetached(machine string) {
	if !s.fleetOn() {
		return
	}
	s.cancelMachineInteractions(machine)
	s.sessionsChanged(machine)
}

func (s *Service) cancelMachineInteractions(machineID string) {
	s.fleet.mu.Lock()
	var ids []string
	now := time.Now()
	for id, p := range s.fleet.interactions {
		// Resumable ones wait for their hook to come back through the next node (await).
		if p.ix.MachineID == machineID && !p.ix.Resumable {
			ids = append(ids, id)
			if p.ix.Kind == proto.IxScreen { // its dialog may still be there when the node is back (screenBackLocked)
				if s.fleet.screenDetached == nil {
					s.fleet.screenDetached = map[string]screenGone{}
				}
				s.fleet.screenDetached[id] = screenGone{machine: machineID, session: p.ix.SessionID, at: now, announced: p.announced,
					told: p.told}
			}
		}
	}
	s.fleet.mu.Unlock()
	s.feishuDetach(ids)
	for _, id := range ids {
		s.finishInteraction(id)
	}
}

// restoreInteractions takes back, as the manager starts, the resumable cards the last one left pending: their hooks
// reconnect through the next node and claim them again (waitInteraction), without a second push; the others it left
// are over (cancelled in the history), and the hub is told so: a hub that took over from the one they were shown by
// does not know them, and an app that resumed its event stream across the switch would keep them up, next to the
// question asked again (#4568). A card that was on its way up to the user finds its route again (restoreRoute); one
// the assistant was deciding on goes on (resumeDelegations). Both before the hub gets the full list (FleetUp).
func (s *Service) restoreInteractions(ctx context.Context) {
	st, ok := s.store().(interface {
		ExpireStaleInteractions(ctx context.Context) ([]proto.Interaction, []string, error)
	})
	if !ok {
		return
	}
	resumable, cancelled, err := func() (out []proto.Interaction, cancelled []string, err error) {
		defer guard(&err) // a test's store may not keep the table
		return st.ExpireStaleInteractions(ctx)
	}()
	if err != nil {
		s.logf("expire stale interactions: %v", err)
		return
	}
	for _, id := range cancelled {
		s.hub.Tell(assistantwire.EventFleetResolved, assistantwire.FleetResolved{ID: id})
	}
	var back []proto.Interaction
	s.fleet.mu.Lock()
	m := s.ixMapLocked()
	for _, ix := range resumable {
		if m[ix.ID] == nil {
			m[ix.ID] = &pending{ix: ix, done: make(chan proto.Resolution, 1)} // not told: whether it rang is not kept
			s.orphanLocked(m[ix.ID])
			back = append(back, ix)
		}
	}
	s.fleet.mu.Unlock()
	var unrouted []proto.Interaction
	for _, ix := range back {
		if !s.restoreRoute(ctx, ix) {
			unrouted = append(unrouted, ix)
		}
	}
	s.resumeDelegations(ctx, unrouted...)
}

// restoreRoute puts card ix, restored after a restart, back on its way to the supervisor of the task it belongs to:
// the route lived on the pending card only, and the apps got the card back without it. The ladder is derived now, as
// routeAsk does for a new card, so a card whose task is done or already waits for the user stays the user's. The
// supervisor hears of it as any card held with it (signalAsk). Whether the user was told of the card before the
// restart is not kept: a question that may have rung then only goes back on the apps when its time is up, while an
// approval rings (ringAsks). True when it was routed.
func (s *Service) restoreRoute(ctx context.Context, ix proto.Interaction) bool {
	if !routable(ix) {
		return false
	}
	ladder, todo, home, ok := s.askLadder(ctx, ix.MachineID, ix.SessionID)
	if !ok {
		return false
	}
	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	if p == nil || p.claimed || p.told || !p.ix.CreatedAt.Equal(ix.CreatedAt) {
		s.fleet.mu.Unlock()
		return false
	}
	now := time.Now()
	// A question may have rung the user before the restart: its timeout only puts it back on the apps. An approval is
	// not quiet: only the user can answer it, so its timeout rings (ringAsks) instead of leaving the agent blocked
	// with nobody told.
	p.route = &askRoute{todo: todo, machine: home, ladder: ladder, since: now, held: now, maybeTold: ix.Kind == proto.IxQuestion}
	p.ix.Delegating = true
	shown := p.ix
	s.fleet.mu.Unlock()
	s.show(shown)
	s.signalAsk(todo, ladder[0], ix, 0)
	return true
}
