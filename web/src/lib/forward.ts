// Forwarding a message (#3899, the web's side of #3807; iOS Forward.swift; docs/protocol/protocol.md "Forwarded messages"):
// a message's actions › Forward › a target, and that chat opens with the message over its box as a card. What goes out
// is the user's own message there: a `<persona-context kind="forward">` block (the original and where it came from)
// before the note typed under it, sent with the chat's usual send (lib/links.ts withContexts). Nothing is written back
// to the original chat. The picker is views/chat/ForwardDialog.tsx, its lists lib/forward-targets.ts; this file is what
// the chat itself needs: the block's card words, the picked message as a forward, and the event that opens the picker;
// writing the block is lib/forward-block.ts, which only the picker loads. No DOM here.
import { t } from "../i18n";
import { isAppLink } from "./compatibility";
import { clock, tabLabel } from "./format";
import { agentLabel } from "./mentions";
import type { Item, Session } from "./types";

export const FORWARD_KIND = "forward";

/** Whose message it was: the user's own, Persona's, an Agent's, or another source's (a relay, another session's Claude). */
export type ForwardRole = "user" | "persona" | "agent" | "other";

/** Where the forwarded message is and who wrote it, as the chat it was picked in knows them. */
export interface ForwardSource {
  mid: string;
  sid: string;
  item: string;
  role: ForwardRole;
  /** Who wrote it, by name ("Persona", the Agent's "Claude"); the user's own is "Me" in the sender's language. */
  author: string;
  /** The chat's title. */
  chat: string;
  /** The Agent's project ("weather › tests"); none for Persona's chats. */
  project?: string;
  /** The computer's name (not its id); none for Persona's chats. */
  machine?: string;
  /** When the original was written (the item's ISO time). */
  time?: string;
}

/** A forward's attributes, read or written: what the block carries besides kind, title, url and text. */
type Attrs = Record<string, string | undefined> | undefined;

/** The session and item a forward's `url` names (`msg` when the link has no item); null when it names none. */
export function forwardOrigin(url: string, msg?: string): { machine: string; session: string; item?: string } | null {
  if (!isAppLink(url)) return null;
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return null;
  }
  if (u.host !== "session") return null;
  let parts: string[];
  try {
    parts = u.pathname.split("/").filter(Boolean).map(decodeURIComponent);
  } catch {
    return null;
  }
  if (parts.length !== 2) return null;
  const item = u.searchParams.get("at") || msg || undefined;
  return { machine: parts[0], session: parts[1], ...(item ? { item } : {}) };
}

/** The author as this device names it: the user's own message is "Me" in this browser's language. */
export function shownAuthor(a: Attrs): string {
  if (a?.role === "user") return t("chat-forward.me");
  return a?.author || t("chat-forward.messages");
}

/** The card's first line: "Forwarded from Claude · Network tests". */
export function forwardHeading(a: Attrs): string {
  const who = shownAuthor(a);
  return a?.chat ? t("chat-forward.heading_chat", { who, chat: a.chat }) : t("chat-forward.heading", { who });
}

/** The card's second line: the computer and when ("MacBook Pro · 10:24"); "" when it knows neither. */
export function forwardMeta(a: Attrs): string {
  return [a?.machine, clock(a?.time)].filter(Boolean).join(" · ");
}

/** A forward's words in its card: its own lines, blank ones dropped. */
export function forwardSummary(text: string): string {
  const lines = text.split("\n").map((l) => l.trim()).filter(Boolean);
  return lines.length ? lines.join("\n").slice(0, 1200) : t("chat-forward.no_content");
}

// ── opening the picker ───────────────────────────────────

/** A picked message on its way to the picker (ForwardDialog): where it is and who wrote it, its text as the chat holds
 *  it, whether the chat holds it cut (the whole is read when it goes), and the pictures of your own message. */
export interface ForwardDraft {
  source: ForwardSource;
  raw: string;
  /** The item the hub cut for the page (Item.full_size): `GET …/items/{id}/full` reads the whole when it goes. */
  cut?: string;
  /** Pictures sent with it (your own message's): image items of the transcript, and photos saved on its computer. */
  images?: Item[];
  photos?: string[];
  /** The chat's working folder, for photo paths relative to it. */
  cwd?: string;
}

