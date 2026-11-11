// @vitest-environment jsdom
// A workflow run's cards. In the chat, in place of the Workflow call that launched it, inside the folded work summary
// until the reader opens it (the call's row again when the hub lists no such run): a task card (#5030, after cindy) with the status word, what it used, its progress line and a
// strip of a square per agent, opening the chat's Background dialog at its run, or in place where the chat has no such
// dialog. In the dialog (#3434): one block per phase with "done/started+", rows in start order folded past 12, only to
// read; a bar in parts moved by transform. Things move only when a value changes: not on the first draw, not with
// reduced motion. Frames are driven by hand.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia = ((query: string) => ({
    get matches() {
      return query.includes("reduce") && (globalThis as { __reduce?: boolean }).__reduce === true;
    },
    media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { listenBackgroundDialog } from "../../lib/background-dialog";
import { reset, upsertSession, useStore } from "../../lib/store";
import { emptyTaskIndex } from "../../lib/tasks";
import { segments } from "../../lib/turns";
import type { Item, Session, Workflow, WorkflowAgent } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";
import { NO_WORKFLOW_CALLS, workflowCalls } from "../../lib/workflows";
import { TWEEN_MS } from "../../ui/motion";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { RunningStatusView } from "./RunningStatusBar";
import { SegmentView } from "./SegmentView";
import { WorkflowCallCard, WorkflowCard } from "./WorkflowCard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-06T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
let seq = 0;
const item = (x: Partial<Item>, ts: number): Item => ({ id: `i${++seq}#0`, seq, ts: at(ts), ...x }) as Item;

const agent = (i: number, phase: string, over: Partial<WorkflowAgent> = {}): WorkflowAgent => ({
  id: `a${i}`,
  session_id: `s@a${i}`,
  label: `${phase.toLowerCase()}:${i}`,
  phase,
  running: false,
  status: "done",
  started_at: at(10 + i),
  ended_at: at(70 + i),
  ...over,
});

const running = (over: Partial<Workflow> = {}): Workflow => ({
  task_id: "w1",
  run_id: "wf_1",
  name: "parity-audit",
  summary: "Audit each area, verify each finding",
  status: "running",
  running: true,
  tool_use_id: "call-wf",
  phases: ["Audit", "Verify"],
  phase: "Verify",
  phase_index: 2,
  phase_stats: [
    { title: "Audit", started: 3, done: 3, running: 0 },
    { title: "Verify", started: 2, done: 1, running: 1 },
  ],
  agents_started: 5,
  agents_done: 4,
  agents_running: 1,
  started_at: at(5),
  agents: [agent(1, "Audit"), agent(2, "Audit"), agent(3, "Audit"), agent(4, "Verify"), agent(5, "Verify", { running: true, status: "running", ended_at: undefined, model: "claude-example", latest_tool: { name: "Read", summary: "Reading example.go" } })],
  ...over,
});

const call = (id = "call-wf"): ToolStep => ({
  kind: "tool",
  id: `${id}#0`,
  use: { id: `${id}#0`, seq: 7, ts: at(5), role: "assistant", kind: "tool_use", tool_name: "Workflow", tool_use_id: id, tool_input: { name: "parity-audit" } },
  result: { id: `${id}-r#0`, seq: 8, ts: at(5), role: "user", kind: "tool_result", tool_use_id: id, text: "Workflow launched in background" },
  images: [],
});

let host: HTMLDivElement;
let root: Root;
let frames = new Map<number, FrameRequestCallback>();
let nextFrame = 0;
let clock = 0;

