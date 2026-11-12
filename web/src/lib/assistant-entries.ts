import { backgroundWork, taskBackgroundWork, type BackgroundWork } from "./background-work";
import { cardDismissed, cardFiled, cardSent, startFailure, type FeedItem, type TaskPlan } from "./feed";
import { cardBucket, taskCardSlots, type TaskCardMetadata } from "./task-cards";
import { reviewState } from "./supervision";
import { supersededFailedStarts } from "./todo-triage";
import { dockAgents, makeSessionLookup, openAsk, todoRank, todoStatus, type SessionLookup, type Todo, type TodoStatus } from "./todos";
import { byTime } from "./times";
import { homeKey } from "./folded-rows";
import type { Interaction, Session } from "./types";

/**
 * The Assistant's two entries above the composer (#3387, docs/assistant/unified-entry.md "Needs you and In progress"):
 * Needs you lists everything waiting on the user, In progress everything running that does not. Each thing is one row in
 * exactly one of them, Needs you first; finished things are in neither (the board has them), and each entry's count is
 * its rows. A task its supervisor parked is In progress, saying until when and why; it is never in Needs you for that. A task is one row whatever its agents: they are not listed on their own. In the main conversation an
 * Agent that belongs to no task (opened by hand) is a row of its own.
 */
export type NeedsYouItem =
  /** A task whose agent asks something (`asks`, oldest first), or that waits for the user's decision or review. */
  | { kind: "task"; key: string; todo: Todo; status: TodoStatus; asks: Interaction[] }
  /** An Agent that belongs to no task, asking something. */
  | { kind: "agent"; key: string; session: Session; asks: Interaction[] }
  /** A plan waiting for Start. */
  | { kind: "plan"; key: string; plan: TaskPlan }
  /** A message card waiting for Send. */
  | { kind: "message"; key: string; item: FeedItem };

export type RunningItem =
  /** A dispatched task not finished and not waiting on the user. */
  | { kind: "task"; key: string; card: TaskCardMetadata; todo: Todo; status: TodoStatus; activity?: BackgroundWork }
  /** A working Agent that belongs to no task. */
  | { kind: "agent"; key: string; session: Session; activity?: BackgroundWork };

export interface AssistantEntries {
  needsYou: NeedsYouItem[];
  running: RunningItem[];
  /** Open tasks with no progress for 48 hours and nothing running (the To-dos page's shelved, #3331): folded under
   *  In progress, not counted. */
  shelved: RunningItem[];
}

export interface AssistantEntriesInput {
  conversation: string;
  /** The main conversation's id: it watches every task and the Agents opened by hand. */
  main?: string;
  todos: Todo[];
  /** Every session, as the lists show them (answered cards applied). */
  sessions: Session[];
  /** Pending cards. */
  interactions: Interaction[];
  plans: Record<string, TaskPlan>;
  /** The conversation's feed. */
  feed: FeedItem[];
  /** The hub's task card slots for the conversation (`task_cards`). */
  cards?: TaskCardMetadata[];
  /** Message cards and task-decision rounds the user put off locally (`later`). */
  deferred?: Record<string, unknown>;
  /** Looks up a task's agents; defaults to `sessions`. */
  lookup?: SessionLookup;
  now?: number;
  /** Tasks the user brought back from shelved, by id: when (ms). */
  restored?: ReadonlyMap<string, number>;
}

/** Task states that wait on the user: an agent asks, or its turn broke off on an error (a failed start is below). */
const WAITS_ON_YOU: ReadonlySet<TodoStatus> = new Set(["needs_you", "interrupted"]);

const sessionKey = (mid: string, sid: string) => `${mid}/${sid}`;

/** A task's status, failed too when only its plan says the start failed (a hub from before #1077). */
export function entryStatus(todo: Todo, lookup: SessionLookup, plans: Record<string, TaskPlan>): TodoStatus {
  const st = todoStatus(todo, lookup);
  return st === "not_started" && startFailure(todo, plans) ? "failed" : st;
}

/** The task waits for the user's decision or review: an open ask (a decided one only while the task is open), or its
 *  supervisor's `pending_review` / `needs_user` on an open task. */
export function taskAwaitsDecision(todo: Todo): boolean {
  const ask = openAsk(todo);
  if (ask) return !(todo.done_at && ask.outcome === "decide");
  return !todo.done_at && (todo.supervision?.state === "pending_review" || todo.supervision?.state === "needs_user");
}

/** The message card still waits for Send: not sent, filed, dismissed, or put off. */
export function messageWaits(it: FeedItem, deferred: Record<string, unknown> = {}): boolean {
  return it.kind === "message" && !cardSent(it) && !cardFiled(it) && !cardDismissed(it) && !deferred[it.id];
}

/** The review round, not the task id alone: a later decision must remain visible. */
export function decisionRound(todo: Todo): string {
  return todo.supervision?.reviews?.at(-1)?.at || todo.decision_review?.review_at || todo.ask?.asked_at || "";
}

export function decisionLaterKey(todo: Todo): string {
  return `decision:${todo.manager || ""}:${todo.id}:${decisionRound(todo)}`;
}

