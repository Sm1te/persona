// The bar above the composer while an Agent works (cindy's RunningStatusBar, #1963): what its status line says (the
// spinner's verb, or "Thinking"), how long the turn has run, the tokens it has written so far and their rate (#2518).
// It breathes once each time the count moves (output, not just time passing); when the turn ends it stays a second,
// then fades out. Without a status line (another agent, an older hub) it says "Running" and counts from when the turn
// started. While the chat's live work line ("Working…") ticks, the bar leaves the time to it (#3534: the same clock
// twice; iOS shows it there only).
// Once the turn is over and a workflow keeps the agent working (#3434, `background_working`), it says what runs:
// "Background workflow · Verify · 169/185+", timed from the workflow's start (the turn's own time read as if the agent
// were still at it). The count stays whole on a narrow screen: the words before it give way first. An agent whose
// background commands only wait (#6045, `background_waiting`) gets a still line instead: what it waits on (the header
// says the state, #3956), timed from when it started waiting, with no breath and no tokens. A subagent still running in
// the background after the turn (#5030, cindy's background subtasks) gets "Background subtasks running, still consuming
// model usage…" with no time and no tokens (the turn's would be left over); cindy's "Stop all" waits for a node that can
// stop a subagent (#5044). With the hub out of reach the bar says so in front of the last state it knew, and its clock
// stands still (nothing new can have arrived).
import { useEffect, useRef, useState } from "react";
import { duration } from "../../lib/format";
import { useSessionLive } from "../../lib/live";
import { useStore } from "../../lib/store";
import type { SessionLive } from "../../lib/types";
import { workflowCounts, type BackgroundLine, type WaitLine } from "../../lib/workflows";
import { stateName } from "../../lib/state-labels";
import { useNow } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { FADE_OUT_MS, LINGER_MS, useBreath, useCountUp } from "../../ui/motion";
import { Rolling } from "../../ui/Rolling";
import { compactTokens } from "../../lib/tokens";
import "../../ui/stream/stream.css";
import { t } from "../../i18n";

/** 22.8k, 1.2k, 950, 1.2M: one decimal with k / M / B (the reference client's formatRunningTokenCount, the subagent
 *  cards' tokens too; not lib/compaction's ten-thousand units). */
export const tokenCount = compactTokens;

/** Tokens a second over the turn so far; nothing before it has run long enough to divide by (#2518). */
export function tokenRate(tokens: number | undefined, elapsed: number): number | undefined {
  return tokens === undefined || elapsed < 3 ? undefined : Math.round(tokens / elapsed);
}

/** The subagents the agent waits on and what they do (lib/tasks.ts agentsWorking). */
export interface AgentsWorking {
  count: number;
  doing: string[];
}

/** The left side's words: while the agent waits on its subagents, how many work and what the newest does (#5602). */
export function statusText(live: SessionLive | null, agents?: AgentsWorking | null): string {
  if (agents) {
    const head = t("chat-runningstatusbar.agents_working", { count: agents.count });
    if (!agents.doing.length) return `${head}…`;
    const more = agents.doing.length > 1 ? ` · ${t("chat-runningstatusbar.agents_more", { count: agents.doing.length - 1 })}` : "";
    return `${head} — ${agents.doing[0]}…${more}`;
  }
  if (!live) return t("chat-runningstatusbar.running");
  if (live.thinking) return t("chat-runningstatusbar.thinking");
  return live.verb ? `${live.verb}…` : t("chat-runningstatusbar.running");
}

export function RunningStatusBar({ mid, sid, working, startedAt, clock, agents, background, wait, subtasks }: { mid: string; sid: string; working: boolean; startedAt?: string; clock?: boolean; agents?: AgentsWorking | null; background?: BackgroundLine | null; wait?: WaitLine | null; subtasks?: boolean }) {
  const live = useSessionLive(mid, sid);
  const offline = useStore((s) => s.conn !== "online");
  if (wait && working) return <WaitingView wait={wait} offline={offline} />;
  return <RunningStatusView live={live} working={working} startedAt={startedAt} clock={clock} agents={agents} background={background} subtasks={subtasks} offline={offline} />;
}

