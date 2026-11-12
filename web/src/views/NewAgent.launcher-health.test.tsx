// @vitest-environment jsdom
// "New agent"'s launchers with their health on the computer (#1643), as groups of the model panel (#5104): one this
// computer's terminal can't run is listed with its rows off and why, never hidden, is not the one picked by default, offers
// how to fix it (under the first message box) instead of being picked, and "Try anyway" there picks it after all. The
// launcher that runs this time, when it can't start here, is said under the box with "Used this time"; Model settings'
// default that runs one is marked on "Follow Model settings". "Configure models" goes to Settings › Model providers. An
// older hub sends no health and nothing changes.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Launcher, Machine } from "../lib/types";

const state = vi.hoisted(() => ({ loaded: true, features: [] as string[], machines: [] as Machine[] }));
// one assistant: its computers are the store's (lib/current-assistant.ts reads the roster, which this store stand-in has none of)
vi.mock("../lib/current-assistant", async () => {
  const store = (await import("../lib/store")) as unknown as { useStore: <T>(select: (s: { machines: unknown[] }) => T) => T };
  return { useScopedMachines: () => store.useStore((s) => s.machines) };
});
vi.mock("../lib/store", () => ({
  getState: () => state,
  useStore: (selector: (value: typeof state) => unknown) => selector(state),
  hubHas: (features: string[], feature: string, unknown = false) => (features.length ? features.includes(feature) : unknown),
}));
vi.mock("react-dom", async (original) => ({ ...(await original<typeof import("react-dom")>()), createPortal: (children: React.ReactNode) => children }));
const launched = vi.hoisted(() => [] as Record<string, unknown>[]);
vi.mock("../lib/new-agent", async (original) => ({ ...(await original<typeof import("../lib/new-agent")>()), launchAgent: (input: Record<string, unknown>) => { launched.push(input); return "local-1"; } }));
const navigated = vi.hoisted(() => [] as unknown[]);
vi.mock("../lib/router", async (original) => ({ ...(await original<typeof import("../lib/router")>()), navigate: (to: unknown) => { navigated.push(to); } }));

import { NewAgent } from "./NewAgent";
import { resetModelListsForTests } from "../lib/session-models";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const BLOCKED: Launcher = { name: "work_gw", health: { state: "blocked", kind: "not_in_terminal", error: "bash: work_gw: command not found", checked_at: "2026-11-09T08:00:00Z" } };
const OK: Launcher = { name: "claude", agent: "claude", health: { state: "ok", checked_at: "2026-11-09T08:00:00Z" } };
let launchers: Launcher[] = [];
let purpose: unknown = null;
let host: HTMLDivElement;
let root: Root;
let closed = 0;
const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
const q = <T extends Element = HTMLElement>(sel: string) => document.body.querySelector<T & HTMLElement>(sel);
const button = (text: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === text)!;
const pill = () => q<HTMLButtonElement>("[data-testid=new-agent-model] [data-model-pill]")!;
const panel = () => q("[data-model-panel]");
const rows = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")];
const row = (key: string) => rows().find((r) => r.dataset.panelRow === key)!;
/** A launcher's group in the panel (its aria-label is the launcher's title). */
const group = (name: string) => [...document.body.querySelectorAll<HTMLElement>("[data-panel-section]")].find((g) => g.getAttribute("aria-label") === name);
const fix = (name: string) => [...group(name)!.querySelectorAll("button")].find((b) => b.textContent === "怎么修好")!;
const note = () => q('[data-testid="new-agent-blocked"]');

async function open(key: string) {
  act(() => root.render(<NewAgent key={key} open onClose={() => { closed++; }} />));
  await flush();
}

async function openPanel() {
  act(() => pill().click());
  await flush();
}

