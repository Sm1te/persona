// Package assistant is the Persona tab's assistant (docs/assistant/assistant.md), a process of its own (`personad assistant`):
// its conversations (titles, archive, messages between them), what it does on its own (scheduled tasks), the tasks
// it hands out (plans, the conversation's feed, receipts, the "Today" card), its MCP tools and the live context each
// message starts with. Its runtime (the headless Claude / Codex) is a node's (node/assistant*.go); the hub is only
// the channel between it and the apps.
//
// The package never imports the hub. It reaches the hub through Hub alone, whose methods are bus methods
// (docs/protocol/protocol.md "Bus", the shapes in internal/assistantwire), and the hub reaches it through Handler
// (handler.go). BusHub (bus.go) is both on the bus connection; Main (main.go) is the process.

package assistant

import (
	"context"
	"encoding/json"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// Hub is everything the assistant asks of the hub.
type Hub interface {
	// Hello is manager.hello's answer: hub.json's assistant section (the defaults of the assistant's own settings) and
	// the machines. BusHub keeps the last one and takes manager.updated.
	Hello(ctx context.Context) (Hello, error)
	// DeviceCall is device.call {device, method, params}: a call to one of the user's machines (assistantwire.Device*).
	DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error)
	// AppCall is app.call {app_id, method, params}: a call to an app the hub runs (App*).
	AppCall(ctx context.Context, p AppCall) (json.RawMessage, error)
	// NotifyRequest is notify.request {notification} → {ok}: a push for the user (nil error is ok).
	NotifyRequest(ctx context.Context, p NotifyRequest) error
	// Broadcast is ws.broadcast {message}: a live update the apps get as it is (assistant_schedule, assistant_feed,
	// task_plan). One way: nothing comes back.
	Broadcast(ctx context.Context, b Broadcast)
	// Snapshot is manager.snapshot: what the assistant reports for the apps. The hub caches the latest and shows it.
	Snapshot(ctx context.Context, s Snapshot) (SnapshotResult, error)
	// InboxChanged is inbox.changed: inbox items changed (inbox.go); the hub tells the extensions and the apps. One way.
	InboxChanged(ctx context.Context, c assistantwire.InboxChanged)
	// Tell is one of the manager's one-way events to the hub (fleet.*: what the apps get of the nodes, fleet.go);
	// an error: not connected.
	Tell(event string, params any) error
}

// What crosses the bus is assistantwire's; these names keep the package's code as it was.
type (
	Hello               = assistantwire.Hello
	Placement           = assistantwire.Placement
	DeviceCall          = assistantwire.DeviceCall
	Device              = assistantwire.Device
	ClientApp           = assistantwire.ClientApp
	ConfiguredLauncher  = assistantwire.ConfiguredLauncher
	HubModels           = assistantwire.HubModels
	AppCall             = assistantwire.AppCall
	ModelBinding        = assistantwire.ModelBinding
	NotifyRequest       = assistantwire.NotifyRequest
	Notification        = assistantwire.Notification
	Broadcast           = assistantwire.Broadcast
	Snapshot            = assistantwire.Snapshot
	TaskView            = assistantwire.TaskView
	SessionView         = assistantwire.SessionView
	SnapshotResult      = assistantwire.SnapshotResult
	ToolCall            = assistantwire.ToolCall
	SessionRef          = assistantwire.SessionRef
	ContextParams       = assistantwire.ContextParams
	ContextResult       = assistantwire.ContextResult
	Action              = assistantwire.Action
	ActionResult        = assistantwire.ActionResult
	InteractionResolved = assistantwire.InteractionResolved
	InteractionRaised   = assistantwire.InteractionRaised
	MessageCard         = assistantwire.MessageCard
	DeviceSessions      = assistantwire.DeviceSessions
	StartEnded          = assistantwire.StartEnded
	ConversationSent    = assistantwire.ConversationSent
	HTTPRequest         = assistantwire.HTTPRequest
	HTTPResponse        = assistantwire.HTTPResponse
	Profile             = assistantwire.Profile
	StartParams         = assistantwire.StartParams
	SendParams          = assistantwire.SendParams
	RenameParams        = assistantwire.RenameParams
	Setting             = assistantwire.Setting
	KBSettings          = assistantwire.KBSettings
)

