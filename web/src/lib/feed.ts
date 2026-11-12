// Tasks dispatched from an assistant conversation (docs/assistant/unified-entry.md, iOS TasksStore / Shared/Tasks.swift): the plans,
// each conversation's feed (what the hub inserted into it), where those go among the chat's lines, the day lines and
// the quick results. A task is a todo whose `origin` is the conversation; its state is the todo's (lib/todos.ts).
import { useSyncExternalStore } from "react";
import { api, ApiError, enc } from "./api";
import { formatDate, t } from "../i18n";
import { parseAPIError } from "./api-errors";
import { getState, onHubEvent, putTodo, refreshTodos, todosPushed } from "./store";
import { applySnapshot } from "./assistant-views";
import type { AssistantView, ViewAction } from "./extension-wire";
import { isResumed } from "./reconnect";
import type { TaskBrief } from "./brief";
import { deleteTodo, type Todo, type TodoAsk } from "./todos";
import { getInbox, itemPath } from "./inbox";
import { BUILTIN_MANAGER, conversationManager, defaultManager, itemHeaders, MANAGER_HEADER, managerHeaderFor, routedConversationManager } from "./manager-choice";
import type { ChatLine } from "./turns";
import type { Item } from "./types";
import { upsertTaskCards, type TaskCardMetadata } from "./task-cards";

export type TaskWorkspace = "new" | "none" | { existing: string } | { new: { branch?: string } };
export interface TaskWorkspaceResolved { machine: string; path: string; branch?: string; workspace_id?: string; created?: boolean }

export interface PlanTask {
  title: string;
  prompt: string;
  machine: string;
  cwd: string;
  workspace?: TaskWorkspace;
  workspace_resolved?: TaskWorkspaceResolved;
  launcher?: string;
  model?: string;
  /** An Agent preset (#2225): a tier of the model profile in use, resolved when the task starts. */
  tier?: string;
  permission?: string;
  /** The agent starts with Claude Code's ultracode on (#12). On start, `true` / `false` overrides it; left out, the hub keeps the assistant's choice. */
  ultracode?: boolean;
  readonly?: boolean;
  why?: string;
  todo_id?: string;
  error?: string;
  /** The failed start's coded refusal and its details (as a session's start_error_code / start_error_args). */
  error_code?: string;
  error_args?: Record<string, string>;
  /** The brief the task will be held to (#3822, lib/brief.ts): the card shows it, and "Start" confirms it. Absent from
   *  a hub before briefs and on a plan made before them. */
  brief?: TaskBrief;
}

export interface TaskPlan {
  id: string;
  conversation: string;
  tasks: PlanTask[];
  state: "open" | "started" | "dismissed";
  created_at?: string;
}

export type FeedKind = "plan" | "task" | "answered" | "receipt" | "routed" | "today" | "message" | "view" | "review" | "report" | "notice" | "brief";

export interface FeedItem {
  id: string;
  conversation: string;
  at: string;
  kind: FeedKind;
  ref?: string;
  body?: Record<string, unknown>;
}

export interface TodayBody {
  date: string;
  done?: { id: string; title: string }[];
  waiting?: { id: string; title: string }[];
  pending_review?: { id: string; title: string }[];
  working?: number;
  meetings?: { start: string; end: string; title: string }[];
  at_me?: { name: string; unread?: number; preview?: string }[];
  unread?: number;
  /** What did not go through since yesterday 18:00 (#4124), newest first: the newest 8, `failed_total` all of them.
   *  A hub before it sends neither. */
  failed?: BriefFailed[];
  failed_total?: number;
}

/** `brief`: the fixed part of a morning or evening run (docs/protocol/protocol.md), as the hub worked it out when the run started. */
export interface BriefRow {
  /** The card (waiting), the to-do (accept). */
  id: string;
  title: string;
  detail?: string;
  from?: string;
  link?: string;
  waited_s: number;
  nudged?: boolean;
  /** accept: when the round's "Please verify" ask was made (with or without a Lark card); the Accept button sends it back. */
  asked_at?: string;
  /** accept: the verdict line, word for word as the Lark card says it (#1226), in the bot's language; a hub from before leaves it out. */
  verdict?: string;
  /** accept: a conclusion's verdict (left out for a deployed task); decide waits for the user's decision, and Accept takes `recommend`. */
  outcome?: TodoAsk["outcome"];
  recommend?: string;
}

export interface BriefFailed {
  kind: "task" | "routine";
  id: string;
  title: string;
  at: string;
  why?: string;
  skipped?: boolean;
}

/** How one thing that did not go through ended, in the brief and on the "Today" card: did not start, run failed, skipped. */
export function failedKey(x: BriefFailed): string {
  if (x.kind === "routine") return x.skipped ? "assistant-brief.failed_skipped" : "assistant-brief.failed_run";
  return "assistant-brief.failed_task";
}

/** "did not start · 03:12 — rpc: connection closed": how it ended, when, and why when the hub said. */
export function failedLine(x: BriefFailed): string {
  const how = t(failedKey(x), { time: formatDate(x.at, { hour: "2-digit", minute: "2-digit" }) });
  const why = x.why?.trim();
  return why ? `${how} — ${why}` : how;
}

/** The titles the "Today" card's row lists when several did not go through. */
const TODAY_FAILED_LISTED = 3;

/** The "Today" card's row for what did not go through overnight (#4124): one by its title, how it ended and why; several by
 *  how many (failed_total, past the hub's newest 8) and the first titles. `first` is the newest, what its button opens.
 *  Undefined when nothing did, or from a hub before it: the card stays as it was. */
