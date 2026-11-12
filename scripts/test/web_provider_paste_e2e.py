#!/usr/bin/env python3
"""End-to-end check of the provider wizard's two other ways in (#5193, docs/integrations/launchers.md): "Found on your
computers" lists the rc-file aliases not in Persona yet and opens the import for one of them (only that alias is
previewed), and "Paste a configuration" reads what was pasted by itself: two shell aliases (a choice between them, the
alias that is not Claude Code named), one on a stored provider's address with its key (add its models: the edit wizard
opens at "Choose models" with the new model checked and saves it), a curl line with another key (replace the key: the
edit wizard opens at "Connect" with it), a cindy provider JSON (a new provider, its display name and models filled in,
the endpoint left out named, saved), and text that is no configuration.

Needs the web page against the fake hub with MOCK_PROVIDERS=1 and Playwright for Python:

    MOCK_PROVIDERS=1 PORT=19011 python3 scripts/mock/mock_hub.py &
    (cd web && PERSONA_HUB_URL=http://127.0.0.1:19011 npx vite --port 5411 --strictPort &)
    uv run --no-project --with playwright==1.58.0 python3 scripts/test/web_provider_paste_e2e.py http://127.0.0.1:5411 \
        --hub http://127.0.0.1:19011 [--shots <dir>]

Run it against a fresh mock: it saves providers. Prints one line per check and a verdict; exits 1 on the first failure.
--shots saves 1280 / 390 wide, light and dark. English UI; drives the installed Chrome (channel="chrome").
"""
import argparse
import json
import os
import re
import sys
import urllib.request

from playwright.sync_api import expect, sync_playwright

SHELL = """# my gateways
alias gwcc='ANTHROPIC_BASE_URL=https://gw.example.com/v1 ANTHROPIC_AUTH_TOKEN=sk-example-gw ANTHROPIC_MODEL=example-haiku claude'
alias kimi='ANTHROPIC_BASE_URL=https://api.kimi.example.com/anthropic ANTHROPIC_AUTH_TOKEN=sk-example-kimi ANTHROPIC_MODEL=kimi-k3 DISABLE_TELEMETRY=1 RELAY_TOKEN=sk-example-relay claude'
alias ll='ls -l'
"""
CURL = ('curl https://gw.example.com/v1/messages -H "x-api-key: sk-example-new" -H "anthropic-version: 2023-06-01" '
        '-d \'{"model": "example-sonnet", "max_tokens": 16}\'')
CINDY = json.dumps({"kind": "custom", "name": "Example AI", "auth": {"method": "apiKey", "apiKey": "sk-example-cindy"},
                    "endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://api.example.invalid/anthropic",
                                   "models": [{"id": "example-claude", "name": "Example Claude", "contextWindow": 400000},
                                              "example-claude-mini"]},
                                  {"protocol": "openai-responses", "baseUrl": "https://api.example.invalid/v1"}]}, indent=2)


def ok(msg):
    print("ok  " + msg, flush=True)


class Run:
    def __init__(self, page, base, hub, token, shots):
        self.page, self.base, self.hub, self.token, self.shots = page, base, hub, token, shots

    def log(self):
        req = urllib.request.Request(self.hub + "/mock/providers/log", headers={"Authorization": "Bearer " + self.token})
        with urllib.request.urlopen(req, timeout=5) as r:
            return json.load(r)

    def shot(self, name):
        if not self.shots:
            return
        w = self.page.viewport_size["width"]
        for theme in ("light", "dark"):
            self.page.evaluate(f"document.documentElement.classList.toggle('dark', {'true' if theme == 'dark' else 'false'})")
            self.page.wait_for_timeout(350)
            self.page.screenshot(path=os.path.join(self.shots, f"paste-{name}-{w}-{theme}.png"))
        self.page.evaluate("document.documentElement.classList.remove('dark')")

    def open_models(self):
        self.page.goto(self.base + "/overview?token=" + self.token)
        self.page.locator("aside").first.wait_for()
        self.page.goto(self.base + "/settings?tab=models")
        expect(self.page.get_by_text("gw_full", exact=True).first).to_be_visible()

    def dialog(self, title):
        d = self.page.get_by_role("dialog", name=title)
        expect(d).to_be_visible()
        return d

    def add(self):
        self.page.get_by_role("button", name="Add provider").click()
        return self.dialog("Add provider")

    def paste(self, text):
        """Opens "Paste a configuration" and fills the box (typed: read 0.7 s after it stops changing)."""
        d = self.add()
        d.get_by_role("button", name="Paste a configuration").click()
        box = d.get_by_role("textbox", name="Paste a configuration")
        expect(box).to_be_focused()
        box.fill(text)
        return d


