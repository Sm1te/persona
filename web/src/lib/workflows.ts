// Background workflows (#2935, docs/protocol/agent-states.md "Background workflows and Monitor"): a Claude Code agent whose turn ended to
// wait for its Workflow run is still at work. What the rows, the chat's header, the task cards and the run's own card
// (#3434: one block per phase, a progress bar in parts) say about them.
// Newer hubs send Session.workflows; an older one only lists the run in background (kind "workflow"). Fields a hub may
// not have yet (phase_stats, tool_use_id, agents_failed…) are read when they are there; without them it says less.
import { t } from "../i18n";
import { backgroundWaiting, stateOf } from "./format";
import { took } from "./tasks";
import type { Session, Workflow, WorkflowAgent, WorkflowPhaseStats } from "./types";
import type { AgentStatus } from "./work-groups";

export type WorkflowStatus = "running" | "completed" | "failed" | "killed" | "stalled";

export function workflowStatus(w: Workflow): WorkflowStatus {
  if (w.running) return "running";
  return (["failed", "killed", "stalled"] as const).find((s) => s === w.status) ?? "completed";
}

/** Its runs still running. */
export function runningWorkflows(s: Session | undefined): Workflow[] {
  return (s?.workflows ?? []).filter((w) => w.running);
}

/** Its background tasks other than workflow runs (those have their own cards), newest hub or not. */
export function otherBackground(s: Session | undefined) {
  const runs = s?.workflows;
  // An older hub lists a run only in background: there it is a task like the others.
  return (s?.background ?? []).filter((b) => b.kind !== "workflow" || !runs);
}

/** Complete details count workflow runs once. Summary background_count is raw and may already include workflows,
 * so their separate scalar must not be added to it. */
export function backgroundCount(s: Session | undefined): number {
  if (typeof s?.waiting === "boolean") return s.background_count ?? 0;
  return otherBackground(s).length + runningWorkflows(s).length;
}

export function workflowName(w: Workflow): string {
  return w.name?.trim() || w.summary?.trim() || t("lib-workflows.untitled");
}

/** "Verify 2/2", "Verify", "" */
export function workflowPhase(w: Workflow): string {
  if (!w.phase) return "";
  const n = w.phases?.length ?? 0;
  return w.phase_index && n ? `${w.phase} ${w.phase_index}/${n}` : w.phase;
}

/** How long it has run (or ran): "14 min". */
export function workflowElapsed(w: Workflow, now: number): string {
  const start = Date.parse(w.started_at ?? "");
  if (!Number.isFinite(start)) return "";
  const end = w.running ? now : Date.parse(w.ended_at ?? "") || now;
  return took(Math.max(0, Math.floor((end - start) / 1000)));
}

/** "169/185+" — the total is not known before it ends: done of those started so far, "+" while it runs (the script
 *  starts more as it goes); "169/185" once it has ended. */
export function workflowCounts(w: Workflow): string {
  return `${w.agents_done ?? 0}/${w.agents_started ?? 0}${w.running ? "+" : ""}`;
}

/** For a narrow row: what moves first, so truncation keeps it: 169/185+ · parity-audit · Verify (+1). */
export function workflowShortLine(s: Session | undefined): string {
  const runs = runningWorkflows(s);
  if (!runs.length) return (s?.running_workflows ?? 0) > 0 || (s?.background ?? []).some((b) => b.kind === "workflow") ? t("lib-workflows.running_in_background") : "";
  const w = runs[0];
  const line = [workflowCounts(w), workflowName(w), w.phase ?? ""].filter(Boolean).join(" · ");
  return runs.length > 1 ? `${line} +${runs.length - 1}` : line;
}

/** One line for a row or a card: Background workflow "parity-audit" · Verify 2/2 · 169/185+ · 14 min (waiting on 2). */
export function workflowLine(s: Session | undefined, now: number): string {
  const runs = runningWorkflows(s);
  if (!runs.length) {
    // an older hub: it only says a workflow runs
    return (s?.running_workflows ?? 0) > 0 || (s?.background ?? []).some((b) => b.kind === "workflow") ? t("lib-workflows.running_in_background") : "";
  }
  const w = runs[0];
  const parts = [t("lib-workflows.line_name", { name: workflowName(w) }), workflowPhase(w), workflowCounts(w), workflowElapsed(w, now)].filter(Boolean);
  const line = parts.join(" · ");
  return runs.length > 1 ? t("lib-workflows.line_more", { line, count: runs.length }) : line;
}

