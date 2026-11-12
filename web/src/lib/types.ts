import type { UsageCard } from "./tokens";
// The hub's wire types (docs/protocol/protocol.md). Only the fields the web client reads.

export type AgentState = "needs_you" | "working" | "unread" | "idle" | "offline" | "unknown";

/** An engine as the machine's node found it (docs/integrations/engines.md): `note` says why one that is there cannot be used. */
export interface DetectedEngine {
  id: string;
  name: string;
  available: boolean;
  version?: string;
  note?: string;
}

export interface Machine {
  id: string;
  /** The manager it belongs to (docs/protocol/protocol.md "Per-manager routing"); none: the default one. */
  manager?: string;
  name: string;
  online: boolean;
  error?: string;
  hostname?: string;
  os?: string;
  claude_version?: string;
  herdr_version?: string;
  node_version?: string;
  node_mismatch?: boolean;
  deploying?: boolean;
  can_deploy?: boolean;
  deploy_reason?: string;
  deploy_phase?: "waiting_idle" | "transferring" | "restarting" | "confirming";
  deploy_error?: string;
  system?: { name?: string; model?: string; cpu?: string; cores?: number; memory?: number };
  /** The terminal engine its sessions come from (herdr when absent); `engines`: what its node found, empty while offline. */
  engine?: string;
  engines?: DetectedEngine[];
  /** The hosts enabled side by side and the default one (#2576, docs/integrations/engines.md "Host sets"); absent from older hubs. */
  engine_hosts?: string[];
  engine_default?: string;
  /** What to preselect when the machine is paired: what its node found available, else herdr (only while online). */
  engine_suggested?: { hosts: string[]; default: string };
  /** The node is too old for sets and runs `engine` alone until it is updated. */
  engine_sets_unsupported?: boolean;
  kind?: string;
  arch?: string;
  home?: string;
  last_seen?: string;
  /** Interconnected hubs (docs/archive/operations/plan-hub-mesh.md 2.6): the hub that owns this computer, and the way there. */
  hub_id?: string;
  hub_name?: string;
  machine_uid?: string;
  route?: { via: string; hops: number; rtt_ms?: number };
  /** Operating it through Cua Driver (the hub's extension cua, docs/extensions/computer-use.md): only while the extension is on and it is a Mac. */
  computer?: {
    enabled: boolean; installed?: boolean; daemon?: boolean; ready?: boolean; accessibility?: string; screen_recording?: string;
    desktop?: string; screen_sharing?: boolean; note?: string;
  };
  /** ego lite (the hub's extension ego, docs/plans/plan-ego-browser.md): only while the extension is on and it is a Mac. */
  browser?: import("./browser").MachineBrowser;
  /** feature machine_trust (docs/protocol/protocol.md "Computer trust"): "pending" while the user has not said it is theirs; omitted = confirmed. */
  trust?: "pending";
  /** What `error` is about, for words of our own: node_service_down / link_offline / never_connected / node_outdated. */
  error_code?: string;
}

/** The hubs of this hub's interconnected group, from the signed-in hello (docs/archive/operations/plan-hub-mesh.md 2.6). */
export interface MeshHub {
  hub_id: string;
  name: string;
  online?: boolean;
  self?: boolean;
  endpoints?: string[];
}

/** A plan or task list a folded call wrote (the hub keeps it with the fold, transcript.foldTaskSnapshots): `replace:
 *  "todo"` is a TodoWrite's whole list. */
export interface FoldTaskSnapshot {
  replace?: string;
  entries: { id: string; subject?: string; description?: string; status?: string }[];
  source_item_id: string;
  source_tool_use_id: string;
  source_seq: number;
}

export interface Subagent {
  id: string;
  session_id: string;
  type?: string;
  description?: string;
  running?: boolean;
  tool_use_id?: string;
  /** The subagent that launched it; absent when the session did. One not in the list: shown at the top level. */
  parent_id?: string;
  /** A fork of its launcher's conversation. */
  fork?: boolean;
  background?: boolean;
  started_at?: string;
  updated_at?: string;
  last_text?: string;
  /** Tool calls it made (newer nodes, #5602). */
  steps?: number;
  /** Its latest tool call: what it is doing now while it runs (newer nodes). */
  last_tool?: { id?: string; name: string; summary: string };
  /** The model it ran on, from its own transcript (newer nodes, #5030); never the model its call asked for. */
  model?: string;
  /** Its latest reply's context plus output tokens, as a workflow agent's `tokens` (newer nodes, #5030). */
  tokens?: number;
}

