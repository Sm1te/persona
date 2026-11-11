package assistantwire

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- what the hub sends the assistant (Handler) ----

// RoleHeader carries the role of the device a relayed request came from (the hub's auth decides it and sets the
// header, replacing any the client sent). Absent (a test's own request) means a full device.
const RoleHeader = "X-Persona-Role"

// ExtensionHeader carries the extension a relayed request came from when RoleHeader is "extension" (set by the hub
// like RoleHeader; never the client's).
const ExtensionHeader = "X-Persona-Extension"

// AdminHeader is "1" on a relayed request that came through the hub's admin UI with a signed-in admin session (the
// hub's admin routes it relays, devicewire.AdminUsageRoutes); set by the hub like RoleHeader, never the client's.
// Its value is the one extwire.HeaderAdmin carries on the extensions' relay.
const AdminHeader = "X-Persona-Admin"

// DeviceNameHeader carries the name of the device a relayed request came from ("" for the hub's own token), so a
// manager can say who did something (a model profile's switch, docs/assistant/model-profiles.md); set by the hub like
// RoleHeader, never the client's.
const DeviceNameHeader = "X-Persona-Device-Name"

// HandoffApprover is the device the hub found may approve a hand-off card (proto.SecretHandoffTool): a full device
// whose app shows the card (docs/extensions/secret-providers.md "Who may approve a hand-off"). It travels in
// HTTPRequest, never in a header, so no client can set it, and an older hub never sends it: a manager approves a
// hand-off only with it.
type HandoffApprover struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name,omitempty"`
}

type handoffApproverKey struct{}

// WithHandoffApprover is ctx with the device the hub found may approve a hand-off card.
func WithHandoffApprover(ctx context.Context, a HandoffApprover) context.Context {
	return context.WithValue(ctx, handoffApproverKey{}, a)
}

// HandoffApproverOf is the device WithHandoffApprover put on ctx; false when there is none.
func HandoffApproverOf(ctx context.Context) (HandoffApprover, bool) {
	a, ok := ctx.Value(handoffApproverKey{}).(HandoffApprover)
	return a, ok && a.DeviceID != ""
}

// ToolCall is assistant.tool's params.
type ToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	MachineID string          `json:"machine_id"` // the machine of the conversation that called it
	SessionID string          `json:"session_id"` // the conversation; "" when it is not known
}

// SessionRef names one session on one machine.
type SessionRef struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

// ContextParams is assistant.context's params. MachineID is the asking node's machine: the hub sets it from the
// node's connection, never from the node's params. With Ack the call only confirms that a message carrying those
// view errors (ContextResult.ViewErrors) was delivered, and answers nothing.
type ContextParams struct {
	SessionID string             `json:"session_id"`
	MachineID string             `json:"machine_id,omitempty"`
	Ack       []ViewErrorReceipt `json:"ack,omitempty"`
}

// ContextResult is assistant.context's answer: the <persona-context> text, and the <persona-view-errors> block of the
// conversation's rich views that failed their check and were not reported yet (docs/assistant/assistant-views.md "Next-turn feedback").
// Reading it consumes nothing: the node acks Receipts once the message is delivered.
type ContextResult struct {
	Text       string             `json:"text"`
	ViewErrors string             `json:"view_errors,omitempty"`
	Receipts   []ViewErrorReceipt `json:"view_error_receipts,omitempty"`
}

// ViewErrorReceipt names the reported errors of one reply as they were read: an ack whose digest no longer matches
// (the errors changed since) confirms nothing.
type ViewErrorReceipt struct {
	ItemID string `json:"item_id"`
	Digest string `json:"digest"`
}

// Action is manager.action's params (appkit.Action): op answer, with the user's answer (a proto.Resolution).
type Action struct {
	ActionID           string          `json:"action_id"`
	SessionID          string          `json:"session_id,omitempty"`
	Op                 string          `json:"op"` // answer
	TurnID             string          `json:"turn_id,omitempty"`
	InteractionID      string          `json:"interaction_id"`
	InteractionVersion uint64          `json:"interaction_version,omitempty"`
	Answer             json.RawMessage `json:"answer,omitempty"`
}

// ActionResult is manager.action's answer.
type ActionResult struct {
	OK  bool   `json:"ok"`
	Seq uint64 `json:"seq"`
}

// InteractionResolved is interaction.resolved's params: the card, the answer, and resolved / dismissed / failed.
type InteractionResolved struct {
	Interaction proto.Interaction `json:"interaction"`
	Resolution  proto.Resolution  `json:"resolution"`
	Status      string            `json:"status"`
}

