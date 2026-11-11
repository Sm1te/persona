// The direction tree (#4352, docs/assistant/unified-entry.md "Direction tree"): each current leader conversation (a
// "direction", shown as 常驻 / Pinned) with the open tasks it supervises, each task with its state and its agents, and
// last the secretary's group (tasks with no leader). The hub builds it once (GET /api/v1/assistant/directions/tree); the
// sidebar, the To-dos page and the Agents page all draw this one tree and never group or rank the tasks themselves.
// This module is the only one that reads and holds it; the Agents list's shape of it is lib/direction-agent-tree.ts
// (kept apart so this module loads light: lib/store.ts reaches it through lib/direction-names.ts).
// Asked on the socket's hello and again on every {"type":"directions"} push (lib/store.ts); a hub without the route
// (404) answers null, and the screens keep what they showed before (no counts, no grouping by direction).
import { useEffect, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { api, ApiError } from "./api";

export const DIRECTION_TREE_PATH = "/assistant/directions/tree";

/** A task's state in the tree, as the hub decides it (one rule, in this precedence). */
export type DirectionTaskState = "needs_you" | "review" | "stuck" | "working";

export const DIRECTION_TASK_STATES: readonly DirectionTaskState[] = ["needs_you", "review", "stuck", "working"];

export interface DirectionCounts {
  working: number;
  needs_you: number;
  stuck: number;
  review: number;
}

export interface DirectionAgent {
  machine_id: string;
  session_id: string;
  title: string;
  agent?: string;
  /** The session's state as session rows carry it ("" when the hub did not find the session). */
  state: string;
  children?: DirectionAgent[];
}

export interface DirectionTask {
  id: string;
  title: string;
  state: DirectionTaskState;
  agents?: DirectionAgent[];
}

export interface Direction {
  /** The leader conversation's id; the main conversation's for the secretary's group. */
  id: string;
  /** The direction's name ("" for the secretary's group). */
  name: string;
  secretary?: boolean;
  counts: DirectionCounts;
  tasks?: DirectionTask[];
}

export interface DirectionTree {
  directions: Direction[];
}

/** The tree as the hub sent it, with anything missing or malformed made safe to draw. */
export function normalizeTree(raw: unknown): DirectionTree {
  const list = (raw as { directions?: unknown })?.directions;
  if (!Array.isArray(list)) return { directions: [] };
  const agent = (a: DirectionAgent): DirectionAgent => ({ ...a, title: a.title ?? "", state: a.state ?? "", children: Array.isArray(a.children) ? a.children.filter(validAgent).map(agent) : [] });
  return {
    directions: list
      .filter((d): d is Direction => !!d && typeof d.id === "string")
      .map((d) => ({
        id: d.id,
        name: typeof d.name === "string" ? d.name : "",
        ...(d.secretary ? { secretary: true } : {}),
        counts: normalizeCounts(d.counts),
        tasks: (Array.isArray(d.tasks) ? d.tasks : [])
          .filter((td): td is DirectionTask => !!td && typeof td.id === "string")
          .map((td) => ({ id: td.id, title: td.title ?? "", state: DIRECTION_TASK_STATES.includes(td.state) ? td.state : "working", agents: (Array.isArray(td.agents) ? td.agents : []).filter(validAgent).map(agent) })),
      })),
  };
}

/** Each count a finite number, 0 for anything else the hub sent. */
function normalizeCounts(raw: unknown): DirectionCounts {
  const c = (raw ?? {}) as Partial<Record<keyof DirectionCounts, unknown>>;
  const n = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);
  return { working: n(c.working), needs_you: n(c.needs_you), stuck: n(c.stuck), review: n(c.review) };
}

function validAgent(a: unknown): a is DirectionAgent {
  return !!a && typeof (a as DirectionAgent).machine_id === "string" && typeof (a as DirectionAgent).session_id === "string";
}

/** The directions to show as such: the current leaders (never the secretary's group). */
export function leaderDirections(tree: DirectionTree | null | undefined): Direction[] {
  return tree?.directions.filter((d) => !d.secretary) ?? [];
}

