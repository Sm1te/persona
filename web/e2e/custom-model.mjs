// End-to-end check of typing a model ID on the web (#1644, docs/assistant/model-config.md "Typing a model ID"): "New
// agent"'s list shows the IDs typed before and "Other model…", whose field says why an ID can't be used and starts with
// a good one; a running Claude Code session's model panel (the composer's model pill, #5032) lists the IDs typed before
// with Forget, offers "Use “…”" for a good ID no listed model answers, and sends it through POST …/modes; the Persona
// chat's launcher menu takes one for the next messages.
//
// Needs the web page against the fake hub with MOCK_CUSTOM_MODELS set, and Chrome (playwright-core drives it):
//
//   MOCK_CUSTOM_MODELS=1 PORT=18796 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18796 npx vite --port 5196 --strictPort &)
//   node web/e2e/custom-model.mjs http://127.0.0.1:5196 --hub http://127.0.0.1:18796 [--shots <dir>]
//
// What it checks follows the fake hub's mode: =1 the flows above; =forgetfail a Forget the hub refuses (New agent's
// toast and the model panel's, the row back in each); =empty New agent's empty list ("Enter model ID…"); =fail New agent
// without a list. Runs wide (1280), light and dark, in Chinese; --shots saves each state. Prints one line per check and
// a verdict; exits 1 on a failure.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const hub = (flag("--hub") ?? "").replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !hub) {
  console.error("usage: node web/e2e/custom-model.mjs <web URL> --hub <fake hub URL> [--shots <dir>]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

const REVIEW = "/s/macbook/a1f3c2d4-0000-4000-8000-000000000006"; // a resting Claude Code agent in a pane

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

const mock = async (path) => (await fetch(`${hub}/mock/${path}`, { signal: AbortSignal.timeout(5000) })).json();

async function shot(page, name, theme) {
  if (!shots) return;
  await page.waitForTimeout(400); // transitions (a button turning on, a list folding) settle first
  await page.screenshot({ path: join(shots, `web-custom-model-${name}-${theme}.png`) });
}

async function expectVisible(page, locator, what) {
  try {
    await locator.first().waitFor({ state: "visible", timeout: 8000 });
  } catch {
    fail(`${what} (not visible)`);
  }
}

async function open(browser, theme) {
  await mock("custom_models?reset=1");
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 820 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN" });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForSelector("main textarea", { timeout: 15000 }); // paired
  await page.evaluate((t) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
  }, theme);
  await page.reload();
  await page.waitForSelector("main textarea", { timeout: 15000 });
  return { ctx, page };
}

// New agent with its model list open
async function newAgentList(page, theme) {
  await page.locator('button[aria-label="新建 Agent"]').first().click();
  const dialog = page.getByRole("dialog");
  const trigger = dialog.locator("button[aria-expanded]").filter({ hasText: /默认|Opus|acme/ }).first();
  await expectVisible(page, trigger, `${theme}: New agent's model row`);
  await trigger.click();
  return { dialog, trigger };
}

async function openSession(page) {
  await page.evaluate((path) => {
    history.pushState(null, "", path);
    dispatchEvent(new PopStateEvent("popstate"));
  }, REVIEW);
}

// The session's model panel, from the composer's model pill, once the session's own list is in (the model that runs)
async function openPanel(page, theme) {
  const pill = page.locator("button[data-model-pill]").first();
  await expectVisible(page, pill, `${theme}: the session's model pill`);
  await pill.click();
  const panel = page.locator("[data-model-panel]");
  await expectVisible(page, panel, `${theme}: the model panel`);
  await expectVisible(page, panel.locator("[data-panel-row][data-current]"), `${theme}: the model that runs`);
  return panel;
}

// a row of the panel by its words
const panelRow = (panel, id) => panel.locator("[data-panel-row]").filter({ hasText: id });

