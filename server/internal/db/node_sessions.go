package db

import (
	"context"
	"encoding/json"
	"time"
)

// ---- node sessions ----

// NodeSession is what a node remembers about a session it started (see internal/node).
// Nodes of #3782 also wrote ultracode observations of sessions the user started, as
// rows without a launcher or directory; newer nodes ignore those rows when loading
// and write no rows for sessions they did not start (#5510).
type NodeSession struct {
	CreationID       string    `json:"creation_id,omitempty"`
	Incarnation      string    `json:"incarnation,omitempty"`
	Model            string    `json:"model,omitempty"`
	ModelFingerprint string    `json:"model_fingerprint,omitempty"`
	ModelEffort      string    `json:"model_effort,omitempty"`
	ConfigDir        string    `json:"config_dir,omitempty"` // the assistant's explicit Claude profile
	Pane             string    `json:"pane,omitempty"`
	Launcher         string    `json:"launcher"`
	Cwd              string    `json:"cwd"`
	StartedAt        time.Time `json:"started_at"`
	Tasks            []string  `json:"tasks,omitempty"` // an assistant conversation's running background tasks
	// The permission mode / effort the app set on an assistant conversation (node/modes.go); kept with the model.
	AppMode   string `json:"app_mode,omitempty"`
	AppEffort string `json:"app_effort,omitempty"`
	// Opaque node-owned process observation, not a preference for later starts.
	UltracodeProcess json.RawMessage `json:"ultracode_process,omitempty"`
	// Guest and GuestKind: a guest conversation's tools and kind (node/assistant_guest.go), kept with the model: every
	// process it runs in, a resume after the node started again too, has only those, and a meeting's room none of
	// the owner's Claude Code setup.
	Guest     []string `json:"guest,omitempty"`
	GuestKind string   `json:"guest_kind,omitempty"`
	// PendingSwitch: a switch to another launcher waiting for the next message (node/launcher_switch.go), opaque here
	// like UltracodeProcess, kept with the model so a node restart neither loses it nor carries it out by itself.
	PendingSwitch json.RawMessage `json:"pending_switch,omitempty"`
}

func (d *DB) PutNodeSession(ctx context.Context, machine, id string, s NodeSession) error {
	// The table needs a timestamp for retention, but a row stored without a start time
	// next to an ultracode observation returns without one: the node tells the rows
	// #3782 wrote for sessions the user started by that (node.observationOnly).
	unknownStart := s.StartedAt.IsZero() && len(s.UltracodeProcess) > 0
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now()
	}
	tasks := s.Tasks
	if tasks == nil {
		tasks = []string{}
	}
	guest, err := json.Marshal(s.Guest) // null for none
	if err != nil {
		return err
	}
	var pendingSwitch json.RawMessage // SQL NULL for none: the key holds a JSON null
	if len(s.PendingSwitch) > 0 && string(s.PendingSwitch) != "null" {
		pendingSwitch = s.PendingSwitch
	}
	_, err = d.pool.Exec(ctx, `WITH saved AS (
		INSERT INTO node_sessions (machine_id, session_id, pane, launcher, cwd, started_at, tasks, updated_at, creation_id, incarnation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $15, $16)
		ON CONFLICT (machine_id, session_id) DO UPDATE SET pane = $3, launcher = $4, cwd = $5, started_at = $6, tasks = $7, updated_at = now(),
		creation_id = CASE WHEN node_sessions.creation_id = '' THEN EXCLUDED.creation_id ELSE node_sessions.creation_id END,
		incarnation = CASE WHEN node_sessions.incarnation = '' THEN EXCLUDED.incarnation ELSE node_sessions.incarnation END
		RETURNING machine_id, session_id
	) INSERT INTO settings (key, value, updated_at)
		SELECT 'node_model.' || machine_id || '.' || session_id,
		jsonb_build_object('model', $8::text, 'model_fingerprint', $9::text, 'model_effort', $10::text,
			'app_mode', $11::text, 'app_effort', $12::text, 'ultracode_process', $13::jsonb,
			'started_at_unknown', $14::bool, 'guest', $17::jsonb, 'guest_kind', $18::text, 'config_dir', $19::text,
			'pending_switch', $20::jsonb), now() FROM saved
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		machine, id, s.Pane, s.Launcher, s.Cwd, s.StartedAt, tasks, s.Model, s.ModelFingerprint, s.ModelEffort, s.AppMode, s.AppEffort,
		s.UltracodeProcess, unknownStart, s.CreationID, s.Incarnation, guest, s.GuestKind, s.ConfigDir, pendingSwitch)
	return err
}

// nodeSessionsSQL reads a machine's sessions from the last 60 days through node_sessions_started (0418), with the
// model settings PutNodeSession keeps beside each one.
// settings holds a node_model row for every session ever started, so the row is fetched per session through
// settings_pkey: a join on the computed key lets the planner hash all of settings instead. The jsonb comes back
// whole because extracting its fields in an outer SELECT repeats the subquery once per field.
const nodeSessionsSQL = `SELECT n.session_id, n.pane, n.launcher, n.cwd, n.started_at, n.tasks, n.creation_id, n.incarnation,
		COALESCE((SELECT s.value FROM settings s WHERE s.key = 'node_model.' || n.machine_id || '.' || n.session_id), '{}')
		FROM node_sessions n
		WHERE n.machine_id = $1 AND (n.started_at > now() - interval '60 days' OR n.creation_id <> '')`

// nodeModel is the node_model.<machine>.<session> settings value PutNodeSession writes.
type nodeModel struct {
	Model            string `json:"model"`
	ModelFingerprint string `json:"model_fingerprint"`
	ModelEffort      string `json:"model_effort"`
	AppMode          string `json:"app_mode"`
	AppEffort        string `json:"app_effort"`
	ConfigDir        string `json:"config_dir"`
	// UltracodeProcess and StartedAtUnknown: the ultracode observation of the session's process, and a row stored
	// without a start time (see PutNodeSession).
	UltracodeProcess json.RawMessage `json:"ultracode_process"`
	StartedAtUnknown bool            `json:"started_at_unknown"`
	Guest            []string        `json:"guest"`
	GuestKind        string          `json:"guest_kind"`
	PendingSwitch    json.RawMessage `json:"pending_switch"`
}

// NodeSessions returns a machine's sessions from the last 60 days.
func (d *DB) NodeSessions(ctx context.Context, machine string) (map[string]NodeSession, error) {
	rows, err := d.pool.Query(ctx, nodeSessionsSQL, machine)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]NodeSession{}
	for rows.Next() {
		var id string
		var s NodeSession
		var m nodeModel
		if err := rows.Scan(&id, &s.Pane, &s.Launcher, &s.Cwd, &s.StartedAt, &s.Tasks, &s.CreationID, &s.Incarnation, &m); err != nil {
			return nil, err
		}
		s.Model, s.ModelFingerprint, s.ModelEffort, s.AppMode, s.AppEffort = m.Model, m.ModelFingerprint, m.ModelEffort, m.AppMode, m.AppEffort
		s.Guest, s.GuestKind = m.Guest, m.GuestKind
		s.ConfigDir = m.ConfigDir
		if len(m.UltracodeProcess) > 0 && string(m.UltracodeProcess) != "null" {
			s.UltracodeProcess = m.UltracodeProcess
		}
		if len(m.PendingSwitch) > 0 && string(m.PendingSwitch) != "null" {
			s.PendingSwitch = m.PendingSwitch
		}
		if m.StartedAtUnknown {
			s.StartedAt = time.Time{}
		}
		out[id] = s
	}
	return out, rows.Err()
}
