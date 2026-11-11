// @vitest-environment jsdom
// The unified model panel (#5032) as a person uses it: the list's groups and the row that runs now, picks that wait for
// the chat (Recent records only applied ones), rows that can't be picked and why, the setup flyout (applied at once on
// the model that runs now, kept as an override on another), favorites, the keys (Tab / arrows / Enter / ← / Esc in
// order), typed IDs, a list being read or that failed, and "Configure models".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PanelProvider, PanelState } from "../../lib/model-panel";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  // a precise pointer: the search takes the focus when the panel opens
  g.matchMedia = (q: string) => ({ matches: q === "(any-pointer: fine)", media: q, addEventListener() {}, removeEventListener() {} });
  Element.prototype.scrollIntoView ??= () => {};
});

const navigated = vi.hoisted(() => [] as string[]);
vi.mock("../../lib/router", async (orig) => ({ ...(await orig<typeof import("../../lib/router")>()), navigate: (to: string) => navigated.push(to) }));

const { ModelPanel } = await import("./ModelPanel");
const { panelStoreKey } = await import("./store");

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const flush = () => act(async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); });
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];

const claude = (over: Partial<PanelProvider> = {}): PanelProvider => ({
  id: "claude",
  name: "Claude",
  engine: "claude",
  typable: true,
  models: [
    { id: "opus", name: "Opus 5.5", resolved: "claude-opus-5-5", description: "Most capable", efforts: EFFORTS, defaultEffort: "high", context: 1_000_000 },
    { id: "sonnet", name: "Sonnet 5", description: "Everyday", efforts: EFFORTS, defaultEffort: "medium" },
    { id: "vendor/model-x", name: "vendor/model-x", efforts: [], custom: true },
  ],
  ...over,
});
const codex = (over: Partial<PanelProvider> = {}): PanelProvider => ({ id: "codex", name: "Codex", engine: "codex", models: [{ id: "gpt-5.5", name: "GPT 5.5", efforts: ["low", "medium", "high"], fast: true }], unavailable: "需要新开对话", ...over });
const baseState: PanelState = { current: { provider: "claude", model: "claude-opus-5-5", effort: "xhigh" }, recommended: ["claude"] };

let host: HTMLDivElement;
let root: Root;
let anchor: HTMLButtonElement;

beforeEach(() => {
  localStorage.clear();
  navigated.length = 0;
  anchor = document.createElement("button");
  document.body.append(anchor);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  anchor.remove();
});

interface Opts {
  providers?: PanelProvider[];
  state?: PanelState;
  onPick?: (c: unknown) => Promise<boolean>;
  onApplyCurrent?: (p: { effort?: string; fast?: boolean }) => Promise<boolean>;
  onReload?: (p: string) => void;
  onForget?: (p: string, m: string) => void;
  header?: React.ReactNode;
}

async function open(o: Opts = {}) {
  const onClose = vi.fn();
  const onPick = vi.fn(o.onPick ?? (async () => true));
  const onApplyCurrent = o.onApplyCurrent && vi.fn(o.onApplyCurrent);
  const render = (extra: Partial<Opts> = {}) =>
    root.render(
      <ModelPanel
        anchor={anchor}
        onClose={onClose}
        providers={extra.providers ?? o.providers ?? [claude(), codex()]}
        state={extra.state ?? o.state ?? baseState}
        scope="agent"
        onPick={onPick}
        onApplyCurrent={onApplyCurrent}
        onReload={o.onReload}
        onForget={o.onForget}
        header={o.header}
      />,
    );
  act(() => render());
  await flush();
  return { onClose, onPick, onApplyCurrent, render };
}

