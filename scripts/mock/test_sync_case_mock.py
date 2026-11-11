"""The mock hub plays every shared chat sync case (spec/fixtures/sync, #2766): a scripted device per case device (a
socket and HTTP, no app) does the cases' user actions, and after each step the frames it got are exactly the ones
the case lists (after an open, the hello and the connection snapshot first), its writes are answered only at their
`reply`, its held reads only at their `release`, and a dropped socket is let in again only at its `open`. At the end,
the hub's state gives the case's `end`: each turn's user message and answer, the cards still open."""
import base64
import json
import os
import socket
import threading
import time
import unittest
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

import sync_case
from test_mock_hub_mesh import Mock

SETTLE = 0.15  # how long nothing more may come for a step's frames to be all of them


class Device:
    """One scripted device: its socket (a reader thread keeps every frame), its HTTP requests, its writes in flight."""

    def __init__(self, port, name):
        self.port, self.name = port, name
        self.frames, self.cond = [], threading.Condition()
        self.sock = None
        self.writes = {}  # the case's id -> {"thread", "result"}
        self.ops = {}  # the case's id -> the op id its next retry goes with
        self.kinds = {}  # the case's id -> "messages" | "resolve" | "conversations"
        self.reconnecting = None
        self.stopped = False  # the case is over: no more reconnects

    # ── the socket ──

    def connect(self, timeout=10):
        s = socket.create_connection(("127.0.0.1", self.port), timeout=timeout)
        key = base64.b64encode(os.urandom(16)).decode()
        s.sendall((f"GET /api/v1/ws HTTP/1.1\r\nHost: 127.0.0.1\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
                   f"Authorization: Bearer mock-{self.name}\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n").encode())
        s.settimeout(None)  # an upgrade the case holds answers only at its `open`
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = s.recv(4096)
            if not chunk:
                raise ConnectionError("no upgrade")
            buf += chunk
        head, _, rest = buf.partition(b"\r\n\r\n")
        if b" 101 " not in head.split(b"\r\n")[0]:
            raise ConnectionError(head.split(b"\r\n")[0].decode())
        self.sock = s
        threading.Thread(target=self._read, args=(s, rest), daemon=True).start()

    def _read(self, s, buf):
        buf = bytearray(buf)

        def need(n):
            while len(buf) < n:
                chunk = s.recv(65536)
                if not chunk:
                    raise ConnectionError
                buf.extend(chunk)

        try:
            while True:
                need(2)
                op, n, off = buf[0] & 0x0F, buf[1] & 0x7F, 2
                if n == 126:
                    need(4)
                    n, off = int.from_bytes(buf[2:4], "big"), 4
                elif n == 127:
                    need(10)
                    n, off = int.from_bytes(buf[2:10], "big"), 10
                need(off + n)
                payload = bytes(buf[off:off + n])
                del buf[:off + n]
                if op == 8:
                    break
                if op == 1:
                    with self.cond:
                        self.frames.append(json.loads(payload))
                        self.cond.notify_all()
        except (ConnectionError, OSError):
            pass
        if self.sock is s and not self.stopped:  # gone (the case dropped it): connect again at once, as an app does
            self.sock = None
            self.reconnect()

    def reconnect(self):
        def again():
            for _ in range(3):
                if self.stopped:
                    return
                try:
                    self.connect(timeout=60)
                    if self.stopped:
                        self.close()
                    return
                except (ConnectionError, OSError):
                    time.sleep(0.2)
        self.reconnecting = threading.Thread(target=again, daemon=True)
        self.reconnecting.start()

    def close(self):
        s, self.sock = self.sock, None
        if s:
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            s.close()

    def take(self, n, wait=5):
        """The frames since the last take, once at least `n` came and nothing more came for SETTLE."""
        end = time.monotonic() + wait
        with self.cond:
            while len(self.frames) < n and time.monotonic() < end:
                self.cond.wait(end - time.monotonic())
        time.sleep(SETTLE)
        with self.cond:
            got, self.frames = self.frames, []
        return got

    # ── HTTP ──

    def request(self, method, path, body=None, op=None, timeout=60):
        headers = {"Authorization": f"Bearer mock-{self.name}", "Content-Type": "application/json"}
        if op:
            headers["X-Persona-Op-Id"] = op
        data = json.dumps(body).encode() if body is not None else None
        req = Request(f"http://127.0.0.1:{self.port}{path}", data=data, method=method, headers=headers)
        try:
            with urlopen(req, timeout=timeout) as r:
                return r.status, json.loads(r.read() or b"null")
        except HTTPError as e:
            with e:
                return e.code, json.loads(e.read() or b"null")

    def write(self, key, kind, method, path, body, op):
        """A write in its own thread (it waits for the case's reply); its result once answered: (status, body) or "cut"."""
        self.kinds[key], self.ops[key] = kind, op
        w = {"result": None}

        def run():
            try:
                w["result"] = self.request(method, path, body, op)
            except (URLError, OSError, ConnectionError):
                w["result"] = "cut"
        w["thread"] = threading.Thread(target=run, daemon=True)
        w["thread"].start()
        self.writes[key] = w


