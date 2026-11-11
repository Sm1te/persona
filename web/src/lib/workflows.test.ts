import { describe, expect, it } from "vitest";
import type { Session, Workflow, WorkflowAgent } from "./types";
import {
  backgroundWaitLine,
  agentDetail,
  agentElapsed,
  agentRunState,
  backgroundCount,
  backgroundLine,
  otherBackground,
  phaseCount,
  phaseRows,
  phaseTitle,
  recentWorkflows,
  tallyParts,
  workflowAgentFor,
  workflowCalls,
  workflowCallsKey,
  workflowCounts,
  workflowCurrent,
  workflowElapsed,
  workflowLine,
  workflowPhase,
  workflowPhaseBlocks,
  workflowShortLine,
  workflowStatus,
  workflowTally,
  workflowDurationMs,
  workflowProgress,
  workflowStrip,
  workflowTaskStatus,
  waitingOnWorkflow,
  STRIP_CELLS,
  WORKFLOW_KEEP_MS,
} from "./workflows";

const start = Date.parse("2026-11-05T13:46:57Z");
const run: Workflow = {
  task_id: "wdp5zxh37",
  run_id: "wf_c1d9daf8-cd2",
  name: "parity-audit",
  status: "running",
  running: true,
  phases: ["Audit", "Verify"],
  phase: "Verify",
  phase_index: 2,
  agents_started: 185,
  agents_done: 169,
  agents_running: 16,
  started_at: "2026-11-05T13:46:57Z",
  agents: [
    { id: "a1", session_id: "s1@a1", label: "audit:chat", phase: "Audit", running: false, last_text: "found 2 gaps" },
    { id: "a2", session_id: "s1@a2", label: "verify:chat#0", phase: "Verify", running: true },
    { id: "a3", session_id: "s1@a3", label: "verify:chat#1", phase: "Verify", running: false },
  ],
};
const session = (over: Partial<Session>): Session => ({ machine_id: "m", session_id: "s1", status: "done", state: "working", ...over }) as Session;

describe("workflows (#2935)", () => {
  it("sums up a running run in one line", () => {
    const s = session({ workflows: [run], background: [{ id: "wdp5zxh37", kind: "workflow", run_id: run.run_id }] });
    expect(workflowLine(s, start + 14 * 60_000)).toBe("后台 workflow「parity-audit」 · Verify 2/2 · 169/185+ · 14 分");
    expect(workflowPhase(run)).toBe("Verify 2/2");
    expect(workflowShortLine(s)).toBe("169/185+ · parity-audit · Verify");
  });

  it("counts a run once, not also as its background task", () => {
    const s = session({ workflows: [run], background: [{ id: "wdp5zxh37", kind: "workflow" }, { id: "b1", kind: "shell" }] });
    expect(otherBackground(s).map((b) => b.id)).toEqual(["b1"]);
    expect(backgroundCount(s)).toBe(2);
  });

  it("falls back to 后台运行中 on a hub without workflows", () => {
    const s = session({ background: [{ id: "w1", kind: "workflow" }] });
    expect(workflowLine(s, start)).toBe("后台运行中");
    expect(otherBackground(s).map((b) => b.id)).toEqual(["w1"]); // a task like the others there
    expect(backgroundCount(s)).toBe(1);
  });

  it("says nothing for ended runs, and reads their status", () => {
    const ended: Workflow = { ...run, running: false, status: "killed", ended_at: "2026-11-05T13:47:12Z" };
    const s = session({ workflows: [ended] });
    expect(workflowLine(s, start)).toBe("");
    expect(backgroundCount(s)).toBe(0);
    expect(workflowStatus(ended)).toBe("killed");
    expect(workflowStatus({ ...ended, status: "something-new" })).toBe("completed");
    expect(workflowElapsed(ended, start + 3_600_000)).toBe("15 秒");
  });

  it("names more than one running run", () => {
    const s = session({ workflows: [run, { ...run, task_id: "w2", name: "review" }] });
    expect(workflowLine(s, start + 60_000)).toContain("（共 2 个）");
  });

  it("finds an agent among its runs, for the side panel's title", () => {
    expect(workflowAgentFor(session({ workflows: [run] }), "s1@a3")?.agent.label).toBe("verify:chat#1");
    expect(workflowAgentFor(session({ workflows: [run] }), "s1@zz")).toBeNull();
  });
});

