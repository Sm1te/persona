// @vitest-environment jsdom
// Synthetic associated reports exercise the actual segment, work and report disclosures.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
vi.mock("../../lib/api", async (original) => ({ ...await original<typeof import("../../lib/api")>(), fullItem: vi.fn() }));
import { fullItem } from "../../lib/api";
import { agentStatus, agentsWorking, buildTaskIndex, terminalCalls } from "../../lib/tasks";
import { chatLines } from "../../lib/turns";
import type { Item, Subagent } from "../../lib/types";
import { ChatContext } from "./chat-context";
import { SegmentView } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const at = (seq: number) => new Date(Date.parse("2026-11-01T10:00:00Z") + seq * 1000).toISOString();
const item = (seq: number, kind: Item["kind"], extra: Partial<Item> = {}): Item => ({ id: `example-${seq}#0`, seq, ts: at(seq), role: kind === "tool_use" ? "assistant" : "user", kind, ...extra });
const launch = item(2, "tool_use", { tool_name: "Agent", tool_use_id: "launch", tool_input: { description: "Check the example tests", prompt: "Inspect the fictional checks", subagent_type: "general-purpose" } });
const receipt = item(3, "tool_result", { tool_use_id: "launch", text: "Async agent launched successfully.\nagentId: example-agent\nThe agent is working in the background." });
const note = (status: string) => ({ kind: "agent", status, task_id: "example-agent", tool_use_id: "launch", title: "Check the example tests" });
const report = (seq: number, status: string, text: string) => item(seq, "tool_result", { tool_use_id: "launch", text, task: { ...note(status), detail: text.slice(0, 4096) } });
const progress = report(4, "running", "## First findings\nThe fictional checks are still running.");
const done = report(6, "completed", "## Final findings\nEvery fictional check passed.");
const initial = [item(1, "text", { text: "Check the example project" }), launch, receipt];
const meta: Subagent = { id: "example-agent", session_id: "s@example-agent", tool_use_id: "launch", running: true, last_tool: { id: "check", name: "Bash", summary: "Checking fictional tests" } };
let host: HTMLDivElement;
let root: Root;
let scope: string;
let counter = 0;
beforeEach(() => {
  localStorage.clear();
  scope = `reports-${++counter}:`;
  host = document.createElement("div"); document.body.append(host); root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); vi.clearAllMocks(); });
async function draw(items: Item[], live = true) {
  const tasks = buildTaskIndex(items);
  await act(async () => root.render(<StrictMode><ChatContext.Provider value={{ tasks, subagents: [meta], scope }}>
    {chatLines(items, { live }).map((line) => line.kind === "segment" ? <SegmentView key={line.id} seg={line.seg} live={line.live} mid="m" sid="s" expandFold={async () => {}} /> : null)}
  </ChatContext.Provider></StrictMode>));
}
async function click(selector: string) {
  const button = host.querySelector<HTMLButtonElement>(selector);
  expect(button, selector).not.toBeNull();
  await act(async () => { button!.click(); await vi.dynamicImportSettled(); });
}

