// One compact execution summary per exchange on every conversation style (#2602).
// The latest assistant text occupies one replace-in-place slot while running; the final text stays there when done.
// Older text and every tool/thinking step remain available inside the manually opened summary: opened, it is cindy's
// timeline (#5155, messageWorkGroups.ts createCompletedWorkGroup): the narration in order, each run of work between two
// pieces of it an inner summary of its own. Compaction boundaries and unloaded history gaps retain their behavior.

import { t } from "../i18n";
import { isChildSessionStep } from "./child-cards";
import type { Step } from "./turns";
import type { Item } from "./types";

export type ToolStep = Extract<Step, { kind: "tool" }>;

export type AgentStatus = "running" | "completed" | "failed" | "stopped";

/** A group of steps (`children` hold steps, or in a timeline the narration and inner groups), or a step left in the open. */
export type WorkNode = WorkGroupNode | { kind: "step"; step: Step };

export interface WorkGroupNode {
  kind: "group";
  /** Stable across narration updates and the live → done transition (`work-…`). */
  id: string;
  /** Its first step's seq: a group inside a fold the reader opened starts open. */
  seq?: number;
  children: WorkNode[];
  /** The run still going at the end of a live exchange (in a timeline: its last run, with no words after it). */
  live: boolean;
  durationMs?: number;
  /** Where its clock starts (ms): the live timer counts from here. */
  startedAt?: number;
}

interface WorkGroupOptions {
  /** The agent is at work on this exchange: its tail stays live. */
  live: boolean;
  /** When the exchange began (your message): the first group's clock starts there. */
  turnStart?: string;
  /** Live text currently replaces the persisted latest text slot. */
  streaming?: boolean;
  /** Stable conversational exchange identity, independent of which text/tool/fold is currently first. */
  identity?: string;
}

/** History that was never loaded between two steps: further apart than this, they do not share a group. */
const HISTORY_GAP_MS = 30 * 60 * 1000;

const AGENT_TOOLS = new Set(["Task", "Agent"]);
const PLAN_TOOLS = new Set(["TodoWrite"]);

export const isAgentStep = (s: Step): boolean => s.kind === "tool" && AGENT_TOOLS.has(s.use.tool_name ?? "");
export const isPlanStep = (s: Step): boolean => s.kind === "tool" && PLAN_TOOLS.has(s.use.tool_name ?? "");
/** Claude Code's Workflow call (#3434): its run's card where the hub lists the run, else the row it always was. */
export const isWorkflowStep = (s: Step): s is ToolStep => s.kind === "tool" && s.use.tool_name === "Workflow";
/** A tool call drawn as an action row (not a subagent card, not a plan card, not a workflow's call). */
export const isRowStep = (s: Step): s is ToolStep => s.kind === "tool" && !isAgentStep(s) && !isPlanStep(s) && !isWorkflowStep(s);
const isCompactStep = (s: Step) => s.kind === "notice" && Boolean(s.item.compact);

/** The item a step stands for (a tool: its call). */
function stepItem(s: Step): Item {
  return s.kind === "tool" ? s.use : s.item;
}

export const tsMs = (ts?: string): number | null => {
  const t = Date.parse(ts ?? "");
  return Number.isFinite(t) ? t : null;
};

function stepStart(s: Step): number | null {
  return tsMs(stepItem(s).ts);
}

/** When a step was over: a tool at its result, folded work at its last step. */
function stepEnd(s: Step): number | null {
  const start = stepStart(s);
  let end: number | null = null;
  if (s.kind === "tool") end = tsMs(s.result?.ts);
  else if (s.kind === "fold") end = tsMs(s.item.fold?.until);
  if (start === null) return end;
  return end === null ? start : Math.max(start, end);
}

/** Your words and the agent's: the boundaries a group's clock runs between. */
const boundaryTs = (s: Step | undefined): number | null => (s && (s.kind === "text" || isCompactStep(s)) ? stepStart(s) : null);

const isAnswer = (s: Step) => s.kind === "text" && Boolean(s.item.text?.trim());

/** The step a group is known by: its first real one (cindy keys a group by its first activity, so a live group keeps
 *  its key as it grows and once it is done). A child session's card is not one: it comes in with the child's row, and
 *  the group keeps the key (and the reader's open state) it had without it. */
const anchorStep = (run: Step[]) => run.find((s) => s.kind !== "text" && !isChildSessionStep(s)) ?? run.find((s) => s.kind !== "text") ?? run[0];

/** A group's key: its anchor's seq and block (a fold's is the seq of the first step it stands for, block 0). */
function anchorKey(run: Step[]): string {
  const first = anchorStep(run);
  const it = stepItem(first);
  return `${it.seq}.${first.kind === "fold" ? 0 : (it.id.split("#")[1] ?? 0)}`;
}