def found(r):
    d = r.add()
    expect(d.get_by_role("button", name="Paste a configuration")).to_be_visible()
    group = d.get_by_role("list", name="Found on your computers")
    expect(group).to_be_visible()
    # cc_glm is a provider already; the others are rc-file aliases with a gateway, none imported yet
    for alias in ("cc_full", "cc_kimi", "cc_relay"):
        expect(group.get_by_role("button", name=re.compile("^%s " % alias))).to_be_visible()
    r.shot("pick")
    ok("the first step lists the providers, then Paste a configuration, then the aliases found on the computers")
    group.get_by_role("button", name=re.compile("^cc_relay ")).click()
    imp = r.dialog("Import from your computer's aliases")
    expect(d).to_be_hidden()
    items = imp.get_by_role("list", name="Providers to import").get_by_role("checkbox")
    expect(items).to_have_count(1)
    expect(items.first).to_contain_text("cc_relay")
    r.shot("found-import")
    imp.get_by_role("button", name="Cancel").click()
    ok("a found alias opens the import for that alias alone")


def shell(r):
    d = r.paste(SHELL)
    drafts = d.get_by_role("radiogroup", name="Providers in the text")
    expect(drafts.get_by_role("radio")).to_have_count(2)
    expect(d.get_by_text("The alias ll doesn't start Claude Code, so it isn't used.")).to_be_visible()
    expect(d.get_by_text("Leaves out", exact=False).first).to_be_visible()
    cont = d.get_by_role("button", name="Continue")
    expect(cont).to_be_disabled()
    drafts.get_by_role("radio").first.click()
    action = d.get_by_role("radiogroup", name="What to do with it")
    expect(action.get_by_role("radio", name="Add its models to Team gateway")).to_have_attribute("aria-checked", "true")
    r.shot("shell")
    ok("two aliases are two drafts; the one on Team gateway's address with its key adds its models by default")
    log_before = len(r.log()["parse"])
    cont.click()
    edit = r.dialog("Edit Team gateway")
    expect(edit.get_by_role("button", name="Choose models", exact=False).first).to_have_attribute("aria-current", "step")
    expect(edit.get_by_text("example-haiku").first).to_be_visible()
    r.shot("merge")
    edit.get_by_role("button", name="Save").click()
    expect(edit).to_be_hidden()
    put = r.log()["put"]
    ids = [m["id"] for m in put.get("models", [])]
    assert "example-haiku" in ids and "example-opus" in ids, ids
    assert put.get("api_key", "") == "", put
    assert len(r.log()["parse"]) == log_before
    ok("Add its models opens Team gateway at Choose models and saves the new model with the old ones, key untouched")


def curl(r):
    d = r.paste(CURL)
    expect(d.get_by_text("Read as a curl command.")).to_be_visible()
    action = d.get_by_role("radiogroup", name="What to do with it")
    expect(action.get_by_role("radio", name="Add as a new provider")).to_have_attribute("aria-checked", "true")
    action.get_by_role("radio", name="Replace Team gateway's key").click()
    r.shot("curl")
    d.get_by_role("button", name="Continue").click()
    edit = r.dialog("Edit Team gateway")
    expect(edit.get_by_role("button", name="Connect", exact=False).first).to_have_attribute("aria-current", "step")
    expect(edit.get_by_label("API Key", exact=False).first).to_have_value("sk-example-new")
    expect(edit.get_by_text("Connected", exact=False).first).to_be_visible(timeout=15000)
    r.shot("replace-key")
    tests = r.log()["test"]
    assert tests and tests[-1].get("api_key") == "<set>" and tests[-1].get("name") == "gw_full", tests[-1:]
    edit.get_by_role("button", name="Cancel").click()
    ok("a curl line with another key replaces Team gateway's key: Connect opens with it and checks it")


