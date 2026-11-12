// @vitest-environment jsdom
// Clearing the main conversation's context (#5095), as Cindy's /clear: the page stays the conversation's (its header, an
// empty chat, the box at the bottom) instead of turning into the centered start page; the first message starts the new
// main conversation there, a refused one comes back to the same place; once a main conversation shows again, a later
// empty main page is the ordinary start page.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  g.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} };
  g.IntersectionObserver = class { observe() {} unobserve() {} disconnect() {} takeRecords() { return []; } };
});

const hub = vi.hoisted(() => ({ main: "c1", calls: [] as { path: string; method: string; json?: unknown }[] }));
vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  api: (path: string, init?: { method?: string; json?: unknown }) => {
    const method = init?.method ?? "GET";
    hub.calls.push({ path, method, json: init?.json });
    if (path === "/assistant/main/clear" && method === "POST") {
      const archived = hub.main;
      hub.main = "";
      return Promise.resolve({ ok: true, archived });
    }
    if (path === "/assistant") return Promise.resolve({ machine_id: "hub", main: hub.main, conversations: [] });
    return Promise.resolve({});
  },
}));

type Started = { session: { machine_id: string; session_id: string } };
const starts = vi.hoisted(() => [] as { resolve: (r: Started) => void; reject: (e: unknown) => void }[]);
vi.mock("../lib/assistant-start", () => ({
  startConversation: () => new Promise<Started>((resolve, reject) => starts.push({ resolve, reject })),
}));
const navigate = vi.hoisted(() => vi.fn());
vi.mock("../lib/router", async (orig) => ({ ...(await orig<typeof import("../lib/router")>()), navigate }));
vi.mock("../lib/readiness", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/readiness")>()),
  useReadiness: () => ({ readiness: null, probing: false }),
  useReadinessGate: () => ({ step: null, blocked: false, readiness: null, help: () => {}, confirm: () => {} }),
  refreshReadiness: () => Promise.resolve(),
  passesBySelf: () => false,
  codedFailure: () => null,
}));
// the conversation page itself is not under test: which conversation it shows is
vi.mock("./chat/ChatView", async (orig) => ({
  ...(await orig<typeof import("./chat/ChatView")>()),
  ChatView: (p: { sid: string }) => <div data-chat={p.sid} />,
}));

import { clearMainContext, forgetMainCleared } from "../lib/main-clear";
import { getState, refreshAssistant, reset } from "../lib/store";
import { PersonaAssistant } from "./PersonaAssistant";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const WORDS = "帮我规划一下这个周末的行程";
const WELCOME = "有什么要我做的？";
let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const chat = () => host.querySelector("[data-chat]")?.getAttribute("data-chat") ?? null;
const cleared = () => host.querySelector("[data-main-cleared]");
const send = async () => {
  const area = host.querySelector("textarea")!;
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, WORDS);
    area.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
  await flush();
};
const hubMain = async (main: string) => {
  hub.main = main;
  await act(async () => { await refreshAssistant(); });
  await flush();
};

beforeEach(() => {
  reset();
  forgetMainCleared();
  hub.main = "c1";
  hub.calls.length = 0;
  starts.length = 0;
  navigate.mockClear();
  Object.assign(getState(), {
    loaded: true,
    features: ["assistant_main_clear"],
    assistant: { machine_id: "hub", main: "c1", conversations: [] },
    assistantError: null,
  });
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  forgetMainCleared();
  reset();
});

describe("清理主对话上下文 (#5095)", () => {
  it("原地清空：还是对话页的页头和底部输入框，中间空着，不变成居中的欢迎页", async () => {
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await flush();
    expect(chat()).toBe("c1");

    await act(async () => { expect(await clearMainContext("c1")).toBe(true); });
    await flush();
    expect(hub.calls).toContainEqual({ path: "/assistant/main/clear", method: "POST", json: { expect: "c1" } });
    expect(navigate).toHaveBeenCalledWith({ name: "persona" });
    expect(chat()).toBeNull();
    expect(cleared()).not.toBeNull();
    expect(host.querySelector("header")).not.toBeNull();
    expect(host.textContent).not.toContain(WELCOME);
    expect(host.querySelector("textarea")!.disabled).toBe(false);
  });

  it("清空后第一条消息就在原处开新的主对话；被拒绝时回到原处，文字回到输入框", async () => {
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await act(async () => { await clearMainContext("c1"); });
    await flush();

    await send();
    expect(starts).toHaveLength(1);
    expect(host.querySelector("[data-start-pending]")).not.toBeNull();
    await act(async () => starts[0].reject(new Error("no launcher")));
    await flush();
    expect(cleared()).not.toBeNull();
    expect(host.textContent).not.toContain(WELCOME);
    expect(host.querySelector("textarea")!.value).toBe(WORDS);
  });

  it("清空后的页面上再发 /clear：列表里有它，什么都不发，页面不变", async () => {
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await act(async () => { await clearMainContext("c1"); });
    await flush();
    const area = host.querySelector("textarea")!;
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, "/");
      area.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await flush();
    expect(host.textContent).toContain("/clear");
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(area, "/clear");
      area.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    await flush();
    expect(starts).toHaveLength(0);
    expect(cleared()).not.toBeNull();
    expect(host.querySelector("[data-start-pending]")).toBeNull();
  });

  it("新的主对话出现后就忘掉：之后再没有主对话（别的设备换掉），是平常的欢迎页", async () => {
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await act(async () => { await clearMainContext("c1"); });
    await flush();
    expect(cleared()).not.toBeNull();

    await hubMain("c2");
    expect(chat()).toBe("c2");
    await hubMain("");
    expect(cleared()).toBeNull();
    expect(host.textContent).toContain(WELCOME);
  });

  it("离开这一页时别的设备开了新主对话又清掉：回来是平常的欢迎页", async () => {
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await act(async () => { await clearMainContext("c1"); });
    await flush();
    act(() => root.render(<div />));
    await hubMain("c2");
    await hubMain("");
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await flush();
    expect(cleared()).toBeNull();
    expect(host.textContent).toContain(WELCOME);
  });

  it("没有在这里清理过：没有主对话时还是平常的欢迎页", async () => {
    await hubMain("");
    await act(async () => root.render(<PersonaAssistant onToggleSidebar={() => {}} />));
    await flush();
    expect(cleared()).toBeNull();
    expect(host.textContent).toContain(WELCOME);
  });
});
