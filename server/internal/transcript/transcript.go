// Package transcript turns Claude Code session JSONL files into normalized proto.Items.
package transcript

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// Limits keep huge tool outputs and images from flooding the phone. Parsing keeps up to maxTextLen of any text, what
// "View full text" shows; pages and pushes carry a light copy (light): a tool's output cut to pageOutputLen, other text to
// pageTextLen, images without their data.
const (
	maxTextLen    = 1 << 20
	pageTextLen   = 24 * 1024
	pageOutputLen = 4 * 1024
	maxInputLen   = 24 * 1024 // tool inputs (a Write's content…) are not fetched whole: clipped for good
	maxImageLen   = 1536 * 1024
)

type entry struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	UUID             string          `json:"uuid"`
	ParentUUID       string          `json:"parentUuid"`
	DurationMS       json.RawMessage `json:"durationMs"`
	Timestamp        time.Time       `json:"timestamp"`
	IsSidechain      bool            `json:"isSidechain"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	CompactMetadata  *struct {
		Trigger   string `json:"trigger"`
		PreTokens int    `json:"preTokens"`
	} `json:"compactMetadata"`
	Content json.RawMessage `json:"content"`
	Message *struct {
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			InputTokens              int `json:"input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	AITitle        string `json:"aiTitle"`
	CustomTitle    string `json:"customTitle"`
	GitBranch      string `json:"gitBranch"`
	Version        string `json:"version"`
	Entrypoint     string `json:"entrypoint"` // the client Claude Code runs in: "cli", "claude-desktop", "sdk-cli"…
	PermissionMode string `json:"permissionMode"`
	Effort         string `json:"effort"` // assistant lines: the effort level the reply was generated with
	Operation      string `json:"operation"`
	Attachment     *struct {
		Type             string          `json:"type"`
		Prompt           json.RawMessage `json:"prompt"`
		CommandMode      string          `json:"commandMode"`
		ReminderType     string          `json:"reminderType"` // ultra_effort_enter: full or sparse
		claudeGoalStatus                 // type goal_status (goal.go)
		Origin           *struct {
			Kind string `json:"kind"`
			Name string `json:"name"` // kind "peer": the Claude session that sent it
			Body string `json:"body"`
		} `json:"origin"`
	} `json:"attachment"`
	// Origin: where a user line came from; a subagent handing back its report carries the report here (Claude Code
	// 2.1.318+), on a hidden line its task notification then only points at (handbackOf). Raw: a shape not known yet
	// must not lose the line.
	Origin json.RawMessage `json:"origin"`
	Cwd    string          `json:"cwd"`
	// toolUseResult is Claude Code's own record of a tool call, its output included (often as long as the tool_result
	// itself): items take the output from the message, and only what outcomes read is decoded (claudeToolResult,
	// outcome.go), never the output. toolDenialKind says why a call did not run (user-rejected, permission-rule…).
	ToolUseResult  claudeToolResult `json:"toolUseResult"`
	ToolDenialKind denialKind       `json:"toolDenialKind"`

	// system/api_error: {message, formatted}, and which retry this is. An assistant line Claude Code wrote itself for a
	// request that failed (isApiErrorMessage) carries the error's kind here as a string (rate_limit,
	// authentication_failed…), and a subscription's limit the account's quota (quotaLimits; claudeErrorKind).
	Error             json.RawMessage `json:"error"`
	RetryAttempt      int             `json:"retryAttempt"`
	MaxRetries        int             `json:"maxRetries"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	APIErrorStatus    int             `json:"apiErrorStatus"`
	QuotaLimits       *struct {
		Status   string          `json:"status"`   // "rejected": the limit refused the request
		ResetsAt json.RawMessage `json:"resetsAt"` // epoch seconds
	} `json:"quotaLimits"`
	// system/scheduled_task_fire: the /loop prompt that is about to run.
	Prompt string `json:"prompt"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
}

