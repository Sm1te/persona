package proto

import (
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/dialogwire"
)

// Interaction kinds.
const (
	IxQuestion    = "question"
	IxPlan        = "plan"
	IxPermission  = "permission"
	IxScreen      = "screen"
	IxElicitation = "elicitation"
)

// AnsweredByAssistant marks a resolution the assistant gave for the user (Resolution.AnsweredBy).
const AnsweredByAssistant = "assistant"

// DismissedInChat is the Reason of a Dismiss for an assistant question taken back because the user wrote in the
// conversation instead of answering it (#9): channels say "replied in the chat" on its card, not "dismissed".
const DismissedInChat = "replied_in_chat"

// DismissedExpired is the Reason of a Dismiss the manager hands one of the assistant's own questions whose card expired
// unanswered (#1617): an ask_user that expires becomes a to-do instead. Only the manager sets it; the apps' answers have
// their Reason dropped (assistant/fleet_routes.go).
const DismissedExpired = "expired"

// AskedWithoutWaiting: ix is the assistant's ask_user raised with wait false (#4928, docs/assistant/assistant.md
// "Asking without holding the turn"): its turn ended when the card went up, so no turn is held behind it. It is the
// one ask_user card that is Resumable (a waiting one lives in the manager's memory and is not).
func (ix Interaction) AskedWithoutWaiting() bool { return ix.ToolName == "ask_user" && ix.Resumable }

// QuestionOptionDefer dismisses a triage question without sending an agent reply.
// Other question runtimes do not interpret this triage-only action.
const QuestionOptionDefer = "defer"

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Action      string `json:"action,omitempty"` // Optional triage intent; empty means a normal answer.
}

type Question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	MultiSelect bool             `json:"multi_select"`
	Options     []QuestionOption `json:"options"`
}