beforeEach(() => {
  frames = new Map();
  nextFrame = 0;
  clock = 0;
  (globalThis as { __reduce?: boolean }).__reduce = false;
  vi.stubGlobal("requestAnimationFrame", (f: FrameRequestCallback) => {
    frames.set(++nextFrame, f);
    return nextFrame;
  });
  vi.stubGlobal("cancelAnimationFrame", (id: number) => frames.delete(id));
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
  vi.setSystemTime(T0 + 120_000);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const tick = (ms: number) =>
  act(() => {
    clock += ms;
    const due = [...frames.values()];
    frames.clear();
    due.forEach((f) => f(clock));
  });
const settle = () => {
  for (let i = 0; i < 100 && frames.size; i++) tick(TWEEN_MS / 4);
  act(() => vi.advanceTimersByTime(1000));
};

let scope = 0;
/** The session with these runs, and its card for the call drawn (a new scope each test: open states are per page). */
function drawCard(runs: Workflow[], step = call()) {
  act(() => upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", workflows: runs } as Session));
  const sc = `t${++scope}:`;
  act(() => root.render(<WorkflowCallCard step={step} mid="m" sid="s" scope={sc} />));
  return sc;
}
/** The dialog's card of the session's first run, as the dialog draws it from the store. */
function DialogCard({ sc }: { sc: string }) {
  const run = useStore((s) => (s.sessions.m ?? []).find((x) => x.session_id === "s")?.workflows?.[0]);
  return run ? <WorkflowCard run={run} scope={sc} /> : null;
}
function drawDialogCard(runs: Workflow[]) {
  act(() => upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", workflows: runs } as Session));
  const sc = `d${++scope}:`;
  act(() => root.render(<DialogCard sc={sc} />));
  return sc;
}
const push = (runs: Workflow[]) => act(() => upsertSession({ machine_id: "m", session_id: "s", workflows: runs } as Session));
const q = (sel: string) => host.querySelector<HTMLElement>(sel);
const text = (sel: string) => q(sel)?.textContent ?? null;
const ended = (over: Partial<Workflow> = {}) =>
  running({ running: false, status: "completed", ended_at: at(100), agents_running: 0, agents_done: 5, agents: running().agents!.map((a) => ({ ...a, running: false, status: "done", ended_at: a.ended_at ?? at(90) })), ...over });

describe("the card in the chat", () => {
  it("draws the run as a task card: its name, the status word, what it used, how far it got and a square per agent", () => {
    drawCard([running({ tokens: 12_345, calls: 37 })]);
    const card = q('[data-workflow-call="running"]')!;
    expect(card.dataset.toolUse).toBe("call-wf");
    expect(card.textContent).toContain("parity-audit");
    expect(card.textContent).not.toContain("子 Agent"); // no kind label before a workflow's title
    expect(q("[data-task-status]")!.dataset.taskStatus).toBe("running");
    expect(text("[data-task-status]")).toBe("运行中");
    expect(q("[data-task-status] svg")).toBeNull(); // a spinner, not an outcome's icon
    expect([...host.querySelectorAll("[data-task-meta] > *")].map((e) => e.textContent)).toEqual(["Workflow", "12.3k tokens", "37 次工具调用", "已运行 1 分 55 秒"]);
    act(() => vi.advanceTimersByTime(2000));
    expect(text("[data-task-elapsed]")).toBe("已运行 1 分 57 秒"); // ticks each second
    expect(text("[data-workflow-progress-line]")).toBe("Verify · 4/5 Agent");
    expect([...host.querySelectorAll<HTMLElement>("[data-workflow-strip] [data-cell]")].map((c) => c.dataset.cell)).toEqual(["done", "done", "done", "done", "running"]);
    expect(q("[data-workflow-strip]")!.getAttribute("aria-hidden")).toBe("true");
    expect(q('[data-workflow-strip] [data-cell="running"]')!.title).toBe("verify:5 · 运行中");
    expect(q("[data-strip-more]")).toBeNull();
    // the old head's pieces are the dialog's now
    expect(q("[data-workflow-status], [data-workflow-counts], [data-workflow-now]")).toBeNull();
  });

  it("leaves out what the hub does not say, and says the time it took once the run has ended", () => {
    drawCard([ended()]);
    expect([...host.querySelectorAll("[data-task-meta] > *")].map((e) => e.textContent)).toEqual(["Workflow", "1 分 35 秒"]);
    expect(text("[data-workflow-progress-line]")).toBe("5/5 Agent"); // no phase once it ended
    expect(q("[data-task-elapsed]")).toBeNull();
  });

  it("says Completed, Failed or Stopped once the run ends; only Failed in red", () => {
    const cases: [Partial<Workflow>, string, string][] = [
      [{ status: "completed" }, "completed", "已完成"],
      [{ status: "failed", error: "Error: agent crashed" }, "failed", "失败"],
      [{ status: "killed" }, "stopped", "已停止"], // stopped by hand
      [{ status: "stalled" }, "stopped", "已停止"], // no word from it for 30 minutes: not known to have failed
    ];
    for (const [over, status, word] of cases) {
      drawCard([ended(over)]);
      const mark = q("[data-task-status]")!;
      expect(mark.dataset.taskStatus).toBe(status);
      expect(mark.textContent).toBe(word);
      expect(mark.className.includes("text-red")).toBe(status === "failed");
      expect(q("[data-workflow-call]")!.dataset.workflowCall).toBe(over.status); // the run's own word stays for scripts
    }
  });

  it("caps the strip at 40 squares with +n, the agents the hub no longer lists drawn from the run's counts", () => {
    // 185 started, 169 back (2 of them failed), 16 running; the hub lists the newest 60
    const listed = Array.from({ length: 60 }, (_, i) => agent(126 + i, "Audit", 126 + i > 169 ? { running: true, status: "running", ended_at: undefined } : {}));
    drawCard([running({ phase: "Audit", agents_started: 185, agents_done: 167, agents_failed: 2, agents_running: 16, agents: listed })]);
    const cells = [...host.querySelectorAll<HTMLElement>("[data-workflow-strip] [data-cell]")];
    expect(cells).toHaveLength(40);
    // the 125 unlisted: 167 - 44 listed done = 123 done, then the 2 failed; the first 40 of them show
    expect(cells.every((c) => c.dataset.cell === "done")).toBe(true);
    expect(cells[0].title).toBe("已完成");
    expect(text("[data-strip-more]")).toBe("+145");
    expect(text("[data-workflow-progress-line]")).toBe("Audit · 169/185 Agent");
  });

  it("draws to static markup (no getServerSnapshot error), opening in place there", () => {
    act(() => upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", workflows: [running()] } as Session));
    const html = renderToStaticMarkup(<WorkflowCallCard step={call()} mid="m" sid="s" scope="static:" />);
    expect(html).toContain('data-workflow-call="running"');
    expect(html).toContain('data-workflow-opens="inline"');
  });

  it("is the call's row again when the hub lists no such run", () => {
    drawCard([running({ tool_use_id: "another" })]);
    expect(q("[data-workflow-call]")).toBeNull();
    expect(q('[data-action-row="Workflow"]')).not.toBeNull();
  });

  it("with no Background dialog for its chat, opens in place to the run's phases; its rows only read", () => {
    drawCard([running()]);
    const head = q("[data-workflow-call] button")!;
    expect(q("[data-workflow-call]")!.dataset.workflowOpens).toBe("inline");
    expect(head.getAttribute("aria-expanded")).toBe("false"); // closed at first, running or not
    expect(head.getAttribute("aria-label")).toBe("展开「parity-audit」");
    expect(q("[data-workflow-phases]")).toBeNull();
    act(() => head.click());
    settle();
    expect(head.getAttribute("aria-expanded")).toBe("true");
    expect(head.getAttribute("aria-label")).toBe("收起「parity-audit」");
    expect(text("[data-workflow-inline] [data-workflow-summary]")).toBe("Audit each area, verify each finding");
    expect([...host.querySelectorAll("[data-phase]")].map((s) => [s.getAttribute("data-phase"), s.getAttribute("data-phase-state"), s.querySelector("[data-phase-count]")?.textContent])).toEqual([
      ["Audit", "done", "3/3"],
      ["Verify", "current", "1/2+"],
    ]);
    const row = q('[data-agent="a5"]')!;
    expect(row.tagName).toBe("LI");
    expect(row.querySelector("button, [role=button], a")).toBeNull();
    expect(row.className).not.toContain("hover:");
    expect(row.title).toBe("verify:5");
    // what the reader chose stays as the run ends
    push([ended({ status: "failed", error: "Error: 2 agents failed: snapshot:dark-large, and a reason long enough to need a second line" })]);
    expect(head.getAttribute("aria-expanded")).toBe("true");
    expect(q("[data-workflow-inline] [data-workflow-error]")!.className).toContain("break-words"); // whole, not one line
  });

  it("does not say a nameless run's summary twice", () => {
    drawCard([ended({ name: undefined })]);
    act(() => q("[data-workflow-call] button")!.click());
    expect(text("[data-task-meta]")).not.toContain("Audit each area");
    expect(q("[data-workflow-call]")!.textContent).toContain("Audit each area, verify each finding"); // the title
    expect(q("[data-workflow-inline] [data-workflow-summary]")).toBeNull();
  });
});

describe("the card and the chat's Background dialog", () => {
  it("opens the dialog at its run, with the panel mark and no chevron, while the chat's dialog lists the run", () => {
    const opened = vi.fn();
    const stop = listenBackgroundDialog("m", "s", ["w1"], opened);
    drawCard([running()]);
    const card = q("[data-workflow-call]")!;
    const head = card.querySelector("button")!;
    expect(card.dataset.workflowOpens).toBe("dialog");
    expect(head.hasAttribute("aria-expanded")).toBe(false);
    expect(head.getAttribute("aria-label")).toBe("在后台任务中打开 parity-audit");
    act(() => head.click());
    expect(opened).toHaveBeenCalledWith("w1");
    expect(q("[data-workflow-inline]")).toBeNull(); // nothing opens in place
    // the dialog goes (the chat's header closed): the card opens in place again
    act(() => stop());
    expect(card.dataset.workflowOpens).toBe("inline");
    expect(head.getAttribute("aria-expanded")).toBe("false");
  });

  it("opens in place when the chat's dialog does not list its run", () => {
    const opened = vi.fn();
    const stop = listenBackgroundDialog("m", "s", ["another"], opened);
    drawCard([running()]);
    act(() => q("[data-workflow-call] button")!.click());
    expect(opened).not.toHaveBeenCalled();
    expect(q("[data-workflow-call] button")!.getAttribute("aria-expanded")).toBe("true");
    stop();
  });
});

describe("the dialog's card", () => {
  it("draws the head, where it is now, the bar and a block per phase, and says where the run started", () => {
    const jump = vi.fn();
    act(() => root.render(<WorkflowCard run={running()} scope="dlg:" onJump={jump} />));
    const card = q('[data-workflow-card="running"]')!;
    expect(card.dataset.workflowTask).toBe("w1");
    expect(text("[data-workflow-status]")).toBe("在跑");
    expect(text("[data-workflow-counts]")).toBe("4/5+");
    expect(text("[data-workflow-now]")).toBe("当前 Verify：verify:5");
    expect(host.querySelectorAll("[data-phase]")).toHaveLength(2);
    // the block with an agent running is open, the finished one folded
    expect(q('[data-phase="Verify"] [data-phase-agents]')).not.toBeNull();
    expect(q('[data-phase="Audit"] [data-phase-agents]')).toBeNull();
    expect(q('[data-phase="Verify"]')!.hasAttribute("data-phase-current")).toBe(true);
    act(() => q("[data-workflow-jump]")!.click());
    expect(jump).toHaveBeenCalledWith("call-wf");
  });

  it("lists its agents as rows to read, not to open", () => {
    act(() => root.render(<WorkflowCard run={running()} scope="dlg2:" />));
    const rows = [...host.querySelectorAll<HTMLElement>("[data-agent]")];
    expect(rows.map((r) => r.dataset.agent)).toEqual(["a4", "a5"]);
    for (const r of rows) {
      expect(r.querySelector("button, [role=button], a")).toBeNull();
      expect(r.className).not.toContain("hover:");
    }
    expect(host.querySelector("[aria-label^='打开']")).toBeNull();
  });

  it("moves the bar's parts by transform: scaleX, never by width", () => {
    drawDialogCard([running()]);
    const parts = [...host.querySelectorAll<HTMLElement>("[data-workflow-progress] [data-part]")];
    expect(parts.map((p) => p.dataset.part)).toEqual(["done", "running", "failed", "stopped"]);
    expect(parts[0].style.transform).toBe("translateX(0.000%) scaleX(0.80000)");
    expect(parts[1].style.transform).toBe("translateX(80.000%) scaleX(0.20000)");
    for (const p of parts) {
      expect(p.style.width).toBe("");
      expect(p.className).toContain("transition-transform");
      expect(p.className).not.toMatch(/transition-(all|\[width)/);
    }
  });

  it("shows the summary and error whole", () => {
    const long = "Error: 2 agents failed: snapshot:dark-large, snapshot:ja-small, and a reason long enough to need a second line";
    drawDialogCard([ended({ status: "failed", error: long })]);
    expect(text("[data-workflow-error]")).toBe(long);
    expect(q("[data-workflow-error]")!.className).toContain("break-words");
    expect(q("[data-workflow-summary]")!.className).not.toContain("truncate");
  });

  /** A block's lines in order: an agent's id, "more", "fewer" or "unlisted". */
  const lines = (phase: string) =>
    [...host.querySelectorAll<HTMLElement>(`[data-phase="${phase}"] [data-phase-agents] > li`)].map(
      (li) => li.dataset.agent ?? (li.hasAttribute("data-phase-unlisted") ? "unlisted" : li.querySelector("[data-phase-more]") ? "more" : "fewer"),
    );
  const ids = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => `a${from + i}`);

  it("shows at most 12 rows in a phase, in start order, the quiet tail as 还有 n 个 where it is left out, running rows never folded", () => {
    const many = Array.from({ length: 20 }, (_, i) => agent(i + 1, "Audit", i === 16 ? { running: true, status: "running", ended_at: undefined } : {}));
    drawDialogCard([running({ phase: "Audit", phase_stats: [{ title: "Audit", started: 20, done: 19, running: 1 }], agents: many })]);
    expect(lines("Audit")).toEqual([...ids(1, 12), "more", "a17"]);
    expect(text("[data-phase-more]")).toBe("还有 7 个"); // what it opens: a13–a16, a18–a20
    act(() => q("[data-phase-more]")!.click());
    expect(lines("Audit")).toEqual([...ids(1, 20), "fewer"]);
    act(() => q("[data-phase-fewer]")!.click());
    expect(lines("Audit")).toEqual([...ids(1, 12), "more", "a17"]);
  });

  it("says the agents the hub no longer lists above the first row, and keeps the rows put as its cap moves on", () => {
    // 24 started in Audit, the hub lists the newest from `from` on; a18 on still run
    const capped = (from: number) =>
      running({
        phase: "Audit",
        phase_stats: [{ title: "Audit", started: 24, done: 21, running: 3 }],
        agents: Array.from({ length: 21 - from }, (_, i) => agent(from + i, "Audit", from + i >= 18 ? { running: true, status: "running", ended_at: undefined } : {})),
      });
    drawDialogCard([capped(1)]);
    expect(lines("Audit")).toEqual(["unlisted", "more", ...ids(9, 20)]);
    expect(text("[data-phase-unlisted]")).toBe("更早的 4 个没有列出");
    expect(text("[data-phase-more]")).toBe("还有 8 个");
    push([capped(2)]); // an agent started elsewhere in the run: the hub drops a1
    expect(lines("Audit")).toEqual(["unlisted", "more", ...ids(9, 20)]);
    expect(text("[data-phase-unlisted]")).toBe("更早的 5 个没有列出");
    expect(text("[data-phase-more]")).toBe("还有 7 个");
    expect([...host.querySelectorAll<HTMLElement>("[data-agent]")].every((r) => r.style.animation === "")).toBe(true); // nothing came in
    act(() => q("[data-phase-more]")!.click());
    expect(lines("Audit")).toEqual(["unlisted", ...ids(2, 20), "fewer"]);
  });

  it("keeps the phase the run is in open between one agent and the next", () => {
    drawDialogCard([running()]);
    expect(q('[data-phase="Verify"] [data-phase-agents]')).not.toBeNull();
    // a5 is back and the next has not started: none running there, the run still in Verify
    push([running({ agents_running: 0, agents_done: 5, phase_stats: [{ title: "Audit", started: 3, done: 3, running: 0 }, { title: "Verify", started: 2, done: 2, running: 0 }], agents: [...running().agents!.slice(0, 4), agent(5, "Verify")] })]);
    expect(q('[data-phase="Verify"]')!.dataset.phaseState).toBe("current");
    expect(q('[data-phase="Verify"] [data-phase-agents]')).not.toBeNull();
  });

  it("marks a phase some of whose agents failed with a red ✕, not a green ✓", () => {
    drawDialogCard([running({ running: false, status: "failed", ended_at: at(100), agents_running: 0, agents_failed: 1, phase_stats: [{ title: "Audit", started: 3, done: 2, running: 0, failed: 1 }, { title: "Verify", started: 2, done: 2, running: 0 }] })]);
    const audit = q('[data-phase="Audit"] [data-phase-mark]')!;
    expect(audit.dataset.phaseMark).toBe("done_failed");
    expect(audit.getAttribute("aria-label")).toBe("完成，有失败");
    expect(audit.querySelector(".text-red")).not.toBeNull();
    expect(audit.querySelector(".text-green")).toBeNull();
    expect(q('[data-phase="Verify"] [data-phase-mark]')!.dataset.phaseMark).toBe("done");
  });
});

describe("what moves", () => {
  it("rolls the counts when they change, not on the first draw", () => {
    drawDialogCard([running()]);
    expect(q("[data-rolling], [data-fading]")).toBeNull(); // the first draw does not move
    push([running({ agents_done: 5, agents_started: 9, phase_stats: [{ title: "Audit", started: 3, done: 3, running: 0 }, { title: "Verify", started: 6, done: 2, running: 4 }] })]);
    expect(q("[data-workflow-counts] [data-rolling]")).not.toBeNull();
    expect(text("[data-workflow-counts]")).toBe("4/5+"); // it starts from what was shown
    tick(0);
    tick(TWEEN_MS / 3);
    expect(text("[data-workflow-counts]")).toMatch(/^[45]\/[5-9]\+$/);
    settle();
    expect(text("[data-workflow-counts]")).toBe("5/9+");
    expect(q("[data-workflow-counts] [data-rolling]")).toBeNull();
  });

  it("fades a new agent in where it started, and nothing else moves", () => {
    drawDialogCard([running()]);
    const first = q('[data-agent="a5"]')!;
    expect(first.style.animation).toBe("");
    push([running({ agents: [...running().agents!, agent(6, "Verify", { running: true, status: "running", ended_at: undefined })] })]);
    const rows = [...host.querySelectorAll<HTMLElement>('[data-phase="Verify"] [data-agent]')];
    expect(rows.map((r) => r.dataset.agent)).toEqual(["a4", "a5", "a6"]); // start order: the new one at the end
    expect(rows[2].style.animation).toContain("fade-in");
    expect(rows[0].style.animation).toBe("");
    expect(rows[1].style.animation).toBe("");
    settle();
    expect(q('[data-agent="a6"]')!.style.animation).toBe("");
  });

  it("lights the next phase and fades in the ✓ of the one before it", () => {
    drawDialogCard([running({ phase: "Audit", phase_index: 1, phase_stats: [{ title: "Audit", started: 3, done: 2, running: 1 }, { title: "Verify", started: 0, done: 0, running: 0 }] })]);
    expect(q('[data-phase="Audit"]')!.hasAttribute("data-phase-current")).toBe(true);
    push([running()]);
    expect(q('[data-phase="Audit"]')!.hasAttribute("data-phase-current")).toBe(false);
    expect(q('[data-phase="Verify"]')!.hasAttribute("data-phase-current")).toBe(true);
    expect(q('[data-phase="Audit"] .text-green')!.style.animation).toContain("fade-in");
  });

  it("turns the chip with a fade when the run ends", () => {
    drawDialogCard([running()]);
    push([running({ running: false, status: "completed", ended_at: at(100) })]);
    expect(q("[data-workflow-status]")!.className).toContain("bg-green-tint");
    expect(q("[data-workflow-status] [data-fading]")?.textContent).toBe("完成");
  });

  it("turns the card's status word and strip in place when the run ends", () => {
    drawCard([running()]);
    const card = q("[data-workflow-call]")!;
    push([ended()]);
    expect(q("[data-workflow-call]")).toBe(card);
    expect(text("[data-task-status]")).toBe("已完成");
    expect([...host.querySelectorAll<HTMLElement>("[data-workflow-strip] [data-cell]")].every((c) => c.dataset.cell === "done")).toBe(true);
  });

  it("shows new values at once with reduced motion", () => {
    (globalThis as { __reduce?: boolean }).__reduce = true;
    drawDialogCard([running()]);
    push([running({ agents_done: 5, agents_started: 9, agents: [...running().agents!, agent(6, "Verify", { running: true })] })]);
    expect(text("[data-workflow-counts]")).toBe("5/9+");
    expect(q("[data-rolling], [data-fading]")).toBeNull();
    expect(q('[data-agent="a6"]')!.style.animation).toBe("");
    expect(frames.size).toBe(0);
  });
});

describe("in the chat's flow", () => {
  const exchange = (): Item[] => [
    item({ role: "user", kind: "text", text: "audit it" }, 0),
    item({ role: "assistant", kind: "tool_use", tool_name: "Grep", tool_use_id: "g1", tool_input: { pattern: "x" } }, 1),
    item({ role: "user", kind: "tool_result", tool_use_id: "g1", text: "ok" }, 2),
    item({ role: "assistant", kind: "tool_use", tool_name: "Workflow", tool_use_id: "call-wf", tool_input: { name: "parity-audit" } }, 5),
    item({ role: "user", kind: "tool_result", tool_use_id: "call-wf", text: "Workflow launched in background" }, 5),
    item({ role: "assistant", kind: "text", text: "Started the audit in the background." }, 6),
  ];
  const draw = (runs: Workflow[]) => {
    upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", workflows: runs } as Session);
    const ctx: ChatContextValue = { tasks: emptyTaskIndex(), subagents: [], scope: `flow${++scope}:`, workflowCalls: workflowCalls(runs) };
    const [seg] = segments(exchange());
    act(() =>
      root.render(
        <ChatContext.Provider value={ctx}>
          <SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={async () => {}} />
        </ChatContext.Provider>,
      ),
    );
  };

  it("keeps a running workflow inside the summary until manual expansion", () => {
    draw([running()]);
    expect(q("[data-workflow-call]")).toBeNull();
    act(() => q("[data-work-group] > button")!.click());
    const card = q('[data-workflow-call="running"]')!;
    expect(card.closest("[data-work-group]")).not.toBeNull();
    expect(q("[data-work-group]")).not.toBeNull(); // the Grep before it folded
  });

  it("keeps an ended workflow inspectable inside the folded execution summary", () => {
    draw([running({ running: false, status: "completed", ended_at: at(100) })]);
    expect(q("[data-workflow-call]")).toBeNull();
    act(() => q("[data-work-group] > button")!.click());
    const card = q('[data-workflow-call="completed"]')!;
    expect(card.closest("[data-work-group]")).not.toBeNull();
    expect(card.querySelector("button[aria-expanded]")!.getAttribute("aria-expanded")).toBe("false"); // no dialog here: in place, closed
    expect(card.querySelector("[data-task-status]")!.textContent).toBe("已完成");
    expect(q("[data-work-group]")!.textContent).toContain("已工作"); // the Grep before it folded
  });

  it("ends in place: the same card turns its status word, nothing leaves the chat", () => {
    draw([running()]);
    expect(q("[data-workflow-call]")).toBeNull();
    act(() => q("[data-work-group] > button")!.click());
    const card = q("[data-workflow-call]")!;
    push([running({ running: false, status: "completed", ended_at: at(100), agents_running: 0, agents_done: 5 })]);
    expect(q("[data-workflow-call]")).toBe(card); // not unmounted and drawn again elsewhere
    expect(card.dataset.workflowCall).toBe("completed");
    expect(card.querySelector("[data-task-status]")!.textContent).toBe("已完成");
    expect(card.querySelector("button[aria-expanded]")!.getAttribute("aria-expanded")).toBe("false");
  });

  const foldedExchange = (): Item[] => [
    item({ role: "user", kind: "text", text: "audit it" }, 0),
    { id: "fold:x#0", seq: 3, ts: at(1), role: "assistant", kind: "fold", fold: { items: 4, tools: 2, thoughts: 0, until: at(5), through_seq: 6, through_id: "x" } } as Item,
    item({ role: "assistant", kind: "text", text: "Started the audit in the background." }, 6),
  ];
  const drawFolded = (runs: Workflow[], expand: (f: Item) => Promise<void>, seg = segments(foldedExchange())[0]) => {
    upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", workflows: runs } as Session);
    const ctx: ChatContextValue = { tasks: emptyTaskIndex(), subagents: [], scope: `fold${++scope}:`, workflowCalls: runs.length ? workflowCalls(runs) : NO_WORKFLOW_CALLS };
    act(() =>
      root.render(
        <ChatContext.Provider value={ctx}>
          <SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={expand} />
        </ChatContext.Provider>,
      ),
    );
    return seg;
  };

  const openSummary = async () => { await act(async () => q("[data-work-group] > button")!.click()); };
  it("fetches folded workflow calls only when the reader expands the summary, live or ended", async () => {
    const expand = vi.fn(async (_f: Item) => {});
    const seg = drawFolded([running()], expand);
    expect(expand).not.toHaveBeenCalled();
    await openSummary();
    expect(expand).toHaveBeenCalledTimes(1);
    expect(expand.mock.calls[0][0].id).toBe("fold:x#0");
    drawFolded([running()], expand, seg);
    expect(expand).toHaveBeenCalledTimes(1); // redrawing a closed summary does not fetch
    act(() => root.render(<></>));
    const ended = vi.fn(async (_f: Item) => {});
    drawFolded([running({ running: false, status: "completed", ended_at: at(100) })], ended);
    expect(ended).not.toHaveBeenCalled();
    await openSummary();
    expect(ended).toHaveBeenCalledTimes(1);
    act(() => root.render(<></>));
    const none = vi.fn(async (_f: Item) => {});
    drawFolded([], none);
    expect(none).not.toHaveBeenCalled();
    await openSummary();
    expect(none).toHaveBeenCalledTimes(1); // even a workflow no longer listed can be inspected
  });

  it("fetches reloaded history on manual open and supports an explicit retry after failure", async () => {
    const expand = vi.fn(async (_f: Item) => {});
    drawFolded([running()], expand);
    await openSummary();
    act(() => root.render(<></>));
    drawFolded([running()], expand);
    expect(expand).toHaveBeenCalledTimes(1);
    await openSummary();
    expect(expand).toHaveBeenCalledTimes(2);
    act(() => root.render(<></>));
    const flaky = vi.fn(async (_f: Item) => {}).mockRejectedValueOnce(new Error("offline"));
    drawFolded([running()], flaky);
    await openSummary();
    expect(flaky).toHaveBeenCalledTimes(1);
    const retry = q("[data-work-group] [role=status] button");
    expect(retry).not.toBeNull();
    await act(async () => retry!.click());
    expect(flaky).toHaveBeenCalledTimes(2);
  });

});

describe("the running bar while a workflow keeps the agent working", () => {
  it("says the workflow, its phase and count, timed from the run's start", () => {
    const run = running();
    act(() => root.render(<RunningStatusView live={null} working startedAt={at(0)} background={{ run, more: 0, since: run.started_at, guessed: false }} clock={false} />));
    expect(text("[data-running-verb]")).toBe("后台 workflow · Verify · 4/5+");
    expect(q("[data-running-background]")!.dataset.runningBackground).toBe("workflow");
    expect(text("[data-running-elapsed]")).toBe("1 分 55 秒"); // from the run's start (5 s in), not the turn's
    expect(host.textContent).not.toContain("运行中");
  });

  it("names the other runs in words, and keeps the count and them whole while the words before give way", () => {
    const run = running();
    act(() => root.render(<RunningStatusView live={null} working background={{ run, more: 1, since: run.started_at, guessed: false }} />));
    expect(text("[data-running-verb]")).toBe("后台 workflow · Verify · 4/5+ · 还有 1 个");
    expect(q("[data-running-counts]")!.className).toContain("shrink-0");
    expect(q("[data-running-more]")!.className).toContain("shrink-0");
  });

  it("times a run that does not say when it started from the turn, not from when the bar appeared", () => {
    act(() => root.render(<RunningStatusView live={null} working startedAt={at(60)} background={{ run: running({ started_at: undefined }), more: 0, guessed: false }} />));
    expect(text("[data-running-elapsed]")).toBe("1 分钟"); // the turn began a minute before now
  });

  it("an older hub's guess yields to a turn of the agent's own: its status line, its subagents, or its work line's clock", () => {
    const run = running();
    const bg = { run, more: 0, since: run.started_at, guessed: true };
    act(() => root.render(<RunningStatusView live={{ verb: "Frosting" }} working background={bg} />));
    expect(text("[data-running-verb]")).toBe("Frosting…");
    // subagents it launched in the foreground and still waits on (#5602): the bar names them
    act(() => root.render(<RunningStatusView live={null} working startedAt={at(0)} background={bg} agents={{ count: 2, doing: ["Reading a.go"] }} />));
    expect(text("[data-running-verb]")).toBe("2 个子 Agent 在工作 — Reading a.go…");
    expect(q("[data-running-background]")).toBeNull();
    // the chat's "Working…" ticks: the bar is the turn's, without a second clock
    act(() => root.render(<RunningStatusView live={null} working startedAt={at(0)} background={bg} clock={false} />));
    expect(text("[data-running-verb]")).toBe("运行中");
    expect(q("[data-running-elapsed]")).toBeNull();
    // nothing of its own shows: the workflow, with its clock
    act(() => root.render(<RunningStatusView live={null} working startedAt={at(0)} background={bg} />));
    expect(text("[data-running-verb]")).toBe("后台 workflow · Verify · 4/5+");
    expect(text("[data-running-elapsed]")).toBe("1 分 55 秒");
  });
});