// Meta is session-level information collected while parsing.
type Meta struct {
	// Developer info for the chat header (all from fields Claude Code writes on each line).
	GitBranch string
	Version   string
	// Surface is the client the agent runs in: SurfaceDesktop (Claude's or Codex's desktop app), SurfaceCLI (its command
	// line, in a terminal), "" when the transcript does not say (an SDK, an older version). Claude Code writes its
	// entrypoint on every line, a Codex rollout its originator on the first (surface.go).
	Surface string
	// VersionAt is when the line that said Version was written (zero when it had no timestamp).
	VersionAt      time.Time
	PermissionMode string
	// ModeAt / Effort / EffortAt: when the permission mode was last seen, and the effort level of the latest reply.
	ModeAt   time.Time
	Effort   string
	EffortAt time.Time
	// Ultracode is independent of effort: nil means the transcript has no dated evidence. Since, when set by a
	// caller, ignores observations from before the current Claude process (resume resets the session-only switch).
	Ultracode        *bool
	UltracodeAt      time.Time
	UltracodeSince   time.Time
	ultracodeCommand *bool // a command's confirmed value, consumed by its next delayed attachment
	// CodexSandbox: Codex rollouts' sandbox of the latest turn (read-only, workspace-write…); PermissionMode is its approval policy.
	CodexSandbox  string
	ContextTokens int // input + cache read + cache creation of the latest reply: what the model last saw
	// ContextWindow is the model's context window when the transcript says it (a Codex rollout's token_count carries
	// model_context_window); 0 for Claude Code, whose transcripts do not (node/contextwindow.go knows by model id).
	ContextWindow    int
	OutputTokens     int // total generated, counted once per message
	Compactions      int
	Turns            int // user prompts
	StartedAt        time.Time
	lastMessageID    string
	lastOutputTokens int
	// Queue holds input typed while Claude works and not yet taken (queue-operation lines:
	// enqueue / dequeue / remove / popAll — popAll is "up arrow pulls them back into the input").
	Queue []string
	// Background holds background shells / agents that have not reported back (see trackBackground).
	Background []proto.BackgroundTask
	bgPending  map[string]proto.BackgroundTask // tool_use_id → started, waiting for the tool result with its id
	// fgBash: foreground Bash calls waiting for their result, which may say it was moved to the background by hand
	// (Ctrl+B, or a message sent mid-turn: node/assistant_interject.go).
	fgBash map[string]proto.BackgroundTask
	// Workflows are the workflow runs the session launched (the Workflow tool), oldest first, at most workflowsKept:
	// what the transcript says (launched, the notification that it ended); the node adds the progress from disk.
	Workflows []proto.Workflow
	// WorkflowScripts: a workflow run's script file (run id → path), where its name and phases are.
	WorkflowScripts map[string]string
	// Loop is the /loop this session runs, if any (see trackBackground).
	Loop        *proto.LoopInfo
	loopPending map[string]proto.LoopInfo
	// Goal is the goal the agent works toward (Claude Code's /goal, a Codex thread goal), nil when none is set (goal.go).
	Goal *proto.GoalInfo
	// Plan is the agent's latest task list, counted (TodoWrite, TaskCreate / TaskUpdate; plan.go), nil when it keeps none.
	Plan        *proto.PlanInfo
	planItems   []planItem
	planUses    map[string]proto.Item // tool_use_id → a plan tool's call, waiting for its result
	planPending [][]byte              // planUses' ids (PlanPending)
	// LastActivity is the time of the last real exchange (user input, assistant output, tool calls). Claude Code
	// also appends recaps, titles and mode lines to idle sessions, so the file mtime is not "last used".
	LastActivity time.Time
	FirstUser    string // first thing the user typed; titles untitled (headless) sessions
	Title        string
	TitleManual  bool   // A custom-title record exists; later ai-titles never clear the user's choice.
	TitleSource  string // manual or auto; empty when this read has no title provenance.
	// Sidechain keeps isSidechain lines: a subagent's own transcript is all sidechain.
	Sidechain bool
	// Codex: the file is a Codex rollout (codex.go), not a Claude transcript.
	Codex bool
	// TurnOpen: the latest turn has started and not finished: a rollout's task_started without task_complete; a
	// Claude prompt (or a background task waking it) without its turn_duration line or an interrupt after it.
	TurnOpen bool
	// TurnClosed: the lines read so far end with a turn finishing (Codex: task_complete / turn_aborted; Claude:
	// turn_duration / an interrupt), no turn started after it. Unlike !TurnOpen it is false when the lines say nothing
	// (a tail read from mid-turn).
	TurnClosed bool
	// TurnError / TurnErrorKind: the latest turn failed on the model service, as the agent's own record says it
	// (Codex: task_complete's error, #2185; Claude Code: the reply it writes itself for a failed request,
	// isApiErrorMessage, #1710); empty when it ended well or the model answered since. Kind is
	// proto.TurnErrorTransient / User / UsageLimit, or "" when the record gives no kind to go by (an older Claude Code,
	// a gateway's 429): the hub reads the text then.
	TurnError, TurnErrorKind string
	// TurnErrorCode identifies a failed refusal fallback using the runtime's structured error.
	TurnErrorCode string
	// ModelFallback is the runtime's active refusal fallback. ModelFallbackObserved also records a clear, so
	// partial readers can distinguish evidence that it ended from a tail that says nothing about it.
	ModelFallback         *proto.ModelRefusal
	ModelFallbackObserved bool
	// TurnErrorResetsAt: when the subscription usage limit that failed the turn resets, as the record says (Claude
	// Code's quotaLimits.resetsAt; for Codex the full window of the rollout's latest rate limits); zero when it does not.
	TurnErrorResetsAt time.Time
	// TurnErrorResumes: Claude Code said it goes on by itself once the limit resets, and nothing took that back since
	// (claudeLimitNotice). It outlasts the turns background tasks wake the session for while it waits (each fails
	// again at once, with no new notice), not a message: typing cancels Claude Code's own continuation.
	TurnErrorResumes bool
	// resumesSaid: a line read said whether Claude Code goes on by itself (TurnErrorResumesSaid).
	resumesSaid bool
	// TurnErrorAfterWork: the failed turn did some work (a reply, a tool call, thinking) before the error ended it.
	TurnErrorAfterWork bool
	// turnWorked: the turn under way did some work so far (TurnErrorAfterWork when it fails); its end or the next
	// turn's start sets it back. turnErrorLine: the uuid of the line that said TurnError (Tail.Settle).
	turnWorked bool
	// turnParts: the latest turn's words so far, for TurnAnswer (answer.go).
	turnParts     []AnswerPart
	turnErrorLine string
	// codexLimitFull: when the full windows of a rollout's latest rate limits (Codex's own limit, at 100%) reset, the
	// latest of them; zero when none is full. A usage-limit failure resets then (TurnErrorResetsAt).
	codexLimitFull time.Time
	// codexSeen: newer rollouts record an item both as event_msg/item_completed and in history_mutation, within
	// one turn: the ids of the turn under way (task_started drops them, as a window starting there has none).
	codexSeen   map[string]bool
	codexTurnID string
	codexUserID string
	// codexTurnModel: the model the turn under way runs on (its turn_context), which the turn's agent items carry.
	// task_started clears it, as a window starting there has none.
	codexTurnModel string
	// LastTurnWake: the latest turn was started by a background task reporting back, not by a message (no
	// "finished" push for it).
	LastTurnWake bool
	// LastTool: the latest tool call no assistant text or new prompt came after (activity.go), nil when the agent's
	// last word is newer.
	LastTool *ToolCall
	// tree is where the line just parsed sits in the transcript's tree (branch.go); the File reads and clears it.
	tree treeLine
	// localInput: the line being parsed is input the terminal handles itself (a slash command, "! cmd"), no message.
	localInput bool
	// localTurn: the open turn was started by a local command (localInput), with no reply from the agent since; its
	// local output ends it, putting back the turn state from before it (beforeLocal) (#4661).
	localTurn   bool
	beforeLocal [2]bool // TurnOpen, TurnClosed
	// bashInput is the last "! cmd" typed in Claude Code, for the Bash call its output line turns into.
	bashInput string
	// notified holds the background-task notifications already shown, by digest: one is often recorded twice (as a user
	// line and as a queued_command attachment). The same task reporting again with other content is a new event and shows.
	notified map[noticeKey]bool
	// taskCalls: a background task's id → the call that started it, from its notifications that name both, for a
	// later one that names only the task (noteCall). It grows like notified.
	taskCalls  map[string]string
	agentCalls map[string]agentCall
	// handbacks: a subagent's id → the report it handed back (SubagentHandback), until its task notification takes it
	// as its detail. handbackCalls: those calls in the subagent's own transcript, whose results are not shown.
	handbacks     map[string]string
	handbackCalls map[string]bool
	Cwd           string
	Model         string
	LastText      string
	// LastTextAt is when the agent last wrote text, botmux's sentinel included (it ends a turn that answered in the
	// Feishu topic): zero when it never did.
	LastTextAt time.Time
	// LastPromptAt is when the latest message to the agent came in (typed, sent or delivered by a host), zero when
	// none did or it had no time.
	LastPromptAt time.Time
	Updated      time.Time
}

