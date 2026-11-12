// Package assistantwire is what the hub and the assistant (internal/assistant, its own process) say to each other
// over the bus (docs/protocol/protocol.md "Bus", docs/assistant/assistant.md "Boundary with hub"): each method's and event's name, params
// and result, the error codes that cross it, and the routes the hub relays. Both sides import it; neither imports
// the other.
package assistantwire

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Hello is manager.hello's answer, and manager.updated's params (the answer again when it changed): what the hub
// knows that the manager needs (its machines, the cards' switches, the push timings, the user's language). The
// manager's own sections (Owner, OwnerNotes, Agents, KB, the models but Launchers, a card's wait) are not sent by a hub
// that knows config.own: the manager lays its manager.json over the hello (assistant/ownconfig.go); an older hub still
// sends its hub.json's, which a manager.json section replaces. Its own settings are its own (Profile).
type Hello struct {
	Owner string `json:"owner"`
	// OwnerNotes is manager.json's assistant.notes: what the assistant knows about the user until "About me" is edited.
	OwnerNotes string `json:"owner_notes"`
	// Assistant is where the hub would run the assistant's conversations by default.
	Assistant Placement `json:"assistant"`
	// DoneDelayMS is how long a finished turn stays still before it counts as the end (hub.json notify): a task's
	// agents are followed with the same patience as the hub's own pushes.
	DoneDelayMS int64 `json:"done_delay_ms"`
	// PersonaView is hub.json's assistant.persona_view: teach ```persona-view rather than ```persona-card (#1850).
	PersonaView bool `json:"persona_view,omitempty"`
	// HTMLViews is hub.json's assistant.html_views (default on) while PersonaView is on: the HTML bridge
	// (…/views/{key}/html-actions) answers. An older hub never sends it, so the bridge stays off there.
	HTMLViews bool `json:"html_views,omitempty"`
	// Agents is manager.json's agents section: the triage's limits and default switches (agents.go).
	Agents AgentsConfig `json:"agents,omitzero"`
	// KB is manager.json's kb section: the knowledge base the assistant keeps (kb.go).
	KB *KBSettings `json:"kb,omitempty"`
	// Models is what hub.json says about models (models.go).
	Models HubModels `json:"models,omitzero"`
	// Locale is the user's language (zh-Hans / en / ja, docs/protocol/i18n.md): the assistant answers in it. "" (an older hub)
	// is zh-Hans.
	Locale string `json:"locale,omitempty"`
	// Mutes are the muted spaces / agents (the hub's settings mutes) of the manager's own machines only (as
	// fleet.mutes), so a manager has them before anything it pushes; nil from an older hub or while the hub cannot
	// read them (fleet.mutes brings them later).
	Mutes *Mutes `json:"mutes,omitempty"`
	// LegacyConfig is what of the hub's hub.json the hello carried before config.own (managerconf.HelloSections:
	// owner, notes, Agents, kb, ai, a card's wait, the inbox's models and AI twin machine), raw. Only in the handshake's
	// answer to a built-in manager the hub did not start (its hello's deploy is not "child"), which takes them over into
	// its manager.json once (managerconf.MoveFrom); the hub's child took them from this hub.json itself.
	LegacyConfig json.RawMessage `json:"legacy_config,omitempty"`
	// Notify is hub.json's notify: the finished push's timings; HookTimeoutMS, how long a hook's card waits by default,
	// is manager.json's hook_timeout_minutes, over the hello (the interactions and the pushes are the manager's).
	Notify NotifyTimings `json:"notify,omitzero"`
	// SecretHandoffGate: the hub lets only a device that may approve a hand-off card say yes to one, and names it in
	// HTTPRequest.HandoffApprover. An older hub never sends it; a manager then shows no hand-off card at all.
	SecretHandoffGate bool `json:"secret_handoff_gate,omitempty"`
}

// NotifyTimings are hub.json's notify section, and manager.json's hook_timeout_minutes, in milliseconds (0: the
// default).
type NotifyTimings struct {
	DoneDelayMS      int64 `json:"done_delay_ms,omitempty"`
	LongTurnMS       int64 `json:"long_turn_ms,omitempty"`
	BackgroundHoldMS int64 `json:"background_hold_ms,omitempty"`
	HookTimeoutMS    int64 `json:"hook_timeout_ms,omitempty"`
}

