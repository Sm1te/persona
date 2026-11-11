// Background work in a chat (iOS TaskIndex / TaskNoteText / NoticeRow.wakeSummary / ActivityChips): which call started
// a background task, what its notices say in words, and how a /loop and the session's background tasks read.

import { formatDate, t } from "../i18n";
import type { BackgroundTask, Item, LoopInfo, Subagent, TaskNote } from "./types";
import type { Step } from "./turns";
import type { AgentStatus } from "./work-groups";
import { launchedTaskID } from "./task-association";
export { attachedAgentNotices } from "./task-association";

/** The id a nested subagent's card stands under among an exchange's steps (`withNestedSubagents`). */
const NESTED = "subagent:";

/** Background tasks and the calls that started them. A notice names its call by tool_use_id, or only by the task id
 *  the call's result announced ("running in background with ID: …", "Monitor started (task …"). */
export interface TaskIndex {
  /** A call's notices, oldest first, by the call's tool_use_id. */
  notes: Map<string, TaskNote[]>;
  /** Every associated subagent report item, oldest first; never replace an earlier report. */
  reports: Map<string, Item[]>;
  /** The call a notice came from (notice item id → tool_use_id). */
  calls: Map<string, string>;
  /** Calls whose result says they went to the background (they may not have reported back yet). */
  started: Set<string>;
  /** What each of those calls was: its description, and when it started. */
  descriptions: Map<string, string>;
  startedAt: Map<string, string>;
}

export function emptyTaskIndex(): TaskIndex {
  return { notes: new Map(), reports: new Map(), calls: new Map(), started: new Set(), descriptions: new Map(), startedAt: new Map() };
}

export function buildTaskIndex(items: Item[]): TaskIndex {
  const x = emptyTaskIndex();
  const byTask = new Map<string, string>();
  const uses = new Map<string, Item>();
  const describe = (call: string) => {
    const use = uses.get(call);
    if (!use) return;
    const d = (use.tool_input as Record<string, unknown> | undefined)?.description;
    if (typeof d === "string" && !x.descriptions.has(call)) x.descriptions.set(call, d);
    if (use.ts && !x.startedAt.has(call)) x.startedAt.set(call, use.ts);
  };
  for (const it of items) {
    if (it.kind === "tool_use" && it.tool_use_id) uses.set(it.tool_use_id, it);
    if (it.kind === "tool_result" && it.tool_use_id) {
      const id = launchedTaskID(it.text ?? "");
      if (id) {
        byTask.set(id, it.tool_use_id);
        x.started.add(it.tool_use_id);
      }
    }
  }
  for (const it of items) {
    if (it.kind !== "tool_result" && it.kind !== "notice") continue;
    const n = it.task;
    const call = it.tool_use_id || n?.tool_use_id || (n?.task_id ? byTask.get(n.task_id) : undefined);
    if (!call) continue;
    const name = uses.get(call)?.tool_name;
    const agent = n?.kind === "agent" || name === "Agent" || name === "Task";
    if (n || agent || x.started.has(call)) describe(call);
    if (n) {
      x.notes.set(call, [...(x.notes.get(call) ?? []), n]);
      x.calls.set(it.id, call);
      if (n.status === "running") x.started.add(call);
    }
    if (agent && !isLaunchReceipt(it) && (it.kind === "tool_result" ? it.text?.trim() : n?.detail?.trim())) {
      x.reports.set(call, [...(x.reports.get(call) ?? []), it]);
    }
  }
  return x;
}

export function taskFailed(n: TaskNote): boolean {
  return n.status === "failed" || (n.exit_code ?? 0) !== 0;
}

/** "Background command" / "Monitor event" / "Monitor ended" / "Subagent". */
export function taskLabel(n: TaskNote): string {
  switch (n.kind) {
    case "shell":
      return t("lib-tasks.kind_shell");
    case "monitor":
      return n.status === "event" ? t("lib-tasks.monitor_event") : t("lib-tasks.monitor_ended");
    case "agent":
      return t("lib-tasks.kind_agent");
  }
  return t("lib-tasks.kind_task");
}

