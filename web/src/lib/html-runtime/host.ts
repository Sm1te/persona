// Runs one html card (#4215, docs/assistant/assistant-views.md "Free-form HTML rendering"): its code in a worker
// (html-runtime.worker.ts, QuickJS), its drawing in a frame of an opaque origin (sandbox="allow-scripts" only) whose
// CSP lets nothing load and runs nothing but its own script (frame.js, by nonce). Every operation is checked here on
// the way through (sanitize.ts). The worker is terminated from here when the card stops, when it takes too long to
// answer a ping, or when it draws past the bounds (limits.ts).
//
// Why the frame runs a script of its own: WebKit runs no listener in a frame without allow-scripts, the page's
// included, so a script-less frame can't tell the page what the user did there.
import wasmUrl from "@jitl/quickjs-wasmfile-release-sync/wasm?url";
import { BATCHES_PER_SECOND_MOST, FRAME_EVENTS_PER_SECOND_MOST, FRAME_MESSAGE_BYTES_MOST, PING_MS, RUNNING_NARROW, RUNNING_WIDE, STALL_MS } from "./limits";
import FRAME_SCRIPT from "./frame.js?raw";
import { CONTENT_MOST } from "./height";
import type { CardInfo, FromWorker, GuestEvent, StopReason, ToWorker } from "./protocol";
import { FRAME_SANDBOX, frameNonce, frameShell, LimitError, Sanitizer, type Clean } from "./sanitize";

let compiled: Promise<WebAssembly.Module> | null = null;

/** QuickJS, compiled once on the page and handed to every card's worker (which itself loads nothing). */
export function runtimeModule(): Promise<WebAssembly.Module> {
  compiled ??= (async () => {
    const res = await fetch(wasmUrl);
    if (!res.ok) throw new Error(`QuickJS: ${res.status}`);
    try {
      return await WebAssembly.compileStreaming(res.clone());
    } catch {
      return WebAssembly.compile(await res.arrayBuffer());
    }
  })();
  compiled.catch(() => (compiled = null));
  return compiled;
}

export type RuntimeState = { kind: "loading" } | { kind: "running" } | { kind: "stopped"; reason: StopReason | "user" };

export interface RunOptions {
  /** An iframe already in the page, its sandbox exactly FRAME_SANDBOX ("allow-scripts"); checked before anything runs. */
  frame: HTMLIFrameElement;
  html: string;
  info: CardInfo;
  /** A bridge call the card made: true when the user confirmed it and it went through. */
  onBridge: (kind: "reply" | "open", value: string) => Promise<boolean>;
  onState: (s: RuntimeState) => void;
  /** How tall what the card draws is, in CSS pixels, whenever that changes (frame.js). */
  onHeight?: (height: number) => void;
  /** The card's own errors (an exception in its code): what the first one says. */
  onError?: (message: string) => void;
  /** Made for tests: another worker (the same code, another URL). */
  worker?: () => Worker;
}

export interface RunningCard {
  stop(): void;
}

const newWorker = () => new Worker(new URL("./html-runtime.worker.ts", import.meta.url), { type: "module", name: "persona-html-card" });

export function runCard(o: RunOptions): RunningCard {
  let worker: Worker | null = null;
  let done = false;
  let watchdog: ReturnType<typeof setInterval> | undefined;
  let resizer: ResizeObserver | undefined;
  const cleanups: (() => void)[] = [];
  const batches: number[] = [];

  const finish = (reason: StopReason | "user") => {
    if (done) return;
    done = true;
    worker?.terminate();
    worker = null;
    if (watchdog) clearInterval(watchdog);
    resizer?.disconnect();
    for (const c of cleanups) c();
    o.onState({ kind: "stopped", reason });
  };

  const post = (m: ToWorker, transfer: Transferable[] = []) => worker?.postMessage(m, transfer);

  (async () => {
    o.onState({ kind: "loading" });
    let module: WebAssembly.Module;
    try {
      module = await runtimeModule();
    } catch {
      return finish("crash");
    }
    if (done) return;
    // the frame must be sandboxed exactly so before anything is drawn in it
    if (o.frame.getAttribute("sandbox") !== FRAME_SANDBOX) return finish("crash");
    const nonce = frameNonce();
    const sanitizer = new Sanitizer();
    let frameReady = false;
    const pending: Clean[][] = [];
    const toFrame = (m: Record<string, unknown>, transfer: Transferable[] = []) => o.frame.contentWindow?.postMessage({ ...m, n: nonce }, "*", transfer);
    // The frame's messages: from this frame's window only (its origin is "null", so never judged by origin), carrying
    // its nonce, in the shape frame.js sends, within a rate. They are what the user did in the card, never a gesture
    // of their own: anything the card then asks for still goes through the native confirmation (ui/HtmlCard.tsx).
    const events: number[] = [];
    const onFrame = (e: MessageEvent) => {
      const m = e.data as { n?: unknown; ready?: unknown; ev?: unknown } | null;
      if (done || e.source !== o.frame.contentWindow || !m || typeof m !== "object" || m.n !== nonce) return;
      const now = performance.now();
      events.push(now);
      while (events.length && events[0] < now - 1000) events.shift();
      if (events.length > FRAME_EVENTS_PER_SECOND_MOST) return;
      if (!frameMessageFits(m)) return;
      if (m.ready === true && !frameReady) {
        frameReady = true;
        for (const ops of pending.splice(0)) toFrame({ ops });
        return;
      }
      const h = (m as { h?: unknown }).h;
      if (typeof h === "number") {
        if (Number.isFinite(h) && h >= 0 && h <= CONTENT_MOST) o.onHeight?.(h);
        return;
      }
      const event = guestEvent(m.ev, sanitizer);
      if (event) post({ type: "event", event });
    };
    window.addEventListener("message", onFrame);
    cleanups.push(() => window.removeEventListener("message", onFrame));
    o.frame.srcdoc = frameShell(o.info.dark, nonce, FRAME_SCRIPT);

    worker = (o.worker ?? newWorker)();
    worker.onerror = () => finish("crash");
    let lastPong = performance.now();
    worker.onmessage = (e: MessageEvent<FromWorker>) => {
      if (done) return;
      const m = e.data;
      switch (m.type) {
        case "ops": {
          const now = performance.now();
          batches.push(now);
          while (batches.length && batches[0] < now - 1000) batches.shift();
          if (batches.length > BATCHES_PER_SECOND_MOST) return finish("size");
          let ops: Clean[];
          try {
            ops = sanitizer.clean(JSON.parse(m.ops));
          } catch (err) {
            return finish(err instanceof LimitError ? "size" : "crash");
          }
          if (!ops.length) return;
          if (frameReady) toFrame({ ops });
          else pending.push(ops);
          return;
        }
        case "bitmap": {
          if (frameReady && sanitizer.tagOf(m.id) === "canvas") toFrame({ bm: m.id, b: m.bitmap }, [m.bitmap]);
          else m.bitmap.close();
          return;
        }
        case "bridge": {
          const { id } = m;
          o.onBridge(m.kind, m.value).then(
            (ok) => !done && post({ type: "settle", id, ok }),
            () => !done && post({ type: "settle", id, ok: false }),
          );
          return;
        }
        case "error":
          o.onError?.(m.message);
          return;
        case "ready":
          o.onState({ kind: "running" });
          return;
        case "stop":
          return finish(m.reason);
        case "pong":
          lastPong = performance.now();
          return;
      }
    };
    let n = 0;
    watchdog = setInterval(() => {
      if (performance.now() - lastPong > STALL_MS) return finish("time");
      post({ type: "ping", n: ++n });
    }, PING_MS);
    if (typeof ResizeObserver !== "undefined") {
      let last = o.info.width;
      resizer = new ResizeObserver(() => {
        const w = Math.round(o.frame.clientWidth);
        if (w && w !== last) post({ type: "resize", width: (last = w), height: o.info.height });
      });
      resizer.observe(o.frame);
    }
    post({ type: "start", module, html: o.html, info: o.info });
  })().catch(() => finish("crash"));

  return { stop: () => finish("user") };
}