/** The message whose Forward was picked, as its actions know it (views/chat/MessageActions.tsx). */
export interface ForwardPick {
  itemId: string;
  /** Its words as the bubble shows them: what goes when the transcript no longer has the item. */
  text: string;
  isUser: boolean;
  ts?: string;
  /** Who wrote it when not the chat's Agent or Persona (a relayed message's source, another session's Claude). */
  from?: string;
  images?: Item[];
  photos?: string[];
}

/** The chat a message is forwarded from, as ChatView knows it when Forward is picked. */
export interface ForwardChat {
  mid: string;
  sid: string;
  /** A Persona conversation, not an Agent's. */
  assistant: boolean;
  title: string;
  items: readonly Item[];
  session?: Session;
  /** The computer's name. */
  machine?: string;
}

/** The picked message as a forward (iOS ChatView.forwardDraft): who wrote it and where, its text as this chat holds it
 *  (the transcript's, quotes and all; the whole of it read when it goes, if cut) and your own message's pictures. */
export function forwardDraftOf(c: ForwardChat, p: ForwardPick): ForwardDraft {
  const s = c.session;
  const role: ForwardRole = p.isUser ? "user" : p.from ? "other" : c.assistant ? "persona" : "agent";
  const author = p.from ?? (role === "persona" ? "Persona" : s ? agentLabel(s) : "Claude");
  const project = c.assistant || !s ? "" : [s.space_name, tabLabel(s)].filter(Boolean).join(" › ");
  // a message of yours is its transcript line's text blocks (with the images that came with them); a reply its item
  const entry = p.itemId.split("#")[0];
  const texts = p.isUser
    ? c.items.filter((i) => i.role === "user" && i.kind === "text" && (i.id === p.itemId || i.id.split("#")[0] === entry))
    : c.items.filter((i) => i.id === p.itemId);
  const raw = texts.map((i) => i.text ?? "").join("\n") || p.text;
  const cut = texts.length === 1 && texts[0].full_size ? texts[0].id : undefined;
  const source: ForwardSource = {
    mid: c.mid,
    sid: c.sid,
    item: p.itemId,
    role,
    author,
    chat: c.title,
    ...(project ? { project } : {}),
    ...(!c.assistant && c.machine ? { machine: c.machine } : {}),
    ...(p.ts ?? texts[0]?.ts ? { time: p.ts ?? texts[0]?.ts } : {}),
  };
  return {
    source,
    raw,
    ...(cut ? { cut } : {}),
    ...(p.isUser && p.images?.length ? { images: p.images } : {}),
    ...(p.isUser && p.photos?.length ? { photos: p.photos } : {}),
    ...(s?.cwd ? { cwd: s.cwd } : {}),
  };
}

const openers = new Set<(d: ForwardDraft) => void>();

/** Opens the picker for `draft` (the app shell's ForwardHost listens). */
export function openForward(draft: ForwardDraft) {
  openers.forEach((l) => l(draft));
}

export function onOpenForward(l: (d: ForwardDraft) => void): () => void {
  openers.add(l);
  return () => void openers.delete(l);
}

// ── going to the original ────────────────────────────────

/** The chat a forward's card opened, and the message to bring into view once its transcript has it. */
let jump: { mid: string; sid: string; item: string } | null = null;

export function jumpWhenOpen(mid: string, sid: string, item: string) {
  jump = { mid, sid, item };
}

/** The item to go to in the chat `mid/sid` just opened, taken once; none when no card opened it. */
export function takeJump(mid: string, sid: string): string | undefined {
  if (!jump || jump.mid !== mid || jump.sid !== sid) return undefined;
  const item = jump.item;
  jump = null;
  return item;
}

export function peekJump(mid: string, sid: string): string | undefined {
  return jump && jump.mid === mid && jump.sid === sid ? jump.item : undefined;
}
