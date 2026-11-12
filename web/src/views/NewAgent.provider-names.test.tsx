// @vitest-environment jsdom
// "New agent" names a model provider by its display name (#5029): in the model panel (#5104) its rail cell, its group and
// the pill by the title alone; "Follow Model settings" says the default's provider by its title; the note under the box
// for a launcher that can't start here and its help have the startup ID beside the name (in mono), the preset note
// "Title (id)", a recent setup's card the ID in its tooltip. An ID this computer's list doesn't have is looked up in the
// model configuration's providers. An rc alias has no display name and reads as before (its ID, in mono where the ID is
// shown).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Launcher, Machine } from "../lib/types";
import { recentSetupsKey, type RecentSetup } from "../lib/recent-setups";

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

import { NewAgent } from "./NewAgent";
import { resetModelListsForTests } from "../lib/session-models";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const LAUNCHERS: Launcher[] = [
  { name: "relay-example-com", display_name: "我的中转 站", agent: "claude", model: "glm-5" },
  { name: "cc_glm", agent: "claude" },
  { name: "team-relay", display_name: "Team relay", health: { state: "blocked", kind: "not_in_terminal", checked_at: "2026-11-09T08:00:00Z" } },
];
const PURPOSE = {
  purpose: { id: "agent.new", effective: { provider: "launcher.relay-example-com" } },
  providers: [
    { id: "launcher.relay-example-com", name: "我的中转 站", kind: "launcher" },
    { id: "launcher.elsewhere", name: "Elsewhere relay", kind: "launcher" },
  ],
};
const setup = (s: Partial<RecentSetup>): RecentSetup => ({ machine: "mac", cwd: "/code/app", launcher: "", model: "", agent_type: "claude", mode: "", effort: "", at: "2026-11-09T03:00:00Z", ...s });

let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); });
const q = (sel: string) => document.body.querySelector<HTMLElement>(sel);
const card = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>("[data-recent-setup]")].find((c) => c.textContent?.includes(text))!;
const button = (text: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === text)!;
const pill = () => q("[data-testid=new-agent-model] [data-model-pill]")!;
const rows = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")];
const row = (key: string) => rows().find((r) => r.dataset.panelRow === key)!;
const group = (name: string) => [...document.body.querySelectorAll<HTMLElement>("[data-panel-section]")].find((g) => g.getAttribute("aria-label") === name);
async function openPanel() {
  act(() => pill().click());
  await flush();
}

beforeEach(() => {
  resetModelListsForTests();
  state.machines = [{ id: "mac", name: "Mac", online: true }];
  localStorage.clear();
  localStorage.setItem("new.machine", "mac");
  localStorage.setItem("new.cwd.mac", "/code/app");
  localStorage.setItem(recentSetupsKey(), JSON.stringify([setup({ launcher: "elsewhere" }), setup({ cwd: "/code/web", launcher: "cc_glm" })]));
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return json(PURPOSE);
    if (url.includes("/launchers/") && url.includes("/models")) return json({ models: [] });
    if (url.includes("/launchers")) {
      return json({
        launchers: LAUNCHERS,
        default: { provider: "relay-example-com", model: "glm-5" },
        tiers: [{ name: "quick", launcher: "relay-example-com", model: "glm-5" }, { name: "alias", launcher: "cc_glm" }],
        tiers_profile: { id: "p", name: "Daily" },
      });
    }
    return json({ path: "/code", entries: [] });
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<NewAgent open onClose={() => {}} />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
});

