package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A Codex started with no first message (codex_waiting.go, #4961) against a fake herdr: the start answers with a
// waiting row once Codex's input box is up, the first message from the app goes in as one paste and the row becomes
// the thread it began, and a thread is attached only by what the pane's own Codex says (its process, herdr, the
// message sent, the message on its screen alone), never by directory.

// Screens of Codex 0.153.4 in a fresh herdr tab (2026-11-12), the directory replaced.
const (
	cxUpdateOffer = "\n  ✨ Update available! 0.153.4 -> 0.167.1\n\n  Release notes: https://github.com/openai/codex/releases/latest\n\n" +
		"› 1. Update now (runs `sh -c 'curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh'`)\n" +
		"  2. Skip\n  3. Skip until next version\n\n  Press enter to continue\n"
	cxTrust0153 = "  Welcome to Codex, OpenAI's command-line coding agent\n\n> You are in /tmp/proj\n\n" +
		"  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection. Trusting the directory allows\n" +
		"  project-local config, hooks, and exec policies to load.\n\n› 1. Yes, continue\n  2. No, quit\n\n  Press enter to continue\n"
	cxLoading = "╭──────────────────────────────────────╮\n│ >_ OpenAI Codex (v0.153.4)           │\n│                                      │\n" +
		"│ model:     loading   /model to change │\n│ directory: /tmp/proj                 │\n╰──────────────────────────────────────╯\n\n\n" +
		"› Ask Codex to do anything\n\n  ? for shortcuts\n"
	cxMCP = "│ model:     gpt-6 xhigh   /model to change │\n│ directory: /tmp/proj                     │\n" +
		"╰──────────────────────────────────────────╯\n\n• Starting MCP servers (1/3): codex_apps (0s • esc to interrupt)\n\n" +
		"› Ask Codex to do anything\n\n  gpt-6 xhigh · /tmp/proj\n"
	cxReady = "│ model:     gpt-6 xhigh   /model to change │\n│ directory: /tmp/proj                     │\n" +
		"╰──────────────────────────────────────────╯\n\n› Ask Codex to do anything\n\n  gpt-6 xhigh · /tmp/proj\n"
)

// codexWaitHerdr is codexStartHerdr whose new tab shows Codex's update offer once the launcher runs, then (after "2")
// its box with the model loading, then ready a few reads later. The pane lists as a Codex herdr says works (it starts
// its MCP servers), with no session id.
func codexWaitHerdr(t *testing.T) (*Node, *fakeHerdr, string, *[]string) {
	t.Helper()
	withCodexWaits(t)
	withCodexLaunchers(t, map[string][]string{"codex": {"codex"}})
	n, f, cwd, cmds := codexStartHerdr(t, time.Hour)
	var mu sync.Mutex
	reads := -1
	f.setScreen("w1:p2", "")
	f.after(func(c herdrCall) {
		if str(c.Params["pane_id"]) != "w1:p2" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case c.Method == "pane.send_input" && strings.Contains(str(c.Params["text"]), "codex-start"):
			f.setScreen("w1:p2", cxUpdateOffer)
		case c.Method == "pane.send_keys" && slices.Equal(keysOf(c), []string{"2"}):
			f.setScreen("w1:p2", cxLoading)
			reads = 0
		case c.Method == "pane.read" && reads >= 0:
			if reads++; reads == 3 {
				f.setScreen("w1:p2", cxReady)
			}
		}
	})
	return n, f, cwd, cmds
}

func withCodexWaits(t *testing.T) {
	t.Helper()
	oldTick, oldBox, oldFirst, oldGone, oldStartPoll, oldPause := codexWaitTick, codexBoxWait, codexFirstWait, codexWaitGone, codexStartPoll, codexDialogPause
	codexWaitTick, codexBoxWait, codexFirstWait, codexWaitGone = time.Hour, 5*time.Second, 5*time.Second, 2
	codexStartPoll, codexDialogPause = 20*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() {
		codexWaitTick, codexBoxWait, codexFirstWait, codexWaitGone = oldTick, oldBox, oldFirst, oldGone
		codexStartPoll, codexDialogPause = oldStartPoll, oldPause
	})
}