// Placement is the hub's defaults for where the assistant runs.
type Placement struct {
	// MachineID is manager.json's assistant.machine when the hub has it, else the hub's own machine, else the first one;
	// "" when the hub has no machine.
	MachineID string `json:"machine_id"`
	// Launcher is manager.json's assistant.launcher ("" is claude).
	Launcher string `json:"launcher,omitempty"`
	// Machines are the machines the hub has: a machine the assistant saved that is none of them is not used.
	Machines []string `json:"machines,omitempty"`
	// WorkMachines are configured work computers, including aliases; none may host the assistant.
	WorkMachines []string `json:"work_machines,omitempty"`
	// Standby: this hub is a member of a mesh group, whose center runs the scheduled tasks (docs/server/mesh.md).
	Standby bool `json:"standby,omitempty"`
}

// DeviceCall is device.call's params. Device is a machine id, or DeviceAll for DeviceDevicesList.
type DeviceCall struct {
	Device string          `json:"device"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// The machine methods the assistant calls. The node's own methods go to the machine's node as they are; the hub
// answers the others from what it holds about the machine.
const (
	// The node's (docs/protocol/protocol.md "hub ↔ node").
	DeviceSessionItems  = "session.items"  // {session_id, turns} → {items}
	DeviceSessionRename = "session.rename" // {session_id, title} → {ok}
	DeviceAssistantSend = "assistant.send" // {session_id, text, resume_only?, permission_mode?, …model} → {session_id}
	DeviceSessionSend   = "session.send"   // {session_id, text} → {}
	DeviceSessionStart  = "session.start"  // {cwd, prompt, launcher?, model?, permission_mode?, tab_name?, workspace_id?} → {session}
	// DeviceWorktreePrepare / DeviceWorktreeCreate: "New worktree…" (docs/integrations/terminal.md), the workspace named by a directory
	// in it: {cwd} → WorktreePrepare; {cwd, branch, path} → WorktreeCreateResult.
	DeviceWorktreePrepare = "worktree.prepare"
	DeviceWorktreeCreate  = "worktree.create"

	// The hub's.
	// DeviceDevicesList (on DeviceAll): {} → {devices: [Device], phone_online, apps: [ClientApp]}, the user's machines
	// and the Persona apps that last said their version (apps: absent from a hub from before).
	DeviceDevicesList = "devices.list"
	// DeviceInteractionsList (on DeviceAll): {} → {interactions}, what the machines' agents wait on the user for.
	DeviceInteractionsList = "interactions.list"
	// DeviceSessionsList: {} → {sessions}, the machine's sessions as the apps get them (state, titles).
	DeviceSessionsList = "sessions.list"
	// DeviceSessionGet: {session_id} → {session, ok}, one session as the node listed it.
	DeviceSessionGet = "session.get"
)

// DeviceAll is the device of the calls about every machine.
const DeviceAll = "*"

// Device is one of the user's machines as devices.list has it.
type Device struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`     // local (the hub's own) | ssh | link …
	SSHHost string `json:"ssh_host,omitempty"` // the hub machine's ssh alias for it
	Home    string `json:"home,omitempty"`     // "" while it has not reported one
	DataDir string `json:"data_dir,omitempty"` // its node's data directory ("" while not reported, or the default)
	Online  bool   `json:"online"`
	// Reachable: a call to it would go out now (its link is up; Online can lag a reconnect).
	Reachable bool `json:"reachable"`
	// Version is its node's build ("" while not reported); ModelConfiguration: that node takes a launcher's model
	// (--model) and effort, which "Models" needs of the machine a launcher purpose runs on.
	Version            string `json:"version,omitempty"`
	ModelConfiguration bool   `json:"model_configuration,omitempty"`
	// ClaudeVersion is Claude Code's version there ("" when it has none or never said): 设置 › 模型供应商's built-in
	// claude lists the machines that have it.
	ClaudeVersion string `json:"claude_version,omitempty"`
}

// ClientApp is one Persona app as it last described itself in client_hello (docs/protocol/protocol.md "Client
// capabilities and heartbeat"): one per signed-in device, newest first. Platform is ios (an iPad too: its OS says
// iPadOS) | macos | watchos | visionos | android; never the web, which the hub serves (its version is the hub's).
type ClientApp struct {
	Platform  string    `json:"platform"`
	Version   string    `json:"version"`         // the marketing version (CFBundleShortVersionString, versionName)
	Build     string    `json:"build,omitempty"` // the build number
	OS        string    `json:"os,omitempty"`    // "iOS 26.1"
	Simulator bool      `json:"simulator,omitempty"`
	SeenAt    time.Time `json:"seen_at"` // its last client_hello
}

