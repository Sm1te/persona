package node

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// A stopped agent's row is read-only (#4754): it can be read and its terminal opened, nothing else; a branch nothing
// has started yet stays writable (its first message starts it).
func TestStoppedAgentRowIsReadOnly(t *testing.T) {
	n, _, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	for _, id := range []string{sidA, sidB} {
		writeTranscript(t, home, cwd, id, time.Now().Add(-time.Hour), `{"type":"custom-title","customTitle":"fix the build"}`,
			`{"type":"user","timestamp":"2026-01-01T00:00:00Z","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
			`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"`+
				strings.Repeat("done ", 600)+`"}]},"uuid":"a1","parentUuid":"u1"}`) // over the 2 KB a listed session has
	}
	n.freshForks = map[string]bool{sidB: true}
	offlineCache.Lock()
	offlineCache.at = time.Time{} // scanned now, not from another test's look
	offlineCache.Unlock()
	rows := byID(n.sessionList(t.Context(), false))
	if s := rows[sidA]; s.Status != proto.StatusOffline || s.Capabilities == nil || s.Capabilities.Send || !s.Capabilities.Read ||
		!s.Capabilities.Terminal || s.ReadOnlyReason != stoppedReason {
		t.Errorf("stopped row = %+v (%+v)", s, s.Capabilities)
	}
	if s := rows[sidB]; s.Capabilities != nil || s.ReadOnlyReason != "" {
		t.Errorf("a fresh branch is read-only: %+v (%+v)", s, s.Capabilities)
	}
}

func TestRetiredAgentRowUsesSessionHost(t *testing.T) {
	for _, def := range []string{proto.EnginePaseo, proto.EngineHerdr} {
		for _, remembered := range []string{"state", "last pane"} {
			t.Run(def+"/"+remembered, func(t *testing.T) {
				withStop(t, 1, 50*time.Millisecond)
				n, f, home := herdrNode(t)
				choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EnginePaseo}, Default: def}
				n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
				cwd := mkdir(t, filepath.Join(home, "proj"))
				writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour),
					`{"type":"user","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
					`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+strings.Repeat("done ", 600)+`"}]},"uuid":"a1","parentUuid":"u1"}`)
				n.agents[sidA] = host.Agent{Agent: "claude", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", AgentStatus: proto.StatusDone}
				if remembered == "state" {
					n.state.put(sidA, sessionState{Pane: "w1:p1", Launcher: "cc", Cwd: cwd})
				} else {
					n.lastPanes = map[string]string{sidA: "w1:p1"}
				}
				quitsOn(f, "w1:p1", sidA, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidA))
				res, err := request(t, n, "session.retire", map[string]any{"session_id": sidA})
				if err != nil || res["closed"] != true {
					t.Fatalf("retire = %v, %v", res, err)
				}
				row := byID(listSessions(t, n))[sidA]
				want := &proto.SessionCapabilities{Read: true, Terminal: true}
				if row.SessionID != sidA || row.Status != proto.StatusOffline || !reflect.DeepEqual(row.Capabilities, want) || row.ReadOnlyReason != stoppedReason {
					t.Fatalf("retired row: status=%q capabilities=%+v reason=%q", row.Status, row.Capabilities, row.ReadOnlyReason)
				}
				closesNothing(t, f)
			})
		}
	}
}

func TestStoppedRuntimeRowIsUnchanged(t *testing.T) {
	for _, def := range []string{proto.EnginePaseo, proto.EngineHerdr} {
		for _, ownership := range []string{"known", "unlisted"} {
			t.Run(def+"/"+ownership, func(t *testing.T) {
				n, _, home := herdrNode(t)
				choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EnginePaseo}, Default: def}
				n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
				cwd := mkdir(t, filepath.Join(home, "proj"))
				writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour),
					`{"type":"user","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
					`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+strings.Repeat("done ", 600)+`"}]},"uuid":"a1","parentUuid":"u1"}`)
				// A runtime takeover can leave the old terminal in the node's state.
				n.state.put(sidA, sessionState{Pane: "w1:p1", Launcher: "cc", Cwd: cwd})
				n.runtimes = newRuntimeRegistry()
				if ownership == "known" {
					n.runtimes.known[sidA] = knownSession{runtime: proto.EnginePaseo, label: "Paseo", at: time.Now()}
				} else {
					n.runtimes.unlisted = map[string]unlistedOf{proto.EnginePaseo: {set: map[string]bool{sidA: true}}}
				}
				row := byID(listSessions(t, n))[sidA]
				if row.SessionID != sidA || row.Status != proto.StatusOffline || row.Capabilities != nil || row.ReadOnlyReason != "" {
					t.Fatalf("runtime history row: status=%q capabilities=%+v reason=%q", row.Status, row.Capabilities, row.ReadOnlyReason)
				}
			})
		}
	}
}

// keptAgent sets up a stopped Claude whose tab is still open: its pane marked for it, only a zsh in front, in cwd.
func keptAgent(t *testing.T) (*Node, *fakeHerdr, string) {
	t.Helper()
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour),
		`{"type":"user","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`)
	n.state.put(sidA, sessionState{Launcher: "claude", Cwd: cwd})
	f.reply("session.snapshot", snapshot(nil, nil, nil, []any{shellPane("w1:p1", "w1:t1", "w1", cwd, sidA)}))
	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": 1, "name": "zsh", "argv0": "-zsh"}}}})
	listSessions(t, n)
	f.reset()
	return n, f, cwd
}

