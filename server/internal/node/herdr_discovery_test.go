package node

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Characterization tests of how the node discovers sessions in herdr: which sessions a herdr state lists, and the
// herdr calls it takes to find them. They pin today's behaviour ahead of the terminal-host abstraction, so they go
// through the node's requests (herdrfake_test.go has the few adapters that reach inside).

const (
	sidA = "0d000000-0000-4000-8000-00000000000a"
	sidB = "0d000000-0000-4000-8000-00000000000b"
	sidC = "0d000000-0000-4000-8000-00000000000c"

	// a compatible CLI thread ids (placeholders).
	thread1 = "0f000000-0000-4000-8000-000000000001"
	thread2 = "0f000000-0000-4000-8000-000000000002"
	thread3 = "0f000000-0000-4000-8000-000000000003"
)

func byID(list []proto.Session) map[string]proto.Session {
	m := map[string]proto.Session{}
	for _, s := range list {
		m[s.SessionID] = s
	}
	return m
}

func ids(list []proto.Session) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.SessionID)
	}
	sort.Strings(out)
	return out
}

func sorted(trace []string) []string {
	out := append([]string(nil), trace...)
	sort.Strings(out)
	return out
}

// writeTranscript writes ~/.claude/projects/<cwd as claude names it>/<id>.jsonl with lines, modified at mtime.
func writeTranscript(t *testing.T, home, cwd, id string, mtime time.Time, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", nonAlnum.ReplaceAllString(cwd, "-"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeTraexOwner writes the thread-writer lock naming the pid that writes thread.
func writeTraexOwner(t *testing.T, home, thread string, pid int) {
	t.Helper()
	dir := filepath.Join(home, ".trae", "cli", "thread-writer-locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"pid": pid})
	if err := os.WriteFile(filepath.Join(dir, thread+".owner.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func processInfo(pid int) map[string]any {
	return map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": pid, "name": "claude", "argv0": "claude"}}}}
}

// One snapshot: claude agents become sessions placed in their workspace (space) and tab (slot); agents of other
// kinds are not listed; a tab the snapshot does not name leaves the slot's name empty.
func TestHerdrListPlacesAgentsInSpacesAndSlots(t *testing.T) {
	n, f, _ := herdrNode(t)
	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
		[]any{
			map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "agent-1", "number": 1},
			map[string]any{"tab_id": "w1:t2", "workspace_id": "w1", "label": "review", "number": 2},
		},
		[]any{
			claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", "/tmp/repo", "fix the build"),
			claudeAgent(sidB, proto.StatusIdle, "w1:p2", "w1:t9", "w1", "/tmp/repo", "Claude Code"),
			agentOf("gemini", sidC, proto.StatusIdle, "w1:p3", "w1:t2", "w1", "/tmp/repo", ""), // herdr knows it, Persona cannot read it: terminal only (opencode.go)
		},
		nil))

	check := func(list []proto.Session) {
		t.Helper()
		if got := ids(list); strings.Join(got, ",") != sidA+","+sidB+",gemini-w1-p3" {
			t.Fatalf("listed %v", got)
		}
		m := byID(list)
		if g := m["gemini-w1-p3"]; g.Agent != "gemini" || g.PaneID != "w1:p3" || g.SlotID != "w1:t2" || g.Status != proto.StatusIdle || g.ReadOnlyReason == "" {
			t.Errorf("gemini = %+v", g)
		}
		a := m[sidA]
		if a.SpaceKind != "herdr" || a.SpaceID != "w1" || a.SpaceName != "repo" || a.SpaceIndex != 1 ||
			a.SlotID != "w1:t1" || a.SlotName != "agent-1" || a.SlotIndex != 1 {
			t.Errorf("A placed in %+v", a)
		}
		if a.Agent != "claude" || a.Status != proto.StatusWorking || a.PaneID != "w1:p1" || a.TabID != "w1:t1" ||
			a.WorkspaceID != "w1" || a.Title != "fix the build" || a.Cwd != "/tmp/repo" || a.StartedByPersona {
			t.Errorf("A = %+v", a)
		}
		b := m[sidB]
		// Not in the snapshot's tabs: slot id from the agent, no name / index. Title "Claude Code" stays without a
		// transcript (untitled only runs when there is one).
		if b.Agent != "claude" || b.SlotID != "w1:t9" || b.SlotName != "" || b.SlotIndex != 0 || b.SpaceName != "repo" ||
			b.Status != proto.StatusIdle || b.Title != "Claude Code" {
			t.Errorf("B = %+v", b)
		}
	}
	check(listSessions(t, n))
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})

	// The list pushes carry is built from what the last snapshot said: the same sessions, no herdr call.
	f.reset()
	check(cachedSessions(n))
	assertTrace(t, f.trace(), nil)
}

