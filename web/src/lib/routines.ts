// The assistant's scheduled tasks (docs/assistant/assistant.md "Scheduled tasks", docs/protocol/protocol.md `/assistant/tasks`): the wire types,
// how a task is said in words (the iOS app's AssistantScheduleText), what the editor checks, and the body it saves.
// Pure functions of their arguments (`now` passed in), so they are tested without a DOM.
import { currentLanguage, formatDate, t } from "../i18n";
import { launcherTitle, type Named } from "./provider-names";

export type Repeat = "once" | "daily" | "interval" | "cron";

/** "Push when done": "" every run's answer is pushed, "asked" only what a run sends itself. */
export type Push = "" | "asked";

/** The hub feature that says it takes Task.push; without it the editor leaves the setting out. */
export const TASK_PUSH_FEATURE = "task_push";

/** The hub feature that says it runs cron tasks (repeat "cron", Task.cron, GET /assistant/tasks/cron). */
export const TASK_CRON_FEATURE = "task_cron";

/** A new cron task's expression: 09:00 on weekdays. */
export const DEFAULT_CRON = "0 9 * * 1-5";

/** The editor's quick picks for "Cron". */
export const CRON_EXAMPLES = [
  { cron: "*/30 9-18 * * 1-5", label: t("lib-routines.cron_example_work_hours") },
  { cron: "0 9 * * 1-5", label: t("lib-routines.cron_example_weekdays") },
  { cron: "0 10 1 * *", label: t("lib-routines.cron_example_monthly") },
];

/** GET /assistant/tasks/cron: the expression as the hub stores it, in words ("" when it has none), the user's time zone and the next runs. */
export interface CronPreview {
  cron: string;
  text: string;
  timezone: string;
  next_runs: string[] | null;
}

export interface Task {
  id: string;
  title: string;
  prompt: string;
  repeat: Repeat;
  /** once: when; interval: the next run. */
  at?: string;
  /** daily: "HH:MM" in the user's time zone. */
  time?: string;
  /** daily: 1 = Monday … 7 = Sunday; empty = every day. */
  weekdays?: number[] | null;
  every_minutes?: number;
  /** cron: "minute hour day-of-month month day-of-week" (or @daily…) in the user's time zone. */
  cron?: string;
  /** cron, from the hub: the expression in words in the app's language ("" when it has none) and its next runs. */
  cron_text?: string;
  next_runs?: string[] | null;
  enabled: boolean;
  machine_id?: string;
  /** A conversation runs go into ("Continue the original chat"); empty = the task's own one. */
  session_id?: string;
  launcher?: string;
  permission_mode?: string;
  effort?: string;
  /** "Push when done" (hubs with the task_push feature): "" every run's answer, "asked" only what a run sends with notify_user.
   *  An "asked" task always runs in its own conversation (docs/assistant/assistant.md "Push after running"). */
  push?: Push;
  /** Optional end time (RFC 3339). After it, a daily or interval task turns itself off. Ignored for once; omitted when unset. */
  until?: string;
  created_by?: string;
  created_at?: string;
  last_run?: string;
  last_session_id?: string;
  last_status?: string;
  last_error?: string;
  next_run?: string;
  run_machine_id?: string;
  run_session_id?: string;
  run_launcher?: string;
}

export interface Run {
  id: string;
  kind: string;
  ref_id: string;
  title: string;
  due: string;
  at: string;
  status: string;
  error?: string;
  manual?: boolean;
  machine_id?: string;
  session_id?: string;
}

/** Monday … Sunday, short (the editor's day buttons, "every Mon, Wed"). */
export const DAY_NAMES = t("lib-routines.day_names").split(",");
export const WORKDAYS = [1, 2, 3, 4, 5];
export const WEEKEND = [6, 7];
export const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/;
export const MIN_EVERY = 5;
export const MAX_EVERY = 7 * 24 * 60;
/** Quick picks for "Interval". */
export const INTERVALS = [15, 30, 60, 120, 360, 1440];

