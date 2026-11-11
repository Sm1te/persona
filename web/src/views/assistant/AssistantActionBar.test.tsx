// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { getFeedState, loadFeed, type FeedItem, type TaskPlan } from "../../lib/feed";
import { getPanel } from "../../lib/panel";
import { putTodo, refresh, refreshTodos, reset, upsertSession } from "../../lib/store";
import type { Todo } from "../../lib/todos";
import type { Interaction, Session } from "../../lib/types";
import { AssistantActionBar, DOCK_CAPSULE, FeedLineView, TaskDock } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const at = "2026-11-10T08:00:00Z";
const conversation = "tray-demo";
const plan: TaskPlan = { id: "tray-plan", conversation, state: "started", created_at: at, tasks: [{ title: "Demo task", prompt: "Demo checks", cwd: "/demo", machine: "demo", todo_id: "tray-task" }] };
let todo: Todo;
let host: HTMLDivElement;
let root: Root;
let writes: string[];
/** More tasks, agents and cards a test adds to what the hub answers; `mainChat`: the main conversation's id. */
let moreTodos: Todo[];
let moreSessions: Session[];
let moreInteractions: Interaction[];
let mainChat: string | undefined;
const feed: FeedItem[] = [
  { id: "tray-plan-event", conversation, at, kind: "plan", ref: plan.id },
  { id: "tray-task-event", conversation, at, kind: "task", ref: "tray-task" },
  { id: "tray-review-event", conversation, at, kind: "review", ref: "tray-task", body: { verdict: "accepted" } },
  { id: "tray-receipt", conversation, at, kind: "receipt", ref: "tray-task", body: { state: "done", summary: "Demo result" } },
];

