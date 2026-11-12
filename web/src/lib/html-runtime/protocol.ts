// The messages between the page and an html card's worker (#4215). The worker runs the card's code in QuickJS and
// sends what it drew as operations; the page trusts none of it: every operation goes through the allowlist in
// sanitize.ts, every bridge call through judge (lib/assistant-html.ts) and a native confirmation.

/** The guest's fixed nodes: the document's html, head and body elements. */
export const HTML_ID = 1;
export const HEAD_ID = 2;
export const BODY_ID = 3;

export interface CardInfo {
  /** The frame's size in CSS pixels, the screen's pixel ratio. */
  width: number;
  height: number;
  dpr: number;
  /** The app's language (Intl), and whether it is dark (prefers-color-scheme inside the card). */
  lang: string;
  dark: boolean;
}

export type ToWorker =
  | { type: "start"; module: WebAssembly.Module; html: string; info: CardInfo }
  | { type: "event"; event: GuestEvent }
  | { type: "settle"; id: number; ok: boolean }
  | { type: "resize"; width: number; height: number }
  | { type: "ping"; n: number };

/** Why a card stopped by itself: too much time, too much memory, too much drawn, or its runtime failed. */
export type StopReason = "time" | "memory" | "size" | "crash";

export type FromWorker =
  | { type: "ops"; ops: string }
  | { type: "bitmap"; id: number; bitmap: ImageBitmap }
  | { type: "bridge"; id: number; kind: "reply" | "open"; value: string }
  | { type: "error"; message: string }
  | { type: "ready" }
  | { type: "stop"; reason: StopReason; detail?: string }
  | { type: "pong"; n: number };

/** An event the page saw on the card's frame, for the guest's node `target`, with the control's state after it. */
export interface GuestEvent {
  type: string;
  target: number;
  value?: string;
  checked?: boolean;
  open?: boolean;
  selectedIndex?: number;
  key?: string;
  shiftKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
  metaKey?: boolean;
}

/**
 * The operations the guest sends, as JSON arrays:
 *  ["t", parent, before, tree]  insert a subtree before `before` (0: at the end)
 *  ["r", id]                    remove a subtree
 *  ["a", id, name, value|null]  set or remove an attribute
 *  ["d", id, text]              a text node's text
 *  ["p", id, name, value]       a control's property (value, checked, selected, open, indeterminate)
 *  ["f", id]                    focus
 * A tree is [id, "#text", text] or [id, tag, attributes [[name, value]…], children […], ns ("svg" | ""), props {…}].
 */
export type Op = unknown[];
