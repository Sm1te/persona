import { compatibleEnvelope, setCompatibilityFeatures } from "./compatibility";
// App-wide state: what the hub pushes over the WebSocket (machines, sessions, interactions), plus the assistant.
// Views read it with useStore(selector); per-session item streams go to listeners (ChatView subscribes).

import { chosenManager, defaultKnown, defaultManager, fromShownManager, noteConversation, noteItemManager, onDefaultManager, onManagerChoice, shownManager } from "./manager-choice";
import { BASE } from "./base";
import { mainConversationMachine, clearMemoryPages, discardFeedCache, feedCacheScope, readFeedCache } from "./feed-cache";
import { useRef, useSyncExternalStore } from "react";
import { agentsChanged, refreshAgentPrefs, refreshAgentSessions, resetAgents, setAgentListMode } from "./agents";
import { onRuntimeChanges, runtimeIdentity, runtimeRemoval, runtimeSessions, SESSIONS_LOOKUP_FEATURE, SESSIONS_PAGE_COUNTS_FEATURE, setRuntimePageCounts } from "./runtime-list";
import { sameValue } from "./stable-lines";
import { SESSIONS_LIST_FEATURE, type ListCounts } from "./session-list";
import { SESSIONS_HIERARCHY_FEATURE } from "./child-sessions";
import { applySessionDelta, SESSIONS_DELTA_FEATURE, sessionRefreshed, sessionSnapshot } from "./sessions-delta";
import { dueMarks, heldRows, liveMarks, markOver, newMark, nextDue, unansweredMarks, type SeenMark, type SeenMarks } from "./seen-outbox";
import { probeEdge } from "./edge-auth";
import { api, ApiError, CLIENT_FEATURES, CLIENT_PONG_TIMEOUT, CLIENT_PLATFORM, CLIENT_VERSION, errorText, getToken, sessionPath } from "./api";
import { currentLanguage } from "../i18n";
import type { AgentMutes } from "./agent-groups";
import { stateOf } from "./format";
import { timeMs } from "./times";
import { NO_PINS, pinsOf, type AgentPins } from "./pins";
import type { AssistantInfo, Interaction, Item, Machine, MeshHub, Session } from "./types";
import type { Resolution } from "./answered";
import { answersIn, type DelegatedAnswer, type Reanswer } from "./delegate";
import type { AgentLaunch } from "./new-agent";
import { isSpacesSupported, makeSessionLookup, type SessionLookup, type Todo } from "./todos";
import { releaseAttachments } from "./attachments";
import { dropElicitationDraft } from "./elicitation";
import type { OutboxEntry } from "./outbox";
import { NO_LINK, RESTARTING_MS, type ConnLink } from "./connection";
import { setOfflineProbe } from "./feedback";
import { readDirectionTree } from "./direction-names";
import { unnamedTaskConversations } from "./background-supervisor";
import { directionTreeAwaitsHello } from "./direction-tree";
import { afterEvent, afterHello, graceUntil, isResumed, NO_CURSOR, RESTART_FAST_MS, retryDelay, shownConn, wsQuery, type Drop, type EventCursor } from "./reconnect";

export type Conn = "connecting" | "online" | "offline";

export interface AnsweredCard {
  ix: Interaction;
  resolution: Resolution;
  /** The hub said it was answered already: the resolution is the answer that took effect, not what was just sent. */
  already?: boolean;
  /** When this page answered it (Date.now(), set by endInteraction): views/assistant/answered-lookup.ts compares it
   *  with the agent's state_since. */
  at?: number;
  /** Handed back to the computer (dismiss; or the hub says it was handed back already): its agent still waits there,
   *  so answered-lookup does not count it as answered (#4877). */
  handedBack?: boolean;
  /** The hub has withdrawn the card (its interaction_resolved came, or it was over already): until then a sessions push
   *  that still says needs_you may be from before the answer took (#4877). */
  withdrawn?: boolean;
}

export interface State {
  conn: Conn;
  /** What is behind `conn` once it dropped: since when, the next try, a restart, the network (lib/connection.ts). */
  link: ConnLink;
  version: string;
  /** What the hub can do (hello `features`): terminal_stream, terminal_control… */
  features: string[];
  /** This hub's name and its interconnected group (docs/archive/operations/plan-hub-mesh.md 2.6); empty on older hubs. */
  hubName: string;
  mesh: MeshHub[];
  machines: Machine[];
  sessions: Record<string, Session[]>;
  /** Compact list rows never overwrite a chat's complete details in sessions. */
  sessionSummaries: Session[];
  sessionCounts: ListCounts | null;
  /** List rows of open tasks' agents the Agents window does not hold, by runtimeIdentity (#3274): the list's deltas
   *  for them and an exact lookup for one no list has, so a task's state never reads a missing agent as stopped. */
  taskRows: Record<string, Session>;
  /** Finished agents this page marked seen that the hub has not recorded yet, by runtimeIdentity (lib/seen-outbox.ts):
   *  the rows in `sessions` and `sessionSummaries` are held seen meanwhile, the paged list's rows by the row itself. */
  seen: SeenMarks;
  interactions: Interaction[];
  /** Cards this page just answered, by interaction id: shown answered (with the picks) for a moment, not clickable. */
  answered: Record<string, AnsweredCard>;
  /** Questions the assistant answered for the user while this page watched, by interaction id: shown in their chat,
   *  with "Change answer", until closed (lib/delegate.ts). */
  delegated: Record<string, DelegatedAnswer>;
  /** What the user changed a delegated answer to, by interaction id (interaction_reanswered or this page's own). */
  reanswered: Record<string, Reanswer>;
  assistant: AssistantInfo | null;
  assistantError: string | null;
  mutes: AgentMutes;
  /** Pinned spaces / agents (GET /pins): the top of the Agents list. */
  pins: AgentPins;
  loaded: boolean;
  launches: Record<string, AgentLaunch>;
  /** Messages other pages sent for a conversation (`mid/sid`), shown there until the transcript has them (lib/outbox.ts). */
  outbox: Record<string, OutboxEntry[]>;
  todos: Todo[];
  todosSupported: boolean;
  todosLoaded: boolean;
  todosError: string | null;
  spacesSupported: boolean;
}

let state: State = {
  conn: "connecting",
  link: { ...NO_LINK, networkUp: networkUp() },
  version: "",
  features: [],
  hubName: "",
  mesh: [],
  machines: [],
  sessions: {},
  sessionSummaries: [],
  sessionCounts: null,
  taskRows: {},
  seen: {},
  interactions: [],
  answered: {},
  delegated: {},
  reanswered: {},
  assistant: null,
  assistantError: null,
  mutes: { spaces: [], sessions: [], allowed: [] },
  pins: NO_PINS,
  loaded: false,
  launches: {},
  outbox: {},
  todos: [],
  todosSupported: true,
  todosLoaded: false,
  todosError: null,
  spacesSupported: true,
};

const listeners = new Set<() => void>();

// What a hub push changes is in `state` at once (getState, the code after it), but the views hear of it once per
// animation frame: a busy hub sends a burst of frames each tick (machines, every machine's sessions, each pending
// card), and telling the views after each one rendered the page that many times. Everything else (a click, an answer
// coming back) still tells them at once: an optimistic update is on screen before the code after it reads the page.
let pushing = false;
let notifyFrame: number | undefined;
let notifyTimer: ReturnType<typeof setTimeout> | undefined;
// a hidden tab runs no animation frames: the timer tells the views in the end anyway
const NOTIFY_FALLBACK_MS = 100;

function notify() {
  if (notifyFrame !== undefined) cancelAnimationFrame(notifyFrame);
  clearTimeout(notifyTimer);
  notifyFrame = notifyTimer = undefined;
  listeners.forEach((l) => l());
}

function notifySoon() {
  if (notifyTimer !== undefined) return;
  notifyTimer = setTimeout(notify, NOTIFY_FALLBACK_MS);
  if (typeof requestAnimationFrame === "function") notifyFrame = requestAnimationFrame(notify);
}

function set(patch: Partial<State>) {
  state = holdSeen({ ...state, ...patch }, patch);
  if (viewed.size && ("sessions" in patch || "sessionSummaries" in patch || "assistant" in patch)) seeViewed(false);
  if (pushing) notifySoon();
  else notify();
}

/** Rows whose seen mark still waits for the hub are shown seen, whichever way they came in (a push, a page of the
 *  list, an HTTP answer); marks the hub's rows show recorded, or no longer about this turn, go (lib/seen-outbox.ts). */
