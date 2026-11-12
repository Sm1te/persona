// @vitest-environment jsdom
// #2602: every conversation shares one collapsed summary and one replace-in-place latest text slot.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { emptyTaskIndex } from "../../lib/tasks";
import type { Item } from "../../lib/types";
import { segments, segmentIdentity, type Segment, type Step } from "../../lib/turns";
import type { WorkLineStyle } from "../../lib/work-groups";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { forgetExpanded, openExpanded } from "./expanded";
import { SegmentView } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const T0 = Date.parse("2026-11-04T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
const tool = (n: number, done = true): Step => ({
  kind: "tool", id: `t${n}#0`,
  use: { id: `t${n}#0`, seq: n, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: `c-${n}`, tool_input: { command: `echo ${n}` }, ts: at(n) },
  result: done ? { id: `t${n}-r#0`, seq: n, role: "user", kind: "tool_result", tool_use_id: `c-${n}`, text: "ok", ts: at(n + 1) } : undefined,
  images: [],
});
const words = (n: number, text: string): Step => {
  const item: Item = { id: `a${n}#0`, seq: n, role: "assistant", kind: "text", text, ts: at(n) };
  return { kind: "text", id: item.id, item };
};
const seg = (steps: Step[]): Segment => ({ id: "seg", steps, tools: steps.length, thoughts: 0, replies: 0, folds: [], start: at(0) });
let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  forgetExpanded();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });
function draw(style: WorkLineStyle, s: Segment, live: boolean, expand: (f: Item) => Promise<void> = async () => {}, streaming?: string) {
  const ctx: ChatContextValue = { tasks: emptyTaskIndex(), subagents: [], scope: "x:", lineStyle: style };
  act(() => root.render(<ChatContext.Provider value={ctx}><SegmentView key={segmentIdentity(s)} seg={s} live={live} mid="m" sid="s" expandFold={expand} streaming={streaming} /></ChatContext.Provider>));
}
const header = () => host.querySelector<HTMLButtonElement>("[data-work-group] > button")!;
const detail = () => host.querySelector("[data-work-detail]");
const detailText = () => detail()?.textContent ?? "";
const slot = () => host.querySelector("[data-latest-text]");
const tap = () => act(() => header().click());
const running = [tool(1), words(2, "First progress."), tool(3), words(4, "Latest progress."), tool(5, false)];
const finished = [...running.slice(0, -1), tool(5), words(9, "Final result.")];

