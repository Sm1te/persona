// A transient user-input navigator at the chat's right edge. Only this small component updates while scrolling;
// cached content offsets are rebuilt on transcript/layout changes, never once per tick on a scroll frame.
import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { activeNavRailOffset, navRailMark, NAV_RAIL_MIN_HEIGHT, planNavRailTicks, type NavRailTick } from "../../lib/nav-rail";
import { useFinePointer } from "../../lib/viewport";
import { NAV_RAIL_METRICS as M } from "../../ui/nav-rail-metrics";
import { cx } from "../../ui/atoms";
import { t } from "../../i18n";

function labelOf(tick: NavRailTick): string {
  return tick.preview || (tick.attachmentsOnly ? t("chat-navrail.attachments", { count: tick.attachmentsOnly }) : tick.ordinal ? t("chat-navrail.input", { index: tick.ordinal }) : "");
}

/** Where the chat's line wrappers (ChatLineView's `[data-line]` boxes) are: the content element's own children, or the
 *  scroller's when it has no content element. A measure reads those children instead of querying the whole transcript
 *  subtree, which grows with every message's markup. */
function lineParent(scroller: HTMLElement): Element {
  for (const el of scroller.children) if (el instanceof HTMLElement && el.dataset.chatContent !== undefined) return el;
  return scroller;
}

