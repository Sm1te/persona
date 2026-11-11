// The unified entry in the assistant's conversation (docs/assistant/unified-entry.md; iOS Views/Assistant/Tasks): the plan card,
// task cards that follow their agents, the hub's lines (answered, receipt, routed), the "Today" card, message cards about
// a task, quick results, the dock above the composer, the tasks' questions, the route chip, and a task's detail in the
// side panel.
import { lazy, memo, Suspense, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { taskMeter, type TaskCardMetadata } from "../../lib/task-cards";
import { assistantEntries, needsYouBySupervisor, type NeedsYouItem } from "../../lib/assistant-entries";
import { useRestoredTodos } from "../../lib/todo-restore";
import {
  answeredItems,
  cardFiled,
  cardSent,
  dayLabel,
  dismissPlan,
  field,
  feedNotice,
  getFeedState,
  receiptState,
  interruptLook,
  type InterruptBatch,
  type InterruptResolution,
  isToday,
  sentByPersona,
  planEditsToSend,
  planUltracodeToSend,
  removeTask,
  retryKey,
  retryTask,
  startFailure,
  setRoute,
  startPlan,
  todayFailedRow,
  useFeed,
  type FeedItem,
  type PlanTask,
  type QuickResult,
  type TaskPlan,
  type TodayBody,
} from "../../lib/feed";
import { backgroundWorkLine, type BackgroundWork } from "../../lib/background-work";
import { runningWorkflows, workflowCounts, workflowLine } from "../../lib/workflows";
import { dockAgents, type DockAgent, interruption, isRemovable, mostUrgentAgent, openAsk, orphanedLine, runningSubagents, stoppedReason, taskNow, todoStatus, TODO_STATUS_INFO, waitingLine, type SessionLookup, type Todo, type TodoStatus } from "../../lib/todos";
import { TodoAskLine } from "../TodoAsk";
import { usageLimitContinues, usageLimitLabel } from "../../lib/usage-limit";
import { parseAPIError } from "../../lib/api-errors";
import { openPanel } from "../../lib/panel";
import { navigate } from "../../lib/router";
import { plainMarkdown } from "../../lib/plain-markdown";
import { Markdown } from "../../ui/Markdown";
import { LiveCard } from "../../ui/AssistantCard";
import type { AssistantView } from "../../lib/extension-wire";
import { getState, hubHas, taskSessionLookup, useStore, type State } from "../../lib/store";
import type { AgentState, Interaction, Machine, Session } from "../../lib/types";
import { Button, Collapse, cx, IconButton, Loading, Menu, toast, useNow } from "../../ui/atoms";
import { StatusMark } from "../../ui/StatusMark";
import { useClamped } from "../../ui/use-clamped";
import { Icon, type IconName } from "../../ui/icons";
import { ReportLine, ReviewBadge, ReviewLine } from "./Supervision";
import { AcceptRow, BriefCard } from "./Brief";

// A plan task's brief (#3822) comes with the first plan card that has one, as the decision row does.
const PlanBrief = lazy(() => import("./TaskBrief").then((m) => ({ default: m.PlanBrief })));

// A decision's row comes with the tray's first decision, not with the first screen (startup-preload's budget).
const TaskDecision = lazy(() => import("./TaskDecision").then((m) => ({ default: m.TaskDecision })));
import { SentLine, TaskSentList } from "./Sent";
import { latestReview, parkedLine, progressLine } from "../../lib/supervision";
import { planLabel, planLine, planShown } from "../../lib/plan";
import { AnsweredCard, InteractionCard } from "../chat/Interactions";
import { MessageActions } from "../chat/MessageActions";
import { sessionTitle, stateOf } from "../../lib/format";
import { formatDate, t } from "../../i18n";
import { interactionAsk } from "../../lib/inbox";
import { answeredLookup, answeredSessions } from "./answered-lookup";
import { lazyPage } from "../../lib/lazy-page";
const PlanEditDialog = lazyPage(() => import("./PlanEdit").then((m) => m.PlanEditDialog));
// "All" opens it: its code and its lib/task-board load then, not with the first screen (#4656)
const TaskBoard = lazyPage(() => import("./TaskBoard").then((m) => m.TaskBoard));
import { conversationTitle } from "./conversations";
import { TaskWorkspaceEditor, TaskWorkspaceLine } from "./TaskWorkspace";
import type { TaskWorkspace } from "../../lib/feed";
import { machineStartBlocked, todoUltracode, ULTRACODE_FEATURE } from "../../lib/ultracode";
import { UltraMark, UltracodeChip } from "../chat/Ultracode";
import { byTime } from "../../lib/times";
import { stateName } from "../../lib/state-labels";

// ── what a task is now ──

export function useTaskLookup() {
  const sessions = useStore((s) => s.sessions);
  const sessionSummaries = useStore((s) => s.sessionSummaries);
  const taskRows = useStore((s) => s.taskRows);
  const interactions = useStore((s) => s.interactions);
  const answered = useStore((s) => s.answered);
  return useMemo(() => answeredLookup(taskSessionLookup({ sessions, sessionSummaries, taskRows }), interactions, answered),
    [sessions, sessionSummaries, taskRows, interactions, answered]);
}

// The cards in the conversation do not take the whole session list, the pending cards or the machines: their store
// selectors work out what each one draws (its status, lead agent, question, place), so a push about another agent
// gives back the same strings and objects (the sessions delta keeps an unchanged session's object) and useStore's
// shallow compare skips the render. A push about its own agent, card or machine still draws it again.

/** What a task is now, from the store: its status and its lead agent. */
function taskNowIn(s: State, todo: Todo, plans: Record<string, TaskPlan>): { status: TodoStatus; lead?: Session } {
  const lookup = answeredLookup(taskSessionLookup(s), s.interactions, s.answered);
  return { status: statusOf(todo, lookup, plans), lead: mostUrgentAgent(todo, lookup)?.session };
}

/** A task's status (undefined without the task). */
function useTaskStatus(todo: Todo | undefined): TodoStatus | undefined {
  const plans = useFeed((s) => s.plans);
  return useStore((s) => (todo ? statusOf(todo, answeredLookup(taskSessionLookup(s), s.interactions, s.answered), plans) : undefined));
}

/** A task's status and lead agent (undefined without the task). */
function useTaskNow(todo: Todo | undefined): { status: TodoStatus; lead?: Session } | undefined {
  const plans = useFeed((s) => s.plans);
  return useStore((s) => (todo ? taskNowIn(s, todo, plans) : undefined));
}

/** Whether a task shows the Agent list row's ultracode mark (#12, lib/ultracode todoUltracode): its agents', else its
 *  plan task's before any agent started. */
function useTaskUltracode(todo: Todo | undefined): boolean {
  const plans = useFeed((s) => s.plans);
  const planned = todo?.plan_id ? plans[todo.plan_id]?.tasks.find((p) => p.todo_id === todo.id) : undefined;
  return useStore((s) => (todo ? todoUltracode(todo, answeredLookup(taskSessionLookup(s), s.interactions, s.answered), planned) : false));
}

/** A task's title on its card and its folded line, with the Agent list row's ultracode mark right after it while on
 *  (#12); without it, the title is drawn as before. */
function TaskTitle({ title, ultra, mark, className }: { title: string; ultra: boolean; mark: number; className: string }) {
  if (!ultra) return <span className={cx("min-w-0 flex-1 truncate", className)}>{title}</span>;
  return (
    <span className="flex min-w-0 flex-1 items-center gap-1.5">
      <span className={cx("min-w-0 truncate", className)}>{title}</span>
      <UltraMark size={mark} />
    </span>
  );
}

/** Two rows with the same fields, each the same value or object. */
function sameRow(a: object, b: object): boolean {
  const ka = Object.keys(a), kb = Object.keys(b);
  return ka.length === kb.length && ka.every((k) => Object.is((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
}

/** useStore for a list of rows a selector builds: useStore compares one level down only, and rows made anew always
 *  differ, so the last list stands while every row has the same fields as before. */
function useStoreRows<T extends object>(select: (s: State) => T[]): T[] {
  const last = useRef<T[] | null>(null);
  return useStore((s) => {
    const rows = select(s);
    const prev = last.current;
    if (prev && prev.length === rows.length && rows.every((r, i) => sameRow(r, prev[i]))) return prev;
    last.current = rows;
    return rows;
  });
}

export function useTodo(id: string | undefined): Todo | undefined {
  return useStore((s) => (id ? s.todos.find((t) => t.id === id) : undefined));
}

/** A task's status, failed too when a hub from before #1077 left it open and only its plan says the start failed. */
export function statusOf(todo: Todo, lookup: SessionLookup, plans: Record<string, TaskPlan>): TodoStatus {
  const st = todoStatus(todo, lookup);
  return st === "not_started" && startFailure(todo, plans) ? "failed" : st;
}

/** Removes a task (lib/feed removeTask), saying so when it did not go. */
export function removeTaskNow(todo: Todo) {
  removeTask(todo).then(
    (gone) => gone && toast(t("assistant-tasks.removed", { title: todo.title }), "ok"),
    (e) => toast(t("assistant-tasks.remove_failed", { error: String(e instanceof Error ? e.message : e) }), "error"),
  );
}

/** Right-click on a task's card, line or pill: "Remove task" when nothing of it runs (iOS: the long press). */
function useTaskMenu(todo: Todo | undefined, status: TodoStatus | undefined) {
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const can = Boolean(todo && status && isRemovable(status));
  return {
    onContextMenu: (e: React.MouseEvent<HTMLElement>) => {
      if (!can) return;
      e.preventDefault();
      setAnchor(e.currentTarget);
    },
    menu:
      anchor && todo ? (
        <Menu
          anchor={anchor}
          onClose={() => setAnchor(null)}
          items={[{ key: "remove", label: t("assistant-tasks.remove_ellipsis"), icon: "trash", danger: true, onSelect: () => removeTaskNow(todo) }]}
        />
      ) : null,
  };
}

/** The task's state as an agent state, for its dot and tone. */
function dotState(st: TodoStatus): AgentState {
  return st === "needs_you" || st === "interrupted" ? "needs_you" : st === "usage_limit" ? "idle" : st === "working" || st === "waiting" || st === "not_started" ? "working" : st === "unread" ? "unread" : st === "follow_up" ? "idle" : "offline"; // done, failed
}

const TONE: Record<string, string> = {
  red: "text-red",
  green: "text-green",
  amber: "text-orange",
  idle: "text-ink-2",
  off: "text-ink-3",
};

function TaskMark({ status, size = 9 }: { status: TodoStatus; size?: number }) {
  if (status === "usage_limit") return <Icon name="clock" size={size + 4} stroke={2.4} className="text-orange" />;
  if (status === "waiting") return <Icon name="clock" size={size + 4} stroke={2.4} className="text-ink-2" />;
  if (status === "failed" || status === "interrupted") return <Icon name="info" size={size + 4} stroke={2.4} className="text-red" />;
  if (status === "done" || status === "unread")
    return <Icon name="check" size={size + 4} stroke={2.4} className={status === "done" ? "text-ink-3" : "text-green"} />;
  return <StatusMark state={dotState(status)} />;
}

/** Where a task runs: machine · folder · agent. `machines` and `plans` default to the stores' own; a selector passes
 *  them in, so what it reads is what it depends on. */
function place(todo: Todo, lead?: Session, machines: Machine[] = getState().machines, plans: Record<string, TaskPlan> = getFeedState().plans): string {
  const a = (todo.agents ?? []).find((x) => !x.released_at) ?? todo.agents?.[0];
  const parts: string[] = [];
  const machineName = (id: string) => machines.find((m) => m.id === id)?.name ?? id;
  if (a) {
    parts.push(machineName(a.machine_id));
    if (a.cwd) parts.push(a.cwd.split("/").filter(Boolean).at(-1) ?? a.cwd);
  } else {
    // No agent (yet, or ever): where the plan meant it to run.
    const planned = todo.plan_id ? plans[todo.plan_id]?.tasks.find((t) => t.todo_id === todo.id) : undefined;
    if (planned) {
      parts.push(machineName(planned.machine));
      parts.push(planned.cwd.split("/").filter(Boolean).at(-1) ?? planned.cwd);
    }
  }
  if (lead?.agent) parts.push(lead.agent);
  return parts.join(" · ");
}

function since(iso?: string): string | undefined {
  if (!iso) return undefined;
  const m = Math.max(0, Math.floor((Date.now() - Date.parse(iso)) / 60000));
  if (m < 1) return t("assistant-tasks.just_started");
  if (m < 60) return t("assistant-tasks.minutes", { count: m });
  return m % 60 ? t("assistant-tasks.hours_minutes", { hours: Math.floor(m / 60), minutes: m % 60 }) : t("assistant-tasks.hours", { count: m / 60 });
}

/** What a task's lead agent says or does now, in a line (lib/todos taskNow): its words, or the step it is on. */
export function progressOf(lead: Session | undefined) {
  const now = taskNow(lead, !!lead && stateOf(lead) === "working");
  return now && { text: now.kind === "words" ? plainMarkdown(now.text) : now.text, for: now.kind === "step" ? since(now.since) : undefined };
}

/** The card a task's agents wait on: its lead agent's first, else any of its other active agents'. */
export function taskInteraction(todo: Todo, interactions: Interaction[], lead?: Session): Interaction | undefined {
  const mine = (ix: Interaction) => (todo.agents ?? []).some((a) => !a.released_at && a.machine_id === ix.machine_id && a.session_id === ix.session_id);
  return (lead && interactions.find((ix) => ix.machine_id === lead.machine_id && ix.session_id === lead.session_id)) ?? interactions.find(mine);
}

/** What a waiting task needs, in a line (#3301): "Has a question: which framework?", "Needs your permission: swift test", "Plan awaiting approval", the words
 *  the to-dos page's agent rows use; undefined without a card. */
export function taskAsk(ix: Interaction | undefined): string | undefined {
  if (!ix) return undefined;
  const kicker =
    ix.kind === "plan" ? t("todos.kicker_plan") : ix.kind === "permission" ? t("todos.kicker_permission") : ix.kind === "screen" ? t("todos.kicker_screen") : ix.kind === "elicitation" ? t("todos.kicker_elicitation") : t("todos.kicker_question");
  const ask = plainMarkdown(interactionAsk(ix).trim()).replace(/\s+/g, " ").trim();
  return ask ? `${t("todos.kicker_line", { kicker })}${ask}` : kicker;
}

/** Opens a task's detail beside the conversation: its agent's own chat (the side panel). */
export function openTask(id: string) {
  openPanel({ kind: "task", todo: id });
}

// Dev only: `?task=<todo id>` opens that task's panel (screenshots, docs/operations/testing.md).
if (import.meta.env.DEV && typeof location !== "undefined") {
  const id = new URLSearchParams(location.search).get("task");
  if (id) setTimeout(() => openTask(id), 0);
}

/** "Retry" on a task that did not start, or "Continue" on one the model service cut off (lib/feed retryTask), saying why when the
 *  hub refused (the machine offline…). */
export function retryTaskNow(todo: Todo, verb: "retry" | "continue" = "retry") {
  retryTask(todo).catch((e) => toast(verb === "continue" ? t("assistant-tasks.continue_failed", { error: String(e instanceof Error ? e.message : e) }) : t("assistant-tasks.retry_failed", { error: String(e instanceof Error ? e.message : e) }), "error"));
}


/** Whether a conversation draws a task's full card (FeedLineView: a task line from today; older ones are a TaskLine with
 *  no reason or "Retry"). With its todo known, that card is the one place a failed start says why and offers "Retry" / "Remove"
 *  (#4145), so the plan and the receipt do not say it again. */
export function taskCardDrawn(feed: FeedItem[] | undefined, todoId: string | undefined, now = new Date()): boolean {
  return Boolean(todoId) && (feed ?? []).some((i) => i.kind === "task" && i.ref === todoId && isToday(i.at, now));
}

function useTaskCardShown(conversation: string, todoId: string | undefined): boolean {
  const drawn = useFeed((s) => taskCardDrawn(s.feeds[conversation], todoId));
  const known = useStore((s) => Boolean(todoId) && s.todos.some((t) => t.id === todoId));
  return drawn && known;
}

/** A task's "Retry" is on its way to the hub. */
export function useRetrying(todoId: string | undefined): boolean {
  return useFeed((s) => (todoId ? Boolean(s.busy[retryKey(todoId)]) : false));
}

// ── the plan card ──

const PERMISSION: Record<string, string> = { acceptEdits: t("assistant-tasks.perm_accept_edits"), plan: t("assistant-tasks.perm_plan"), auto: t("assistant-tasks.perm_auto"), bypassPermissions: t("assistant-tasks.perm_bypass"), default: t("assistant-tasks.perm_default") };

/** A task of a started plan that did not start: why, unless its task card right below says so already (#4145). */
function PlanTaskFailed({ task, conversation }: { task: PlanTask; conversation: string }) {
  if (useTaskCardShown(conversation, task.todo_id)) return null;
  // A blank reason from the hub would leave the sentence hanging after the colon (#4809).
  const error = task.error?.trim() || t("assistant-tasks.unknown_reason");
  return <span className="text-red">{t("assistant-tasks.plan_task_failed", { title: task.title, error })}</span>;
}

/** Why each task's computer can't start with ultracode (#12; a known Claude Code too old), "" where it can; undefined
 *  where the hub has no ultracode, and the card shows none. Nothing is read for a plan no longer open. */
function usePlanUltracodeBlocked(plan: TaskPlan): string[] | undefined {
  const open = plan.state === "open";
  const offered = useStore((s) => open && hubHas(s.features, ULTRACODE_FEATURE));
  const rows = useStoreRows((s) => (open ? plan.tasks.map((task) => ({ why: machineStartBlocked(s, task.machine) })) : []));
  return offered ? rows.map((r) => r.why) : undefined;
}

/** A computer can't start with ultracode now (what Start sends checks it as it goes). */
const ultracodeBlockedNow = (machine: string) => Boolean(machineStartBlocked(getState(), machine));

export function PlanCard({ plan }: { plan: TaskPlan }) {
  const busy = useFeed((s) => s.busy[plan.id]);
  const [remember, setRemember] = useState(false);
  const [editing, setEditing] = useState(false);
  const [ultraChoice, setUltraChoice] = useState<Record<number, boolean>>({});
  const [workspaceChoice, setWorkspaceChoice] = useState<Record<number, TaskWorkspace>>({});
  const blocked = usePlanUltracodeBlocked(plan);
  if (plan.state !== "open") {
    const failed = plan.tasks.filter((t) => t.error);
    return (
      <div className="flex flex-col gap-1 text-[13px] text-ink-2">
        <span className="flex items-center gap-1.5">
          <Icon name={plan.state === "started" ? "check" : "x"} size={13} />
          {plan.state === "started" ? t("assistant-tasks.plan_started", { count: plan.tasks.length }) : t("assistant-tasks.plan_dismissed", { count: plan.tasks.length })}
        </span>
        {failed.map((task) => (
          <PlanTaskFailed key={task.title} task={task} conversation={plan.conversation} />
        ))}
        {plan.tasks.map((task, i) => <TaskWorkspaceLine key={i} task={task} />)}
      </div>
    );
  }
  const run = (f: () => Promise<void>) => f().catch((e) => toast(t("assistant-tasks.start_failed", { error: String(e instanceof Error ? e.message : e) }), "error"));
  // each task's choice, the card's own else the assistant's: the chip draws it (off where the computer can't), "Edit"
  // opens with it (so a task moved there to a computer that can has it again), and Start sends it, off where it can't
  const ultracode = plan.tasks.map((task, i) => ultraChoice[i] ?? task.ultracode === true);
  const editedPlan = { ...plan, tasks: plan.tasks.map((task, i) => workspaceChoice[i] === undefined ? task : { ...task, workspace: workspaceChoice[i], workspace_resolved: undefined }) };
  const startEdits = () => {
    const edits = planUltracodeToSend(plan, ultracode, ultracodeBlockedNow) ?? plan.tasks.map(() => ({}));
    const all = edits.map((e, i) => workspaceChoice[i] === undefined ? e : { ...e, workspace: workspaceChoice[i] });
    return all.some((e) => Object.keys(e).length) ? all : undefined;
  };
  return (
    <div className="flex flex-col gap-3 rounded-card bg-surface p-4 shadow-card">
      <div className="flex items-center gap-1.5 text-[12px] font-medium text-ink-2">
        <Icon name="list" size={13} />
        {plan.tasks.length > 1 ? t("assistant-tasks.plan_split", { count: plan.tasks.length }) : t("assistant-tasks.plan_single")}
      </div>
      {editedPlan.tasks.map((t, i) => (
        <div key={i} className="flex min-w-0 flex-col gap-2">
          <PlanTaskRow n={i + 1} task={t} numbered={plan.tasks.length > 1} ultra={blocked && { on: ultracode[i], blocked: blocked[i] }} busy={busy} onUltra={(on) => setUltraChoice((c) => ({ ...c, [i]: on }))} />
          <TaskWorkspaceEditor plan={editedPlan} index={i} disabled={busy} onChange={(workspace) => setWorkspaceChoice((c) => ({ ...c, [i]: workspace }))} />
        </div>
      ))}
      <label className="flex cursor-pointer items-center gap-2 text-[12.5px] text-ink-2">
        <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} className="accent-[var(--accent)]" />
        {t("assistant-tasks.plan_remember")}
      </label>
      {/* Start stays in reach (#3822): a card taller than the tray it scrolls in (several tasks, an opened brief) keeps
          its buttons at the tray's bottom edge; a card that fits looks as it did (the padding it takes it gives back) */}
      <div data-plan-actions className="sticky bottom-0 z-[1] -mx-4 -mt-2 -mb-4 flex gap-2 rounded-b-card bg-surface px-4 pt-2 pb-4">
        <Button tone="accent" busy={busy} onClick={() => run(() => startPlan(plan, remember, startEdits()))} className="flex-1">
          {plan.tasks.length > 1 ? t("assistant-tasks.plan_start_n", { count: plan.tasks.length }) : t("assistant-tasks.plan_start")}
        </Button>
        <Button disabled={busy} onClick={() => setEditing(true)}>
          {t("assistant-tasks.plan_edit")}
        </Button>
        <Button disabled={busy} onClick={() => run(() => dismissPlan(plan))}>
          {t("assistant-tasks.plan_dismiss")}
        </Button>
      </div>
      {editing && <Suspense fallback={<Loading />}>
        <PlanEditDialog
          plan={editedPlan}
          ultracode={ultracode}
          onClose={() => setEditing(false)}
          onStart={(edits) => {
            setEditing(false);
            void run(() => startPlan(plan, remember, planEditsToSend(plan, edits, ultracodeBlockedNow)));
          }}
        />
      </Suspense>}
    </div>
  );
}

function PlanTaskRow({ n, task, numbered, ultra, busy, onUltra }: { n: number; task: PlanTask; numbered: boolean; ultra?: { on: boolean; blocked: string }; busy?: boolean; onUltra: (on: boolean) => void }) {
  const machines = useStore((s) => s.machines);
  const machine = machines.find((m) => m.id === task.machine)?.name ?? task.machine;
  const chips: { icon: IconName; text: string }[] = [
    { icon: "machine", text: machine },
    { icon: "folder", text: task.cwd.split("/").filter(Boolean).at(-1) ?? task.cwd },
    // an explicit model wins over the preset's (resolved at the start, with the profile in use then); a launcher of its own
    // is said with it: a different one runs instead of the preset's
    { icon: "agent", text: task.model || [task.launcher, task.tier && t("assistant-tasks.preset", { tier: task.tier })].filter(Boolean).join(" · ") || t("assistant-tasks.default_model") },
  ];
  return (
    <div className={cx("flex flex-col gap-1.5", n > 1 && "border-t border-line pt-3")}>
      <div className="flex items-baseline gap-2 text-[14.5px] font-semibold">
        {numbered && <span className="flex size-[18px] shrink-0 items-center justify-center rounded-full bg-accent-tint text-[11px] font-bold text-accent">{n}</span>}
        {task.title}
      </div>
      <div className="flex flex-wrap gap-1.5">
        {chips.map((c) => (
          <span key={c.icon} className="inline-flex h-6 items-center gap-1 rounded-full bg-field px-2 text-[12px] text-ink-2">
            <Icon name={c.icon} size={12} />
            {c.text}
          </span>
        ))}
        <span className={cx("inline-flex h-6 items-center rounded-full px-2 text-[12px]", task.readonly ? "bg-accent-tint text-accent" : "bg-field text-ink-2")}>
          {task.readonly ? t("assistant-tasks.readonly") : PERMISSION[task.permission || "default"] ?? task.permission}
        </span>
        {ultra && <UltracodeChip on={ultra.on} blocked={ultra.blocked} disabled={busy} onChange={onUltra} />}
      </div>
      {/* it is asked for where it can't run: say it starts without */}
      {ultra?.blocked && ultra.on && <div className="text-[12px] text-orange">{t("ultracode.plan_blocked", { reason: ultra.blocked })}</div>}
      {task.why && <div className="text-[12px] text-ink-2">{task.why}</div>}
      {/* what the task will be held to, shown before "Start" confirms it (#3822) */}
      {task.brief && (
        <Suspense fallback={null}>
          <PlanBrief brief={task.brief} title={task.title} />
        </Suspense>
      )}
    </div>
  );
}

// ── task cards ──

/** What a task card draws from the store: the task, its status and lead agent, the card it waits on (only while it
 *  waits) and where it runs (with the machine's name, so a renamed or loaded machine draws it again). */
function useTaskCard(todoId: string) {
  const plans = useFeed((s) => s.plans);
  const card = useStore((s) => {
    const todo = s.todos.find((t) => t.id === todoId);
    if (!todo) return undefined;
    const { status, lead } = taskNowIn(s, todo, plans);
    const ix = status === "needs_you" ? taskInteraction(todo, s.interactions, lead) : undefined;
    return { todo, status, lead, ix, where: place(todo, lead, s.machines, plans) };
  });
  return { ...card, plans };
}

export function TaskCard({ todoId, conversation }: { todoId: string; conversation: string }) {
  const { todo, status, lead, ix, where, plans } = useTaskCard(todoId);
  const ultra = useTaskUltracode(todo);
  const menu = useTaskMenu(todo, status);
  const retrying = useRetrying(todoId);
  useNow(30_000, status === "working" || status === "usage_limit"); // a step's age, and the limit's grace running out
  // its turn ended to wait for a background workflow (#2935): the run's progress, under its own.
  // Called before the early return: a card drawn before its todo arrives must call the same hooks once it has (#5422).
  const wfNow = useNow(60_000, runningWorkflows(lead).length > 0);
  if (!todo || !status) return null;
  const info = TODO_STATUS_INFO[status];
  const startError = todo.agents?.find((a) => a.start_error)?.start_error;
  const cut = status === "interrupted" ? interruption(lead) : undefined;
  const limitText = status === "usage_limit" ? (usageLimitLabel(lead) ?? t("lib-usage-limit.short")) : undefined;
  const progress = progressOf(lead);
  const now =
    status === "needs_you"
      ? (taskAsk(ix) ?? t("assistant-tasks.needs_answer"))
      : limitText
        ? limitText
        : cut
        ? t("assistant-tasks.interrupted_reason", { reason: cut.reason })
        : status === "failed"
        ? t("assistant-tasks.start_failed", { error: startFailure(todo, plans) ?? t("assistant-tasks.unknown_reason") })
        : progress?.text || (startError ? t("assistant-tasks.start_failed", { error: startError }) : status === "not_started" ? t("assistant-tasks.starting_agent") : t("assistant-tasks.no_activity"));
  const stepFor = now === progress?.text ? progress.for : undefined;
  // Its subagents add to its own progress, below it, and never stand in for it.
  const subagents = status === "working" || status === "waiting" || status === "needs_you" ? runningSubagents(lead) : [];
  const workflow = status === "working" || status === "waiting" || status === "needs_you" ? workflowLine(lead, wfNow) : "";
  // what its agent last told Persona (report_to_supervisor): while the task is open
  const reported = status !== "done" && status !== "failed" ? parkedLine(todo) ?? progressLine(todo.supervision) : undefined;
  // nobody is working on it (#3618): the hub's finding and why, ahead of what its agent last reported
  const orphan = status !== "needs_you" ? orphanedLine(todo) : undefined;
  // its agent's own task list (#5302), unless a report to Persona came after it: the report says more
  const plan = status !== "done" && status !== "failed" && planShown(lead, todo.supervision) ? planLine(lead) : "";
  return (
    <div
      role="button"
      tabIndex={0}
      onClick={() => openTask(todo.id)}
      // Enter / Space on the card itself (not on its own buttons) opens it, as a click does
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
        e.preventDefault();
        openTask(todo.id);
      }}
      onContextMenu={menu.onContextMenu}
      aria-label={t("assistant-tasks.card_label", { title: todo.title, status: info.label })}
      className={cx("flex cursor-pointer flex-col gap-2 rounded-card bg-surface p-4 shadow-card transition-shadow hover:shadow-raised", status === "needs_you" && "ring-[1.5px] ring-red")}
    >
      <div className="flex items-center gap-2">
        <TaskMark status={status} />
        <TaskTitle title={todo.title} ultra={ultra} mark={12} className="text-[14.5px] font-semibold" />
        {status !== "done" && status !== "failed" && <span className="shrink-0 text-[12px] text-ink-3">{since(todo.created_at)}</span>}
      </div>
      <div className="truncate text-[12px] text-ink-2">{where}</div>
      <div className={cx("flex items-start gap-2 rounded-control bg-field px-3 py-2 text-[13px]", status === "needs_you" ? "text-attention" : (status === "failed" || cut) && "text-red")}>
        <Icon name={status === "needs_you" ? "hand" : limitText ? "clock" : status === "failed" || cut ? "info" : "terminal"} size={13} className="mt-0.5 shrink-0 text-ink-2" />
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="line-clamp-2">
            {now}
            {stepFor && <span className="text-ink-3"> · {stepFor}</span>}
          </span>
          {subagents.length > 0 && (
            <span className="truncate text-[12px] text-ink-3">
              {subagents.length > 3
                ? t("assistant-tasks.subagents_running_more", { names: subagents.slice(0, 3).join(t("assistant-tasks.list_separator")), count: subagents.length })
                : t("assistant-tasks.subagents_running", { names: subagents.slice(0, 3).join(t("assistant-tasks.list_separator")) })}
            </span>
          )}
          {plan && (
            <span data-plan-line title={planLabel(lead)} aria-label={planLabel(lead)} className="flex min-w-0 items-center gap-1 text-[12px] text-ink-3">
              <Icon name="list" size={11} stroke={2.2} className="shrink-0" />
              <span className="truncate tabular-nums">{plan}</span>
            </span>
          )}
          {orphan && (
            <span data-orphaned-line className="flex min-w-0 items-center gap-1 text-[12px] text-orange">
              <Icon name="warning" size={11} stroke={2.2} className="shrink-0" />
              <span className="truncate"><span className="font-medium">{orphan.lead}</span>{orphan.reason && <span className="text-ink-3"> · {orphan.reason}</span>}</span>
            </span>
          )}
          {reported && (
            <span data-progress-line className="flex min-w-0 items-center gap-1 text-[12px] text-ink-3">
              <Icon name="chat" size={11} stroke={2.2} className="shrink-0" />
              <span className="truncate">{reported}</span>
            </span>
          )}
          {workflow && (
            <span data-workflow-line className="flex min-w-0 items-center gap-1 text-[12px] text-ai">
              <Icon name="workflow" size={11} stroke={2.2} className="shrink-0" />
              <span className="truncate tabular-nums">{workflow}</span>
            </span>
          )}
        </span>
      </div>
      {/* what its result waits on from the user, and the one tap that answers it (#2056) */}
      <TodoAskLine todo={todo} className="px-1" />
      {/* what Persona sent its agent (#1542): the hand-off sent with this card is drawn only here */}
      <TaskSentList todoId={todo.id} conversation={conversation} />
      {status === "done" && todo.supervision && (
        <div className="flex items-center text-[12.5px]">
          <ReviewBadge todo={todo} />
        </div>
      )}
      {status !== "done" && (
        <div className="flex items-center gap-2 text-[12.5px]">
          <span className={cx("font-medium", TONE[info.tone])}>{info.label}</span>
          <ReviewBadge todo={todo} />
          <span className="flex-1" />
          {status === "failed" ? (
            // Nothing to say a word to: the ways out are on the card — start it again as it was, or let it go.
            <span className="flex items-center gap-4">
              <button
                type="button"
                disabled={retrying}
                onClick={(e) => {
                  e.stopPropagation();
                  retryTaskNow(todo);
                }}
                className="inline-flex items-center gap-1 whitespace-nowrap font-semibold text-accent hover:underline disabled:opacity-50 disabled:hover:no-underline"
              >
                <Icon name="retry" size={12} />
                {retrying ? t("assistant-tasks.retrying") : t("assistant-tasks.retry")}
              </button>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  removeTaskNow(todo);
                }}
                className="inline-flex items-center gap-1 whitespace-nowrap font-semibold text-red hover:underline"
              >
                <Icon name="trash" size={12} />
                {t("assistant-tasks.remove")}
              </button>
            </span>
          ) : (
            <span className="flex items-center gap-4">
              {(cut?.canContinue || (limitText && lead && !usageLimitContinues(lead))) && (
                // The model service cut its turn off, or a usage limit nothing will take up: "Continue" tells its agent
                // to go on. Hidden while the CLI or the hub will continue it (a message would cancel that).
                <button
                  type="button"
                  disabled={retrying}
                  onClick={(e) => {
                    e.stopPropagation();
                    retryTaskNow(todo, "continue");
                  }}
                  className="inline-flex items-center gap-1 whitespace-nowrap font-semibold text-accent hover:underline disabled:opacity-50 disabled:hover:no-underline"
                >
                  <Icon name="retry" size={12} />
                  {t("assistant-tasks.continue")}
                </button>
              )}
              {!(limitText && lead && usageLimitContinues(lead)) && (
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    setRoute(conversation, todo.id);
                  }}
                  className="inline-flex items-center gap-1 whitespace-nowrap font-semibold text-accent hover:underline"
                >
                  <Icon name="send" size={12} />
                  {t("assistant-tasks.say_something")}
                </button>
              )}
            </span>
          )}
        </div>
      )}
      {menu.menu}
    </div>
  );
}

