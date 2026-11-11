"""The mock hub attends a recorded meeting as the contract says (#2981, docs/assistant/meeting-minutes.md "Attending a
meeting"): the feature and the default role, the upload's attendance fields, the live log and tap-to-ask, in the
contract's order of checks; and a hub without it (MOCK_NO_MEETING_ATTENDANCE=1) behaves as before."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import unittest
import uuid
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
            raise RuntimeError("mock hub exited before startup")
        try:
            with urlopen(base + "/api/v1/health", timeout=1):
                break
        except (URLError, TimeoutError):
            time.sleep(0.1)
    else:
        raise RuntimeError("mock hub startup timed out")

    def stop():
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        log.close()
    return base, stop


class Hub:
    def call(self, method, path, body=None, headers=None):
        data = None if body is None else (body if isinstance(body, bytes) else json.dumps(body).encode())
        request = Request(self.base + path, data=data, method=method, headers={"Content-Type": "application/json", **(headers or {})})
        try:
            with urlopen(request, timeout=10) as response:
                return response.status, json.load(response)
        except HTTPError as e:
            return e.code, json.load(e)

    def upload(self, rid, **fields):
        body = {"revision": int(time.time() * 1000), "started_at": "2026-11-10T14:00:00+08:00", "device": "phone", "language": "en",
                "kind": "meeting", "title": "Design review", "segments": [], "complete": False, **fields}
        return self.call("PUT", f"/api/v1/recordings/{rid}", body)


class MeetingAttendanceMockTests(Hub, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({})
        cls.addClassCleanup(stop)

    def setUp(self):
        self.call("POST", "/mock/meeting_attendance", {"feed": "ok", "answer": "ok", "answer_seconds": 3, "kb_read_only": False})

    def test_feature_and_default_role(self):
        self.assertIn("meeting_attendance", self.call("GET", "/api/v1/health")[1]["features"])
        status, state = self.call("GET", "/api/v1/assistant")
        self.assertEqual((status, state["meeting_attendance_default"], state["manager"]), (200, "off", "assistant"))
        self.assertEqual(self.call("PUT", "/api/v1/assistant", {"meeting_attendance_default": "listener"})[1]["meeting_attendance_default"], "listener")
        self.assertEqual(self.call("PUT", "/api/v1/assistant", {"meeting_attendance_default": "loud"})[0], 400)
        status, refused = self.call("PUT", "/api/v1/assistant", {"meeting_attendance_default": "participant"}, {"X-Mock-Role": "answer"})
        self.assertEqual((status, refused["code"]), (403, "forbidden"))
        self.assertEqual(self.call("GET", "/api/v1/assistant")[1]["meeting_attendance_default"], "listener")
        self.call("PUT", "/api/v1/assistant", {"meeting_attendance_default": "off"})
        other = self.call("GET", "/api/v1/assistant", headers={"X-Persona-Manager": "m-3fa21c"})[1]
        self.assertNotIn("meeting_attendance_default", other)
        self.assertEqual(other["manager"], "m-3fa21c")

    def test_upload_fields_never_fail_and_reach_the_detail(self):
        rid = str(uuid.uuid4())
        session = {"machine_id": "macbook", "session_id": "a1f3c2d4-0000-4000-8000-000000000006", "extra": 1}
        status, out = self.upload(rid, attendance="participant", attending_session=session)
        self.assertEqual((status, out["attendance"]), (200, "participant"))
        self.assertNotIn("attendance_problem", out)
        meta = self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"]
        self.assertEqual((meta["attendance"], meta["attending_session"]), ("participant", {"machine_id": "macbook", "session_id": session["session_id"]}))
        self.assertEqual(meta["minutes_state"], "pending")
        for fields, problem in [({"attendance": "loud"}, "unknown_role"), ({"attendance": "listener", "kind": "note"}, "not_meeting"),
                                ({"attendance": "listener", "attending_session": {"machine_id": "macbook"}}, "bad_session"),
                                ({"attendance": "listener", "attending_session": {"machine_id": "m", "session_id": "s" * 201}}, "bad_session")]:
            status, out = self.upload(rid, **fields)
            self.assertEqual((status, out["status"], out["attendance"], out["attendance_problem"]), (200, "stored", "off", problem), fields)
            self.assertNotIn("attendance", self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"])
        status, out = self.upload(rid, attendance="listener", attending_session={"machine_id": "", "session_id": ""})
        self.assertEqual((out["attendance"], "attendance_problem" in out), ("listener", False))
        self.assertNotIn("attending_session", self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"])
        latest = self.upload(rid)[1]  # omitted: attending turns off
        self.assertEqual(latest["attendance"], "off")
        # Stale, as the hub answers: the upload's own revision, the stored role.
        self.assertEqual(self.upload(rid, revision=1, attendance="listener")[1], {"id": rid, "revision": 1, "status": "stale", "attendance": "off"})
        self.upload(rid, complete=True, ended_at="2026-11-10T15:00:00+08:00")
        self.assertNotIn("minutes_state", self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"])

    def test_upload_attendance_is_settled_as_the_hub_does(self):
        """The hub's normalizeAttendance (server/internal/kb/live.go) after clean() (kb/recordings.go), case by case."""
        session = {"machine_id": "macbook", "session_id": "a1f3c2d4-0000-4000-8000-000000000006"}
        for fields, role, problem, stored in [
            # Off or absent says nothing, whatever the session, and stores none.
            ({"attendance": "off", "attending_session": session}, "off", None, None),
            ({"attendance": "off", "attending_session": {"machine_id": "macbook"}}, "off", None, None),
            ({"attending_session": {"machine_id": "macbook"}}, "off", None, None),
            ({"attendance": None, "attending_session": "macbook"}, "off", None, None),
            ({"attendance": " OFF ", "attending_session": session, "kind": "note"}, "off", None, None),
            # A role it cannot take keeps no session either.
            ({"attendance": "loud", "attending_session": session}, "off", "unknown_role", None),
            ({"attendance": 5, "attending_session": session}, "off", "unknown_role", None),
            ({"attendance": "listener", "attending_session": session, "kind": "note"}, "off", "not_meeting", None),
            ({"attendance": "listener", "attending_session": session, "kind": None}, "off", "not_meeting", None),
            ({"attendance": "listener", "attending_session": ["macbook", "s1"]}, "off", "bad_session", None),
            ({"attendance": "listener", "attending_session": {"machine_id": 5, "session_id": "s1"}}, "off", "bad_session", None),
            ({"attendance": "listener", "attending_session": {"machine_id": "  ", "session_id": "s1"}}, "off", "bad_session", None),
            # Roles, kinds and ids are trimmed (and the role and kind lowercased) as the hub does.
            ({"attendance": " Listener ", "kind": " Meeting ", "attending_session": {"machine_id": " macbook ", "session_id": " s1 "}},
             "listener", None, {"machine_id": "macbook", "session_id": "s1"}),
            ({"attendance": "PARTICIPANT", "attending_session": {"machine_id": None, "session_id": None}}, "participant", None, None),
            # A watch recording is stored as the hub stores it (the phone never gives one a role).
            ({"attendance": "listener", "device": "watch", "attending_session": session}, "listener", None, session),
        ]:
            with self.subTest(fields=fields):
                rid = str(uuid.uuid4())
                status, out = self.upload(rid, **fields)
                self.assertEqual((status, out["status"], out["attendance"], out.get("attendance_problem")), (200, "stored", role, problem))
                meta = self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"]
                self.assertEqual((meta.get("attendance"), meta.get("attending_session")), (None if role == "off" else role, stored))
                self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live")[1]["attendance"], role)
        rid = str(uuid.uuid4())
        self.upload(rid, kind=" Meeting ", attendance="listener")
        meta = self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"]
        self.assertEqual((meta["kind"], meta["minutes_state"]), ("meeting", "pending"))
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0})[0], 200)
        for kind in ["lecture", 5]:  # clean() refuses an unknown kind before anything else
            self.assertEqual(self.upload(str(uuid.uuid4()), kind=kind)[0], 400, kind)
            self.assertEqual(self.upload(rid, revision=1, kind=kind)[0], 400, kind)

    def test_a_stale_upload_still_says_what_was_wrong(self):
        rid = str(uuid.uuid4())
        self.upload(rid, attendance="listener")
        # Nothing is written; the role in effect is the stored one, and the problem is this upload's, as on a stored one.
        for fields, problem in [({"attendance": "loud"}, "unknown_role"), ({"attendance": "listener", "kind": "note"}, "not_meeting"),
                                ({"attendance": "participant", "attending_session": {"machine_id": "macbook"}}, "bad_session"),
                                ({"attendance": "off", "attending_session": {"machine_id": "macbook"}}, None), ({"attendance": "participant"}, None)]:
            with self.subTest(fields=fields):
                want = {"id": rid, "revision": 1, "status": "stale", "attendance": "listener", **({"attendance_problem": problem} if problem else {})}
                self.assertEqual(self.upload(rid, revision=1, **fields), (200, want))
        self.assertEqual(self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"]["attendance"], "listener")

    def test_live_log_is_append_only_and_checked_in_order(self):
        rid = str(uuid.uuid4())
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": []})
        self.assertEqual((status, out["code"]), (404, "recording_not_found"))
        self.upload(rid, attendance="listener")
        lines = [{"start": -2, "end": -5, "text": "Let's keep the cap."}, {"start": 61.2, "end": 64, "text": "é" * 2001}]
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": lines})
        self.assertEqual(status, 200)
        self.assertEqual((out["next"], out["fed"], out["attendance"]), (2, 2, "listener"))
        self.assertEqual(out["attending_session"]["main"], True)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": lines})[1]["next"], 2)
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 5, "segments": lines})
        self.assertEqual((status, out["code"], out["next"]), (409, "live_gap", 2))
        for bad in [{"segments": []}, {"from": -1}, {"from": 1.5}, {"from": True}, {"from": 2, "segments": [{"start": "x"}]},
                    {"from": 2, "segments": [{"start": 1, "end": 2, "text": "x"}] * 201}]:
            self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", bad)[0], 400, bad)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", b"x" * ((1 << 20) + 1))[1]["code"], "too_large")
        stored = self.call("GET", "/mock/meeting_attendance")[1]["live"][rid]["segments"]
        self.assertEqual(stored[0], {"start": 0.0, "end": 0.0, "text": "Let's keep the cap."})
        self.assertEqual(len(stored[1]["text"].encode()), 4000)
        live = self.call("GET", f"/api/v1/recordings/{rid}/live")[1]
        self.assertEqual((live["next"], live["asks"]), (2, []))
        self.call("POST", "/mock/meeting_attendance", {"feed": "busy"})
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 2, "segments": [{"start": 70, "end": 72, "text": "Next"}]})[1]["fed"], 2)
        self.call("POST", "/mock/meeting_attendance", {"feed": "not_allowed"})
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live")[1]["problem"], "attendee_not_allowed")
        self.call("POST", "/mock/meeting_attendance", {"feed": "ok", "kb_read_only": True})
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 3})[1]["code"], "kb_read_only")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live")[0], 200)  # reading works on a read-only knowledge base
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": False})
        self.upload(rid, attending_session={"machine_id": "studio", "session_id": "gone"}, attendance="listener")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live")[1]["problem"], "attendee_unavailable")
        self.upload(rid)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 3})[1]["code"], "not_attending")
        live = self.call("GET", f"/api/v1/recordings/{rid}/live")[1]
        # revs: a manager with meeting_conversation (MOCK_NO_MEETING_CONVERSATION=1 is one without it).
        revs = {} if os.environ.get("MOCK_NO_MEETING_CONVERSATION") == "1" else {"revs": {"asks": 0}}
        self.assertEqual(live, {"next": 3, "attendance": "off", "fed": 3, "asks": [], **revs})
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live", headers={"X-Mock-Role": "watch"})[1]["code"], "forbidden")
        self.call("DELETE", f"/api/v1/recordings/{rid}")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0})[1]["code"], "recording_deleted")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live")[1]["code"], "recording_deleted")

    def test_ask_answers_once_per_id(self):
        earlier = str(uuid.uuid4())
        # Marked not private: with the meeting conversation (#3862) only such a meeting is material for another's answers.
        self.upload(earlier, complete=True, segments=[{"start": 0, "end": 3, "text": "Keep the cap."}], private=False)
        rid = str(uuid.uuid4())
        self.upload(rid, attendance="participant")
        self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": [{"start": 1800, "end": 1806, "text": "Did we decide on the cap?"}]})
        aid = str(uuid.uuid4()).upper()
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "What did we decide last time?"})
        ask = out["ask"]
        self.assertEqual(status, 200)
        self.assertEqual((ask["id"], ask["state"], ask["question_source"], ask["speak"], ask["lang"]), (aid.lower(), "answered", "typed", True, "en"))
        self.assertTrue(ask["answer"] and len(ask["answer"]) <= 400 and ask["answered_at"].endswith("Z"))
        self.assertEqual(ask["sources"][0], {"kind": "recordings", "id": earlier, "name": "Design review", "when": "2026-11-03 14:00–15:10",
                                             "start": "2026-11-03T14:00:00+08:00", "end": "2026-11-03T15:10:00+08:00"})
        self.assertNotIn("at", ask)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid.lower(), "question": "What did we decide last time?"})[1]["ask"], ask)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "Another?"})[1]["code"], "op_id_reused")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "What did we decide last time?", "at": 3})[1]["code"], "op_id_reused")
        heard = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "at": 1830, "live_next": 1})[1]["ask"]
        self.assertEqual((heard["question"], heard["question_source"], heard["at"], heard["state"]), ("Did we decide on the cap?", "heard", 1830, "answered"))
        quiet = str(uuid.uuid4())
        nothing = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": quiet, "at": 10})[1]["ask"]
        self.assertEqual((nothing["state"], nothing["error"], nothing["answer"], nothing["sources"]), ("failed", "nothing_heard", "", []))
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": quiet, "at": 10})[1]["ask"]["error"], "nothing_heard")  # tried again
        for bad in [{"question": "x"}, {"id": "nope", "question": "x"}, {"id": str(uuid.uuid4())}, {"id": str(uuid.uuid4()), "question": "x" * 1001},
                    {"id": str(uuid.uuid4()), "at": "soon"}]:
            self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", bad)[0], 400, bad)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", b"x" * ((64 << 10) + 1))[1]["code"], "too_large")
        asks = self.call("GET", f"/api/v1/recordings/{rid}/live")[1]["asks"]
        self.assertEqual([a["id"] for a in asks], [aid.lower(), heard["id"], quiet])
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": True})
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "What did we decide last time?"})[0], 200)  # stored: no checks
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["code"], "kb_read_only")
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": False})
        self.upload(rid, attendance="listener")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["ask"]["speak"], False)
        self.upload(rid)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["code"], "not_attending")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "What did we decide last time?"})[0], 200)

    def test_slow_and_failed_answers(self):
        rid = str(uuid.uuid4())
        self.upload(rid, attendance="participant", language="zh-CN")
        self.call("POST", "/mock/meeting_attendance", {"answer": "slow", "answer_seconds": 0.3})
        aid = str(uuid.uuid4())
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "上次怎么定的？"})  # i18n-ignore
        self.assertEqual((status, out["ask"]["state"], out["ask"]["answer"], out["ask"]["lang"]), (202, "pending", "", "zh-CN"))
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "上次怎么定的？"})[0], 202)  # i18n-ignore
        time.sleep(0.4)
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "上次怎么定的？"})  # i18n-ignore
        self.assertEqual((status, out["ask"]["state"]), (200, "answered"))
        self.call("POST", "/mock/meeting_attendance", {"answer_seconds": 30})
        for _ in range(2):
            self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[0], 202)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["code"], "ask_busy")
        other = str(uuid.uuid4())
        self.upload(other, attendance="listener")
        self.call("POST", "/mock/meeting_attendance", {"answer": "fail"})
        failed = self.call("POST", f"/api/v1/recordings/{other}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["ask"]
        self.assertEqual((failed["state"], failed["error"], failed["answer"]), ("failed", "model_unavailable", ""))

    def test_asks_are_capped_at_one_hundred(self):
        rid = str(uuid.uuid4())
        self.upload(rid, attendance="listener")
        for _ in range(100):
            self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["code"], "asks_full")


class OlderHubMockTests(Hub, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({"MOCK_NO_MEETING_ATTENDANCE": "1"})
        cls.addClassCleanup(stop)

    def test_takes_the_fields_and_ignores_them(self):
        self.assertNotIn("meeting_attendance", self.call("GET", "/api/v1/health")[1]["features"])
        self.assertNotIn("meeting_attendance_default", self.call("GET", "/api/v1/assistant")[1])
        rid = str(uuid.uuid4())
        status, out = self.upload(rid, attendance="participant")
        self.assertEqual((status, "attendance" in out), (200, False))
        self.assertNotIn("attendance", self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"])
        for method, path in [("POST", "live"), ("GET", "live"), ("POST", "ask")]:
            status, out = self.call(method, f"/api/v1/recordings/{rid}/{path}", {"from": 0} if method == "POST" else None)
            self.assertEqual(status, 404)
            self.assertNotEqual(out.get("code"), "recording_not_found")


if __name__ == "__main__":
    unittest.main()