/** The call's description when we know the call, else what the notice says (a Monitor event: the event's first line). */
export function taskSubject(n: TaskNote, description?: string): string {
  if (n.kind === "monitor" && n.status === "event" && n.detail) {
    const line = firstLine(n.detail);
    if (line) return line;
  }
  return description || n.title || "";
}

/** How long it ran, short: "40s", "5m", "2h 5m". */
export function took(s: number): string {
  if (s < 60) return t("lib-tasks.took_seconds", { count: s });
  if (s < 3600) return t("lib-tasks.took_minutes", { count: Math.floor(s / 60) });
  const m = Math.floor((s % 3600) / 60);
  return m === 0 ? t("lib-tasks.took_hours", { count: Math.floor(s / 3600) }) : t("lib-tasks.took_hours_minutes", { hours: Math.floor(s / 3600), minutes: m });
}

export type Tone = "green" | "red" | "neutral" | "orange";

/** The outcome as a status pill (done green, failed red, stopped plain, waiting amber), with how long it ran. */
export function taskPill(n: TaskNote, startedAt?: string, at?: string): { text: string; tone: Tone } | null {
  let ran = "";
  const s = Date.parse(startedAt ?? ""), e = Date.parse(at ?? "");
  if (n.status !== "event" && Number.isFinite(s) && Number.isFinite(e) && e > s) ran = took(Math.floor((e - s) / 1000));
  const withRan = (w: string) => (ran ? `${w} · ${ran}` : w);
  switch (n.status) {
    case "completed":
    case "ended":
      return { text: withRan(t("lib-tasks.state_done")), tone: "green" };
    case "failed":
      return { text: withRan(n.exit_code !== undefined ? t("lib-tasks.exit_code", { code: n.exit_code }) : t("lib-tasks.state_failed")), tone: "red" };
    case "killed":
    case "stopped":
      return { text: withRan(t("lib-tasks.state_stopped")), tone: "neutral" };
    case "waiting":
      return { text: t("lib-tasks.state_waiting"), tone: "orange" };
    case "event":
      return { text: t("lib-tasks.state_event"), tone: "neutral" };
  }
  if (n.exit_code !== undefined && n.exit_code !== 0) return { text: withRan(t("lib-tasks.exit_code", { code: n.exit_code })), tone: "red" };
  return null;
}

/** The badge on the call that started background work: "Background · running" / "Background · done" / "Background · 3 events". */
export function taskBadge(notes: TaskNote[]): { text: string; failed: boolean } {
  const events = notes.filter((n) => n.status === "event").length;
  const last = [...notes].reverse().find((n) => n.status !== "event");
  if (!last || last.status === "running" || last.kind === "agent" && !["completed", "failed", "killed", "stopped", "ended", "waiting"].includes(last.status ?? "")) return { text: events > 0 ? t("lib-tasks.badge_events", { count: events }) : t("lib-tasks.badge_running"), failed: false };
  const state = last.status === "failed" ? t("lib-tasks.state_failed") : last.status === "killed" || last.status === "stopped" ? t("lib-tasks.state_stopped") : last.status === "waiting" ? t("lib-tasks.state_waiting") : last.status === "ended" ? t("lib-tasks.state_ended") : t("lib-tasks.state_done");
  return { text: events > 0 ? t("lib-tasks.badge_state_events", { state, count: events }) : t("lib-tasks.badge_state", { state }), failed: taskFailed(last) };
}

/** A wake notice in one line: the hub's text when it read the notification (`task`), else worked out from an older
 *  hub's summary: `Background command "Release" completed (exit code 0)` → "Background command done · Release". */
