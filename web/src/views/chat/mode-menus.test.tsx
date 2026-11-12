// @vitest-environment jsdom
// #5584: the composer's permission mode menu shows each option's name over its full description, marks the current one,
// fits a narrow window and works from the keyboard. (The thinking effort is the model pill's, #5032 / #5033.)
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// jsdom has no matchMedia (the menu reads the phone-width query)
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({
    matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  })) as typeof window.matchMedia;
});
const api = vi.hoisted(() => vi.fn(async () => ({})));
vi.mock("../../lib/api", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../lib/api")>()), api }));
vi.mock("../../lib/store", async (importOriginal) => ({ ...(await importOriginal<typeof import("../../lib/store")>()), upsertSession: vi.fn() }));

import { ModeChips } from "./SessionDialogs";
import { switchableModes } from "../../lib/session-actions";
import type { Session } from "../../lib/types";
import { t } from "../../i18n";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

// a Persona conversation
const session = { machine_id: "server", session_id: "s1", assistant: true, agent: "claude", permission_mode: "acceptEdits", effort: "high" } as unknown as Session;
const startWidth = window.innerWidth;
let host: HTMLDivElement;
let root: Root;

function setInnerWidth(w: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, writable: true, value: w });
}

beforeEach(() => {
  setInnerWidth(1280);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<ModeChips session={session} assistant />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setInnerWidth(startWidth);
  api.mockClear();
});

const open = (title: string) => act(() => host.querySelector<HTMLButtonElement>(`button[title="${title}"]`)!.click());
const rows = () => [...document.querySelectorAll<HTMLButtonElement>('[role="menu"] [role="menuitemradio"]')];
const panel = () => document.querySelector<HTMLElement>('[role="menu"]')!;

describe("permission mode menu (#5584)", () => {
  it("shows every mode's name above its full description, which wraps instead of crowding the name out", () => {
    open(t("chat-sessiondialogs.permission_mode"));
    const modes = switchableModes(true, "claude");
    expect(rows()).toHaveLength(modes.length);
    rows().forEach((row, i) => {
      const [name, detail] = row.querySelectorAll<HTMLElement>("span.block");
      expect(name.textContent).toBe(modes[i].title);
      expect(name.className).not.toContain("truncate");
      expect(detail.textContent).toBe(modes[i].detail);
      expect(detail.className).toContain("break-words");
      expect(detail.className).not.toMatch(/truncate|whitespace-nowrap/);
      // the description is the row's accessible description, the name its accessible name
      expect(document.getElementById(row.getAttribute("aria-describedby")!)).toBe(detail);
      // a two-line row grows with its text instead of a fixed 32px that cuts it
      expect(row.className).toContain("min-h-8");
      expect(row.className).not.toMatch(/(^|\s)h-8(\s|$)/);
    });
  });

  it("marks the current mode for the eye and for screen readers, and the check never shrinks away", () => {
    open(t("chat-sessiondialogs.permission_mode"));
    expect(document.querySelectorAll('[role="menu"] [role="menuitem"]')).toHaveLength(0);
    const current = rows().filter((r) => r.getAttribute("aria-checked") === "true");
    expect(current).toHaveLength(1);
    expect(rows().filter((r) => r.getAttribute("aria-checked") === "false")).toHaveLength(rows().length - 1);
    expect(current[0].textContent).toContain(switchableModes(true, "claude").find((m) => m.id === "acceptEdits")!.title);
    for (const row of rows()) {
      const check = row.querySelector("svg")!;
      expect(check.getAttribute("class")).toContain("shrink-0");
      expect(check.getAttribute("class")).toContain(row === current[0] ? "text-ink" : "invisible");
    }
  });

  // jsdom has no layout, so this checks only the placement; clipping inside the panel is checked in a real browser by
  // scripts/test/web_mode_menu_e2e.py
  it("is placed at its 300px on a wide window and inside a 320px one", () => {
    open(t("chat-sessiondialogs.permission_mode"));
    expect(panel().style.width).toBe("300px");
    act(() => root.unmount());
    setInnerWidth(320);
    root = createRoot(host);
    act(() => root.render(<ModeChips session={session} assistant />));
    open(t("chat-sessiondialogs.permission_mode"));
    const left = parseFloat(panel().style.left);
    const width = parseFloat(panel().style.width);
    expect(width).toBeLessThanOrEqual(304);
    expect(left).toBeGreaterThanOrEqual(8);
    expect(left + width).toBeLessThanOrEqual(312);
  });

  it("is chosen from the keyboard", async () => {
    open(t("chat-sessiondialogs.permission_mode"));
    const key = (k: string) => act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })));
    key("ArrowDown");
    key("ArrowDown");
    await act(async () => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    const second = switchableModes(true, "claude")[1].id;
    expect(api).toHaveBeenCalledWith(expect.stringContaining("/modes"), { method: "POST", json: { permission_mode: second } });
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });
});

describe("Allow all on a running agent (#1379)", () => {
  const agent = (over: Partial<Session>) => ({ machine_id: "mac", session_id: "a1", agent: "claude", permission_mode: "acceptEdits", effort: "high", ...over }) as unknown as Session;
  const show = (s: Session) => {
    act(() => root.unmount());
    root = createRoot(host);
    act(() => root.render(<ModeChips session={s} />));
    // in "Allow all" the chip's title is its red warning
    open(s.permission_mode === "bypassPermissions" ? t("lib-session-actions.bypass_warning") : t("chat-sessiondialogs.permission_mode"));
  };
  const bypassRow = () => rows().find((r) => r.textContent?.includes(t("lib-session-actions.mode_bypass_permissions")));

  it("is not offered to a Claude that wasn't started so it can reach it", () => {
    show(agent({}));
    expect(rows()).toHaveLength(4);
    expect(bypassRow()).toBeUndefined();
  });

  it("is offered in red when the node says it can reach it, and asks again before switching", async () => {
    show(agent({ bypass_available: true }));
    expect(rows()).toHaveLength(5);
    const row = bypassRow()!;
    expect(row.className).toContain("text-red");
    await act(async () => row.click());
    expect(api).not.toHaveBeenCalled();
    expect(document.querySelector('[role="menu"]')!.textContent).toContain(t("chat-sessiondialogs.bypass_confirm"));
    expect(document.querySelector('[role="menu"]')!.textContent).toContain(t("lib-session-actions.bypass_warning"));
    await act(async () => bypassRow()!.click());
    expect(api).toHaveBeenCalledWith(expect.stringContaining("/modes"), { method: "POST", json: { permission_mode: "bypassPermissions" } });
  });

  it("is the checked row while the agent runs in it, so it can be switched away and back", () => {
    show(agent({ permission_mode: "bypassPermissions" }));
    expect(rows()).toHaveLength(5);
    expect(bypassRow()!.getAttribute("aria-checked")).toBe("true");
  });
});
