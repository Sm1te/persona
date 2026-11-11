// A decision answered on its Needs you row (#3804): the real web app with synthetic HTTP/WS only; it never connects to a
// user's hub or conversation. Two answerable decisions and a legacy note-only review wait.
// node web/e2e/decision-row.mjs <web URL> --shots <directory> [--large] [--chromium <executable>]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = args[0]?.replace(/\/$/, "");
const shots = flag("--shots");
const textSize = args.includes("--large") ? "large" : "standard";
if (!base || !shots) {
  console.error("usage: node web/e2e/decision-row.mjs <web URL> --shots <directory> [--chromium <executable>]");
  process.exit(2);
}
const origin = new URL(base);
assert(["127.0.0.1", "localhost", "[::1]"].includes(origin.hostname) && origin.protocol === "http:", "only an owned loopback HTTP web server is allowed");
assert(!origin.username && !origin.password && !origin.search && !origin.hash, "the web URL must not contain credentials");
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT decision-row browser verification (180s)"); process.exit(124); }, 180_000);
const AT = "2026-11-10T08:00:00Z";
const sid = "decision-row-demo";
const RESPONSES = ["decide_yourself", "not_what_i_asked", "explain_first"];
const menuWords = {
  en: ["You decide", "This isn't what I asked for", "Tell me the background first"],
  "zh-Hans": ["你来定", "这不是我要的", "先告诉我背景"],
  ja: ["任せます", "頼んだことと違います", "先に背景を教えて"],
};

function todos() {
  // a deadline later today in the browser's zone, so the row says a time of day
  const deadline = new Date();
  deadline.setHours(23, 30, 0, 0);
  const task = (id, title, ask) => ({ id, title, origin: sid, created_at: AT, updated_at: AT, agents: [{ machine_id: "demo", session_id: `${id}-worker`, linked_at: AT }],
    supervision: { supervisor: sid, state: "needs_user", reviews: [{ at: AT, verdict: "needs_user", note: "Example extension checks passed; the rollout still needs your decision." }] }, decision_review: { review_at: AT }, ask: { outcome: "decide", asked_at: AT, responses: RESPONSES, ...ask } });
  return [
    task("demo-rollout", "Example rollout", {
      verdict: "Needs your decision: roll out to everyone now, or to 10% first",
      recommend: "10% first",
      question: "Roll the example change out to everyone now, or to 10% first?",
      why_owner: "priority",
      options: [
        { label: "10% first", leads_to: "one more day; a bad build reaches few people" },
        { label: "Everyone now", leads_to: "ships today; a bad build reaches everyone" },
        { label: "Hold the rollout", leads_to: "nothing ships this week" },
      ],
      cost_of_waiting: "the rollout slips by a day",
      background: "From the main conversation on Nov 10: \"roll out the example change\" · merged and checked in staging, not released",
      deadline: deadline.toISOString(),
      default: "taken",
    }),
    task("demo-cleanup", "Example data cleanup", {
      verdict: "Needs your decision: delete the example records older than a year",
      recommend: "Delete after an export",
      question: "Delete the example records older than a year?",
      why_owner: "irreversible",
      options: [
        { label: "Delete after an export", leads_to: "an export is kept for 30 days, then the records are gone" },
        { label: "Keep everything", leads_to: "storage keeps growing; nothing is lost" },
      ],
      cost_of_waiting: "none this week",
      background: "Started by the assistant on Nov 6 · the export script is written and tested on a copy",
      default: "never",
    }),
    { ...task("demo-legacy", "Example extension rollout", {}), ask: undefined },
  ];
}

