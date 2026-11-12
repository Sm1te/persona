import { describe, expect, it } from "vitest";
import { buildMachineTree, homeOverview, homeTaskGroups, launcherOf, mostUrgent, paneOrder, paneRoute, resolveTab, summaryState, tabGroups, tabOfSessionLink, tabPagePane, tabRowKeys } from "./herdr-tree";
import { keyedRefs } from "./keyed-refs";
import type { AgentState, Machine, Session } from "./types";

const mac: Machine = { id: "mac", name: "Mac", online: true };
const box: Machine = { id: "box", name: "Box", online: true };
const away: Machine = { id: "away", name: "Away", online: false };

let n = 0;
function pane(o: Partial<Session> & { space?: string; slot?: number; p?: number; st?: AgentState }): Session {
  const { space, slot, p, st = "idle", ...rest } = o;
  const sidx = space ? Number(space.replace(/\D/g, "")) || 1 : undefined;
  return {
    machine_id: "mac",
    session_id: `s${++n}`,
    title: `pane ${n}`,
    cwd: "/code",
    status: "idle",
    state: st,
    ...(space ? { space_kind: "herdr", space_id: space, space_name: `ws-${space}`, space_index: sidx } : {}),
    ...(space && slot !== undefined ? { slot_id: `${space}:t${slot}`, slot_index: slot, slot_name: String(slot), tab_id: `${space}:t${slot}` } : {}),
    ...(space && p !== undefined ? { pane_id: `${space}:p${p}` } : {}),
    ...rest,
  };
}