/** A task of another day, folded into a line. */
export function TaskLine({ todoId }: { todoId: string }) {
  const todo = useTodo(todoId);
  const status = useTaskStatus(todo);
  const menu = useTaskMenu(todo, status);
  const ultra = useTaskUltracode(todo);
  if (!todo || !status) return null;
  const info = TODO_STATUS_INFO[status];
  return (
    <>
    <button type="button" onClick={() => openTask(todo.id)} onContextMenu={menu.onContextMenu} className="flex w-full items-center gap-2 rounded-control bg-field px-3 py-2 text-left text-[13px] hover:bg-hover">
      <TaskMark status={status} />
      <TaskTitle title={todo.title} ultra={ultra} mark={11} className="font-medium" />
      <span className={cx("shrink-0 text-[12px]", TONE[info.tone])}>{info.label}</span>
      <Icon name="chevronRight" size={13} className="text-ink-3" />
    </button>
    {menu.menu}
    </>
  );
}

// ── the hub's lines ──

// Memoized: the chat re-renders on every streamed item and session push, while an item keeps its identity until its body
// changes (mergeInto); the plan, todo and "Later" it shows come through its own selectors.
export const FeedLineView = memo(function FeedLineView({ item, simplified = false }: { item: FeedItem; simplified?: boolean }) {
  const plan = useFeed((s) => (item.kind === "plan" && item.ref ? s.plans[item.ref] : undefined));
  const isLater = useFeed((s) => !!s.later[item.id]);
  const title = useStore((s) => s.todos.find((todo) => todo.id === item.ref)?.title) ?? t("assistant-tasks.task_fallback");
  const known = useStore((s) => s.todos.some((t) => t.id === item.ref));
  const planned = useFeed((s) => item.kind === "task" && Object.values(s.plans).some((p) => p.conversation === item.conversation && p.tasks.some((task) => task.todo_id === item.ref)));
  if (simplified) {
    // the dispatched tasks themselves, each opening its own task (its Agent's chat), never the whole In progress tray (#3700)
    if (item.kind === "plan") return plan?.state === "started" ? <DispatchHint tasks={plan.tasks.map((task) => ({ todo: task.todo_id, title: task.title }))} /> : null;
    if (item.kind === "task") return planned ? null : <DispatchHint tasks={[{ todo: item.ref, title }]} />;
    // an interrupted receipt the hub worked out stays as its record (#3834): one card for a batch, a grey line once handled
    if (item.kind === "receipt" && interruptLook(item)) return <ReceiptCard item={item} />;
    if (item.kind === "review" || item.kind === "receipt" || item.kind === "report") return null;
    if (item.kind === "message" && !cardSent(item) && !cardFiled(item)) return null;
  }
  switch (item.kind) {
    case "plan":
      return plan ? <PlanCard plan={plan} /> : null;
    case "task":
      return item.ref ? isToday(item.at) ? <TaskCard todoId={item.ref} conversation={item.conversation} /> : <TaskLine todoId={item.ref} /> : null;
    case "answered":
      return <AnsweredLine item={item} title={title} onOpenTask={known && item.ref ? () => openTask(item.ref!) : undefined} />;
    case "receipt":
      return <ReceiptCard item={item} />;
    case "review":
      return <ReviewLine item={item} title={title} onOpenTask={known && item.ref ? () => openTask(item.ref!) : undefined} />;
    case "report":
      return <ReportLine item={item} title={title} onOpenTask={known && item.ref ? () => openTask(item.ref!) : undefined} />;
    case "routed":
      // Persona's own message to the task's agent is folded, never the user's bubble (#1542)
      if (sentByPersona(item)) return <SentLine item={item} title={title} onOpenTask={known && item.ref ? () => openTask(item.ref!) : undefined} />;
      // what the user said straight to the task (its card's Message); interrupted: from a hub before #1542
      return (
        <div className="flex flex-col items-end gap-1 pl-10">
          <span className="inline-flex items-center gap-1 text-[12px] font-medium text-accent">
            <Icon name={item.body?.interrupted === true ? "stop" : "send"} size={11} />
            {item.body?.interrupted === true ? t("assistant-tasks.said_to_interrupting", { title }) : t("assistant-tasks.say_to", { title })}
          </span>
          <div className="rounded-[18px] bg-field px-3.5 py-2 text-[14.5px]">{field(item, "text")}</div>
        </div>
      );
    case "today":
      return <TodayCard body={item.body as unknown as TodayBody} conversation={item.conversation} />;
    case "brief":
      return <BriefCard item={item} />;
    case "message":
      return item.ref && !isLater ? <MessageCard item={item} /> : null;
    case "notice": {
      // a line the hub wrote (#4305): small and centered, as the chat's own notices
      const text = feedNotice(item);
      return text ? (
        <p data-testid="feed-notice" className="px-6 text-center text-[12px] text-ink-2 [overflow-wrap:anywhere]">
          {text}
        </p>
      ) : null;
    }
    case "view":
      // a live card: drawn where it was first shown, again at each version (ui/AssistantCard.tsx)
      return item.ref ? <LiveCard conversation={item.conversation} id={item.ref} first={(item.body as { view?: AssistantView } | undefined)?.view} /> : null;
    default:
      return null;
  }
});