type Interaction struct {
	// Manager is the manager it belongs to (docs/assistant/managers.md, "Ownership"); the hub stamps it on what it sends the apps.
	Manager string `json:"manager,omitempty"`
	// Private: a Just me turn raised it (docs/assistant/meeting-minutes.md "Just me"): its push and the Fleet Live
	// Activity say nothing of what it asks, as a health conversation's card, and it never goes to Lark or a channel.
	Private         bool            `json:"private,omitempty"`
	ID              string          `json:"id"`
	MachineID       string          `json:"machine_id"`
	SessionID       string          `json:"session_id"`
	SessionTitle    string          `json:"session_title,omitempty"`
	TaskTitle       string          `json:"task_title,omitempty"` // the task its agent works for (docs/assistant/unified-entry.md), set when sent to clients
	Cwd             string          `json:"cwd,omitempty"`
	Kind            string          `json:"kind"`
	CreatedAt       time.Time       `json:"created_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	ToolName        string          `json:"tool_name,omitempty"`
	ToolUseID       string          `json:"tool_use_id,omitempty"`
	Questions       []Question      `json:"questions,omitempty"`
	Plan            string          `json:"plan,omitempty"`
	ToolInput       json.RawMessage `json:"tool_input,omitempty"`
	Summary         string          `json:"summary,omitempty"`
	Screen          string          `json:"screen,omitempty"`
	AgentID         string          `json:"agent_id,omitempty"`
	AgentType       string          `json:"agent_type,omitempty"`
	MCPServerName   string          `json:"mcp_server_name,omitempty"`
	MCPConfigSource string          `json:"mcp_config_source,omitempty"`
	Elicitation     *Elicitation    `json:"elicitation,omitempty"`
	// CanCancel is advertised only by runtimes supporting question-only cancellation.
	CanCancel bool `json:"can_cancel,omitempty"`
	// Dialog is the screen's dialog read on the hub (screen interactions); answers name an option and are checked
	// against Dialog.Rev before any key is pressed. Absent when the screen shows no dialog it could read.
	Dialog *dialogwire.Dialog `json:"dialog,omitempty"`
	// Secret: the screen asks for a password. The app takes it in a password field and keeps no copy; the hub never
	// stores what is typed into a terminal card anyway.
	Secret bool `json:"secret,omitempty"`
	// Fields are filled in on the phone before approving (a permission card that needs a password): the values come
	// back in Resolution.Values and never pass through the model. Quick approve (notification buttons, preview
	// cards, the watch, Siri) is off for such cards: they open the form.
	Fields []Field `json:"fields,omitempty"`
	// Resumable: a hook waits on it that reconnects when its node goes away (hub or node restart), so the hub keeps
	// it pending through the gap instead of cancelling it (docs/protocol/protocol.md "Interactions survive restarts").
	Resumable bool `json:"resumable,omitempty"`
	// Agent is who asks when it is not Claude Code: "codex" or "traex" (a card the node read off a Codex TUI,
	// docs/integrations/codex.md "Questions and approvals").
	Agent string `json:"agent,omitempty"`
	// Reason: why the agent asks for this permission, in its words (Codex's "Reason: …").
	Reason string `json:"reason,omitempty"`
	// AlwaysLabel names what "always allow" does for this request ("don't ask about this command again in this session", in Chinese); a permission card offers it
	// only when set or for Claude.
	AlwaysLabel string `json:"always_label,omitempty"`
	// AgentLabel is what to call Agent when the apps do not know it (a runtime's own name, "DSH"; docs/integrations/runtimes.md),
	// as proto.Session.AgentLabel.
	AgentLabel string `json:"agent_label,omitempty"`
	// AlwaysKeys: what "Always allow in this session" grants on an assistant conversation's permission card, set by the node (a tool,
	// a Codex command or file, a gated write's route). The hub keeps them for the conversation once answered so, and
	// answers a later card whose keys are all kept by itself: the agent's own session rule dies with its process
	// (docs/assistant/assistant.md "Always allow in this session"). Absent on Agents sessions' cards.
	AlwaysKeys []string `json:"always_keys,omitempty"`
	// AlwaysHonored is the manager's word on whether an answer of "Always allow" on this permission card is kept
	// (docs/protocol/protocol.md "Always allow"): true when it is (a node's card with AlwaysKeys the manager remembers,
	// a card of its own that says what it keeps in AlwaysLabel), false when nothing would keep it (the manager's other
	// cards: the extensions' tools, persona_settings…), so the apps offer only Allow and Deny. Absent (an older manager,
	// and Agents sessions' cards, whose agent keeps it itself) the apps keep their own rule.
	AlwaysHonored *bool `json:"always_honored,omitempty"`
	// ToolTitle is the tool's name for people in the user's language ("iPhone control" for iphone_do), set by the
	// manager on its own tools' cards and on a node's card for one of Persona's own MCP tools ("Report to supervisor ·
	// Done", #4966); the apps name the tool with it, else with ToolName as before.
	ToolTitle string `json:"tool_title,omitempty"`
	// ToolNameTitle is the tool alone, without what this call does, where ToolTitle says that too ("Report to
	// supervisor" for "Report to supervisor · Done"): what "Always allow" covers, so a line about every call of the
	// tool names it this way. Set by the manager only beside such a ToolTitle; otherwise ToolTitle names the tool.
	ToolNameTitle string `json:"tool_name_title,omitempty"`
	// Delegating: the assistant is deciding whether it may answer this question for the user (agents.delegate,
	// docs/assistant/assistant.md "Assistant answers questions first"); nothing was pushed yet. The card shows in the apps meanwhile, and an answer
	// given there wins.
	Delegating bool `json:"delegating,omitempty"`
	// Delegate is what the assistant found when it left the question to the user: the context it filled in.
	Delegate *Delegate `json:"delegate,omitempty"`
	// Suggestion: the owner answered this kind of card the same way three times; the card offers a standing rule
	// (rule_suggestion.go). Set by the manager on a question a supervisor passed up; absent otherwise.
	Suggestion *RuleSuggestion `json:"suggestion,omitempty"`
	// Muted says the user muted this agent or its space on the hub. The hub stamps it when answering GET
	// /api/v1/interactions so an answer-only device (the watch) can hide the card and not buzz for it; the phone
	// keeps its own mutes. Absent on older hubs' lists and on the manager's bus copy.
	Muted bool `json:"muted,omitempty"`
	// FoldedUnder is its session's Session.FoldedUnder: the listed session whose chat shows the card when the asking
	// session is a child folded under it (docs/protocol/protocol.md "Session hierarchy"). Set when sent to clients.
	FoldedUnder string `json:"folded_under,omitempty"`
}

// Delegate is the assistant's context on a question it left to the user (Interaction.Delegate): it is a red line or
// about the goal itself, or the assistant could not tell.
type Delegate struct {
	What      string     `json:"what,omitempty"`      // what this is about, in a line
	WhyNow    string     `json:"why_now,omitempty"`   // why the agent asks now
	Recommend string     `json:"recommend,omitempty"` // the answer it recommends (an option's label when there is one)
	Cost      string     `json:"cost,omitempty"`      // what not taking the recommendation costs
	Reason    string     `json:"reason,omitempty"`    // why it is the user's to answer
	Deadline  *time.Time `json:"deadline,omitempty"`  // by when: 24 hours, or the card's expiry when that is sooner
}

// Field is one input of a card (Interaction.Fields).
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret,omitempty"` // a password: a secure field, redacted in the history
	Optional    bool   `json:"optional,omitempty"`
}