// ObserveNamingMetadata reads only title provenance and generated usage during an existing incremental scan.
// It never decodes message content (which can contain large screenshots or tool output).
func (m *Meta) ObserveNamingMetadata(line []byte) {
	if !bytes.Contains(line, []byte(`"custom-title"`)) && !bytes.Contains(line, []byte(`"ai-title"`)) &&
		!bytes.Contains(line, []byte(`"output_tokens"`)) {
		return
	}
	var e struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		AITitle     string `json:"aiTitle"`
		IsSidechain bool   `json:"isSidechain"`
		Message     *struct {
			ID    string `json:"id"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
		Payload struct {
			Type string `json:"type"`
			Info *struct {
				Total struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "custom-title":
		if e.CustomTitle != "" {
			m.Title, m.TitleManual, m.TitleSource = e.CustomTitle, true, "manual"
		}
	case "ai-title":
		if e.AITitle != "" && !m.TitleManual {
			m.Title, m.TitleSource = e.AITitle, "auto"
		}
	case "assistant":
		if !e.IsSidechain && e.Message != nil && e.Message.Usage != nil {
			m.observeOutput(e.Message.ID, e.Message.Usage.OutputTokens)
		}
	case "event_msg":
		if e.Payload.Type == "token_count" && e.Payload.Info != nil {
			m.OutputTokens = e.Payload.Info.Total.OutputTokens
		}
	}
}

func (m *Meta) observeOutput(id string, tokens int) {
	if id != m.lastMessageID {
		m.lastMessageID, m.lastOutputTokens = id, 0
	}
	// Content blocks repeat usage; later streaming records may increase the same message's count.
	if tokens > m.lastOutputTokens {
		m.OutputTokens += tokens - m.lastOutputTokens
		m.lastOutputTokens = tokens
	}
}

// botmuxSentinel is the line a botmux session's agent ends a turn with when its answer went to the Feishu topic
// through `botmux send`: a word for botmux, not the answer.
const botmuxSentinel = "BOTMUX_NOTHING_TO_SEND"

// Some Anthropic-compatible proxies have a "thinking as text" mode: the thinking block comes back as a text block whose
// lines are each wrapped in ANSI dim italic, opening with "💭 Thinking" and closing with "💭 Thought for Ns (N tokens)",
// so a terminal shows the reasoning inline. That is thinking, not the agent's words.
const (
	thinkingTextOpen = "\x1b[2;3m💭 Thinking\x1b[0m"
	thinkingTextMark = "💭 Thought for "
)

// thinkingAsText is the reasoning in a proxy's thinking-as-text block, without its styling and marker lines; ok is
// false when text is not such a block.
func thinkingAsText(text string) (thinking string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(text, " \t\r\n"), thinkingTextOpen)
	if !ok {
		return "", false
	}
	lines := strings.Split(ansiRe.ReplaceAllString(rest, ""), "\n")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), thinkingTextMark) })
	return strings.TrimSpace(strings.Join(lines, "\n")), true
}

// withoutBotmuxSentinel is text without its botmux sentinel lines, "" when that is all it says (LastText then stays the
// turn's earlier words).
func withoutBotmuxSentinel(text string) string {
	if !strings.Contains(text, botmuxSentinel) {
		return text
	}
	lines := strings.Split(text, "\n")
	lines = slices.DeleteFunc(lines, func(l string) bool { return strings.TrimSpace(l) == botmuxSentinel })
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// pastedRe matches the wrapper Claude Code puts around long pasted input (also what herdr's bracketed paste from
// the phone, e.g. a WeChat voice-input transcript, turns into): <pasted_content id="ae9b">…</pasted_content id="ae9b">.
var pastedRe = lazyre.New(`<[\\/]*pasted_content(?:\s+id="[^"]*")?\s*>`) // also <\pasted…> / <\/pasted…> from nested pastes

// ContextRe matches the live snapshot Persona starts each message to its assistant with (node/assistant_context.go):
// context for the model, not something the user wrote. It is <persona-context>; history and older nodes have
// <persona-context> (#3182).
var ContextRe = lazyre.New(`(?s)<` + compat.ContextPattern() + `>.*?</` + compat.ContextPattern() + `>\s*`)

// ViewErrorsRe matches the <persona-view-errors> feedback the node puts after the snapshot when the assistant's last
// replies drew rich views that failed their check (node/assistant_context.go turnContext): for the model, like the
// snapshot. Left in, it became a user item of its own before the message, its raw tags in the user's bubble.
var ViewErrorsRe = lazyre.New(`(?s)<persona-view-errors\b[^>]*>.*?</persona-view-errors>\s*`)

var tagRe = lazyre.New(`(?s)<(command-name|command-message|command-args|local-command-stdout|local-command-stderr|task-notification|system-reminder)>(.*?)</(command-name|command-message|command-args|local-command-stdout|local-command-stderr|task-notification|system-reminder)>`)

// APIRetrying ends an API error notice Claude Code is still retrying: the turn goes on.
const APIRetrying = "（正在重试）"

// InterruptedNotice is the notice a turn stopped by the user becomes (Claude Code and Codex); APIErrorPrefix starts
// the notice of a turn the model service failed. The channels read them as why a turn ended without words
// (channel.Stopped).
const (
	InterruptedNotice = "已中断"
	APIErrorPrefix    = "API 错误："
	// CompactedNotice marks where the context was compacted (Claude Code's compact_boundary, Codex's
	// ContextCompaction); CompactSummaryNotice stands for the summary a compacted conversation goes on from.
	CompactedNotice      = "上下文已压缩"
	CompactSummaryNotice = "已从压缩的上下文继续"
)

// CompactNotice is the notice a /compact without instructions becomes (no user line): a /compact sent from the phone
// is tied to it (node/echo.go).
const CompactNotice = "压缩上下文"

// ParseLine converts one JSONL line (1-based seq) into items, updating meta.
func ParseLine(seq int, line []byte, meta *Meta) []proto.Item {
	var items []proto.Item
	if meta.Codex || isCodexLine(line) {
		meta.Codex = true
		items = parseCodexLine(seq, line, meta)
	} else {
		items = parseLine(seq, line, meta)
	}
	if len(items) > 0 && items[0].Role == "user" && items[0].Kind == proto.KindText {
		meta.Turns++
	}
	if len(meta.FirstUser) > pageTextLen { // it titles the session in every list
		meta.FirstUser = clipTo(meta.FirstUser, pageTextLen)
	}
	defer func() { meta.localInput = false }()
	for _, it := range items {
		if it.Role != "system" && !it.TS.IsZero() && it.TS.After(meta.LastActivity) {
			meta.LastActivity = it.TS
		}
		meta.trackActivity(it)
		meta.trackPlan(it)
		if it.Wake || it.Role == "user" && !it.Queued && (it.Kind == proto.KindText || it.Kind == proto.KindImage) {
			// A new turn's words start afresh (TurnAnswer): a photo with no caption and a command that runs the model
			// start one too, though they do not reach ClaudeTurnStarted.
			meta.turnParts = nil
		}
		switch {
		case it.Wake:
			meta.LastTurnWake = true
		case it.Role == "user" && it.Kind == proto.KindText && !it.Queued:
			meta.LastTurnWake = false
			// A message to the agent: not a local command, a `! cmd` or another agent's report (localInput).
			if it.From == "" && !meta.localInput && it.TS.After(meta.LastPromptAt) {
				meta.LastPromptAt = it.TS
			}
		}
		if !meta.Codex && (it.Wake || it.Role == "user" && it.Kind == proto.KindText && !it.Queued) {
			local := meta.localInput && !it.Wake
			if local && !meta.localTurn {
				meta.localTurn, meta.beforeLocal = true, [2]bool{meta.TurnOpen, meta.TurnClosed}
			} else if !local {
				meta.localTurn = false
				// A local command (a slash command, "! cmd") is not sent to the model: it does not end a limit.
				meta.ClaudeTurnStarted(it.Wake)
			}
			meta.TurnOpen, meta.TurnClosed = true, false // a Claude turn starts; parseLine closes it
		}
	}
	return items
}

// lineJSON is encoding/json's way of decoding without its check of the whole input before it decodes (its legacy
// error semantics): the decoder checks each byte as it reads it anyway, so input that is not JSON fails all the same,
// read once instead of twice. A transcript line is mostly its message, and a message's content was read up to four
// times (the line, then the content as a string, as blocks, a tool result's again). The error differs from
// json.Unmarshal's (its type and text, and it stops at the first value of the wrong type), so only for callers that
// just ask whether decoding failed; TestLineJSONMatchesUnmarshal checks it fails and decodes as json.Unmarshal does.
var lineJSON = jsonv2.JoinOptions(json.DefaultOptionsV1(), json.ReportErrorsWithLegacySemantics(false))

// unmarshalLine is json.Unmarshal reading data once (lineJSON); err only says whether it failed.
func unmarshalLine(data []byte, v any) error { return jsonv2.Unmarshal(data, v, lineJSON) }

func parseLine(seq int, line []byte, meta *Meta) []proto.Item {
	var e entry
	if err := unmarshalLine(line, &e); err != nil {
		return nil
	}
	meta.tree = treeLine{uuid: e.UUID, parent: e.ParentUUID, sidechain: e.IsSidechain, assistant: e.Type == "assistant",
		system: e.Type == "system", turnEnd: e.Type == "system" && e.Subtype == "turn_duration"}
	if e.Cwd != "" {
		meta.Cwd = e.Cwd
	}
	if !e.Timestamp.IsZero() {
		meta.Updated = e.Timestamp
	}
	if e.GitBranch != "" {
		meta.GitBranch = e.GitBranch
	}
	if e.Version != "" {
		meta.Version, meta.VersionAt = e.Version, e.Timestamp
	}
	if e.Entrypoint != "" {
		meta.Surface = ClaudeSurface(e.Entrypoint)
	}
	if meta.StartedAt.IsZero() && !e.Timestamp.IsZero() {
		meta.StartedAt = e.Timestamp
	}
	// The mode is written on every prompt (user lines) and as its own line when it changes; the effort on replies.
	if e.PermissionMode != "" && !e.IsSidechain && (e.Type == "permission-mode" || e.Type == "user") {
		meta.PermissionMode, meta.ModeAt = e.PermissionMode, meta.Updated
	}
	if e.Type == "assistant" && !e.IsSidechain {
		meta.localTurn = false // the agent answers: a skill or a custom command expanded into a prompt is a turn
	}
	if e.Effort != "" && e.Type == "assistant" && !e.IsSidechain {
		meta.Effort, meta.EffortAt = e.Effort, meta.Updated
	}
	switch {
	case e.Type == "system" && e.Subtype == "compact_boundary":
		meta.Compactions++
	case e.Type == "assistant" && e.Message != nil && e.Message.Usage != nil && !e.IsSidechain:
		u := e.Message.Usage
		// A gateway's sum of a whole turn is not a context, nor the zeros it reports on the turn's other replies
		// (usage.go): the last real one stands.
		if tokens := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens; tokens > 0 && PlausibleContext(tokens, 0) {
			meta.ContextTokens = tokens
		}
		meta.observeOutput(e.Message.ID, u.OutputTokens)
	}
	if e.Type == "queue-operation" {
		content := rawString(e.Content)
		if strings.Contains(content, "<task-notification>") {
			// A background task reporting back is queued like input, but it is not something the user typed.
			meta.taskFinished(content, e.Timestamp)
			return nil
		}
		meta.applyQueue(e.Operation, content)
		return nil
	}
	if e.Type == "custom-title" && e.CustomTitle != "" { // /rename: the user's name beats Claude's generated one
		meta.Title, meta.TitleManual, meta.TitleSource = e.CustomTitle, true, "manual"
		return nil
	}
	if e.Type == "ai-title" && e.AITitle != "" {
		if !meta.TitleManual {
			meta.Title, meta.TitleSource = e.AITitle, "auto"
		}
		return nil
	}
	if e.IsSidechain && !meta.Sidechain {
		return nil
	}
	mk := func(idx int, role, kind string) proto.Item {
		id := e.UUID
		if id == "" {
			id = fmt.Sprintf("line%d", seq)
		}
		return proto.Item{ID: fmt.Sprintf("%s#%d", id, idx), Seq: seq, TS: e.Timestamp, Role: role, Kind: kind}
	}
	switch e.Type {
	case "attachment":
		// Input typed while Claude was working is queued, then absorbed into the running turn; it is stored as a
		// queued_command attachment instead of a user message, so without this it never shows up in the chat.
		a := e.Attachment
		if a != nil && !e.IsSidechain && (a.Type == "ultra_effort_enter" || a.Type == "ultra_effort_exit") {
			return meta.claudeUltracodeAttachment(a.Type, a.ReminderType, e.Timestamp, func() proto.Item { return mk(0, "system", proto.KindNotice) })
		}
		if a != nil && a.Type == "goal_status" && !e.IsSidechain {
			return meta.claudeGoal(a.claudeGoalStatus, meta.Updated, func() proto.Item { return mk(0, "system", proto.KindNotice) })
		}
		if a == nil || a.Type != "queued_command" {
			return nil
		}
		notification := a.CommandMode == "task-notification"
		// A subagent handing its report back while the session works (Claude Code 2.1.319): a queued command with a
		// peer origin that says handback, written by Claude Code itself. Like the hidden user line of one that came
		// while idle, it is no message: its report goes to the subagent's notification, which follows (reportFor).
		if a.Origin != nil && a.Origin.Kind == "peer" {
			if it, ok := meta.agentPeer(mk, attachmentOrigin(line), a.Prompt); ok {
				return []proto.Item{it}
			}
			if agent, report, ok := handbackOf(attachmentOrigin(line)); ok {
				meta.handedBack(agent, report)
				return nil
			}
		}
		if a.Origin != nil && a.Origin.Kind == "peer" { // another Claude session's message, absorbed mid-turn
			var prompt string
			_ = json.Unmarshal(a.Prompt, &prompt)
			it, ok := peerItem(mk, prompt, a.Origin.Name, a.Origin.Body)
			if !ok {
				return nil
			}
			it.Queued = true
			return []proto.Item{it}
		}
		if !notification && ((a.CommandMode != "" && a.CommandMode != "prompt") || (a.Origin != nil && a.Origin.Kind != "human")) {
			return nil
		}
		var text string
		var images []proto.Item // a message with pictures, sent while the agent worked: its prompt is blocks
		if json.Unmarshal(a.Prompt, &text) != nil {
			var blocks []block
			if json.Unmarshal(a.Prompt, &blocks) == nil {
				var parts []string
				for i, b := range blocks {
					switch b.Type {
					case "text":
						parts = append(parts, b.Text)
					case "image":
						if img := imageOf(b); img != nil {
							it := mk(i, "user", proto.KindImage)
							it.Image, it.Queued = img, true
							images = append(images, it)
						}
					}
				}
				text = strings.Join(parts, "\n")
			}
		}
		if strings.Contains(text, "<task-notification>") {
			meta.taskFinished(text, e.Timestamp)
			return userText(mk, text, meta) // a "background task finished" notice
		}
		if notification {
			return nil // other harness notifications are not user input
		}
		items := userText(mk, text, meta)
		for i := range items {
			items[i].Queued = true
		}
		if meta.FirstUser == "" && len(items) > 0 && items[0].Kind == proto.KindText {
			meta.FirstUser = items[0].Text
		}
		return append(items, images...) // the pictures dropped here once: the phone showed the message without them
	case "system":
		if strings.HasPrefix(e.Subtype, "model_refusal_") {
			return meta.claudeModelRefusal(e.Subtype, line, mk(0, "system", proto.KindNotice))
		}
		var text string
		switch e.Subtype {
		case "turn_duration":
			meta.TurnOpen, meta.TurnClosed = false, true
			meta.turnWorked = false // the turn's work is done: Claude Code's own continuation after a limit opens with no prompt
			ms, ok := recordedMilliseconds(e.DurationMS)
			if !ok || strings.TrimSpace(e.ParentUUID) == "" {
				return nil
			}
			it := mk(0, "system", "turn_duration")
			it.TurnDuration = &proto.TurnDuration{DurationMS: ms, Source: "claude_turn_duration", ParentUUID: e.ParentUUID}
			return []proto.Item{it}
		case "compact_boundary":
			text = CompactedNotice
		case "api_error":
			// Claude Code writes one line per retry (retryAttempt 1..maxRetries): show the first and, if it never
			// got through, the last.
			if e.RetryAttempt > 1 && (e.MaxRetries == 0 || e.RetryAttempt < e.MaxRetries) {
				return nil
			}
			text = APIErrorPrefix + apiErrorText(e)
			switch {
			case e.MaxRetries > 0 && e.RetryAttempt >= e.MaxRetries:
				text += fmt.Sprintf("（重试 %d 次后放弃）", e.RetryAttempt)
			case e.RetryAttempt == 1:
				text += APIRetrying
			}
		case "local_command":
			// Newer Claude Code writes slash commands and their output as system lines (older ones as user lines):
			// "<command-name>/model</command-name>…" is what the user typed, "<local-command-stdout>…" the answer.
			return userText(mk, rawString(e.Content), meta)
		case "scheduled_task_fire":
			// A /loop or cron job firing: the turn that follows was started by the schedule, not by a message.
			text = "/loop 到点，开始新一轮"
			if p := snippetLine(e.Prompt, 80); p != "" {
				text += "：" + p
			}
			it := mk(0, "system", proto.KindNotice)
			it.Text, it.Wake = text, true
			return []proto.Item{it}
		case "away_summary":
			recap := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rawString(e.Content)), "(disable recaps in /config)"))
			if recap == "" {
				return nil
			}
			it := mk(0, "system", proto.KindRecap)
			it.Text = clip(recap)
			return []proto.Item{it}
		case "informational":
			text = rawString(e.Content)
			meta.claudeLimitNotice(text)
		default:
			return nil
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		it := mk(0, "system", proto.KindNotice)
		it.Text = clip(text)
		if e.Subtype == "compact_boundary" {
			it.Compact = &proto.Compact{}
			if m := e.CompactMetadata; m != nil {
				it.Compact.Trigger, it.Compact.PreTokens = m.Trigger, m.PreTokens
			}
		}
		return []proto.Item{it}
	case "user":
		if e.Message == nil {
			return nil
		}
		if it, ok := meta.agentPeer(mk, e.Origin, e.Message.Content); ok {
			it.Wake = true
			return []proto.Item{it}
		}
		if agent, report, ok := handbackOf(e.Origin); ok {
			meta.handedBack(agent, report)
		}
		if e.IsCompactSummary {
			it := mk(0, "system", proto.KindNotice)
			it.Text = CompactSummaryNotice
			return []proto.Item{it}
		}
		if e.IsMeta {
			if it, ok := peerMessage(mk, e.Origin, e.Message.Content); ok {
				return []proto.Item{it}
			}
		}
		var s string
		if raw := bytes.TrimSpace(e.Message.Content); len(raw) > 0 && raw[0] == '"' && unmarshalLine(raw, &s) == nil {
			if strings.Contains(s, "<task-notification>") {
				meta.taskFinished(s, e.Timestamp)
			}
			if e.IsMeta {
				return nil
			}
			items := userText(mk, s, meta)
			if strings.Contains(s, "<task-notification>") {
				markWake(items)
			}
			if meta.FirstUser == "" && len(items) > 0 && items[0].Kind == proto.KindText {
				meta.FirstUser = items[0].Text
			}
			return items
		}
		var blocks []block
		if unmarshalLine(e.Message.Content, &blocks) != nil {
			return nil
		}
		var out []proto.Item
		// The line's record of a call (toolUseResult, toolDenialKind) is its result's when it holds only one.
		record, denial := &noToolResult, ""
		if oneToolResult(blocks) {
			record, denial = &e.ToolUseResult, string(e.ToolDenialKind)
		}
		for i, b := range blocks {
			switch b.Type {
			case "text":
				if strings.Contains(b.Text, "<task-notification>") {
					meta.taskFinished(b.Text, e.Timestamp)
				}
				if e.IsMeta {
					continue
				}
				items := userText(func(_ int, role, kind string) proto.Item { return mk(i, role, kind) }, b.Text, meta)
				if strings.Contains(b.Text, "<task-notification>") {
					markWake(items)
				}
				if meta.FirstUser == "" && len(items) > 0 && items[0].Kind == proto.KindText {
					meta.FirstUser = items[0].Text
				}
				out = append(out, items...)
			case "image":
				if e.IsMeta {
					continue
				}
				if img := imageOf(b); img != nil {
					it := mk(i, "user", proto.KindImage)
					it.Image = img
					out = append(out, it)
				}
			case "tool_result":
				if meta.handbackCalls[b.ToolUseID] { // "Report delivered to your caller.": its report is the words above
					delete(meta.handbackCalls, b.ToolUseID)
					continue
				}
				text, imgs := toolResultContent(b.Content)
				meta.backgroundStarted(b.ToolUseID, text, b.IsError, e.Timestamp)
				it := mk(i, "user", proto.KindToolResult)
				it.ToolUseID = b.ToolUseID
				it.IsError = b.IsError
				it.Text = clip(text)
				it.Outcome = claudeOutcome(text, b.IsError, record, denial)
				it.Task = meta.agentResult(it.ToolUseID, it.Text, it.IsError, line)
				out = append(out, it)
				for j, img := range imgs {
					im := mk(i*100+j+1, "user", proto.KindImage)
					im.ToolUseID = b.ToolUseID
					im.Image = img
					out = append(out, im)
				}
			}
		}
		return out
	case "assistant":
		if e.Message == nil {
			return nil
		}
		model := e.Message.Model
		if model == "<synthetic>" { // Claude Code's own lines (an API error, an interruption): no model wrote them
			model = ""
		}
		if model != "" {
			meta.Model = model
			meta.observeReplyModel(model)
		}
		var blocks []block
		if unmarshalLine(e.Message.Content, &blocks) != nil {
			return nil
		}
		var out []proto.Item
		for i, b := range blocks {
			switch b.Type {
			case "text":
				if strings.TrimSpace(b.Text) == "" {
					continue
				}
				if t, ok := thinkingAsText(b.Text); ok {
					if t != "" {
						it := mk(i, "assistant", proto.KindThinking)
						it.Text = clip(t)
						out = append(out, it)
					}
					continue
				}
				it := mk(i, "assistant", proto.KindText)
				it.Text = clip(b.Text)
				meta.said(withoutBotmuxSentinel(b.Text), e.Timestamp)
				out = append(out, it)
			case "thinking":
				if strings.TrimSpace(b.Thinking) == "" {
					continue
				}
				it := mk(i, "assistant", proto.KindThinking)
				it.Text = clip(b.Thinking)
				out = append(out, it)
			case "tool_use", "server_tool_use":
				if b.Name == handbackTool {
					// a subagent handing its report back to the session that launched it: its last words, not a step
					if report := meta.handbackCall(b.ID, b.Input); report != "" {
						it := mk(i, "assistant", proto.KindText)
						it.Text = clip(report)
						meta.said(report, e.Timestamp)
						out = append(out, it)
					}
					continue
				}
				meta.turnTool()
				it := mk(i, "assistant", proto.KindToolUse)
				it.ToolUseID = b.ID
				it.ToolName = b.Name
				it.ToolInput = clipJSON(b.Input)
				out = append(out, it)
				meta.trackBackground(b.ID, b.Name, b.Input, e.Timestamp)
			}
		}
		meta.claudeReply(&e, blocks, len(out) > 0)
		for i := range out {
			out[i].Model = model
		}
		return out
	}
	return nil
}

