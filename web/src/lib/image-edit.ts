// What the image editor (views/chat/ImageEditor.tsx) did to a picture, as iOS keeps it (ImageEdits.swift, #4963):
// quarter turns, the crop as fractions of the turned picture with its shape, and marks in the source's pixels, each
// with its own width. Kept apart from the picture, so a picture edited in the composer opens again with its crop and
// marks as they were, each still undoable, and the source is never written. Pure values and geometry; no DOM here.

import { fitIn, fitRect } from "./image-zoom";

export type MarkTool = "box" | "pen";

export const INKS = ["red", "yellow", "green", "blue", "white", "black"] as const;
export type Ink = (typeof INKS)[number];

export interface Point {
  x: number;
  y: number;
}

export interface Size {
  width: number;
  height: number;
}

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A box or a free line drawn on the picture. */
export interface Mark {
  tool: MarkTool;
  ink: Ink;
  /** The line's width, in the source picture's pixels. */
  width: number;
  /** In the source picture's pixels, before it was turned or cropped; a box is its first and last point. */
  points: Point[];
}

/** A crop's shape: free, the picture's own, or width : height. */
export type CropShape = "free" | "original" | "square" | "4:3" | "3:4" | "16:9" | "9:16";
export const CROP_SHAPES: readonly CropShape[] = ["free", "original", "square", "4:3", "3:4", "16:9", "9:16"];

/** Where a crop is taken by the pointer: a corner, an edge, or inside it (moves it). */
export type CropHandle = "topLeft" | "top" | "topRight" | "left" | "right" | "bottomLeft" | "bottom" | "bottomRight" | "inside";

export interface ImageEdits {
  /** Quarter turns clockwise, 0 to 3. */
  turns: number;
  /** What is kept, in fractions of the picture as turned. */
  crop: Rect;
  /** The crop's shape, kept with it so a turn or an undo brings it back as it was. */
  shape: CropShape;
  marks: Mark[];
}

/** Undo and Redo over whole edits: each finished change (a mark, a crop let go, a turn, a shape, a reset, Clear) is a step. */
export interface EditHistory {
  past: ImageEdits[];
  future: ImageEdits[];
}

/** Where the editor stopped: the edits and their history. */
export interface EditState {
  edits: ImageEdits;
  history: EditHistory;
}

/** A picture as the editor left it, kept beside the picture drawn from it (iOS EditedImage): the source as it came
 *  (a URL; never changed), the edits and their history. The composer holds it until the picture is removed or sent. */
export interface EditedImage extends EditState {
  source: string;
}

/** The editor's measures, in CSS pixels (iOS Theme.ImageEdit). */
export const IMAGE_EDIT = {
  /** A mark's line on screen: thin, medium, thick. Medium is the 3.5 px Box and Pen drew with before there was a
   *  choice, and the default; the line is burned in at the same width relative to the picture as it is seen. */
  lineWidths: [2, 3.5, 7],
  defaultLineWidth: 1,
  /** Room left round the picture while cropping, so the handles on its edges can be taken. */
  cropInset: 28,
  /** The crop frame's line, its corner marks (length and thickness), and how far round an edge the pointer takes it. */
  cropLine: 1,
  cropCorner: 20,
  cropCornerWidth: 3,
  cropGrab: 22,
  /** The smallest crop, a side on screen. */
  cropMinimum: 64,
  /** Outside the crop, dimmed; the thirds drawn inside it while it is dragged. */
  cropDim: 0.6,
  cropThirdsOpacity: 0.45,
} as const;

export const WHOLE: Rect = { x: 0, y: 0, width: 1, height: 1 };

export function noEdits(): ImageEdits {
  return { turns: 0, crop: WHOLE, shape: "free", marks: [] };
}

export function noHistory(): EditHistory {
  return { past: [], future: [] };
}

const sameRect = (a: Rect, b: Rect) => a.x === b.x && a.y === b.y && a.width === b.width && a.height === b.height;

