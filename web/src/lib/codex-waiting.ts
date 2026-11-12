import { stateOf } from "./format";
import type { Session } from "./types";

/** A Codex opened with no first message (docs/integrations/codex.md "Starting without a first message"). */
export function waitsForFirstMessage(s: Pick<Session, "awaiting_first_message"> | undefined): boolean {
  return s?.awaiting_first_message === true;
}

/** The session a waiting Codex row became: the row the same pane lists now, once Codex has a thread (its first message
 *  went in from here or from the terminal). The row's id is gone from the list then, so its page follows the pane. */
export function codexThreadOf(rows: readonly Session[], waiting: Pick<Session, "session_id" | "pane_id">): Session | undefined {
  if (!waiting.pane_id) return undefined;
  return rows.find((x) => x.pane_id === waiting.pane_id && x.session_id !== waiting.session_id && !waitsForFirstMessage(x) && x.agent === "codex" && stateOf(x) !== "offline");
}
