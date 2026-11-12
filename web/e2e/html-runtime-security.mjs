// The web's html card runtime against attacks (#4215, docs/assistant/assistant-views.md "Security boundaries each platform
// must verify"): it counts the requests a card actually makes. A local server on another origin (the observer) counts every
// request and connection it gets, WebSocket upgrades included. Each case runs three ways:
//
//   control  the same HTML as a plain page with scripts on: the observer must see it (or the page must run it), which
//            proves the case and the observer work;
//   card     the HTML as an assistant card, through the whole runtime (QuickJS in a worker, the sanitizer, the display
//            frame): 0 requests, nothing run outside the card's runtime, the frame still about:srcdoc, no new page;
//   raw      (the frame cases) the HTML written straight into the display frame, the sanitizer bypassed: what its
//            sandbox, CSP and own script stop by themselves.
//
// Then the liveness cases (endless loops, timers, memory, a DOM bomb): stopped or contained within 5 s, the page's thread
// never held more than 250 ms; the layout (the frame as tall as what the card draws, within its bounds; a stopped card as
// tall as its words); and the bridge (a flood of calls, forged messages both ways).
//
// Needs the web's dev server (the lab page is dev only), and Edge / Chrome and Playwright's WebKit:
//
//   (cd web && npx vite --port 5997 --strictPort &)
//   node web/e2e/html-runtime-security.mjs http://127.0.0.1:5997 [--browser chromium|webkit|firefox|all] [--channel msedge] [--only <case>]
//
// Prints a line per case and browser and a verdict; exits 1 on a failure.
import http from "node:http";
import { chromium, firefox, webkit } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
if (!base || base.startsWith("--")) {
  console.error("usage: node web/e2e/html-runtime-security.mjs <dev server URL> [--browser chromium|webkit|firefox|all] [--channel msedge] [--only <case>]");
  process.exit(2);
}
const which = flag("--browser") ?? "all";
const channel = flag("--channel") ?? "msedge";
const only = flag("--only");

// ── the observer and the control pages ──
const hits = [];
let connections = 0;
const observer = http.createServer((q, r) => {
  hits.push(q.url);
  const type = q.url.endsWith(".css") ? "text/css" : q.url.endsWith(".js") ? "text/javascript" : "text/plain";
  r.writeHead(200, { "content-type": type, "access-control-allow-origin": "*" });
  r.end(q.url.endsWith(".js") ? "parent.postMessage('pwned:loaded-script','*')" : "");
});
observer.on("connection", () => connections++);
observer.on("upgrade", (q, sock) => {
  hits.push(q.url);
  sock.destroy();
});
await new Promise((ok) => observer.listen(0, "127.0.0.1", ok));
const O = `http://127.0.0.1:${observer.address().port}`;
const WS = O.replace("http", "ws");

const controls = new Map();
const controlServer = http.createServer((q, r) => {
  const html = controls.get(decodeURIComponent(q.url.split("?")[0].slice("/control/".length)));
  if (html === undefined) return r.writeHead(404).end();
  r.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  r.end(`<!doctype html><html><head><meta charset="utf-8"></head><body>${html}</body></html>`);
});
await new Promise((ok) => controlServer.listen(0, "127.0.0.1", ok));
const CONTROL = `http://127.0.0.1:${controlServer.address().port}`;