function holdSeen(next: State, patch: Partial<State>): State {
  if (!Object.keys(next.seen).length || !("sessions" in patch || "sessionSummaries" in patch || "assistant" in patch || "seen" in patch)) return next;
  const now = Date.now();
  let marks = liveMarks(next.seen, now);
  const over: string[] = [];
  const settle = (rows: Session[]) => {
    for (const s of rows) {
      const id = runtimeIdentity(s);
      if (marks[id] && markOver(s, marks[id], now)) over.push(id);
    }
  };
  Object.values(next.sessions).forEach(settle);
  settle(next.sessionSummaries);
  if (over.length) {
    const kept: Record<string, SeenMark> = { ...marks };
    for (const id of over) delete kept[id];
    marks = kept;
  }
  const sessions: Record<string, Session[]> = {};
  let changed = false;
  for (const [mid, rows] of Object.entries(next.sessions)) {
    sessions[mid] = heldRows(rows, marks, now);
    changed ||= sessions[mid] !== rows;
  }
  const summaries = heldRows(next.sessionSummaries, marks, now);
  const assistant = next.assistant;
  const conversations = assistant ? heldRows(assistant.conversations, marks, now) : undefined;
  return {
    ...next,
    seen: marks,
    ...(changed ? { sessions } : {}),
    ...(summaries !== next.sessionSummaries ? { sessionSummaries: summaries } : {}),
    ...(assistant && conversations && conversations !== assistant.conversations ? { assistant: { ...assistant, conversations } } : {}),
  };
}

let listedFull = new Map<string, Session>();
const rowHints = new Map<string, Session>();
const ROW_HINT_LIMIT = 100;
runtimeSessions.subscribe(() => {
  const { rows, counts } = runtimeSessions.getSnapshot();
  if (rows !== state.sessionSummaries && !runtimeSessions.isSummary()) {
    const next = new Map(rows.map((row) => [runtimeIdentity(row), row]));
    applyCompleteSessions(rows.filter((row) => listedFull.get(runtimeIdentity(row)) !== row));
    listedFull = next;
  }
  if (rows !== state.sessionSummaries || counts !== state.sessionCounts) set({ sessionSummaries: rows, sessionCounts: counts });
});
onRuntimeChanges((upsert, remove) => {
  for (const row of upsert) if (rowHints.has(runtimeIdentity(row))) rowHints.set(runtimeIdentity(row), row);
  // Older paged hubs return complete rows. Update only details already retained; never materialize the delta backlog.
  const known = new Set(Object.values(state.sessions).flat().map(runtimeIdentity));
  const updates = runtimeSessions.isSummary() ? [] : upsert.filter((row) => known.has(runtimeIdentity(row)));
  applyCompleteSessions(updates, remove);
  keepTaskRows(upsert, remove.map(runtimeRemoval));
});

/** The runtime identities of the open tasks' agents. */
function openTaskAgents(): Set<string> {
  const out = new Set<string>();
  for (const todo of state.todos) {
    if (todo.done_at) continue;
    for (const a of todo.agents ?? []) if (!a.released_at) out.add(runtimeIdentity({ machine_id: a.machine_id, session_id: a.session_id } as Session));
  }
  return out;
}

/** Keeps the list's newest rows of open tasks' agents (`taskRows`), whether the Agents window holds them or not. */
function keepTaskRows(upsert: Session[], remove: string[] = []) {
  const agents = openTaskAgents();
  let rows = state.taskRows;
  const change = () => { if (rows === state.taskRows) rows = { ...rows }; };
  for (const row of upsert) {
    const id = runtimeIdentity(row);
    if (agents.has(id) && rows[id] !== row) { change(); rows[id] = row; }
  }
  for (const id of remove) if (id in rows) { change(); delete rows[id]; }
  for (const id of Object.keys(rows)) if (!agents.has(id)) { change(); delete rows[id]; }
  if (rows !== state.taskRows) set({ taskRows: rows });
}

/** `base`, and for an agent it does not have, the list's row of an open task's agent (`taskRows`). */
export function withTaskRows(base: SessionLookup, rows: Record<string, Session>): SessionLookup {
  return (mid, sid) => base(mid, sid) ?? rows[runtimeIdentity({ machine_id: mid, session_id: sid } as Session)];
}

/** How a task's agents are now, for its status, card and pill (#3274): the newest of the list's row (the Agents window or
 *  `taskRows`, which the list's deltas keep current) and the chat's complete row, which stops being refreshed once its
 *  chat closes. The complete row stands while it says the same about the agent's state, for its subagents and steps. */
export function taskSessionLookup(s: Pick<State, "sessions" | "sessionSummaries" | "taskRows">): SessionLookup {
  const full = makeSessionLookup(s.sessions);
  const listed = withTaskRows(s.sessionSummaries.length ? makeSessionLookup(s.sessionSummaries) : () => undefined, s.taskRows);
  return (mid, sid) => {
    const detail = full(mid, sid);
    const row = listed(mid, sid);
    if (!row || !detail || row === detail) return row ?? detail;
    if ((detail.rev ?? 0) > (row.rev ?? 0)) return detail;
    // New summaries carry all the tray's live work metadata. A same-state update can change counts/phase/progress
    // without changing work or pending cards; never let an older open-chat snapshot hide it.
    if (row.background_tasks_count !== undefined) return row;
    const same = row.state === detail.state && !!row.background_working === !!detail.background_working
      && (row.pending_interactions ?? 0) === (detail.pending_interactions ?? 0) && sameValue(row.work, detail.work);
    return same ? detail : row;
  };
}

const taskRowsAsked = new Set<string>();
/** An open task's agent that no list row or chat detail has (outside the Agents window and quiet since it loaded): one
 *  exact lookup for it (`sessions_lookup`), then the list's deltas keep it (#3274). */
async function lookUpTaskAgents() {
  if (!state.features.includes(SESSIONS_LOOKUP_FEATURE)) return;
  const listed = new Set(state.sessionSummaries.map(runtimeIdentity));
  const missing = [...openTaskAgents()].filter((id) => !taskRowsAsked.has(id) && !listed.has(id) && !(id in state.taskRows)
    && !(state.sessions[JSON.parse(id)[0]] ?? []).some((s) => runtimeIdentity(s) === id));
  for (const id of missing.slice(0, 20)) {
    taskRowsAsked.add(id);
    const [mid, sid] = JSON.parse(id) as [string, string];
    const params = new URLSearchParams({ summary: "1", limit: "1", machine_id: mid, session_id: sid });
    try {
      const page = await api<{ sessions?: Session[] }>(`/sessions?${params}`);
      const row = page?.sessions?.[0];
      if (row && runtimeIdentity(row) === id) keepTaskRows([row]);
    } catch {
      taskRowsAsked.delete(id); // asked again with the next todos or connection
    }
  }
}

const cardRowsAsked = new Set<string>();
let cardRowsFor: Interaction[] | null = null;
/** A child session's card (`folded_under`, #5120) whose session no list holds: a paging hub leaves child sessions out of
 *  the Agents list's pages (`roots`). One exact lookup for its row (`sessions_lookup`) puts it among the row hints, so the
 *  card can be answered wherever it shows (the main chat's Needs you, the Inbox), not only on its parent's open page. */
async function lookUpCardSessions() {
  if (state.interactions === cardRowsFor || !state.features.includes(SESSIONS_LOOKUP_FEATURE)) return;
  cardRowsFor = state.interactions;
  const missing = new Set(state.interactions.filter((ix) => ix.folded_under && ix.session_id && !getSession(ix.machine_id, ix.session_id))
    .map((ix) => runtimeIdentity({ machine_id: ix.machine_id, session_id: ix.session_id } as Session)));
  for (const id of [...missing].filter((x) => !cardRowsAsked.has(x)).slice(0, 20)) {
    cardRowsAsked.add(id);
    const [mid, sid] = JSON.parse(id) as [string, string];
    const params = new URLSearchParams({ summary: "1", limit: "1", machine_id: mid, session_id: sid });
    try {
      const page = await api<{ sessions?: Session[] }>(`/sessions?${params}`);
      const row = page?.sessions?.[0];
      if (row && runtimeIdentity(row) === id) {
        rememberSessionRow(row);
        notify(); // what reads getSession for the card (can it be answered) reads it again
      }
    } catch {
      cardRowsAsked.delete(id); // asked again with the next change of the cards or connection
    }
  }
}

/** A clicked filtered row may not belong to the unfiltered first page. Keep a small hint until exact lookup finishes. */
export function rememberSessionRow(row: Session) {
  const id = runtimeIdentity(row);
  rowHints.delete(id);
  rowHints.set(id, row);
  if (rowHints.size > ROW_HINT_LIMIT) rowHints.delete(rowHints.keys().next().value!);
  if (!state.features.includes(SESSIONS_LOOKUP_FEATURE) && typeof row.waiting !== "boolean") applyCompleteSessions([row]);
}

/** Tells the views now of the pushes still waiting for the next frame, if any: for code that keeps something tied to
 *  the session the page draws, read from the state right after a request returns (lib/ultracode.ts setUltracode), so
 *  the page draws that same session with it rather than an older one for a frame. */
export function flushPushes() {
  if (notifyTimer !== undefined) notify();
}

export function getState() {
  return state;
}

