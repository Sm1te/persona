// The agent's plan (TodoWrite) in the chat, as cindy draws it (TodoListCard.tsx: InlinePlanCard and TodoListCard; the
// owner asked on 2026-11-12 that it follow cindy): a card where the plan was made that opens in place to the whole list,
// and, while that card is out of view, a pill above the composer whose flyout shows the list again (PlanPill). Both load
// on demand, off the chat's first screen (WorkGroup and ChatView take them lazily); what the chat reads on every render
// (which plan is the newest, whether its card is in view, Close Plan) is in latest-plan.ts.
import { useEffect, useId, useLayoutEffect, useRef, useState, type CSSProperties, type RefObject } from "react";
import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { spinStyle } from "../../lib/busy-motion";
import { planProgress, type Todo } from "../../lib/work-groups";
import { Collapse, cx, EASE, EXIT_MS, useExit } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { MOVE_MS } from "../../ui/motion";
import { useEscapeClose } from "../../ui/overlay";

// ── the marks (cindy's lucide Circle, CircleCheck and loader arc, in the 24-unit stroke grammar of ui/icons.tsx) ──

const RING = { cx: 12, cy: 12, r: 10 };
/** The marks' size and stroke (cindy's 18 at 1.5). */
const MARK = 18;
const MARK_STROKE = 1.5;

function Svg({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <svg width={MARK} height={MARK} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={MARK_STROKE} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false" className={cx("shrink-0", className)}>
      {children}
    </svg>
  );
}

/** One turn of the arc, while the turn runs (cindy's spinner cycle). */
const ARC_TURN_MS = 1000;

/** The step in progress: an arc on the same circle as a pending step's, with a gap. It turns only while the turn runs
 *  (an interrupted plan must not look busy), and never with reduced motion: unlike the spinners it does not carry
 *  BUSY_MOTION (lib/busy-motion.ts), as cindy's arc stands still there; its shape alone says "in progress". The turning
 *  is on the box around the SVG, the SVG itself stays still (cindy's Spinner). */
function ActiveMark({ live }: { live?: boolean }) {
  return (
    <span className="inline-flex shrink-0 items-center justify-center text-ink" data-plan-arc={live ? "turning" : "still"} style={live ? spinStyle(ARC_TURN_MS) : undefined}>
      <Svg>
        <path d="M22 12a10 10 0 1 1-6.91-9.51" />
      </Svg>
    </span>
  );
}

function TodoMark({ status, live }: { status: Todo["status"]; live?: boolean }) {
  if (status === "in_progress") return <ActiveMark live={live} />;
  return (
    <Svg className={status === "completed" ? "text-tool-ink" : "text-ink"}>
      <circle {...RING} />
      {status === "completed" && <path d="m9 12 2 2 4-4" />}
    </Svg>
  );
}

/** A step's words: done ones dimmed, the one in progress weighted (`active`: medium in the card, semibold in the
 *  flyout, as cindy's). */
const todoText = (status: Todo["status"], active: string) => (status === "completed" ? "text-tool-ink" : status === "in_progress" ? cx("text-ink", active) : "text-ink");

/** The pill's: how far along the current step is, a still ring (it says where the plan is, not that something runs). Its
 *  change moves (MOVE_MS); with reduced motion the stylesheet takes every transition to 1 ms, so it jumps. */
function ProgressRing({ progress }: { progress: number }) {
  const size = 16;
  const stroke = 2;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const p = Math.min(1, Math.max(0, progress));
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true" focusable="false" className="shrink-0" data-plan-ring>
      <circle cx={size / 2} cy={size / 2} r={r} fill="none" stroke="var(--tool-ink)" strokeWidth={stroke} opacity={0.45} />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke="var(--ink)"
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={c}
        strokeDashoffset={c * (1 - p)}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
        style={{ transition: `stroke-dashoffset ${MOVE_MS}ms ${EASE}` }}
      />
    </svg>
  );
}

// ── the card ──

