// The Sessions and Terminal page's list (#4754, docs/web/web.md "Pages", **Terminal**): a switch at its top between the
// sessions and **Terminals**, every plain terminal of every computer (computer → space → tab → pane) at full height.
// Which one shows is the address's `?view=sessions|terminals`, else the one this browser chose last, else the sessions.
// Also "Close finished terminals": the terminals stopped Agents left, previewed, confirmed, then closed. Pure, tested
// without a DOM.
import { api, enc } from "./api";
import type { TermSpace } from "./terminals";

export type TerminalListView = "sessions" | "terminals";

const KEY = "persona.terminal.list";

type Store = Pick<Storage, "getItem" | "setItem">;

export function parseListView(v: string | null | undefined): TerminalListView | undefined {
  return v === "sessions" || v === "terminals" ? v : undefined;
}

/** The list this browser showed last; the sessions when it never chose. */
export function readListView(store: Store = localStorage): TerminalListView {
  try {
    return parseListView(store.getItem(KEY)) ?? "sessions";
  } catch {
    return "sessions";
  }
}

export function rememberListView(view: TerminalListView, store: Store = localStorage) {
  try {
    store.setItem(KEY, view);
  } catch {
    // private mode, storage full: the choice lasts the page
  }
}

/** The list to draw: the sessions while a session is open beside it, or when the hub cannot list terminals; else the
 *  address's, else the one remembered. */
export function resolveListView(url: TerminalListView | undefined, remembered: TerminalListView, o: { panes: boolean; session: boolean }): TerminalListView {
  if (!o.panes || o.session) return "sessions";
  return url ?? remembered;
}

/** How many of a computer's panes are terminals stopped Agents left (`agent_session`). */
export function finishedCount(spaces: readonly TermSpace[]): number {
  return spaces.reduce((n, sp) => n + sp.tabs.reduce((m, tab) => m + tab.panes.filter((p) => p.agentSession).length, 0), 0);
}

// ── Close finished terminals (`POST /machines/{mid}/panes/close_finished`, docs/protocol/protocol.md) ──

export interface FinishedPane {
  pane: string;
  session_id: string;
  tab_id?: string;
  workspace_id?: string;
}
export interface KeptPane {
  pane: string;
  session_id: string;
  /** Why the node kept it (a program in front of the shell…), in the node's words: not shown. */
  why?: string;
}
export interface CloseFinishedResult {
  closed: FinishedPane[];
  kept: KeptPane[];
  preview: boolean;
}

type Post = (path: string, init: { method: "POST"; json: unknown }) => Promise<unknown>;
const post: Post = (path, init) => api(path, init);

function result(raw: unknown): CloseFinishedResult {
  const r = (raw ?? {}) as Partial<CloseFinishedResult>;
  return { closed: Array.isArray(r.closed) ? r.closed : [], kept: Array.isArray(r.kept) ? r.kept : [], preview: r.preview === true };
}

/** What "Close finished terminals" would close on `mid` now, closing nothing. */
export async function previewFinished(mid: string, call: Post = post): Promise<CloseFinishedResult> {
  return result(await call(`/machines/${enc(mid)}/panes/close_finished`, { method: "POST", json: { preview: true } }));
}

/** Closes exactly `panes` (the ones the user confirmed), each checked again on the computer. */
export async function closeFinished(mid: string, panes: readonly string[], call: Post = post): Promise<CloseFinishedResult> {
  return result(await call(`/machines/${enc(mid)}/panes/close_finished`, { method: "POST", json: { panes: panes.join(",") } }));
}
