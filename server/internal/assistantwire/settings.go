package assistantwire

import "github.com/nwverse/persona/server/internal/proto"

// ---- the assistant's settings, and the conversations the hub's features start (docs/assistant/assistant.md "Boundary with hub") ----

// Profile is the assistant's settings (settings `assistant`, its own) as the hub's own features need them, in every
// snapshot: a phone's message to the main conversation is told apart, the model configuration's main chat runs on its
// machine, the hub reads times in the user's zone, and the nodes keep listing Keep (the Feishu bot and the channels are
// the assistant's own). The hub keeps the last
// one; before one came in it goes by hub.json's assistant section, its own time zone and no main conversation.
type Profile struct {
	MachineID     string `json:"machine_id"` // where its conversations run
	Main          string `json:"main,omitempty"`
	MainMachineID string `json:"main_machine_id,omitempty"` // confirmed route of Main, independent of the default
	// Launcher is the one it runs on when "Models" › main chat names none (its own setting, else hub.json's, else claude):
	// that purpose's default.
	Launcher string `json:"launcher"`
	Timezone string `json:"timezone,omitempty"` // the user's (the phone reports it), IANA; "" while not known
	// Keep are the conversations their machines list however long they are quiet: the main one, the main ones it
	// replaced, scheduled tasks' own (node.configure keep_sessions: a busy machine's newest transcripts crowd them out).
	Keep []SessionRef `json:"keep"`
}

// MainRouteMachine keeps legacy profiles usable while preferring the confirmed main binding.
func (p Profile) MainRouteMachine() string {
	if p.MainMachineID != "" {
		return p.MainMachineID
	}
	return p.MachineID
}

// The hub asks the assistant (Handler) for what its own features need of the assistant's conversations.
const (
	// MethodStart: StartParams → {session}: a conversation the hub's features start (the inbox's AI twin).
	MethodStart = "assistant.start"
	// MethodSendParams: SendParams → {params}: a message for one of its conversations (the phone's, one the node sends
	// by itself) with the launcher and model it runs on, and what
	// the old main conversation left before the text of the first one to a new main conversation started empty.
	MethodSendParams = "assistant.send_params"
	// MethodRename: RenameParams → the node's answer: the user renames one of its conversations in the app (the main
	// one has no title to change, a Codex one only its tab); the new title is the user's from now on.
	MethodRename = "assistant.rename"
)

// The node's methods the assistant calls for its directory, its conversations and its settings
// (docs/protocol/protocol.md "hub ↔ node"), passed through by the hub's device.call like the others.
const (
	// DeviceAssistantPrepare: {dir, claude_md, connectors, clis, plugin_skills} → {cwd, plugin_skills_need}.
	DeviceAssistantPrepare   = "assistant.prepare"
	DeviceAssistantExecution = "assistant.execution" // {session_id} → proto.AssistantExecution
	// DeviceAssistantGuestPrepare: {dir, claude_md} → {cwd}: a guest conversation's directory (assistant/guest.go), only
	// its CLAUDE.md / AGENTS.md written; the assistant's connectors, CLIs and skills are left as they are. A node without
	// it answers "unknown method": no guest conversation starts there.
	DeviceAssistantGuestPrepare = "assistant.guest_prepare"
	// DeviceAssistantGuestDelete: {dir, session_id} → {deleted}: a meeting's room removed at its transcript's cut or
	// with its recording (docs/assistant/meeting-minutes.md "Deletion"): its process, transcript and directory. With
	// kind meeting, assistant.guest_prepare answers {cwd, kind, start_ids} on a node that starts rooms; an older node
	// answers {cwd} only, or "unknown method" for this one.
	DeviceAssistantGuestDelete = "assistant.guest_delete"
	// DeviceAssistantWarm: {cwd, launcher, …model} → {}: a process ready for the next new conversation.
	DeviceAssistantWarm = "assistant.warm"
	// DeviceAssistantMemory: {op, cwd, name?, content?} → the memory files (op list) or {}.
	DeviceAssistantMemory = "assistant.memory"
	// DeviceAssistantCompact: {session_id} → {}.
	DeviceAssistantCompact = "assistant.compact"
	// DeviceAssistantHandoff: {session_id} → {summary, recent}: what a main conversation leaves for the next one.
	DeviceAssistantHandoff = "assistant.handoff"
	// DeviceLaunchersList: {} → {launchers}, the node's own.
	DeviceLaunchersList = "launchers.list"
	// DeviceLaunchersProbe: {name} → {detail}.
	DeviceLaunchersProbe = "launchers.probe"
)

