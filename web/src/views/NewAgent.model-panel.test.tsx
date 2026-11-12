// @vitest-environment jsdom
// "New agent"'s model choice in the model pill and its unified model panel (#5104, after Cindy's new-task composer): left
// alone it follows Model settings (nothing of the model is sent, the pill names what the settings run with the engine's
// mark); a model row sets the launcher, model and the effort it shows, and unchecks "Follow Model settings", which goes
// back; the picked model's flyout changes the effort ("Reset to recommended" clears it) and its engine pills pick the
// launcher that runs the same model with another engine; a pick goes along to another computer that has its launcher and
// falls back, saying so, on one that hasn't; a typed ID is sent; a model pick leaves the permission mode and folder alone;
// a list that can't be read is said under the box; a pick goes on past a computer whose launchers are still being read,
// and none of it to one whose launchers can't be read; a launcher that can't start here isn't asked for its list.
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

import { NewAgent } from "./NewAgent";
import { resetModelListsForTests } from "../lib/session-models";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
// the Mac has a launcher the Studio lacks; a gateway (relay) lists a model a Codex launcher lists too
const LAUNCHERS: Record<string, Launcher[]> = {
  mac: [{ name: "claude", agent: "claude" }, { name: "relay", agent: "claude" }, { name: "codex", agent: "codex" }, { name: "only-mac", agent: "claude" }],
  studio: [{ name: "claude", agent: "claude" }, { name: "relay", agent: "claude" }],
  mini: [{ name: "claude", agent: "claude" }, { name: "relay", agent: "claude" }],
};
const LISTS: Record<string, unknown> = {
  claude: { agent: "claude", models: [{ id: "default", name: "Default", default: true, efforts: EFFORTS }, { id: "opus", name: "Opus 5.5", efforts: EFFORTS, default_effort: "high" }, { id: "sonnet", name: "Sonnet 5", efforts: EFFORTS }] },
  relay: { agent: "claude", models: [{ id: "default", name: "Default", default: true }, { id: "glm-5", name: "GLM 5", efforts: ["low", "medium", "high"] }, { id: "gpt-5.5", name: "GPT 5.5" }] },
  codex: { agent: "codex", models: [{ id: "default", name: "Default", default: true }, { id: "gpt-5.5", name: "GPT 5.5" }] },
  "only-mac": { agent: "claude", models: [{ id: "default", name: "Default", default: true }] },
};
// Model settings › New agent: Claude Code's Opus at High
const PURPOSE = { purpose: { id: "agent.new", effective: { provider: "launcher.claude", model: "opus", effort: "high" } }, providers: [] };
let failList = "";
let failLaunchersOf = "";
const asked: string[] = [];

let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); });
const q = <T extends HTMLElement = HTMLElement>(sel: string) => document.body.querySelector<T>(sel);
const button = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === text)!;
const radio = (name: string) => [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent?.trim() === name)!;
const machineChip = (name: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === name && b.className.includes("h-8"))!;
const folder = () => q<HTMLInputElement>("input[placeholder]")!;
const pill = () => q<HTMLButtonElement>("[data-testid=new-agent-model] [data-model-pill]")!;
const panel = () => q("[data-model-panel]");
const flyout = () => q("[data-model-flyout]");
const follow = () => q("[data-follow-row]")!;
const row = (key: string) => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")].find((r) => r.dataset.panelRow === key)!;

function type(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    setter.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const key = (el: Element, k: string) => act(() => { el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true })); });

