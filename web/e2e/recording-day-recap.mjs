// Synthetic Knowledge detail of an all-day recording and its day recap (#4741), no live hub. Run against an already
// running Vite server.
// BASE_URL=http://127.0.0.1:5797 PERSONA_E2E_CHROMIUM=<chrome> SHOTS_DIR=/tmp/day/shots node web/e2e/recording-day-recap.mjs
import assert from "node:assert/strict";
import { mkdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { chromium } from "playwright-core";

const base = new URL(process.env.BASE_URL || "http://127.0.0.1:5797");
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Use a loopback Vite server");
assert.ok(["http:", "https:"].includes(base.protocol) && !base.username && !base.password && base.pathname === "/" && !base.search && !base.hash, "Use an origin without credentials or a route");
const executablePath = process.env.PERSONA_E2E_CHROMIUM || process.env.CHROME_BIN;
assert.ok(executablePath, "Set PERSONA_E2E_CHROMIUM or CHROME_BIN");
const output = resolve(process.env.SHOTS_DIR || "/tmp/day/shots");
await mkdir(output, { recursive: true });
const id = "dddddddd-dddd-dddd-dddd-dddddddddddd";
const meetingId = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee";
const detailPath = `/kb/recordings/${id}`;
const minutesPath = `/recordings/${id}/minutes`;
// The day's second part ("Parts" in docs/assistant/day-recap.md): sentences the watch sent late, whatever their time.
const part2 = "ffffffff-ffff-ffff-ffff-ffffffffffff";
const part2Path = `/kb/recordings/${part2}`;
const part2Todos = `/recordings/${part2}/minutes/todos`;

// Invented words of an invented day, in English or Chinese. Times are seconds since 08:00 in Shanghai; the browser runs in
// London, so every clock shown must be the recording's own.
const words = {
  en: {
    name: "Day · Nov 11", summary: "Mostly the export release: the size cap stays with a clear message, and the beta waits for the flaky upload test.",
    lines: ["Morning, everyone. Let's start the export design review.", "The export path drops entries over 4 MB.", "Let's keep the cap for this release and add a clear message.", "The first screen should say what the hub is.", "I'll look at the flaky upload test tomorrow.", "The beta goes out on Friday if the upload test is fixed.", "I'll write the release notes tomorrow morning."],
    titles: ["Design review", "Onboarding copy with Cy", "Lunch with the platform team", "Weekly sync", "Planning tomorrow"],
    meetings: ["Export design review", "Weekly sync"],
    actions: ["Look at the flaky upload test.", "Write the release notes."], by: "tomorrow",
    part2: {
      summary: "Late from the watch: a coffee about onboarding and the beta go/no-go.",
      lines: ["The welcome screen needs one sentence, not three.", "We ship the beta on Friday."],
      titles: ["Coffee with Dee", "Beta go/no-go"], action: "Send the beta checklist.",
    },
  },
  zh: {
    name: "全天 · 11月11日", summary: "主要在推进导出功能的发布：大小限制保留并补充明确提示，测试版要等上传测试修好再发。", // i18n-ignore: fixture text
    lines: ["大家早，我们开始导出功能的设计评审。", "超过 4 MB 的条目在导出时会被丢掉。", "这一版先保留限制，补充清楚的提示。", "第一屏应该说明 hub 是什么。", "我明天看一下那个不稳定的上传测试。", "上传测试修好的话，测试版周五发。", "我明天上午写发布说明。"], // i18n-ignore: fixture text
    titles: ["设计评审", "和 Cy 讨论引导文案", "和平台组吃午饭", "每周例会", "规划明天的工作"], // i18n-ignore: fixture text
    meetings: ["导出功能设计评审", "每周例会"], // i18n-ignore: fixture text
    actions: ["看一下不稳定的上传测试。", "写发布说明。"], by: "明天", // i18n-ignore: fixture text
    part2: {
      summary: "手表晚到的内容：和 Dee 喝咖啡聊引导页，以及测试版是否发布。", // i18n-ignore: fixture text
      lines: ["欢迎页一句话就够，不要三句。", "测试版周五发。"], // i18n-ignore: fixture text
      titles: ["和 Dee 喝咖啡", "测试版发布决定"], action: "发出测试版检查清单。", // i18n-ignore: fixture text
    },
  },
};
const fixture = (lang) => {
  const w = lang === "en" ? words.en : words.zh;
  const at = [3630, 3912, 4840, 10325, 16270, 25440, 36750];
  return {
    kind: "recordings", id, path: `recordings/${id}`,
    meta: {
      title: w.name, kind: "day", day: "2026-11-11", tz: "Asia/Shanghai", part: 1, started_at: "2026-11-11T08:00:00+08:00", duration: 31200, device: "watch",
      language: lang === "en" ? "en-US" : "zh-CN", revision: 9, base: 1, next: 7, complete: false, recap_state: "ready",
      coverage: { to: 39600, gaps: [{ from: 8400, to: 9900, reason: "interrupted" }, { from: 20400, to: 23400, reason: "paused" }] },
    },
    summary: w.summary,
    segments: at.map((start, i) => ({ start, end: start + 8, text: w.lines[i] })),
    recap: {
      version: 3, revision: 9, generated_at: "2026-11-11T11:00:41Z", day: "2026-11-11", covers: { from: 0, to: 39600 }, final: false,
      summary: w.summary,
      timeline: [
        { from: 3600, to: 6900, kind: "meeting", title: w.titles[0], meeting: { kind: "recordings", id: meetingId, name: w.meetings[0] }, points: [{ text: w.lines[1], at: 3912, who: "Ada" }, { text: w.lines[2], at: 4840, who: "Bo" }] },
        { from: 10200, to: 12600, kind: "outside", title: w.titles[1], points: [{ text: w.lines[3], at: 10325 }] },
        { from: 16200, to: 18600, kind: "outside", title: w.titles[2], points: [{ text: w.lines[4], at: 16270, who: "Bo" }] },
        { from: 25200, to: 27900, kind: "meeting", title: w.titles[3], meeting: { kind: "events", id: "example-weekly-sync", name: w.meetings[1] }, points: [{ text: w.lines[5], at: 25440 }] },
        { from: 36600, to: 37800, kind: "outside", title: w.titles[4], points: [{ text: w.lines[6], at: 36750 }] },
      ],
      actions: [
        { id: "a1", who: "Bo", what: w.actions[0], by: w.by, due_date: "2026-11-12", at: 16270 },
        { id: "a2", who: "Ada", what: w.actions[1], at: 36750, todo_id: "example-todo" },
      ],
      gaps: [{ from: 8400, to: 9900, reason: "interrupted" }, { from: 20400, to: 23400, reason: "paused" }],
      partial: false,
      markdown: "# 2026-11-11\n\n## Meetings\n\n- 09:00–09:55 Design review",
    },
  };
};
// Part 2 of the same day: the same day, zone and started_at, its own recap (version 2) and an action id part 1 also uses.
const second = (lang) => {
  const w = (lang === "en" ? words.en : words.zh).part2;
  const base = fixture(lang);
  return {
    kind: "recordings", id: part2, path: `recordings/${part2}`,
    meta: { ...base.meta, part: 2, duration: 600, revision: 4, base: 2, next: 2 },
    summary: w.summary,
    segments: [{ start: 7840, end: 7848, text: w.lines[0] }, { start: 30720, end: 30728, text: w.lines[1] }],
    recap: {
      ...base.recap, version: 2, revision: 4, summary: w.summary,
      timeline: [
        { from: 7800, to: 8300, kind: "outside", title: w.titles[0], points: [{ text: w.lines[0], at: 7840, who: "Dee" }] },
        { from: 30600, to: 32400, kind: "meeting", title: w.titles[1], points: [{ text: w.lines[1], at: 30720 }] },
      ],
      actions: [{ id: "a1", who: "Dee", what: w.action, at: 30720 }],
      markdown: "# 2026-11-11 (2)",
    },
  };
};
const browser = await chromium.launch({ executablePath, headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const deadline = setTimeout(() => { console.error("FAIL recording day recap timed out"); process.exit(1); }, 300_000);
let screenshots = 0, runs = 0;

async function run(theme, lang, width, shoot, twoParts = false) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const context = await browser.newContext({ viewport: { width, height: width < 600 ? 2400 : 2000 }, colorScheme: theme, timezoneId: "Europe/London", deviceScaleFactor: width < 600 ? 2 : 1, reducedMotion: "reduce" });
  await context.addInitScript(({ theme, lang }) => {
    localStorage.setItem("persona.token", "fixture-token");
    localStorage.setItem("persona.language", lang);
    localStorage.setItem("persona.theme", theme);
  }, { theme, lang });
  const detail = fixture(lang);
  const detail2 = second(lang);
  const writes = [], errors = [];
  // The parts the hub lists: a single-part day lists only itself.
  const listed = [detail, ...(twoParts ? [detail2] : [])].map((d) => ({ id: d.id, revision: d.meta.revision, complete: false, started_at: d.meta.started_at, kind: "day", day: d.meta.day, part: d.meta.part, next: d.meta.next, base: d.meta.base }));
  await context.route("**/*", (route) => new URL(route.request().url()).origin === base.origin ? route.continue() : route.abort());
  await context.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "");
    if (path === detailPath) return route.fulfill({ json: { detail } });
    if (twoParts && path === part2Path) return route.fulfill({ json: { detail: detail2 } });
    if (twoParts && path === part2Todos) {
      assert.equal(request.method(), "POST");
      assert.equal(request.headers()["x-persona-manager"], "assistant");
      const body = request.postDataJSON(); writes.push({ path, body });
      assert.deepEqual(body, { version: 2, actions: ["a1"] });
      detail2.recap.actions[0].todo_id = "example-part2-todo";
      return route.fulfill({ json: { todos: [{ id: "example-part2-todo" }], recap: detail2.recap } });
    }
    if (path === minutesPath || path === `${minutesPath}/todos`) {
      assert.equal(request.method(), "POST");
      assert.equal(request.headers()["x-persona-manager"], "assistant");
      if (path.endsWith("/todos")) {
        const body = request.postDataJSON(); writes.push({ path, body });
        assert.deepEqual(body, { version: 3, actions: ["a1"] });
        detail.recap.actions[0].todo_id = "example-added-todo";
        return route.fulfill({ json: { todos: [{ id: "example-added-todo" }], recap: detail.recap } });
      }
      assert.equal(request.postData(), null);
      writes.push({ path }); detail.meta.recap_state = "pending";
      return route.fulfill({ status: 202, json: { state: "pending" } });
    }
    assert.ok(["GET", "HEAD"].includes(request.method()), `Unexpected write ${path}`);
    const data = {
      "/kb/recordings": { items: [{ kind: "recordings", id, name: detail.meta.title, relevance: 0 }], tags: [] },
      "/recordings": { days: 14, recordings: listed, deleted: [] },
      "/sessions": { sessions: [], total: 0, has_more: false, revision: "fixture" },
      "/machines": { machines: [] }, "/interactions": { interactions: [] },
      "/assistant": { conversations: [] }, "/todos": { todos: [] },
      "/mutes": { spaces: [], sessions: [], allowed: [] }, "/pins": { spaces: [], sessions: [], sections: [] },
      "/agents/sessions": { sessions: [] }, "/agents/prefs": {},
      "/managers": { managers: [{ id: "assistant", name: "Persona", builtin: true, online: true }], default: "assistant" },
      "/personas": { personas: [] }, "/assistant/personas": { personas: [] }, "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
    };
    return route.fulfill({ json: data[path] ?? {} });
  });
  await context.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (ws) => {
    ws.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: ["managers", "recording_day"], hub_name: "Example hub" }));
    ws.send(JSON.stringify({ type: "machines", machines: [] }));
    ws.onMessage((message) => { const event = JSON.parse(String(message)); if (event.type === "app_ping") ws.send(JSON.stringify({ type: "pong", id: event.id })); });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const recap = page.getByRole("region", { name: s.recap, exact: true });
  await recap.waitFor();
  await recap.getByRole("heading", { name: s.recap_evening, exact: true }).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(350);
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert.equal(await page.locator("[data-recording-minutes]").count(), 0, "A day recording offers no minutes");
  if (twoParts) return runTwoParts({ page, context, recap, s, lang, theme, width, detail, detail2, writes, errors });
  assert.deepEqual(await recap.locator("h3").allTextContents(), [s.recap_morning, s.recap_afternoon, s.recap_evening, s.recap_actions]);
  assert.equal(await recap.locator('[data-recap-entry="meeting"]').count(), 2);
  assert.equal(await recap.locator('[data-recap-entry="meeting"] a').first().getAttribute("href"), `/kb/recordings/${meetingId}`);
  // The recording's clock (Shanghai), not the browser's (London).
  assert.equal((await recap.locator('[aria-controls="recording-line-3912"]').first().textContent()).trim(), "09:05:12");
  assert.equal((await recap.locator("[data-recap-covers]").textContent()).trim(), s.recap_so_far.replace("{time}", "19:00"));
  assert.ok((await recap.locator("[data-recap-gaps]").textContent()).includes("10:20"));
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No page overflow");
  assert.ok(await recap.evaluate((section) => section.scrollWidth <= section.clientWidth + 1), "No recap overflow");
  assert.ok(await recap.locator("button").evaluateAll((buttons) => buttons.every((button) => {
    const range = document.createRange(); range.selectNodeContents(button);
    return getComputedStyle(button).whiteSpace === "nowrap" && range.getBoundingClientRect().width <= button.clientWidth + 1;
  })), "All button text fits on one line");
  if (shoot) {
    // The detail scrolls inside its own pane: bring the recap to the top of it, so the shot holds the facts and the recap.
    await page.locator("[data-recording-recap]").evaluate((el) => el.closest(".overflow-y-auto")?.scrollTo({ top: 0 }));
    await page.screenshot({ path: join(output, `day-recap-${lang}-${theme}-${width}.png`), fullPage: true });
    screenshots++;
  }

  // Exercise the real app: timestamp jump, confirm a suggested to-do, then ask for an update.
  const timestamp = recap.locator('[aria-controls="recording-line-16270"]').first();
  await timestamp.click();
  await page.waitForFunction(() => document.activeElement?.id === "recording-line-16270");
  const line = page.locator('[id="recording-line-16270"]');
  assert.ok(await line.evaluate((element) => element.classList.contains("bg-accent-tint")));
  assert.ok(await line.evaluate((element) => { const rect = element.getBoundingClientRect(); return rect.top >= 0 && rect.bottom <= innerHeight; }), "Jump reveals the transcript line");
  await recap.getByRole("button", { name: s.minutes_add_todo, exact: true }).click();
  await recap.getByRole("button", { name: s.minutes_add_todo, exact: true }).waitFor({ state: "detached" });
  assert.equal(writes.filter((write) => write.path.endsWith("/todos")).length, 1);
  await recap.getByRole("button", { name: s.recap_update, exact: true }).click();
  await recap.getByText(s.recap_updating, { exact: true }).waitFor();
  assert.equal(writes.filter((write) => write.path === minutesPath).length, 1);
  if (shoot && width > 600) {
    // The older recap stays while the update is written, under a spinner.
    await recap.evaluate((el) => el.scrollIntoView({ block: "start" }));
    await recap.screenshot({ path: join(output, `day-recap-${lang}-${theme}-${width}-updating.png`) });
    screenshots++;
  }
  assert.deepEqual(errors, []);
  await context.close();
  runs++;
}