// ErrNoDevice: the hub has no machine by the id a DeviceCall gave.
var ErrNoDevice = errors.New("no such machine")

// AppCall is app.call's params (appkit.RelayParams on the bus).
type AppCall struct {
	AppID  string          `json:"app_id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	// TimeoutMS is how long the caller waits for the answer (its context's deadline); the hub stops there too. 0: none.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

// The apps the hub runs for the assistant and their methods.
const (
	// AppConnectors is the hub's connectors (docs/extensions/connectors.md).
	AppConnectors = "connectors"
	// ConnectorsSnapshots: {tz} → {snapshots: [connectorwire.SnapshotData]}, the live states of the signed-in connectors
	// that keep one (the chats waiting on the user, today's meetings), those that answered within a few seconds.
	ConnectorsSnapshots = "connectors.snapshots"

	// AppInteractions is the hub's interaction queue (InteractionsGet, InteractionsAnswer).
	AppInteractions = "interactions"

	// AppInbox is the inbox's sources (inbox.go), answered in the manager's process by the extension manager
	// (internal/extmgr), not the hub; AppSettings is Persona's own settings (persona_settings).
	AppInbox    = "inbox"
	AppSettings = "settings"

	// AppNotify is the hub's push settings: NotifyQuietGet tells whether the user is in their quiet hours now.
	AppNotify = "notify"
	// NotifyQuietGet: {} → {quiet}: true while any of the user's devices is inside its quiet hours (#5806), read in
	// the hub's clock and the user's time zone. The Lark bot's nudges (#6084) wait for it to turn false.
	NotifyQuietGet = "notify.quiet"

	// AppTool: {name, arguments, machine_id, session_id} → proto.ToolResult, one of the assistant's tools that is the
	// app's to run (persona_settings).
	AppTool = "app.tool"
)

// What the interaction queue offers the assistant: data in, data out. The tools on it (interaction_answer) are the
// assistant's, with their rules and wording.
const (
	// InteractionsGet: {interaction_id} → {interaction, ok}, the interaction while it is pending; when it is not (ok
	// false), status says how it ended (resolved, dismissed, expired, cancelled; "" when there is no such interaction).
	InteractionsGet = "interactions.get"
	// InteractionsAnswer: {machine_id, session_id, interaction_id, answers: {question: answer}} → {ok, status}: the
	// pending question of that session resolved with answers, answered by the assistant; ok false (with status as
	// InteractionsGet's) when it is no longer pending. An interaction that is not a question, or not that session's,
	// is an error.
	InteractionsAnswer = "interactions.answer"
)

// ModelBinding is what a purpose runs on: a provider (launcher.<name> for a launcher) with a model and effort.
type ModelBinding struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Runtime  string `json:"runtime,omitempty"` // main chat only: cli (default) or agentsdk; kept across a provider change
}

// NotifyRequest is notify.request's params.
type NotifyRequest struct {
	Notification Notification `json:"notification"`
}

// Notification is one notification intent: a push (kind assistant) to every device whose settings take it, like an
// agent's. The hub delivers it as it is: it does not derive what it means.
type Notification struct {
	ID        string `json:"id"` // stable, for deduplication
	Kind      string `json:"kind"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body,omitempty"`
	Subtitle  string `json:"subtitle,omitempty"`  // the line under the title: where it comes from
	DeviceID  string `json:"device_id,omitempty"` // the machine of the session it opens
	SessionID string `json:"session_id,omitempty"`
	// Asked: the conversation asked for this push itself (notify_user): the conversation's own finished push is left
	// out. (The assistant sends it to the Feishu bot and the channels with pushes on itself.)
	Asked bool `json:"asked,omitempty"`
	// Interaction is a NotifyInput push's card.
	Interaction *proto.Interaction `json:"interaction,omitempty"`
	// TookMS and LongMS are a NotifyDone push's turn: how long it took, and from how long a turn counts as long (the
	// devices' "long turns only").
	TookMS int64 `json:"took_ms,omitempty"`
	LongMS int64 `json:"long_ms,omitempty"`
	// Todo is a NotifyDecide push's to-do and the round of its decision.
	Todo *NotifyTodo `json:"todo,omitempty"`
	// LinkCovered: a NotifyInput push's card is what the manager's Persona Link summary shows for its agent and rings
	// for (the card that stands for it, notifying, among the listed items), so a Persona Link desktop of the manager's
	// may stand in for the phone's push ("Phone Muted", docs/operations/link.md "Presence"). Older managers leave it
	// out: the phone is pushed.
	LinkCovered bool `json:"link_covered,omitempty"`
}