/** The plan in the chat (cindy's InlinePlanCard): a head, `› ☰ 2/5 · <the step in progress, else the last>`, that opens
 *  and closes the whole list in place (`Collapse`, its chevron turning, as every fold in the chat). It starts open, and
 *  is not remembered; closed, a 2 px bar under the head says how far along it is. Like every tool call it sits inside
 *  its exchange's work summary (super rule 2; cindy draws it in the open, #5162). */
export function PlanCard({ id, todos, live }: { id?: string; todos: Todo[]; live?: boolean }) {
  const [open, setOpen] = useState(true);
  if (!todos.length) return null;
  const done = todos.filter((todo) => todo.status === "completed").length;
  const total = todos.length;
  // cindy's card names the step in progress, else the last one (the pill counts the next pending one instead)
  const head = todos.find((todo) => todo.status === "in_progress")?.content ?? todos[total - 1].content;
  return (
    <div className="flex w-full justify-start" data-plan-card data-plan-id={id}>
      <div className="max-w-full min-w-0 overflow-hidden rounded-xl border border-line bg-surface">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
          className="flex w-full min-w-0 cursor-pointer items-center gap-2 px-3.5 py-2.5 transition-opacity select-none hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-inset [@media(pointer:coarse)]:min-h-10"
        >
          <Icon name="chevronRight" size={14} stroke={2} className="shrink-0 text-tool-ink transition-transform duration-150 motion-reduce:transition-none" style={{ transform: open ? "rotate(90deg)" : "none" }} />
          <Icon name="todo" size={16} stroke={2} className="shrink-0 text-ink" />
          <span className="shrink-0 font-mono text-[13px] leading-none font-medium text-ink tabular-nums" data-plan-count>
            {done}/{total}
          </span>
          <span className="shrink-0 text-[length:var(--chat-secondary)] leading-none text-tool-ink">·</span>
          <span className="mt-px min-w-0 truncate text-[length:var(--chat-secondary)] leading-none text-ink" data-plan-head>
            {head}
          </span>
        </button>
        <Collapse open={open} lazy>
          <div className="flex flex-col gap-0.5 border-t border-line px-3.5 pt-2.5 pb-3" data-plan-list>
            {todos.map((todo, i) => (
              <div key={`${todo.content}:${i}`} className="flex min-h-7.5 items-center gap-2.5" data-todo={todo.status}>
                <TodoMark status={todo.status} live={live} />
                <span className={cx("min-w-0 text-[length:var(--chat-secondary)] leading-[1.45] break-words", todoText(todo.status, "font-medium"))}>{todo.content}</span>
              </div>
            ))}
          </div>
        </Collapse>
        <Collapse open={!open} lazy>
          <div className="h-0.5 w-full bg-line" data-plan-progress>
            <div className="h-full bg-ink" style={{ width: `${(done / total) * 100}%`, transition: `width ${MOVE_MS}ms ${EASE}` }} />
          </div>
        </Collapse>
      </div>
    </div>
  );
}

// ── the pill above the composer ──

type Reveal = "closed" | "hover" | "pinned";

/** The flyout's gap above the pill, and the band over it that keeps the pointer "over" while it crosses (cindy's mb-2
 *  and h-3). */
const FLYOUT_GAP = 8;
const HOVER_BRIDGE = 12;
/** cindy's flyout widths: at least 220, at most 420 and the screen less its margins. */
const FLYOUT_MIN = 220;
const FLYOUT_MAX = 420;
const SCREEN_MARGIN = 16;

interface Place {
  /** The flyout's center and bottom edge, on screen. */
  x: number;
  bottom: number;
  maxWidth: number;
  /** The pill's own box: the hover band sits on its top edge. */
  pill: { left: number; top: number; width: number };
}

/** Where the flyout goes while it shows: centered on the composer's column (cindy centers it on the composer, so a pill
 *  pushed aside by something next to it still opens in the middle), its bottom a little above the pill. It is drawn on
 *  the page's body, since what holds the pill scrolls and would clip it. Placed again when the window or the column
 *  under it changes. */
