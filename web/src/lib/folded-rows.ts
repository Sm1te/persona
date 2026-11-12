// The lists' side of child sessions folded into their parent (#5120, lib/child-sessions.ts): every list and count the
// web draws takes its rows through foldRows, so a child is never a row, a dock entry or a count of its own, and each
// parent is drawn and counted once in its folded state. A child's card belongs to the parent too (homeKey).
import { foldedState, isFoldedIn, listedKeys, requestHome } from "./child-sessions";
import { stateOf } from "./format";
import type { Interaction, Session } from "./types";

// One folded copy per row object: selectors and memoized rows compare by identity, so the same row gives back the same
// copy until the hub sends a new one.
const copies = new WeakMap<Session, Session>();

/** A row as the lists draw and count it: in its folded state, `s` itself when that is its own state. */
export function foldedRow(s: Session): Session {
  if (!s.children) return s;
  const state = foldedState(s);
  if (state === stateOf(s)) return s;
  let copy = copies.get(s);
  if (copy?.state !== state) {
    copy = { ...s, state };
    copies.set(s, copy);
  }
  return copy;
}

// One folded list per list object: a store selector folding a machine's rows (`sessions[mid]`, replaced only when that
// machine's rows change) gets the same array back on every other push.
const folds = new WeakMap<Session[], Session[]>();

/** A list as the web shows and counts it: without the children folded under a row the list holds, each parent in its
 *  folded state (foldedRow); `rows` itself when nothing changes. */
export function foldRows(rows: Session[]): Session[] {
  const known = folds.get(rows);
  if (known) return known;
  if (!rows.some((s) => s.folded_under || s.children)) return rows;
  const listed = listedKeys(rows);
  let changed = false;
  const out: Session[] = [];
  for (const s of rows) {
    if (isFoldedIn(s, listed)) {
      changed = true;
      continue;
    }
    const shown = foldedRow(s);
    if (shown !== s) changed = true;
    out.push(shown);
  }
  if (!changed) return rows;
  folds.set(rows, out);
  return out;
}

/** The agentKey (`<machine>/<session>`) of the session a card belongs to in lists and counts: the parent a child's
 *  card folds under, else its own. */
export function homeKey(ix: Pick<Interaction, "machine_id" | "session_id" | "folded_under">): string {
  return `${ix.machine_id}/${requestHome(ix)}`;
}

/** The cards waiting for the user from the sessions folded under mid/sid (its children's questions and approvals). */
export function childCards(interactions: readonly Interaction[], mid: string, sid: string): Interaction[] {
  return interactions.filter((ix) => ix.machine_id === mid && ix.folded_under === sid && ix.session_id !== sid);
}

/** The card a session's row or task speaks for: its own, else one its child sessions wait on (their home is its page). */
export function sessionCard(interactions: readonly Interaction[], mid: string, sid: string): Interaction | undefined {
  return interactions.find((ix) => ix.machine_id === mid && ix.session_id === sid) ?? childCards(interactions, mid, sid)[0];
}
