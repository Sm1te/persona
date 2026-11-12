package proto

import (
	"math"
	"time"
)

// Claude Code's effort levels and permission modes (node/modes.go sets them; the hub checks a scheduled task's).
var (
	EffortLevels    = []string{"low", "medium", "high", "xhigh", "max"}
	PermissionModes = []string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"}
)

// ModeBypass skips every permission prompt; a headless claude only takes it at start (--permission-mode).
const ModeBypass = "bypassPermissions"

// ClaudeDefaultModeSetting is Claude Code's permissions.defaultMode, a key of Settings › Claude Code (node
// claudeconfig.go): the mode a session started without --permission-mode runs in. The hub reads it for a planned
// task with no mode of its own (assistant/tasks_permission.go).
const ClaudeDefaultModeSetting = "permissions.defaultMode"

// Session statuses mirror herdr agent states, plus offline for transcripts with no live agent.
const (
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
	StatusUnknown = "unknown"
	StatusOffline = "offline"
	// StatusInterrupted: it was running in a pane when the node restarted, which took herdr's panes with it. The
	// conversation is still resumable (session.start with resume) but nothing carries it on until the user says so
	// (#1917, interrupted.go in the node).
	StatusInterrupted = "interrupted"
)

// Unified agent states (docs/protocol/agent-states.md).
const (
	StateNeedsYou = "needs_you" // waiting for the user: a question, plan, permission or any TUI dialog
	StateWorking  = "working"
	StateUnread   = "unread" // finished, result not looked at yet
	StateIdle     = "idle"   // finished and seen, ready for input
	StateOffline  = "offline"
	StateUnknown  = "unknown"
)

// CodedStartErrorMax is how many runes of a coded refusal's start_error a list row or a task keeps where a summary would
// be clipped shorter: the hub's sentence for the code (StartErrorCode) is bounded, and its remedy comes last (#1642).
const CodedStartErrorMax = 600

// ToolLoop is a node-detected repetition. It contains no tool input or output.
// Since identifies the start of this repetition within the current turn.
type ToolLoop struct {
	Tool  string    `json:"tool"`
	Count int       `json:"count"`
	Since time.Time `json:"since"`
}

