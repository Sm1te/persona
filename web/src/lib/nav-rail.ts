// Genuine loaded user inputs for the edge navigator. The data-source adapter can replace this projection with
// the independent index without coupling the rail's gestures to transcript loading.

import { NAV_RAIL_METRICS } from "../ui/nav-rail-metrics";
import { isLoopTick, isOwnMessage, type ChatLine, type Segment, type UserMessage } from "./turns";

/** One of your questions in the loaded conversation: what a tick knows. */
export interface NavRailTick {
  /** The line's id (a `seg:…` ChatLine): what a jump looks for in the scroller (`[data-line=…]`). */
  id: string;
  /** Optional loaded row owner and stable sequence from the independent server index. */
  lineId?: string;
  seq?: number;
  /** An unloaded input's ordinal, localized by the component rather than cached in its preview. */
  ordinal?: number;
  /** The question's first line worth showing (quote blocks skipped, trimmed, capped): the preview card and the label. */
  preview: string;
  /** A question with no words of its own (only pictures, files or picked contexts): how many, for a fallback label. */
  attachmentsOnly?: number;
  /** A bounded answer excerpt, or visible follow-up text in this exchange while the answer is not available. */
  answerExcerpt?: string;
}

/** Even a single loaded input is a useful position and selection target. */
export const NAV_RAIL_MIN_TICKS = 1;
export const NAV_RAIL_TICK_PITCH = NAV_RAIL_METRICS.tickPitch;
export const NAV_RAIL_TICK_MIN_PITCH = NAV_RAIL_METRICS.minTickPitch;
export const NAV_RAIL_MIN_HEIGHT = NAV_RAIL_METRICS.minHeight;
/** The card renders at most two excerpt lines; this also bounds the underlying accessible description. */
export const NAV_RAIL_EXCERPT_MAX = 200;
/** The question line's cap for the label; the card truncates to one line, the label carries the whole preview. */
export const NAV_RAIL_PREVIEW_MAX = 120;

/** The questions of the loaded lines, oldest first: one tick per turn you wrote yourself. Persona's own messages,
 *  other sessions' and other conversations' (`isOwnMessage`), /loop wake-ups (`isLoopTick`) and lines that are not
 *  turns (notices, compactions, recaps, errors) never become ticks; a question with neither words nor attachments has
 *  nothing to show or jump to and is skipped. Only what is loaded counts: the rail never pages history in. */
export function navRailTicks(lines: ChatLine[]): NavRailTick[] {
  const out: NavRailTick[] = [];
  for (const line of lines) {
    if (line.kind !== "segment") continue;
    const { seg } = line;
    let read = ticksOf.get(line);
    if (!read || read.seg !== seg || read.user !== seg.user || read.answer !== seg.answer || read.steps !== seg.steps) {
      read = { seg, user: seg.user, answer: seg.answer, steps: seg.steps, tick: segmentTick(line) };
      ticksOf.set(line, read);
    }
    if (read.tick) out.push(read.tick);
  }
  return out;
}

/** Each segment line's tick (null: none), read once per line object. stabilizeLines hands an unchanged line's object
 *  on to the next push, so a push reads only the lines it changed (re-reading every answer was most of an items
 *  push's script at 200 turns). The parts the tick is read from are kept with it, so a line given another question,
 *  answer or steps is read again. The ticks are shared: read-only. */
const ticksOf = new WeakMap<ChatLine, { seg: Segment; user: Segment["user"]; answer: Segment["answer"]; steps: Segment["steps"]; tick: NavRailTick | null }>();

function segmentTick(line: Extract<ChatLine, { kind: "segment" }>): NavRailTick | null {
  const msg = line.seg.user;
  if (!msg || !isOwnMessage(msg) || isLoopTick(msg)) return null;
  const preview = navRailPreview(wordsOf(msg));
  const attachments = msg.images.length + (msg.files?.length ?? 0) + (msg.photos?.length ?? 0) + msg.contexts.length;
  if (!preview && !attachments) return null;
  const answer = responseExcerpt(line.seg);
  return { id: line.id, preview, ...(preview ? {} : { attachmentsOnly: attachments }), ...(answer ? { answerExcerpt: answer } : {}) };
}

/** Prefer the answer, then visible text from this exchange only. Never inspect tool payloads, private reasoning,
 *  injected wake text or another session to manufacture a hover summary. */
function responseExcerpt(seg: Segment): string {
  const answer = navRailExcerpt(seg.answer?.text ?? "");
  if (answer) return answer;
  for (const step of seg.steps) {
    if (step.kind !== "text" && step.kind !== "notice") continue;
    const item = step.item;
    if (item.role !== "assistant" || item.from || item.injected || item.wake || item.supervisionUser) continue;
    const text = navRailExcerpt(item.text ?? "");
    if (text) return text;
  }
  return "";
}

/** A merged bubble (several queued messages in one turn) previews its parts in order; the first line of the first
 *  part that has one is the question's face. */
function wordsOf(msg: UserMessage): string {
  return msg.parts ? msg.parts.join("\n") : msg.text;
}

/** The question's first own line: quote blocks (`> `, drawn as cards by Quote.tsx) are skipped for your words above
 *  them, and a message that is all quote falls back to the quote's first line. */
export function navRailPreview(text: string): string {
  const lines = text.split("\n");
  const own = lines.find((l) => l.trim() && !l.trimStart().startsWith(">"));
  const any = lines.find((l) => l.trim()) ?? "";
  const line = (own ?? any).replace(/^\s*>\s?/, "").trim();
  return line.length > NAV_RAIL_PREVIEW_MAX ? `${line.slice(0, NAV_RAIL_PREVIEW_MAX - 1)}…` : line;
}

