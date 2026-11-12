// A Codex opened with no first message (#4961, docs/integrations/codex.md "Starting without a first message"): New agent
// starts a Codex with the message left empty; its chat waits for one (the note above the box, the box's placeholder, the
// row's second line); the first message sent from the chat moves the page to the thread it began.
//
// Needs the web page against the fake hub with MOCK_CODEX_WAITING=1, and Chrome (playwright-core drives it):
//
//   MOCK_CODEX_WAITING=1 PORT=18961 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18961 npx vite --port 5261 --strictPort &)
//   node web/e2e/codex-first-message.mjs http://127.0.0.1:5261 [--shots <dir>] [--locale zh-CN|en-US|ja-JP] [--executable <chromium>]
//
// Runs wide (1280) and phone width (390), light and dark; each run starts its own Codex. Prints ok / FAIL lines.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => (args.includes(name) ? args.splice(args.indexOf(name), 2)[1] : undefined);
const shots = flag("--shots");
const locale = flag("--locale") ?? "zh-CN";
const executable = flag("--executable");
const base = (args[0] ?? "").replace(/\/$/, "");
if (!base) {
  console.log("usage: node web/e2e/codex-first-message.mjs <web URL> [--shots <dir>] [--locale zh-CN|en-US|ja-JP] [--executable <chromium>]");
  process.exit(2);
}
if (shots) mkdirSync(shots, { recursive: true });

const lang = locale.startsWith("en") ? "en" : locale.startsWith("ja") ? "ja" : "zh";
const WORDS = {
  zh: { optional: "第一条消息（可选）", start: "启动", waiting: "等第一条消息", box: "给 Codex 的第一条消息", send: "发送", note: "Codex 已在「" },
  en: { optional: "First message (optional)", start: "Start", waiting: "Waiting for its first message", box: "First message to Codex", send: "Send", note: "Codex is open on" },
  ja: { optional: "最初のメッセージ（任意）", start: "開始", waiting: "最初のメッセージ待ち", box: "Codex への最初のメッセージ", send: "送信", note: "Codexは「" },
}[lang];
const SIZES = { wide: { width: 1280, height: 860 }, narrow: { width: 390, height: 844 } };
const RUNS = [["wide", "light"], ["wide", "dark"], ["narrow", "light"], ["narrow", "dark"]];
const WAITING = /\/c0de0000-[0-9a-f-]+$/;
const THREAD = /\/019a2b3c-[0-9a-f-]+$/; // the thread's page, under its workspace (/t/…) or not (/s/…)

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

async function shot(page, name, tag) {
  if (!shots) return;
  await page.waitForTimeout(400); // the dialog's and the note's entrances
  await page.screenshot({ path: join(shots, `${name}-${tag}.png`) });
}

async function openNewAgent(page, tag, narrow) {
  const name = /^(新建 Agent|New agent|新規エージェント)$/;
  let add = page.getByRole("button", { name }).filter({ visible: true });
  if (narrow) {
    await page.goto(`${base}/agents`);
    add = page.locator("main").getByRole("button", { name }).filter({ visible: true });
  }
  await add.first().click({ timeout: 8000 }).catch(() => fail(`${tag}: no New agent button`));
  await page.locator('[role="dialog"]').waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: the New agent dialog did not open`));
}

async function run(browser, size, theme) {
  const tag = `${size}-${theme}`;
  const narrow = size === "narrow";
  const ctx = await browser.newContext({ viewport: SIZES[size], deviceScaleFactor: 2, hasTouch: narrow, colorScheme: theme, locale });
  const page = await ctx.newPage();
  await page.goto(`${base}/?token=mock`);
  await page.waitForSelector("main textarea", { timeout: 15000 }).catch(() => fail(`${tag}: did not pair with the fake hub`));
  await page.evaluate((t) => localStorage.setItem("persona.theme", t), theme);
  await page.reload();
  await page.waitForSelector("main textarea", { timeout: 15000 });

  // New agent: the fake hub's 服务器, a folder, its Codex launcher (one the computer opens with no first message), the
  // message left empty
  await openNewAgent(page, tag, narrow);
  const dialog = page.locator('[role="dialog"]');
  await dialog.getByRole("button", { name: "服务器", exact: true }).click({ timeout: 8000 }).catch(() => fail(`${tag}: no 服务器 in New agent`));
  await dialog.getByText("weather", { exact: true }).first().click({ timeout: 8000 }).catch(() => fail(`${tag}: no weather folder in New agent`));
  // Codex through the model panel (#5133): its pill, then the name of a row the codex launcher runs (a row's middle can
  // be its favorite star)
  const pill = dialog.locator("[data-testid=new-agent-model] [data-model-pill]");
  await pill.click({ timeout: 8000 }).catch(() => fail(`${tag}: no model pill in New agent`));
  await page.locator('[data-panel-row^="model:codex"] .truncate').first().click({ timeout: 8000 }).catch(() => fail(`${tag}: no codex launcher in New agent`));
  if (!/^Codex\b/.test((await pill.getAttribute("title")) ?? "")) fail(`${tag}: picked ${await pill.getAttribute("title")}, not Codex`);
  if (!(await dialog.getByText(WORDS.optional).count())) fail(`${tag}: the first message is not offered as optional`);
  ok(`${tag}: New agent offers Codex's first message as optional`);
  await shot(page, "web-newagent-codex", tag);
  await dialog.getByRole("button", { name: WORDS.start, exact: true }).click();

  // the waiting row's chat
  await page.waitForURL(WAITING, { timeout: 15000 }).catch(() => fail(`${tag}: the start did not open the waiting row (${page.url()})`));
  const note = page.locator('[data-testid="codex-waiting"]');
  await note.waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: the chat does not say Codex waits for its first message`));
  if (!((await note.textContent()) ?? "").includes(WORDS.note)) fail(`${tag}: the note says ${await note.textContent()}`);
  const box = page.locator("main textarea").first();
  if ((await box.getAttribute("placeholder")) !== WORDS.box) fail(`${tag}: the box says ${await box.getAttribute("placeholder")}`);
  ok(`${tag}: the waiting chat has its note and its box`);
  if (!narrow) {
    const sid = new URL(page.url()).pathname.split("/").pop();
    const row = page.locator(`a[href$="${sid}"]`).first();
    await row.waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: the waiting row is not in the sidebar`));
    if (!((await row.textContent()) ?? "").includes(WORDS.waiting)) fail(`${tag}: the row says ${await row.textContent()}`);
    ok(`${tag}: the sidebar row says it waits for its first message`);
  }
  await shot(page, "web-codex-waiting", tag);

  // its first message: the page follows the thread it began
  const message = `${tag}: fix "the" weather icon — 天气图标`;
  await box.fill(message);
  await page.getByRole("button", { name: WORDS.send, exact: true }).filter({ visible: true }).first().click();
  await page.waitForURL(THREAD, { timeout: 15000 }).catch(() => fail(`${tag}: the page did not move to the thread (${page.url()})`));
  await page.getByText(message).first().waitFor({ timeout: 8000 }).catch(() => fail(`${tag}: the thread does not show the first message`));
  if (await page.locator('[data-testid="codex-waiting"]').count()) fail(`${tag}: the thread still says it waits`);
  ok(`${tag}: the first message moved the page to its thread`);
  await shot(page, "web-codex-thread", tag);
  await ctx.close();
}

const browser = await chromium.launch(executable ? { executablePath: executable } : { channel: "chrome" });
try {
  for (const [size, theme] of RUNS) await run(browser, size, theme);
} finally {
  await browser.close();
}
console.log("PASS");
