package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ---- starting and dismissing a plan ----

var errPlanNotOpen = errors.New("this plan was already started or dismissed")

// planInvalid is a start the user's edits make impossible as they are (a model that is no model id): 400.
type planInvalid struct{ error }

// startPlan opens a todo for each task of the open plan id (edits, when given, replace the tasks' fields the user can
// change) and starts their agents in the background; remember lets this kind start without asking next time.
func (s *Service) startPlan(ctx context.Context, id string, edits []PlanTask, remember bool) (*TaskPlan, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return nil, err
	}
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.State != planOpen {
		return nil, errPlanNotOpen
	}
	for i := range p.Tasks {
		if i < len(edits) {
			applyPlanEdit(&p.Tasks[i], edits[i])
		}
		p.Tasks[i].normalizeReadonlyPermission()
		if err := p.Tasks[i].normalizeMachinePolicy(); err != nil {
			return nil, planInvalid{err}
		}
		if err := checkModelID(p.Tasks[i].Model); err != nil { // typed on the card (#1644): refused before anything starts
			return nil, planInvalid{err}
		}
		p.Tasks[i].Permission = strings.TrimSpace(p.Tasks[i].Permission)
		if m, ok := s.machineByName(ctx, p.Tasks[i].Machine); ok && p.Tasks[i].Role == roleLeader && s.isWorkMachine(m.ID) {
			return nil, planInvalid{fmt.Errorf("task %d: role \"leader\" cannot run on %s: a work-trust computer gives its agents only "+
				"the reporting tools, so it could not supervise workers. Put the task leader on a computer you own", i+1, m.Name)}
		}
		if !s.placeTask(ctx, &p.Tasks[i]) {
			return nil, i18n.Err("assistant-tasks.task_no_machine", i18n.Args{"task": p.Tasks[i].Title, "machine": strconv.Quote(p.Tasks[i].Machine)})
		}
		if err := s.resolveTaskWorkspace(ctx, &p.Tasks[i]); err != nil {
			return nil, planInvalid{err}
		}
	}
	p.State = planStarted
	for i := range p.Tasks {
		p.Tasks[i].OwnerStarted = ownerStarts(ctx)
	}
	ok, err := s.movePlan(ctx, p, planOpen)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errPlanNotOpen
	}
	for i := range p.Tasks {
		t := &p.Tasks[i]
		var todo Todo
		if t.ReuseTodo != "" {
			// The todo is already there with its own title, notes and task card in the conversation's feed (a Lark topic
			// opened it): the task runs on it, and nothing is made or shown twice.
			todo, err = s.takeTodoForTask(ctx, p.ID, p.Conversation, t.ReuseTodo)
			if err != nil {
				t.Error = err.Error()
				continue
			}
			if t.Project != "" && todo.Project != t.Project {
				if moved, err := s.editTodo(ctx, todo.ID, db.TodoEdit{Project: &t.Project}); err != nil {
					s.logf("task %s: project: %v", todo.ID, err)
				} else {
					todo = moved
				}
			}
		} else {
			title, retitle := s.taskTitle(ctx, t.Title, t.Prompt) // the assistant's own title stays when it is a real one
			todo, err = s.createTaskTodoAs(ctx, t.ReservedTodoID, title, t.Prompt, p.Conversation, p.ID, t.Project)
			if err != nil {
				t.Error = err.Error()
				continue
			}
			if retitle {
				s.nameTodoLater(ctx, todo.ID, title, t.Prompt)
			}
		}
		t.TodoID = todo.ID
		s.briefStarted(ctx, p, t, todo) // what was asked, kept from now on (task_brief_plan.go)
		s.setTaskStageKind(ctx, todo.ID, "", "", taskStageKind(t.Readonly, t.DoneWhen, todo.PRURL), false)
		// The main conversation or a leader follows up what it hands out (supervise.go); a task the main conversation
		// handed to a leader is that leader's, while it still is one (else the main conversation's again).
		if sup := p.Conversation; s.supervises(ctx, sup) {
			if t.Supervisor != "" && s.isLeader(ctx, t.Supervisor) {
				sup = t.Supervisor
				if t.ReuseTodo == "" {
					s.taskCard(ctx, sup, todo.ID, "") // the leader sees its task's card too; the plan's stays where it was made
				}
			}
			s.superviseTodo(ctx, todo.ID, sup, t.DoneWhen, t.ExpectMinutes)
		} else if sup, ok := s.defaultPlanSupervisor(ctx, p.Conversation, t.Parent); ok {
			// Any other conversation's task is its own to supervise: its signals go to the conversation that planned it,
			// which also sees its card and reviews it. No second card when that is the planning conversation itself.
			if t.ReuseTodo == "" && sup != p.Conversation {
				s.taskCard(ctx, sup, todo.ID, "")
			}
			s.superviseTodo(ctx, todo.ID, sup, t.DoneWhen, t.ExpectMinutes)
		}
		if t.Parent != "" { // a task leader's worker: supervised by its leader's agent, with no conversation to show a card in
			s.superviseTodo(ctx, todo.ID, p.Conversation, t.DoneWhen, t.ExpectMinutes)
			continue
		}
		if t.ReuseTodo != "" {
			continue
		}
		if _, err := s.appendFeed(ctx, p.Conversation, feedTask, todo.ID, nil); err != nil {
			s.logf("task %s: feed: %v", todo.ID, err)
		}
	}
	if _, err := s.movePlan(ctx, p, planStarted); err != nil { // keep the todo ids
		s.logf("plan %s: %v", p.ID, err)
	}
	if remember {
		s.trustKinds(ctx, p.Tasks)
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	for i := range p.Tasks {
		if task := p.Tasks[i]; task.TodoID != "" {
			s.startPlanTaskAsync(p.ID, p.Conversation, i, task, false, func() {
				s.releaseTakenTodo(task.ReuseTodo) // linked or failed: the todo is no longer in the gap
			})
		}
	}
	return p, nil
}

