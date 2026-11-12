package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Leader conversations (#3133, docs/assistant/unified-entry.md "Leader conversations"). By default only the main
// conversation supervises (supervise.go). The user can make side conversations leaders (assistant.leaders, off while
// the list is empty), one per repository or long-running project: a task a leader hands out with task_plan has the
// leader as its supervisor, so its supervision messages go to the leader, which reviews it with task_review. What still
// needs the main conversation goes there: a leader's needs_user (escalated, with the user's decision card as ever), a
// failed start (the start remedies are the main conversation's) and anything about a task waiting for the user.
//
// Depth is bounded: an agent handed out as a task's agent (a worker) hands out no Persona tasks itself (task_plan,
// agent_start); it uses its own in-session subagents. Leaders and the main conversation are not limited.

// isLeader: conversation id is one the user made a leader (Settings.Leaders), and not the main conversation.
func (s *Service) isLeader(ctx context.Context, id string) bool {
	if id == "" {
		return false
	}
	cfg := s.settings(ctx)
	return id != cfg.Main && slices.Contains(cfg.Leaders, id)
}

// supervises: conversation id supervises the tasks it hands out with task_plan: the main conversation or a leader.
func (s *Service) supervises(ctx context.Context, id string) bool {
	return s.isMain(ctx, id) || s.isLeader(ctx, id)
}

// defaultPlanSupervisor is who supervises a task conversation hands out with task_plan when conversation does not supervise
// (supervises): that conversation itself, so a side conversation follows up and reviews the work it dispatched. None for
// a task leader's worker (parent set: its leader's agent supervises it), a reporting conversation's task (its notices
// go back to it, #4101) or a plan with no conversation. A planning conversation that is not a live assistant
// conversation cannot be told anything, so its task stays the main conversation's (#4669), and so does what the
// background supervisor hands out: nothing is written on a task for it (supervise_background.go). digestTo sends the
// main conversation's own to the background supervisor while there is one.
func (s *Service) defaultPlanSupervisor(ctx context.Context, conversation, parent string) (string, bool) {
	if parent != "" || conversation == "" {
		return "", false
	}
	if _, reports := s.reporterOf(ctx, conversation); reports {
		return "", false
	}
	if s.supervises(ctx, conversation) {
		return "", false // the main conversation and leaders supervise their own (supervises)
	}
	if c, ok := s.session(ctx, s.settings(ctx).MachineID, conversation); ok && c.Assistant && !s.isBackground(ctx, conversation) {
		return conversation, true
	}
	main := s.settings(ctx).Main
	return main, main != "" && main != conversation
}

// digestTo is the conversation signal g goes to, with cfg the settings in effect: the one it names (To), else its task's
// supervisor when that is a leader or another side conversation, unless the main conversation handles it (its needs_user,
// a start to remedy, a task waiting for the user); else, for a task whose ledger names the main conversation, the
// background supervisor, or the main conversation while there is none (ownSupervisor, supervise_background.go).
func (s *Service) digestTo(cfg Settings, g superviseSignal) string {
	own := s.ownSupervisor(cfg)
	if g.To != "" {
		switch {
		case g.Kind == sigDecide && g.To == cfg.Main:
			return own // a task leader's decision for the conversation above it, whose task is one no leader conversation supervises
		case g.To == cfg.BackgroundSupervisor && g.To != own:
			return cfg.Main // named the background supervisor, which was switched off or moved meanwhile
		}
		return g.To
	}
	switch g.Kind {
	case sigEscalated, sigFailed, sigStarted, sigLeaderStuck:
		return cfg.Main
	}
	t, ok := s.todoNow(g.Todo)
	if !ok || t.Supervision == nil {
		return cfg.Main
	}
	lead := t.Supervision.Supervisor
	// A supervisor is the conversation that handed the task out: the main conversation, a leader, or any other
	// conversation (sendDigest falls back to the main one when that conversation is gone). The main conversation's own
	// go to the background supervisor while there is one. A task leader's agent never reaches this: leaderRoute takes
	// its workers' signals first.
	if lead == "" || lead == cfg.Main {
		return own
	}
	if t.Supervision.state(t) == reviewNeedsUser {
		return cfg.Main
	}
	return lead
}

// escalatedIn: one of the entries is a leader's needs_user.
func escalatedIn(list []prompts.SuperviseEntry) bool {
	return slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigEscalated })
}