async function run(browser, theme) {
  const { ctx, page } = await open(browser, theme);

  // New agent: the list, the field and its reason, the typed pick
  const { dialog, trigger } = await newAgentList(page, theme);
  const heading = dialog.getByText("输入过的");
  await expectVisible(page, heading, `${theme}: the IDs typed before`);
  await expectVisible(page, dialog.getByRole("button", { name: "忘记「vendor/model-x」" }), `${theme}: Forget beside a typed ID`);
  const other = dialog.locator('[data-testid="model-picker-other"]');
  await other.scrollIntoViewIfNeeded();
  await dialog.getByRole("button", { name: "忘记「vendor/model-x」" }).scrollIntoViewIfNeeded(); // the typed IDs and their ×
  await shot(page, "new-agent-list", theme);
  ok(`${theme}: New agent lists the typed IDs and Other model…`);
  await other.click();
  const field = dialog.locator("[data-model-id-field] input");
  await field.fill("acme coder");
  await expectVisible(page, dialog.locator("[data-model-id-problem]").filter({ hasText: "空格" }), `${theme}: why "acme coder" can't be used`);
  await shot(page, "new-agent-invalid", theme);
  await field.fill("acme/coder-9");
  await field.press("Enter");
  await expectVisible(page, trigger.filter({ hasText: "acme/coder-9" }), `${theme}: the typed pick on the row`);
  await shot(page, "new-agent-picked", theme);
  ok(`${theme}: the field says why, and a good ID is the pick`);
  await page.keyboard.press("Escape");

  // A running session: its model panel (the composer's model pill, #5032), opened the way a click goes
  await openSession(page);
  const panel = await openPanel(page, theme);
  const before = panelRow(panel, "vendor/model-x").first();
  await expectVisible(page, before.filter({ hasText: "输入过的" }), `${theme}: an ID typed before, said so in the panel`);
  await before.hover();
  await expectVisible(page, before.getByRole("button", { name: "忘记「vendor/model-x」" }), `${theme}: Forget beside it`);
  await shot(page, "session-panel", theme);
  ok(`${theme}: the panel lists the IDs typed before, each with Forget`);
  const search = panel.locator('input[aria-label="搜索模型…"]');
  const use = panel.locator('[data-panel-row^="typed:"]');
  // an ID the hub would refuse is offered nowhere
  await search.fill("a;b");
  await expectVisible(page, panel.getByText("没有匹配的模型"), `${theme}: "a;b" matches nothing`);
  if (await use.count()) fail(`${theme}: "a;b" offered to use`);
  // a word a listed model answers is a search, not an ID
  await search.fill("opus");
  await expectVisible(page, panelRow(panel, "Opus 5.5"), `${theme}: "opus" finds Opus`);
  if (await use.count()) fail(`${theme}: "opus" offered as a typed ID beside the model it names`);
  const id = `~vendor/model-${theme}`; // a model the session doesn't run yet (the fake hub keeps the last switch)
  await search.fill(id);
  await expectVisible(page, use.filter({ hasText: `使用「${id}」` }), `${theme}: Use “${id}” for an ID no listed model answers`);
  if ((await use.count()) !== 1) fail(`${theme}: ${await use.count()} "Use" rows, want the session's launcher's one`);
  await shot(page, "session-panel-typed", theme);
  await use.first().click(); // applies
  for (let i = 0; i < 40; i++) {
    const { modes } = await mock("modes");
    if ((modes ?? []).some((m) => m.session_id === "a1f3c2d4-0000-4000-8000-000000000006" && m.model === id)) break;
    if (i === 39) fail(`${theme}: no POST …/modes with the typed model`);
    await page.waitForTimeout(150);
  }
  try {
    await panel.waitFor({ state: "detached", timeout: 5000 });
  } catch {
    fail(`${theme}: the panel stays open after the switch went through`);
  }
  ok(`${theme}: the session switched through …/modes, and the panel closed`);

  // The Persona chat: the launcher menu's Other model…
  await page.evaluate(() => {
    history.pushState(null, "", "/");
    dispatchEvent(new PopStateEvent("popstate"));
  });
  const launcherChip = page.locator('button[title="启动器：下一条消息起使用"]').first();
  await expectVisible(page, launcherChip, `${theme}: the chat's launcher chip`);
  await launcherChip.click();
  await page.getByRole("menu").getByText("claude", { exact: true }).first().click();
  await expectVisible(page, page.getByRole("menu").getByText("vendor/model-x"), `${theme}: a typed ID in the launcher's models`);
  await expectVisible(page, page.getByRole("menu").locator('[role="presentation"]').filter({ hasText: "输入过的" }), `${theme}: their heading`);
  await page.getByRole("menu").getByText("其他模型…").click();
  const mf = page.getByRole("menu").locator("input");
  await mf.fill("vendor model");
  await expectVisible(page, page.getByRole("menu").getByRole("alert"), `${theme}: why "vendor model" can't be used`);
  const ring = () => mf.evaluate((el) => getComputedStyle(el).boxShadow);
  const bad = await ring();
  await shot(page, "chat-menu-invalid", theme);
  await mf.fill("vendor/model-y");
  if ((await ring()) === bad) fail(`${theme}: a bad ID's ring is the same as a good one's (${bad})`);
  await shot(page, "chat-menu-valid", theme);
  await mf.press("Enter");
  await expectVisible(page, page.getByText("vendor/model-y"), `${theme}: the pick said`);
  ok(`${theme}: the chat takes a typed ID for the next messages`);
  await ctx.close();
}

