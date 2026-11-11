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

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Asks go to the supervisor first (docs/assistant/unified-entry.md "Asks go to the supervisor first"). With leader
// conversations configured (assistant.leaders not empty), a card a supervised task's agent raises — a question, a tool
// approval or a plan — goes to the conversation supervising the task, not to the user: the apps show it marked
// delegating, nothing rings, and the supervisor gets it as a supervision entry. The supervisor answers a question
// itself (interaction_answer; the answer becomes the task's decision), or hands what the user must decide up with
// ask_escalate: every card of the task still waiting then rings the user once, with the supervisor's reason and
// recommendation, and a leader's escalation is an escalated entry in the main conversation, so the layers above know.
// Approvals stay the user's: no conversation can allow a tool or approve a plan (interaction_answer refuses them), so
// they reach the user through ask_escalate or the timeout. A card nobody handles within askHoldFor moves one rung up
// (leader, main conversation, the user), so nothing waits on a supervisor for ever.
//
// The route lives on the pending card, not in the store: after a manager restart the card is back on the apps without
// a push (as every card is), and its route is made again from the task's supervisor (restoreRoute) so that the
// supervisor can still answer or escalate it.

// askHoldFor is how long a card stays with one rung of its ladder before it moves up. The time does not count while
// that rung's conversation is on a turn (askTick): a supervisor working through a long round has not neglected the
// card, and a longer fixed hold would run out under a longer turn all the same.
var askHoldFor = 5 * time.Minute

// askBusyMax bounds that: a rung whose conversation is still on a turn this long after the card came is stuck rather
// than working through it, and the card's hold runs again (askTick), so a conversation that never comes back cannot
// keep a card for ever.
var askBusyMax = time.Hour

// askRoute is where a pending card is on its way to the user: the conversations it goes through, nearest first (its
// task's leader, then the main conversation), the one it is with (step; past the last: the user's), and since when.
type askRoute struct {
	todo    string
	machine string // where its rungs are: the assistant's conversations, on the assistant's machine
	ladder  []string
	step    int
	since   time.Time
	held    time.Time // when it reached this rung: askBusyMax counts from here
	// maybeTold: a question card restored after a restart. Whether the user was told of it is not kept, so its
	// timeout only puts it back on the apps (ringAsks), not ringing them a second time. An approval restored so (a
	// permission or a plan) is not: no conversation may answer it, so its timeout rings.
	maybeTold bool
}

func (r *askRoute) at() string {
	if r == nil || r.step >= len(r.ladder) {
		return ""
	}
	return r.ladder[r.step]
}

// escalationBook keeps which cards the user got through an escalation, so their answers become the task's decisions.
type escalationBook struct {
	escalated map[string]escalatedCard // by interaction id
}

type escalatedCard struct {
	todo string
	at   time.Time
}

// escalatedKeep bounds how long an escalated card is remembered unanswered (it expired, was cancelled or its node went).
const escalatedKeep = 48 * time.Hour

// routable: ix is a card a supervisor can decide on or pass up. Terminal dialogs read off the screen, forms, cards
// asking for a password and the assistant's own questions are the user's as they come.
func routable(ix proto.Interaction) bool {
	if len(ix.Fields) > 0 {
		return false
	}
	switch ix.Kind {
	case proto.IxQuestion:
		return len(ix.Questions) > 0 && ix.ToolName != "ask_user" && asksForInput(ix)
	case proto.IxPermission, proto.IxPlan:
		return true
	}
	return false
}

// askLadder is where a card of machine/sid's goes first: the conversations supervising its task, nearest first, the
// task, and the machine those conversations are on (the assistant's: its leaders and its main conversation). None when
// no leader is configured (everything stays as it was), the agent is no supervised task's, the task already waits for
// the user, or the agent is a supervising conversation itself.
func (s *Service) askLadder(ctx context.Context, machine, sid string) (ladder []string, todo, home string, ok bool) {
	cfg := s.settings(ctx)
	if len(cfg.Leaders) == 0 || cfg.Main == "" || sid == cfg.Main || slices.Contains(cfg.Leaders, sid) {
		return nil, "", "", false
	}
	home = cfg.MachineID
	if s.devs != nil {
		machine = s.devs.Canonical(machine) // as the task's agents are kept
		home = s.devs.Canonical(home)       // and as the session lists the apps see are (busyLocked reads stateSince)
	}
	t, found := s.taskOf(ctx, machine, sid)
	if !found || t.Supervision == nil {
		return nil, "", "", false
	}
	switch t.Supervision.state(t) {
	case reviewNeedsUser, reviewAccepted, reviewCancelled:
		return nil, "", "", false
	}
	if lead := t.Supervision.Supervisor; lead != cfg.Main && slices.Contains(cfg.Leaders, lead) {
		return []string{lead, cfg.Main}, t.ID, home, true
	}
	return []string{cfg.Main}, t.ID, home, true
}

