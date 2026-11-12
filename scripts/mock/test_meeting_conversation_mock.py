"""The mock hub's meeting conversation (#3862, docs/assistant/meeting-minutes.md "Phase 1: the meeting conversation"), as
the hub branch has it: the feature, the upload's room and private, the room conversation and asks answered in it, the
private mark, Just me and the follow-up with their receipts, the fields of GET …/live (parts, revs) and detail.meta, and
the meeting_live event; and a manager without it (MOCK_NO_MEETING_CONVERSATION=1) behaves as before."""
import base64
import json
import os
import socket
import time
import unittest
import uuid

from test_meeting_attendance_mock import Hub, start

LINES = [{"start": 5, "end": 9, "text": "Ada: Today we look at the export flow."}, {"start": 1800, "end": 1806, "text": "Bo: Who owns the regression test?"}]


class Conversation(Hub):
    def wait(self, check, what, seconds=5.0):
        end = time.monotonic() + seconds
        while time.monotonic() < end:
            got = check()
            if got:
                return got
            time.sleep(0.05)
        self.fail(f"timed out waiting for {what}")

    def live(self, rid, query=""):
        return self.call("GET", f"/api/v1/recordings/{rid}/live{query}")[1]

    def meta(self, rid):
        return self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["meta"]

    def items(self, sid):
        return self.call("GET", f"/api/v1/sessions/server/{sid}/items")[1]["items"]

    def ready_room(self, rid, role="participant", **fields):
        status, out = self.upload(rid, room={"attendance": role}, **fields)
        self.assertEqual((status, out["attendance"], out["room"]["state"]), (200, role, "none"))
        return self.wait(lambda: (r := self.live(rid).get("room")) and r["state"] == "ready" and r, "the room")


