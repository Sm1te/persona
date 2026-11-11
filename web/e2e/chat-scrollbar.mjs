// Actual conversation scrollbar acceptance with invented data. Never forward writes or connect to a real hub.
// Usage: node web/e2e/chat-scrollbar.mjs <loopback web origin> <artifact directory>
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { chromium } from "playwright-core";
const [address, directory] = process.argv.slice(2);
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname));
assert.equal(base.pathname, "/");
const output = resolve(directory);
await mkdir(output, { recursive: true });
const health = await (await fetch(new URL("/api/v1/health", base), { signal: AbortSignal.timeout(5000) })).json();
assert.equal(health.version, "mock", "Refuse production, including through a loopback proxy");
const MID = "demo", SID = "scrollbar-demo", CHAT = "[data-chat-scroll]", RAIL = "[data-nav-rail]";
const items = Array.from({ length: 16 }, (_, i) => [
  { id: `input-${i}#0`, seq: i * 2 + 1, ts: "2026-01-01T00:00:00Z", role: "user", kind: "text", text: `Example input ${i + 1}: Keep navigation separate from scrolling.` },
  { id: `answer-${i}#0`, seq: i * 2 + 2, ts: "2026-01-01T00:00:01Z", role: "assistant", kind: "text", text: `**Example answer ${i + 1}**\n\n` + Array(7).fill("This invented response makes the conversation long enough to scroll. Messages remain readable beside the navigation marks. No real account or private conversation is used.").join("\n\n") },
]).flat();
const session = { machine_id: MID, session_id: SID, title: "Conversation scrollbar example", agent: "claude", status: "idle", state: "idle", updated_at: items.at(-1).ts, capabilities: { read: true, send: true } };
const machine = { id: MID, name: "Example computer", online: true };
const results = { status: "running", checks: [], screenshots: [], errors: [] };
let browser;
const deadline = setTimeout(() => { results.status = "timeout"; void browser?.close(); }, 180_000);
async function fixture(context, theme, large, fallback) {
  await context.addInitScript(({ theme, large, fallback }) => {
    localStorage.setItem("persona.token", "synthetic-scrollbar-token");
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", theme);
    if (large) localStorage.setItem("persona.textSize", "large");
    if (fallback) {
      const supports = CSS.supports.bind(CSS);
      CSS.supports = (...args) => args[0] === "scrollbar-color" ? false : supports(...args);
    }
  }, { theme, large, fallback });
  await context.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url());
    assert.equal(url.origin, base.origin, "All assets and reads stay on the fixture origin");
    if (!url.pathname.startsWith("/api/")) return route.continue();
    if (request.method() !== "GET") return route.fulfill({ json: { ok: true } });
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const data = {
      "/machines": { machines: [machine] }, "/sessions": { sessions: [session], total: 1, has_more: false },
      [`/sessions/${MID}/${SID}/info`]: session,
      [`/sessions/${MID}/${SID}/items`]: { items, has_more: false, more_after: false, history: "scrollbar-fixture", from_seq: 1, to_seq: 32 },
      "/assistant": { machine_id: MID, conversations: [] }, "/interactions": { interactions: [] }, "/todos": { todos: [] },
      "/managers": { managers: [], default_id: "" }, "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
      "/pins": { spaces: [], sessions: [], sections: [] }, "/mutes": { spaces: [], sessions: [], allowed: [] },
      "/agents/sessions": { sessions: [] }, "/personas": { personas: [] }, "/assistant/personas": { personas: [] },
    };
    return route.fulfill({ json: data[path] ?? {} });
  });
  await context.routeWebSocket(/\/api\/v1\/ws/, socket => {
    socket.send(JSON.stringify({ type: "hello", server_version: "mock", features: [], protocol: 1, hub_name: "Example hub" }));
    socket.send(JSON.stringify({ type: "machines", machines: [machine] }));
    socket.send(JSON.stringify({ type: "sessions", machine_id: MID, sessions: [session] }));
    socket.onMessage(message => {
      const event = JSON.parse(String(message));
      if (event.type === "app_ping") socket.send(JSON.stringify({ type: "pong", id: event.id }));
    });
  });
}
async function check(name, fn) {
  const detail = await fn();
  results.checks.push({ name, detail });
  console.log(`PASS ${name}: ${JSON.stringify(detail)}`);
}
async function geometry(page) {
  return page.evaluate(([chat, rail]) => {
    const sc = document.querySelector(chat), nav = document.querySelector(rail), content = sc.querySelector("[data-chat-content]");
    const r = sc.getBoundingClientRect(), n = nav?.getBoundingClientRect(), c = content.getBoundingClientRect();
    return { left: r.left, right: r.right, top: r.top, height: sc.clientHeight, gutter: sc.offsetWidth - sc.clientWidth,
      navLeft: n?.left, navRight: n?.right, contentRight: c.right - parseFloat(getComputedStyle(content).paddingRight),
      scrollTop: sc.scrollTop, scrollHeight: sc.scrollHeight, overflow: document.documentElement.scrollWidth - innerWidth };
  }, [CHAT, RAIL]);
}
async function run(theme, width, { touch = false, large = false, fallback = false, reducedMotion = false, forcedColors = false } = {}) {
  const name = `${width}-${theme}${touch ? "-touch" : ""}${large ? "-large" : ""}${fallback ? "-fallback" : ""}${reducedMotion ? "-reduced" : ""}${forcedColors ? "-contrast" : ""}`;
  const context = await browser.newContext({ viewport: { width, height: 820 }, locale: "en-US", colorScheme: theme, hasTouch: touch, isMobile: touch, serviceWorkers: "block", reducedMotion: reducedMotion ? "reduce" : "no-preference", forcedColors: forcedColors ? "active" : "none" });
  try {
    await fixture(context, theme, large, fallback);
    const page = await context.newPage();
    page.setDefaultTimeout(15_000);
    page.on("pageerror", e => results.errors.push(e.message));
    await page.goto(new URL(`/s/${MID}/${SID}`, base).href);
    await page.waitForFunction(chat => document.querySelector(chat)?.querySelectorAll("[data-user-bubble]").length === 16, CHAT);
    await page.waitForFunction(dark => document.documentElement.classList.contains("dark") === dark, theme === "dark");
    if (!touch) await page.locator(RAIL).waitFor();
    if (large) await page.evaluate(() => document.documentElement.dataset.textSize = "large");
    const sc = page.locator(CHAT);
    const beforeScroll = await geometry(page);
    await page.mouse.move(width / 2, 300);
    await page.mouse.wheel(0, -800);
    await page.waitForFunction(chat => document.querySelector(chat).scrollTop < document.querySelector(chat).scrollHeight - document.querySelector(chat).clientHeight - 100, CHAT);
    await check(`${name} geometry and independent hit areas`, async () => {
      const g = await geometry(page);
      assert.ok(g.overflow <= 1);
      if (touch) assert.equal(await page.locator(RAIL).count(), 0);
      else {
        assert.equal(Math.round(g.right - g.gutter - g.navRight), 16);
        assert.ok(g.contentRight <= g.navLeft - 3, "Text stays clear of the rail even in a narrow panel");
        const hit = await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.closest("[data-nav-rail]") != null, [g.right - Math.max(g.gutter / 2, 2), g.top + 50]);
        assert.equal(hit, false, "The scrollbar edge cannot hit the rail");
      }
      return g;
    });
    if (!fallback && !forcedColors) {
      await page.waitForFunction(chat => document.querySelector(chat).dataset.scrollbarVisible === "true", CHAT);
      await page.waitForFunction(chat => !getComputedStyle(document.querySelector(chat)).scrollbarColor.startsWith("rgba(0, 0, 0, 0)"), CHAT);
      await page.screenshot({ path: join(output, `${name}-scrolling.png`) });
      results.screenshots.push(`${name}-scrolling.png`);
      await page.waitForFunction(chat => !document.querySelector(chat).dataset.scrollbarVisible && getComputedStyle(document.querySelector(chat)).scrollbarColor.startsWith("rgba(0, 0, 0, 0)"), CHAT);
      await page.screenshot({ path: join(output, `${name}-idle.png`) });
      results.screenshots.push(`${name}-idle.png`);
      await check(`${name} quiet fade without layout shift`, async () => {
        const g = await geometry(page);
        assert.equal(g.gutter, beforeScroll.gutter);
        assert.equal(g.contentRight, beforeScroll.contentRight);
        assert.equal(g.navRight, beforeScroll.navRight);
        assert.equal(await sc.evaluate(el => getComputedStyle(el).scrollbarColor), "rgba(0, 0, 0, 0) rgba(0, 0, 0, 0)");
        return { color: "transparent", gutter: g.gutter };
      });
      if (!touch) {
        const g = await geometry(page);
        const x = g.right - Math.max(g.gutter / 2, 2), y = g.top + g.height * 0.5;
        await page.mouse.move(x, y);
        await page.waitForFunction(chat => document.querySelector(chat).dataset.scrollbarVisible === "true", CHAT);
        await page.waitForTimeout(1300);
        await check(`${name} scrollbar-only hover remains visible`, async () => {
          assert.equal(await sc.getAttribute("data-scrollbar-visible"), "true");
          assert.equal(await page.locator("[data-nav-preview]").count(), 0);
          return { native: true };
        });
        // Scroll near the middle, then drag the real native thumb, not a DOM imitation.
        await sc.evaluate(el => el.scrollTop = (el.scrollHeight - el.clientHeight) / 2);
        const middle = await geometry(page);
        const thumbHeight = Math.max(20, middle.height * middle.height / middle.scrollHeight);
        const thumbY = middle.top + (middle.height - thumbHeight) / 2 + thumbHeight / 2;
        await page.mouse.move(x, thumbY);
        await page.mouse.down();
        await page.mouse.move(x, thumbY - 70, { steps: 10 });
        await page.waitForTimeout(1300);
        await check(`${name} real native thumb drag`, async () => {
          const after = await geometry(page);
          assert.ok(after.scrollTop < middle.scrollTop - 100, "Native dragging really scrolls the conversation");
          assert.equal(await sc.getAttribute("data-scrollbar-visible"), "true");
          return { before: middle.scrollTop, after: after.scrollTop };
        });
        await page.mouse.up();
        await page.mouse.down();
        await page.mouse.move(x - 90, thumbY - 100, { steps: 10 });
        await page.mouse.up();
        // No subsequent pointer movement: Chrome suppresses mousemove during a native thumb drag.
        await page.waitForFunction(chat => !document.querySelector(chat).dataset.scrollbarVisible, CHAT);
        await check(`${name} drag release outside clears stale hover`, async () => {
          assert.equal(await sc.getAttribute("data-scrollbar-visible"), null);
          return { releaseOutside: true, subsequentPointerMove: false };
        });
        await page.mouse.move(width / 2, 300);
      }
    } else {
      await check(`${name} native fallback`, async () => {
        if (fallback) assert.equal(await sc.getAttribute("data-chat-scrollbar"), null);
        const color = await sc.evaluate(el => getComputedStyle(el).scrollbarColor);
        assert.notEqual(color, "rgba(0, 0, 0, 0) rgba(0, 0, 0, 0)");
        return { color, scrollTop: (await geometry(page)).scrollTop };
      });
    }
    await page.screenshot({ path: join(output, `${name}-verified.png`) });
    results.screenshots.push(`${name}-verified.png`);
  } finally { await context.close(); }
}
try {
  browser = await chromium.launch({ channel: "chrome", headless: true, ignoreDefaultArgs: ["--hide-scrollbars"] });
  for (const theme of ["light", "dark"]) {
    await run(theme, 1600);
    await run(theme, 640);
    await run(theme, 390);
    await run(theme, 390, { touch: true });
    await run(theme, 640, { large: true });
  }
  await run("light", 640, { fallback: true });
  await run("dark", 640, { reducedMotion: true });
  await run("light", 640, { forcedColors: true });
  assert.deepEqual(results.errors, []);
  results.status = "pass";
} catch (e) {
  results.status = results.status === "timeout" ? "timeout" : "fail";
  results.errors.push(e.stack);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser?.close();
  await writeFile(join(output, "results.json"), JSON.stringify(results, null, 2) + "\n");
  console.log(`${results.status.toUpperCase()} chat-scrollbar: ${results.checks.length} checks`);
}
