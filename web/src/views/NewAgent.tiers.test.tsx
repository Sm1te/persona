// @vitest-environment jsdom
// "New agent"'s presets (#2225), a group of their own in the model panel (#5104): the tiers of the model profile in use
// this computer can start. Picking one fills the launcher, model and effort as picking them by hand would (the model
// survives the new launcher's list loading) and says so under the first message box; a pick by hand drops it; what the
// launcher can't take is said, not silently dropped; "Follow Model settings" goes back to the defaults; a preset whose
// launcher failed its check here can't be picked.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";

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

import { NewAgent } from "./NewAgent";
import { Toasts } from "../ui/atoms";
import { resetModelListsForTests } from "../lib/session-models";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const TIERS = [
  { name: "quick", launcher: "claude" },
  { name: "review", launcher: "fast", model: "m-review", effort: "high", description: "代码审查和设计" },
];
let tiers: unknown = TIERS;
let purpose: unknown = null;
let claudeList: unknown = { agent: "claude", models: [{ id: "default", name: "Default", default: true }] };
let launchers: unknown[] = [{ name: "claude", agent: "claude" }, { name: "fast", agent: "claude" }];
let fastList: unknown = { agent: "claude", models: [{ id: "default", name: "Default", default: true }, { id: "m-review", name: "Review", efforts: ["low", "high"] }] };
let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
const button = (text: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === text)!;
const hint = () => document.body.querySelector("[data-testid=new-agent-preset-hint]");
const pill = () => document.body.querySelector<HTMLButtonElement>("[data-testid=new-agent-model] [data-model-pill]")!;
const panel = () => document.body.querySelector<HTMLElement>("[data-model-panel]");
const rows = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")];
const row = (key: string) => rows().find((r) => r.dataset.panelRow === key);
const preset = (name: string) => row(`preset:${name}`);

