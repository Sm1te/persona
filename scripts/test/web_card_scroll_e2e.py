#!/usr/bin/env python3
"""End-to-end check of a card waiting on you that is taller than the box over the composer (#3112): a permission card
with a very long command, or an ask_user card with many options. The card keeps its full height (it used to shrink
to what the box had left, its answer buttons cut off and nothing to scroll), its buttons stay on screen and can be
hit, and scrolling the box (the wheel on a desktop, a finger on a phone) brings its last line into view, where it
can be picked.

Where the cards sit differs: Persona's main chat draws them straight in its tray (MOCK_ASK_LONG=perm or =1, the
default --path /persona: guards #2026, a card squeezed by the tray); an agent's chat on a phone folds them under the
composer (MOCK_ASK_LONG=agent with --path /s/macbook/<session>: guards #3112, a fold that clips and leaves the buttons
below it). The agent's chat has two long cards: the second is checked
too, after tapping "1 more" to open its fold.

Needs a running web page against the fake hub, Playwright for Python and its bundled browsers:

    MOCK_ASK_LONG=agent PORT=18973 python3 scripts/mock/mock_hub.py &      # =perm / =1: the main chat's tray
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18973 npx vite --port 5973 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 -m playwright install chromium webkit
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_card_scroll_e2e.py http://127.0.0.1:5973 \
        --path /s/macbook/a1f3c2d4-0000-4000-8000-000000000002 [--browser chromium|webkit|all] [--theme light|dark|both] [--shots <dir>]

Checks 1440×900, 1280×720 and a 390×844 phone (touch events in Chromium; WebKit's phone uses the wheel), in English
(the tray's labels). Defaults: Chromium, light. Prints one line per check and a verdict; exits 1 on the first failure.
--shots saves each card before and after scrolling. Not for a very short box (a phone on its side): the swipe starts
just over the buttons and could land outside it.
"""
import argparse
import json
import os
import sys

from playwright.sync_api import sync_playwright

UA = ("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 "
      "(KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1")
CARD = "div:has(> [data-card-actions])"
SIZES = [("desktop", 1440, 900), ("small-window", 1280, 720), ("phone", 390, 844)]

# The card on screen: its heights, the box it scrolls in, the answer button (Allow / Send) and whether a finger on its
# middle lands on it, and the card's last line (the always-allow note, the last option) likewise.
LONG = "lark|这周先处理"  # the long cards' titles (MOCK_ASK_LONG), in English and Chinese
STATE = """(done = []) => {
  const long = new RegExp('""" + LONG + """');
  const card = [...document.querySelectorAll('div:has(> [data-card-actions])')].find((c) => {
    const l = c.getAttribute('aria-label') || '';
    return long.test(l) && !done.includes(l) && !c.closest('[inert]');
  });
  if (!card) return null;
  let box = card.parentElement;
  while (box && !/(auto|scroll)/.test(getComputedStyle(box).overflowY)) box = box.parentElement;
  const hit = (el) => { if (!el) return null; const r = el.getBoundingClientRect(); const x = r.left + r.width / 2, y = r.top + Math.min(r.height / 2, 10);
    return { top: r.top, bottom: r.bottom, hit: y >= 0 && y < innerHeight && el.contains(document.elementFromPoint(x, y)), x, y }; };
  const footer = card.querySelector('[data-card-actions]');
  const buttons = footer ? [...footer.querySelectorAll('button')] : [];
  const main = buttons[buttons.length - 1];
  const options = [...card.querySelectorAll('button[aria-pressed]')];
  const last = options.length ? options[options.length - 1] : [...card.querySelectorAll('p')].pop();
  return { kind: options.length ? 'question' : 'permission', card: [card.clientHeight, card.scrollHeight],
    box: box && [box.scrollTop, box.clientHeight, box.scrollHeight], main: hit(main), last: hit(last),
    cx: (() => { const r = card.getBoundingClientRect(); return r.left + r.width / 2; })(),
    head: (() => { const r = card.getBoundingClientRect(); return r.top + 24 < innerHeight ? r.top + 24 : null; })(),
    // what shows under the buttons while the box has more below (at its end the box's own padding is under them)
    under: (() => { if (!footer || !box) return null; const f = footer.getBoundingClientRect(), b = box.getBoundingClientRect();
      return card.getBoundingClientRect().bottom > b.bottom + 1 ? b.bottom - f.bottom : null; })(),
    lastText: last && last.textContent, title: card.getAttribute('aria-label'), inner: innerHeight };
}"""


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def scroll_box(page, cdp, s, touch, done):
    """Scrolls the card's box down in small steps, by a finger (touch) or the wheel from the card's upper part, until the
    card's last line can be hit (other cards may follow it in the box: don't run past it)."""
    x = s["cx"]
    # from the card's title (not its command box, which scrolls by itself), else just over the buttons
    y0 = s["head"] if s["head"] is not None and s["head"] > 80 else max(s["main"]["y"] - 60, 80)
    page.mouse.move(x, y0)
    for _ in range(30):
        st = page.evaluate(STATE, done)
        if st["last"] and st["last"]["hit"]:
            return
        if touch:
            cdp.send("Input.dispatchTouchEvent", {"type": "touchStart", "touchPoints": [{"x": x, "y": y0}]})
            for i in range(1, 9):
                cdp.send("Input.dispatchTouchEvent", {"type": "touchMove", "touchPoints": [{"x": x, "y": y0 - 60 * i / 8}]})
                page.wait_for_timeout(16)
            cdp.send("Input.dispatchTouchEvent", {"type": "touchEnd", "touchPoints": []})
        else:
            page.mouse.wheel(0, 60)
        page.wait_for_timeout(200)
        # the title may have scrolled away: keep the pointer over the card
        nxt = page.evaluate(STATE, done)
        if nxt["head"] is None or nxt["head"] < 80:
            y0 = max(nxt["main"]["y"] - 60, 80)


