package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Tools only the assistant (the "Chat" tab's headless Claude) gets: asking on the phone (headless claude has no
// AskUserQuestion), pushing, waking itself up later, its conversations, the tasks it hands out, the coding agents the
// hub already tracks, the knowledge base, the inbox's drafts, Feishu's /agent replies and Persona's own settings.
// The hub lists them with its own and sends their calls here (Handler.Tool). Those on the hub's knowledge base,
// interaction queue and inbox call those apps' data methods (tools_apps.go); one that is still an app's to run goes to
// that app (AppTool).

// toolNames are the tools only assistant conversations get; their descriptions and schemas are in prompts/mcp
// (docs/assistant/prompts.md).
var toolNames = []string{"ask_user", "notify_user", "supervision_finish", "schedule_followup", "schedule_routine", "delete_routine", "list_followups",
	"cancel_followup", "list_places", "save_place", "machines_list", "agents_list", "agent_read", "agent_send", "agent_interrupt", "agent_start", "agent_supervise", "agent_archive", "agent_unarchive", "interaction_answer", "ask_escalate", "inbox_suggest", "inbox_draft",
	"inbox_folders", "inbox_move", "kb_search", "kb_list", "kb_get", "kb_update", "kb_history_plan", "kb_history_status", "kb_history_stop", "kb_history_resume", "persona_settings", "secret_run",
	"model_profiles_list", "model_profile_get", "model_profile_switch", "model_profile_rollback",
	"conversations_list", "conversation_read", "conversation_rename", "conversation_archive", "conversation_unarchive",
	"conversation_new", "conversation_send", "task_plan", "task_review", "task_scope", "task_start_screen", "task_start_keys", "task_retry",
	"todo_list", "todo_get", "todo_create", "todo_update", "todo_complete", "todo_reopen", "todo_delete", "concern_report",
	"direction_name", "leader_progress"}

// Tools are the assistant's tools (assistant.tools).
func (s *Service) Tools(ctx context.Context) []proto.Tool {
	names := append([]string(nil), toolNames...)
	if s.hello(ctx).PersonaView {
		names = append(names, "view_show", "view_update", "view_get")
	}
	return toolsNamed(names)
}

// toolsNamed are the MCP tools of these names, as prompts/mcp has them.
func toolsNamed(names []string) []proto.Tool {
	tools := make([]proto.Tool, 0, len(names))
	for _, n := range names {
		t := prompts.Tool(n)
		tools = append(tools, proto.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return tools
}

// Tool runs one of the assistant's MCP tools for the conversation call.SessionID on machine call.MachineID.
func (s *Service) Tool(ctx context.Context, call ToolCall) proto.ToolResult {
	call.Name = compat.Tool(call.Name)
	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if res, refused := s.meetingRefuses(ctx, call.MachineID, call.SessionID, call.Name, true); refused {
		return res // a meeting's batch started this turn: only reading tools (meeting_turns.go)
	}
	switch call.Name {
	case "persona_settings":
		return s.toolPersonaSettings(ctx, call, args) // settings_tool.go
	case "task_plan", "agent_start":
		// A task's agent does the work itself (supervise_leader.go); the hub's own starts (an inbox hand-off) are not
		// a model's call and come through runTool.
		if err := s.workerRefusal(ctx, call.MachineID, call.SessionID); err != nil {
			return proto.TextResult(err.Error(), true)
		}
	}
	// A model's call says which conversation asked (task_brief.go callerOrigin); the hub's own runTool calls do not.
	res, err := s.runTool(withToolCaller(ctx, call.MachineID, call.SessionID), call.MachineID, call.SessionID, call.Name, args)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(res, false)
}

// appTool runs a tool that is app's (app.call app.tool).
func (s *Service) appTool(ctx context.Context, app string, call ToolCall, args json.RawMessage) proto.ToolResult {
	var res proto.ToolResult
	if err := s.appCall(ctx, app, AppTool, map[string]any{"name": compat.LegacyTool(call.Name), "arguments": args, "machine_id": call.MachineID,
		"session_id": call.SessionID}, &res); err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return res
}

func (s *Service) runTool(ctx context.Context, machine, self, name string, args json.RawMessage) (string, error) {
	// Health stays in the health space (docs/assistant/health.md "How directories and memory are isolated"): it writes nothing to the knowledge base,
	// nor changes or starts what is imported into it (kb_history_status only reads).
	if name == "kb_update" || strings.HasPrefix(name, "kb_history_") && name != "kb_history_status" {
		if space, _ := s.selfSpace(ctx, self); space == SpaceHealth {
			return "", errors.New("the health space doesn't write the knowledge base: health information stays in its conversations and memory")
		}
	}
	switch name {
	case "supervision_finish":
		return s.toolSupervisionFinish(ctx, self, args)
	case "ask_user":
		return s.toolAskUser(ctx, machine, self, args)
	case "view_show", "view_update", "view_get":
		return s.toolView(ctx, machine, self, name, args)
	case "task_plan":
		return s.toolTaskPlanFrom(ctx, machine, self, args)
	case "model_profiles_list", "model_profile_get", "model_profile_switch", "model_profile_rollback":
		return s.toolModelProfile(ctx, self, name, args)
	case "task_start_screen": // a failed start's kept tab (start_remedy.go)
		return s.toolTaskStartScreen(ctx, self, args)
	case "task_start_keys":
		return s.toolTaskStartKeys(ctx, self, args)
	case "task_retry":
		return s.toolTaskRetry(ctx, self, args)
	case "task_scope":
		return s.toolTaskScope(ctx, self, args) // task_brief_scope.go
	case "task_review":
		res, err := s.toolTaskReview(ctx, self, args) // supervise_review.go
		if err == nil {
			var a struct {
				Verdict string `json:"verdict"`
			}
			if json.Unmarshal(args, &a) == nil && (a.Verdict == reviewAccepted || a.Verdict == reviewNeedsUser) {
				s.promoteSupervision(self)
			}
		}
		return res, err
	case "todo_list", "todo_get", "todo_create", "todo_update", "todo_complete", "todo_reopen", "todo_delete":
		return s.toolTodo(ctx, machine, self, name, args) // todo_tools.go
	case "direction_name":
		return s.toolDirectionName(ctx, self, args) // direction_progress.go
	case "leader_progress":
		return s.toolLeaderProgress(ctx, self, args)
	case "concern_report":
		return s.toolConcernReport(ctx, machine, self, args) // concerns.go
	case "kb_search", "kb_list", "kb_get", "kb_update":
		return s.toolKB(ctx, name, args)
	case "kb_history_plan", "kb_history_status", "kb_history_stop", "kb_history_resume":
		return s.toolKBHistory(ctx, name, args) // kb_history_tools.go
	case "interaction_answer":
		return s.toolInteractionAnswer(ctx, self, args)
	case "ask_escalate":
		return s.toolAskEscalate(ctx, self, args) // ask_route.go
	case "secret_run":
		return s.toolSecretRun(ctx, machine, self, args) // secret_run.go
	case "list_places":
		return s.toolListPlaces(ctx) // places.go
	case "save_place":
		return s.toolSavePlace(ctx, args)
	case "inbox_draft":
		return s.toolInboxDraft(ctx, self, args)
	case "inbox_suggest":
		return s.toolInboxSuggest(ctx, self, args)
	case "inbox_folders":
		return s.toolInboxFolders(ctx, args) // inbox_folders.go
	case "inbox_move":
		return s.toolInboxMove(ctx, args)
	case "notify_user":
		var a struct{ Title, Body string }
		if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Title+a.Body) == "" {
			return "", fmt.Errorf("title and body are required")
		}
		if s.justMeNow(machine, self) {
			// A Just me turn (meeting_justme.go): no push and no copy anywhere; the text stays in the conversation,
			// which the meeting page shows.
			return justMeNotice(s.lang(ctx)), nil
		}
		if s.inHealth(ctx, self) { // the lock screen and the channels hear there is news, not what it is (health.go)
			a.Title, a.Body = healthNotice(s.lang(ctx))
		}
		// Asked for explicitly: every device, the Feishu bot too, and no separate push when this turn ends.
		s.push(ctx, Notification{Subtitle: "Persona", DeviceID: machine, SessionID: self, Title: a.Title, Body: a.Body, Asked: true})
		// The Feishu bot and the channels with pushes on (channels_handler.go).
		s.channelsPushed(machine, self, a.Title, a.Body)
		s.recordNotify(ctx, machine, self) // an interruption (north_star.go)
		s.promoteSupervision(self)
		return "Notification sent.", nil
	case "conversations_list":
		return s.toolConversationsList(ctx, self, args)
	case "conversation_read":
		return s.toolConversationRead(ctx, self, args)
	case "conversation_rename":
		return s.toolConversationRename(ctx, self, args)
	case "conversation_archive", "conversation_unarchive":
		return s.toolConversationArchive(ctx, self, args, name == "conversation_archive")
	case "conversation_new":
		return s.toolConversationNew(ctx, self, args)
	case "conversation_send":
		return s.toolConversationSend(ctx, self, args)
	case "schedule_followup":
		return s.toolScheduleFollowup(ctx, machine, self, args)
	case "list_followups":
		space, _ := s.knownSpace(ctx, self)
		return s.describeSchedule(ctx, space), nil
	case "schedule_routine":
		return s.toolScheduleRoutine(ctx, self, args)
	case "delete_routine", "cancel_followup":
		var a struct{ ID string }
		_ = json.Unmarshal(args, &a)
		space, _ := s.knownSpace(ctx, self)
		if !s.taskInSpace(ctx, a.ID, space) { // another space's task is as good as none (health.go)
			return "", fmt.Errorf("no task %q", a.ID)
		}
		ok, err := s.deleteTask(ctx, a.ID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no task %q", a.ID)
		}
		if name == "delete_routine" {
			return "Deleted.", nil
		}
		return "Cancelled.", nil
	case "machines_list":
		return s.toolMachinesList(ctx)
	case "agents_list":
		var a struct{ State, Machine, Query string }
		_ = json.Unmarshal(args, &a)
		return s.describeAgents(ctx, a.State, a.Machine, a.Query), nil
	case "agent_read":
		var a struct {
			Machine   string `json:"machine"`
			SessionID string `json:"session_id"`
			Turns     int    `json:"turns"`
		}
		_ = json.Unmarshal(args, &a)
		return s.toolAgentRead(ctx, a.Machine, a.SessionID, a.Turns)
	case "agent_send":
		var a struct {
			Machine   string `json:"machine"`
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
			// DismissNotice: close a notice that Enter only closes (no options) before sending; any other dialog
			// still refuses the send.
			DismissNotice bool `json:"dismiss_notice"`
			// Task: new work handed to this agent becomes a supervised task of the main conversation (agent_control.go),
			// read from RawTask by givenTask (nil: a reply).
			Task    *sendTask       `json:"-"`
			RawTask json.RawMessage `json:"task"`
			// Attachments: the user's pictures and files, by their paths on this conversation's computer (#2437).
			Attachments []string `json:"attachments"`
		}
		_ = json.Unmarshal(args, &a)
		a.Task = givenTask(a.RawTask)
		am, c, err := s.findAgent(ctx, a.Machine, a.SessionID)
		if err != nil {
			return "", err
		}
		if c.Assistant {
			return "", fmt.Errorf("that is an assistant conversation, not a coding agent")
		}
		if c.SourceKind != "" && (c.Capabilities == nil || !c.Capabilities.Send) {
			// i18n-ignore: original MCP refusal for the model, not API error, push or approval-card copy; keep the source reason verbatim.
			return "", fmt.Errorf("这个 Agent 仅支持查看：%s", c.ReadOnlyReason)
		}
		if strings.TrimSpace(a.Text) == "" {
			return "", fmt.Errorf("text is empty")
		}
		if stoppedAgent(c) { // read-only (#4754): nothing here starts it again
			if exitText(a.Text) {
				return fmt.Sprintf("%s has already stopped: nothing was sent.", agentLabel(c)), nil
			}
			return "", fmt.Errorf("%s has stopped and is read-only: its conversation and terminal stay, it takes no messages, "+
				"and nothing here starts it again. Read it with agent_read; to go on, start a new agent with agent_start in its "+
				"directory (with the task when it worked on one) and give it what it needs. Only the user can resume it, with "+
				"Resume on its terminal page. Nothing was sent", agentLabel(c))
		}
		atts, err := cleanAttachments(a.Attachments)
		if err == nil {
			atts, err = s.checkAttachments(ctx, machine, self, atts)
		}
		if err != nil {
			return "", fmt.Errorf("%w; nothing was sent", err)
		}
		if a.Task != nil { // refused before the user is asked to confirm a send that would not go
			if err := s.sendTaskAllowed(ctx, self, am, c, *a.Task, a.Text); err != nil {
				return "", err
			}
		} else if err := s.exitAllowed(ctx, self, am, c, a.Text, len(atts)); err != nil {
			return "", err
		}
		if ok, err := s.confirmOutbound(ctx, machine, self, name, agentLabel(c)+" · "+am.Name+"\n\n"+a.Text, args); err != nil {
			return "", err
		} else if !ok {
			return "The user declined on the phone: nothing was sent to that agent.", nil
		}
		var task *handedTask
		if a.Task != nil {
			if task, err = s.handTask(ctx, self, am, c, *a.Task, a.Text); err != nil {
				return "", err
			}
		}
		text := a.Text
		if a.Task == nil {
			if t, theirs, err := s.supervisedElsewhere(ctx, self, am, c); err != nil {
				return "", err // Unknown task ownership must not strip message provenance.
			} else if theirs {
				// Read after the phone: the agent may have become another conversation's task while the user was asked.
				if err := exitRefused(c, t, a.Text, len(atts)); err != nil {
					return "", err
				}
				text = prompts.RenderSideConversation(prompts.SideConversation{From: self, Text: a.Text})
			}
		}
		if a.Task != nil {
			text, err = s.withStandingAuthorizations(ctx, am.ID, c.Cwd, text)
			if err != nil {
				return "", task.sendFailed(ctx, s, err)
			}
		}
		// The user's attachments, copied to the agent's computer when it is another one: the text ends with them, the
		// pictures in the <attached_images> block a picture sent straight to the agent ends with (#2437).
		params := sendParams(c.SessionID, text, a.DismissNotice)
		var handed handedAttachments
		if mentioned := s.mentionedAttachments(ctx, machine, self, a.Text, atts); len(atts)+len(mentioned) > 0 {
			handed = s.handAttachments(ctx, machine, am.ID, atts, mentioned)
			params["text"] = withAttachments(text, handed)
		}
		if c.Status != proto.StatusOffline {
			if err := s.call(ctx, am.ID, DeviceSessionSend, params, nil); err != nil {
				return "", task.sendFailed(ctx, s, err)
			}
			return fmt.Sprintf("Sent to %s on %s.", agentLabel(c), am.Name) + handed.said() + task.sent(ctx, s, a.Text), nil
		}
		// Not running and not read-only: a branch nothing has started yet (node fork.go), or a node from before #4754
		// that still resumes a stopped agent. Its node starts it first, which can take minutes and goes on if this call
		// stops waiting.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if err := s.call(sctx, am.ID, DeviceSessionSend, params, nil); err != nil {
			return "", task.sendFailed(sctx, s, err)
		}
		return fmt.Sprintf("Resumed %s on %s (same session, directory and launcher) and sent.", agentLabel(c), am.Name) + handed.said() + task.sent(sctx, s, a.Text), nil
	case "agent_interrupt":
		return s.toolAgentInterrupt(ctx, machine, self, args) // agent_control.go
	case "agent_supervise":
		return s.toolAgentSupervise(ctx, self, args)
	case "agent_archive", "agent_unarchive":
		return s.toolAgentArchive(ctx, machine, self, args, name == "agent_archive") // agent_archive_tools.go
	case "agent_start":
		var a struct {
			Machine  string `json:"machine"`
			Cwd      string `json:"cwd"`
			Prompt   string `json:"prompt"`
			Launcher string `json:"launcher"`
			Model    string `json:"model"`
			Effort   string `json:"effort"`
			Tier     string `json:"tier"`
			Worktree struct {
				Branch string `json:"branch"`
			} `json:"worktree"`
			Ultracode bool `json:"ultracode"`
			// Attachments: the user's pictures and files, by their paths on this conversation's computer (#2437).
			Attachments []string `json:"attachments"`
			// SecretEnv: environment variables the Agent gets from secret providers, by reference, after the user
			// approves on the phone (secret_handoff.go).
			SecretEnv map[string]string `json:"secret_env"`
		}
		_ = json.Unmarshal(args, &a)
		if err := checkSecretEnvArg(args); err != nil {
			return "", fmt.Errorf("%w; no agent was started", err)
		}
		am, ok := s.machineByName(ctx, a.Machine)
		if !ok {
			return "", fmt.Errorf("unknown machine %q (machines: %s)", a.Machine, s.machineIDs(ctx))
		}
		// Refused before the user is asked: a model goes to the agent as --model (#1644).
		if a.Model = strings.TrimSpace(a.Model); a.Model != "" && !proto.ValidModelID(a.Model) {
			return "", fmt.Errorf("model %q is not a model id: %s; no agent was started", a.Model, proto.ModelIDRule)
		}
		atts, err := cleanAttachments(a.Attachments)
		if err == nil {
			atts, err = s.checkAttachments(ctx, machine, self, atts)
		}
		if err != nil {
			return "", fmt.Errorf("%w; no agent was started", err)
		}
		a.Cwd = proto.ExpandHome(a.Cwd, am.Home)
		own := startPick{launcher: strings.TrimSpace(a.Launcher), effort: strings.TrimSpace(a.Effort)} // before the tier
		// The tier is resolved first (an unknown one asks and starts nothing), then Model Settings' Agents laid over it as
		// the start lays it: what runs is said on the card (a hand-off from the health space asks) and in the answer.
		tier, runs := strings.TrimSpace(a.Tier), ""
		if tier != "" {
			t, err := s.tierModel(ctx, tier)
			if err != nil {
				return "", fmt.Errorf("%w; no agent was started", err)
			}
			if !layTier(t, &a.Launcher, &a.Model, &a.Effort) {
				tier = "" // a launcher of its own: the tier gives it nothing
			} else {
				picks := map[string]any{"launcher": a.Launcher, "model": a.Model, "effort": a.Effort}
				if s.applyModel(ctx, "agent.new", picks) == nil { // one that can't run is the start's to say
					l, _ := picks["launcher"].(string)
					m, _ := picks["model"].(string)
					e, _ := picks["effort"].(string)
					runs = ModelTier{Launcher: l, Model: m, Effort: e}.runs()
				}
			}
		}
		// What it runs on this computer, before the user is asked (#2640, launcher_fit.go): a launcher named here that
		// the computer lacks starts nothing; a default it lacks gives way to one it has, and the card and answer say so.
		run := map[string]any{"launcher": a.Launcher, "model": a.Model, "effort": a.Effort}
		if err := s.applyModel(ctx, "agent.new", run); err != nil {
			return "", err
		}
		swapped, err := s.fitLauncher(ctx, am, own, run)
		if err != nil {
			return "", fmt.Errorf("%w; no agent was started", err)
		}
		if swapped != "" {
			a.Launcher, _ = run["launcher"].(string)
			a.Model, _ = run["model"].(string)
			a.Effort, _ = run["effort"].(string)
			tier, runs = "", "" // the tier's launcher is not what runs
		}
		branch := strings.TrimSpace(a.Worktree.Branch)
		where := am.Name + " · " + a.Cwd
		if branch != "" {
			where += i18n.T(s.lang(ctx), "assistant-tools.new_worktree", i18n.Args{"branch": branch})
		}
		switch { // the user sees what it will run on before saying yes
		case swapped != "":
			where += " · " + swapped
		case runs != "":
			where += i18n.T(s.lang(ctx), "assistant-tools.with_tier", i18n.Args{"tier": tier, "runs": runs})
		case a.Model != "":
			where += i18n.T(s.lang(ctx), "assistant-tools.with_model", i18n.Args{"model": a.Model})
		}
		if a.Ultracode {
			where += " · ultracode"
		}
		if ok, err := s.confirmOutbound(ctx, machine, self, name, where+"\n\n"+a.Prompt, withoutSecretEnv(args)); err != nil {
			return "", err
		} else if !ok {
			return "The user declined on the phone: no agent was started.", nil
		}
		var res struct {
			Session proto.Session `json:"session"`
		}
		// The node builds the session and starts the agent before answering; a machine waking from sleep or a
		// launcher cold start can take minutes. Like agent_send's resume branch, this wait is detached from the
		// conversation's turn and given agentStartTimeout instead of the old 100s.
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), agentStartTimeout)
		defer cancel()
		// The tab is named from the prompt as written; the agent gets its project's background before it (#2820).
		params := map[string]any{"cwd": a.Cwd, "launcher": a.Launcher, "model": a.Model, "effort": a.Effort, "tab_name": s.tabName(sctx, a.Prompt)}
		params["prompt"] = s.withProjectContext(sctx, am.ID, a.Cwd, s.withCloseoutBrief(sctx, a.Prompt, false))
		if a.Ultracode {
			params["ultracode"] = true
		}
		if err := s.applyModel(sctx, "agent.new", params); err != nil {
			return "", err
		}
		// A launcher that failed its health check here runs on a machine where it works (launcher_health_check.go).
		picked, _ := params["launcher"].(string)
		launcher := s.startLauncher(ctx, picked)
		alt, note, moved := s.healthDetour(ctx, am, launcher)
		if moved {
			a.Cwd = rehome(a.Cwd, am, alt)
			params["cwd"], am = a.Cwd, alt
		}
		if note != "" {
			note = " Note: " + note + "."
		}
		// Secrets for this run (secret_handoff.go): checked, then asked on the card, for the computer and launcher it
		// starts with; resolved and staged right before the start.
		var hand *secretHandoff
		approvedBy := ""
		if len(a.SecretEnv) > 0 {
			if hand, err = s.handoffPrepare(ctx, am.ID, am.Name, params, a.SecretEnv); err != nil {
				return "", fmt.Errorf("%w; no agent was started", err)
			}
			tab, _ := params["tab_name"].(string)
			by, answer, err := s.handoffApprove(ctx, machine, self, hand, tab, a.Cwd, a.Prompt)
			if err != nil {
				return "", err
			}
			if by == "" {
				return answer, nil
			}
			approvedBy = by
		}
		if runs != "" {
			note = " Preset " + tier + ": runs on " + runs + "." + note
		}
		if swapped != "" {
			note = " Note: " + swapped + "." + note
		}
		if branch != "" { // the machine's engine makes the worktree (herdr's or Orca's own), the agent starts in it
			wt, err := s.newWorktree(sctx, am.ID, a.Cwd, branch)
			if err != nil {
				return "", fmt.Errorf("the worktree on branch %s was not made, no agent was started: %w", branch, err)
			}
			a.Cwd = wt.Path
			params["cwd"], params["workspace_id"] = wt.Path, wt.WorkspaceID
			note = fmt.Sprintf(" It runs in a new worktree on branch %s.", wt.Branch) + note
		}
		// The user's attachments, after any detour: to the machine the agent starts on (#2437).
		if mentioned := s.mentionedAttachments(ctx, machine, self, a.Prompt, atts); len(atts)+len(mentioned) > 0 {
			handed := s.handAttachments(sctx, machine, am.ID, atts, mentioned)
			params["prompt"] = withAttachments(params["prompt"].(string), handed)
			note += handed.said()
		}
		// Remember the sessions already there, so an unanswered start can adopt only the session it created:
		// UpdatedAt is last sign of life, so a cwd match alone could be another agent already busy there. The
		// list failing is non-fatal: it just means an unknown result cannot be turned into an adoption. It gets
		// its own short bound, so a slow list cannot spend the start's wait before the start is even sent.
		lctx, lcancel := context.WithTimeout(sctx, agentStartListTimeout)
		knownIDs := s.knownSessionIDs(lctx, am.ID)
		lcancel()
		s.setPlanThen(ctx, am.ID, params)
		var values *handoffValues
		if hand != nil {
			var answer string
			if values, answer = s.handoffResolve(sctx, hand, approvedBy); values == nil {
				return answer, nil
			}
			defer values.wipe()
			if params["secret_ticket"], err = values.stage(sctx); err != nil {
				return "", fmt.Errorf("the secrets could not be staged on %s: %v; no agent was started", am.Name, err)
			}
			if params["ultracode"] != true {
				values.wipe() // no retry can need them again
			}
			note += hand.granted(approvedBy)
		}
		err = s.call(sctx, am.ID, DeviceSessionStart, params, &res)
		if reason, refused := s.ultracodeRefusal(ctx, err, params); refused {
			delete(params, "ultracode")                                  // an extra the agent it landed on cannot have: it starts without it (#3596)
			note += " Note: started without ultracode (" + reason + ")." // an answer lost on the way still says so
			err = s.call(sctx, am.ID, DeviceSessionStart, params, &res)
			// Refused before the node took the ticket, the same ticket starts it; one the refused start took and
			// dropped is gone, and the values are staged once more for this start.
			if values != nil && refusedWith(err, proto.CodeSecretEnvGone) {
				if params["secret_ticket"], err = values.stage(sctx); err != nil {
					return "", fmt.Errorf("the secrets could not be staged on %s again: %v; no agent was started", am.Name, err)
				}
				err = s.call(sctx, am.ID, DeviceSessionStart, params, &res)
			}
		}
		values.wipe()
		if err != nil {
			s.recordStartFailure(ctx, am.ID, launcher, err)
			if startResultUnknown(err) {
				// The node may already have the session and a running agent although the answer never came back:
				// do not report failure (the model would start it again, duplicating the run).
				return s.startedWithoutAnswer(ctx, self, am, picked, a.Cwd, a.Prompt, params["tab_name"], note, knownIDs), nil
			}
			return "", err
		}
		s.recordStartSuccess(ctx, am.ID, launcher)
		s.inboxAgentStarted(ctx, self, am.ID, res.Session.SessionID, a.Prompt) // an inbox event's conversation: its chain
		if told, ok := s.quietAgentTask(ctx, self, am, a.Cwd, a.Prompt, params["tab_name"], res.Session.SessionID); ok {
			return fmt.Sprintf("Started on %s in %s: session %s, a task of this conversation (its card is in the conversation; "+
				"%s).%s", am.Name, a.Cwd, res.Session.SessionID, told, note), nil
		}
		return fmt.Sprintf("Started on %s in %s: session %s. It shows up in the app's Agents tab.%s", am.Name, a.Cwd, res.Session.SessionID, note), nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

// agentStartTimeout bounds the wait for an agent_start answer: the node creates the session and launches the agent
// first, and a machine waking from sleep or a launcher cold start can take minutes. Same bound as a quiet task's
// own start (tasks_start.go's taskStartTimeout). A var so tests shorten it instead of sleeping past it.
var agentStartTimeout = taskStartTimeout

// agentStartAdoptWindow bounds how fresh a listed session's last sign of life must be to count as the one an
// unanswered start created.
var agentStartAdoptWindow = 10 * time.Minute

// agentStartListTimeout bounds the sessions.list calls around a start (the before snapshot and each adoption
// attempt), so they never spend the start's own wait.
var agentStartListTimeout = 15 * time.Second

// agentStartAdoptRetry is the one wait before a second adoption list: the hub's held list can lag the node.
var agentStartAdoptRetry = 200 * time.Millisecond

// startResultUnknown: the answer to session.start never came back (the wait ran out or the link dropped while it
// was out), though the node may already have built the session and launched the agent. An offline machine, a
// missing one or a request the node refused is definite and stays an error; rpc.ErrDropped answers the same words
// as rpc.ErrClosed (a relay passes them through), so the rpc.Error message form counts too.
func startResultUnknown(err error) bool {
	var re *rpc.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, rpc.ErrClosed) || errors.As(err, &re) && re.Message == rpc.ErrClosed.Error()
}

