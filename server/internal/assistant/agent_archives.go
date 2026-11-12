package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const agentArchivesKey = "sessions.archived"
const agentArchiveTimeout = 3 * time.Second

type agentArchive struct {
	Session   proto.Session `json:"session"`
	Archived  bool          `json:"archived"`
	ChangedAt time.Time     `json:"changed_at"`
	// ClosedAt: "Archive and close" closed its agent then (agent_archive_close.go). Whatever the agent wrote while it
	// was being closed is not new activity: only activity after this moment restores the session.
	ClosedAt time.Time `json:"closed_at,omitempty"`
	// wrote is the archive's write that last changed the record (agentArchiveBook.writes), kept in memory only: 0 for
	// a record as loaded, written before anything this process read.
	wrote uint64
}

// activeAfter is the moment after which source activity restores the archived session.
func (rec agentArchive) activeAfter() time.Time {
	if rec.ClosedAt.After(rec.Session.UpdatedAt) {
		return rec.ClosedAt
	}
	return rec.Session.UpdatedAt
}

// A just-closed confirmation can remain in a node's cached list until its next push.
// New work or a newer transcript still restores it; the old blocked snapshot cannot undo the close.
//
// mark is the archive's write count when s was read from the node's list (archiveMark). A row read before the
// record last changed says nothing about activity after that: a reader that read the list while the agent still
// worked or waited on the user, then waited for the archive's lock while the agent was closed and archived, would
// otherwise restore it for good (#4372). The next list read weighs it again.
func (rec agentArchive) active(s proto.Session, mark uint64) bool {
	if rec.wrote > mark {
		return false
	}
	return s.UpdatedAt.After(rec.activeAfter()) || agentArchiveBusy(s) && (rec.ClosedAt.IsZero() || s.Status != proto.StatusBlocked)
}

type agentArchiveBook struct {
	once     sync.Once
	gate     chan struct{}
	snapshot atomic.Pointer[map[string]agentArchive]
	loaded   bool
	records  map[string]agentArchive
	// Pending automatic restores survive subsequent idle snapshots if a database
	// write fails. They are retried; they are never reported as persisted success.
	pending map[string]bool
	// writes counts the record writes (saveAgentArchivesLocked), which stamp each record they change.
	writes atomic.Uint64
}

// archiveMark is taken before reading a list the archive is applied to (archivedSessions): the records written
// after it are newer than anything the list shows.
func (svc *Service) archiveMark() uint64 { return svc.agentArchives.writes.Load() }