def go(page, path):
    """In-page navigation (a pushState and its popstate), as a link in the app would: a second page load would cut the
    first page's requests off, which WebKit reports as page errors."""
    page.evaluate(f"history.pushState(null, '', {json.dumps(path)}); dispatchEvent(new PopStateEvent('popstate'))")


def check(browser, engine, base, label, w, h, shots, path="/persona", theme="light"):
    phone = w < 640
    touch = phone and engine == "chromium"
    ctx = browser.new_context(viewport={"width": w, "height": h}, device_scale_factor=2 if phone else 1,
                              is_mobile=phone and engine == "chromium", has_touch=phone, user_agent=UA if phone else None,
                              color_scheme=theme, locale="en-US")
    page = ctx.new_page()
    errors = []
    page.on("pageerror", lambda e: errors.append(str(e)))
    page.goto(base + "/overview?token=mock")
    page.wait_for_selector("aside", state="attached", timeout=20000)
    go(page, path)
    page.wait_for_selector("[data-chat-dock]", timeout=20000)
    page.wait_for_timeout(800)
    # Persona's main chat keeps what waits on you in the "Pending" tray over the composer: open it
    tray = page.locator("[data-chat-dock] button").filter(has_text="Pending")
    if not page.locator(CARD).count() and tray.count():
        tray.first.click()
    page.wait_for_selector(CARD, timeout=20000)
    what = f"{engine} {label} {w}x{h} {theme}"
    where = path.strip("/").split("/")[0] if path != "/persona" else "persona"
    cdp = ctx.new_cdp_session(page) if touch else None
    first = check_card(page, cdp, touch, what, shots and os.path.join(shots, f"{where}-{engine}-{w}x{h}-{theme}-1"), [])
    if path != "/persona":
        # the agent's second long card: behind "1 more" where the cards fold (phone), else right under
        fold = page.locator("[data-chat-cards-fold]")
        if fold.count():
            if touch:
                b = fold.first.bounding_box()
                page.touchscreen.tap(b["x"] + b["width"] / 2, b["y"] + b["height"] / 2)
            else:
                fold.first.click()
            page.wait_for_timeout(1200)
            ok(f"{what}: opened the folded cards")
        check_card(page, cdp, touch, what, shots and os.path.join(shots, f"{where}-{engine}-{w}x{h}-{theme}-2"), [first])
    if errors:
        fail(f"{what}: page errors: {errors[:3]}")
    ctx.close()


