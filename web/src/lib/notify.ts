// Settings › Notifications of this browser (#2971, the app's NotificationSettingsView): what the hub keeps per device (GET / PUT
// /devices/{id}/prefs, hub/notify.go NotifyPrefs), and the browser's system notifications that follow it. The hub does
// not push to a browser: while the page is open it watches its own session list (lib/attention-signals.ts) and posts a
// notification when it is not in front, as the Mac app does (MacAttention.swift). The prefs also decide the sounds.
import { useSyncExternalStore } from "react";
import { t } from "../i18n";
import { api, credentialOwner, enc, onCredentialChange } from "./api";
import { isDelegating } from "./delegate";
import { sessionTitle } from "./format";
import { interactionAsk } from "./inbox";
import { plainMarkdown } from "./plain-markdown";
import { byTime } from "./times";
import { requestHome } from "./child-sessions";
import type { Interaction, Session } from "./types";

export type DonePref = "off" | "long" | "always";

/** The part of a device's prefs this page reads and writes; the rest (the phone's Live Activity, translation) stays as
 *  the hub has it: a PUT decodes over the device's stored prefs. */
export interface NotifyPrefs {
  input: boolean;
  done: DonePref;
  /** What a long task means, from the hub (notify.go longTurn). */
  long_turn_seconds?: number;
  /** How long a finished turn waits before the phone's push (notify.go doneDelay). */
  done_delay_seconds?: number;
  /** Quiet hours (#5806, hub/quiethours.go): inside the window no "finished" signal, and "Needs you" without sound. */
  quiet?: QuietHours;
  /** The managers whose pushes this device doesn't want (Settings › Managers); this browser's signals leave them out too. */
  muted_managers?: string[];
}

/** "HH:MM" local times; an empty `tz` is this browser's time zone. The window may cross midnight. */
export interface QuietHours {
  enabled: boolean;
  start: string;
  end: string;
  tz?: string;
}

/** The times the switch shows before any are set. */
export const DEFAULT_QUIET: QuietHours = { enabled: false, start: "23:00", end: "07:00" };

/** The hub's defaults (notify.go defaultPrefs, longTurn). */
export const DEFAULT_PREFS: NotifyPrefs = { input: true, done: "long" };
export const DEFAULT_LONG_TURN_SECONDS = 60;

// ── which events speak ───────────────────────────────────

/**
 * Does this browser want a signal (sound, notification) for the event? "Needs you": the "When something needs you" switch. "Done": "Don't notify" never,
 * "Every time" always, "Long tasks only" for a turn of at least long_turn_seconds (`ranMs`; unknown is not long, as on the Mac).
 */
export function wants(kind: "needs" | "finished", prefs: NotifyPrefs, ranMs?: number, now: Date = new Date()): boolean {
  if (kind === "needs") return prefs.input;
  if (inQuietHours(prefs, now)) return false;
  switch (prefs.done) {
    case "always":
      return true;
    case "long":
      return ranMs !== undefined && ranMs >= (prefs.long_turn_seconds ?? DEFAULT_LONG_TURN_SECONDS) * 1000;
    default:
      return false;
  }
}

function clockMinutes(s: string | undefined): number | undefined {
  const m = /^([01]\d|2[0-3]):([0-5]\d)$/.exec(s ?? "");
  return m ? Number(m[1]) * 60 + Number(m[2]) : undefined;
}