// Archive mutations serialize through a cancellable gate. Readers of published
// visibility metadata do not wait for a database operation holding the gate.
func (b *agentArchiveBook) lock(ctx context.Context) error {
	b.once.Do(func() { b.gate = make(chan struct{}, 1) })
	select {
	case b.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-b.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *agentArchiveBook) unlock() { <-b.gate }

func (svc *Service) loadAgentArchivesLocked(ctx context.Context) error {
	b := &svc.agentArchives
	if b.loaded {
		return nil
	}
	store := svc.archiveStore()
	if store == nil {
		return errors.New("no database")
	}
	records := map[string]agentArchive{}
	if _, err := store.Setting(ctx, agentArchivesKey, &records); err != nil {
		return err
	}
	if records == nil {
		records = map[string]agentArchive{}
	}
	b.records, b.pending, b.loaded = records, map[string]bool{}, true
	b.snapshot.Store(&records)
	return nil
}

func (svc *Service) saveAgentArchivesLocked(ctx context.Context, records map[string]agentArchive) error {
	store := svc.archiveStore()
	if store == nil {
		return errors.New("no database")
	}
	n := svc.agentArchives.writes.Add(1)
	for key, rec := range records {
		if old, ok := svc.agentArchives.records[key]; !ok || old.Archived != rec.Archived ||
			!old.ChangedAt.Equal(rec.ChangedAt) || !old.ClosedAt.Equal(rec.ClosedAt) {
			rec.wrote = n
			records[key] = rec
		}
	}
	if err := store.PutSetting(ctx, agentArchivesKey, records); err != nil {
		return err
	}
	svc.agentArchives.records = records
	svc.agentArchives.snapshot.Store(&records)
	return nil
}

func agentArchiveBusy(s proto.Session) bool {
	for _, child := range s.Subagents {
		if child.Running {
			return true
		}
	}
	for _, workflow := range s.Workflows {
		if workflow.Running {
			return true
		}
	}
	// Awaiting the user's reply (a finished turn that ends with a question, #5096) is not busy: the agent is idle,
	// and the user archiving it is the reply. "Delete chat" took those too, and archive replaces it.
	return s.Starting || s.Status == proto.StatusWorking || s.Status == proto.StatusBlocked ||
		s.State == proto.StateWorking || s.State == proto.StateNeedsYou || s.BackgroundWorking ||
		s.BackgroundWaiting || s.PendingInteractions > 0 || len(s.Background) > 0
}

// archiveSnapshot contains identity and display metadata, not stale runtime
// controls. The source transcript remains on its computer and is read by ID.
func archiveSnapshot(s proto.Session) proto.Session {
	preview := []rune(s.LastText)
	if len(preview) > 180 {
		preview = preview[:180]
	}
	return proto.Session{
		MachineID: s.MachineID, SessionID: s.SessionID, Title: s.Title,
		Cwd: s.Cwd, WorkspaceCwd: s.WorkspaceCwd, RepoRoot: s.RepoRoot, Worktree: s.Worktree, Branch: s.Branch,
		Agent: s.Agent, AgentLabel: s.AgentLabel, AgentName: s.AgentName,
		SourceKind: s.SourceKind, SourceThreadID: s.SourceThreadID, SourceInstanceID: s.SourceInstanceID,
		ParentThreadID: s.ParentThreadID, ParentSessionID: s.ParentSessionID, RootSessionID: s.RootSessionID, IsSubagent: s.IsSubagent,
		UpdatedAt: s.UpdatedAt, LastText: string(preview),
		Status: proto.StatusOffline, State: proto.StateOffline,
		Capabilities: &proto.SessionCapabilities{Read: true},
	}
}

// archivedSessions overlays the manager's durable visibility on the current
// inventory and retains old archived/restored rows beyond its seven-day window.
// Observed new activity permanently restores a row, including a busy-to-idle
// transition with no timestamp change. Only that transition writes the database.
// mark is archiveMark taken before list was read: a record changed after it is not
// weighed against the list (agentArchive.active).
func (svc *Service) archivedSessions(ctx context.Context, machine string, list []proto.Session, mark uint64) ([]proto.Session, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		if records := b.snapshot.Load(); records != nil {
			list = overlayAgentArchives(machine, list, *records, nil, mark)
		}
		return list, false, err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return list, false, err
	}
	if len(b.records) == 0 {
		return list, false, nil
	}
	var next map[string]agentArchive
	for _, s := range list {
		key := runtimeListID(machine, s.SessionID)
		rec, ok := b.records[key]
		if !ok || !rec.Archived {
			continue
		}
		if rec.active(s, mark) {
			b.pending[key] = true
		}
		if b.pending[key] {
			rec.Archived, rec.ChangedAt = false, time.Now().UTC()
			rec.Session = archiveSnapshot(s)
			rec.Session.MachineID = machine
			if next == nil {
				next = maps.Clone(b.records)
			}
			next[key] = rec
		}
	}
	var saveErr error
	if next != nil {
		saveErr = svc.saveAgentArchivesLocked(ctx, next)
		if saveErr == nil {
			for key := range b.pending {
				if rec, ok := next[key]; ok && !rec.Archived {
					delete(b.pending, key)
				}
			}
		}
	}
	return overlayAgentArchives(machine, list, b.records, b.pending, mark), next != nil && saveErr == nil, saveErr
}

// archivedInventory is machine's whole list with the archive applied (archivedSessions), its children folded under
// their parents among the rows left listed (foldInventory).
func (svc *Service) archivedInventory(ctx context.Context, machine string) ([]proto.Session, bool, error) {
	mark := svc.archiveMark()
	list, changed, err := svc.archivedSessions(ctx, machine, svc.decoratedUnarchived(machine), mark)
	svc.foldInventory(machine, list)
	return list, changed, err
}

