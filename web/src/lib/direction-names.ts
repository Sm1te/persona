// What each leader conversation's direction is called (docs/assistant/unified-entry.md "Leader conversations"): the
// sidebar's Directions group shows that name, not the conversation's title. `GET /api/v1/assistant` lists only the
// leaders' ids (`assistant.leaders`); the names come from `GET /api/v1/assistant/leaders` (#3519, #3695), which the hub
// works out (the name given, else the project's, else the title without its "MMDD | kind | " prefix).
// Asked again whenever the leaders or their titles change; until it answers, or on a hub that cannot, a row shows its title.
// Names and counts come from the hub's direction tree (GET /api/v1/assistant/directions/tree, #4352): each current
// direction in the leaders' order with its name and `counts` (Working and Waiting on you are shown, #4331). The tree is
// read and held in one place, lib/direction-tree.ts (the To-dos page and the Agents list draw the same one); this module
// only takes the names and counts from it whenever it changes. The hub sends `directions` whenever the tree changes (a
// leader made or ended anywhere, a name, a task's state), and the page reads the tree again then (readDirectionTree), so
// an open sidebar follows at once. A hub without the tree answers 404: the names come from GET
// /api/v1/assistant/leaders and there are no counts.
import { useEffect, useSyncExternalStore } from "react";
import { api } from "./api";
import { DIRECTION_TREE_PATH, directionTreeNow, readDirectionTreeOnce, refreshDirectionTree, resetDirectionTree, subscribeDirectionTree } from "./direction-tree";

export const LEADERS_PATH = "/assistant/leaders";
export const TREE_PATH = DIRECTION_TREE_PATH;

/** A direction as the tree lists it (only what the sidebar reads). */
interface TreeDirection {
  id: string;
  name?: string;
  secretary?: boolean;
  counts?: Partial<LeaderCounts>;
}

/** The tree's directions as leaders: the secretary group left out. */
export function treeLeaders(tree: { directions?: TreeDirection[] } | undefined): LeaderInfo[] {
  return (tree?.directions ?? []).filter((d) => d.id && !d.secretary).map((d) => ({ id: d.id, name: d.name, counts: { working: d.counts?.working ?? 0, needs_you: d.counts?.needs_you ?? 0 } }));
}

export interface LeaderInfo {
  id: string;
  title?: string;
  name?: string;
  project?: string;
  ended?: boolean;
  counts?: LeaderCounts;
}

/** A leader's open tasks in the To-dos page's Working and Waiting on you sections. */
export interface LeaderCounts {
  working: number;
  needs_you: number;
}

/** The names of the current leaders, by conversation id (ended ones and blank names left out). */
export function directionNames(list: readonly LeaderInfo[] | undefined): Map<string, string> {
  const out = new Map<string, string>();
  for (const l of list ?? []) {
    const name = l.name?.trim();
    if (l.id && name && !l.ended) out.set(l.id, name);
  }
  return out;
}

/** What a leader row says: its direction's name, else the conversation's own title. */
export function directionLabel(names: ReadonlyMap<string, string>, id: string, title: string): string {
  return names.get(id) ?? title;
}

/** The counts of the current leaders, by conversation id. */
export function leaderCounts(list: readonly LeaderInfo[] | undefined): Map<string, LeaderCounts> {
  const out = new Map<string, LeaderCounts>();
  for (const l of list ?? []) {
    if (l.id && !l.ended && l.counts) out.set(l.id, { working: l.counts.working ?? 0, needs_you: l.counts.needs_you ?? 0 });
  }
  return out;
}

/** What a direction's row says at its end: "在做 N · 等你 N", the parts that are not zero; "" with nothing open. */
export function countsLabel(c: LeaderCounts | undefined, t: (key: string, o: { count: number }) => string): string {
  if (!c) return "";
  return [c.working > 0 ? t("sidebar.leader_working", { count: c.working }) : "", c.needs_you > 0 ? t("sidebar.leader_needs_you", { count: c.needs_you }) : ""].filter(Boolean).join(" · ");
}

