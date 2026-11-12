package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func readonlyTaskArgs(t *testing.T, tasks ...PlanTask) json.RawMessage {
	t.Helper()
	type input struct {
		PlanTask
		TodoID string `json:"todo_id,omitempty"`
	}
	inputs := make([]input, 0, len(tasks))
	for _, task := range tasks {
		inputs = append(inputs, input{PlanTask: task, TodoID: task.ReuseTodo})
	}
	raw, err := json.Marshal(map[string]any{"tasks": inputs})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readonlyTestTask(permission string) PlanTask {
	return PlanTask{Title: "Inspect source", Prompt: "Read the source and explain the login flow.", Machine: "box",
		Cwd: "/code/app", Readonly: true, Permission: permission}
}

// Avoid a real model server in a synctest bubble: these tests inspect dispatch, not generated tab names.
func readonlyTaskService(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	f := newFakeHub()
	f.sessions["box"] = nil
	f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}}
	f.ownModels = true
	return newStorelessService(t, f), f
}

func readonlyFeedPlan(t *testing.T, s *Service, conversation string) *TaskPlan {
	t.Helper()
	_, plans, err := s.feed(context.Background(), conversation, time.Time{})
	if err != nil || len(plans) != 1 {
		t.Fatalf("feed plans = %v, error = %v", plans, err)
	}
	for _, p := range plans {
		return p
	}
	return nil
}

func TestReadonlyTaskPlanLaunchPermissions(t *testing.T) {
	for _, permission := range []string{"", "default", "plan", "acceptEdits", "bypassPermissions"} {
		name := permission
		if name == "" {
			name = "omitted"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, readonlyTestTask(permission))); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				p := readonlyFeedPlan(t, s, "readonly-chat")
				if p.State != planStarted || p.Tasks[0].Permission != "bypassPermissions" || !p.Tasks[0].Readonly {
					t.Fatalf("saved read-only plan = %+v", p)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" {
					t.Fatalf("read-only launch = %v", f.starts)
				}
				prompt, _ := f.starts[0]["prompt"].(string)
				if strings.Count(prompt, "本任务只读：不改项目文件、不提交、不对外写入") != 1 {
					t.Fatalf("read-only prompt = %q", prompt)
				}
				if todo := onlyTodo(s); len(todo.Agents) != 1 {
					t.Fatalf("task did not finish dispatch: %+v", todo)
				}
			})
		})
	}
}

func TestReadonlyTaskClaudeAliasLaunchPermissions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := readonlyTaskService(t)
		f.launcherList = []proto.Launcher{{Name: "cc-grok", Agent: "claude"}}
		task := readonlyTestTask("plan")
		task.Launcher = "cc-grok"
		if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if p := readonlyFeedPlan(t, s, "readonly-chat"); p.Tasks[0].Permission != "bypassPermissions" {
			t.Fatalf("saved aliased Claude plan = %+v", p)
		}
		if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" {
			t.Fatalf("aliased Claude launch = %v", f.starts)
		}
	})
}

