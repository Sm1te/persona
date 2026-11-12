// The html card worker in Node (#4215): the card's code in QuickJS with the guest DOM (guest.js), what it draws as
// operations, the bridge, what it has no way to reach, and how it is stopped (too long a turn, too much memory).
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import type { FromWorker, ToWorker } from "./protocol";

type Self = { postMessage: (m: FromWorker) => void; onmessage?: (e: { data: ToWorker }) => void } & Record<string, unknown>;

let wasm: WebAssembly.Module;
beforeAll(async () => {
  const require = createRequire(import.meta.url);
  wasm = await WebAssembly.compile(readFileSync(require.resolve("@jitl/quickjs-wasmfile-release-sync/wasm")));
});
afterEach(() => {
  vi.resetModules();
  delete (globalThis as Record<string, unknown>).self;
});

/** A worker of its own (a fresh module), started on `html`; resolves once it is ready or stopped. */
async function start(html: string) {
  const got: FromWorker[] = [];
  const self: Self = { postMessage: (m) => got.push(m), fetch: () => "net", XMLHttpRequest: class {}, WebSocket: class {} };
  (globalThis as Record<string, unknown>).self = self;
  await import("./html-runtime.worker");
  const send = (m: ToWorker) => self.onmessage!({ data: m });
  send({ type: "start", module: wasm, html, info: { width: 300, height: 200, dpr: 1, lang: "en", dark: false } });
  await vi.waitFor(() => expect(got.some((m) => m.type === "ready" || m.type === "stop")).toBe(true), { timeout: 10_000 });
  const ops = () => got.flatMap((m) => (m.type === "ops" ? (JSON.parse(m.ops) as unknown[][]) : []));
  return { got, send, self, ops };
}

describe("the card's code in QuickJS", () => {
  it("parses the card, runs its scripts in order and sends what it drew", async () => {
    const w = await start(`<h2 id="t">a</h2><script>document.getElementById("t").textContent += "b";</script><script>document.body.append(Object.assign(document.createElement("p"), { className: "x", textContent: document.querySelectorAll("h2").length }))</script>`);
    expect(w.got.find((m) => m.type === "stop")).toBeUndefined();
    const ops = w.ops();
    const text = JSON.stringify(ops);
    expect(text).toContain('"h2"');
    expect(text).toContain('"ab"');
    expect(text).toContain('["class","x"]');
    expect(text).toContain('"1"');
    // the script elements reach the page as nodes, which it doesn't draw (sanitize.ts)
    expect(text).toContain('"script"');
  });

  it("runs an inline onclick on a click from the page and asks the bridge, which waits for the page's answer", async () => {
    const w = await start(`<button id="b" onclick="persona.reply('go on').then(ok => document.getElementById('b').textContent = ok ? 'sent' : 'no')">x</button>`);
    const tree = w.ops().find((op) => op[0] === "t")![3] as unknown[];
    const button = (JSON.stringify(tree).match(/\[(\d+),"button"/) ?? [])[1];
    w.send({ type: "event", event: { type: "click", target: Number(button) } });
    const ask = w.got.find((m) => m.type === "bridge");
    expect(ask).toMatchObject({ type: "bridge", kind: "reply", value: "go on" });
    w.send({ type: "settle", id: (ask as { id: number }).id, ok: true });
    expect(JSON.stringify(w.ops())).toContain('"sent"');
  });

  it("has no way to the network, the page or storage, and can't replace the bridge", async () => {
    const w = await start(`<p id="o"></p><script>"use strict";
      const out = [typeof fetch, typeof XMLHttpRequest, typeof WebSocket, typeof EventSource, typeof importScripts, typeof Worker,
        typeof indexedDB, typeof localStorage, typeof caches, typeof navigator.sendBeacon, document.cookie, String(location.href)];
      try { persona = null } catch (e) { out.push("persona fixed") }
      try { persona.reply = () => 1 } catch (e) { out.push("reply fixed") }
      try { Object.defineProperty(globalThis, "persona", { value: 1 }) } catch (e) { out.push("not redefined") }
      out.push(typeof persona.reply);
      document.getElementById("o").textContent = out.join(",");
    </script>`);
    expect(JSON.stringify(w.ops())).toContain('"undefined,undefined,undefined,undefined,undefined,undefined,undefined,undefined,undefined,undefined,,about:srcdoc,persona fixed,reply fixed,not redefined,function"');
    // the worker's own network globals are gone once QuickJS is up
    expect(w.self.fetch).toBeUndefined();
    expect(w.self.XMLHttpRequest).toBeUndefined();
  });

  it("is stopped when a turn runs too long, at load or in a handler", async () => {
    const atLoad = await start(`<script>while (true) {}</script>`);
    expect(atLoad.got.at(-1)).toEqual({ type: "stop", reason: "time" });
    vi.resetModules();
    const later = await start(`<button id="b" onclick="for (;;) {}">x</button>`);
    expect(later.got.find((m) => m.type === "ready")).toBeTruthy();
    const button = (JSON.stringify(later.ops()).match(/\[(\d+),"button"/) ?? [])[1];
    later.send({ type: "event", event: { type: "click", target: Number(button) } });
    expect(later.got.at(-1)).toEqual({ type: "stop", reason: "time" });
  }, 20_000);

  it("is stopped when it takes too much memory", async () => {
    const w = await start(`<script>const a = []; for (;;) a.push("x".repeat(1 << 16) + a.length)</script>`);
    expect(w.got.at(-1)).toMatchObject({ type: "stop", reason: expect.stringMatching(/memory|time/) });
  }, 20_000);

  it("answers the page's ping, so the page knows it is alive", async () => {
    const w = await start(`<p>hi</p>`);
    w.send({ type: "ping", n: 7 });
    expect(w.got.at(-1)).toEqual({ type: "pong", n: 7 });
  });
});
