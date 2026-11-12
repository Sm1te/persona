// @vitest-environment jsdom
// The plan as cindy draws it (#5156): the card opens and closes in place (open first, a bar when closed), the pill above
// the composer says which step of how many, its flyout opens on hover and stays on a click (a second click, a press
// outside or Esc closes it), and Close Plan hides the pill until the plan changes.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Item } from "../../lib/types";
import type { Segment, UserMessage } from "../../lib/turns";
import type { Todo } from "../../lib/work-groups";
import { emptyTaskIndex } from "../../lib/tasks";
import { ChatContext, type ChatContextValue } from "./chat-context";
import { forgetExpanded } from "./expanded";
import { planCardStep } from "../../lib/work-groups";
import { useEscapeClose } from "../../ui/overlay";
import { latestPlan, usePlanClosed } from "./latest-plan";
import { PlanCard, PlanPill } from "./PlanCard";
import { SegmentView } from "./SegmentView";

const todos: Todo[] = [
  { content: "读代码", status: "completed" },
  { content: "改实现", status: "completed" },
  { content: "补测试", status: "in_progress" },
  { content: "跑一遍集成测试", status: "pending" },
  { content: "更新文档", status: "pending" },
];
const allDone = todos.map((todo) => ({ ...todo, status: "completed" as const }));

let host: HTMLDivElement;
let root: Root;
const render = (ui: React.ReactNode) => act(() => root.render(ui));

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement("div");
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

const head = () => host.querySelector<HTMLButtonElement>("[data-plan-card] button")!;
const pill = () => host.querySelector<HTMLButtonElement>("[data-plan-pill]");
const flyout = () => document.querySelector<HTMLElement>("[data-plan-flyout]");
const closeButton = () => document.querySelector<HTMLButtonElement>("[data-plan-close]");
const click = (el: Element) => act(() => el.dispatchEvent(new MouseEvent("click", { bubbles: true })));
// React's onMouseEnter / onMouseLeave come from mouseover / mouseout
const enter = (el: Element) => act(() => el.dispatchEvent(new MouseEvent("mouseover", { bubbles: true, relatedTarget: document.body })));
const leave = (el: Element) => act(() => el.dispatchEvent(new MouseEvent("mouseout", { bubbles: true, relatedTarget: document.body })));
const press = (el: Element) => act(() => el.dispatchEvent(new Event("pointerdown", { bubbles: true })));
const key = (k: string, at: Element = document.body) => act(() => at.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true })));
// a Collapse finishes moving on its transition's end, or 450 ms later without one (jsdom)
const settle = () => act(() => new Promise((r) => setTimeout(r, 500)));
const frame = () => act(() => new Promise((r) => requestAnimationFrame(() => r(undefined))));
/** The Collapse around `sel`: open is a 1fr row; closed, 0fr and inert. */
const fold = (sel: string) => host.querySelector(sel)?.closest<HTMLElement>(".grid");

describe("the plan card", () => {
  it("starts open: the head, then every step with its mark", async () => {
    await render(<PlanCard id="p" todos={todos} />);
    expect(head().getAttribute("aria-expanded")).toBe("true");
    // one chevron, turned down while open (as every fold in the chat)
    expect(head().querySelector("svg")!.style.transform).toBe("rotate(90deg)");
    expect(host.querySelector("[data-plan-count]")!.textContent).toBe("2/5");
    expect(host.querySelector("[data-plan-head]")!.textContent).toBe("补测试"); // the step in progress
    const rows = [...host.querySelectorAll("[data-plan-list] [data-todo]")];
    expect(rows.map((r) => [r.getAttribute("data-todo"), r.textContent])).toEqual(todos.map((todo) => [todo.status, todo.content]));
    // done ones dimmed, the one in progress medium, pending ones in ink
    expect(rows[0].querySelector("span")!.className).toContain("text-tool-ink");
    expect(rows[2].lastElementChild!.className).toContain("font-medium");
    expect(rows[3].lastElementChild!.className).toContain("text-ink");
    expect(host.querySelector("[data-plan-progress]")).toBeNull();
  });

  it("closes in place (Collapse) to its head and a bar of how many are done, and opens again", async () => {
    await render(<PlanCard id="p" todos={todos} />);
    expect(fold("[data-plan-list]")!.style.gridTemplateRows).toBe("1fr");
    await click(head());
    expect(head().getAttribute("aria-expanded")).toBe("false");
    expect(head().querySelector("svg")!.style.transform).toBe("none");
    // the list folds away with the height, then goes
    expect(fold("[data-plan-list]")!.style.gridTemplateRows).toBe("0fr");
    await frame();
    await settle();
    expect(host.querySelector("[data-plan-list]")).toBeNull();
    const bar = host.querySelector<HTMLElement>("[data-plan-progress] > div")!;
    expect(bar.parentElement!.className).toContain("h-0.5");
    expect(bar.style.width).toBe("40%");
    expect(fold("[data-plan-progress]")!.style.gridTemplateRows).toBe("1fr");
    await click(head());
    expect(head().getAttribute("aria-expanded")).toBe("true");
    await frame();
    await settle();
    expect(host.querySelector("[data-plan-progress]")).toBeNull();
    expect(fold("[data-plan-list]")!.style.gridTemplateRows).toBe("1fr");
  });

  it("names the last step when none is in progress", async () => {
    await render(<PlanCard id="p" todos={allDone} />);
    expect(host.querySelector("[data-plan-count]")!.textContent).toBe("5/5");
    expect(host.querySelector("[data-plan-head]")!.textContent).toBe("更新文档");
  });

  it("turns the step in progress only while the turn runs", async () => {
    await render(<PlanCard id="p" todos={todos} live />);
    const arc = host.querySelector<HTMLElement>("[data-plan-arc]")!;
    expect(arc.dataset.planArc).toBe("turning");
    expect(arc.style.animation).toContain("spin-long");
    // it is not a busy indicator: reduced motion stops it (no busy-motion class)
    expect(arc.className).not.toContain("busy-motion");
    await render(<PlanCard id="p" todos={todos} />);
    const still = host.querySelector<HTMLElement>("[data-plan-arc]")!;
    expect(still.dataset.planArc).toBe("still");
    expect(still.style.animation).toBe("");
  });

  it("does not open the side panel or say 'Updated'", () => {
    const html = renderToStaticMarkup(<PlanCard id="p" todos={todos} />);
    expect(html).not.toContain("更新了待办");
    expect(html).not.toContain("侧栏");
  });
});

