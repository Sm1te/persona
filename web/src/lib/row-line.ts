// The sidebar's agent rows say under their title what they need from you or what they did (#3195), so the owner
// does not have to hover or open each one: the same words as the to-dos page's agent rows (todos.json kicker_*).
import { stateName } from "./state-labels";
import { t } from "../i18n";
import { sessionPreview, stateOf } from "./format";
import { usageLimitLabel } from "./usage-limit";
import { replyAsk } from "./agent-groups";
import { interactionAsk } from "./inbox";
import { plainMarkdown } from "./plain-markdown";
import { waitsForFirstMessage } from "./codex-waiting";
import type { Interaction, Session } from "./types";

/** What kind of card waits, in a few words (docs/design/DESIGN_SYSTEM.md 5.6): "Has a question" / "Plan awaiting approval" /
 *  "Waiting for your approval" / "Terminal needs you" / "MCP request". */
export function interactionKicker(ix: Pick<Interaction, "kind"> | undefined): string {
  switch (ix?.kind) {
    case "plan":
      return t("todos.kicker_plan");
    case "permission":
      return t("todos.kicker_permission");
    case "screen":
      return t("todos.kicker_screen");
    case "elicitation":
      return t("todos.kicker_elicitation");
    default:
      return t("todos.kicker_question");
  }
}

// a row shows one line of it; the rest is cut by the row anyway
const MAX = 160;

/**
 * The second line of an agent's sidebar row: for one that needs you, its card's kind and what it asks
 * ("Has a question: which test framework?"), else why it waits (sessionPreview: the host's reason, the error, the hub's triage, its
 * last words); for one that finished unseen, what it did ("✓ conclusion" or its last words, as plain text); for one seen and
 * awaiting the user's reply (`awaiting`, lib/agent-groups.ts awaitsReply, #5096), "Awaiting your reply:" and what it asks;
 * for a Codex opened with no first message, that it waits for one.
 * Undefined for every other state, and when there is nothing to say. `lead` is the colored part (`tone`: red for
 * needs you, the unread tint for Awaiting Reply), colon included when text follows.
 */
export function rowLine(s: Session, ix?: Interaction, awaiting = false): { lead?: string; text: string; tone?: "needs" | "reply" } | undefined {
  const st = stateOf(s);
  // A Codex opened with no first message: it waits for one, from here or its terminal (lib/codex-waiting.ts).
  if (waitsForFirstMessage(s) && st === "idle") return { text: t("sidebar.codex_waiting") };
  // A usage limit is a wait, not "needs you" and not an unseen finish: the row says when it resumes.
  const limit = st !== "needs_you" ? usageLimitLabel(s) : undefined;
  if (limit) return { text: limit };
  if (awaiting && st === "idle") {
    const text = plainMarkdown(replyAsk(s)).slice(0, MAX);
    const lead = stateName("awaits_reply");
    return text ? { lead: t("todos.kicker_line", { kicker: lead }), text, tone: "reply" } : { lead, text: "", tone: "reply" };
  }
  if (st !== "needs_you" && st !== "unread") return undefined;
  if (st === "needs_you" && ix) {
    const kicker = interactionKicker(ix);
    const text = plainMarkdown(interactionAsk(ix)).slice(0, MAX);
    return text ? { lead: t("todos.kicker_line", { kicker }), text } : { lead: kicker, text: "" };
  }
  const text = plainMarkdown(sessionPreview(s)).slice(0, MAX);
  return text ? { text } : undefined;
}

/**
 * The second line of a conversation's sidebar row while something waits unsent in its box (#5568), in place of rowLine
 * as in Slack, WeChat and Telegram (the status mark and the count still say it needs you): "Draft: " and the words on one
 * line, or just "Draft" when only quotes wait. Undefined when the words are only whitespace and no quote waits.
 */
export function draftLine(d: { text: string; quoted: boolean }): { lead: string; text: string } | undefined {
  const text = d.text.replace(/\s+/g, " ").trim().slice(0, MAX);
  if (text) return { lead: t("todos.kicker_line", { kicker: t("sidebar.draft") }), text };
  return d.quoted ? { lead: t("sidebar.draft"), text: "" } : undefined;
}