// notReusable is the refusal of a todo_id: it says why, so the assistant can plan again without it.
func notReusable(id, why string) error {
	return fmt.Errorf("todo_id %s cannot be used: %s (leave todo_id out and the task gets a todo of its own)", id, why)
}

// reusableTodo says whether a task of a plan from conversation may run on todo id (task_plan's todo_id): the todo is
// one that conversation opened, is not done, has no agent on it (one linked and not let go or dismissed) and is not
// taken by a plan that started and has not linked its agent yet. taking: that last one is what the caller is about
// to claim itself, so it is not looked at (takeTodoForTask holds it).
func (s *Service) reusableTodo(conversation, id string, taking bool) error {
	t, ok := s.todoNow(id)
	switch {
	case !ok:
		return notReusable(id, "there is no such todo")
	case conversation == "" || t.Origin != conversation:
		return notReusable(id, "it was not opened from this conversation")
	case t.DoneAt != nil:
		return notReusable(id, "it is already done")
	}
	for _, a := range t.Agents {
		if a.ReleasedAt == nil && a.DismissedAt == nil {
			return notReusable(id, "an agent is already working on it")
		}
	}
	if !taking {
		s.tasks.mu.Lock()
		busy := s.tasks.reusing[id]
		s.tasks.mu.Unlock()
		if busy {
			return notReusable(id, "another plan is starting an agent on it")
		}
	}
	return nil
}

// takeTodoForTask is the todo a started plan's task runs on (PlanTask.ReuseTodo), checked again now (it may have been
// closed or taken since the plan was made) and held until the task's agent is linked (releaseTakenTodo).
func (s *Service) takeTodoForTask(ctx context.Context, planID, conversation, id string) (Todo, error) {
	s.tasks.mu.Lock()
	if s.tasks.reusing[id] {
		s.tasks.mu.Unlock()
		return Todo{}, notReusable(id, "another plan is starting an agent on it")
	}
	s.tasks.reusing[id] = true
	s.tasks.mu.Unlock()
	if err := s.reusableTodo(conversation, id, true); err != nil {
		s.releaseTakenTodo(id)
		return Todo{}, err
	}
	// TodoID is the execution binding; ReuseTodo preserves each historical plan's intended target.
	// Retire old bindings while this takeover holds the to-do, before the new agent can ask for approval.
	// Creation order is not start order, and a later retry can change an old plan's UpdatedAt.
	plans, err := s.conversationPlans(ctx, conversation)
	if err != nil {
		s.releaseTakenTodo(id)
		return Todo{}, err
	}
	for _, p := range plans {
		if p.ID == planID {
			continue
		}
		for i, task := range p.Tasks {
			if task.TodoID == id {
				if err := s.patchPlanTask(ctx, p.ID, i, func(t *PlanTask) { t.TodoID = "" }); err != nil {
					s.releaseTakenTodo(id)
					return Todo{}, err
				}
			}
		}
	}
	t, _ := s.todoNow(id)
	return formatTodo(t), nil
}