// Waiting cards and their edits must carry the same restriction as automatic starts.
func TestReadonlyTaskWaitingPlanEdits(t *testing.T) {
	for _, health := range []bool{false, true} {
		name := "mixed"
		if health {
			name = "health"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				conversation := "readonly-chat"
				tasks := []PlanTask{readonlyTestTask("bypassPermissions")}
				if health {
					f.hello.Assistant.MachineID, f.hello.Assistant.Machines = "box", []string{"box"}
					setSettings(t, s, func(v *Settings) { v.Main, v.MachineID = "main", "box" })
					f.setSessions("box", proto.Session{SessionID: conversation, Space: SpaceHealth})
				} else {
					edit := readonlyTestTask("bypassPermissions")
					edit.Title, edit.Prompt, edit.Readonly = "Fix login", "Fix the login flow and add tests.", false
					tasks = append(tasks, edit)
				}
				if _, err := s.toolTaskPlan(context.Background(), conversation, readonlyTaskArgs(t, tasks...)); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				p := readonlyFeedPlan(t, s, conversation)
				if p.State != planOpen || p.Tasks[0].Permission != "bypassPermissions" || len(f.starts) != 0 {
					t.Fatalf("waiting plan = %+v, starts = %v", p, f.starts)
				}
				p, err := s.startPlan(context.Background(), p.ID, []PlanTask{{Permission: "acceptEdits"}}, false)
				if err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				p = readonlyFeedPlan(t, s, conversation)
				if p.State != planStarted || p.Tasks[0].Permission != "bypassPermissions" {
					t.Fatalf("edited plan = %+v", p)
				}
				if len(f.starts) != len(tasks) {
					t.Fatalf("starts = %v", f.starts)
				}
				for _, start := range f.starts {
					if start["permission_mode"] != "bypassPermissions" {
						t.Fatalf("start = %v, want Claude bypass", start)
					}
				}
			})
		})
	}
}

func TestReadonlyTaskLegacyStartAndRetry(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "start"
		if retry {
			name = "retry"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				ctx := context.Background()
				p := &TaskPlan{ID: "plan_legacy", Conversation: "readonly-chat", State: planOpen,
					Tasks: []PlanTask{readonlyTestTask("bypassPermissions")}}
				if retry {
					todo, err := s.createTodo(ctx, p.Tasks[0].Title, p.Tasks[0].Prompt, p.Conversation, p.ID)
					if err != nil {
						t.Fatal(err)
					}
					if err := s.failTodo(ctx, todo.ID, "Start failed"); err != nil {
						t.Fatal(err)
					}
					p.State, p.Tasks[0].TodoID = planStarted, todo.ID
				}
				if err := s.savePlan(ctx, p); err != nil {
					t.Fatal(err)
				}
				if retry {
					if _, err := s.retryTask(ctx, p.Tasks[0].TodoID); err != nil {
						t.Fatal(err)
					}
				} else if _, err := s.startPlan(ctx, p.ID, nil, false); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				saved, err := s.loadPlan(ctx, p.ID)
				if err != nil || saved.Tasks[0].Permission != "bypassPermissions" {
					t.Fatalf("legacy plan = %+v, error = %v", saved, err)
				}
				if len(f.starts) != 1 || f.starts[0]["permission_mode"] != "bypassPermissions" || len(onlyTodo(s).Agents) != 1 {
					t.Fatalf("legacy launch = %v, todo = %+v", f.starts, onlyTodo(s))
				}
				prompt, _ := f.starts[0]["prompt"].(string)
				if strings.Count(prompt, "本任务只读：不改项目文件、不提交、不对外写入") != 1 {
					t.Fatalf("legacy read-only prompt = %q", prompt)
				}
			})
		})
	}
}

func TestReadonlyTaskDispatchBoundary(t *testing.T) {
	for _, readonly := range []bool{false, true} {
		for _, permission := range []string{"", "default", "plan", "acceptEdits", "bypassPermissions"} {
			s, f := readonlyTaskService(t)
			task := readonlyTestTask(permission)
			task.Readonly = readonly
			if _, _, err := s.startTaskAgent(context.Background(), &task, true); err != nil {
				t.Fatal(err)
			}
			got, _ := f.starts[0]["permission_mode"].(string)
			if got != "bypassPermissions" || task.Permission != "bypassPermissions" {
				t.Fatalf("readonly=%v permission=%q: launch=%v task=%+v", readonly, permission, f.starts, task)
			}
		}
	}
}