// askedIn: one of the entries is a card that came to the supervisor before the user (ask_route.go).
func askedIn(list []prompts.SuperviseEntry) bool {
	return slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigAsk })
}

// escalateToMain tells the main conversation that leader task t needs the user (task_review needs_user from its leader):
// why, and the option the leader recommends. The user's decision card goes out as for any needs_user.
func (s *Service) escalateToMain(t Todo, round int, why, recommend string) {
	s.signal(superviseSignal{Todo: t.ID, Kind: sigEscalated, Key: fmt.Sprintf("needs_user@%d@%s", round, time.Now().UTC().Format(time.RFC3339Nano)),
		Detail: clip(oneLine(why), 200), Ask: clip(oneLine(recommend), 200)})
}

// workerRefusal refuses task_plan and agent_start from an agent handed out as a task's agent: it does the work itself,
// with its own in-session subagents, so tasks do not spawn tasks without end. The main conversation and leaders hand
// out freely.
func (s *Service) workerRefusal(ctx context.Context, machine, self string) error {
	if self == "" || s.supervises(ctx, self) {
		return nil
	}
	if s.devs != nil {
		machine = s.devs.Canonical(machine) // as the task's agents are kept
	}
	t, ok := s.taskOf(ctx, machine, self)
	if !ok {
		return nil
	}
	return fmt.Errorf("you are the agent of task %s (%s): an agent handed out as a task does not hand out Persona tasks "+
		"(task_plan, agent_start). Do the work here, with your own in-session subagents if it splits; if it needs another "+
		"task, say so to your supervisor (report_to_supervisor)", t.ID, t.Title)
}

// ---- assistant.leaders ----

func (s *Service) getLeaders(ctx context.Context) (string, error) {
	list := s.settings(ctx).Leaders
	if len(list) == 0 {
		return "[] (none: only the main conversation supervises)", nil
	}
	raw, _ := json.Marshal(list)
	return string(raw), nil
}

// setLeaders takes the whole list of leader conversations: each an assistant conversation of the assistant's machine
// other than the main one.
func (s *Service) setLeaders(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var in []string
	if err := json.Unmarshal(v, &in); err != nil {
		return nil, errors.New("value must be a list of conversation ids ([] for none)")
	}
	cfg := s.settings(ctx)
	list := []string{}
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(list, id) {
			continue
		}
		if id == cfg.Main {
			return nil, errors.New("the main conversation supervises already: a leader is a side conversation")
		}
		if id == cfg.BackgroundSupervisor {
			return nil, errors.New("the background supervisor is built in and follows up the tasks no leader supervises: a leader is another side conversation")
		}
		if c, ok := s.session(ctx, cfg.MachineID, id); !ok || !c.Assistant {
			return nil, fmt.Errorf("%s is not a Persona conversation on the assistant's machine (conversations_list has their ids)", id)
		}
		list = append(list, id)
	}
	if len(list) > leadersMost {
		return nil, fmt.Errorf("at most %d leader conversations", leadersMost)
	}
	before := cfg.Leaders
	if slices.Equal(list, before) || len(list) == 0 && len(before) == 0 { // nil and [] are both none
		return &ownChange{summary: "assistant.leaders did not change"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	shown := func(l i18n.Lang, ids []string) string { return clipped(l, strings.Join(ids, ", ")) }
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_leaders", i18n.Args{"before": shown(l, before), "after": shown(l, list)})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.Leaders = list; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return fmt.Sprintf("assistant.leaders is %q: tasks these conversations hand out with task_plan from now on are theirs to "+
				"supervise (tasks handed out before keep their supervisor).", list), nil
		}}, nil
}

// ---- a cancelled task's agents ----

// cancelStopWait is how long a cancelled task's closed agent may still read busy before it is left unarchived;
// cancelStopPoll how often it is looked at meanwhile.
var cancelStopWait, cancelStopPoll = 30 * time.Second, 500 * time.Millisecond

