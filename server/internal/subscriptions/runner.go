package subscriptions

import (
	"context"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

// The runner's limits (docs/extensions/subscriptions.md "Running").
const (
	tickEvery        = 30 * time.Second
	startJitter      = 60 * time.Second
	callGap          = 20 * time.Second // between two calls to one connector
	callJitter       = 5 * time.Second
	defaultPerHour   = 120
	maxRunning       = 4 // connectors running at once
	backoffMax       = 6 * time.Hour
	connFailsToPause = 3           // calls in a row that failed before the whole connector backs off
	connBackoffStart = time.Minute // doubling up to backoffMax
	shapeMissesMax   = 2           // runs in a row whose items cannot be read before the recipe needs redoing
)

// gate is one connector's limits, shared by all its subscriptions: one call at a time and at least callGap apart, an
// hourly budget, and a pause after rate_limited or repeated failures. It is kept in memory; a pause is also on each
// subscription (PausedUntil), from which a restarted manager takes it back.
type gate struct {
	calls  []time.Time // in the last hour
	last   time.Time
	paused time.Time
	fails  int
}

func (g *gate) prune(now time.Time) {
	g.calls = slices.DeleteFunc(g.calls, func(t time.Time) bool { return now.Sub(t) >= time.Hour })
}

func (s *Service) gateOf(id string) *gate {
	g := s.gates[id]
	if g == nil {
		g = &gate{}
		s.gates[id] = g
	}
	return g
}

// poke has the loop look for due work now.
func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run schedules and runs subscriptions until ctx is done: it waits a random part of a minute first (a fleet of
// restarted managers does not call every source at once), then looks every 30 seconds and whenever asked.
func (s *Service) Run(ctx context.Context) {
	if s.o.Clock.Sleep(ctx, rand.N(startJitter)) != nil {
		return
	}
	for {
		s.startDue(ctx)
		t := time.NewTimer(tickEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.wake:
		case <-t.C:
		}
		t.Stop()
	}
}

// RunDue starts what is due and waits for it (tests, and a manual pass).
func (s *Service) RunDue(ctx context.Context) {
	s.startDue(ctx).Wait()
}

// startDue starts one worker for each connector with due subscriptions, at most maxRunning at once, each running its
// connector's due subscriptions one by one.
func (s *Service) startDue(ctx context.Context) *sync.WaitGroup {
	var wg sync.WaitGroup
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		s.o.Logf("subscriptions: list: %v", err)
		return &wg
	}
	now := s.now()
	due := map[string][]Subscription{}
	s.mu.Lock()
	for _, sub := range all {
		if p := sub.PausedUntil; p != nil && p.After(now) && sub.RunState == StateRateLimited {
			if g := s.gateOf(sub.Connector); g.paused.Before(*p) {
				g.paused = *p // as it was before a restart
			}
		}
		if IsPush(sub) {
			continue // filled by its device, never pulled
		}
		asked := s.asked[sub.ID]
		if !asked && (!on(sub) || sub.NextRunAt == nil || sub.NextRunAt.After(now)) {
			continue
		}
		if sub.LeaseUntil != nil && sub.LeaseUntil.After(now) {
			continue
		}
		due[sub.Connector] = append(due[sub.Connector], sub)
	}
	ids := make([]string, 0, len(due))
	for id := range due {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if s.busy[id] || s.running >= maxRunning {
			continue
		}
		subs := due[id]
		sort.SliceStable(subs, func(i, j int) bool {
			return s.asked[subs[i].ID] && !s.asked[subs[j].ID] || before(subs[i].NextRunAt, subs[j].NextRunAt)
		})
		s.busy[id] = true
		s.running++
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				s.mu.Lock()
				delete(s.busy, id)
				s.running--
				s.mu.Unlock()
			}()
			for _, sub := range subs {
				if ctx.Err() != nil {
					return
				}
				s.runOne(ctx, sub.ID)
			}
		}()
	}
	s.mu.Unlock()
	return &wg
}

func before(a, b *time.Time) bool {
	switch {
	case a == nil:
		return false
	case b == nil:
		return true
	}
	return a.Before(*b)
}

