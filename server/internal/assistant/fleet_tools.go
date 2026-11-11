package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The nodes' MCP tools (mcp.tools / mcp.call): an assistant conversation's or an agent's Persona tools. The manager
// lists and routes them: the phone's (the hub runs them on the connected phone), the connectors', Cua's and ego's
// (the extensions', in this process: ExtensionTools, internal/extmgr), and the assistant's own (here). The skills' gated commands' settings go to
// the assistant's machine only (assistant.lark).

// runtimeCLITool is the DEBUG app's own channel, for the user's scripts only: never a node's.
const runtimeCLITool = "phone_runtime_cli"

// mcpTools are the tools of an assistant conversation or an agent: the phone's, then those of the connectors that
// are ok and give their tools to agents too (mail); for the assistant also its own, then Cua's, ego's and those of
// the connectors only it gets (Feishu, GitHub…).
func (s *Service) mcpTools(ctx context.Context, assistant bool) []proto.Tool {
	var phone struct {
		Tools []proto.Tool `json:"tools"`
	}
	if err := s.appCall(ctx, assistantwire.AppPhone, assistantwire.PhoneTools, map[string]any{}, &phone); err != nil {
		s.logf("mcp tools: the phone's: %v", err)
	}
	var agents, assistantOnly []proto.Tool
	if s.ext != nil {
		agents, assistantOnly = s.ext.AgentTools(ctx, assistant)
	}
	tools := append(phone.Tools, agents...)
	if !assistant {
		tools = append(tools, s.AgentTools()...)       // report_to_supervisor, todo_decisions, todo_decision_add, todo_bind_thread (agentToolNames)
		tools = append(tools, s.dispatchTools(ctx)...) // dispatch_start, dispatch_read, when agents.dispatch is on (dispatch.go)
	}
	if assistant {
		tools = append(tools, s.Tools(ctx)...)
		tools = append(tools, assistantOnly...)
	}
	return tools
}

// sessionMCPTools are machine's conversation session's tools: a guest conversation's only its own (guest.go; the
// node saying it is one, the hub not knowing it or not able to tell: none), any other's mcpTools.
func (s *Service) sessionMCPTools(ctx context.Context, machine, session string, assistant, guestHint bool) []proto.Tool {
	assistant = assistant && s.assistantDiscovery(ctx, machine, session)
	allowed, guest, err := s.guestOf(ctx, machine, session)
	if err != nil {
		s.logf("mcp tools: %v", err)
		return []proto.Tool{}
	}
	if _, room, err := s.roomOf(ctx, machine, session); guest && err == nil && room {
		if s.isWorkMachine(machine) {
			return []proto.Tool{}
		}
		// A meeting's room: its meeting tools only (meeting_room_tools.go), those it started with.
		return guestTools(meetingRoomTools(), allowed)
	}
	if !guest {
		if guestHint {
			return []proto.Tool{}
		}
		if s.isWorkMachine(machine) {
			return s.AgentTools()
		}
		tools := s.mcpTools(ctx, assistant)
		if !assistant {
			tools = append(tools, s.leaderTools(ctx, machine, session)...) // a task leader's task_plan (task_leader.go)
		}
		return tools
	}
	if s.isWorkMachine(machine) {
		return []proto.Tool{}
	}
	return guestTools(s.mcpTools(ctx, assistant), allowed)
}

// guestRefuses refuses a guest conversation's call of a tool it was not given (and any call of one the hub cannot
// tell about, or that the node says is a guest the hub does not know).
func (s *Service) guestRefuses(ctx context.Context, machine, session, name string, guestHint bool) (proto.ToolResult, bool) {
	allowed, guest, err := s.guestOf(ctx, machine, session)
	switch {
	case err != nil:
		s.logf("mcp call %s: %v", name, err)
		return proto.TextResult("tool unavailable for the moment: "+name, true), true
	case !guest && guestHint:
		return proto.TextResult("unknown tool "+name, true), true
	case guest && !slices.Contains(allowed, name):
		return proto.TextResult("unknown tool "+name, true), true
	}
	return proto.ToolResult{}, false
}