describe("associated subagent reports", () => {
  it("keeps launch and interim reports running under the collapsed original tool, then shows only its terminal result (#5030)", async () => {
    const started = { ...receipt, task: note("running") };
    expect(agentStatus(started, { ...meta, running: false }, false)).toBe("running");
    await draw([...initial.slice(0, 2), started, progress]);
    // super rule 2: every tool call, a running subagent's too, stays in the folded summary
    expect(host.querySelector("[data-work-group] > button")?.getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("[data-subagent-card]")).toBeNull();
    await click("[data-work-group] > button");
    expect(host.querySelector("[data-subagent-card]")?.getAttribute("data-subagent-card")).toBe("running");
    expect(host.querySelector("[data-subagent-card] button")?.getAttribute("aria-expanded")).toBe("false");
    expect(host.textContent).not.toContain("The fictional checks are still running.");
    await click("[data-subagent-card] button");
    // the interim report is what it has said so far
    expect(host.textContent).toContain("The fictional checks are still running.");
    const running = [...initial, progress];
    expect(agentsWorking([meta], terminalCalls(running))).toEqual({ count: 1, doing: ["Checking fictional tests"] });
    const terminal = [...running, done, item(7, "tool_result", { tool_use_id: "launch", task: note("completed") })];
    await draw(terminal);
    expect(host.querySelector("[data-subagent-card]")?.getAttribute("data-subagent-card")).toBe("completed");
    // opened before, opened still (remembered per card), with only its latest report
    expect(host.querySelector("[data-subagent-card] button")?.getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelectorAll("[data-subagent-summary]")).toHaveLength(1);
    expect(host.querySelector('[data-items="example-6#0"]')).not.toBeNull();
    expect(host.textContent).toContain("Every fictional check passed.");
    expect(host.textContent).not.toContain("The fictional checks are still running.");
    expect(agentsWorking([meta], terminalCalls(terminal))).toBeNull();
    expect(host.querySelectorAll("[data-subagent-card]")).toHaveLength(1);
    expect(host.textContent).not.toContain("Async agent launched");
  });

  it("keeps legacy notifications associated by receipt id, but never guesses that an unrelated peer is a subagent", async () => {
    const legacy = item(4, "notice", { role: "system", wake: true, text: "Agent finished", task: { kind: "agent", task_id: "example-agent", status: "completed", detail: "## Legacy findings\nThe original report remains readable." } });
    const items = [...initial, legacy, item(5, "text", { from: "general-purpose", text: "An unrelated peer message" })];
    expect(buildTaskIndex(items).calls.get(legacy.id)).toBe("launch");
    const lines = chatLines(items);
    expect(lines.some((line) => line.kind === "notice" && line.item.id === legacy.id)).toBe(false);
    expect(lines.filter((line) => line.kind === "segment" && line.seg.user?.from === "general-purpose")).toHaveLength(1);
    await draw(items, false);
    await click("[data-work-group] > button");
    expect(host.querySelector("[data-subagent-card]")?.getAttribute("data-subagent-card")).toBe("completed");
    await click("[data-subagent-card] button");
    expect(host.textContent).toContain("The original report remains readable.");
    expect(host.textContent).toContain("An unrelated peer message");
    expect(host.textContent).not.toContain("Agent finished");
  });

  it("attaches a late terminal result to the original call across another user input", () => {
    const items = [...initial, item(5, "text", { text: "Continue with the other checks" }), done];
    const original = chatLines(items).find((line) => line.kind === "segment" && line.seg.steps.some((step) => step.kind === "tool"));
    expect(original?.kind).toBe("segment");
    if (original?.kind !== "segment") throw new Error("Missing original exchange");
    const tool = original.seg.steps.find((step) => step.kind === "tool");
    expect(tool?.kind === "tool" ? tool.result?.id : undefined).toBe(done.id);
    expect(buildTaskIndex(items).reports.get("launch")?.map((i) => i.id)).toEqual([done.id]);
  });

  it.each(["tool_result", "notice"] as const)("keeps %s reports under their original call after completed turns and a wake", async (kind) => {
    // Older display types ignore this wire marker, but real transcripts still include it between completed turns.
    const duration = (seq: number): Item => ({ ...item(seq, "notice", { role: "system" }), kind: "turn_duration", turn_duration: { duration_ms: 1000, source: "claude_turn_duration" } } as unknown as Item);
    const first = [...initial, progress, item(5, "text", { role: "assistant", text: "The agent is checking the project in the background." }), duration(6)];
    const second = [...first, item(7, "text", { text: "What does the example project do?" }), item(8, "text", { role: "assistant", text: "It demonstrates fictional checks." }), duration(9)];
    const completion: Item = kind === "tool_result" ? { ...done, id: "completion#0", seq: 10, ts: at(10), wake: true } : item(10, "notice", { role: "system", wake: true, text: "Agent completed", task: { kind: "agent", status: "completed", task_id: "example-agent", detail: done.text } });
    const final = [...second, completion, item(11, "text", { role: "assistant", text: "The background checks are now complete." }), duration(12)];
    await draw(first, false);
    await draw(second, false);
    await draw(final, false);
    expect(host.querySelectorAll("[data-subagent-card]")).toHaveLength(0); // both finished work groups stay folded
    await click("[data-work-group] > button");
    expect(host.querySelectorAll("[data-subagent-card]")).toHaveLength(1);
    expect(host.querySelector("[data-subagent-card]")?.getAttribute("data-subagent-card")).toBe("completed");
    await click("[data-subagent-card] button");
    expect(host.querySelectorAll("[data-subagent-summary]")).toHaveLength(1);
    expect(host.querySelector(`[data-items="${completion.id}"]`)?.closest("[data-subagent-card]")).not.toBeNull();
    expect(host.textContent).toContain("Every fictional check passed.");
    expect(host.textContent).toContain("The background checks are now complete.");
    expect(host.textContent).not.toContain("Agent completed");
  });

  it.each(["failed", "stopped", "waiting"])("uses the %s lifecycle without treating a wait as completion", (status) => {
    const current = report(4, status, "A fictional update");
    const tasks = buildTaskIndex([...initial, current]);
    expect(agentStatus(current, meta, true, tasks.notes.get("launch"))).toBe(status === "waiting" ? "running" : status);
    expect(terminalCalls([...initial, current]).has("launch")).toBe(status !== "waiting");
  });

  it.each([false, true])("preserves a foreground report without its agentId tail (metadata=%s)", async (metadata) => {
    const foreground = item(3, "tool_result", { tool_use_id: "launch", text: "## Foreground findings\nThe complete report is still readable.\nagentId: example-agent (This is an internal ID for your use)", ...(metadata ? { task: note("completed") } : {}) });
    await draw([...initial.slice(0, 2), foreground], false);
    await click("[data-work-group] > button");
    expect(host.querySelector("[data-subagent-card]")?.getAttribute("data-subagent-card")).toBe("completed");
    await click("[data-subagent-card] button");
    expect(host.textContent).toContain("The complete report is still readable.");
    expect(host.querySelector("[data-subagent-summary]")?.textContent).not.toContain("agentId");
  });

  it("drops Claude Code's hand-back frame from a report", async () => {
    const framed = item(3, "tool_result", { tool_use_id: "launch", text: "[Subagent hand-back] The text below is the final report of a subagent. The report follows:\n## Findings\nAll fictional checks passed." });
    await draw([...initial.slice(0, 2), framed], false);
    await click("[data-work-group] > button");
    await click("[data-subagent-card] button");
    const summary = host.querySelector("[data-subagent-summary]")?.textContent ?? "";
    expect(summary).toContain("All fictional checks passed.");
    expect(summary).not.toContain("hand-back");
  });

  it("folds a result to four lines, with Show full result only when that cut it (#5030)", async () => {
    // jsdom lays nothing out: the clamped paragraph says it is taller than its box
    const tall = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(120);
    const box = vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(80);
    const long = item(3, "tool_result", { tool_use_id: "launch", text: ["## Findings", "one", "two", "three", "four", "five"].join("\n") });
    await draw([...initial.slice(0, 2), long], false);
    await click("[data-work-group] > button");
    await click("[data-subagent-card] button");
    const text = () => host.querySelector("[data-subagent-summary] p");
    expect(text()?.className).toContain("line-clamp-4");
    expect(host.querySelector("[data-subagent-summary] button")?.textContent).toBe("展开完整结果");
    await click("[data-subagent-summary] button");
    expect(text()?.className).not.toContain("line-clamp-4");
    expect(host.querySelector("[data-subagent-summary] button")?.textContent).toBe("收起结果");
    tall.mockRestore();
    box.mockRestore();
  });

  it("offers no Show full result when four lines hold all of it", async () => {
    await draw([...initial.slice(0, 2), item(3, "tool_result", { tool_use_id: "launch", text: "Short and done." })], false);
    await click("[data-work-group] > button");
    await click("[data-subagent-card] button");
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toBe("Short and done.");
    expect(host.querySelector("[data-subagent-summary] button")).toBeNull();
  });

  it("fetches full text by the report item's parent-session identity, not the task's 4KB preview", async () => {
    const cut = { ...done, full_size: 16000, task: { ...done.task!, detail: "Compatibility preview only" } };
    vi.mocked(fullItem).mockResolvedValue({ ...cut, text: "## Full findings\nThe entire fictional report is visible." });
    await draw([...initial, cut], false);
    await click("[data-work-group] > button");
    await click("[data-subagent-card] button");
    expect(host.textContent).toContain("Final findings");
    expect(host.textContent).not.toContain("Compatibility preview only");
    expect(fullItem).not.toHaveBeenCalled();
    await click("[data-full-text]");
    expect(fullItem).toHaveBeenCalledWith("m", "s", cut.id);
    expect(host.textContent).toContain("The entire fictional report is visible.");
  });
});
