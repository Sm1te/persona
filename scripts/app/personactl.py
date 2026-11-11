#!/usr/bin/env python3
"""personactl: drive a running DEBUG Persona in the simulator through its runtime CLI (docs/operations/runtime-cli.md).

  personactl.py launch --hub http://127.0.0.1:18760 [-- <more launch args>]   terminate, launch with the channel, wait
  personactl.py ping | state
  personactl.py settle [--timeout s] [--pixels n] [--stable n]                 wait until the screen stops changing
               [--ignore-id prefix …] [--ignore-label-contains t …] [--ignore-rect x,y,w,h …]   … except there
  personactl.py freeze [--off] [--layers strip|pause|none] [--keep-motion]     hold still what animates (Reduce Motion…)
  personactl.py online [--timeout s]                                           wait for the hub's live connection
                                                                              (+ mock_scenarios.py's restarts of the hub)
  personactl.py idle [--timeout s]                                             no request to the mock hub for 0.8 s
  personactl.py screenshot <out.png> [--no-settle]                             settle, then `simctl io screenshot`
  personactl.py reset [--cold-chats] [--drop-chat-cache]                      a new view tree, as after a launch
  personactl.py navigate home|assistant|agents|chat|inbox|settings|machines [--session m/s] [--machine id]
               [--flag key=value …] [--keep] [--cold-chats]                    the page a launch switch opens
  personactl.py set-style <glass|clay|island|lark|beautiful> [--color c]       UI style / theme color
  personactl.py appearance light|dark [--in-app]                               the simulator's (default) or the app's
  personactl.py defaults [--set k=v …] [--remove k …] [--get k …]              this run's switches, saved keys
  personactl.py tree [filters] | find [filters]                                the accessibility tree (what XCUITest sees)
  personactl.py wait [filters] [--gone] [--timeout s]                          until an element is there (or gone)
  personactl.py wait-loaded [--session m/s] [--timeout s]                      the chat on screen has its history from the hub
  personactl.py tap [filters] [--hid] [--allow-hidden]                         activate it; a real touch (idb) if it does not take that
  personactl.py tap [filters] --action NAME                                    its custom accessibility action (a menu's item it offers)
  personactl.py alert [--button T]                                             the app's alert: its buttons, or press one
  personactl.py longpress [filters] [--duration s]                             a real touch held on it (idb)
  personactl.py swipe [filters] --dir left|right|up|down [--distance pt] [--duration s]   a real swipe across it
  personactl.py drag --from x,y --to x,y [--duration s]                        a real finger between two points (pt)
  personactl.py background [--seconds s] | foreground                          the Home button | the app back, same process
  personactl.py scroll [--pages n | --to top|bottom]                           the largest scroll view, no fling
  personactl.py type --text T [filters] | dismiss-keyboard                     into the focused field (filters: focus that first)
  personactl.py snapshot <out.png>                                             the app's windows, no status bar
  personactl.py scenario [name] [--env MOCK_X=1,…] [--control port]           switch the mock hub's data set (mock_scenarios.py)
  personactl.py perf | refresh                                                 view-body counts (-perfCounts YES) | pull the hub
  personactl.py call <command> [key=value …]                                   any command the app knows

A DEBUG build on a real phone, through the hub (docs/operations/runtime-cli.md "Reaching a real device through the hub"): --via-hub <hub url> (or
$PERSONACTL_VIA_HUB) sends every command to POST /api/v1/phone/runtime-cli; the hub's token comes from $PERSONACTL_HUB_TOKEN,
the file $PERSONACTL_HUB_TOKEN_FILE, or hub.json in the selected state root on the hub's machine, and is never printed. state, settle, tree,
find, wait, tap (accessibility activation only), scroll, type, snapshot and screenshot work that way; launch, reset and
the idb gestures do not (install the DEBUG build with Xcode or devicectl and open it yourself, with -runtimeCLIHub YES).

Element filters: --id, --id-prefix, --label, --label-contains, --label-prefix, --trait button|text|header|selected…,
--visible (a touch at its middle reaches it), --on-screen (its frame is on screen, covered or not), --containers (also the
containers that have an id, such as the chat's `chat.*` marks), --index n (else the first match a touch reaches). The simulator is $PERSONACTL_UDID (or --udid, default "booted"); the app is $PERSONACTL_BUNDLE / $PERSONA_BUNDLE_ID.
`launch` picks a one-time token and keeps it, with the port, in ~/.cache/personactl/<udid>.json for the later
commands. Standard library only; scripts can `from personactl import Ctl`.
"""
import argparse
import base64
import json
import os
import secrets
import socket
import subprocess
import sys
import time
import urllib.parse
import urllib.error
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from persona_identity import data_dir, env_value, service_environment