func startWaiting(t *testing.T, n *Node) proto.Session {
	t.Helper()
	res, err := request(t, n, "session.start", map[string]any{"cwd": "~/proj", "launcher": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	return decodeSessions(t, []any{res["session"]})[0]
}

func TestCodexStartWithoutAMessage(t *testing.T) {
	n, f, cwd, cmds := codexWaitHerdr(t)
	s := startWaiting(t, n)
	if !isCodexWaitID(s.SessionID) || !s.AwaitingFirstMessage || s.Agent != "codex" || s.PaneID != "w1:p2" || s.Status != proto.StatusIdle || s.Cwd != cwd {
		t.Fatalf("answered %+v", s)
	}
	if len(*cmds) != 1 || strings.Join(ran, " ") != "codex" { // the launcher alone: no "--", no message
		t.Fatalf("ran %q (commands %q)", ran, *cmds)
	}
	// The update offer was skipped with its own key: never enter, which takes the highlighted "Update now".
	if keys := f.trace("pane.send_keys"); len(keys) != 1 || !strings.Contains(keys[0], `"2"`) {
		t.Fatalf("keys %q", keys)
	}
	// Listed as the waiting row while herdr says the pane works (Codex starting its MCP servers): idle, not a turn.
	list := listSessions(t, n)
	var rows []proto.Session
	for _, x := range list {
		if x.PaneID == "w1:p2" {
			rows = append(rows, x)
		}
	}
	if len(rows) != 1 || rows[0].SessionID != s.SessionID || !rows[0].AwaitingFirstMessage || rows[0].Status != proto.StatusIdle ||
		rows[0].Capabilities == nil || !rows[0].Capabilities.Send || !rows[0].Capabilities.Terminal {
		t.Fatalf("rows for the pane %+v", rows)
	}
	res, err := request(t, n, "session.items", map[string]any{"session_id": s.SessionID})
	if err != nil || len(res["items"].([]any)) != 0 {
		t.Fatalf("items %v %v", res, err)
	}
	if _, err := request(t, n, "session.modes", map[string]any{"session_id": s.SessionID}); err == nil {
		t.Error("modes of a row with no thread")
	}
}

// The first message from the app: typed once the box is up, as one paste of exactly what was sent, and answered with
// the thread it began, which the pane lists from then on; requests naming the row reach the thread.
func TestCodexFirstMessageFromTheApp(t *testing.T) {
	n, f, cwd, _ := codexWaitHerdr(t)
	home := filepath.Dir(cwd)
	s := startWaiting(t, n)
	var mu sync.Mutex
	var pasted string
	f.after(func(c herdrCall) {
		if str(c.Params["pane_id"]) != "w1:p2" {
			return
		}
		switch {
		case c.Method == "pane.send_text":
			mu.Lock()
			pasted = str(c.Params["text"])
			mu.Unlock()
			f.setScreen("w1:p2", "› [Pasted Content 9035 chars]\n\n  gpt-6 xhigh · /tmp/proj\n")
		case c.Method == "pane.send_keys" && slices.Equal(keysOf(c), []string{"enter"}):
			f.setScreen("w1:p2", "› fix\n\n• Working (0s • esc to interrupt)\n\n› Ask Codex to do anything\n")
			writeWaitRollout(t, home, cwd, startThread, time.Now(), specialPrompt)
		}
	})
	res, err := request(t, n, "session.send", map[string]any{"session_id": s.SessionID, "text": specialPrompt, "client_msg_id": "m1"})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeSessions(t, []any{res["session"]})[0]
	if got.SessionID != startThread || got.Agent != "codex" || got.PaneID != "w1:p2" {
		t.Fatalf("answered %+v", got)
	}
	mu.Lock()
	if pasted != pasteStart+specialPrompt+pasteEnd {
		t.Errorf("pasted %d bytes, not the message as sent", len(pasted))
	}
	mu.Unlock()
	if n.codexWaitByID(s.SessionID) != nil || n.codexAttachedThread(s.SessionID) != startThread {
		t.Fatal("the row did not become the thread")
	}
	st, ok := n.state.get(startThread)
	if !ok || st.Launcher != "codex" || st.Cwd != cwd || st.Pane != "w1:p2" {
		t.Errorf("thread's record %+v", st)
	}
	for _, x := range listSessions(t, n) {
		if x.PaneID == "w1:p2" && (x.SessionID != startThread || x.AwaitingFirstMessage) {
			t.Errorf("listed %+v", x)
		}
	}
	// A client that still names the row reaches the thread.
	f.reset()
	if _, err := request(t, n, "session.keys", map[string]any{"session_id": s.SessionID, "keys": []string{"escape"}}); err != nil {
		t.Fatal(err)
	}
	if keys := f.trace("pane.send_keys"); len(keys) != 1 || !strings.Contains(keys[0], "w1:p2") {
		t.Errorf("keys %q", keys)
	}
}

// What the computer has in the box, or a dialog not ours, refuses with nothing typed; so do photos.
func TestCodexFirstMessageRefusals(t *testing.T) {
	n, f, _, _ := codexWaitHerdr(t)
	s := startWaiting(t, n)
	f.setScreen("w1:p2", "› half a thought\n\n  gpt-6 xhigh · /tmp/proj\n")
	f.reset()
	_, err := request(t, n, "session.send", map[string]any{"session_id": s.SessionID, "text": "hello"})
	if code, data := refusalOf(t, err); code != "input_draft" || data["delivered"] != "none" {
		t.Errorf("draft: %q %v", code, data)
	}
	_, err = request(t, n, "session.send", map[string]any{"session_id": s.SessionID, "text": "hello",
		"images": []any{map[string]any{"media_type": "image/png", "data": "iVBORw0KGgo="}}})
	if code, _ := refusalOf(t, err); code != "codex_first_text" {
		t.Errorf("photo: %v", err)
	}
	if typed := f.trace("pane.send_text", "pane.send_keys"); len(typed) != 0 {
		t.Errorf("typed %q", typed)
	}
	if n.codexWaitByID(s.SessionID) == nil {
		t.Error("the row went")
	}
}

// The first message waits out a model still loading and the update offer, skipping the offer with "2" only.
func TestCodexFirstMessageWaitsForTheBox(t *testing.T) {
	n, f, cwd, _ := codexWaitHerdr(t)
	home := filepath.Dir(cwd)
	s := startWaiting(t, n)
	var mu sync.Mutex
	reads := 0
	f.setScreen("w1:p2", cxTrust0153)
	f.after(func(c herdrCall) {
		if str(c.Params["pane_id"]) != "w1:p2" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case c.Method == "pane.send_keys" && slices.Equal(keysOf(c), []string{"enter"}) && reads < 100:
			f.setScreen("w1:p2", cxMCP) // the trust dialog answered: MCP servers start
			reads = 100
		case c.Method == "pane.read" && reads >= 100 && reads < 1000:
			if reads++; reads == 103 {
				f.setScreen("w1:p2", cxReady)
				reads = 1000
			}
		case c.Method == "pane.send_text":
			f.setScreen("w1:p2", "› hello\n\n  gpt-6 xhigh · /tmp/proj\n")
		case c.Method == "pane.send_keys" && slices.Equal(keysOf(c), []string{"enter"}) && reads == 1000:
			writeWaitRollout(t, home, cwd, startThread, time.Now(), "hello")
		}
	})
	f.reset()
	res, err := request(t, n, "session.send", map[string]any{"session_id": s.SessionID, "text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeSessions(t, []any{res["session"]})[0]; got.SessionID != startThread {
		t.Fatalf("answered %+v", got)
	}
	// enter twice: the trust dialog's ("Yes, continue" under the cursor) and the message's, after its paste.
	var trace []string
	for _, c := range f.calls() {
		if c.Method == "pane.send_keys" || c.Method == "pane.send_text" {
			trace = append(trace, c.Method+" "+strings.Join(keysOf(c), ","))
		}
	}
	if want := []string{"pane.send_keys enter", "pane.send_text ", "pane.send_keys enter"}; !slices.Equal(trace, want) {
		t.Errorf("typed %q, want %q", trace, want)
	}
}

// Two Codex panes waiting in one directory each get their own thread: the one whose own codex process holds a
// rollout gets that; the other gets the one new thread left when its first message shows on its screen alone. An
// app-server a pane started (it carries that pane's HERDR_PANE_ID) holding a thread says nothing about the pane.
func TestCodexWaitingRowsInOneDirectory(t *testing.T) {
	n, f, home := herdrNode(t)
	withCodexWaits(t)
	n.codices = newCodexRegistry()
	n.configureCodex(proto.CodexConfig{Homes: []string{filepath.Join(home, ".codex")}})
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		agentOf("codex", "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, ""),
		agentOf("codex", "", proto.StatusIdle, "w1:p3", "w1:t3", "w1", cwd, ""),
	}, nil))
	began := time.Now().Add(-time.Minute)
	n.openCodexWait(context.Background(), "w1:p2", cwd, "codex", "", began)
	n.openCodexWait(context.Background(), "w1:p3", cwd, "codex", "", began)
	w2, w3 := n.codexWaitOf("w1:p2"), n.codexWaitOf("w1:p3")
	if w2 == nil || w3 == nil || w2.id == w3.id {
		t.Fatal("no rows")
	}
	const threadA, threadB = "01a19a00-0000-7000-8000-00000000000a", "01a19a00-0000-7000-8000-00000000000b"
	pathA := writeWaitRollout(t, home, cwd, threadA, time.Now().Add(-5*time.Second), "make the tests pass")
	pathB := writeWaitRollout(t, home, cwd, threadB, time.Now().Add(-4*time.Second), "write the docs")
	tui, daemon := holdOpen(t, pathB), holdOpen(t, pathA)
	n.codices.checked = nil
	n.codices.scan = func() []codexProcess {
		return []codexProcess{
			{pid: tui, home: filepath.Join(home, ".codex"), pane: "w1:p3"},
			{pid: daemon, home: filepath.Join(home, ".codex"), pane: "w1:p2", endpoint: "unix://"},
		}
	}
	// Nothing on p2's screen yet: B goes to p3 (its process), A to nobody.
	f.setScreen("w1:p2", cxReady)
	f.setScreen("w1:p3", cxReady)
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w2, w3}, true)
	if got := n.codexAttachedThread(w3.id); got != threadB {
		t.Fatalf("p3 has %q", got)
	}
	if again := n.codexWaitOf("w1:p3"); again != nil { // the pane is the thread's now: no row made again from its record
		t.Fatalf("p3 waits again as %s", again.id)
	}
	if n.codexWaitByID(w2.id) == nil {
		t.Fatal("p2 took a thread its screen does not show")
	}
	// A's first message on p2's screen, and on no other waiting pane's: p2 takes it.
	f.setScreen("w1:p2", "› make the tests pass\n\n• Working (3s • esc to interrupt)\n\n› Ask Codex to do anything\n")
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w2}, true)
	if got := n.codexAttachedThread(w2.id); got != threadA {
		t.Fatalf("p2 has %q", got)
	}
	byPane := map[string]string{}
	for _, x := range listSessions(t, n) {
		if x.PaneID != "" {
			byPane[x.PaneID] = x.SessionID
		}
	}
	if byPane["w1:p2"] != threadA || byPane["w1:p3"] != threadB {
		t.Errorf("listed %v", byPane)
	}
}

