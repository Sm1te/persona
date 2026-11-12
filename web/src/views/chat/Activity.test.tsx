// @vitest-environment jsdom
// The chat's Background dialog opened from a workflow run's card (#5030, after cindy's background tasks panel): the
// card opens the dialog brought to its run, which is lit a moment; a chat with no dialog that lists the run (no header,
// a run older than the dialog keeps) has the card open in place instead. The dialog's agent rows open nothing.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  // reduced motion: the dialog closes at once
  window.matchMedia = ((query: string) => ({
    matches: query.includes("reduce"),
    media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { canOpenBackgroundDialog } from "../../lib/background-dialog";
import { reset, upsertSession, useStore } from "../../lib/store";
import type { Session, Workflow } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";
import { ActivityChips } from "./Activity";
import { WorkflowCallCard } from "./WorkflowCard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const T0 = Date.parse("2026-11-12T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();

const run = (task: string, call: string, over: Partial<Workflow> = {}): Workflow => ({
  task_id: task,
  name: `run-${task}`,
  status: "running",
  running: true,
  tool_use_id: call,
  phases: ["Scan"],
  phase: "Scan",
  agents_started: 2,
  agents_done: 1,
  agents_running: 1,
  started_at: at(-60),
  agents: [
    { id: `${task}-a1`, session_id: `s@${task}-a1`, label: "scan:1", phase: "Scan", running: false, status: "done", started_at: at(-50), ended_at: at(-20) },
    { id: `${task}-a2`, session_id: `s@${task}-a2`, label: "scan:2", phase: "Scan", running: true, status: "running", started_at: at(-40) },
  ],
  ...over,
});

const step = (id: string): ToolStep => ({
  kind: "tool",
  id: `${id}#0`,
  use: { id: `${id}#0`, seq: 1, ts: at(-60), role: "assistant", kind: "tool_use", tool_name: "Workflow", tool_use_id: id, tool_input: {} },
  result: { id: `${id}-r#0`, seq: 2, ts: at(-60), role: "user", kind: "tool_result", tool_use_id: id, text: "Workflow launched in background" },
  images: [],
});

/** A chat: its header's chips (unless it has none, as the assistant's chat) and the cards of these calls. */
function Chat({ header, calls }: { header: boolean; calls: string[] }) {
  const session = useStore((s) => (s.sessions.m ?? []).find((x) => x.session_id === "s"));
  return (
    <>
      {header && session && <ActivityChips session={session} onStopLoop={() => {}} />}
      {calls.map((c) => (
        <WorkflowCallCard key={c} step={step(c)} mid="m" sid="s" scope="chat:" />
      ))}
    </>
  );
}

let host: HTMLDivElement;
let root: Root;
const animate = vi.fn();
const scrollIntoView = vi.fn();

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(T0);
  animate.mockClear();
  scrollIntoView.mockClear();
  HTMLElement.prototype.animate = animate as unknown as HTMLElement["animate"];
  HTMLElement.prototype.scrollIntoView = scrollIntoView;
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.useRealTimers();
  delete (HTMLElement.prototype as Partial<HTMLElement>).animate;
  delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView;
});

function draw(runs: Workflow[], { header = true, calls = runs.map((w) => w.tool_use_id!) }: { header?: boolean; calls?: string[] } = {}) {
  act(() => upsertSession({ machine_id: "m", session_id: "s", status: "working", state: "working", workflows: runs } as Session));
  act(() => root.render(<Chat header={header} calls={calls} />));
}
const card = (call: string) => host.querySelector<HTMLElement>(`[data-workflow-call][data-tool-use="${call}"]`)!;
const head = (call: string) => card(call).querySelector<HTMLElement>("button")!;
const dialog = () => document.querySelector<HTMLElement>("[role=dialog]");
const close = () => act(() => document.querySelector<HTMLElement>('[role=dialog] button[aria-label="关闭"]')!.click());
/** The dialog cards lit, by run. */
const lit = () => animate.mock.contexts.map((el) => (el as HTMLElement).dataset.workflowTask);

describe("a workflow card opening the chat's Background dialog", () => {
  it("opens the dialog brought to its run, lit a moment", () => {
    draw([run("w1", "call-1"), run("w2", "call-2")]);
    expect(card("call-2").dataset.workflowOpens).toBe("dialog");
    expect(head("call-2").getAttribute("aria-label")).toBe("在后台任务中打开 run-w2");
    expect(dialog()).toBeNull();
    act(() => head("call-2").click());
    expect(dialog()).not.toBeNull();
    expect(dialog()!.querySelectorAll("[data-workflow-task]")).toHaveLength(2);
    expect(lit()).toEqual(["w2"]);
    const [frames, timing] = animate.mock.calls[0];
    expect(frames[0]).toEqual({ backgroundColor: "var(--accent-tint)" });
    expect(timing).toMatchObject({ duration: 1800 });
    expect(scrollIntoView.mock.contexts.map((el) => (el as HTMLElement).dataset.workflowTask)).toEqual(["w2"]);
  });

  it("lights the run each time a card asks, and nothing when the chip opens it", () => {
    draw([run("w1", "call-1")]);
    act(() => head("call-1").click());
    close();
    expect(dialog()).toBeNull();
    act(() => head("call-1").click());
    expect(lit()).toEqual(["w1", "w1"]);
    close();
    act(() => host.querySelector<HTMLElement>("[data-background-chip]")!.closest("button")!.click());
    expect(dialog()).not.toBeNull();
    expect(lit()).toEqual(["w1", "w1"]);
  });

  it("lists the dialog's agents as rows that open nothing", () => {
    draw([run("w1", "call-1")]);
    act(() => head("call-1").click());
    const rows = [...dialog()!.querySelectorAll<HTMLElement>("[data-agent]")];
    expect(rows.map((r) => r.dataset.agent)).toEqual(["w1-a1", "w1-a2"]);
    for (const r of rows) expect(r.querySelector("button, [role=button], a")).toBeNull();
    act(() => rows[1].click());
    expect(dialog()).not.toBeNull(); // nothing opened over the chat, the dialog stays
  });

  it("opens in place in a chat with no header: nothing there lists the run", () => {
    draw([run("w1", "call-1")], { header: false });
    expect(canOpenBackgroundDialog("m", "s", "w1")).toBe(false);
    expect(card("call-1").dataset.workflowOpens).toBe("inline");
    act(() => head("call-1").click());
    expect(dialog()).toBeNull();
    expect(head("call-1").getAttribute("aria-expanded")).toBe("true");
    expect(card("call-1").querySelector("[data-workflow-phases]")).not.toBeNull();
  });

  it("opens in place for a run the dialog no longer keeps (ended over a day ago)", () => {
    const old = run("w0", "call-0", { running: false, status: "completed", started_at: at(-3 * 86_400), ended_at: at(-2 * 86_400), agents_running: 0, agents_done: 2 });
    draw([old, run("w1", "call-1")]);
    expect(card("call-0").dataset.workflowOpens).toBe("inline");
    expect(card("call-1").dataset.workflowOpens).toBe("dialog");
    act(() => head("call-0").click());
    expect(dialog()).toBeNull();
    // with nothing else to show the header has no chip, and no dialog
    draw([old]);
    expect(host.querySelector("[data-background-chip]")).toBeNull();
    expect(canOpenBackgroundDialog("m", "s")).toBe(false);
  });

  it("stops listening once the header is gone", () => {
    draw([run("w1", "call-1")]);
    expect(canOpenBackgroundDialog("m", "s", "w1")).toBe(true);
    act(() => root.render(<Chat header={false} calls={["call-1"]} />));
    expect(canOpenBackgroundDialog("m", "s", "w1")).toBe(false);
    expect(card("call-1").dataset.workflowOpens).toBe("inline");
  });
});