/** Is `now` inside the prefs' quiet hours (as the hub reads them: the start is inside, the end is not, start = end is none)? */
export function inQuietHours(prefs: Pick<NotifyPrefs, "quiet">, now: Date = new Date()): boolean {
  const q = prefs.quiet;
  if (!q?.enabled) return false;
  const start = clockMinutes(q.start);
  const end = clockMinutes(q.end);
  if (start === undefined || end === undefined || start === end) return false;
  let minutes = now.getHours() * 60 + now.getMinutes();
  if (q.tz) {
    try {
      const parts = new Intl.DateTimeFormat("en-US", { timeZone: q.tz, hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(now);
      const part = (type: string) => Number(parts.find((x) => x.type === type)?.value);
      minutes = part("hour") * 60 + part("minute");
    } catch {
      // a zone this browser doesn't know: its own time
    }
  }
  return start < end ? minutes >= start && minutes < end : minutes >= start || minutes < end;
}

/** A duration as the app says it (NotifyPrefs.duration): whole minutes in minutes, else seconds; 60 s when unknown. */
export function durationText(seconds: number | undefined, units: { s: (n: number) => string; m: (n: number) => string }): string {
  const s = seconds ?? DEFAULT_LONG_TURN_SECONDS;
  return s >= 60 && s % 60 === 0 ? units.m(s / 60) : units.s(s);
}

// ── this browser's device id and its prefs ───────────────

const DEVICE_KEY = "persona.notifyDevice";
const PREFS_KEY = "persona.notifyPrefs";

const randomId = () => crypto.randomUUID?.() ?? Math.random().toString(36).slice(2);

/**
 * The id this browser's prefs go by on the hub: made once, kept in this browser (`kept`). The hub ties the row to the
 * device token that wrote it, so revoking this browser in Settings › Devices removes it. Without storage the id would be new
 * on every load: it is this page's own and nothing is sent (`kept: false`), so no browser leaves rows behind or shares
 * one id with another.
 */
let pageId: string | undefined;
export function deviceId(): { id: string; kept: boolean } {
  try {
    let id = localStorage.getItem(DEVICE_KEY);
    if (!id) {
      id = `web-${randomId()}`;
      localStorage.setItem(DEVICE_KEY, id);
    }
    return { id, kept: true };
  } catch {
    pageId ??= `web-${randomId()}`;
    return { id: pageId, kept: false };
  }
}

function readCached(): NotifyPrefs {
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    return raw ? normalize(JSON.parse(raw)) : DEFAULT_PREFS;
  } catch {
    return DEFAULT_PREFS;
  }
}

/** The hub's answer, defaults filled in and an unknown "Done" taken as "Long tasks only" (as the hub does). */
export function normalize(raw: Partial<NotifyPrefs> | null | undefined): NotifyPrefs {
  const done: DonePref = raw?.done === "off" || raw?.done === "always" ? raw.done : "long";
  return {
    input: raw?.input ?? true,
    done,
    long_turn_seconds: raw?.long_turn_seconds,
    done_delay_seconds: raw?.done_delay_seconds,
    quiet: raw?.quiet?.enabled || raw?.quiet?.start ? { ...DEFAULT_QUIET, ...raw.quiet } : undefined,
    // a hub from before them has none: leave the field out, so Settings › Managers knows not to offer the switch
    ...(Array.isArray(raw?.muted_managers) ? { muted_managers: raw.muted_managers.filter((x): x is string => typeof x === "string") } : {}),
  };
}

/** Whether this device turned off manager's pushes (an item without one is the default manager's: `fallback`). */
export function managerMuted(prefs: NotifyPrefs, manager: string | undefined, fallback: string): boolean {
  return (prefs.muted_managers ?? []).includes(manager || fallback);
}

/** Turns one manager's pushes on or off for this device: optimistic, as saveNotifyPrefs; only the list is sent. */
export async function saveManagerPush(manager: string, on: boolean): Promise<void> {
  return savePrefs((current) => {
    const list = (current.muted_managers ?? []).filter((x) => x !== manager);
    return { muted_managers: on ? list : [...list, manager] };
  });
}

let prefs: NotifyPrefs = typeof localStorage === "undefined" ? DEFAULT_PREFS : readCached();
const listeners = new Set<() => void>();
type PrefsPatch = Partial<Pick<NotifyPrefs, "input" | "done" | "quiet" | "muted_managers">>;
type PrefsChange = (current: NotifyPrefs) => PrefsPatch;
type PendingSave = { change: PrefsChange; path: string; controller: AbortController; resolve: () => void; reject: (error: unknown) => void };

function prefsCredentialOwner() {
  try { return credentialOwner(); } catch { /* Without storage, preferences remain local to this page. */ }
}

function createPrefsSession(owner: string | undefined, confirmed = DEFAULT_PREFS) {
  return { owner, confirmed, pending: [] as PendingSave[], loads: new Set<AbortController>(), saving: false, revision: 0, loadSeq: 0 };
}
let prefsSession = createPrefsSession(prefsCredentialOwner(), prefs);
const cancelledPrefs = () => new DOMException("Notification preference session ended", "AbortError");

onCredentialChange((owner) => {
  if (owner === prefsSession.owner) return;
  const retired = prefsSession;
  prefsSession = createPrefsSession(owner);
  retired.pending.splice(0).forEach((save) => {
    save.controller.abort();
    save.reject(cancelledPrefs());
  });
  retired.loads.forEach((controller) => controller.abort());
  // A previous login's optimistic cache is not a baseline for the next login. Its reads will populate this session.
  try { localStorage.removeItem(PREFS_KEY); } catch { /* Storage may be unavailable. */ }
  set(DEFAULT_PREFS, false);
});

function currentPrefsSession() {
  prefsCredentialOwner(); // Also observe credentials replaced directly in storage, without setToken().
  return prefsSession;
}

// Another tab of the app changed them (its own save or load): this one follows at once, sounds and notifications too.
if (typeof window !== "undefined") {
  window.addEventListener("storage", (e) => {
    const session = currentPrefsSession();
    if (e.key !== PREFS_KEY) return;
    session.confirmed = readCached();
    session.revision++;
    publishPrefs(session, false);
  });
}

function set(next: NotifyPrefs, cache = true) {
  prefs = next;
  if (cache) {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(next));
    } catch {
      // storage unavailable: kept for this page only
    }
  }
  listeners.forEach((l) => l());
}

