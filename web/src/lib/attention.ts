// Attention signals of the web client (#2332): the tab title's count, the favicon's badge and the optional sounds. This
// file is the logic without a DOM (tested in attention.test.ts); lib/attention-signals.ts draws and plays.
//
// The count is the sidebar's own red "Needs you" count (stateCounts().badge in lib/sessions.ts: quiet tasks' agents are told
// about in the main conversation, not counted), so the tab and the sidebar never disagree.
import { useSyncExternalStore } from "react";
import { stateOf } from "./format";
import { agentKey } from "./pins";
import { homeKey } from "./folded-rows";
import { isDelegating } from "./delegate";
import { isMuted } from "./session-actions";
import type { AgentMutes } from "./agent-groups";
import { byTime } from "./times";
import type { AgentState, Interaction, Session } from "./types";

const SITE = "Persona";

/** The tab title: the page's label, `(N) ` in front while N sessions need you. */
export function tabTitle(label: string, needs: number): string {
  const page = !label || label === SITE ? SITE : `${label} · ${SITE}`;
  return needs > 0 ? `(${needs}) ${page}` : page;
}

/** What the favicon's badge says: the count up to 9, "9+" beyond (it is drawn in 16–32 px); nothing for 0. */
export function badgeText(needs: number): string {
  if (needs <= 0) return "";
  return needs > 9 ? "9+" : String(needs);
}

/** `card`: a "Needs you" one about a new card of a session that was already "Needs you" (detectNewCards). */
export type AttentionEvent = { kind: "needs" | "finished"; key: string; card?: Interaction; follows?: boolean };

/**
 * What changed between two snapshots of the sessions' states (`prev`: the states of the last one, by agentKey).
 * - needs: a session became "Needs you" (not a quiet task's: the sidebar does not count it either).
 * - finished: a session went "Working" → finished unread. This mirrors the hub's "finished" push (hub/machine.go notifyFinished,
 *   notify.go settle): the push is for a turn that ended and has not been seen (unread, not idle), is dropped for a
 *   muted or quiet session, and a session with background work still running stays "Working" (backgroundState), so it
 *   never ends here. Differences: the hub waits `done_delay` and re-checks; here the caller re-checks after its own
 *   short wait (SETTLE_MS). The hub also drops a turn started by a background task's report (`WokeByTask`) and one
 *   the assistant already pushed about; the web client does not see those and chimes for them.
 * A session with no earlier state (the first snapshot, a new session) produces nothing, so opening the page does not chime.
 * A session "Needs you" only for questions the assistant is still looking at (`interactions`, lookingOnly) counts as
 * working: the hub pushes nothing for them either, and it becomes "Needs you" here once the assistant hands one to the user.
 */
export function detectTransitions(prev: ReadonlyMap<string, AgentState>, sessions: Session[], mutes: AgentMutes, interactions: readonly Interaction[] = []): { next: Map<string, AgentState>; events: AttentionEvent[] } {
  const next = new Map<string, AgentState>();
  const events: AttentionEvent[] = [];
  const looking = lookingOnly(interactions);
  for (const s of sessions) {
    const key = agentKey(s);
    const state = stateOf(s);
    const now = state === "needs_you" && looking.has(key) ? "working" : state;
    next.set(key, now);
    const was = prev.get(key);
    if (was === undefined || was === now || s.quiet) continue;
    if (now === "needs_you") events.push({ kind: "needs", key });
    else if (was === "working" && now === "unread" && !isMuted(s, mutes)) events.push({ kind: "finished", key });
  }
  return { next, events };
}

/** The sessions (by agentKey) whose every pending card is a question the assistant is still looking at (#544). */
export function lookingOnly(interactions: readonly Interaction[]): Set<string> {
  const looking = new Set<string>();
  const other = new Set<string>();
  for (const ix of interactions) (isDelegating(ix) ? looking : other).add(homeKey(ix));
  for (const key of other) looking.delete(key);
  return looking;
}

/**
 * The notification tags (`<agentKey>#needs` / `#finished`, as attention-signals posts them) whose event is over between
 * two snapshots: a session no longer "Needs you", or no longer finished unread (seen here or elsewhere), or gone from the list. Their
 * system notifications are taken back, so one answered on the phone does not wait in the notification centre (#3458).
 */