STATE_DIR = os.path.expanduser("~/.cache/personactl")
DEFAULT_PORT = 18950


def port_taken(port):
    """Whether something on the Mac holds 127.0.0.1:`port` (the app's own listener included)."""
    with socket.socket() as s:
        try:
            s.bind(("127.0.0.1", port))
        except OSError:
            return True
    return False


class CtlError(Exception):
    pass


def sh(*cmd, check=True):
    return subprocess.run(cmd, check=check, text=True, capture_output=True)


def hub_token():
    """The hub's credential for --via-hub: never printed or logged."""
    if os.environ.get("PERSONACTL_HUB_TOKEN"):
        return os.environ["PERSONACTL_HUB_TOKEN"].strip()
    path = os.environ.get("PERSONACTL_HUB_TOKEN_FILE")
    if path:
        with open(os.path.expanduser(path)) as f:
            return f.read().strip()
    try:
        with open(os.path.join(data_dir(environment=service_environment(role="hub")), "hub.json")) as f:
            return json.load(f).get("token", "")
    except (OSError, ValueError):
        raise CtlError("--via-hub needs the hub's token: $PERSONACTL_HUB_TOKEN or $PERSONACTL_HUB_TOKEN_FILE")


class Ctl:
    def __init__(self, udid=None, bundle=None, port=None, via_hub=None):
        self.via_hub = (via_hub or os.environ.get("PERSONACTL_VIA_HUB") or "").rstrip("/") or None
        self.udid = udid or os.environ.get("PERSONACTL_UDID") or "booted"
        self.bundle = bundle or os.environ.get("PERSONACTL_BUNDLE") or env_value("PERSONA_BUNDLE_ID") or "com.example.persona"
        saved = self._load()
        # PERSONACTL_PORT: a port of this simulator's own, when another one's app may listen on the default
        self.port = port or int(os.environ.get("PERSONACTL_PORT") or 0) or saved.get("port") or DEFAULT_PORT
        self.token = saved.get("token", "")
        self.hub = saved.get("hub")

    def _path(self):
        return os.path.join(STATE_DIR, self.udid + ".json")

    def _load(self):
        try:
            with open(self._path()) as f:
                return json.load(f)
        except (OSError, ValueError):
            return {}

    def _save(self):
        os.makedirs(STATE_DIR, exist_ok=True)
        path = self._path()
        with open(path, "w") as f:
            json.dump({"port": self.port, "token": self.token, "hub": self.hub}, f)
        os.chmod(path, 0o600)

    def call(self, cmd, _timeout=None, **args):
        """One request per connection: a JSON line out, a JSON line back. Raises CtlError when the app says no."""
        if self.via_hub:
            return self._call_hub(cmd, _timeout, args)
        req = json.dumps({"token": self.token, "cmd": cmd, "args": {k: v for k, v in args.items() if v is not None}})
        wait = _timeout or (args.get("timeout") or 0) + 30
        with socket.create_connection(("127.0.0.1", self.port), timeout=wait) as s:
            s.sendall(req.encode() + b"\n")
            buf = b""
            while not buf.endswith(b"\n"):
                chunk = s.recv(1 << 16)
                if not chunk:
                    break
                buf += chunk
        r = json.loads(buf or b'{"ok": false, "error": "no answer (did the app crash?)"}')
        if not r.get("ok"):
            raise CtlError(f"{cmd}: {r.get('error')}")
        return r

    def _call_hub(self, cmd, timeout, args):
        """The command through the hub to the phone's DEBUG app (POST /api/v1/phone/runtime-cli)."""
        if cmd == "reset":
            raise CtlError("reset is not done on a real phone yet: navigate builds a new view tree too")
        wait = timeout or (args.get("timeout") or 0) + 30
        body = json.dumps({"cmd": cmd, "args": {k: v for k, v in args.items() if v is not None}, "timeout": wait}).encode()
        req = urllib.request.Request(self.via_hub + "/api/v1/phone/runtime-cli", data=body, method="POST",
                                     headers={"Content-Type": "application/json", "Authorization": "Bearer " + hub_token()})
        try:
            with urllib.request.urlopen(req, timeout=wait + 10) as resp:
                r = json.load(resp)
        except urllib.error.HTTPError as e:
            try:
                why = json.load(e).get("error")
            except ValueError:
                why = e.reason
            if e.code == 503:
                why = "no phone offers the runtime CLI (a DEBUG build launched with -runtimeCLIHub YES, open, on this hub)"
            raise CtlError(f"{cmd}: hub {e.code}: {why}")
        if not r.get("ok"):
            raise CtlError(f"{cmd}: {r.get('error')}")
        return r

    def _simulator_only(self, what):
        if self.via_hub:
            raise CtlError(f"{what} needs the simulator (idb / simctl); over the hub there is no such thing on a real phone")

    def launch(self, hub=None, extra=(), wait=20, pair="mock"):
        """Terminates the app and launches it with a fresh token (paired with `hub` when given, with the hub token `pair`:
        scripts/mock/mock_hub.py playing a sync case takes `mock-<device>` as that device; "" keeps the pairing the app
        has, as a relaunch does, still as an automated run: `-pairURL keep` pairs with nothing, and a `pairURL` at all
        keeps the app from asking for notifications, a system alert only a touch answers); waits for `ping`."""
        if self.via_hub:
            raise CtlError("launch drives the simulator only: on a real phone install the DEBUG build with Xcode or "
                           "devicectl and open it yourself (launch arguments: -runtimeCLIHub YES)")
        self.hub = hub or self.hub
        self.token = secrets.token_hex(16)
        sh("xcrun", "simctl", "terminate", self.udid, self.bundle, check=False)
        args = ["-runtimeCLI", str(self.port), "-runtimeCLIToken", self.token]
        if self.hub and pair:
            args += ["-pairURL", "persona://pair?url=" + urllib.parse.quote(self.hub, safe="") + "&token=" + urllib.parse.quote(pair, safe="")]
        elif self.hub:
            args += ["-pairURL", "keep"]
        sh("xcrun", "simctl", "launch", self.udid, self.bundle, *args, *extra)
        self._save()
        deadline = time.time() + wait
        while True:
            try:
                return self.call("ping", _timeout=2)
            except (OSError, CtlError, ValueError):
                if time.time() > deadline:
                    if port_taken(self.port):
                        # the simulator shares the Mac's loopback: a mock hub on this port keeps the app's listener out
                        raise CtlError("the app did not answer: something else listens on port %d (set PERSONACTL_PORT "
                                       "to a port nothing else uses), or the app hangs" % self.port)
                    raise CtlError("the app did not answer (a DEBUG build? did it crash?)")
                time.sleep(0.2)

    def idle(self, quiet=0.8, timeout=10):
        """Network idle, seen from the mock hub (scripts/mock/mock_hub.py counts every GET at /mock/hits): waits until no
        request has come in for `quiet` seconds. A page that is still waiting for its data (machines' metrics) looks
        settled to `settle`. Returns how many requests came in meanwhile."""
        if not self.hub:
            raise CtlError("idle needs the mock hub (launch --hub)")

        def total():
            with urllib.request.urlopen(self.hub.rstrip("/") + "/mock/hits", timeout=5) as r:
                # The app's requests only: these polls of /mock/hits are counted too.
                return sum(n for k, n in json.load(r).get("hits", {}).items() if " /mock/" not in k)

        start = last = total()
        changed, deadline = time.time(), time.time() + timeout
        while time.time() < deadline:
            time.sleep(0.1)
            now = total()
            if now != last:
                last, changed = now, time.time()
            elif time.time() - changed >= quiet:
                return {"ok": True, "idle": True, "requests": now - start}
        return {"ok": True, "idle": False, "requests": last - start}

    def tap(self, hid=False, **f):
        """Accessibility activation (a button's action, a menu opening), or with `hid` / when the element does not take
        it (a menu's items) a real touch at the middle of its frame through `idb ui tap`, once the screen settles (an
        opening menu moves its items under the touch). Only on a match a touch reaches (`allowHidden=True`: any).
        `action`: the element's custom accessibility action of that name instead (no touch does that)."""
        if f.get("action"):
            return self.call("activate", **f)
        if not hid:
            try:
                r = self.call("activate", **f)
            except CtlError as e:
                if "not visible" not in str(e):
                    raise
                # An opening menu's items fade in: not reached yet, then reached once the screen settles.
                self.call("settle", timeout=2, stable=2, every=80, pixels=600)
                r = self.call("activate", **f)
            if r.get("activated"):
                return r
        if self.via_hub:
            raise CtlError(f"tap: {f} takes no accessibility activation, and a real touch needs idb (the simulator)")
        self.call("settle", timeout=2, stable=2, every=80, pixels=600)
        e = self.call("find", **(f if f.get("allowHidden") or "index" in f else {"visibleOnly": True} | f))["element"]
        if not e:
            raise CtlError(f"tap: no match a touch reaches {f}")
        x, y, w, h = e["frame"]
        self._idb("tap", round(x + w / 2), round(y + h / 2))
        return {"ok": True, "hid": True, "element": e}

    def _idb(self, *args):
        """`idb ui <args>` on this simulator; idb's companion may need a first call to come up."""
        self._simulator_only(f"idb ui {args[0]}")
        for _ in range(2):
            r = subprocess.run(["idb", "ui", *map(str, args), "--udid", self.udid], text=True, capture_output=True)
            if r.returncode == 0:
                return
            sh("idb", "connect", self.udid, check=False)
        raise CtlError(f"idb ui {args[0]} failed: {r.stderr.strip()[-200:]}")

    def frame(self, **f):
        """Where the element is once the screen settles (what moves under a finger moves first): [x, y, w, h] in
        points; the whole screen without filters."""
        self.call("settle", timeout=2, stable=2, every=80, pixels=600)
        if not f:
            return [0, 0, *self.call("state")["screen"]]
        e = self.call("find", **f)["element"]
        if not e:
            raise CtlError(f"not found {f}")
        return e["frame"]

    def longpress(self, duration=1.2, **f):
        """A real touch held at the middle of the element (a row's context menu), as XCUITest's press(forDuration:)."""
        x, y, w, h = self.frame(**f)
        at = [round(x + w / 2), round(y + h / 2)]
        self._idb("tap", *at, "--duration", duration)
        return {"ok": True, "at": at, "duration": duration}

    def swipe(self, direction, distance=None, duration=0.25, **f):
        """A real swipe through the middle of the element (the whole screen without filters), as XCUITest's swipeLeft /
        swipeUp: across half its width for left / right (a row's swipe actions show; a longer one is a full swipe,
        which runs the first action), 70 % of its height for up / down, or `distance` points."""
        sign = {"left": -1, "up": -1, "right": 1, "down": 1}.get(direction)
        if sign is None:
            raise CtlError(f"swipe: --dir left|right|up|down, not {direction!r}")
        x, y, w, h = self.frame(**f)
        cx, cy = x + w / 2, y + h / 2
        horizontal = direction in ("left", "right")
        half = distance / 2 if distance else (w * 0.25 if horizontal else h * 0.35)
        if horizontal:
            return self.drag((cx - sign * half, cy), (cx + sign * half, cy), duration)
        return self.drag((cx, cy - sign * half), (cx, cy + sign * half), duration)

    def drag(self, start, end, duration=1.0):
        """A real finger from `start` to `end` ((x, y) in points) over `duration` seconds: slow is a drag (the pull that
        shows the Agents search field), fast a fling."""
        pts = [round(v) for v in (*start, *end)]
        self._idb("swipe", *pts, "--duration", duration, "--delta", 5)
        return {"ok": True, "from": pts[:2], "to": pts[2:], "duration": duration}

    def background(self, seconds=0):
        """The Home button: the app goes to the background, the process stays; `seconds` there before returning."""
        self._idb("button", "HOME")
        time.sleep(seconds)
        return {"ok": True}

    def foreground(self, wait=10):
        """The app back in front, the same process (`simctl launch` of a running app activates it, no relaunch);
        returns once it is active again."""
        self._simulator_only("foreground")
        before = self._pid()
        sh("xcrun", "simctl", "launch", self.udid, self.bundle)
        deadline = time.time() + wait
        while True:
            try:
                if self.call("state", _timeout=2).get("appState") == "active":
                    pid = self._pid()
                    return {"ok": True, "pid": pid, "samePid": before is None or pid == before}
            except (OSError, CtlError, ValueError):
                pass
            if time.time() > deadline:
                raise CtlError("foreground: the app is not active again")
            time.sleep(0.2)

    def _pid(self):
        try:
            return self.call("ping", _timeout=2).get("pid")
        except (OSError, CtlError, ValueError):
            return None

    def type(self, text, **f):
        """Types into the focused field; with filters, focuses that element first: accessibility activation, and when
        that leaves no field focused (a SwiftUI field often), a real touch on it."""
        try:
            return self.call("type", text=text, **f)
        except CtlError as e:
            if not f or "no text field has focus" not in str(e):
                raise
        self.tap(hid=True, **f)
        time.sleep(0.4)
        return self.call("type", text=text)

    def scenario(self, name=None, env=None, control=None, wait=20):
        """Switches the data set of the hub scripts/mock/mock_scenarios.py runs (its control port: `control`, else the hub's
        port - 1), then waits for the app's live connection (none for "offline"). Without name / env: what runs."""
        if not control:
            if not self.hub:
                raise CtlError("scenario needs the mock hub (launch --hub) or --control")
            control = urllib.parse.urlparse(self.hub).port - 1
        q = {"name": name} if name else {"env": env} if env else {}
        url = f"http://127.0.0.1:{control}/scenario" + ("?" + urllib.parse.urlencode(q) if q else "")
        try:
            with urllib.request.urlopen(url, timeout=60) as r:
                out = json.load(r)
        except urllib.error.HTTPError as e:
            raise CtlError(f"scenario: {json.load(e).get('error')}")
        except OSError as e:
            raise CtlError(f"scenario: no scripts/mock/mock_scenarios.py on {control} ({e})")
        if q and out.get("scenario") != "offline":
            self.call("online", timeout=wait)
        return dict(out, ok=True)

    def online(self, timeout=None, control=None):
        """Waits for the hub's live connection. When the hub is scripts/mock/mock_scenarios.py's, also says whether it had to
        start the hub again since it began (`mockRestarts`: when, exit code, scenario): what a long run shot while the
        hub was down shows "Connecting...", so the script can take those again."""
        r = self.call("online", timeout=timeout)
        if self.hub or control:
            port = control or urllib.parse.urlparse(self.hub).port - 1
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{port}/scenario", timeout=2) as resp:
                    r["mockRestarts"] = json.load(resp).get("restarts", [])
            except (OSError, ValueError):
                pass  # a mock_hub.py on its own, or a real hub: nothing to ask
        return r

    def appearance(self, value, in_app=False):
        """Light or dark: the whole simulator (status bar included, as the screenshot scripts do) or the app only."""
        if in_app or self.via_hub:
            return self.call("set-appearance", value=value)
        sh("xcrun", "simctl", "ui", self.udid, "appearance", value)
        return {"ok": True, "appearance": value}

    def screenshot(self, path, settle=True, **settle_args):
        """`simctl io screenshot` (status bar included, as the identity shots), after the screen settles. Over the hub,
        the app's own `snapshot` (no status bar), JPEG when the path says so."""
        if settle:
            self.call("settle", **({"timeout": 4, "stable": 3, "pixels": 600} | settle_args))
        if self.via_hub:
            jpeg = path.lower().endswith((".jpg", ".jpeg"))
            r = self.call("snapshot", format="jpeg" if jpeg else None, scale=None if jpeg else 0)
            with open(path, "wb") as f:
                f.write(base64.b64decode(r["png"]))
            return path
        sh("xcrun", "simctl", "io", self.udid, "screenshot", path)
        return path