export function notifyPrefs(): NotifyPrefs {
  return prefs;
}

function publishPrefs(session: ReturnType<typeof createPrefsSession>, cache = true) {
  if (session !== currentPrefsSession()) return;
  set(session.pending.reduce((current, save) => ({ ...current, ...save.change(current) }), session.confirmed), cache);
}

// One PUT at a time: response fencing alone cannot stop the hub committing older snapshots last. Reapply queued
// intents over each confirmed answer; a rejected edit must not become another field's implicit retry.
async function flushSaves(session: ReturnType<typeof createPrefsSession>) {
  session.saving = true;
  try {
    while (session.pending.length && session === currentPrefsSession()) {
      const save = session.pending[0];
      let failed = false;
      let error: unknown;
      try {
        const answer = await api<Partial<NotifyPrefs>>(save.path, { method: "PUT", json: save.change(session.confirmed), signal: save.controller.signal });
        if (session !== currentPrefsSession()) return;
        session.confirmed = normalize(answer);
      } catch (e) {
        if (session !== currentPrefsSession()) return;
        failed = true;
        error = e;
      }
      session.pending.shift();
      session.revision++;
      publishPrefs(session);
      if (failed) save.reject(error);
      else save.resolve();
    }
  } finally {
    session.saving = false;
  }
}

function savePrefs(change: PrefsChange): Promise<void> {
  const session = currentPrefsSession();
  const { id, kept } = deviceId();
  session.revision++;
  if (!kept) {
    session.confirmed = { ...prefs, ...change(prefs) };
    set(session.confirmed);
    return Promise.resolve();
  }
  const result = new Promise<void>((resolve, reject) => session.pending.push({ change, path: `/devices/${enc(id)}/prefs`, controller: new AbortController(), resolve, reject }));
  publishPrefs(session);
  if (!session.saving) void flushSaves(session);
  return result;
}

/** The hub's prefs for this browser; the cached copy until it answers (and if it can't). */
export async function loadNotifyPrefs(): Promise<NotifyPrefs> {
  const session = currentPrefsSession();
  const { id, kept } = deviceId();
  if (!kept) return prefs;
  const seq = ++session.loadSeq;
  const revision = session.revision;
  const controller = new AbortController();
  session.loads.add(controller);
  try {
    const p = normalize(await api<Partial<NotifyPrefs>>(`/devices/${enc(id)}/prefs`, { signal: controller.signal }));
    if (session !== currentPrefsSession()) throw cancelledPrefs();
    if (seq === session.loadSeq && revision === session.revision && !session.pending.length) {
      session.confirmed = p;
      set(p);
    }
    return prefs;
  } catch (e) {
    if (session !== currentPrefsSession()) throw cancelledPrefs();
    throw e;
  } finally {
    session.loads.delete(controller);
  }
}

