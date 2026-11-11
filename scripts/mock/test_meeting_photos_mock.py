"""The mock hub's meeting photos (#3862 phase 3, docs/assistant/meeting-minutes.md "Phase 3: photos"): the feature, POST,
GET and DELETE of …/photos with the contract's errors in its order, the same id and op_id_reused, a new photo into the
room and the room's note, the withdrawn line, photos in meeting_live and GET …/live's revs, minutes citing photos and
made again, the trim keeping only the cited ones, the follow-up's seed; and managers without it."""
import base64
import json
import struct
import time
import unittest
import uuid
import zlib
from urllib.request import Request, urlopen

import test_meeting_conversation_mock as conversation
from test_meeting_attendance_mock import Hub, start

STARTED = "2026-11-10T14:00:00+08:00"


def png(width=4, height=3, depth=8, interlace=0):
    """A real PNG of that size (white, 8-bit grey); another depth or interlace method makes a header no decoder takes."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    rows = b"".join(b"\x00" + b"\xff" * width for _ in range(height))
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, depth, 0, 0, 0, interlace))
            + chunk(b"IDAT", zlib.compress(rows)) + chunk(b"IEND", b""))


def jpeg(width=1600, height=1200, pad=0):
    """A JPEG's header as a decoder reads its size: SOI, a comment of `pad` bytes, a baseline start of frame, EOI."""
    out = b"\xff\xd8"
    while pad > 0:
        n = min(pad, 65533)
        out += b"\xff\xfe" + struct.pack(">H", n + 2) + b"\x00" * n
        pad -= n
    return out + b"\xff\xc0" + struct.pack(">HBHHB", 17, 8, height, width, 3) + b"\x01\x22\x00\x02\x11\x01\x03\x11\x01" + b"\xff\xd9"


def photo(at=1834.2, mime="image/png", data=None, **fields):
    raw = data if data is not None else (png() if mime == "image/png" else jpeg())
    return {"id": str(uuid.uuid4()), "at": at, "mime": mime, "data": base64.b64encode(raw).decode(), **fields}


class Photos(conversation.Conversation):
    websocket = conversation.MeetingConversationMockTests.websocket

    def post(self, rid, body, headers=None):
        return self.call("POST", f"/api/v1/recordings/{rid}/photos", body, headers)

    def photos(self, rid):
        status, out = self.call("GET", f"/api/v1/recordings/{rid}/photos")
        self.assertEqual(status, 200, out)
        return out["photos"]

    def image(self, rid, n):
        with urlopen(Request(f"{self.base}/api/v1/recordings/{rid}/photos/{n}"), timeout=10) as r:
            return r.status, r.headers["Content-Type"], r.read()


