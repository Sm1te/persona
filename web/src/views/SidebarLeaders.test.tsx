// @vitest-environment jsdom
// The leader conversations in the sidebar (#3623, #4331): Persona chats are the main one (its row above) and the hub's
// `assistant.leaders` only, "常驻", in the hub's order under each one's name with what it has Working and Waiting on you
// (the direction tree, GET /api/v1/assistant/directions/tree, #4352); every other chat is under 全部对话. The hub's
// `directions` message makes an open sidebar read the tree again (a leader made or ended anywhere).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../i18n";
import { resetDirectionNames } from "../lib/direction-names";
import { connect, refresh, reset } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = (session_id: string, title: string, updated_at: string): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin: "user", updated_at,
});
const conversations = [
  conversation("main", "Persona", "2026-11-10T09:00:00Z"),
  conversation("newest", "Trip plan", "2026-11-10T08:00:00Z"),
  conversation("mine", "Release notes", "2026-11-01T00:00:00Z"),
  conversation("lead-b", "Payments", "2026-11-02T00:00:00Z"),
  conversation("lead-a", "Growth", "2026-11-03T00:00:00Z"),
];
let leaders: string[] | undefined;
let named: unknown;
let posts: string[] = [];

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const rowIds = () => [...host.querySelectorAll<HTMLAnchorElement>('a[href^="/persona/"]')].map((a) => a.getAttribute("href")!.slice("/persona/".length)).filter((id) => id !== "new");
const headings = () => [...host.querySelectorAll('[role="heading"]')].map((h) => h.textContent);
const newChat = () => host.querySelector<HTMLAnchorElement>(`a[href="/persona/new"][aria-label="${t("sidebar.new_chat")}"]`);
const row = (sid: string) => host.querySelector<HTMLAnchorElement>(`a[href="/persona/${sid}"]`)!;

beforeEach(() => {
  localStorage.clear();
  leaders = ["lead-a", "lead-b"];
  posts = [];
  named = { leaders: [
    { id: "lead-a", title: "1110 | explore | Growth", name: "Product", counts: { working: 2, needs_you: 1 } },
    { id: "lead-b", title: "Payments", name: "Infrastructure", counts: { working: 0, needs_you: 0 } },
    { id: "old", title: "Old", name: "Ended one", ended: true },
  ] };
  vi.stubGlobal("fetch", vi.fn((url: string, init?: RequestInit) => {
    const path = new URL(url, "http://x").pathname;
    if (init?.method === "POST") {
      posts.push(path);
      return Promise.resolve(new Response(String(init.body), { headers: { "Content-Type": "application/json" } }));
    }
    if ((path === "/api/v1/assistant/leaders" || path === "/api/v1/assistant/directions/tree") && named === undefined) return Promise.resolve(new Response("{}", { status: 404 }));
    if (path === "/api/v1/assistant/directions/tree") {
      const list = (named as { leaders: { id: string; name?: string; ended?: boolean; counts?: unknown }[] }).leaders.filter((l) => !l.ended);
      return Promise.resolve(new Response(JSON.stringify({ directions: [...list.map((l) => ({ id: l.id, name: l.name, counts: { stuck: 0, review: 0, ...(l.counts as object) }, tasks: [] })), { id: "main", name: "", secretary: true, counts: { working: 9, needs_you: 9, stuck: 0, review: 0 }, tasks: [] }] }), { headers: { "Content-Type": "application/json" } }));
    }
    const body = path === "/api/v1/assistant" ? { machine_id: "hub", main: "main", leaders, conversations } : path === "/api/v1/assistant/leaders" ? named : path === "/api/v1/machines" ? { machines: [{ id: "hub", name: "Hub", online: true }] } : {};
    return Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
  }));
  history.replaceState(null, "", "/overview");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  resetDirectionNames();
  vi.unstubAllGlobals();
});

async function renderSidebar() {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
}

class FakeSocket {
  static last: FakeSocket | null = null;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  constructor() { FakeSocket.last = this; }
  send() {}
  close() { this.readyState = 3; }
}
const push = (msg: unknown) => act(() => FakeSocket.last!.onmessage?.({ data: JSON.stringify(msg) }));