export function getSession(mid: string, sid: string): Session | undefined {
  return (state.sessions[mid] ?? []).find((s) => s.session_id === sid)
    ?? state.assistant?.conversations.find((s) => s.machine_id === mid && s.session_id === sid)
    ?? state.sessionSummaries.find((s) => s.machine_id === mid && s.session_id === sid)
    ?? rowHints.get(runtimeIdentity({ machine_id: mid, session_id: sid } as Session));
}

/** Complete snapshots replace optional fields as well: omitted task arrays must not survive from an older snapshot.
 *  `also` goes in the same update (one render for both). */
export function applyCompleteSessions(rows: Session[], remove: string[] = [], also: Partial<State> = {}) {
  if (!rows.length && !remove.length) return;
  const by = { ...state.sessions };
  const updates = new Map<string, Map<string, Session>>();
  const machine = (mid: string) => {
    if (!updates.has(mid)) updates.set(mid, new Map((by[mid] ?? []).map((row) => [row.session_id, row])));
    return updates.get(mid)!;
  };
  let changed = false;
  remove = remove.map(runtimeRemoval);
  for (const id of remove) {
    changed = rowHints.delete(id) || changed;
    const [mid, sid] = JSON.parse(id) as [string, string];
    changed = machine(mid).delete(sid) || changed;
    if (state.assistant?.conversations.some((row) => runtimeIdentity(row) === id)) changed = true;
  }
  for (const row of rows) {
    const records = machine(row.machine_id);
    if (!sameValue(records.get(row.session_id), row)) { records.set(row.session_id, row); changed = true; }
  }
  if (!changed) {
    if (Object.keys(also).length) set(also);
    return;
  }
  for (const [mid, records] of updates) by[mid] = [...records.values()];
  const assistant = state.assistant;
  const full = new Map(rows.map((row) => [runtimeIdentity(row), row]));
  const gone = new Set(remove);
  set({ ...also, sessions: by, ...(assistant ? { assistant: { ...assistant, conversations: assistant.conversations.filter((row) => !gone.has(runtimeIdentity(row))).map((row) => full.get(runtimeIdentity(row)) ?? row) } } : {}) });
}

// Selectors may build new arrays / objects: a result shallow-equal to the last one is the last one, so
// useSyncExternalStore sees a stable snapshot and only re-renders on a real change.
function shallowEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((x, i) => Object.is(x, b[i]));
  if (a && b && typeof a === "object" && typeof b === "object" && !Array.isArray(a) && !Array.isArray(b)) {
    const ka = Object.keys(a), kb = Object.keys(b);
    return ka.length === kb.length && ka.every((k) => Object.is((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]));
  }
  return false;
}

/** Called after every change of the state (lib/error-reports-start.ts watches the hello's features). */
export const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
};
// a child session's card may need its row looked up (lookUpCardSessions): checked as the cards change
subscribe(() => void lookUpCardSessions());

export function useStore<T>(select: (s: State) => T): T {
  const last = useRef<{ v: T } | null>(null);
  const get = () => {
    const v = select(state);
    if (last.current && shallowEqual(last.current.v, v)) return last.current.v;
    last.current = { v };
    return v;
  };
  return useSyncExternalStore(subscribe, get, get); // the third: static rendering (tests)
}

// ── what the hub can do (hello `features`) ───────────────

/**
 * Whether the hub has `feature`. `unknown` is the answer until the hub has named any (before hello, or a hub that
 * sends no features): for something the web always used to do, that keeps the old behavior until the hub says.
 */
export function hubHas(features: string[], feature: string, unknown = false): boolean {
  return features.length ? features.includes(feature) : unknown;
}

/**
 * Whether this connection gets the session list as deltas. The page asks for it on every connect, so a hub that has
 * it says so in hello; one that does not never sends `sessions_delta` and keeps sending the whole list.
 */
function deltaOn(): boolean {
  return state.features.includes(SESSIONS_DELTA_FEATURE);
}

/**
 * Feishu through the Lark gateway (feature `lark`, which an extension brings): /api/v1/lark/…, the "Messages" page, the Lark settings,
 * the side panel's readers for Feishu documents and Moments posts. Without it those links are ordinary links.
 */
export const LARK_FEATURE = "lark";

/** The web reads working Claudes' status lines (`live` pushes, lib/live.ts; docs/protocol/protocol.md "Live status"). */
export const SESSION_LIVE_FEATURE = "session_live";
/** The hub answers a client's {"type":"ping","id"} with a pong carrying the id (docs/protocol/protocol.md). */
export const APP_PING_FEATURE = "app_ping";

export function larkOn(s: Pick<State, "features"> = state): boolean {
  return hubHas(s.features, LARK_FEATURE, true);
}

export function useLarkOn(): boolean {
  return useStore(larkOn);
}

/**
 * The Feishu plugin writes document comments for the user (feature `lark_doc_comments`, #1290): the reader can reply,
 * comment, resolve and reopen. Not "unknown is what it used to do": a hub that does not say so has no such routes.
 */
export const LARK_DOC_COMMENTS_FEATURE = "lark_doc_comments";

export function docCommentsOn(s: Pick<State, "features"> = state): boolean {
  return larkOn(s) && s.features.includes(LARK_DOC_COMMENTS_FEATURE);
}

export function useDocCommentsOn(): boolean {
  return useStore(docCommentsOn);
}

// ── item stream (subscribed sessions) ───────────────────

export interface ItemsEvent {
  machine_id: string;
  session_id: string;
  items: Item[];
  history?: string;
  to_seq?: number;
  from_seq?: number;
  backlog?: boolean;
  more_after?: boolean;
  /** Only on a replay counted back from the end (a subscribe without since_seq): whether older items exist. */
  has_more?: boolean;
}

type SessionListener = {
  items?: (e: ItemsEvent) => void;
  delta?: (text: string) => void;
  /** The hub's `revs` event: the transcript grew to this rev; the list's row keeps the rev it had (lib/stale-page.ts). */
  rev?: (rev: number) => void;
  reconnected?: () => void;
};
const sessionListeners = new Map<string, Set<SessionListener>>();
const key = (mid: string, sid: string) => `${mid}/${sid}`;

export function listenSession(mid: string, sid: string, l: SessionListener) {
  const k = key(mid, sid);
  if (!sessionListeners.has(k)) sessionListeners.set(k, new Set());
  sessionListeners.get(k)!.add(l);
  return () => {
    const ls = sessionListeners.get(k);
    ls?.delete(l);
    // the last listener of a chat takes its set along: every reader treats a missing key as an empty set, and the
    // reconnect walk would otherwise visit one for every chat ever opened
    if (ls?.size === 0) sessionListeners.delete(k);
  };
}

/** Tests only: how many sessions have a listener set. */
export function sessionListenerKeysForTests() {
  return sessionListeners.size;
}

// ── other pushes (inbox, lark, terminal…): pages that need them listen by type ──

type HubListener = (msg: Record<string, unknown>) => void;
const hubListeners = new Map<string, Set<HubListener>>();
// Private UI trees retain their original JSON for strict numeric/schema validation.
const privateStreamListeners = new Set<(raw: string) => void>();
const socketListeners = new Set<(connected: boolean) => void>();
export function onPrivateUIStream(listener: (raw: string) => void) {
  privateStreamListeners.add(listener);
  return () => { privateStreamListeners.delete(listener); };
}
export function onAppSocket(listener: (connected: boolean) => void) {
  socketListeners.add(listener);
  return () => { socketListeners.delete(listener); };
}
function socketChanged(connected: boolean) {
  if (!connected) authenticatedSocket = null;
  socketListeners.forEach((listener) => listener(connected));
}
/** A WebSocket open event or cached features cannot substitute for this connection's authenticated hello. */
export function appSocketReady() { return authenticatedSocket !== null && authenticatedSocket === ws && socketToken === getToken() && isOpen(); }

/** Listen to one type of WebSocket message (`inbox`, `lark`, `terminal`, `terminal_control`…); returns the unsubscribe. */
export function onHubEvent(type: string, l: HubListener) {
  if (!hubListeners.has(type)) hubListeners.set(type, new Set());
  hubListeners.get(type)!.add(l);
  return () => {
    hubListeners.get(type)?.delete(l);
  };
}

/** Whether the socket is open now (a page that sends its own subscriptions waits for `hello` otherwise). */
export function isOpen() {
  return ws?.readyState === WebSocket.OPEN;
}

// ── WebSocket ───────────────────────────────────────────

let ws: WebSocket | null = null;
let authenticatedSocket: WebSocket | null = null;
let socketToken = "";
let retry = 0;
let retryTimer: number | undefined;
let lastMessage = 0;
let lastPing = 0;
let stopped = true;
// A reconnect after a drop is invisible for a while (lib/reconnect.ts): the drop that began while online, the
// attempts since, the ones in the quick phase after a restart, and the timer that shows the drop once the grace ends.
let drop: Drop | null = null;
let fastTries = 0;
let graceTimer: number | undefined;
// The hub said hub_restarting on this socket: its close is a restart whatever the close code.
let restartNotice = false;
let cursor: EventCursor = NO_CURSOR;