// Elicitation carries the hook's original schema or URL without interpreting its fields.
type Elicitation struct {
	Mode            string          `json:"mode"`
	Message         string          `json:"message"`
	RequestedSchema json.RawMessage `json:"requested_schema,omitempty"`
	URL             string          `json:"url,omitempty"`
	ID              string          `json:"elicitation_id,omitempty"`
}

type Resolution struct {
	// Cancel discards only this question, without supplying answers or interrupting the task.
	Cancel   bool              `json:"cancel,omitempty"`
	Action   string            `json:"action,omitempty"`
	Content  map[string]any    `json:"content,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Approve  *bool             `json:"approve,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	Feedback string            `json:"feedback,omitempty"`
	Allow    *bool             `json:"allow,omitempty"`
	Always   bool              `json:"always,omitempty"`
	Message  string            `json:"message,omitempty"`
	Keys     []string          `json:"keys,omitempty"`
	Text     string            `json:"text,omitempty"`
	// AnsweredBy is who answered when it was not the user in the app: AnsweredByAssistant for a question the
	// assistant answered on their behalf (docs/assistant/assistant.md). Absent when the app answered.
	AnsweredBy string `json:"answered_by,omitempty"`
	// Reason is why the assistant answered so (AnsweredByAssistant): the Y of "Answered by the assistant: X, because Y".
	// With Dismiss and no AnsweredBy it is why the card was dismissed (DismissedInChat).
	Reason string `json:"reason,omitempty"`
	// Remembered marks an allow the manager gave from an "Always allow in this session" it had kept (or that a card
	// answered meanwhile covers), not from an answer to this card: the node's sent-as-user ledger records such a write
	// as conversation_always, not as a card (docs/extensions/connectors.md "Sent as the user"). Only the manager sets it; an
	// answer from an app or a channel never carries it. An older manager does not send it.
	Remembered bool `json:"remembered,omitempty"`
	// PreKeys go in before Text (Keys after it): pick "Type something." by its number, type the answer, enter.
	PreKeys []string `json:"pre_keys,omitempty"`
	Dismiss bool     `json:"dismiss,omitempty"`
	// Option answers a screen dialog by its option's key (Interaction.Dialog): the keys are worked out where they are
	// pressed, from the screen as it is then (Text goes with a type_something option).
	Option string `json:"option,omitempty"`
	// Options answer a multi-select screen dialog (Dialog.Multi): the keys of the options to have checked when it is
	// sent, the rest unchecked; Text is its free-text field (empty: left unchecked).
	Options []string `json:"options,omitempty"`
	// Values are the card's Fields as filled in on the phone, by key.
	Values map[string]string `json:"values,omitempty"`
}
