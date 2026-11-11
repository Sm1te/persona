// The thinking-effort slider in a model's setup flyout (#5032, after Cindy's EffortSlider). One stop per level the model
// takes, weakest first; the bar is one color, the level's own (lib/model-panel.ts effortColor, --effort-*), and while it
// is dragged the thumb follows the pointer and the color moves between the two levels around it. Letting go (or a click)
// lands on the nearest level and sweeps there. A bubble over the thumb names the level on hover, focus and drag, with
// "Recommended" on the model's default; "Faster" and "Smarter" label the ends while dragging. Keys: ←/↓ one level down,
// →/↑ one up, Home, End.
import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from "react";
import { t } from "../../i18n";
import { effortColor, effortColorAt } from "../../lib/model-panel";
import { cx } from "../atoms";

const MOVE = "var(--effort-move) var(--effort-ease)";
const RESIZE = "var(--effort-resize) var(--effort-ease)";

export function EffortSlider({ stops, value, recommended, labelOf, onChange, disabled = false }: {
  /** The levels, weakest first; two at least (one level is no choice: the flyout draws no slider). */
  stops: string[];
  /** The level now; one not among the stops sits at the first. */
  value: string;
  /** The model's default level: its bubble says "Recommended". */
  recommended?: string;
  labelOf: (effort: string) => string;
  onChange: (effort: string) => void;
  disabled?: boolean;
}) {
  const track = useRef<HTMLDivElement>(null);
  // the continuous position while dragging (0 … stops − 1); null: drawn at `value`
  const [drag, setDrag] = useState<number | null>(null);
  const dragging = drag !== null;
  // the pointer moved since it went down: the thumb follows it 1:1, no transition
  const [live, setLive] = useState(false);
  const pointer = useRef<number | null>(null);
  const last = Math.max(0, stops.length - 1);
  const valueIndex = Math.max(0, stops.indexOf(value));
  const index = drag === null ? valueIndex : Math.round(drag);
  const level = stops[Math.min(last, Math.max(0, index))] ?? stops[0] ?? "";
  const percent = last > 0 ? ((drag ?? valueIndex) / last) * 100 : 0;
  const color = drag === null ? effortColor(level) : effortColorAt(stops, drag);

  const at = useCallback((clientX: number) => {
    const r = track.current?.getBoundingClientRect();
    if (!r || r.width === 0) return valueIndex;
    return Math.min(1, Math.max(0, (clientX - r.left) / r.width)) * last;
  }, [last, valueIndex]);

  const commit = useCallback((i: number) => {
    const next = stops[Math.min(last, Math.max(0, i))];
    if (next && next !== value) onChange(next);
  }, [last, onChange, stops, value]);

  // the pointer is followed on the document: a drag let go outside the track still lands
  useEffect(() => {
    if (!dragging) return;
    const move = (e: PointerEvent) => {
      if (e.pointerId !== pointer.current) return;
      setLive(true);
      setDrag(at(e.clientX));
    };
    const up = (e: PointerEvent) => {
      if (e.pointerId !== pointer.current) return;
      setLive(false);
      commit(Math.round(at(e.clientX)));
      setDrag(null);
    };
    const cancel = () => {
      setLive(false);
      setDrag(null);
    };
    document.addEventListener("pointermove", move);
    document.addEventListener("pointerup", up);
    document.addEventListener("pointercancel", cancel);
    window.addEventListener("blur", cancel);
    return () => {
      document.removeEventListener("pointermove", move);
      document.removeEventListener("pointerup", up);
      document.removeEventListener("pointercancel", cancel);
      window.removeEventListener("blur", cancel);
    };
  }, [at, commit, dragging]);

  const down = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (disabled || e.button !== 0 || !e.isPrimary) return;
    pointer.current = e.pointerId;
    e.currentTarget.focus();
    e.preventDefault();
    setLive(false);
    // pressing previews the nearest level; letting go applies the last one (a drag is one change, not two)
    setDrag(Math.round(at(e.clientX)));
  };

  const key = (e: KeyboardEvent<HTMLDivElement>) => {
    if (disabled) return;
    const step = e.key === "ArrowRight" || e.key === "ArrowUp" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowDown" ? -1 : e.key === "Home" ? -last : e.key === "End" ? last : 0;
    if (!step) return;
    e.preventDefault();
    e.stopPropagation();
    commit(valueIndex + step);
  };

  return (
    <div className={cx("group/effort relative min-w-0 flex-1 pr-2.5 pl-1.5", disabled && "opacity-50")}>
      <div
        ref={track}
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label={t("ui-modelpanel.effort")}
        aria-valuemin={0}
        aria-valuemax={last}
        aria-valuenow={index}
        aria-valuetext={labelOf(level)}
        aria-disabled={disabled || undefined}
        data-dragging={dragging || undefined}
        onPointerDown={down}
        onKeyDown={key}
        className={cx("relative flex h-[38px] touch-none items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-accent", disabled ? "cursor-not-allowed" : "cursor-ew-resize")}
      >
        <div className="absolute inset-x-0 h-3 rounded-full bg-line-strong" />
        <div data-effort-fill className="absolute left-0 z-[1] h-3 rounded-full" style={{ width: `${percent}%`, backgroundColor: color, transition: live ? "none" : `width ${RESIZE}, background-color ${MOVE}` }} />
        {stops.map((s, i) =>
          i === 0 || i === last ? null : (
            <div key={s} aria-hidden className="absolute z-[2] size-[5px] -translate-x-1/2 rounded-full" style={{ left: `${(i / last) * 100}%`, backgroundColor: "color-mix(in srgb, var(--surface) 65%, transparent)" }} />
          ),
        )}
        <div
          data-effort-thumb
          className={cx("absolute z-[3] -translate-x-1/2 rounded-full border-[2.5px] border-surface", dragging && !disabled ? "h-[21px] w-[27px]" : cx("size-[21px]", !disabled && "hover:size-[23px]"))}
          style={{
            left: `${percent}%`,
            backgroundColor: color,
            boxShadow: `0 0 0 4px color-mix(in srgb, ${color} 22%, transparent)`,
            transition: `${live ? "" : `left ${MOVE}, background-color ${MOVE}, `}width ${RESIZE}, height ${RESIZE}, box-shadow ${RESIZE}`,
          }}
        >
          <div
            className={cx(
              "pointer-events-none absolute bottom-[calc(100%-2px)] left-1/2 flex -translate-x-1/2 items-center gap-1.5 rounded-[7px] bg-tooltip-bg px-2.5 py-1 text-[11px] whitespace-nowrap text-tooltip-fg opacity-0 transition-opacity duration-100",
              "group-hover/effort:opacity-100 group-has-[:focus-visible]/effort:opacity-100",
              dragging && "opacity-100",
            )}
          >
            <span aria-hidden className="size-1.5 shrink-0 rounded-full" style={{ backgroundColor: effortColor(level) }} />
            <span>{labelOf(level)}</span>
            {recommended && level === recommended && <span className="opacity-70">· {t("ui-modelpanel.recommended")}</span>}
          </div>
        </div>
      </div>
      <div aria-hidden className={cx("pointer-events-none absolute inset-x-1 -bottom-0.5 flex justify-between text-[10px] text-ink-3 transition-opacity duration-150", dragging ? "opacity-100" : "opacity-0")}>
        <span>{t("ui-modelpanel.faster")}</span>
        <span>{t("ui-modelpanel.smarter")}</span>
      </div>
    </div>
  );
}
