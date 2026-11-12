package assistantwire

import "github.com/nwverse/persona/server/internal/devicewire"

// ---- the bus ----

// AssistantID is the bus identity the assistant connects as (a manager's token, hub/bustokens.go).
const AssistantID = "assistant"

// Capabilities a manager names in manager.hello's capabilities (stable names: never renamed or reused).
const (
	// CapabilityConfigOwn: the manager reads its own sections of the configuration from its manager.json (owner,
	// notes, Agents, the knowledge base, the models, a card's wait): the hello does not carry them any more, and a
	// manager without it runs on its built-in defaults (docs/assistant/managers.md "Configuration").
	CapabilityConfigOwn = "config.own"
	// CapabilityAgentSessionArchives: the manager owns recoverable Codex/Claude archives.
	CapabilityAgentSessionArchives = "agent_session_archives"
	// CapabilityAgentNaming: ordinary full-user renames record manual intent before node dispatch.
	CapabilityAgentNaming = "agent_naming.v1"
	// CapabilityAgentSessionArchiveClose: the manager's archive also takes ?close=1, "Archive and close", which closes
	// the agent on its computer (session.retire) once archived (docs/protocol/protocol.md "Agent session archives").
	CapabilityAgentSessionArchiveClose = "agent_session_archive_close"
	// CapabilityMachinesOwn: the manager keeps its own list of computers in its manager.json and takes the hub's edits
	// of it (devicewire MethodMachinesPut / Remove / Get) once the hub hands it MachinesOwn
	// (docs/assistant/managers.md "Each manager's computers").
	CapabilityMachinesOwn = "machines.own"
	// CapabilityMeetingAttendance: the manager attends recorded meetings (the recordings' attendance fields, …/live,
	// …/ask and meeting_attendance_default; docs/assistant/meeting-minutes.md "Attending a meeting"). The hub lists the
	// meeting_attendance feature only while the default manager names it.
	CapabilityMeetingAttendance = "meeting_attendance"
	// CapabilityMeetingConversation: the manager takes the meeting conversation's phase 1 (the upload's room and
	// private, the room conversation, …/ask answered by it, …/just-me, …/private, …/followup, the meeting_live event;
	// docs/assistant/meeting-minutes.md "Meeting conversation (#3862)"). The hub lists meeting_conversation only while
	// the default manager names it. A manager that names it names CapabilityMeetingAttendance too.
	CapabilityMeetingConversation = "meeting_conversation"
	// CapabilityMeetingPhotos: the manager takes the meeting conversation's phase 3, its photos (…/photos, forwarding
	// them to the room, the room's notes, citations in the minutes; docs/assistant/meeting-minutes.md "Phase 3:
	// photos"). The hub lists meeting_photos only while the default manager names it beside
	// CapabilityMeetingConversation.
	CapabilityMeetingPhotos = "meeting_photos"
	// CapabilityRecordingDay: the manager takes all-day recordings and writes their daily recap (the upload's kind
	// day, POST …/segments, detail.recap, recap_state; #4741). The hub lists the recording_day feature only while the
	// default manager names it.
	CapabilityRecordingDay = "recording_day"
)

// The methods and events, by direction. The assistant's handshake is manager.hello {id, host, os, arch, deploy,
// version, capabilities}; its answer is Hello (with the hub's name, role and managers besides).
const (
	// The assistant calls the hub (Hub).
	MethodHello    = "manager.hello"
	MethodDevice   = "device.call"
	MethodApp      = "app.call"
	MethodNotify   = "notify.request"
	MethodSnapshot = "manager.snapshot"
	EventBroadcast = "ws.broadcast" // one way

	// The hub calls the assistant (Handler).
	MethodHTTP    = "http.request" // HTTPRequest → HTTPResponse
	MethodTools   = "assistant.tools"
	MethodTool    = "assistant.tool"
	MethodContext = "assistant.context"
	MethodAction  = "manager.action"

	// The hub tells the assistant (one way).
	EventUserSpoke           = "session.user_spoke"
	EventUserStopped         = "session.user_stopped" // the user stopped an assistant conversation's turn in an app
	EventTurnStarted         = "session.turn_started"
	EventTurnEnded           = "session.turn_ended"
	EventSessionsChanged     = "sessions.changed"
	EventInteractionResolved = "interaction.resolved"
	EventInteractionRaised   = "interaction.raised"
	EventUpdated             = "manager.updated" // Hello again: hub.json's assistant section or the machines changed
	EventResync              = "manager.resync"  // {}: report the whole snapshot again

	// EventStartEnded (StartEnded): how an app's start the node ran before answering went (POST /api/v1/sessions).
	EventStartEnded = "session.start_ended"

	// EventConversationSent (ConversationSent): how an app's message to one of the assistant's conversations went, as
	// the hub sent it (the north star's availability, docs/assistant/north-star.md). An older manager ignores it.
	EventConversationSent = "session.assistant_sent"
)

