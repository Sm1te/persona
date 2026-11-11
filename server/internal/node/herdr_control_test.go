package node

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Characterization tests of what the node does to herdr to start, prompt, key, close and rename sessions: the exact
// herdr calls, in order, driven through the node's requests.

const startID = "0e000000-0000-4000-8000-0000000000aa"

// startHerdr answers agent.get for pane as a claude in status, and a snapshot with no agents.
func startHerdr(f *fakeHerdr, pane, status string) {
	f.reply("agent.get", map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": status, "pane_id": pane,
		"tab_id": "tab-of-" + pane, "workspace_id": "ws-of-" + pane}})
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
}

// startCmd is the command a Claude start types into its herdr pane (and herdr's resume of it evals): the node's
// directory in front and the assistant's environment left out (agentenv.go), herdr told the agent is Claude
// (HERDR_AGENT), then the launcher with Persona's flags.
func startCmd(launcher, args, dir string) string {
	return inAgentEnv(dir, "HERDR_AGENT=claude "+launcher+" "+args+" --settings "+shellQuote(filepath.Join(dir, "claude-settings.json"))+
		" --mcp-config "+shellQuote(filepath.Join(dir, "mcp.json")), shellQuote)
}

// resumeHeld are the two reports that give herdr the pane's resume command (herdrresume.go): the pane held as
// Persona's, then the session with eval of cmd as its resume_argv.
func resumeHeld(pane, id, cmd string) [2]string {
	return resumeHeldAgent("claude", pane, id, cmd)
}

func resumeHeldAgent(agent, pane, id, cmd string) [2]string {
	return [2]string{
		call("pane.report_agent", map[string]any{"pane_id": pane, "source": resumeSource, "agent": agent, "state": "idle", "seq": 7}),
		call("pane.report_agent_session", map[string]any{"pane_id": pane, "source": resumeSource, "agent": agent, "seq": 8,
			"agent_session_id": id, "resume_argv": []string{"eval", cmd}}),
	}
}

func readVisible(pane string) string {
	return call("pane.read", map[string]any{"pane_id": pane, "source": "visible", "lines": 60})
}

// readCodexEnd is how a Codex pane is read (codexScreen): the end of its output, colored.
func readCodexEnd(pane string) string {
	return call("pane.read", map[string]any{"pane_id": pane, "source": "recent", "lines": 60, "format": "ansi", "strip_ansi": false})
}

func readANSI(pane string) string {
	return call("pane.read", map[string]any{"pane_id": pane, "source": "visible", "lines": 60, "format": "ansi", "strip_ansi": false})
}

func agentGet(pane string) string { return call("agent.get", map[string]any{"target": pane}) }

func sendKeys(pane string, keys ...string) string {
	return call("pane.send_keys", map[string]any{"pane_id": pane, "keys": keys})
}

