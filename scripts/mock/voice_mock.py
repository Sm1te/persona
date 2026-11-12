"""MOCK_VOICE for the iOS and web voice modes (docs/assistant/voice.md 6.8, 6.11; web/e2e/voice.mjs): the voice extension at protocol 2, beside the protocol 1 script
that mock_hub.py keeps for Android (docs/android/android.md"Voice conversations"). mock_hub.py loads it when MOCK_VOICE is set and hands
it its own module (`install`); everything here is made up.

What it adds, under /api/v1/extensions and the old /api/v1/plugins alike:
  GET  /api/v1/extensions                      the voice extension, running (needs_setup with MOCK_VOICE=setup)
  GET  …/voice/info                            protocol 2 and its features (404 with MOCK_VOICE=p1: an extension too old)
  GET  …/voice/-/                              its settings page (Settings › Extensions › the voice extension)
  GET  …/voice/models, …/voice/voices          as the protocol 1 mock has them; /voices fails, waits or says stale
                                               on demand (#2411): MOCK_VOICE_VOICES=fail|slow|slowfail|flaky|stale|
                                               none, the same per request with ?mock=…, or for the rest of the run
                                               with GET /mock/voice2?voices=fail|slow|slowfail|flaky|stale|none|ok
  GET  …/voice/ws                              a start with protocol 2 and a target gets the script below; any other
                                               start is handed to mock_hub.py's protocol 1 script unchanged
  POST …/voice/speak                           a text read aloud (docs/assistant/voice.md 4.15, #5004): `speak` in the
                                               features, and a quiet 440 Hz tone as audio/pcm at 24 kHz, streamed in
                                               100 ms pieces at about real time, MOCK_VOICE_SPEAK_SECONDS long (3 by
                                               default). MOCK_VOICE_SPEAK=ok|fail|slow|off: the tone / 502
                                               upstream_failed / the tone after 12 s (the phone's wait for the first
                                               audio runs out) / an extension from before it (no `speak`, a 404); or
                                               for the rest of the run GET /mock/voice2?speak=…&speak_seconds=…
  GET  /mock/voice2                            what the app sent (starts, texts, interrupts, resends, the PCM frames: how
                                               many, their bytes and sizes, how many were all zero, how many times it
                                               asked for the voices, the texts it had read aloud: speaks, newest last),
                                               the script's steps, how many protocol 2 sockets closed, the script's
                                               pace, and the speak mode and length
The model configuration's use plugin.voice comes from mock_hub.py's own MOCK_VOICE code.

The script (seconds after ready): the user's words come in as captions (partial, then final) and the voice hands them
over (run sending → sent → working, the instruction in the conversation, the conversation working); the upstream pauses
and comes back; a question card arrives and the voice asks it (ask asking; answered by voice at 21 s unless it was
tapped first: then released; the answer's caption comes after the voice began to reply, with before); a second
instruction is refused for the computer's unsent line (run refused, input_draft; resend picks it up); the first one is
done and read out. interrupt while the voice speaks: clear.
"""
import base64
import hashlib
import json
import math
import os
import struct
import threading
import time
import uuid
from datetime import datetime, timedelta, timezone

EXTENSION = "voice"
# MOCK_VOICE_PACE=0.1: the script ten times as fast (test_voice_mock.py); 1 for the app, at a pace a screen can follow.
PACE = float(os.environ.get("MOCK_VOICE_PACE") or "1")
FEATURES = ["target", "runs", "asks", "resume", "suspend"]
LOG = {"starts": [], "texts": [], "interrupts": 0, "resends": [], "audio": 0, "audio_bytes": 0, "audio_sizes": {}, "audio_silent": 0, "steps": [], "closes": 0, "voices": 0,
       "speaks": []}
# How POST …/speak answers (#5004): ok the tone, fail a 502 upstream_failed, slow the tone after SPEAK_SLOW seconds,
# off as an extension from before it (no `speak` in the features, the route a 404). seconds: how long the tone is.
SPEAK = {"mode": os.environ.get("MOCK_VOICE_SPEAK", "") or "ok",
         "seconds": float(os.environ.get("MOCK_VOICE_SPEAK_SECONDS") or "3")}
