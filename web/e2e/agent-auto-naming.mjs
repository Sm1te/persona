// Browser acceptance uses synthetic API/dialogue data only, never a live hub.
// BASE_URL=http://127.0.0.1:51812 SHOTS_DIR=/tmp/persona-4812-shots node web/e2e/agent-auto-naming.mjs
import assert from "node:assert/strict";
import { mkdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";
const base = new URL(process.env.BASE_URL || "http://127.0.0.1:51812");
assert.equal(base.hostname, "127.0.0.1", "Use an isolated loopback preview");
const output = resolve(process.env.SHOTS_DIR || "/tmp/persona-4812-shots");
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.PERSONA_E2E_CHROMIUM || "/usr/bin/google-chrome", headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const deadline = setTimeout(() => { console.error("TIMEOUT agent automatic naming"); process.exit(1); }, 180_000);
let screenshots = 0;
async function run(theme, lang, large = false, narrow = false) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/settings-assistantsettings.json`, import.meta.url), "utf8"));
  const context = await browser.newContext({ viewport: { width: narrow ? 390 : 1280, height: large ? 1400 : 1000 }, colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, lang, large }) => {
    localStorage.setItem("persona.token", "fixture-token");
    localStorage.setItem("persona.language", lang);
    localStorage.setItem("persona.theme", theme);
    localStorage.setItem("persona.text-size", large ? "large" : "standard");
  }, { theme, lang, large });
  const state = { manager: "assistant", agent_tasks: false, archive_closed_days: 3, agent_auto_naming: true, agent_auto_naming_max: 1, agent_auto_naming_interval_hours: 0 };
  const writes = [], errors = [];
  await context.route("**/*", (route) => new URL(route.request().url()).origin === base.origin ? route.continue() : route.abort());
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request(), path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "");
    if (path === "/assistant") {
      if (request.method() === "PUT") { assert.equal(request.headers()["x-persona-manager"], "assistant"); const patch = request.postDataJSON(); writes.push(patch); Object.assign(state, patch); }
      return route.fulfill({ json: { notes: "", conversations: [], ...state } });
    }
    assert.ok(["GET", "HEAD"].includes(request.method()), `Unexpected write ${path}`);
    const data = {
      "/sessions": { sessions: [], total: 0, has_more: false, revision: "fixture" },
      "/machines": { machines: [] }, "/interactions": { interactions: [] }, "/todos": { todos: [] },
      "/tasks": { tasks: [] }, "/memory": { files: [] },
      "/mutes": { spaces: [], sessions: [], allowed: [] }, "/pins": { spaces: [], sessions: [], sections: [] },
      "/agents/sessions": { sessions: [] }, "/agents/prefs": {},
      "/managers": { managers: [{ id: "assistant", name: "Persona", builtin: true, online: true }], default: "assistant" },
      "/personas": { personas: [] }, "/assistant/personas": { personas: [] }, "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
    };
    return route.fulfill({ json: data[path] ?? {} });
  });
  await context.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: ["managers"], hub_name: "Example hub" }));
    ws.send(JSON.stringify({ type: "machines", machines: [] }));
    ws.onMessage((message) => { const event = JSON.parse(String(message)); if (event.type === "app_ping") ws.send(JSON.stringify({ type: "pong", id: event.id })); });
  });
  const page = await context.newPage(); page.setDefaultTimeout(15000);
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(new URL("/settings?tab=persona#settings-agents", base).href);
  const naming = page.getByRole("switch", { name: s.auto_naming, exact: true });
  const periodic = page.getByRole("switch", { name: s.auto_naming_periodic, exact: true });
  const maximum = page.getByRole("spinbutton", { name: s.auto_naming_max, exact: true });
  await naming.waitFor(); assert.equal(await naming.getAttribute("aria-checked"), "true");
  assert.equal(await periodic.getAttribute("aria-checked"), "false"); assert.equal(await maximum.inputValue(), "1");
  const group = page.locator('[data-settings-group="agents"]');
  const shot = async (label) => {
    await group.scrollIntoViewIfNeeded(); await page.evaluate(() => document.fonts.ready);
    assert.ok(await group.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "settings group overflow");
    await group.screenshot({ path: join(output, `${label}-${lang}-${theme}${large ? "-large" : ""}${narrow ? "-phone" : ""}.png`) }); screenshots++;
  };
  if (large) await page.evaluate(() => { document.documentElement.style.fontSize = "200%"; });
  await shot("naming-default");
  await periodic.click(); const interval = page.getByRole("spinbutton", { name: s.auto_naming_interval, exact: true });
  await interval.waitFor(); assert.equal(await interval.inputValue(), "24"); await page.getByText(s.auto_naming_raise_max, { exact: true }).waitFor();
  await shot("naming-periodic-limit");
  await maximum.fill("5"); await maximum.press("Enter"); await page.waitForFunction(() => document.querySelector('input[max="1000"]')?.value === "5" && !document.querySelector('input[max="1000"]')?.disabled);
  await shot("naming-periodic");
  await periodic.click(); await interval.waitFor({ state: "detached" });
  await naming.click(); await maximum.waitFor({ state: "detached" });
  await shot("naming-disabled");
  assert.deepEqual(writes, [{ agent_auto_naming_interval_hours: 24 }, { agent_auto_naming_max: 5 }, { agent_auto_naming_interval_hours: 0 }, { agent_auto_naming: false }]);
  await page.reload(); await naming.waitFor(); assert.equal(await naming.getAttribute("aria-checked"), "false");
  assert.deepEqual(errors, []); await context.close();
}
try {
  for (const theme of ["light", "dark"]) { await run(theme, "zh-Hans"); await run(theme, "zh-Hans", true); await run(theme, "zh-Hans", false, true); }
  await run("light", "en"); await run("light", "ja");
  console.log(`PASS Agent naming: ${screenshots} screenshots; defaults, scoped saves, daily refresh, cap warning, disable and reload; light/dark, phone and 200% text.`);
} catch (e) { console.error(`FAIL Agent naming: ${e.message}`); process.exitCode = 1; }
finally { clearTimeout(deadline); await browser.close(); }
