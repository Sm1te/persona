// The model panel's rail (#5032, after Cindy's UnifiedModelRail): a 48 px column of view cells, Recent and Favorites (what
// you keep), a short rule, then All and one cell per provider in the order given. The cell that is on is filled in the
// ink; the others are quiet and light up under the pointer. It scrolls on its own, without a bar.
import { t } from "../../i18n";
import { sameView, type PanelProvider, type PanelView } from "../../lib/model-panel";
import { cx } from "../atoms";
import { Icon } from "../icons";
import { ProviderMark } from "./ProviderMark";

export function Rail({ providers, view, onView, disabled }: { providers: PanelProvider[]; view: PanelView; onView: (v: PanelView) => void; disabled?: boolean }) {
  // `tip`: the tooltip when it says more than the name (a provider's startup ID, #5029)
  const cell = (v: PanelView, label: string, icon: React.ReactNode, tip = label) => {
    const on = sameView(v, view);
    return (
      <button
        key={v.kind === "provider" ? `provider:${v.provider}` : v.kind}
        type="button"
        disabled={disabled}
        aria-label={label}
        aria-pressed={on}
        title={tip}
        data-rail-cell={v.kind === "provider" ? v.provider : v.kind}
        onClick={() => onView(v)}
        className={cx(
          "relative flex h-[38px] w-[34px] shrink-0 flex-col items-center justify-center rounded-[9px] transition-colors duration-150",
          on ? "bg-ink text-surface" : "text-ink-3 enabled:hover:bg-hover-2 enabled:hover:text-ink-2",
          disabled && "cursor-not-allowed opacity-50",
        )}
      >
        <span className="flex h-6 items-center justify-center">{icon}</span>
      </button>
    );
  };
  return (
    <div role="group" aria-label={t("ui-modelpanel.views")} className="flex min-h-0 w-12 shrink-0 scrollbar-none flex-col items-center gap-0.5 overflow-x-hidden overflow-y-auto border-r border-line px-1.5 py-2">
      {cell({ kind: "recent" }, t("ui-modelpanel.recent"), <Icon name="history" size={16} />)}
      {cell({ kind: "favorites" }, t("ui-modelpanel.favorites"), <Icon name="star" size={16} className={view.kind === "favorites" ? "fill-current" : undefined} />)}
      <div aria-hidden className="my-[3px] w-[22px] shrink-0 border-t border-line" />
      {cell({ kind: "all" }, t("ui-modelpanel.all"), <Icon name="grid" size={16} />)}
      {providers.map((p) => cell({ kind: "provider", provider: p.id }, p.name, <ProviderMark name={p.name} />, p.idNote ? `${p.name} (${p.idNote})` : p.name))}
    </div>
  );
}
