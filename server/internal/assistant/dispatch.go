package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Cross-machine dispatch (docs/plans/plan-agent-dispatch.md): an agent on one computer hands a subtask to a new agent
// on another connected computer (dispatch_start) and reads its state and final reply back (dispatch_read). The
// manager starts the session as it starts any agent, so the hub stays the only path between computers. Off unless
// manager.json agents.dispatch.enabled is true; while off the tools are not listed and their calls are refused.

// dispatchToolNames are the agents' dispatch tools (prompts/mcp/dispatch). Like agentToolNames they act for the
// agent calling them: the node names its session (node.callerTools).
var dispatchToolNames = []string{"dispatch_start", "dispatch_read"}

func isDispatchTool(name string) bool { return slices.Contains(dispatchToolNames, name) }

// dispatchTools are the dispatch tools when the switch is on, none otherwise.
func (s *Service) dispatchTools(ctx context.Context) []proto.Tool {
	if !s.hello(ctx).Agents.Dispatch.On() {
		return nil
	}
	return toolsNamed(dispatchToolNames)
}

const (
	// dispatchSettingKey holds the dispatched tasks ([]dispatchTask): who may read which session survives a restart.
	dispatchSettingKey = "agents.dispatch.tasks"
	dispatchKeep       = 7 * 24 * time.Hour
	dispatchKeepMost   = 200
	// dispatchStartGrace: a task this young counts as working although its session does not say so yet.
	dispatchStartGrace = 2 * time.Minute
	// dispatchWaitMost bounds one dispatch_read wait: a tool call held longer risks its caller's own timeout.
	dispatchWaitMost = 50 * time.Second
)

// dispatchPoll is how often a waiting dispatch_read looks at the task's session.
var dispatchPoll = 2 * time.Second

// dispatchTask is one subtask an agent handed to another computer.
type dispatchTask struct {
	FromMachine string    `json:"from_machine"`
	FromSession string    `json:"from_session"`
	Machine     string    `json:"machine"`
	Session     string    `json:"session"`
	Cwd         string    `json:"cwd,omitempty"`
	Title       string    `json:"title,omitempty"`
	At          time.Time `json:"at"`
}

// dispatchBook is the dispatched tasks, read from the settings on first use and saved on every change.
type dispatchBook struct {
	mu     sync.Mutex
	loaded bool
	tasks  []dispatchTask
}

// dispatched are the tasks kept (a copy). A read that fails is tried again by the next call; meanwhile the tasks of
// this process are still known.
func (s *Service) dispatched(ctx context.Context) []dispatchTask {
	b := &s.dispatch
	b.mu.Lock()
	defer b.mu.Unlock()
	s.loadDispatched(ctx)
	return slices.Clone(b.tasks)
}

// loadDispatched reads the saved tasks once, before those this process added; the book's lock is held.
func (s *Service) loadDispatched(ctx context.Context) {
	b := &s.dispatch
	st := s.store()
	if b.loaded || st == nil {
		return
	}
	var saved []dispatchTask
	if _, err := st.Setting(ctx, dispatchSettingKey, &saved); err != nil {
		s.logf("dispatch: the saved tasks could not be read: %v", err)
		return
	}
	b.tasks, b.loaded = append(saved, b.tasks...), true
}

// addDispatched records t, dropping tasks older than dispatchKeep and all but the newest dispatchKeepMost.
func (s *Service) addDispatched(ctx context.Context, t dispatchTask) {
	b := &s.dispatch
	b.mu.Lock()
	defer b.mu.Unlock()
	s.loadDispatched(ctx)
	kept := b.tasks[:0]
	for _, old := range b.tasks {
		if t.At.Sub(old.At) < dispatchKeep {
			kept = append(kept, old)
		}
	}
	kept = append(kept, t)
	if n := len(kept) - dispatchKeepMost; n > 0 {
		kept = kept[n:]
	}
	b.tasks = kept
	st := s.store()
	if st == nil || !b.loaded {
		return // no database, or its tasks are not read yet: saving now would drop them
	}
	if err := st.PutSetting(ctx, dispatchSettingKey, b.tasks); err != nil {
		s.logf("dispatch: the task %s on %s was not saved: %v", short(t.Session), t.Machine, err)
	}
}