/** A card of a task's agent the user dealt with, kept as a line "✓ task · answer". Click it for every question with its
 *  answer in full (#1231), selectable, and again to fold it; the task's name opens the task. */
export function AnsweredLine({ item, title, onOpenTask }: { item: FeedItem; title: string; onOpenTask?: () => void }) {
  const [open, setOpen] = useState(false);
  const items = answeredItems(item);
  const expandable = items.length > 0;
  return (
    <div className={cx("flex max-w-full flex-col self-start bg-field text-[12px] text-ink-2", open ? "w-full rounded-card" : "rounded-full")}>
      <div className="flex min-w-0 items-center gap-1.5 px-3 py-1.5">
        <Icon name="check" size={12} className="shrink-0" />
        {onOpenTask ? (
          <button type="button" onClick={onOpenTask} className="max-w-[45%] shrink-0 truncate text-left hover:text-ink hover:underline">
            {title}
          </button>
        ) : (
          <span className="max-w-[45%] shrink-0 truncate">{title}</span>
        )}
        <button
          type="button"
          aria-expanded={expandable ? open : undefined}
          disabled={!expandable}
          onClick={() => setOpen(!open)}
          className="flex min-w-0 flex-1 items-center gap-1.5 text-left enabled:cursor-pointer"
        >
          <span className="min-w-0 flex-1 truncate">· {field(item, "answer") ?? t("assistant-tasks.answered")}</span>
          {expandable && (
            <Icon name="chevronRight" size={12} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : "none" }} />
          )}
        </button>
      </div>
      <Collapse open={open} lazy>
        <div className="flex flex-col gap-2.5 px-3 pt-0.5 pb-2.5 select-text" data-testid="answered-detail">
          {items.map((x, i) => (
            <div key={i} className="flex flex-col gap-0.5 whitespace-pre-wrap [overflow-wrap:anywhere]">
              {x.question && <div className="font-medium text-ink-3">{x.question}</div>}
              {x.answer && <div className="text-[13px] leading-[1.55] text-ink">{x.answer}</div>}
            </div>
          ))}
        </div>
      </Collapse>
    </div>
  );
}

