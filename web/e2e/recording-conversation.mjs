// A recorded meeting's conversation (#3862), with synthetic fixtures and no live hub: on a hub that lists
// meeting_conversation, the private switch, a running room meeting's one thread (read-only, the room's state, batches
// folded), questions asked in the room that got no answer (why, or still being answered; one that never went in after the
// thread), an ended one's follow-up composer that opens the follow-up, the thread after the divider, and a room that is
// unavailable showing its asks. With meeting_photos (phase 3): the photos the room was sent in its thread (one deleted
// since: "Photo removed", never the room's copy), **Photos**, a photos revision read again from GET …/photos only, the
// minutes items that cite a photo and the image viewer. Run against an already running Vite server.
// BASE_URL=http://127.0.0.1:5791 PERSONA_E2E_CHROMIUM=<chrome> SHOTS_DIR=/tmp/mm/shots node web/e2e/recording-conversation.mjs
import assert from "node:assert/strict";
import { mkdir, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { crc32, deflateSync } from "node:zlib";
import { chromium } from "playwright-core";

const base = new URL(process.env.BASE_URL || "http://127.0.0.1:5791");
assert.ok(["127.0.0.1", "localhost", "[::1]"].includes(base.hostname), "Use a loopback Vite server");
assert.ok(["http:", "https:"].includes(base.protocol) && !base.username && !base.password && base.pathname === "/" && !base.search && !base.hash, "Use an origin without credentials or a route");
const executablePath = process.env.PERSONA_E2E_CHROMIUM || process.env.CHROME_BIN;
assert.ok(executablePath, "Set PERSONA_E2E_CHROMIUM or CHROME_BIN");
const output = resolve(process.env.SHOTS_DIR || "/tmp/mm/shots");
await mkdir(output, { recursive: true });
const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const room = "dddddddd-0000-4000-8000-000000000001";
const follow = "eeeeeeee-0000-4000-8000-000000000002";
const typedAsk = "11111111-1111-4111-8111-111111111111";
const heardAsk = "22222222-2222-4222-8222-222222222222";
const detailPath = `/kb/recordings/${id}`;
const livePath = `/recordings/${id}/live`;
const MACHINE = { id: "server", name: "Hub", online: true, os: "linux" };
const ready = { state: "ready", machine_id: "server", session_id: room };
const ended = { state: "ended", machine_id: "server", session_id: room };
const unavailable = { state: "unavailable", problem: "machine_offline" };
const followup = { machine_id: "server", session_id: follow };
const FEATURES = ["managers", "meeting_attendance", "meeting_conversation", "meeting_photos"];

// Synthetic photos: made-up pictures drawn here as PNGs (a whiteboard, a slide), no real content.
function png(width, height, paint) {
  const rows = Buffer.alloc((width * 3 + 1) * height);
  for (let y = 0; y < height; y++) {
    rows[y * (width * 3 + 1)] = 0;
    for (let x = 0; x < width; x++) rows.set(paint(x, y), y * (width * 3 + 1) + 1 + x * 3);
  }
  const chunk = (type, data) => {
    const head = Buffer.alloc(8); head.writeUInt32BE(data.length, 0); head.write(type, 4, "ascii");
    const sum = Buffer.alloc(4); sum.writeUInt32BE(crc32(Buffer.concat([head.subarray(4), data])), 0);
    return Buffer.concat([head, data, sum]);
  };
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(width, 0); ihdr.writeUInt32BE(height, 4); ihdr.set([8, 2, 0, 0, 0], 8);
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(rows)), chunk("IEND", Buffer.alloc(0))]);
}
const inBox = (x, y, [x0, y0, x1, y1]) => x >= x0 && x < x1 && y >= y0 && y < y1;
const whiteboard = png(320, 240, (x, y) => {
  if (x < 6 || y < 6 || x >= 314 || y >= 234) return [150, 154, 160];
  if ([[24, 24, 170, 34], [24, 52, 230, 58], [24, 70, 200, 76], [24, 88, 250, 94], [24, 106, 180, 112]].some((b) => inBox(x, y, b))) return [36, 60, 140];
  if (inBox(x, y, [24, 122, 210, 126])) return [196, 52, 52];
  if ((x === 40 || x === 150 || x === 260 || y === 150 || y === 180 || y === 210) && inBox(x, y, [40, 150, 261, 211])) return [60, 64, 70];
  return [246, 246, 242];
});
const slide = png(240, 320, (x, y) => {
  if (y < 64) return [40, 96, 200];
  if (inBox(x, y, [20, 24, 170, 40])) return [240, 244, 252];
  if ([[24, 96, 32, 104], [24, 136, 32, 144], [24, 176, 32, 184]].some((b) => inBox(x, y, b))) return [40, 96, 200];
  if ([[44, 96, 200, 104], [44, 136, 180, 144], [44, 176, 210, 184], [24, 240, 216, 290]].some((b) => inBox(x, y, b))) return [200, 204, 212];
  return [252, 252, 253];
});
const photoOne = "55555555-5555-4555-8555-555555555555";
const photoGone = "66666666-6666-4666-8666-666666666666";
const photoThree = "77777777-7777-4777-8777-777777777777";
const photo = (n, over) => ({ n, at: 1795, mime: "image/png", width: 320, height: 240, cited: false, forwarded: true, added_at: "2026-11-10T06:29:55Z", ...over });
const photosList = (cited = false) => [
  photo(1, { id: photoOne, caption: "Whiteboard", text: "Q3 budget\nTravel 4k → 2k\nExport cap 4 MB (keep)", note: "The whiteboard lists the Q3 budget: travel cut from 4k to 2k, and the 4 MB export cap kept for this release.", cited }),
  photo(3, { id: photoThree, at: 1830, width: 240, height: 320, caption: "Slide: rollout plan", added_at: "2026-11-10T06:30:30Z" }),
];
const IMAGES = { 1: whiteboard, 3: slide };

