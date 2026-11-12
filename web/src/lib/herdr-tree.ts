// The sidebar's "By machine" tree, laid out the way herdr arranges agents: machine → workspace (a herdr space) → tab (a
// slot) → panes. A Session is one agent pane; the sessions of one slot are that tab's panes, which the chat shows as
// tabs of their own (PaneTabs). Hosts without tabs (Paseo, botmux, runtimes) list their sessions right under the
// space; sessions outside any space go to the machine's "Other". Shell panes never reach the hub, so they are not here.
// A space the host nests under another (Orca's child worktrees, `parent_space_id`) is listed inside its parent, as
// Orca's sidebar does with "N children"; when the parent has no live agent the child stays at the top (#2073).
import { splitAgentSessions } from "./agent-groups";
import { stateOf, tabLabel } from "./format";
import { isSpacePinned, type AgentPins } from "./pins";
import type { Route, SessionView } from "./router";
import type { AgentState, Machine, Session } from "./types";
import { byTime } from "./times";
import { homeKey } from "./folded-rows";

// docs/protocol/agent-states.md "Unified states": what a folded row shows for everything under it, and which pane a tab opens on.
const URGENCY: AgentState[] = ["needs_you", "working", "unread", "idle", "unknown", "offline"];

const urgency = (s: Session) => URGENCY.indexOf(stateOf(s));

/** The most urgent of `list` (needs you > working > unread > seen); the first in order among equals. */
export function mostUrgent(list: readonly Session[]): Session | undefined {
  return list.reduce<Session | undefined>((best, s) => (!best || urgency(s) < urgency(best) ? s : best), undefined);
}

export function summaryState(list: readonly Session[]): AgentState | undefined {
  const s = mostUrgent(list);
  return s && stateOf(s);
}

// herdr counts its panes up in base 36 ("w4R:pW", "w4R:p2E", "w4R:p3A"): the order they were opened in. Herdr App's
// terminals are numbered in decimal ("herdr-core:t1", "herdr-core:t2"). Other hosts' ids ("botmux:<uuid>") carry no
// order and go last.
function paneNumber(s: Session): number {
  const herdr = s.pane_id?.match(/:p([0-9A-Za-z]+)$/);
  if (herdr) {
    const n = parseInt(herdr[1]!, 36);
    if (Number.isFinite(n)) return n;
  }
  const numbered = s.pane_id?.match(/(\d+)$/);
  return numbered ? Number(numbered[1]) : Number.MAX_SAFE_INTEGER;
}

const placeOf = (s: Session) => s.pane_index ?? Number.MAX_SAFE_INTEGER;

/** A tab's panes in their order: their place in the tab's layout (`pane_index`, #2320; panes without one after), then
 *  the order herdr opened them in (the number in the pane id), then the session id. */
export function paneOrder(a: Session, b: Session): number {
  return placeOf(a) - placeOf(b) || paneNumber(a) - paneNumber(b) || a.session_id.localeCompare(b.session_id);
}

/** The tab's "Lead" (#2320): the agent in the tab's first pane (`pane_index` 0, top left, the one the others were split
 *  from). None when the tab has one agent, or that pane is a shell or not placed: the agents are then peers. */
export function launcherOf(panes: readonly Session[]): Session | undefined {
  return panes.length > 1 ? panes.find((s) => s.pane_index === 0) : undefined;
}

/** A tab's agents as one entry (the overview's task list): the panes in order, the "Lead" if any, the most urgent. */
export interface TabGroup {
  key: string;
  sessions: Session[];
  launcher?: Session;
  lead: Session;
  state: AgentState;
}

/** `sessions` folded by tab (same machine, space and slot), each tab's panes in pane order; a session without a slot is
 *  an entry of its own. Entries keep the order of their first session in `sessions`. */
