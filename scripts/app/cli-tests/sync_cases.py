#!/usr/bin/env python3
"""sync_cases: the shared chat sync cases (spec/fixtures/sync) played on screen in the iOS app, against the mock hub
playing each case (#2952, #1073 step 4).

    PORT=18760 MOCK_SYNC_CASE='sends.json#send_tied_by_echo' python3 scripts/mock/mock_hub.py &
    PERSONACTL_UDID=<udid> HUB=http://127.0.0.1:18760 scripts/app/cli-tests/sync_cases.py [case[@device] …]

The mock may start on any case: this loads each in turn. One device of a case is the app, paired as `mock-<device>`;
the case's other devices are scripted (`Device` in scripts/mock/test_sync_case_mock.py), and a two-device case runs
once for each device the app can be. Every run starts from nothing the app kept (its chats, outbox and drafts).

Each step: the mock plays the hub's side (`POST /mock/case/step`), this does the user's action on screen, waits the
step's `wait` in real time (SYNC_CASE_WAIT_CAP=<seconds> shortens longer ones), lets the screen settle and reads it
back as the case's view through the chat's marks (`chatMark`: `chat.user.*`, `chat.answer.*`, `chat.outbox.*`,
`chat.card.*`, `chat.live`). After every step the three rules hold, the step's `expect` matches, and once everything
settled the screen shows `end`. One PASS / FAIL / KNOWN line per run; a failed run leaves the screen in CLITEST_OUT
(clitest's `dump`); SYNC_CASE_TRACE=1 prints every step's view. Exits non-zero when a run failed or a known one passed.
"""
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(ROOT, "scripts", "mock"))
from clitest import PINNED, dump  # noqa: E402
from personactl import Ctl, CtlError  # noqa: E402
from test_sync_case_mock import Device  # noqa: E402

SPEC = os.path.join(ROOT, "spec", "fixtures", "sync")
TRACE = os.environ.get("SYNC_CASE_TRACE") == "1"  # every step's view, as read from the screen

# Cases that fail for a bug of their own, until it is fixed: "<case>" or "<case>@<device>" -> the issue, and what the
# failure must say (another failure is still a failure).
KNOWN = {}

ALLOW, DENY = ("Allow", "允许"), ("Deny", "拒绝")
# A failed bubble's menu items (its accessibility actions), and the alert a refusal for a draft on the computer raises.
RESEND, DELETE, NOT_NOW = ("Resend", "重发"), ("Delete", "删除"), ("Not Now", "先不发")


