// The image editor (iOS ImageEditor.swift, #4963 / #4991), over the picture on black: Box (the tool it opens on, as it
// always has) and Pen in six inks and three widths, Crop (free or a fixed shape, Rotate a quarter left, Reset), and Undo
// / Redo over all of it. The source is never changed: Done hands back the picture drawn from the edits at the source's
// own resolution, and the edits with their history (lib/image-edit.ts), so one handed in again carries on where it
// stopped.
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { imagePayload, MAX_IMAGE } from "../../lib/attachments";
import type { DraftImage } from "../../lib/drafts";
import {
  boxRect,
  CROP_SHAPES,
  dragCrop,
  handleAt,
  handleCursor,
  IMAGE_EDIT,
  INKS,
  isEmpty,
  isFramedAsIs,
  keepMark,
  layout,
  markWidth,
  noEdits,
  noHistory,
  record,
  redo,
  renderPlan,
  resetFrame,
  reshape,
  sameEdits,
  screenToPixel,
  shapeRatio,
  turnLeft,
  turnMatrix,
  turnedSize,
  undo,
  unturnPoint,
  type CropHandle,
  type CropShape,
  type EditState,
  type ImageEdits,
  type Ink,
  type Layout,
  type Mark,
  type Point,
  type Rect,
} from "../../lib/image-edit";
import { cx, Menu, Spinner, toast, type MenuItem } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { useModalFocus } from "../../ui/overlay";
import { t } from "../../i18n";

type Tool = "box" | "pen" | "crop";

const INK_NAME: Record<Ink, string> = {
  red: t("chat-imageeditor.color_red"),
  yellow: t("chat-imageeditor.color_yellow"),
  green: t("chat-imageeditor.color_green"),
  blue: t("chat-imageeditor.color_blue"),
  white: t("chat-imageeditor.color_white"),
  black: t("chat-imageeditor.color_black"),
};
const WIDTH_NAME = [t("chat-imageeditor.width_thin"), t("chat-imageeditor.width_medium"), t("chat-imageeditor.width_thick")];
const SHAPE_NAME: Record<CropShape, string> = {
  free: t("chat-imageeditor.shape_free"),
  original: t("chat-imageeditor.shape_original"),
  square: t("chat-imageeditor.shape_square"),
  "4:3": "4:3",
  "3:4": "3:4",
  "16:9": "16:9",
  "9:16": "9:16",
};

/** The inks' colors (`--mark-*`, the same in light and dark), read once for a drawing rather than once a mark. */
function inkColors(): Record<Ink, string> {
  const css = getComputedStyle(document.documentElement);
  return Object.fromEntries(INKS.map((ink) => [ink, css.getPropertyValue(`--mark-${ink}`).trim() || ink])) as Record<Ink, string>;
}

/** A mark's line, in whatever coordinates the context is set to (the source's pixels). */
function strokeMark(ctx: CanvasRenderingContext2D, m: Mark, colors: Record<Ink, string>) {
  const pts = m.points;
  if (pts.length < 2) return;
  ctx.strokeStyle = colors[m.ink];
  ctx.lineWidth = m.width;
  ctx.beginPath();
  if (m.tool === "box") {
    const r = boxRect(pts[0], pts[pts.length - 1]);
    ctx.rect(r.x, r.y, r.width, r.height);
  } else {
    ctx.moveTo(pts[0].x, pts[0].y);
    for (const p of pts.slice(1)) ctx.lineTo(p.x, p.y);
  }
  ctx.stroke();
}

/** The crop's frame while cropping: the rest dimmed, a line round the crop, its corners marked, the thirds while it is
 *  dragged. */