export function tabGroups(sessions: readonly Session[]): TabGroup[] {
  const byTab = groupBy([...sessions], (s) => (s.slot_id && s.space_id ? `${s.machine_id}/${s.space_kind ?? ""}:${s.space_id}/${s.slot_id}` : `s:${s.machine_id}/${s.session_id}`));
  return [...byTab].map(([key, panes]) => {
    panes.sort(paneOrder);
    const lead = mostUrgent(panes)!;
    return { key, sessions: panes, launcher: launcherOf(panes), lead, state: stateOf(lead) };
  });
}

const sameSpace = (a: Session, b: Session) => a.machine_id === b.machine_id && (a.space_kind ?? "") === (b.space_kind ?? "") && a.space_id === b.space_id;

/** The agents in the same tab as `s` (same machine, space and slot), in pane order; just `[s]` without a slot. */
export function tabSessions(all: Session[], s: Session): Session[] {
  if (!s.slot_id || !s.space_id) return [s];
  const tab = all.filter((x) => !x.assistant && sameSpace(x, s) && x.slot_id === s.slot_id && (x.session_id === s.session_id || stateOf(x) !== "offline"));
  if (!tab.some((x) => x.session_id === s.session_id)) tab.push(s);
  return tab.sort(paneOrder);
}

type HomeBucket = "needs" | "working" | "unread";
/** A pending card as the overview places it: on its session, or on the parent a child session folds under (#5120). */
type CardOf = Parameters<typeof homeKey>[0];

// How the overview orders a bucket's rows (and so which of a tab's equally urgent panes speaks for it): the longest
// working first, by when it started; the others the latest first, by when they last changed. A negative `before`
// puts `a` first.
const BUCKET_ORDER: Record<HomeBucket, (a: Session, b: Session) => number> = {
  working: (a, b) => byTime(a.state_since, b.state_since),
  needs: (a, b) => byTime(b.updated_at, a.updated_at),
  unread: (a, b) => byTime(b.updated_at, a.updated_at),
};

/** The overview's task rows (#2320): the live agents (`sessions` without the offline ones) folded by tab, filed under
 *  the most urgent state among them — needs you, working, done and not seen; a tab where all is seen is not listed.
 *  An agent waiting on one of `interactions` is that card's (listed above them), so it does not count towards its tab's
 *  state, though the tab still lists it. Each tab's `lead` is, of its panes in that state, the one the bucket orders
 *  first (the longest working, the latest to ask or finish): its time and words are the row's, and the rows are
 *  ordered by it the way single agents are — working tabs the longest working first, the others the latest first. */
export function homeTaskGroups(sessions: readonly Session[], interactions: readonly CardOf[]): Record<HomeBucket, TabGroup[]> {
  // a child session's card is its parent's (#5120): the parent waits in Needs you's cards, not in a row of its own too
  const asked = new Set(interactions.map(homeKey));
  const carded = (s: Session) => stateOf(s) === "needs_you" && asked.has(`${s.machine_id}/${s.session_id}`);
  const out: Record<HomeBucket, TabGroup[]> = { needs: [], working: [], unread: [] };
  for (const g of tabGroups(sessions.filter((s) => !s.assistant && stateOf(s) !== "offline"))) {
    const eligible = g.sessions.filter((s) => !carded(s));
    const state = summaryState(eligible);
    const bucket: HomeBucket | undefined = state === "needs_you" ? "needs" : state === "working" || state === "unread" ? state : undefined;
    if (!bucket) continue;
    // stable: among panes with the same time, the first in pane order
    const lead = eligible.filter((s) => stateOf(s) === state).sort(BUCKET_ORDER[bucket])[0]!;
    out[bucket].push({ ...g, lead, state: state! });
  }
  for (const b of Object.keys(out) as HomeBucket[]) out[b].sort((x, y) => BUCKET_ORDER[b](x.lead, y.lead));
  return out;
}

/** What the overview shows (Home): the task rows (homeTaskGroups); every unread agent, which "Mark all as seen" marks —
 *  all of them, as before tabs were rows, though some sit in tabs filed under needs-you or working — and where that
 *  action goes: "Done, not seen yet"'s header, beside the summary when that section is empty, nowhere when none is unread;
 *  and whether there is nothing to do (no card waiting either). */
