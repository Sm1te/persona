// @vitest-environment jsdom
// "New agent"'s recent setups (#2134): a start is remembered; the cards fill every field (computer, folder, launcher, model,
// permission mode, effort; the model pill and its panel since #5104) and never start; picking a computer brings back its
// model with the launcher it restores; an offline computer's card can't be picked; "Allow everything" is marked; the ⋯
// removes one or clears them; a sheet opened on a folder of another page's has no cards.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Machine } from "../lib/types";
import { loadRecentSetups, recentSetupsKey, type RecentSetup } from "../lib/recent-setups";

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

const EFFORTS = ["low", "medium", "high", "xhigh", "max"];
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const LISTS: Record<string, unknown> = {
  claude: { agent: "claude", models: [{ id: "default", name: "Default", default: true, efforts: EFFORTS }, { id: "opus", name: "Opus 5.5", efforts: EFFORTS }, { id: "sonnet", name: "Sonnet 5", efforts: EFFORTS }] },
  work: { agent: "claude", models: [{ id: "default", name: "Default", default: true, efforts: EFFORTS }, { id: "sonnet", name: "Sonnet 5", efforts: EFFORTS }] },
  codex: { agent: "codex", models: [{ id: "default", name: "Default", default: true }, { id: "gpt-5.5", name: "Codex 5.5" }] },
};
const setup = (s: Partial<RecentSetup>): RecentSetup => ({ machine: "mac", cwd: "/code/app", launcher: "claude", model: "", agent_type: "claude", mode: "", effort: "", at: "2026-11-09T03:00:00Z", ...s });
const seed = (entries: RecentSetup[]) => localStorage.setItem(recentSetupsKey(), JSON.stringify(entries));

let host: HTMLDivElement;
let root: Root;
const flush = () => act(async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); });
const q = <T extends HTMLElement = HTMLElement>(sel: string) => document.body.querySelector<T>(sel);
const button = (text: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === text);
const cards = () => [...document.body.querySelectorAll<HTMLButtonElement>("[data-recent-setup]")];
const card = (text: string) => cards().find((c) => c.textContent?.includes(text))!;
// a card's two lines: "folder · computer", then launcher · model · mode · effort
const lines = (c: HTMLElement) => [...c.children].map((line) => [...line.children].map((p) => p.textContent).join(line === c.firstElementChild ? " " : ""));
const radio = (name: string) => [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) => b.textContent?.trim() === name);
const machineChip = (name: string) => [...document.body.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === name && b.className.includes("h-8"))!;
const folder = () => q<HTMLInputElement>('input[placeholder]')!.value;
const textarea = () => q<HTMLTextAreaElement>("textarea")!;
// the model pill (its tooltip: "engine · model · launcher · effort") and its panel's rows, by key (`model:<launcher>\n<id>`)
const pill = () => q<HTMLButtonElement>("[data-testid=new-agent-model] [data-model-pill]")!;
const pillEffort = () => pill().querySelector("[data-pill-effort]")?.textContent ?? "";
const row = (key: string) => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")].find((r) => r.dataset.panelRow === key)!;
async function openPanel() {
  act(() => pill().click());
  await flush();
}
async function pick(key: string) {
  await openPanel();
  act(() => row(key).click());
  await flush();
}
/** One key on the effort slider of the open row flyout (the effort is the form's at once). */
async function slide(key: string) {
  const slider = q('[data-model-flyout] [role="slider"]')!;
  act(() => { slider.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true })); });
  await flush();
}
/** Opens the panel and the flyout of the row that runs now. */
async function customizeCurrent() {
  await openPanel();
  act(() => q('[data-panel-row][data-current] [data-row-customize]')!.click());
  await flush();
}