// Error codes that cross the bus where a Go error would lose its kind (rpc.Error.Code).
const (
	CodeNoDevice         = "no_device"         // ErrNoDevice
	CodeMachineOffline   = "machine_offline"   // the machine's node is not connected (apierr.ErrOffline)
	CodeNodeClosed       = "node_closed"       // the link to the machine's node dropped during the call (rpc.ErrClosed)
	CodeStaleSnapshot    = "stale_snapshot"    // ErrStaleSnapshot
	CodeAssistantOffline = "assistant_offline" // the hub has no connection from the assistant
	// CodeFeishuAPI: Feishu answered the bot's call with an error code (Data: {"code": n}): a refusal or a passing
	// failure the assistant tells apart (the agent messages, feishu_cot.go).
	CodeFeishuAPI = "feishu_api"
	// CodeChatDenied: the hub refused to send into a chat or to reply to a message in one, because the bot may only use
	// the owner's private chat and the group chats the owner opted in. Nothing was sent.
	CodeChatDenied = "chat_denied"
	// CodeMeetingEnded: a message into a meeting's room that ended (docs/assistant/meeting-minutes.md "Messages from
	// elsewhere"): the hub answers 409 meeting_ended, and the apps write in the follow-up instead.
	CodeMeetingEnded = "meeting_ended"
	// CodeLauncherOtherKind: a launcher of another agent kind picked for a message in an existing assistant
	// conversation (Data: conversation_agent, launcher, launcher_agent, delivered "none"): the hub answers 409, nothing
	// was sent, and the conversation keeps its agent (docs/assistant/assistant.md, #4662).
	CodeLauncherOtherKind = "launcher_other_kind"
)

