// The targets forwards went to lately, latest first (Forward to's Recent, #3899; iOS ForwardMemory): kept in this browser
// for this hub (localStorage is per origin, as Hand to AI's memory, lib/handoff-memory.ts): ids only, at most
// FORWARD_MEMORY_LIMIT, never synced, forgotten on sign-out (lib/disconnect.ts).

export const FORWARD_MEMORY_KEY = "forward.recentTargets";
export const FORWARD_MEMORY_LIMIT = 8;

/** "persona" (the main chat) or "session:<machine>/<session>"; New agent is not a place to go back to. */
export type ForwardTargetId = string;

export const PERSONA_TARGET = "persona";

export function sessionTarget(mid: string, sid: string): ForwardTargetId {
  return `session:${mid}/${sid}`;
}

/** The remembered targets, latest first; what does not read is left out. */
export function forwardTargets(store: Storage = localStorage): ForwardTargetId[] {
  try {
    const list = JSON.parse(store.getItem(FORWARD_MEMORY_KEY) ?? "[]");
    return Array.isArray(list) ? list.filter((x): x is string => typeof x === "string" && (x === PERSONA_TARGET || /^session:[^/]+\/.+/.test(x))) : [];
  } catch {
    return [];
  }
}

/** The remembered sessions (`<machine>/<session>`), latest first. */
export function forwardSessions(store: Storage = localStorage): string[] {
  return forwardTargets(store).flatMap((x) => (x.startsWith("session:") ? [x.slice("session:".length)] : []));
}

export function rememberForward(target: ForwardTargetId, store: Storage = localStorage) {
  const list = [target, ...forwardTargets(store).filter((x) => x !== target)].slice(0, FORWARD_MEMORY_LIMIT);
  try {
    store.setItem(FORWARD_MEMORY_KEY, JSON.stringify(list));
  } catch {
    // a full or blocked storage only loses the memory
  }
}

export function forgetForwards(store: Storage = localStorage) {
  store.removeItem(FORWARD_MEMORY_KEY);
}
