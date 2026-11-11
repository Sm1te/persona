import { api, enc } from "./api";
import { t } from "../i18n";
import { MANAGER_HEADER } from "./manager-choice";

/** Download preferences belong to one extension manager, not the hub's global settings. */
export interface ExtensionMarketSettings {
  registry_mirror: string;
  catalog_mirror: string;
}

// This endpoint has no legacy /plugins fallback. Pin each request to the editor's owner, including the default.
const settingsPath = (manager: string) => `/extensions/-/market-settings?manager=${enc(manager)}`;

/** A malformed read is not empty settings: do not let a subsequent save overwrite unknown values. */
function settingsOf(value: ExtensionMarketSettings): ExtensionMarketSettings {
  if (!value || typeof value.registry_mirror !== "string" || typeof value.catalog_mirror !== "string") {
    throw new Error(t("settings-hubsettings.mirror_response_invalid"));
  }
  return { registry_mirror: value.registry_mirror, catalog_mirror: value.catalog_mirror };
}

// A write outlives its editor. Returning to that manager must read only after it settles,
// including failures (the manager may have committed before the connection failed).
const writes = new Map<string, Promise<void>>();

export type MirrorRequestContext = {
  token: string;
  tokenOwner: string;
  isAuthContextCurrent: () => boolean;
  signal?: AbortSignal;
};

function requireCurrent(context: MirrorRequestContext) {
  if (context.signal?.aborted || !context.isAuthContextCurrent()) throw new DOMException("Retired mirror editor", "AbortError");
}

export async function getExtensionMarketSettings(manager: string, context: MirrorRequestContext): Promise<ExtensionMarketSettings> {
  while (writes.has(manager)) await writes.get(manager);
  // A read can wait for an older write; never start it with the replacement editor's identity.
  requireCurrent(context);
  return settingsOf(await api<ExtensionMarketSettings>(settingsPath(manager), { ...context, headers: { [MANAGER_HEADER]: manager } }));
}

/** Both fields are sent; an empty string explicitly removes that mirror. */
export function setExtensionMarketSettings(manager: string, settings: ExtensionMarketSettings, context: MirrorRequestContext): Promise<ExtensionMarketSettings> {
  requireCurrent(context);
  const result = api<ExtensionMarketSettings>(settingsPath(manager), { ...context, headers: { [MANAGER_HEADER]: manager }, method: "PUT", json: settings }).then(settingsOf);
  const settled = Promise.all([writes.get(manager), result.then(() => {}, () => {})]).then(() => {
    if (writes.get(manager) === settled) writes.delete(manager);
  });
  writes.set(manager, settled);
  return result;
}

/** HTTPS only, with no embedded credentials, query or fragment; registry URLs may include a path prefix. */
export function validMirrorURL(value: string): boolean {
  if (!value) return true;
  try {
    const u = new URL(value);
    return /^https:\/\//i.test(value) && u.protocol === "https:" && !!u.hostname && !u.username && !u.password && !u.search && !u.hash;
  } catch {
    return false;
  }
}