// waitCalls waits until the fake has recorded at least n calls of method.
func waitCalls(t *testing.T, f *fakeHerdr, method string, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for len(f.trace(method)) < n {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d %s calls; got:\n  %s", n, method, strings.Join(f.trace(), "\n  "))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func personaDir(home string) string { return filepath.Join(home, ".persona") }

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// session.start (async, the app's path): the workspace named after the directory is reused, a new tab in it (named
// agent-<n> past the taken ones), the launcher typed with the session id, mode and effort flags, the trust dialog
// accepted, then the agent renamed and the first message prompted. The pane is bound to the session from then on.
func TestHerdrStartInTheProjectsWorkspace(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{
		map[string]any{"workspace_id": "w0", "label": "other"}, map[string]any{"workspace_id": "w1", "label": "proj"}}})
	startHerdr(f, "w1:p2", proto.StatusIdle)
	f.reply("session.snapshot", snapshot(nil, []any{
		map[string]any{"tab_id": "w1:t1", "workspace_id": "w1", "label": "agent-1"},
		map[string]any{"tab_id": "w0:t1", "workspace_id": "w0", "label": "agent-2"}, // another workspace's: free here
	}, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.screenSteps("w1:p2", isKeys(), screenFixture(t, "claude-2.1.312-trust-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.txt"))
	promptLands(t, f, home, cwd) // the transcript has it: nothing more is asked of herdr

	res, err := request(t, n, "session.start", map[string]any{"session_id": startID, "cwd": "~/proj", "prompt": "fix the build",
		"permission_mode": "plan", "effort": "high", "async": true})
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSessions(t, []any{res["session"]})[0]
	if s.SessionID != startID || !s.Starting || s.Status != proto.StatusIdle || s.Cwd != cwd || s.Launcher != "claude" ||
		s.PermissionMode != "plan" || s.Effort != "high" {
		t.Errorf("answered %+v", s)
	}
	waitCalls(t, f, "session.snapshot", 2) // tab labels, then sessionFor at the end
	assertTrace(t, f.trace(), []string{
		call("workspace.list", nil),
		call("session.snapshot", nil),
		call("tab.create", map[string]any{"workspace_id": "w1", "cwd": cwd, "label": "agent-2", "focus": false}),
		markTab("w1:p2", startID),
		call("pane.send_input", map[string]any{"pane_id": "w1:p2", "keys": []string{"Enter"},
			"text": startCmd("claude", "--session-id "+startID+" --permission-mode plan --effort high", personaDir(home))}),
		readVisible("w1:p2"), // the trust dialog, cursor on "No, exit"
		sendKeys("w1:p2", "down", "enter"),
		readVisible("w1:p2"), agentGet("w1:p2"), // ready once…
		readVisible("w1:p2"), agentGet("w1:p2"), // …and twice
		call("agent.rename", map[string]any{"target": "w1:p2", "name": "j-0e0000"}),
		resumeHeld("w1:p2", startID, startCmd("claude", "--resume "+startID, personaDir(home)))[0],
		resumeHeld("w1:p2", startID, startCmd("claude", "--resume "+startID, personaDir(home)))[1],
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "fix the build"}),
		call("session.snapshot", nil), // sessionFor
	})

	// herdr now shows a claude in the pane without naming its session: the pane's binding from the start names it.
	f.reset()
	f.reply("pane.process_info", processInfo(1<<30))
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent("", proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	list := listSessions(t, n)
	if len(list) != 1 || list[0].SessionID != startID || list[0].PaneID != "w1:p2" || !list[0].StartedByPersona ||
		list[0].Launcher != "claude" || list[0].Starting {
		t.Fatalf("listed %+v", list)
	}
}

// No workspace for the directory: one is made (labelled with the directory's name, not focused) and its first tab
// renamed; a resume (session.start with resume_session_id, answered when ready) types --resume and no first message.
func TestHerdrStartCreatesTheWorkspaceAndResumes(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "fresh"))
	writeTranscript(t, home, cwd, startID, time.Now(),
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"hello"}}`)
	f.reply("workspace.list", map[string]any{"workspaces": []any{}})
	f.reply("workspace.create", map[string]any{"workspace": map[string]any{"workspace_id": "w7"},
		"tab": map[string]any{"tab_id": "w7:t1"}, "root_pane": map[string]any{"pane_id": "w7:p1"}})
	startHerdr(f, "w7:p1", proto.StatusDone)
	f.setScreen("w7:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))

	res, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "launcher": "work_claude",
		"resume_session_id": startID, "tab_name": "  fix\nthe   build "})
	if err != nil {
		t.Fatal(err)
	}
	if s := decodeSessions(t, []any{res["session"]})[0]; s.SessionID != startID || s.PaneID != "w7:p1" || s.Status != proto.StatusDone ||
		s.SpaceKind != "herdr" || !s.StartedByPersona || s.Launcher != "work_claude" {
		t.Errorf("session %+v", s)
	}
	assertTrace(t, f.trace(), []string{
		call("workspace.list", nil),
		call("workspace.create", map[string]any{"cwd": cwd, "label": "fresh", "focus": false}),
		call("tab.rename", map[string]any{"tab_id": "w7:t1", "label": "fix the build"}),
		markTab("w7:p1", startID),
		call("pane.send_input", map[string]any{"pane_id": "w7:p1", "keys": []string{"Enter"},
			"text": startCmd("work_claude", "--resume "+startID, personaDir(home))}),
		readVisible("w7:p1"), agentGet("w7:p1"),
		readVisible("w7:p1"), agentGet("w7:p1"),
		call("agent.rename", map[string]any{"target": "w7:p1", "name": "j-0e0000"}),
		resumeHeld("w7:p1", startID, startCmd("work_claude", "--resume "+startID, personaDir(home)))[0],
		resumeHeld("w7:p1", startID, startCmd("work_claude", "--resume "+startID, personaDir(home)))[1],
		call("session.snapshot", nil),
	})
}

// A resume of a session whose first start failed (claude never came up, so it wrote no transcript) starts it under
// its id: `claude --resume` would answer "No conversation found" (issue #861).
func TestHerdrResumeWithoutTranscriptStartsUnderItsID(t *testing.T) {
	n, f, home := herdrNode(t)
	n.starts.add(startID, &startRec{done: make(chan struct{}), session: proto.Session{SessionID: startID, Starting: true}})
	n.starts.finish(startID, errors.New("claude did not become ready"))
	cwd := mkdir(t, filepath.Join(home, "fresh"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{}})
	f.reply("workspace.create", map[string]any{"workspace": map[string]any{"workspace_id": "w7"},
		"tab": map[string]any{"tab_id": "w7:t1"}, "root_pane": map[string]any{"pane_id": "w7:p1"}})
	startHerdr(f, "w7:p1", proto.StatusDone)
	f.setScreen("w7:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"))
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "launcher": "claude", "resume_session_id": startID}); err != nil {
		t.Fatal(err)
	}
	if err := n.starts.wait(t.Context(), startID); err != nil {
		t.Fatalf("a successful recovery must not reject subsequent messages with the old start error: %v", err)
	}
	want := call("pane.send_input", map[string]any{"pane_id": "w7:p1", "keys": []string{"Enter"},
		"text": startCmd("claude", "--session-id "+startID, personaDir(home))})
	for _, c := range f.trace() {
		if c == want {
			return
		}
	}
	t.Fatalf("no --session-id start in %v", f.trace())
}

// A workspace given (a tab next to another agent): no workspace lookup. A claude that comes up blocked (a dialog of
// its own) counts as ready at once; the first message is not typed into the dialog, and the session is listed as
// needing the user, saying so (firstprompt.go). A managed launcher (kept by the hub) runs as `personad launch <name>`.
func TestHerdrStartIntoAGivenWorkspaceReturnsOnABlockedClaude(t *testing.T) {
	n, f, home := herdrNode(t)
	if _, err := request(t, n, "launchers.managed", map[string]any{"launchers": []any{
		map[string]any{"name": "work_gw", "base_url": "https://gateway.example.com", "model": "m"}}}); err != nil {
		t.Fatal(err)
	}
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w2:t5"}, "root_pane": map[string]any{"pane_id": "w2:p5"}})
	startHerdr(f, "w2:p5", proto.StatusBlocked)
	f.setScreen("w2:p5", screenFixture(t, "claude-2.1.312-model-picker-162.txt"))
	if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "launcher": "work_gw", "prompt": "hello",
		"workspace_id": "w2", "tab_name": "a very long tab name that goes past thirty-two runes", "async": true}); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, f, "session.snapshot", 2)
	assertTrace(t, f.trace(), []string{
		call("session.snapshot", nil),
		call("tab.create", map[string]any{"workspace_id": "w2", "cwd": home, "label": "a very long tab name that goes p", "focus": false}),
		markTab("w2:p5", startID),
		// A managed launcher's start gets Persona's settings with its gateway merged in (#1336).
		call("pane.send_input", map[string]any{"pane_id": "w2:p5", "keys": []string{"Enter"},
			"text": inAgentEnv(personaDir(home), "HERDR_AGENT=claude /tmp/persona-test/personad launch work_gw --session-id "+startID+" --settings "+
				shellQuote(filepath.Join(personaDir(home), "launcher-settings", "work_gw-claude-settings.json"))+
				" --mcp-config "+shellQuote(filepath.Join(personaDir(home), "mcp.json")), shellQuote)}),
		readVisible("w2:p5"), agentGet("w2:p5"),
		call("agent.rename", map[string]any{"target": "w2:p5", "name": "j-0e0000"}),
		// herdr's restore resumes it through the launcher too, not with a bare `claude --resume` (#1879).
		resumeHeld("w2:p5", startID, inAgentEnv(personaDir(home), "HERDR_AGENT=claude /tmp/persona-test/personad launch work_gw --resume "+startID+" --settings "+
			filepath.Join(personaDir(home), "launcher-settings", "work_gw-claude-settings.json")+
			" --mcp-config "+filepath.Join(personaDir(home), "mcp.json"), dquote))[0],
		resumeHeld("w2:p5", startID, inAgentEnv(personaDir(home), "HERDR_AGENT=claude /tmp/persona-test/personad launch work_gw --resume "+startID+" --settings "+
			filepath.Join(personaDir(home), "launcher-settings", "work_gw-claude-settings.json")+
			" --mcp-config "+filepath.Join(personaDir(home), "mcp.json"), dquote))[1],
		call("session.snapshot", nil),
	})
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(startID, proto.StatusIdle, "w2:p5", "w2:t5", "w2", home, "")}, nil))
	if l := listSessions(t, n); len(l) != 1 || l[0].Status != proto.StatusBlocked || l[0].AttentionReason != lostDialog {
		t.Errorf("listed %+v", l)
	}
}

// A tab name given (session.rename) while the tab is still being made is kept and given to the tab once it exists.
func TestHerdrStartNamesTheTabOnceItExists(t *testing.T) {
	n, f, home := herdrNode(t)
	startHerdr(f, "w2:p6", proto.StatusIdle)
	f.setScreen("w2:p6", screenFixture(t, "claude-2.1.312-empty-162.txt"))
	entered, release := make(chan struct{}), make(chan struct{})
	f.handle("tab.create", func(map[string]any) any {
		close(entered)
		<-release
		return map[string]any{"tab": map[string]any{"tab_id": "w2:t6"}, "root_pane": map[string]any{"pane_id": "w2:p6"}}
	})
	if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "workspace_id": "w2", "async": true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("no tab.create")
	}
	if _, err := request(t, n, "session.rename", map[string]any{"session_id": startID, "tab_name": "named early"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitCalls(t, f, "session.snapshot", 2)
	assertTrace(t, f.trace(), []string{
		call("session.snapshot", nil),
		call("tab.create", map[string]any{"workspace_id": "w2", "cwd": home, "label": "agent-1", "focus": false}),
		markTab("w2:p6", startID),
		call("tab.rename", map[string]any{"tab_id": "w2:t6", "label": "named early"}),
		call("pane.send_input", map[string]any{"pane_id": "w2:p6", "keys": []string{"Enter"},
			"text": startCmd("claude", "--session-id "+startID, personaDir(home))}),
		readVisible("w2:p6"), agentGet("w2:p6"),
		readVisible("w2:p6"), agentGet("w2:p6"),
		call("agent.rename", map[string]any{"target": "w2:p6", "name": "j-0e0000"}),
		resumeHeld("w2:p6", startID, startCmd("claude", "--resume "+startID, personaDir(home)))[0],
		resumeHeld("w2:p6", startID, startCmd("claude", "--resume "+startID, personaDir(home)))[1],
		call("session.snapshot", nil),
	})
}

// tab.create failing: nothing is typed, and the session stays listed, offline, with the error.
func TestHerdrStartFailsWhenTheTabCannotBeMade(t *testing.T) {
	n, f, home := herdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", herdrFail{Code: "workspace_not_found", Message: "no such workspace"})
	if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "cwd": home, "workspace_id": "w9", "async": true}); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, f, "tab.create", 1)
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil), call("tab.create", map[string]any{"workspace_id": "w9", "cwd": home, "label": "agent-1", "focus": false})})
	var s proto.Session
	deadline := time.Now().Add(10 * time.Second)
	for s.StartError == "" {
		if time.Now().After(deadline) {
			t.Fatalf("start did not fail: %+v", s)
		}
		time.Sleep(20 * time.Millisecond)
		if l := listSessions(t, n); len(l) == 1 {
			s = l[0]
		}
	}
	if s.SessionID != startID || s.Status != proto.StatusOffline || s.Starting || (!strings.Contains(s.StartError, "create herdr tab") && !strings.Contains(s.StartError, "创建终端标签页失败")) {
		t.Errorf("listed %+v", s)
	}
	trace := slices.DeleteFunc(f.trace(), func(c string) bool { return c == call("session.snapshot", nil) })
	assertTrace(t, trace, []string{call("tab.create", map[string]any{"workspace_id": "w9", "cwd": home, "label": "agent-1", "focus": false})})
}

// withStartWaits shortens how long n's starts wait for the shell to run claude, then for claude's input box. Set on n
// before its first start, not on package variables: a start another test left running still reads its own node's.
func withStartWaits(n *Node, start, ready time.Duration) {
	n.claudeStart, n.claudeReady = start, ready
}

// A busy machine's shell takes longer than claude's whole ready wait to run claude (#1396: ~/.zshrc took 54 s with a
// dozen agents working): the wait for the input box starts once claude runs, so the start still succeeds.
func TestHerdrStartWaitsForASlowShell(t *testing.T) {
	n, f, home := herdrNode(t)
	withStartWaits(n, 20*time.Second, 1500*time.Millisecond)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	var mu sync.Mutex
	gets := 0
	f.handle("agent.get", func(map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if gets++; gets <= 6 { // ~3 s of a shell still loading
			return map[string]any{"agent": map[string]any{"agent": "", "agent_status": "unknown", "pane_id": "w1:p2"}}
		}
		return map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusIdle, "pane_id": "w1:p2",
			"tab_id": "w1:t2", "workspace_id": "w1"}}
	})
	f.setScreen("w1:p2", screenFixture(t, "claude-2.1.312-empty-162.txt"))
	res, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "prompt": "hi"})
	if err != nil {
		t.Fatalf("a slow shell failed the start: %v", err)
	}
	if s := decodeSessions(t, []any{res["session"]})[0]; s.PaneID != "w1:p2" {
		t.Errorf("session %+v", s)
	}
	if got := f.trace("tab.close"); len(got) != 0 {
		t.Errorf("a started tab was closed: %v", got)
	}
}

// A shell that never runs claude fails the start after claudeStartWait, saying so, and closes the tab it made: a
// claude that came up later would sit there unbound, and a retry opens a new one.
func TestHerdrStartThatNeverRunsClaudeClosesItsTab(t *testing.T) {
	n, f, home := herdrNode(t)
	withStartWaits(n, 1500*time.Millisecond, time.Minute)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.reply("agent.get", map[string]any{"agent": map[string]any{"agent": "", "agent_status": "unknown", "pane_id": "w1:p2"}})
	f.reply("pane.list", map[string]any{"panes": []any{map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2"}}})
	f.setScreen("w1:p2", "~/proj %")
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "claude did not start within") {
		t.Fatalf("start: %v", err)
	}
	if got := f.trace("tab.close"); len(got) != 1 || got[0] != call("tab.close", map[string]any{"tab_id": "w1:t2"}) {
		t.Errorf("closed %v", got)
	}
}

// Claude runs but never shows its input box: the start fails claudeReadyWait after claude came up, as
// agent_start_stalled with the tab's last lines, and keeps the tab for someone to look at (#2570).
func TestHerdrStartThatNeverGetsReady(t *testing.T) {
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, 1500*time.Millisecond)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	startHerdr(f, "w1:p2", proto.StatusWorking)
	f.reply("pane.list", map[string]any{"panes": []any{map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2"}}})
	f.setScreen("w1:p2", "Claude Code\n loading…")
	began := time.Now()
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "did not become ready within 1.5s of starting") || rpcCode(err) != proto.CodeAgentStartStalled {
		t.Fatalf("start: %v", err)
	}
	if d := time.Since(began); d > 10*time.Second {
		t.Errorf("waited %s, not claudeReadyWait after claude came up", d)
	}
	if data := refusalData(t, err); !strings.Contains(data, `"screen":"Claude Code\n loading…"`) || !strings.Contains(data, `"pane":"w1:p2"`) ||
		!strings.Contains(data, `"kept_until":`) {
		t.Errorf("data %s", data)
	}
	if got := f.trace("tab.close"); len(got) != 0 {
		t.Errorf("closed the tab of a claude that stopped: %v", got)
	}
}

// Claude comes up and exits before its input box (a launcher's bad config): herdr stops naming it and the pane's shell
// is back in front, so the start fails within claudeGoneWait of that, not claudeReadyWait later, with the pane's last
// lines, and closes its tab (#1447).
func TestHerdrStartWhoseClaudeExitsFailsAtOnce(t *testing.T) {
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, time.Minute)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.reply("pane.list", map[string]any{"panes": []any{map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2"}}})
	var mu sync.Mutex
	gets := 0
	f.handle("agent.get", func(map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if gets++; gets <= 2 { // claude runs for about a second
			return map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusUnknown, "pane_id": "w1:p2"}}
		}
		return map[string]any{"agent": map[string]any{"agent": "", "agent_status": "unknown", "pane_id": "w1:p2"}}
	})
	f.reply("pane.process_info", foreground([2]string{"zsh", "-zsh"}))
	f.setScreen("w1:p2", "Invalid private gateway configuration; stopped\n~/proj %")
	began := time.Now()
	_, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "prompt": "hi"})
	if err == nil || !strings.Contains(err.Error(), "claude exited before showing its input box") ||
		!strings.Contains(err.Error(), "Invalid private gateway configuration") {
		t.Fatalf("start: %v", err)
	}
	if d := time.Since(began); d > 15*time.Second {
		t.Errorf("waited %s for a claude that had exited", d)
	}
	if got := f.trace("tab.close"); len(got) != 1 {
		t.Errorf("closed %v", got)
	}
}

// Herdr loses sight of claude for a moment while the pane still runs something other than a shell (a wrapper between
// two runs of claude): the start keeps waiting and succeeds once claude is back with its input box.
func TestHerdrStartRidesOutABlinkOfClaude(t *testing.T) {
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, 20*time.Second)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	var mu sync.Mutex
	gets := 0
	f.handle("agent.get", func(map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if gets++; gets > 1 && gets <= 12 { // ~5 s without an agent after the first look
			return map[string]any{"agent": map[string]any{"agent": "", "agent_status": "unknown", "pane_id": "w1:p2"}}
		}
		return map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusIdle, "pane_id": "w1:p2",
			"tab_id": "w1:t2", "workspace_id": "w1"}}
	})
	f.reply("pane.process_info", foreground([2]string{"node", "my-claude-wrapper"}))
	f.setScreen("w1:p2", screenFixture(t, "claude-2.1.312-empty-162.txt"))
	if _, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1", "prompt": "hi"}); err != nil {
		t.Fatalf("a wrapper between two runs of claude failed the start: %v", err)
	}
}

// A machine whose Claude Code never ran (2.1.318): the start answers the style picker, then the security notes, then the
// folder's trust dialog, and comes back with the agent ready. Before, the picker's unnumbered choices were not recognised
// and the start timed out on it; nothing answered the security notes after it either.
func TestHerdrStartThroughTheFirstRunScreens(t *testing.T) {
	n, f, home := herdrNode(t)
	withStartWaits(n, time.Minute, 20*time.Second)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	startHerdr(f, "w1:p2", proto.StatusIdle)
	f.reply("pane.list", map[string]any{"panes": []any{map[string]any{"pane_id": "w1:p2", "tab_id": "w1:t2"}}})
	f.screenSteps("w1:p2", isKeys(),
		screenFixture(t, "claude-2.1.318-theme-picker-120.txt"),
		screenFixture(t, "claude-2.1.318-security-notes-120.txt"),
		screenFixture(t, "claude-2.1.312-trust-162.txt"),
		screenFixture(t, "claude-2.1.312-empty-162.txt"))
	if _, err := request(t, n, "session.start", map[string]any{"cwd": home, "workspace_id": "w1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	assertTrace(t, f.trace("pane.send_keys"), []string{
		sendKeys("w1:p2", "enter"),         // the style picker: its default
		sendKeys("w1:p2", "enter"),         // the security notes
		sendKeys("w1:p2", "down", "enter"), // the trust dialog, cursor on "No, exit"
	})
}

// Resuming a session that is already live does nothing in herdr but refresh the layout; herdr not running fails
// before anything is created.
func TestHerdrStartOfALiveSessionAndWithoutHerdr(t *testing.T) {
	n, f, home := herdrNode(t)
	n.starts.add(startID, &startRec{done: make(chan struct{}), session: proto.Session{SessionID: startID, Starting: true}})
	n.starts.finish(startID, errors.New("claude did not become ready"))
	seedAgents(t, n, f, claudeAgent(startID, proto.StatusIdle, "w1:p1", "w1:t1", "w1", home, ""))
	res, err := request(t, n, "session.start", map[string]any{"cwd": home, "resume_session_id": startID})
	if s := decodeSessions(t, []any{res["session"]}); err != nil || s[0].PaneID != "w1:p1" {
		t.Fatalf("%+v %v", s, err)
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})
	if err := n.starts.wait(t.Context(), startID); err != nil {
		t.Fatalf("a running session kept a failed start: %v", err)
	}

	f.reset()
	f.reply("workspace.list", herdrFail{Code: "internal", Message: "down"})
	// Refused with nothing sent, in words the app can show (not herdr's own error).
	if _, err := request(t, n, "session.start", map[string]any{"cwd": home}); !isRefusal(err, engineMissing) || !strings.Contains(err.Error(), "没在运行") {
		t.Fatalf("err %v", err)
	}
	assertTrace(t, f.trace(), []string{call("workspace.list", nil)})
}

// session.send to an agent herdr does not prompt (agent.prompt: *_not_found, as for a compatible CLI): the text is typed and
// enter pressed; other errors are returned as they are. A Codex pane is checked with one colored read of its end
// (codexReady: a pane scrolled back reads the same).
func TestHerdrSendFallsBackToTyping(t *testing.T) {
	n, f, _ := herdrNode(t)
	f.reply("pane.process_info", foreground([2]string{"zsh", "-zsh"}, [2]string{"node", "traex"}))
	seedAgents(t, n, f,
		agentOf("codex", sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/c", ""),
		agentOf("codex", sidB, proto.StatusIdle, "w1:p2", "w1:t2", "w1", "/tmp/c", ""),
		agentOf("codex", sidC, proto.StatusIdle, "w1:p3", "w1:t3", "w1", "/tmp/c", ""))
	f.handle("agent.prompt", func(p map[string]any) any {
		switch p["target"] {
		case "w1:p1":
			return herdrFail{Code: "agent_not_found", Message: "no agent in pane"}
		case "w1:p2":
			return herdrFail{Code: "pane_not_found", Message: "no pane"}
		}
		return herdrFail{Code: "internal", Message: "boom"}
	})
	send := func(id, text string) error {
		_, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": text})
		return err
	}
	if err := send(sidA, "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	if err := send(sidB, "x"); err != nil {
		t.Fatal(err)
	}
	if err := send(sidC, "y"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err %v", err)
	}
	assertTrace(t, f.trace(), []string{
		readCodexEnd("w1:p1"),
		call("agent.prompt", map[string]any{"target": "w1:p1", "text": "line one\nline two"}),
		call("pane.process_info", map[string]any{"pane_id": "w1:p1"}),
		call("pane.send_text", map[string]any{"pane_id": "w1:p1", "text": pasteStart + "line one\nline two" + pasteEnd}),
		sendKeys("w1:p1", "enter"),
		readCodexEnd("w1:p2"),
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "x"}),
		call("pane.process_info", map[string]any{"pane_id": "w1:p2"}),
		call("pane.send_text", map[string]any{"pane_id": "w1:p2", "text": pasteStart + "x" + pasteEnd}),
		sendKeys("w1:p2", "enter"),
		readCodexEnd("w1:p3"),
		call("agent.prompt", map[string]any{"target": "w1:p3", "text": "y"}),
	})
}

// session.send to a claude: the box is checked first (a draft refuses the send, nothing typed); after the prompt a
// box still showing it after the grace period gets one Enter.
func TestHerdrSendChecksThePaneAndPressesEnterForAStuckPrompt(t *testing.T) {
	withSubmitGrace(t, 200*time.Millisecond)
	n, f, _ := herdrNode(t)
	seedAgents(t, n, f,
		claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/s", ""),
		claudeAgent(sidB, proto.StatusIdle, "w1:p2", "w1:t2", "w1", "/tmp/s", ""))
	typedTxt, typedANSI := screenFixture(t, "claude-2.1.312-typed-162.txt"), screenFixture(t, "claude-2.1.312-typed-162.ansi")
	f.setScreens("w1:p1", typedTxt, typedANSI)
	_, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "hello"})
	if code, _ := refusalOf(t, err); code != "input_draft" {
		t.Fatalf("send over a draft: %v", err)
	}
	assertTrace(t, f.trace(), []string{readANSI("w1:p1")})

	f.reset()
	f.setScreens("w1:p2", screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	f.after(func(c herdrCall) {
		if c.Method == "agent.prompt" { // the box keeps showing what was prompted ("fix the flaky test")
			f.setScreens("w1:p2", typedTxt, typedANSI)
		}
	})
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidB, "text": "fix the flaky test"}); err != nil {
		t.Fatal(err)
	}
	assertTrace(t, squeeze(f.trace()), []string{
		readANSI("w1:p2"),
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "fix the flaky test"}),
		readANSI("w1:p2"), // read once, when the grace is over
		sendKeys("w1:p2", "enter"),
	})
}

// session.send to a session that is not running: it is resumed (launcher and directory from what is known of it),
// its pane checked again, then prompted.
func TestHerdrSendResumesAnOfflineSession(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	writeTranscript(t, home, cwd, sidA, time.Now(),
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"hello"}}`)
	f.reply("workspace.list", map[string]any{"workspaces": []any{}})
	f.reply("workspace.create", map[string]any{"workspace": map[string]any{"workspace_id": "w7"},
		"tab": map[string]any{"tab_id": "w7:t1"}, "root_pane": map[string]any{"pane_id": "w7:p1"}})
	startHerdr(f, "w7:p1", proto.StatusIdle)
	f.setScreens("w7:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "go on"}); err != nil {
		t.Fatal(err)
	}
	assertTrace(t, f.trace(), []string{
		call("session.snapshot", nil), // is it running?
		call("workspace.list", nil),
		call("workspace.create", map[string]any{"cwd": cwd, "label": "proj", "focus": false}),
		call("tab.rename", map[string]any{"tab_id": "w7:t1", "label": "agent-1"}),
		markTab("w7:p1", sidA),
		call("pane.send_input", map[string]any{"pane_id": "w7:p1", "keys": []string{"Enter"},
			"text": startCmd("claude", "--resume "+sidA, personaDir(home))}),
		readVisible("w7:p1"), agentGet("w7:p1"),
		readVisible("w7:p1"), agentGet("w7:p1"),
		call("agent.rename", map[string]any{"target": "w7:p1", "name": "j-0d0000"}),
		resumeHeld("w7:p1", sidA, startCmd("claude", "--resume "+sidA, personaDir(home)))[0],
		resumeHeld("w7:p1", sidA, startCmd("claude", "--resume "+sidA, personaDir(home)))[1],
		call("session.snapshot", nil),
		readANSI("w7:p1"), // the pane checked again
		call("agent.prompt", map[string]any{"target": "w7:p1", "text": "go on"}),
		readANSI("w7:p1"), // the box emptied: no enter
	})
}

