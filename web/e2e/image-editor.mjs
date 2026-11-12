// Screenshots and a recording of the image editor (#4991, docs/web/web.md "Image viewer"): a made-up picture attached to
// the main chat's composer, opened from its tile, marked with Box and Pen in each width, cropped by a corner, given a
// shape, turned, stepped back and forward with Undo and Redo, finished, and opened again from the tile with its edits.
//
// Needs the web page against the fake hub, and Chromium (Playwright's, Chrome with --channel chrome, Edge with
// --channel msedge):
//
//   PORT=18977 python3 scripts/mock/mock_hub.py &
//   (cd web && PERSONA_HUB_URL=http://127.0.0.1:18977 npx vite --host 127.0.0.1 --port 5977 --strictPort &)
//   node web/e2e/image-editor.mjs http://127.0.0.1:5977 --shots <dir> [--before] [--video <dir> --ffmpeg <ffmpeg>] [--channel msedge | --executable <chromium>]
//
// Runs en and zh-Hans, light and dark, 1440 × 900 and 390 × 844 (touch), then ja at 320 × 700 with the largest text size. --before takes only what the editor had before
// #4991 (opened, a box drawn), for the comparison. --video records the whole walk-through once wide in English (dark)
// and once on a phone in Chinese (light), slowed down, from Chrome's screencast: VP8 WebM through --ffmpeg (any build
// with libvpx; Playwright's own recorder needs VP9), else the frames as JPEGs. Prints one line per check and a verdict;
// exits 1 on a failure and after five minutes. Every request outside the loopback server is refused; the picture is
// drawn in the page.
import { spawn } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright-core";

const args = process.argv.slice(2);
const flag = (name) => {
  const i = args.indexOf(name);
  return i >= 0 ? args[i + 1] : undefined;
};
const base = (args[0] ?? "").replace(/\/$/, "");
const shots = flag("--shots");
const video = flag("--video");
const ffmpeg = flag("--ffmpeg");
const before = args.includes("--before");
const executable = flag("--executable");
const channel = flag("--channel");
if (!base || !shots) {
  console.error("usage: node web/e2e/image-editor.mjs <web URL> --shots <dir> [--before] [--video <dir>] [--channel msedge | --executable <chromium>]");
  process.exit(2);
}
if (!["127.0.0.1", "localhost"].includes(new URL(base).hostname)) {
  console.error(`only a loopback server is allowed: ${base}`);
  process.exit(2);
}
mkdirSync(shots, { recursive: true });
if (video) mkdirSync(video, { recursive: true });
setTimeout(() => {
  console.log("FAIL timed out after 5 minutes");
  process.exit(1);
}, 300_000).unref();

const ok = (msg) => console.log(`ok  ${msg}`);
function fail(msg) {
  console.log(`FAIL ${msg}`);
  process.exit(1);
}

const locales = join(dirname(fileURLToPath(import.meta.url)), "../src/i18n/locales");
const strings = (lang, file) => JSON.parse(readFileSync(join(locales, lang, `${file}.json`), "utf8"));
const words = (lang) => ({ ...strings(lang, "chat-imageeditor"), cancel: strings(lang, "common").cancel, tile: strings(lang, "ui-attachmenttray").edit });

/** A made-up landscape, 1600 × 1000, drawn in the page: sky, sun, hills and a signpost. */
async function picture(page) {
  const url = await page.evaluate(() => {
    const c = document.createElement("canvas");
    c.width = 1600;
    c.height = 1000;
    const g = c.getContext("2d");
    const sky = g.createLinearGradient(0, 0, 0, 1000);
    sky.addColorStop(0, "#5aa9e6");
    sky.addColorStop(1, "#cfe8ff");
    g.fillStyle = sky;
    g.fillRect(0, 0, 1600, 1000);
    g.fillStyle = "#ffd166";
    g.beginPath();
    g.arc(1240, 230, 110, 0, Math.PI * 2);
    g.fill();
    g.fillStyle = "#7fb069";
    g.beginPath();
    g.moveTo(0, 720);
    g.quadraticCurveTo(420, 520, 860, 700);
    g.quadraticCurveTo(1220, 840, 1600, 640);
    g.lineTo(1600, 1000);
    g.lineTo(0, 1000);
    g.fill();
    g.fillStyle = "#4f772d";
    g.beginPath();
    g.moveTo(0, 860);
    g.quadraticCurveTo(700, 720, 1600, 900);
    g.lineTo(1600, 1000);
    g.lineTo(0, 1000);
    g.fill();
    g.fillStyle = "#8d6346";
    g.fillRect(470, 560, 22, 220);
    g.fillStyle = "#f4f1de";
    g.fillRect(380, 520, 220, 70);
    g.fillStyle = "#3d405b";
    g.font = "bold 40px sans-serif";
    g.fillText("Trail", 438, 570);
    return c.toDataURL("image/png");
  });
  return Buffer.from(url.slice(url.indexOf(",") + 1), "base64");
}

