package assistant

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The knowledge base is the assistant's (docs/assistant/knowledge.md "Who owns what"): the engine (internal/kb), its loop, its files
// under the data directory and its index rows in Postgres. Only the assistant writes them. The hub keeps the sources:
// what its connectors deliver is read over the bus (kb_sources.go), the posts their programs hand it come over the bus
// (kb_posts.go), and the hub's features that read the knowledge base ask here (kb_bus.go). The kb_* tools, the live
// context and the apps' /api/v1/kb routes (kb_routes.go) are local calls.
//
// How it is kept is the kb section of the manager's manager.json, laid over the hub's hello (ownconfig.go). The knowledge base opens (kbGet) once that came, the user's time zone could be read (the archive is
// put into it first, so nothing is written in another zone) and the data directory's lock is this process's; it
// closes while the assistant is not connected to the hub, so a process the hub no longer takes (its token replaced)
// lets the data directory go to the one it does.

// KBOptions is where the assistant keeps the knowledge base.
type KBOptions struct {
	// Dir is the data directory (~/.persona/data, the hub's own when the assistant runs beside it).
	Dir string
	// KeyDir holds handles.key, the key of the senders' identity hints (kb/handles.go); "": none are kept.
	KeyDir string
}

const (
	kbPoll = 2 * time.Minute
	// kbChangedFloor is how soon after it was last read a connector that says it changed is read again (and the items):
	// a chatty connector cannot make every round read it.
	kbChangedFloor = time.Minute
	kbDebounce     = 5 * time.Second
	kbItemsPoll    = 30 * time.Minute
	kbCompressMax  = 8   // chats compressed per run
	kbTagItemsMax  = 3   // item tagging calls per run (40 items each); what is left goes on next round
	kbWaitingMost  = 200 // agents' turns kept until it opens
	// kbRetryWaiting is how soon the loop tries to open again while agents' turns wait for it (the user's zone not
	// read yet, the data directory still locked by the process before): not at the next round, two minutes on.
	kbRetryWaiting = time.Second
)

var (
	errKBOff = errors.New("the knowledge base is off (manager.json kb.enabled)")
	// errKBNotOpen: it is on but not open yet (no word from the hub yet, the user's time zone not readable, the data
	// directory another process's).
	errKBNotOpen = errors.New("the knowledge base is not open yet; try again in a moment")
)

// kbState is the knowledge base's part of the Service.
type kbState struct {
	owner       kbOwnerState // encrypted owner identities and their source provenance (kb_owner.go)
	events      kbEventQueue // bounded, asynchronous notifications (kb_events.go)
	dir, keyDir string       // "" dir: none (unit tests that do not need one)
	kick        chan struct{}
	wake        chan struct{}            // the loop looks again at once (kbWake)
	cur         atomic.Pointer[kbOpened] // nil while not open

	open    sync.Mutex // opening and closing
	store   *kb.Store
	key     []byte
	unlock  func()
	passed  bool // the one-time passes ran (once a process)
	notOpen time.Time

	// sourceWrites lets deletion finish after already running connector reads and writes.
	sourceWrites     sync.Mutex
	postWrites       sync.Mutex // separate: a connector read may synchronously submit posts
	archiveCleanupMu sync.Mutex
	archiveCleanup   map[string]archiveReservation
	mu               sync.Mutex
	archivedAt       map[string]time.Time        // connector id → when its archive was last asked (kb_sources.go)
	changed          map[string]bool             // connectors that said their chats changed: read at the next round (kbChangedFloor)
	itemsNew         bool                        // a connector said its items changed: collected at the next round
	agentAt          map[string]time.Time        // agent → when its turn last went into a project timeline (kb_bus.go)
	waiting          []assistantwire.KBAgentTurn // turns that came before it opened
	postsDirty       atomic.Bool                 // posts came in since the post archive's index was written (kb_posts.go)
	retries          kb.Retries                  // the steps' backoffs, kept across the engines built one after another

	history   kbHistoryShared  // the history import (kb_history.go)
	selection kbSelectionState // what is brought in and kept (kb_selection.go)
}

