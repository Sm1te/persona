// @vitest-environment jsdom
// The wizard's side of #5193: "Found on your computers" on its first step (only on a hub that has it and with aliases to
// show; picking one hands it to the import), and a stored provider opened with what a paste brought: the models it
// lacks, checked, at "Choose models", or the pasted key at "Connect", saved only on Save.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../../i18n";
import type { EditDraft, FoundAlias } from "../../lib/provider-paste";
import type { ProviderView } from "../../lib/providers";
import { resetReadiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; body?: Record<string, unknown> };
const FOUND: FoundAlias[] = [
  { name: "cc_team", base_url: "https://gw.example.com", machine: "laptop", computer: "Laptop", online: true },
  { name: "cc_relay", base_url: "https://relay.example.org", machine: "desk", computer: "Desk", online: false },
];
const STORED: ProviderView = { name: "gw_full", display_name: "Team gateway", base_url: "https://gw.example.com", auth: "bearer", has_key: true, model: "example-5", models: [{ id: "example-5" }] };

let calls: Call[];
let host: HTMLDivElement;
let root: Root;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const text = () => document.body.textContent ?? "";
const button = (label: string) => {
  const found = [...document.body.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  expect(found, label).toBeDefined();
  return found!;
};
const foundList = () => document.body.querySelector("section ul");
const put = () => calls.find((c) => c.method === "PUT");

async function draw(existing: ProviderView | "new", props: { found?: FoundAlias[]; draft?: EditDraft | null; onImportAlias?: (a: FoundAlias) => void } = {}, features = ["builtin_presets", "launcher_auto_name", "launcher_import_parse"]) {
  Object.assign(getState(), { features });
  act(() => root.render(<ProviderWizard existing={existing} onClose={() => {}} onSaved={() => {}} {...props} />));
  await flush();
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  calls = [];
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/assistant/readiness")) return Promise.resolve(json({ error: "not found" }, 404));
    if (call.path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets: [] }));
    if (call.path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: [] }));
    if (call.path.endsWith("/launchers/test")) return Promise.resolve(json({ ok: true, ms: 90, model: "example-5" }));
    if (call.method === "PUT") return Promise.resolve(json({ launcher: STORED }));
    return Promise.resolve(json({}));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("found on your computers", () => {
  it("lists the aliases with their host and computer, offline ones said so, and hands a picked one to the import", async () => {
    const picked: FoundAlias[] = [];
    await draw("new", { found: FOUND, onImportAlias: (a) => picked.push(a) });
    expect(text()).toContain(t("settings-providerwizard.found_title"));
    const rows = [...foundList()!.querySelectorAll("li")];
    expect(rows.map((li) => li.querySelector(".font-mono")?.textContent)).toEqual(["cc_team", "cc_relay"]);
    expect(rows[0]!.textContent).toContain("gw.example.com");
    expect(rows[0]!.textContent).toContain(t("settings-providerwizard.found_on", { computer: "Laptop" }));
    expect(rows[1]!.textContent).toContain(t("settings-providerwizard.found_on_offline", { computer: "Desk" }));
    act(() => rows[1]!.querySelector("button")!.click());
    expect(picked).toEqual([FOUND[1]]);
  });

  it("shows no group with nothing found, nor on a hub without the feature", async () => {
    await draw("new", { found: [] });
    expect(text()).not.toContain(t("settings-providerwizard.found_title"));
    expect(text()).toContain(t("settings-providerwizard.paste_title"));
    act(() => root.unmount());
    root = createRoot(host);
    await draw("new", { found: FOUND }, ["builtin_presets", "launcher_auto_name"]);
    expect(text()).not.toContain(t("settings-providerwizard.found_title"));
    expect(text()).not.toContain(t("settings-providerwizard.paste_title"));
    // the list is what it was: no rows other than the custom gateway
    expect(document.body.querySelectorAll("ul li")).toHaveLength(1);
  });
});

describe("a stored provider opened from a paste", () => {
  it("opens at Choose models with the models it lacks, checked, and saves them only on Save", async () => {
    await draw(STORED, { draft: { step: "models", models: [{ id: "example-5" }, { id: "example-5-air", context_window: 64000 }] } });
    expect(document.body.querySelector('[aria-current="step"]')!.textContent).toContain(t("settings-providerwizard.step_models"));
    const rows = [...document.body.querySelectorAll("li[data-model]")].map((li) => [li.getAttribute("data-model"), li.querySelector('[role="checkbox"]')!.getAttribute("aria-checked")]);
    expect(rows).toEqual([["example-5", "true"], ["example-5-air", "true"]]);
    expect(put()).toBeUndefined();
    act(() => button(t("common.save")).click());
    await flush();
    expect(put()!.path).toMatch(/\/launchers\/gw_full$/);
    expect(put()!.body!.models).toEqual([{ id: "example-5" }, { id: "example-5-air", context_window: 64000 }]);
    expect(put()!.body!.api_key).toBeUndefined();
  });

  it("opens at Connect with the pasted key, checks it by itself, and saves it on Save", async () => {
    await draw(STORED, { draft: { step: "connect", api_key: "sk-example-new" } });
    expect(document.body.querySelector('[aria-current="step"]')!.textContent).toContain(t("settings-providerwizard.step_connect"));
    expect(document.body.querySelector<HTMLInputElement>('input[type="password"]')!.value).toBe("sk-example-new");
    await act(async () => { await vi.advanceTimersByTimeAsync(700); });
    await flush();
    expect(calls.find((c) => c.path.endsWith("/launchers/discover"))!.body).toMatchObject({ name: "gw_full", api_key: "sk-example-new" });
    act(() => button(t("common.save")).click());
    await flush();
    expect(put()!.body).toMatchObject({ api_key: "sk-example-new", base_url: "https://gw.example.com" });
    expect(put()!.body!.models).toBeUndefined();
  });

  it("opens as before without one", async () => {
    await draw(STORED);
    expect(document.body.querySelector('[aria-current="step"]')!.textContent).toContain(t("settings-providerwizard.step_connect"));
    expect(document.body.querySelector<HTMLInputElement>('input[type="password"]')!.value).toBe("");
  });
});
