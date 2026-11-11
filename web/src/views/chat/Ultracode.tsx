// Claude Code's ultracode switch in the chat and in "New agent" (#3428, docs/web/web.md "ultracode"): the row in the model
// panel's flyout of the model that runs (#5032), the workflow mark on the model pill, on agent rows and on to-dos, the plan
// card's chip (#12) and the line above the composer after a resume turned it off. The rules and the request are
// lib/ultracode.ts.
import { useState } from "react";
import { t } from "../../i18n";
import { useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import {
  dismissUltracodeLost, lostShown, quotaAge, quotaText, quotaWarnShown, setUltracode, shownOn, ULTRACODE, ultracodeQuota, ultracodeSwitch, useUltracodeAsk,
  type UltracodeQuota,
} from "../../lib/ultracode";
import { useUsageWindows } from "../../lib/usage-windows";
import { cx, IconButton, Spinner, Switch, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

/** The workflow mark (the same icon as a workflow's progress line) in the AI colour. It fades in with the state
 *  transition (at once with reduced motion) and has no motion of its own. `label`: read out and shown on hover. */
export function UltraMark({ size = 11, label = t("ultracode.on"), title = t("ultracode.icon_title"), className }: { size?: number; label?: string; title?: string; className?: string }) {
  return (
    <span role="img" aria-label={label} title={title} data-ultracode-mark className={cx("inline-flex shrink-0 items-center text-ai", className)} style={{ animation: "fade-in 200ms ease-out both" }}>
      <Icon name="layers" size={size} stroke={2.2} />
    </span>
  );
}

/** The Claude subscription's 5-hour and weekly use under the switch (#2432's windows), one line, and past 80% a
 *  warning when `warn` (a switch that is on or can simply be turned on; not under a reason it can't, nor while it is
 *  being turned off). Nothing without data. */
export function UltracodeQuotaLine({ warn = true, className }: { warn?: boolean; className?: string }) {
  return <QuotaLine quota={ultracodeQuota(useUsageWindows())} warn={warn} className={className} />;
}

/** The line itself. Numbers the hub marked out of date fade and say so with their age, as the usage bars do (#1906):
 *  use only grows, so an old 62% may be past 80% by now. */
export function QuotaLine({ quota, warn, className, now }: { quota: UltracodeQuota | null; warn: boolean; className?: string; now?: number }) {
  if (!quota) return null;
  const age = quotaAge(quota, now);
  return (
    <div data-ultracode-quota className={cx("flex flex-col gap-0.5 text-[11px] leading-snug", className)}>
      <span className={cx("truncate text-ink-3 tabular-nums", quota.stale && "opacity-70")}>{t("ultracode.quota", { name: quota.name, windows: quotaText(quota) })}</span>
      {age && <span data-ultracode-quota-stale className="truncate text-ink-3 opacity-70">{age}</span>}
      {warn && quota.warn && <span data-ultracode-quota-warn className="text-orange">{t("ultracode.quota_warn")}</span>}
    </div>
  );
}

/** The switch's head: the mark, the name and the switch, one line. Grayed, its track keeps a hairline: the shared
 *  switch's 50% on a white card all but vanishes, and here it sits beside the reason it can't be used. */
function SwitchHead({ on, disabled, busy, onChange }: { on: boolean; disabled: boolean; busy?: boolean; onChange: (on: boolean) => void }) {
  return (
    <div className="flex items-center gap-2">
      <Icon name="layers" size={13} stroke={2.2} className="shrink-0 text-ai" />
      <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-ink">{ULTRACODE}</span>
      {busy && <Spinner size={11} />}
      <Switch on={on} disabled={disabled} onChange={onChange} label={ULTRACODE} className="disabled:shadow-[inset_0_0_0_1px_var(--ink-3)]" />
    </div>
  );
}

/** The row under the controls of the model panel's flyout for the model that runs (its `currentExtra`, #5032): the switch
 *  applies at once (its own request, never part of a pick); Enter / Space on it are its own. A failure, a refusal too,
 *  puts it back and says why under it until the next try; the node's own reason (`ultracode_note`) is already the line,
 *  so a failure that only repeats it is not said twice. */
export function UltracodeSwitchRow({ session }: { session: Session }) {
  const ask = useUltracodeAsk(session);
  const sw = ultracodeSwitch(session, ask);
  const [error, setError] = useState("");
  const toggle = async (on: boolean) => {
    setError("");
    const r = await setUltracode(session, on);
    if (!r.ok && r.error.trim() !== (session.ultracode_note ?? "").trim()) setError(t("ultracode.failed", { error: r.error }));
  };
  return (
    <div data-popover-switch data-testid="ultracode-row" className="mt-1 border-t border-line px-2 pt-2 pb-1.5">
      <SwitchHead on={sw.on} disabled={sw.disabled} busy={ask?.sending} onChange={(on) => void toggle(on)} />
      <p data-ultracode-line className={cx("mt-1 pl-[21px] text-[11px] leading-snug break-words", sw.caution ? "text-orange" : "text-ink-3")}>{sw.line}</p>
      {error && <p role="alert" className="mt-1 pl-[21px] text-[11px] leading-snug break-words text-red">{error}</p>}
      <UltracodeQuotaLine warn={quotaWarnShown(sw)} className="mt-1 pl-[21px]" />
    </div>
  );
}

/** The same switch for an agent about to start: in "New agent", under "Thinking effort" (off each time the sheet opens,
 *  never remembered), and for each task in a plan's "Edit" (#12). `blocked`: why this computer can't start with it (its
 *  Claude Code is too old), grayed and said instead of the line. */
export function UltracodeStartRow({ on, onChange, blocked }: { on: boolean; onChange: (on: boolean) => void; blocked: string }) {
  return (
    <div className="rounded-control bg-surface px-3 py-2 shadow-hairline">
      <SwitchHead on={on && !blocked} disabled={Boolean(blocked)} onChange={onChange} />
      <p data-ultracode-line className={cx("mt-1 pl-[21px] text-[11.5px] leading-snug break-words", blocked ? "text-orange" : "text-ink-3")}>{blocked || t("ultracode.subtitle")}</p>
      <UltracodeQuotaLine warn={!blocked} className="mt-1 pl-[21px]" />
    </div>
  );
}

/** A plan task's ultracode on its card (#12), beside its computer, folder, model and permission chips: a toggle in the
 *  AI colour while on, quiet while off. `blocked`: why its computer can't start with it, grayed and said on hover. */
export function UltracodeChip({ on, onChange, blocked, disabled }: { on: boolean; onChange: (on: boolean) => void; blocked: string; disabled?: boolean }) {
  const shown = on && !blocked;
  return (
    <button
      type="button"
      data-testid="plan-ultracode"
      aria-pressed={shown}
      disabled={disabled || Boolean(blocked)}
      title={blocked || (shown ? t("ultracode.plan_on") : t("ultracode.plan_off"))}
      onClick={() => onChange(!shown)}
      className={cx(
        "inline-flex h-6 items-center gap-1 rounded-full px-2 text-[12px] whitespace-nowrap transition-colors disabled:cursor-not-allowed",
        shown ? "bg-ai-tint font-medium text-ai" : "bg-field text-ink-3 enabled:hover:text-ink-2",
        Boolean(blocked) && "opacity-60",
      )}
    >
      <Icon name="layers" size={12} stroke={2.2} className="shrink-0" />
      {ULTRACODE}
    </button>
  );
}

/** Over the composer after a resume turned it off (`ultracode_lost_at`): one line, "Turn back on" (one request; the
 *  node clears the line once it is on) and × to dismiss it. While the request waits for the turn: "Applies next turn". */
export function UltracodeLostNote({ session }: { session: Session }) {
  const features = useStore((s) => s.features);
  const ask = useUltracodeAsk(session);
  if (!lostShown(session, features)) return null;
  const waiting = shownOn(session, ask) && Boolean(session.ultracode_pending);
  const reopen = async () => {
    const r = await setUltracode(session, true);
    if (!r.ok) toast(t("ultracode.failed", { error: r.error }), "error");
  };
  const dismiss = async () => {
    const r = await dismissUltracodeLost(session);
    if (!r.ok) toast(t("ultracode.dismiss_failed", { error: r.error }), "error");
  };
  return (
    <div role="status" data-testid="ultracode-lost" className="flex h-9 shrink-0 items-center gap-2 rounded-[12px] bg-surface pr-1 pl-3 text-[12.5px] shadow-hairline" style={{ animation: "fade-up 200ms ease-out both" }}>
      <Icon name="layers" size={13} stroke={2.2} className="shrink-0 text-ai" />
      <span className="min-w-0 flex-1 truncate text-ink-2" title={t("ultracode.lost")}>{t("ultracode.lost")}</span>
      {waiting ? (
        <span className="shrink-0 whitespace-nowrap text-[12px] text-ink-3">{t("ultracode.pending")}</span>
      ) : (
        <button
          type="button"
          data-testid="ultracode-reopen"
          disabled={ask?.sending}
          onClick={() => void reopen()}
          className="flex shrink-0 items-center gap-1 whitespace-nowrap font-medium text-accent enabled:hover:underline disabled:opacity-50"
        >
          {ask?.sending ? <Spinner size={11} /> : <Icon name="retry" size={12} stroke={2.2} />}
          {t("ultracode.reopen")}
        </button>
      )}
      <IconButton icon="x" label={t("ultracode.dismiss")} onClick={() => void dismiss()} size={26} iconSize={13} />
    </div>
  );
}