class MeetingPhotosMockTests(Photos, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({})
        cls.addClassCleanup(stop)

    def setUp(self):
        self.call("POST", "/mock/recording_minutes", {"mode": "ready"})
        self.call("POST", "/mock/meeting_attendance", {"feed": "ok", "answer": "ok", "answer_seconds": 3, "kb_read_only": False})
        self.call("POST", "/mock/meeting_conversation", {"room": "ok", "end_seconds": 180, "room_seconds": 2})
        self.call("POST", "/mock/meeting_photos", {"note": "ok", "note_seconds": 0.2, "on": True})

    def test_feature_beside_the_meeting_conversation(self):
        self.assertIn("meeting_photos", self.call("GET", "/api/v1/health")[1]["features"])
        self.call("POST", "/mock/meeting_photos", {"on": False})
        self.assertNotIn("meeting_photos", self.call("GET", "/api/v1/health")[1]["features"])
        rid = str(uuid.uuid4())
        self.upload(rid)
        status, out = self.post(rid, photo())
        self.assertEqual((status, out.get("code")), (404, "not_found"))  # a 404 without a code of the routes
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos")[0], 404)

    def test_a_photo_goes_into_the_room_and_gets_its_note(self):
        rid = str(uuid.uuid4())
        room = self.ready_room(rid)
        ws = self.websocket()
        body = photo(text="Q3 budget: 120k\nHiring: 2", caption="Whiteboard")
        status, out = self.post(rid, body)
        self.assertEqual(status, 200, out)
        p = out["photo"]
        self.assertEqual({k: p[k] for k in ("n", "id", "at", "mime", "width", "height", "text", "caption", "cited", "forwarded")},
                         {"n": 1, "id": body["id"], "at": 1834.2, "mime": "image/png", "width": 4, "height": 3,
                          "text": "Q3 budget: 120k\nHiring: 2", "caption": "Whiteboard", "cited": False, "forwarded": True})
        self.assertNotIn("note", p)
        self.assertTrue(p["added_at"].endswith("Z"))
        ev = ws("meeting_live", where=lambda f: f["recording_id"] == rid and "photos" in f["changed"])
        self.assertIsNotNone(ev)
        self.assertEqual(set(ev), {"type", "recording_id", "changed", "revs"})  # content-free
        # The room: the image, then the line asking for the note; both carry the photo's id.
        items = self.items(room["session_id"])
        sent = [x for x in items if x.get("client_msg_id") == "photo-" + body["id"]]
        self.assertEqual([x["kind"] for x in sent], ["image", "text"])
        self.assertEqual(sent[0]["image"], {"mime": "image/png", "data": body["data"]})
        self.assertEqual(sent[1]["source"]["kind"], "meeting")
        self.assertIn("meeting_photo_note (photo 1", sent[1]["text"])
        self.assertIn("Caption: Whiteboard", sent[1]["text"])
        noted = self.wait(lambda: (x := self.photos(rid)) and x[0].get("note") and x, "the note")
        self.assertIn("Q3 budget", noted[0]["note"])
        calls = [x for x in self.items(room["session_id"]) if x["kind"] == "tool_use"]
        self.assertEqual(calls[-1]["tool_name"], "mcp__persona__meeting_photo_note")
        self.assertEqual(calls[-1]["tool_input"], {"photo": 1, "text": noted[0]["note"]})
        # GET …/live: the photos' revision; ?parts=photos only that.
        live = self.live(rid)
        self.assertGreaterEqual(live["revs"]["photos"], 2)  # the photo, then its note
        self.assertNotIn("photos", live)
        self.assertEqual(self.live(rid, "?parts=photos"), {"revs": {"photos": live["revs"]["photos"]}})

    def test_the_same_id_and_another_body(self):
        rid = str(uuid.uuid4())
        self.upload(rid)
        body = photo(caption="Board")
        first = self.post(rid, body)
        self.assertEqual(first[0], 200)
        again = self.post(rid, body)
        self.assertEqual(again, first)  # the stored photo, nothing new
        self.assertEqual(len(self.photos(rid)), 1)
        status, out = self.post(rid, dict(body, caption="Another"))
        self.assertEqual((status, out["code"]), (409, "op_id_reused"))
        status, out = self.post(rid, dict(body, id=body["id"].upper()))  # ids are case-insensitive
        self.assertEqual((status, out["photo"]["n"]), (200, 1))
        # n counts from 1 and is never reused.
        second = self.post(rid, photo())[1]["photo"]
        self.assertEqual(second["n"], 2)
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/2"), (200, {"deleted": True}))
        self.assertEqual(self.post(rid, photo())[1]["photo"]["n"], 3)
        # A retry of a photo the owner deleted never brings it back.
        status, out = self.post(rid, {k: v for k, v in body.items()} | {"id": second["id"]})
        self.assertEqual((status, out["code"]), (410, "photo_deleted"))

    def test_bad_bodies_are_400_and_a_big_one_413(self):
        rid = str(uuid.uuid4())
        self.upload(rid)
        bad = [dict(photo(), id="not-a-uuid"), dict(photo(), at="12"), dict(photo(), at=True), {k: v for k, v in photo().items() if k != "at"},
               photo(mime="image/gif"), dict(photo(), data="%%%"), photo(mime="image/jpeg", data=png()), photo(data=jpeg()),
               photo(data=png(4097, 2)), photo(mime="image/jpeg", data=jpeg(2, 4097)), photo(mime="image/jpeg", data=jpeg(pad=(1 << 20) + 1)),
               photo(text="x" * 8001), photo(caption="x" * 201), photo(caption=7), "a photo", photo(data=b""),
               dict(photo(), mime=[]), dict(photo(), mime={}), dict(photo(), at=None), photo(data=png()[:24]), photo(data=png()[:32]),
               photo(data=png(depth=7)), photo(data=png(interlace=2))]
        for body in bad:
            status, out = self.post(rid, body)
            self.assertEqual(status, 400, (body if isinstance(body, str) else {k: str(v)[:40] for k, v in body.items()}, out))
        # Raw bodies JSON can carry but Python would choke on: a number past a float, a lone surrogate.
        good = photo()
        for raw in (json.dumps(good).replace('"at": 1834.2', '"at": 1' + "0" * 400), json.dumps(dict(good, caption="\ud800"))):
            status, out = self.post(rid, raw.encode())
            self.assertEqual(status, 400, (raw[:80], out))
        ok = self.post(rid, photo(mime="image/jpeg", data=jpeg(4096, 3072, pad=(1 << 20) - 200), text="x" * 8000, caption="y" * 200))
        self.assertEqual((ok[0], ok[1]["photo"]["width"], ok[1]["photo"]["height"]), (200, 4096, 3072))
        status, out = self.post(rid, photo(mime="image/jpeg", data=jpeg(pad=(2 << 20))))
        self.assertEqual((status, out["code"]), (413, "too_large"))

    def test_errors_in_the_contracts_order(self):
        rid = str(uuid.uuid4())
        self.assertEqual(self.post(rid, photo())[1]["code"], "recording_not_found")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos")[1]["code"], "recording_not_found")
        self.upload(rid)
        watch = {"X-Mock-Role": "watch"}
        self.assertEqual(self.post(rid, photo(), watch)[1]["code"], "forbidden")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos", headers=watch)[1]["code"], "forbidden")
        self.assertEqual(self.post(rid, "x")[0], 400)
        stored = photo()
        self.post(rid, stored)
        # The same id is answered before the knowledge base's own checks.
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": True})
        self.assertEqual(self.post(rid, stored)[0], 200)
        self.assertEqual(self.post(rid, dict(stored, caption="x"))[1]["code"], "op_id_reused")
        self.assertEqual(self.post(rid, photo())[1]["code"], "kb_read_only")
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/1")[1]["code"], "kb_read_only")
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/9")[1]["code"], "photo_not_found")  # 404 first
        self.assertEqual(self.photos(rid)[0]["n"], 1)  # reads work on a read-only knowledge base
        self.assertEqual(self.image(rid, 1)[0], 200)
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": False})
        note = str(uuid.uuid4())
        self.upload(note, kind="note")
        self.assertEqual(self.post(note, photo())[1]["code"], "not_meeting")
        for _ in range(49):
            self.assertEqual(self.post(rid, photo())[0], 200)
        status, out = self.post(rid, photo())
        self.assertEqual((status, out["code"]), (413, "photos_full"))
        self.call("DELETE", f"/api/v1/recordings/{rid}/photos/50")
        self.assertEqual(self.post(rid, photo())[1]["photo"]["n"], 51)  # a deleted one makes room
        self.call("DELETE", f"/api/v1/recordings/{rid}")
        self.assertEqual(self.post(rid, photo())[1]["code"], "recording_deleted")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos")[1]["code"], "recording_deleted")
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos/1")[1]["code"], "recording_deleted")

    def test_at_is_clamped_into_the_recording(self):
        rid = str(uuid.uuid4())
        self.upload(rid, ended_at="2026-11-10T15:00:00+08:00")
        self.assertEqual(self.post(rid, photo(at=-5))[1]["photo"]["at"], 0)
        self.assertEqual(self.post(rid, photo(at=99999))[1]["photo"]["at"], 3600)
        naive = str(uuid.uuid4())  # a time without a zone, or none the mock can read: not clamped above, never a crash
        self.upload(naive, started_at="2026-11-10T06:00:00", ended_at=7)
        self.assertEqual(self.post(naive, photo(at=99999))[1]["photo"]["at"], 99999)

    def test_a_note_due_before_a_reset_never_lands_on_a_new_photo(self):
        rid = str(uuid.uuid4())
        self.ready_room(rid)
        self.call("POST", "/mock/meeting_photos", {"note_seconds": 1})
        body = photo(text="board")
        self.assertEqual(self.post(rid, body)[0], 200)
        self.call("POST", "/mock/recording_minutes", {"mode": "ready"})  # a fresh start before the note is due
        self.call("POST", "/mock/meeting_photos", {"note": "none"})
        self.ready_room(rid)
        self.assertEqual(self.post(rid, body)[1]["photo"]["n"], 1)
        time.sleep(1.5)
        self.assertNotIn("note", self.photos(rid)[0])

    def test_reading_and_deleting(self):
        rid = str(uuid.uuid4())
        room = self.ready_room(rid)
        body = photo(mime="image/jpeg", caption="Roadmap")
        self.post(rid, body)
        self.wait(lambda: self.photos(rid)[0].get("note"), "the note")
        status, kind, raw = self.image(rid, 1)
        self.assertEqual((status, kind, raw), (200, "image/jpeg", base64.b64decode(body["data"])))
        for n in ("2", "0", "x"):
            self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos/{n}")[1]["code"], "photo_not_found")
        # A path below the recording that is no photo never deletes the recording.
        for bad in ("photos/1/x", "photos/x/", "nothing"):
            self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/{bad}")[0], 404, bad)
        self.assertEqual(self.image(rid, 1)[0], 200)
        ws = self.websocket()
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/1/"), (200, {"deleted": True}))  # the trailing slash names it too
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/1"), (200, {"deleted": False}))  # a repeat
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos/1")[1]["code"], "photo_not_found")
        self.assertEqual(self.photos(rid), [])
        self.assertIsNotNone(ws("meeting_live", where=lambda f: f["recording_id"] == rid and "photos" in f["changed"]))
        last = self.items(room["session_id"])[-1]
        self.assertEqual(last["source"]["kind"], "meeting")
        self.assertIn("Photo 1 was withdrawn", last["text"])
        self.assertEqual(last["client_msg_id"], "photo-withdrawn-" + body["id"])

    def test_a_photo_taken_while_the_room_comes_up_goes_once_it_is_ready(self):
        self.call("POST", "/mock/meeting_conversation", {"room": "slow", "room_seconds": 1})
        rid = str(uuid.uuid4())
        status, out = self.upload(rid, room={"attendance": "listener"})
        self.assertEqual((status, out["room"]["state"]), (200, "none"))
        ask = str(uuid.uuid4())
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/ask", {"id": ask, "question": "What did we decide?"})[0], 202)
        p = self.post(rid, photo(text="board"))[1]["photo"]
        self.assertFalse(p["forwarded"])
        rev = self.live(rid)["revs"]["photos"]
        room = self.wait(lambda: (r := self.live(rid).get("room")) and r["state"] == "ready" and r, "the room")
        self.wait(lambda: self.live(rid)["revs"]["photos"] > rev, "the photos' revision for forwarded")
        ids = [x.get("client_msg_id") for x in self.items(room["session_id"])]
        self.assertLess(ids.index(f"ask-{ask}-1"), ids.index("photo-" + p["id"]))  # the ask that waited goes first
        self.wait(lambda: self.photos(rid)[0]["forwarded"] and self.photos(rid)[0].get("note"), "the photo in the room and its note")
        kinds = [x["kind"] for x in self.items(room["session_id"]) if x.get("client_msg_id") == "photo-" + p["id"]]
        self.assertEqual(kinds, ["image", "text"])

    def test_not_forwarded_without_a_ready_room(self):
        off = str(uuid.uuid4())
        self.upload(off, room={"attendance": "off"})
        p = self.post(off, photo(text="board"))[1]["photo"]
        self.assertFalse(p["forwarded"])
        ended = str(uuid.uuid4())
        room = self.ready_room(ended)
        self.upload(ended, room={"attendance": "participant"}, ended_at="2026-11-10T15:00:00+08:00", complete=True)
        self.wait(lambda: self.live(ended)["room"]["state"] == "ended", "the end")
        before = len(self.items(room["session_id"]))
        p = self.post(ended, photo(text="board"))[1]["photo"]
        self.assertFalse(p["forwarded"])
        time.sleep(0.5)
        self.assertEqual(len(self.items(room["session_id"])), before)
        self.assertNotIn("note", self.photos(ended)[0])

    def test_minutes_cite_photos_and_the_trim_keeps_those(self):
        rid = str(uuid.uuid4())
        lines = [{"start": 5, "end": 9, "text": "Ada: Today we look at the export flow."}]
        self.upload(rid, segments=lines)
        self.post(rid, photo(at=120, text="Export: 4 MB cap\nOwner: Bo"))
        self.post(rid, photo(at=240))  # no words: nothing to cite
        self.upload(rid, segments=lines, ended_at="2026-11-10T15:00:00+08:00", complete=True)
        self.assertGreaterEqual(self.live(rid)["revs"]["photos"], 3)  # two photos, then the minutes citing one
        minutes = self.meta_minutes(rid)
        cites = [x for x in minutes["points"] if "photo" in x]
        self.assertEqual(cites, [{"text": "Export: 4 MB cap", "at": 120, "photo": 1}])
        self.assertEqual([x["cited"] for x in self.photos(rid)], [True, False])
        # A photo added after the minutes makes them again.
        self.post(rid, photo(at=300, caption="Timeline"))
        self.assertEqual([x.get("photo") for x in self.meta_minutes(rid)["points"] if "photo" in x], [1, 3])
        self.call("DELETE", f"/api/v1/recordings/{rid}/photos/3")
        self.assertEqual([x.get("photo") for x in self.meta_minutes(rid)["points"] if "photo" in x], [1])
        # The transcript cut keeps the cited photo only; the others are gone (404, not a repeat's false).
        self.assertEqual(self.call("POST", "/mock/recording_trim", {"id": rid})[0], 200)
        self.assertEqual([x["n"] for x in self.photos(rid)], [1])
        self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos/2")[1]["code"], "photo_not_found")
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/2")[1]["code"], "photo_not_found")
        self.assertEqual(self.post(rid, photo())[1]["code"], "transcript_expired")
        # A cited photo can still be deleted; the minutes are not made again (their item shows "Photo removed").
        self.assertEqual(self.call("DELETE", f"/api/v1/recordings/{rid}/photos/1"), (200, {"deleted": True}))
        self.assertEqual([x.get("photo") for x in self.meta_minutes(rid)["points"] if "photo" in x], [1])
        self.assertEqual(self.photos(rid), [])

    def meta_minutes(self, rid):
        return self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]["minutes"]

    def test_a_trimmed_recordings_seed_has_its_minutes_only(self):
        rid = str(uuid.uuid4())
        lines = [{"start": 5, "end": 9, "text": "Ada: Today we look at the export flow."}]
        self.upload(rid, segments=lines)
        self.post(rid, photo(at=120, text="Export: 4 MB cap"))
        self.upload(rid, segments=lines, ended_at="2026-11-10T15:00:00+08:00", complete=True)
        self.call("POST", "/mock/recording_trim", {"id": rid})
        self.assertEqual(len(self.photos(rid)), 1)  # cited: kept
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/followup", {"id": str(uuid.uuid4()), "text": "Draft it."})
        self.assertEqual(status, 200, out)
        seed = next(x["text"] for x in self.items(out["followup"]["session_id"]) if x["kind"] == "text" and x["role"] == "user")
        self.assertNotIn("Photos:", seed)

    def test_search_finds_a_recording_by_its_photos_words(self):
        rid = str(uuid.uuid4())
        self.upload(rid)
        self.post(rid, photo(caption="Roadmap", text="Quokka migration plan"))
        hits = self.call("GET", "/api/v1/kb-search?q=quokka")[1]["hits"]
        self.assertIn(f"recordings/{rid}/photos.md", [x["path"] for x in hits])
        matches = self.call("GET", f"/api/v1/kb/recordings/{rid}?q=quokka")[1]["matches"]
        self.assertEqual([x["line"] for x in matches], ["[P1] 30:34 photo: Roadmap · Quokka migration plan"])
        self.call("DELETE", f"/api/v1/recordings/{rid}/photos/1")
        self.assertNotIn(f"recordings/{rid}/photos.md", [x["path"] for x in self.call("GET", "/api/v1/kb-search?q=quokka")[1]["hits"]])

    def test_the_followups_seed_lists_the_photos(self):
        rid = str(uuid.uuid4())
        self.ready_room(rid, segments=[{"start": 5, "end": 9, "text": "Ada: Today we look at the export flow."}])
        self.post(rid, photo(at=75, caption="Whiteboard", text="Q3 budget"))
        self.wait(lambda: self.photos(rid)[0].get("note"), "the note")
        self.post(rid, photo(at=80, caption="Withdrawn one"))
        self.call("DELETE", f"/api/v1/recordings/{rid}/photos/2")
        self.upload(rid, room={"attendance": "participant"}, segments=[{"start": 5, "end": 9, "text": "Ada: Today we look at the export flow."}],
                    ended_at="2026-11-10T15:00:00+08:00", complete=True)
        self.wait(lambda: self.live(rid)["room"]["state"] == "ended", "the end")
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/followup", {"id": str(uuid.uuid4()), "text": "Draft the email."})
        self.assertEqual(status, 200, out)
        seed = next(x["text"] for x in self.items(out["followup"]["session_id"]) if x["kind"] == "text" and x["role"] == "user")
        self.assertIn("Photos:\n[P1] 1:15 photo: Whiteboard · Q3 budget · Whiteboard photo: Q3 budget.", seed)
        self.assertNotIn("Withdrawn one", seed)


class WithoutPhotosMockTests(Hub, unittest.TestCase):
    def test_a_manager_without_meeting_photos(self):
        for env in ({"MOCK_NO_MEETING_PHOTOS": "1"}, {"MOCK_NO_MEETING_CONVERSATION": "1"}):
            self.base, stop = start(env)
            try:
                features = self.call("GET", "/api/v1/health")[1]["features"]
                self.assertNotIn("meeting_photos", features)
                rid = str(uuid.uuid4())
                self.upload(rid)
                self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/photos", photo())[0], 404)
                self.assertEqual(self.call("GET", f"/api/v1/recordings/{rid}/photos")[0], 404)
                live = self.call("GET", f"/api/v1/recordings/{rid}/live")[1]
                self.assertNotIn("photos", json.dumps(live))
            finally:
                stop()


if __name__ == "__main__":
    unittest.main()