// MOCK_CUSTOM_MODELS=forgetfail: the row goes, comes back, and the reason is said
async function forgetFails(browser, theme) {
  const { ctx, page } = await open(browser, theme);
  const { dialog } = await newAgentList(page, theme);
  const x = dialog.getByRole("button", { name: "忘记「vendor/model-x」" });
  await expectVisible(page, x, `${theme}: Forget beside a typed ID`);
  await x.click();
  await expectVisible(page, page.getByText(/没能忘记「vendor\/model-x」/), `${theme}: why it wasn't forgotten`);
  await expectVisible(page, x, `${theme}: the row back`);
  await x.scrollIntoViewIfNeeded();
  await shot(page, "new-agent-forget-failed", theme);
  ok(`${theme}: New agent puts a refused Forget back`);
  await page.keyboard.press("Escape");
  await page.waitForTimeout(600); // the toast goes

  await openSession(page);
  const panel = await openPanel(page, theme);
  const row = panelRow(panel, "vendor/model-x").first();
  await expectVisible(page, row, `${theme}: the ID typed before in the panel`);
  // New agent's toast has gone, so the one that follows is the panel's
  const why = page.getByText(/没能忘记「vendor\/model-x」/);
  for (let i = 0; (await why.count()) > 0; i++) {
    if (i === 60) fail(`${theme}: New agent's toast stays`);
    await page.waitForTimeout(200);
  }
  await row.hover();
  await row.getByRole("button", { name: "忘记「vendor/model-x」" }).click();
  await expectVisible(page, why, `${theme}: why it wasn't forgotten`);
  await expectVisible(page, panelRow(panel, "vendor/model-x"), `${theme}: the panel's row back`);
  await shot(page, "session-forget-failed", theme);
  ok(`${theme}: the model panel puts a refused Forget back and says why`);
  await ctx.close();
}

// MOCK_CUSTOM_MODELS=empty / fail: New agent offers "Enter model ID…" without a list
async function noList(browser, theme, mode) {
  const { ctx, page } = await open(browser, theme);
  const { dialog } = await newAgentList(page, theme);
  const other = dialog.locator('[data-testid="model-picker-other"]');
  await expectVisible(page, other.filter({ hasText: "输入模型 ID…" }), `${theme}: Enter model ID… (${mode})`);
  if (mode === "fail") await expectVisible(page, dialog.getByText("claude 没有登录"), `${theme}: why the list couldn't be read`);
  await other.scrollIntoViewIfNeeded();
  await shot(page, mode === "fail" ? "new-agent-list-failed" : "new-agent-empty", theme);
  ok(`${theme}: New agent takes an ID without a list (${mode})`);
  await ctx.close();
}

const { mode } = await mock("custom_models");
const browser = await chromium.launch({ channel: "chrome", headless: true });
try {
  for (const theme of ["light", "dark"]) {
    if (mode === "forgetfail") await forgetFails(browser, theme);
    else if (mode === "empty" || mode === "fail") await noList(browser, theme, mode);
    else await run(browser, theme);
  }
} finally {
  await browser.close();
}
console.log(`PASS custom model (${mode})`);
