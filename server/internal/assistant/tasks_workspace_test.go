package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func workspaceTaskService(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	s, f := readonlyTaskService(t)
	f.nodes = map[string]nodeAnswer{
		"worktree.status": func(string, json.RawMessage) (any, error) {
			return proto.WorktreeStatus{Path: "/code/app", Branch: "main"}, nil
		},
		"workspace.resolve": func(_ string, raw json.RawMessage) (any, error) {
			var in struct{ Path string }
			_ = json.Unmarshal(raw, &in)
			return proto.Workspace{Cwd: strings.TrimSuffix(in.Path, "/"), Available: true}, nil
		},
		"workspaces.list": func(string, json.RawMessage) (any, error) {
			return proto.WorkspacesResponse{Workspaces: []proto.Workspace{
				{ID: "ws-one", Source: "herdr", SourceID: "ws-one", Cwd: "/code/other", Branch: "fix/login", Available: true},
			}}, nil
		},
	}
	return s, f
}

func workspacePlan(t *testing.T, s *Service, choice string) *TaskPlan {
	t.Helper()
	args := `{"tasks":[{"title":"Fix login","prompt":"Fix login and add tests","machine":"box","cwd":"/code/app","permission":"bypassPermissions"`
	if choice != "" {
		args += `,"workspace":` + choice
	}
	_, err := s.toolTaskPlan(context.Background(), "workspace-chat", json.RawMessage(args+`}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return readonlyFeedPlan(t, s, "workspace-chat")
}

func TestTaskWorkspaceNewUsesEngineAndPersistsActualCwd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		p := workspacePlan(t, s, `"new"`)
		if len(f.starts) != 0 {
			t.Fatal("preview started an agent")
		}
		p, err := s.startPlan(context.Background(), p.ID, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		wantBranch := "task/" + p.Tasks[0].TodoID
		if len(f.worktrees) < 2 {
			t.Fatalf("task did not use the machine engine: %v", f.worktrees)
		}
		create := f.worktrees[len(f.worktrees)-1]
		if create["branch"] != wantBranch || len(f.starts) != 1 || f.starts[0]["workspace_id"] != "ws-new" {
			t.Fatalf("create=%v starts=%v; want branch %s", create, f.starts, wantBranch)
		}
		path := create["path"].(string)
		if !strings.HasPrefix(path, "/wt/app/") || saved.Tasks[0].Cwd != path || onlyTodo(s).Agents[0].Cwd != path {
			t.Fatalf("actual path lost: plan=%+v todo=%+v", saved, onlyTodo(s))
		}
	})
}

func TestTaskWorkspaceCreateFailureStartsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		f.failWorktree = errors.New("branch already checked out")
		p := workspacePlan(t, s, `"new"`)
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		if len(f.starts) != 0 || !strings.Contains(saved.Tasks[0].Error, "branch already checked out") || onlyTodo(s).StartError == "" {
			t.Fatalf("failed creation was not a dispatch failure: starts=%v plan=%+v", f.starts, saved)
		}
	})
}

func TestTaskWorkspaceUsesReturnedEnginePathNotPreview(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		f.nodes[DeviceWorktreeCreate] = func(_ string, raw json.RawMessage) (any, error) {
			var in struct{ Branch string }
			_ = json.Unmarshal(raw, &in)
			return proto.WorktreeCreateResult{Path: "/engine/actual-checkout", Branch: in.Branch, WorkspaceID: "engine-id"}, nil
		}
		p := workspacePlan(t, s, `"new"`)
		if p.Tasks[0].WorkspaceResolved.Path == "/engine/actual-checkout" {
			t.Fatal("fixture preview must differ from the final engine answer")
		}
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		if saved.Tasks[0].Cwd != "/engine/actual-checkout" || onlyTodo(s).Agents[0].Cwd != "/engine/actual-checkout" ||
			f.starts[0]["cwd"] != "/engine/actual-checkout" || f.starts[0]["workspace_id"] != "engine-id" {
			t.Fatalf("engine answer lost: plan=%+v starts=%v", saved, f.starts)
		}
	})
}

func TestTaskWorkspaceOwnershipIsMachineLocalAndOnlyActiveLinks(t *testing.T) {
	s, _ := workspaceTaskService(t)
	ctx := context.Background()
	owner, _ := s.createTodo(ctx, "Writer", "", "other-chat", "")
	if err := s.linkTodoAgent(ctx, owner.ID, "box", "writer", "", "/code/app", ""); err != nil {
		t.Fatal(err)
	}
	if s.taskWorkspaceOwner(ctx, "another-machine", "/code/app", "") != nil ||
		s.taskWorkspaceOwner(ctx, "box", "/code/other", "") != nil ||
		s.taskWorkspaceOwner(ctx, "box", "/code/app", owner.ID) != nil {
		t.Fatal("ownership leaked across machine, path, or the same task")
	}
	if s.taskWorkspaceOwner(ctx, "box", "/code/app/", "") == nil {
		t.Fatal("active canonical path was not occupied")
	}
	s.todos.mu.Lock()
	s.todos.todos[owner.ID].Agents[0].ReleasedAt = &owner.CreatedAt
	s.todos.mu.Unlock()
	if s.taskWorkspaceOwner(ctx, "box", "/code/app", "") != nil {
		t.Fatal("released task still occupies the checkout")
	}
}

func TestTaskWorkspaceExistingBusyRefusesWithOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		ctx := context.Background()
		owner, err := s.createTodo(ctx, "Existing writer", "", "other-chat", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.linkTodoAgent(ctx, owner.ID, "box", "writer", owner.Title, "/code/app", ""); err != nil {
			t.Fatal(err)
		}
		p := workspacePlan(t, s, `{"existing":"/code/app/"}`)
		_, err = s.startPlan(ctx, p.ID, nil, false)
		synctest.Wait()
		saved, _ := s.loadPlan(ctx, p.ID)
		reason := saved.Tasks[0].Error
		if err != nil {
			reason = err.Error()
		}
		if len(f.starts) != 0 || !strings.Contains(reason, owner.ID) || !strings.Contains(reason, owner.Title) {
			t.Fatalf("busy workspace was not refused with owner: err=%v plan=%+v starts=%v", err, saved, f.starts)
		}
	})
}

func TestTaskWorkspaceNoneDoesNotCreate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		p := workspacePlan(t, s, `"none"`)
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if len(f.worktrees) != 0 || len(f.starts) != 1 || f.starts[0]["cwd"] != "/code/app" {
			t.Fatalf("none created a workspace: %v %v", f.worktrees, f.starts)
		}
	})
}

func TestTaskWorkspaceDefaultsAndPreference(t *testing.T) {
	for _, tc := range []struct {
		name, preference, explicit, want string
		readonly, git, busy              bool
	}{
		{name: "default reuses free cwd", git: true, want: `"existing":"/code/app"`},
		{name: "prefer new isolates", preference: preferNew, git: true, want: `"workspace":"new"`},
		{name: "busy defaults new", git: true, busy: true, want: `"workspace":"new"`},
		{name: "readonly wins preference", preference: preferNew, git: true, readonly: true, want: `"workspace":"none"`},
		{name: "non git needs none", preference: preferNew, want: `"workspace":"none"`},
		{name: "explicit existing wins", preference: preferNew, git: true, explicit: `{"existing":"/code/other"}`, want: `"existing":"/code/other"`},
		{name: "explicit none wins", preference: preferNew, git: true, explicit: `"none"`, want: `"workspace":"none"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := workspaceTaskService(t)
			ctx := context.Background()
			if tc.preference != "" {
				setSettings(t, s, func(v *Settings) { v.TaskWorkspacePreference = tc.preference })
			}
			if !tc.git {
				f.nodes["worktree.status"] = func(string, json.RawMessage) (any, error) { return proto.WorktreeStatus{}, nil }
			}
			if tc.busy {
				owner, _ := s.createTodo(ctx, "writer", "", "owner-chat", "")
				if err := s.linkTodoAgent(ctx, owner.ID, "box", "writer", "", "/code/app", ""); err != nil {
					t.Fatal(err)
				}
			}
			task := PlanTask{Machine: "box", Cwd: "/code/app", Readonly: tc.readonly}
			if tc.explicit != "" {
				if err := json.Unmarshal([]byte(tc.explicit), &task.Workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.resolveTaskWorkspace(ctx, &task); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(task)
			if !strings.Contains(string(raw), tc.want) {
				t.Fatalf("got %s, want %s", raw, tc.want)
			}
		})
	}
}

func TestTaskWorkspacePreferenceIsRecheckedWhenDispatching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		p := workspacePlan(t, s, "")
		if p.Tasks[0].Workspace.Mode != workspaceExisting {
			t.Fatal("default preference did not reuse the free checkout")
		}
		setSettings(t, s, func(v *Settings) { v.TaskWorkspacePreference = preferNew })
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		if !saved.Tasks[0].WorkspaceResolved.Created || len(f.starts) != 1 || f.starts[0]["cwd"] == "/code/app" {
			t.Fatalf("dispatch ignored the saved preference: plan=%+v starts=%v", saved, f.starts)
		}
	})
}