/** The secretary's group, when it is to be shown: there is at least one leader and it holds at least one task. */
export function shownSecretary(tree: DirectionTree | null | undefined): Direction | undefined {
  if (!tree || leaderDirections(tree).length === 0) return undefined;
  const s = tree.directions.find((d) => d.secretary);
  return s && (s.tasks?.length ?? 0) > 0 ? s : undefined;
}

/** Whether the tree has something to group by: a hub that sends it and at least one leader. */
export function groupsByDirection(tree: DirectionTree | null | undefined): tree is DirectionTree {
  return leaderDirections(tree).length > 0;
}

/** A direction's title: its name; 书记官 for the secretary's group; the id as a last resort (a leader with no name yet). */
export function directionTitle(d: Direction, fallback?: string): string {
  if (d.secretary) return t("lib-todo-tree.secretary");
  return d.name || fallback || d.id;
}

/** A task state's word (在做 / 等你 / 卡住 / 待验收). */
export function directionStateName(s: DirectionTaskState): string {
  switch (s) {
    case "needs_you":
      return t("lib-state-labels.direction_needs_you");
    case "review":
      return t("assistant-tasks.review_pending");
    case "stuck":
      return t("lib-state-labels.direction_stuck");
    default:
      return t("lib-state-labels.direction_working");
  }
}

/** A task state's text color: needs-you, working, and caution for stuck and review (Tailwind token classes). */
export const DIRECTION_STATE_TONE: Record<DirectionTaskState, string> = { needs_you: "text-attention", working: "text-orange", stuck: "text-orange", review: "text-orange" };

/** Every agent of a task, its children included, depth first. */
export function taskAgents(td: DirectionTask): DirectionAgent[] {
  const out: DirectionAgent[] = [];
  const walk = (list: readonly DirectionAgent[] | undefined) => list?.forEach((a) => (out.push(a), walk(a.children)));
  walk(td.agents);
  return out;
}

/** The "machine/session" key of an agent. */
export const agentRef = (a: Pick<DirectionAgent, "machine_id" | "session_id">) => `${a.machine_id}/${a.session_id}`;

// ── the store: one tree for the page ─────────────────────────
// The only place the web reads the tree. lib/store.ts asks on hello (readDirectionTreeOnce) and on every `directions`
// push (refreshDirectionTree); the sidebar's 常驻 names and counts (lib/direction-names.ts) are taken from it, and the
// To-dos page and the Agents list draw it, so each change costs one GET however many screens show it.

/** undefined: not asked yet (or asking the first time); null: no tree (an older hub, or the first read failed). */
type Snapshot = DirectionTree | null | undefined;

const listeners = new Set<() => void>();
let tree: Snapshot = undefined;
let reading: Promise<DirectionTree | null> | null = null;
/** When the read out (or, once it answered, the last one) was sent, and what the last one that answered gave. */
let readingSince = 0;
let lastRead: { at: number; tree: DirectionTree } | null = null;
let queued: Promise<DirectionTree | null> | null = null;
let generation = 0;
/** Reads are numbered as they are sent; an answer older than one already applied is not applied (two reads can be out
 *  at once, a hello's beside one sent before the socket opened, and they can come back in any order). */
let readSeq = 0;
let appliedSeq = 0;

function set(next: Snapshot) {
  if (next === tree) return;
  tree = next;
  listeners.forEach((l) => l());
}

/** An answer that is not a tree (a layer in front of an older hub): a tree always lists its secretary group. */
class NoTree extends Error {}

