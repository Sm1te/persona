import { useState } from "react";
import { t } from "../../i18n";
import type { FeedItem } from "../../lib/feed";
import { ago } from "../../lib/format";
import { checkCount, reportLine, reportText, reviewBadge, reviewVerdictLabel, type ReviewTone } from "../../lib/supervision";
import type { Todo } from "../../lib/todos";
import { cx } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Markdown } from "../../ui/Markdown";
import { useClamped } from "../../ui/use-clamped";

// How a supervised task's review stands (#3124, docs/assistant/unified-entry.md "Supervision"): a chip and the count of criteria met
// on its card, a line in the conversation for each review. The existing checklist stays with the lazy to-do detail.

const TONE: Record<ReviewTone, string> = {
  accent: "bg-field text-accent-ink",
  red: "bg-field text-red",
  orange: "bg-field text-orange",
  green: "bg-green-tint text-green",
  muted: "bg-field text-ink-2",
};

/** The review's chip, with the criteria met; nothing for a task that is not supervised. */
export function ReviewBadge({ todo, className }: { todo: Todo; className?: string }) {
  const badge = reviewBadge(todo);
  if (!badge) return null;
  const count = checkCount(todo.supervision);
  return (
    <span data-testid="review-badge" className={cx("inline-flex h-5 shrink-0 items-center gap-1 whitespace-nowrap rounded-md px-1.5 text-[11.5px] font-semibold", TONE[badge.tone], className)}>
      <Icon name="target" size={11} stroke={2.2} />
      {badge.label}
      {count && <span className="font-medium tabular-nums opacity-80">· {count}</span>}
    </span>
  );
}

/** What the task's agent last reported to Persona, as it wrote it (#3181): the Progress row shows the hub's paraphrase,
 *  this is the original, Markdown rendered. A section on the to-do's page; `banner`: over its agent's chat in the task's
 *  details, folding to its title line. Nothing before its first report (or on an older hub). */
export function LatestReport({ todo, banner }: { todo: Todo; banner?: boolean }) {
  const [open, setOpen] = useState(true);
  const text = reportText(todo.supervision);
  if (!text) return null;
  const when = ago(todo.supervision?.progress?.at);
  const body = (
    <div className={cx("min-w-0 select-text text-ink [overflow-wrap:anywhere]", banner ? "mt-1.5 max-h-[40vh] overflow-y-auto overscroll-contain" : "mt-2")}>
      <Markdown text={text} small />
    </div>
  );
  if (banner) {
    return (
      <section data-testid="latest-report" aria-label={t("assistant-tasks.report_latest")} className="border-b border-line bg-inset px-4 py-2.5">
        <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="touch-hit flex w-full min-w-0 items-center gap-2 text-left text-[12.5px] text-ink-2 hover:text-ink">
          <Icon name="chat" size={12} className="shrink-0" />
          <span className="min-w-0 flex-1 truncate font-semibold">{t("assistant-tasks.report_latest")}</span>
          {when && <span className="shrink-0 text-[12px] text-ink-3">{when}</span>}
          <Icon name="chevronRight" size={12} className="shrink-0 text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : undefined }} />
        </button>
        {open && body}
      </section>
    );
  }
  return (
    <section data-testid="latest-report" className="mt-4 rounded-control bg-inset p-3.5 shadow-hairline">
      <div className="flex items-center gap-2 text-[12.5px] text-ink-2">
        <Icon name="chat" size={12} className="shrink-0" />
        <h3 className="min-w-0 flex-1 truncate font-semibold">{t("assistant-tasks.report_latest")}</h3>
        {when && <span className="shrink-0 text-[12px] text-ink-3">{when}</span>}
      </div>
      {body}
    </section>
  );
}

/** A supervised task's agent reported to Persona (feed kind "report": a question, a block, a finish): who, and what. */
export function ReportLine(props: { item: FeedItem; title: string; onOpenTask?: () => void }) {
  const [open, setOpen] = useState(false);
  if (props.item.body?.kind !== "progress") return <ReportBody {...props} />;
  return (
    <div className="flex min-w-0 flex-col gap-1" data-testid="progress-report">
      <button type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} className="touch-hit flex min-w-0 items-center gap-1 text-left text-xs whitespace-nowrap text-ink-3 hover:text-ink-2">
        <span className="min-w-0 truncate">{t("assistant-tasks.report_progress", { title: props.title })}</span>
        <Icon name="chevronRight" size={12} className="shrink-0" style={{ transform: open ? "rotate(90deg)" : undefined }} />
      </button>
      {open && <ReportBody {...props} />}
    </div>
  );
}

