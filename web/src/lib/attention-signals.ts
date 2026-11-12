// The DOM side of the attention signals (#2332, logic in lib/attention.ts): the tab title, the favicon's badge, and the
// optional sounds, and the system notifications (#2971, lib/notify.ts). `<AttentionSignals>` is what App renders for them.
import { useEffect, useRef } from "react";
import { attentionReason, interactionCanAnswer, sessionTitle, stateOf } from "./format";
import { agentKey } from "./pins";
import { homeKey } from "./folded-rows";
import { useCounts, useAgentSessions } from "./sessions";
import { getState, useStore } from "./store";
import { answeredTags, attentionSoundEnabled, badgeText, batchDue, finishedBatch, detectNewCards, detectTransitions, pickEvent, rateLimiter, SETTLE_MS, settledTags, suppressed, tabTitle, trackTurns, type Turn } from "./attention";
import { claimNotification, closeNotification, dismissNotification, releaseClaim, FRONT_BEAT_MS, inQuietHours, loadNotifyPrefs, markFront, notificationText, notifyPrefs, waitingCard, otherTabInFront, postNotification, systemNotifyAllowed, wants, managerMuted } from "./notify";
import { defaultManager } from "./manager-choice";
import { failedTagsOver, listenForNotificationClicks } from "./notification-answer";
import { isMuted } from "./session-actions";
import { href, navigate } from "./router";
import { needsFieldValues } from "./quick-answers";
import { t } from "../i18n";
import { requestRoute } from "./card-focus";
import type { AgentState, Interaction, Session } from "./types";

// ── favicon ──────────────────────────────────────────────

const ICONS = 'link[rel="icon"]';
const SIZE = 64; // the icon files are 64 px; the browser scales the tab's down
const decoded = new Map<string, Promise<HTMLImageElement>>();
const drawn = new Map<string, string>();

function load(src: string): Promise<HTMLImageElement> {
  let p = decoded.get(src);
  if (!p) {
    p = new Promise((resolve, reject) => {
      const img = new Image();
      img.onload = () => resolve(img);
      img.onerror = () => reject(new Error("icon"));
      img.src = src;
    });
    decoded.set(src, p);
    p.catch(() => decoded.delete(src));
  }
  return p;
}

/** The icon with the "Needs you" badge at its top right: a red disc (the sidebar's --status-needs) with the count. */
async function badged(src: string, text: string): Promise<string> {
  const key = `${src}|${text}`;
  const hit = drawn.get(key);
  if (hit) return hit;
  const img = await load(src);
  const canvas = document.createElement("canvas");
  canvas.width = canvas.height = SIZE;
  const g = canvas.getContext("2d");
  if (!g) throw new Error("canvas");
  g.drawImage(img, 0, 0, SIZE, SIZE);
  const css = getComputedStyle(document.documentElement);
  const r = SIZE * 0.34;
  const cx = SIZE - r;
  const cy = r;
  g.beginPath();
  g.arc(cx, cy, r, 0, Math.PI * 2);
  g.fillStyle = css.getPropertyValue("--status-needs").trim() || css.getPropertyValue("--red").trim() || "red";
  g.fill();
  g.lineWidth = SIZE * 0.05;
  g.strokeStyle = css.getPropertyValue("--surface").trim() || "white";
  g.stroke();
  g.fillStyle = "white";
  g.font = `700 ${text.length > 1 ? r * 1.05 : r * 1.35}px system-ui, sans-serif`;
  g.textAlign = "center";
  g.textBaseline = "middle";
  g.fillText(text, cx, cy + r * 0.06);
  const url = canvas.toDataURL("image/png");
  drawn.set(key, url);
  return url;
}

let faviconSeq = 0;
let faviconNeeds = 0;

/** The look's icon (lib/look-icon.ts, #3200) under the badge: each link's original becomes the look's picture for its
 *  scheme (index.html's light and dark links), or the files index.html names again with null. */