export function wakeSummary(item: Item): { text: string; failed: boolean } {
  if (item.task) return { text: item.text || item.task.title || "", failed: taskFailed(item.task) };
  const s = item.text ?? "";
  const m = /^(.*?)\s*"(.+)"\s*(.*)$/.exec(s);
  if (!m) return { text: s, failed: false };
  const kind = m[1] === "Background command" ? t("lib-tasks.wake_kind_shell") : m[1] === "Agent" ? t("lib-tasks.wake_kind_agent") : m[1] === "Monitor" ? t("lib-tasks.wake_kind_monitor") : t("lib-tasks.wake_kind_task");
  const rest = m[3].toLowerCase();
  const code = /exit code (\d+)/.exec(rest);
  const failed = rest.includes("fail") || rest.includes("error") || (code ? Number(code[1]) !== 0 : false);
  const state = failed ? t("lib-tasks.wake_failed") : rest.includes("kill") || rest.includes("stop") ? t("lib-tasks.wake_stopped") : rest.includes("complet") ? t("lib-tasks.wake_completed") : t("lib-tasks.wake_updated");
  return { text: t("lib-tasks.wake_summary", { kind, state, subject: m[2] }), failed };
}

function firstLine(s: string): string {
  return s.split("\n").map((l) => l.trim()).find(Boolean) ?? "";
}

// ── the session's /loop and background tasks ──

/** The task, without the "/loop" command word. */
export function loopTask(loop: LoopInfo): string {
  const p = (loop.prompt ?? "").trim();
  return p.startsWith("/loop") ? p.slice(5).trim() : p;
}

/** "Every 30 minutes" / "On schedule 33 17 * * *". */
export function loopCadence(loop: LoopInfo): string {
  if (loop.kind === "cron" && loop.cron) return t("lib-tasks.cadence_cron", { cron: loop.cron });
  const s = loop.interval_seconds ?? 0;
  if (s <= 0) return "";
  if (s < 120) return t("lib-tasks.every_seconds", { count: s });
  if (s < 7200) return t("lib-tasks.every_minutes", { count: Math.floor(s / 60) });
  return t("lib-tasks.every_hours", { count: Math.floor(s / 3600) });
}

function hhmm(ms: number): string {
  return formatDate(ms, { hour: "2-digit", minute: "2-digit", hour12: false });
}

/** The loop beside its arrows, short: "Next 11:57", or that it is running. */
export function loopShortLabel(loop: LoopInfo, now: number): string {
  const next = Date.parse(loop.next_at ?? "");
  if (Number.isFinite(next) && next > now) return t("lib-tasks.loop_next", { time: hhmm(next) });
  return loop.next_at ? t("lib-tasks.loop_running_short") : t("lib-tasks.loop_short");
}

/** "Next 11:57" / "Loop running" / "Every 30 minutes". */
export function loopLabel(loop: LoopInfo, now: number): string {
  const next = Date.parse(loop.next_at ?? "");
  if (Number.isFinite(next) && next > now) return t("lib-tasks.loop_next", { time: hhmm(next) });
  if (loop.next_at) return t("lib-tasks.loop_running");
  return loopCadence(loop) || t("lib-tasks.loop_looping");
}

/** "Next wake-up 11:57 (in 4 minutes)" / "Running this round". */
export function loopNext(loop: LoopInfo, now: number): string {
  const next = Date.parse(loop.next_at ?? "");
  if (!Number.isFinite(next)) return "";
  if (next <= now) return t("lib-tasks.loop_this_round");
  const m = Math.max(1, Math.ceil((next - now) / 60_000));
  const until =
    m < 60
      ? t("lib-tasks.until_minutes", { count: m })
      : m % 60
        ? t("lib-tasks.until_hours_minutes", { hours: Math.floor(m / 60), minutes: m % 60 })
        : t("lib-tasks.until_hours", { count: m / 60 });
  return t("lib-tasks.next_wake", { time: hhmm(next), until });
}

/** What tells the agent to end its /loop: nothing outside Claude can cancel a scheduled wakeup or its cron job. */
export const STOP_LOOP_MESSAGE =
  // i18n-ignore: an instruction to the agent, not interface text
  "停止当前的 /loop：调用 ScheduleWakeup(stop: true) 结束循环（如果用 CronCreate 建过定时任务，用 CronDelete 删掉），不要再安排下一次唤醒，回复一句「循环已停止」即可。";