const meta = (over) => ({
  title: "Design review", kind: "meeting", complete: false, started_at: "2026-11-10T14:00:00+08:00", duration: 1860, device: "phone", language: "en",
  revision: 5, minutes_state: "pending", attendance: "participant", updated: new Date().toISOString(), room: ready, ...over,
});
const detail = (over = {}) => ({
  kind: "recordings", id, path: `recordings/${id}`, meta: meta(over), summary: "",
  segments: [
    { start: 5, end: 9, text: "Ada: Today we look at the export flow of the example project." },
    { start: 1790, end: 1806, text: "Bo: Did we decide on the cap last time, or is it still open?" },
  ],
});
const ask = (over) => ({
  id: typedAsk, question: "What did we decide about the export cap last time?", question_source: "typed", at: 1834.2, state: "answered",
  answer: "At the design review on November 3 you kept the 4 MB cap for this release. Bo owns the regression test; it is still open.",
  lang: "en", speak: true, sources: [], asked_at: "2026-11-10T06:30:34Z", answered_at: "2026-11-10T06:30:40Z", via: "room", ...over,
});
const asks = () => [ask(), ask({ id: heardAsk, question: "", question_source: "heard", at: 1810, answer: "Bo owns it, and it is still open.", asked_at: "2026-11-10T06:30:10Z" })];
const live = (over = {}) => ({ next: 57, fed: 57, attendance: "participant", asks: asks(), room: ready, revs: { room: 2, asks: 4 }, ...over });

