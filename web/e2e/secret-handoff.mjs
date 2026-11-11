// Screenshots of secret references and the hand-off card (#3962) in the real web app: the card in the main
// conversation, a secret that holds a reference with its dialog, and a secret provider's page (who uses its references,
// its Always rules, the confirm before one is removed). Light and dark, desktop and phone.
// Usage: node web/e2e/secret-handoff.mjs <web-origin> <artifactDir> [--chromium <executable>]
// Every hub request and the socket are answered in the browser with invented data; nothing reaches a hub.
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const chromiumAt = args.includes("--chromium") ? args.splice(args.indexOf("--chromium"), 2)[1] : undefined;
const [address, artifacts, ...extra] = args;
assert.ok(address && artifacts && !extra.length, "Supply the web origin and artifact directory");
const base = new URL(address);
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Use a loopback web server");
const output = resolve(artifacts);
await mkdir(output, { recursive: true });

const AT = "2026-01-01T10:00:00Z";
const MAIN = "example-main";
const MACHINE = { id: "example", name: "Computer A", online: true, os: "linux" };
const LAPTOP = { id: "laptop", name: "Laptop", online: true, os: "darwin" };
const SESSION = { machine_id: MACHINE.id, session_id: MAIN, title: "Persona", state: "needs_you", status: "needs_you", cwd: "/example", updated_at: AT, started_by_persona: true, assistant: true, capabilities: { send: true, interrupt: true, approve: true } };
const ITEMS = [
  { id: "u1", seq: 1, role: "user", kind: "text", text: "Start an Agent on the laptop to fix the release CI; it needs the example registry token.", ts: AT },
  { id: "a1", seq: 2, role: "assistant", kind: "text", text: "I'll start it with the token from the example vault once you approve.", ts: AT },
];
const HANDOFF = {
  id: "example-handoff", machine_id: MACHINE.id, session_id: MAIN, kind: "permission", agent: "claude", tool_name: "secret_handoff", created_at: AT,
  tool_title: "Hand secrets to an Agent",
  tool_input: {
    variables: [
      { name: "REGISTRY_TOKEN", provider: "example-vault", provider_name: "Example Vault", title: "Registry token › credential" },
      { name: "DEPLOY_KEY", provider: "example-vault", provider_name: "Example Vault", title: "Deploy key › private key" },
    ],
    agent: "Fix the release CI", machine_name: LAPTOP.name, cwd: "~/code/example-app", prompt: "Fix the release CI: the publish step fails to log in to the registry.",
  },
  summary: "Give Example Vault › Registry token › credential as REGISTRY_TOKEN to “Fix the release CI” on Laptop, for this run only. Approve on iPhone or the web.",
  always_label: "skips this card for these items with claude on Laptop, in conversations you start yourself", always_honored: true,
};
const NOTES = {
  id: "example-notes", name: "Example Notes", kind: "process", status: "running", source: "installed", enabled: true, version: "1.2.0",
  secrets: [
    { name: "api_key", title: "API key", set: true, reference: { provider: "example-vault", provider_name: "Example Vault", reference: "op://Example/Notes API/credential", title: "Notes API › credential" } },
    { name: "webhook_secret", title: "Webhook secret", set: true, hint: "4f2a" },
  ],
};
const VAULT = {
  id: "example-vault", name: "Example Vault", kind: "process", status: "running", source: "installed", enabled: true, version: "0.2.0",
  contributes: { secret_providers: ["vault"] },
  secret_users: [
    { kind: "extension", extension: "example-notes", extension_name: "Example Notes", secret: "api_key", secret_title: "API key", reference: "op://Example/Notes API/credential" },
    { kind: "launcher", launcher: "gateway", launcher_name: "Example gateway", machine: LAPTOP.id, reference: "op://Example/Gateway/key" },
  ],
  handoff_rules: [
    { id: "0123456789abcdef", reference: "op://Example/Registry token/credential", launcher: "claude", machine: LAPTOP.id, by: "device:d1", at: AT },
  ],
};