SPEAK_MODES = ("ok", "fail", "slow", "off")
SPEAK_SLOW = float(os.environ.get("MOCK_VOICE_SPEAK_SLOW") or "12")  # test_voice_mock.py shortens it
SPEAK_RATE = 24000
HUB_MODE = {"mode": lambda: "1"}  # MOCK_VOICE as install sees it (the extension's state)


def features():
    """What /info and ready list: speak unless the mock plays an extension from before it."""
    return FEATURES + ([] if SPEAK["mode"] == "off" else ["speak"])
PREFIXES = ("/api/v1/extensions", "/api/v1/plugins")
# How GET …/voices answers (#2411): "" its list; fail a 502 (the hub could not reach the extension); slow its list
# after VOICES_DELAY seconds (the pickers' Loading voices…); slowfail a 502 after VOICES_DELAY seconds (a failure
# that takes longer than a picker's reopen gap: no asking in a loop while the menu redraws); flaky a 502 for the
# first two asks, then its list (the pickers' retry); stale its list marked stale (the hub answered with the last
# good one); none an empty list (a model without voices: the pickers' None for this model).
VOICES = {"mode": os.environ.get("MOCK_VOICE_VOICES", ""), "failed": 0}
VOICES_DELAY = float(os.environ.get("MOCK_VOICE_VOICES_DELAY") or "6")
VOICES_MODES = ("", "ok", "fail", "slow", "slowfail", "flaky", "stale", "none")

# The script's steps, as (seconds after ready, name); the names are what /mock/voice2 lists as they happen.
STEPS = [
    (1.2, "partial"), (2.6, "final"), (3.2, "sending"), (3.6, "handed"), (4.4, "sent"), (5.2, "working"),
    (6.4, "quiet"), (8.0, "paused"), (11.0, "live"), (12.0, "ask"), (21.0, "answered"), (23.0, "second"),
    (24.0, "refused"), (30.0, "done"), (35.0, "quiet"),
]