func TestTaskWorkspaceExistingCatalogIDsAndCanonicalPath(t *testing.T) {
	for _, id := range []string{"ws-one", qualifyWorkspace("box", proto.Workspace{ID: "ws-one", Source: "herdr", SourceID: "ws-one"}).ID} {
		t.Run(id, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := workspaceTaskService(t)
				p := workspacePlan(t, s, `{"existing":"`+id+`"}`)
				if p.Tasks[0].WorkspaceResolved.Path != "/code/other" {
					t.Fatalf("catalog ID did not resolve: %+v", p.Tasks[0])
				}
				if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				if len(f.starts) != 1 || f.starts[0]["cwd"] != "/code/other" || f.starts[0]["workspace_id"] != "ws-one" {
					t.Fatalf("catalog selection lost: %v", f.starts)
				}
				if len(f.worktrees) != 0 {
					t.Fatalf("existing created a tree: %v", f.worktrees)
				}
			})
		})
	}
}

func TestTaskWorkspacePreviewEditsAreReadOnlyAndServerOwned(t *testing.T) {
	s, f := workspaceTaskService(t)
	p := workspacePlan(t, s, `"none"`)
	body := `{"tasks":[{"workspace":{"new":{"branch":"fix/Login"}},"reserved_todo_id":"forged","workspace_resolved":{"path":"/forged","created":true}}]}`
	w := request(s, http.MethodPost, "/api/v1/task-plans/"+p.ID+"/workspace-preview", body)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"path":"/wt/app/fix-login"`) || strings.Contains(w.Body.String(), "forged") {
		t.Fatalf("preview = %d %s", w.Code, w.Body.String())
	}
	saved, _ := s.loadPlan(context.Background(), p.ID)
	if saved.Tasks[0].Workspace.Mode != workspaceNone || len(f.starts) != 0 {
		t.Fatalf("preview mutated plan: %+v starts=%v", saved, f.starts)
	}
	for _, call := range f.worktrees {
		if call["method"] == DeviceWorktreeCreate {
			t.Fatalf("preview created a workspace: %v", f.worktrees)
		}
	}
	// Invalid choices remain editable; Start cannot get past validation either.
	for _, choice := range []string{`"invalid"`, `{}`, `{"existing":""}`, `{"existing":"x","new":{}}`} {
		w := request(s, http.MethodPost, "/api/v1/task-plans/"+p.ID+"/start", `{"tasks":[{"workspace":`+choice+`}]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", choice, w.Code, w.Body.String())
		}
	}
}

