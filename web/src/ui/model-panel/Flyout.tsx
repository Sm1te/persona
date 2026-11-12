// A model's setup flyout (#5032, after Cindy's ModelConfigFlyout and UnifiedFlyoutHost): 264 px, outside the panel's left
// edge (right when the left has no room), its top a little above the row's. From the top: the model's name and a star;
// the provider and the context window; the effort slider (two levels or more) and Fast (where the model has it); the
// Agent that runs it; and a footer that is as tall in its three states: recommended, customized (with "Reset to
// recommended"), a saved favorite (with "Remove from favorites"). Opened by Customize, a right-click or ←, never by
// hover; a click outside closes it, and so does Esc (the panel's key handler, which gives the focus back to the row).
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { t } from "../../i18n";
import { agentName } from "../../lib/agent-kind";
import { contextSize, flyoutPlace, priceAmount, priceTier, shownEffort, type EngineChoice, type PanelConfig, type PanelRow } from "../../lib/model-panel";
import { effortTitle } from "../../lib/session-actions";
import { AgentMark } from "../AgentMark";
import { cx } from "../atoms";
import { Icon } from "../icons";
import { EffortSlider } from "./EffortSlider";

const FLYOUT_WIDTH = 264;

export type FlyoutMode = "recommended" | "customized" | "favorite";