function drawCropFrame(ctx: CanvasRenderingContext2D, area: { width: number; height: number }, f: Rect, dragging: boolean) {
  ctx.fillStyle = `rgb(0 0 0 / ${IMAGE_EDIT.cropDim})`;
  ctx.beginPath();
  ctx.rect(0, 0, area.width, area.height);
  ctx.rect(f.x, f.y, f.width, f.height);
  ctx.fill("evenodd");
  ctx.strokeStyle = "#fff";
  ctx.lineWidth = IMAGE_EDIT.cropLine;
  ctx.strokeRect(f.x, f.y, f.width, f.height);
  if (dragging) {
    ctx.strokeStyle = `rgb(255 255 255 / ${IMAGE_EDIT.cropThirdsOpacity})`;
    ctx.beginPath();
    for (const i of [1, 2]) {
      const x = f.x + (f.width * i) / 3,
        y = f.y + (f.height * i) / 3;
      ctx.moveTo(x, f.y);
      ctx.lineTo(x, f.y + f.height);
      ctx.moveTo(f.x, y);
      ctx.lineTo(f.x + f.width, y);
    }
    ctx.stroke();
  }
  const c = Math.min(IMAGE_EDIT.cropCorner, f.width / 2, f.height / 2);
  ctx.strokeStyle = "#fff";
  ctx.lineWidth = IMAGE_EDIT.cropCornerWidth;
  ctx.lineCap = "square";
  ctx.beginPath();
  for (const [x, y, dx, dy] of [
    [f.x, f.y, c, c],
    [f.x + f.width, f.y, -c, c],
    [f.x, f.y + f.height, c, -c],
    [f.x + f.width, f.y + f.height, -c, -c],
  ]) {
    ctx.moveTo(x + dx, y);
    ctx.lineTo(x, y);
    ctx.lineTo(x, y + dy);
  }
  ctx.stroke();
}

/** Whether a row's buttons fit with their words (iOS ViewThatFits): measured with them, and with them again once the
 *  row is as wide as it needed. */
function useFitsWords(ref: React.RefObject<HTMLElement | null>, key: unknown): boolean {
  const [words, setWords] = useState(true);
  const needed = useRef(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const check = () => {
      if (words) {
        if (el.scrollWidth > el.clientWidth + 0.5) {
          needed.current = el.scrollWidth;
          setWords(false);
        }
      } else if (el.clientWidth >= needed.current) setWords(true);
    };
    check();
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref, words, key]);
  return words;
}

