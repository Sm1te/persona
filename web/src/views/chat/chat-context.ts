// What the chat hands down to its rows without threading props through every step: the background tasks and their
// calls (to badge a call that went to the background and name a notice's call), the subagents (their cards), the
// workflow runs' calls (their cards), the items that came in live (their words fade in), and the key the open / closed
// state is kept under.
import { createContext, useContext } from "react";
import type { ExternalOrigin } from "../../lib/conversation-origins";
import { emptyTaskIndex, type TaskIndex } from "../../lib/tasks";
import type { Subagent } from "../../lib/types";
import { NO_WORKFLOW_CALLS, type WorkflowCalls } from "../../lib/workflows";
import type { WorkLineStyle } from "../../lib/work-groups";

export interface ChatContextValue {
  tasks: TaskIndex;
  /** The session's subagents, by the tool_use_id of the call that launched them. */
  subagents: Subagent[];
  /** Prefixes the keys of what the reader opened (expanded.ts): one chat's groups do not open another's. */
  scope: string;
  /** Brings the call with this tool_use_id into view, opening the groups it is folded in (a background task's
   *  "Jump to the step that started it"). */
  jumpToCall?: (call: string) => void;
  /** The Workflow calls whose run the hub lists (#3434): they draw the run's card, and a running one stays out of the
   *  folded work. Only which runs there are, not their progress (each card reads that), so it seldom changes. */
  workflowCalls?: WorkflowCalls;
  /** Conversation identity; both styles share the collapsed work-line default (#2602). */
  lineStyle?: WorkLineStyle;
  /** The message an extension opened this conversation with and where it came from (#3758, `externalOpening`). */
  opening?: { id: string; origin: ExternalOrigin };
  /** The exchange whose last words carried the closeout the card above the transcript shows (#3612): they are drawn
   *  without the raw block. Unset while no card is shown. */
  closeoutSegment?: string;
}

export const ChatContext = createContext<ChatContextValue>({ tasks: emptyTaskIndex(), subagents: [], scope: "", workflowCalls: NO_WORKFLOW_CALLS });

/** Counts the live work lines' clocks on screen (+1 shown, -1 gone): while one ticks, the running bar above the
 *  composer leaves the time to it (#3534). Null outside a chat with that bar. */
export const LiveClockContext = createContext<((delta: 1 | -1) => void) | null>(null);

export function useChatContext() {
  return useContext(ChatContext);
}

