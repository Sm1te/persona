// A card answered here goes at once (store endInteraction), while the agent's own state says needs_you until the
// node's next sessions push: rows built from the session (the dock's pill, a task card's status, a to-dos row, a manual
// agent's pill and its "All" row) kept saying "Needs you" for that while (#4186). Until the hub catches up, an agent that needs
// you, has no card waiting and has a card just answered here counts as at work; a new card for it makes it wait again
// at once, and so does a needs_you the hub sent after the answer (a second card still on its way, a TUI dialog).
// A deny shows "Working" too until the next push: the agent goes on either way. A card handed back to the computer is not
// an answer (the agent waits there), and a needs_you pushed before the hub withdrew the card is the old one (#4877).
import { foldedState } from "../../lib/child-sessions";
import { homeKey } from "../../lib/folded-rows";
import { makeSessionLookup, type SessionLookup } from "../../lib/todos";
import type { AnsweredCard } from "../../lib/store";
import type { Interaction, Session } from "../../lib/types";

const key = (mid: string, sid: string) => `${mid}/${sid}`;

// One answered copy per session object: store selectors compare what they return, so the same session gives back the
// same copy until the hub sends a new one.
const copies = new WeakMap<Session, Session>();

function answeredCopy(s: Session): Session {
  let c = copies.get(s);
  if (!c) {
    // a parent waiting only through a child's card answered here works with it: its rollup's waiting child is that one
    const children = s.children?.needs_you ? { ...s.children, needs_you: undefined } : s.children;
    c = { ...s, state: "working", ...(children !== s.children ? { children } : {}) };
    copies.set(s, c);
  }
  return c;
}

/** An agent's answers here: the latest one's time (ms, the card's `at` from endInteraction; 0 without one, so any
 *  needs_you with a time counts as newer), and whether the hub has yet to withdraw one of the cards. */
export interface AnsweredMark {
  at: number;
  pending: boolean;
}

/** Per agent, its answers here; cards handed back to the computer are not answers. */
function answeredBy(answered: Record<string, AnsweredCard>): Map<string, AnsweredMark> {
  const out = new Map<string, AnsweredMark>();
  for (const a of Object.values(answered)) {
    if (a.handedBack) continue;
    // a child session's card is its parent's to answer and to count (#5120): answered, the parent's row stops waiting
    const k = homeKey(a.ix);
    const cur = out.get(k);
    out.set(k, { at: Math.max(cur?.at ?? 0, a.at ?? 0), pending: Boolean(cur?.pending) || !a.withdrawn });
  }
  return out;
}

/** Whether an agent's needs_you is only the hub not having caught up with a card answered here. */
export function answeredHere(s: Session, waiting: ReadonlySet<string>, answered: ReadonlyMap<string, AnsweredMark>): boolean {
  const k = key(s.machine_id, s.session_id);
  const mark = answered.get(k);
  if (mark === undefined || foldedState(s) !== "needs_you" || waiting.has(k)) return false;
  // Until the hub withdraws the card, a push saying needs_you comes from before the answer took, whatever its time:
  // the withdrawal comes on the same socket, after it. A new card makes it wait at once either way (`waiting`).
  if (mark.pending) return true;
  // a needs_you that started after the answer is a new one, not the old one lingering. state_since is the hub's
  // clock and `at` this browser's: a skew of a few seconds only moves where the 20 s window cuts over.
  const since = s.state_since ? Date.parse(s.state_since) : NaN;
  return Number.isNaN(since) || since <= mark.at;
}

/** The sessions lookup with that applied. */
export function answeredLookup(sessions: Session[] | Record<string, Session[]> | SessionLookup, interactions: Interaction[], answered: Record<string, AnsweredCard>): SessionLookup {
  const base = typeof sessions === "function" ? sessions : makeSessionLookup(sessions);
  const done = answeredBy(answered);
  if (!done.size) return base;
  const waiting = new Set(interactions.map(homeKey));
  return (mid, sid) => {
    const s = base(mid, sid);
    return s && answeredHere(s, waiting, done) ? answeredCopy(s) : s;
  };
}

/** The sessions with that applied, for lists that take them whole (the dock's and "All"'s manual agents). Same array
 *  when nothing changes. */
export function answeredSessions(sessions: Session[], interactions: Interaction[], answered: Record<string, AnsweredCard>): Session[] {
  const done = answeredBy(answered);
  if (!done.size) return sessions;
  const waiting = new Set(interactions.map(homeKey));
  let changed = false;
  const out = sessions.map((s) => {
    if (!answeredHere(s, waiting, done)) return s;
    changed = true;
    return answeredCopy(s);
  });
  return changed ? out : sessions;
}

/** One session as a row shows it (the sidebar, #4286), by the same rule; `answeredAt` is when this page last answered
 *  one of its cards (ms), undefined for none; `pending`: the hub has yet to withdraw one of them (answerPending). */
export function answeredSession(s: Session, waiting: boolean, answeredAt: number | undefined, pending = false): Session {
  if (answeredAt === undefined) return s;
  const k = key(s.machine_id, s.session_id);
  return answeredHere(s, waiting ? new Set([k]) : new Set(), new Map([[k, { at: answeredAt, pending }]])) ? answeredCopy(s) : s;
}

/** When this page last answered one of a session's cards (ms), for answeredSession. */
export function lastAnsweredAt(answered: Record<string, AnsweredCard>, mid: string, sid: string): number | undefined {
  return answeredBy(answered).get(key(mid, sid))?.at;
}

/** Whether the hub has yet to withdraw a card this page answered for a session, for answeredSession. */
export function answerPending(answered: Record<string, AnsweredCard>, mid: string, sid: string): boolean {
  return answeredBy(answered).get(key(mid, sid))?.pending ?? false;
}