// said notes the agent writing at ts: LastText (when t has words) and what its branch said.
func (m *Meta) said(t string, ts time.Time) {
	if t != "" {
		m.LastText = t
		m.tree.said = t
		m.turnSaid(t)
	}
	m.LastTextAt = ts
	m.tree.saidAt, m.tree.spoke = ts, true
}

// ansiRe matches terminal escape sequences (colors, bold) that local command output keeps.
var ansiRe = lazyre.New(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// apiErrorText is what an api_error line says went wrong: error.formatted / error.message, or older lines' content.
func apiErrorText(e entry) string {
	var s string
	if json.Unmarshal(e.Error, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		Message   string `json:"message"`
		Formatted string `json:"formatted"`
	}
	if json.Unmarshal(e.Error, &obj) == nil {
		if obj.Formatted != "" {
			return obj.Formatted
		}
		if obj.Message != "" {
			return obj.Message
		}
	}
	if s := strings.TrimSpace(rawString(e.Content)); s != "" {
		return s
	}
	return "请求失败"
}

func firstTag(s, tag string) string {
	open, close := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}

func toolResultContent(raw json.RawMessage) (string, []*proto.Image) {
	var s string
	if unmarshalLine(raw, &s) == nil {
		return s, nil
	}
	var blocks []block
	if unmarshalLine(raw, &blocks) != nil {
		return "", nil
	}
	var parts []string
	var imgs []*proto.Image
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, b.Text)
		case "image":
			if img := imageOf(b); img != nil {
				imgs = append(imgs, img)
			}
		case "tool_reference":
			parts = append(parts, "已加载工具 "+b.Name)
		}
	}
	return strings.Join(parts, "\n"), imgs
}

func imageOf(b block) *proto.Image {
	if b.Source == nil || b.Source.Type != "base64" || len(b.Source.Data) > maxImageLen {
		return nil
	}
	return &proto.Image{Mime: b.Source.MediaType, Data: b.Source.Data}
}

func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