beforeEach(async () => {
  writes = [];
  moreTodos = [];
  moreSessions = [];
  moreInteractions = [];
  mainChat = undefined;
  todo = { id: "tray-task", title: "Demo task", origin: conversation, plan_id: plan.id, created_at: at, updated_at: at, pr_url: "https://github.com/example/demo/pull/42", agents: [{ machine_id: "demo", session_id: "worker", linked_at: at }], supervision: { supervisor: conversation, progress: { kind: "progress", step: 2, steps: 5, text: "Run the checks", at } } };
  vi.stubGlobal("fetch", vi.fn(async (url: string, init?: RequestInit) => {
    const path = String(url);
    if (init?.method && init.method !== "GET") writes.push(path);
    const body = path.includes("/feed") ? { items: feed, plans: { [plan.id]: plan }, task_cards: [{ id: `plan:${plan.id}:0`, plan_id: plan.id, index: 0, todo_id: todo.id, at, updated_at: at, state: "accepted" }] } : path.includes("/todos") ? { todos: [todo, ...moreTodos] } : path.includes("/sessions") ? { sessions: [{ machine_id: "demo", session_id: "worker", title: "Demo agent", state: "working", status: "working", updated_at: at }, ...moreSessions] } : path.includes("/interactions") ? { interactions: moreInteractions } : /\/assistant(\?|$)/.test(path) && mainChat ? { machine_id: "demo", main: mainChat, conversations: [] } : {};
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
  act(() => root.render(<StrictMode><AssistantActionBar conversation={conversation} />{feed.map((item) => <FeedLineView key={item.id} item={item} simplified />)}</StrictMode>));
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  history.replaceState(null, "", "/");
  reset();
  delete getFeedState().plans[plan.id];
  delete getFeedState().feeds[conversation];
  delete getFeedState().taskCards[conversation];
  vi.unstubAllGlobals();
});
const entries = () => [...host.querySelectorAll<HTMLButtonElement>("[data-assistant-actionbar] [data-tray-entry]")];
const entry = (key: string) => host.querySelector<HTMLButtonElement>(`[data-assistant-actionbar] [data-tray-entry="${key}"]`)!;

const label = { needs: t("assistant-tasks.tray_needs_you"), running: t("assistant-tasks.tray_running") };
// where a click went: a page of its own (#5096), never a side panel
const opened = () => location.pathname + location.search;
const trays = () => host.querySelector("[data-assistant-actionbar]")!.textContent;

describe("Assistant Needs you / In progress trays", () => {
  it("uses the Working mark and live subtitle for background work, with a clock only after all work ends", () => {
    act(() => entry("running").click());
    const row = host.querySelector("[data-task-slot]")!;
    const session: Session = { machine_id: "demo", session_id: "worker", title: "Demo agent", cwd: "/demo", status: "working", state: "working", updated_at: at,
      waiting: true, background_working: true, background_count: 2, background_tasks_count: 2 };
    act(() => upsertSession(session));
    expect(host.querySelector("[data-task-slot]")).toBe(row);
    expect(row.querySelector('[data-task-mark="working"]')).not.toBeNull();
    expect(row.querySelector('[data-task-mark="waiting"]')).toBeNull();
    expect(row.textContent).toContain(t("assistant-tasks.background_tasks_running", { count: 2 }));
    const workflow = { task_id: "wf", status: "running", running: true, phase: "Adversarial review", agents_done: 5, agents_started: 6 };
    act(() => upsertSession({ ...session, background_tasks_count: 0, background_count: 1, running_workflows: 1, workflow_progress: workflow }));
    expect(row.textContent).toContain("Adversarial review · 5/6+");
    expect(row.querySelector('[data-task-mark="working"]')).not.toBeNull();
    act(() => upsertSession({ ...session, background_tasks_count: 0, background_count: 0, running_subagents: 3 }));
    expect(row.textContent).toContain(t("assistant-tasks.subagents_count_running", { count: 3 }));
    expect(row.textContent).not.toContain("Adversarial review");
    act(() => upsertSession({ ...session, state: "idle", status: "idle", background_working: false, background_count: 0,
      background_tasks_count: 0, work: { phase: "blocked", on: "external", wait: "Wait for deployment", line: "Wait for deployment" } }));
    expect(row.querySelector('[data-task-mark="waiting"]')).not.toBeNull();
    expect(row.querySelector('[data-task-mark="working"]')).toBeNull();
    expect(row.textContent).toContain("Wait for deployment");
    expect(row.textContent).not.toContain(t("assistant-tasks.background_tasks_running", { count: 2 }));
    expect(writes).toEqual([]);
  });

  it("keeps two entries in order, collapses by default and replaces conversation cards with the dispatch's hand-off card", () => {
    expect(entries().map((button) => button.getAttribute("data-tray-entry"))).toEqual(["needs", "running"]);
    expect(entries().map((button) => button.textContent)).toEqual([label.needs, `${label.running}1`]);
    expect(host.querySelector("section")).toBeNull();
    // the plan's one Agent; the task line of a planned task adds none
    expect(host.querySelectorAll("[data-handoff-cards]")).toHaveLength(1);
    expect(host.querySelectorAll("[data-handoff-card]")).toHaveLength(1);
    expect(host.textContent).not.toContain("Demo result");
    act(() => entry("running").click());
    expect(entry("running").getAttribute("aria-expanded")).toBe("true");
    expect(host.querySelectorAll("[data-task-slot]")).toHaveLength(1);
    expect(host.querySelector('[role="progressbar"]')?.getAttribute("aria-valuenow")).toBe("2");
    expect(host.textContent).toContain("PR #42");
    // the task's agent is in its row, not a row of its own
    expect(host.querySelector("[data-running-agent]")).toBeNull();
    expect(trays()).not.toContain("Demo agent");
  });

  it("draws the dispatched Agent as a hand-off card that opens its page, not the In progress tray (#3700, #5096)", () => {
    const card = host.querySelector<HTMLButtonElement>('[data-handoff-card="demo/worker"]')!;
    expect(card.tagName).toBe("BUTTON");
    // the Agent's own title, New (the plan started it), and its state
    expect(card.textContent).toContain("Demo agent");
    expect(card.querySelector('[data-handoff-wake="created"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_created"));
    expect(card.querySelector('[data-handoff-state="working"]')).not.toBeNull();
    act(() => card.click());
    expect(opened()).toBe("/s/demo/worker");
    expect(getPanel()).toBeNull();
    expect(host.querySelector("section")).toBeNull();
    expect(entry("running").getAttribute("aria-expanded")).toBe("false");
  });

  it("draws a card for each Agent of several dispatched tasks, and the one that did not start says why", async () => {
    moreTodos = [{ id: "second-task", title: "Second task", origin: conversation, created_at: at, updated_at: at, agents: [{ machine_id: "demo", session_id: "second", linked_at: at }, { machine_id: "demo", session_id: "third", linked_at: at }] }];
    moreSessions = [
      { machine_id: "demo", session_id: "second", title: "Second agent", cwd: "/demo", state: "needs_you", status: "blocked", updated_at: at } as Session,
      { machine_id: "demo", session_id: "third", title: "", cwd: "/demo", state: "idle", status: "idle", updated_at: at } as Session,
    ];
    await act(async () => {
      await refresh();
      await refreshTodos();
      for (const x of moreSessions) upsertSession(x);
    });
    const many: TaskPlan = { id: "tray-plan-many", conversation, state: "started", created_at: at, tasks: [
      { title: "First task", prompt: "a", cwd: "/demo", machine: "demo", todo_id: "tray-task" },
      { title: "Second task", prompt: "b", cwd: "/demo", machine: "demo", todo_id: "second-task" },
      { title: "Failed task", prompt: "c", cwd: "/demo", machine: "demo", error: "machine offline" },
    ] };
    getFeedState().plans[many.id] = many;
    try {
      act(() => root.render(<StrictMode><AssistantActionBar conversation={conversation} /><FeedLineView item={{ id: "many-event", conversation, at, kind: "plan", ref: many.id }} simplified /></StrictMode>));
      // one card per Agent in plan order, no heading over them (cindy stacks cards)
      const group = host.querySelector("[data-handoff-cards]")!;
      expect(group.getAttribute("data-handoff-cards")).toBe("4");
      const cards = [...group.querySelectorAll<HTMLElement>("[data-handoff-card]")];
      expect(cards.map((el) => [el.tagName, el.getAttribute("data-handoff-card")])).toEqual([["BUTTON", "demo/worker"], ["BUTTON", "demo/second"], ["BUTTON", "demo/third"], ["DIV", "tray-plan-many/2"]]);
      // an Agent that needs you says so on its card; one without a title of its own takes its task's
      expect(cards[1]!.querySelector('[data-handoff-state="needs_you"]')).not.toBeNull();
      expect(cards[1]!.textContent).toContain("Second agent");
      expect(cards[2]!.textContent).toContain("Second task");
      expect(cards[3]!.textContent).toContain("Failed task");
      expect(cards[3]!.textContent).toContain(t("assistant-tasks.start_failed", { error: "machine offline" }));
      act(() => cards[1]!.click());
      expect(opened()).toBe("/s/demo/second");
      act(() => cards[0]!.click());
      expect(opened()).toBe("/s/demo/worker");
      expect(host.querySelector("section")).toBeNull();
    } finally {
      delete getFeedState().plans[many.id];
    }
  });

  it("updates the same row while it runs, opens the task, and drops it once it is accepted", () => {
    act(() => entry("running").click());
    const row = host.querySelector("[data-task-slot]");
    act(() => putTodo({ ...todo, supervision: { ...todo.supervision!, progress: { kind: "blocked", step: 3, steps: 5, text: "Waiting for a decision" } } }));
    expect(host.querySelector("[data-task-slot]")).toBe(row);
    expect(row?.textContent).toContain("Waiting for a decision");
    // the hub's paraphrase in the user's language replaces the agent's words in the row (#3181)
    act(() => putTodo({ ...todo, supervision: { ...todo.supervision!, progress: { kind: "blocked", step: 3, steps: 5, text: "Waiting for a decision", line: "Needs a call on the rollout" } } }));
    expect(row?.textContent).toContain("Needs a call on the rollout");
    expect(row?.textContent).not.toContain("Waiting for a decision");
    expect(host.querySelector('[role="progressbar"] .bg-orange')).not.toBeNull();
    act(() => (host.querySelector("[data-task-slot]") as HTMLButtonElement).click());
    // its Agent's page (#5096)
    expect(opened()).toBe("/s/demo/worker");
    act(() => putTodo({ ...todo, done_at: at, supervision: { ...todo.supervision!, state: "accepted" } }));
    // finished: in neither entry (the board has it), no fold for it either
    expect(entry("running").textContent).toBe(label.running);
    expect(entry("needs").textContent).toBe(label.needs);
    expect(host.querySelector("[data-task-slot]")).toBeNull();
    expect(host.textContent).toContain(t("assistant-tasks.tray_empty_progress"));
  });

  it("reuses round-bound owner acceptance and deduplicates its supervision decision", async () => {
    const firstRound = { outcome: "decide" as const, verdict: "Choose the rollout", recommend: "Use the demo rollout", asked_at: at };
    todo = { ...todo, ask: firstRound, supervision: { ...todo.supervision!, state: "needs_user" } };
    await act(async () => putTodo(todo));
    await act(async () => { entry("needs").click(); await import("./TaskDecision"); });
    expect(host.querySelectorAll('[data-brief-row="accept"]')).toHaveLength(1);
    expect(host.textContent).not.toContain(t("assistant-tasks.review_needs_user"));
    const first = [...host.querySelectorAll("button")].find((button) => button.textContent?.trim() === t("assistant-brief.take"))!;
    const nextRound = "2026-11-10T08:20:00Z";
    await act(async () => {
      first.click();
      todo = { ...todo, ask: { ...firstRound, asked_at: nextRound } };
      putTodo(todo);
    });
    const second = [...host.querySelectorAll("button")].find((button) => button.textContent?.trim() === t("assistant-brief.take"))!;
    expect(second).not.toBe(first);
    await act(async () => second.click());
    const rounds = vi.mocked(fetch).mock.calls.filter(([url]) => String(url).endsWith("/accept")).map(([, init]) => JSON.parse(String(init?.body)).asked_at);
    expect(rounds).toEqual([at, nextRound]);
    expect(writes.filter((path) => path.includes("message"))).toEqual([]);
  });

  it("removes a directly finished decision from Needs you without sending or accepting anything", async () => {
    const ask = { outcome: "decide" as const, verdict: "Choose the demo rollout", recommend: "Use the demo rollout", asked_at: at };
    todo = { ...todo, ask, supervision: { ...todo.supervision!, state: "needs_user" } };
    await act(async () => putTodo(todo));
    await act(async () => { entry("needs").click(); await import("./TaskDecision"); });
    const row = host.querySelector('[data-brief-row="accept"]')!;
    expect([...row.querySelectorAll("button")].map((b) => b.textContent?.trim())).toEqual([t("assistant-brief.open"), t("assistant-brief.finish"), t("assistant-brief.take")]);
    const read = vi.mocked(fetch).getMockImplementation()!;
    vi.mocked(fetch).mockImplementation(async (url, init) => {
      if (init?.method === "PATCH") {
        writes.push(String(url));
        todo = { ...todo, ask: undefined, done_at: at };
        return new Response(JSON.stringify({ todo }), { headers: { "Content-Type": "application/json" } });
      }
      return read(url, init);
    });
    await act(async () => [...row.querySelectorAll("button")][1].click());
    expect(host.querySelector('[data-brief-row="accept"]')).toBeNull();
    expect(host.textContent).toContain(t("assistant-tasks.tray_empty_pending"));
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatch(/\/todos\/tray-task$/);
    const request = vi.mocked(fetch).mock.calls.find(([, init]) => init?.method === "PATCH")!;
    expect(JSON.parse(String(request[1]?.body))).toEqual({ done: true });
    expect(writes.filter((path) => /accept|message|resume/.test(path))).toEqual([]);
  });

  it("puts a task waiting on the user and a draft in Needs you only, without sending anything", async () => {
    const draft: FeedItem = { id: "tray-draft", conversation, at, kind: "message", ref: "draft-ref", body: { draft: "Demo reply", text: "Demo message" } };
    getFeedState().feeds[conversation] = [...feed, draft];
    act(() => putTodo({ ...todo, supervision: { ...todo.supervision!, state: "needs_user" } }));
    expect(entries().map((button) => button.textContent)).toEqual([`${label.needs}2`, label.running]);
    await act(async () => {
      entry("needs").click();
      await import("./MessageDraft");
      await import("./TaskDecision");
    });
    expect(host.querySelector('[data-testid="message-card"]')).not.toBeNull();
    expect(host.textContent).toContain(t("assistant-tasks.review_needs_user"));
    const draftCard = host.querySelector('[data-testid="message-card"]')!;
    const actions = [...draftCard.querySelectorAll("button")];
    expect(actions.map((button) => button.textContent)).toEqual([t("assistant-tasks.send"), t("assistant-tasks.later"), t("assistant-tasks.dismiss_draft")]);
    act(() => actions[1].click());
    expect(host.querySelector('[data-testid="message-card"]')).toBeNull();
    expect(entries().map((button) => button.textContent)).toEqual([`${label.needs}1`, label.running]);
    act(() => entry("running").click());
    expect(host.querySelector("[data-task-slot]")).toBeNull();
    expect(writes).toEqual([]);
  });

  it("moves a task whose agent asks to Needs you, with the card under its name, and back once answered", async () => {
    moreSessions = [{ machine_id: "demo", session_id: "worker", title: "Demo agent", cwd: "/demo", state: "needs_you", status: "blocked", updated_at: at, pending_interactions: 1 } as Session];
    moreInteractions = [{ id: "ix-1", machine_id: "demo", session_id: "worker", kind: "question", created_at: at, questions: [{ question: "Which port?", options: [{ label: "8080" }, { label: "9090" }] }] } as Interaction];
    await act(async () => {
      await refresh();
      upsertSession(moreSessions[0]);
    });
    expect(entries().map((button) => button.textContent)).toEqual([`${label.needs}1`, label.running]);
    act(() => entry("needs").click());
    const row = host.querySelector('[data-needs-row="task:tray-task"]')!;
    expect(row.textContent).toContain("Demo task");
    expect(row.textContent).toContain("Which port?");
    // the agent is not a row of its own in either tray (the chat's hand-off card names it)
    expect(trays()).not.toContain("Demo agent");
    moreInteractions = [];
    await act(async () => {
      await refresh();
      upsertSession({ machine_id: "demo", session_id: "worker", title: "Demo agent", cwd: "/demo", state: "working", status: "working", updated_at: "2026-11-10T08:05:00Z" });
    });
    expect(entries().map((button) => button.textContent)).toEqual([label.needs, `${label.running}1`]);
  });

  it("lists Agents that belong to no task in the main conversation only, by what they need", async () => {
    mainChat = conversation;
    moreSessions = [
      { machine_id: "demo", session_id: "by-hand-asks", title: "Hand asks", cwd: "/demo", state: "needs_you", status: "blocked", updated_at: at, pending_interactions: 1 } as Session,
      { machine_id: "demo", session_id: "by-hand-works", title: "Hand works", cwd: "/demo", state: "working", status: "working", updated_at: at } as Session,
    ];
    moreInteractions = [{ id: "ix-hand", machine_id: "demo", session_id: "by-hand-asks", kind: "question", created_at: at, questions: [{ question: "Keep the old file?", options: [{ label: "Yes" }, { label: "No" }] }] } as Interaction];
    await act(async () => {
      await refresh();
      for (const s of moreSessions) upsertSession(s);
    });
    expect(entries().map((button) => button.textContent)).toEqual([`${label.needs}1`, `${label.running}2`]);
    act(() => entry("needs").click());
    expect(host.querySelector('[data-needs-row="agent:demo/by-hand-asks"]')?.textContent).toContain("Keep the old file?");
    expect(host.textContent).not.toContain("Hand works");
    act(() => entry("running").click());
    expect(host.querySelector('[data-running-agent="demo/by-hand-works"]')?.textContent).toContain("Hand works");
    expect(host.querySelector('[data-running-agent="demo/by-hand-asks"]')).toBeNull();
    // the task's own agent is still not listed by itself
    expect(host.querySelector('[data-running-agent="demo/worker"]')).toBeNull();
    act(() => (host.querySelector('[data-running-agent="demo/by-hand-works"]') as HTMLButtonElement).click());
    expect(opened()).toBe("/s/demo/by-hand-works");
  });

  it("draws the entries as the dock's old All / task capsules, with the open tray selected", () => {
    const dockHost = document.createElement("div");
    document.body.append(dockHost);
    const dockRoot = createRoot(dockHost);
    act(() => dockRoot.render(<TaskDock conversation={conversation} />));
    const all = dockHost.querySelector<HTMLButtonElement>('[data-testid="task-dock-board"]')!;
    const capsule = DOCK_CAPSULE.split(" ");
    for (const button of [all, ...entries()]) for (const name of capsule) expect(button.classList).toContain(name);
    act(() => dockRoot.unmount());
    dockHost.remove();
    // no fixed-width grid or tall rounded rectangle left over from #2920
    expect(host.querySelector("[data-assistant-actionbar] .grid")).toBeNull();
    for (const button of entries()) expect(button.className).not.toMatch(/rounded-control|py-2\b|flex-1/);
    // the open tray is the selected capsule; aria-expanded / aria-controls still say which one
    expect(entries().map((button) => button.getAttribute("aria-expanded"))).toEqual(["false", "false"]);
    act(() => entry("running").click());
    expect(entry("running").getAttribute("aria-expanded")).toBe("true");
    expect(entry("running").classList).toContain("bg-ink");
    expect(document.getElementById(entry("running").getAttribute("aria-controls")!)?.tagName).toBe("SECTION");
    expect(entry("needs").classList).not.toContain("bg-ink");
    act(() => entry("running").click());
    expect(entry("running").getAttribute("aria-expanded")).toBe("false");
    expect(host.querySelector("section")).toBeNull();
  });

  it("keeps an empty Needs you quiet (grey) but still opens it", () => {
    const needs = entry("needs");
    expect(needs.disabled).toBe(false);
    expect(needs.classList).toContain("text-ink-3");
    expect(needs.querySelector("[aria-hidden]")?.classList).toContain("bg-ink-3");
    expect(entry("running").querySelector("[aria-hidden]")?.classList).toContain("bg-accent");
    act(() => needs.click());
    expect(host.textContent).toContain(t("assistant-tasks.tray_empty_pending"));
  });

  it("lists every open task that does not wait on the user in In progress, and the one that does in Needs you", async () => {
    const mk = (id: string, title: string, sid: string | undefined, extra: Partial<Todo> = {}): Todo => ({ id, title, origin: conversation, created_at: at, updated_at: at, agents: sid ? [{ machine_id: "demo", session_id: sid, linked_at: at }] : [], ...extra });
    moreTodos = [
      mk("open-stopped", "Stopped between rounds", "idle-worker"),
      mk("open-unread", "Finished, not seen", "unread-worker"),
      mk("open-asks", "Needs a decision", "asking-worker"),
      mk("closed-old", "Closed long ago", "idle-worker", { done_at: at }),
      mk("closed-elsewhere", "Another chat's task", "idle-worker", { origin: "another-chat" }),
    ];
    await act(async () => {
      await refreshTodos();
      upsertSession({ machine_id: "demo", session_id: "idle-worker", title: "Idle", cwd: "/demo", state: "idle", status: "idle", updated_at: at });
      upsertSession({ machine_id: "demo", session_id: "unread-worker", title: "Unread", cwd: "/demo", state: "unread", status: "unread", updated_at: at });
      upsertSession({ machine_id: "demo", session_id: "asking-worker", title: "Asking", cwd: "/demo", state: "needs_you", status: "needs_you", updated_at: at });
    });
    expect(entries().map((button) => button.textContent)).toEqual([`${label.needs}1`, `${label.running}3`]);
    act(() => entry("running").click());
    const rows = [...host.querySelectorAll("[data-task-slot]")];
    expect(rows.map((row) => row.querySelector(".font-semibold")?.textContent)).toEqual(["Demo task", "Finished, not seen", "Stopped between rounds"]);
    expect(rows.map((row) => row.querySelector("[data-task-mark]")?.getAttribute("data-task-mark"))).toEqual(["working", "unread", "follow_up"]);
    expect(host.textContent).not.toContain("Another chat's task");
    expect(host.textContent).not.toContain("Closed long ago");
    expect(host.textContent).not.toContain("Needs a decision");
    act(() => entry("needs").click());
    expect(host.querySelector('[data-needs-row="task:open-asks"]')?.textContent).toContain("Needs a decision");
  });

  it("says why a stopped task is not done, from the hub's completion check (#3180)", async () => {
    const stoppedTodo: Todo = { id: "open-checked", title: "Waiting on CI", origin: conversation, created_at: at, updated_at: at,
      agents: [{ machine_id: "demo", session_id: "idle-worker", linked_at: at }], check: { verdict: "waiting", reason: "Waiting for CI to finish", at, work_since: at } };
    moreTodos = [stoppedTodo];
    await act(async () => {
      await refreshTodos();
      upsertSession({ machine_id: "demo", session_id: "idle-worker", title: "Idle", cwd: "/demo", state: "idle", status: "idle", updated_at: at, last_text: "The checks are queued" });
    });
    act(() => entry("running").click());
    const row = () => [...host.querySelectorAll("[data-task-slot]")].find((r) => r.querySelector(".font-semibold")?.textContent === "Waiting on CI");
    expect(row()?.textContent).toContain(t("assistant-tasks.progress_stopped_reason", { reason: "Waiting for CI to finish" }));
    expect(row()?.textContent).not.toContain("The checks are queued");
    // no reason: the line alone; done or unsure: the row as before
    act(() => putTodo({ ...stoppedTodo, check: { verdict: "not_done", at } }));
    expect(row()?.textContent).toContain(t("assistant-tasks.progress_stopped"));
    expect(row()?.textContent).not.toContain(" · ");
    act(() => putTodo({ ...stoppedTodo, check: { verdict: "unsure", reason: "Can't tell", at } }));
    expect(row()?.textContent).not.toContain(t("assistant-tasks.progress_stopped"));
    expect(row()?.textContent).toContain("The checks are queued");
    // working again: the agent's words, not the old check
    act(() => putTodo(stoppedTodo));
    await act(async () => upsertSession({ machine_id: "demo", session_id: "idle-worker", title: "Idle", cwd: "/demo", state: "working", status: "working", updated_at: at, last_text: "Rerunning the checks" }));
    expect(row()?.textContent).not.toContain(t("assistant-tasks.progress_stopped"));
  });

  it("counts a plan waiting for Start as one Needs you row, where Start is one click away", () => {
    const waiting: TaskPlan = { id: "tray-open-plan", conversation, state: "open", created_at: at, tasks: [{ title: "Diet card", prompt: "Build it", cwd: "/demo", machine: "demo" }, { title: "Fix the supervisor", prompt: "Fix it", cwd: "/demo", machine: "demo" }] };
    act(() => {
      getFeedState().plans[waiting.id] = waiting;
      putTodo({ ...todo });
    });
    try {
      expect(entries().map((button) => button.getAttribute("data-tray-entry"))).toEqual(["needs", "running"]);
      expect(entry("needs").textContent).toBe(`${label.needs}1`);
      expect(entry("needs").querySelector("[aria-hidden]")?.classList).toContain("bg-attention");
      // In progress counts what the To-dos page counts (#3331): the plan's tasks are no to-dos yet
      expect(entry("running").textContent).toBe(`${label.running}1`);
      act(() => entry("running").click());
      expect(host.querySelector('[data-task-slot="plan:tray-open-plan:0"]')).toBeNull();
      act(() => entry("needs").click());
      expect(entry("needs").getAttribute("aria-expanded")).toBe("true");
      expect(host.querySelector("section")?.textContent).toContain("Diet card");
      expect(host.querySelector("section")?.textContent).toContain("Fix the supervisor");
      expect([...host.querySelectorAll("section button")].some((button) => button.textContent?.includes(t("assistant-tasks.plan_start_n", { count: 2 })))).toBe(true);
      // once it is started or dismissed it leaves Needs you
      act(() => {
        getFeedState().plans[waiting.id] = { ...waiting, state: "dismissed" };
        putTodo({ ...todo, updated_at: "2026-11-10T08:01:00Z" });
      });
      expect(entry("needs").textContent).toBe(label.needs);
      expect(host.textContent).toContain(t("assistant-tasks.tray_empty_pending"));
    } finally {
      delete getFeedState().plans[waiting.id];
    }
  });
});