func overlayAgentArchives(machine string, list []proto.Session, records map[string]agentArchive, pending map[string]bool, mark uint64) []proto.Session {
	seen := make(map[string]bool, len(list))
	for i := range list {
		key := runtimeListID(machine, list[i].SessionID)
		seen[key] = true
		if rec, ok := records[key]; ok && !list[i].Assistant {
			// A storage timeout must neither reveal unrelated archived rows nor hide
			// new work. The durable restore remains pending until its write succeeds.
			active := rec.active(list[i], mark)
			list[i].Archived = rec.Archived && !pending[key] && !active
			list[i].ArchiveChangedAt = archiveChangedAt(rec.ChangedAt)
		}
	}
	keys := make([]string, 0, len(records))
	for key, rec := range records {
		if !seen[key] && rec.Session.MachineID == machine {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys) // stable inventories avoid needless delta/list revisions
	for _, key := range keys {
		rec := records[key]
		s := rec.Session
		s.Archived = rec.Archived
		s.ArchiveChangedAt = archiveChangedAt(rec.ChangedAt)
		list = append(list, s)
	}
	return list
}

func (svc *Service) archiveStore() Store {
	if svc.store == nil {
		return nil
	}
	return svc.store()
}

func runtimeListID(machine, session string) string {
	raw, _ := json.Marshal([2]string{machine, session})
	return string(raw)
}

// decorated applies manager-owned archive visibility to its final agent states.
func (svc *Service) decorated(machine string) []proto.Session {
	list, _, err := svc.archivedInventory(context.Background(), machine)
	if err != nil {
		svc.logf("load agent archives: %v", err)
	}
	return list
}

func (svc *Service) listArchivedSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), agentArchiveTimeout)
	defer cancel()
	mid := r.URL.Query().Get("machine_id")
	if mid != "" {
		if m, ok := svc.fleetMachine(mid); !ok {
			apierr.Write(w, http.StatusNotFound, i18n.Err("devices-api.unknown_machine"))
			return
		} else {
			mid = m.ID
		}
	}
	out := []proto.Session{}
	for _, m := range svc.fleetMachines() {
		if mid != "" && mid != m.ID {
			continue
		}
		list, changed, err := svc.archivedInventory(ctx, m.ID)
		if err != nil {
			apierr.Write(w, http.StatusInternalServerError, i18n.Err("hub-agent_archives.storage"))
			return
		}
		if changed {
			svc.publishList(m.ID)
		}
		for _, row := range list {
			if row.Archived && !row.Assistant {
				row.Manager = svc.id
				row.NativeArchive = svc.nativeArchiveView(m.ID, row.SessionID)
				out = append(out, row)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return runtimeListID(out[i].MachineID, out[i].SessionID) < runtimeListID(out[j].MachineID, out[j].SessionID)
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	apierr.WriteJSON(w, map[string]any{"sessions": out})
}

// agentArchiveError is a refusal of an archive operation: the HTTP status and code the apps get, and its message.
type agentArchiveError struct {
	status int
	code   string
	err    error
}

func (e *agentArchiveError) Error() string { return e.err.Error() }
func (e *agentArchiveError) Unwrap() error { return e.err }

func archiveRefusal(status int, code string, err error) error {
	return &agentArchiveError{status: status, code: code, err: err}
}

// writeAgentArchiveError answers an archive operation's error: its refusal, else a storage failure.
func (svc *Service) writeAgentArchiveError(w http.ResponseWriter, err error) {
	var refusal *agentArchiveError
	if errors.As(err, &refusal) {
		apierr.WriteCode(w, refusal.status, refusal.code, refusal.err)
		return
	}
	svc.logf("agent archive: %v", err)
	apierr.Write(w, http.StatusInternalServerError, i18n.Err("hub-agent_archives.storage"))
}

// agentArchiveTarget is machine mid's session sid as the archive sees it (the overlay applied), with the canonical
// machine id; a refusal when there is no such machine or session, or it is an assistant conversation (those archive
// in their own API). Every other agent, whatever its runtime, archives the same way.
func (svc *Service) agentArchiveTarget(ctx context.Context, mid, sid string) (proto.Session, error) {
	m, ok := svc.fleetMachine(mid)
	if !ok {
		return proto.Session{}, archiveRefusal(http.StatusNotFound, "", i18n.Err("devices-api.unknown_machine"))
	}
	list, _, err := svc.archivedInventory(ctx, m.ID)
	if err != nil {
		return proto.Session{}, err
	}
	for _, row := range list {
		if row.SessionID != sid {
			continue
		}
		if row.Assistant {
			return proto.Session{}, archiveRefusal(http.StatusBadRequest, "", i18n.Err("hub-agent_archives.unsupported"))
		}
		row.MachineID = m.ID
		return row, nil
	}
	return proto.Session{}, archiveRefusal(http.StatusNotFound, "", i18n.Err("hub-deleted_sessions.unknown"))
}

// setAgentArchive archives (or restores) machine mid's session sid: the one way every caller changes an agent's
// archive state (the apps' routes, "Archive and close", the assistant's tools). A busy session is refused (409).
// The record is the manager's; the session's source stays as it is.
func (svc *Service) setAgentArchive(ctx context.Context, mid, sid string, archived bool) (proto.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	row, err := svc.agentArchiveTarget(ctx, mid, sid)
	if err != nil {
		return row, err
	}
	if archived && agentArchiveBusy(row) {
		return row, archiveRefusal(http.StatusConflict, "agent_busy", i18n.Err("hub-agent_archives.running"))
	}
	if err := svc.noteAgentArchive(ctx, row, archived); err != nil {
		return row, err
	}
	svc.publishList(row.MachineID)
	return row, nil
}

func (svc *Service) setAgentSessionArchive(w http.ResponseWriter, r *http.Request, mid string, archived bool) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, i18n.Err("hub-agent_archives.forbidden"))
		return
	}
	if archived && r.URL.Query().Get("force") == "1" {
		if err := svc.forceArchiveConfirmation(r.Context(), mid, r.PathValue("sid")); err != nil {
			svc.writeAgentArchiveError(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true, "archived": true, "close": map[string]any{"closed": true, "outcome": archiveCloseClosed}})
		return
	}
	if archived && r.URL.Query().Get("close") == "1" {
		svc.archiveAndCloseHTTP(w, r, mid)
		return
	}
	row, err := svc.setAgentArchive(r.Context(), mid, r.PathValue("sid"), archived)
	if err != nil {
		svc.writeAgentArchiveError(w, err)
		return
	}
	apierr.WriteJSON(w, map[string]any{"ok": true, "native": svc.nativeArchiveAwaited(r.Context(), row.MachineID, row.SessionID)}) // agent_archive_native.go
}

