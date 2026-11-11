import { storedCredentials, storeCredentials, CLIENT_HEADERS, OP_HEADERS, compatibleEnvelope, requestForHub } from "./compatibility";
// REST client for the hub. The page is served by the hub itself (same origin), so every path is relative.

import { MANAGER_HEADER, managerHeaderFor } from "./manager-choice";
import { BASE } from "./base";
import { intercepted, reloadForSignIn } from "./edge-auth";
import { requestCrumb } from "./error-reports";
import { currentLanguage, t } from "../i18n";
import type { Item } from "./types";
import { clearFeedCache } from "./feed-cache";
import { credentialEpoch, renewCredentialEpoch } from "./credential-epoch";

let ownerToken: string | undefined;
let ownerEpoch: string | undefined;
let ownerGeneration = 0;
const credentialListeners = new Set<(owner: string) => void>();
function publishCredentialOwner(): string {
  const owner = `credential-${++ownerGeneration}`;
  credentialListeners.forEach((listener) => listener(owner));
  return owner;
}
/** Opaque in-memory auth partition; never expose credentials in tree keys or persisted drafts. */
export function credentialOwner(): string {
  const token = getToken();
  const epoch = credentialEpoch();
  if (token !== ownerToken || epoch !== ownerEpoch) {
    ownerToken = token;
    ownerEpoch = epoch;
    return publishCredentialOwner();
  }
  return `credential-${ownerGeneration}`;
}

/** Retire work when pairing or sign-out replaces its credential, including a return to the same token. */
export function onCredentialChange(listener: (owner: string) => void): () => void {
  credentialListeners.add(listener);
  return () => { credentialListeners.delete(listener); };
}

/** React editors follow the shared credential subscription and other tabs' storage changes. */
export function subscribeCredentialOwner(listener: () => void): () => void {
  const stop = onCredentialChange(listener);
  const storage = () => { credentialOwner(); };
  window.addEventListener("storage", storage);
  return () => { stop(); window.removeEventListener("storage", storage); };
}

export function getToken(): string {
  return storedCredentials().token;
}

export function setToken(token: string, tokenId?: string) {
  if (token !== getToken() || !token) clearFeedCache();
  storeCredentials(token, tokenId);
  credentialOwner();
}

/** A successful sign-in is a new owner even if the hub returned the same token. */
export function setPairedToken(token: string, tokenId?: string) {
  credentialOwner();
  storeCredentials(token, tokenId);
  const shared = renewCredentialEpoch();
  clearFeedCache();
  if (shared) credentialOwner();
  else {
    ownerToken = getToken();
    ownerEpoch = credentialEpoch();
    publishCredentialOwner();
  }
}

export class ApiError extends Error {
  status: number;
  body: Record<string, unknown>;
  isJson: boolean;
  /** The hub logs a failed request under this id (X-Request-Id): for a report, not for the message itself. */
  requestId?: string;
  constructor(status: number, message: string, body: Record<string, unknown>, isJson = true, requestId?: string, readonly requestPath?: string) {
    super(message);
    this.status = status;
    this.body = body;
    this.isJson = isJson;
    this.requestId = requestId;
  }
  get code(): string | undefined {
    const code = typeof this.body.code === "string" ? this.body.code : undefined;
    const path = this.requestPath?.split("?")[0];
    // Older hubs lost this business code. Match only their exact refusal on knowledge/recording routes,
    // never a phrase in an arbitrary internal error. Keep the original body for diagnostics.
    const knowledge = path === "/kb" || path?.startsWith("/kb/") || path === "/kb-search"
      || path === "/recordings" || path?.startsWith("/recordings/");
    if (knowledge && this.status === 503 && code === "internal" && this.body.detail === "the knowledge base is off") return "kb_disabled";
    return code;
  }
}

let onUnauthorized: () => void = () => {};
/** What a 401 does from now on; returns what it did before, for a caller that sets it only for a while. */
export function handleUnauthorized(fn: () => void): () => void {
  const before = onUnauthorized;
  onUnauthorized = fn;
  return before;
}

/**
 * Who this page is to the hub: the platform and version its socket's client_hello names, and every request's
 * X-Persona-Client. The hub answers a request with what this page's socket declared (MCP forms and links: only to a
 * request whose platform and version match a connection that declared them, docs/protocol/protocol.md "MCP elicitation").
 */