export function settledTags(prev: ReadonlyMap<string, AgentState>, next: ReadonlyMap<string, AgentState>): string[] {
  const out: string[] = [];
  for (const [key, was] of prev) {
    const now = next.get(key);
    if (was === now) continue;
    if (was === "needs_you") out.push(`${key}#needs`);
    else if (was === "unread") out.push(`${key}#finished`);
  }
  return out;
}

/** A new card for a session that was already "Needs you" (#4058). `follows`: the session's "Needs you" came before any card
 *  (a Codex approval is read off the screen after the pane blocks), so this card is that same wait: its notification
 *  is written again from the card, without a sound. */
export type CardEvent = { kind: "needs"; key: string; card: Interaction; follows?: boolean };

/** How long a card id is remembered after it was last pending: one that drops out and comes back (a reconnect, a
 *  screen re-read) is not new. */
export const FORGET_CARD_MS = 10 * 60_000;

/**
 * The cards that arrived between two snapshots for a session that stayed "Needs you" (`was` the states of the last snapshot,
 * `next` this one's, as detectTransitions works them out). A session that only now becomes "Needs you" is detectTransitions'
 * (its notification is about its oldest card), so a card is announced once: by the transition or here; `bare` is the
 * sessions whose transition had no card yet, whose first card follows it (CardEvent.follows). `known` is the card ids
 * seen pending, with when they last were, undefined before the first snapshot (opening the page announces nothing).
 * Quiet tasks' sessions are left out, as detectTransitions does; several new cards for one session make one event, the oldest.
 * A question the assistant is still looking at is neither announced nor remembered: when it hands the same card to the
 * user, that is when it arrives.
 */
export function detectNewCards(known: ReadonlyMap<string, number> | undefined, interactions: readonly Interaction[], was: ReadonlyMap<string, AgentState>, next: ReadonlyMap<string, AgentState>, sessions: readonly Session[], bare: ReadonlySet<string>, now: number): { ids: Map<string, number>; events: CardEvent[] } {
  const ids = new Map<string, number>();
  for (const [id, at] of known ?? []) if (now - at < FORGET_CARD_MS) ids.set(id, at);
  const waiting = interactions.filter((ix) => !isDelegating(ix));
  for (const ix of waiting) ids.set(ix.id, now);
  if (!known) return { ids, events: [] };
  const quiet = new Set(sessions.filter((s) => s.quiet).map(agentKey));
  const first = new Map<string, Interaction>();
  for (const ix of waiting) {
    if (known.has(ix.id)) continue;
    // a child session's card is announced for its parent (#5120), the session the lists show and its link opens
    const key = ix.session_id === "" ? `card:${ix.id}` : homeKey(ix);
    if (ix.session_id === "") { first.set(key, ix); continue; }
    if (quiet.has(key) || was.get(key) !== "needs_you" || next.get(key) !== "needs_you") continue;
    const had = first.get(key);
    if (!had || byTime(ix.created_at, had.created_at) < 0) first.set(key, ix);
  }
  return { ids, events: [...first].map(([key, card]) => (bare.has(key) ? { kind: "needs", key, card, follows: true } : { kind: "needs", key, card })) };
}

/**
 * The "Needs you" tags whose notification was about a card that is no longer pending (answered here or elsewhere, withdrawn),
 * while the session may still be "Needs you" for another one: taken back as settledTags does for a state change (#3610).
 * `announced` is the card each session's notification is about, by agentKey.
 */
export function answeredTags(announced: ReadonlyMap<string, string>, pending: readonly Interaction[]): string[] {
  const ids = new Set(pending.map((ix) => ix.id));
  return [...announced].filter(([, card]) => !ids.has(card)).map(([key]) => `${key}#needs`);
}

/** A session's current turn as the page saw it: when it started working, and when it last stopped (ms). */
export interface Turn {
  start: number;
  endedAt?: number;
}

/**
 * Follows each session's turns between snapshots, for "Long tasks only" (hub/notify.go turnTracker): a turn starts when the
 * session starts working (the hub's state_since, else now), and one that resumes within SETTLE_MS of stopping is the
 * same turn (the status flaps around a turn), so its start is kept. A session not working keeps its last turn, so the
 * length of the turn that just finished can be read.
 */
