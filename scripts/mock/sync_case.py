"""sync_case.py: the hub's side of one shared chat sync case (spec/fixtures/sync/README.md) for scripts/mock/mock_hub.py
(#2766, #1073 step 4). Standard library only.

    MOCK_SYNC_CASE=reconnect.json#lost_push_fills_gap PORT=18950 python3 scripts/mock/mock_hub.py

The mock then holds what the case says the hub holds, for the case's one chat, and plays each step's hub side when the
runner asks (POST /mock/case/step). The runner does the step's user action on screen and checks its `expect`; this
module never looks at a screen. The rules for reads, writes, receipts and the socket are the web consumer's
(web/src/views/chat/sync/fake-hub.ts), for several devices on real connections:

- Devices: an app paired with the token `mock-<device>` (`token=mock-a`) is that device, by its Authorization header or
  its `?token=`; any other token is the first.
- The chat's reads are answered from the case's hub state at once, unless the case holds them (`hold` / `release`).
- The chat's writes (a send, a card's answer, a start) wait for their `reply` step; a receipt the reply leaves is what
  later retries with the same op id get at once.
- The socket: the first one gets a hello that did not resume, then the connection snapshot (the case's machine, the
  chat's session list, its pending cards). After that it only gets the frames the steps list. A `drop` closes it and
  holds every new one until the `open` step, whose hello says `resumed` or not; the snapshot follows, and the step's
  frames after it (the hub's replay). The first subscription to the chat gets its backlog (the last two turns), the way
  a chat loads.

GET /mock/case: where the case is, and per device the socket, the requests it made (`seen`, as the case's `requests`
name them) and its writes in flight. POST /mock/case/load {"case": "<file>#<name>"}: another case, in the same mock;
with `"now": true` its times moved so its clock (CLOCK) reads the present, for an app that runs on the real clock.
Everything else in the mock is left as it is.
"""
import base64
import calendar
import copy
import glob
import json
import os
import re
import threading
import time

HERE = os.path.dirname(os.path.abspath(__file__))
CASES = os.path.join(HERE, "..", "..", "spec", "fixtures", "sync")
STATUS = {"working": "working", "needs_you": "blocked", "unread": "done", "idle": "idle"}
UPDATED = "2026-01-02T03:04:05Z"
# The cases' clock: the web's consumers pin theirs here. An app on the real clock plays a case moved to the present
# (`POST /mock/case/load {"case": …, "now": true}`): left in January, every line is months older than what the app
# sends, and iOS takes a line without `client_msg_id` for a bubble only when it was written after it (#2952).
CLOCK = "2026-01-02T03:05:00Z"
STAMP = re.compile(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?Z$")
FEATURES = ["client_msg_id", "client_ops", "send_refusals", "sessions_delta", "error_codes", "client_hello", "app_ping",
            "resumable_interactions", "assistant_start_ops"]
# How long a step waits for an app: its write to arrive before a `reply`, its read before a `release`, its new socket
# before an `open` returns. A step that times out answers 409 and says what it waited for.
APP_WAIT = float(os.environ.get("MOCK_SYNC_CASE_WAIT") or 30)  # shorter in the mock's own test
CUT = object()  # what a write answers when the case cuts its connection: no response at all


def load(spec):
    """The case `file#name` (a file under spec/fixtures/sync), or `name` looked up in all of them."""
    file, _, name = spec.rpartition("#")
    files = [os.path.join(CASES, file)] if file else sorted(glob.glob(os.path.join(CASES, "*.json")))
    for f in files:
        with open(f, encoding="utf-8") as fh:
            for c in json.load(fh)["cases"]:
                if c["name"] == name:
                    return c
    raise ValueError("no sync case %r" % spec)


def shift_to(now):
    """The seconds that move the cases' clock (CLOCK) to `now` (seconds since the epoch)."""
    return int(now) - calendar.timegm(time.strptime(CLOCK, "%Y-%m-%dT%H:%M:%SZ"))


def moved(v, shift):
    """`v` (any of a case's JSON) with every time in it `shift` seconds later; nothing else changes."""
    if not shift:
        return v
    if isinstance(v, dict):
        return {k: moved(x, shift) for k, x in v.items()}
    if isinstance(v, list):
        return [moved(x, shift) for x in v]
    m = STAMP.match(v) if isinstance(v, str) else None
    if not m:
        return v
    t = calendar.timegm(time.strptime(m.group(1), "%Y-%m-%dT%H:%M:%S")) + shift
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(t)) + (m.group(2) or "") + "Z"


