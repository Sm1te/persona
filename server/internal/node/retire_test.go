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

	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
	"github.com/nwverse/persona/server/internal/proto"
)

// Retiring a finished task's agent and resuming it on the next message (retire.go).

// withoutAgentReports drops herdr's agent reports from a trace. The node reports a pane's agent — its state, and which
// session runs in it — in the background (herdrevents.go, herdrresume.go), so one can land between any two calls of
// whatever is being driven; a test that lists the calls it expects should not depend on where it fell (#1940).
func withoutAgentReports(trace []string) []string {
	return slices.DeleteFunc(slices.Clone(trace), func(c string) bool {
		return strings.HasPrefix(c, "pane.report_agent") || strings.HasPrefix(c, "pane.clear_agent_authority")
	})
}

// session.retire closes a stopped Claude that can be resumed (its pane, as session.close does), keeps what it was
// started with, and refuses one that works, waits, or has no transcript to resume from; one not running is no error.
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

	res, err := request(t, n, "session.retire", map[string]any{"session_id": sidA})
	if err != nil || res["closed"] != true {
		t.Fatalf("retire = %v, %v", res, err)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "cc" || st.Cwd != cwd || st.Model != "opus" {
		t.Errorf("state after retire = %+v", st)
	}
	for _, id := range []string{sidB, sidC, sidD} {
		_, err := request(t, n, "session.retire", map[string]any{"session_id": id})
		if code, _ := refusalOf(t, err); code != notRetirable {
			t.Errorf("%s: code %q", id, code)
		}
	}
	assertTrace(t, withoutAgentReports(f.trace()), []string{
		sendKeys("w1:p1", "ctrl+c"), sendKeys("w1:p1", "ctrl+c"),
		call("pane.list", map[string]any{"workspace_id": "w1"}),
		call("tab.close", map[string]any{"tab_id": "w1:t1"}),
	})

	f.reset()
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	const gone = "0d000000-0000-4000-8000-0000000000ee"
	res, err = request(t, n, "session.retire", map[string]any{"session_id": gone})
	if err != nil || res["closed"] != false {
		t.Fatalf("not running: %v, %v", res, err)
	}
}

// A message to a Codex Persona started that is no longer running resumes it in a new tab of its directory's
// workspace, named after its title: its launcher's words with the model it ran with, `resume <id>`, and the message
// as the prompt (nothing typed). One whose launcher is not known stays refused.
func TestHerdrSendResumesARetiredCodex(t *testing.T) {
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

	if _, err := request(t, n, "session.send", map[string]any{"session_id": startThread, "text": "go on"}); err == nil ||
		!strings.Contains(err.Error(), "重新打开") {
		t.Fatalf("resumed a Codex whose launcher is not known: %v", err)
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
	if _, err := request(t, n, "session.send", map[string]any{"session_id": startThread, "text": "go on"}); err != nil {
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

// Orca continues only Claude sessions (#5548): retire closes there only what a message brings back. A stopped Codex
// on Orca is not closed — the task closer would close an agent that could not be continued (send refuses it as not
// running) — while a stopped Claude is.
func TestOrcaRetiresOnlyWhatItCanContinue(t *testing.T) {
	withCodexLaunchers(t, map[string][]string{"cx": {"codex"}})
	n, _, o, procs, home := orcaNode(t)
	const cx = "0a000000-0000-4000-8000-0000000000c1"
	writeRollout(t, home, cx, orcaCwd, time.Now(), time.Now().Add(-time.Minute), event("task_started"), event("task_complete"))
	n.state.put(cx, sessionState{Launcher: "cx", Cwd: orcaCwd})
	o.SetTerminals(orcaTerm("term_1", "tab1", "leaf1", "codex"))
	o.SetWorktrees(orcarttest.Worktree("repo::"+orcaCwd, orcaCwd, "refs/heads/main"))
	procs.set("tab1:leaf1", orcaProc{pid: 4250, agent: "codex", start: time.Now().Add(-time.Hour)})
	if m := byID(listSessions(t, n)); m[cx].PaneID != "orca:tab1:leaf1" {
		t.Fatalf("codex not listed: %v", m)
	}
	o.Reset()
	_, err := request(t, n, "session.retire", map[string]any{"session_id": cx})
	if code, _ := refusalOf(t, err); code != notRetirable {
		t.Fatalf("codex on Orca: %v", err)
	}
	if c := o.Trace("terminal.close"); len(c) != 0 {
		t.Errorf("closed: %v", c)
	}

	cn, co, _ := orcaSession(t) // a stopped Claude (done): closed, the next message resumes it
	res, err := request(t, cn, "session.retire", map[string]any{"session_id": sidA})
	if err != nil || res["closed"] != true {
		t.Fatalf("claude on Orca: %v, %v (calls %v)", res, err, co.Trace())
	}
}