// startedWithoutAnswer answers an agent_start whose result is unknown. A list showing the new session the node
// built (not present before the start) adopts and links it like an answered start; otherwise (or when the pre-start
// list failed) the model gets neutral text: check the Agents list first and never start a second copy.
func (s *Service) startedWithoutAnswer(ctx context.Context, conversation string, m Device, launcher, cwd, prompt string, tab any, note string, knownIDs map[string]bool) string {
	if sess, ok := s.adoptStartedSession(ctx, m, launcher, cwd, knownIDs); ok {
		if told, ok := s.quietAgentTask(ctx, conversation, m, cwd, prompt, tab, sess.SessionID); ok {
			return fmt.Sprintf("Started on %s in %s: session %s (the start took longer than the wait; the session was "+
				"confirmed from the machine's own list), a task of this conversation (its card is in the conversation; "+
				"%s).%s",
				m.Name, cwd, sess.SessionID, told, note)
		}
		return fmt.Sprintf("Started on %s in %s: session %s (the start took longer than the wait; the session was "+
			"confirmed from the machine's own list). It shows up in the app's Agents tab.%s", m.Name, cwd, sess.SessionID, note)
	}
	return fmt.Sprintf("The agent may already be starting on %s in %s, but the machine did not answer within the wait, "+
		"so the result is unknown. Do NOT start it again: check the app's Agents tab (or call agents_list) for a new "+
		"session there, and only start again if no new session appears.%s", m.Name, cwd, note)
}