// releaseTakenTodo lets go of a todo takeTodoForTask held; id "" is nothing.
func (s *Service) releaseTakenTodo(id string) {
	if id == "" {
		return
	}
	s.tasks.mu.Lock()
	delete(s.tasks.reusing, id)
	s.tasks.mu.Unlock()
}

// taskStartTimeout bounds one start of a task's agent: a busy machine's shell can take minutes to run claude
// (node's claudeStartWait, #1396).
var taskStartTimeout = 5 * time.Minute

// taskFailTimeout bounds recording a failed start (the todo, the plan, the receipt), after the start's own time.
const taskFailTimeout = 30 * time.Second

// startPlanTask starts one task's agent and links it to the task's todo. A failed start ends the task: the todo is
// done with why (so nothing shows it as going on, #1077), the plan keeps the error, and the conversation gets a
// receipt — one per task: a retry that fails again (retry) updates the error the receipt shows (clients read it off
// the todo). A task the user finished while its agent was still starting (#2954) is not a failed start: the agent
// that did start stays in the task and the close loop retires it (tasks_close.go). Use startPlanTaskAsync to
// schedule it: go startPlanTask would register too late for the manager's idle check.
func (s *Service) startPlanTask(planID, conversation string, i int, t PlanTask, retry bool) {
	s.taskStarting.Add(1)
	defer s.taskStarting.Add(-1)
	s.runPlanTaskStart(planID, conversation, i, t, retry)
}

// startPlanTaskAsync reserves the start before launching its goroutine: the request may return before the
// goroutine is scheduled, and the manager must not see that gap as idle. Keep the reservation through cleanup.
func (s *Service) startPlanTaskAsync(planID, conversation string, i int, t PlanTask, retry bool, after func()) {
	s.taskStarting.Add(1)
	go func() {
		defer s.taskStarting.Add(-1)
		if after != nil {
			defer after()
		}
		s.runPlanTaskStart(planID, conversation, i, t, retry)
	}()
}

