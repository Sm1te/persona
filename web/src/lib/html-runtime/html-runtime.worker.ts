// An html card's worker (#4215; started by host.ts, one per running card, terminated from outside when it stops).
// It runs the card's code in QuickJS with the guest's small DOM (guest.js) and sends the page what the card drew.
// The card's code reaches nothing of this worker: QuickJS gives it only the ECMAScript built-ins and what guest.js
// builds on the host functions below. The hub serves this file with a CSP that lets it load and connect to nothing
// (server/internal/hub/web.go), it is one file with QuickJS built in, the WebAssembly comes compiled from the page,
// and the network globals are removed once QuickJS is up.
import { QuickJSFFI } from "@jitl/quickjs-wasmfile-release-sync/ffi";
import moduleLoader from "@jitl/quickjs-wasmfile-release-sync/emscripten-module";
import { newQuickJSWASMModuleFromVariant, newVariant, type QuickJSContext, type QuickJSHandle, type QuickJSRuntime, type QuickJSSyncVariant } from "quickjs-emscripten-core";
import GUEST from "./guest.js?raw";
import { BATCH_BYTES_MOST, BUDGET_MS, BUDGET_WINDOW_MS, FRAME_MS, HEAP_BYTES, STACK_BYTES, TIMERS_MOST, TIMER_MIN_MS, TURN_MS } from "./limits";
import { canvasFits } from "./sanitize";
import type { CardInfo, FromWorker, StopReason, ToWorker } from "./protocol";

const post = (m: FromWorker, transfer: Transferable[] = []) => (self as unknown as Worker).postMessage(m, transfer);

// The release build's variant with its parts imported here, not loaded later: the worker is one file that loads nothing
// (its CSP is default-src 'none'), and the WebAssembly comes compiled from the page.
const releaseVariant: QuickJSSyncVariant = { type: "sync", importFFI: () => Promise.resolve(QuickJSFFI), importModuleLoader: () => Promise.resolve(moduleLoader) };

let rt: QuickJSRuntime | undefined;
let vm: QuickJSContext | undefined;
let api: Record<string, QuickJSHandle> = {};
let info: CardInfo;
let deadline = Infinity;
let stopped = false;
const spent: { at: number; ms: number }[] = [];
const timers = new Map<number, ReturnType<typeof setTimeout>>();
let errors = 0;

function stop(reason: StopReason, detail?: string) {
  if (stopped) return;
  stopped = true;
  for (const t of timers.values()) clearTimeout(t);
  timers.clear();
  post({ type: "stop", reason, ...(detail ? { detail } : {}) });
}

function report(message: string) {
  if (++errors > 20) return;
  post({ type: "error", message: message.slice(0, 500) });
}

/** Why a turn ended badly: the interrupt (too long), out of memory, or an error of the card's. */
function failure(e: unknown): StopReason | null {
  const text = typeof e === "object" && e !== null ? `${(e as { name?: string }).name ?? ""} ${(e as { message?: string }).message ?? ""}` : String(e);
  if (/interrupted/i.test(text)) return "time";
  if (/out of memory/i.test(text)) return "memory";
  return null;
}

/** One turn of the card's code: a call into the guest, its pending jobs, then what it drew goes to the page. */
function turn(method: string, args: QuickJSHandle[] = [], budget = TURN_MS): unknown {
  if (stopped || !vm || !rt) return undefined;
  const start = performance.now();
  deadline = start + budget;
  let value: unknown;
  try {
    const res = vm.callFunction(api[method], vm.undefined, ...args);
    if (res.error) {
      const e = vm.dump(res.error);
      res.error.dispose();
      const why = failure(e);
      if (why) return stop(why);
      report(typeof e === "object" && e && "message" in e ? String((e as { message: unknown }).message) : String(e));
    } else {
      value = vm.dump(res.value);
      res.value.dispose();
    }
    const jobs = rt.executePendingJobs(-1);
    if (jobs.error) {
      const e = jobs.error.context.dump(jobs.error);
      jobs.error.dispose();
      const why = failure(e);
      if (why) return stop(why);
      report(String((e as { message?: unknown })?.message ?? e));
    }
    flush();
  } catch (e) {
    // QuickJS itself gave out (its memory, a trap): the runtime is done
    return stop(failure(e) ?? "crash", String(e).slice(0, 200));
  } finally {
    for (const a of args) if (a !== vm?.true && a !== vm?.false && a !== vm?.undefined && a.alive) a.dispose();
    deadline = Infinity;
    account(performance.now() - start);
  }
  return value;
}

