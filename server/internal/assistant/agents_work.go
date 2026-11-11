package assistant

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Where an agent stands in its work (docs/assistant/agent-work.md, #1686), the manager's: when a turn ends it triages it — rules
// first, then a cheap model reading the last reply — into done or blocked on a named precondition, and acts on what it
// can: "Continue" after a transient model error (with backoff and a cap), a wake when what it waits for should be over, a
// card for what the user is to decide (the answer goes back to the agent). Everything else (pushing code, deploying,
// messaging people, touching someone else's MR) is only ever a card. Each action checks, the moment before, that the
// session is still stopped on the same turn with nothing pending or queued: whatever the user did meanwhile wins.
//
// The hub carries it (assistantwire/agents.go): it says when a turn starts and ends (session.turn_started / ended)
// and when the user looked at a session (session.seen); the work states go to it in the snapshot (Snapshot.Work, put
// on the session lists), the cards among the snapshot's interactions (answered with manager.action); what the manager
// sends an agent goes through device.call session.send.

// TriageSwitches turn the triage and each of its actions on, all off until the user turns them on (Settings › Session Triage,
// settings agents.triage): the triage calls a model on every stopped turn, and a "Continue" or a wake runs a turn of the
// agent on its own model (docs/assistant/agent-work.md "Switches").
type TriageSwitches struct {
	Enabled      bool `json:"enabled"`       // triage stopped turns: the lists' line, done no longer unread
	AutoContinue bool `json:"auto_continue"` // "Continue" after a transient model error, and once a usage limit reset
	Wake         bool `json:"wake"`          // wake agents whose wait should be over
	Decide       bool `json:"decide"`        // cards for what the user is to decide
	ToolLoop     bool `json:"tool_loop"`     // detect repeated tool calls while a turn runs
	// Closeout: hold an agent's stop once when a turn that did work ends without a closeout, and ask for one
	// (agents_closeout.go). The held stop runs one more turn of the agent on its own model.
	Closeout bool `json:"closeout"`
	// CloseoutAsk: ask an agent nobody supervises where its work stands, once, when a turn that did work ended
	// without a closeout and the triage could not call it finished (agents_closeout_ask.go). The question runs one
	// turn of the agent on its own model.
	CloseoutAsk bool `json:"closeout_ask"`
	// ReplyReminder: push once when an agent the user looked at still waits on their answer (agents_reply.go,
	// #5096). Rules only, no tokens: unset is on.
	ReplyReminder *bool `json:"reply_reminder,omitempty"`
	// ReplyReminderMinutes: after how long since the user looked. 0: 30 (a PUT with 0 keeps it, a negative one clears it).
	ReplyReminderMinutes int `json:"reply_reminder_minutes,omitempty"`
	// TransientRetry: "Continue" after a transient model error (a dropped connection, a 5xx, overloaded), with backoff
	// and a cap (#3208). A rule, no triage model call: independent of Enabled, and unset is on.
	TransientRetry *bool `json:"transient_retry,omitempty"`
}

// transientOn: a transient model error is continued automatically (the switch unset counts as on).
func (sw TriageSwitches) transientOn() bool { return sw.TransientRetry == nil || *sw.TransientRetry }

// active: the manager follows stopped turns at all: the triage, or only the transient errors it continues.
func (sw TriageSwitches) active() bool { return sw.Enabled || sw.transientOn() }

// wakes: w's pending wake still goes under sw. A transient error's "Continue" is transient_retry's alone; everything
// else needs the main switch, then closeout_ask for a status question, auto_continue for a usage limit's continue and
// wake for a wait's wake.
func (sw TriageSwitches) wakes(w proto.WorkState) bool {
	switch {
	case w.CloseoutAsk == closeoutAskPending:
		return sw.Enabled && sw.CloseoutAsk
	case w.On == proto.WaitTransient && w.ResetsAt == nil:
		return sw.transientOn()
	case !sw.Enabled:
		return false
	case continues(w):
		return sw.AutoContinue
	}
	return sw.Wake
}

const triageSettingKey = "agents.triage"

func set(b *bool) bool { return b != nil && *b }

func switchesOf(c assistantwire.TriageConfig) TriageSwitches {
	return TriageSwitches{Enabled: set(c.Enabled), AutoContinue: set(c.AutoContinue), Wake: set(c.Wake), Decide: set(c.Decide), ToolLoop: set(c.ToolLoop),
		Closeout: set(c.Closeout), CloseoutAsk: set(c.CloseoutAsk), TransientRetry: c.TransientRetry}
}

func maxRetries(c assistantwire.TriageConfig) int { return cmp0(c.MaxRetries, 5) }
func maxWakes(c assistantwire.TriageConfig) int   { return cmp0(c.MaxWakes, 6) }
func settleOf(c assistantwire.TriageConfig) time.Duration {
	return time.Duration(cmp0(c.SettleSeconds, 15)) * time.Second
}

func cmp0(n, def int) int {
	if n > 0 {
		return n
	}
	return def
}

// retryBackoff is how long after a transient error the "Continue" numbered attempt (0: the first) goes: the base
// (retry_base_seconds, 15 s by default) doubled for each attempt before it, four times that for a rate limit, never
// more than retryBackoffMost. 15 s, 30 s, 60 s, 120 s, 240 s by default; 60 s … 960 s for a rate limit.
func retryBackoff(c assistantwire.TriageConfig, attempt int, rateLimited bool) time.Duration {
	d := time.Duration(cmp0(c.RetryBaseSeconds, 15)) * time.Second
	if rateLimited {
		d *= 4
	}
	for range max(attempt, 0) {
		if d >= retryBackoffMost {
			break
		}
		d *= 2
	}
	return min(d, retryBackoffMost)
}

// retryBackoffMost caps one transient error's backoff, however large max_retries is set.
const retryBackoffMost = 30 * time.Minute

// workEvery is how often the manager looks for due wakes and expired cards; workBackfillEvery how often it looks for
// turns that ended unseen (while it was away, or whose triage failed a while ago).
var (
	workEvery         = 15 * time.Second
	workBackfillEvery = time.Minute
	workRetryTriage   = 10 * time.Minute
)

const (
	workCardTool  = assistantwire.WorkCardTool // the card's tool_name: who asks is Persona, about the agent's turn
	workCardTTL   = 24 * time.Hour
	workKeep      = 30 * 24 * time.Hour
	workBackfill  = 24 * time.Hour // turns that ended this long ago still get triaged when they were missed
	workSampleGap = 5 * time.Minute
)

// continueText (tasks_start.go) is what goes on a turn the model service cut off, as the chat's "Retry" on an API error card.

// Card answers the manager acts on itself instead of handing them to the agent: each one's default-language label,
// which names it here. A card shows answerLabel's (the user's language); canonicalAnswer reads any language's back.
const (
	answerSkip    = "先不管"
	answerRetry   = "再试一次"
	answerHandled = "已处理，继续"
	answerAuthed  = "已授权，继续"
	answerWait    = "再等等"
	answerStop    = "不等了"
	answerGo      = "等好了就去做"
	answerHold    = "先别做"
	answerNotNow  = "先不"
	answerOK      = "好，按你说的做"
)

// answerKeys are the answers' texts in this file's namespace.
var answerKeys = map[string]string{answerSkip: "answer_skip", answerRetry: "answer_retry", answerHandled: "answer_handled",
	answerAuthed: "answer_authed", answerWait: "answer_wait", answerStop: "answer_stop", answerGo: "answer_go",
	answerHold: "answer_hold", answerNotNow: "answer_not_now", answerOK: "answer_ok"}

// answerLabel is answer a's label in l.
func answerLabel(l i18n.Lang, a string) string {
	if k, ok := answerKeys[a]; ok {
		return workT(l, k)
	}
	return a
}

// canonicalAnswer is the answer a card's label a stands for, whatever language the card was shown in (a card stays
// up across a change of language); any other text as it is.
func canonicalAnswer(a string) string {
	for name, k := range answerKeys {
		for _, l := range i18n.Langs {
			if workT(l, k) == a {
				return name
			}
		}
	}
	return a
}

// workT is one of this file's texts in l.
func workT(l i18n.Lang, name string, args ...i18n.Args) string {
	return i18n.T(l, "assistant-agents_work."+name, args...)
}

// workStore is the database's session_work and session_work_events (db/session_work.go): only the manager reads and
// writes them.
type workStore interface {
	PutSessionWork(ctx context.Context, w db.SessionWork) error
	SessionWorks(ctx context.Context, since time.Time) ([]db.SessionWork, error)
	PruneSessionWork(ctx context.Context, before time.Time) error
	AddWorkEvent(ctx context.Context, e db.WorkEvent) error
	WorkEvents(ctx context.Context, since time.Time) ([]db.WorkEvent, error)
}

// workBook is the manager's copy of session_work, plus what only matters while it runs.
type workBook struct {
	mu           sync.Mutex
	entries      map[[2]string]*db.SessionWork
	cards        map[string]proto.Interaction // the cards out, by id (the snapshot's interactions)
	loaded       bool
	triaging     map[[2]string]bool
	ciPolling    map[[2]string]bool           // bounded concurrent check reads
	loops        map[[2]string]*toolLoopState // running-turn loop candidates and cards
	loopLoadMu   sync.Mutex                   // serialize loading without holding work.mu on the database
	loopSaveMu   sync.Mutex                   // serialize durable loop snapshots
	switchMu     sync.Mutex                   // serialize settings updates
	loopLoaded   bool
	loopSaved    bool
	loopSnapshot []byte                        // last successfully persisted canonical snapshot, protected by mu
	loopInvalid  map[[2]string]loopClearReason // activity received while the saved state could not be read
	generation   map[[2]string]uint64          // user activity invalidates in-flight triage
	tried        map[[2]string]triedTurn       // the turn a triage was last attempted on, and when
	sent         map[[2]string]time.Time       // when the manager itself last sent the session something
	// told is when anything of this manager last sent the session a message (Service.call), the triage's own sends
	// and every other (a supervisor's rework, a task's follow-up, the assistant's session tool); would is the triage
	// action that alone would have gone on with the session's last stop had its switch been on (actOn). Both are for
	// the weekly setup block's counts (setup_suggest.go) and are not kept over a restart: what they cannot say is
	// not counted.
	told  map[[2]string]time.Time
	would map[[2]string]string
	seen         map[[2]string]time.Time       // the turn whose first look was recorded
	started      map[[2]string]time.Time       // local time of the latest turn-start event
	viewed       map[[2]string]bool            // a view since that event
	approved     map[[2]string]approval        // a risky next step the user said yes to, for the wait it ends
	// answering marks cards whose answer is being delivered to the agent: a repeated callback in the send window
	// (a redelivered webhook, a second tap, phone and watch together) is the same answer, not a second one.
	answering map[string]bool
	writes    map[[2]string]*sync.Mutex
	switches  *TriageSwitches // the saved switches, once read (nil: not read yet)
	// delegated are the questions the assistant answered for the user, by session (fleet_delegate.go), read back
	// from the events once (delegatedLoaded): the same question again is the user's.
	delegated       map[[2]string][]delegatedAsk
	delegatedLoaded bool

	sampleAt, pruneAt, backfillAt time.Time
	publishing                    atomic.Bool // a snapshot is on its way (publishSoon)
	// testEvents collects the events when there is no store (tests).
	testEvents []db.WorkEvent
	// eventWrites are the event inserts still on their way to the store (the tests wait on them).
	eventWrites sync.WaitGroup
}

func (b *workBook) init() {
	if b.entries == nil {
		b.entries = map[[2]string]*db.SessionWork{}
		b.cards = map[string]proto.Interaction{}
		b.triaging = map[[2]string]bool{}
		b.ciPolling = map[[2]string]bool{}
		b.loops = map[[2]string]*toolLoopState{}
		b.generation = map[[2]string]uint64{}
		b.tried = map[[2]string]triedTurn{}
		b.sent = map[[2]string]time.Time{}
		b.told = map[[2]string]time.Time{}
		b.would = map[[2]string]string{}
		b.seen = map[[2]string]time.Time{}
		b.started = map[[2]string]time.Time{}
		b.viewed = map[[2]string]bool{}
		b.approved = map[[2]string]approval{}
		b.answering = map[string]bool{}
		b.writes = map[[2]string]*sync.Mutex{}
	}
}

// approval is the user's "Do it once it's ready" on a risky wait's card: once the wait is over, the agent may take that step. It
// holds while the agent keeps waiting (a wake, the agent's own check), so the same question is not asked again each
// turn; anything the user does themselves, a turn that is no wait, or a riskier step ends it.
type approval struct {
	wait, then string
	task       string // the todo whose text authorized it (agents_work_authorized.go); "" the user's own yes
}

// covers: a may stand for w's next step as far as the words go — w waits, and its step is no riskier than the approved
// one (each risky word in it was in the approved step). No link: an authorization is the user's to give each time.
// The words cannot tell a target apart (beta or master, one person or the group): applyApproval also wants the triage
// model to say it is the same wait and the same step.
func (a approval) covers(w *proto.WorkState) bool {
	if a.then == "" || w.Phase != proto.WorkBlocked || w.Wait == "" || w.Link != nil {
		return false
	}
	approved := strings.ToLower(a.then)
	for _, word := range riskyWordsIn(w.Then) {
		if !strings.Contains(approved, word) {
			return false
		}
	}
	return true
}

// triedTurn is a turn a triage was attempted on: a failed one is tried again after workRetryTriage, not every tick.
type triedTurn struct{ turn, at time.Time }

// triageCfg is manager.json's agents.triage, as the hello has it.
func (s *Service) triageCfg(ctx context.Context) assistantwire.TriageConfig {
	return s.hello(ctx).Agents.Triage
}

// triageSwitches are the switches in force: the saved ones, else hub.json's. Read from the database once; while it
// cannot be read, hub.json's (off when unset).
func (s *Service) triageSwitches(ctx context.Context) TriageSwitches {
	sw, _ := s.readTriageSwitches(ctx)
	return sw
}

// readTriageSwitches keeps read failures separate from an unset setting. Internal triage may use the returned
// defaults on failure, but the settings API must report the error so clients cannot save those defaults back.
func (s *Service) readTriageSwitches(ctx context.Context) (TriageSwitches, error) {
	s.work.mu.Lock()
	sw := s.work.switches
	s.work.mu.Unlock()
	if sw != nil {
		return *sw, nil
	}
	def := switchesOf(s.triageCfg(ctx))
	st := s.store()
	if st == nil {
		return def, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var saved TriageSwitches
	ok, err := st.Setting(ctx, triageSettingKey, &saved)
	if err != nil {
		return def, err // read again next time
	}
	if !ok {
		saved = def
	}
	s.work.mu.Lock()
	// A settings PUT or another initial read may have finished while the store was answering.
	if s.work.switches == nil {
		s.work.switches = &saved
	}
	saved = *s.work.switches
	s.work.mu.Unlock()
	return saved, nil
}

func (s *Service) workOn(ctx context.Context) bool { return s.triageSwitches(ctx).Enabled }

// workActive: stopped turns are followed at all (workOn, or only to continue transient errors).
func (s *Service) workActive(ctx context.Context) bool { return s.triageSwitches(ctx).active() }

// putTriageSwitches saves the switches. What they turn off stops at once: the cards are withdrawn, due retries and
// wakes dropped; the apps hear of it ({"type":"agents.triage"}) and the lists follow the next snapshot.
func (s *Service) putTriageSwitches(ctx context.Context, sw TriageSwitches) error {
	s.work.switchMu.Lock()
	defer s.work.switchMu.Unlock()
	turnOff := !sw.Enabled || !sw.ToolLoop
	clearLoops := turnOff
	if turnOff {
		// If it was already off, its saved rows can still hold pending expiry
		// history. Updating another switch must not load or overwrite them.
		if previous, err := s.readTriageSwitches(ctx); err == nil {
			if !previous.Enabled || !previous.ToolLoop {
				clearLoops = false
			} else if err := s.loadToolLoops(ctx); err != nil {
				return err
			}
		}
	}
	loopLocked := false
	defer func() {
		if loopLocked {
			s.work.loopSaveMu.Unlock()
			s.work.loopLoadMu.Unlock()
		}
	}()
	if clearLoops {
		// A load or loop save must not resurrect old cards between the atomic
		// settings commit and the in-memory clear.
		s.work.loopLoadMu.Lock()
		s.work.loopSaveMu.Lock()
		loopLocked = true
	}
	var expired []savedToolLoop
	if clearLoops {
		s.work.mu.Lock()
		for k, state := range s.work.loops {
			if state.expiredID != "" && (!state.consuming || state.consumedSaved) {
				expired = append(expired, savedToolLoop{Machine: k[0], Session: k[1], UpdatedAt: time.Now().UTC(), ExpiredID: state.expiredID})
			}
		}
		s.work.mu.Unlock()
	}
	var loopSnapshot []byte
	if st := s.store(); st != nil {
		if clearLoops {
			var err error
			loopSnapshot, err = toolLoopSnapshot(expired)
			if err != nil {
				return err
			}
			batch, ok := st.(interface {
				PutSettings(context.Context, map[string]any) error
			})
			if !ok {
				return errors.New("atomic triage settings writes are unavailable")
			}
			if expired == nil {
				expired = []savedToolLoop{}
			}
			if err := batch.PutSettings(ctx, map[string]any{triageSettingKey: sw, toolLoopsSetting: expired}); err != nil {
				return err
			}
		} else if err := st.PutSetting(ctx, triageSettingKey, sw); err != nil {
			return err
		}
	}
	s.work.mu.Lock()
	s.work.init()
	s.work.switches = &sw
	if clearLoops {
		for _, loop := range s.work.loops {
			delete(s.work.cards, loop.cardID)
		}
		clear(s.work.loops)
		for _, row := range expired {
			s.work.loops[[2]string{row.Machine, row.Session}] = &toolLoopState{updatedAt: row.UpdatedAt, expiredID: row.ExpiredID}
		}
		s.work.loopLoaded, s.work.loopSaved = true, len(expired) != 0
		s.work.loopSnapshot = loopSnapshot
		s.work.loopInvalid = nil
	}
	var stale []db.SessionWork
	for _, e := range s.work.entries {
		off := !sw.wakes(e.Work) && (!sw.Enabled || continues(e.Work) || e.Work.On == proto.WaitExternal || e.Work.On == proto.WaitAgent ||
			e.Work.CloseoutAsk == closeoutAskPending)
		if e.InteractionID != "" && (!sw.Enabled || !sw.Decide) {
			delete(s.work.cards, e.InteractionID)
			e.InteractionID, e.Work.InteractionID = "", ""
			stale = append(stale, *e)
		} else if e.WakeAt != nil && off {
			e.WakeAt, e.Work.WakeAt = nil, nil
			stale = append(stale, *e)
		}
	}
	s.work.mu.Unlock()
	if loopLocked {
		s.work.loopSaveMu.Unlock()
		s.work.loopLoadMu.Unlock()
		loopLocked = false
	}
	for _, e := range stale {
		s.saveWork(e)
	}
	s.event(ctx, map[string]any{"type": "agents.triage", "triage": sw})
	s.publish(ctx)
	return nil
}

func (s *Service) workDB() workStore {
	st, _ := s.store().(workStore)
	return st
}

// sameTurn: c is still on the turn that ended at turnAt (a stopped transcript does not change).
func sameTurn(c proto.Session, turnAt time.Time) bool {
	d := c.UpdatedAt.Sub(turnAt)
	return d > -2*time.Second && d < 2*time.Second
}

// loadWork reads session_work back (once; false while it cannot be read). A card the hub still has up (only the
// manager restarted) stays up as it is, until it is answered, its agent goes on or it expires, as when the triage was
// the hub's; the others (the hub restarted too, or it was answered meanwhile and the hub keeps the answer) are put back
// under the same id while their turn still waits, those whose machine is not back yet by a later tick (cardTick).
func (s *Service) loadWork(ctx context.Context) bool {
	s.work.mu.Lock()
	s.work.init()
	loaded := s.work.loaded
	s.work.mu.Unlock()
	if loaded {
		return true
	}
	var rows []db.SessionWork
	if st := s.workDB(); st != nil {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var err error
		rows, err = st.SessionWorks(lctx, time.Now().Add(-workBackfill*2))
		cancel()
		if err != nil {
			s.logf("work: load: %v", err)
			return false // tried again next time
		}
	}
	v := s.newWorkView(ctx)
	now := time.Now()
	var forgot []db.SessionWork
	var forgotIDs []string
	cards := map[string]proto.Interaction{}
	var up map[string]proto.Interaction
	for i := range rows {
		e := rows[i]
		if e.InteractionID == "" {
			continue
		}
		if up == nil {
			up = s.cardsUp(ctx)
		}
		if ix, ok := up[e.InteractionID]; ok && !now.After(e.TurnAt.Add(workCardTTL)) {
			ix.CanCancel = true // The manager can retire its own cards, including cards restored from older hubs.
			cards[e.InteractionID] = ix
			continue
		}
		switch ix, keep := s.cardBack(v, e, now); {
		case ix != nil:
			cards[e.InteractionID] = *ix
		case !keep:
			forgotIDs = append(forgotIDs, e.InteractionID)
			rows[i].InteractionID, rows[i].Work.InteractionID = "", ""
			forgot = append(forgot, rows[i])
		}
	}
	s.work.mu.Lock()
	if s.work.loaded { // loaded meanwhile by another caller
		s.work.mu.Unlock()
		return true
	}
	for i := range rows {
		r := rows[i]
		k := [2]string{r.MachineID, r.SessionID}
		if _, ok := s.work.entries[k]; !ok {
			s.work.entries[k] = &r
		}
	}
	for id, ix := range cards {
		s.work.cards[id] = ix
	}
	s.work.loaded = true
	s.work.mu.Unlock()
	for _, e := range forgot {
		s.saveWork(e)
	}
	for _, id := range forgotIDs {
		s.recordEnd(id, "cancelled", nil) // kept pending across the restart for this (db ExpireStaleInteractions)
	}
	return true
}

func (s *Service) saveWork(e db.SessionWork) {
	unlock := s.lockWorkWrite([2]string{e.MachineID, e.SessionID})
	defer unlock()
	// A caller may have captured e before a newer answer was committed. Write
	// the current state after acquiring the session's write lock, never that
	// stale snapshot over a durable cancellation.
	if current, ok := s.workEntry([2]string{e.MachineID, e.SessionID}); ok {
		e = current
	}
	st := s.workDB()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := st.PutSessionWork(ctx, e); err != nil {
		s.logf("work: save %s/%s: %v", e.MachineID, e.SessionID, err)
	}
}

func (s *Service) workEvent(machine, session, kind string, detail map[string]any) {
	e := db.WorkEvent{At: time.Now().UTC(), MachineID: machine, SessionID: session, Kind: kind, Detail: detail}
	st := s.workDB()
	if st == nil {
		s.work.mu.Lock()
		s.work.testEvents = append(s.work.testEvents, e)
		s.work.mu.Unlock()
		return
	}
	s.work.eventWrites.Add(1)
	go func() {
		defer s.work.eventWrites.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.AddWorkEvent(ctx, e); err != nil {
			s.logf("work: event: %v", err)
		}
	}()
}

// workEntry is a copy of a session's stored work state.
func (s *Service) workEntry(k [2]string) (db.SessionWork, bool) {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	s.work.init()
	e, ok := s.work.entries[k]
	if !ok {
		return db.SessionWork{}, false
	}
	return *e, true
}

// putWork stores e (memory and database) and reports it (the snapshot: the hub pushes the lists that changed).
func (s *Service) putWork(_ context.Context, e db.SessionWork) {
	e.UpdatedAt = time.Now().UTC()
	s.work.mu.Lock()
	s.work.init()
	key := [2]string{e.MachineID, e.SessionID}
	if current := s.work.entries[key]; current != nil && current.Work.Deferred && current.TurnAt.Equal(e.TurnAt) {
		e.Work.Deferred = true
		e.InteractionID, e.Work.InteractionID, e.WakeAt, e.Work.WakeAt = "", "", nil, nil
	}
	c := e
	s.work.entries[key] = &c
	s.work.mu.Unlock()
	s.saveWork(e)
	s.publishSoon()
}

// putTriageWork commits a scheduled external wait only if no session activity
// arrived while triage was reading its transcript.
func (s *Service) putTriageWork(e db.SessionWork, generation uint64) bool {
	key := [2]string{e.MachineID, e.SessionID}
	s.work.mu.Lock()
	s.work.init()
	current := s.work.entries[key]
	if s.work.generation[key] != generation || current != nil && (current.TurnAt.After(e.TurnAt) || current.Work.Deferred && current.TurnAt.Equal(e.TurnAt)) {
		s.work.mu.Unlock()
		return false
	}
	if e.Work.CI != nil && s.work.viewed[key] {
		e.WakeAt, e.Work.WakeAt, e.Work.CI = nil, nil, nil
	}
	e.UpdatedAt = time.Now().UTC()
	saved := e
	s.work.entries[key] = &saved
	s.work.mu.Unlock()
	s.saveWork(e)
	s.publishSoon()
	return true
}

// workPublishDelay is how long the changes of a pass (a tick, a triage, an answer) are gathered into one snapshot.
var workPublishDelay = 200 * time.Millisecond

// publishSoon reports the snapshot a moment from now, once for whatever changed meanwhile (at once when the delay is
// 0: unit tests).
func (s *Service) publishSoon() {
	if workPublishDelay == 0 {
		s.publish(context.Background())
		return
	}
	if !s.work.publishing.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(workPublishDelay, func() {
		s.work.publishing.Store(false)
		s.publish(context.Background())
	})
}

// ---- what the hub shows ----

// workReport is the snapshot's work: the stored states (with the switches' On), nil while they could not be read.
func (s *Service) workReport(ctx context.Context) *WorkReport {
	sw := s.triageSwitches(ctx)
	if sw.Enabled && sw.ToolLoop {
		if err := s.loadToolLoops(ctx); err != nil {
			s.logf("tool loop: load: %v", err)
			return nil
		}
	}
	if !s.loadWork(ctx) {
		return nil
	}
	r := &WorkReport{On: sw.Enabled, Sessions: []SessionWork{}}
	if !sw.active() {
		return r
	}
	s.work.mu.Lock()
	for _, e := range s.work.entries {
		if !workShown(&e.Work) {
			continue // unsure: nothing shown
		}
		if !r.On && !transientPending(e.Work) {
			continue // triage off: only a "Continue" on its way is told (fleet_work.go)
		}
		w := e.Work
		w.CI, w.RetiredAt, w.RetiredTurnAt, w.CloseoutAsk = nil, nil, nil, "" // internal state stays in the manager's store
		w.TurnAt = e.TurnAt
		r.Sessions = append(r.Sessions, SessionWork{MachineID: e.MachineID, SessionID: e.SessionID, Work: w})
	}
	s.work.mu.Unlock()
	sort.Slice(r.Sessions, func(i, j int) bool {
		a, b := r.Sessions[i], r.Sessions[j]
		return a.MachineID+"/"+a.SessionID < b.MachineID+"/"+b.SessionID
	})
	return r
}

// workCards are the cards out (the snapshot's interactions besides ask_user's), oldest first.
func (s *Service) workCards() []proto.Interaction {
	s.work.mu.Lock()
	defer s.work.mu.Unlock()
	out := make([]proto.Interaction, 0, len(s.work.cards))
	for _, ix := range s.work.cards {
		out = append(out, ix)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// ---- the hub's events ----

// workReconnected: the manager is connected again (or for the first time): the hub sent nothing meanwhile (a hub that
// just started serves its bus last, after its nodes came up), so the next tick looks for the turns that ended unseen.
func (s *Service) workReconnected() {
	s.work.mu.Lock()
	s.work.backfillAt = time.Time{}
	s.work.mu.Unlock()
}

// SessionSeen is session.seen: the user looked at a session in an app.
func (s *Service) SessionSeen(_ context.Context, p SessionSeen) {
	s.workSeen(p.MachineID, p.SessionID, p.At)
}
