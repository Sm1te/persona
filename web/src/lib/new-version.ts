// An open page picks up a new web build (#4331; docs/web/web.md "New versions"). The service worker already serves a
// fresh index.html to every navigation, but a page that stays open never navigates again, so it kept the old build for
// days. The page asks the hub for its shell now and then (on a reconnect, when it comes back to the front, every few
// minutes) and compares the entry script the shell loads with the one this page runs; the build's file names carry
// their content hash, so a new build always names another one. That covers a web-only deploy, which leaves the hub's
// own version (hello `server_version`, /api/v1/health) unchanged.
//
// A new build reloads the page by itself when nothing would be lost: the page is idle (no key or pointer for a while,
// or hidden), no text field has a draft and no dialog or menu is open. Otherwise a toast that stays says so, with a
// button that reloads at once; the check keeps waiting for a quiet moment and reloads then.
import { BASE } from "./base";
import { onHello } from "./store";

/** How long the page has to be left alone before it reloads by itself. */
export const QUIET_MS = 60_000;
/** How often an open page asks for the shell. */
export const CHECK_EVERY_MS = 5 * 60_000;
/** A check asked for within this long of the last one is skipped (focus flapping, several reconnects). */
export const CHECK_GAP_MS = 20_000;
/** While a new build waits for a quiet moment, how often the page looks again. */
const WAIT_TICK_MS = 15_000;

/** The entry script a shell loads (its `<script type="module" src="./assets/…">`), null when it has none: not the
 *  app's shell (a sign-in page of a layer in front of the hub) or a dev server. */