/** Session.model_pick (#5033): model absent is the launcher's default, effort absent the one it runs with. */
export interface ModelPick {
  launcher: string;
  model?: string;
  effort?: string;
}

/** Session.pending_switch (#5035). */
export interface PendingSwitch {
  launcher: string;
  model?: string;
  effort?: string;
  at?: string;
}

export interface SessionCapabilities {
  read?: boolean;
  send?: boolean;
  interrupt?: boolean;
  approve?: boolean;
  terminal?: boolean;
  attach?: boolean;
  /** Whether the runtime explicitly supports session model changes. */
  model?: boolean;
  /** Closing the agent independently of terminal access. Older nodes retain terminal-only closing. */
  close?: boolean;
  /** The permission mode and effort chips can change it (session.modes, #1843): the node's word, absent from older nodes. */
  modes?: boolean;
}

/** A session's work state (docs/assistant/agent-work.md): working, blocked on a named precondition, or done. */
export interface WorkState {
  phase: "working" | "blocked" | "done" | "";
  /** What a blocked one waits on: user (a decision), agent, external (a deploy, CI, a time), transient (a model error). */
  on?: "user" | "agent" | "external" | "transient";
  /** One line for the lists: what it waits for, or what it concluded. */
  line?: string;
  wait?: string;
  then?: string;
  /** When the hub acts next on its own: "Continue" (transient) or a wake (external, agent). */
  wake_at?: string;
  /** When a subscription usage limit that stopped the turn resets (docs/assistant/agent-work.md "Usage limits"). */
  resets_at?: string;
  attempts?: number;
  max?: number;
  agent?: { machine_id: string; session_id: string; label?: string };
  link?: { url: string; expires_at?: string };
  artifacts?: { kind: string; ref: string }[];
  /** What the agent said of its own work as the turn ended (lib/closeout.ts). A turn the hub could not call finished
   *  (the closeout says merged and it is not) carries it with an empty `phase`. */
  closeout?: Closeout;
  interaction_id?: string;
}

/** An agent's closeout (docs/assistant/agent-work.md "Closeout"): its own claim of where the work stands. `merged` and
 *  `deployed` are "yes" or "no", absent when not said; `checked` is what the hub found when it looked itself, absent
 *  where it could not tell. Session lists leave out `did` and `verified`. */
export interface Closeout {
  status: "done" | "not_done" | "blocked" | (string & {});
  /** Who or what a blocked turn waits for. */
  waiting?: string;
  did?: string[];
  merged?: string;
  deployed?: string;
  verified?: string;
  missing?: string;
  checked?: { merged?: string; deployed?: string };
}

/** Settings › Session triage (GET / PUT /agents/triage): all off until the user turns them on. */
export interface TriageSwitches {
  enabled: boolean;
  auto_continue: boolean;
  wake: boolean;
  decide: boolean;
  /** Detect repeated tool calls during a turn (off when absent). */
  tool_loop?: boolean;
  /** Ask for a closeout (#3612): hold an agent's stop once when a turn that did work ends without one (off when absent). */
  closeout?: boolean;
  /** Ask afterwards (#3616): one status question to an unsupervised agent whose turn did work and ended without a closeout (off when absent). */
  closeout_ask?: boolean;
  /** Remind When Seen, Unanswered (#5096): remind once when an agent asked the user something and it stays unanswered after they saw
   *  it. Rule-based, no tokens, so it is on when absent and does not depend on `enabled`. */
  reply_reminder?: boolean;
  /** How long after the user saw it the reminder goes out, in minutes (the hub's default when absent). */
  reply_reminder_minutes?: number;
}

/** A port an agent's processes listen on that answers HTTP (docs/operations/ports.md): its dev server. */
export interface Port {
  port: number;
  addr?: string;
  pid?: number;
  process?: string;
  title?: string;
}

