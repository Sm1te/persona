#!/usr/bin/env python3
import base64
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("relay_body_rss", HERE / "relay_body_rss.py")
rss = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rss)

PS = """
  100     1  2048 /opt/persona/personad hub run
  101   100 40000 /opt/persona/personad hub slot
  102   101 50000 /opt/persona/personad assistant
  103   102  9000 /usr/bin/some-helper --flag
  200     1 70000 /somewhere/else/personad hub slot
  201   200 80000 /somewhere/else/personad assistant
"""


def run(label, edge=True, footprint=True, **memory):
    row = lambda base: {"before": base, "peak": base * 4, "after_10s": base * 3, "after": base * 2}
    result = {"label": label, "edge": edge}
    for key in ("slot", "manager"):
        result[key] = row(memory[key])
        if footprint:
            result[key]["footprint"] = row(memory[key] // 2)
    return result


class RelayBodyRSSTests(unittest.TestCase):
    def test_binary_argument(self):
        label, path = rss.parse_binary("base=/tmp/x/personad")
        self.assertEqual((label, Path(path).name), ("base", "personad"))
        self.assertEqual(rss.parse_binary("/tmp/x/personad-head")[0], "personad-head")
        for bad in ("=/tmp/x", "base="):
            with self.assertRaises(Exception):
                rss.parse_binary(bad)

    def test_body_is_the_upload_of_the_issue(self):
        small = json.loads(rss.make_body(0.001))
        self.assertEqual(small["machine_id"], rss.MACHINE)
        self.assertEqual(len(base64.b64decode(small["images"][0]["data"])), int(0.001 * rss.MIB))
        self.assertEqual(rss.make_body(0.001), rss.make_body(0.001))  # the same bytes every run
        body = rss.make_body(16)
        self.assertEqual(len(body), 22_369_743)  # 16 MiB in base64 and the fields around it: the 22.4 MB body
        self.assertTrue(body.startswith(b'{"prompt": "relay rss probe", "machine_id": "relay-rss-no-such-machine"'))

    def test_the_slot_and_the_manager_are_the_hubs_own(self):
        table = rss.process_table(PS)
        self.assertEqual(rss.descendants(table, 100), {101, 102, 103})
        self.assertEqual(rss.pick_processes(table, 100), (101, 102, True))
        # Without an edge the hub itself serves, and the manager is its child.
        self.assertEqual(rss.pick_processes(table, 200), (200, 201, False))
        # Another hub's processes are never taken for this one's.
        self.assertEqual(rss.pick_processes(table, 103), (103, None, False))

    def test_process_table_of_this_machine(self):
        table = rss.read_process_table()
        me = __import__("os").getpid()
        self.assertIn(me, table)
        self.assertEqual(table[me][0], __import__("os").getppid())
        self.assertIn("test_relay_body_rss", table[me][2] + " test_relay_body_rss")  # args read without error

    def test_database_per_run(self):
        pg = rss.Postgres("postgres://persona:secret@127.0.0.1:5432/persona_test?sslmode=disable")
        original = rss.shutil.which
        try:
            rss.shutil.which = lambda name: None  # no psql: the URL as it is
            self.assertEqual(pg.database("relay_rss_0_0"), pg.url)
        finally:
            rss.shutil.which = original

    def test_sampler_readings(self):
        sampler = rss.Sampler([7], 0.05)
        sampler.samples = [(10.0, {7: (100, 60)}), (10.5, {7: (400, 300)}), (11.2, {7: (300, 200)}), (12.4, {7: (120, 70)}), (13.0, {})]
        self.assertEqual(sampler.at(7, 10.4), 100)
        self.assertEqual(sampler.at(7, 9.0), None)
        self.assertEqual(sampler.at(7, 12.4, 1), 70)
        self.assertEqual(sampler.at(7, 99.0), 120)  # the last reading while it lived
        self.assertEqual(sampler.highest(7, 10.2), 400)
        self.assertEqual(sampler.highest(7, 11.0, 1), 200)
        self.assertEqual(sampler.series(7, 10.5), [[0, 400], [1, 120]])

    def test_tables(self):
        results = [run("base", slot=10240, manager=20480), run("base", slot=30720, manager=20480),
                   run("head", slot=5120, manager=5120)]
        text = rss.tables_of(results, ["base", "head"], 60)
        self.assertIn("| Build | Process | RSS before, MB | RSS peak, MB | RSS 10 s after, MB | RSS 60 s after, MB |", text)
        self.assertIn("| base | hub slot | 20.0 (10.0-30.0) | 80.0 (40.0-120.0) | 60.0 (30.0-90.0) | 40.0 (20.0-60.0) |", text)
        self.assertIn("| head | manager | 5.0 | 20.0 | 15.0 | 10.0 |", text)
        self.assertIn("| Build | Process | Footprint before, MB |", text)
        self.assertIn("| head | hub slot | 2.5 | 10.0 | 7.5 | 5.0 |", text)
        plain = rss.tables_of([run("base", edge=False, footprint=False, slot=10240, manager=10240)], ["base"], 5)
        self.assertNotIn("Footprint", plain)
        self.assertNotIn("10 s after", plain)  # a settle of 5 s has no reading at 10
        self.assertIn("| base | hub (no edge) | 10.0 | 40.0 | 20.0 |", plain)

    def test_cli_refuses_before_it_starts_anything(self):
        script = str(HERE / "relay_body_rss.py")
        for arguments in (["--binary", "/nonexistent/personad"],
                          ["--binary", "a=" + sys.executable, "--binary", "a=" + sys.executable],
                          ["--binary", sys.executable, "--rounds", "0"],
                          ["--binary", sys.executable, "--env", "GODEBUG"],
                          ["--binary", sys.executable, "--database-url", "postgres://x", "--rounds", "2"]):
            # No psql on this PATH: a server given by URL then holds one run.
            result = subprocess.run([sys.executable, script, *arguments], capture_output=True, text=True, timeout=60,
                                    env={"PATH": "/nonexistent"})
            self.assertEqual(result.returncode, 2, arguments)
            self.assertNotIn("PASS", result.stdout)


if __name__ == "__main__":
    unittest.main()