// mcpCall runs one: a connector's, Cua's or ego's at the hub (it says whether it is one of them), else the
// assistant's own (for its conversations), else the phone's.
func (s *Service) mcpCall(ctx context.Context, machine, session, name string, args json.RawMessage, assistant bool) proto.ToolResult {
	if res, refused := s.meetingRefuses(ctx, machine, session, name, assistant); refused {
		return res // a meeting's batch started this turn: only reading tools (meeting_turns.go)
	}
	if s.isWorkMachine(machine) {
		if isAgentTool(name) {
			return s.agentTool(ctx, machine, session, name, args)
		}
		return proto.TextResult(i18n.T(s.lang(ctx), "assistant-privacy.tool_denied"), true)
	}
	if assistant && !s.assistantCaller(ctx, machine, session) {
		return proto.TextResult(i18n.T(s.lang(ctx), "assistant-privacy.assistant_denied"), true)
	}
	if s.ext != nil { // the connectors', Cua's, ego's: ctx is the node's call (its caller going away withdraws a card)
		if res, ok := s.ext.CallTool(ctx, machine, session, name, args, assistant); ok {
			return res
		}
	}
	if !assistant && isAgentTool(name) {
		return s.agentTool(ctx, machine, session, name, args)
	}
	if !assistant && name == "task_plan" { // only a task leader's agent has it (task_leader.go)
		return s.leaderTaskPlan(ctx, machine, session, args)
	}
	if !assistant && name == "task_review" {
		return s.leaderTaskReview(ctx, machine, session, args)
	}
	if !assistant && isDispatchTool(name) {
		return s.dispatchTool(ctx, machine, session, name, args)
	}
	if assistant && s.isOwnTool(ctx, name) {
		return s.Tool(ctx, ToolCall{Name: name, Arguments: args, MachineID: machine, SessionID: session})
	}
	switch {
	case name == runtimeCLITool:
		return proto.TextResult("unknown tool "+name, true)
	case strings.HasPrefix(name, "phone_"):
		if name == "phone_notify" && s.justMeNow(machine, session) {
			// A Just me turn (meeting_justme.go): a phone on the table shows its notifications to the room. As
			// notify_user's: nothing goes out, the text stays in the conversation, which the meeting page shows.
			return proto.TextResult(justMeNotice(s.lang(ctx)), false)
		}
		if name == "phone_notify" && s.inHealth(ctx, session) {
			var a map[string]any
			if err := json.Unmarshal(args, &a); err != nil {
				return proto.TextResult(err.Error(), true)
			}
			if a != nil {
				// Local phone notifications follow the same privacy rule as notify_user.
				a["title"], a["body"] = healthNotice(s.lang(ctx))
				args, _ = json.Marshal(a)
			}
		}
		var res proto.ToolResult
		if err := s.appCall(ctx, assistantwire.AppPhone, assistantwire.PhoneCall, map[string]any{"name": name, "args": args}, &res); err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return res
	}
	return proto.TextResult("unknown tool "+name, true)
}

// mailAttachment answers a node's mail.attachment: the bytes of the attachment mail_save_attachment saves on that
// machine (node/mailattachment.go), for a caller that could call the tool through mcp.call: not a work machine, an
// assistant conversation only when it is one, a guest only when it was given the tool; then the extensions decide
// whether the mail connector offers it to that caller (extmgr MailAttachment).
func (s *Service) mailAttachment(ctx context.Context, machine string, req proto.MailAttachmentRequest) (proto.MailAttachment, error) {
	// Not named "name": the i18n key check resolves constants by name across the package, and a constant called
	// name would turn every "prefix."+name text wrapper into one whole key.
	const saveAttachmentTool = "mail_save_attachment"
	if s.isWorkMachine(machine) {
		return proto.MailAttachment{}, errors.New(i18n.T(s.lang(ctx), "assistant-privacy.tool_denied"))
	}
	if req.Assistant && !s.assistantCaller(ctx, machine, req.SessionID) {
		return proto.MailAttachment{}, errors.New(i18n.T(s.lang(ctx), "assistant-privacy.assistant_denied"))
	}
	res, refused := s.guestRefuses(ctx, machine, req.SessionID, saveAttachmentTool, req.Guest)
	if !refused {
		res, refused = s.meetingRefuses(ctx, machine, req.SessionID, saveAttachmentTool, req.Assistant)
	}
	if refused {
		var why strings.Builder
		for _, c := range res.Content {
			why.WriteString(c.Text)
		}
		return proto.MailAttachment{}, errors.New(why.String())
	}
	if s.ext == nil {
		return proto.MailAttachment{}, errors.New("mail is not available")
	}
	return s.ext.MailAttachment(ctx, req)
}

// isOwnTool: name is one of the assistant's own tools.
func (s *Service) isOwnTool(ctx context.Context, name string) bool {
	for _, t := range s.Tools(ctx) {
		if t.Name == name {
			return true
		}
	}
	return false
}

// assistantLark is what the assistant's machine gets for its skills' commands (proto.AssistantLark): lark-cli's
// settings and rules, and the plugins' command gates (the hub's, app.call hub). Another machine gets nothing: the
// credentials go to the assistant's machine only (its process runs them).
func (s *Service) assistantLark(ctx context.Context, machine string) (proto.AssistantLark, error) {
	if s.isWorkMachine(machine) || !s.sameMachine(machine, s.settings(ctx).MachineID) {
		return proto.AssistantLark{}, nil
	}
	if s.ext == nil {
		return proto.AssistantLark{}, nil
	}
	return s.ext.AssistantLark(ctx)
}

// assistantSummary is the text a gated command's card shows, as its plugin names what the ids are; false: none.
func (s *Service) assistantSummary(ctx context.Context, tool, summary string) (string, bool) {
	if tool == "" {
		return summary, false
	}
	if s.ext == nil {
		return summary, false
	}
	return s.ext.AssistantSummary(ctx, tool, summary)
}