def transcript_view(truth):
    """Each turn's user message and its answer (the agent's last words in it), as the hub's state has them (the Go
    self-check's truthMessages, without the device's own failed and pending sends)."""
    out, answer = [], -1
    for it in truth.lines:
        if it.get("role") == "user" and it.get("kind") == "text" and not it.get("injected") and not it.get("from"):
            out.append({"from": "user", "text": it["text"], "state": "shown"})
            answer = -1
        elif it.get("role") == "assistant" and it.get("kind") == "text":
            if answer >= 0:
                out[answer] = {"from": "agent", "text": it["text"]}
            else:
                out.append({"from": "agent", "text": it["text"]})
                answer = len(out) - 1
    return out


class MovedCaseTests(unittest.TestCase):
    """A case played for an app on the real clock: every time in it moves by the same amount, nothing else."""

    def test_times_move_and_nothing_else(self):
        case = sync_case.load("sends.json#send_tied_by_words_without_echo")
        shift = sync_case.shift_to(1767323100 + 3600)  # an hour after the cases' clock, 2026-01-02T03:05:00Z
        self.assertEqual(shift, 3600)
        got = sync_case.moved(case, shift)
        self.assertEqual(got["start"]["hub"]["lines"][0]["ts"], "2026-01-02T04:04:05Z")
        self.assertEqual(sync_case.moved("2026-01-02T03:04:05.250Z", shift), "2026-01-02T04:04:05.250Z")
        self.assertEqual(got["steps"][0], case["steps"][0])  # no times in a send
        self.assertEqual(got["description"], case["description"])
        self.assertIs(sync_case.moved(case, 0), case)

    def test_the_hub_state_moves_too(self):
        hub = sync_case.CaseHub(sync_case.load("sends.json#send_tied_by_words_without_echo"), shift=86400)
        self.assertEqual(hub.truth.lines[0]["ts"], "2026-01-03T03:04:05Z")
        self.assertEqual(hub.rows()[0]["updated_at"], "2026-01-03T03:04:05Z")


class SyncCaseMockTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        first = sync_case.all_cases()[0]
        cls.hub = Mock(port, MOCK_SYNC_CASE="%s#%s" % (first[0], first[1]["name"]), MOCK_SYNC_CASE_WAIT="5")
        cls.addClassCleanup(cls.hub.stop)

    def post(self, path, body=None):
        req = Request(self.hub.base + path, data=json.dumps(body or {}).encode(), method="POST",
                      headers={"Content-Type": "application/json"})
        try:
            with urlopen(req, timeout=90) as r:
                return r.status, json.load(r)
        except HTTPError as e:
            with e:
                return e.code, json.loads(e.read() or b"null")

    def test_every_case(self):
        only = os.environ.get("SYNC_CASE_ONLY")  # one case by name, when looking into it
        for file, case in sync_case.all_cases():
            if only and case["name"] != only:
                continue
            with self.subTest(case=case["name"]):
                self.play(file, case)

    def play(self, file, case):
        status, _ = self.post("/mock/case/load", {"case": "%s#%s" % (file, case["name"])})
        self.assertEqual(status, 200)
        truth = sync_case.Truth(case["start"]["hub"])  # what the hub holds, step by step, for the hellos
        chat = case["chat"]
        base = "/api/v1/sessions/%s/%s" % (chat["machine_id"], chat["session_id"])
        devices = {d: Device(self.hub.port, d) for d in case["devices"]}
        try:
            for d in devices.values():
                d.connect()
                got = d.take(3)
                self.assertEqual([f["type"] for f in got[:3]], ["hello", "machines", "sessions"], got)
                self.assertFalse(got[0]["resumed"])
                self.assertEqual([f["interaction"]["id"] for f in got[3:]], [i["id"] for i in truth.pending])
            for i, s in enumerate(case["steps"]):
                where = "%s step %d (%s)" % (case["name"], i, ", ".join(k for k in s if k != "expect"))
                if os.environ.get("SYNC_CASE_TRACE"):
                    print(where, flush=True)
                status, out = self.post("/mock/case/step")  # the hub's side first, then what the user does
                self.assertEqual(status, 200, "%s: %s" % (where, out))
                self.assertEqual(out["index"], i, where)
                self.do_user(out.get("user"), devices, base)
                if s.get("hub"):
                    truth.apply(s["hub"])
                if s.get("hold"):
                    d = devices[s["hold"]["device"]]
                    path = {"/items": base + "/items", "/sessions": "/api/v1/sessions",
                            "/interactions": "/api/v1/interactions"}[s["hold"]["get"]]
                    d.held = {"result": None}

                    def read(d=d, path=path):
                        d.held["result"] = d.request("GET", path)
                    d.held["thread"] = threading.Thread(target=read, daemon=True)
                    d.held["thread"].start()
                    time.sleep(SETTLE)
                    self.assertIsNone(d.held["result"], "%s: a held read was answered before its release" % where)
                if s.get("release"):
                    d = devices[s["release"]["device"]]
                    d.held["thread"].join(10)
                    self.assertIsNotNone(d.held["result"], "%s: the released read was not answered" % where)
                self.check_frames(s, devices, truth, where)
                if s.get("reply"):
                    w = devices[s["reply"]["device"]].writes.get(s["reply"]["write"])
                    self.assertIsNotNone(w, where)
                    w["thread"].join(10)
                    if s["reply"].get("cut"):
                        self.assertEqual(w["result"], "cut", where)
                    else:
                        self.assertEqual(w["result"][0], s["reply"].get("status", 200), where)
            # settled: the hub's state is the case's end
            for name, end in case["end"].items():
                shown = [m for m in end["messages"] if m.get("state") not in ("failed", "pending")]
                self.assertEqual(shown, transcript_view(truth), "%s: end of %s" % (case["name"], name))
                self.assertEqual(sorted(k for k, v in end["cards"].items() if v == "open"),
                                 sorted(i["id"] for i in truth.pending), "%s: open cards of %s" % (case["name"], name))
            status, out = self.post("/mock/case/step")
            self.assertTrue(out.get("done"), out)
        finally:
            for d in devices.values():
                d.stopped = True
                d.close()

    def do_user(self, user, devices, base):
        """The step's user action, as an app would make its requests (the mock holds the writes)."""
        if not user:
            return
        d = devices[user["device"]]
        if user.get("send"):
            m = user["send"]
            d.write(m["id"], "messages", "POST", base + "/messages", {"text": m["text"], "client_msg_id": m["id"]}, m["id"])
        elif user.get("start"):
            m = user["start"]
            d.write(m["id"], "conversations", "POST", "/api/v1/assistant/conversations", {"text": m["text"]}, m["id"])
        elif user.get("answer"):
            a = user["answer"]
            d.write(a["id"], "resolve", "POST", "/api/v1/interactions/%s/resolve" % a["id"],
                    {"resolution": a["resolution"]}, "op-" + a["id"])
        elif user.get("retry"):
            key = user["retry"]
            if d.kinds.get(key) == "conversations":  # a refused start goes again with a new op id
                d.write(key, "conversations", "POST", "/api/v1/assistant/conversations", {"text": ""}, d.ops[key] + "-again")
            else:
                d.write(key, d.kinds.get(key, "messages"), "POST", base + "/messages", {"client_msg_id": key}, d.ops.get(key, key))
        elif user.get("relaunch"):
            d.close()
            d.reconnect()

    def check_frames(self, s, devices, truth, where):
        """The step's frames reached each device, and nothing else; after an open, the hello and the snapshot first."""
        sock = s.get("socket") or {}
        frames = s.get("frames") or {}
        for name, d in devices.items():
            want = list(frames.get(name, []))
            opened = sock.get("device") == name and sock.get("open") is not None
            if opened:
                got = d.take(3 + len(want))
                self.assertEqual([f["type"] for f in got[:3]], ["hello", "machines", "sessions"], "%s: %s" % (where, got))
                self.assertEqual(got[0]["resumed"], sock["open"].get("resumed", False), where)
                self.assertEqual((got[0]["event_stream"], got[0]["event_seq"]), (truth.stream, truth.seq), where)
                got = got[3:]
            else:
                got = d.take(len(want), wait=5 if want else SETTLE)
            self.assertEqual(json.dumps(got, sort_keys=True), json.dumps(want, sort_keys=True), "%s: frames to %s" % (where, name))
            if sock.get("device") == name and sock.get("drop"):
                status, out = self.post_state()
                self.assertFalse(out["devices"][name]["connected"], where)

    def post_state(self):
        with urlopen(self.hub.base + "/mock/case", timeout=10) as r:
            return r.status, json.load(r)


if __name__ == "__main__":
    unittest.main()
