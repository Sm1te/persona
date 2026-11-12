// A host tab's team (#5180, after cindy's Team; docs/web/web.md "Team"). In a herdr tab with several Agents the lead is the
// Agent in the tab's first pane (the one the others were split from), and the tab's other running Agents are its workers: a
// Claude Code agent team's teammates, or panes split off the lead. The hub's manager folds the workers under their lead
// (`folded_under`, `folded_as: "worker"`, docs/protocol/protocol.md "Session hierarchy"), so no list shows or counts them on
// their own (lib/folded-rows.ts). The lead's page has them in its Team side panel (views/chat/TeamPanel.tsx): a strip of the
// workers above the chosen one's conversation. This module says who is a worker, which one the panel shows, and when the
// panel opens or closes by itself (cindy's timing, `teamReveal`).
import { useEffect, useRef, useSyncExternalStore } from "react";
import { stateOf } from "./format";
import { paneOrder } from "./herdr-tree";
import { getPanel, openPanel, usePanel, type Panel } from "./panel";
import { NARROW, useMedia } from "./viewport";
import type { Session } from "./types";

/** A lead's worker: an Agent of its tab folded under it (#5180). */
export const isWorker = (s: { folded_as?: string }): boolean => s.folded_as === "worker";

/** How many workers a lead has (its rollup's `workers`); 0 for any other session and while the row is not in. */
export const workerCount = (s: Session | undefined): number => s?.children?.workers ?? 0;

/** The Team panel, when it is `mid/sid`'s (the lead's). */
export type TeamPanel = Extract<Panel, { kind: "team" }>;
export function teamPanelOf(p: Panel | null, mid: string, sid: string): TeamPanel | undefined {
  return p?.kind === "team" && p.mid === mid && p.sid === sid ? p : undefined;
}

/** The workers among the rows folded under a lead, in pane order. A worker that stops is not folded any more (the
 *  manager folds only running ones): it leaves the strip and is a row of its own again. */
export function teamWorkers(rows: readonly Session[]): Session[] {
  return rows.filter((s) => isWorker(s) && stateOf(s) !== "offline").sort(paneOrder);
}

// the worker picked last on each lead's strip (this browser tab), so the panel opens on it again
const picked = new Map<string, string>();
const leadKey = (mid: string, sid: string) => `${mid}/${sid}`;

/** The worker the panel shows: the one asked for (a link, a pick), else the one picked last for this lead, else the first
 *  that needs you, else the first; undefined without workers. The lead's page and the panel read the same answer. */
export function shownWorker(workers: readonly Session[], mid: string, lead: string, asked?: string): Session | undefined {
  const find = (id: string | undefined) => (id ? workers.find((w) => w.session_id === id) : undefined);
  return find(asked) ?? find(picked.get(leadKey(mid, lead))) ?? workers.find((w) => stateOf(w) === "needs_you") ?? workers[0];
}

/** Shows `worker` in its lead's Team panel (a pill picked on the strip), opening the panel if it is not. */
export function showWorker(mid: string, lead: string, worker: string) {
  picked.set(leadKey(mid, lead), worker);
  openPanel({ kind: "team", mid, sid: lead, worker });
}

// ── what was decided for a lead's panel in this browser: cindy's per-task record of the right sidebar's collapsed state ──
const RECORD = "persona.team.panel";
/** How many leads' choices are kept: the last ones opened or closed. */
export const RECORDS_KEPT = 50;
export type TeamRecord = "open" | "closed";

function records(): Record<string, TeamRecord> {
  try {
    const v = JSON.parse(localStorage.getItem(RECORD) ?? "{}");
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, TeamRecord>) : {};
  } catch {
    return {};
  }
}

/** The lead's panel was left open or closed by hand (or opened by itself) in this browser; undefined: never. */
export function teamRecord(mid: string, sid: string): TeamRecord | undefined {
  const v = records()[leadKey(mid, sid)];
  return v === "open" || v === "closed" ? v : undefined;
}

export function recordTeam(mid: string, sid: string, v: TeamRecord) {
  const { [leadKey(mid, sid)]: _, ...rest } = records();
  const all = { ...rest, [leadKey(mid, sid)]: v };
  const keys = Object.keys(all);
  try {
    localStorage.setItem(RECORD, JSON.stringify(Object.fromEntries(keys.slice(Math.max(0, keys.length - RECORDS_KEPT)).map((k) => [k, all[k]]))));
  } catch {
    // private mode / full: the panel opens by itself again next time
  }
}

// ── a link to a worker (App: `/s/<mid>/<worker>`, `/t/…/<worker>`, a worker's card): its lead's page, asked to show it ──
const asks = new Map<string, string>();
const askListeners = new Set<() => void>();
const tellAsks = () => askListeners.forEach((l) => l());

/** The lead's page is asked to open its Team panel on `worker` (a link to the worker); returns the lead, to open. */
export function askTeamWorker(mid: string, lead: string, worker: string): string {
  asks.set(leadKey(mid, lead), worker);
  tellAsks();
  return lead;
}

