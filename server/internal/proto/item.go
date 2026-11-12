package proto

import (
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/gif" // image sizes for lazy placeholders
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"
)

// Item kinds.
const (
	KindText       = "text"
	KindThinking   = "thinking"
	KindToolUse    = "tool_use"
	KindToolResult = "tool_result"
	KindNotice     = "notice"
	KindImage      = "image"
	KindRecap      = "recap" // Claude Code's "while you were away" summary
	// KindFold stands for a run of the agent's work left out of a page (Item.Fold); only for callers that ask
	// (fold_work).
	KindFold = "fold"
)

// Fold is what a KindFold item stands for: a run of tool calls with their results and thoughts, from the item it is
// named after ("fold:" + its id, at its seq and time) through ThroughID. A chat shows the run folded into counts;
// opening it fetches the run (session.items with from_seq / through_seq), which replaces the fold.
type Fold struct {
	Items      int                `json:"items"`
	Tools      int                `json:"tools"`
	Thoughts   int                `json:"thoughts"` // thoughts with words (blank ones are not shown)
	Until      time.Time          `json:"until"`
	ThroughSeq int                `json:"through_seq"`
	ThroughID  string             `json:"through_id"`
	Tasks      []FoldTaskSnapshot `json:"tasks,omitempty"`
	// Images: the images the run's calls looked at, in order (FoldImage): a chat shows its turn's under the answer
	// without opening the run.
	Images []FoldImage `json:"images,omitempty"`
	// LatestThinking is a bounded preview of the run's newest thought with words. The original item stays in the fold.
	LatestThinking *Item `json:"latest_thinking,omitempty"`
	// SupervisionUser preserves an explicit user-visible action hidden inside this work range.
	SupervisionUser bool `json:"supervision_user,omitempty"`
}