/** A background task in one line: the description Claude gave, else the command. */
export function backgroundTitle(task: BackgroundTask): string {
  if (task.description) return task.description;
  return task.command?.split("\n")[0] || (task.kind === "agent" ? t("lib-tasks.background_agent") : t("lib-tasks.kind_shell"));
}

/** "40s" / "5m 12s" / "1h 5m". */
export function elapsed(fromIso: string | undefined, now: number): string {
  const start = Date.parse(fromIso ?? "");
  if (!Number.isFinite(start)) return "";
  const s = Math.max(0, Math.floor((now - start) / 1000));
  if (s < 60) return t("lib-tasks.elapsed_seconds", { count: s });
  if (s < 3600) return t("lib-tasks.elapsed_minutes_seconds", { minutes: Math.floor(s / 60), seconds: s % 60 });
  return t("lib-tasks.elapsed_hours_minutes", { hours: Math.floor(s / 3600), minutes: Math.floor((s % 3600) / 60) });
}

export function subagentTitle(a: Subagent): string {
  return a.description || a.type || t("lib-tasks.kind_agent");
}

// ── a subagent call in the chat (its card, cindy's AgentTaskCard) ──

/** What an Agent / Task call returned when it only started the subagent in the background. */
const LAUNCH_RECEIPT = /^\s*(?:Async agent launched|This is an internal ID for your use)/;
/** The "agentId: …" line Claude Code appends to a subagent's report. */
const AGENT_ID_LINE = /^\s*agentId:.*$/gm;

/** The subagent an Agent / Task call launched, by the call's tool_use_id (a nested subagent's card: `subagentCall`). */
export function subagentFor(all: Subagent[], use: Item): Subagent | undefined {
  return use.tool_use_id ? all.find((a) => subagentCall(a) === use.tool_use_id) : undefined;
}

export function isLaunchReceipt(result?: Item): boolean {
  if (result?.task?.kind === "agent" && (result.task.detail || agentNoteStatus(result.task) !== "running")) return false;
  return LAUNCH_RECEIPT.test(result?.text ?? "");
}

/** How a subagent call is going, by the call's result first (#5602: the session's list used to win over a result
 *  already in, and its `running` comes and goes, so a finished card flipped back to running): an error failed, a
 *  result done; none yet runs while the exchange is live or the list says it does, and stopped otherwise. A
 *  background subagent (its result is the launch receipt) is the list's to tell; without it, it runs while the
 *  exchange is live. */
/** Structured subagent lifecycle is authoritative; a running report is not a completed tool call. */
function agentNoteStatus(note: TaskNote | undefined): AgentStatus | undefined {
  if (note?.kind !== "agent") return undefined;
  if (taskFailed(note)) return "failed";
  switch (note.status) {
    case "running":
    case "waiting":
    case "event": return "running";
    case "completed":
    case "ended": return "completed";
    case "killed":
    case "stopped": return "stopped";
  }
  return "running";
}

export function agentStatus(result: Item | undefined, meta: Subagent | undefined, live: boolean, notes?: TaskNote[]): AgentStatus {
  if (result?.is_error) return "failed";
  const status = agentNoteStatus(notes?.at(-1)) ?? agentNoteStatus(result?.task);
  if (status) return status;
  if (!result) return live || meta?.running ? "running" : "stopped";
  if (!isLaunchReceipt(result)) return "completed";
  if (meta) return meta.running ? "running" : "completed";
  return live ? "running" : "completed";
}

/** Calls with a terminal result or notification. Launch receipts and interim reports leave work visible. */
export function terminalCalls(items: Item[], index = buildTaskIndex(items)): Set<string> {
  const answered = new Set<string>();
  for (const it of items) {
    const call = it.tool_use_id || index.calls.get(it.id);
    if (!call) continue;
    const status = agentNoteStatus(it.task);
    if (status === "running") answered.delete(call);
    else if (status || it.kind === "tool_result" && !isLaunchReceipt(it)) answered.add(call);
  }
  return answered;
}

