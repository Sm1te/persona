import { describe, expect, it } from "vitest";
import { parseAPIError } from "./api-errors";
import { agentStatus, agentsWorking, agentSummary, attachedAgentNotices, buildTaskIndex, loopCadence, loopLabel, loopNext, loopShortLabel, loopTask, stableSubagents, subagentEnded, taskBadge, taskPill, terminalCalls, wakeSummary } from "./tasks";
import type { Item, Subagent } from "./types";
import type { Step } from "./turns";
import * as tasksModule from "./tasks";

describe("buildTaskIndex", () => {
  it("finds the call a notice came from by its task id", () => {
    const items: Item[] = [
      { id: "t1#0", seq: 1, ts: "2026-11-02T10:00:00Z", role: "assistant", kind: "tool_use", tool_use_id: "call1", tool_name: "Bash", tool_input: { command: "make", description: "构建", run_in_background: true } },
      { id: "r1#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "call1", text: "Command running in background with ID: bk1. Output is being written to: /tmp/bk1.output" },
      { id: "n1#0", seq: 3, ts: "2026-11-02T10:05:00Z", role: "system", kind: "notice", text: "后台命令完成 · make", task: { kind: "shell", status: "completed", title: "make", task_id: "bk1" } },
    ];
    const x = buildTaskIndex(items);
    expect(x.started.has("call1")).toBe(true);
    expect(x.calls.get("n1#0")).toBe("call1");
    expect(x.descriptions.get("call1")).toBe("构建");
    expect(taskBadge(x.notes.get("call1")!).text).toBe("后台 · 已完成");
    expect(taskPill(items[2].task!, x.startedAt.get("call1"), items[2].ts)).toEqual({ text: "已完成 · 5 分", tone: "green" });
  });

  it("reads the agent's id from a 2.1.319 receipt, not the word before it", () => {
    const receipt =
      "Async agent launched successfully. (This tool result is internal metadata — never quote or paste any part of it, including the agentId below, into a user-facing reply.)\nagentId: a1b2c3 (internal ID - do not mention to user.)\nThe agent is working in the background.";
    const items: Item[] = [
      { id: "t1#0", seq: 1, role: "assistant", kind: "tool_use", tool_use_id: "call1", tool_name: "Agent", tool_input: { description: "Check the tests", prompt: "p" } },
      { id: "r1#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "call1", text: receipt },
      { id: "n1#0", seq: 3, role: "system", kind: "notice", text: "子 Agent 完成 · Check the tests", task: { kind: "agent", status: "completed", title: "Check the tests", task_id: "a1b2c3", detail: "## Summary" } },
    ];
    const x = buildTaskIndex(items);
    expect(x.calls.get("n1#0")).toBe("call1");
    expect(x.notes.get("call1")?.map((n) => n.detail)).toEqual(["## Summary"]);
  });

  it("leaves unassociated notices visible, including an Agent call with no association id", () => {
    const use: Item = { id: "call#0", seq: 1, role: "assistant", kind: "tool_use", tool_name: "Agent" };
    const notice: Item = { id: "notice#0", seq: 2, role: "system", kind: "notice", task: { kind: "agent", status: "completed", task_id: "unrelated", detail: "An unrelated report" } };
    expect(attachedAgentNotices([use, notice])).toEqual(new Set());
    expect(buildTaskIndex([use, notice]).reports.size).toBe(0);
    expect(attachedAgentNotices([{ ...notice, task: { ...notice.task!, tool_use_id: "unloaded-call" } }])).toEqual(new Set());
  });

  it("badges a call still running, with its events", () => {
    expect(taskBadge([]).text).toBe("后台 · 运行中");
    expect(taskBadge([{ kind: "monitor", status: "event" }, { kind: "monitor", status: "event" }]).text).toBe("后台 · 2 个事件");
    expect(taskBadge([{ kind: "shell", status: "failed", exit_code: 1 }])).toEqual({ text: "后台 · 失败", failed: true });
  });
});

describe("wakeSummary", () => {
  it("reads an older hub's summary", () => {
    expect(wakeSummary({ id: "n", seq: 1, role: "system", kind: "notice", text: 'Background command "Release" completed (exit code 0)' })).toEqual({ text: "后台命令完成 · Release", failed: false });
    expect(wakeSummary({ id: "n", seq: 1, role: "system", kind: "notice", text: 'Background command "lint" failed with exit code 2' }).failed).toBe(true);
  });
  it("takes the hub's line when it read the notification", () => {
    expect(wakeSummary({ id: "n", seq: 1, role: "system", kind: "notice", text: "后台命令失败 · lint", task: { kind: "shell", status: "failed", title: "lint" } })).toEqual({ text: "后台命令失败 · lint", failed: true });
  });
});

describe("loops", () => {
  const now = Date.parse("2026-11-02T10:00:00Z");
  it("reads the cadence and the next wakeup", () => {
    const loop = { kind: "wakeup", prompt: "/loop check CI", interval_seconds: 1800, next_at: "2026-11-02T10:04:00Z" };
    expect(loopTask(loop)).toBe("check CI");
    expect(loopCadence(loop)).toBe("每 30 分钟");
    expect(loopLabel(loop, now)).toMatch(/^下次 \d\d:\d\d$/);
    expect(loopShortLabel(loop, now)).toMatch(/^下次 \d\d:\d\d$/);
    expect(loopShortLabel({ ...loop, next_at: "2026-11-02T09:00:00Z" }, now)).toBe("执行中");
    expect(loopNext(loop, now)).toContain("还有 4 分钟");
    expect(loopLabel({ ...loop, next_at: "2026-11-02T09:00:00Z" }, now)).toBe("循环执行中");
    expect(loopCadence({ kind: "cron", cron: "33 17 * * *" })).toBe("按计划 33 17 * * *");
  });
});

describe("parseAPIError", () => {
  it("explains common errors", () => {
    expect(parseAPIError("API Error: 401 {\"type\":\"authentication_error\"} · Please run /login")).toMatchObject({ status: 401, transient: false, reason: "登录过期了，要在电脑上重新 /login" });
    expect(parseAPIError("API 错误：Overloaded（正在重试）")).toMatchObject({ transient: true, reason: "模型服务太忙，过载了" });
    const gpu = parseAPIError("API Error: 502 upstream error: Req 202610302115A1B2C3D4 Session 1ebd0000:7e470000 no available GPU pod");
    expect(gpu).toMatchObject({ status: 502, transient: true, requestId: "202610302115A1B2C3D4", session: "1ebd0000:7e470000" });
    expect(parseAPIError("all good")).toBeNull();
  });
});

describe("a subagent call's card", () => {
  const res = (text: string, is_error = false): Item => ({ id: "r#0", seq: 2, role: "user", kind: "tool_result", tool_use_id: "c", text, is_error });
  const meta = (running: boolean): Subagent => ({ id: "a", session_id: "s@a", running, last_text: "最近说的话" });

  it("goes by the result, then by the session's list", () => {
    expect(agentStatus(undefined, meta(true), false)).toBe("running");
    expect(agentStatus(res("done"), meta(false), true)).toBe("completed");
    expect(agentStatus(res("boom", true), meta(true), true)).toBe("failed");
    expect(agentStatus(undefined, undefined, true)).toBe("running");
    expect(agentStatus(undefined, undefined, false)).toBe("stopped");
    expect(agentStatus(res("Async agent launched successfully.\nagentId: x (This is an internal ID for your use)"), undefined, true)).toBe("running");
    expect(agentStatus(res("Async agent launched successfully."), undefined, false)).toBe("completed");
    expect(agentStatus(res("42 passed"), undefined, true)).toBe("completed");
  });

  it("uses the latest nonterminal evidence instead of a past completion for working bars", () => {
    const finished: Item = { ...res("Done"), task: { kind: "agent", status: "completed", tool_use_id: "c" } };
    const resumed: Item = { ...res("Working again"), id: "resumed#0", seq: 3, task: { kind: "agent", status: "running", tool_use_id: "c" } };
    expect(terminalCalls([finished, resumed]).has("c")).toBe(false);
  });

  it("does not infer completion from an unknown subagent status", () => {
    const result: Item = { ...res("Interim detail"), task: { kind: "agent", status: "future-status", tool_use_id: "c" } };
    expect(agentStatus(result, meta(false), false)).toBe("running");
    expect(terminalCalls([result]).has("c")).toBe(false);
    expect(taskBadge([result.task!]).text).toBe("后台 · 运行中");
  });

  it("does not flip a finished call back to running when the list still says running (#5602)", () => {
    // a foreground call's result is in: done, whatever the list says (its running lags, or is still true for 20 min)
    expect(agentStatus(res("42 passed"), meta(true), true)).toBe("completed");
    expect(agentStatus(res("42 passed"), meta(true), false)).toBe("completed");
    // the list dropping it (past a day, no meta) does not bring a result back to running either
    expect(agentStatus(res("42 passed"), undefined, true)).toBe("completed");
    // a background one goes by the list once its receipt is in
    const receipt = res("Async agent launched successfully.\nagentId: x (This is an internal ID for your use)");
    expect(agentStatus(receipt, meta(true), false)).toBe("running");
    expect(agentStatus(receipt, meta(false), true)).toBe("completed");
  });

  it("times a background subagent to its last line, not its launch receipt (#5602)", () => {
    const receipt: Item = { ...res("Async agent launched successfully."), ts: "2026-11-06T10:00:00Z" };
    expect(subagentEnded(receipt, { ...meta(false), updated_at: "2026-11-06T10:07:00Z" })).toBe(Date.parse("2026-11-06T10:07:00Z"));
    const done: Item = { ...res("42 passed"), ts: "2026-11-06T10:03:00Z" };
    expect(subagentEnded(done, { ...meta(false), updated_at: "2026-11-06T10:09:00Z" })).toBe(Date.parse("2026-11-06T10:03:00Z"));
    expect(subagentEnded(undefined, undefined)).toBeUndefined();
  });

  it("tells which subagents the agent waits on and what they do (#5602)", () => {
    const a = (id: string, extra: Partial<Subagent>): Subagent => ({ id, session_id: `s@${id}`, running: true, tool_use_id: `c-${id}`, ...extra });
    const list = [
      a("x", { last_tool: { id: "t1", name: "Read", summary: "在读 a.go" }, updated_at: "2026-11-06T10:00:01Z" }),
      a("y", { last_tool: { id: "t2", name: "Bash", summary: "跑测试" }, updated_at: "2026-11-06T10:00:05Z" }),
      a("z", {}), // no call yet
      a("bg", { background: true, last_tool: { id: "t3", name: "Bash", summary: "后台跑" } }),
      a("done", { running: false }),
      a("answered", { last_tool: { id: "t4", name: "Grep", summary: "在搜" } }),
    ];
    expect(agentsWorking(list, new Set(["c-answered"]))).toEqual({ count: 3, doing: ["跑测试", "在读 a.go"] });
    expect(agentsWorking([a("bg", { background: true })], new Set())).toBeNull();
    expect(agentsWorking(undefined, new Set())).toBeNull();
  });

  it("keeps the subagents array while only updated_at moved (#5602)", () => {
    const one = [{ ...meta(true), updated_at: "2026-11-06T10:00:00Z" }];
    const later = [{ ...meta(true), updated_at: "2026-11-06T10:00:01Z" }];
    expect(stableSubagents(one, later)).toBe(one);
    const stopped = [{ ...meta(false), updated_at: "2026-11-06T10:00:02Z" }];
    expect(stableSubagents(one, stopped)).toBe(stopped);
    const text = [{ ...meta(true), updated_at: "2026-11-06T10:00:03Z", last_text: "又说了一句" }];
    expect(stableSubagents(one, text)).toBe(text);
    expect(stableSubagents(one, [...one, { ...meta(true), id: "b", session_id: "s@b" }])).toHaveLength(2);
    expect(stableSubagents(one, undefined)).toBeUndefined();
    // the latest call comes as a new object on every push: the same call keeps the array, a new one changes it
    const tool = (id: string) => [{ ...meta(true), steps: 2, last_tool: { id, name: "Read", summary: "在读 a.go" } }];
    const withTool = tool("t1");
    expect(stableSubagents(withTool, tool("t1"))).toBe(withTool);
    expect(stableSubagents(withTool, tool("t2"))).not.toBe(withTool);
  });

  it("summarizes the report without its agentId line; a launch receipt by the latest words", () => {
    expect(agentSummary(res("结算流程 42 个用例全部通过。\nagentId: sa1 (use SendMessage with to: 'sa1' to continue this agent)"), undefined)).toBe("结算流程 42 个用例全部通过。");
    expect(agentSummary(res("Async agent launched successfully.\nagentId: sa2 (This is an internal ID for your use)"), meta(true))).toBe("最近说的话");
    expect(agentSummary(undefined, undefined)).toBe("");
  });

});

describe("subagent cards (#5030)", () => {
  it("modelChipLabel: a Claude id as family and version, GPT as OpenAI writes it, anything else in title case", () => {
    const { modelChipLabel } = tasksModule;
    expect(modelChipLabel("claude-haiku-4-5-20251001")).toBe("Haiku 4.5");
    expect(modelChipLabel("claude-opus-4-8[1m]")).toBe("Opus 4.8");
    expect(modelChipLabel("claude-fable-5")).toBe("Fable 5");
    expect(modelChipLabel("us.anthropic.claude-sonnet-4-6")).toBe("Sonnet 4.6");
    expect(modelChipLabel("gateway/claude-sonnet-4-6")).toBe("Sonnet 4.6");
    expect(modelChipLabel("gpt-5.4-mini")).toBe("GPT-5.4 Mini");
    expect(modelChipLabel("some-future-model")).toBe("Some Future Model");
    expect(modelChipLabel(undefined)).toBe("");
    expect(modelChipLabel("  ")).toBe("");
  });

  it("withNestedSubagents: what a subagent launched is a card of its own, by when it started, after its launcher's call", () => {
    const { withNestedSubagents, isNestedSubagentStep, nestedSubagentStatus } = tasksModule;
    const ts = (s: number) => new Date(Date.parse("2026-11-04T10:00:00Z") + s * 1000).toISOString();
    const step = (id: string, seq: number, s: number, name = "Bash"): Step => ({ kind: "tool", id, use: { id, seq, ts: ts(s), role: "assistant", kind: "tool_use", tool_name: name, tool_use_id: `c-${id}` }, images: [] });
    const steps = [step("a", 1, 0, "Agent"), step("b", 2, 10), step("c", 3, 30)];
    const all: Subagent[] = [
      { id: "root", session_id: "s@root", tool_use_id: "c-a", running: true, started_at: ts(0) },
      { id: "kid", session_id: "s@kid", tool_use_id: "c-kid", parent_id: "root", running: false, started_at: ts(20), description: "Look it up", type: "Explore" },
      { id: "grandkid", session_id: "s@grandkid", tool_use_id: "c-gk", parent_id: "kid", running: true, started_at: ts(5) },
      { id: "other", session_id: "s@other", tool_use_id: "c-elsewhere", running: true, started_at: ts(1) },
    ];
    const out = withNestedSubagents(steps, all);
    expect(out.map((s) => s.id)).toEqual(["a", "subagent:grandkid", "b", "subagent:kid", "c"]);
    const kid = out[3];
    expect(isNestedSubagentStep(kid)).toBe(true);
    expect(kid.kind === "tool" && kid.use).toMatchObject({ tool_name: "Agent", tool_use_id: "c-kid", tool_input: { description: "Look it up" }, seq: 2 });
    expect(isNestedSubagentStep(steps[0])).toBe(false);
    expect(nestedSubagentStatus(all[1])).toBe("completed");
    expect(nestedSubagentStatus(all[2])).toBe("running");
    // a fork the node names no call of gets one of its own; launchers naming each other add nothing twice
    const fork = withNestedSubagents(steps, [all[0], { id: "f", session_id: "s@f", parent_id: "root", started_at: ts(15) }]);
    expect(fork[2].kind === "tool" && fork[2].use.tool_use_id).toBe("subagent:f");
    const loop = withNestedSubagents(steps, [all[0], { id: "x", session_id: "s@x", parent_id: "y", tool_use_id: "c-x" }, { id: "y", session_id: "s@y", parent_id: "x", tool_use_id: "c-y" }]);
    expect(loop).toBe(steps);
    // nothing nested: the same array
    expect(withNestedSubagents(steps, [all[0]])).toBe(steps);
    expect(withNestedSubagents(steps, [])).toBe(steps);
  });
});