export function assistantEntries(input: AssistantEntriesInput): AssistantEntries {
  const { conversation, main, todos, sessions, interactions, plans, feed } = input;
  const now = input.now ?? Date.now();
  const lookup = input.lookup ?? makeSessionLookup(sessions);
  const isMain = !!main && main === conversation;
  const byTodo = new Map(todos.map((todo) => [todo.id, todo]));
  // failed starts whose work finished in a related to-do: closed, as the To-dos page has them (#3713)
  const superseded = supersededFailedStarts(todos);
  const asksOf = new Map<string, Interaction[]>();
  for (const ix of [...interactions].sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))) {
    // a child session's card is asked under its parent (#5120): the parent's task or Agent row holds it
    const k = homeKey(ix);
    asksOf.set(k, [...(asksOf.get(k) ?? []), ix]);
  }

  const asking: NeedsYouItem[] = [];
  const deciding: { item: NeedsYouItem; rank: number; at: string }[] = [];
  const open: { item: RunningItem; rank: number; at: string }[] = [];
  const shelved: { item: RunningItem; at: string }[] = [];
  const folded = new Set<string>();
  for (const card of taskCardSlots(conversation, input.cards ?? [], feed, plans, todos, isMain)) {
    const todo = card.todo_id ? byTodo.get(card.todo_id) : undefined;
    // a plan task with no to-do yet is its plan's row (below); a to-do the hub no longer sends is finished
    if (!todo) continue;
    const status = entryStatus(todo, lookup, plans);
    const active = todo.done_at ? [] : (todo.agents ?? []).filter((a) => !a.released_at);
    const asks = active.flatMap((a) => asksOf.get(sessionKey(a.machine_id, a.session_id)) ?? []);
    const key = `task:${todo.id}`;
    const bucket = cardBucket(card, todo, plans, lookup, now, input.restored?.get(todo.id) ?? 0, superseded.has(todo.id));
    // a failed start carries done_at (the hub ends it), yet it is the user's to retry while its bucket says open
    const waits = status === "failed" ? bucket === "open" : !todo.done_at && WAITS_ON_YOU.has(status);
    if (asks.length) asking.push({ kind: "task", key, todo, status, asks });
    else if (waits || taskAwaitsDecision(todo)) {
      // Only the review decision is hidden; a live Agent question above still has its home.
      if (!waits && taskAwaitsDecision(todo) && (todo.decision_review?.dismissal_id && todo.decision_review.review_at === decisionRound(todo) || input.deferred?.[decisionLaterKey(todo)])) {
        for (const agent of active) folded.add(sessionKey(agent.machine_id, agent.session_id));
        continue;
      }
      deciding.push({ item: { kind: "task", key, todo, status, asks }, rank: todoRank(todo, status), at: card.at });
    }
    // a parked task waits on purpose (#3804): with the tasks in progress however long it has been still, never folded away
    else if (bucket === "open" || reviewState(todo) === "parked") open.push({ item: { kind: "task", key, card, todo, status, activity: taskBackgroundWork(todo, lookup) }, rank: todoRank(todo, status), at: card.at });
    else if (bucket === "shelved") shelved.push({ item: { kind: "task", key, card, todo, status }, at: card.at });
    else continue;
    for (const agent of active) folded.add(sessionKey(agent.machine_id, agent.session_id));
  }

  // Agents that belong to no task, the main conversation's only (dockAgents): asking ones wait on the user, working ones run.
  const agentsAsking: NeedsYouItem[] = [];
  const agentsRunning: RunningItem[] = [];
  for (const { session, state } of dockAgents(sessions.map((s) => lookup(s.machine_id, s.session_id) ?? s), todos, conversation, main)) {
    if (folded.has(sessionKey(session.machine_id, session.session_id))) continue;
    const key = `agent:${sessionKey(session.machine_id, session.session_id)}`;
    if (state === "needs_you") agentsAsking.push({ kind: "agent", key, session, asks: asksOf.get(sessionKey(session.machine_id, session.session_id)) ?? [] });
    else agentsRunning.push({ kind: "agent", key, session, activity: backgroundWork(session) });
  }

  const plansWaiting: NeedsYouItem[] = Object.values(plans)
    .filter((plan) => plan.conversation === conversation && plan.state === "open")
    .sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))
    .map((plan) => ({ kind: "plan", key: `plan:${plan.id}`, plan }));
  const messages: NeedsYouItem[] = feed.filter((it) => messageWaits(it, input.deferred)).map((item) => ({ kind: "message", key: `message:${item.id}`, item }));

  const newest = (a: { at: string; rank?: number }, b: { at: string; rank?: number }) => (a.rank ?? 0) - (b.rank ?? 0) || byTime(b.at, a.at);
  const firstAsk = (item: NeedsYouItem) => (item.kind === "task" || item.kind === "agent" ? item.asks[0]?.created_at : undefined);
  return {
    needsYou: [
      ...[...asking, ...agentsAsking].sort((a, b) => byTime(firstAsk(a), firstAsk(b))),
      ...plansWaiting,
      ...deciding.sort(newest).map((row) => row.item),
      ...messages,
    ],
    running: [...open.sort(newest).map((row) => row.item), ...agentsRunning],
    shelved: shelved.sort(newest).map((row) => row.item),
  };
}

/** Needs you's rows by the conversation supervising them (#4314, docs/assistant/assistant.md "Background rounds"): a
 *  task's `origin`, else `conversation` (agents, plans, message cards, hand-made to-dos). This conversation's group comes
 *  first, then the others in the order their first row appears; the rows and their order within a group are as given,
 *  so the entry's count is unchanged. One group when every row is this conversation's. */
export function needsYouBySupervisor(items: readonly NeedsYouItem[], conversation: string): { supervisor: string; items: NeedsYouItem[] }[] {
  const groups = new Map<string, NeedsYouItem[]>([[conversation, []]]);
  for (const item of items) {
    const supervisor = item.kind === "task" && item.todo.origin ? item.todo.origin : conversation;
    const group = groups.get(supervisor);
    if (group) group.push(item);
    else groups.set(supervisor, [item]);
  }
  return [...groups].map(([supervisor, rows]) => ({ supervisor, items: rows }));
}