// The snapshot's layouts place each agent's pane in its tab: pane_index is its place, top left first; a pane the
// layouts leave out has none.
func TestHerdrListGivesPaneIndexFromLayouts(t *testing.T) {
	n, f, _ := herdrNode(t)
	snap := snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
		[]any{
			map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "split", "number": 1},
			map[string]any{"tab_id": "w1:t2", "workspace_id": "w1", "label": "solo", "number": 2},
		},
		[]any{
			claudeAgent(sidB, proto.StatusWorking, "w1:p2E", "w1:t1", "w1", "/tmp/repo", "branch"),
			claudeAgent(sidA, proto.StatusIdle, "w1:pW", "w1:t1", "w1", "/tmp/repo", "launcher"),
			claudeAgent(sidC, proto.StatusIdle, "w1:pX", "w1:t2", "w1", "/tmp/repo", "missing from layouts"),
		},
		nil)
	rect := func(x, y, w, h int) map[string]any { return map[string]any{"x": x, "y": y, "width": w, "height": h} }
	snap["snapshot"].(map[string]any)["layouts"] = []any{map[string]any{
		"tab_id": "w1:t1", "workspace_id": "w1", "focused_pane_id": "w1:p2E", "zoomed": false, "splits": []any{},
		"panes": []any{
			map[string]any{"pane_id": "w1:p2E", "focused": true, "rect": rect(93, 0, 92, 55)},
			map[string]any{"pane_id": "w1:pW", "focused": false, "rect": rect(0, 0, 93, 55)},
		}}}
	f.reply("session.snapshot", snap)
	m := byID(listSessions(t, n))
	idx := func(s proto.Session) string {
		if s.PaneIndex == nil {
			return "none"
		}
		return fmt.Sprint(*s.PaneIndex)
	}
	if got := idx(m[sidA]) + "," + idx(m[sidB]) + "," + idx(m[sidC]); got != "0,1,none" {
		t.Fatalf("pane indexes %s", got)
	}
	b, _ := json.Marshal(m[sidA])
	if !strings.Contains(string(b), `"pane_index":0`) {
		t.Fatalf("the first pane's index is sent: %s", b)
	}
	if b, _ := json.Marshal(m[sidC]); strings.Contains(string(b), "pane_index") {
		t.Fatalf("no index when unknown: %s", b)
	}
}