export function send(msg: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
}

export function connect() {
  setCompatibilityFeatures([]);
  stopped = false;
  clearTimeout(retryTimer);
  ws?.close();
  socketChanged(false);
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  socketToken = getToken();
  directionTreeAwaitsHello(true); // its hello reads the direction tree: the screens wait for that read
  const sock = new WebSocket(`${proto}//${location.host}${BASE}/api/v1/ws?${wsQuery(socketToken, cursor, [SESSIONS_LIST_FEATURE, SESSIONS_DELTA_FEATURE, SESSION_LIVE_FEATURE, "extension_ui_stream_v1", "persona_contracts_v1"])}`);
  ws = sock;
  restartNotice = false;
  // an attempt does not move offline back to connecting: the pill stays on one phase while the hub is down instead of
  // flipping at every backoff tick (the app's ConnectionTitle); shownConn decides it when the attempt fails
  sock.onopen = () => {
    socketOpenedAt = Date.now(); // a direction tree read since then serves this hello (lib/direction-tree.ts)
    // the interface language: this connection's text, and the user's language for pushes and the assistant (docs/protocol/i18n.md)
    // and what it can render: MCP forms and links come only to a socket that says so
    sock.send(JSON.stringify({ type: "client_hello", platform: CLIENT_PLATFORM, version: CLIENT_VERSION, features: CLIENT_FEATURES, locale: currentLanguage(), heartbeat: { pong_timeout: CLIENT_PONG_TIMEOUT } }));
    retry = 0;
    fastTries = 0;
    lastMessage = Date.now();
    lastPing = Date.now();
  };
  sock.onmessage = (ev) => {
    if (ws !== sock) return;
    lastMessage = Date.now();
    let msg: Record<string, unknown>;
    try {
      msg = compatibleEnvelope(JSON.parse(ev.data));
    } catch {
      return;
    }
    if (msg.type === "extension_ui_stream") {
      if (appSocketReady()) privateStreamListeners.forEach((listener) => listener(ev.data));
      return; // Private trees never enter the global replay cursor or store.
    }
    if (msg.type === "hello") authenticatedSocket = sock;
    pushing = true;
    try {
      onMessage(msg);
      if (msg.type === "hello") socketChanged(true);
    } finally {
      pushing = false;
    }
  };
  sock.onclose = (ev) => {
    if (ws === sock) directionTreeAwaitsHello(false); // no hello: whoever waited for the tree reads it now
    if (ws !== sock) return;
    const wasLive = authenticatedSocket === sock;
    ws = null;
    setCompatibilityFeatures([]);
    socketChanged(false);
    if (stopped) return;
    // an attempt that never got a hello carries no status: ask the hub once whether a sign-in layer in front of it has
    // expired (lib/edge-auth.ts); a socket that was live just dropped, and its reconnect attempt is the one to ask about
    if (!wasLive && !restartNotice && ev.code !== 1001) void probeEdge();
    const now = Date.now();
    const restart = restartNotice || ev.code === 1001;
    // the drop starts while online; a restart announced on a later attempt stretches its grace
    const next = !drop && state.conn === "online" ? { at: now, restart } : drop && restart && !drop.restart ? { ...drop, restart } : null;
    if (next) {
      drop = next;
      clearTimeout(graceTimer);
      graceTimer = window.setTimeout(showDrop, Math.max(0, graceUntil(next) - now));
    }
    const since = drop ? now - drop.at : Infinity;
    const fast = Boolean(drop?.restart) && since < RESTART_FAST_MS;
    // the ordinary backoff counts its own attempts: after a restart's quick phase it starts again from the fast one
    const wait = retryDelay(fast ? fastTries : retry - fastTries, Boolean(drop?.restart), since, Math.random());
    if (fast) fastTries++;
    retry++;
    const conn = shownConn(drop, retry - 1, now);
    // what the pill's details say (the facts change on every attempt; only the pill and its details read them)
    const link: ConnLink = { ...state.link, lastOnline: drop?.at ?? state.link.lastOnline, nextTry: now + wait, restarting: Boolean(drop?.restart) && now - (drop?.at ?? now) < RESTARTING_MS };
    set(conn !== state.conn ? { conn, link } : { link }); // inside the grace: the page still looks online
    retryTimer = window.setTimeout(connect, wait);
  };
}

// The grace is over and the socket is still not back: show it as before.
function showDrop() {
  if (!drop || stopped || state.conn !== "online") return;
  set({ conn: shownConn(drop, Math.max(0, retry - 1), Date.now()) });
}

function back() {
  drop = null;
  clearTimeout(graceTimer);
}

/** "Retry now" (the pill's details): try now instead of waiting for the backoff. */
export function reconnectNow() {
  if (stopped) return;
  connect();
}

function networkUp(): boolean {
  return typeof navigator === "undefined" || navigator.onLine !== false;
}

// An action that failed while the hub is out of reach says so once, calmly (lib/feedback.ts, ui/atoms toast).
setOfflineProbe(() => (!networkUp() ? "network" : state.conn !== "online" || drop ? "hub" : null));

export function disconnect() {
  setCompatibilityFeatures([]);
  stopped = true;
  // a reconnect scheduled after a drop would otherwise fire connect(), which clears stopped (signed out: an empty token)
  clearTimeout(retryTimer);
  back();
  ws?.close();
  ws = null;
  socketChanged(false);
}

// A socket that went quiet (sleep, network change) is reconnected. The hub's own pings are WebSocket control frames
// the page never sees, and an idle hub pushes nothing, so a hub that has app_ping is asked: every APP_PING_EVERY a
// {"type":"ping"} goes out and its pong counts as hearing from the hub (docs/protocol/protocol.md). A hub without it is left
// as before: the quiet limit alone, so a hub with nothing to say is reconnected now and then. Checked every 5 s, so a
// ping goes out every 25 to 30 s while timers run normally; background tabs can delay it further.
const APP_PING_EVERY = 25_000;
let pingSeq = 0;

/** One look at the socket (every 5 s): reconnect a quiet one, or ask a hub with app_ping whether it is there. */
export function checkSocket() {
  const now = Date.now();
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  if (now - lastMessage > 70_000) {
    connect();
    return;
  }
  if (hubHas(state.features, APP_PING_FEATURE) && now - lastPing >= APP_PING_EVERY) {
    lastPing = now;
    send({ type: "ping", id: `web-${++pingSeq}` });
  }
}
setInterval(() => checkSocket(), 5_000);
if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && !stopped && (!ws || Date.now() - lastMessage > 40_000)) connect();
  });
}
if (typeof window !== "undefined") {
  window.addEventListener("online", () => {
    set({ link: { ...state.link, networkUp: true } });
    if (!stopped) connect();
  });
  window.addEventListener("offline", () => set({ link: { ...state.link, networkUp: false } }));
}

/** The tree read again: its leaders become the assistant's (a new one not listed yet: the assistant is read again). */
/** When the socket last opened. */
let socketOpenedAt = 0;

async function followDirections(changed = false) {
  if (pageOf() !== "") return; // the tree is the default assistant's (docs/protocol/protocol.md)
  const ids = await readDirectionTree(changed, changed ? undefined : socketOpenedAt);
  const a = state.assistant;
  if (!ids || !a) return;
  const known = new Set(a.conversations.map((c) => c.session_id));
  if (ids.some((id) => !known.has(id))) void refreshAssistant().catch(() => {});
  else if (JSON.stringify(ids) !== JSON.stringify((a.leaders ?? []).filter((id) => id !== a.main))) set({ assistant: { ...a, leaders: ids } });
}

const helloHooks = new Set<() => void>();
/** Runs `f` on every hello (lib/new-version.ts asks for the shell then); loaded after the first screen, so it is not in it. */
export function onHello(f: () => void): () => void {
  helloHooks.add(f);
  return () => void helloHooks.delete(f);
}

// The assistant's live updates about the page itself rather than one conversation: only the shown manager's count.
const PAGE_EVENTS = new Set(["assistant_schedule"]);