export interface Session {
  /** The engine host it lives in (herdr, herdr-core, orca, paseo…), and the other hosts it is live in at once (#2576). */
  host?: string;
  also_open_in?: string[];
  /** Node-verified physical working directory; the original cwd stays available for existing session flows. */
  workspace_cwd?: string;
  /** The main checkout of the repository cwd belongs to: the node reads it off git (#1805), Paseo names its own (#2840).
   *  "By repo" groups by it. */
  repo_root?: string;
  /** The top of the git working tree cwd is in (repo_root for the main checkout) and its branch, from the node (#1805). */
  worktree?: string;
  branch?: string;
  machine_id: string;
  session_id: string;
  /** The manager it belongs to (docs/protocol/protocol.md "Per-manager routing"); none: the default one. */
  manager?: string;
  title: string;
  cwd: string;
  status: string;
  starting?: boolean;
  /** A Codex opened with no first message: it runs in its terminal but has no thread yet, so this row's id is the
   *  computer's, not the thread's. Its first message (text only) goes in here or in the terminal; then the row goes and
   *  the thread's comes, in the same pane (docs/integrations/codex.md "Starting without a first message"). */
  awaiting_first_message?: boolean;
  start_error?: string;
  /** The failed start's coded refusal (launcher_missing, claude_missing…) and its details; start_error is the hub's sentence for it. */
  start_error_code?: string;
  start_error_args?: Record<string, string>;
  state?: AgentState;
  state_since?: string;
  agent?: string;
  agent_name?: string;
  source_kind?: string;
  source_thread_id?: string;
  source_instance_id?: string;
  parent_thread_id?: string;
  read_only_reason?: string;
  capabilities?: SessionCapabilities;
  attached?: boolean;
  /** "Branch to new chat" works for it (a Claude Code conversation, node/fork.go); older nodes never say so. */
  can_fork?: boolean;
  /** What to call an agent the app does not know (a runtime's own name, "DSH"; docs/integrations/runtimes.md). */
  agent_label?: string;
  /** The client the agent runs in: "desktop" (its desktop app) or "cli" (its command line); absent when unknown (#6390). */
  agent_surface?: string;
  launcher?: string;
  /** A Persona chat's own pick (feature `assistant_model_pick`, #5033): the launcher, model and effort its next messages
   *  run on, kept on the hub for every device. Absent: it follows the default (the main chat) or keeps what it runs
   *  (docs/assistant/model-config.md "A conversation's own model"). */
  model_pick?: ModelPick;
  /** A switch of this running Claude Code Agent to another launcher (feature `launcher_switch`, #5035) that its next
   *  message carries out: the launcher, the model and effort picked (absent: the launcher's default, the effort it runs
   *  with now). `launcher` above stays the one it runs on until then (docs/protocol/protocol.md "Switching a running Agent
   *  to another provider"). */
  pending_switch?: PendingSwitch;
  model?: string;
  permission_mode?: string;
  effort?: string;
  /** This Claude Code may switch to Allow All while it runs: it was started with a flag that allows it (#1379). */
  bypass_available?: boolean;
  /** What the model saw in the latest reply and the model's window, in tokens (#2430); omitted while the node cannot tell. */
  context_tokens?: number;
  context_window?: number;
  /** The levels `effort` may be set to when they are not Claude Code's: a Codex thread driven through its app-server (only its effort changes; docs/protocol/protocol.md). */
  efforts?: string[];
  /** Native Codex speed. Absent while the current setting is unknown. */
  service_tier?: string;
  service_tiers?: { id: string; name: string; description?: string }[];
  /** Claude Code's session-only ultracode switch as the node last saw it (docs/protocol/protocol.md "Claude Code ultracode"):
   *  absent is unknown, not off. */
  ultracode?: boolean;
  /** Why the switch can't be used, was refused or could not be confirmed (the node's words, in Chinese). */
  ultracode_note?: string;
  /** A switch request waits for the turn to end ("Applies next turn"); not the requested value. */
  ultracode_pending?: boolean;
  /** It was on before the agent resumed, and the resumed process has it off: the chat offers to turn it back on. */
  ultracode_lost_at?: string;
  assistant?: boolean;
  /** The assistant's space it runs in: "health" for the health space's (docs/assistant/health.md). */
  space?: string;
  started_by_persona?: boolean;
  space_id?: string;
  space_name?: string;
  space_index?: number;
  /** The space this one is nested under in its host, same machine and kind (Orca: a child worktree's parent, #2073). */
  parent_space_id?: string;
  slot_id?: string;
  slot_name?: string;
  slot_index?: number;
  /** The pane's 0-based place in its tab by layout, top left first (#2320): 0 is the pane the others were split from,
   *  the tab's "Lead" when an agent runs there. Absent when the host does not say (older nodes, hosts without layouts). */
  pane_index?: number;
  /** The host's pane, tab and workspace ids (herdr: bare "w9:p1"; other hosts "<kind>:<id>"). Workspace ids change
   *  across a herdr restart: never key anything stored by them (pins and folds go by machine/space name). */
  pane_id?: string;
  tab_id?: string;
  workspace_id?: string;
  /** Which host the space belongs to ("herdr", "orca", a runtime id…): space ids are unique only within one. */
  space_kind?: string;
  updated_at?: string;
  seen_at?: string;
  last_text?: string;
  /** The question its last reply put to the user (#5096), when its closing words ask something. */
  asks?: string;
  /** It stopped (unread or seen) on a question to the user nobody has answered yet (#5096): Awaiting Reply once seen. */
  awaits_reply?: boolean;
  /** Its latest step when that is a tool call no text came after ("running tests", "reading tasks.go"), and when it started. */
  activity?: string;
  activity_at?: string;
  /** Its latest turn ended in a model API error (the hub sets it while it is stopped, #1516): its task is interrupted. */
  turn_error?: string;
  /** What the agent's record says of turn_error: "transient" (worth a "Continue"), "user" (the user must act),
   *  or "usage_limit" (a subscription limit, until it resets). */
  turn_error_kind?: string;
  /** What is wrong, when the hub can tell from turn_error (docs/assistant/onboarding.md 5.2): not_logged_in / auth_failed /
   *  model_route / rate_limited / model_fallback_unavailable (the model Claude Code fell back to after a refusal can't
   *  be used: no "Continue"). */
  turn_error_code?: string;
  /** The refusal fallback still active in this session, as Claude Code recorded it (#2289). */
  model_fallback?: ModelRefusal;
  /** "Back to the configured model" was asked and no newer reply has come yet (#2528): the next start asks for `to`. Not
   *  proof the configured model runs. */
  model_restore?: ModelRestore;
  /** When turn_error_kind is "usage_limit": when that subscription limit resets. Absent when it is not known. */
  turn_error_resets_at?: string;
  /** The CLI continues by itself once the usage limit resets. */
  turn_error_resumes?: boolean;
  /** Where it stands in its work (docs/assistant/agent-work.md): set by the hub once it triaged a stopped turn. */
  work?: WorkState;
  pending_interactions?: number;
  queued?: string[];
  /** In a Persona chat, the last this many of `queued` came from other conversations: the assistant delivers them when
   *  the turn ends, and the strip's actions leave them alone (#4342). */
  queued_relayed?: number;
  subagents?: Subagent[];
  loop?: LoopInfo;
  /** The goal it works toward until it holds (Claude Code's /goal, a Codex thread goal; docs/integrations/agent-goal.md). */
  goal?: GoalInfo;
  /** Its own task list (Claude Code's TodoWrite / TaskCreate): how far along it is (docs/integrations/agent-goal.md "Plan"). */
  plan?: PlanInfo;
  background?: BackgroundTask[];
  /** Bounded list summaries omit task arrays; these scalars are authoritative when present. */
  waiting?: boolean;
  background_count?: number;
  running_subagents?: number;
  running_workflows?: number;
  /** Independent shell/Monitor count; unlike background_count, excludes workflows and subagents. */
  background_tasks_count?: number;
  /** Newest running workflow, compact metadata only (no agents, phases or tools). */
  workflow_progress?: Workflow;
  parent_session_id?: string;
  root_session_id?: string;
  is_subagent?: boolean;
  /** The listed session this child folds under (an Agent another Agent started: a Codex subagent, an Agent a Paseo Agent
   *  created), set by the hub's manager (docs/protocol/protocol.md "Session hierarchy", #5120): the web never lists or
   *  counts it on its own, and draws it as a card in that session's chat. */
  folded_under?: string;
  /** On a session other sessions fold under: how many, and how many of them work or need the user (by their own state). */
  children?: SessionChildren;
  /** When the session began, where the node knows it (child sessions at least). */
  started_at?: string;
  /** Claude Code workflow runs it launched in the last day, newest first (#2935); older hubs leave it out. */
  workflows?: Workflow[];
  /** Working only because of background work: its turn is over, workflows / background commands / subagents run on
   *  (#2935). Older hubs leave it out. */
  background_working?: boolean;
  /** Working only because of background commands that only wait (sleep, a check loop, gh … --watch; #6045): shown
   *  as "Waiting in the background", lighter, and not counted as Working. An older hub never sends it. */
  background_waiting?: boolean;
  rev?: number;
  suggestion?: string;
  /** An assistant conversation moved out of the list (docs/assistant/assistant.md "Managing its own conversations"), or
   *  an archived agent (docs/protocol/protocol.md "Agent session archives": only the Archived list has those). */
  archived?: boolean;
  /** The HTTP ports its processes listen on (docs/operations/ports.md): closing it would stop those servers, and they are
   *  the dev servers a preview offers (docs/web/preview.md). */
  ports?: Port[];
  /** Who opened an assistant conversation (docs/protocol/protocol.md "Origin of assistant chats"): user, channel, inbox, task, spawned,
   *  lark_agent, extension, or one this page doesn't know; none is the user's (older hubs never say). */
  origin?: string;
  /** What it was opened for: the inbox item, the task, the conversation, the channel, the extension. */
  origin_ref?: string;
  /** The channels messages came into it from, newest first (docs/extensions/channels.md "Source markers"). */
  channels?: string[];
  todo?: { id: string; title: string };
  /** Works (or worked) for a quiet task (#1731): not in the global badge; the main conversation tells about it. */
  quiet?: boolean;
  /** Why it waits for the user, in its host's words (a botmux attention's reason); only while it needs you (docs/integrations/engines.md "botmux"). */
  attention_reason?: string;
  /** The host stopped its turn on an error it will not retry by itself (a botmux notice), until a message goes to it:
   *  shown over the composer with "Retry" (iOS FailureNote). */
  failure_reason?: string;
  /** Where it lives outside Persona (a botmux session's Feishu topic): "Open in Lark". */
  external_url?: string;
  /** An assistant conversation's runtime as it runs ("agentsdk"; absent: the CLI, #2455). */
  runtime?: string;
  /** Why an assistant conversation asked to run on the Agent SDK runs on the CLI instead. */
  runtime_fallback?: string;
  /** The last compaction the user asked for in an assistant conversation (POST …/compact, #1727). */
  compaction?: Compaction;
}