const result = { status: "running", runs: [] };
const browser = await chromium.launch(chromiumAt ? { executablePath: chromiumAt, args: ["--no-sandbox"] } : { channel: "chrome" });
const deadline = setTimeout(() => { result.status = "timeout"; void browser.close(); }, 300_000);
const settle = (page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
try {
  for (const [width, height] of [[1280, 820], [390, 844]]) for (const theme of ["light", "dark"]) {
    const name = `${width}x${height}-${theme}`;
    const touch = width < 500;
    const context = await browser.newContext({ viewport: { width, height }, locale: "en-US", colorScheme: theme, reducedMotion: "reduce", isMobile: touch, hasTouch: touch });
    await context.addInitScript(`localStorage.setItem('persona.token','example');localStorage.setItem('persona.language','en');localStorage.setItem('persona.theme','${theme}');`);
    await context.route(/^https?:\/\/(?!127\.0\.0\.1|localhost|\[::1\])/, (route) => route.abort());
    await context.route("**/api/v1/**", (route) => {
      const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
      if (route.request().method() !== "GET") return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
      const data = path === "/machines" ? { machines: [MACHINE, LAPTOP] }
        : path === "/sessions" ? { sessions: [SESSION] }
        : path === "/interactions" ? { interactions: [HANDOFF] }
        : path === "/assistant" ? { machine_id: MACHINE.id, main: MAIN, conversations: [SESSION], tasks: [], memory: [] }
        : path.endsWith("/feed") ? { items: [], plans: {}, task_cards: [] }
        : path === "/todos" ? { todos: [] }
        : path.endsWith("/items") ? { items: ITEMS, has_more: false }
        : /^\/sessions\/[^/]+\/[^/]+$/.test(path) ? { session: SESSION }
        : path === "/extensions" ? { extensions: [NOTES, VAULT] }
        : path === `/extensions/${NOTES.id}/-/` ? NOTES
        : path === `/extensions/${VAULT.id}/-/` ? VAULT
        : {};
      return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(data) });
    });
    await context.routeWebSocket("**/api/v1/ws**", (ws) => {
      ws.send(JSON.stringify({ type: "hello", server_version: "example", hub_name: "Example hub", features: ["extensions", "extension_manager", "extension_secret_references"] }));
      ws.onMessage((m) => { if (String(m).includes("app_ping")) ws.send(JSON.stringify({ type: "pong" })); });
    });
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", (e) => errors.push(String(e)));

    // the card in the main conversation
    await page.goto(new URL("/", base).href);
    await page.locator("[data-assistant-actionbar]").waitFor({ timeout: 15_000 });
    if (!(await page.locator(`[data-card="${HANDOFF.id}"] [data-handoff-variable]`).count())) await page.locator('[data-tray-entry="pending"]').click();
    await page.locator(`[data-card="${HANDOFF.id}"] [data-handoff-variable]`).first().waitFor({ timeout: 10_000 });
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-card.png` });
    const card = await page.locator(`[data-card="${HANDOFF.id}"]`).innerText();
    assert.ok(card.includes("REGISTRY_TOKEN") && !card.includes("op://"), `${name}: the card's words`);

    // a secret that holds a reference, and its dialog
    await page.goto(new URL(`/settings?tab=extensions&extension=${NOTES.id}`, base).href);
    await page.getByText("From Example Vault").first().waitFor({ timeout: 15_000 });
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-secret.png`, fullPage: false });
    await page.getByText("From Example Vault").first().click();
    await page.locator('input[aria-label="API key"]').waitFor({ timeout: 10_000 });
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-secret-dialog.png` });

    // the provider's page: who uses its references, its Always rules, the confirm before removing one
    await page.goto(new URL(`/settings?tab=extensions&extension=${VAULT.id}`, base).href);
    await page.locator("[data-handoff-rules]").waitFor({ timeout: 15_000 });
    await page.locator("[data-secret-users]").scrollIntoViewIfNeeded();
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-provider.png` });
    await page.locator("[data-handoff-rules] button", { hasText: "Remove" }).click();
    await page.getByText("Remove this rule?").waitFor({ timeout: 10_000 });
    await settle(page);
    await page.screenshot({ path: `${output}/${name}-provider-remove.png` });

    const run = { name, errors };
    result.runs.push(run);
    console.log(JSON.stringify(run));
    assert.deepEqual(errors, [], `${name}: page errors`);
    await context.close();
  }
  result.status = "pass";
} catch (error) {
  result.status = result.status === "timeout" ? "timeout" : "fail";
  result.error = String(error);
} finally {
  clearTimeout(deadline);
  await writeFile(`${output}/results.json`, JSON.stringify(result, null, 2));
  await browser.close().catch(() => {});
  console.log(`${result.status.toUpperCase()} secret hand-off screenshots${result.error ? `: ${result.error.split("\n")[0]}` : ""}`);
  process.exit(result.status === "pass" ? 0 : 1);
}