const EVENT_TYPES = new Set(["click", "input", "change", "keydown", "toggle", "focus"]);
const MESSAGE_KEYS = new Set(["n", "ready", "ev", "h"]);
const EVENT_KEYS = new Set(["type", "target", "value", "checked", "open", "selectedIndex", "key", "shiftKey", "ctrlKey", "altKey", "metaKey"]);

/** A frame message's shape and size: only the keys frame.js sends, within FRAME_MESSAGE_BYTES_MOST. */
export function frameMessageFits(m: object): boolean {
  if (Object.keys(m).some((k) => !MESSAGE_KEYS.has(k))) return false;
  const h = (m as { h?: unknown }).h;
  if (h !== undefined && typeof h !== "number") return false;
  const ev = (m as { ev?: unknown }).ev;
  if (ev !== undefined && (!ev || typeof ev !== "object" || Object.keys(ev).some((k) => !EVENT_KEYS.has(k)))) return false;
  try {
    return JSON.stringify(m).length <= FRAME_MESSAGE_BYTES_MOST;
  } catch {
    return false;
  }
}

/** What the frame says the user did, as the guest takes it: a known node, the event's fields with their types. */
export function guestEvent(raw: unknown, s: Pick<Sanitizer, "has">): GuestEvent | null {
  if (!raw || typeof raw !== "object") return null;
  const r = raw as Record<string, unknown>;
  const target = Number(r.target);
  if (typeof r.type !== "string" || !EVENT_TYPES.has(r.type) || !Number.isSafeInteger(target) || !s.has(target)) return null;
  const e: GuestEvent = { type: r.type, target };
  if (typeof r.value === "string") e.value = r.value.slice(0, 64 * 1024);
  if (typeof r.checked === "boolean") e.checked = r.checked;
  if (typeof r.open === "boolean") e.open = r.open;
  if (typeof r.selectedIndex === "number" && Number.isSafeInteger(r.selectedIndex)) e.selectedIndex = r.selectedIndex;
  if (typeof r.key === "string") e.key = r.key.slice(0, 32);
  for (const k of ["shiftKey", "ctrlKey", "altKey", "metaKey"] as const) if (typeof r[k] === "boolean") e[k] = r[k] as boolean;
  return e;
}

// ── how many cards run at once ──

interface Slot {
  at: number;
  evict: () => void;
}
const running = new Set<Slot>();

const most = () => (typeof matchMedia === "function" && matchMedia("(max-width: 640px)").matches ? RUNNING_NARROW : RUNNING_WIDE);

/**
 * A place for one more card to run, or null when as many run as may. `force` (the user pressed Run): the card
 * that has run longest gives its place up (`evict`).
 */
export function claimSlot(evict: () => void, force = false): Slot | null {
  if (running.size >= most()) {
    if (!force) return null;
    const oldest = [...running].sort((a, b) => a.at - b.at)[0];
    running.delete(oldest);
    oldest.evict();
  }
  const s = { at: performance.now(), evict };
  running.add(s);
  return s;
}

export function releaseSlot(s: Slot | null | undefined) {
  if (s) running.delete(s);
}

/** How many run now (tests). */
export const runningCount = () => running.size;
