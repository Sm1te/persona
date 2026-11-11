import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { tokenCount, tokenRate, RunningStatusView, statusText, WaitingView } from "./RunningStatusBar";

afterEach(() => vi.useRealTimers());

const render = (props: Parameters<typeof RunningStatusView>[0]) => renderToStaticMarkup(<RunningStatusView {...props} />);

describe("tokenCount", () => {
  it("shows thousands with one decimal", () => {
    expect(tokenCount(950)).toBe("950");
    expect(tokenCount(1200)).toBe("1.2k");
    expect(tokenCount(22_800)).toBe("22.8k");
    expect(tokenCount(412.6)).toBe("413");
  });
});

describe("tokenRate (#2518)", () => {
  it("divides the turn's tokens by its seconds, once it has run long enough", () => {
    expect(tokenRate(22_800, 975)).toBe(23);
    expect(tokenRate(0, 3)).toBe(0);
    expect(tokenRate(120, 2)).toBeUndefined();
    expect(tokenRate(undefined, 10)).toBeUndefined();
  });
});

describe("statusText", () => {
  it("says how many subagents the agent waits on and what the newest does (#5602)", () => {
    expect(statusText({ verb: "Frosting" }, { count: 2, doing: ["跑测试", "在读 a.go"] })).toBe("2 个子 Agent 在工作 — 跑测试… · 还有 1 个");
    expect(statusText(null, { count: 1, doing: ["跑测试"] })).toBe("1 个子 Agent 在工作 — 跑测试…");
    expect(statusText(null, { count: 3, doing: [] })).toBe("3 个子 Agent 在工作…");
    expect(statusText({ verb: "Frosting" }, null)).toBe("Frosting…");
  });

  it("says the verb, 思考中 while thinking, 运行中 without a line", () => {
    expect(statusText({ verb: "Frosting" })).toBe("Frosting…");
    expect(statusText({ verb: "Frosting", thinking: true })).toBe("思考中");
    expect(statusText(null)).toBe("运行中");
    expect(statusText({})).toBe("运行中");
  });
});

describe("RunningStatusView", () => {
  it("shows the verb, the line's time and its tokens", () => {
    const html = render({ live: { verb: "Frosting", elapsed_s: 975, tokens_down: 22_800 }, working: true });
    expect(html).toContain("Frosting…");
    expect(html).toContain("16 分 15 秒");
    expect(html).toContain("↓ 22.8k tokens");
    expect(html).toContain("23 tok/s");
    expect(html).toContain("text-status-accent");
    expect(html).toContain('data-running-status="on"');
  });

  it("background subagents after the turn: cindy's line, no time, no tokens, no Stop all the node cannot do (#5030)", () => {
    const html = render({ live: { verb: "Frosting", elapsed_s: 975, tokens_down: 22_800 }, working: true, subtasks: true });
    expect(html).toContain("后台子任务运行中，模型用量仍在消耗…");
    expect(html).toContain('data-running-background="subtasks"');
    expect(html).not.toContain("Frosting");
    expect(html).not.toContain("data-running-elapsed");
    expect(html).not.toContain("tokens");
    expect(html).not.toContain("全部停止");
  });

  it("a background workflow says so first, before background subagents", () => {
    const html = render({ live: null, working: true, subtasks: true, background: { run: { task_id: "w", status: "running", running: true, agents_started: 3, agents_done: 1 }, more: 0, guessed: false } });
    expect(html).toContain('data-running-background="workflow"');
    expect(html).not.toContain("后台子任务运行中");
  });

  it("says 思考中 while thinking", () => {
    expect(render({ live: { verb: "Frosting", thinking: true, elapsed_s: 3 }, working: true })).toContain("思考中");
  });

  it("without a line: 运行中, counting from when the turn started, no tokens", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-11-05T10:01:05Z"));
    const html = render({ live: null, working: true, startedAt: "2026-11-05T10:00:00Z" });
    expect(html).toContain("运行中");
    expect(html).toContain("1 分 5 秒");
    expect(html).not.toContain("tokens");
  });

  it("leaves the time to the chat's live work line when it has one (#3534)", () => {
    const html = render({ live: { verb: "Frosting", elapsed_s: 975, tokens_down: 22_800 }, working: true, clock: false });
    expect(html).toContain("Frosting…");
    expect(html).not.toContain("data-running-elapsed");
    expect(html).not.toContain("16 分 15 秒");
    expect(html).toContain("↓ 22.8k tokens");
    expect(html).toContain("23 tok/s");
  });

  it("says the hub is out of reach, in front of the last state it knew", () => {
    const html = render({ live: { verb: "Frosting", elapsed_s: 975 }, working: true, offline: true });
    expect(html).toContain("data-running-offline");
    expect(html).toContain("连接已断开");
    expect(html).toContain("Frosting…");
    expect(html).toContain("16 分 15 秒");
    expect(render({ live: { verb: "Frosting" }, working: true })).not.toContain("data-running-offline");
  });

  it("is not there while nothing works", () => {
    expect(render({ live: null, working: false })).toBe("");
  });
});

describe("the bar of an agent waiting in the background (#6045)", () => {
  it("is still: what it waits on, the time since it began, no breath and no tokens", () => {
    const html = renderToStaticMarkup(<WaitingView wait={{ what: "等 PR #7 的检查跑完", more: 2, since: new Date(Date.now() - 90_000).toISOString() }} />);
    expect(html).toContain('data-running-background="waiting"');
    // the header's title line says the state: the line says what it waits on (#3956)
    expect(html).toContain("等 PR #7 的检查跑完 · 还有 2 个");
    expect(html).not.toContain("后台等待中");
    // a wait without words of its own still says what it is
    expect(renderToStaticMarkup(<WaitingView wait={{ what: "", more: 0 }} />)).toContain("后台等待中");
    expect(html).toMatch(/data-running-elapsed[^>]*>1 分/);
    expect(html).not.toContain("tokens");
    expect(html).not.toContain("status-breath");
  });
});