describe.each(["assistant", "agents"] as const)("%s compact execution", (style) => {
  it("starts folded with one latest text, no tool or thinking preview", () => {
    draw(style, seg(running), true);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    expect(detail()).toBeNull();
    expect(slot()?.textContent).toContain("Latest progress.");
    expect(host.textContent).not.toContain("First progress.");
    expect(host.textContent).not.toContain("echo 5");
    expect(host.querySelectorAll("[data-work-group]")).toHaveLength(1);
    expect(host.querySelector("[data-live-preview]")).toBeNull();
  });
  it("replaces text in the same DOM slot, retaining older text for manual inspection", () => {
    draw(style, seg(running), true);
    const originalSlot = slot();
    draw(style, seg([...running, words(6, "New progress."), tool(7, false)]), true);
    expect(slot()).toBe(originalSlot);
    expect(slot()?.textContent).toContain("New progress.");
    expect(host.textContent).not.toContain("Latest progress.");
    tap();
    expect(detailText()).toContain("First progress.");
    expect(detailText()).toContain("Latest progress.");
    for (const n of [1, 3, 5, 7]) expect(detailText()).toContain(`echo ${n}`);
  });
  it("folds long intermediate Markdown too, keeping only the final result", () => {
    draw(style, seg([tool(1), words(2, `## Intermediate report\n${"Example progress. ".repeat(50)}`), tool(3), words(4, "Final result.")]), false);
    expect(host.textContent).not.toContain("Intermediate report");
    expect(slot()?.textContent).toContain("Final result.");
    tap();
    expect(detailText()).toContain("Intermediate report");
  });
  it("opens a single running call manually; later calls do not reset the choice", () => {
    draw(style, seg([tool(1, false)]), true);
    expect(header().disabled).toBe(false);
    expect(detail()).toBeNull();
    tap();
    expect(detailText()).toContain("echo 1");
    draw(style, seg([tool(1), tool(2, false)]), true);
    expect(header().getAttribute("aria-expanded")).toBe("true");
    expect(detailText()).toContain("echo 2");
    tap();
    draw(style, seg([tool(1), tool(2), tool(3, false)]), true);
    expect(header().getAttribute("aria-expanded")).toBe("false");
  });
  it("completion collapses a manually opened run and replaces progress with final text in place", () => {
    draw(style, seg(running), true);
    const originalSlot = slot();
    tap();
    draw(style, seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    expect(slot()).toBe(originalSlot);
    expect(slot()?.textContent).toContain("Final result.");
    tap();
    for (const n of [1, 3, 5]) expect(detailText()).toContain(`echo ${n}`);
    expect(detailText()).toContain("Latest progress.");
    tap();
    expect(header().getAttribute("aria-expanded")).toBe("false");
  });
  it("does not restore a historical expansion when a resumed untouched run completes", () => {
    draw(style, seg(finished), false);
    tap();
    expect(header().getAttribute("aria-expanded")).toBe("true");
    draw(style, seg([...finished, tool(10, false)]), true);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    draw(style, seg([...finished, tool(10), words(12, "Resumed result.")]), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    tap();
    expect(detailText()).toContain("echo 10");
  });
  it("streaming replaces persisted narration, then commits the final text without a duplicate", () => {
    draw(style, seg(running), true);
    const originalSlot = slot();
    draw(style, seg(running), true, undefined, "New streamed progress.");
    expect(slot()).toBe(originalSlot);
    expect(slot()?.querySelectorAll("[data-answer]")).toHaveLength(1);
    expect(slot()?.textContent).toContain("New streamed progress.");
    expect(host.textContent).not.toContain("Latest progress.");
    tap();
    expect(detailText()).toContain("Latest progress.");
    draw(style, seg(finished), false);
    expect(slot()).toBe(originalSlot);
    expect(slot()?.querySelectorAll("[data-answer]")).toHaveLength(1);
    expect(slot()?.textContent).toContain("Final result.");
  });
  it("keeps a text-only summary open when its first tool arrives", () => {
    const steps = [words(1, "Earlier text."), words(2, "Latest text.")];
    draw(style, seg(steps), true);
    tap();
    expect(detailText()).toContain("Earlier text.");
    draw(style, seg([...steps, tool(3, false)]), true);
    expect(header().getAttribute("aria-expanded")).toBe("true");
    expect(detailText()).toContain("echo 3");
  });
  it("does not drop earlier text across an unloaded history gap", () => {
    const first = words(1, "Earlier history text.");
    const later = words(2, "Latest history text.") as Extract<Step, { kind: "text" }>;
    draw(style, seg([first, { ...later, item: { ...later.item, ts: at(3600) } }]), false);
    expect(host.textContent).toContain("Earlier history text.");
    expect(slot()?.textContent).toContain("Latest history text.");
  });
  it("jump-to-call opens a running summary and completion still collapses it", () => {
    draw(style, seg(running), true);
    const id = host.querySelector<HTMLElement>("[data-work-group]")!.dataset.groupId!;
    act(() => openExpanded([`x:${id}`]));
    expect(header().getAttribute("aria-expanded")).toBe("true");
    expect(detailText()).toContain("echo 1");
    draw(style, seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
  });
  it("all tool kinds and every plan update stay inside one summary until manually opened", async () => {
    const named = (n: number, name: string, input: Record<string, unknown>): Step => {
      const s = tool(n) as Extract<Step, { kind: "tool" }>;
      return { ...s, use: { ...s.use, tool_name: name, tool_input: input } };
    };
    const steps = [
      named(1, "TodoWrite", { todos: [{ content: "First plan", status: "pending" }, { content: "Second step", status: "pending" }] }),
      named(2, "TodoWrite", { todos: [{ content: "Revised plan", status: "pending" }, { content: "Last step", status: "pending" }] }),
      named(3, "Agent", { description: "Example subagent", prompt: "Check the example" }),
      named(4, "Workflow", { name: "Example workflow" }), words(5, "Latest text."),
    ];
    draw(style, seg(steps), true);
    expect(host.querySelectorAll("[data-work-group]")).toHaveLength(1);
    expect(host.textContent).not.toContain("Example subagent");
    expect(host.textContent).not.toContain("Example workflow");
    await act(async () => { header().click(); await vi.dynamicImportSettled(); });
    expect(detailText()).toContain("Example subagent");
    expect(detailText()).toContain("Example workflow");
    // one plan card, the latest list (#5156, cindy); the earlier update stays inspectable as a row
    const cards = detail()!.querySelectorAll("[data-plan-card]");
    expect(cards).toHaveLength(1);
    expect(cards[0].textContent).toContain("Revised plan");
    expect(cards[0].textContent).not.toContain("First plan");
    expect(detail()!.querySelector('[data-action-row="TodoWrite"]')).not.toBeNull();
  });
  it("reloaded completed history starts folded and remains manually inspectable", () => {
    draw(style, seg(finished), false);
    expect(header().getAttribute("aria-expanded")).toBe("false");
    tap();
    expect(detailText()).toContain("First progress.");
    expect(detailText()).toContain("echo 1");
  });
});

const fold = (a: number, b: number): Step => {
  const item: Item = { id: `fold:t${a}#0`, seq: a, role: "assistant", kind: "fold", ts: at(a), fold: { items: (b - a + 1) * 2, tools: b - a + 1, thoughts: 0, until: at(b + 1), through_seq: b } };
  return { kind: "fold", id: item.id, item };
};
const calls = (a: number, b: number) => Array.from({ length: b - a + 1 }, (_, i) => tool(a + i));
function foldingChat(style: WorkLineStyle, start: Step[], live: boolean) {
  let steps = start;
  const expand = vi.fn(async (f: Item) => {
    steps = steps.flatMap((s) => s.kind === "fold" && s.item.id === f.id ? calls(f.seq, f.fold!.through_seq) : [s]);
    draw(style, seg(steps), live, expand);
  });
  return { expand, draw: () => draw(style, seg(steps), live, expand) };
}
const settle = async () => { for (let i = 0; i < 10; i++) await act(() => new Promise<void>((r) => setTimeout(r, 2))); };
describe.each(["assistant", "agents"] as const)("%s folded history", (style) => {
  it("keeps manual expansion when a fold resolves to a nonzero tool block", async () => {
    const folded = fold(1, 1);
    const text = words(3, "Latest text.");
    const first = tool(1) as Extract<Step, { kind: "tool" }>;
    const expanded = { ...first, id: "t1#1", use: { ...first.use, id: "t1#1" } };
    const sourceSegment = (steps: Step[]) => segments(steps.flatMap((s) => s.kind === "tool" ? [s.use, ...(s.result ? [s.result] : [])] : [s.item]))[0];
    const before = sourceSegment([folded, text]);
    const after = sourceSegment([expanded, text]);
    expect(before.id).not.toBe(after.id);
    const expand = vi.fn(async () => draw(style, after, true, expand));
    draw(style, before, true, expand);
    const originalSlot = slot();
    tap();
    await settle();
    expect(slot()).toBe(originalSlot);
    expect(expand).toHaveBeenCalledTimes(1);
    expect(header().getAttribute("aria-expanded")).toBe("true");
    expect(detailText()).toContain("echo 1");
  });
  it.each([true, false])("fetches folded tools only when manually opened (live=%s)", async (live) => {
    const chat = foldingChat(style, [fold(1, 3), words(4, "Earlier progress."), fold(5, 7), words(8, "Latest text.")], live);
    chat.draw();
    await settle();
    expect(chat.expand).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("Earlier progress.");
    tap();
    await settle();
    expect(new Set(chat.expand.mock.calls.map(([f]) => f.seq))).toEqual(new Set([1, 5]));
    for (const n of [1, 2, 3, 5, 6, 7]) expect(detailText()).toContain(`echo ${n}`);
    expect(detailText()).toContain("Earlier progress.");
    expect(host.querySelectorAll('[data-work-group] > button span[style*="width: 11px"]')).toHaveLength(0);
  });
});
