#!/usr/bin/env python3
"""End-to-end check of the launcher defaults and the main chat's "Runtime" in the web (#2455): model config's main chat offers
Claude Code CLI / Agent SDK, a switch is saved and still there after a reload; "New agent"'s model pill and panel (#5104)
start at "Follow Model settings" with "<provider> · <model>" from the hub's `default`, and a launcher picked over it
unchecks it (the pill names that launcher) until the follow row is picked again; the conversation's launcher menu opens
with "Default: <provider> · <model>", and a launcher picked over it says "This time only"; the main conversation that fell
back to the CLI says so above the composer.

Needs a running web page against the fake hub with MOCK_MODEL_DEFAULTS=1 and Playwright for Python (it drives the
installed Chrome):

    MOCK_MODEL_DEFAULTS=1 PORT=19012 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19012 npx vite --port 5412 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_model_defaults_e2e.py http://127.0.0.1:5412 [--shots <dir>]

Prints one line per check and a verdict; exits 1 on the first failure. Runs light and dark, at desktop and phone
widths; --shots saves each state there.
"""
import argparse
import os
import re
import sys

from playwright.sync_api import expect, sync_playwright

SIZES = {"wide": {"width": 1280, "height": 900}, "narrow": {"width": 390, "height": 844}}
MAIN = "b2e4d6f8-0000-4000-8000-000000000001"  # the fake hub's main conversation
DEFAULT = "默认：claude · model-chat"  # the fake hub's model config default for the main chat and "New agent"
FOLLOWED = "claude · model-chat"  # the same default under New agent's "Follow Model settings" row
FALLBACK = "已退回 CLI：这台电脑没有 Node.js 22+"


def ok(msg):
    print("ok  " + msg, flush=True)


def fail(msg):
    print("FAIL " + msg, flush=True)
    sys.exit(1)


class Page:
    def __init__(self, browser, base, token, size, theme, shots):
        self.base, self.tag, self.size, self.theme, self.shots = base, f"{size}/{theme}", size, theme, shots
        self.ctx = browser.new_context(viewport=SIZES[size], device_scale_factor=2, has_touch=size == "narrow",
                                       color_scheme=theme, reduced_motion="reduce")
        self.page = self.ctx.new_page()
        self.page.set_default_timeout(15000)
        self.patches = []
        self.page.on("request", lambda r: r.method == "PATCH" and "/model-config/purposes/assistant" in r.url and self.patches.append(r.post_data_json))
        self.page.goto(base + "/overview?token=" + token)
        self.page.wait_for_function("localStorage.getItem('persona.token')")
        self.page.evaluate(f"localStorage.setItem('persona.theme', '{theme}')")
        self.page.locator("aside").first.wait_for(state="attached")

    def go(self, path):
        self.page.goto(self.base + path)

    def shot(self, name):
        if self.shots:
            self.page.wait_for_timeout(300)
            self.page.screenshot(path=os.path.join(self.shots, f"defaults-{self.size}-{self.theme}-{name}.png"))

    def close(self):
        self.ctx.close()


def runtime(p):
    def editor():
        p.go("/settings?tab=model-config&purpose=assistant")
        d = p.page.get_by_role("dialog")
        expect(d.get_by_role("radiogroup", name="运行方式")).to_be_visible()
        return d

    d = editor()
    expect(d.get_by_role("radio", name="Claude Code CLI")).to_have_attribute("aria-checked", "true")
    expect(d.get_by_text("Agent SDK 需要那台电脑有 Node 22+，缺了会自动用 CLI")).to_be_visible()
    d.get_by_role("radio", name="Agent SDK").click()
    expect(d.get_by_role("radio", name="Agent SDK")).to_have_attribute("aria-checked", "true")
    p.shot("runtime-edit")
    d.get_by_role("button", name="保存").click()
    expect(d).to_have_count(0)
    if not p.patches or p.patches[-1].get("runtime") != "agentsdk":
        fail(f"{p.tag}: the save did not send runtime agentsdk: {p.patches}")
    ok(f"{p.tag}: 运行方式 offers CLI and Agent SDK with the Node 22+ note; saving sends runtime agentsdk")

    p.page.reload()
    d = editor()
    expect(d.get_by_role("radio", name="Agent SDK")).to_have_attribute("aria-checked", "true")
    expect(d.get_by_text("Agent SDK", exact=True).first).to_be_visible()
    ok(f"{p.tag}: after a reload 主对话 still runs on the Agent SDK")
    d.get_by_role("radio", name="Claude Code CLI").click()  # back as it was for the next round
    d.get_by_role("button", name="保存").click()
    expect(d).to_have_count(0)
    if p.patches[-1].get("runtime") != "cli":
        fail(f"{p.tag}: switching back did not send runtime cli: {p.patches[-1]}")