func TestReadonlyTaskResolveStartFailureDoesNotSavePlan(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	s, f := newTaskServiceOn(t, database)
	f.launcherList = []proto.Launcher{{Name: "claude", Agent: "claude"}}
	if err := s.putSetting(ctx, launchersManagedKey, []storedLauncher{{ManagedLauncher: proto.ManagedLauncher{
		Name: "direct", Kind: "api", API: "responses", BaseURL: "https://api.example.com", Model: "m",
	}}}); err != nil {
		t.Fatal(err)
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.direct"})
	_, err = s.toolTaskPlan(ctx, "readonly-chat", readonlyTaskArgs(t, readonlyTestTask("")))
	resolutionError := i18n.T(s.lang(ctx), "assistant-models.err_api_as_launcher")
	if err == nil || !strings.Contains(err.Error(), resolutionError) {
		t.Fatalf("planning error = %v, want the launcher/model resolution failure", err)
	}
	if len(f.starts) != 0 {
		t.Fatalf("a failed resolution started agents: %v", f.starts)
	}
	rows, readErr := database.TaskPlans(ctx, "readonly-chat")
	if readErr != nil || len(rows) != 0 {
		t.Fatalf("a failed resolution saved a plan: %v, error = %v", rows, readErr)
	}
}

func TestReadonlyCodexTaskKeepsPlanSandbox(t *testing.T) {
	s, f := readonlyTaskService(t)
	f.launcherList = []proto.Launcher{{Name: "codex", Agent: "codex"}}
	task := readonlyTestTask("bypassPermissions")
	task.Launcher = "codex"
	if _, _, err := s.startTaskAgent(context.Background(), &task, true); err != nil {
		t.Fatal(err)
	}
	if got := f.starts[0]["permission_mode"]; got != "plan" || task.Permission != "plan" {
		t.Fatalf("Codex read-only launch = %v, task = %+v", f.starts, task)
	}
}

func TestReadonlyTaskUnsupportedLauncherReportsFailure(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		t.Run(string(lang), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				f.hello.Locale = string(lang)
				f.failStart = &rpc.Error{Code: "readonly_unsupported", Message: "Node cannot enforce read-only mode"}
				ctx := context.Background()
				if _, err := s.toolTaskPlan(ctx, "readonly-chat", readonlyTaskArgs(t, readonlyTestTask(""))); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				p := readonlyFeedPlan(t, s, "readonly-chat")
				if task := p.Tasks[0]; task.ErrorCode != "readonly_unsupported" || task.Permission != "bypassPermissions" {
					t.Fatalf("unsupported task = %+v", task)
				}
				if todo := onlyTodo(s); todo.DoneAt == nil || len(todo.Agents) != 0 ||
					todo.StartError != i18n.T(lang, "rpc-errors.readonly_unsupported") {
					t.Fatalf("refusal was not localized or finished: %+v", todo)
				}
				if len(f.starts) != 1 || f.agents != 0 || len(f.checked) != 0 {
					t.Fatalf("refusal retried or probed the launcher: starts=%v agents=%d checks=%v", f.starts, f.agents, f.checked)
				}
				if health, ok := s.launcherHealth(ctx, "box", "claude"); ok {
					t.Fatalf("unsupported mode poisoned launcher health: %+v", health)
				}
			})
		})
	}
}

func TestReadonlyTaskPlanPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	s, _ := newTaskServiceOn(t, database)
	read := readonlyTestTask("bypassPermissions")
	edit := readonlyTestTask("bypassPermissions")
	edit.Readonly = false
	if _, err := s.toolTaskPlan(ctx, "readonly-chat", readonlyTaskArgs(t, read, edit)); err != nil {
		t.Fatal(err)
	}
	p := readonlyFeedPlan(t, s, "readonly-chat")
	restarted, _ := newTaskServiceOn(t, database)
	saved, err := restarted.loadPlan(ctx, p.ID)
	if err != nil || saved.State != planOpen || saved.Tasks[0].Permission != "bypassPermissions" || saved.Tasks[1].Permission != "bypassPermissions" {
		t.Fatalf("persisted plan = %+v, error = %v", saved, err)
	}
}

