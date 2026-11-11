// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { api, handleUnauthorized, setPairedToken, setToken } from "../../lib/api";
import { CREDENTIAL_EPOCH_KEY, renewCredentialEpoch } from "../../lib/credential-epoch";
import { reloadForSignIn } from "../../lib/edge-auth";
import { pinManager, resetManagerChoice, setRosterDefault } from "../../lib/manager-choice";

const roster = vi.hoisted(() => ({ managers: [
  { id: "assistant", name: "First manager", online: true, builtin: true },
  { id: "second", name: "Second manager", online: true },
], default: "assistant" }));
vi.mock("../../lib/managers", async (original) => ({ ...(await original<typeof import("../../lib/managers")>()), useRoster: () => roster }));
vi.mock("../../lib/edge-auth", async (original) => ({ ...(await original<typeof import("../../lib/edge-auth")>()), reloadForSignIn: vi.fn() }));
import { ExtensionDownloadMirrors } from "./ExtensionDownloadMirrors";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
window.matchMedia ??= ((media: string) => ({ matches: false, media, addEventListener() {}, removeEventListener() {} })) as typeof window.matchMedia;

type Mirrors = { registry_mirror: string; catalog_mirror: string };
type Call = { method: string; token: string | null; manager: string; header: string | null; body?: Mirrors; signal?: AbortSignal | null };
const old: Mirrors = { registry_mirror: "https://old-registry.example", catalog_mirror: "https://old-catalog.example" };
const fresh: Mirrors = { registry_mirror: "https://fresh-registry.example", catalog_mirror: "https://fresh-catalog.example" };
const second: Mirrors = { registry_mirror: "https://second-registry.example", catalog_mirror: "https://second-catalog.example" };
const edited = "https://edited.example";
let host: HTMLDivElement, root: Root, calls: Call[], server: Map<string, Mirrors>;
let intercept: ((call: Call) => Response | Promise<Response> | undefined) | undefined;
let releasePending: (() => void)[];
let restoreUnauthorized: () => void;
const unauthorized = vi.fn();
const flush = async () => { for (let i = 0; i < 10; i++) await act(async () => { await Promise.resolve(); }); };
const inputs = () => [...host.querySelectorAll<HTMLInputElement>('input[type="url"]')];
const button = (key: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.trim() === t(`settings-hubsettings.${key}`))!;
const getCalls = (method: string) => calls.filter((call) => call.method === method);
const json = (value: unknown) => Response.json(value);