def _now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def install(hub):
    """Wraps mock_hub.py's voice routes with protocol 2; returns the new voice_route."""
    old_route = hub.voice_route

    def mode():
        return hub.VOICE_MODE or "1"

    def extension():  # as mock_hub.py's protocol 1 list had it (the Android screenshots keep their words)
        if mode() == "off":
            return {"id": EXTENSION, "name": "语音", "kind": "voice", "status": "error", "error": "插件进程退出了"}
        if mode() == "setup":
            return {"id": EXTENSION, "name": "语音", "kind": "voice", "status": "needs_setup",
                    "needs_setup": ["还没填实时语音服务的 Key"]}
        return {"id": EXTENSION, "name": "语音", "kind": "voice", "status": "running"}

    def detail():
        """Settings › Extensions › the voice extension: the built-in one's form, its key set."""
        fields = [
            {"name": "url", "type": "string", "title": "服务地址", "format": "uri",
             "description": "兼容 GPT-Live 的会话地址，wss://<主机>/v1/live"},
            {"name": "dialect", "type": "string", "title": "协议", "default": "live",
             "enum": [{"value": "live", "title": "GPT-Live"}, {"value": "codex", "title": "Codex (gpt-live-1-codex)"}]},
        ]
        return {**extension(), "version": "202611090000", "source": "builtin", "enabled": True,
                "description": "和当前对话实时语音：快模型听你说、替你转交，慢活交给 Agent。", "contributes": {"voice": ["voice"]},
                "form": {"fields": fields}, "config": {"url": "wss://voice.example.com/v1/live", "dialect": "live"},
                "secrets": [{"name": "key", "title": "API Key", "set": mode() != "setup"}],
                "permissions": [{"key": "hub_api", "title": "完整使用 hub",
                                 "detail": "拿到 hub 的地址和 token，能做 hub 主人能做的一切：读知识库、驱动各台电脑上的 Agent……"}]}

    def voices(handler, q):
        LOG["voices"] += 1
        how = (q.get("mock") or [VOICES["mode"]])[0]
        if how == "flaky" and VOICES["failed"] < 2:
            VOICES["failed"] += 1
            how = "fail"
        if how == "slowfail":
            time.sleep(VOICES_DELAY)
            how = "fail"
        if how == "fail":
            handler.send_json({"error": "plugin voice is not answering: connection refused"}, 502)
            return
        if how == "slow":
            time.sleep(VOICES_DELAY)
        out = dict(hub.VOICE_VOICES.get(q.get("model", [hub.VOICE_MODELS["default"]])[0], {"voices": []}))
        if how == "none":
            out = {"voices": []}
        if how == "stale":
            out["stale"] = True
        handler.send_json(out)

    def route(handler, p, q):
        if not hub.VOICE_MODE:
            return False
        if p == "/mock/voice2":
            how = (q.get("voices") or [None])[0]
            if how is not None and how in VOICES_MODES:
                VOICES["mode"], VOICES["failed"] = ("" if how == "ok" else how), 0
            speak_how = (q.get("speak") or [None])[0]
            if speak_how in SPEAK_MODES:
                SPEAK["mode"] = speak_how
            try:
                SPEAK["seconds"] = max(0.0, min(60.0, float((q.get("speak_seconds") or [SPEAK["seconds"]])[0])))
            except ValueError:
                pass
            handler.send_json(dict(LOG, pace=PACE, voices_mode=VOICES["mode"] or "ok", speak_mode=SPEAK["mode"],
                                   speak_seconds=SPEAK["seconds"]))
            return True
        if p == "/api/v1/extensions":
            handler.send_json({"extensions": [extension()]})
            return True
        if p == "/api/v1/plugins":
            handler.send_json({"plugins": [extension()]})
            return True
        for prefix in PREFIXES:
            base = prefix + "/" + EXTENSION + "/"
            if not p.startswith(base):
                continue
            rest = p[len(base):]
            if mode() == "off":
                handler.send_json({"error": "no such extension"}, 404)
            elif rest == "info":
                if mode() == "p1":
                    handler.send_json({"error": "not found"}, 404)
                else:
                    setup = ["还没填实时语音服务的 Key"] if mode() == "setup" else []
                    handler.send_json({"protocol": 2, "features": features(), "setup": setup, "warnings": []})
            elif rest == "models":
                handler.send_json(hub.VOICE_MODELS)
            elif rest == "voices":
                voices(handler, q)
            elif rest in ("-", "-/") and handler.command == "GET":
                handler.send_json(detail())
            elif rest == "ws":
                serve(hub, handler)
            else:
                handler.send_json({"error": "not found"}, 404)
            return True
        return old_route(handler, p, q)

    hub.MOCK_HELP.append("MOCK_VOICE=setup|p1  语音扩展：需要设置（还没填 Key）/ 协议 1 的旧扩展（/info 404）；"
                         "iOS 语音模式的协议 2 脚本见 scripts/mock/voice_mock.py，GET /mock/voice2 看 App 发了什么")
    hub.MOCK_HELP.append("MOCK_VOICE_VOICES=fail|slow|slowfail|flaky|stale|none  语音扩展的音色列表：502 / 晚 MOCK_VOICE_VOICES_DELAY 秒（默认 6）/ "
                         "晚 MOCK_VOICE_VOICES_DELAY 秒再 502 / 前两次 502 / 标成 stale / 空列表；运行中用 GET /mock/voice2?voices=… 改（#2411）")
    hub.MOCK_HELP.append("MOCK_VOICE_SPEAK=ok|fail|slow|off  POST …/voice/speak (a text read aloud, #5004): a quiet tone of "
                         "MOCK_VOICE_SPEAK_SECONDS seconds (default 3) as audio/pcm at 24 kHz, at about real time / 502 "
                         "upstream_failed / the tone after 12 s / an extension from before it (no speak feature, 404); "
                         "while it runs: GET /mock/voice2?speak=…&speak_seconds=…")
    # mock_hub.py's do_POST has no voice hook: the speak route goes in front of the first route it asks
    # (custom_models_route, defined before this module is loaded and looked up by name on every request).
    old_post_route = hub.custom_models_route

    def post_route(h, method, p, q, body=b""):
        if method == "POST" and hub.VOICE_MODE and any(p == prefix + "/" + EXTENSION + "/speak" for prefix in PREFIXES):
            speak(h, body)
            return True
        return old_post_route(h, method, p, q, body)

    hub.custom_models_route = post_route
    HUB_MODE["mode"] = mode
    return route


