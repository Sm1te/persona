#!/usr/bin/env python3
"""End-to-end check of a herdr tab's Team on the web (#5180, after cindy's Team). On `MOCK_TEAM=1` one tab of the computer
"server" holds a lead (its first pane) and three running workers (a Codex review that waits on a permission, a done
test pass, a docs pass at work) and a stopped pane. No worker is a row of its own; the lead's row needs you (its waiting
worker). On a wide screen the lead's page opens its Team panel by itself: a strip of the three running workers in pane
order, the waiting one shown, its conversation and its permission card in the panel, not also in the lead's dock.
Picking another worker moves the card to the lead's dock, "From worker · …". Closing the panel is kept: the next visit
leaves it shut, the header's Team button carries the attention dot and opens it again. Old links to a worker open the
lead with the panel on it. On a phone the panel never opens by itself; the Team button and old links open it. Light /
dark × 1280 / 390.

    MOCK_TEAM=1 PORT=18931 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18931 npx vite --port 5201 --strictPort &)
    python3 scripts/test/web_team_e2e.py http://127.0.0.1:5201 [--shots <dir>]

Prints one line per check and a verdict (also when a wait fails or times out); exits 1 on the first failure.
"""
import argparse
import os
import sys

from playwright.sync_api import Error as PlaywrightError, expect, sync_playwright

