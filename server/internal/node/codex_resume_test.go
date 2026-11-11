package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Resuming a Codex thread nothing runs (codex_resume.go).

// codexHistoryNode is a herdr node whose ~/.codex has one rollout, of a Codex CLI started outside Persona in cwd and
// since closed: listed as a Codex history row.
func codexHistoryNode(t *testing.T) (*Node, *fakeHerdr, string) {
	t.Helper()
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	day := time.Now()
	dir := mkdir(t, filepath.Join(home, ".codex", "sessions", day.Format("2006"), day.Format("01"), day.Format("02")))
	meta, _ := json.Marshal(map[string]any{"timestamp": day.UTC().Format(time.RFC3339Nano), "type": "session_meta",
		"payload": map[string]any{"id": startThread, "cwd": cwd, "originator": "codex-tui", "source": "cli"}})
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-11-02T10-00-00-"+startThread+".jsonl"), append(meta, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	n.codices = newCodexRegistry()
	n.codices.scan = func() []codexProcess { return nil }
	n.codices.checked = func() ([]codexProcess, bool) { return nil, true }
	n.configureCodex(proto.CodexConfig{}) // ~/.codex is the legacy home: its thread ids are the session ids
	return n, f, cwd
}

// resumeFile answers the tab the resume opens: it records what codex-start was given and lists the Codex in its pane.
func resumeFile(f *fakeHerdr, cwd string) func() codexStartFile {
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	var mu sync.Mutex
	var file codexStartFile
	f.after(func(c herdrCall) {
		if c.Method != "pane.send_input" {
			return
		}
		if m := startFileRE.FindStringSubmatch(str(c.Params["text"])); m != nil {
			b, _ := os.ReadFile(m[1])
			mu.Lock()
			_ = json.Unmarshal(b, &file)
			mu.Unlock()
			f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", "", proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
		}
	})
	return func() codexStartFile { mu.Lock(); defer mu.Unlock(); return file }
}

// A Codex history row nothing has open is a stopped agent (#4754): read-only, with its terminal page offered for
// Resume, and a message to it is refused with nothing opened. While a process holds its rollout it stays view only, as
// before.
func TestCodexHistoryIsReadOnlyWhenStopped(t *testing.T) {
	n, f, _ := codexHistoryNode(t)
	s, ok := byID(listSessions(t, n))[startThread]
	if !ok || s.SourceKind != "codex_history" || s.Capabilities == nil || s.Capabilities.Send || !s.Capabilities.Terminal ||
		s.ReadOnlyReason != stoppedReason {
		t.Fatalf("history row = %+v (%+v)", s, s.Capabilities)
	}

	n.codices.mu.Lock()
	n.codices.liveRollouts = map[string]string{startThread: codexHolderCLI}
	n.codices.mu.Unlock()
	if s := byID(listSessions(t, n))[startThread]; s.Capabilities.Send || s.ReadOnlyReason == stoppedReason {
		t.Errorf("held open by a Codex CLI: %+v", s)
	}
	_, err := request(t, n, "session.send", map[string]any{"session_id": startThread, "text": "go on", "launcher": "claude"})
	if code, _ := refusalOf(t, err); code != "codex_read_only" {
		t.Errorf("held open: %v", err)
	}
	n.codices.mu.Lock()
	n.codices.liveRollouts = nil
	n.codices.mu.Unlock()

	f.reset()
	_, err = request(t, n, "session.send", map[string]any{"session_id": startThread, "text": "go on", "launcher": "claude"})
	if code, _ := refusalOf(t, err); code != agentStopped {
		t.Fatalf("a message to a stopped Codex: %v", err)
	}
	if got := f.trace("tab.create", "pane.send_input"); len(got) != 0 {
		t.Errorf("resumed it: %q", got)
	}
}

// session.start with resume brings back a Codex thread nothing runs, without a message (`codex resume <id>` alone),
// through the launcher it was given when that one runs Codex.
func TestCodexHistoryResumesOnAStart(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"codex", "-m", "gpt-6"}})
	n, f, cwd := codexHistoryNode(t)
	listSessions(t, n)
	f.reset()
	got := resumeFile(f, cwd)
	res, err := request(t, n, "session.start", map[string]any{"resume_session_id": startThread, "launcher": "cx"})
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := res["session"].(map[string]any); s == nil || s["session_id"] != startThread || s["pane_id"] != "w1:p2" {
		t.Errorf("session = %v", res["session"])
	}
	if file := got(); strings.Join(file.Argv, " ") != "codex -m gpt-6 resume "+startThread || file.Prompt != "" {
		t.Errorf("ran %q with %q", file.Argv, file.Prompt)
	}
	if st, _ := n.state.get(startThread); st.Launcher != "cx" || st.Cwd != cwd {
		t.Errorf("state = %+v", st)
	}
}

