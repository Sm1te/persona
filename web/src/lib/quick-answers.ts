// The answers a card in "Overview"'s "Needs you" offers on its row (#4090), as iOS's InteractionPreviewCard.quickActions: a
// permission's "Deny" / "Allow" (not one that has fields to fill in), one single-choice question's options (four at most),
// a terminal dialog's plain options (one to four, read by the hub too). Every other card opens its chat. The bodies
// are the chat card's (views/chat/Interactions.tsx sends them through the same resolve).
import { t } from "../i18n";
import type { Resolution } from "./answered";
import { isSecretHandoff, isSecretRun } from "./tool-detail";
import type { Interaction } from "./types";

/** A permission card only its own form can allow: it has fields to fill in (#4987), it is a password card (#1137),
 *  answered only with the password typed on it, or a hand-off card (#3962), approved only on the card. Its quick paths
 *  (a row's answers, a notification's buttons) open the chat instead. */
export function needsFieldValues(ix: Interaction): boolean {
  return ix.kind === "permission" && (!!ix.fields?.length || isSecretRun(ix) || isSecretHandoff(ix));
}

/** What a permission card's "Allow" sends. */
export const allowBody = (): Resolution => ({ allow: true });
/** What a permission card's "Deny" sends: the agent reads the message. */
export const denyBody = (): Resolution => ({ allow: false, message: t("chat-interactions.denied_message") });

export interface QuickChoice {
  key: string;
  label: string;
  body: Resolution;
  primary?: boolean;
  /** What the option means (a question's description, a dialog option's detail): the button's tooltip. */
  hint?: string;
}

/** A row's one-tap answers; null when the card needs its chat (a plan, several questions, ticks, typing, a form). */
export function quickAnswers(ix: Interaction): QuickChoice[] | null {
  switch (ix.kind) {
    case "permission": {
      if (needsFieldValues(ix)) return null;
      return [
        { key: "allow", label: t("chat-interactions.allow"), body: allowBody(), primary: true },
        { key: "deny", label: t("chat-interactions.deny"), body: denyBody() },
      ];
    }
    case "question": {
      const qs = ix.questions ?? [];
      const q = qs[0];
      const options = q?.options ?? [];
      if (qs.length !== 1 || !q || q.multi_select || !options.length || options.length > 4) return null;
      return options.map((o, i) => ({ key: `o${i}`, label: o.label, body: { answers: { [q.question]: o.label } }, hint: o.description }));
    }
    case "screen": {
      const d = ix.dialog;
      const plain = (d?.options ?? []).filter((o) => !o.kind);
      // a question read off the terminal with several steps (its tab bar) or its review tab goes to the chat
      if (!d || d.multi || d.steps?.length || d.review || plain.length < 1 || plain.length > 4) return null;
      return plain.map((o) => ({ key: o.key, label: o.label, body: { option: o.key }, hint: o.detail }));
    }
    default:
      return null;
  }
}

/** A card answered from its row, kept in its slot while it is sent, then shown answered for a moment and folded away,
 *  so the next row does not move under the pointer. The slot is just before the row that followed it (`before`), or
 *  `index` when that one is gone too. */
export interface HeldRow {
  ix: Interaction;
  index: number;
  before?: string;
  phase: "sending" | "done" | "leaving";
  /** The choice sent (it spins) and what it sent (the answered row says it). */
  pick: string;
  body: Resolution;
}

/** The rows of "Needs you": the cards waiting, in their order, with each held one back in its slot. The last held first:
 *  a row answered right after the one above it is that one's `before`, already in place when it goes in. */
export function withHeld(waiting: Interaction[], held: HeldRow[]): { ix: Interaction; held?: HeldRow }[] {
  const ids = new Set(held.map((h) => h.ix.id));
  const out: { ix: Interaction; held?: HeldRow }[] = waiting.filter((w) => !ids.has(w.id)).map((ix) => ({ ix }));
  for (const h of [...held].sort((a, b) => b.index - a.index)) {
    const at = h.before === undefined ? -1 : out.findIndex((r) => r.ix.id === h.before);
    out.splice(at >= 0 ? at : Math.min(h.index, out.length), 0, { ix: h.ix, held: h });
  }
  return out;
}