export function setFaviconBase(icon: { light: string; dark: string } | null): void {
  for (const link of document.querySelectorAll<HTMLLinkElement>(ICONS)) {
    link.dataset.file ??= link.dataset.original ?? link.getAttribute("href") ?? "";
    const dark = (link.getAttribute("media") ?? "").includes("dark");
    link.dataset.original = icon ? (dark ? icon.dark : icon.light) : link.dataset.file;
  }
  setFaviconBadge(faviconNeeds);
}

/** Swaps the page's icon links (index.html has a light and a dark one, chosen by the browser's media query) for the
 *  badged copy of each, or back to the originals at 0. The files themselves come through the service worker like any
 *  other icon (lib/sw-routes.ts "static": the hub's, else the cached one), so this also works offline; the drawn
 *  copies are data: URLs and never touch the network or the cache. */
export function setFaviconBadge(needs: number): void {
  faviconNeeds = needs;
  const links = [...document.querySelectorAll<HTMLLinkElement>(ICONS)];
  const text = badgeText(needs);
  const seq = ++faviconSeq;
  for (const link of links) {
    link.dataset.original ??= link.getAttribute("href") ?? "";
    const original = link.dataset.original;
    if (!text) {
      link.setAttribute("href", original);
      continue;
    }
    badged(original, text).then(
      (url) => seq === faviconSeq && link.setAttribute("href", url),
      () => seq === faviconSeq && link.setAttribute("href", original), // the icon can't be read: leave it as it is
    );
  }
}

// ── sounds ───────────────────────────────────────────────

let ctx: AudioContext | undefined;
let gestured = false;

/** Browsers only start audio after a user gesture: until one has happened nothing plays (and nothing is queued). */
export function armAudio(): void {
  if (typeof window === "undefined") return;
  const unlock = () => {
    gestured = true;
    try {
      ctx ??= new AudioContext();
      void ctx.resume();
    } catch {
      // no WebAudio here: silent
    }
  };
  for (const ev of ["pointerdown", "keydown", "touchend"]) window.addEventListener(ev, unlock, { once: true, capture: true });
}

// [frequency Hz, start s, length s, peak gain]
type Note = [number, number, number, number];
/** "Needs you": two rising notes, clear. */
const CHIME: Note[] = [[784, 0, 0.22, 0.16], [1175, 0.14, 0.34, 0.16]];
/** "Done": one low, soft note. */
const DONE: Note[] = [[523, 0, 0.4, 0.07]];

export function play(kind: "needs" | "finished"): void {
  if (!gestured || !ctx || ctx.state !== "running") return;
  const t0 = ctx.currentTime;
  for (const [freq, at, len, peak] of kind === "needs" ? CHIME : DONE) {
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.type = "sine";
    osc.frequency.value = freq;
    gain.gain.setValueAtTime(0.0001, t0 + at);
    gain.gain.exponentialRampToValueAtTime(peak, t0 + at + 0.02);
    gain.gain.exponentialRampToValueAtTime(0.0001, t0 + at + len);
    osc.connect(gain).connect(ctx.destination);
    osc.start(t0 + at);
    osc.stop(t0 + at + len + 0.05);
  }
}

// ── the hook ─────────────────────────────────────────────

/** The page is the one in front: its tab is showing and its window has the focus. */
function inFront(): boolean {
  return document.visibilityState === "visible" && document.hasFocus();
}

const ASK_TITLE = {
  question: "notify.question_title",
  plan: "notify.plan_title",
  permission: "notify.permission_title",
  screen: "notify.screen_title",
  elicitation: "notify.elicitation_title",
} as const;

/** No notification goes out from any tab now: they are off, or one of the tabs is in front (that one's title, badge and
 *  sounds cover it) and "Always show system notifications" is off (#2451). */
const unannounced = () => !systemNotifyAllowed({ here: inFront(), other: otherTabInFront() });

/** A system notification for the event (Settings › Notifications): only while unannounced says so, and once for all
 *  the open tabs. Clicking it opens the agent's conversation. `card` is the new
 *  card of a session that was already "Needs you" (#4058): it replaces the agent's earlier "Needs you" one (same tag), and the tabs
 *  share it by the card, so a transition just posted does not keep it back. Undefined when it was not posted, else
 *  the card a "Needs you" one is about. */
