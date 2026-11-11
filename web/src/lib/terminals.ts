// The terminal browser's tree (docs/integrations/terminal.md "Standalone terminal"): what a machine's node lists (`GET /machines/{mid}/panes`:
// workspaces, tabs and panes of its terminal host, agent or plain shell) turned into the rows the page shows, plus the
// Jump-To filter and which terminals the page keeps alive. Pure, so it is tested without a DOM.
import { t } from "../i18n";
import { baseName, sessionTitle, shortPath, stateOf } from "./format";
import { agentSurface, type AgentSurface } from "./agent-kind";
import type { AgentState, Session } from "./types";

// ── what the hub sends (proto/panes.go) ──────────────────

export interface PaneSpaceDTO {
  id: string;
  label: string;
  number?: number;
  cwd?: string;
  branch?: string;
}
export interface PaneTabDTO {
  id: string;
  workspace_id: string;
  label: string;
  number?: number;
}
export interface PaneDTO {
  pane: string;
  /** What the terminal stream calls the pane ("pane:<pane>"). */
  terminal: string;
  tab_id: string;
  workspace_id: string;
  /** The kind of agent running there; absent for a plain shell. */
  agent?: string;
  /** The host's status for the agent (idle / working / blocked / done…). */
  status?: string;
  title?: string;
  cwd?: string;
  /** An agent session of this machine runs in the pane. */
  session_id?: string;
  /** A plain pane Persona opened for an Agent that has stopped since (#4754): that Agent's session. "Close finished
   *  terminals" closes these. Older nodes never send it. */
  agent_session?: string;
  /** The program in front of a plain pane ("zsh" at its prompt, "vim", "npm"…), as the host says; older nodes never send it. */
  foreground?: string;
}
export interface PaneHostDTO {
  kind: string;
  stream: boolean;
  control: boolean;
  new_tab: boolean;
  /** The host can make a Git worktree of a workspace's repository (an older node does not say). */
  worktree?: boolean;
  workspaces: PaneSpaceDTO[];
  tabs: PaneTabDTO[];
  panes: PaneDTO[];
}
export interface PanesResponse {
  hosts: PaneHostDTO[];
}

// ── the tree ─────────────────────────────────────────────

/** A pane's badge: an agent's state, or none for a plain shell. */
export type PaneState = AgentState | "shell";

export interface TermPane {
  /** The id the terminal opens by: the agent's session id when it has one (it shares that session's stream), else the pane's address. */
  id: string;
  title: string;
  /** The line under the title: the agent, the directory. */
  detail: string;
  agent: string;
  /** The client its agent runs in, from the session in the pane (#6390): the command line's mark carries </>. */
  surface?: AgentSurface;
  /** The pane's working directory ("" when the host does not say). */
  cwd: string;
  state: PaneState;
  /** The host's own id for the pane ("w1:p2"), what the menu's actions name; `id` is what the terminal opens by. */
  pane: string;
  /** The agent session in the pane, "" for a plain shell. */
  sessionId: string;
  /** The stopped Agent whose terminal this plain pane is (#4754), "" for any other pane. */
  agentSession: string;
  /** What the filter matches: everything the row shows, and the names around it. */
  haystack: string;
}
export interface TermTab {
  id: string;
  label: string;
  panes: TermPane[];
}
export interface TermSpace {
  id: string;
  /** The host kind the workspace belongs to: "New terminal" says which one. */
  host: string;
  label: string;
  branch: string;
  cwd: string;
  /** A shell can be opened here. */
  canOpen: boolean;
  /** "New worktree…" can be offered on this workspace (#2431). */
  canWorktree?: boolean;
  tabs: TermTab[];
}

const HOST_STATUS: Record<string, AgentState> = { working: "working", blocked: "needs_you", done: "unread", idle: "idle" };

/** An agent pane's state: the session's own (it knows unread and pending answers) else what the host says. */
export function paneState(p: PaneDTO, session: Session | undefined): PaneState {
  if (session) return stateOf(session);
  if (!p.agent) return "shell";
  return HOST_STATUS[p.status ?? ""] ?? "unknown";
}

/** A pane's row: an agent by its session's title, a shell by what the terminal says (its title, else its directory). A
 *  shell a stopped Agent left (#4754) is "Finished: <that Agent's title>" (`finished`: its session, when this computer
 *  lists it; else a short id). A shell's line under it starts with the program in front of it, when the host says. */
