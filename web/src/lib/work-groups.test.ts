// Ported from cindy's workGroupDeliveryProse / workGroupDurationAnchor / workGroupRunningSubagent / workGroupHistoryGap
// tests (apps/desktop/src/renderer/__tests__), on transcript items instead of cindy's chat messages.
import { describe, expect, it } from "vitest";
import { exchangeSteps, segments } from "./turns";
import type { Item } from "./types";
import {
  nestedLineDefault,
  togglesLiveOverride,
  workLineOpen,
  workLineStyle,
  groupsHolding,
  explorationSummary,
  groupOpens,
  groupTitle,
  isRowStep,
  isWorkflowStep,
  planProgress,
  planSteps,
  planTodos,
  thinkingDurations,
  workBlocks,
  workDuration,
  type AgentStatus,
  type ToolStep,
  type WorkGroupNode,
  type WorkNode,
} from "./work-groups";

const T0 = Date.parse("2026-07-01T00:00:00.000Z");
const iso = (offsetMs: number) => new Date(T0 + offsetMs).toISOString();
let seq = 0;
const mk = (role: Item["role"], kind: Item["kind"], at: number | undefined, extra: Partial<Item> = {}): Item => {
  seq++;
  return { id: `${extra.id ?? `i${seq}`}#0`, seq, role, kind, ...(at !== undefined ? { ts: iso(at) } : {}), ...extra, ...(extra.id ? { id: `${extra.id}#0` } : {}) };
};
const user = (id: string, at?: number, text = "巡检待放行的 PR，给一份产品决策简报") => mk("user", "text", at, { id, text });
const say = (id: string, text: string, at?: number) => mk("assistant", "text", at, { id, text });
const think = (id: string, at?: number, text = "Reviewing the repository structure…") => mk("assistant", "thinking", at, { id, text });
const tool = (id: string, name = "Bash", at?: number, resultAt?: number, input: Record<string, unknown> = { command: "gh pr diff 1111" }, result = "ok"): Item[] => [
  mk("assistant", "tool_use", at, { id, tool_name: name, tool_use_id: `tu-${id}`, tool_input: input }),
  mk("user", "tool_result", resultAt ?? at, { id: `${id}-r`, tool_use_id: `tu-${id}`, text: result }),
];

function blocks(items: Item[], live = false, agentStatus?: (s: ToolStep) => AgentStatus): WorkNode[] {
  const seg = segments(items)[0];
  void agentStatus;
  return workBlocks(exchangeSteps(seg), { live, turnStart: seg.user?.ts ?? seg.start });
}