describe("the plan pill", () => {
  it("says which step of how many, with a ring of how far the current step is", async () => {
    await render(<PlanPill todos={todos} />);
    expect(pill()!.textContent).toBe("第 3 / 5 步");
    const ring = pill()!.querySelectorAll("[data-plan-ring] circle")[1];
    const c = 2 * Math.PI * 7;
    // step 3 of 5, not 2 done of 5
    expect(Number(ring.getAttribute("stroke-dashoffset"))).toBeCloseTo(c * (1 - 3 / 5));
  });

  it("opens its flyout on hover and closes it on leaving", async () => {
    await render(<PlanPill todos={todos} onClose={() => {}} />);
    expect(flyout()).toBeNull();
    expect(pill()!.getAttribute("aria-expanded")).toBe("false");
    await enter(pill()!);
    expect(flyout()!.dataset.planFlyout).toBe("hover");
    expect(pill()!.getAttribute("aria-expanded")).toBe("true");
    expect(pill()!.getAttribute("aria-controls")).toBe(flyout()!.id);
    // drawn on the body: what holds the pill scrolls and would clip it
    expect(host.contains(flyout())).toBe(false);
    const rows = [...flyout()!.querySelectorAll("[data-todo]")];
    expect(rows.map((r) => r.textContent)).toEqual(todos.map((todo) => todo.content));
    expect(rows[2].lastElementChild!.className).toContain("font-semibold");
    await leave(pill()!);
    expect(flyout()).toBeNull();
  });

  it("stays open once clicked, until a second click", async () => {
    await render(<PlanPill todos={todos} />);
    await enter(pill()!);
    await click(pill()!);
    expect(flyout()!.dataset.planFlyout).toBe("pinned");
    await leave(pill()!);
    expect(flyout()!.dataset.planFlyout).toBe("pinned");
    await click(pill()!);
    expect(flyout()).toBeNull();
  });

  it("pinned, closes on a press outside but not on one inside the flyout", async () => {
    await render(<PlanPill todos={todos} />);
    await click(pill()!);
    await press(flyout()!.querySelector("[data-todo]")!);
    expect(flyout()).not.toBeNull();
    await press(document.body);
    expect(flyout()).toBeNull();
  });

  it("pinned, it is a dialog (so the chat's Esc does not stop the turn) that Esc closes", async () => {
    await render(<PlanPill todos={todos} />);
    await enter(pill()!);
    // hovering it is no dialog: Esc is the chat's
    expect(document.querySelector("[role=dialog]")).toBeNull();
    await click(pill()!);
    const dialog = document.querySelector("[role=dialog]");
    expect(dialog).toBe(flyout());
    expect(dialog!.getAttribute("aria-labelledby")).toBe(pill()!.id);
    await key("Escape");
    expect(flyout()).toBeNull();
    expect(document.querySelector("[role=dialog]")).toBeNull();
  });

  it("Esc closes an overlay opened over the pinned flyout first", async () => {
    function Over({ open, onClose }: { open: boolean; onClose: () => void }) {
      useEscapeClose(open, onClose);
      return open ? <div data-over /> : null;
    }
    const closeOver = vi.fn();
    await render(<PlanPill todos={todos} />);
    await click(pill()!);
    await render(<><PlanPill todos={todos} /><Over open onClose={closeOver} /></>);
    await key("Escape");
    expect(closeOver).toHaveBeenCalledTimes(1);
    expect(flyout()!.dataset.planFlyout).toBe("pinned");
    await render(<><PlanPill todos={todos} /><Over open={false} onClose={closeOver} /></>);
    await key("Escape");
    expect(flyout()).toBeNull();
  });

  it("Tab goes from the pill to Close Plan while the flyout shows", async () => {
    await render(<PlanPill todos={todos} onClose={() => {}} />);
    await click(pill()!);
    pill()!.focus();
    await key("Tab", pill()!);
    expect(document.activeElement).toBe(closeButton());
    await key("Tab", closeButton()!);
    expect(document.activeElement).toBe(pill());
  });

  it("Close Plan is the flyout's ✕, labelled", async () => {
    const onClose = vi.fn();
    await render(<PlanPill todos={todos} onClose={onClose} />);
    await enter(pill()!);
    expect(closeButton()!.getAttribute("aria-label")).toBe("关闭计划");
    expect(closeButton()!.title).toBe("关闭计划");
    await click(closeButton()!);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe("Close Plan", () => {
  /** What ChatView does with it: the pill shows unless this plan was closed. */
  function Dock({ chat, plan }: { chat: string; plan: { id: string; todos: Todo[] } }) {
    const [closed, close] = usePlanClosed(chat, plan);
    return closed ? null : <PlanPill key={plan.id} todos={plan.todos} onClose={close} />;
  }

  it("hides the pill for this plan; a change to it or a new plan brings it back", async () => {
    const plan = { id: "tw#0", todos };
    await render(<Dock chat="m/a" plan={plan} />);
    await enter(pill()!);
    await click(closeButton()!);
    expect(pill()).toBeNull();
    expect(flyout()).toBeNull();
    // the same plan drawn again (a push that changed something else) stays closed
    await render(<Dock chat="m/a" plan={{ id: "tw#0", todos: todos.map((todo) => ({ ...todo })) }} />);
    expect(pill()).toBeNull();
    // the agent moved on a step: back
    const next = todos.map((todo, i) => (i === 2 ? { ...todo, status: "completed" as const } : i === 3 ? { ...todo, status: "in_progress" as const } : todo));
    await render(<Dock chat="m/a" plan={{ id: "tw#0", todos: next }} />);
    expect(pill()!.textContent).toBe("第 4 / 5 步");
    // closed again, then a new plan: back
    await enter(pill()!);
    await click(closeButton()!);
    expect(pill()).toBeNull();
    await render(<Dock chat="m/a" plan={{ id: "tw2#0", todos: next }} />);
    expect(pill()).not.toBeNull();
  });

  it("is kept per chat", async () => {
    await render(<Dock chat="m/b" plan={{ id: "tw#0", todos }} />);
    await enter(pill()!);
    await click(closeButton()!);
    expect(pill()).toBeNull();
    await render(<Dock chat="m/c" plan={{ id: "tw#0", todos }} />);
    expect(pill()).not.toBeNull();
    await render(<Dock chat="m/b" plan={{ id: "tw#0", todos }} />);
    expect(pill()).toBeNull();
  });
});

describe("an exchange's plan in its summary", () => {
  // cindy draws only a plan's latest list and drops the calls before it; super rule 2 keeps every tool call
  // inspectable, so those stay ordinary rows (#5156)
  const call = (n: number, name: string, input: Record<string, unknown>): Segment["steps"][number] => ({
    kind: "tool", id: `t${n}#0`,
    use: { id: `t${n}#0`, seq: n, role: "assistant", kind: "tool_use", tool_name: name, tool_use_id: `c-${n}`, tool_input: input } as Item,
    result: { id: `t${n}-r#0`, seq: n, role: "user", kind: "tool_result", tool_use_id: `c-${n}`, text: "ok" } as Item,
    images: [],
  });
  const answer: Segment["steps"][number] = { kind: "text", id: "a9#0", item: { id: "a9#0", seq: 9, role: "assistant", kind: "text", text: "做完了。" } as Item };

  it("the latest list of two or more items is the card; an earlier one and a list of one are action rows", async () => {
    forgetExpanded();
    const steps = [
      call(1, "TodoWrite", { todos: todos.slice(0, 2) }),
      call(2, "Bash", { command: "echo 1" }),
      call(3, "TodoWrite", { todos }),
      call(4, "TodoWrite", { todos: todos.slice(0, 1) }),
      answer,
    ];
    const seg: Segment = { id: "seg", steps, tools: 4, thoughts: 0, replies: 1, folds: [] };
    const ctx: ChatContextValue = { tasks: emptyTaskIndex(), subagents: [], scope: "plan:", lineStyle: "agents" };
    await render(<ChatContext.Provider value={ctx}><SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={async () => {}} /></ChatContext.Provider>);
    // folded: no card drawn
    expect(host.querySelector("[data-plan-card]")).toBeNull();
    await act(async () => {
      host.querySelector<HTMLButtonElement>("[data-work-group] > button")!.click();
      await vi.dynamicImportSettled();
    });
    const cards = [...host.querySelectorAll("[data-plan-card]")];
    expect(cards.map((c) => c.getAttribute("data-plan-id"))).toEqual(["t3#0"]);
    expect(cards[0].querySelectorAll("[data-todo]")).toHaveLength(5);
    const rows = [...host.querySelectorAll('[data-action-row="TodoWrite"]')];
    expect(rows.map((r) => r.getAttribute("data-tool-use"))).toEqual(["c-1", "c-4"]);
  });
});

describe("latestPlan", () => {
  const user = (id: string, extra: Partial<UserMessage> = {}): UserMessage => ({ id, text: "做", images: [], contexts: [], ...extra });
  const planSeg = (id: string, u?: UserMessage): Segment => ({
    id,
    user: u,
    steps: [{ kind: "tool", id: `${id}-tw#0`, use: { id: `${id}-tw#0`, seq: 1, role: "assistant", kind: "tool_use", tool_name: "TodoWrite", tool_input: { todos } } as Item, images: [] }],
    tools: 1,
    thoughts: 0,
    replies: 0,
    folds: [],
  });
  const plain = (id: string, u?: UserMessage, supervision?: boolean): Segment => ({ id, user: u, steps: [], tools: 0, thoughts: 0, replies: 0, folds: [], supervision });

  it("one card per exchange: its latest list of two or more items; a list of one draws none", () => {
    const tw = (id: string, list: Todo[], error = false): Segment["steps"][number] => ({
      kind: "tool", id, use: { id, seq: 1, role: "assistant", kind: "tool_use", tool_name: "TodoWrite", tool_input: { todos: list } } as Item,
      result: { id: `${id}-r`, seq: 2, role: "user", kind: "tool_result", is_error: error } as Item, images: [],
    });
    expect(planCardStep([tw("a", todos), tw("b", todos), tw("c", todos.slice(0, 1))])).toBe("b");
    expect(planCardStep([tw("a", todos), tw("b", todos, true)])).toBe("a"); // the tool refused it: the plan did not change
    expect(planCardStep([tw("a", todos.slice(0, 1))])).toBe("");
  });

  it("finds the newest plan in a fold the hub made too (#5602)", () => {
    const fold: Item = { id: "f#0", seq: 5, role: "assistant", kind: "fold", fold: { items: 6, tools: 3, thoughts: 0, through_seq: 10,
      tasks: [{ replace: "todo", entries: [{ id: "todo:0", subject: "读代码", status: "completed" }, { id: "todo:1", subject: "改实现", status: "in_progress" }], source_item_id: "tw#0", source_tool_use_id: "tw", source_seq: 7 }] } };
    const seg = { id: "s", steps: [{ kind: "fold" as const, id: fold.id, item: fold }], tools: 3, thoughts: 0, replies: 0, folds: [fold] };
    expect(latestPlan([seg])).toEqual({ id: "tw#0", todos: [{ content: "读代码", status: "completed" }, { content: "改实现", status: "in_progress" }], superseded: false });
  });

  it("is an earlier turn's once a message of the user's came after it (cindy's ownership boundary)", () => {
    expect(latestPlan([planSeg("a", user("u1"))])?.superseded).toBe(false);
    expect(latestPlan([planSeg("a", user("u1")), plain("b", user("u2"))])).toMatchObject({ id: "a-tw#0", superseded: true });
    // what Persona typed in, another session's message, a supervision round or a queued message start no turn of the user's
    expect(latestPlan([planSeg("a", user("u1")), plain("b", user("u2", { injected: { kind: "task_notice", line: "任务完成" } }))])?.superseded).toBe(false);
    expect(latestPlan([planSeg("a", user("u1")), plain("b", user("u2", { from: "reviewer" }))])?.superseded).toBe(false);
    expect(latestPlan([planSeg("a", user("u1")), plain("b", user("u2"), true)])?.superseded).toBe(false);
    expect(latestPlan([planSeg("a", user("u1")), plain("b", user("u2", { queued: true }))])?.superseded).toBe(false);
    // a newer plan in the later turn is that turn's own
    expect(latestPlan([planSeg("a", user("u1")), planSeg("b", user("u2"))])).toMatchObject({ id: "b-tw#0", superseded: false });
  });
});
