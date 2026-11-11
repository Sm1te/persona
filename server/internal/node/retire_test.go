package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Retiring a finished task's agent and resuming it on the next message (retire.go).

// withoutAgentReports drops herdr's agent reports from a trace. The node reports a pane's agent — its state, and which
// session runs in it — in the background (herdrevents.go, herdrresume.go), so one can land between any two calls of
// whatever is being driven; a test that lists the calls it expects should not depend on where it fell (#1940).
func withoutAgentReports(trace []string) []string {
	return slices.DeleteFunc(slices.Clone(trace), func(c string) bool {
		return strings.HasPrefix(c, "pane.report_agent")
	})
}

// session.retire (what a hub before #4754 asks for a finished or idle agent) stops a still Claude and keeps its pane,
// with what it was started with, and refuses one that works or waits; one not running is no error.
func TestHerdrRetire(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	for _, id := range []string{sidA, sidB, sidC} {
		// A finished turn: a transcript with a prompt and nothing after it, written just now, is a turn under way
		// whatever herdr says (claudeStatus, #2069).
		writeTranscript(t, home, cwd, id, time.Now(), `{"type":"user","cwd":"`+cwd+`","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"uuid":"a1","parentUuid":"u1"}`,
			`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":1000}`)
	}
	n.state.put(sidA, sessionState{Launcher: "cc", Cwd: cwd, Model: "opus"})
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		claudeAgent(sidA, proto.StatusDone, "w1:p1", "w1:t1", "w1", cwd, ""),
		claudeAgent(sidB, proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, ""),
		claudeAgent(sidC, proto.StatusBlocked, "w1:p3", "w1:t3", "w1", cwd, ""),
		claudeAgent(sidD, proto.StatusIdle, "w1:p4", "w1:t4", "w1", cwd, ""), // no transcript
	}, nil))
	listSessions(t, n)
	// sidA was started by Persona: the refresh gives herdr its resume command in the background (backfillResume),
	// once. Wait for it, or it lands among retire's calls.
	waitCalls(t, f, "pane.report_agent_session", 1)
	f.reset()

	for _, id := range []string{sidB, sidC} {
		_, err := request(t, n, "session.retire", map[string]any{"session_id": id})
		if code, _ := refusalOf(t, err); code != notRetirable {
			t.Errorf("%s: code %q", id, code)
		}
	}
	quitsOn(f, "w1:p1", sidA, []any{
		claudeAgent(sidB, proto.StatusWorking, "w1:p2", "w1:t2", "w1", cwd, ""),
		claudeAgent(sidC, proto.StatusBlocked, "w1:p3", "w1:t3", "w1", cwd, ""),
		claudeAgent(sidD, proto.StatusIdle, "w1:p4", "w1:t4", "w1", cwd, ""),
	}, shellPane("w1:p1", "w1:t1", "w1", cwd, sidA))
	res, err := request(t, n, "session.retire", map[string]any{"session_id": sidA})
	if err != nil || res["closed"] != true {
		t.Fatalf("retire = %v, %v", res, err)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "cc" || st.Cwd != cwd || st.Model != "opus" {
		t.Errorf("state after retire = %+v", st)
	}
	assertTrace(t, withoutAgentReports(f.trace("pane.send_keys", "tab.close", "pane.close")), []string{
		sendKeys("w1:p1", "ctrl+c"), sendKeys("w1:p1", "ctrl+c"),
	})

	f.reset()
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	const gone = "0d000000-0000-4000-8000-0000000000ee"
	res, err = request(t, n, "session.retire", map[string]any{"session_id": gone})
	if err != nil || res["closed"] != false {
		t.Fatalf("not running: %v, %v", res, err)
	}
}

// A message to a Codex Persona started that is no longer running is refused (#4754: a stopped agent takes no
// messages). The user's Resume (session.start) brings it back in a new tab of its directory's workspace, named after
// its title: its launcher's words with the model it ran with, `resume <id>`, and the start's prompt (nothing typed).
func TestHerdrResumeBringsBackAStoppedCodex(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"codex", "-m", "gpt-6"}})
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	day := time.Now()
	dir := mkdir(t, filepath.Join(home, ".codex", "sessions", day.Format("2006"), day.Format("01"), day.Format("02")))
	now := day.UTC().Format(time.RFC3339Nano)
	meta, _ := json.Marshal(map[string]any{"timestamp": now, "type": "session_meta", "payload": map[string]any{"id": startThread, "cwd": cwd}})
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-11-02T10-00-00-"+startThread+".jsonl"), append(meta, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := request(t, n, "session.send", map[string]any{"session_id": startThread, "text": "go on"})
	if code, _ := refusalOf(t, err); code != agentStopped {
		t.Fatalf("a message to a stopped Codex: %v", err)
	}

	n.state.put(startThread, sessionState{Launcher: "cx", Cwd: cwd, Model: "gpt-7"})
	f.reset()
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
	if _, err := request(t, n, "session.start", map[string]any{"resume_session_id": startThread, "prompt": "go on"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"codex", "--model", "gpt-7", "resume", startThread}; strings.Join(file.Argv, " ") != strings.Join(want, " ") ||
		file.Prompt != "go on" || file.Agent != "codex" {
		t.Errorf("ran %q with %q (HERDR_AGENT %q)", file.Argv, file.Prompt, file.Agent)
	}
	if st, _ := n.state.get(startThread); st.Pane != "w1:p2" || st.Launcher != "cx" || st.Model != "gpt-7" {
		t.Errorf("state = %+v", st)
	}
	if a, ok := n.agent(startThread); !ok || a.PaneID != "w1:p2" {
		t.Errorf("the resumed Codex is not its session: %+v %v", a, ok)
	}
	if got := f.trace("tab.create", "pane.send_keys", "agent.prompt", "pane.send_text"); len(got) != 1 ||
		!strings.Contains(got[0], `"workspace_id":"w1"`) {
		t.Errorf("herdr calls %q", got)
	}
}

