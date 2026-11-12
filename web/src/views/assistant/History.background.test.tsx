// @vitest-environment jsdom
// "All chats" lists the background supervisor (#5082) with the scheduled tasks' chats, marked built in, and its menu only
// copies its id: it is not renamed, archived or made 常驻.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../../lib/types";

const hub = vi.hoisted(() => ({ fetched: [] as unknown[], state: { assistant: null as unknown, sessions: {} as Record<string, unknown[]> } }));
vi.mock("../../lib/api", async (original) => {
  const real = await original<typeof import("../../lib/api")>();
  return {
    ...real,
    api: vi.fn(async (path: string) => {
      if (path === "/assistant?archived=1") return { conversations: hub.fetched };
      throw new real.ApiError(404, "not found", {});
    }),
  };
});
vi.mock("../../lib/store", async (original) => {
  const real = await original<typeof import("../../lib/store")>();
  return {
    ...real,
    refreshAssistant: async () => {},
    useStore: <T,>(select: (s: ReturnType<typeof real.getState>) => T): T => select({ ...real.getState(), ...hub.state } as ReturnType<typeof real.getState>),
  };
});
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { History } from "./History";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.IS_REACT_ACT_ENVIRONMENT = true;
  g.matchMedia = (q: string) => ({ matches: q.includes("min-width"), addEventListener() {}, removeEventListener() {} });
});

const conv = (sid: string, p: Partial<Session> = {}): Session => ({
  machine_id: "server", session_id: sid, title: `对话 ${sid}`, cwd: "/home/me/.persona/assistant", status: "idle", assistant: true,
  updated_at: "2026-11-01T09:00:00Z", ...p,
} as Session);

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); });
};
const mount = async (origin?: string) => {
  root = createRoot(host);
  act(() => root.render(<History origin={origin} onToggleSidebar={() => {}} />));
  await flush();
};
const titles = () => [...host.querySelectorAll("a")].map((a) => a.getAttribute("title")?.split("\n")[0]);
const menuOf = async (title: string) => {
  const row = [...host.querySelectorAll("a")].find((a) => a.getAttribute("title")?.startsWith(title))!.parentElement!;
  await act(async () => (row.querySelector('button[aria-label="更多"]') as HTMLButtonElement).click());
  return [...document.querySelectorAll('[role="menuitem"]')].map((b) => b.textContent?.trim());
};

beforeEach(() => {
  const list = [conv("main"), conv("bg", { title: "后台督办", origin: "task" }), conv("morning", { origin: "task" }), conv("mine", { origin: "user" })];
  hub.fetched = list;
  hub.state = { assistant: { machine_id: "server", main: "main", background_supervisor: "bg", conversations: list }, sessions: { server: list } };
  host = document.createElement("div");
  document.body.append(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

describe("全部对话's background supervisor (#5082)", () => {
  it("is with the scheduled tasks' chats, under its plain name, marked built in", async () => {
    await mount("task");
    expect(titles()).toEqual(["后台督办", "对话 morning"]);
    const row = [...host.querySelectorAll("a")].find((a) => a.getAttribute("title")?.startsWith("后台督办"))!;
    expect(row.querySelector('[data-testid="built-in"]')?.textContent).toBe("内置");
    expect(host.querySelectorAll('[data-testid="built-in"]')).toHaveLength(1);
  });

  it("is not among the user's own", async () => {
    await mount("mine");
    expect(titles()).toEqual(["Persona", "对话 mine"]);
  });

  it("only copies its id, while a scheduled task's chat keeps its whole menu", async () => {
    await mount("task");
    expect(await menuOf("后台督办")).toEqual(["拷贝会话 ID"]);
    await act(async () => document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    const full = await menuOf("对话 morning");
    expect(full).toHaveLength(4); // 设为常驻 (with its accelerator), 重命名…, 拷贝会话 ID, 归档
    expect(full).toEqual(expect.arrayContaining(["重命名…", "拷贝会话 ID", "归档"]));
  });

  it("goes by its origin on a hub that does not name it", async () => {
    hub.state = { ...hub.state, assistant: { ...(hub.state.assistant as object), background_supervisor: undefined } };
    await mount("task");
    expect(titles()).toEqual(["后台督办", "对话 morning"]);
    expect(host.querySelector('[data-testid="built-in"]')).toBeNull();
  });
});