beforeEach(() => {
  resetModelListsForTests();
  state.machines = [{ id: "mac", name: "Mac", online: true }];
  launchers = [BLOCKED, OK];
  purpose = null;
  launched.length = 0;
  navigated.length = 0;
  closed = 0;
  localStorage.clear();
  localStorage.setItem("new.cwd.mac", "/code/app");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return purpose ? json(purpose) : json({ error: "not found" }, 404);
    if (url.includes("/launchers/") && url.includes("/models")) return json({ models: [] });
    if (url.includes("/launchers")) return json({ launchers });
    return json({ path: "/code", entries: [] });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("New agent's launcher health", () => {
  // The blocked list under the launcher chips is gone: a launcher that can't start here is said in its own group of the
  // panel, and under the box only when it is the one that runs this time.
  it("lists the launcher the terminal can't run with its rows off and why, and picks one that can start", async () => {
    localStorage.setItem("new.launcher.mac", "work_gw"); // the last one used is blocked here now
    await open("a");
    expect(pill().title).toContain("claude");
    expect(note()).toBeNull();
    await openPanel();
    const blocked = group("work_gw")!;
    expect(blocked.textContent).toContain("这台电脑的终端里用不了");
    expect(fix("work_gw")).toBeDefined();
    const off = row("model:work_gw\ndefault");
    expect(off.getAttribute("aria-disabled")).toBe("true");
    expect(off.getAttribute("aria-selected")).toBe("false");
    expect(off.title).toBe("这台电脑的终端里用不了");
    expect(row("model:claude\ndefault").getAttribute("aria-selected")).toBe("true");
    expect(row("model:claude\ndefault").getAttribute("aria-disabled")).toBeNull();
    expect(q('[data-testid="launcher-blocked-help"]')).toBeNull();
  });

  it("choosing it shows how to fix it instead of picking it, and Try anyway picks it", async () => {
    await open("b");
    await openPanel();
    act(() => row("model:work_gw\ndefault").click());
    await flush();
    expect(panel()).not.toBeNull(); // not picked: the panel stays as it was
    act(() => fix("work_gw").click());
    expect(panel()).toBeNull();
    expect(pill().title).toContain("claude");
    const help = q('[data-testid="launcher-blocked-help"]')!;
    expect(help.textContent).toContain("终端里用不了");
    expect(help.textContent).toContain("「work_gw」");
    expect(help.textContent).toContain("~/.bashrc");
    expect(q('[data-testid="launcher-blocked-detail"]')!.textContent).toContain("bash: work_gw: command not found");
    act(() => button("仍然使用").click());
    await flush();
    expect(pill().title).toContain("work_gw");
    expect(q('[data-testid="launcher-blocked-help"]')).toBeNull();
    // the reason stays said while it is the one that will run
    expect(note()!.textContent).toContain("本次使用");
    expect(note()!.textContent).toContain("这台电脑的终端里用不了");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ machineId: "mac", launcher: "work_gw" });
  });

  it("Add model provider opens Settings › Model providers with the wizard", async () => {
    await open("c");
    await openPanel();
    act(() => fix("work_gw").click());
    act(() => button("添加模型供应商").click());
    expect(closed).toBe(1);
    expect(navigated).toEqual(["/settings?tab=models&add=1"]);
  });

  // "Manage launchers" under the launcher chips (#4384) is gone with them: the panel's "Configure models" is the way.
  it("Configure models closes the dialog and opens Settings › Model providers", async () => {
    await open("m");
    await openPanel();
    act(() => button("配置模型").click());
    expect(panel()).toBeNull();
    expect(closed).toBe(1);
    expect(navigated).toEqual(["/settings?tab=models#settings-providers"]);
  });

  it("starts with a lone blocked launcher too, saying so, rather than hiding it", async () => {
    launchers = [BLOCKED];
    await open("d");
    expect(pill().disabled).toBe(false);
    expect(pill().title).toContain("work_gw");
    expect(note()!.textContent).toContain("本次使用");
    await openPanel();
    // the row that runs, lit with why (it stays reachable for its flyout, as the panel's running row always is)
    const runs = row("model:work_gw\ndefault");
    expect(runs.getAttribute("aria-selected")).toBe("true");
    expect(runs.title).toBe("这台电脑的终端里用不了");
    expect(fix("work_gw")).toBeDefined();
    act(() => pill().click());
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "work_gw" });
  });

  it("shows other blocked classes the same way, without the provider button", async () => {
    launchers = [OK, { name: "team_gw", health: { state: "blocked", kind: "auth", error: "401 invalid api key" } }, { name: "slow", health: { state: "failing", kind: "timeout" } }];
    await open("e");
    await openPanel();
    expect(row("model:team_gw\ndefault").getAttribute("aria-disabled")).toBe("true");
    expect(row("model:slow\ndefault").getAttribute("aria-disabled")).toBeNull(); // still the hub's to retry
    expect(group("slow")!.textContent).not.toContain("怎么修好");
    act(() => fix("team_gw").click());
    const help = q('[data-testid="launcher-blocked-help"]')!;
    expect(help.textContent).toContain("这台电脑上用不了");
    expect(help.textContent).not.toContain("添加模型供应商");
  });

  it("marks Follow Model settings when the launcher it runs can't start here, and still starts with it", async () => {
    purpose = { purpose: { id: "agent.new", effective: { provider: "launcher.work_gw" } }, providers: [] };
    launchers = [OK, BLOCKED];
    await open("g");
    // the launcher this start runs is said under the box
    expect(note()!.textContent).toContain("本次使用");
    expect(note()!.textContent).toContain("work_gw");
    expect(note()!.textContent).toContain("这台电脑的终端里用不了");
    await openPanel();
    // still the choice, with the warning: it is Model settings' to change
    const follow = q("[data-follow-row]")!;
    expect(follow.getAttribute("aria-selected")).toBe("true");
    expect(follow.getAttribute("aria-disabled")).toBeNull();
    expect(follow.querySelector("[title]")!.getAttribute("title")).toContain("这台电脑的终端里用不了");
    act(() => pill().click());
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ machineId: "mac", launcher: "" });
  });

  it("no mark on Follow Model settings when what it runs can start; the blocked one is still said in its group", async () => {
    purpose = { purpose: { id: "agent.new", effective: { provider: "launcher.claude" } }, providers: [] };
    await open("h");
    expect(note()).toBeNull();
    await openPanel();
    expect(q("[data-follow-row]")!.textContent).not.toContain("这台电脑的终端里用不了");
    expect(group("work_gw")!.textContent).toContain("这台电脑的终端里用不了");
  });

  it("names the launcher on How to fix for a screen reader, and points at the help it opens", async () => {
    purpose = { purpose: { id: "agent.new", effective: { provider: "launcher.team_gw" } }, providers: [] };
    launchers = [OK, BLOCKED, { name: "team_gw", health: { state: "blocked", kind: "auth" } }];
    await open("i");
    const how = note()!.querySelector("button")!;
    expect(how.getAttribute("aria-label")).toBe("怎么修好 team_gw");
    expect(how.getAttribute("aria-expanded")).toBe("false");
    act(() => how.click());
    expect(how.getAttribute("aria-expanded")).toBe("true");
    const help = q('[data-testid="launcher-blocked-help"]')!;
    expect(how.getAttribute("aria-controls")).toBe(help.id);
    // the cause itself is in the help, whole, not only in the line that can be narrow
    expect(q('[data-testid="launcher-blocked-reason"]')!.textContent).toBe("Key 或登录被拒绝了");
    // in the panel, each How to fix sits in its launcher's named group
    await openPanel();
    expect(fix("work_gw").closest('[role="group"]')!.getAttribute("aria-label")).toBe("work_gw");
    expect(fix("team_gw").closest('[role="group"]')!.getAttribute("aria-label")).toBe("team_gw");
  });

  it("an older hub's launchers (no health) are offered as before", async () => {
    launchers = [{ name: "claude" }, { name: "work_gw" }];
    await open("f");
    expect(note()).toBeNull();
    await openPanel();
    expect(row("model:work_gw\ndefault").getAttribute("aria-disabled")).toBeNull();
    act(() => row("model:work_gw\ndefault").click());
    await flush();
    expect(pill().title).toContain("work_gw");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "work_gw" });
  });
});