async function open(browser, { lang, theme, phone, small }) {
  const ctx = await browser.newContext({
    viewport: small ? { width: 320, height: 700 } : phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    colorScheme: theme,
    locale: lang === "en" ? "en-US" : lang === "ja" ? "ja-JP" : "zh-CN",
    hasTouch: phone,
    isMobile: phone,
  });
  await ctx.route("**/*", (route) => (new URL(route.request().url()).origin === new URL(base).origin ? route.continue() : route.abort()));
  await ctx.addInitScript(
    ([lang, theme, large]) => {
      localStorage.setItem("persona.language", lang);
      localStorage.setItem("persona.theme", theme);
      // the largest text size (Settings › Appearance; ui/look.ts TEXT_SIZE_KEY)
      if (large) localStorage.setItem("persona.text-size", "large");
    },
    [lang, theme, Boolean(small)],
  );
  const page = await ctx.newPage();
  page.setDefaultTimeout(15000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(`${base}/?token=mock`);
  await page.waitForFunction(() => localStorage.getItem("persona.token"), null, { timeout: 15000 });
  return { ctx, page, errors };
}

/** Chrome's screencast of `page` until the returned function is called, then written as `<name>.webm` (or JPEGs). */
async function record(ctx, page, name) {
  const cdp = await ctx.newCDPSession(page);
  const frames = [];
  cdp.on("Page.screencastFrame", ({ data, metadata, sessionId }) => {
    frames.push({ at: metadata.timestamp, data: Buffer.from(data, "base64") });
    void cdp.send("Page.screencastFrameAck", { sessionId }).catch(() => {});
  });
  await cdp.send("Page.startScreencast", { format: "jpeg", quality: 85 });
  return async () => {
    await cdp.send("Page.stopScreencast");
    if (!frames.length) fail(`${name}: no frames recorded`);
    // a steady 10 frames a second: each frame held until the next came
    const fps = 10;
    const steady = [];
    frames.forEach((f, i) => {
      const next = frames[i + 1]?.at ?? f.at + 1;
      for (let n = Math.max(1, Math.round((next - f.at) * fps)); n > 0; n--) steady.push(f.data);
    });
    if (!ffmpeg) {
      const dir = join(video, name);
      mkdirSync(dir, { recursive: true });
      steady.forEach((d, i) => writeFileSync(join(dir, `frame-${String(i).padStart(5, "0")}.jpg`), d));
      return ok(`${name}: ${steady.length} frames in ${dir}`);
    }
    const out = join(video, `${name}.webm`);
    const enc = spawn(ffmpeg, ["-loglevel", "error", "-y", "-f", "image2pipe", "-c:v", "mjpeg", "-framerate", String(fps), "-i", "pipe:0", "-c:v", "libvpx", "-b:v", "3M", "-pix_fmt", "yuv420p", out], { stdio: ["pipe", "inherit", "inherit"] });
    for (const d of steady) if (!enc.stdin.write(d)) await new Promise((r) => enc.stdin.once("drain", r));
    enc.stdin.end();
    const code = await new Promise((r) => enc.on("close", r));
    if (code !== 0) fail(`${name}: ffmpeg exited ${code}`);
    ok(`${name}: ${out} (${(steady.length / fps).toFixed(1)} s)`);
  };
}

const still = async (page, name) => {
  await page.waitForTimeout(300); // the open animations
  await page.screenshot({ path: join(shots, `${name}.png`) });
};

/** The picture attached to the main chat's composer, and the editor opened from its tile. */
async function openEditor(page, w, attached) {
  const input = page.locator('main input[type="file"]').first();
  await input.waitFor({ state: "attached" });
  await input.setInputFiles({ name: "trail.png", mimeType: "image/png", buffer: await picture(page) });
  // the composer with the picture in it, nothing edited: the same before and after
  await page.getByRole("button", { name: w.tile.replace("{name}", "trail.png"), exact: true }).waitFor();
  await page.mouse.move(0, 0);
  await attached?.();
  // low on the left: the remove button's touch area covers the tile's top right
  await page.getByRole("button", { name: w.tile.replace("{name}", "trail.png"), exact: true }).click({ position: { x: 14, y: 42 } });
  const dialog = page.getByRole("dialog", { name: w.title, exact: true });
  await dialog.waitFor({ state: "visible" });
  await page.waitForTimeout(400); // the picture decoded and drawn
  return dialog;
}

/** A drag on the editor's canvas between two points given as fractions of it, in steps. */
async function drag(page, dialog, from, to, { hold = 0, steps = 12 } = {}) {
  const box = await dialog.locator("canvas").boundingBox();
  const at = (p) => ({ x: box.x + box.width * p[0], y: box.y + box.height * p[1] });
  const a = at(from), b = at(to);
  await page.mouse.move(a.x, a.y);
  await page.mouse.down();
  await page.mouse.move(b.x, b.y, { steps });
  if (hold) await page.waitForTimeout(hold);
  return () => page.mouse.up();
}

const button = (dialog, name) => dialog.getByRole("button", { name, exact: true });
/** A menu's button: its name is the menu's and the choice shown ("Line width: Medium", "粗细：中"). */
const menuButton = (dialog, name) => dialog.getByRole("button", { name: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}[:：]`) });
/** The made-up picture's size, and the room the editor leaves round it while cropping (IMAGE_EDIT.cropInset). */
const PICTURE = { width: 1600, height: 1000 };
const CROP_INSET = 28;
function fitted(size, box) {
  const s = Math.min(box.width / size.width, box.height / size.height);
  const width = size.width * s, height = size.height * s;
  return { x: box.x + (box.width - width) / 2, y: box.y + (box.height - height) / 2, width, height };
}
const pause = (page, ms) => page.waitForTimeout(ms);

async function runBefore(browser, combo) {
  const { lang, theme, phone } = combo;
  const tag = `${lang}-${theme}-${combo.small ? "320-large" : phone ? "390" : "1440"}`;
  const w = words(lang);
  const { ctx, page, errors } = await open(browser, combo);
  const dialog = await openEditor(page, w, () => still(page, `before-${tag}-attached`));
  await still(page, `before-${tag}-open`);
  await (await drag(page, dialog, [0.3, 0.3], [0.55, 0.6]))();
  await still(page, `before-${tag}-box`);
  if (errors.length) fail(`${tag}: page errors ${errors.join(" | ")}`);
  ok(`${tag}: before shots`);
  await ctx.close();
}

async function runAfter(browser, combo) {
  const { lang, theme, phone, recording } = combo;
  const tag = `${lang}-${theme}-${combo.small ? "320-large" : phone ? "390" : "1440"}`;
  const w = words(lang);
  const slow = recording ? 700 : 0;
  const { ctx, page, errors } = await open(browser, combo);
  const stop = recording ? await record(ctx, page, `recording-${tag}`) : null;
  // the recording run walks the same steps; its stills would only overwrite the ones taken already
  const shot = recording ? async () => {} : still;
  const dialog = await openEditor(page, w, () => shot(page, `after-${tag}-attached`));
  if ((await button(dialog, w.box).getAttribute("aria-pressed")) !== "true") fail(`${tag}: the editor does not open on Box`);
  if (!(await button(dialog, w.undo).isDisabled()) || !(await button(dialog, w.redo).isDisabled())) fail(`${tag}: Undo / Redo enabled with nothing done`);
  await shot(page, `after-${tag}-open`);
  await pause(page, slow);

  await (await drag(page, dialog, [0.3, 0.3], [0.55, 0.6]))();
  await pause(page, slow);
  await button(dialog, w.pen).click();
  await menuButton(dialog, w.line_width).click();
  const menu = page.getByRole("menu").last();
  await menu.waitFor({ state: "visible" });
  await shot(page, `after-${tag}-width-menu`);
  await pause(page, slow);
  await menu.getByRole("menuitemradio", { name: w.width_thick }).click();
  await (await drag(page, dialog, [0.62, 0.35], [0.8, 0.55], { steps: 16 }))();
  await pause(page, slow);
  await shot(page, `after-${tag}-pen`);
  if (await button(dialog, w.undo).isDisabled()) fail(`${tag}: Undo off after two marks`);

  await button(dialog, w.crop).click();
  await pause(page, slow);
  await shot(page, `after-${tag}-crop`);
  // the bottom-right corner, just inside it: cropping, the whole picture is fitted in the canvas less CROP_INSET round it
  const canvas = await dialog.locator("canvas").boundingBox();
  const frame = fitted(PICTURE, { x: CROP_INSET, y: CROP_INSET, width: canvas.width - 2 * CROP_INSET, height: canvas.height - 2 * CROP_INSET });
  await page.mouse.move(canvas.x + frame.x + frame.width - 4, canvas.y + frame.y + frame.height - 4);
  await page.mouse.down();
  await page.mouse.move(canvas.x + frame.x + frame.width * 0.7, canvas.y + frame.y + frame.height * 0.75, { steps: 14 });
  await shot(page, `after-${tag}-crop-dragging`);
  await page.mouse.up();
  await pause(page, slow);
  await menuButton(dialog, w.aspect_ratio).click();
  const shapes = page.getByRole("menu").last();
  await shapes.waitFor({ state: "visible" });
  await shot(page, `after-${tag}-shape-menu`);
  await pause(page, slow);
  await shapes.getByRole("menuitemradio", { name: "16:9" }).click();
  await pause(page, slow);
  await button(dialog, w.rotate).click();
  await pause(page, slow);
  await shot(page, `after-${tag}-turned`);
  if (!(await menuButton(dialog, w.aspect_ratio).getAttribute("aria-label")).endsWith("9:16")) fail(`${tag}: a turn did not take 16:9 to 9:16`);

  // Undo back over the turn and the shape, Redo once
  await button(dialog, w.undo).click();
  await pause(page, slow);
  await button(dialog, w.undo).click();
  await pause(page, slow);
  await button(dialog, w.redo).click();
  await pause(page, slow);
  await shot(page, `after-${tag}-undo-redo`);
  if (!(await menuButton(dialog, w.aspect_ratio).getAttribute("aria-label")).endsWith("16:9")) fail(`${tag}: Undo, Undo, Redo did not land on 16:9 unturned`);

  await button(dialog, w.box).click();
  await pause(page, slow);
  await shot(page, `after-${tag}-cropped`);
  await button(dialog, w.done).click();
  await dialog.waitFor({ state: "detached" });
  await pause(page, slow);
  await shot(page, `after-${tag}-composer`);
  // the picture sent is the 16:9 crop of the 1600 × 1000 source, at its own pixels
  const sent = await page.locator('main img[alt="image.png"]').first().evaluate((el) => (el.complete ? [el.naturalWidth, el.naturalHeight] : null));
  if (!sent || sent[0] !== 1600 || sent[1] !== 900) fail(`${tag}: the result is ${sent} pixels, not 1600 × 900`);

  // again from the tile: the crop, the marks and the history are as they were
  await page.getByRole("button", { name: w.tile.replace("{name}", "image.png"), exact: true }).click({ position: { x: 14, y: 42 } });
  await dialog.waitFor({ state: "visible" });
  await page.waitForTimeout(400);
  await pause(page, slow);
  if (await button(dialog, w.undo).isDisabled()) fail(`${tag}: the edits came back without their history`);
  await shot(page, `after-${tag}-reopened`);
  for (let i = 0; i < 6 && !(await button(dialog, w.undo).isDisabled()); i++) {
    await button(dialog, w.undo).click();
    await pause(page, slow / 2);
  }
  await shot(page, `after-${tag}-undone`);
  await button(dialog, w.cancel).click();
  await dialog.waitFor({ state: "detached" });
  await pause(page, slow);
  if (stop) await stop();
  if (errors.length) fail(`${tag}: page errors ${errors.join(" | ")}`);
  ok(`${tag}: ${recording ? "recording" : "after shots"}`);
  await ctx.close();
}

const browser = await chromium.launch(executable ? { executablePath: executable } : channel ? { channel } : {});
try {
  for (const lang of ["en", "zh-Hans"])
    for (const theme of ["light", "dark"])
      for (const phone of [false, true]) {
        const combo = { lang, theme, phone };
        await (before ? runBefore(browser, combo) : runAfter(browser, combo));
      }
  // the narrowest phone at the largest text size, in the language with the longest words: the tools keep to one line
  await (before ? runBefore : runAfter)(browser, { lang: "ja", theme: "dark", phone: true, small: true });
  if (video && !before) {
    await runAfter(browser, { lang: "en", theme: "dark", phone: false, recording: true });
    await runAfter(browser, { lang: "zh-Hans", theme: "light", phone: true, recording: true });
  }
  console.log("PASS image editor shots");
} finally {
  await browser.close();
}