function ReceiptCard({ item }: { item: FeedItem }) {
  const state = receiptState(item);
  const look = interruptLook(item);
  if (look?.kind === "hidden") return null;
  if (look?.kind === "batch") return <InterruptBatchCard batch={look.batch} />;
  if (look?.kind === "resolved") return <InterruptHandledLine todoId={item.ref} title={field(item, "title") ?? t("assistant-tasks.task_fallback")} resolution={look.resolution} />;
  if (state === "interrupted") return <InterruptedReceipt item={item} />;
  return <OutcomeReceipt item={item} failed={state === "failed"} />;
}

/** The words for how an interrupted task was dealt with, as the hub said (#3834). */
function resolutionLabel(r: InterruptResolution): string {
  return t(`assistant-tasks.interrupt_resolution_${r}`);
}

/** The words for what cut a batch off (the hub's `cause`; one it doesn't know reads as a model service error). */
function causeLabel(cause: string): string {
  const known = ["model_error", "rate_limited", "auth", "model_route", "no_output"];
  return t(`assistant-tasks.interrupt_cause_${known.includes(cause) ? cause : "model_error"}`);
}

/** An interrupted task that was dealt with since (#3834): a grey line, its result in words; it opens the task. */
function InterruptHandledLine({ todoId, title, resolution }: { todoId?: string; title: string; resolution: Exclude<InterruptResolution, "pending"> }) {
  const known = useStore((s) => s.todos.some((x) => x.id === todoId));
  const text = t("assistant-tasks.interrupt_handled", { title, result: resolutionLabel(resolution) });
  const body = (
    <>
      <Icon name="info" size={13} className="shrink-0" />
      <span className="min-w-0 truncate">{text}</span>
    </>
  );
  return known && todoId ? (
    <button type="button" data-interrupt="handled" onClick={() => openTask(todoId)} className="flex max-w-full items-center gap-1.5 self-start text-left text-[13px] text-ink-2 hover:text-ink">
      {body}
    </button>
  ) : (
    <div data-interrupt="handled" className="flex max-w-full items-center gap-1.5 self-start text-[13px] text-ink-2">{body}</div>
  );
}

/** Several tasks one outage cut off (#3834): one card, its cause, how many are handled and how many still pending; it
 *  opens to each task and its result. Red only while one is pending. */
export function InterruptBatchCard({ batch }: { batch: InterruptBatch }) {
  const [open, setOpen] = useState(false);
  const todos = useStore((s) => s.todos);
  const pending = batch.pending > 0;
  const cause = causeLabel(batch.cause);
  const title = batch.machine
    ? t("assistant-tasks.interrupt_batch_title_on", { machine: batch.machine, cause, count: batch.total })
    : t("assistant-tasks.interrupt_batch_title", { cause, count: batch.total });
  const counts = pending ? t("assistant-tasks.interrupt_batch_counts", { resolved: batch.resolved, pending: batch.pending }) : t("assistant-tasks.interrupt_batch_all");
  return (
    <div data-interrupt="batch" data-pending={pending || undefined} className={cx("flex flex-col rounded-card", pending ? "bg-red-tint" : "bg-field")}>
      <button type="button" aria-expanded={open} onClick={() => setOpen(!open)} className="flex items-start gap-3 p-4 text-left">
        <Icon name="info" size={18} stroke={2.4} className={cx("shrink-0", pending ? "text-red" : "text-ink-3")} />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className={cx("text-[14.5px] font-semibold", !pending && "text-ink-2")}>{title}</div>
          <div className="text-[13px] text-ink-2">{counts}</div>
        </div>
        <Icon name="chevronRight" size={14} stroke={2.2} className="mt-1 shrink-0 text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : "none" }} />
      </button>
      <Collapse open={open} lazy>
        <div className="flex flex-col gap-0.5 px-3 pb-3" data-testid="interrupt-batch-tasks">
          {batch.tasks.map((task) => {
            const known = todos.find((x) => x.id === task.todo_id);
            const waiting = task.resolution === "pending";
            return (
              <button
                key={task.receipt}
                type="button"
                disabled={!known}
                onClick={() => known && openTask(known.id)}
                className="flex min-w-0 items-center gap-2 rounded-control px-2 py-1.5 text-left text-[13px] enabled:hover:bg-hover"
              >
                <span className="min-w-0 flex-1 truncate">{known?.title || task.title}</span>
                <span className={cx("shrink-0 whitespace-nowrap", waiting ? "font-medium text-red" : "text-ink-2")}>{resolutionLabel(task.resolution)}</span>
                {known && <Icon name="chevronRight" size={12} className="shrink-0 text-ink-3" />}
              </button>
            );
          })}
        </div>
      </Collapse>
    </div>
  );
}

/** A task the model service cut off (#1516): red, why in words, "Continue" while it is still cut off; once its agent went on,
 *  a quiet line that opens the task (a later receipt says how it ended). */