// NotifyTodo is the to-do a NotifyDecide push asks about: its id, when the decision now waiting was asked (what
// POST /api/v1/todos/{id}/accept takes, so an OK from an earlier round takes nothing) and the option it recommends.
type NotifyTodo struct {
	ID        string    `json:"id"`
	AskedAt   time.Time `json:"asked_at"`
	Recommend string    `json:"recommend"`
	// Options are the labels of the decision's options, the recommended first, and Responses the fixed answers it takes
	// besides them (decide_yourself, not_what_i_asked, explain_first); the accept route takes either as `option` or
	// `response`. Left out by a hub from before them.
	Options   []string `json:"options,omitempty"`
	Responses []string `json:"responses,omitempty"`
}

// NotifyPush is the kind of the assistant's pushes.
const NotifyPush = "assistant"

// Broadcast is ws.broadcast's params: a JSON object with a string "type", sent to the apps unchanged.
type Broadcast struct {
	Message json.RawMessage `json:"message"`
}

// Snapshot is manager.snapshot's params: the whole of what the assistant shows, every time (the hub keeps the
// newest by Seq and shows nothing it did not report).
type Snapshot struct {
	Epoch string    `json:"epoch"` // this run of the assistant
	Seq   uint64    `json:"seq"`   // increasing within Epoch
	At    time.Time `json:"at"`
	// Partial: some of the state could not be read (the database did not answer). The hub no longer reads it: it
	// used to ask again with manager.resync.
	Partial bool `json:"partial,omitempty"`
	// Sessions are the assistant's conversations it has something to say about.
	Sessions []SessionView `json:"sessions"`
	// Interactions are the questions it asks on the phone (ask_user: headless Claude has no AskUserQuestion), and its
	// cards about agents' turns (WorkCardTool). The hub shows each like any agent's and sends the answer back
	// (Handler.Action); one that leaves the list is withdrawn.
	Interactions []proto.Interaction `json:"interactions"`
	// Tasks are its scheduled tasks; null while they could not be read. The hub no longer keeps them: the model
	// configuration that read them is the manager's own now.
	Tasks []TaskView `json:"tasks"`
	// Todos is which todo each agent works for (TodoReport); null while the todos could not be read.
	Todos *TodoReport `json:"todos"`
	// Profile is its settings as the hub's own features need them; null while they could not be read.
	Profile *Profile `json:"profile"`
	// Work is where the agents stand after their turns (agents.go); null while it could not be read.
	Work *WorkReport `json:"work"`
	// Channels is what the hub shows of the channels (ChannelsReport); null while it could not be read.
	Channels *ChannelsReport `json:"channels"`
	// Inbox is what the hub shows of the inbox (InboxReport, inbox.go); null while it could not be read.
	Inbox *InboxReport `json:"inbox,omitempty"`
}

// TaskView is one scheduled task in the snapshot.
type TaskView struct {
	ID           string    `json:"id"`
	Label        string    `json:"label"`  // its title, or what stands for one
	Repeat       string    `json:"repeat"` // once / daily / interval / cron
	Enabled      bool      `json:"enabled"`
	At           time.Time `json:"at,omitempty"` // once: when; interval, cron: the next run
	EveryMinutes int       `json:"every_minutes,omitempty"`
	MachineID    string    `json:"machine_id,omitempty"` // of the conversation it came from
	RunMachineID string    `json:"run_machine_id,omitempty"`
	RunSessionID string    `json:"run_session_id,omitempty"` // its own conversation
	// Push is what of a run reaches the phone: "" every run's answer, PushAsked only what the run sends with
	// notify_user (#2396). Such a task always runs in its own conversation, whose finished turns are not pushed.
	Push string `json:"push,omitempty"`
}