export function todayFailedRow(body: TodayBody): { title: string; detail: string; first: BriefFailed } | undefined {
  const failed = body.failed ?? [];
  const first = failed[0];
  if (!first) return undefined;
  const count = Math.max(body.failed_total ?? failed.length, failed.length);
  if (count === 1) return { title: t("assistant-tasks.today_failed_single", { title: first.title }), detail: failedLine(first), first };
  const shown = failed.slice(0, TODAY_FAILED_LISTED);
  const titles = shown.map((x) => x.title).join(t("assistant-tasks.list_separator"));
  const more = count - shown.length;
  return { title: t("assistant-tasks.today_failed_n", { count }), detail: more > 0 ? `${titles} · ${t("assistant-tasks.more_asks", { count: more })}` : titles, first };
}

export interface BriefBody {
  title: string;
  at: string;
  hours: number;
  waiting_unknown?: boolean;
  waiting: BriefRow[];
  failed: BriefFailed[];
  failed_total: number;
  done: { id: string; title: string; at: string }[];
  done_total: number;
  accept: BriefRow[];
  /** Questions Agents left to wait for a briefing, the oldest first (`id` is the one the Handled button sends). A hub that does not know them leaves the list out. */
  later: BriefRow[];
  later_total: number;
  progress: { id: string; title: string; stage?: string; stage_kind?: Todo["stage_kind"] }[];
  progress_total: number;
}

/** A `brief` item's body, every list a list (a hub that left one out, or an item that is not one, gives nothing to draw). */
export function briefBody(it: FeedItem): BriefBody | undefined {
  const b = it.kind === "brief" ? it.body : undefined;
  if (!b || typeof b !== "object") return undefined;
  const list = <T,>(v: unknown): T[] => (Array.isArray(v) ? (v as T[]) : []);
  const waiting = list<BriefRow>(b.waiting);
  const accept = list<BriefRow>(b.accept);
  const later = list<BriefRow>(b.later);
  const failed = list<BriefFailed>(b.failed);
  const done = list<BriefBody["done"][number]>(b.done);
  const progress = list<BriefBody["progress"][number]>(b.progress);
  const num = (v: unknown, n: number) => (typeof v === "number" ? v : n);
  return {
    title: typeof b.title === "string" ? b.title : "",
    at: typeof b.at === "string" ? b.at : it.at,
    hours: num(b.hours, 12),
    waiting_unknown: b.waiting_unknown === true,
    waiting,
    failed,
    failed_total: num(b.failed_total, failed.length),
    done,
    done_total: num(b.done_total, done.length),
    accept,
    later,
    later_total: num(b.later_total, later.length),
    progress,
    progress_total: num(b.progress_total, progress.length),
  };
}

/** A string field of an item's body. */
export function field(it: FeedItem, key: string): string | undefined {
  const v = it.body?.[key];
  return typeof v === "string" ? v : undefined;
}

/** `notice`: a line the hub wrote in the user's language (#4305), e.g. a routine running again once the computer and
 *  model are ready; undefined when it has no words. */
export function feedNotice(it: FeedItem): string | undefined {
  if (it.kind !== "notice") return undefined;
  return field(it, "text")?.trim() || undefined;
}

/** `routed`: Persona wrote it to the task's agent (agent_send with a task, agent_interrupt's then, a rework; #1542), so
 *  it is drawn folded as Persona's (`SentLine`), never as the user's bubble. What the user sent has no `from`. */
export function sentByPersona(it: FeedItem): boolean {
  return it.kind === "routed" && it.body?.from === "assistant";
}

/** How long after its task's card a message of Persona's counts as the card's hand-off: agent_send with a task writes the
 *  card and, right after it, what it sent. */
export const HAND_OFF_MS = 60_000;

/** The hand-offs the task cards take in (#1542, not drawn on their own): Persona's messages within `HAND_OFF_MS` after
 *  their own task's card in the same conversation. Only a card drawn in full (from today, FeedLineView) has the list: from
 *  another day it is a TaskLine, and its hand-off stays a folded line under it (#1668). */
export function takenIn(feed: FeedItem[], now = new Date()): Set<string> {
  const cards = new Map<string, number[]>();
  for (const it of feed) {
    if (it.kind !== "task" || !it.ref || !isToday(it.at, now)) continue;
    const key = it.conversation + "/" + it.ref;
    cards.set(key, [...(cards.get(key) ?? []), Date.parse(it.at)]);
  }
  const out = new Set<string>();
  if (cards.size === 0) return out;
  for (const it of feed) {
    if (!sentByPersona(it) || !it.ref) continue;
    const at = Date.parse(it.at);
    if ((cards.get(it.conversation + "/" + it.ref) ?? []).some((c) => c <= at && at - c <= HAND_OFF_MS)) out.add(it.id);
  }
  return out;
}

/** The feed without the hand-offs the cards take in: what the chat draws among its lines. */
export function shownFeed(feed: FeedItem[], now = new Date()): FeedItem[] {
  const hidden = takenIn(feed, now);
  return hidden.size === 0 ? feed : feed.filter((it) => !hidden.has(it.id));
}