// runOne runs a subscription once, if it is still due and no other run holds it.
func (s *Service) runOne(ctx context.Context, id string) {
	sub, err := s.o.Store.GetSubscription(ctx, id)
	if err != nil || sub == nil {
		return
	}
	s.mu.Lock()
	asked := s.asked[id]
	g := s.gateOf(sub.Connector)
	now := s.now()
	g.prune(now)
	perHour := defaultPerHour
	if sub.MaxCallsPerHour > 0 {
		perHour = sub.MaxCallsPerHour
	}
	paused, full := g.paused.After(now), len(g.calls) >= perHour
	wait := time.Duration(0)
	if !g.last.IsZero() {
		wait = g.last.Add(callGap + rand.N(callJitter)).Sub(now)
	}
	resume := g.paused
	if full {
		resume = g.calls[0].Add(time.Hour)
	}
	s.mu.Unlock()
	if !asked && (!on(*sub) || sub.NextRunAt == nil || sub.NextRunAt.After(now)) {
		return
	}
	if paused || full {
		// Not a run: the connector's limits hold it back. A pause shows; the hourly budget only moves the run.
		next := *sub
		next.NextRunAt = &resume
		if paused {
			next.RunState, next.PausedUntil = StateRateLimited, &resume
		}
		s.commit(ctx, *sub, next, nil, nil, now, false)
		return
	}
	if wait > 0 && s.o.Clock.Sleep(ctx, wait) != nil {
		return
	}
	now = s.now()
	if ok, err := s.o.Store.LeaseSubscription(ctx, id, now, now.Add(leaseLength)); err != nil || !ok {
		return
	}
	s.mu.Lock()
	delete(s.asked, id)
	s.mu.Unlock()
	s.execute(ctx, *sub, asked, now)
}

// execute is one run of sub, holding its lease: check the connector and the tool, call it, keep what is new.
func (s *Service) execute(ctx context.Context, sub Subscription, asked bool, start time.Time) {
	next := sub
	next.PausedUntil = nil
	trigger := TriggerSchedule
	switch {
	case asked:
		trigger = TriggerNow
	case sub.FailCount > 0:
		trigger = TriggerRetry
	}
	c, ok := s.o.Connectors.Find(ctx, sub.Connector)
	if !ok || !c.ready() {
		// Not a failure and not a run: it resumes by itself once the connector can be called.
		next.RunState, next.Error = StateNotConnected, &Problem{CodeNotConnected, "The connector cannot be reached now."}
		n := s.nextRun(next, start, false)
		next.NextRunAt = &n
		s.commit(ctx, sub, next, nil, nil, start, false)
		return
	}
	sources, err := s.catalog(ctx, c, catalogFresh)
	if err != nil {
		p := catalogProblem(err)
		if p.Code == CodeNotConnected {
			next.RunState, next.Error = StateNotConnected, p
			n := s.nextRun(next, start, false)
			next.NextRunAt = &n
			s.commit(ctx, sub, next, nil, nil, start, false)
			return
		}
		s.failed(ctx, sub, next, p, trigger, start, 0)
		return
	}
	src, tool, refused := admit(sources, sub.Source, sub.Tool)
	if refused != nil {
		s.needsRecipe(ctx, sub, next, refused, trigger, start)
		return
	}
	next.SourceName, next.Icon, next.Service = src.Name, src.Icon, src.Service
	next.SourceMin, next.MaxCallsPerHour = intOr(src.MinIntervalSeconds), intOr(src.MaxCallsPerHour)
	if fp := fingerprint(tool); fp != sub.ToolFingerprint {
		// The tool changed: carry on only while the recipe's arguments still fit it.
		args, err := fillArgs(sub.Recipe.Args, vars(start, s.o.Location()))
		if err == nil {
			err = checkArgs(tool.InputSchema, args)
		}
		if err != nil {
			s.needsRecipe(ctx, sub, next, &Problem{CodeToolChanged, "The tool changed and no longer takes the recipe's arguments."}, trigger, start)
			return
		}
		s.o.Logf("subscription %s: its tool's description changed; the recipe still fits", sub.ID)
		next.ToolFingerprint = fp
	}
	since := start.Add(-firstBack)
	if sub.SinceAt != nil {
		since = sub.SinceAt.Add(-overlap)
	}
	s.mu.Lock()
	g := s.gateOf(sub.Connector)
	g.last = start
	g.calls = append(g.calls, start)
	s.mu.Unlock()
	pl := pull(ctx, c.Conn, sub.Source, sub.Recipe, since, s.o.Location(), sub.Label)
	ms := s.now().Sub(start).Milliseconds()
	if pl.problem != nil {
		switch {
		case pl.problem.Code == CodeRateLimited:
			until := s.now().Add(pl.retry)
			s.mu.Lock()
			if g.paused.Before(until) {
				g.paused = until
			}
			s.mu.Unlock()
			next.RunState, next.Error, next.PausedUntil, next.NextRunAt = StateRateLimited, pl.problem, &until, &until
			s.commit(ctx, sub, next, nil, &Run{At: start, MS: ms, Trigger: trigger, Error: pl.problem}, start, true)
		case pl.connError:
			next.RunState, next.Error = StateNotConnected, pl.problem
			n := s.nextRun(next, start, false)
			next.NextRunAt = &n
			s.commit(ctx, sub, next, nil, nil, start, false)
		case pl.problem.Code == CodeNoList:
			s.shapeMiss(ctx, sub, next, pl.problem, trigger, start, ms)
		case pl.problem.Code == CodeNotReadOnly:
			s.needsRecipe(ctx, sub, next, pl.problem, trigger, start)
		default:
			s.connFailed(sub.Connector)
			s.failed(ctx, sub, next, pl.problem, trigger, start, ms)
		}
		return
	}
	s.mu.Lock()
	g.fails = 0
	s.mu.Unlock()
	if pl.noTitle {
		s.shapeMiss(ctx, sub, next, &Problem{CodeShapeChanged, "The items no longer have a title where the recipe reads it."}, trigger, start, ms)
		return
	}
	run := Run{At: start, MS: ms, Trigger: trigger}
	var seen []Seen
	var items []Item
	// The first run that succeeds after a recipe changed with existing baseline records what it finds as seen and
	// keeps none of it: only what arrives later comes in.
	baseline := sub.Baseline
	next.Baseline = nil
	if pl.snapshot != nil {
		run.Count = 1
		if pl.snapHash != sub.SnapshotHash {
			it := *pl.snapshot
			it.SubscriptionID = sub.ID
			items, seen = append(items, it), append(seen, Seen{UID: it.UID, ContentHash: pl.snapHash})
			next.SnapshotHash = pl.snapHash
		}
	} else {
		run.Count = len(pl.items)
		known, err := s.o.Store.SeenKeys(ctx, sub.ID, uids(pl.items))
		if err != nil {
			s.o.Logf("subscription %s: seen keys: %v", sub.ID, err)
			s.failed(ctx, sub, next, &Problem{CodeCallFailed, "The subscription's seen items could not be read."}, trigger, start, ms)
			return
		}
		newest := sub.SinceAt
		for _, m := range newOnes(pl.items, known) {
			it := m.item
			it.SubscriptionID = sub.ID
			items, seen = append(items, it), append(seen, Seen{UID: it.UID, ContentHash: m.hash})
		}
		for _, m := range pl.items {
			if t := m.item.Time; t != nil && (newest == nil || t.After(*newest)) && !t.After(start) {
				tt := *t
				newest = &tt
			}
		}
		if newest == nil || len(pl.items) > 0 && allUntimed(pl.items) {
			newest = &start // items without times: the next run looks back from now
		}
		next.SinceAt = newest
		next.WeakDedup = pl.weak
	}
	if baseline != nil {
		run.Baseline, items = len(items), nil
	}
	run.New = len(items)
	next.Shape, next.ShapeMisses = pl.shape, 0
	next.RunState, next.Error, next.FailCount, next.FailingSince = StateOK, nil, 0, nil
	next.LastOKAt, next.LastNewCount = &start, len(items)
	// The first run since the Inbox was turned on ends its catch-up: what is stored up to it (this run's items too).
	caughtUp := false
	if from := inboxFrom(next); from != nil && (next.InboxCatchup == nil || next.InboxCatchup.Before(*from)) {
		next.InboxCatchup, caughtUp = &start, true
	}
	n := s.nextRun(next, start, false)
	next.NextRunAt = &n
	if !s.commit(ctx, sub, next, &commitData{seen: seen, items: items, baseline: baseline}, &run, start, true) {
		return
	}
	if len(items) > 0 {
		s.o.Logf("subscription %s: %d new of %d", sub.ID, len(items), run.Count)
	}
	if baseline != nil {
		s.o.Logf("subscription %s: baseline after a recipe change: %d of %d recorded as seen", sub.ID, run.Baseline, run.Count)
	}
	if len(items) > 0 || caughtUp {
		s.o.Delivered(next)
	}
	s.prune(ctx, next, start)
}