/** The answer's start as one plain line: Markdown marks a preview does not render are stripped (links keep their
 *  text, headings and quotes their prefix, bold and code their marks), whitespace folds, and the result is capped.
 *  A long answer is read from its head when that gives the same result (excerptHead). */
export function navRailExcerpt(raw: string): string {
  const head = excerptHead(raw);
  if (head !== undefined) {
    const out = plainLine(head);
    if (out.length >= NAV_RAIL_EXCERPT_MAX) return out.slice(0, NAV_RAIL_EXCERPT_MAX);
  }
  return plainLine(raw.replace(COMMENT, "").replace(LINK, "$1")).slice(0, NAV_RAIL_EXCERPT_MAX);
}

const COMMENT = /<!--[\s\S]*?-->/g;
const LINK = /\[([^\]]*)\]\([^)]*\)/g;
/** How much of a long answer the excerpt reads first: several times what the card shows, so the head of an answer in
 *  prose fills the excerpt; when it does not (a long comment or code block on top), the whole answer is read. */
const EXCERPT_HEAD = 1024;

/** The excerpt's steps after comments and links: line-start marks, bold and code marks, whitespace. */
function plainLine(text: string): string {
  return text
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/^>\s?/gm, "")
    .replace(/^[-+*]\s+/gm, "")
    .replace(/[*`]/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

/** A longer answer's first EXCERPT_HEAD characters through the end of their line, comments and links already replaced;
 *  undefined when one of those starts in the head and might end after it. Every comment and link in the head is then
 *  decided inside it, and the later steps look only within a line (a heading or list mark's spaces may run on past
 *  the cut, but only past it), so the head's result is a prefix of the whole answer's and its first
 *  NAV_RAIL_EXCERPT_MAX characters are the whole answer's excerpt when it has that many (nav-rail-cache.test.ts
 *  checks this against the whole-text excerpt on random Markdown). */
function excerptHead(raw: string): string | undefined {
  const cut = raw.indexOf("\n", EXCERPT_HEAD) + 1;
  if (cut <= 0 || cut === raw.length) return undefined;
  const head = raw.slice(0, cut);
  // A closer after the head completes anything still open at the cut: a comment or link that reaches into it may
  // close only further on in the whole answer, so the head cannot stand for that answer.
  let open = false;
  const bare = (head + "-->").replace(COMMENT, (m: string, at: number) => {
    if (at + m.length > head.length) open = true;
    return "";
  });
  if (open) return undefined;
  const text = bare.slice(0, -3);
  const linked = (text + "](x)").replace(LINK, (m: string, words: string, at: number) => {
    if (at + m.length > text.length) open = true;
    return words;
  });
  return open ? undefined : linked.slice(0, -4);
}

/** Idle marks stay uniformly short; only an interactive preview creates bell-shaped focus shoulders. */
export function navRailMark(index: number, count: number, current: number | null, preview: number | null) {
  const m = NAV_RAIL_METRICS;
  const isCurrent = index === current;
  if (preview === null) {
    return {
      state: isCurrent ? "current" : "idle",
      width: m.tickWidth,
      height: m.tickHeight,
      opacity: isCurrent ? 1 : m.tailOpacity,
    };
  }
  const sigma = Math.min(m.spread, Math.max(m.minSpread, (count - 1) / 2));
  const distance = index - preview;
  const bell = Math.exp(-(distance * distance) / (2 * sigma * sigma));
  const isPreview = index === preview;
  const peak = preview === current ? m.currentHoverWidth : m.hoverWidth;
  const width = m.tickWidth + (peak - m.tickWidth) * bell;
  return {
    state: isCurrent ? (isPreview ? "current-hover" : "current") : isPreview ? "hover" : "idle",
    width,
    height: isCurrent ? (isPreview ? m.currentHoverHeight : m.currentHeight) : isPreview ? m.hoverHeight : m.tickHeight,
    opacity: isCurrent || isPreview ? 1 : m.tailOpacity + m.shoulderOpacity * bell,
  };
}

export interface NavRailPlan {
  start: number;
  pitch: number;
  hidden: number;
  count: number;
}

/** Bound the DOM, not the navigable history: keep a window around the selected input. Keyboard navigation and
 *  the rail's wheel can move this window in either direction; older inputs are never permanently discarded. */
export function planNavRailTicks(count: number, height: number, focus = count - 1): NavRailPlan {
  if (count <= 0 || height <= 0) return { start: 0, pitch: NAV_RAIL_TICK_PITCH, hidden: 0, count: 0 };
  const capacity = Math.max(1, Math.floor(height / NAV_RAIL_TICK_MIN_PITCH));
  const shown = Math.min(count, capacity);
  const start = Math.max(0, Math.min(Math.floor(focus) - Math.floor(shown / 2), count - shown));
  return { start, pitch: Math.min(NAV_RAIL_TICK_PITCH, height / shown), hidden: count - shown, count: shown };
}

/** Cached monotone offsets make normal scrolling logarithmic, without per-message DOM reads. */
export function activeNavRailOffset(offsets: { index: number; top: number }[], top: number): number | null {
  if (!offsets.length) return null;
  let lo = 0;
  let hi = offsets.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (offsets[mid].top <= top) lo = mid + 1;
    else hi = mid;
  }
  return offsets[Math.max(0, lo - 1)].index;
}

/** Explicit data-source seam: an index adapter may load missing content before resolving a selection. */
export interface NavRailSource {
  ticks: NavRailTick[];
  total?: number;
  select: (id: string) => void | Promise<void>;
}