/** In front of the last state the bar knows, while the hub is out of reach. */
function Disconnected() {
  return <span className="shrink-0 text-ink-3" data-running-offline>{t("chat-runningstatusbar.disconnected")} ·</span>;
}

/** The bar of an agent that only waits in the background (#6045): still, in the waiting color, timed from when it began. */
export function WaitingView({ wait, offline }: { wait: WaitLine; offline?: boolean }) {
  const now = useNow(1000, !offline);
  const since = wait.since ? Date.parse(wait.since) : NaN;
  // what it waits on: the header's title line says the state ("Waiting in the background"), once (#3956); the state's name
  // only stands in when the wait has no words of its own
  const words = [wait.what || stateName("bg_waiting"), wait.more > 0 ? t("chat-runningstatusbar.more_waits", { count: wait.more }) : ""].filter(Boolean).join(" · ");
  return (
    <div data-running-status="on" data-running-background="waiting" className="flex select-none items-center justify-between gap-3 px-2 py-1.5 text-[13px] font-medium">
      <div className="flex min-w-0 items-center gap-1.5 text-status-waiting">
        <Icon name="clock" size={14} className="shrink-0 -translate-y-px" />
        {offline && <Disconnected />}
        <span className="truncate" data-running-verb>{words}</span>
      </div>
      {Number.isFinite(since) && <span className="shrink-0 whitespace-nowrap text-ink-3 tabular-nums" data-running-elapsed>{duration(Math.max(0, now - since))}</span>}
    </div>
  );
}

/** Whether the bar speaks of the background workflow: the hub says the turn is over, or (a hub from before
 *  background_working) a workflow runs and no turn of the agent's own shows: no status line, no live work line ticking
 *  (`clock`: the bar has the time), no subagent it is waiting on (`agents`, its call not answered yet). */
function inBackground(background: BackgroundLine | null | undefined, live: SessionLive | null, clock: boolean, agents: AgentsWorking | null | undefined): background is BackgroundLine {
  return Boolean(background && (!background.guessed || (clock && !agents && !(live?.verb || live?.thinking))));
}

/** The words for it: "Background workflow · Verify · 169/185+ · 1 more". Only the words before the count shrink (the
 *  name for it first, then the phase), so a narrow bar keeps the count and the other runs whole. */
function BackgroundWords({ line }: { line: BackgroundLine }) {
  const w = line.run;
  return (
    <span className="flex min-w-0 items-center whitespace-pre tabular-nums" data-running-verb>
      <span className="min-w-0 truncate">{t("chat-runningstatusbar.background_workflow")}</span>
      {/* not `truncate`: its nowrap would drop the space the piece starts with */}
      {w.phase && <span className="max-w-[40%] shrink-0 overflow-hidden text-ellipsis">{` · ${w.phase}`}</span>}
      <span className="shrink-0" data-running-counts>
        {" · "}
        <Rolling value={workflowCounts(w)} />
      </span>
      {line.more > 0 && <span className="shrink-0" data-running-more>{` · ${t("chat-runningstatusbar.more_workflows", { count: line.more })}`}</span>}
    </span>
  );
}

type Phase = "on" | "linger" | "fading" | "off";

/** The bar for a given status line (RunningStatusBar subscribes; tests render this). `clock: false`: no time, the
 *  chat's live work line shows it. */