// A day in two parts, opened at part 1: one timeline and one list of suggested to-dos by time, part 2's times linking
// to part 2's page, each part saying how far its recap is, and a part 2 to-do confirmed on part 2 with its version.
async function runTwoParts({ page, context, recap, s, lang, theme, width, detail, detail2, writes, errors }) {
  const fill = (text, vars) => Object.entries(vars).reduce((out, [k, v]) => out.replace(`{${k}}`, String(v)), text);
  await recap.getByRole("heading", { name: detail2.recap.timeline[0].title, exact: true }).waitFor();
  const titles = [...detail.recap.timeline, ...detail2.recap.timeline].sort((a, b) => a.from - b.from).map((e) => e.title);
  assert.deepEqual(await recap.locator("h4").allTextContents(), titles);
  assert.equal((await recap.locator("[data-recap-covers]").textContent()).trim(),
    `${fill(s.recap_part_so_far, { part: 1, time: "19:00" })} · ${fill(s.recap_part_so_far, { part: 2, time: "19:00" })}`);
  // The gaps both parts name, once.
  assert.equal((await recap.locator("[data-recap-gaps]").textContent()).split("10:20").length, 2);
  const link = recap.getByRole("link", { name: fill(s.recap_part_jump, { time: "10:10:40", part: 2 }), exact: true });
  assert.equal(await link.getAttribute("href"), part2Path);
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No page overflow");
  assert.ok(await recap.evaluate((section) => section.scrollWidth <= section.clientWidth + 1), "No recap overflow");
  await page.locator("[data-recording-recap]").evaluate((el) => el.closest(".overflow-y-auto")?.scrollTo({ top: 0 }));
  await page.screenshot({ path: join(output, `day-recap-parts-${lang}-${theme}-${width}.png`), fullPage: true });
  screenshots++;

  // Part 2's suggested to-do goes to part 2 with part 2's version, and part 1's stays to confirm.
  const todo = recap.locator("li").filter({ hasText: detail2.recap.actions[0].what });
  await todo.getByRole("button", { name: s.minutes_add_todo, exact: true }).click();
  await todo.getByText(s.minutes_added, { exact: true }).waitFor();
  assert.deepEqual(writes.map((write) => write.path), [part2Todos]);
  await recap.locator("li").filter({ hasText: detail.recap.actions[0].what }).getByRole("button", { name: s.minutes_add_todo, exact: true }).waitFor();
  // Part 2's time opens part 2, which lists part 1 the same way.
  await link.click();
  await page.waitForURL((url) => url.pathname === part2Path);
  await recap.getByRole("link", { name: fill(s.recap_part_jump, { time: "09:05:12", part: 1 }), exact: true }).first().waitFor();
  assert.deepEqual(errors, []);
  await context.close();
  runs++;
}

try {
  for (const lang of ["en", "zh-Hans"]) {
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) await run(theme, lang, width, true);
    }
  }
  for (const lang of ["en", "zh-Hans"]) {
    for (const [theme, width] of [["light", 1440], ["dark", 390]]) await run(theme, lang, width, true, true);
  }
  await run("dark", "ja", 390, false);
  console.log(`PASS recording day recap: ${runs} browser runs, ${screenshots} screenshots; zone clock, timestamps, mutations, two-part day, overflow and single-line buttons checked`);
} finally {
  clearTimeout(deadline);
  await browser.close();
}
