// A lead's Team side panel (#5180, after cindy's Team tab, `OrcaWorkerPanel` / `WorkerListToolbar`; docs/web/web.md "Team"):
// a 32 px strip of the lead's workers (the other Agents of its herdr tab, lib/team.ts) as round pills, each with its status
// mark and title, the one shown filled; under it the shown worker's own conversation, the chat page's without its header,
// where it is read, answered, written to and stopped. The lead stays the page's chat.
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { sessionTitle, stateOf } from "../../lib/format";
import { agentLabel } from "../../lib/mentions";
import { STATE_LABEL, stateName } from "../../lib/state-labels";
import { getSession, useStore } from "../../lib/store";
import { awaitsReply } from "../../lib/agent-groups";
import { shownWorker, showWorker, teamWorkers, workerCount } from "../../lib/team";
import { useChildSessions } from "../../lib/use-child-sessions";
import type { Session } from "../../lib/types";
import { cx, Loading } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { PanelHeader } from "../../ui/PanelHeader";
import { StatusMark } from "../../ui/StatusMark";
import { t } from "../../i18n";
import { scrollBehavior } from "../../ui/stream/motion";
import { ChatView } from "./ChatView";

/** The pill an arrow key (or Home / End) moves to from `i` among `n`; undefined for any other key. */
export function nextTab(i: number, key: string, n: number): number | undefined {
  if (key === "ArrowRight") return (i + 1) % n;
  if (key === "ArrowLeft") return (i - 1 + n) % n;
  if (key === "Home") return 0;
  if (key === "End") return n - 1;
  return undefined;
}

/** How far one press of a strip's arrow scrolls it (cindy's `workerTabsScrollStep`): most of what is in view. */
export function stripStep(width: number): number {
  return Math.max(80, Math.floor(width * 0.7));
}

/** A pill's hover text: which Agent, its title and its state, as the pane tabs said it. */
export function workerHint(w: Session, awaiting: boolean): string {
  const st = stateOf(w);
  return [agentLabel(w), sessionTitle(w), awaiting && st === "idle" ? stateName("awaits_reply") : STATE_LABEL[st]].filter(Boolean).join(" · ");
}

export function TeamPanel({ mid, sid, worker, onClose }: { mid: string; sid: string; worker?: string; onClose: () => void }) {
  const lead = useStore(() => getSession(mid, sid));
  // the lead's folded rows (lib/use-child-sessions.ts): the store's, or on a paging hub one bounded read of them
  const kids = useChildSessions(mid, sid, workerCount(lead) > 0 || Boolean(worker));
  const workers = useMemo(() => teamWorkers(kids.rows), [kids.rows]);
  const shown = shownWorker(workers, mid, sid, worker);
  const running = workers.filter((w) => stateOf(w) === "working").length;
  const body = useId();
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PanelHeader icon="users" title={t("sidepanel.team")} subtitle={workers.length ? t("chat-teampanel.summary", { count: workers.length, running }) : undefined} onClose={onClose} />
      {workers.length > 0 && <WorkerStrip workers={workers} shown={shown?.session_id} controls={body} onPick={(w) => showWorker(mid, sid, w.session_id)} />}
      <div id={body} role={shown ? "tabpanel" : undefined} aria-label={shown ? sessionTitle(shown) : undefined} className="flex min-h-0 flex-1 flex-col">
        {shown ? (
          // the worker's conversation: its own chat, keyed by it, so switching pills starts it at its latest message
          <ChatView key={`${mid}/${shown.session_id}`} mid={mid} sid={shown.session_id} embedded />
        ) : !kids.loaded ? (
          <div className="flex flex-1 items-center justify-center"><Loading /></div>
        ) : (
          <div className="flex flex-1 items-center justify-center px-6 text-center text-[13px] text-ink-3">{t("chat-teampanel.waiting")}</div>
        )}
      </div>
    </div>
  );
}

/** cindy's tab layout of the worker list: one row of pills that scrolls sideways, with ‹ › at its ends while there is more
 *  that way and the edges fading; a mouse wheel scrolls it too. The shown pill is kept in view. Arrow keys, Home and End
 *  move between the pills as in any tab list. */