export function shellEntry(html: string): string | null {
  for (const m of html.matchAll(/<script\b[^>]*>/gi)) {
    const tag = m[0];
    if (!/\btype=["']?module\b/i.test(tag)) continue;
    const src = /\bsrc=["']?\.?(\/assets\/[^"'\s?#>]+)/i.exec(tag);
    if (src) return src[1];
  }
  return null;
}

/** All the build's files a shell names up front (its entry script, stylesheets and preloads), sorted: a build that
 *  changed only its global stylesheet keeps the entry's name but not this. "" for a page that is not the app's. */
export function shellFingerprint(html: string): string {
  if (!shellEntry(html)) return "";
  const out = new Set<string>();
  for (const m of html.matchAll(/(?:src|href)=["']?\.?(\/assets\/[^"'\s?#>]+)/g)) out.add(m[1]);
  return [...out].sort().join(" ");
}

/** Whether `served` is another build than `running` (unknown on either side is no). */
export function isNewBuild(running: string | null, served: string | null): boolean {
  return !!running && !!served && running !== served;
}

export interface Calm {
  /** The page is in the background (another tab, minimized). */
  hidden: boolean;
  /** A text field holds something typed and not sent, or has the focus. */
  draft: boolean;
  /** A dialog, sheet or menu is open. */
  overlay: boolean;
  /** Milliseconds since the last key, pointer or wheel. */
  quietMs: number;
}

/** Whether the page may reload itself now: never over a draft or an open dialog; else when hidden or left alone. */
export function mayReload(c: Calm): boolean {
  if (c.draft || c.overlay) return false;
  return c.hidden || c.quietMs >= QUIET_MS;
}

const EDITABLE = 'textarea, input:not([type]), input[type="text"], input[type="search"], input[type="url"], input[type="email"], input[type="number"], [contenteditable=""], [contenteditable="true"]';
const OVERLAY = '[role="dialog"], [role="alertdialog"], [role="menu"], [aria-modal="true"], dialog[open]';

/** Whether `doc` has a draft: a text field with something in it (an empty one with the focus, such as the composer
 *  a chat focuses by itself, is none; typing keeps the page from being quiet anyway). */
export function hasDraft(doc: Document): boolean {
  for (const el of doc.querySelectorAll<HTMLElement>(EDITABLE)) {
    if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
      if (!el.disabled && !el.readOnly && el.value.trim() !== "") return true;
    } else if ((el.textContent ?? "").trim() !== "") return true;
  }
  return false;
}

export function hasOverlay(doc: Document): boolean {
  return doc.querySelector(OVERLAY) !== null;
}

/** What the checker needs from the world; tests swap it. */
export interface NewVersionDeps {
  running: () => string | null;
  fetchShell: () => Promise<string>;
  calm: () => Calm;
  reload: () => void;
  notify: (reloadNow: () => void) => void;
  now: () => number;
}

/** One page's checker: `check` asks for the shell, `tick` reloads a waiting build once the page is calm. */
export function newVersionChecker(deps: NewVersionDeps) {
  let last = -Infinity;
  // the shell of this page's own build as the hub first served it: what a later shell is compared with in full
  let baseline = "";
  let waiting = false;
  let told = false;
  let gone = false;
  const reloadNow = () => {
    if (gone) return;
    gone = true;
    deps.reload();
  };
  const tick = () => {
    if (!waiting || gone) return;
    if (mayReload(deps.calm())) return reloadNow();
    if (!told) {
      told = true;
      deps.notify(reloadNow);
    }
  };
  const check = async (force = false) => {
    const now = deps.now();
    if (waiting || gone || (!force && now - last < CHECK_GAP_MS)) return tick();
    last = now;
    let html: string;
    try {
      html = await deps.fetchShell();
    } catch {
      return; // the hub out of reach: the next check asks again
    }
    const entry = shellEntry(html);
    const print = shellFingerprint(html);
    if (isNewBuild(deps.running(), entry)) waiting = true;
    else if (entry && entry === deps.running()) {
      if (!baseline) baseline = print;
      else if (print !== baseline) waiting = true;
    }
    tick();
  };
  return { check, tick, waiting: () => waiting };
}

/** The entry script this page runs. */
export function runningEntry(doc: Document): string | null {
  for (const s of doc.querySelectorAll<HTMLScriptElement>('script[type="module"][src]')) {
    const src = /\.?(\/assets\/[^"'\s?#]+)/.exec(s.getAttribute("src") ?? "");
    if (src) return src[1];
  }
  return null;
}

function begin(notify: (reloadNow: () => void) => void) {
  if (!import.meta.env.PROD || typeof document === "undefined") return;
  const running = runningEntry(document);
  if (!running) return;
  let lastInput = Date.now();
  for (const ev of ["keydown", "pointerdown", "wheel", "touchstart"]) addEventListener(ev, () => (lastInput = Date.now()), { capture: true, passive: true });
  const checker = newVersionChecker({
    running: () => running,
    fetchShell: async () => {
      const r = await fetch(`${BASE}/`, { cache: "no-store", credentials: "same-origin", headers: { Accept: "text/html" } });
      if (!r.ok || r.redirected) throw new Error(String(r.status));
      return r.text();
    },
    calm: () => ({ hidden: document.visibilityState === "hidden", draft: hasDraft(document), overlay: hasOverlay(document), quietMs: Date.now() - lastInput }),
    reload: () => {
      // the worker fetches the new shell and its files first, so the reload is quick and the next start offline is new too
      const sw = navigator.serviceWorker;
      const update = sw ? sw.getRegistration().then((reg) => reg?.update()).catch(() => {}) : Promise.resolve();
      void Promise.race([update, new Promise((r) => setTimeout(r, 3000))]).then(() => location.reload());
    },
    notify,
    now: () => Date.now(),
  });
  document.addEventListener("visibilitychange", () => void (document.visibilityState === "hidden" ? checker.tick() : checker.check()));
  addEventListener("online", () => void checker.check());
  setInterval(() => void checker.check(), CHECK_EVERY_MS);
  setInterval(() => checker.waiting() && checker.tick(), WAIT_TICK_MS);
  // at once too: a hello that came before this module loaded would otherwise leave the first check to the interval
  void checker.check(true);
  return checker;
}

/** From main.tsx: starts checking in a built page (never under Vite's dev server, whose modules aren't a build). */
export function startNewVersion(notify: (reloadNow: () => void) => void) {
  const checker = begin(notify);
  // a hub that came back (a restart, a deploy) is asked at once
  if (checker) onHello(() => void checker.check(true));
}