/** The workflow agent with this session id among s's runs, for the side panel's title. */
export function workflowAgentFor(s: Session | undefined, sid: string): { run: Workflow; agent: WorkflowAgent } | null {
  for (const run of s?.workflows ?? []) {
    const agent = run.agents?.find((a) => a.session_id === sid);
    if (agent) return { run, agent };
  }
  return null;
}

// ── the run's card (#3434) ──

/** Ended runs stay listed (and the chat's "Background" chip stays) this long after they ended: the node's workflowKeep. */
export const WORKFLOW_KEEP_MS = 24 * 60 * 60 * 1000;

const ms = (ts?: string): number | null => {
  const v = Date.parse(ts ?? "");
  return Number.isFinite(v) ? v : null;
};

/** When it ended, or its last sign of life when the hub does not say. */
function endedAt(w: Workflow): number | null {
  return ms(w.ended_at) ?? ms(w.updated_at) ?? ms(w.started_at);
}

/** Its runs still running, and those that ended within the last day (the node lists them that long; an older one may
 *  list them longer): what the chat's "Background" chip and its dialog show. */
export function recentWorkflows(s: Session | undefined, now: number): Workflow[] {
  return (s?.workflows ?? []).filter((w) => {
    if (w.running) return true;
    const end = endedAt(w);
    return end === null || now - end <= WORKFLOW_KEEP_MS;
  });
}

/** What one of its agents is doing: running, back with its result, failed, or stopped when the run ended without it. */
export type AgentRunState = "running" | "done" | "failed" | "stopped";

export function agentRunState(a: WorkflowAgent): AgentRunState {
  if (a.running) return "running";
  if (a.status === "failed") return "failed";
  if (a.status === "stopped") return "stopped";
  return "done";
}

/** How long an agent has run (or ran): "2 min"; "" when the hub does not say when it started. */
export function agentElapsed(a: WorkflowAgent, now: number): string {
  const start = ms(a.started_at);
  if (start === null) return "";
  const end = a.running ? now : (ms(a.ended_at) ?? ms(a.updated_at));
  return end === null ? "" : took(Math.max(0, Math.floor((end - start) / 1000)));
}

/** Its model and latest tool, for a wide row: "claude-sonnet · Reading example.go". */
export function agentDetail(a: WorkflowAgent): string {
  const tool = a.latest_tool ? a.latest_tool.summary?.trim() || a.latest_tool.name : "";
  return [a.model?.trim() ?? "", tool].filter(Boolean).join(" · ");
}

export function agentLabel(a: WorkflowAgent): string {
  return a.label?.trim() || t("lib-workflows.agent");
}

/** Where a block stands: its agents all back (done), some running (current), none started yet (next), or the run
 *  ended with some of them not back (stopped). */
export type PhaseState = "done" | "current" | "next" | "stopped";

/** One phase of a run as its card draws it. */
export interface PhaseBlock {
  /** The phase as the script names it ("": the agents whose phase is not known). */
  title: string;
  started: number;
  done: number;
  running: number;
  failed: number;
  /** Its agents the hub listed (the newest 60 of the run), in the order they started. */
  agents: WorkflowAgent[];
  state: PhaseState;
  /** The run is in this phase now: its block is lit. */
  current: boolean;
  /** More may still start in it: its count reads "5/8+". */
  growing: boolean;
}

/** Counts from the agents listed, for a hub without phase_stats: declared phases first, then any other an agent names,
 *  then the agents without one. They undercount once a run has more agents than the hub lists. */
function statsFromAgents(w: Workflow, agents: WorkflowAgent[]): WorkflowPhaseStats[] {
  const titles = [...(w.phases ?? [])];
  for (const a of agents) if (!titles.includes(a.phase ?? "")) titles.push(a.phase ?? "");
  return titles.map((title) => {
    const in_ = agents.filter((a) => (a.phase ?? "") === title);
    const states = in_.map(agentRunState);
    return {
      title,
      started: in_.length,
      running: states.filter((s) => s === "running").length,
      done: states.filter((s) => s === "done").length,
      failed: states.filter((s) => s === "failed").length,
    };
  });
}

