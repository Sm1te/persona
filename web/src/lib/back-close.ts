// Back (the browser's button, a phone's back gesture) closes what is open over the page before it leaves the page
// (#1996): the drawer, a full-screen side panel, the picture viewer, a bottom sheet. Opening one pushes an entry with
// the same URL; going back pops it and closes the overlay; closing it from the page takes its entry back off.
import { useEffect, useRef } from "react";

const KEY = "personaOverlay";
/** The address the overlay opened on: the entry under its own has it (see settleOverlayEntry). */
const AT = "personaOverlayAt";
let next = 0;
// A close takes its entry off a tick later: an overlay that opens again before then (React's StrictMode runs every
// effect twice in development; a sheet swapped for another) reuses the entry instead of racing an async history.back()
// that would pop the new one and close it at once.
let pending: { id: string; timer: ReturnType<typeof setTimeout> } | null = null;

/** For a navigation: whether the current entry is an open overlay's, which the new page should replace instead of
 *  stacking on (else Back from the new page lands on a dead copy of the old one). The navigation's replaceState takes
 *  the overlay's marker with it, so the overlay's close finds nothing of it to take off. */
export function takeOverlayEntry(): boolean {
  const st = history.state as Record<string, unknown> | null;
  const id = st && typeof st === "object" ? st[KEY] : undefined;
  if (typeof id !== "string") return false;
  if (pending?.id === id) {
    clearTimeout(pending.timer);
    pending = null;
  }
  return true;
}

/** The state an overlay's entry carries (kept apart from whatever the page's state is). */
export function overlayState(base: unknown, id: string): Record<string, unknown> {
  return { ...(base && typeof base === "object" ? (base as Record<string, unknown>) : {}), [KEY]: id };
}

/** Whether `state` is an open overlay's entry (any overlay's). */
export function onOverlayEntry(state: unknown = typeof history !== "undefined" ? history.state : null): boolean {
  return Boolean(state && typeof state === "object" && typeof (state as Record<string, unknown>)[KEY] === "string");
}

/** Whether `state` is the entry the overlay `id` pushed. */
export function isOverlayEntry(state: unknown, id: string): boolean {
  return Boolean(state && typeof state === "object" && (state as Record<string, unknown>)[KEY] === id);
}

/** Opens one overlay's history entry: Back calls `onClose`. Returns the close (from the page) that takes it off. */
export function openOverlay(onClose: () => void): () => void {
  const id = `o${++next}`;
  if (pending && isOverlayEntry(history.state, pending.id)) {
    clearTimeout(pending.timer);
    pending = null;
    history.replaceState(overlayState(history.state, id), "", location.href);
  } else {
    history.pushState({ ...overlayState(history.state, id), [AT]: location.href }, "", location.href);
  }
  // where it opened: a replace that moves the page under it (an old link turned into its new one, a delete going
  // home) keeps the marker, so Back still closes it; closing it from the page then must not go back from the new page
  const at = location.href;
  let popped = false;
  const onPop = () => {
    // the entry under ours came back: ours is gone, the overlay goes with it
    if (!isOverlayEntry(history.state, id)) {
      popped = true;
      removeEventListener("popstate", onPop);
      onClose();
    }
  };
  addEventListener("popstate", onPop);
  return () => {
    removeEventListener("popstate", onPop);
    if (popped || !isOverlayEntry(history.state, id)) return; // popped, or a navigation went on top: leave it
    if (location.href !== at) {
      // the page moved under it: take its marker off this entry and stay on the new page
      const rest = { ...(history.state as Record<string, unknown>) };
      delete rest[KEY];
      delete rest[AT];
      history.replaceState(Object.keys(rest).length ? rest : null, "", location.href);
      return;
    }
    const p = {
      id,
      timer: setTimeout(() => {
        if (pending !== p) return;
        pending = null;
        if (isOverlayEntry(history.state, id)) history.back();
      }, 0),
    };
    pending = p;
  };
}

/** For the page's first load, before anything draws: an entry that still carries an overlay's marker is one the last
 *  load left behind (a reload with the side panel open, #5134). The overlay is gone, so its entry would only make the
 *  first Back do nothing. On the address it opened on, go back off it (more than once for overlays stacked on each
 *  other); on another (the page moved under it), take the marker off and stay. `ms`: how long one Back may take. */
export async function settleOverlayEntry(ms = 500): Promise<void> {
  const marked = () => {
    const st = history.state as Record<string, unknown> | null;
    return st && typeof st === "object" && typeof st[KEY] === "string" ? st : null;
  };
  for (let i = 0; i < 5; i++) {
    const st = marked();
    if (!st) return;
    if (st[AT] !== location.href) break;
    const popped = await new Promise<boolean>((done) => {
      const timer = setTimeout(() => (removeEventListener("popstate", pop), done(false)), ms);
      const pop = () => (clearTimeout(timer), removeEventListener("popstate", pop), done(true));
      addEventListener("popstate", pop);
      history.back();
    });
    if (!popped) break; // nothing under it (a new tab's only entry): keep the entry, without the marker
  }
  const st = marked();
  if (!st) return;
  const rest = { ...st };
  delete rest[KEY];
  delete rest[AT];
  history.replaceState(Object.keys(rest).length ? rest : null, "", location.href);
}

/** While `open` (and `enabled`, e.g. only on a narrow screen), Back closes it through `onClose`. A navigation away while
 *  it is open leaves its entry under the new page; closing then does not go back (that would undo the navigation). */
export function useCloseOnBack(open: boolean, onClose: () => void, enabled = true): void {
  const close = useRef(onClose);
  close.current = onClose;
  useEffect(() => (open && enabled ? openOverlay(() => close.current()) : undefined), [open, enabled]);
}