/** When a finished subagent ended (ms): at its result, but a background one's result is the launch receipt, written as
 *  it started, so at its last line (#5602: its card said it took no time). */
export function subagentEnded(result: Item | undefined, meta: Subagent | undefined): number | undefined {
  const at = (ts?: string) => {
    const v = Date.parse(ts ?? "");
    return Number.isFinite(v) ? v : undefined;
  };
  return isLaunchReceipt(result) ? (at(meta?.updated_at) ?? at(result?.ts)) : (at(result?.ts) ?? at(meta?.updated_at));
}

const sameTool = (x: Subagent["last_tool"], y: Subagent["last_tool"]) => x === y || (x?.id === y?.id && x?.name === y?.name && x?.summary === y?.summary);

/** The subagents the main agent is waiting on (#5602, Claude Desktop's "2 agents working — …"): running ones it
 *  launched in the foreground whose calls have no result yet (`answered`: the call ids that do), and what they are
 *  doing, newest call first. Null when there are none. */
export function agentsWorking(all: Subagent[] | undefined, answered: ReadonlySet<string>): { count: number; doing: string[] } | null {
  const waiting = (all ?? []).filter((a) => a.running && !a.background && a.tool_use_id && !answered.has(a.tool_use_id));
  if (!waiting.length) return null;
  const doing = waiting
    .filter((a) => a.last_tool)
    .sort((a, b) => (Date.parse(b.updated_at ?? "") || 0) - (Date.parse(a.updated_at ?? "") || 0))
    .map((a) => a.last_tool!.summary?.trim() || a.last_tool!.name);
  return { count: waiting.length, doing: [...new Set(doing)] };
}

/** What of a subagent its card and list show: everything but `updated_at`, which moves with every line it writes. */
function subagentShown(a: Subagent): Omit<Subagent, "updated_at"> {
  const { updated_at: _, ...shown } = a;
  return shown;
}

/** `next`, or `prev` when nothing a card shows changed (#5602: the session list's subagents are a new array on every
 *  push, about once a second while one runs, and every exchange of the chat drew again). A subagent that stopped
 *  changed `running`, so the array that says so carries its last `updated_at`. */
export function stableSubagents(prev: Subagent[] | undefined, next: Subagent[] | undefined): Subagent[] | undefined {
  if (!prev || !next || prev.length !== next.length) return next;
  for (let i = 0; i < next.length; i++) {
    const a = subagentShown(prev[i]);
    const b = subagentShown(next[i]);
    const keys = Object.keys(b) as (keyof typeof b)[];
    const same = (k: keyof typeof b) => (k === "last_tool" ? sameTool(a.last_tool, b.last_tool) : a[k] === b[k]);
    if (keys.length !== Object.keys(a).length || !keys.every(same)) return next;
  }
  return prev;
}

/** What the subagent reported, without its agentId line; a launch receipt says nothing yet (its latest words do). */
export function agentSummary(result: Item | undefined, meta: Subagent | undefined): string {
  const text = isLaunchReceipt(result) ? "" : (result?.text ?? "").replace(AGENT_ID_LINE, "").trim();
  return text || meta?.last_text?.trim() || "";
}

/** A subagent's model as its card's chip says it (#5030): a Claude id as its family and version ("claude-haiku-4-5-20251001"
 *  → "Haiku 4.5", "claude-opus-4-8[1m]" → "Opus 4.8"), a GPT id as OpenAI writes it ("gpt-5.4-mini" → "GPT-5.4 Mini"),
 *  anything else in title case. A gateway's "<provider>/<model>" and a cloud route's "anthropic." prefix are dropped. */