let seq = 0;
const at = (minute) => `2026-11-10T06:${String(minute).padStart(2, "0")}:00Z`;
const item = (minute, over) => ({ id: `i${++seq}#0`, seq, role: "user", kind: "text", ts: at(minute), ...over });
const source = '<persona-context kind="message-source" title="">\n{"kind":"meeting","title":"Design review"}\n</persona-context>\n\n';
const batch = (minute, lines) => item(minute, {
  source: { kind: "meeting", title: "Design review" },
  text: `${source}New lines from "Design review", a meeting you attend as a participant. Write nothing in reply.\n\n<persona-context kind="context" title="Design review · live" url="">\n${lines.join("\n")}\n</persona-context>`,
});
const roomItems = () => [
  item(0, { client_msg_id: `meeting-${id}`, text: `${source}The meeting is starting. Write nothing now.\n\n<persona-context kind="context" title="Design review" url="">\nTitle: Design review\nStarted: 2026-11-10 14:00 CST\nLanguage: en\nRole: participant: answers may be said aloud\n</persona-context>` }),
  batch(1, ["[0:05] Ada: Today we look at the export flow of the example project.", "[0:12] Bo: The cap is still 4 MB.", "[0:31] Ada: Let's keep it for this release."]),
  batch(29, ["[29:40] Bo: Did we decide on the cap last time, or is it still open?"]),
  item(30, { client_msg_id: `ask-${heardAsk}-1`, text: `${source}Someone in the room just asked a question.\n\n<persona-context kind="context" title="What was just said (30:10)" url="">\n[29:40] Bo: Did we decide on the cap last time, or is it still open?\n</persona-context>` }),
  item(30, { role: "assistant", text: "Bo owns it, and it is still open." }),
  item(30, { client_msg_id: `ask-${typedAsk}-1`, text: "What did we decide about the export cap last time?" }),
  item(30, { role: "assistant", text: ask().answer }),
];
const forwarded = (minute, photoID, n, caption) => [
  item(minute, { client_msg_id: `photo-${photoID}`, kind: "image", image: { mime: "image/png", lazy: true }, source: { kind: "meeting", title: "Design review" } }),
  item(minute, {
    client_msg_id: `photo-${photoID}`, source: { kind: "meeting", title: "Design review" },
    text: `${source}Photo ${n} was taken in the meeting. Say what in it matters for the meeting with meeting_photo_note, then write nothing in reply.\n\n<persona-context kind="context" title="Photo ${n}" url="">\nCaption: ${caption}\n</persona-context>`,
  }),
];
/** The room with photos: one noted, one deleted since (withdrawn in the room), one just sent. */
const photoRoomItems = () => [
  ...roomItems().slice(0, 3),
  ...forwarded(29, photoOne, 1, "Whiteboard"),
  item(29, { role: "assistant", kind: "tool_use", tool_name: "mcp__persona__meeting_photo_note", tool_input: { photo: 1, text: "The whiteboard lists the Q3 budget." } }),
  ...forwarded(29, photoGone, 2, "Hallway"),
  item(30, { client_msg_id: `photo-withdrawn-${photoGone}`, source: { kind: "meeting", title: "Design review" }, text: `${source}Photo 2 was withdrawn: do not describe or use it again. Write nothing in reply.` }),
  ...roomItems().slice(3),
  ...forwarded(30, photoThree, 3, "Slide: rollout plan"),
];
const minutes = () => ({
  version: 2, revision: 3, generated_at: "2026-11-10T06:40:00Z", title: "Design review", when: "2026-11-10 14:00–14:31", attendees: ["Ada", "Bo"],
  points: [{ text: "Q3 budget: travel cut from 4k to 2k", at: 1795, photo: 1 }, { text: "The export flow of the example project", at: 5, who: "Ada" }],
  decisions: [{ text: "Keep the 4 MB export cap for this release", at: 1795, photo: 1 }, { text: "Move the hallway poster", at: 1801, photo: 2 }],
  actions: [{ id: "a1", who: "Bo", what: "Send the updated budget", at: 1795, photo: 1 }, { id: "a2", who: "Bo", what: "Add the regression test", at: 1790 }],
  open: [{ text: "Is the cap still open?", at: 1790 }],
  markdown: "# Design review\n\n- Q3 budget: travel cut from 4k to 2k（照片 1）",
});
const followItems = () => [
  item(40, { client_msg_id: "followup-1", text: '<persona-context kind="context" title="Meeting: Design review" url="">\nThis is the material of a meeting I recorded.\nMinutes:\n- Keep the 4 MB cap for this release.\n- Bo owns the regression test.\n</persona-context>\n\nDraft the email to the vendor about the cap.' }),
  item(41, { role: "assistant", text: "Here is a **draft**:\n\n> We are keeping the 4 MB export cap for this release and will revisit it next quarter." }),
];