// kbOpened is the open knowledge base and the hub's settings it was opened with.
type kbOpened struct {
	eng *kb.Engine
	set assistantwire.KBSettings
}

// kbIndex is the optional Postgres index of the entities (internal/db implements it).
type kbIndex interface {
	PutKBEntity(ctx context.Context, kind, id, name, importance string, relevance int, tags []string, oneLine string, updated time.Time) error
	DeleteKBEntities(ctx context.Context, kind string, ids []string) error
}

// kbInit records where the knowledge base is (nil: none); it opens when it is first needed (kbGet).
func (s *Service) kbInit(o *KBOptions) {
	s.kb.kick = make(chan struct{}, 1)
	s.kb.wake = make(chan struct{}, 1)
	s.kbHistoryInit()
	if o != nil {
		s.kb.dir, s.kb.keyDir = o.Dir, o.KeyDir
	}
}

// kbSettings is manager.json's kb section, laid over the hub's hello (ownconfig.go); nil before the first hello.
func (s *Service) kbSettings(ctx context.Context) *assistantwire.KBSettings {
	if s.kb.dir == "" {
		return &assistantwire.KBSettings{Off: true}
	}
	return s.hello(ctx).KB
}

// kbOn: the hub keeps a knowledge base (open or not yet).
func (s *Service) kbOn(ctx context.Context) bool {
	set := s.kbSettings(ctx)
	return set != nil && !set.Off
}

// kbGet is the knowledge base, opened with the hub's settings now: errKBOff when the hub has none, errKBNotOpen while
// it cannot open yet.
func (s *Service) kbGet(ctx context.Context) (*kb.Engine, error) {
	set := s.kbSettings(ctx)
	switch {
	case set == nil:
		return nil, errKBNotOpen
	case set.Off:
		return nil, errKBOff
	}
	if o := s.kb.cur.Load(); o != nil && sameKBSettings(o.set, *set) {
		return o.eng, nil
	}
	return s.kbOpen(ctx, *set)
}

func sameKBSettings(a, b assistantwire.KBSettings) bool {
	return a.ReadOnly == b.ReadOnly && a.Off == b.Off && a.TextDays == b.TextDays && a.AttachmentDays == b.AttachmentDays && slices.Equal(a.ExtraDirs, b.ExtraDirs) &&
		maps.Equal(a.AttachmentDaysBySource, b.AttachmentDaysBySource)
}

// kbOpen opens the knowledge base with set: the data directory's lock, the store put into the user's zone, the
// one-time passes, an engine with set's limits (a new one when the hub's settings changed).
func (s *Service) kbOpen(ctx context.Context, set assistantwire.KBSettings) (*kb.Engine, error) {
	k := &s.kb
	k.open.Lock()
	if o := k.cur.Load(); o != nil && sameKBSettings(o.set, set) {
		k.open.Unlock()
		return o.eng, nil
	}
	e, err := s.kbOpenLocked(ctx, set)
	k.open.Unlock()
	if err != nil {
		if time.Since(k.notOpen) > time.Minute { // said once a minute
			k.notOpen = time.Now()
			s.logf("kb: not open: %v", err)
		}
		return nil, errKBNotOpen
	}
	s.kbTakeWaiting(e)
	s.attendResume(e) // the meetings attended before it opened (a restart) go on from their live logs (meeting_feed.go)
	return e, nil
}