/** A time the hub sent, or null: empty, unparsable, or Go's zero time ("0001-01-01T00:00:00Z"). */
export function parseTime(iso?: string | null): Date | null {
  if (!iso) return null;
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return null;
  const d = new Date(t);
  return d.getUTCFullYear() < 2000 ? null : d;
}

/** Monday = 1 … Sunday = 7. */
function isoDay(d: Date): number {
  return ((d.getDay() + 6) % 7) + 1;
}

function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/** Calendar days from `now` to `d` (negative: before). */
function daysBetween(now: Date, d: Date): number {
  return Math.round((startOfDay(d) - startOfDay(now)) / 86_400_000);
}

/** 1…7, unique, sorted; all seven (or none) is []: every day. */
export function normalizeDays(days?: number[] | null): number[] {
  const s = [...new Set((days ?? []).filter((d) => Number.isInteger(d) && d >= 1 && d <= 7))].sort((a, b) => a - b);
  return s.length === 7 ? [] : s;
}

/** "Every day" / "Weekdays" / "Weekends" / "Every Mon, Wed, Fri". */
export function repeatsLabel(days?: number[] | null): string {
  const d = normalizeDays(days);
  if (d.length === 0) return t("lib-routines.every_day");
  if (d.join() === WORKDAYS.join()) return t("lib-routines.workdays");
  if (d.join() === WEEKEND.join()) return t("lib-routines.weekend");
  return t("lib-routines.weekly", { days: d.map((x) => DAY_NAMES[x - 1]).join(t("lib-routines.day_separator")) });
}

/** "09:00" → "9:00 AM", "21:30" → "9:30 PM", "00:15" → "12:15 AM" (Chinese names the part of the day). */
export function clockLabel(hhmm?: string): string {
  if (!hhmm || !HHMM.test(hhmm)) return hhmm ?? "";
  const [h, m] = hhmm.split(":").map(Number);
  // Chinese says the part of the day (small hours / early morning / morning…), finer than Intl's; the others read the clock their own way
  if (currentLanguage() !== "zh-Hans") return formatDate(new Date(2000, 0, 1, h, m), { hour: "numeric", minute: "2-digit" });
  const part = h < 5 ? t("lib-routines.part_small_hours") : h < 9 ? t("lib-routines.part_early_morning") : h < 12 ? t("lib-routines.part_morning") : h < 13 ? t("lib-routines.part_noon") : h < 18 ? t("lib-routines.part_afternoon") : t("lib-routines.part_evening");
  const h12 = h % 12 === 0 ? 12 : h % 12;
  return t("lib-routines.clock", { part, time: `${h12}:${String(m).padStart(2, "0")}` });
}

/** "Every 30 minutes" / "Every 2 hours" / "Every 1 hour 30 minutes" / "Every day" / "Every 2 days". */
export function everyLabel(minutes: number): string {
  if (minutes < 60) return t("lib-routines.every_minutes", { count: minutes });
  if (minutes % 1440 === 0) return minutes === 1440 ? t("lib-routines.every_day") : t("lib-routines.every_days", { count: minutes / 1440 });
  const h = Math.floor(minutes / 60), m = minutes % 60;
  return m ? t("lib-routines.every_hours_minutes", { hours: h, minutes: m }) : t("lib-routines.every_hours", { count: h });
}