export function paneRow(p: PaneDTO, session: Session | undefined, finished?: Session): Pick<TermPane, "title" | "detail"> {
  const dir = shortPath(p.cwd);
  if (session) return { title: sessionTitle(session), detail: [p.agent, dir].filter(Boolean).join(" · ") };
  if (p.agent) return { title: p.title?.trim() || p.agent, detail: [p.agent, dir].filter(Boolean).join(" · ") };
  const front = p.foreground?.trim() ?? "";
  if (p.agent_session) {
    const name = finished ? sessionTitle(finished) : p.agent_session.slice(0, 8);
    return { title: t("lib-terminals.finished", { title: name }), detail: [front, dir].filter(Boolean).join(" · ") };
  }
  const title = p.title?.trim() || baseName(p.cwd) || t("lib-terminals.terminal");
  return { title, detail: [front === title ? "" : front, title === dir ? "" : dir].filter(Boolean).join(" · ") };
}

/** The id a pane opens by (see TermPane.id). */
export function terminalId(p: PaneDTO): string {
  return p.session_id || p.terminal;
}

const byNumber = <T extends { number?: number; id: string }>(a: T, b: T) => (a.number ?? 0) - (b.number ?? 0) || a.id.localeCompare(b.id, undefined, { numeric: true });

/** herdr numbers a tab's panes ("w9:p1", "w9:p2"); ids with no number go last. */
function paneNumber(p: PaneDTO): number {
  const m = p.pane.match(/:p(\d+)$/);
  return m ? Number(m[1]) : Number.MAX_SAFE_INTEGER;
}

/** A machine's workspaces → tabs → panes. `sessions` are that machine's agent sessions (their state, their title). */
export function buildTerminalTree(res: PanesResponse | undefined, sessions: readonly Session[]): TermSpace[] {
  const out: TermSpace[] = [];
  const byId = new Map(sessions.map((s) => [s.session_id, s]));
  for (const host of res?.hosts ?? []) {
    const tabsOf = new Map<string, PaneTabDTO[]>();
    for (const t of host.tabs ?? []) tabsOf.set(t.workspace_id, [...(tabsOf.get(t.workspace_id) ?? []), t]);
    const panesOf = new Map<string, PaneDTO[]>();
    for (const p of host.panes ?? []) panesOf.set(p.tab_id, [...(panesOf.get(p.tab_id) ?? []), p]);
    const known = new Set((host.workspaces ?? []).map((w) => w.id));
    // panes whose workspace the host did not list still show: under a workspace of their own id
    const orphans = [...new Set((host.panes ?? []).filter((p) => !known.has(p.workspace_id)).map((p) => p.workspace_id))];
    const spaces: PaneSpaceDTO[] = [...[...(host.workspaces ?? [])].sort(byNumber), ...orphans.map((id) => ({ id, label: id }))];
    for (const w of spaces) {
      const tabDTOs = [...(tabsOf.get(w.id) ?? [])].sort(byNumber);
      const listed = new Set(tabDTOs.map((t) => t.id));
      const strays = [...new Set((host.panes ?? []).filter((p) => p.workspace_id === w.id && !listed.has(p.tab_id)).map((p) => p.tab_id))];
      const tabs: TermTab[] = [...tabDTOs, ...strays.map((id) => ({ id, workspace_id: w.id, label: id }))]
        .map((t) => ({
          id: t.id,
          label: t.label || t.id,
          // a tab id is unique only within its workspace, and a host with no tabs (botmux) leaves it empty on every pane
          panes: (panesOf.get(t.id) ?? [])
            .filter((p) => p.workspace_id === w.id)
            .sort((a, b) => paneNumber(a) - paneNumber(b) || a.pane.localeCompare(b.pane))
            .map((p): TermPane => {
              const session = p.session_id ? byId.get(p.session_id) : undefined;
              const row = paneRow(p, session, p.agent_session ? byId.get(p.agent_session) : undefined);
              const label = t.label || t.id;
              return {
                id: terminalId(p),
                ...row,
                agent: p.agent ?? "",
                surface: agentSurface(session?.agent_surface),
                cwd: p.cwd ?? "",
                state: paneState(p, session),
                pane: p.pane,
                sessionId: p.session_id ?? "",
                agentSession: p.agent_session ?? "",
                haystack: [row.title, row.detail, p.cwd, label, w.label, w.branch].join(" ").toLowerCase(),
              };
            }),
        }))
        .filter((t) => t.panes.length > 0);
      if (tabs.length === 0 && !host.new_tab) continue;
      out.push({ id: w.id, host: host.kind, label: w.label || w.id, branch: w.branch ?? "", cwd: w.cwd ?? "", canOpen: host.new_tab, canWorktree: host.worktree === true, tabs });
    }
  }
  return out;
}

