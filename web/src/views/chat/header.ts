// The page header's side slots around a switch in its middle (the shell's Assistant | Agents, narrow screens): at least
// 64px each, the left's buttons at its start, the right's at its end, so two pages with the same switch have their
// buttons in the same places (iOS's new shell). A slot grows to fit what it holds (the main conversation's right side
// has New chat, Start an Agent and ⋯): its buttons never spill under the switch, which takes what room is left (#5036).
// 64px, not the 80 it was: the left slot holds one button, and the switch needs the room at 390px.
// Kept apart from ChatView so another page reads it cheaply.

/** A slot's box: `HEADER_SLOT` plus `HEADER_SLOT_START` or `HEADER_SLOT_END`. */
export const HEADER_SLOT = "flex shrink-0 items-center gap-2 max-lg:min-w-16";
export const HEADER_SLOT_START = "justify-start";
export const HEADER_SLOT_END = "justify-end";