// runPlanTaskStart is the shared start body; its caller owns the idle reservation through every return path.
func (s *Service) runPlanTaskStart(planID, conversation string, i int, t PlanTask, retry bool) {
	t.planID, t.planIndex = planID, i
	defer s.releaseTaskWorkspace(t.TodoID)
	ctx, cancel := context.WithTimeout(context.Background(), taskStartTimeout)
	defer cancel()
	placed, placedWhy := t.Machine, t.Why
	if t.Role == roleLeader { // its tools are listed once it is linked: a list asked for meanwhile waits (task_leader.go)
		s.leaderStarts.Add(1)
		defer s.leaderStarts.Add(-1)
	}
	t.Permission = strings.TrimSpace(t.Permission)
	mid, sid, err := s.startTaskAgent(ctx, &t, retry)
	if err != nil && ctx.Err() == nil && !startStalled(err) {
		// The machine's link dropped (a node restarting, a tunnel blinking): try once more after a moment. Any other
		// launcher failure may move a flexible task, after verifying its directory on the destination.
		shouldRetry := transientStartErr(err)
		if !shouldRetry && t.MachinePolicy == machineFlexible {
			shouldRetry = s.healthyPlace(ctx, &t)
		}
		if shouldRetry {
			s.logf("task %s: start: %v; retrying", t.TodoID, err)
			select {
			case <-time.After(s.taskStartRetry()):
				mid, sid, err = s.startTaskAgent(ctx, &t, retry)
			case <-ctx.Done():
			}
		}
	}
	if t.Machine != placed || t.Why != placedWhy { // workspace admission already persisted the actual checkout
		s.updatePlanTask(ctx, planID, i, t)
	}
	if err == nil {
		err = s.linkPlanAgent(ctx, t, mid, sid, false)
		if errors.Is(err, db.ErrTodoCompleted) {
			// The user tapped "Done" while the agent was starting (a busy machine takes minutes, #1396). The task stays
			// done and its agent stays in it: nothing reads failed, and the close loop still finds the agent to retire.
			err = s.linkPlanAgent(ctx, t, mid, sid, true)
			if err != nil {
				s.logf("task %s: link to the finished task: %v", t.TodoID, err)
			}
		}
	}
	if err == nil {
		s.remedyStarted(context.WithoutCancel(ctx), planID, conversation, i, t) // after an assistant's retry (start_remedy.go)
		return
	}
	s.logf("task %s: start: %v", t.TodoID, err)
	// The start may have used up ctx (a busy machine's shell taking all of taskStartTimeout): recording the failure
	// on it failed too, and the task stayed "starting" with no agent for ever (#4510). The bookkeeping gets its own time.
	ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), taskFailTimeout)
	defer cancel()
	limit := 200
	if code, _ := codedStartRefusal(err); code != "" {
		limit = proto.CodedStartErrorMax // the hub's sentence, whose remedy comes last (#1642): kept whole
	}
	reason := clip(oneLine(s.startFailureText(ctx, t.Machine, err)), limit)
	// The new tab's shell waits on a question, or the agent's screen stopped, and the computer keeps the tab: a
	// supervised task's start is remedied first, not failed yet (start_remedy.go, #2570).
	code, args := codedStartRefusal(err)
	if s.beginRemedy(ctx, planID, i, t, err, code, args, reason) {
		return
	}
	// A retry the assistant asked for that failed so its tab is gone: the start fails for good, and the supervisor and
	// the user hear of it as of a first failure.
	remedied := t.Start != nil && t.Start.State == remedying
	if ferr := s.failTodo(ctx, t.TodoID, reason); ferr != nil {
		s.logf("task %s: fail: %v", t.TodoID, ferr)
	}
	if perr := s.patchPlanTask(ctx, planID, i, func(pt *PlanTask) {
		pt.Error = err.Error()
		pt.ErrorCode, pt.ErrorArgs = code, args
		if remedied && pt.Start != nil {
			pt.Start.State, pt.Start.Pane, pt.Start.At = remedyFailed, "", time.Now().UTC()
		}
	}); perr != nil {
		s.logf("plan %s: task %d's start error: %v", planID, i, perr)
	}
	s.tasks.mu.Lock()
	s.tasks.receipted[t.TodoID] = time.Now()
	s.tasks.mu.Unlock()
	if remedied {
		s.remedyStep(ctx, conversation, t.TodoID, startRemedyBody{Action: "gave_up", By: "hub", Attempt: t.Start.Attempt, Reason: reason})
	}
	if retry && !remedied {
		return
	}
	_, _ = s.appendFeed(ctx, conversation, feedReceipt, t.TodoID, receiptBody{Title: t.Title, State: "failed",
		Summary: i18n.T(s.lang(ctx), "assistant-tasks.failed_start", i18n.Args{"reason": reason})})
	if todo, ok, err := s.todo(ctx, t.TodoID); err == nil && ok {
		s.taskNotice(ctx, todo, t.Machine, "failed", "", reason)
		s.taskReport(todo, reportFailed, t.Machine, [2]string{}, reason, nil)
	}
}

var (
	errTaskNotRetryable = i18n.Err("assistant-tasks.not_retryable")
	errTaskNotInPlan    = i18n.Err("assistant-tasks.not_in_plan")
	errMachineOffline   = errors.New("machine offline") // matched with errors.Is; the text is machineOffline's
)

// machineOffline is errMachineOffline for machine name, said in the request's language.
func machineOffline(name string) error {
	return offlineError{i18n.Err("assistant-tasks.machine_offline", i18n.Args{"name": name})}
}

type offlineError struct{ msg error }

func (e offlineError) Error() string        { return e.msg.Error() }
func (e offlineError) Unwrap() error        { return e.msg }
func (e offlineError) Is(target error) bool { return target == errMachineOffline }

