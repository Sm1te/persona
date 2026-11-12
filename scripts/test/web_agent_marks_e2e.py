#!/usr/bin/env python3
"""End-to-end check of the agent marks (#3391): the mark of each agent's program before its state dot in the sidebar's
rows, the session header, the /terminal tree, the palette's results and New agent's model panel (#5104: each launcher's
rows and the pill); a lettered tile for an agent with no mark of its own; a </> badge on the mark of one run from the
command line, none on the desktop app's (#6390). Light / dark x wide / 390 px.

Needs the fake hub with one agent of each kind, a web page against it and Playwright for Python:

    PORT=19050 MOCK_AGENT_MARKS=1 MOCK_TERMINAL_STREAM=1 MOCK_TERMINAL_CONTROL=1 MOCK_TERMINAL_PANES=1 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19050 npx vite --port 19051 --strictPort &)
    python3 scripts/test/web_agent_marks_e2e.py http://127.0.0.1:19051 [--shots <dir>] [--channel chrome]

Prints one line per check and a verdict; exits 1 on the first failure. --shots saves the screenshots.
"""
import argparse
import os
import sys

from playwright.sync_api import expect, sync_playwright

# title of the mock session -> (the mark's kind, its accessible name)
AGENTS = {
    "整理发布说明": ("claude", "Claude Code（命令行版）"),
    "修复订单列表的 N+1 查询": ("codex", "Codex（桌面版）"),
    "重构解析器的错误处理": ("opencode", "OpenCode"),
    "给导出脚本加上进度条": ("pi", "Pi"),
    "Gemini CLI": ("gemini", "Gemini CLI"),
    "补全设置页的单元测试": ("cursor", "Cursor"),
    "TRAEX 的一个会话": ("letter", "TRAEX"),
    "amp 的一个会话": ("letter", "amp"),
    "TRAE CLI 的一个会话": ("trae", "TRAE（命令行版）"),
}
TEMPLATES = ("codex", "opencode", "pi", "gemini", "cursor", "trae")
CLI = ("整理发布说明", "TRAE CLI 的一个会话")  # the ones the mock runs from the command line (#6390)


def ok(msg):
    print("ok  " + msg, flush=True)


def rows_of(page):
    """The marks workspace's rows (another mock session shares the first title)."""
    return {title: page.locator(f'aside a[href^="/s/server/marks-"][title^="{title}"]').first for title in AGENTS}


def check_sidebar(page, theme, where):
    """Every row has its mark, named, in a 14 px box, centred on its dot, before it and at the same x as the others; the
    command line's carry the </> badge inside the row, the others none."""
    rows = rows_of(page)
    xs, shades = [], {}
    for title, (kind, name) in AGENTS.items():
        row = rows[title]
        row.scroll_into_view_if_needed()
        mark = row.locator("[data-agent-mark]")
        expect(mark).to_have_count(1)
        assert mark.get_attribute("data-agent-mark") == kind, (title, mark.get_attribute("data-agent-mark"))
        assert mark.get_attribute("role") == "img" and mark.get_attribute("aria-label") == name, (title, name)
        box, dot = mark.bounding_box(), row.locator('[role="img"]:not([data-agent-mark])').first.bounding_box()
        assert round(box["width"]) == 14 and round(box["height"]) == 14, (title, box)
        assert abs((box["y"] + box["height"] / 2) - (dot["y"] + dot["height"] / 2)) <= 1.5, (title, box, dot)
        assert box["x"] + box["width"] < dot["x"], (title, "the mark sits before the state dot, not on it")
        badge = mark.locator("[data-agent-badge=cli]")
        expect(badge).to_have_count(1 if title in CLI else 0)
        if title in CLI:
            b, r = badge.bounding_box(), row.bounding_box()
            assert b["x"] + b["width"] < dot["x"] and b["y"] >= r["y"] and b["y"] + b["height"] <= r["y"] + r["height"], (title, b, dot, r)
        xs.append(round(box["x"], 1))
        if kind in TEMPLATES:
            shades[kind] = mark.locator("span").first.evaluate("e => getComputedStyle(e).backgroundColor")
    assert max(xs) - min(xs) <= 1, ("marks of one group start at the same x", xs)
    # the one-color marks follow the theme: dark on the light page, light on the dark one
    for kind, rgb in shades.items():
        r, g, b = [int(v) for v in rgb[rgb.index("(") + 1:rgb.index(")")].split(",")[:3]]
        lum = (0.299 * r + 0.587 * g + 0.114 * b) / 255
        assert (lum < 0.5) == (theme == "light"), (kind, rgb, theme)
    letters = rows["TRAEX 的一个会话"].locator("[data-agent-mark=letter]").inner_text()
    assert letters == "T", letters
    ok(f"{where} {theme}: {len(AGENTS)} rows, each mark named, 14 px, before its dot, one color follows the theme, letters for TRAEX and amp, </> on the command line's")


def settle(page):
    page.wait_for_timeout(500)