// Session is one row of a computer's list. Rows are copied one at a time all over the hub, the manager and the node,
// so the size matters: its bools sit next to other bools, several to an 8-byte word, which keeps a row inside Go's
// 1,536-byte size class (TestSessionFitsItsSizeClass). A bool between two wider fields takes a word to itself.
type Session struct {
	ToolLoop *ToolLoop `json:"tool_loop,omitempty"`
	// Manager is the manager it belongs to (docs/assistant/managers.md, "Ownership"); the hub stamps it on what it sends the apps.
	Manager          string               `json:"manager,omitempty"`
	MachineID        string               `json:"machine_id"`
	SessionID        string               `json:"session_id"`
	SourceKind       string               `json:"source_kind,omitempty"`
	SourceThreadID   string               `json:"source_thread_id,omitempty"`
	SourceInstanceID string               `json:"source_instance_id,omitempty"`
	ParentThreadID   string               `json:"parent_thread_id,omitempty"`
	ParentSessionID  string               `json:"parent_session_id,omitempty"` // The session that started it: a Codex parent's public ID (scoped to the Codex home on this machine), or a runtime's own session (#4494, #4495).
	RootSessionID    string               `json:"root_session_id,omitempty"`
	ReadOnlyReason   string               `json:"read_only_reason,omitempty"`
	Capabilities     *SessionCapabilities `json:"capabilities,omitempty"`
	IsSubagent       bool                 `json:"is_subagent,omitempty"`
	Attached         bool                 `json:"attached,omitempty"`
	// CanFork: "Branch to new chat" works for it (a Claude Code conversation the node can copy, node/fork.go); older nodes
	// never say so, and the clients hide it.
	CanFork          bool `json:"can_fork,omitempty"`
	StartedByPersona bool `json:"started_by_persona"`
	Assistant        bool `json:"assistant"` // runs in the assistant directory (the app's "Chat" tab)
	// Archived: a retained conversation moved out of the normal list. Assistant
	// conversations and Claude/Codex Agents are managed by their owning manager.
	Archived    bool `json:"archived,omitempty"`
	TitleManual bool `json:"title_manual,omitempty"` // A runtime record proves the user chose a title.
	// BypassAvailable sits here, beside the bools before it, not with PermissionMode: alone between two wider fields it
	// took an 8-byte word, and a Session must stay at most 1,528 bytes (see BackgroundWorking below); PendingSwitch got
	// that word (#5035).
	// BypassAvailable: this Claude Code can be switched to bypassPermissions (Allow All) while it runs, since it was
	// started with --dangerously-skip-permissions, --allow-dangerously-skip-permissions or --permission-mode
	// bypassPermissions, which put that mode in its shift+tab cycle (#1379). The node reads it off the claude process its
	// hooks run under; false while unknown (no hook yet, a node too old to say).
	BypassAvailable bool   `json:"bypass_available,omitempty"`
	Title           string `json:"title"`
	Cwd             string `json:"cwd"`
	// WorkspaceCwd is the node-verified physical directory for Cwd. Consumers
	// matching workspace bindings prefer it and fall back to Cwd on older nodes.
	WorkspaceCwd string `json:"workspace_cwd,omitempty"`
	// RepoRoot is the main checkout of the repository Cwd belongs to, when the host knows it (#2840: the Paseo bridge,
	// whose worktrees live under Paseo's own folder): "By repo" groups by it; without it the clients infer the repository
	// from Cwd's path.
	RepoRoot string `json:"repo_root,omitempty"`
	// Worktree is the top of the git working tree Cwd is in (RepoRoot itself for the main checkout, a linked worktree's
	// own folder otherwise) and Branch the branch checked out there ("" when detached or unknown). The node reads both,
	// and RepoRoot when no host names it, off the filesystem (node/gitplace.go): the apps list machine → project
	// (RepoRoot) → worktree (Worktree, labelled Branch) → sessions, as Orca does (#1805). RepoRoot and Worktree are
	// physical directories (symbolic links resolved, #4492), however Cwd is written and however a host wrote the
	// RepoRoot it named: the apps compare them as strings.
	Worktree    string `json:"worktree,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Status      string `json:"status"`
	PaneID      string `json:"pane_id,omitempty"`
	TabID       string `json:"tab_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	AgentName   string `json:"agent_name,omitempty"`
	// CodexRoute: how Persona reaches a Codex in a terminal (docs/integrations/codex.md "Sessions on the daemon") — "app_server" (joined on
	// the shared app-server: questions, approvals and messages go through its protocol) or "terminal" (its rollout and
	// screen, keys and typing); CodexRouteNote says why when it is the terminal.
	CodexRoute     string `json:"codex_route,omitempty"`
	CodexRouteNote string `json:"codex_route_note,omitempty"`
	// Agent is the coding agent in the pane: "claude" (also when empty, from older nodes) or "codex"; for a session a
	// runtime lists (docs/integrations/runtimes.md), the agent it says it runs ("dsh").
	Agent string `json:"agent,omitempty"`
	// AgentLabel is what the apps call the agent when they do not know it themselves: a runtime's own name ("DSH").
	// Empty for the agents the apps know (Claude Code, Codex, a compatible CLI).
	AgentLabel string `json:"agent_label,omitempty"`
	// AgentSurface is the client the agent runs in: "desktop" (Claude's or Codex's desktop app) or "cli" (its command
	// line); empty when its record does not say (an SDK, an older version, a runtime). The apps badge the agent's mark
	// with </> for "cli" (#6390).
	AgentSurface string `json:"agent_surface,omitempty"`
	Launcher     string `json:"launcher,omitempty"`
	Model        string `json:"model,omitempty"`
	// PermissionMode and Effort are what Claude Code runs with now: default / acceptEdits / plan / auto /
	// bypassPermissions, and low … max. Empty while unknown (before the first turn, Codex). A Codex thread the phone
	// drives through its app-server has an Effort too (Codex's levels, see Efforts) but never a PermissionMode.
	PermissionMode string `json:"permission_mode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	// Ultracode is Claude Code's independent session-only workflow switch: nil means unknown.
	// UltracodePending (beside AsksRead) means a requested change waits for an idle input box; LostAt asks the client
	// to offer reopening after a resumed process reset an enabled switch. Reopening or dismissing the notice clears
	// LostAt.
	Ultracode       *bool      `json:"ultracode,omitempty"`
	UltracodeNote   string     `json:"ultracode_note,omitempty"`
	UltracodeLostAt *time.Time `json:"ultracode_lost_at,omitempty"`
	// PendingSwitch is a switch of this running Claude Code Agent to another launcher (model provider) of the same
	// engine that the user picked and that waits for the next message (#5035, docs/integrations/claude-integration.md
	// "Switching a running Agent to another provider"): that message quits the agent and resumes the same conversation
	// on the new launcher in the same terminal first. Nil when none waits.
	PendingSwitch *PendingSwitch `json:"pending_switch,omitempty"`
	// ContextTokens is what the model saw in the agent's latest reply (Claude: input + cache read + cache creation;
	// Codex: the last token_count's input) and ContextWindow the model's window, both read off the transcript tail the
	// node already reads (#2430). ContextTokens is omitted while unknown (no reply yet); ContextWindow too, when the
	// node cannot tell the model's window. The apps show used / window as the context bar.
	// Both are int32, next to each other, to leave Session the word Hierarchy takes (TestSessionFitsItsSizeClass): the
	// largest windows are a few million tokens (Tokens32 saturates anything bigger).
	ContextTokens int32 `json:"context_tokens,omitempty"`
	ContextWindow int32 `json:"context_window,omitempty"`
	// OutputTokens is cumulative generated output, not the latest context size or transcript revision.
	OutputTokens int `json:"output_tokens,omitempty"`
	// Efforts are the levels session.modes takes for Effort when they are not Claude Code's (which the apps know): a
	// Codex thread the phone drives through its app-server (node/codex_effort.go), its model's levels weakest first.
	// Speed has its own advertised choices in ServiceTiers. Empty for every other session: Claude's and the assistant's keep the apps' own
	// list, the rest cannot change it from the phone.
	Efforts []string `json:"efforts,omitempty"`
	// ServiceTier is the acknowledged native speed, omitted while unknown.
	ServiceTier  string        `json:"service_tier,omitempty"`
	ServiceTiers []ServiceTier `json:"service_tiers,omitempty"`
	// Space is the assistant's space it runs in: "health" for the health space's (docs/assistant/health.md), "" for the
	// assistant's own. Set by the hub on the assistant's conversations.
	Space string `json:"space,omitempty"`
	// ModelPick is the launcher / model / effort picked for one of the assistant's conversations, which its next
	// messages run on (#5033, docs/assistant/model-config.md "A conversation's own model"); nil follows Model
	// configuration › Main chat (the main conversation, a scheduled task's) or what the conversation runs. Set by the
	// manager.
	ModelPick *ModelPick `json:"model_pick,omitempty"`
	// ArchiveChangedAt is the manager's last explicit archive/restore visibility change; nil when there was none. The
	// hub uses it only to order its legacy delete tombstones against that report. A pointer, not a time.Time: the 16
	// bytes it saves keep Session in its size class (session_size_test.go).
	ArchiveChangedAt *time.Time `json:"archive_changed_at,omitempty"`
	// NativeArchive is whether the tool that owns an archived (or just restored) agent session has it the same way
	// in its own archive (Codex's archived threads): only on GET /api/v1/sessions/archived rows, nil when the manager
	// never asked the tool.
	NativeArchive *NativeArchive `json:"native_archive,omitempty"`
	// Origin is who opened an assistant conversation and OriginRef what of (docs/protocol/protocol.md"Assistant conversation origins"):
	// user, channel, inbox, task, spawned, lark_agent, extension; "" is the user's. Set by the manager's fleet from
	// the assistant's snapshot (internal/assistant/fleet.go).
	Origin    string `json:"origin,omitempty"`
	OriginRef string `json:"origin_ref,omitempty"`
	// OpenedBy is the extension whose token opened an assistant conversation, by id (docs/protocol/protocol.md "Origin of
	// assistant chats"): noted when it opens, never backfilled; "" for any other. Set like Origin.
	OpenedBy string `json:"opened_by,omitempty"`
	// Compaction is the last compaction the user asked for in an assistant conversation (POST …/compact), kept by the
	// node until the next one: running, then done or failed (docs/assistant/assistant.md "Manual compaction").
	Compaction *Compaction `json:"compaction,omitempty"`
	// Channels are the channels messages came into this conversation from, newest first ("wechat", "feishu"):
	// the conversation list draws the latest's icon (docs/extensions/channels.md"Source labels"). Set by the manager's fleet
	// (internal/assistant/fleet.go).
	Channels []string `json:"channels,omitempty"`
	// Space / slot generalize where an agent lives in its host tool: for herdr a workspace and a tab
	// (docs/protocol/agent-states.md). Agents sharing a space are shown together as one collaborating group.
	SpaceKind  string `json:"space_kind,omitempty"` // herdr
	SpaceID    string `json:"space_id,omitempty"`
	SpaceName  string `json:"space_name,omitempty"`
	SpaceIndex int    `json:"space_index,omitempty"`
	// ParentSpaceID is the space this one is nested under in its host, same kind and machine (Orca: a child
	// worktree's parent, #2073); empty for a top-level space. Clients that know it fold the child under its parent.
	ParentSpaceID string `json:"parent_space_id,omitempty"`
	SlotID        string `json:"slot_id,omitempty"`
	SlotName      string `json:"slot_name,omitempty"`
	SlotIndex     int    `json:"slot_index,omitempty"`
	// PaneIndex is the pane's 0-based place in its slot by layout, top left first: 0 is the pane the others were split
	// from (the group's "Initiator" when an agent runs there). Absent (nil) when the host does not say where its panes sit
	// (older nodes, hosts without layouts, a pane missing from the layout). A pointer so that 0 is sent.
	PaneIndex *int `json:"pane_index,omitempty"`
	// State is the unified agent state the apps render (needs_you, working, unread, idle, offline, unknown),
	// computed by the hub from the host status, pending interactions and whether the user has seen the result.
	State string `json:"state"`
	// BackgroundWorking: State is working only because of background work (#2935): its turn ended while its
	// background commands, subagents, Monitors or workflows still run. Clients say "Working (background)". Set by the hub.
	BackgroundWorking bool `json:"background_working,omitempty"`
	// BackgroundWaiting: BackgroundWorking, and all that background work only waits (#6045): its background commands
	// sleep, loop on a check or watch something, using next to no CPU; no subagent or workflow runs. Clients show
	// "Waiting in the background" apart from Working (lighter, not in the Working count); State stays working, so an
	// older app shows Working. The node says whether its background commands wait; the manager keeps it only so.
	BackgroundWaiting bool `json:"background_waiting,omitempty"`
	// The fields from BackgroundWorking to QueuedRelayed sit together, not beside the ones they go with: with Go's
	// 8-byte allocation header a Session must stay at most 1,528 bytes to keep its 1,536-byte size class, and every
	// row the manager copies out of a node's list pays the next class's 256 bytes otherwise (internal/assistant's
	// TestSessionReadersCopyNoList, #2528). Mind the padding when adding a field.
	// WokeByTask: the latest turn was started by a background task reporting back, not by the user; finishing
	// it sends no push (the user did not ask for anything).
	WokeByTask bool `json:"woke_by_task,omitempty"`
	// Starting: Persona is opening its tab and waiting for claude (node/starting.go); messages sent now go in once it is
	// ready.
	Starting bool `json:"starting,omitempty"`
	// Quiet: the agent works (or worked) for a quiet task, one the main conversation opened while the user wanted
	// those told only there (#1731): set by the hub, also once the task is done. Clients leave it out of task
	// notifications and global badges; the Agents page shows it as always.
	Quiet bool `json:"quiet,omitempty"`
	// AwaitingFirstMessage: a Codex Persona opened with no first message (node/codex_waiting.go): Codex runs in its
	// terminal but has no thread until it gets one, so this row is keyed by an id of the node's, not the thread's.
	// It takes the first message (text only) and opens its terminal; once the thread exists the row goes and the
	// thread's comes, in the same pane (docs/integrations/codex.md "Starting without a first message").
	AwaitingFirstMessage bool `json:"awaiting_first_message,omitempty"`
	// QueuedRelayed: the last this many of Queued are messages from other conversations that an assistant
	// conversation's manager holds until its turn ends (docs/assistant/assistant.md "Queue while running"); the apps
	// show them but take back, delete, move and send now only the others (#4342). A uint16 here sits in the padding
	// before SeenAt, so a Session stays the size the allocation budgets measured.
	QueuedRelayed uint16    `json:"queued_relayed,omitempty"`
	SeenAt        time.Time `json:"seen_at,omitempty"`
	// StateSince is when the agent entered its current state, as the hub saw it (the hub restarting resets it):
	// lists sort "Working" by it, since updated_at moves on every step.
	StateSince time.Time `json:"state_since,omitempty"`
	// Queued is input typed while the agent works, waiting to be taken (shown above the composer, not in the chat).
	Queued []string `json:"queued,omitempty"`
	// Background lists commands / agents Claude started in the background that have not reported back yet
	// (the TUI's "2 shells still running").
	Background []BackgroundTask `json:"background,omitempty"`
	// Subagents the agent launched (Agent / Task tool); each one's transcript is the session "<id>@<agent id>".
	Subagents []Subagent `json:"subagents,omitempty"`
	// Workflows are the Claude Code workflow runs (the Workflow tool) the agent launched in the last day, newest first:
	// a running one is background work, as Background is (a running one is in Background too, kind "workflow").
	Workflows []Workflow `json:"workflows,omitempty"`
	// Loop is the /loop driving this agent, when there is one (it wakes itself up on a schedule).
	Loop *LoopInfo `json:"loop,omitempty"`
	// Goal is the goal the agent works toward, when one is set (Claude Code's /goal, a Codex thread goal; docs/integrations/agent-goal.md).
	Goal *GoalInfo `json:"goal,omitempty"`
	// Plan is the agent's own task list (Claude Code's TodoWrite, or TaskCreate / TaskUpdate), when it keeps one: how far
	// along it is and the step in progress (docs/integrations/agent-goal.md "Plan"). Absent for Codex and for runtimes
	// whose conversation is not a Claude transcript on the node.
	Plan *PlanInfo `json:"plan,omitempty"`
	// Ports are the TCP ports the agent's processes listen on that answer HTTP (docs/operations/ports.md): the dev servers
	// it started, which the apps open through the hub.
	Ports     []Port    `json:"ports,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	LastText  string    `json:"last_text,omitempty"`
	// Asks is what the agent's last reply asks the user, when its closing words put a question or a decision to
	// them (transcript.AskOf, #5096): read by the node off the whole reply, since LastText is only its beginning.
	Asks string `json:"asks,omitempty"`
	// UltracodePending: a requested switch waits for an idle input box (grouped here to keep rows compact).
	UltracodePending bool `json:"ultracode_pending,omitempty"`
	// AsksRead: the node read Asks off the reply (an empty Asks then means it asks nothing, or the user answered it);
	// without it (an older node, a host that does not say) the hub reads the list's last words when they are whole.
	AsksRead bool `json:"asks_read,omitempty"`
	// AwaitsReply: the agent stopped on a question to the user nobody has answered yet (docs/protocol/agent-states.md "Awaiting your reply",
	// #5096) — it asks (Asks, or the triage says it waits on the user) and has no card, error or background work. Set by
	// the hub on a stopped session (Unread or Seen); an Seen one is listed as Awaiting Reply, not folded with the idle ones.
	AwaitsReply bool `json:"awaits_reply,omitempty"`
	// TurnErrorResumes: the CLI said it goes on by itself once the usage limit behind TurnError resets (Claude Code's
	// "Usage limit reached · continuing automatically at …") and nothing cancelled that since. Typing into the session
	// cancels it, so nothing is sent to it while it waits.
	TurnErrorResumes bool `json:"turn_error_resumes,omitempty"`
	// TurnErrorAfterWork: the turn did some work (a reply, a tool call) before TurnError ended it; false when the error
	// was all it did (a continuation the limit turned down again).
	TurnErrorAfterWork bool `json:"turn_error_after_work,omitempty"`
	// Known transcript provenance, packed with flags; its JSON remains a string.
	TitleSource TitleSource `json:"title_source,omitempty"`
	// TurnError: the agent's latest turn ended in a model API error (the connection lost mid-response, overloaded,
	// rate limited, the model service down…). The node sets it when the agent's own record says the turn failed
	// (Codex: a rollout's task_complete.error, an app-server's failed turn, #2185); otherwise the hub reads it off the
	// last words, as Claude Code writes them ("API Error: …"). The hub keeps it only while the agent is stopped: empty
	// while it works or when the turn ended normally. The task it works for is interrupted, not done
	// (docs/assistant/todos.md "State calculation rules", #1516).
	TurnError string `json:"turn_error,omitempty"`
	// TurnErrorKind is what the agent's record says of TurnError, when it says (Claude Code's error field, Codex's
	// error kinds): TurnErrorTransient (worth a "Continue"), TurnErrorUser (the user must act) or TurnErrorUsageLimit
	// (a subscription's usage limit, until it resets); empty: read off the text.
	TurnErrorKind string `json:"turn_error_kind,omitempty"`
	// TurnErrorResetsAt is when the subscription usage limit that ended the turn resets (TurnErrorKind
	// TurnErrorUsageLimit): what the agent's record says (Claude Code's quotaLimits.resetsAt, the full window of a Codex
	// rollout's own rate limits), else what the hub reads off Claude Code's words when they name a time zone. Nil: not
	// known, and nothing waits for it. Only a subscription's limit has one: a gateway's or an API key's 429 never does.
	TurnErrorResetsAt *time.Time `json:"turn_error_resets_at,omitempty"`
	// TurnErrorCode says what is wrong when the hub can tell from TurnError (docs/assistant/onboarding.md 5.2): one of the
	// TurnErrorCode* values, for a card that says what to fix (sign in, the key, the model). Empty: not known.
	TurnErrorCode string `json:"turn_error_code,omitempty"`
	// ModelFallback is the refusal fallback still active in this session, as the runtime recorded it.
	ModelFallback *ModelRefusal `json:"model_fallback,omitempty"`
	// ModelRestore: "back to the configured model" was asked for this session's refusal fallback (#2528) and no newer
	// model evidence has come since. Not proof the configured model runs: only a newer reply says that.
	ModelRestore *ModelRestore `json:"model_restore,omitempty"`
	// Work is where the session stands in its work (docs/assistant/agent-work.md): working, blocked on a named precondition, or
	// done, with a line saying what it waits for or what it concluded. Set by the hub; absent before the hub has
	// triaged a stopped session's turn.
	Work *WorkState `json:"work,omitempty"`
	// Activity says what the agent is doing when its latest step is a tool call no text came after ("running tests",
	// "reading tasks.go", "waiting for CI", transcript.Activity), and ActivityAt when that call started: while an agent only runs
	// tools its last_text stays put for minutes, and a task card showing it looked stuck. Empty once it writes text.
	Activity   string     `json:"activity,omitempty"`
	ActivityAt *time.Time `json:"activity_at,omitempty"`
	// Rev changes whenever the transcript does (its size): the app pulls new items for every session whose rev
	// differs from what it has, without opening it. Changes travel as the light `revs` event.
	Rev                 int64 `json:"rev,omitempty"`
	PendingInteractions int   `json:"pending_interactions"`
	// Suggestion is the next message Claude Code proposes after a turn (grey in its empty input box), read off the
	// screen; gone once the agent works again or a message is sent.
	Suggestion string `json:"suggestion,omitempty"`
	// StartError: the start failed (shown in the chat for a while). StartErrorCode is the start's coded refusal
	// when it was one (claude_missing, launcher_missing…: the code a synchronous start is answered 409 with), with
	// StartErrorArgs, the refusal's details (launcher, where, missing, pane_shell…, docs/protocol/protocol.md "Error body"):
	// the hub writes StartError as the sentence for that code in the user's language, and a client offers its remedy.
	StartError     string            `json:"start_error,omitempty"`
	StartErrorCode string            `json:"start_error_code,omitempty"`
	StartErrorArgs map[string]string `json:"start_error_args,omitempty"`
	// Todo is the active (uncompleted, unreleased) todo this session is linked to (assistant/todos.go).
	Todo *TodoRef `json:"todo,omitempty"`
	// SpawnFrom is persona_from reported by node for todo derivation (#762), stripped by hub before client push.
	SpawnFrom string `json:"spawn_from,omitempty"`
	// AttentionReason is why the session waits for the user, in the host's words (a botmux attention's reason).
	AttentionReason string `json:"attention_reason,omitempty"`
	// FailureReason: the host stopped the session's turn on an error it will not retry by itself (a botmux notice),
	// until a message goes to the session. The apps show it with a retry; the session is not waiting for the user.
	FailureReason string `json:"failure_reason,omitempty"`
	// ExternalURL opens the session where it lives outside Persona (a botmux session's Feishu topic).
	ExternalURL string `json:"external_url,omitempty"`
	// Runtime: an assistant conversation's runtime as it actually runs ("agentsdk"; "" is the CLI, #2455).
	Runtime string `json:"runtime,omitempty"`
	// RuntimeFallback: why an assistant conversation asked to run on the Agent SDK runs on the CLI instead (no
	// Node.js 22+, no bridge package, no claude); "" when it runs as asked.
	RuntimeFallback string `json:"runtime_fallback,omitempty"`
	// Chat, Topic, Dispatched: a botmux session's Feishu chat, its topic's root message and the topics it dispatched
	// (`botmux dispatch`), for the todos to follow (docs/assistant/todos.md "botmux following"); stripped by hub like SpawnFrom.
	Chat       string   `json:"chat,omitempty"`
	Topic      string   `json:"topic,omitempty"`
	Dispatched []string `json:"dispatched,omitempty"`
	// AlsoOpenIn are the other hosts (host kinds: herdr, herdr-core, orca…) the session is live in at the same time
	// (#2576): it is listed and driven in the host that owns it (node/ownership.go); the apps warn of the conflict.
	AlsoOpenIn []string `json:"also_open_in,omitempty"`
	// Host is the engine host the session lives in (#2576): the kind of its pane (herdr, herdr-core, orca, botmux) or
	// paseo for the Paseo runtime's; empty for transcripts with no live host and for other runtimes. The apps label a
	// row with it when the machine enables more than one host.
	Host string `json:"host,omitempty"`
	// Hierarchy is where the session stands among the sessions that started one another, and when it began
	// (docs/protocol/protocol.md "Session hierarchy"); nil for most rows. Its members are written on the row itself
	// (folded_under, folded_as, children, started_at, asking_parent_session_id: sessionWire), so it has no key of its
	// own. Read it with FoldedUnder, FoldedAs, Children, StartedAt and AskingParentSessionID; it is shared by every copy
	// of the row, so it is never written in place: SetFold, SetStartedAt and SetAskingParentSessionID put a new one.
	Hierarchy *SessionHierarchy `json:"-"`
}