/** Persona's messages to task `todoId`, oldest first: what its card lists. */
export function sentTo(feed: FeedItem[], todoId: string): FeedItem[] {
  return feed
    .filter((it) => sentByPersona(it) && it.ref === todoId)
    .sort((a, b) => Date.parse(a.at) - Date.parse(b.at) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

/** The first line with words in it, trimmed: what a folded message shows. */
export function firstLine(text: string): string {
  for (const line of text.split(/\r?\n/)) {
    const t = line.trim();
    if (t) return t;
  }
  return "";
}

/** A receipt's outcome: an `interrupted` one (#1516), or a `done` one from a hub before it whose summary is the API
 *  error that cut the turn off, is not done. */
export function receiptState(it: FeedItem): "done" | "failed" | "interrupted" {
  const st = field(it, "state");
  if (st === "failed" || st === "interrupted") return st;
  return parseAPIError(field(it, "summary")) ? "interrupted" : "done";
}

/** How an interrupted receipt's task was dealt with, as the hub worked it out (#3834): pending until it was. */
export type InterruptResolution = "pending" | "cancelled" | "rerouted" | "resumed" | "accepted" | "done";

/** One task of a batch of interruptions (`batch.tasks`). */
export interface InterruptBatchTask {
  receipt: string;
  todo_id: string;
  title: string;
  resolution: InterruptResolution;
  at: string;
}

/** What a batch's head says of the batch: interruptions with the same cause in one chat within ten minutes. */
export interface InterruptBatch {
  cause: string;
  machine?: string;
  total: number;
  pending: number;
  resolved: number;
  tasks: InterruptBatchTask[];
}

/** How the chat draws an interrupted receipt, from what the hub sent (#3834): `hidden` inside its batch's head, `batch`
 *  the head of several, `resolved` one dealt with (a grey line), `pending` still to deal with. undefined: a hub from before
 *  it (no `resolution`), drawn as ever. */
export type InterruptLook =
  | { kind: "hidden" }
  | { kind: "batch"; batch: InterruptBatch }
  | { kind: "resolved"; resolution: Exclude<InterruptResolution, "pending"> }
  | { kind: "pending" };

const RESOLUTIONS: ReadonlySet<string> = new Set(["pending", "cancelled", "rerouted", "resumed", "accepted", "done"]);

export function interruptLook(it: FeedItem): InterruptLook | undefined {
  if (it.kind !== "receipt" || field(it, "state") !== "interrupted") return undefined;
  const resolution = field(it, "resolution");
  if (!resolution || !RESOLUTIONS.has(resolution)) return undefined;
  const group = field(it, "group");
  if (group && group !== it.id) return { kind: "hidden" };
  const batch = it.body?.batch as InterruptBatch | undefined;
  if (batch && Array.isArray(batch.tasks) && batch.tasks.length > 1) return { kind: "batch", batch };
  return resolution === "pending" ? { kind: "pending" } : { kind: "resolved", resolution: resolution as Exclude<InterruptResolution, "pending"> };
}

/** One question of an answered card and what the user answered. */
export interface AnsweredItem {
  question: string;
  answer: string;
}

/** `answered`: every question of the card with the user's answer in full, newlines kept (#1231). A record from before
 *  it has only its line (question, answer), which stands in; empty when there is nothing to show. */
export function answeredItems(it: FeedItem): AnsweredItem[] {
  const str = (v: unknown) => (typeof v === "string" ? v : "");
  const raw = it.body?.items;
  if (Array.isArray(raw)) {
    const out = raw.flatMap((x): AnsweredItem[] => {
      if (!x || typeof x !== "object") return [];
      const o = x as Record<string, unknown>;
      const one = { question: str(o.question), answer: str(o.answer) };
      return one.question || one.answer ? [one] : [];
    });
    if (out.length) return out;
  }
  const line = { question: field(it, "question") ?? "", answer: field(it, "answer") ?? "" };
  return line.question || line.answer ? [line] : [];
}

// ── state ──

interface FeedState {
  feeds: Record<string, FeedItem[]>;
  plans: Record<string, TaskPlan>;
  taskCards: Record<string, TaskCardMetadata[]>;
  /** Conversation → the task the next message goes to ("Tell "xx""). */
  routes: Record<string, string>;
  busy: Record<string, boolean>;
  /** Message cards put off with "Later". */
  later: Record<string, boolean>;
}

let st: FeedState = { feeds: {}, plans: {}, taskCards: {}, routes: {}, busy: {}, later: {} };
let feedRevision = 0;
const itemRevisions = new Map<string, number>();
const draftOwners = new WeakMap<FeedItem, string>();
const feedKey = (item: FeedItem) => `${item.conversation}/${item.id}`;
const currentCard = (card: FeedItem) => st.feeds[card.conversation]?.find((it) => it.id === card.id) ?? card;
const listeners = new Set<() => void>();
function set(patch: Partial<FeedState>) {
  st = { ...st, ...patch };
  listeners.forEach((l) => l());
}

export function useFeed<T>(select: (s: FeedState) => T): T {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => select(st),
    () => select(st), // static rendering (tests)
  );
}

export function getFeedState() {
  return st;
}

/** Puts items into a conversation's feed, oldest first, without repeats; one it has whose body changed (a message
 *  card sent) takes the new body. */
export function mergeInto(list: FeedItem[], items: FeedItem[]): FeedItem[] {
  const have = new Map(list.map((i) => [i.id, i]));
  const changed = new Map(items.filter((i) => have.has(i.id) && JSON.stringify(i.body) !== JSON.stringify(have.get(i.id)!.body)).map((i) => [i.id, i]));
  const fresh = items.filter((i) => !have.has(i.id));
  if (!fresh.length && !changed.size) return list;
  const kept = changed.size ? list.map((i) => changed.get(i.id) ?? i) : list;
  if (!fresh.length) return kept;
  return [...kept, ...fresh].sort((a, b) => (a.at === b.at ? a.id.localeCompare(b.id) : Date.parse(a.at) - Date.parse(b.at)));
}

/** A message card whose reply went out — from this card or from the inbox, on any device (the hub records it on the
 *  card): when, and what it said (a card sent before the hub recorded it: its draft). */
export function cardSent(it: FeedItem): { at: string; text: string } | undefined {
  const at = field(it, "sent_at");
  return at ? { at, text: field(it, "sent") || field(it, "draft") || "" } : undefined;
}

/** Handled elsewhere, not from this card (#4797): the hub's filed_at; a sent card is never filed. */
export function cardFiled(it: FeedItem): { at: string } | undefined {
  const at = field(it, "filed_at");
  return at && !field(it, "sent_at") ? { at } : undefined;
}

/** The user dismissed this exact draft, without sending it or completing its task. */
export function cardDismissed(it: FeedItem): { at: string; id: string } | undefined {
  const at = field(it, "dismissed_at");
  const id = field(it, "dismissal_id");
  return at && !cardSent(it) ? { at, id: id ?? "" } : undefined;
}

/** A blank a draft leaves to fill in (a phrase in full-width lenticular brackets, {{link}}): it would go out as it is. */
export function draftPlaceholder(text: string): string | undefined {
  return /【[^】\n]*】|\{\{[^}\n]*\}\}/.exec(text)?.[0];
}