// The channels' (channels.go, feishu.go).
type (
	ChannelsReport       = assistantwire.ChannelsReport
	ChannelMark          = assistantwire.ChannelMark
	ChannelTurn          = assistantwire.ChannelTurn
	ChannelReceive       = assistantwire.ChannelReceive
	ChannelUnbound       = assistantwire.ChannelUnbound
	ChannelNoted         = assistantwire.ChannelNoted
	FeishuMessage        = assistantwire.FeishuMessage
	FeishuAction         = assistantwire.FeishuAction
	FeishuView           = assistantwire.FeishuView
	FeishuAgent          = assistantwire.FeishuAgent
	FeishuOwner          = assistantwire.FeishuOwner
	FeishuRecalled       = assistantwire.FeishuRecalled
	FeishuFeatures       = assistantwire.FeishuFeatures
	FeishuStatus         = assistantwire.FeishuStatus
	InteractionAnnounced = assistantwire.InteractionAnnounced
	InteractionEnded     = assistantwire.InteractionEnded
	SessionItems         = assistantwire.SessionItems
	SessionDelta         = assistantwire.SessionDelta
)

const (
	DeviceSessionFollow   = assistantwire.DeviceSessionFollow
	DeviceSessionUnfollow = assistantwire.DeviceSessionUnfollow
	DeviceSessionSendOnce = assistantwire.DeviceSessionSendOnce
	AppFeishu             = assistantwire.AppFeishu
	AppNotify             = assistantwire.AppNotify
	NotifyQuietGet        = assistantwire.NotifyQuietGet
	FeishuStatusGet       = assistantwire.FeishuStatusGet
	FeishuPost            = assistantwire.FeishuPost
	FeishuText            = assistantwire.FeishuText
	FeishuUpdateCard      = assistantwire.FeishuUpdateCard
	FeishuPin             = assistantwire.FeishuPin
	FeishuRecall          = assistantwire.FeishuRecall
	FeishuCotCreate       = assistantwire.FeishuCotCreate
	FeishuCotAppend       = assistantwire.FeishuCotAppend
	FeishuCotComplete     = assistantwire.FeishuCotComplete
	CodeFeishuAPI         = assistantwire.CodeFeishuAPI
	CodeChatDenied        = assistantwire.CodeChatDenied
	FeishuFromElsewhere   = assistantwire.FeishuFromElsewhere
	FeishuReact           = assistantwire.FeishuReact
	FeishuUnreact         = assistantwire.FeishuUnreact
	FeishuMessages        = assistantwire.FeishuMessages
	FeishuResource        = assistantwire.FeishuResource
	FeishuSave            = assistantwire.FeishuSave
	FeishuUserName        = assistantwire.FeishuUserName
	FeishuConfigure       = assistantwire.FeishuConfigure
	FeishuTest            = assistantwire.FeishuTest
	AppChannels           = assistantwire.AppChannels
	ChannelsList          = assistantwire.ChannelsList
	ChannelsSend          = assistantwire.ChannelsSend
	ChannelsTyping        = assistantwire.ChannelsTyping
	ChannelsEnable        = assistantwire.ChannelsEnable
	ChannelsUnbind        = assistantwire.ChannelsUnbind
	InteractionsGet       = assistantwire.InteractionsGet
	InteractionsResolve   = assistantwire.InteractionsResolve
	CodeCannotSend        = assistantwire.CodeCannotSend
	CodeTryAgain          = assistantwire.CodeTryAgain
)

// The todos' (todos.go).
type (
	TodoReport        = assistantwire.TodoReport
	TodoLink          = assistantwire.TodoLink
	TodoHandoff       = assistantwire.TodoHandoff
	TodoHandoffResult = assistantwire.TodoHandoffResult
	TodoLinkParams    = assistantwire.TodoLinkParams
	TodoLinkResult    = assistantwire.TodoLinkResult
	TodoSpaceRenamed  = assistantwire.TodoSpaceRenamed
)