const browser = await chromium.launch({ executablePath, headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const deadline = setTimeout(() => { console.error("FAIL recording conversation timed out"); process.exit(1); }, 240_000);
let screenshots = 0, runs = 0;
const fill = (text, values) => text.replace(/\{(\w+)\}/g, (_, k) => String(values[k]));

async function context(theme, lang, width, scene) {
  const ctx = await browser.newContext({ viewport: { width, height: 1400 }, colorScheme: theme, timezoneId: "Asia/Shanghai", deviceScaleFactor: 1, reducedMotion: "reduce" });
  await ctx.addInitScript(({ theme, lang }) => {
    localStorage.setItem("persona.token", "fixture-token");
    localStorage.setItem("persona.language", lang);
    localStorage.setItem("persona.theme", theme);
  }, { theme, lang });
  const state = { live: [], private: [], followup: [], items: [], made: false, asksRev: 0, photos: [], images: [], roomImages: [], details: 0, photosRev: 0 };
  await ctx.route("**/*", (route) => new URL(route.request().url()).origin === base.origin ? route.continue() : route.abort());
  await ctx.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace(/^\/api\/v1/, "");
    if (path === detailPath) {
      state.details++;
      return route.fulfill({ json: { detail: { ...detail({ ...scene.meta, ...(state.made ? { followup } : {}) }), ...(scene.minutes ? { minutes: scene.minutes() } : {}) } } });
    }
    if (path === `/recordings/${id}/photos`) {
      assert.equal(request.method(), "GET");
      assert.equal(request.headers()["x-persona-manager"], "assistant");
      assert.equal(request.headers().authorization, "Bearer fixture-token");
      state.photos.push(url.search);
      return route.fulfill({ json: { photos: (scene.photos ?? (() => []))(state) } });
    }
    const image = /^\/recordings\/[^/]+\/photos\/(\d+)$/.exec(path);
    if (image) {
      // An <img> can't send these: the page reads the bytes itself.
      assert.equal(request.headers()["x-persona-manager"], "assistant");
      assert.equal(request.headers().authorization, "Bearer fixture-token");
      state.images.push(Number(image[1]));
      const body = IMAGES[image[1]];
      return body ? route.fulfill({ body, contentType: "image/png", headers: { "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff" } })
        : route.fulfill({ status: 404, json: { error: "no such photo", code: "photo_not_found" } });
    }
    if (/\/items\/[^/]+\/image$/.test(path)) state.roomImages.push(path);
    if (path === livePath) {
      assert.equal(request.method(), "GET");
      assert.equal(request.headers()["x-persona-manager"], "assistant");
      state.live.push(url.search);
      const revs = { ...(scene.live.revs ?? live().revs), ...(state.asksRev ? { asks: state.asksRev } : {}), ...(state.made ? { followup: 1 } : {}), ...(state.photosRev ? { photos: state.photosRev } : {}) };
      return route.fulfill({ json: live({ ...scene.live, ...(state.made ? { followup } : {}), revs }) });
    }
    if (path === `/recordings/${id}/private`) {
      assert.equal(request.method(), "PUT");
      const body = request.postDataJSON(); state.private.push(body);
      return route.fulfill({ json: { private: body.private } });
    }
    if (path === `/recordings/${id}/followup`) {
      assert.equal(request.method(), "POST");
      const body = request.postDataJSON(); state.followup.push(body);
      state.made = true;
      return route.fulfill({ json: { followup, created: true, client_msg_id: `followup-${body.id}` } });
    }
    if (path === `/sessions/server/${room}/items` || path === `/sessions/server/${follow}/items`) {
      assert.equal(request.method(), "GET");
      // The detail's own reads; the chat page the follow-up opens reads it its own way.
      if (url.searchParams.get("turns") === "400") state.items.push(path);
      return route.fulfill({ json: { items: path.includes(room) ? (scene.roomItems ?? roomItems)() : followItems(), has_more: false } });
    }
    assert.ok(["GET", "HEAD"].includes(request.method()), `Unexpected write ${request.method()} ${path}`);
    const assistant = { manager: "assistant", machine_id: "server", main: "main-1", conversations: [], meeting_attendance_default: "participant", notes_effective: "", main_tasks_quiet: true };
    const data = {
      "/kb/recordings": { items: [{ kind: "recordings", id, name: "Design review", relevance: 0 }], tags: [] },
      "/sessions": { sessions: [], total: 0, has_more: false, revision: "fixture" },
      "/machines": { machines: [MACHINE] }, "/interactions": { interactions: [] },
      "/assistant": assistant, "/assistant/tasks": { tasks: [] }, "/assistant/memory": { files: [] }, "/todos": { todos: [] },
      "/mutes": { spaces: [], sessions: [], allowed: [] }, "/pins": { spaces: [], sessions: [], sections: [] },
      "/agents/sessions": { sessions: [] }, "/agents/prefs": {}, "/skills": { skills: [] },
      "/managers": { managers: [{ id: "assistant", name: "Persona", builtin: true, online: true }], default: "assistant" },
      "/personas": { personas: [] }, "/assistant/personas": { personas: [] }, "/workspaces": { workspaces: [] }, "/extensions": { extensions: [] },
    };
    return route.fulfill({ json: data[path] ?? {} });
  });
  const sockets = [];
  await ctx.routeWebSocket(/\/api\/v1\/ws(?:\?|$)/, (ws) => {
    sockets.push(ws);
    ws.send(JSON.stringify({ type: "hello", server_version: "mock", protocol: 1, features: scene.features ?? FEATURES, hub_name: "Example hub" }));
    ws.send(JSON.stringify({ type: "machines", machines: [MACHINE] }));
    ws.onMessage((message) => { const event = JSON.parse(String(message)); if (event.type === "app_ping") ws.send(JSON.stringify({ type: "pong", id: event.id })); });
  });
  return { ctx, state, push: (msg) => sockets.forEach((ws) => ws.send(JSON.stringify(msg))) };
}