// retryTask starts again the agent of a task that did not start (#1396): the same task as its plan has it (machine,
// directory, prompt, launcher, model, permission, title), in the same todo — reopened, its error and the plan's
// cleared — so the task shows as starting where it was, not as a second one. A task already
// starting again, or started since, is answered as it is: a double tap starts one agent. A machine that is offline is
// refused before anything changes. A task that is open is not started again: one whose agent the model service cut
// off is told to go on (continueTask).
// The todo it answers is as the apps get it.
func (s *Service) retryTask(ctx context.Context, todoID string) (json.RawMessage, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return nil, err
	}
	cur, ok, err := s.todo(ctx, todoID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, db.ErrTodoNotFound
	}
	if cur.PlanID == "" || cur.Origin == "" {
		return nil, errTaskNotRetryable
	}
	if cur.DoneAt == nil {
		return s.continueTask(ctx, cur)
	}
	s.tasks.mu.Lock()
	busy := s.tasks.retrying[todoID]
	if !busy && cur.DoneAt != nil && cur.StartError != "" {
		s.tasks.retrying[todoID] = true
	}
	s.tasks.mu.Unlock()
	if busy || cur.DoneAt == nil || cur.StartError == "" {
		if !busy && cur.DoneAt != nil {
			return nil, errTaskNotRetryable // done by hand or by the hub, not a failed start
		}
		return s.todoView(ctx, todoID)
	}
	started := false
	defer func() {
		if !started {
			s.tasks.mu.Lock()
			delete(s.tasks.retrying, todoID)
			s.tasks.mu.Unlock()
		}
	}()
	p, err := s.loadPlan(ctx, cur.PlanID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(p.Tasks, func(t PlanTask) bool { return t.TodoID == todoID })
	if i < 0 {
		return nil, errTaskNotInPlan
	}
	p.Tasks[i].normalizeReadonlyPermission()
	t := p.Tasks[i]
	t.Permission = strings.TrimSpace(t.Permission)
	m, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return nil, i18n.Err("assistant-tasks.no_machine", i18n.Args{"machine": strconv.Quote(t.Machine)})
	}
	if err := s.leaderOnWorkMachine(&t, m); err != nil { // a retry of a leader plan saved on a computer that is work-trust now
		return nil, fmt.Errorf("%w; nothing was started", err)
	}
	if !m.Reachable {
		return nil, machineOffline(m.Name)
	}
	reopened, ok, err := s.reopenFailedTodo(ctx, todoID)
	if err != nil {
		return nil, err
	}
	view, err := json.Marshal(reopened)
	if err != nil {
		return nil, err
	}
	if !ok { // another assistant sharing the store reopened it first
		return view, nil
	}
	// Task i is kept as this retry starts it (t), its error cleared. The plan is read again under its lock, so what its
	// other tasks recorded since p was read stays.
	if err := s.patchPlanTask(ctx, p.ID, i, func(pt *PlanTask) {
		*pt = t
		pt.Error, pt.ErrorCode, pt.ErrorArgs = "", "", nil
	}); err != nil {
		s.logf("plan %s: %v", p.ID, err)
	}
	s.logf("task %s: retrying its start on %s", todoID, m.ID)
	started = true
	s.startPlanTaskAsync(p.ID, p.Conversation, i, t, true, func() {
		s.tasks.mu.Lock()
		delete(s.tasks.retrying, todoID)
		s.tasks.mu.Unlock()
	})
	return view, nil
}

// continueText is what goes on a cut-off turn: what the chat's "Retry" on an API error card sends.
const continueText = "继续"

// continueHold bounds how long a sent "Continue" keeps further taps from sending another while the agent takes it up.
var continueHold = 30 * time.Second