/** The worker the lead's page was asked to show, until it did (`doneTeamAsk`). */
export function useTeamAsk(mid: string, sid: string): string | undefined {
  const read = () => asks.get(leadKey(mid, sid));
  return useSyncExternalStore(
    (l) => {
      askListeners.add(l);
      return () => void askListeners.delete(l);
    },
    read,
    read,
  );
}

export function doneTeamAsk(mid: string, sid: string) {
  if (asks.delete(leadKey(mid, sid))) tellAsks();
}

/** What the lead's page does with its Team panel (cindy, `CCAgentSessionView`'s Team effects and `orcaPassiveReveal`):
 *  - asked to show a worker (a link to it): open on it, at any width;
 *  - arriving at a lead with workers (`before` undefined: the page has not seen this lead's count yet): open by itself on a
 *    wide screen unless the panel was closed here before (no record: the first time, which records it open);
 *  - its first worker appearing while the page is on the lead (the team just started, a new intent): open on a wide screen,
 *    even if it was closed before;
 *  - its last worker gone: close.
 *  On a narrow screen the panel covers the page, as every side panel does there, so it opens by itself only for a link. */
export function teamReveal(r: { workers: number; before: number | undefined; record: TeamRecord | undefined; wide: boolean; asked: boolean }): "open" | "close" | undefined {
  if (r.workers === 0) return r.before !== undefined && r.before > 0 ? "close" : undefined;
  if (r.asked) return "open";
  if (!r.wide) return undefined;
  if (r.before === undefined) return r.record === "closed" ? undefined : "open";
  return r.before === 0 ? "open" : undefined;
}

/** How long a lead's worker count stays at 0 before the team counts as ended (its panel closes, and workers coming back
 *  are a new team): a list without the lead's pane, or the lead offline for a push, dissolves the fold for a moment. */
export const TEAM_GONE_MS = 4000;

/** The lead's page's side of the Team panel: opens and closes it with cindy's timing (`teamReveal`), and keeps what the
 *  user did with it (closed with ×, Esc, ⌘B or the header's Team button while on the lead: kept closed until the team
 *  starts again; opened: kept open). `workers`: how many the lead has (its row's rollup); `session` undefined while the row
 *  is not in (nothing is decided until it is). */
export function useTeamReveal(mid: string, sid: string, session: Session | undefined, enabled: boolean) {
  const workers = enabled ? workerCount(session) : 0;
  const known = enabled && Boolean(session);
  const asked = useTeamAsk(mid, sid);
  const wide = !useMedia(NARROW);
  const panel = usePanel();
  // the count the page last saw for this lead (undefined: it just arrived, or the row is not in yet), and when the page
  // arrived at it: a 0 seen in the first moment is no "team not started yet" (a row can come first without its rollup)
  const seen = useRef<{ lead: string; workers: number; since: number } | undefined>(undefined);
  // the panel this hook closed itself (the team ended): not the user's choice, so not remembered as closed
  const closedItself = useRef(false);
  const gone = useRef(0);
  useEffect(() => () => clearTimeout(gone.current), []);
  useEffect(() => {
    clearTimeout(gone.current);
    if (!known) return;
    const lead = leadKey(mid, sid);
    const now = Date.now();
    const last = seen.current?.lead === lead ? seen.current : undefined;
    const since = last?.since ?? now;
    const before = last && !(last.workers === 0 && now - last.since < TEAM_GONE_MS) ? last.workers : undefined;
    if (asked) doneTeamAsk(mid, sid);
    // the last worker gone: the team ends (the panel closes) only once none came back for a moment
    if (workers === 0 && before !== undefined && before > 0) {
      gone.current = window.setTimeout(() => {
        seen.current = { lead, workers: 0, since: 0 };
        if (!teamPanelOf(getPanel(), mid, sid)) return;
        closedItself.current = true;
        openPanel(null);
      }, TEAM_GONE_MS);
      return;
    }
    seen.current = { lead, workers, since };
    if (teamReveal({ workers, before, record: teamRecord(mid, sid), wide, asked: Boolean(asked) }) !== "open") return;
    // after the page's other effects: App keeps the panel of the session being left and may bring back this lead's own
    // (lib/panel-memory.ts), which an arrival's open does not replace; a link or a team starting does
    const arriving = !asked && before === undefined;
    queueMicrotask(() => {
      const shown = getPanel();
      const open = teamPanelOf(shown, mid, sid);
      if (arriving && shown && !open) return;
      recordTeam(mid, sid, "open");
      if (!open || (asked && open.worker !== asked)) openPanel({ kind: "team", mid, sid, worker: asked ?? open?.worker });
    });
    // the panel's own state is decided once per arrival and count: a later resize or panel change does not reopen it
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [known, mid, sid, workers, asked]);
  // closed by hand while the page is on the lead: remembered, so the next visit does not open it by itself
  const was = useRef<Panel | null>(null);
  useEffect(() => {
    const before = was.current;
    was.current = panel;
    const itself = closedItself.current;
    closedItself.current = false;
    if (teamPanelOf(before, mid, sid) && !panel && known && !itself) recordTeam(mid, sid, "closed");
  }, [panel, mid, sid, known]);
}