function hold() {
  let resolve!: (response: Response) => void;
  const response = new Promise<Response>((done) => { resolve = done; });
  let settled = false;
  const answer = (value: Response) => { if (!settled) { settled = true; resolve(value); } };
  releasePending.push(() => answer(json(old)));
  return { response, answer };
}
async function render() { await act(async () => root.render(<ExtensionDownloadMirrors />)); await flush(); }
async function edit(index: number, value: string) {
  await act(async () => {
    const input = inputs()[index];
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await flush();
}
async function save() { await act(async () => button("mirror_save").click()); await flush(); }
async function switchCredential(token: string, crossTab = false) {
  await act(async () => {
    if (!crossTab) setToken(token);
    else {
      const before = localStorage.getItem("persona.token");
      localStorage.setItem("persona.token", token);
      window.dispatchEvent(new StorageEvent("storage", { key: "persona.token", oldValue: before, newValue: token, storageArea: localStorage }));
    }
  });
  await flush();
}
async function switchManager(manager = "second") {
  await act(async () => {
    const select = host.querySelector("select")!;
    select.value = manager;
    select.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await flush();
}
async function respond(pending: ReturnType<typeof hold>, response: Response) {
  await act(async () => pending.answer(response));
  await flush();
}

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  history.replaceState(null, "", "/settings?tab=hub");
  resetManagerChoice();
  setRosterDefault("assistant");
  setToken("synthetic-A");
  calls = [];
  server = new Map([["assistant", { ...old }], ["second", { ...second }]]);
  intercept = undefined;
  releasePending = [];
  unauthorized.mockReset();
  vi.mocked(reloadForSignIn).mockClear();
  restoreUnauthorized = handleUnauthorized(unauthorized);
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://local.invalid");
    if (!url.pathname.endsWith("/market-settings")) throw new Error(`Unexpected synthetic endpoint: ${url.pathname}`);
    const headers = new Headers(init?.headers);
    const manager = url.searchParams.get("manager")!;
    const call: Call = { method: init?.method ?? "GET", token: headers.get("Authorization"), manager,
      header: headers.get("X-Persona-Manager"), signal: init?.signal,
      ...(init?.body ? { body: JSON.parse(String(init.body)) as Mirrors } : {}) };
    expect(init).not.toHaveProperty("isAuthContextCurrent");
    calls.push(call);
    const overridden = intercept?.(call);
    if (overridden !== undefined) return Promise.resolve(overridden);
    if (call.method === "PUT") server.set(manager, { ...call.body! });
    return Promise.resolve(json(server.get(manager)));
  }));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(async () => {
  await act(async () => { root.unmount(); releasePending.forEach((release) => release()); });
  await flush();
  host.remove();
  handleUnauthorized(restoreUnauthorized);
  setToken("");
  resetManagerChoice();
  vi.unstubAllGlobals();
});

describe("download mirror credential and manager ownership", () => {
  it("reloads on cross-tab credential replacement before saving either full field", async () => {
    await render();
    expect(inputs().map((input) => input.value)).toEqual([old.registry_mirror, old.catalog_mirror]);
    await edit(1, "https://unsaved.example");
    server.set("assistant", { ...fresh }); // An independent synthetic client changed the shared config.
    await switchCredential("synthetic-B", true);
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
    await edit(0, edited);
    await save();
    expect(getCalls("GET").map((call) => call.token)).toEqual(["Bearer synthetic-A", "Bearer synthetic-B"]);
    expect(getCalls("PUT")).toEqual([expect.objectContaining({ token: "Bearer synthetic-B", manager: "assistant", header: "assistant",
      body: { ...fresh, registry_mirror: edited } })]);
    expect(server.get("assistant")).toEqual({ ...fresh, registry_mirror: edited });
  });

  it.each(["B", "B then A"])("rejects an old A read after %s", async (transition) => {
    const pending = hold();
    intercept = (call) => call.method === "GET" && getCalls("GET").length === 1 ? pending.response : undefined;
    await render();
    expect(getCalls("GET")[0].token).toBe("Bearer synthetic-A");
    server.set("assistant", { ...fresh });
    await switchCredential("synthetic-B", true);
    if (transition === "B then A") await switchCredential("synthetic-A", true);
    expect(inputs()[1].value).toBe(fresh.catalog_mirror);
    expect(getCalls("GET")[0].signal?.aborted).toBe(true);
    await respond(pending, json(old)); // The synthetic transport ignores abort, like a late response.
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it.each(["settings", "401"])("retires an A read when cross-tab A to B to A events arrive after the token returned (%s)", async (answer) => {
    const pending = hold();
    intercept = (call) => call.method === "GET" && getCalls("GET").length === 1 ? pending.response : undefined;
    await render();
    server.set("assistant", { ...fresh });
    await act(async () => {
      // Both writes finished in the other tab before this tab processed either storage event.
      localStorage.setItem("persona.token", "synthetic-B");
      localStorage.setItem("persona.token", "synthetic-A");
      window.dispatchEvent(new StorageEvent("storage", { key: "persona.token", oldValue: "synthetic-A", newValue: "synthetic-B", storageArea: localStorage }));
      window.dispatchEvent(new StorageEvent("storage", { key: "persona.token", oldValue: "synthetic-B", newValue: "synthetic-A", storageArea: localStorage }));
    });
    await flush();
    await respond(pending, answer === "settings" ? json(old) : jsonError(401));
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it("reloads an edited form when the same token is paired again", async () => {
    await render();
    await edit(1, "https://unsaved.example");
    server.set("assistant", { ...fresh });
    await act(async () => setPairedToken("synthetic-A"));
    await flush();
    expect(getCalls("GET").map((call) => call.token)).toEqual(["Bearer synthetic-A", "Bearer synthetic-A"]);
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
    await edit(0, edited);
    await save();
    expect(getCalls("PUT")[0]).toMatchObject({ token: "Bearer synthetic-A", body: { ...fresh, registry_mirror: edited } });
    expect(server.get("assistant")).toEqual({ ...fresh, registry_mirror: edited });
  });

  it("wakes a mounted form on another tab's same-token pairing epoch", async () => {
    await render();
    await edit(0, "https://unsaved.example");
    server.set("assistant", { ...fresh });
    await act(async () => {
      const before = localStorage.getItem(CREDENTIAL_EPOCH_KEY);
      expect(renewCredentialEpoch()).toBe(true);
      const after = localStorage.getItem(CREDENTIAL_EPOCH_KEY);
      expect(after).not.toBe(before);
      window.dispatchEvent(new StorageEvent("storage", { key: CREDENTIAL_EPOCH_KEY, oldValue: before, newValue: after, storageArea: localStorage }));
    });
    await flush();
    expect(getCalls("GET").map((call) => call.token)).toEqual(["Bearer synthetic-A", "Bearer synthetic-A"]);
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
  });

  it.each(["settings", "json", "html", "redirect"])("rejects old A %s before cross-tab A to B to A events when the writer renews the shared epoch", async (answer) => {
    const pending = hold();
    intercept = (call) => call.method === "GET" && getCalls("GET").length === 1 ? pending.response : undefined;
    await render();
    server.set("assistant", { ...fresh });
    // Another current client completed both pairings; this tab has not received storage events yet.
    localStorage.setItem("persona.token", "synthetic-B");
    expect(renewCredentialEpoch()).toBe(true);
    localStorage.setItem("persona.token", "synthetic-A");
    expect(renewCredentialEpoch()).toBe(true);
    await respond(pending, answer === "settings" ? json(old) : authResponse(answer));
    expect(getCalls("GET").map((call) => call.token)).toEqual(["Bearer synthetic-A", "Bearer synthetic-A"]);
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
    expect(unauthorized).not.toHaveBeenCalled();
    expect(reloadForSignIn).not.toHaveBeenCalled();
  });

  it.each(["json", "html", "redirect"])("isolates a dispatched old write's %s after same-token reauthentication", async (answer) => {
    const pending = hold();
    intercept = (call) => call.method === "PUT" ? pending.response : undefined;
    await render();
    await edit(0, edited);
    await save();
    server.set("assistant", { ...fresh });
    await act(async () => setPairedToken("synthetic-A"));
    await flush();
    expect(getCalls("GET")).toHaveLength(1); // The original write may still have committed.
    intercept = undefined;
    await respond(pending, authResponse(answer));
    expect(inputs().map((input) => input.value)).toEqual([fresh.registry_mirror, fresh.catalog_mirror]);
    expect(unauthorized).not.toHaveBeenCalled();
    expect(reloadForSignIn).not.toHaveBeenCalled();
    expect(host.querySelector('[role="alert"]')).toBeNull();
  });

  it("still signs out for the current owner's 401 after same-token reauthentication", async () => {
    await render();
    await act(async () => setPairedToken("synthetic-A"));
    await flush();
    intercept = (call) => call.method === "PUT" ? jsonError(401) : undefined;
    await edit(0, edited);
    await save();
    expect(unauthorized).toHaveBeenCalledTimes(1);
    expect(reloadForSignIn).not.toHaveBeenCalled();
  });

  it("waits for an old write while retiring a queued B read across A to B to A", async () => {
    const pending = hold();
    intercept = (call) => call.method === "PUT" ? pending.response : undefined;
    await render();
    await edit(0, edited);
    await save();
    const original = getCalls("PUT")[0];
    expect(original).toMatchObject({ token: "Bearer synthetic-A", body: { ...old, registry_mirror: edited } });
    await switchCredential("synthetic-B");
    await switchCredential("synthetic-A");
    expect(getCalls("GET")).toHaveLength(1);
    expect(inputs()).toHaveLength(0);
    server.set("assistant", { ...original.body! }); // A dispatched write is allowed to finish.
    intercept = undefined;
    await respond(pending, json(original.body));
    expect(getCalls("GET").map((call) => call.token)).toEqual(["Bearer synthetic-A", "Bearer synthetic-A"]);
    expect(inputs().map((input) => input.value)).toEqual([edited, old.catalog_mirror]);
    expect(host.querySelector('[role="status"]')).toBeNull();
    await edit(1, fresh.catalog_mirror);
    await save();
    expect(server.get("assistant")).toEqual({ registry_mirror: edited, catalog_mirror: fresh.catalog_mirror });
  });

  it("binds both routing forms to the selected manager despite another view's pin", async () => {
    const unpin = pinManager("/extensions/-", "second");
    try {
      await render();
      expect(getCalls("GET")[0]).toMatchObject({ manager: "assistant", header: "assistant", token: "Bearer synthetic-A" });
      await switchManager();
      expect(inputs().map((input) => input.value)).toEqual([second.registry_mirror, second.catalog_mirror]);
      await edit(0, edited);
      await save();
      expect(getCalls("PUT")[0]).toMatchObject({ manager: "second", header: "second", body: { ...second, registry_mirror: edited } });
      expect(server.get("assistant")).toEqual(old);
    } finally { unpin(); }
  });

  it("preserves same-owner editing, retries failed reads and writes, and clears both fields", async () => {
    let failRead = true, failWrite = true;
    intercept = (call) => {
      if (call.method === "GET" && failRead) { failRead = false; return jsonError(503); }
      if (call.method === "PUT" && failWrite) { failWrite = false; return jsonError(500); }
      return undefined;
    };
    await render();
    expect(inputs()).toHaveLength(0);
    expect(host.querySelector('[role="alert"]')).not.toBeNull();
    await act(async () => button("retry").click()); await flush();
    await edit(0, edited);
    await save();
    expect(inputs()[0].value).toBe(edited);
    expect(host.querySelector('[role="alert"]')).not.toBeNull();
    expect(button("mirror_save").disabled).toBe(false);
    await save();
    expect(server.get("assistant")).toEqual({ ...old, registry_mirror: edited });
    expect(host.querySelector('[role="status"]')).not.toBeNull();
    await act(async () => button("mirror_clear").click()); await flush();
    expect(server.get("assistant")).toEqual({ registry_mirror: "", catalog_mirror: "" });
    expect(inputs().map((input) => input.value)).toEqual(["", ""]);
  });
});

function jsonError(status: number) { return Response.json({ error: "Synthetic failure" }, { status }); }
const authResponse = (kind: string) => kind === "json" ? jsonError(401)
  : kind === "html" ? new Response("<html>Sign in</html>", { status: 401, headers: { "Content-Type": "text/html" } })
    : new Response(null, { status: 302, headers: { Location: "/sign-in" } });

describe("mirror authentication responses", () => {
  it.each(["GET", "PUT"])("honors current %s JSON 401 and proxy responses", async (method) => {
    for (const kind of ["json", "html", "redirect"]) {
      const pending = hold();
      intercept = (call) => call.method === method ? pending.response : undefined;
      if (method === "GET") await render();
      else {
        if (!inputs().length) await render();
        await edit(0, `${edited}/${kind}`);
        await save();
      }
      await respond(pending, authResponse(kind));
      expect(unauthorized).toHaveBeenCalledTimes(kind === "json" ? 1 : 0);
      expect(reloadForSignIn).toHaveBeenCalledTimes(kind === "json" ? 0 : 1);
      unauthorized.mockClear(); vi.mocked(reloadForSignIn).mockClear();
      if (method === "GET") {
        intercept = undefined;
        await act(async () => button("retry").click()); await flush();
        await act(async () => root.unmount());
        root = createRoot(host);
      }
    }
  });

  it.each(["GET", "PUT"] as const)("isolates retired %s 401 and proxy responses after credential or manager changes", async (method) => {
    for (const transition of ["credential", "manager"] as const) {
      for (const kind of ["json", "html", "redirect"]) {
        const pending = hold();
        let captured = false;
        intercept = (call) => {
          if (!captured && call.method === method) { captured = true; return pending.response; }
          return undefined;
        };
        await render();
        if (method === "PUT") { await edit(0, edited); await save(); }
        if (transition === "credential") await switchCredential("synthetic-B");
        else await switchManager();
        await respond(pending, authResponse(kind));
        expect(unauthorized).not.toHaveBeenCalled();
        expect(reloadForSignIn).not.toHaveBeenCalled();
        expect(host.querySelector('[role="alert"]')).toBeNull();
        await act(async () => root.unmount());
        root = createRoot(host);
        history.replaceState(null, "", "/settings?tab=hub");
        await switchCredential("synthetic-A");
      }
    }
  });

  it("keeps unowned probe interception and validated 304 behavior", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(authResponse("html")).mockResolvedValueOnce(new Response(null, { status: 304 })));
    await expect(api("/pair-probe", { token: "synthetic-probe" })).rejects.toMatchObject({ status: 401 });
    expect(reloadForSignIn).toHaveBeenCalledTimes(1);
    expect(unauthorized).not.toHaveBeenCalled();
    const onResponse = vi.fn();
    await expect(api("/cached", { headers: { "If-None-Match": '"synthetic"' }, onResponse })).resolves.toEqual({});
    expect(onResponse).toHaveBeenCalledWith(expect.any(Headers), 304);
    expect(reloadForSignIn).toHaveBeenCalledTimes(1);
  });
});