// StartParams is assistant.start's params.
type StartParams struct {
	Text           string        `json:"text"`
	Images         []proto.Image `json:"images,omitempty"`
	Launcher       string        `json:"launcher,omitempty"`
	ConfigDir      string        `json:"config_dir,omitempty"` // Claude profile on the target machine; "" private, "legacy" old resolution, "default" unset
	PermissionMode string        `json:"permission_mode,omitempty"`
	Effort         string        `json:"effort,omitempty"`
	Purpose        string        `json:"purpose,omitempty"`    // the model configuration's purpose; "" is the main chat's
	MachineID      string        `json:"machine_id,omitempty"` // "" is the assistant's machine
	// Space: "health" starts it in the health space (its own directory, instructions and memory, docs/assistant/health.md);
	// "" is the assistant's own.
	Space string `json:"space,omitempty"`
	// Main: it becomes the main conversation (what the old one left goes before Text).
	Main bool `json:"main,omitempty"`
	// Untitled: not one of the conversations it names and lists as its own (the inbox's AI twin: its result is the
	// draft in the inbox).
	Untitled bool `json:"untitled,omitempty"`
	// Origin and OriginRef: who opens it (SessionView.Origin); "" is the user's.
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`
	// OpenedBy: the extension whose token opened it (SessionView.OpenedBy). Only POST /api/v1/assistant/conversations
	// sets it, from the hub's headers; never over the bus.
	OpenedBy string `json:"-"`
	// GuestTools: a guest conversation (assistant/guest.go) that may use only these connector tools; nil is an
	// ordinary one.
	GuestTools []string `json:"guest_tools,omitempty"`
	// NewSessionID: start it under this session id (a meeting's follow-up, made once also across a restart); a node
	// that cannot picks its own. Never over the bus.
	NewSessionID string `json:"-"`
	// ClientMsgID: the first message's client_msg_id (with NewSessionID: the node sends it once and its transcript
	// item carries it, as a send's; without, the node ties it to the new conversation's first line). Never over the bus.
	ClientMsgID string `json:"-"`
	// Human: a person's own app opens it, as the hub vouched (HTTPRequest.Human): its first message gets a
	// client_msg_id (FirstMessageIDPrefix) and counts as theirs, as a send the hub vouched for does (SendParams.Human).
	// Never over the bus.
	Human bool `json:"-"`
}

// FirstMessageIDPrefix begins the client_msg_id the manager gives the first message of a conversation a person's own
// app opened (StartParams.Human). It is the manager's alone: the hub refuses an app's send that names one, so no later
// message can carry it; a channel's ids (<channel>-<message>) never begin with it.
const FirstMessageIDPrefix = "start:"

// SendParams is assistant.send_params's params: Params are the node's assistant.send / session.send params. Lang is
// the app's language, the one a refusal is said in ("": the user's; an older hub sends none).
type SendParams struct {
	MachineID string         `json:"machine_id"`
	SessionID string         `json:"session_id"`
	Params    map[string]any `json:"params"`
	Lang      string         `json:"lang,omitempty"`
	// Human is the hub's word that a person's own app sent this message: the request's credential is a full device's
	// (never an extension's, a computer link's or the hub's own token). Set by the hub's auth, never from the request;
	// the manager's own sends (channels, the inbox, meetings) never set it, and an older hub never sends it. A hand-off's
	// Always rule spares the card only for a turn of such messages (assistant secret_handoff.go).
	Human bool `json:"human,omitempty"`
}

// RenameParams is assistant.rename's params: Params are the app's ({title?, tab_name?}).
type RenameParams struct {
	MachineID string         `json:"machine_id"`
	SessionID string         `json:"session_id"`
	Params    map[string]any `json:"params"`
	// Extension is the extension whose token asked, as the hub's auth found it (never what the caller says), "" for
	// anyone else: its title is a hint the hub names the conversation by, not the user's (docs/protocol/protocol.md
	// "Origin of assistant chats"). An older hub never sends it, and an older manager takes every rename as the user's.
	Extension string `json:"extension,omitempty"`
}

// CodeBadRequest is what the assistant refuses a request of the hub's with when the app's request itself is wrong
// (the hub answers it 400 with the message).
const CodeBadRequest = "bad_request"

// What the hub's apps tell the assistant for its directory and its settings.
const (
	// ConnectorsSignedIn: {} → {connectors, clis, lark_cli}: the connectors switched on and signed in (rate limited
	// counts), their command lines (name → connector id), and whether hub.json sets up lark-cli.
	ConnectorsSignedIn = "connectors.signed_in"

	// AppPlugins is the hub's plugins (docs/extensions/extensions.md "Assistant").
	AppPlugins = "plugins"
	// PluginsAssistant: {machine_id} → {instructions, skills}: the plugins' parts of the assistant's instructions, and
	// their skills for that machine (files only with those it was not sent yet).
	PluginsAssistant = "plugins.assistant"
	// PluginsSkillsNeed: {machine_id, names} → {}: the machine's node lacks these skills (its assistant.prepare said
	// plugin_skills_need): their files go next time.
	PluginsSkillsNeed = "plugins.skills_need"

	// SettingsList: {} → {settings: [Setting]}: the hub's own settings persona_settings offers; app.tool persona_settings
	// runs get / set / add / remove on them (a change waits for the user's approval on the phone).
	SettingsList = "settings.list"
)

// Setting is one of the hub's settings persona_settings offers.
type Setting struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	About   string   `json:"about"`
	Value   string   `json:"value,omitempty"` // what set / add take, for the model
	Actions []string `json:"actions"`
}