describe("buildMachineTree", () => {
  it("orders machines as given, spaces by index, tabs by slot index, panes by pane number then id", () => {
    const list = [
      pane({ space: "w2", slot: 1, p: 1 }),
      pane({ space: "w1", slot: 2, p: 10, session_id: "b" }),
      pane({ space: "w1", slot: 2, p: 2, session_id: "z" }),
      pane({ space: "w1", slot: 2, p: 2, session_id: "a" }),
      pane({ space: "w1", slot: 1, p: 1 }),
      pane({ machine_id: "box", space: "w1", slot: 1, p: 1 }),
    ];
    const tree = buildMachineTree([box, mac], list);
    expect(tree.map((m) => m.machine.id)).toEqual(["box", "mac"]);
    const m = tree[1]!;
    expect(m.spaces.map((s) => s.name)).toEqual(["ws-w1", "ws-w2"]);
    expect(m.spaces[0]!.tabs.map((t) => t.label)).toEqual(["#1", "#2"]);
    expect(m.spaces[0]!.tabs[1]!.sessions.map((s) => s.session_id)).toEqual(["a", "z", "b"]);
  });

  it("orders spaces by herdr's index before their names", () => {
    const tree = buildMachineTree([mac], [pane({ space: "w3", slot: 1, space_name: "alpha", space_index: 2 }), pane({ space: "w4", slot: 1, space_name: "zeta", space_index: 1 })]);
    expect(tree[0]!.spaces.map((s) => s.name)).toEqual(["zeta", "alpha"]);
  });

  it("orders spaces of equal index by name", () => {
    const tree = buildMachineTree([mac], [pane({ space: "w3", slot: 1, space_name: "zeta", space_index: 1 }), pane({ space: "w4", slot: 1, space_name: "alpha", space_index: 1 })]);
    expect(tree[0]!.spaces.map((s) => s.name)).toEqual(["alpha", "zeta"]);
  });

  it("lists a host without tabs right under its space, and sessions without a space under「其他」", () => {
    const bot = pane({ space_kind: "botmux", space_id: "dir:/code", space_name: "code", pane_id: "botmux:x" });
    const loose = pane({});
    const tree = buildMachineTree([mac], [bot, loose]);
    const sp = tree[0]!.spaces[0]!;
    expect(sp.tabs).toEqual([]);
    expect(sp.loose).toEqual([bot]);
    expect(tree[0]!.other).toEqual([loose]);
  });

  it("keeps two hosts' spaces with the same id apart", () => {
    const tree = buildMachineTree([mac], [pane({ space: "w1", slot: 1 }), pane({ space: "w1", slot: 1, space_kind: "orca" })]);
    expect(tree[0]!.spaces).toHaveLength(2);
  });

  it("sums up each level as its most urgent state: needs you > working > unread > seen", () => {
    const list = [
      pane({ space: "w1", slot: 1, p: 1, st: "unread" }),
      pane({ space: "w1", slot: 1, p: 2, st: "working" }),
      pane({ space: "w2", slot: 1, p: 1, st: "idle" }),
      pane({ space: "w3", slot: 1, p: 1, st: "needs_you" }),
      pane({ space: "w3", slot: 1, p: 2, st: "needs_you" }),
    ];
    const m = buildMachineTree([mac], list)[0]!;
    const tab = m.spaces[0]!.tabs[0]!;
    expect(tab.state).toBe("working");
    expect(tab.lead).toBe(list[1]);
    expect(m.spaces.map((s) => s.state)).toEqual(["working", "idle", "needs_you"]);
    expect(m.state).toBe("needs_you");
    expect(m.needs).toBe(2);
    expect(summaryState([pane({ st: "idle" }), pane({ st: "unread" })])).toBe("unread");
    expect(mostUrgent([])).toBeUndefined();
  });

  it("nests a child space under its parent and sums the children's states on the parent (#2073)", () => {
    const orca = (space: string, idx: number, st: AgentState, parent?: string) =>
      pane({ space, slot: 1, st, space_kind: "orca", space_index: idx, ...(parent ? { parent_space_id: parent } : {}) });
    const list = [
      orca("main", 1, "idle"),
      orca("a", 2, "needs_you", "main"),
      orca("b", 3, "working", "main"),
      orca("b1", 4, "unread", "b"),
      orca("c", 5, "idle", "main"),
      orca("other", 6, "idle"),
    ];
    const m = buildMachineTree([mac], list)[0]!;
    expect(m.spaces.map((s) => s.name)).toEqual(["ws-main", "ws-other"]);
    const main = m.spaces[0]!;
    expect(main.children.map((s) => s.name)).toEqual(["ws-a", "ws-b", "ws-c"]);
    expect(main.children[1]!.children.map((s) => s.name)).toEqual(["ws-b1"]);
    expect(main.childStates).toEqual({ needs_you: 1, working: 1, done: 2 });
    expect(main.state).toBe("needs_you");
    expect(main.sessions).toHaveLength(1);
    expect(main.tree).toHaveLength(5);
    expect(m.spaces[1]!.children).toEqual([]);
  });

  it("keeps a child at the top when its parent has no live agent, is another host's, or the parents loop", () => {
    const list = [
      pane({ space: "w1", slot: 1, space_kind: "orca", parent_space_id: "gone" }),
      pane({ space: "w2", slot: 1, space_kind: "orca", parent_space_id: "w3" }),
      pane({ space: "w3", slot: 1, space_kind: "orca", parent_space_id: "w2" }),
      pane({ space: "w4", slot: 1, space_kind: "herdr", parent_space_id: "w5" }),
      pane({ space: "w5", slot: 1, space_kind: "orca" }),
      pane({ space: "w6", slot: 1, space_kind: "orca", parent_space_id: "w6" }),
    ];
    const m = buildMachineTree([mac], list)[0]!;
    expect(m.spaces.map((s) => s.name)).toEqual(["ws-w1", "ws-w2", "ws-w3", "ws-w4", "ws-w5", "ws-w6"]);
    expect(m.spaces.every((s) => s.children.length === 0)).toBe(true);
  });

  it("leaves out assistant and offline sessions, and lists an offline machine empty", () => {
    const list = [pane({ space: "w1", slot: 1, assistant: true }), pane({ space: "w1", slot: 1, st: "offline" }), pane({ machine_id: "away", space: "w1", slot: 1, st: "working" })];
    const tree = buildMachineTree([mac, away], list);
    expect(tree.map((m) => [m.machine.id, m.sessions.length])).toEqual([["mac", 0], ["away", 0]]);
    expect(buildMachineTree([mac, away], list, { hideEmpty: true })).toEqual([]);
  });

  it("puts pinned spaces first on their machine", () => {
    const tree = buildMachineTree([mac], [pane({ space: "w1", slot: 1 }), pane({ space: "w2", slot: 1 })], { pins: { spaces: ["mac/ws-w2"], sessions: [] } });
    expect(tree[0]!.spaces.map((s) => [s.name, s.pinned])).toEqual([["ws-w2", true], ["ws-w1", false]]);
  });

  it("keys folds by machine and space name, so they survive a herdr restart", () => {
    const before = buildMachineTree([mac], [pane({ space: "w1", slot: 1, space_name: "app" })])[0]!;
    const after = buildMachineTree([mac], [pane({ space: "w7", slot: 1, space_name: "app" })])[0]!;
    expect(after.spaces[0]!.key).not.toBe(before.spaces[0]!.key);
    expect(after.spaces[0]!.foldKey).toBe(before.spaces[0]!.foldKey);
    expect(after.foldKey).toBe(before.foldKey);
  });
});