// Rule 4 is strict: two new threads in the directory, or a first message shown on two waiting panes, attach none.
func TestCodexWaitingRowsAmbiguousScreens(t *testing.T) {
	n, f, home := herdrNode(t)
	withCodexWaits(t)
	n.codices = newCodexRegistry()
	n.configureCodex(proto.CodexConfig{Homes: []string{filepath.Join(home, ".codex")}})
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		agentOf("codex", "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, ""),
		agentOf("codex", "", proto.StatusIdle, "w1:p3", "w1:t3", "w1", cwd, ""),
	}, nil))
	n.codices.checked = nil
	n.codices.scan = func() []codexProcess { return nil }
	began := time.Now().Add(-time.Minute)
	n.openCodexWait(context.Background(), "w1:p2", cwd, "codex", "", began)
	n.openCodexWait(context.Background(), "w1:p3", cwd, "codex", "", began)
	w2, w3 := n.codexWaitOf("w1:p2"), n.codexWaitOf("w1:p3")
	writeWaitRollout(t, home, cwd, "01a19a00-0000-7000-8000-00000000000a", time.Now().Add(-5*time.Second), "same words")
	shown := "› same words\n\n› Ask Codex to do anything\n"
	f.setScreen("w1:p2", shown)
	f.setScreen("w1:p3", shown)
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w2, w3}, true)
	if n.codexWaitByID(w2.id) == nil || n.codexWaitByID(w3.id) == nil {
		t.Fatal("attached a message two panes show")
	}
	f.setScreen("w1:p3", cxReady)
	writeWaitRollout(t, home, cwd, "01a19a00-0000-7000-8000-00000000000b", time.Now().Add(-5*time.Second), "other words")
	clearRolloutCache(home)
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w2, w3}, true)
	if n.codexWaitByID(w2.id) == nil {
		t.Fatal("attached one of two new threads")
	}
}

