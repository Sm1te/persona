"""Mock hub: a meeting's action items confirmed as to-dos, and minutes written again (#4062, docs/assistant/meeting-minutes.md).

POST /api/v1/recordings/{id}/minutes/todos {version, actions}: one to-do per action, its todo_id written back, nothing made
twice; an old version is 409 minutes_stale. POST …/minutes keeps the minutes shown while the next version is written.
"""
import time
import unittest
from datetime import datetime, timedelta
import uuid

from test_meeting_attendance_mock import Hub, start


class RecordingMinutesTodosMockTests(Hub, unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.base, stop = start({"MOCK_RECORDING_MINUTES": "ready"})
        cls.addClassCleanup(stop)

    def meeting(self):
        rid = str(uuid.uuid4())
        segments = [{"start": 0, "end": 9, "text": "看导出"}, {"start": 10, "end": 19, "text": "张三负责补测试。"},
                    {"start": 20, "end": 29, "text": "李四负责发版说明。"}]
        self.assertEqual(self.upload(rid, complete=True, segments=segments)[1]["status"], "stored")
        return rid

    def minutes(self, rid):
        return self.call("GET", f"/api/v1/kb/recordings/{rid}")[1]["detail"]

    def test_actions_become_todos_once(self):
        rid = self.meeting()
        m = self.minutes(rid)["minutes"]
        # `when` as the hub writes it: the recording's own start in the local zone, and at least a minute; `start` / `end`
        # the same span (#4713).
        start = datetime.fromisoformat("2026-11-10T14:00:00+08:00").astimezone()
        end = start + timedelta(minutes=1)
        self.assertEqual(m["when"], start.strftime("%Y-%m-%d %H:%M") + "–" + end.strftime("%H:%M"))
        self.assertEqual((m["start"], m["end"]), (start.isoformat(timespec="seconds"), end.isoformat(timespec="seconds")))
        self.assertEqual((m["version"], [a["id"] for a in m["actions"]], m["actions"][0]["todo_id"]), (1, ["a1", "a2"], ""))
        path = f"/api/v1/recordings/{rid}/minutes/todos"
        status, out = self.call("POST", path, {"version": 1, "actions": ["a1"]})
        self.assertEqual(status, 200)
        self.assertEqual(len(out["todos"]), 1)
        self.assertTrue(out["todos"][0]["title"].startswith("张三: "))
        self.assertEqual(out["minutes"]["actions"][0]["todo_id"], out["todos"][0]["id"])
        # Both again: only the one not added yet becomes a to-do.
        status, again = self.call("POST", path, {"version": 1, "actions": ["a1", "a2"]})
        self.assertEqual((status, len(again["todos"])), (200, 1))
        self.assertTrue(all(a["todo_id"] for a in self.minutes(rid)["minutes"]["actions"]))
        self.assertEqual(self.call("POST", path, {"version": 1, "actions": []})[0], 400)
        self.assertEqual(self.call("POST", path, {"version": 1, "actions": ["a9"]})[0], 404)

    def test_an_old_version_is_stale(self):
        rid = self.meeting()
        self.assertEqual(self.call("POST", "/mock/recording_minutes_rewrite", {"id": rid})[1]["version"], 2)
        status, out = self.call("POST", f"/api/v1/recordings/{rid}/minutes/todos", {"version": 1, "actions": ["a1"]})
        self.assertEqual((status, out.get("code")), (409, "minutes_stale"))

    def test_regenerating_keeps_the_minutes_shown(self):
        rid = self.meeting()
        self.assertEqual(self.call("POST", f"/api/v1/recordings/{rid}/minutes")[0], 202)
        detail = self.minutes(rid)
        self.assertEqual((detail["meta"]["minutes_state"], detail["minutes"]["version"]), ("pending", 1))
        for _ in range(60):
            detail = self.minutes(rid)
            if detail["meta"]["minutes_state"] == "ready":
                break
            time.sleep(0.1)
        self.assertEqual((detail["meta"]["minutes_state"], detail["minutes"]["version"]), ("ready", 2))


if __name__ == "__main__":
    unittest.main()