func TestReadonlyTaskExitPlanModeDoesNotAskUser(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%v", reuse), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := readonlyTaskService(t)
				task := readonlyTestTask("")
				if reuse {
					todo, err := s.createTodo(context.Background(), "Inspect", "", "readonly-chat", "")
					if err != nil {
						t.Fatal(err)
					}
					task.ReuseTodo = todo.ID
				}
				if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				todo := onlyTodo(s)
				if len(todo.Agents) != 1 {
					t.Fatalf("task not dispatched: %+v", todo)
				}
				if reuse && (todo.ID != task.ReuseTodo || todo.PlanID != "") {
					t.Fatalf("expected the original reused to-do, got %+v", todo)
				}
				agent := todo.Agents[0]
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				for _, resumable := range []bool{false, true} {
					r, err := s.nodeInteraction(ctx, proto.Interaction{ID: "readonly-exit", MachineID: agent.MachineID,
						SessionID: agent.SessionID, Kind: proto.IxPlan, ToolName: "ExitPlanMode", Plan: "Inspection complete.", Resumable: resumable})
					if err != nil {
						t.Fatal(err)
					}
					if r.Approve == nil || *r.Approve || r.Dismiss || r.Mode != "" || !strings.Contains(r.Feedback, "read-only") {
						t.Fatalf("read-only plan exit must be denied with completion guidance, got %+v", r)
					}
				}
				if cards := s.workCards(); len(cards) != 0 {
					t.Fatalf("pending cards: %+v", cards)
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				for _, event := range f.told {
					if event.event == assistantwire.EventFleetInteraction {
						t.Fatal("read-only completion created a user approval card")
					}
				}
			})
		})
	}
}

func TestReadonlyPlanExitDoesNotSuppressOtherApprovals(t *testing.T) {
	for _, tc := range []struct {
		name                string
		readonly            bool
		machine, kind, tool string
	}{
		{"writable plan", false, "box", proto.IxPlan, "ExitPlanMode"},
		{"other machine", true, "other", proto.IxPlan, "ExitPlanMode"},
		{"tool permission", true, "box", proto.IxPermission, "Bash"},
		{"other plan tool", true, "box", proto.IxPlan, "OtherPlanTool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, _ := readonlyTaskService(t)
				task := readonlyTestTask("bypassPermissions")
				task.Readonly = tc.readonly
				if _, err := s.toolTaskPlan(context.Background(), "readonly-chat", readonlyTaskArgs(t, task)); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if !tc.readonly {
					plan := readonlyFeedPlan(t, s, "readonly-chat")
					if _, err := s.startPlan(context.Background(), plan.ID, nil, false); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
				}
				todo := onlyTodo(s)
				if len(todo.Agents) != 1 {
					t.Fatalf("task not dispatched: %+v", todo)
				}
				ix := proto.Interaction{ID: "approval", MachineID: tc.machine, SessionID: todo.Agents[0].SessionID,
					Kind: tc.kind, ToolName: tc.tool}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan proto.Resolution, 1)
				go func() { r, _ := s.nodeInteraction(ctx, ix); done <- r }()
				synctest.Wait()
				select {
				case r := <-done:
					t.Fatalf("approval was suppressed: %+v", r)
				default:
				}
				s.fleet.mu.Lock()
				_, pending := s.fleet.interactions[ix.ID]
				s.fleet.mu.Unlock()
				if !pending {
					t.Fatal("ordinary approval card was not registered")
				}
				approve := true
				if err := s.resolve(ix.ID, proto.Resolution{Approve: &approve}); err != nil {
					t.Fatal(err)
				}
				if r := <-done; r.Approve == nil || !*r.Approve {
					t.Fatalf("user approval lost: %+v", r)
				}
			})
		})
	}
}