export function RunningStatusView({ live, working, startedAt, clock = true, agents, background, subtasks, offline }: { live: SessionLive | null; working: boolean; startedAt?: string; clock?: boolean; agents?: AgentsWorking | null; background?: BackgroundLine | null; subtasks?: boolean; offline?: boolean }) {
  const phase = usePhase(working);
  // after the turn: what it last said, while it lingers and fades
  const last = useRef(live);
  if (working) last.current = live;
  const shown = working ? live : last.current;
  const lastBackground = useRef(background);
  if (working) lastBackground.current = background;
  const bg = working ? background : lastBackground.current;
  // and whether it had the time: the work line's clock goes with the turn, the bar does not take it up as it fades
  const hadClock = useRef(clock);
  if (working) hadClock.current = clock;
  const ownClock = working ? clock : hadClock.current;
  const behind = inBackground(bg, shown, ownClock, working ? agents : null);
  const lastSubtasks = useRef(subtasks);
  if (working) lastSubtasks.current = subtasks;
  // the turn is over and background subagents run on: neither the turn's time nor its tokens say anything of them
  const usage = Boolean(working ? subtasks : lastSubtasks.current);
  // The workflow has a clock of its own, which no work line shows (the hub says the turn is over: it is drawn finished)
  const showClock = behind || (ownClock && !usage);
  const now = useNow(1000, phase === "on" && !offline && !usage);
  // from the run's start; a run that does not say when it started, from the turn's (not from when the bar appeared)
  const elapsed = useElapsed(behind ? null : shown, behind ? (bg.since ?? startedAt) : startedAt, now);
  const tokens = useCountUp(shown?.tokens_down ?? 0);
  const rate = tokenRate(shown?.tokens_down, elapsed);
  const breath = useBreath(shown?.tokens_down, phase === "on");
  if (phase === "off") return null;
  return (
    <div
      data-running-status={phase}
      data-running-background={behind ? "workflow" : usage ? "subtasks" : undefined}
      className="flex select-none items-center justify-between gap-3 px-2 py-1.5 text-[13px] font-medium"
      style={{ opacity: phase === "fading" ? 0 : 1, transition: `opacity ${FADE_OUT_MS}ms ease-out` }}
    >
      <div key={breath.cycle} onAnimationEnd={breath.ended} className={`flex min-w-0 items-center gap-1.5 ${behind ? "text-ai" : "text-status-accent"} ${breath.cycle ? "status-breath" : ""}`}>
        <Icon name={behind ? "workflow" : "sparkles"} size={14} className="shrink-0 -translate-y-px" />
        {offline && <Disconnected />}
        {behind ? (
          <BackgroundWords line={bg} />
        ) : (
          <span className="truncate" data-running-verb>
            {usage ? t("chat-runningstatusbar.background_subtasks") : statusText(shown, working ? agents : null)}
          </span>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-ink-3 tabular-nums">
        {showClock && <span data-running-elapsed>{duration(elapsed * 1000)}</span>}
        {!behind && !usage && shown?.tokens_down !== undefined && (
          <>
            {showClock && <span aria-hidden>·</span>}
            <span data-running-tokens>↓ {tokenCount(tokens)} tokens</span>
            {rate !== undefined && (
              <>
                <span aria-hidden>·</span>
                <span data-running-rate>{t("chat-runningstatusbar.tokens_per_second", { n: rate })}</span>
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
}

/** on while working; after it, a second as it was, then the fade, then gone. */
function usePhase(working: boolean): Phase {
  const [phase, setPhase] = useState<Phase>(working ? "on" : "off");
  useEffect(() => {
    if (working) {
      setPhase("on");
      return;
    }
    if (phase === "off") return;
    const a = window.setTimeout(() => setPhase("fading"), LINGER_MS);
    const b = window.setTimeout(() => setPhase("off"), LINGER_MS + FADE_OUT_MS);
    return () => {
      clearTimeout(a);
      clearTimeout(b);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [working]);
  return working ? "on" : phase === "on" ? "linger" : phase;
}

/**
 * Seconds the turn has run: the status line's count, going on locally between its updates (about one a second);
 * without one, from `startedAt` (or from when the bar appeared).
 */
function useElapsed(live: SessionLive | null, startedAt: string | undefined, now: number): number {
  const base = useRef<{ s: number; at: number } | null>(null);
  const mounted = useRef(Date.now());
  if (live?.elapsed_s === undefined) base.current = null;
  else if (base.current?.s !== live.elapsed_s) base.current = { s: live.elapsed_s, at: Date.now() };
  if (base.current) return base.current.s + Math.max(0, Math.floor((now - base.current.at) / 1000));
  const from = startedAt ? Date.parse(startedAt) : NaN;
  return Math.max(0, Math.floor((now - (Number.isNaN(from) ? mounted.current : from)) / 1000));
}
