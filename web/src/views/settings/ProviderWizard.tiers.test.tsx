// @vitest-environment jsdom
// "Choose models" for a Claude Code gateway (#4975): the tiers summary fills itself in from the checked models and says
// so, Adjust edits it in place, a lighter model is offered when the Haiku tier lands on Opus, a saved provider changes
// only when a suggestion is applied, and a model typed by hand is looked up in the hub's catalog.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../../i18n";
import type { ProviderView } from "../../lib/providers";
import { resetReadiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const OPUS = "claude-opus-5-5";
const SONNET = "claude-sonnet-5-5";
const HAIKU = "claude-haiku-5-5";
type Call = { method: string; path: string; body?: Record<string, unknown> };
let calls: Call[];
let catalog: Record<string, unknown>[] | null;
// set: the catalog answers only when this is resolved
let catalogGate: Promise<void> | null;
let host: HTMLDivElement;
let root: Root;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const text = () => document.body.textContent ?? "";
function button(label: string) {
  const found = [...document.body.querySelectorAll("button")].find((el) => el.textContent?.trim().startsWith(label));
  expect(found, label).toBeDefined();
  return found!;
}
const click = (label: string) => act(() => button(label).click());
const hasButton = (label: string) => [...document.body.querySelectorAll("button")].some((el) => el.textContent?.trim() === label);
/** A step in the wizard's step list (its number, then its title). */
const toStep = (title: string) => act(() => [...document.body.querySelectorAll("ol button")].find((el) => el.textContent?.includes(title))!.dispatchEvent(new MouseEvent("click", { bubbles: true })));
function input(selector: string) {
  const found = document.body.querySelector<HTMLInputElement>(selector);
  expect(found, selector).not.toBeNull();
  return found!;
}
function type(el: HTMLInputElement, value: string) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
const tierSelect = (label: string) => input(`select[aria-label="${label}"]`) as unknown as HTMLSelectElement;
function choose(label: string, value: string) {
  const el = tierSelect(label);
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("change", { bubbles: true }));
  });
}
const summary = () => document.body.querySelector("section dl")?.textContent ?? "";
const posted = () => calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"))?.body;
const put = () => calls.find((c) => c.method === "PUT")?.body;

async function draw(existing: ProviderView | "new") {
  act(() => root.render(<ProviderWizard existing={existing} onClose={() => {}} onSaved={() => {}} />));
  await flush();
}
/** A new custom gateway whose model list is Opus, Sonnet and Haiku, checked by itself, on "Choose models". */
async function newGateway() {
  await draw("new");
  click(t("settings-providerwizard.custom"));
  type(input('input[placeholder*="my_gateway"]'), "gw");
  type(input('input[inputmode="url"]'), "https://gw.example.com");
  act(() => { input('input[inputmode="url"]').focus(); input('input[inputmode="url"]').blur(); });
  type(input('input[type="password"]'), "sk-example-key");
  await act(async () => { await vi.advanceTimersByTimeAsync(700); });
  await flush();
  click(t("settings-providerwizard.next"));
  await flush();
}
async function save() {
  click(t("common.save"));
  await flush();
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  calls = [];
  catalog = [];
  catalogGate = null;
  Object.assign(getState(), { features: ["builtin_presets"] });
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets: [] }));
    if (call.path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: [{ id: OPUS }, { id: SONNET }, { id: HAIKU }] }));
    if (call.path.endsWith("/launchers/test")) return Promise.resolve(json({ ok: true, ms: 120, model: OPUS }));
    if (call.path.endsWith("/launchers/catalog")) return (catalogGate ?? Promise.resolve()).then(() => (catalog ? json({ models: catalog }) : json({ error: "not found" }, 404)));
    if (call.path.endsWith("/launchers") && call.method === "POST") return Promise.resolve(json({ launcher: { name: "gw" } }));
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

