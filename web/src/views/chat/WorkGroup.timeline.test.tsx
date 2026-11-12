// @vitest-environment jsdom
// #5155: an opened summary is cindy's timeline: the narration in order and an inner summary for each run of work
// between, opening with it (super rule 2: opening the summary shows every tool call); thoughts with words are rows.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const live = vi.hoisted(() => ({ thinking: false }));
vi.mock("../../lib/live", async (load) => ({ ...(await load<typeof import("../../lib/live")>()), useSessionLive: () => (live.thinking ? { thinking: true } : null) }));

import { emptyTaskIndex } from "../../lib/tasks";
import type { Item } from "../../lib/types";
import { segmentIdentity, type Segment, type Step } from "../../lib/turns";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { forgetExpanded } from "./expanded";
import { revealItem, undoReveals } from "./search-reveal";
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
const thought = (n: number, text: string): Step => {
  const item: Item = { id: `th${n}#0`, seq: n, role: "assistant", kind: "thinking", text, ts: at(n) };
  return { kind: "thinking", id: item.id, item };
};
const seg = (steps: Step[]): Segment => ({ id: "seg", steps, tools: steps.length, thoughts: 0, replies: 0, folds: [], start: at(0) });
// a thought without words, one with, a run, the narration, a second run with a long thought, the answer
const finished = [thought(1, ""), thought(2, "Read the config first."), tool(3), words(5, "Found the setting."), tool(6), thought(8, "Line one\nline `two`"), tool(9), words(12, "Final result.")];
const running = [...finished.slice(0, -1), words(12, "Checking once more."), tool(13, false)];

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  forgetExpanded();
  live.thinking = false;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });
function draw(s: Segment, isLive: boolean, style: "agents" | "assistant" = "agents") {
  const ctx: ChatContextValue = { tasks: emptyTaskIndex(), subagents: [], scope: "x:", lineStyle: style };
  act(() => root.render(<ChatContext.Provider value={ctx}><SegmentView key={segmentIdentity(s)} seg={s} live={isLive} mid="m" sid="s" expandFold={async () => {}} /></ChatContext.Provider>));
}
const groupsShown = () => [...host.querySelectorAll<HTMLElement>("[data-work-group]")];
const head = (g: HTMLElement) => g.querySelector<HTMLButtonElement>(":scope > button")!;
const title = (g: HTMLElement) => head(g).textContent ?? "";
const outer = () => groupsShown()[0];
const inner = () => groupsShown().slice(1);
const tap = (b: HTMLElement) => act(() => b.click());
// thought rows come in their own chunk (WorkGroup.tsx lazy): wait for it once something opened
const loaded = () => act(async () => { await vi.dynamicImportSettled(); });
const rows = () => [...host.querySelectorAll("[data-action-row]")].map((r) => r.textContent ?? "");
const thoughtRows = () => [...host.querySelectorAll<HTMLElement>('[data-thinking="words"] > div')];