/** A compaction the user asked for (proto.Compaction): running, done or failed (`error` says why). */
export interface Compaction {
  state: "running" | "done" | "failed";
  error?: string;
  started_at: string;
  finished_at?: string;
}

/** Where the context was compacted (proto.Compact): "manual" (/compact) or "auto", the size before it. `notes`: the
 *  notices of the compaction folded into it here (lib/compaction.ts), never from the hub. */
export interface CompactInfo {
  trigger?: string;
  pre_tokens?: number;
  notes?: string[];
}

/** A goal the agent keeps working toward until it holds (proto.GoalInfo, docs/integrations/agent-goal.md). */
export interface GoalInfo {
  objective: string;
  /** active / paused / blocked / usage_limited / budget_limited / complete (Claude Code's are only active or complete). */
  status: string;
  /** Claude Code's latest check of the condition: why it holds, or what is still missing. */
  reason?: string;
  tokens_used?: number;
  token_budget?: number;
  seconds?: number;
  updated_at?: string;
}

/** An agent's own task list, counted (proto.PlanInfo): done of total items, and the one in progress. */
export interface PlanInfo {
  done: number;
  total: number;
  /** The item in progress (its activeForm), one line; absent when none is. */
  current?: string;
  at?: string;
}

/** A /loop: Claude re-runs a prompt on its own, scheduling each wakeup ("wakeup") or by cron ("cron"). */
export interface LoopInfo {
  kind: string;
  prompt?: string;
  reason?: string;
  interval_seconds?: number;
  next_at?: string;
  cron?: string;
  ticks?: number;
}