func (s *Service) kbOpenLocked(ctx context.Context, set assistantwire.KBSettings) (*kb.Engine, error) {
	k := &s.kb
	if set.ReadOnly {
		st, err := kb.OpenReadOnly(k.dir)
		if err != nil {
			return nil, err
		}
		e := &kb.Engine{Store: st, TextDays: set.TextDays, AttachmentDays: set.AttachmentDays, AttachmentDaysBySource: set.AttachmentDaysBySource, RecordingDaysFunc: s.recordingDaysNow, MinutesDaysFunc: s.minutesDaysNow, ExtraDirs: kbReadDirs(set.ExtraDirs),
			Selection: s.kbSelectionNow, TopicsOn: s.kbTopicsNow}
		k.cur.Store(&kbOpened{eng: e, set: set})
		return e, nil
	}
	loc, ok := s.kbZone(ctx)
	if !ok {
		return nil, errors.New("the user's time zone could not be read")
	}
	if k.store == nil {
		if err := os.MkdirAll(k.dir, 0o700); err != nil {
			return nil, err
		}
		unlock, err := lockKBDir(k.dir)
		if err != nil {
			return nil, err
		}
		st, err := kb.NewStore(k.dir)
		if err != nil {
			unlock()
			return nil, err
		}
		if k.keyDir != "" && k.key == nil {
			if key, err := handleKey(k.keyDir); err != nil {
				s.logf("kb: identity hints off: %v", err) // never the key
			} else {
				k.key = key
			}
		}
		k.store, k.unlock = st, unlock
		if err := s.kbOwnerOpen(k.key); err != nil {
			s.logf("kb: owner identities unavailable")
		}
	}
	s.kbRezone(k.store, loc)
	// The models are counted per lane run (kb_lanes.go: kbCounted, through the run's context).
	// Retries: the steps' backoffs carry over to the engine after this one (the hub's settings changed; #4247).
	e := &kb.Engine{Store: k.store, Retries: &k.retries, Model: kbCounted(s.kbModel("inbox.triage")), ModelFor: func(id string) kb.Model { return kbCounted(s.kbModel(id)) },
		Owner: s.owner(ctx), Logf: s.logf, TextDays: set.TextDays, AttachmentDays: set.AttachmentDays, AttachmentDaysBySource: set.AttachmentDaysBySource, HandleKey: k.key, Index: s.kbIndexEntity, Unindex: s.kbUnindex, RecordingDaysFunc: s.recordingDaysNow, MinutesDaysFunc: s.minutesDaysNow, MinutesWritten: s.minutesWritten, LiveChanged: s.meetingKick, MeetingChanged: s.meetingLiveChanged, EarlierMeetingsInAnswers: true, Changed: s.kbChanged, MineObserved: s.kbObserveOwner,
		Selection: s.kbSelectionNow, TopicsOn: s.kbTopicsNow,
		// All-day recordings (kb_day.go): the recap time from the evening routine's, their names; the day's push below
		// (it counts the day's parts in e).
		DayRecapClock: s.eveningClock, DayRecordingName: s.dayRecordingName}
	e.DayRecapWritten = func(ctx context.Context, r kb.Recording, d kb.DayRecap) ([]string, error) {
		return s.dayRecapWritten(ctx, e, r, d)
	}
	e.ObserveChanges()
	e.ExtraDirs = kbReadDirs(set.ExtraDirs)
	if !k.passed {
		k.passed = true
		// Projects from before keywords existed get them at their next rewrite: the active ones are rewritten now (a
		// few a run), so their Moments (company feed) posts can be found.
		if n := e.SeedKeywords(60 * 24 * time.Hour); n > 0 {
			s.logf("kb: %d project(s) to be rewritten for their keywords", n)
		}
		if n := e.IgnoreNoiseProjects(); n > 0 {
			s.logf("kb: %d project(s) from notifications marked ignored", n)
		}
		if n := e.PruneProjectAliases(); n > 0 {
			s.logf("kb: took %d alias(es) off projects: other projects' names, too generic, or past the bound", n)
		}
		if n := e.RetagMail(); n > 0 {
			s.logf("kb: %d mail chat(s) queued to be tagged again under the current rules", n)
		}
	}
	k.cur.Store(&kbOpened{eng: e, set: set})
	return e, nil
}

// kbClose lets the knowledge base go (the data directory's lock with it): the assistant is not connected, or stops.
// It opens again when it is next needed.
func (s *Service) kbClose() {
	k := &s.kb
	k.open.Lock()
	defer k.open.Unlock()
	k.cur.Store(nil)
	if k.unlock != nil {
		k.unlock()
	}
	k.store, k.unlock = nil, nil
}