/** Optimistic: the control moves at once and goes back if the hub says no (the error is the caller's to show). */
export async function saveNotifyPrefs(patch: Partial<Pick<NotifyPrefs, "input" | "done" | "quiet">>): Promise<void> {
  const intent = { ...patch, ...(patch.quiet && { quiet: { ...patch.quiet } }) };
  return savePrefs(() => intent);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function useNotifyPrefs(): NotifyPrefs {
  return useSyncExternalStore(subscribe, notifyPrefs, () => DEFAULT_PREFS);
}

/** Mounted consumers must reread when pairing changes, even when both generations have identical default prefs. */
export function notifyPrefsGeneration() {
  return prefsSession.owner;
}

export function useNotifyPrefsGeneration() {
  return useSyncExternalStore(subscribe, notifyPrefsGeneration, () => undefined);
}

// ── the browser's system notifications ───────────────────

/** unsupported: this browser has no Notification API (an iPhone's Safari outside a home-screen app, an old browser). */
export type Permission = "unsupported" | "default" | "granted" | "denied";

export function permission(): Permission {
  if (typeof Notification === "undefined") return "unsupported";
  return Notification.permission;
}

const SYSTEM_KEY = "persona.systemNotify";
const systemListeners = new Set<() => void>();

/** The page's own switch, off until turned on here (turning it on asks the browser). */
export function systemNotifyOn(): boolean {
  try {
    return localStorage.getItem(SYSTEM_KEY) === "1";
  } catch {
    return false;
  }
}

function setSystemFlag(on: boolean) {
  try {
    if (on) localStorage.setItem(SYSTEM_KEY, "1");
    else localStorage.removeItem(SYSTEM_KEY);
  } catch {
    // storage unavailable
  }
  systemListeners.forEach((l) => l());
}

/** Turns the switch on (asking the browser first when it has not been asked) or off. Says the permission it ended with. */
export async function setSystemNotify(on: boolean): Promise<Permission> {
  if (!on) {
    setSystemFlag(false);
    return permission();
  }
  let p = permission();
  if (p === "default") p = await Notification.requestPermission();
  setSystemFlag(p === "granted");
  return p;
}

/** Notifications go out: the switch is on and the browser allows them. */
export function systemNotifyActive(): boolean {
  return systemNotifyOn() && permission() === "granted";
}

const ALWAYS_KEY = "persona.systemNotifyAlways";

/** "Always show system notifications" (#2451): also while a tab of the app is in front. Kept apart from the main
 *  switch and only counts while that one is active, so turning the main one off and on again keeps this choice. */
export function systemNotifyAlways(): boolean {
  try {
    return localStorage.getItem(ALWAYS_KEY) === "1";
  } catch {
    return false;
  }
}

export function setSystemNotifyAlways(on: boolean): void {
  try {
    if (on) localStorage.setItem(ALWAYS_KEY, "1");
    else localStorage.removeItem(ALWAYS_KEY);
  } catch {
    // storage unavailable
  }
  systemListeners.forEach((l) => l());
}

/**
 * May a system notification go out now? Only while they are on (`active`); then only while no tab of the app is in front
 * (`here`: this one, `other`: another), unless `always` is on. Which tab posts it is claimNotification's, either way.
 */
export function systemNotifyAllowed(front: { here: boolean; other: boolean }, s = { active: systemNotifyActive(), always: systemNotifyAlways() }): boolean {
  return s.active && (s.always || (!front.here && !front.other));
}

function subscribeSystem(l: () => void) {
  systemListeners.add(l);
  window.addEventListener("storage", l);
  // the permission changes in the browser's own settings: look again when the page comes back
  window.addEventListener("focus", l);
  return () => {
    systemListeners.delete(l);
    window.removeEventListener("storage", l);
    window.removeEventListener("focus", l);
  };
}

const snapshot = () => `${systemNotifyOn() ? 1 : 0}|${permission()}|${systemNotifyAlways() ? 1 : 0}`;

export function useSystemNotify(): { on: boolean; permission: Permission; always: boolean } {
  const s = useSyncExternalStore(subscribeSystem, snapshot, () => "0|unsupported|0");
  const [on, p, always] = s.split("|");
  return { on: on === "1", permission: p as Permission, always: always === "1" };
}

// ── between the app's tabs ───────────────────────────────
// Every open tab watches the same sessions. A notification is for when the user is not looking at the app at all, so
// none goes out while any tab of it is in front (unless "Always" is on), and one event makes one notification, not one per tab.

const FRONT_KEY = "persona.frontTab";
const NOTIFIED_KEY = "persona.notified";
/** The tab in front says so again this often; a mark older than FRONT_FRESH_MS is a tab that went away without a word. */
export const FRONT_BEAT_MS = 20_000;
export const FRONT_FRESH_MS = 3 * FRONT_BEAT_MS;
/** The tabs see an event within moments of each other: the first to post it within this long is the one that does. */
export const NOTIFIED_WINDOW_MS = 5_000;

let tabId = `tab-${randomId()}`;

/** For tests: this module as another tab of the same browser. */
export function _setTabId(id: string): void {
  tabId = id;
}

function readJSON<T>(key: string): T | undefined {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : undefined;
  } catch {
    return undefined;
  }
}