// On Orca too, retire stops a still agent with Ctrl+C and never closes its terminal, a Codex as well as a Claude: the
// terminal stays, so whether Orca could continue the session does not matter any more (#4754).
func TestOrcaRetireKeepsTheTerminal(t *testing.T) {
	withStop(t, 1, orcaProcsMaxAge+time.Second) // the node reads Orca's processes at most this often
	cn, co, procs := orcaSession(t)
	go func() { // claude quits once Ctrl+C has been pressed twice: its process leaves the terminal
		tick := time.NewTicker(10 * time.Millisecond) // the fake's trace signals nothing: looked at 200 times at most
		defer tick.Stop()
		for range 200 {
			if len(co.Trace("terminal.send")) >= 2 {
				procs.set("tab1:leaf1")
				return
			}
			<-tick.C
		}
	}()
	res, err := request(t, cn, "session.retire", map[string]any{"session_id": sidA})
	if err != nil || res["closed"] != true {
		t.Fatalf("claude on Orca: %v, %v (calls %v)", res, err, co.Trace())
	}
	if c := co.Trace("terminal.close"); len(c) != 0 {
		t.Errorf("closed: %v", c)
	}
}

func TestRetireUsesSessionHostWithRuntimeDefault(t *testing.T) {
	for _, closeTerminal := range []bool{false, true} {
		action := "stop"
		if closeTerminal {
			action = "close"
		}
		t.Run(action, func(t *testing.T) {
			for _, def := range []string{proto.EnginePaseo, proto.EngineHerdr} {
				t.Run(def, func(t *testing.T) {
					withStop(t, 1, 50*time.Millisecond)
					n, f, home := herdrNode(t)
					choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EnginePaseo}, Default: def}
					n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
					cwd := mkdir(t, filepath.Join(home, "proj"))
					writeTranscript(t, home, cwd, sidA, time.Now(), `{"type":"assistant","message":{"role":"assistant","content":"done"},"uuid":"a1"}`)
					n.agents[sidA] = host.Agent{Agent: "claude", PaneID: "w1:p1", TabID: "w1:t1", WorkspaceID: "w1", AgentStatus: proto.StatusDone}
					n.state.put(sidA, sessionState{Pane: "w1:p1", Launcher: "cc", Cwd: cwd})
					n.runtimes = newRuntimeRegistry()
					n.runtimes.known[sidB] = knownSession{runtime: proto.EnginePaseo, label: "Paseo", at: time.Now()}
					n.agents[sidB] = host.Agent{Agent: "claude", PaneID: "w1:p9", AgentStatus: proto.StatusDone}
					n.state.put(sidB, sessionState{Pane: "w1:p9"}) // stale terminal state cannot override runtime ownership
					if !closeTerminal {
						quitsOn(f, "w1:p1", sidA, nil, shellPane("w1:p1", "w1:t1", "w1", cwd, sidA))
					}
					res, err := request(t, n, "session.retire", map[string]any{"session_id": sidA, "close_terminal": closeTerminal})
					if err != nil || res["closed"] != true {
						t.Fatalf("herdr session: %v, %v", res, err)
					}
					if closeTerminal {
						if got := f.trace("tab.close"); len(got) != 1 || !strings.Contains(got[0], `"tab_id":"w1:t1"`) {
							t.Fatalf("herdr close calls: %v", got)
						}
					} else {
						closesNothing(t, f)
					}
					f.reset()
					_, err = request(t, n, "session.retire", map[string]any{"session_id": sidB, "close_terminal": closeTerminal})
					if code, _ := refusalOf(t, err); code != notRetirable {
						t.Fatalf("Paseo session: %v", err)
					}
					if got := f.trace("pane.send_keys", "tab.close"); len(got) != 0 {
						t.Fatalf("Paseo retirement touched terminals: %v", got)
					}
				})
			}
		})
	}
}

func TestRetireHerdrCoreWithRuntimeDefault(t *testing.T) {
	for _, closeTerminal := range []bool{false, true} {
		action := "stop"
		if closeTerminal {
			action = "close"
		}
		t.Run(action, func(t *testing.T) {
			withStop(t, 1, 50*time.Millisecond)
			n, hc := hcSession(t)
			choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdrCore, proto.EnginePaseo}, Default: proto.EnginePaseo}
			n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
			hc.handle("terminal.keys", func(map[string]any) any {
				hc.setTerminals(hcTerminal("t1", "", "", 4243, "zsh", hcCwd, "", "", ""))
				return map[string]any{}
			})
			hc.handle("terminal.kill", func(map[string]any) any {
				k := hcTerminal("t1", "", "", 0, "", hcCwd, "", "", "")
				k["state"] = "finished"
				return k
			})
			res, err := request(t, n, "session.retire", map[string]any{"session_id": sidA, "close_terminal": closeTerminal})
			if err != nil || res["closed"] != true {
				t.Fatalf("herdr-core retirement: %v, %v", res, err)
			}
			if got := hc.trace("terminal.keys"); len(got) != 2 {
				t.Fatalf("interrupt calls: %v", got)
			}
			kills := hc.trace("terminal.kill")
			if closeTerminal {
				if len(kills) != 1 || !strings.Contains(kills[0], `"terminal":"t1"`) {
					t.Fatalf("close calls: %v", kills)
				}
			} else {
				if len(kills) != 0 {
					t.Fatalf("stopping closed terminal: %v", kills)
				}
				if _, live := n.agent(sidA); live {
					t.Fatal("agent still live after stop")
				}
			}
		})
	}
}