// ── the cases ──
// html: the attack, in the card's HTML. head: what goes in the raw frame's head (meta, base, link). click: what the user
// presses. dialog: a press that asks to open a link, through the bridge's native confirmation (cancelled here).
// unobservable: what no control can show (a DNS lookup), still required to make nothing happen in the card.
const P = (c) => `parent.postMessage('pwned:${c}','*');top.postMessage('pwned:${c}','*')`;
const CASES = [
  { name: "script", html: `<script>${P("script")};fetch('${O}/hit/script')</script>`, raw: true },
  { name: "forged-nonce-script", html: `<script nonce="AAAAAAAAAAAAAAAAAAAAAAAA">${P("nonce")};new Image().src='${O}/hit/nonce'</script>`, raw: true },
  { name: "inline-handlers", html: `<img src="data:image/png;base64,AAAA" onerror="${P("onerror")};new Image().src='${O}/hit/onerror'"><svg onload="${P("onload")}"></svg><button id="b" onclick="${P("onclick")};fetch('${O}/hit/onclick')">b</button>`, click: "#b", raw: true },
  { name: "javascript-link", html: `<a id="a" href="javascript:${P("jslink")};fetch('${O}/hit/jslink')">go</a>`, click: "#a", raw: true },
  { name: "remote-img", html: `<img src="${O}/hit/img.png"><img srcset="${O}/hit/srcset.png 1x" alt="">`, raw: true },
  { name: "css-url", html: `<div style="width:10px;height:10px;background:url(${O}/hit/css-attr)"></div><style>.x{width:10px;height:10px;background-image:url(${O}/hit/css-sheet)}</style><div class="x"></div>`, raw: true },
  { name: "css-import-font", html: `<style>@import url(${O}/hit/import.css);@font-face{font-family:f;src:url(${O}/hit/font.woff)}body{font-family:f}</style>text`, raw: true },
  { name: "link-prefetch", html: `<link rel="stylesheet" href="${O}/hit/link.css"><link rel="prefetch" href="${O}/hit/prefetch"><link rel="preload" as="image" href="${O}/hit/preload"><link rel="dns-prefetch" href="//pwned-dns.invalid"><img src="data:image/png;base64,AAAA">`, head: `<link rel="stylesheet" href="${O}/hit/link.css"><link rel="prefetch" href="${O}/hit/prefetch"><link rel="preload" as="image" href="${O}/hit/preload"><link rel="dns-prefetch" href="//pwned-dns.invalid">`, raw: true },
  { name: "meta-refresh", html: `<meta http-equiv="refresh" content="0;url=${O}/hit/refresh">`, head: `<meta http-equiv="refresh" content="0;url=${O}/hit/refresh">`, raw: "navigation" },
  { name: "base", html: `<base href="${O}/hit/base/"><img src="x.png"><a id="a" href="page">relative</a>`, head: `<base href="${O}/hit/base/">`, click: "#a", raw: "navigation" },
  { name: "form-submit", html: `<form action="${O}/hit/form" method="get"><input name="q" value="1"><button id="s" type="submit">send</button></form>`, click: "#s", raw: true },
  { name: "form-script-submit", html: `<form id="f" action="${O}/hit/form-script"><input name="q" value="1"></form><script>document.getElementById('f').submit()</script>` },
  { name: "frames-and-objects", html: `<iframe src="${O}/hit/iframe"></iframe><object data="${O}/hit/object"></object><embed src="${O}/hit/embed"><iframe srcdoc="<script>${P("srcdoc").replaceAll("'", "&#39;")}</script>"></iframe>`, raw: true },
  { name: "media-and-svg", html: `<video poster="${O}/hit/poster" src="${O}/hit/video"></video><audio src="${O}/hit/audio" preload="auto"></audio><svg width="20" height="20"><image href="${O}/hit/svg-image" width="10" height="10"/><use href="${O}/hit/svg-use#a"/><rect fill="url(${O}/hit/svg-fill#g)" width="10" height="10"/></svg>`, raw: true },
  { name: "connections", html: `<script>try{new WebSocket('${WS}/hit/ws')}catch(e){}try{new EventSource('${O}/hit/sse')}catch(e){}try{navigator.sendBeacon('${O}/hit/beacon','x')}catch(e){}try{var x=new XMLHttpRequest();x.open('GET','${O}/hit/xhr');x.send()}catch(e){}try{import('${O}/hit/import.js').catch(function(){})}catch(e){}</script>` },
  { name: "navigation-script", html: `<script>setTimeout(function(){try{window.open('${O}/hit/open')}catch(e){}try{location.replace('${O}/hit/replace')}catch(e){}try{location='${O}/hit/location'}catch(e){}},30)</script>` },
  // a press on a link asks to open it through the bridge; this one isn't https, so it doesn't even ask (openTarget)
  { name: "link-click", html: `<a id="a" href="${O}/hit/click" target="_blank">go</a>`, click: "#a" },
  { name: "storage", html: `<script>document.cookie='pwned=1;path=/';try{localStorage.setItem('pwned','1')}catch(e){}try{indexedDB.open('pwned')}catch(e){}try{caches.open('pwned')}catch(e){}${P("storage")}</script>`, storage: true },
];

