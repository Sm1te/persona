package subscriptions

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
)

// Retention of a subscription's dedup keys.
const (
	seenKeep    = 5000
	seenMaxAge  = 400 * 24 * time.Hour
	RunsKept    = 20
	leaseLength = 5 * time.Minute
)

// Store keeps the subscriptions, their dedup keys and their pending items (internal/db: the manager's subscriptions,
// subscription_seen and subscription_items).
type Store interface {
	ListSubscriptions(ctx context.Context) ([]Subscription, error)
	// GetSubscription is nil when there is none with id.
	GetSubscription(ctx context.Context, id string) (*Subscription, error)
	CreateSubscription(ctx context.Context, s Subscription) error
	// UpdateSubscription writes all of s if the stored revision is prev; false when it is not.
	UpdateSubscription(ctx context.Context, s Subscription, prev int64) (bool, error)
	// DeleteSubscription removes it with its dedup keys and pending items.
	DeleteSubscription(ctx context.Context, id string) error
	// LeaseSubscription holds id for a run until until, if no other run holds it at now; false when one does.
	LeaseSubscription(ctx context.Context, id string, now, until time.Time) (bool, error)
	// SeenKeys says which of uids the subscription has seen.
	SeenKeys(ctx context.Context, id string, uids []string) (map[string]bool, error)
	// CommitRun writes a run's outcome in one transaction (RunCommit).
	CommitRun(ctx context.Context, c RunCommit) error
	// HeldItems are the subscription's held items q picks, in the order they were stored (#2039).
	HeldItems(ctx context.Context, id string, q ItemQuery) ([]Held, error)
	// PruneItems removes the subscription's held items stored before before (their dedup keys stay).
	PruneItems(ctx context.Context, id string, before time.Time) error
}

// ItemQuery picks held items: those stored at or after Since, after the stored order After, at most Limit (the
// oldest, or the newest when Newest), returned in stored order.
type ItemQuery struct {
	After  int64
	Since  time.Time
	Limit  int
	Newest bool
}

// Held is an item as it waits for the destinations: its place in the stored order and when its run stored it.
type Held struct {
	Seq    int64
	Stored time.Time
	Item
}

// RunCommit is what a run writes at once: the new dedup keys and items, the cursor and history, and the state. The
// state (RunState, Error, FailCount, FailingSince, PausedUntil, ShapeMisses, ToolFingerprint) is written only while
// the subscription is still at Revision: a run that started before the user changed the recipe does not undo the
// change. The run's lease is released.
type RunCommit struct {
	Sub      Subscription
	Revision int64
	Seen     []Seen
	Items    []Item
	Now      time.Time
	// Baseline is the baseline mark the run took (Subscription.Baseline): it is cleared only while it is still that
	// one, so a recipe changed again during the run keeps its own.
	Baseline *time.Time
}

// MemStore is a Store in memory, for tests and a manager without a database.
type MemStore struct {
	mu    sync.Mutex
	subs  map[string]Subscription
	seen  map[string]map[string]time.Time
	items []Held
	seq   int64
	// FailCommit, when set, fails every CommitRun with it (a crash in the middle of a run).
	FailCommit error
}

func NewMemStore() *MemStore {
	return &MemStore{subs: map[string]Subscription{}, seen: map[string]map[string]time.Time{}}
}

func (m *MemStore) ListSubscriptions(context.Context) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Subscription, 0, len(m.subs))
	for _, s := range m.subs {
		out = append(out, clone(s))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemStore) GetSubscription(_ context.Context, id string) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok {
		return nil, nil
	}
	c := clone(s)
	return &c, nil
}

func (m *MemStore) CreateSubscription(_ context.Context, s Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[s.ID] = clone(s)
	return nil
}

func (m *MemStore) UpdateSubscription(_ context.Context, s Subscription, prev int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.subs[s.ID]
	if !ok || cur.Revision != prev {
		return false, nil
	}
	m.subs[s.ID] = clone(s)
	return true, nil
}

func (m *MemStore) DeleteSubscription(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, id)
	delete(m.seen, id)
	m.items = slices.DeleteFunc(m.items, func(it Held) bool { return it.SubscriptionID == id })
	return nil
}

func (m *MemStore) LeaseSubscription(_ context.Context, id string, now, until time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.subs[id]
	if !ok || s.LeaseUntil != nil && s.LeaseUntil.After(now) {
		return false, nil
	}
	s.LeaseUntil = &until
	m.subs[id] = s
	return true, nil
}