def token_of(h):
    """The token a request came with: its Authorization header, or `?token=` (a browser's WebSocket cannot set headers)."""
    from urllib.parse import parse_qs, urlparse
    got = h.headers.get("Authorization", "").removeprefix("Bearer ").strip()
    return got or (parse_qs(urlparse(h.path).query).get("token") or [""])[0]


def all_cases():
    """(file, case) for every case, in file order."""
    out = []
    for f in sorted(glob.glob(os.path.join(CASES, "*.json"))):
        with open(f, encoding="utf-8") as fh:
            out += [(os.path.basename(f), c) for c in json.load(fh)["cases"]]
    return out


class CaseError(Exception):
    """A step the apps are out of step with (a reply to a write that never came, a device that did not reconnect)."""


class Truth:
    """The hub's state for the chat, as the case changes it."""

    def __init__(self, h, updated=UPDATED):
        self.history = h["history"]
        self.lines = copy.deepcopy(h["lines"])
        self.to_seq = h["to_seq"]
        self.pending = copy.deepcopy(h["interactions"])
        self.state = h["session"].get("state", "")
        self.queued = list(h["session"].get("queued") or [])
        self.stream = h.get("event_stream", "ev")
        self.seq = h.get("event_seq", 0)
        self.updated = updated
        self.rev = 100

    def apply(self, c):
        if c.get("restart") and not c["restart"].get("resumable"):
            self.stream += "+"
        if c.get("rewrite"):
            self.history = c["rewrite"]["history"]
            self.lines = copy.deepcopy(c["rewrite"]["lines"])
            self.to_seq = c["rewrite"]["to_seq"]
        if c.get("append"):
            self.lines += copy.deepcopy(c["append"])
            self.to_seq = max([self.to_seq] + [i["seq"] for i in c["append"]])
        if "to_seq" in c:
            self.to_seq = c["to_seq"]
        if c.get("append"):  # the list moves with the transcript, as the hub's does
            last = c["append"][-1]
            if last.get("ts") and last["ts"] > self.updated:
                self.updated = last["ts"]
            self.rev += 10
        if c.get("end"):
            self.pending = [i for i in self.pending if i["id"] != c["end"]["id"]]
            self.seq += 1
        if c.get("raise"):
            self.pending.append(copy.deepcopy(c["raise"]))
            self.seq += 1
        s = c.get("session") or {}
        if s.get("state"):
            self.state = s["state"]
        if "queued" in s:
            self.queued = list(s["queued"] or [])

    def row(self, chat):
        r = {"machine_id": chat["machine_id"], "session_id": chat["session_id"], "title": "Weekend trip",
             "cwd": "/home/user/project", "status": STATUS.get(self.state, "idle"), "state": self.state,
             "assistant": bool(chat.get("assistant")), "updated_at": self.updated, "rev": self.rev}
        if self.queued:
            r["queued"] = list(self.queued)
        return r

    @staticmethod
    def _turn_start(lines, turns):
        if not turns:
            return 0
        starts = [i for i, it in enumerate(lines) if it.get("role") == "user" and it.get("kind") == "text"]
        return starts[-turns] if len(starts) > turns else 0

    def latest(self, turns=None):
        start = self._turn_start(self.lines, turns)
        return {"items": copy.deepcopy(self.lines[start:]), "history": self.history, "to_seq": self.to_seq, "has_more": start > 0}

    def after(self, seq):
        return {"items": copy.deepcopy([i for i in self.lines if i["seq"] > seq]), "history": self.history,
                "from_seq": seq, "to_seq": self.to_seq}

    def before(self, seq, turns=None):
        older = [i for i in self.lines if i["seq"] < seq]
        start = self._turn_start(older, turns)
        return {"items": copy.deepcopy(older[start:]), "history": self.history, "has_more": start > 0}


class Conn:
    """An app's open socket: writes a frame, closes."""

    def __init__(self, write, close):
        self.write, self.close = write, close


class Device:
    def __init__(self, name):
        self.name = name
        self.socket = None  # the open Conn
        self.accepting = True  # False from a `drop` until its `open`: new sockets wait
        self.next_open = None  # what the next socket gets: {"resumed", "frames"}; None: a first connection
        self.connections = 0
        self.subscribes = 0
        self.seen = []
        self.holds = {}  # path -> "armed", or the held reads (events) once one came