// An older herdr without session.snapshot: the agent list is used instead. Space / slot names stay what the last
// good snapshot said (even for an agent that moved), and a compatible CLI panes, found only in a snapshot, are not listed.
func TestHerdrListFallsBackToAgentListWithoutSnapshot(t *testing.T) {
	n, f, home := herdrNode(t)
	writeTraexOwner(t, home, thread1, os.Getpid())
	seedTraexPane(os.Getpid(), "w1:p9")
	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
		[]any{
			map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "agent-1", "number": 1},
			map[string]any{"tab_id": "w1:t2", "workspace_id": "w1", "label": "review", "number": 2},
		},
		[]any{claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", "/tmp/repo", "t")},
		[]any{map[string]any{"pane_id": "w1:p9", "tab_id": "w1:t2", "workspace_id": "w1", "cwd": "/tmp/repo",
			"terminal_title": "⠸ build"}}))
	if got := ids(listSessions(t, n)); strings.Join(got, ",") != sidA+","+thread1 {
		t.Fatalf("with a snapshot: %v", got)
	}

	f.reset()
	f.reply("session.snapshot", herdrFail{Code: "internal", Message: "no snapshot"})
	// (Still working: a turn ending would start reading the screen for a suggestion.)
	// The agent moved to tab 2 and to a workspace the last snapshot did not have.
	f.reply("agent.list", map[string]any{"agents": []any{
		claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t2", "w1", "/tmp/repo", "t"),
		claudeAgent(sidB, proto.StatusIdle, "w8:p1", "w8:t1", "w8", "/tmp/repo", "t"),
	}})
	list := listSessions(t, n)
	if got := ids(list); strings.Join(got, ",") != sidA+","+sidB {
		t.Fatalf("listed %v", got)
	}
	a, b := byID(list)[sidA], byID(list)[sidB]
	if a.Status != proto.StatusWorking || a.SpaceName != "repo" || a.SlotID != "w1:t2" || a.SlotName != "review" || a.SlotIndex != 2 {
		t.Errorf("A = %+v", a)
	}
	if b.SpaceKind != "herdr" || b.SpaceID != "w8" || b.SpaceName != "" || b.SlotName != "" {
		t.Errorf("B = %+v", b)
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil), call("agent.list", nil)})

	// Both fail: the list keeps the agents it had.
	f.reset()
	f.reply("agent.list", herdrFail{Code: "internal", Message: "down"})
	if got := ids(listSessions(t, n)); strings.Join(got, ",") != sidA+","+sidB {
		t.Fatalf("after a failed refresh: %v", got)
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil), call("agent.list", nil)})
}

// A claude herdr cannot name (no integration): its process (sessions/<pid>.json) first, then the pane's binding,
// then a guess — a transcript in its directory whose title is the terminal title, else the newest one nobody has.
func TestHerdrUnnamedClaudeIsGuessed(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := "/tmp/proj"
	old := time.Now().Add(-time.Hour)
	writeTranscript(t, home, cwd, sidA, old, `{"type":"custom-title","customTitle":"fix the flaky test","sessionId":"`+sidA+`"}`)
	writeTranscript(t, home, cwd, sidB, time.Now().Add(-time.Minute), `{"type":"custom-title","customTitle":"other","sessionId":"`+sidB+`"}`)
	writeTranscript(t, home, cwd, sidC, old.Add(-time.Hour), `{"type":"custom-title","customTitle":"bound","sessionId":"`+sidC+`"}`)
	bindPane(n, "w1:p3", sidC) // an earlier start / hook

	f.reply("pane.process_info", processInfo(1<<30)) // no sessions/<pid>.json for it
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		claudeAgent("", proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "fix the flaky test"), // title match: A
		claudeAgent("", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "something else"),     // newest unclaimed: B
		claudeAgent("", proto.StatusIdle, "w1:p3", "w1:t3", "w1", cwd, "fix the flaky test"), // its binding: C
		claudeAgent("", proto.StatusIdle, "w1:p4", "w1:t4", "w1", "", ""),                    // no cwd: nothing
	}, nil))
	list := listSessions(t, n)
	want := map[string]string{sidA: "w1:p1", sidB: "w1:p2", sidC: "w1:p3"}
	if len(list) != len(want) {
		t.Fatalf("listed %v", ids(list))
	}
	for _, s := range list {
		if want[s.SessionID] != s.PaneID {
			t.Errorf("%s in %q, want %s", s.SessionID, s.PaneID, want[s.SessionID])
		}
	}
	assertTrace(t, f.trace(), []string{
		call("session.snapshot", nil),
		call("pane.process_info", map[string]any{"pane_id": "w1:p1"}),
		call("pane.process_info", map[string]any{"pane_id": "w1:p2"}),
		call("pane.process_info", map[string]any{"pane_id": "w1:p3"}),
		call("pane.process_info", map[string]any{"pane_id": "w1:p4"}),
	})

	// The pid not found is remembered for panePIDRetry: the next snapshot asks herdr for no process.
	f.reset()
	listSessions(t, n)
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})
}

