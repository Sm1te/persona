"""mock_hub.py's alias import (#5024, docs/protocol/protocol.md): the preview names what will really be created
(`create_as`: an alias Persona already lists is saved as "<alias>-2"), the POST saves under those names, honours `split`,
`hide_aliases` and `alias` (#5193), and refuses a name it did not preview. The parse route (#5193) reads each pasted
format into drafts with their matches and notes, and logs only the text's length. Each case runs its own mock hub on a
free port; the parse cases share one, as parsing changes nothing."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

HERE = Path(__file__).resolve().parent


class Hub:
    def __init__(self, **env):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        self.base = "http://127.0.0.1:%d" % port
        self.log = tempfile.TemporaryFile()
        clean = {k: v for k, v in os.environ.items() if not k.startswith("MOCK_")}
        self.process = subprocess.Popen(["python3", "-I", str(HERE / "mock_hub.py")],
                                        env=dict(clean, PORT=str(port), **env), stdout=self.log, stderr=self.log)
        for _ in range(100):
            if self.process.poll() is not None:
                self.log.seek(0)
                out = self.log.read().decode(errors="replace")[-2000:]
                self.log.close()
                raise RuntimeError("mock hub exited: %s" % out)
            try:
                with urlopen(self.base + "/api/v1/health", timeout=1):
                    return
            except (URLError, TimeoutError, ConnectionError):
                time.sleep(0.1)
        self.stop()
        raise RuntimeError("mock hub startup timed out")

    def stop(self):
        self.process.terminate()
        try:
            self.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.process.kill()
            self.process.wait(timeout=5)
        self.log.close()

    def call(self, method, path, body=None):
        req = Request(self.base + path, data=None if body is None else json.dumps(body).encode(), method=method,
                      headers={"Content-Type": "application/json", "Authorization": "Bearer mock"})
        try:
            with urlopen(req, timeout=5) as r:
                return r.status, json.load(r)
        except HTTPError as e:
            with e:
                return e.code, json.load(e)


def hub(test, **env):
    h = Hub(**env)
    test.addCleanup(h.stop)
    return h


def by_name(preview):
    return {g["name"]: g for g in preview["providers"]}


class ProviderImportMockTests(unittest.TestCase):
    def test_a_listed_alias_previews_and_saves_as_dash_two(self):
        h = hub(self, MOCK_PROVIDERS="1")
        _, preview = h.call("GET", "/api/v1/launchers/import?machine=macbook")
        groups = by_name(preview)
        # cc_glm is a launcher Persona lists, so its provider can't take the name
        self.assertEqual(groups["cc_glm"]["create_as"], "cc_glm-2")
        self.assertEqual([v.get("create_as") for v in groups["cc_kimi"]["variants"]], ["cc_kimi-2", "cc_kimi_fast-2"])
        # one that merges is not created, so it has no name of its own
        self.assertEqual(groups["cc_full"]["existing"], "gw_full")
        self.assertNotIn("create_as", groups["cc_full"])
        status, out = h.call("POST", "/api/v1/launchers/import",
                             {"machine": "macbook", "providers": ["cc_glm", "cc_kimi"], "split": ["cc_kimi"], "hide_aliases": True})
        self.assertEqual(status, 200)
        self.assertEqual(out["created"], ["cc_glm-2", "cc_kimi-2", "cc_kimi_fast-2"])
        self.assertEqual(out["hidden"], ["cc_glm", "cc_kimi"])
        self.assertEqual(out["kept"], [{"alias": "cc_kimi_fast", "hide": "in_use", "uses": ["主对话"]}])
        # the next preview lands the same aliases in what was just saved, and the hidden ones have nothing left to hide
        groups = by_name(h.call("GET", "/api/v1/launchers/import?machine=macbook")[1])
        self.assertEqual(groups["cc_glm"]["existing"], "cc_glm-2")
        self.assertNotIn("create_as", groups["cc_glm"])
        self.assertEqual(groups["cc_glm"]["sources"][0]["hide"], "none")

    def test_a_plain_mock_answers_the_preview_with_the_names_it_keeps(self):
        # without MOCK_PROVIDERS the import aliases are not listed (cc_glm is), and nothing is there to merge into
        h = hub(self)
        status, preview = h.call("GET", "/api/v1/launchers/import?machine=macbook")
        self.assertEqual(status, 200)
        groups = by_name(preview)
        self.assertNotIn("create_as", groups["cc_kimi"])
        self.assertNotIn("existing", groups["cc_full"])
        self.assertEqual(groups["cc_glm"]["create_as"], "cc_glm-2")
        self.assertEqual(h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "providers": ["cc_kimi"]})[1]["created"], ["cc_kimi"])
        # and the parse route, with no managed provider to match
        out = h.call("POST", "/api/v1/launchers/import/parse", {"text": "ANTHROPIC_BASE_URL=https://gw.example.com claude"})[1]
        self.assertEqual((out["format"], out["drafts"][0]["matches"]), ("shell", []))

    def test_refusals(self):
        h = hub(self, MOCK_PROVIDERS="1")
        self.assertEqual(h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "providers": ["nope"]})[0], 400)
        self.assertEqual(h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "providers": ["cc_glm"], "split": ["cc_kimi"]})[0], 400)
        self.assertEqual(h.call("GET", "/api/v1/launchers/import?machine=studio")[1]["code"], "node_outdated")
        self.assertEqual(h.call("GET", "/api/v1/launchers/import?machine=nas")[0], 503)

    def test_one_alias(self):
        h = hub(self, MOCK_PROVIDERS="1")
        one = lambda alias: h.call("GET", "/api/v1/launchers/import?machine=macbook&alias=" + alias)[1]["providers"]
        # an alias out of a group of several is its own provider; one that stands alone is its group
        fast, = one("cc_kimi_fast")
        self.assertEqual((fast["name"], fast["aliases"], fast["model"], fast["create_as"]), ("cc_kimi_fast", ["cc_kimi_fast"], "kimi-k3-turbo", "cc_kimi_fast-2"))
        self.assertNotIn("variants", fast)
        self.assertEqual(one("cc_full_haiku")[0]["existing"], "gw_full")
        self.assertEqual([g["name"] for g in one("cc_relay")], ["cc_relay"])
        self.assertEqual(one("nope"), [])
        # the POST reads only that alias too: another one's provider is not there to import
        status, out = h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "alias": "cc_kimi_fast", "providers": ["cc_kimi_fast"]})
        self.assertEqual((status, out["created"]), (200, ["cc_kimi_fast-2"]))
        self.assertEqual(h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "alias": "cc_kimi_fast", "providers": ["cc_kimi"]})[0], 400)


SHELL = """# gateways
export HTTPS_PROXY=http://127.0.0.1:7890 ANTHROPIC_MODEL=example-sonnet
alias gwcc='ANTHROPIC_BASE_URL=https://gw.example.com/v1 ANTHROPIC_AUTH_TOKEN=sk-example-gw ANTHROPIC_MODEL="example-opus[1m]" claude'
alias gwcc_haiku='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=sk-example-gw ANTHROPIC_MODEL=example-haiku claude'
alias cc_glm="ANTHROPIC_BASE_URL=https://glm.example.com/api/anthropic ANTHROPIC_API_KEY=sk-example-glm HOME=/tmp RELAY_TOKEN=x \\
  DISABLE_TELEMETRY=1 NOTE='a b' claude --dangerously-skip-permissions"