describe("Claude Code tiers on Choose models", () => {
  it("fills the tiers in from the checked models, marks them Auto, and saves them", async () => {
    await newGateway();
    expect(text()).toContain(t("settings-providerwizard.tiers_title"));
    expect(summary()).toContain(SONNET);
    expect(summary()).toContain(HAIKU);
    expect(summary()).toContain(t("settings-providerwizard.tier_auto"));
    expect(text()).toContain(t("settings-providerwizard.tiers_auto_note"));
    // the Opus tier is the default model already: left empty, said so
    expect(summary()).toContain(t("settings-providerwizard.tier_is_default"));
    expect(hasButton(t("settings-providerwizard.tiers_apply"))).toBe(false);
    // the default model is offered as "the session's model", not again by name (empty already runs it)
    click(t("settings-providerwizard.tiers_adjust"));
    expect([...tierSelect("Opus").querySelectorAll("option")].map((o) => o.value)).toEqual(["", SONNET, HAIKU]);
    await save();
    expect(posted()).toMatchObject({ model: OPUS, sonnet_model: SONNET, small_model: HAIKU });
    expect(posted()).not.toHaveProperty("opus_model");
  });

  it("keeps what the user picks in Adjust, and offers the lighter model when Haiku lands on Opus", async () => {
    await newGateway();
    click(t("settings-providerwizard.tiers_adjust"));
    expect(button(t("settings-providerwizard.tiers_close")).getAttribute("aria-expanded")).toBe("true");
    choose(t("settings-providerwizard.tier_light"), "");
    choose("Sonnet", HAIKU);
    // a change of default afterwards leaves both as picked
    const sonnetRow = document.body.querySelector(`[data-model="${SONNET}"]`)!;
    act(() => [...sonnetRow.querySelectorAll("button")].find((b) => b.textContent === t("settings-providerwizard.set_default"))!.click());
    await flush();
    expect(tierSelect("Sonnet").value).toBe(HAIKU);
    expect(tierSelect(t("settings-providerwizard.tier_light")).value).toBe("");
    // the Opus tier, still automatic, followed the new default
    expect(tierSelect("Opus").value).toBe(OPUS);
    click(t("settings-providerwizard.set_default"));
    await flush();
    // back on Opus as the default: Haiku / small model now runs on it while Haiku is checked
    const hint = text();
    expect(hint).toContain(t("settings-providerwizard.tiers_same_as_opus", { model: OPUS, list: t("settings-providerwizard.tier_pair", { tier: t("settings-providerwizard.tier_light"), model: HAIKU }) }));
    click(t("settings-providerwizard.tiers_apply"));
    expect(tierSelect(t("settings-providerwizard.tier_light")).value).toBe(HAIKU);
    expect(hasButton(t("settings-providerwizard.tiers_apply"))).toBe(false);
    await save();
    expect(posted()).toMatchObject({ model: OPUS, sonnet_model: HAIKU, small_model: HAIKU });
  });

  it("looks a model typed by hand up in the hub's catalog, and a tier fills itself in from it", async () => {
    catalog = [{ id: "claude-fable-5-1", name: "Fable 5.1", context_window: 1000000, efforts: ["low", "medium", "high"], default_effort: "medium" }];
    await newGateway();
    type(input(`[aria-label="${t("settings-providerwizard.add_model_id")}"]`), "claude-fable-5-1");
    click(t("settings-providerwizard.add"));
    await flush();
    expect(calls.find((c) => c.path.endsWith("/launchers/catalog"))?.body).toEqual({ ids: ["claude-fable-5-1"] });
    expect(input(`input[aria-label="${t("settings-providerwizard.context_window", { id: "claude-fable-5-1" })}"]`).value).toBe("1000000");
    expect(text()).toContain("Fable 5.1");
    await save();
    const fable = (posted()!.models as Record<string, unknown>[]).find((m) => m.id === "claude-fable-5-1");
    expect(fable).toMatchObject({ context_window: 1000000, efforts: ["low", "medium", "high"], default_effort: "medium" });
    expect(posted()).toMatchObject({ fable_model: "claude-fable-5-1" });
  });

  it("drops a catalog answer that comes after the wizard was opened again", async () => {
    let open!: () => void;
    catalogGate = new Promise<void>((resolve) => { open = resolve; });
    catalog = [{ id: "claude-fable-5-1", name: "Fable 5.1", context_window: 1000000 }];
    await newGateway();
    type(input(`[aria-label="${t("settings-providerwizard.add_model_id")}"]`), "claude-fable-5-1");
    click(t("settings-providerwizard.add"));
    await flush();
    // reopened for a saved provider with the same model before the answer
    await draw({ name: "gw", source: "persona", base_url: "https://gw.example.com", has_key: true, model: "claude-fable-5-1", models: [{ id: "claude-fable-5-1" }] } as ProviderView);
    toStep(t("settings-providerwizard.step_models"));
    await act(async () => open());
    await flush();
    expect(input(`input[aria-label="${t("settings-providerwizard.context_window", { id: "claude-fable-5-1" })}"]`).value).toBe("");
  });

  it("keeps a typed model as it is on a hub without the catalog", async () => {
    catalog = null;
    await newGateway();
    type(input(`[aria-label="${t("settings-providerwizard.add_model_id")}"]`), "my-model");
    click(t("settings-providerwizard.add"));
    await flush();
    expect(input(`input[aria-label="${t("settings-providerwizard.context_window", { id: "my-model" })}"]`).value).toBe("");
    expect(text()).not.toContain("not found");
  });

  it("changes a saved provider's tiers only when its suggestion is applied", async () => {
    const view: ProviderView = { name: "gw", source: "persona", base_url: "https://gw.example.com", has_key: true, auth: "bearer", model: OPUS, models: [{ id: OPUS }, { id: SONNET }, { id: HAIKU }] } as ProviderView;
    await draw({ ...view });
    toStep(t("settings-providerwizard.step_models"));
    await flush();
    expect(summary()).not.toContain(t("settings-providerwizard.tier_auto"));
    expect(hasButton(t("settings-providerwizard.tiers_apply"))).toBe(true);
    await save();
    for (const k of ["opus_model", "sonnet_model", "haiku_model", "small_model"]) expect(put()?.[k] ?? "").toBe("");

    calls = [];
    await draw({ ...view });
    toStep(t("settings-providerwizard.step_models"));
    await flush();
    click(t("settings-providerwizard.tiers_apply"));
    expect(summary()).toContain(SONNET);
    await save();
    expect(put()).toMatchObject({ sonnet_model: SONNET, small_model: HAIKU });
  });

  it("is not shown for other kinds of provider", async () => {
    await draw({ name: "cx", source: "persona", agent: "codex", base_url: "https://gw.example.com", has_key: true, model: "gpt-5.4", models: [{ id: "gpt-5.4" }] } as ProviderView);
    toStep(t("settings-providerwizard.step_models"));
    await flush();
    expect(text()).not.toContain(t("settings-providerwizard.tiers_title"));
  });
});