// A Codex App thread (originator "Codex Desktop") is not resumed in a terminal: the App owns it.
func TestCodexDesktopThreadIsNotResumedInATerminal(t *testing.T) {
	n, _, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	day := time.Now()
	dir := mkdir(t, filepath.Join(home, ".codex", "sessions", day.Format("2006"), day.Format("01"), day.Format("02")))
	meta, _ := json.Marshal(map[string]any{"timestamp": day.UTC().Format(time.RFC3339Nano), "type": "session_meta",
		"payload": map[string]any{"id": startThread, "cwd": cwd, "originator": "Codex Desktop", "source": "vscode"}})
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-11-02T10-00-00-"+startThread+".jsonl"), append(meta, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	n.codices = newCodexRegistry()
	n.codices.scan = func() []codexProcess { return nil }
	n.configureCodex(proto.CodexConfig{})
	if s := byID(listSessions(t, n))[startThread]; s.Capabilities == nil || s.Capabilities.Send {
		t.Errorf("a Codex App thread is sendable: %+v", s)
	}
	_, err := request(t, n, "session.start", map[string]any{"resume_session_id": startThread})
	if code, _ := refusalOf(t, err); code != "codex_read_only" {
		t.Errorf("resume of a Codex App thread: %v", err)
	}
}

// The holders the list shows are up to 30 seconds old: a resume (the user's Resume) checks the processes again first, and a Codex opened
// on the computer since keeps it from starting a second writer.
func TestCodexResumeChecksHoldersAgain(t *testing.T) {
	n, f, _ := codexHistoryNode(t)
	listSessions(t, n)
	path := n.codexPath(startThread)
	n.codices.mu.Lock()
	n.codices.checked = func() ([]codexProcess, bool) { return []codexProcess{{pid: os.Getpid()}}, true }
	n.codices.mu.Unlock()
	f.reset()
	// This test process holds the rollout open: it is "a Codex" that has it open now.
	h, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	_, err = request(t, n, "session.start", map[string]any{"resume_session_id": startThread})
	if code, _ := refusalOf(t, err); code != codexResumeRefused {
		t.Fatalf("resumed a thread held open right now: %v", err)
	}
	if got := f.trace("tab.create"); len(got) != 0 {
		t.Errorf("opened a tab: %q", got)
	}
}

// A resume whose Codex did not show in its tab in time may still come up: the next Resume does not start another
// for a while, and what it was resumed with is kept.
func TestCodexUnconfirmedResumeIsNotStartedAgain(t *testing.T) {
	n, f, cwd := codexHistoryNode(t)
	listSessions(t, n)
	old := codexStartWait
	codexStartWait = 300 * time.Millisecond
	t.Cleanup(func() { codexStartWait = old; codexResumes.Delete(startThread) })
	f.reset()
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	if _, err := request(t, n, "session.start", map[string]any{"resume_session_id": startThread}); err == nil {
		t.Fatal("a Codex that never showed was confirmed")
	}
	if st, _ := n.state.get(startThread); st.Launcher != "codex" || st.Cwd != cwd || st.Pane != "w1:p2" {
		t.Errorf("state after an unconfirmed resume = %+v", st)
	}
	_, err := request(t, n, "session.start", map[string]any{"resume_session_id": startThread})
	if code, _ := refusalOf(t, err); code != codexResumeRefused {
		t.Fatalf("second resume: %v", err)
	}
	if got := f.trace("tab.create"); len(got) != 1 {
		t.Errorf("tabs opened: %d", len(got))
	}
}

// A thread started outside Persona is not resumed through a launcher on another app-server or another Codex home.
func TestCodexResumeLauncherStaysInTheHome(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"codex", "-m", "gpt-6"}, "rem": {"codex", "--remote", "ws://127.0.0.1:1"},
		"home": {"CODEX_HOME=/elsewhere", "codex"}})
	n, _, _ := codexHistoryNode(t)
	ctx := t.Context()
	for launcher, want := range map[string]string{"": "codex", "claude": "codex", "cx": "cx", "rem": "codex", "home": "codex", "nope": "codex"} {
		if got := n.codexResumeLauncher(ctx, launcher); got != want {
			t.Errorf("%q: %q, want %q", launcher, got, want)
		}
	}
}