function received(items: FeedItem[], plans?: Record<string, TaskPlan>) {
  const feeds = { ...st.feeds };
  // one merge per conversation: merging item by item re-sorted the whole feed once per item of a 500-item page
  const byConversation = new Map<string, Map<string, FeedItem>>();
  for (const it of items) {
    if (it.kind === "message") {
      const before = feeds[it.conversation]?.find((old) => old.id === it.id);
      const owner = draftOwners.get(it);
      if (before && owner) draftOwners.set(before, owner);
      if (!before || JSON.stringify(before.body) !== JSON.stringify(it.body)) itemRevisions.set(feedKey(it), ++feedRevision);
    }
    const group = byConversation.get(it.conversation) ?? new Map<string, FeedItem>();
    group.set(it.id, it); // a later copy of an id wins, as merging one by one did
    byConversation.set(it.conversation, group);
  }
  for (const [c, group] of byConversation) feeds[c] = mergeInto(feeds[c] ?? [], [...group.values()]);
  set({ feeds, plans: plans ? { ...st.plans, ...plans } : st.plans });
  if (items.some((i) => i.kind === "task" || i.kind === "receipt")) todosPushed();
}

const loading = new Set<string>();
/** Conversations asked for again while their read was out: that read's answer may predate what was asked for. */
const again = new Set<string>();
const todayAsked = new Map<string, string>();

/** The most items the hub answers at once (docs/protocol/protocol.md: oldest first, at most 500): a full page has more after it. */
const FEED_PAGE = 500;
/** Pages read in one load at most. */
const FEED_PAGES = 40;

/** A conversation's feed: what came since the newest item it has. A read asked for while one is out runs once more
 *  after it (a reconnect's hello during the first read on open was dropped, and what was pushed meanwhile stayed missing). */
export async function loadFeed(conversation: string) {
  if (loading.has(conversation)) {
    again.add(conversation);
    return;
  }
  loading.add(conversation);
  try {
    do {
      again.delete(conversation);
      await readFeedPages(conversation);
    } while (again.has(conversation));
  } catch {
    // an older hub has no feed: the conversation shows as before
  } finally {
    again.delete(conversation);
    loading.delete(conversation);
  }
}

/** One bounded feed traversal. New hubs return a snapshot cursor; older hubs retain the timestamp fallback. */
async function readFeedPages(conversation: string) {
  let last = st.feeds[conversation]?.at(-1)?.at;
  let cursor: string | undefined;
  const endpoint = `/assistant/conversations/${enc(conversation)}/feed`;
  // Capture an authoritative owner, when known, for the whole traversal. An unknown conversation can still be
  // routed by the hub itself; never stamp a guessed default onto cards arriving before their session metadata.
  const owner = conversationManager(conversation)
    ?? Object.values(getState().sessions).flat().find((s) => s.session_id === conversation)?.manager
    ?? managerHeaderFor(endpoint);
  // the default's conversation names none (itemHeaders): one manager gets the requests it always did
  const headers = itemHeaders(owner);
  for (let n = 0; n < FEED_PAGES; n++) {
    const readingAt = feedRevision;
    const query = cursor ? `?cursor=${enc(cursor)}` : `?paginate=1${last ? `&since=${enc(last)}` : ""}`;
    const res = await api<{ items: FeedItem[]; next_cursor?: string; plans?: Record<string, TaskPlan>; task_cards?: TaskCardMetadata[] | null; views?: Record<string, AssistantView>; view_actions?: Record<string, ViewAction>; view_blocks?: string[] }>(endpoint + query, { headers });
    if (!st.feeds[conversation]) set({ feeds: { ...st.feeds, [conversation]: [] } });
    const items = res.items ?? [];
    if (owner) for (const it of items) draftOwners.set(it, owner);
    // A write or push accepted after this read started wins over the captured response.
    received(items.filter((it) => (itemRevisions.get(feedKey(it)) ?? 0) <= readingAt), res.plans ?? undefined);
    if (res.task_cards?.length) set({ taskCards: { ...st.taskCards, [conversation]: upsertTaskCards(st.taskCards[conversation] ?? [], res.task_cards) } });
    // the cards' snapshot comes whole with every page: what an event missed comes back here
    applySnapshot(conversation, res);
    if (typeof res.next_cursor === "string") {
      if (!res.next_cursor || res.next_cursor === cursor || !items.length) break;
      cursor = res.next_cursor;
      continue;
    }
    const newest = items.at(-1)?.at;
    // Compatibility: a hub without cursors advances by its last original timestamp.
    if (items.length < FEED_PAGE || !newest || (last && Date.parse(newest) <= Date.parse(last))) break;
    last = newest;
  }
}