export function homeOverview(sessions: readonly Session[], interactions: readonly CardOf[]) {
  const groups = homeTaskGroups(sessions, interactions);
  const unread = sessions.filter((s) => !s.assistant && stateOf(s) === "unread");
  const markAllAt: "section" | "summary" | undefined = !unread.length ? undefined : groups.unread.length ? "section" : "summary";
  const quiet = !interactions.length && !groups.needs.length && !groups.working.length && !groups.unread.length;
  return { groups, unread, markAllAt, quiet };
}

/** The sidebar keys a multi-agent tab's rows register under (the gliding highlight and keeping the open row in view):
 *  opened up, the tab's row is its own (`tabKey`) and each pane's row its agent's (`paneKeys`); folded (`shut`), the
 *  tab's row stands for its open pane (`activeKey`, when one of them is) and the pane rows, still mounted in the
 *  closed collapse, register under none (undefined) so no hidden row takes the visible one's place. */
export function tabRowKeys(tabKey: string, paneKeys: readonly string[], activeKey: string, shut: boolean): { row: string; panes: (string | undefined)[] } {
  const holds = paneKeys.includes(activeKey);
  return { row: shut && holds ? activeKey : tabKey, panes: paneKeys.map((k) => (shut ? undefined : k)) };
}

/** Where a pane opens: its tab (`/t/…`) when it has one, else the session itself (`/s/…`). */
export function paneRoute(s: Session): Route {
  return s.space_id && s.slot_id
    ? { name: "tab", mid: s.machine_id, space: s.space_id, slot: s.slot_id, sid: s.session_id }
    : { name: "session", mid: s.machine_id, sid: s.session_id };
}

/** The `/t/…` an old `/s/<mid>/<sid>` link stands for: its pane's tab, when the session is known and has one (#1996). */
export function tabOfSessionLink(all: Session[], r: { mid: string; sid: string; view?: SessionView }): Route | undefined {
  const s = all.find((x) => x.machine_id === r.mid && x.session_id === r.sid);
  const to = s && paneRoute(s);
  return to?.name === "tab" ? { ...to, ...(r.view && { view: r.view }) } : undefined;
}

/** What `/t/<mid>/<space>/<slot>[/<sid>]` opens: the asked-for pane when the tab has it, else the session `sid` (the
 *  tab's ids changed with a herdr restart), else the tab's most urgent pane; undefined when none of them is there. */
export function resolveTab(all: Session[], r: { mid: string; space: string; slot: string; sid?: string }): Session | undefined {
  const mine = all.filter((s) => s.machine_id === r.mid && !s.assistant);
  const tab = mine.filter((s) => s.space_id === r.space && s.slot_id === r.slot).sort(paneOrder);
  const asked = r.sid ? mine.find((s) => s.session_id === r.sid) : undefined;
  if (asked && tab.includes(asked)) return asked;
  return asked ?? mostUrgent(tab.filter((s) => stateOf(s) !== "offline")) ?? tab[0];
}

/** The conversation a `/t/…` page shows: the tab's pane, or — only while the session list is not in yet — the session
 *  its link names. Once the list is in and neither is there, none (the page says the tab is gone). */
export function tabPagePane(tabPane: Session | undefined, r: { mid: string; sid?: string }, loaded: boolean): { mid: string; sid: string } | undefined {
  if (tabPane) return { mid: tabPane.machine_id, sid: tabPane.session_id };
  return !loaded && r.sid ? { mid: r.mid, sid: r.sid } : undefined;
}