def run(browser, base, token, shots, channel_note):
    for width, height, touch in ((1280, 900, False), (390, 844, True)):
        where = "wide" if width > 600 else "390"
        for theme in ("light", "dark"):
            ctx = browser.new_context(viewport={"width": width, "height": height}, device_scale_factor=2, has_touch=touch, locale="zh-CN")
            page = ctx.new_page()
            page.set_default_timeout(10000)
            page.goto(base + "/overview?token=" + token)
            expect(page.locator("aside")).to_be_attached()
            page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
            page.reload()
            if width <= 600:
                page.locator("main").get_by_role("button", name="侧边栏").first.click()
            expect(page.locator('aside a[href="/s/server/marks-claude"]').first).to_be_visible()
            check_sidebar(page, theme, where)
            if shots:
                settle(page)
                rows_of(page)["整理发布说明"].scroll_into_view_if_needed()
                page.wait_for_timeout(300)
                page.locator("aside").first.screenshot(path=os.path.join(shots, f"sidebar-{where}-{theme}.png"))

            # the session header
            page.goto(base + "/s/server/marks-codex")
            header = page.locator("header").first
            expect(header.locator("[data-agent-mark=codex]")).to_have_attribute("aria-label", "Codex（桌面版）")
            expect(header.locator('[role="img"][aria-label="Codex（桌面版）"]')).to_have_count(1)
            expect(header.locator("[data-agent-badge]")).to_have_count(0)
            page.wait_for_timeout(600)
            box = header.locator("[data-agent-mark]").bounding_box()
            assert round(box["width"]) == 16, box
            ok(f"{where} {theme}: the session header names Codex, its desktop app, without a badge")
            if shots:
                page.screenshot(path=os.path.join(shots, f"header-{where}-{theme}.png"), clip={"x": 0, "y": 0, "width": width, "height": 70})

            if width > 600:
                # the /terminal tree: an agent's pane has its mark, a plain shell none
                page.goto(base + "/terminal")
                tree = page.locator("main")
                expect(tree.get_by_text("agent-3").first).to_be_attached()
                page.wait_for_timeout(1200)
                for title, (kind, name) in AGENTS.items():
                    row = tree.locator("a", has_text=title).first
                    expect(row.locator(f'[data-agent-mark="{kind}"]')).to_have_attribute("aria-label", name)
                shell = tree.locator("a", has_text="npm run dev").first
                expect(shell.locator("[data-agent-mark]")).to_have_count(0)
                ok(f"{where} {theme}: /terminal rows carry the marks, a plain shell none")
                if shots:
                    tree.locator("a", has_text="整理发布说明").first.scroll_into_view_if_needed()
                    page.wait_for_timeout(300)
                    page.screenshot(path=os.path.join(shots, f"terminal-{where}-{theme}.png"))

                # the palette
                page.goto(base + "/overview")
                page.wait_for_timeout(800)
                page.keyboard.press("ControlOrMeta+k")
                page.keyboard.type("agent-")
                page.wait_for_timeout(700)
                dialog = page.locator('[role="dialog"]').first
                got = dialog.locator("[data-agent-mark]").count()
                assert got >= 4, got
                ok(f"{where} {theme}: the palette's results carry the marks ({got})")
                if shots:
                    dialog.screenshot(path=os.path.join(shots, f"palette-{where}-{theme}.png"))
                page.keyboard.press("Escape")

                # the launcher picker: New agent's model panel (#5104; it was a row of launcher chips), one group per
                # launcher whose rows carry the mark of the agent it starts, and the pill the one that runs
                page.locator('aside button[aria-label="新建 Agent"]').first.click()
                dialog = page.get_by_role("dialog", name="新建 Agent", exact=True)
                pill = dialog.locator('[data-testid="new-agent-model"] [data-model-pill]')
                expect(pill).to_have_attribute("data-model-pill", "ready")
                expect(pill).to_be_enabled()
                expect(pill.locator('[data-pill-engine] [data-agent-mark="claude"]')).to_have_count(1)
                pill.click()
                picker = page.locator("[data-model-panel]")
                expect(picker).to_be_visible()
                expect(picker.locator('[role="status"]')).to_have_count(0)  # every launcher's list read
                kinds = picker.locator('[data-panel-section="provider"]').evaluate_all(
                    "els => els.map(s => s.querySelector('[data-panel-row] [data-row-setup] [data-agent-mark]')?.dataset.agentMark)")
                assert kinds == ["claude", "claude", "codex"], kinds
                ok(f"{where} {theme}: the launcher picker shows the agent each launcher starts: {kinds}")
                if shots:
                    page.screenshot(path=os.path.join(shots, f"launcher-{where}-{theme}.png"))
            ctx.close()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    ap.add_argument("--channel")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    with sync_playwright() as p:
        browser = p.chromium.launch(channel=a.channel) if a.channel else p.chromium.launch()
        try:
            run(browser, a.base.rstrip("/"), a.token, a.shots, a.channel)
        finally:
            browser.close()
    print("PASS web agent marks e2e")


if __name__ == "__main__":
    try:
        main()
    except AssertionError as e:
        print("FAIL", e)
        sys.exit(1)