beforeEach(() => {
  resetModelListsForTests();
  state.machines = [{ id: "mac", name: "Mac", online: true }];
  launched.length = 0;
  tiers = TIERS;
  purpose = null;
  claudeList = { agent: "claude", models: [{ id: "default", name: "Default", default: true }] };
  launchers = [{ name: "claude", agent: "claude" }, { name: "fast", agent: "claude" }];
  fastList = { agent: "claude", models: [{ id: "default", name: "Default", default: true }, { id: "m-review", name: "Review", efforts: ["low", "high"] }] };
  localStorage.clear();
  localStorage.setItem("new.cwd.mac", "/code/app");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return purpose ? json(purpose) : json({ error: "not found" }, 404);
    if (url.includes("/launchers/fast/models")) return json(fastList);
    if (url.includes("/launchers/") && url.includes("/models")) return json(claudeList);
    if (url.includes("/launchers")) return json({ launchers, tiers, tiers_profile: tiers ? { id: "performance", name: "性能" } : undefined });
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

async function open() {
  act(() => root.render(<><NewAgent open onClose={() => {}} /><Toasts /></>));
  await flush();
}

async function openPanel() {
  act(() => pill().click());
  await flush();
}

/** Opens the panel and picks a row by its key; the panel closes once the form took it. */
async function pick(key: string) {
  await openPanel();
  act(() => row(key)!.click());
  await flush();
}

describe("New agent: presets (#2225)", () => {
  it("lists the presets as a group, fills launcher, model and effort from one and starts with them", async () => {
    await open();
    expect(hint()).toBeNull();
    await openPanel();
    const group = document.body.querySelector<HTMLElement>('[data-panel-section="presets"]')!;
    expect(group.getAttribute("aria-label")).toBe("预设");
    expect([...group.querySelectorAll<HTMLElement>("[data-panel-row]")].map((r) => r.dataset.panelRow)).toEqual(["preset:quick", "preset:review"]);
    expect(preset("review")!.textContent).toContain("代码审查和设计");
    act(() => preset("review")!.click());
    await flush();
    expect(panel()).toBeNull();
    expect(hint()?.textContent).toBe("预设 review · 代码审查和设计");
    // the model it runs is lit in its launcher's group (a preset row itself never is)
    await openPanel();
    expect(row("model:fast\nm-review")!.getAttribute("aria-selected")).toBe("true");
    expect(preset("review")!.getAttribute("aria-selected")).toBe("false");
    act(() => pill().click());
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ machineId: "mac", launcher: "fast", model: "m-review", effort: "high" });
  });

  it("drops the preset when something is picked by hand", async () => {
    await open();
    await pick("preset:review");
    expect(hint()).not.toBeNull();
    await pick("model:claude\ndefault");
    expect(hint()).toBeNull();
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "claude" });
    expect(launched[0]?.model).toBeUndefined();
    expect(launched[0]?.effort).toBeUndefined();
  });

  it("says what a launcher fixing its own model can't take, and sends none of it", async () => {
    fastList = { agent: "claude", models: [], locked: "启动器固定了模型参数" };
    await open();
    await pick("preset:review");
    expect(hint()?.textContent).toContain("m-review");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "fast" });
    expect(launched[0]?.model).toBeUndefined();
  });

  it("says an effort the preset's model doesn't take, and starts without it", async () => {
    tiers = [{ name: "deepest", launcher: "fast", model: "m-review", effort: "max" }];
    await open();
    await pick("preset:deepest");
    expect(hint()?.textContent).toBe("这里用不上：最高，会按启动器或模型自己的设置运行。");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "fast", model: "m-review" });
    expect(launched[0]?.effort).toBeUndefined();
  });

  it("has no preset group when the hub offers none", async () => {
    tiers = undefined;
    await open();
    await openPanel();
    expect(document.body.querySelector('[data-panel-section="presets"]')).toBeNull();
    expect(hint()).toBeNull();
    expect(document.body.textContent).not.toContain("预设");
  });

  it("keeps a preset's effort when the one before it ran on another launcher", async () => {
    claudeList = { agent: "claude", models: [{ id: "default", name: "Default", default: true, efforts: ["low", "medium", "high"] },
      { id: "opus", name: "Opus", efforts: ["low", "high", "max"] }] };
    fastList = { agent: "claude", models: [{ id: "default", name: "Default", default: true, efforts: ["low", "high", "max"] }] };
    tiers = [{ name: "big", launcher: "claude", model: "opus", effort: "max" }, { name: "deep", launcher: "fast", effort: "max" }];
    await open();
    await pick("preset:big");
    await pick("preset:deep");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "fast", effort: "max" });
  });

  // A preset picked again no longer goes back to the defaults (a preset row picks, it doesn't toggle, as every row of the
  // panel): "Follow Model settings" on top of the list is the way back.
  it("stays picked when picked again; Follow Model settings goes back to the defaults", async () => {
    purpose = { purpose: { id: "agent.new", effective: { provider: "launcher.claude" } }, providers: [] };
    await open();
    await pick("preset:review");
    await pick("preset:review");
    expect(hint()?.textContent).toBe("预设 review · 代码审查和设计");
    await openPanel();
    expect(document.body.querySelector("[data-follow-row]")!.getAttribute("aria-selected")).toBe("false");
    act(() => document.body.querySelector<HTMLElement>("[data-follow-row]")!.click());
    await flush();
    expect(hint()).toBeNull();
    act(() => button("启动").click());
    expect(launched[0]?.launcher).toBe("");
    expect(launched[0]?.model).toBeUndefined();
    expect(launched[0]?.effort).toBeUndefined();
  });

  it("can't pick a preset whose launcher failed its check here, and keeps nothing after the sheet reopens", async () => {
    launchers = [{ name: "claude", agent: "claude" }, { name: "fast", agent: "claude", health: { state: "blocked", kind: "auth", error: "没登录", checked_at: "2026-11-09T00:00:00Z" } }];
    await open();
    await openPanel();
    const review = preset("review")!;
    expect(review.getAttribute("aria-disabled")).toBe("true");
    expect(review.title).toBe("Key 或登录被拒绝了");
    act(() => review.click());
    await flush();
    expect(panel()).not.toBeNull();
    expect(hint()).toBeNull();
    act(() => preset("quick")!.click());
    await flush();
    expect(hint()).not.toBeNull();
    act(() => root.render(<><NewAgent open={false} onClose={() => {}} /><Toasts /></>));
    await flush();
    await open();
    expect(hint()).toBeNull();
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ launcher: "claude" });
    expect(launched[0]?.model).toBeUndefined();
    expect(launched[0]?.effort).toBeUndefined();
  });
});
