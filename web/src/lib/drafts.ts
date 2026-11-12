import { storedValue, storeValue, removeStoredValue } from "./compatibility";
// Changes to a composer's draft from outside it: the message menu's "Quote" / "Edit", a queued message pulled back, an
// edited image put in for the next message (iOS: ChatView.bindMenu, AppModel.editQueued, ImageViewer's attach).
// The composer for that draft key listens; with none mounted, text goes to the stored draft (it is read on mount).
// "Quote" waits as a bar above the input (not as `> ` lines in it) and goes out as the same `> ` block in front of the
// message, so what the agent reads is unchanged; a bubble draws a leading `> ` block folded (splitQuote). A comment on
// picked words or on lines of code (lib/comments.ts, #2437) is one more entry of that list: the message quote is
// at most one (a new one replaces it), comments pile up, each its own block after it.
import { useSyncExternalStore } from "react";
import { clampComment, commentBlock, isCommentHead, parseCommentBlock, type QuoteComment } from "./comments";
import { uuid } from "./format";
import type { EditedImage } from "./image-edit";

export interface DraftImage {
  name: string;
  mime: string;
  data: string; // base64
}

/** What "Quote" holds above the input: the quoted words, who said them, when; for a task's card, which task. */
export interface DraftQuote {
  text: string;
  from?: string;
  ts?: string;
  task?: QuotedTask;
  /** Set on a comment (the note, where it sits); the quote then carries the picked words or the code in `text`. */
  comment?: QuoteComment;
  /** Which entry of the list (set when it joins it). */
  id?: string;
}

/** The task a quoted card is about: its title and todo id, sent on the quote's first line so the assistant knows. */
export interface QuotedTask {
  title: string;
  id?: string;
}

/** The first line of a task's quote: "task", the title in corner brackets, then "(todo id: <id>)" in full-width parentheses (TASK_LINE). */
export function taskQuoteLine(t: QuotedTask): string {
  // i18n-ignore: written into the message and read back by TASK_LINE (and by the apps); not interface text
  return `任务「${t.title}」${t.id ? `（todo id: ${t.id}）` : ""}`;
}

// i18n-ignore: the shape taskQuoteLine writes
const TASK_LINE = /^任务「(.*)」(?:（todo id: ([^）\s]+)）)?$/;

/** A quote's words as sent (splitQuote's `quote`) back to what was quoted: a task's first line becomes its task. */
export function parseQuote(text: string): DraftQuote {
  const [first, ...rest] = text.split("\n");
  const m = TASK_LINE.exec(first.trim());
  if (!m) return { text };
  return { text: rest.join("\n").trim(), task: { title: m[1], ...(m[2] ? { id: m[2] } : {}) } };
}

export type DraftEvent =
  | ({ type: "quote" } & DraftQuote)
  /** `edit`: it starts editing the last message (#4503); any other replace ends an edit. */
  | { type: "replace"; text: string; edit?: boolean }
  | { type: "append"; text: string }
  /** The words of a send that failed after its box moved on (another conversation, or closed): back in front. */
  | { type: "unsent"; text: string }
  /** `edited`: the picture came out of the image editor; its source, edits and history, so the composer's tile opens
   *  it where the editor stopped (#4991). */
  | { type: "image"; image: DraftImage; edited?: EditedImage };

/** "> " in front of every line, then a blank line, then what was already typed (ChatView.swift bindMenu). */
export function withQuote(draft: string, text: string): string {
  const quoted = text.split("\n").map((l) => `> ${l}`).join("\n");
  return `${quoted}\n\n${draft}`;
}

/** One quote's words as they go into the message (a task's first line, then the words). */
function quoteBody(q: DraftQuote): string {
  const words = q.text.trim();
  return q.task ? [taskQuoteLine(q.task), words].filter(Boolean).join("\n\n") : words;
}

/** The quotes waiting as the blocks that lead the message: the message quote first, then the comments numbered in order. */
export function quoteBlocks(quotes: DraftQuote | readonly DraftQuote[] | null): string[] {
  const list = quotes === null ? [] : "text" in quotes ? [quotes] : quotes;
  let n = 0;
  const bodies = [...list.filter((q) => !q.comment), ...list.filter((q) => q.comment)].map((q) => (q.comment ? commentBlock(q, ++n) : quoteBody(q)));
  return bodies.filter(Boolean).map((b) => withQuote("", b).trimEnd());
}

/** The message as sent with its quotes: their blocks, a blank line, then what was typed (just the blocks when nothing was). */
export function withQuoteBlock(quotes: DraftQuote | readonly DraftQuote[] | null, text: string): string {
  const blocks = quoteBlocks(quotes);
  if (!blocks.length) return text;
  const lead = blocks.join("\n\n");
  return text.trim() ? `${lead}\n\n${text}` : lead;
}