// knownSessionIDs is the machine's session ids before a start; nil when the list could not be read (no adoption).
func (s *Service) knownSessionIDs(ctx context.Context, machine string) map[string]bool {
	list, err := s.sessions(ctx, machine)
	if err != nil {
		return nil
	}
	ids := make(map[string]bool, len(list))
	for _, sess := range list {
		ids[sess.SessionID] = true
	}
	return ids
}

// adoptStartedSession finds the new session an unanswered start created: not in knownIDs, hub-started, a
// non-assistant agent of the same launcher and directory, fresh sign of life; newest wins. It lists twice, since
// the hub's held sessions can lag the node by a changed event. nil knownIDs (the pre-start list failed): nothing.
//
// Limit: two unanswered starts in the same directory and launcher at the same instant cannot be told apart, so one
// could adopt the other's session (a mislabeled card, never a duplicate agent); newest-wins is the best guess and
// the neutral fallback stays safe otherwise.
func (s *Service) adoptStartedSession(ctx context.Context, m Device, launcher, cwd string, knownIDs map[string]bool) (proto.Session, bool) {
	if knownIDs == nil {
		return proto.Session{}, false
	}
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return proto.Session{}, false
			case <-time.After(agentStartAdoptRetry):
			}
		}
		lctx, cancel := context.WithTimeout(ctx, agentStartListTimeout)
		list, err := s.sessions(lctx, m.ID)
		cancel()
		if err != nil {
			return proto.Session{}, false
		}
		var best proto.Session
		found := false
		for _, sess := range list {
			if knownIDs[sess.SessionID] || !sess.StartedByPersona || sess.Assistant || sess.Launcher != launcher ||
				(sess.Cwd != cwd && sess.WorkspaceCwd != cwd) || sess.UpdatedAt.IsZero() {
				continue
			}
			if time.Since(sess.UpdatedAt) > agentStartAdoptWindow || found && !sess.UpdatedAt.After(best.UpdatedAt) {
				continue
			}
			best, found = sess, true
		}
		if found {
			return best, true
		}
	}
	return proto.Session{}, false
}