function InterruptedReceipt({ item }: { item: FeedItem }) {
  const todo = useTodo(item.ref);
  const task = useTaskNow(todo);
  const clock = useNow(30_000, task?.status === "usage_limit");
  const retrying = useRetrying(todo?.id);
  const status = task?.status;
  const title = field(item, "title") ?? t("assistant-tasks.task_fallback");
  if (todo && task?.status === "usage_limit") {
    const goesOn = task.lead ? usageLimitContinues(task.lead) : false;
    return (
      <div className="flex gap-3 rounded-card bg-field p-4">
        <Icon name="clock" size={18} stroke={2.4} className="text-orange" />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="text-[14.5px] font-semibold">{title}</div>
          <div className="text-[13px] text-ink-2">{usageLimitLabel(task.lead, clock) ?? t("lib-usage-limit.short")}</div>
          <div className="mt-1 flex gap-2">
            {!goesOn && (
              <Button icon="retry" busy={retrying} disabled={retrying} onClick={() => retryTaskNow(todo, "continue")}>
                {t("assistant-tasks.continue")}
              </Button>
            )}
            {item.ref && <Button onClick={() => openTask(item.ref!)}>{t("assistant-tasks.look")}</Button>}
          </div>
        </div>
      </div>
    );
  }
  if (todo && status && status !== "interrupted") {
    return (
      <button type="button" onClick={() => openTask(todo.id)} className="flex items-center gap-1.5 self-start text-left text-[13px] text-ink-2 hover:text-ink">
        <Icon name={status === "done" ? "check" : "retry"} size={13} />
        {status === "done" ? t("assistant-tasks.receipt_done", { title }) : t("assistant-tasks.receipt_continued", { title })}
      </button>
    );
  }
  const cut = todo ? interruption(task?.lead) : undefined;
  const raw = field(item, "error") ?? field(item, "summary");
  const reason = cut?.reason ?? parseAPIError(raw)?.reason;
  const canContinue = todo && (cut ? cut.canContinue : true);
  return (
    <div className="flex gap-3 rounded-card bg-red-tint p-4">
      <Icon name="info" size={18} stroke={2.4} className="text-red" />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="text-[14.5px] font-semibold">{t("assistant-tasks.interrupted_title", { title })}</div>
        <Clamped className="text-[13px] text-ink-2">{reason ? t("assistant-tasks.interrupted_reason", { reason }) : t("assistant-tasks.interrupted")}</Clamped>
        <div className="mt-1 flex gap-2">
          {canContinue && (
            <Button icon="retry" busy={retrying} disabled={retrying} onClick={() => retryTaskNow(todo, "continue")}>
              {t("assistant-tasks.continue")}
            </Button>
          )}
          {item.ref && <Button onClick={() => openTask(item.ref!)}>{t("assistant-tasks.look")}</Button>}
        </div>
      </div>
    </div>
  );
}

function OutcomeReceipt({ item, failed }: { item: FeedItem; failed: boolean }) {
  // A failed start's banner follows its task: "Retry" while it is still failed (with its latest error), a quiet line once
  // it was started again, so no stale error stays in the conversation (#1396).
  const linked = useTodo(item.ref);
  const todo = failed ? linked : undefined;
  // A done receipt ends a stretch of work, not the task: while its todo is open (the agent stopped unfinished, waits on
  // the user…) it says so, not "done"; it turns green once the todo is done (#6153). One gone from the list stands on its receipt.
  const stopped = !failed && !!linked && !linked.done_at;
  // A receipt is historical. Only the current todo can say it actually awaits acceptance now.
  const review = stopped ? item.body?.review as { state?: string; criteria?: number } | undefined : undefined;
  const awaitingReview = stopped && linked?.supervision?.state === "pending_review";
  const criteria = review?.criteria ?? linked?.supervision?.done_when?.length ?? 0;
  const plans = useFeed((s) => s.plans);
  const retrying = useRetrying(todo?.id);
  const status = useTaskStatus(todo);
  const cardShown = useTaskCardShown(item.conversation, todo?.id);
  const pr = field(item, "pr");
  const prN = pr?.split("/").at(-1);
  const title = field(item, "title") ?? t("assistant-tasks.task_fallback");
  if (failed && todo && status && status !== "failed") {
    const again = status === "not_started" ? t("assistant-tasks.receipt_restarting", { title }) : t("assistant-tasks.receipt_restarted", { title });
    return (
      <button type="button" onClick={() => openTask(todo.id)} className="flex items-center gap-1.5 self-start text-left text-[13px] text-ink-2 hover:text-ink">
        <Icon name={status === "not_started" ? "retry" : "check"} size={13} />
        {again}
      </button>
    );
  }
  // Still failed with its task card drawn above: that card says why and has "Retry" / "Remove", so this is one line pointing
  // there, not the same error and a second "Retry" again (#4145). No "Copy" / "Quote" on it: the card is what to quote.
  if (failed && todo && status === "failed" && cardShown) {
    return (
      <button type="button" onClick={() => openTask(todo.id)} className="flex items-center gap-1.5 self-start text-left text-[13px] text-red hover:underline">
        <Icon name="info" size={13} />
        {t("assistant-tasks.receipt_failed_above", { title })}
      </button>
    );
  }
  const failure = failed && todo ? startFailure(todo, plans) : undefined;
  const summary = failure ? t("assistant-tasks.start_failed", { error: failure }) : field(item, "summary");
  const heading = failed
    ? t("assistant-tasks.outcome_failed", { title })
    : stopped
      ? t("assistant-tasks.outcome_stopped", { title })
      : t("assistant-tasks.outcome_done", { title });
  // "Copy" / "Quote" like a reply's (#2462): the quote names the task and its todo id for the assistant
  return (
    <MessageActions text={summary?.trim() ? summary : heading} ts={item.at} task={{ title, ...(item.ref ? { id: item.ref } : {}) }} corner>
      <div className={cx("flex gap-3 rounded-card p-4", failed ? "bg-red-tint" : stopped ? "bg-field" : "bg-green-tint")} data-receipt>
        <Icon name={failed ? "info" : stopped ? "clock" : "check"} size={18} stroke={2.4} className={failed ? "text-red" : stopped ? "text-ink-2" : "text-green"} />
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="text-[14.5px] font-semibold">{heading}</div>
          {summary && <ReceiptSummary text={summary} plain={failed} />}
          <div className="mt-1 flex gap-2">
            {failed && todo && (
              <Button icon="retry" busy={retrying} disabled={retrying} onClick={() => retryTaskNow(todo)}>
                {t("assistant-tasks.retry")}
              </Button>
            )}
            {pr && (
              <Button icon="git" onClick={() => window.open(pr, "_blank", "noopener")}>
                PR #{prN}
              </Button>
            )}
            {!failed && item.ref && <Button onClick={() => openTask(item.ref!)}>{t("assistant-tasks.look")}</Button>}
          </div>
          {!awaitingReview && (review?.state === "checking" || review?.state === "awaiting") && <div className="text-xs text-ink-3">{t("assistant-tasks.receipt_review_followup")}</div>}
          {awaitingReview && (
            <div className="text-xs text-ink-3">
              {criteria > 0
                ? t("assistant-tasks.receipt_review_criteria", { count: criteria })
                : t("assistant-tasks.receipt_review_awaiting")}
            </div>
          )}
        </div>
      </div>
    </MessageActions>
  );
}

/** What the task's agent said last, as Markdown (a start error is plain text), in about four lines (#1508); "Show all"
 *  shows the rest in place when it is cut (#4715). */
export function ReceiptSummary({ text, plain }: { text: string; plain?: boolean }) {
  if (plain) return <Clamped className="text-[13px] text-ink-2">{text}</Clamped>;
  return (
    <Clamped className="text-ink-2">
      <Markdown text={text} compact />
    </Clamped>
  );
}