func TestReadonlyTaskExitPlanModeAfterRestart(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%v", reuse), func(t *testing.T) {
			s, _ := newTaskServiceOn(t, database)
			task := readonlyTestTask("")
			conversation := fmt.Sprintf("readonly-restart-%v", reuse)
			if reuse {
				todo, err := s.createTodo(ctx, "Inspect", "", conversation, "")
				if err != nil {
					t.Fatal(err)
				}
				task.ReuseTodo = todo.ID
			}
			if _, err := s.toolTaskPlan(ctx, conversation, readonlyTaskArgs(t, task)); err != nil {
				t.Fatal(err)
			}
			plan := readonlyFeedPlan(t, s, conversation)
			var todo Todo
			waitFor(t, "read-only agent", func() bool {
				saved, err := s.loadPlan(ctx, plan.ID)
				if err != nil || saved.Tasks[0].TodoID == "" {
					return false
				}
				todo, _ = s.todoNow(saved.Tasks[0].TodoID)
				return len(todo.Agents) == 1
			})
			if reuse && (todo.ID != task.ReuseTodo || todo.PlanID != "") {
				t.Fatalf("expected the original reused to-do, got %+v", todo)
			}
			restarted, f := newTaskServiceOn(t, database)
			callCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			r, err := restarted.nodeInteraction(callCtx, proto.Interaction{ID: "restart-exit", MachineID: todo.Agents[0].MachineID,
				SessionID: todo.Agents[0].SessionID, Kind: proto.IxPlan, ToolName: "ExitPlanMode", Resumable: true})
			if err != nil || r.Approve == nil || *r.Approve || r.Dismiss || r.Mode != "" {
				t.Fatalf("persisted read-only plan exit = %+v, error = %v", r, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, event := range f.told {
				if event.event == assistantwire.EventFleetInteraction {
					t.Fatal("restart created an approval card")
				}
			}
		})
	}
}

// A topic can outlive several plans; only its current task may control plan approval.
func TestReusedWritableTaskExitPlanModeKeepsApproval(t *testing.T) {
	testReusedWritableTaskKeepsApproval(t, false)
}

func TestReusedWritableTaskReportKeepsApproval(t *testing.T) {
	testReusedWritableTaskKeepsApproval(t, true)
}