// After a restart the row is known again from the pane's binding (the hub gives it back) and its record.
func TestCodexWaitingRowAfterARestart(t *testing.T) {
	n, f, home := herdrNode(t)
	withCodexWaits(t)
	n.codices = newCodexRegistry()
	n.configureCodex(proto.CodexConfig{Homes: []string{filepath.Join(home, ".codex")}})
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusDone, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	n.codices.checked = nil
	n.codices.scan = func() []codexProcess { return nil }
	n.openCodexWait(context.Background(), "w1:p2", cwd, "codex", "", time.Now())
	id := n.codexWaitOf("w1:p2").id
	n.mu.Lock()
	n.codexWaiting = nil // what a new process has: the binding and the record only
	n.mu.Unlock()
	list := listSessions(t, n)
	found := false
	for _, x := range list {
		if x.PaneID == "w1:p2" {
			found = x.SessionID == id && x.AwaitingFirstMessage && x.Status == proto.StatusIdle
		}
	}
	if !found || n.codexWaitByID(id) == nil {
		t.Fatalf("not known again: %+v", list)
	}
}

// The row goes once its pane runs no codex (the user quit Codex before a first message); the pane's binding goes too.
func TestCodexWaitingRowGoesWithCodex(t *testing.T) {
	n, f, home := herdrNode(t)
	withCodexWaits(t)
	n.codices = newCodexRegistry()
	n.configureCodex(proto.CodexConfig{Homes: []string{filepath.Join(home, ".codex")}})
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	n.codices.checked = nil
	n.codices.scan = func() []codexProcess { return nil }
	n.openCodexWait(context.Background(), "w1:p2", cwd, "codex", "", time.Now())
	w := n.codexWaitOf("w1:p2")
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("pane.get", map[string]any{"pane": map[string]any{"pane_id": "w1:p2", "agent": nil}})
	_ = n.refreshHosts(context.Background())
	for range codexWaitGone {
		n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w}, false)
	}
	if n.codexWaitByID(w.id) != nil {
		t.Fatal("the row stayed")
	}
	n.mu.Lock()
	bound := n.panes["w1:p2"]
	n.mu.Unlock()
	if bound != "" {
		t.Errorf("pane still bound to %q", bound)
	}
}

