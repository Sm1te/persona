// The hub side of an html component's bridge (#4215, docs/assistant/assistant-views.md "Free-form HTML rendering"): what
// the user confirmed goes to POST …/views/{key}/html-actions. Its own module, loaded with the html card (ui/HtmlCard.tsx),
// so the first screen doesn't carry it.
import { ApiError, api, enc } from "./api";
import type { HtmlActionRequest } from "./html-bridge";
import { applyAction, applyView, isCardVersion, type CardKey, type SubmitOutcome } from "./assistant-views";
import type { ViewAction, ViewChangedError } from "./extension-wire";

/** The path of an html component's bridge calls (POST, docs/assistant/assistant-views.md "Free-form HTML rendering"). */
export const htmlActionsPath = (conversation: string, key: CardKey) => `/assistant/conversations/${enc(conversation)}/views/${enc(key)}/html-actions`;

export type HtmlActionOutcome = { ok: true; open?: string } | { ok: false; outcome: Exclude<SubmitOutcome, "sent" | "busy">; message?: string };

/**
 * Sends a bridge call the user confirmed (persona.reply / persona.open of an html component, #4215). A reply is the
 * card's one answer, kept like a declared one; open comes back with what to open. The same 409s as submitAction:
 * answered from somewhere else, or the card changed under it.
 */
export async function postHtmlAction(conversation: string, key: CardKey, req: HtmlActionRequest): Promise<HtmlActionOutcome> {
  try {
    const res = await api<{ action?: ViewAction; open?: string }>(htmlActionsPath(conversation, key), { method: "POST", json: req });
    if (res.action) applyAction(conversation, res.action);
    return { ok: true, ...(typeof res.open === "string" && res.open ? { open: res.open } : {}) };
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.code === "already_answered") {
      applyAction(conversation, e.body.action as ViewAction | undefined);
      return { ok: false, outcome: "answered_elsewhere" };
    }
    if (e instanceof ApiError && e.status === 409 && e.code === "view_changed") {
      const latest = (e.body as Partial<ViewChangedError>).view;
      if (latest && latest.id === key && isCardVersion(latest.version)) applyView(conversation, latest);
      return { ok: false, outcome: "changed" };
    }
    return { ok: false, outcome: "failed", message: e instanceof Error ? e.message : String(e) };
  }
}
