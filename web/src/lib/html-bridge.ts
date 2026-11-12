// The bridge of an html card on the web (#4215, docs/assistant/assistant-views.md "Free-form HTML rendering"): a
// card's persona.reply / persona.open held to the one instance on screen, a native question at a time with a limit
// after a refusal, and the body the hub's html-actions endpoint takes (htmlRequest). Loaded with the card
// (ui/HtmlCard.tsx), never on the first screen: lib/assistant-html.ts keeps only what decides whether a card runs.
import { TEXT_MOST, openTarget } from "./assistant-blocks";
import type { CardKey } from "./assistant-views";
import type { AssistantHTMLActionInput } from "./extension-wire";

/** What POST …/views/{key}/html-actions takes: the generated AssistantHTMLActionInput, only the two kinds the bridge
 *  has and always confirmed (the user said yes natively, outside the HTML). */
export type HtmlActionRequest = AssistantHTMLActionInput & { kind: "reply" | "open"; confirmed: true };

/** One running html component: a fresh nonce each time it is mounted; its messages carry it. */
export interface HtmlInstance {
  nonce: string;
  conversation: string;
  key: CardKey;
  componentKey: string;
  page: string;
  version: number;
}

export type BridgeAsk = { kind: "reply"; text: string } | { kind: "open"; url: string };

export type BridgeVerdict = { ok: true; ask: BridgeAsk } | { ok: false; reason: "stale" | "shape" | "too_long" | "open" | "quiet" };

/** After a refusal, no new prompt for this long; and at most this many prompts per instance. */
export const REFUSED_QUIET_MS = 10_000;
export const PROMPTS_MOST = 3;

export interface BridgeState {
  /** The instance on screen now (one per card); a message from any other is old and dropped. */
  current?: HtmlInstance;
  prompts: number;
  quietUntil: number;
}

export const newBridge = (): BridgeState => ({ prompts: 0, quietUntil: 0 });

/** Mounting an instance: the older one's messages no longer count, the prompt count starts over. */
export function mounted(_st: BridgeState, inst: HtmlInstance): BridgeState {
  return { current: inst, prompts: 0, quietUntil: 0 };
}

/** Gone from the screen: its messages are old from now on. */
export function unmounted(st: BridgeState, nonce: string): BridgeState {
  return st.current?.nonce === nonce ? { ...st, current: undefined } : st;
}

/**
 * What a message from a frame asks, if it may be put to the user: from the instance on screen (its nonce, the card's
 * version it was started at), one of reply or open in the bridge's shape, within the limits, not while quiet after a
 * refusal or past the prompt limit.
 */
export function judge(st: BridgeState, msg: unknown, version: number, now: number): BridgeVerdict {
  const cur = st.current;
  if (!cur || !msg || typeof msg !== "object") return { ok: false, reason: "stale" };
  const m = msg as Record<string, unknown>;
  if (m.nonce !== cur.nonce || cur.version !== version) return { ok: false, reason: "stale" };
  if (now < st.quietUntil || st.prompts >= PROMPTS_MOST) return { ok: false, reason: "quiet" };
  if (m.kind === "reply" && typeof m.text === "string") {
    if (!m.text.trim()) return { ok: false, reason: "shape" };
    if ([...m.text].length > TEXT_MOST) return { ok: false, reason: "too_long" };
    return { ok: true, ask: { kind: "reply", text: m.text } };
  }
  if (m.kind === "open" && typeof m.url === "string") return openTarget(m.url) ? { ok: true, ask: { kind: "open", url: m.url } } : { ok: false, reason: "open" };
  return { ok: false, reason: "shape" };
}

/** A prompt shown: counted; refused: quiet for a while. */
export function prompted(st: BridgeState, refused: boolean, now: number): BridgeState {
  return { ...st, prompts: st.prompts + 1, quietUntil: refused ? now + REFUSED_QUIET_MS : st.quietUntil };
}

/** The request for what the user confirmed, at the version the instance was started at. */
export function htmlRequest(inst: HtmlInstance, ask: BridgeAsk, clientMsgId: string): HtmlActionRequest {
  return {
    kind: ask.kind,
    component_key: inst.componentKey,
    ...(inst.page ? { page: inst.page } : {}),
    expected_version: inst.version,
    ...(ask.kind === "reply" ? { text: ask.text } : { url: ask.url }),
    client_msg_id: clientMsgId,
    confirmed: true,
  };
}