/** The main conversation's "Today" card, once a day (the hub makes it on the first ask). */
export async function loadToday(conversation: string) {
  const day = new Date().toLocaleDateString("sv");
  if (todayAsked.get(conversation) === day) return;
  todayAsked.set(conversation, day);
  try {
    const res = await api<{ item: FeedItem }>(`/assistant/conversations/${enc(conversation)}/today`, { method: "POST" });
    if (res.item) received([res.item]);
  } catch {
    todayAsked.delete(conversation);
  }
}

onHubEvent("assistant_feed", (m) => {
  const it = m.item as FeedItem | undefined;
  if (it) {
    draftOwners.set(it, typeof m.manager === "string" ? m.manager : BUILTIN_MANAGER);
    received([it]);
  }
});
// A connection that did not resume missed events: the conversations open here read their feed (and cards) again.
onHubEvent("hello", (m) => {
  if (isResumed(m)) return;
  for (const c of Object.keys(st.feeds)) void loadFeed(c);
});
onHubEvent("task_plan", (m) => {
  const p = m.plan as TaskPlan | undefined;
  if (p) set({ plans: { ...st.plans, [p.id]: p } });
});

// ── actions ──

/** The fields of a plan's task "Edit" lets the user change before it starts (iOS PlanEditSheet), ultracode included (#12). */
export type PlanTaskEdit = Pick<PlanTask, "title" | "machine" | "cwd" | "model" | "permission" | "prompt" | "workspace"> & { ultracode: boolean };

export const PLAN_PERMISSIONS = ["default", "acceptEdits", "plan", "auto", "bypassPermissions"] as const;

/** A plan's tasks as "Edit" starts them: every editable field filled in (an unset permission is "Ask every step").
 *  `ultracode`: the card's own choice per task, where it has one; else the assistant's. */
export function planEditsOf(plan: TaskPlan, ultracode?: readonly boolean[]): PlanTaskEdit[] {
  return plan.tasks.map((t, i) => ({
    title: t.title,
    machine: t.machine,
    cwd: t.cwd,
    model: t.model ?? "",
    permission: t.permission || "default",
    prompt: t.prompt,
    ultracode: ultracode?.[i] ?? t.ultracode === true,
    ...(t.workspace !== undefined ? { workspace: t.workspace } : {}),
  }));
}

/** Whether "Edit" changed anything since it opened with `before` (closing it then asks before throwing the changes away). */
export function planEditsChanged(before: PlanTaskEdit[], edits: PlanTaskEdit[]): boolean {
  return edits.some((e, i) => (Object.keys(e) as (keyof PlanTaskEdit)[]).some((k) => JSON.stringify(e[k] ?? "") !== JSON.stringify(before[i]?.[k] ?? "")));
}

/** Whether a computer can't start an agent with ultracode (lib/ultracode machineStartBlocked); a task chosen with it
 *  there starts without. */
export type UltracodeBlocked = (machine: string) => boolean;

/** A task's ultracode as Start sends it (#12): the choice, off on a computer that can't; undefined when that is the
 *  assistant's own (left out, the hub keeps it). */
function ultracodeToSend(planned: PlanTask | undefined, on: boolean, machine: string, blocked: UltracodeBlocked): boolean | undefined {
  const sent = on && !blocked(machine);
  return sent === (planned?.ultracode === true) ? undefined : sent;
}

/** What "Start" sends from "Edit": the edited tasks, leaving out an "Ask every step" the plan never set (the agent's own
 *  default) and an ultracode the same as the assistant's. */
export function planEditsToSend(plan: TaskPlan, edits: PlanTaskEdit[], blocked: UltracodeBlocked = () => false): Partial<PlanTask>[] {
  return edits.map((e, i) => {
    const out: Partial<PlanTask> = { ...e, ultracode: ultracodeToSend(plan.tasks[i], e.ultracode, e.machine, blocked) };
    if (e.permission === "default" && !plan.tasks[i]?.permission) delete out.permission;
    if (out.ultracode === undefined) delete out.ultracode;
    return out;
  });
}

/** What the card's own "Start" sends (#12): `ultracode` for each task whose choice, as sent, differs from the
 *  assistant's and nothing for the rest; undefined when nothing differs, so it starts the plan as it is. */
export function planUltracodeToSend(plan: TaskPlan, ultracode: readonly boolean[], blocked: UltracodeBlocked = () => false): Partial<PlanTask>[] | undefined {
  const edits = plan.tasks.map((t, i): Partial<PlanTask> => {
    const sent = ultracodeToSend(t, ultracode[i], t.machine, blocked);
    return sent === undefined ? {} : { ultracode: sent };
  });
  return edits.some((e) => "ultracode" in e) ? edits : undefined;
}

/**
 * The header naming the manager of conversation, for what its feed offers (a plan, a briefing's question): the routes
 * name no conversation, so the hub would take them to the default manager (docs/protocol/protocol.md "Per-manager
 * routing"), whichever assistant the conversation is (another made current, or another's conversation opened here).
 * The manager a page noted for it, else the one the hub's sessions list has it under, else the one its address named; none for the default's.
 */
