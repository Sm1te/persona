// Zoom and pan for the image viewer (views/chat/ImageViewer.tsx; like Android's ImageViewer: 1–6×, double tap zooms
// in at the finger). Pure: the viewer keeps a View and draws the fitted picture moved by (x, y) and scaled by
// `scale` from its top-left corner. Every point is in the area's own coordinates (its top-left is 0, 0).

/** How the fitted picture is shown: scaled from its top-left corner, then moved. 1× at rest is IDENTITY. */
export interface View {
  scale: number;
  x: number;
  y: number;
}

export interface Size {
  width: number;
  height: number;
}
export interface Point {
  x: number;
  y: number;
}
/** Where the picture sits at 1× (fitted and centred) in the area. */
export interface Rect extends Size, Point {}

export const IDENTITY: View = { scale: 1, x: 0, y: 0 };
/** As far as a picture zooms (Android's 6×). */
export const ZOOM_MAX = 6;
/** A double tap on a picture that is not much bigger than its fit (Android's 2.5×). */
const DOUBLE_TAP_SCALE = 2.5;

/** `size` scaled to fit inside `box`, centered in it (the image editor's layout too, lib/image-edit.ts). */
export function fitIn(size: Size, box: Rect): Rect {
  if (size.width <= 0 || size.height <= 0 || box.width <= 0 || box.height <= 0) return { x: 0, y: 0, width: 0, height: 0 };
  const s = Math.min(box.width / size.width, box.height / size.height);
  const width = size.width * s,
    height = size.height * s;
  return { x: box.x + (box.width - width) / 2, y: box.y + (box.height - height) / 2, width, height };
}

/** The image scaled to fit inside a box at the origin, centered (the image viewer's picture). */
export function fitRect(image: Size, box: Size): Rect {
  return fitIn(image, { x: 0, y: 0, width: box.width, height: box.height });
}

/** Zoomed in at all (past rounding): drags pan, swipes do not page, a click beside it does not close. */
export function isZoomed(v: View): boolean {
  return v.scale > 1.001;
}

/** Two fingers: how far apart and their middle, for a pinch. */
export function fingers(a: Point, b: Point): { dist: number; mid: Point } {
  return { dist: Math.hypot(a.x - b.x, a.y - b.y), mid: { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } };
}

/** One side's offset: a shown length that fits the area is centred, a longer one covers it edge to edge. */
function clampAxis(offset: number, start: number, length: number, room: number): number {
  if (length <= room) return (room - length) / 2 - start + 0; // + 0: no -0 at rest
  return Math.min(Math.max(offset, room - length - start), -start) + 0;
}

/** The view held to the rules: scale 1–6×, the picture centred where it fits and never pulled off an edge. */
export function clampView(v: View, fit: Rect, area: Size): View {
  const scale = Math.min(Math.max(v.scale, 1), ZOOM_MAX);
  return {
    scale,
    x: clampAxis(v.x, fit.x, fit.width * scale, area.width),
    y: clampAxis(v.y, fit.y, fit.height * scale, area.height),
  };
}

/** Zoom by `factor` keeping the picture's point under `p` (the pointer, the fingers' middle) where it is. */
export function zoomAt(v: View, factor: number, p: Point, fit: Rect, area: Size): View {
  const scale = Math.min(Math.max(v.scale * factor, 1), ZOOM_MAX);
  // the picture's point under p, in its fitted pixels
  const px = (p.x - fit.x - v.x) / v.scale,
    py = (p.y - fit.y - v.y) / v.scale;
  return clampView({ scale, x: p.x - fit.x - px * scale, y: p.y - fit.y - py * scale }, fit, area);
}

/** Drag a zoomed picture; at 1× it stays put. */
export function panView(v: View, dx: number, dy: number, fit: Rect, area: Size): View {
  return clampView({ scale: v.scale, x: v.x + dx, y: v.y + dy }, fit, area);
}

/** Double click / tap: zoomed out → in at the point, 2.5× or the picture's real size when that is more (what the
 *  viewer's double click showed before it zoomed; at most 6×); zoomed → back to fit. */
export function doubleTapView(v: View, p: Point, fit: Rect, area: Size, natural?: Size | null): View {
  if (isZoomed(v)) return clampView(IDENTITY, fit, area);
  const real = natural && fit.width > 0 ? natural.width / fit.width : 0;
  return zoomAt(v, Math.max(real, DOUBLE_TAP_SCALE), p, fit, area);
}

/** A wheel step as a zoom factor: up zooms in. A trackpad pinch arrives as ctrl + small pixel deltas, so it counts more per pixel. */
export function wheelFactor(e: { deltaY: number; deltaMode: number; ctrlKey: boolean }): number {
  const px = e.deltaY * (e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? 400 : 1);
  return Math.exp(-px * (e.ctrlKey ? 0.01 : 0.002));
}
