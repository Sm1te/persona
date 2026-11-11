"""Tests mock_hub's GET /api/v1/assistant/directions/tree (#4352): the leaders' open to-dos, the secretary's group last."""
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


def get(base, path):
    with urlopen(Request(base + path, headers={"Authorization": "Bearer mock"}), timeout=3) as response:
        return json.load(response)


class DirectionTreeMockTests(unittest.TestCase):
    def test_leaders_tasks_states_and_secretary(self):
        base, process, log = start({"MOCK_LEADERS": "1"})
        self.addCleanup(stop, process, log)
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        leaders = [d["id"] for d in get(base, "/api/v1/assistant/leaders")["leaders"]]
        self.assertEqual([d["id"] for d in tree[:-1]], leaders)
        secretary = tree[-1]
        self.assertTrue(secretary["secretary"])
        self.assertEqual(secretary["name"], "")
        self.assertEqual(secretary["id"], get(base, "/api/v1/assistant")["main"])
        self.assertTrue(secretary["tasks"])
        states = {t["state"] for d in tree for t in d["tasks"]}
        self.assertEqual(states, {"needs_you", "review", "stuck", "working"})
        order = {"needs_you": 0, "review": 1, "stuck": 2, "working": 3}
        for d in tree:
            ranks = [order[t["state"]] for t in d["tasks"]]
            self.assertEqual(ranks, sorted(ranks))
            for k in ("working", "needs_you", "stuck", "review"):
                self.assertEqual(d["counts"][k], sum(1 for t in d["tasks"] if t["state"] == k))
        # a spawned agent sits under the agent it came from, once
        weather = next(t for d in tree for t in d["tasks"] if t["id"] == "todo_weather")
        flat = [a["session_id"] for a in weather["agents"]] + [c["session_id"] for a in weather["agents"] for c in a["children"]]
        self.assertEqual(len(flat), len(set(flat)))
        self.assertTrue(any(a["children"] for a in weather["agents"]))
        # every open to-do is in exactly one group; done ones in none
        todos = get(base, "/api/v1/todos")["todos"]
        listed = [t["id"] for d in tree for t in d["tasks"]]
        self.assertEqual(sorted(listed), sorted(t["id"] for t in todos if not t.get("done_at")))

    def test_without_leaders_only_the_secretary(self):
        base, process, log = start({"MOCK_LEADERS": "0"})
        self.addCleanup(stop, process, log)
        tree = get(base, "/api/v1/assistant/directions/tree")["directions"]
        self.assertEqual(len(tree), 1)
        self.assertTrue(tree[0]["secretary"])

    def test_an_older_hub_has_none(self):
        base, process, log = start({"MOCK_LEGACY_HUB": "1"})
        self.addCleanup(stop, process, log)
        with self.assertRaises(HTTPError) as err:
            get(base, "/api/v1/assistant/directions/tree")
        self.assertEqual(err.exception.code, 404)


if __name__ == "__main__":
    unittest.main()