// A waiting pane whose Codex resumed a thread (/resume on the shared app-server: no process of the pane holds it, no
// new thread begins) is attached by its title, only when exactly one thread has that whole name.
func TestCodexWaitingRowTakesTheThreadItsTitleNames(t *testing.T) {
	n, f, home := herdrNode(t)
	withCodexWaits(t)
	n.codices = newCodexRegistry()
	n.configureCodex(proto.CodexConfig{Homes: []string{filepath.Join(home, ".codex")}})
	n.codices.checked = nil
	n.codices.scan = func() []codexProcess { return nil }
	cwd := mkdir(t, filepath.Join(home, "proj"))
	const old, twin = "01a19a00-0000-7000-8000-0000000000c1", "01a19a00-0000-7000-8000-0000000000c2"
	writeWaitRollout(t, home, cwd, old, time.Now().Add(-48*time.Hour), "yesterday's work")
	writeWaitRollout(t, home, cwd, twin, time.Now().Add(-72*time.Hour), "older work")
	index := `{"id":"` + old + `","thread_name":"Fix the scheduler"}` + "\n" + `{"id":"` + twin + `","thread_name":"Same name"}` + "\n" +
		`{"id":"01a19a00-0000-7000-8000-0000000000c3","thread_name":"Same name"}` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".codex", "session_index.jsonl"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	list := func(title string) {
		f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, title)}, nil))
		_ = n.refreshHosts(context.Background())
	}
	n.openCodexWait(context.Background(), "w1:p2", cwd, "codex", "", time.Now())
	w := n.codexWaitOf("w1:p2")
	list("Same name | proj") // two threads have that name: none is taken
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w}, false)
	if n.codexWaitByID(w.id) == nil {
		t.Fatal("attached a name two threads share")
	}
	list("Fix the sched... | proj") // cut short
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w}, false)
	if n.codexWaitByID(w.id) == nil {
		t.Fatal("attached a name cut short")
	}
	list("Fix the scheduler | proj")
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w}, false)
	if got := n.codexAttachedThread(w.id); got != old {
		t.Fatalf("attached %q", got)
	}
}

