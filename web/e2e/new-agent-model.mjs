// End-to-end check of New agent's model choice in the unified model panel (#5104, docs/web/web.md "New Agent"): the model
// pill at the right of the first message box shows the model Model settings › New agent runs, with Claude Code's mark, and
// its panel has "Follow Model settings" checked on top; the panel lists the profile's presets, one group per launcher of
// the computer (a launcher that failed its check here with its reason and How to fix), searches, and the flyout of the row
// that runs has the effort slider and Claude Code's ultracode switch. glm-5.3's flyout offers Codex as well; picking it
// makes the pill show the Codex engine and hides Permission mode. Start sends the launcher, model and effort picked.
// Switching to the Mac Studio (no codex launcher there) after a Codex pick falls back to its default and says so under the
// box, and the follow row takes a pick back to the default.
//
// Needs the web page against the fake hub with these modes, and Chrome (playwright-core drives it). MOCK_ULTRACODE gives
// the hub the ultracode feature, without which New agent offers no switch:
//
//   MOCK_MODEL_DEFAULTS=1 MOCK_LAUNCHER_HEALTH=1 MOCK_CUSTOM_MODELS=1 MOCK_PROVIDERS=1 MOCK_NEW_AGENT_PANEL=1 MOCK_ULTRACODE=1 \
//     PORT=18741 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18741 npx vite --port 5201 --strictPort &)
//   node web/e2e/new-agent-model.mjs http://127.0.0.1:5201 --hub http://127.0.0.1:18741 [--shots <dir>] [--channel msedge]
//
// Runs wide (1280) and phone width (390), light and dark, in Chinese, then once in English (1280, light); --shots saves
// each state as web-new-agent-model-<state>-<width>-<theme>.png (the English pass adds -en). Prints one line per check
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
  console.error("usage: node web/e2e/new-agent-model.mjs <web URL> --hub <fake hub URL> [--shots <dir>] [--channel msedge]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

// the words the checks look for, as the catalogs say them
const WORDS = {
  "zh-Hans": {
    locale: "zh-CN", title: "新建 Agent", start: "启动", follow: "跟随模型配置", presets: "预设", fix: "怎么修好", configure: "配置模型",
    noMatch: "没有匹配的模型", unset: "模型默认", high: "高", permission: "权限模式", close: "关闭",
    terminal: "这台电脑的终端里用不了", auth: "Key 或登录被拒绝了",
  },
  en: {
    locale: "en-US", title: "New agent", start: "Start", follow: "Follow Model settings", presets: "Presets", fix: "How to fix", configure: "Configure models",
    noMatch: "No matching models", unset: "Model default", high: "High", permission: "Permission mode", close: "Close",
    terminal: "Not available in this computer's terminal", auth: "Its key or sign-in was rejected",
  },
};
// the fake hub's Model settings › New agent: claude's model-chat (the follow row's second line)
const FOLLOWED = "claude · model-chat";
const SERVER = "服务器"; // the fake hub's computers keep their names in every language
const STUDIO = "Mac Studio";

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

async function until(check, what, tries = 60) {
  for (let i = 0; i < tries; i++) {
    if (await check()) return;
    await new Promise((r) => setTimeout(r, 150));
  }
  fail(typeof what === "function" ? await what() : what);
}

// a model row of the panel: its key is "model:<launcher>\n<model id>" (\a is the line break in a CSS string)
const rowSel = (launcher, model) => `[data-panel-row="model:${launcher}\\a ${model}"]`;

async function open(browser, { theme, width, lang }) {
  const ctx = await browser.newContext({
    viewport: { width, height: width > 600 ? 1000 : 844 }, deviceScaleFactor: 2, colorScheme: theme, locale: WORDS[lang].locale, hasTouch: width <= 600,
  });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  await page.evaluate(({ theme, lang }) => {
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.language", lang);
    localStorage.setItem("new.machine", "server");
  }, { theme, lang });
  await page.reload();
  await page.waitForSelector("main", { timeout: 15000 });
  return { ctx, page };
}

function shooter(page, { theme, width, lang }) {
  return async (state) => {
    if (!shots) return;
    await page.waitForTimeout(400); // the sheet's slide-up, the panel's growing out of the pill
    await page.screenshot({ path: join(shots, `web-new-agent-model-${state}-${width}-${theme}${lang === "en" ? "-en" : ""}.png`) });
  };
}

async function newAgent(page, w) {
  await page.evaluate(() => import("/src/lib/new-agent.ts").then((m) => m.openNewAgent()));
  // the panel and its flyout are dialogs too: New agent is the one with its title
  const dialog = page.getByRole("dialog", { name: w.title, exact: true });
  await expectVisible(dialog.getByRole("button", { name: w.start, exact: true }), "New agent's Start");
  await page.waitForTimeout(300);
  return dialog;
}

const pillOf = (page) => page.locator('[data-testid="new-agent-model"] [data-model-pill]');
const machine = (dialog, name) => dialog.locator("button.h-8").filter({ hasText: new RegExp(`^${name}$`) });

/** The pill once the computer's launchers and the running launcher's list are in, and what it says. */
async function pillReady(page, tag, want = {}) {
  const pill = pillOf(page);
  const read = async () => ({
    state: await pill.getAttribute("data-model-pill"),
    disabled: await pill.isDisabled(),
    text: (await pill.textContent()) ?? "",
    title: (await pill.getAttribute("title")) ?? "",
    engine: await pill.locator("[data-pill-engine]").getAttribute("data-pill-engine").catch(() => null),
    effort: (await pill.locator("[data-pill-effort]").count()) ? await pill.locator("[data-pill-effort]").textContent() : "",
  });
  let now;
  await until(async () => {
    now = await read();
    return now.state === "ready" && !now.disabled && (!want.model || now.text.includes(want.model)) && (!want.engine || now.engine === want.engine)
      && (want.effort === undefined || now.effort === want.effort);
  }, async () => `${tag}: the pill says ${JSON.stringify(now)}, want ${JSON.stringify(want)}`);
  return now;
}

// a row picked by its name: the middle of a row can be its star or Customize (always shown on a touch screen)
const pickRow = (row) => row.click({ position: { x: 48, y: 12 } });

async function openPanel(page, tag) {
  await pillReady(page, tag);
  await pillOf(page).click();
  const panel = page.locator("[data-model-panel]");
  await expectVisible(panel, `${tag}: the model panel`);
  // every launcher's list is read on opening (three at a time): the groups' spinners go
  await until(async () => (await panel.locator('[role="status"]').count()) === 0, `${tag}: the panel's lists did not load`);
  return panel;
}

async function closePanel(page, tag) {
  // Esc closes the flyout, then the panel; never pressed once the panel is gone (it would close New agent)
  for (let i = 0; i < 3 && (await page.locator("[data-model-panel]").count()); i++) {
    await page.keyboard.press("Escape");
    await page.waitForTimeout(250);
  }
  if (await page.locator("[data-model-panel]").count()) fail(`${tag}: the model panel stays open`);
}

async function flyoutOf(page, panel, sel, tag) {
  const row = panel.locator(sel);
  await row.scrollIntoViewIfNeeded();
  await row.hover();
  await row.locator("[data-row-customize]").click();
  const fly = page.locator("[data-model-flyout]");
  await expectVisible(fly, `${tag}: the flyout of ${sel}`);
  await page.waitForTimeout(250);
  return fly;
}

/** The panel and its flyout stay inside the window, and the page doesn't scroll sideways. */
async function inWindow(page, tag) {
  const { width: vw, height: vh } = page.viewportSize();
  for (const sel of ["[data-model-panel]", "[data-model-flyout]"]) {
    const el = page.locator(sel);
    if (!(await el.count())) continue;
    const b = await el.boundingBox();
    if (!b || b.x < -0.5 || b.y < -0.5 || b.x + b.width > vw + 0.5 || b.y + b.height > vh + 0.5) fail(`${tag}: ${sel} leaves the window: ${JSON.stringify(b)}`);
  }
  const sideways = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  if (sideways > 0) fail(`${tag}: the page scrolls sideways (${sideways}px)`);
}

async function startNow(page, dialog, tag, w) {
  const before = (await started()).length;
  await dialog.getByRole("button", { name: w.start, exact: true }).click();
  await until(async () => (await started()).length > before, `${tag}: Start sent nothing`);
  await dialog.waitFor({ state: "detached", timeout: 8000 }).catch(() => fail(`${tag}: New agent stayed after Start`));
  await page.waitForTimeout(300); // a Codex start is asked again with its first message
  return (await started()).slice(before);
}

async function run(browser, pass) {
  const { theme, width, lang } = pass;
  const w = WORDS[lang];
  const tag = `${width}/${theme}/${lang}`;
  const { ctx, page } = await open(browser, pass);
  const shot = shooter(page, pass);

  // ── the pill and the panel ──
  let dialog = await newAgent(page, w);
  await machine(dialog, SERVER).click();
  const first = await pillReady(page, tag, { model: "model-chat", engine: "claude" });
  if (!(await pillOf(page).locator('[data-pill-engine] [data-agent-mark="claude"]').count())) fail(`${tag}: no Claude Code mark on the pill`);
  if (!first.title.includes("Claude Code")) fail(`${tag}: the pill's tooltip doesn't name Claude Code: ${first.title}`);
  await shot("default");
  ok(`${tag}: the pill shows Model settings' model-chat with Claude Code's mark`);

  let panel = await openPanel(page, tag);
  const follow = panel.locator("[data-follow-row]");
  if ((await follow.getAttribute("aria-selected")) !== "true") fail(`${tag}: the follow row isn't checked`);
  const followText = (await follow.textContent()) ?? "";
  if (!followText.includes(w.follow) || !followText.includes(FOLLOWED)) fail(`${tag}: the follow row says ${followText}`);
  if (!(await panel.locator(`${rowSel("claude", "default")}[data-current]`).count())) fail(`${tag}: claude's Default row isn't the one that runs`);
  ok(`${tag}: "${w.follow}" is checked, says ${FOLLOWED}, and claude's Default row is lit`);

  const presets = panel.locator('[data-panel-section="presets"]');
  if ((await presets.getAttribute("aria-label")) !== w.presets) fail(`${tag}: no Presets group`);
  const presetRows = await presets.locator("[data-panel-row]").evaluateAll((els) => els.map((e) => e.dataset.panelRow));
  if (JSON.stringify(presetRows) !== JSON.stringify(["preset:quick", "preset:review"])) fail(`${tag}: presets ${JSON.stringify(presetRows)}`);
  const groups = await panel.locator('[data-panel-section="provider"]').evaluateAll((els) => els.map((e) => e.getAttribute("aria-label")));
  for (const name of ["claude", "work_gw", "team_gw", "sonnetcc", "codex"]) {
    if (!groups.includes(name)) fail(`${tag}: no ${name} group in ${JSON.stringify(groups)}`);
  }
  const cells = await panel.locator("[data-rail-cell]").evaluateAll((els) => els.map((e) => e.dataset.railCell));
  for (const cell of ["recent", "favorites", "all", "claude", "work_gw", "team_gw", "sonnetcc", "codex"]) {
    if (!cells.includes(cell)) fail(`${tag}: no ${cell} cell on the rail ${JSON.stringify(cells)}`);
  }
  await expectVisible(panel.getByRole("button", { name: w.configure }), `${tag}: Configure models`);
  ok(`${tag}: the panel lists the presets, a group and a rail cell per launcher, and Configure models`);

  // the launchers that failed their check here: listed with why, rows off, How to fix (#1643)
  for (const [name, reason] of [["work_gw", w.terminal], ["team_gw", w.auth]]) {
    const group = panel.locator(`[data-panel-section="provider"][aria-label="${name}"]`);
    if (!((await group.textContent()) ?? "").includes(reason)) fail(`${tag}: ${name}'s group doesn't say ${reason}`);
    await expectVisible(group.getByRole("button", { name: w.fix }), `${tag}: ${name}'s How to fix`);
    const off = await group.locator("[data-panel-row]").evaluateAll((els) => els.map((e) => e.getAttribute("aria-disabled")));
    if (!off.length || off.some((x) => x !== "true")) fail(`${tag}: ${name}'s rows can be picked: ${JSON.stringify(off)}`);
  }
  await panel.locator('[data-panel-section="provider"][aria-label="work_gw"]').scrollIntoViewIfNeeded();
  await inWindow(page, tag);
  await shot("panel");
  ok(`${tag}: work_gw and team_gw are listed with their reasons, rows off, each with How to fix`);
  await panel.locator('[data-panel-section="provider"][aria-label="work_gw"]').getByRole("button", { name: w.fix }).click();
  const help = page.locator('[data-testid="launcher-blocked-help"]');
  await expectVisible(help, `${tag}: work_gw's How to fix`);
  if (await page.locator("[data-model-panel]").count()) fail(`${tag}: the panel stays over How to fix`);
  if (!((await help.textContent()) ?? "").includes("work_gw")) fail(`${tag}: How to fix doesn't name work_gw`);
  await help.getByRole("button", { name: w.close }).click();
  await help.waitFor({ state: "detached", timeout: 4000 }).catch(() => fail(`${tag}: How to fix didn't close`));
  ok(`${tag}: a blocked group's How to fix closes the panel and opens the help under the box`);

  // ── the search ──
  panel = await openPanel(page, tag);
  const search = panel.locator("input");
  await search.fill("glm");
  await until(async () => (await panel.locator(rowSel("codex", "gateway/glm-5.3")).count()) === 1, `${tag}: "glm" doesn't find codex's glm-5.3`);
  const found = await panel.locator('[data-panel-row]:not([data-follow-row])').evaluateAll((els) => els.map((e) => [e.dataset.panelRow, e.textContent ?? ""]));
  if (!found.some(([k]) => k === "model:claude\ngateway/glm-5.3")) fail(`${tag}: "glm" doesn't find claude's glm-5.3`);
  const stray = found.filter(([, text]) => !/glm/i.test(text));
  if (stray.length) fail(`${tag}: "glm" shows rows without it: ${JSON.stringify(stray)}`);
  if (await panel.locator('[data-panel-section="presets"]').count()) fail(`${tag}: "glm" keeps the presets`);
  await shot("search");
  await search.fill("no such model");
  await expectVisible(panel.getByText(w.noMatch), `${tag}: "no such model" matches nothing`);
  await search.fill("");
  await expectVisible(panel.locator('[data-panel-section="presets"]'), `${tag}: the list back after the search`);
  ok(`${tag}: the search finds glm-5.3 under claude and codex only, and says when nothing matches`);

  // ── the flyout of the row that runs: effort and ultracode ──
  let fly = await flyoutOf(page, panel, rowSel("claude", "default"), tag);
  const slider = fly.locator('[role="slider"]');
  if ((await slider.getAttribute("aria-valuetext")) !== w.unset) fail(`${tag}: the effort is ${await slider.getAttribute("aria-valuetext")}, want ${w.unset}`);
  const ultra = fly.locator('[data-testid="new-agent-ultracode"]');
  await expectVisible(ultra, `${tag}: the ultracode switch in the flyout`);
  const sw = ultra.getByRole("switch", { name: "ultracode" });
  if ((await sw.getAttribute("aria-checked")) !== "false") fail(`${tag}: ultracode starts on`);
  const sb = await slider.boundingBox();
  const ub = await ultra.boundingBox();
  if (ub.y < sb.y + sb.height - 1) fail(`${tag}: the ultracode switch isn't under the effort slider`);
  // from "Model default", → lands on the lowest level; two more steps up is High
  await slider.focus();
  for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowRight");
  await until(async () => (await slider.getAttribute("aria-valuetext")) === w.high, async () => `${tag}: the slider says ${await slider.getAttribute("aria-valuetext")}`);
  await sw.click();
  await until(async () => (await sw.getAttribute("aria-checked")) === "true", `${tag}: ultracode didn't turn on`);
  await pillReady(page, tag, { model: "model-chat", engine: "claude", effort: w.high });
  if (!(await pillOf(page).locator("[data-ultracode-mark]").count())) fail(`${tag}: no ultracode mark on the pill`);
  if ((await panel.locator("[data-follow-row]").getAttribute("aria-selected")) !== "true") fail(`${tag}: an effort unchecked the follow row`);
  await inWindow(page, tag);
  await shot("flyout");
  ok(`${tag}: the flyout of the row that runs sets the effort (High on the pill) and turns ultracode on (its mark on the pill)`);
  await closePanel(page, tag);

  // Start: what follows Model settings sends no launcher and no model, the effort and ultracode picked
  await dialog.locator("input[placeholder]").first().fill("/home/me/code/api");
  let sent = await startNow(page, dialog, tag, w);
  let last = sent.at(-1);
  if (last.machine_id !== "server" || last.launcher || last.model || last.effort !== "high" || last.ultracode !== true || last.permission_mode)
    fail(`${tag}: the start sent ${JSON.stringify(last)}`);
  ok(`${tag}: Start sends no launcher or model (Model settings decide), effort high and ultracode`);

  // ── glm-5.3 under Codex ──
  dialog = await newAgent(page, w);
  await pillReady(page, tag, { model: "model-chat", engine: "claude", effort: "" });
  if (await pillOf(page).locator("[data-ultracode-mark]").count()) fail(`${tag}: ultracode was remembered`);
  panel = await openPanel(page, tag);
  fly = await flyoutOf(page, panel, rowSel("claude", "gateway/glm-5.3"), tag);
  const engines = await fly.locator("[data-engine-choice]").evaluateAll((els) => els.map((e) => [e.dataset.engineChoice, e.getAttribute("aria-pressed"), e.disabled]));
  if (JSON.stringify(engines) !== JSON.stringify([["claude", "true", true], ["codex", "false", false]])) fail(`${tag}: glm-5.3's engines ${JSON.stringify(engines)}`);
  await inWindow(page, tag);
  await shot("engines");
  ok(`${tag}: glm-5.3's flyout offers Claude Code (its own) and Codex`);
  await fly.locator('[data-engine-choice="codex"]').click();
  await page.locator("[data-model-panel]").waitFor({ state: "detached", timeout: 5000 }).catch(() => fail(`${tag}: the panel stayed after the engine pick`));
  const codex = await pillReady(page, tag, { model: "glm-5.3", engine: "codex" });
  if (!codex.title.startsWith("Codex")) fail(`${tag}: the pill's tooltip ${codex.title}`);
  await until(async () => (await dialog.getByText(w.permission, { exact: true }).count()) === 0, `${tag}: Permission mode stays with Codex`);
  await shot("codex");
  ok(`${tag}: picking Codex makes the pill show the Codex engine, and Permission mode goes`);

  // ── another computer: no codex on the Mac Studio ──
  await machine(dialog, STUDIO).click();
  await page.waitForTimeout(500);
  await pillReady(page, tag, { model: "model-chat", engine: "claude" });
  const dropped = dialog.locator('[data-testid="new-agent-dropped"]');
  await expectVisible(dropped, `${tag}: the note that codex was dropped`);
  const droppedText = (await dropped.textContent()) ?? "";
  if (!droppedText.includes(STUDIO) || !droppedText.includes("codex")) fail(`${tag}: the note says ${droppedText}`);
  await expectVisible(dialog.getByText(w.permission, { exact: true }), `${tag}: Permission mode back with Claude Code`);
  await shot("dropped");
  ok(`${tag}: on the Mac Studio the Codex pick falls back to the default, said under the box`);

  panel = await openPanel(page, tag);
  if (await panel.locator('[data-rail-cell="codex"]').count()) fail(`${tag}: the Mac Studio's panel has codex`);
  if ((await panel.locator("[data-follow-row]").getAttribute("aria-selected")) !== "true") fail(`${tag}: the follow row isn't checked after the fallback`);
  // a pick by hand, then the follow row takes it back
  await pickRow(panel.locator(rowSel("claude", "opus")));
  await page.locator("[data-model-panel]").waitFor({ state: "detached", timeout: 5000 }).catch(() => fail(`${tag}: the panel stayed after a pick`));
  await pillReady(page, tag, { model: "Opus 5.5", engine: "claude" });
  if (await dropped.count()) fail(`${tag}: the dropped note stays after a pick`);
  panel = await openPanel(page, tag);
  if ((await panel.locator("[data-follow-row]").getAttribute("aria-selected")) !== "false") fail(`${tag}: the follow row is checked with Opus picked`);
  if (!(await panel.locator(`${rowSel("claude", "opus")}[data-current]`).count())) fail(`${tag}: Opus isn't lit`);
  await panel.locator("[data-follow-row]").click();
  await page.locator("[data-model-panel]").waitFor({ state: "detached", timeout: 5000 }).catch(() => fail(`${tag}: the panel stayed after the follow row`));
  await pillReady(page, tag, { model: "model-chat", engine: "claude" });
  panel = await openPanel(page, tag);
  if ((await panel.locator("[data-follow-row]").getAttribute("aria-selected")) !== "true") fail(`${tag}: the follow row isn't checked again`);
  await shot("follow");
  ok(`${tag}: a pick unchecks the follow row; the follow row goes back to Model settings' model`);
  await closePanel(page, tag);

  // ── Start with Codex's glm-5.3: the launcher and model go, no effort (Codex keeps its own) ──
  await machine(dialog, SERVER).click();
  await page.waitForTimeout(500);
  panel = await openPanel(page, tag);
  await pickRow(panel.locator(rowSel("codex", "gateway/glm-5.3")));
  await page.locator("[data-model-panel]").waitFor({ state: "detached", timeout: 5000 }).catch(() => fail(`${tag}: the panel stayed after the Codex pick`));
  await pillReady(page, tag, { model: "glm-5.3", engine: "codex" });
  await dialog.locator("input[placeholder]").first().fill("/home/me/code/api");
  await dialog.getByRole("textbox", { name: lang === "en" ? "First message" : "第一条消息" }).fill("README");
  sent = await startNow(page, dialog, tag, w);
  last = sent.at(-1);
  const wrong = (b) => b.machine_id !== "server" || b.launcher !== "codex" || b.model !== "gateway/glm-5.3" || b.effort || b.permission_mode || b.ultracode;
  if (!sent.length || sent.some(wrong)) fail(`${tag}: the Codex start sent ${JSON.stringify(sent)}`);
  if (last.prompt !== "README") fail(`${tag}: Codex didn't get its first message: ${JSON.stringify(last)}`);
  ok(`${tag}: Start sends launcher codex and model gateway/glm-5.3, no effort or permission mode`);
  await ctx.close();
}

const PASSES = [
  ...[1280, 390].flatMap((width) => ["light", "dark"].map((theme) => ({ width, theme, lang: "zh-Hans" }))),
  { width: 1280, theme: "light", lang: "en" },
];
const browser = await chromium.launch({ channel });
try {
  for (const pass of PASSES) await run(browser, pass);
} catch (e) {
  fail(e instanceof Error ? e.message.split("\n")[0] : String(e));
} finally {
  await browser.close();
}
console.log("PASS new agent model");