// kbError is what the hub gets for a call the knowledge base cannot take now: kb_unavailable while it is not open
// (the hub answers 503, try again).
func kbError(err error) error {
	if errors.Is(err, kb.ErrReadOnly) {
		return &rpc.Error{Code: assistantwire.CodeKBReadOnly, Message: err.Error()}
	}
	if errors.Is(err, errKBOff) {
		return &rpc.Error{Code: assistantwire.CodeKBDisabled, Message: err.Error()}
	}
	if errors.Is(err, errKBNotOpen) {
		return &rpc.Error{Code: assistantwire.CodeKBUnavailable, Message: err.Error()}
	}
	return err
}

// kbIndexEntity mirrors an entity into the index table, when the store has one.
func (s *Service) kbIndexEntity(kind, id, name string, t kb.Tagging, one string, updated time.Time) {
	idx, ok := s.store().(kbIndex)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := idx.PutKBEntity(ctx, kind, id, name, t.Importance, t.Relevance, t.Tags, one, updated); err != nil {
		s.logf("kb index: %v", err)
	}
}

// kbUnindex removes index rows (recordings deleted or past their retention), when the store has an index.
func (s *Service) kbUnindex(kind string, ids []string) {
	idx, ok := s.store().(kbIndex)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := idx.DeleteKBEntities(ctx, kind, ids); err != nil {
		s.logf("kb index: %v", err)
	}
}

// kbZone is the user's time zone the archive is written in (as location reads it); false while the assistant's
// settings cannot be read: then the archive is not moved (into the machine's zone, and back a round later) and nothing
// is written.
func (s *Service) kbZone(ctx context.Context) (*time.Location, bool) {
	v, ok := s.savedSettings(ctx)
	if !ok {
		return nil, false
	}
	return zoneOf(v.Timezone), true
}

// kbRezone keeps the archive in the user's time zone (kb/zone.go): the days and times written in another (the hub's
// own, UTC, or before the user moved) are moved into it.
func (s *Service) kbRezone(st *kb.Store, loc *time.Location) {
	n, err := st.Rezone(loc)
	if n > 0 {
		s.logf("kb: %d chat(s) / timeline(s) moved into %s", n, loc)
	}
	if err != nil {
		s.logf("kb: %v", err)
	}
}