// What the pane's claude process says (its sessions/<pid>.json) beats the pane's stored binding; a binding to a
// session another pane already has falls through to the guess.
func TestHerdrProcessNamesTheSessionOverTheBinding(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := "/tmp/proc"
	writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour), `{"type":"custom-title","customTitle":"a"}`)
	writeTranscript(t, home, cwd, sidC, time.Now().Add(-time.Minute), `{"type":"custom-title","customTitle":"c"}`)
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "4242.json"), []byte(`{"pid":4242,"sessionId":"`+sidB+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bindPane(n, "w1:p1", sidA) // what w1:p1 ran days ago
	bindPane(n, "w1:p2", sidB) // claimed by w1:p1's process below
	f.handle("pane.process_info", func(p map[string]any) any {
		if p["pane_id"] == "w1:p1" {
			return processInfo(4242)
		}
		return processInfo(4343) // no file
	})
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		claudeAgent("", proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, ""),
		claudeAgent("", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, ""),
	}, nil))
	list := listSessions(t, n)
	m := byID(list)
	if len(list) != 2 || m[sidB].PaneID != "w1:p1" || m[sidC].PaneID != "w1:p2" {
		t.Fatalf("listed %+v", list)
	}
	assertTrace(t, f.trace(), []string{
		call("session.snapshot", nil),
		call("pane.process_info", map[string]any{"pane_id": "w1:p1"}),
		call("pane.process_info", map[string]any{"pane_id": "w1:p2"}),
	})
}

// a compatible CLI panes (herdr sees no agent there) are found by the thread lock files and the pane in their process's
// environment; their status comes from the title's spinner without reading the screen. A pane herdr already reports
// as an agent is not taken for a compatible CLI one.
func TestHerdrTraexPanesAreMergedIntoTheList(t *testing.T) {
	n, f, home := herdrNode(t)
	writeTraexOwner(t, home, thread1, os.Getpid())
	writeTraexOwner(t, home, thread2, os.Getppid())
	seedTraexPane(os.Getpid(), "w3:p2")  // what HERDR_PANE_ID in its environment says
	seedTraexPane(os.Getppid(), "w3:p1") // a pane herdr reports as a claude agent

	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w3", "label": "tools", "number": 3}},
		[]any{map[string]any{"tab_id": "w3:t2", "workspace_id": "w3", "label": "traex", "number": 2}},
		[]any{
			claudeAgent(sidA, proto.StatusIdle, "w3:p1", "w3:t1", "w3", "/tmp/tools", "claude here"),
			// PaneReportResume's fallback claims the pane as traex without a session id (#4657): traexAgents still
			// supplies its session id and screen/title status instead of dropping the pane.
			agentOf(traexAgent, "", proto.StatusIdle, "w3:p2", "w3:t2", "w3", "/tmp/tools/sub", "build the thing"),
		},
		[]any{
			map[string]any{"pane_id": "w3:p1", "tab_id": "w3:t1", "workspace_id": "w3", "cwd": "/tmp/tools"},
			map[string]any{"pane_id": "w3:p2", "tab_id": "w3:t2", "workspace_id": "w3", "cwd": "/tmp/tools/sub",
				"terminal_title": "⠸ build the thing", "terminal_title_stripped": "build the thing"},
		}))
	list := listSessions(t, n)
	m := byID(list)
	if len(list) != 2 {
		t.Fatalf("listed %v", ids(list))
	}
	tr, ok := m[thread1]
	if !ok {
		t.Fatalf("no TRAEX session: %v", ids(list))
	}
	// No rollout: the title falls back to the directory's name (decorate).
	if tr.Agent != traexAgent || tr.Status != proto.StatusWorking || tr.PaneID != "w3:p2" || tr.SpaceName != "tools" ||
		tr.SlotName != "traex" || tr.SlotIndex != 2 || tr.Title != "sub" || tr.Cwd != "/tmp/tools/sub" {
		t.Errorf("TRAEX session %+v", tr)
	}
	if _, ok := m[thread2]; ok {
		t.Error("a claude pane was taken for TRAEX")
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})
}

