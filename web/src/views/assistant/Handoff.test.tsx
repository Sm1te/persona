// @vitest-environment jsdom
// What a dispatch draws in the conversation (#3700, #5096: cindy's hand-off card): a card for each Agent it handed work
// to, which opens that Agent's own page, never a side panel and never the whole In progress tray.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  globalThis.IntersectionObserver ??= class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } } as unknown as typeof IntersectionObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

import { t } from "../../i18n";
import { getFeedState, type FeedItem, type TaskPlan } from "../../lib/feed";
import { handoffFrom } from "../../lib/handoff";
import { getPanel } from "../../lib/panel";
import { applyCompleteSessions, refreshTodos, reset } from "../../lib/store";
import type { Todo } from "../../lib/todos";
import type { Session } from "../../lib/types";
import { lastActive, shortSessionId } from "./Handoff";
import { AssistantActionBar, FeedLineView } from "./Tasks";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const at = "2026-11-10T08:00:00Z";
const conversation = "dispatch-demo";
const agent: Session = { machine_id: "example-computer", session_id: "0f3c2a91-5d7e-4b8a-9c11-2e4f6a8b7ed0", title: "Example Agent", cwd: "/example", state: "working", status: "working", updated_at: at } as Session;
const linked = (id: string, title: string, sessions: Session[], extra: Partial<Todo> = {}): Todo => ({
  id, title, origin: conversation, created_at: at, updated_at: at,
  agents: sessions.map((s) => ({ machine_id: s.machine_id, session_id: s.session_id, title: s.title, linked_at: at })), ...extra,
});
let served: Todo[];
let host: HTMLDivElement;
let root: Root;
const settle = () => act(async () => { await new Promise((r) => setTimeout(r, 0)); });
const opened = () => location.pathname + location.search;
const tray = () => host.querySelector<HTMLButtonElement>('[data-tray-entry="running"]')!;
const cards = () => [...host.querySelectorAll<HTMLElement>("[data-handoff-card]")];

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => new Response(JSON.stringify(String(url).includes("/todos") ? { todos: served } : String(url).includes("/items") ? { items: [], to_seq: 0 } : {}), { headers: { "Content-Type": "application/json" } })));
  history.replaceState(null, "", "/persona");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  history.replaceState(null, "", "/");
  for (const id of Object.keys(getFeedState().plans)) delete getFeedState().plans[id];
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function show(item: FeedItem, todos: Todo[], sessions: Session[]) {
  served = todos;
  await act(async () => {
    applyCompleteSessions(sessions);
    await refreshTodos();
  });
  act(() => root.render(<StrictMode><AssistantActionBar conversation={conversation} /><div data-chat-scroll><FeedLineView item={item} simplified /></div></StrictMode>));
  await settle();
}

