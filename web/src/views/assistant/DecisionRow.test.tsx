// @vitest-environment jsdom
// A decision in the Needs you tray is answered on its row (#3804): the question, the background, what each option
// leads to, the options as buttons with the recommended one first, what happens by default, "Other…", and the answers
// that are no option in the ⋯ menu. An ask without options keeps the row it had.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { getFeedState, loadFeed, type FeedItem, type TaskPlan } from "../../lib/feed";
import { clock } from "../../lib/format";
import { closePanel, getPanel } from "../../lib/panel";
import { decisionLaterKey } from "../../lib/assistant-entries";
import { putTodo, refresh, refreshTodos, reset } from "../../lib/store";
import type { Todo, TodoAsk } from "../../lib/todos";
import { AssistantActionBar } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const at = "2026-11-10T08:00:00Z";
const deadline = "2026-11-11T06:00:00Z";
const conversation = "decision-demo";
const plan: TaskPlan = { id: "decision-plan", conversation, state: "started", created_at: at, tasks: [{ title: "Demo release", prompt: "Demo", cwd: "/demo", machine: "demo", todo_id: "decision-task" }] };
const feed: FeedItem[] = [
  { id: "decision-plan-event", conversation, at, kind: "plan", ref: plan.id },
  { id: "decision-task-event", conversation, at, kind: "task", ref: "decision-task" },
];
const decision: TodoAsk = {
  outcome: "decide",
  verdict: "Needs your decision: release to everyone now, or to 10% first",
  recommend: "10% first",
  asked_at: at,
  question: "Release to everyone now, or to 10% first?",
  why_owner: "priority",
  options: [
    { label: "10% first", leads_to: "one more day, a bad build reaches few people" },
    { label: "Everyone now", leads_to: "ships today, a bad build reaches all" },
    { label: "Hold the release", leads_to: "nothing ships this week" },
  ],
  cost_of_waiting: "the release slips a day",
  background: "From the main conversation, Nov 10 · merged and checked in staging",
  deadline,
  default: "taken",
  responses: ["decide_yourself", "not_what_i_asked", "explain_first"],
};

let ask: TodoAsk | undefined;
let todo: Todo;
let host: HTMLDivElement;
let root: Root;
let posts: { path: string; body: unknown }[];
let status: number;

async function mount() {
  todo = { id: "decision-task", title: "Demo release", origin: conversation, plan_id: plan.id, created_at: at, updated_at: at,
    agents: [{ machine_id: "demo", session_id: "worker", linked_at: at }], supervision: { supervisor: conversation, state: "needs_user", reviews: [{ at, verdict: "needs_user", note: "The example checks passed; release approval is still needed." }] }, decision_review: { review_at: at }, ask };
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const path = String(url);
    if (init?.method === "POST") {
      posts.push({ path, body: JSON.parse(String(init.body)) });
      if (status !== 200) return new Response(JSON.stringify({ error: "The computer is offline" }), { status, headers: { "Content-Type": "application/json" } });
      if (path.endsWith("/decision-dismiss")) {
        todo = { ...todo, decision_review: { review_at: at, dismissal_id: "dismiss-demo" } };
        return new Response(JSON.stringify(todo.decision_review), { headers: { "Content-Type": "application/json" } });
      }
      if (path.endsWith("/decision-undo-dismiss")) {
        todo = { ...todo, decision_review: { review_at: at } };
        return new Response(JSON.stringify(todo.decision_review), { headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ ok: true }), { headers: { "Content-Type": "application/json" } });
    }
    const body = path.includes("/feed") ? { items: feed, plans: { [plan.id]: plan }, task_cards: [{ id: `plan:${plan.id}:0`, plan_id: plan.id, index: 0, todo_id: todo.id, at, updated_at: at, state: "accepted" }] }
      : path.includes("/todos") ? { todos: [todo] }
      : path.includes("/sessions") ? { sessions: [{ machine_id: "demo", session_id: "worker", title: "Demo agent", state: "idle", status: "idle", updated_at: at }] } : {};
    return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
  }));
  await act(async () => {
    await refresh();
    await loadFeed(conversation);
    await refreshTodos();
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<AssistantActionBar conversation={conversation} />));
  act(() => host.querySelector<HTMLButtonElement>('[data-tray-entry="needs"]')!.click());
  await act(async () => void (await import("./TaskDecision"))); // the row's code arrives with the first decision
}

beforeEach(() => {
  ask = decision;
  posts = [];
  status = 200;
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  closePanel();
  reset();
  delete getFeedState().plans[plan.id];
  delete getFeedState().feeds[conversation];
  delete getFeedState().taskCards[conversation];
  delete getFeedState().later[decisionLaterKey(todo)];
  vi.unstubAllGlobals();
});