// kbFileWaiting files the agents' turns that wait for the knowledge base, when it can open now (the hub's settings
// just came, kept from the hello).
func (s *Service) kbFileWaiting(ctx context.Context) {
	if !s.kbWaiting() {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if e, err := s.kbGet(cctx); err == nil {
		s.kbTakeWaiting(e)
	} else {
		s.kbWake() // the loop tries again (the user's zone not read yet, the data directory still locked)
	}
}

// kbWaiting: agents' turns wait for the knowledge base to open.
func (s *Service) kbWaiting() bool {
	s.kb.mu.Lock()
	defer s.kb.mu.Unlock()
	return len(s.kb.waiting) > 0
}

// kbWake has the loop look again now, without kbKick's wait for more news.
func (s *Service) kbWake() {
	select {
	case s.kb.wake <- struct{}{}:
	default:
	}
}

// kbNoteKick notes what a kb.kick says changed: that connector's chats are read at the next round (kbConnectors), or
// the items collected, even before their poll interval; at most every kbChangedFloor.
func (s *Service) kbNoteKick(k assistantwire.KBKick) {
	s.kb.mu.Lock()
	defer s.kb.mu.Unlock()
	switch {
	case k.Scope == string(connector.ScopeItems):
		s.kb.itemsNew = true
	case k.Connector != "":
		if s.kb.changed == nil {
			s.kb.changed = map[string]bool{}
		}
		s.kb.changed[k.Connector] = true
	}
}

// kbItemsDue says whether a round collects the items, last collected at last: every kbItemsPoll, or the round after a
// connector said its items changed (at most every kbChangedFloor).
func (s *Service) kbItemsDue(last time.Time) bool {
	s.kb.mu.Lock()
	defer s.kb.mu.Unlock()
	since := time.Since(last)
	due := since >= kbItemsPoll || s.kb.itemsNew && since >= kbChangedFloor
	if due {
		s.kb.itemsNew = false
	}
	return due
}

// kbKick has the loop go round soon (a source has something new).
func (s *Service) kbKick() {
	select {
	case s.kb.kick <- struct{}{}:
	default:
	}
}

// kbConnected: the assistant is connected to the hub now (true for a hub that cannot tell, the unit tests' fake).
func (s *Service) kbConnected() bool {
	c, ok := s.hub.(interface{ Connected() bool })
	return !ok || c.Connected()
}

// kbLoop archives, compresses and keeps up the knowledge base, every two minutes (five seconds after a kick, which
// also has the connector that changed read then), from when the assistant first connected until ctx ends. Each round
// starts the lanes (kb_lanes.go) that are not still running. While it is not connected the knowledge base is closed,
// once no lane runs (a lane started goes on to the end of its run with the engine it started with, as a round did). A
// mesh member's waits: the center's knowledge base is the group's (docs/server/mesh.md), this one's stays as it was.
func (s *Service) kbLoop(ctx context.Context) {
	if s.kb.dir == "" {
		return
	}
	ctx = ai.Background(ctx) // its model calls give their turn back when queued under a connection's cap (ai/limit.go)
	var r kbRounds
	lanes := s.kbLaneSet(&r)
	var debounce <-chan time.Time // a kick's round, kbDebounce after it
	t := time.NewTicker(kbPoll)
	defer t.Stop()
	for {
		switch {
		case !s.kbConnected():
			if !lanes.busy() {
				s.kbClose()
			}
		case !s.hello(ctx).Assistant.Standby:
			if e, err := s.kbGet(ctx); err == nil {
				s.kbTakeWaiting(e) // turns that came while it could not open, also when another call opened it
				lanes.startDue(func() bool { return s.kbPrepare(ctx, e) }, func(l *kbLane) { s.kbRunLane(ctx, e, l) })
			}
		}
		var soon <-chan time.Time
		if s.kbWaiting() {
			soon = time.After(kbRetryWaiting)
		}
		select {
		case <-ctx.Done():
			return
		case <-soon:
		case l := <-lanes.done:
			lanes.ended(l) // a lane that became due while it ran starts now
		case <-s.kb.wake:
			lanes.tick()
		case <-t.C:
			lanes.tick()
		case <-s.kb.kick:
			if debounce == nil { // a round in kbDebounce, for what else comes meanwhile; the loop goes on handling the rest
				debounce = time.After(kbDebounce)
			}
		case <-debounce:
			debounce = nil
			lanes.tick()
		}
	}
}

// kbRounds is when the archive lane last did what it does less often than every run (only that lane touches it).
type kbRounds struct {
	items, prune, links time.Time
	tagMore             bool // item tagging stopped at its cap with untagged items left
}

// kbPrepare is what a round checks before it starts lanes, quickly, on the loop's goroutine: nothing when the
// knowledge base is read-only or the user's zone cannot be read (false: the lanes wait for the next round); else the
// owner's name for the models' prompts read again. Moving the archive into the user's zone (Store.Rezone, which waits
// for the archive's lock) is the archive lane's first step, so the loop never waits behind a lane's writes.
func (s *Service) kbPrepare(ctx context.Context, e *kb.Engine) bool {
	if e.Store.ReadOnly() || !s.kbWritable(ctx) {
		return false
	}
	if _, ok := s.kbZone(ctx); !ok {
		s.logf("kb: the user's time zone could not be read: archiving waits for the next round")
		return false
	}
	e.SetOwner(s.owner(ctx)) // the lanes read it while they run
	return true
}

// kbArchiveLane is one run of the archive lane (kb_lanes.go): its steps one after another, each under kbRun so a panic
// in one is logged and the steps after it still run. First the archive is kept in the user's zone (kb/zone.go), before
// anything is archived in it.
func (s *Service) kbArchiveLane(ctx context.Context, e *kb.Engine, r *kbRounds) {
	loc, ok := s.kbZone(ctx)
	if !ok {
		return // read a moment ago (kbPrepare); the next round tries again
	}
	s.kbRun(ctx, func() { s.kbRezone(e.Store, loc) })
	s.kbRun(ctx, func() { s.kbConnectors(ctx, e) }) // mail, GitHub, the plugins'…, each when it is due
	s.kbRun(ctx, func() {
		for i, c := range e.DueForCompression(time.Now()) {
			if i >= kbCompressMax || ctx.Err() != nil {
				break
			}
			if err := e.Compress(ctx, c); err != nil {
				if ai.IsPaused(err) { // the chats stay due: compressed once the connection is back
					break
				}
				s.logf("kb: compress %s: %v", c.ID, err)
			}
		}
	})
	s.kbRun(ctx, func() { e.RefreshProjects(ctx, 3) })
	// Recordings: those past recording_days go, or keep only their minutes until minutes_days (every run: a setting may
	// just have shortened), complete ones get their summary (their minutes are the minutes lane's).
	s.kbRun(ctx, func() {
		if n := e.PruneRecordings(time.Now()); n > 0 {
			s.logf("kb: pruned %d recording(s)", n)
		}
		s.meetingRoomsSweep(ctx, e) // the rooms of transcripts cut, and of recordings gone (meeting_room.go)
		e.SummarizeRecordings(ctx, 3)
	})
	// Same work under two names, and the Moments (company feed) posts about each project: a few model calls every half hour.
	if time.Since(r.links) >= kbItemsPoll {
		r.links = time.Now()
		s.kbRun(ctx, func() {
			if n := e.DedupeProjects(ctx, 4); n > 0 {
				s.logf("kb: merged %d duplicate project(s)", n)
			}
			if n := e.LinkMoments(ctx, 3); n > 0 {
				s.logf("kb: linked %d 公司圈 post(s) to projects", n)
			}
		})
	}
	switch {
	case s.kbItemsDue(r.items):
		r.items = time.Now()
		s.kbRun(ctx, func() { r.tagMore = s.kbCollectItems(ctx, e, r.tagMore) })
	case r.tagMore: // untagged items left past the last run's cap
		s.kbRun(ctx, func() { r.tagMore = s.kbTagItems(ctx, e, r.tagMore) })
	}
	daily := time.Since(r.prune) >= 24*time.Hour
	if daily {
		r.prune = time.Now()
		s.kbRun(ctx, func() {
			if n := e.Prune(time.Now()); n > 0 {
				s.logf("kb: pruned %d day(s) and document(s)", n)
			}
		})
	}
	s.kbRun(ctx, func() { s.kbPostsUpkeep(e, daily) })
}

// kbRun calls f, turning a panic into a log line with its stack: one malformed message must not stop the loop,
// but the part it broke must still leave a trace to debug.
func (s *Service) kbRun(ctx context.Context, f func()) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("kb: panic: %v\n%s", r, debug.Stack())
		}
	}()
	if ctx.Err() == nil {
		f()
	}
}

// kbTextRetention is how long the knowledge base keeps text (kb.text_days, a year by default).
func kbTextRetention(e *kb.Engine) time.Duration {
	days := e.TextDays
	if days <= 0 {
		days = 365
	}
	return time.Duration(days) * 24 * time.Hour
}

// expandHome turns a leading "~/" into this machine's home.
func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

// kbExtraDirs are manager.json's kb.extra_dirs as written, for CLAUDE.md (none while the knowledge base is off).
func (s *Service) kbExtraDirs(ctx context.Context) []string {
	if set := s.kbSettings(ctx); set != nil && !set.Off {
		return set.ExtraDirs
	}
	return nil
}
