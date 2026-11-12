// @vitest-environment jsdom
// An agent's page (`/s/…`, `/t/…`) takes only what it opens from the machine's session list: a push for another session
// on that machine leaves the shell alone, so its sidebar and the open chat are not drawn again. The shell's link
// handling stays as it was: a pane's `/s/…` goes to its tab, an id prefix to the whole id, several matches to a list.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// every render of the shell draws its sidebar and the page's chat: these stand-ins count them and read nothing from the store
const drawn = vi.hoisted(() => ({ sidebar: 0, chat: 0, chatSid: "" }));
vi.mock("./views/Sidebar", () => ({
  Sidebar: () => {
    drawn.sidebar++;
    return null;
  },
  SidebarRail: () => null,
}));
vi.mock("./views/chat/ChatView", () => ({
  ChatView: ({ sid }: { sid: string }) => {
    drawn.chat++;
    drawn.chatSid = sid;
    return null;
  },
}));
vi.mock("./views/Palette", () => ({ Palette: () => null }));
vi.mock("./views/NewAgent", () => ({ NewAgent: () => null }));
vi.mock("./views/NewWorktreeHost", () => ({ NewWorktreeHost: () => null }));
vi.mock("./views/SidePanel", () => ({ SidePanel: () => null }));
vi.mock("./views/ShortcutHelp", () => ({ ShortcutHelp: () => null }));
vi.mock("./lib/store", async (load) => ({ ...(await load<typeof import("./lib/store")>()), connect: () => {}, disconnect: () => {} }));
import { getState, refresh, reset, upsertSession } from "./lib/store";
import { doneSubagentLink, useSubagentLink } from "./lib/subagent-link";
import { setToken } from "./lib/api";
import type { Machine, Session } from "./lib/types";
import { App } from "./App";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const PREFIX_ID = "abcdef12-3456-4789-8abc-def012345678";
const TWIN_A = "fedcba98-0000-4000-8000-000000000001";
const TWIN_B = "fedcba98-0000-4000-8000-000000000002";
const machines: Machine[] = [{ id: "mac", name: "MacBook", online: true }];
const row = (session_id: string, extra: Partial<Session> = {}): Session => ({ machine_id: "mac", session_id, title: `Task ${session_id}`, cwd: "/code", status: "working", state: "working", ...extra });
const sessions: Session[] = [
  row("s1"),
  row("s2"),
  // two panes of one tab: the second needs you, so a tab link without a pane would open it
  row("p1", { space_id: "sp", slot_id: "sl" }),
  row("p2", { space_id: "sp", slot_id: "sl", state: "needs_you", status: "needs_you" }),
  row(PREFIX_ID),
  row(TWIN_A, { activity_at: "2026-11-01T10:00:00Z" }),
  row(TWIN_B, { activity_at: "2026-11-02T10:00:00Z" }),
  // a child session folded under s1 (#5120): a card in s1's chat, never a page of its own
  row("kid1", { folded_under: "s1", is_subagent: true, parent_session_id: "s1" }),
];
const answers: Record<string, unknown> = { "/api/v1/sessions": { sessions }, "/api/v1/machines": { machines } };

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const open = async (path: string) => {
  history.replaceState(null, "", path);
  await act(() => refresh());
  act(() => root.render(<App />));
  await flush();
};
const pushTitles = async (sid: string, times: number) => {
  for (let i = 0; i < times; i++) {
    const current = getState().sessions.mac.find((s) => s.session_id === sid)!;
    act(() => upsertSession({ ...current, title: `Task ${sid} (${i})` }));
    await flush();
  }
};

beforeEach(() => {
  localStorage.clear();
  setToken("tok");
  drawn.sidebar = drawn.chat = 0;
  drawn.chatSid = "";
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
});

describe("another session's push on the machine", () => {
  it("leaves a session page's shell and chat alone", async () => {
    await open("/s/mac/s1");
    expect(drawn.chatSid).toBe("s1");
    const sidebar = drawn.sidebar, chat = drawn.chat;
    expect(sidebar).toBeGreaterThan(0);

    await pushTitles("s2", 10);
    expect({ sidebar: drawn.sidebar - sidebar, chat: drawn.chat - chat }).toEqual({ sidebar: 0, chat: 0 });
  });

  it("leaves a tab page's shell and chat alone", async () => {
    await open("/t/mac/sp/sl/p1");
    expect(drawn.chatSid).toBe("p1");
    const sidebar = drawn.sidebar, chat = drawn.chat;

    await pushTitles("s2", 10);
    expect({ sidebar: drawn.sidebar - sidebar, chat: drawn.chat - chat }).toEqual({ sidebar: 0, chat: 0 });
  });
});

describe("the shell's session links", () => {
  it("replaces a pane's /s link with its tab's /t route", async () => {
    await open("/s/mac/p1");
    expect(location.pathname).toBe("/t/mac/sp/sl/p1");
    expect(drawn.chatSid).toBe("p1");
  });

  it("opens an 8-character prefix under the whole id", async () => {
    await open("/s/mac/abcdef12");
    expect(location.pathname).toBe(`/s/mac/${PREFIX_ID}`);
    expect(drawn.chatSid).toBe(PREFIX_ID);
  });

  it("lists the sessions an ambiguous prefix matches, newest first, and keeps the list up to date", async () => {
    await open("/s/mac/fedcba98");
    expect(location.pathname).toBe("/s/mac/fedcba98");
    expect(drawn.chat).toBe(0);
    const links = () => [...host.querySelectorAll("li a")].map((a) => a.getAttribute("href"));
    expect(links()).toEqual([`/s/mac/${TWIN_B}`, `/s/mac/${TWIN_A}`]);

    await pushTitles(TWIN_A, 1);
    expect(host.textContent).toContain(`Task ${TWIN_A} (0)`);
  });

  it("opens a child session's /s link as its parent's chat, which is asked for the child's card", async () => {
    let asked: string | undefined;
    function Probe() {
      asked = useSubagentLink("mac", "s1");
      return null;
    }
    const probe = createRoot(document.createElement("div"));
    act(() => probe.render(<Probe />));
    await open("/s/mac/kid1");
    expect(location.pathname).toBe("/s/mac/s1");
    expect(drawn.chatSid).toBe("s1");
    expect(asked).toBe("child:kid1");
    act(() => doneSubagentLink("mac", "s1"));
    act(() => probe.unmount());
  });

  it("an unlisted child opens as itself until its row says whose it is", async () => {
    await open("/s/mac/kid2");
    expect(drawn.chatSid).toBe("kid2");
    act(() => upsertSession(row("kid2", { folded_under: "s2", parent_session_id: "s2" })));
    await flush();
    expect(location.pathname).toBe("/s/mac/s2");
    expect(drawn.chatSid).toBe("s2");
    act(() => doneSubagentLink("mac", "s2"));
  });

  it("opens the pane a /t route names, not the tab's most urgent one", async () => {
    await open("/t/mac/sp/sl/p1");
    expect(location.pathname).toBe("/t/mac/sp/sl/p1");
    expect(drawn.chatSid).toBe("p1");
  });

  it("settles a /t route without a pane on the tab's most urgent one", async () => {
    await open("/t/mac/sp/sl");
    expect(drawn.chatSid).toBe("p2");
    expect(location.pathname).toBe("/t/mac/sp/sl/p2");
  });
});
