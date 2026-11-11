package assistant

import (
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The owner comes first (#4925, docs/assistant/assistant.md "Messages from the system wait for the owner"): what the
// system puts into an assistant conversation (supervision messages, task notices, late ask_user answers, messages from
// other conversations) waits while the owner is talking there, and what waits goes in by rank, not in the order it
// came. A scheduled run into such a conversation waits as well (runHold, schedule.go runDue). The rules hold for the
// main conversation and leader conversations alike; they are the manager's, and the queue (assistant.relay) keeps
// its shape.

var (
	// ownerPresentFor is how long after the owner's last message in a conversation nothing from the system is
	// delivered to it.
	ownerPresentFor = 2 * time.Minute
	// relayDecisionMax is the longest a message that waits on a decision is held for a present owner, counted from
	// when it queued: the time a card stays on one rung (askHoldFor).
	relayDecisionMax = 5 * time.Minute
)

// The ranks of the queue, first to go first; within a rank the one that queued first.
const (
	relayRankAnswer   = iota // the owner's late answer to an ask_user question (ask_todo.go)
	relayRankDecision        // a supervision message or task notice that waits on a decision
	relayRankReport          // an outcome the conversation is to report to the owner (a task's notice)
	relayRankOther           // everything else: other supervision messages, messages from conversations, records
)

// decisionWaiting: somebody waits on what msg starts (the owner's late answer, an agent that needs an answer or was
// cut off, a start that failed), so a present owner holds it for relayDecisionMax at most.
func (msg relayMsg) decisionWaiting() bool {
	switch msg.Tool {
	case askAnswerTool:
		return true
	case superviseTool:
		return msg.Urgent
	case noticeTool:
		return msg.State == "needs" || msg.State == "interrupted" || msg.State == "failed"
	}
	return false
}

func (msg relayMsg) rank() int {
	switch {
	case msg.Tool == askAnswerTool:
		return relayRankAnswer
	case msg.decisionWaiting():
		return relayRankDecision
	case msg.Tool == noticeTool:
		return relayRankReport
	}
	return relayRankOther
}

// waitsOnDecision: somebody waits on what the signal asks for. Narrower than urgent, which is how soon a supervision
// message goes out: a start the supervisor's retry brought about and a report that the work is done are told soon, yet
// nobody waits on them.
func (g superviseSignal) waitsOnDecision() bool {
	switch g.Kind {
	case sigStarted:
		return false
	case sigReport:
		return g.Report == upReportQuestion || g.Report == upReportBlocked
	}
	return g.urgent()
}

// anyDecision: one of the signals of a supervision message waits on a decision (relayMsg.Urgent).
func anyDecision(list []superviseSignal) bool {
	return slices.ContainsFunc(list, func(g superviseSignal) bool { return g.waitsOnDecision() })
}

// replacing is msg taking queued message old's place at now. It has waited since old was queued (queuedAs), so a
// message replaced again and again still comes to the end of its hold and its queue wait is the whole wait. One that
// only now waits on a decision counts its relayDecisionMax from now: it starts with the decision, not with the
// ordinary message it merged into.
func (msg relayMsg) replacing(old relayMsg, now time.Time) relayMsg {
	msg = msg.queuedAs(old)
	msg.DecisionAt = time.Time{}
	if msg.decisionWaiting() {
		if old.decisionWaiting() {
			msg.DecisionAt = old.DecisionAt
		} else {
			msg.DecisionAt = now.UTC()
		}
	}
	return msg
}

// decisionSince is when msg began to wait on a decision: when it queued, unless it took the place of a message
// that waited on none (DecisionAt). One queued by a manager from before queued_at counts from its own time.
func (msg relayMsg) decisionSince() time.Time {
	switch {
	case !msg.DecisionAt.IsZero():
		return msg.DecisionAt
	case !msg.QueuedAt.IsZero():
		return msg.QueuedAt
	}
	return msg.At
}

// holdLocked is how long msg still has to wait before it may go into conversation to (0: it may go now); b.mu is
// held. A message that waits on a decision and does not say when it queued is not held, and neither is the owner's
// own answer on a card asked without waiting (relayMsg.Owner, #4928): the hold keeps the system out of the owner's
// way, and that answer is the owner speaking.
func (b *relayBook) holdLocked(to string, msg relayMsg, now time.Time) time.Duration {
	spoke, ok := b.st.Spoke[to]
	if !ok || msg.Owner {
		return 0
	}
	until := spoke.Add(ownerPresentFor)
	if msg.decisionWaiting() {
		if latest := msg.decisionSince().Add(relayDecisionMax); latest.Before(until) {
			until = latest
		}
	}
	return max(until.Sub(now), 0)
}

// pickLocked is the index of the message of conversation to's queue that goes in next: the best rank among those not
// held for the owner, the one that queued first within it. The message that could not go in last time (b.stuck: its
// agent's computer unreachable, a delivery that failed) is passed over while another may go, so it holds up nothing
// but itself. None may go (-1): wait is how long until the first may. b.mu is held.
func (b *relayBook) pickLocked(to string, now time.Time) (i int, wait time.Duration) {
	i = -1
	stuck := -1
	q := b.st.Queue[to]
	for j, m := range q {
		if h := b.holdLocked(to, m, now); h > 0 {
			if wait == 0 || h < wait {
				wait = h
			}
			continue
		}
		if last, ok := b.stuck[to]; ok && stuck < 0 && m == last {
			stuck = j
			continue
		}
		if i < 0 || m.rank() < q[i].rank() {
			i = j
		}
	}
	if i < 0 {
		i = stuck // nothing else may go: it is tried again
	}
	if i >= 0 {
		wait = 0
	}
	return i, wait
}

// frontLocked moves message i of conversation to's queue to its head, where the message going in is (what replaces
// or drops queued messages leaves the head of a draining queue alone); the others keep their order. b.mu is held.
func (b *relayBook) frontLocked(to string, i int) {
	q := b.st.Queue[to]
	if i <= 0 || i >= len(q) {
		return
	}
	msg := q[i]
	copy(q[1:i+1], q[:i])
	q[0] = msg
}

// ownerSpokeLocked notes that the owner sent conversation id a message at now; b.mu is held.
func (b *relayBook) ownerSpokeLocked(id string, now time.Time) {
	if b.st.Spoke == nil {
		b.st.Spoke = map[string]time.Time{}
	}
	b.st.Spoke[id] = now.UTC()
}

// ownersPresent is when the owner last spoke in each conversation they are present in at now.
func (s *Service) ownersPresent(now time.Time) map[string]time.Time {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return nil
	}
	present := map[string]time.Time{}
	for id, spoke := range b.st.Spoke {
		if now.Sub(spoke) < ownerPresentFor {
			present[id] = spoke
		}
	}
	return present
}

// runHold is how long the scheduled run of t that was due at due still waits at now, because the owner is talking
// (present: ownersPresent) in the conversation it goes into: the one it came from, else its own. A run has a time
// the owner set, so it waits relayDecisionMax past that time at most, as a message that waits on a decision does;
// "Run now" is the owner's own and is not held (it does not come through runDue).
func runHold(present map[string]time.Time, t Task, due, now time.Time) time.Duration {
	id := t.SessionID
	if id == "" {
		id = t.RunSessionID
	}
	spoke, ok := present[id]
	if !ok || id == "" {
		return 0
	}
	until := spoke.Add(ownerPresentFor)
	if latest := due.Add(relayDecisionMax); latest.Before(until) {
		until = latest
	}
	return max(until.Sub(now), 0)
}

// relayWaits: msg can't go into conversation to right now even though it is not running: others wait in its queue
// already, or the owner is talking in it. The caller queues it and drains (drainRelays), which delivers what may go
// and comes back when the hold ends.
func (s *Service) relayWaits(to string, msg relayMsg) bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	return len(b.st.Queue[to]) > 0 || b.holdLocked(to, msg, time.Now()) > 0
}