// TestTraexOwnerHelper is the child TestHerdrTraexStatusFromScreen names as a compatible CLI owner: it waits until killed.
func TestTraexOwnerHelper(t *testing.T) {
	if os.Getenv("PERSONA_TRAEX_OWNER_HELPER") == "" {
		t.Skip("helper process only")
	}
	time.Sleep(time.Minute)
}

// Without a spinner a compatible CLI pane's status is read off its screen (at most every 2 s per pane): "esc to interrupt"
// is working, a numbered choice blocked, anything else a finished turn; a failed read keeps the last status, or is
// unknown without one.
func TestHerdrTraexStatusFromScreen(t *testing.T) {
	n, f, home := herdrNode(t)
	// Three of this user's processes, all alive: this one, its parent, and a child it runs. Not init: a pid file this
	// user's a compatible CLI wrote that another user's process now has is a reused pid, not a compatible CLI (#2141).
	child := exec.Command(os.Args[0], "-test.run=^TestTraexOwnerHelper$")
	child.Env = append(os.Environ(), "PERSONA_TRAEX_OWNER_HELPER=1")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	third := child.Process.Pid
	pids := map[int]string{os.Getpid(): thread1, os.Getppid(): thread2, third: thread3}
	panes := map[int]string{os.Getpid(): "w4:p1", os.Getppid(): "w4:p2", third: "w4:p3"}
	for pid, th := range pids {
		writeTraexOwner(t, home, th, pid)
		seedTraexPane(pid, panes[pid])
	}
	pane := func(id string) map[string]any {
		return map[string]any{"pane_id": id, "tab_id": "w4:t1", "workspace_id": "w4", "cwd": "/tmp/t", "terminal_title": "traex | /tmp/t"}
	}
	f.reply("session.snapshot", snapshot(nil, nil, nil, []any{pane("w4:p1"), pane("w4:p2"), pane("w4:p3"), pane("w4:p4")}))
	f.setScreen("w4:p1", "• Running tests (12s • esc to interrupt)\n")
	f.setScreen("w4:p2", "Would you like to run the following command?\n\n› 1. Yes, proceed (y)\n  2. No (esc)\n")
	f.setScreen("w4:p3", "◆ Done.\n\n❯ Run /review on my current changes\n")
	read := func(p string) string {
		return call("pane.read", map[string]any{"pane_id": p, "source": "visible", "lines": 40})
	}
	status := func() map[string]string {
		out := map[string]string{}
		for _, s := range listSessions(t, n) {
			out[s.PaneID] = s.Status
		}
		return out
	}
	if got := status(); got["w4:p1"] != proto.StatusWorking || got["w4:p2"] != proto.StatusBlocked || got["w4:p3"] != proto.StatusDone {
		t.Fatalf("statuses %v", got)
	}
	assertTrace(t, sorted(f.trace()), []string{read("w4:p1"), read("w4:p2"), read("w4:p3"), call("session.snapshot", nil)})

	// Within 2 s the statuses are remembered: no read.
	f.reset()
	status()
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})

	// Past 2 s: read again, once each. A failed read keeps the last status; a pane never read is unknown.
	ageTraexStatus()
	seedTraexPane(third, "w4:p4")
	f.handle("pane.read", func(p map[string]any) any {
		if p["pane_id"] == "w4:p1" || p["pane_id"] == "w4:p4" {
			return herdrFail{Code: "pane_not_found", Message: "gone"}
		}
		return map[string]any{"read": map[string]any{"text": "› 1. Yes\n"}}
	})
	f.reset()
	if got := status(); got["w4:p1"] != proto.StatusWorking || got["w4:p2"] != proto.StatusBlocked || got["w4:p4"] != proto.StatusUnknown {
		t.Fatalf("after failed reads %v", got)
	}
	assertTrace(t, sorted(f.trace()), []string{read("w4:p1"), read("w4:p2"), read("w4:p4"), call("session.snapshot", nil)})
}

