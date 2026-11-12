package subscriptions

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// DB is what internal/db keeps subscriptions with (the manager's database).
type DB interface {
	Subscriptions(ctx context.Context) ([]db.SubscriptionRow, error)
	Subscription(ctx context.Context, id string) (*db.SubscriptionRow, error)
	CreateSubscription(ctx context.Context, r db.SubscriptionRow) error
	UpdateSubscription(ctx context.Context, r db.SubscriptionRow, prev int64) (bool, error)
	DeleteSubscription(ctx context.Context, id string) error
	LeaseSubscription(ctx context.Context, id string, now, until time.Time) (bool, error)
	SubscriptionSeen(ctx context.Context, id string, uids []string) (map[string]bool, error)
	CommitSubscriptionRun(ctx context.Context, id string, merge func(data []byte, revision int64) ([]byte, error),
		seen []db.SubscriptionSeenRow, items []db.SubscriptionItemRow, now time.Time) error
	SubscriptionItems(ctx context.Context, id string, q db.SubscriptionItemsQuery) ([]db.SubscriptionItemRow, error)
	PruneSubscriptionItems(ctx context.Context, id string, before time.Time) error
}

// DBStore is the Store in the manager's database.
type DBStore struct{ db DB }

func NewDBStore(d DB) *DBStore { return &DBStore{db: d} }

// kept is a subscription as its data column holds it: what the API shows, and what the runner keeps beside it.
type kept struct {
	Subscription
	Keep keptRun `json:"keep"`
}

type keptRun struct {
	Tool            string     `json:"tool"`
	ToolFingerprint string     `json:"tool_fingerprint"`
	SourceMin       int        `json:"source_min,omitempty"`
	MaxCallsPerHour int        `json:"max_calls_per_hour,omitempty"`
	SinceAt         *time.Time `json:"since_at,omitempty"`
	ShapeMisses     int        `json:"shape_misses,omitempty"`
	SnapshotHash    string     `json:"snapshot_hash,omitempty"`
	RunState        string     `json:"run_state,omitempty"`
	Runs            []Run      `json:"runs,omitempty"`
	InboxFrom       *time.Time `json:"inbox_from,omitempty"`
	InboxCatchup    *time.Time `json:"inbox_catchup,omitempty"`
	KBFrom          *time.Time `json:"kb_from,omitempty"`
	CatchupFrom     *time.Time `json:"catchup_from,omitempty"`
	Baseline        *time.Time `json:"baseline,omitempty"`
}

func encode(s Subscription) ([]byte, error) {
	return json.Marshal(kept{Subscription: s, Keep: keptRun{Tool: s.Tool, ToolFingerprint: s.ToolFingerprint, SourceMin: s.SourceMin,
		MaxCallsPerHour: s.MaxCallsPerHour, SinceAt: s.SinceAt, ShapeMisses: s.ShapeMisses, SnapshotHash: s.SnapshotHash,
		RunState: s.RunState, Runs: s.Runs, InboxFrom: s.InboxFrom, InboxCatchup: s.InboxCatchup, KBFrom: s.KBFrom,
		CatchupFrom: s.CatchupFrom, Baseline: s.Baseline}})
}

func decode(data []byte) (Subscription, error) {
	var k kept
	if err := json.Unmarshal(data, &k); err != nil {
		return Subscription{}, err
	}
	s := k.Subscription
	s.Tool, s.ToolFingerprint, s.SourceMin, s.MaxCallsPerHour = k.Keep.Tool, k.Keep.ToolFingerprint, k.Keep.SourceMin, k.Keep.MaxCallsPerHour
	s.SinceAt, s.ShapeMisses, s.SnapshotHash, s.RunState, s.Runs = k.Keep.SinceAt, k.Keep.ShapeMisses, k.Keep.SnapshotHash, k.Keep.RunState, k.Keep.Runs
	s.InboxFrom, s.InboxCatchup, s.KBFrom = k.Keep.InboxFrom, k.Keep.InboxCatchup, k.Keep.KBFrom
	s.CatchupFrom, s.Baseline = k.Keep.CatchupFrom, k.Keep.Baseline
	return s, nil
}