// drainAfter drains conversation id's queue once more in wait, when the hold on what waits there ends. One timer per
// conversation: a later call replaces it.
func (s *Service) drainAfter(machine, id string, wait time.Duration) {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if t := b.holds[id]; t != nil {
		t.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(wait, func() {
		b.mu.Lock()
		if b.holds[id] == t {
			delete(b.holds, id)
		}
		b.mu.Unlock()
		s.drainConversations(machine, []string{id})
	})
	b.holds[id] = t
}

// drainConversations drains the queues of machine's conversations ids as its list has them now. A list that can't
// be read (false) leaves them to the next look (drainHeldRelays) or the next change of the list.
func (s *Service) drainConversations(machine string, ids []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := s.sessions(ctx, machine)
	if err != nil {
		s.logf("relay: held messages of %d conversation(s) wait on: %v", len(ids), err)
		return false
	}
	list = slices.DeleteFunc(slices.Clone(list), func(c proto.Session) bool { return !slices.Contains(ids, c.SessionID) })
	s.drainRelays(machine, list)
	return true
}

// drainHeldRelays is the look every superviseTick that nothing held for the owner is left behind: a conversation
// the owner spoke in, with a message that may go by now and no timer of its own (the manager restarted meanwhile, or
// the timer could not read the list), is drained. The owner's last message is forgotten once ownerPresentFor passed
// and what it held was handed on.
func (s *Service) drainHeldRelays(ctx context.Context, now time.Time) {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var due []string
	for id, spoke := range b.st.Spoke {
		if len(b.st.Queue[id]) == 0 {
			if now.Sub(spoke) >= ownerPresentFor {
				delete(b.st.Spoke, id)
			}
			continue
		}
		if b.holds[id] != nil || b.draining[id] {
			continue
		}
		if i, _ := b.pickLocked(id, now); i >= 0 {
			due = append(due, id)
		}
	}
	b.mu.Unlock()
	if len(due) == 0 {
		return
	}
	machine := s.settings(ctx).MachineID
	if machine == "" || !s.drainConversations(machine, due) {
		return
	}
	b.mu.Lock()
	for _, id := range due {
		if spoke, ok := b.st.Spoke[id]; ok && now.Sub(spoke) >= ownerPresentFor {
			delete(b.st.Spoke, id) // handed on: a delivery that fails waits for the list to change, as ever
		}
	}
	b.mu.Unlock()
}