// ---- ask_user ----

// askUserWait is how long an ask_user card waits for the user. It is longer than the Lark nudge's default wait
// (assistantwire.NudgeAfterDefault), so an owner who is away is nudged while the question still waits (#9); the
// turn that asked is held for as long, which is why the user speaking in the conversation withdraws the question
// (withdrawAskUser). A question nobody answers in it becomes a to-do (ask_todo.go). A variable for the tests.
var askUserWait = 2 * time.Hour

// question is an ask_user waiting for the user: the card the hub shows, and where the answer goes.
type question struct {
	ix     proto.Interaction
	answer chan proto.Resolution
	// userSpoke is set (under questions.mu) when the question was withdrawn because the user wrote in the conversation.
	userSpoke bool
}

// errUserSpoke is what ask returns for a question withdrawn because the user wrote in the conversation.
var errUserSpoke = errors.New("the user wrote in the conversation")

// questions are the questions out to the user, by interaction id: they go to the hub in the snapshot.
type questions struct {
	mu sync.Mutex
	m  map[string]*question
}

func (s *Service) toolAskUser(ctx context.Context, machine, sessionID string, args json.RawMessage) (string, error) {
	var a struct {
		Questions []struct {
			Question    string                 `json:"question"`
			Header      string                 `json:"header"`
			MultiSelect bool                   `json:"multiSelect"`
			Options     []proto.QuestionOption `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(args, &a); err != nil || len(a.Questions) == 0 {
		return "", fmt.Errorf("questions are required")
	}
	var da askDeadlineArgs
	if err := json.Unmarshal(args, &da); err != nil {
		return "", fmt.Errorf("arguments: %v", err) // a deadline or reversible of the wrong type is no deadline to ignore
	}
	now := time.Now()
	deadline, err := readAskDeadline(da, now)
	if err != nil {
		return "", err
	}
	expires := now.Add(askUserWait)
	if !deadline.At.IsZero() {
		// The deadline is kept as a time in the question's arguments (askDeadlineOf reads it back when it expires), and
		// the card waits no longer than it.
		var m map[string]any
		if err := json.Unmarshal(args, &m); err != nil {
			return "", fmt.Errorf("arguments: %v", err)
		}
		m["deadline"] = deadline.At.Format(time.RFC3339Nano)
		if args, err = json.Marshal(m); err != nil {
			return "", err
		}
		if deadline.At.Before(expires) {
			expires = deadline.At
		}
	}
	ix := proto.Interaction{
		ID: "ix_" + randomHex(6), MachineID: machine, SessionID: sessionID, SessionTitle: "Persona", Kind: proto.IxQuestion, CanCancel: true,
		ToolName: "ask_user", ToolInput: args, CreatedAt: now.UTC(), ExpiresAt: expires.UTC(),
	}
	if c, ok := s.session(ctx, machine, sessionID); ok {
		ix.Cwd = c.Cwd
		if t := transcript.Title(c.Title); t != "" {
			ix.SessionTitle = t
		}
	}
	for _, q := range a.Questions {
		ix.Questions = append(ix.Questions, proto.Question{Question: q.Question, Header: q.Header, MultiSelect: q.MultiSelect, Options: q.Options})
	}
	markRecommended(s.lang(ctx), s.location(ctx), ix.Questions, deadline)
	// Answers come back keyed by question text: two questions with the same text would lose one answer.
	if proto.HasDuplicateQuestionText(ix.Questions) {
		return "", i18n.Err("hub-hub.duplicate_question")
	}
	s.promoteSupervision(sessionID)
	r, err := s.ask(ctx, ix)
	if errors.Is(err, errUserSpoke) {
		return prompts.RenderQuestionUserSpoke(), nil
	}
	if err != nil {
		return "", err
	}
	if r.Cancel {
		return prompts.RenderQuestionCancelled(), nil
	}
	if s.askExpiredResolution(ix, r) { // nobody answered in time: it waits as a to-do (ask_todo.go)
		t, err := s.askExpired(context.WithoutCancel(ctx), ix)
		if err == nil {
			return s.askExpiredResult(context.WithoutCancel(ctx), ix, t, deadline), nil
		}
		s.logf("ask_user %s: expired, and no to-do could be made: %v", ix.ID, err)
	}
	if r.Dismiss || len(r.Answers) == 0 {
		return "The user did not answer (dismissed or timed out). Don't go ahead with what you asked about; say what is waiting for them.", nil
	}
	var b strings.Builder
	b.WriteString("The user answered:\n")
	for _, q := range a.Questions {
		if ans, ok := r.Answers[q.Question]; ok {
			fmt.Fprintf(&b, "- %s → %s\n", q.Question, ans)
		}
	}
	return b.String(), nil
}

// ask puts ix on the phone (the snapshot's interactions: the hub shows it like any agent's card, pushes it and
// expires it) and waits for the answer (Action); when ctx ends first the question is withdrawn.
func (s *Service) ask(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	ix = s.ownCard(ctx, ix)
	q := &question{ix: ix, answer: make(chan proto.Resolution, 1)}
	s.questions.mu.Lock()
	if s.questions.m == nil {
		s.questions.m = map[string]*question{}
	}
	s.questions.m[ix.ID] = q
	s.questions.mu.Unlock()
	s.publish(context.WithoutCancel(ctx))
	defer func() {
		s.questions.mu.Lock()
		delete(s.questions.m, ix.ID)
		s.questions.mu.Unlock()
		s.publish(context.WithoutCancel(ctx))
	}()
	select {
	case r := <-q.answer:
		s.questions.mu.Lock()
		spoke := q.userSpoke
		s.questions.mu.Unlock()
		if spoke && r.Dismiss && r.Reason == proto.DismissedInChat { // not an answer that got in first
			return r, errUserSpoke
		}
		return r, nil
	case <-ctx.Done():
		return proto.Resolution{}, ctx.Err()
	}
}

// withdrawAskUser takes back the ask_user questions the conversation is waiting on, because the user wrote in it: the
// turn that asked is blocked on the answer, so what they write would wait behind it (Claude queues a message sent
// during a turn until the turn ends). The tool returns at once and says the new message follows. The card is closed
// as dismissed, with the reason "replied in the chat" (proto.DismissedInChat). Other cards, and Agents' questions, are
// not touched.
func (s *Service) withdrawAskUser(session string) {
	if session == "" {
		return
	}
	s.questions.mu.Lock()
	var mine []*question
	for _, q := range s.questions.m {
		if q.ix.SessionID == session && q.ix.ToolName == "ask_user" && !q.userSpoke {
			q.userSpoke = true
			mine = append(mine, q)
		}
	}
	s.questions.mu.Unlock()
	r := proto.Resolution{Dismiss: true, Reason: proto.DismissedInChat}
	for _, q := range mine {
		// The card is on the phone through the interaction queue, which hands the dismissal back to the question; before
		// it got there (or with no queue) the question is handed the dismissal directly.
		if err := s.resolve(q.ix.ID, r); err != nil {
			select {
			case q.answer <- r:
			default:
			}
		}
	}
}

// pendingQuestions are the questions out to the user, oldest first.
func (s *Service) pendingQuestions() []proto.Interaction {
	s.questions.mu.Lock()
	defer s.questions.mu.Unlock()
	out := []proto.Interaction{}
	for _, q := range s.questions.m {
		out = append(out, q.ix)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Action takes the user's answer to one of the questions out, or to a card about an agent's turn (agents_work_act.go)
// (op answer, the answer a proto.Resolution).
func (s *Service) Action(ctx context.Context, a Action) (ActionResult, error) {
	if a.Op != "answer" {
		return ActionResult{}, fmt.Errorf("unknown op %q", a.Op)
	}
	var r proto.Resolution
	if len(a.Answer) > 0 {
		if err := json.Unmarshal(a.Answer, &r); err != nil {
			return ActionResult{}, err
		}
	}
	if mine, err := s.toolLoopAnswer(ctx, a.InteractionID, r); mine {
		return ActionResult{OK: err == nil}, err
	}
	if mine, err := s.workAnswer(ctx, a.InteractionID, r); mine {
		return ActionResult{OK: err == nil}, err
	}
	s.questions.mu.Lock()
	q := s.questions.m[a.InteractionID]
	s.questions.mu.Unlock()
	if q == nil {
		return ActionResult{}, errGone(a.InteractionID)
	}
	if r.Cancel && (q.ix.Kind != proto.IxQuestion || !q.ix.CanCancel || !r.IsQuestionCancel()) {
		return ActionResult{}, errInvalidQuestionCancel
	}
	r = honest(q.ix, r)
	select {
	case q.answer <- r:
	default: // answered already
	}
	return ActionResult{OK: true}, nil
}

// ---- agents ----

// machineByName is the machine by its id, else by its name or ssh alias (any case).
func (s *Service) machineByName(ctx context.Context, id string) (Device, bool) {
	devices := s.devices(ctx)
	for _, m := range devices {
		if m.ID == id {
			return m, true
		}
	}
	for _, m := range devices {
		if strings.EqualFold(m.Name, id) || strings.EqualFold(m.SSHHost, id) {
			return m, true
		}
	}
	return Device{}, false
}

func (s *Service) machineIDs(ctx context.Context) string {
	devices := s.devices(ctx)
	ids := make([]string, 0, len(devices))
	for _, m := range devices {
		ids = append(ids, m.ID)
	}
	return strings.Join(ids, ", ")
}

// findAgent resolves a session by full id or prefix; an empty machine searches all of them.
func (s *Service) findAgent(ctx context.Context, machine, id string) (Device, proto.Session, error) {
	id = strings.TrimSpace(id)
	if len(id) < 6 {
		return Device{}, proto.Session{}, fmt.Errorf("session_id is required (at least its first 8 characters)")
	}
	var found *Device
	var session proto.Session
	for _, m := range s.devices(ctx) {
		if machine != "" {
			if want, ok := s.machineByName(ctx, machine); !ok || want.ID != m.ID {
				continue
			}
		}
		list, _ := s.sessions(ctx, m.ID)
		for _, c := range list {
			if c.SessionID == id || strings.HasPrefix(c.SessionID, id) {
				if found != nil {
					return Device{}, proto.Session{}, fmt.Errorf("session_id %s is ambiguous; use the full id and machine", id)
				}
				found, session = &m, c
			}
		}
	}
	if found != nil {
		return *found, session, nil
	}
	return Device{}, proto.Session{}, fmt.Errorf("no agent %s (use agents_list)", id)
}

func (s *Service) describeAgents(ctx context.Context, state, machine, query string) string {
	if state == "" {
		state = "active"
	}
	now := time.Now()
	var res struct {
		Interactions []proto.Interaction `json:"interactions"`
	}
	_ = s.call(ctx, DeviceAll, DeviceInteractionsList, map[string]any{}, &res)
	pending := map[[2]string][]string{}
	for _, ix := range res.Interactions {
		what := ix.Summary
		if ix.Kind == proto.IxQuestion && len(ix.Questions) > 0 {
			what = "提问：" + questionSummary(ix)
		} else if ix.Kind == proto.IxPlan {
			what = "计划待审批：" + planTitle(ix.Plan)
		} else if ix.Kind == proto.IxPermission {
			what = "要授权 " + ix.ToolName + "：" + ix.Summary
		} else if ix.Kind == proto.IxScreen {
			what = "终端对话框等你处理"
		}
		k := [2]string{ix.MachineID, ix.SessionID}
		what += " (interaction_id=" + ix.ID + ")"
		pending[k] = append(pending[k], clip(what, 400))
	}
	type row struct {
		m Device
		c proto.Session
	}
	var rows []row
	var offline []string
	want, wanted := Device{}, false
	if machine != "" {
		want, wanted = s.machineByName(ctx, machine)
	}
	for _, m := range s.devices(ctx) {
		if machine != "" && (!wanted || want.ID != m.ID) {
			continue
		}
		if !m.Online {
			offline = append(offline, m.Name)
		}
		list, muted, _ := s.sessionsMuted(ctx, m.ID)
		for _, c := range list {
			if c.Assistant {
				continue
			}
			// Archived ones are out of the apps' lists: only "archived" lists them, and "all" with them marked.
			switch state {
			case "all":
			case "archived":
				if !c.Archived {
					continue
				}
			case "active":
				if c.State != proto.StateNeedsYou && c.State != proto.StateWorking && c.State != proto.StateUnread && !c.AwaitsReply {
					continue // a seen one still asking the user is active too (#5096)
				}
			case "done":
				if snapshotAgentGroup(c, now) != snapshotAgentDone || muted[c.SessionID] {
					continue
				}
			case "muted":
				if snapshotAgentGroup(c, now) == snapshotAgentOutside || !muted[c.SessionID] {
					continue
				}
			default:
				if c.State != state {
					continue
				}
			}
			if c.Archived && state != "all" && state != "archived" {
				continue
			}
			if query != "" {
				hay := strings.ToLower(c.Title + " " + c.Cwd + " " + c.SpaceName + " " + c.SlotName)
				if !strings.Contains(hay, strings.ToLower(query)) {
					continue
				}
			}
			rows = append(rows, row{m, c})
		}
	}
	order := map[string]int{proto.StateNeedsYou: 0, proto.StateWorking: 1, proto.StateUnread: 2, proto.StateIdle: 3}
	sort.SliceStable(rows, func(i, j int) bool {
		oi, oj := order[rows[i].c.State], order[rows[j].c.State]
		if oi != oj {
			return oi < oj
		}
		return rows[i].c.UpdatedAt.After(rows[j].c.UpdatedAt)
	})
	if len(rows) > 60 {
		rows = rows[:60]
	}
	var b strings.Builder
	if len(rows) == 0 {
		b.WriteString("No agents match.\n")
	}
	for _, r := range rows {
		c := r.c
		fmt.Fprintf(&b, "- [%s] %s · machine=%s session_id=%s · %s", stateNames[c.State], agentLabel(c), r.m.ID, c.SessionID, c.Cwd)
		if c.SourceKind != "" {
			fmt.Fprintf(&b, " · source=%s instance=%s", c.SourceKind, c.SourceInstanceID)
			if c.Capabilities == nil || !c.Capabilities.Send {
				fmt.Fprintf(&b, " · 仅查看：%s", c.ReadOnlyReason)
			}
		}
		if c.Archived {
			b.WriteString(" · archived")
		}
		if !c.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, " · %s", ago(c.UpdatedAt))
		}
		if mode := ultracodeState(c.Ultracode); mode != "" {
			fmt.Fprintf(&b, " · ultracode=%s", mode)
		}
		if c.Loop != nil {
			b.WriteString(" · /loop")
		}
		if n := len(c.Background); n > 0 {
			fmt.Fprintf(&b, " · 后台任务 %d", n)
		}
		if state == "done" {
			b.WriteString(" · work=done")
		}
		if state == "muted" {
			b.WriteString(" · muted")
		}
		b.WriteString("\n")
		if (state == "done" || state == "muted") && c.Work != nil && c.Work.Phase != proto.WorkWorking && c.Work.Line != "" {
			fmt.Fprintf(&b, "    结论：%s\n", clip(oneLine(c.Work.Line), 200)) // i18n-ignore: agents_list output for the model, as the lines around it
		}
		for _, w := range runningWorkflows(c, now) { // its turn ended to wait for them: not done (#2935)
			fmt.Fprintf(&b, "    后台 workflow 在跑（在等它的结果，没做完）：%s\n", prompts.RenderSnapshotWorkflow(w))
		}
		for _, p := range pending[[2]string{r.m.ID, c.SessionID}] {
			fmt.Fprintf(&b, "    等你：%s\n", p)
		}
		if c.AwaitsReply && c.Asks != "" {
			fmt.Fprintf(&b, "    在问你、还没回：%s\n", c.Asks) // i18n-ignore: agents_list output for the model, as the lines around it
		}
		// Why it waits, while it does (a dialog's first line, a host's reason), as the apps' rows show it; else its last words.
		if last := c.LastText; c.State == proto.StateNeedsYou && strings.TrimSpace(c.AttentionReason) != "" {
			fmt.Fprintf(&b, "    最后说：%s\n", clip(oneLine(c.AttentionReason), 200))
		} else if last != "" {
			fmt.Fprintf(&b, "    最后说：%s\n", clip(oneLine(last), 200))
		}
	}
	if len(offline) > 0 {
		fmt.Fprintf(&b, "(offline machines: %s)\n", strings.Join(offline, ", "))
	}
	if machine == "" || wanted {
		b.WriteString(s.healthLines(ctx, want.ID))
	}
	return b.String()
}

func (s *Service) toolAgentRead(ctx context.Context, machine, id string, turns int) (string, error) {
	m, c, err := s.findAgent(ctx, machine, id)
	if err != nil {
		return "", err
	}
	if turns <= 0 {
		turns = 2
	}
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, m.ID, DeviceSessionItems, map[string]any{"session_id": c.SessionID, "turns": min(turns, 10)}, &res); err != nil {
		return "", err
	}
	head := fmt.Sprintf("%s on %s (%s, %s)", agentLabel(c), m.Name, stateNames[c.State], c.Cwd)
	return transcriptText(head, sessionLink(s.webBase(ctx), m.ID, c.SessionID, false), res.Items), nil
}

// questionSummary is every question of an interaction with its options, so agents_list carries what answering one
// (interaction_answer) needs without a second call: "Send? (Send / Don't send)".
func questionSummary(ix proto.Interaction) string {
	parts := make([]string, 0, len(ix.Questions))
	for _, q := range ix.Questions {
		part := q.Question
		if len(q.Options) > 0 {
			part += "（" + optionLabels(q) + "）"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " / ")
}

// sendParams are an agent_send's session.send parameters.
func sendParams(session, text string, dismissNotice bool) map[string]any {
	p := map[string]any{"session_id": session, "text": text}
	if dismissNotice {
		p["dismiss_notice"] = true
	}
	return p
}