/** Four lines of a card's text; when they cut it, a "Show all" / "Hide" under them opens the rest in place (#4715). */
function Clamped({ className, children }: { className: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const { ref, cut: long } = useClamped<HTMLDivElement>(open);
  return (
    <>
      <div ref={ref} className={cx(className, !open && "line-clamp-4")} data-clamped={open ? undefined : ""}>
        {children}
      </div>
      {long && (
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          className="-ml-1 w-fit rounded-[6px] px-1 text-[12px] font-medium whitespace-nowrap text-ink-3 transition-colors hover:bg-hover-2 hover:text-ink-2"
        >
          {open ? t("assistant-tasks.summary_collapse") : t("assistant-tasks.summary_expand")}
        </button>
      )}
    </>
  );
}

export function QuickResultCard({ result }: { result: QuickResult }) {
  const when = result.when ? formatDate(result.when, { weekday: "short", hour: "2-digit", minute: "2-digit" }) : undefined;
  return (
    <div className="flex items-center gap-3 rounded-card bg-field px-3.5 py-3">
      <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-control", result.kind === "reminder" ? "bg-orange-tint text-orange" : "bg-accent-tint text-accent")}>
        <Icon name={result.kind === "reminder" ? "bell" : "clock"} size={16} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-[13.5px] font-semibold">{result.kind === "reminder" ? t("assistant-tasks.reminder_added") : t("assistant-tasks.calendar_added")}</div>
        <div className="truncate text-[12px] text-ink-2">{[when, result.title, result.place].filter(Boolean).join(" · ")}</div>
      </div>
      <span className="shrink-0 text-[11.5px] text-ink-3">{t("assistant-tasks.on_iphone")}</span>
    </div>
  );
}

// ── Today ──

/** The "Today" card's "Needs you" row's lines under its title (#4478), one per task, each truncated on its own: what its task
 *  waits on ("Has a question: …"), or for several, up to three "<title>: <what it waits on>" (the title alone when the store
 *  has no card) and "N more" past them. A card just answered here is not one it waits on (answeredLookup). */
const TODAY_WAITING_MAX = 3;
function todayWaitingLines(s: State, waiting: { id: string; title: string }[]): string[] {
  const lookup = answeredLookup(taskSessionLookup(s), s.interactions, s.answered);
  const askOf = (id: string) => {
    const todo = s.todos.find((x) => x.id === id);
    return todo ? taskAsk(taskInteraction(todo, s.interactions, mostUrgentAgent(todo, lookup)?.session)) : undefined;
  };
  if (waiting.length === 1) {
    const ask = askOf(waiting[0].id);
    return ask ? [ask] : [];
  }
  const out = waiting.slice(0, TODAY_WAITING_MAX).map((w) => {
    const ask = askOf(w.id);
    return ask ? t("assistant-tasks.dock_task_asks", { title: w.title, ask }) : w.title;
  });
  if (waiting.length > TODAY_WAITING_MAX) out.push(t("assistant-tasks.more_asks", { count: waiting.length - TODAY_WAITING_MAX }));
  return out;
}

function TodayCard({ body, conversation }: { body: TodayBody; conversation: string }) {
  const say = useSayToPersona(conversation);
  const [board, setBoard] = useState(false);
  // one string, so useStore's compare keeps the card still while nothing it shows changes
  const waitingText = useStore((s) => (body.waiting?.length ? todayWaitingLines(s, body.waiting).join("\n") : ""));
  const rows: { icon: IconName; tint: string; title: string; detail?: string; lines?: string[]; action?: string; run?: () => void }[] = [];
  if (body.done?.length)
    rows.push({ icon: "check", tint: "bg-green-tint text-green", title: body.done.length === 1 ? t("assistant-tasks.today_done_single", { title: body.done[0].title }) : t("assistant-tasks.today_done_n", { count: body.done.length }), detail: body.done.length > 1 ? body.done.map((d) => d.title).join(t("assistant-tasks.list_separator")) : undefined, action: t("assistant-tasks.look"), run: () => openTask(body.done![0].id) });
  // What did not go through overnight (#4124): "View" opens the newest, a task beside the conversation or a run's Scheduled tasks
  const failed = todayFailedRow(body);
  if (failed) {
    const first = failed.first;
    rows.push({ icon: "info", tint: "bg-red-tint text-red", title: failed.title, detail: failed.detail, action: t("assistant-tasks.go_look"), run: first.kind === "task" ? () => openTask(first.id) : () => navigate({ name: "routines" }) });
  }
  if (body.waiting?.length)
    rows.push({ icon: "hand", tint: "bg-field text-attention", title: body.waiting.length === 1 ? t("assistant-tasks.today_waiting_single", { title: body.waiting[0].title }) : t("assistant-tasks.today_waiting_n", { count: body.waiting.length }), lines: waitingText ? waitingText.split("\n") : undefined, action: t("assistant-tasks.go_look"), run: () => openTask(body.waiting![0].id) });
  if (body.pending_review?.length)
    rows.push({ icon: "hand", tint: "bg-field text-attention", title: body.pending_review.length === 1 ? t("assistant-tasks.today_pending_review_single", { title: body.pending_review[0].title }) : t("assistant-tasks.today_pending_review_n", { count: body.pending_review.length }), action: t("assistant-tasks.go_look"), run: () => openTask(body.pending_review![0].id) });
  if (body.working) rows.push({ icon: "gear", tint: "bg-orange-tint text-orange", title: t("assistant-tasks.today_working", { count: body.working }), action: t("assistant-tasks.all"), run: () => setBoard(true) });
  for (const m of (body.meetings ?? []).slice(0, 3)) {
    const at = formatDate(m.start, { hour: "2-digit", minute: "2-digit" });
    rows.push({ icon: "clock", tint: "bg-accent-tint text-accent", title: t("assistant-tasks.today_meeting", { time: at, title: m.title }), action: t("assistant-tasks.prepare"), run: () => say(t("assistant-tasks.prepare_prompt", { time: at, title: m.title })) });
  }
  if (body.at_me?.length)
    rows.push({
      icon: "at",
      tint: "bg-accent-tint text-accent",
      title: body.unread ? t("assistant-tasks.today_at_me_unread", { count: body.at_me.length, unread: body.unread }) : t("assistant-tasks.today_at_me", { count: body.at_me.length }),
      detail: body.at_me.slice(0, 2).map((c) => (c.preview ? t("assistant-tasks.chat_preview", { name: c.name, preview: c.preview }) : c.name)).join("\n"),
      action: t("assistant-tasks.review"),
      run: () => say(t("assistant-tasks.review_prompt")),
    });
  return (
    <div className="flex flex-col rounded-card bg-surface p-4 shadow-card">
      <div className="mb-2 flex items-center gap-1.5 text-[12px] font-medium text-ink-2">
        <Icon name="sun" size={13} />
        {t("assistant-tasks.today_title")}
      </div>
      {rows.length === 0 && <div className="text-[13px] text-ink-2">{t("assistant-tasks.today_empty")}</div>}
      {rows.map((r, i) => (
        <div key={i} className={cx("flex items-center gap-3 py-2", i > 0 && "border-t border-line")}>
          <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-control", r.tint)}>
            <Icon name={r.icon} size={15} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="text-[13.5px] font-medium">{r.title}</div>
            {r.detail && <div className="line-clamp-2 whitespace-pre-line text-[12px] text-ink-2">{r.detail}</div>}
            {r.lines?.map((l, j) => (
              <div key={j} data-today-line className="truncate text-[12px] text-ink-2" title={l}>
                {l}
              </div>
            ))}
          </div>
          {r.action && r.run && <Button onClick={r.run}>{r.action}</Button>}
        </div>
      ))}
      {board && <Suspense fallback={null}><TaskBoard open conversation={conversation} onClose={() => setBoard(false)} /></Suspense>}
    </div>
  );
}

/** Says something to Persona in this conversation (the "Today" card's actions). */
function useSayToPersona(conversation: string) {
  const assistant = useStore((s) => s.assistant);
  return (text: string) => {
    const conv = assistant?.conversations.find((c) => c.session_id === conversation);
    if (!conv) return;
    window.dispatchEvent(new CustomEvent("persona:say", { detail: { mid: conv.machine_id, sid: conv.session_id, text } }));
  };
}

// ── message cards ──

const LazyMessageDraft = lazy(() => import("./MessageDraft"));
export const draftTasks = { useTodo, useTaskNow, progressOf, TaskMark, openTask };

function MessageCard({ item }: { item: FeedItem }) {
  return <Suspense fallback={null}><LazyMessageDraft item={item} tasks={draftTasks} /></Suspense>;
}

// ── above the composer ──
// The chat re-renders on every streamed item and hands these only the conversation: they are memo and read the rest
// from the store and the feed themselves, so an item that changes nothing they show renders none of them.

/** The oldest question the conversation's tasks' agents ask, under its task's name ("N more"). */
function questionsIn(s: State, conversation: string) {
  const out: { todo: Todo; ix: Interaction }[] = [];
  const keys = new Set<string>();
  for (const t of s.todos) {
    if (t.origin !== conversation || t.done_at) continue;
    const mine = (t.agents ?? []).filter((a) => !a.released_at).map((a) => `${a.machine_id}/${a.session_id}`);
    mine.forEach((k) => keys.add(k));
    for (const ix of s.interactions) if (mine.includes(`${ix.machine_id}/${ix.session_id}`)) out.push({ todo: t, ix });
  }
  // just answered here: shown answered for a moment above the next one
  const done = Object.values(s.answered).filter((a) => keys.has(`${a.ix.machine_id}/${a.ix.session_id}`));
  return { asks: out.sort((a, b) => byTime(a.ix.created_at, b.ix.created_at)), answered: done };
}

export const TaskQuestions = memo(function TaskQuestions({ conversation }: { conversation: string }) {
  // a card for another agent coming or going leaves these rows as they were: no render
  const asks = useStoreRows((s) => questionsIn(s, conversation).asks);
  const answered = useStoreRows((s) => questionsIn(s, conversation).answered);
  const first = asks[0];
  if (!first && !answered.length) return null;
  return (
    <div className="flex flex-col gap-1">
      {answered.map((a) => (
        <AnsweredCard key={a.ix.id} card={a} />
      ))}
      {first && (
        <>
          <div className="flex items-center gap-1.5 px-1 text-[12px] font-semibold text-attention">
            <Icon name="hand" size={13} />
            <span className="truncate">{first.todo.title}</span>
            <span className="flex-1" />
            {asks.length > 1 && <span className="shrink-0">{t("assistant-tasks.more_asks", { count: asks.length - 1 })}</span>}
          </div>
          <InteractionCard ix={first.ix} />
        </>
      )}
    </div>
  );
});

/** The dock's capsule: "All", the task pills and the Assistant's Pending / Progress / Agents entries share one height,
 *  radius, padding and type size, like iOS DockPillLabel (#2999). */
export const DOCK_CAPSULE = "inline-flex h-8 shrink-0 items-center whitespace-nowrap rounded-full px-3 text-[12.5px]";

/** The dock: "All N" held at its left edge (it does not scroll), then a pill for each task going on (waiting first) and,
 * in the main conversation, each agent going on that no task stands for (#1113), scrolling on their own (iOS TaskDock). */
// Failed, done and stopped tasks are not in the dock.
const DOCK_RANK: Partial<Record<TodoStatus, number>> = { needs_you: 0, interrupted: 0, usage_limit: 0, working: 1, waiting: 1, not_started: 1, unread: 2 };

/** The dock's task pills, waiting first: each task going on, its status and what it waits on (#3463). */
function dockTasksIn(s: State, conversation: string, plans: Record<string, TaskPlan>): { t: Todo; st: TodoStatus; ask?: string; limit?: string }[] {
  const lookup = answeredLookup(taskSessionLookup(s), s.interactions, s.answered);
  return s.todos
    .filter((t) => t.origin === conversation)
    .map((t) => ({ t, st: statusOf(t, lookup, plans) }))
    .filter((x) => DOCK_RANK[x.st] !== undefined)
    .sort((a, b) => DOCK_RANK[a.st]! - DOCK_RANK[b.st]! || byTime(b.t.created_at, a.t.created_at))
    .map(({ t: todo, st }) => {
      const lead = mostUrgentAgent(todo, lookup)?.session;
      return {
        t: todo,
        st,
        ask: st === "needs_you" ? taskAsk(taskInteraction(todo, s.interactions, lead)) : undefined,
        limit: st === "usage_limit" ? (usageLimitLabel(lead) ?? t("lib-usage-limit.short")) : undefined,
      };
    });
}

type AssistantTray = "needs" | "running";
/** Each entry's mark: Needs you waits on the user, In progress is work under way. */
const TRAY_DOT: Record<AssistantTray, string> = { needs: "bg-attention", running: "bg-accent" };
const EMPTY_FEED: FeedItem[] = [];
const EMPTY_CARDS: TaskCardMetadata[] = [];

/** What a reply dispatched, under it (#3700): one task reads "Dispatched: <title> · Open", several list their titles; each
 *  opens that task's detail beside the conversation, its Agent's chat or, before the Agent starts, the task card (openTask).
 *  A task the hub could not start has no to-do yet and is shown without a link (its plan card says why). */
function DispatchHint({ tasks }: { tasks: { todo?: string; title: string }[] }) {
  if (tasks.length === 1) {
    const [task] = tasks;
    return (
      <DispatchedTask todo={task!.todo} title={task!.title} hint className="max-w-full">
        {(title) => (
          <>
            <span className="truncate">{t("assistant-tasks.dispatch_single", { title })}</span>
            {task!.todo && <span className="shrink-0 font-semibold text-accent">· {t("assistant-tasks.dispatch_open")}</span>}
          </>
        )}
      </DispatchedTask>
    );
  }
  return (
    <div data-dispatch-hint={tasks.length} className="flex max-w-full flex-col items-start gap-1.5 self-start">
      <span className="px-1 text-[12px] text-ink-2">{t("assistant-tasks.dispatch_many", { count: tasks.length })}</span>
      <div className="flex max-w-full flex-wrap gap-1.5">
        {tasks.map((task, i) => (
          <DispatchedTask key={task.todo ?? `unstarted-${i}`} todo={task.todo} title={task.title} className="max-w-[280px]">
            {(title) => <span className="truncate">{title}</span>}
          </DispatchedTask>
        ))}
      </div>
    </div>
  );
}

/** One dispatched task's link: the to-do's current title (the plan's until the to-do arrives). */
function DispatchedTask({ todo, title, hint, className, children }: { todo?: string; title: string; hint?: boolean; className: string; children: (title: string) => ReactNode }) {
  const current = useStore((s) => (todo ? s.todos.find((x) => x.id === todo)?.title : undefined)) ?? title;
  const pill = cx("inline-flex min-w-0 items-center gap-2 self-start rounded-full bg-field px-3 py-1.5 text-sm text-ink-2", className);
  if (!todo) return <span data-dispatch-hint={hint ? 1 : undefined} data-dispatched-task="" className={pill}>{children(current)}</span>;
  return (
    <button type="button" data-dispatch-hint={hint ? 1 : undefined} data-dispatched-task={todo} title={current} onClick={() => openTask(todo)} className={cx(pill, "hover:bg-hover")}>
      {children(current)}
      <Icon name="chevronRight" className="shrink-0" />
    </button>
  );
}

/** One slot, one row: live task/session state wins over historical feed metadata. */
function ProgressTaskRow({ card, conversation, activity }: { card: TaskCardMetadata; conversation: string; activity?: BackgroundWork }) {
  const todo = useTodo(card.todo_id);
  const task = useTaskNow(todo);
  const plans = useFeed((s) => s.plans);
  const feed = useFeed((s) => s.feeds[conversation] ?? EMPTY_FEED);
  const planned = card.plan_id !== undefined && card.index !== undefined ? plans[card.plan_id]?.tasks[card.index] : undefined;
  const title = todo?.title ?? planned?.title ?? t("assistant-tasks.task_fallback");
  const accepted = !!todo?.done_at && todo.supervision?.state === "accepted";
  let state = task?.status ?? (card.state === "followup" ? "follow_up" : card.state);
  if (todo && accepted) state = "accepted";
  else if (todo?.done_at && todo.supervision?.state === "cancelled") state = "cancelled";
  else if (!todo && card.plan_id && plans[card.plan_id]?.state === "dismissed") state = "cancelled";
  const finished = state === "done" || state === "accepted";
  const blocked = ["needs_you", "interrupted", "usage_limit", "failed"].includes(state) || (!finished && todo?.supervision?.progress?.kind === "blocked");
  const meter = taskMeter(todo, task?.lead);
  const report = feed.find((it) => it.id === card.report_id);
  const receipt = feed.find((it) => it.id === card.receipt_id);
  const pr = todo?.pr_url ?? (receipt && field(receipt, "pr"));
  const prNumber = pr ? /\/(\d+)\/?$/.exec(pr)?.[1] : undefined;
  const toStart = !todo && !!card.plan_id && plans[card.plan_id]?.state === "open";
  const liveWork = !finished && state !== "cancelled" ? activity : undefined;
  const liveLine = backgroundWorkLine(liveWork);
  // parked by its supervisor (#3804): the row says so, until when and why, ahead of what its agent last did
  const parked = liveWork || finished || state === "cancelled" ? undefined : parkedLine(todo);
  const statusLabel = liveWork ? stateName("working") : parked ? t("assistant-tasks.review_parked") : accepted || (!todo && state === "accepted") ? t("assistant-tasks.review_accepted") : state === "cancelled" ? t("assistant-tasks.review_cancelled") : toStart ? t("assistant-tasks.plan_waiting") : TODO_STATUS_INFO[state as TodoStatus]?.label ?? t("assistant-tasks.no_activity");
  // the mark tells the state at a glance, in the colors the dock and the board use (TaskMark)
  const mark: TodoStatus = liveWork ? "working" : accepted || state === "accepted" ? "done" : state === "cancelled" ? "done" : toStart ? "needs_you" : (TODO_STATUS_INFO[state as TodoStatus] ? state as TodoStatus : "not_started");
  const ask = useStore((s) => taskAsk(todo ? taskInteraction(todo, s.interactions, task?.lead) : undefined));
  // what a waiting task waits for (#3274): its agent's own line for it
  const waits = useStore((s) => (todo && state === "waiting" ? waitingLine(todo, taskSessionLookup(s)) : undefined));
  // stopped and not done: the hub's completion check says why (#3180); an agent's open question or waiting line says more
  const why = ask || waits ? undefined : stoppedReason(todo, state);
  const stopped = why === undefined ? undefined : why ? t("assistant-tasks.progress_stopped_reason", { reason: why }) : t("assistant-tasks.progress_stopped");
  // nobody is working on it (#3618): said ahead of why it stopped and of its agent's last report
  const orphan = todo && !ask ? orphanedLine(todo) : undefined;
  const nobody = orphan && (orphan.reason ? `${orphan.lead} · ${orphan.reason}` : orphan.lead);
  const detail = finished || state === "cancelled"
    ? latestReview(todo?.supervision)?.note ?? todo?.done_reason ?? (receipt && field(receipt, "summary"))
    : liveLine ?? parked ?? waits ?? nobody ?? stopped ?? progressLine(todo?.supervision) ?? ask ?? task?.lead?.plan?.current ?? progressOf(task?.lead)?.text ?? (report && field(report, "text"));
  return (
    <button type="button" data-task-slot={card.id} disabled={!todo && !card.plan_id} onClick={() => card.todo_id ? openTask(card.todo_id) : window.dispatchEvent(new CustomEvent("persona:task-start", { detail: conversation }))} className="flex w-full min-w-0 items-center gap-3 px-3 py-2.5 text-left hover:bg-hover disabled:cursor-default">
      <span data-task-mark={mark} className="flex w-4 shrink-0 justify-center"><TaskMark status={mark} size={8} /></span>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2"><span className="truncate text-sm font-semibold" title={title}>{title}</span><span className={cx("shrink-0 text-xs", toStart || state === "needs_you" ? "text-attention" : "text-ink-2")}>{statusLabel}</span></div>
        <BackgroundWorkDetail activity={liveWork} detail={detail} pr={prNumber ? t("assistant-tasks.progress_pr", { number: prNumber }) : undefined} />
        {!finished && state !== "cancelled" && meter && <TaskProgressMeter value={meter.value} total={meter.total} blocked={blocked} title={title} />}
      </div>
      {finished ? <Icon name="check" className="shrink-0 text-green" /> : blocked ? <Icon name="info" className="shrink-0 text-orange" /> : <Icon name="chevronRight" className="shrink-0 text-ink-3" />}
    </button>
  );
}

/** Workflow counts never disappear behind an ellipsized phase, even on a narrow tray or with large text. */
function BackgroundWorkDetail({ activity, detail, pr }: { activity?: BackgroundWork; detail?: string; pr?: string }) {
  if (!activity?.workflow || !activity.workflows) return <div className="truncate text-xs text-ink-2" title={detail}>{[detail, pr].filter(Boolean).join(" · ")}</div>;
  const other = activity.tasks || activity.subagents ? backgroundWorkLine({ ...activity, workflows: 0, workflow: undefined }) : undefined;
  return <>
    <div className="flex min-w-0 items-center gap-1 text-xs text-ink-2" title={detail}>
      <span className="min-w-0 truncate">{activity.workflow.phase || t("chat-runningstatusbar.background_workflow")}</span>
      <span className="shrink-0 whitespace-nowrap tabular-nums"> · {workflowCounts(activity.workflow)}{activity.workflows > 1 ? ` · ${t("chat-runningstatusbar.more_workflows", { count: activity.workflows - 1 })}` : ""}</span>
    </div>
    {(activity.tasks || activity.subagents || pr) ? <div className="truncate text-xs text-ink-2" title={other}>{[activity.tasks || activity.subagents ? other : undefined, pr].filter(Boolean).join(" · ")}</div> : null}
  </>;
}

/** An honest count meter with a readable value and a larger hover target on its task row. */
function TaskProgressMeter({ value, total, blocked, title }: { value: number; total: number; blocked: boolean | undefined; title: string }) {
  const label = t("assistant-tasks.progress_count", { value, total });
  return (
    <div className="mt-1.5 flex items-center gap-2" title={label}>
      <div role="progressbar" aria-label={title} aria-valuemin={0} aria-valuemax={total} aria-valuenow={value} aria-valuetext={label} className="task-count-meter min-w-0 flex-1 overflow-hidden rounded-full bg-field">
        <span className={cx("block h-full origin-left rounded-full", blocked ? "bg-orange" : "bg-accent")} style={{ width: `${value / total * 100}%` }} />
      </div>
      <span className="shrink-0 text-xs text-ink-2 tabular-nums">{label}</span>
    </div>
  );
}

/** The task's line in Needs you when no card of its agent stands for it: what it waits for. */
function waitsLabel(todo: Todo, status: TodoStatus): string {
  if (todo.supervision?.state === "pending_review") return t("assistant-tasks.review_pending");
  if (todo.supervision?.state === "needs_user") return t("assistant-tasks.review_needs_user");
  return TODO_STATUS_INFO[status]?.label ?? t("assistant-tasks.review_needs_user");
}

/** One row of Needs you (lib/assistant-entries): a task or an Agent with the cards it asks, a plan to start, a task to
 *  decide on or review, a message to send. */
function NeedsYouRow({ item }: { item: NeedsYouItem }) {
  if (item.kind === "plan") return <PlanCard plan={item.plan} />;
  if (item.kind === "message") return <MessageCard item={item.item} />;
  if (item.kind === "agent") {
    const { session, asks } = item;
    const open = () => openPanel({ kind: "agent", mid: session.machine_id, sid: session.session_id });
    if (!asks.length) return <button type="button" data-needs-row={item.key} onClick={open} className="flex items-center gap-2 rounded-control bg-field px-3 py-2 text-left text-sm"><StatusMark state="needs_you" /><span className="min-w-0 flex-1 truncate">{sessionTitle(session)}</span><span className="shrink-0 text-xs text-ink-2">{stateName("needs_you")}</span></button>;
    return <div data-needs-row={item.key}><button type="button" onClick={open} className="block max-w-full truncate px-1 text-left text-sm font-semibold hover:underline">{sessionTitle(session)}</button>{asks.map((ix) => <InteractionCard key={ix.id} ix={ix} />)}</div>;
  }
  const { todo, asks, status } = item;
  if (asks.length) return <div data-needs-row={item.key}><button type="button" onClick={() => openTask(todo.id)} className="block max-w-full truncate px-1 text-left text-sm font-semibold hover:underline">{todo.title}</button>{asks.map((ix) => <InteractionCard key={ix.id} ix={ix} />)}</div>;
  const ask = openAsk(todo);
  // Both structured decisions and older review-only decisions have the same deferral controls.
  if (ask?.outcome === "decide" || todo.supervision?.state === "needs_user")
    return (
      <Suspense fallback={<div data-needs-row={item.key} className="truncate rounded-control bg-field px-3 py-2.5 text-[12px] text-ink-2">{todo.title}</div>}>
        <TaskDecision key={todo.id + (todo.supervision?.reviews?.at(-1)?.at || todo.decision_review?.review_at || ask?.asked_at)} rowKey={item.key} todo={todo} />
      </Suspense>
    );
  if (ask) return <div data-needs-row={item.key}><AcceptRow stacked key={todo.id + ask.asked_at} row={{ id: todo.id, title: todo.title, waited_s: Math.max(0, (Date.now() - Date.parse(ask.asked_at)) / 1000), outcome: ask.outcome, verdict: ask.verdict, recommend: ask.recommend, asked_at: ask.asked_at }} /></div>;
  return <button type="button" data-needs-row={item.key} onClick={() => openTask(todo.id)} className="flex items-center gap-2 rounded-control bg-field px-3 py-2 text-left text-sm"><Icon name="hand" className="shrink-0 text-attention" /><span className="min-w-0 flex-1 truncate">{todo.title}</span><span className="shrink-0 text-xs text-ink-2">{waitsLabel(todo, status)}</span></button>;
}

/** An Agent that belongs to no task, working: one row of In progress that opens its chat. */
function RunningAgentRow({ session, activity }: { session: Session; activity?: BackgroundWork }) {
  const detail = backgroundWorkLine(activity) ?? session.activity?.trim();
  return (
    <button type="button" data-running-agent={`${session.machine_id}/${session.session_id}`} onClick={() => openPanel({ kind: "agent", mid: session.machine_id, sid: session.session_id })} className="flex w-full min-w-0 items-center gap-3 px-3 py-2.5 text-left hover:bg-hover">
      <span className="flex w-4 shrink-0 justify-center"><StatusMark state="working" /></span>
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2"><span className="truncate text-sm font-semibold">{sessionTitle(session)}</span><span className="shrink-0 text-xs text-ink-2">{stateName("working")}</span></div>
        {detail && <BackgroundWorkDetail activity={activity} detail={detail} />}
      </div>
      <Icon name="chevronRight" className="shrink-0 text-ink-3" />
    </button>
  );
}

/** The conversation's two upward trays (#3387): Needs you, everything waiting on the user, and In progress, everything
 *  running that does not; each thing in one of them only (lib/assistant-entries). Finished things are on the board. */
export const AssistantActionBar = memo(function AssistantActionBar({ conversation, ownPending, ownCount = 0 }: { conversation: string; ownPending?: ReactNode; ownCount?: number }) {
  const [tray, setTrayNow] = useState<AssistantTray | null>(null);
  // the open tray filling the chat column (#4656), for going through many cards; closing or switching trays undoes it
  const [expanded, setExpanded] = useState(false);
  const setTray = (next: AssistantTray | null) => {
    setTrayNow(next);
    setExpanded(false);
  };
  const [board, setBoard] = useState(false);
  const [showShelved, setShowShelved] = useState(false);
  const trayId = useId();
  const plans = useFeed((s) => s.plans);
  const feed = useFeed((s) => s.feeds[conversation] ?? EMPTY_FEED);
  const metadata = useFeed((s) => s.taskCards[conversation] ?? EMPTY_CARDS);
  const deferred = useFeed((s) => s.later);
  const todos = useStore((s) => s.todos);
  const sessionRows = useStoreRows<Session>((s) => s.features.includes("sessions_list") ? s.sessionSummaries : Object.values(s.sessions).flat());
  const interactions = useStore((s) => s.interactions);
  const answered = useStore((s) => s.answered);
  const main = useStore((s) => s.assistant?.main);
  const lookup = useTaskLookup();
  const [restored] = useRestoredTodos();
  // a minute's tick moves tasks across the 48 h shelving line while nothing else changes, as on the To-dos page
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => setTick((n) => n + 1), 60_000);
    return () => window.clearInterval(id);
  }, []);
  const entries = useMemo(
    () => assistantEntries({ conversation, main, todos, sessions: answeredSessions(sessionRows, interactions, answered), interactions, plans, feed, cards: metadata, deferred, lookup, restored, now: Date.now() }),
    // eslint-disable-next-line react-hooks/exhaustive-deps -- tick: re-read the clock once a minute
    [conversation, main, todos, sessionRows, interactions, answered, plans, feed, metadata, deferred, lookup, restored, tick],
  );
  const isAsking = (item: NeedsYouItem) => (item.kind === "task" || item.kind === "agent") && item.asks.length > 0;
  // While background rounds fold, rows other conversations supervise are grouped under them (#4314); with the
  // switch off, or every row this conversation's, the tray is as it was.
  const foldBackground = useStore((s) => s.assistant?.main_background_folded) !== false;
  const conversations = useStore((s) => s.assistant?.conversations);
  const groups = foldBackground ? needsYouBySupervisor(entries.needsYou, conversation) : [{ supervisor: conversation, items: entries.needsYou }];
  const own = groups[0].items;
  const asking = own.filter(isAsking);
  const rest = own.filter((item) => !asking.includes(item));
  const supervisorTitle = (id: string) => {
    const row = conversations?.find((c) => c.session_id === id);
    return row ? conversationTitle(row) : t("assistant-tasks.needs_group_another");
  };
  const counts: Record<AssistantTray, number> = { needs: ownCount + entries.needsYou.length, running: entries.running.length };
  useEffect(() => {
    const needs = (event: Event) => { if ((event as CustomEvent).detail === conversation) setTray("needs"); };
    window.addEventListener("persona:task-pending", needs);
    window.addEventListener("persona:task-start", needs);
    return () => {
      window.removeEventListener("persona:task-pending", needs);
      window.removeEventListener("persona:task-start", needs);
    };
  }, [conversation]);
  const labels: Record<AssistantTray, string> = { needs: t("assistant-tasks.tray_needs_you"), running: t("assistant-tasks.tray_running") };
  const entryKeys: AssistantTray[] = ["needs", "running"];
  return (
    // shrinks with the dock (ChatView's DOCK_MAX): an open tray gives up its height and scrolls inside, instead of
    // standing taller than the dock and leaving its bottom (a card's answer row) and the entries under the composer
    <div className="flex min-h-0 shrink flex-col gap-2" data-assistant-actionbar>
      {tray && (
        // its height follows what is visible (styles.css: an open tray lets the dock take most of it); the header stays
        // and the list scrolls under it, so a plan card's Start (sticky to the list's bottom) is never under the header
        <section id={trayId} aria-label={labels[tray]} data-expanded={expanded || undefined} className="assistant-action-tray flex min-h-0 flex-col overflow-hidden rounded-card bg-surface shadow-card">
          <div className="flex shrink-0 items-center gap-2 bg-surface px-3 py-2 text-sm"><h2 className="min-w-0 flex-1 truncate font-semibold">{labels[tray]}</h2><Button onClick={() => setBoard(true)}>{t("assistant-tasks.all")}</Button><IconButton icon={expanded ? "collapse" : "expand"} label={t(expanded ? "assistant-tasks.tray_restore" : "assistant-tasks.tray_expand")} onClick={() => setExpanded((v) => !v)} /><IconButton icon="x" label={t("ui-atoms.close")} onClick={() => setTray(null)} /></div>
          <div data-tray-list className="min-h-0 grow overflow-y-auto overscroll-contain">
          {tray === "running" && <div className="divide-y divide-line">
            {entries.running.map((item) => item.kind === "task" ? <ProgressTaskRow key={item.key} card={item.card} conversation={conversation} activity={item.activity} /> : <RunningAgentRow key={item.key} session={item.session} activity={item.activity} />)}
            {!entries.running.length && <p className="px-3 py-3 text-sm text-ink-2">{t("assistant-tasks.tray_empty_progress")}</p>}
            {entries.shelved.length > 0 && (
              <button type="button" data-progress-shelved aria-expanded={showShelved} onClick={() => setShowShelved((v) => !v)} title={t("assistant-tasks.tray_shelved_hint")} className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-ink-2 hover:bg-hover">
                <Icon name={showShelved ? "chevron" : "chevronRight"} size={12} className="shrink-0" />
                <span className="whitespace-nowrap">{t("assistant-tasks.tray_shelved", { count: entries.shelved.length })}</span>
              </button>
            )}
            {showShelved && entries.shelved.map((item) => item.kind === "task" && <ProgressTaskRow key={item.key} card={item.card} conversation={conversation} activity={item.activity} />)}
          </div>}
          {tray === "needs" && <div className="flex flex-col gap-2 p-2">
            {asking.map((item) => <NeedsYouRow key={item.key} item={item} />)}
            {ownPending}
            {rest.map((item) => <NeedsYouRow key={item.key} item={item} />)}
            {groups.slice(1).map((group) => (
              <section key={group.supervisor} data-needs-supervisor={group.supervisor} aria-label={supervisorTitle(group.supervisor)} className="flex flex-col gap-2">
                <h3 className="truncate px-1 pt-1 text-xs text-ink-2">{supervisorTitle(group.supervisor)}</h3>
                {[...group.items.filter(isAsking), ...group.items.filter((item) => !isAsking(item))].map((item) => <NeedsYouRow key={item.key} item={item} />)}
              </section>
            ))}
            {!counts.needs && <p className="px-1 py-2 text-sm text-ink-2">{t("assistant-tasks.tray_empty_pending")}</p>}
          </div>}
          </div>
        </section>
      )}
      {/* The entries are the dock's old "All" / task capsules (DOCK_CAPSULE), content-wide at the leading edge; the
          open tray is the selected capsule. The scroller pads by the focus ring's width and takes it back, as the
          dock's did, so a large font scrolls sideways instead of clipping the ring. */}
      <div className="scrollbar-none -my-1 flex min-w-0 shrink-0 items-center gap-2 overflow-x-auto py-1">
        {entryKeys.map((key) => {
          const open = tray === key;
          // An empty Needs you is drawn quiet (grey) but still opens "All caught up".
          const quiet = key === "needs" && !counts.needs;
          return (
            <button
              key={key}
              type="button"
              data-tray-entry={key}
              aria-expanded={open}
              aria-controls={open ? trayId : undefined}
              onClick={() => setTray(open ? null : key)}
              className={cx(DOCK_CAPSULE, "gap-2 font-medium", open ? "bg-ink text-surface" : quiet ? "bg-field text-ink-3 hover:bg-hover" : "bg-field text-ink hover:bg-hover")}
            >
              <span aria-hidden className={cx("size-2 shrink-0 rounded-full", quiet ? "bg-ink-3" : TRAY_DOT[key])} />
              <span>{labels[key]}</span>
              {counts[key] > 0 && <span className={cx("tabular-nums", !open && "text-ink-2")}>{counts[key]}</span>}
            </button>
          );
        })}
      </div>
      {board && <Suspense fallback={null}><TaskBoard open conversation={conversation} onClose={() => setBoard(false)} /></Suspense>}
    </div>
  );
});