// The poll loop: one snapshot per tick (no event stream), a sessions.changed with the whole list when something it
// shows changed, and only sessions.revs when just a transcript grew.
func TestHerdrPollPushesChangedThenRevs(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := pollRepo(t)
	cwdJSON, _ := json.Marshal(cwd)
	p := writeTranscript(t, home, cwd, sidA, time.Now(),
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":`+string(cwdJSON)+`,"message":{"role":"user","content":"hello"}}`)
	f.reply("session.snapshot", snapshot(nil, nil,
		[]any{claudeAgent(sidA, proto.StatusIdle, "w5:p1", "w5:t1", "w5", cwd, "poll")}, nil))
	pushes, stop := pollNode(t, n)
	next := func() hubPush {
		t.Helper()
		select {
		case m := <-pushes:
			return m
		case <-time.After(10 * time.Second):
			t.Fatal("no push")
		}
		return hubPush{}
	}

	first := next()
	var changed struct {
		Sessions []proto.Session `json:"sessions"`
	}
	if first.Event != "sessions.changed" || json.Unmarshal(first.Params, &changed) != nil || len(changed.Sessions) != 1 ||
		changed.Sessions[0].SessionID != sidA {
		t.Fatalf("first push %s %s", first.Event, first.Params)
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})
	// The transcript grows by a line the list does not show: only its rev goes out.
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fh.WriteString(`{"type":"file-history-snapshot","messageId":"m1"}` + "\n")
	fh.Close()
	st, _ := os.Stat(p)
	second := next()
	var revs struct {
		Revs map[string]int64 `json:"revs"`
	}
	if second.Event != "sessions.revs" || json.Unmarshal(second.Params, &revs) != nil || revs.Revs[sidA] != st.Size() {
		t.Fatalf("second push %s %s (size %d)", second.Event, second.Params, st.Size())
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil), call("session.snapshot", nil)})
	stop()
}

// Panes swapped in herdr and nothing else: the next poll pushes sessions.changed with the new pane indexes (#2320).
func TestHerdrPollPushesALayoutOnlyChange(t *testing.T) {
	n, f, home := herdrNode(t)
	// Transcripts a minute apart keep the list in one order, so only the indexes differ between the pushes.
	for i, sid := range []string{sidA, sidB} {
		writeTranscript(t, home, "/tmp/repo", sid, time.Now().Add(-time.Duration(i+1)*time.Minute),
			`{"type":"user","timestamp":"2026-11-05T10:00:00Z","cwd":"/tmp/repo","message":{"role":"user","content":"hi"}}`)
	}
	rect := func(x, y, w, h int) map[string]any { return map[string]any{"x": x, "y": y, "width": w, "height": h} }
	// The rects swap; herdr's pane list and focus stay as they were.
	snap := func(left string) map[string]any {
		at := func(id string) map[string]any {
			if id == left {
				return rect(0, 0, 93, 55)
			}
			return rect(93, 0, 92, 55)
		}
		s := snapshot(
			[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
			[]any{map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "split", "number": 1}},
			[]any{
				claudeAgent(sidA, proto.StatusIdle, "w1:pA", "w1:t1", "w1", "/tmp/repo", "a"),
				claudeAgent(sidB, proto.StatusIdle, "w1:pB", "w1:t1", "w1", "/tmp/repo", "b"),
			}, nil)
		s["snapshot"].(map[string]any)["layouts"] = []any{map[string]any{
			"tab_id": "w1:t1", "workspace_id": "w1", "focused_pane_id": "w1:pA", "zoomed": false, "splits": []any{},
			"panes": []any{
				map[string]any{"pane_id": "w1:pA", "focused": true, "rect": at("w1:pA")},
				map[string]any{"pane_id": "w1:pB", "focused": false, "rect": at("w1:pB")},
			}}}
		return s
	}
	f.reply("session.snapshot", snap("w1:pA"))
	pushes, stop := pollNode(t, n)
	defer stop()
	indexes := func() string {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case m := <-pushes:
				if m.Event != "sessions.changed" {
					continue
				}
				var changed struct {
					Sessions []proto.Session `json:"sessions"`
				}
				if err := json.Unmarshal(m.Params, &changed); err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for _, s := range changed.Sessions {
					got[s.SessionID] = "none"
					if s.PaneIndex != nil {
						got[s.SessionID] = fmt.Sprint(*s.PaneIndex)
					}
				}
				return got[sidA] + "," + got[sidB]
			case <-deadline:
				t.Fatal("no sessions.changed")
				return ""
			}
		}
	}
	if got := indexes(); got != "0,1" {
		t.Fatalf("first push: pane indexes %s", got)
	}
	f.reply("session.snapshot", snap("w1:pB"))
	if got := indexes(); got != "1,0" {
		t.Fatalf("after the swap: pane indexes %s", got)
	}
}