/** Its phases, one block each, in the script's order (then undeclared ones, then agents without a phase). */
export function workflowPhaseBlocks(w: Workflow): PhaseBlock[] {
  const agents = w.agents ?? [];
  const stats = w.phase_stats?.length ? w.phase_stats : statsFromAgents(w, agents);
  // a run that names no phases at all: one block of its agents, or nothing when it has none
  if (stats.length === 1 && !stats[0].title && !stats[0].started) return [];
  const titled = Boolean(w.phase) && stats.some((s) => s.title === w.phase);
  // where the run is now: the phase of its newest running agent, else the first block with an agent running
  const currentAt = titled ? stats.findIndex((s) => s.title === w.phase) : w.running ? stats.findIndex((s) => s.running > 0) : -1;
  return stats.map((st, i) => {
    const failed = st.failed ?? 0;
    const current = w.running && i === currentAt;
    let state: PhaseState;
    if (st.running > 0 || current) state = "current";
    // none started (as far as the hub says): passed when the run went on beyond it; the phase an ended run stopped in
    // (a hub that lists neither its agents nor phase_stats) did run, done or not
    else if (!st.started) state = currentAt >= 0 && i < currentAt ? "done" : i === currentAt && !w.running ? (workflowStatus(w) === "completed" ? "done" : "stopped") : "next";
    else if (st.done + failed >= st.started) state = "done";
    else state = w.running ? "done" : "stopped";
    return {
      title: st.title,
      started: st.started,
      done: st.done,
      running: w.running ? st.running : 0,
      failed,
      agents: agents.filter((a) => (a.phase ?? "") === st.title || (!st.title && !stats.some((x) => x.title === a.phase))),
      state,
      current,
      growing: w.running && (st.running > 0 || current),
    };
  });
}

/** A block's count: done of started, "+" while more may start in it ("5/8+"). */
export function phaseCount(b: PhaseBlock): string {
  return `${b.done}/${b.started}${b.growing ? "+" : ""}`;
}

/** A block's name ("" is the agents whose phase is not known). */
export function phaseTitle(b: PhaseBlock): string {
  return b.title || t("lib-workflows.phase_untitled");
}

/** At most this many agents are rows in a block; the quiet ones after them fold into "n more". */
export const PHASE_ROWS = 12;

/** How a block's rows stand: `rows` in start order; `folded` the listed ones not shown ("n more" opens them all), the
 *  first of them where `breakAt` rows have been shown; `unlisted` the block's earliest agents, which the hub no longer
 *  lists (it keeps the run's newest 60), said above its first row. */
export interface PhaseRows {
  rows: WorkflowAgent[];
  folded: number;
  breakAt: number;
  unlisted: number;
}

/**
 * The rows a block shows, in the order its agents started (never reordered, so nothing jumps as they finish), running
 * ones always. Normally its first PHASE_ROWS: the quiet tail folds. Once the hub's cap has cut into the block (it drops
 * the run's oldest agent each time one starts anywhere in the run), its last PHASE_ROWS instead: anchored to the end,
 * the rows stay put as the cap moves on, and all that is hidden comes before them.
 */
export function phaseRows(b: PhaseBlock, all = false, limit = PHASE_ROWS): PhaseRows {
  const unlisted = Math.max(0, b.started - b.agents.length);
  if (all) return { rows: b.agents, folded: 0, breakAt: b.agents.length, unlisted };
  const from = unlisted > 0 ? Math.max(0, b.agents.length - limit) : 0;
  const shown = (a: WorkflowAgent, i: number) => a.running || (i >= from && i < from + limit);
  const rows = b.agents.filter(shown);
  const firstHidden = b.agents.findIndex((a, i) => !shown(a, i));
  const breakAt = firstHidden < 0 ? rows.length : b.agents.slice(0, firstHidden).filter(shown).length;
  return { rows, folded: b.agents.length - rows.length, breakAt, unlisted };
}

