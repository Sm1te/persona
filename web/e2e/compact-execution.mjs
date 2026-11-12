// Actual app browser regression, synthetic HTTP/WS only; no production hub or conversation writes.
// node web/e2e/compact-execution.mjs <loopback web URL> <screenshot directory> [Chrome executable]
import assert from "node:assert/strict";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright-core";

const [base, shots, executable = "/opt/google/chrome/chrome"] = process.argv.slice(2);
if (!base || !shots || !/^https?:\/\/(127\.0\.0\.1|localhost)(:\d+)?\/?$/.test(base)) {
  console.error("usage: node web/e2e/compact-execution.mjs <loopback web URL> <screenshot directory> [Chrome executable]");
  process.exit(2);
}
mkdirSync(shots, { recursive: true });
const watchdog = setTimeout(() => { console.error("TIMEOUT compact-execution browser verification (180s)"); process.exit(124); }, 180_000);
const AT = "2026-11-01T10:00:00Z";
const item = (seq, role, kind, extra) => ({ id: `example-${seq}#0`, seq, ts: new Date(Date.parse(AT) + seq * 1000).toISOString(), role, kind, ...extra });
const text = (seq, words) => item(seq, "assistant", "text", { text: words });
const call = (seq, name, input, done = true) => [
  item(seq, "assistant", "tool_use", { tool_name: name, tool_use_id: `call-${seq}`, tool_input: input }),
  ...(done ? [item(seq + 1, "user", "tool_result", { tool_use_id: `call-${seq}`, text: "Synthetic check passed." })] : []),
];
const initial = [
  item(1, "user", "text", { text: "Check the example project and report the final result." }),
  item(2, "assistant", "thinking", { text: "Comparing the fictional checks." }),
  ...call(3, "Read", { file_path: "/tmp/example-project/README.md" }),
  text(5, "First progress: the example files are loaded."),
  ...call(6, "Bash", { command: "npm test", description: "Check the example tests" }),
  text(8, "Latest progress: checking the example build."),
  ...call(9, "Bash", { command: "npm run build", description: "Build the example project" }, false),
];
const changed = [...initial, item(10, "user", "tool_result", { tool_use_id: "call-9", text: "Synthetic build passed." }), text(11, "New progress: reviewing the final checks."), ...call(12, "Read", { file_path: "/tmp/example-project/report.md" }, false)];
const finished = [...changed, item(13, "user", "tool_result", { tool_use_id: "call-12", text: "All fictional checks passed." }), text(14, "Final result: the example tests and build passed. No further action is needed.")];