// routeAsk sends new card ix to its supervisor instead of the user (announce); false when it is not one to route.
func (s *Service) routeAsk(ix proto.Interaction) bool {
	if !routable(ix) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
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
	p.route = &askRoute{todo: todo, machine: home, ladder: ladder, since: now, held: now}
	p.ix.Delegating = true
	s.fleet.mu.Unlock()
	shown := ix
	shown.Delegating = true
	s.show(shown)
	s.workEvent(ix.MachineID, ix.SessionID, "ask_routed", map[string]any{"to": ladder[0], "kind": ix.Kind, "todo": todo, "id": ix.ID})
	s.signalAsk(todo, ladder[0], ix, 0)
	return true
}

// signalAsk tells conversation to that task todo's card ix (step: the rung it reached) waits for it.
func (s *Service) signalAsk(todo, to string, ix proto.Interaction, step int) {
	s.signal(superviseSignal{Todo: todo, Kind: sigAsk, To: to, Machine: ix.MachineID, Session: ix.SessionID,
		Key: fmt.Sprintf("%s@%d", ix.ID, step)})
}

// heldAsks are the cards of task todo waiting with conversation to, oldest first, as a supervision entry lists them.
func (s *Service) heldAsks(todo, to string) []prompts.SuperviseAsk {
	type held struct {
		ix   proto.Interaction
		step int
	}
	var list []held
	s.fleet.mu.Lock()
	for _, p := range s.fleet.interactions {
		if r := p.route; r != nil && !p.claimed && r.todo == todo && r.at() == to {
			list = append(list, held{p.ix, r.step})
		}
	}
	s.fleet.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].ix.CreatedAt.Before(list[j].ix.CreatedAt) })
	out := make([]prompts.SuperviseAsk, 0, len(list))
	for _, h := range list {
		out = append(out, prompts.SuperviseAsk{ID: h.ix.ID, Kind: h.ix.Kind, Machine: s.machineName(h.ix.MachineID),
			Session: short(h.ix.SessionID), Text: noticeAsk(h.ix), Raised: h.step > 0})
	}
	return out
}

// routedSessions are the agents with a card on its way up: their needs_you is the card's entry already.
func (s *Service) routedSessions() map[[2]string]bool {
	out := map[[2]string]bool{}
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	for _, p := range s.fleet.interactions {
		if p.route != nil && !p.claimed {
			out[[2]string{p.ix.MachineID, p.ix.SessionID}] = true
			if s.devs != nil { // the watch has the task's agents under their canonical machine
				out[[2]string{s.devs.Canonical(p.ix.MachineID), p.ix.SessionID}] = true
			}
		}
	}
	return out
}

// routedTo: pending card id is on its way up and with conversation self now, or with one below it (the main
// conversation answering what its leader still holds); its task.
func (s *Service) routedTo(id, self string) (string, bool) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	p := s.fleet.interactions[id]
	if p == nil || p.route == nil || p.claimed || p.route.step >= len(p.route.ladder) || !slices.Contains(p.route.ladder[p.route.step:], self) {
		return "", false
	}
	return p.route.todo, true
}

// askTick moves each card held past askHoldFor one rung up: to the next conversation, or to the user, every card of
// the same task that reached the user at once going out as one. The time a card has spent with a conversation on a turn
// does not count (busyLocked): it is being worked through, not waiting to be looked at. A conversation still on a turn
// askBusyMax after the card came is stuck instead, and its time counts again.
func (s *Service) askTick(now time.Time) {
	type up struct {
		todo, to string
		ix       proto.Interaction
		step     int
	}
	var ups []up
	toUser := map[string][]string{}
	s.fleet.mu.Lock()
	for id, p := range s.fleet.interactions {
		r := p.route
		if r == nil || p.claimed {
			continue
		}
		if r.busyLocked(s) && now.Sub(r.held) < askBusyMax {
			r.since = now // its conversation is on a turn: the hold starts again once the turn ends
			continue
		}
		if now.Sub(r.since) < askHoldFor {
			continue
		}
		r.step++
		r.since, r.held = now, now
		if to := r.at(); to != "" {
			ups = append(ups, up{r.todo, to, p.ix, r.step})
		} else {
			toUser[r.todo] = append(toUser[r.todo], id)
		}
	}
	s.fleet.mu.Unlock()
	for _, u := range ups {
		s.logf("ask %s: not handled in %v, up to %s", u.ix.ID, askHoldFor, u.to)
		s.signalAsk(u.todo, u.to, u.ix, u.step)
	}
	for todo, ids := range toUser {
		s.logf("asks %v of task %s: not handled in time, to the user", ids, todo)
		s.ringAsks(todo, ids, nil)
	}
}

