// End-to-end check of typing a model ID on the web (#1644, docs/assistant/model-config.md "Typing a model ID"): "New
// agent"'s model panel (#5104, the model pill at the right of the first message box) lists the IDs typed before with
// Forget, offers no "Use “…”" for an ID the hub would refuse and offers it for a good one, which becomes the pick; a
// running Claude Code session's model panel (the composer's model pill, #5032) lists the IDs typed before
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
// toast and the model panel's, the row back in each); =empty New agent's empty list (its Default row, and a typed ID
// offered by the search); =fail New agent without a list (why, Retry, and a typed ID still offered). Runs wide (1280),
// light and dark, in Chinese; --shots saves each state. Prints one line per check and a verdict; exits 1 on a failure.
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

// New agent with its model panel open (#5104: the model pill at the right of the first message box; the panel is portaled
// to the page), once claude's list is read
async function newAgentList(page, theme) {
  await page.locator('button[aria-label="新建 Agent"]').first().click();
  const dialog = page.getByRole("dialog", { name: "新建 Agent", exact: true });
  const trigger = dialog.locator('[data-testid="new-agent-model"] [data-model-pill]');
  await expectVisible(page, trigger, `${theme}: New agent's model pill`);
  for (let i = 0; i < 60 && ((await trigger.isDisabled()) || (await trigger.getAttribute("data-model-pill")) === "loading"); i++) await page.waitForTimeout(150);
  await trigger.click();
  const panel = page.locator("[data-model-panel]");
  await expectVisible(page, panel, `${theme}: New agent's model panel`);
  for (let i = 0; (await panel.locator('[role="status"]').count()) > 0; i++) {
    if (i === 60) fail(`${theme}: New agent's lists did not load`);
    await page.waitForTimeout(150);
  }
  return { dialog, trigger, panel };
}

// Esc closes the panel (its flyout first), then New agent; pressed only while one of them is open
async function closeNewAgent(page) {
  for (let i = 0; i < 4 && (await page.locator('[data-model-panel], [role="dialog"]').count()); i++) {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(250);
  }
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

  // New agent: the IDs typed before (each a row of claude's group, "输入过的" under it, Forget beside it), then the
  // panel's search in place of "Other model…"'s field
  const { trigger, panel: naPanel } = await newAgentList(page, theme);
  const typedBefore = panelRow(naPanel, "vendor/model-x").first();
  await expectVisible(page, typedBefore.filter({ hasText: "输入过的" }), `${theme}: the IDs typed before`);
  await typedBefore.scrollIntoViewIfNeeded();
  await typedBefore.hover();
  await expectVisible(page, typedBefore.getByRole("button", { name: "忘记「vendor/model-x」" }), `${theme}: Forget beside a typed ID`);
  await shot(page, "new-agent-list", theme);
  ok(`${theme}: New agent lists the typed IDs, each with Forget`);
  // an ID the hub would refuse (a space) is offered nowhere: the search says nothing matches (the field's reason under
  // it went with the field)
  const naSearch = naPanel.locator('input[aria-label="搜索模型…"]');
  const naUse = naPanel.locator('[data-panel-row^="typed:"]');
  await naSearch.fill("acme coder");
  await expectVisible(page, naPanel.getByText("没有匹配的模型"), `${theme}: "acme coder" matches nothing`);
  if (await naUse.count()) fail(`${theme}: "acme coder" offered to use`);
  await shot(page, "new-agent-invalid", theme);
  // a good one: "Use “…”" in each launcher that takes typed IDs (claude, codex; sonnetcc fixes its own model), and Enter
  // picks the first, claude's
  await naSearch.fill("acme/coder-9");
  await expectVisible(page, naUse.filter({ hasText: "使用「acme/coder-9」" }), `${theme}: Use “acme/coder-9”`);
  const offered = await naUse.evaluateAll((els) => els.map((e) => e.dataset.panelRow));
  if (JSON.stringify(offered) !== JSON.stringify(["typed:claude\nacme/coder-9", "typed:codex\nacme/coder-9"])) fail(`${theme}: "Use" rows ${JSON.stringify(offered)}`);
  await naSearch.press("Enter");
  try {
    await naPanel.waitFor({ state: "detached", timeout: 5000 });
  } catch {
    fail(`${theme}: New agent's panel stays open after the pick`);
  }
  // the pill names a gateway's model without its vendor ("coder-9"); the panel lights the typed ID itself, as a row of
  // claude's
  await expectVisible(page, trigger.filter({ hasText: "coder-9" }), `${theme}: the typed pick on the pill`);
  if ((await trigger.locator("[data-pill-engine]").getAttribute("data-pill-engine")) !== "claude") fail(`${theme}: the typed pick isn't claude's`);
  await shot(page, "new-agent-picked", theme);
  await trigger.click();
  await expectVisible(page, page.locator('[data-model-panel] [data-panel-row="model:claude\\a acme/coder-9"][data-current]'), `${theme}: the typed ID lit in claude's group`);
  ok(`${theme}: no Use for an ID the hub would refuse, and a good ID is the pick`);
  await closeNewAgent(page);

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
  const { panel: naPanel } = await newAgentList(page, theme);
  const typed = panelRow(naPanel, "vendor/model-x").first();
  await typed.scrollIntoViewIfNeeded();
  await typed.hover();
  const x = typed.getByRole("button", { name: "忘记「vendor/model-x」" });
  await expectVisible(page, x, `${theme}: Forget beside a typed ID`);
  await x.click();
  await expectVisible(page, page.getByText(/没能忘记「vendor\/model-x」/), `${theme}: why it wasn't forgotten`);
  await expectVisible(page, panelRow(naPanel, "vendor/model-x"), `${theme}: the row back`);
  await shot(page, "new-agent-forget-failed", theme);
  ok(`${theme}: New agent puts a refused Forget back`);
  await closeNewAgent(page);
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

// MOCK_CUSTOM_MODELS=empty / fail: New agent takes a typed ID without a list. "Enter model ID…" went with the old list:
// the panel's search offers "Use “…”" in claude's group instead (docs/assistant/model-config.md "Typing a model ID":
// an empty list or one that couldn't be read still takes a typed ID), beside the error and Retry when it failed.
async function noList(browser, theme, mode) {
  const { ctx, page } = await open(browser, theme);
  const { dialog, panel } = await newAgentList(page, theme);
  const claude = panel.locator('[data-panel-section="provider"][aria-label="claude"]');
  if (mode === "fail") {
    await expectVisible(page, claude.getByText("claude 没有登录"), `${theme}: why the list couldn't be read`);
    await expectVisible(page, claude.getByRole("button", { name: "重试" }), `${theme}: Retry beside it`);
    await expectVisible(page, dialog.locator('[data-testid="new-agent-models-failed"]'), `${theme}: the line under the box`);
  } else {
    const rows = await claude.locator("[data-panel-row]").evaluateAll((els) => els.map((e) => e.dataset.panelRow));
    if (JSON.stringify(rows) !== JSON.stringify(["model:claude\ndefault"])) fail(`${theme}: claude's empty list shows ${JSON.stringify(rows)}`);
  }
  await shot(page, mode === "fail" ? "new-agent-list-failed" : "new-agent-empty", theme);
  await panel.locator('input[aria-label="搜索模型…"]').fill("acme/coder-9");
  await expectVisible(page, panel.locator('[data-panel-row="typed:claude\\a acme/coder-9"]'), `${theme}: Use “acme/coder-9” in claude's group (${mode})`);
  await shot(page, mode === "fail" ? "new-agent-typed-failed" : "new-agent-typed-empty", theme);
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