// Processes that could not be listed (ps timed out, /proc unreadable) are not "no Codex holds it": nothing resumes.
func TestCodexResumeRefusedWhenProcessesCannotBeListed(t *testing.T) {
	n, f, _ := codexHistoryNode(t)
	listSessions(t, n)
	n.codices.mu.Lock()
	n.codices.checked = func() ([]codexProcess, bool) { return nil, false }
	n.codices.mu.Unlock()
	f.reset()
	_, err := request(t, n, "session.start", map[string]any{"resume_session_id": startThread})
	if code, _ := refusalOf(t, err); code != codexResumeRefused {
		t.Fatalf("resumed without knowing who holds it: %v", err)
	}
	if got := f.trace("tab.create"); len(got) != 0 {
		t.Errorf("opened a tab: %q", got)
	}
}

// An explicit resume host must reach both session.start's guard and the locked resume guard.
func TestCodexResumeUsesRequestedHost(t *testing.T) {
	for _, direct := range []bool{false, true} {
		entry := "session.start"
		if direct {
			entry = "resumeCodexSession"
		}
		t.Run(entry, func(t *testing.T) {
			for _, tc := range []struct {
				name, pane, def, explicit string
				orca, wantResume          bool
			}{
				{"orca_explicit_herdr", "orca:tab1:leaf1", "herdr", "herdr", true, true},
				{"disabled_host_explicit_herdr", "orca:tab1:leaf1", "paseo", "herdr", false, true},
				{"orca_without_explicit_host", "orca:tab1:leaf1", "herdr", "", true, false},
				{"disabled_host_without_explicit_host", "orca:tab1:leaf1", "paseo", "", false, false},
				{"remembered_herdr_without_explicit_host", "w1:p1", "paseo", "", false, true},
				{"explicit_paseo", "w1:p1", "herdr", "paseo", false, false},
			} {
				if tc.explicit == "paseo" && !direct {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					withCodexLaunchers(t, map[string][]string{"cx": {"codex", "-m", "gpt-6"}})
					n, f, cwd := codexHistoryNode(t)
					listSessions(t, n)
					choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EnginePaseo}, Default: tc.def}
					if tc.orca {
						choice.Hosts = append(choice.Hosts, proto.EngineOrca)
						n.hosts = host.NewRegistry(n.hosts.Get(host.Herdr), &launchHost{setHost: newSetHost(host.Orca)})
					}
					n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
					n.state.put(startThread, sessionState{Pane: tc.pane})
					f.reset()
					got := resumeFile(f, cwd)
					var err error
					if direct {
						var session *proto.Session
						session, err = n.resumeCodexSession(withStartHost(context.Background(), tc.explicit), startThread, "cx", "", "")
						if err == nil && (session == nil || session.SessionID != startThread || session.PaneID != "w1:p2") {
							t.Fatalf("resumed session = %+v", session)
						}
					} else {
						var res map[string]any
						res, err = request(t, n, "session.start", map[string]any{"resume_session_id": startThread, "launcher": "cx", "host": tc.explicit})
						if err == nil {
							session, _ := res["session"].(map[string]any)
							if session["session_id"] != startThread || session["pane_id"] != "w1:p2" {
								t.Fatalf("resumed session = %v", session)
							}
						}
					}
					if !tc.wantResume {
						want := "codex_read_only"
						if direct {
							want = codexResumeRefused
						}
						if code, _ := refusalOf(t, err); code != want {
							t.Fatalf("refusal = %v, want %s", err, want)
						}
						if calls := f.trace("tab.create", "pane.send_input"); len(calls) != 0 {
							t.Fatalf("refused resume touched terminal: %v", calls)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if file := got(); strings.Join(file.Argv, " ") != "codex -m gpt-6 resume "+startThread || file.Prompt != "" {
						t.Fatalf("ran %q with %q", file.Argv, file.Prompt)
					}
					if st, _ := n.state.get(startThread); st.Pane != "w1:p2" || st.Launcher != "cx" || st.Cwd != cwd {
						t.Fatalf("state = %+v", st)
					}
				})
			}
		})
	}
}
