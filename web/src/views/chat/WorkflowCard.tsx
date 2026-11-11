// A Claude Code workflow run (#2935, #3434; docs/protocol/agent-states.md "Background workflows and Monitor") as a card.
// In the chat, where the Workflow call that launched it was (WorkflowCallCard, #5030 after cindy's AgentTaskCard): the
// shared task card (TaskCard.tsx), its title the run's name, its status word, "Workflow · tokens · tool uses · time"
// ("Running for …" while it runs), "{phase} · done/started agents" and a strip of one square per agent. It opens the
// chat's Background dialog at its run (lib/background-dialog.ts); where the chat has no such dialog (the assistant's
// chat) it opens in place to the run's phases instead. Like every tool call it sits in the folded work (super rule 2).
// In the background dialog (WorkflowCard), always open:
// - a head: the mark, its name, its status, "169/185+" agents done of those started (the "+" while it runs: the script
//   starts more as it goes), how long it has run;
// - while it runs "Now Verify: verify:chat-rich", once it has ended what it was for (and a failed run's error);
// - a progress bar in parts (done, running, failed, stopped), moving by transform: scaleX, never by width;
// - one block per phase: "done/started" (with "+" while more may start there), its agents in the order they started,
//   at most PHASE_ROWS of them with the quiet rest folded into "n more" (where they are left out), the earliest ones the
//   hub no longer lists said above the first row. The rows only say how each agent stands: a workflow agent never opens
//   as a conversation of its own.
// Things move only when a value changes, never on the first draw, and not at all with reduced motion: counts roll,
// the lit phase moves on and the one before it gets its ✓, a new agent fades in, a finished run's chip changes color.
import { memo, useState, useSyncExternalStore, type ReactNode } from "react";
import { t } from "../../i18n";
import { canOpenBackgroundDialog, openBackgroundDialog, subscribeBackgroundDialogs } from "../../lib/background-dialog";
import { useStore } from "../../lib/store";
import { compactTokens } from "../../lib/tokens";
import type { Workflow, WorkflowAgent } from "../../lib/types";
import { workDuration, type ToolStep } from "../../lib/work-groups";
import {
  agentDetail,
  agentElapsed,
  agentLabel,
  agentRunState,
  phaseCount,
  phaseRows,
  phaseTitle,
  tallyParts,
  workflowCounts,
  workflowCurrent,
  workflowDurationMs,
  workflowElapsed,
  workflowName,
  workflowPhaseBlocks,
  workflowProgress,
  workflowStatus,
  workflowStrip,
  workflowTally,
  workflowTaskStatus,
  type AgentRunState,
  type PhaseBlock,
  type PhaseState,
  type StripCell,
  type WorkflowStatus,
} from "../../lib/workflows";
import { Collapse, cx, EASE, useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { fadeInStyle, MOVE_MS, useArrivals, useFlash } from "../../ui/motion";
import { Fading, Rolling } from "../../ui/Rolling";
import { StatusMark } from "../../ui/StatusMark";
import { ActionRow } from "./ActionRow";
import { useExpanded } from "./expanded";
import { RunningFor, TaskCard, taskStatusWord } from "./TaskCard";

/** A color, a highlight or a fill going to its new value: the design system's curve, MOVE_MS. */
const MOVE: React.CSSProperties = { transitionDuration: `${MOVE_MS}ms`, transitionTimingFunction: EASE };

const CHIP: Record<WorkflowStatus, string> = {
  running: "bg-ai-tint text-ai",
  completed: "bg-green-tint text-green",
  failed: "bg-red-tint text-red",
  killed: "bg-red-tint text-red",
  stalled: "bg-orange-tint text-orange",
};

const PART: Record<AgentRunState, string> = {
  done: "bg-ai",
  running: "bg-ai/35",
  failed: "bg-red",
  stopped: "bg-ink-3/45",
};

/** A strip square's fill: the bar's part colors; an agent the counts do not account for, the plain chip fill. */
const CELL: Record<StripCell["state"], string> = { ...PART, unknown: "bg-field" };

/** An agent's state in the task cards' words (Running, Completed, Failed, Stopped). */
const CELL_WORD: Record<AgentRunState, Parameters<typeof taskStatusWord>[0]> = { running: "running", done: "completed", failed: "failed", stopped: "stopped" };

/** The run a Workflow call launched, in the chat in that call's place (#3434) while the hub lists the run, drawn as a
 *  task card (#5030). Its head opens the chat's Background dialog at the run; with no dialog listing it, it opens in
 *  place to the run's phases (closed at first; what the reader chose stays for the page's life). The hub listing no
 *  such run (an older hub, a run over a day old, one not read yet): the call's row as it always was. */
export const WorkflowCallCard = memo(function WorkflowCallCard({ step, mid, sid, scope }: { step: ToolStep; mid: string; sid: string; scope: string }) {
  const call = step.use.tool_use_id ?? "";
  const run = useStore((s) => {
    const session = (s.sessions[mid] ?? []).find((x) => x.session_id === sid) ?? s.assistant?.conversations.find((c) => c.session_id === sid);
    return session?.workflows?.find((w) => w.tool_use_id === call);
  });
  const task = run?.task_id;
  // the same read on the server: a page drawn to markup has no dialog yet, and opens in place
  const reach = () => Boolean(task) && canOpenBackgroundDialog(mid, sid, task);
  const inDialog = useSyncExternalStore(subscribeBackgroundDialogs, reach, reach);
  const [open, setOpen] = useExpanded(`${scope}workflow:${call}`);
  // the agents' clocks go each second only while their rows show
  const now = useNow(1000, Boolean(run?.running) && open && !inDialog);
  if (!run) return <ActionRow step={step} mid={mid} sid={sid} />;
  const name = workflowName(run);
  const ids = [step.use.id, ...(step.result ? [step.result.id] : [])].join(" ");
  return (
    <TaskCard
      icon="workflow"
      title={name}
      status={workflowTaskStatus(run)}
      meta={meta(run)}
      extra={<Overview run={run} />}
      onClick={inDialog ? () => void openBackgroundDialog(mid, sid, run.task_id) : () => setOpen((o) => !o)}
      ariaLabel={inDialog ? t("chat-subagentcard.open_in_background", { name }) : t(open ? "lib-workflows.collapse_run" : "lib-workflows.expand_run", { name })}
      expanded={inDialog ? undefined : open}
      trailing={inDialog ? <Icon name="sidebar" size={14} className="mt-1 shrink-0 text-ink-3" /> : undefined}
      data={{ "data-workflow-call": workflowStatus(run), "data-tool-use": call, "data-items": ids, "data-workflow-opens": inDialog ? "dialog" : "inline" }}
    >
      {!inDialog && (
        <Collapse open={open} lazy className="-mx-3">
          <div className="@container flex flex-col pt-2" data-workflow-inline>
            <div className="flex flex-col px-3 pb-2 empty:hidden">
              {/* a run without a name has its summary for a title already */}
              <About run={run} summary={Boolean(run.name?.trim())} />
            </div>
            <Body run={run} scope={scope} now={now} />
          </div>
        </Collapse>
      )}
    </TaskCard>
  );
});

/** The second line's pieces: what ran it, the tokens and tool uses its agents used (left out when the hub does not
 *  say), and how long it ran ("Running for …", each second, while it runs). */
function meta(run: Workflow): ReactNode[] {
  const parts: ReactNode[] = [<span key="harness">{t("chat-subagentcard.harness_workflow")}</span>];
  if (run.tokens) parts.push(<span key="tokens" data-task-tokens>{t("chat-subagentcard.tokens", { value: compactTokens(run.tokens) })}</span>);
  if (run.calls) parts.push(<span key="calls" data-task-calls>{t("chat-subagentcard.tool_uses", { count: run.calls })}</span>);
  const start = Date.parse(run.started_at ?? "");
  const took = workflowDurationMs(run);
  if (run.running && Number.isFinite(start)) parts.push(<RunningFor key="time" since={start} />);
  else if (took !== null) parts.push(<span key="time" className="tabular-nums" data-task-duration>{workDuration(took)}</span>);
  return parts;
}

/** Under the second line: "{phase} · done/started agents", then a square per agent, the first STRIP_CELLS of them. */
function Overview({ run }: { run: Workflow }) {
  const progress = workflowProgress(run);
  if (!progress) return null;
  const { cells, more } = workflowStrip(run);
  return (
    <>
      <span className="mt-0.5 block truncate text-[12px] leading-4 text-ink-3" data-workflow-progress-line>
        {progress.phase
          ? t("chat-subagentcard.workflow_progress_line", { phase: progress.phase, done: progress.done, total: progress.total })
          : t("chat-subagentcard.workflow_progress_count", { done: progress.done, total: progress.total })}
      </span>
      {cells.length > 0 && (
        <span aria-hidden className="mt-1 flex flex-wrap items-center gap-1" data-workflow-strip>
          {cells.map((c, i) => (
            <span
              key={i}
              data-cell={c.state}
              title={c.state === "unknown" ? c.label : [c.label, taskStatusWord(CELL_WORD[c.state])].filter(Boolean).join(" · ")}
              className={cx("size-2 rounded-[2px] transition-colors", CELL[c.state])}
              style={MOVE}
            />
          ))}
          {more > 0 && (
            <span className="text-[11px] leading-4 text-ink-3 tabular-nums" data-strip-more>
              +{more}
            </span>
          )}
        </span>
      )}
    </>
  );
}

/** A run in the chat's background dialog: always open. `onJump`: "Show in chat", to the call that launched it. */
export function WorkflowCard({ run, scope, onJump }: { run: Workflow; scope: string; onJump?: (call: string) => void }) {
  const now = useNow(1000, run.running);
  const status = workflowStatus(run);
  return (
    <div data-workflow-card={status} data-workflow-task={run.task_id} className="@container flex flex-col overflow-hidden rounded-card bg-surface shadow-hairline">
      <div className="flex min-w-0 items-center gap-2 px-3 pt-2.5 pb-1">
        <Head run={run} now={now} />
      </div>
      <div className="flex min-w-0 items-start gap-2 pr-3 pb-2 pl-10">
        <div className="flex min-w-0 flex-1 flex-col">
          <Sub run={run} />
        </div>
        {onJump && run.tool_use_id && (
          <button
            type="button"
            onClick={() => onJump(run.tool_use_id!)}
            className="flex h-5 shrink-0 items-center gap-1 rounded-[6px] text-[12px] font-medium whitespace-nowrap text-accent-ink hover:underline"
            data-workflow-jump
          >
            {t("lib-workflows.show_in_chat")}
            <Icon name="chevronRight" size={12} stroke={2.2} />
          </button>
        )}
      </div>
      <Body run={run} scope={scope} now={now} />
    </div>
  );
}

/** Its status, in its color: a new one fades in and the color goes over (completed green, failed red). */
function StatusChip({ status }: { status: WorkflowStatus }) {
  return (
    <span data-workflow-status={status} className={cx("inline-flex h-5 shrink-0 items-center rounded-full px-2 text-[11px] font-medium whitespace-nowrap transition-colors", CHIP[status])} style={MOVE}>
      <Fading text={t(`lib-workflows.status_${status}`)} />
    </span>
  );
}

/** The head's pieces: the mark, the name, the status, the counts and the time. */
function Head({ run, now }: { run: Workflow; now: number }) {
  const elapsed = workflowElapsed(run, now);
  return (
    <>
      <span className="inline-flex size-5 shrink-0 items-center justify-center rounded-full bg-ai-tint text-ai">
        <Icon name="workflow" size={12} stroke={2.1} />
      </span>
      <span className="min-w-0 flex-1 truncate text-[length:var(--chat-secondary)] leading-[1.428571] font-medium text-ink" title={run.summary}>
        {workflowName(run)}
      </span>
      <StatusChip status={workflowStatus(run)} />
      <span
        data-workflow-counts
        title={t("lib-workflows.agents_progress", { done: run.agents_done ?? 0, started: run.agents_started ?? 0 })}
        className="shrink-0 text-[12px] whitespace-nowrap text-ink-2 tabular-nums"
      >
        <Rolling value={workflowCounts(run)} />
      </span>
      {elapsed && (
        <span data-workflow-elapsed className="shrink-0 text-[12px] whitespace-nowrap text-ink-3 tabular-nums">
          <Rolling value={elapsed} />
        </span>
      )}
    </>
  );
}

/** The line under the head: where it is now while it runs; once ended, what it was for and why it stopped. */
function Sub({ run }: { run: Workflow }) {
  const now = workflowCurrent(run);
  if (!now) return <About run={run} />;
  const text = now.phase && now.agent
    ? t("lib-workflows.now_phase_agent", { phase: now.phase, agent: now.agent })
    : now.phase
      ? t("lib-workflows.now_phase", { phase: now.phase })
      : t("lib-workflows.now_agent", { agent: now.agent });
  return (
    <p className="truncate text-[12.5px] leading-5 text-ink-2" data-workflow-now title={text}>
      <Fading text={text} />
    </p>
  );
}

/** What the run is for (unless `summary` is false), and once it has ended without completing why it stopped, whole (a
 *  touch screen has no hover for a title). */
function About({ run, summary = true }: { run: Workflow; summary?: boolean }) {
  const status = workflowStatus(run);
  const error = status !== "completed" && status !== "running" ? (run.error ?? "") : "";
  const about = summary ? (run.summary ?? "") : "";
  if (!about && !error) return null;
  const fit = "break-words whitespace-pre-line";
  return (
    <>
      {about && (
        <p className={cx(fit, "text-[12.5px] leading-5 text-ink-2")} data-workflow-summary title={about}>
          <Fading text={about} />
        </p>
      )}
      {error && (
        <p className={cx(fit, "text-[12px] leading-5 text-red")} data-workflow-error title={error}>
          <Fading text={error} />
        </p>
      )}
    </>
  );
}

/** What opens below the head: the progress bar and the phases. */
function Body({ run, scope, now }: { run: Workflow; scope: string; now: number }) {
  return (
    <div className="flex flex-col" data-workflow-body>
      <Progress run={run} />
      <Phases run={run} scope={scope} now={now} />
    </div>
  );
}

/** The run's agents as a bar in parts, done first; each part moves by transform (scaleX from its start), so a change
 *  is one composited move and the first draw does not move at all. Under it, what each part counts. */
function Progress({ run }: { run: Workflow }) {
  const tally = workflowTally(run);
  if (!tally.started) return null;
  const parts = tallyParts(tally);
  return (
    <div className="flex flex-col gap-1.5 px-3 pb-2.5">
      <div
        role="progressbar"
        aria-label={t("lib-workflows.agents_progress", { done: tally.done, started: tally.started })}
        aria-valuemin={0}
        aria-valuemax={tally.started}
        aria-valuenow={tally.done}
        className="relative h-1.5 overflow-hidden rounded-full bg-field"
        data-workflow-progress
      >
        {parts.map((p) => (
          <span
            key={p.key}
            data-part={p.key}
            className={cx("absolute inset-y-0 left-0 w-full origin-left transition-transform", PART[p.key])}
            style={{ ...MOVE, transform: `translateX(${(p.from * 100).toFixed(3)}%) scaleX(${p.size.toFixed(5)})` }}
          />
        ))}
      </div>
      <div className="flex min-w-0 items-center gap-3 overflow-hidden text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums">
        {parts
          .filter((p) => tally[p.key] > 0)
          .map((p) => (
            <span key={p.key} className="flex shrink-0 items-center gap-1" data-tally={p.key}>
              <span aria-hidden className={cx("size-1.5 rounded-full", PART[p.key])} />
              <Rolling value={t(`lib-workflows.tally_${p.key}`, { count: tally[p.key] })} />
            </span>
          ))}
        {(run.calls ?? 0) > 0 && (
          <span className="ml-auto hidden shrink-0 @min-[480px]:inline" data-workflow-calls>
            <Rolling value={t("lib-workflows.calls", { count: run.calls ?? 0 })} />
          </span>
        )}
      </div>
    </div>
  );
}

/** One block per phase. */
function Phases({ run, scope, now }: { run: Workflow; scope: string; now: number }) {
  const blocks = workflowPhaseBlocks(run);
  if (!blocks.length) return null;
  return (
    <div className="flex flex-col border-t border-line" data-workflow-phases>
      {blocks.map((b, i) => (
        <PhaseView key={`${i}:${b.title}`} b={b} run={run} scope={scope} now={now} />
      ))}
    </div>
  );
}

/** A phase: its head (lit while the run is in it), and its agents. Open while the run is in it or any of them runs
 *  (not folding between one agent and the next of a phase that runs them one at a time), folded once it is behind the
 *  run with all back; a phase the reader opened or folded stays so. */
function PhaseView({ b, run, scope, now }: { b: PhaseBlock; run: Workflow; scope: string; now: number }) {
  const [open, setOpen] = useExpanded(`${scope}workflow:${run.task_id}:phase:${b.title}`, b.running > 0 || b.current);
  const [all, setAll] = useState(false);
  const opens = b.agents.length > 0;
  const { rows, folded, breakAt, unlisted } = phaseRows(b, all);
  const fewer = all && phaseRows(b).folded > 0;
  // a new agent fades in where it started (in start order: nothing above it moves)
  const fresh = useArrivals(rows.map((a) => a.id));
  const row = (a: WorkflowAgent) => <AgentRow key={a.id} a={a} now={now} fresh={fresh.has(a.id)} />;
  return (
    <section data-phase={b.title} data-phase-state={b.state} data-phase-current={b.current || undefined} className="border-t border-line first:border-t-0">
      <button
        type="button"
        disabled={!opens}
        onClick={() => setOpen((o) => !o)}
        aria-expanded={opens ? open : undefined}
        className={cx("flex h-8 w-full min-w-0 items-center gap-2 px-3 text-left transition-colors [@media(pointer:coarse)]:h-10", b.current ? "bg-ai-tint" : "enabled:hover:bg-hover")}
        style={MOVE}
      >
        <PhaseMark state={b.state} current={b.current} failed={b.failed > 0} />
        <span className={cx("min-w-0 truncate text-[12.5px] transition-colors", b.state === "next" ? "text-ink-3" : "font-medium", b.current ? "text-ai" : b.state !== "next" && "text-ink")} style={MOVE}>
          {phaseTitle(b)}
        </span>
        {b.failed > 0 && <span className="shrink-0 text-[11.5px] whitespace-nowrap text-red">{t("lib-workflows.phase_failed", { count: b.failed })}</span>}
        <span className="flex-1" />
        {b.started > 0 && (
          <span data-phase-count className="shrink-0 text-[12px] whitespace-nowrap text-ink-2 tabular-nums">
            <Rolling value={phaseCount(b)} />
          </span>
        )}
        {opens && <Icon name="chevronRight" size={12} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-150 motion-reduce:transition-none" style={{ transform: open ? "rotate(90deg)" : "none" }} />}
      </button>
      {opens && (
        <Collapse open={open} lazy>
          <ul className="flex flex-col pb-1" data-phase-agents>
            {unlisted > 0 && (
              <li className="flex h-7 min-w-0 items-center pr-3 pl-9" data-phase-unlisted>
                <span className="min-w-0 truncate text-[12px] text-ink-3">{t("lib-workflows.unlisted_agents", { count: unlisted })}</span>
              </li>
            )}
            {rows.slice(0, breakAt).map(row)}
            {folded > 0 && (
              <li className="flex h-7 items-center pr-3 pl-9">
                <button type="button" onClick={() => setAll(true)} className="text-[12px] font-medium whitespace-nowrap text-accent-ink hover:underline" data-phase-more>
                  {t("lib-workflows.more_agents", { count: folded })}
                </button>
              </li>
            )}
            {rows.slice(breakAt).map(row)}
            {fewer && (
              <li className="flex h-7 items-center pr-3 pl-9">
                <button type="button" onClick={() => setAll(false)} className="text-[12px] font-medium whitespace-nowrap text-accent-ink hover:underline" data-phase-fewer>
                  {t("lib-workflows.fewer_agents")}
                </button>
              </li>
            )}
          </ul>
        </Collapse>
      )}
    </section>
  );
}

/** Where a phase stands: ✓ done (fading in when it just got there; a red ✕ instead when some of its agents failed), a
 *  dot in the ai color while the run is in it, a ring before it starts, a square when the run ended with some of its
 *  agents not back. */
function PhaseMark({ state, current, failed }: { state: PhaseState; current: boolean; failed: boolean }) {
  const flash = useFlash(state);
  const label = current ? "current" : state === "done" && failed ? "done_failed" : state;
  return (
    <span role="img" aria-label={t(`lib-workflows.phase_${label}`)} data-phase-mark={label} className="flex size-4 shrink-0 items-center justify-center">
      {state === "done" ? (
        <span key={flash} className={cx("flex", failed ? "text-red" : "text-green")} style={flash ? fadeInStyle() : undefined}>
          <Icon name={failed ? "x" : "check"} size={13} stroke={2.4} />
        </span>
      ) : state === "current" ? (
        <span className={cx("size-2 rounded-full", current ? "bg-ai" : "bg-ai/50")} />
      ) : state === "stopped" ? (
        <span className="size-2 rounded-[2px] bg-ink-3/60" />
      ) : (
        <span className="size-2 rounded-full border-[1.5px] border-line-strong" />
      )}
    </span>
  );
}

const AGENT_MARK: Record<Exclude<AgentRunState, "failed">, "working" | "idle" | "offline"> = { running: "working", done: "idle", stopped: "offline" };

/** An agent: how it stands, its label, (wide) its model and latest tool, how long it ran; its last words on hover. Only
 *  a line to read (as in cindy's workflow tree): a workflow agent does not open as a conversation of its own. */
function AgentRow({ a, now, fresh }: { a: WorkflowAgent; now: number; fresh: boolean }) {
  const state = agentRunState(a);
  const label = agentLabel(a);
  const detail = agentDetail(a);
  const took = agentElapsed(a, now);
  return (
    <li
      data-agent={a.id}
      data-agent-state={state}
      title={a.last_text?.trim() || label}
      style={fresh ? fadeInStyle() : undefined}
      className="flex h-7 w-full min-w-0 items-center gap-2 pr-3 pl-9"
    >
      <span className="flex size-3 shrink-0 items-center justify-center">
        {state === "failed" ? <span role="img" aria-label={t("lib-workflows.agent_failed")} className="size-1.5 rounded-full bg-red" /> : <StatusMark state={AGENT_MARK[state]} size="sm" />}
      </span>
      <span className={cx("min-w-0 flex-1 truncate text-[12.5px]", state === "running" ? "text-ink" : "text-ink-2")}>{label}</span>
      {detail && <span className="hidden max-w-[45%] min-w-0 truncate text-[11.5px] text-ink-3 @min-[520px]:inline" data-agent-detail>{detail}</span>}
      {took && <span className="shrink-0 text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums">{took}</span>}
    </li>
  );
}