# ---------- reading a text aloud (POST …/speak) ----------

def tone(seconds):
    """A quiet 440 Hz tone, PCM16 little-endian mono at SPEAK_RATE: what the mock's voice says."""
    n = int(seconds * SPEAK_RATE)
    return b"".join(struct.pack("<h", int(1600 * math.sin(2 * math.pi * 440 * i / SPEAK_RATE))) for i in range(n))


def speak(h, body):
    """POST …/speak as the built-in extension answers it (server/internal/voice/speak.go): the text logged, then the
    voice as it comes (chunked, 100 ms at a time), or an error before any audio."""
    try:
        req = json.loads(body or b"{}")
    except ValueError:
        req = None
    if not isinstance(req, dict):
        h.send_json({"error": "the body is JSON: {text, model?, voice?, lang?}", "code": "bad_request"}, 400)
        return
    mode = SPEAK["mode"]
    voice = HUB_MODE["mode"]()
    if mode == "off" or voice in ("off", "p1"):  # an extension from before it, or none running
        h.send_json({"error": "not found"}, 404)
        return
    LOG["speaks"].append({k: req.get(k) for k in ("text", "model", "voice", "lang")})
    if voice == "setup":
        h.send_json({"error": "no realtime voice service is set up yet", "code": "no_upstream"}, 503)
        return
    if not str(req.get("text") or "").strip():
        h.send_json({"error": "no text to read", "code": "bad_request"}, 400)
        return
    if mode == "fail":
        h.send_json({"error": "the realtime voice service did not read it: HTTP 500", "code": "upstream_failed"}, 502)
        return
    if mode == "slow":
        time.sleep(SPEAK_SLOW)
    pcm = tone(SPEAK["seconds"])
    piece = SPEAK_RATE * 2 // 10  # 100 ms
    try:
        h.send_response(200)
        h.send_header("Content-Type", "audio/pcm")
        h.send_header("X-Persona-Audio-Rate", str(SPEAK_RATE))
        h.send_header("Cache-Control", "no-store")
        h.send_header("Transfer-Encoding", "chunked")
        h.end_headers()
        for i in range(0, len(pcm), piece):
            chunk = pcm[i:i + piece]
            h.wfile.write(b"%x\r\n%s\r\n" % (len(chunk), chunk))
            h.wfile.flush()
            if i + piece < len(pcm):
                time.sleep(0.1)
        h.wfile.write(b"0\r\n\r\n")
        h.wfile.flush()
    except OSError:
        h.close_connection = True  # the phone hung up (Stop Speaking, the next answer)


# ---------- the socket ----------

class _Recorder:
    """The request's input, keeping what was read so a protocol 1 start can be read again by the old script."""

    def __init__(self, raw):
        self.raw = raw
        self.seen = bytearray()

    def read(self, n):
        data = self.raw.read(n)
        self.seen += data
        return data


class _Replay:
    """The handler for mock_hub.py's protocol 1 script, after this module took the upgrade: the bytes it read come
    first, and the upgrade it would answer is already sent."""

    def __init__(self, h, seen):
        self._h = h
        self.headers = h.headers
        self.wfile = h.wfile
        self.rfile = _Prefixed(bytes(seen), h.rfile)

    def send_response(self, *a): pass
    def send_header(self, *a): pass
    def end_headers(self): pass

    def __setattr__(self, name, value):
        # close_connection and the like are the real handler's.
        if name in ("headers", "wfile", "rfile", "_h"):
            object.__setattr__(self, name, value)
        else:
            setattr(self._h, name, value)


class _Prefixed:
    """`first`, then the rest of the connection (only `read(n)`, as the script reads it)."""

    def __init__(self, first, rest):
        self.first, self.rest = first, rest

    def read(self, n):
        out, self.first = self.first[:n], self.first[n:]
        return out + self.rest.read(n - len(out)) if len(out) < n else out


