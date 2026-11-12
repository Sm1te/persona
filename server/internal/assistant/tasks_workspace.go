package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

const (
	workspaceNew      = "new"
	workspaceExisting = "existing"
	workspaceNone     = "none"
	preferExisting    = "prefer-existing"
	preferNew         = "prefer-new"
)

// TaskWorkspace is a task-level policy, separate from its resolved checkout. The
// object variants leave room for future repository entries without changing cwd.
type TaskWorkspace struct {
	Mode     string
	Existing string
	Branch   string
}

func (w *TaskWorkspace) UnmarshalJSON(raw []byte) error {
	*w = TaskWorkspace{}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		if mode != workspaceNew && mode != workspaceNone {
			return fmt.Errorf("workspace must be new, {existing: cwd or workspace_id}, {new: {branch: name}}, or none")
		}
		w.Mode = mode
		return nil
	}
	var in struct {
		Existing string `json:"existing"`
		New      *struct {
			Branch string `json:"branch"`
		} `json:"new"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("invalid workspace: %w", err)
	}
	in.Existing = strings.TrimSpace(in.Existing)
	if in.Existing != "" && in.New == nil {
		w.Mode, w.Existing = workspaceExisting, in.Existing
		return nil
	}
	if in.New != nil && in.Existing == "" {
		w.Mode, w.Branch = workspaceNew, strings.TrimSpace(in.New.Branch)
		return nil
	}
	return fmt.Errorf("workspace must select exactly one of new, existing, or none")
}

func (w TaskWorkspace) MarshalJSON() ([]byte, error) {
	switch w.Mode {
	case workspaceExisting:
		return json.Marshal(map[string]string{"existing": w.Existing})
	case workspaceNew:
		if w.Branch != "" {
			return json.Marshal(map[string]any{"new": map[string]string{"branch": w.Branch}})
		}
	}
	return json.Marshal(w.Mode)
}

// TaskWorkspaceResolved is hub-owned. Before start Path is an engine preview;
// after creation it is the engine's answer. No client-supplied resolution is trusted.
type TaskWorkspaceResolved struct {
	Machine     string `json:"machine"`
	Path        string `json:"path"`
	Branch      string `json:"branch,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Created     bool   `json:"created,omitempty"`
	// Shared: the engine could not prepare a checkout for an inferred new workspace, so the task runs in a cwd
	// another active task also writes in, as every task did before workspaces existed.
	Shared bool `json:"shared,omitempty"`
}

func validWorkspacePreference(v string) bool {
	return v == preferExisting || v == preferNew
}

func (s *Service) taskWorkspacePreference(ctx context.Context) string {
	if s.settings(ctx).TaskWorkspacePreference == preferNew {
		return preferNew
	}
	return preferExisting
}