describe("hand-off cards", () => {
  it("one dispatched task: its Agent's card, New, opening the Agent's page with the way back, not the In progress tray", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task" }, [linked("example-task", "Example task", [agent])], [agent]);
    const [card] = cards();
    expect(cards()).toHaveLength(1);
    expect(card!.tagName).toBe("BUTTON");
    expect(card!.textContent).toContain("Example Agent");
    expect(card!.querySelector('[data-handoff-wake="created"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_created"));
    expect(card!.querySelector('[data-handoff-state="working"]')?.textContent).toContain(t("lib-state-labels.working"));
    // the id is only in the tooltip, cindy's short form
    expect(card!.textContent).not.toContain("0f3c2a91");
    expect(card!.getAttribute("title")).toBe("Example Agent · 0f3c2a91…7ed0");
    act(() => card!.click());
    expect(opened()).toBe(`/s/example-computer/${agent.session_id}`);
    expect(handoffFrom()).toEqual({ path: "/persona" });
    expect(getPanel()).toBeNull();
    expect(tray().getAttribute("aria-expanded")).toBe("false");
  });

  it("a task made of an Agent that was running already says Already active", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task", body: { wake: "active" } }, [linked("example-task", "Example task", [agent])], [agent]);
    expect(cards()[0]!.querySelector('[data-handoff-wake="active"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_active"));
  });

  it("a closed Agent the hand-off resumed says Resumed", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task", body: { wake: "resumed" } }, [linked("example-task", "Example task", [agent])], [agent]);
    expect(cards()[0]!.querySelector('[data-handoff-wake="resumed"]')?.textContent).toBe(t("assistant-tasks.handoff_wake_resumed"));
  });

  it("draws the Agents the hand-off went to, not one a later message started (that one has its own card)", async () => {
    const next: Session = { ...agent, session_id: "next-agent", title: "Next Agent" };
    const todo = linked("example-task", "Example task", [agent, next]);
    todo.agents![1]!.linked_at = "2026-11-10T09:00:00Z";
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task", body: { wake: "active" } }, [todo], [agent, next]);
    expect(cards().map((c) => c.getAttribute("data-handoff-card"))).toEqual([`example-computer/${agent.session_id}`]);
  });

  it("before its Agent is up the card is the task's: starting, and it opens the to-do", async () => {
    await show({ id: "task-event", conversation, at, kind: "task", ref: "example-task" }, [linked("example-task", "Example task", [])], []);
    const [card] = cards();
    expect(card!.textContent).toContain("Example task");
    expect(card!.textContent).toContain(t("assistant-tasks.starting_agent"));
    act(() => card!.click());
    expect(opened()).toBe("/todos?id=example-task");
  });

  it("a task that did not start says why; one removed since says so and opens nothing", async () => {
    const plan: TaskPlan = { id: "plan-two", conversation, state: "started", created_at: at, tasks: [
      { title: "Broken task", prompt: "a", cwd: "/example", machine: "example-computer", todo_id: "broken-task" },
      { title: "Removed task", prompt: "b", cwd: "/example", machine: "example-computer", todo_id: "removed-task" },
    ] };
    getFeedState().plans[plan.id] = plan;
    await show({ id: "plan-event", conversation, at, kind: "plan", ref: plan.id }, [linked("broken-task", "Broken task", [], { start_error: "launcher missing", done_at: at })], []);
    const [broken, removed] = cards();
    expect(broken!.textContent).toContain(t("assistant-tasks.start_failed", { error: "launcher missing" }));
    expect(removed!.tagName).toBe("DIV");
    expect(removed!.textContent).toContain("Removed task");
    expect(removed!.textContent).toContain(t("assistant-tasks.handoff_gone"));
  });

  it("a message that started a new Agent for its task has that Agent's card under it", async () => {
    const next: Session = { ...agent, session_id: "next-agent", title: "Next Agent" };
    await show({ id: "routed-event", conversation, at, kind: "routed", ref: "example-task", body: { text: "Go on", new_agent: "next-agent", new_agent_machine: "example-computer" } },
      [linked("example-task", "Example task", [agent, next])], [agent, next]);
    const [card] = cards();
    expect(cards()).toHaveLength(1);
    expect(card!.textContent).toContain("Next Agent");
    act(() => card!.click());
    expect(opened()).toBe("/s/example-computer/next-agent");
  });
});

describe("the card's words", () => {
  it("shortens an id as cindy does", () => {
    expect(shortSessionId("0f3c2a91-5d7e-4b8a-9c11-2e4f6a8b7ed0")).toBe("0f3c2a91…7ed0");
    expect(shortSessionId("short-id")).toBe("short-id");
  });

  it("says when the Agent was last active", () => {
    const now = Date.parse(at);
    expect(lastActive({ activity_at: new Date(now - 10_000).toISOString() }, now)).toBe(t("assistant-tasks.handoff_active_now"));
    expect(lastActive({ updated_at: new Date(now - 5 * 60_000).toISOString() }, now)).toMatch(/5/);
    expect(lastActive({}, now)).toBeUndefined();
  });
});