// A new thread no rule attaches (a Codex outside herdr in the same repository) makes the look scan the processes a
// few times, then only with the periodic full scan.
func TestCodexWaitingRowStopsScanningForAThreadNotItsOwn(t *testing.T) {
	n, f, home := herdrNode(t)
	withCodexWaits(t)
	n.codices = newCodexRegistry()
	n.configureCodex(proto.CodexConfig{Homes: []string{filepath.Join(home, ".codex")}})
	n.codices.checked = nil
	scans := 0
	n.codices.scan = func() []codexProcess { scans++; return nil }
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	f.setScreen("w1:p2", cxReady)
	n.openCodexWait(context.Background(), "w1:p2", cwd, "codex", "", time.Now().Add(-time.Minute))
	w := n.codexWaitOf("w1:p2")
	writeWaitRollout(t, home, cwd, "01a19a00-0000-7000-8000-0000000000d1", time.Now().Add(-10*time.Second), "someone else's")
	for range codexJudgeLooks + 3 {
		n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w}, false)
	}
	if scans != codexJudgeLooks || n.codexWaitByID(w.id) == nil {
		t.Fatalf("%d scans (want %d), row waits %v", scans, codexJudgeLooks, n.codexWaitByID(w.id) != nil)
	}
	n.lookForCodexThreadsOnce(context.Background(), []*codexWait{w}, true) // the periodic one still looks
	if scans != codexJudgeLooks+1 {
		t.Fatalf("%d scans after a full look", scans)
	}
}

// "/exit" as the first message quits Codex as for any agent: nothing is pasted into its box.
func TestCodexFirstMessageExit(t *testing.T) {
	n, f, _, _ := codexWaitHerdr(t)
	s := startWaiting(t, n)
	f.reset()
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_keys" && slices.Equal(keysOf(c), []string{"ctrl+c"}) {
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
		}
	})
	if _, err := request(t, n, "session.send", map[string]any{"session_id": s.SessionID, "text": "/exit"}); err != nil {
		t.Fatal(err)
	}
	if pasted := f.trace("pane.send_text"); len(pasted) != 0 {
		t.Errorf("pasted %q", pasted)
	}
	if keys := f.trace("pane.send_keys"); len(keys) == 0 || !strings.Contains(keys[0], "ctrl+c") {
		t.Errorf("keys %q", keys)
	}
}

