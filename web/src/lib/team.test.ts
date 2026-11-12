// @vitest-environment jsdom
// A lead's Team (#5180, after cindy): who is a worker, which one the panel shows, and when it opens or closes by itself.
import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getPanel, openPanel } from "./panel";
import { askTeamWorker, isWorker, recordTeam, RECORDS_KEPT, TEAM_GONE_MS, shownWorker, showWorker, teamPanelOf, teamRecord, teamReveal, teamWorkers, useTeamReveal, workerCount } from "./team";
import type { Session } from "./types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let narrow = false;
const row = (id: string, extra: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id: id, title: id, cwd: "/code", status: "idle", state: "idle", ...extra }) as Session;
const worker = (id: string, index: number, extra: Partial<Session> = {}) => row(id, { folded_under: "lead", folded_as: "worker", space_id: "w1", slot_id: "t1", pane_index: index, ...extra });

beforeEach(() => {
  narrow = false;
  // lib/viewport.ts keeps one list per query: its `matches` reads the test's `narrow` each time
  window.matchMedia = ((q: string) => ({ get matches() { return q.includes("max-width: 1023px") ? narrow : false; }, media: q, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
  localStorage.clear();
  openPanel(null);
});

describe("teamReveal (cindy's timing)", () => {
  const r = (x: Partial<Parameters<typeof teamReveal>[0]>) => teamReveal({ workers: 2, before: undefined, record: undefined, wide: true, asked: false, ...x });
  it("opens by itself on arriving at a lead on a wide screen, unless it was closed here before", () => {
    expect(r({})).toBe("open");
    expect(r({ record: "open" })).toBe("open");
    expect(r({ record: "closed" })).toBeUndefined();
  });
  it("never opens by itself on a narrow screen, where it covers the page; a link to a worker opens it at any width", () => {
    expect(r({ wide: false })).toBeUndefined();
    expect(r({ wide: false, before: 0 })).toBeUndefined();
    expect(r({ wide: false, asked: true, record: "closed" })).toBe("open");
  });
  it("opens when the team starts while the page is on the lead, even if it was closed before", () => {
    expect(r({ before: 0, record: "closed" })).toBe("open");
    expect(r({ before: 1 })).toBeUndefined();
  });
  it("closes when the last worker goes, and does nothing for a session that never had one", () => {
    expect(r({ workers: 0, before: 2 })).toBe("close");
    expect(r({ workers: 0, before: 0 })).toBeUndefined();
    expect(r({ workers: 0, before: undefined, asked: true })).toBeUndefined();
  });
});

describe("the workers and the one shown", () => {
  const kids = [worker("b", 2), row("codex-sub", { folded_under: "lead", parent_session_id: "lead" }), worker("a", 1, { state: "working" }), worker("gone", 3, { state: "offline" }), worker("asks", 4, { state: "needs_you" })];
  it("are the running workers in pane order", () => {
    expect(teamWorkers(kids).map((s) => s.session_id)).toEqual(["a", "b", "asks"]);
    expect(isWorker(kids[1]!)).toBe(false);
    expect(workerCount(row("lead", { children: { total: 3, workers: 2 } }))).toBe(2);
    expect(workerCount(row("x", { children: { total: 3 } }))).toBe(0);
  });
  it("shows the one asked for, else the one picked last, else the first that needs you, else the first", () => {
    const ws = teamWorkers(kids);
    expect(shownWorker(ws, "mac", "lead")?.session_id).toBe("asks");
    expect(shownWorker(ws, "mac", "lead", "b")?.session_id).toBe("b");
    expect(shownWorker(ws, "mac", "lead", "nobody")?.session_id).toBe("asks");
    showWorker("mac", "lead", "a");
    expect(getPanel()).toEqual({ kind: "team", mid: "mac", sid: "lead", worker: "a" });
    expect(shownWorker(ws, "mac", "lead")?.session_id).toBe("a");
    expect(shownWorker(ws.filter((w) => w.state !== "needs_you" && w.session_id !== "a"), "mac", "other")?.session_id).toBe("b");
    expect(shownWorker([], "mac", "lead")).toBeUndefined();
  });
  it("says whose Team panel is open", () => {
    openPanel({ kind: "team", mid: "mac", sid: "lead" });
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeTruthy();
    expect(teamPanelOf(getPanel(), "mac", "other")).toBeUndefined();
    openPanel({ kind: "files", mid: "mac", sid: "lead", cwd: "/code" });
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeUndefined();
  });
});

describe("what was decided for a lead's panel", () => {
  it("is kept per lead, the latest few", () => {
    expect(teamRecord("mac", "l0")).toBeUndefined();
    recordTeam("mac", "l0", "closed");
    expect(teamRecord("mac", "l0")).toBe("closed");
    for (let i = 1; i <= RECORDS_KEPT; i++) recordTeam("mac", `l${i}`, "open");
    expect(teamRecord("mac", "l0")).toBeUndefined();
    expect(teamRecord("mac", `l${RECORDS_KEPT}`)).toBe("open");
  });
});

describe("useTeamReveal on the lead's page", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.useFakeTimers();
    host = document.createElement("div");
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    vi.useRealTimers();
  });
  const lead = (workers: number) => row("lead", { space_id: "w1", slot_id: "t1", pane_index: 0, children: workers ? { total: workers, workers } : undefined });
  function Page({ s, on = true }: { s?: Session; on?: boolean }) {
    useTeamReveal("mac", "lead", s, on);
    return null;
  }
  // the panel opens after the page's other effects (a microtask): `await act` lets it
  const render = async (s?: Session, on = true) => act(async () => root.render(createElement(Page, { s, on })));
  const gone = () => act(() => void vi.advanceTimersByTime(TEAM_GONE_MS));

  it("opens on arriving (once the row is in), keeps a hand-closed panel closed next time, and opens again for a new team", async () => {
    await render(undefined);
    expect(getPanel()).toBeNull();
    await render(lead(2));
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeTruthy();
    expect(teamRecord("mac", "lead")).toBe("open");
    act(() => openPanel(null));
    expect(teamRecord("mac", "lead")).toBe("closed");
    act(() => root.unmount());
    root = createRoot(host);
    await render(lead(2));
    expect(getPanel()).toBeNull();
    // the team ends and starts again while the page is on the lead: a new intent
    await render(lead(0));
    gone();
    await render(lead(1));
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeTruthy();
  });

  it("closes a moment after the team ends, without remembering that as the user's choice; a blip is no new team", async () => {
    await render(lead(2));
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeTruthy();
    act(() => openPanel(null));
    expect(teamRecord("mac", "lead")).toBe("closed");
    // the fold gone for one push (the lead's pane missing from a list): nothing closes, and nothing reopens
    await render(lead(0));
    await render(lead(2));
    gone();
    expect(getPanel()).toBeNull();
    // opened again by hand (the header's button records it open)
    act(() => {
      recordTeam("mac", "lead", "open");
      openPanel({ kind: "team", mid: "mac", sid: "lead" });
    });
    await render(lead(0));
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeTruthy();
    gone();
    expect(getPanel()).toBeNull();
    expect(teamRecord("mac", "lead")).toBe("open");
  });

  it("reads a row that came first without its rollup as an arrival, not a team starting", async () => {
    recordTeam("mac", "lead", "closed");
    await render(lead(0));
    await render(lead(2));
    expect(getPanel()).toBeNull();
    // a lead seen with no workers for a while: workers appearing then are a team starting
    act(() => root.unmount());
    root = createRoot(host);
    await render(row("lead", { space_id: "w1", slot_id: "t1", pane_index: 0, title: "solo" }));
    act(() => void vi.advanceTimersByTime(TEAM_GONE_MS));
    await render(lead(1));
    expect(teamPanelOf(getPanel(), "mac", "lead")).toBeTruthy();
  });

  it("does not replace a panel the page brought back on arriving, but a link to a worker does", async () => {
    openPanel({ kind: "files", mid: "mac", sid: "lead", cwd: "/code" });
    await render(lead(2));
    expect(getPanel()?.kind).toBe("files");
    await act(async () => void askTeamWorker("mac", "lead", "mate"));
    expect(getPanel()).toEqual({ kind: "team", mid: "mac", sid: "lead", worker: "mate" });
  });

  it("stays shut on a narrow screen until a link to a worker asks for it", async () => {
    narrow = true;
    await render(lead(2));
    expect(getPanel()).toBeNull();
    await act(async () => void askTeamWorker("mac", "lead", "mate"));
    expect(getPanel()).toEqual({ kind: "team", mid: "mac", sid: "lead", worker: "mate" });
  });

  it("does nothing in a worker's own conversation or a session without workers", async () => {
    await render(lead(2), false);
    expect(getPanel()).toBeNull();
    await render(lead(0));
    expect(getPanel()).toBeNull();
  });
});