function onMessage(msg: Record<string, unknown>) {
  cursor = msg.type === "hello" ? afterHello(cursor, msg) : afterEvent(cursor, msg);
  switch (msg.type) {
    case "hello": {
      helloHooks.forEach((f) => f()); // e.g. a hub that came back may serve a new web build (lib/new-version.ts)
      setCompatibilityFeatures(Array.isArray(msg.features) ? msg.features as string[] : []);
      const mesh = (msg.mesh as { hubs?: MeshHub[] } | undefined)?.hubs;
      set({
        conn: "online",
        link: { ...state.link, nextTry: 0, restarting: false, networkUp: true },
        version: String(msg.server_version ?? ""),
        features: Array.isArray(msg.features) ? (msg.features as string[]) : [],
        hubName: typeof msg.hub_name === "string" ? msg.hub_name : "",
        mesh: Array.isArray(mesh) ? mesh : [],
      });
      back();
      const summaryMode = state.features.includes(SESSIONS_LOOKUP_FEATURE);
      if (runtimeSessions.isSummary() !== summaryMode) listedFull.clear();
      runtimeSessions.setSummary(summaryMode);
      // a hub that folds child sessions (#5120): the Agents list and its counts leave them out, each parent counted once
      runtimeSessions.setRoots(state.features.includes(SESSIONS_HIERARCHY_FEATURE));
      setRuntimePageCounts(state.features.includes(SESSIONS_PAGE_COUNTS_FEATURE));
      setAgentListMode(state.features.includes(SESSIONS_LIST_FEATURE));
      taskRowsAsked.clear();
      cardRowsAsked.clear();
      cardRowsFor = null;
      void lookUpTaskAgents();
      // subscriptions are per connection: chats resubscribe from what they have either way
      sessionListeners.forEach((ls) => ls.forEach((l) => l.reconnected?.()));
      // a seen sent over the socket that went may be lost with it
      resendSeen();
      // resumed: the hub replays every broadcast missed since the cursor, nothing needs refetching
      if (!isResumed(msg)) {
        void followDirections(); // the direction tree again: a `directions` may have been missed while away
        void refresh();
        void refreshAgentSessions();
        void refreshAgentPrefs();
      }
      directionTreeAwaitsHello(false); // after followDirections started its read: those waiting share it
      break;
    }
    case "directions":
      // the direction tree changed (#4352): a leader made or ended anywhere, a name, a task's state. The sidebar's 常驻
      // follow at once (#4331: a sixth leader stayed hidden until a reload); a read already out may predate the change
      void followDirections(true);
      break;
    case "hub_restarting":
      restartNotice = true; // the close that follows is a restart: retry at once, stay quiet longer
      break;
    case "pong": // an app_ping answer: arriving at all is the point (lastMessage)
      break;
    case "machines":
      machinesRev++;
      set({ machines: stableMachines(state.machines, (msg.machines as Machine[] | null) ?? []) });
      break;
    case "sessions_list":
      // A hint is not a checkpoint. Even resumed connections must check the HTTP epoch.
      runtimeSessions.invalidate();
      break;
    case "sessions": {
      const mid = msg.machine_id as string;
      sessionsChanged(mid);
      // A delta hub's list has no Codex history (the page has that from GET /sessions): keep what it already has.
      const list = sessionSnapshot((msg.sessions as Session[]) ?? [], state.sessions[mid] ?? [], deltaOn());
      set({ sessions: { ...state.sessions, [mid]: list }, ...syncAssistant(mid, list) });
      break;
    }
    case "sessions_delta": {
      const mid = msg.machine_id as string;
      sessionsChanged(mid);
      const list = applySessionDelta(
        state.sessions[mid] ?? [],
        (msg.upsert as Session[]) ?? [],
        (msg.remove as string[]) ?? [],
      );
      set({ sessions: { ...state.sessions, [mid]: list }, ...syncAssistant(mid, list) });
      break;
    }
    case "session": {
      const s = msg.session as Session;
      sessionsChanged(s.machine_id);
      upsertSession(s);
      break;
    }
    case "interaction": {
      const ix = msg.interaction as Interaction;
      if (!endedHere.has(ix.id)) changeInteraction(ix.id, ix);
      break;
    }
    case "interaction_resolved":
      if (msg.answered_by === "assistant") answeredForUser(msg);
      changeInteraction(msg.id as string, null);
      cardWithdrawn(msg.id as string);
      break;
    case "interaction_reanswered":
      noteReanswer(msg.id as string, answersIn((msg.resolution as { answers?: unknown } | undefined)?.answers));
      break;
    case "items": {
      const e = msg as unknown as ItemsEvent;
      sessionListeners.get(key(e.machine_id, e.session_id))?.forEach((l) => l.items?.(e));
      break;
    }
    case "revs": {
      // Transcripts that grew while their rows' other fields stayed: only an open chat uses it, to notice a lost push.
      const mid = msg.machine_id as string;
      for (const [sid, rev] of Object.entries((msg.revs as Record<string, unknown> | undefined) ?? {})) {
        if (typeof rev === "number") sessionListeners.get(key(mid, sid))?.forEach((l) => l.rev?.(rev));
      }
      break;
    }
    case "agents.prefs":
      void refreshAgentPrefs();
      break;
    case "agents":
      agentsChanged(Array.isArray(msg.session_ids) ? (msg.session_ids as string[]) : []);
      break;
    case "todos":
      todosPushed();
      break;
    case "delta":
      sessionListeners.get(key(msg.machine_id as string, msg.session_id as string))?.forEach((l) => l.delta?.(msg.text as string));
      break;
  }
  if (typeof msg.type !== "string") return;
  // another manager's schedule than the assistant page shows (lib/manager-choice.ts) is not this page's; what names a
  // conversation (feed items, cards) goes by it, whichever manager's it is, and an open conversation of another stays live.
  // Before the roster says which manager is the default, every one passes: it only makes the page read its own again.
  if (PAGE_EVENTS.has(msg.type) && defaultKnown() && !fromShownManager(msg)) return;
  hubListeners.get(msg.type)?.forEach((l) => l(msg));
}

// This page's changes to the mutes and the pins: numbered, and how many PUTs are still on their way. Each goes out as a
// whole PUT and the hub pushes nothing back, so a refresh() sent before a change lands (or while its PUT is out, the
// hub may read before it writes) answers the old value, and a failed change put back over a later one undoes it.
const mutesSaves = { rev: 0, out: 0 };
const pinsSaves = { rev: 0, out: 0 };

/** Shows `next` at once and PUTs it; on failure puts back what was before, unless a later change came, and throws. */
async function save<K extends "mutes" | "pins">(key: K, path: string, next: State[K], saves: { rev: number; out: number }) {
  const before = state[key];
  const change = ++saves.rev;
  saves.out++;
  set({ [key]: next } as Partial<State>);
  try {
    await api(path, { method: "PUT", json: next });
  } catch (e) {
    if (change === saves.rev) set({ [key]: before } as Partial<State>);
    throw e;
  } finally {
    saves.out--;
  }
}

/** "Mute" (optimistic): PUT /mutes; throws when the hub refuses, with the mutes put back. */
export function saveMutes(mutes: AgentMutes): Promise<void> {
  return save("mutes", "/mutes", mutes, mutesSaves);
}

/** One machine as the hub answered a change to it (PUT …/engine), over what the list has: the hub's next \`machines\`
 * push says the same, this only does not wait for it (iOS AppModel.applyMachine). */
export function applyMachine(m: Machine) {
  if (!state.machines.some((x) => x.id === m.id)) return;
  machinesRev++;
  set({ machines: stableMachines(state.machines, state.machines.map((x) => (x.id === m.id ? { ...x, ...m } : x))) });
}

/** `next`, with each machine that is the same as `prev`'s of that id (sameValue) kept as `prev`'s object, and `prev`
 *  itself when nothing changed: the hub sends its whole list again on events that often leave this page's as it was
 *  (an extension's features, a device report), and a new array of new rows redrew every view that reads the list. */
function stableMachines(prev: Machine[], next: Machine[]): Machine[] {
  const before = new Map(prev.map((m) => [m.id, m]));
  let same = prev.length === next.length;
  const out = next.map((m, i) => {
    const old = before.get(m.id);
    const kept = old && sameValue(old, m) ? old : m;
    same &&= kept === prev[i];
    return kept;
  });
  return same ? prev : out;
}

/** "Pin" (optimistic): PUT /pins; throws when the hub refuses, with the pins put back. */
export function savePins(pins: AgentPins): Promise<void> {
  return save("pins", "/pins", pins, pinsSaves);
}

/** "Mark all as seen": every finished-unread one of these is seen now, in one update of the list (iOS AppModel.markSeen). The
 * hub pushes the sessions back as seen; the list does not wait for it, and each waits in the seen outbox until it does. */
export function markSeen(list: Session[]) {
  const keys = new Set<string>();
  const retained = new Map(Object.values(state.sessions).flat().map((s) => [runtimeIdentity(s), s]));
  const changedAt = (s: Session) => Math.max(timeMs(s.updated_at), timeMs(s.state_since), timeMs(s.seen_at));
  const now = new Date().toISOString();
  const changed: Session[] = [];
  const marks: Record<string, SeenMark> = {};
  for (const given of list) {
    const id = runtimeIdentity(given);
    if (keys.has(id)) continue;
    keys.add(id);
    // The full-list fallback can supply rows absent from the bounded store. Retain only acted-on rows,
    // and honor a more recent local state (for example, an agent that has started working again).
    const current = retained.get(id);
    const s = current && changedAt(current) >= changedAt(given) ? current : given;
    const mark = newMark(s, Date.now(), now);
    if (!mark) continue;
    marks[id] = deliverSeen(s.machine_id, s.session_id, mark);
    changed.push({ ...s, state: "idle", seen_at: now });
  }
  if (!changed.length) return;
  // one update of the list: the marks and the rows they hold seen together
  applyCompleteSessions(changed, [], { seen: { ...state.seen, ...marks } });
  scheduleSeenCheck();
}

