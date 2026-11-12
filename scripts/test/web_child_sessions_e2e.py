#!/usr/bin/env python3
"""End-to-end check of child sessions folded into their parent on the web (#5120, after cindy). On the Paseo computer of
`MOCK_CHILD_SESSIONS=1`, the lead "Rework the login flow" created four Agents (one waits for you, one works with a Codex
subagent of its own, one is done, one failed): none of them is a sidebar row; the lead's row needs you (its waiting
child), says what that child waits for and how many subagents run; an Agent whose creator is not listed stays a row.
The lead's chat draws one subagent card per child inside the second exchange's work summary (Running / Completed /
Failed, the harness and the model chip), a card opens in place to what it was asked, its words and its last tool, and
the waiting child's approval is on the lead's page, "From subagent · …", answerable there. An old link to a child opens
the lead with that child's card open. With `--paged` the hub pages (`MOCK_SESSIONS_LIST=1`, `sessions_hierarchy`): every
read of the Agents list asks for roots. Light / dark × 1280 / 390.

    MOCK_CHILD_SESSIONS=1 [MOCK_SESSIONS_LIST=1] PORT=18921 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:18921 npx vite --port 5192 --strictPort &)
    python3 scripts/test/web_child_sessions_e2e.py http://127.0.0.1:5192 [--paged] [--shots <dir>]

Prints one line per check and a verdict (also when a wait fails or times out); exits 1 on the first failure.
"""
import argparse
import os
import sys

from playwright.sync_api import Error as PlaywrightError, expect, sync_playwright

MID = "server"
LEAD = "9a5e0000-0000-4000-8000-000000000002"
CHILDREN = {  # session id: the status its card shows
    "paseo-9a5e0000-0000-4000-8000-000000000003": "running",  # waits for you
    "paseo-9a5e0000-0000-4000-8000-000000000004": "running",  # Codex, working
    "019a5e00-0000-7000-8000-000000000005": "running",  # its Codex subagent
    "paseo-9a5e0000-0000-4000-8000-000000000007": "completed",
    "paseo-9a5e0000-0000-4000-8000-000000000008": "failed",
}
ORPHAN = "paseo-9a5e0000-0000-4000-8000-000000000009"
DONE = "paseo-9a5e0000-0000-4000-8000-000000000007"
FAILED = "paseo-9a5e0000-0000-4000-8000-000000000008"
GRANDCHILD = "019a5e00-0000-7000-8000-000000000005"
SIZES = {"wide": {"width": 1280, "height": 860}, "narrow": {"width": 390, "height": 844}}
OVERFLOW = "() => document.documentElement.scrollWidth - document.documentElement.clientWidth"


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


def row(page, sid):
    return page.locator(f'a[href$="/s/{MID}/{sid}"]')