/** "17:05", always 24-hour. */
export function hm(d: Date): string {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** The day part of a moment: "Today" / "Tomorrow" / "Yesterday" / "Wed" (within a week) / "Nov 2". */
export function dayLabel(d: Date, now: Date): string {
  const n = daysBetween(now, d);
  if (n === 0) return t("lib-routines.today");
  if (n === 1) return t("lib-routines.tomorrow");
  if (n === -1) return t("lib-routines.yesterday");
  if (Math.abs(n) < 7) return t("lib-routines.weekday", { day: DAY_NAMES[isoDay(d) - 1] });
  return formatDate(d, { month: "short", day: "numeric" });
}

/** "Today 15:30" / "Tomorrow 09:00" / "Wed 09:00" / "Nov 2 09:00". */
export function momentLabel(d: Date, now: Date): string {
  return `${dayLabel(d, now)} ${hm(d)}`;
}

/** A future moment, roughly: "Right away" / "in 25 minutes" / "in 2 hours" / "in 1 hour 30 minutes" / "in 3 days"; "Past due" in the past. */
export function untilLabel(d: Date, now: Date): string {
  const s = (d.getTime() - now.getTime()) / 1000;
  if (s < 60) return s < -60 ? t("lib-routines.past_due") : t("lib-routines.right_away");
  if (s < 3600) return t("lib-routines.in_minutes", { count: Math.floor(s / 60) });
  if (s < 86400) {
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
    return h < 3 && m >= 10 ? t("lib-routines.in_hours_minutes", { hours: h, minutes: m }) : t("lib-routines.in_hours", { count: h });
  }
  return t("lib-routines.in_days", { count: Math.max(daysBetween(now, d), 1) });
}

/** Its title, or the first line of the prompt (a follow-up Persona scheduled without one). */
export function taskTitle(task: Pick<Task, "title" | "prompt">): string {
  if (task.title?.trim()) return task.title.trim();
  const first = (task.prompt ?? "").split("\n").find((l) => l.trim()) ?? "";
  return first.trim() || t("lib-routines.scheduled_follow_up");
}

/** The rule in words: "Every weekday 9:00 AM" / "Every 30 minutes" / "Once · New chat" / "0 10 1 * * · At 10:00, on day 1 of the month"
 *  (a cron task's expression first, so a one-line row always shows it). */
export function ruleText(task: Task): string {
  if (task.repeat === "interval") return everyLabel(task.every_minutes ?? 0);
  if (task.repeat === "cron") {
    const cron = task.cron ?? "";
    return task.cron_text ? t("lib-routines.rule_cron_text", { text: task.cron_text, cron }) : t("lib-routines.rule_cron", { cron });
  }
  if (task.repeat === "once") return task.session_id ? t("lib-routines.once_same_chat") : t("lib-routines.once_new_chat");
  const d = normalizeDays(task.weekdays);
  const days = d.length === 0 ? t("lib-routines.every_day") : d.join() === WORKDAYS.join() ? t("lib-routines.every_workday") : d.join() === WEEKEND.join() ? t("lib-routines.every_weekend") : repeatsLabel(d);
  return t("lib-routines.rule_daily", { days, time: clockLabel(task.time) });
}

/** When it runs next, or null when it is switched off or the hub has no time for it. */
export function nextRun(t: Task): Date | null {
  return t.enabled ? parseTime(t.next_run) ?? parseTime(t.at) : null;
}

/** "Next Today 09:00 · in 2 hours" / "Off" / "". */
export function nextText(task: Task, now: Date): string {
  if (!task.enabled) return t("lib-routines.off");
  const n = nextRun(task);
  return n ? t("lib-routines.next", { moment: momentLabel(n, now), until: untilLabel(n, now) }) : "";
}

/** The last run in a few words, null when it never ran: "Last Today 09:03 · Ran". */
export function lastRunText(task: Task, now: Date): { text: string; failed: boolean } | null {
  const at = parseTime(task.last_run);
  if (!at) return null;
  const word = task.last_status === "failed" ? t("lib-routines.status_failed") : task.last_status === "skipped" ? t("lib-routines.status_skipped") : t("lib-routines.status_ran");
  return { text: t("lib-routines.last", { moment: momentLabel(at, now), status: word }), failed: task.last_status === "failed" };
}

// ── what a run starts claude with ───────────────────────

export interface ModeOption {
  id: string;
  title: string;
  detail: string;
}

/** Besides "Default" (""): the hub's permission modes (proto.PermissionModes). */
export const PERMISSION_MODES: ModeOption[] = [
  // the same names as the chat's ⋯ menu (session-actions.ts)
  { id: "acceptEdits", title: t("lib-session-actions.mode_accept_edits"), detail: t("lib-session-actions.mode_accept_edits_detail") },
  { id: "plan", title: t("lib-session-actions.mode_plan"), detail: t("lib-session-actions.mode_plan_detail") },
  { id: "auto", title: t("lib-session-actions.mode_auto"), detail: t("lib-session-actions.mode_auto_detail") },
  { id: "bypassPermissions", title: t("lib-session-actions.mode_bypass_permissions"), detail: t("lib-routines.bypass_detail") },
];

export const EFFORTS: ModeOption[] = [
  ...["low", "medium", "high", "xhigh", "max"].map((id) => ({ id, title: t(`lib-session-actions.effort_${id}`), detail: t(`lib-session-actions.effort_${id}_detail`) })),
];

export const BYPASS = "bypassPermissions";

/** "Auto-edit" for a known mode, the raw value otherwise; "" for the default. */
export function modeLabel(id?: string): string {
  if (!id || id === "default") return "";
  return PERMISSION_MODES.find((m) => m.id === id)?.title ?? id;
}

export function effortLabel(id?: string): string {
  if (!id) return "";
  return EFFORTS.find((e) => e.id === id)?.title ?? id;
}

/** What it runs with when that isn't the default, in a few words: "claude-fast · Auto · Low thinking"; `danger` for "Allow all".
 *  The launcher by its display name from `names` (#5029). */
export function runsWith(task: Pick<Task, "launcher" | "permission_mode" | "effort">, names?: readonly Named[]): { text: string; danger: boolean } | null {
  const parts = [task.launcher && launcherTitle(task.launcher, names), modeLabel(task.permission_mode), task.effort ? t("lib-routines.effort", { effort: effortLabel(task.effort) }) : ""].filter(Boolean);
  return parts.length ? { text: parts.join(" · "), danger: task.permission_mode === BYPASS } : null;
}

// ── the editor ──────────────────────────────────────────

/** An enabled daily or interval task whose end time is more than a minute ago (the same grace as a one-off's `at`).
 *  Empty is fine, a disabled task may keep a past end time, and a one-off is not checked. */
function pastUntil(d: Task, now: Date): string | null {
  if (!d.enabled || d.repeat === "once") return null;
  const at = parseTime(d.until);
  if (!at || at.getTime() >= now.getTime() - 60_000) return null;
  return t("lib-routines.error_until");
}

/** What the editor checks before saving (the hub checks the same and says why in Chinese): null = fine. */
export function validateTask(d: Task, now: Date): string | null {
  if (d.repeat !== "once" && !d.title.trim()) return t("lib-routines.error_title");
  if (!d.prompt.trim()) return t("lib-routines.error_prompt");
  switch (d.repeat) {
    case "daily":
      if (!HHMM.test(d.time ?? "")) return t("lib-routines.error_time");
      return pastUntil(d, now);
    case "interval": {
      const m = d.every_minutes ?? 0;
      if (!Number.isInteger(m) || m < MIN_EVERY || m > MAX_EVERY) return t("lib-routines.error_interval");
      return pastUntil(d, now);
    }
    case "cron":
      // the hub checks the expression itself (the editor shows its answer as you type)
      if (!(d.cron ?? "").trim()) return t("lib-routines.error_cron");
      return pastUntil(d, now);
    case "once": {
      const at = parseTime(d.at);
      if (!at) return t("lib-routines.error_at");
      if (d.enabled && at.getTime() < now.getTime() - 60_000) return t("lib-routines.error_past");
      return null;
    }
    default:
      return t("lib-routines.error_repeat");
  }
}

/** Whether two versions differ in anything the user edits (the hub's own fields and, outside its rule, the fields
 *  another rule uses don't count). */
export function sameTask(a: Task, b: Task): boolean {
  if (a.title.trim() !== b.title.trim() || a.prompt.trim() !== b.prompt.trim() || a.repeat !== b.repeat || a.enabled !== b.enabled) return false;
  if ((a.session_id ?? "") !== (b.session_id ?? "")) return false;
  if ((a.permission_mode ?? "") !== (b.permission_mode ?? "")) return false;
  if ((a.push ?? "") !== (b.push ?? "")) return false;
  if (a.repeat !== "once" && !sameInstant(a.until, b.until)) return false;
  switch (a.repeat) {
    case "once":
      return parseTime(a.at)?.getTime() === parseTime(b.at)?.getTime();
    case "daily":
      return a.time === b.time && normalizeDays(a.weekdays).join() === normalizeDays(b.weekdays).join();
    case "interval":
      return a.every_minutes === b.every_minutes;
    case "cron":
      return (a.cron ?? "").trim() === (b.cron ?? "").trim();
  }
}

/** The same instant, with both missing (empty, unparsable, or Go's zero time) counting as the same. */
function sameInstant(a?: string | null, b?: string | null): boolean {
  return (parseTime(a)?.getTime() ?? null) === (parseTime(b)?.getTime() ?? null);
}

/** The `until` value for a save, or `undefined` when the key stays out of the body.
 *  A new daily or interval task sends an ISO time only when one is set. An update leaves the key out when the instant
 *  is unchanged; clearing a repeating task sends null; switching to once clears a stored end time and otherwise omits it. */
function untilBody(d: Task, saved?: Task | null): string | null | undefined {
  const draft = parseTime(d.until);
  if (!saved) return d.repeat === "once" || !draft ? undefined : draft.toISOString();
  if (d.repeat === "once") return parseTime(saved.until) ? null : undefined;
  if (sameInstant(d.until, saved.until)) return undefined;
  return draft ? draft.toISOString() : null;
}

/** The JSON for POST (no `saved`) or PUT. Models follow Models › Main chat; tasks only configure permissions.
 *  `push` goes only when changed from `saved`. `session_id` "" makes runs use the task's own conversation; `at` is left out when there is none (Go can't read "").
 *  `until` follows untilBody: omit to keep, null to clear. */
export function taskBody(d: Task, saved?: Task | null): Record<string, unknown> {
  const body: Record<string, unknown> = {
    title: d.title.trim(),
    prompt: d.prompt.trim(),
    repeat: d.repeat,
    enabled: d.enabled,
    time: d.repeat === "daily" ? d.time ?? "" : "",
    weekdays: d.repeat === "daily" ? normalizeDays(d.weekdays) : [],
    every_minutes: d.repeat === "interval" ? d.every_minutes ?? 0 : 0,
    // always sent, so a hub that knows cron tasks changes the rule (left out, it keeps a cron task's); an older one ignores it
    cron: d.repeat === "cron" ? (d.cron ?? "").trim() : "",
    session_id: d.session_id ?? "",
    permission_mode: d.permission_mode ?? "",
  };
  const at = d.repeat === "daily" || d.repeat === "cron" ? null : parseTime(d.at);
  if (at) body.at = at.toISOString();
  const until = untilBody(d, saved);
  if (until !== undefined) body.until = until;
  // Only when changed: a hub keeps it when left out, and one that does not know it never gets it.
  if ((d.push ?? "") !== (saved?.push ?? "")) body.push = d.push ?? "";
  return body;
}

/** "Until Today 18:00" for a repeating task that has an end time; "" for a one-off or when none is set (the row renders nothing). */
export function untilText(task: Pick<Task, "repeat" | "until">, now: Date): string {
  if (task.repeat === "once") return "";
  const d = parseTime(task.until);
  return d ? t("assistant-routines.until", { when: momentLabel(d, now) }) : "";
}

/** The draft with "Push when done" set. "asked" runs in the task's own conversation: "Continue the original chat" goes (the hub clears it too). */
export function withPush(d: Task, push: Push): Task {
  return push === "asked" ? { ...d, push, session_id: undefined } : { ...d, push };
}

/** The next whole hour after `now` (a new one-off's time). */
export function nextHour(now: Date): Date {
  const d = new Date(now.getTime() + 3_600_000);
  d.setMinutes(0, 0, 0);
  return d;
}

/** A Date as an <input type="datetime-local"> value, in local time: "2026-11-02T15:30". */
export function toLocalInput(d: Date | null): string {
  if (!d) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** An <input type="datetime-local"> value back to a Date (local time); null when empty or malformed. */
export function fromLocalInput(s: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(s);
  if (!m) return null;
  const d = new Date(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** Switching the rule keeps the editor valid: "Once" gets the next whole hour when it has no future time, "Interval" 30 minutes
 *  when it has none, "Every day" 09:00 when it has no time, "Cron" 09:00 on weekdays when it has no expression. */
export function withRepeat(d: Task, r: Repeat, now: Date): Task {
  const t: Task = { ...d, repeat: r };
  if (r === "once" && (parseTime(t.at)?.getTime() ?? 0) < now.getTime()) t.at = nextHour(now).toISOString();
  if (r === "interval" && (t.every_minutes ?? 0) < MIN_EVERY) t.every_minutes = 30;
  if (r === "daily" && !HHMM.test(t.time ?? "")) t.time = "09:00";
  if (r === "cron" && !(t.cron ?? "").trim()) t.cron = DEFAULT_CRON;
  return t;
}

// ── new tasks ───────────────────────────────────────────

export interface Template {
  key: string;
  title: string;
  prompt: string;
  repeat: Repeat;
  time?: string;
  weekdays?: number[];
  every_minutes?: number;
}

/** The + menu's starting points (the iOS app's AssistantRoutineTemplate). */
export const TEMPLATES: Template[] = [
  {
    key: "meeting",
    title: t("lib-routines.template_meeting_title"),
    prompt:
      t("lib-routines.template_meeting_prompt"),
    repeat: "daily",
    time: "08:45",
    weekdays: WORKDAYS,
  },
  {
    key: "weekly",
    title: t("lib-routines.template_weekly_title"),
    prompt:
      t("lib-routines.template_weekly_prompt"),
    repeat: "daily",
    time: "17:00",
    weekdays: [5],
  },
  {
    key: "patrol",
    title: t("lib-routines.template_patrol_title"),
    prompt: t("lib-routines.template_patrol_prompt"),
    repeat: "interval",
    every_minutes: 30,
  },
  {
    key: "wrapup",
    title: t("lib-routines.template_wrapup_title"),
    prompt: t("lib-routines.template_wrapup_prompt"),
    repeat: "daily",
    time: "18:30",
    weekdays: WORKDAYS,
  },
];

/** A blank task: every workday at 09:00. */
export function blankTask(): Task {
  return { id: "", title: "", prompt: "", repeat: "daily", time: "09:00", weekdays: WORKDAYS, every_minutes: 30, enabled: true };
}

export function fromTemplate(t: Template): Task {
  return { ...blankTask(), title: t.title, prompt: t.prompt, repeat: t.repeat, time: t.time ?? "09:00", weekdays: t.weekdays ?? [], every_minutes: t.every_minutes ?? 30 };
}

// ── lists ───────────────────────────────────────────────

/** Every day / every N minutes in the hub's order; one-offs soonest first. */
export function splitTasks(tasks: Task[]): { repeating: Task[]; oneOff: Task[] } {
  const oneOff = tasks.filter((t) => t.repeat === "once");
  const time = (t: Task) => nextRun(t)?.getTime() ?? parseTime(t.at)?.getTime() ?? Number.MAX_SAFE_INTEGER;
  return { repeating: tasks.filter((t) => t.repeat !== "once"), oneOff: oneOff.sort((a, b) => time(a) - time(b)) };
}

export interface Upcoming {
  id: string;
  at: Date;
  title: string;
  detail: string;
  repeating: boolean;
}

/** What Persona will do on its own next, soonest first; what is switched off is not coming up. */
export function upcoming(tasks: Task[], limit = 3): Upcoming[] {
  const out: Upcoming[] = [];
  for (const task of tasks) {
    const at = nextRun(task);
    if (!at) continue;
    out.push({ id: task.id, at, title: taskTitle(task), repeating: task.repeat !== "once", detail: task.repeat === "once" ? (task.session_id ? t("lib-routines.follow_up_same_chat") : t("lib-routines.follow_up_new_chat")) : ruleText(task) });
  }
  return out.sort((a, b) => a.at.getTime() - b.at.getTime()).slice(0, limit);
}

// ── runs ────────────────────────────────────────────────

export function runStatusWord(r: Pick<Run, "status">): string {
  return r.status === "ok" ? t("lib-routines.status_ran") : r.status === "skipped" ? t("lib-routines.status_skipped") : t("lib-routines.status_failed");
}

/** "Run manually" / the error / "On time" / "21 seconds late" / "3 minutes late" / "2 hours late". */
export function runDetail(r: Run): string {
  if (r.manual) return t("lib-routines.run_manual");
  if (r.status !== "ok") return r.error || runStatusWord(r);
  const at = parseTime(r.at), due = parseTime(r.due);
  if (!at || !due) return runStatusWord(r);
  const late = (at.getTime() - due.getTime()) / 1000;
  if (late < 5) return t("lib-routines.run_on_time");
  if (late < 120) return t("lib-routines.late_seconds", { count: Math.round(late) });
  if (late < 7200) return t("lib-routines.late_minutes", { count: Math.floor(late / 60) });
  return t("lib-routines.late_hours", { count: Math.floor(late / 3600) });
}

/** Newest first, one group per day ("Today", "Yesterday", "Wed", "Nov 2"). */
export function runDays(runs: Run[], now: Date): { day: string; runs: Run[] }[] {
  const out: { day: string; runs: Run[] }[] = [];
  const sorted = runs
    .map((r) => ({ r, at: parseTime(r.at) }))
    .sort((a, b) => (b.at?.getTime() ?? 0) - (a.at?.getTime() ?? 0));
  for (const { r, at } of sorted) {
    const day = at ? dayLabel(at, now) : t("lib-routines.earlier");
    if (out.length && out[out.length - 1].day === day) out[out.length - 1].runs.push(r);
    else out.push({ day, runs: [r] });
  }
  return out;
}

/** The conversations a task's runs went into (#2824), one entry each, at most `n`, with when a run last went into
 *  it: its own conversation first when it has one, then the runs' (newest first, as the hub lists them). The
 *  conversation it came from ("Continue the original chat") is the user's own and stays out, and so is any the caller knows is the
 *  user's (`mine`: the main one, one the hub says the user opened), since `session_id` may have been cleared since
 *  runs went into it. */
export function taskConversations(task: Task, runs: readonly Run[], n = 3, mine: (sid: string) => boolean = () => false): { sid: string; at?: string }[] {
  const out: { sid: string; at?: string }[] = [];
  const add = (sid?: string, at?: string) => {
    if (!sid || sid === task.session_id || (sid !== task.run_session_id && mine(sid))) return;
    const had = out.find((c) => c.sid === sid);
    if (!had) out.push({ sid, at });
    else if (!had.at) had.at = at;
  };
  add(task.run_session_id);
  for (const r of runs) if (r.ref_id === task.id) add(r.session_id, r.at);
  if (out[0] && !out[0].at && out[0].sid === task.last_session_id) out[0].at = task.last_run;
  return out.slice(0, n);
}
