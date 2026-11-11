// @vitest-environment jsdom
// #4678: the "Hand to agent" dialog keeps what is typed and the agent picked while sessions are pushed; it starts over only
// when it opens again, and moves the pick only once the picked agent is gone. The hub is faked; the data made up.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Todo } from "../lib/todos";
import type { Session } from "../lib/types";
import { SESSIONS_LIST_FEATURE } from "../lib/session-list";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});

const api = vi.fn(async (_path: string, _init?: unknown): Promise<unknown> => ({}));
vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: (path: string, init?: unknown) => api(path, init),
}));

const { getState, upsertSession, dropSession } = await import("../lib/store");
const { TodosView } = await import("./Todos");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const button = (label: string) => [...document.querySelectorAll<HTMLElement>("button")].find((b) => b.textContent?.trim() === label);
const click = async (el: Element | undefined | null) => {
  expect(el, "element").toBeTruthy();
  await act(async () => (el as HTMLElement).click());
  await flush();
};
const dialog = () => document.querySelector<HTMLElement>("[role=dialog]")!;
const box = () => dialog().querySelector<HTMLTextAreaElement>("textarea")!;
const typeInto = async (el: HTMLTextAreaElement, value: string) => {
  const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
  await act(async () => {
    set.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
};
const agentRow = (title: string) =>
  [...dialog().querySelectorAll<HTMLElement>("div.cursor-pointer")].find((r) => r.textContent?.includes(title));
const picked = () => [...dialog().querySelectorAll<HTMLElement>("div.cursor-pointer")].find((r) => r.className.includes("bg-accent"))?.textContent ?? "";

const iso = (agoMs: number) => new Date(Date.now() - agoMs).toISOString();
const todo: Todo = { id: "t1", title: "整理发布清单", created_at: iso(7_200_000), updated_at: iso(3_600_000) };
const session = (sid: string, title: string, o: Partial<Session> = {}) =>
  ({ machine_id: "m1", session_id: sid, title, status: "idle", ...o }) as Session;

const seed = (patch: Record<string, unknown>) => Object.assign(getState() as unknown as Record<string, unknown>, patch);
const before = { ...getState() };

beforeEach(() => {
  api.mockReset();
  api.mockImplementation(async (path: string) => (path.endsWith("/launchers") ? { launchers: [] } : {}));
  seed({
    todos: [todo],
    todosLoaded: true,
    machines: [{ id: "m1", name: "示例电脑", online: true }],
    sessions: { m1: [session("s1", "第一个 Agent"), session("s2", "第二个 Agent")] },
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
  document.body.innerHTML = "";
  seed(before);
  vi.restoreAllMocks();
});

const openHandover = async () => {
  await act(async () => root.render(<TodosView selectedId="t1" />));
  await flush();
  await click(button("交给 Agent"));
  await click(button("交给已有的 Agent"));
};

describe("交给 Agent 对话框开着时", () => {
  it("loads complete metadata on paging hubs instead of relying on the bounded summary list", async () => {
    const closed = session("closed", "Paged closed agent", { status: "offline", state: "offline", source_kind: "claude_cli", capabilities: { send: true } });
    seed({
      loaded: true,
      features: [SESSIONS_LIST_FEATURE],
      sessionSummaries: [session("s1", "Only summary")],
      todos: [{ ...todo, agents: [{ machine_id: "m1", session_id: "closed", linked_at: iso(1000) }] }],
    });
    api.mockImplementation(async (path: string) => path.startsWith("/sessions?")
      ? { sessions: [closed], remove: [], has_more: false, total: 1, revision: "handover-1" }
      : path.endsWith("/launchers") ? { launchers: [] } : {});
    await openHandover();
    expect(picked()).toContain("Paged closed agent");
    expect(agentRow("Only summary")).toBeUndefined();
    expect(api).toHaveBeenCalledWith("/sessions?summary=0&limit=200", expect.anything());
  });

  it("includes closed sendable agents only on online computers, preferring unreleased task agents", async () => {
    seed({
      todos: [{ ...todo, agents: [
        { machine_id: "m1", session_id: "closed", linked_at: iso(1000) },
        { machine_id: "m1", session_id: "released", linked_at: iso(2000), released_at: iso(1000) },
      ] }],
      machines: [{ id: "m1", name: "Online", online: true }, { id: "m2", name: "Offline", online: false }],
      sessions: {
        m1: [
          session("old", "Older", { updated_at: iso(3000) }),
          session("closed", "Closed task agent", { state: "offline", status: "offline", updated_at: iso(10000) }),
          session("released", "Released", { updated_at: iso(2000) }),
          session("recent", "Recent", { updated_at: iso(1000) }),
          session("readonly", "Read only", { source_kind: "codex_history", capabilities: { read: true } }),
          session("assistant", "Assistant", { assistant: true }),
        ],
        m2: [session("offline-host", "Offline host", { machine_id: "m2" })],
      },
    });
    await openHandover();
    const rows = [...dialog().querySelectorAll<HTMLElement>("div.cursor-pointer")];
    expect(rows.map((r) => r.textContent)).toEqual([
      expect.stringContaining("Closed task agent"),
      expect.stringContaining("Recent"),
      expect.stringContaining("Released"),
      expect.stringContaining("Older"),
    ]);
    expect(picked()).toContain("Closed task agent");
    expect(agentRow("Closed task agent")?.textContent).toContain("已关闭，发送后恢复");
  });

  it("keeps the user's selection and text when its process closes or another task agent arrives", async () => {
    seed({ todos: [{ ...todo, agents: [{ machine_id: "m1", session_id: "linked", linked_at: iso(1000) }] }] });
    await openHandover();
    await click(agentRow("第二个 Agent"));
    await typeInto(box(), "Keep this draft");
    await act(async () => {
      upsertSession(session("s2", "第二个 Agent", { status: "offline", state: "offline" }));
      upsertSession(session("linked", "Linked later", { updated_at: iso(0) }));
    });
    await flush();
    expect(picked()).toContain("第二个 Agent");
    expect(box().value).toBe("Keep this draft");
  });

  it("falls back when the selected computer disconnects and clears a vanished selection", async () => {
    seed({
      machines: [{ id: "m1", name: "First", online: true }, { id: "m2", name: "Second", online: true }],
      sessions: { m1: [session("s1", "First")], m2: [session("s2", "Second", { machine_id: "m2" })] },
    });
    await openHandover();
    await typeInto(box(), "Keep this draft");
    await act(async () => {
      seed({ machines: [{ id: "m1", name: "First", online: false }, { id: "m2", name: "Second", online: true }] });
      upsertSession(session("s2", "Second", { machine_id: "m2", status: "working" }));
    });
    await flush();
    expect(picked()).toContain("Second");
    expect(box().value).toBe("Keep this draft");
    await act(async () => dropSession("m2", "s2"));
    await flush();
    expect(picked()).toBe("");
    expect(dialog().textContent).toContain("在线电脑上暂无可用 Agent");
  });

  it("scopes the default and submission to the linked computer when session ids collide", async () => {
    seed({
      todos: [{ ...todo, agents: [{ machine_id: "m2", session_id: "same", linked_at: iso(1000) }] }],
      machines: [{ id: "m1", name: "First", online: true }, { id: "m2", name: "Second", online: true }],
      sessions: {
        m1: [session("same", "Unrelated", { updated_at: iso(0) })],
        m2: [session("same", "Linked closed", { machine_id: "m2", status: "offline", state: "offline" })],
      },
    });
    await openHandover();
    expect(picked()).toContain("Linked closed");
    const send = [...dialog().querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes("发送") && !b.disabled);
    await click(send);
    expect(api).toHaveBeenCalledWith("/sessions/m2/same/messages", expect.objectContaining({
      method: "POST",
      json: expect.objectContaining({ todo_id: "t1", client_msg_id: expect.any(String) }),
    }));
  });

  it("会话推送不清空输入、不改选中的 Agent", async () => {
    await openHandover();
    await click(agentRow("第二个 Agent"));
    await typeInto(box(), "把网络层的测试补齐");
    expect(picked()).toContain("第二个 Agent");

    // an agent starts working: the hub pushes its session
    await act(async () => upsertSession(session("s1", "第一个 Agent", { status: "working" })));
    await flush();
    expect(box().value).toBe("把网络层的测试补齐");
    expect(picked()).toContain("第二个 Agent");
  });

  it("选中的 Agent 不在了才换成第一个；关掉再打开从头开始", async () => {
    await openHandover();
    await click(agentRow("第二个 Agent"));
    await typeInto(box(), "先看一下");
    await act(async () => dropSession("m1", "s2"));
    await flush();
    expect(picked()).toContain("第一个 Agent");
    expect(box().value).toBe("先看一下");

    await click(button("取消"));
    await click(button("交给 Agent"));
    await click(button("交给已有的 Agent"));
    expect(box().value).toBe("");
  });

  it("关掉再打开时输入清空，选中项回到第一个", async () => {
    await openHandover();
    await click(agentRow("第二个 Agent"));
    await typeInto(box(), "这句不发了");
    await click(button("取消"));
    await click(button("交给 Agent"));
    await click(button("交给已有的 Agent"));
    expect(box().value).toBe("");
    expect(picked()).toContain("第一个 Agent");
  });

  it("开着时待办换了（比如被别处删掉），写给前一个待办的话不带过去", async () => {
    seed({ todos: [todo, { ...todo, id: "t2", title: "补齐测试" }] });
    await openHandover();
    await typeInto(box(), "写给整理发布清单的");
    await act(async () => root.render(<TodosView selectedId="t2" />));
    await flush();
    expect(box()?.value ?? "").toBe("");
  });
});