export function conversationHeaders(conversation: string): Record<string, string> | undefined {
  const noted = conversationManager(conversation);
  if (noted) return itemHeaders(noted);
  for (const list of Object.values(getState().sessions)) {
    const s = list.find((x) => x.session_id === conversation);
    if (s) return itemHeaders(s.manager);
  }
  const routed = routedConversationManager(conversation);
  return routed ? itemHeaders(routed) : undefined;
}

export async function startPlan(plan: TaskPlan, remember: boolean, edits?: Partial<PlanTask>[]) {
  if (st.busy[plan.id]) return;
  set({ busy: { ...st.busy, [plan.id]: true } });
  try {
    const res = await api<{ plan: TaskPlan }>(`/task-plans/${enc(plan.id)}/start`, { method: "POST", json: { tasks: edits, remember }, headers: conversationHeaders(plan.conversation) });
    set({ plans: { ...st.plans, [res.plan.id]: res.plan } });
    void refreshTodos();
  } finally {
    set({ busy: { ...st.busy, [plan.id]: false } });
  }
}

export async function dismissPlan(plan: TaskPlan) {
  if (st.busy[plan.id]) return;
  set({ busy: { ...st.busy, [plan.id]: true } });
  try {
    const res = await api<{ plan: TaskPlan }>(`/task-plans/${enc(plan.id)}/dismiss`, { method: "POST", headers: conversationHeaders(plan.conversation) });
    set({ plans: { ...st.plans, [res.plan.id]: res.plan } });
  } finally {
    set({ busy: { ...st.busy, [plan.id]: false } });
  }
}

/** The busy key of a task's "Retry" (busy is keyed by plan id otherwise). */
export const retryKey = (todoId: string) => `retry:${todoId}`;

/**
 * "Retry" on a task whose agent did not start (#1396): the hub starts the same task again (its plan's machine, directory,
 * prompt, launcher, model and permission) in the same todo, which it reopens at once — the card shows it starting
 * where it was. Taps while one is on its way do nothing; the hub starts one agent however many arrive. On a task the
 * model service cut off (#1516) it is "Continue": the hub tells its agent to go on, once.
 */
export async function retryTask(todo: Todo) {
  const key = retryKey(todo.id);
  if (st.busy[key]) return;
  set({ busy: { ...st.busy, [key]: true } });
  try {
    const res = await api<{ todo: Todo }>(`/todos/${enc(todo.id)}/retry`, { method: "POST" });
    if (res.todo) putTodo(res.todo);
    void refreshTodos();
  } finally {
    set({ busy: { ...st.busy, [key]: false } });
  }
}

/** How a decision is answered other than by taking its recommendation (#3804): one of its options by its label, or one
 *  of the fixed responses that say the owner cannot answer it as asked. */
export type AskAnswer = { option: string } | { response: string };

/** The Accept button of a `brief` item's task to verify: "accepted", or "handled" when the hub says that round is over
 *  (asked again since, or already verified). Any other failure throws. `answer`: a decision's option or fixed response
 *  instead of its recommendation. */
export async function acceptBrief(todoId: string, askedAt: string, answer?: AskAnswer): Promise<"accepted" | "handled"> {
  try {
    await api(`/todos/${enc(todoId)}/accept`, { method: "POST", json: { asked_at: askedAt, ...answer } });
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) return "handled";
    throw e;
  }
  void refreshTodos();
  return "accepted";
}

/** The Handled button of a question in a `brief` item's `later` list: "marked", or "handled" when the hub says it is not in
 *  the queue any more (marked before, its to-do ended, or 7 days). Any other failure throws. */
export async function handledLater(id: string, conversation: string): Promise<"marked" | "handled"> {
  try {
    await api(`/later/${enc(id)}/handled`, { method: "POST", headers: conversationHeaders(conversation) });
  } catch (e) {
    if (e instanceof ApiError && e.status === 409) return "handled";
    throw e;
  }
  return "marked";
}

/**
 * Why a task's agent did not start: the todo's own word (#1077), else — from a hub before it, which left the todo open
 * — its plan's, while it has no agent. Undefined while it may still start.
 */
export function startFailure(todo: Todo, plans: Record<string, TaskPlan>): string | undefined {
  if (todo.start_error) return todo.start_error;
  if ((todo.agents ?? []).length > 0 || !todo.plan_id) return undefined;
  return plans[todo.plan_id]?.tasks.find((t) => t.todo_id === todo.id)?.error || undefined;
}

/**
 * Removes a task that does not run: its todo goes (its agents and their chats stay on the Agents page), and with it
 * its card, pill and panel. One whose agents ran asks first; one whose agent never started goes at once. Answers
 * whether it went.
 */
export async function removeTask(todo: Todo, ask: (message: string) => boolean = (m) => confirm(m)): Promise<boolean> {
  if ((todo.agents ?? []).length > 0 && !ask(t("lib-feed.remove_confirm", { title: todo.title }))) return false;
  await deleteTodo(todo.id);
  const routes = { ...st.routes };
  for (const [c, id] of Object.entries(routes)) if (id === todo.id) delete routes[c];
  set({ routes });
  await refreshTodos();
  return true;
}

export function setRoute(conversation: string, todoId: string | null) {
  const routes = { ...st.routes };
  if (todoId) routes[conversation] = todoId;
  else delete routes[conversation];
  set({ routes });
}