describe("resolveTab and paneRoute", () => {
  const a = pane({ space: "w1", slot: 1, p: 1, st: "idle" });
  const b = pane({ space: "w1", slot: 1, p: 2, st: "needs_you" });
  const c = pane({ space: "w2", slot: 1, p: 1 });
  const all = [a, b, c];

  it("opens the asked pane, else the tab's most urgent one", () => {
    expect(resolveTab(all, { mid: "mac", space: "w1", slot: "w1:t1", sid: a.session_id })).toBe(a);
    expect(resolveTab(all, { mid: "mac", space: "w1", slot: "w1:t1" })).toBe(b);
  });

  it("falls back to the session when the tab's ids changed, and to nothing", () => {
    expect(resolveTab(all, { mid: "mac", space: "w9", slot: "w9:t1", sid: c.session_id })).toBe(c);
    expect(resolveTab(all, { mid: "mac", space: "w9", slot: "w9:t1", sid: "nope" })).toBeUndefined();
    expect(resolveTab(all, { mid: "box", space: "w1", slot: "w1:t1" })).toBeUndefined();
  });

  it("links a pane with a tab to the tab, the rest to the session", () => {
    expect(paneRoute(a)).toEqual({ name: "tab", mid: "mac", space: "w1", slot: "w1:t1", sid: a.session_id });
    const s = pane({});
    expect(paneRoute(s)).toEqual({ name: "session", mid: "mac", sid: s.session_id });
  });
});

describe("tabOfSessionLink: an old /s/ link of a pane in a tab is that tab's /t/", () => {
  const a = pane({ space: "w1", slot: 1, p: 1 });
  const loose = pane({});
  it("names the tab and the pane", () => {
    expect(tabOfSessionLink([a, loose], { mid: "mac", sid: a.session_id })).toEqual({ name: "tab", mid: "mac", space: "w1", slot: "w1:t1", sid: a.session_id });
  });
  it("leaves a session without a tab, an unknown one and another machine's", () => {
    expect(tabOfSessionLink([a, loose], { mid: "mac", sid: loose.session_id })).toBeUndefined();
    expect(tabOfSessionLink([a], { mid: "mac", sid: "nope" })).toBeUndefined();
    expect(tabOfSessionLink([a], { mid: "box", sid: a.session_id })).toBeUndefined();
  });
});

describe("tabPagePane", () => {
  const r = { mid: "mac", sid: "gone" };
  it("waits on the link's session only while the list is not in", () => {
    expect(tabPagePane(undefined, r, false)).toEqual({ mid: "mac", sid: "gone" });
  });
  it("shows nothing once the list is in and the tab and its session are gone (the page says the tab is gone)", () => {
    expect(tabPagePane(undefined, r, true)).toBeUndefined();
    expect(tabPagePane(undefined, { mid: "mac" }, false)).toBeUndefined();
  });
  it("opens the tab's pane when it is there", () => {
    expect(tabPagePane({ machine_id: "mac", session_id: "a1" } as never, r, true)).toEqual({ mid: "mac", sid: "a1" });
  });
});

