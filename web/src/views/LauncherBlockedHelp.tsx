// How to fix a launcher "New agent" grays out (#1643): why it can't start on this computer, what to do about it, the
// hub's own words under Details, and the ways on: add it to Persona as a model provider (for one the terminal can't
// run), or "Try anyway" once it is fixed (a start with it that works clears the record).
import type { LauncherBlock } from "../lib/launcher-health";
import { providerIdNote, providerTitle, type Named } from "../lib/provider-names";
import { Button, cx, IconButton } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { ProviderName } from "../ui/ProviderName";
import { t } from "../i18n";

export function LauncherBlockedHelp({ launcher, block, onTryAnyway, onAddProvider, onClose }: {
  /** Named by its display name, its startup ID beside it (#5029); an rc alias by its ID, as before. */
  launcher: Named;
  block: LauncherBlock;
  /** Picks it after all. */
  onTryAnyway: () => void;
  /** Settings › Model providers (only offered for one the terminal can't run). */
  onAddProvider: () => void;
  onClose: () => void;
}) {
  const name = providerTitle(launcher);
  const id = providerIdNote(launcher);
  return (
    <div id="new-agent-blocked-help" role="region" aria-label={block.title} data-testid="launcher-blocked-help" className="mt-2 rounded-control bg-surface px-3.5 py-3 shadow-hairline">
      <div className="flex items-start gap-2.5">
        <Icon name="warning" size={14} className="mt-0.5 shrink-0 text-orange" />
        <div className="flex min-w-0 flex-1 flex-col gap-1.5">
          <div className="flex min-w-0 items-baseline gap-2">
            <span className="shrink-0 text-[13px] font-medium text-ink">{block.title}</span>
            <ProviderName of={launcher} className={cx("min-w-0 truncate text-[11.5px]", id ? "text-ink-2" : "text-ink-3")} idClassName="min-w-0 shrink-[2] truncate text-[11px] text-ink-3" />
          </div>
          {/* the cause itself, whole (the line under the launchers was the only other place it was said); the title of
              one the terminal can't run says it already */}
          {!block.notInTerminal && <p data-testid="launcher-blocked-reason" className="text-[12.5px] font-medium leading-snug text-orange">{block.reason}</p>}
          <p className="text-[12.5px] leading-relaxed text-ink-2">
            {block.notInTerminal ? t("newagent.blocked_terminal_body", { name }) : t("newagent.blocked_other_body")}
          </p>
          <div className="text-[12px] font-medium text-ink-2">{t("newagent.how_to_fix")}</div>
          <ul className="flex list-disc flex-col gap-1 pl-4 text-[12.5px] leading-relaxed text-ink-2">
            {block.notInTerminal ? (
              <>
                <li>{t("newagent.blocked_terminal_fix_rc")}</li>
                <li>{t("newagent.blocked_terminal_fix_provider")}</li>
              </>
            ) : (
              <li>{t("newagent.blocked_other_fix")}</li>
            )}
          </ul>
          {block.detail && (
            <details data-testid="launcher-blocked-detail" className="rounded-control bg-inset px-2.5 py-1 text-[12px] text-ink-2 shadow-hairline">
              <summary className="cursor-pointer select-none">{t("newagent.details")}</summary>
              <pre className="mt-1 max-h-32 overflow-auto font-mono text-[11.5px] whitespace-pre-wrap text-ink-3 [overflow-wrap:anywhere]">{block.detail}</pre>
            </details>
          )}
          <p className="text-[11.5px] leading-snug text-ink-3">{block.notInTerminal ? t("newagent.blocked_terminal_clears") : t("newagent.blocked_other_clears")}</p>
          <div className="mt-1 flex flex-wrap gap-2">
            {block.notInTerminal && (
              <Button tone="primary" icon="plus" onClick={onAddProvider}>
                {t("newagent.add_provider")}
              </Button>
            )}
            <Button onClick={onTryAnyway}>{t("newagent.try_anyway")}</Button>
          </div>
        </div>
        <IconButton icon="x" label={t("newagent.close_help")} size={24} iconSize={13} onClick={onClose} className="-mt-1 -mr-1.5 shrink-0" />
      </div>
    </div>
  );
}
