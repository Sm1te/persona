// The line across the chat where the replies' model changed (#5034, lib/model-change.ts): cindy's model-only switch
// chip, "⇄ Opus 5.5 → Sonnet 4.5", between two hairlines. Not a control: there is nothing to open.
import { replyModelName, type ModelChange } from "../../lib/model-change";
import { Icon } from "../../ui/icons";
import { t } from "../../i18n";

export function ModelChangeDivider({ from, to }: ModelChange) {
  const before = replyModelName(from);
  const after = replyModelName(to);
  return (
    <div role="separator" aria-label={t("chat-lines.model_changed", { from: before, to: after })} className="w-full py-2 select-none" data-model-change>
      <div className="flex w-full min-w-0 items-center gap-3">
        <span aria-hidden className="h-px min-w-4 flex-1 bg-line-strong" />
        <span aria-hidden className="flex min-w-0 items-center gap-1.5 rounded-full border border-line-strong bg-page/50 px-2.5 py-1 text-[11px] text-ink-3" title={`${from} → ${to}`}>
          <Icon name="arrowLeftRight" size={12} className="shrink-0" />
          <span className="min-w-0 truncate">{`${before} → ${after}`}</span>
        </span>
        <span aria-hidden className="h-px min-w-4 flex-1 bg-line-strong" />
      </div>
    </div>
  );
}