export interface HerdrTab {
  /** Unique while the tab lives (it has the host's ids): React keys and the sidebar's highlight. */
  key: string;
  spaceId: string;
  slotId: string;
  /** `#N` or the tab's name (format.ts tabLabel); empty when the host gives neither. */
  label: string;
  /** Whether the tab's row is opened up to its agents, kept by machine/space name/tab number/tab name like the space's fold. */
  foldKey: string;
  /** In pane order (paneOrder). */
  sessions: Session[];
  /** The agent the others were split from (launcherOf), when the tab says. */
  launcher?: Session;
  /** The pane the row speaks for and opens on: the most urgent. */
  lead: Session;
  state: AgentState;
}

export interface HerdrSpace {
  key: string;
  machineId: string;
  name: string;
  /** Folds are kept by machine/space name, like pins: herdr's workspace ids change when it restarts. */
  foldKey: string;
  sessions: Session[];
  tabs: HerdrTab[];
  /** Sessions of a host without tabs, listed right under the space. */
  loose: Session[];
  state?: AgentState;
  pinned: boolean;
  /** Spaces nested under this one (Orca's child worktrees), in the machine's space order. */
  children: HerdrSpace[];
  /** Its own sessions and every descendant's: what the space's row stands for when folded. `state` sums these. */
  tree: Session[];
  /** How many of its children (at any depth) need you, are working, or are done (unread or seen). */
  childStates: ChildStates;
}

export interface ChildStates {
  needs_you: number;
  working: number;
  done: number;
}

export interface HerdrMachine {
  key: string;
  machine: Machine;
  foldKey: string;
  spaces: HerdrSpace[];
  /** Sessions outside any space ("Other"), folded by `otherFoldKey`. */
  other: Session[];
  otherFoldKey: string;
  sessions: Session[];
  state?: AgentState;
  /** How many of its agents need you. */
  needs: number;
}

export interface MachineTreeOptions {
  pins?: AgentPins;
  /** Leave out machines with nothing under them (a quick filter is on). */
  hideEmpty?: boolean;
}

/** The live agents (`sessions` may hold any: assistants, offline ones and those of machines that are away are left
 *  out, as in splitAgentSessions) as machine → space → tab, every level in herdr's order; pinned spaces lead their
 *  machine. Machines keep the hub's order; an offline one is listed empty unless `hideEmpty`. */
export function buildMachineTree(machines: Machine[], sessions: Session[], opts: MachineTreeOptions = {}): HerdrMachine[] {
  const { live } = splitAgentSessions(sessions, machines);
  const byMachine = groupBy(live, (s) => s.machine_id);
  const out: HerdrMachine[] = [];
  for (const m of machines) {
    const mine = byMachine.get(m.id) ?? [];
    if (opts.hideEmpty && !mine.length) continue;
    const spaces = groupBy(mine.filter((s) => s.space_id), (s) => `${m.id}/${s.space_kind ?? ""}:${s.space_id}`);
    const built = [...spaces].map(([key, list]) => buildSpace(key, m.id, list, opts.pins));
    const index = (sp: HerdrSpace) => sp.sessions[0]!.space_index ?? Number.MAX_SAFE_INTEGER;
    built.sort((a, b) => Number(b.pinned) - Number(a.pinned) || index(a) - index(b) || a.name.localeCompare(b.name) || a.key.localeCompare(b.key));
    out.push({
      key: `machine:${m.id}`,
      machine: m,
      foldKey: `tree:${m.id}`,
      spaces: nestChildren(m.id, built),
      other: mine.filter((s) => !s.space_id).sort(paneOrder),
      otherFoldKey: `tree:${m.id}/其他`, // i18n-ignore: a stored fold key, not text
      sessions: mine,
      state: summaryState(mine),
      needs: mine.filter((s) => stateOf(s) === "needs_you").length,
    });
  }
  return out;
}

const spaceKey = (machineId: string, kind: string | undefined, id: string) => `${machineId}/${kind ?? ""}:${id}`;

/** `spaces` (sorted) with each child moved under its parent; a child whose parent is not there, or whose parents
 *  loop back to it, stays where it is. Fills every space's `tree`, `state` and `childStates` from its descendants. */
