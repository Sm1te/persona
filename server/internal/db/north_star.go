package db

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// NorthStarCard is one card of the interruption readout (assistant/north_star.go): a card raised, told to the user or
// answered by a standing authorization in the window. The readout classifies it; the database only lists it.
type NorthStarCard struct {
	ID               string
	MachineID        string
	SessionID        string
	TodoID           string
	Kind             string
	ToolName         string
	Cwd              string
	SessionTitle     string
	CreatedAt        time.Time
	UserNotifiedAt   *time.Time
	AutoAuthorizedAt *time.Time
}

// NorthStarCards lists the cards created, told to the user or automatically authorized in [since, until]. A database
// from before migration 0504 has no markers: every card comes back without them (the readout calls those unknown),
// so the readout can also look at a database another build owns without migrating it.
func (d *DB) NorthStarCards(ctx context.Context, since, until time.Time) ([]NorthStarCard, error) {
	var marked bool
	if err := d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'interactions' AND column_name = 'user_notified_at')`).Scan(&marked); err != nil {
		return nil, err
	}
	q := `SELECT id, machine_id, session_id, '', kind, payload, created_at, NULL::timestamptz, NULL::timestamptz
		FROM interactions WHERE created_at >= $1 AND created_at <= $2 ORDER BY created_at, id`
	if marked {
		q = `SELECT id, machine_id, session_id, todo_id, kind, payload, created_at, user_notified_at, auto_authorized_at
		FROM interactions WHERE (created_at >= $1 AND created_at <= $2)
		OR (user_notified_at >= $1 AND user_notified_at <= $2)
		OR (auto_authorized_at >= $1 AND auto_authorized_at <= $2)
		ORDER BY created_at, id`
	}
	rows, err := d.pool.Query(ctx, q, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NorthStarCard{}
	for rows.Next() {
		var c NorthStarCard
		var payload []byte
		if err := rows.Scan(&c.ID, &c.MachineID, &c.SessionID, &c.TodoID, &c.Kind, &payload, &c.CreatedAt, &c.UserNotifiedAt, &c.AutoAuthorizedAt); err != nil {
			return nil, err
		}
		var p struct {
			ToolName     string `json:"tool_name"`
			Cwd          string `json:"cwd"`
			SessionTitle string `json:"session_title"`
		}
		_ = json.Unmarshal(payload, &p)
		c.ToolName, c.Cwd, c.SessionTitle = p.ToolName, p.Cwd, p.SessionTitle
		out = append(out, c)
	}
	return out, rows.Err()
}

// WorkEventsBetween are the events of kinds in [since, until], oldest first.
func (d *DB) WorkEventsBetween(ctx context.Context, since, until time.Time, kinds []string) ([]WorkEvent, error) {
	rows, err := d.pool.Query(ctx, `SELECT at, machine_id, session_id, kind, detail FROM session_work_events
		WHERE at >= $1 AND at <= $2 AND kind = ANY($3) ORDER BY at, id`, since, until, kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkEvent{}
	for rows.Next() {
		var e WorkEvent
		var b []byte
		if err := rows.Scan(&e.At, &e.MachineID, &e.SessionID, &e.Kind, &b); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(b, &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClientMessage is an app's message to a session as the hub's client_ops kept it (op id, state, answer): the
// availability baseline from before the manager recorded them itself (assistant north-star command).
type ClientMessage struct {
	MachineID, SessionID string
	State                string // done, failed, unknown or pending
	Code, Detail         string // the answer's error, when it has one
	At                   time.Time
}

// ClientMessages are the hub's recorded message sends created in [since, until].
func (d *DB) ClientMessages(ctx context.Context, since, until time.Time) ([]ClientMessage, error) {
	rows, err := d.pool.Query(ctx, `SELECT target->>'path', state, result, created_at FROM client_ops
		WHERE kind = 'message' AND created_at >= $1 AND created_at <= $2 ORDER BY created_at`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientMessage{}
	for rows.Next() {
		var path string
		var result []byte
		var m ClientMessage
		if err := rows.Scan(&path, &m.State, &result, &m.At); err != nil {
			return nil, err
		}
		// /api/v1/sessions/{machine}/{session}/messages
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) != 6 || parts[3] == "" || parts[4] == "" || parts[5] != "messages" {
			continue
		}
		m.MachineID, m.SessionID = parts[3], parts[4]
		var r struct {
			Body []byte `json:"body"`
		}
		if json.Unmarshal(result, &r) == nil && len(r.Body) > 0 {
			var e struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
				Error  string `json:"error"`
			}
			if json.Unmarshal(r.Body, &e) == nil {
				m.Code, m.Detail = e.Code, e.Detail
				if m.Detail == "" {
					m.Detail = e.Error
				}
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
