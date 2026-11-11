// @vitest-environment jsdom
// Exercise the mounted Settings wizard through its real HTTP client and form controls.
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

type Editor = ProviderView | "new" | null;
type Call = { method: string; path: string; body?: Record<string, unknown> };
type Pending = Call & { resolve: (value: unknown, status?: number) => void; reject: (error: Error) => void };
const a: ProviderView = { name: "provider-a", base_url: "https://a.example.com", has_key: true, auth: "bearer" };
const b: ProviderView = { name: "provider-b", base_url: "https://b.example.com", has_key: true, auth: "bearer" };
let calls: Call[];
let pending: Pending[];
let host: HTMLDivElement;
let root: Root;
let saved: () => void;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const text = () => document.body.textContent ?? "";
function button(key: string) {
  const found = [...document.body.querySelectorAll("button")].find((el) => el.textContent?.trim().startsWith(t(key)));
  expect(found, key).toBeDefined();
  return found!;
}
function click(key: string) { act(() => button(key).click()); }
function input(selector: string) {
  const found = document.body.querySelector<HTMLInputElement>(selector);
  expect(found, selector).not.toBeNull();
  return found!;
}
function type(el: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const prototype = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  act(() => {
    Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}
function render(existing: Editor) {
  root.render(<ProviderWizard existing={existing} onClose={() => render(null)} onSaved={saved} />);
}
async function draw(existing: Editor) {
  act(() => render(existing));
  await flush();
}
async function settle(request: Pending, outcome: "success" | "http error" | "network error", id = "a-only-model") {
  await act(async () => {
    if (outcome === "network error") request.reject(new Error("Retired request failed"));
    else if (outcome === "http error") request.resolve({ error: "Retired request failed" }, 502);
    else request.resolve(request.path.endsWith("/test") ? { ok: true, ms: 123, model: id } : { models: [{ id }] });
  });
  await flush();
}
async function addModel(id: string) {
  type(input(`[aria-label="${t("settings-providerwizard.add_model_id")}"]`), id);
  click("settings-providerwizard.add");
}
async function save() {
  click("common.save");
  await flush();
  expect(saved).toHaveBeenCalledOnce();
  return calls.find((c) => c.method === "PUT")!;
}
// The Connect step's automatic check waits this long after the address and key stop changing (AUTO_TEST_MS in
// ProviderWizard.tsx). Timers are fake, so nothing fires unless a test advances them.
const AUTO = 700;
async function advance(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
  await flush();
}
const nameInput = () => input('input[placeholder*="my_gateway"]');
const urlInput = () => input('input[inputmode="url"]');
const keyInput = () => input('input[type="password"]');
const primary = (key: string) => button(key).classList.contains("bg-ink");
async function resolve(request: Pending, value: unknown) {
  await act(async () => request.resolve(value));
  await flush();
}
/** Leaves a field (focus, then away), as Tab or a click elsewhere does. */
function leave(el: HTMLInputElement) {
  act(() => { el.focus(); el.blur(); });
}
/** Pastes `text`: over all the field's text (`whole`), or at the caret after it. As a browser does, the paste event
 *  carries the text, then the field changes (only when its text does). */
function paste(el: HTMLInputElement, text: string, whole = true) {
  act(() => {
    const before = el.value;
    if (whole) el.setSelectionRange(0, before.length);
    else el.setSelectionRange(before.length, before.length);
    const event = new Event("paste", { bubbles: true });
    Object.defineProperty(event, "clipboardData", { value: { getData: () => text } });
    el.dispatchEvent(event);
    const after = whole ? text.replace(/[\r\n]+/g, "") : before + text;
    if (after === before) return;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, after);
    el.dispatchEvent(new InputEvent("input", { bubbles: true, inputType: "insertFromPaste" }));
  });
}
async function newGateway(url = "https://a.example.com", key = "example-key-a") {
  await draw("new");
  click("settings-providerwizard.custom");
  type(urlInput(), url);
  leave(urlInput());
  type(keyInput(), key);
}

beforeEach(() => {
  // only timers are faked: promises, fetch and React's scheduling keep running for real
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  calls = [];
  pending = [];
  saved = vi.fn();
  Object.assign(getState(), { features: ["builtin_presets"] });
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (/\/launchers\/(discover|test)$/.test(call.path)) {
      return new Promise<Response>((resolve, reject) => pending.push({ ...call, resolve: (value, status) => resolve(json(value, status)), reject }));
    }
    return Promise.resolve(json(call.path.endsWith("/presets") ? { presets: [{ id: "example/preset", plugin: "example", name: "Synthetic provider", kind: "launcher", base_url: "https://preset.example.com", model: "preset-model" }] } : {}));
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

describe("provider discovery ownership", () => {
  it("does not save A's deferred models into B after closing and reopening (#1314)", async () => {
    await draw(a);
    click("settings-providerwizard.discover");
    expect(pending[0]!.body).toEqual({ name: "provider-a", base_url: "https://a.example.com", auth: "bearer", api: "messages" });
    click("common.cancel");
    await draw(b);
    await settle(pending[0]!, "success");
    click("settings-providerwizard.next");
    expect(document.querySelector('[data-model="a-only-model"]')).toBeNull();
    await addModel("b-manual");
    expect(await save()).toMatchObject({ path: "/api/v1/launchers/provider-b", body: { name: "provider-b", base_url: "https://b.example.com", model: "b-manual", models: [{ id: "b-manual" }] } });
  });

  it.each(["switch provider", "reopen same provider"] as const)("retires a request when we %s, even with identical connection inputs", async (transition) => {
    await draw(a);
    click("settings-providerwizard.discover");
    if (transition === "reopen same provider") { await draw(null); await draw(a); }
    else await draw({ ...b, base_url: a.base_url });
    expect(button("settings-providerwizard.discover").disabled).toBe(false);
    click("settings-providerwizard.next");
    await addModel("current-edit");
    await settle(pending[0]!, "success");
    expect(document.querySelector('[data-model="a-only-model"]')).toBeNull();
    expect((await save()).body).toMatchObject({ model: "current-edit", models: [{ id: "current-edit" }] });
  });

  it.each(["success", "http error", "network error"] as const)("ignores retired %s and finally while the new editor is discovering", async (outcome) => {
    await draw(a);
    click("settings-providerwizard.discover");
    await draw(null);
    await draw(b);
    click("settings-providerwizard.discover");
    expect(pending).toHaveLength(2);
    await settle(pending[0]!, outcome);
    expect(button("settings-providerwizard.discover").getAttribute("aria-busy")).toBe("true");
    expect(text()).not.toContain("Retired request failed");
    expect(text()).not.toContain(t("settings-providerwizard.discover_found", { count: 1 }));
    await settle(pending[1]!, "success", "b-only-model");
    expect(button("settings-providerwizard.discover").disabled).toBe(false);
    click("settings-providerwizard.next");
    expect((await save()).body).toMatchObject({ model: "b-only-model", models: [{ id: "b-only-model" }] });
  });

  it.each(["success", "http error", "network error"] as const)("ignores %s and finally from an earlier connection in the same editor", async (outcome) => {
    await draw(a);
    click("settings-providerwizard.discover");
    type(input('input[inputmode="url"]'), "https://b.example.com");
    expect(button("settings-providerwizard.discover").disabled).toBe(false);
    click("settings-providerwizard.discover");
    expect(pending[1]!.body).toMatchObject({ name: "provider-a", base_url: "https://b.example.com" });
    await settle(pending[0]!, outcome);
    expect(button("settings-providerwizard.discover").getAttribute("aria-busy")).toBe("true");
    expect(text()).not.toContain("Retired request failed");
    await settle(pending[1]!, "success", "current-model");
    click("settings-providerwizard.next");
    expect((await save()).body).toMatchObject({ base_url: "https://b.example.com", model: "current-model", models: [{ id: "current-model" }] });
  });

  it("does not replace a completed fresh result with a retired failure", async () => {
    await draw(a);
    click("settings-providerwizard.discover");
    await draw(b);
    click("settings-providerwizard.discover");
    await settle(pending[1]!, "success", "b-model");
    await settle(pending[0]!, "network error");
    expect(text()).toContain(t("settings-providerwizard.discover_found", { count: 1 }));
    expect(text()).not.toContain("Retired request failed");
    click("settings-providerwizard.next");
    expect((await save()).body).toMatchObject({ model: "b-model", models: [{ id: "b-model" }] });
  });

  it("retires custom discovery when picking a preset and returning to the same custom form", async () => {
    await draw("new");
    click("settings-providerwizard.custom");
    type(input('input[placeholder*="my_gateway"]'), "my_gateway");
    type(input('input[inputmode="url"]'), "https://a.example.com");
    type(input('input[type="password"]'), "example-key-a");
    click("settings-providerwizard.discover");
    act(() => document.querySelector<HTMLButtonElement>("ol button")!.click());
    act(() => [...document.querySelectorAll("button")].find((el) => el.textContent?.startsWith("Synthetic provider"))!.click());
    act(() => document.querySelector<HTMLButtonElement>("ol button")!.click());
    click("settings-providerwizard.custom");
    await settle(pending[0]!, "success");
    expect(button("settings-providerwizard.discover").disabled).toBe(false);
    expect(input('input[type="password"]').value).toBe("example-key-a");
    click("settings-providerwizard.next");
    expect(document.querySelector('[data-model="a-only-model"]')).toBeNull();
  });

  it.each(["endpoint", "key", "auth", "type", "protocol", "headers", "endpoint changed back"] as const)("invalidates discovery on %s edits without resetting the form", async (field) => {
    await draw("new");
    click("settings-providerwizard.custom");
    type(input('input[placeholder="e.g. my_gateway"], input[placeholder*="my_gateway"]'), "my_gateway");
    type(input('input[inputmode="url"]'), "https://a.example.com");
    type(input('input[type="password"]'), "example-key-a");
    click("settings-providerwizard.discover");
    if (field.startsWith("endpoint")) {
      type(input('input[inputmode="url"]'), "https://b.example.com");
      if (field === "endpoint changed back") type(input('input[inputmode="url"]'), "https://a.example.com");
    } else if (field === "key") type(input('input[type="password"]'), "example-key-b");
    else if (field === "auth") act(() => [...document.querySelectorAll("button")].find((el) => el.textContent === "x-api-key")!.click());
    else if (field === "type" || field === "protocol") {
      click("settings-providerwizard.direct_api");
      if (field === "protocol") act(() => [...document.querySelectorAll("button")].find((el) => el.textContent === "OpenAI Responses")!.click());
    } else {
      click("settings-providerwizard.next");
      click("settings-providerwizard.advanced");
      const headers = document.querySelector<HTMLTextAreaElement>("textarea")!;
      type(headers, "X-Example: changed");
    }
    const expectedURL = field === "endpoint" ? "https://b.example.com" : "https://a.example.com";
    await settle(pending[0]!, "success");
    if (field !== "headers") {
      expect(input('input[inputmode="url"]').value).toBe(expectedURL);
      expect(input('input[type="password"]').value).toBe(field === "key" ? "example-key-b" : "example-key-a");
      click("settings-providerwizard.next");
    }
    expect(document.querySelector('[data-model="a-only-model"]')).toBeNull();
    await addModel("manual-model");
    click("common.save");
    await flush();
    const body = calls.find((c) => c.method === "POST" && c.path.endsWith("/launchers"))!.body;
    expect(body).toMatchObject({ base_url: expectedURL, model: "manual-model", models: [{ id: "manual-model" }] });
    if (field === "headers") expect(body?.headers).toBe("X-Example: changed");
  });

  it("allows retry after current discovery fails, then auto-selects models in the empty form", async () => {
    await draw(a);
    click("settings-providerwizard.discover");
    await settle(pending[0]!, "http error");
    expect(text()).toContain("Retired request failed");
    expect(button("settings-providerwizard.discover").disabled).toBe(false);
    click("settings-providerwizard.discover");
    expect(text()).not.toContain("Retired request failed");
    await settle(pending[1]!, "success", "discovered-model");
    click("settings-providerwizard.next");
    expect((await save()).body).toMatchObject({ model: "discovered-model", models: [{ id: "discovered-model" }] });
  });

  it("merges valid results with current model edits, checked choices and an intentional default", async () => {
    await draw({ ...a, model: "original", models: [{ id: "original" }, { id: "keep-unchecked" }] });
    click("settings-providerwizard.discover");
    click("settings-providerwizard.next");
    act(() => document.querySelector<HTMLButtonElement>('[data-model="keep-unchecked"] [role="checkbox"]')!.click());
    type(input(`[aria-label="${t("settings-providerwizard.context_window", { id: "original" })}"]`), "12345");
    await addModel("manual-default");
    act(() => [...document.querySelectorAll<HTMLButtonElement>('[data-model="manual-default"] button')].find((el) => el.textContent === t("settings-providerwizard.set_default"))!.click());
    await act(async () => pending[0]!.resolve({ models: [{ id: "original", context_window: 99999 }, { id: "keep-unchecked" }, { id: "new-unchecked" }] }));
    await flush();
    expect(document.querySelector('[data-model="new-unchecked"] [role="checkbox"]')?.getAttribute("aria-checked")).toBe("false");
    expect(document.querySelector('[data-model="keep-unchecked"] [role="checkbox"]')?.getAttribute("aria-checked")).toBe("false");
    expect((await save()).body).toMatchObject({ model: "manual-default", models: [{ id: "original", context_window: 12345 }, { id: "manual-default" }] });
  });
});

it.each(["discover", "test"] as const)("keeps the latest %s request's busy/result ownership when clicks overlap before rerender", async (operation) => {
  await draw(a);
  const key = `settings-providerwizard.${operation}`;
  act(() => { button(key).click(); button(key).click(); });
  expect(pending).toHaveLength(2);
  await settle(pending[0]!, "http error");
  expect(button(key).getAttribute("aria-busy")).toBe("true");
  expect(text()).not.toContain("Retired request failed");
  await settle(pending[1]!, "success", "latest-model");
  expect(button(key).disabled).toBe(false);
  if (operation === "test") expect(document.querySelector('[role="status"]')?.textContent).toContain("123");
  else {
    click("settings-providerwizard.next");
    expect((await save()).body).toMatchObject({ model: "latest-model", models: [{ id: "latest-model" }] });
  }
});

describe("connection test ownership", () => {
  it.each(["success", "http error", "network error"] as const)("ignores retired %s and finally while the new editor is testing", async (outcome) => {
    await draw(a);
    click("settings-providerwizard.test");
    await draw(null);
    await draw(b);
    click("settings-providerwizard.test");
    expect(pending).toHaveLength(2);
    await settle(pending[0]!, outcome);
    expect(button("settings-providerwizard.test").getAttribute("aria-busy")).toBe("true");
    expect(document.querySelector('[role="status"]')?.textContent).toBe(t("settings-providerwizard.testing"));
    await settle(pending[1]!, "http error");
    expect(text()).toContain("Retired request failed");
    click("settings-providerwizard.test");
    await settle(pending[2]!, "success", "b-model");
    expect(text()).not.toContain("Retired request failed");
    expect(document.querySelector('[role="status"]')?.textContent).toContain("123");
    expect(button("settings-providerwizard.test").disabled).toBe(false);
  });

  it.each(["endpoint", "key", "test model"] as const)("invalidates a test on %s edits and lets the current request complete", async (field) => {
    await draw(a);
    click("settings-providerwizard.test");
    if (field === "endpoint") type(input('input[inputmode="url"]'), "https://b.example.com");
    else if (field === "key") type(input('input[type="password"]'), "example-key-b");
    else type(input(`[aria-label="${t("settings-providerwizard.test_model")}"]`), "new-test-model");
    expect(button("settings-providerwizard.test").disabled).toBe(false);
    click("settings-providerwizard.test");
    await settle(pending[0]!, "network error");
    expect(button("settings-providerwizard.test").getAttribute("aria-busy")).toBe("true");
    expect(text()).not.toContain("Retired request failed");
    await settle(pending[1]!, "success");
    expect(document.querySelector('[role="status"]')?.textContent).toContain("123");
  });

  it("keeps discovery alive when only the test model changes, and keeps a current test alive when discovery fails", async () => {
    await draw(a);
    click("settings-providerwizard.discover");
    type(input(`[aria-label="${t("settings-providerwizard.test_model")}"]`), "explicit-model");
    click("settings-providerwizard.test");
    await settle(pending[0]!, "http error");
    expect(button("settings-providerwizard.test").getAttribute("aria-busy")).toBe("true");
    await settle(pending[1]!, "success");
    expect(document.querySelector('[role="status"]')?.textContent).toContain("123");
    click("settings-providerwizard.discover");
    await settle(pending[2]!, "success", "valid-model");
    click("settings-providerwizard.next");
    expect((await save()).body).toMatchObject({ model: "valid-model", models: [{ id: "valid-model" }] });
  });
});

describe("the Connect step's own check (#4881)", () => {
  it("tests and fetches whatever the name is; only Next waits for a valid one", async () => {
    await newGateway();
    for (const name of ["", "1 bad name"]) {
      type(nameInput(), name);
      expect(button("settings-providerwizard.test").disabled).toBe(false);
      expect(button("settings-providerwizard.discover").disabled).toBe(false);
    }
    click("settings-providerwizard.test");
    expect(pending.map((r) => r.path)).toEqual(["/api/v1/launchers/test"]);
    click("settings-providerwizard.next");
    expect(document.querySelector('[role="alert"]')?.textContent).toBe(t("lib-launcher-presets.name_invalid"));
    expect(urlInput().value).toBe("https://a.example.com");
    // a check started by hand is the check: nothing more is sent by itself
    await advance(AUTO * 3);
    expect(pending).toHaveLength(1);
  });

  it("fetches the models once the address and key stop changing, then tests the default model once", async () => {
    await draw("new");
    click("settings-providerwizard.custom");
    expect(text()).toContain(t("settings-providerwizard.check_hint"));
    type(urlInput(), "https://a.example.com");
    leave(urlInput());
    type(keyInput(), "example-key");
    expect(text()).toContain(t("settings-providerwizard.check_wait"));
    await advance(AUTO - 1);
    type(keyInput(), "example-key-a");
    await advance(AUTO - 1);
    expect(pending).toHaveLength(0);
    await advance(1);
    expect(pending).toHaveLength(1);
    expect(pending[0]).toMatchObject({ path: "/api/v1/launchers/discover", body: { base_url: "https://a.example.com", api_key: "example-key-a", auth: "bearer", api: "messages" } });
    expect(text()).toContain(t("settings-providerwizard.discovering"));
    expect(primary("settings-providerwizard.next")).toBe(true);
    await resolve(pending[0]!, { models: [{ id: "first-model" }, { id: "second-model" }] });
    expect(pending).toHaveLength(2);
    expect(pending[1]).toMatchObject({ path: "/api/v1/launchers/test", body: { base_url: "https://a.example.com", api_key: "example-key-a", model: "first-model" } });
    expect(text()).toContain(t("settings-providerwizard.testing"));
    await resolve(pending[1]!, { ok: true, ms: 412, model: "first-model" });
    expect(text()).toContain(t("lib-providers.test_ok", { ms: 412 }));
    expect(text()).toContain(t("settings-providerwizard.discover_found", { count: 2 }));
    // the same connection is not checked again: naming it, waiting, or going to the models and back
    type(nameInput(), "my_gateway");
    await advance(AUTO * 3);
    click("settings-providerwizard.next");
    expect(document.querySelector('[data-model="first-model"] [role="checkbox"]')?.getAttribute("aria-checked")).toBe("true");
    click("settings-providerwizard.back");
    await advance(AUTO * 3);
    expect(pending).toHaveLength(2);
    expect(text()).toContain(t("lib-providers.test_ok", { ms: 412 }));
  });

  it("tests the first model found when a long list leaves nothing checked", async () => {
    await newGateway();
    await advance(AUTO);
    await resolve(pending[0]!, { models: Array.from({ length: 12 }, (_, i) => ({ id: `listed-${i + 1}` })) });
    expect(pending[1]).toMatchObject({ path: "/api/v1/launchers/test", body: { model: "listed-1" } });
  });

  it.each(["key", "address"] as const)("checks again once the %s changes", async (field) => {
    await newGateway();
    await advance(AUTO);
    await resolve(pending[0]!, { models: [{ id: "first-model" }] });
    await resolve(pending[1]!, { ok: true, ms: 412, model: "first-model" });
    if (field === "key") type(keyInput(), "example-key-b");
    else {
      type(urlInput(), "https://b.example.com");
      leave(urlInput());
    }
    expect(text()).not.toContain(t("lib-providers.test_ok", { ms: 412 }));
    await advance(AUTO);
    expect(pending).toHaveLength(3);
    expect(pending[2]).toMatchObject({ path: "/api/v1/launchers/discover", body: field === "key" ? { api_key: "example-key-b" } : { base_url: "https://b.example.com" } });
    await resolve(pending[2]!, { models: [{ id: "first-model" }] });
    expect(pending[3]).toMatchObject({ path: "/api/v1/launchers/test", body: { model: "first-model" } });
  });

  it("sends nothing on its own while a new gateway's address or key is incomplete", async () => {
    await newGateway("https://a.example.com", "short");
    await advance(AUTO * 3);
    type(keyInput(), "example-key-a");
    type(urlInput(), "a.example.com");
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    expect(text()).toContain(t("settings-providerwizard.check_hint"));
  });

  it.each(["address", "key"] as const)("sends nothing for a saved provider until its %s changes", async (field) => {
    await draw(a);
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    expect(text()).not.toContain(t("settings-providerwizard.check_hint"));
    expect(primary("common.save")).toBe(true);
    expect(primary("settings-providerwizard.next")).toBe(false);
    if (field === "key") type(keyInput(), "example-key-b");
    else paste(urlInput(), "https://b.example.com");
    await advance(AUTO);
    expect(pending).toHaveLength(1);
    expect(pending[0]).toMatchObject({ path: "/api/v1/launchers/discover", body: { name: "provider-a", ...(field === "key" ? { api_key: "example-key-b" } : { base_url: "https://b.example.com" }) } });
    await resolve(pending[0]!, { models: [{ id: "new-model" }] });
    // models to choose from: Next is the step's one primary action, Save steps back
    expect(primary("settings-providerwizard.next")).toBe(true);
    expect(primary("common.save")).toBe(false);
  });

  it.each(["saved", "new"] as const)("never sends a key to an address still being typed (%s provider)", async (kind) => {
    if (kind === "saved") await draw(a);
    else {
      await draw("new");
      click("settings-providerwizard.custom");
      type(keyInput(), "example-key-a");
    }
    // typed a character at a time, paused partway: "https://b.example.co" may be someone else's host
    type(urlInput(), "https://b.example.co");
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    type(urlInput(), "https://b.example.com");
    leave(urlInput());
    await advance(AUTO);
    expect(pending).toHaveLength(1);
    expect(pending[0]).toMatchObject({ path: "/api/v1/launchers/discover", body: { base_url: "https://b.example.com" } });
  });

  it("still tests a model it already knows when the gateway lists none, and does not test it again on Retry", async () => {
    await draw({ ...a, model: "known-model", models: [{ id: "known-model" }] });
    type(keyInput(), "example-key-b");
    await advance(AUTO);
    await resolve(pending[0]!, { models: [], error: "The gateway has no model list", code: "not_found" });
    expect(pending[1]).toMatchObject({ path: "/api/v1/launchers/test", body: { name: "provider-a", api_key: "example-key-b", model: "known-model" } });
    await resolve(pending[1]!, { ok: true, ms: 412, model: "known-model" });
    // Retry is the list's: the test that passed is not paid for again
    click("settings-providerwizard.retry");
    await resolve(pending[2]!, { models: [], error: "The gateway has no model list", code: "not_found" });
    expect(pending.map((r) => r.path)).toEqual(["/api/v1/launchers/discover", "/api/v1/launchers/test", "/api/v1/launchers/discover"]);
    expect(text()).toContain(t("lib-providers.test_ok", { ms: 412 }));
  });

  it.each([
    // a key may answer without being allowed to list models: the list's 401 / 403 says nothing about chatting
    ["a list refused for the key", { models: [], error: "The gateway refused this key", code: "auth" }],
    ["a busy gateway", { models: [], error: "Too many requests" }],
    ["an unreadable list", { models: [], error: "The model list could not be read" }],
    ["a list that failed after its first page", { models: [{ id: "first-model" }], error: "The next page could not be read" }],
  ] as const)("still tests the model after %s", async (_, answer) => {
    await draw({ ...a, model: "known-model", models: [{ id: "known-model" }] });
    type(keyInput(), "example-key-b");
    await advance(AUTO);
    await resolve(pending[0]!, answer);
    expect(pending[1]).toMatchObject({ path: "/api/v1/launchers/test", body: { model: "known-model" } });
  });

  it("still tests the model when the hub itself could not fetch the list", async () => {
    await draw({ ...a, model: "known-model", models: [{ id: "known-model" }] });
    type(keyInput(), "example-key-b");
    await advance(AUTO);
    await settle(pending[0]!, "http error");
    expect(pending[1]).toMatchObject({ path: "/api/v1/launchers/test", body: { model: "known-model" } });
  });

  it("does not send a second test when the list is retried while the check's test runs", async () => {
    await draw({ ...a, model: "known-model", models: [{ id: "known-model" }] });
    type(keyInput(), "example-key-b");
    await advance(AUTO);
    await resolve(pending[0]!, { models: [], error: "The gateway has no model list", code: "not_found" });
    expect(text()).toContain(t("settings-providerwizard.testing"));
    click("settings-providerwizard.retry");
    await resolve(pending[2]!, { models: [], error: "The gateway has no model list", code: "not_found" });
    await resolve(pending[1]!, { ok: true, ms: 412, model: "known-model" });
    expect(pending.map((r) => r.path)).toEqual(["/api/v1/launchers/discover", "/api/v1/launchers/test", "/api/v1/launchers/discover"]);
    expect(text()).toContain(t("lib-providers.test_ok", { ms: 412 }));
  });

  it("counts a paste into part of the URL as typing", async () => {
    await draw(a);
    const url = urlInput();
    act(() => url.focus());
    type(url, "https://b.exa");
    paste(url, "mple", false);
    expect(url.value).toBe("https://b.example");
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    paste(url, "https://b.example.com\n");
    await advance(AUTO);
    expect(pending[0]).toMatchObject({ path: "/api/v1/launchers/discover", body: { base_url: "https://b.example.com" } });
  });

  it("counts pasting the same URL over all of it, though nothing changes", async () => {
    await draw(a);
    const url = urlInput();
    act(() => url.focus());
    type(url, "https://b.example.com");
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    paste(url, "https://b.example.com");
    await advance(AUTO);
    expect(pending[0]).toMatchObject({ path: "/api/v1/launchers/discover", body: { base_url: "https://b.example.com" } });
  });

  it("keeps a half-typed URL waiting when Next is refused for the name", async () => {
    await draw("new");
    click("settings-providerwizard.custom");
    type(nameInput(), "1 bad");
    type(keyInput(), "example-key-a");
    type(urlInput(), "https://api.exa");
    click("settings-providerwizard.next");
    expect(document.querySelector('[role="alert"]')?.textContent).toBe(t("lib-launcher-presets.name_invalid"));
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
  });

  it("takes leaving the step as the URL being entered, even when the field never blurs", async () => {
    await draw("new");
    click("settings-providerwizard.custom");
    type(nameInput(), "my_gateway");
    type(keyInput(), "example-key-a");
    type(urlInput(), "https://a.example.com");
    expect(text()).toContain(t("settings-providerwizard.check_leave_url"));
    click("settings-providerwizard.next");
    click("settings-providerwizard.back");
    await advance(AUTO);
    expect(pending[0]).toMatchObject({ path: "/api/v1/launchers/discover", body: { base_url: "https://a.example.com" } });
  });

  it("keeps waiting while the window, not the field, loses focus, and says leaving the field starts the check", async () => {
    await draw(a);
    const url = urlInput();
    act(() => url.focus());
    type(url, "https://b.example.co");
    expect(text()).toContain(t("settings-providerwizard.check_leave_url"));
    // switching to another app blurs the field but leaves it the page's focused element
    act(() => { url.dispatchEvent(new FocusEvent("focusout", { bubbles: true })); });
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    // typing after a paste is typing again
    paste(url, "https://b.example.com");
    type(url, "https://b.example.com/x");
    await advance(AUTO * 3);
    expect(pending).toHaveLength(0);
    leave(url);
    await advance(AUTO);
    expect(pending).toHaveLength(1);
  });

  it("does not repeat a test pressed by hand while the check is fetching", async () => {
    await newGateway();
    await advance(AUTO);
    click("settings-providerwizard.test");
    await resolve(pending[0]!, { models: [{ id: "first-model" }] });
    expect(pending.map((r) => r.path)).toEqual(["/api/v1/launchers/discover", "/api/v1/launchers/test"]);
  });

  it("retries a fetch pressed by hand as a fetch only", async () => {
    await newGateway();
    click("settings-providerwizard.discover");
    await resolve(pending[0]!, { models: [], error: "The gateway has no model list" });
    click("settings-providerwizard.retry");
    await resolve(pending[1]!, { models: [{ id: "first-model" }] });
    await advance(AUTO * 3);
    expect(pending.map((r) => r.path)).toEqual(["/api/v1/launchers/discover", "/api/v1/launchers/discover"]);
  });

  it("says the hub's words with Retry, and tests nothing after a failed fetch with no model known", async () => {
    await newGateway();
    await advance(AUTO);
    await resolve(pending[0]!, { models: [], error: "The gateway has no model list" });
    expect(pending).toHaveLength(1);
    expect(text()).toContain(t("settings-providerwizard.discover_failed", { error: "The gateway has no model list" }));
    expect(primary("settings-providerwizard.next")).toBe(true);
    click("settings-providerwizard.retry");
    expect(pending[1]).toMatchObject({ path: "/api/v1/launchers/discover" });
    await resolve(pending[1]!, { models: [{ id: "first-model" }] });
    expect(pending[2]).toMatchObject({ path: "/api/v1/launchers/test", body: { model: "first-model" } });
    await resolve(pending[2]!, { ok: false, ms: 80, model: "first-model", error: "The gateway refused this key" });
    expect(text()).toContain("The gateway refused this key");
    click("settings-providerwizard.retry");
    expect(pending).toHaveLength(4);
    expect(pending[3]).toMatchObject({ path: "/api/v1/launchers/test", body: { model: "first-model" } });
    await resolve(pending[3]!, { ok: true, ms: 412, model: "first-model" });
    expect(text()).not.toContain("The gateway refused this key");
    expect(document.body.querySelectorAll("button").length).toBeGreaterThan(0);
    expect([...document.body.querySelectorAll("button")].some((el) => el.textContent === t("settings-providerwizard.retry"))).toBe(false);
  });

  it("only fetches when Fetch models is pressed by hand", async () => {
    await newGateway();
    click("settings-providerwizard.discover");
    await resolve(pending[0]!, { models: [{ id: "first-model" }] });
    await advance(AUTO * 3);
    expect(pending.map((r) => r.path)).toEqual(["/api/v1/launchers/discover"]);
  });
});