/** The same edits: the same turn, crop and shape, and the very same marks. */
export function sameEdits(a: ImageEdits, b: ImageEdits): boolean {
  return a.turns === b.turns && a.shape === b.shape && sameRect(a.crop, b.crop) && a.marks.length === b.marks.length && a.marks.every((m, i) => m === b.marks[i]);
}

/** Nothing done: the picture goes as it is. */
export function isEmpty(e: ImageEdits): boolean {
  return e.turns === 0 && sameRect(e.crop, WHOLE) && e.marks.length === 0;
}

/** Neither turned nor cropped nor shaped (Reset has nothing to do). */
export function isFramedAsIs(e: ImageEdits): boolean {
  return e.turns === 0 && sameRect(e.crop, WHOLE) && e.shape === "free";
}

// ── turning ────────────────────────────────────────────

/** The picture's size (`size`, its pixels) once turned. */
export function turnedSize(e: ImageEdits, size: Size): Size {
  return e.turns % 2 === 0 ? size : { width: size.height, height: size.width };
}

/** A point of the picture where it is once turned. */
export function turnPoint(e: ImageEdits, p: Point, size: Size): Point {
  switch (e.turns) {
    case 1:
      return { x: size.height - p.y, y: p.x };
    case 2:
      return { x: size.width - p.x, y: size.height - p.y };
    case 3:
      return { x: p.y, y: size.width - p.x };
    default:
      return p;
  }
}

/** A point of the turned picture back where it is in the picture. */
export function unturnPoint(e: ImageEdits, p: Point, size: Size): Point {
  switch (e.turns) {
    case 1:
      return { x: p.y, y: size.height - p.x };
    case 2:
      return { x: size.width - p.x, y: size.height - p.y };
    case 3:
      return { x: size.width - p.y, y: p.x };
    default:
      return p;
  }
}

/** A 2D transform as a canvas takes it (`setTransform(a, b, c, d, e, f)`): x' = a·x + c·y + e, y' = b·x + d·y + f. */
export type Matrix = [a: number, b: number, c: number, d: number, e: number, f: number];

/** What `turnPoint` does, as a transform (top-left coordinates). */
export function turnMatrix(e: ImageEdits, size: Size): Matrix {
  switch (e.turns) {
    case 1:
      return [0, 1, -1, 0, size.height, 0];
    case 2:
      return [-1, 0, 0, -1, size.width, size.height];
    case 3:
      return [0, -1, 1, 0, 0, size.width];
    default:
      return [1, 0, 0, 1, 0, 0];
  }
}

export function applyMatrix(m: Matrix, p: Point): Point {
  return { x: m[0] * p.x + m[2] * p.y + m[4], y: m[1] * p.x + m[3] * p.y + m[5] };
}

/** Turned a quarter to the left (Photos' button), the crop and its shape with the picture. */
export function turnLeft(e: ImageEdits): ImageEdits {
  const c = e.crop;
  return { ...e, turns: (e.turns + 3) % 4, crop: { x: c.y, y: 1 - (c.x + c.width), width: c.height, height: c.width }, shape: turnedShape(e.shape) };
}

/** Back to the picture as it came: not turned, not cropped (the marks stay). */
export function resetFrame(e: ImageEdits): ImageEdits {
  return { ...e, turns: 0, crop: WHOLE, shape: "free" };
}

// ── shapes and the crop ────────────────────────────────

/** Width over height in pixels, for a picture of `size` (as turned); null: any. */
export function shapeRatio(shape: CropShape, size: Size): number | null {
  switch (shape) {
    case "free":
      return null;
    case "original":
      return size.height > 0 ? size.width / size.height : null;
    case "square":
      return 1;
    case "4:3":
      return 4 / 3;
    case "3:4":
      return 3 / 4;
    case "16:9":
      return 16 / 9;
    case "9:16":
      return 9 / 16;
  }
}

/** The same shape once the picture has turned a quarter: 4:3 becomes 3:4. */
export function turnedShape(shape: CropShape): CropShape {
  switch (shape) {
    case "4:3":
      return "3:4";
    case "3:4":
      return "4:3";
    case "16:9":
      return "9:16";
    case "9:16":
      return "16:9";
    default:
      return shape;
  }
}