// Session.TurnErrorKind values.
const (
	TurnErrorTransient  = "transient"   // the model service failed for a moment: worth a "Continue"
	TurnErrorUser       = "user"        // sign-in, context window, the model…: the user must act
	TurnErrorUsageLimit = "usage_limit" // a subscription's usage limit: it goes on once the limit resets (TurnErrorResetsAt)
)

// Session.TurnErrorCode values (docs/assistant/onboarding.md 4.6).
const (
	TurnErrorNotLoggedIn              = "not_logged_in"              // Claude Code on that machine is not signed in
	TurnErrorAuthFailed               = "auth_failed"                // the key or token was refused or has expired
	TurnErrorModelRoute               = "model_route"                // the model name is not one the service or gateway knows
	TurnErrorRateLimited              = "rate_limited"               // too many requests, or a usage limit
	TurnErrorModelFallbackUnavailable = "model_fallback_unavailable" // a refusal selected a model the service cannot use
)

// Codes a call refuses with when the assistant's conversation cannot start on a machine (docs/assistant/onboarding.md 4.6, 5.2):
// rpc.Error codes the hub answers 409 with, the text in the request's language (apierr.Call).
const (
	CodeNoMachine       = "no_machine"       // no machine to run the main conversation on
	CodeMachinePending  = "machine_pending"  // the machine has not been confirmed as the user's yet
	CodeClaudeMissing   = "claude_missing"   // Claude Code is not installed (or not on the launcher's PATH) there
	CodeLauncherMissing = "launcher_missing" // the launcher (a ~/.zshrc function or alias) is not there
	// CodeTerminalNotReady: the new tab's terminal would not take the start command (herdr's pane_send_failed); nothing
	// ran, the tab is closed, a retry may work (node/typedstart.go). 503, retryable.
	CodeTerminalNotReady = "terminal_not_ready"
	// CodeAgentStartStalled: the agent opened but its session never became identifiable. This says nothing about
	// launcher health and must not trigger an automatic retry or a health detour.
	CodeAgentStartStalled = "agent_start_stalled"
	// CodeShellWaiting: the new tab's shell stopped on a question of its startup files (an update prompt, [Y/n]) before
	// the start command was typed (#2570). Nothing ran; the tab is kept a while for someone to answer (node/shellsettle.go).
	CodeShellWaiting = "shell_waiting"
	// CodeStartLineRejected: the new tab's shell did not take the typed start line, twice (a startup prompt ate its first
	// characters, #2570). Nothing ran; the tab is closed.
	CodeStartLineRejected = "start_line_rejected"
	// CodeDirectoryUntypable: the start's directory has a control character in its name, which a shell's line editor
	// would act on when an engine types the directory (Orca's `cd`, node/hostorca_launch.go). Nothing ran; a refusal of
	// the request, not the launcher's fault.
	CodeDirectoryUntypable = "directory_untypable"
)

