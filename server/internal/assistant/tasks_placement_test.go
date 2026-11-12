package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestFixedTaskPreviewKeepsTheRequestedMachine(t *testing.T) {
	s, _ := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	s.healthRound(ctx, true)
	res, err := s.toolTaskPlan(ctx, "conv9", json.RawMessage(`{"tasks":[{"title":"Inspect files","permission":"bypassPermissions","prompt":"Inspect files without writing","machine":"l4","cwd":"~/code/app","launcher":"work","why":"Files are local"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := regexp.MustCompile(`plan_[0-9a-f]+`).FindString(res)
	p, err := s.loadPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if task := p.Tasks[0]; task.Machine != "l4" || task.Cwd != "/home/me/code/app" || strings.Contains(task.Why, "instead") {
		t.Fatalf("fixed task moved during preview: %+v", task)
	}
	if p.Tasks[0].MachinePolicy != machineFixed {
		t.Fatalf("policy was not saved: %+v", p.Tasks[0])
	}
	// The persisted JSON uses the same codec when a manager reads the plan again.
	raw, err := json.Marshal(p.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	restored := planFromRow(db.TaskPlanRow{ID: p.ID, Tasks: raw, State: p.State})
	if restored.Tasks[0].MachinePolicy != machineFixed || restored.Tasks[0].Machine != "l4" {
		t.Fatalf("stored routing changed: %+v", restored)
	}
}

func taskStartMachines(f *fakeHub) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var machines []string
	for _, call := range f.calls {
		if call.Method == DeviceSessionStart {
			machines = append(machines, call.Device)
		}
	}
	return machines
}

func TestFixedTaskHealthChangesBetweenPreviewDispatchAndRetry(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	args := json.RawMessage(`{"tasks":[{"title":"Inspect files","prompt":"Inspect local files","machine":"l4","cwd":"~/code/app","launcher":"work","permission":"bypassPermissions"}]}`)
	res, err := s.toolTaskPlan(ctx, "conv9", args)
	if err != nil {
		t.Fatal(err)
	}
	id := regexp.MustCompile(`plan_[0-9a-f]+`).FindString(res)
	// The launcher fails after the preview, before dispatch.
	s.healthRound(ctx, true)
	p, err := s.startPlan(ctx, id, []PlanTask{{Title: "Inspect files"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	todoID := p.Tasks[0].TodoID
	waitFor(t, "fixed target failure", func() bool { return todoNow(s, todoID).StartError != "" })
	if starts := taskStartMachines(f); len(starts) != 0 {
		t.Fatalf("unhealthy fixed target dispatched: %v", starts)
	}
	stored, err := s.loadPlan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if task := stored.Tasks[0]; task.Machine != "l4" || task.MachinePolicy != machineFixed || task.Error == "" || task.Cwd != "/home/me/code/app" {
		t.Fatalf("fixed refusal was not retained: %+v", task)
	}
	// A retry while still unhealthy fails there too, without a cross-machine call.
	if _, err := s.retryTask(ctx, todoID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "retry failure", func() bool {
		s.tasks.mu.Lock()
		busy := s.tasks.retrying[todoID]
		s.tasks.mu.Unlock()
		return !busy && todoNow(s, todoID).StartError != ""
	})
	if starts := taskStartMachines(f); len(starts) != 0 {
		t.Fatalf("failed retry moved: %v", starts)
	}
	// An actual passing health result allows the same saved target on retry.
	s.recordHealth(ctx, "l4", "work", healthResult{ok: true, source: healthSourceCheck})
	if _, err := s.retryTask(ctx, todoID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "recovered fixed target", func() bool { return len(todoNow(s, todoID).Agents) == 1 })
	if starts := taskStartMachines(f); len(starts) != 1 || starts[0] != "l4" {
		t.Fatalf("recovery changed target: %v", starts)
	}
	f.mu.Lock()
	start := f.starts[0]
	f.mu.Unlock()
	if start["cwd"] != "/home/me/code/app" || start["permission_mode"] != "bypassPermissions" || !strings.Contains(start["prompt"].(string), "Inspect local files") {
		t.Fatalf("retry changed task inputs: %v", start)
	}
}

func TestFixedTaskAsyncAuthenticationFailureIsNotMoved(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	f.failStart = &rpc.Error{Message: "Not logged in; sign in to continue"}
	res, err := s.toolTaskPlan(context.Background(), "conv9", json.RawMessage(`{"tasks":[{"title":"Inspect","prompt":"Inspect","machine":"l4","cwd":"/work","launcher":"work","readonly":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "Queued 1 task(s)") || strings.Contains(res, "Started 1") {
		t.Fatalf("claimed a start: %s", res)
	}
	waitFor(t, "authentication failure", func() bool { return onlyTodo(s).StartError != "" })
	if starts := taskStartMachines(f); len(starts) != 1 || starts[0] != "l4" {
		t.Fatalf("authentication failure moved: %v", starts)
	}
	todo := onlyTodo(s)
	if todo.DoneAt == nil || len(todo.Agents) != 0 || !strings.Contains(todo.StartError, "Not logged in") {
		t.Fatalf("failure hidden: %+v", todo)
	}
	items, _, err := s.feed(context.Background(), "conv9", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.Kind == feedReceipt {
			var receipt receiptBody
			if err := json.Unmarshal(item.Body, &receipt); err != nil {
				t.Fatal(err)
			}
			found = found || receipt.State == "failed"
		}
	}
	if !found {
		t.Fatal("missing asynchronous failure receipt")
	}
}

func TestFixedTaskRejectsUnknownPoliciesAndPreservesEdits(t *testing.T) {
	s, _ := newTaskService(t)
	if _, err := s.toolTaskPlan(context.Background(), "conv1", json.RawMessage(`{"tasks":[{"title":"Inspect","permission":"bypassPermissions","prompt":"Inspect","machine":"box","cwd":"/work","machine_policy":"anywhere"}]}`)); err == nil {
		t.Fatal("unknown policy accepted")
	}
	task := PlanTask{Machine: "l4", MachinePolicy: machineFixed}
	applyPlanEdit(&task, PlanTask{Machine: "l4", Title: "Edited by an older client"})
	if task.MachinePolicy != machineFixed {
		t.Fatal("old card dropped fixed policy")
	}
	task.MachinePolicy = machineFlexible
	applyPlanEdit(&task, PlanTask{Machine: "box"})
	if task.MachinePolicy != machineFixed {
		t.Fatal("explicit machine edit did not pin its new target")
	}
	legacy := PlanTask{Machine: "l4"}
	if err := legacy.normalizeMachinePolicy(); err != nil || legacy.MachinePolicy != machineFixed {
		t.Fatalf("legacy plan was allowed to move: %+v %v", legacy, err)
	}
}

func TestTaskStartRequiresTheRequestedSessionIdentity(t *testing.T) {
	for _, session := range []proto.Session{{}, {MachineID: "other", SessionID: "foreign"}, {SessionID: "failed", StartError: "Not logged in"}} {
		s, f := newTaskService(t)
		f.nodes = map[string]nodeAnswer{DeviceSessionStart: func(string, json.RawMessage) (any, error) {
			return map[string]any{"session": session}, nil
		}}
		_, _, err := s.startTaskAgent(context.Background(), &PlanTask{Machine: "box", Cwd: "/work"}, false)
		if err == nil {
			t.Fatalf("invalid start treated as success: %+v", session)
		}
	}
	s, f := newTaskService(t)
	f.nodes = map[string]nodeAnswer{DeviceSessionStart: func(string, json.RawMessage) (any, error) {
		return map[string]any{"session": proto.Session{SessionID: "pending", MachineID: "box", Starting: true}}, nil
	}}
	if _, _, err := s.startTaskAgent(context.Background(), &PlanTask{Machine: "box", Cwd: "/work"}, false); err != nil {
		t.Fatal(err)
	}
	if r, ok := s.launcherHealth(context.Background(), "box", "claude"); ok {
		t.Fatalf("pending start recorded as health success: %+v", r)
	}
}

func TestFixedTaskUnknownOrOfflineTargetNeverFallsBack(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	f.devices[1].Reachable = false
	_, _, err := s.startTaskAgent(context.Background(), &PlanTask{Machine: "l4", Cwd: "/work", Launcher: "work"}, false)
	if !errors.Is(err, errMachineOffline) {
		t.Fatalf("offline target: %v", err)
	}
	_, _, err = s.startTaskAgent(context.Background(), &PlanTask{Machine: "unknown", Cwd: "/work", Launcher: "work"}, false)
	if err == nil {
		t.Fatal("unknown target accepted")
	}
	if starts := taskStartMachines(f); len(starts) != 0 {
		t.Fatalf("unavailable targets dispatched: %v", starts)
	}
}

func TestFixedTaskExplicitRetryRechecksTheOriginalTerminal(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	ctx := context.Background()
	f.failStart = &rpc.Error{Code: proto.CodeLauncherMissing, Message: "terminal has no launcher", Data: json.RawMessage(`{"where":"terminal","launcher":"work"}`)}
	if _, err := s.toolTaskPlan(ctx, "conv9", json.RawMessage(`{"tasks":[{"title":"Inspect","prompt":"Inspect","machine":"l4","cwd":"/work","launcher":"work","readonly":true}]}`)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "terminal refusal", func() bool { return onlyTodo(s).StartError != "" })
	if r := healthState(s, "l4", "work"); r.Kind != errKindNotInTerminal {
		t.Fatalf("terminal refusal lost: %+v", r)
	}
	// A login-shell check cannot establish that the terminal shell was fixed.
	s.recordHealth(ctx, "l4", "work", healthResult{ok: true, source: healthSourceCheck})
	f.mu.Lock()
	f.failStart = nil
	f.mu.Unlock()
	if _, err := s.retryTask(ctx, onlyTodo(s).ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "original terminal recovery", func() bool { return len(onlyTodo(s).Agents) == 1 })
	if starts := taskStartMachines(f); len(starts) != 2 || starts[0] != "l4" || starts[1] != "l4" {
		t.Fatalf("terminal recovery moved: %v", starts)
	}
	if r := healthState(s, "l4", "work"); r.State != healthOK {
		t.Fatalf("real terminal success did not clear failure: %+v", r)
	}
}

func TestReturnedTaskFailureCountsOnceAndPendingSuccessIsFollowed(t *testing.T) {
	s, f := newTaskService(t)
	ctx := context.Background()
	session := proto.Session{SessionID: "failed", Launcher: "work", StartError: "terminal has no launcher", StartErrorCode: proto.CodeLauncherMissing, StartErrorArgs: map[string]string{"where": "terminal", "launcher": "work"}}
	f.nodes = map[string]nodeAnswer{DeviceSessionStart: func(string, json.RawMessage) (any, error) {
		return map[string]any{"session": session}, nil
	}}
	if _, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "box", Cwd: "/work", Launcher: "work"}, false); err == nil {
		t.Fatal("failed session accepted")
	}
	s.followStarts(ctx, DeviceSessions{MachineID: "box", Sessions: []proto.Session{session}})
	if r := healthState(s, "box", "work"); r.Fails != 1 {
		t.Fatalf("one session counted more than once: %+v", r)
	}
	f.mu.Lock()
	session = proto.Session{SessionID: "pending", Launcher: "work", Starting: true}
	f.mu.Unlock()
	if _, _, err := s.startTaskAgent(ctx, &PlanTask{Machine: "box", Cwd: "/work", Launcher: "work"}, true); err != nil {
		t.Fatal(err)
	}
	if r := healthState(s, "box", "work"); r.State == healthOK {
		t.Fatal("pending response cleared terminal failure")
	}
	// Even if the first list arrives after startup, the returned pending session
	// was registered with the existing watcher, so its success clears the block.
	s.followStarts(ctx, DeviceSessions{MachineID: "box", Sessions: []proto.Session{{SessionID: "pending", Launcher: "work"}}})
	if r := healthState(s, "box", "work"); r.State != healthOK {
		t.Fatalf("pending start was not followed: %+v", r)
	}
}

func TestTaskPlanReportsQueuedDispatchBeforeAnAgentExists(t *testing.T) {
	s, _ := newTaskService(t)
	res, err := s.toolTaskPlan(context.Background(), "conv1", planArgs(true))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "Queued 1 task(s)") || strings.Contains(res, "Started 1 task(s)") || !strings.Contains(res, "not confirmation") {
		t.Fatalf("dispatch was reported as an actual start: %s", res)
	}
}

// A writable task says bypassPermissions itself (the tool refuses any other), so the manager's default mode and Claude
// Code's own never change what it starts with, and neither is read for it.
func TestTaskPlanKeepsTheExplicitBypassWhateverTheDefaultsSay(t *testing.T) {
	// claude: permissions.defaultMode in Claude Code's settings on box ("" none; "-" a node that can't tell, #3026).
	for _, tc := range []struct{ name, configured, claude, explicit, want string }{
		{"unset", "", "", "bypassPermissions", "bypassPermissions"},
		{"node cannot tell", "", "-", "bypassPermissions", "bypassPermissions"},
		{"claude settings", "", "auto", "bypassPermissions", "bypassPermissions"},
		{"claude settings, a mode Persona does not know", "", "dontAsk", "bypassPermissions", "bypassPermissions"},
		{"configured", "acceptEdits", "auto", "bypassPermissions", "bypassPermissions"},
		{"explicit", "acceptEdits", "auto", "bypassPermissions", "bypassPermissions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := newTaskService(t)
			ctx := context.Background()
			if _, err := s.updateSettings(ctx, func(v *Settings) error {
				v.DefaultPermissionMode = tc.configured
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			asked := 0
			if tc.claude != "-" {
				f.answerNode(assistantwire.DeviceClaudeSettings, func(machine string, _ json.RawMessage) (any, error) {
					asked++
					var value any
					if tc.claude != "" {
						value = tc.claude
					}
					return map[string]any{"settings": []map[string]any{
						{"key": "effortLevel", "value": "xhigh"}, {"key": proto.ClaudeDefaultModeSetting, "value": value},
					}}, nil
				})
			}
			// Writable tasks exercise the default contract. Read-only tasks are
			// independently constrained to plan and must not inherit this mode.
			task := map[string]any{"title": "Inspect code", "prompt": "Review code", "machine": "box",
				"cwd": "/code/app", "readonly": false, "permission": tc.explicit}
			second := maps.Clone(task)
			second["title"], second["cwd"] = "Inspect more code", "/code/lib"
			args, err := json.Marshal(map[string]any{"tasks": []map[string]any{task, second}})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.toolTaskPlan(ctx, "conv1", args)
			if err != nil {
				t.Fatal(err)
			}
			id := regexp.MustCompile(`plan_[0-9a-f]+`).FindString(res)
			plan, err := s.loadPlan(ctx, id)
			if err != nil {
				t.Fatalf("loadPlan: %v; response: %s", err, res)
			}
			if len(plan.Tasks) != 2 || plan.Tasks[0].Permission != tc.want || plan.Tasks[1].Permission != tc.want {
				t.Fatalf("saved tasks = %+v, want permission %q", plan.Tasks, tc.want)
			}
			// Read once a plan, and only when neither the task nor the manager's default says.
			wantAsked := 0
			if tc.claude != "-" && tc.configured == "" && tc.explicit == "" {
				wantAsked = 1
			}
			if asked != wantAsked {
				t.Fatalf("claude settings read %d times, want %d", asked, wantAsked)
			}
			f.mu.Lock()
			starts := len(f.starts)
			f.mu.Unlock()
			if starts != 0 {
				t.Fatalf("writable plan started before approval: %d", starts)
			}
			if _, err := s.startPlan(ctx, id, nil, false); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "approved tasks started", func() bool {
				f.mu.Lock()
				defer f.mu.Unlock()
				return len(f.starts) == 2
			})
			f.mu.Lock()
			got := []any{f.starts[0]["permission_mode"], f.starts[1]["permission_mode"]}
			f.mu.Unlock()
			if got[0] != tc.want || got[1] != tc.want {
				t.Fatalf("started permissions = %v, want %q", got, tc.want)
			}
		})
	}
}

// A saved writable plan starts without a permission, with another mode, or with a card edit that took it away: no
// permission is required. A read-only task still starts in plan mode.
func TestStartTakesASavedWritablePlanWithoutPermission(t *testing.T) {
	s, f := newTaskService(t)
	ctx := context.Background()
	for _, saved := range []string{"", "default", "acceptEdits"} {
		res, err := s.toolTaskPlan(ctx, "conv1", json.RawMessage(`{"tasks":[{"title":"Fix","permission":"bypassPermissions","prompt":"Fix it","machine":"box","cwd":"/code/app"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		plan, err := s.loadPlan(ctx, regexp.MustCompile(`plan_[0-9a-f]+`).FindString(res))
		if err != nil {
			t.Fatal(err)
		}
		plan.Tasks[0].Permission = saved
		if err := s.savePlan(ctx, plan); err != nil {
			t.Fatal(err)
		}
		want := len(f.starts) + 1
		if _, err := s.startPlan(ctx, plan.ID, []PlanTask{{Permission: ""}}, false); err != nil {
			t.Fatalf("saved permission %q: %v", saved, err)
		}
		waitFor(t, "the start with permission "+saved, func() bool { return len(f.starts) == want })
	}
}