// PushAsked is a scheduled task's push that sends only what its runs push themselves (TaskView.Push).
const PushAsked = "asked"

// FinishedText is the words the finished push carries for s's turn, chosen as hub/machine.go notifyFinished chooses
// them: the work line it ended on (done or stuck), else its last words; "" when it said nothing (the push then says a
// localised "done", which is never a cut-off). NormalFinish judges a cut-off on them.
func FinishedText(s proto.Session) string {
	if w := s.Work; w != nil && w.Line != "" && (w.Phase == proto.WorkDone || w.Phase == proto.WorkBlocked) {
		return w.Line
	}
	return s.LastText
}

// NormalFinish: s's turn ended normally, as the finished push would tell it: no model error, no cut-off answer, not
// stuck on something. A quiet task's conversation (PushAsked) pushes nothing then; an error, a cut-off or a stuck
// turn is told as ever.
func NormalFinish(s proto.Session) bool {
	return strings.TrimSpace(s.TurnError) == "" && !CutOff(FinishedText(s)) && !(s.Work != nil && s.Work.Phase == proto.WorkBlocked)
}

// QuietTaskFinish: s, the own conversation of a task that pushes only what its runs send, may go unpushed now. Only
// when it runs on an agent whose failed turn is told on the session (reportsFailures), so that NormalFinish can tell
// "nothing new" from an error. Never the main conversation (main: its session id), which the user may have made of
// the task's. And only a normal finish (NormalFinish).
func QuietTaskFinish(s proto.Session, main string) bool {
	return reportsFailures(s) && (main == "" || s.SessionID != main) && NormalFinish(s)
}

// SupervisedFinish: s, a conversation whose current turn a supervision message started (SessionView.Supervising), may go
// unpushed now: a normal finish (NormalFinish); an error, a cut-off or a stuck turn is pushed as ever.
func SupervisedFinish(s proto.Session, supervising bool) bool {
	return supervising && reportsFailures(s) && NormalFinish(s)
}

// reportsFailures: s's agent tells a turn that failed on the model service. Claude Code (Agent "") writes the error
// in its last words (CutOff); a headless Codex's node reports the failed turn/completed as TurnError (#2571). A
// runtime's (Agent: its id, DSH…) is not known to, so its quiet task is pushed as ever.
func reportsFailures(s proto.Session) bool {
	return s.Agent == "" || s.Agent == "codex"
}

// SessionView is what the assistant reports about one of its conversations, put on the session lists the apps get.
type SessionView struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	// Title is "MMDD | kind | topic" or the user's own (titles.go); the main conversation's is always "Persona".
	Title    string   `json:"title,omitempty"`
	Archived bool     `json:"archived,omitempty"`
	Queued   []string `json:"queued,omitempty"` // messages from other conversations waiting for its turn to end
	// Origin is who opened the conversation (docs/protocol/protocol.md "Origin of assistant chats": user, channel, inbox, task, spawned,
	// lark_agent, extension) and OriginRef what of (the inbox item, the task, the conversation, the channel, the
	// extension); both "" while it is not known.
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`
	// OpenedBy is the extension whose token opened it, by id: noted when it opened (StartParams.OpenedBy), never
	// backfilled, so "" for any other and for one opened before it was noted. Only that extension's token may change
	// its permission mode (hub/extension_modes.go).
	OpenedBy string `json:"opened_by,omitempty"`
	// Supervising: its current turn was started by a supervision message about the tasks it handed out (#3124): the assistant
	// does not push its normal finish (SupervisedFinish); it pushes what the user needs to know (notify_user).
	Supervising bool `json:"supervising,omitempty"`
	// Direction: the conversation is (or was) a leader, one direction of the owner's work (assistant/directions.go,
	// #3484); nil for any other.
	Direction *proto.Direction `json:"direction,omitempty"`
	// ModelPick: what the user set the conversation to run on (assistant/model_pick.go, #5033); nil follows the default.
	ModelPick *proto.ModelPick `json:"model_pick,omitempty"`
}

// ErrStaleSnapshot is manager.snapshot's stale_snapshot: a newer one came in first (two changes reported at once).
var ErrStaleSnapshot = errors.New("stale_snapshot: a newer snapshot came in")

// SnapshotResult is manager.snapshot's answer.
type SnapshotResult struct {
	OK  bool   `json:"ok"`
	Seq uint64 `json:"seq"`
}