MID = "server"
LEAD = "7ab5e000-0000-4000-8000-000000000001"
REVIEW = "7ab5e000-0000-4000-8000-000000000002"  # Codex, waits on a permission (pane 1)
TESTS = "7ab5e000-0000-4000-8000-000000000003"  # done, not seen (pane 2)
SIZES = {"wide": {"width": 1280, "height": 860}, "narrow": {"width": 390, "height": 844}}
OVERFLOW = "() => document.documentElement.scrollWidth - document.documentElement.clientWidth"
TEAM = "协同"


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def run(browser, base, token, size, theme, shots):
    tag = f"{size}/{theme}"
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow", color_scheme=theme, locale="zh-CN")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    errors = []
    page.on("pageerror", lambda e: errors.append(str(e)))
    page.goto(base + "/overview?token=" + token)
    page.wait_for_load_state("networkidle")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
    prefix = f"team-{size}-{theme}"

    def shot(name, el=None):
        if shots:
            (el or page).screenshot(path=os.path.join(shots, f"{prefix}-{name}.png"))

    panel = page.locator(f'aside[aria-label="{TEAM}"]')
    pills = panel.get_by_role("tab")
    button = page.locator("main header").get_by_role("button", name=TEAM)
    main_dock = page.locator("main").locator("[data-card]")

    # the list: on the phone the Agents page, on a computer the sidebar beside the lead's page
    page.goto(base + ("/agents" if size == "narrow" else f"/s/{MID}/{LEAD}"))
    page.wait_for_load_state("networkidle")
    lead_row = page.locator(f'a[href$="/{LEAD}"]').first
    expect(lead_row).to_be_visible()
    for sid in (REVIEW, TESTS):
        if page.locator(f'nav a[href$="/{sid}"], [data-agents-list] a[href$="/{sid}"]').count():
            fail(f"{tag}: the worker {sid} is a row of its own")
    expect(lead_row).to_contain_text("在等你批准")
    expect(lead_row).to_contain_text("go test ./auth/")
    ok(f"{tag}: no worker is a row; the lead's row says what its waiting worker asks")
    shot("list")

    if size == "wide":
        # arriving at the lead on a wide screen opens its Team by itself, on the worker that needs you
        page.wait_for_url(f"**/t/{MID}/**/{LEAD}")
        expect(panel).to_be_visible()
        expect(pills).to_have_count(3)
        expect(pills.nth(0)).to_have_attribute("aria-selected", "true")
        expect(pills.nth(0)).to_contain_text("审一下登录重构的改动")
        expect(panel.locator("header")).to_contain_text("子 Agent 3 个")
        expect(panel.locator("[data-card]")).to_have_count(1)
        if main_dock.count():
            fail(f"{tag}: the shown worker's card is in the lead's dock too")
        ok(f"{tag}: the Team panel opened by itself on the waiting worker, its card in its conversation only")
        shot("panel")
        # another worker: the waiting one's card goes to the lead's dock, marked as that worker's
        pills.nth(1).click()
        expect(pills.nth(1)).to_have_attribute("aria-selected", "true")
        expect(panel.locator("[data-card]")).to_have_count(0)
        expect(main_dock.locator("[data-permission-from]").first).to_contain_text("来自子 Agent · 审一下登录重构的改动")
        ok(f"{tag}: picking another worker puts the waiting one's card in the lead's dock, from that worker")
        shot("switched")
        # closed by hand: kept closed next time, the button carrying the dot
        panel.get_by_role("button", name="关闭面板").click()
        expect(panel).to_have_count(0)
        page.reload()
        page.wait_for_load_state("networkidle")
        page.wait_for_timeout(500)
        if panel.count():
            fail(f"{tag}: a panel closed by hand opened by itself again")
        expect(page.locator("main header").get_by_role("button", name="协同：有子 Agent 在等你")).to_be_visible()
        ok(f"{tag}: a panel closed by hand stays shut on the next visit; the Team button says a worker needs you")
        shot("closed")
        page.locator("main header").get_by_role("button", name="协同：有子 Agent 在等你").click()
        expect(panel).to_be_visible()
        ok(f"{tag}: the Team button opens it again")
    else:
        # a phone: the panel would cover the page, so it never opens by itself
        page.goto(base + f"/s/{MID}/{LEAD}")
        page.wait_for_url(f"**/t/{MID}/**/{LEAD}")
        page.wait_for_load_state("networkidle")
        page.wait_for_timeout(500)
        if panel.count():
            fail(f"{tag}: the Team panel opened by itself on a phone")
        expect(main_dock.locator("[data-permission-from]").first).to_contain_text("来自子 Agent · 审一下登录重构的改动")
        expect(page.locator("main header").get_by_role("button", name="协同：有子 Agent 在等你")).to_be_visible()
        ok(f"{tag}: on a phone the panel stays shut; the worker's card is in the lead's dock and the Team button has the dot")
        shot("lead")
        page.locator("main header").get_by_role("button", name="协同：有子 Agent 在等你").click()
        expect(panel).to_be_visible()
        expect(pills).to_have_count(3)
        expect(panel.locator("[data-card]")).to_have_count(1)
        ok(f"{tag}: the Team button opens the panel over the page, on the waiting worker")
        shot("panel")
        panel.get_by_role("button", name="关闭面板").click()
        expect(panel).to_have_count(0)
        # on a phone the panel has a history entry of its own (Back closes it): let closing step back before going on
        page.wait_for_timeout(400)

    # an old link to a worker: the lead's page, the panel on that worker (at any width)
    page.goto(base + f"/s/{MID}/{TESTS}")
    page.wait_for_url(f"**/t/{MID}/**/{LEAD}")
    expect(panel).to_be_visible()
    expect(panel.get_by_role("tab", selected=True)).to_contain_text("补刷新失败的测试")
    ok(f"{tag}: an old link to a worker opens the lead with the panel on it")
    shot("old-link")
    if page.evaluate(OVERFLOW) > 0:
        fail(f"{tag}: the page scrolls sideways")
    if errors:
        fail(f"{tag}: page errors: {errors}")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--only", choices=list(SIZES), help="one width only")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            for size in [a.only] if a.only else SIZES:
                for theme in ("light", "dark"):
                    run(browser, a.base.rstrip("/"), a.token, size, theme, a.shots)
        except (AssertionError, PlaywrightError) as e:  # an expect() that failed or timed out: the verdict line too
            fail(str(e).strip().splitlines()[0] if str(e).strip() else type(e).__name__)
        finally:
            browser.close()
    print("PASS web team e2e")


if __name__ == "__main__":
    main()
