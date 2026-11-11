// @vitest-environment jsdom
// The Persona conversation rows (the main row, the pinned and recent ones) keep their memo across a push that leaves
// their conversation as it was: the menu they share is one function for the sidebar's life. The menu still opens on
// the conversation as it is now.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
// every render of a SessionRow and of the main row reads its draft once: counted per "machine/session"
const draftReads = vi.hoisted(() => new Map<string, number>());
vi.mock("../lib/drafts", async (load) => {
  const real = await load<typeof import("../lib/drafts")>();
  return {
    ...real,
    useLeftDraft: (key: string | null) => {
      if (key) draftReads.set(key, (draftReads.get(key) ?? 0) + 1);
      return real.useLeftDraft(key);
    },
  };
});
import { t } from "../i18n";
import { connect, flushPushes, getState, refresh, reset, upsertSession } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// `efforts`: a field that is not a plain value, so a list parsed anew gives it a new array
const conversation = (session_id: string, title: string): Session => ({
  machine_id: "hub", session_id, title, cwd: "/persona", status: "idle", state: "idle", assistant: true, origin: "user", updated_at: "2026-05-01T10:00:00Z", efforts: ["low", "high"],
});
const main = conversation("main", "Persona");
const pinned = conversation("pinned", "Release notes");
const recent = conversation("recent", "Trip plan");
const agent: Session = { machine_id: "mac", session_id: "agent", title: "Build", cwd: "/code", status: "working", state: "working" };
// an agent on the assistant's own machine: its pushes carry that machine's whole list, the conversations too
const hubAgent: Session = { machine_id: "hub", session_id: "hub-agent", title: "Deploy", cwd: "/srv", status: "working", state: "working" };
const answers: Record<string, unknown> = {
  "/api/v1/assistant": { machine_id: "hub", online: true, main: "main", leaders: ["pinned", "recent"], conversations: [main, pinned, recent] },
  "/api/v1/sessions": { sessions: [main, pinned, recent, agent, hubAgent] },
  "/api/v1/machines": { machines: [{ id: "hub", name: "Hub", online: true }, { id: "mac", name: "MacBook", online: true }] },
};

let host: HTMLDivElement;
let root: Root;
const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const reads = (key: string) => draftReads.get(key) ?? 0;
const mainRow = () => host.querySelector<HTMLAnchorElement>(`a[title="${t("sidebar.persona_main")}"]`)!;
const row = (sid: string) => host.querySelector<HTMLAnchorElement>(`a[href="/persona/${sid}"]`)!;
const menu = () => document.querySelector('[role="menu"]');
const push = (mid: string, sid: string, change: Partial<Session>) => {
  act(() => upsertSession({ ...getState().sessions[mid].find((s) => s.session_id === sid)!, ...change }));
};

// the hub's socket: its messages go through the store as the hub's pushes do
class FakeSocket {
  static OPEN = 1;
  static last: FakeSocket | null = null;
  readyState = 0;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number }) => void) | null = null;
  constructor() {
    FakeSocket.last = this;
  }
  send() {}
  close() {
    this.readyState = 3;
  }
}
const online = () => {
  act(() => connect());
  const sock = FakeSocket.last!;
  sock.readyState = 1;
  act(() => sock.onopen?.());
  hubPush({ type: "hello", server_version: "v1", event_stream: "s1", event_seq: 1, resumed: true, features: ["sessions_delta"] });
};
const hubPush = (msg: Record<string, unknown>) => {
  act(() => FakeSocket.last!.onmessage?.({ data: JSON.stringify(msg) }));
  act(() => flushPushes());
};

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("assistant.pinned", '["pinned"]');
  draftReads.clear();
  vi.stubGlobal("WebSocket", FakeSocket);
  vi.stubGlobal("fetch", vi.fn((url: string) => Promise.resolve(new Response(JSON.stringify(answers[new URL(url, "http://x").pathname] ?? {}), { headers: { "Content-Type": "application/json" } }))));
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
});