function account(ms: number) {
  const now = performance.now();
  spent.push({ at: now, ms });
  while (spent.length && spent[0].at < now - BUDGET_WINDOW_MS) spent.shift();
  if (spent.reduce((a, s) => a + s.ms, 0) > BUDGET_MS) stop("time");
}

function flush() {
  if (!vm || stopped) return;
  const res = vm.callFunction(api.flush, vm.undefined);
  if (res.error) {
    const e = vm.dump(res.error);
    res.error.dispose();
    return stop(failure(e) ?? "crash");
  }
  const ops = vm.typeof(res.value) === "string" ? vm.getString(res.value) : "";
  res.value.dispose();
  if (ops.length > BATCH_BYTES_MOST) return stop("size");
  if (ops) post({ type: "ops", ops });
  for (const id of dirty) void showCanvas(id);
  dirty.clear();
}

// ── canvases ──
const C2D_METHODS = new Set(["fillRect", "strokeRect", "clearRect", "beginPath", "closePath", "moveTo", "lineTo", "arc", "arcTo", "bezierCurveTo", "quadraticCurveTo", "rect", "roundRect", "ellipse", "fill", "stroke", "clip", "save", "restore", "translate", "rotate", "scale", "transform", "setTransform", "resetTransform", "fillText", "strokeText", "setLineDash", "reset"]);
const C2D_PROPS = new Set(["fillStyle", "strokeStyle", "lineWidth", "font", "textAlign", "textBaseline", "globalAlpha", "lineCap", "lineJoin", "miterLimit", "lineDashOffset", "shadowColor", "shadowBlur", "shadowOffsetX", "shadowOffsetY", "globalCompositeOperation", "imageSmoothingEnabled", "direction", "filter", "letterSpacing", "wordSpacing", "fontKerning"]);
type Ctx = OffscreenCanvasRenderingContext2D;
const canvases = new Map<number, Ctx>();
const dirty = new Set<number>();
type GradientSpec = { g: number; kind: string; args: number[]; stops: [number, string][] };

function canvasFor(id: number, w: number, h: number): Ctx | null {
  if (typeof OffscreenCanvas === "undefined") return null;
  if (!canvasFits(w, h)) {
    stop("size");
    return null;
  }
  let ctx = canvases.get(id);
  if (!ctx || ctx.canvas.width !== w || ctx.canvas.height !== h) {
    ctx = new OffscreenCanvas(Math.max(1, w), Math.max(1, h)).getContext("2d") ?? undefined;
    if (!ctx) return null;
    canvases.set(id, ctx);
  }
  return ctx;
}

function gradient(ctx: Ctx, s: GradientSpec): CanvasGradient | null {
  const a = s.args.map(Number);
  let g: CanvasGradient | null = null;
  try {
    if (s.kind === "linear" && a.length >= 4) g = ctx.createLinearGradient(a[0], a[1], a[2], a[3]);
    else if (s.kind === "radial" && a.length >= 6) g = ctx.createRadialGradient(a[0], a[1], a[2], a[3], a[4], a[5]);
    else if (s.kind === "conic" && a.length >= 3 && "createConicGradient" in ctx) g = ctx.createConicGradient(a[0], a[1], a[2]);
    for (const [o, c] of s.stops ?? []) g?.addColorStop(Math.min(1, Math.max(0, Number(o))), String(c));
  } catch {
    return null;
  }
  return g;
}