export function putLaunch(launch: AgentLaunch) {
  set({ launches: { ...state.launches, [launch.id]: launch } });
}

export function setOutbox(key: string, list: OutboxEntry[]) {
  const next = { ...state.outbox };
  if (list.length) next[key] = list;
  else delete next[key];
  set({ outbox: next });
}

// A deleted conversation leaves the list right away; the hub's next sessions push agrees (docs/protocol/protocol.md DELETE /sessions).
export function dropSession(mid: string, sid: string) {
  const list = state.sessions[mid];
  if (!list?.some((x) => x.session_id === sid)) return;
  set({ sessions: { ...state.sessions, [mid]: list.filter((x) => x.session_id !== sid) } });
}

export function upsertSession(s: Session) {
  const list = state.sessions[s.machine_id] ?? [];
  const i = list.findIndex((x) => x.session_id === s.session_id);
  const next = i < 0 ? [...list, s] : list.map((x, j) => {
    if (j !== i) return x;
    const merged = { ...x, ...s };
    merged.background_working = s.background_working;
    merged.background_waiting = s.background_waiting;
    // An optimistic action can start from getSession's summary fallback. A later complete info response must
    // drop its scalar summary markers, or they keep overriding the complete task arrays indefinitely.
    if (typeof s.waiting !== "boolean") {
      delete merged.waiting;
      delete merged.background_count;
      delete merged.running_subagents;
      delete merged.running_workflows;
      delete merged.background_tasks_count;
      delete merged.workflow_progress;
      merged.background = s.background;
      merged.subagents = s.subagents;
      merged.workflows = s.workflows;
    } else {
      // A summary is a complete projection: omitted zero counts and a disappeared run clear the previous one.
      merged.background_count = s.background_count ?? 0;
      merged.running_subagents = s.running_subagents ?? 0;
      merged.running_workflows = s.running_workflows ?? 0;
      merged.background_tasks_count = s.background_tasks_count;
      merged.workflow_progress = s.workflow_progress;
    }
    return merged;
  });
  set({ sessions: { ...state.sessions, [s.machine_id]: next }, ...syncAssistant(s.machine_id, [s]) });
}

/** The conversations the assistant was read again for (unnamedTaskConversations): once each, whatever it answered. */
const askedForUnnamed = new Set<string>();

// The assistant's conversations are sessions on its machine too: the change that keeps the copy in `assistant`
// current, to go with the sessions' own (one update, not two).
function syncAssistant(mid: string, list: Session[]): Partial<State> {
  const a = state.assistant;
  if (!a || a.machine_id !== mid) return {};
  // a background supervisor the hub made since the assistant was read (#5082): read again, so it is the built-in one
  const unnamed = unnamedTaskConversations(a, list).filter((id) => !askedForUnnamed.has(id));
  if (unnamed.length) {
    unnamed.forEach((id) => askedForUnnamed.add(id));
    void refreshAssistant().catch(() => {});
  }
  let changed = false;
  const convs = a.conversations.map((c) => {
    const s = list.find((x) => x.session_id === c.session_id);
    if (!s) return c;
    // a push carries its machine's whole list, parsed anew in a full snapshot: a conversation it leaves as it was keeps
    // its object (the sidebar's memoized rows compare by identity)
    const next = { ...c, ...s };
    if (sameValue(next, c)) return c;
    changed = true;
    return next;
  });
  return changed ? { assistant: { ...a, conversations: convs } } : {};
}

// ── pending interactions ───────────────

// Every change to the pending interactions (an event, an answer from this page), numbered: a /interactions snapshot
// asked for before a change must not undo it when it lands. refresh() waits for /sessions too (seconds on a busy hub),
// so without this a card answered meanwhile came back, clickable, and answering it again said "no longer pending".
let ixRev = 0;
const ixChanges = new Map<string, { rev: number; ix: Interaction | null }>();
const IX_CHANGES_KEPT = 500;

function changeInteraction(id: string, ix: Interaction | null) {
  ixChanges.delete(id); // re-inserted: the oldest are dropped first
  ixChanges.set(id, { rev: ++ixRev, ix });
  if (ixChanges.size > IX_CHANGES_KEPT) ixChanges.delete(ixChanges.keys().next().value!);
  // the hub sends every pending card again now and then: one that says what the page has already changes nothing
  const old = ix && state.interactions.find((i) => i.id === id);
  if (old && JSON.stringify(old) === JSON.stringify(ix)) return;
  const interactions = state.interactions.filter((i) => i.id !== id);
  if (ix) interactions.push(ix);
  else dropElicitationDraft(id); // over here or elsewhere: what was typed into it goes with it
  if (!ix && interactions.length === state.interactions.length) return;
  set({ interactions });
}

/** The pending interactions from a snapshot asked for at revision `before`, with what changed since. */
export function mergeInteractions(snapshot: Interaction[], before: number): Interaction[] {
  const out = new Map(snapshot.filter((i) => !endedHere.has(i.id)).map((i) => [i.id, i]));
  for (const [id, c] of ixChanges) {
    if (c.rev <= before) continue;
    out.delete(id);
    if (c.ix) out.set(id, c.ix);
  }
  return [...out.values()];
}

const ANSWERED_MS = 20_000;

// Cards this page ended (answered, found over, handed back), newest last. Until the node ends one, the hub's pending
// cards still list it: the snapshot after a hello that resumes the stream sends it again as an `interaction` frame, and
// a full list read after the answer has it too. It is never answerable here again (#1541). A failed answer does not end
// its card, so nothing has to come off this list.
const endedHere = new Set<string>();
const ENDED_KEPT = 500;

/**
 * An interaction this page answered (or found over when it tried): its card goes at once, without waiting for the
 * hub's interaction_resolved, which may be late or lost with a socket. With `answer` it shows answered for a while.
 */
export function endInteraction(id: string, answer?: AnsweredCard) {
  endedHere.delete(id);
  endedHere.add(id);
  if (endedHere.size > ENDED_KEPT) endedHere.delete(endedHere.values().next().value!);
  changeInteraction(id, null);
  if (!answer) return;
  const card = { ...answer, at: answer.at ?? Date.now(), withdrawn: answer.withdrawn || answer.already === true || resolvedSeen.has(id) };
  set({ answered: { ...state.answered, [id]: card } });
  setTimeout(() => dropAnswered(id, card), ANSWERED_MS);
}

// Cards the hub has withdrawn (interaction_resolved), newest last: the event often comes before the answer's own reply.
const resolvedSeen = new Set<string>();
const RESOLVED_KEPT = 200;

/** The hub withdrew a card (interaction_resolved, on the same socket as the sessions pushes). */
export function cardWithdrawn(id: string) {
  resolvedSeen.delete(id);
  resolvedSeen.add(id);
  if (resolvedSeen.size > RESOLVED_KEPT) resolvedSeen.delete(resolvedSeen.values().next().value!);
  const cur = state.answered[id];
  if (!cur || cur.withdrawn) return;
  set({ answered: { ...state.answered, [id]: { ...cur, withdrawn: true } } });
}

/** Takes an answered card away (its time is up, or closed); `only` keeps a newer one for the same id (the same answer
 *  marked withdrawn since is still that one: compared by its time). */
export function dropAnswered(id: string, only?: AnsweredCard) {
  const cur = state.answered[id];
  if (!cur || (only && cur !== only && cur.at !== only.at)) return;
  const answered = { ...state.answered };
  delete answered[id];
  set({ answered });
}

/** interaction_resolved with answered_by "assistant": the card it answered (the page had it pending, or just answered
 *  it too and lost) stays in its chat with what the assistant answered and why; a card the page never saw is the
 *  session history's to show. */
function answeredForUser(msg: Record<string, unknown>) {
  const id = msg.id as string;
  const ix = state.interactions.find((i) => i.id === id) ?? state.answered[id]?.ix;
  if (ix) keepDelegated(ix, msg);
}

// Delegated answers kept in their chats at most, the oldest put away first: a tab left open for days on many agents.
const DELEGATED_KEPT = 50;

/** A question the assistant answered for the user (`r`: its answers and reason, from interaction_resolved or from the
 *  hub's reply to this page's own answer that came second): it leaves the pending cards and stays in its chat. This
 *  page's own answer lost, so its "Answered" card goes. */
export function keepDelegated(ix: Interaction, r: Record<string, unknown>) {
  if (ix.kind !== "question") return;
  changeInteraction(ix.id, null);
  const answered = { ...state.answered };
  delete answered[ix.id];
  const card: DelegatedAnswer = state.delegated[ix.id] ?? { ix, answers: answersIn(r.answers), reason: typeof r.reason === "string" ? r.reason : "", at: Date.now() };
  const delegated = { ...state.delegated, [ix.id]: card };
  const ids = Object.keys(delegated);
  for (const old of ids.slice(0, Math.max(0, ids.length - DELEGATED_KEPT))) delete delegated[old];
  set({ delegated, answered });
}