async function renderSidebar() {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={{ name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
  expect(mainRow()).toBeTruthy();
  expect(row("pinned")).toBeTruthy();
  expect(row("recent")).toBeTruthy();
  expect(reads("hub/main")).toBeGreaterThan(0);
  expect(reads("hub/pinned")).toBeGreaterThan(0);
  draftReads.clear();
}

describe("the Persona conversation rows", () => {
  it("keep their render across pushes for an agent", async () => {
    await renderSidebar();
    for (let i = 1; i <= 10; i++) {
      push("mac", "agent", { title: `Build ${i}` });
      await flush();
    }
    expect(host.textContent).toContain("Build 10");
    expect(reads("mac/agent")).toBeGreaterThanOrEqual(10);
    expect(reads("hub/main")).toBe(0);
    expect(reads("hub/pinned")).toBe(0);
    expect(reads("hub/recent")).toBe(0);
  });

  it("render again only for their own conversation", async () => {
    await renderSidebar();
    push("hub", "recent", { title: "Trip plan, day 2" });
    await flush();
    expect(row("recent").textContent).toContain("Trip plan, day 2");
    expect(reads("hub/recent")).toBeGreaterThan(0);
    expect(reads("hub/pinned")).toBe(0);
    expect(reads("hub/main")).toBe(0);

    push("hub", "main", { status: "working", state: "working" });
    await flush();
    expect(mainRow().querySelector('[data-state="working"]')).not.toBeNull();
    expect(reads("hub/main")).toBeGreaterThan(0);
    expect(reads("hub/pinned")).toBe(0);
  });

  it("keep their render across pushes for an agent on the assistant's own machine", async () => {
    await renderSidebar();
    online();
    await flush();
    draftReads.clear();
    for (let i = 1; i <= 3; i++) {
      hubPush({ type: "sessions_delta", machine_id: "hub", upsert: [{ ...hubAgent, title: `Deploy ${i}` }], remove: [] });
      await flush();
    }
    expect(host.textContent).toContain("Deploy 3");
    expect(reads("hub/hub-agent")).toBeGreaterThanOrEqual(3);
    expect(reads("hub/main")).toBe(0);
    expect(reads("hub/pinned")).toBe(0);
    expect(reads("hub/recent")).toBe(0);

    // the machine's full list, every session in it parsed anew
    hubPush({ type: "sessions", machine_id: "hub", sessions: [main, pinned, recent, { ...hubAgent, title: "Deploy 4" }] });
    await flush();
    expect(host.textContent).toContain("Deploy 4");
    expect(reads("hub/main")).toBe(0);
    expect(reads("hub/pinned")).toBe(0);
    expect(reads("hub/recent")).toBe(0);

    // a push that does change a conversation still reaches its row
    hubPush({ type: "sessions_delta", machine_id: "hub", upsert: [{ ...pinned, title: "Release notes v2" }], remove: [] });
    await flush();
    expect(row("pinned").textContent).toContain("Release notes v2");
    expect(reads("hub/main")).toBe(0);
    expect(reads("hub/recent")).toBe(0);
  });

  it("open the menu on the conversation as it is now, from the ⋯ and on a right click", async () => {
    await renderSidebar();
    const more = () => mainRow().parentElement!.querySelector<HTMLButtonElement>(`button[aria-label="${t("conversation-menu.actions")}"]`)!;
    const close = async () => {
      act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
      await flush();
      expect(menu()).toBeNull();
    };

    // the main row's ⋯: the main conversation's menu (no pin, rename or archive), with End while it is live
    act(() => more().click());
    await flush();
    expect(menu()?.textContent).toContain(t("conversation-menu.copy_id"));
    expect(menu()?.textContent).not.toContain(t("conversation-menu.make_leader"));
    expect(menu()?.textContent).toContain(t("conversation-menu.end"));
    await close();

    // the main conversation and the pinned one go offline: their menus offer what they are now (no End), not what
    // they were when the menu function was made
    push("hub", "main", { status: "offline", state: "offline" });
    push("hub", "pinned", { status: "offline", state: "offline" });
    await flush();
    act(() => more().click());
    await flush();
    expect(menu()?.textContent).toContain(t("conversation-menu.copy_id"));
    expect(menu()?.textContent).not.toContain(t("conversation-menu.end"));
    await close();

    // a right click on the pinned row: that conversation's menu (pinned), as it is now
    act(() => row("pinned").dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 })));
    await flush();
    expect(menu()?.textContent).toContain(t("conversation-menu.end_leader"));
    expect(menu()?.textContent).toContain(t("conversation-menu.rename"));
    expect(menu()?.textContent).not.toContain(t("conversation-menu.end"));
  });
});
