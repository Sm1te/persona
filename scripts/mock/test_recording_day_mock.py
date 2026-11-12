"""All-day recording fixtures follow the #4741 wire contract, including retries, repairs and recap retention."""
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
import json
import unittest
from urllib.parse import urlencode
import uuid

from test_meeting_attendance_mock import Hub, start


def segment(at=0, text="A sentence", end=None):
    return {"start": at, "end": at + 1 if end is None else end, "text": text}


class RecordingDayMockTests(Hub, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({"MOCK_RECORDINGS_DOWN": "0", "MOCK_NO_RECORDING_DAY": "0", "MOCK_DAY_RECAP": "ready"})
        cls.addClassCleanup(stop)

    def setUp(self):
        self.call("POST", "/mock/recording_minutes", {"mode": "ready"})
        self.call("POST", "/mock/recording_day", {"on": True, "recap": "ready"})
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": False})
        self.call("PUT", "/api/v1/assistant", {"recording_days": 14})
        self.now = datetime.now(timezone.utc).replace(hour=8, minute=0, second=0, microsecond=0)
        self.rid = str(uuid.uuid4())

    def put(self, rid=None, **fields):
        body = {"revision": 10, "kind": "day", "day": self.now.date().isoformat(), "tz": "UTC", "part": 1,
                "started_at": self.now.isoformat(), "device": "watch", "language": "en", "duration": 0,
                "segments": [], "coverage": {"to": 10000, "gaps": []}, "complete": False, **fields}
        return self.call("PUT", "/api/v1/recordings/" + (rid or self.rid), body)

    def append(self, rid=None, **fields):
        return self.call("POST", "/api/v1/recordings/%s/segments" % (rid or self.rid),
                         {"base": 10, "revision": 20, "from": 0, "segments": [], **fields})

    def detail(self, rid=None):
        return self.call("GET", "/api/v1/kb/recordings/" + (rid or self.rid))[1]["detail"]

    def state(self, rid=None):
        return next(r for r in self.call("GET", "/api/v1/recordings")[1]["recordings"] if r["id"] == (rid or self.rid))

    def error(self, reply, status, code=None):
        self.assertEqual(reply[0], status, reply)
        if code is not None: self.assertEqual(reply[1].get("code"), code, reply)
        return reply[1]

    def test_feature_and_switches(self):
        self.assertEqual(self.call("GET", "/mock/recording_day"), (200, {"on": True, "recap": "ready"}))
        self.assertIn("recording_day", self.call("GET", "/api/v1/health")[1]["features"])
        self.put()
        self.call("POST", "/mock/recording_day", {"on": False, "recap": "none"})
        self.assertNotIn("recording_day", self.call("GET", "/api/v1/health")[1]["features"])
        self.error(self.put(), 400)
        self.assertNotIn("code", self.error(self.append(), 404))
        self.call("POST", "/mock/recording_day", {"on": True})
        self.assertEqual(self.append()[0], 200)
        for body in ({"on": "false"}, {"recap": "bad"}, [], b"not json"):
            self.error(self.call("POST", "/mock/recording_day", body), 400)

    def test_put_defaults_and_never_a_meeting(self):
        fields = {"revision": 10, "kind": "day", "day": self.now.date().isoformat(), "started_at": self.now.isoformat(),
                  "device": "watch", "segments": [segment(2, "  text  "), segment(0, " ")],
                  "calendar": {"title": "A meeting"}, "attendance": "participant", "attending_session": {"bad": True},
                  "room": {"attendance": "participant"}, "private": False, "transcription": "private"}
        status, out = self.call("PUT", "/api/v1/recordings/" + self.rid.upper(), fields)
        self.assertEqual(status, 200)
        self.assertEqual((out["base"], out["next"], out["revision"], out["attendance"], out["attendance_problem"]),
                         (10, 2, 10, "off", "not_meeting"))
        detail = self.detail()
        self.assertEqual((detail["meta"]["tz"], detail["meta"]["part"], detail["meta"]["coverage"]),
                         ("Asia/Shanghai", 1, {"to": 0, "gaps": []}))
        self.assertEqual(detail["meta"]["name"], "Day · " + fields["day"])
        self.assertEqual(detail["segments"], [{**segment(2, "text"), "n": 1}])
        for key in ("calendar", "minutes_state", "meeting", "attendance", "attending_session", "room", "private", "transcription"):
            self.assertNotIn(key, detail["meta"])
        self.assertNotIn("minutes", detail)
        self.assertEqual((detail["meta"]["matched_rev"], detail["meta"]["summarized_rev"]), (10, 10))

    def test_put_validation(self):
        for fields in ({"day": ""}, {"day": "2026-02-30"}, {"day": "2026-1-02"}, {"day": None}, {"tz": "No/Such_Zone"},
                       {"tz": 1}, {"tz": ""}, {"part": 0}, {"part": 100}, {"part": True}, {"part": 1.5},
                       {"started_at": "2026-11-11"}, {"started_at": "bad"}, {"revision": True},
                       {"duration": "2"}, {"complete": "false"}, {"segments": [segment(text=4)]},
                       {"coverage": None}, {"coverage": {"to": "4"}}, {"coverage": {"gaps": {}}},
                       {"coverage": {"gaps": [{"from": 1, "to": 2, "reason": 1}]}},
                       {"coverage": {"to": float("inf")}}, {"segments": [segment(float("nan"))]},
                       {"bookmarks": [{"at": False}]}, {"ended_at": "yesterday"}, {"title": 5}):
            with self.subTest(fields=fields): self.error(self.put(**fields), 400)
        self.assertEqual(self.call("GET", "/api/v1/recordings")[1]["recordings"], [])

    def test_coverage_is_mended_not_refused(self):
        gaps = [{"from": 20, "to": 30, "reason": " pending "}, {"from": -5, "to": 2, "reason": " future_reason "},
                {"from": 25, "to": 40, "reason": "erased"}, {"from": 8, "to": 5, "reason": "x" * 40}]
        self.assertEqual(self.put(coverage={"to": -10, "gaps": gaps})[0], 200)
        got = self.detail()["meta"]["coverage"]
        self.assertEqual(got, {"to": 0, "gaps": [{"from": 0, "to": 2, "reason": "future_reason"},
                                                {"from": 5, "to": 8, "reason": "x" * 32},
                                                {"from": 20, "to": 30, "reason": "pending"},
                                                {"from": 30, "to": 40, "reason": "erased"}]})
        gaps = [{"from": i * 10, "to": i * 10 + 1, "reason": " paused "} for i in range(601)]
        self.assertEqual(self.append(coverage={"to": 7000, "gaps": list(reversed(gaps))})[0], 200)
        got = self.detail()["meta"]["coverage"]["gaps"]
        self.assertEqual(len(got), 500)
        self.assertEqual((got[0]["from"], got[-1]["to"]), (0, 6001))
        self.assertTrue(all(a["to"] < b["from"] for a, b in zip(got, got[1:])))
        self.append(revision=21, coverage={"to": 1, "gaps": [{"from": 0, "to": 1, "reason": "界" * 20}]})
        self.assertLessEqual(len(self.detail()["meta"]["coverage"]["gaps"][0]["reason"].encode()), 32)

    def test_coverage_preserves_different_reason_remainders(self):
        gaps = [{"from": 10, "to": 30, "reason": "lost"}, {"from": 0, "to": 20, "reason": "pending"},
                {"from": 2, "to": 5, "reason": "paused"}, {"from": 30, "to": 40, "reason": "interrupted"},
                {"from": 35, "to": 45, "reason": "interrupted"}, {"from": 50, "to": 50, "reason": "empty"}]
        self.assertEqual(self.put(coverage={"to": 60, "gaps": gaps})[0], 200)
        self.assertEqual(self.detail()["meta"]["coverage"]["gaps"], [
            {"from": 0, "to": 20, "reason": "pending"}, {"from": 20, "to": 30, "reason": "lost"},
            {"from": 30, "to": 45, "reason": "interrupted"}])

    def test_coverage_compacts_closest_same_reason_first(self):
        gaps = [{"from": 0, "to": 1, "reason": "pending"}, {"from": 2, "to": 3, "reason": "lost"},
                {"from": 6, "to": 7, "reason": "lost"}]
        gaps += [{"from": 100 + i * 10, "to": 101 + i * 10, "reason": "paused"} for i in range(498)]
        self.assertEqual(self.put(coverage={"to": 6000, "gaps": gaps})[0], 200)
        self.assertEqual(self.detail()["meta"]["coverage"]["gaps"], [gaps[0], {"from": 2, "to": 7, "reason": "lost"}] + gaps[3:])

    def test_coverage_keeps_separated_erased_gaps_and_recap_between_them(self):
        text = "I will keep this un-erased sentence"
        self.put(segments=[segment(1.25, text, end=1.75)])
        prior = self.detail()["recap"]
        self.call("POST", "/mock/recording_day", {"recap": "pending"})
        gaps = [{"from": i * 2, "to": i * 2 + 1, "reason": "erased"} for i in range(501)]
        self.assertEqual(self.put(revision=30, segments=[segment(1.25, text, end=1.75)],
                                  coverage={"to": 1100, "gaps": gaps})[0], 200)
        detail = self.detail()
        self.assertEqual(detail["meta"]["coverage"]["gaps"], gaps)
        self.assertEqual(detail["meta"]["recap_state"], "pending")
        self.assertEqual(detail["recap"]["timeline"], prior["timeline"])
        self.assertEqual(detail["recap"]["actions"], prior["actions"])

    def test_positions_retries_and_stored_revision(self):
        self.put(segments=[segment(50, "first")])
        body = {"segments": [segment(999, "ignored retry"), segment(0, "  late  "), segment(1, " "), segment(70, "last")]}
        reply = self.append(**body)
        self.assertEqual(reply, (200, {"id": self.rid, "revision": 20, "next": 4, "status": "stored"}))
        before = self.detail()
        self.assertEqual(self.append(**body), reply)
        self.assertEqual(self.detail(), before)
        self.assertEqual([s["text"] for s in before["segments"]], ["late", "first", "last"])
        self.assertEqual([s["n"] for s in before["segments"]], [2, 1, 4])
        self.assertEqual(self.state()["next"], 4)
        self.assertEqual((before["meta"]["matched_rev"], before["meta"]["summarized_rev"]), (20, 20))

    def test_append_metadata_only_if_newer_and_present(self):
        self.put()
        ended = (self.now + timedelta(hours=2)).isoformat()
        self.append(segments=[segment()], duration=10, ended_at=ended, complete=True, title="Finished", bookmarks=[{"at": 7}],
                    coverage={"to": 20, "gaps": []})
        self.append(revision=15, **{"from": 1}, segments=[segment(2, "old request, new position")], duration=1,
                    ended_at=(self.now + timedelta(hours=1)).isoformat(), complete=False, title="Old", bookmarks=[], coverage={"to": 0})
        detail = self.detail()
        self.assertEqual((detail["meta"]["revision"], detail["meta"]["next"], detail["meta"]["duration"]), (20, 2, 10))
        self.assertEqual((detail["meta"]["title"], detail["meta"]["ended_at"], detail["meta"]["complete"]), ("Finished", ended, True))
        self.assertEqual((detail["meta"]["bookmarks"], detail["meta"]["coverage"]["to"]), ([{"at": 7}], 20))
        self.append(revision=20, **{"from": 2}, title="Equal revision", bookmarks=[])
        self.append(revision=21, **{"from": 2}, complete=False, ended_at="")
        meta = self.detail()["meta"]
        self.assertEqual((meta["title"], meta["bookmarks"], meta["duration"], meta["coverage"]["to"]), ("Equal revision", [], 10, 20))
        self.assertEqual((meta["ended_at"], meta["complete"]), (ended, True))
        self.assertEqual(self.put(revision=30)[0], 200)
        self.assertFalse(self.detail()["meta"]["complete"])
        self.assertNotIn("ended_at", self.detail()["meta"])

    def test_gap_base_changed_and_repair(self):
        self.put(segments=[segment()])
        self.error(self.append(**{"from": 2}), 409, "segments_gap")
        self.put(revision=30, segments=[segment(9), segment(1, " ")])
        out = self.error(self.append(**{"from": 99}), 409, "base_changed")
        self.assertEqual((out["base"], out["next"]), (30, 2))
        out = self.append(base=30, revision=31, **{"from": 2}, segments=[segment(0)])[1]
        self.assertEqual((out["revision"], out["next"]), (31, 3))
        out = self.put(revision=20)[1]
        self.assertEqual((out["status"], out["revision"], out["base"], out["next"]), ("stale", 31, 30, 3))
        # Equal-revision PUT retains the hub's existing replacement semantics, rather than becoming stale.
        self.assertEqual(self.put(revision=31)[1]["status"], "stored")
        self.assertEqual((self.state()["base"], self.state()["next"]), (31, 0))

    def test_kind_changed_and_not_day(self):
        self.put()
        self.error(self.put(kind="note", revision=1), 409, "kind_changed")
        self.error(self.put(kind="meeting", revision=20), 409, "kind_changed")
        note = str(uuid.uuid4())
        self.put(note, kind="note")
        self.error(self.put(note, revision=20), 409, "kind_changed")
        self.error(self.append(note), 409, "not_day")
        self.error(self.append(note, base=0), 400)
        self.assertEqual(self.detail()["meta"]["kind"], "day")

    def test_append_invalid_fields_and_error_order(self):
        self.put()
        for fields in ({"base": 0}, {"base": None}, {"base": True}, {"revision": 10}, {"revision": True},
                       {"from": -1}, {"from": None}, {"from": 1.5}, {"from": False}, {"segments": {}},
                       {"segments": [segment()] * 501}, {"segments": [segment("0", end=1)]}, {"segments": [segment(True)]},
                       {"segments": [segment(float("inf"))]}, {"title": None}, {"duration": float("nan")},
                       {"bookmarks": "none"}, {"coverage": {"gaps": [{"to": "2"}]}}):
            with self.subTest(fields=fields): self.error(self.append(**fields), 400)
        route = "/api/v1/recordings/%s/segments" % self.rid
        for raw in (b"broken", b"null", b"[]", b"{}"):
            self.error(self.call("POST", route, raw), 400)
        self.error(self.call("POST", route, {}, headers={"X-Mock-Role": "answer"}), 403, "forbidden")
        self.call("POST", "/mock/meeting_attendance", {"kb_read_only": True})
        self.error(self.call("POST", route, b"broken"), 403, "kb_read_only")
        self.error(self.append(str(uuid.uuid4()), base=0), 404, "recording_not_found")
        self.call("DELETE", "/api/v1/recordings/" + self.rid)
        self.error(self.append(base=0), 410, "recording_deleted")
        self.error(self.call("POST", route, b"x" * ((1 << 20) + 1)), 413, "too_large")

    def test_deleted_tombstone(self):
        self.put(segments=[segment()])
        self.assertEqual(self.call("DELETE", "/api/v1/recordings/" + self.rid), (200, {"deleted": True}))
        self.error(self.append(), 410, "recording_deleted")
        self.error(self.put(), 410, "recording_deleted")
        self.error(self.call("GET", "/api/v1/kb/recordings/" + self.rid), 404)
        state = self.call("GET", "/api/v1/recordings")[1]
        self.assertIn(self.rid, state["deleted"])
        self.assertEqual(state["recordings"], [])

    def test_day_full_positions_atomic_on_put_and_append(self):
        self.call("POST", "/mock/recording_day", {"recap": "none"})
        empty = segment(text="")
        self.error(self.put(segments=[empty] * 20001), 413, "day_full")
        self.assertEqual(self.put(segments=[empty] * 20000)[1]["next"], 20000)
        before = self.detail()
        out = self.error(self.append(**{"from": 20000}, segments=[empty], title="must not apply"), 413, "day_full")
        self.assertEqual(out["next"], 20000)
        self.assertEqual(self.detail(), before)
        self.assertEqual(self.append(**{"from": 20000}, complete=True)[1]["next"], 20000)
        self.error(self.put(revision=30, segments=[empty] * 20001), 413, "day_full")
        self.assertEqual(self.state()["base"], 10)

    def test_day_full_bytes_and_no_per_sentence_cut(self):
        self.call("POST", "/mock/recording_day", {"recap": "none"})
        text = "界" * ((4 << 20) // 3) + "x"  # exactly 4 MiB of UTF-8, not a rune count
        self.error(self.put(segments=[segment(text=text + "x")]), 413, "day_full")
        self.put(segments=[segment(text="  " + text + "  ")])
        self.assertEqual(self.detail()["segments"][0]["text"], text)
        self.error(self.append(**{"from": 1}, segments=[segment(text="x")], title="not stored"), 413, "day_full")
        self.assertEqual((self.state()["revision"], self.state()["next"]), (10, 1))
        self.assertEqual(self.append(**{"from": 1}, segments=[segment(text="  ")])[1]["next"], 2)
        self.put(revision=30)
        long = " x" * 5000
        self.append(base=30, revision=31, segments=[segment(text=long)])
        self.assertEqual(self.detail()["segments"][0]["text"], long.strip())

    def test_expired_after_validation_before_kind_and_base(self):
        old = self.now - timedelta(days=30)
        self.assertEqual(self.put(started_at=old.isoformat(), day=old.date().isoformat())[1]["status"], "expired")
        self.assertEqual(self.call("GET", "/api/v1/recordings")[1]["recordings"], [])
        self.assertEqual(self.call("PUT", "/api/v1/assistant", {"recording_days": 60})[0], 200)
        self.assertEqual(self.put(started_at=old.isoformat(), day=old.date().isoformat(), segments=[segment()])[1]["status"], "stored")
        note = str(uuid.uuid4())
        self.put(note, kind="note", started_at=old.isoformat())
        self.call("PUT", "/api/v1/assistant", {"recording_days": 14})
        self.error(self.append(base=0), 400)
        out = self.append(base=99, revision=100, **{"from": 500})[1]
        self.assertEqual((out["status"], out["revision"], out["next"]), ("expired", 10, 1))
        self.assertEqual(self.append(note)[1]["status"], "expired")
        self.assertEqual(self.state()["revision"], 10)

    def test_recap_shape_and_reconciliation(self):
        self.put(tz="Asia/Shanghai", segments=[segment(3700, "I will write the test"), segment(1, "A meeting about exports"),
                                               segment(7500, "A walk outside")],
                 coverage={"to": 8000, "gaps": [{"from": 3000, "to": 3200, "reason": "interrupted"}]})
        detail = self.detail()
        recap = detail["recap"]
        self.assertEqual(set(recap), {"version", "revision", "generated_at", "day", "covers", "final", "summary", "timeline",
                                      "actions", "gaps", "partial", "markdown"})
        self.assertEqual((recap["revision"], recap["version"], recap["final"], recap["covers"]), (10, 1, False, {"from": 0, "to": 8000}))
        self.assertEqual([e["kind"] for e in recap["timeline"]], ["meeting", "outside", "outside"])
        self.assertEqual([e["from"] for e in recap["timeline"]], [1, 3700, 7500])
        self.assertEqual(recap["actions"], [{"id": "a1", "what": "I will write the test", "at": 3700, "todo_id": ""}])
        self.assertNotIn("who", recap["timeline"][0]["points"][0])
        self.assertEqual(detail["summary"], recap["summary"])
        self.assertIn("16:00", recap["markdown"])
        self.assertIn("## Meetings", recap["markdown"])
        self.assertIn("## Outside meetings", recap["markdown"])
        listed = self.state()
        self.assertEqual({k: listed[k] for k in ("kind", "day", "part", "base", "next", "recap_state")},
                         {"kind": "day", "day": self.now.date().isoformat(), "part": 1, "base": 10, "next": 3, "recap_state": "ready"})
        self.assertNotIn("recap_final", listed)
        matches = self.call("GET", "/api/v1/kb/recordings/" + self.rid + "?q=walk")[1]["matches"]
        self.assertTrue(matches)
        self.assertTrue(all(m["path"].endswith("/recap.md") for m in matches))

    def test_global_search_matches_recap_before_and_after_trim(self):
        text = "I will check ZebraQuartz731 with the team"
        self.put(segments=[segment(0, "An ordinary morning"), segment(1, "A quiet afternoon"), segment(2, text)], complete=True)
        self.assertNotIn("ZebraQuartz731", self.detail()["summary"])
        for trimmed in (False, True):
            if trimmed: self.assertEqual(self.call("POST", "/mock/recording_trim", {"id": self.rid})[0], 200)
            for word in ("zebraquartz731", "TEAM zebraQUARTZ731"):
                with self.subTest(trimmed=trimmed, word=word):
                    query = urlencode({"q": word, "kind": "recordings"})
                    matches = self.call("GET", "/api/v1/kb/recordings/" + self.rid + "?" + query)[1]["matches"]
                    self.assertTrue(matches)
                    self.assertTrue(all(m["path"] == "recordings/%s/recap.md" % self.rid for m in matches))
                    status, found = self.call("GET", "/api/v1/kb-search?results=1&" + query)
                    self.assertEqual(status, 200)
                    self.assertEqual(len(found["results"]), 1)
                    result = found["results"][0]
                    self.assertEqual(result["entry"]["id"], self.rid)
                    self.assertEqual(result["count"], len(matches))
                    self.assertEqual(result["hits"], matches[:3])
                    self.assertFalse(result["in_name"])
                    status, found = self.call("GET", "/api/v1/kb-search?" + query)
                    self.assertEqual(status, 200)
                    self.assertEqual(found["hits"], [{"path": m["path"], "line": m["line"]} for m in matches])

    def test_recap_modes_and_manual_requests(self):
        route = "/api/v1/recordings/%s/minutes" % self.rid
        self.put()
        self.assertNotIn("recap_state", self.detail()["meta"])
        self.error(self.call("POST", route), 409, "incomplete")
        for mode in ("none", "pending", "failed", "ready"):
            with self.subTest(mode=mode):
                self.call("POST", "/mock/recording_day", {"recap": mode})
                self.put(revision=100, segments=[segment()])
                detail = self.detail()
                self.assertEqual(detail["meta"].get("recap_state"), None if mode == "none" else mode)
                self.assertEqual("recap" in detail, mode == "ready")
                self.assertEqual(self.call("POST", route), (202, {"state": "pending"}))
                self.assertNotIn("minutes_state", self.detail()["meta"])
        prior = self.detail()["recap"]
        for mode in ("pending", "failed"):
            self.call("POST", "/mock/recording_day", {"recap": mode})
            self.assertEqual(self.detail()["recap"], prior)
        self.call("POST", "/mock/recording_day", {"recap": "ready"})
        self.assertGreater(self.detail()["recap"]["version"], prior["version"])

    def test_final_complete_and_cutoff(self):
        self.put(segments=[segment()], complete=True, ended_at=(self.now + timedelta(hours=1)).isoformat())
        self.assertEqual(self.detail()["recap"]["final_reason"], "complete")
        self.assertTrue(self.state()["recap_final"])
        self.assertTrue(self.detail()["meta"]["recap_final"])
        first = self.detail()["meta"]["recap_final_at"]
        self.assertEqual(self.state()["recap_final_at"], first)
        old = self.now - timedelta(days=2)
        self.put(revision=30, started_at=old.isoformat(), day=old.date().isoformat(), segments=[segment()])
        self.assertEqual(self.detail()["recap"]["final_reason"], "cutoff")
        self.append(base=30, revision=31, **{"from": 1}, complete=True)
        self.assertEqual(self.detail()["recap"]["final_reason"], "complete")

    def test_repair_keeps_first_final_timestamp_when_reopened(self):
        self.put(segments=[segment()], complete=True)
        first = self.detail()["meta"]["recap_final_at"]
        for revision, complete in ((20, True), (30, False), (40, True)):
            with self.subTest(revision=revision, complete=complete):
                self.assertEqual(self.put(revision=revision, segments=[segment()], complete=complete)[0], 200)
                detail = self.detail()
                self.assertEqual(detail["meta"]["recap_final_at"], first)
                self.assertEqual(self.state()["recap_final_at"], first)
                self.assertEqual(detail["recap"]["final"], complete)
                self.assertEqual(detail["meta"].get("recap_final", False), complete)
                self.assertEqual(self.state().get("recap_final", False), complete)
        self.call("POST", "/mock/recording_day", {"recap": "none"})
        self.put(revision=50)
        self.assertNotIn("recap", self.detail())
        self.assertEqual(self.detail()["meta"]["recap_final_at"], first)
        self.assertEqual(self.state()["recap_final_at"], first)

    def test_erased_repair_withdraws_old_items_while_pending(self):
        self.put(segments=[segment(10, "I will erase this secret"), segment(20, "Keep this")])
        self.call("POST", "/mock/recording_day", {"recap": "pending"})
        self.put(revision=30, segments=[segment(20, "Keep this")], coverage={"to": 30, "gaps": [{"from": 0, "to": 15, "reason": "erased"}]})
        detail = self.detail()
        self.assertEqual(detail["summary"], "")
        self.assertEqual(detail["meta"]["recap_state"], "pending")
        self.assertNotIn("secret", json.dumps(detail))
        self.assertEqual(detail["recap"]["actions"], [])
        # The entry overlapped the erased gap, so it went whole (its title could quote the erased words).
        self.assertEqual(detail["recap"]["timeline"], [])
        self.assertEqual(self.call("GET", "/api/v1/kb/recordings/" + self.rid + "?q=secret")[1]["matches"], [])

    def test_todos_idempotent_stale_and_carry_over(self):
        text = "I will send the report"
        self.put(segments=[segment(4, text)])
        route = "/api/v1/recordings/%s/minutes/todos" % self.rid
        version = self.detail()["recap"]["version"]
        self.error(self.call("POST", route, {"version": version, "actions": []}), 400)
        self.error(self.call("POST", route, {"version": version, "actions": ["unknown"]}), 400)
        request = {"version": version, "actions": ["a1", "a1"]}
        status, out = self.call("POST", route, request)
        self.assertEqual(status, 200)
        self.assertEqual(len(out["todos"]), 1)
        tid = out["todos"][0]["id"]
        self.assertTrue(tid.startswith("todo_day_"))
        self.assertIn(self.now.date().isoformat(), out["todos"][0]["notes"])
        self.assertIn("08:00", out["todos"][0]["notes"])
        self.assertEqual(out["recap"]["actions"][0]["todo_id"], tid)
        self.assertEqual(self.call("POST", route, request)[1], out)
        self.put(revision=30, segments=[segment(5, "No action this time")])
        self.error(self.call("POST", route, request), 409, "minutes_stale")
        self.put(revision=40, segments=[segment(6, "  " + text.upper() + "  ")])
        self.assertEqual(self.detail()["recap"]["actions"][0]["todo_id"], tid)
        self.assertEqual(sum(t["id"] == tid for t in self.call("GET", "/api/v1/todos")[1]["todos"]), 1)

    def test_todos_same_wording_confirm_independently(self):
        text = "I will call the team"
        self.put(segments=[segment(at, text) for at in (0, 60, 3600)])
        route = "/api/v1/recordings/%s/minutes/todos" % self.rid
        request = {"version": self.detail()["recap"]["version"], "actions": ["a1", "a2", "a3"]}
        status, out = self.call("POST", route, request)
        self.assertEqual(status, 200)
        ids = [todo["id"] for todo in out["todos"]]
        self.assertEqual(len(set(ids)), 3)
        self.assertEqual([a["todo_id"] for a in out["recap"]["actions"]], ids)
        self.assertEqual(self.call("POST", route, request)[1], out)
        self.assertEqual(sum(t["id"] in ids for t in self.call("GET", "/api/v1/todos")[1]["todos"]), 3)
        self.put(revision=30, segments=[segment(at, text) for at in (59, 3599)])
        self.assertEqual([a["todo_id"] for a in self.detail()["recap"]["actions"]], ids[1:])

    def test_todos_concurrent_confirmations_reuse_only_the_selected_action(self):
        text = "I will call the team"
        self.put(segments=[segment(at, text) for at in (0, 3600)])
        route = "/api/v1/recordings/%s/minutes/todos" % self.rid
        version = self.detail()["recap"]["version"]
        aids = ["a1", "a2"] * 8
        with ThreadPoolExecutor(max_workers=8) as pool:
            replies = list(pool.map(lambda aid: self.call("POST", route, {"version": version, "actions": [aid]}), aids))
        ids = {}
        for aid, (status, out) in zip(aids, replies):
            self.assertEqual(status, 200)
            tid = out["todos"][0]["id"]
            self.assertEqual(ids.setdefault(aid, tid), tid)
        self.assertNotEqual(ids["a1"], ids["a2"])
        self.assertEqual(sum(t["id"] in ids.values() for t in self.call("GET", "/api/v1/todos")[1]["todos"]), 2)

    def test_todo_carry_over_is_one_to_one_within_original_time_window(self):
        text = "I will call the team"
        self.put(segments=[segment(1000, text)])
        route = "/api/v1/recordings/%s/minutes/todos" % self.rid
        version = self.detail()["recap"]["version"]
        status, out = self.call("POST", route, {"version": version, "actions": ["a1"]})
        self.assertEqual(status, 200)
        tid = out["todos"][0]["id"]
        self.put(revision=20, segments=[segment(0, "No action this time")])
        self.put(revision=30, segments=[segment(at, "  " + text.upper() + "  ") for at in (1000, 1001, 1900, 1901)])
        self.assertEqual([a["todo_id"] for a in self.detail()["recap"]["actions"]], [tid, "", "", ""])
        self.put(revision=40, segments=[segment(1900, text)])
        recap = self.detail()["recap"]
        self.assertEqual(recap["actions"][0]["todo_id"], tid)  # Exactly 900 seconds still matches.
        self.assertEqual(self.call("POST", route, {"version": recap["version"], "actions": ["a1"]})[1]["todos"][0]["id"], tid)
        self.put(revision=50, segments=[segment(1901, text)])
        self.assertEqual(self.detail()["recap"]["actions"][0]["todo_id"], "")  # A retry never shifts the original time.
        self.put(revision=60, segments=[segment(7200, text)])
        self.assertEqual(self.detail()["recap"]["actions"][0]["todo_id"], "")

    def test_trim_keeps_recap_and_todos(self):
        self.put(segments=[segment(2, "I will keep the recap")], complete=True)
        recap = self.detail()["recap"]
        self.assertEqual(self.call("POST", "/mock/recording_trim", {"id": self.rid.upper()})[0], 200)
        detail = self.detail()
        self.assertNotIn("segments", detail)
        self.assertEqual(detail["recap"], recap)
        self.assertEqual(detail["summary"], recap["summary"])
        self.assertEqual(detail["meta"]["recap_state"], "ready")
        self.assertIn("trimmed_at", self.state())
        self.error(self.append(base=0), 400)
        self.assertEqual(self.append(base=99, revision=100, **{"from": 500})[1]["status"], "expired")
        put = self.put(revision=40)[1]
        self.assertEqual((put["status"], put["revision"], put["base"], put["next"]), ("expired", 10, 10, 1))
        self.error(self.call("POST", "/api/v1/recordings/%s/minutes" % self.rid), 409, "transcript_expired")
        self.assertEqual(self.call("POST", "/api/v1/recordings/%s/minutes/todos" % self.rid,
                                   {"version": recap["version"], "actions": ["a1"]})[0], 200)
        self.call("POST", "/mock/recording_day", {"recap": "none"})
        self.assertIn("recap", self.detail())
        self.call("DELETE", "/api/v1/recordings/" + self.rid)
        self.error(self.append(), 410, "recording_deleted")

    def test_threaded_retries_do_not_duplicate_positions(self):
        self.put()
        with ThreadPoolExecutor(max_workers=12) as pool:
            replies = list(pool.map(lambda _: self.append(segments=[segment(0, "Concurrent retry")]), range(24)))
        self.assertTrue(all(reply == replies[0] for reply in replies))
        self.assertEqual(replies[0][1]["next"], 1)
        self.assertEqual(len(self.detail()["segments"]), 1)
        self.assertEqual(self.detail()["recap"]["version"], 1)


class RecordingDayEnvironmentMockTests(Hub, unittest.TestCase):
    def test_environment_flag_and_mode(self):
        self.base, stop = start({"MOCK_NO_RECORDING_DAY": "1", "MOCK_DAY_RECAP": "failed", "MOCK_RECORDINGS_DOWN": "0"})
        self.addCleanup(stop)
        self.assertEqual(self.call("GET", "/mock/recording_day"), (200, {"on": False, "recap": "failed"}))
        self.assertNotIn("recording_day", self.call("GET", "/api/v1/health")[1]["features"])
        rid = str(uuid.uuid4())
        self.assertEqual(self.upload(rid, kind="day", day="2026-11-11")[0], 400)
        status, out = self.call("POST", "/api/v1/recordings/%s/segments" % rid, b"invalid")
        self.assertEqual(status, 404)
        self.assertNotIn("code", out)

    def test_unavailable_precedes_size_and_deleted(self):
        self.base, stop = start({"MOCK_RECORDINGS_DOWN": "1", "MOCK_NO_RECORDING_DAY": "0"})
        self.addCleanup(stop)
        status, out = self.call("POST", "/api/v1/recordings/%s/segments" % uuid.uuid4(), b"x" * ((1 << 20) + 1))
        self.assertEqual((status, out["code"]), (503, "kb_unavailable"))


if __name__ == "__main__":
    unittest.main()
