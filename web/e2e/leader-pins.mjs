// End-to-end check of the leader conversations in the sidebar (#3623, #4331, docs/web/web.md "Persona chats"): with the
// hub's `assistant.leaders` set, a fresh browser shows Persona chats as the leaders only, the 常驻 group, in the hub's
// order under each one's name from GET /api/v1/assistant/leaders with what it has open (在做 N · 等你 N); every other
// chat is under 全部对话; New chat is the compose icon on the section's title; a lead's menu has 取消常驻 (no 置顶 anywhere),
// which ends it on the hub and takes its row away; 全部对话's menu has 设为常驻, which brings it back.
//
// Needs the web page against the fake hub with leaders, and Chrome (or any Chromium through --executable):
//
//   MOCK_LEADERS=1 PORT=18986 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18986 npx vite --port 5986 --strictPort &)
//   node web/e2e/leader-pins.mjs http://127.0.0.1:5986 [--shots <dir>] [--executable <chromium>]
//
// Runs wide (1280) and phone width (390, the drawer), light and dark, in Chinese; --shots saves each. Prints one line per
// check and a verdict; exits 1 on a failure and after three minutes.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const executable = flag("--executable");
if (!base) {
  console.error("usage: node web/e2e/leader-pins.mjs <web URL> [--shots <dir>] [--executable <chromium>]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });
setTimeout(() => {
  console.log("FAIL timed out after 3 minutes");
  process.exit(1);
}, 180_000).unref();

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

const hub = (path) => fetch(`${base}/api/v1${path}`, { headers: { Authorization: "Bearer mock" }, signal: AbortSignal.timeout(5000) }).then((r) => r.json());
const assistant = await hub("/assistant");
const leaders = assistant.leaders ?? [];
if (leaders.length < 2) fail(`the fake hub has no leaders (start it with MOCK_LEADERS=1): ${JSON.stringify(leaders)}`);
const names = new Map(((await hub("/assistant/leaders")).leaders ?? []).map((l) => [l.id, l.name]));
const others = assistant.conversations.filter((c) => c.session_id !== assistant.main && !leaders.includes(c.session_id));
const local = others[others.length - 1].session_id;

// the Persona chats' rows in the order shown, without New chat
const rowIds = (page) => page.$$eval('aside a[href^="/persona/"]', (as) => as.map((a) => a.getAttribute("href").slice("/persona/".length).split("?")[0]).filter((id) => id !== "new"));
// the group titles inside Persona chats
const headings = (page) => page.$$eval('aside [role="heading"]', (hs) => hs.map((h) => h.textContent.trim()));

async function open(browser, theme, width, pins) {
  const ctx = await browser.newContext({ viewport: { width, height: width > 600 ? 820 : 844 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN", hasTouch: width <= 600 });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await page.evaluate(([t, p]) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
    if (p) localStorage.setItem("assistant.pinned", JSON.stringify(p));
    else localStorage.removeItem("assistant.pinned");
  }, [theme, pins]);
  await page.goto(`${base}/overview`);
  await page.waitForSelector("main", { timeout: 15000 });
  if (width <= 600) {
    await page.locator("main").getByRole("button", { name: "侧边栏" }).first().click();
    await page.waitForTimeout(500); // the drawer slides in
  }
  await page.locator(`aside a[href^="/persona/${leaders[0]}"]`).first().waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${theme} ${width}: no row for the first leader`));
  return { ctx, page };
}

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
const counted = new Map(((await hub("/assistant/directions/tree")).directions ?? []).filter((d) => !d.secretary).map((d) => [d.id, d.counts]));
const label = (c) => [c?.working ? `在做 ${c.working}` : "", c?.needs_you ? `等你 ${c.needs_you}` : ""].filter(Boolean).join(" · ");
try {
  for (const theme of ["light", "dark"]) {
    for (const width of [1280, 390]) {
      const where = `${theme} ${width}px`;
      const { ctx, page } = await open(browser, theme, width, null);
      const ids = await rowIds(page);
      if (JSON.stringify(ids) !== JSON.stringify(leaders)) fail(`${where}: Persona chats are not the leaders only, in the hub's order: ${ids.join(",")}`);
      for (const id of leaders) {
        const text = await page.locator(`aside a[href^="/persona/${id}"]`).first().textContent();
        if (!names.get(id) || !text.includes(names.get(id))) fail(`${where}: leader ${id} does not show its name ${names.get(id)}: ${text}`);
        if (label(counted.get(id)) && !text.includes(label(counted.get(id)))) fail(`${where}: leader ${id} does not say ${label(counted.get(id))}: ${text}`);
      }
      const hs = await headings(page);
      if (JSON.stringify(hs) !== JSON.stringify(["常驻"])) fail(`${where}: group titles are ${hs.join(" | ")}`);
      if (!(await page.locator('aside a[href="/history"]', { hasText: "全部对话" }).count())) fail(`${where}: no 全部对话`);
      const compose = page.locator('aside .group\\/sec a[href="/persona/new"][aria-label="新对话"]');
      if (!(await compose.count()) || (await compose.first().textContent()) !== "") fail(`${where}: New chat is not the icon on the Persona chats title`);
      ok(`${where}: Persona chats are 常驻 (${leaders.map((id) => `${names.get(id)}${label(counted.get(id)) ? " " + label(counted.get(id)) : ""}`).join(" / ")}) then 全部对话; New chat is the title's icon`);
      if (shots) await page.screenshot({ path: join(shots, `web-leader-pins-${width > 600 ? "wide" : "narrow"}-${theme}.png`) });
      await ctx.close();
    }
  }

  const { ctx, page } = await open(browser, "light", 1280, null);
  const first = leaders[0];
  await page.locator(`aside a[href^="/persona/${first}"]`).first().click({ button: "right" });
  const menu = page.locator('[role="menu"]').last();
  await menu.waitFor({ state: "visible", timeout: 5000 });
  const items = await menu.locator('[role="menuitem"]').allTextContents();
  if (items.some((x) => x.includes("置顶"))) fail(`a lead's menu still says 置顶: ${items.join(" | ")}`);
  if (shots) await page.screenshot({ path: join(shots, "web-lead-menu.png") });
  await menu.locator('[role="menuitem"]', { hasText: "取消常驻" }).first().click();
  await page.waitForFunction((id) => !document.querySelector(`aside a[href^="/persona/${id}"]`), first, { timeout: 5000 }).catch(() => fail("取消常驻 left the row in the sidebar"));
  if ((await hub("/assistant")).leaders?.includes(first)) fail("the hub still lists the ended lead");
  ok("取消常驻 ends the lead on the hub and its row goes");
  // back from 全部对话: its menu has 设为常驻
  await page.goto(`${base}/history`);
  const histRow = page.locator(`a[href^="/persona/${first}"]`).first();
  await histRow.waitFor({ timeout: 10000 });
  await histRow.locator("xpath=..").getByRole("button").last().click();
  const hmenu = page.locator('[role="menu"]').last();
  await hmenu.waitFor({ state: "visible", timeout: 5000 });
  await hmenu.locator('[role="menuitem"]', { hasText: "设为常驻" }).first().click();
  await page.waitForFunction((id) => !!document.querySelector(`aside a[href^="/persona/${id}"]`), first, { timeout: 8000 }).catch(() => fail("设为常驻 in 全部对话 did not bring the row back"));
  ok("设为常驻 in 全部对话 makes it a lead again");
  await ctx.close();
} finally {
  await browser.close();
}
console.log("PASS web leader pins e2e");
process.exit(0);
