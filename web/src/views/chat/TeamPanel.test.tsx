// @vitest-environment jsdom
// A lead's Team panel (#5180, after cindy's Team tab): the strip of its workers, the one shown filled, and that worker's
// own conversation under it; with no worker running, cindy's "Waiting for worker".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
});
const shown = vi.hoisted(() => ({ sid: "", embedded: false }));
vi.mock("./ChatView", () => ({
  ChatView: ({ sid, embedded }: { sid: string; embedded?: boolean }) => {
    shown.sid = sid;
    shown.embedded = Boolean(embedded);
    return <div data-worker-chat={sid} />;
  },
}));
import { applyCompleteSessions, reset } from "../../lib/store";
import { getPanel, openPanel } from "../../lib/panel";
import type { Session } from "../../lib/types";
import { nextTab, stripStep, TeamPanel, workerHint } from "./TeamPanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const row = (id: string, extra: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id: id, title: id, cwd: "/code", status: "idle", state: "idle", space_id: "w1", slot_id: "t1", ...extra }) as Session;
const lead = (workers: number) => row("lead", { pane_index: 0, title: "Refactor the login flow", children: workers ? { total: workers, workers, working: 1 } : undefined });
const worker = (id: string, index: number, extra: Partial<Session> = {}) => row(id, { pane_index: index, folded_under: "lead", folded_as: "worker", ...extra });

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  shown.sid = "";
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  openPanel(null);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
});

const pills = () => [...host.querySelectorAll<HTMLButtonElement>("[role=tab]")];

describe("TeamPanel", () => {
  it("lists the running workers in pane order, opens on the one that needs you, and shows its conversation", () => {
    act(() => applyCompleteSessions([
      lead(3),
      worker("tests", 2, { title: "Add the refresh tests", state: "working", status: "working" }),
      worker("review", 1, { title: "Review the change", agent: "codex", state: "needs_you", status: "needs_you" }),
      worker("gone", 3, { state: "offline", status: "offline" }),
      // a Codex subagent folded under the lead too: a card in its chat, not a worker
      row("sub", { folded_under: "lead", parent_session_id: "lead", is_subagent: true }),
    ]));
    act(() => root.render(<TeamPanel mid="mac" sid="lead" onClose={() => {}} />));
    expect(pills().map((p) => p.textContent)).toEqual(["Review the change", "Add the refresh tests"]);
    expect(pills().map((p) => p.getAttribute("aria-selected"))).toEqual(["true", "false"]);
    expect(pills()[0]!.className).toContain("bg-ink");
    expect(host.querySelector("[role=tablist]")?.getAttribute("aria-label")).toBe("子 Agent");
    expect(shown).toEqual({ sid: "review", embedded: true });
    expect(host.textContent).toContain("子 Agent 2 个 · 1 个在跑");
    // picking a pill shows that worker: the panel remembers it
    act(() => pills()[1]!.click());
    expect(getPanel()).toEqual({ kind: "team", mid: "mac", sid: "lead", worker: "tests" });
  });

  it("shows the worker it was asked for, and waits for a worker when none runs", () => {
    act(() => applyCompleteSessions([lead(2), worker("a", 1), worker("b", 2)]));
    act(() => root.render(<TeamPanel mid="mac" sid="lead" worker="b" onClose={() => {}} />));
    expect(shown.sid).toBe("b");
    // the team ended: the store holds the lead alone
    act(() => {
      reset();
      applyCompleteSessions([lead(0)], [], { loaded: true });
    });
    act(() => root.render(<TeamPanel mid="mac" sid="lead" onClose={() => {}} />));
    expect(pills()).toHaveLength(0);
    expect(host.textContent).toContain("等待子 Agent");
  });

  it("moves between pills with the arrow keys, and scrolls most of a strip's width at a time", () => {
    expect(nextTab(0, "ArrowRight", 3)).toBe(1);
    expect(nextTab(2, "ArrowRight", 3)).toBe(0);
    expect(nextTab(0, "ArrowLeft", 3)).toBe(2);
    expect(nextTab(1, "Home", 3)).toBe(0);
    expect(nextTab(1, "End", 3)).toBe(2);
    expect(nextTab(1, "Enter", 3)).toBeUndefined();
    expect(stripStep(300)).toBe(210);
    expect(stripStep(50)).toBe(80);
  });

  it("says in a pill's hover text which Agent it is, its title and its state", () => {
    expect(workerHint(worker("r", 1, { agent: "codex", title: "Review", state: "needs_you" }), false)).toBe("Codex · Review · 需要你");
  });
});
