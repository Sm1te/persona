// The card a task the agent started draws in the chat where it started it (#5030, after cindy's AgentTaskCard): a
// subagent's (SubagentCard) and a workflow run's (WorkflowCallCard) share it. Two lines: the avatar, a small kind label,
// the title and the status word; then what ran it, what it ran on, what it used and how long it took ("Running for …",
// each second, while it runs). The head is one button: it opens the card in place, or opens where the task lives.
import { type ReactNode } from "react";
import { t } from "../../i18n";
import { cx, Spinner, useNow } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { workDuration, type AgentStatus } from "../../lib/work-groups";

/** The status word, the same four on every task card. */
export function taskStatusWord(status: AgentStatus): string {
  return t(`chat-subagentcard.status_${status}`);
}

/** The status mark: a spinner while it runs, otherwise the outcome's icon; only a failure is red. */
function StatusMark({ status }: { status: AgentStatus }) {
  if (status === "running") return <Spinner size={11} />;
  return <Icon name={status === "failed" ? "warning" : status === "stopped" ? "stop" : "check"} size={12} />;
}

/** "Running for 1m 5s", counted each second in its own leaf: the ticking redraws nothing else of the card. */
export function RunningFor({ since }: { since: number }) {
  const now = useNow(1000);
  return (
    <span className="tabular-nums" data-task-elapsed>
      {t("chat-subagentcard.running_for", { duration: workDuration(Math.max(0, now - since)) })}
    </span>
  );
}

/** The model chip on the second line: the line's own size and color, only a chip's fill and radius. */
export function ModelChip({ children }: { children: ReactNode }) {
  return (
    <span className="inline-flex items-center rounded-full bg-field px-1.5 py-0.5" data-task-model>
      {children}
    </span>
  );
}

export interface TaskCardProps {
  icon: IconName;
  /** The small word before the title ("Subagent"); none for a workflow. */
  label?: string;
  title: string;
  status: AgentStatus;
  /** The second line's pieces, in order (the harness, the model chip, tokens, tool uses, the time). */
  meta: ReactNode[];
  /** Lines under the second one (a workflow's phase and its agents). */
  extra?: ReactNode;
  /** The head's own button: what it does, and how it says so. */
  onClick: () => void;
  ariaLabel: string;
  /** Set when the head opens the card in place; unset when it opens something else (`trailing` says what). */
  expanded?: boolean;
  /** The mark at the head's end; the chevron (turned while open) when unset. */
  trailing?: ReactNode;
  /** The card's opened part, under the head. */
  children?: ReactNode;
  data?: Record<string, string | undefined>;
}

export function TaskCard({ icon, label, title, status, meta, extra, onClick, ariaLabel, expanded, trailing, children, data }: TaskCardProps) {
  return (
    <div className="flex w-full min-w-0" {...data}>
      <div className="w-full min-w-0 rounded-xl border border-line bg-surface px-3 py-2">
        <button
          type="button"
          onClick={onClick}
          aria-expanded={expanded}
          aria-label={ariaLabel}
          className="flex w-full min-w-0 items-start gap-2 rounded-lg text-left [@media(pointer:coarse)]:min-h-10"
        >
          <span className="mt-0.5 inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-field text-ink-2">
            <Icon name={icon} size={14} />
          </span>
          <span className="min-w-0 flex-1">
            <span className="flex min-w-0 items-center gap-2">
              {label && <span className="shrink-0 text-[11px] text-ink-2">{label}</span>}
              <span className="min-w-0 flex-1 truncate text-[length:var(--chat-secondary)] leading-[1.428571] font-medium text-ink">{title}</span>
              <span className={cx("inline-flex shrink-0 items-center gap-1.5 text-[12px] leading-[1.333333] whitespace-nowrap", status === "failed" ? "text-red" : "text-ink-2")} data-task-status={status}>
                <StatusMark status={status} />
                {taskStatusWord(status)}
              </span>
            </span>
            {meta.length > 0 && (
              <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[12px] leading-[1.333333] text-ink-3" data-task-meta>
                {meta}
              </span>
            )}
            {extra}
          </span>
          {trailing ?? (
            <Icon name="chevronRight" size={14} stroke={2.2} className="mt-1 shrink-0 text-ink-3 transition-transform duration-150 motion-reduce:transition-none" style={{ transform: expanded ? "rotate(90deg)" : "none" }} />
          )}
        </button>
        {children}
      </div>
    </div>
  );
}