func TestTaskWorkspaceRetryKeepsCreatedCheckout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		f.failStart = errors.New("launcher failed")
		p := workspacePlan(t, s, `{"new":{"branch":"fix/Login"}}`)
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		if !saved.Tasks[0].WorkspaceResolved.Created {
			t.Fatalf("checkout was not recorded after failed launch: %+v", saved)
		}
		f.failStart = nil
		if _, err := s.retryTask(context.Background(), saved.Tasks[0].TodoID); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		creates := 0
		for _, call := range f.worktrees {
			if call["method"] == DeviceWorktreeCreate {
				creates++
			}
		}
		if creates != 1 || len(f.starts) != 2 || f.starts[1]["cwd"] != "/wt/app/fix-login" || len(onlyTodo(s).Agents) != 1 {
			t.Fatalf("retry made another checkout or lost cwd: creates=%d starts=%v", creates, f.starts)
		}
	})
}

func TestTaskWorkspaceConcurrentExistingStartsCannotBothWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		args := `{"tasks":[
			{"title":"Writer one","prompt":"Write one","machine":"box","cwd":"/code/app","permission":"bypassPermissions","workspace":{"existing":"/code/app"}},
			{"title":"Writer two","prompt":"Write two","machine":"box","cwd":"/code/app","permission":"bypassPermissions","workspace":{"existing":"/code/app"}}
		]}`
		if _, err := s.toolTaskPlan(context.Background(), "workspace-chat", json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
		p := readonlyFeedPlan(t, s, "workspace-chat")
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		failures := 0
		for _, task := range saved.Tasks {
			if strings.Contains(task.Error, "occupied by task") {
				failures++
			}
		}
		if failures != 1 || len(f.starts) != 1 {
			t.Fatalf("writers were not serialized: starts=%v plan=%+v", f.starts, saved)
		}
	})
}