def mock(hub, path, body=None, timeout=90):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(hub.rstrip("/") + path, data=data, method="GET" if body is None else "POST",
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, json.load(r)
    except urllib.error.HTTPError as e:
        with e:
            return e.code, json.load(e)


def all_cases():
    out = []
    for f in sorted(os.listdir(SPEC)):
        if f.endswith(".json"):
            with open(os.path.join(SPEC, f)) as fh:
                out += [(f, c) for c in json.load(fh)["cases"]]
    return out


def words(s):
    return re.sub(r"\s+", " ", s or "").strip()


class App:
    """The app as one device of the case: launched, driven and read through the runtime CLI."""

    def __init__(self, ctl, hub, device, chat):
        self.c, self.hub, self.device, self.chat = ctl, hub, device, chat
        self.sent = []  # {"id", "text", "discarded"}
        self.ended, self.answering = set(), set()

    def args(self, fresh):
        a = PINNED + ["-app.tab", "assistant", "-tab", "assistant"]
        # a `new` chat starts on the new-conversation page; the session it starts opens there by itself
        if self.chat.get("new"):
            a += ["-newAssistant", "YES"]
        else:
            a += ["-openAssistant", "%s/%s" % (self.chat["machine_id"], self.chat["session_id"])]
        return a + (["-openChatsFresh", "YES"] if fresh else [])

    def forget(self):
        """Nothing the app kept: its chats, outbox, drafts and snapshot (Library/Caches/state, chats-*). It is not
        running (a new install would ask for notifications again: no `idb` to answer it here)."""
        subprocess.run(["xcrun", "simctl", "terminate", self.c.udid, self.c.bundle], capture_output=True)
        out = subprocess.run(["xcrun", "simctl", "get_app_container", self.c.udid, self.c.bundle, "data"],
                             capture_output=True, text=True)
        caches = os.path.join(out.stdout.strip(), "Library", "Caches")
        if out.returncode == 0 and os.path.isdir(caches):
            for n in os.listdir(caches):
                if n == "state" or n.startswith("chats-"):
                    shutil.rmtree(os.path.join(caches, n), ignore_errors=True)

    def launch(self):
        self.forget()
        self.c.launch(self.hub, extra=self.args(fresh=True), pair="mock-" + self.device)
        self.in_front()

    def relaunch(self):
        """Killed and started again from what it stored, paired as it was."""
        self.c.launch(self.hub, extra=self.args(fresh=False), pair="")
        self.in_front()

    def in_front(self):
        """Active: a system alert over it (a permission the app asked for) leaves it inactive, with no socket, in every
        run after, and nothing here can answer it."""
        for _ in range(20):
            if self.c.call("state").get("appState") == "active":
                return
            time.sleep(0.25)
        raise CtlError("start: the app is not active: a system alert covers it? (look with `xcrun simctl io <udid> "
                       "screenshot`; reboot the simulator to clear it)")

    def settle(self):
        try:
            self.c.call("settle", timeout=3, stable=2, pixels=600)
        except CtlError:
            pass

    def marked(self, prefix="chat."):
        """The chat's marks, top to bottom, each with the elements it holds (by the tree, not by where they are drawn:
        an alert over a bubble is not in it). Rows scrolled out of sight count: the chat's list keeps them."""
        els = self.c.call("tree", containers=True)["elements"]
        marks = sorted((e for e in els if e.get("container") and e["id"].startswith(prefix)), key=lambda e: e["frame"][1])
        return [(m, [e for e in els if not e.get("container") and e.get("in") == m["id"]]) for m in marks]

    def look(self):
        """What the chat shows, as a case's view."""
        view = {"messages": [], "cards": {}, "live": None}
        for m, inner in self.marked():
            parts = m["id"].split(".")
            text = words(" ".join(e["label"] for e in inner if "text" in e["traits"] and "button" not in e["traits"]))
            if parts[1] == "user":
                view["messages"].append({"from": "user", "text": text, "state": "shown"})
            elif parts[1] == "answer":
                view["messages"].append({"from": "agent", "text": text})
            elif parts[1] == "outbox":
                view["messages"].append({"from": "user", "text": text, "state": "failed" if parts[2] == "failed" else "pending"})
            elif parts[1] == "card":
                buttons = [e for e in inner if "button" in e["traits"]]
                view["cards"][".".join(parts[2:])] = "open" if any("disabled" not in b["traits"] for b in buttons) else "answering"
            elif parts[1] == "live":
                view["live"] = words(text.rstrip("▍")) or None
        return view

    # ── what the user does ──

    def send(self, text):
        self.c.call("type", text=text, id="shell-composer-field")
        # the button comes in once there is text: wait until a touch reaches it and it stopped moving
        self.c.call("wait", id="shell-composer-send", visibleOnly=True, stable=True, timeout=5)
        self.c.call("activate", id="shell-composer-send")
        self.c.call("dismiss-keyboard")

    def nth(self, e, **filters):
        """`e`'s index among the elements `filters` match (what `activate` takes as `index`)."""
        for i, x in enumerate(self.c.call("tree", **filters)["elements"]):
            if x["frame"] == e["frame"]:
                return i
        raise CtlError("%s is gone" % json.dumps(filters, ensure_ascii=False))

    def close_alert(self):
        """A refusal for a draft on the computer asks first (Clear and Send / Not Now): the user closes it to use the
        bubble's own Resend. Its buttons take no accessibility activation: the runtime CLI's `alert` presses one."""
        try:
            shown = self.c.call("alert")
        except CtlError:  # no alert
            return
        label = next((b for b in NOT_NOW if b in shown["buttons"]), None)
        if not label:
            raise CtlError("an alert %r without %s: %s" % (shown["title"], NOT_NOW[0], shown["buttons"]))
        self.c.call("alert", button=label)

    def on_failed(self, text, names):
        """Resend or Delete (`names`: the item's label in each language) on the failed bubble with these words: the
        items of its menu, done as its accessibility actions (no touch reaches a menu's items without idb)."""
        self.close_alert()
        for m, inner in self.marked("chat.outbox.failed."):
            if words(text) not in words(" ".join(e["label"] for e in inner)):
                continue
            menu = next((e for e in inner if e.get("actions")), None)
            name = menu and next((n for n in names if n in menu["actions"]), None)
            if not name:
                raise CtlError("the failed bubble %r has no %s: %s" % (text, names[0], menu and menu["actions"]))
            i = self.nth(menu, label=menu["label"])
            self.c.call("activate", label=menu["label"], index=i, action=name)
            return
        raise CtlError("no failed bubble %r on screen" % text)

    def answer(self, ix, allow):
        card = [inner for m, inner in self.marked("chat.card.") if m["id"] == "chat.card." + ix]
        if not card:
            raise CtlError("no card %s on screen" % ix)
        for label in (ALLOW if allow else DENY):
            button = next((e for e in card[0] if e["label"] == label and "button" in e["traits"]), None)
            if button:
                self.c.call("activate", label=label, trait="button", index=self.nth(button, label=label, trait="button"))
                return
        raise CtlError("no %s on card %s" % ("Allow" if allow else "Deny", ix))


def scripted(d, user, base):
    """A scripted device's user action: its requests as an app makes them (the mock holds the writes)."""
    if user.get("send"):
        m = user["send"]
        d.write(m["id"], "messages", "POST", base + "/messages", {"text": m["text"], "client_msg_id": m["id"]}, m["id"])
    elif user.get("answer"):
        a = user["answer"]
        d.write(a["id"], "resolve", "POST", "/api/v1/interactions/%s/resolve" % a["id"],
                {"resolution": a["resolution"]}, "op-" + a["id"])
    elif user.get("retry"):
        k = user["retry"]
        d.write(k, d.kinds.get(k, "messages"), "POST", base + "/messages", {"client_msg_id": k}, d.ops.get(k, k))
    elif user.get("relaunch"):
        d.close()
        d.reconnect()
    else:
        raise ValueError("a scripted device does not do %s" % json.dumps(user))


def differences(got, want):
    diffs = []
    if "messages" in want:
        listed = lambda ms: ["%s:%s:%s" % (m["from"], m.get("state") or "", m["text"]) for m in ms]  # noqa: E731
        if listed(got["messages"]) != listed(want["messages"]):
            diffs.append("messages %s, want %s" % (json.dumps(listed(got["messages"]), ensure_ascii=False),
                                                   json.dumps(listed(want["messages"]), ensure_ascii=False)))
    if "cards" in want:
        opened = lambda cs: sorted(k for k, v in cs.items() if v == "open")  # noqa: E731
        if opened(got["cards"]) != opened(want["cards"]):
            diffs.append("open cards %s, want %s" % (opened(got["cards"]), opened(want["cards"])))
    if "live" in want and got["live"] != want["live"]:
        diffs.append("live %r, want %r" % (got["live"], want["live"]))
    return diffs


def match_requests(want, seen, before):
    """The requests since the last check, matched in order against the case's (others may come between)."""
    at = 0
    for w in want:
        k = next((i for i in range(at, len(seen)) if req_is(w, seen[i], before)), -1)
        if k < 0:
            return "no %s in %s" % (json.dumps(w), json.dumps(seen[at:]))
        at = k + 1
    return None


def req_is(w, s, before):
    # one_of: any one of these, where the protocol allows several ways (#2770); an op's receipt goes by the write's id
    if "one_of" in w:
        return any(req_is(o, s, before) for o in w["one_of"])
    if w.get("get") == "/ops":
        return s.get("get") == "/ops" and s.get("key") == w.get("id")
    if "get" in w:
        return s.get("get") == w["get"] and ("after_seq" not in w or s.get("after_seq") == w["after_seq"])
    if "ws" in w:
        return s.get("ws") == w["ws"] and ("since_seq" not in w or s.get("since_seq") == w["since_seq"])
    if w.get("post") != s.get("post"):
        return False
    if w.get("new_op"):
        return s.get("op") is not None and s["op"] not in before
    return s.get("key") == w.get("id")


def play(c, hub, file, case, me, out):
    """One run of `case` with the app as device `me`: the failures, in order, and where the screen was saved when
    there were some (`out`, before the app goes)."""
    failures, trace, saved = [], "", None
    # its times moved to now: the simulator runs on the real clock (the web's runner pins its clock to the case's)
    status, loaded = mock(hub, "/mock/case/load", {"case": "%s#%s" % (file, case["name"]), "now": True})
    if status != 200:
        return ["load: %s" % loaded], None
    chat = case["chat"]
    base = "/api/v1/sessions/%s/%s" % (chat["machine_id"], chat["session_id"])
    port = urllib.parse.urlparse(hub).port
    app = App(c, hub, me, chat)
    others = {n: Device(port, n) for n in case["devices"] if n != me}
    cap = float(os.environ["SYNC_CASE_WAIT_CAP"]) if os.environ.get("SYNC_CASE_WAIT_CAP") else None
    try:
        for d in others.values():
            d.connect()
        app.launch()
        # The case starts with every device on the chat, loaded the normal way: the app subscribed. Its requests count
        # from here.
        checked = 0
        for i in range(60):
            seen = mock(hub, "/mock/case")[1]["devices"][me]
            if chat.get("new") or (seen["connected"] and any(s.get("ws") == "subscribe" for s in seen["seen"])):
                checked = len(seen["seen"])
                break
            if i == 59:
                raise CtlError("start: the app did not subscribe to the chat: %s" % json.dumps(seen))
            time.sleep(0.5)
        try:
            if chat.get("new"):
                c.call("wait", id="shell-composer-field", timeout=20)
            else:
                c.call("wait-loaded", _timeout=20, session="%s/%s" % (chat["machine_id"], chat["session_id"]))
        except CtlError as e:
            failures.append("start: %s" % e)
        app.settle()

        def rules(where):
            v = app.look()
            for s in app.sent:
                if s.get("discarded"):
                    continue
                n = sum(1 for m in v["messages"] if m["from"] == "user" and m["text"] == s["text"])
                if n != 1:
                    failures.append("%s: sent message %s shows %d times" % (where, s["id"], n))
            for ix, state in v["cards"].items():
                if state == "open" and (ix in app.ended or ix in app.answering):
                    failures.append("%s: card %s is answerable again" % (where, ix))
            return v

        for i, st in enumerate(case["steps"]):
            where = "step %d (%s)" % (i + 1, "+".join(k for k in st if k != "expect"))
            status, got = mock(hub, "/mock/case/step", {})
            if status != 200:
                failures.append("%s: the mock: %s" % (where, got.get("error", got)))
                break
            sock = st.get("socket") or {}
            if sock.get("device") == me and (sock.get("open") or {}).get("resumed") is False:
                for ix in list(app.answering):
                    if not any((f.get("interaction") or {}).get("id") == ix for f in (st.get("frames") or {}).get(me, [])):
                        app.ended.add(ix)
            for f in (st.get("frames") or {}).get(me, []):
                if f.get("type") == "interaction_resolved":
                    app.ended.add(str(f.get("id")))
            u = got.get("user")
            if u and u["device"] != me:
                try:
                    scripted(others[u["device"]], u, base)
                except ValueError as e:
                    failures.append("%s: %s" % (where, e))
            elif u:
                try:
                    if u.get("send") or u.get("start"):
                        m = u.get("send") or u["start"]
                        app.sent.append({"id": m["id"], "text": m["text"]})
                        app.send(m["text"])
                    elif u.get("retry"):
                        app.on_failed(next(s["text"] for s in app.sent if s["id"] == u["retry"]), RESEND)
                    elif u.get("discard"):
                        s = next(s for s in app.sent if s["id"] == u["discard"])
                        s["discarded"] = True
                        app.on_failed(s["text"], DELETE)
                    elif u.get("answer"):
                        app.answering.add(u["answer"]["id"])
                        app.answer(u["answer"]["id"], u["answer"]["resolution"].get("allow", True) is not False)
                    elif u.get("relaunch"):
                        app.relaunch()
                    else:
                        failures.append("%s: %s is not something this runner does yet" % (where, json.dumps(u)))
                except CtlError as e:
                    failures.append("%s: the app could not do %s: %s" % (where, json.dumps(u), e))
            r = st.get("reply")
            if r and r["device"] == me and r["write"] in app.answering and not r.get("cut"):
                app.answering.discard(r["write"])
                if r.get("status", 200) < 300 or r.get("status") == 410:
                    app.ended.add(r["write"])
            wait = (st.get("wait") or 100) / 1000
            time.sleep(min(wait, cap) if cap else wait)
            app.settle()
            v = rules(where)
            if TRACE:
                print("  %s: %s" % (where, json.dumps(v, ensure_ascii=False)), flush=True)
            want = (st.get("expect") or {}).get(me)
            if want:
                failures += ["%s: %s" % (where, d) for d in differences(v, want)]
                if want.get("requests"):
                    seen = mock(hub, "/mock/case")[1]["devices"][me]["seen"]
                    before = {s["op"] for s in seen[:checked] if s.get("op")}
                    miss = match_requests(want["requests"], seen[checked:], before)
                    if miss:
                        failures.append("%s: requests: %s" % (where, miss))
                    checked = len(seen)
        status, got = mock(hub, "/mock/case/step", {})
        if not got.get("done"):
            failures.append("end: the mock is not done: %s" % got)
        # Settled: the screen must come to show `end` (the app's own timers run in real time).
        want, deadline, diffs = case["end"][me], time.time() + 15, []
        while True:
            app.settle()
            diffs = differences(app.look(), want)
            if not diffs or time.time() > deadline:
                break
            time.sleep(1)
        rules("end")
        failures += ["end: %s" % d for d in diffs]
    except CtlError as e:  # the app could not go on
        failures.append(str(e))
    except Exception as e:  # a bug here, or the app gone: the next run goes on
        failures.append("the runner: %s" % (str(e) or repr(e)))
        trace = traceback.format_exc()
    finally:
        if failures:
            saved = dump(c, out, "\n".join(failures), trace)
        for d in others.values():
            d.stopped = True
            d.close()
        # an app left running would connect to the next case as its device and take that device's socket
        subprocess.run(["xcrun", "simctl", "terminate", c.udid, c.bundle], capture_output=True)
    return failures, saved


def main(argv):
    hub = os.environ.get("HUB")
    if not hub:
        sys.exit("HUB=<the mock hub playing a case> (MOCK_SYNC_CASE)")
    status, _ = mock(hub, "/mock/case")
    if status != 200:
        sys.exit("the mock hub needs MOCK_SYNC_CASE (any case: this loads each in turn)")
    only = set(argv)
    c = Ctl()
    dumps = os.path.join(os.environ.get("CLITEST_OUT") or os.path.join(tempfile.gettempdir(), "clitest"),
                         time.strftime("%Y%m%d-%H%M%S"))
    t0, bad, rows = time.time(), 0, []
    for file, case in all_cases():
        for me in case["devices"]:
            run = case["name"] if len(case["devices"]) == 1 else "%s@%s" % (case["name"], me)
            if only and run not in only and case["name"] not in only:
                continue
            s = time.time()
            failures, saved = play(c, hub, file, case, me, os.path.join(dumps, run))
            known = KNOWN.get(run) or KNOWN.get(case["name"])
            if known and failures and all(re.search(known["because"], f) for f in failures):
                verdict, why = "KNOWN", "%s: %s" % (known["issue"], failures[0])
            elif known and not failures:
                verdict, why, bad = "FAIL", "known under %s, and it passed: take it off KNOWN" % known["issue"], bad + 1
            elif failures:
                verdict, why, bad = "FAIL", failures[0], bad + 1
                why += "  (%d failures; %s)" % (len(failures), saved)
            else:
                verdict, why = "PASS", ""
            rows.append((run, verdict))
            print("%s\t%s\t%.1f s\t%s" % (run, verdict, time.time() - s, why), flush=True)
    n = {v: sum(1 for _, x in rows if x == v) for v in ("PASS", "KNOWN", "FAIL")}
    print("SUMMARY sync cases on screen: %d passed, %d known, %d failed of %d runs, %.0f s"
          % (n["PASS"], n["KNOWN"], n["FAIL"], len(rows), time.time() - t0))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