/** The user changed a delegated answer (interaction_reanswered, or this page's own POST …/reanswer once it went). The
 *  hub sends only the questions that changed: an earlier change to another question of the card stays. */
export function noteReanswer(id: string, answers: Record<string, string>) {
  if (!id || !Object.keys(answers).length) return;
  const before = state.reanswered[id]?.answers;
  set({ reanswered: { ...state.reanswered, [id]: { answers: { ...before, ...answers }, at: Date.now() } } });
}

/** Closes a delegated answer's card in its chat; what was changed stays for the session's history. */
export function dropDelegated(id: string) {
  if (!state.delegated[id]) return;
  const delegated = { ...state.delegated };
  delete delegated[id];
  set({ delegated });
}

// Every request for a list the page keeps whole (/todos, /assistant), numbered: an answer to a request sent before the
// one already applied is older than what the page shows (refresh() waits for /sessions too, seconds on a busy hub) and
// must not put the old list back.
function numbered<T>(path: string) {
  let asked = 0;
  let applied = 0;
  return {
    ask() {
      const n = ++asked;
      const answer = api<T>(path);
      /** Whether this answer may be applied (no later request's answer is in place); if so it is the one in place now. */
      const fresh = () => {
        if (n < applied) return false;
        applied = n;
        return true;
      };
      return { answer, fresh };
    },
    /** Every answer still on its way is old now (another login). */
    forget() {
      applied = ++asked;
    },
  };
}

const todosAsked = numbered<{ todos: Todo[] }>("/todos");
const assistantAsked = numbered<AssistantInfo>("/assistant");

// refresh() calls, numbered the same way: one that started before the last one applied, or before reset(), is older
// than what the page shows and is dropped whole.
const refreshes = { asked: 0, applied: 0 };
// Bumped by every machines push and applyMachine(): a GET /machines answer asked for before one of them is older.
let machinesRev = 0;
// Session pushes, numbered, and the last one per machine: on a hub without deltas a GET /sessions answer asked for
// before a push to a machine must not put that machine's older list back.
let sessionsRev = 0;
const sessionsPushed = new Map<string, number>();

function sessionsChanged(mid: string) {
  sessionsPushed.set(mid, ++sessionsRev);
}

export async function refresh() {
  const gen = ++refreshes.asked;
  const machinesAt = machinesRev;
  const sessionsAt = sessionsRev;
  const before = ixRev;
  // the answers count only if this page changes nothing while they are on their way, and had no change out when they left
  const mutesAt = mutesSaves.out ? -1 : mutesSaves.rev;
  const pinsAt = pinsSaves.out ? -1 : pinsSaves.rev;
  const asked = todosAsked.ask();
  // The main conversation must not wait for the slowest sidebar request. refreshAssistant also guards
  // against an earlier login or manager's answer and invalidates a superseded cached main conversation.
  const assistantDone = refreshAssistant().catch(() => {});
  // the todos go in as soon as they answer: By to-do (the default, #2812) waits for them, not for /sessions, which can
  // take seconds on a busy hub while the socket has pushed the sessions already
  const todosDone = asked.answer.then(
    (res) => applyTodos({ status: "fulfilled", value: res }, asked.fresh),
    (reason: unknown) => applyTodos({ status: "rejected", reason }, asked.fresh),
  );
  const [sessions, interactions, machines, mutes, pins] = await Promise.allSettled([
    state.features.includes(SESSIONS_LIST_FEATURE)
      ? runtimeSessions.load().then(() => null)
      : state.loaded ? Promise.resolve(null) : api<{ sessions: Session[] }>("/sessions"),
    api<{ interactions: Interaction[] }>("/interactions"),
    api<{ machines: Machine[] }>("/machines"),
    api<AgentMutes>("/mutes"),
    api<Partial<AgentPins>>("/pins"),
    assistantDone, // keep the cold-start loaded gate until the main reference or its error is known
  ]);
  if (gen < refreshes.applied) return todosDone;
  refreshes.applied = gen;
  const patch: Partial<State> = { loaded: true };
  if (sessions.status === "fulfilled" && sessions.value) {
    const by: Record<string, Session[]> = {};
    for (const s of sessions.value.sessions ?? []) (by[s.machine_id] ??= []).push(s);
    if (deltaOn()) {
      // What the socket pushed is newer than this answer (the request went out before): keep it, take only the Codex
      // history the answer has and the page does not.
      for (const [mid, old] of Object.entries(state.sessions)) by[mid] = sessionRefreshed(by[mid] ?? [], old, true);
    } else {
      // a machine pushed since the request went out keeps what was pushed
      for (const [mid, rev] of sessionsPushed) if (rev > sessionsAt && state.sessions[mid]) by[mid] = state.sessions[mid];
    }
    patch.sessions = by;
  }
  if (interactions.status === "fulfilled") patch.interactions = mergeInteractions(interactions.value.interactions ?? [], before);
  if (machines.status === "fulfilled" && machinesAt === machinesRev) patch.machines = stableMachines(state.machines, machines.value.machines ?? []);
  if (mutes.status === "fulfilled" && mutesAt === mutesSaves.rev) patch.mutes = { spaces: mutes.value.spaces ?? [], sessions: mutes.value.sessions ?? [], allowed: mutes.value.allowed ?? [] };
  if (pins.status === "fulfilled" && pinsAt === pinsSaves.rev) patch.pins = pinsOf(pins.value);
  set(patch);
  await Promise.all([todosDone, assistantDone]);
}

/** Both refresh paths share the list, error and unsupported-hub (404) handling; only a fresh answer finishes loading. */
function applyTodos(todos: PromiseSettledResult<{ todos: Todo[] }>, fresh: () => boolean) {
  if (!fresh()) return; // a later answer is in place, or this one belongs to a login reset() ended
  if (todos.status === "fulfilled") {
    const list = todos.value.todos ?? [];
    for (const t of list) {
      if (t.manager) noteItemManager(`/todos/${encodeURIComponent(t.id)}`, t.manager);
    }
    set({ todos: list, todosSupported: true, todosLoaded: true, todosError: null, spacesSupported: computeSpacesSupported(list) });
    keepTaskRows([]);
    void lookUpTaskAgents();
    return;
  }
  const reason = todos.reason;
  if (reason instanceof ApiError && reason.status === 404) return set({ todosSupported: false, todosLoaded: true, todosError: null, spacesSupported: false });
  set({ todosLoaded: true, todosError: errorText(reason) });
}

export function computeSpacesSupported(todos: Todo[]): boolean {
  return todos.length > 0 ? isSpacesSupported(todos) : true;
}

export function setSpacesSupported(supported: boolean) {
  set({ spacesSupported: supported });
}

/** Puts a todo the hub just answered in place of the one we have (a retried task shows starting at once). */
export function putTodo(todo: Todo) {
  // A confirmed write is newer than every list read that is still in flight.
  todosAsked.forget();
  if (todo.manager) noteItemManager(`/todos/${encodeURIComponent(todo.id)}`, todo.manager);
  set({ todos: state.todos.map((t) => (t.id === todo.id ? { ...t, ...todo } : t)) });
}

/** Takes a todo the hub just deleted out of the list at once; a /todos answer asked for before the delete is older
 *  and must not put it back. */
export function dropTodo(id: string) {
  noteItemManager(`/todos/${encodeURIComponent(id)}`, undefined);
  todosAsked.forget();
  set({ todos: state.todos.filter((t) => t.id !== id) });
}

// A push that says the to-dos changed ({"type":"todos"}, a task's feed item) comes in bursts: the hub sends one per write,
// and a supervised task's report, stage and links land within the same second. Every GET /todos is the whole list (tens
// of KB on a busy hub), so pushes read it one at a time: one that lands while such a read is out asks for one more read
// after it, however many land meanwhile. What this page itself just changed still reads at once (refreshTodos).
let pushedRead = false;
let pushedAgain = false;

export function todosPushed() {
  if (pushedRead) {
    pushedAgain = true;
    return;
  }
  pushedRead = true;
  void refreshTodos().finally(() => {
    pushedRead = false;
    if (!pushedAgain) return;
    pushedAgain = false;
    todosPushed();
  });
}

export async function refreshTodos() {
  const asked = todosAsked.ask();
  try {
    const res = await asked.answer;
    applyTodos({ status: "fulfilled", value: res }, asked.fresh);
  } catch (err) {
    applyTodos({ status: "rejected", reason: err }, asked.fresh);
  }
}