// FoldImage is an image a tool call of a folded run looked at (docs/protocol/protocol.md "Images viewed this turn"): one its result
// carried (ItemID, fetched from GET …/items/{item id}/image), or a picture file Read opened whose result carried none
// (only Path; the file's thumbnail is GET /machines/{mid}/file/thumb).
type FoldImage struct {
	ItemID    string `json:"item_id,omitempty"`
	ToolUseID string `json:"tool_use_id"`
	Tool      string `json:"tool"`              // the call's tool as written (Read, mcp__persona__computer_state)
	Source    string `json:"source"`            // read, screenshot (computer_*), browser (browser_*), mcp (another MCP tool), tool
	Path      string `json:"path,omitempty"`    // the picture file Read opened (a Codex view_image too)
	Machine   string `json:"machine,omitempty"` // screenshot / browser: the machine the call named (an id or a name)
	Mime      string `json:"mime,omitempty"`
	Size      int    `json:"size,omitempty"` // bytes, about (the base64's length × 3 / 4, like Image.Placeholder)
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// FoldTaskSnapshot preserves a successful task result without its raw tool payload.
// Replacement boundaries stay ordered so partial updates can inherit older loaded tasks.
type FoldTaskSnapshot struct {
	Replace         string     `json:"replace,omitempty"`
	FallbackStatus  string     `json:"fallback_status,omitempty"`
	Entries         []FoldTask `json:"entries"`
	SourceItemID    string     `json:"source_item_id"`
	SourceToolUseID string     `json:"source_tool_use_id"`
	SourceSeq       int        `json:"source_seq"`
}

type FoldTask struct {
	ID          string  `json:"id"`
	Subject     *string `json:"subject,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
}

type Image struct {
	Mime string `json:"mime"`
	Data string `json:"data"`
	// Lazy: the hub left Data out (clients that asked for lazy_images); fetch it from
	// GET /api/v1/sessions/{mid}/{sid}/items/{item id}/image when the image is shown. Size is the byte count.
	// Width / Height (pixels) let the app reserve the image's space before the bytes arrive.
	Lazy   bool `json:"lazy,omitempty"`
	Size   int  `json:"size,omitempty"`
	Width  int  `json:"width,omitempty"`
	Height int  `json:"height,omitempty"`
}

// Placeholder is the image without its data: what a page carries, fetched on demand when shown.
func (img *Image) Placeholder() *Image {
	if img.Data == "" {
		return img
	}
	lazy := &Image{Mime: img.Mime, Lazy: true, Size: len(img.Data) * 3 / 4}
	// Only the header is read: the decoder stops once it knows the size.
	if c, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(img.Data))); err == nil {
		lazy.Width, lazy.Height = c.Width, c.Height
	}
	return lazy
}

// WithoutImageData returns items whose images carry no data, only a placeholder to fetch on demand: chat pages
// with screenshots inline ran to megabytes.
func WithoutImageData(items []Item) []Item {
	var out []Item
	for i, it := range items {
		if it.Image == nil || it.Image.Data == "" {
			continue
		}
		if out == nil {
			out = append([]Item(nil), items...)
		}
		out[i].Image = it.Image.Placeholder()
	}
	if out == nil {
		return items
	}
	return out
}

// Item is one content block of a Claude Code transcript.
type Item struct {
	ID        string          `json:"id"`
	Seq       int             `json:"seq"`
	TS        time.Time       `json:"ts"`
	Role      string          `json:"role"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Image     *Image          `json:"image,omitempty"`
	Queued    bool            `json:"queued,omitempty"` // user input typed while the agent worked, absorbed mid-turn
	// Outcome: what a tool_result's call came to (status, exit code, patch, git…), set by the node from the
	// runtime's record; absent on other items and from older nodes (ToolOutcome).
	Outcome *ToolOutcome `json:"outcome,omitempty"`
	// ClientMsgID: this item came from the message an app sent with this client_msg_id (session.send): its words,
	// its pictures, or the notice it became (/compact). Set by the node, which ties the transcript's line to the send
	// (node/echo.go); absent for anything else and from older nodes. docs/protocol/protocol.md "Matching pending messages".
	ClientMsgID string `json:"client_msg_id,omitempty"`
	// FullSize: Text was cut for pages and pushes (it ends in a "truncated, open for the full text" line, in Chinese); the whole of it, this many
	// bytes, comes from GET /api/v1/sessions/{mid}/{sid}/items/{item id}/full.
	FullSize int `json:"full_size,omitempty"`
	// Wake marks a background task's notice that started new work after the agent had finished its turn
	// (not one absorbed mid-turn): the exchange before it is over and its last words are an answer.
	Wake bool `json:"wake,omitempty"`
	// From names the Claude session that sent this message (cross-session messaging), not the user.
	From string `json:"from,omitempty"`
	// Injected: this user line is a message Persona put into the conversation for the model (a task's notice, a
	// scheduled run, a wake-up…), not something the user wrote. Set by the hub from the line's text
	// (prompts.ParseInjected) on its way to the apps; docs/protocol/protocol.md "Messages injected by Persona".
	Injected *Injected `json:"injected,omitempty"`
	// Source is the author of an automated input, independent of its model role.
	// Unknown nonempty kinds are still not user-authored input.
	Source *MessageSource `json:"source,omitempty"`
	Fold   *Fold          `json:"fold,omitempty"`
	// Task: a background task notice, or a subagent tool result associated with its launching call.
	Task         *TaskNote     `json:"task,omitempty"`
	TurnDuration *TurnDuration `json:"turn_duration,omitempty"`
	// Compact: this notice is where the context was compacted (Claude Code's compact_boundary).
	Compact *Compact `json:"compact,omitempty"`
	// ModelRefusal preserves Claude Code's refusal record on a notice.
	ModelRefusal *ModelRefusal `json:"model_refusal,omitempty"`
	// Model is the model that wrote this assistant item as the transcript records it: Claude Code's message.model
	// (never its "<synthetic>" placeholder), a Codex turn's turn_context model. Absent when the transcript does not
	// say, on other roles, and from older nodes. Clients draw a divider where the model of the replies changes.
	Model string `json:"model,omitempty"`
}

// ModelRefusal is a Claude Code model_refusal_* record as clients see it: its known fields, values kept as recorded
// (unknown subtypes, categories and scopes included), and Text, the refusal's own words (the record's string
// content). Nothing else of the record leaves the node (#2528): the line may carry fields no one reviewed.
type ModelRefusal struct {
	Subtype       string `json:"subtype"`
	OriginalModel string `json:"original_model,omitempty"`
	FallbackModel string `json:"fallback_model,omitempty"`
	Category      string `json:"category,omitempty"`
	Scope         string `json:"scope,omitempty"`
	Explanation   string `json:"explanation,omitempty"`
	Text          string `json:"text,omitempty"`
	// Raw is the whole JSONL line, for the node's own use only (telling one record from another): never encoded, so
	// no RPC, REST answer, push or snapshot carries it. Older builds sent it as "raw"; clients must not show that.
	Raw string `json:"-"`
}

// ModelRestore is a pending "back to the configured model" (node session.restore_model, #2528): State
// ModelRestoreNextStart says the session's idle headless process was closed (or none ran), so its next start asks for
// To, the model the conversation is configured with as the node read it at At. Nothing about which model runs.
type ModelRestore struct {
	State string    `json:"state"`
	To    string    `json:"to,omitempty"`
	At    time.Time `json:"at"`
}

// ModelRestoreNextStart: the next start of the session's process uses the configured model.
const ModelRestoreNextStart = "next_start"

// MessageSource preserves an input's provenance without changing the model's role.
// ConversationID is a stable session id, never a peer's display name (Item.From).
type MessageSource struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
	Title          string `json:"title,omitempty"`
	// Push is a scheduled input's delivery mode, persisted with the input so a queued user message cannot change it.
	// "asked" suppresses that run's normal completion; absent keeps the existing every-answer behavior.
	Push string `json:"push,omitempty"`
}