const (
	DeviceSessionItems       = assistantwire.DeviceSessionItems
	DeviceSessionRename      = assistantwire.DeviceSessionRename
	DeviceAssistantSend      = assistantwire.DeviceAssistantSend
	DeviceSessionSend        = assistantwire.DeviceSessionSend
	DeviceSessionStart       = assistantwire.DeviceSessionStart
	DeviceWorktreePrepare    = assistantwire.DeviceWorktreePrepare
	DeviceWorktreeCreate     = assistantwire.DeviceWorktreeCreate
	DeviceDevicesList        = assistantwire.DeviceDevicesList
	DeviceInteractionsList   = assistantwire.DeviceInteractionsList
	DeviceSessionsList       = assistantwire.DeviceSessionsList
	DeviceSessionGet         = assistantwire.DeviceSessionGet
	DeviceAssistantPrepare   = assistantwire.DeviceAssistantPrepare
	DeviceAssistantWarm      = assistantwire.DeviceAssistantWarm
	DeviceAssistantMemory    = assistantwire.DeviceAssistantMemory
	DeviceAssistantCompact   = assistantwire.DeviceAssistantCompact
	DeviceAssistantHandoff   = assistantwire.DeviceAssistantHandoff
	DeviceLaunchersList      = assistantwire.DeviceLaunchersList
	DeviceLaunchersProbe     = assistantwire.DeviceLaunchersProbe
	DeviceLaunchersManaged   = assistantwire.DeviceLaunchersManaged
	ConnectorsSignedIn       = assistantwire.ConnectorsSignedIn
	AppPlugins               = assistantwire.AppPlugins
	PluginsAssistant         = assistantwire.PluginsAssistant
	PluginsSkillsNeed        = assistantwire.PluginsSkillsNeed
	SettingsList             = assistantwire.SettingsList
	AppConnectors            = assistantwire.AppConnectors
	ConnectorsSnapshots      = assistantwire.ConnectorsSnapshots
	AppInbox                 = assistantwire.AppInbox
	AppInteractions          = assistantwire.AppInteractions
	AppSettings              = assistantwire.AppSettings
	AppTool                  = assistantwire.AppTool
	MethodHello              = assistantwire.MethodHello
	MethodDevice             = assistantwire.MethodDevice
	MethodApp                = assistantwire.MethodApp
	MethodNotify             = assistantwire.MethodNotify
	MethodSnapshot           = assistantwire.MethodSnapshot
	EventBroadcast           = assistantwire.EventBroadcast
	MethodHTTP               = assistantwire.MethodHTTP
	MethodTools              = assistantwire.MethodTools
	MethodTool               = assistantwire.MethodTool
	MethodContext            = assistantwire.MethodContext
	MethodAction             = assistantwire.MethodAction
	MethodStart              = assistantwire.MethodStart
	MethodSendParams         = assistantwire.MethodSendParams
	MethodRename             = assistantwire.MethodRename
	CodeBadRequest           = assistantwire.CodeBadRequest
	EventUserSpoke           = assistantwire.EventUserSpoke
	EventUserStopped         = assistantwire.EventUserStopped
	EventTurnStarted         = assistantwire.EventTurnStarted
	EventTurnEnded           = assistantwire.EventTurnEnded
	EventSessionsChanged     = assistantwire.EventSessionsChanged
	EventInteractionResolved = assistantwire.EventInteractionResolved
	EventInteractionRaised   = assistantwire.EventInteractionRaised
	EventUpdated             = assistantwire.EventUpdated
	EventResync              = assistantwire.EventResync
	EventStartEnded          = assistantwire.EventStartEnded
	CodeNoDevice             = assistantwire.CodeNoDevice
	CodeMachineOffline       = assistantwire.CodeMachineOffline
	CodeNodeClosed           = assistantwire.CodeNodeClosed
	CodeMeetingEnded         = assistantwire.CodeMeetingEnded
	CodeStaleSnapshot        = assistantwire.CodeStaleSnapshot
	CodeAssistantOffline     = assistantwire.CodeAssistantOffline
	DeviceAll                = assistantwire.DeviceAll
	NotifyPush               = assistantwire.NotifyPush
	RoleHeader               = assistantwire.RoleHeader
	ExtensionHeader          = assistantwire.ExtensionHeader
	AssistantID              = assistantwire.AssistantID
)

// DeviceAssistantGuestPrepare makes a guest conversation's directory (guest.go); DeviceAssistantGuestDelete removes a
// meeting's room (meeting_room.go).
const (
	DeviceAssistantGuestPrepare = assistantwire.DeviceAssistantGuestPrepare
	DeviceAssistantGuestDelete  = assistantwire.DeviceAssistantGuestDelete
)

var (
	ErrNoDevice      = assistantwire.ErrNoDevice
	ErrStaleSnapshot = assistantwire.ErrStaleSnapshot
	Routes           = assistantwire.Routes
)