function useFlyoutPlace(shown: boolean, pill: RefObject<HTMLElement | null>): Place | null {
  const [place, setPlace] = useState<Place | null>(null);
  useLayoutEffect(() => {
    const el = pill.current;
    if (!shown || !el) return;
    const column = el.closest("[data-chat-dock]") ?? el.parentElement ?? el;
    const measure = () => {
      const r = el.getBoundingClientRect();
      const c = column.getBoundingClientRect();
      const cs = getComputedStyle(column);
      const inner = c.width - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
      const x = c.left + (parseFloat(cs.paddingLeft) || 0) + inner / 2;
      const screen = document.documentElement.clientWidth || innerWidth;
      setPlace({ x, bottom: r.top - FLYOUT_GAP, maxWidth: Math.max(0, Math.min(FLYOUT_MAX, inner > 0 ? inner : FLYOUT_MAX, screen - 2 * SCREEN_MARGIN)), pill: { left: r.left, top: r.top, width: r.width } });
    };
    measure();
    const vv = window.visualViewport;
    // the box over the composer scrolls once what is in it is taller than it may be: the pill moves with it
    const box = el.closest("[data-scroll-box]");
    addEventListener("resize", measure);
    vv?.addEventListener("resize", measure);
    box?.addEventListener("scroll", measure, { passive: true });
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    ro?.observe(column);
    return () => {
      removeEventListener("resize", measure);
      vv?.removeEventListener("resize", measure);
      box?.removeEventListener("scroll", measure);
      ro?.disconnect();
    };
  }, [shown, pill]);
  return shown ? place : null;
}

/** Above the composer while the newest plan's card is out of view (cindy's TodoListCard): a still ring of how far along
 *  the current step is and "Step 3 of 5" (ChatView shows it only while steps are left, as cindy's PinnedPlanPanel).
 *  Hovering opens the whole list in a flyout above it and leaving closes it; a click pins it open, and a second click, a
 *  click outside or Esc closes it. The flyout's ✕ (Close Plan, `onClose`) hides the pill for this plan; the agent's plan
 *  itself is untouched. `live`: the turn runs, so the step in progress turns. */