// stopTaskAgents stops what task t's agents still ask of the user once t is cancelled: each agent on it is closed on
// its computer (session.close: its tab and process go, its transcript stays, and where its engine can resume it a
// message does), its
// cards still waiting end, and it is archived (reversible, nothing deleted). It runs in the background: closing can
// take a while and the review is answered first.
func (s *Service) stopTaskAgents(ctx context.Context, t Todo) {
	var agents []TodoAgent
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil && a.SessionID != "" {
			agents = append(agents, a)
		}
	}
	if len(agents) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		for _, a := range agents {
			s.stopCancelledAgent(ctx, t.ID, a.MachineID, a.SessionID)
		}
	}()
}

// stopCancelledAgent stops, ends the cards of and archives one agent of cancelled task id; one that works for another
// open task now, an assistant conversation (the user's to stop) or a session its machine does not list is left alone.
// The agent quits and its terminal stays (session.stop, #4754); a session with no terminal (another tool's, an engine
// that runs agents itself) is closed as before.
func (s *Service) stopCancelledAgent(ctx context.Context, id, machine, session string) {
	s.todos.mu.RLock()
	other, linked := s.todos.activeBySession[[2]string{machine, session}]
	s.todos.mu.RUnlock()
	if linked && other != id {
		s.logf("task %s cancelled: agent %s/%s works for %s now, left alone", id, machine, short(session), other)
		return
	}
	c, err := s.agentArchiveTarget(ctx, machine, session) // refuses an assistant conversation and an unknown session
	if err != nil {
		s.logf("task %s cancelled: agent %s/%s left alone: %v", id, machine, short(session), err)
		return
	}
	machine = c.MachineID
	if c.Status != proto.StatusOffline {
		cctx, cancel := context.WithTimeout(ctx, agentArchiveCloseTimeout)
		err := s.call(cctx, machine, deviceSessionStop, map[string]any{"session_id": session}, nil)
		if re := (*rpc.Error)(nil); errors.As(err, &re) && re.Code == "no_terminal" {
			err = s.call(cctx, machine, "session.close", map[string]any{"session_id": session}, nil)
		}
		if err != nil {
			s.logf("task %s cancelled: stop agent %s/%s: %v", id, machine, short(session), err)
		}
		cancel()
	}
	s.endSessionCards(machine, session)
	// The archive waits for the list to show it closed: a row still busy there (working, a card waiting) would bring
	// it straight back (archivedSessions).
	var row proto.Session
	for deadline := time.Now().Add(cancelStopWait); ; time.Sleep(cancelStopPoll) {
		r, err := s.agentArchiveTarget(ctx, machine, session)
		if err != nil {
			s.logf("task %s cancelled: archive agent %s/%s: %v", id, machine, short(session), err)
			return
		}
		if !agentArchiveBusy(r) {
			row = r
			break
		}
		if time.Now().After(deadline) {
			s.logf("task %s cancelled: agent %s/%s still busy, left unarchived", id, machine, short(session))
			return
		}
	}
	if err := s.noteAgentArchive(ctx, row, true); err != nil {
		s.logf("task %s cancelled: archive agent %s/%s: %v", id, machine, short(session), err)
		return
	}
	if err := s.noteAgentArchiveClosed(ctx, row, time.Now().UTC()); err != nil { // what it writes as it goes is not new work
		s.logf("task %s cancelled: archive agent %s/%s: %v", id, machine, short(session), err)
	}
	s.publishList(row.MachineID)
	s.logf("task %s cancelled: closed and archived agent %s/%s", id, machine, short(session))
}

// endSessionCards ends the cards machine/session still waits on (cancelled in the history), as when its node goes away.
func (s *Service) endSessionCards(machine, session string) {
	s.fleet.mu.Lock()
	var ids []string
	for id, p := range s.fleet.interactions {
		if p.ix.SessionID == session && s.sameMachine(p.ix.MachineID, machine) && !p.claimed {
			p.claimed = true
			ids = append(ids, id)
		}
	}
	s.fleet.mu.Unlock()
	s.feishuDetach(ids)
	for _, id := range ids {
		s.recordEnd(id, "cancelled", nil)
		s.finishInteraction(id)
	}
}