function WorkerStrip({ workers, shown, controls, onPick }: { workers: Session[]; shown?: string; controls: string; onPick: (w: Session) => void }) {
  const scroller = useRef<HTMLDivElement>(null);
  const pills = useRef<(HTMLButtonElement | null)[]>([]);
  const [more, setMore] = useState({ left: false, right: false });
  const at = Math.max(0, workers.findIndex((w) => w.session_id === shown));
  const mutes = useStore((s) => s.mutes);
  const measure = useCallback(() => {
    const el = scroller.current;
    if (!el) return;
    const max = el.scrollWidth - el.clientWidth;
    setMore((m) => {
      const next = { left: el.scrollLeft > 1, right: max - el.scrollLeft > 1 };
      return m.left === next.left && m.right === next.right ? m : next;
    });
  }, []);
  useEffect(() => {
    measure();
    const el = scroller.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [measure, workers.length]);
  useEffect(() => {
    pills.current[at]?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
    measure();
  }, [at, measure, workers.length]);
  const nudge = (dir: 1 | -1) => {
    const el = scroller.current;
    if (!el) return;
    el.scrollBy({ left: dir * stripStep(el.clientWidth), behavior: scrollBehavior() });
  };
  const arrow = (dir: 1 | -1) => {
    const on = dir < 0 ? more.left : more.right;
    const label = t(dir < 0 ? "chat-teampanel.scroll_left" : "chat-teampanel.scroll_right");
    // the arrows' places stay when they hide, so the strip does not shift as its ends are reached
    return (
      <span className="flex h-6 w-5 shrink-0 items-center justify-center">
        {on && (
          <button type="button" aria-label={label} title={label} tabIndex={-1} onClick={() => nudge(dir)} className="flex h-6 w-5 items-center justify-center rounded-full text-ink-3 hover:text-ink">
            <Icon name={dir < 0 ? "chevronLeft" : "chevronRight"} size={14} />
          </button>
        )}
      </span>
    );
  };
  return (
    <div className="relative flex h-8 shrink-0 items-center border-b border-line px-1.5">
      {arrow(-1)}
      <div
        ref={scroller}
        role="tablist"
        aria-label={t("chat-teampanel.strip_label")}
        onScroll={measure}
        onWheel={(e) => {
          const el = e.currentTarget;
          if (Math.abs(e.deltaY) <= Math.abs(e.deltaX) || el.scrollWidth <= el.clientWidth) return;
          el.scrollLeft += e.deltaY;
          measure();
        }}
        className="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto overscroll-x-contain [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
      >
        {workers.map((w, i) => {
          const on = i === at;
          const st = stateOf(w);
          const awaiting = awaitsReply(w, mutes);
          return (
            <button
              key={w.session_id}
              ref={(el) => {
                pills.current[i] = el;
              }}
              type="button"
              role="tab"
              aria-selected={on}
              aria-controls={controls}
              tabIndex={on ? 0 : -1}
              title={workerHint(w, awaiting)}
              onClick={() => !on && onPick(w)}
              onKeyDown={(e) => {
                const to = nextTab(i, e.key, workers.length);
                if (to === undefined) return;
                e.preventDefault();
                pills.current[to]?.focus();
                if (to !== i) onPick(workers[to]!);
              }}
              className={cx(
                "touch-hit inline-flex h-6 max-w-[220px] shrink-0 items-center gap-1.5 rounded-full border px-2 text-[11px] leading-none transition-colors duration-150",
                on ? "border-ink bg-ink text-surface" : "border-line bg-field text-ink hover:bg-hover",
              )}
            >
              <StatusMark state={st} awaiting={awaiting} size="sm" label={false} />
              <span className="min-w-0 truncate font-medium">{sessionTitle(w)}</span>
            </button>
          );
        })}
      </div>
      {/* the ends fade while there is more that way, as cindy's strip does */}
      {more.left && <span aria-hidden className="pointer-events-none absolute inset-y-0 left-6.5 w-4 bg-linear-to-r from-page to-transparent" />}
      {more.right && <span aria-hidden className="pointer-events-none absolute inset-y-0 right-6.5 w-4 bg-linear-to-l from-page to-transparent" />}
      {arrow(1)}
    </div>
  );
}