export function PlanPill({ todos, live, onClose }: { todos: Todo[]; live?: boolean; onClose?: () => void }) {
  const flyoutId = useId();
  const pillId = `${flyoutId}-pill`;
  const wrap = useRef<HTMLDivElement>(null);
  const pill = useRef<HTMLButtonElement>(null);
  const flyout = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const closeButton = useRef<HTMLButtonElement>(null);
  const [mode, setMode] = useState<Reveal>("closed");
  const revealed = mode !== "closed";
  const pinned = mode === "pinned";
  const exit = useExit(revealed, EXIT_MS.card, () => [card.current?.animate([{ opacity: 1 }, { opacity: 0 }], { duration: EXIT_MS.card, easing: "ease-in", fill: "forwards" })]);
  const place = useFlyoutPlace(exit.mounted, pill);
  // Pinned, Esc closes it, and only when it is the topmost overlay (a menu or dialog opened over it closes first). It is
  // a dialog then, so the chat's own Esc, which stops a running turn, leaves it be (ChatView looks for one).
  useEscapeClose(pinned, () => setMode("closed"));

  useEffect(() => {
    if (!pinned) return;
    // the flyout is on the body, not inside the pill's box: a press on it is not outside
    const inside = (n: EventTarget | null) => n instanceof Node && (!!wrap.current?.contains(n) || !!flyout.current?.contains(n));
    const down = (e: PointerEvent) => {
      if (!inside(e.target)) setMode("closed");
    };
    document.addEventListener("pointerdown", down, true);
    return () => document.removeEventListener("pointerdown", down, true);
  }, [pinned]);

  if (!todos.length) return null;
  const p = planProgress(todos);
  // The flyout is drawn on the body, so the browser's Tab order would reach its ✕ only at the end of the page: Tab goes
  // from the pill to the ✕ while it shows, and from the ✕ on to what comes after the pill (cindy's DOM order).
  const tabIn = (e: React.KeyboardEvent) => {
    if (e.key !== "Tab" || e.shiftKey || !revealed || !closeButton.current) return;
    e.preventDefault();
    closeButton.current.focus();
  };
  const tabOut = (e: React.KeyboardEvent) => {
    if (e.key !== "Tab") return;
    // back to the pill; going forward the browser then moves on from it, as if the ✕ came right after it
    pill.current?.focus();
    if (e.shiftKey) e.preventDefault();
  };
  return (
    <div className="flex justify-center">
      <div
        ref={wrap}
        className="inline-flex items-center justify-center"
        onMouseEnter={() => setMode((m) => (m === "closed" ? "hover" : m))}
        onMouseLeave={() => setMode((m) => (m === "hover" ? "closed" : m))}
      >
        <button
          ref={pill}
          id={pillId}
          type="button"
          onClick={() => setMode((m) => (m === "pinned" ? "closed" : "pinned"))}
          onKeyDown={tabIn}
          aria-controls={flyoutId}
          aria-expanded={revealed}
          className="touch-hit flex cursor-pointer items-center gap-2 rounded-full border border-line bg-surface px-3.5 py-1.5 whitespace-nowrap transition-opacity select-none hover:opacity-80"
          data-plan-pill
        >
          <ProgressRing progress={p.total ? p.step / p.total : 0} />
          <span className="text-[13px] leading-none text-ink tabular-nums">{t("chat-plancard.step_of", { step: p.step, total: p.total })}</span>
        </button>
        {exit.mounted &&
          place &&
          createPortal(
            <>
              {/* the band between the pill and the flyout: crossing it, the pointer is still over the pill */}
              <div aria-hidden className="fixed z-30" style={{ left: place.pill.left, top: place.pill.top - HOVER_BRIDGE, width: place.pill.width, height: HOVER_BRIDGE }} />
              <div
                ref={flyout}
                id={flyoutId}
                role={pinned ? "dialog" : undefined}
                aria-labelledby={pinned ? pillId : undefined}
                data-plan-flyout={exit.leaving ? "leaving" : mode}
                className="fixed z-30 w-max"
                style={{ left: place.x, top: place.bottom, transform: "translate(-50%, -100%)", minWidth: Math.min(FLYOUT_MIN, place.maxWidth), maxWidth: place.maxWidth } as CSSProperties}
              >
                <div
                  ref={card}
                  aria-hidden={!revealed}
                  className="relative w-full overflow-hidden rounded-xl border border-line bg-surface"
                  style={{ animation: `pop-in 160ms ${EASE} both`, transformOrigin: "bottom center" }}
                >
                  {onClose && revealed && (
                    <button
                      ref={closeButton}
                      type="button"
                      onClick={onClose}
                      onKeyDown={tabOut}
                      aria-label={t("chat-plancard.close")}
                      title={t("chat-plancard.close")}
                      className="absolute top-2 right-2 z-10 flex size-7 items-center justify-center rounded-full text-tool-ink transition-colors hover:bg-tool-hover hover:text-ink active:scale-[0.98]"
                      data-plan-close
                    >
                      <Icon name="x" size={14} stroke={2} />
                    </button>
                  )}
                  <div className={cx("flex max-h-70 flex-col gap-0.5 overflow-y-auto overscroll-contain py-2.5 pl-3.5", onClose ? "pr-10" : "pr-3.5")} data-plan-list>
                    {todos.map((todo, i) => (
                      <div key={i} className="flex h-7.5 items-center gap-2.5" data-todo={todo.status}>
                        <TodoMark status={todo.status} live={live} />
                        <span className={cx("mt-px min-w-0 truncate text-[length:var(--chat-secondary)]", todoText(todo.status, "font-semibold"))}>{todo.content}</span>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            </>,
            document.body,
          )}
      </div>
    </div>
  );
}