/** "Verify: verify:chat-rich" while it runs: the phase it is in and its newest agent running there. */
export function workflowCurrent(w: Workflow): { phase: string; agent: string } | null {
  if (!w.running) return null;
  const agents = [...(w.agents ?? [])].reverse();
  const phase = w.phase ?? "";
  const lead = agents.find((a) => a.running && (!phase || a.phase === phase)) ?? agents.find((a) => a.running);
  const agent = lead ? agentLabel(lead) : "";
  return phase || agent ? { phase, agent } : null;
}

/** Its agents by how they stand, for the progress bar: those back, still running, failed, and stopped by its end. */
export interface WorkflowTally {
  started: number;
  done: number;
  running: number;
  failed: number;
  stopped: number;
}

export function workflowTally(w: Workflow): WorkflowTally {
  const started = Math.max(0, w.agents_started ?? 0);
  const done = Math.min(started, Math.max(0, w.agents_done ?? 0));
  const running = w.running ? Math.min(started - done, Math.max(0, w.agents_running ?? 0)) : 0;
  const failed = Math.min(started - done - running, Math.max(0, w.agents_failed ?? 0));
  // the ones not back when it ended (while it runs, the rest is not known yet: a hub before #5511 counts failures as done)
  const stopped = w.running ? 0 : started - done - running - failed;
  return { started, done, running, failed, stopped };
}

/** The bar's parts in order (done, running, failed, stopped): where each starts and how much of the bar it is (0…1). */
export function tallyParts(c: WorkflowTally): { key: "done" | "running" | "failed" | "stopped"; from: number; size: number }[] {
  const total = c.started || 1;
  let at = 0;
  return (["done", "running", "failed", "stopped"] as const).map((key) => {
    const size = c.started ? c[key] / total : 0;
    const part = { key, from: at, size };
    at += size;
    return part;
  });
}

// ── the run's card in the chat (#5030, after cindy's AgentTaskCard) ──

/** The task card's status word for a run: killed (stopped by hand) and stalled (no word from it for 30 minutes, its
 *  claude gone) both read Stopped; neither is known to have failed, and only a failure is red. */
export function workflowTaskStatus(w: Workflow): AgentStatus {
  const s = workflowStatus(w);
  return s === "killed" || s === "stalled" ? "stopped" : s;
}

/** How long an ended run ran (ms), to its end or its last sign of life; null while it runs or when the hub does not say. */
export function workflowDurationMs(w: Workflow): number | null {
  const start = ms(w.started_at);
  const end = w.running ? null : (ms(w.ended_at) ?? ms(w.updated_at));
  return start === null || end === null ? null : Math.max(0, end - start);
}

/** The card's progress line: its agents back (done or failed) of those started, and the phase it is in while it runs. */
export function workflowProgress(w: Workflow): { phase: string; done: number; total: number } | null {
  const c = workflowTally(w);
  if (!c.started) return null;
  return { phase: w.running ? (w.phase?.trim() ?? "") : "", done: c.done + c.failed, total: c.started };
}

/** One square of the card's strip: how an agent stands ("unknown": one the hub no longer lists that the counts do not
 *  account for), and its label when the hub lists it. */
export interface StripCell {
  state: AgentRunState | "unknown";
  label?: string;
}

/** At most this many squares in the strip; the rest is "+n". */
export const STRIP_CELLS = 40;

/**
 * The card's strip: one square per agent in the order they started, the first `max` of them, and how many more. The
 * hub lists only the run's newest agents: the earlier ones it leaves out are squares too, their states what the run's
 * counts leave over once the listed ones are counted (done first: the oldest are the likeliest back).
 */
