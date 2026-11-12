package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Archiving closed sessions by themselves (docs/assistant/todos.md "Closed sessions archive themselves", #3618). A
// finished task's agent is closed a few minutes after the task (tasks_close.go) and an idle one after half a day
// (tasks_idle.go), but the closed session stays in the lists as stopped, and the few agents that matter get lost among
// them. A closed session that has been idle for archive_closed_days, and is on no to-do or only on done ones, is
// archived with the archive the apps use (agent_archives.go): reversible, nothing is deleted, it is listed under
// Archived, and a message to it restores and resumes it as ever.
//
// Never archived (autoArchivable): a session still in a host (a pane or an engine host, whatever state the host
// reads), or working, starting or with background work; one
// with a card waiting or queued input; one the manager is to wake or continue (session_work); one linked to an open
// to-do; an assistant conversation; a review's reviewer; a subagent's row; a session another tool owns (its archive
// would also move it in that tool); with no to-do, one whose last words ask the user something.

const (
	// defaultArchiveClosedDays is archive_closed_days when unset; 0 turns the pass off.
	defaultArchiveClosedDays = 3
	maxArchiveClosedDays     = 3650
)

var (
	// autoArchiveEvery is how often closed sessions are looked at (the loop's own tick is taskCloseEvery).
	autoArchiveEvery = 5 * time.Minute
	// autoArchiveBatch bounds one pass: each archive is a write of the archive records and a list refresh.
	autoArchiveBatch = 20
)

// archiveClosedAfter is how long a closed session must be idle before it is archived; 0 when the pass is off.
func (s *Service) archiveClosedAfter(ctx context.Context) time.Duration {
	d := defaultArchiveClosedDays
	if v := s.settings(ctx).ArchiveClosedDays; v != nil && *v >= 0 && *v <= maxArchiveClosedDays {
		d = *v
	}
	return time.Duration(d) * 24 * time.Hour
}

// sessionLinks is what the to-dos say of the sessions: the ones an open to-do counts on, when the latest to-do of each
// was done, and the reviews' reviewers.
type sessionLinks struct {
	open      map[[2]string]bool
	done      map[[2]string]time.Time
	reviewers map[[2]string]bool
}

// link is what the to-dos say of machine's session.
func (l sessionLinks) link(machine, session string) (open, onTodo, reviewer bool, lastDone time.Time) {
	key := [2]string{machine, session}
	lastDone, onTodo = l.done[key]
	return l.open[key], onTodo || l.open[key], l.reviewers[key], lastDone
}

// sessionLinks reads the to-dos' links. A link that was released or dismissed no longer holds its session for an open
// to-do; for a done one it still dates the session's last use.
func (s *Service) sessionLinks() sessionLinks {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	l := sessionLinks{open: map[[2]string]bool{}, done: map[[2]string]time.Time{}, reviewers: map[[2]string]bool{}}
	for _, t := range s.todos.todos {
		for _, a := range t.Agents {
			key := [2]string{a.MachineID, a.SessionID}
			if a.Source == sourceReview {
				l.reviewers[key] = true
			}
			switch {
			case t.DoneAt == nil:
				if a.ReleasedAt == nil && a.DismissedAt == nil {
					l.open[key] = true
				}
			case t.DoneAt.After(l.done[key]):
				l.done[key] = *t.DoneAt
			}
		}
	}
	for key := range s.todos.activeBySession { // the open to-do each agent works for, as the rest of the manager reads it
		l.open[key] = true
	}
	return l
}

// sessionClosed: c's tab is gone: it runs in no host (no pane, no engine host), is not working or starting, and only
// its transcript is listed.
//
// The status does not say so. A closed session often reads `interrupted`, not `offline`: when a node restarts, it
// marks the offline rows Persona had started in a pane before that boot as interrupted, so the apps offer "Continue"
// (node interrupted.go), and the mark stays until a message resumes the session. The unified state has no case for
// that status, so those rows read `unknown` (agentState, fleet.go). A row can also keep the `idle` or `unknown` its
// host last said after the pane went. So `unknown` means "the host cannot tell" only while there is a host or a pane.
//
// A session another tool lists (SourceKind) never has a pane of ours: it is closed only when that tool says offline.
func sessionClosed(c proto.Session) bool {
	if c.PaneID != "" || c.Host != "" || c.Starting {
		return false
	}
	if c.Status == proto.StatusWorking || c.Status == proto.StatusBlocked || c.State == proto.StateWorking {
		return false
	}
	return c.SourceKind == "" || c.Status == proto.StatusOffline
}

// wakePending: the manager is to act on c by itself (a wake to check, a "Continue"), or c waits on something the
// manager follows for it (the user's answer, another agent, CI, a moment's model failure).
func wakePending(c proto.Session) bool {
	return c.Work != nil && (c.Work.Phase == proto.WorkBlocked || c.Work.WakeAt != nil)
}

// autoArchivable says whether c may be archived by itself now: closed, idle for `idle` since its last step, since its
// latest to-do was done (lastDone) and since its archive state last changed (a session the user restored is not taken
// straight back), and none of the cases above.
func autoArchivable(c proto.Session, now time.Time, idle time.Duration, onOpenTodo, onTodo, reviewer bool, lastDone time.Time) bool {
	switch {
	case idle <= 0 || c.Archived || onOpenTodo || reviewer:
		return false
	case c.Assistant || c.IsSubagent || c.SourceKind != "":
		return false
	case !sessionClosed(c) || c.UpdatedAt.IsZero():
		return false
	case agentCloseBusy(c):
		return false // working, starting, a card waiting, queued input, background work
	case wakePending(c):
		return false
	case !onTodo && c.AwaitsReply:
		return false // nobody answered what it asked, and no to-do says the matter is over
	}
	last := c.UpdatedAt
	if lastDone.After(last) {
		last = lastDone
	}
	if at := c.ArchiveChangedAt; at != nil && at.After(last) {
		last = *at
	}
	return now.Sub(last) >= idle
}

// autoArchiveCandidates are the sessions on reachable machines that may be archived now (autoArchivable), by machine
// and session id.
func (s *Service) autoArchiveCandidates(ctx context.Context, now time.Time) [][2]string {
	idle := s.archiveClosedAfter(ctx)
	if idle <= 0 || !s.todosLoaded() { // without the to-dos nothing says which sessions open ones count on
		return nil
	}
	links := s.sessionLinks()
	var out [][2]string
	for _, d := range s.devices(ctx) {
		if !d.Reachable {
			continue
		}
		list, err := s.sessions(ctx, d.ID)
		if err != nil {
			continue
		}
		for _, c := range list {
			open, onTodo, reviewer, lastDone := links.link(d.ID, c.SessionID)
			if autoArchivable(c, now, idle, open, onTodo, reviewer, lastDone) {
				out = append(out, [2]string{d.ID, c.SessionID})
			}
		}
	}
	return out
}

// archiveClosedSessions archives the closed sessions that are due, at most autoArchiveBatch of them, and answers how
// many it took. Each is looked at again as the archive sees it just before, with the to-dos read anew: a message, a
// card or a to-do that came meanwhile keeps it.
func (s *Service) archiveClosedSessions(ctx context.Context, now time.Time) int {
	idle := s.archiveClosedAfter(ctx)
	took := 0
	changed := map[string]bool{}
	for _, key := range s.autoArchiveCandidates(ctx, now) {
		if took >= autoArchiveBatch || ctx.Err() != nil {
			break
		}
		row, err := s.agentArchiveTarget(ctx, key[0], key[1])
		if err != nil {
			continue // gone, or an assistant conversation after all
		}
		open, onTodo, reviewer, lastDone := s.sessionLinks().link(key[0], key[1])
		if !autoArchivable(row, now, idle, open, onTodo, reviewer, lastDone) {
			continue
		}
		if err := s.noteAgentArchive(ctx, row, true); err != nil {
			s.logf("auto archive %s/%s: %v", row.MachineID, short(row.SessionID), err)
			break // the records could not be written: the next pass tries again
		}
		took++
		changed[row.MachineID] = true
		s.logf("auto archive: archived closed session %s/%s, idle since %s (restores on the next message)",
			row.MachineID, short(row.SessionID), row.UpdatedAt.UTC().Format(time.RFC3339))
	}
	for machine := range changed {
		s.publishList(machine)
	}
	return took
}