function notify(s: Session | undefined, kind: "needs" | "finished", card?: Interaction): { about?: string } | undefined {
  if (unannounced()) return;
  if (!s && card?.session_id !== "") return;
  const tag = `${s ? agentKey(s) : `card:${card!.id}`}#${kind}`;
  if (!claimNotification(card ? `${tag}@${card.id}` : tag)) return;
  // "Needs you": what the card asks, not the agent's last words (#4056); the hub tells the card before the session's state
  const waiting = kind === "needs" ? { ix: card ?? (s ? waitingCard(getState().interactions, s) : undefined), reason: s ? attentionReason(s) : undefined } : {};
  const { title, body } = notificationText(
    s ?? { title: card?.agent_label || card?.session_title || "", cwd: card?.cwd ?? "", last_text: "" },
    kind,
    {
      needs: (name) => t("notify.needs_title", { name }),
      finished: (name) => t("notify.finished_title", { name }),
      needsBody: t("notify.needs_body"),
      finishedBody: t("notify.finished_body"),
      asks: (ask, name) => t(ASK_TITLE[ask], { name }),
    },
    waiting,
  );
  const route = card ? requestRoute(card) : { name: "session", mid: s!.machine_id, sid: s!.session_id } as const;
  const icon = document.querySelector<HTMLLinkElement>(ICONS)?.dataset.original ?? document.querySelector<HTMLLinkElement>(ICONS)?.href;
  // a permission card's gets Allow / Deny (#4464), when the session can be answered from here and nothing has to be
  // filled in first (fields: as the hub's push and the overview's quick answers); the rest need the conversation
  const ix = waiting.ix?.kind === "permission" && !needsFieldValues(waiting.ix) && interactionCanAnswer(waiting.ix, s) ? waiting.ix.id : undefined;
  // inside quiet hours a "Needs you" one comes without a sound (a finished one doesn't come at all: wants)
  postNotification({ title, body, tag, url: href(route), icon, ix, silent: inQuietHours(notifyPrefs()) }, () => navigate(route));
  return { about: waiting.ix?.id };
}

/** The tag of the one notification for several finished turns (#4945): the next batch replaces it. */
const BATCH_TAG = "finished#batch";
/** What the tabs claim a batch by: its agents, so the tabs post one batch once. */
const batchClaim = (keys: readonly string[]) => `${BATCH_TAG}@${[...keys].sort().join(",")}`;

/** One notification for several finished turns (#4945), under the same rules as notify's; clicking it opens the overview.
 *  `names` are the first few of `keys` (finishedBatch), `more` that there are others. The caller checked unannounced. */
function notifyBatch(keys: readonly string[], names: readonly string[], more: boolean): void {
  // this tab's own single ones are told in the batch now; their claims stay, another tab may hold them
  for (const k of keys) dismissNotification(`${k}#finished`);
  // the tabs see the same batch: the first to post it does
  if (!claimNotification(batchClaim(keys))) return;
  const list = names.join(t("notify.finished_batch_separator"));
  const title = t("notify.finished_batch_title", { count: keys.length, names: more ? t("notify.finished_batch_more", { names: list }) : list });
  const route = { name: "home" } as const;
  const icon = document.querySelector<HTMLLinkElement>(ICONS)?.dataset.original ?? document.querySelector<HTMLLinkElement>(ICONS)?.href;
  postNotification({ title, body: t("notify.finished_batch_body"), tag: BATCH_TAG, url: href(route), icon }, () => navigate(route));
}

/**
 * The tab title (`label` is the page's label, as App works it out), the favicon badge, the sounds and the system
 * notifications. `pane` is the agent conversation on the page, if any: it is not announced while the window is focused.
 * Sounds and notifications follow this browser's Settings › Notifications (lib/notify.ts): "Needs you" by its switch, "Done" by its three
 * choices, a turn's length as the page saw it (trackTurns).
 */
