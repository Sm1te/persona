package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrDecisionNotFound = errors.New("decision not found")
	// ErrDecisionSupersede: the decision named as superseded is not an earlier one of the same todo.
	ErrDecisionSupersede = errors.New("a decision can only supersede an earlier decision of the same todo")
)

// Decision sources: written in the todo by hand, from the user's answer to an agent's question card, what a task
// came to when it was finished (its outcome, decisions and pitfalls, #2820), or noted by an agent on it as it worked
// (todo_decision_add, #5279: the agent's call, not the user's), or said by the user in the chat thread the todo is bound
// to (#5279: kept as said; never posted back into that thread).
const (
	DecisionManual  = "manual"
	DecisionCard    = "card"
	DecisionOutcome = "outcome"
	DecisionAgent   = "agent"
	DecisionThread  = "thread"
	// DecisionReported: the main conversation told the owner the outcome of a concern (#3561); Choice is what it told
	// them. Written only by ReportConcern, which closes the concern with it.
	DecisionReported = "reported"
)

// ErrNotConcern: the todo is not a concern (only a concern's outcome is reported).
var ErrNotConcern = errors.New("the todo is not a concern")

// TodoDecisionRow is one call the user made on a todo (#1618). SupersedesID names the earlier decision this one
// replaces; SupersededBy is filled in when listing: the newest decision that supersedes this one, empty while it is
// in effect.
type TodoDecisionRow struct {
	ID            string    `json:"id"`
	TodoID        string    `json:"todo_id"`
	Question      string    `json:"question"`
	Choice        string    `json:"choice"`
	Reason        string    `json:"reason"`
	Source        string    `json:"source"`
	SourceMachine string    `json:"source_machine"`
	SourceSession string    `json:"source_session"`
	SourceMessage string    `json:"source_message"`
	SupersedesID  string    `json:"supersedes_id"`
	SupersededBy  string    `json:"superseded_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// ListTodoDecisions returns a todo's decisions, oldest first: only those in effect, or all of them with all.
func (d *DB) ListTodoDecisions(ctx context.Context, todoID string, all bool) ([]TodoDecisionRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT d.id, d.todo_id, d.question, d.choice, d.reason, d.source, d.source_machine,
			d.source_session, d.source_message, COALESCE(d.supersedes_id, ''),
			COALESCE((SELECT n.id FROM todo_decisions n WHERE n.supersedes_id = d.id ORDER BY n.created_at DESC, n.id DESC LIMIT 1), ''),
			d.created_at
		FROM todo_decisions d WHERE d.todo_id = $1
		ORDER BY d.created_at, d.id`, todoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TodoDecisionRow{}
	for rows.Next() {
		var r TodoDecisionRow
		if err := rows.Scan(&r.ID, &r.TodoID, &r.Question, &r.Choice, &r.Reason, &r.Source, &r.SourceMachine,
			&r.SourceSession, &r.SourceMessage, &r.SupersedesID, &r.SupersededBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		if all || r.SupersededBy == "" {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// TodoDecisionsSince returns the decisions in effect of every todo written at or after since, oldest first: what the
// owner answered lately, counted when a standing rule is suggested (assistant/rule_suggest.go, #4929).
func (d *DB) TodoDecisionsSince(ctx context.Context, since time.Time) ([]TodoDecisionRow, error) {
	rows, err := d.pool.Query(ctx, `SELECT d.id, d.todo_id, d.question, d.choice, d.reason, d.source, d.source_machine,
			d.source_session, d.source_message, COALESCE(d.supersedes_id, ''), d.created_at
		FROM todo_decisions d
		WHERE d.created_at >= $1 AND NOT EXISTS (SELECT 1 FROM todo_decisions n WHERE n.supersedes_id = d.id)
		ORDER BY d.created_at, d.id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TodoDecisionRow{}
	for rows.Next() {
		var r TodoDecisionRow
		if err := rows.Scan(&r.ID, &r.TodoID, &r.Question, &r.Choice, &r.Reason, &r.Source, &r.SourceMachine,
			&r.SourceSession, &r.SourceMessage, &r.SupersedesID, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsertTodoDecision writes a decision. ErrTodoNotFound when the todo is gone, ErrDecisionSupersede when SupersedesID
// is not an earlier decision of the same todo. A card's answer already written (same todo, interaction and question)
// is not written again: inserted is false then.
func (d *DB) InsertTodoDecision(ctx context.Context, r TodoDecisionRow) (inserted bool, err error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	if r.Source == DecisionReported {
		return false, errors.New("a report is written by ReportConcern")
	}
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var one int
		err := tx.QueryRow(ctx, `SELECT 1 FROM todos WHERE id = $1 FOR UPDATE`, r.TodoID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTodoNotFound
		}
		if err != nil {
			return err
		}
		if r.SupersedesID != "" {
			if err := checkSupersede(ctx, tx, r.TodoID, r.SupersedesID, r.ID, r.CreatedAt); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO todo_decisions (id, todo_id, question, choice, reason, source, source_machine,
				source_session, source_message, supersedes_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), $11)
			ON CONFLICT (todo_id, source_message, question) WHERE source_message <> '' DO NOTHING`,
			r.ID, r.TodoID, r.Question, r.Choice, r.Reason, r.Source, r.SourceMachine, r.SourceSession, r.SourceMessage,
			r.SupersedesID, r.CreatedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		inserted = true
		_, err = tx.Exec(ctx, `UPDATE todos SET updated_at = $2 WHERE id = $1`, r.TodoID, r.CreatedAt)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" { // the todo was deleted between the check and the insert
		return false, ErrTodoNotFound
	}
	return inserted, err
}

// ReportConcern writes r, the report of a concern's outcome to the owner (Source DecisionReported), and closes the
// concern with it (DoneByReported, reason the report) in one transaction: a concern is never closed without its report,
// and a report is never written on one left open. A concern already closed keeps its close and gains the report
// (closed is false then). ErrTodoNotFound when it is gone, ErrNotConcern when it is not a concern.
func (d *DB) ReportConcern(ctx context.Context, r TodoDecisionRow, reason string) (row *TodoRow, closed bool, err error) {
	if r.Source != DecisionReported {
		return nil, false, errors.New("a concern's report has source reported")
	}
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Release()
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var cur TodoRow
		err := tx.QueryRow(ctx, `SELECT `+todoColumns+` FROM todos WHERE id = $1 FOR UPDATE`, r.TodoID).Scan(todoDest(&cur)...)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTodoNotFound
		}
		if err != nil {
			return err
		}
		if cur.Kind != KindConcern {
			return ErrNotConcern
		}
		if _, err := tx.Exec(ctx, `INSERT INTO todo_decisions (id, todo_id, question, choice, reason, source, source_machine,
				source_session, source_message, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			r.ID, r.TodoID, r.Question, r.Choice, r.Reason, r.Source, r.SourceMachine, r.SourceSession, r.SourceMessage,
			r.CreatedAt); err != nil {
			return err
		}
		at := r.CreatedAt
		if cur.DoneAt == nil {
			closed = true
			cur.DoneAt, cur.DoneBy, cur.DoneReason, cur.StartError = &at, DoneByReported, reason, ""
		}
		cur.UpdatedAt = at
		if _, err := tx.Exec(ctx, `UPDATE todos SET done_at = $2, done_by = $3, done_reason = $4, start_error = $5, updated_at = $6
			WHERE id = $1`, cur.ID, cur.DoneAt, cur.DoneBy, cur.DoneReason, cur.StartError, cur.UpdatedAt); err != nil {
			return err
		}
		row = &cur
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return row, closed, nil
}

// SetTodoDecisionSupersedes marks decision id of todoID as superseding the earlier decision supersedesID ("" clears
// it), and returns it as listed. ErrDecisionNotFound when id is not the todo's, ErrDecisionSupersede as for
// InsertTodoDecision.
func (d *DB) SetTodoDecisionSupersedes(ctx context.Context, todoID, id, supersedesID string, at time.Time) (*TodoDecisionRow, error) {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		var createdAt time.Time
		err := tx.QueryRow(ctx, `SELECT created_at FROM todo_decisions WHERE id = $1 AND todo_id = $2 FOR UPDATE`, id, todoID).Scan(&createdAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDecisionNotFound
		}
		if err != nil {
			return err
		}
		if supersedesID != "" {
			if supersedesID == id {
				return ErrDecisionSupersede
			}
			if err := checkSupersede(ctx, tx, todoID, supersedesID, id, createdAt); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE todo_decisions SET supersedes_id = NULLIF($2, '') WHERE id = $1`, id, supersedesID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE todos SET updated_at = $2 WHERE id = $1`, todoID, at)
		return err
	})
	if err != nil {
		return nil, err
	}
	all, err := d.ListTodoDecisions(ctx, todoID, true)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, ErrDecisionNotFound
}

// checkSupersede: old is a decision of todoID that comes before the decision (id, at) in the list order (created_at,
// then id), so supersession never goes in a circle.
func checkSupersede(ctx context.Context, tx pgx.Tx, todoID, old, id string, at time.Time) error {
	var oldAt time.Time
	err := tx.QueryRow(ctx, `SELECT created_at FROM todo_decisions WHERE id = $1 AND todo_id = $2`, old, todoID).Scan(&oldAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDecisionSupersede
	}
	if err != nil {
		return err
	}
	if oldAt.After(at) || oldAt.Equal(at) && old >= id {
		return ErrDecisionSupersede
	}
	return nil
}