beforeEach(() => {
  resetModelListsForTests();
  state.machines = [{ id: "mac", name: "Mac", online: true }, { id: "studio", name: "Studio", online: true }];
  launched.length = 0;
  failList = "";
  failLaunchersOf = "";
  asked.length = 0;
  localStorage.clear();
  localStorage.setItem("new.machine", "mac");
  localStorage.setItem("new.cwd.mac", "/code/app");
  localStorage.setItem("new.cwd.studio", "/code/studio-app");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    asked.push(url);
    if (url.includes("/model-config/purposes/")) return json(PURPOSE);
    const list = /\/machines\/([^/]+)\/launchers\/([^/?]+)\/models/.exec(url);
    if (list) return decodeURIComponent(list[2]) === failList ? json({ error: "节点没有回应" }, 500) : json(LISTS[decodeURIComponent(list[2])] ?? { models: [] });
    const ls = /\/machines\/([^/]+)\/launchers/.exec(url);
    if (ls && decodeURIComponent(ls[1]) === failLaunchersOf) return json({ error: "节点没有回应" }, 500);
    if (ls) return json({ launchers: LAUNCHERS[decodeURIComponent(ls[1])] ?? [], default: { provider: "claude", model: "opus", effort: "high" } });
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
  act(() => root.render(<NewAgent open onClose={() => {}} />));
  await flush();
}

async function openPanel() {
  act(() => pill().click());
  await flush();
  expect(panel()).not.toBeNull();
}

/** Opens the panel and picks a row by its key (`model:<launcher>\n<id>`); the panel closes once the form took it. */
async function pick(k: string) {
  await openPanel();
  act(() => row(k).click());
  await flush();
  expect(panel()).toBeNull();
}

/** Opens a row's setup flyout. */
async function customize(k: string) {
  act(() => row(k).querySelector<HTMLButtonElement>("[data-row-customize]")!.click());
  await flush();
  expect(flyout()).not.toBeNull();
}

async function slide(k: string) {
  key(flyout()!.querySelector('[role="slider"]')!, k);
  await flush();
}

const start = () => act(() => button("启动").click());

describe("New agent's model panel (#5104)", () => {
  it("left alone, follows Model settings: nothing of the model is sent, the pill names what they run with the engine's mark", async () => {
    await open();
    expect(pill().title).toBe("Claude Code · Opus 5.5 · claude · 高");
    expect(pill().querySelector("[data-pill-engine]")!.getAttribute("data-pill-engine")).toBe("claude");
    await openPanel();
    expect(follow().getAttribute("aria-selected")).toBe("true");
    expect(follow().textContent).toBe("跟随模型配置claude · Opus 5.5 · 高");
    // the row lit is the launcher's Default row, named after the model the settings run
    const lit = row("model:claude\ndefault");
    expect(lit.getAttribute("aria-selected")).toBe("true");
    expect(lit.textContent).toContain("默认：Opus 5.5");
    act(() => pill().click());
    start();
    expect(launched).toHaveLength(1);
    expect(launched[0]).toMatchObject({ machineId: "mac", cwd: "/code/app", launcher: "" });
    expect(launched[0].model).toBeUndefined();
    expect(launched[0].effort).toBeUndefined();
  });

  it("a model row sets the launcher, the model and the effort it shows, and unchecks Follow Model settings", async () => {
    await open();
    await openPanel();
    // the row says the effort a pick applies: the one running now, which this model takes
    expect(row("model:claude\nsonnet").querySelector("[data-row-setup]")!.textContent).toBe("高");
    act(() => row("model:claude\nsonnet").click());
    await flush();
    expect(panel()).toBeNull();
    expect(pill().title).toBe("Claude Code · Sonnet 5 · claude · 高");
    await openPanel();
    expect(follow().getAttribute("aria-selected")).toBe("false");
    expect(row("model:claude\nsonnet").getAttribute("aria-selected")).toBe("true");
    expect(row("model:claude\ndefault").getAttribute("aria-selected")).toBe("false");
    act(() => pill().click());
    start();
    expect(launched[0]).toMatchObject({ launcher: "claude", model: "sonnet", effort: "high" });
  });

  it("Follow Model settings goes back to the default after a pick", async () => {
    await open();
    await pick("model:relay\nglm-5");
    expect(pill().title).toContain("GLM 5");
    await openPanel();
    act(() => follow().click());
    await flush();
    expect(panel()).toBeNull();
    expect(pill().title).toBe("Claude Code · Opus 5.5 · claude · 高");
    start();
    expect(launched[0]?.launcher).toBe("");
    expect(launched[0]?.model).toBeUndefined();
    expect(launched[0]?.effort).toBeUndefined();
  });

  it("sends the effort changed in the picked model's flyout", async () => {
    await open();
    await pick("model:relay\nglm-5");
    await openPanel();
    await customize("model:relay\nglm-5");
    expect(flyout()!.querySelector('[role="slider"]')!.getAttribute("aria-valuetext")).toBe("高");
    await slide("ArrowLeft");
    expect(pill().title).toBe("Claude Code · GLM 5 · relay · 中");
    expect(flyout()!.textContent).toContain("已自定义");
    act(() => pill().click());
    start();
    expect(launched[0]).toMatchObject({ launcher: "relay", model: "glm-5", effort: "medium" });
  });

  it("Reset to recommended in the flyout clears the effort again", async () => {
    await open();
    await pick("model:relay\nglm-5");
    await openPanel();
    await customize("model:relay\nglm-5");
    await slide("ArrowLeft");
    act(() => button("恢复推荐").click());
    await flush();
    expect(pill().title).toBe("Claude Code · GLM 5 · relay");
    expect(flyout()!.textContent).toContain("推荐配置");
    expect(flyout()!.querySelector('[role="slider"]')!.getAttribute("aria-valuetext")).toBe("模型默认");
    act(() => pill().click());
    start();
    expect(launched[0]).toMatchObject({ launcher: "relay", model: "glm-5" });
    expect(launched[0].effort).toBeUndefined();
  });

  it("an engine pill in a model's flyout picks the launcher that runs the same model with that engine", async () => {
    await open();
    await openPanel();
    await customize("model:relay\ngpt-5.5");
    const engines = [...flyout()!.querySelectorAll<HTMLButtonElement>("[data-engine-choice]")];
    expect(engines.map((b) => [b.dataset.engineChoice, b.getAttribute("aria-pressed")])).toEqual([["claude", "true"], ["codex", "false"]]);
    expect(engines[1].title).toBe("通过 codex 运行");
    act(() => engines[1].click());
    await flush();
    expect(panel()).toBeNull();
    expect(pill().title).toBe("Codex · GPT 5.5 · codex");
    expect(pill().querySelector("[data-pill-engine]")!.getAttribute("data-pill-engine")).toBe("codex");
    start();
    expect(launched[0]).toMatchObject({ launcher: "codex", model: "gpt-5.5" });
    expect(launched[0].effort).toBeUndefined();
  });

  it("keeps a pick on another computer that has its launcher", async () => {
    await open();
    await pick("model:relay\nglm-5");
    act(() => machineChip("Studio").click());
    await flush();
    expect(pill().title).toBe("Claude Code · GLM 5 · relay · 高");
    expect(q("[data-testid=new-agent-dropped]")).toBeNull();
    start();
    expect(launched[0]).toMatchObject({ machineId: "studio", cwd: "/code/studio-app", launcher: "relay", model: "glm-5", effort: "high" });
  });

  it("falls back to the default on a computer without the picked launcher, and says so under the box", async () => {
    await open();
    await pick("model:only-mac\ndefault");
    expect(pill().title).toContain("only-mac");
    act(() => machineChip("Studio").click());
    await flush();
    expect(q("[data-testid=new-agent-dropped]")!.textContent).toBe("Studio 上用不了 only-mac，已换回这台电脑的默认设置。");
    expect(pill().title).toBe("Claude Code · Opus 5.5 · claude · 高");
    // a pick on this computer takes the note away
    await pick("model:claude\nsonnet");
    expect(q("[data-testid=new-agent-dropped]")).toBeNull();
    await pick("model:relay\ndefault");
    start();
    expect(launched[0]).toMatchObject({ machineId: "studio", launcher: "relay" });
    expect(launched[0].model).toBeUndefined();
  });

  it("a pick goes on to the computer after the next one when that one's launchers were still being read", async () => {
    state.machines = [...state.machines, { id: "mini", name: "Mini", online: true }];
    localStorage.setItem("new.cwd.mini", "/code/mini-app");
    await open();
    await pick("model:relay\nglm-5");
    act(() => machineChip("Studio").click());
    act(() => machineChip("Mini").click());
    await flush();
    expect(pill().title).toBe("Claude Code · GLM 5 · relay · 高");
    start();
    expect(launched[0]).toMatchObject({ machineId: "mini", launcher: "relay", model: "glm-5" });
  });

  it("a computer whose launchers can't be read gets nothing of the model picked on the one before", async () => {
    failLaunchersOf = "studio";
    await open();
    await pick("model:claude\nsonnet");
    act(() => machineChip("Studio").click());
    await flush();
    start();
    expect(launched[0]).toMatchObject({ machineId: "studio", launcher: "" });
    expect(launched[0].model).toBeUndefined();
  });

  it("doesn't ask a launcher that can't start here for its list when the panel opens", async () => {
    LAUNCHERS.mac = [...LAUNCHERS.mac, { name: "broken", agent: "claude", health: { state: "blocked", kind: "auth" } }];
    try {
      await open();
      await openPanel();
      expect(asked.some((u) => u.includes("/launchers/relay/models"))).toBe(true);
      expect(asked.some((u) => u.includes("/launchers/broken/models"))).toBe(false);
      // still listed, its Default row off
      expect(row("model:broken\ndefault").getAttribute("aria-disabled")).toBe("true");
    } finally {
      LAUNCHERS.mac = LAUNCHERS.mac.filter((l) => l.name !== "broken");
    }
  });

  it("sends a model ID typed in the search (Enter picks its “Use …” row)", async () => {
    await open();
    await openPanel();
    const search = panel()!.querySelector<HTMLInputElement>("input")!;
    type(search, "acme/coder-9");
    expect(row("typed:claude\nacme/coder-9").textContent).toContain("使用「acme/coder-9」");
    key(search, "Enter");
    await flush();
    expect(panel()).toBeNull();
    start();
    expect(launched[0]).toMatchObject({ launcher: "claude", model: "acme/coder-9" });
  });

  it("leaves the permission mode and the folder alone when a model is picked", async () => {
    await open();
    act(() => radio("只做计划").click());
    type(folder(), "/code/other");
    await pick("model:claude\nsonnet");
    expect(radio("只做计划").getAttribute("aria-checked")).toBe("true");
    expect(folder().value).toBe("/code/other");
    start();
    expect(launched[0]).toMatchObject({ cwd: "/code/other", permissionMode: "plan", launcher: "claude", model: "sonnet" });
  });

  it("says under the box when the list of the launcher that runs can't be read, and still starts", async () => {
    failList = "claude";
    await open();
    expect(pill().dataset.modelPill).toBe("failed");
    expect(q("[data-testid=new-agent-models-failed]")!.textContent).toBe("没读到模型列表，仍可以用默认模型启动");
    start();
    expect(launched[0]).toMatchObject({ machineId: "mac", launcher: "" });
    expect(launched[0].model).toBeUndefined();
  });
});