export function NavRail({ ticks, total, scroller, onJump, reset }: {
  ticks: NavRailTick[];
  total?: number;
  scroller: HTMLDivElement | null;
  onJump: (id: string) => void | Promise<void>;
  reset: string;
}) {
  const fine = useFinePointer();
  const [height, setHeight] = useState(0);
  const [right, setRight] = useState(0);
  const [visible, setVisible] = useState(false);
  const [active, setActive] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [preview, setPreview] = useState<string | null>(null);
  const [windowCenter, setWindowCenter] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const rail = useRef<HTMLElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const frame = useRef(0);
  const idleTimer = useRef(0);
  const pendingTimer = useRef(0);
  const hovered = useRef(false);
  const focused = useRef(false);
  const drag = useRef<{ pointer: number; ids: string[]; pitch: number; top: number } | null>(null);
  const selected = useRef<string | null>(null);
  const previewOrigin = useRef<"pointer" | "keyboard" | "drag" | null>(null);
  const suppressClick = useRef(false);
  const list = useRef(ticks);
  list.current = ticks;
  const dirty = useRef(true);
  const offsets = useRef<{ index: number; top: number }[]>([]);
  const previewId = useId();

  const wake = useCallback(() => {
    setVisible(true);
    clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => {
      idleTimer.current = 0;
      if (hovered.current || focused.current || drag.current) return;
      setVisible(false);
      setPreview(null);
      previewOrigin.current = null;
      setWindowCenter(null);
    }, M.idleMs);
  }, []);

  const measure = useCallback(() => {
    frame.current = 0;
    if (!scroller || !fine || !list.current.length) return;
    if (dirty.current) {
      dirty.current = false;
      setHeight(Math.max(0, scroller.clientHeight - M.padding * 2));
      setRight(Math.max(0, scroller.offsetWidth - scroller.clientWidth) + M.edgeGap);
      const content = lineParent(scroller);
      // Wide chats already have a margin. Add only the missing clearance, keeping that column centered.
      const margin = Math.max(0, (scroller.clientWidth - content.clientWidth) / 2);
      scroller.style.setProperty("--chat-nav-clearance", `${Math.max(0, M.width + M.edgeGap + M.endInset - margin)}px`);
      const rows = new Map<string, HTMLElement>();
      for (const el of content.children) {
        const child = el.firstElementChild;
        if (el instanceof HTMLElement && el.dataset.line !== undefined && child instanceof HTMLElement) rows.set(el.dataset.line, child);
      }
      const top = scroller.getBoundingClientRect().top;
      offsets.current = list.current.flatMap((tick, index) => {
        const row = rows.get(tick.lineId ?? tick.id);
        return row ? [{ index, top: row.getBoundingClientRect().top - top + scroller.scrollTop }] : [];
      });
    }
    const index = activeNavRailOffset(offsets.current, scroller.scrollTop + M.activeOffset);
    setActive(index === null ? null : list.current[index]?.id ?? null);
  }, [scroller, fine]);

  const schedule = useCallback(() => {
    if (!frame.current) frame.current = requestAnimationFrame(measure);
  }, [measure]);

  useLayoutEffect(() => {
    dirty.current = true;
    schedule();
  }, [ticks, scroller, schedule]);

  useEffect(() => {
    if (!scroller || !fine || !ticks.length) return;
    const content = scroller.querySelector<HTMLElement>("[data-chat-content]") ?? scroller.firstElementChild;
    const changed = () => { dirty.current = true; schedule(); };
    const scroll = () => { wake(); schedule(); };
    scroller.addEventListener("scroll", scroll, { passive: true });
    const resize = new ResizeObserver(changed);
    resize.observe(scroller);
    if (content) resize.observe(content);
    // Disclosure and equal-height replacements can change anchors without changing the outer content height.
    const mutations = new MutationObserver(changed);
    if (content) mutations.observe(content, { childList: true, subtree: true });
    return () => {
      scroller.removeEventListener("scroll", scroll);
      scroller.style.removeProperty("--chat-nav-clearance");
      resize.disconnect();
      mutations.disconnect();
      if (frame.current) cancelAnimationFrame(frame.current);
      frame.current = 0;
    };
  }, [scroller, fine, ticks.length, schedule, wake]);

  useLayoutEffect(() => {
    hovered.current = focused.current = false;
    drag.current = null;
    selected.current = null;
    previewOrigin.current = null;
    suppressClick.current = false;
    clearTimeout(idleTimer.current);
    clearTimeout(pendingTimer.current);
    setActive(null);
    setPending(null);
    setPreview(null);
    setWindowCenter(null);
    setAnnouncement("");
    setVisible(false);
    setHeight(0);
    dirty.current = true;
    schedule();
  }, [reset, fine, schedule]);

  useEffect(() => () => {
    clearTimeout(idleTimer.current);
    clearTimeout(pendingTimer.current);
    if (frame.current) cancelAnimationFrame(frame.current);
    frame.current = 0;
  }, []);

  useEffect(() => {
    if (pending !== null && pending === active) {
      clearTimeout(pendingTimer.current);
      setPending(null);
    }
  }, [pending, active]);

  const indexOf = (id: string | null) => id === null ? -1 : ticks.findIndex((tick) => tick.id === id);
  const current = pending ?? active;
  const center = indexOf(windowCenter ?? current);
  const plan = planNavRailTicks(ticks.length, height, center < 0 ? ticks.length - 1 : center);
  const shown = ticks.slice(plan.start, plan.start + plan.count);
  const previewTick = ticks.find((tick) => tick.id === preview);
  const currentIndex = indexOf(current);
  const previewIndex = indexOf(preview);

  useLayoutEffect(() => {
    if (!card.current || !rail.current || !previewTick) return;
    const i = ticks.findIndex((tick) => tick.id === previewTick.id);
    const y = M.padding + (height - plan.count * plan.pitch) / 2 + (i - plan.start + 0.5) * plan.pitch;
    card.current.style.top = `${Math.max(0, Math.min(y - card.current.offsetHeight / 2, height + M.padding * 2 - card.current.offsetHeight))}px`;
  }, [previewTick, ticks, height, plan.start, plan.count, plan.pitch]);

  const dismissPreview = () => {
    previewOrigin.current = null;
    selected.current = null;
    setPreview(null);
  };
  const pick = (id: string, origin: "pointer" | "keyboard" | "drag" = "pointer") => {
    previewOrigin.current = origin;
    selected.current = id;
    setPreview(id);
    wake();
  };
  const jump = (id: string) => {
    if (!list.current.some((tick) => tick.id === id)) return;
    setPending(id);
    clearTimeout(pendingTimer.current);
    pendingTimer.current = window.setTimeout(() => setPending(null), M.pendingMs);
    void onJump(id);
    wake();
  };
  const pickY = (y: number) => {
    const bounds = drag.current;
    if (!bounds?.ids.length) return;
    const i = Math.max(0, Math.min(bounds.ids.length - 1, Math.floor((y - bounds.top) / bounds.pitch)));
    const id = bounds.ids[i];
    if (list.current.some((tick) => tick.id === id)) pick(id, "drag");
  };

  if (!fine || !ticks.length || height < NAV_RAIL_MIN_HEIGHT) return null;

  return (
    <nav
      ref={rail}
      aria-label={t("chat-navrail.rail")}
      aria-description={t("chat-navrail.keys")}
      aria-describedby={previewTick ? previewId : undefined}
      data-nav-rail
      data-nav-total={total ?? ticks.length}
      data-nav-source={total === undefined ? "loaded" : "server"}
      data-visible={visible}
      tabIndex={0}
      className={cx("absolute inset-y-0 z-10 flex flex-col justify-center py-6 transition-opacity duration-200 motion-reduce:transition-none", visible ? "opacity-100" : "opacity-0")}
      style={{ right, width: M.width }}
      onMouseEnter={() => {
        hovered.current = true;
        if (!drag.current) setWindowCenter(preview ?? current);
        wake();
      }}
      onMouseLeave={() => {
        hovered.current = false;
        if (!drag.current) {
          if (previewOrigin.current === "pointer") dismissPreview();
          wake(); // Only the rail lingers; the hover card has already gone.
        }
      }}
      onFocus={() => {
        focused.current = true;
        if (preview === null && !drag.current) pick(current ?? ticks[0].id, "keyboard");
        else wake();
      }}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
          focused.current = false;
          if (!drag.current) {
            if (hovered.current) previewOrigin.current = "pointer";
            else dismissPreview();
          }
          wake();
        }
      }}
      onKeyDown={(e) => {
        if (e.nativeEvent.isComposing || e.altKey || e.ctrlKey || e.metaKey) return;
        const old = indexOf(preview ?? current);
        let next: number | null = null;
        if (e.key === "ArrowDown" || e.key === "ArrowRight") next = Math.min(ticks.length - 1, Math.max(0, old + 1));
        if (e.key === "ArrowUp" || e.key === "ArrowLeft") next = Math.max(0, old - 1);
        if (e.key === "Home") next = 0;
        if (e.key === "End") next = ticks.length - 1;
        if (next !== null) {
          e.preventDefault();
          e.stopPropagation();
          pick(ticks[next].id, "keyboard");
          setWindowCenter(ticks[next].id);
          setAnnouncement(t("chat-navrail.selection", { index: next + 1, count: ticks.length, preview: labelOf(ticks[next]), answer: ticks[next].answerExcerpt ?? "" }));
        } else if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          e.stopPropagation();
          const id = preview ?? current ?? ticks[0].id;
          jump(id);
        } else if (e.key === "Escape") {
          e.preventDefault();
          e.stopPropagation();
          drag.current = null;
          dismissPreview();
          wake();
        }
      }}
      onPointerDown={(e) => {
        if (e.button !== 0 || card.current?.contains(e.target as Node)) return;
        e.preventDefault();
        const rect = e.currentTarget.getBoundingClientRect();
        drag.current = { pointer: e.pointerId, ids: shown.map((tick) => tick.id), pitch: plan.pitch, top: rect.top + M.padding + (height - plan.count * plan.pitch) / 2 };
        suppressClick.current = true;
        e.currentTarget.setPointerCapture(e.pointerId);
        pickY(e.clientY);
      }}
      onPointerMove={(e) => { if (drag.current?.pointer === e.pointerId) pickY(e.clientY); }}
      onPointerUp={(e) => {
        if (drag.current?.pointer !== e.pointerId) return;
        pickY(e.clientY);
        drag.current = null;
        e.currentTarget.releasePointerCapture(e.pointerId);
        if (selected.current) jump(selected.current);
        previewOrigin.current = "pointer";
        if (!hovered.current) dismissPreview();
      }}
      onPointerCancel={() => { drag.current = null; suppressClick.current = false; dismissPreview(); wake(); }}
      onLostPointerCapture={() => { if (drag.current) { drag.current = null; dismissPreview(); wake(); } }}
      onWheel={(e) => {
        if (plan.hidden) {
          const i = Math.max(0, Math.min(ticks.length - 1, (center < 0 ? 0 : center) + Math.sign(e.deltaY) * plan.count));
          setWindowCenter(ticks[i].id);
          pick(ticks[i].id);
        } else if (scroller) {
          const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? scroller.clientHeight : 1;
          scroller.scrollBy({ top: e.deltaY * unit, left: e.deltaX * unit });
        }
      }}
    >
      <span className="sr-only" aria-live="polite" aria-atomic="true">{announcement}</span>
      {shown.map((tick, k) => {
        const i = plan.start + k;
        const isCurrent = tick.id === current;
        const isPreview = tick.id === preview;
        const mark = navRailMark(i, ticks.length, currentIndex < 0 ? null : currentIndex, previewIndex < 0 ? null : previewIndex);
        return (
          <button
            key={tick.id}
            type="button"
            data-tick={i}
            tabIndex={-1}
            aria-label={t("chat-navrail.jump", { index: i + 1, preview: labelOf(tick) })}
            aria-current={isCurrent ? "true" : undefined}
            aria-describedby={tick.id === preview ? previewId : undefined}
            className="flex shrink-0 items-center justify-end"
            style={{ height: plan.pitch, paddingRight: M.endInset }}
            onMouseEnter={() => pick(tick.id)}
            onClick={() => {
              if (suppressClick.current) { suppressClick.current = false; return; }
              jump(tick.id);
            }}
          >
            <span
              aria-hidden
              data-nav-mark={mark.state}
              className={cx("shrink-0 rounded-full transition-[width,height,background-color,opacity] duration-150 motion-reduce:transition-none", isCurrent ? "bg-ink" : isPreview ? "bg-ink-2" : "bg-ink-3")}
              style={{ width: mark.width, height: mark.height, opacity: mark.opacity }}
            />
          </button>
        );
      })}
      {previewTick && (
        <div
          ref={card}
          id={previewId}
          data-nav-preview
          data-nav-rail-preview
          className="pointer-events-auto absolute z-20"
          // Padding connects the card's hit box to the rail without a timer or an unhoverable gap.
          style={{ right: M.width, width: M.previewWidth + M.previewGap, paddingRight: M.previewGap, maxWidth: `calc(100vw - ${M.width * 2}px)` }}
        >
          <div data-nav-preview-surface className="rounded-nav-preview border border-line bg-surface shadow-nav-preview" style={{ padding: M.previewPadding }}>
            <p data-nav-preview-title className="truncate text-sm font-semibold text-ink">{labelOf(previewTick)}</p>
            {previewTick.answerExcerpt && <p data-nav-preview-excerpt className="line-clamp-2 text-sm leading-relaxed text-ink-2" style={{ marginTop: M.previewParagraphGap }}>{previewTick.answerExcerpt}</p>}
          </div>
        </div>
      )}
    </nav>
  );
}