class CaseHub:
    def __init__(self, case, now=time.monotonic, shift=0):
        """`shift`: every time in the case that many seconds later (`shift_to`)."""
        case = moved(case, shift)
        self.case = case
        self.chat = case["chat"]
        self.devices = {d: Device(d) for d in case["devices"]}
        self.truth = Truth(case["start"]["hub"], moved(UPDATED, shift))
        self.started = not self.chat.get("new")
        self.index = 0
        self.writes = []  # {"device", "kind", "key", "op", "event", "answer"}
        self.receipts = {}  # op id -> {"status", "body"}
        self.key_of = {}  # an app's client_msg_id -> the case's message id
        self.last_cmid = {}  # the case's message id -> the client_msg_id its latest write had
        self.texts = {}  # the case's message id -> its words (to know a send by them)
        self.start_id = ""
        self.unknown = []
        self.lock = threading.RLock()
        self.changed = threading.Condition(self.lock)
        self.now = now
        self.closed = False

    # ── who is who ──

    def device_of(self, token):
        name = token[len("mock-"):] if token.startswith("mock-") else ""
        return name if name in self.devices else self.case["devices"][0]

    @property
    def base(self):
        return "/api/v1/sessions/%s/%s" % (self.chat["machine_id"], self.chat["session_id"])

    def rows(self):
        return [self.truth.row(self.chat)] if self.started else []

    def machines(self):
        return [{"id": self.chat["machine_id"], "name": "Hub machine", "online": True}]

    def tie(self, v):
        """The case ties a line to its own message id (`m1`); the app sent that message with an id of its own. The node
        tags the line with the id the message went with, so the hub here does too."""
        if isinstance(v, list):
            return [self.tie(x) for x in v]
        if not isinstance(v, dict):
            return v
        o = {k: self.tie(x) for k, x in v.items()}
        if isinstance(o.get("client_msg_id"), str):
            o["client_msg_id"] = self.last_cmid.get(o["client_msg_id"], o["client_msg_id"])
        return o

    # ── the socket ──

    def admit(self, device, timeout=APP_WAIT * 4, alive=lambda: True):
        """A new socket from `device`: waits while the case keeps the device disconnected, then says what it gets.
        None: still refused when the wait ran out (the handler answers 503), or its app went meanwhile (`alive`: a page
        that reloaded while its upgrade waited must not take the turn of the one it reloaded into)."""
        d = self.devices[device]
        end = self.now() + timeout
        with self.changed:
            while not d.accepting or d.socket is not None:
                left = end - self.now()
                if left <= 0 or self.closed or not alive():
                    return None
                self.changed.wait(min(left, 0.5))
            # Not one the case opens (the first, or one the app made on its own): the hub's own snapshot, cards too.
            ticket = d.next_open or {"resumed": False, "frames": [], "first": True}
            d.next_open = None
            d.accepting = False  # one socket at a time: a second one waits for this one to go
            return ticket

    def unadmit(self, device, ticket):
        """The admitted socket's app was gone before it opened (a page reloaded while its upgrade waited): the next one
        gets what it would have."""
        d = self.devices[device]
        with self.changed:
            if ticket.get("resumed") or ticket.get("frames") or not ticket.get("first"):
                d.next_open = ticket
            d.accepting = True
            self.changed.notify_all()

    def opened(self, device, conn, ticket):
        """The admitted socket is open: the hello, the connection snapshot, the replay."""
        d = self.devices[device]
        with self.changed:
            d.socket = conn
            d.connections += 1
            d.accepting = True
            conn.write({"type": "hello", "server_version": "mock-sync", "client_id": "c-%s-%d" % (device, d.connections),
                        "hub_id": "hub1", "hub_name": "Hub", "features": FEATURES,
                        "event_stream": self.truth.stream, "event_seq": self.truth.seq, "resumed": bool(ticket.get("resumed"))})
            conn.write({"type": "machines", "machines": self.machines()})
            conn.write({"type": "sessions", "machine_id": self.chat["machine_id"], "sessions": self.rows()})
            if ticket.get("first"):
                for ix in self.truth.pending:
                    conn.write({"type": "interaction", "interaction": copy.deepcopy(ix)})
            for f in ticket.get("frames") or []:
                conn.write(self.tie(f))
            self.changed.notify_all()

    def gone(self, device, conn):
        d = self.devices[device]
        with self.changed:
            if d.socket is conn:
                d.socket = None
            self.changed.notify_all()

    def from_app(self, device, msg):
        """A frame the app sent on its socket."""
        if msg.get("type") != "subscribe" or msg.get("session_id") != self.chat["session_id"]:
            return
        d = self.devices[device]
        with self.lock:
            since = msg.get("since_seq") if isinstance(msg.get("since_seq"), int) else None
            d.seen.append({"ws": "subscribe", **({"since_seq": since} if since is not None else {})})
            d.subscribes += 1
            # the first subscription's replay (the last turns) is how the chat loads; later ones come from the case
            if d.subscribes == 1 and d.socket is not None:
                d.socket.write(self.tie({"type": "items", "machine_id": self.chat["machine_id"],
                                         "session_id": self.chat["session_id"], "backlog": True, **self.truth.latest(2)}))

    # ── HTTP ──

    def handle(self, device, method, path, query, headers, body):
        """The answer to an app's request about the case: (status, JSON), CUT, or None for one the mock answers."""
        op = (headers.get("X-Persona-Op-Id") or "").lower()
        q = {k: v[0] for k, v in query.items()}
        if method == "GET":
            if path == "/api/v1/sessions":
                return self.read(device, "/sessions", lambda: {"sessions": self.rows()})
            if path == "/api/v1/assistant":
                # the fields the apps decode as required (iOS: `launcher`, `cwd`); an assistant chat is the main one
                st = {"machine_id": self.chat["machine_id"], "launcher": "claude", "cwd": "/home/user/project",
                      "conversations": self.rows()}
                if self.chat.get("assistant") and not self.chat.get("new"):
                    st["main"] = self.chat["session_id"]
                return 200, st
            if path == "/api/v1/interactions":
                return self.read(device, "/interactions", lambda: {"interactions": copy.deepcopy(self.truth.pending)})
            if path == "/api/v1/machines":
                return 200, {"machines": self.machines()}
            if path == self.base + "/items":
                d = self.devices[device]
                if "after_seq" in q:
                    after = int(q["after_seq"])
                    with self.lock:
                        d.seen.append({"get": "/items", "after_seq": after})
                    return self.read(device, "/items", lambda: self.truth.after(after))
                with self.lock:
                    d.seen.append({"get": "/items"})
                turns = int(q["turns"]) if "turns" in q else None
                if "before_seq" in q:
                    return self.read(device, "/items", lambda: self.truth.before(int(q["before_seq"]), turns))
                return self.read(device, "/items", lambda: self.truth.latest(self.case["start"]["hub"].get("page_turns") or turns))
            if path.startswith("/api/v1/ops/"):
                asked = path[len("/api/v1/ops/"):].lower()
                with self.lock:
                    seen = self.devices[device].seen
                    # the case's id for the write that op went with ({"get": "/ops", "id"}, #2770)
                    key = next((s["key"] for s in seen if str(s.get("op") or "").lower() == asked), asked)
                    seen.append({"get": "/ops", "key": key})
                r = self.receipts.get(asked)
                if not r:
                    return 404, {"error": "no such operation"}
                encoded = base64.b64encode(json.dumps(r["body"]).encode()).decode()
                return 200, {"id": path[len("/api/v1/ops/"):], "state": "done" if 200 <= r["status"] < 300 else "failed",
                             "result": {"status": r["status"], "body": encoded, "content_type": "application/json"}}
            return None
        if method == "POST" and path == self.base + "/messages":
            cmid = (body or {}).get("client_msg_id") or op
            with self.lock:
                key = self.key_of.get(cmid)
                if not key:
                    key = next((k for k, t in self.texts.items() if t == (body or {}).get("text")), cmid)
                    self.key_of[cmid] = key
                self.last_cmid[key] = cmid
            return self.write(device, "messages", key, (op or cmid).lower())
        if method == "POST" and path.startswith("/api/v1/interactions/") and path.endswith("/resolve"):
            return self.write(device, "resolve", path[len("/api/v1/interactions/"):-len("/resolve")], op)
        if method == "POST" and path == "/api/v1/assistant/conversations" and self.chat.get("new"):
            return self.write(device, "conversations", self.start_id, op)
        if method == "POST" and path.startswith(self.base) and path.endswith("/seen"):
            return 200, {"ok": True}
        return None

    def read(self, device, path, answer):
        d = self.devices[device]
        with self.changed:
            if d.holds.get(path) != "armed":
                return 200, self.tie(answer())
            body = self.tie(answer())  # what the hub held when the read came: a stale answer at the release
            released = threading.Event()
            d.holds[path] = released
            self.changed.notify_all()
        released.wait(APP_WAIT * 4)
        return 200, body

    def write(self, device, kind, key, op):
        with self.changed:
            self.devices[device].seen.append({"post": kind, "key": key, "op": op})
            receipt = self.receipts.get(op) if op else None
            if receipt:
                return receipt["status"], copy.deepcopy(receipt["body"])
            w = {"device": device, "kind": kind, "key": key, "op": op, "event": threading.Event(), "answer": None}
            self.writes.append(w)
            self.changed.notify_all()
        w["event"].wait(APP_WAIT * 4)
        return w["answer"] if w["answer"] is not None else CUT

    # ── the steps ──

    def step(self):
        """Plays the next step's hub side; what the runner does next: the user action, the wait, what to check."""
        if self.index >= len(self.case["steps"]):
            return {"done": True, "end": self.case["end"]}
        s = self.case["steps"][self.index]
        self.index += 1
        if s.get("hub"):
            with self.lock:
                self.truth.apply(s["hub"])
        sock = s.get("socket")
        if sock and sock.get("drop"):
            self.drop(sock["device"])
        elif sock and sock.get("open") is not None:
            self.open(sock["device"], sock["open"].get("resumed", False), (s.get("frames") or {}).get(sock["device"], []))
        for device, frames in (s.get("frames") or {}).items():
            if sock and sock.get("open") is not None and device == sock["device"]:
                continue  # the replay after the hello
            for f in frames:
                self.push(device, f)
        if s.get("reply"):
            self.reply(s["reply"])
        if s.get("hold"):
            with self.changed:
                self.devices[s["hold"]["device"]].holds[s["hold"]["get"]] = "armed"
        if s.get("release"):
            self.release(s["release"]["device"], s["release"]["get"])
        user = s.get("user")
        if user:
            with self.lock:
                if user.get("send"):
                    self.texts[user["send"]["id"]] = user["send"]["text"]
                if user.get("start"):
                    self.start_id = user["start"]["id"]
        out = {"index": self.index - 1, "steps": len(self.case["steps"])}
        for k in ("user", "wait", "expect"):
            if k in s:
                out[k] = s[k]
        return out

    def push(self, device, frame):
        d = self.devices[device]
        with self.lock:
            if d.socket is None:
                raise CaseError("frames for %s, whose socket is not open" % device)
            d.socket.write(self.tie(frame))

    def drop(self, device):
        d = self.devices[device]
        with self.changed:
            s, d.socket, d.accepting = d.socket, None, False
            self.changed.notify_all()
        if s is not None:
            s.close()

    def open(self, device, resumed, frames, timeout=APP_WAIT):
        """The case's `open`: the device's next socket (or the one waiting) gets this hello. Returns once it is open."""
        d = self.devices[device]
        with self.changed:
            d.next_open = {"resumed": resumed, "frames": copy.deepcopy(frames), "first": False}
            d.accepting = True
            self.changed.notify_all()
            end = self.now() + timeout
            while d.next_open is not None or d.socket is None:
                left = end - self.now()
                if left <= 0:
                    raise CaseError("%s did not connect again within %g s" % (device, timeout))
                self.changed.wait(left)

    def reply(self, r, timeout=APP_WAIT):
        """Answers every request of that device in flight for the write (its retries too)."""
        mine = lambda w: w["key"] == r["write"] and w["device"] == r["device"]
        with self.changed:
            end = self.now() + timeout
            while not [w for w in self.writes if mine(w)]:
                left = end - self.now()
                if left <= 0:
                    raise CaseError("reply to %s on %s: no write for it came within %g s" % (r["write"], r["device"], timeout))
                self.changed.wait(left)
            answering = [w for w in self.writes if mine(w)]
            self.writes = [w for w in self.writes if not mine(w)]
            status = r.get("status")
            done = r.get("done", status is not None and 200 <= status < 300)
            for w in answering:
                if done and r.get("receipt"):
                    self.receipts[w["op"]] = r["receipt"]
                elif done and not r.get("cut") and status:
                    self.receipts[w["op"]] = {"status": status, "body": r.get("body", {})}
            # a start carried out: from now on the chat exists (one op id starts one conversation, README "Steps")
            if done and any(w["kind"] == "conversations" for w in answering):
                self.started = True
                if not self.truth.state:
                    self.truth.state = "working"
        for w in answering:
            w["answer"] = None if r.get("cut") else (status or 200, copy.deepcopy(r.get("body", {})))
            w["event"].set()

    def release(self, device, path, timeout=APP_WAIT):
        d = self.devices[device]
        with self.changed:
            end = self.now() + timeout
            while not isinstance(d.holds.get(path), threading.Event):
                left = end - self.now()
                if left <= 0:
                    raise CaseError("release of %s: %s never read it within %g s" % (path, device, timeout))
                self.changed.wait(left)
            held = d.holds.pop(path)
        held.set()

    def close(self):
        """Done with: every socket closed, every held write cut, every held read answered."""
        with self.changed:
            self.closed = True
            socks = [d.socket for d in self.devices.values() if d.socket is not None]
            for d in self.devices.values():
                d.socket, d.accepting = None, False
                for path, held in list(d.holds.items()):
                    if isinstance(held, threading.Event):
                        held.set()
                d.holds.clear()
            writes, self.writes = self.writes, []
            self.changed.notify_all()
        for w in writes:
            w["event"].set()
        for s in socks:
            s.close()

    def state(self):
        with self.lock:
            return {"case": self.case["name"], "index": self.index, "steps": len(self.case["steps"]),
                    "done": self.index >= len(self.case["steps"]), "unknown": list(self.unknown),
                    "devices": {n: {"connected": d.socket is not None, "accepting": d.accepting, "connections": d.connections,
                                    "seen": list(d.seen),
                                    "in_flight": [{"kind": w["kind"], "key": w["key"]} for w in self.writes if w["device"] == n]}
                                for n, d in self.devices.items()}}