// busyLocked: the conversation the card waits with is on a turn now, as the state the apps show it says: it is working
// through the card (or whatever else), so its hold does not run out meanwhile. s.fleet.mu is held.
func (r *askRoute) busyLocked(s *Service) bool {
	to := r.at()
	if to == "" || r.machine == "" {
		return false
	}
	mk, ok := s.fleet.stateSince[[2]string{r.machine, to}]
	return ok && mk.state == proto.StateWorking
}

// ringAsks tells the user of task todo's cards ids, still pending and not told yet, as one: the apps get each card
// (with the supervisor's context d, when it gave some) and the channels each card to answer, but only the first one
// pushes. Cards that come later are not folded into it: they go to the supervisor as any. d nil is the timeout's: a
// quiet task's cards then only go on the apps, as they did before routing (main_tasks_quiet: never pushed unless the
// supervisor says so), and so does a question restored after a restart, which may have rung the user before it. An
// approval restored so is not quiet: no conversation may answer it, so its timeout rings as any other approval's.
// The cards rung.
func (s *Service) ringAsks(todo string, ids []string, d *proto.Delegate) []proto.Interaction {
	var list []proto.Interaction
	maybeTold := map[string]bool{}
	s.fleet.mu.Lock()
	for _, id := range ids {
		p := s.fleet.interactions[id]
		if p == nil || p.claimed || p.told {
			continue
		}
		if p.route != nil && p.route.maybeTold {
			maybeTold[id] = true
		}
		p.route = nil
		p.ix.Delegating = false
		if d != nil {
			c := *d
			p.ix.Delegate = &c
		}
		list = append(list, p.ix)
	}
	if len(list) > 0 && s.askUp.escalated == nil {
		s.askUp.escalated = map[string]escalatedCard{}
	}
	now := time.Now()
	for id, c := range s.askUp.escalated {
		if now.Sub(c.at) > escalatedKeep {
			delete(s.askUp.escalated, id)
		}
	}
	for _, ix := range list {
		s.askUp.escalated[ix.ID] = escalatedCard{todo: todo, at: now}
	}
	s.fleet.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	pushed := false
	for _, ix := range list {
		if d != nil {
			ix = s.suggestOnCard(ix) // a card a supervisor passed up may offer a standing rule (rule_suggest.go)
		}
		ix = s.tellApps(ix)
		if d == nil && (maybeTold[ix.ID] || s.quietSession(ix.MachineID, ix.SessionID)) {
			continue // a question that may have rung before, or a quiet task's: the apps, no second push
		}
		s.tellUser(ix, !pushed)
		pushed = true
	}
	return list
}

// waitingCards are the pending cards of task t's agents the user has not been told of: held by a supervisor, or put
// back on the apps after a restart without a route (its task is not supervised any more).
func (s *Service) waitingCards(t Todo) []string {
	agents := map[[2]string]bool{}
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil {
			agents[[2]string{a.MachineID, a.SessionID}] = true
		}
	}
	var ids []string
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	for id, p := range s.fleet.interactions {
		if p.claimed || p.told || !routable(p.ix) || p.route == nil && p.ix.Delegating {
			continue // the user's already, or the assistant deciding on it for them (fleet_delegate.go): it tells the user itself
		}
		mid := p.ix.MachineID
		if s.devs != nil {
			mid = s.devs.Canonical(mid)
		}
		if p.route != nil && p.route.todo == t.ID || agents[[2]string{mid, p.ix.SessionID}] {
			ids = append(ids, id)
		}
	}
	return ids
}

// ---- ask_escalate (prompts/mcp/agents) ----

