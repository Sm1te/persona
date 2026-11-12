"""Tests mock_hub's background supervisor (#4927, #5082): the conversation, its off switch, its model purpose and its reviews."""
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

PURPOSE = "/api/v1/model-config/purposes/assistant.supervise"


def start(env):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    log = tempfile.TemporaryFile()
    process = subprocess.Popen(["python3", "-I", str(Path(__file__).with_name("mock_hub.py"))],
                               env=dict(os.environ, PORT=str(port), **env), stdout=log, stderr=log)
    base = f"http://127.0.0.1:{port}"
    for _ in range(100):
        if process.poll() is not None:
            raise RuntimeError("mock hub did not start")
        try:
            with urlopen(base + "/api/v1/health", timeout=1):
                return base, process, log
        except (URLError, TimeoutError):
            time.sleep(0.1)
    raise RuntimeError("mock hub start timed out")


def stop(process, log):
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)
    log.close()


def call(base, path, body=None, method=None):
    request = Request(base + path, data=None if body is None else json.dumps(body).encode(),
                      method=method or ("GET" if body is None else "PUT"),
                      headers={"Authorization": "Bearer mock", "Content-Type": "application/json"})
    with urlopen(request, timeout=3) as response:
        return json.load(response)


class BackgroundSupervisorMockTests(unittest.TestCase):
    def test_conversation_setting_and_reviews(self):
        base, process, log = start({"MOCK_BACKGROUND_SUPERVISOR": "1", "MOCK_TASKS": "1", "MOCK_TASKS_REPORTS": "1"})
        self.addCleanup(stop, process, log)
        a = call(base, "/api/v1/assistant")
        self.assertIs(a["main_supervises_own_tasks"], False)
        row = next(c for c in a["conversations"] if c["session_id"] == a["background_supervisor"])
        self.assertEqual(row["origin"], "task")
        self.assertNotIn("origin_ref", row)
        self.assertNotEqual(a["background_supervisor"], a["main"])
        self.assertNotIn(a["background_supervisor"], a.get("leaders", []))
        # the switch is saved and read back, both ways
        call(base, "/api/v1/assistant", {"main_supervises_own_tasks": True})
        self.assertIs(call(base, "/api/v1/assistant")["main_supervises_own_tasks"], True)
        call(base, "/api/v1/assistant", {"main_supervises_own_tasks": False})
        self.assertIs(call(base, "/api/v1/assistant")["main_supervises_own_tasks"], False)
        # the reviews in the main chat's feed are the background supervisor's
        feed = call(base, "/api/v1/assistant/conversations/" + a["main"] + "/feed")["items"]
        reviews = [i for i in feed if i["kind"] == "review"]
        self.assertTrue(reviews)
        self.assertTrue(all(i["body"]["by"] == "background" for i in reviews))

    def test_one_review_without_the_reports(self):
        base, process, log = start({"MOCK_BACKGROUND_SUPERVISOR": "1", "MOCK_TASKS": "1"})
        self.addCleanup(stop, process, log)
        main = call(base, "/api/v1/assistant")["main"]
        reviews = [i for i in call(base, "/api/v1/assistant/conversations/" + main + "/feed")["items"] if i["kind"] == "review"]
        self.assertEqual([(i["body"]["verdict"], i["body"]["by"]) for i in reviews], [("accepted", "background")])

    def test_model_purpose_follows_main_chat_until_set(self):
        base, process, log = start({})
        self.addCleanup(stop, process, log)
        self.assertNotIn("assistant.supervise", [p["id"] for p in call(base, "/api/v1/model-config")["purposes"]])
        main = call(base, "/api/v1/model-config/purposes/assistant")["purpose"]
        purpose = call(base, PURPOSE)["purpose"]
        self.assertEqual((purpose["parent"], purpose["category_id"]), ("assistant", "assistant"))
        self.assertEqual(purpose["effective"], main["effective"])
        self.assertEqual(purpose["override"], {})
        self.assertTrue(purpose["inherited_from"])
        self.assertIn("model", purpose["fields"])
        saved = call(base, PURPOSE, {"revision": purpose["revision"], "model": "model-small"}, "PATCH")["purpose"]
        self.assertEqual(saved["effective"]["model"], "model-small")
        self.assertEqual(call(base, "/api/v1/model-config/purposes/assistant")["purpose"]["effective"], main["effective"])
        with self.assertRaises(HTTPError) as stale:
            call(base, PURPOSE, {"revision": purpose["revision"], "model": "stale"}, "PATCH")
        self.assertEqual(stale.exception.code, 409)
        restored = call(base, PURPOSE, {"revision": saved["revision"], "model": None}, "PATCH")["purpose"]
        self.assertEqual(restored["effective"], main["effective"])

    def test_without_the_flag_no_conversation_and_an_older_hub_has_nothing(self):
        base, process, log = start({})
        self.addCleanup(stop, process, log)
        a = call(base, "/api/v1/assistant")
        self.assertNotIn("background_supervisor", a)
        self.assertIs(a["main_supervises_own_tasks"], False)
        old, process2, log2 = start({"MOCK_LEGACY_HUB": "1", "MOCK_BACKGROUND_SUPERVISOR": "1"})
        self.addCleanup(stop, process2, log2)
        self.assertNotIn("main_supervises_own_tasks", call(old, "/api/v1/assistant"))
        self.assertNotIn("background_supervisor", call(old, "/api/v1/assistant"))
        with self.assertRaises(HTTPError) as missing:
            call(old, PURPOSE)
        self.assertEqual(missing.exception.code, 404)


if __name__ == "__main__":
    unittest.main()