/** Jump To: the panes whose row or surroundings (tab, workspace, branch, directory, machine) hold every word. */
export function filterTerminalTree(spaces: readonly TermSpace[], query: string, machine = ""): TermSpace[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [...spaces];
  const out: TermSpace[] = [];
  for (const sp of spaces) {
    const tabs = sp.tabs
      .map((t) => ({ ...t, panes: t.panes.filter((p) => words.every((w) => `${p.haystack} ${machine.toLowerCase()}`.includes(w))) }))
      .filter((t) => t.panes.length > 0);
    if (tabs.length) out.push({ ...sp, tabs });
  }
  return out;
}

/** Every pane of a tree in the order it is shown (what ↑ ↓ walk). */
export function paneIds(spaces: readonly TermSpace[]): string[] {
  return spaces.flatMap((sp) => sp.tabs.flatMap((t) => t.panes.map((p) => p.id)));
}

/** The pane's place for a header: workspace › tab · title. */
export function paneTrail(spaces: readonly TermSpace[], id: string): string {
  for (const sp of spaces)
    for (const t of sp.tabs) {
      const p = t.panes.find((x) => x.id === id);
      if (p) return [sp.label, t.label === sp.label ? "" : t.label, p.title !== t.label ? p.title : ""].filter(Boolean).join(" › ");
    }
  return "";
}

/** The pane's working directory, "" when unknown. */
export function paneCwd(spaces: readonly TermSpace[], id: string): string {
  for (const sp of spaces)
    for (const t of sp.tabs) {
      const p = t.panes.find((x) => x.id === id);
      if (p) return p.cwd;
    }
  return "";
}

// ── which terminals stay open ────────────────────────────

/** A terminal the page holds: a machine and the id it opens by. */
export interface OpenTerminal {
  mid: string;
  id: string;
}

/** Terminals kept alive per machine: the one on screen and the one before it, so going back is instant. */
export const KEEP_PER_MACHINE = 2;

export const sameTerminal = (a: OpenTerminal | undefined, b: OpenTerminal | undefined) => Boolean(a && b && a.mid === b.mid && a.id === b.id);

/** `open` goes first; of each machine only the most recent `keep` stay (the rest are closed, which gives the pane back). */
export function keepRecent(list: readonly OpenTerminal[], open: OpenTerminal, keep = KEEP_PER_MACHINE): OpenTerminal[] {
  const out: OpenTerminal[] = [open];
  const per = new Map<string, number>([[open.mid, 1]]);
  for (const t of list) {
    if (sameTerminal(t, open)) continue;
    const n = per.get(t.mid) ?? 0;
    if (n >= keep) continue;
    per.set(t.mid, n + 1);
    out.push(t);
  }
  return out;
}

/** The page went to the background: only what is on screen stays open. */
export function keepOnlyShown(list: readonly OpenTerminal[], shown: OpenTerminal | undefined): OpenTerminal[] {
  return list.filter((t) => sameTerminal(t, shown));
}

/** A terminal id as the address carries it ("pane:w1:p2" holds colons: encoded). */
export function isPaneAddress(id: string): boolean {
  return id.startsWith("pane:");
}

/** Only the plain shells of a tree (the sidebar lists the agents already): tabs and workspaces left with none are dropped. */
export function shellsOnly(spaces: readonly TermSpace[]): TermSpace[] {
  const out: TermSpace[] = [];
  for (const sp of spaces) {
    const tabs = sp.tabs.map((t) => ({ ...t, panes: t.panes.filter((p) => p.state === "shell") })).filter((t) => t.panes.length > 0);
    if (tabs.length) out.push({ ...sp, tabs });
  }
  return out;
}