// #2320: several agents in one herdr tab are one group, in the order their panes sit, the first pane's the "Lead".
describe("panes of one tab", () => {
  const at = (id: string, o: Partial<Session> & { st?: AgentState } = {}) => pane({ space: "w4R", slot: 1, ...o, pane_id: `w4R:${id}` });

  it("orders by pane_index, then by the order herdr opened the panes (base 36 ids), then session id", () => {
    const a = at("p2E", { pane_index: 1 });
    const b = at("pW", { pane_index: 0 });
    const c = at("p31", { pane_index: 2 });
    expect([a, c, b].sort(paneOrder)).toEqual([b, a, c]);
    // without pane_index: herdr's real ids are base 36 counters (pW < p13 < p2E < p3A)
    const ids = ["p3A", "p2E", "pW", "p13"].map((id) => at(id));
    expect(ids.sort(paneOrder).map((s) => s.pane_id)).toEqual(["w4R:pW", "w4R:p13", "w4R:p2E", "w4R:p3A"]);
    // a pane the layout places goes before one it does not
    const placed = at("p3A", { pane_index: 1 });
    const unplaced = at("p2");
    expect([unplaced, placed].sort(paneOrder)).toEqual([placed, unplaced]);
  });

  it("the 发起者 is the agent in pane 0, only when the tab has several", () => {
    const first = at("pW", { pane_index: 0 });
    const branch = at("p2E", { pane_index: 1 });
    expect(launcherOf([first, branch])).toBe(first);
    expect(launcherOf([first])).toBeUndefined();
    // the top left pane is a shell: the agents are peers
    expect(launcherOf([branch, at("p31", { pane_index: 2 })])).toBeUndefined();
    expect(launcherOf([at("pA"), at("pB")])).toBeUndefined();
  });

  it("tabGroups folds a tab's agents into one entry in pane order, sessions without a slot stay alone", () => {
    const branch = at("p2E", { pane_index: 1, st: "needs_you" });
    const first = at("pW", { pane_index: 0, st: "working" });
    const loose = pane({ space: "w4R" });
    const other = pane({ space: "w4R", slot: 2, p: 1 });
    const groups = tabGroups([branch, loose, first, other]);
    expect(groups.map((g) => g.sessions)).toEqual([[first, branch], [loose], [other]]);
    expect(groups[0]!.launcher).toBe(first);
    expect(groups[0]!.lead).toBe(branch);
    expect(groups[0]!.state).toBe("needs_you");
  });

  it("buildMachineTree's tabs know their 发起者 and keep a fold key by number", () => {
    const tree = buildMachineTree([mac], [at("p2E", { pane_index: 1 }), at("pW", { pane_index: 0 })]);
    const tab = tree[0]!.spaces[0]!.tabs[0]!;
    expect(tab.sessions.map((s) => s.pane_index)).toEqual([0, 1]);
    expect(tab.launcher).toBe(tab.sessions[0]);
    expect(tab.foldKey).toBe("tree:mac/ws-w4R/1");
  });

  it("orders Herdr App terminals by their number when there is no pane_index", () => {
    const t1 = { ...at("pX", {}), pane_id: "herdr-core:t1", session_id: "z" } as Session;
    const t2 = { ...at("pY", {}), pane_id: "herdr-core:t2", session_id: "a" } as Session;
    expect([t2, t1].sort(paneOrder).map((s) => s.pane_id)).toEqual(["herdr-core:t1", "herdr-core:t2"]);
  });

  it("keeps a tab's fold through a rename (the hub names a `#2` after its task)", () => {
    const named = (slot_name: string) => buildMachineTree([mac], [pane({ space: "w1", slot: 2, p: 2, space_name: "app", slot_name })])[0]!.spaces[0]!.tabs[0]!;
    expect(named("修登录").foldKey).toBe(named("").foldKey);
  });

  it("keys a tab's fold by its number: two tabs of one name apart, both through a herdr restart", () => {
    const tabs = (space: string) =>
      buildMachineTree([mac], [pane({ space, slot: 1, p: 1, space_name: "app", slot_name: "review" }), pane({ space, slot: 2, p: 2, space_name: "app", slot_name: "review" })])[0]!.spaces[0]!.tabs;
    const before = tabs("w1"),
      after = tabs("w9");
    expect(before.map((t) => t.label)).toEqual(["review", "review"]);
    expect(before[0]!.foldKey).not.toBe(before[1]!.foldKey);
    expect(after.map((t) => t.foldKey)).toEqual(before.map((t) => t.foldKey));
    // a host without tab numbers: by the tab's id
    const unnumbered = buildMachineTree([mac], [pane({ space: "w1", slot: 1, slot_index: undefined, slot_name: "review" }), pane({ space: "w1", slot: 2, slot_index: undefined, slot_name: "review" })])[0]!.spaces[0]!.tabs;
    expect(unnumbered.map((t) => t.foldKey)).toEqual(["tree:mac/ws-w1/w1:t1", "tree:mac/ws-w1/w1:t2"]);
  });

  it("homeTaskGroups files each tab under its most urgent agent, leaving out what a card shows", () => {
    const first = at("pW", { pane_index: 0, st: "idle", updated_at: "2026-01-01T00:00:01Z" });
    const working = at("p2E", { pane_index: 1, st: "working", state_since: "2026-01-01T00:00:05Z" });
    const asking = at("p31", { pane_index: 2, st: "needs_you" });
    const solo = pane({ space: "w1", slot: 3, p: 1, st: "working", state_since: "2026-01-01T00:00:01Z" });
    const done = pane({ space: "w1", slot: 4, p: 1, st: "unread" });
    const quiet = pane({ space: "w1", slot: 5, p: 1, st: "idle" });
    const gone = pane({ space: "w1", slot: 6, p: 1, st: "offline" });
    const g = homeTaskGroups([first, working, asking, solo, done, quiet, gone], []);
    expect(g.needs.map((x) => x.sessions)).toEqual([[first, working, asking]]);
    expect(g.needs[0]!.lead).toBe(asking);
    expect(g.working.map((x) => x.lead)).toEqual([solo]);
    expect(g.unread.map((x) => x.lead)).toEqual([done]);
    // the asking agent has a card: its tab is filed by what the others do
    const carded = homeTaskGroups([first, working, asking, solo], [{ machine_id: "mac", session_id: asking.session_id }]);
    expect(carded.needs).toEqual([]);
    expect(carded.working.map((x) => x.lead)).toEqual([solo, working]);
    expect(carded.working[1]!.sessions).toHaveLength(3);
  });

  it("homeTaskGroups: a tab of two working panes is as long working as its longest, and shows that one", () => {
    // tab A: pane 0 working since 10:59, pane 1 since 10:00; tab B: one pane since 10:30
    const a0 = at("pW", { pane_index: 0, st: "working", state_since: "2026-01-01T10:59:00Z" });
    const a1 = at("p2E", { pane_index: 1, st: "working", state_since: "2026-01-01T10:00:00Z" });
    const b = pane({ space: "w1", slot: 2, p: 1, st: "working", state_since: "2026-01-01T10:30:00Z" });
    const g = homeTaskGroups([a0, a1, b], []);
    expect(g.working.map((x) => x.lead)).toEqual([a1, b]);
    expect(g.working[0]!.sessions).toEqual([a0, a1]);
  });

  it("homeTaskGroups: a tab of two unread panes is as new as its newest, and shows that one", () => {
    const old0 = at("pW", { pane_index: 0, st: "unread", updated_at: "2026-01-01T09:00:00Z" });
    const new1 = at("p2E", { pane_index: 1, st: "unread", updated_at: "2026-01-01T11:00:00Z" });
    const other = pane({ space: "w1", slot: 2, p: 1, st: "unread", updated_at: "2026-01-01T10:00:00Z" });
    const g = homeTaskGroups([old0, new1, other], []);
    expect(g.unread.map((x) => x.lead)).toEqual([new1, other]);
    // the same time: the first in pane order
    const tie0 = at("pW", { pane_index: 0, st: "unread", updated_at: "2026-01-01T09:00:00Z" });
    const tie1 = at("p2E", { pane_index: 1, st: "unread", updated_at: "2026-01-01T09:00:00Z" });
    expect(homeTaskGroups([tie1, tie0], []).unread[0]!.lead).toBe(tie0);
  });

  it("homeOverview: a card waiting is something to do, though no bucket lists its agent", () => {
    const asking = at("pW", { st: "needs_you" });
    const idle = pane({ space: "w1", slot: 2, p: 1, st: "idle" });
    const o = homeOverview([asking, idle], [{ machine_id: "mac", session_id: asking.session_id }]);
    expect(o.groups.needs).toEqual([]);
    expect(o.quiet).toBe(false);
    expect(homeOverview([idle], []).quiet).toBe(true);
  });

  it("homeOverview: 全部标为已看 marks every unread agent, beside the summary when their tabs are filed elsewhere", () => {
    const working = at("pW", { pane_index: 0, st: "working" });
    const doneA = at("p2E", { pane_index: 1, st: "unread" });
    const asking = pane({ space: "w1", slot: 2, p: 1, pane_index: 0, st: "needs_you" });
    const doneB = pane({ space: "w1", slot: 2, p: 2, pane_index: 1, st: "unread" });
    const o = homeOverview([working, doneA, asking, doneB], []);
    expect(o.groups.unread).toEqual([]);
    expect(o.groups.working.map((g) => g.lead)).toEqual([working]);
    expect(o.groups.needs.map((g) => g.lead)).toEqual([asking]);
    expect(o.unread).toEqual([doneA, doneB]);
    expect(o.markAllAt).toBe("summary");
    // with a "Finished" section: in its header, still every unread agent
    const solo = pane({ space: "w1", slot: 3, p: 1, st: "unread" });
    const withSection = homeOverview([working, doneA, solo], []);
    expect(withSection.groups.unread.map((g) => g.lead)).toEqual([solo]);
    expect(withSection.unread).toEqual([doneA, solo]);
    expect(withSection.markAllAt).toBe("section");
    expect(homeOverview([working], []).markAllAt).toBeUndefined();
  });
});