const rows = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")];
const row = (text: string) => rows().find((r) => r.textContent?.includes(text))!;
const groups = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-section]")].map((g) => `${g.getAttribute("aria-label")}: ${[...g.querySelectorAll("[data-panel-row]")].map((r) => r.querySelector(".truncate")?.textContent).join(", ")}`);
const button = (label: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === label || b.textContent?.trim() === label)!;
const key = (el: Element, k: string, init: KeyboardEventInit = {}) => act(() => { el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, ...init })); });
const flyout = () => document.body.querySelector<HTMLElement>("[data-model-flyout]");
const search = () => document.body.querySelector<HTMLInputElement>("[data-model-panel] input")!;
function type(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    setter.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const stored = (kind: "favorites" | "recent" | "overrides") => JSON.parse(localStorage.getItem(panelStoreKey("agent", kind)) ?? "null");

describe("the model panel", () => {
  it("opens on All: Recommended (the model that runs now first, selected), then each provider; the search has the focus", async () => {
    await open();
    expect(groups()).toEqual(["推荐: Opus 5.5, Sonnet 5, vendor/model-x", "Codex: GPT 5.5"]);
    expect(row("Opus").getAttribute("aria-selected")).toBe("true");
    expect(row("Sonnet").getAttribute("aria-selected")).toBe("false");
    expect(document.activeElement).toBe(search());
    expect(document.body.querySelector('[role="listbox"]')).not.toBeNull();
    // the rail: Recent, Favorites, All (on), one cell per provider
    expect([...document.body.querySelectorAll("[data-rail-cell]")].map((c) => `${c.getAttribute("data-rail-cell")}${c.getAttribute("aria-pressed") === "true" ? "*" : ""}`)).toEqual(["recent", "favorites", "all*", "claude", "codex"]);
  });

  it("a pick waits for the chat: applied, it goes into Recent and the panel closes", async () => {
    const { onPick, onClose } = await open();
    key(row("Sonnet"), "Enter");
    await flush();
    expect(onPick).toHaveBeenCalledWith({ provider: "claude", model: "sonnet", effort: "xhigh" });
    expect(onClose).toHaveBeenCalled();
    expect(stored("recent")).toEqual([{ provider: "claude", model: "sonnet", effort: "xhigh" }]);
  });

  it("a refused pick keeps the panel open and records nothing", async () => {
    const { onPick, onClose } = await open({ onPick: async () => false });
    act(() => row("Sonnet").click());
    await flush();
    expect(onPick).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
    expect(stored("recent")).toBeNull();
  });

  it("rows that can't be picked are off and say why; the reason shows once under the provider's header", async () => {
    const { onPick } = await open();
    const gpt = row("GPT");
    expect(gpt.getAttribute("aria-disabled")).toBe("true");
    expect(gpt.title).toBe("需要新开对话");
    expect(document.body.querySelector('[data-panel-section="provider"]')?.textContent).toContain("需要新开对话");
    act(() => gpt.click());
    key(gpt, "Enter");
    await flush();
    expect(onPick).not.toHaveBeenCalled();
  });

  it("while the chat is busy every row is off and the reason is the note on top", async () => {
    const { onPick } = await open({ state: { ...baseState, busy: "Agent 正在工作" } });
    expect(document.body.querySelector('[role="note"]')?.textContent).toContain("Agent 正在工作");
    expect(rows().every((r) => r.getAttribute("aria-disabled") === "true")).toBe(true);
    key(row("Sonnet"), "Enter");
    await flush();
    expect(onPick).not.toHaveBeenCalled();
  });

  it("while busy the running model's row stays reachable for its flyout, but Enter picks nothing", async () => {
    const { onPick } = await open({ state: { ...baseState, busy: "Agent 正在工作" }, onApplyCurrent: async () => true });
    const opus = row("Opus");
    expect(opus.getAttribute("aria-disabled")).toBeNull();
    expect(rows().filter((r) => r !== opus).every((r) => r.getAttribute("aria-disabled") === "true")).toBe(true);
    key(opus, "Enter");
    await flush();
    expect(onPick).not.toHaveBeenCalled();
  });

  it("the flyout of the model that runs now applies an effort at once and stays open", async () => {
    const { onApplyCurrent } = await open({ onApplyCurrent: async () => true });
    key(row("Opus"), "ArrowLeft");
    await flush();
    const f = flyout()!;
    expect(f.getAttribute("aria-label")).toContain("Opus 5.5");
    expect(f.textContent).toContain("Claude · 1M 上下文");
    expect(f.textContent).toContain("推荐配置");
    const slider = f.querySelector<HTMLElement>('[role="slider"]')!;
    expect(document.activeElement).toBe(slider);
    expect(slider.getAttribute("aria-valuenow")).toBe("3");
    key(slider, "ArrowRight");
    await flush();
    expect(onApplyCurrent).toHaveBeenCalledWith({ effort: "max" });
    expect(flyout()).not.toBeNull();
    expect(stored("overrides")).toEqual({ "claude\nopus": { effort: "max" } });
  });

  it("the flyout of another model keeps its setup as an override, shown as customized until reset", async () => {
    const { onApplyCurrent } = await open({ onApplyCurrent: async () => true });
    act(() => row("Sonnet").querySelector<HTMLButtonElement>("[data-row-customize]")!.click());
    const slider = flyout()!.querySelector<HTMLElement>('[role="slider"]')!;
    key(slider, "Home");
    await flush();
    expect(onApplyCurrent).not.toHaveBeenCalled();
    expect(stored("overrides")).toEqual({ "claude\nsonnet": { effort: "low" } });
    expect(flyout()!.textContent).toContain("已自定义");
    act(() => button("恢复推荐").click());
    expect(stored("overrides")).toEqual({});
    expect(flyout()!.textContent).toContain("推荐配置");
  });

  it("Codex's current model takes effort and Fast in its flyout though its rows can't be picked", async () => {
    const state: PanelState = { current: { provider: "codex", model: "gpt-5.5", effort: "low" }, recommended: [] };
    const { onApplyCurrent, onPick } = await open({ providers: [codex({ unavailable: "Codex 不能换模型" })], state, onApplyCurrent: async () => true });
    const gpt = row("GPT");
    // reachable for its flyout, so not announced as disabled; its pick is still off
    expect(gpt.getAttribute("aria-disabled")).toBeNull();
    key(gpt, "Enter");
    await flush();
    expect(onPick).not.toHaveBeenCalled();
    act(() => gpt.querySelector<HTMLButtonElement>("[data-row-customize]")!.click());
    const fast = flyout()!.querySelector<HTMLButtonElement>("[data-fast-toggle]")!;
    expect(fast.getAttribute("aria-pressed")).toBe("false");
    act(() => fast.click());
    await flush();
    expect(onApplyCurrent).toHaveBeenCalledWith({ fast: true });
  });

  it("Esc closes the flyout first (the focus back on its row), then the panel", async () => {
    const { onClose } = await open();
    const sonnet = row("Sonnet");
    sonnet.focus();
    key(sonnet, "ArrowLeft");
    expect(flyout()).not.toBeNull();
    key(document.activeElement ?? document.body, "Escape");
    expect(flyout()).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(row("Sonnet"));
    key(document.activeElement!, "Escape");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("the star saves a copy of the row's setup once; Favorites lists it, and its star removes it", async () => {
    await open();
    const star = () => document.body.querySelector('[data-panel-section="recommended"]')!.querySelectorAll<HTMLButtonElement>('button[aria-label="存为收藏"]')[1];
    act(() => star().click());
    // the row's star is one way: a second press saves nothing new
    act(() => star().click());
    const saved = stored("favorites");
    expect(saved).toHaveLength(1);
    expect(saved[0]).toMatchObject({ provider: "claude", model: "sonnet", effort: "xhigh" });
    expect(groups()[0]).toBe("收藏: Sonnet 5");
    act(() => button("收藏").click());
    expect(groups()).toEqual(["收藏: Sonnet 5"]);
    act(() => rows()[0].querySelector<HTMLButtonElement>('button[aria-label="取消收藏"]')!.click());
    expect(stored("favorites")).toEqual([]);
    expect(document.body.textContent).toContain("还没有收藏");
  });

  it("Recent lists the setups applied, most recent first", async () => {
    localStorage.setItem(panelStoreKey("agent", "recent"), JSON.stringify([{ provider: "claude", model: "sonnet", effort: "low" }, { provider: "claude", model: "opus", effort: "xhigh" }]));
    await open();
    act(() => button("最近").click());
    expect(groups()).toEqual(["最近: Sonnet 5, Opus 5.5"]);
    expect(row("Opus").getAttribute("aria-selected")).toBe("true");
    // a recent row has no flyout
    expect(row("Sonnet").querySelector("[data-row-customize]")).toBeNull();
  });

  it("keys: Tab or ↓ from the search goes to the rows, ↑↓ move, ↑ on the first goes back", async () => {
    await open();
    key(search(), "ArrowDown");
    expect(document.activeElement).toBe(row("Opus"));
    key(row("Opus"), "ArrowDown");
    expect(document.activeElement).toBe(row("Sonnet"));
    key(row("Sonnet"), "ArrowUp");
    key(row("Opus"), "ArrowUp");
    expect(document.activeElement).toBe(search());
    key(search(), "Tab");
    expect(document.activeElement).toBe(row("Opus"));
  });

  it("a search lists every provider's matches and offers a typed ID; picking it sends the ID", async () => {
    const { onPick } = await open();
    act(() => button("Codex").click());
    type(search(), "vendor/new-1");
    expect(groups()).toEqual(["Claude: 使用「vendor/new-1」"]);
    expect(document.body.querySelector('[data-rail-cell="all"]')?.getAttribute("aria-pressed")).toBe("true");
    key(search(), "Enter");
    await flush();
    expect(onPick).toHaveBeenCalledWith({ provider: "claude", model: "vendor/new-1" });
    type(search(), "");
    expect(document.body.querySelector('[data-rail-cell="codex"]')?.getAttribute("aria-pressed")).toBe("true");
  });

  it("an ID typed before is in monospace with a second line and can be forgotten", async () => {
    const onForget = vi.fn();
    await open({ onForget });
    const typed = row("vendor/model-x");
    expect(typed.textContent).toContain("输入过的");
    expect(typed.querySelector(".font-mono")).not.toBeNull();
    act(() => typed.querySelector<HTMLButtonElement>('button[aria-label="忘记「vendor/model-x」"]')!.click());
    expect(onForget).toHaveBeenCalledWith("claude", "vendor/model-x");
  });

  it("a provider still being read shows a spinner; one that failed says why and offers Retry", async () => {
    const onReload = vi.fn();
    await open({ providers: [claude(), codex({ unavailable: undefined, models: null, loading: true }), { ...codex(), id: "cx2", name: "Codex 2", unavailable: undefined, models: null, error: "连不上" }], onReload });
    const sections = [...document.body.querySelectorAll('[data-panel-section="provider"]')];
    expect(sections[0].querySelector('[role="status"]')?.textContent).toContain("正在读取模型");
    expect(sections[1].textContent).toContain("连不上");
    act(() => button("重试").click());
    expect(onReload).toHaveBeenCalledWith("cx2");
  });

  it("Configure models opens Settings › Model providers and closes the panel", async () => {
    const { onClose } = await open();
    act(() => button("配置模型").click());
    expect(onClose).toHaveBeenCalled();
    expect(navigated).toEqual(["/settings?tab=models#settings-providers"]);
  });

  it("a press outside closes it; one on the pill (which toggles it itself) does not", async () => {
    const { onClose } = await open({ header: <div data-testid="strip">跟随默认</div> });
    expect(document.body.querySelector('[data-testid="strip"]')).not.toBeNull();
    act(() => { anchor.dispatchEvent(new Event("pointerdown", { bubbles: true })); });
    expect(onClose).not.toHaveBeenCalled();
    act(() => { search().dispatchEvent(new Event("pointerdown", { bubbles: true })); });
    expect(onClose).not.toHaveBeenCalled();
    act(() => { document.body.dispatchEvent(new Event("pointerdown", { bubbles: true })); });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps an Agent's records apart from a Persona chat's, under this hub's address", () => {
    expect(panelStoreKey("agent", "favorites")).toBe("persona.modelPanel..agent.favorites");
    expect(panelStoreKey("assistant", "recent", "/hub")).toBe("persona.modelPanel./hub.assistant.recent");
  });
});