function read(): Promise<DirectionTree | null> {
  const n = generation;
  const at = Date.now();
  const seq = ++readSeq;
  const p: Promise<DirectionTree | null> = api<unknown>(DIRECTION_TREE_PATH)
    .then((res) => {
      if (!Array.isArray((res as { directions?: unknown } | null)?.directions)) throw new NoTree();
      const next = normalizeTree(res);
      if (n !== generation) return null;
      // a newer read already answered: this one is stale, and its caller gets the tree that read gave
      if (seq < appliedSeq) return tree ?? null;
      appliedSeq = seq;
      lastRead = { at, tree: next };
      set(next);
      return next;
    })
    // an older hub: no tree, and the screens stay as they were. Any other failure keeps the last tree; with none yet
    // it counts as no tree too, so no screen waits on a read that failed (the next hello or push asks again)
    .catch((e: unknown) => {
      if (n === generation && seq > appliedSeq && (e instanceof NoTree || (e instanceof ApiError && e.status === 404) || tree === undefined)) set(null);
      return null;
    })
    .finally(() => {
      if (reading === p) reading = null;
    });
  reading = p;
  readingSince = at;
  return p;
}

/** Reads the tree, or joins the read already out (on hello, and for a screen that needs it first): the tree just
 *  read, null when this read gave none. With `since` (hello: when the socket opened), a read sent since then is as good
 *  as a new one (any change after it comes as a push). Without it, while a hello is on its way (the socket is
 *  connecting), it waits for the hello's read instead of sending one beside it. */
export function readDirectionTreeOnce(since?: number): Promise<DirectionTree | null> {
  if (since === undefined) return reading ?? helloWait?.promise ?? read();
  if (reading && readingSince >= since) return reading;
  if (!reading && lastRead && lastRead.at >= since) return Promise.resolve(lastRead.tree);
  return read();
}

let helloWait: { promise: Promise<DirectionTree | null>; resolve: (p: Promise<DirectionTree | null>) => void } | null = null;

/** lib/store.ts: a socket is connecting, so a hello that reads the tree is on its way (true); the hello came, or the
 *  socket closed before it (false). The first screen and the sidebar's names then share the hello's one read. */
export function directionTreeAwaitsHello(pending: boolean) {
  if (pending) {
    if (!helloWait) {
      let resolve: (p: Promise<DirectionTree | null>) => void = () => {};
      const promise = new Promise<DirectionTree | null>((r) => (resolve = r));
      helloWait = { promise, resolve };
    }
    return;
  }
  const w = helloWait;
  helloWait = null;
  // the hello's read when it made one (it starts before this is called); otherwise a read for whoever waited
  w?.resolve(reading ?? (tree === undefined ? read() : Promise.resolve(tree)));
}

/** Reads the tree because it changed (a `directions` push): a read already out may have left before the change, so
 *  one more follows it, once however many pushes come meanwhile. */
export function refreshDirectionTree(): Promise<DirectionTree | null> {
  if (!reading) return read();
  if (!queued) {
    const n = generation;
    queued = reading.then(() => {
      queued = null;
      return n === generation ? read() : null;
    });
  }
  return queued;
}

/** Forget the tree (another login, tests; lib/direction-names.ts's reset calls it). */
export function resetDirectionTree() {
  generation++;
  reading = null;
  queued = null;
  lastRead = null;
  helloWait = null;
  readSeq = 0;
  appliedSeq = 0;
  tree = undefined;
}

/** Calls `l` whenever the tree changes (lib/direction-names.ts keeps the 常驻 names and counts from it). */
export function subscribeDirectionTree(l: () => void): () => void {
  listeners.add(l);
  return () => void listeners.delete(l);
}
const snapshot = () => tree;

/** Whether the last tree read has a leader to group by (the grouping shortcut offers "By pinned" then). */
export function directionTreeGroups(): boolean {
  return groupsByDirection(tree);
}

/** The tree as last read, outside React. */
export const directionTreeNow = snapshot;

/** The hub's direction tree: undefined until it first answers, null on a hub without one. Asks if nothing has yet. */
export function useDirectionTree(): Snapshot {
  useEffect(() => {
    // after this commit's other effects, where the app opens its socket: the read then waits for that hello
    queueMicrotask(() => tree === undefined && void readDirectionTreeOnce());
  }, []);
  return useSyncExternalStore(subscribeDirectionTree, snapshot, snapshot);
}