const (
	SourceConversation = "conversation"
	SourcePatrol       = "patrol"
	SourceFollowup     = "followup"
	SourceSupervision  = "supervision"
	// SourceMeeting is a batch of a recorded meeting the session attends (docs/assistant/meeting-minutes.md "Feeding
	// the attending session"): other people's words, never the user's; Title is the meeting's name.
	SourceMeeting = "meeting"
)

// SourceFromInjected upgrades existing metadata without guessing from ordinary text.
func SourceFromInjected(in *Injected) *MessageSource {
	if in == nil {
		return nil
	}
	kind := ""
	switch {
	case in.Kind == InjectedFollowup && (in.State == "daily" || in.State == "interval"):
		kind = SourcePatrol
	case in.Kind == InjectedFollowup:
		kind = SourceFollowup
	case in.Kind == InjectedSystem && in.State == "supervise":
		kind = SourceSupervision
	}
	if kind == "" {
		return nil
	}
	return &MessageSource{Kind: kind, Title: in.Title}
}

// Injected says what a message Persona wrote for the model is, for the apps to show one line instead of it.
type Injected struct {
	// Level classifies supervision: info updates only, triage folds the turn, user is visible.
	Level   string `json:"level,omitempty"`
	Kind    string `json:"kind"`              // InjectedTaskNotice | InjectedFollowup | InjectedSystem
	State   string `json:"state,omitempty"`   // task_notice: done|needs|interrupted|failed; followup: once|daily|interval|wake; system: lost_tasks|supervise|inbox
	Title   string `json:"title,omitempty"`   // the task's, the scheduled task's, the inbox item's, or what a woken agent waits for
	Machine string `json:"machine,omitempty"` // task_notice: where its agent runs (a name)
	TodoID  string `json:"todo_id,omitempty"` // task_notice: the task, when the hub can tell
	Line    string `json:"line"`              // the one line to show
	Detail  string `json:"detail,omitempty"`  // followup once|daily|interval: what the task asks, as the user wrote it
}

// Injected kinds.
const (
	InjectedTaskNotice = "task_notice"
	InjectedFollowup   = "followup"
	InjectedSystem     = "system"
)

// Compact says how a compaction came about: Trigger "manual" (/compact) or "auto", and the context's size before it.
type Compact struct {
	Trigger   string `json:"trigger,omitempty"`
	PreTokens int    `json:"pre_tokens,omitempty"`
}

// Compaction states.
const (
	CompactionRunning = "running"
	CompactionDone    = "done"
	CompactionFailed  = "failed"
)

// Compaction is a compaction the user asked for: State running, done or failed (Error says why).
type Compaction struct {
	State      string     `json:"state"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// TurnDuration preserves a recorded whole-turn duration and its native association.
type TurnDuration struct {
	DurationMS int64  `json:"duration_ms"`
	Source     string `json:"source"`
	TurnID     string `json:"turn_id,omitempty"`
	ParentUUID string `json:"parent_uuid,omitempty"`
	UserItemID string `json:"user_item_id,omitempty"`
}

// TaskNote is what a <task-notification> says, read from the notification alone (the call it came from is found by
// the app, through TaskID / ToolUseID): a command run in the background finishing, a Monitor's event or its end, a
// subagent finishing. Title, Detail and Text come decoded (Claude Code writes &gt; &lt; &amp; in them).
type TaskNote struct {
	Kind   string `json:"kind"`             // shell | monitor | agent | workflow | task (anything else)
	Status string `json:"status,omitempty"` // running | completed | failed | killed | stopped | event | ended | waiting
	// Title: the call's description, or the first line of its command when it had none.
	Title      string `json:"title"`
	TaskID     string `json:"task_id,omitempty"`
	ToolUseID  string `json:"tool_use_id,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	OutputFile string `json:"output_file,omitempty"`
	// Detail: a Monitor's event, a subagent's result, or a command's whole text when the title is only its first line.
	Detail string `json:"detail,omitempty"`
}