/** Sends text straight to the task the conversation is routed to; false when none (send it to Persona as usual). */
export async function sendRouted(conversation: string, text: string): Promise<boolean> {
  const todoId = st.routes[conversation];
  if (!todoId) return false;
  // A failed send keeps the route for the retry, so a task gone elsewhere (its chip no longer drawn) must not hold it.
  const { todos, todosLoaded } = getState();
  if (todosLoaded && !todos.some((t) => t.id === todoId)) {
    setRoute(conversation, null);
    return false;
  }
  const res = await api<{ item: FeedItem }>(`/assistant/conversations/${enc(conversation)}/route`, { method: "POST", json: { todo_id: todoId, text } });
  if (st.routes[conversation] === todoId) setRoute(conversation, null);
  if (res.item) received([res.item]);
  return true;
}

/**
 * "Send" on a message card: the (edited) draft goes out through the inbox, which files the item as done and records it
 * on the card (the hub sends once per card, however many taps or devices). The card shows it sent once the hub says
 * so; taps while it is on its way do nothing. A draft with a blank to fill in is not sent.
 */
export async function sendMessage(card: FeedItem, text: string) {
  const itemId = card.ref;
  const latest = () => st.feeds[card.conversation]?.find((i) => i.id === card.id) ?? card;
  // Handled elsewhere (#5095): the hub would refuse it, so nothing goes out.
  if (!itemId || st.busy[card.id] || cardSent(latest()) || cardFiled(latest()) || cardDismissed(latest())) return;
  const blank = draftPlaceholder(text);
  if (blank) throw new Error(t("lib-feed.draft_blank", { blank }));
  set({ busy: { ...st.busy, [card.id]: true } });
  try {
    // the item is in the inbox of the card's conversation's manager (the one its page noted), else of the one manager
    // whose list has that id (docs/assistant/managers.md "Clients"); none: the default's
    const listed = getInbox().items.filter((it) => it.id === itemId);
    const manager = conversationManager(card.conversation) ?? (listed.length === 1 ? listed[0].manager : undefined);
    await api(`${itemPath(itemId)}/reply`, { method: "POST", json: { text, card: card.id }, headers: draftHeaders(card, manager) });
    // The hub's assistant_feed event brings the card as it recorded it; this draws it sent without waiting.
    const now = latest();
    if (!cardSent(now)) received([{ ...now, body: { ...now.body, sent_at: new Date().toISOString(), sent: text } }]);
    return true;
  } finally {
    set({ busy: { ...st.busy, [card.id]: false } });
  }
}

function draftHeaders(card: FeedItem, fallback?: string): Record<string, string> {
  const owner = draftOwners.get(currentCard(card)) ?? draftOwners.get(card) ?? conversationManager(card.conversation)
    ?? Object.values(getState().sessions).flat().find((s) => s.session_id === card.conversation)?.manager ?? fallback ?? defaultManager();
  // Include the default explicitly: otherwise api() inserts a later assistant selection.
  return { [MANAGER_HEADER]: owner };
}

function applyDraftReceipt(card: FeedItem, beforeBody: string | undefined, item: FeedItem, owner: string) {
  if (JSON.stringify(currentCard(card).body) !== beforeBody) return;
  draftOwners.set(item, owner);
  received([item]);
}

/** Resolve only this draft on the hub. Send and dismiss share a synchronous admission guard. */
export async function dismissMessage(card: FeedItem): Promise<string | undefined> {
  const latest = st.feeds[card.conversation]?.find((it) => it.id === card.id) ?? card;
  if (card.kind !== "message" || st.busy[card.id] || cardSent(latest) || cardFiled(latest) || cardDismissed(latest)) return;
  const headers = draftHeaders(card);
  const beforeBody = JSON.stringify(currentCard(card).body);
  set({ busy: { ...st.busy, [card.id]: true } });
  try {
    const result = await api<{ item: FeedItem; dismissal_id: string }>(`/assistant/feed/${enc(card.id)}/dismiss`, {
      method: "POST", json: { draft: field(card, "draft") ?? "" }, headers,
    });
    applyDraftReceipt(card, beforeBody, result.item, headers[MANAGER_HEADER]);
    return result.dismissal_id;
  } finally {
    set({ busy: { ...st.busy, [card.id]: false } });
  }
}

/** Undo names the dismissal receipt, so a newer draft or decision cannot be overwritten. */
export async function undoDismissMessage(card: FeedItem, dismissalId: string): Promise<boolean> {
  if (!dismissalId || st.busy[card.id]) return false;
  const headers = draftHeaders(card);
  const beforeBody = JSON.stringify(currentCard(card).body);
  set({ busy: { ...st.busy, [card.id]: true } });
  try {
    const result = await api<{ item: FeedItem }>(`/assistant/feed/${enc(card.id)}/undo-dismiss`, {
      method: "POST", json: { dismissal_id: dismissalId }, headers,
    });
    applyDraftReceipt(card, beforeBody, result.item, headers[MANAGER_HEADER]);
    const now = currentCard(card);
    return !cardDismissed(now) && !cardSent(now) && !cardFiled(now);
  } finally {
    set({ busy: { ...st.busy, [card.id]: false } });
  }
}

export function later(itemId: string) {
  set({ later: { ...st.later, [itemId]: true } });
}

// ── the conversation's lines ──

export interface QuickResult {
  kind: "reminder" | "event";
  callId: string;
  title: string;
  when?: string;
  place?: string;
}

export type DisplayLine =
  | { kind: "chat"; id: string; line: ChatLine }
  | { kind: "feed"; id: string; item: FeedItem }
  | { kind: "quick"; id: string; result: QuickResult }
  | { kind: "day"; id: string; date: Date };