// InteractionRaised is interaction.raised's params: an agent of a quiet task (#1731) asks the user something; the
// hub sends no push for it, the main conversation is told instead. The card carries its task's title.
type InteractionRaised struct {
	Interaction proto.Interaction `json:"interaction"`
}

// MessageCard is a message the inbox's AI twin drafted a reply to, about one of the user's tasks: what the card in the
// task's conversation shows until the item is sent or done (the app sends the draft through the inbox, by the item's
// id).
type MessageCard struct {
	TodoID  string `json:"todo_id"`
	Source  string `json:"source"`            // lark | mail | …
	Account string `json:"account,omitempty"` // Lark / the mail address
	Chat    string `json:"chat"`              // the chat's name / the mail's subject
	Sender  string `json:"sender,omitempty"`
	Text    string `json:"text"` // the message, clipped
	Draft   string `json:"draft"`
	// MessageVersion and ProgressVersion identify the source message and task
	// progress used to produce the draft, independently of its wording.
	MessageVersion  string `json:"message_version,omitempty"`
	ProgressVersion string `json:"progress_version,omitempty"`
	// SentAt: the reply to the item went out (from this card or from the inbox, on any device) and Sent is what it
	// said; set when the inbox sent the reply (the inbox is the assistant's too), never cleared, so the card stays sent
	// when the item opens again. A card sent before that was recorded has no Sent (the draft is what went out).
	SentAt *time.Time `json:"sent_at,omitempty"`
	Sent   string     `json:"sent,omitempty"`
	// Dismissal is per draft card, not inbox filing or task completion. Undo
	// removes these two fields but retains DismissalUpdatedAt for incremental reads.
	DismissedAt        *time.Time `json:"dismissed_at,omitempty"`
	DismissalID        string     `json:"dismissal_id,omitempty"`
	DismissalUpdatedAt *time.Time `json:"dismissal_updated_at,omitempty"`
}

// APIErrorMarkers start what Claude Code answers when the model call itself failed (an error card in the app). Both
// sides read a turn by them: the hub's session lists say an agent's turn was cut off (proto.Session.TurnError), the
// assistant's scheduled runs and tasks act on it. "Not logged in · Please run /login" is what Claude Code answers when
// it has no credentials (a model of its own, "<synthetic>"): the turn did not run, the user must sign in
// (docs/assistant/onboarding.md 4.4). Its separator is in the marker, so a reply that talks about being logged out is a reply.
var APIErrorMarkers = []string{"API Error:", "There's an issue with the selected model", "Failed to authenticate",
	"OAuth token has expired", "Invalid API key", "Not logged in ·"}

// refusalMarkers start the other lines Claude Code writes itself instead of an answer when the request was refused
// before the model ran: a sign-in that is gone ("Please run /login · API Error: 403", "Login expired · Please run
// /login"); a subscription's usage limit is its own line (UsageLimitText). Newer versions mark every such
// line in their record (isApiErrorMessage, read by the node into proto.Session.TurnError); these read an older node's
// list. Only as the start of the answer, never inside one (a reply may quote them).
var refusalMarkers = []string{"Please run /login", "Login expired ·", "OAuth token revoked ·", "Login reached its time limit ·"}

// InterruptedSummary is what is said of a turn the model service cut off, instead of its last words (the error is no
// conclusion, #1516): an interrupted task's receipt, the push.
const InterruptedSummary = "模型服务出错，中断了"

// CutOff: an agent's answer is the model API error that ended its turn (it starts with one, APIErrorMarkers or
// refusalMarkers, or is Claude Code's usage-limit line).
func CutOff(answer string) bool {
	answer = strings.TrimSpace(answer)
	return startsWithAny(answer, APIErrorMarkers) || startsWithAny(answer, refusalMarkers) || UsageLimitText(answer)
}

func startsWithAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// StartEnded is session.start_ended's params: how an app's start that the node ran before answering went (a resume, a
// Codex, an older app; one answered at once is followed in the session lists), for launcher health
// (docs/server/launcher-health.md). Launcher is the one the start ran ("" : claude); Error and Code, Data are the
// node's answer when it failed (its rpc.Error's code and data, when it had them).
type StartEnded struct {
	MachineID string          `json:"machine_id"`
	SessionID string          `json:"session_id,omitempty"`
	Launcher  string          `json:"launcher,omitempty"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Code      string          `json:"code,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// DeviceSessions is sessions.changed's params: the list as the node sent it, and when the node's connection to the hub
// began (a new one: the node may have restarted and forgotten what it was told, e.g. the herdr todo marks).
type DeviceSessions struct {
	MachineID string          `json:"machine_id"`
	Sessions  []proto.Session `json:"sessions"`
	Connected time.Time       `json:"connected,omitzero"`
}