function createGroup(run: Step[], next: Step | undefined, live: boolean, prevBoundary: number | null): WorkGroupNode {
  const first = anchorStep(run);
  const anchor = stepStart(first);
  // the previous boundary when it is not after the run's first step: a thought written at once carries the time it
  // was written, and the thinking before it belongs to the run
  const start = prevBoundary !== null && (anchor === null || prevBoundary <= anchor) ? prevBoundary : anchor;
  let end: number | null = next && boundaryTs(next) !== null ? boundaryTs(next) : null;
  if (end === null) {
    // parallel calls finish out of order: the latest end, not the last step's
    for (const s of run) {
      const e = stepEnd(s);
      if (e !== null) end = end === null ? e : Math.max(end, e);
    }
  }
  return {
    kind: "group",
    id: `work-${anchorKey(run)}`,
    seq: stepItem(first).seq,
    children: run.map((step) => ({ kind: "step", step })),
    live,
    durationMs: start !== null && end !== null && end >= start ? end - start : undefined,
    ...(start !== null ? { startedAt: start } : {}),
  };
}

/** cindy's createCompletedWorkGroup: a summary whose steps hold intermediate narration opens to that narration in order,
 *  each maximal run of other steps between two pieces of it an inner group (its clock from the narration before it, or
 *  the turn's start, to the narration after it, else its latest end). Null without narration: the steps stay direct.
 *  `work` is the summary's steps: the latest text (in the slot) is not among them, so it is no boundary. `answer`: that
 *  text when it came after the last run, which is then over and ends there; otherwise, while the turn is live, the last
 *  run is the one still going. */
function timeline(work: Step[], answer: Step | undefined, turnStart: number | null, live: boolean): WorkNode[] | null {
  if (!work.some(isAnswer)) return null;
  const out: WorkNode[] = [];
  let run: Step[] = [];
  let prev = turnStart;
  // a text without words is no boundary: it goes with the run around it, and alone it is drawn as before (as nothing)
  const flush = (next: Step | undefined, live: boolean) => {
    if (run.some((s) => s.kind !== "text")) out.push(createGroup(run, next, live, prev));
    else out.push(...run.map((step) => ({ kind: "step" as const, step })));
    run = [];
  };
  for (const s of work) {
    if (!isAnswer(s)) {
      run.push(s);
      continue;
    }
    flush(s, false);
    out.push({ kind: "step", step: s });
    prev = stepStart(s);
  }
  flush(answer, live && !answer);
  return out;
}

/** One summary plus one text slot, irrespective of tool kind, text length or Markdown shape.
 *  A streamed text segment supersedes the persisted text, but the persisted words remain inspectable in the summary. */
function groupTurn(items: Step[], turnStart: number | null, live: boolean, streaming: boolean, identity?: string): WorkNode[] {
  let latest = -1;
  if (!streaming) items.forEach((s, i) => isAnswer(s) && (latest = i));
  const work: Step[] = [];
  const kept: WorkNode[] = [];
  for (let i = 0; i < items.length; i++) {
    const s = items[i];
    if (i === latest) continue;
    if (!isCompactStep(s)) work.push(s);
    else kept.push({ kind: "step", step: s });
  }
  const answer = latest >= 0 ? items[latest] : undefined;
  const group = work.length ? createGroup(work, answer, live, turnStart) : undefined;
  if (group) {
    // the latest text ends the last run when nothing but words came after it
    const answered = answer && items.slice(latest + 1).every((s) => s.kind === "text") ? answer : undefined;
    const children = timeline(work, answered, turnStart, live);
    if (identity) group.id = `work-turn-${identity}`;
    if (children) {
      group.children = children;
      // an outer line without the exchange's key (a part before a compaction or a gap) is known by its first step, as
      // its first inner group is: that one gets a key of its own, so each keeps its own open / closed state
      for (const c of children) if (c.kind === "group" && c.id === group.id) c.id = c.id.replace(/^work-/, "work-run-");
    }
  }
  // The time runs to the latest end of every step of the exchange, its answer included (#5119): the span the live
  // clock was counting, so it carries on from what that showed (iOS ChatLines.workEnd counts the same). A text written
  // before the last tool result is still the newest text, not where the time stops.
  if (group && group.startedAt !== undefined) {
    const end = items.reduce((latest, s) => Math.max(latest, stepEnd(s) ?? latest), group.startedAt);
    group.durationMs = Math.max(0, end - group.startedAt);
  }
  return [...(group ? [group] : []), ...kept, ...(answer ? [{ kind: "step" as const, step: answer }] : [])];
}