/** A command or subagent running in the background until it reports back. */
export interface BackgroundTask {
  id: string;
  kind: string;
  description?: string;
  command?: string;
  output_file?: string;
  started_at?: string;
  /** A Monitor: when it stops by itself. */
  expires_at?: string;
  /** A workflow: its run in Session.workflows. */
  run_id?: string;
}

/** A Claude Code workflow run (the Workflow tool, proto.Workflow; docs/protocol/protocol.md "workflows"). */
export interface Workflow {
  task_id: string;
  run_id?: string;
  name?: string;
  summary?: string;
  /** running | completed | failed | killed | stalled; anything else reads as completed. */
  status: string;
  running: boolean;
  phases?: string[];
  phase?: string;
  /** Phase's 1-based place in phases; 0 or absent: not known. */
  phase_index?: number;
  phase_count?: number;
  /** Per-phase counts over all its agents (not only the 60 listed), in `phases` order then undeclared ones; title ""
   *  is agents whose phase is not known. Older hubs leave it out. */
  phase_stats?: WorkflowPhaseStats[];
  /** The Workflow call that launched it: its card goes in the chat in that call's place (#3434). */
  tool_use_id?: string;
  agents_started?: number;
  agents_done?: number;
  agents_running?: number;
  /** Agents that failed (counted apart from done); hubs before #5511 count them as done and leave this out. */
  agents_failed?: number;
  /** Sums over all its agents: each agent's latest usage (not cumulative spend), and its tool calls. */
  tokens?: number;
  calls?: number;
  started_at?: string;
  ended_at?: string;
  updated_at?: string;
  error?: string;
  agents?: WorkflowAgent[];
}

