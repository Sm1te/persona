#!/usr/bin/env python3
"""End-to-end check of the composer's permission-mode and thinking-effort menus (#5584) in a real browser, where layout
exists (jsdom has none): every option shows its name and its whole description, nothing in a row is wider than the
row or runs past the menu, exactly one option is checked (aria-checked) and its check is not squeezed, and the menu
stays inside the window and above the chip that opened it. Runs an agent chat and a Persona chat, light and dark, at
1440, 390 and 320 wide. An agent chat has the permission chip only: its effort is on the composer's model pill (#5032).

    PORT=18871 MOCK_HUB_ID=mode-menu python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18871 npx vite --port 5371 --strictPort &)
    python3 scripts/test/web_mode_menu_e2e.py http://127.0.0.1:5371 [--shots <dir>] [--channel chrome]

Prints one line per check and a verdict; exits 1 on the first failure.
"""
import argparse
import os
import sys

from playwright.sync_api import sync_playwright

SIZES = {"wide": {"width": 1440, "height": 900}, "narrow": {"width": 390, "height": 844}, "tiny": {"width": 320, "height": 640}}
# the mock hub's Claude Code agent chat and Persona chat
PAGES = {"agent": "/s/macbook/a1f3c2d4-0000-4000-8000-000000000001", "assistant": "/persona/b2e4d6f8-0000-4000-8000-000000000002"}
CHIPS = {
    "permission": 'button[title="Permission mode"], button[title^="Never asks"]',
    "effort": 'button[title="Thinking effort"]',
}
# the chips each page has
PAGE_CHIPS = {"agent": ["permission"], "assistant": ["permission", "effort"]}

# For each option row: its name and description, whether anything inside overflows the row, and the check's width.
MEASURE = """menu => {
  const box = menu.getBoundingClientRect();
  const rows = [...menu.querySelectorAll('[role^="menuitem"]')].map((row) => {
    const r = row.getBoundingClientRect();
    const spans = [...row.querySelectorAll('span')];
    const wide = spans.filter((s) => s.scrollWidth > s.clientWidth + 1 || s.getBoundingClientRect().right > box.right - 1);
    const name = row.querySelector('span.block') || spans[0];
    const detail = row.getAttribute('aria-describedby') && document.getElementById(row.getAttribute('aria-describedby'));
    const svgs = [...row.querySelectorAll('svg')];
    const check = svgs[svgs.length - 1];
    return {
      role: row.getAttribute('role'), checked: row.getAttribute('aria-checked'),
      name: name ? name.innerText : '', nameWidth: name ? name.getBoundingClientRect().width : 0,
      detail: detail ? detail.innerText : null, overflow: wide.length > 0 || row.scrollWidth > row.clientWidth + 1 || r.right > box.right,
      check: check ? check.getBoundingClientRect().width : 0,
    };
  });
  return { left: box.left, right: box.right, top: box.top, bottom: box.bottom, rows };
}"""


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def check_menu(page, chip, tag, viewport):
    m = page.locator('[role="menu"]').first.evaluate(MEASURE)
    rows = m["rows"]
    if not rows:
        fail(f"{tag}: the menu has no options")
    for r in rows:
        if not r["name"].strip() or r["nameWidth"] < 20:
            fail(f"{tag}: an option's name is missing or squeezed ({r['name']!r}, {r['nameWidth']:.0f}px)")
        if not r["detail"]:
            fail(f"{tag}: {r['name']!r} has no description")
        if r["overflow"]:
            fail(f"{tag}: {r['name']!r} is clipped: something in the row is wider than it or runs past the menu")
        if r["role"] != "menuitemradio":
            fail(f"{tag}: {r['name']!r} is a {r['role']}, not one choice of a set")
    if [r["checked"] for r in rows].count("true") != 1:
        fail(f"{tag}: {[r['checked'] for r in rows]} checked, want exactly one")
    current = next(r for r in rows if r["checked"] == "true")
    if current["check"] < 12:
        fail(f"{tag}: the check on {current['name']!r} is squeezed to {current['check']:.0f}px")
    if m["left"] < 0 or m["right"] > viewport["width"] or m["top"] < 0:
        fail(f"{tag}: the menu runs out of the window ({m['left']:.0f}..{m['right']:.0f}, top {m['top']:.0f})")
    chip_top = chip.bounding_box()["y"]
    if m["bottom"] > chip_top + 1:
        fail(f"{tag}: the menu ends at {m['bottom']:.0f}, over the chip at {chip_top:.0f}")
    ok(f"{tag}: {len(rows)} options named and described, none clipped, one checked ({current['name']})")


def case(browser, base, size, theme, where, shots):
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, locale="en-US", color_scheme=theme)
    page = ctx.new_page()
    page.set_default_timeout(15000)
    page.goto(base + "/overview?token=mock")
    page.wait_for_function("localStorage.getItem('persona.token')")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
    page.goto(base + PAGES[where] + "?token=mock")
    for menu in PAGE_CHIPS[where]:
        chip = page.locator(CHIPS[menu]).first
        chip.wait_for()
        chip.click()
        page.wait_for_timeout(500)
        is_menu = page.locator('[role="menu"]').count() > 0
        if is_menu:
            check_menu(page, chip, f"{where}/{menu}/{theme}/{size}", SIZES[size])
        else:
            fail(f"{where}/{menu}/{theme}/{size}: the chip opened no menu")
        if shots:
            page.screenshot(path=os.path.join(shots, f"mode-menu-{where}-{menu}-{theme}-{size}.png"))
        page.keyboard.press("Escape")
        if is_menu:
            page.wait_for_selector('[role="menu"]', state="detached")
        page.wait_for_timeout(200)
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--channel", help="a browser channel (chrome) instead of Playwright's own Chromium")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel=a.channel)
        for theme in ("light", "dark"):
            for size in SIZES:
                for where in PAGES:
                    case(browser, a.base.rstrip("/"), size, theme, where, a.shots)
        browser.close()
    print("PASS", flush=True)


if __name__ == "__main__":
    main()