function ReportBody({ item, title, onOpenTask }: { item: FeedItem; title: string; onOpenTask?: () => void }) {
  const body = (item.body ?? {}) as { kind?: string; text?: string; step?: number; steps?: number; later?: boolean };
  // A question that can wait is no call on the owner now: it is drawn quietly, with its mark.
  const tone = !body.later && (body.kind === "blocked" || body.kind === "question") ? "text-orange" : "text-ink-2";
  return (
    <div data-testid="report-line" className="flex max-w-full min-w-0 flex-col gap-1 self-start pr-10">
      <span className={cx("inline-flex min-w-0 items-center gap-1 text-[12px] font-medium", tone)}>
        <Icon name="chat" size={11} className="shrink-0" />
        {onOpenTask ? (
          <button type="button" onClick={onOpenTask} className="min-w-0 truncate text-left hover:underline">
            {t("assistant-tasks.report_from", { title })}
          </button>
        ) : (
          <span className="min-w-0 truncate">{t("assistant-tasks.report_from", { title })}</span>
        )}
        {body.later && <span className="shrink-0 font-normal" data-testid="report-later">· {t("assistant-tasks.report_later")}</span>}
      </span>
      <div className="rounded-[18px] bg-field px-3.5 py-2 text-[14.5px] whitespace-pre-wrap">{reportLine(body.kind ?? "", body.text ?? "", body.step, body.steps)}</div>
    </div>
  );
}

/** A review in the conversation (feed kind "review"): the verdict and the supervisor's note. A note cut at the line's
 *  end opens in full on a click (what to decide, what is missing, #4861), as an answered card's line does. `by`: who
 *  reviewed when it was not this conversation (the background supervisor, #5082), said before the task. With it the
 *  line has one more thing to fit: the reviewer and the task give way (each cut with an ellipsis) and the verdict and
 *  the note's arrow keep their room, so a phone's width cuts names, never the verdict (`fit`). */
export function ReviewLine({ item, title, by, onOpenTask }: { item: FeedItem; title: string; by?: string; onOpenTask?: () => void }) {
  const body = (item.body ?? {}) as { verdict?: string; round?: number; note?: string };
  const [open, setOpen] = useState(false);
  const { ref, cut } = useClamped<HTMLSpanElement>(open, "x");
  const expandable = Boolean(body.note) && (cut || open);
  const fit = Boolean(by);
  return (
    <div data-testid="review-line" className={cx("flex max-w-full min-w-0 flex-col self-start bg-field text-[12px] text-ink-2", open ? "w-full rounded-card" : "rounded-full")}>
      <div className="flex min-w-0 items-center gap-1.5 px-3 py-1.5">
        <Icon name="target" size={12} className="shrink-0" />
        {by && (
          <span className="flex min-w-0 items-center gap-1">
            <span data-testid="review-by" className="min-w-0 truncate">{by}</span>
            <span className="shrink-0">·</span>
          </span>
        )}
        {onOpenTask ? (
          <button type="button" onClick={onOpenTask} className={cx("max-w-[45%] truncate text-left hover:text-ink hover:underline", fit ? "min-w-0" : "shrink-0")}>
            {title}
          </button>
        ) : (
          <span className={cx("max-w-[45%] truncate", fit ? "min-w-0" : "shrink-0")}>{title}</span>
        )}
        <button
          type="button"
          aria-expanded={expandable ? open : undefined}
          disabled={!expandable}
          onClick={() => setOpen(!open)}
          className={cx("flex flex-1 items-center gap-1.5 text-left enabled:cursor-pointer", fit ? "min-w-max" : "min-w-0")}
        >
          <span className="shrink-0 whitespace-nowrap font-semibold">· {reviewVerdictLabel(body.verdict ?? "", body.round)}</span>
          {body.note && !open && (
            <span ref={ref} className={cx("min-w-0 flex-1 truncate", fit && "w-0")}>
              {body.note}
            </span>
          )}
          {open && <span className="flex-1" />}
          {expandable && (
            <Icon name="chevronRight" size={12} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : "none" }} />
          )}
        </button>
      </div>
      {open && body.note && (
        <div className="px-3 pt-0.5 pb-2.5 text-[13px] leading-[1.55] whitespace-pre-wrap text-ink select-text [overflow-wrap:anywhere]" data-testid="review-note">
          {body.note}
        </div>
      )}
    </div>
  );
}