/** One phase's counts (proto.WorkflowPhaseStats); `failed` only from hubs with #5511. */
export interface WorkflowPhaseStats {
  title: string;
  started: number;
  done: number;
  running: number;
  failed?: number;
}

/** One agent of a workflow run; its conversation reads like a subagent's (session_id "<parent>@<id>"). */
export interface WorkflowAgent {
  id: string;
  session_id: string;
  label?: string;
  phase?: string;
  running: boolean;
  /** running | done | stopped (it had not come back when the run ended) | failed (#5511); older hubs leave it out. */
  status?: string;
  started_at?: string;
  ended_at?: string;
  updated_at?: string;
  last_text?: string;
  model?: string;
  /** Its latest tool call, kept after its last words. */
  latest_tool?: { name: string; summary?: string };
  tokens?: number;
  calls?: number;
}

/** What a background task's notice says (proto.TaskNote). */
export interface TaskNote {
  kind: string;
  status: string;
  title?: string;
  task_id?: string;
  tool_use_id?: string;
  exit_code?: number;
  output_file?: string;
  detail?: string;
}

/** A picture folded into a run of work (proto.Fold.images): the turn-image strip shows it without opening the fold. */
export interface FoldImage {
  /** The image item: its bytes from /sessions/{mid}/{sid}/items/{item_id}/image. Missing: only a path (file/thumb). */
  item_id?: string;
  tool_use_id: string;
  tool: string;
  source: "read" | "screenshot" | "browser" | "mcp" | "tool";
  path?: string;
  machine?: string;
  mime?: string;
  size?: number;
  width?: number;
  height?: number;
}

export interface Item {
  id: string;
  seq: number;
  ts?: string;
  role: "user" | "assistant" | "system";
  kind: "text" | "thinking" | "tool_use" | "tool_result" | "notice" | "image" | "fold" | "recap";
  text?: string;
  tool_use_id?: string;
  tool_name?: string;
  tool_input?: Record<string, unknown>;
  is_error?: boolean;
  full_size?: number;
  queued?: boolean;
  wake?: boolean;
  task?: TaskNote;
  fold?: { items: number; tools: number; thoughts: number; until?: string; through_seq: number; through_id?: string; images?: FoldImage[]; tasks?: FoldTaskSnapshot[]; latest_thinking?: Item; supervision_user?: boolean };
  image?: { mime: string; data?: string; lazy?: boolean; size?: number; width?: number; height?: number };
  /** Sent by another Claude session (cross-session messaging), not typed by you: its name. */
  from?: string;
  /** The client_msg_id of the message this came from, as the hub tied it (every item of it: the text, its images, a
   *  queued copy, or the notice it became); older hubs never send it (lib/echo.ts). */
  client_msg_id?: string;
  /** This notice is where the context was compacted (Claude Code's compact_boundary). */
  compact?: CompactInfo;
  /** A notice for a Claude Code model_refusal_* record (#2289), kept as recorded. */
  model_refusal?: ModelRefusal;
  /** The model that wrote this assistant item, as its transcript records it (proto.Item.Model, #5034); absent when the
   *  transcript does not say and from older nodes. Drawn only as a divider where the replies' model changes. */
  model?: string;
  /** A message Persona typed in for the model (a task's notice, a scheduled run…), not by you (lib/injected.ts). */
  injected?: Injected;
  /** Display provenance, independent of the model-facing role. Unknown kinds remain non-user sources. */
  source?: MessageSource;
  /** Client-only promotion evidence transferred from a removed Fold.supervision_user during an overlapping merge. */
  supervisionUser?: boolean;
}

/** A Claude Code model_refusal_* record (server proto.ModelRefusal) as the hub gives it: its known fields, values as
 *  recorded (unknown ones included), and `text`, the refusal's own words. Never the record's line (#2528). */
export interface ModelRefusal {
  subtype: string;
  original_model?: string;
  fallback_model?: string;
  category?: string;
  scope?: string;
  explanation?: string;
  text?: string;
}

/** A pending "back to the configured model" (server proto.ModelRestore): "next_start" — the idle headless process was
 *  closed (or none ran), so the next start asks for `to`. */
export interface ModelRestore {
  state: "next_start" | (string & {});
  to?: string;
  at: string;
}