/** `shape` from now on, the crop made the largest of that shape there is room for round its middle (`size`: the
 *  picture's pixels). Freeform keeps the crop as it is. */
export function reshape(e: ImageEdits, shape: CropShape, size: Size): ImageEdits {
  const turned = turnedSize(e, size);
  const ratio = shapeRatio(shape, turned);
  return { ...e, shape, crop: ratio === null ? e.crop : fitCrop(e.crop, ratio, turned) };
}

/** The largest crop of `ratio` (width / height, pixels) in a picture of `size` (as turned), round `crop`'s middle and
 *  moved inside the picture. */
export function fitCrop(crop: Rect, ratio: number, size: Size): Rect {
  if (size.width <= 0 || size.height <= 0 || ratio <= 0) return crop;
  const k = (ratio * size.height) / size.width; // the ratio in fractions
  let w = 1,
    h = 1 / k;
  if (h > 1) {
    h = 1;
    w = k;
  }
  const midX = crop.x + crop.width / 2,
    midY = crop.y + crop.height / 2;
  return { x: clamp(midX - w / 2, 0, 1 - w), y: clamp(midY - h / 2, 0, 1 - h), width: w, height: h };
}

/** The crop in the turned picture's pixels: whole pixels, inside it. */
export function cropRect(e: ImageEdits, size: Size): Rect {
  const t = turnedSize(e, size);
  const x0 = e.crop.x * t.width,
    y0 = e.crop.y * t.height;
  const x1 = (e.crop.x + e.crop.width) * t.width,
    y1 = (e.crop.y + e.crop.height) * t.height;
  // whole pixels outward (CGRect.integral), with a hair of slack for fractions that land a rounding error off one
  const left = Math.max(0, Math.floor(x0 + 1e-9)),
    top = Math.max(0, Math.floor(y0 + 1e-9));
  const right = Math.min(t.width, Math.ceil(x1 - 1e-9)),
    bottom = Math.min(t.height, Math.ceil(y1 - 1e-9));
  return { x: left, y: top, width: Math.max(0, right - left), height: Math.max(0, bottom - top) };
}

const isLeft = (h: CropHandle) => h === "topLeft" || h === "left" || h === "bottomLeft";
const isRight = (h: CropHandle) => h === "topRight" || h === "right" || h === "bottomRight";
const isUpper = (h: CropHandle) => h === "topLeft" || h === "top" || h === "topRight";
const isLower = (h: CropHandle) => h === "bottomLeft" || h === "bottom" || h === "bottomRight";

/** `start` dragged by `handle` by `delta` (fractions of the turned picture), kept inside it and no smaller than `minimum`
 *  (fractions); with `ratio` (width / height in fractions, not pixels) it keeps that shape, a corner pinned opposite the
 *  one dragged and an edge's middle opposite the edge dragged. */