export function trackTurns(prev: ReadonlyMap<string, Turn>, sessions: Session[], now: number): Map<string, Turn> {
  const next = new Map<string, Turn>();
  for (const s of sessions) {
    const key = agentKey(s);
    const was = prev.get(key);
    if (stateOf(s) === "working") {
      if (was && (was.endedAt === undefined || now - was.endedAt < SETTLE_MS)) next.set(key, { start: was.start });
      else {
        const since = s.state_since ? Date.parse(s.state_since) : NaN;
        next.set(key, { start: Number.isNaN(since) ? now : Math.min(since, now) });
      }
    } else if (was) next.set(key, was.endedAt === undefined ? { start: was.start, endedAt: now } : was);
  }
  return next;
}

/** How long a finished turn must stay finished before it sounds (the status flaps working → done → working around a turn). */
export const SETTLE_MS = 1500;

/** How long finished turns gather before their notification (#4945): it goes out once none has joined for
 *  BATCH_QUIET_MS (after its own SETTLE_MS), and at the latest BATCH_MAX_MS after the first joined. */
export const BATCH_QUIET_MS = 1500;
export const BATCH_MAX_MS = 5000;

/** When a batch whose first turn joined at `firstAt` and last at `lastAt` (ms) is posted. */
export function batchDue(firstAt: number, lastAt: number): number {
  return Math.min(lastAt + BATCH_QUIET_MS, firstAt + BATCH_MAX_MS);
}

/** How many agents a batch's notification names; the rest are "and others". */
export const BATCH_NAMES = 3;

/** How a batch of finished turns is told: nothing, the one agent's own notification, or one for all of them. */
export type FinishedBatch = { kind: "none" } | { kind: "one"; key: string } | { kind: "many"; keys: string[]; names: string[]; more: boolean };

/**
 * How a batch of finished turns is told (#4945). `keys` are the agents in the order they finished (repeats are
 * fine), `unread` the names of the sessions that are still finished unread, by agentKey: one seen or gone meanwhile is left
 * out. None left: nothing; one: its own notification, as before; more: one notification naming the first `max`.
 */
export function finishedBatch(keys: readonly string[], unread: ReadonlyMap<string, string>, max = BATCH_NAMES): FinishedBatch {
  const left = [...new Set(keys)].filter((k) => unread.has(k));
  if (!left.length) return { kind: "none" };
  if (left.length === 1) return { kind: "one", key: left[0] };
  return { kind: "many", keys: left, names: left.slice(0, max).map((k) => unread.get(k)!), more: left.length > max };
}

/** Where the user is looking: the session on the page, and whether the window is the one they are using. */
export interface Viewing {
  /** agentKey of the conversation on the page, if any. */
  key?: string;
  /** The window is focused and visible. */
  focused: boolean;
}

/** No sound for the session on screen while its window is focused: the user is looking at it. */
export function suppressed(key: string, viewing: Viewing): boolean {
  return viewing.focused && viewing.key === key;
}

/** Of a batch the more urgent one speaks: "Needs you" over finished. */
export function pickEvent(events: AttentionEvent[]): AttentionEvent | undefined {
  return events.find((e) => e.kind === "needs") ?? events[0];
}

export const MIN_GAP_MS = 3000;

/** At most one sound per `gap` ms. `now` is a clock in ms (injected for tests). */
export function rateLimiter(gap = MIN_GAP_MS) {
  let last = -Infinity;
  return (now: number): boolean => {
    if (now - last < gap) return false;
    last = now;
    return true;
  };
}

// ── "Alert sound" setting (Settings › Notifications), kept in this browser, off by default ──────────────

const KEY = "persona.attentionSound";
const listeners = new Set<() => void>();

export function attentionSoundEnabled(): boolean {
  try {
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}

export function setAttentionSoundEnabled(on: boolean): void {
  try {
    if (on) localStorage.setItem(KEY, "1");
    else localStorage.removeItem(KEY);
  } catch {
    // storage unavailable: nothing is kept, the default (off) stays
  }
  listeners.forEach((l) => l());
}

function subscribe(l: () => void) {
  listeners.add(l);
  window.addEventListener("storage", l);
  return () => {
    listeners.delete(l);
    window.removeEventListener("storage", l);
  };
}

export function useAttentionSoundSetting(): boolean {
  return useSyncExternalStore(subscribe, attentionSoundEnabled, () => false);
}
