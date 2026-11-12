// What keeps an agent going, beside the chat's title (iOS ActivityChips / ActivitySheet): a /loop (its next wakeup, a
// way to stop it) and the background tasks (their output). Subagents are cards in the chat where they were launched
// (SubagentCard.tsx); a workflow run's card there opens this dialog at its run (lib/background-dialog.ts).
import { useEffect, useRef, useState } from "react";
import { listenBackgroundDialog } from "../../lib/background-dialog";
import { openPanel } from "../../lib/panel";
import { t } from "../../i18n";
import { goalHeadline, goalStatusLabel, goalTone, goalUsage } from "../../lib/goal";
import { backgroundTitle, elapsed, loopCadence, loopLabel, loopNext, loopShortLabel, loopTask } from "../../lib/tasks";
import type { BackgroundTask, GoalInfo, LoopInfo, Session } from "../../lib/types";
import { backgroundCount, otherBackground, recentWorkflows } from "../../lib/workflows";
import { WorkflowCard } from "./WorkflowCard";
import { Button, copyText, cx, Dialog, useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

/** "⟳ 11:57" and "Background 2" in the chat's header (on a phone one icon: ⟳, or ⚙ 2); a click opens them. The background
 *  work is a count by a neutral gear (#3956, iOS #3719): whether it works or only waits is the agent's state, which the
 *  title's line says once; a workflow's runs keep their mark. A run's card in the chat opens the dialog too, brought to
 *  that run and lit a moment (lib/background-dialog.ts).
 *  `onJumpToCall`: a workflow card's "Show in chat" brings the call that launched it into view. */
export function ActivityChips({ session, onStopLoop, onJumpToCall }: { session: Session; onStopLoop: () => void; onJumpToCall?: (call: string) => void }) {
  const [open, setOpen] = useState(false);
  // the run a card in the chat opened the dialog at (a new object each time: the same run asked for again lights again),
  // and the last one lit
  const [focus, setFocus] = useState<{ run: string } | null>(null);
  const lit = useRef<{ run: string } | null>(null);
  const body = useRef<HTMLDivElement>(null);
  const loop = session.loop;
  // Workflow runs have their own cards (WorkflowCard); an older hub lists a run only as a background task (#2935). An
  // ended run keeps the chip for a day after it ended (#3434, the node's workflowKeep): what became of it.
  const bg = otherBackground(session);
  const now = useNow(30_000, Boolean(loop) || (session.workflows?.length ?? 0) > 0);
  const runs = recentWorkflows(session, now);
  const count = backgroundCount(session);
  const hasBg = bg.length > 0 || runs.length > 0;
  const shown = Boolean(loop) || hasBg;
  const listed = runs.map((w) => w.task_id).join("\n");
  const { machine_id: mid, session_id: sid } = session;
  useEffect(() => {
    if (!shown) return;
    return listenBackgroundDialog(mid, sid, listed ? listed.split("\n") : [], (run) => {
      setOpen(true);
      if (run) setFocus({ run });
    });
  }, [mid, sid, shown, listed]);
  useEffect(() => {
    if (!open || !focus || lit.current === focus) return;
    // the dialog's body is in this commit (Dialog mounts with `open`): bring the run's card into view and light it once
    const card = [...(body.current?.querySelectorAll<HTMLElement>("[data-workflow-task]") ?? [])].find((el) => el.dataset.workflowTask === focus.run);
    if (!card) return;
    lit.current = focus;
    card.scrollIntoView?.({ block: "nearest" });
    const base = getComputedStyle(card).backgroundColor;
    card.animate?.([{ backgroundColor: "var(--accent-tint)" }, { backgroundColor: base }], { duration: 1800, easing: "ease-out" });
  }, [open, focus]);
  // the chip speaks of workflows (the ai color, their mark) while one runs, or when ended runs are all it has
  const aboutRuns = runs.some((w) => w.running) || (runs.length > 0 && !bg.length);
  if (!shown) return null;
  const label = loop && count
    ? t("chat-activity.label_loop_and_background", { loop: loopLabel(loop, now), count })
    : loop
      ? t("chat-activity.label_loop", { loop: loopLabel(loop, now) })
      : count
        ? t("chat-activity.label_background", { count })
        : t("lib-workflows.title");
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} aria-label={t("chat-activity.open_label", { label })} title={label} className="touch-hit flex h-7 shrink-0 items-center gap-1 rounded-[8px] px-1 transition-colors hover:bg-hover">
        {loop && (
          <span className="flex h-5.5 items-center gap-1 rounded-full bg-accent-tint px-2 text-[11.5px] font-medium whitespace-nowrap text-accent-ink">
            <Icon name="loop" size={12} stroke={2.2} />
            <span className="hidden @min-[640px]:inline">{loopShortLabel(loop, now)}</span>
          </span>
        )}
        {hasBg && (
          <span className={cx("flex h-5.5 items-center gap-1 rounded-full px-2 text-[11.5px] font-medium whitespace-nowrap", aboutRuns ? "bg-ai-tint text-ai" : "bg-field text-ink-2", loop && "@max-[640px]:hidden")} data-background-chip={aboutRuns ? "workflow" : "tasks"}>
            <Icon name={aboutRuns ? "workflow" : "gear"} size={12} stroke={2.2} />
            <span className="hidden @min-[640px]:inline">{t("chat-activity.background_chip")}</span> {count > 0 && count}
          </span>
        )}
      </button>
      <Dialog open={open} onClose={() => setOpen(false)} title={loop && !hasBg ? t("chat-activity.title_loop") : !loop ? t("chat-activity.title_background") : t("chat-activity.title_both")} width={520}>
        <div ref={body} className="flex flex-col gap-3">
          {loop && (
            <LoopCard
              loop={loop}
              onStop={() => {
                setOpen(false);
                onStopLoop();
              }}
            />
          )}
          {runs.length > 0 && (
            <section aria-label={t("lib-workflows.title")} className="flex flex-col gap-2">
              {runs.some((w) => w.running) && <p className="text-[12px] leading-relaxed text-ink-3">{t("lib-workflows.waiting_note")}</p>}
              {runs.map((w) => (
                <WorkflowCard
                  key={w.task_id}
                  run={w}
                  scope={`${session.machine_id}/${session.session_id}:`}
                  onJump={
                    onJumpToCall &&
                    ((call) => {
                      setOpen(false);
                      onJumpToCall(call);
                    })
                  }
                />
              ))}
            </section>
          )}
          {bg.length > 0 && (
            <BackgroundList
              tasks={bg}
              onOutput={(task) => {
                setOpen(false);
                openPanel({ kind: "file", mid: session.machine_id, path: task.output_file!, cwd: session.cwd });
              }}
            />
          )}
        </div>
      </Dialog>
    </>
  );
}