const LIVENESS = [
  { name: "loop-at-load", html: `<p>x</p><script>while(true){}</script>`, stops: true },
  { name: "loop-in-click", html: `<button id="b" onclick="for(;;){}">b</button>`, click: "#b", stops: true },
  { name: "loop-in-timer", html: `<script>setTimeout(function(){for(;;){}},50)</script>`, stops: true },
  { name: "interval-storm", html: `<p id="p">0</p><script>var n=0;for(var i=0;i<1000;i++)setInterval(function(){n++;document.getElementById('p').textContent=n},0)</script>` },
  { name: "deep-recursion", html: `<script>function f(n){return f(n+1)+1}f(0)</script><p>after</p>` },
  { name: "huge-string", html: `<script>var s='x'.repeat(2**28)</script>`, stops: true },
  { name: "growing-array", html: `<script>var a=[];for(;;)a.push(new Array(1e4).fill(a.length))</script>`, stops: true },
  { name: "dom-bomb", html: `<div id="d"></div><script>var d=document.getElementById('d');for(var i=0;i<100000;i++)d.appendChild(document.createElement('span'))</script>`, stops: true },
  { name: "dom-bomb-slowly", html: `<div id="d"></div><script>setInterval(function(){var d=document.getElementById('d');for(var i=0;i<400;i++)d.appendChild(document.createElement('span'))},20)</script>`, stops: true },
];

// ── running ──
const results = [];
const line = (browser, mode, name, ok, detail) => {
  results.push({ browser, mode, name, ok });
  console.log(`${ok ? "ok  " : "FAIL"} ${browser.padEnd(8)} ${mode.padEnd(8)} ${name}${detail ? `  ${detail}` : ""}`);
};
const settle = (ms) => new Promise((r) => setTimeout(r, ms));

async function lab(ctx) {
  const page = await ctx.newPage();
  await page.goto(`${base}/html-runtime-lab.html?theme=light`);
  await page.waitForFunction(() => document.documentElement.dataset.labReady === "1", null, { timeout: 90_000 });
  return page;
}

async function cardState(page, timeout = 15_000) {
  // running and shown: the frame is unseen until it first says how tall the card is (then it takes that height), and a
  // press before that would land wherever the frame was; stopped as it is
  await page.waitForFunction(() => {
    const c = document.querySelector('[data-assistant-card="html"]');
    const s = c?.getAttribute("data-html-state");
    return (s === "running" && c.querySelector("[data-html-frame-height]")) || s === "stopped";
  }, null, { timeout });
  return page.evaluate(() => {
    const c = document.querySelector('[data-assistant-card="html"]');
    return { state: c?.getAttribute("data-html-state"), reason: c?.getAttribute("data-html-reason") };
  });
}

/** The card's display frame, once it is there and has its document (WebKit can take a moment after the card says it runs). */
async function cardFrame(page) {
  for (let i = 0; i < 30; i++) {
    const frame = await (await page.$('[data-assistant-card="html"] iframe'))?.contentFrame();
    if (frame && frame.url() === "about:srcdoc") return frame;
    await settle(100);
  }
  const has = await page.$('[data-assistant-card="html"] iframe');
  console.log(`     (no frame after 3 s; the iframe element is ${has ? "there" : "missing"})`);
  return undefined;
}