export async function refreshAssistant() {
  const asked = assistantAsked.ask();
  const from = pageOf();
  try {
    const a = await asked.answer;
    if (!asked.fresh() || from !== pageOf()) return;
    const assistant = { ...a, conversations: a.conversations ?? [] };
    const scope = feedCacheScope();
    const cached = readFeedCache(scope);
    if (cached && (cached.manager ?? "") === from && (cached.machineID !== mainConversationMachine(assistant) || cached.sessionID !== assistant.main)) discardFeedCache(scope);
    assistantByManager.set(cacheKey(), assistant);
    // its conversations' own requests (feed, stop…) go to it, whichever manager the page shows later
    // (the default's are not noted: with no header the hub finds their manager itself)
    const owner = chosenManager() ?? undefined;
    for (const c of assistant.conversations) noteConversation(c.session_id, owner);
    if (assistant.main) noteConversation(assistant.main, owner);
    set({ assistant, assistantError: null });
  } catch (err) {
    if (asked.fresh() && from === pageOf()) {
      set({ assistantError: errorText(err) });
    }
    throw err;
  }
}

// The last /assistant answer of each manager this page showed: an offline one's history stays viewable (its requests
// answer 503 assistant_offline), and switching back shows the page at once while it is read again.
const assistantByManager = new Map<string, AssistantInfo>();

/** The assistant page asked for: "" the default's (no header goes, whichever manager the default turns out to be),
 *  else the manager picked on it. */
const pageOf = () => chosenManager() ?? "";
/** Where the page's answer is kept: under its manager, the one picked (kept across reloads, so it may be there before
 *  the roster is read) or the default once the roster said which that is; "" before (the default's, whose id is not
 *  known yet), moved under the default's id when it is. */
const cacheKey = () => chosenManager() ?? (defaultKnown() ? defaultManager() : "");

// The manager whose page is drawn (null: the default's, before the roster said which that is).
let drawnPage = { page: pageOf(), manager: null as string | null };

// Another manager picked on the assistant page, or the default moved while its page is shown: that manager's page, from
// what this page kept of it until the hub answers. The placeholder default becoming the roster's is the same page.
onManagerChoice(() => {
  const page = pageOf();
  const manager = defaultKnown() ? shownManager() : null;
  const same = page === drawnPage.page && (drawnPage.manager === null || manager === null || manager === drawnPage.manager);
  drawnPage = { page, manager };
  if (same) return;
  assistantAsked.forget();
  set({ assistant: assistantByManager.get(cacheKey()) ?? null, assistantError: null });
  void refreshAssistant().catch(() => {});
});

// The default's page read before the roster said which manager that is: it is that one's.
onDefaultManager((was) => {
  if (!was.settled && drawnPage.page === "") drawnPage = { page: "", manager: defaultManager() };
  const kept = assistantByManager.get("");
  if (was.settled || !kept) return;
  assistantByManager.delete("");
  assistantByManager.set(defaultManager(), kept);
});

/** The user looked at this session on this page (its chat is open and the page is in front): a finished turn stops
 *  being Unread here at once, and on the hub so every client agrees (docs/protocol/agent-states.md "Seen"). An agent
 *  already seen is told again anyway (cheap, and the hub may not have the page's last mark): only an unread one waits
 *  in the outbox. */
export function markSeenLocally(mid: string, sid: string) {
  const s = getSession(mid, sid);
  const mark = s ? newMark(s, Date.now()) : undefined;
  if (!mark) {
    send({ type: "seen", machine_id: mid, session_id: sid });
    return;
  }
  set({ seen: { ...state.seen, [runtimeIdentity(s!)]: deliverSeen(mid, sid, mark) } });
  scheduleSeenCheck();
}

// ── chats on screen ─────────────────────────────────────
// The agent conversations shown on this page, by runtimeIdentity, with how many views show each (the page and a side
// panel can show the same one). While the page is in front, one of them that turns Unread (its turn ended while the
// user watched) is marked seen in the same update that brought it: the list never shows it green (iOS
// AppModel.notifyTransitions, ChatView's onChange(state)). In a background tab nothing is marked; coming back is.
const viewed = new Map<string, number>();

/** The page is what the user is looking at: its tab shows (a focused window is not required: a second screen counts). */
function pageInFront(): boolean {
  return typeof document === "undefined" || document.visibilityState === "visible";
}

/** Marks every viewed chat that is Unread now. `always`: an opened chat, or the page coming back to the front, tells
 *  the hub even when this page has it seen already (another device's view, an older seen the hub lost). */
function seeViewed(always: boolean) {
  if (!viewed.size || !pageInFront()) return;
  for (const id of viewed.keys()) {
    const [mid, sid] = JSON.parse(id) as [string, string];
    const s = getSession(mid, sid);
    if (always || (s && stateOf(s) === "unread" && !state.seen[id])) markSeenLocally(mid, sid);
  }
}

/** A chat is on screen: it is seen now (if the page is in front) and whenever a turn ends while it stays. Returns the
 *  release. Subagent transcripts are not their own agents: their views do not call this. */
export function viewSession(mid: string, sid: string): () => void {
  const id = runtimeIdentity({ machine_id: mid, session_id: sid } as Session);
  viewed.set(id, (viewed.get(id) ?? 0) + 1);
  if (pageInFront()) markSeenLocally(mid, sid);
  return () => {
    const n = (viewed.get(id) ?? 1) - 1;
    if (n > 0) viewed.set(id, n);
    else viewed.delete(id);
  };
}

if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => seeViewed(true));
}

/** Over the socket while it is open, else over HTTP, which answers. Returns the mark as it was sent. */
function deliverSeen(mid: string, sid: string, mark: SeenMark, overHTTP = false): SeenMark {
  const at = Date.now();
  if (!overHTTP && isOpen()) {
    send({ type: "seen", machine_id: mid, session_id: sid });
    return { ...mark, sentAt: at, route: "socket", answered: false };
  }
  const id = runtimeIdentity({ machine_id: mid, session_id: sid } as Session);
  void api(`${sessionPath(mid, sid)}/seen`, { method: "POST" }).then(
    () => {
      const m = state.seen[id];
      if (m && m.sentAt === at) set({ seen: { ...state.seen, [id]: { ...m, answered: true } } });
    },
    () => {}, // waits for the socket to come back (hello)
  );
  return { ...mark, sentAt: at, route: "http", answered: false };
}

// A socket's seen not taken (no list showing it recorded) within SEEN_CONFIRM_MS goes again over HTTP (#1588).
let seenTimer: ReturnType<typeof setTimeout> | undefined;
function scheduleSeenCheck() {
  if (seenTimer !== undefined) return;
  const at = nextDue(state.seen, Date.now());
  if (at === undefined) return;
  seenTimer = setTimeout(() => {
    seenTimer = undefined;
    const due = dueMarks(state.seen, Date.now());
    if (due.length) {
      const seen = { ...state.seen };
      for (const id of due) {
        const [mid, sid] = JSON.parse(id) as [string, string];
        seen[id] = deliverSeen(mid, sid, seen[id], true);
      }
      set({ seen });
    }
    scheduleSeenCheck();
  }, Math.max(at - Date.now(), 100));
}

/** A new connection's hello: what it has not answered goes again (a socket's seen may have gone with the old one). */
function resendSeen() {
  const ids = unansweredMarks(state.seen, Date.now());
  if (!ids.length) return;
  const seen = { ...state.seen };
  for (const id of ids) {
    const [mid, sid] = JSON.parse(id) as [string, string];
    seen[id] = deliverSeen(mid, sid, seen[id]);
  }
  set({ seen });
  scheduleSeenCheck();
}

export function reset() {
  disconnect();
  cursor = NO_CURSOR; // another login: another stream
  ixChanges.clear();
  resolvedSeen.clear();
  endedHere.clear();
  // a /todos or /assistant answer still on its way belongs to the old login
  todosAsked.forget();
  pushedAgain = false; // the next login reads its own list
  assistantAsked.forget();
  assistantByManager.clear();
  // and so do the pins and mutes of a refresh() or a failed save still on its way
  mutesSaves.rev++;
  pinsSaves.rev++;
  // a refresh() still on its way belongs to the old login too
  refreshes.applied = ++refreshes.asked;
  sessionsPushed.clear();
  resetAgents();
  rowHints.clear();
  taskRowsAsked.clear();
  cardRowsAsked.clear();
  cardRowsFor = null;
  listedFull.clear();
  runtimeSessions.reset();
  clearMemoryPages();
  clearTimeout(seenTimer);
  seenTimer = undefined;
  Object.values(state.launches).forEach((launch) => releaseAttachments(launch.attachments));
  set({
    machines: [],
    sessions: {},
  sessionSummaries: [],
  sessionCounts: null,
    taskRows: {},
    seen: {},
    interactions: [],
    answered: {},
    delegated: {},
    reanswered: {},
    assistant: null,
    assistantError: null,
    launches: {},
    outbox: {},
    mutes: { spaces: [], sessions: [], allowed: [] },
    pins: NO_PINS,
    todos: [],
    todosSupported: true,
    todosLoaded: false,
    todosError: null,
    spacesSupported: true,
    loaded: false,
    conn: "connecting",
    link: { ...NO_LINK, networkUp: networkUp() },
  });
}