def filters(a):
    """The element filters of the command line, as the app's commands take them."""
    f = {"id": a.id, "idPrefix": a.id_prefix, "label": a.label, "labelContains": a.label_contains,
         "labelPrefix": a.label_prefix, "trait": a.trait, "index": a.index, "visibleOnly": a.visible or None,
         "onScreenOnly": a.on_screen or None, "allowHidden": a.allow_hidden or None, "containers": a.containers or None}
    return {k: v for k, v in f.items() if v is not None}


def ignored(a):
    """settle's --ignore-id / --ignore-label-contains / --ignore-rect, as the app's `ignore` and `ignoreRects`."""
    ignore = [{"idPrefix": p} for p in a.ignore_id] + [{"labelContains": t} for t in a.ignore_label_contains]
    rects = [[float(v) for v in r.split(",")] for r in a.ignore_rect]
    return {"ignore": ignore or None, "ignoreRects": rects or None}


def point(v):
    x, y = v.split(",")
    return float(x), float(y)


def value(v):
    """key=value from the command line: numbers and YES/NO as JSON, the rest as text."""
    if v in ("YES", "true"):
        return True
    if v in ("NO", "false"):
        return False
    try:
        return json.loads(v)
    except ValueError:
        return v


def main(argv):
    extra = []
    if "--" in argv:
        i = argv.index("--")
        argv, extra = argv[:i], argv[i + 1:]
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--udid")
    p.add_argument("--port", type=int)
    p.add_argument("--bundle")
    p.add_argument("--hub")
    p.add_argument("--via-hub")
    p.add_argument("--timeout", type=float)
    p.add_argument("--pixels", type=int)
    p.add_argument("--stable", type=int)
    p.add_argument("--no-settle", action="store_true")
    p.add_argument("--session")
    p.add_argument("--machine")
    p.add_argument("--color")
    p.add_argument("--flag", action="append", default=[])
    p.add_argument("--set", action="append", default=[])
    p.add_argument("--remove", action="append", default=[])
    p.add_argument("--get", action="append", default=[])
    p.add_argument("--keep", action="store_true")
    p.add_argument("--in-app", action="store_true")
    for f in ("--id", "--id-prefix", "--label", "--label-contains", "--label-prefix", "--trait"):
        p.add_argument(f)
    p.add_argument("--index", type=int)
    p.add_argument("--visible", action="store_true")
    p.add_argument("--on-screen", action="store_true")
    p.add_argument("--allow-hidden", action="store_true")
    p.add_argument("--containers", action="store_true")
    p.add_argument("--action")
    p.add_argument("--ignore-id", action="append", default=[])
    p.add_argument("--ignore-label-contains", action="append", default=[])
    p.add_argument("--ignore-rect", action="append", default=[])
    p.add_argument("--off", action="store_true")
    p.add_argument("--layers")
    p.add_argument("--keep-motion", action="store_true")
    p.add_argument("--gone", action="store_true")
    p.add_argument("--hid", action="store_true")
    p.add_argument("--duration", type=float)
    p.add_argument("--dir")
    p.add_argument("--distance", type=float)
    p.add_argument("--from", dest="start")
    p.add_argument("--seconds", type=float)
    p.add_argument("--text")
    p.add_argument("--button")
    p.add_argument("--pages", type=float)
    p.add_argument("--to")
    p.add_argument("--env")
    p.add_argument("--control", type=int)
    p.add_argument("--cold-chats", action="store_true")
    p.add_argument("--drop-chat-cache", action="store_true")
    p.add_argument("cmd")
    p.add_argument("rest", nargs="*")
    a = p.parse_args(argv)
    c = Ctl(a.udid, a.bundle, a.port, a.via_hub)
    if a.cmd == "launch":
        r = c.launch(a.hub, extra)
    elif a.cmd in ("ping", "state"):
        r = c.call(a.cmd)
    elif a.cmd == "settle":
        r = c.call("settle", timeout=a.timeout, pixels=a.pixels, stable=a.stable, **ignored(a))
    elif a.cmd == "freeze":
        r = c.call("freeze", on=not a.off, layers=a.layers, reduceMotion=False if a.keep_motion else None)
    elif a.cmd == "idle":
        r = c.idle(timeout=a.timeout or 10)
    elif a.cmd == "online":
        r = c.online(a.timeout, a.control)
    elif a.cmd == "screenshot":
        if not a.rest:
            p.error("screenshot needs a path")
        r = {"path": c.screenshot(a.rest[0], not a.no_settle)}
    elif a.cmd == "reset":
        r = c.call("reset", coldChats=a.cold_chats or None, dropChatCache=a.drop_chat_cache or None)
    elif a.cmd == "navigate":
        if not a.rest:
            p.error("navigate needs a scene")
        flags = {k: value(v) for k, v in (x.partition("=")[::2] for x in a.flag)}
        r = c.call("navigate", scene=a.rest[0], session=a.session, machine=a.machine, defaults=flags or None,
                   keep=a.keep or None, coldChats=a.cold_chats or None, dropChatCache=a.drop_chat_cache or None)
    elif a.cmd == "set-style":
        r = c.call("set-style", style=a.rest[0] if a.rest else None, color=a.color)
    elif a.cmd == "appearance":
        if not a.rest:
            p.error("appearance needs light or dark")
        r = c.appearance(a.rest[0], a.in_app)
    elif a.cmd == "defaults":
        sets = {k: value(v) for k, v in (x.partition("=")[::2] for x in a.set)}
        r = c.call("defaults", set=sets or None, remove=a.remove or None, get=a.get or None)
    elif a.cmd in ("tree", "find", "wait"):
        f = filters(a)
        if a.cmd == "wait":
            f.update(timeout=a.timeout, gone=a.gone or None)
        r = c.call(a.cmd, **f)
    elif a.cmd == "wait-loaded":
        r = c.call("wait-loaded", session=a.session, timeout=a.timeout)
    elif a.cmd == "scenario":
        r = c.scenario(a.rest[0] if a.rest else None, a.env, a.control)
    elif a.cmd == "tap":
        r = c.tap(a.hid, **filters(a), action=a.action)
    elif a.cmd == "longpress":
        r = c.longpress(a.duration or 1.2, **filters(a))
    elif a.cmd == "swipe":
        r = c.swipe(a.dir or "", a.distance, a.duration or 0.25, **filters(a))
    elif a.cmd == "drag":
        if not (a.start and a.to and "," in a.to):
            p.error("drag needs --from x,y --to x,y")
        r = c.drag(point(a.start), point(a.to), a.duration or 1.0)
    elif a.cmd == "background":
        r = c.background(a.seconds or 0)
    elif a.cmd == "foreground":
        r = c.foreground()
    elif a.cmd == "scroll":
        r = c.call("scroll", pages=a.pages, to=a.to)
    elif a.cmd == "type":
        r = c.type(a.text or "", **filters(a))
    elif a.cmd == "snapshot":
        if not a.rest:
            p.error("snapshot needs a path")
        r = {"path": c.screenshot(os.path.abspath(a.rest[0]), settle=False)} if c.via_hub else \
            c.call("snapshot", path=os.path.abspath(a.rest[0]))
    elif a.cmd == "alert":
        r = c.call("alert", button=a.button)
    elif a.cmd in ("perf", "refresh", "dismiss-keyboard"):
        r = c.call(a.cmd)
    elif a.cmd == "call":
        if not a.rest:
            p.error("call needs a command")
        kv = dict(x.partition("=")[::2] for x in a.rest[1:])
        r = c.call(a.rest[0], **{k: value(v) for k, v in kv.items()})
    else:
        p.error(f"unknown command {a.cmd}")
    print(json.dumps(r, ensure_ascii=False, indent=1 if a.cmd == "tree" else None))


if __name__ == "__main__":
    try:
        main(sys.argv[1:])
    except CtlError as e:
        print(f"personactl: {e}", file=sys.stderr)
        sys.exit(1)
    except OSError as e:
        print(f"personactl: cannot reach the app ({e}); launched with `personactl.py launch`?", file=sys.stderr)
        sys.exit(1)