/** What the display frame holds that it never should: scripts, handlers, links, forms, anything with a URL. */
const frameLeftovers = (frame) =>
  frame.evaluate(() => {
    const bad = [...document.querySelectorAll("script, iframe, object, embed, link, meta:not([http-equiv='Content-Security-Policy']):not([name='color-scheme']), base, form, video, audio, image, use, foreignObject, [href], [srcset], [action], [poster]")].map((e) => e.nodeName);
    for (const e of document.querySelectorAll("*")) for (const a of e.getAttributeNames()) if (/^on/i.test(a)) bad.push(`${e.nodeName}[${a}]`);
    for (const e of document.querySelectorAll("img[src]")) if (!e.getAttribute("src").startsWith("data:image/")) bad.push("img[src]");
    return bad;
  });

async function runBrowser(name, type, opts) {
  const browser = await type.launch({ headless: true, ...opts });
  const pick = (list) => list.filter((c) => !only || c.name === only);

  // controls: each case must be visible to the observer or run, unprotected
  for (const c of pick(CASES)) {
    controls.set(c.name, c.html);
    const ctx = await browser.newContext();
    await ctx.addInitScript(() => {
      window.__pwned = [];
      addEventListener("message", (e) => String(e.data).includes("pwned") && window.__pwned.push(String(e.data)));
    });
    const page = await ctx.newPage();
    hits.length = 0;
    const before = connections;
    let popups = 0;
    ctx.on("page", () => popups++);
    await page.goto(`${CONTROL}/control/${encodeURIComponent(c.name)}`).catch(() => {});
    await settle(600);
    if (c.click) await page.click(c.click, { timeout: 3000 }).catch(() => {});
    await settle(1200);
    const pwned = await page.evaluate(() => window.__pwned?.length ?? 0).catch(() => 0);
    const cookie = c.storage ? (await ctx.cookies(CONTROL)).some((k) => k.name === "pwned") : false;
    const seen = hits.length + pwned + popups + (cookie ? 1 : 0) + (connections - before > 0 && c.name === "link-prefetch" ? 1 : 0);
    line(name, "control", c.name, seen > 0, `requests ${hits.length}, ran ${pwned}, popups ${popups}${c.storage ? `, cookie ${cookie}` : ""}`);
    await ctx.close();
  }

  // the cards (one first, so a cold dev server has compiled the runtime before anything is timed)
  const ctx = await browser.newContext({ viewport: { width: 800, height: 900 } });
  let popups = 0;
  ctx.on("page", () => popups++);
  let page = await lab(ctx);
  // a cold dev server may pre-bundle the runtime's dependencies on this first card and reload the page: once more then
  for (let attempt = 0; ; attempt++) {
    await page.evaluate(() => window.lab.card("<p>warm up</p>"));
    if (await cardState(page, 90_000).then(() => true, () => false)) break;
    if (attempt) throw new Error("the warm-up card never ran");
    await page.close();
    page = await lab(ctx);
  }
  for (const c of pick(CASES)) {
    hits.length = 0;
    const before = connections;
    popups = 0;
    await page.evaluate(() => (window.lab.pwned.length = 0));
    await page.evaluate(([h]) => window.lab.card(h), [c.html]);
    const st = await cardState(page).catch(() => ({ state: "none" }));
    const frame = await cardFrame(page);
    let dialog = "";
    if (c.click && frame) {
      await frame.click(c.click, { timeout: 3000 }).catch(() => {});
      await settle(400);
      dialog = (await page.$eval("[data-html-confirm]", (e) => e.getAttribute("data-html-confirm")).catch(() => "")) ?? "";
      if (dialog) await page.getByRole("button", { name: /^(Cancel|取消)$/ }).click();
    }
    await settle(1500);
    const pwned = await page.evaluate(() => window.lab.pwned.slice());
    const posts = await page.evaluate(() => window.lab.posts.length);
    const leftovers = frame ? await frameLeftovers(frame) : ["no frame"];
    const url = frame?.url() ?? "";
    const storage = c.storage
      ? await page.evaluate(async () => [localStorage.getItem("pwned"), document.cookie.includes("pwned"), (await indexedDB.databases?.())?.some((d) => d.name === "pwned") ?? false])
      : [null, false, false];
    const cookies = (await ctx.cookies()).filter((k) => k.name === "pwned").length;
    const ok = hits.length === 0 && connections === before && pwned.length === 0 && popups === 0 && posts === 0 && leftovers.length === 0 && url === "about:srcdoc" && st.state === "running" && storage[0] === null && !storage[1] && !storage[2] && cookies === 0 && (c.dialog ? dialog === c.dialog : !dialog);
    line(name, "card", c.name, ok, `${st.state}${st.reason ? ` (${st.reason})` : ""}, requests ${hits.length}, connections ${connections - before}, ran ${pwned.length}, popups ${popups}, frame ${url}, left ${leftovers.join("|") || "none"}${dialog ? `, asked ${dialog}` : ""}${c.storage ? `, storage ${JSON.stringify(storage)} cookies ${cookies}` : ""}`);
  }

  // the display frame by itself, the sanitizer bypassed
  for (const c of pick(CASES).filter((c) => c.raw)) {
    hits.length = 0;
    const before = connections;
    popups = 0;
    await page.evaluate(() => (window.lab.pwned.length = 0));
    await page.evaluate(([head, body]) => window.lab.rawFrame(head, body), [c.head ?? "", c.head ? "" : c.html]);
    await settle(800);
    const frame = await (await page.$("#raw-frame"))?.contentFrame();
    if (c.click && frame) await frame.click(c.click, { timeout: 3000 }).catch(() => {});
    await settle(1500);
    const pwned = await page.evaluate(() => window.lab.pwned.slice());
    const url = frame?.url() ?? "";
    const ok = hits.length === 0 && pwned.length === 0 && popups === 0;
    const known = c.raw === "navigation" && !ok;
    line(name, "raw", c.name, ok || known, `requests ${hits.length}, connections ${connections - before}, ran ${pwned.length}, popups ${popups}, frame ${url}${known ? "  (navigation: the sanitizer's to stop, it never draws this; see the card line)" : ""}`);
  }
  await page.evaluate(() => document.getElementById("raw-frame")?.remove());

  // liveness: stopped or contained, the page's thread never held long
  for (const c of pick(LIVENESS)) {
    await page.evaluate(() => {
      window.__gap = 0;
      let last = performance.now();
      clearInterval(window.__probe);
      window.__probe = setInterval(() => {
        const n = performance.now();
        window.__gap = Math.max(window.__gap, n - last);
        last = n;
      }, 20);
    });
    const t0 = Date.now();
    await page.evaluate(([h]) => window.lab.card(h), [c.html]);
    let st = await cardState(page, 15_000).catch(() => ({ state: "none" }));
    if (c.click && st.state === "running") {
      await (await cardFrame(page))?.click(c.click, { timeout: 3000 }).catch(() => {});
    }
    if (c.stops) await page.waitForFunction(() => document.querySelector('[data-assistant-card="html"]')?.getAttribute("data-html-state") === "stopped", null, { timeout: 6000 }).catch(() => {});
    else await settle(3000);
    st = await page.evaluate(() => {
      const el = document.querySelector('[data-assistant-card="html"]');
      return { state: el?.getAttribute("data-html-state"), reason: el?.getAttribute("data-html-reason") };
    });
    const took = Date.now() - t0;
    const gap = await page.evaluate(() => Math.round(window.__gap));
    const ok = (c.stops ? st.state === "stopped" && took <= 8000 : st.state === "running" || st.state === "stopped") && gap <= 250;
    line(name, "liveness", c.name, ok, `${st.state}${st.reason ? ` (${st.reason})` : ""} after ${took} ms, longest page-thread gap ${gap} ms`);
  }

  // layout: the frame follows what the card draws, between its least and the component's height; a stopped card is as
  // tall as its words
  if (!only || only === "layout") {
    const box = () =>
      page.evaluate(() => {
        const c = document.querySelector('[data-assistant-card="html"]');
        const f = c?.querySelector("iframe");
        const words = c?.querySelector("p.overflow-auto");
        return { frame: f ? Math.round(f.getBoundingClientRect().height) : 0, words: words ? Math.round(words.getBoundingClientRect().height) : 0 };
      });
    const scrolls = async () => (await cardFrame(page))?.evaluate(() => document.scrollingElement.scrollHeight > document.scrollingElement.clientHeight + 1);
    const LAYOUT = [
      { name: "one-line", html: "<p>one line</p>", check: (b) => b.frame >= 44 && b.frame <= 90 },
      { name: "long-scrolls", html: Array.from({ length: 80 }, (_, i) => `<p>row ${i}</p>`).join(""), check: (b, s) => b.frame === 320 && s },
      { name: "declared-height", html: Array.from({ length: 80 }, (_, i) => `<p>row ${i}</p>`).join(""), height: 200, check: (b, s) => b.frame === 200 && s },
    ];
    for (const c of LAYOUT) {
      await page.evaluate(([h, height]) => window.lab.card(h, height ? { height } : {}), [c.html, c.height ?? 0]);
      await cardState(page);
      await page.waitForFunction(() => document.querySelector("[data-html-frame-height]"), null, { timeout: 5000 }).catch(() => {});
      await settle(300);
      const b = await box();
      const s2 = await scrolls();
      line(name, "layout", c.name, c.check(b, s2), `frame ${b.frame} px${s2 ? ", scrolls inside" : ""}`);
    }
    // grows as the card adds rows
    await page.evaluate(() => window.lab.card(`<div id="d"></div><script>var n=0;var t=setInterval(function(){var p=document.createElement('p');p.textContent='row '+(++n);document.getElementById('d').appendChild(p);if(n>=6)clearInterval(t)},120)</script>`));
    await cardState(page);
    await settle(200);
    const first = (await box()).frame;
    await settle(1500);
    const later = (await box()).frame;
    line(name, "layout", "grows", later > first && later <= 320, `frame ${first} → ${later} px`);
    // stopped: its words, as tall as they are
    await page.evaluate(() => window.lab.card("<p>x</p><script>while(true){}</script>"));
    await page.waitForFunction(() => document.querySelector('[data-assistant-card="html"]')?.getAttribute("data-html-state") === "stopped", null, { timeout: 8000 }).catch(() => {});
    const stopped = await box();
    line(name, "layout", "stopped-shrinks", stopped.frame === 0 && stopped.words > 0 && stopped.words <= 90, `words ${stopped.words} px`);
  }

  // the bridge
  if (!only || only === "bridge") {
    // a flood of calls: one question at a time, none for a while after a refusal, nothing sent unconfirmed
    await page.evaluate(() => (window.lab.posts.length = 0));
    await page.evaluate(() => window.lab.card(`<p>flood</p><script>for (var i = 0; i < 200; i++) persona.reply('flood ' + i)</script><button id="b" onclick="persona.reply('again')">again</button>`));
    await cardState(page);
    await settle(600);
    const asked = await page.$$eval("[data-html-confirm]", (e) => e.length);
    const text = await page.$eval("[data-html-confirm]", (e) => e.textContent).catch(() => "");
    await page.getByRole("button", { name: /^(Cancel|取消)$/ }).click().catch(() => {});
    await (await cardFrame(page))?.click("#b").catch(() => {});
    await settle(800);
    const askedAfter = await page.$$eval("[data-html-confirm]", (e) => e.length);
    const posts = await page.evaluate(() => window.lab.posts.length);
    line(name, "bridge", "flood", asked === 1 && text.includes("flood 0") && askedAfter === 0 && posts === 0, `questions ${asked} (${text.includes("flood 0") ? "the first call's text" : "?"}), after a refusal ${askedAfter}, sent ${posts}`);

    // a press on an https link asks natively, showing the address; cancelled, nothing is opened or sent
    await page.evaluate(() => (window.lab.posts.length = 0));
    await page.evaluate(() => window.lab.card(`<a id="a" href="https://example.com/next">next</a>`));
    await cardState(page);
    await (await cardFrame(page))?.click("#a").catch(() => {});
    await page.waitForSelector("[data-html-confirm]", { timeout: 5000 }).catch(() => {});
    const openAsked = await page.$eval("[data-html-confirm]", (e) => [e.getAttribute("data-html-confirm"), e.textContent]).catch(() => ["", ""]);
    await page.getByRole("button", { name: /^(Cancel|取消)$/ }).click().catch(() => {});
    await settle(400);
    const openPosts = await page.evaluate(() => window.lab.posts.length);
    line(name, "bridge", "link-asks", openAsked[0] === "open" && openAsked[1].includes("https://example.com/next") && openPosts === 0 && popups === 0, `asked ${openAsked[0] || "nothing"}, sent ${openPosts}, popups ${popups}`);

    // forged messages: to the page, claiming to be the frame; into the frame, without its nonce
    await page.evaluate(() => window.lab.card(`<p id="p">kept</p><button id="b" onclick="persona.reply('forged')">b</button>`));
    await cardState(page);
    await page.evaluate(() => {
      for (let id = 1; id < 60; id++) {
        window.postMessage({ n: "guess", ev: { type: "click", target: id } }, "*");
        window.postMessage({ ev: { type: "click", target: id } }, "*");
      }
      const f = document.querySelector('[data-assistant-card="html"] iframe');
      f.contentWindow.postMessage({ n: "guess", ops: [["t", 3, 0, [999, "#text", "injected"]]] }, "*");
      f.contentWindow.postMessage({ ops: [["d", 5, "injected"]] }, "*");
    });
    await settle(800);
    const forgedAsked = await page.$$eval("[data-html-confirm]", (e) => e.length);
    const injected = await (await cardFrame(page))?.evaluate(() => document.body.textContent.includes("injected"));
    line(name, "bridge", "forged-messages", forgedAsked === 0 && injected === false, `questions ${forgedAsked}, frame took a forged drawing: ${injected}`);

    // a confirmed reply goes to the hub once, with what the user saw
    await page.evaluate(() => window.lab.card(`<button id="b" onclick="persona.reply('the reply')">b</button>`));
    await cardState(page);
    await (await cardFrame(page))?.click("#b");
    await page.waitForSelector("[data-html-confirm]", { timeout: 5000 }).catch(() => {});
    await page.getByRole("button", { name: /^(Send|发送)$/ }).click().catch(() => {});
    await settle(800);
    const sent = await page.evaluate(() => window.lab.posts.slice());
    line(name, "bridge", "confirmed-reply", sent.length === 1 && sent[0].kind === "reply" && sent[0].text === "the reply", JSON.stringify(sent));
  }
  await ctx.close();
  await browser.close();
}

const BROWSERS = { chromium: [chromium, { channel }], webkit: [webkit, {}], firefox: [firefox, {}] };
for (const [name, [type, opts]] of Object.entries(BROWSERS)) {
  if (which !== "all" && which !== name) continue;
  try {
    await runBrowser(name, type, opts);
  } catch (e) {
    if (name === "firefox" && which === "all") console.log(`skip     firefox  (${String(e.message).split("\n")[0]})`);
    else line(name, "run", "browser", false, String(e.message).split("\n")[0]);
  }
}
observer.close();
controlServer.close();
const failed = results.filter((r) => !r.ok);
const by = (b) => results.filter((r) => r.browser === b);
console.log(`VERDICT ${failed.length ? "FAIL" : "PASS"}: ${Object.keys(BROWSERS).filter((b) => by(b).length).map((b) => `${b} ${by(b).filter((r) => r.ok).length}/${by(b).length}`).join(", ")}${failed.length ? `; failed: ${failed.map((r) => `${r.browser} ${r.mode} ${r.name}`).join(", ")}` : ""}`);
process.exit(failed.length ? 1 : 0);
