// The bounds an html card runs within on the web (#4215, docs/assistant/assistant-views.md "Do not overstate
// execution-time limits"). Over any of them the card stops and shows why, with Retry; none is a hard quota on the
// browser tab, only on what the card's own runtime and the page's copy of its drawing may take.

/** One turn of the card's code (a script, an event handler, a timer): QuickJS's interrupt handler stops it past this. */
export const TURN_MS = 200;
/** Runtime the card's code may use in any BUDGET_WINDOW_MS. */
export const BUDGET_MS = 2000;
export const BUDGET_WINDOW_MS = 10_000;

/** The page pings the worker this often; no answer for STALL_MS and the worker is terminated from outside. */
export const PING_MS = 1000;
export const STALL_MS = 3000;

/** The card's heap and stack inside QuickJS. */
export const HEAP_BYTES = 32 * 1024 * 1024;
export const STACK_BYTES = 512 * 1024;

/** Timers the card may have waiting at once, how soon they may fire, and how often an animation frame comes. */
export const TIMERS_MOST = 32;
export const TIMER_MIN_MS = 16;
export const FRAME_MS = 33;

/** What the page draws for the card: elements and text nodes, text, CSS, operations per batch, batches a second. */
export const NODES_MOST = 4000;
export const TEXT_BYTES_MOST = 512 * 1024;
export const CSS_BYTES_MOST = 64 * 1024;
export const OPS_PER_BATCH_MOST = 5000;
export const BATCHES_PER_SECOND_MOST = 60;
/** One batch as the worker sends it (JSON). */
export const BATCH_BYTES_MOST = 2 * 1024 * 1024;

/** What the display frame may tell the page: events a second (more are dropped), and one message's size. */
export const FRAME_EVENTS_PER_SECOND_MOST = 120;
export const FRAME_MESSAGE_BYTES_MOST = 72 * 1024;

/** A canvas: each side and its area. An inline image (data: URL) on an img. */
export const CANVAS_SIDE_MOST = 4096;
export const CANVAS_AREA_MOST = 4096 * 4096;
export const IMAGE_BYTES_MOST = 256 * 1024;

/** Cards running at once: on a phone-width screen, on a wide one. The rest wait with a Run button. */
export const RUNNING_NARROW = 2;
export const RUNNING_WIDE = 4;
/** Off screen this long and the card is torn down; back on screen, it starts again from the beginning. */
export const HIDDEN_GRACE_MS = 1000;