// resolveTaskWorkspace is metadata-only: it must never create a checkout on a
// waiting card. Re-resolving after an edit or a machine change asks that machine.
func (s *Service) resolveTaskWorkspace(ctx context.Context, t *PlanTask) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if t.ReservedTodoID == "" {
		t.ReservedTodoID = firstNonEmpty(t.ReuseTodo, t.TodoID, "todo_"+randomHex(8))
	}
	var sourceCwd string // the inferred cwd's workspace, and whether another task writes there
	var busy bool
	if t.Workspace == nil || t.WorkspaceInferred {
		mode := workspaceNone
		if !t.Readonly {
			var st proto.WorktreeStatus
			if err := s.call(ctx, t.Machine, "worktree.status", map[string]any{"cwd": t.Cwd}, &st); err != nil {
				return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-tasks_workspace.infer", i18n.Args{"machine": t.Machine}), err)
			}
			if st.Path != "" {
				mode = workspaceExisting
				var source proto.Workspace
				if err := s.call(ctx, t.Machine, "workspace.resolve", map[string]any{"path": t.Cwd}, &source); err != nil {
					return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-tasks_workspace.resolve", i18n.Args{"path": t.Cwd}), err)
				}
				if !source.Available || !path.IsAbs(source.Cwd) {
					return i18n.ErrEnglish("assistant-tasks_workspace.unavailable", i18n.Args{"path": t.Cwd})
				}
				sourceCwd, busy = source.Cwd, s.taskWorkspaceOwner(ctx, t.Machine, source.Cwd, t.TodoID) != nil
				if s.taskWorkspacePreference(ctx) == preferNew || busy {
					mode = workspaceNew
				}
			}
		}
		t.Workspace = &TaskWorkspace{Mode: mode}
		if mode == workspaceExisting {
			t.Workspace.Existing = t.Cwd
		}
		t.WorkspaceInferred = true
	}
	res := &TaskWorkspaceResolved{Machine: t.Machine, Path: path.Clean(t.Cwd)}
	switch t.Workspace.Mode {
	case workspaceNone:
	case workspaceExisting:
		selected := t.Workspace.Existing
		if selected == "" {
			return fmt.Errorf("workspace.existing requires a cwd or workspace_id")
		}
		if m, ok := s.machineByName(ctx, t.Machine); ok {
			selected = proto.ExpandHome(selected, m.Home)
		}
		if !path.IsAbs(selected) {
			var list proto.WorkspacesResponse
			if err := s.call(ctx, t.Machine, "workspaces.list", map[string]any{}, &list); err != nil {
				return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-tasks_workspace.list", i18n.Args{"machine": t.Machine}), err)
			}
			var found bool
			for _, w := range list.Workspaces {
				qualified := qualifyWorkspace(t.Machine, w)
				if w.ID == selected || qualified.ID == selected {
					if !w.Available || w.Cwd == "" {
						return i18n.ErrEnglish("assistant-tasks_workspace.catalog_unavailable", i18n.Args{"workspace": selected, "machine": t.Machine})
					}
					selected, res.WorkspaceID, res.Branch = w.Cwd, w.ID, w.Branch
					found = true
					break
				}
			}
			if !found {
				return i18n.ErrEnglish("assistant-tasks_workspace.not_found", i18n.Args{"workspace": selected, "machine": t.Machine})
			}
		}
		var resolved proto.Workspace
		if err := s.call(ctx, t.Machine, "workspace.resolve", map[string]any{"path": selected}, &resolved); err != nil {
			return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-tasks_workspace.resolve", i18n.Args{"path": selected}), err)
		}
		if !resolved.Available || !path.IsAbs(resolved.Cwd) {
			return i18n.ErrEnglish("assistant-tasks_workspace.unavailable", i18n.Args{"path": selected})
		}
		res.Path = path.Clean(resolved.Cwd)
	case workspaceNew:
		res.Branch = firstNonEmpty(t.Workspace.Branch, "task/"+firstNonEmpty(t.TodoID, t.ReservedTodoID))
		var prep proto.WorktreePrepare
		if err := s.call(ctx, t.Machine, DeviceWorktreePrepare, map[string]any{"cwd": t.Cwd}, &prep); err != nil {
			if t.WorkspaceInferred && sourceCwd != "" && engineCannotPrepare(err) {
				// An engine without checkouts (Paseo, Cindy, or a host without checkout support) runs the task in its cwd,
				// as tasks did before workspaces: a busy cwd is shared rather than claimed, a free one is reused.
				res.Branch = ""
				if busy {
					t.Workspace, res.Shared = &TaskWorkspace{Mode: workspaceNone}, true
				} else {
					t.Workspace, res.Path = &TaskWorkspace{Mode: workspaceExisting, Existing: t.Cwd}, path.Clean(sourceCwd)
				}
				break
			}
			return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-tasks_workspace.prepare", i18n.Args{"machine": t.Machine}), err)
		}
		if !path.IsAbs(prep.Path) {
			return i18n.ErrEnglish("assistant-tasks_workspace.engine_path")
		}
		res.Path = path.Join(prep.Path, herdr.WorktreeDirName(res.Branch))
	default:
		return fmt.Errorf("unknown task workspace policy %q", t.Workspace.Mode)
	}
	t.WorkspaceResolved = res
	return nil
}

// engineCannotPrepare: the machine's engine has no checkouts to prepare (a runtime such as Paseo or Cindy, or a host
// without checkout support), as the node's error code says; any other failure, a missing host included, stays an error.
func engineCannotPrepare(err error) bool {
	var re *rpc.Error
	return errors.As(err, &re) && re.Code == proto.CodeWorktreeUnsupported
}

// taskWorkspaceOwner checks durable active task links, and the gap between
// admission and linking. Callers that admit a writer hold workspaceMu as well.
func (s *Service) taskWorkspaceOwner(ctx context.Context, machine, cwd, except string) *Todo {
	machine, cwd = s.effectiveBoardMachine(machine), path.Clean(cwd)
	s.tasks.mu.Lock()
	owner := s.tasks.workspaceClaims[machine+"\x00"+cwd]
	s.tasks.mu.Unlock()
	if owner != "" && owner != except {
		if t, ok := s.todoNow(owner); ok {
			return &t
		}
	}
	for _, t := range s.todosOf(ctx, "*") {
		if t.ID == except || t.DoneAt != nil {
			continue
		}
		for _, a := range t.Agents {
			if a.ReleasedAt == nil && a.DismissedAt == nil && s.effectiveBoardMachine(a.MachineID) == machine && path.Clean(a.Cwd) == cwd {
				return &t
			}
		}
	}
	return nil
}

