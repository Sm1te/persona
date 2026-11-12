// @vitest-environment jsdom
// "Paste a configuration" in Settings › Model providers › Add (#5193) through the wizard and its real HTTP client: the
// text is read by itself once it stops changing and at once on a paste, an answer for an older text is dropped, a draft
// fills in the gateway's steps (nothing saved before Save), several are chosen among, a draft on a stored address opens
// that provider with its models or its key, the notes are said in words, and an older hub or a failure keeps the text.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((media: string) => ({ matches: false, media, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { t } from "../../i18n";
import { noteText, type EditDraft, type PasteDraft, type PasteResult } from "../../lib/provider-paste";
import { importBrings, importLeftOut } from "../../lib/providers";
import { resetReadiness } from "../../lib/readiness";
import { getState } from "../../lib/store";
import { ProviderWizard } from "./ProviderWizard";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Call = { method: string; path: string; body?: Record<string, unknown> };
const GW: PasteDraft = {
  label: "kimi, kimi_fast",
  aliases: ["kimi", "kimi_fast"],
  provider: {
    kind: "launcher", agent: "claude", api: "messages", name: "kimi", base_url: "https://gw.example.com", auth: "bearer", api_key: "sk-example-gw",
    model: "example-5", models: [{ id: "example-5" }, { id: "example-5-air" }], opus_model: "example-5", env: "DISABLE_TELEMETRY=1", headers: "X-Team: blue",
  },
  env_names: ["DISABLE_TELEMETRY"],
  skipped_env: [{ name: "RELAY_TOKEN", reason: "credential" }],
  skipped_headers: [{ name: "anthropic-version", reason: "protocol" }],
};
const RELAY: PasteDraft = { provider: { kind: "api", api: "responses", base_url: "https://relay.example.org/v1", api_key: "sk-example-relay", model: "example-r" } };
const answer = (drafts: PasteDraft[], notes: PasteResult["notes"] = [], format: PasteResult["format"] = "shell"): PasteResult => ({ format, drafts, notes });

let calls: Call[];
let parse: (text: string) => Response | Promise<Response>;
let edits: [string, EditDraft][];
let host: HTMLDivElement;
let root: Root;
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
const flush = async () => { for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); }); };
const advance = async (ms: number) => { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); await flush(); };
const text = () => document.body.textContent ?? "";
const buttons = () => [...document.body.querySelectorAll("button")];
const button = (label: string) => {
  const found = buttons().find((b) => b.textContent?.trim() === label);
  expect(found, label).toBeDefined();
  return found!;
};
const pasteRow = () => buttons().find((b) => b.textContent?.startsWith(t("settings-providerwizard.paste_title")));
const box = () => document.body.querySelector<HTMLTextAreaElement>("textarea")!;
const parses = () => calls.filter((c) => c.path.endsWith("/launchers/import/parse"));
const saves = () => calls.filter((c) => (c.method === "POST" && c.path.endsWith("/api/v1/launchers")) || c.method === "PUT");
const continueButton = () => button(t("settings-providerwizard.paste_continue"));
/** Types into the box (`paste`: the edit is a paste). */
function enter(value: string, paste = false) {
  act(() => {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(box(), value);
    box().dispatchEvent(paste ? new InputEvent("input", { bubbles: true, inputType: "insertFromPaste" }) : new Event("input", { bubbles: true }));
  });
}
const click = async (el: Element) => { act(() => (el as HTMLElement).click()); await flush(); };