export function ImageEditor({
  src,
  start,
  doneTitle = t("chat-imageeditor.done"),
  onDone,
  onCancel,
}: {
  /** The picture as it came (never changed). */
  src: string;
  /** Where an earlier edit of `src` stopped: its edits and their history, carried on. */
  start?: EditState;
  doneTitle?: string;
  onDone: (img: DraftImage, state: EditState) => void;
  onCancel: () => void;
}) {
  const [img, setImg] = useState<HTMLImageElement | null>(null);
  const [tool, setTool] = useState<Tool>("box");
  const [ink, setInk] = useState<Ink>("red");
  const [lineWidth, setLineWidth] = useState<number>(IMAGE_EDIT.defaultLineWidth);
  const [doc, setDoc] = useState<EditState>(() => start ?? { edits: noEdits(), history: noHistory() });
  const [current, setCurrent] = useState<Mark | null>(null);
  const [cropping, setCropping] = useState<{ handle: CropHandle; from: Rect; at: Point } | null>(null);
  const [cursor, setCursor] = useState("default");
  const [menu, setMenu] = useState<{ kind: "width" | "shape"; anchor: HTMLElement } | null>(null);
  const [size, setSize] = useState({ width: 0, height: 0 });
  const [busy, setBusy] = useState(false);
  const area = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const toolsRow = useRef<HTMLDivElement>(null);
  const cropRow = useRef<HTMLDivElement>(null);
  const toolWords = useFitsWords(toolsRow, tool);
  const cropWords = useFitsWords(cropRow, `${tool}${doc.edits.shape}`);
  // focus in, Tab kept in, back to the button that opened it (the viewer's "Edit", the composer's) on close
  useModalFocus(root);
  const { edits, history } = doc;

  useEffect(() => {
    let live = true;
    const el = new Image();
    el.src = src;
    el.decode().then(
      () => live && setImg(el),
      () => {
        if (!live) return;
        toast(t("chat-imageeditor.unreadable"), "error");
        onCancel();
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [src]);

  useLayoutEffect(() => {
    const el = area.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setSize({ width: el.clientWidth, height: el.clientHeight }));
    ro.observe(el);
    setSize({ width: el.clientWidth, height: el.clientHeight });
    return () => ro.disconnect();
  }, []);

  const natural = img ? { width: img.naturalWidth, height: img.naturalHeight } : { width: 0, height: 0 };
  const l: Layout = layout(edits, natural, size, tool === "crop");

  // the picture, turned and cropped, the marks over it and, cropping, the frame; at the screen's pixel density
  useEffect(() => {
    const c = canvas.current;
    if (!c || !img || !size.width) return;
    const dpr = devicePixelRatio || 1;
    c.width = Math.round(size.width * dpr);
    c.height = Math.round(size.height * dpr);
    const ctx = c.getContext("2d");
    if (!ctx) return; // no 2D canvas (lost, or none at all): nothing to draw on
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, size.width, size.height);
    ctx.save();
    ctx.beginPath();
    ctx.rect(l.visible.x, l.visible.y, l.visible.width, l.visible.height);
    ctx.clip();
    ctx.translate(l.picture.x, l.picture.y);
    ctx.scale(l.scale, l.scale);
    ctx.transform(...turnMatrix(edits, natural));
    ctx.drawImage(img, 0, 0, natural.width, natural.height);
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    const colors = inkColors();
    for (const m of current ? [...edits.marks, current] : edits.marks) strokeMark(ctx, m, colors);
    ctx.restore();
    if (tool === "crop") drawCropFrame(ctx, size, l.crop, cropping !== null);
  });

  /** One step of the history: the edits changed by `make`, the ones before kept for Undo. */
  const change = (make: (e: ImageEdits) => ImageEdits) =>
    setDoc((d) => {
      const next = make(d.edits);
      return sameEdits(next, d.edits) ? d : { edits: next, history: record(d.history, d.edits) };
    });
  const stepBack = () =>
    setDoc((d) => {
      const s = undo(d.history, d.edits);
      return s ? { edits: s.edits, history: s.history } : d;
    });
  const stepForward = () =>
    setDoc((d) => {
      const s = redo(d.history, d.edits);
      return s ? { edits: s.edits, history: s.history } : d;
    });
  const dragging = current !== null || cropping !== null;

  // read by the key handler, registered once
  const keys = useRef({ menu: false, dragging: false, stepBack, stepForward, onCancel });
  keys.current = { menu: menu !== null, dragging, stepBack, stepForward, onCancel };
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const k = keys.current;
      if (k.menu) return; // an open menu takes its own keys (Esc closes it, not the editor)
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        k.onCancel();
      } else if ((e.metaKey || e.ctrlKey) && (e.key.toLowerCase() === "z" || (e.ctrlKey && !e.metaKey && e.key.toLowerCase() === "y"))) {
        e.preventDefault();
        e.stopPropagation();
        if (k.dragging) return;
        if (e.key.toLowerCase() === "y" || e.shiftKey) k.stepForward();
        else k.stepBack();
      }
    };
    addEventListener("keydown", key, true);
    return () => removeEventListener("keydown", key, true);
  }, []);

  const local = (e: React.PointerEvent): Point => {
    const r = canvas.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };

  const onPointerDown = (e: React.PointerEvent<HTMLCanvasElement>) => {
    if (!img || e.button !== 0 || busy) return;
    const p = local(e);
    if (tool === "crop") {
      const handle = handleAt(p, l.crop);
      if (!handle) return;
      e.currentTarget.setPointerCapture(e.pointerId);
      setCropping({ handle, from: edits.crop, at: p });
      return;
    }
    e.currentTarget.setPointerCapture(e.pointerId);
    const width = markWidth(IMAGE_EDIT.lineWidths[lineWidth], l.scale);
    setCurrent({ tool, ink, width, points: [unturnPoint(edits, screenToPixel(l, p), natural)] });
  };

  const onPointerMove = (e: React.PointerEvent<HTMLCanvasElement>) => {
    const p = local(e);
    if (cropping) {
      if (l.picture.width <= 0 || l.picture.height <= 0) return;
      const delta = { x: (p.x - cropping.at.x) / l.picture.width, y: (p.y - cropping.at.y) / l.picture.height };
      const minimum = { width: IMAGE_EDIT.cropMinimum / l.picture.width, height: IMAGE_EDIT.cropMinimum / l.picture.height };
      const r = shapeRatio(edits.shape, turnedSize(edits, natural));
      const crop = dragCrop(cropping.from, cropping.handle, delta, minimum, r === null ? null : (r * l.picture.height) / l.picture.width);
      setDoc((d) => ({ ...d, edits: { ...d.edits, crop } }));
      return;
    }
    if (current) {
      const q = unturnPoint(edits, screenToPixel(l, p), natural);
      setCurrent((c) => (c ? { ...c, points: c.tool === "box" ? [c.points[0], q] : [...c.points, q] } : c));
      return;
    }
    if (tool === "crop" && e.pointerType === "mouse") setCursor(handleCursor(handleAt(p, l.crop)));
  };

  const onPointerUp = () => {
    if (cropping) {
      const { from } = cropping;
      setCropping(null);
      // the crop let go is one step: back to where it was before this drag
      setDoc((d) => (sameEdits({ ...d.edits, crop: from }, d.edits) ? d : { ...d, history: record(d.history, { ...d.edits, crop: from }) }));
      return;
    }
    if (current && keepMark(current)) {
      const m = current;
      change((e) => ({ ...e, marks: [...e.marks, m] }));
    }
    setCurrent(null);
  };

  const onPointerCancel = () => {
    if (cropping) {
      const { from } = cropping;
      setDoc((d) => ({ ...d, edits: { ...d.edits, crop: from } }));
      setCropping(null);
    }
    setCurrent(null);
  };

  const finish = async () => {
    if (!img || busy) return;
    const state: EditState = { edits, history };
    setBusy(true);
    try {
      if (isEmpty(edits)) {
        // nothing done (or all of it undone): the picture as it is
        const blob = await (await fetch(src)).blob();
        onDone({ name: "image.png", ...(await imagePayload(new File([blob], "image", { type: blob.type || "image/png" }))) }, state);
        return;
      }
      const plan = renderPlan(edits, natural);
      if (!plan) throw new Error("empty crop");
      const out = document.createElement("canvas");
      out.width = plan.width;
      out.height = plan.height;
      const ctx = out.getContext("2d");
      if (!ctx) throw new Error("no 2D canvas");
      // a transparent picture sent as JPEG would go black where it is clear: white under it, as iOS and Android do
      const colors = inkColors();
      ctx.fillStyle = colors.white;
      ctx.fillRect(0, 0, plan.width, plan.height);
      ctx.setTransform(...plan.matrix);
      ctx.imageSmoothingQuality = "high";
      ctx.drawImage(img, 0, 0, natural.width, natural.height);
      ctx.lineCap = "round";
      ctx.lineJoin = "round";
      for (const m of edits.marks) strokeMark(ctx, m, colors);
      const blob = (await encode(out, "image/png")) ?? null;
      // a big screenshot as PNG can pass the 8 MB a message takes: JPEG then
      const fitting = blob && blob.size <= MAX_IMAGE ? blob : await encode(out, "image/jpeg", 0.9);
      if (!fitting || fitting.size > MAX_IMAGE) {
        toast(t("chat-imageeditor.too_large"), "error");
        return;
      }
      const name = fitting.type === "image/jpeg" ? "image.jpg" : "image.png";
      onDone({ name, ...(await imagePayload(new File([fitting], name, { type: fitting.type }))) }, state);
    } catch {
      toast(t("chat-imageeditor.save_failed"), "error");
    } finally {
      setBusy(false);
    }
  };

  const off = !img || busy;
  const menuItems: MenuItem[] =
    menu?.kind === "width"
      ? IMAGE_EDIT.lineWidths.map((_, i) => ({ key: `w${i}`, label: WIDTH_NAME[i], lead: <LineSample width={i} />, radio: true, checked: lineWidth === i, onSelect: () => setLineWidth(i) }))
      : CROP_SHAPES.map((s) => ({ key: s, label: SHAPE_NAME[s], radio: true, checked: edits.shape === s, onSelect: () => change((e) => reshape(e, s, natural)) }));

  return createPortal(
    // dark whatever the page's appearance, as the image viewer: its menus (put inside it) take the dark look too
    <div ref={root} role="dialog" aria-modal="true" aria-label={t("chat-imageeditor.title")} tabIndex={-1} className="dark fixed inset-0 z-[60] flex flex-col bg-black text-white outline-none" style={{ animation: "fade-in 150ms ease-out both" }}>
      <div className="grid h-14 shrink-0 grid-cols-[1fr_auto_1fr] items-center gap-2 px-3">
        <button type="button" onClick={onCancel} className="h-9 justify-self-start rounded-full px-3 text-[13.5px] whitespace-nowrap text-white/85 transition-colors hover:bg-white/15 hover:text-white">
          {t("common.cancel")}
        </button>
        {/* Undo and Redo up top, as iOS and Photos have them: the rows below stay one line on a phone */}
        <div className="flex items-center gap-1">
          <BarIcon icon="undo" label={t("chat-imageeditor.undo")} disabled={off || !history.past.length} onClick={stepBack} />
          <BarIcon icon="redo" label={t("chat-imageeditor.redo")} disabled={off || !history.future.length} onClick={stepForward} />
        </div>
        <button
          type="button"
          onClick={() => void finish()}
          disabled={off}
          className="flex h-9 min-w-0 items-center gap-1.5 justify-self-end rounded-full bg-white px-4 text-[13.5px] font-semibold whitespace-nowrap text-black transition-[opacity,transform] active:scale-[0.97] disabled:opacity-50"
        >
          {busy && <Spinner />}
          {doneTitle}
        </button>
      </div>
      <div ref={area} className="relative min-h-0 flex-1">
        <canvas
          ref={canvas}
          className={cx("absolute inset-0 size-full", tool !== "crop" && "cursor-crosshair")}
          style={{ touchAction: "none", cursor: tool === "crop" ? (cropping ? handleCursor(cropping.handle) : cursor) : undefined }}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={onPointerCancel}
        />
        {!img && (
          <div className="absolute inset-0 flex items-center justify-center">
            <Spinner />
          </div>
        )}
      </div>
      <div className="@container flex shrink-0 flex-col gap-3 px-4 pt-3 pb-[max(12px,env(safe-area-inset-bottom))]">
        {tool === "crop" ? (
          <div ref={cropRow} className="flex min-w-0 items-center gap-2 overflow-hidden">
            <ToolButton icon="rotateLeft" label={t("chat-imageeditor.rotate")} words={cropWords} disabled={off} onClick={() => change(turnLeft)} />
            <ToolButton
              icon="ratio"
              label={t("chat-imageeditor.aspect_ratio")}
              text={SHAPE_NAME[edits.shape]}
              words={cropWords}
              disabled={off}
              menu={menu?.kind === "shape"}
              onClick={(el) => setMenu((m) => (m ? null : { kind: "shape", anchor: el }))}
            />
            <div className="flex-1" />
            <ToolButton label={t("chat-imageeditor.reset")} words disabled={off || isFramedAsIs(edits)} onClick={() => change(resetFrame)} />
          </div>
        ) : (
          <div className="flex items-center justify-center gap-3">
            {INKS.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={INK_NAME[c]}
                title={INK_NAME[c]}
                aria-pressed={ink === c}
                onClick={() => setInk(c)}
                className={cx("size-7 shrink-0 rounded-full transition-[box-shadow,transform] active:scale-90", ink === c ? "ring-3 ring-white" : "ring-1 ring-white/40")}
                style={{ background: `var(--mark-${c})` }}
              />
            ))}
            <IconAction
              icon={<LineSample width={lineWidth} />}
              label={t("chat-imageeditor.line_width")}
              value={WIDTH_NAME[lineWidth]}
              menu={menu?.kind === "width"}
              disabled={off}
              onClick={(el) => setMenu((m) => (m ? null : { kind: "width", anchor: el }))}
            />
          </div>
        )}
        <div ref={toolsRow} className="flex min-w-0 items-center gap-2 overflow-hidden">
          <ToolButton icon="square" label={t("chat-imageeditor.box")} words={toolWords} active={tool === "box"} onClick={() => setTool("box")} />
          <ToolButton icon="write" label={t("chat-imageeditor.pen")} words={toolWords} active={tool === "pen"} onClick={() => setTool("pen")} />
          <ToolButton icon="crop" label={t("chat-imageeditor.crop")} words={toolWords} active={tool === "crop"} disabled={!img} onClick={() => setTool("crop")} />
          <div className="flex-1" />
          {tool !== "crop" && <IconAction icon="trash" label={t("chat-imageeditor.clear")} disabled={off || !edits.marks.length} onClick={() => change((e) => ({ ...e, marks: [] }))} />}
        </div>
      </div>
      {menu && (
        <Menu
          items={menuItems}
          anchor={menu.anchor}
          container={root.current}
          side="top"
          align={menu.kind === "width" ? "right" : "left"}
          width={180}
          header={menu.kind === "width" ? t("chat-imageeditor.line_width") : t("chat-imageeditor.aspect_ratio")}
          onClose={() => setMenu(null)}
        />
      )}
    </div>,
    document.body,
  );
}