func (s *Service) taskWorkspaceBusy(ctx context.Context, machine, cwd, except string) error {
	if owner := s.taskWorkspaceOwner(ctx, machine, cwd, except); owner != nil {
		return i18n.ErrEnglish("assistant-tasks_workspace.busy", i18n.Args{"id": owner.ID, "title": owner.Title, "machine": machine, "path": cwd})
	}
	return nil
}

// admitTaskWorkspace resolves at the final placement, creates through the same
// engine as agent_start and claims the actual cwd before any session can start.
func (s *Service) admitTaskWorkspace(ctx context.Context, t *PlanTask) error {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	s.tasks.workspaceMu.Lock()
	defer s.tasks.workspaceMu.Unlock()
	created := t.WorkspaceResolved != nil && t.WorkspaceResolved.Created
	if created && t.WorkspaceResolved.Machine != t.Machine {
		return i18n.ErrEnglish("assistant-tasks_workspace.machine_local", i18n.Args{"from": t.WorkspaceResolved.Machine, "to": t.Machine})
	}
	if !created {
		if err := s.resolveTaskWorkspace(ctx, t); err != nil {
			return err
		}
		if t.Workspace.Mode == workspaceNew {
			wt, err := s.newWorktree(ctx, t.Machine, t.Cwd, t.WorkspaceResolved.Branch)
			if err != nil {
				return fmt.Errorf("%s: %w", i18n.T(s.lang(ctx), "assistant-tasks_workspace.create"), err)
			}
			if !path.IsAbs(wt.Path) {
				return i18n.ErrEnglish("assistant-tasks_workspace.engine_path")
			}
			t.WorkspaceResolved = &TaskWorkspaceResolved{Machine: t.Machine, Path: path.Clean(wt.Path),
				Branch: firstNonEmpty(wt.Branch, t.WorkspaceResolved.Branch), WorkspaceID: wt.WorkspaceID, Created: true}
			// Once created it is machine-local, including automatic and explicit retries.
			t.MachinePolicy = machineFixed
		}
	}
	t.Cwd = t.WorkspaceResolved.Path
	if t.Workspace.Mode != workspaceNone {
		if err := s.taskWorkspaceBusy(ctx, t.Machine, t.Cwd, t.TodoID); err != nil {
			return err
		}
		if t.TodoID != "" {
			s.tasks.mu.Lock()
			if s.tasks.workspaceClaims == nil {
				s.tasks.workspaceClaims = map[string]string{}
			}
			s.tasks.workspaceClaims[s.effectiveBoardMachine(t.Machine)+"\x00"+t.Cwd] = t.TodoID
			s.tasks.mu.Unlock()
		}
	}
	// A shared fallback has mode none, but its resolved marker must still survive the start.
	if t.planID != "" && (t.Workspace.Mode != workspaceNone || t.WorkspaceResolved != nil && t.WorkspaceResolved.Shared) {
		// Persist the engine's answer before starting, so a retry never creates a second tree.
		return s.patchPlanTask(ctx, t.planID, t.planIndex, func(pt *PlanTask) {
			pt.Cwd, pt.Workspace, pt.WorkspaceResolved = t.Cwd, t.Workspace, t.WorkspaceResolved
			pt.WorkspaceInferred, pt.ReservedTodoID, pt.MachinePolicy = t.WorkspaceInferred, t.ReservedTodoID, t.MachinePolicy
		})
	}
	return nil
}

func (s *Service) releaseTaskWorkspace(todoID string) {
	s.tasks.mu.Lock()
	defer s.tasks.mu.Unlock()
	for key, owner := range s.tasks.workspaceClaims {
		if owner == todoID {
			delete(s.tasks.workspaceClaims, key)
		}
	}
}

// Preview uses the saved task as its trusted base, applying only card-editable
// fields. It neither changes the saved plan nor allocates a host workspace.
func (s *Service) previewPlanWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, fmt.Errorf("read-only role cannot edit task workspaces"))
		return
	}
	p, err := s.loadPlan(r.Context(), r.PathValue("id"))
	if err != nil {
		planErr(w, err)
		return
	}
	if p.State != planOpen {
		planErr(w, errPlanNotOpen)
		return
	}
	var in struct {
		Tasks []PlanTask `json:"tasks"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	for i := range p.Tasks {
		if i < len(in.Tasks) {
			applyPlanEdit(&p.Tasks[i], in.Tasks[i])
		}
		if !s.placeTask(r.Context(), &p.Tasks[i]) {
			apierr.Write(w, http.StatusBadRequest, fmt.Errorf("unknown machine %s", p.Tasks[i].Machine))
			return
		}
		if err := s.resolveTaskWorkspace(r.Context(), &p.Tasks[i]); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
	}
	apierr.WriteJSON(w, map[string]any{"plan": p})
}