def check_card(page, cdp, touch, what, shot, done):
    """One long card a user can reach (not one already checked): full height, buttons on screen, scrolled to its last
    line (and its last option picked). Returns its title."""
    # in view in its box, as reading down to it would leave it
    page.evaluate("""(done) => { const long = new RegExp('""" + LONG + """');
      [...document.querySelectorAll('div:has(> [data-card-actions])')].find((c) => { const l = c.getAttribute('aria-label') || '';
        return long.test(l) && !done.includes(l) && !c.closest('[inert]'); })?.scrollIntoView({ block: 'start' }); }""", done)
    page.wait_for_timeout(1200)
    s = page.evaluate(STATE, done)
    if not s:
        fail(f"{what}: no long card to check{' besides ' + repr(done) if done else ''} (a fake hub with MOCK_ASK_LONG?)")
    what = f"{what} {s['title']!r}"
    if shot:
        page.screenshot(path=f"{shot}-{s['kind']}-top.png")
    if s["card"][1] > s["card"][0] + 1:
        fail(f"{what}: the card is squeezed to {s['card'][0]} of its {s['card'][1]} px")
    ok(f"{what}: the {s['kind']} card keeps its full height ({s['card'][0]} px)")
    if not s["main"] or not s["main"]["hit"] or s["main"]["bottom"] > s["inner"] + 0.5:
        fail(f"{what}: the answer button is out of sight ({s['main']})")
    ok(f"{what}: the answer button is on screen and can be hit")
    if s["under"] is not None and s["under"] > 1:
        fail(f"{what}: {s['under']:.1f} px of the card show under its buttons")
    if not s["box"]:
        fail(f"{what}: no box to scroll the card in")
    if s["box"][2] <= s["box"][1] + 1:
        # it fits: all of it on screen, nothing to scroll
        if not s["last"] or not s["last"]["hit"]:
            fail(f"{what}: the card fits its box, yet its last line ({s['lastText']!r}) is out of sight ({s['last']})")
        ok(f"{what}: the card fits its box, its last line on screen")
        return s["title"]
    scroll_box(page, cdp, s, touch, done)
    t = page.evaluate(STATE, done)
    if t["box"][0] <= s["box"][0] and not (s["last"] and s["last"]["hit"]):
        fail(f"{what}: {'swiping' if touch else 'the wheel'} did not scroll the card's box ({s['box']} → {t['box']})")
    if not t["last"] or not t["last"]["hit"]:
        fail(f"{what}: the card's last line ({t['lastText']!r}) is out of sight after scrolling ({t['last']})")
    if not t["main"]["hit"]:
        fail(f"{what}: the answer button left the screen while scrolling ({t['main']})")
    ok(f"{what}: {'a swipe' if touch else 'the wheel'} scrolls the box to the last line, the button still on screen")
    if t["kind"] == "question":
        if touch:
            page.touchscreen.tap(t["last"]["x"], t["last"]["y"])
        else:
            page.mouse.click(t["last"]["x"], t["last"]["y"])
        # read at once: a single-choice pick moves on to the next question 380 ms later (QuestionCard)
        page.wait_for_timeout(120)
        pressed = page.evaluate("""(title) => { const c = [...document.querySelectorAll('div:has(> [data-card-actions])')].find((c) => c.getAttribute('aria-label') === title);
          const o = [...c.querySelectorAll('button[aria-pressed]')]; return o[o.length - 1].getAttribute('aria-pressed'); }""", t["title"])
        if pressed != "true":
            fail(f"{what}: tapping the last option did not pick it")
        ok(f"{what}: the last option is picked")
    if shot:
        page.screenshot(path=f"{shot}-{t['kind']}-scrolled.png")
    return t["title"]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--browser", default="chromium", choices=["chromium", "webkit", "all"])
    ap.add_argument("--shots")
    ap.add_argument("--theme", default="light", choices=["light", "dark", "both"])
    ap.add_argument("--path", default="/persona", help="the chat to open: /persona, or an agent's /s/<machine>/<session> (MOCK_ASK_LONG=agent)")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    engines = ["chromium", "webkit"] if a.browser == "all" else [a.browser]
    with sync_playwright() as p:
        for engine in engines:
            browser = getattr(p, engine).launch()
            for label, w, h in SIZES:
                for theme in (["light", "dark"] if a.theme == "both" else [a.theme]):
                    check(browser, engine, a.base.rstrip("/"), label, w, h, a.shots, a.path, theme)
            browser.close()
    print("PASS web_card_scroll_e2e")


if __name__ == "__main__":
    main()