// A session that is not running and that nothing says which launcher it ran with (started outside Persona) is
// resumed with the launcher the hub sends ("New Agent"'s), not plain claude; the launcher Persona started it with
// still wins over the hub's.
func TestHerdrSendResumesWithTheHubsLauncher(t *testing.T) {
	for _, tc := range []struct {
		name, recorded, want string
	}{
		{"unknown", "", "work"},
		{"recorded", "mine", "mine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			cwd := mkdir(t, filepath.Join(home, "proj"))
			writeTranscript(t, home, cwd, sidA, time.Now(),
				`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"hello"}}`)
			if tc.recorded != "" {
				n.state.put(sidA, sessionState{Launcher: tc.recorded, Cwd: cwd})
			}
			f.reply("workspace.list", map[string]any{"workspaces": []any{}})
			f.reply("workspace.create", map[string]any{"workspace": map[string]any{"workspace_id": "w7"},
				"tab": map[string]any{"tab_id": "w7:t1"}, "root_pane": map[string]any{"pane_id": "w7:p1"}})
			startHerdr(f, "w7:p1", proto.StatusIdle)
			f.setScreens("w7:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
			if _, err := request(t, n, "session.send", map[string]any{"session_id": sidA, "text": "go on", "launcher": "work"}); err != nil {
				t.Fatal(err)
			}
			want := call("pane.send_input", map[string]any{"pane_id": "w7:p1", "keys": []string{"Enter"},
				"text": startCmd(tc.want, "--resume "+sidA, personaDir(home))})
			if !slices.Contains(f.trace(), want) {
				t.Fatalf("resumed with another launcher than %q:\n%s", tc.want, strings.Join(f.trace(), "\n"))
			}
		})
	}
}

