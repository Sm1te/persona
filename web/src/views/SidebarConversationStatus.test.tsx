// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});

import { refresh, reset, upsertSession } from "../lib/store";
import type { Session } from "../lib/types";
import { Sidebar } from "./Sidebar";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation: Session = {
  machine_id: "hub", session_id: "review", title: "/agent · Review release notes", cwd: "/code",
  status: "idle", state: "idle", assistant: true, origin: "user", updated_at: "2026-05-01T10:00:00Z",
};
const main: Session = { ...conversation, session_id: "main", title: "Persona" };
let listed: Session;
let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
const row = () => host.querySelector<HTMLAnchorElement>('a[href="/persona/review"]')!;
const mark = () => row().querySelector('[data-state="working"]');

beforeEach(() => {
  reset();
  localStorage.clear();
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-05-01T10:02:00Z"));
  listed = { ...conversation };
  vi.stubGlobal("fetch", vi.fn((url: string) => {
    const path = new URL(url, "http://example.com").pathname;
    const data = path === "/api/v1/assistant" ? { machine_id: "hub", online: true, main: "main", leaders: ["review"], conversations: [main, listed] }
      : path === "/api/v1/sessions" ? { sessions: [main, listed] }
      : path === "/api/v1/machines" ? { machines: [{ id: "hub", name: "Hub", online: true }] }
      : {};
    return Promise.resolve(new Response(JSON.stringify(data), { headers: { "Content-Type": "application/json" } }));
  }));
  history.replaceState(null, "", "/");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function show(active = false) {
  await act(() => refresh());
  act(() => root.render(<Sidebar route={active ? { name: "persona", sid: "review" } : { name: "home" }} onSearch={() => {}} onNewAgent={() => {}} />));
  await flush();
}

describe("a working assistant conversation's sidebar mark", () => {
  it.each([
    { name: "a recent conversation", pinned: false, active: false, legacy: false },
    { name: "an open pinned /agent conversation", pinned: true, active: true, legacy: false },
    { name: "a conversation from a legacy hub", pinned: false, active: true, legacy: true },
  ])("updates $name without losing its timestamp or idle icon", async ({ pinned, active, legacy }) => {
    if (pinned) {
      listed.origin = "lark_agent";
      localStorage.setItem("assistant.pinned", '["review"]');
    }
    if (legacy) delete listed.state;
    await show(active);
    const idleIcon = row().firstElementChild!.innerHTML;
    const text = row().textContent;
    const classes = row().className;
    expect(mark()).toBeNull();

    const working: Session = { ...listed, status: "working", ...(legacy ? {} : { state: "working" }) };
    act(() => upsertSession(working));
    await flush();
    expect(mark()).not.toBeNull();
    expect(mark()!.getAttribute("data-shape")).toBe("arc");
    expect(mark()!.getAttribute("style")).toContain("var(--status-working)");
    expect(mark()!.getAttribute("aria-label")).toBe("工作中");
    expect(row().textContent).toBe(text);
    expect(row().className).toBe(classes);
    expect(row().querySelector("[data-agent-mark]")).toBeNull();
    expect(row().querySelector("[data-row-line]")).toBeNull();

    act(() => upsertSession({ ...listed }));
    await flush();
    expect(mark()).toBeNull();
    expect(row().firstElementChild!.innerHTML).toBe(idleIcon);
    expect(row().textContent).toBe(text);
  });

  it("shows work already in progress on the first load", async () => {
    listed = { ...listed, status: "working", state: "working" };
    await show();
    expect(mark()).not.toBeNull();
  });

  it("does not mistake a stale legacy status for the authoritative idle state", async () => {
    listed = { ...listed, status: "working", state: "idle" };
    await show();
    expect(mark()).toBeNull();
  });
});