class MeetingConversationMockTests(Conversation, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({})
        cls.addClassCleanup(stop)

    def setUp(self):
        self.call("POST", "/mock/meeting_attendance", {"feed": "ok", "answer": "ok", "answer_seconds": 3, "kb_read_only": False})
        self.call("POST", "/mock/meeting_conversation", {"room": "ok", "end_seconds": 180, "room_seconds": 2})

    def websocket(self):
        """An app's socket: a function giving its next frame of `type` (None when none comes within `wait` seconds)."""
        port = int(self.base.rsplit(":", 1)[1])
        sock = socket.create_connection(("127.0.0.1", port), timeout=5)
        self.addCleanup(sock.close)
        key = base64.b64encode(os.urandom(16)).decode()
        sock.sendall((f"GET /api/v1/ws HTTP/1.1\r\nHost: 127.0.0.1:{port}\r\nUpgrade: websocket\r\n"
                      f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n").encode())
        buf = bytearray()

        def fill(n, end):
            while len(buf) < n:
                left = end - time.monotonic()
                if left <= 0:
                    return False
                sock.settimeout(left)
                try:
                    chunk = sock.recv(65536)
                except (socket.timeout, TimeoutError):
                    return False
                if not chunk:
                    return False
                buf.extend(chunk)
            return True
        end = time.monotonic() + 5
        while b"\r\n\r\n" not in buf:
            self.assertTrue(fill(len(buf) + 1, end), "no upgrade")
        head, _, rest = bytes(buf).partition(b"\r\n\r\n")
        self.assertIn(b" 101 ", head)
        buf[:] = rest

        def frame(end):
            if not fill(2, end):
                return None
            n, off = buf[1] & 0x7F, 2
            if n == 126:
                if not fill(4, end):
                    return None
                n, off = int.from_bytes(buf[2:4], "big"), 4
            elif n == 127:
                if not fill(10, end):
                    return None
                n, off = int.from_bytes(buf[2:10], "big"), 10
            if not fill(off + n, end):
                return None
            payload = bytes(buf[off:off + n])
            del buf[:off + n]
            return json.loads(payload)

        def next_of(kind, wait=3.0, where=lambda f: True):
            end = time.monotonic() + wait
            while True:
                f = frame(end)
                if f is None:
                    return None
                if f.get("type") == kind and where(f):
                    return f
        return next_of

    def test_feature_and_the_uploads_room(self):
        self.assertIn("meeting_conversation", self.call("GET", "/api/v1/health")[1]["features"])
        rid = str(uuid.uuid4())
        # room replaces the top-level fields: they are ignored beside it.
        status, out = self.upload(rid, room={"attendance": "listener"}, attendance="participant",
                                  attending_session={"machine_id": "macbook", "session_id": "a1f3c2d4-0000-4000-8000-000000000006"}, private=False)
        self.assertEqual((status, out["attendance"], out["room"], "attendance_problem" in out), (200, "listener", {"state": "none"}, False))
        room = self.wait(lambda: (r := self.live(rid).get("room")) and r["state"] == "ready" and r, "the room")
        self.assertEqual(room["machine_id"], "server")
        meta = self.meta(rid)
        self.assertEqual((meta["attendance"], meta["room"], meta["private"]), ("listener", room, False))
        self.assertNotIn("attending_session", meta)
        # Without room: the role is off, and it stays a room meeting; private is taken only the first time.
        status, out = self.upload(rid, attendance="participant", private=True)
        self.assertEqual((out["attendance"], out["room"]["state"]), ("off", "ready"))
        self.assertNotIn("attendance", self.meta(rid))
        self.assertEqual(self.meta(rid)["private"], False)
        for room_field, problem in [({"attendance": "loud"}, "unknown_role"), ({"attendance": 3}, "unknown_role")]:
            out = self.upload(rid, room=room_field)[1]
            self.assertEqual((out["attendance"], out["attendance_problem"]), ("off", problem))
        note = str(uuid.uuid4())
        out = self.upload(note, kind="note", room={"attendance": "listener"}, private=True)[1]
        self.assertEqual((out["attendance"], out["attendance_problem"], out["room"]), ("off", "not_meeting", {"state": "none"}))
        self.assertNotIn("private", self.meta(note))  # a note takes no mark
        plain = str(uuid.uuid4())
        out = self.upload(plain, room="listener", attendance="listener")[1]  # a room that is not an object counts as absent
        self.assertEqual((out["attendance"], "room" in out), ("listener", False))
        # A stale upload still says the room.
        self.assertEqual(self.upload(rid, revision=1)[1]["room"]["state"], "ready")

    def test_live_parts_and_revisions(self):
        rid = str(uuid.uuid4())
        self.ready_room(rid, private=True)
        out = self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": LINES[:1]})[1]
        self.assertEqual((out["next"], out["fed"], out["private"], out["room"]["state"]), (1, 1, True, "ready"))
        self.assertNotIn("revs", out)
        self.assertNotIn("attending_session", out)
        live = self.live(rid)
        self.assertEqual(set(live["revs"]), {"asks", "room", "private"})
        self.assertEqual(live["revs"]["asks"], 0)
        only = self.live(rid, "?parts=room,asks,photos")
        self.assertEqual(set(only), {"room", "asks", "revs"})
        self.assertEqual(only["revs"], {"room": live["revs"]["room"], "asks": 0})
        self.assertEqual(self.live(rid, "?parts=followup"), {"revs": {}})
        # A batch went to the room: the header first, then the lines from the meeting.
        items = self.items(live["room"]["session_id"])
        self.assertEqual(items[0]["client_msg_id"], f"meeting-{rid}")
        self.assertIn('"kind": "meeting"', items[0]["text"])
        self.assertIn("[0:05] Ada: Today we look at the export flow.", items[-1]["text"])
        self.assertEqual(items[-1]["source"]["kind"], "meeting")
        row = next(s for s in self.call("GET", "/api/v1/sessions")[1]["sessions"] if s["session_id"] == live["room"]["session_id"])
        self.assertEqual((row["origin"], row["origin_ref"]), ("meeting", rid))
        self.assertRegex(row["title"], r"^\d{4} \| .+ \| Design review$")

    def test_asks_go_to_the_room(self):
        rid = str(uuid.uuid4())
        room = self.ready_room(rid)
        self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": LINES})
        typed = str(uuid.uuid4())
        ask = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": typed, "question": "What did we decide last time?"})[1]["ask"]
        self.assertEqual((ask["state"], ask["via"], ask["question_source"], ask["speak"]), ("answered", "room", "typed", True))
        spoken = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "Who owns it?", "spoken": True})[1]["ask"]
        self.assertEqual(spoken["question_source"], "spoken")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": typed, "question": "What did we decide last time?", "spoken": True})[1]["code"], "op_id_reused")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x", "spoken": "yes"})[0], 400)
        heard_id = str(uuid.uuid4())
        heard = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": heard_id, "at": 1810, "live_next": 2})[1]["ask"]
        self.assertEqual((heard["question"], heard["question_source"], heard["via"]), ("", "heard", "room"))
        self.assertEqual(heard["heard"], [LINES[1]])
        items = self.items(room["session_id"])
        sent = {x.get("client_msg_id"): x for x in items}
        self.assertEqual(sent[f"ask-{typed}-1"]["text"], "What did we decide last time?")
        self.assertIn("What was just said (30:10)", sent[f"ask-{heard_id}-1"]["text"])
        self.assertIn("[30:00] Bo: Who owns the regression test?", sent[f"ask-{heard_id}-1"]["text"])
        self.assertEqual(sum(1 for x in items if x["role"] == "assistant"), 3)
        self.assertEqual(self.live(rid)["revs"]["asks"], 3)

    def test_a_room_that_cannot_start_answers_from_the_material(self):
        self.call("POST", "/mock/meeting_conversation", {"room": "unavailable", "problem": "node_too_old"})
        rid = str(uuid.uuid4())
        self.upload(rid, room={"attendance": "listener"})
        room = self.wait(lambda: (r := self.live(rid).get("room")) and r["state"] == "unavailable" and r, "an unavailable room")
        self.assertEqual(room, {"state": "unavailable", "problem": "node_too_old"})
        ask = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})[1]["ask"]
        self.assertEqual((ask["state"], ask["via"]), ("answered", "material"))
        self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": LINES})
        heard = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "at": 1810})[1]["ask"]
        # meeting.answer says the question it understood, as for #2981 meetings; the room's would keep "".
        self.assertEqual((heard["via"], heard["question"], heard["heard"]), ("material", LINES[1]["text"], [LINES[1]]))

    def test_earlier_meetings_are_material_only_when_marked_not_private(self):
        self.call("POST", "/mock/recording_minutes", {"mode": "pending"})  # a fresh start: no earlier meeting of another test
        unmarked, private, shared, rid = (str(uuid.uuid4()) for _ in range(4))
        self.upload(unmarked, complete=True, segments=LINES[:1])
        self.upload(private, complete=True, segments=LINES[:1], private=True)
        self.ready_room(rid)

        def earlier():
            ask = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "What did we decide last time?"})[1]["ask"]
            return [x["id"] for x in ask["sources"] if x["kind"] == "recordings"]
        self.assertEqual(earlier(), [])  # no mark counts as private
        self.upload(shared, complete=True, segments=LINES[:1], private=False)
        self.assertEqual(earlier(), [shared])
        self.call("PUT", f"/api/v1/recordings/{shared}/private", {"private": True})
        self.assertEqual(earlier(), [])

    def test_a_room_is_told_what_the_material_answered(self):
        self.call("POST", "/mock/meeting_conversation", {"room": "unavailable", "problem": "machine_offline"})
        rid = str(uuid.uuid4())
        self.upload(rid, room={"attendance": "listener"}, segments=LINES[:1])
        self.wait(lambda: (self.live(rid).get("room") or {}).get("state") == "unavailable", "an unavailable room")
        self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 0, "segments": LINES[:1]})
        ask = self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "Who owns the test?"})[1]["ask"]
        self.assertEqual((ask["state"], ask["via"]), ("answered", "material"))
        # The computer is back: the room comes up and first gets the meeting so far, the material's answer with it.
        self.call("POST", "/mock/meeting_conversation", {"room": "ok"})
        self.upload(rid, room={"attendance": "listener"}, segments=LINES[:1])
        room = self.wait(lambda: (r := self.live(rid).get("room")) and r["state"] == "ready" and r, "the room")
        so_far = self.items(room["session_id"])[-1]["text"]
        self.assertIn("[0:05] Ada: Today we look at the export flow.", so_far)
        self.assertIn("Q: Who owns the test?", so_far)
        # Told once: the next batch has only its new lines.
        self.call("POST", f"/api/v1/recordings/{rid}/live", {"from": 1, "segments": LINES[1:]})
        latest = self.items(room["session_id"])[-1]["text"]
        self.assertIn("Bo: Who owns the regression test?", latest)
        self.assertNotIn("Q: Who owns the test?", latest)

    def test_the_meetings_end(self):
        rid = str(uuid.uuid4())
        room = self.ready_room(rid, segments=LINES, complete=False)
        aid = str(uuid.uuid4())
        self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "x"})
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/followup", {"id": str(uuid.uuid4()), "text": "Draft the email."})
        self.assertEqual((status, out["code"]), (409, "meeting_running"))
        out = self.upload(rid, segments=LINES, complete=True, ended_at="2026-11-10T15:00:00+08:00", room={"attendance": "participant"})[1]
        self.assertEqual(out["room"], {"state": "ended", "machine_id": "server", "session_id": room["session_id"]})
        self.assertIn("has ended", self.items(room["session_id"])[-1]["text"])
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "y"})[1]["code"], "meeting_ended")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": aid, "question": "x"})[0], 200)  # stored: answered as before
        status, out = self.call("POST", f"/api/v1/sessions/server/{room['session_id']}/messages", {"text": "One more thing"})
        self.assertEqual((status, out["code"]), (409, "meeting_ended"))

    def test_the_transcript_cut_deletes_the_room(self):
        self.call("POST", "/mock/recording_minutes", {"mode": "ready"})  # a fresh start, with minutes (only those are trimmed)
        self.addCleanup(self.call, "POST", "/mock/recording_minutes", {"mode": "pending"})
        rid = str(uuid.uuid4())
        room = self.ready_room(rid, segments=LINES)
        self.upload(rid, segments=LINES, complete=True, ended_at="2026-11-10T15:00:00+08:00", room={"attendance": "participant"})
        self.call("PUT", f"/api/v1/recordings/{rid}/private", {"private": False})
        made = self.call("POST", f"/api/v1/recordings/{rid}/followup", {"id": str(uuid.uuid4()), "text": "Draft the email."})[1]["followup"]
        self.assertEqual(self.call("POST", "/mock/recording_trim", {"id": rid})[0], 200)
        meta = self.meta(rid)
        self.assertEqual((meta["room"], meta["private"], meta["followup"]), ({"state": "deleted"}, False, made))
        self.assertNotIn("attendance", meta)
        listed = {s["session_id"] for s in self.call("GET", "/api/v1/sessions")[1]["sessions"]}
        self.assertNotIn(room["session_id"], listed)
        self.assertIn(made["session_id"], listed)
        # Deleting the recording takes meeting.json with it; the follow-up stays a conversation of its own.
        self.call("DELETE", f"/api/v1/recordings/{rid}")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/live")[1]["code"], "recording_deleted")
        self.assertIn(made["session_id"], {s["session_id"] for s in self.call("GET", "/api/v1/sessions")[1]["sessions"]})

    def test_private_mark_in_the_contracts_order(self):
        rid = str(uuid.uuid4())
        self.upload(rid)
        path = f"/api/v1/recordings/{rid}/private"
        self.assertEqual(self.call("PUT", path, {"private": True}, {"X-Mock-Role": "watch"})[1]["code"], "forbidden")
        self.assertEqual(self.call("PUT", path, b"{" + b" " * 1100 + b"}")[1]["code"], "too_large")
        self.assertEqual(self.call("PUT", f"/api/v1/recordings/{uuid.uuid4()}/private", {"private": True})[1]["code"], "recording_not_found")
        for bad in [{"private": None}, {"private": "yes"}, {}, [True]]:
            self.assertEqual(self.call("PUT", path, bad)[0], 400, bad)
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": True})
        self.assertEqual(self.call("PUT", path, {"private": True})[1]["code"], "kb_read_only")
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": False})
        self.assertEqual(self.call("PUT", path, {"private": True}), (200, {"private": True}))
        self.assertEqual((self.meta(rid)["private"], self.live(rid)["private"]), (True, True))
        note = str(uuid.uuid4())
        self.upload(note, kind="note")
        self.assertEqual(self.call("PUT", f"/api/v1/recordings/{note}/private", {"private": False})[1]["code"], "not_meeting")
        self.upload(note, kind="note", complete=True, segments=LINES)
        self.call("POST", f"/api/v1/recordings/{note}/minutes")  # minutes asked for: a meeting as #2981 counts them
        self.assertEqual(self.call("PUT", f"/api/v1/recordings/{note}/private", {"private": False}), (200, {"private": False}))
        self.call("DELETE", f"/api/v1/recordings/{rid}")
        self.assertEqual(self.call("PUT", path, {"private": True})[1]["code"], "recording_deleted")

    def test_followup_made_once_with_receipts(self):
        rid = str(uuid.uuid4())
        self.ready_room(rid, segments=LINES)
        self.upload(rid, segments=LINES, complete=True, ended_at="2026-11-10T15:00:00+08:00", room={"attendance": "participant"})
        path = f"/api/v1/recordings/{rid}/followup"
        for bad in [{"text": "x"}, {"id": "nope", "text": "x"}, {"id": str(uuid.uuid4())}, {"id": str(uuid.uuid4()), "text": "x" * 8001},
                    {"id": str(uuid.uuid4()), "images": [{"mime": "image/png", "data": "QQ=="}] * 5},
                    {"id": str(uuid.uuid4()), "images": [{"mime": "text/plain", "data": "QQ=="}]}]:
            self.assertEqual(self.call("POST", path, bad)[0], 400, bad)
        first = str(uuid.uuid4())
        status, made = self.call("POST", path, {"id": first, "text": "Draft the email to the vendor."})
        self.assertEqual((status, made["created"], made["client_msg_id"]), (200, True, f"followup-{first}"))
        sid = made["followup"]["session_id"]
        self.assertEqual(self.live(rid)["followup"], made["followup"])
        self.assertEqual(self.meta(rid)["followup"], made["followup"])
        items = self.items(sid)
        self.assertEqual(items[0]["client_msg_id"], f"followup-{first}")
        self.assertTrue(items[0]["text"].startswith('<persona-context kind="context" title="Meeting: Design review"'))
        self.assertIn("[30:00] Bo: Who owns the regression test?", items[0]["text"])
        self.assertTrue(items[0]["text"].endswith("\n\nDraft the email to the vendor."))
        self.assertEqual(items[-1]["role"], "assistant")
        self.assertLess(items[0]["seq"], items[-1]["seq"])  # apps order by seq: the owner's words come before the reply
        # The same call again: its answer, nothing sent; another body under its id: op_id_reused.
        self.assertEqual(self.call("POST", path, {"id": first, "text": "Draft the email to the vendor."}), (200, made))
        self.assertEqual(len(self.items(sid)), len(items))
        self.assertEqual(self.call("POST", path, {"id": first, "text": "Something else"})[1]["code"], "op_id_reused")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{uuid.uuid4()}/followup", {"id": first, "text": "Draft the email to the vendor."})[1]["code"], "op_id_reused")
        later = str(uuid.uuid4())
        status, again = self.call("POST", path, {"id": later, "text": "Shorter, please."})
        self.assertEqual((status, again["created"], again["followup"]), (200, False, made["followup"]))
        self.assertEqual(self.items(sid)[-2]["client_msg_id"], f"followup-{later}")
        row = next(s for s in self.call("GET", "/api/v1/sessions")[1]["sessions"] if s["session_id"] == sid)
        self.assertEqual((row["origin"], row["origin_ref"]), ("meeting_followup", rid))
        # A definite failure is kept too: the phone asks again with a new id.
        empty = str(uuid.uuid4())
        self.upload(empty, complete=True, ended_at="2026-11-10T15:00:00+08:00")
        bad_id = str(uuid.uuid4())
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{empty}/followup", {"id": bad_id, "text": "x"})[1]["code"], "no_material")
        self.upload(empty, complete=True, ended_at="2026-11-10T15:00:00+08:00", segments=LINES, revision=int(time.time() * 1000) + 5)
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{empty}/followup", {"id": bad_id, "text": "x"})[1]["code"], "no_material")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{empty}/followup", {"id": str(uuid.uuid4()), "text": "x"})[1]["created"], True)

    def test_just_me_goes_to_the_main_chat(self):
        rid = str(uuid.uuid4())
        self.upload(rid, room={"attendance": "off"})
        path = f"/api/v1/recordings/{rid}/just-me"
        self.assertEqual(self.call("POST", path, {"id": str(uuid.uuid4()), "text": "x"}, {"X-Mock-Role": "answer"})[1]["code"], "forbidden")
        cid = str(uuid.uuid4())
        status, out = self.call("POST", path, {"id": cid, "text": "Should I push back on the vendor?"})
        self.assertEqual((status, out["client_msg_id"]), (200, f"just-me-{cid}"))
        main = self.call("GET", "/api/v1/assistant")[1]["main"]
        self.assertEqual(out["conversation"], {"machine_id": "server", "session_id": main})
        sent = next(x for x in self.items(main) if x.get("client_msg_id") == f"just-me-{cid}")
        self.assertTrue(sent["text"].startswith('<persona-context kind="context" title="Meeting: Design review"'))
        self.assertTrue(sent["text"].endswith("\n\nShould I push back on the vendor?"))
        self.assertEqual(self.call("POST", path, {"id": cid, "text": "Should I push back on the vendor?"}), (200, out))
        self.assertEqual([x["id"] for x in self.items(main) if x.get("client_msg_id") == f"just-me-{cid}"], [sent["id"]])
        self.assertEqual(self.call("POST", path, {"id": cid, "text": "other"})[1]["code"], "op_id_reused")
        self.assertEqual(self.live(rid)["asks"], [])  # nothing of it is kept with the recording
        for _ in range(40):  # the main chat answers a moment later, after the owner's message
            after = [x for x in self.items(main) if x["seq"] > sent["seq"] and x["role"] == "assistant"]
            if after: break
            time.sleep(0.1)
        self.assertTrue(after)
        note = str(uuid.uuid4())
        self.upload(note, kind="note")
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{note}/just-me", {"id": str(uuid.uuid4()), "text": "x"})[1]["code"], "not_meeting")
        self.upload(rid, ended_at="2026-11-10T15:00:00+08:00", room={"attendance": "off"})
        self.assertEqual(self.call("POST", path, {"id": str(uuid.uuid4()), "text": "x"})[1]["code"], "meeting_ended")
        self.assertEqual(self.call("POST", path, {"id": cid, "text": "Should I push back on the vendor?"}), (200, out))  # the receipt first

    def test_a_fresh_start_takes_just_me_out_of_the_main_chat(self):
        # A UI test's Just me question and the main chat's reply must not show in a later test's main chat.
        rid = str(uuid.uuid4())
        self.upload(rid, room={"attendance": "listener"})
        main = self.call("GET", "/api/v1/assistant")[1]["main"]
        kept = [x["id"] for x in self.items(main)] if main else []
        cid = str(uuid.uuid4())
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/just-me", {"id": cid, "text": "Who writes the notes?"})[0], 200)
        main = self.call("GET", "/api/v1/assistant")[1]["main"]
        self.wait(lambda: any(x["role"] == "assistant" and x["id"] not in kept for x in self.items(main)), "the reply")
        self.call("POST", "/mock/recording_minutes", {"mode": "pending"})
        after = self.call("GET", "/api/v1/assistant")[1]["main"]
        left = [x["id"] for x in self.items(after)] if after else []
        self.assertEqual(left, kept)  # the question and its reply are gone, everything before stays
        # A reply still on its timer when the fresh start comes is never written.
        self.upload(rid, room={"attendance": "listener"})
        self.call("POST", f"/api/v1/recordings/{rid}/just-me", {"id": str(uuid.uuid4()), "text": "And the slides?"})
        self.call("POST", "/mock/recording_minutes", {"mode": "pending"})
        time.sleep(1.5)
        after = self.call("GET", "/api/v1/assistant")[1]["main"]
        self.assertEqual([x["id"] for x in self.items(after)] if after else [], kept)

    def test_meeting_live_events_are_content_free(self):
        next_of = self.websocket()
        rid = str(uuid.uuid4())
        mine = lambda f: f.get("recording_id") == rid
        self.upload(rid, room={"attendance": "listener"}, private=True)
        first = next_of("meeting_live", where=mine)
        self.assertEqual(set(first), {"type", "recording_id", "changed", "revs"})
        self.assertTrue(set(first["changed"]) <= {"room", "private"})
        self.assertTrue(all(isinstance(v, int) for v in first["revs"].values()))
        self.wait(lambda: self.live(rid).get("room", {}).get("state") == "ready", "the room")
        self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": str(uuid.uuid4()), "question": "x"})
        asks = next_of("meeting_live", where=lambda f: mine(f) and "asks" in f["changed"])
        self.assertEqual(asks["revs"]["asks"], self.live(rid)["revs"]["asks"])
        # At most one a second per recording: changes in a burst come in fewer events, the last with the last revision.
        for value in (False, True, False):
            self.call("PUT", f"/api/v1/recordings/{rid}/private", {"private": value})
        final = self.live(rid)["revs"]["private"]
        seen = []
        while not seen or seen[-1]["revs"]["private"] != final:
            got = next_of("meeting_live", where=lambda f: mine(f) and "private" in f["changed"])
            self.assertIsNotNone(got, "the last revision arrives")
            seen.append(got)
        self.assertLess(len(seen), 3)


class OlderManagerMockTests(Conversation, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({"MOCK_NO_MEETING_CONVERSATION": "1"})
        cls.addClassCleanup(stop)

    def test_takes_the_fields_and_ignores_them(self):
        features = self.call("GET", "/api/v1/health")[1]["features"]
        self.assertIn("meeting_attendance", features)
        self.assertNotIn("meeting_conversation", features)
        rid = str(uuid.uuid4())
        status, out = self.upload(rid, room={"attendance": "participant"}, private=True)
        self.assertEqual((status, out["attendance"], "room" in out), (200, "off", False))  # a 200 without room: an older manager
        meta = self.meta(rid)
        self.assertFalse({"room", "private", "followup"} & set(meta))
        live = self.live(rid)
        self.assertFalse({"room", "private", "followup", "revs"} & set(live))
        for method, path in [("PUT", "private"), ("POST", "just-me"), ("POST", "followup")]:
            status, out = self.call(method, f"/api/v1/recordings/{rid}/{path}", {"id": str(uuid.uuid4()), "text": "x", "private": True})
            self.assertEqual(status, 404)  # without recording_not_found: the route is missing
            self.assertNotEqual(out.get("code"), "recording_not_found")


if __name__ == "__main__":
    unittest.main()