def cindy(r):
    d = r.paste(CINDY)
    expect(d.get_by_text("Read as a provider's JSON.")).to_be_visible()
    expect(d.get_by_text("Example AI").first).to_be_visible()
    expect(d.get_by_text("Its openai-responses address", exact=False)).to_be_visible()
    r.shot("cindy")
    d.get_by_role("button", name="Continue").click()
    expect(d.get_by_label("Display name")).to_have_value("Example AI")
    expect(d.get_by_label("URL")).to_have_value("https://api.example.invalid/anthropic")
    d.get_by_role("button", name="Next").click()
    expect(d.get_by_text("example-claude-mini").first).to_be_visible()
    r.shot("cindy-models")
    d.get_by_role("button", name="Save").click()
    expect(d).to_be_hidden()
    post = r.log()["post"]
    assert post.get("display_name") == "Example AI" and post.get("api_key") == "<set>", post
    assert [m["id"] for m in post.get("models", [])] == ["example-claude", "example-claude-mini"], post.get("models")
    ok("a cindy provider fills a new provider (name, address, key, models) and saves it")


def unrecognized(r):
    d = r.paste("hello there\nnothing to see")
    expect(d.get_by_text("This doesn't look like a provider's settings.", exact=False)).to_be_visible()
    expect(d.get_by_role("button", name="Continue")).to_be_disabled()
    d.get_by_role("button", name="Back").click()
    expect(d.get_by_role("button", name="Paste a configuration")).to_be_visible()
    d.get_by_role("button", name="Cancel").click()
    assert all(set(e) == {"length"} for e in r.log()["parse"]), "the mock logged more than the text's length"
    ok("text that is no configuration says so, Continue stays off, Back returns to the list")


def phone(browser, base, hub, token, shots):
    ctx = browser.new_context(viewport={"width": 390, "height": 844}, device_scale_factor=2, has_touch=True, is_mobile=True,
                              locale="en-US")
    page = ctx.new_page()
    page.set_default_timeout(10000)
    r = Run(page, base, hub, token, shots)
    r.open_models()
    page.get_by_role("button", name="Add", exact=True).click()
    d = r.dialog("Add provider")
    expect(d.get_by_role("button", name="Paste a configuration")).to_be_visible()
    r.shot("pick")
    d.get_by_role("button", name="Paste a configuration").click()
    d.get_by_role("textbox", name="Paste a configuration").fill(SHELL)
    expect(d.get_by_role("radiogroup", name="Providers in the text").get_by_role("radio")).to_have_count(2)
    d.get_by_role("radiogroup", name="Providers in the text").get_by_role("radio").nth(1).click()
    r.shot("shell")
    width = page.evaluate("document.documentElement.scrollWidth")
    assert width <= 390, f"the page scrolls sideways at 390 px ({width})"
    ctx.close()
    ok("at phone width the first step and the paste view fit")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("--hub", default="http://127.0.0.1:19011")
    ap.add_argument("--shots")
    ap.add_argument("--token", default="mock")
    a = ap.parse_args()
    if a.shots:
        os.makedirs(a.shots, exist_ok=True)
    base, hub = a.base.rstrip("/"), a.hub.rstrip("/")
    with sync_playwright() as p:
        browser = p.chromium.launch(channel="chrome")
        try:
            ctx = browser.new_context(viewport={"width": 1280, "height": 860}, device_scale_factor=2, locale="en-US")
            page = ctx.new_page()
            page.set_default_timeout(10000)
            r = Run(page, base, hub, a.token, a.shots)
            r.open_models()
            found(r)
            shell(r)
            curl(r)
            cindy(r)
            unrecognized(r)
            ctx.close()
            phone(browser, base, hub, a.token, a.shots)
        except Exception as e:
            print("FAIL", e, flush=True)
            sys.exit(1)
        finally:
            browser.close()
    print("PASS web provider paste e2e")


if __name__ == "__main__":
    main()
