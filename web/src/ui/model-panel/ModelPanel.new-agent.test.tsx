// @vitest-environment jsdom
// What the unified model panel gained for New Agent (#5104), as a person uses it: the follow row on top (checked while
// followed, applied on a click), presets as a group of their own (picked through `onPreset`, no star, no flyout), no
// Recommended group for an Agent not started yet, the engines a model runs with in its flyout (another one picked on a
// click), "How to fix" under a launcher that failed its check, "Configure models" handed to the caller, and the effort
// slider with no level set.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PanelPreset, PanelProvider, PanelState } from "../../lib/model-panel";

vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
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

const claude: PanelProvider = {
  id: "claude",
  name: "Claude",
  engine: "claude",
  typable: true,
  models: [
    { id: "default", name: "默认：Opus 5.5", efforts: EFFORTS, providerDefault: true },
    { id: "opus", name: "Opus 5.5", efforts: EFFORTS },
    { id: "gateway/glm-5.3", name: "glm-5.3", efforts: ["low", "medium", "high"] },
  ],
};
const codex: PanelProvider = { id: "codex", name: "Codex", engine: "codex", models: [{ id: "gpt-5.5", name: "5.5", efforts: [] }, { id: "gateway/glm-5.3", name: "glm-5.3", efforts: [] }] };
const blocked: PanelProvider = { id: "work_gw", name: "work_gw", engine: "claude", models: [{ id: "default", name: "默认", efforts: [], providerDefault: true }], unavailable: "这台电脑的终端里用不了" };
const state: PanelState = { current: { provider: "claude", model: "default" }, recommended: [], starting: true };
const presets: PanelPreset[] = [{ key: "review", name: "review", description: "Code review", setup: { provider: "claude", model: "opus", effort: "high" } }];

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

async function open(o: { follow?: boolean; active?: boolean; onConfigure?: () => void; state?: PanelState } = {}) {
  const onClose = vi.fn();
  const onPick = vi.fn(async () => true);
  const onApplyCurrent = vi.fn(async () => true);
  const onFollow = vi.fn(() => true);
  const onPreset = vi.fn(() => true);
  const onHelp = vi.fn();
  act(() =>
    root.render(
      <ModelPanel
        anchor={anchor}
        onClose={onClose}
        providers={[claude, blocked, codex]}
        state={o.state ?? state}
        scope="agent"
        onPick={onPick}
        onApplyCurrent={onApplyCurrent}
        follow={o.follow === false ? undefined : { active: o.active ?? true, label: "跟随模型配置", detail: "Claude · Opus 5.5", onFollow }}
        presets={presets}
        onPreset={onPreset}
        engines
        onHelp={onHelp}
        onConfigure={o.onConfigure}
      />,
    ),
  );
  await flush();
  return { onClose, onPick, onApplyCurrent, onFollow, onPreset, onHelp };
}

const rows = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")];
const row = (text: string, group?: string) => rows().find((r) => r.textContent?.includes(text) && (!group || r.closest("[data-panel-section]")?.getAttribute("aria-label") === group))!;
const groups = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-section]")].map((g) => g.getAttribute("aria-label"));
const button = (label: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === label || b.textContent?.trim() === label)!;
const key = (el: Element, k: string) => act(() => { el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })); });
const flyout = () => document.body.querySelector<HTMLElement>("[data-model-flyout]");
const stored = (kind: "recent") => JSON.parse(localStorage.getItem(panelStoreKey("agent", kind)) ?? "null");