func TestTaskWorkspaceSettingsHTTPAndApproval(t *testing.T) {
	s, f := workspaceTaskService(t)
	f.settings = nil
	s = newTestService(t, f)
	if s.view(context.Background(), false).TaskWorkspacePreference != preferExisting {
		t.Fatal("default is not prefer-existing")
	}
	for _, v := range []string{preferNew, preferExisting} {
		w := request(s, http.MethodPut, "/api/v1/assistant", `{"task_workspace_preference":"`+v+`"}`)
		if w.Code != http.StatusOK || s.taskWorkspacePreference(context.Background()) != v {
			t.Fatalf("PUT %s: %d %s", v, w.Code, w.Body.String())
		}
	}
	w := request(s, http.MethodPut, "/api/v1/assistant", `{"task_workspace_preference":"invented-root"}`)
	if w.Code != http.StatusBadRequest || s.taskWorkspacePreference(context.Background()) != preferExisting {
		t.Fatalf("invalid preference changed settings: %d %s", w.Code, w.Body.String())
	}
	change, err := s.setTaskWorkspacePreference(context.Background(), json.RawMessage(`"prefer-new"`))
	if err != nil {
		t.Fatal(err)
	}
	if s.taskWorkspacePreference(context.Background()) != preferExisting {
		t.Fatal("tool changed preference before approval")
	}
	if _, err := change.apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	task := PlanTask{Machine: "box", Cwd: "/code/app"}
	if err := s.resolveTaskWorkspace(context.Background(), &task); err != nil || task.Workspace.Mode != workspaceNew {
		t.Fatalf("saved preference did not affect dispatch: %+v, %v", task, err)
	}
}

// cannotPrepare is the node's answer to worktree.prepare on an engine without checkouts, as it crosses the wire:
// the default engine is a runtime such as Paseo (no terminal host on the node) or a host without checkout support.
func cannotPrepare(kind string) error {
	return &rpc.Error{Code: "worktree_unsupported", Message: "unsupported: " + kind + " cannot do this"}
}

// busyCwdService is a machine whose worktree.prepare answers prepareErr (nil: it prepares checkouts), with /code/app
// already written in by another task.
func busyCwdService(t *testing.T, prepareErr error) (*Service, *fakeHub) {
	t.Helper()
	s, f := workspaceTaskService(t)
	if prepareErr != nil {
		f.nodes[DeviceWorktreePrepare] = func(string, json.RawMessage) (any, error) { return nil, prepareErr }
	}
	owner, err := s.createTodo(context.Background(), "Other writer", "", "other-chat", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.linkTodoAgent(context.Background(), owner.ID, "box", "writer", owner.Title, "/code/app", ""); err != nil {
		t.Fatal(err)
	}
	return s, f
}

func TestTaskWorkspaceInferredFallsBackToCwdWhenEngineCannotPrepare(t *testing.T) {
	for _, kind := range []string{"paseo", "herdr-core"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, f := busyCwdService(t, cannotPrepare(kind))
				p := workspacePlan(t, s, "")
				if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				saved, _ := s.loadPlan(context.Background(), p.ID)
				task := saved.Tasks[0]
				if task.Error != "" || len(f.starts) != 1 || f.starts[0]["cwd"] != "/code/app" {
					t.Fatalf("inferred task did not start in its cwd: error=%q starts=%v", task.Error, f.starts)
				}
				if task.Workspace.Mode != workspaceNone || task.WorkspaceResolved == nil || !task.WorkspaceResolved.Shared ||
					task.WorkspaceResolved.Path != "/code/app" || task.WorkspaceResolved.Created {
					t.Fatalf("fallback not recorded as a shared cwd: workspace=%+v resolved=%+v", task.Workspace, task.WorkspaceResolved)
				}
				for _, call := range f.worktrees {
					if call["method"] == DeviceWorktreeCreate {
						t.Fatalf("created a checkout on an engine that cannot: %v", f.worktrees)
					}
				}
			})
		})
	}
}