function drawCanvases(json: string) {
  let all: Record<string, { w: number; h: number; cmds: unknown[][] }>;
  try {
    all = JSON.parse(json);
  } catch {
    return;
  }
  for (const [key, c] of Object.entries(all)) {
    const id = Number(key);
    let ctx = canvasFor(id, Number(c.w), Number(c.h));
    if (!ctx) continue;
    for (const cmd of Array.isArray(c.cmds) ? c.cmds : []) {
      if (!Array.isArray(cmd) || !ctx) continue;
      const cx: Ctx = ctx;
      const [name, ...args] = cmd as [string, ...unknown[]];
      try {
        if (name === "#size") {
          canvases.delete(id);
          ctx = canvasFor(id, Number(args[0]), Number(args[1]));
          if (!ctx) break;
        } else if (name === "=" && C2D_PROPS.has(String(args[0]))) {
          const v = args[1];
          const value = v && typeof v === "object" ? gradient(cx, v as GradientSpec) : v;
          if (value !== null) (cx as unknown as Record<string, unknown>)[String(args[0])] = value;
        } else if (C2D_METHODS.has(name)) {
          const fn = (cx as unknown as Record<string, unknown>)[name];
          if (typeof fn === "function") fn.apply(cx, args.map((a) => (a && typeof a === "object" && !Array.isArray(a) ? gradient(cx, a as GradientSpec) : a)));
        }
      } catch {
        /* a bad call draws nothing, as in a browser */
      }
    }
    dirty.add(id);
  }
}

async function showCanvas(id: number) {
  const ctx = canvases.get(id);
  if (!ctx || stopped) return;
  try {
    const bitmap = await createImageBitmap(ctx.canvas);
    post({ type: "bitmap", id, bitmap }, [bitmap]);
  } catch {
    /* nothing to show yet */
  }
}

let measurer: Ctx | null = null;
function measure(font: string, text: string): string {
  measurer ??= typeof OffscreenCanvas === "undefined" ? null : new OffscreenCanvas(1, 1).getContext("2d");
  if (!measurer) return JSON.stringify({ width: text.length * 6 });
  measurer.font = font;
  const m = measurer.measureText(text);
  return JSON.stringify({
    width: m.width,
    actualBoundingBoxLeft: m.actualBoundingBoxLeft,
    actualBoundingBoxRight: m.actualBoundingBoxRight,
    actualBoundingBoxAscent: m.actualBoundingBoxAscent,
    actualBoundingBoxDescent: m.actualBoundingBoxDescent,
    fontBoundingBoxAscent: m.fontBoundingBoxAscent,
    fontBoundingBoxDescent: m.fontBoundingBoxDescent,
  });
}

// ── Intl, which QuickJS lacks: the worker's own ──
const formats = new Map<string, (v: number) => string>();
function intl(kind: string, locale: string, opts: string, v: string): string {
  const key = `${kind}|${locale}|${opts}`;
  let f = formats.get(key);
  if (!f) {
    try {
      const loc = locale || info.lang;
      const o = JSON.parse(opts) as Record<string, unknown>;
      if (kind === "number") {
        const nf = new Intl.NumberFormat(loc, o);
        f = (x) => nf.format(x);
      } else if (kind === "date") {
        const df = new Intl.DateTimeFormat(loc, o);
        f = (x) => df.format(new Date(x));
      } else {
        const { unit, ...rest } = o;
        const rf = new Intl.RelativeTimeFormat(loc, rest);
        f = (x) => rf.format(x, String(unit) as Intl.RelativeTimeFormatUnit);
      }
    } catch {
      f = (x) => String(x);
    }
    if (formats.size > 200) formats.clear();
    formats.set(key, f);
  }
  try {
    return f(Number(v));
  } catch {
    return v;
  }
}

// ── start ──

/** What a request could be made with, gone from this worker once QuickJS no longer needs to load anything. */
function harden() {
  const g = self as unknown as Record<string, unknown>;
  for (const k of ["fetch", "XMLHttpRequest", "WebSocket", "EventSource", "importScripts", "WebTransport", "BroadcastChannel", "indexedDB", "caches", "Worker", "SharedWorker", "Request"]) {
    try {
      delete g[k];
    } catch {
      /* not configurable */
    }
    try {
      Object.defineProperty(g, k, { value: undefined, configurable: false, writable: false });
    } catch {
      /* not configurable */
    }
  }
}

