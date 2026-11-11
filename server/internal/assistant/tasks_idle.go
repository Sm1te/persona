package assistant

import (
	"context"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Stopping agents that have been idle for a long time (docs/assistant/todos.md "Idle Agents stop too"). The pass in
// tasks_close.go only knows agents of finished tasks; agents started directly (agent_start), agents of tasks that
// stall or are never accepted, and stopped agents a message resumed (agent_send, the chat) would run for good.
// An agent Persona started that has been still for idle_close_hours with nothing going on (retirable) is stopped the
// same way (session.stop): its terminal, transcript and launch state stay and the next message resumes it.
//
// Left running besides what retirable keeps: an agent with a wake due (Work.WakeAt) or blocked on anything (Work.Phase
// blocked: the user, another agent, CI or a deploy, a transient error), and one of an open task unless that task has
// itself been quiet for idleStalledFactor times the idle time.

const (
	// defaultIdleCloseHours is idle_close_hours when unset; 0 turns the pass off.
	defaultIdleCloseHours = 12
	maxIdleCloseHours     = 24 * 365
	// idleStalledFactor: an open task untouched for this many idle times no longer protects its agents.
	idleStalledFactor = 3
)

// idleCloseAfter is how long an agent must be idle before it is closed; 0 when the pass is off.
func (s *Service) idleCloseAfter(ctx context.Context) time.Duration {
	h := defaultIdleCloseHours
	if v := s.settings(ctx).IdleCloseHours; v != nil && *v >= 0 && *v <= maxIdleCloseHours {
		h = *v
	}
	return time.Duration(h) * time.Hour
}

// idleSeen: (machine, session) → when the pass first saw it running in a pane. A resumed agent's last activity can be
// old until its first step lands, so the idle time counts from the later of the two.
type idleSeen map[[2]string]time.Time

// openTaskOf reports whether key works for an open task, and when that task was last touched.
func (s *Service) openTaskOf(key [2]string) (open bool, touched time.Time) {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	id, ok := s.todos.activeBySession[key]
	if !ok {
		return false, time.Time{}
	}
	t := s.todos.todos[id]
	if t == nil {
		return true, time.Time{}
	}
	return true, t.UpdatedAt
}

// idleCloseable says whether c, first seen running at since, may be closed for being idle.
func idleCloseable(c proto.Session, since, now time.Time, idle time.Duration, openTask bool, taskTouched time.Time) bool {
	if c.Work != nil && (c.Work.Phase == proto.WorkBlocked || c.Work.WakeAt != nil) {
		return false
	}
	if openTask && now.Sub(taskTouched) < idle*idleStalledFactor {
		return false
	}
	return retirableAfter(c, since, now, idle)
}

// closeIdleAgents retires the agents on reachable machines that have been idle for too long (idleCloseable).
func (s *Service) closeIdleAgents(ctx context.Context, refused taskCloseRefused, seen idleSeen) {
	idle := s.idleCloseAfter(ctx)
	if idle <= 0 || !s.todosLoaded() {
		return
	}
	now := time.Now()
	running := map[[2]string]bool{}
	for _, d := range s.devices(ctx) {
		if !d.Reachable {
			for k := range seen { // an unreachable machine keeps what was seen
				if k[0] == d.ID {
					running[k] = true
				}
			}
			continue
		}
		list, err := s.sessions(ctx, d.ID)
		if err != nil {
			for k := range seen {
				if k[0] == d.ID {
					running[k] = true
				}
			}
			continue
		}
		for _, c := range list {
			if c.Status == proto.StatusOffline || c.PaneID == "" {
				continue
			}
			key := [2]string{d.ID, c.SessionID}
			running[key] = true
			since, ok := seen[key]
			if !ok {
				seen[key] = now
				since = now
			}
			open, touched := s.openTaskOf(key)
			if !idleCloseable(c, since, now, idle, open, touched) {
				continue
			}
			if r, ok := refused[key]; ok && r.updated.Equal(c.UpdatedAt) && now.Sub(r.at) < taskCloseRetry {
				continue
			}
			s.retireAgent(ctx, refused, "idle", d.ID, c, now)
		}
	}
	for k := range seen {
		if !running[k] {
			delete(seen, k)
		}
	}
}