// #3434: the run's card — counts with "+", blocks per phase, rows in start order folded past 12, the bar's parts
describe("the run's card (#3434)", () => {
  const agent = (i: number, phase: string, over: Partial<WorkflowAgent> = {}): WorkflowAgent => ({
    id: `a${i}`,
    session_id: `s1@a${i}`,
    label: `${phase.toLowerCase()}:${i}`,
    phase,
    running: false,
    status: "done",
    started_at: new Date(start + i * 1000).toISOString(),
    ended_at: new Date(start + i * 1000 + 90_000).toISOString(),
    ...over,
  });
  const stats: Workflow = {
    ...run,
    phases: ["Audit", "Verify", "Report"],
    phase_stats: [
      { title: "Audit", started: 180, done: 169, running: 11 },
      { title: "Verify", started: 5, done: 0, running: 5 },
      { title: "Report", started: 0, done: 0, running: 0 },
    ],
    agents: [agent(1, "Audit"), agent(2, "Audit", { running: true, status: "running" }), agent(3, "Verify", { running: true, status: "running" })],
  };

  it("counts done of started, with + only while it runs", () => {
    expect(workflowCounts(run)).toBe("169/185+");
    expect(workflowCounts({ ...run, running: false, status: "completed" })).toBe("169/185");
    expect(workflowCounts({ task_id: "x", status: "running", running: true })).toBe("0/0+");
  });

  it("draws one block per phase from phase_stats, in their order, the run's phase lit", () => {
    const blocks = workflowPhaseBlocks(stats);
    expect(blocks.map((b) => [b.title, b.state, b.current, phaseCount(b)])).toEqual([
      ["Audit", "current", false, "169/180+"],
      ["Verify", "current", true, "0/5+"],
      ["Report", "next", false, "0/0"],
    ]);
    expect(blocks[0].agents.map((a) => a.id)).toEqual(["a1", "a2"]);
    expect(blocks[1].agents.map((a) => a.id)).toEqual(["a3"]);
  });

  it("drops the + once the run ends, and marks phases that did not finish", () => {
    const ended: Workflow = {
      ...stats,
      running: false,
      status: "killed",
      phase_stats: [
        { title: "Audit", started: 4, done: 4, running: 0 },
        { title: "Verify", started: 3, done: 1, running: 0 },
      ],
    };
    const blocks = workflowPhaseBlocks(ended);
    expect(blocks.map((b) => [b.state, phaseCount(b), b.current])).toEqual([
      ["done", "4/4", false],
      ["stopped", "1/3", false],
    ]);
  });

  it("counts failures apart when the hub sends them (#5511), and reads without them", () => {
    const failed: Workflow = { ...stats, phase_stats: [{ title: "Audit", started: 6, done: 4, running: 0, failed: 2 }], phase: "Audit" };
    const [b] = workflowPhaseBlocks(failed);
    expect(b.failed).toBe(2);
    expect(b.state).toBe("current"); // the run is in it
    expect(workflowPhaseBlocks({ ...failed, phase: undefined })[0].state).toBe("done"); // all back, two failed
  });

  it("derives blocks from the agents on a hub without phase_stats", () => {
    const old: Workflow = { ...run, phase_stats: undefined };
    const blocks = workflowPhaseBlocks(old);
    expect(blocks.map((b) => [b.title, b.started, b.done, b.running, b.state])).toEqual([
      ["Audit", 1, 1, 0, "done"],
      ["Verify", 2, 1, 1, "current"],
    ]);
    // no phases at all: one block of its agents under a name of its own; none at all: nothing
    const plain: Workflow = { task_id: "p", status: "running", running: true, agents: [agent(1, ""), agent(2, "", { running: true })] };
    const [only] = workflowPhaseBlocks(plain);
    expect([phaseTitle(only), only.started, only.agents.length]).toEqual(["未分阶段", 2, 2]);
    expect(workflowPhaseBlocks({ task_id: "e", status: "running", running: true })).toEqual([]);
    // an ended run the hub lists no agents of: the phases before where it stopped passed, that one stopped
    const killed: Workflow = { ...old, running: false, status: "killed", phases: ["Scan", "Fix", "Check"], phase: "Fix", agents: [] };
    expect(workflowPhaseBlocks(killed).map((b) => b.state)).toEqual(["done", "stopped", "next"]);
  });

  it("keeps rows in start order, at most 12, the quiet tail folded where it is left out, running ones never", () => {
    const agents = Array.from({ length: 20 }, (_, i) => agent(i + 1, "Audit", i === 15 ? { running: true, status: "running" } : {}));
    const w: Workflow = { ...run, phase: "Audit", phase_stats: [{ title: "Audit", started: 20, done: 19, running: 1 }], agents };
    const [b] = workflowPhaseBlocks(w);
    const { rows, folded, breakAt, unlisted } = phaseRows(b);
    expect(rows.map((a) => a.id)).toEqual([...Array.from({ length: 12 }, (_, i) => `a${i + 1}`), "a16"]);
    expect(folded).toBe(7); // a13–a15, a17–a20: what "n more" opens
    expect(breakAt).toBe(12); // the first of them comes after a12
    expect(unlisted).toBe(0);
    const open = phaseRows(b, true);
    expect(open.rows).toHaveLength(20);
    expect(open.folded).toBe(0);
  });

  it("anchors a block the hub's cap cut into to its end, so the cap moving on shifts nothing", () => {
    // started 180 in Audit, the hub lists #126–180 (its newest 60 are these and 5 more elsewhere); #170 on still run
    const listed = (from: number) =>
      Array.from({ length: 181 - from }, (_, i) => agent(from + i, "Audit", from + i >= 170 ? { running: true, status: "running" } : {}));
    const block = (from: number) => workflowPhaseBlocks({ ...run, phase: "Verify", phase_stats: [{ title: "Audit", started: 180, done: 169, running: 11 }], agents: listed(from) })[0];
    const before = phaseRows(block(126));
    expect(before.rows.map((a) => a.id)).toEqual(Array.from({ length: 12 }, (_, i) => `a${169 + i}`));
    expect([before.folded, before.breakAt, before.unlisted]).toEqual([43, 0, 125]); // all that is hidden comes first
    // an agent starts elsewhere in the run: the hub drops #126; the rows are the same
    const after = phaseRows(block(127));
    expect(after.rows.map((a) => a.id)).toEqual(before.rows.map((a) => a.id));
    expect([after.folded, after.unlisted]).toEqual([42, 126]);
    // a running one before the window stays, the break after it
    const early = workflowPhaseBlocks({ ...run, phase_stats: [{ title: "Audit", started: 30, done: 28, running: 2 }], agents: Array.from({ length: 20 }, (_, i) => agent(i + 11, "Audit", i === 0 || i === 19 ? { running: true, status: "running" } : {})) })[0];
    const e = phaseRows(early);
    expect(e.rows.map((a) => a.id)).toEqual(["a11", ...Array.from({ length: 12 }, (_, i) => `a${19 + i}`)]);
    expect([e.folded, e.breakAt, e.unlisted]).toEqual([7, 1, 10]);
  });

  it("splits the bar into done, running, failed and stopped, stopped only once it ended", () => {
    expect(workflowTally(run)).toEqual({ started: 185, done: 169, running: 16, failed: 0, stopped: 0 });
    const killed: Workflow = { ...run, running: false, status: "killed", agents_running: 3, agents_failed: 2 };
    expect(workflowTally(killed)).toEqual({ started: 185, done: 169, running: 0, failed: 2, stopped: 14 });
    const parts = tallyParts(workflowTally(killed));
    expect(parts.map((p) => p.key)).toEqual(["done", "running", "failed", "stopped"]);
    expect(parts[2].from).toBeCloseTo(169 / 185);
    expect(parts.reduce((n, p) => n + p.size, 0)).toBeCloseTo(1);
    expect(tallyParts(workflowTally({ task_id: "z", status: "running", running: true })).every((p) => p.size === 0)).toBe(true);
  });

  it("says where it is now: the phase and its newest running agent", () => {
    expect(workflowCurrent(run)).toEqual({ phase: "Verify", agent: "verify:chat#0" });
    expect(workflowCurrent({ ...run, running: false })).toBeNull();
    expect(workflowCurrent({ ...run, phase: undefined, agents: [] })).toBeNull();
  });

  it("reads an agent's state, time and detail", () => {
    const a = agent(1, "Audit", { model: "claude-example", latest_tool: { name: "Read", summary: "Reading example.go" } });
    expect(agentRunState(a)).toBe("done");
    expect(agentRunState({ ...a, status: "stopped" })).toBe("stopped");
    expect(agentRunState({ ...a, status: "failed" })).toBe("failed");
    expect(agentRunState({ ...a, running: true })).toBe("running");
    expect(agentElapsed(a, start)).toBe("1 分");
    expect(agentElapsed({ ...a, running: true }, start + 1000 + 30_000)).toBe("30 秒");
    expect(agentElapsed({ ...a, started_at: undefined }, start)).toBe("");
    expect(agentDetail(a)).toBe("claude-example · Reading example.go");
    expect(agentDetail({ ...a, latest_tool: { name: "Grep" }, model: undefined })).toBe("Grep");
  });

  it("keeps an ended run (and the Background chip) for a day after it ended", () => {
    const ended: Workflow = { ...run, running: false, status: "completed", ended_at: new Date(start).toISOString() };
    const s = session({ workflows: [run, ended] });
    expect(recentWorkflows(s, start + WORKFLOW_KEEP_MS - 1000)).toHaveLength(2);
    expect(recentWorkflows(s, start + WORKFLOW_KEEP_MS + 1000).map((w) => w.running)).toEqual([true]);
  });

  it("knows the Workflow calls with a run and when each started", () => {
    const a: Workflow = { ...run, tool_use_id: "tu1" };
    const b: Workflow = { ...run, task_id: "w2", tool_use_id: "tu2", running: false, status: "completed", started_at: undefined };
    const calls = workflowCalls([a, b, { ...run, task_id: "w3" }]);
    expect([...calls.known]).toEqual(["tu1", "tu2"]);
    expect(calls.startedAt.get("tu1")).toBe(start);
    expect(calls.startedAt.get("tu2")).toBeNaN();
    // progress and a run ending do not change the key (the cards read those); a new run does
    expect(workflowCallsKey([{ ...a, agents_done: 170 }, b])).toBe(workflowCallsKey([a, b]));
    expect(workflowCallsKey([{ ...a, running: false }, b])).toBe(workflowCallsKey([a, b]));
    expect(workflowCallsKey([a])).not.toBe(workflowCallsKey([a, b]));
  });

  it("tells the running bar the workflow the agent waits for, and only that", () => {
    const s = session({ workflows: [{ ...run, tool_use_id: "tu1" }, { ...run, task_id: "w2", tool_use_id: "tu2" }], background_working: true });
    expect(backgroundLine(s)).toMatchObject({ run: { task_id: run.task_id }, more: 1, since: run.started_at, guessed: false });
    expect(waitingOnWorkflow(s)).toBe(true);
    // other background work (a command, a subagent) leaves the bar and the turn as they were
    const shell = session({ background_working: true, background: [{ id: "b1", kind: "shell", started_at: "2026-11-05T13:00:00Z" }], subagents: [{ id: "x", session_id: "s1@x", running: true }] });
    expect(backgroundLine(shell)).toBeNull();
    expect(waitingOnWorkflow(shell)).toBe(false);
    expect(waitingOnWorkflow(session({ workflows: [{ ...run, running: false, status: "completed" }], background_working: true }))).toBe(false);
    // the hub says the turn is live: nothing
    expect(backgroundLine(session({ workflows: [run], background_working: false }))).toBeNull();
    // a hub from before background_working (#3127): guessed from a running workflow
    expect(backgroundLine(session({ workflows: [run] }))?.guessed).toBe(true);
    expect(backgroundLine(session({ workflows: [run], state: "idle" }))).toBeNull();
    // a current hub leaves background_working out when it is false (a turn of its own): no guess
    expect(backgroundLine(session({ workflows: [{ ...run, tool_use_id: "tu1" }] }))).toBeNull();
    expect(backgroundLine(session({ workflows: [{ ...run, agents: [{ ...run.agents![1], status: "running" }] }] }))).toBeNull();
  });
});

