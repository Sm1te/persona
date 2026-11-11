// A session's side panel comes back with the session (#5150, docs/web/web.md "Side panels"). Moving to another page closes
// a session's Files / Git / file panel (App); coming back to that session (the sidebar, the browser's Back) opens it
// again where it was: the same kind, the Files panel's folder and the file open in it. What is kept is the panel open
// when the page left the session, so one the user closed (×, Esc, ⌘B, its button) before leaving is not brought back,
// and one brought back is kept again only if it is still open when the page leaves. Kept per browser tab
// (sessionStorage), for the last few sessions. A Persona chat keeps its panel the same way, and it is any panel but a
// link (a task's or an agent's chat, the plan, a file): opened from that chat, it goes and comes back with it (#6199).
import { useEffect, useRef } from "react";
import { closePanel, directoryPanelKey, getPanel, openPanel, type Panel } from "./panel";

/** The panels that belong to the session the page shows (the ones App closes when the page moves on). */
export type SessionPanel = Extract<Panel, { kind: "files" | "git" | "review" | "file" | "preview" }>;

/** Where a Files panel is: the folder it shows and the file open in it (FilesPanel tells, `notePlace`). */
export interface Place {
  path?: string;
  file?: string;
}

export type PanelMemory = Record<string, Panel>;

const STORE = "persona.panel.back";
/** How many sessions' panels are kept: the ones gone back and forth between, not every one ever visited. */
export const KEPT = 20;

/** The session a pane is, as the memory keys it. */
export function sessionKey(pane: { mid: string; sid: string }): string {
  return `${pane.mid}/${pane.sid}`;
}

/** A Persona chat, as the memory keys it (its sid; "" for /persona with none). */
export function chatKey(sid: string): string {
  return `persona:${sid}`;
}

/** Whether `p` is a panel of the session `pane` that the page closes on leaving it (App's rule). */
export function belongsTo(p: Panel | null, pane: { mid: string; sid: string } | null | undefined): p is SessionPanel {
  if (!p || !pane) return false;
  if (p.kind === "file") return true; // a file opened from this conversation (its links, its tool rows)
  return (p.kind === "files" || p.kind === "git" || p.kind === "review" || p.kind === "preview") && p.mid === pane.mid && p.sid === pane.sid;
}

/** The memory after the page left `session` with `panel` open (null: none of its own): kept, or forgotten. */
export function leaving(memory: PanelMemory, session: string, panel: Panel | null, place?: Place): PanelMemory {
  const { [session]: _, ...rest } = memory;
  if (!panel) return rest;
  const kept: Panel = panel.kind === "files" && place ? { ...panel, path: place.path, file: place.file } : panel;
  const out = { ...rest, [session]: kept };
  const keys = Object.keys(out);
  return keys.length > KEPT ? Object.fromEntries(keys.slice(keys.length - KEPT).map((k) => [k, out[k]])) : out;
}

/** Coming back to `session` with `open` showing: the panel to open (and the memory without it), or none. A panel that
 * is already open (a link, a task: they stay across pages) is not replaced; the session's is kept for the next time. */
export function arriving(memory: PanelMemory, session: string, open: Panel | null): { memory: PanelMemory; back: Panel | null } {
  const back = memory[session];
  if (!back || open) return { memory, back: null };
  const { [session]: _, ...rest } = memory;
  return { memory: rest, back };
}

// The kinds a panel can be now: one kept by an older version of the page (the task and Agent panels before #5096) is
// not brought back.
const KINDS = new Set<Panel["kind"]>(["files", "git", "review", "file", "link", "plan", "preview"]);

function load(): PanelMemory {
  try {
    const v = JSON.parse(sessionStorage.getItem(STORE) ?? "{}");
    if (!v || typeof v !== "object" || Array.isArray(v)) return {};
    return Object.fromEntries(Object.entries(v as Record<string, Panel>).filter(([, p]) => p && typeof p === "object" && KINDS.has(p.kind)));
  } catch {
    return {};
  }
}

function save(m: PanelMemory) {
  try {
    if (Object.keys(m).length) sessionStorage.setItem(STORE, JSON.stringify(m));
    else sessionStorage.removeItem(STORE);
  } catch {
    // private mode / full: the panel just does not come back
  }
}

const places = new Map<string, Place>();

/** A Files panel says where it is now (its folder, the file open in it), so leaving keeps that. */
export function notePlace(panel: { mid: string; sid: string; cwd: string }, place: Place) {
  places.set(directoryPanelKey(panel), place);
}

/** On each page: leaving a session or a Persona chat keeps its open panel (and closes it, with the other session
 * panels App closed before), coming back to one opens what it kept. `pane` is the session the page shows, if any;
 * `chat` the Persona chat's sid (undefined: not a Persona chat). */
export function useSessionPanels(at: string, pane: { mid: string; sid: string } | null | undefined, chat?: string) {
  const was = useRef<{ key: string; pane: { mid: string; sid: string } | null } | null>(null);
  useEffect(() => {
    const p = getPanel();
    const left = was.current;
    const key = pane ? sessionKey(pane) : chat !== undefined ? chatKey(chat) : null;
    was.current = key ? { key, pane: pane ? { mid: pane.mid, sid: pane.sid } : null } : null;
    let memory = load();
    let own: Panel | null = null;
    if (left && left.key !== key) {
      own = left.pane ? (belongsTo(p, left.pane) ? p : null) : p && p.kind !== "link" ? p : null;
      memory = leaving(memory, left.key, own, own?.kind === "files" ? places.get(directoryPanelKey(own)) : undefined);
    }
    // a session's terminal / files / Git belong to that conversation: moving to another page closes them (a link stays,
    // unless a Persona chat being left owns it)
    if (p && (own || (p.kind !== "link" && !(p.kind !== "file" && pane && p.mid === pane.mid && p.sid === pane.sid)))) closePanel();
    if (key) {
      const r = arriving(memory, key, getPanel());
      memory = r.memory;
      if (r.back) openPanel(r.back);
    }
    save(memory);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [at]);
}