describe("the leader conversations in the sidebar", () => {
  it("are 常驻, in the hub's order, under their names with what they have open; the other chats are under 全部对话", async () => {
    await renderSidebar();
    expect(rowIds()).toEqual(["lead-a", "lead-b"]);
    expect(headings()).toEqual([t("sidebar.directions")]);
    expect(t("sidebar.directions")).toBe("常驻");
    expect(row("lead-a").textContent).toContain("Product");
    expect(row("lead-a").textContent).toContain("在做 2 · 等你 1");
    expect(row("lead-b").textContent).toContain("Infrastructure");
    expect(row("lead-b").textContent).not.toContain("在做");
    // the pin icon on each, no other group, and none of the old words
    const allChats = host.querySelector<HTMLAnchorElement>('a[href="/history"]')!;
    expect(allChats.textContent).toContain(t("sidebar.all_chats"));
    expect(headings().join()).not.toMatch(/置顶|其他对话|方向/);
  });

  it("show their titles on a hub without the leaders list", async () => {
    named = undefined;
    await renderSidebar();
    expect(rowIds()).toEqual(["lead-a", "lead-b"]);
    expect(row("lead-a").textContent).toContain("Growth");
  });

  it("leave only 全部对话 when the hub has none", async () => {
    leaders = undefined;
    await renderSidebar();
    expect(rowIds()).toEqual([]);
    expect(headings()).toEqual([]);
    expect(host.querySelector('a[href="/history"]')).not.toBeNull();
  });

  it("have New chat as the compose icon on the section's title, not a row of its own", async () => {
    await renderSidebar();
    const link = newChat();
    expect(link).not.toBeNull();
    expect(link!.textContent).toBe("");
    expect(link!.closest("[data-sidebar-rows] .group\\/sec")).not.toBeNull();
    expect(link!.parentElement!.textContent).toContain(t("sidebar.persona_chats"));
  });

  it("are made and ended from the menu through the hub: 取消常驻 on one, never 置顶", async () => {
    await renderSidebar();
    act(() => row("lead-a").dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, clientX: 10, clientY: 10 })));
    await flush();
    const items = [...document.querySelectorAll<HTMLElement>('[role="menu"] [role="menuitem"]')];
    expect(items.some((el) => el.textContent?.includes("置顶"))).toBe(false);
    act(() => items.find((el) => el.textContent?.includes("取消常驻"))!.click());
    await flush();
    expect(posts).toEqual(["/api/v1/assistant/directions/lead-a/end"]);
  });

  it("follow the hub's directions message at once: a new one shows, an ended one goes, counts change", async () => {
    vi.stubGlobal("WebSocket", FakeSocket);
    act(() => {
      connect();
      FakeSocket.last!.readyState = 1;
      FakeSocket.last!.onopen?.();
    });
    push({ type: "hello", features: [] });
    await renderSidebar();
    expect(rowIds()).toEqual(["lead-a", "lead-b"]);
    // the secretary made "mine" a lead: the tree has it; no reload, no reconnect
    named = { leaders: [
      { id: "lead-a", name: "Product", counts: { working: 0, needs_you: 3 } },
      { id: "lead-b", name: "Infrastructure", counts: { working: 0, needs_you: 0 } },
      { id: "mine", name: "Docs sync", counts: { working: 1, needs_you: 0 } },
    ] };
    push({ type: "directions" });
    await flush();
    expect(rowIds()).toEqual(["lead-a", "lead-b", "mine"]);
    expect(row("mine").textContent).toContain("Docs sync");
    expect(row("mine").textContent).toContain("在做 1");
    expect(row("lead-a").textContent).toContain("等你 3");
    // the secretary group's counts are never a lead's
    expect(host.querySelector("[data-sidebar-rows]")!.textContent).not.toContain("在做 9");
    named = { leaders: [{ id: "mine", name: "Docs sync", counts: { working: 1, needs_you: 0 } }] };
    push({ type: "directions" });
    await flush();
    expect(rowIds()).toEqual(["mine"]);
  });
});
