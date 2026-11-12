#!/usr/bin/env python3
"""Post one large request body through a throwaway hub to its built-in manager and report what memory each process
takes for it and how long it keeps it (issue #3199, docs/lessons/server.md).

Everything it starts is its own and is removed at the end:
  - a Postgres 18 container named persona-ci-db-relay-rss-<random> (unless --database-url names a server, as when
    the script itself runs in a Linux container);
  - for each run a fresh database there and a fresh directory under /tmp that is both HOME and PERSONA_HOME's parent
    (short on purpose: the hub's edge needs Unix socket paths under 104 bytes), so the run never reads or writes the
    real ~/.persona, whatever the caller's environment says;
  - `<binary> hub run` on a free loopback port: the edge, its slot (the hub) and the built-in manager as its child.

It then posts a JSON body carrying --body-mib MiB as base64 (16 MiB is a 22.4 MB body) to
POST /api/v1/assistant/conversations for a machine that does not exist. The hub relays the whole body, the manager
decodes the whole body and answers 400, and no conversation starts. The resident set size (RSS) of the slot and of the
manager is sampled every --interval seconds: just before the post, the highest from the post on, and --settle seconds
after the answer. On Linux the peak is the kernel's own (VmHWM); elsewhere it is the highest sample. On macOS the
physical footprint is read too (what Activity Monitor shows as Memory): it counts pages the system compressed or
swapped out, which RSS drops, and not pages the process gave back, so it says what the process still holds when the
system is short of memory.

Usage:
  (cd server && go build -o /tmp/personad-base ./cmd/personad)      # one binary per build to compare
  python3 scripts/diag/relay_body_rss.py --binary base=/tmp/personad-base --binary head=/tmp/personad-head --rounds 3

Several binaries run one after another, in alternating order from round to round, and the tables show each one's
median and range. --posts 2 --gap 5 sends a second body five seconds after the first. Always ends with one line:
PASS / FAIL / TIMEOUT, exit 0 only on PASS.
"""

import argparse
import base64
import ctypes
import ctypes.util
import http.client
import json
import os
from pathlib import Path
import random
import secrets
import shutil
import signal
import socket
import statistics
import subprocess
import sys
import tempfile
import threading
import time
from urllib.parse import urlsplit, urlunsplit

ROUTE = "/api/v1/assistant/conversations"
MACHINE = "relay-rss-no-such-machine"
IMAGE = "postgres:18-alpine"
MIB = 1 << 20
MARKS = (1, 10, 30)  # seconds after the answer that are read besides --settle


class Timeout(Exception):
    pass


class Failed(Exception):
    pass


def parse_binary(arg):
    """label=path, or a path alone (its label is the file's name)."""
    label, sep, path = arg.partition("=")
    if not sep:
        label, path = Path(arg).name, arg
    if not label or not path:
        raise argparse.ArgumentTypeError(f"--binary {arg!r}: expected [label=]path")
    return label, str(Path(path).expanduser().resolve())


def make_body(mib, seed=3199):
    """The request: mib MiB of bytes as one image's base64, for a machine no hub has."""
    data = base64.b64encode(random.Random(seed).randbytes(int(mib * MIB))).decode()
    head = json.dumps({"prompt": "relay rss probe", "machine_id": MACHINE, "images": [{"mime": "image/png", "data": ""}]})
    at = head.rindex('""')
    return (head[: at + 1] + data + head[at + 1 :]).encode()


def process_table(text):
    """`ps -axo pid=,ppid=,rss=,args=` as {pid: (ppid, rss KiB, args)}."""
    table = {}
    for line in text.splitlines():
        parts = line.split(None, 3)
        if len(parts) >= 3 and parts[0].isdigit() and parts[1].isdigit() and parts[2].isdigit():
            table[int(parts[0])] = (int(parts[1]), int(parts[2]), parts[3] if len(parts) > 3 else "")
    return table