// Codes session.modes refuses with (node/modes.go), the text in the request's language (apierr.Call). 409.
const (
	// CodeModesOnComputer: a Claude outside Persona's terminals (Claude's desktop app, another terminal); its mode and
	// effort change only there. Its row's capabilities say modes: false.
	CodeModesOnComputer = "modes_on_computer"
	// CodeModesNotRunning: no process or pane runs the session; a message resumes it.
	CodeModesNotRunning = "modes_not_running"
)

// PendingSwitch is Session.PendingSwitch: the launcher the Agent moves to, the model and effort to run it with ("":
// the launcher's own default model; the effort it runs with now), and when the user picked it.
type PendingSwitch struct {
	Launcher string    `json:"launcher"`
	Model    string    `json:"model,omitempty"`
	Effort   string    `json:"effort,omitempty"`
	At       time.Time `json:"at"`
}

// Codes a launcher switch of a running Agent refuses with (node/launcher_switch.go, #5035): 409, nothing typed, the
// text in the request's language (apierr.Call, rpc-errors.<code>); the node's own words, with the names, are the
// body's detail. switch_stale comes only from POST …/modes; the not-stopped and failed ones only from the user's
// message that carries a switch out; the rest from both, a message's when something changed since the pick. A message
// refused with one of them drops the switch: nothing tries it again by itself.
const (
	// CodeSwitchStale: the pick was made against another launcher than the session runs on by now (POST …/modes'
	// from): a switch was carried out meanwhile. Nothing was recorded.
	CodeSwitchStale = "switch_stale"
	// CodeSwitchUnderWay: a switch of the session is being carried out right now (maybe on the last connection's Node
	// of a resident node): what would type into its pane or press its keys (a model, mode or effort change, a rewind,
	// an answer, keys, a close), and a message that would carry out another, is refused with nothing done.
	CodeSwitchUnderWay = "switch_under_way"
	// CodeSwitchPickModel: no model was picked, and what the launcher runs by default cannot be seen (a wrapper script,
	// a provider that names no model): resumed without --model, the conversation would go on with the model of the
	// launcher it ran on.
	CodeSwitchPickModel = "switch_pick_model"
	// CodeSwitchUnsupported: not a Claude Code Agent running in a terminal Persona can restart it in (Codex, another
	// CLI, a runtime's session, an assistant conversation, botmux).
	CodeSwitchUnsupported = "switch_unsupported"
	// CodeSwitchLauncherMissing: the launcher picked is not on the session's computer.
	CodeSwitchLauncherMissing = "switch_launcher_missing"
	// CodeSwitchOtherAgent: the launcher picked runs another agent than Claude Code.
	CodeSwitchOtherAgent = "switch_other_agent"
	// CodeSwitchModelLocked: a model was picked, and the launcher fixes its own.
	CodeSwitchModelLocked = "switch_model_locked"
	// CodeSwitchConfigDir: the launcher runs Claude Code with another config directory than the one the conversation
	// is in: --resume would not find it there, and the sign-in lives in that directory too.
	CodeSwitchConfigDir = "switch_config_dir"
	// CodeSwitchNotStopped: the agent did not quit; it runs on as before.
	CodeSwitchNotStopped = "switch_not_stopped"
	// CodeSwitchFailed: the agent did not come up on the new launcher; it is back on its own one.
	CodeSwitchFailed = "switch_failed"
	// CodeSwitchFailedStopped: the agent did not come up on the new launcher and could not be brought back on its own
	// one: it has stopped (Resume on its terminal page).
	CodeSwitchFailedStopped = "switch_failed_stopped"
)