async function verify(browser, kind, theme, width) {
  const sid = `example-${kind}`;
  const assistant = kind !== "agent";
  let items = initial;
  let live = true;
  let socket;
  const session = () => ({ machine_id: "example", session_id: sid, title: assistant ? "Example conversation" : "Example Agent", agent: "claude", cwd: "/tmp/example-project", assistant, started_by_persona: assistant, state: live ? "working" : "idle", status: live ? "working" : "idle", running: true, updated_at: AT, state_since: AT, capabilities: { read: true, send: true, interrupt: true } });
  const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, at }) => {
    localStorage.setItem("persona.token", "synthetic-example-token");
    localStorage.setItem("persona.language", "en");
    localStorage.setItem("persona.theme", theme);
    const NativeDate = Date;
    const began = NativeDate.now();
    globalThis.Date = class extends NativeDate {
      constructor(...args) { super(...(args.length ? args : [at + NativeDate.now() - began])); }
      static now() { return at + NativeDate.now() - began; }
    };
  }, { theme, at: Date.parse(AT) + 18000 });
  await context.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname.replace(/^\/api\/v1/, "");
    assert(["GET", "HEAD"].includes(route.request().method()) || /\/(seen|read|today)$/.test(path), `no conversation writes: ${path}`);
    let body = {};
    if (path === "/machines") body = { machines: [{ id: "example", name: "Example computer", online: true, os: "linux" }] };
    else if (path === "/sessions") body = { sessions: [session()] };
    else if (path === "/interactions") body = { interactions: [] };
    else if (path === "/assistant") body = { machine_id: "example", main: kind === "main" ? sid : "example-main", conversations: assistant ? [session()] : [], tasks: [], memory: [] };
    else if (path === "/todos") body = { todos: [] };
    else if (path === `/sessions/example/${sid}`) body = { session: session() };
    else if (path.endsWith("/items")) body = { items: url.searchParams.has("after_seq") ? items.filter((i) => i.seq > Number(url.searchParams.get("after_seq"))) : items, has_more: false };
    else if (path.endsWith("/feed")) body = { items: [] };
    else if (path === "/extensions/ui") body = { slots: [] };
    else if (path === "/extensions") body = { extensions: [] };
    else if (path === "/managers") body = { managers: [] };
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
  });
  await context.routeWebSocket("**/api/v1/ws**", (ws) => {
    socket = ws;
    ws.send(JSON.stringify({ type: "hello", server_version: "synthetic", hub_name: "Example hub", features: [] }));
    ws.onMessage((raw) => { if (JSON.parse(String(raw)).type === "app_ping") ws.send(JSON.stringify({ type: "pong" })); });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  page.setDefaultTimeout(12000);
  const path = kind === "main" ? "/" : kind === "ordinary" ? `/persona/${sid}` : `/s/example/${sid}`;
  await page.goto(base.replace(/\/$/, "") + path);
  const chat = page.locator("[data-chat-scroll]");
  const summary = chat.locator("[data-work-group] > button").first();
  const slot = chat.locator("[data-latest-text]");
  await slot.getByText("Latest progress: checking the example build.", { exact: true }).waitFor();
  assert.equal(await summary.getAttribute("aria-expanded"), "false");
  assert.equal(await chat.locator("[data-work-group]").count(), 1);
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 0);
  assert.equal(await chat.getByText("First progress: the example files are loaded.", { exact: true }).count(), 0);
  await slot.evaluate((el) => { el.dataset.identity = "fixed-text-slot"; });
  // After a reload the chat's header can still grow a line, moving the summary under a click: wait until it holds still.
  const stillThenClick = async (target) => {
    let last = "";
    for (let i = 0; i < 20; i++) {
      const box = JSON.stringify(await target.boundingBox());
      if (box === last) break;
      last = box;
      await page.waitForTimeout(250);
    }
    await target.click();
  };
  const shot = async (state) => {
    const expanded = state.endsWith("expanded");
    // Keep the full manually opened trace in the screenshot rather than behind the fixed composer.
    if (expanded) await page.setViewportSize({ width, height: 1800 });
    if (expanded) await page.waitForFunction(() => {
      const frame = document.querySelector("[data-work-detail]")?.parentElement?.parentElement;
      return frame && frame.getBoundingClientRect().height > 120 && Number(getComputedStyle(frame).opacity) > 0.99;
    });
    assert.deepEqual(errors, [], "no page errors");
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "no horizontal overflow");
    await page.screenshot({ path: join(shots, `web-${kind}-${width}-${theme}-${state}.png`) });
    if (expanded) await page.setViewportSize({ width, height: 900 });
  };
  await shot("running");
  items = changed;
  socket.send(JSON.stringify({ type: "items", machine_id: "example", session_id: sid, items: changed.filter((i) => i.seq >= 10) }));
  await slot.getByText("New progress: reviewing the final checks.", { exact: true }).waitFor();
  assert.equal(await slot.getAttribute("data-identity"), "fixed-text-slot", "text updates in the same slot");
  assert.equal(await chat.getByText("Latest progress: checking the example build.", { exact: true }).count(), 0);
  await summary.click();
  await chat.locator('[data-action-row="Bash"]').first().waitFor();
  assert.equal(await summary.getAttribute("aria-expanded"), "true");
  assert(await chat.getByText("First progress: the example files are loaded.", { exact: true }).isVisible());
  // #5155: opened, the narration and an inner summary for each run between, all open with it; the last run is live
  const inner = chat.locator("[data-work-detail] [data-work-group]");
  assert.deepEqual(await inner.evaluateAll((gs) => gs.map((g) => [g.dataset.workGroup, g.querySelector(":scope > button").getAttribute("aria-expanded")])),
    [["done", "true"], ["done", "true"], ["live", "true"]]);
  await page.waitForFunction(() => document.querySelector("[data-work-detail]")?.parentElement?.parentElement?.style.gridTemplateRows === "1fr");
  await shot("running-expanded");
  items = finished;
  live = false;
  socket.send(JSON.stringify({ type: "items", machine_id: "example", session_id: sid, items: finished.filter((i) => i.seq >= 13) }));
  socket.send(JSON.stringify({ type: "sessions", machine_id: "example", sessions: [session()] }));
  await slot.getByText("Final result: the example tests and build passed. No further action is needed.", { exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector("[data-work-group] > button")?.getAttribute("aria-expanded") === "false");
  await chat.locator("[data-work-detail]").first().waitFor({ state: "detached" });
  await page.getByText("Running", { exact: true }).waitFor({ state: "hidden" });
  assert.equal(await slot.getAttribute("data-identity"), "fixed-text-slot", "completion uses the same slot");
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 0);
  await shot("completed");
  await summary.click();
  assert(await chat.getByText("New progress: reviewing the final checks.", { exact: true }).isVisible());
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 4);
  assert.deepEqual(await inner.evaluateAll((gs) => gs.map((g) => [g.dataset.workGroup, g.querySelector(":scope > button").getAttribute("aria-expanded")])),
    [["done", "true"], ["done", "true"], ["done", "true"], ["done", "true"]]);
  await page.waitForFunction(() => document.querySelector("[data-work-detail]")?.parentElement?.parentElement?.style.gridTemplateRows === "1fr");
  await shot("completed-expanded");
  await summary.click();
  await page.reload();
  await slot.getByText("Final result: the example tests and build passed. No further action is needed.", { exact: true }).waitFor();
  assert.equal(await summary.getAttribute("aria-expanded"), "false", "reloaded history is folded");
  await stillThenClick(summary);
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 4);
  // Reload creates a new document; mark that document's slot before checking wake-time identity.
  await slot.evaluate((el) => { el.dataset.identity = "fixed-text-slot"; });
  // A background wake reopens this exchange before its recap. A historical manual choice must not reopen the run.
  const resumed = [...finished,
    item(15, "system", "recap", { text: "Synthetic prior context." }),
    item(16, "system", "notice", { wake: true, text: "Synthetic background work resumed." }),
    ...call(17, "Read", { file_path: "/tmp/example-project/follow-up.md" }),
    text(19, "Wake progress: checking the follow-up."),
    ...call(20, "Bash", { command: "npm test", description: "Check the synthetic follow-up" }, false),
  ];
  items = resumed;
  live = true;
  socket.send(JSON.stringify({ type: "items", machine_id: "example", session_id: sid, items: resumed.filter((i) => i.seq >= 15) }));
  socket.send(JSON.stringify({ type: "sessions", machine_id: "example", sessions: [session()] }));
  await slot.getByText("Wake progress: checking the follow-up.", { exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector("[data-work-group] > button")?.getAttribute("aria-expanded") === "false");
  await chat.locator("[data-work-detail]").first().waitFor({ state: "detached" });
  assert.equal(await chat.locator("[data-work-group]").count(), 1, "no extra placeholder or wake drawer");
  assert.equal(await slot.getAttribute("data-identity"), "fixed-text-slot", "wake retains the same text slot");
  await shot("wake-running");
  await summary.click();
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 6);
  await shot("wake-running-expanded");
  items = [...resumed, item(21, "user", "tool_result", { tool_use_id: "call-20", text: "Synthetic follow-up passed." }), text(22, "Wake final result: follow-up complete.")];
  live = false;
  socket.send(JSON.stringify({ type: "items", machine_id: "example", session_id: sid, items: items.filter((i) => i.seq >= 21) }));
  socket.send(JSON.stringify({ type: "sessions", machine_id: "example", sessions: [session()] }));
  await slot.getByText("Wake final result: follow-up complete.", { exact: true }).waitFor();
  await page.waitForFunction(() => document.querySelector("[data-work-group] > button")?.getAttribute("aria-expanded") === "false");
  await chat.locator("[data-work-detail]").first().waitFor({ state: "detached" });
  await page.getByText("Running", { exact: true }).waitFor({ state: "hidden" });
  assert.equal(await slot.getAttribute("data-identity"), "fixed-text-slot");
  await shot("wake-completed");
  await summary.click();
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 6);
  await shot("wake-completed-expanded");
  await page.reload();
  await slot.getByText("Wake final result: follow-up complete.", { exact: true }).waitFor();
  assert.equal(await summary.getAttribute("aria-expanded"), "false");
  await stillThenClick(summary);
  assert.equal(await chat.locator("[data-action-row]:visible").count(), 6);
  console.log(`PASS ${kind} ${width} ${theme}: collapsed running/completed, same text slot, manual trace, recap wake and reload`);
  await context.close();
}
let browser;
try {
  browser = await chromium.launch({ executablePath: executable, args: ["--no-sandbox"] });
  for (const kind of ["main", "ordinary", "agent"]) for (const width of [1280, 390]) for (const theme of ["light", "dark"]) await verify(browser, kind, theme, width);
  console.log("PASS compact-execution browser verification: 12 real-app scenarios, 96 screenshots, no production writes");
} catch (error) {
  console.error("FAIL compact-execution browser verification", error);
  process.exitCode = 1;
} finally {
  await browser?.close();
  clearTimeout(watchdog);
}