describe("New agent: provider display names (#5029)", () => {
  // The launcher chips are gone (#5104): the panel draws every provider by its title, as Cindy's does, with no startup ID
  // beside it (the tooltips and the monospace alias that were the chips' are no longer there).
  it("titles each launcher by its display name in the panel and the pill; an rc alias by its ID", async () => {
    await flush();
    expect(pill().title).toContain("我的中转 站");
    expect(pill().title).not.toContain("relay-example-com");
    await openPanel();
    const cells = [...document.body.querySelectorAll<HTMLElement>("[data-rail-cell]")].filter((c) => !["recent", "favorites", "all"].includes(c.dataset.railCell!));
    expect(cells.map((c) => [c.dataset.railCell, c.getAttribute("aria-label")])).toEqual([
      ["relay-example-com", "我的中转 站"],
      ["cc_glm", "cc_glm"],
      ["team-relay", "Team relay"],
    ]);
    // the startup ID is secondary text (#5029): the rail cell's tooltip, small and monospace in the group's header
    expect(cells.map((c) => c.title)).toEqual(["我的中转 站 (relay-example-com)", "cc_glm", "Team relay (team-relay)"]);
    expect(group("我的中转 站")).toBeDefined();
    expect(group("cc_glm")).toBeDefined();
    expect(group("relay-example-com")).toBeUndefined();
    // a row's second line is its provider, by its title
    expect(row("model:cc_glm\ndefault").textContent).toContain("cc_glm");
  });

  // The "Default: …" chip went with the chips, and its ID tooltip with it: the follow row says the provider by its title.
  it("names the default's provider by its title in Follow Model settings", async () => {
    await flush();
    await openPanel();
    const follow = q("[data-follow-row]")!;
    expect(follow.getAttribute("aria-selected")).toBe("true");
    expect(follow.textContent).toBe("跟随模型配置我的中转 站 · glm-5");
  });

  it("names a blocked launcher by its title in its group, its ID beside it in mono in the note and its help", async () => {
    await flush();
    await openPanel();
    const blocked = group("Team relay")!;
    expect(blocked.textContent).toContain("这台电脑的终端里用不了");
    act(() => [...blocked.querySelectorAll("button")].find((b) => b.textContent === "怎么修好")!.click());
    const help = q('[data-testid="launcher-blocked-help"]')!;
    expect(help.textContent).toContain("Team relay");
    expect(help.textContent).toContain("team-relay");
    expect(help.textContent).toContain("「Team relay」");
    // tried anyway: the launcher that runs this time is said under the box, its ID beside its title
    act(() => button("仍然使用").click());
    const note = q('[data-testid="new-agent-blocked"]')!;
    const span = (text: string) => [...note.querySelectorAll("span")].find((s) => s.textContent === text)!;
    const [title, id] = [span("Team relay"), span("team-relay")];
    expect([title.textContent, title.className.includes("font-mono")]).toEqual(["Team relay", false]);
    expect([id.textContent, id.className.includes("font-mono")]).toEqual(["team-relay", true]);
    expect(note.querySelector("button")!.getAttribute("aria-label")).toBe("怎么修好 Team relay");
  });

  // The preset chips' "Title (id)" tooltip is gone: a preset row says its launcher by its title (as every panel row
  // does), and the note under the box once it is picked keeps "Title (id)".
  it("says a picked preset's launcher as \"Title (id)\", an rc alias's as its ID", async () => {
    await flush();
    await openPanel();
    expect(row("preset:quick").textContent).toContain("我的中转 站 · glm-5");
    expect(row("preset:alias").textContent).toContain("cc_glm");
    act(() => row("preset:quick").click());
    await flush();
    expect(q('[data-testid="new-agent-preset-hint"]')!.textContent).toBe("预设 quick · 我的中转 站 (relay-example-com) · glm-5");
    await openPanel();
    act(() => row("preset:alias").click());
    await flush();
    expect(q('[data-testid="new-agent-preset-hint"]')!.textContent).toBe("预设 alias · cc_glm");
  });

  it("titles a recent setup's launcher from the model configuration when this computer doesn't have it", async () => {
    await flush();
    const away = card("Elsewhere relay");
    expect(away.title).toBe("/code/app\nelsewhere");
    const alias = card("cc_glm");
    expect(alias.title).toBe("/code/web");
    expect([...alias.querySelectorAll(".font-mono")].map((s) => s.textContent)).toEqual(["cc_glm"]);
  });
});