/** When the user sent the line's message (feed items go before the first message sent after them). */
function inputTime(line: ChatLine): number | undefined {
  if (line.kind === "supervision") return line.lines.map(inputTime).find((at) => at !== undefined);
  if (line.kind !== "segment" || !line.seg.user?.ts) return undefined;
  return Date.parse(line.seg.user.ts);
}

/** When a line happened, for the day lines. */
function lineTime(l: DisplayLine): number | undefined {
  switch (l.kind) {
    case "feed":
      return Date.parse(l.item.at);
    case "chat": {
      const c = l.line;
      if (c.kind === "supervision") return inputTime(c);
      if (c.kind === "segment") return Date.parse(c.seg.user?.ts ?? c.seg.start ?? "") || undefined;
      if (c.kind === "notice" || c.kind === "apiError" || c.kind === "compact") return Date.parse(c.item.ts ?? "") || undefined;
      return undefined;
    }
    default:
      return undefined;
  }
}

/** A reminder or an event the assistant added on the phone, from its tool call. */
export function quickResult(use: Item, result?: Item): QuickResult | undefined {
  const short = (use.tool_name ?? "").split("__").at(-1);
  const kind = short === "phone_reminders_create" ? "reminder" : short === "phone_calendar_create_event" ? "event" : undefined;
  if (!kind || !result || result.is_error || !result.text) return undefined;
  try {
    const o = JSON.parse(result.text) as Record<string, unknown>;
    if (typeof o.id !== "string" || !o.id) return undefined;
    const when = kind === "reminder" ? o.due : o.start;
    const place = kind === "reminder" ? o.list : o.calendar;
    return { kind, callId: use.id, title: String(o.title ?? ""), when: typeof when === "string" ? when : undefined, place: typeof place === "string" ? place : undefined };
  } catch {
    return undefined;
  }
}

/** When the oldest loaded transcript item happened (displayLines `from`); undefined when none says. */
export function loadedFrom(items: Item[]): number | undefined {
  for (const it of items) {
    const at = Date.parse(it.ts ?? "");
    if (!Number.isNaN(at)) return at;
  }
  return undefined;
}

/** The chat's lines with the feed among them (each item at the end of the exchange it happened in), each exchange's
 *  quick results after it, and a day line over the first line of each day. `from`: while older transcript pages are
 *  not loaded yet, the time of the oldest loaded item. The feed always holds the whole history, so its older items would
 *  stack on top of the newest page, with their day lines, and read as the conversation's start with its messages gone;
 *  they wait for the page that covers them. */
export function displayLines(lines: ChatLine[], all: FeedItem[], from?: number): DisplayLine[] {
  // the hand-offs the task cards take in are drawn there, not among the lines (#1542)
  const feed = shownFeed(all).filter((it) => from === undefined || !(Date.parse(it.at) < from));
  const out: DisplayLine[] = [];
  let next = 0;
  for (const line of lines) {
    const at = inputTime(line);
    if (at !== undefined) {
      while (next < feed.length && Date.parse(feed[next].at) < at) {
        out.push({ kind: "feed", id: "feed-" + feed[next].id, item: feed[next] });
        next++;
      }
    }
    out.push({ kind: "chat", id: line.id, line });
    if (line.kind === "segment") {
      for (const step of line.seg.steps) {
        if (step.kind !== "tool") continue;
        const q = quickResult(step.use, step.result);
        if (q) out.push({ kind: "quick", id: "quick-" + q.callId, result: q });
      }
    }
  }
  for (; next < feed.length; next++) out.push({ kind: "feed", id: "feed-" + feed[next].id, item: feed[next] });
  return withDays(out);
}

/** The local day of a time, as a day line takes it (`withDays`). */
export function startOfDay(t: number): Date {
  const d = new Date(t);
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

/** A day line over the first line of each later day. The lines keep their order, which is not always the order of their
 *  times (an exchange's feed items, a clock that was behind): a line from a day already passed gets no line of its own,
 *  so the days only go forward and each day line (`day-<date>`) is there once. */
export function withDays(lines: DisplayLine[], now = Date.now()): DisplayLine[] {
  const out: DisplayLine[] = [];
  let last = "";
  for (const l of lines) {
    const t = lineTime(l);
    // a day line only forward (a line from a clock that was behind is not a day back, nor the same day twice), and none
    // from a line dated after now (a clock ahead would take every day after it with it)
    if (t !== undefined && !Number.isNaN(t) && t <= now + 60_000) {
      const d = new Date(t);
      // YYYY-MM-DD: compares as a string
      const day = d.toLocaleDateString("sv");
      if (day > last) {
        out.push({ kind: "day", id: "day-" + day, date: new Date(d.getFullYear(), d.getMonth(), d.getDate()) });
        last = day;
      }
    }
    out.push(l);
  }
  return out;
}

/** "Today · Tue, Nov 3", "Yesterday · …", or the date. */
export function dayLabel(date: Date, now = new Date()): string {
  const day = formatDate(date, { month: "long", day: "numeric", weekday: "short" });
  const same = (a: Date, b: Date) => a.toDateString() === b.toDateString();
  if (same(date, now)) return t("lib-feed.today", { day });
  const y = new Date(now);
  y.setDate(now.getDate() - 1);
  if (same(date, y)) return t("lib-feed.yesterday", { day });
  if (date.getFullYear() !== now.getFullYear()) return formatDate(date, { year: "numeric", month: "long", day: "numeric", weekday: "short" });
  return day;
}

export function isToday(iso: string, now = new Date()): boolean {
  return new Date(iso).toDateString() === now.toDateString();
}
