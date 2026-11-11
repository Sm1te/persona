// Forward to's lists (#3899, iOS ForwardTargets): which chats a forward may go to, the recent ones first, the rest by
// computer, and what a search finds. Built from the sessions this page already holds; nothing is fetched. No DOM here.
import { sessionCan, stateOf } from "./format";
import { agentLabel, displayTitle } from "./mentions";
import type { Session } from "./types";

/** Recent targets shown at most (ForwardMemory keeps more). */
export const RECENT_LIMIT = 5;

export interface TargetSection {
  id: string;
  kind: "recent" | "machine" | "results";
  /** The computer's name for a machine section. */
  title: string;
  sessions: Session[];
}

/** `<machine>/<session>`. */
export const keyOf = (s: Pick<Session, "machine_id" | "session_id">) => `${s.machine_id}/${s.session_id}`;

/** Whether `s` can take a forward from the chat `from`: another chat that takes messages; an Agent on an online computer,
 *  live or (`ended`) one the send resumes; never Health's conversations or an archived chat. This page holds one hub
 *  (linked hubs' computers are its own), so every chat it has is on the same hub. */
export function takesForward(s: Session, from: string, ended: boolean, online: (mid: string) => boolean): boolean {
  if (keyOf(s) === from || !sessionCan(s, "send") || s.space === "health" || s.archived) return false;
  if (s.assistant) return true;
  return online(s.machine_id) && (ended || stateOf(s) !== "offline");
}

function folderName(cwd?: string): string {
  const parts = (cwd ?? "").replace(/\/+$/, "").split("/");
  return parts[parts.length - 1] || "";
}

/** A row's second line: who answers there and its project, and its computer where rows mix computers. */
export function targetDetail(s: Session, machine?: string): string {
  if (s.assistant) return "Persona";
  const project = s.space_name?.trim() || folderName(s.cwd);
  return [agentLabel(s), project, machine].filter(Boolean).join(" · ");
}

/** A row's title. */
export function targetTitle(s: Session, conversationTitle: (s: Session) => string): string {
  return s.assistant ? conversationTitle(s) : displayTitle(s);
}

const latest = (a: Session, b: Session) => (b.updated_at ?? "").localeCompare(a.updated_at ?? "");

/** The sections for `query`: with none, the recent targets, then each computer's live Agents, latest first; with one,
 *  every match, ended Agents and Persona's other conversations included. `main`: the main conversation's key, which has
 *  its own row above the lists. */
export function forwardSections(o: {
  all: Session[];
  from: string;
  recents: string[];
  main?: string;
  machines: { id: string; name: string }[];
  query: string;
  online: (mid: string) => boolean;
  conversationTitle: (s: Session) => string;
}): TargetSection[] {
  const name = (mid: string) => o.machines.find((m) => m.id === mid)?.name ?? mid;
  const terms = o.query.toLowerCase().split(/\s+/).filter(Boolean);
  // one row per chat: the page may hold a chat both as a list row and as a chat's details
  const seen = new Set<string>();
  const all = o.all.filter((s) => !seen.has(keyOf(s)) && Boolean(seen.add(keyOf(s))));
  if (terms.length) {
    const found = all.filter((s) => {
      if (keyOf(s) === o.main || !takesForward(s, o.from, true, o.online)) return false;
      const hay = [targetTitle(s, o.conversationTitle), targetDetail(s, s.assistant ? undefined : name(s.machine_id))].join(" ").toLowerCase();
      return terms.every((w) => hay.includes(w));
    });
    return found.length ? [{ id: "results", kind: "results", title: "", sessions: found.sort(latest) }] : [];
  }
  const out: TargetSection[] = [];
  const byKey = new Map(all.map((s) => [keyOf(s), s]));
  const recent = o.recents
    .flatMap((k) => byKey.get(k) ?? [])
    .filter((s) => keyOf(s) !== o.main && takesForward(s, o.from, false, o.online))
    .slice(0, RECENT_LIMIT);
  if (recent.length) out.push({ id: "recent", kind: "recent", title: "", sessions: recent });
  const shown = new Set(recent.map(keyOf));
  const agents = all.filter((s) => !s.assistant && !shown.has(keyOf(s)) && takesForward(s, o.from, false, o.online));
  for (const m of o.machines) {
    const rows = agents.filter((s) => s.machine_id === m.id).sort(latest);
    if (rows.length) out.push({ id: `machine:${m.id}`, kind: "machine", title: m.name, sessions: rows });
  }
  return out;
}
