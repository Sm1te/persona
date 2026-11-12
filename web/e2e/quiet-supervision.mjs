// Real app browser regression with synthetic HTTP/WS only; no live hub or main conversation.
// node web/e2e/quiet-supervision.mjs <web URL> --shots <directory> [--chromium <executable>]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = args[0]?.replace(/\/$/, "");
const shots = flag("--shots");
if (!base || !shots) {
  console.error("usage: node web/e2e/quiet-supervision.mjs <web URL> --shots <directory> [--chromium <executable>]");
  process.exit(2);
}
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT quiet-supervision browser verification (180s)"); process.exit(124); }, 180_000);
const AT = "2026-11-01T10:00:00Z";
const item = (seq, role, kind, extra) => ({ id: `demo-${seq}#0`, seq, ts: AT, role, kind, ...extra });
// i18n-ignore: fixed injected protocol markers.
const raw = '📋 督办：你派出去的任务有 1 个要你处理\n<persona-supervision level="triage" />\n这是 Persona 的督办消息\nInspect the fictional release evidence and check the example task.';
const input = item(1, "user", "text", { text: raw });
const thought = item(2, "assistant", "thinking", { text: "Checking the fictional evidence against the acceptance criteria." });
const read = item(3, "assistant", "tool_use", { tool_name: "Read", tool_use_id: "demo-read", tool_input: { file_path: "/tmp/fictional-evidence.txt" } });
const readResult = item(4, "user", "tool_result", { tool_use_id: "demo-read", text: "The synthetic checks passed." });
const finish = item(5, "assistant", "tool_use", { tool_name: "supervision_finish", tool_use_id: "demo-finish", tool_input: { visibility: "silent", summary: "Checked the example task." } });
const finishResult = item(6, "user", "tool_result", { tool_use_id: "demo-finish", text: '{"visibility":"silent","summary":"Checked the example task.","level":"triage"}' });
const answer = item(7, "assistant", "text", { text: "The fictional release evidence is complete. No user action is needed." });
const next = item(8, "user", "text", { text: "Now check the next example task.", queued: true, client_msg_id: "demo-next" });
const nextAnswer = item(9, "assistant", "text", { text: "I will check the next example task separately." });
const folded = (promoted) => item(2, "assistant", "fold", { id: "fold:demo-2#0", fold: { items: 3, tools: 1, thoughts: 1, through_seq: 4, until: AT, supervision_user: promoted } });
const questions = [{ question: "Which fictional rollout should we use?", header: "Rollout", options: [{ label: "Small batch", description: "Start with the demo group" }, { label: "All examples", description: "Use the full demo set" }] }];
const pending = item(3, "assistant", "tool_use", { tool_name: "mcp__persona__ask_user", tool_use_id: "demo-question", tool_input: { questions } });

