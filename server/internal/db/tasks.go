package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Plans and conversation feed items for tasks dispatched from an assistant conversation (docs/assistant/unified-entry.md).

var ErrTaskPlanNotFound = errors.New("task plan not found")

// TaskPlanRow is a plan the assistant handed the user: Tasks is the JSON list the hub reads (hub.PlanTask).
type TaskPlanRow struct {
	ID           string          `json:"id"`
	Conversation string          `json:"conversation"`
	Tasks        json.RawMessage `json:"tasks"`
	State        string          `json:"state"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// FeedRow is one item the hub inserted into an assistant conversation.
type FeedRow struct {
	ID           string          `json:"id"`
	Conversation string          `json:"conversation"`
	At           time.Time       `json:"at"`
	Kind         string          `json:"kind"`
	Ref          string          `json:"ref"`
	Body         json.RawMessage `json:"body"`
}

func (d *DB) CreateTaskPlan(ctx context.Context, p TaskPlanRow) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO task_plans (id, conversation, tasks, state, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		p.ID, p.Conversation, []byte(p.Tasks), p.State, p.CreatedAt, p.UpdatedAt)
	return err
}

func (d *DB) TaskPlan(ctx context.Context, id string) (*TaskPlanRow, error) {
	var p TaskPlanRow
	var tasks []byte
	err := d.pool.QueryRow(ctx, `SELECT id, conversation, tasks, state, created_at, updated_at FROM task_plans WHERE id = $1`, id).
		Scan(&p.ID, &p.Conversation, &tasks, &p.State, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTaskPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	p.Tasks = tasks
	return &p, nil
}

// TaskPlansByID reads the plans with ids in one query (a feed names many), in no particular order: an id with no plan
// is left out, a repeated one read once. No ids: none, without asking.
func (d *DB) TaskPlansByID(ctx context.Context, ids []string) ([]TaskPlanRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := d.pool.Query(ctx, `SELECT id, conversation, tasks, state, created_at, updated_at FROM task_plans
		WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	return scanTaskPlans(rows)
}

// TaskPlans lists a conversation's plans, oldest first.
func (d *DB) TaskPlans(ctx context.Context, conversation string) ([]TaskPlanRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT id, conversation, tasks, state, created_at, updated_at FROM task_plans
		WHERE conversation = $1 ORDER BY created_at`, conversation)
	if err != nil {
		return nil, err
	}
	return scanTaskPlans(rows)
}

// TaskPlansMadeSince lists the plans made at or after since that were started or dismissed, oldest first: what the
// owner did with the plan cards lately, counted when a standing rule is suggested (assistant/rule_suggest.go, #4929).
func (d *DB) TaskPlansMadeSince(ctx context.Context, since time.Time) ([]TaskPlanRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT id, conversation, tasks, state, created_at, updated_at FROM task_plans
		WHERE created_at >= $1 AND state IN ('started', 'dismissed') ORDER BY created_at, id`, since)
	if err != nil {
		return nil, err
	}
	return scanTaskPlans(rows)
}

func scanTaskPlans(rows pgx.Rows) ([]TaskPlanRow, error) {
	defer rows.Close()
	var out []TaskPlanRow
	for rows.Next() {
		var p TaskPlanRow
		var tasks []byte
		if err := rows.Scan(&p.ID, &p.Conversation, &tasks, &p.State, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Tasks = tasks
		out = append(out, p)
	}
	return out, rows.Err()
}

// UpdateTaskPlan moves a plan from state from to state to with its (edited) tasks; false when it was not in from
// (someone else started or dismissed it first).
func (d *DB) UpdateTaskPlan(ctx context.Context, id, from, to string, tasks json.RawMessage, at time.Time) (bool, error) {
	tag, err := d.pool.Exec(ctx, `UPDATE task_plans SET state = $3, tasks = $4, updated_at = $5 WHERE id = $1 AND state = $2`,
		id, from, to, []byte(tasks), at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (d *DB) AppendFeed(ctx context.Context, f FeedRow) error {
	_, err := d.appendDraftFeed(ctx, f, false)
	return err
}

// AppendDraftFeed suppresses another card for a progress generation the user
// already dismissed. Insertion and the dismissal decision share the ref lock.
func (d *DB) AppendDraftFeed(ctx context.Context, f FeedRow) (bool, error) {
	return d.appendDraftFeed(ctx, f, true)
}

func (d *DB) appendDraftFeed(ctx context.Context, f FeedRow, deduplicate bool) (bool, error) {
	body := []byte(f.Body)
	if len(body) == 0 {
		body = []byte("{}")
	}
	// sent_at mirrors body.sent_at (0390) so that Feed finds the sent cards by index.
	inserted := false
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := lockFeedRef(ctx, tx, f.Kind, f.Ref); err != nil {
			return err
		}
		if deduplicate {
			var ignored bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM assistant_feed
				WHERE kind = 'message' AND ref = $1 AND body ? 'dismissal_id'
				AND body->>'todo_id' = $2::jsonb->>'todo_id'
				AND COALESCE(body->>'message_version', '') = COALESCE($2::jsonb->>'message_version', '')
				AND (COALESCE($2::jsonb->>'progress_version', '') = ''
					OR body->>'progress_version' = $2::jsonb->>'progress_version'))`, f.Ref, body).Scan(&ignored); err != nil {
				return err
			}
			if ignored {
				return nil
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO assistant_feed (id, conversation, at, kind, ref, body, sent_at)
			VALUES ($1, $2, $3, $4, $5, $6, ($6::jsonb->>'sent_at')::timestamptz)`,
			f.ID, f.Conversation, f.At, f.Kind, f.Ref, body)
		inserted = err == nil
		return err
	})
	return inserted && err == nil, err
}

