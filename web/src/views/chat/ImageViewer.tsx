// Full-screen viewer for a conversation's images (iOS ImageViewer.swift): ← / → (or a swipe) through all of them,
// n / m on top; the wheel, a trackpad pinch or two fingers zoom 1–6× at the pointer, a drag moves a zoomed picture,
// a double click / tap zooms in there and back, + / - / 0 from the keyboard; "Edit" (box + pen, then into the composer
// for the next message), "Copy" where the browser allows it, "Download", "Open in new tab". Esc or a click beside the picture
// closes it (not while zoomed).
import { Suspense, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { imageURL } from "../../lib/api";
import { useCloseOnBack } from "../../lib/back-close";
import { sendDraft, type DraftImage } from "../../lib/drafts";
import type { EditedImage } from "../../lib/image-edit";
import { clampView, doubleTapView, fingers, fitRect, IDENTITY, isZoomed, panView, wheelFactor, zoomAt, type Point, type Rect, type Size, type View } from "../../lib/image-zoom";
import type { Item } from "../../lib/types";
import { cx, EASE, toast } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { NARROW, useMedia } from "../../ui/media-query";
import { useModalFocus } from "../../ui/overlay";
import { lazyPage } from "../../lib/lazy-page";
import { useChatActions } from "./chat-actions";
import { t } from "../../i18n";

// Drawn only while an image is edited: its own chunk, fetched by the idle preload or the first edit (lib/lazy-page.ts).
const ImageEditor = lazyPage(() => import("./ImageEditor").then((m) => m.ImageEditor), { idle: true });

export interface ViewerImage {
  id: string;
  src: string;
  mime?: string;
  /** What it is (a file's name, "Screenshot · MacBook"): on top, beside n / m. */
  label?: string;
  /** The name "Download" saves it under. */
  name?: string;
}

/** A button of the viewer's bottom bar for the picture shown ("Copy path", "Open in Files"). */
export interface ViewerAction {
  key: string;
  icon: IconName;
  label: string;
  run: () => void;
}

function viewerImage(item: Item, mid: string, sid: string): ViewerImage {
  const src = item.image?.data ? `data:${item.image.mime};base64,${item.image.data}` : imageURL(mid, sid, item.id);
  return { id: item.id, src, mime: item.image?.mime };
}

/** The viewer for a picture in the chat: the whole conversation's images when it is among them, else just this one. */
export function ChatImageViewer({ item, mid, sid, onClose }: { item: Item; mid: string; sid: string; onClose: () => void }) {
  const chat = useChatActions();
  const [{ images, index }] = useState(() => {
    const all = (chat?.images() ?? []).filter((i) => i.kind === "image");
    const at = all.findIndex((i) => i.id === item.id);
    return at >= 0 ? { images: all.map((i) => viewerImage(i, mid, sid)), index: at } : { images: [viewerImage(item, mid, sid)], index: 0 };
  });
  const attach = chat?.canCompose
    ? (img: DraftImage, edited?: EditedImage) => {
        if (!sendDraft(chat.draftKey, { type: "image", image: img, edited })) {
          toast(t("chat-imageviewer.no_composer"), "error");
          return false;
        }
        toast(t("chat-imageviewer.attached"), "ok");
        return true;
      }
    : undefined;
  return <ImageViewer images={images} start={index} onClose={onClose} attach={attach} />;
}

const canCopyImage = () => typeof window !== "undefined" && window.isSecureContext && "ClipboardItem" in window && Boolean(navigator.clipboard?.write);

export function ImageViewer({
  images,
  start,
  onClose,
  attach,
  actions,
}: {
  images: ViewerImage[];
  start: number;
  onClose: () => void;
  /** Where an edited picture goes (the chat's composer), with its source, edits and history to carry on from there;
   *  none hides "Edit". True when it was taken. */
  attach?: (img: DraftImage, edited?: EditedImage) => boolean;
  /** More buttons for the picture at `index`, after "Download". */
  actions?: (index: number) => ViewerAction[];
}) {
  const [index, setIndex] = useState(() => Math.min(Math.max(start, 0), images.length - 1));
  const [view, setView] = useState<View>(IDENTITY);
  const [editing, setEditing] = useState(false);
  const [failed, setFailed] = useState<Record<string, boolean>>({});
  const box = useRef<HTMLDivElement>(null);
  const swipe = useRef<{ x: number; y: number } | null>(null);
  const cur = images[index];
  const many = images.length > 1;
  // Back (a phone's back gesture) closes the viewer, not the chat under it
  useCloseOnBack(true, onClose, useMedia(NARROW));
  // fitted like the app's viewer (scaled to fit, small pictures too): the picture's own size and the room it has
  const area = useRef<HTMLDivElement>(null);
  const [natural, setNatural] = useState<{ width: number; height: number } | null>(null);
  // room: the content box the picture fits in, where it starts (the padding) and the whole area a zoomed one may fill
  const [room, setRoom] = useState<{ width: number; height: number; left: number; top: number; outerWidth: number; outerHeight: number } | null>(null);
  useEffect(() => {
    setNatural(null);
    setView(IDENTITY);
  }, [cur.id]);
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    const ro = new ResizeObserver(() => {
      const cs = getComputedStyle(el);
      const left = parseFloat(cs.paddingLeft),
        top = parseFloat(cs.paddingTop);
      setRoom({ width: el.clientWidth - left - parseFloat(cs.paddingRight), height: el.clientHeight - top - parseFloat(cs.paddingBottom), left, top, outerWidth: el.clientWidth, outerHeight: el.clientHeight });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const fitted = natural && room ? fitRect(natural, room) : null;
  // the fitted picture in the area's coordinates, which the zoom works in (lib/image-zoom.ts)
  const fit = fitted && room && fitted.width > 0 ? { ...fitted, x: fitted.x + room.left, y: fitted.y + room.top } : null;
  const whole = room ? { width: room.outerWidth, height: room.outerHeight } : null;
  // held to the current size: a resize (or a rotated phone) never leaves the picture off an edge
  const shown = fit && whole ? clampView(view, fit, whole) : IDENTITY;
  const zoomed = isZoomed(shown);
  const viewRef = useRef(shown);
  viewRef.current = shown;
  // for the event handlers: the latest layout, not the one of the render that bound them
  const geo = useRef({ fit, whole, natural });
  geo.current = { fit, whole, natural };
  /** A change to the view from the current one (several events can land before a render). */
  const update = (f: (v: View, fit: Rect, whole: Size) => View) => {
    const { fit, whole } = geo.current;
    if (fit && whole) setView((v) => f(clampView(v, fit, whole), fit, whole));
  };
  // pointers on the area: one drags a zoomed picture (or swipes at 1×), two pinch
  const pointers = useRef(new Map<number, Point>());
  const gesture = useRef<{ moved: boolean; last?: Point; pinch?: { dist: number; mid: Point; base: View }; type: string }>({ moved: false, type: "mouse" });
  const lastTap = useRef<{ t: number; x: number; y: number } | null>(null);
  const [dragging, setDragging] = useState(false);

  const go = (d: number) => {
    setView(IDENTITY);
    setIndex((i) => Math.min(Math.max(i + d, 0), images.length - 1));
  };
  /** A zoom step from the keyboard, at the area's middle; 0 is back to fit. */
  const zoomBy = (factor: number) => update((v, fit, whole) => (factor === 0 ? IDENTITY : zoomAt(v, factor, { x: whole.width / 2, y: whole.height / 2 }, fit, whole)));
  const local = (e: { clientX: number; clientY: number }): Point => {
    const r = area.current!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  };
  /** Where a double click / tap zooms: the picture, or anywhere once zoomed; never a button (← / →) on top. */
  const zoomTarget = (target: EventTarget, current: EventTarget) =>
    !(target instanceof Element && target.closest("button")) && (target !== current || isZoomed(viewRef.current));

  // the wheel and a trackpad pinch (ctrl + wheel) zoom at the pointer; React's wheel listener is passive, so it can
  // not keep the page itself from zooming
  useEffect(() => {
    const el = area.current;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      if (!geo.current.fit) return;
      e.preventDefault();
      const p = local(e),
        factor = wheelFactor(e);
      update((v, fit, whole) => zoomAt(v, factor, p, fit, whole));
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (editing) return;
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      } else if (e.key === "ArrowLeft") go(-1);
      else if (e.key === "ArrowRight") go(1);
      else if ((e.key === "+" || e.key === "=") && !e.metaKey && !e.ctrlKey) zoomBy(1.5);
      else if (e.key === "-" && !e.metaKey && !e.ctrlKey) zoomBy(1 / 1.5);
      else if (e.key === "0" && !e.metaKey && !e.ctrlKey) zoomBy(0);
    };
    // capture: before the chat's own Esc (interrupt) and any menu
    addEventListener("keydown", key, true);
    return () => removeEventListener("keydown", key, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing, images.length, onClose]);

  // focus in, Tab kept in, back to what opened it on close; the editor on top (portaled beside the viewer) has its own
  useModalFocus(box);

  const copy = async () => {
    try {
      const blob = await (await fetch(cur.src)).blob();
      // the clipboard takes PNG everywhere; other types are converted first
      const png = blob.type === "image/png" ? blob : await toPNG(blob);
      await navigator.clipboard.write([new ClipboardItem({ "image/png": png })]);
      toast(t("chat-imageviewer.copied"), "ok");
    } catch {
      toast(t("chat-imageviewer.copy_failed"), "error");
    }
  };

  const download = async () => {
    try {
      const response = await fetch(cur.src);
      if (!response.ok) throw new Error(`Image download failed: HTTP ${response.status}`);
      const blob = await response.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = cur.name || `image-${index + 1}.${(blob.type.split("/")[1] || "png").replace("jpeg", "jpg")}`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch {
      toast(t("chat-imageviewer.download_failed"), "error");
    }
  };

  // the original in a new tab; browsers refuse to open a data: address at the top level, so that goes as a blob
  const openOriginal = async () => {
    if (!cur.src.startsWith("data:")) {
      window.open(cur.src, "_blank", "noopener");
      return;
    }
    const url = URL.createObjectURL(await (await fetch(cur.src)).blob());
    window.open(url, "_blank", "noopener");
    setTimeout(() => URL.revokeObjectURL(url), 60_000);
  };

  // the bottom bar; more than three don't fit a phone with their words, so there they are icons
  const off = failed[cur.id];
  const bar: (ViewerAction & { disabled?: boolean })[] = [
    ...(attach ? [{ key: "edit", icon: "pencil" as const, label: t("chat-imageviewer.edit"), run: () => setEditing(true), disabled: off }] : []),
    ...(canCopyImage() ? [{ key: "copy", icon: "copy" as const, label: t("chat-imageviewer.copy"), run: () => void copy(), disabled: off }] : []),
    { key: "download", icon: "download", label: t("chat-imageviewer.download"), run: () => void download(), disabled: off },
    ...(actions?.(index) ?? []),
  ];

  return createPortal(
    <div
      ref={box}
      role="dialog"
      aria-modal="true"
      aria-label={[cur.label || t("chat-imageviewer.image"), many ? `${index + 1} / ${images.length}` : ""].filter(Boolean).join(" ")}
      tabIndex={-1}
      className="fixed inset-0 z-50 flex flex-col bg-black/90 text-white outline-none backdrop-blur-sm"
      style={{ animation: "fade-in 150ms ease-out both" }}
    >
      <div className="flex h-14 shrink-0 items-center gap-2 px-3">
        <RoundButton icon="x" label={t("chat-imageviewer.close")} onClick={onClose} />
        <div className="flex-1" />
        {cur.label && <span className="min-w-0 truncate text-[13px] font-medium">{cur.label}</span>}
        {many && (
          <span className="shrink-0 rounded-full bg-white/15 px-3 py-1 text-[12.5px] font-medium tabular-nums whitespace-nowrap" aria-live="polite">
            {index + 1} / {images.length}
          </span>
        )}
        <div className="flex-1" />
        <RoundButton icon="external" label={t("chat-imageviewer.open_in_new_tab")} onClick={() => void openOriginal()} />
      </div>

      <div
        ref={area}
        className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden px-4 sm:px-16"
        // the viewer takes every touch gesture: a pinch zooms the picture, not the page
        style={{ touchAction: "none" }}
        // beside the picture closes it, unless that ended a drag, the picture is zoomed (a click there is the first
        // half of the double click back to fit) or it is a double click's second click (← / → went away under it
        // on the last picture)
        onClick={(e) => e.target === e.currentTarget && e.detail < 2 && !gesture.current.moved && !zoomed && onClose()}
        onDoubleClick={(e) => {
          // touch has its own double tap below; a browser that also sends dblclick would undo it
          if (gesture.current.type !== "mouse" || !zoomTarget(e.target, e.currentTarget)) return;
          const p = local(e);
          update((v, fit, whole) => doubleTapView(v, p, fit, whole, geo.current.natural));
        }}
        onPointerDown={(e) => {
          // a new first pointer starts a new gesture: nothing left over from a pointer that went up elsewhere
          if (e.isPrimary) pointers.current.clear();
          const p = local(e);
          pointers.current.set(e.pointerId, p);
          const ps = [...pointers.current.values()];
          if (ps.length === 1) {
            gesture.current = { moved: false, last: p, type: e.pointerType };
            if (!zoomed && e.pointerType !== "mouse") swipe.current = { x: e.clientX, y: e.clientY };
          } else if (ps.length === 2) {
            // a second finger: a pinch from here, never a swipe or a tap
            swipe.current = null;
            const { dist, mid } = fingers(ps[0], ps[1]);
            gesture.current = { moved: true, type: e.pointerType, pinch: { dist, mid, base: viewRef.current } };
            e.currentTarget.setPointerCapture(e.pointerId);
          }
        }}
        onPointerMove={(e) => {
          if (!pointers.current.has(e.pointerId)) return;
          const p = local(e);
          pointers.current.set(e.pointerId, p);
          const gs = gesture.current,
            ps = [...pointers.current.values()];
          if (gs.pinch && ps.length >= 2) {
            const { dist, mid } = fingers(ps[0], ps[1]),
              pinch = gs.pinch;
            if (pinch.dist <= 0) return;
            // from the view the pinch started with, so it does not drift however many moves arrive
            update((_, fit, whole) => panView(zoomAt(pinch.base, dist / pinch.dist, pinch.mid, fit, whole), mid.x - pinch.mid.x, mid.y - pinch.mid.y, fit, whole));
            return;
          }
          const last = gs.last ?? p;
          if (!gs.moved && Math.hypot(p.x - last.x, p.y - last.y) < 4) return; // a click, not yet a drag
          // capture only once it is a drag: a captured click would land on the area and close the viewer
          if (!gs.moved && zoomed) {
            e.currentTarget.setPointerCapture(e.pointerId);
            setDragging(true);
          }
          gs.moved = true;
          gs.last = p;
          if (zoomed) update((v, fit, whole) => panView(v, p.x - last.x, p.y - last.y, fit, whole));
        }}
        onPointerUp={(e) => {
          if (!pointers.current.delete(e.pointerId)) return;
          const gs = gesture.current;
          const rest = [...pointers.current.values()];
          if (gs.pinch) {
            // one finger left after a pinch drags from where it is, without a jump
            if (rest.length === 1) gesture.current = { moved: true, last: rest[0], type: gs.type };
            else if (rest.length === 0) gs.pinch = undefined;
            return;
          }
          setDragging(false);
          const s = swipe.current;
          swipe.current = null;
          if (s && !zoomed) {
            const dx = e.clientX - s.x,
              dy = e.clientY - s.y;
            if (Math.abs(dx) > 50 && Math.abs(dy) < 80) return go(dx < 0 ? 1 : -1);
          }
          // a touch double tap on the picture (or anywhere on a zoomed one): zoom in there, or back to fit
          if (gs.moved || e.pointerType === "mouse") return;
          const p = local(e),
            prev = lastTap.current;
          if (prev && e.timeStamp - prev.t < 300 && Math.hypot(p.x - prev.x, p.y - prev.y) < 30 && zoomTarget(e.target, e.currentTarget)) {
            lastTap.current = null;
            update((v, fit, whole) => doubleTapView(v, p, fit, whole, geo.current.natural));
          } else lastTap.current = { t: e.timeStamp, x: p.x, y: p.y };
        }}
        onPointerCancel={(e) => {
          pointers.current.delete(e.pointerId);
          swipe.current = null;
          setDragging(false);
          if (pointers.current.size === 0) gesture.current.pinch = undefined;
        }}
      >
        {failed[cur.id] ? (
          <span className="text-[13px] text-white/70">{t("chat-imageviewer.unreadable")}</span>
        ) : (
          <img
            key={cur.id}
            src={cur.src}
            alt=""
            draggable={false}
            onLoad={(e) => setNatural({ width: e.currentTarget.naturalWidth, height: e.currentTarget.naturalHeight })}
            onError={() => setFailed((f) => ({ ...f, [cur.id]: true }))}
            title={zoomed ? t("chat-imageviewer.zoomed_hint") : t("chat-imageviewer.zoom_hint")}
            className={cx("select-none rounded-[8px] object-contain", fit ? "absolute max-w-none" : "max-h-full max-w-full", !zoomed && "shadow-overlay", zoomed ? (dragging ? "cursor-grabbing" : "cursor-grab") : "cursor-zoom-in")}
            // fitted at rest; zoomed with translate / scale (not transform, which the pop-in animation owns)
            style={{
              animation: `pop-in 220ms ${EASE} both`,
              ...(fit
                ? { left: fit.x, top: fit.y, width: fit.width, height: fit.height, transformOrigin: "0 0", translate: `${shown.x}px ${shown.y}px`, scale: `${shown.scale}` }
                : {}),
            }}
          />
        )}
        {many && !zoomed && (
          <>
            <SideButton side="left" disabled={index === 0} onClick={() => go(-1)} />
            <SideButton side="right" disabled={index === images.length - 1} onClick={() => go(1)} />
          </>
        )}
      </div>

      <div className="flex shrink-0 items-center justify-center gap-2 px-4 pt-2 pb-[max(12px,env(safe-area-inset-bottom))]">
        {bar.map((b) => (
          <BarButton key={b.key} icon={b.icon} label={b.label} onClick={b.run} disabled={b.disabled} compact={bar.length > 3} />
        ))}
      </div>

      {editing && attach && (
        <Suspense fallback={null}>
          <ImageEditor
            src={cur.src}
            doneTitle={t("chat-imageviewer.add_to_composer")}
            onCancel={() => setEditing(false)}
            onDone={(img, state) => {
              setEditing(false);
              if (attach(img, { source: cur.src, ...state })) onClose();
            }}
          />
        </Suspense>
      )}
    </div>,
    document.body,
  );
}

async function toPNG(blob: Blob): Promise<Blob> {
  const bmp = await createImageBitmap(blob);
  const c = document.createElement("canvas");
  c.width = bmp.width;
  c.height = bmp.height;
  c.getContext("2d")!.drawImage(bmp, 0, 0);
  return new Promise((ok, fail) => c.toBlob((b) => (b ? ok(b) : fail(new Error("png"))), "image/png"));
}

function RoundButton({ icon, label, onClick }: { icon: IconName; label: string; onClick: () => void }) {
  return (
    <button type="button" aria-label={label} title={label} onClick={onClick} className="flex size-9 items-center justify-center rounded-full bg-white/15 transition-colors hover:bg-white/25">
      <Icon name={icon} size={15} />
    </button>
  );
}

function SideButton({ side, disabled, onClick }: { side: "left" | "right"; disabled: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={side === "left" ? t("chat-imageviewer.previous") : t("chat-imageviewer.next")}
      title={side === "left" ? t("chat-imageviewer.previous") : t("chat-imageviewer.next")}
      disabled={disabled}
      onClick={onClick}
      className={cx(
        "absolute top-1/2 hidden size-10 -translate-y-1/2 items-center justify-center rounded-full bg-white/15 transition-[background-color,opacity] hover:bg-white/25 disabled:pointer-events-none disabled:opacity-0 sm:flex",
        side === "left" ? "left-3" : "right-3",
      )}
    >
      <Icon name={side === "left" ? "chevronLeft" : "chevronRight"} size={18} stroke={2.2} />
    </button>
  );
}

/** `compact`: just its icon on a phone (its words for screen readers and as its tooltip). */
function BarButton({ icon, label, onClick, disabled, compact }: { icon: IconName; label: string; onClick: () => void; disabled?: boolean; compact?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={compact ? label : undefined}
      className={cx(
        "flex h-9 items-center gap-1.5 rounded-full bg-white/15 px-4 text-[13px] font-medium whitespace-nowrap transition-[background-color,transform] hover:bg-white/25 active:scale-[0.97] disabled:opacity-40",
        compact && "max-sm:size-11 max-sm:justify-center max-sm:px-0",
      )}
    >
      <Icon name={icon} size={14} />
      <span className={compact ? "max-sm:sr-only" : undefined}>{label}</span>
    </button>
  );
}
