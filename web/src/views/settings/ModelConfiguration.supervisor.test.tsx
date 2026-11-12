// @vitest-environment jsdom
// Model settings lists the background supervisor's purpose (#5082) right after Main chat, in Main chat's group, with what
// it is for. The hub answers it by its own id and leaves it out of the catalog; a hub without it lists nothing new.
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
vi.mock("./ConfigurationManager", () => ({ ConfigurationManager: ({ children }: { children: (id: string) => unknown }) => children("assistant") }));
vi.mock("./ModelProfiles", () => ({ ModelProfiles: () => null }));
vi.mock("../../lib/store", () => ({ useStore: (select: (s: { machines: [] }) => unknown) => select({ machines: [] }) }));
vi.mock("../../ui/atoms", async (original) => ({ ...(await original<typeof import("../../ui/atoms")>()), toast: vi.fn() }));
import { initI18n } from "../../i18n";
import type { ModelProvider, ModelPurpose } from "../../lib/model-config";
import { ModelConfiguration } from "./ModelConfiguration";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const providers: ModelProvider[] = [{ id: "launcher.default", name: "Default launcher", kind: "launcher" }];
const base = { group: "Default models", kind: "launcher" as const, revision: 1, override: {}, effective: { provider: "launcher.default", model: "default-model" }, fields: ["provider", "model", "effort"] };
const main: ModelPurpose = { ...base, id: "assistant", category_id: "assistant", name: "Main chat", launch: "headless" };
const agents: ModelPurpose = { ...base, id: "agent.new", category_id: "agent.new", name: "Agents", launch: "engine" };
const supervise: ModelPurpose = { ...base, id: "assistant.supervise", category_id: "assistant", parent: "assistant", name: "Background supervisor", group: "Assistant", launch: "headless", inherited_from: "Main chat" };

/** What the hub does with the purpose: answers it by id (as built, #4927), also lists it, or has none (an older hub). */
let mode: "detail" | "listed" | "none" | "failing";
let patches: Record<string, unknown>[];
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
const hub = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const path = String(input).split("?")[0];
  if (init?.method === "PATCH") {
    patches.push(JSON.parse(String(init.body)));
    return json({ purpose: { ...supervise, override: { model: "small-model" }, effective: { ...supervise.effective, model: "small-model" }, revision: 2 }, providers });
  }
  if (path.endsWith("/model-config")) return json({ purposes: mode === "listed" ? [main, supervise, agents] : [main, agents], providers });
  if (path.endsWith("/purposes/assistant.supervise")) return mode === "none" ? json({ error: "not found" }, 404) : mode === "failing" ? json({ error: "hub busy" }, 503) : json({ purpose: supervise, providers });
  if (path.endsWith("/models")) return json({ error: "unavailable" }, 404);
  throw new Error(`Unexpected request: ${path}`);
};
let host: HTMLDivElement;
let root: Root;
const flush = async () => { for (let i = 0; i < 8; i++) await act(async () => { await Promise.resolve(); }); };
const rows = () => [...host.querySelectorAll<HTMLElement>("[data-model-purpose]")];
async function render() {
  function Harness() {
    const [open, onOpen] = useState<string | null>(null);
    return <ModelConfiguration open={open} onOpen={onOpen} onProviders={() => {}} />;
  }
  await act(async () => root.render(<Harness />));
  await flush();
}
beforeEach(async () => {
  await initI18n("en");
  mode = "detail";
  patches = [];
  vi.stubGlobal("fetch", vi.fn(hub));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  await initI18n("zh-Hans");
});

describe("the background supervisor's model purpose (#5082)", () => {
  it("is the row after Main chat, in its group, following it, with what it is for", async () => {
    await render();
    expect(rows().map((r) => r.dataset.modelPurpose)).toEqual(["assistant", "assistant.supervise", "agent.new"]);
    const [mainRow, row] = rows();
    expect(row.parentElement).toBe(mainRow.parentElement); // one group
    expect(row.textContent).toContain("Background supervisor");
    expect(row.textContent).toContain("Follows Main chat");
    expect(row.querySelector('[data-testid="purpose-description"]')?.textContent).toContain("Follows up and reviews the tasks Persona hands out");
    expect(mainRow.querySelector('[data-testid="purpose-description"]')).toBeNull();
  });

  it("opens its own editor and saves its own record", async () => {
    await render();
    await act(async () => [...rows()[1].querySelectorAll("button")].find((b) => b.textContent?.includes("Background supervisor"))!.click());
    await flush();
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]')!;
    expect(dialog.textContent).toContain("Background supervisor");
    await act(async () => [...dialog.querySelectorAll("button")].find((b) => b.textContent === "Save")!.click());
    await flush();
    expect(patches).toEqual([{ revision: 1, provider: null, model: null, effort: null }]);
    expect(vi.mocked(fetch).mock.calls.some(([input, init]) => (init as RequestInit | undefined)?.method === "PATCH" && String(input).endsWith("/model-config/purposes/assistant.supervise"))).toBe(true);
  });

  it("is listed once when the hub's catalog has it itself", async () => {
    mode = "listed";
    await render();
    expect(rows().map((r) => r.dataset.modelPurpose)).toEqual(["assistant", "assistant.supervise", "agent.new"]);
  });

  it("has no row on a hub before it, and the rest loads as before", async () => {
    mode = "none";
    await render();
    expect(rows().map((r) => r.dataset.modelPurpose)).toEqual(["assistant", "agent.new"]);
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("says the page failed when the purpose could not be read, not that the hub has none", async () => {
    mode = "failing";
    await render();
    expect(rows()).toEqual([]);
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("hub busy");
  });
});
