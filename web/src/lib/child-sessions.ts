// Sessions an Agent started (a Codex subagent, an Agent a Paseo Agent created) folded into the session that started them
// (#5120, after cindy: a child Agent is never a task of its own). The hub's manager says which listed session a child
// folds under (`folded_under`) and gives that session a rollup of their states (`children`, docs/protocol/protocol.md
// "Session hierarchy"). The web leaves the children out of every list and count, shows the parent in its folded state,
// and draws each child as a card in the parent's chat, where its requests are answered too.
import { stateOf } from "./format";
import type { AgentState, Interaction, Session } from "./types";

/** The hub folds child sessions and its paged list takes `roots=1` and `parent_session_id=` (docs/protocol/protocol.md
 *  "Session hierarchy"): send those keys only when hello lists this. */
export const SESSIONS_HIERARCHY_FEATURE = "sessions_hierarchy";

/** The state a parent shows and is counted under with its folded children (the hub's `Session.FoldedState`): one of
 *  them waiting on the user makes it need you; one working makes it work, unless it has a reply the user has not read or
 *  awaits the user's own. */
export function foldedState(s: Session): AgentState {
  const own = stateOf(s);
  const c = s.children;
  if (own === "needs_you" || (c?.needs_you ?? 0) > 0) return "needs_you";
  if ((c?.working ?? 0) > 0 && own !== "unread" && !(own === "idle" && s.awaits_reply)) return "working";
  return own;
}

/** A session's key in the sets and maps of child rows (`listedKeys`, the rows open chats hold). */
export const rowKey = (mid: string, sid: string) => `${mid}\u0000${sid}`;

/** The keys of the sessions a list holds, for `isFoldedIn`. */
export function listedKeys(rows: readonly Session[]): Set<string> {
  return new Set(rows.map((s) => rowKey(s.machine_id, s.session_id)));
}

/** s folds under a session the list holds (on its own machine): it is never a row or a count of its own there. A child
 *  whose parent the list does not hold (another filter, another page) stays a row. */
export function isFoldedIn(s: Session, listed: ReadonlySet<string>): boolean {
  return Boolean(s.folded_under) && listed.has(rowKey(s.machine_id, s.folded_under!));
}

/** The sessions folded under mid/sid, oldest first (by when they started, else were last updated). */
export function foldedChildren(rows: readonly Session[], mid: string, sid: string): Session[] {
  const at = (s: Session) => Date.parse(s.started_at ?? s.updated_at ?? "") || 0;
  return rows.filter((s) => s.machine_id === mid && s.folded_under === sid).sort((a, b) => at(a) - at(b));
}

/** Where a request is answered: the page of the session its session folds under, else its own. */
export function requestHome(ix: Pick<Interaction, "session_id" | "folded_under">): string {
  return ix.folded_under || ix.session_id;
}

/** How many of a session's folded children are working. */
export function runningChildren(s: Session): number {
  return s.children?.working ?? 0;
}