export const TaskDock = memo(function TaskDock({ conversation }: { conversation: string }) {
  const plans = useFeed((s) => s.plans);
  const [menuFor, setMenuFor] = useState<{ todo: Todo; el: HTMLElement } | null>(null);
  const [board, setBoard] = useState(false);
  // rows of strings and the store's own objects: a push about an agent or a card none of them shows draws nothing
  const agents = useStoreRows<DockAgent>((s) => dockAgents(answeredSessions(Object.values(s.sessions).flat(), s.interactions, s.answered), s.todos, conversation, s.assistant?.main));
  const going = useStoreRows((s) => dockTasksIn(s, conversation, plans));
  const mine = useStore((s) => s.todos.filter((t) => t.origin === conversation).length);
  if (mine === 0 && agents.length === 0) return null;
  const capsule = DOCK_CAPSULE;
  const pill = cx(capsule, "max-w-[240px] gap-2 font-semibold");
  return (
    // Top-aligned, so a scrollbar an engine still draws under the pills cannot push "All" down from them (#1381).
    <div className="flex min-w-0 items-start gap-2" data-testid="task-dock">
      <button
        type="button"
        data-testid="task-dock-board"
        aria-haspopup="dialog"
        onClick={() => setBoard(true)}
        className={cx(capsule, "bg-field font-medium text-ink-2 hover:bg-hover")}
      >
        {t("assistant-tasks.dock_all", { count: mine + agents.length })}
      </button>
      {(going.length > 0 || agents.length > 0) && (
        // The scroller pads by the focus ring's width and takes it back, so the ring is not clipped and the row
        // stays a pill high.
        <div className="scrollbar-none -my-1 flex min-w-0 flex-1 items-center gap-2 overflow-x-auto py-1">
          {going.map(({ t: todo, st, ask, limit }) => {
            // a waiting task says what it waits on when hovered or read out (#3463)
            const label = ask ? t("assistant-tasks.dock_task_asks", { title: todo.title, ask }) : limit ? `${todo.title} · ${limit}` : undefined;
            return (
            <button
              key={todo.id}
              type="button"
              title={label}
              aria-label={label}
              onClick={() => openTask(todo.id)}
              onContextMenu={(e) => {
                if (!isRemovable(st)) return;
                e.preventDefault();
                setMenuFor({ todo, el: e.currentTarget });
              }}
              className={cx(pill, st === "needs_you" || st === "interrupted" ? "bg-field text-attention" : "bg-field text-ink hover:bg-hover")}
            >
              <TaskMark status={st} size={8} />
              <span className="truncate">{todo.title}</span>
              {st === "needs_you" && <span className="shrink-0">{stateName("needs_you")}</span>}
              {st === "interrupted" && <span className="shrink-0">{stateName("interrupted")}</span>}
              {st === "usage_limit" && <span className="shrink-0">{t("lib-usage-limit.short")}</span>}
            </button>
            );
          })}
          {agents.map(({ session: s, state }) => (
            <button
              key={`${s.machine_id}/${s.session_id}`}
              type="button"
              aria-label={t("assistant-tasks.dock_agent", { title: sessionTitle(s), state: stateName(state === "needs_you" ? "needs_you" : "working") })}
              onClick={() => openPanel({ kind: "agent", mid: s.machine_id, sid: s.session_id })}
              className={cx(pill, state === "needs_you" ? "bg-field text-attention" : "bg-field text-ink hover:bg-hover")}
            >
              <StatusMark state={state} />
              <Icon name="agent" size={13} className={cx("shrink-0", state !== "needs_you" && "text-ink-2")} />
              <span className="truncate">{sessionTitle(s)}</span>
              {state === "needs_you" && <span className="shrink-0">{stateName("needs_you")}</span>}
            </button>
          ))}
        </div>
      )}
      {menuFor && (
        <Menu
          anchor={menuFor.el}
          side="top"
          onClose={() => setMenuFor(null)}
          items={[{ key: "remove", label: t("assistant-tasks.remove_ellipsis"), icon: "trash", danger: true, onSelect: () => removeTaskNow(menuFor.todo) }]}
        />
      )}
      {board && <Suspense fallback={null}><TaskBoard open conversation={conversation} onClose={() => setBoard(false)} /></Suspense>}
    </div>
  );
});

