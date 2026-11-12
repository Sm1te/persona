// @vitest-environment jsdom
// The plan's pill above the composer (#5156): it keeps its place in the dock when "Jump to bottom" comes beside it or
// the phone turns on its side, so a flyout pinned open on it stays; and the pinned flyout's Esc closes it without
// stopping the running turn.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item, Session } from "../../lib/types";

const media = vi.hoisted(() => {
  const state = new Map<string, { matches: boolean; listeners: Set<() => void> }>();
  const entry = (q: string) => {
    let e = state.get(q);
    if (!e) state.set(q, (e = { matches: false, listeners: new Set() }));
    return e;
  };
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = (q: string) => ({
    get matches() {
      return entry(q).matches;
    },
    media: q,
    addEventListener: (_: string, l: () => void) => entry(q).listeners.add(l),
    removeEventListener: (_: string, l: () => void) => entry(q).listeners.delete(l),
  });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  // jsdom has no CSS namespace
  g.CSS ??= { escape: (s: string) => s.replace(/["\\]/g, "\\$&") };
  // the card is inside a folded summary, never drawn: the pill shows without any report
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
  return {
    set(q: string, matches: boolean) {
      const e = entry(q);
      e.matches = matches;
      e.listeners.forEach((l) => l());
    },
  };
});

const transcript = vi.hoisted(() => ({ items: [] as Item[], loading: false, error: "", hasMore: false, streaming: "" }));
vi.mock("./useTranscript", () => ({ useTranscript: () => transcript }));
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../lib/router", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/router")>(), useRoute: () => ({ name: "session", mid: "m", sid: "s" }) }));

const { ChatView } = await import("./ChatView");
const { reset, upsertSession } = await import("../../lib/store");
const { SHORT } = await import("../../lib/viewport");
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ts = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const todos = [
  { content: "读代码", status: "completed", activeForm: "读代码" },
  { content: "改实现", status: "in_progress", activeForm: "改实现" },
  { content: "补测试", status: "pending", activeForm: "补测试" },
];
const items: Item[] = [
  { id: "u1#0", seq: 1, role: "user", kind: "text", text: "按计划改", ts: ts(5) },
  { id: "tw#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "TodoWrite", tool_use_id: "c-tw", tool_input: { todos }, ts: ts(4) },
  { id: "tw-r#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "c-tw", text: "ok", ts: ts(4) },
  { id: "b#0", seq: 4, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "c-b", tool_input: { command: "go test ./..." }, ts: ts(1) },
];

let host: HTMLDivElement;
let root: Root;
let fetcher: ReturnType<typeof vi.fn>;
// the pill loads lazily (PlanCard.tsx): wait for that import too
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  await act(async () => { await vi.dynamicImportSettled(); });
  await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
};
const pill = () => host.querySelector<HTMLButtonElement>("[data-plan-pill]");
const flyout = () => document.querySelector<HTMLElement>("[data-plan-flyout]");
const interrupts = () => fetcher.mock.calls.filter(([url]) => String(url).includes("/interrupt"));

beforeEach(() => {
  localStorage.clear();
  transcript.items = items;
  upsertSession({ machine_id: "m", session_id: "s", title: "改实现", status: "working", state: "working", agent: "claude", cwd: "/work" } as Session);
  fetcher = vi.fn(async () => new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }));
  vi.stubGlobal("fetch", fetcher);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  media.set(SHORT, false);
  vi.unstubAllGlobals();
});

describe("the plan's pill in the dock (#5156)", () => {
  it("keeps a pinned flyout when Jump to bottom comes beside it and when the phone turns", async () => {
    act(() => root.render(<ChatView mid="m" sid="s" onToggleSidebar={() => {}} />));
    await flush();
    const before = pill();
    expect(before?.textContent).toBe("第 2 / 3 步");
    await act(async () => before!.click());
    expect(flyout()?.dataset.planFlyout).toBe("pinned");

    // the reader scrolls up: "Jump to bottom" goes beside the pill
    const sc = host.querySelector<HTMLElement>("[data-chat-scroll]")!;
    Object.defineProperty(sc, "scrollHeight", { configurable: true, value: 3000 });
    Object.defineProperty(sc, "clientHeight", { configurable: true, value: 500 });
    await act(async () => sc.dispatchEvent(new WheelEvent("wheel", { deltaY: -120, bubbles: true })));
    await flush();
    expect(host.querySelector("[data-plan-pills] [data-jump-pill]")).not.toBeNull();
    expect(pill()).toBe(before);
    expect(flyout()?.dataset.planFlyout).toBe("pinned");

    // the phone on its side: the pill shares a line with the run's status
    await act(async () => media.set(SHORT, true));
    await flush();
    expect(pill()).toBe(before);
    expect(flyout()?.dataset.planFlyout).toBe("pinned");
  });

  it("pinned, Esc closes the flyout and does not stop the running turn", async () => {
    act(() => root.render(<ChatView mid="m" sid="s" onToggleSidebar={() => {}} />));
    await flush();
    await act(async () => pill()!.click());
    expect(flyout()).not.toBeNull();
    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    await flush();
    expect(flyout()).toBeNull();
    expect(interrupts()).toHaveLength(0);
    // closed, Esc is the chat's again: it stops the turn
    await act(async () => window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true })));
    await flush();
    expect(interrupts()).toHaveLength(1);
  });
});