export function useAttentionSignals(label: string, pane: { mid: string; sid: string } | undefined): void {
  const needs = useCounts().badge;
  useEffect(() => {
    document.title = tabTitle(label, needs);
  }, [label, needs]);
  useEffect(() => setFaviconBadge(needs), [needs]);

  useEffect(armAudio, []);
  // Allow / Deny clicked on a permission card's notification: the service worker hands it to this page (#4464)
  useEffect(listenForNotificationClicks, []);

  // Tells the other tabs whether this one is in front (notify: none of them posts then).
  useEffect(() => {
    const mark = () => markFront(inFront());
    mark();
    const beat = setInterval(() => inFront() && markFront(true), FRONT_BEAT_MS);
    const away = () => markFront(false);
    window.addEventListener("focus", mark);
    window.addEventListener("blur", mark);
    document.addEventListener("visibilitychange", mark);
    window.addEventListener("pagehide", away);
    return () => {
      clearInterval(beat);
      away();
      window.removeEventListener("focus", mark);
      window.removeEventListener("blur", mark);
      document.removeEventListener("visibilitychange", mark);
      window.removeEventListener("pagehide", away);
    };
  }, []);

  const sessions = useAgentSessions();
  const loaded = useStore((s) => s.loaded);
  const interactions = useStore((s) => s.interactions);
  useEffect(() => {
    if (loaded) loadNotifyPrefs().catch(() => {}); // the cached copy stays when the hub can't say
  }, [loaded]);
  const key = pane ? agentKey({ machine_id: pane.mid, session_id: pane.sid }) : undefined;
  // read when an event fires, not when the effect ran
  const viewing = useRef(key);
  viewing.current = key;
  const prev = useRef<ReadonlyMap<string, AgentState>>(new Map());
  const turns = useRef<ReadonlyMap<string, Turn>>(new Map());
  const allow = useRef(rateLimiter());
  const latest = useRef(sessions);
  latest.current = sessions;
  const timers = useRef(new Set<ReturnType<typeof setTimeout>>());
  // the card ids seen pending (undefined before the first run), the card each "Needs you" notification is about, and the
  // sessions whose "Needs you" came before any card (its first card is the same wait, CardEvent.follows)
  const cards = useRef<ReadonlyMap<string, number> | undefined>(undefined);
  const announced = useRef(new Map<string, string>());
  const bare = useRef(new Set<string>());
  // finished turns gathering for one notification (#4945), and the agents the finished notification on screen names
  const batch = useRef<{ keys: string[]; firstAt: number; timer?: ReturnType<typeof setTimeout> }>({ keys: [], firstAt: 0 });
  const batched = useRef<string[]>([]);

  useEffect(() => {
    if (!loaded) return;
    const now = Date.now();
    const { next, events } = detectTransitions(prev.current, sessions, getState().mutes, interactions);
    // a session already "Needs you" that gets another card speaks again (#4058)
    const fresh = detectNewCards(cards.current, interactions, prev.current, next, sessions, bare.current, now);
    cards.current = fresh.ids;
    for (const k of bare.current) if (next.get(k) !== "needs_you") bare.current.delete(k);
    for (const e of fresh.events) bare.current.delete(e.key);
    for (const e of events) if (e.kind === "needs" && !interactions.some((ix) => homeKey(ix) === e.key)) bare.current.add(e.key);
    // answered or seen, here or elsewhere: its system notification goes too (#3458); and one about a card that is gone,
    // while the session waits on another; and a Could not allow one about a card that is gone
    const settled = new Set([...settledTags(prev.current, next), ...answeredTags(announced.current, interactions), ...failedTagsOver(interactions)]);
    settled.forEach(closeNotification);
    // every agent the batch notification names was seen (or is gone): it goes too (a single one goes by settledTags)
    if (batched.current.length && !batched.current.some((k) => next.get(k) === "unread")) {
      releaseClaim(batchClaim(batched.current)); // the same agents finishing again post again
      batched.current = [];
      closeNotification(BATCH_TAG);
    }
    for (const tag of settled) if (tag.endsWith("#needs")) announced.current.delete(tag.slice(0, -"#needs".length));
    prev.current = next;
    turns.current = trackTurns(turns.current, sessions, now);
    const prefs = notifyPrefs();
    const ran = (k: string) => {
      const turn = turns.current.get(k);
      return turn?.endedAt !== undefined ? turn.endedAt - turn.start : undefined;
    };
    // a manager this device turned off (Settings › Managers) says nothing here either, as its pushes don't reach the phone
    const ownMuted = (e: { key: string; card?: Interaction }) => managerMuted(prefs, e.card?.manager ?? latest.current.find((x) => agentKey(x) === e.key)?.manager, defaultManager());
    const wanted = [...events, ...fresh.events].filter((e) => wants(e.kind, prefs, e.kind === "finished" ? ran(e.key) : undefined) && !ownMuted(e));
    const sound = attentionSoundEnabled();
    const speak = (kind: "needs" | "finished", k: string) => {
      if (!sound || inQuietHours(prefs) || suppressed(k, { key: viewing.current, focused: inFront() })) return;
      if (allow.current(Date.now())) play(kind);
    };
    const find = (k: string) => latest.current.find((x) => agentKey(x) === k);
    // The finished turns of a batch make one notification (#4945): one alone is its own, as before.
    const flush = () => {
      const keys = batch.current.keys;
      batch.current = { keys: [], firstAt: 0 };
      const unread = new Map(latest.current.filter((x) => stateOf(x) === "unread").map((x) => [agentKey(x), sessionTitle(x)]));
      if (finishedBatch(keys, unread).kind === "none") return;
      // it replaces the batch notification on screen: the agents that one named and are still unseen stay in it
      const shown = finishedBatch([...batched.current, ...keys], unread);
      if (shown.kind === "none" || unannounced()) return;
      // remembered whichever tab won the claim, so every tab forms the next batch from the same agents
      if (shown.kind === "one") {
        const s = find(shown.key);
        if (!s) return;
        notify(s, "finished");
        batched.current = [shown.key]; // a batch after it, while it is unseen, takes it in
      } else {
        notifyBatch(shown.keys, shown.names, shown.more);
        batched.current = shown.keys;
      }
    };
    const gather = (k: string) => {
      const at = Date.now();
      const b = batch.current;
      if (!b.keys.length) b.firstAt = at;
      b.keys.push(k);
      if (b.timer) {
        clearTimeout(b.timer);
        timers.current.delete(b.timer);
      }
      const timer = setTimeout(() => {
        timers.current.delete(timer);
        flush();
      }, batchDue(b.firstAt, at) - at);
      b.timer = timer;
      timers.current.add(timer);
    };
    const urgent = wanted.filter((e) => e.kind === "needs");
    // a card that follows its session's "Needs you" rewrites that notification without a second sound
    const first = pickEvent(urgent.filter((e) => !e.follows));
    if (first) speak(first.kind, first.key);
    // The sound of "Needs you" does not look at mutes (detectTransitions); the notification does, as the phone's push and
    // the Mac's do (hub announce, MacAttention.transitions).
    for (const e of urgent) {
      const s = find(e.key);
      if (s ? isMuted(s, getState().mutes) : e.card?.session_id !== "") continue;
      const about = notify(s, "needs", e.card)?.about;
      if (about) announced.current.set(e.key, about);
    }
    // A finished turn speaks once it has stayed finished (the status flaps working → done → working around a turn).
    // No sound when a "Needs you" one is going out with it: that one is the more urgent.
    for (const done of wanted.filter((e) => e.kind === "finished")) {
      const t = setTimeout(() => {
        timers.current.delete(t);
        const s = find(done.key);
        if (!s || stateOf(s) !== "unread") return;
        if (!first) speak("finished", done.key);
        gather(done.key);
      }, SETTLE_MS);
      timers.current.add(t);
    }
  }, [sessions, interactions, loaded]);

  useEffect(() => {
    const pending = timers.current;
    const gathering = batch;
    return () => {
      pending.forEach(clearTimeout);
      gathering.current = { keys: [], firstAt: 0 }; // nothing gathered is posted by a later mount
    };
  }, []);
}

/**
 * `useAttentionSignals` as a component that draws nothing. It reads every session push (the "Needs you" count, the
 * transitions): rendered as a leaf, a push re-renders only it, not the shell with the sidebar and the page around it.
 */
export function AttentionSignals({ label, pane }: { label: string; pane: { mid: string; sid: string } | undefined }): null {
  useAttentionSignals(label, pane);
  return null;
}