async function start(m: Extract<ToWorker, { type: "start" }>) {
  info = m.info;
  const QuickJS = await newQuickJSWASMModuleFromVariant(newVariant(releaseVariant, { wasmModule: m.module }));
  harden();
  rt = QuickJS.newRuntime();
  rt.setMemoryLimit(HEAP_BYTES);
  rt.setMaxStackSize(STACK_BYTES);
  rt.setInterruptHandler(() => performance.now() > deadline);
  vm = rt.newContext();
  const c = vm;
  const str = (h: QuickJSHandle) => (c.typeof(h) === "string" ? c.getString(h) : "");
  const num = (h: QuickJSHandle) => (c.typeof(h) === "number" ? c.getNumber(h) : 0);
  const host = c.newObject();
  const fn = (name: string, impl: (...a: QuickJSHandle[]) => QuickJSHandle | void) => {
    const f = c.newFunction(name, impl);
    c.setProp(host, name, f);
    f.dispose();
  };
  fn("info", () => c.newString(JSON.stringify(info)));
  fn("now", () => c.newNumber(performance.now()));
  fn("timer", (id, ms) => schedule(num(id), num(ms)));
  fn("clearTimer", (id) => cancel(num(id)));
  fn("bridge", (id, kind, value) => {
    const k = str(kind);
    if (k === "reply" || k === "open") post({ type: "bridge", id: num(id), kind: k, value: str(value) });
  });
  fn("error", (msg) => report(str(msg)));
  fn("log", () => {});
  fn("canvas", (json) => drawCanvases(str(json)));
  fn("measure", (_id, font, text) => c.newString(measure(str(font), str(text))));
  fn("intl", (kind, locale, opts, v) => c.newString(intl(str(kind), str(locale), str(opts), str(v))));
  const limits = c.newObject();
  for (const [k, v] of [["timers", TIMERS_MOST], ["timerMin", TIMER_MIN_MS], ["frame", FRAME_MS]] as const) {
    const h = c.newNumber(v);
    c.setProp(limits, k, h);
    h.dispose();
  }
  c.setProp(host, "limits", limits);
  limits.dispose();

  // the guest's world, built once (ours: a longer budget than a turn of the card's)
  deadline = performance.now() + 5000;
  const boot = c.evalCode(GUEST, "persona-card-runtime.js");
  if (boot.error) {
    const e = c.dump(boot.error);
    boot.error.dispose();
    return stop("crash", String(e?.message ?? e));
  }
  const made = c.callFunction(boot.value, c.undefined, host);
  boot.value.dispose();
  host.dispose();
  deadline = Infinity;
  if (made.error) {
    const e = c.dump(made.error);
    made.error.dispose();
    return stop("crash", String(e?.message ?? e));
  }
  for (const k of ["load", "run", "loaded", "event", "fire", "settle", "resize", "flush"]) api[k] = c.getProp(made.value, k);
  made.value.dispose();

  const scripts = Number(turn("load", [c.newString(m.html)], 1000)) || 0;
  for (let i = 0; i < scripts && !stopped; i++) turn("run", [c.newNumber(i)]);
  turn("loaded");
  if (!stopped) post({ type: "ready" });
}

function schedule(id: number, ms: number) {
  cancel(id);
  if (stopped || timers.size >= TIMERS_MOST * 2) return;
  timers.set(id, setTimeout(() => {
    timers.delete(id);
    if (vm) turn("fire", [vm.newNumber(id)]);
  }, Math.max(TIMER_MIN_MS, ms)));
}

function cancel(id: number) {
  const t = timers.get(id);
  if (t !== undefined) clearTimeout(t);
  timers.delete(id);
}

self.onmessage = (e: MessageEvent<ToWorker>) => {
  const m = e.data;
  switch (m?.type) {
    case "start":
      start(m).catch((err) => stop("crash", String(err).slice(0, 200)));
      return;
    case "ping":
      post({ type: "pong", n: m.n });
      return;
    case "event":
      if (vm) turn("event", [vm.newString(JSON.stringify(m.event))]);
      return;
    case "settle":
      if (vm) turn("settle", [vm.newNumber(m.id), m.ok ? vm.true : vm.false]);
      return;
    case "resize":
      if (vm) {
        info = { ...info, width: m.width, height: m.height };
        turn("resize", [vm.newNumber(m.width), vm.newNumber(m.height)]);
      }
      return;
  }
};