export function dragCrop(start: Rect, handle: CropHandle, delta: { x: number; y: number }, minimum: Size, ratio: number | null): Rect {
  const minW = Math.min(minimum.width, 1),
    minH = Math.min(minimum.height, 1);
  if (handle === "inside") {
    return { x: clamp(start.x + delta.x, 0, 1 - start.width), y: clamp(start.y + delta.y, 0, 1 - start.height), width: start.width, height: start.height };
  }
  const left = isLeft(handle),
    right = isRight(handle),
    upper = isUpper(handle),
    lower = isLower(handle);
  const maxX0 = start.x + start.width,
    maxY0 = start.y + start.height;
  if (ratio === null || !(ratio > 0)) {
    let minX = start.x,
      maxX = maxX0,
      minY = start.y,
      maxY = maxY0;
    if (left) minX = Math.min(Math.max(minX + delta.x, 0), maxX - minW);
    if (right) maxX = Math.max(Math.min(maxX + delta.x, 1), minX + minW);
    if (upper) minY = Math.min(Math.max(minY + delta.y, 0), maxY - minH);
    if (lower) maxY = Math.max(Math.min(maxY + delta.y, 1), minY + minH);
    return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
  }
  // A fixed shape: the size the pointer asks for, then what fits from what stays put.
  const k = ratio;
  const leastW = Math.min(Math.max(minW, minH * k), 1, k);
  if ((left || right) && (upper || lower)) {
    const anchorX = left ? maxX0 : start.x,
      anchorY = upper ? maxY0 : start.y;
    const askedW = left ? start.width - delta.x : start.width + delta.x;
    const askedH = upper ? start.height - delta.y : start.height + delta.y;
    const roomW = left ? anchorX : 1 - anchorX,
      roomH = upper ? anchorY : 1 - anchorY;
    const w = Math.max(Math.min(Math.max(askedW, askedH * k), roomW, roomH * k), Math.min(leastW, roomW, roomH * k));
    const h = w / k;
    return { x: left ? anchorX - w : anchorX, y: upper ? anchorY - h : anchorY, width: w, height: h };
  }
  if (left || right) {
    const anchorX = left ? maxX0 : start.x;
    const askedW = left ? start.width - delta.x : start.width + delta.x;
    const roomW = left ? anchorX : 1 - anchorX;
    const w = Math.max(Math.min(askedW, roomW, k), Math.min(leastW, roomW));
    const h = w / k;
    const y = clamp(start.y + start.height / 2 - h / 2, 0, 1 - h);
    return { x: left ? anchorX - w : anchorX, y, width: w, height: h };
  }
  const anchorY = upper ? maxY0 : start.y;
  const askedH = upper ? start.height - delta.y : start.height + delta.y;
  const roomH = upper ? anchorY : 1 - anchorY;
  const h = Math.max(Math.min(askedH, roomH, 1 / k), Math.min(leastW / k, roomH));
  const w = h * k;
  const x = clamp(start.x + start.width / 2 - w / 2, 0, 1 - w);
  return { x, y: upper ? anchorY - h : anchorY, width: w, height: h };
}

/** What the pointer at `p` takes of a crop framed at `frame` (screen): a corner or an edge within `reach`, else inside
 *  it; null outside it. */
export function handleAt(p: Point, frame: Rect, reach: number = IMAGE_EDIT.cropGrab): CropHandle | null {
  const maxX = frame.x + frame.width,
    maxY = frame.y + frame.height;
  if (p.x < frame.x - reach || p.x > maxX + reach || p.y < frame.y - reach || p.y > maxY + reach) return null;
  const left = Math.abs(p.x - frame.x) <= reach,
    right = !left && Math.abs(p.x - maxX) <= reach;
  const top = Math.abs(p.y - frame.y) <= reach,
    bottom = !top && Math.abs(p.y - maxY) <= reach;
  if (left && top) return "topLeft";
  if (right && top) return "topRight";
  if (left && bottom) return "bottomLeft";
  if (right && bottom) return "bottomRight";
  if (left) return "left";
  if (right) return "right";
  if (top) return "top";
  if (bottom) return "bottom";
  return p.x >= frame.x && p.x <= maxX && p.y >= frame.y && p.y <= maxY ? "inside" : null;
}

/** The pointer's look over a handle (a desktop has one to show). */
export function handleCursor(h: CropHandle | null): string {
  switch (h) {
    case "topLeft":
    case "bottomRight":
      return "nwse-resize";
    case "topRight":
    case "bottomLeft":
      return "nesw-resize";
    case "top":
    case "bottom":
      return "ns-resize";
    case "left":
    case "right":
      return "ew-resize";
    case "inside":
      return "move";
    default:
      return "default";
  }
}

// ── on screen ──────────────────────────────────────────

// fitIn and fitRect live in lib/image-zoom.ts: the image viewer on the first screen needs them, and importing them from
// here would put this whole module, which only the lazy editor uses, on the first screen with them.
export { fitIn, fitRect } from "./image-zoom";

/** Where things are on screen. Marking, the crop fills the room; cropping, all of the turned picture shows, inset so the
 *  handles on its edges can be taken, the crop's frame over it. */
export interface Layout {
  /** The whole turned picture's frame (past the room where it is cropped away), and screen pixels per picture pixel. */
  picture: Rect;
  scale: number;
  /** What shows of it. */
  visible: Rect;
  /** The crop's frame. */
  crop: Rect;
}