/** Records whether this tab is the one in front (on focus, blur, visibility changes and every FRONT_BEAT_MS). */
export function markFront(front: boolean, now = Date.now()): void {
  try {
    if (front) localStorage.setItem(FRONT_KEY, JSON.stringify({ tab: tabId, at: now }));
    else if (readJSON<{ tab?: string }>(FRONT_KEY)?.tab === tabId) localStorage.removeItem(FRONT_KEY);
  } catch {
    // storage unavailable: each tab decides on its own
  }
}

/** Some other tab of the app is the one in front. */
export function otherTabInFront(now = Date.now()): boolean {
  const mark = readJSON<{ tab?: string; at?: number }>(FRONT_KEY);
  return !!mark && mark.tab !== tabId && typeof mark.at === "number" && now - mark.at < FRONT_FRESH_MS;
}

/** Takes the event (`tag`) for this tab: false when another tab already posted it just now. */
export function claimNotification(tag: string, now = Date.now()): boolean {
  const seen = readJSON<Record<string, number>>(NOTIFIED_KEY) ?? {};
  if (typeof seen[tag] === "number" && now - seen[tag] < NOTIFIED_WINDOW_MS) return false;
  const next: Record<string, number> = { [tag]: now };
  for (const [k, at] of Object.entries(seen)) if (k !== tag && now - at < NOTIFIED_WINDOW_MS) next[k] = at;
  try {
    localStorage.setItem(NOTIFIED_KEY, JSON.stringify(next));
  } catch {
    // storage unavailable: this tab posts
  }
  return true;
}

/** Forgets that `tag` was posted just now (its notification was taken back), so the next event for it posts. */
export function releaseClaim(tag: string): void {
  const seen = readJSON<Record<string, number>>(NOTIFIED_KEY);
  if (!seen || !(tag in seen)) return;
  delete seen[tag];
  try {
    localStorage.setItem(NOTIFIED_KEY, JSON.stringify(seen));
  } catch {
    // storage unavailable: nothing was kept to forget
  }
}

/** The words a notification is written in; `asks` names what a waiting card wants (the hub's push titles). */
export interface NotificationWords {
  needs: (title: string) => string;
  finished: (title: string) => string;
  needsBody: string;
  finishedBody: string;
  asks?: (kind: Interaction["kind"], title: string) => string;
}

/** What a session waits for you on, for its "Needs you" notification: its oldest pending card, else its host's reason. */
export interface Waiting {
  ix?: Interaction;
  reason?: string;
}

/** The session's card the notification is about: the oldest one pending for it (the one the overview lists first), not
 *  one the assistant is still looking at (nothing is pushed for those). A child session's card is its parent's (#5120). */
export function waitingCard(interactions: readonly Interaction[], s: Pick<Session, "machine_id" | "session_id">): Interaction | undefined {
  return interactions
    .filter((ix) => ix.machine_id === s.machine_id && requestHome(ix) === s.session_id && !isDelegating(ix))
    .sort((a, b) => byTime(a.created_at, b.created_at) || a.id.localeCompare(b.id))[0];
}

/** What a card asks, as one line: the question, the permission's description or command, the plan's first line
 *  (hub interactionPayloadIn); empty when it says nothing. */
function cardLine(ix: Interaction): string {
  if (ix.kind === "plan") return plainMarkdown((ix.plan ?? "").split("\n").find((l) => l.trim()) ?? ix.summary ?? "");
  return plainMarkdown(interactionAsk(ix));
}

const oneLine = (text: string | undefined) => text?.replace(/\s+/g, " ").trim() ?? "";

/**
 * What a notification says (MacAttention.text): the agent and what happened. A "Needs you" one about a card says what kind
 * of card it is and what it asks (as the phone's push does), else why the host says it waits; otherwise, and for a
 * finished turn, its last words are the body.
 */