export const CLIENT_PLATFORM = "web";
export const CLIENT_VERSION = "1";
/** What this page can render beyond the basics (client_hello `features`). `persona_card_ring`: it draws ```persona-card
 *  rings (lib/assistant-view.ts); a client without it gets each ring as a stacked bar (the hub's card_compat.go). */
/** `managers`: the hub pushes {"type":"managers"} when its roster changes (Settings › Managers, lib/managers.ts). */
/**
 * The pong timeout (seconds) this page asks the hub for in client_hello. The hub's default of 15 s is tight on a
 * high-latency public link whose outbound path changes now and then: a ping or its pong lost across a switch closed
 * the socket (pong_timeout) every 30-250 s. 45 s rides through a switch yet stays under the page's own 70 s quiet
 * limit, so a dead link is still dropped. Only the timeout is asked for: the 20 s interval stays the hub's.
 */
export const CLIENT_PONG_TIMEOUT = 45;
export const CLIENT_FEATURES = ["elicitation", "extension_operations", "extension_auth", "managers", "extension_ui_stream_v1", "persona_contracts_v1", "persona_card_ring"];

// A read the hub has not answered by then fails instead of loading forever (#4492): a restarting hub, a stuck node or a
// half-dead network. Longer than the hub's own cut-offs for asking a computer (30–40 s), so when the hub has an error
// that says which computer, that one arrives first. Writes have none: one that took effect must not read as failed.
export const READ_TIMEOUT_MS = 45_000;

/** The `timeout` for a read the hub itself gives `hubMs` or longer (a node that starts a CLI, a gateway): a few seconds
 *  past the hub's own limit, so the hub's error (which computer, which launcher) or a late answer comes first (#5035). */
export const pastHub = (hubMs: number): number => hubMs + 5_000;

/** The waits before a read's second and third try: a dropped connection or a proxy's 502 usually clears within seconds.
 *  None under Vitest, whose stub hubs answer once and whose failures would otherwise all wait these out. */
const readRetryMs = () => (import.meta.env.MODE === "test" ? [] : [300, 1_000]);

/** The hub did not answer a read within its time (`api`'s `timeout`). */
export class HubTimeoutError extends Error {
  constructor() {
    super(t("lib-api.hub_no_response"));
    this.name = "HubTimeoutError";
  }
}

type ApiOptions = RequestInit & { json?: unknown; token?: string; tokenOwner?: string; isAuthContextCurrent?: () => boolean; onResponse?: (headers: Headers, status: number) => void };

/** `tokenOwner`: explicit credentials opt into 401 recovery only while both the token and credential generation are current.
 *  `isAuthContextCurrent`: an optional additional lifetime fence for a request scoped to a view or manager.
 *  `timeout`: ms before a read gives up with HubTimeoutError (default READ_TIMEOUT_MS for GET, none otherwise; 0: none). */
export function api<T>(path: string, init: ApiOptions & { timeout?: number } = {}): Promise<T> {
  return call<T>(path, init, false);
}

/** A hub route's bytes (an image it serves, such as a meeting photo's) with `api`'s credentials, headers, time limit and
 *  errors: for what an `<img>` can't fetch itself, since it sends no header (the bearer token, the manager's). */
export function apiBlob(path: string, init: ApiOptions & { timeout?: number } = {}): Promise<Blob> {
  return call<Blob>(path, init, true);
}

async function call<T>(path: string, init: ApiOptions & { timeout?: number }, binary: boolean): Promise<T> {
  const { timeout, ...rest } = init;
  const read = (init.method ?? "GET").toUpperCase() === "GET";
  const limit = timeout ?? (read ? READ_TIMEOUT_MS : 0);
  if (!limit) return request<T>(path, rest, binary);
  // The caller's own signal still aborts as before (an AbortError); only the time running out becomes HubTimeoutError.
  const controller = new AbortController();
  const outer = init.signal;
  const forward = () => controller.abort(outer?.reason);
  if (outer?.aborted) forward();
  else outer?.addEventListener("abort", forward, { once: true });
  let late = false;
  const timer = setTimeout(() => {
    late = true;
    controller.abort();
  }, limit);
  try {
    for (let tries = 0; ; tries++) {
      try {
        return await request<T>(path, { ...rest, signal: controller.signal }, binary);
      } catch (e) {
        // asking again is safe for a read: a gap gets a couple more tries, all inside the read's own time limit
        if (!read || tries >= readRetryMs().length || !isTransient(e)) throw e;
        await pause(readRetryMs()[tries], controller.signal);
      }
    }
  } catch (e) {
    throw late && isAbort(e) ? new HubTimeoutError() : e;
  } finally {
    clearTimeout(timer);
    outer?.removeEventListener("abort", forward);
  }
}