/** Message provenance supplied by the assistant, not a group-chat member or a model role. */
export interface MessageSource {
  kind: "conversation" | "patrol" | "followup" | "supervision" | (string & {});
  conversation_id?: string;
  title?: string;
}

/** What a message Persona wrote for the model is (docs/protocol/protocol.md "Messages injected by Persona"). */
export interface Injected {
  kind: "task_notice" | "followup" | "system" | (string & {});
  /** task_notice: done | needs | interrupted | failed; followup: once | daily | interval | wake; system: lost_tasks | supervise */
  state?: string;
  /** Supervision without a level is legacy triage; other injections remain informational. */
  level?: "info" | "triage" | "user";
  title?: string;
  machine?: string;
  todo_id?: string;
  /** The one line to show. */
  line: string;
  /** followup once | daily | interval: what the task asks. */
  detail?: string;
}

export interface ItemsPage {
  items: Item[];
  has_more?: boolean;
  more_after?: boolean;
  more?: boolean;
  history?: string;
  to_seq?: number;
  from_seq?: number;
}

export interface Question {
  question: string;
  header?: string;
  multi_select?: boolean;
  /** Missing or null for a question asked in free text (Codex request_user_input; a Go nil slice, #1573). */
  options?: { label: string; description?: string }[] | null;
}

export interface DialogOption {
  key: string;
  label: string;
  detail?: string;
  kind?: string;
  /** Ticked at the computer (a multi-select box), or an answered question's pick. */
  checked?: boolean;
  /** What is typed into the free-text field (kind "type_something"). */
  text?: string;
}

/** A terminal dialog as the hub reads it. A multi-select question (multi) is sent whole by its submit row ("Submit",
 *  or "Next" when another question follows); steps are the questions of its tab bar, review the tab that submits them. */
export interface ScreenDialog {
  header?: string;
  question?: string;
  detail?: string;
  options?: DialogOption[];
  rev?: string;
  multi?: boolean;
  submit?: string;
  steps?: { label: string; done: boolean }[];
  review?: boolean;
  incomplete?: boolean;
}

/** What an MCP server asks for: a form (`requested_schema`, a JSON Schema subset) or a link to open (`url`). */
export interface Elicitation {
  mode: "form" | "url" | (string & {});
  message?: string;
  requested_schema?: Record<string, unknown>;
  url?: string;
  elicitation_id?: string;
}

/** Session.children: the sessions folded under one, counted by their own state (docs/protocol/protocol.md "Session hierarchy"). */
export interface SessionChildren {
  total: number;
  working?: number;
  needs_you?: number;
}

export interface Interaction {
  id: string;
  /** The manager it belongs to (docs/protocol/protocol.md "Per-manager routing"); none: the default one. */
  manager?: string;
  machine_id: string;
  session_id: string;
  session_title?: string;
  cwd?: string;
  kind: "question" | "plan" | "permission" | "screen" | "elicitation";
  created_at?: string;
  tool_name?: string;
  questions?: Question[];
  plan?: string;
  tool_input?: Record<string, unknown>;
  summary?: string;
  screen?: string;
  secret?: boolean;
  dialog?: ScreenDialog;
  /** Who asks when it is not Claude Code: "codex", "traex", or an agent runtime's agent ("dsh"; docs/integrations/runtimes.md). */
  agent?: string;
  /** Display label supplied by the source (for example a script request). */
  agent_label?: string;
  /** What "always allow" allows here; another agent's card offers it only with this. */
  always_label?: string;
  /** Permission scopes remembered by the hub for this conversation. */
  always_keys?: string[];
  /** Whether answering "Always allow" is kept, as the manager says (#3590): false hides the choice, true offers it;
   *  absent (an older hub, an Agents session's card) the card's own rule (offersAlways). */
  always_honored?: boolean;
  /** The tool by its name for people, from the manager: its own tools ("iPhone control" for iphone_do) and Persona's
   *  MCP tools on a node's card ("Report to supervisor · Done", #4966); absent on other cards. */
  tool_title?: string;
  /** The tool alone where tool_title also says what this call does ("Report to supervisor"): what Always covers. */
  tool_name_title?: string;
  /** Why another agent asks for the permission, in its own words (a Codex and compatible variants approval read off the terminal). */
  reason?: string;
  /** An MCP form or link (kind elicitation, docs/protocol/protocol.md "MCP elicitation"): the server, where it is configured, and
   *  the subagent that asks, when the hook says. */
  mcp_server_name?: string;
  mcp_config_source?: string;
  agent_id?: string;
  agent_type?: string;
  /** The session the card's session folds under (Session.folded_under, #5120): the card's home is that session's page. */
  folded_under?: string;
  elicitation?: Elicitation;
  /** What a permission card needs filled in before it is approved (a password): such a card is never approved in one tap. */
  fields?: { key: string; label: string; secret?: boolean }[];
  /** The assistant is deciding whether it may answer this question for the user (docs/assistant/assistant.md "Assistant
   *  answers questions first"): nothing was pushed yet, and an answer given here wins. The same card comes again without it. */
  delegating?: boolean;
  /** What the assistant filled in when it left the question to the user. */
  delegate?: Delegate;
}