func TestTaskWorkspaceSharedFallbackPersistsWhenBusyAppearsAtAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := workspaceTaskService(t)
		f.nodes[DeviceWorktreePrepare] = func(string, json.RawMessage) (any, error) {
			return nil, cannotPrepare("paseo")
		}
		args := `{"tasks":[
			{"title":"Writer one","prompt":"Write one","machine":"box","cwd":"/code/app","permission":"bypassPermissions"},
			{"title":"Writer two","prompt":"Write two","machine":"box","cwd":"/code/app","permission":"bypassPermissions"}
		]}`
		if _, err := s.toolTaskPlan(context.Background(), "workspace-chat", json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
		p := readonlyFeedPlan(t, s, "workspace-chat")
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		if len(f.starts) != 2 {
			t.Fatalf("starts=%v", f.starts)
		}
		shared := 0
		for i, task := range saved.Tasks {
			t.Logf("saved task %d: mode=%s resolved=%+v", i, task.Workspace.Mode, task.WorkspaceResolved)
			if task.WorkspaceResolved != nil && task.WorkspaceResolved.Shared {
				shared++
				if task.Workspace.Mode != workspaceNone {
					t.Fatalf("shared task is not none: %+v", task)
				}
			}
		}
		if shared != 1 {
			t.Fatalf("one task actually shared the cwd, but saved plans mark %d shared: tasks=%+v", shared, saved.Tasks)
		}
	})
}

func TestTaskWorkspaceInferredPreferNewFallsBackToFreeCwd(t *testing.T) {
	s, f := workspaceTaskService(t)
	f.nodes[DeviceWorktreePrepare] = func(string, json.RawMessage) (any, error) { return nil, cannotPrepare("paseo") }
	setSettings(t, s, func(v *Settings) { v.TaskWorkspacePreference = preferNew })
	task := PlanTask{Machine: "box", Cwd: "/code/app"}
	if err := s.resolveTaskWorkspace(context.Background(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Workspace.Mode != workspaceExisting || task.Workspace.Existing != "/code/app" ||
		task.WorkspaceResolved.Path != "/code/app" || task.WorkspaceResolved.Shared {
		t.Fatalf("free cwd not reused: workspace=%+v resolved=%+v", task.Workspace, task.WorkspaceResolved)
	}
}

// Only the node's "cannot" code falls back: any other failure to prepare (a host missing from the machine, an old node
// that sends no code) still stops the task rather than silently sharing a directory.
func TestTaskWorkspaceInferredOtherPrepareFailuresStillFail(t *testing.T) {
	for _, prepareErr := range []error{errors.New("no orca host on this machine"), errors.New("unsupported: herdr-core cannot do this")} {
		s, f := workspaceTaskService(t)
		f.nodes[DeviceWorktreePrepare] = func(string, json.RawMessage) (any, error) { return nil, prepareErr }
		setSettings(t, s, func(v *Settings) { v.TaskWorkspacePreference = preferNew })
		task := PlanTask{Machine: "box", Cwd: "/code/app"}
		if err := s.resolveTaskWorkspace(context.Background(), &task); err == nil || !strings.Contains(err.Error(), prepareErr.Error()) {
			t.Fatalf("%v: fell back instead of failing: err=%v workspace=%+v", prepareErr, err, task.Workspace)
		}
	}
}

func TestTaskWorkspaceExplicitNewStillFailsWhenEngineCannotPrepare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := busyCwdService(t, cannotPrepare("paseo"))
		args := `{"tasks":[{"title":"Fix login","prompt":"Fix login and add tests","machine":"box","cwd":"/code/app","permission":"bypassPermissions","workspace":"new"}]}`
		_, err := s.toolTaskPlan(context.Background(), "workspace-chat", json.RawMessage(args))
		reason := ""
		if err != nil {
			reason = err.Error()
		} else {
			p := readonlyFeedPlan(t, s, "workspace-chat")
			if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
				reason = err.Error()
			}
			synctest.Wait()
			saved, _ := s.loadPlan(context.Background(), p.ID)
			reason += saved.Tasks[0].Error
		}
		if len(f.starts) != 0 || !strings.Contains(reason, "unsupported: paseo cannot do this") {
			t.Fatalf("explicit new did not refuse: reason=%q starts=%v", reason, f.starts)
		}
	})
}

func TestTaskWorkspaceInferredBusyStillCreatesWhenEngineCanPrepare(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, f := busyCwdService(t, nil)
		p := workspacePlan(t, s, "")
		if _, err := s.startPlan(context.Background(), p.ID, nil, false); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		saved, _ := s.loadPlan(context.Background(), p.ID)
		task := saved.Tasks[0]
		if task.Workspace.Mode != workspaceNew || !task.WorkspaceResolved.Created || task.WorkspaceResolved.Shared ||
			!strings.HasPrefix(task.WorkspaceResolved.Path, "/wt/app/") || len(f.starts) != 1 || f.starts[0]["cwd"] != task.WorkspaceResolved.Path {
			t.Fatalf("supported engine lost its new checkout: workspace=%+v resolved=%+v starts=%v", task.Workspace, task.WorkspaceResolved, f.starts)
		}
	})
}