async function run(browser, size, theme, language) {
  const context = await browser.newContext({ viewport: size === "wide" ? { width: 1280, height: 900 } : { width: 390, height: 844 }, colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, language, textSize }) => {
    localStorage.setItem("persona.token", "synthetic-demo-token");
    localStorage.setItem("persona.language", language);
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.text-size", textSize);
  }, { theme, language, textSize });
  const session = { machine_id: "demo", session_id: sid, title: "Example conversation", assistant: true, state: "idle", status: "idle", updated_at: AT };
  let list = todos();
  const agents = list.map((t) => ({ machine_id: "demo", session_id: `${t.id}-worker`, title: `${t.title} Agent`, state: "idle", status: "idle", updated_at: AT, state_since: AT }));
  const writes = [];
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, "");
    if (path === `/assistant/conversations/${sid}/today`) {
      await route.fulfill({ contentType: "application/json", body: "{}" });
      return;
    }
    if (request.method() !== "GET") {
      const body = request.postDataJSON();
      writes.push({ path, method: request.method(), body });
      const dismissal = /^\/todos\/([^/]+)\/decision-(dismiss|undo-dismiss)$/.exec(path);
      if (dismissal) {
        assert.equal(body.review_at, AT);
        const todo = list.find((t) => t.id === dismissal[1]);
        assert(todo && !todo.done_at);
        if (dismissal[2] === "undo-dismiss") assert.equal(body.dismissal_id, "demo-dismissal");
        todo.decision_review = { review_at: AT, ...(dismissal[2] === "dismiss" ? { dismissal_id: "demo-dismissal" } : {}) };
        await route.fulfill({ contentType: "application/json", body: JSON.stringify(todo.decision_review) });
        return;
      }
      const m = /^\/todos\/([^/]+)\/accept$/.exec(path);
      assert(m && request.method() === "POST", `only decision routes are written: ${request.method()} ${path}`);
      list = list.map((t) => (t.id === m[1] ? { ...t, ask: undefined, supervision: { ...t.supervision, state: "" } } : t));
      await route.fulfill({ contentType: "application/json", body: JSON.stringify({ ok: true }) });
      return;
    }
    let body = {};
    if (path === "/machines") body = { machines: [{ id: "demo", name: "Example computer", online: true, os: "linux" }] };
    else if (path === "/sessions") body = { sessions: [session, ...agents] };
    else if (path === "/interactions") body = { interactions: [] };
    else if (path === "/assistant") body = { machine_id: "demo", main: sid, conversations: [session], tasks: [], memory: [] };
    else if (path === "/todos") body = { todos: list };
    else if (path === `/sessions/demo/${sid}`) body = { session };
    else if (path.endsWith("/feed")) body = { items: [] };
    else if (path.endsWith("/items")) body = { items: [], has_more: false };
    else if (path === "/extensions/ui") body = { slots: [] };
    else if (path === "/extensions") body = { extensions: [] };
    else if (path === "/managers") body = { managers: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await context.routeWebSocket("**/api/v1/ws**", (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", hub_name: "Example hub", features: [] }));
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw));
      if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
    });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.setDefaultTimeout(12_000);
  const name = (step) => join(shots, `decision-${size}-${theme}-${language}-${step}${textSize === "large" ? "-large" : ""}.png`);
  try {
    await page.goto(`${base}/`);
    await page.locator('[data-tray-entry="needs"]').click();
    const rows = page.locator("[data-decision-row]");
    await rows.first().waitFor();
    assert.equal(await rows.count(), 2);
    const rollout = page.locator('[data-needs-row="task:demo-rollout"]');
    const cleanup = page.locator('[data-needs-row="task:demo-cleanup"]');
    assert.equal(await rollout.locator('[data-decision-default="taken"]').count(), 1, "the reversible decision says when its default is taken");
    assert.match(await rollout.locator('[data-decision-default="taken"]').innerText(), /23:30/);
    assert.equal(await cleanup.locator('[data-decision-default="never"]').count(), 1, "the irreversible one says it is never taken by default");
    for (const row of [rollout, cleanup]) {
      assert.equal(await row.locator("[data-decision-actions] button").count(), 2, "Later and Don't handle stay on the row");
      const buttons = row.locator("div.overflow-hidden > button, div.overflow-hidden > span > button");
      const geometry = await buttons.evaluateAll((els) => els.map((el) => ({ top: Math.round(el.getBoundingClientRect().top + el.getBoundingClientRect().height / 2), height: el.clientHeight, scroll: el.scrollHeight })));
      assert(geometry.every((g) => g.top === geometry[0].top), `the buttons are on one line: ${JSON.stringify(geometry)}`);
      assert(geometry.every((g) => g.scroll <= g.height + 1), `no button's text wraps: ${JSON.stringify(geometry)}`);
      const shown = await row.locator("[data-decision-option]").count();
      const primary = await row.locator("[data-decision-option]").first().evaluate((el) => el.closest("button").className);
      assert(primary.includes("bg-ink"), "the recommended option is the solid button");
      assert(shown >= 1);
      const separate = await row.evaluate((el) => {
        const footer = el.querySelector("[data-decision-actions]").getBoundingClientRect();
        return [...el.querySelectorAll("[data-decision-option]")].every((label) => label.closest("button").getBoundingClientRect().bottom <= footer.top + 1);
      });
      assert(separate, "deferral controls never cover the decision option buttons");
    }
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), false, "no horizontal page overflow");
    await rollout.scrollIntoViewIfNeeded(); // keep this decision's whole action group in the evidence capture
    await page.waitForTimeout(300); // the entry has turned to its selected colors
    await page.screenshot({ path: name("tray") });
    // the menu: the options that did not fit, then the three answers that are no option
    const shownOptions = await rollout.locator("[data-decision-option]").count();
    await rollout.locator("span[data-decision-tail] button[aria-label]").last().click();
    const items = page.locator("[role=menuitem]");
    await items.first().waitFor();
    const labels = await items.evaluateAll((els) => els.map((el) => el.querySelector("[data-menu-label]")?.textContent ?? el.textContent));
    assert.equal(labels.length, 3 - shownOptions + 3, `the menu holds the overflowed options and the three responses: ${JSON.stringify(labels)}`);
    menuWords[language].forEach((word, i) => assert(labels[3 - shownOptions + i].startsWith(word), `${labels[3 - shownOptions + i]} starts with ${word}`));
    await page.waitForTimeout(300); // the menu has faded in
    await page.screenshot({ path: name("menu") });
    await items.nth(3 - shownOptions + 1).click(); // "This isn't what I asked for"
    await rollout.waitFor({ state: "detached" }); // answered: the hub lists it without its ask, and the row goes
    assert.deepEqual(writes, [{ path: "/todos/demo-rollout/accept", method: "POST", body: { asked_at: AT, response: "not_what_i_asked" } }]);
    // an option on the other row
    await cleanup.locator("[data-decision-option]").nth(0).click();
    assert.equal(writes.length, 1, "irreversible options require confirmation");
    await cleanup.locator("[data-decision-option]").nth(0).click();
    await cleanup.waitFor({ state: "detached" });
    assert.deepEqual(writes[1], { path: "/todos/demo-cleanup/accept", method: "POST", body: { asked_at: AT, option: "Delete after an export" } });
    const legacy = page.locator('[data-needs-row="task:demo-legacy"]');
    await legacy.locator("[data-decision-actions]").scrollIntoViewIfNeeded();
    assert.equal(await legacy.locator("[data-decision-option]").count(), 0, "no invented options for a review without an ask");
    assert.match(await legacy.locator("[data-decision-legacy]").innerText(), /Example extension checks passed/);
    await page.screenshot({ path: name("legacy") });
    await legacy.locator("[data-decision-actions] button").nth(0).click();
    await legacy.waitFor({ state: "detached" });
    assert.equal(writes.length, 2, "Later writes nothing");
    await page.reload();
    await page.locator('[data-tray-entry="needs"]').click();
    await legacy.waitFor();
    await legacy.locator("[data-decision-actions] button").nth(1).click();
    await legacy.waitFor({ state: "detached" });
    assert.deepEqual(writes[2], { path: "/todos/demo-legacy/decision-dismiss", method: "POST", body: { review_at: AT } });
    const undo = page.locator('[aria-live="polite"] button').filter({ hasText: /Undo|撤销|元に戻す/ });
    await undo.click();
    await legacy.waitFor();
    assert.deepEqual(writes[3], { path: "/todos/demo-legacy/decision-undo-dismiss", method: "POST", body: { review_at: AT, dismissal_id: "demo-dismissal" } });
    await legacy.locator("[data-decision-actions] button").nth(1).click();
    await legacy.waitFor({ state: "detached" });
    await page.reload();
    await page.locator('[data-tray-entry="needs"]').click();
    assert.equal(await legacy.count(), 0, "dismissal survives a read and reload without completing the task");
    assert(!list.find((t) => t.id === "demo-legacy").done_at);
    await page.screenshot({ path: name("answered") });
    assert.deepEqual(errors, []);
    console.log(`PASS ${size} ${theme} ${language}: rows=2, option buttons on the first row=${shownOptions}, menu=${labels.length}, POST=${writes.length}`);
  } catch (error) {
    await page.screenshot({ path: name("failed") }).catch(() => {});
    throw error;
  } finally {
    await context.close();
  }
}

let browser;
try {
  browser = await chromium.launch({ executablePath: flag("--chromium") || "/usr/bin/google-chrome", headless: true });
  for (const theme of ["light", "dark"]) {
    await run(browser, "wide", theme, "en");
    await run(browser, "narrow", theme, "en");
    await run(browser, "wide", theme, "zh-Hans");
  }
  for (const language of ["zh-Hans", "ja"]) for (const theme of ["light", "dark"]) await run(browser, "narrow", theme, language);
  console.log("PASS decision-row browser verification");
} catch (error) {
  console.error("FAIL decision-row browser verification", error);
  process.exitCode = 1;
} finally {
  clearTimeout(watchdog);
  await browser?.close();
}