def _upgrade(h):
    key = h.headers.get("Sec-WebSocket-Key", "")
    accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest()).decode()
    h.send_response(101, "Switching Protocols")
    h.send_header("Upgrade", "websocket")
    h.send_header("Connection", "Upgrade")
    h.send_header("Sec-WebSocket-Accept", accept)
    h.end_headers()
    h.wfile.flush()


def _read_frame(r):
    """(opcode, payload) of the next frame, or None when the connection closed."""
    hd = r.read(2)
    if len(hd) < 2:
        return None
    op, n = hd[0] & 0x0F, hd[1] & 0x7F
    if n == 126:
        n = struct.unpack(">H", r.read(2))[0]
    elif n == 127:
        n = struct.unpack(">Q", r.read(8))[0]
    mask = r.read(4) if hd[1] & 0x80 else b"\0\0\0\0"
    data = r.read(n)
    return op, bytes(b ^ mask[i % 4] for i, b in enumerate(data))


def serve(hub, h):
    _upgrade(h)
    rec = _Recorder(h.rfile)
    first = None
    while first is None:
        frame = _read_frame(rec)
        if frame is None:
            return
        op, payload = frame
        if op == 1:
            try:
                first = json.loads(payload or b"{}")
            except ValueError:
                first = {}
    if first.get("type") != "start" or (first.get("protocol") or 1) < 2 or not first.get("target"):
        # Android and other protocol 1 clients: the old script, from the frame it was sent.
        hub.voice_socket(_Replay(h, rec.seen))
        return
    Call(hub, h, first).run()