// Active Claude sessions in Herdr panes are retained when Herdr's foreground process detection
// temporarily omits the agent during a tool or subshell execution, as long as the pane is still alive
// and the Claude process is running (#1641).
func TestHerdrActiveClaudeRetainedWhenAgentFlaps(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := "/tmp/repo"
	// Write live session file so liveClaudes sees the session's process alive.
	pid := os.Getpid()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	procFile := filepath.Join(dir, strconv.Itoa(pid)+".json")
	procBody, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": sidA, "cwd": cwd, "kind": "interactive", "entrypoint": "cli"})
	if err := os.WriteFile(procFile, procBody, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(procFile)

	writeTranscript(t, home, cwd, sidA, time.Now(),
		`{"type":"custom-title","customTitle":"fixing bug","sessionId":"`+sidA+`"}`,
		`{"type":"user","timestamp":"2026-11-04T12:46:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"run tests"}}`,
		strings.Repeat(" ", 4096))

	// Snapshot 1: Herdr sees the Claude agent in pane w1:p1.
	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
		[]any{map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "task", "number": 1}},
		[]any{claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", cwd, "fixing bug")},
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": cwd, "terminal_title": "fixing bug"}},
	))
	list1 := listSessions(t, n)
	m1 := byID(list1)
	s1, ok := m1[sidA]
	if !ok || s1.Status != proto.StatusWorking || s1.Agent != "claude" || s1.PaneID != "w1:p1" {
		t.Fatalf("snapshot 1: unexpected session %+v", s1)
	}

	// Snapshot 2: Herdr foreground process flapped to a child process (agent=None).
	// agents is empty, but panes still has w1:p1.
	f.reset()
	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
		[]any{map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "task", "number": 1}},
		[]any{},
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": cwd, "terminal_title": "cat test.go"}},
	))
	list2 := listSessions(t, n)
	m2 := byID(list2)
	s2, ok := m2[sidA]
	if !ok {
		t.Fatalf("snapshot 2: session lost from list: %+v", list2)
	}
	if s2.Status != proto.StatusWorking || s2.Agent != "claude" || s2.PaneID != "w1:p1" {
		t.Fatalf("snapshot 2: session fell through or degraded: %+v", s2)
	}

	// Snapshot 3: Claude process exited (proc file removed).
	_ = os.Remove(procFile)
	f.reset()
	f.reply("session.snapshot", snapshot(
		[]any{map[string]any{"workspace_id": "w1", "label": "repo", "number": 1}},
		[]any{map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "task", "number": 1}},
		[]any{},
		[]any{map[string]any{"pane_id": "w1:p1", "tab_id": "w1:t1", "workspace_id": "w1", "cwd": cwd, "terminal_title": "zsh"}},
	))
	list3 := listSessions(t, n)
	m3 := byID(list3)
	s3, ok := m3[sidA]
	if !ok {
		t.Fatalf("snapshot 3: offline session missing from list: %+v", list3)
	}
	if s3.Status != proto.StatusOffline || s3.Agent != "claude" || s3.PaneID != "" {
		t.Fatalf("snapshot 3: session should be offline without pane: %+v", s3)
	}
}
