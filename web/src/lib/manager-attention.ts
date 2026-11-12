// What waits for the user at each assistant (docs/assistant/managers.md "Clients"): the switcher's count beside the other
// assistants and the dot on the sidebar's avatar. A count is the agents waiting (a pending card to approve or answer,
// or a session in Needs you; one agent with both counts once) plus the assistant's inbox items that need the user
// (`inbox_needs_you` of the roster; a hub without it: the agents only). The agents are counted here from what the hub
// already pushes: sessions and cards carry their `manager`.
import { useMemo } from "react";
import type { AgentMutes } from "./agent-groups";
import { stateOf } from "./format";
import { requestHome } from "./child-sessions";
import { useShownManager } from "./manager-choice";
import { installedManagers, multiManager, ownerIn, useRoster, type ManagerList } from "./managers";
import { isMuted } from "./session-actions";
import { useFullAgentSessions } from "./sessions";
import { useStore } from "./store";
import type { Interaction, Session } from "./types";

const NO_MUTES: AgentMutes = { spaces: [], sessions: [] };

/** The agents waiting for the user, per manager id (the same rule on every client): a session of the manager that is
 *  not a quiet task's and not muted, with a pending card or in Needs you, counts once however many cards it has; a
 *  pending card on no session (a script's request) counts by itself. A card is muted as in the Inbox's list of agents
 *  waiting (lib/inbox.ts interactionMuted): its session, or its session's space, is. The assistant's own conversations
 *  count by their cards only, as the sidebar's Needs you leaves them out. */
export function waitingAgents(list: ManagerList, sessions: readonly Session[], interactions: readonly Interaction[], mutes: AgentMutes = NO_MUTES): Record<string, number> {
  const waiting = new Map<string, Set<string>>();
  const add = (manager: string | undefined, key: string) => {
    const id = ownerIn(list, manager);
    if (!waiting.has(id)) waiting.set(id, new Set());
    waiting.get(id)!.add(key);
  };
  const keyOf = (mid: string, sid: string) => `${mid}/${sid}`;
  const byKey = new Map(sessions.map((s) => [keyOf(s.machine_id, s.session_id), s]));
  const counted = (s: Session) => !s.quiet && !isMuted(s, mutes);
  for (const s of sessions) if (!s.assistant && counted(s) && stateOf(s) === "needs_you") add(s.manager, keyOf(s.machine_id, s.session_id));
  for (const ix of interactions) {
    if (!ix.session_id) {
      add(ix.manager, `card:${ix.id}`);
      continue;
    }
    // a child session's card counts for its parent (#5120): one agent waiting, however many of its children ask
    const home = requestHome(ix);
    const key = keyOf(ix.machine_id, home);
    const s = byKey.get(key);
    // a session not in the list (not read yet): muted by its own switch only, as interactionMuted does
    if (s ? !counted(s) : isMuted({ machine_id: ix.machine_id, session_id: home }, mutes)) continue;
    add(ix.manager, key);
  }
  return Object.fromEntries([...waiting].map(([id, keys]) => [id, keys.size]));
}

/** Each installed assistant's count: its agents waiting plus its inbox items that need the user (none where the
 *  roster has no `inbox_needs_you`). */
export function managerBadges(list: ManagerList, sessions: readonly Session[], interactions: readonly Interaction[], mutes: AgentMutes = NO_MUTES): Record<string, number> {
  const agents = waitingAgents(list, sessions, interactions, mutes);
  return Object.fromEntries(installedManagers(list).map((m) => [m.id, (agents[m.id] ?? 0) + Math.max(0, m.inbox_needs_you ?? 0)]));
}

const NONE: Record<string, number> = {};

/** managerBadges for the roster as it is now; empty with one assistant (nothing shows a count then, and the complete
 *  list of agents is not asked for). */
export function useManagerBadges(): Record<string, number> {
  const roster = useRoster();
  const several = multiManager(roster);
  const { sessions } = useFullAgentSessions(several);
  const interactions = useStore((s) => s.interactions);
  const mutes = useStore((s) => s.mutes);
  return useMemo(() => (several ? managerBadges(roster, sessions, interactions, mutes) : NONE), [several, roster, sessions, interactions, mutes]);
}

/** Whether an assistant other than the current one has something waiting: the dot on the sidebar's avatar. */
export function useOthersWaiting(): boolean {
  const badges = useManagerBadges();
  const current = useShownManager();
  return Object.entries(badges).some(([id, n]) => id !== current && n > 0);
}
