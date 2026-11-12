// @vitest-environment jsdom
// "New agent" with a model ID typed by hand (#1644), in the model panel (#5104): a search that names no listed model
// offers "Use “…”", and the pick survives the lists being read again (each opening reads them) and goes with the start;
// a launcher that fixes its own --model offers its Default row alone and no typed ID; the IDs typed before can be
// forgotten (off at once, back where they were when the hub says no).
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
const models = () => ({ agent: "claude", models: [
  { id: "default", name: "Default", default: true },
  { id: "opus", name: "Opus 5.5" },
  { id: "vendor/model-x", name: "vendor/model-x", description: "之前用过的模型 ID", custom: true },
] });
let host: HTMLDivElement;
let root: Root;
let forgetStatus = 200;
let listBody: () => unknown = models;
const requests: { method: string; url: string }[] = [];
const flush = () => act(async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); });
const button = (text: string) => [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === text)!;
const pill = () => document.body.querySelector<HTMLButtonElement>("[data-testid=new-agent-model] [data-model-pill]")!;
const panel = () => document.body.querySelector<HTMLElement>("[data-model-panel]");
const search = () => panel()!.querySelector<HTMLInputElement>("input")!;
const rows = () => [...document.body.querySelectorAll<HTMLElement>("[data-panel-row]")];
const row = (key: string) => rows().find((r) => r.dataset.panelRow === key);
const listReads = () => requests.filter((r) => r.url.includes("/launchers/claude/models")).length;

function type(el: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
  act(() => {
    setter.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

beforeEach(() => {
  resetModelListsForTests();
  state.machines = [{ id: "mac", name: "Mac", online: true }];
  launched.length = 0;
  requests.length = 0;
  forgetStatus = 200;
  listBody = models;
  localStorage.clear();
  localStorage.setItem("new.cwd.mac", "/code/app");
  vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    requests.push({ method: init?.method ?? "GET", url });
    if (url.includes("/model-config/purposes/")) return json({ error: "not found" }, 404);
    if (url.includes("/models/custom")) return forgetStatus === 200 ? json({ ok: true }) : json({ error: "数据库暂时不可用" }, forgetStatus);
    if (url.includes("/launchers/") && url.includes("/models")) return json(listBody());
    if (url.includes("/launchers")) return json({ launchers: [{ name: "claude", agent: "claude" }] });
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

describe("New agent: a typed model ID (#1644)", () => {
  it("keeps the typed pick when the lists are read again, and starts with it", async () => {
    await open();
    await openPanel();
    type(search(), "acme/coder-9");
    const use = row("typed:claude\nacme/coder-9")!;
    expect(use.textContent).toContain("使用「acme/coder-9」");
    act(() => use.click());
    await flush();
    expect(panel()).toBeNull();
    expect(pill().title).toContain("coder-9");
    // the next opening reads the list again: the ID isn't in it, and stays the pick, on a row of its own
    const reads = listReads();
    await openPanel();
    expect(listReads()).toBe(reads + 1);
    expect(row("model:claude\nacme/coder-9")!.getAttribute("aria-selected")).toBe("true");
    act(() => pill().click());
    expect(pill().title).toContain("coder-9");
    act(() => button("启动").click());
    expect(launched[0]).toMatchObject({ machineId: "mac", launcher: "claude", model: "acme/coder-9" });
  });

  it("offers only the Default row, with why, for a launcher that passes its own --model, also with an empty list", async () => {
    listBody = () => ({ agent: "claude", models: [], locked: "启动器固定了模型参数" });
    await open();
    await openPanel();
    expect(rows().map((r) => r.dataset.panelRow)).toEqual(["model:claude\ndefault"]);
    // the provider's own view describes each row: the Default row says why no model can be picked
    act(() => document.body.querySelector<HTMLButtonElement>('[data-rail-cell="claude"]')!.click());
    expect(row("model:claude\ndefault")!.textContent).toContain("启动器固定了模型参数");
    // and a search for an ID offers no "Use …"
    type(search(), "acme/coder-9");
    expect(rows().some((r) => r.dataset.panelRow?.startsWith("typed:"))).toBe(false);
    act(() => pill().click());
    act(() => button("启动").click());
    expect(launched[0]?.model).toBeUndefined();
  });

  it("forgets an ID typed before at once, and puts it back with why when the hub says no", async () => {
    forgetStatus = 500;
    await open();
    await openPanel();
    const forget = () => document.body.querySelector<HTMLButtonElement>('button[aria-label="忘记「vendor/model-x」"]');
    act(() => forget()!.click());
    expect(forget()).toBeNull(); // gone before the hub answers
    await flush();
    const del = requests.find((r) => r.method === "DELETE")!;
    expect(del.url).toContain("/machines/mac/launchers/claude/models/custom?model=vendor%2Fmodel-x");
    expect(forget()).not.toBeNull(); // back
    expect(document.body.textContent).toMatch(/没能忘记「vendor\/model-x」：.*数据库暂时不可用/);
  });
});