describe.each(["agents", "assistant"] as const)("%s: the timeline", (style) => {
  it("a finished turn is one folded line; opened, its narration and inner summaries, each open with every row", async () => {
    draw(seg(finished), false, style);
    expect(groupsShown()).toHaveLength(1);
    expect(head(outer()).getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-latest-text]")?.textContent).toContain("Final result.");
    expect(host.textContent).not.toContain("Found the setting.");
    tap(head(outer()));
    await loaded();
    expect(inner()).toHaveLength(2);
    expect(inner().map(title)).toEqual(["已工作 5s", "已工作 7s"]);
    expect(inner().map((g) => head(g).getAttribute("aria-expanded"))).toEqual(["true", "true"]);
    expect(rows().join(" ")).toContain("echo 3");
    expect(rows().join(" ")).toContain("echo 9");
    // in order: the first run, the narration, the second run
    const detail = outer().querySelector("[data-work-detail]")!.textContent!;
    expect(detail.indexOf("echo 3")).toBeLessThan(detail.indexOf("Found the setting."));
    expect(detail.indexOf("Found the setting.")).toBeLessThan(detail.indexOf("echo 6"));
    // a thought without words is no row; one with words is
    expect(thoughtRows().map((r) => r.textContent)).toEqual(["Read the config first.", "Line one line two"]);
    // backtick code reads as code on the row too, without its backticks
    expect(thoughtRows()[1].querySelector('[data-thinking-markup="code"]')?.textContent).toBe("two");
    expect(host.textContent).not.toContain("思考");
  });

  it("each inner summary keeps its own open / closed choice, also when the outer one opens again and after a remount", () => {
    draw(seg(finished), false, style);
    tap(head(outer()));
    tap(head(inner()[0]));
    expect(head(inner()[0]).getAttribute("aria-expanded")).toBe("false");
    tap(head(outer()));
    tap(head(outer()));
    expect(inner().map((g) => head(g).getAttribute("aria-expanded"))).toEqual(["false", "true"]);
    act(() => root.unmount());
    root = createRoot(host);
    draw(seg(finished), false, style);
    expect(head(outer()).getAttribute("aria-expanded")).toBe("true");
    expect(inner().map((g) => head(g).getAttribute("aria-expanded"))).toEqual(["false", "true"]);
  });

  it("a thought with a line break opens in place to its whole text and stays open after a remount", async () => {
    draw(seg(finished), false, style);
    tap(head(outer()));
    await loaded();
    const long = thoughtRows()[1];
    expect(long.getAttribute("aria-expanded")).toBe("false");
    tap(long);
    expect(thoughtRows()[1].getAttribute("aria-expanded")).toBe("true");
    expect(thoughtRows()[1].textContent).toBe("Line one\nline two");
    expect(thoughtRows()[1].querySelector('[data-thinking-markup="code"]')?.textContent).toBe("two");
    expect(thoughtRows()[1].className).toContain("items-start");
    act(() => root.unmount());
    root = createRoot(host);
    draw(seg(finished), false, style);
    await loaded();
    expect(thoughtRows()[1].getAttribute("aria-expanded")).toBe("true");
  });

  it("live: the last run is Working… with its own clock, earlier runs are done; nothing opens by itself", async () => {
    draw(seg(running), true, style);
    expect(groupsShown()).toHaveLength(1);
    expect(title(outer())).toContain("正在工作…");
    expect(head(outer()).getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-latest-text]")?.textContent).toContain("Checking once more.");
    tap(head(outer()));
    await loaded();
    const [done, going] = inner();
    expect(title(done)).toBe("已工作 5s");
    expect(going.dataset.workGroup).toBe("live");
    expect(title(going)).toMatch(/^正在工作…\d/);
    expect(rows().join(" ")).toContain("echo 13");
  });

  it("the live Thinking line is drawn once, in the innermost live group", () => {
    live.thinking = true;
    draw(seg(running), true, style);
    tap(head(outer()));
    const lines = host.querySelectorAll('[data-thinking="live"]');
    expect(lines).toHaveLength(1);
    expect(inner()[1].contains(lines[0])).toBe(true);
    expect(lines[0].textContent).toContain("正在思考");
    // without a timeline the outer group is the live one
    draw(seg([tool(1), tool(2, false)]), true, style);
    expect(host.querySelectorAll('[data-thinking="live"]')).toHaveLength(1);
  });

  it("an opened thought's words can be selected and copied: a click that ends a selection does not fold it; keys toggle it", async () => {
    draw(seg(finished), false, style);
    tap(head(outer()));
    await loaded();
    const row = () => thoughtRows()[1];
    expect(row().getAttribute("role")).toBe("button");
    expect(row().tabIndex).toBe(0);
    act(() => row().dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(row().getAttribute("aria-expanded")).toBe("true");
    const words = row().querySelector<HTMLElement>("span.italic")!;
    expect(words.className).toContain("select-text");
    // the reader selected some of its words: the click that ends it leaves the row open
    const range = document.createRange();
    range.selectNodeContents(words);
    getSelection()!.removeAllRanges();
    getSelection()!.addRange(range);
    act(() => words.click());
    expect(row().getAttribute("aria-expanded")).toBe("true");
    getSelection()!.removeAllRanges();
    act(() => words.click());
    expect(row().getAttribute("aria-expanded")).toBe("false");
    act(() => row().dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true })));
    expect(row().getAttribute("aria-expanded")).toBe("true");
  });

  it("thoughts without words are no steps: a turn of nothing else has no summary, and they split no rows", async () => {
    draw(seg([thought(1, ""), words(4, "Just an answer.")]), false, style);
    expect(groupsShown()).toHaveLength(0);
    expect(host.querySelector("[data-latest-text]")?.textContent).toContain("Just an answer.");
    // live, the turn still says it is at work
    draw(seg([thought(1, "")]), true, style);
    expect(groupsShown()).toHaveLength(1);
    expect(head(outer()).disabled).toBe(true);
    // between two narrations they make no empty inner summary; between two calls they leave one block of rows
    draw(seg([tool(1), words(2, "First."), thought(3, ""), words(4, "Second."), tool(5), thought(6, " "), tool(7), words(9, "Done.")]), false, style);
    tap(head(outer()));
    await loaded();
    expect(inner().map(title)).toEqual(["已工作 2s", "已工作 5s"]);
    const blocks = [...outer().querySelectorAll("[data-action-row]")].map((r) => r.parentElement);
    expect(blocks[1]).toBe(blocks[2]);
  });

  it("while the exchange runs, a call without its result spins in a finished inner group too; in history it does not", async () => {
    const steps = [tool(1, false), words(2, "Started the build."), tool(3), words(4, "Checking the logs.")];
    const status = () => [...host.querySelectorAll("[data-action-row] [role=img]")].map((e) => e.getAttribute("aria-label"));
    draw(seg(steps), true, style);
    tap(head(outer()));
    expect(inner().map((g) => g.dataset.workGroup)).toEqual(["done", "done"]);
    expect(status()).toEqual(["进行中", "完成"]);
    draw(seg([...steps, words(9, "Done.")]), false, style);
    if (head(outer()).getAttribute("aria-expanded") === "false") tap(head(outer()));
    expect(status()[0]).toBe("完成");
  });

  it("a search hit in a thought opens the summary, its inner group and the row; Back closes them again", async () => {
    draw(seg(finished), false, style);
    act(() => revealItem("x:", "th8#0"));
    // the row's chunk comes in after the summary opened: the hit is still asked for, so it opens on arrival
    await loaded();
    expect(head(outer()).getAttribute("aria-expanded")).toBe("true");
    expect(head(inner()[1]).getAttribute("aria-expanded")).toBe("true");
    expect(thoughtRows()[1].getAttribute("aria-expanded")).toBe("true");
    act(() => undoReveals());
    expect(head(outer()).getAttribute("aria-expanded")).toBe("false");
  });
});