def new_agent(p):
    """New agent's launcher chips (Default marked, another one "This time only") are its model pill and panel now (#5104):
    the follow row stands for the default, and a launcher picked over it unchecks it."""
    p.go("/agents")
    p.page.get_by_role("button", name="新建 Agent").first.click()
    d = p.page.get_by_role("dialog", name="新建 Agent", exact=True)
    pill = d.locator('[data-testid="new-agent-model"] [data-model-pill]')
    expect(pill).to_have_attribute("data-model-pill", "ready")
    expect(pill).to_be_enabled()
    expect(pill).to_contain_text("model-chat")

    def panel():
        pill.click()
        panel = p.page.locator("[data-model-panel]")
        expect(panel).to_be_visible()
        expect(panel.locator('[role="status"]')).to_have_count(0)  # every launcher's list read
        return panel

    def pick(row):
        # by its name: the middle of a row can be its star or Customize (always shown on a touch screen)
        row.click(position={"x": 48, "y": 12})
        expect(p.page.locator("[data-model-panel]")).to_have_count(0)

    m = panel()
    follow = m.locator("[data-follow-row]")
    expect(follow).to_have_attribute("aria-selected", "true")
    expect(follow).to_contain_text("跟随模型配置")
    expect(follow).to_contain_text(FOLLOWED)
    ok(f"{p.tag}: 新建 Agent's model panel starts at 「跟随模型配置」 with 「{FOLLOWED}」")
    p.shot("new-agent-default")
    pick(m.locator('[data-panel-row="model:sonnetcc\\a default"]'))
    expect(pill).to_have_attribute("title", re.compile(r"· sonnetcc"))
    m = panel()
    expect(m.locator("[data-follow-row]")).to_have_attribute("aria-selected", "false")
    expect(m.locator('[data-panel-row="model:sonnetcc\\a default"]')).to_have_attribute("data-current", "true")
    ok(f"{p.tag}: another launcher picked unchecks 「跟随模型配置」 and is the one lit, the pill names it")
    p.shot("new-agent-once")
    m.locator("[data-follow-row]").click()
    expect(p.page.locator("[data-model-panel]")).to_have_count(0)
    expect(pill).to_contain_text("model-chat")
    expect(pill).not_to_have_attribute("title", re.compile(r"sonnetcc"))
    p.page.keyboard.press("Escape")
    expect(d).to_have_count(0)


def conversation(p):
    p.go(f"/persona/{MAIN}")
    note = p.page.get_by_role("status").filter(has_text=FALLBACK)
    expect(note).to_be_visible()
    ok(f"{p.tag}: the main conversation says 「{FALLBACK}」 above the composer")
    chip = p.page.locator("button[title='启动器：下一条消息起使用']")
    chip.click()
    menu = p.page.get_by_role("menu")
    expect(menu.get_by_text(DEFAULT)).to_be_visible()
    ok(f"{p.tag}: the conversation's launcher menu starts at 「{DEFAULT}」")
    p.shot("chat-menu")
    menu.get_by_text("sonnetcc", exact=True).click()
    p.page.keyboard.press("Escape")
    expect(chip).to_contain_text("仅这一次")
    ok(f"{p.tag}: a launcher picked in the conversation is marked 仅这一次")
    p.shot("chat-once")
    chip.click()
    p.page.get_by_role("menu").get_by_text(DEFAULT).click()
    expect(chip).not_to_contain_text("仅这一次")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    base = a.base.rstrip("/")
    with sync_playwright() as pw:
        browser = pw.chromium.launch(channel="chrome")
        try:
            for size in SIZES:
                for theme in ("light", "dark"):
                    p = Page(browser, base, a.token, size, theme, a.shots)
                    try:
                        for case in (runtime, new_agent, conversation):
                            case(p)
                    finally:
                        p.close()
        finally:
            browser.close()
    print("PASS web model defaults e2e")


if __name__ == "__main__":
    main()