// A folded tab's pane rows stay mounted in the closed collapse: only the visible row may hold the open pane's key, or
// the highlight and the scroll to the open row land on a hidden row (#2320).
describe("a multi-agent tab's rows in the sidebar's registry", () => {
  type Row = { name: string };
  const tabRow: Row = { name: "tab" },
    paneRows: Row[] = [{ name: "pane 0" }, { name: "pane 1" }];
  const paneKeys = ["s:mac/a", "s:mac/b"];
  // one render of TabRow: each row's ref under its key (none for a hidden pane); the refs it had before let go first,
  // as React does
  const render = (reg: (k: string) => (el: Row | null) => void, shut: boolean, before: ((el: Row | null) => void)[]) => {
    before.forEach((r) => r(null));
    const keys = tabRowKeys("t:tab", paneKeys, "s:mac/b", shut);
    const mounted: [string, Row][] = [[keys.row, tabRow], ...keys.panes.flatMap((k, i): [string, Row][] => (k ? [[k, paneRows[i]!]] : []))];
    return mounted.map(([k, el]) => {
      const ref = reg(k);
      ref(el);
      return ref;
    });
  };

  it("folded, the tab's row holds the open pane's key; opened up, that pane's row does", () => {
    const rows = new Map<string, Row>();
    const reg = keyedRefs(rows);
    let refs = render(reg, true, []);
    expect(rows.get("s:mac/b")).toBe(tabRow);
    expect([...rows.keys()]).toEqual(["s:mac/b"]);
    refs = render(reg, false, refs);
    expect(rows.get("s:mac/b")).toBe(paneRows[1]);
    expect(rows.get("t:tab")).toBe(tabRow);
    expect(rows.get("s:mac/a")).toBe(paneRows[0]);
    render(reg, true, refs);
    expect(rows.get("s:mac/b")).toBe(tabRow);
    expect(rows.has("t:tab")).toBe(false);
  });

  it("a row letting go of a key takes out only its own entry", () => {
    const rows = new Map<string, Row>();
    const reg = keyedRefs(rows);
    const first = reg("k"),
      second = reg("k");
    first(tabRow);
    second(paneRows[0]!);
    first(null);
    expect(rows.get("k")).toBe(paneRows[0]);
    second(null);
    expect(rows.has("k")).toBe(false);
  });

  it("a tab not holding the open pane keeps its own key either way", () => {
    expect(tabRowKeys("t:tab", paneKeys, "s:mac/z", true)).toEqual({ row: "t:tab", panes: [undefined, undefined] });
    expect(tabRowKeys("t:tab", paneKeys, "s:mac/z", false)).toEqual({ row: "t:tab", panes: paneKeys });
  });
});