async function settle(page) {
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(350);
  assert.equal(await page.locator("vite-error-overlay").count(), 0);
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "No page overflow");
}

async function until(check, what, ms = 6000) {
  const started = Date.now();
  while (!(await check())) {
    assert.ok(Date.now() - started < ms, `Timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

/** The private switch and the Conversation section from the top of the facts down, as one picture. */
async function shoot(page, name) {
  const section = page.locator("[data-meeting-conversation]");
  assert.ok(await section.evaluate((el) => el.scrollWidth <= el.clientWidth + 1), "No section overflow");
  await page.screenshot({ path: join(output, name), fullPage: true });
  screenshots++;
}

const kinds = (page) => page.locator("[data-meeting-conversation] li[data-thread]").evaluateAll((els) => els.map((el) => el.getAttribute("data-thread")));

async function running(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const { ctx, state, push } = await context(theme, lang, width, { meta: {}, live: {} });
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  await section.getByText(ask().answer, { exact: true }).waitFor();
  await settle(page);
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "heard", "answer", "question", "answer"]);
  assert.equal(await section.getByRole("status").innerText(), s.room_ready_participant);
  assert.ok(await section.getByText(fill(s.thread_lines_other, { count: 3 }), { exact: true }).isVisible());
  assert.ok(await section.getByText(fill(s.thread_heard, { clock: "30:10" }), { exact: true }).isVisible());
  assert.ok(await section.getByText(s.conversation_read_only, { exact: true }).isVisible());
  assert.equal(await section.locator("textarea").count(), 0);
  assert.equal(await page.locator("[data-recording-asks]").count(), 0, "A room meeting's asks are in its thread");
  assert.equal(await page.getByText(s.recording_attending, { exact: true }).count(), 0, "No attending session row");
  // The private switch: no mark yet reads as private.
  const box = page.locator("[data-meeting-private]");
  const toggle = box.getByRole("switch");
  assert.equal(await toggle.getAttribute("aria-checked"), "true");
  assert.ok(await box.getByText(s.private_unset, { exact: true }).isVisible());
  // The first batch opens onto its lines.
  await section.locator('li[data-thread="lines"] button').first().click();
  await section.getByText("Bo: The cap is still 4 MB.", { exact: true }).waitFor();
  await settle(page);
  await shoot(page, width === 1440 ? `conversation-running-${theme}.png` : `conversation-running-${lang}-${width}.png`);
  if (runs === 0) {
    await toggle.click();
    await until(async () => state.private.length === 1, "the private mark's PUT");
    assert.deepEqual(state.private, [{ private: false }]);
    await box.getByText(s.private_off, { exact: true }).waitFor();
    assert.equal(await toggle.getAttribute("aria-checked"), "false");
    // A meeting_live event reads only the newer parts, then the room's thread again.
    const reads = state.live.length, threads = state.items.length;
    state.asksRev = 5;
    push({ type: "meeting_live", recording_id: id, changed: ["room", "asks"], revs: { room: 2, asks: 5 } });
    await until(async () => state.live.length > reads && state.items.length > threads, "the newer part and the thread");
    assert.equal(state.live[reads], "?parts=asks");
  }
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

async function after(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const scene = { meta: { complete: true, ended_at: "2026-11-10T14:31:00+08:00", minutes_state: "ready", room: ended, private: false }, live: { room: ended, private: false, revs: { room: 3, asks: 4, private: 1 } } };
  const { ctx, state } = await context(theme, lang, width, scene);
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  const field = section.locator("textarea");
  await field.waitFor();
  await section.getByText(ask().answer, { exact: true }).waitFor();
  await settle(page);
  assert.equal(await section.getByRole("status").innerText(), s.room_ended);
  assert.ok(await page.locator("[data-meeting-private]").getByText(s.private_off, { exact: true }).isVisible());
  await field.fill("Draft the email to the vendor about the cap.");
  await shoot(page, width === 1440 ? `conversation-composer-${theme}.png` : `conversation-composer-${lang}-${width}.png`);
  await section.getByRole("button", { name: s.followup_send, exact: true }).click();
  await page.waitForURL((u) => u.pathname === `/persona/${follow}`);
  assert.equal(state.followup.length, 1);
  assert.match(state.followup[0].id, /^[0-9a-f-]{36}$/);
  assert.equal(state.followup[0].text, "Draft the email to the vendor about the cap.");
  // Back on the recording, the thread goes on after the divider. (The chat page in between is fed nothing real.)
  assert.deepEqual(errors, []);
  await page.goto(new URL(detailPath, base).href);
  errors.length = 0;
  await section.getByRole("separator").waitFor();
  await section.getByText("draft", { exact: true }).waitFor();
  await settle(page);
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "heard", "answer", "question", "answer", "material", "question", "answer"]);
  const seed = section.locator('li[data-thread="material"] button');
  assert.equal(await seed.innerText(), s.thread_material);
  assert.equal(await seed.getAttribute("aria-expanded"), "false");
  assert.equal(await section.getByRole("link", { name: s.conversation_continue, exact: true }).getAttribute("href"), `/persona/${follow}`);
  assert.equal(await field.count(), 0);
  await section.evaluate((el) => el.scrollIntoView({ block: "end" }));
  await shoot(page, width === 1440 ? `conversation-followup-${theme}.png` : `conversation-followup-${lang}-${width}.png`);
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

async function down(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const scene = { meta: { room: unavailable }, live: { room: unavailable, asks: [ask({ via: "material" })] } };
  const { ctx, state } = await context(theme, lang, width, scene);
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  await section.locator("li[data-ask]").first().waitFor();
  await settle(page);
  assert.equal(await section.getByRole("status").innerText(), s.room_problem_machine_offline);
  assert.equal(await section.locator("li[data-ask]").count(), 1);
  assert.deepEqual(state.items, [], "A room that never came up has no thread to read");
  await shoot(page, width === 1440 ? `conversation-unavailable-${theme}.png` : `conversation-unavailable-${lang}-${width}.png`);
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

/** Questions in the room that got no answer: one in the thread says why, one still being answered says so, and one that
 *  never went into the room (spoken, failed) shows after the thread. */
async function stuck(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const spokenAsk = "33333333-3333-4333-8333-333333333333";
  const waitingAsk = "44444444-4444-4444-8444-444444444444";
  const stuckAsks = [
    ask({ id: heardAsk, question: "", question_source: "heard", at: 1810, answer: "Bo owns it, and it is still open.", asked_at: "2026-11-10T06:30:10Z" }),
    ask({ state: "failed", error: "no_answer", answer: "", answered_at: undefined }),
    ask({ id: waitingAsk, question: "And when is the next review?", state: "pending", answer: "", answered_at: undefined, asked_at: "2026-11-10T06:31:00Z" }),
    ask({ id: spokenAsk, question: "Who presents next week?", question_source: "spoken", state: "failed", error: "model_error", answer: "", answered_at: undefined, asked_at: "2026-11-10T06:31:20Z" }),
  ];
  const items = () => [...roomItems().slice(0, -1), item(31, { client_msg_id: `ask-${waitingAsk}-1`, text: "And when is the next review?" })];
  const { ctx } = await context(theme, lang, width, { meta: {}, live: { asks: stuckAsks }, roomItems: items });
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  await section.locator(`li[data-ask="${spokenAsk}"]`).waitFor();
  await settle(page);
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "heard", "answer", "question", "question"]);
  const questions = section.locator('li[data-thread="question"]');
  assert.ok(await questions.nth(0).getByText(s.ask_failed_no_answer, { exact: true }).isVisible());
  assert.ok(await questions.nth(1).getByText(s.ask_pending, { exact: true }).isVisible());
  assert.equal(await section.locator("li[data-ask]").count(), 1, "Only the ask its thread does not hold");
  const row = section.locator(`li[data-ask="${spokenAsk}"]`);
  assert.equal(await row.locator(`[title="${s.ask_spoken}"]`).count(), 1);
  assert.ok(await row.getByText(s.ask_failed_model_error, { exact: true }).isVisible());
  await shoot(page, width === 1440 ? `conversation-stuck-${theme}.png` : `conversation-stuck-${lang}-${width}.png`);
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

/** The part of the detail from `selector` down, as one picture: the detail scrolls inside its own pane. */
async function shootFrom(page, selector, name) {
  await page.locator(selector).evaluate((el) => el.scrollIntoView({ block: "start" }));
  await settle(page);
  await page.screenshot({ path: join(output, name) });
  screenshots++;
}

/** Phase 3 while the meeting runs: the room's photos in its thread (a deleted one as "Photo removed", never the room's
 *  copy), **Photos** with the note and the folded text, and a photos revision read again from GET …/photos only. */
async function photosRunning(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const scene = {
    meta: {}, live: { revs: { room: 2, asks: 4, photos: 2 } }, roomItems: photoRoomItems,
    photos: (st) => st.photosRev >= 3 ? photosList().map((p) => (p.n === 3 ? { ...p, note: "A slide with the rollout plan: three steps, then a review." } : p)) : photosList(),
  };
  const { ctx, state, push } = await context(theme, lang, width, scene);
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const section = page.locator("[data-meeting-conversation]");
  const photos = page.locator("[data-recording-photos]");
  await section.getByText(ask().answer, { exact: true }).waitFor();
  await photos.locator('[data-photo-thumb="ready"]').nth(1).waitFor();
  await section.locator('li[data-thread="photo"] [data-photo-thumb="ready"]').nth(1).waitFor();
  assert.deepEqual(await kinds(page), ["joined", "lines", "lines", "photo", "photo", "heard", "answer", "question", "answer", "photo"]);
  const entries = section.locator('li[data-thread="photo"]');
  assert.ok(await entries.nth(0).getByText(fill(s.photo_label, { n: 1 }), { exact: true }).isVisible());
  assert.ok(await entries.nth(0).getByText(photosList()[0].note, { exact: true }).isVisible());
  assert.ok(await entries.nth(1).getByText(s.photo_removed, { exact: true }).isVisible());
  assert.equal(await entries.nth(1).locator("img").count(), 0, "A removed photo has no thumbnail");
  assert.deepEqual(state.roomImages, [], "Never the room's own copy of a photo");
  assert.equal(await page.getByText(s.thread_ended, { exact: true }).count(), 0, "The withdrawal is not the meeting's end");
  // **Photos**: two rows; the text folded until asked for.
  assert.equal(await photos.locator("li[data-photo]").count(), 2);
  const fold = photos.locator("li[data-photo]").first().getByRole("button", { name: s.photo_text, exact: true });
  assert.equal(await fold.getAttribute("aria-expanded"), "false");
  await fold.click();
  await photos.getByText("Travel 4k → 2k", { exact: false }).waitFor();
  assert.deepEqual(state.photos, [""]);
  assert.deepEqual([...state.images].sort(), [1, 3], "Each image read once");
  await settle(page);
  const tag = width === 1440 ? theme : `${lang}-${width}-${theme}`;
  await shootFrom(page, "[data-meeting-conversation]", `photos-running-thread-${tag}.png`);
  await shootFrom(page, "[data-recording-photos]", `photos-running-list-${tag}.png`);
  if (lang === "en" && width === 1440) {
    // The room's note on the newest photo lands: only GET …/photos is read again, never `?parts=photos`.
    const lives = state.live.length;
    state.photosRev = 3;
    push({ type: "meeting_live", recording_id: id, changed: ["photos"], revs: { photos: 3 } });
    await photos.getByText("A slide with the rollout plan: three steps, then a review.", { exact: false }).waitFor();
    assert.equal(state.photos.length, 2);
    assert.equal(state.live.length, lives, "photos has no field on …/live");
    // The same revision again reads nothing: the asks beside it are read, the photos not.
    state.asksRev = 5;
    push({ type: "meeting_live", recording_id: id, changed: ["photos", "asks"], revs: { photos: 3, asks: 5 } });
    await until(async () => state.live.length > lives, "the newer asks");
    assert.equal(state.live.at(-1), "?parts=asks");
    assert.equal(state.photos.length, 2);
  }
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

/** Phase 3 after the meeting: minutes items that cite a photo show it (one deleted since as "Photo removed") and go to it
 *  under **Photos**; a thumbnail there opens the image viewer. */
async function photosAfter(theme, lang = "en", width = 1440) {
  const s = JSON.parse(await readFile(new URL(`../src/i18n/locales/${lang}/knowledge-kbdetail.json`, import.meta.url), "utf8"));
  const scene = {
    meta: { complete: true, ended_at: "2026-11-10T14:31:00+08:00", minutes_state: "ready", room: ended, private: false },
    live: { room: ended, private: false, revs: { room: 3, asks: 4, private: 1, photos: 4 } },
    roomItems: photoRoomItems, minutes, photos: () => photosList(true),
  };
  const { ctx, state } = await context(theme, lang, width, scene);
  const errors = [];
  const page = await ctx.newPage();
  page.setDefaultTimeout(15_000);
  page.on("pageerror", (err) => errors.push(err.message));
  await page.goto(new URL(detailPath, base).href);
  const minutesBox = page.locator("[data-recording-minutes]");
  const cites = minutesBox.locator("[data-minutes-photo]");
  await minutesBox.locator('[data-minutes-photo] [data-photo-thumb="ready"]').nth(2).waitFor();
  assert.equal(await cites.count(), 4);
  assert.equal(await cites.nth(2).innerText(), s.photo_removed);
  assert.equal(await cites.nth(2).locator("img").count(), 0);
  assert.equal(await minutesBox.getByRole("button", { name: fill(s.minutes_photo_jump, { n: 1, time: "14:29:55" }), exact: true }).count(), 3);
  const cited = page.locator("[data-recording-photos] li[data-photo]").first();
  assert.ok(await cited.getByText(s.photo_cited, { exact: true }).isVisible());
  const tag = width === 1440 ? theme : `${lang}-${width}-${theme}`;
  await shootFrom(page, "[data-recording-minutes]", `photos-minutes-${tag}.png`);
  // The first citation goes to the photo, highlighted.
  await cites.first().click();
  await until(async () => (await cited.getAttribute("class")).includes("bg-accent-tint"), "the photo highlighted");
  assert.equal(await page.evaluate(() => document.activeElement?.id), "recording-photo-1");
  if (lang === "en" && width === 1440) {
    const shown = state.details;
    await cited.getByRole("button", { name: fill(s.photo_open, { n: 1 }), exact: true }).click();
    const viewer = page.getByRole("dialog");
    await viewer.locator("img").first().waitFor();
    await settle(page);
    await page.screenshot({ path: join(output, `photos-viewer-${theme}.png`) });
    screenshots++;
    await page.keyboard.press("Escape");
    await viewer.waitFor({ state: "detached" });
    assert.equal(state.details, shown, "Viewing a photo reads nothing again");
  }
  assert.deepEqual(errors, []);
  await ctx.close();
  runs++;
}

try {
  for (const theme of ["light", "dark"]) await photosRunning(theme);
  for (const theme of ["light", "dark"]) await photosRunning(theme, "en", 390);
  await photosRunning("light", "zh-Hans", 390);
  await photosRunning("dark", "ja", 390);
  for (const theme of ["light", "dark"]) await photosAfter(theme);
  for (const theme of ["light", "dark"]) await photosAfter(theme, "en", 390);
  await photosAfter("dark", "ja", 390);
  for (const theme of ["light", "dark"]) await running(theme);
  for (const theme of ["light", "dark"]) await stuck(theme);
  await stuck("dark", "ja", 390);
  for (const lang of ["en", "zh-Hans", "ja"]) await running("dark", lang, 390);
  for (const theme of ["light", "dark"]) await after(theme);
  for (const lang of ["zh-Hans", "ja"]) await after("light", lang, 390);
  for (const theme of ["light", "dark"]) await down(theme);
  console.log(`PASS recording conversation: ${runs} browser runs, ${screenshots} screenshots; private switch, room thread, questions without an answer, meeting_live parts, follow-up composer, follow-up thread, unavailable room, photos (thread, list, revision, minutes citations, viewer) and overflow checked`);
} catch (err) {
  console.error(`FAIL recording conversation: ${err?.stack || err}`);
  process.exitCode = 1;
} finally {
  clearTimeout(deadline);
  await browser.close();
}