// continueTask tells the agent of a task whose latest turn the model service cut off (#1516) to go on, as the chat's
// "Retry" on the error card does; the task shows as in progress again once the agent works. One "Continue" per cut: taps while
// one is on its way, or before the agent took it up, answer the todo as it is, as does a task with no agent cut off.
// The agent's machine offline is refused.
func (s *Service) continueTask(ctx context.Context, cur Todo) (json.RawMessage, error) {
	var cut proto.Session
	for _, c := range s.taskAgents(ctx, cur) {
		if c.TurnError != "" && (cut.SessionID == "" || c.UpdatedAt.After(cut.UpdatedAt)) {
			cut = c
		}
	}
	if cut.SessionID == "" {
		return s.todoView(ctx, cur.ID)
	}
	m, ok := s.device(ctx, cut.MachineID)
	if !ok || !m.Reachable {
		name := cut.MachineID
		if ok {
			name = m.Name
		}
		return nil, machineOffline(name)
	}
	s.tasks.mu.Lock()
	busy := s.tasks.retrying[cur.ID]
	s.tasks.retrying[cur.ID] = true
	s.tasks.mu.Unlock()
	if busy {
		return s.todoView(ctx, cur.ID)
	}
	release := func() {
		s.tasks.mu.Lock()
		delete(s.tasks.retrying, cur.ID)
		s.tasks.mu.Unlock()
	}
	if stoppedAgent(cut) { // it stopped since (read-only, #4754): a new agent goes on with the task (tasks_successor.go)
		defer release()
		if live, _ := liveTaskAgents(s.taskAgents(ctx, cur)); len(live) > 0 {
			return s.todoView(ctx, cur.ID) // another agent of the task still runs: it is that one's
		}
		if _, err := s.startTaskSuccessor(ctx, cur, cut, continueText); err != nil {
			return nil, err
		}
		return s.todoView(ctx, cur.ID)
	}
	if err := s.call(ctx, cut.MachineID, DeviceSessionSend, map[string]any{"session_id": cut.SessionID, "text": continueText}, nil); err != nil {
		release()
		return nil, err
	}
	s.logf("task %s: told %s/%s to go on after: %s", cur.ID, cut.MachineID, cut.SessionID, clip(oneLine(cut.TurnError), 120))
	go func() {
		defer release()
		ctx := context.WithoutCancel(ctx)
		for end := time.Now().Add(continueHold); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
			// Still cut off by the same error: not taken up yet. The session is as the node lists it: Claude Code's error
			// is its last words, a Codex turn's the node's own TurnError (no words of its answer, #2185).
			c, ok := s.session(ctx, cut.MachineID, cut.SessionID)
			if !ok || c.Status == proto.StatusWorking || (strings.TrimSpace(c.LastText) != cut.TurnError && c.TurnError != cut.TurnError) {
				return // it took it up (or is gone): a later cut may be continued again
			}
		}
	}()
	return s.todoView(ctx, cur.ID)
}

// taskStartRetryDelay is how long a task waits before its one retry after a dropped link (Service.taskRetry shortens
// it in tests).
const taskStartRetryDelay = 3 * time.Second

// taskStartRetry is the wait before a failed task start's one retry: taskStartRetryDelay unless tests set taskRetry.
func (s *Service) taskStartRetry() time.Duration {
	if s.taskRetry > 0 {
		return s.taskRetry
	}
	return taskStartRetryDelay
}

// Bounds of the bounded retry before a needs recheck treats a node as unreachable: agentReachBackoff before the
// second try and doubled before the third; agentReachMax bounds one whole recheck, machines included (tests shorten).
var (
	agentReachAttempts = 3
	agentReachBackoff  = 300 * time.Millisecond
	agentReachMax      = 2 * time.Second
)

// transientStartErr: the agent did not start because a link on the way was down or dropped (this hub's to the
// machine, or one further on that answered with the same words: a member hub, a connector), or the new tab's
// terminal was not ready for the start command (proto.CodeTerminalNotReady), not because the node refused or the
// launcher failed; worth one retry, and never counted against the launcher (launcher_health.go).
func transientStartErr(err error) bool {
	var re *rpc.Error
	return errors.Is(err, rpc.ErrClosed) || errors.Is(err, apierr.ErrOffline) ||
		errors.As(err, &re) && (re.Message == rpc.ErrClosed.Error() || re.Code == proto.CodeTerminalNotReady)
}

func (s *Service) dismissPlan(ctx context.Context, id string) (*TaskPlan, error) {
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.State != planOpen {
		return nil, errPlanNotOpen
	}
	p.State = planDismissed
	ok, err := s.movePlan(ctx, p, planOpen)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errPlanNotOpen
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	return p, nil
}