function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((done, fail) => {
    const timer = setTimeout(done, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      fail(new DOMException("aborted", "AbortError"));
    }, { once: true });
  });
}

async function request<T>(path: string, init: ApiOptions, binary = false): Promise<T> {
  const { onResponse, isAuthContextCurrent, ...fetchOptions } = init;
  const owner = init.token === undefined ? credentialOwner() : init.tokenOwner;
  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${init.token ?? getToken()}`);
  // the hub writes its error and status text in the interface language (docs/protocol/i18n.md "Protocol")
  if (!headers.has("Accept-Language")) headers.set("Accept-Language", currentLanguage());
  CLIENT_HEADERS.forEach(name => headers.set(name, `${CLIENT_PLATFORM}/${CLIENT_VERSION}`));
  const op = OP_HEADERS.map(name => headers.get(name)).find(Boolean);
  if (op) OP_HEADERS.forEach(name => headers.set(name, op));
  // the manager the assistant page shows, on the assistant's requests (lib/manager-choice.ts); none for the default
  const manager = managerHeaderFor(path);
  if (manager && !headers.has(MANAGER_HEADER)) headers.set(MANAGER_HEADER, manager);
  let body = init.body;
  if (init.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(requestForHub(path, init.json));
  }
  const method = init.method ?? "GET";
  let res: Response;
  try {
    // manual: a redirect (the sign-in layer in front of the hub has expired) arrives as one instead of being followed
    // into a cross-origin page this script cannot read; the hub itself never redirects its API
    res = await fetch(`${BASE}/api/v1${path}`, { redirect: "manual", ...fetchOptions, headers, body });
  } catch (e) {
    // a breadcrumb for an error report: the method, the path's pattern (no ids, no query) and that it did not get there
    if (!isAbort(e)) requestCrumb(method, path, 0);
    throw e;
  }
  // A 304 answers a request that sent a validator (If-None-Match): the caller keeps what it has. It is neither the
  // sign-in layer's redirect nor an error.
  const notModified = res.status === 304 && headers.has("If-None-Match");
  if (!notModified && intercepted(res)) {
    requestCrumb(method, path, res.status);
    // Retired requests cannot reload a replacement view or credential; an unowned pairing probe can still sign in.
    if ((isAuthContextCurrent?.() ?? true)
      && (owner === undefined || (owner === credentialOwner() && (init.token === undefined || init.token === getToken())))) {
      reloadForSignIn(); // the layer in front takes over the page and sends it to its sign-in
    }
    throw new ApiError(res.status || 401, t("lib-api.hub_no_response"), {}, false);
  }
  if (binary && res.ok) {
    onResponse?.(res.headers, res.status);
    return (await res.blob()) as T;
  }
  const text = await res.text();
  let data: Record<string, unknown> = {};
  let isJson = false;
  try {
    data = text ? JSON.parse(text) : {};
    isJson = typeof data === "object" && data !== null && !Array.isArray(data);
  } catch {
    // a proxy's HTML error page says nothing a person can use: the status stands in for it
    data = { error: /^\s*</.test(text) ? "" : text };
    isJson = false;
  }
  if (!res.ok && !notModified) {
    requestCrumb(method, path, res.status);
    if (res.status === 401 && owner !== undefined && owner === credentialOwner() && (init.token === undefined || init.token === getToken())
      && (isAuthContextCurrent?.() ?? true)) onUnauthorized();
    const msg = typeof data.error === "string" && data.error ? data.error : `HTTP ${res.status}`;
    throw new ApiError(res.status, msg, data, isJson, res.headers?.get("X-Request-Id") ?? undefined, path);
  }
  onResponse?.(res.headers, res.status);
  return compatibleEnvelope(data) as T;
}

export const enc = encodeURIComponent;

export function sessionPath(mid: string, sid: string) {
  return `/sessions/${enc(mid)}/${enc(sid)}`;
}

export function attachSession(mid: string, sid: string): Promise<unknown> {
  return api(`${sessionPath(mid, sid)}/attach`, { method: "POST" });
}

// An <img> can't send a header: the hub also takes the token as a query parameter (it never logs queries).
export function imageURL(mid: string, sid: string, itemID: string) {
  return `${BASE}/api/v1${sessionPath(mid, sid)}/items/${enc(itemID)}/image?token=${enc(getToken())}`;
}

/** An item whole, when the page carries it cut (Item.full_size): `GET …/items/{id}/full`. */
export function fullItem(mid: string, sid: string, itemID: string): Promise<Item> {
  return api<Item>(`${sessionPath(mid, sid)}/items/${enc(itemID)}/full`);
}

/** A machine file's bytes by Range (docs/protocol/protocol.md "File preview"): `<video>` / `<audio>` seek through it, a viewer
 * fetches it whole. `inline` lets the browser show it in place (images, media, PDF only; anything else downloads). */
export function fileDownloadURL(mid: string, path: string, cwd?: string, opts: { inline?: boolean } = {}) {
  return `${BASE}/api/v1/machines/${enc(mid)}/file/download?path=${enc(path)}&cwd=${enc(cwd ?? "")}${opts.inline ? "&inline=1" : ""}&token=${enc(getToken())}`;
}

// Fresh legacy URLs must miss the browser cache after this client upgrades. A page-load identity also retries
// after the node upgrades later; the same URL remains reusable while this page is open.
const thumbnailCacheEpoch = `v2-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;

/** A machine file's thumbnail, at most `size` px on its long side; 404 `no_thumbnail` when the machine can't draw it. */
export function fileThumbURL(mid: string, path: string, cwd?: string, size = 256) {
  return `${BASE}/api/v1/machines/${enc(mid)}/file/thumb?path=${enc(path)}&cwd=${enc(cwd ?? "")}&size=${size}&token=${enc(getToken())}&thumb=${thumbnailCacheEpoch}`;
}

export function isAbort(e: unknown) {
  return e instanceof DOMException && e.name === "AbortError";
}

/** Whether the request may not have reached a running hub: fetch failed, or something in front of it answered 502/503/504. */
export function isTransient(e: unknown): boolean {
  if (e instanceof TypeError) return true; // fetch: the connection failed (an abort is a DOMException)
  // the hub's own errors carry a code or say what was delivered (a machine offline, a node timing out): those are
  // answers, not gaps
  return e instanceof ApiError && [502, 503, 504].includes(e.status) && (!e.isJson || (!e.code && e.body.delivered === undefined));
}

/** A coded business refusal is an answer, not evidence of a hub outage. `delivered: none` marks a plainly offline
 * computer where nothing went out. Original fields stay on ApiError for diagnostics, never interpolated here. */
function businessFailureText(e: ApiError): string | undefined {
  if (e.code === "kb_disabled") return t("lib-api.knowledge_disabled");
  if (e.code === "kb_unavailable") return t("lib-api.knowledge_unavailable");
  if (e.code === "kb_read_only") return t("lib-api.knowledge_read_only");
  if (e.code === "assistant_offline") return t("lib-api.manager_offline");
  if (e.code === "node_unreachable") return t(e.body.delivered === "none" ? "lib-api.computer_offline" : "lib-api.computer_dropped");
  if (e.code === "timeout") return t("lib-api.computer_timeout");
  // a stopped Agent takes no messages (#4754): Resume on its terminal page runs it again
  if (e.code === "agent_stopped") return t("lib-api.agent_stopped");
  return undefined;
}

export function errorText(e: unknown): string {
  if (e instanceof ApiError) {
    const business = businessFailureText(e);
    if (business) return business;
    // Keep both the status and the original reason for diagnostics and existing error-specific explanations.
    const status = `HTTP ${e.status}`;
    const detail = e.code === "internal" && e.status >= 500 ? status
      : e.message && e.message !== status ? `${status}: ${e.message}` : status;
    if (e.status === 404 || e.code === "not_found" || /^not[ _]found$/i.test(e.message.trim())) {
      return t("lib-api.not_found", { detail });
    }
    if (e.status === 502 || e.status === 503 || e.status === 504) return t("lib-api.hub_unavailable", { detail });
    if (e.status >= 500 && e.status < 600) return t("lib-api.server_error", { detail });
    return e.message;
  }
  if (e instanceof TypeError) return t("lib-api.cant_reach_hub");
  return e instanceof Error ? e.message : String(e);
}