export function workflowStrip(w: Workflow, max = STRIP_CELLS): { cells: StripCell[]; more: number } {
  const listed = w.agents ?? [];
  const tally = workflowTally(w);
  const total = Math.max(tally.started, listed.length);
  const cells: StripCell[] = [];
  let unlisted = total - listed.length;
  if (unlisted > 0) {
    const seen = { done: 0, running: 0, failed: 0, stopped: 0 };
    for (const a of listed) seen[agentRunState(a)]++;
    for (const s of ["done", "failed", "stopped", "running"] as const) {
      const n = Math.min(unlisted, Math.max(0, tally[s] - seen[s]));
      for (let i = 0; i < n && cells.length < max; i++) cells.push({ state: s });
      unlisted -= n;
    }
    for (let i = 0; i < unlisted && cells.length < max; i++) cells.push({ state: "unknown" });
  }
  for (const a of listed) {
    if (cells.length >= max) break;
    cells.push({ state: agentRunState(a), label: agentLabel(a) });
  }
  return { cells, more: total - cells.length };
}

/** The Workflow calls of a chat with a run the hub lists (#3434), by tool_use_id, with when each run started (ms, NaN when
 *  the hub does not say): those draw the run's card, inside the turn's folded work like every tool call (super rule 2). */
export interface WorkflowCalls {
  known: ReadonlySet<string>;
  startedAt: ReadonlyMap<string, number>;
}

export const NO_WORKFLOW_CALLS: WorkflowCalls = { known: new Set(), startedAt: new Map() };

/** What `workflowCalls` depends on: it is the same while this is (a run's progress, or its end, does not change it). */
export function workflowCallsKey(runs: Workflow[] | undefined): string {
  return (runs ?? []).filter((w) => w.tool_use_id).map((w) => `${w.tool_use_id}:${w.started_at ?? ""}`).join(",");
}

export function workflowCalls(runs: Workflow[] | undefined): WorkflowCalls {
  const known = new Set<string>();
  const startedAt = new Map<string, number>();
  for (const w of runs ?? []) {
    if (!w.tool_use_id) continue;
    known.add(w.tool_use_id);
    startedAt.set(w.tool_use_id, ms(w.started_at) ?? NaN);
  }
  return known.size ? { known, startedAt } : NO_WORKFLOW_CALLS;
}

/** What the running bar says once the turn is over and a workflow keeps the agent working (#3434): the run it waits for
 *  (the first running one, as the rows say) and where its clock starts. Other background work (commands, Monitors,
 *  subagents) leaves the bar as it was. */
export interface BackgroundLine {
  run: Workflow;
  /** Other runs running besides it. */
  more: number;
  /** The run's start. */
  since?: string;
  /** A hub from before background_working (#3127) running a workflow: background only when no turn of the agent's
   *  own shows (no status line, no live work line). */
  guessed: boolean;
}

/** A hub from #3127 on: it sends background_working (omitted when false), and with it each run's tool_use_id and each
 *  agent's status. */
function knowsBackgroundWorking(runs: Workflow[]): boolean {
  return runs.some((w) => w.tool_use_id || w.phase_count || w.agents?.some((a) => a.status));
}

export function backgroundLine(s: Session | undefined): BackgroundLine | null {
  if (!s) return null;
  const runs = runningWorkflows(s);
  if (!runs.length) return null;
  const guessed = s.background_working === undefined && stateOf(s) === "working" && !knowsBackgroundWorking(s.workflows ?? []);
  if (s.background_working !== true && !guessed) return null;
  const run = runs[0];
  return { run, more: runs.length - 1, ...(run.started_at ? { since: run.started_at } : {}), guessed };
}

/** What an agent waiting in the background waits on (#6045, Session.background_waiting): its first background
 *  command's description (or command, on one line), how many more, and since when it waits. Null otherwise. */
export interface WaitLine {
  what: string;
  more: number;
  since?: string;
}

export function backgroundWaitLine(s: Session | undefined): WaitLine | null {
  if (!s || !backgroundWaiting(s)) return null;
  const tasks = (s.background ?? []).filter((b) => b.kind === "shell" || b.kind === "monitor");
  const first = tasks[0];
  const what = (first?.description || first?.command || "").split("\n")[0]!.trim();
  return { what, more: Math.max(0, tasks.length - 1), ...(s.state_since ? { since: s.state_since } : {}) };
}

/** The hub says the agent's turn is over and a workflow it launched keeps it working: the chat draws that turn finished. */
export function waitingOnWorkflow(s: Session | undefined): boolean {
  return Boolean(s?.background_working) && runningWorkflows(s).length > 0;
}