alias glm_low='ANTHROPIC_BASE_URL=https://glm.example.com/api/anthropic ANTHROPIC_API_KEY=sk-example-glm CLAUDE_CODE_EFFORT_LEVEL=low claude'
alias ll='ls -la'
alias plain=claude
source ~/.secrets
"""


class ProviderPasteMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.h = Hub(MOCK_PROVIDERS="1")
        cls.addClassCleanup(cls.h.stop)

    def parse(self, text):
        status, out = self.h.call("POST", "/api/v1/launchers/import/parse", {"text": text})
        self.assertEqual(status, 200, out)
        return out

    def test_the_hub_lists_the_feature(self):
        self.assertIn("launcher_import_parse", self.h.call("GET", "/api/v1/health")[1]["features"])

    def test_shell_aliases(self):
        out = self.parse(SHELL)
        self.assertEqual(out["format"], "shell")
        gw, glm, low = out["drafts"]
        # two aliases on one address and key differing only in the model: one draft with both models, the alias's own
        # model over the exported one, /v1 dropped, [1m] as the context window, the exported proxy
        self.assertEqual(gw["aliases"], ["gwcc", "gwcc_haiku"])
        self.assertEqual(gw["label"], "gwcc, gwcc_haiku")
        self.assertEqual(gw["provider"], {
            "kind": "launcher", "agent": "claude", "api": "messages", "name": "gwcc", "base_url": "https://gw.example.com",
            "auth": "bearer", "api_key": "sk-example-gw", "model": "example-opus", "proxy": "http://127.0.0.1:7890",
            "models": [{"id": "example-opus", "context_window": 1000000}, {"id": "example-haiku"}]})
        self.assertEqual(gw["matches"], [{"name": "gw_full", "display_name": "Team gateway", "same_key": True}])
        # one key, but the effort differs: each alias a draft of its own; cc_glm is listed already, so its name is -2
        self.assertEqual((glm["provider"]["name"], low["provider"]["name"]), ("cc_glm-2", "glm_low"))
        self.assertEqual(low["provider"]["effort"], "low")
        self.assertEqual(glm["provider"]["args"], ["--dangerously-skip-permissions"])
        self.assertEqual(glm["provider"]["env"], 'DISABLE_TELEMETRY=1\nNOTE="a b"')
        self.assertEqual(glm["env_names"], ["DISABLE_TELEMETRY", "NOTE"])
        self.assertEqual(glm["skipped_env"], [{"name": "HOME", "reason": "computer"}, {"name": "RELAY_TOKEN", "reason": "credential"}])
        self.assertEqual(glm["matches"], [])
        self.assertEqual(out["notes"], [{"code": "not_claude", "name": "ll"}, {"code": "no_gateway", "name": "plain"},
                                        {"code": "not_claude", "line": 10}])

    def test_shell_variables_alone(self):
        out = self.parse("ANTHROPIC_BASE_URL=\"https://gw.example.com\"\nexport ANTHROPIC_AUTH_TOKEN=$GW_TOKEN # set by the profile\n"
                         "ANTHROPIC_CUSTOM_HEADERS='X-Team: research'\nANTHROPIC_DEFAULT_HAIKU_MODEL=example-haiku\n")
        d, = out["drafts"]
        self.assertNotIn("label", d)
        self.assertNotIn("name", d["provider"])
        self.assertNotIn("api_key", d["provider"])
        self.assertEqual((d["provider"]["headers"], d["provider"]["haiku_model"]), ("X-Team: research", "example-haiku"))
        self.assertEqual(d["notes"], [{"code": "key_shell"}])
        self.assertEqual(d["matches"], [{"name": "gw_full", "display_name": "Team gateway", "same_key": False}])
        # a claude line takes the variables before it and its own
        d, = self.parse("export ANTHROPIC_BASE_URL=https://relay.example.org\nANTHROPIC_API_KEY=sk-example-r claude")["drafts"]
        self.assertEqual((d["provider"]["auth"], d["provider"]["api_key"], d["notes"]), ("api_key", "sk-example-r", []))

    def test_addresses_without_a_draft(self):
        cases = {"alias a='ANTHROPIC_BASE_URL=$GW claude'": [{"code": "url_shell", "name": "a"}],
                 "ANTHROPIC_BASE_URL=https://me:pw@gw.example.com claude": [{"code": "url_user", "line": 1}],
                 "export ANTHROPIC_MODEL=example-opus": [{"code": "no_gateway"}]}
        for text, notes in cases.items():
            out = self.parse(text)
            self.assertEqual((out["format"], out["drafts"], out["notes"]), ("shell", [], notes), text)
        # as the hub: no alias, claude command or variable of Claude Code's is nothing it recognizes
        for text in ("export FOO=1", "\n\nK=1 npx something"):
            self.assertEqual(self.parse(text), {"format": "", "drafts": [], "notes": [{"code": "unrecognized"}]}, text)

    def test_settings_json(self):
        out = self.parse('{"env": {"ANTHROPIC_BASE_URL": "https://gw.example.com/v1", "MAX": 3, "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "32000"},'
                         ' "model": "example-opus", "apiKeyHelper": "~/bin/key.sh"}')
        self.assertEqual(out["format"], "settings")
        d, = out["drafts"]
        self.assertEqual((d["provider"]["base_url"], d["provider"]["model"]), ("https://gw.example.com", "example-opus"))
        self.assertEqual(d["env_names"], ["CLAUDE_CODE_MAX_OUTPUT_TOKENS"])
        self.assertEqual(d["notes"], [{"code": "key_helper"}])
        self.assertEqual(self.parse('{"model": "example-opus"}')["notes"], [{"code": "no_gateway"}])

    def test_curl(self):
        out = self.parse("curl -sS https://gw.example.com/v1/messages?beta=true \\\n  -H 'x-api-key: sk-example-other' \\\n"
                         "  -H 'anthropic-version: 2023-06-01' -H 'X-Team: research' -H 'X-Org-Token: abc' -u me:pw -x http://127.0.0.1:7890 \\\n"
                         "  -d '{\"model\": \"example-sonnet\", \"max_tokens\": 16}'")
        self.assertEqual(out["format"], "curl")
        d, = out["drafts"]
        self.assertEqual({k: d["provider"][k] for k in ("kind", "base_url", "auth", "api_key", "model", "headers", "proxy")},
                         {"kind": "launcher", "base_url": "https://gw.example.com", "auth": "api_key", "api_key": "sk-example-other",
                          "model": "example-sonnet", "headers": "X-Team: research", "proxy": "http://127.0.0.1:7890"})
        self.assertEqual(d["skipped_headers"], [{"name": "anthropic-version", "reason": "protocol"}, {"name": "X-Org-Token", "reason": "credential"}])
        self.assertEqual(d["notes"], [{"code": "curl_option", "name": "-u"}])
        self.assertEqual(d["matches"], [{"name": "gw_full", "display_name": "Team gateway", "same_key": False}])
        # a direct API: the address keeps its /v1, it takes no headers and no proxy
        d, = self.parse("curl https://api.example.com/v1/chat/completions -H 'Authorization: Bearer sk-example-c' -H 'X-Team: a' "
                        "--noproxy localhost")["drafts"]
        self.assertEqual({k: d["provider"].get(k) for k in ("kind", "api", "base_url", "auth", "headers", "no_proxy")},
                         {"kind": "api", "api": "chat", "base_url": "https://api.example.com/v1", "auth": "bearer", "headers": None, "no_proxy": None})
        self.assertEqual(d["skipped_headers"], [{"name": "X-Team", "reason": "unsupported"}])
        self.assertEqual(d["notes"], [{"code": "curl_option", "name": "--noproxy"}])
        d, = self.parse("curl https://gw.example.com/custom/path")["drafts"]
        self.assertEqual(d["provider"]["base_url"], "https://gw.example.com/custom/path")
        self.assertEqual(d["notes"], [{"code": "curl_path", "name": "/custom/path"}, {"code": "no_key"}])

    def test_cindy(self):
        out = self.parse(json.dumps({"providers": [
            {"kind": "custom", "name": "Example AI", "auth": {"method": "apiKey", "apiKey": "sk-example-cindy"}, "endpoints": [
                {"protocol": "openai-responses", "baseUrl": "https://api.example.invalid/v1"},
                {"protocol": "anthropic-messages", "baseUrl": "https://api.example.invalid/anthropic/v1", "headers": {"X-Tenant": "t"},
                 "models": [{"id": "example-claude", "contextWindow": 400000, "reasoningEfforts": ["low", "ultra"], "reasoningDefaultEffort": "ultra"}, "mini"]}]},
            {"kind": "custom", "name": "Responses", "auth": {"method": "apiKey", "apiKey": "sk-example-r"}, "models": ["example-codex"],
             "endpoints": [{"protocol": "openai-responses", "baseUrl": "https://api.example.invalid/v1/"}]},
            {"kind": "custom", "name": "Local", "auth": {"method": "none"}, "endpoints": [{"protocol": "openai-chat", "baseUrl": "http://127.0.0.1:4000/v1"}]},
            {"kind": "custom", "auth": {"method": "oauth"}, "endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://x.example.invalid"}]},
            {"kind": "custom", "endpoints": [{"protocol": "google-generative-ai", "baseUrl": "https://g.example.invalid"}]},
            {"kind": "preset", "preset": "example", "apiKey": "sk-example-p"}]}))
        self.assertEqual(out["format"], "cindy")
        messages, responses, local = out["drafts"]
        self.assertEqual(messages["label"], "Example AI")
        self.assertEqual({k: messages["provider"][k] for k in ("kind", "display_name", "base_url", "api_key", "model", "models", "headers")},
                         {"kind": "launcher", "display_name": "Example AI", "base_url": "https://api.example.invalid/anthropic",
                          "api_key": "sk-example-cindy", "model": "example-claude", "headers": "X-Tenant: t",
                          "models": [{"id": "example-claude", "context_window": 400000, "efforts": ["low"]}, {"id": "mini"}]})
        self.assertEqual(messages["notes"], [{"code": "cindy_endpoint", "name": "openai-responses"}])
        self.assertEqual({k: responses["provider"][k] for k in ("kind", "api", "base_url", "model")},
                         {"kind": "api", "api": "responses", "base_url": "https://api.example.invalid/v1", "model": "example-codex"})
        self.assertNotIn("api_key", local["provider"])
        self.assertEqual(local["notes"], [{"code": "cindy_auth", "name": "none"}])
        self.assertEqual(out["notes"], [{"code": "cindy_auth", "name": "oauth"}, {"code": "cindy_endpoint", "name": "google-generative-ai"},
                                        {"code": "cindy_kind", "name": "preset"}])

    def test_nothing_to_read(self):
        for text, note in (('{"providers": [', "invalid_json"), ("[1, 2]", "unrecognized"), ('{"theme": "dark"}', "unrecognized"),
                           ("Hello, here is my gateway.\nIt is fast.", "unrecognized")):
            self.assertEqual(self.parse(text), {"format": "", "drafts": [], "notes": [{"code": note}]}, text)
        many = "".join("alias a%d='ANTHROPIC_BASE_URL=https://gw%d.example.com claude'\n" % (i, i) for i in range(51))
        out = self.parse(many)
        self.assertEqual((len(out["drafts"]), out["notes"]), (50, [{"code": "too_many"}]))

    def test_blank_text_and_the_log(self):
        for body in ({"text": " \n "}, {}):
            self.assertEqual(self.h.call("POST", "/api/v1/launchers/import/parse", body)[0], 400)
        text = "alias gwcc='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=sk-example-logged claude'"
        self.parse(text)
        log = self.h.call("GET", "/mock/providers/log")[1]["parse"]
        self.assertEqual(log[-1], {"length": len(text)})
        self.assertNotIn("sk-example-logged", json.dumps(log))


if __name__ == "__main__":
    unittest.main()
