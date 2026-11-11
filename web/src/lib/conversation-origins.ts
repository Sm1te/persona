// Where an assistant conversation comes from (docs/protocol/protocol.md "Origin of assistant chats", #3264): the user's own (the app, a
// channel's /new) can be in the sidebar's "Recent" (#4873); the ones the hub opened by itself (the inbox's AI twin, a scheduled
// task, conversation_new, a plugin's /agent…) are only in "All chats", filtered by origin. Pure functions, tested without a DOM.
import { t } from "../i18n";
import type { IconName } from "../ui/icons";
import type { BrandId } from "./connectors";
import { byTime } from "./times";
import type { Item, Session } from "./types";

/** The sidebar's groups of automatic conversations, in its order; `other` is any origin this page doesn't know. */
export const AUTO_ORIGINS = ["inbox", "task", "lark_agent", "spawned", "extension", "other"] as const;
export type AutoOrigin = (typeof AUTO_ORIGINS)[number];

/** What "All chats" can be filtered to: everything, the user's own, or one group of automatic ones. */
export type OriginFilter = "all" | "mine" | AutoOrigin;

const USER_ORIGINS = new Set(["", "user", "channel"]);

/** The automatic group a conversation is in; undefined for the user's own (an older hub says nothing: all are). */
export function autoOrigin(s: Pick<Session, "origin">): AutoOrigin | undefined {
  const o = s.origin ?? "";
  if (USER_ORIGINS.has(o)) return undefined;
  return (AUTO_ORIGINS as readonly string[]).includes(o) ? (o as AutoOrigin) : "other";
}

export const isAutoConversation = (s: Pick<Session, "origin">) => autoOrigin(s) !== undefined;

export function originLabel(o: AutoOrigin): string {
  return t(`assistant-conversations.origin_${o}`);
}

/** Each group's mark: the sidebar's "Automatic" rows, and an external conversation's (OriginMark). */
export const ORIGIN_ICON: Record<AutoOrigin, IconName> = { inbox: "inbox", task: "clock", lark_agent: "at", spawned: "fork", extension: "extension", other: "more" };
/** A group with a brand of its own draws its mark instead of the icon (#3758). */
export const ORIGIN_BRAND: Partial<Record<AutoOrigin, BrandId>> = { lark_agent: "lark" };

/** The conversations an extension opened (#3758): Lark /agent's and any other extension's. */
export const EXTERNAL_ORIGINS = ["lark_agent", "extension"] as const;
export type ExternalOrigin = (typeof EXTERNAL_ORIGINS)[number];

/** Where an external conversation came from, by its origin with the spaces trimmed; undefined for every other one (the
 *  user's own, a channel's /new, the inbox's, a task's, one spawned from a chat, an origin this page doesn't know). */
export function externalOrigin(s: Pick<Session, "origin"> | undefined): ExternalOrigin | undefined {
  const o = s?.origin?.trim() ?? "";
  return (EXTERNAL_ORIGINS as readonly string[]).includes(o) ? (o as ExternalOrigin) : undefined;
}

/** "From Lark /agent", "From an extension": under the message it was opened with, and the name of its mark on a row. */
export function externalFrom(o: ExternalOrigin): string {
  return o === "extension" ? t("assistant-conversations.from_extension") : t("chat-segmentview.from", { name: originLabel(o) });
}

/** The message an external conversation was opened with: its first user message (lib/turns.ts's rule: a text, or a
 *  picture that is not a tool's), with where it came from. Only once the loaded items reach the conversation's start: while
 *  earlier ones are still to load (`hasMore`), the first one loaded is not the first. */
export function externalOpening(s: Pick<Session, "origin"> | undefined, items: readonly Pick<Item, "id" | "role" | "kind" | "tool_use_id">[], hasMore: boolean): { id: string; origin: ExternalOrigin } | undefined {
  const origin = externalOrigin(s);
  if (!origin || hasMore) return undefined;
  const first = items.find((i) => i.role === "user" && (i.kind === "text" || (i.kind === "image" && !i.tool_use_id)));
  return first ? { id: first.id, origin } : undefined;
}

/** The filter of a `/history/<filter>` address; anything else is all of them. */
export function parseOriginFilter(v: string | undefined): OriginFilter {
  return v === "mine" || (AUTO_ORIGINS as readonly string[]).includes(v ?? "") ? (v as OriginFilter) : "all";
}

export function matchesOrigin(s: Pick<Session, "origin">, filter: OriginFilter): boolean {
  if (filter === "all") return true;
  const o = autoOrigin(s);
  return filter === "mine" ? o === undefined : o === filter;
}

const working = (s: Session) => s.state === "working" || s.status === "working" || !!s.starting;
const needsYou = (s: Session) => s.state === "needs_you";

/** How a set of conversations stands, for a folded row: how many, how many run, how many wait for the user. */
export interface OriginTally {
  count: number;
  working: number;
  needsYou: number;
}

function tally(list: readonly Session[]): OriginTally {
  return { count: list.length, working: list.filter(working).length, needsYou: list.filter(needsYou).length };
}

/** The automatic conversations (not archived, not the main one) by group, only the groups that have any, in the
 *  sidebar's order, and all of them together. */
export function autoConversations(conversations: readonly Session[], main: string | undefined): { total: OriginTally; groups: (OriginTally & { origin: AutoOrigin })[] } {
  const auto = conversations.filter((c) => c.session_id !== main && !c.archived && isAutoConversation(c));
  const groups = AUTO_ORIGINS.map((origin) => ({ origin, ...tally(auto.filter((c) => autoOrigin(c) === origin)) })).filter((g) => g.count > 0);
  return { total: tally(auto), groups };
}

/** How many chats the sidebar's "Recent" lists (#4873); the rest are in "All chats". */
export const RECENT_LIMIT = 5;

/** The sidebar's "Recent" under Persona chats (#4873): the newest `limit` of the user's own conversations that are not the
 *  main one and not a leader (常驻, listed above it), by last activity (equal times by id, as iOS). The ones the hub opened
 *  by itself stay in "All chats", as "Recent" left them out before #4331. */
export function recentConversations(conversations: readonly Session[], main: string | undefined, leaders: readonly string[], limit = RECENT_LIMIT): Session[] {
  const lead = new Set(leaders);
  return conversations
    .filter((c) => c.session_id !== main && !lead.has(c.session_id) && !c.archived && !isAutoConversation(c))
    .sort((a, b) => byTime(b.updated_at, a.updated_at) || (a.session_id < b.session_id ? -1 : a.session_id > b.session_id ? 1 : 0))
    .slice(0, limit);
}