const row = () => host.querySelector<HTMLElement>('[data-needs-row="task:decision-task"]')!;
const optionButtons = () => [...row().querySelectorAll<HTMLElement>("[data-decision-option]")].map((s) => s.closest("button")!);
const menuRows = () => [...document.querySelectorAll<HTMLElement>("[role=menuitem]")];
const openMenu = () => act(() => row().querySelector<HTMLButtonElement>(`button[aria-label="${t("assistant-decisionrow.more")}"]`)!.click());
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

describe("a decision in the Needs you tray", () => {
  it("shows the question, the background, what each option leads to and the default, with the recommended option first", async () => {
    await mount();
    expect(row().hasAttribute("data-decision-row")).toBe(true);
    expect(row().querySelector("[data-decision-question]")!.textContent).toBe(decision.question);
    expect(row().querySelector("[data-decision-background]")!.textContent).toBe(decision.background);
    const leads = [...row().querySelectorAll("[data-decision-leads]")].map((li) => li.textContent);
    expect(leads[0]).toBe("10% first" + t("chat-interactions.recommended") + t("assistant-decisionrow.leads_to", { leads: "one more day, a bad build reaches few people" }));
    expect(leads[1]).toBe(t("assistant-decisionrow.leads", { option: "Everyone now", leads: "ships today, a bad build reaches all" }));
    expect(row().querySelector('[data-decision-default="taken"]')!.textContent).toBe(t("assistant-decisionrow.default_at", { time: clock(deadline) }));
    expect(row().querySelector("[data-decision-cost]")!.textContent).toBe(t("assistant-decisionrow.cost", { cost: "the release slips a day" }));
    const buttons = optionButtons();
    expect(buttons.map((b) => b.textContent)).toEqual(["10% first", "Everyone now", "Hold the release"]);
    expect(buttons[0].className).toContain("bg-ink"); // the one solid button
    expect(buttons[1].className).not.toContain("bg-ink");
    for (const b of row().querySelectorAll("button")) expect(b.className).not.toMatch(/whitespace-normal|flex-wrap/);
    expect(row().textContent).toContain(t("assistant-decisionrow.another"));
    // the fixed answers are in the menu, not buttons on the row
    expect(row().textContent).not.toContain(t("assistant-decisionrow.decide_yourself"));
    openMenu();
    expect(menuRows().map((r) => r.textContent)).toEqual([
      t("assistant-decisionrow.decide_yourself") + t("assistant-decisionrow.decide_yourself_detail"),
      t("assistant-decisionrow.not_what_i_asked") + t("assistant-decisionrow.not_what_i_asked_detail"),
      t("assistant-decisionrow.explain_first") + t("assistant-decisionrow.explain_first_detail"),
    ]);
  });

  it("says an irreversible decision is never taken by default", async () => {
    ask = { ...decision, why_owner: "irreversible", default: "never", deadline: undefined };
    await mount();
    expect(row().querySelector('[data-decision-default="never"]')!.textContent).toBe(t("assistant-decisionrow.default_never"));
  });

  it("takes two presses for an option of an irreversible decision", async () => {
    ask = { ...decision, why_owner: "irreversible", default: "never", deadline: undefined };
    await mount();
    const [first] = optionButtons();
    act(() => first.click());
    await flush();
    expect(posts).toEqual([]);
    expect(row().querySelector("[data-decision-armed]")!.textContent).toBe(t("assistant-decisionrow.confirm", { option: "10% first" }));
    act(() => optionButtons()[0].click());
    await flush();
    expect(posts).toEqual([{ path: expect.stringContaining("/todos/decision-task/accept"), body: { asked_at: at, option: "10% first" } }]);
  });

  it("posts the option chosen with the round, once, and says it was answered", async () => {
    await mount();
    const [, second] = optionButtons();
    act(() => second.click());
    act(() => second.click());
    await flush();
    expect(posts).toEqual([{ path: expect.stringContaining("/todos/decision-task/accept"), body: { asked_at: at, option: "Everyone now" } }]);
    expect(row().querySelector("[data-decision-done]")!.textContent).toBe(t("assistant-decisionrow.chosen", { option: "Everyone now" }));
    expect(optionButtons()).toEqual([]);
  });

  it("posts a fixed response from the menu", async () => {
    await mount();
    openMenu();
    act(() => menuRows()[1].click());
    await flush();
    expect(posts).toEqual([{ path: expect.stringContaining("/todos/decision-task/accept"), body: { asked_at: at, response: "not_what_i_asked" } }]);
    expect(row().querySelector("[data-decision-done]")!.textContent).toBe(t("assistant-decisionrow.responded"));
  });

  it("keeps the buttons when the answer did not go, and says a round already answered is handled", async () => {
    status = 502;
    await mount();
    act(() => optionButtons()[0].click());
    await flush();
    expect(posts).toHaveLength(1);
    expect(row().querySelector("[data-decision-done]")).toBeNull();
    expect(optionButtons()[0].hasAttribute("disabled")).toBe(false);
    status = 409;
    act(() => optionButtons()[0].click());
    await flush();
    expect(row().querySelector("[data-decision-done]")!.textContent).toBe(t("assistant-brief.handled"));
  });

  it("opens the task from Other…", async () => {
    await mount();
    const other = [...row().querySelectorAll("button")].find((b) => b.textContent === t("assistant-decisionrow.another"))!;
    act(() => other.click());
    expect(getPanel()).toMatchObject({ kind: "task", todo: "decision-task" });
    expect(posts).toEqual([]);
  });

  it("says it went back to Persona while a decision answered with a fixed response still waits", async () => {
    ask = { ...decision, response: "explain_first", default: "never", deadline: undefined };
    await mount();
    const row = host.querySelector("[data-decision-row]")!;
    expect(row.querySelector("[data-decision-response]")?.textContent).toBe(t("assistant-decisionrow.responded"));
    expect(row.querySelector("[data-decision-option]")).not.toBeNull(); // the options can still be chosen
  });

  it("defers only this decision round without writing or completing the task", async () => {
    await mount();
    const actions = row().querySelector("[data-decision-actions]")!;
    expect([...actions.querySelectorAll("button")].map((b) => b.textContent)).toEqual([t("assistant-tasks.later"), t("assistant-tasks.dismiss_draft")]);
    act(() => actions.querySelector<HTMLButtonElement>("button")!.click());
    expect(row()).toBeNull();
    expect(posts).toEqual([]);
    const next = "2026-11-11T08:00:00Z";
    await act(async () => putTodo({ ...todo, supervision: { ...todo.supervision!, reviews: [{ at: next, verdict: "needs_user" }] }, decision_review: { review_at: next }, ask: { ...decision, asked_at: next } }));
    expect(row()).not.toBeNull();
  });

  it("dismisses the exact review, never accepts an option or completes the task", async () => {
    await mount();
    const buttons = row().querySelector("[data-decision-actions]")!.querySelectorAll<HTMLButtonElement>("button");
    await act(async () => { buttons[1].click(); optionButtons()[0].click(); buttons[0].click(); });
    expect(posts).toEqual([{ path: expect.stringContaining("/todos/decision-task/decision-dismiss"), body: { review_at: at } }]);
    expect(row()).toBeNull();
    expect(todo.done_at).toBeUndefined();
    expect(todo.ask).toEqual(decision);
    expect(todo.supervision?.state).toBe("needs_user");
  });

  it("keeps dismiss and later on a legacy review without inventing acceptance options", async () => {
    ask = undefined;
    await mount();
    expect(row().querySelector("[data-decision-legacy]")?.textContent).toContain("release approval is still needed");
    expect(row().querySelector("[data-decision-option]")).toBeNull();
    expect(row().textContent).toContain(t("assistant-decisionrow.missing_options"));
    const buttons = row().querySelector("[data-decision-actions]")!.querySelectorAll<HTMLButtonElement>("button");
    await act(async () => buttons[1].click());
    expect(posts).toEqual([{ path: expect.stringContaining("/decision-dismiss"), body: { review_at: at } }]);
    expect(row()).toBeNull();
  });

  it("does not let a stale dismissal projection hide a newly pushed review", async () => {
    await mount();
    await act(async () => row().querySelector("[data-decision-actions]")!.querySelectorAll<HTMLButtonElement>("button")[1].click());
    const next = "2026-11-11T09:00:00Z";
    await act(async () => putTodo({ ...todo, supervision: { ...todo.supervision!, reviews: [{ at: next, verdict: "needs_user" }] }, ask: { ...decision, asked_at: next } }));
    expect(row()).not.toBeNull();
    expect(row().querySelector("[data-decision-option]")).not.toBeNull();
  });

  it("allows retry after dismiss fails and blocks dismissal after option acceptance starts", async () => {
    status = 503;
    await mount();
    const dismiss = row().querySelector("[data-decision-actions]")!.querySelectorAll<HTMLButtonElement>("button")[1];
    await act(async () => dismiss.click());
    expect(row()).not.toBeNull();
    expect(dismiss.disabled).toBe(false);
    status = 200;
    await act(async () => { optionButtons()[0].click(); dismiss.click(); });
    expect(posts.map((p) => p.path.split("/").at(-1))).toEqual(["decision-dismiss", "accept"]);
  });

  it("leaves an ask without options as it was: one Take button, no decision row", async () => {
    ask = { outcome: "decide", verdict: decision.verdict, recommend: "10% first", asked_at: at };
    await mount();
    expect(row().hasAttribute("data-decision-row")).toBe(false);
    expect(row().querySelector('[data-brief-row="accept"]')).not.toBeNull();
    expect(row().textContent).toContain(t("assistant-brief.take"));
    expect(row().querySelector("[data-decision-option]")).toBeNull();
  });
});