class Call:
    def __init__(self, hub, h, start):
        self.hub, self.h, self.start = hub, h, start
        self.lock = threading.Lock()
        self.open = True
        self.speaking = False
        self.cut = threading.Event()
        target = start.get("target") or {}
        self.mid, self.sid = target.get("machine_id", ""), target.get("session_id", "")
        # the hub's own row, not all_sessions()'s copy: the state changes below are what its sessions push sends
        self.session = next((s for s in hub.SESSIONS + hub.ASSISTANT if s["machine_id"] == self.mid and s["session_id"] == self.sid), None)
        self.label = "Persona" if (self.session or {}).get("assistant") else "Claude"
        self.runs = {}
        self.ask = None
        # this call made the conversation work and its script has not reached done yet
        self.left_working = False
        LOG["starts"].append({k: start.get(k) for k in ("model", "voice", "protocol", "target", "recent", "history", "resume")})

    # frames out

    def frame(self, op, data):
        n = len(data)
        hdr = bytes([0x80 | op]) + (bytes([n]) if n < 126 else bytes([126]) + struct.pack(">H", n) if n < 65536 else bytes([127]) + struct.pack(">Q", n))
        with self.lock:
            if not self.open:
                return
            self.h.wfile.write(hdr + data)
            self.h.wfile.flush()

    def say(self, obj):
        self.frame(0x1, json.dumps(obj, ensure_ascii=False).encode())

    def caption(self, cid, role, text, final=True, before=None):
        frame = {"type": "transcript", "id": cid, "role": role, "text": text, "final": final}
        if before:
            frame["before"] = before
        self.say(frame)

    def run_frame(self, rid, state, **kw):
        run = self.runs.setdefault(rid, {"type": "run", "id": rid, "msg_ids": [rid], "base_seq": self._base(), "started_at": _now()})
        run.update(kw, state=state)
        self.say(dict(run))

    def link(self, state):
        self.say({"type": "link", "state": state})

    def speak(self, on):
        if on and not self.speaking:
            self.speaking = True
            threading.Thread(target=self._audio, daemon=True).start()
        elif not on:
            self.speaking = False

    def _audio(self):  # 20 ms of 24 kHz PCM16 silence every 20 ms while the voice speaks
        chunk = bytes(960)
        while self.open and self.speaking:
            try:
                self.frame(0x2, chunk)
            except OSError:
                return
            time.sleep(0.02)

    # the conversation, as the hub would change it

    def _base(self):
        items = self.hub.ITEMS.get(self.sid) or []
        return items[-1]["seq"] if items else 0

    def _items(self, new):
        items = self.hub.ITEMS.get(self.sid)
        if items is None:
            return
        before = items[-1]["seq"] if items else 0
        self.hub.ITEMS[self.sid] = items + new
        for send, _ in list(self.hub.LIVE):
            try:
                send({"type": "items", "machine_id": self.mid, "session_id": self.sid, "items": new, **self.hub.span(new, before)})
            except Exception:
                pass

    def _state(self, state, activity=None):
        if self.session is None:
            return
        self.session.update(state=state, status="working" if state == "working" else "idle", state_since=_now(), updated_at=_now(),
                            activity=activity)
        self.hub.push_machine(self.mid)

    def _send_instruction(self, rid, task):
        self._items([self.hub.it("user", "text", 0, text=task, client_msg_id=rid)])
        self._state("working", "在跑测试")
        self.left_working = True

    def _card(self):
        ix = {"id": "ix_voice_%s" % uuid.uuid4().hex[:8], "machine_id": self.mid, "session_id": self.sid,
              "session_title": (self.session or {}).get("title", ""), "kind": "question", "tool_name": "AskUserQuestion",
              "created_at": _now(), "expires_at": (datetime.now(timezone.utc) + timedelta(minutes=30)).isoformat().replace("+00:00", "Z"),
              "questions": [{"question": "数据库用哪个？", "header": "数据库", "multi_select": False, "options": [
                  {"label": "Postgres (Recommended)", "description": "和线上一样，迁移脚本不用改"},
                  {"label": "SQLite", "description": "本地开发更轻，不用起容器"}]}]}
        self.hub.INTERACTIONS.append(ix)
        for send, _ in list(self.hub.LIVE):
            try:
                send({"type": "interaction", "interaction": ix})
            except Exception:
                pass
        return ix["id"]

    def _pending(self, ix_id):
        return any(x["id"] == ix_id for x in self.hub.INTERACTIONS)

    def _answer(self, ix_id):
        self.hub.INTERACTIONS[:] = [x for x in self.hub.INTERACTIONS if x["id"] != ix_id]
        for send, _ in list(self.hub.LIVE):
            try:
                send({"type": "interaction_resolved", "id": ix_id})
            except Exception:
                pass

    # the script

    def step(self, name):
        LOG["steps"].append(name)
        first, second = "voice-7c1e4a90b2d35f68", "voice-2b8d0e6f4a1c9375"
        task = "修登录页失败的测试" if self.label == "Claude" else "查明天下午的日程"
        if name == "partial":
            self.caption("u1", "user", "帮我把登录页", final=False)
        elif name == "final":
            self.caption("u1", "user", "帮我把登录页那个失败的测试修一下" if self.label == "Claude" else "查一下明天下午有什么会")
        elif name == "sending":
            self.run_frame(first, "sending", task=task)
        elif name == "handed":
            self.caption("a1", "assistant", "好，交给 Claude 了。" if self.label == "Claude" else "好，这就去查。")
            self.speak(True)
        elif name == "sent":
            self._send_instruction(first, task)
            self.run_frame(first, "sent")
        elif name == "working":
            self.run_frame(first, "working")
        elif name == "quiet":
            self.speak(False)
        elif name == "paused":
            self.link("paused")
        elif name == "live":
            self.link("live")
        elif name == "ask":
            self.ask = self._card()
            self.run_frame(first, "needs_you")
            self.say({"type": "ask", "interaction_id": self.ask, "state": "asking"})
            self.caption("a2", "assistant", "%s 问：数据库用哪个？Postgres，还是 SQLite？" % self.label)
        elif name == "answered":
            if self.ask and self._pending(self.ask):
                self._answer(self.ask)
                self.say({"type": "ask", "interaction_id": self.ask, "state": "answered", "by": "voice"})
                # A late caption (#5673): the voice begins to answer before the user's caption comes, which goes
                # before that answer; the answer goes on in its own line.
                self.caption("a3", "assistant", "好，", final=False)
                self.caption("u3", "user", "第一个", before="a3")
                self.caption("a3", "assistant", "好，告诉它了。")
            elif self.ask:
                self.say({"type": "ask", "interaction_id": self.ask, "state": "released", "by": "elsewhere"})
            self.run_frame(first, "working")
        elif name == "second":
            self.caption("u4", "user", "顺便把 lint 也修了")
            self.run_frame(second, "sending", task="顺便把 lint 也修了")
        elif name == "refused":
            self.run_frame(second, "refused", code="input_draft", message="电脑上 Claude 的输入框里有没发出去的内容",
                           draft="[⧉ Selected 3 lines from foo.go] fix it", clearable="fix it")
            self.caption("a4", "assistant", "%s 的输入框里有没发出去的字，要清空再发吗？" % self.label)
        elif name == "done":
            answer = "登录页的测试修好了，改了 Login.tsx 和 session.go 两处。"
            self._items([self.hub.it("assistant", "text", 0, text=answer)])
            self._state("idle")
            self.left_working = False
            self.run_frame(first, "done")
            self.caption("a5", "assistant", answer)
            self.speak(True)

    def script(self, began):
        for at, name in STEPS:
            if self.cut.wait(max(began + at * PACE - time.monotonic(), 0)) or not self.open:
                return
            try:
                self.step(name)
            except OSError:
                return

    def resend(self, msg):
        rid = msg.get("id", "")
        LOG["resends"].append({"id": rid, "clear_draft": msg.get("clear_draft")})
        if rid not in self.runs:
            return
        self.run_frame(rid, "sending")
        self._send_instruction(rid, self.runs[rid].get("task", ""))
        self.run_frame(rid, "working", code=None, draft=None, clearable=None, message=None)

    def run(self):
        target = self.start.get("target") or {}
        self.say({"type": "ready", "protocol": 2, "features": features(), "input_rate": 16000, "output_rate": 24000,
                  "model": self.start.get("model") or self.hub.VOICE_MODELS["default"], "voice": self.start.get("voice"),
                  "target": {"machine_id": target.get("machine_id"), "session_id": target.get("session_id"),
                             "kind": "main" if self.label == "Persona" else "agent",
                             "title": (self.session or {}).get("title", ""),
                             "agent_label": "" if self.label == "Persona" else "Claude Code", "machine_name": self.mid}})
        threading.Thread(target=self.script, args=(time.monotonic(),), daemon=True).start()
        r = self.h.rfile
        try:
            while True:
                frame = _read_frame(r)
                if frame is None:
                    break
                op, payload = frame
                if op == 2:
                    LOG["audio"] += 1
                    LOG["audio_bytes"] += len(payload)
                    size = str(len(payload))
                    LOG["audio_sizes"][size] = LOG["audio_sizes"].get(size, 0) + 1
                    if not any(payload):  # all zero: what a muted microphone sends
                        LOG["audio_silent"] += 1
                    continue
                if op == 8:
                    break
                if op == 9:
                    self.frame(0xA, payload)
                    continue
                if op != 1:
                    continue
                msg = json.loads(payload or b"{}")
                kind = msg.get("type")
                self.hub.LOG.write("  VOICE2 %s\n" % kind)
                self.hub.LOG.flush()
                if kind == "interrupt":
                    LOG["interrupts"] += 1
                    if self.speaking:
                        self.speak(False)
                        self.say({"type": "clear"})
                elif kind == "text":
                    LOG["texts"].append(msg.get("text", ""))
                    self.caption("t%d" % len(LOG["texts"]), "user", msg.get("text", ""))
                elif kind == "resend":
                    self.resend(msg)
        except OSError:
            pass
        finally:
            self.open = False
            self.cut.set()
            self.h.close_connection = True
            # A call that closed before its script's done (Continue's, or the one a new voice replaced) leaves the
            # conversation as it found it: the next call's script starts from an idle conversation, whose line shows the
            # instruction being handed over (web/e2e/voice.mjs waits for it), not the work this one left behind.
            if self.left_working:
                self._state("idle")
            LOG["closes"] += 1
