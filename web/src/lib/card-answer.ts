// Answering a card (POST /interactions/{id}/resolve): the card's own buttons (views/chat/Interactions.tsx), Overview's
// quick answers and a system notification's Allow / Deny (lib/notification-answer.ts) go through here, so all check the
// same, send the same and see the one answer on its way for each card.
import { useEffect, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { ApiError, enc, errorText } from "./api";
import { answerLines, endedText, type Resolution, type ResolveResult } from "./answered";
import { fields } from "./elicitation";
import { interactionCanAnswer } from "./format";
import { apiOp } from "./ops";
import { endInteraction, getSession, keepDelegated, useStore } from "./store";
import { isSecretHandoff, isSecretRun } from "./tool-detail";
import type { Interaction } from "./types";

/** Sent (`already`: it had been answered, here or elsewhere), or why not, as a sentence to show. */
export type CardAnswer = { ok: true; already: boolean } | { ok: false; error: string };

/** What a password card (#1137) may send: its own form's answers, a bare decline or an allow with the typed password
 *  alone. Anything else (a quick Allow, a reason, Always allow, hand back, other values) is refused before a request. */
function isSecretRunAnswer(body: Resolution): boolean {
  const keys = Object.keys(body);
  if (body.allow === false) return keys.length === 1;
  const values = (body.values ?? {}) as Record<string, unknown>;
  return body.allow === true && keys.length === 2 && Object.keys(values).length === 1 && typeof values.password === "string" && values.password.trim() !== "";
}

/** What a hand-off card's own approval says on its request, and only there: the hub lets an approval of a hand-off
 *  card through only from a request that declares it (docs/protocol/protocol.md "Secret hand-off cards"). */
const HANDOFF_HEADERS = { "X-Persona-Features": "secret_handoff" };

/** Where an answer comes from: the hand-off card's own buttons say so; every other path (Overview, a notification) is
 *  not that card. */
export interface AnswerFrom {
  handoffCard?: boolean;
}

/** A property name that holds a credential whatever the form says about it (the hub's secretName, answered.go). */
const secretName = /pass|secret|token|credential|api[_-]?key|private|otp|pin\b/i;

/** The answer as the answered receipt keeps it, as the hub's history keeps it (#1600): each value filled in only by its
 *  key, a secret card's typed text masked, and an MCP form's secret properties (by schema or by name) masked. */
function redacted(ix: Interaction, body: Resolution): Resolution {
  let r = body;
  if (ix.secret && r.text) r = { ...r, text: "<redacted>" };
  if (r.values && typeof r.values === "object") r = { ...r, values: Object.fromEntries(Object.keys(r.values).map((k) => [k, "<redacted>"])) };
  if (r.content && typeof r.content === "object") {
    const secret = new Set(fields(ix.elicitation?.requested_schema).filter((f) => f.secret).map((f) => f.key));
    r = { ...r, content: Object.fromEntries(Object.entries(r.content).map(([k, v]) => [k, secret.has(k) || secretName.test(k) ? "<redacted>" : v])) };
  }
  return r;
}

/** An answer on its way to the hub (apiOp keeps trying through a gap, up to OP_OFFLINE_MS while the hub is away): what
 *  it says, as its receipt will show it (redacted), and how it ends. */
interface Pending {
  body: Resolution;
  shown: Resolution;
  done: Promise<CardAnswer>;
}

/** At most one per card, by interaction id: it outlives the card's own state (leaving the chat unmounts it). */
const pending = new Map<string, Pending>();
const watchers = new Set<() => void>();
const changed = () => watchers.forEach((l) => l());

/** Plain JSON answers, equal key by key (in any order). */
function sameAnswer(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (!a || !b || typeof a !== "object" || typeof b !== "object" || Array.isArray(a) !== Array.isArray(b)) return false;
  const x = a as Record<string, unknown>;
  const y = b as Record<string, unknown>;
  const keys = Object.keys(x);
  return keys.length === Object.keys(y).length && keys.every((k) => k in y && sameAnswer(x[k], y[k]));
}

/**
 * Answers a card, once at a time: while an answer to it is on its way, the same answer again shares that request and
 * its result, and a different one is refused without a request. It is not swapped in: a retry keeps its op id and
 * body (an op id with another body is 409 op_id_reused, docs/lessons/web.md #2459), and the first one may have reached
 * the hub already. Once it ends (delivered, refused, or out of time) the card can be answered again.
 */
export function answerCard(ix: Interaction, body: Resolution, from: AnswerFrom = {}): Promise<CardAnswer> {
  if (!interactionCanAnswer(ix, getSession(ix.machine_id, ix.session_id))) return Promise.resolve({ ok: false, error: t("chat-interactions.approve_not_allowed") });
  if (isSecretRun(ix) && !isSecretRunAnswer(body)) return Promise.resolve({ ok: false, error: t("chat-interactions.secret_run_card_only") });
  // a hand-off is approved on its card alone; a no goes from anywhere
  const handoff = isSecretHandoff(ix) && body.allow === true;
  if (handoff && !from.handoffCard) return Promise.resolve({ ok: false, error: t("chat-interactions.secret_handoff_card_only") });
  const cur = pending.get(ix.id);
  if (cur) return sameAnswer(cur.body, body) ? cur.done : Promise.resolve({ ok: false, error: t("chat-interactions.answer_pending") });
  const entry: Pending = {
    body,
    shown: redacted(ix, body),
    done: send(ix, body, handoff ? HANDOFF_HEADERS : undefined).finally(() => {
      if (pending.get(ix.id) !== entry) return;
      pending.delete(ix.id);
      changed();
    }),
  };
  pending.set(ix.id, entry);
  changed();
  return entry.done;
}

const watch = (l: () => void) => {
  watchers.add(l);
  return () => {
    watchers.delete(l);
  };
};

/** The answer on its way for this card, as its receipt will show it (redacted); undefined when there is none. */
export function usePendingAnswer(id: string): Resolution | undefined {
  const read = () => pending.get(id)?.shown;
  return useSyncExternalStore(watch, read, read);
}

/** How long an answer goes on its way with the hub connected before its card says so: one delivered at once shows no
 *  line that comes and goes (the button that sent it spins meanwhile). With the hub away it is said at once. */
export const PENDING_LINE_MS = 500;

/**
 * An answer on its way for this card, wherever it was given (the card's buttons, Overview's row, a notification), and
 * the line that says so, the same on the chat's card and Overview's row: "Sending: Allowed", or "Sends when connected:
 * Allowed" while the hub is away. `line` is empty when there is none, and for the first PENDING_LINE_MS online.
 */
export function usePendingLine(ix: Interaction): { pending: boolean; line: string } {
  const answer = usePendingAnswer(ix.id);
  const offline = useStore((s) => s.conn !== "online");
  const [late, setLate] = useState(false);
  useEffect(() => {
    setLate(false);
    if (!answer) return;
    const timer = setTimeout(() => setLate(true), PENDING_LINE_MS);
    return () => clearTimeout(timer);
  }, [answer]);
  if (!answer || !(offline || late)) return { pending: !!answer, line: "" };
  const words = answerLines(ix, answer).map((l) => l.answer).join(t("lib-answered.separator"));
  return { pending: true, line: t(offline ? "chat-interactions.pending_offline" : "chat-interactions.pending_sending", { answer: words }) };
}

async function send(ix: Interaction, body: Resolution, headers?: Record<string, string>): Promise<CardAnswer> {
  try {
    // one op id for this answer: sent again through a gap (a hub restart), it is taken once
    const res = await apiOp<ResolveResult>(`/interactions/${enc(ix.id)}/resolve`, { method: "POST", json: body, headers });
    // Answered already (here, its event missed; or elsewhere): the hub says with what, and the card shows that.
    const already = res?.already === true;
    const r: Resolution = already ? (res.resolution ?? {}) : redacted(ix, body);
    // The assistant answered this question first (#544): it shows as its answer, which the user may change.
    if (already && r.answered_by === "assistant" && ix.kind === "question") {
      keepDelegated(ix, r);
      return { ok: true, already };
    }
    // Handed back now, or the hub says it was handed back already: the agent waits on the computer (#4877). A hand back
    // leaves a receipt too, as an answer does: the hub takes no other answer for this card any more, so the card must
    // not just vanish (answered-lookup still counts the agent as waiting).
    const handedBack = already ? res.status === "dismissed" : r.dismiss === true;
    endInteraction(ix.id, { ix, resolution: r, already, handedBack });
    return { ok: true, already };
  } catch (e) {
    if (e instanceof ApiError && e.status === 410) {
      endInteraction(ix.id); // over (expired, withdrawn, handed back): nothing left to answer
      return { ok: false, error: endedText(e.body.status) };
    }
    if (e instanceof ApiError && e.code === "prompt_changed") return { ok: false, error: t("chat-interactions.prompt_changed") };
    return { ok: false, error: t("chat-interactions.send_failed", { error: errorText(e) }) };
  }
}