function nestChildren(machineId: string, spaces: HerdrSpace[]): HerdrSpace[] {
  const byKey = new Map(spaces.map((sp) => [sp.key, sp]));
  const parentOf = (sp: HerdrSpace): HerdrSpace | undefined => {
    const first = sp.sessions[0]!;
    const pid = first.parent_space_id;
    const parent = pid ? byKey.get(spaceKey(machineId, first.space_kind, pid)) : undefined;
    return parent === sp ? undefined : parent;
  };
  const loops = (sp: HerdrSpace) => {
    const seen = new Set<HerdrSpace>([sp]);
    for (let p = parentOf(sp); p; p = parentOf(p)) {
      if (seen.has(p)) return true;
      seen.add(p);
    }
    return false;
  };
  const top: HerdrSpace[] = [];
  for (const sp of spaces) {
    const parent = loops(sp) ? undefined : parentOf(sp);
    if (parent) parent.children.push(sp);
    else top.push(sp);
  }
  const fill = (sp: HerdrSpace): Session[] => {
    const below = sp.children.flatMap(fill);
    const counts: ChildStates = { needs_you: 0, working: 0, done: 0 };
    for (const c of sp.children) {
      for (const [k, v] of Object.entries(c.childStates) as [keyof ChildStates, number][]) counts[k] += v;
      const st = summaryState(c.sessions);
      if (st === "needs_you" || st === "working") counts[st]++;
      else if (st === "unread" || st === "idle") counts.done++;
    }
    sp.childStates = counts;
    sp.tree = [...sp.sessions, ...below];
    sp.state = summaryState(sp.tree);
    return sp.tree;
  };
  top.forEach(fill);
  return top;
}

function groupBy<T>(list: T[], key: (x: T) => string): Map<string, T[]> {
  const out = new Map<string, T[]>();
  for (const x of list) {
    const k = key(x);
    const at = out.get(k);
    if (at) at.push(x);
    else out.set(k, [x]);
  }
  return out;
}

function buildSpace(key: string, machineId: string, list: Session[], pins?: AgentPins): HerdrSpace {
  const first = list[0]!;
  const name = first.space_name || first.space_id || "";
  const slots = groupBy(list.filter((s) => s.slot_id), (s) => s.slot_id!);
  const loose = list.filter((s) => !s.slot_id);
  const tabs: HerdrTab[] = [...slots].map(([slotId, panes]) => {
    panes.sort(paneOrder);
    const lead = mostUrgent(panes)!;
    const label = tabLabel(panes[0]!);
    // by the tab's number, not its name: two tabs may share a name, a tab is renamed (the hub names a `#2` after its
    // task, people rename them), and herdr keeps the numbers across a restart (its ids it does not); a host without
    // numbers, by the tab's id
    const place = panes[0]!.slot_index ?? slotId;
    const foldKey = `tree:${machineId}/${name}/${place}`;
    return { key: `${key}/${slotId}`, spaceId: first.space_id!, slotId, label, foldKey, sessions: panes, launcher: launcherOf(panes), lead, state: stateOf(lead) };
  });
  const slotIndex = (t: HerdrTab) => t.sessions[0]!.slot_index ?? Number.MAX_SAFE_INTEGER;
  tabs.sort((a, b) => slotIndex(a) - slotIndex(b) || a.slotId.localeCompare(b.slotId));
  // a space's sessions in the order the sidebar shows them: its tabs' panes, then the ones without a tab
  const sessions = [...tabs.flatMap((t) => t.sessions), ...loose.sort(paneOrder)];
  return {
    key,
    machineId,
    name,
    foldKey: `tree:${machineId}/${name}`,
    sessions,
    tabs,
    loose,
    state: summaryState(sessions),
    pinned: Boolean(pins && isSpacePinned(machineId, name, pins)),
    children: [],
    tree: sessions,
    childStates: { needs_you: 0, working: 0, done: 0 },
  };
}