// Feed lists a conversation's items after since (zero: from the start), oldest first, at most limit, and the
// message cards put in before since but sent after it (their body changed, not their time). Each half reads its own
// index (assistant_feed_conversation, assistant_feed_sent): one OR over both read every row of the conversation.
func (d *DB) Feed(ctx context.Context, conversation string, since time.Time, limit int) ([]FeedRow, error) {
	q := `SELECT id, conversation, at, kind, ref, body FROM assistant_feed
		WHERE conversation = $1 AND at > $2 ORDER BY at, id LIMIT $3`
	if !since.IsZero() {
		// From the start there is nothing put in before since; the sent half would read every sent card for none.
		q = `(` + q + `)
		UNION ALL
		(SELECT id, conversation, at, kind, ref, body FROM assistant_feed
			WHERE conversation = $1 AND kind = 'message' AND sent_at > $2 AND at <= $2)
		UNION ALL
		(SELECT id, conversation, at, kind, ref, body FROM assistant_feed
			WHERE conversation = $1 AND kind = 'message' AND updated_at > $2 AND at <= $2
			AND (sent_at IS NULL OR sent_at <= $2))
		ORDER BY at, id LIMIT $3`
	}
	rows, err := d.pool.Query(ctx, q, conversation, since, limit)
	if err != nil {
		return nil, err
	}
	return scanFeed(rows)
}

// FeedItem is one item by its id; nil when there is none.
func (d *DB) FeedItem(ctx context.Context, id string) (*FeedRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT id, conversation, at, kind, ref, body FROM assistant_feed WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	out, err := scanFeed(rows)
	if err != nil || len(out) == 0 {
		return nil, err
	}
	return &out[0], nil
}

// markFeedSentSQL is MarkFeedSent's update. Its WHERE matches the partial index assistant_feed_unsent
// (migrations/0392): keep the two in step, or every reply scans the whole feed again.
const markFeedSentSQL = `UPDATE assistant_feed SET body = body || $4::jsonb,
		sent_at = ((body || $4::jsonb)->>'sent_at')::timestamptz
	WHERE kind = $1 AND ref = $2 AND at <= $3 AND NOT body ? 'sent_at'
	RETURNING id, conversation, at, kind, ref, body`

// MarkFeedSent merges patch into the body of the items of kind about ref put in at or before at that have no
// sent_at yet (the column follows the body), and returns them as they are now.
func (d *DB) MarkFeedSent(ctx context.Context, kind, ref string, at time.Time, patch json.RawMessage) ([]FeedRow, error) {
	rows, err := d.pool.Query(ctx, markFeedSentSQL, kind, ref, at, []byte(patch))
	if err != nil {
		return nil, err
	}
	return scanFeed(rows)
}

func scanFeed(rows pgx.Rows) ([]FeedRow, error) {
	defer rows.Close()
	var out []FeedRow
	for rows.Next() {
		var f FeedRow
		var body []byte
		if err := rows.Scan(&f.ID, &f.Conversation, &f.At, &f.Kind, &f.Ref, &body); err != nil {
			return nil, err
		}
		f.Body = body
		out = append(out, f)
	}
	return out, rows.Err()
}