def run(browser, base, token, size, theme, shots, paged):
    tag = f"{size}/{theme}{'/paged' if paged else ''}"
    ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow", color_scheme=theme, locale="zh-CN")
    page = ctx.new_page()
    page.set_default_timeout(15000)
    errors, reads = [], []
    page.on("pageerror", lambda e: errors.append(str(e)))
    page.on("request", lambda r: reads.append(r.url) if "/api/v1/sessions?" in r.url else None)
    page.goto(base + "/overview?token=" + token)
    page.wait_for_load_state("networkidle")
    page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
    prefix = f"child-sessions-{size}-{theme}{'-paged' if paged else ''}"

    def shot(name, el=None):
        if shots:
            (el or page).screenshot(path=os.path.join(shots, f"{prefix}-{name}.png"))

    # the list: on the phone the Agents page, on a computer the sidebar
    page.goto(base + ("/agents" if size == "narrow" else f"/s/{MID}/{LEAD}"))
    lead = row(page, LEAD).first
    expect(lead).to_be_visible()
    for sid in CHILDREN:
        if row(page, sid).count():
            fail(f"{tag}: the child {sid} is a row of its own")
    expect(row(page, ORPHAN).first).to_be_visible()
    ok(f"{tag}: no child is a row; the lead is, and so is the Agent whose creator is not listed")
    expect(lead.locator("[data-children-line]")).to_contain_text("子 Agent 2 个在跑")
    expect(lead).to_contain_text("npm run migrate")
    ok(f"{tag}: the lead's row says what its waiting child asks and how many subagents run")
    if paged:
        # the bounded list's pages and counts (not an exact lookup, not the complete copy pins and groupings read)
        lists = [u for u in reads if "session_id=" not in u and ("limit=50" in u or "counts=1" in u)]
        if not lists or any("roots=1" not in u for u in lists):
            fail(f"{tag}: every Agents list read should ask for roots: {lists}")
        ok(f"{tag}: every Agents list page and count asks for roots ({len(lists)})")
    shot("list")

    # the lead's chat: one card per child in the second exchange's work summary
    if size == "narrow":
        page.goto(base + f"/s/{MID}/{LEAD}")
    main = page.locator("main")
    seg = main.locator("[data-segment]", has_text="Have agents add the tests")
    expect(seg).to_be_visible()
    cards = seg.locator("[data-child-card]")
    if cards.count() == 0:
        seg.locator('[data-work-group] > button[aria-expanded="false"]').first.click()
    expect(cards).to_have_count(len(CHILDREN))
    if paged and not any(f"parent_session_id={LEAD}" in u for u in reads):
        fail(f"{tag}: the lead's chat should read its children with parent_session_id")
    if not cards.first.evaluate("(el) => Boolean(el.closest('[data-work-group]'))"):
        fail(f"{tag}: a child's card should sit inside its exchange's work summary")
    for sid, status in CHILDREN.items():
        card = seg.locator(f'[data-tool-use="child:{sid}"]')
        expect(card).to_have_attribute("data-child-card", status)
    failed = seg.locator(f'[data-tool-use="child:{FAILED}"] [data-task-status]')
    if "text-red" not in (failed.get_attribute("class") or ""):
        fail(f"{tag}: only a failed card's status is red")
    done = seg.locator(f'[data-tool-use="child:{DONE}"]')
    expect(done.locator("[data-task-meta]")).to_contain_text("Claude Code")
    expect(done.locator("[data-task-model]")).to_contain_text("Sonnet 4.5")
    ok(f"{tag}: five cards in the exchange's work summary, Running / Completed / Failed, harness and model chip")
    done.evaluate("(e) => e.scrollIntoView({ block: 'center' })")
    shot("cards")
    done.locator("button").first.click()
    details = done.locator("[data-subagent-details]")
    expect(details.locator("[data-subagent-prompt]")).to_contain_text("Write regression tests")
    expect(details).to_contain_text("all pass")
    expect(details.locator("[data-subagent-last-tool]")).to_be_visible()
    ok(f"{tag}: a card opens in place to its prompt, its words and its last tool")
    shot("card-open", done)

    # the waiting child's approval on the lead's page
    asks = main.get_by_role("group").filter(has=page.locator("[data-permission-from]")).first
    expect(asks).to_be_visible()
    expect(asks.locator("[data-permission-from]").first).to_contain_text("Add a remember-me option")
    expect(asks).to_contain_text("npm run migrate")
    if asks.evaluate("(el) => Boolean(el.closest('[data-work-group]'))"):
        fail(f"{tag}: the approval sits in the pending area, not in the work summary")
    ok(f"{tag}: the waiting child's approval is on the lead's page, from that subagent")
    asks.evaluate("(e) => e.scrollIntoView({ block: 'center' })")
    shot("request")
    if page.evaluate(OVERFLOW) > 0:
        fail(f"{tag}: the page scrolls sideways")

    # an old link to a child: the lead, its card open
    page.goto(base + f"/s/{MID}/{GRANDCHILD}")
    page.wait_for_url(f"**/s/{MID}/{LEAD}**")
    card = page.locator(f'main [data-tool-use="child:{GRANDCHILD}"]')
    expect(card).to_be_visible()
    expect(card.locator("[data-subagent-details]")).to_be_visible()
    ok(f"{tag}: an old link to a child opens the lead with its card open")
    shot("old-link")
    if errors:
        fail(f"{tag}: page errors: {errors}")
    ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--paged", action="store_true", help="the hub pages (MOCK_SESSIONS_LIST=1)")
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
                    run(browser, a.base.rstrip("/"), a.token, size, theme, a.shots, a.paged)
        except (AssertionError, PlaywrightError) as e:  # an expect() that failed or timed out: the verdict line too
            fail(str(e).strip().splitlines()[0] if str(e).strip() else type(e).__name__)
        finally:
            browser.close()
    print("PASS web child sessions e2e")


if __name__ == "__main__":
    main()