describe("the model panel for a new Agent", () => {
  it("a follow row on top, checked while followed; Presets after the favorites; no Recommended group", async () => {
    await open();
    const follow = document.body.querySelector<HTMLElement>("[data-follow-row]")!;
    expect(follow.textContent).toContain("跟随模型配置");
    expect(follow.textContent).toContain("Claude · Opus 5.5");
    expect(follow.getAttribute("aria-selected")).toBe("true");
    // the list opens at its top, on the follow row (the panel scrolls to the first row marked current)
    expect(document.body.querySelector("[data-current]")).toBe(follow);
    expect(groups()).toEqual(["预设", "Claude", "work_gw", "Codex"]);
    // the model that runs stays lit in its own group
    expect(row("默认：Opus 5.5", "Claude").getAttribute("aria-selected")).toBe("true");
  });

  it("the follow row applies on a click and closes the panel; nothing goes into Recent", async () => {
    const { onFollow, onClose } = await open({ active: false });
    const follow = document.body.querySelector<HTMLElement>("[data-follow-row]")!;
    expect(follow.getAttribute("aria-selected")).toBe("false");
    key(follow, "Enter");
    await flush();
    expect(onFollow).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
    expect(stored("recent")).toBeNull();
  });

  it("a preset is picked through onPreset, with no star or Customize, and its setup goes into Recent", async () => {
    const { onPreset, onPick } = await open();
    const preset = row("review", "预设");
    expect(preset.textContent).toContain("Code review");
    expect(preset.querySelector('button[aria-label="收藏"], [data-row-customize]')).toBeNull();
    act(() => preset.click());
    await flush();
    expect(onPreset).toHaveBeenCalledWith("review");
    expect(onPick).not.toHaveBeenCalled();
    expect(stored("recent")).toEqual([{ provider: "claude", model: "opus", effort: "high" }]);
  });

  it("a launcher that failed its check: rows off, its reason once, How to fix handed to the caller", async () => {
    const { onHelp, onPick } = await open();
    const section = [...document.body.querySelectorAll<HTMLElement>("[data-panel-section]")].find((g) => g.getAttribute("aria-label") === "work_gw")!;
    expect(section.textContent).toContain("这台电脑的终端里用不了");
    const r = section.querySelector<HTMLElement>("[data-panel-row]")!;
    expect(r.getAttribute("aria-disabled")).toBe("true");
    act(() => r.click());
    expect(onPick).not.toHaveBeenCalled();
    act(() => button("怎么修好").click());
    expect(onHelp).toHaveBeenCalledWith("work_gw");
  });

  it("a model's flyout lists the engines it runs with here; another one is picked on a click", async () => {
    const { onPick, onClose } = await open();
    act(() => row("glm-5.3", "Claude").querySelector<HTMLButtonElement>("[data-row-customize]")!.click());
    const pills = [...flyout()!.querySelectorAll<HTMLButtonElement>("[data-engine-choice]")];
    expect(pills.map((p) => `${p.dataset.engineChoice}${p.getAttribute("aria-pressed") === "true" ? "*" : ""}`)).toEqual(["claude*", "codex"]);
    act(() => pills[1].click());
    await flush();
    expect(onPick).toHaveBeenCalledWith({ provider: "codex", model: "gateway/glm-5.3" });
    expect(onClose).toHaveBeenCalled();
  });

  it("a Default row is its launcher's own: its flyout shows its engine alone", async () => {
    await open();
    act(() => row("默认：Opus 5.5", "Claude").querySelector<HTMLButtonElement>("[data-row-customize]")!.click());
    expect(flyout()!.querySelectorAll("[data-engine-choice]")).toHaveLength(0);
  });

  it("no effort set: the slider says the model's default; a level picked applies at once", async () => {
    const { onApplyCurrent } = await open();
    key(row("默认：Opus 5.5", "Claude"), "ArrowLeft");
    await flush();
    const slider = flyout()!.querySelector<HTMLElement>('[role="slider"]')!;
    expect(slider.getAttribute("aria-valuetext")).toBe("模型默认");
    expect(slider.getAttribute("aria-valuenow")).toBeNull();
    key(slider, "ArrowRight");
    await flush();
    expect(onApplyCurrent).toHaveBeenLastCalledWith({ effort: "low" });
  });

  it("Reset to recommended on the chosen model with no default effort goes back to none", async () => {
    // the form took the level picked above (the panel keeps it as that model's setup too)
    localStorage.setItem(panelStoreKey("agent", "overrides"), JSON.stringify({ "claude\ndefault": { effort: "low" } }));
    const { onApplyCurrent } = await open({ state: { ...state, current: { provider: "claude", model: "default", effort: "low" } } });
    key(row("默认：Opus 5.5", "Claude"), "ArrowLeft");
    await flush();
    expect(flyout()!.textContent).toContain("已自定义");
    act(() => button("恢复推荐").click());
    await flush();
    expect(onApplyCurrent).toHaveBeenLastCalledWith({ effort: "" });
  });

  it("Configure models goes to the caller when it takes it over", async () => {
    const onConfigure = vi.fn();
    const { onClose } = await open({ onConfigure });
    act(() => button("配置模型").click());
    expect(onClose).toHaveBeenCalled();
    expect(onConfigure).toHaveBeenCalled();
    expect(navigated).toEqual([]);
  });
});