// HTTPRequest is http.request's params: an app's request on one of Routes, with an escaped URL path (header without
// the hub's credentials and hop-by-hop headers, the asking device's role in RoleHeader; body as base64).
// HandoffApprover is set by the hub on a card's answer from a device that may approve a hand-off card; Human on a new
// conversation a person's own app opens.
type HTTPRequest struct {
	Caller          *devicewire.Caller         `json:"caller,omitempty"`
	ShareAuthority  *devicewire.ShareAuthority `json:"share_authority,omitempty"`
	HandoffApprover *HandoffApprover           `json:"handoff_approver,omitempty"`
	// Human is the hub's word that a person's own app opens this conversation (POST /api/v1/assistant/conversations
	// with a full device's credential, decided as for SendParams.Human): its first message counts as theirs
	// (WithHumanStart). Set by the hub on that route only, never from the request; an older hub never sends it.
	Human  bool                `json:"human,omitempty"`
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

// HTTPResponse is http.request's answer, written back to the app unchanged.
type HTTPResponse struct {
	ShareAuthority *devicewire.ShareAuthority `json:"share_authority,omitempty"`
	Status         int                        `json:"status"`
	Header         map[string][]string        `json:"header,omitempty"`
	Body           []byte                     `json:"body,omitempty"`
}

// Routes are the routes the assistant answers (net/http patterns): the hub relays the apps' requests on them as
// http.request. The assistant registers exactly these (assistant.TestRoutesAreTheWireOnes).
var Routes = []string{
	"POST /api/v1/assistant/health/workouts",
	"GET /api/v1/sessions/archived",
	"POST /api/v1/sessions/{mid}/{sid}/archive",
	"POST /api/v1/sessions/{mid}/{sid}/unarchive",
	"POST /api/v1/assistant/conversations/{id}/archive",
	"GET /api/v1/assistant/conversations/{id}/always",
	"GET /api/v1/assistant/standing-authorizations",
	"GET /api/v1/assistant/north-star",
	"DELETE /api/v1/assistant/standing-authorizations/{id}",
	"POST /api/v1/assistant/rule-suggestions/{id}/always",
	"POST /api/v1/assistant/rule-suggestions/{id}/not-now",
	"DELETE /api/v1/assistant/conversations/{id}/always/{hash}",
	"POST /api/v1/assistant/conversations/{id}/unarchive",
	"POST /api/v1/assistant/conversations/{id}/stop",
	"GET /api/v1/assistant/reporters",
	"GET /api/v1/assistant/leaders",
	"GET /api/v1/assistant/availability",
	"GET /api/v1/assistant/directions/tree",
	"GET /api/v1/assistant/directions/offers",
	"DELETE /api/v1/assistant/directions/offers/{id}",
	"POST /api/v1/assistant/directions/{id}/start",
	"POST /api/v1/assistant/directions/{id}/make",
	"POST /api/v1/assistant/directions/{id}/end",
	"POST /api/v1/assistant/route/agent",
	"POST /api/v1/assistant/agents/{mid}/{sid}/supervisor",
	"PUT /api/v1/assistant/directions/{id}/name",
	"GET /api/v1/assistant/conversations/{id}/feed",
	"POST /api/v1/assistant/feed/{id}/dismiss",
	"POST /api/v1/assistant/feed/{id}/undo-dismiss",
	"POST /api/v1/assistant/conversations/{id}/route",
	"POST /api/v1/assistant/conversations/{id}/today",
	"GET /api/v1/assistant/tasks",
	"GET /api/v1/assistant/tasks/cron",
	"POST /api/v1/assistant/tasks",
	"PUT /api/v1/assistant/tasks/{id}",
	"DELETE /api/v1/assistant/tasks/{id}",
	"POST /api/v1/assistant/tasks/{id}/run",
	"POST /api/v1/assistant/tasks/{id}/fresh",
	"GET /api/v1/assistant/places",
	"POST /api/v1/assistant/places",
	"PUT /api/v1/assistant/places/{id}",
	"DELETE /api/v1/assistant/places/{id}",
	"GET /api/v1/assistant/runs",
	"GET /api/v1/assistant/routines",
	"PUT /api/v1/assistant/routines",
	"POST /api/v1/assistant/routines/{id}/run",
	"GET /api/v1/assistant/followups",
	"POST /api/v1/assistant/followups/{id}/run",
	"DELETE /api/v1/assistant/followups/{id}",
	"GET /api/v1/task-plans/{id}",
	"POST /api/v1/task-plans/{id}/start",
	"POST /api/v1/task-plans/{id}/workspace-preview",
	"POST /api/v1/task-plans/{id}/dismiss",
	"GET /api/v1/task-trust",
	"DELETE /api/v1/task-trust",
	// The todos (the assistant's todos.go, docs/assistant/todos.md).
	"GET /api/v1/todos",
	"POST /api/v1/todos",
	"PATCH /api/v1/todos/{id}",
	"DELETE /api/v1/todos/{id}",
	"PUT /api/v1/todos/{id}/spaces/{mid}/{space...}",
	"DELETE /api/v1/todos/{id}/spaces/{mid}/{space...}",
	"PUT /api/v1/todos/{id}/agents/{mid}/{sid}",
	"DELETE /api/v1/todos/{id}/agents/{mid}/{sid}",
	"GET /api/v1/todos/{id}/decisions",
	"POST /api/v1/todos/{id}/decisions",
	"PATCH /api/v1/todos/{id}/decisions/{did}",
	"PUT /api/v1/todos/{id}/thread",
	"DELETE /api/v1/todos/{id}/thread",
	"POST /api/v1/todos/{id}/accept",
	"POST /api/v1/todos/{id}/decision-dismiss",
	"POST /api/v1/todos/{id}/decision-undo-dismiss",
	"POST /api/v1/later/{id}/handled", // a question that can wait, marked handled (later.go)
	"POST /api/v1/todos/{id}/done-check",
	"POST /api/v1/todos/{id}/retry",
	"POST /api/v1/todos/retitle",
	// Its settings and its main conversation (profile.go, start.go).
	"GET /api/v1/assistant",
	"PUT /api/v1/assistant",
	"POST /api/v1/assistant/warm",
	"GET /api/v1/assistant/readiness",
	"POST /api/v1/assistant/readiness/dismiss",
	"GET /api/v1/assistant/memory",
	"PUT /api/v1/assistant/memory/{name}",
	"DELETE /api/v1/assistant/memory/{name}",
	"GET /api/v1/assistant/capabilities",
	"POST /api/v1/assistant/conversations",
	"POST /api/v1/assistant/conversations/{id}/compact",
	"PUT /api/v1/assistant/conversations/{id}/model", // the model a conversation's next messages run on (model_pick.go, #5033)
	"DELETE /api/v1/assistant/conversations/{id}/model",
	"POST /api/v1/assistant/main/new",
	"POST /api/v1/assistant/main/clear",
	"POST /api/v1/assistant/origins/backfill",
	// The channels (channels.go there, docs/extensions/channels.md "API").
	"GET /api/v1/channels",
	"GET /api/v1/channels/{id}",
	"PUT /api/v1/channels/{id}",
	"DELETE /api/v1/channels/{id}/binding",
	"POST /api/v1/assistant/conversations/{id}/views/{key}/actions",
	"POST /api/v1/assistant/conversations/{id}/views/{key}/html-actions",
	"POST /api/v1/channels/{id}/test",
	// The Lark bot's workbench: companion bots and their notices (feishu_companions.go there, docs/extensions/bots.md
	// "Companion bots").
	"PUT /api/v1/assistant/feishu/companions",
	"GET /api/v1/assistant/feishu/notices",
	"POST /api/v1/assistant/feishu/notices/action",
	"POST /api/v1/assistant/feishu/workbench",
	// The knowledge base (the assistant's kb_routes.go, docs/assistant/knowledge.md "App API").
	"GET /api/v1/kb/{kind}",
	"GET /api/v1/kb/{kind}/{id}",
	"PUT /api/v1/kb/{kind}/{id}",
	"DELETE /api/v1/kb/projects/{id}",
	"GET /api/v1/kb-trash",
	"POST /api/v1/kb-trash/{id}/restore",
	"GET /api/v1/kb/moments/{id}",
	"GET /api/v1/kb/chats/{id}/context",
	"GET /api/v1/kb/chats/{id}/days/{day}",
	"GET /api/v1/kb-search",
	// The selection: what the knowledge base brings in and keeps (kb_selection.go there).
	"GET /api/v1/kb/selection",
	"PUT /api/v1/kb/selection",
	"GET /api/v1/kb/selection/candidates",
	// The phone's recordings (kb_recordings.go there, docs/protocol/protocol.md "Recording sync").
	"PUT /api/v1/recordings/{id}",
	"DELETE /api/v1/recordings/{id}",
	"GET /api/v1/recordings",
	"POST /api/v1/recordings/{id}/minutes",
	"POST /api/v1/recordings/{id}/minutes/todos",
	// All-day recordings' appends (kb_day.go there, #4741).
	"POST /api/v1/recordings/{id}/segments",
	// Attending a meeting (meeting_live.go there, docs/assistant/meeting-minutes.md "Attending a meeting").
	"POST /api/v1/recordings/{id}/live",
	"GET /api/v1/recordings/{id}/live",
	"POST /api/v1/recordings/{id}/ask",
	// The meeting conversation (meeting_room.go there, docs/assistant/meeting-minutes.md "Meeting conversation (#3862)").
	"PUT /api/v1/recordings/{id}/private",
	"POST /api/v1/recordings/{id}/just-me",
	"POST /api/v1/recordings/{id}/followup",
	// Its photos (meeting_photos.go there, "Phase 3: photos").
	"POST /api/v1/recordings/{id}/photos",
	"GET /api/v1/recordings/{id}/photos",
	"GET /api/v1/recordings/{id}/photos/{n}",
	"DELETE /api/v1/recordings/{id}/photos/{n}",
}
