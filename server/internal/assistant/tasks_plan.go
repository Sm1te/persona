package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the assistant's task_plan tool ----

// planTaskIn is a task as task_plan's arguments have it. Its todo_id is the todo to run on (PlanTask.ReuseTodo); the
// plan's own todo_id, the todo a started task became, is the hub's to set, and the shallower field wins in JSON.
type planTaskIn struct {
	PlanTask
	TodoID string `json:"todo_id"`
}

// toolTaskPlanFrom is task_plan from the conversation sessionID running on machine (where its attachments are).
func (s *Service) toolTaskPlanFrom(ctx context.Context, machine, sessionID string, args json.RawMessage) (string, error) {
	return s.taskPlanFrom(ctx, machine, sessionID, args, nil)
}

// taskPlanFrom is task_plan from sessionID: a conversation (scope nil), or the agent of the task leader's task in scope,
// which plans workers beneath that task only and starts them at once (task_leader.go).
func (s *Service) taskPlanFrom(ctx context.Context, machine, sessionID string, args json.RawMessage, scope *leaderScope) (string, error) {
	var a struct{ Tasks []PlanTask }
	lead := "" // the leader conversation the main conversation hands the plan to (task_plan's supervisor, #3608)
	if scope != nil {
		tasks, err := scope.workerTasks(args)
		if err != nil {
			return "", err
		}
		a.Tasks = tasks
	} else {
		var in struct {
			Tasks      []planTaskIn `json:"tasks"`
			Supervisor string       `json:"supervisor"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("tasks: %v", err)
		}
		var err error
		if lead, err = s.planSupervisor(ctx, sessionID, in.Supervisor); err != nil {
			return "", err
		}
		for _, t := range in.Tasks {
			t.PlanTask.ReuseTodo = strings.TrimSpace(t.TodoID)
			t.PlanTask.Parent = "" // the hub's, set only for a task leader's workers
			t.PlanTask.Supervisor = lead
			a.Tasks = append(a.Tasks, t.PlanTask)
		}
	}
	if len(a.Tasks) == 0 {
		return "", fmt.Errorf("tasks is empty")
	}
	if len(a.Tasks) > 8 {
		return "", fmt.Errorf("at most 8 tasks in one plan")
	}
	if scope != nil {
		if err := s.checkLeaderRoom(ctx, scope, len(a.Tasks)); err != nil {
			return "", err
		}
	}
	reused := map[string]bool{}
	modes := map[string]string{} // each computer's Claude Code default mode, read once for this plan
	for i := range a.Tasks {
		t := &a.Tasks[i]
		t.normalizeReadonlyPermission()
		t.TodoID, t.Error, t.ErrorCode, t.ErrorArgs = "", "", "", nil
		t.WorkspaceResolved, t.ReservedTodoID, t.WorkspaceInferred = nil, "", false
		t.OwnerStarted = false // the hub's: only the owner's Start on the card sets it (startPlan)
		// Reroute is hub-owned: drop any reroute a model passes in task_plan args, so healthyPlace seeds it only from a
		// real health detour and its first-detour guard keeps the true original machine (#2271).
		t.Reroute = nil
		t.Title, t.Prompt, t.Cwd = strings.TrimSpace(t.Title), strings.TrimSpace(t.Prompt), strings.TrimSpace(t.Cwd)
		t.Model = strings.TrimSpace(t.Model)
		t.Permission = strings.TrimSpace(t.Permission)
		if err := t.checkRole(scope != nil); err != nil {
			return "", fmt.Errorf("task %d: %w", i+1, err)
		}
		if m, ok := s.machineByName(ctx, t.Machine); ok && t.Role == roleLeader && s.isWorkMachine(m.ID) {
			return "", fmt.Errorf("task %d: role \"leader\" cannot run on %s: a work-trust computer gives its agents only the "+
				"reporting tools, so it could not supervise workers. Put the task leader on a computer you own", i+1, m.Name)
		}
		if err := t.normalizeMachinePolicy(); err != nil {
			return "", fmt.Errorf("task %d: %w", i+1, err)
		}
		t.DoneWhen, t.ExpectMinutes = cleanDoneWhen(t.DoneWhen), max(0, min(t.ExpectMinutes, 24*60))
		if t.Title == "" || t.Prompt == "" || t.Cwd == "" {
			return "", fmt.Errorf("task %d: title, prompt and cwd are required", i+1)
		}
		t.Brief = nil // the hub's (planBriefs below), never the tool call's
		t.Goal, t.OutOfBounds = clip(oneLine(t.Goal), 300), cleanDoneWhen(t.OutOfBounds)
		t.Continues, t.ContinuesWhy = strings.TrimSpace(t.Continues), clip(oneLine(t.ContinuesWhy), 300)
		t.StartedWhy = clip(oneLine(t.StartedWhy), 300)
		if err := briefRequired(t); err != nil && !(s.briefsOptional && t.Continues == "") {
			return "", fmt.Errorf("task %d (%s): %w", i+1, t.Title, err)
		}
		if t.Model != "" && !proto.ValidModelID(t.Model) { // it goes to the agent as --model (#1644)
			return "", fmt.Errorf("task %d: model %q is not a model id: %s", i+1, t.Model, proto.ModelIDRule)
		}
		if scope == nil { // a task leader has none to give and keeps its task's project
			atts, err := cleanAttachments(t.Attachments)
			if err == nil {
				atts, err = s.checkAttachments(ctx, machine, sessionID, atts)
			}
			if err != nil {
				return "", fmt.Errorf("task %d: %w", i+1, err)
			}
			t.Attachments, t.Mentioned, t.AttachFrom = atts, s.mentionedAttachments(ctx, machine, sessionID, t.Prompt, atts), ""
			if t.Project, err = s.todoProject(ctx, t.Project); err != nil {
				return "", fmt.Errorf("task %d: %w", i+1, err)
			}
		}
		if len(t.Attachments)+len(t.Mentioned) > 0 {
			t.AttachFrom = machine
		}
		if !s.placeTask(ctx, t) {
			return "", fmt.Errorf("task %d: unknown machine %q (machines: %s)", i+1, t.Machine, s.machineIDs(ctx))
		}
		// Validate the tier now without pinning its model: it is resolved again at dispatch.
		preview := *t
		if err := s.applyTaskTier(ctx, &preview); err != nil {
			return "", fmt.Errorf("task %d: %w", i+1, err)
		}
		s.healthyPlace(ctx, &preview) // health follows the resolved launcher
		t.Machine, t.Cwd, t.Why = preview.Machine, preview.Cwd, preview.Why
		t.Reroute = preview.Reroute
		// What it runs on the computer it lands on (#2640): a default that computer lacks is swapped now, so the card
		// names the launcher that will run and its why says so; a launcher this plan names that it lacks is refused.
		if m, ok := s.machineByName(ctx, t.Machine); ok {
			params := map[string]any{"launcher": t.Launcher, "model": t.Model, "effort": t.Effort, "tier": t.Tier}
			own, err := s.resolveStart(ctx, params)
			if err != nil {
				return "", fmt.Errorf("task %d: %w", i+1, err)
			}
			claude, err := s.fitTaskLauncher(ctx, t, m, own, params)
			if err != nil {
				return "", fmt.Errorf("task %d: %w", i+1, err)
			}
			if claude {
				t.Permission = "bypassPermissions"
			}
		}
		// Unset: settled on the computer it lands on, so the card shows what will run (tasks_permission.go).
		if t.Permission == "" {
			t.Permission = s.taskPermission(ctx, t.Machine, modes)
		}
		if err := s.resolveTaskWorkspace(ctx, t); err != nil {
			return "", fmt.Errorf("task %d: %w", i+1, err)
		}
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", fmt.Errorf("todos are unavailable: %v", err)
	}
	for i, t := range a.Tasks {
		if t.ReuseTodo == "" {
			continue
		}
		if reused[t.ReuseTodo] {
			return "", fmt.Errorf("task %d: todo_id %s is already the todo of another task in this plan", i+1, t.ReuseTodo)
		}
		reused[t.ReuseTodo] = true
		if err := s.reusableTodo(sessionID, t.ReuseTodo, false); err != nil {
			return "", fmt.Errorf("task %d: %v", i+1, err)
		}
	}
	if err := s.planBriefs(ctx, machine, sessionID, a.Tasks); err != nil {
		return "", err
	}
	dups := s.planDuplicates(ctx, a.Tasks) // before the plan's own todos exist
	now := time.Now().UTC()
	p := &TaskPlan{ID: "plan_" + randomHex(6), Conversation: sessionID, Tasks: a.Tasks, State: planOpen, CreatedAt: now, UpdatedAt: now}
	if err := s.savePlan(ctx, p); err != nil {
		return "", err
	}
	// The plan goes in the item's body too: a client that missed the task_plan event (or raced it) still draws the
	// card from the conversation itself.
	if scope == nil {
		if _, err := s.appendFeed(ctx, sessionID, feedPlan, p.ID, map[string]any{"plan": p}); err != nil {
			s.logf("task plan %s: feed: %v", p.ID, err)
		}
	}
	// A plan from the health space hands its prompts to agents outside it: it waits for the user's "Start" on its card,
	// whatever would start it at once, and its push names no task (health.go).
	health := s.inHealth(ctx, sessionID)
	if scope != nil { // no card: a leader's workers start at once, as part of the task the user let start
		started, err := s.startPlan(ctx, p.ID, nil, false)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Queued %d worker(s) beneath your task %s for dispatch:\n", len(started.Tasks), scope.task.ID)
		for _, t := range started.Tasks {
			if t.Error != "" {
				fmt.Fprintf(&b, "- %s on %s: dispatch failed: %s\n", t.Title, t.Machine, t.Error)
			} else {
				fmt.Fprintf(&b, "- %s → todo %s on %s (dispatching)\n", t.Title, t.TodoID, t.Machine)
			}
		}
		b.WriteString("This is not confirmation that any agent started: you are told when one fails to start. A worker that has " +
			"report_to_supervisor reports to you in a message, and they cannot hand work on.")
		b.WriteString(dups)
		return b.String(), nil
	}
	if health || !s.planStartsAtOnce(ctx, p.Tasks) {
		s.event(ctx, map[string]any{"type": "task_plan", "plan": s.planWithSuggestion(ctx, p, s.lang(ctx))})
		// A card below the answer is easy to miss: push it as well.
		titles := make([]string, 0, len(p.Tasks))
		for _, t := range p.Tasks {
			titles = append(titles, t.Title)
		}
		l := s.lang(ctx)
		title := i18n.T(l, "assistant-tasks.plan_waiting", i18n.Args{"n": len(p.Tasks)})
		if len(titles) == 1 {
			title = i18n.T(l, "assistant-tasks.plan_waiting_single")
		}
		here := s.settings(ctx).MachineID
		machineName := "Persona"
		if m, ok := s.device(ctx, here); ok {
			machineName = m.Name
		}
		body := strings.Join(titles, i18n.T(l, "assistant-tasks.list_sep"))
		if health {
			title, body = healthNotice(l)
		}
		s.push(ctx, Notification{Subtitle: machineName, DeviceID: here, SessionID: sessionID, Title: title, Body: body})
		if lead != "" || s.isLeader(ctx, sessionID) {
			s.lreports.plansDue.Store(true)
			s.directionsSoon() // a leader's plan waiting for Start is a decision for the owner (leader_reports.go)
		}
		return fmt.Sprintf("Plan %s is on the user's screen as a card and pushed to their phone; nothing has started. "+
			"Don't wait for it: carry on. When they start it, the tasks show up in the context snapshot.", p.ID) + dups, nil
	}
	started, err := s.startPlan(ctx, p.ID, nil, false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Queued %d task(s) for dispatch without asking (read-only, asked for by the user, or a kind the user trusts):\n", len(started.Tasks))
	for _, t := range started.Tasks {
		if t.Error != "" {
			fmt.Fprintf(&b, "- %s on %s: dispatch failed: %s\n", t.Title, t.Machine, t.Error)
		} else {
			fmt.Fprintf(&b, "- %s → todo %s on %s (dispatching)\n", t.Title, t.TodoID, t.Machine)
		}
	}
	b.WriteString("This is not confirmation that any agent started. Startup can still fail; the task cards and context snapshot show the outcome.")
	background := s.backgroundOn(s.settings(ctx)) // who follows up a task no leader supervises (supervise_background.go)
	switch {
	case lead != "":
		fmt.Fprintf(&b, " Leader conversation %s supervises them: their supervision messages go there, and it reviews them with task_review.", lead)
	case background && s.isBackground(ctx, sessionID):
		b.WriteString(" You follow them up as the background supervisor: a supervision message comes when one needs you (review it with task_review).")
	case background && s.isMain(ctx, sessionID):
		b.WriteString(" The background supervisor follows them up: their supervision messages go to it and it reviews them with " +
			"task_review, so none comes to you for them. You still hear of a start that failed and of what it hands to the user, " +
			"and may review any of them yourself (task_review).")
	case s.supervises(ctx, sessionID):
		b.WriteString(" You supervise them: a supervision message comes when one needs you (review it with task_review).")
	default:
		// A live side conversation supervises what it hands out; one that cannot be told anything leaves its task to
		// the main conversation's follow-up (defaultPlanSupervisor), which is the background supervisor's while it is on.
		if sup, ok := s.defaultPlanSupervisor(ctx, sessionID, ""); ok && sup == sessionID {
			b.WriteString(" You supervise them: a supervision message comes when one needs you (review it with task_review).")
		} else if ok && background {
			b.WriteString(" The background supervisor follows them up (their supervision messages and task_review); this conversation " +
				"gets a one-line notice with the task's link when each one ends.")
		} else if ok {
			b.WriteString(" The main conversation supervises them (its supervision messages and task_review); this conversation " +
				"gets a one-line notice with the task's link when each one ends.")
		}
	}
	b.WriteString(dups)
	return b.String(), nil
}

// planSupervisor is task_plan's supervisor from conversation self: "" (self supervises, as before) or a current leader,
// which only the main conversation may hand work to.
func (s *Service) planSupervisor(ctx context.Context, self, lead string) (string, error) {
	lead = strings.TrimSpace(lead)
	switch {
	case lead == "" || lead == self:
		return "", nil
	case !s.isMain(ctx, self):
		return "", errors.New("supervisor: only the main conversation hands work to a leader conversation; leave it out to supervise the tasks yourself")
	case !s.isLeader(ctx, lead):
		return "", fmt.Errorf("supervisor: %s is not a leader conversation (assistant.leaders lists them); leave it out to supervise the tasks yourself", lead)
	}
	return lead, nil
}

// applyPlanEdit takes what the user changed on the card; an empty field keeps the assistant's.
func applyPlanEdit(t *PlanTask, e PlanTask) {
	if e.Workspace != nil {
		t.Workspace, t.WorkspaceResolved, t.WorkspaceInferred = e.Workspace, nil, false
	}
	if s := strings.TrimSpace(e.Title); s != "" {
		t.Title = s
	}
	if s := strings.TrimSpace(e.Prompt); s != "" {
		t.Prompt = s
	}
	if s := strings.TrimSpace(e.Machine); s != "" {
		if s != t.Machine {
			t.MachinePolicy = machineFixed
			// The user's explicit retarget supersedes a plan-time health detour: drop the stale reroute so the receipt
			// names the machine the task will actually run on, not the old landing (#2271).
			t.Reroute = nil
		}
		t.Machine = s
	}
	if e.MachinePolicy != "" {
		t.MachinePolicy = e.MachinePolicy
	}
	if s := strings.TrimSpace(e.Cwd); s != "" {
		t.Cwd = s
	}
	if s := strings.TrimSpace(e.Launcher); s != "" {
		t.Launcher = s
	}
	if s := strings.TrimSpace(e.Model); s != "" {
		t.Model = s
	}
	if s := strings.TrimSpace(e.Tier); s != "" {
		t.Tier = s
	}
	if s := strings.TrimSpace(e.Effort); s != "" {
		t.Effort = s
	}
	if s := strings.TrimSpace(e.Permission); s != "" {
		t.Permission = s
	}
	if e.Ultracode != nil {
		t.Ultracode = e.Ultracode
	}
}

func planErr(w http.ResponseWriter, err error) {
	var invalid planInvalid
	switch {
	case errors.As(err, &invalid):
		apierr.Write(w, http.StatusBadRequest, invalid.error)
	case errors.Is(err, errPlanNotFound):
		apierr.Write(w, http.StatusNotFound, err)
	case errors.Is(err, errPlanNotOpen):
		apierr.Write(w, http.StatusConflict, err)
	default:
		apierr.Write(w, http.StatusServiceUnavailable, err)
	}
}

// checkRole refuses a role a plan task may not have, before anything is planned. Any conversation may hand out a
// leader: its own reports reach the conversation that planned it. A task leader's workers are never one.
func (t *PlanTask) checkRole(byLeader bool) error {
	switch {
	case t.Role == "":
	case byLeader:
		return errors.New("a task leader plans workers only: role is refused")
	case t.Role != roleLeader:
		return fmt.Errorf("role %q is not one: leave it out, or \"leader\"", t.Role)
	}
	return nil
}