def read_process_table():
    """Every process as {pid: (ppid, RSS KiB or 0, args)}: from /proc on Linux (a container's ps may know no such
    columns), from ps elsewhere."""
    if sys.platform.startswith("linux"):
        table = {}
        for entry in Path("/proc").iterdir():
            if entry.name.isdigit():
                try:
                    stat = (entry / "stat").read_text()
                    args = (entry / "cmdline").read_bytes().replace(b"\0", b" ").decode(errors="replace").strip()
                    table[int(entry.name)] = (int(stat.rsplit(")", 1)[1].split()[1]), 0, args)
                except (OSError, ValueError, IndexError):
                    pass  # gone meanwhile
        return table
    return process_table(subprocess.run(["ps", "-axo", "pid=,ppid=,rss=,args="], capture_output=True, text=True, timeout=20).stdout)


def descendants(table, root):
    found, grew = {root}, True
    while grew:
        grew = False
        for pid, (ppid, _, _) in table.items():
            if ppid in found and pid not in found:
                found.add(pid)
                grew = True
    found.discard(root)
    return found


def pick_processes(table, root):
    """The hub's slot and its manager among root's descendants: (slot pid, manager pid, whether an edge runs).
    Without an edge (a socket path too long, PERSONA_HUB_EDGE=0) root itself is the hub."""
    slot = manager = None
    for pid in sorted(descendants(table, root)):
        words = table[pid][2].split()
        if words[1:3] == ["hub", "slot"]:
            slot = pid
        elif words[1:] == ["assistant"]:
            manager = pid
    return (slot or root), manager, slot is not None


class _RUsage(ctypes.Structure):
    """macOS rusage_info_v4 (sys/resource.h)."""

    _fields_ = [("ri_uuid", ctypes.c_uint8 * 16)] + [(name, ctypes.c_uint64) for name in (
        "ri_user_time", "ri_system_time", "ri_pkg_idle_wkups", "ri_interrupt_wkups", "ri_pageins", "ri_wired_size",
        "ri_resident_size", "ri_phys_footprint", "ri_proc_start_abstime", "ri_proc_exit_abstime", "ri_child_user_time",
        "ri_child_system_time", "ri_child_pkg_idle_wkups", "ri_child_interrupt_wkups", "ri_child_pageins",
        "ri_child_elapsed_abstime", "ri_diskio_bytesread", "ri_diskio_byteswritten", "ri_cpu_time_qos_default",
        "ri_cpu_time_qos_maintenance", "ri_cpu_time_qos_background", "ri_cpu_time_qos_utility", "ri_cpu_time_qos_legacy",
        "ri_cpu_time_qos_user_initiated", "ri_cpu_time_qos_user_interactive", "ri_billed_system_time",
        "ri_serviced_system_time", "ri_logical_writes", "ri_lifetime_max_phys_footprint", "ri_instructions", "ri_cycles",
        "ri_billed_energy", "ri_serviced_energy", "ri_interval_max_phys_footprint", "ri_runnable_time")]


