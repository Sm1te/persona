package assistant

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Availability metrics (#4924, docs/assistant/assistant.md "Availability metrics"; step M1 of
// docs/design/proposals/main-chat-availability.md): how long the owner waits for an assistant conversation's reply,
// what its rounds are spent on, how long system messages wait in its queue, and who answers the cards. Everything is
// a work event (session_work_events, kept 30 days like the others): one per answered owner message, per round, per
// delivered queued message and per ended card. Nothing here calls a model or reads a transcript.
//
// GET /api/v1/assistant/availability reads them. It is a route of its own, not part of #4601's north-star read
// (north_star.go): that one reads a single day from the database alone, this one a window of days plus the queues and
// the acceptance book as the running manager has them. Its top-level fields do not collide with the north star's.
const (
	// replyWaitEvent: the owner's message to an assistant conversation got its first reply text. Detail: state (what the
	// conversation was doing when the message came, the wait* constants), wait (seconds), main, open_tasks (the open
	// tasks when the message came; absent when not known); unanswered when no text came within replyWaitMax (wait is
	// then that bound); by "turn_end" when a turn's end stood in for a text the session list never showed changing.
	replyWaitEvent = "reply_wait"
	// roundEvent: an assistant conversation's round ended. Detail: source (who started it, the round* constants and
	// the message sources), seconds, main; stopped when it ended without a turn's end (interrupted, its process gone).
	roundEvent = "round"
	// queueWaitEvent: a queued system message was delivered. Detail: kind (as roundEvent's source), wait (seconds since
	// it was first queued), depth (the queue's length with it when it was added), main.
	queueWaitEvent = "queue_wait"
	// askOutcomeEvent: an agent's or the assistant's card ended. Detail: interaction (its id; not "id", which the
	// delegate's marks of a card are read back by), kind (the card's, "ask_user" for the
	// assistant's own question), by (askByOwner, askByAssistant or askByRemembered), status, after (seconds); expired
	// when the card ended because nobody answered it in time.
	askOutcomeEvent = "ask_outcome"
)

// What started a round, besides the message sources (proto.SourceSupervision, SourceFollowup, SourcePatrol,
// SourceConversation) and roundNotice (owner_preempt.go; here also a late ask_user answer and a spawned
// conversation's report).
const (
	roundOwner = "owner"
	// roundOther: the manager neither sent nor saw the input: a new conversation's first message, a background task
	// reporting back, a round under way when the manager started.
	roundOther = "other"
)

// What the conversation was doing when the owner's message came.
const (
	waitIdle       = "idle"
	waitOwnerRound = "owner_round"
	waitBackground = "background_round"
	// waitUnknownRound: working on a round the manager did not see start (it restarted meanwhile).
	waitUnknownRound = "unknown_round"
)

// Who ended a card, as recorded; the read says which of the assistant's it was (the askWho* constants).
const (
	askByOwner      = "owner"
	askByAssistant  = "assistant"
	askByRemembered = "remembered" // an "Always allow in this session" kept earlier
)

// Variables for the tests.
var (
	// availInputFor: a noted input starts the conversation's next round when that starts within this long (resuming a
	// stopped conversation takes a while). An owner message to an idle conversation that started no turn in this
	// long was not delivered (a refused send) and is no wait.
	availInputFor = 5 * time.Minute
	// availQueuedGrace: an input noted during a round that did not take it starts the next round within this long of
	// the round's end; later, it was taken into the round it came in.
	availQueuedGrace = 15 * time.Second
	// replyWaitMax: an owner message with no reply text this long after it is recorded unanswered.
	replyWaitMax = 30 * time.Minute
)

const (
	// availSentKeep: how long an owner message's client_msg_id is remembered, to tell its replay.
	availSentKeep = time.Hour
	// availReplayFor: a replayed message's session.user_spoke and its assistant.send_params come within this long of
	// each other, in either order.
	availReplayFor = 5 * time.Second
	// availMarkBefore: how long before the read's window the marks of whose answer a card's was (delegate_start,
	// standing_authorized) are still read: the card may have been taken up before the window began.
	availMarkBefore = 48 * time.Hour
)

type availInput struct {
	source string
	until  time.Time
}

type availRound struct {
	source  string
	started time.Time
}

type availWait struct {
	at    time.Time
	state string
	// tasks: the open tasks when the message came; -1 when the to-dos are not loaded.
	tasks int
	// began: a turn was seen starting after the message. queued: its node showed the message waiting behind the
	// running turn. Text counts as the reply only after one of them: until then it is the running round's own.
	began, queued bool
	// ended: the round the message came in ended with neither seen. Unless a turn starts at once, the message was
	// taken into that round and its end stands in for the reply.
	ended time.Time
	// fresh: the owner's latest message opened it: that message turning out a replay takes it back.
	fresh bool
}

// availBook is what the metrics need between two events, in memory: a restart loses the rounds and waits under way.
type availBook struct {
	mu     sync.Mutex
	known  map[[2]string]bool       // the conversations an input was noted for: assistant conversations
	inputs map[[2]string]availInput // the input a conversation's next round starts with
	rounds map[[2]string]availRound // the rounds under way
	waits  map[[2]string]availWait  // the owner's earliest message still without a reply
	// sent: the client_msg_id of the owner's messages of the last availSentKeep; replayed: the conversations a
	// replay was seen for whose session.user_spoke has not come yet.
	sent     map[string]time.Time
	replayed map[[2]string]time.Time
	// waiting is len(waits): the session lists are not looked at while nobody waits.
	waiting atomic.Int32
}

func (b *availBook) init() {
	if b.known == nil {
		b.known, b.inputs = map[[2]string]bool{}, map[[2]string]availInput{}
		b.rounds, b.waits = map[[2]string]availRound{}, map[[2]string]availWait{}
		b.sent, b.replayed = map[string]time.Time{}, map[[2]string]time.Time{}
	}
}

// dropWait forgets k's wait; with b.mu held.
func (b *availBook) dropWait(k [2]string) {
	delete(b.waits, k)
	b.waiting.Store(int32(len(b.waits)))
}

// availInput notes that an input from source goes to machine's assistant conversation now.
func (s *Service) availInput(machine, session, source string, now time.Time) {
	if machine == "" || session == "" || source == "" || source == proto.SourceMeeting {
		return // a meeting's batch is a side exchange, not a round (meeting_turns.go)
	}
	b := &s.avail
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	k := [2]string{machine, session}
	b.known[k] = true
	b.inputs[k] = availInput{source: source, until: now.Add(availInputFor)}
}

// relayKind is what a queued message counts as: its message source, else a notice.
func relayKind(msg relayMsg) string {
	if src := msg.source(); src != nil {
		return src.Kind
	}
	return roundNotice
}

// queuedAs is msg taking queued message old's place: it has waited since old was queued.
func (msg relayMsg) queuedAs(old relayMsg) relayMsg {
	msg.QueuedAt, msg.Depth = old.QueuedAt, old.Depth
	return msg
}

// availOpenTasks is how many tasks are open now; -1 when the to-dos are not loaded.
func (s *Service) availOpenTasks() int {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	if !s.todos.loaded {
		return -1
	}
	n := 0
	for _, t := range s.todos.todos {
		if t != nil && t.DoneAt == nil && !t.Cancelled() {
			n++
		}
	}
	return n
}

// availOwnerSpoke: the owner's message for machine's assistant conversation was accepted. The earliest one without a
// reply is what the reply wait is measured from.
func (s *Service) availOwnerSpoke(r SessionRef, now time.Time) {
	if r.MachineID == "" || r.SessionID == "" {
		return
	}
	k := [2]string{r.MachineID, r.SessionID}
	c, listed := s.nodeSession(r.MachineID, r.SessionID)
	working := listed && c.Status == proto.StatusWorking
	tasks := s.availOpenTasks()
	b := &s.avail
	b.mu.Lock()
	b.init()
	if at, ok := b.replayed[k]; ok {
		delete(b.replayed, k)
		if now.Sub(at) < availReplayFor {
			b.mu.Unlock()
			return // a replay of a message already counted (availOwnerSent saw it first)
		}
	}
	if w, waits := b.waits[k]; waits {
		w.fresh = false
		b.waits[k] = w
	} else {
		// A round still in the book of a conversation its node lists as stopped never ended (interrupted, its
		// process gone): the conversation is idle.
		round, onRound := b.rounds[k]
		onRound = onRound && (!listed || working)
		state := waitIdle
		switch {
		case onRound && round.source == roundOwner:
			state = waitOwnerRound
		case onRound:
			state = waitBackground
		case working:
			state = waitUnknownRound
		}
		b.waits[k] = availWait{at: now, state: state, tasks: tasks, fresh: true}
		b.waiting.Store(int32(len(b.waits)))
	}
	b.mu.Unlock()
	s.availInput(r.MachineID, r.SessionID, roundOwner, now)
}

// availOwnerSent: the owner's message id (its client_msg_id) for machine's conversation is about to be sent
// (assistant.send_params). The hub says session.user_spoke before it knows a message for a replay of one it already
// delivered, so a second sight of an id takes back the wait that replay opened, or keeps it from opening.
func (s *Service) availOwnerSent(machine, session, id string, now time.Time) {
	if machine == "" || session == "" || id == "" {
		return
	}
	k := [2]string{machine, session}
	b := &s.avail
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	at, seen := b.sent[id]
	if !seen || now.Sub(at) >= availSentKeep {
		if len(b.sent) >= 256 {
			for old, at := range b.sent {
				if now.Sub(at) >= availSentKeep {
					delete(b.sent, old)
				}
			}
		}
		b.sent[id] = now
		return
	}
	w, waits := b.waits[k]
	switch {
	case !waits:
		b.replayed[k] = now // its session.user_spoke is still on the way
	case now.Sub(w.at) < availReplayFor:
		b.takeBack(k)
	}
}

// availOwnerFailed: the owner's latest message for machine's conversation was not sent (the hub's
// session.assistant_sent, or the manager refused it): the wait and the round's input it opened are taken back. A
// wait an earlier message opened stays.
func (s *Service) availOwnerFailed(machine, session string) {
	k := [2]string{machine, session}
	b := &s.avail
	b.mu.Lock()
	defer b.mu.Unlock()
	b.takeBack(k)
}

// takeBack undoes the owner's latest message to k when it opened the wait; with b.mu held.
func (b *availBook) takeBack(k [2]string) {
	if w, ok := b.waits[k]; !ok || !w.fresh {
		return
	}
	b.dropWait(k)
	if in, ok := b.inputs[k]; ok && in.source == roundOwner {
		delete(b.inputs, k)
	}
}

// availConversation: k (machine, session) is one of the assistant's conversations, as far as is known without
// asking its node.
func (s *Service) availConversation(k [2]string) bool {
	b := &s.avail
	b.mu.Lock()
	known := b.known[k]
	b.mu.Unlock()
	if known {
		return true
	}
	m, ok := s.fleetMachine(k[0])
	if !ok {
		return false
	}
	c, ok := s.nodeSession(k[0], k[1])
	return ok && m.isAssistant(c)
}

// availTurnStarted: machine's session is seen working; newTurn: it was seen stopped before. An assistant
// conversation's round starts with the input noted for it.
func (s *Service) availTurnStarted(machine, session string, newTurn bool, now time.Time) {
	k := [2]string{machine, session}
	if !s.availConversation(k) {
		return
	}
	b := &s.avail
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	in, noted := b.inputs[k]
	noted = noted && now.Before(in.until)
	if _, open := b.rounds[k]; open && !newTurn && !noted {
		// The same round seen again (a reconnect). With an input waiting to start a round it is a new one: the round
		// in the book stopped unseen (its node went away with it), and is dropped rather than carried on.
		return
	}
	source := roundOther
	if noted {
		source = in.source
	}
	delete(b.inputs, k)
	b.rounds[k] = availRound{source: source, started: now}
}

// closeRound takes k's round out of the book as it ends at now; with b.mu held. An input noted while it ran starts
// the next round when that follows at once, however long ago it was noted.
func (b *availBook) closeRound(k [2]string, now time.Time) (availRound, bool) {
	round, ok := b.rounds[k]
	if !ok {
		return round, false
	}
	delete(b.rounds, k)
	if in, ok := b.inputs[k]; ok {
		in.until = now.Add(availQueuedGrace)
		b.inputs[k] = in
	}
	return round, true
}

// availTurnEnded: machine's session finished a turn: its round is recorded, and an owner message the round started
// after is answered by now, whether or not the list showed its text.
func (s *Service) availTurnEnded(ctx context.Context, machine, session string, now time.Time) {
	k := [2]string{machine, session}
	b := &s.avail
	b.mu.Lock()
	b.init()
	round, ok := b.closeRound(k, now)
	if !ok {
		b.mu.Unlock()
		return
	}
	wait, waits := b.waits[k]
	answered := waits && !round.started.Before(wait.at)
	switch {
	case answered:
		b.dropWait(k)
	case waits && !wait.began: // began: this end is of the round before the one the message started, come late
		wait.ended = now // came during this round: answered by it, unless a turn starts at once (availSeen)
		b.waits[k] = wait
	}
	b.mu.Unlock()
	main := s.isMain(ctx, session)
	s.workEvent(machine, session, roundEvent, map[string]any{"source": round.source, "seconds": now.Sub(round.started).Seconds(), "main": main})
	if answered {
		s.replyWaited(machine, session, wait, now.Sub(wait.at), main, map[string]any{"by": "turn_end"})
	}
}

// availStopped: machine's session went from working to stopped without a turn's end (interrupted, its process gone):
// the round is over all the same, and its time was spent.
func (s *Service) availStopped(machine, session string, now time.Time) {
	k := [2]string{machine, session}
	b := &s.avail
	b.mu.Lock()
	b.init()
	round, ok := b.closeRound(k, now)
	b.mu.Unlock()
	if !ok {
		return
	}
	s.workEvent(machine, session, roundEvent, map[string]any{"source": round.source, "seconds": now.Sub(round.started).Seconds(),
		"main": s.isMain(context.Background(), session), "stopped": true})
}

// replyWaited records wait as over after waited.
func (s *Service) replyWaited(machine, session string, wait availWait, waited time.Duration, main bool, more map[string]any) {
	detail := map[string]any{"state": wait.state, "wait": waited.Seconds(), "main": main}
	if wait.tasks >= 0 {
		detail["open_tasks"] = wait.tasks
	}
	for k, v := range more {
		detail[k] = v
	}
	s.workEvent(machine, session, replyWaitEvent, detail)
}

// What became of a wait at a look (availWait.look).
const (
	waitOn         = iota
	waitReplied    // the reply's text showed
	waitTaken      // taken into the round it came in, which ended
	waitUnanswered // no reply within replyWaitMax
	waitDropped    // never delivered: no wait
)

// look is what became of the wait as its conversation's node lists it now: status, text (changed is whether the last
// text differs from the list before) and what is queued behind its turn. began is whether a turn is seen starting.
// The wait itself is updated.
func (w *availWait) look(status string, began, changed bool, queued int, now time.Time) int {
	if began {
		w.began, w.ended = true, time.Time{}
	}
	if queued > 0 {
		w.queued, w.ended = true, time.Time{}
	}
	switch {
	// The reply: text once nothing of the owner's is queued any more. For a message that came during a round, only
	// after its node showed it queued or a turn started: before, the text is that round's own (a node that lists no
	// queue cannot say when the message is taken in).
	case changed && queued == 0 && (w.state == waitIdle || w.began || w.queued):
		return waitReplied
	case w.state == waitIdle && !w.began && now.Sub(w.at) > availInputFor:
		return waitDropped
	case !w.ended.IsZero() && status != proto.StatusWorking && queued == 0 && now.Sub(w.ended) > availQueuedGrace:
		return waitTaken
	case now.Sub(w.at) > replyWaitMax:
		return waitUnanswered
	}
	return waitOn
}

// availSeen looks at machine's session as its node lists it now (was: as it listed it before): the first change of
// its last text after the owner's message is the reply (availWait.look).
func (s *Service) availSeen(machine string, sess *proto.Session, was turnPrev, now time.Time) {
	b := &s.avail
	if b.waiting.Load() == 0 {
		return
	}
	k := [2]string{machine, sess.SessionID}
	b.mu.Lock()
	wait, ok := b.waits[k]
	if !ok {
		b.mu.Unlock()
		return
	}
	// A turn starting: from a known stopped turn, or, for a message to an idle conversation, from anything (it may
	// have been resumed from offline).
	began := sess.Status == proto.StatusWorking && was.Status != proto.StatusWorking &&
		(wait.state == waitIdle || was.Status == proto.StatusDone || was.Status == proto.StatusIdle || was.Status == proto.StatusInterrupted)
	what := wait.look(sess.Status, began, sess.LastText != "" && sess.LastText != was.LastText, len(sess.Queued), now)
	if what == waitOn {
		b.waits[k] = wait
	} else {
		b.dropWait(k)
	}
	b.mu.Unlock()
	s.availWaitOver(machine, sess.SessionID, wait, what, now)
}

// availWaitOver records a wait that ended as what (not waitOn).
func (s *Service) availWaitOver(machine, session string, wait availWait, what int, now time.Time) {
	if what == waitOn || what == waitDropped {
		return
	}
	main := s.isMain(context.Background(), session)
	switch what {
	case waitReplied:
		s.replyWaited(machine, session, wait, now.Sub(wait.at), main, nil)
	case waitTaken:
		s.replyWaited(machine, session, wait, wait.ended.Sub(wait.at), main, map[string]any{"by": "turn_end"})
	case waitUnanswered:
		s.replyWaited(machine, session, wait, replyWaitMax, main, map[string]any{"unanswered": true})
	}
}

// availSweep ends the waits no list will: those of machine's conversations its node no longer lists (list is its
// whole list), and those of a machine that sent no list for longer than replyWaitMax.
func (s *Service) availSweep(machine string, list []proto.Session, now time.Time) {
	b := &s.avail
	if b.waiting.Load() == 0 {
		return
	}
	type over struct {
		k    [2]string
		wait availWait
		what int
	}
	var done []over
	b.mu.Lock()
	for k, wait := range b.waits {
		if k[0] == machine {
			if slices.ContainsFunc(list, func(c proto.Session) bool { return c.SessionID == k[1] }) {
				continue
			}
		} else if now.Sub(wait.at) <= replyWaitMax+time.Minute {
			continue
		}
		// Gone or silent: as a look at a stopped conversation with nothing new.
		if what := wait.look(proto.StatusOffline, false, false, 0, now); what != waitOn {
			done = append(done, over{k, wait, what})
			delete(b.waits, k)
		}
	}
	b.waiting.Store(int32(len(b.waits)))
	b.mu.Unlock()
	for _, o := range done {
		s.availWaitOver(o.k[0], o.k[1], o.wait, o.what, now)
	}
}

// availQueueDelivered: queued message msg went into machine's conversation to.
func (s *Service) availQueueDelivered(machine, to string, msg relayMsg, now time.Time) {
	if msg.QueuedAt.IsZero() {
		return // queued by a manager from before the metric
	}
	s.workEvent(machine, to, queueWaitEvent, map[string]any{"kind": relayKind(msg), "wait": now.Sub(msg.QueuedAt).Seconds(),
		"depth": msg.Depth, "main": s.isMain(context.Background(), to)})
}

// availAskEnded: a card ended (InteractionResolved). One whose answer failed is still open and not counted.
func (s *Service) availAskEnded(r InteractionResolved, now time.Time) {
	ix := r.Interaction
	if ix.ID == "" || ix.CreatedAt.IsZero() || r.Status == "failed" {
		return
	}
	by := askByOwner
	switch {
	case r.Resolution.Remembered:
		by = askByRemembered
	case r.Resolution.AnsweredBy != "":
		by = askByAssistant
	}
	kind := ix.Kind
	if ix.ToolName == "ask_user" {
		kind = "ask_user"
	}
	detail := map[string]any{"interaction": ix.ID, "kind": kind, "by": by, "status": r.Status, "after": max(now.Sub(ix.CreatedAt).Seconds(), 0)}
	if r.Resolution.Dismiss && r.Resolution.Reason == proto.DismissedExpired {
		detail["expired"] = true
	}
	s.workEvent(ix.MachineID, ix.SessionID, askOutcomeEvent, detail)
}

// ---- the read ----

// availabilityStats is GET /api/v1/assistant/availability: the four metrics over the last hours.
type availabilityStats struct {
	Hours int `json:"hours"`
	// ReplyWait: seconds from the owner's message to the conversation's first reply text.
	ReplyWait replyWaits `json:"reply_wait"`
	// BackgroundShare: per day (the owner's time zone) and conversation, round time and rounds by what started them;
	// newest day first.
	BackgroundShare []roundDay `json:"background_share"`
	// QueueWait: seconds a system message waited in a conversation's queue, by kind, and what waits now.
	QueueWait queueWaits `json:"queue_wait"`
	// AskOutcome: by card kind, then by who answered: how many and after how long.
	AskOutcome map[string]map[string]waitStats `json:"ask_outcome"`
}

type replyWaits struct {
	// All and Main: by what the conversation was doing when the message came (idle, owner_round, background_round,
	// unknown_round); Main is the main conversation alone.
	All  map[string]replyWaitStats `json:"all"`
	Main map[string]replyWaitStats `json:"main"`
	// MainDays: the main conversation's waits per day, whatever it was doing; newest first.
	MainDays []replyWaitDay `json:"main_days"`
}

type replyWaitStats struct {
	waitStats
	// Unanswered: how many of them got no reply text within 30 minutes (counted at that bound).
	Unanswered int `json:"unanswered"`
}

type replyWaitDay struct {
	Day string `json:"day"`
	replyWaitStats
	// OpenTasks: the most tasks open when one of the day's messages came (acceptance A1 compares a day with ten or
	// more against a day with none); absent when no wait of the day knew.
	OpenTasks *int `json:"open_tasks,omitempty"`
}

type roundDay struct {
	Day          string `json:"day"`
	Conversation string `json:"conversation"`
	Main         bool   `json:"main"`
	// Seconds and Rounds by what started the round: owner, supervision, followup, patrol, conversation, notice, other.
	Seconds map[string]float64 `json:"seconds"`
	Rounds  map[string]int     `json:"rounds"`
	// BackgroundShare: of the Seconds whose round's start is known (all but "other"), the share the owner did not
	// start.
	BackgroundShare float64 `json:"background_share"`
}

type queueWaits struct {
	// ByKind: every assistant conversation's; MainByKind: the main conversation's alone.
	ByKind     map[string]queueWaitStats `json:"by_kind"`
	MainByKind map[string]queueWaitStats `json:"main_by_kind"`
	// QueuedNow: the conversations with system messages waiting now, the longest wait first.
	QueuedNow []queuedNow `json:"queued_now"`
}

type queueWaitStats struct {
	waitStats
	// MaxSeconds: the longest one of them waited.
	MaxSeconds float64 `json:"max_seconds"`
	// MaxDepth: the longest the queue was when one of them was added, itself included.
	MaxDepth int `json:"max_depth"`
}

type queuedNow struct {
	Conversation  string  `json:"conversation"`
	Main          bool    `json:"main"`
	Depth         int     `json:"depth"`
	OldestSeconds float64 `json:"oldest_seconds"`
}

// Who answered a card, as the read says it, besides askByOwner and askByRemembered.
const (
	askWhoSupervisor = "supervisor"
	askWhoDelegate   = "delegate"
	askWhoStanding   = "standing_authorization"
	askWhoDefault    = "default"
	askWhoDismissed  = "dismissed" // ended without an answer: dismissed, cancelled, withdrawn
	askWhoExpired    = "expired"   // ended because nobody answered it in time
)

// taskAsk is one answered task card of the acceptance book (availabilityAsks), for summarizeAvailability.
type taskAsk struct {
	kind, who string
	after     float64
}

func waitsOf(v []float64) waitStats {
	if len(v) == 0 {
		return waitStats{}
	}
	slices.Sort(v)
	return waitStats{N: len(v), Median: v[len(v)/2], P90: v[min(len(v)-1, len(v)*9/10)]}
}

// summarizeAvailability reads the four metrics out of the events since since (events: oldest first, and from
// availMarkBefore earlier for the marks of whose answer a card's was); days are loc's. asks and queued are what is
// not an event: the answered task cards and the queues as they are now.
func summarizeAvailability(hours int, since time.Time, events []db.WorkEvent, loc *time.Location, asks []taskAsk, queued []queuedNow) availabilityStats {
	out := availabilityStats{Hours: hours, ReplyWait: replyWaits{All: map[string]replyWaitStats{}, Main: map[string]replyWaitStats{}, MainDays: []replyWaitDay{}},
		BackgroundShare: []roundDay{}, QueueWait: queueWaits{ByKind: map[string]queueWaitStats{}, MainByKind: map[string]queueWaitStats{}, QueuedNow: queued},
		AskOutcome: map[string]map[string]waitStats{}}
	if out.QueueWait.QueuedNow == nil {
		out.QueueWait.QueuedNow = []queuedNow{}
	}
	type waits struct {
		v          []float64
		unanswered int
	}
	add := func(m map[string]*waits, k string, wait float64, unanswered bool) {
		w := m[k]
		if w == nil {
			w = &waits{}
			m[k] = w
		}
		w.v = append(w.v, wait)
		if unanswered {
			w.unanswered++
		}
	}
	replyAll, replyMain, replyDays := map[string]*waits{}, map[string]*waits{}, map[string]*waits{}
	dayTasks := map[string]int{}
	days := map[[2]string]*roundDay{}
	type queued1 struct {
		v     []float64
		depth int
	}
	queue, queueMain := map[string]*queued1{}, map[string]*queued1{}
	addQueue := func(m map[string]*queued1, kind string, wait float64, depth int) {
		q := m[kind]
		if q == nil {
			q = &queued1{}
			m[kind] = q
		}
		q.v, q.depth = append(q.v, wait), max(q.depth, depth)
	}
	// The cards a standing authorization answered, and those the delegate was deciding on: which of the assistant's
	// answers were theirs.
	standing, delegated := map[string]bool{}, map[string]bool{}
	type outcome struct {
		id, kind, by, status string
		after                float64
		expired              bool
	}
	var outcomes []outcome
	counted := map[string]bool{} // a card's first end counts (an answer changed later ends it again)
	for _, e := range events {
		str := func(k string) string { v, _ := e.Detail[k].(string); return v }
		num := func(k string) float64 {
			switch v := e.Detail[k].(type) {
			case float64:
				return v
			case int: // as recorded, before the store's JSON
				return float64(v)
			}
			return 0
		}
		main, _ := e.Detail["main"].(bool)
		if e.At.Before(since) && e.Kind != "standing_authorized" && e.Kind != "delegate_start" {
			continue
		}
		switch e.Kind {
		case replyWaitEvent:
			unanswered, _ := e.Detail["unanswered"].(bool)
			state := cmp.Or(str("state"), waitIdle)
			add(replyAll, state, num("wait"), unanswered)
			if main {
				add(replyMain, state, num("wait"), unanswered)
				day := e.At.In(loc).Format(time.DateOnly)
				add(replyDays, day, num("wait"), unanswered)
				if _, known := e.Detail["open_tasks"]; known {
					dayTasks[day] = max(dayTasks[day], int(num("open_tasks")))
				}
			}
		case roundEvent:
			k := [2]string{e.At.In(loc).Format(time.DateOnly), e.SessionID}
			d := days[k]
			if d == nil {
				d = &roundDay{Day: k[0], Conversation: k[1], Seconds: map[string]float64{}, Rounds: map[string]int{}}
				days[k] = d
			}
			source := cmp.Or(str("source"), roundOther)
			d.Main = d.Main || main
			d.Seconds[source] += num("seconds")
			d.Rounds[source]++
		case queueWaitEvent:
			kind := cmp.Or(str("kind"), roundNotice)
			addQueue(queue, kind, num("wait"), int(num("depth")))
			if main {
				addQueue(queueMain, kind, num("wait"), int(num("depth")))
			}
		case "standing_authorized":
			standing[str("interaction")] = true
		case "delegate_start":
			delegated[str("id")] = true
		case askOutcomeEvent:
			if id := str("interaction"); !counted[id] {
				counted[id] = true
				expired, _ := e.Detail["expired"].(bool)
				outcomes = append(outcomes, outcome{id: id, kind: str("kind"), by: str("by"), status: str("status"), after: num("after"), expired: expired})
			}
		}
	}
	stats := func(w *waits) replyWaitStats {
		return replyWaitStats{waitStats: waitsOf(w.v), Unanswered: w.unanswered}
	}
	for k, w := range replyAll {
		out.ReplyWait.All[k] = stats(w)
	}
	for k, w := range replyMain {
		out.ReplyWait.Main[k] = stats(w)
	}
	for day, w := range replyDays {
		d := replyWaitDay{Day: day, replyWaitStats: stats(w)}
		if n, known := dayTasks[day]; known {
			d.OpenTasks = &n
		}
		out.ReplyWait.MainDays = append(out.ReplyWait.MainDays, d)
	}
	slices.SortFunc(out.ReplyWait.MainDays, func(a, b replyWaitDay) int { return cmp.Compare(b.Day, a.Day) })
	for _, d := range days {
		// A round nobody saw start ("other": a new conversation's first message is the owner's, a background task's
		// report is not) is on neither side of the share.
		total := 0.0
		for source, sec := range d.Seconds {
			if source != roundOther {
				total += sec
			}
		}
		if total > 0 {
			d.BackgroundShare = (total - d.Seconds[roundOwner]) / total
		}
		out.BackgroundShare = append(out.BackgroundShare, *d)
	}
	slices.SortFunc(out.BackgroundShare, func(a, b roundDay) int {
		return cmp.Or(cmp.Compare(b.Day, a.Day), cmp.Compare(a.Conversation, b.Conversation))
	})
	for kind, q := range queue {
		out.QueueWait.ByKind[kind] = queueWaitStats{waitStats: waitsOf(q.v), MaxSeconds: slices.Max(q.v), MaxDepth: q.depth}
	}
	for kind, q := range queueMain {
		out.QueueWait.MainByKind[kind] = queueWaitStats{waitStats: waitsOf(q.v), MaxSeconds: slices.Max(q.v), MaxDepth: q.depth}
	}
	afters := map[[2]string][]float64{}
	for _, o := range outcomes {
		who := o.by
		switch {
		case o.expired:
			who = askWhoExpired
		case o.status != "resolved":
			who = askWhoDismissed
		case o.by != askByAssistant:
		case standing[o.id]:
			who = askWhoStanding
		case delegated[o.id]:
			who = askWhoDelegate
		default: // a conversation's interaction_answer
			who = askWhoSupervisor
		}
		afters[[2]string{o.kind, who}] = append(afters[[2]string{o.kind, who}], o.after)
	}
	for _, a := range asks {
		afters[[2]string{a.kind, a.who}] = append(afters[[2]string{a.kind, a.who}], a.after)
	}
	for k, v := range afters {
		if out.AskOutcome[k[0]] == nil {
			out.AskOutcome[k[0]] = map[string]waitStats{}
		}
		out.AskOutcome[k[0]][k[1]] = waitsOf(v)
	}
	return out
}

// availabilityAsks are the task cards (a supervised task's decision or conclusion, a deployed task's "Please verify")
// asked since since and answered, as the acceptance book still has them: by the owner, the task's supervisor, or by
// default at a reversible decision's deadline. A card whose task was closed or asked again is no longer in the book.
func (s *Service) availabilityAsks(ctx context.Context, since time.Time) []taskAsk {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(ctx) {
		return nil
	}
	var out []taskAsk
	for _, a := range b.asks {
		if a == nil || a.AskedAt.Before(since) || a.AcceptedAt.IsZero() {
			continue
		}
		t := taskAsk{kind: "verify", who: askByOwner, after: max(a.AcceptedAt.Sub(a.AskedAt).Seconds(), 0)}
		switch {
		case decideAsk(a):
			t.kind = "decision"
		case a.Conclusion:
			t.kind = "conclusion"
		}
		switch {
		case a.ByDefault:
			t.who = askWhoDefault
		case a.BySupervisor, a.ByAssistant:
			t.who = askWhoSupervisor
		}
		out = append(out, t)
	}
	return out
}

// availabilityQueued are the conversations with system messages waiting now.
func (s *Service) availabilityQueued(ctx context.Context, now time.Time) []queuedNow {
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(ctx) {
		b.mu.Unlock()
		return nil
	}
	var out []queuedNow
	for id, q := range b.st.Queue {
		if len(q) == 0 {
			continue
		}
		oldest := now
		for _, msg := range q {
			at := msg.QueuedAt
			if at.IsZero() {
				at = msg.At
			}
			if !at.IsZero() && at.Before(oldest) {
				oldest = at
			}
		}
		out = append(out, queuedNow{Conversation: id, Depth: len(q), OldestSeconds: now.Sub(oldest).Seconds()})
	}
	b.mu.Unlock()
	for i := range out {
		out[i].Main = s.isMain(ctx, out[i].Conversation)
	}
	slices.SortFunc(out, func(a, b queuedNow) int {
		return cmp.Or(cmp.Compare(b.OldestSeconds, a.OldestSeconds), cmp.Compare(a.Conversation, b.Conversation))
	})
	return out
}

func (s *Service) availabilityRoutes() {
	// ?hours=168 (the default: a week; at most 30 days, as long as the events are kept).
	s.handle("GET /api/v1/assistant/availability", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("only a full device reads the availability metrics"))
			return
		}
		hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
		if hours <= 0 || hours > 24*30 {
			hours = 24 * 7
		}
		st := s.workDB()
		if st == nil {
			apierr.Write(w, http.StatusServiceUnavailable, errors.New("no database"))
			return
		}
		now := time.Now()
		since := now.Add(-time.Duration(hours) * time.Hour)
		events, err := st.WorkEvents(r.Context(), since.Add(-availMarkBefore))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, summarizeAvailability(hours, since, events, s.location(r.Context()), s.availabilityAsks(r.Context(), since),
			s.availabilityQueued(r.Context(), now)))
	})
}