// toolAskEscalate hands the cards task todo_id's agents wait on to the user, as one, with why and what the
// supervisor recommends; only the task's supervisor and the main conversation may.
func (s *Service) toolAskEscalate(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID    string `json:"todo_id"`
		Reason    string `json:"reason"`
		Recommend string `json:"recommend"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	reason, recommend := clip(oneLine(a.Reason), 120), clip(oneLine(a.Recommend), 120)
	if reason == "" {
		return "", errors.New("reason is required: in a sentence, what the user is to decide")
	}
	if s.ensureTodosLoaded(ctx) != nil {
		return "", errTodosUnavailable
	}
	t, ok := s.todoNow(strings.TrimSpace(a.TodoID))
	if !ok || t.DoneAt != nil || t.Supervision == nil {
		return "", fmt.Errorf("no open supervised task %s", a.TodoID)
	}
	if len(s.settings(ctx).Leaders) == 0 {
		return "", errors.New("cards go to their supervisor first only with leader conversations (assistant.leaders): without them a task's cards are the user's already")
	}
	main := s.isMain(ctx, self)
	if !main && t.Supervision.Supervisor != self {
		return "", fmt.Errorf("task %s is not yours to supervise: its supervisor or the main conversation escalates its cards", t.ID)
	}
	ids := s.waitingCards(t)
	if len(ids) == 0 {
		return "", fmt.Errorf("task %s has no card waiting that the user has not been told of", t.ID)
	}
	rung := s.ringAsks(t.ID, ids, &proto.Delegate{Reason: reason, Recommend: recommend})
	if len(rung) == 0 {
		return "", fmt.Errorf("task %s has no card waiting any more", t.ID)
	}
	cards := make([]prompts.SuperviseAsk, 0, len(rung))
	for _, ix := range rung {
		cards = append(cards, prompts.SuperviseAsk{ID: ix.ID, Kind: ix.Kind, Machine: s.machineName(ix.MachineID),
			Session: short(ix.SessionID), Text: noticeAsk(ix)})
	}
	for _, ix := range rung {
		// card, kind and recommend: what a suggested rule counts the owner's answer against (rule_suggest.go).
		s.workEvent(ix.MachineID, ix.SessionID, "ask_escalated", map[string]any{"by": self, "todo": t.ID, "card": ix.ID, "kind": ix.Kind, "recommend": recommend})
	}
	if !main {
		// The main conversation knows the user was asked, and what this leader recommends: it does not ask again.
		cfg := s.settings(ctx)
		s.signal(superviseSignal{Todo: t.ID, Kind: sigEscalated, To: cfg.Main, Key: "ask@" + rung[0].ID,
			Detail: reason, Ask: recommend, Cards: cards})
	}
	return fmt.Sprintf("The user is asked now: %d card(s) of task %s went to them as one, with your reason and "+
		"recommendation. Their answers go to the agents and are kept as the task's decisions; don't ask them the same again.", len(rung), t.ID), nil
}

// ---- the answers, as the task's decisions ----

// askAnswered keeps a supervisor's answer to a card of task todo (interaction_answer on a routed card) as the
// task's decision: the agent's call, not the user's, made by conversation self.
func (s *Service) askAnswered(ctx context.Context, todo, self string, ix proto.Interaction, answers map[string]string, reason string) {
	st := s.todoDB()
	if st == nil {
		return
	}
	at := decisionNow()
	wrote := false
	for i, q := range ix.Questions {
		c := decisionText(answers[q.Question])
		if c == "" {
			continue
		}
		ok, err := s.insertDecision(ctx, st, db.TodoDecisionRow{ID: "dec_" + randomHex(8), TodoID: todo,
			Question: decisionText(q.Question), Choice: c, Reason: decisionText(reason), Source: db.DecisionAgent,
			SourceSession: self, SourceMessage: ix.ID, CreatedAt: at.Add(time.Duration(i) * time.Microsecond)})
		if err != nil {
			s.logf("decisions: todo %s, card %s: %v", todo, ix.ID, err)
			return
		}
		wrote = wrote || ok
	}
	if wrote {
		s.decisionsChanged(ctx, todo, at)
	}
}

// escalatedResolved keeps the user's answer to an approval or plan they got through an escalation as the task's
// decision (a question's answer is kept as every card's is, cardDecisions), so a later card cannot quietly go the other way.
func (s *Service) escalatedResolved(ctx context.Context, ix proto.Interaction, r proto.Resolution, status string) {
	if status == "failed" {
		return // a Codex card the node could not answer is open again: its next answer counts
	}
	s.fleet.mu.Lock()
	card, ok := s.askUp.escalated[ix.ID]
	delete(s.askUp.escalated, ix.ID)
	s.fleet.mu.Unlock()
	todo := card.todo
	st := s.todoDB()
	if !ok || st == nil || status != "resolved" || r.Dismiss || ix.Kind == proto.IxQuestion {
		return
	}
	l := s.lang(ctx)
	q, c := decisionText(interactionQuestion(l, ix)), decisionText(resolutionText(l, ix, r))
	if q == "" || c == "" {
		return
	}
	at := decisionNow()
	wrote, err := s.insertDecision(ctx, st, db.TodoDecisionRow{ID: "dec_" + randomHex(8), TodoID: todo, Question: q, Choice: c,
		Source: db.DecisionCard, SourceMachine: ix.MachineID, SourceSession: ix.SessionID, SourceMessage: ix.ID, CreatedAt: at})
	if err != nil {
		s.logf("decisions: todo %s, card %s: %v", todo, ix.ID, err)
		return
	}
	if wrote {
		s.decisionsChanged(ctx, todo, at)
	}
}
