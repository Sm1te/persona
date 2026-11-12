// An html component's height (#4215): the schema's bounds and default for the height a card gives, and the height its
// frame takes, which follows what the card draws.

export const HEIGHT_MIN = 160;
export const HEIGHT_MAX = 640;
export const HEIGHT_DEFAULT = 320;

export const cardHeight = (h: unknown) => (typeof h === "number" && Number.isFinite(h) ? Math.min(HEIGHT_MAX, Math.max(HEIGHT_MIN, Math.round(h))) : HEIGHT_DEFAULT);

/** A running card's frame: as tall as what it draws, at least FRAME_MIN, at most the component's height (cardHeight:
 *  what it gives, else the schema's default); taller content scrolls inside the frame. */
export const FRAME_MIN = 44;
export const frameHeight = (content: number, most: number) => Math.min(most, Math.max(FRAME_MIN, Math.ceil(content)));
/** What the frame may say its content measures: more is not believed. */
export const CONTENT_MOST = 100_000;