beforeEach(() => {
  resetModelListsForTests();
  state.machines = [
    { id: "mac", name: "MacBook", online: true },
    { id: "studio", name: "Studio", online: true },
    { id: "nas", name: "NAS", online: false },
  ];
  launched.length = 0;
  localStorage.clear();
  localStorage.setItem("new.machine", "mac");
  localStorage.setItem("new.cwd.mac", "/code/app");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes("/model-config/purposes/")) return json({ error: "not found" }, 404);
    const list = /\/launchers\/([^/]+)\/models/.exec(url);
    if (list) return json(LISTS[decodeURIComponent(list[1])] ?? { models: [] });
    if (url.includes("/launchers")) return json({ launchers: [{ name: "claude", agent: "claude" }, { name: "work", agent: "claude" }, { name: "codex", agent: "codex", agent_type: "codex" }] });
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

async function open(key = "a", preset?: { machineId?: string; cwd?: string }) {
  act(() => root.render(<NewAgent key={key} open preset={preset} onClose={() => {}} />));
  await flush();
}

describe("New agent's recent setups (#2134)", () => {
  it("shows no section before anything started, and remembers a start with the model and effort it sent", async () => {
    await open();
    expect(q('[data-testid="recent-setups"]')).toBeNull();
    await pick("model:claude\nopus");
    // the effort in the picked model's flyout: none set, then up three levels
    await customizeCurrent();
    for (const k of ["ArrowRight", "ArrowRight", "ArrowRight"]) await slide(k);
    expect(pillEffort()).toBe("高");
    act(() => pill().click());
    act(() => radio("只做计划")!.click());
    act(() => button("启动")!.click());
    expect(launched).toHaveLength(1);
    expect(loadRecentSetups()).toEqual([{ ...setup({ model: "opus", mode: "plan", effort: "high" }), at: expect.stringMatching(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/) }]);
  });

  it("remembers a Codex start with its launcher and model only", async () => {
    await open();
    act(() => radio("只做计划")!.click()); // a Claude Code choice that stays for this computer's next one
    await pick("model:codex\ngpt-5.5");
    act(() => button("启动")!.click());
    expect(loadRecentSetups()).toEqual([expect.objectContaining({ launcher: "codex", model: "gpt-5.5", agent_type: "codex", mode: "", effort: "" })]);
  });

  it("a card fills every field, marks itself, focuses the first message, and does not start", async () => {
    seed([
      setup({ machine: "mac", cwd: "/code/app", launcher: "claude", model: "", mode: "", effort: "" }),
      setup({ machine: "studio", cwd: "/code/notes-app", launcher: "work", model: "sonnet", mode: "bypassPermissions", effort: "xhigh" }),
    ]);
    await open();
    expect(cards()).toHaveLength(2);
    expect(lines(card("notes-app"))).toEqual(["notes-app · Studio", "work · sonnet · 全部放行 · 很高"]);
    expect(lines(card("app"))).toEqual(["app · MacBook", "claude"]);
    act(() => card("notes-app").click());
    await flush();
    expect(machineChip("Studio").className).toContain("bg-ink");
    expect(folder()).toBe("/code/notes-app");
    expect(pill().title).toBe("Claude Code · Sonnet 5 · work · 很高");
    expect(radio("全部放行")!.getAttribute("aria-checked")).toBe("true");
    expect(card("notes-app").getAttribute("aria-pressed")).toBe("true");
    expect(document.activeElement).toBe(textarea());
    expect(launched).toHaveLength(0);
    // the panel lights what the card put in
    await openPanel();
    expect(row("model:work\nsonnet").getAttribute("aria-selected")).toBe("true");
    act(() => pill().click());
    // the form changed by hand: no longer that card
    await customizeCurrent();
    await slide("ArrowLeft");
    expect(pillEffort()).toBe("高");
    expect(card("notes-app").getAttribute("aria-pressed")).toBe("false");
    // Start is the user's: it starts what the form holds
    await slide("ArrowRight");
    act(() => pill().click());
    act(() => button("启动")!.click());
    expect(launched[0]).toMatchObject({ machineId: "studio", cwd: "/code/notes-app", launcher: "work", model: "sonnet", effort: "xhigh", permissionMode: "bypassPermissions" });
  });

  it("a card on the same computer fills it in place", async () => {
    seed([setup({ cwd: "/code/other", launcher: "codex", model: "gpt-5.5", agent_type: "codex" })]);
    await open();
    expect(lines(card("other"))).toEqual(["other · MacBook", "codex · gpt-5.5"]);
    act(() => card("other").click());
    await flush();
    expect(folder()).toBe("/code/other");
    expect(pill().title).toBe("Codex · Codex 5.5 · codex");
    expect(pill().querySelector("[data-pill-engine]")!.getAttribute("data-pill-engine")).toBe("codex");
    expect(card("other").getAttribute("aria-pressed")).toBe("true");
    expect(launched).toHaveLength(0);
  });

  it("picking a computer brings back its model with the launcher it restores; another launcher starts from the default", async () => {
    localStorage.setItem("new.launcher.studio", "claude");
    seed([
      setup({ machine: "studio", launcher: "codex", model: "gpt-5.5", agent_type: "codex" }),
      setup({ machine: "studio", launcher: "claude", model: "opus" }),
      setup({ machine: "studio", launcher: "claude", model: "sonnet" }),
    ]);
    await open();
    expect(pill().title).toBe("Claude Code · Default · claude"); // the Mac has no recent setup: the default
    act(() => machineChip("Studio").click());
    await flush();
    expect(pill().title).toBe("Claude Code · Opus 5.5 · claude");
    await pick("model:work\ndefault");
    expect(pill().title).toBe("Claude Code · Default · work");
    act(() => machineChip("MacBook").click());
    await flush();
    expect(pill().title).toContain("Default");
  });

  it("restores the model on open too", async () => {
    localStorage.setItem("new.launcher.mac", "claude");
    seed([setup({ model: "sonnet" })]);
    await open();
    expect(pill().title).toBe("Claude Code · Sonnet 5 · claude");
  });

  it("an offline computer's card is dimmed, says so, and can't be picked", async () => {
    seed([setup({ machine: "nas", cwd: "/data/backup", launcher: "codex", model: "gpt-5.5", agent_type: "codex" }), setup({ machine: "gone", cwd: "/code/old" })]);
    await open();
    expect(cards()).toHaveLength(1); // a computer the hub no longer lists has none
    const offline = card("backup");
    expect(offline.disabled).toBe(true);
    expect(lines(offline)).toEqual(["backup · NAS", "codex · gpt-5.5 · 离线"]);
    act(() => offline.click());
    await flush();
    expect(machineChip("MacBook").className).toContain("bg-ink");
    expect(folder()).toBe("/code/app");
    expect(offline.getAttribute("aria-pressed")).toBe("false");
  });

  it("marks Allow everything with the warning", async () => {
    seed([setup({ mode: "bypassPermissions" }), setup({ cwd: "/code/b", mode: "plan" })]);
    await open();
    const bypass = card("app");
    expect(bypass.dataset.bypass).toBe("true");
    const mark = [...bypass.querySelectorAll("span")].find((s) => s.textContent === "全部放行" && s.className.includes("text-orange"))!;
    expect(mark.querySelector("svg")).not.toBeNull();
    expect(cards()[1].dataset.bypass).toBeUndefined();
  });

  it("shows at most three, removes one and clears them all from the ⋯", async () => {
    seed(["a", "b", "c", "d"].map((x) => setup({ cwd: `/code/${x}` })));
    await open();
    expect(cards().map((c) => lines(c)[0])).toEqual(["a · MacBook", "b · MacBook", "c · MacBook"]);
    act(() => q<HTMLButtonElement>('button[aria-label="最近配置选项"]')!.click());
    act(() => [...document.body.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((r) => r.textContent?.includes("从最近中移除"))!.click());
    await flush();
    act(() => [...document.body.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((r) => r.textContent?.trim() === "b · MacBook")!.click());
    await flush();
    expect(cards().map((c) => lines(c)[0])).toEqual(["a · MacBook", "c · MacBook", "d · MacBook"]);
    expect(loadRecentSetups().map((e) => e.cwd)).toEqual(["/code/a", "/code/c", "/code/d"]);
    act(() => q<HTMLButtonElement>('button[aria-label="最近配置选项"]')!.click());
    act(() => [...document.body.querySelectorAll<HTMLElement>('[role="menuitem"]')].find((r) => r.textContent?.includes("清除最近配置"))!.click());
    await flush();
    expect(q('[data-testid="recent-setups"]')).toBeNull();
    expect(loadRecentSetups()).toEqual([]);
  });

  it("has no cards when another page opened it on a folder of its own", async () => {
    seed([setup({ cwd: "/code/b" })]);
    await open("p", { machineId: "studio", cwd: "/code/space" });
    expect(q('[data-testid="recent-setups"]')).toBeNull();
    expect(folder()).toBe("/code/space");
  });
});