func (svc *Service) noteAgentArchive(ctx context.Context, s proto.Session, archived bool) error {
	ctx, cancel := context.WithTimeout(ctx, agentArchiveTimeout)
	defer cancel()
	b := &svc.agentArchives
	if err := b.lock(ctx); err != nil {
		return err
	}
	defer b.unlock()
	if err := svc.loadAgentArchivesLocked(ctx); err != nil {
		return err
	}
	key := runtimeListID(s.MachineID, s.SessionID)
	next := maps.Clone(b.records)
	next[key] = agentArchive{Session: archiveSnapshot(s), Archived: archived, ChangedAt: time.Now().UTC()}
	if err := svc.saveAgentArchivesLocked(ctx, next); err != nil {
		return err
	}
	delete(b.pending, key)
	svc.nativeArchiveWanted(s, archived) // the tool's own archive follows (agent_archive_native.go)
	return nil
}

func (svc *Service) agentArchiveRoutes() {
	svc.handle("GET /api/v1/sessions/archived", svc.listArchivedSessions)
	svc.handle("POST /api/v1/sessions/{mid}/{sid}/archive", func(w http.ResponseWriter, r *http.Request) {
		svc.setAgentSessionArchive(w, r, r.PathValue("mid"), true)
	})
	svc.handle("POST /api/v1/sessions/{mid}/{sid}/unarchive", func(w http.ResponseWriter, r *http.Request) {
		svc.setAgentSessionArchive(w, r, r.PathValue("mid"), false)
	})
}

// archiveChangedAt is a record's change time as a session carries it (proto.Session.ArchiveChangedAt): nil for none.
func archiveChangedAt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