// Held items are kept past their destinations: the Inbox's window and a day more, and while the knowledge base takes
// them, until kbKeep (a knowledge base that stopped reading for longer misses them).
const (
	inboxKeep = 8 * 24 * time.Hour
	kbKeep    = 30 * 24 * time.Hour
)

// prune drops sub's held items its destinations are past.
func (s *Service) prune(ctx context.Context, sub Subscription, now time.Time) {
	keep := inboxKeep
	if sub.ToKB {
		keep = kbKeep
	}
	if err := s.o.Store.PruneItems(ctx, sub.ID, now.Add(-keep)); err != nil {
		s.o.Logf("subscription %s: prune its items: %v", sub.ID, err)
	}
}

func allUntimed(items []mapped) bool {
	for _, m := range items {
		if m.item.Time != nil {
			return false
		}
	}
	return true
}

// connFailed counts a failed call against the connector: after connFailsToPause in a row, all its subscriptions wait,
// a minute first and doubling up to backoffMax.
func (s *Service) connFailed(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.gateOf(id)
	g.fails++
	if g.fails >= connFailsToPause {
		d := min(connBackoffStart<<min(g.fails-connFailsToPause, 20), backoffMax)
		g.paused = s.now().Add(d)
		s.o.Logf("subscriptions: connector %s failed %d calls in a row: paused %s", id, g.fails, d)
	}
}