/** The counts of every leader added up (the folded section's tally). */
export function totalCounts(counts: ReadonlyMap<string, LeaderCounts>, ids: readonly string[]): LeaderCounts {
  const out = { working: 0, needs_you: 0 };
  for (const id of ids) {
    const c = counts.get(id);
    if (c) {
      out.working += c.working;
      out.needs_you += c.needs_you;
    }
  }
  return out;
}

const listeners = new Set<() => void>();
let names: Map<string, string> = new Map();
let counts: Map<string, LeaderCounts> = new Map();
let askedFor = "";
let asked = 0;

function set(list: readonly LeaderInfo[] | undefined) {
  names = directionNames(list);
  counts = leaderCounts(list);
  listeners.forEach((l) => l());
}

/** The current leaders with their names and counts, as the tree had them: taken at once, and remembered as asked for,
 *  so the next render does not ask again. Returns the leaders' ids in order. */
export function applyLeadersEvent(list: readonly LeaderInfo[] | undefined): string[] {
  asked++;
  set(list);
  const ids = (list ?? []).map((l) => l.id).filter(Boolean);
  toldIds = ids.join("\u0001");
  return ids;
}

/** The leaders the last tree named: while the page shows those, `directions` keeps their names and counts current (it
 *  comes again on any change), so nothing is asked for. */
let toldIds: string | null = null;
const idsOf = (key: string) => key.split("\u0001").map((k) => k.split("\u0000")[0]).filter(Boolean).join("\u0001");

/** Asks the hub for the names when `key` (the leaders and their titles) differs from the last ask. */
function ask(key: string) {
  if (key === askedFor) return;
  askedFor = key;
  if (toldIds !== null && idsOf(key) === toldIds) return;
  const n = ++asked;
  if (!key) return set([]);
  // the tree's names and counts arrive through followTree; only without a tree are the names asked for on their own
  readDirectionTreeOnce()
    .then((t) => (t ? undefined : api<{ leaders?: LeaderInfo[] }>(LEADERS_PATH).then((res) => n === asked && set(res.leaders ?? [])))) // an older hub: names only
    // a failed read: the rows keep their titles; the next change asks again
    .catch(() => n === asked && (askedFor = ""));
}

/** The tree's leaders, every time the tree changes (lib/direction-tree.ts reads it, for every screen at once). */
function followTree() {
  const t = directionTreeNow();
  if (t) applyLeadersEvent(treeLeaders(t));
}
subscribeDirectionTree(followTree);

/** Forget the names (another login, tests). */
export function resetDirectionNames() {
  askedFor = "";
  asked++;
  names = new Map();
  counts = new Map();
  toldIds = null;
  resetDirectionTree();
}

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};
const snapshot = () => names;

/** The direction names of `leaders` (ids), asked for again when `key` changes: pass the leaders with their titles,
 *  so a rename that changes a fallback name is picked up. */
export function useDirectionNames(key: string): ReadonlyMap<string, string> {
  useEffect(() => ask(key), [key]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

const countsSnapshot = () => counts;

/** The current leaders' counts (read with useDirectionNames, which asks for them). */
export function useLeaderCounts(): ReadonlyMap<string, LeaderCounts> {
  return useSyncExternalStore(subscribe, countsSnapshot, countsSnapshot);
}

/** Reads the tree (on every hello, `since` the socket opened: a read sent since then serves; with `changed`, on
 *  `directions`, once more after any read out): the leaders' ids in order, or null when it could not be read (an older
 *  hub, a failure), in which case nothing changes. The names and counts are already taken from it by then (followTree). */
export async function readDirectionTree(changed = false, since?: number): Promise<string[] | null> {
  const t = await (changed ? refreshDirectionTree() : readDirectionTreeOnce(since));
  return t ? treeLeaders(t).map((l) => l.id) : null;
}