/** The assistant's context on a question it left to the user (Interaction.delegate). */
export interface Delegate {
  what?: string;
  why_now?: string;
  /** The answer it recommends: an option's label when the question has options. */
  recommend?: string;
  /** What not taking the recommendation costs. */
  cost?: string;
  /** Why the user has to decide. */
  reason?: string;
  deadline?: string;
}

export interface Launcher {
  name: string;
  /** A Persona-managed provider's name for people, any language (#5029); absent for an rc alias or from an older hub.
   *  Show it through lib/provider-names.ts. */
  display_name?: string;
  model?: string;
  /** What it starts when not Claude Code ("codex"); agent_type: what the hub's machine config says it runs. */
  agent?: string;
  agent_type?: string;
  /** A Codex launcher the computer starts with no first message too; absent from an older computer, whose Codex
   *  starts need one. */
  prompt_optional?: boolean;
  /** How its last check or start went on the computer the list is for (GET /machines/{mid}/launchers,
   *  docs/server/launcher-health.md); absent: never checked, a failure too old to count, or an older hub. */
  health?: LauncherHealth;
}

/** A launcher's health record on one computer (#1643). */
export interface LauncherHealth {
  /** ok | failing | repairing | blocked */
  state: string;
  /** The failure's class: not_in_terminal, auth, model_route, cli_outdated, timeout, other. */
  kind?: string;
  /** The redacted raw text of the failure. */
  error?: string;
  checked_at?: string;
}

export interface AssistantInfo {
  machine_id: string;
  main_machine_id?: string;
  launcher?: string;
  cwd?: string;
  main?: string;
  timezone?: string;
  notes?: string;
  notes_effective?: string;
  /** The side conversations the user made leaders (docs/assistant/unified-entry.md "Leader conversations"): absent
   *  when there are none or from an older hub. The sidebar shows them first among the Persona chats, as pinned. */
  leaders?: string[];
  /** "Fold background rounds" in effect (#4314, docs/assistant/assistant.md "Background rounds"); absent from an older hub, which means on. */
  main_background_folded?: boolean;
  /** The background supervisor's conversation (#4927, lib/background-supervisor.ts): its session id once the hub made
   *  it; absent before that and from an older hub. Read-only. */
  background_supervisor?: string;
  conversations: Session[];
}

export interface MetricPoint {
  t: number;
  cpu?: number;
  load?: number;
  mem?: number;
  gap?: boolean;
}

export interface MachineMetrics {
  machine_id: string;
  online: boolean;
  system?: Machine["system"];
  latest?: { t: number; cpu?: number; load?: number; mem_used?: number; mem_total?: number; net_rx?: number; net_tx?: number; disk_used?: number; disk_total?: number };
  points: MetricPoint[];
  /** Its tokens (docs/server/machines.md "Token usage"): absent from an older hub, or when its node never reported usage. */
  usage?: UsageCard;
}

/** What a working Claude's status line says right now (hub `live` push, feature `session_live`; docs/protocol/protocol.md
 *  "Live status"): read off its pane about once a second while someone watches. Absent fields: the line did not say. */
export interface SessionLive {
  /** The spinner's verb without the ellipsis ("Frosting"). */
  verb?: string;
  /** Seconds this turn has run, as the line counts them. */
  elapsed_s?: number;
  /** Output tokens so far this turn (the line's "↓ 22.8k tokens"). */
  tokens_down?: number;
  /** It is thinking now ("thinking with … effort"). */
  thinking?: boolean;
  /** It just thought for this many seconds ("thought for 3s"). */
  thought_s?: number;
  /** When the node read it (RFC 3339). */
  at?: string;
}

/** The hub's `live` push: `live` null or absent means the turn is over (or it is no longer read). */
export interface LiveEvent {
  type: "live";
  machine_id: string;
  session_id: string;
  live?: SessionLive | null;
}