async function openPaste(features = ["builtin_presets", "launcher_auto_name", "launcher_import_parse"]) {
  Object.assign(getState(), { features });
  act(() => root.render(<ProviderWizard existing="new" onClose={() => {}} onSaved={() => {}} onEditWith={(name, d) => edits.push([name, d])} />));
  await flush();
  if (features.includes("launcher_import_parse")) await click(pasteRow()!);
}
/** Pastes `value` and lets the answer come in. */
async function pasteText(value: string) {
  enter(value, true);
  await flush();
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  calls = [];
  edits = [];
  parse = () => json(answer([GW]));
  resetReadiness();
  vi.stubGlobal("fetch", vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const call = { method: init?.method ?? "GET", path: String(url).split("?")[0]!, body: init?.body ? JSON.parse(String(init.body)) : undefined };
    calls.push(call);
    if (call.path.endsWith("/assistant/readiness")) return Promise.resolve(json({ error: "not found" }, 404));
    if (call.path.endsWith("/launchers/presets")) return Promise.resolve(json({ presets: [] }));
    if (call.path.endsWith("/launchers/import/parse")) return Promise.resolve(parse(String(call.body?.text)));
    if (call.path.endsWith("/launchers/discover")) return Promise.resolve(json({ models: [] }));
    if (call.path.endsWith("/launchers/test")) return Promise.resolve(json({ ok: true, ms: 90, model: "example-5" }));
    if (call.path.endsWith("/launchers") && call.method === "POST") return Promise.resolve(json({ launcher: { name: "kimi" } }));
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

describe("paste a configuration", () => {
  it("is offered only by a hub that reads pasted settings", async () => {
    await openPaste(["builtin_presets", "launcher_auto_name"]);
    expect(pasteRow()).toBeUndefined();
    act(() => root.unmount());
    root = createRoot(host);
    await openPaste();
    expect(box()).not.toBeNull();
    expect(button(t("settings-providerwizard.paste_continue")).disabled).toBe(true);
  });

  it("reads the text once it stops changing, and a paste at once", async () => {
    await openPaste();
    enter("export ANTHROPIC_BASE_URL=https://gw.example.com");
    await advance(500);
    enter("export ANTHROPIC_BASE_URL=https://gw.example.com\nexport ANTHROPIC_MODEL=example-5");
    await advance(500);
    expect(parses()).toHaveLength(0);
    await advance(200);
    expect(parses().map((c) => c.body)).toEqual([{ text: "export ANTHROPIC_BASE_URL=https://gw.example.com\nexport ANTHROPIC_MODEL=example-5" }]);
    expect(text()).toContain(t("settings-providerpaste.format_shell"));

    await pasteText("alias kimi='claude'");
    expect(parses()).toHaveLength(2);
    expect(parses()[1]!.body).toEqual({ text: "alias kimi='claude'" });
    // an empty box asks nothing
    enter("");
    await advance(700);
    expect(parses()).toHaveLength(2);
  });

  it("drops an answer for a text that changed since", async () => {
    let release!: () => void;
    parse = (value) => value === "first" ? new Promise((r) => (release = () => r(json(answer([RELAY]))))) : json(answer([GW]));
    await openPaste();
    await pasteText("first");
    await pasteText("second");
    release();
    await flush();
    expect(parses()).toHaveLength(2);
    expect(text()).toContain("kimi, kimi_fast");
    expect(text()).not.toContain("relay.example.org");
  });

  it("fills in the gateway's steps from one draft, and saves nothing before Save", async () => {
    await openPaste();
    await pasteText("alias kimi=…");
    const card = document.body.querySelector("[data-paste-draft]")!.textContent;
    expect(card).toContain("kimi, kimi_fast");
    expect(card).toContain("gw.example.com");
    expect(card).toContain(t("lib-providers.model_count", { count: 2 }));
    expect(card).toContain("Bearer");
    expect(card).toContain(importBrings(GW));
    for (const l of importLeftOut(GW)) expect(card).toContain(l);
    expect(card).toContain(t("lib-provider-paste.headers_kept", { names: "X-Team", count: 1 }));
    expect(card).toContain(t("lib-provider-paste.headers_left_protocol", { names: "anthropic-version", count: 1 }));

    await click(continueButton());
    expect(document.body.querySelector<HTMLInputElement>('input[inputmode="url"]')!.value).toBe("https://gw.example.com");
    expect(document.body.querySelector<HTMLInputElement>('input[type="password"]')!.value).toBe("sk-example-gw");
    expect(document.body.querySelector('[aria-current="step"]')!.textContent).toContain(t("settings-providerwizard.step_connect"));
    // the Connect step's own check runs on what was pasted
    await advance(700);
    expect(calls.some((c) => c.path.endsWith("/launchers/discover"))).toBe(true);
    await click(button(t("settings-providerwizard.next")));
    const rows = [...document.body.querySelectorAll("li[data-model]")].map((li) => [li.getAttribute("data-model"), li.querySelector('[role="checkbox"]')!.getAttribute("aria-checked")]);
    expect(rows).toEqual([["example-5", "true"], ["example-5-air", "true"]]);
    expect(saves()).toEqual([]);

    await click(button(t("common.save")));
    expect(saves()).toHaveLength(1);
    expect(saves()[0]!.body).toMatchObject({
      name: "kimi", base_url: "https://gw.example.com", auth: "bearer", api_key: "sk-example-gw", model: "example-5",
      models: [{ id: "example-5" }, { id: "example-5-air" }], opus_model: "example-5", small_model: "example-5-air", env: "DISABLE_TELEMETRY=1", headers: "X-Team: blue",
    });
  });

  it("waits for a choice among several drafts, by click or arrow keys", async () => {
    parse = () => json(answer([GW, RELAY]));
    await openPaste();
    await pasteText("two");
    expect(text()).toContain(t("settings-providerpaste.several", { count: 2 }));
    expect(continueButton().disabled).toBe(true);
    const group = document.body.querySelector(`[role="radiogroup"][aria-label="${t("settings-providerpaste.drafts_label")}"]`)!;
    const radio = (i: number) => group.querySelector<HTMLElement>(`[data-draft="${i}"]`)!;
    expect(radio(0).tabIndex).toBe(0);
    act(() => group.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    await flush();
    expect(radio(0).getAttribute("aria-checked")).toBe("true");
    act(() => group.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true })));
    await flush();
    expect(radio(1).getAttribute("aria-checked")).toBe("true");
    expect(document.activeElement).toBe(radio(1));
    // a direct API says so, and has no sign-in method to pick
    expect(radio(1).textContent).toContain(t("settings-providerwizard.direct_api"));
    expect(radio(1).textContent).not.toContain("Bearer");
    await click(radio(0));
    await click(radio(1));
    await click(continueButton());
    expect(document.body.querySelector<HTMLInputElement>('input[inputmode="url"]')!.value).toBe("https://relay.example.org/v1");
  });

  it("adds the models to a stored provider with the same key, in its edit wizard", async () => {
    parse = () => json(answer([{ ...GW, matches: [{ name: "gw_full", display_name: "Team gateway", same_key: true }] }]));
    await openPaste();
    await pasteText("same key");
    expect(text()).toContain(t("settings-providerpaste.matched", { count: 1 }));
    const merge = buttons().find((b) => b.getAttribute("data-choice") === "0")!;
    expect(merge.textContent).toContain(t("settings-providerpaste.merge", { name: "Team gateway" }));
    expect(merge.getAttribute("aria-checked")).toBe("true");
    await click(continueButton());
    expect(edits).toEqual([["gw_full", { step: "models", models: [{ id: "example-5" }, { id: "example-5-air" }] }]]);
    expect(saves()).toEqual([]);
  });

  it("replaces a stored provider's key when the key differs, if asked", async () => {
    parse = () => json(answer([{ ...GW, matches: [{ name: "gw_other", display_name: "Other gateway", same_key: false }] }]));
    await openPaste();
    await pasteText("other key");
    const option = (i: number) => buttons().find((b) => b.getAttribute("data-choice") === String(i))!;
    expect(option(0).textContent).toContain(t("settings-providerpaste.key", { name: "Other gateway" }));
    // another key: a new provider unless the user picks replacing
    expect(option(1).getAttribute("aria-checked")).toBe("true");
    await click(option(0));
    await click(continueButton());
    expect(edits).toEqual([["gw_other", { step: "connect", api_key: "sk-example-gw" }]]);
  });

  it("says in words why parts were not used, and when nothing can be added", async () => {
    const notes = [{ code: "line_unused", line: 3 }, { code: "not_claude", name: "ll" }];
    parse = () => json(answer([{ ...GW, notes: [{ code: "curl_option", name: "-u" }] }], notes));
    await openPaste();
    await pasteText("with notes");
    for (const n of notes) expect(text()).toContain(noteText(n));
    expect(document.body.querySelector("[data-paste-draft]")!.textContent).toContain(noteText({ code: "curl_option", name: "-u" }));

    parse = () => json(answer([], [{ code: "unrecognized" }], ""));
    await pasteText("hello");
    expect(text()).toContain(t("settings-providerpaste.nothing"));
    expect(text()).toContain(t("lib-provider-paste.unrecognized"));
    expect(continueButton().disabled).toBe(true);
  });

  it("says an older hub needs updating, and keeps what was pasted", async () => {
    parse = () => json({ error: "not found" }, 404);
    await openPaste();
    await pasteText("alias kimi=…");
    expect(document.body.querySelector('[role="alert"]')!.textContent).toBe(t("settings-providerpaste.hub_old"));
    expect(box().value).toBe("alias kimi=…");
    expect(buttons().some((b) => b.textContent === t("settings-providerwizard.retry"))).toBe(false);
  });

  it("keeps the text on a failure and reads it again on Retry", async () => {
    parse = () => json({ error: "boom" }, 500);
    await openPaste();
    await pasteText("alias kimi=…");
    expect(document.body.querySelector('[role="alert"]')!.textContent).toContain(t("settings-providerpaste.read_failed", { error: "boom" }).slice(0, 4));
    expect(box().value).toBe("alias kimi=…");
    parse = () => json(answer([GW]));
    await click(button(t("settings-providerwizard.retry")));
    expect(parses()).toHaveLength(2);
    expect(document.body.querySelector('[role="alert"]')).toBeNull();
    expect(continueButton().disabled).toBe(false);
  });

  it("goes back to the list with Back, and finds the paste as it was coming back, without reading it again", async () => {
    await openPaste();
    await pasteText("alias kimi=…");
    await click(button(t("settings-providerwizard.back")));
    expect(document.body.querySelector("textarea")).toBeNull();
    expect(text()).toContain(t("settings-providerwizard.custom"));
    await click(pasteRow()!);
    expect(box().value).toBe("alias kimi=…");
    await click(continueButton());
    // from "Connect", the first step is the paste again
    await click([...document.body.querySelectorAll("ol button")].find((b) => b.textContent?.includes(t("settings-providerwizard.step_pick")))!);
    expect(box().value).toBe("alias kimi=…");
    expect(document.body.querySelector("[data-paste-draft]")).not.toBeNull();
    await advance(700);
    expect(parses()).toHaveLength(1);
  });
});