// The terminal of a stopped agent is the shell it left: the terminal calls find its pane (not send, which refuses).
func TestStoppedAgentTerminalIsTheKeptShell(t *testing.T) {
	n, _, _ := keptAgent(t)
	if pane, err := n.terminalPane(t.Context(), sidA); err != nil || pane != "w1:p1" {
		t.Fatalf("terminal pane = %q, %v", pane, err)
	}
	if _, err := n.paneFor(t.Context(), sidA); err == nil {
		t.Error("send would type into the kept shell")
	}
}

// The user's Resume (session.start with resume_session_id) runs claude --resume in the shell the agent left, no new
// tab, when that shell is at rest in the session's directory; one that moved elsewhere gets a new tab instead.
func TestResumeRunsInTheKeptShell(t *testing.T) {
	n, f, cwd := keptAgent(t)
	n.claudeStart, n.claudeReady = 200*time.Millisecond, 200*time.Millisecond // the fake shell never runs claude
	_, _ = request(t, n, "session.start", map[string]any{"resume_session_id": sidA})
	if got := f.trace("tab.create", "workspace.create"); len(got) != 0 {
		t.Errorf("opened a tab: %q", got)
	}
	var typed []string
	for _, c := range f.calls() {
		if c.Method == "pane.send_input" {
			typed = append(typed, str(c.Params["pane_id"])+" "+str(c.Params["text"]))
		}
	}
	if len(typed) == 0 || !strings.HasPrefix(typed[0], "w1:p1 ") || !strings.Contains(typed[0], "--resume "+sidA) {
		t.Fatalf("typed %q", typed)
	}
	if got := f.trace("tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("closed the kept terminal: %q", got)
	}

	// The shell moved to another directory: claude --resume would not find the session there.
	n2, f2, _ := keptAgent(t)
	f2.reply("session.snapshot", snapshot(nil, nil, nil, []any{shellPane("w1:p1", "w1:t1", "w1", filepath.Dir(cwd), sidA)}))
	listSessions(t, n2)
	if ctx := n2.inKeptTerminal(t.Context(), sidA, cwd); adoptedPane(ctx) != nil {
		t.Error("resumed in a shell that left the session's directory")
	}
}

// panes.list labels the terminal a stopped agent left with its session; "Close finished terminals"
// (panes.close_finished) lists those as they are now (preview: nothing closed), closes only the ones the user
// confirmed when it names them, and never a plain shell the user opened.
func TestCloseFinishedTerminals(t *testing.T) {
	n, f, cwd := keptAgent(t)
	f.reply("session.snapshot", snapshot(nil, nil, nil, []any{
		shellPane("w1:p1", "w1:t1", "w1", cwd, sidA), shellPane("w1:p2", "w1:t2", "w1", cwd, "")}))
	listSessions(t, n)
	res, err := request(t, n, "panes.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if s := string(b); !strings.Contains(s, `"pane":"w1:p1"`) || !strings.Contains(s, `"agent_session":"`+sidA+`"`) ||
		strings.Count(s, `"agent_session"`) != 1 {
		t.Fatalf("panes.list = %s", s)
	}
	f.reset()

	res, err = request(t, n, "panes.close_finished", map[string]any{"preview": true})
	if b, _ := json.Marshal(res["closed"]); err != nil || !strings.Contains(string(b), `"pane":"w1:p1"`) || strings.Contains(string(b), "w1:p2") {
		t.Fatalf("preview = %v, %v", res, err)
	}
	res, err = request(t, n, "panes.close_finished", map[string]any{"panes": "w1:p9"})
	if b, _ := json.Marshal(res["closed"]); err != nil || string(b) != "[]" {
		t.Fatalf("not confirmed = %v, %v", res, err)
	}
	closesNothing(t, f)

	res, err = request(t, n, "panes.close_finished", map[string]any{"panes": "w1:p1"})
	if b, _ := json.Marshal(res["closed"]); err != nil || !strings.Contains(string(b), `"pane":"w1:p1"`) {
		t.Fatalf("close = %v, %v", res, err)
	}
	if got := f.trace("tab.close", "pane.close"); len(got) != 1 || !strings.Contains(got[0], "w1:t1") {
		t.Errorf("closed %q", got)
	}
}

// panes.list names the program in front of each pane with no agent (#4754: plain terminals in the apps' lists),
// asking the host, never reading the screen; an agent's pane is not asked.
func TestPanesListNamesTheProgramInFront(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")},
		[]any{shellPane("w1:p2", "w1:t2", "w1", cwd, "")}))
	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": 1, "name": "zsh", "argv0": "-zsh"}, map[string]any{"pid": 2, "name": "vim", "argv0": "vim"}}}})
	listSessions(t, n)
	f.reset()
	res, err := request(t, n, "panes.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if s := string(b); !strings.Contains(s, `"foreground":"vim"`) || strings.Count(s, `"foreground"`) != 1 {
		t.Fatalf("panes.list = %s", s)
	}
	if got := f.trace("pane.read"); len(got) != 0 {
		t.Errorf("read a screen: %q", got)
	}
}