def _libproc():
    """macOS libproc when its numbers for this process agree with ps (the struct above is the system's), else None."""
    if sys.platform != "darwin":
        return None
    try:
        lib = ctypes.CDLL(ctypes.util.find_library("proc") or "/usr/lib/libproc.dylib")
        info = _RUsage()
        if lib.proc_pid_rusage(os.getpid(), 4, ctypes.byref(info)) != 0:
            return None
        ps = int(subprocess.run(["ps", "-o", "rss=", "-p", str(os.getpid())], capture_output=True, text=True, timeout=10).stdout)
        resident = info.ri_resident_size // 1024
        return lib if abs(resident - ps) <= max(ps // 5, 2048) and info.ri_phys_footprint > 0 else None
    except (OSError, ValueError, AttributeError, subprocess.SubprocessError):
        return None


LIBPROC = _libproc()


def read_memory(pids):
    """{pid: (RSS, physical footprint or None)} in KiB, of the live ones among pids."""
    out = {}
    if sys.platform.startswith("linux"):
        for pid in pids:
            try:
                for line in Path(f"/proc/{pid}/status").read_text().splitlines():
                    if line.startswith("VmRSS:"):
                        out[pid] = (int(line.split()[1]), None)
            except OSError:
                pass
        return out
    if LIBPROC is not None:
        for pid in pids:
            info = _RUsage()
            if LIBPROC.proc_pid_rusage(pid, 4, ctypes.byref(info)) == 0:
                out[pid] = (info.ri_resident_size // 1024, info.ri_phys_footprint // 1024)
        return out
    res = subprocess.run(["ps", "-o", "pid=,rss=", "-p", ",".join(map(str, pids))], capture_output=True, text=True, timeout=10)
    return {int(a): (int(b), None) for a, b in (line.split() for line in res.stdout.splitlines() if len(line.split()) == 2)}


def read_hwm(pid):
    """The kernel's peak RSS in KiB (Linux), else None."""
    try:
        for line in Path(f"/proc/{pid}/status").read_text().splitlines():
            if line.startswith("VmHWM:"):
                return int(line.split()[1])
    except OSError:
        pass
    return None


class Sampler(threading.Thread):
    """RSS and footprint of pids every interval seconds, as (monotonic time, {pid: (RSS KiB, footprint KiB or None)})."""

    def __init__(self, pids, interval):
        super().__init__(daemon=True)
        self.pids, self.interval = list(pids), interval
        self.samples, self.done = [], threading.Event()

    def run(self):
        while not self.done.is_set():
            at = time.monotonic()
            try:
                self.samples.append((at, read_memory(self.pids)))
            except (OSError, ValueError, subprocess.SubprocessError):
                pass
            self.done.wait(max(0.0, self.interval - (time.monotonic() - at)))

    def stop(self):
        self.done.set()
        self.join(timeout=15)

    def at(self, pid, when, which=0):
        """The last sample of pid at or before when (which: 0 RSS, 1 footprint)."""
        last = None
        for at, memory in self.samples:
            if at > when:
                break
            if pid in memory:
                last = memory[pid][which]
        return last

    def highest(self, pid, start, which=0):
        values = [memory[pid][which] for at, memory in self.samples if at >= start and pid in memory and memory[pid][which] is not None]
        return max(values) if values else None

    def series(self, pid, start, which=0):
        """One reading per second from start on: [seconds, KiB]."""
        out, second = [], -1
        for at, memory in self.samples:
            if at >= start and pid in memory and int(at - start) > second and memory[pid][which] is not None:
                second = int(at - start)
                out.append([second, memory[pid][which]])
        return out


def docker(*args, timeout=120, check=True):
    res = subprocess.run(["docker", *args], capture_output=True, text=True, timeout=timeout)
    if check and res.returncode != 0:
        raise Failed(f"docker {' '.join(args[:2])}: {res.stderr.strip() or res.stdout.strip()}")
    return res.stdout.strip()


class Postgres:
    """A Postgres 18 container of this run's own (as scripts/ci/server.sh makes one), or a server given by URL."""

    def __init__(self, url):
        self.url, self.container, self.tag = url, None, secrets.token_hex(3)

    def start(self):
        if self.url:
            return
        self.container = "persona-ci-db-relay-rss-" + secrets.token_hex(4)
        docker("run", "--detach", "--name", self.container, "-e", "POSTGRES_USER=persona", "-e", "POSTGRES_PASSWORD=ci-only",
               "-e", "POSTGRES_DB=persona", "-p", "127.0.0.1::5432", IMAGE, timeout=300)
        deadline = time.monotonic() + 90
        # Over TCP: the image's first start answers on its socket before the real server is up.
        while subprocess.run(["docker", "exec", self.container, "pg_isready", "-h", "127.0.0.1", "-U", "persona", "-d", "persona"],
                             capture_output=True, timeout=20).returncode != 0:
            if time.monotonic() > deadline:
                raise Timeout("Postgres did not become ready in 90 s")
            time.sleep(1)
        port = docker("port", self.container, "5432/tcp").splitlines()[0].rsplit(":", 1)[1]
        self.url = f"postgres://persona:ci-only@127.0.0.1:{port}/persona?sslmode=disable"

    def database(self, name):
        """A fresh database's URL: in the container, or in the server given by URL when psql is here to make it. Without
        psql that URL's own database is used as it is (one run)."""
        name = f"{name}_{self.tag}"
        create = ["-v", "ON_ERROR_STOP=1", "-c", f'CREATE DATABASE "{name}"']
        if self.container:
            docker("exec", self.container, "psql", "-U", "persona", "-d", "persona", *create)
        elif shutil.which("psql"):
            res = subprocess.run(["psql", self.url, *create], capture_output=True, text=True, timeout=60)
            if res.returncode != 0:
                raise Failed(f"psql could not make a database: {res.stderr.strip()[:200]}")
        else:
            return self.url
        return urlunsplit(urlsplit(self.url)._replace(path="/" + name))

    def remove(self):
        if self.container:
            docker("rm", "-f", "-v", self.container, check=False)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def get(port, path, token, timeout=10):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=timeout)
    try:
        conn.request("GET", path, headers={"Authorization": "Bearer " + token})
        res = conn.getresponse()
        return res.status, res.read()
    finally:
        conn.close()


def wait_for(what, seconds, ready, proc):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise Failed(f"the hub exited with status {proc.returncode} while waiting for {what}")
        try:
            if ready():
                return
        except (OSError, http.client.HTTPException):
            pass
        time.sleep(0.5)
    raise Timeout(f"{what} not within {seconds} s")


def post(port, token, body, chunked):
    conn = http.client.HTTPConnection("127.0.0.1", port, timeout=300)
    headers = {"Authorization": "Bearer " + token, "Content-Type": "application/json"}
    try:
        if chunked:  # no Content-Length: the hub cannot know the size before it has read the body
            step = 1 << 16
            conn.request("POST", ROUTE, body=(body[i : i + step] for i in range(0, len(body), step)), headers=headers, encode_chunked=True)
        else:
            conn.request("POST", ROUTE, body=body, headers=headers)
        res = conn.getresponse()
        return res.status, res.read()
    finally:
        conn.close()


def stop_hub(proc, pids_before):
    """SIGTERM to the edge (it releases its slot, which stops the manager), then whatever is left of its tree."""
    table = read_process_table()
    tree = {pid: table[pid][2] for pid in descendants(table, proc.pid) | (set(pids_before) & set(table))}
    if proc.poll() is None:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            pass
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass
    proc.wait(timeout=30)
    time.sleep(0.5)
    table = read_process_table()
    for pid, args in tree.items():  # one that left the process group; the same command still, so the same process
        if pid in table and table[pid][2] == args:
            try:
                os.kill(pid, signal.SIGKILL)
            except (ProcessLookupError, PermissionError):
                pass


def run_once(label, binary, pg, body, args, name):
    """One hub's life: start, warm up, post, settle. Returns the numbers in KiB."""
    root = Path(tempfile.mkdtemp(prefix="persona-rss-", dir="/tmp"))
    home = root / ".persona"
    home.mkdir(mode=0o700)
    (root / "tmp").mkdir()
    port, token = free_port(), secrets.token_hex(24)
    config = {"listen": f"127.0.0.1:{port}", "public_url": f"http://127.0.0.1:{port}", "token": token,
              "database_url": pg.database(name), "machines": [], "admin": {"access": "loopback"}, "mesh": {"discovery": False}}
    (home / "hub.json").write_text(json.dumps(config))
    (home / "hub.json").chmod(0o600)
    env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "HOME": str(root), "PERSONA_HOME": str(home), "TMPDIR": str(root / "tmp"),
           "LANG": "en_US.UTF-8", "PERSONA_LOCAL_NODE": "0"}
    env.update(kv.split("=", 1) for kv in args.env)
    log = open(root / "hub.log", "wb")
    proc = subprocess.Popen([binary, "hub", "run"], env=env, cwd=root, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT,
                            start_new_session=True)
    sampler, watched, keep = None, [], False
    try:
        wait_for("the hub's health", 120, lambda: get(port, "/api/v1/health", token)[0] == 200, proc)
        wait_for("the built-in manager", 180, lambda: get(port, "/api/v1/assistant/capabilities", token)[0] == 200, proc)
        table = read_process_table()
        slot, manager, edge = pick_processes(table, proc.pid)
        if manager is None:
            raise Failed("no manager process among the hub's children")
        watched = [slot, manager]
        sampler = Sampler(watched, args.interval)
        sampler.start()
        time.sleep(args.warmup)
        result = {"label": label, "edge": edge, "body_bytes": len(body), "posts": []}
        began = time.monotonic()
        for i in range(args.posts):
            if i > 0:
                time.sleep(args.gap)
            sent = time.monotonic()
            status, answer = post(port, token, body, args.chunked)
            took = time.monotonic() - sent
            if status != 400 or MACHINE.encode() not in answer:
                raise Failed(f"expected 400 naming {MACHINE}, got {status}: {answer[:200]!r}")
            result["posts"].append(round(took, 3))
        answered = time.monotonic()
        time.sleep(args.settle + 2 * args.interval)
        sampler.stop()
        for key, pid in (("slot", slot), ("manager", manager)):
            row = {"before": sampler.at(pid, began), "peak": sampler.highest(pid, began), "after": sampler.at(pid, answered + args.settle)}
            for mark in MARKS:
                if mark < args.settle:
                    row[f"after_{mark}s"] = sampler.at(pid, answered + mark)
            hwm = read_hwm(pid)
            if hwm is not None and row["peak"] is not None:  # the kernel's mark can trail the counter by a little
                row["peak"] = max(row["peak"], hwm)
            if None in row.values():
                raise Failed(f"the {key} (pid {pid}) was gone before the end")
            row["series"] = sampler.series(pid, answered)
            if sampler.at(pid, began, 1) is not None:  # macOS: the physical footprint beside it
                row["footprint"] = {"before": sampler.at(pid, began, 1), "peak": sampler.highest(pid, began, 1),
                                    "after": sampler.at(pid, answered + args.settle, 1), "series": sampler.series(pid, answered, 1)}
                for mark in MARKS:
                    if mark < args.settle:
                        row["footprint"][f"after_{mark}s"] = sampler.at(pid, answered + mark, 1)
            result[key] = row
        return result
    except BaseException:
        keep = args.keep_failed
        raise
    finally:
        if sampler is not None:
            sampler.stop()
        stop_hub(proc, watched)
        log.close()
        if args.logs:
            Path(args.logs).mkdir(parents=True, exist_ok=True)
            shutil.copyfile(root / "hub.log", Path(args.logs) / f"{name}-{label}.log")
        if keep:
            print(f"  kept {root} (hub.log)", flush=True)
        else:
            shutil.rmtree(root, ignore_errors=True)