func (m *MemStore) SeenKeys(_ context.Context, id string, uids []string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for _, u := range uids {
		if _, ok := m.seen[id][u]; ok {
			out[u] = true
		}
	}
	return out, nil
}

func (m *MemStore) CommitRun(_ context.Context, c RunCommit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailCommit != nil {
		return m.FailCommit
	}
	cur, ok := m.subs[c.Sub.ID]
	if !ok {
		return nil // deleted while it ran: nothing is kept
	}
	if m.seen[c.Sub.ID] == nil {
		m.seen[c.Sub.ID] = map[string]time.Time{}
	}
	for _, k := range c.Seen {
		if _, had := m.seen[c.Sub.ID][k.UID]; !had {
			m.seen[c.Sub.ID][k.UID] = c.Now
		}
	}
	for _, it := range c.Items {
		it.SubscriptionID = c.Sub.ID
		m.seq++
		m.items = append(m.items, Held{Seq: m.seq, Stored: c.Now, Item: it})
	}
	m.pruneSeen(c.Sub.ID, c.Now)
	applyRun(&cur, c, cur.Revision == c.Revision)
	cur.LeaseUntil = nil
	m.subs[c.Sub.ID] = cur
	return nil
}

func (m *MemStore) pruneSeen(id string, now time.Time) {
	keys := m.seen[id]
	type kv struct {
		k string
		t time.Time
	}
	var all []kv
	for k, t := range keys {
		if now.Sub(t) > seenMaxAge {
			delete(keys, k)
			continue
		}
		all = append(all, kv{k, t})
	}
	if len(all) <= seenKeep {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.After(all[j].t) })
	for _, e := range all[seenKeep:] {
		delete(keys, e.k)
	}
}

func (m *MemStore) HeldItems(_ context.Context, id string, q ItemQuery) ([]Held, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Held
	for _, it := range m.items {
		if it.SubscriptionID == id && it.Seq > q.After && !it.Stored.Before(q.Since) {
			out = append(out, it)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		if q.Newest {
			out = out[len(out)-q.Limit:]
		} else {
			out = out[:q.Limit]
		}
	}
	return slices.Clone(out), nil
}

func (m *MemStore) PruneItems(_ context.Context, id string, before time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = slices.DeleteFunc(m.items, func(it Held) bool { return it.SubscriptionID == id && it.Stored.Before(before) })
	return nil
}

// Items are the pending items held, in order (tests).
func (m *MemStore) Items() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Item, len(m.items))
	for i, it := range m.items {
		out[i] = it.Item
	}
	return out
}

// SeenCount is how many dedup keys a subscription has (tests).
func (m *MemStore) SeenCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen[id])
}

// applyRun copies a run's outcome onto cur: the cursor, history and timings always, the state only when withState.
func applyRun(cur *Subscription, c RunCommit, withState bool) {
	out := c.Sub
	if c.Baseline != nil && sameTime(cur.Baseline, c.Baseline) {
		cur.Baseline = nil
	}
	cur.SinceAt, cur.SnapshotHash, cur.Runs = out.SinceAt, out.SnapshotHash, out.Runs
	cur.LastRunAt, cur.LastOKAt, cur.LastNewCount = out.LastRunAt, out.LastOKAt, out.LastNewCount
	cur.WeakDedup, cur.Shape, cur.NextRunAt = out.WeakDedup, out.Shape, out.NextRunAt
	cur.SourceName, cur.Icon, cur.Service, cur.SourceMin, cur.MaxCallsPerHour = out.SourceName, out.Icon, out.Service, out.SourceMin, out.MaxCallsPerHour
	if sameTime(cur.InboxFrom, out.InboxFrom) { // the catch-up mark of the Inbox turned on as the run saw it, not of another
		cur.InboxCatchup = out.InboxCatchup
	}
	if withState {
		cur.RunState, cur.Error, cur.FailCount, cur.FailingSince = out.RunState, out.Error, out.FailCount, out.FailingSince
		cur.PausedUntil, cur.ShapeMisses, cur.ToolFingerprint = out.PausedUntil, out.ShapeMisses, out.ToolFingerprint
	}
}

func sameTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func clone(s Subscription) Subscription {
	s.Runs = slices.Clone(s.Runs)
	return s
}
