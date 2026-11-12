// End-to-end check of New agent's recent setups (#2134, docs/web/web.md "Recent setups"): two starts through the form are
// remembered; reopened, New agent shows them as cards above the computers (with a made-up offline computer's one,
// dimmed, that can't be picked, and Allow everything marked); a tap fills computer, folder, launcher, model, permission
// mode and effort and starts nothing; the ⋯ removes one and clears them. The model, launcher and effort are New agent's
// model pill and panel (#5104): a row picked there puts in its launcher and model, the effort is set in the flyout of the
// row that runs, and the pill says what the form holds.
//
// Needs the web page against the fake hub with one manager (MOCK_MANAGERS=1: otherwise the MacBook is another manager's
// and not offered), and Chrome or Edge (playwright-core drives it):
//
//   MOCK_MANAGERS=1 PORT=18984 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18984 npx vite --port 5984 --strictPort &)
//   node web/e2e/recent-setups.mjs http://127.0.0.1:5984 --hub http://127.0.0.1:18984 [--shots <dir>] [--channel msedge]
//
// Runs wide (1280) and phone width (390), light and dark, in Chinese; --shots saves each state. Prints one line per check
// and a verdict; exits 1 on a failure.
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
const channel = flag("--channel") ?? "chrome";
if (!base || !hub) {
  console.error("usage: node web/e2e/recent-setups.mjs <web URL> --hub <fake hub URL> [--shots <dir>] [--channel msedge]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

const KEY = "persona.recentSetups";
// a made-up older setup on the fake hub's offline NAS: the form can't start there, so it is put in the list directly
const OFFLINE = { machine: "nas", cwd: "/home/me/backup", launcher: "codex", model: "gpt-5.5-codex", agent_type: "codex", mode: "", effort: "", at: "2026-11-09T01:00:00Z" };

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}
const started = async () => (await (await fetch(`${hub}/mock/started`, { signal: AbortSignal.timeout(5000) })).json()).started ?? [];

async function expectVisible(locator, what) {
  try {
    await locator.first().waitFor({ state: "visible", timeout: 8000 });
  } catch {
    fail(`${what} (not visible)`);
  }
}

async function until(check, what) {
  for (let i = 0; i < 50; i++) {
    if (await check()) return;
    await new Promise((r) => setTimeout(r, 150));
  }
  fail(what);
}

async function open(browser, theme, width) {
  const ctx = await browser.newContext({ viewport: { width, height: width > 600 ? 1240 : 1500 }, deviceScaleFactor: 2, colorScheme: theme, locale: "zh-CN", hasTouch: width <= 600 });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await page.evaluate((t) => {
    localStorage.setItem("persona.theme", t);
    localStorage.setItem("persona.language", "zh-Hans");
  }, theme);
  await page.reload();
  await page.waitForSelector("main", { timeout: 15000 });
  return { ctx, page };
}

const shot = async (page, name) => {
  if (!shots) return;
  await page.waitForTimeout(400); // the sheet's slide-up and the lists loading settle first
  await page.screenshot({ path: join(shots, `web-recent-setups-${name}.png`) });
};

async function newAgent(page) {
  await page.evaluate(() => import("/src/lib/new-agent.ts").then((m) => m.openNewAgent()));
  // the model panel and its flyout are dialogs too: New agent is the one with its title
  const dialog = page.getByRole("dialog", { name: "新建 Agent", exact: true });
  await expectVisible(dialog.getByRole("button", { name: "启动", exact: true }), "New agent's Start");
  await page.waitForTimeout(300);
  return dialog;
}

const machine = (dialog, name) => dialog.locator("button.h-8").filter({ hasText: new RegExp(`^${name}$`) });
// the model pill at the right of the first message box (#5104); its panel and the panel's flyout are portaled to the page
const pillOf = (dialog) => dialog.locator('[data-testid="new-agent-model"] [data-model-pill]');
const pillEffort = async (dialog) => ((await pillOf(dialog).locator("[data-pill-effort]").count()) ? await pillOf(dialog).locator("[data-pill-effort]").textContent() : "");
// a model row of the panel: "model:<launcher>\n<model id>" (\a is the line break in a CSS string)
const rowSel = (launcher, model) => `[data-panel-row="model:${launcher}\\a ${model}"]`;

async function openPanel(page, dialog, tag) {
  const pill = pillOf(dialog);
  await until(async () => (await pill.getAttribute("data-model-pill")) === "ready" && !(await pill.isDisabled()), `${tag}: the model pill isn't ready`);
  await pill.click();
  const panel = page.locator("[data-model-panel]");
  await expectVisible(panel, `${tag}: the model panel`);
  await until(async () => (await panel.locator('[role="status"]').count()) === 0, `${tag}: the panel's lists did not load`);
  return panel;
}

async function closePanel(page, tag) {
  // Esc closes the flyout, then the panel; never once the panel is gone (that would close New agent)
  for (let i = 0; i < 3 && (await page.locator("[data-model-panel]").count()); i++) {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(250);
  }
  if (await page.locator("[data-model-panel]").count()) fail(`${tag}: the model panel stays open`);
}

// What were the launcher chips and the model list: a row of the panel, picked by its name (the middle of a row can be its
// star or Customize, always shown on a touch screen). A pick puts in the row's launcher and model and closes the panel.
async function pickModel(page, dialog, tag, launcher, model) {
  const panel = await openPanel(page, dialog, tag);
  await panel.locator(rowSel(launcher, model)).click({ position: { x: 48, y: 12 } });
  await page.locator("[data-model-panel]").waitFor({ state: "detached", timeout: 5000 }).catch(() => fail(`${tag}: the panel stayed after picking ${model}`));
}

// What were the effort chips: the slider in the flyout of the row that runs. From the model's default, → lands on the
// lowest level and each → goes one up.
async function setEffort(page, dialog, tag, label) {
  const panel = await openPanel(page, dialog, tag);
  const row = panel.locator('[data-panel-row^="model:"][data-current]').first();
  await row.scrollIntoViewIfNeeded();
  await row.hover();
  await row.locator("[data-row-customize]").click();
  const slider = page.locator('[data-model-flyout] [role="slider"]');
  await expectVisible(slider, `${tag}: the effort slider`);
  await slider.focus();
  for (let i = 0; i < 6 && (await slider.getAttribute("aria-valuetext")) !== label; i++) await page.keyboard.press("ArrowRight");
  if ((await slider.getAttribute("aria-valuetext")) !== label) fail(`${tag}: the slider stops at ${await slider.getAttribute("aria-valuetext")}, want ${label}`);
  await closePanel(page, tag);
  await until(async () => (await pillEffort(dialog)) === label, `${tag}: the pill doesn't say ${label}`);
}

async function startThrough(page, tag, { computer, folder, pick, mode, effort }) {
  const before = (await started()).length;
  const dialog = await newAgent(page);
  await machine(dialog, computer).click();
  await page.waitForTimeout(400);
  const field = dialog.locator("input[placeholder]").first();
  await field.fill(folder);
  if (pick) await pickModel(page, dialog, tag, ...pick);
  await dialog.getByRole("radio", { name: mode, exact: true }).click();
  await setEffort(page, dialog, tag, effort);
  await dialog.getByRole("button", { name: "启动", exact: true }).click();
  await until(async () => (await started()).length > before, `${tag}: no start for ${folder}`);
  await page.getByRole("dialog").waitFor({ state: "detached", timeout: 8000 }).catch(() => fail(`${tag}: the sheet stayed after Start`));
}

async function run(browser, theme, width) {
  const tag = `${width}/${theme}`;
  const { ctx, page } = await open(browser, theme, width);
  await page.evaluate((key) => localStorage.removeItem(key), KEY);

  // Two starts through the form: each is remembered, newest first, with the model and effort sent. Opus 5.5 is claude's
  // row in the panel, so the setup carries its launcher too (the old list offered the followed launcher's models with
  // no launcher of their own); sonnetcc is its Default row (it fixes its own model).
  await startThrough(page, tag, { computer: "MacBook", folder: "/Users/me/code/weather-app", pick: ["claude", "opus"], mode: "只做计划", effort: "高" });
  await startThrough(page, tag, { computer: "Mac Studio", folder: "/Users/me/code/site", pick: ["sonnetcc", "default"], mode: "全部放行", effort: "很高" });
  const kept = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) ?? "[]"), KEY);
  const want = [
    { machine: "studio", cwd: "/Users/me/code/site", launcher: "sonnetcc", model: "", agent_type: "claude", mode: "bypassPermissions", effort: "xhigh" },
    { machine: "macbook", cwd: "/Users/me/code/weather-app", launcher: "claude", model: "opus", agent_type: "claude", mode: "plan", effort: "high" },
  ];
  if (JSON.stringify(kept.map(({ at, ...rest }) => rest)) !== JSON.stringify(want)) fail(`${tag}: remembered ${JSON.stringify(kept)}`);
  ok(`${tag}: two starts are remembered, newest first, with the model and effort sent`);
  await page.evaluate(({ key, offline }) => {
    const list = JSON.parse(localStorage.getItem(key) ?? "[]");
    list.splice(1, 0, offline);
    localStorage.setItem(key, JSON.stringify(list));
  }, { key: KEY, offline: OFFLINE });

  // Reopened: three cards on top, the offline one dimmed and not pickable, Allow everything marked.
  const dialog = await newAgent(page);
  const cards = dialog.locator("[data-recent-setup]");
  if ((await cards.count()) !== 3) fail(`${tag}: ${await cards.count()} cards`);
  const site = cards.filter({ hasText: "site" });
  const weather = cards.filter({ hasText: "weather-app" });
  const offline = cards.filter({ hasText: "backup" });
  if ((await site.getAttribute("data-bypass")) !== "true") fail(`${tag}: the bypass card isn't marked`);
  if (!(await offline.isDisabled())) fail(`${tag}: the offline card can be picked`);
  if (!(await offline.textContent()).includes("离线")) fail(`${tag}: the offline card doesn't say so`);
  await shot(page, `${width}-${theme}-cards`);
  ok(`${tag}: three cards, the offline one dimmed, Allow everything marked`);
  await offline.click({ force: true });
  if ((await offline.getAttribute("aria-pressed")) !== "false") fail(`${tag}: the offline card got picked`);

  // A tap fills every field and starts nothing.
  const before = (await started()).length;
  await weather.click();
  await page.waitForTimeout(800);
  if ((await weather.getAttribute("aria-pressed")) !== "true") fail(`${tag}: the card isn't marked picked`);
  if (!/bg-ink/.test((await machine(dialog, "MacBook").getAttribute("class")) ?? "")) fail(`${tag}: MacBook isn't the computer`);
  if ((await dialog.locator("input[placeholder]").first().inputValue()) !== "/Users/me/code/weather-app") fail(`${tag}: the folder isn't filled`);
  // the model and effort: what the pill says (the model chips and effort chips are gone)
  await until(async () => (await pillOf(dialog).textContent()).includes("Opus 5.5"), async () => `${tag}: the model isn't filled (${await pillOf(dialog).textContent()})`);
  if ((await dialog.getByRole("radio", { name: "只做计划", exact: true }).getAttribute("aria-checked")) !== "true") fail(`${tag}: the mode isn't filled`);
  if ((await pillEffort(dialog)) !== "高") fail(`${tag}: the effort isn't filled (${await pillEffort(dialog)})`);
  if (!(await page.evaluate(() => document.activeElement?.tagName === "TEXTAREA"))) fail(`${tag}: the first message isn't focused`);
  await shot(page, `${width}-${theme}-picked`);
  if ((await started()).length !== before) fail(`${tag}: a tap started an agent`);
  ok(`${tag}: a tap fills computer, folder, model, mode and effort, focuses the first message, starts nothing`);

  await site.click();
  await page.waitForTimeout(800);
  // the launcher: the pill names it, and its Default row is the one lit in the panel (no launcher chips any more)
  await until(async () => ((await pillOf(dialog).getAttribute("title")) ?? "").includes("sonnetcc"), async () => `${tag}: the launcher isn't filled (${await pillOf(dialog).getAttribute("title")})`);
  const lit = await openPanel(page, dialog, tag);
  if (!(await lit.locator(`${rowSel("sonnetcc", "default")}[data-current]`).count())) fail(`${tag}: sonnetcc's row isn't the one lit`);
  await closePanel(page, tag);
  if ((await dialog.getByRole("radio", { name: "全部放行", exact: true }).getAttribute("aria-checked")) !== "true") fail(`${tag}: Allow everything isn't filled`);
  await shot(page, `${width}-${theme}-bypass`);
  if ((await started()).length !== before) fail(`${tag}: a tap started an agent`);
  ok(`${tag}: the bypass card fills its launcher and Allow everything, with the form's warning`);

  // The ⋯: remove one, then clear them all.
  await dialog.getByRole("button", { name: "最近配置选项" }).click();
  await page.getByRole("menuitem").filter({ hasText: "从最近中移除" }).click();
  await expectVisible(page.getByRole("menuitem").filter({ hasText: "backup · NAS" }), `${tag}: the cards in Remove from Recent`);
  await shot(page, `${width}-${theme}-menu`);
  await page.getByRole("menuitem").filter({ hasText: "backup · NAS" }).click();
  await until(async () => (await cards.count()) === 2, `${tag}: the removed card is still there`);
  await dialog.getByRole("button", { name: "最近配置选项" }).click();
  await page.getByRole("menuitem").filter({ hasText: "清除最近配置" }).click();
  await until(async () => (await dialog.locator('[data-testid="recent-setups"]').count()) === 0, `${tag}: Clear left the section`);
  if (await page.evaluate((key) => localStorage.getItem(key), KEY)) fail(`${tag}: Clear left the list`);
  ok(`${tag}: the ⋯ removes one and clears them all`);
  await ctx.close();
}

const browser = await chromium.launch({ channel });
try {
  for (const width of [1280, 390]) for (const theme of ["light", "dark"]) await run(browser, theme, width);
} catch (e) {
  fail(e instanceof Error ? e.message : String(e));
} finally {
  await browser.close();
}
console.log("PASS");
