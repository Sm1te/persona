// Derived lists the sidebar, the home page and the palette share.
import { useEffect, useMemo, useSyncExternalStore } from "react";
import { credentialOwner } from "./api";
import { CompleteSessions } from "./complete-sessions";
import { stateOf } from "./format";
import { foldedState } from "./child-sessions";
import { foldRows } from "./folded-rows";
import { getState, onHubEvent, subscribe, useStore } from "./store";
import { SESSIONS_LIST_FEATURE } from "./session-list";
import type { Session } from "./types";

/** The agents the lists show and count: without the child sessions folded under one of them, each parent in its folded
 *  state (lib/folded-rows.ts, #5120). A child's own row is in the store (`sessions`, `sessionSummaries`) for its card. */
export function useAgentSessions(): Session[] {
  const by = useStore((s) => s.sessions);
  const summaries = useStore((s) => s.sessionSummaries);
  const bounded = useStore((s) => s.features.includes(SESSIONS_LIST_FEATURE));
  // folded before the assistant's conversations go: a child folded under one of them is no Agent of its own either
  return useMemo(() => foldRows(bounded ? summaries : Object.values(by).flat()).filter((s) => !s.assistant), [by, summaries, bounded]);
}

const EMPTY_FULL_LIST = { sessions: [] as Session[], loaded: false, base: {} as Record<string, Session[]> };
const FULL_LIST_RETRY_MS = 5_000;
// Working agents emit list hints for every display update. Space reads from their completion so hints that keep
// arriving share one read. The complete list is read once; later reads ask only for what changed (complete-sessions.ts).
const FULL_LIST_REFRESH_MS = 3_000;

// A short handoff window covers Sidebar → AgentsPage and StrictMode remounts, not a permanent
// full-list cache. Only the active credential partition can own this one shared transport.
const FULL_LIST_GRACE_MS = 1_000;
let fullSnapshot = { ...EMPTY_FULL_LIST, epoch: 0 };
const fullListeners = new Set<() => void>();
const subscribeFull = (listener: () => void) => {
  fullListeners.add(listener);
  return () => { fullListeners.delete(listener); };
};
const fullChanged = () => fullListeners.forEach((listener) => listener());
const getFullSnapshot = () => fullSnapshot;
let fullCache: ReturnType<typeof createFullCache> | undefined;

function createFullCache(owner: string) {
  let stopped = false, consumers = 0, pending = false, dirty = false, urgent = false;
  let nextReadAt = 0;
  let controller: AbortController | undefined;
  const complete = new CompleteSessions();
  let retry: ReturnType<typeof setTimeout> | undefined;
  let trailing: ReturnType<typeof setTimeout> | undefined;
  let expiry: ReturnType<typeof setTimeout> | undefined;
  const valid = () => !stopped && getState().loaded && credentialOwner() === owner
    && getState().features.includes(SESSIONS_LIST_FEATURE);
  const read = async (immediate = false) => {
    if (!valid()) return;
    if (!consumers || pending) { dirty = true; urgent ||= immediate; return; }
    const delay = nextReadAt - Date.now();
    if (!immediate && delay > 0) {
      // Hints share one fixed trailing deadline, even across consumer handoffs.
      trailing ??= setTimeout(() => { trailing = undefined; void read(); }, delay);
      return;
    }
    clearTimeout(trailing);
    trailing = undefined;
    clearTimeout(retry);
    retry = undefined;
    dirty = urgent = false;
    pending = true;
    controller = new AbortController();
    const base = getState().sessions;
    try {
      const result = await complete.read(controller.signal);
      // An unchanged delta leaves the published snapshot (and its older base, which only widens the overlay) as it is.
      if (valid() && (result.changed || !fullSnapshot.loaded)) {
        fullSnapshot = { sessions: foldRows(result.sessions).filter((s) => !s.assistant), loaded: true, base, epoch: fullSnapshot.epoch };
        fullChanged();
      }
    } catch {
      // An initial failure or quiet disconnected hub still needs a retry. During grace, retain
      // the retry intent but do not download data without a consumer.
      if (valid()) {
        dirty = true;
        if (consumers) retry = setTimeout(() => { retry = undefined; void read(true); }, FULL_LIST_RETRY_MS);
      }
    } finally {
      pending = false;
      nextReadAt = Date.now() + FULL_LIST_REFRESH_MS;
      if (valid() && consumers && dirty && retry === undefined) void read(urgent);
    }
  };
  const off = ["sessions_list", "session", "sessions", "sessions_delta", "hello"].map((event) => onHubEvent(event, (msg) => {
    // A hint for the revision this copy already holds (the one on connect, a burst's repeat) needs no read.
    if (event === "sessions_list" && complete.holds(msg?.revision)) return;
    void read(event === "hello");
  }));
  const dispose = () => {
    if (stopped) return;
    stopped = true;
    controller?.abort();
    clearTimeout(retry);
    clearTimeout(trailing);
    clearTimeout(expiry);
    off.forEach((unsubscribe) => unsubscribe());
    fullCache = undefined;
    fullSnapshot = { ...EMPTY_FULL_LIST, epoch: fullSnapshot.epoch + 1 };
    fullChanged();
  };
  // Keep this fence subscribed through grace: a reset followed by the same login must not
  // revive an old snapshot, even when React batches both store publications into one commit.
  off.push(subscribe(() => { if (!valid()) dispose(); }));
  return {
    owner,
    retain() {
      consumers++;
      clearTimeout(expiry);
      expiry = undefined;
      if (!pending && (dirty || !fullSnapshot.loaded) && retry === undefined) void read(urgent || !fullSnapshot.loaded);
      return () => {
        if (stopped || --consumers) return;
        clearTimeout(retry);
        retry = undefined;
        if (trailing !== undefined) dirty = true;
        clearTimeout(trailing);
        trailing = undefined;
        expiry = setTimeout(dispose, FULL_LIST_GRACE_MS);
      };
    },
    dispose,
  };
}

