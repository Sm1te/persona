// Contexts waiting in a composer (picked in the link panel): one chip each above the input — what it is, the passage
// (or the page's title for a whole document; a workspace's name), × to drop it. They go with the next message as <persona-context>.
// A forwarded message is a card of its own (views/chat/ForwardCard.tsx ForwardBars), not a chip.
import { removeContext, useContexts, WHOLE } from "../../lib/links";
import { EASE } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { QuoteIcon } from "./QuoteIcon";
import { t } from "../../i18n";

export function ContextChips({ draftKey }: { draftKey: string }) {
  const refs = useContexts(draftKey).filter((r) => r.kind !== "forward");
  if (!refs.length) return null;
  return (
    <div className="flex flex-wrap gap-1.5 px-0.5 pt-0.5" aria-label={t("link-contextchips.label")}>
      {refs.map((r) => {
        const whole = r.text === WHOLE;
        const workspace = r.kind === "workspace";
        const label = workspace ? r.title : whole ? r.title || r.url : r.text.replace(/\s+/g, " ");
        const kindLabel = r.kind === "edit" ? t("link-contextchips.edit") : workspace ? t("link-contextchips.workspace") : t("link-contextchips.context");
        return (
          <span
            key={r.id}
            title={`${r.kind === "edit" ? t("link-contextchips.edit_title") : kindLabel} · ${r.title}\n${whole ? r.url : r.text}`}
            className="flex h-7 max-w-[280px] min-w-0 items-center gap-1.5 rounded-full bg-accent-tint pr-1 pl-2.5 text-accent-ink"
            style={{ animation: `pop-in 180ms ${EASE} both` }}
          >
            {r.kind === "edit" ? <Icon name="spark" size={11} /> : workspace ? <Icon name="folder" size={12} /> : <QuoteIcon size={12} />}
            <span className="shrink-0 text-[11px] font-semibold">{kindLabel}</span>
            <span className="min-w-0 truncate text-[12px]">{whole || workspace ? label : t("link-contextchips.quoted", { text: label })}</span>
            <button
              type="button"
              aria-label={t("link-contextchips.remove_label")}
              title={t("link-contextchips.remove")}
              onClick={() => removeContext(draftKey, r.id)}
              className="flex size-5 shrink-0 items-center justify-center rounded-full transition-colors hover:bg-accent-tint"
            >
              <Icon name="x" size={11} stroke={2.4} />
            </button>
          </span>
        );
      })}
    </div>
  );
}