// canonical is machine's id as the manager keeps it.
func (s *Service) canonical(machine string) string {
	if s.devs != nil {
		return s.devs.Canonical(machine)
	}
	return machine
}

// dispatchTool runs one of the dispatch tools for agent session on machine.
func (s *Service) dispatchTool(ctx context.Context, machine, session, name string, args json.RawMessage) proto.ToolResult {
	if !s.hello(ctx).Agents.Dispatch.On() {
		return proto.TextResult("handing tasks to other computers is switched off (agents.dispatch.enabled in manager.json); do the work here", true)
	}
	if session == "" {
		return proto.TextResult("Persona cannot tell which agent is calling: this computer's node may be older than the hub (update it), "+
			"or this agent does not run in a pane Persona follows", true)
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	machine = s.canonical(machine)
	var res string
	var err error
	if name == "dispatch_start" {
		res, err = s.dispatchStart(ctx, machine, session, args, time.Now())
	} else {
		res, err = s.dispatchRead(ctx, machine, session, args)
	}
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(res, false)
}

// dispatchStart starts a new agent on another computer for agent machine/session and records it as that agent's.
func (s *Service) dispatchStart(ctx context.Context, machine, session string, args json.RawMessage, now time.Time) (string, error) {
	var a struct {
		Machine  string `json:"machine"`
		Cwd      string `json:"cwd"`
		Prompt   string `json:"prompt"`
		Launcher string `json:"launcher"`
		Worktree struct {
			Branch string `json:"branch"`
		} `json:"worktree"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	a.Machine, a.Cwd, a.Prompt = strings.TrimSpace(a.Machine), strings.TrimSpace(a.Cwd), strings.TrimSpace(a.Prompt)
	if a.Machine == "" || a.Cwd == "" || a.Prompt == "" {
		return "", errors.New("machine, cwd and prompt are required; nothing was started")
	}
	tasks := s.dispatched(ctx)
	// One level: a dispatched task does its work where it is, so tasks do not spawn tasks across computers without end.
	for _, t := range tasks {
		if t.Machine == machine && t.Session == session {
			return "", errors.New("you are a task another agent handed to this computer: do the work here (your own in-session " +
				"subagents are fine), a dispatched task does not dispatch; nothing was started")
		}
	}
	am, ok := s.machineByName(ctx, a.Machine)
	if !ok {
		return "", fmt.Errorf("unknown computer %q (computers: %s); nothing was started", a.Machine, s.machineIDs(ctx))
	}
	switch {
	case s.canonical(am.ID) == machine:
		return "", errors.New("that is the computer you run on: use your own in-session subagents for work here; nothing was started")
	case s.isWorkMachine(am.ID):
		return "", fmt.Errorf("%s is a work computer: tasks are not handed to it; nothing was started", am.Name)
	case !am.Online && !am.Reachable:
		return "", fmt.Errorf("%s is offline; nothing was started", am.Name)
	}
	limit, active := s.hello(ctx).Agents.Dispatch.Limit(), 0
	for _, t := range tasks {
		if t.FromMachine == machine && t.FromSession == session && s.dispatchWorking(ctx, t, now) {
			active++
		}
	}
	if active >= limit {
		return "", fmt.Errorf("%d tasks you dispatched are still working (the limit is %d): read them with dispatch_read and "+
			"wait for one to stop; nothing was started", active, limit)
	}

	// The start is detached from the calling turn, as agent_start's is: a computer waking up can take minutes.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentStartTimeout)
	defer cancel()
	a.Cwd = proto.ExpandHome(a.Cwd, am.Home)
	from := prompts.DispatchTask{Prompt: a.Prompt}
	if fm, ok := s.machineByName(ctx, machine); ok {
		from.FromMachine = fm.Name
	}
	if c, ok := s.session(ctx, machine, session); ok {
		from.From = agentLabel(c)
	}
	title := s.tabName(sctx, a.Prompt)
	params := map[string]any{"cwd": a.Cwd, "launcher": strings.TrimSpace(a.Launcher), "model": "", "effort": "",
		"tab_name": title, "prompt": s.withCloseoutBrief(sctx, prompts.RenderDispatchTask(from), false)}
	if err := s.applyModel(sctx, "agent.new", params); err != nil {
		return "", fmt.Errorf("%w; nothing was started", err)
	}
	note := ""
	if branch := strings.TrimSpace(a.Worktree.Branch); branch != "" {
		wt, err := s.newWorktree(sctx, am.ID, a.Cwd, branch)
		if err != nil {
			return "", fmt.Errorf("the worktree on branch %s was not made, nothing was started: %w", branch, err)
		}
		a.Cwd = wt.Path
		params["cwd"], params["workspace_id"] = wt.Path, wt.WorkspaceID
		note = fmt.Sprintf(" It runs in a new worktree on branch %s.", wt.Branch)
	}
	var res struct {
		Session proto.Session `json:"session"`
	}
	s.setPlanThen(ctx, am.ID, params)
	if err := s.call(sctx, am.ID, DeviceSessionStart, params, &res); err != nil {
		switch {
		case errors.Is(err, apierr.ErrOffline):
			return "", fmt.Errorf("%s is offline; nothing was started", am.Name)
		case startResultUnknown(err):
			return "", fmt.Errorf("%s did not answer the start in time: an agent may or may not have started there, and it is "+
				"not recorded as yours. Tell the user instead of starting it again", am.Name)
		}
		return "", fmt.Errorf("the start on %s failed: %w", am.Name, err)
	}
	s.addDispatched(ctx, dispatchTask{FromMachine: machine, FromSession: session, Machine: s.canonical(am.ID),
		Session: res.Session.SessionID, Cwd: a.Cwd, Title: title, At: now})
	s.logf("dispatch %s/%s -> %s/%s in %s", machine, short(session), am.ID, short(res.Session.SessionID), a.Cwd)
	child := res.Session
	child.Cwd = cmp.Or(child.Cwd, a.Cwd)
	s.superviseDispatched(ctx, am, child, a.Prompt, cmp.Or(from.From, short(session)), cmp.Or(from.FromMachine, machine))
	return fmt.Sprintf("Started on %s in %s: session %s.%s It works on its own and the user can watch it in the apps. "+
		"Read its state and final reply with dispatch_read (session_id %s, wait_seconds up to 50).",
		am.Name, a.Cwd, res.Session.SessionID, note, res.Session.SessionID), nil
}

// dispatchState is where task t stands: working, stopped (its last reply is its result), or gone (its computer
// is offline or the session is no longer listed).
func (s *Service) dispatchState(ctx context.Context, t dispatchTask, now time.Time) string {
	c, ok := s.session(ctx, t.Machine, t.Session)
	switch {
	case !ok && now.Sub(t.At) < dispatchStartGrace:
		return "working"
	case !ok:
		return "gone"
	case c.Status == proto.StatusWorking:
		return "working"
	case c.Status == proto.StatusBlocked:
		return "waiting for the user"
	case c.Status == proto.StatusInterrupted || c.Status == proto.StatusOffline:
		return "interrupted"
	}
	return "stopped"
}

func (s *Service) dispatchWorking(ctx context.Context, t dispatchTask, now time.Time) bool {
	return s.dispatchState(ctx, t, now) == "working"
}

// dispatchRead answers agent machine/session about the tasks it dispatched: all of them and the computers there
// are, or one task's state and last turn, after waiting for it to stop when asked.
func (s *Service) dispatchRead(ctx context.Context, machine, session string, args json.RawMessage) (string, error) {
	var a struct {
		SessionID   string `json:"session_id"`
		WaitSeconds int    `json:"wait_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	var mine []dispatchTask
	for _, t := range s.dispatched(ctx) {
		if t.FromMachine == machine && t.FromSession == session {
			mine = append(mine, t)
		}
	}
	id := strings.TrimSpace(a.SessionID)
	if id == "" {
		return s.dispatchOverview(ctx, machine, mine), nil
	}
	var found []dispatchTask
	for _, t := range mine {
		if t.Session == id || len(id) >= 8 && strings.HasPrefix(t.Session, id) {
			found = append(found, t)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("no task %s among those you dispatched (dispatch_read with no arguments lists them)", id)
	}
	t := found[0]
	wait := min(time.Duration(max(a.WaitSeconds, 0))*time.Second, dispatchWaitMost)
	state := s.dispatchState(ctx, t, time.Now())
	for deadline := time.Now().Add(wait); state == "working" && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(dispatchPoll):
		}
		state = s.dispatchState(ctx, t, time.Now())
	}
	name := t.Machine
	if m, ok := s.machineByName(ctx, t.Machine); ok {
		name = m.Name
	}
	head := fmt.Sprintf("Task %s on %s (%s): %s.", t.Session, name, t.Cwd, state)
	switch state {
	case "working":
		head += " Its turn is not over: what follows is not its result yet. Call again with wait_seconds."
	case "stopped":
		head += " Its last reply below is its result."
	case "waiting for the user":
		head += " It asked the user something and goes on once they answer in the apps."
	case "gone":
		return head + " Its computer is offline or the session is no longer listed; nothing can be read now.", nil
	}
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, t.Machine, DeviceSessionItems, map[string]any{"session_id": t.Session, "turns": 1}, &res); err != nil {
		return "", fmt.Errorf("%s Its transcript could not be read: %w", head, err)
	}
	return transcriptText(head, sessionLink(s.webBase(ctx), t.Machine, t.Session, false), res.Items), nil
}