/** Pins, grouping and the per-assistant lists need complete rows, including agents outside the bounded first page.
 * Consumers share a scoped snapshot and request, released shortly after the last one leaves. Folded like
 * useAgentSessions: no child session folded under a listed one, each parent in its folded state. */
export function useFullAgentSessions(on = true): { sessions: Session[]; loaded: boolean } {
  const full = useCompleteAgentSessions(on);
  return useMemo(() => {
    const sessions = foldRows(full.sessions);
    return sessions === full.sessions ? full : { sessions, loaded: full.loaded };
  }, [full]);
}

function useCompleteAgentSessions(on: boolean): { sessions: Session[]; loaded: boolean } {
  const by = useStore((s) => s.sessions);
  const bounded = useStore((s) => s.features.includes(SESSIONS_LIST_FEATURE));
  const loaded = useStore((s) => s.loaded);
  const snapshot = useSyncExternalStore(subscribeFull, getFullSnapshot, getFullSnapshot);
  const owner = on ? credentialOwner() : "";
  const full = fullCache?.owner === owner ? snapshot : EMPTY_FULL_LIST;
  useEffect(() => {
    if (!on || !bounded || !loaded) return;
    if (fullCache && fullCache.owner !== owner) fullCache.dispose();
    fullCache ??= createFullCache(owner);
    return fullCache.retain();
  }, [on, bounded, loaded, owner, snapshot.epoch]);
  return useMemo(() => {
    // `on` false: the caller has no use for the list now and asks the hub for nothing
    if (!on) return EMPTY_FULL_LIST;
    if (!bounded) return { sessions: foldRows(Object.values(by).flat()).filter((s) => !s.assistant), loaded };
    if (!loaded) return EMPTY_FULL_LIST;
    if (by === full.base) return full;
    // Reflect actions on retained details (rename, mark seen) without copying the complete list into the store.
    // Unchanged cached details may predate this HTTP snapshot, so only apply rows changed since its request.
    const key = (s: Session) => JSON.stringify([s.machine_id, s.session_id]);
    const before = new Map(Object.values(full.base).flat().map((s) => [key(s), s]));
    const current = new Map(Object.values(by).flat().map((s) => [key(s), s]));
    const sessions = full.sessions.filter((s) => !before.has(key(s)) || current.has(key(s))).map((s) => {
      const next = current.get(key(s));
      return next && next !== before.get(key(s)) ? next : s;
    });
    return { sessions, loaded: full.loaded };
  }, [on, bounded, loaded, full, by]);
}

/** The agents by state. `badge` is the sidebar's "Needs you" count: without quiet tasks' agents, which the main conversation
 * tells about (#1731). */
export function stateCounts(all: Session[]) {
  const c = { needs: 0, working: 0, unread: 0, badge: 0 };
  for (const s of all) {
    const st = stateOf(s);
    if (st === "needs_you") {
      c.needs++;
      if (!s.quiet) c.badge++;
    } else if (st === "working") c.working++;
    else if (st === "unread") c.unread++;
  }
  return c;
}

/** `badge` without `open`, the agent on screen: it says it is waiting itself, so a count of only it would tell nothing.
 *  `quietCounted`: `badge` is the hub's own count, which has quiet agents in it. */
export function othersWaiting(badge: number, open: Session | undefined, quietCounted = false): number {
  return Math.max(0, badge - (open && !open.assistant && (quietCounted || !open.quiet) && foldedState(open) === "needs_you" ? 1 : 0));
}

export function useCounts() {
  const all = useAgentSessions();
  const interactions = useStore((s) => s.interactions.length);
  const counts = useStore((s) => s.sessionCounts);
  const bounded = useStore((s) => s.features.includes(SESSIONS_LIST_FEATURE));
  return useMemo(() => {
    if (!bounded || !counts) return { ...stateCounts(all), interactions, quietCounted: false };
    const needs = counts.state.needs_you ?? 0;
    return { needs, working: counts.state.working ?? 0, unread: counts.state.unread ?? 0, badge: needs, interactions, quietCounted: true };
  }, [all, interactions, counts, bounded]);
}