async function run(browser, size, theme, scenario) {
  const sid = `demo-${scenario}`;
  const live = scenario === "running" || scenario === "ask";
  const session = { machine_id: "demo", session_id: sid, title: "Example supervision", cwd: "/tmp/fictional-project", assistant: true, started_by_persona: true, state: live ? "working" : "idle", status: live ? "working" : "idle", updated_at: AT, state_since: AT, capabilities: { send: true, read: true } };
  const items = scenario === "ask" ? [input, thought, pending]
    : scenario === "running" ? [input, thought, read]
    : [input, folded(scenario === "promoted"), finish, scenario === "user" ? { ...finishResult, text: '{"visibility":"user","summary":"A fictional user decision is needed.","level":"user"}' } : finishResult, answer, next, nextAnswer];
  const context = await browser.newContext({ viewport: size === "wide" ? { width: 1280, height: 1000 } : { width: 390, height: 844 }, colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, now }) => {
    const NativeDate = Date;
    globalThis.Date = class extends NativeDate {
      constructor(...args) { super(...(args.length ? args : [now])); }
      static now() { return now; }
    };
    localStorage.setItem("persona.token", "synthetic-demo-token");
    localStorage.setItem("persona.language", "zh-Hans");
    localStorage.setItem("persona.theme", theme);
  }, { theme, now: Date.parse(AT) + 1000 });
  const interactions = scenario === "ask" ? [{ id: "demo-question-card", machine_id: "demo", session_id: sid, session_title: "Persona", kind: "question", tool_name: "ask_user", questions, created_at: AT }] : [];
  const todo = { id: "demo-task", title: "Check the example release", origin: sid, created_at: AT, updated_at: AT, agents: [] };
  const feed = scenario === "receipt" ? [
    { id: "demo-progress", conversation: sid, kind: "report", ref: todo.id, at: AT, body: { kind: "progress", text: "Checked two of three fictional criteria.", step: 2, steps: 3, evidence: ["https://example.com/evidence"] } },
    { id: "demo-checking", conversation: sid, kind: "receipt", ref: todo.id, at: AT, body: { state: "done", title: todo.title, summary: "The example agent submitted evidence for Persona to follow up.", review: { state: "checking", criteria: 3 } } },
  ] : [];
  await context.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, "");
    let body = {};
    if (path === "/machines") body = { machines: [{ id: "demo", name: "Example computer", online: true, os: "linux" }] };
    else if (path === "/sessions") body = { sessions: [session] };
    else if (path === "/interactions") body = { interactions };
    else if (path === "/assistant") body = { machine_id: "demo", main: sid, conversations: [session], tasks: [], memory: [] };
    else if (path === "/todos") body = { todos: scenario === "receipt" ? [todo] : [] };
    else if (path === `/sessions/demo/${sid}`) body = { session };
    else if (path.endsWith("/feed")) body = { items: feed };
    else if (path.endsWith("/items")) body = { items: url.searchParams.has("from_seq") ? [thought, read, readResult] : url.searchParams.has("after_seq") ? [] : items, has_more: false };
    else if (path === "/extensions/ui") body = { slots: [] };
    else if (path === "/extensions") body = { extensions: [] };
    else if (path === "/managers") body = { managers: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await context.routeWebSocket("**/api/v1/ws**", (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", hub_name: "Example hub", features: [] }));
    ws.onMessage((rawMessage) => {
      const message = JSON.parse(String(rawMessage));
      if (message.type === "app_ping") ws.send(JSON.stringify({ type: "pong" }));
      if (message.type === "subscribe" && scenario === "running") ws.send(JSON.stringify({ type: "delta", machine_id: "demo", session_id: sid, text: "Checking the fictional task quietly." }));
    });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.setDefaultTimeout(12_000);
  await page.goto(`${base}/`);
  await page.locator("[data-chat-scroll]").waitFor();
  const chat = page.locator("[data-chat-scroll]");
  const quiet = chat.locator("[data-supervision]");
  const tag = `${size}-${theme}-${scenario}`;
  const shot = async (name) => {
    assert.equal(await page.locator("vite-error-overlay").count(), 0, "no Vite overlay");
    assert.deepEqual(errors, [], "no page errors");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth), false, "no sideways page overflow");
    await page.screenshot({ path: join(shots, `${tag}-${name}.png`) });
  };
  if (scenario === "silent" || scenario === "running") {
    await quiet.waitFor();
    assert.equal(await quiet.locator(":scope > button").getAttribute("aria-expanded"), "false");
    assert(!(await chat.textContent()).includes(answer.text), "silent answer is hidden");
    if (!live) assert((await chat.textContent()).includes(next.text), "next user is outside the quiet round");
    await shot("folded");
    await quiet.locator(":scope > button").click();
    await quiet.locator("[data-supervision-input]").waitFor();
    assert.equal(await quiet.locator("[data-supervision-input] pre").textContent(), raw);
    if (!live) {
      await quiet.locator("[data-work-group] > button").first().click();
      await quiet.locator('[data-action-row="Read"]').waitFor();
      assert((await quiet.textContent()).includes(answer.text));
      assert(!(await quiet.textContent()).includes(next.text));
    }
    await shot("expanded");
    if (!live && size === "wide") {
      // a thought that fits on its row says it there; one that does not opens in place. Rows come in their own chunk (#5155)
      await quiet.locator('[data-thinking="words"]').waitFor();
      const thoughtRow = quiet.locator('[data-thinking="words"] > [role="button"]');
      if (await thoughtRow.count()) await thoughtRow.click();
      await quiet.locator('[data-action-row="Read"] > button').click();
      await quiet.locator('[data-action-row="supervision_finish"] > button').click();
      assert((await quiet.textContent()).includes(thought.text), "original thoughts are inspectable");
      assert((await quiet.textContent()).includes(readResult.text), "Read shows its historical result, not today's file");
      assert((await quiet.textContent()).includes(finishResult.text), "original finish receipt is inspectable");
      await page.setViewportSize({ width: 1280, height: 1500 });
      await shot("trace-details");
    }
  } else if (scenario === "promoted" || scenario === "user") {
    await chat.locator('[data-action-row="supervision_finish"]').waitFor();
    assert.equal(await quiet.count(), 0, "authoritative folded promotion survives a later silent finish");
    assert((await chat.textContent()).includes(answer.text));
    await shot("visible");
  } else if (scenario === "ask") {
    await page.getByText(questions[0].question, { exact: true }).waitFor();
    assert.equal(await quiet.count(), 0, "ask_user is visible while waiting for its result");
    assert(await page.getByRole("button", { name: "Small batch", exact: false }).count() > 0, "pending answer options are visible");
    await shot("pending-question");
  } else {
    await page.getByText("由 Persona 跟进", { exact: true }).waitFor();
    assert.equal(await page.getByText("等 Persona 验收", { exact: false }).count(), 0, "checking is not pending acceptance");
    const progress = page.locator('[data-testid="progress-report"]');
    assert.equal(await progress.locator('[data-testid="report-line"]').count(), 0, "progress defaults to folded");
    await shot("followup-and-progress");
    await progress.locator("button").first().click();
    await progress.getByText("Checked two of three fictional criteria.", { exact: false }).waitFor();
  }
  console.log(`PASS ${tag}: real app, synthetic data, quiet/visible boundaries verified`);
  await context.close();
}

let browser;
try {
  browser = await chromium.launch({ executablePath: flag("--chromium") ?? process.env.PERSONA_E2E_CHROMIUM ?? "/usr/bin/google-chrome", args: ["--no-sandbox"] });
  for (const size of ["wide", "narrow"]) for (const theme of ["light", "dark"]) for (const scenario of ["silent", "running", "promoted", "user", "ask", "receipt"]) await run(browser, size, theme, scenario);
  console.log("PASS quiet-supervision browser verification");
} catch (error) {
  console.error("FAIL quiet-supervision browser verification", error);
  process.exitCode = 1;
} finally {
  await browser?.close();
  clearTimeout(watchdog);
}