def mb(kib):
    return f"{kib / 1024:.1f}"


def tables_of(results, labels, settle):
    """Markdown: per build and process, the median over its rounds, with the range when there is more than one. RSS,
    and the physical footprint where the runs have it (macOS)."""
    def cell(values):
        if len(values) == 1:
            return mb(values[0])
        return f"{mb(statistics.median(values))} ({mb(min(values))}-{mb(max(values))})"

    columns = [("before", "before"), ("peak", "peak")]
    columns += [(f"after_{mark}s", f"{mark} s after") for mark in MARKS if mark == 10 and mark < settle]
    columns.append(("after", f"{settle:g} s after"))
    out = []
    for what, pick in (("RSS", lambda row: row), ("Footprint", lambda row: row.get("footprint"))):
        if not results or any(pick(r[key]) is None for r in results for key in ("slot", "manager")):
            continue
        lines = ["| Build | Process | " + " | ".join(f"{what} {title}, MB" for _, title in columns) + " |",
                 "| --- | --- |" + " ---: |" * len(columns)]
        for label in labels:
            runs = [r for r in results if r["label"] == label]
            for key, title in (("slot", "hub slot"), ("manager", "manager")):
                if runs:
                    if key == "slot" and not runs[0]["edge"]:
                        title = "hub (no edge)"
                    lines.append(f"| {label} | {title} | " + " | ".join(cell([pick(r[key])[c] for r in runs]) for c, _ in columns) + " |")
        out.append("\n".join(lines))
    return "\n\n".join(out)


