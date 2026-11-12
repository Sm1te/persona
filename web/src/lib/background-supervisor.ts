// The background supervisor (#4927, #5082, docs/assistant/unified-entry.md "Background supervisor"): the hub's built-in
// conversation that follows up the tasks no leader supervises. The hub names it in GET /api/v1/assistant
// (`background_supervisor`, its session id once it was made); it is listed with the scheduled tasks' conversations,
// never as a direction or among the user's recent chats, and it is not renamed, archived, ended or made a direction.
// Pure functions, tested without a DOM.
import { t } from "../i18n";
import type { FeedItem } from "./feed";
import type { AssistantInfo, Session } from "./types";

/** `by` on a review's chat-activity entry the background supervisor made (#5084, docs/protocol/protocol.md, FeedItem "review"). */
export const REVIEW_BY_BACKGROUND = "background";

/** The purpose its conversation runs under (docs/assistant/model-config.md); it follows Main chat until it has its own. */
export const SUPERVISE_PURPOSE = "assistant.supervise";

/** Conversation `id` is the background supervisor. Never the main conversation; false on a hub before it, or before it was made. */
export function isBackgroundSupervisor(assistant: Pick<AssistantInfo, "background_supervisor" | "main"> | null | undefined, id: string | undefined): boolean {
  return !!id && !!assistant?.background_supervisor && assistant.background_supervisor === id && id !== assistant.main;
}

/** What it is called: the hub's title (it names it in the owner's language), else this page's own name for it. */
export function backgroundSupervisorTitle(s?: Pick<Session, "title">): string {
  const title = s?.title?.trim();
  return !title || title === "Claude Code" || title === "assistant" ? t("assistant-conversations.background_supervisor") : title;
}

/** The review entry is one the background supervisor made: the main chat took no turn for it, so the chat shows its
 *  line. Any other `by` (a value this page doesn't know) is not: the entry stays undrawn, as before. */
export function reviewedInBackground(item: Pick<FeedItem, "kind" | "body">): boolean {
  return item.kind === "review" && item.body?.by === REVIEW_BY_BACKGROUND;
}

/** The conversations in `list` (a machine's sessions as pushed) that may be a background supervisor the held assistant
 *  answer does not name yet: an assistant conversation of origin `task` with no `origin_ref` (a scheduled task's has
 *  its task there) that is neither the one named nor among the listed conversations. The hub makes the background
 *  supervisor on first use, and replaces one that is gone, while the page is open: the page learns its id only by
 *  reading the assistant again (lib/store.ts does, once for each such conversation). */
export function unnamedTaskConversations(assistant: Pick<AssistantInfo, "background_supervisor" | "conversations">, list: readonly Pick<Session, "session_id" | "assistant" | "origin" | "origin_ref">[]): string[] {
  return list
    .filter((s) => s.assistant && s.origin === "task" && !s.origin_ref && s.session_id !== assistant.background_supervisor && !assistant.conversations?.some((c) => c.session_id === s.session_id))
    .map((s) => s.session_id);
}
