package assistant

import (
	"cmp"
	"context"
	"errors"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A task whose agents have all stopped (read-only: node stopped.go, #4754) goes on with a new agent when the user asks
// it to: a message on its card (route) or its Retry. The new agent starts on the computer, in the directory and with
// the launcher of the task's latest agent; its first message is the user's, after who worked on the task before and
// that agent's last reply (prompts agents/task_successor); it is linked to the task as the first was, and the card's
// feed line says a new agent took it. Persona's own follow-ups (relayed answers, rework, digests) are not delivered to
// a stopped agent and start nothing: errTaskAgentsStopped.

// errTaskAgentsStopped: every agent of the task has stopped; nothing was sent.
var errTaskAgentsStopped = errors.New("every agent of this task has stopped and is read-only: nothing was sent. " +
	"A message on the task's card starts a new agent for it; the user can also resume one from its terminal page")

// liveTaskAgents are the task's agents that have not stopped; last is the latest of all when none is left.
func liveTaskAgents(agents []proto.Session) (live []proto.Session, last proto.Session) {
	for _, c := range agents {
		if !stoppedAgent(c) {
			live = append(live, c)
		} else if last.SessionID == "" || c.UpdatedAt.After(last.UpdatedAt) {
			last = c
		}
	}
	return live, last
}

// startTaskSuccessor starts a new agent for task t after its stopped agent last, with the user's text as its first
// message, and links it to t.
func (s *Service) startTaskSuccessor(ctx context.Context, t Todo, last proto.Session, text string) (proto.Session, error) {
	m, ok := s.device(ctx, last.MachineID)
	if !ok || !m.Reachable {
		return proto.Session{}, machineOffline(cmp.Or(m.Name, last.MachineID))
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentStartTimeout)
	defer cancel()
	prompt := prompts.RenderTaskSuccessor(prompts.TaskSuccessor{Previous: agentLabel(last), PreviousID: last.SessionID,
		LastReply: clip(last.LastText, 2000), Text: text})
	params := map[string]any{"cwd": last.Cwd, "launcher": last.Launcher, "model": "", "effort": "", "tab_name": t.Title,
		"prompt": prompt}
	if last.PermissionMode != "" {
		params["permission_mode"] = last.PermissionMode
	}
	if err := s.applyModel(sctx, "agent.new", params); err != nil {
		return proto.Session{}, err
	}
	var res struct {
		Session proto.Session `json:"session"`
	}
	if err := s.call(sctx, m.ID, DeviceSessionStart, params, &res); err != nil {
		return proto.Session{}, err
	}
	c := res.Session
	c.MachineID = cmp.Or(c.MachineID, m.ID)
	link := s.linkTodoAgent
	if t.DoneAt != nil {
		link = s.linkTodoAgentOfDoneTask
	}
	if err := link(sctx, t.ID, m.ID, c.SessionID, t.Title, cmp.Or(c.Cwd, last.Cwd), cmp.Or(c.Agent, last.Agent)); err != nil {
		s.logf("task %s: new agent %s/%s not linked: %v", t.ID, m.ID, short(c.SessionID), err)
	}
	// The task's ledger goes on with the new agent: an open task without a supervisor gets one, as any Agent Persona
	// starts does (#4669); a done one is left as it is.
	s.ensureSupervisor(sctx, t.ID)
	s.logf("task %s: its agents had stopped; started %s/%s for the user's message", t.ID, m.ID, short(c.SessionID))
	return c, nil
}