export function layout(e: ImageEdits, size: Size, area: Size, cropping: boolean): Layout {
  const turned = turnedSize(e, size);
  const none: Rect = { x: 0, y: 0, width: 0, height: 0 };
  if (turned.width <= 0 || turned.height <= 0 || area.width <= 0 || area.height <= 0) return { picture: none, scale: 1, visible: none, crop: none };
  if (cropping) {
    const inset = IMAGE_EDIT.cropInset;
    const picture = fitIn(turned, { x: inset, y: inset, width: Math.max(0, area.width - 2 * inset), height: Math.max(0, area.height - 2 * inset) });
    const c = e.crop;
    const crop = { x: picture.x + c.x * picture.width, y: picture.y + c.y * picture.height, width: c.width * picture.width, height: c.height * picture.height };
    return { picture, scale: picture.width / turned.width, visible: picture, crop };
  }
  const kept = cropRect(e, size);
  const shown = fitRect({ width: kept.width, height: kept.height }, area);
  const scale = shown.width / Math.max(kept.width, 1);
  const picture = { x: shown.x - kept.x * scale, y: shown.y - kept.y * scale, width: turned.width * scale, height: turned.height * scale };
  return { picture, scale, visible: shown, crop: shown };
}

/** A point on screen in the turned picture's pixels, kept inside what shows. */
export function screenToPixel(l: Layout, p: Point): Point {
  const q = { x: clamp(p.x, l.visible.x, l.visible.x + l.visible.width), y: clamp(p.y, l.visible.y, l.visible.y + l.visible.height) };
  return { x: (q.x - l.picture.x) / l.scale, y: (q.y - l.picture.y) / l.scale };
}

/** A mark's width in the source's pixels for a line `screenWidth` wide on screen: burned in, it is as wide relative to
 *  the picture as it was seen. */
export function markWidth(screenWidth: number, scale: number): number {
  return screenWidth / Math.max(scale, Number.EPSILON);
}

/** A box mark's rectangle, whichever way it was dragged. */
export function boxRect(a: Point, b: Point): Rect {
  return { x: Math.min(a.x, b.x), y: Math.min(a.y, b.y), width: Math.abs(b.x - a.x), height: Math.abs(b.y - a.y) };
}

/** A mark worth keeping: more than a tap. */
export function keepMark(m: Mark): boolean {
  if (m.points.length < 2) return false;
  if (m.tool === "box") {
    const r = boxRect(m.points[0], m.points[m.points.length - 1]);
    return r.width >= 2 || r.height >= 2;
  }
  return true;
}

// ── drawing the result ─────────────────────────────────

/** The result's size and the transform that takes the source's pixels (and the marks, which are in them) into it:
 *  turned, then cropped. Null when nothing would be left. */
export function renderPlan(e: ImageEdits, size: Size): { width: number; height: number; matrix: Matrix } | null {
  const out = cropRect(e, size);
  if (out.width < 1 || out.height < 1) return null;
  const t = turnMatrix(e, size);
  return { width: out.width, height: out.height, matrix: [t[0], t[1], t[2], t[3], t[4] - out.x, t[5] - out.y] };
}

// ── history ────────────────────────────────────────────

/** A change was made to `before`: a step back to it, and nothing left to redo. */
export function record(h: EditHistory, before: ImageEdits): EditHistory {
  return { past: [...h.past, before], future: [] };
}

/** The edits a step back from `now`, and `now` kept for Redo; null when there is nothing to undo. */
export function undo(h: EditHistory, now: ImageEdits): { history: EditHistory; edits: ImageEdits } | null {
  if (!h.past.length) return null;
  return { history: { past: h.past.slice(0, -1), future: [...h.future, now] }, edits: h.past[h.past.length - 1] };
}

export function redo(h: EditHistory, now: ImageEdits): { history: EditHistory; edits: ImageEdits } | null {
  if (!h.future.length) return null;
  return { history: { past: [...h.past, now], future: h.future.slice(0, -1) }, edits: h.future[h.future.length - 1] };
}

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(Math.max(v, lo), hi);
}