/** The /loop driving this agent: what it repeats, how often, when it wakes next — and a way to stop it. */
function LoopCard({ loop, onStop }: { loop: LoopInfo; onStop: () => void }) {
  const now = useNow(30_000);
  const [confirming, setConfirming] = useState(false);
  const cadence = loopCadence(loop);
  const next = loopNext(loop, now);
  return (
    <div className="flex flex-col gap-2 rounded-card bg-accent-tint/50 px-3.5 py-3 shadow-hairline">
      <div className="flex items-center gap-2.5">
        <Icon name="loop" size={15} stroke={2.2} className="shrink-0 text-accent" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-[13px] font-semibold text-ink">{cadence ? t("chat-activity.looping_every", { cadence }) : t("chat-activity.looping")}</span>
          {next && <span className="truncate text-[12px] text-ink-2">{next}</span>}
        </div>
        {!confirming && (
          <button
            type="button"
            onClick={() => setConfirming(true)}
            className="inline-flex h-8 shrink-0 items-center rounded-control bg-surface px-3 text-[12.5px] font-medium whitespace-nowrap text-red shadow-btn transition-colors hover:bg-hover"
          >
            {t("chat-activity.stop_loop")}
          </button>
        )}
      </div>
      {loopTask(loop) && <p className="text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2">{loopTask(loop)}</p>}
      {loop.reason && (
        <span className="flex items-start gap-1.5 text-[12px] text-ink-3">
          <Icon name="chat" size={12} className="mt-[2px] shrink-0" />
          {loop.reason}
        </span>
      )}
      {confirming && (
        <div className="flex flex-col gap-2 rounded-[8px] bg-surface px-3 py-2.5 shadow-hairline">
          <p className="text-[12.5px] leading-relaxed text-ink-2">{t("chat-activity.stop_loop_explain")}</p>
          <div className="flex justify-end gap-2">
            <Button tone="quiet" onClick={() => setConfirming(false)}>
              {t("common.cancel")}
            </Button>
            <Button tone="danger" onClick={onStop}>
              {t("chat-activity.stop_loop")}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

/** Every background task, how long it has run, and its output (in the side panel). */
function BackgroundList({ tasks, onOutput }: { tasks: BackgroundTask[]; onOutput: (t: BackgroundTask) => void }) {
  const now = useNow(1000);
  return (
    <div className="flex flex-col overflow-hidden rounded-card bg-surface shadow-hairline">
      <div className="flex items-center gap-1.5 border-b border-line bg-inset px-3.5 py-2 text-[12px] font-medium text-ink-2">
        <Icon name="gear" size={12} className="text-orange" />
        {t("chat-activity.background_count", { count: tasks.length })}
      </div>
      {tasks.map((task, i) => {
        const cmd = task.command && task.command !== backgroundTitle(task) ? task.command : "";
        return (
          <div key={task.id} className={cx("flex items-start gap-2.5 px-3.5 py-2.5", i > 0 && "border-t border-line")}>
            <span className={cx("mt-[3px] flex size-4 shrink-0 items-center justify-center", task.kind === "workflow" ? "text-ai" : "text-orange")}>
              <Icon name={task.kind === "agent" ? "agent" : task.kind === "workflow" ? "workflow" : task.kind === "monitor" ? "monitor" : "run"} size={13} />
            </span>
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="truncate text-[13px] font-medium text-ink">{backgroundTitle(task)}</span>
              {cmd && (
                <button type="button" onClick={() => copyText(cmd)} title={t("chat-activity.copy_command")} className="line-clamp-2 text-left font-mono text-[11.5px] text-ink-3 hover:text-ink-2">
                  {cmd}
                </button>
              )}
            </div>
            <span className="shrink-0 text-[12px] whitespace-nowrap text-ink-3 tabular-nums">{elapsed(task.started_at, now)}</span>
            {task.output_file && (
              <button type="button" onClick={() => onOutput(task)} className="flex shrink-0 items-center gap-0.5 text-[12px] font-medium whitespace-nowrap text-accent-ink hover:underline">
                {t("chat-activity.view_output")}
                <Icon name="chevronRight" size={12} stroke={2.2} />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}

const goalChipTone = {
  active: "bg-accent-tint text-accent-ink",
  done: "bg-green-tint text-green",
  stopped: "bg-orange-tint text-orange",
} as const;
const goalTextTone = { active: "text-accent-ink", done: "text-green", stopped: "text-orange" } as const;

/** The goal it works toward (docs/integrations/agent-goal.md, iOS GoalBar / GoalSheet): ◎ and its status in the chat's header (the
 *  first line too where there is room); a click shows all of it, the latest check's reason and what it has spent. */
export function GoalChip({ goal }: { goal: GoalInfo }) {
  const [open, setOpen] = useState(false);
  const status = goalStatusLabel(goal);
  const headline = goalHeadline(goal);
  const usage = goalUsage(goal);
  const tone = goalChipTone[goalTone(goal)];
  const textTone = goalTextTone[goalTone(goal)];
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} aria-label={t("agent-goal.chip_label", { status, headline })} title={t("agent-goal.chip_title", { status, headline })}
        className="touch-hit flex h-7 min-w-0 shrink items-center rounded-[8px] px-1 transition-colors hover:bg-hover">
        <span className={cx("flex h-5.5 min-w-0 items-center gap-1 rounded-full px-2 text-[11.5px] font-medium whitespace-nowrap", tone)}>
          <Icon name="target" size={12} stroke={2.2} className="shrink-0" />
          <span className="hidden max-w-[220px] truncate @min-[800px]:inline">{headline}</span>
          <span className="shrink-0 @max-[640px]:hidden @min-[800px]:before:content-['·_']">{status}</span>
        </span>
      </button>
      <Dialog open={open} onClose={() => setOpen(false)} title={t("agent-goal.title")} width={520}>
        <div className="flex flex-col gap-3">
          <div className={cx("flex items-center gap-1.5 text-[12.5px] font-semibold", textTone)}>
            <Icon name="target" size={14} stroke={2.2} className="shrink-0" />
            <span className="whitespace-nowrap">{status}</span>
            {usage && <span className="truncate font-normal text-ink-3">{usage}</span>}
          </div>
          <p className="text-[13.5px] leading-relaxed whitespace-pre-wrap text-ink select-text">{goal.objective}</p>
          {goal.reason && (
            <div className="flex flex-col gap-1 rounded-card bg-stripe-bg px-3.5 py-3 shadow-hairline">
              <span className="text-[11.5px] font-semibold text-ink-3">{t(goal.status === "complete" ? "agent-goal.reason_met" : "agent-goal.reason_check")}</span>
              <p className="text-[12.5px] leading-relaxed whitespace-pre-wrap text-ink-2 select-text">{goal.reason}</p>
            </div>
          )}
          <p className="text-[12px] text-ink-3">{t("agent-goal.hint")}</p>
        </div>
      </Dialog>
    </>
  );
}
