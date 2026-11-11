// @vitest-environment jsdom
// A sidebar row's ⋯ toggles the row's menu: a second press closes it. A browser sends a pointerdown before the click,
// and the menu closes on a pointerdown outside its panel and its anchor, so the ⋯ has to lie inside the anchor.
// jsdom's click() sends no pointerdown: `press` sends both, in order.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  // jsdom has no CSS.escape; the menu's outside-press check calls it
  const css = ((globalThis as { CSS?: Partial<typeof CSS> }).CSS ??= {});
  css.escape ??= (s: string) => s.replace(/[^\w-]/g, (c) => `\\${c}`);
});
import { t } from "../i18n";
import { connect, refresh, reset } from "../lib/store";
import type { PanesResponse } from "../lib/terminals";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = (session_id: string, title: string): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin: "user", updated_at: "2026-05-01T10:00:00Z",
});
const pane = (session_id: string, pane_id: string, title: string): Session => ({
  machine_id: "mac", session_id, title, cwd: "/code", status: "idle", state: "idle",
  space_kind: "herdr", space_id: "w1", space_name: "app", slot_id: "t1", slot_index: 1, slot_name: "#1", pane_id,
});
const main = conversation("main", "Persona");
const pinned = conversation("pinned", "Release notes");
const agent: Session = { machine_id: "mac", session_id: "agent", title: "Build docs", cwd: "/code", status: "working", state: "working" };
// a herdr tab with two panes: its lead row has the tab's menu
const lead = pane("lead", "p1", "Lead pane");
const split = pane("split", "p2", "Split pane");
// a plain shell on the machine's terminal host: a terminal row (Settings › Appearance › Show terminals in sidebar)
const panes: PanesResponse = {
  hosts: [{
    kind: "herdr", stream: true, control: true, new_tab: false,
    workspaces: [{ id: "w9", label: "shells" }],
    tabs: [{ id: "t9", workspace_id: "w9", label: "zsh" }],
    panes: [{ pane: "w9:p1", terminal: "pane:w9:p1", tab_id: "t9", workspace_id: "w9", title: "Shell pane", cwd: "/tmp" }],
  }],
};
const answers: Record<string, unknown> = {
  "/api/v1/assistant": { machine_id: "hub", online: true, main: "main", leaders: ["pinned"], conversations: [main, pinned] },
  "/api/v1/sessions": { sessions: [main, pinned, agent, lead, split] },
  "/api/v1/machines": { machines: [{ id: "hub", name: "Hub", online: true }, { id: "mac", name: "MacBook", online: true }] },
  "/api/v1/machines/mac/panes": panes,
};
const json = (body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); });
};
const menus = () => document.querySelectorAll('[role="menu"]');
const link = (text: string) => [...host.querySelectorAll("a")].find((a) => a.textContent?.includes(text));
// the ⋯ next to a row's link (not inside it: a button cannot sit in a link)
const more = (row: Element | undefined) => row?.parentElement?.querySelector<HTMLButtonElement>(':scope > button[aria-haspopup="menu"]') ?? null;
// a press as a browser sends it: pointerdown, then (a separate task) the click
const press = async (el: Element) => {
  act(() => void el.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true, button: 0, pointerType: "mouse" })));
  await flush();
  act(() => (el as HTMLElement).click());
  await flush();
};

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("assistant.pinned", '["pinned"]');
  localStorage.setItem("persona.agentGrouping", "machine");
  localStorage.setItem("sidebar.terminals", "1");
  vi.stubGlobal("fetch", vi.fn((url: string) => json(answers[new URL(url, "http://x").pathname] ?? {})));
  history.replaceState(null, "", "/overview");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

async function renderSidebar() {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
}

const personaRow = () => host.querySelector(`a[title="${t("sidebar.persona_main")}"]`) ?? undefined;
const rows: [string, () => Element | undefined][] = [
  ["the Persona main row", personaRow],
  ["a pinned conversation", () => link("Release notes")],
  ["an agent", () => link("Build docs")],
  ["a herdr tab with several panes", () => link("Lead pane")],
  ["a terminal pane", () => link("Shell pane")],
];

describe("a sidebar row's ⋯", () => {
  it.each(rows)("on %s opens the row's menu and closes it on a second press", async (_name, row) => {
    await renderSidebar();
    const button = more(row());
    expect(button).not.toBeNull();

    await press(button!);
    expect(menus()).toHaveLength(1);

    await press(more(row())!);
    expect(menus()).toHaveLength(0);

    // and opens it again on a third
    await press(more(row())!);
    expect(menus()).toHaveLength(1);
  });

  it("moves the menu to another row's ⋯, and a press elsewhere still closes it", async () => {
    await renderSidebar();
    await press(more(personaRow())!);
    expect(menus()).toHaveLength(1);
    expect(menus()[0].textContent).toContain(t("conversation-menu.end"));

    await press(more(link("Release notes"))!);
    expect(menus()).toHaveLength(1);
    expect(menus()[0].textContent).toContain(t("conversation-menu.end_leader"));

    act(() => void document.body.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 })));
    await flush();
    expect(menus()).toHaveLength(0);
  });

  it("on a right-clicked row's ⋯ moves the menu there instead of closing it", async () => {
    await renderSidebar();
    const row = link("Release notes")!;
    act(() => void row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 })));
    await flush();
    expect(menus()).toHaveLength(1);

    await press(more(row)!);
    expect(menus()).toHaveLength(1);
    await press(more(row)!);
    expect(menus()).toHaveLength(0);
  });
});

// The paged Agents list (a hub with sessions_list and nothing pinned or grouped): its rows' ⋯ toggles the same way.
describe("a paged Agents row's ⋯", () => {
  class FakeSocket {
    static last: FakeSocket | null = null;
    readyState = 0;
    onopen: (() => void) | null = null;
    onmessage: ((ev: { data: string }) => void) | null = null;
    constructor() { FakeSocket.last = this; }
    send() {}
    close() { this.readyState = 3; }
  }

  it("opens the row's menu and closes it on a second press", async () => {
    localStorage.removeItem("persona.agentGrouping");
    localStorage.removeItem("sidebar.terminals");
    vi.stubGlobal("WebSocket", FakeSocket);
    // the list draws only the rows in view of its scroller: give it one with a size (jsdom has no layout)
    host.style.overflowY = "auto";
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(800);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(280);
    vi.stubGlobal("fetch", vi.fn((input: string) => {
      const url = new URL(input, "http://x");
      if (url.pathname === "/api/v1/sessions" && url.searchParams.has("counts")) return json({ total: 1, state: { working: 1 }, machine: { mac: 1 }, group: {}, project: {}, revision: "r1" });
      if (url.pathname === "/api/v1/sessions" && url.search) return json({ sessions: [agent], remove: [], has_more: false, revision: "r1", total: 1 });
      return json(answers[url.pathname] ?? {});
    }));
    act(() => {
      connect();
      const socket = FakeSocket.last!;
      socket.readyState = 1;
      socket.onopen?.();
      socket.onmessage?.({ data: JSON.stringify({ type: "hello", features: ["sessions_list", "sessions_lookup"] }) });
    });
    await flush();
    act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
    await flush();
    // the paged list: no grouping control
    expect(host.querySelector(`[role="radiogroup"][aria-label="${t("sidebar.grouping_header")}"]`)).toBeNull();
    const button = more(link("Build docs"));
    expect(button).not.toBeNull();

    await press(button!);
    expect(menus()).toHaveLength(1);
    await press(more(link("Build docs"))!);
    expect(menus()).toHaveLength(0);
  });
});