describe("backgroundWaitLine (#6045)", () => {
  it("names what an agent waiting in the background waits on, and since when", () => {
    const s = { machine_id: "m", session_id: "s", state: "working", background_waiting: true, state_since: "2026-01-01T00:00:00Z",
      background: [{ id: "b1", kind: "shell", command: "until gh pr checks 7 --watch\ndo sleep 60; done", started_at: "" }, { id: "b2", kind: "monitor", description: "tail the log", started_at: "" }, { id: "b3", kind: "agent", started_at: "" }] } as Session;
    expect(backgroundWaitLine(s)).toEqual({ what: "until gh pr checks 7 --watch", more: 1, since: "2026-01-01T00:00:00Z" });
    expect(backgroundWaitLine({ ...s, background_waiting: false })).toBeNull();
    expect(backgroundWaitLine({ ...s, state: "idle" })).toBeNull();
  });
});

describe("the run's task card (#5030)", () => {
  const ended = (over: Partial<Workflow>): Workflow => ({ ...run, running: false, status: "completed", ended_at: "2026-11-05T14:01:57Z", ...over });

  it("says the four status words: killed and stalled read Stopped", () => {
    expect(workflowTaskStatus(run)).toBe("running");
    expect(workflowTaskStatus(ended({}))).toBe("completed");
    expect(workflowTaskStatus(ended({ status: "failed" }))).toBe("failed");
    expect(workflowTaskStatus(ended({ status: "killed" }))).toBe("stopped");
    expect(workflowTaskStatus(ended({ status: "stalled" }))).toBe("stopped");
    expect(workflowTaskStatus(ended({ status: "something new" }))).toBe("completed");
  });

  it("times an ended run to its end, or its last sign of life; nothing while it runs or with no start", () => {
    expect(workflowDurationMs(ended({}))).toBe(15 * 60_000);
    expect(workflowDurationMs(ended({ ended_at: undefined, updated_at: "2026-11-05T13:56:57Z" }))).toBe(10 * 60_000);
    expect(workflowDurationMs(ended({ ended_at: undefined }))).toBeNull();
    expect(workflowDurationMs(ended({ started_at: undefined }))).toBeNull();
    expect(workflowDurationMs(run)).toBeNull();
  });

  it("counts the agents back, failed ones too, of those started, with the phase while it runs", () => {
    expect(workflowProgress({ ...run, agents_failed: 3, agents_done: 166 })).toEqual({ phase: "Verify", done: 169, total: 185 });
    expect(workflowProgress(ended({ agents_running: 0 }))).toEqual({ phase: "", done: 169, total: 185 });
    expect(workflowProgress({ ...run, agents_started: 0 })).toBeNull();
  });

  it("draws a square per agent in start order, the listed ones with their labels", () => {
    const small: Workflow = { ...run, agents_started: 3, agents_done: 1, agents_running: 1, agents_failed: 1, agents: [
      { id: "a1", session_id: "s@a1", label: "one", running: false, status: "done" },
      { id: "a2", session_id: "s@a2", label: "two", running: false, status: "failed" },
      { id: "a3", session_id: "s@a3", running: true, status: "running" },
    ] };
    expect(workflowStrip(small)).toEqual({ cells: [{ state: "done", label: "one" }, { state: "failed", label: "two" }, { state: "running", label: "agent" }], more: 0 });
  });

  it("draws the agents the hub no longer lists from the run's counts, first, and stops at 40 with the rest as more", () => {
    // 185 started: 169 back (one of them failed), 16 running; the hub lists the newest three
    const big: Workflow = { ...run, agents_failed: 1, agents_done: 168 };
    const { cells, more } = workflowStrip(big);
    expect(cells).toHaveLength(STRIP_CELLS);
    expect(more).toBe(145);
    expect(cells.every((c) => c.state === "done" && !c.label)).toBe(true);
    // with room for all: the unlisted ones are the counts left over (168 - 2 listed done, 1 failed, 16 - 1 listed running), then the listed ones
    const all = workflowStrip(big, 200);
    expect(all.more).toBe(0);
    expect(all.cells).toHaveLength(185);
    const unlisted = all.cells.slice(0, 182).map((c) => c.state);
    expect(unlisted.filter((s) => s === "done")).toHaveLength(166);
    expect(unlisted.slice(166)).toEqual(["failed", ...Array(15).fill("running")]);
    expect(all.cells.slice(182).map((c) => c.label)).toEqual(["audit:chat", "verify:chat#0", "verify:chat#1"]);
  });

  it("says unknown for unlisted agents the counts do not account for", () => {
    // an ended run that lists none of its agents: those not back were stopped by its end
    const odd: Workflow = { ...run, running: false, status: "completed", agents_started: 5, agents_done: 1, agents_running: 0, agents: [] };
    expect(workflowStrip(odd).cells.map((c) => c.state)).toEqual(["done", "stopped", "stopped", "stopped", "stopped"]);
    // a running run whose counts have none done or running yet
    const none: Workflow = { ...run, agents_started: 4, agents_done: 0, agents_running: 0, agents: [] };
    expect(workflowStrip(none).cells.map((c) => c.state)).toEqual(["unknown", "unknown", "unknown", "unknown"]);
  });
});