export function modelChipLabel(model: string | undefined): string {
  let id = (model ?? "").trim().split("/").pop() ?? "";
  id = id.replace(/\[1m\]$/i, "").replace(/-\d{8}$/, "").replace(/^(?:[a-z]{2}\.)?anthropic\./i, "");
  if (!id) return "";
  const word = (w: string) => w.charAt(0).toUpperCase() + w.slice(1).toLowerCase();
  const claude = /^claude-([a-z]+)-(\d+)(?:-(\d+))?$/i.exec(id);
  if (claude) return `${word(claude[1])} ${claude[3] ? `${claude[2]}.${claude[3]}` : claude[2]}`;
  const gpt = /^gpt-([\d.]+)(?:-(mini|nano))?$/i.exec(id);
  if (gpt) return `GPT-${gpt[1]}${gpt[2] ? ` ${word(gpt[2])}` : ""}`;
  return id.split("-").filter(Boolean).map(word).join(" ");
}

/** A card for a subagent another subagent launched: its call is in that subagent's transcript, not in the chat's. */
export const isNestedSubagentStep = (s: Step): boolean => s.kind === "tool" && s.use.id.startsWith(NESTED);

/** How a nested subagent is going: only the session's list knows (no call result of the chat's says it). */
export const nestedSubagentStatus = (a: Subagent | undefined): AgentStatus => (a?.running ? "running" : "completed");

/** An exchange's steps with the subagents its subagents launched (and theirs), each a card of its own (#5030, cindy:
 *  no tree), among the steps by when it started: after the last step that started before it, never before the call
 *  of the subagent it descends from. A launcher naming itself or a loop of them adds nothing. */
export function withNestedSubagents(steps: Step[], all: Subagent[]): Step[] {
  if (!all.some((a) => a.parent_id)) return steps;
  const kids = new Map<string, Subagent[]>();
  for (const a of all) if (a.parent_id && a.parent_id !== a.id) kids.set(a.parent_id, [...(kids.get(a.parent_id) ?? []), a]);
  const out = [...steps];
  const seen = new Set<string>();
  for (const s of steps) {
    if (s.kind !== "tool" || (s.use.tool_name !== "Agent" && s.use.tool_name !== "Task")) continue;
    const root = subagentFor(all, s.use);
    if (!root) continue;
    seen.add(root.id);
    const from = out.indexOf(s);
    const queue = [...(kids.get(root.id) ?? [])];
    for (let a = queue.shift(); a; a = queue.shift()) {
      if (seen.has(a.id)) continue;
      seen.add(a.id);
      queue.push(...(kids.get(a.id) ?? []));
      const at = Date.parse(a.started_at ?? "");
      let i = from + 1;
      while (i < out.length && (Date.parse(stepTs(out[i]) ?? "") || Infinity) <= at) i++;
      const before = out[i - 1];
      const seq = before.kind === "tool" ? before.use.seq : before.item.seq;
      const use: Item = { id: `${NESTED}${a.id}`, seq, ts: a.started_at, role: "assistant", kind: "tool_use", tool_name: "Agent", tool_use_id: subagentCall(a), tool_input: { description: a.description ?? "" } };
      out.splice(i, 0, { kind: "tool", id: use.id, use, images: [] });
    }
  }
  return out.length > steps.length ? out : steps;
}

/** What decides where `withNestedSubagents` puts its cards (who launched whom, when, and the call and words a card is
 *  drawn from); "" when no subagent was launched by another. */
export function nestingKey(all: Subagent[]): string {
  if (!all.some((a) => a.parent_id)) return "";
  return all.map((a) => [a.id, a.parent_id, a.tool_use_id, a.started_at, a.description].join("\u0000")).join("\u0001");
}

const stepTs = (s: Step) => (s.kind === "tool" ? s.use.ts : s.item.ts);
/** The key a subagent card's open / closed state is kept under: by its call, so a link to the subagent can open it. */
export const subagentCardKey = (scope: string, call: string) => `${scope}task:${call}`;

/** The call a subagent's card is known by (its key, `data-tool-use`): the call that launched it, or for a fork the node
 *  names no call of, one of its own. */
export const subagentCall = (a: Subagent): string => a.tool_use_id || `${NESTED}${a.id}`;