/** A thought without words (Claude Code writes most of them so) says nothing: it is no step of the work, as in cindy
 *  (thinkingActivityForMessage), so a turn of nothing else has no summary and it never splits a run of rows (#5155). */
const isWordlessThought = (s: Step) => s.kind === "thinking" && !s.item.text?.trim();

/** One exchange's steps (the answer among them, in order) as what the chat draws. */
export function workBlocks(steps: Step[], opts: WorkGroupOptions): WorkNode[] {
  const out: WorkNode[] = [];
  let turn: Step[] = [];
  let turnStart = tsMs(opts.turnStart);
  let prevEnd = turnStart;
  const flush = (activeTail: boolean) => {
    if (!turn.length) return;
    out.push(...groupTurn(turn, turnStart, activeTail && opts.live, activeTail && opts.live && Boolean(opts.streaming), activeTail ? opts.identity : undefined));
    turn = [];
  };
  for (const s of steps) {
    if (isWordlessThought(s)) continue;
    if (isCompactStep(s)) {
      flush(false);
      out.push({ kind: "step", step: s });
      turnStart = stepStart(s);
      prevEnd = turnStart;
      continue;
    }
    const start = stepStart(s);
    if (prevEnd !== null && start !== null && start - prevEnd > HISTORY_GAP_MS) {
      flush(false);
      turnStart = null; // what came before the gap cannot start this part's clock
    }
    turn.push(s);
    const end = stepEnd(s);
    if (end !== null) prevEnd = prevEnd === null ? end : Math.max(prevEnd, end);
  }
  flush(true);
  return out;
}

/** A TodoWrite that can be its exchange's plan card: the tool took it (a refused one did not change the plan, #5602)
 *  and it lists two or more items (cindy draws no card for a list of one). The one rule for the card and the pill. */
export const isPlanList = (s: Step): s is ToolStep => isPlanStep(s) && !(s as ToolStep).result?.is_error && planTodos(s as ToolStep).length >= 2;

/** The step of an exchange that draws its plan card: its latest plan list, "" for none (cindy draws one card per plan,
 *  where its latest list was written). Its other plan updates stay in the summary as ordinary rows, each inspectable
 *  (super rule 2; cindy drops them). */
export function planCardStep(steps: Step[]): string {
  for (let i = steps.length - 1; i >= 0; i--) if (isPlanList(steps[i])) return steps[i].id;
  return "";
}

export interface Todo {
  content: string;
  status: "pending" | "in_progress" | "completed";
  activeForm?: string;
}

export function planTodos(s: ToolStep): Todo[] {
  const todos = (s.use.tool_input as { todos?: unknown } | undefined)?.todos;
  if (!Array.isArray(todos)) return [];
  return todos.flatMap((t) => {
    if (!t || typeof t !== "object") return [];
    const o = t as Record<string, unknown>;
    const content = typeof o.content === "string" ? o.content : typeof o.activeForm === "string" ? o.activeForm : "";
    const status = o.status === "completed" || o.status === "in_progress" ? o.status : "pending";
    return content ? [{ content, status, ...(typeof o.activeForm === "string" ? { activeForm: o.activeForm } : {}) }] : [];
  });
}

/** done / total, the current step (in progress, else the next pending, else the last), and its 1-based number. */
export function planProgress(todos: Todo[]): { done: number; total: number; current: string; step: number; allDone: boolean } {
  const done = todos.filter((t) => t.status === "completed").length;
  const total = todos.length;
  const active = todos.findIndex((t) => t.status === "in_progress");
  const pending = todos.findIndex((t) => t.status === "pending");
  const idx = active >= 0 ? active : pending >= 0 ? pending : Math.max(0, total - 1);
  const allDone = total > 0 && done >= total;
  return { done, total, current: todos[idx]?.content ?? "", step: allDone ? total : idx + 1, allDone };
}

// ── what a group says and shows ──

type ExplorationKind = "read" | "search" | "list";

/** Which reading a tool call is (cindy's descriptor kinds): a file read, a search, a listing; anything else is not. */
function explorationOf(s: ToolStep): { kind: ExplorationKind; key?: string } | undefined {
  const name = s.use.tool_name ?? "";
  const inp = (s.use.tool_input ?? {}) as Record<string, unknown>;
  if (name === "Read") return { kind: "read", key: typeof inp.file_path === "string" ? inp.file_path : undefined };
  if (name === "Grep" || name === "Glob") return { kind: "search" };
  if (name === "LS") return { kind: "list" };
  return undefined;
}

