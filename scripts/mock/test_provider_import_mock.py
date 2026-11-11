"""mock_hub.py's alias import (#5024, docs/protocol/protocol.md): the preview names what will really be created
(`create_as`: an alias Persona already lists is saved as "<alias>-2"), the POST saves under those names, honours `split`
and `hide_aliases`, and refuses a name it did not preview. Each case runs its own mock hub on a free port."""
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

    def test_refusals(self):
        h = hub(self, MOCK_PROVIDERS="1")
        self.assertEqual(h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "providers": ["nope"]})[0], 400)
        self.assertEqual(h.call("POST", "/api/v1/launchers/import", {"machine": "macbook", "providers": ["cc_glm"], "split": ["cc_kimi"]})[0], 400)
        self.assertEqual(h.call("GET", "/api/v1/launchers/import?machine=studio")[1]["code"], "node_outdated")
        self.assertEqual(h.call("GET", "/api/v1/launchers/import?machine=nas")[0], 503)


if __name__ == "__main__":
    unittest.main()
