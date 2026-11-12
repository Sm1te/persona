import { requestRoute } from "../lib/card-focus";
import { requestHome } from "../lib/child-sessions";
import { stateOf } from "../lib/format";
import { withWorkView, type Route } from "../lib/router";
import type { AnsweredCard } from "../lib/store";
import type { Interaction, Session } from "../lib/types";
import { answeredSessions } from "./assistant/answered-lookup";

export interface SidebarPendingRow {
  key: string;
  machineId: string;
  session?: Session;
  cards: Interaction[];
  to: Route;
}

const sessionKey = (mid: string, sid: string) => `session:${JSON.stringify([mid, sid])}`;

/** The current assistant's pending cards and sessions that still need attention without a card. */
export function sidebarPending(sessions: Session[], interactions: Interaction[], answered: Record<string, AnsweredCard>): SidebarPendingRow[] {
  const bySession = new Map(sessions.map((s) => [sessionKey(s.machine_id, s.session_id), s]));
  const rows = new Map<string, SidebarPendingRow>();
  const cards = [...new Map(interactions.map((ix) => [ix.id, ix])).values()];
  for (const ix of cards.sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? "") || a.id.localeCompare(b.id))) {
    // a child session's card is its parent's (#5120): one row per parent, opening the parent
    const key = ix.session_id ? sessionKey(ix.machine_id, requestHome(ix)) : `request:${ix.id}`;
    const existing = rows.get(key);
    if (existing) {
      existing.cards.push(ix);
      continue;
    }
    const to = requestRoute(ix);
    // A remembered terminal view cannot answer a card; the conversation can.
    rows.set(key, { key, machineId: ix.machine_id, session: bySession.get(key), cards: [ix], to: to.name === "session" ? withWorkView({ ...to, view: "chat" }, true) : to });
  }
  const waiting = answeredSessions(sessions, cards, answered)
    .filter((s) => stateOf(s) === "needs_you")
    .sort((a, b) => (b.updated_at ?? "").localeCompare(a.updated_at ?? "") || sessionKey(a.machine_id, a.session_id).localeCompare(sessionKey(b.machine_id, b.session_id)));
  for (const s of waiting) {
    const home = requestHome(s);
    const key = sessionKey(s.machine_id, home);
    if (!rows.has(key)) rows.set(key, { key, machineId: s.machine_id, session: bySession.get(key) ?? s, cards: [], to: { name: "session", mid: s.machine_id, sid: home, view: "chat", work: true } });
  }
  return [...rows.values()];
}