func testReusedWritableTaskKeepsApproval(t *testing.T, report bool) {
	for _, backend := range []string{"memory", "database", "restart"} {
		for _, retired := range []string{"released", "dismissed"} {
			t.Run(backend+"/"+retired, func(t *testing.T) {
				ctx := context.Background()
				var store Store
				if backend != "memory" {
					store = taskRecoveryDatabase(t)
				}
				s, f := newTaskServiceOn(t, store)
				f.ownModels = true
				setSettings(t, s, func(v *Settings) { v.Main = "reuse-chat" })
				e := &todoEnv{t: t, s: s, f: f}
				todo, err := s.createTodo(ctx, "Inspect and then fix", "", "reuse-chat", "")
				if err != nil {
					t.Fatal(err)
				}
				task := readonlyTestTask("")
				task.ReuseTodo = todo.ID
				if _, err := s.toolTaskPlan(ctx, "reuse-chat", readonlyTaskArgs(t, task)); err != nil {
					t.Fatal(err)
				}
				oldPlan := readonlyFeedPlan(t, s, "reuse-chat")
				waitFor(t, "readonly task dispatch", func() bool {
					cur, _ := s.todoNow(todo.ID)
					s.tasks.mu.Lock()
					defer s.tasks.mu.Unlock()
					return len(cur.Agents) == 1 && !s.tasks.reusing[todo.ID]
				})
				cur, _ := s.todoNow(todo.ID)
				oldAgent := cur.Agents[0]
				if retired == "dismissed" {
					e.must("DELETE", "/api/v1/todos/"+todo.ID+"/agents/"+oldAgent.MachineID+"/"+oldAgent.SessionID, "")
				} else {
					other := e.create("Another task")
					e.must("PUT", "/api/v1/todos/"+other.ID+"/agents/"+oldAgent.MachineID+"/"+oldAgent.SessionID, "")
				}
				task.Readonly, task.Permission = false, "bypassPermissions"
				result, err := s.toolTaskPlan(ctx, "reuse-chat", readonlyTaskArgs(t, task))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.startPlan(ctx, planIDRe.FindString(result), nil, false); err != nil {
					t.Fatal(err)
				}
				var agent TodoAgent
				waitFor(t, "writable task dispatch", func() bool {
					cur, _ := s.todoNow(todo.ID)
					for _, a := range cur.Agents {
						if a.active() && a.SessionID != oldAgent.SessionID {
							agent = a
							return true
						}
					}
					return false
				})
				cur, _ = s.todoNow(todo.ID)
				if cur.PlanID != "" {
					t.Fatalf("expected a reused topic to-do without PlanID, got %+v", cur)
				}
				if _, err := s.loadPlan(ctx, oldPlan.ID); err != nil {
					t.Fatalf("historical plan was lost: %v", err)
				}
				if backend == "restart" {
					s, _ = newTaskServiceOn(t, store)
				}
				callCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				ix := proto.Interaction{ID: "reused-writable-exit", MachineID: agent.MachineID,
					SessionID: agent.SessionID, Kind: proto.IxPlan, ToolName: "ExitPlanMode", Resumable: true}
				if report {
					ix.Kind, ix.ToolName = proto.IxPermission, "mcp__persona__report_to_supervisor"
				}
				done := make(chan proto.Resolution, 1)
				go func() {
					r, err := s.nodeInteraction(callCtx, ix)
					if err != nil {
						t.Error(err)
					}
					done <- r
				}()
				waitFor(t, "writable plan approval card", func() bool {
					select {
					case r := <-done:
						t.Fatalf("writable reused session was auto-resolved: %+v", r)
					default:
					}
					s.fleet.mu.Lock()
					defer s.fleet.mu.Unlock()
					return s.fleet.interactions[ix.ID] != nil
				})
				approve := true
				resolution := proto.Resolution{Approve: &approve}
				if report {
					resolution = proto.Resolution{Allow: &approve}
				}
				if err := s.resolve(ix.ID, resolution); err != nil {
					t.Fatal(err)
				}
				r := <-done
				got, _ := json.Marshal(r)
				want, _ := json.Marshal(resolution)
				if string(got) != string(want) {
					t.Fatalf("writable user approval was lost: %+v", r)
				}
				// A subsequent read-only takeover keeps its own completion behavior without a card.
				e.s = s
				e.must("DELETE", "/api/v1/todos/"+todo.ID+"/agents/"+agent.MachineID+"/"+agent.SessionID, "")
				task.Readonly = true
				if _, err := s.toolTaskPlan(ctx, "reuse-chat", readonlyTaskArgs(t, task)); err != nil {
					t.Fatal(err)
				}
				writableSession := agent.SessionID
				waitFor(t, "new readonly task dispatch", func() bool {
					cur, _ := s.todoNow(todo.ID)
					for _, a := range cur.Agents {
						if a.active() && a.SessionID != writableSession {
							agent = a
							return true
						}
					}
					return false
				})
				if backend == "restart" {
					s, _ = newTaskServiceOn(t, store)
				}
				denyCtx, stop := context.WithTimeout(ctx, time.Second)
				defer stop()
				ix.ID, ix.MachineID, ix.SessionID = "reused-readonly-exit", agent.MachineID, agent.SessionID
				r, err = s.nodeInteraction(denyCtx, ix)
				if report {
					raw, _ := json.Marshal(r)
					if err != nil || string(raw) != `{"allow":true}` {
						t.Fatalf("current readonly report was not allowed once: %s, error = %v", raw, err)
					}
				} else if err != nil || r.Approve == nil || *r.Approve || !strings.Contains(r.Feedback, "read-only") || r.Mode != "" {
					t.Fatalf("current readonly task lost its restriction: %+v, error = %v", r, err)
				}
				if cards := s.workCards(); len(cards) != 0 {
					t.Fatalf("current readonly task created approval cards: %+v", cards)
				}
			})
		}
	}
}
