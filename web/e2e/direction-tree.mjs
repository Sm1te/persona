// End-to-end check of the direction tree on the web's pages (#4352, docs/web/web.md "Persona chats"): the To-dos page
// groups by 常驻 with each direction's counts in the sidebar's words, the secretary's group 书记官 last; the Agents list groups by 常驻
// from the same tree, a direction opening to its tasks (state words) and a task to its agents; the default 状态 grouping
// and the sidebar stay as they were.
//
// Needs the web page against the fake hub with leaders, and Chrome (or any Chromium through --executable):
//
//   MOCK_LEADERS=1 PORT=18990 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18990 npx vite --port 5990 --strictPort &)
//   node web/e2e/direction-tree.mjs http://127.0.0.1:5990 [--shots <dir>] [--executable <chromium>]
//
// Runs in Chinese, light and dark, wide (1280) and phone width (390); --shots saves each. Prints one line per check and a
// verdict; exits 1 on a failure and after three minutes.
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
  console.error("usage: node web/e2e/direction-tree.mjs <web URL> [--shots <dir>] [--executable <chromium>]");
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
const tree = (await hub("/assistant/directions/tree")).directions ?? [];
const leaders = tree.filter((d) => !d.secretary);
if (leaders.length < 2) fail("the fake hub has no leaders (start it with MOCK_LEADERS=1)");
// the sidebar's 常驻 rows' words (sidebar.leader_*): only the parts above zero
const countsText = (c) => [c.working > 0 ? `在做 ${c.working}` : "", c.needs_you > 0 ? `等你 ${c.needs_you}` : ""].filter(Boolean).join(" · ");

async function open(browser, theme, width, prefs = {}, path = "/overview") {
  const ctx = await browser.newContext({ viewport: { width, height: width > 600 ? 900 : 844 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN", hasTouch: width <= 600 });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await page.evaluate(([t, p]) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
    for (const [k, v] of Object.entries(p)) localStorage.setItem(k, v);
  }, [theme, prefs]);
  await page.goto(`${base}${path}`);
  await page.waitForSelector("main", { timeout: 15000 });
  if (width <= 600 && path !== "/agents") {
    await page.locator("main").getByRole("button", { name: "侧边栏" }).first().click();
    await page.waitForTimeout(500); // the drawer slides in
  }
  return { ctx, page };
}

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
try {
  // the To-dos page by 常驻
  for (const theme of ["light", "dark"]) {
    const { ctx, page } = await open(browser, theme, 1280, { "persona.todos.grouping": "direction" }, "/todos");
    const groups = page.locator('[data-testid="todo-group"][data-group^="direction:"]');
    await groups.first().waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${theme}: the To-dos page is not grouped by 常驻`));
    const shown = await groups.evaluateAll((gs) => gs.map((g) => g.getAttribute("data-group")));
    const expected = tree.filter((d) => d.tasks.length > 0).map((d) => `direction:${d.secretary ? "secretary" : d.id}`);
    if (JSON.stringify(shown) !== JSON.stringify(expected)) fail(`${theme}: To-dos groups ${shown.join(",")} != ${expected.join(",")}`);
    if (!(await page.getByRole("radio", { name: "常驻" }).count()) && !(await page.getByText("常驻", { exact: true }).count())) fail(`${theme}: no 常驻 grouping option`);
    // each section's counts in the sidebar's words: the non-zero parts, nothing when both are zero
    const said = await groups.evaluateAll((gs) => gs.map((g) => g.querySelector("[data-direction-counts]")?.getAttribute("title") ?? ""));
    const want = tree.filter((d) => d.tasks.length > 0).map((d) => countsText(d.counts));
    if (JSON.stringify(said) !== JSON.stringify(want)) fail(`${theme}: To-dos counts ${JSON.stringify(said)} != ${JSON.stringify(want)}`);
    ok(`${theme}: To-dos grouped by 常驻 in the tree's order (${shown.length} groups), counts as the sidebar says them`);
    if (shots) await page.screenshot({ path: join(shots, `web-direction-todos-${theme}.png`) });
    await ctx.close();
  }

  // the To-dos page's default grouping (状态) and the sidebar, as before: no 常驻 section, the sidebar's chats untouched
  {
    const { ctx, page } = await open(browser, "light", 1280, {}, "/todos");
    await page.locator('[data-testid="todo-section"]').first().waitFor({ state: "visible", timeout: 10000 }).catch(() => fail("the To-dos page shows no status sections"));
    if (await page.locator('[data-testid="todo-group"][data-group^="direction:"]').count()) fail("the default To-dos grouping shows 常驻 sections");
    if (await page.locator("aside [data-direction]").count()) fail("the sidebar has direction rows");
    ok("To-dos by 状态 by default, the sidebar as before");
    if (shots) await page.screenshot({ path: join(shots, "web-direction-todos-default-light.png") });
    await ctx.close();
  }

  // the Agents list by 常驻: the Agents page on a wide screen and at phone width
  for (const [theme, width] of [["light", 1280], ["dark", 1280], ["light", 390]]) {
    const where = `${theme} ${width}px`;
    const { ctx, page } = await open(browser, theme, width, { "persona.agentGrouping": "direction", "persona.agentChatList": "0" }, "/agents");
    const choice = page.locator("main").getByRole("radio", { name: "按常驻" }).first();
    await choice.waitFor({ state: "visible", timeout: 10000 }).catch(() => fail(`${where}: no 常驻 grouping on the Agents page`));
    if ((await choice.getAttribute("aria-checked")) !== "true") fail(`${where}: the Agents page is not grouped by 常驻`);
    await page.locator("main [data-direction-counts]").first().waitFor({ state: "visible", timeout: 5000 }).catch(() => fail(`${where}: no direction section on the Agents page`));
    await page.waitForTimeout(400); // the sections finish opening
    // a direction shows when the Agents list holds one of its tasks' agents: each shown one with its counts and a task
    const text = await page.locator("main").innerText();
    const shown = leaders.filter((d) => text.includes(countsText(d.counts)) && d.tasks.some((t) => t.agents.length > 0 && text.includes(t.title)));
    if (!shown.length) fail(`${where}: no direction with its counts and a task on the Agents page`);
    ok(`${where}: the Agents page groups by 常驻 (${shown.map((d) => d.name).join(", ")}) with their counts and tasks`);
    if (shots) await page.screenshot({ path: join(shots, `web-direction-agents-${width > 600 ? "wide" : "narrow"}-${theme}.png`) });
    await ctx.close();
  }
  console.log("PASS direction tree");
} finally {
  await browser.close();
}