/** A message that starts with a `> ` block (sent with "Quote", or typed): the quoted words and what follows. */
export function splitQuote(text: string): { quote: string; rest: string } | null {
  const lines = text.replace(/^\s*\n/, "").split("\n");
  let n = 0;
  while (n < lines.length && /^ {0,3}>/.test(lines[n])) n++;
  if (!n) return null;
  const quote = lines.slice(0, n).map((l) => l.replace(/^ {0,3}> ?/, "")).join("\n").trim();
  if (!quote) return null;
  return { quote, rest: lines.slice(n).join("\n").trim() };
}

/** A message's leading quotes: the first `> ` block, and after it the comment blocks (each starts `Comment N` /
 *  `Code comment N`, lib/comments.ts); what follows them is `rest`. */
export function splitQuotes(text: string): { quotes: string[]; rest: string } | null {
  const first = splitQuote(text);
  if (!first) return null;
  const quotes = [first.quote];
  let rest = first.rest;
  for (let next = splitQuote(rest); next && isCommentHead(next.quote.split("\n")[0]); next = splitQuote(rest)) {
    quotes.push(next.quote);
    rest = next.rest;
  }
  return { quotes, rest };
}

/** The quotes of a sent message's blocks back to what was quoted (an edit puts them back above the input). */
export function parseQuotes(blocks: readonly string[]): DraftQuote[] {
  return blocks.map((b) => (isCommentHead(b.split("\n")[0]) ? parseCommentBlock(b) : null) ?? parseQuote(b)).map((q) => ({ ...q, id: uuid() }));
}

const quoteKey = (key: string) => `quote:${key}`;

function validQuote(q: unknown): q is DraftQuote {
  const x = q as DraftQuote | null;
  return Boolean(x && typeof x === "object" && typeof x.text === "string" && (x.text || x.task?.title || x.comment));
}

/** The quotes kept for a composer's draft key (read on mount, like the draft). An older build kept just one object. */
export function storedQuotes(key: string): DraftQuote[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(quoteKey(key)) ?? "null");
    return (Array.isArray(v) ? v : [v]).filter(validQuote).map((q) => (q.id ? q : { ...q, id: uuid() }));
  } catch {
    return [];
  }
}

export function storeQuotes(key: string, list: readonly DraftQuote[]) {
  if (list.length) localStorage.setItem(quoteKey(key), JSON.stringify(list));
  else localStorage.removeItem(quoteKey(key));
}

/** What a composer's quote list is right now, and which comment card is being written in. */
export interface QuoteState {
  list: readonly DraftQuote[];
  /** The id of the card whose note is open for writing. */
  editing: string | null;
  /** That card is new: Esc takes it back instead of leaving the note as it was. */
  fresh: boolean;
}

const EMPTY: QuoteState = { list: [], editing: null, fresh: false };
const quoteStates = new Map<string, QuoteState>();
const quoteListeners = new Set<() => void>();

/** The composer's quotes (memory, filled from the stored ones the first time a key is asked for). */
export function getQuotes(key: string): QuoteState {
  let st = quoteStates.get(key);
  if (!st) {
    const list = storedQuotes(key);
    st = list.length ? { list, editing: null, fresh: false } : EMPTY;
    quoteStates.set(key, st);
  }
  return st;
}

function putQuotes(key: string, st: QuoteState) {
  quoteStates.set(key, st.list.length ? st : EMPTY);
  storeQuotes(key, st.list);
  quoteListeners.forEach((l) => l());
}

export function useQuotes(key: string): QuoteState {
  return useSyncExternalStore(
    (l) => {
      quoteListeners.add(l);
      return () => quoteListeners.delete(l);
    },
    () => getQuotes(key),
    () => getQuotes(key),
  );
}

/** A quote joins the list: a message quote takes the place of the last one (first in the list), a comment comes last.
 *  Returns its id. A comment starts open for writing (`write`). */
export function addQuote(key: string, q: DraftQuote, write = Boolean(q.comment)): string {
  const st = getQuotes(key);
  const added: DraftQuote = { ...q, id: uuid() };
  const list = q.comment ? [...st.list, added] : [added, ...st.list.filter((x) => x.comment)];
  putQuotes(key, { list, editing: write ? added.id! : st.editing && list.some((x) => x.id === st.editing) ? st.editing : null, fresh: write });
  return added.id!;
}

/** The whole list replaced (a send that cleared it, a failed one that puts it back, an edit that returns the quotes). */
export function setQuotes(key: string, list: readonly DraftQuote[]) {
  putQuotes(key, { list, editing: null, fresh: false });
}

export function removeQuote(key: string, id: string) {
  const st = getQuotes(key);
  const list = st.list.filter((q) => q.id !== id);
  putQuotes(key, { list, editing: st.editing === id ? null : st.editing, fresh: st.editing === id ? false : st.fresh });
}