/** A control's name with the value it shows, in the language's own form ("Line width: Medium", "粗细：中"). */
function named(label: string, value: string): string {
  return t("chat-imageeditor.named_value", { label, value });
}

function encode(c: HTMLCanvasElement, type: string, quality?: number): Promise<Blob | null> {
  return new Promise((ok) => c.toBlob(ok, type, quality));
}

/** A tool, or an option of Crop: an icon and its word on one line (the icon alone when the row has no room for the
 *  words; `label` is then what a screen reader and the tooltip say). `text` stands in the word's place (the shape
 *  chosen). Pressed for the tool shown; a menu's button when `menu` is set. */
function ToolButton({
  icon,
  label,
  text,
  words,
  active,
  disabled,
  menu,
  onClick,
}: {
  icon?: IconName;
  label: string;
  text?: string;
  words: boolean;
  active?: boolean;
  disabled?: boolean;
  menu?: boolean;
  onClick: (el: HTMLElement) => void;
}) {
  const shown = text ?? label;
  // icon-only, or showing its value: named in full ("Aspect ratio: Freeform"), aloud and in the tooltip
  const said = text ? named(label, text) : label;
  return (
    <button
      type="button"
      aria-label={text || !words ? said : undefined}
      title={text || !words ? said : undefined}
      aria-pressed={active}
      aria-haspopup={menu === undefined ? undefined : "menu"}
      aria-expanded={menu === undefined ? undefined : menu}
      disabled={disabled}
      onClick={(e) => onClick(e.currentTarget)}
      className={cx(
        "flex h-9 shrink-0 items-center gap-1.5 rounded-full text-[13px] font-medium whitespace-nowrap transition-[background-color,color,opacity] disabled:opacity-40",
        words || !icon ? "px-4" : "w-9 justify-center",
        active ? "bg-white text-black" : "bg-white/15 text-white hover:bg-white/25",
      )}
    >
      {icon && <Icon name={icon} size={14} />}
      {(words || !icon) && <span>{shown}</span>}
    </button>
  );
}