// TodoRef is a session's link to an active todo (docs/assistant/todos.md).
type TodoRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// SourceRuntime is the source_kind of a session an agent runtime lists (docs/integrations/runtimes.md): source_instance_id names
// the runtime ("dsh"); its capabilities come from what the runtime declared (no terminal, never attach).
const SourceRuntime = "runtime"

// Port is a listening TCP port on a machine (docs/operations/ports.md).
type Port struct {
	Port int `json:"port"`
	// Addr is what it is bound to: 127.0.0.1, 0.0.0.0, ::, ::1 or one of the machine's addresses.
	Addr    string `json:"addr,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"` // the command, as ps / lsof name it (node, python3.12…)
	Title   string `json:"title,omitempty"`   // the <title> of its page at /
}

// NativeArchive is a session's state in its own tool's archive, as its manager last asked for it
// (docs/protocol/protocol.md "session.archive").
type NativeArchive struct {
	// Archived is what the tool was asked for: archived (true) or restored.
	Archived bool `json:"archived"`
	// State: done (the tool has it so), pending (not yet: the machine offline, the agent working, an error; tried
	// again), unsupported (the tool has no archive Persona can drive: the archive is Persona's alone).
	State string `json:"state"`
	// Error is why the last try did not finish, while pending.
	Error string `json:"error,omitempty"`
}

// Direction is a leader conversation's record (assistantwire.SessionView.Direction, GET /api/v1/assistant/leaders): Project the knowledge-base project's slug ("" while
// none), By "assistant" or "user" (the owner's choice, never redone by the hub), At when it became a direction, EndedAt
// when it stopped being a leader (nothing is deleted).
type Direction struct {
	Project string     `json:"project,omitempty"`
	By      string     `json:"by"`
	At      time.Time  `json:"at"`
	EndedAt *time.Time `json:"ended_at,omitempty"`
}

// ModelPick is what an assistant conversation was set to run on in the apps (PUT
// /api/v1/assistant/conversations/{id}/model): a launcher and, when one was picked, its model and reasoning effort.
type ModelPick struct {
	Launcher string `json:"launcher"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
}

// ServiceTier is a speed advertised by the native Codex model catalog.
type ServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Tokens32 is n as Session.ContextTokens and ContextWindow hold it: saturated at the int32 bounds.
func Tokens32(n int) int32 {
	return int32(max(min(n, math.MaxInt32), math.MinInt32))
}