/** The note of a comment card; `done` closes it for writing. */
export function setNote(key: string, id: string, body: string, done = false) {
  const st = getQuotes(key);
  const list = st.list.map((q) => (q.id === id && q.comment ? { ...q, comment: { ...q.comment, body: clampComment(body) } } : q));
  putQuotes(key, done ? { list, editing: null, fresh: false } : { ...st, list });
}

/** Opens a card's note for writing again (a click on "Comment"). */
export function editNote(key: string, id: string) {
  const st = getQuotes(key);
  if (st.editing !== id) putQuotes(key, { ...st, editing: id, fresh: false });
}

/** Esc in a note: a new card goes, an older one keeps its last note (the caller restores it). */
export function cancelNote(key: string, id: string, previous: string) {
  const st = getQuotes(key);
  if (st.fresh && st.editing === id) removeQuote(key, id);
  else setNote(key, id, previous, true);
}

/** A pulled-back queued message goes under what is typed, on its own line (AppModel.editQueued). */
export function withAppended(draft: string, text: string): string {
  return draft ? `${draft}\n${text}` : text;
}

/** The draft after a send failed: the composer emptied itself when the message went out, so the text comes back;
 * what was typed while it was on its way stays, under it on its own line. */
export function withUnsent(draft: string, unsent: string): string {
  if (!unsent) return draft;
  return draft ? `${unsent}\n${draft}` : unsent;
}

/** The draft after a text event (an image changes the attachments and a quote the bar above, not the text). */
export function applyDraft(draft: string, e: DraftEvent): string {
  switch (e.type) {
    case "replace":
      return e.text;
    case "append":
      return withAppended(draft, e.text);
    case "unsent":
      return withUnsent(draft, e.text);
    default:
      return draft;
  }
}

/** Replacing asks first only when something else is typed ("Edit" must not silently drop a draft). */
export function replaceNeedsConfirm(draft: string, text: string): boolean {
  return draft.trim() !== "" && draft.trim() !== text.trim();
}

type Listener = (e: DraftEvent) => void;
const listeners = new Map<string, Set<Listener>>();

// The sidebar's rows mark a conversation with something left unsent while its box is not on screen (#5568): they hear
// of the words a box keeps (`draft:<key>`, read when the box mounts, written through storeDraft), of its quotes, and
// of a box mounting or going away for a key (onDraftEvent).
const rowListeners = new Set<() => void>();
const tellRows = () => rowListeners.forEach((l) => l());
const subscribeRows = (l: () => void) => {
  rowListeners.add(l);
  quoteListeners.add(l);
  return () => {
    rowListeners.delete(l);
    quoteListeners.delete(l);
  };
};

/** A composer listens for its draft key; returns the unsubscribe. */
export function onDraftEvent(key: string, l: Listener): () => void {
  if (!listeners.has(key)) listeners.set(key, new Set());
  listeners.get(key)!.add(l);
  tellRows();
  return () => {
    const ls = listeners.get(key);
    ls?.delete(l);
    // the last composer of a key takes its set along: readers only ask for its size, so a missing key reads the same
    if (ls?.size === 0) listeners.delete(key);
    tellRows();
  };
}

/** Tests only: how many draft keys have a listener set. */
export function draftListenerKeysForTests() {
  return listeners.size;
}

export function storeDraft(key: string, text: string) {
  if (text) storeValue(`draft:${key}`, text);
  else removeStoredValue(`draft:${key}`);
  tellRows();
}

/** What waits unsent for a conversation whose box is not on screen: its stored words, and whether quotes wait on its
 *  bar. Nothing while its box is mounted (you are in it; typing there does not re-render the list), or for `null`. */
export function useLeftDraft(key: string | null): { text: string; quoted: boolean } {
  const away = () => key !== null && !listeners.get(key)?.size;
  const read = () => (away() ? (storedValue(`draft:${key}`) ?? "") : "");
  const hasQuotes = () => away() && getQuotes(key!).list.length > 0;
  const text = useSyncExternalStore(subscribeRows, read, read);
  const quoted = useSyncExternalStore(subscribeRows, hasQuotes, hasQuotes);
  return { text, quoted };
}

/** Sends an event to the composer of `key`. False when none is mounted and it could not be kept (an image). */
export function sendDraft(key: string, e: DraftEvent): boolean {
  const ls = listeners.get(key);
  if (ls?.size) {
    ls.forEach((l) => l(e));
    return true;
  }
  if (e.type === "image") return false;
  if (e.type === "quote") {
    addQuote(key, { text: e.text, from: e.from, ts: e.ts, task: e.task }, false);
    return true;
  }
  // "Edit" / "Rewind" a message sent with a quote while its box is away: as the composer would, the quotes go back to the bar
  // above the input (any it had are replaced) and only the words into the draft.
  const split = e.type === "replace" ? splitQuotes(e.text) : null;
  if (e.type === "replace") setQuotes(key, split ? parseQuotes(split.quotes) : []);
  const next = split ? split.rest : applyDraft(storedValue(`draft:${key}`) ?? "", e);
  storeDraft(key, next);
  return true;
}
