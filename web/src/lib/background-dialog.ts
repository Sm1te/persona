// A chat's Background dialog (views/chat/Activity.tsx ActivityChips) opened from elsewhere in that chat: a workflow
// run's card opens it at its run (#5030, after cindy, whose workflow card opens its background tasks panel). Each chat
// on screen with the dialog says so, with the runs the dialog lists; a card whose chat has none that lists its run (the
// assistant's chat, a run older than the dialog keeps) opens in place instead, so a click is never dead.

/** One chat's dialog: the runs it lists (by task_id), and how to open it (at a run). */
interface Opener {
  runs: ReadonlySet<string>;
  open: (run?: string) => void;
}

const openers = new Map<string, Opener[]>();
const watchers = new Set<() => void>();
const key = (mid: string, sid: string) => `${mid}/${sid}`;

function changed() {
  watchers.forEach((w) => w());
}

/** The chat's dialog is on screen and lists these runs; the function returned says it is gone. The same chat drawn
 *  twice has two: the one that came last opens. */
export function listenBackgroundDialog(mid: string, sid: string, runs: Iterable<string>, open: (run?: string) => void): () => void {
  const k = key(mid, sid);
  const me: Opener = { runs: new Set(runs), open };
  openers.set(k, [...(openers.get(k) ?? []), me]);
  changed();
  return () => {
    const left = (openers.get(k) ?? []).filter((o) => o !== me);
    if (left.length) openers.set(k, left);
    else openers.delete(k);
    changed();
  };
}

function opener(mid: string, sid: string, run?: string): Opener | undefined {
  const list = openers.get(key(mid, sid)) ?? [];
  for (let i = list.length - 1; i >= 0; i--) if (run === undefined || list[i].runs.has(run)) return list[i];
  return undefined;
}

/** Whether the chat has its dialog on screen (listing this run, when one is named). */
export function canOpenBackgroundDialog(mid: string, sid: string, run?: string): boolean {
  return opener(mid, sid, run) !== undefined;
}

/** Opens the chat's dialog, brought to this run when one is named. False when there is none that lists it: the caller
 *  shows the run some other way. */
export function openBackgroundDialog(mid: string, sid: string, run?: string): boolean {
  const o = opener(mid, sid, run);
  o?.open(run);
  return o !== undefined;
}

/** Told when a dialog comes or goes or lists other runs (a `useSyncExternalStore` subscribe). */
export function subscribeBackgroundDialogs(fn: () => void): () => void {
  watchers.add(fn);
  return () => void watchers.delete(fn);
}