export function Flyout({ rowEl, panelEl, row, setup, mode, starred, disabled, focusOnOpen, extra, reposition, engines, onEngine, onEffort, onFast, onReset, onStar, onClose }: {
  /** The row it belongs to, and the panel it sits beside. */
  rowEl: HTMLElement;
  panelEl: HTMLElement;
  row: PanelRow;
  /** The setup it shows (an edit on its way shows its new value). */
  setup: PanelConfig;
  mode: FlyoutMode;
  starred: boolean;
  disabled?: boolean;
  /** Opened from the keyboard (←): the focus moves to its first control. */
  focusOnOpen?: boolean;
  /** Drawn under the controls (the model that runs now: Claude Code's ultracode switch). */
  extra?: ReactNode;
  /** Changes when the list's rows do (a favorite saved adds a group above the row): placed again. */
  reposition?: unknown;
  /** The engines the model runs with here (lib/model-panel.ts engineChoices), its own first: more than one draws them as
   *  small pills, the others picked on a click; absent or one: the row's engine as a static block. */
  engines?: EngineChoice[];
  onEngine?: (choice: EngineChoice) => void;
  onEffort: (effort: string) => void;
  onFast: (on: boolean) => void;
  onReset: () => void;
  onStar: () => void;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState<{ left: number; top: number } | null>(null);
  const { model, provider } = row;

  useLayoutEffect(() => {
    let frame = 0;
    const place = () => {
      if (!rowEl.isConnected || !ref.current) return;
      const p = flyoutPlace({ row: rowEl.getBoundingClientRect(), panel: panelEl.getBoundingClientRect(), width: FLYOUT_WIDTH, height: ref.current.offsetHeight, screenW: innerWidth, screenH: innerHeight });
      setAt((was) => (was && was.left === p.left && was.top === p.top ? was : { left: p.left, top: p.top }));
    };
    const soon = (e?: Event) => {
      if (e && ref.current?.contains(e.target as Node)) return;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(place);
    };
    place();
    // a list that scrolls or a window that changes size moves the row: the flyout follows it
    document.addEventListener("scroll", soon, true);
    addEventListener("resize", soon);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("scroll", soon, true);
      removeEventListener("resize", soon);
    };
  }, [rowEl, panelEl, row.key, reposition]);

  useEffect(() => {
    const down = (e: PointerEvent) => {
      const target = e.target as Element | null;
      if (!target || ref.current?.contains(target)) return;
      // a row's Customize toggles or moves the flyout on its own click
      if (target.closest?.("[data-row-customize]") && panelEl.contains(target)) return;
      onClose();
    };
    addEventListener("pointerdown", down, true);
    return () => removeEventListener("pointerdown", down, true);
  }, [panelEl, onClose]);

  useEffect(() => {
    if (!focusOnOpen) return;
    const first = ['[role="slider"]:not([aria-disabled])', "[data-fast-toggle]:not(:disabled)", "button:not(:disabled)"];
    for (const sel of first) {
      const el = ref.current?.querySelector<HTMLElement>(sel);
      if (!el) continue;
      el.focus({ preventScroll: true });
      break;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once, when it opens
  }, [row.key]);

  const slider = model.efforts.length > 1;
  // none set and no default known (a Claude Code model for a new Agent): the slider says so rather than sit on the lowest
  const effort = shownEffort(setup, model);
  const name = model.name;
  const price = priceTier(model.price) ? model.price : undefined;
  const star = mode === "favorite" ? t("ui-modelpanel.remove_favorite") : t("ui-modelpanel.save_favorite");

  return createPortal(
    <div
      ref={ref}
      role="dialog"
      aria-label={t("ui-modelpanel.setup_of", { model: name })}
      data-model-flyout
      className="fixed z-50 flex flex-col rounded-[12px] bg-surface p-3.5 pb-3 shadow-overlay transition-[top] duration-150 ease-out"
      style={{ width: FLYOUT_WIDTH, maxWidth: "calc(100vw - 16px)", left: at?.left ?? -9999, top: at?.top ?? -9999, visibility: at ? undefined : "hidden" }}
    >
      <div className="flex items-start gap-2">
        <span title={name} className={cx("line-clamp-2 min-w-0 flex-1 text-[14px] leading-[1.35] font-semibold break-words text-ink", model.custom && "font-mono")}>{name}</span>
        <button
          type="button"
          disabled={disabled}
          title={star}
          aria-label={star}
          aria-pressed={mode === "favorite"}
          onClick={onStar}
          className={cx("mt-px flex size-5 shrink-0 items-center justify-center rounded-[6px] transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50", starred ? "text-favorite-star" : "text-ink-3 enabled:hover:text-favorite-star")}
        >
          <Icon name="star" size={14} className={starred ? "fill-current" : undefined} />
        </button>
      </div>
      <div className="truncate border-b border-line pt-[3px] pb-2.5 text-[12px] text-ink-3">
        {provider.name}
        {model.context ? ` · ${t("ui-modelpanel.context", { value: contextSize(model.context) })}` : ""}
      </div>
      {(slider || model.fast) && (
        <div className="flex items-center gap-2 pt-3.5">
          {slider && <EffortSlider stops={model.efforts} value={effort} recommended={model.defaultEffort} labelOf={effortTitle} unsetLabel={t("ui-modelpanel.effort_unset")} onChange={onEffort} disabled={disabled} />}
          {model.fast && (
            <button
              type="button"
              data-fast-toggle
              disabled={disabled}
              aria-pressed={Boolean(setup.fast)}
              title={t("ui-modelpanel.fast")}
              aria-label={t("ui-modelpanel.fast")}
              onClick={() => onFast(!setup.fast)}
              className={cx(
                "ml-auto flex size-7 shrink-0 items-center justify-center rounded-full border transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50",
                setup.fast ? "border-transparent bg-fast-tint text-fast" : "border-line-strong text-ink-3 enabled:hover:bg-hover-2",
              )}
            >
              <Icon name="bolt" size={14} className="fill-current" />
            </button>
          )}
        </div>
      )}
      {extra && <div className="pt-2">{extra}</div>}
      <div role="group" aria-label={t("ui-modelpanel.engine")} className="pt-2">
        {engines && engines.length > 1 ? (
          // Cindy's engine pills: 26 px, no border, mark and name, wrapping; the row's own one is the chosen one
          <div className="flex flex-wrap gap-1">
            {engines.map((c) => (
              <button
                key={`${c.engine}\n${c.provider.id}`}
                type="button"
                data-engine-choice={c.engine}
                aria-pressed={c.own}
                disabled={disabled || c.own || c.blocked !== undefined}
                title={c.own ? agentName(c.engine) : c.blocked || t("ui-modelpanel.engine_via", { provider: c.provider.name })}
                onClick={() => onEngine?.(c)}
                className={cx(
                  "flex h-[26px] max-w-full min-w-0 items-center gap-1.5 rounded-control px-2 text-[12px] leading-4 transition-colors duration-150",
                  c.own ? "bg-hover-2 text-ink" : "text-ink-2 enabled:hover:bg-hover-2 enabled:hover:text-ink",
                  c.blocked !== undefined && "opacity-50",
                  !c.own && "disabled:cursor-not-allowed",
                )}
              >
                <AgentMark agent={c.engine} size={12} decorative className="shrink-0 text-ink-2" />
                <span className="min-w-0 truncate">{agentName(c.engine)}</span>
                {c.own && <Icon name="check" size={12} stroke={2.5} className="shrink-0 text-ink" />}
              </button>
            ))}
          </div>
        ) : (
          <div className="flex min-h-7 items-center gap-1 rounded-control bg-hover-2 pr-1">
            <span className="flex min-h-7 min-w-0 flex-1 items-center gap-1.5 px-1 text-ink">
              <span aria-hidden className="inline-flex size-4 shrink-0 items-center justify-center">
                <AgentMark agent={provider.engine} size={12} decorative className="text-ink-2" />
              </span>
              <span className="min-w-0 truncate text-[12px] leading-4">{agentName(provider.engine)}</span>
            </span>
            <Icon name="check" size={12} stroke={2.5} className="shrink-0 text-ink" />
          </div>
        )}
      </div>
      {price && (
        <div data-model-price className="mt-2 border-t border-line pt-2 text-[12px] leading-[1.7] text-ink-3">
          <div>{t("ui-modelpanel.price_per_million")}</div>
          <div className="flex flex-wrap items-baseline gap-x-1.5 tabular-nums">
            <span className="whitespace-nowrap">{t("ui-modelpanel.price_input")} <span className="font-medium text-ink">${priceAmount(price.input)}</span></span>
            <span className="whitespace-nowrap">{t("ui-modelpanel.price_output")} <span className="font-medium text-ink">${priceAmount(price.output)}</span></span>
            {price.cache_read !== undefined && <span className="whitespace-nowrap">{t("ui-modelpanel.price_cache_read")} <span className="font-medium text-ink">${priceAmount(price.cache_read)}</span></span>}
          </div>
        </div>
      )}
      <div className="mt-2 flex min-h-[22px] items-center justify-between gap-2 border-t border-line pt-2 text-[11px]">
        <span className={cx("min-w-0 truncate", mode === "recommended" ? "text-ink-3" : "text-ink-2")}>
          {mode === "favorite" ? t("ui-modelpanel.setup_saved") : mode === "customized" ? t("ui-modelpanel.setup_customized") : t("ui-modelpanel.setup_recommended")}
        </span>
        {mode !== "recommended" && (
          <button
            type="button"
            disabled={disabled}
            onClick={mode === "favorite" ? onStar : onReset}
            className="shrink-0 whitespace-nowrap text-ink-2 underline underline-offset-2 transition-colors duration-150 enabled:hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
          >
            {mode === "favorite" ? t("ui-modelpanel.remove_favorite") : t("ui-modelpanel.reset")}
          </button>
        )}
      </div>
    </div>,
    document.body,
  );
}
