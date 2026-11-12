// @vitest-environment jsdom
// A child session's card in its parent's chat (#5120, cindy's AgentTaskCard): two lines from the child's own row, its
// status, what ran it with the model and effort, how long it took; opened in place to what it was asked, its latest words
// and its last tool, read from its transcript only once opened.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
});
import { setToken } from "../../lib/api";
import { childCardKey } from "../../lib/child-cards";
import { emptyTaskIndex } from "../../lib/tasks";
import { reset, upsertSession } from "../../lib/store";
import type { Item, Session } from "../../lib/types";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { ChildSessionCard, childMeta, childModelChip, childStatus, childToolName, knownEffort } from "./ChildSessionCard";
import { forgetExpanded } from "./expanded";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
const kid = (extra: Partial<Session> = {}): Session => ({
  machine_id: "m", session_id: "kid", title: "Review the session handling", cwd: "/w", status: "working", state: "working",
  agent: "codex", model: "gpt-5.5", effort: "high", started_at: at(0), updated_at: at(65), folded_under: "p", last_text: "Reading how the token is refreshed.", ...extra,
});
const ctx = (over: Partial<ChatContextValue> = {}): ChatContextValue => ({ tasks: emptyTaskIndex(), subagents: [], scope: "t:", ...over });
const html = (over: Partial<ChatContextValue> = {}) =>
  renderToStaticMarkup(
    <ChatContext.Provider value={ctx(over)}>
      <ChildSessionCard mid="m" sid="kid" scope="t:" />
    </ChatContext.Provider>,
  );

afterEach(() => reset());

const child = (sid: string, extra: Partial<Session> = {}): Session => ({ machine_id: "m", session_id: sid, title: sid, cwd: "/w", status: "idle", state: "idle", folded_under: "p", ...extra });

describe("childStatus", () => {
  it("runs while the child works, waits on the user or has background work", () => {
    expect(childStatus(child("a", { state: "working" }))).toBe("running");
    expect(childStatus(child("a", { state: "needs_you" }))).toBe("running");
    expect(childStatus(child("a", { state: "idle", background_working: true }))).toBe("running");
    // a running child with an old error is still running
    expect(childStatus(child("a", { state: "working", turn_error: "timeout" }))).toBe("running");
  });
  it("failed with a turn error, stopped when interrupted, completed otherwise", () => {
    expect(childStatus(child("a", { turn_error: "The model service timed out." }))).toBe("failed");
    expect(childStatus(child("a", { status: "interrupted" }))).toBe("stopped");
    expect(childStatus(child("a", { state: "unread" }))).toBe("completed");
    expect(childStatus(child("a", { state: "offline", status: "offline" }))).toBe("completed");
    // its own state, never a rollup: a child is not shown in a folded state
    expect(childStatus(child("a", { state: "idle", children: { total: 1, working: 1 } }))).toBe("completed");
  });
});

describe("knownEffort", () => {
  it("names only the levels a chip says", () => {
    expect(knownEffort("high")).toBe("high");
    expect(knownEffort("xhigh")).toBe("xhigh");
    expect(knownEffort("turbo")).toBeUndefined();
    expect(knownEffort(undefined)).toBeUndefined();
  });
});


describe("ChildSessionCard: the two lines", () => {
  it("running: Subagent, its title, Running; what ran it, the model with its effort, Running for", () => {
    upsertSession(kid());
    const h = html();
    expect(h).toContain('data-child-card="running"');
    expect(h).toContain('data-tool-use="child:kid"');
    expect(h).toContain(">子 Agent<");
    expect(h).toContain(">Review the session handling<");
    expect(h).toContain('data-task-status="running"');
    expect(h).toContain(">Codex<");
    expect(h).toMatch(/data-task-model[^>]*>GPT-5\.5 · 高</);
    expect(h).toContain("已运行 ");
    expect(h).toContain('aria-expanded="false"');
    // no tokens (a row has no usage total), no way to open it as a conversation of its own
    expect(h).not.toContain("tokens");
    expect(h).not.toContain("href");
  });

  it("finished: Completed and how long it took, from its start to its last update", () => {
    upsertSession(kid({ state: "unread", status: "idle" }));
    const h = html();
    expect(h).toContain("已完成");
    expect(h).toContain(">1m 5s<");
    expect(h).not.toContain("已运行");
  });

  it("failed is red; stopped is not", () => {
    upsertSession(kid({ state: "idle", status: "idle", turn_error: "The model service timed out." }));
    expect(html()).toMatch(/text-red[^>]*data-task-status="failed"/);
    reset();
    upsertSession(kid({ state: "idle", status: "interrupted" }));
    expect(html()).toContain("已停止");
    expect(html()).not.toMatch(/text-red[^>]*data-task-status/);
  });

  it("in a Persona conversation, no harness; an untitled child is a Subagent task, a long title cut at 96", () => {
    upsertSession(kid({ title: "" }));
    const h = html({ lineStyle: "assistant" });
    expect(h).not.toContain(">Codex<");
    expect(h).toContain(">子 Agent 任务<");
    upsertSession(kid({ title: "x".repeat(200) }));
    expect(html()).toContain(`>${"x".repeat(95)}…<`);
  });

  it("draws nothing for a child whose row is gone", () => {
    expect(html()).toBe("");
  });

  it("childMeta and the chip: each piece only when known", () => {
    expect(childModelChip({ model: "claude-sonnet-4-5", effort: "medium" })).toBe("Sonnet 4.5 · 中");
    expect(childModelChip({ model: "gpt-5.4-mini" })).toBe("GPT-5.4 Mini");
    expect(childModelChip({ effort: "low" })).toBe("低");
    expect(childModelChip({ model: "", effort: "turbo" })).toBe("");
    expect(childMeta(kid({ model: undefined, effort: undefined, started_at: undefined }), false)).toEqual({});
    expect(childMeta(kid({ state: "idle", status: "idle" }), true)).toEqual({ harness: "Codex", model: "GPT-5.5 · 高", took: "1m 5s" });
    expect(childMeta(kid(), true)).toEqual({ harness: "Codex", model: "GPT-5.5 · 高", since: T0 });
  });

  it("names a tool the way the tool rows do", () => {
    expect(childToolName("Bash")).not.toBe("");
    expect(childToolName("mcp__github__create_issue")).toContain(" · ");
  });
});