func TestCodexStartScreens(t *testing.T) {
	if keys := codexSkipUpdateKeys(cxUpdateOffer); !slices.Equal(keys, []string{"2"}) {
		t.Errorf("update offer keys %v", keys)
	}
	if codexSkipUpdateKeys(cxTrust0153) != nil || codexSkipUpdateKeys(cxReady) != nil {
		t.Error("skip keys for a screen without the offer")
	}
	if keys := codexTrustKeys(cxTrust0153); !slices.Equal(keys, []string{"enter"}) {
		t.Errorf("trust keys %v", keys)
	}
	if !codexLoadingRE.MatchString(cxLoading) || codexLoadingRE.MatchString(cxReady) || codexLoadingRE.MatchString(cxMCP) {
		t.Error("loading")
	}
	if !codexBusyStarting(cxMCP) || codexBusyStarting(cxReady) {
		t.Error("MCP servers starting")
	}
}

func TestCodexMessageShown(t *testing.T) {
	screen := "• Done.\n\n› fix the flaky test in the scheduler and\n  run the suite twice\n  第二行\n\n• Working (1s • esc to interrupt)\n\n› Ask Codex to do anything\n"
	for _, c := range []struct {
		message string
		want    bool
	}{
		{"fix the flaky test in the scheduler and run the suite twice\n第二行", true},
		{"fix the flaky test in the scheduler and run the suite twice", false}, // a prefix is not the message
		{"run the suite twice", false},
		{"Ask Codex to do anything", true}, // the box counts: it can only make a match ambiguous
		{"  ", false},
	} {
		if got := codexMessageShown(screen, c.message); got != c.want {
			t.Errorf("%q: %v", c.message, got)
		}
	}
}

func TestCodexWaitIDs(t *testing.T) {
	id := codexWaitID()
	if !isCodexWaitID(id) || !validID(id) || isCodexWaitID(startThread) || isCodexWaitID(newUUID()) {
		t.Fatalf("%q", id)
	}
}

// writeWaitRollout writes a thread's rollout as Codex does once it has its first message.
func writeWaitRollout(t *testing.T, home, cwd, thread string, at time.Time, first string) string {
	t.Helper()
	dir := mkdir(t, filepath.Join(home, ".codex", "sessions", at.Format("2006"), at.Format("01"), at.Format("02")))
	resolved, _ := filepath.EvalSymlinks(cwd)
	ts := at.UTC().Format(time.RFC3339Nano)
	meta, _ := json.Marshal(map[string]any{"timestamp": ts, "type": "session_meta", "payload": map[string]any{"id": thread, "cwd": resolved}})
	user, _ := json.Marshal(map[string]any{"timestamp": ts, "type": "event_msg", "payload": map[string]any{"type": "item_completed",
		"item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": first}}}}})
	path := filepath.Join(dir, "rollout-"+at.Format("2006-01-02T15-04-05")+"-"+thread+".jsonl")
	if err := os.WriteFile(path, []byte(string(meta)+"\n"+string(user)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearRolloutCache(filepath.Join(home, ".codex"))
	return path
}

func clearRolloutCache(home string) {
	rolloutCaches.Lock()
	for h, c := range rolloutCaches.byHome {
		if h == home || strings.HasPrefix(h, home) || strings.HasPrefix(home, h) {
			c.at = time.Time{}
		}
	}
	rolloutCaches.Unlock()
}

// holdOpen runs a process that holds path open, as a Codex holds its rollout, and returns its pid.
func holdOpen(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := exec.Command("sleep", "60")
	cmd.ExtraFiles = []*os.File{f}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd.Process.Pid
}
