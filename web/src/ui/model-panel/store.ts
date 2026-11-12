// What the model panel keeps (#5032): favorites, recent setups and per-model overrides, in this browser's storage. The
// hub has no store for personal picker preferences, so they stay on the device the way Cindy keeps them per install
// (docs/web/web.md "Model pill and model panel"). An Agent's and a Persona chat's are kept apart (`scope`), and so are two
// hubs served under different sub-paths of one origin (BASE). Every open panel and every tab follows a change at once
// (the storage event for other tabs).
import { useCallback, useMemo, useSyncExternalStore } from "react";
import { BASE } from "../../lib/base";
import { storedValue, storeValue } from "../../lib/compatibility";
import { uuid } from "../../lib/format";
import {
  addFavorite,
  pushRecent,
  removeFavorite,
  resetOverride,
  setOverride,
  updateFavorite,
  type PanelConfig,
  type PanelFavorite,
  type PanelOverride,
  type PanelOverrides,
  type PanelScope,
  RECENT_MAX,
} from "../../lib/model-panel";

type Kind = "favorites" | "recent" | "overrides";

/** The storage key of one kind of record. */
export const panelStoreKey = (scope: PanelScope, kind: Kind, base = BASE) => `persona.modelPanel.${base}.${scope}.${kind}`;

const str = (v: unknown): v is string => typeof v === "string" && v.length > 0;

function setupOf(v: unknown): PanelConfig | null {
  if (!v || typeof v !== "object") return null;
  const o = v as Record<string, unknown>;
  if (!str(o.provider) || !str(o.model)) return null;
  return { provider: o.provider, model: o.model, ...(str(o.effort) ? { effort: o.effort } : {}), ...(o.fast === true ? { fast: true } : {}) };
}

// What a stored value may be; anything else (an older shape, a hand edit) reads as nothing.
const PARSE: { [K in Kind]: (v: unknown) => unknown } = {
  favorites: (v) => (Array.isArray(v) ? v.flatMap((f) => { const s = setupOf(f); return s && str((f as { uid?: unknown }).uid) ? [{ uid: (f as { uid: string }).uid, ...s }] : []; }) : []),
  recent: (v) => (Array.isArray(v) ? v.flatMap((c) => { const s = setupOf(c); return s ? [s] : []; }).slice(0, RECENT_MAX) : []),
  overrides: (v) => {
    if (!v || typeof v !== "object" || Array.isArray(v)) return {};
    const out: PanelOverrides = {};
    for (const [k, o] of Object.entries(v as Record<string, unknown>)) {
      if (!o || typeof o !== "object") continue;
      const r = o as Record<string, unknown>;
      const e: PanelOverride = {};
      if (str(r.effort)) e.effort = r.effort;
      if (typeof r.fast === "boolean") e.fast = r.fast;
      if (Object.keys(e).length) out[k] = e;
    }
    return out;
  },
};
const EMPTY: { [K in Kind]: unknown } = { favorites: [], recent: [], overrides: {} };

// one parsed value per key, read again only when its text changed (a snapshot must be the same object between changes)
const cache = new Map<string, { raw: string | null; value: unknown }>();
const listeners = new Set<() => void>();
// where changes live while storage can't be written (private mode, quota): until the page reloads
const memory = new Map<string, string>();

function read<T>(scope: PanelScope, kind: Kind): T {
  const key = panelStoreKey(scope, kind);
  let raw: string | null = null;
  try {
    raw = memory.get(key) ?? storedValue(key);
  } catch {
    // storage unavailable: nothing kept
  }
  const hit = cache.get(key);
  if (hit && hit.raw === raw) return hit.value as T;
  let value = EMPTY[kind];
  if (raw) {
    try {
      value = PARSE[kind](JSON.parse(raw));
    } catch {
      // not JSON: read as nothing, overwritten on the next change
    }
  }
  cache.set(key, { raw, value });
  return value as T;
}

function write(scope: PanelScope, kind: Kind, value: unknown): void {
  const key = panelStoreKey(scope, kind);
  const raw = JSON.stringify(value);
  try {
    storeValue(key, raw);
    memory.delete(key);
  } catch {
    memory.set(key, raw);
  }
  listeners.forEach((l) => l());
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  if (typeof window !== "undefined") window.addEventListener("storage", l);
  return () => {
    listeners.delete(l);
    if (typeof window !== "undefined") window.removeEventListener("storage", l);
  };
}

/** The records of one scope (read at once, kept in step) and the changes the panel makes to them. */
export function usePanelStore(scope: PanelScope) {
  const favorites = useSyncExternalStore(subscribe, () => read<PanelFavorite[]>(scope, "favorites"));
  const recent = useSyncExternalStore(subscribe, () => read<PanelConfig[]>(scope, "recent"));
  const overrides = useSyncExternalStore(subscribe, () => read<PanelOverrides>(scope, "overrides"));
  const change = useCallback(<T,>(kind: Kind, f: (now: T) => T) => {
    const now = read<T>(scope, kind);
    const next = f(now);
    if (next !== now) write(scope, kind, next);
  }, [scope]);
  return useMemo(() => ({
    favorites,
    recent,
    overrides,
    addFavorite: (c: PanelConfig) => change<PanelFavorite[]>("favorites", (l) => addFavorite(l, c, uuid())),
    removeFavorite: (id: string) => change<PanelFavorite[]>("favorites", (l) => removeFavorite(l, id)),
    updateFavorite: (id: string, patch: PanelOverride) => change<PanelFavorite[]>("favorites", (l) => updateFavorite(l, id, patch)),
    pushRecent: (c: PanelConfig) => change<PanelConfig[]>("recent", (l) => pushRecent(l, c)),
    setOverride: (provider: string, model: string, patch: PanelOverride) => change<PanelOverrides>("overrides", (o) => setOverride(o, provider, model, patch)),
    resetOverride: (provider: string, model: string) => change<PanelOverrides>("overrides", (o) => resetOverride(o, provider, model)),
  }), [favorites, recent, overrides, change]);
}
