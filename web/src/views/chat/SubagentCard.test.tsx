// @vitest-environment jsdom
// The subagent card after cindy's AgentTaskCard (#5030): two lines, the status word, what it ran on and used, the
// details opened in place and remembered per card.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
});
import { emptyTaskIndex } from "../../lib/tasks";
import type { Item, Subagent, TaskNote } from "../../lib/types";
import type { AgentStatus, ToolStep } from "../../lib/work-groups";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { forgetExpanded } from "./expanded";
import { subagentCardKey } from "../../lib/tasks";
import { compactText, SubagentCard, subagentMeta } from "./SubagentCard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-04T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
const call = (input: Record<string, unknown>, result?: Partial<Item>): ToolStep => ({
  kind: "tool",
  id: "a1#0",
  use: { id: "a1#0", seq: 1, role: "assistant", kind: "tool_use", tool_name: "Agent", tool_use_id: "c-a1", tool_input: input, ts: at(0) },
  result: result ? { id: "a1-r#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "c-a1", ts: at(65), ...result } : undefined,
  images: [],
});
const meta: Subagent = { id: "sa1", session_id: "s@sa1", tool_use_id: "c-a1", running: true, started_at: at(0), model: "claude-haiku-4-5-20251001", tokens: 23_456, steps: 12, last_tool: { id: "t9", name: "Bash", summary: "Running the checkout tests" } };
const input = { description: "Checkout regression", prompt: "Run the checkout regression suite and report what fails.", subagent_type: "general-purpose", model: "opus" };

function ctx(over: Partial<ChatContextValue> = {}): ChatContextValue {
  return { tasks: emptyTaskIndex(), subagents: [meta], scope: "t:", ...over };
}
const html = (step: ToolStep, status: AgentStatus, over: Partial<ChatContextValue> = {}) =>
  renderToStaticMarkup(
    <ChatContext.Provider value={ctx(over)}>
      <SubagentCard step={step} status={status} mid="m" sid="s" scope="t:" />
    </ChatContext.Provider>,
  );

describe("SubagentCard: the two lines", () => {
  it("running: Subagent, its title, Running; Claude Code, the model it ran on, tokens, tool uses, Running for", () => {
    const h = html(call(input), "running");
    expect(h).toContain('data-subagent-card="running"');
    expect(h).toContain(">子 Agent<");
    expect(h).toContain(">Checkout regression<");
    expect(h).toContain('data-task-status="running"');
    expect(h).toContain("运行中");
    expect(h).toContain(">Claude Code<");
    expect(h).toMatch(/data-task-model[^>]*>Haiku 4\.5</);
    expect(h).toContain(">23.5k tokens<");
    expect(h).toContain(">12 次工具调用<");
    expect(h).toContain("已运行 ");
    expect(h).toContain('aria-expanded="false"');
    // no grid, no recent steps, no way to open it as a conversation of its own
    expect(h).not.toContain("查看完整过程");
    expect(h).not.toContain("data-subagent-recent");
  });

  it("finished: Completed and how long it took, no live clock", () => {
    const h = html(call(input, { text: "All 120 cases passed." }), "completed", { subagents: [{ ...meta, running: false }] });
    expect(h).toContain("已完成");
    expect(h).toContain(">1m 5s<");
    expect(h).not.toContain("已运行");
  });

  it("only a failure is red", () => {
    expect(html(call(input, { text: "boom", is_error: true }), "failed")).toMatch(/text-red[^>]*data-task-status="failed"/);
    expect(html(call(input), "stopped")).toContain("已停止");
    expect(html(call(input), "stopped")).not.toMatch(/text-red[^>]*data-task-status/);
  });

  it("never shows the model its call asked for: no chip without the node's word", () => {
    const h = html(call(input), "running", { subagents: [{ ...meta, model: undefined, tokens: undefined, steps: undefined }] });
    expect(h).not.toContain("data-task-model");
    expect(h).not.toContain("Opus");
    expect(h).not.toContain("tokens");
  });

  it("the Persona chat's own calls leave out the harness", () => {
    expect(html(call(input), "running", { lineStyle: "assistant" })).not.toContain("Claude Code");
  });

  it("titles from the description, else the subagent's, else the prompt, else Subagent task; at most 96 characters", () => {
    expect(html(call({ prompt: "x".repeat(200) }), "running", { subagents: [] })).toContain(`>${"x".repeat(95)}…<`);
    expect(html(call({}), "running", { subagents: [] })).toContain(">子 Agent 任务<");
    expect(compactText("  a\n\n b  ", 10)).toBe("a b");
    // never half of a character
    expect(compactText("😀😀😀", 2)).toBe("😀…");
  });

  it("subagentMeta: the pieces in cindy's order, each only when known", () => {
    expect(subagentMeta({ ...meta, tokens: 999 }, "6 秒", true)).toEqual({ harness: "Claude Code", model: "Haiku 4.5", tokens: "999 tokens", uses: "12 次工具调用", took: "6 秒" });
    expect(subagentMeta(undefined, "", false)).toEqual({ model: "" });
  });

});

describe("SubagentCard: opened in place", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    forgetExpanded();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });
  const draw = (step: ToolStep, status: AgentStatus, over: Partial<ChatContextValue> = {}) =>
    act(() =>
      root.render(
        <ChatContext.Provider value={ctx(over)}>
          <SubagentCard step={step} status={status} mid="m" sid="s" scope="t:" />
        </ChatContext.Provider>,
      ),
    );
  const head = () => host.querySelector<HTMLButtonElement>("[data-subagent-card] button")!;

  it("shows what it was asked, its last tool and its output file; no result while it runs", () => {
    const notes = new Map<string, TaskNote[]>([["c-a1", [{ kind: "agent", status: "running", output_file: "/tmp/example/agent.output" }]]]);
    draw(call(input), "running", { tasks: { ...emptyTaskIndex(), notes }, subagents: [{ ...meta, last_text: "Halfway there" }] });
    act(() => head().click());
    expect(head().getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelector("[data-subagent-prompt]")?.textContent).toBe(input.prompt);
    expect(host.querySelector("[data-subagent-last-tool]")?.textContent).toBe("最近工具：Bash");
    expect(host.querySelector("[data-subagent-output]")?.textContent).toBe("输出：/tmp/example/agent.output");
    expect(host.querySelector("[data-subagent-summary]")).toBeNull();
  });

  it("remembers it was opened, per card, under its call (a link to the subagent opens the same key)", () => {
    draw(call(input), "running");
    act(() => head().click());
    act(() => root.unmount());
    root = createRoot(host);
    draw(call(input, { text: "Done." }), "completed");
    expect(head().getAttribute("aria-expanded")).toBe("true");
    expect(subagentCardKey("t:", "c-a1")).toBe("t:task:c-a1");
  });
});