export function notificationText(s: Pick<Session, "title" | "cwd" | "last_text">, kind: "needs" | "finished", words: NotificationWords, waiting: Waiting = {}): { title: string; body: string } {
  const name = sessionTitle(s);
  const ix = kind === "needs" ? waiting.ix : undefined;
  const title = ix && words.asks ? words.asks(ix.kind, name) : kind === "needs" ? words.needs(name) : words.finished(name);
  const said = (ix && oneLine(cardLine(ix))) || (kind === "needs" ? oneLine(waiting.reason) : "") || oneLine(s.last_text);
  return { title, body: (said || (kind === "needs" ? words.needsBody : words.finishedBody)).slice(0, 200) };
}

/** The browser draws buttons on a notification (Chrome, Edge, Firefox 152+; not Safari) and a service worker controls
 *  this page to show one with them: only the worker's notifications have buttons. Not on a phone or tablet: its lock
 *  screen runs a button without unlocking, and the apps ask to unlock before Allow (NotificationActions), a page can't. */
function buttonsOnNotifications(): boolean {
  if (typeof matchMedia === "undefined" || !matchMedia("(pointer: fine)").matches) return false;
  return ((Notification as { maxActions?: number }).maxActions ?? 0) >= 2 && !!navigator.serviceWorker?.controller;
}

/**
 * Posts one notification; a later one for the same agent and kind replaces it (`tag`). Clicking it brings the page
 * forward and runs `open`. Where the page can't make one itself (Chrome on Android wants the service worker's), the
 * service worker shows it and opens `url` when clicked (sw.ts notificationclick). One about a permission card (`ix`)
 * gets Allow / Deny where the browser can draw them (#4464): the worker shows it and an open page answers a button
 * (lib/notification-answer.ts); elsewhere it is the plain one.
 */
export function postNotification(n: { title: string; body: string; tag: string; url: string; icon?: string; ix?: string; silent?: boolean }, open: () => void): void {
  if (!systemNotifyActive()) return;
  if (n.ix && buttonsOnNotifications()) {
    // Chrome doesn't let the worker's notification replace the page's under the same tag: take the page's back first
    closePageNotification(n.tag);
    const actions = [{ action: "allow", title: t("chat-interactions.allow") }, { action: "deny", title: t("chat-interactions.deny") }];
    navigator.serviceWorker.ready
      .then((reg) => reg.showNotification(n.title, { body: n.body, tag: n.tag, icon: n.icon, silent: n.silent, data: { url: n.url, ix: n.ix }, actions } as NotificationOptions))
      .catch(() => postNotification({ ...n, ix: undefined }, open));
    return;
  }
  try {
    const note = new Notification(n.title, { body: n.body, tag: n.tag, icon: n.icon, silent: n.silent });
    posted.set(n.tag, note);
    // nor the page's the worker's (one with buttons, from an earlier permission card of this agent)
    closeWorkerNotifications(n.tag);
    note.onclick = () => {
      window.focus();
      open();
      note.close();
    };
    note.onclose = () => {
      if (posted.get(n.tag) === note) posted.delete(n.tag);
    };
  } catch {
    navigator.serviceWorker
      ?.getRegistration()
      .then((reg) => reg?.showNotification(n.title, { body: n.body, tag: n.tag, icon: n.icon, silent: n.silent, data: { url: n.url } }))
      .catch(() => {});
  }
}

/** The notifications this page made itself, by tag: what closeNotification takes back. */
const posted = new Map<string, Notification>();

/**
 * Takes back the notification for `tag` once its event is over (answered or seen, here or elsewhere, #3458): the one
 * this page made, and one the service worker shows for it. Does nothing where there are no notifications.
 */
export function closeNotification(tag: string): void {
  // its event is over: the next one for the same agent and kind may post at once, not after NOTIFIED_WINDOW_MS
  releaseClaim(tag);
  closePageNotification(tag);
  closeWorkerNotifications(tag);
}

/** Closes this page's notification for `tag` and the worker's, keeping the tabs' claim on it (another tab may hold it). */
export function dismissNotification(tag: string): void {
  closePageNotification(tag);
  closeWorkerNotifications(tag);
}

function closePageNotification(tag: string): void {
  const note = posted.get(tag);
  if (!note) return;
  posted.delete(tag);
  try {
    note.close();
  } catch {
    // already gone
  }
}

function closeWorkerNotifications(tag: string): void {
  if (typeof navigator === "undefined" || !navigator.serviceWorker) return;
  navigator.serviceWorker
    .getRegistration()
    .then((reg) => reg?.getNotifications({ tag }))
    .then((list) => list?.forEach((n) => n.close()))
    .catch(() => {});
}
