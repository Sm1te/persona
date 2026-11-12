// A subagent is a card in the chat that launched it, never a conversation of its own (#5030, cindy). A link to its
// conversation from before ("<parent>@<agent>": a page `/s/<machine>/<parent>@<agent>`, an "agent" side panel, a link in
// a message) opens the parent's chat instead, and that chat brings the subagent's card into view, opened (ChatView).
// A child session's (#5120: a Codex subagent, an Agent a Paseo Agent created; its row says which session it folds under)
// the same way: its parent's chat, at the child's card.
import { useSyncExternalStore } from "react";
import { childCall } from "./child-cards";

/** The parent session and the subagent of a "<parent>@<agent>" id; null for any other id. */
export function splitSubagentLink(sid: string): { parent: string; agent: string } | null {
  const at = sid.indexOf("@");
  if (at <= 0 || at === sid.length - 1) return null;
  return { parent: sid.slice(0, at), agent: sid.slice(at + 1) };
}

/** The subagent each parent chat is asked to show, by "<machine>/<parent>". */
const pending = new Map<string, string>();
const listeners = new Set<() => void>();
const key = (mid: string, sid: string) => `${mid}/${sid}`;
const tell = () => listeners.forEach((l) => l());

/** The id to open for `sid`: its parent's for a subagent's, which is then asked to show the subagent's card; for a child
 *  session's (`parent`, the session its row folds under), that parent's (`followChildSession`); `sid` itself otherwise. */
export function followSubagentLink(mid: string, sid: string, parent?: string): string {
  if (parent) return followChildSession(mid, sid, parent);
  const link = splitSubagentLink(sid);
  if (!link) return sid;
  pending.set(key(mid, link.parent), link.agent);
  tell();
  return link.parent;
}

/** The id to open for a child session's link: its parent's, which is then asked to show the child's card (the call the
 *  card is known by, beside the subagents' ids; lib/child-cards.ts childOfCall reads it back). */
export function followChildSession(mid: string, child: string, parent: string): string {
  pending.set(key(mid, parent), childCall(child));
  tell();
  return parent;
}

/** The subagent (or, `childOfCall`, the child session) this chat is asked to show, until it did (`doneSubagentLink`). */
export function useSubagentLink(mid: string, sid: string): string | undefined {
  const read = () => pending.get(key(mid, sid));
  return useSyncExternalStore(subscribe, read, read);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => void listeners.delete(l);
}

export function doneSubagentLink(mid: string, sid: string) {
  if (pending.delete(key(mid, sid))) tell();
}
