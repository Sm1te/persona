package hub

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Deleting a conversation (issue #801) only takes it out of the apps' lists: the hub notes when (settings
// sessions.deleted, by "machine/session id") and leaves out of what it sends the apps an offline session that has
// not been used since. The transcript stays on its computer; once the session is used again (resumed, a new
// message) it is listed again. A start that failed (start_error) goes the same way, right away instead of when the
// node forgets it. A finished one that is still open (idle, seen) goes too. Running sessions and the assistant's
// conversations (they archive) cannot be deleted.

const deletedSessionsKey = "sessions.deleted"

type deletedBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]time.Time // "machine/session id" → when it was deleted, as saved
	// m split at the first "/": machine → session id → when. Every push looks up its machine here instead of going
	// through all the notes, which never expire. The maps are replaced, never changed, so readers keep theirs.
	byMachine map[string]map[string]time.Time
}

// notesByMachine is deletedBook.byMachine for the notes m.
func notesByMachine(m map[string]time.Time) map[string]map[string]time.Time {
	out := map[string]map[string]time.Time{}
	for k, t := range m {
		if machine, sid, ok := strings.Cut(k, "/"); ok {
			if out[machine] == nil {
				out[machine] = map[string]time.Time{}
			}
			out[machine][sid] = t
		}
	}
	return out
}

// deletedLocked reads the notes once; b.mu is held. A failed read leaves them unloaded (nothing is saved over them).
func (h *Hub) deletedLocked(ctx context.Context) bool {
	b := &h.deleted
	if b.loaded {
		return true
	}
	m := map[string]time.Time{}
	if h.store != nil {
		if _, err := h.store.Setting(ctx, deletedSessionsKey, &m); err != nil {
			h.logf("load deleted sessions: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]time.Time{}
	}
	b.m, b.byMachine, b.loaded = m, notesByMachine(m), true
	return true
}

// deletedAt is when each deleted session of machine was deleted, by session id (nil: none). Read only: it is the
// book's own map, which nobody changes.
func (h *Hub) deletedAt(machine string) map[string]time.Time {
	b := &h.deleted
	b.mu.Lock()
	defer b.mu.Unlock()
	if !h.deletedLocked(context.Background()) {
		return nil
	}
	if !strings.Contains(machine, "/") {
		return b.byMachine[machine]
	}
	// hub.json does not forbid a "/" in a machine id; byMachine splits such notes at its first "/", so go through them.
	var out map[string]time.Time
	prefix := machine + "/"
	for k, t := range b.m {
		if sid, ok := strings.CutPrefix(k, prefix); ok {
			if out == nil {
				out = map[string]time.Time{}
			}
			out[sid] = t
		}
	}
	return out
}

// stillDeleted reports whether s stays out of the list given when it was deleted: it has not run or been used since.
// Finished, seen (idle) and interrupted sessions count like offline: the user asked for it to go, and only new work
// brings it back (#6433 follow-up). Status alone is no use: being looked at turns done into idle, a Codex App thread
// reads done / idle for days after its last turn (codex_desktop.go), and a restart can mark an unchanged transcript
// interrupted. What counts is a turn running (working, blocked) or a newer updated_at, the session's last real exchange.
func stillDeleted(s proto.Session, at time.Time) bool {
	finished := s.Status == proto.StatusOffline || s.Status == proto.StatusDone || s.Status == proto.StatusIdle ||
		s.Status == proto.StatusUnknown || s.Status == proto.StatusInterrupted
	return finished && !s.Starting && !s.Assistant && s.PendingInteractions == 0 && !s.UpdatedAt.After(at)
}

// sessionHiddenByDeletion applies the same precedence to normal and archived lists.
func sessionHiddenByDeletion(s proto.Session, deleted map[string]time.Time) bool {
	at, ok := deleted[s.SessionID]
	return ok && (s.ArchiveChangedAt == nil || !s.ArchiveChangedAt.After(at)) && stillDeleted(s, at)
}

// shown is list (a machine's decorated sessions) without the ones the user deleted: what the apps get. When none is
// left out, as on most pushes, it is list itself: a session is ~1.4 KB and a busy machine lists 150, so a copy was
// ~200 KB on every push. list is never changed.
func (h *Hub) shown(machine string, list []proto.Session) []proto.Session {
	del := h.deletedAt(machine)
	hidden := func(s *proto.Session) bool { return s.Archived && !s.Assistant || sessionHiddenByDeletion(*s, del) }
	for i := range list {
		if !hidden(&list[i]) {
			continue
		}
		out := make([]proto.Session, i, len(list)-1)
		copy(out, list[:i])
		for j := i + 1; j < len(list); j++ {
			if !hidden(&list[j]) {
				out = append(out, list[j])
			}
		}
		return out
	}
	return list
}

var (
	errDeleteRunning   = i18n.Err("hub-deleted_sessions.running")
	errDeleteAssistant = i18n.Err("hub-deleted_sessions.assistant")
	errDeleteUnknown   = i18n.Err("hub-deleted_sessions.unknown")
)

// deleteSession is DELETE /api/v1/sessions/{mid}/{sid}: take an offline session out of the apps' lists.
func (h *Hub) deleteSession(w http.ResponseWriter, r *http.Request, m *Machine) {
	if principalOf(r.Context()).Role != roleFull {
		writeErr(w, http.StatusForbidden, errors.New("read-only role cannot delete conversations"))
		return
	}
	id := r.PathValue("sid")
	s, found := h.decoratedSession(m, id)
	switch {
	case !found:
		writeErr(w, http.StatusNotFound, errDeleteUnknown)
		return
	case s.Assistant:
		writeErr(w, http.StatusBadRequest, errDeleteAssistant)
		return
	case s.Starting || s.Status == proto.StatusWorking || s.Status == proto.StatusBlocked:
		writeErr(w, http.StatusConflict, errDeleteRunning)
		return
	}
	if err := h.noteDeleted(r.Context(), m.cfg.ID, id, time.Now().UTC()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	h.sessionsChanged(m) // the apps get the list without it now
	writeJSON(w, map[string]any{"ok": true})
}

// noteDeleted keeps deletion timestamps without expiry. A manager may retain
// an archived/restored snapshot indefinitely, including across its downtime;
// expiring a tombstone would unexpectedly revive that old conversation.
func (h *Hub) noteDeleted(ctx context.Context, machine, id string, at time.Time) error {
	if h.store == nil {
		return errors.New("no database")
	}
	b := &h.deleted
	b.mu.Lock()
	defer b.mu.Unlock()
	if !h.deletedLocked(ctx) {
		return errors.New("deleted sessions could not be read")
	}
	next := make(map[string]time.Time, len(b.m)+1)
	for k, t := range b.m {
		next[k] = t
	}
	key := machine + "/" + id
	next[key] = at
	byMachine := make(map[string]map[string]time.Time, len(b.byMachine)+1)
	maps.Copy(byMachine, b.byMachine)
	mk, sid, _ := strings.Cut(key, "/") // as notesByMachine splits it
	mine := make(map[string]time.Time, len(byMachine[mk])+1)
	maps.Copy(mine, byMachine[mk])
	mine[sid] = at
	byMachine[mk] = mine
	if err := h.store.PutSetting(ctx, deletedSessionsKey, next); err != nil {
		return err
	}
	b.m, b.byMachine = next, byMachine
	return nil
}