// failed records a failed run: retried after interval × 2^failures, at most backoffMax, never stopped.
func (s *Service) failed(ctx context.Context, sub, next Subscription, p *Problem, trigger string, start time.Time, ms int64) {
	next.RunState, next.Error = StateFailing, p
	next.FailCount = sub.FailCount + 1
	if sub.FailingSince == nil {
		next.FailingSince = &start
	}
	n := start.Add(backoff(time.Duration(max(sub.IntervalSeconds, minInterval))*time.Second, next.FailCount))
	next.NextRunAt = &n
	s.o.Logf("subscription %s: run failed (%s), %d in a row", sub.ID, p.Code, next.FailCount)
	s.commit(ctx, sub, next, nil, &Run{At: start, MS: ms, Trigger: trigger, Error: p}, start, true)
}

func backoff(base time.Duration, failures int) time.Duration {
	d := base
	for i := 0; i < failures && d < backoffMax; i++ {
		d *= 2
	}
	return min(d, backoffMax)
}

// shapeMiss is a run whose result the recipe can no longer read: a failure, and after shapeMissesMax in a row the
// recipe needs redoing.
func (s *Service) shapeMiss(ctx context.Context, sub, next Subscription, p *Problem, trigger string, start time.Time, ms int64) {
	next.ShapeMisses = sub.ShapeMisses + 1
	if next.ShapeMisses >= shapeMissesMax {
		s.needsRecipe(ctx, sub, next, &Problem{CodeShapeChanged, p.Message}, trigger, start)
		return
	}
	s.failed(ctx, sub, next, p, trigger, start, ms)
}

// needsRecipe pauses sub until its recipe is changed: it is not run again by itself.
func (s *Service) needsRecipe(ctx context.Context, sub, next Subscription, p *Problem, trigger string, start time.Time) {
	next.RunState, next.Error, next.NextRunAt = StateNeedsRecipe, p, nil
	s.o.Logf("subscription %s: needs a new recipe (%s)", sub.ID, p.Code)
	s.commit(ctx, sub, next, nil, &Run{At: start, Trigger: trigger, Error: p}, start, true)
}

type commitData struct {
	seen     []Seen
	items    []Item
	baseline *time.Time // the baseline mark this run took
}

// commit writes a run's outcome (a run with a Run, a skipped round without) and tells the apps; false when it could
// not be kept.
func (s *Service) commit(ctx context.Context, sub, next Subscription, d *commitData, run *Run, now time.Time, ran bool) bool {
	if ran {
		next.LastRunAt = &now
	}
	if run != nil {
		next.Runs = append(slices.Clone(sub.Runs), *run)
		if len(next.Runs) > RunsKept {
			next.Runs = next.Runs[len(next.Runs)-RunsKept:]
		}
	}
	if !on(next) && next.RunState != StateNeedsRecipe {
		next.NextRunAt = nil // off: run only when asked
	}
	c := RunCommit{Sub: next, Revision: sub.Revision, Now: now}
	if d != nil {
		c.Seen, c.Items, c.Baseline = d.seen, d.items, d.baseline
	}
	if err := s.o.Store.CommitRun(ctx, c); err != nil {
		// Nothing of the run is kept; its lease runs out and the run is made again, deduplicated.
		s.o.Logf("subscription %s: keep run: %v", sub.ID, err)
		return false
	}
	if cur, err := s.o.Store.GetSubscription(ctx, sub.ID); err == nil && cur != nil {
		s.o.Changed(shown(*cur), false)
	}
	return true
}

// nextRun is when sub runs next after a run at now: at its daily time, or after its interval with up to a tenth
// more at random so subscriptions made together drift apart.
func (s *Service) nextRun(sub Subscription, now time.Time, _ bool) time.Time {
	if sub.DailyAt != "" && sub.Recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
		return nextDaily(sub.DailyAt, now, s.o.Location())
	}
	iv := time.Duration(max(sub.IntervalSeconds, floorOf(sub.Recipe.Mode, sub.SourceMin))) * time.Second
	return now.Add(iv + rand.N(iv/10+1))
}

// nextDaily is the next hh:mm in loc after now.
func nextDaily(hhmm string, now time.Time, loc *time.Location) time.Time {
	h, _ := strconv.Atoi(hhmm[:2])
	m, _ := strconv.Atoi(hhmm[3:])
	l := now.In(loc)
	t := time.Date(l.Year(), l.Month(), l.Day(), h, m, 0, 0, loc)
	if !t.After(now) {
		t = time.Date(l.Year(), l.Month(), l.Day()+1, h, m, 0, 0, loc)
	}
	return t.UTC()
}