const idOf = (n: WorkNode) => (n.kind === "step" ? n.step.id.replace(/#\d+$/, "") : n.id);
const topSteps = (ns: WorkNode[]) => ns.filter((n) => n.kind === "step").map(idOf);
const groups = (ns: WorkNode[]) => ns.filter((n): n is WorkGroupNode => n.kind === "group");
function folded(ns: WorkNode[], id: string): boolean {
  const has = (n: WorkNode): boolean => (n.kind === "step" ? idOf(n) === id : n.children.some(has));
  return groups(ns).some((g) => g.children.some(has));
}

/** "body → side-effect actions to wrap up → a one-line wrap-up": the shape that folded a 3250-character brief (cindy, 2026-07-31). */
const briefThenNotify = (body: string): Item[] => [
  user("u1"),
  think("th1"),
  ...tool("diff", "Bash", undefined, undefined, { command: "gh pr diff 1111" }, "diff --git a/apps/mobile/app/devices/index.tsx"),
  say("brief", body),
  ...tool("notify", "mcp__scheduler__notify"),
  say("wrap", "本轮有 3 条首次亮相的 PR 需要你决策，已触发通知。"),
];
const longPlain = `本轮 7 条有活动，3 条首次亮相。${"逐条核对了改动落在哪些产品面。".repeat(50)}`;

describe("only the final text stays outside execution (#2602)", () => {
  it("folds long intermediate text along with the tools", () => {
    expect(longPlain.length).toBeGreaterThanOrEqual(600);
    const b = blocks(briefThenNotify(longPlain));
    expect(topSteps(b)).toEqual(["wrap"]);
    expect(folded(b, "brief")).toBe(true);
    expect(folded(b, "th1")).toBe(true);
  });

  it.each([
    ["a heading", "## 产品决策简报\n\n本轮 7 条有活动。"],
    ["a list of three", "需要你决定的三条：\n- #1111 手机离线缓存\n- #692 effort 阻断发送\n- #1080 插件授权重构"],
    ["a table", "PR 一览：\n\n| PR | 建议 |\n| --- | --- |\n| #1111 | 放行 |"],
    ["a Markdown image", "图表如下 ![chart](https://example.com/chart.png)"],
    ["an <img>", '缩略图：<img src="https://example.com/thumb.png" width="150">'],
  ])("folds intermediate Markdown with %s", (_, body) => {
    expect(topSteps(blocks(briefThenNotify(body)))).toEqual(["wrap"]);
  });

  it.each([
    ["a one-line note", "我已经读完所有必要信息，现在写简报。"],
    ["a plain link", "先看 [PR](https://github.com/x/y/pull/1) 的 diff。"],
    ["an <img> without src", "说明里提到 <img> 标签。"],
    ["a custom <img-wrapper>", '<img-wrapper src="https://example.com/a.png">'],
    ["a list of two", "接下来两件事：\n- 读 diff\n- 写简报"],
    ["a lone rule", "先跑脚本生成简报数据。\n\n---\n\n然后读结果。"],
  ])("a progress note folds: %s", (_, body) => {
    const b = blocks(briefThenNotify(body));
    expect(topSteps(b)).toEqual(["wrap"]);
    expect(folded(b, "brief")).toBe(true);
  });


});

describe("a finished exchange", () => {
  it("keeps final text and all intermediate narration/tools in one flat inspectable summary", () => {
    const b = blocks([user("u1", 0), think("th1", 5951), say("a1", "Checking the code.", 6062), ...tool("b1", "Bash", 6500, 7000), say("a2", "Final result.", 29000)]);
    expect(topSteps(b)).toEqual(["a2"]);
    const [outer] = groups(b);
    expect(outer.children.map(idOf)).toEqual(["th1", "a1", "b1"]);
    expect(outer.durationMs).toBe(29000);
    expect(outer.live).toBe(false);
  });

  it("without your message the first run counts from its own step", () => {
    const b = blocks([think("th1", 5951), say("a1", "我先摸一下。", 6062), ...tool("b1", "Bash", 6500, 7000), say("a2", "看法如下。", 29000)]);
    expect(groups(b)[0].durationMs).toBe(29000 - 5951);
  });

  it("an interrupted exchange keeps its latest text and one completed summary", () => {
    const b = blocks([user("u1", 0), ...tool("t1", "Bash", 1000), say("a1", "接着看。", 2000), ...tool("t2", "Read", 3000)]);
    expect(b.map((n) => n.kind)).toEqual(["group", "step"]);
    expect(groups(b).every((g) => !g.live)).toBe(true);
  });

  it("only the final text segment stays visible, earlier segments remain inspectable", () => {
    const b = blocks([user("u1", 0), ...tool("t1", "Bash", 1000), say("a1", "第一部分。", 5000), say("a2", "第二部分。", 6000)]);
    expect(topSteps(b)).toEqual(["a2"]);
    expect(folded(b, "a1")).toBe(true);
  });
});

describe("while the agent works", () => {
  it("the tail run is live; its clock starts at your message", () => {
    const b = blocks([user("u1", 0), think("th1", 5951)], true);
    const [g] = groups(b);
    expect(g.live).toBe(true);
    expect(g.startedAt).toBe(T0);
    expect(groupTitle(g)).toBe("正在工作…");
  });

  it("new text replaces earlier text while subsequent tools remain in the same summary", () => {
    const items = [user("u1", 0), ...tool("t1", "Bash", 1000), say("a1", "First progress.", 2000), ...tool("t2", "Bash", 3000)];
    const first = blocks(items, true);
    expect(first.map((n) => n.kind === "group" ? `g:${n.live}` : idOf(n))).toEqual(["g:true", "a1"]);
    const newer = blocks([...items, say("a2", "Latest progress.", 4000), ...tool("t3", "Bash", 5000)], true);
    expect(topSteps(newer)).toEqual(["a2"]);
    expect(groups(newer)).toHaveLength(1);
    expect(groups(newer)[0].id).toBe(groups(first)[0].id);
    expect(folded(newer, "a1")).toBe(true);
    expect(groups(newer)[0].children.map(idOf)).toEqual(["t1", "a1", "t2", "t3"]);
  });

  it("the first running tool can be opened without a preview threshold", () => {
    const [g] = groups(blocks([user("u1", 0), ...tool("t1", "Bash", 1000)], true));
    expect(groupOpens(g)).toBe(true);
    expect(groupOpens({ ...g, children: [] })).toBe(false);
  });

  it("thinking and folded latest_thinking stay in the summary, not the text slot", () => {
    const thought = think("note", 500, "Checking the example.");
    const fold = mk("assistant", "fold", 500, { id: "fold:note", fold: { items: 13, tools: 6, thoughts: 1, through_seq: 99, latest_thinking: thought } });
    const b = blocks([user("u1", 0), fold, say("a1", "Latest text.", 2000), think("new", 3000)], true);
    expect(topSteps(b)).toEqual(["a1"]);
    expect(groups(b)[0].children.map(idOf)).toEqual(["fold:note", "new"]);
    expect(groups(b)[0].children[0]).toMatchObject({ step: { item: fold } });
  });

  it("streaming supersedes the persisted text without losing it from inspection", () => {
    const seg = segments([user("u1", 0), say("a1", "Earlier progress.", 1000), ...tool("t1", "Bash", 2000)])[0];
    const b = workBlocks(exchangeSteps(seg), { live: true, streaming: true, turnStart: seg.user?.ts });
    expect(topSteps(b)).toEqual([]);
    expect(folded(b, "a1")).toBe(true);
  });

  it("the summary keeps its key when the final text arrives and the run ends", () => {
    const items = [user("u1", 0), ...tool("t1", "Bash", 1000), say("a1", "Progress.", 2000), ...tool("t2", "Bash", 3000)];
    const running = blocks(items, true);
    const done = blocks([...items, say("final", "Final result.", 4000)]);
    expect(groups(done)[0].id).toBe(groups(running)[0].id);
    expect(topSteps(done)).toEqual(["final"]);
    expect(folded(done, "a1")).toBe(true);
  });
});

describe("subagents", () => {
  const items = () => [user("u1", 0), ...tool("pre", "Bash", 500), ...tool("task1", "Agent", 1000, 9000, { description: "回归", prompt: "跑回归" }), say("final", "都做完了。", 10000)];

  // Super rule 2 keeps every tool call in the exchange's one summary, a running subagent's too (cindy keeps a running
  // one in the open; that waits on an amendment of the rule).
  it("a running subagent stays inside the execution summary", () => {
    const b = blocks(items(), false, () => "running");
    expect(topSteps(b)).toEqual(["final"]);
    expect(folded(b, "task1")).toBe(true);
    expect(groups(b).length).toBeGreaterThan(0);
  });

  it("a finished subagent stays inside the same summary without changing the key", () => {
    const same = items();
    const running = blocks(same, false, () => "running");
    const done = blocks(same, false, () => "completed");
    expect(topSteps(done)).toEqual(["final"]);
    expect(folded(done, "task1")).toBe(true);
    // the same groups, with the same keys, either way
    expect(groups(done).map((g) => g.id)).toEqual(groups(running).map((g) => g.id));
  });

  it("a subagent card and the tools around it share one summary", () => {
    const b = blocks([user("u1", 0), ...tool("a", "Bash", 500), ...tool("task1", "Agent", 1000), ...tool("b", "Bash", 2000)], true, () => "running");
    expect(b.map((n) => (n.kind === "group" ? "g" : idOf(n)))).toEqual(["g"]);
  });
});

describe("history gaps", () => {
  const MIN = 60_000;
  it("steps on either side of a gap over 30 minutes are in different groups, neither spanning it", () => {
    const b = blocks([user("u1", 0), ...tool("t1", "Read", MIN), ...tool("t2", "Read", 2 * MIN), ...tool("t3", "Read", 50 * MIN), ...tool("t4", "Read", 51 * MIN), say("a", "好了。", 52 * MIN)]);
    const gs = groups(b);
    expect(gs.length).toBe(2);
    expect(gs.every((g) => (g.durationMs ?? 0) < 30 * MIN)).toBe(true);
  });

  it("a tool that ran 40 minutes is not a gap: the answer after its result still closes the exchange", () => {
    const b = blocks([user("u1", 0), ...tool("long", "Bash", MIN, 41 * MIN), say("a", "构建好了。", 42 * MIN)]);
    expect(topSteps(b)).toEqual(["a"]);
    expect(groups(b)[0].durationMs).toBe(42 * MIN);
  });

  it("calls each close to the last, an hour in all, stay one group", () => {
    const b = blocks([user("u1", 0), ...Array.from({ length: 7 }, (_, i) => tool(`t${i}`, "Bash", i * 10 * MIN)).flat(), say("a", "完成。", 61 * MIN)]);
    expect(groups(b)).toHaveLength(1);
  });

  it("without words after it, a group's end is its latest result, not its last call", () => {
    const b = blocks([...tool("long", "Bash", 0, 40 * MIN), ...tool("short", "Bash", MIN, 2 * MIN)]);
    expect(groups(b)[0].durationMs).toBe(40 * MIN);
  });
});

describe("titles and plans", () => {
  it("a group that only read and searched says so", () => {
    const b = blocks([user("u1", 0), ...tool("r1", "Read", 1000, undefined, { file_path: "/a.ts" }), ...tool("r2", "Read", 2000, undefined, { file_path: "/b.ts" }), ...tool("r3", "Read", 2500, undefined, { file_path: "/a.ts" }), ...tool("g", "Grep", 3000, undefined, { pattern: "x" }), say("a", "看完了。", 65_000)]);
    const [g] = groups(b);
    expect(explorationSummary(g)).toBe("读取 2 个文件 · 搜索 1 次");
    expect(groupTitle(g)).toBe("已工作 1 分 5 秒 · 读取 2 个文件 · 搜索 1 次");
  });

  it("one that ran a command does not", () => {
    const g = groups(blocks([user("u1", 0), ...tool("r1", "Read", 1000, undefined, { file_path: "/a.ts" }), ...tool("b", "Bash", 2000), say("a", "好。", 6000)]))[0];
    expect(groupTitle(g)).toBe("已工作 6 秒");
  });

  it("formats durations like cindy, in Chinese units", () => {
    expect(workDuration(0)).toBe("1 秒");
    expect(workDuration(6062)).toBe("6 秒");
    expect(workDuration(65_000)).toBe("1 分 5 秒");
    expect(workDuration(120_000)).toBe("2 分钟");
    expect(workDuration(3_900_000)).toBe("1 小时 05 分");
    expect(workDuration(3_600_000)).toBe("1 小时");
    expect(workDuration(93_900_000)).toBe("1 天 02 小时 05 分");
  });

  it("keeps every plan update in the manual trace, while planSteps still selects the latest for the composer", () => {
    const todos = (s: string[]) => ({ todos: s.map((content, i) => ({ content, status: i === 0 ? "completed" : i === 1 ? "in_progress" : "pending" })) });
    const items = [user("u1", 0), ...tool("p1", "TodoWrite", 1000, undefined, todos(["Read", "Edit"])), ...tool("t", "Bash", 2000), ...tool("p2", "TodoWrite", 3000, undefined, todos(["Read", "Edit", "Test"])), ...tool("one", "TodoWrite", 3500, undefined, todos(["One"])), say("a", "Done.", 4000)];
    const b = blocks(items);
    expect(topSteps(b)).toEqual(["a"]);
    for (const id of ["p1", "p2", "one"]) expect(folded(b, id)).toBe(true);
    const selected = planSteps(exchangeSteps(segments(items)[0])).filter((s) => s.kind === "tool" && s.use.tool_name === "TodoWrite");
    expect(selected).toHaveLength(1);
    expect(planProgress(planTodos(selected[0] as ToolStep))).toEqual({ done: 1, total: 3, current: "Edit", step: 2, allDone: false });
  });

  it("thoughts take from the step before to when they were written", () => {
    const seg = segments([user("u1", 0), think("th", 3000, ""), ...tool("t", "Bash", 4000, 9000), think("th2", 12000)])[0];
    const d = thinkingDurations(seg.steps, seg.user?.ts);
    expect(d.get("th#0")).toBe(3000);
    expect(d.get("th2#0")).toBe(3000);
  });
});

describe("planProgress", () => {
  it("names the next pending step when none is in progress, the same one step points at", () => {
    const p = planProgress([
      { content: "A", status: "completed" },
      { content: "B", status: "pending" },
      { content: "C", status: "pending" },
    ]);
    expect(p).toMatchObject({ done: 1, total: 3, step: 2, current: "B", allDone: false });
  });
  it("names the one in progress, else the last when all are done", () => {
    expect(planProgress([{ content: "A", status: "completed" }, { content: "B", status: "in_progress" }]).current).toBe("B");
    expect(planProgress([{ content: "A", status: "completed" }, { content: "B", status: "completed" }])).toMatchObject({ current: "B", step: 2, allDone: true });
  });
});

describe("groupsHolding (#2719: a background task's notice → the call that started it)", () => {
  const items = () => [
    user("u1", 0),
    think("th", 1000),
    ...tool("grep", "Grep", 2000, 2500, { pattern: "x" }),
    ...tool("bg", "Bash", 3000, 3500, { command: "npm test", run_in_background: true }, "Command running in background with ID: b1"),
    ...tool("read", "Read", 4000, 4500, { file_path: "/a.ts" }),
    say("a", "测试在后台跑，跑完我再看。", 5000),
  ];

  it("names the groups to open, outer first, down to the call", () => {
    const b = blocks(items());
    const path = groupsHolding(b, "tu-bg");
    expect(path).not.toBeNull();
    expect(path!.length).toBeGreaterThan(0);
    // every id on the path is a group, each one inside the one before it
    let level: WorkNode[] = b;
    for (const id of path!) {
      const g = groups(level).find((x) => x.id === id);
      expect(g, id).toBeTruthy();
      level = g!.children;
    }
    expect(level.some((n) => n.kind === "step" && n.step.kind === "tool" && n.step.use.tool_use_id === "tu-bg")).toBe(true);
  });

  it("is empty for a call left in the open, null for one not there", () => {
    const b = blocks([user("u1", 0), ...tool("bg", "Bash", 1000, 1500, { command: "npm test" }), say("a", "好了", 2000)], true);
    const open = b.find((n) => n.kind === "step" && n.step.kind === "tool");
    if (open) expect(groupsHolding(b, "tu-bg")).toEqual([]);
    expect(groupsHolding(blocks(items()), "tu-nope")).toBeNull();
    expect(groupsHolding([], "tu-bg")).toBeNull();
  });

  it("looks in the open timeline's inner groups too", () => {
    const g = (id: string, children: WorkNode[]): WorkGroupNode => ({ kind: "group", id, children, live: false });
    const step = blocks(items()).flatMap(function all(n): WorkNode[] { return n.kind === "group" ? n.children.flatMap(all) : [n]; })
      .find((n) => n.kind === "step" && n.step.kind === "tool" && n.step.use.tool_use_id === "tu-bg")!;
    const tree = [g("outer", [g("inner-1", []), g("inner-2", [step])])];
    expect(groupsHolding(tree, "tu-bg")).toEqual(["outer", "inner-2"]);
  });
});

// #3434: a Workflow call is its run's card, not a row; while the hub lists the run (running or ended) it stays out of the
// folded work
describe("workflow calls", () => {
  const launch = () => [
    user("u1", 0),
    ...tool("grep", "Grep", 1000, 2000, { pattern: "x" }),
    ...tool("wf", "Workflow", 3000, 3000, { name: "parity-audit" }, "Workflow launched in background"),
    say("ans", "Started it in the background.", 4000),
  ];

  it("is not an ordinary row, and a group that launched one did more than read", () => {
    const [seg] = segments(launch());
    const wf = exchangeSteps(seg).find((s) => s.kind === "tool" && s.use.tool_name === "Workflow")!;
    expect(isWorkflowStep(wf)).toBe(true);
    expect(isRowStep(wf)).toBe(false);
    const g = groups(blocks(launch()))[0];
    expect(explorationSummary(g)).toBe("");
  });

  it("keeps workflow execution inside the same summary, whether live or completed", () => {
    const seg = segments(launch())[0];
    const steps = exchangeSteps(seg);
    for (const live of [true, false]) {
      const nodes = workBlocks(steps, { live, turnStart: seg.user?.ts });
      expect(folded(nodes, "wf")).toBe(true);
      expect(topSteps(nodes)).toEqual(["ans"]);
      expect(groups(nodes)).toHaveLength(1);
    }
  });
});

describe("workLineOpen (#2602)", () => {
  it.each(["assistant", "agents"] as const)("%s uses the same collapsed default and manual inspection", (style) => {
    expect(workLineOpen({ style, turnLive: true, remembered: false })).toBe(false);
    expect(workLineOpen({ style, turnLive: true, remembered: false, liveOverride: true })).toBe(true);
    expect(workLineOpen({ style, turnLive: true, remembered: true, liveOverride: false })).toBe(false);
    expect(workLineOpen({ style, turnLive: false, remembered: false, liveOverride: true })).toBe(false);
    expect(workLineOpen({ style, turnLive: false, remembered: true })).toBe(true);
    expect(togglesLiveOverride(style, true)).toBe(true);
    expect(togglesLiveOverride(style, false)).toBe(false);
    expect(nestedLineDefault(style)).toBe(true);
  });
});

describe("workLineStyle (#1614)", () => {
  it("a Persona conversation is assistant style on the assistant page and when opened from the Agents page", () => {
    expect(workLineStyle(true, undefined)).toBe("assistant");
    expect(workLineStyle(false, true)).toBe("assistant");
    expect(workLineStyle(undefined, true)).toBe("assistant");
    expect(workLineStyle(false, false)).toBe("agents");
    expect(workLineStyle(undefined, undefined)).toBe("agents");
  });
});
