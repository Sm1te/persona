"""The voice mock for the iOS and web voice modes (voice_mock.py, docs/assistant/voice.md 6.8, 6.11): /info at protocol 2, the
extension in both lists, and the protocol 2 script on the socket (ready → captions → run → link → ask → refused → done;
interrupt → clear; resend; the conversation turning working; the PCM frames it got), while a protocol 1 start still gets
mock_hub.py's old script."""
import base64
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import sys
import tempfile
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

HUB = Path(__file__).with_name("mock_hub.py")
TARGET = {"machine_id": "macbook", "session_id": "a1f3c2d4-0000-4000-8000-000000000001"}


class Socket:
    """A minimal WebSocket client: masked text frames out, frames in."""

    def __init__(self, port, path):
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=5)
        key = base64.b64encode(os.urandom(16)).decode()
        self.sock.sendall(("GET %s HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                           "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nAuthorization: Bearer mock\r\n\r\n"
                           % (path, key)).encode())
        head = b""
        while b"\r\n\r\n" not in head:
            chunk = self.sock.recv(1)
            if not chunk:
                raise ConnectionError("closed during the handshake")
            head += chunk
        self.status = int(head.split(b" ")[1])

    def send(self, obj):
        self._send(0x81, json.dumps(obj).encode())

    def send_bytes(self, data):
        self._send(0x82, data)

    def _send(self, first, data):
        mask = os.urandom(4)
        n = len(data)
        hdr = bytes([first]) + (bytes([0x80 | n]) if n < 126 else bytes([0x80 | 126]) + struct.pack(">H", n))
        self.sock.sendall(hdr + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(data)))

    def _read(self, n):
        out = b""
        while len(out) < n:
            chunk = self.sock.recv(n - len(out))
            if not chunk:
                raise ConnectionError("closed")
            out += chunk
        return out

    def frame(self):
        hd = self._read(2)
        op, n = hd[0] & 0x0F, hd[1] & 0x7F
        if n == 126:
            n = struct.unpack(">H", self._read(2))[0]
        elif n == 127:
            n = struct.unpack(">Q", self._read(8))[0]
        return op, self._read(n)

    def until(self, match, timeout=15):
        """Text frames up to and including the first that `match` takes (binary frames skipped)."""
        seen = []
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            op, data = self.frame()
            if op != 1:
                continue
            obj = json.loads(data)
            seen.append(obj)
            if match(obj):
                return seen
        raise AssertionError("no matching frame in %s s; saw %s" % (timeout, [x.get("type") for x in seen]))

    def close(self):
        self.sock.close()


class VoiceMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            cls.port = sock.getsockname()[1]
        cls.log = tempfile.TemporaryFile()
        env = {"PATH": os.environ.get("PATH", ""), "PORT": str(cls.port), "MOCK_VOICE": "1", "MOCK_VOICE_PACE": "0.1",
               "MOCK_VOICE_VOICES_DELAY": "0.3", "MOCK_VOICE_SPEAK_SLOW": "0.3"}
        cls.process = subprocess.Popen([sys.executable, "-I", str(HUB)], env=env, stdout=cls.log, stderr=cls.log)
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if cls.process.poll() is not None:
                raise RuntimeError("mock hub exited before startup")
            try:
                with urlopen("http://127.0.0.1:%d/api/v1/health" % cls.port, timeout=1):
                    return
            except (URLError, OSError):
                time.sleep(0.1)
        raise RuntimeError("mock hub startup timed out")

    @classmethod
    def tearDownClass(cls):
        cls.process.terminate()
        try:
            cls.process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            cls.process.kill()
            cls.process.wait(timeout=5)
        cls.log.close()

    def get(self, path):
        with urlopen("http://127.0.0.1:%d%s" % (self.port, path), timeout=3) as r:
            return json.load(r)

    def test_info_and_lists(self):
        for prefix in ("/api/v1/extensions", "/api/v1/plugins"):
            info = self.get(prefix + "/voice/info")
            self.assertEqual(info["protocol"], 2)
            self.assertIn("target", info["features"])
            self.assertEqual(info["setup"], [])
            self.assertEqual(self.get(prefix + "/voice/models")["default"], "voice-deep")
            self.assertTrue(self.get(prefix + "/voice/voices?model=voice-fast")["voices"])
        self.assertEqual(self.get("/api/v1/extensions")["extensions"][0]["kind"], "voice")
        self.assertEqual(self.get("/api/v1/plugins")["plugins"][0]["status"], "running")
        purpose = self.get("/api/v1/model-config/purposes/plugin.voice")["purpose"]
        self.assertEqual(purpose["id"], "plugin.voice")

    def status(self, path):
        """The status and JSON body of a GET that may fail."""
        try:
            with urlopen("http://127.0.0.1:%d%s" % (self.port, path), timeout=3) as r:
                return r.status, json.load(r)
        except HTTPError as e:
            return e.code, json.load(e)

    def test_voices_fail_on_demand(self):
        # #2411: the pickers' failed, retry and stale states.
        base = "/api/v1/extensions/voice/voices?model=voice-deep"
        self.assertEqual(self.status(base + "&mock=fail")[0], 502)
        code, body = self.status(base + "&mock=stale")
        self.assertEqual((code, body["stale"], body["default"]), (200, True, "soft"))
        self.assertEqual(self.status(base + "&mock=none"), (200, {"voices": []}))
        began = time.monotonic()
        self.assertEqual(self.status(base + "&mock=slowfail")[0], 502)
        self.assertGreaterEqual(time.monotonic() - began, 0.3, "slowfail answers late")
        try:
            self.assertEqual(self.get("/mock/voice2?voices=fail")["voices_mode"], "fail")
            self.assertEqual(self.status(base)[0], 502)
            self.get("/mock/voice2?voices=flaky")
            self.assertEqual([self.status(base)[0] for _ in range(3)], [502, 502, 200])
        finally:
            self.assertEqual(self.get("/mock/voice2?voices=ok")["voices_mode"], "ok")
        code, body = self.status(base)
        self.assertEqual((code, "stale" in body), (200, False))
        self.assertGreaterEqual(self.get("/mock/voice2")["voices"], 7)
        # The extension's settings page.
        page = self.get("/api/v1/extensions/voice/-/")
        self.assertEqual((page["id"], page["kind"], page["status"]), ("voice", "voice", "running"))
        self.assertTrue(page["form"]["fields"])

    def speak(self, prefix, obj):
        """POST …/voice/speak: the status, headers and body."""
        req = Request("http://127.0.0.1:%d%s/voice/speak" % (self.port, prefix), data=json.dumps(obj).encode(), method="POST",
                      headers={"Content-Type": "application/json", "Authorization": "Bearer mock"})
        try:
            with urlopen(req, timeout=10) as r:
                return r.status, r.headers, r.read()
        except HTTPError as e:
            return e.code, e.headers, e.read()

    def test_speak(self):
        # #5004: a text read aloud, as the built-in extension answers it: the tone at 24 kHz, streamed at about real time.
        text = {"text": "回归测试由 Bo 负责，周五前完成。", "model": "voice-deep", "voice": "soft", "lang": "zh-CN"}
        try:
            state = self.get("/mock/voice2?speak=ok&speak_seconds=0.5")
            self.assertEqual((state["speak_mode"], state["speak_seconds"]), ("ok", 0.5))
            self.assertIn("speak", self.get("/api/v1/extensions/voice/info")["features"])
            began = time.monotonic()
            code, headers, body = self.speak("/api/v1/extensions", text)
            took = time.monotonic() - began
            self.assertEqual((code, headers["Content-Type"], headers["X-Persona-Audio-Rate"]), (200, "audio/pcm", "24000"))
            self.assertEqual(len(body), 12000 * 2)
            self.assertTrue(any(body), "a tone, not silence")
            self.assertGreaterEqual(took, 0.35, "about real time, 100 ms at a time")
            self.assertEqual(self.get("/mock/voice2")["speaks"][-1], text)

            # The first piece comes long before the last.
            self.get("/mock/voice2?speak_seconds=1.5")
            req = Request("http://127.0.0.1:%d/api/v1/plugins/voice/speak" % self.port, data=b'{"text":"hi"}', method="POST",
                          headers={"Content-Type": "application/json"})
            began = time.monotonic()
            with urlopen(req, timeout=10) as r:
                self.assertEqual(len(r.read(4800)), 4800)
                first = time.monotonic() - began
                rest = r.read()
                self.assertEqual(4800 + len(rest), 36000 * 2)
            self.assertLess(first, 0.6, "the first 100 ms came as it was made")
            self.assertGreaterEqual(time.monotonic() - began, 1.2)

            self.get("/mock/voice2?speak=fail")
            code, _, body = self.speak("/api/v1/extensions", {"text": "hi"})
            self.assertEqual((code, json.loads(body)["code"]), (502, "upstream_failed"))
            self.get("/mock/voice2?speak=slow&speak_seconds=0.1")
            began = time.monotonic()
            code, _, body = self.speak("/api/v1/extensions", {"text": "hi"})
            self.assertEqual((code, len(body)), (200, 4800))
            self.assertGreaterEqual(time.monotonic() - began, 0.3, "slow answers late")
            self.assertEqual(self.speak("/api/v1/extensions", {"text": " "})[0], 400)

            # An extension from before it: no speak in the features, and no such route.
            self.get("/mock/voice2?speak=off")
            self.assertNotIn("speak", self.get("/api/v1/extensions/voice/info")["features"])
            self.assertEqual(self.speak("/api/v1/extensions", {"text": "hi"})[0], 404)
            self.assertEqual(self.get("/mock/voice2")["speak_mode"], "off")
        finally:
            self.get("/mock/voice2?speak=ok&speak_seconds=3")
        self.assertEqual([s["text"] for s in self.get("/mock/voice2")["speaks"]][-5:],
                         [text["text"], "hi", "hi", "hi", " "], "every request, newest last (none while off)")

    def test_protocol_2_script(self):
        ws = Socket(self.port, "/api/v1/extensions/voice/ws")
        try:
            self.assertEqual(ws.status, 101)
            ws.send({"type": "start", "protocol": 2, "model": "voice-deep", "target": TARGET, "recent": []})
            ready = ws.until(lambda f: f["type"] == "ready")[-1]
            self.assertEqual(ready["protocol"], 2)
            self.assertEqual(ready["target"]["session_id"], TARGET["session_id"])
            self.assertEqual(ready["target"]["kind"], "agent")
            frames = ws.until(lambda f: f["type"] == "run" and f["state"] == "done" and f["id"].startswith("voice-7"), timeout=20)
            kinds = [(f["type"], f.get("state") or f.get("final")) for f in frames]
            self.assertIn(("transcript", False), kinds)
            self.assertIn(("transcript", True), kinds)
            states = [f["state"] for f in frames if f["type"] == "run" and f["id"].startswith("voice-7")]
            self.assertEqual(states[:3], ["sending", "sent", "working"])
            self.assertEqual([f["state"] for f in frames if f["type"] == "link"], ["paused", "live"])
            asks = [f for f in frames if f["type"] == "ask"]
            self.assertEqual([a["state"] for a in asks], ["asking", "answered"])
            # The answer's late caption (#5673): it comes after the voice began to reply, and says it goes before that.
            lines = [(f["id"], f.get("before")) for f in frames if f["type"] == "transcript" and f["id"] in ("u3", "a3")]
            self.assertEqual(lines, [("a3", None), ("u3", "a3"), ("a3", None)])
            refused = next(f for f in frames if f["type"] == "run" and f["state"] == "refused")
            self.assertEqual(refused["code"], "input_draft")
            self.assertEqual(refused["clearable"], "fix it")
            # Clear and send: the refused run goes again under its id.
            ws.send({"type": "resend", "id": refused["id"], "clear_draft": "fix it"})
            again = ws.until(lambda f: f["type"] == "run" and f["id"] == refused["id"] and f["state"] == "working")
            self.assertNotIn("code", [k for k, v in again[-1].items() if v is not None])
            log = self.get("/mock/voice2")
            self.assertEqual(log["resends"][-1], {"id": refused["id"], "clear_draft": "fix it"})
            self.assertEqual(log["starts"][-1]["protocol"], 2)
        finally:
            ws.close()

    def test_interrupt_clears_while_speaking(self):
        ws = Socket(self.port, "/api/v1/plugins/voice/ws")
        try:
            ws.send({"type": "start", "protocol": 2, "target": TARGET})
            ws.until(lambda f: f["type"] == "transcript" and f["id"] == "a1")
            ws.send({"type": "interrupt"})
            ws.until(lambda f: f["type"] == "clear", timeout=5)
            ws.send({"type": "text", "text": "顺便看下 lint"})
            ws.until(lambda f: f["type"] == "transcript" and f["text"] == "顺便看下 lint", timeout=5)
        finally:
            ws.close()

    def test_conversation_turns_working_and_frames_are_counted(self):
        idle = {"machine_id": "macbook", "session_id": "a1f3c2d4-0000-4000-8000-000000000006"}
        state = lambda: next(r["state"] for r in self.get("/api/v1/sessions")["sessions"]
                             if r["machine_id"] == idle["machine_id"] and r["session_id"] == idle["session_id"])
        self.assertEqual(state(), "idle")
        ws = Socket(self.port, "/api/v1/extensions/voice/ws")
        try:
            ws.send({"type": "start", "protocol": 2, "target": idle})
            ws.until(lambda f: f["type"] == "ready")
            before = self.get("/mock/voice2")
            for _ in range(3):
                ws.send_bytes(bytes(640))  # 20 ms of 16 kHz PCM16, all zero (muted)
            ws.send_bytes(b"\x01\x00" * 320)  # one that is not
            ws.until(lambda f: f["type"] == "run" and f["state"] == "working")
            # the hub's own row changed, so its sessions list (and push) says so
            self.assertEqual(state(), "working")
            log = self.get("/mock/voice2")
            self.assertEqual(log["audio"] - before["audio"], 4)
            self.assertEqual(log["audio_bytes"] - before["audio_bytes"], 2560)
            self.assertGreaterEqual(log["audio_sizes"].get("640", 0), 4)
            self.assertEqual(log["audio_silent"] - before["audio_silent"], 3)
            self.assertEqual(log["pace"], 0.1)  # web/e2e/voice.mjs checks the uplink's rate over 40 s at pace 1
        finally:
            ws.close()
        # closed before its script was done: the conversation goes back to idle for the next call
        deadline = time.monotonic() + 5
        while state() != "idle" and time.monotonic() < deadline:
            time.sleep(0.05)
        self.assertEqual(state(), "idle")

    def test_protocol_1_start_gets_the_old_script(self):
        ws = Socket(self.port, "/api/v1/plugins/voice/ws")
        try:
            ws.send({"type": "start", "model": "voice-fast"})
            ready = ws.until(lambda f: f["type"] == "ready")[-1]
            self.assertNotIn("protocol", ready)
            self.assertEqual(ready["model"], "voice-fast")
            caption = ws.until(lambda f: f["type"] == "transcript")[-1]
            self.assertEqual(caption["text"], "今天下午")
        finally:
            ws.close()


if __name__ == "__main__":
    unittest.main()