def load():
    try:
        return "load " + " ".join(f"{v:.2f}" for v in os.getloadavg())
    except OSError:
        return "load unknown"


def main():
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--binary", action="append", type=parse_binary, required=True, metavar="[LABEL=]PATH",
                        help="a personad (or persona-hub with persona-manager beside it) to measure; repeat to compare builds")
    parser.add_argument("--rounds", type=int, default=1, help="runs per binary (default 1)")
    parser.add_argument("--body-mib", type=float, default=16, help="MiB of bytes sent as base64 (default 16: a 22.4 MB body)")
    parser.add_argument("--posts", type=int, default=1, help="posts per run, one after another (default 1)")
    parser.add_argument("--gap", type=float, default=0, help="seconds between a run's posts (default 0)")
    parser.add_argument("--chunked", action="store_true", help="send the body without a Content-Length")
    parser.add_argument("--warmup", type=float, default=20, help="seconds idle before the post (default 20)")
    parser.add_argument("--settle", type=float, default=60, help="seconds after the answer for the last reading (default 60)")
    parser.add_argument("--interval", type=float, default=0.05, help="seconds between RSS samples (default 0.05)")
    parser.add_argument("--env", action="append", default=[], metavar="KEY=VALUE", help="an environment variable for the hub (GODEBUG=…)")
    parser.add_argument("--database-url", help="a Postgres server to use instead of a container (inside a container, say); each run "
                        "gets a database of its own there when psql is installed, else the URL's own, for one run")
    parser.add_argument("--json", help="write every run's numbers (KiB) to this file")
    parser.add_argument("--logs", metavar="DIR", help="copy every run's hub.log (the hub's and the manager's output) here")
    parser.add_argument("--keep-failed", action="store_true", help="keep a failed run's directory and hub.log")
    args = parser.parse_args()
    labels = [label for label, _ in args.binary]
    if len(set(labels)) != len(labels):
        parser.error("--binary labels must differ")
    for _, path in args.binary:
        if not os.access(path, os.X_OK):
            parser.error(f"{path} is not an executable")
    if any("=" not in kv for kv in args.env):
        parser.error("--env takes KEY=VALUE")
    if args.database_url and (args.rounds > 1 or len(args.binary) > 1) and not shutil.which("psql"):
        parser.error("--database-url without psql holds one run; install psql or leave it out to compare builds or run rounds")
    if args.rounds < 1 or args.posts < 1 or args.interval <= 0 or args.body_mib <= 0 or min(args.settle, args.warmup, args.gap) < 0:
        parser.error("--rounds and --posts are at least 1, --interval and --body-mib above 0, the waits not negative")

    body = make_body(args.body_mib)
    print(f"body {len(body) / 1e6:.1f} MB ({len(body)} bytes), settle {args.settle:g} s, {load()}", flush=True)
    pg, results, verdict, why = Postgres(args.database_url), [], "PASS", ""
    try:
        pg.start()
        for round_ in range(args.rounds):
            order = args.binary if round_ % 2 == 0 else list(reversed(args.binary))
            for label, path in order:
                print(f"round {round_ + 1}/{args.rounds} {label}: starting ({load()})", flush=True)
                r = run_once(label, path, pg, body, args, f"relay_rss_{round_}_{labels.index(label)}")
                r["round"], r["load"] = round_ + 1, load()
                results.append(r)
                print(f"  slot {mb(r['slot']['before'])} -> peak {mb(r['slot']['peak'])} -> {mb(r['slot']['after'])} MB, "
                      f"manager {mb(r['manager']['before'])} -> peak {mb(r['manager']['peak'])} -> {mb(r['manager']['after'])} MB, "
                      f"post {r['posts'][0]:.2f} s", flush=True)
    except Timeout as e:
        verdict, why = "TIMEOUT", str(e)
    except (Failed, OSError, subprocess.SubprocessError, http.client.HTTPException) as e:
        verdict, why = "FAIL", str(e)
    except KeyboardInterrupt:
        verdict, why = "FAIL", "interrupted"
    finally:
        pg.remove()
    if results:
        print()
        print(tables_of(results, labels, args.settle))
        print()
    if args.json:
        Path(args.json).write_text(json.dumps({"body_bytes": len(body), "settle_seconds": args.settle, "runs": results}, indent=1) + "\n")
    runs = f"{len(results)} of {args.rounds * len(args.binary)} runs"
    print(f"{verdict}: {runs}, a {len(body) / 1e6:.1f} MB body relayed and decoded" + (f"; {why}" if why else ""))
    return 0 if verdict == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