// dispatchOverview lists the computers a task can go to from machine, and the calling agent's tasks.
func (s *Service) dispatchOverview(ctx context.Context, machine string, mine []dispatchTask) string {
	var b strings.Builder
	b.WriteString("Computers a task can go to:\n")
	n := 0
	for _, m := range s.devices(ctx) {
		if s.canonical(m.ID) == machine || s.isWorkMachine(m.ID) {
			continue
		}
		state := "offline"
		if m.Online || m.Reachable {
			state = "online"
		}
		home := m.Home
		if home == "" {
			home = "home not reported"
		}
		fmt.Fprintf(&b, "- %s (id %s): %s, %s\n", m.Name, m.ID, state, home)
		n++
	}
	if n == 0 {
		b.WriteString("- none: no other computer is connected\n")
	}
	b.WriteString("\nTasks you dispatched:\n")
	if len(mine) == 0 {
		b.WriteString("- none\n")
	}
	now := time.Now()
	for _, t := range mine {
		fmt.Fprintf(&b, "- %s on %s (%s): %s", t.Session, t.Machine, t.Cwd, s.dispatchState(ctx, t, now))
		if t.Title != "" {
			fmt.Fprintf(&b, " · %s", t.Title)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// superviseDispatched makes dispatched session c on am a supervised task of the main conversation, as agent_supervise
// makes one (adoptAgent): a todo whose notes say which agent handed it out, its card in the main conversation's feed,
// and the supervision messages and task_review of any task the main conversation hands out. The calling agent still
// reads the result with dispatch_read. Without a main conversation, or when the todo cannot be made, the task runs
// untracked as before; the start itself has succeeded either way.
func (s *Service) superviseDispatched(ctx context.Context, am Device, c proto.Session, prompt, from, fromMachine string) {
	main := s.settings(ctx).Main
	if main == "" || c.SessionID == "" {
		return
	}
	notes := i18n.T(s.lang(ctx), "assistant-tasks.dispatch_notes", i18n.Args{"from": from,
		"from_machine": fromMachine, "machine": am.Name, "cwd": c.Cwd}) + "\n\n" + prompt
	todo, made, err := s.adoptAgent(context.WithoutCancel(ctx), main, am, c, "", prompt, notes, nil, 0, false)
	if err != nil {
		s.logf("dispatch %s on %s: task: %v", short(c.SessionID), am.ID, err)
		return
	}
	if made {
		s.taskCard(ctx, main, todo.ID, "")
	}
}