// session.keys: pre_keys first (their own send_keys), then the text, then the keys; session.interrupt is esc.
// A session herdr does not have is looked for in one snapshot and refused.
func TestHerdrKeysWithPreKeys(t *testing.T) {
	n, f, _ := herdrNode(t)
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/k", ""))
	if _, err := request(t, n, "session.keys", map[string]any{"session_id": sidA, "pre_keys": []string{"down", "down"},
		"text": "other answer", "keys": []string{"enter"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := request(t, n, "session.interrupt", map[string]any{"session_id": sidA}); err != nil {
		t.Fatal(err)
	}
	if _, err := request(t, n, "session.keys", map[string]any{"session_id": sidA}); err != nil { // nothing to send
		t.Fatal(err)
	}
	assertTrace(t, f.trace(), []string{
		sendKeys("w1:p1", "down", "down"),
		call("pane.send_text", map[string]any{"pane_id": "w1:p1", "text": "other answer"}),
		sendKeys("w1:p1", "enter"),
		sendKeys("w1:p1", "esc"),
	})

	f.reset()
	if _, err := request(t, n, "session.keys", map[string]any{"session_id": sidB, "keys": []string{"enter"}}); err == nil {
		t.Fatal("keys to a session that is not running")
	}
	assertTrace(t, f.trace(), []string{call("session.snapshot", nil)})
}

// session.close: ctrl+c twice, then the pane alone in a split tab, else the whole tab. When the panes cannot be counted
// only the pane goes (#4873): herdr closes the tab itself when it was the tab's last pane, and the other panes of a
// split tab stay. A session Persona started that is gone is already closed; one herdr never had cannot be closed.
func TestHerdrClose(t *testing.T) {
	n, f, _ := herdrNode(t)
	seedAgents(t, n, f,
		claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/c", ""),
		claudeAgent(sidB, proto.StatusIdle, "w1:p2", "w1:t2", "w1", "/tmp/c", ""),
		claudeAgent(sidC, proto.StatusIdle, "w2:p1", "w2:t1", "w2", "/tmp/c", ""))
	f.handle("pane.list", func(p map[string]any) any {
		if p["workspace_id"] == "w2" {
			return herdrFail{Code: "internal", Message: "boom"}
		}
		return map[string]any{"panes": []any{
			map[string]any{"tab_id": "w1:t1"}, map[string]any{"tab_id": "w1:t1"}, map[string]any{"tab_id": "w1:t2"}}}
	})
	for _, id := range []string{sidA, sidB, sidC} {
		if _, err := request(t, n, "session.close", map[string]any{"session_id": id}); err != nil {
			t.Fatal(err)
		}
	}
	assertTrace(t, f.trace(), []string{
		sendKeys("w1:p1", "ctrl+c"), sendKeys("w1:p1", "ctrl+c"),
		call("pane.list", map[string]any{"workspace_id": "w1"}),
		call("pane.close", map[string]any{"pane_id": "w1:p1"}),
		sendKeys("w1:p2", "ctrl+c"), sendKeys("w1:p2", "ctrl+c"),
		call("pane.list", map[string]any{"workspace_id": "w1"}),
		call("tab.close", map[string]any{"tab_id": "w1:t2"}),
		sendKeys("w2:p1", "ctrl+c"), sendKeys("w2:p1", "ctrl+c"),
		call("pane.list", map[string]any{"workspace_id": "w2"}),
		call("pane.close", map[string]any{"pane_id": "w2:p1"}), // pane count unknown: only the pane
	})

	f.reset()
	const gone = "0d000000-0000-4000-8000-0000000000ee"
	rememberStarted(n, gone)
	if _, err := request(t, n, "session.close", map[string]any{"session_id": gone}); err != nil {
		t.Fatalf("gone already: %v", err)
	}
	if _, err := request(t, n, "session.close", map[string]any{"session_id": "0d000000-0000-4000-8000-0000000000ff"}); err == nil {
		t.Fatal("closed a session herdr never had")
	}
	assertTrace(t, f.trace(), nil)
}

// session.queue.edit: up pulls the queue into the box, ctrl+u + backspace once per line clears it, a last ctrl+u,
// then the kept messages are prompted again in the order given (an index out of range is skipped).
func TestHerdrEditQueue(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := "/tmp/queue"
	writeTranscript(t, home, cwd, sidA, time.Now(),
		`{"type":"queue-operation","operation":"enqueue","content":"first"}`,
		`{"type":"queue-operation","operation":"enqueue","content":"second\nhas two lines"}`,
		`{"type":"queue-operation","operation":"enqueue","content":"third"}`)
	writeTranscript(t, home, cwd, sidB, time.Now(), `{"type":"custom-title","customTitle":"x"}`)
	seedAgents(t, n, f,
		claudeAgent(sidA, proto.StatusWorking, "w1:p1", "w1:t1", "w1", cwd, ""),
		claudeAgent(sidB, proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, ""))
	res, err := request(t, n, "session.queue.edit", map[string]any{"session_id": sidA, "keep": []int{2, 0, 7}})
	if err != nil || strings.Join(strs(res["queue"]), "|") != "first|second\nhas two lines|third" {
		t.Fatalf("queue %v %v", res, err)
	}
	assertTrace(t, f.trace(), []string{
		sendKeys("w1:p1", "up"),
		sendKeys("w1:p1", "ctrl+u", "backspace"), sendKeys("w1:p1", "ctrl+u", "backspace"),
		sendKeys("w1:p1", "ctrl+u", "backspace"), sendKeys("w1:p1", "ctrl+u", "backspace"),
		sendKeys("w1:p1", "ctrl+u"),
		call("agent.prompt", map[string]any{"target": "w1:p1", "text": "third"}),
		call("agent.prompt", map[string]any{"target": "w1:p1", "text": "first"}),
	})

	// Nothing queued: no keys.
	f.reset()
	if _, err := request(t, n, "session.queue.edit", map[string]any{"session_id": sidB}); err == nil {
		t.Fatal("nothing queued")
	}
	assertTrace(t, f.trace(), nil)
}

func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, s := range l {
			out = append(out, str(s))
		}
	}
	return out
}

// space.rename renames the workspace; session.rename renames the tab of a live session and sends /rename for the
// title, and writes the title into the transcript of a session that is not running.
func TestHerdrRenames(t *testing.T) {
	n, f, home := herdrNode(t)
	seedAgents(t, n, f, claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", "/tmp/r", ""))
	if _, err := request(t, n, "space.rename", map[string]any{"workspace_id": "w1", "name": "  backend "}); err != nil {
		t.Fatal(err)
	}
	if _, err := request(t, n, "space.rename", map[string]any{"workspace_id": "w1", "name": " "}); err == nil {
		t.Fatal("empty name")
	}
	if _, err := request(t, n, "session.rename", map[string]any{"session_id": sidA, "tab_name": " review ",
		"title": "  fix   the\nbuild "}); err != nil {
		t.Fatal(err)
	}
	assertTrace(t, f.trace(), []string{
		call("workspace.rename", map[string]any{"workspace_id": "w1", "label": "backend"}),
		call("tab.rename", map[string]any{"tab_id": "w1:t1", "label": "review"}),
		call("agent.prompt", map[string]any{"target": "w1:p1", "text": "/rename fix the build"}),
	})

	// Not running: the tab cannot be renamed; the title goes into the transcript, no herdr call.
	f.reset()
	p := writeTranscript(t, home, "/tmp/r", sidB, time.Now(), `{"type":"custom-title","customTitle":"old"}`)
	if _, err := request(t, n, "session.rename", map[string]any{"session_id": sidB, "tab_name": "review"}); err == nil {
		t.Fatal("renamed the tab of a session that is not running")
	}
	if _, err := request(t, n, "session.rename", map[string]any{"session_id": sidB, "title": "new title"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasSuffix(string(b), `{"customTitle":"new title","sessionId":"`+sidB+`","type":"custom-title"}`+"\n") {
		t.Errorf("transcript %s", b)
	}
	assertTrace(t, f.trace(), nil)
}

// markTab is the call that marks a tab the node opened for session id as Persona's (personatab.go).
func markTab(pane, id string) string {
	return call("pane.report_metadata", map[string]any{"pane_id": pane, "source": "persona", "tokens": map[string]any{personaTabToken: id}})
}
