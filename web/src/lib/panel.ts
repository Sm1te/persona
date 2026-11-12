// The page's right-hand panel: a session's files / Git, or a link opened in the page (docs/web/web.md "Side panel"). A
// session's terminal is the page's own view now (lib/session-view.ts), not a panel.
// One panel at a time; opening another replaces it. Session panels close when the page moves to another conversation;
// a link stays until closed (it is not tied to the conversation it came from).
import { useSyncExternalStore } from "react";
import type { DiffScope } from "./comments";

/** A diff opened straight at a line (a comment's card, lib/comments.ts): which diff, which file, which line. */
export interface GitSpot {
  /** The repository-relative path git reports. */
  path: string;
  diff: DiffScope;
  line: number;
  /** The last line of the range, when it is one. */
  to?: number;
  /** The line number is the old file's. */
  old?: boolean;
}

export type Panel =
  /** `path`: the folder to open at; `file`: a file open in it (a panel brought back, lib/panel-memory.ts). */
  | { kind: "files"; mid: string; sid: string; cwd: string; path?: string; file?: string }
  | { kind: "git"; mid: string; sid: string; cwd: string; at?: GitSpot }
  /** Reviewing the session's changes (#151): `scope` to open at, `at` a comment's file and line to go to. */
  | { kind: "review"; mid: string; sid: string; cwd: string; scope?: "uncommitted" | "branch" | "commit"; at?: { path: string; line: number; old?: boolean } }
  /** `sid`: the session whose message box takes the comments made in it (else the one last used). */
  | { kind: "file"; mid: string; path: string; cwd?: string; line?: number; to?: number; sid?: string }
  /** `manager`: the chat's it was opened from, which reads it (none: the default's). */
  | { kind: "link"; url: string; title?: string; manager?: string }
  /** A lead's Team (#5180, lib/team.ts): its workers' strip and the conversation of `worker` (else the one lib/team.ts
   *  shownWorker picks). `sid` is the lead's: the panel stays while the page shows the lead. */
  | { kind: "team"; mid: string; sid: string; worker?: string }
  /** The session's dev server in a headless browser on its computer (docs/web/preview.md); `port` / `path`: the one to
   *  show (a localhost link of a reply), else the one shown last or the first running one. */
  | { kind: "preview"; mid: string; sid: string; cwd: string; port?: number; path?: string };

/** Files and Git state belongs to a directory even when that workspace has no Agent/session ID. */
export function directoryPanelKey(panel: { mid: string; sid: string; cwd: string }): string {
  return JSON.stringify([panel.mid, panel.sid, panel.cwd]);
}

let current: Panel | null = null;
let opener: Element | null = null;
const listeners = new Set<() => void>();

export function openPanel(p: Panel | null) {
  // what had the focus when the panel opened (its header button): closing gives the focus back to it (#5155). Called
  // from the click, before the panel renders, so it is never something inside the panel; a panel replacing another keeps
  // the first one's.
  if (!current && p) opener = typeof document === "undefined" ? null : document.activeElement;
  current = p;
  listeners.forEach((l) => l());
}

export function closePanel() {
  openPanel(null);
}

export function getPanel() {
  return current;
}

/** What had the focus when the open panel was opened (see openPanel). */
export function panelOpener(): Element | null {
  return opener;
}

/** Toggle a session panel from a header button: the same kind for the same session closes it. */
export function togglePanel(p: Panel) {
  const c = current;
  const same = c && c.kind === p.kind && "mid" in c && "mid" in p && c.mid === p.mid && ("sid" in c && "sid" in p ? c.sid === p.sid : true);
  openPanel(same ? null : p);
}

export function usePanel(): Panel | null {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
    () => current,
  );
}
