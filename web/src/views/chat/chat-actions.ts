// What the chat's message parts need from the chat around them: where quotes, edits and edited images go (the
// composer's draft key), whether it takes them now, and the conversation's images for the viewer to page through.
import { createContext, useContext } from "react";
import type { ForwardPick } from "../../lib/forward";
import type { RewindRestore } from "../../lib/rewind";
import type { Item } from "../../lib/types";

export interface ChatActions {
  draftKey: string;
  /** The conversation title for message context cards, separate from the reply's speaker. */
  title: string;
  /** The composer can send here: "Quote" / "Use as context" / "Edit" / "Edit image" are offered only then. */
  canCompose: boolean;
  /** Who the replies are from, for "Quote"'s "Quote Persona · 16:45" (the assistant's name, or the agent). */
  speaker: string;
  /** Every image of the loaded conversation, oldest first. A function: read when the viewer opens, not on every push. */
  images: () => Item[];
  /** "Forward" (#3899): opens Forward to for a message of this chat, including when this chat is read-only. */
  forward?: (pick: ForwardPick) => void;
  /** "Branch to new chat" from a reply (its item id): set only when the session can be forked. */
  fork?: (itemId: string) => void;
  /** "Rewind to here" (#2436) on a message of yours: set for a session in a terminal that takes messages. `block` is why it is
   *  off for now ("" = on: Claude only, working…), live: it changes each time the session starts or stops working, and only
   *  an open menu reads it, so that does not render every message's actions again. `run` goes back to before the message
   *  (its UserMessage.id) and puts it in the composer, rejecting with the reason in the message. */
  rewind?: { block: LiveValue<string>; run: RewindRun };
}

export type RewindRun = (messageId: string, text: string, restore: RewindRestore) => Promise<void>;

/** A value that changes while the rest of the chat's actions stay the same: read where it shows (useSyncExternalStore). */
export interface LiveValue<T> {
  get: () => T;
  subscribe: (listener: () => void) => () => void;
}

export function liveValue<T>(value: T): LiveValue<T> & { set: (next: T) => void } {
  const listeners = new Set<() => void>();
  return {
    get: () => value,
    subscribe: (l) => {
      listeners.add(l);
      return () => void listeners.delete(l);
    },
    set: (next) => {
      if (Object.is(next, value)) return;
      value = next;
      listeners.forEach((l) => l());
    },
  };
}

export const ChatActionsContext = createContext<ChatActions | null>(null);

export function useChatActions(): ChatActions | null {
  return useContext(ChatActionsContext);
}