// a workflow's call counts too: a group that launched one did more than read; so does a plan update (cindy's verb
// "updated" is no reading), drawn as a row or as the card
const directTools = (g: WorkGroupNode): ToolStep[] =>
  g.children.flatMap((c) => (c.kind === "step" && (isRowStep(c.step) || isWorkflowStep(c.step) || isPlanStep(c.step)) ? [c.step as ToolStep] : []));

/** "Read 3 files · searched 2 times" when a group did nothing but read and search (a file read twice counts once). */
export function explorationSummary(g: WorkGroupNode): string {
  const tools = directTools(g);
  if (!tools.length) return "";
  const reads = new Set<string>();
  let anon = 0;
  let search = 0;
  let list = 0;
  for (const t of tools) {
    const e = explorationOf(t);
    if (!e) return "";
    if (e.kind === "read") e.key ? reads.add(e.key) : anon++;
    else if (e.kind === "search") search++;
    else list++;
  }
  const read = reads.size + anon;
  return [read ? t("lib-work-groups.read_files", { count: read }) : "", search ? t("lib-work-groups.searches", { count: search }) : "", list ? t("lib-work-groups.listings", { count: list }) : ""].filter(Boolean).join(" · ");
}

/** The groups holding the tool call `call` (its tool_use_id), outer first, down to the one it sits in: what opens to
 *  bring it into view (a background task's "Jump to the step that started it", iOS ChatView.jumpToCall). Empty: it is in the open; null:
 *  not among these nodes. */
export function groupsHolding(nodes: WorkNode[], call: string): string[] | null {
  for (const n of nodes) {
    if (n.kind === "step") {
      if (n.step.kind === "tool" && n.step.use.tool_use_id === call) return [];
      continue;
    }
    const inner = groupsHolding(n.children, call);
    if (inner) return [n.id, ...inner];
  }
  return null;
}

/** Every nonempty summary can be inspected, even a single running tool call. */
export const groupOpens = (g: WorkGroupNode): boolean => g.children.length > 0;

/** Conversation identity is independent of the common collapsed execution default. */
export type WorkLineStyle = "agents" | "assistant";
export const workLineStyle = (assistantView?: boolean, assistantSession?: boolean): WorkLineStyle => (assistantView || assistantSession ? "assistant" : "agents");

export interface WorkLineState {
  style: WorkLineStyle;
  turnLive: boolean;
  remembered: boolean;
  liveOverride?: boolean;
}

/** A manual choice during execution lasts for the turn; completion restores its collapsed default on every style. */
export const workLineOpen = (s: WorkLineState): boolean => s.turnLive ? (s.liveOverride ?? false) : s.remembered;
export const togglesLiveOverride = (_style: WorkLineStyle, turnLive: boolean): boolean => turnLive;
export const workLineOpens = (g: WorkGroupNode, _style: WorkLineStyle, _turnLive: boolean): boolean => groupOpens(g);
/** Opening a summary must reveal every tool call: a timeline's inner groups start open with it (cindy folds them, which
 *  super rule 2 does not allow), each remembering its own choice after that. */
export const nestedLineDefault = (_style: WorkLineStyle): boolean => true;

/** cindy's formatSessionDuration: under an hour `6s`, `1m 5s`, `2m` (the same in every language's catalog; at least a
 *  second); from an hour the language's form, always with the minutes ("1h 0m", "1 小时 0 分", "1時間0分"; "1d 2h 5m"). */
export function workDuration(msValue: number): string {
  const total = Math.max(1, Math.round((Number.isFinite(msValue) ? msValue : 0) / 1000));
  if (total < 60) return t("lib-work-groups.dur_seconds", { s: total });
  if (total >= 3600) {
    const d = Math.floor(total / 86_400);
    const h = Math.floor((total % 86_400) / 3600);
    const m = Math.floor((total % 3600) / 60);
    return d > 0 ? t("lib-work-groups.dur_days_hours_minutes", { d, h, m }) : t("lib-work-groups.dur_hours_minutes", { h, m });
  }
  const m = Math.floor(total / 60);
  const s = total % 60;
  return s ? t("lib-work-groups.dur_minutes_seconds", { m, s }) : t("lib-work-groups.dur_minutes", { m });
}

/** A finished group's title: "Worked for 1m 5s" ("Work details" without its times), and what it read when that is all
 *  it did directly (an outer timeline's tools are in its inner groups, so it says only how long). */
export function groupTitle(g: WorkGroupNode): string {
  if (g.live) return t("lib-work-groups.working");
  const base = g.durationMs !== undefined ? t("lib-work-groups.worked_for", { duration: workDuration(g.durationMs) }) : t("lib-work-groups.work");
  const ex = explorationSummary(g);
  return ex ? `${base} · ${ex}` : base;
}