func fromRow(r db.SubscriptionRow) (Subscription, error) {
	s, err := decode(r.Data)
	if err != nil {
		return Subscription{}, err
	}
	s.ID, s.Connector, s.Source, s.Revision, s.LeaseUntil = r.ID, r.Connector, r.Source, r.Revision, r.LeaseUntil
	s.CreatedAt, s.UpdatedAt = r.CreatedAt, r.UpdatedAt
	return s, nil
}

func toRow(s Subscription) (db.SubscriptionRow, error) {
	data, err := encode(s)
	return db.SubscriptionRow{ID: s.ID, Connector: s.Connector, Source: s.Source, Revision: s.Revision, Data: data,
		LeaseUntil: s.LeaseUntil, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}, err
}

func (d *DBStore) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := d.db.Subscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Subscription, 0, len(rows))
	for _, r := range rows {
		s, err := fromRow(r)
		if err != nil {
			continue // a row this build cannot read is left alone
		}
		out = append(out, s)
	}
	return out, nil
}

func (d *DBStore) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	r, err := d.db.Subscription(ctx, id)
	if err != nil || r == nil {
		return nil, err
	}
	s, err := fromRow(*r)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *DBStore) CreateSubscription(ctx context.Context, s Subscription) error {
	r, err := toRow(s)
	if err != nil {
		return err
	}
	return d.db.CreateSubscription(ctx, r)
}

func (d *DBStore) UpdateSubscription(ctx context.Context, s Subscription, prev int64) (bool, error) {
	r, err := toRow(s)
	if err != nil {
		return false, err
	}
	return d.db.UpdateSubscription(ctx, r, prev)
}

func (d *DBStore) DeleteSubscription(ctx context.Context, id string) error {
	return d.db.DeleteSubscription(ctx, id)
}

func (d *DBStore) LeaseSubscription(ctx context.Context, id string, now, until time.Time) (bool, error) {
	return d.db.LeaseSubscription(ctx, id, now, until)
}

func (d *DBStore) SeenKeys(ctx context.Context, id string, uids []string) (map[string]bool, error) {
	return d.db.SubscriptionSeen(ctx, id, uids)
}

func (d *DBStore) CommitRun(ctx context.Context, c RunCommit) error {
	seen := make([]db.SubscriptionSeenRow, len(c.Seen))
	for i, k := range c.Seen {
		seen[i] = db.SubscriptionSeenRow{UID: k.UID, ContentHash: k.ContentHash}
	}
	items := make([]db.SubscriptionItemRow, 0, len(c.Items))
	for _, it := range c.Items {
		b, err := json.Marshal(it)
		if err != nil {
			return err
		}
		items = append(items, db.SubscriptionItemRow{UID: it.UID, Data: b})
	}
	return d.db.CommitSubscriptionRun(ctx, c.Sub.ID, func(data []byte, revision int64) ([]byte, error) {
		cur, err := decode(data)
		if err != nil {
			return nil, err
		}
		applyRun(&cur, c, revision == c.Revision)
		return encode(cur)
	}, seen, items, c.Now)
}

func (d *DBStore) HeldItems(ctx context.Context, id string, q ItemQuery) ([]Held, error) {
	rows, err := d.db.SubscriptionItems(ctx, id, db.SubscriptionItemsQuery{After: q.After, Since: q.Since, Limit: q.Limit, Newest: q.Newest})
	if err != nil {
		return nil, err
	}
	out := make([]Held, 0, len(rows))
	for _, r := range rows {
		var it Item
		if err := json.Unmarshal(r.Data, &it); err != nil {
			continue // a row this build cannot read is left out
		}
		it.SubscriptionID, it.UID = id, r.UID
		out = append(out, Held{Seq: r.Seq, Stored: r.CreatedAt, Item: it})
	}
	return out, nil
}

func (d *DBStore) PruneItems(ctx context.Context, id string, before time.Time) error {
	return d.db.PruneSubscriptionItems(ctx, id, before)
}