def serve_socket(h, hub):
    """The WebSocket of an app in a case (the handler `h` of GET /api/v1/ws): admitted when the case lets the device in,
    then the hello and snapshot, the case's frames, and what the app says (`client_hello`, `ping`, `subscribe`)."""
    import hashlib
    import socket
    import struct
    import select
    device = hub.device_of(token_of(h))

    def alive():  # the app still waits for its upgrade (a closed connection reads as end of file)
        try:
            readable, _, _ = select.select([h.connection], [], [], 0)
            return not readable or h.connection.recv(1, socket.MSG_PEEK) != b""
        except OSError:
            return False
    ticket = hub.admit(device, alive=alive)
    if ticket is None:
        h.send_response(503)
        h.end_headers()
        return
    key = h.headers.get("Sec-WebSocket-Key", "")
    accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
    try:
        h.send_response(101, "Switching Protocols")
        h.send_header("Upgrade", "websocket")
        h.send_header("Connection", "Upgrade")
        h.send_header("Sec-WebSocket-Accept", accept)
        h.end_headers()
        h.wfile.flush()
    except OSError:
        hub.unadmit(device, ticket)
        return
    lock = threading.Lock()

    def frame(op, data):
        n = len(data)
        hdr = bytes([0x80 | op]) + (bytes([n]) if n < 126 else bytes([126]) + struct.pack(">H", n) if n < 65536
                                    else bytes([127]) + struct.pack(">Q", n))
        with lock:
            h.wfile.write(hdr + data)
            h.wfile.flush()

    def write(obj):
        frame(1, json.dumps(obj, ensure_ascii=False).encode())

    def close():
        try:
            frame(8, struct.pack(">H", 1001) + b"case drop")
            h.connection.shutdown(socket.SHUT_RDWR)
        except OSError:
            pass

    conn = Conn(write, close)
    try:
        hub.opened(device, conn, ticket)
    except OSError:  # gone while the hello went out
        hub.gone(device, conn)
        hub.unadmit(device, ticket)
        return
    r = h.rfile
    try:
        while True:
            head = r.read(2)
            if len(head) < 2:
                break
            op, n = head[0] & 0x0F, head[1] & 0x7F
            if n == 126:
                n = struct.unpack(">H", r.read(2))[0]
            elif n == 127:
                n = struct.unpack(">Q", r.read(8))[0]
            mask = r.read(4) if head[1] & 0x80 else b"\0\0\0\0"
            payload = bytes(b ^ mask[i % 4] for i, b in enumerate(r.read(n)))
            if op == 8:
                break
            if op == 9:
                frame(10, payload)
                continue
            if op != 1:
                continue
            msg = json.loads(payload or b"{}")
            if msg.get("type") == "client_hello":
                write({"type": "client_hello_ack", "heartbeat": {"interval": 20, "pong_timeout": 15}})
            elif msg.get("type") == "ping" and isinstance(msg.get("id"), str):
                write({"type": "pong", "id": msg["id"]})
            else:
                hub.from_app(device, msg)
    except OSError:
        pass
    finally:
        hub.gone(device, conn)