describe("ChildSessionCard: opened in place", () => {
  let host: HTMLDivElement;
  let root: Root;
  let asked: string[];
  let page: { items: Item[]; has_more?: boolean };
  const flush = async () => {
    for (let i = 0; i < 6; i++) await act(async () => { await new Promise((r) => setTimeout(r, 0)); });
  };
  beforeEach(() => {
    forgetExpanded();
    setToken("tok");
    asked = [];
    page = {
      items: [
        { id: "u#0", seq: 1, role: "user", kind: "text", text: "Review the session handling after the refresh moved into the middleware." },
        { id: "t#0", seq: 2, role: "assistant", kind: "tool_use", tool_name: "Bash", tool_use_id: "c1", tool_input: { command: "rg -n refresh" } },
        { id: "r#0", seq: 3, role: "user", kind: "tool_result", tool_use_id: "c1", text: "src/auth/middleware.ts:12" },
        { id: "a#0", seq: 4, role: "assistant", kind: "text", text: "No token leaks: the refresh keeps it in memory." },
      ],
    };
    vi.stubGlobal("fetch", vi.fn(async (url: string) => {
      asked.push(new URL(url, "http://x").pathname);
      return new Response(JSON.stringify(page), { headers: { "Content-Type": "application/json" } });
    }));
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });
  const draw = () =>
    act(() =>
      root.render(
        <ChatContext.Provider value={ctx()}>
          <ChildSessionCard mid="m" sid="kid" scope="t:" />
        </ChatContext.Provider>,
      ),
    );
  const head = () => host.querySelector<HTMLButtonElement>("[data-child-card] button")!;

  it("reads nothing until opened; then what it was asked, its latest words and its last tool", async () => {
    upsertSession(kid({ state: "unread", status: "idle" }));
    draw();
    await flush();
    expect(asked).toEqual([]);
    act(() => head().click());
    expect(head().getAttribute("aria-expanded")).toBe("true");
    // while the transcript is read, the row's last words
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("Reading how the token is refreshed.");
    await flush();
    expect(asked).toEqual(["/api/v1/sessions/m/kid/items"]);
    expect(host.querySelector("[data-subagent-prompt]")?.textContent).toBe("Review the session handling after the refresh moved into the middleware.");
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("No token leaks");
    expect(host.querySelector("[data-subagent-last-tool]")?.textContent).toMatch(/^最近工具：/);
  });

  it("a transcript without words of its own keeps the row's last words", async () => {
    page = { items: [page.items[0]] };
    upsertSession(kid({ state: "idle", status: "idle", turn_error: "The model service timed out.", last_text: "The model service timed out." }));
    draw();
    act(() => head().click());
    await flush();
    expect(host.querySelector("[data-subagent-summary]")?.textContent).toContain("The model service timed out.");
    expect(host.querySelector("[data-subagent-last-tool]")).toBeNull();
  });

  it("remembers it was opened under the child's key (a link to the child opens the same one)", () => {
    upsertSession(kid());
    draw();
    act(() => head().click());
    act(() => root.unmount());
    root = createRoot(host);
    draw();
    expect(head().getAttribute("aria-expanded")).toBe("true");
    expect(childCardKey("t:", "kid")).toBe("t:child:kid");
  });
});