/** A line as wide on screen as the pen draws at width `width` (an index of IMAGE_EDIT.lineWidths): what Line width shows
 *  for the one chosen, and each of its rows. */
function LineSample({ width }: { width: number }) {
  return <span aria-hidden className="block w-4 rounded-full bg-current" style={{ height: IMAGE_EDIT.lineWidths[width] }} />;
}

function IconAction({ icon, label, value, menu, disabled, onClick }: { icon: IconName | React.ReactNode; label: string; value?: string; menu?: boolean; disabled: boolean; onClick: (el: HTMLElement) => void }) {
  const said = value ? named(label, value) : label;
  return (
    <button
      type="button"
      aria-label={said}
      title={said}
      aria-haspopup={menu === undefined ? undefined : "menu"}
      aria-expanded={menu === undefined ? undefined : menu}
      disabled={disabled}
      onClick={(e) => onClick(e.currentTarget)}
      className="flex size-9 shrink-0 items-center justify-center rounded-full bg-white/15 transition-colors hover:bg-white/25 disabled:opacity-40"
    >
      {typeof icon === "string" ? <Icon name={icon} size={15} /> : icon}
    </button>
  );
}

/** Undo and Redo in the top bar: an icon on the bar's black, its name in the tooltip. */
function BarIcon({ icon, label, disabled, onClick }: { icon: IconName; label: string; disabled: boolean; onClick: () => void }) {
  return (
    <button type="button" aria-label={label} title={label} disabled={disabled} onClick={onClick} className="flex size-9 items-center justify-center rounded-full text-white transition-colors hover:bg-white/15 disabled:opacity-35">
      <Icon name={icon} size={17} />
    </button>
  );
}