/** "Talking to "xx"" while the next message goes straight to a task; × gives it back to Persona. */
export const RouteChip = memo(function RouteChip({ conversation }: { conversation: string }) {
  const todoId = useFeed((s) => s.routes[conversation]);
  const todo = useTodo(todoId);
  if (!todo) return null;
  return (
    <div className="flex">
      <span className="inline-flex h-7 items-center gap-1.5 rounded-full bg-accent-tint pl-3 pr-1 text-[12.5px] font-semibold text-accent">
        <Icon name="send" size={12} />
        {t("assistant-tasks.say_to", { title: todo.title })}
        <button type="button" aria-label={t("assistant-tasks.give_back")} onClick={() => setRoute(conversation, null)} className="flex size-5 items-center justify-center rounded-full hover:bg-hover">
          <Icon name="x" size={11} />
        </button>
      </span>
    </div>
  );
});

export function DayLine({ date }: { date: Date }) {
  return <div className="py-1 text-center text-[12px] text-ink-3">{dayLabel(date)}</div>;
}

/** A task's state for other views (the side panel's header). */
export function useTaskState(todo?: Todo): { status: TodoStatus; lead?: Session; failure?: string } | undefined {
  const now = useTaskNow(todo);
  const plans = useFeed((s) => s.plans);
  if (!todo || !now) return undefined;
  return { ...now, failure: now.status === "failed" ? startFailure(todo, plans) : undefined };
}

export { TaskMark, place };
