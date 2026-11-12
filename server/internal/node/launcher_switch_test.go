package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Switching a running Claude Code Agent to another launcher with the user's next message (launcher_switch.go, #5035).

// withLoginShellConfigDir makes the login shell answer dir as its $CLAUDE_CONFIG_DIR ("" for none; ok false: the
// shell did not answer), without running one.
func withLoginShellConfigDir(t *testing.T, dir string, ok bool) {
	t.Helper()
	old := loginShellConfigDir
	loginShellConfigDir = func(context.Context) (string, bool) { return dir, ok }
	t.Cleanup(func() { loginShellConfigDir = old })
}

// finishedTurn is a transcript whose last turn ended an hour ago: an idle agent with nothing in the background.
func finishedTurn(cwd string) []string {
	return []string{`{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"hi"},"uuid":"u1"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"uuid":"a1","parentUuid":"u1"}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":1000}`}
}

// knownProbes keeps a start from running a login shell to look at launchers it does not know (codexFunctionLauncher).
func knownProbes(t *testing.T, names ...string) {
	t.Helper()
	probeCache.Lock()
	for _, name := range names {
		probeCache.at[name], probeCache.got[name] = time.Now(), proto.LauncherDetail{Agent: "claude"}
	}
	probeCache.Unlock()
	t.Cleanup(func() {
		probeCache.Lock()
		for _, name := range names {
			delete(probeCache.at, name)
			delete(probeCache.got, name)
		}
		probeCache.Unlock()
	})
}

// switchNode is a node with Claude session sidA in herdr pane w1:p1 with status (Persona started it on plain claude in
// ~/proj, its transcript in ~/.claude: finishedTurn when transcript is nil), the managed launchers work (a gateway
// running Claude Code on its own model), bare (one that names no model), cx (Codex) and elsewhere (another Claude
// profile), no shell launchers, a login shell with no $CLAUDE_CONFIG_DIR and an empty input box on the pane. persisted
// collects what the record writes through.
func switchNode(t *testing.T, status string, transcript func(cwd string) []string) (n *Node, f *fakeHerdr, home, cwd string, persisted func() []sessionState) {
	t.Helper()
	n, f, home = herdrNode(t)
	withLoginShellConfigDir(t, "", true)
	withShellLaunchers(t)
	cwd = mkdir(t, filepath.Join(home, "proj"))
	if transcript == nil {
		transcript = finishedTurn
	}
	writeTranscript(t, home, cwd, sidA, time.Now().Add(-time.Hour), transcript(cwd)...)
	var mu sync.Mutex
	var puts []sessionState
	n.state.persist = func(id string, v sessionState) {
		if id == sidA {
			mu.Lock()
			puts = append(puts, v)
			mu.Unlock()
		}
	}
	n.state.put(sidA, sessionState{Launcher: "claude", Cwd: cwd, StartedAt: time.Now().Add(-2 * time.Hour).UTC()})
	knownProbes(t, "work", "bare", "elsewhere")
	n.setManaged([]proto.ManagedLauncher{
		{Name: "work", BaseURL: "https://gateway.example.com", Model: "glm-5"},
		{Name: "bare", BaseURL: "https://bare.example.com"},
		{Name: "cx", Agent: "codex", BaseURL: "https://codex.example.com"},
		{Name: "elsewhere", BaseURL: "https://gateway.example.com", Model: "glm-5", Env: "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude-work")},
	})
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, status, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
	f.reply("pane.process_info", map[string]any{"process_info": map[string]any{"foreground_processes": []any{
		map[string]any{"pid": 1, "name": "zsh", "argv0": "-zsh"}}}})
	f.setScreens("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	listSessions(t, n)
	// Persona started it: the refresh gives herdr its resume command in the background, once (backfillResume).
	waitCalls(t, f, "pane.report_agent_session", 1)
	f.reset()
	return n, f, home, cwd, func() []sessionState {
		mu.Lock()
		defer mu.Unlock()
		return append([]sessionState(nil), puts...)
	}
}

// pick is POST …/modes with params for sidA.
func pick(t *testing.T, n *Node, params map[string]any) error {
	t.Helper()
	p := map[string]any{"session_id": sidA}
	for k, v := range params {
		p[k] = v
	}
	_, err := request(t, n, "session.modes", p)
	return err
}

// tell is the user's message from the chat (apply_switch) to sidA; others false is any other sender's.
func tell(t *testing.T, n *Node, text string, user bool) error {
	t.Helper()
	p := map[string]any{"session_id": sidA, "text": text}
	if user {
		p["apply_switch"] = true
	}
	_, err := request(t, n, "session.send", p)
	return err
}

// rowOf is session id's row in a fresh list.
func rowOf(t *testing.T, n *Node, id string) proto.Session {
	t.Helper()
	for _, s := range listSessions(t, n) {
		if s.SessionID == id {
			return s
		}
	}
	t.Fatalf("%s is not listed", id)
	return proto.Session{}
}

// typedStarts are the commands starts typed into pane, in order.
func typedStarts(f *fakeHerdr, pane string) []string {
	var out []string
	for _, c := range f.calls() {
		if c.Method == "pane.send_input" && str(c.Params["pane_id"]) == pane {
			out = append(out, str(c.Params["text"]))
		}
	}
	return out
}

// drove is what a test that expects the terminal untouched finds pressed or typed there.
func drove(f *fakeHerdr) []string {
	return f.trace("pane.send_keys", "pane.send_input", "pane.send_text", "agent.prompt")
}

// comesUpWhen makes the claude a start types into pane come up (agent.get names it) only for a typed command up says
// yes to, so a start of another launcher waits in vain.
func comesUpWhen(f *fakeHerdr, pane string, up func(typed string) bool) {
	var mu sync.Mutex
	ok := false
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_input" && str(c.Params["pane_id"]) == pane {
			mu.Lock()
			ok = up(str(c.Params["text"]))
			mu.Unlock()
		}
	})
	f.handle("agent.get", func(map[string]any) any {
		mu.Lock()
		defer mu.Unlock()
		if !ok {
			return map[string]any{"agent": nil}
		}
		return map[string]any{"agent": map[string]any{"agent": "claude", "agent_status": proto.StatusIdle, "pane_id": pane,
			"tab_id": "w1:t1", "workspace_id": "w1"}}
	})
}

// quitsToShell makes the agent leave pane w1:p1 for its shell in dir after two Ctrl+C.
func quitsToShell(f *fakeHerdr, dir string) {
	quitsOn(f, "w1:p1", sidA, nil, shellPane("w1:p1", "w1:t1", "w1", dir, sidA))
}

// Picking another launcher keeps it as the session's pending switch, in the record (persisted) and on its row, and
// touches nothing in the terminal; a new pick replaces it, and the session's own launcher drops it.
func TestLauncherSwitchIsPendingUntilCancelled(t *testing.T) {
	n, f, _, _, persisted := switchNode(t, proto.StatusIdle, nil)
	if err := pick(t, n, map[string]any{"launcher": "work", "from": "claude", "model": "opus", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if got := drove(f); len(got) != 0 {
		t.Fatalf("picking a launcher drove the terminal: %q", got)
	}
	row := rowOf(t, n, sidA)
	if ps := row.PendingSwitch; ps == nil || ps.Launcher != "work" || ps.Model != "opus" || ps.Effort != "high" || ps.At.IsZero() {
		t.Fatalf("row's pending switch = %+v", ps)
	}
	if row.Launcher != "claude" {
		t.Errorf("row's launcher = %q before the switch ran", row.Launcher)
	}
	if puts := persisted(); len(puts) == 0 || puts[len(puts)-1].PendingSwitch == nil || puts[len(puts)-1].PendingSwitch.Launcher != "work" {
		t.Fatalf("the switch was not kept with the record: %+v", puts)
	}
	if err := pick(t, n, map[string]any{"launcher": "work", "from": "claude"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps == nil || ps.Model != "" || ps.Effort != "" {
		t.Fatalf("a new pick did not replace the old one: %+v", ps)
	}
	if err := pick(t, n, map[string]any{"launcher": "claude", "from": "claude"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps != nil {
		t.Fatalf("its own launcher left the switch pending: %+v", ps)
	}
	if rowOf(t, n, sidA).PendingSwitch != nil {
		t.Error("the row still shows the cancelled switch")
	}
	if puts := persisted(); puts[len(puts)-1].PendingSwitch != nil {
		t.Errorf("the cancel was not written through: %+v", puts[len(puts)-1])
	}
	if got := drove(f); len(got) != 0 {
		t.Fatalf("cancelling drove the terminal: %q", got)
	}
}

// A pick or a cancel made against a launcher the session no longer runs on (from) is stale: nothing is recorded. A
// permission mode that does not go through takes the switch it came with back.
func TestLauncherSwitchPickPreconditions(t *testing.T) {
	n, _, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	for _, params := range []map[string]any{
		{"launcher": "work", "from": "elsewhere"},
		{"launcher": "claude", "from": "work"},
	} {
		err := pick(t, n, params)
		if code, _ := refusalOf(t, err); code != proto.CodeSwitchStale {
			t.Errorf("%v: %v", params, err)
		}
	}
	if ps := n.pendingSwitch(sidA); ps != nil {
		t.Fatalf("a stale pick was recorded: %+v", ps)
	}
	// while a switch is carried out (the session listed as starting), a pick or a cancel is refused at once, not held
	// for the minutes it takes
	n.switches().begin(sidA, proto.Session{SessionID: sidA, Starting: true})
	if code, _ := refusalOf(t, pick(t, n, map[string]any{"launcher": "claude", "from": "claude"})); code != proto.CodeSwitchStale {
		t.Errorf("a cancel during a switch: %q", code)
	}
	n.switches().end(sidA)
	// nor while the message carrying it out still runs its checks, before the session is listed as starting
	done := n.markSwitching(sidA)
	if code, _ := refusalOf(t, pick(t, n, map[string]any{"launcher": "work", "from": "claude"})); code != proto.CodeSwitchStale {
		t.Errorf("a pick during a switch's checks: %q", code)
	}
	done()
	if err := pick(t, n, map[string]any{"launcher": "work", "from": "claude", "model": "opus"}); err != nil {
		t.Fatal(err)
	}
	err := pick(t, n, map[string]any{"launcher": "work", "from": "claude", "model": "sonnet", "permission_mode": "sideways"})
	if err == nil || !strings.Contains(err.Error(), "sideways") {
		t.Fatalf("a bad permission mode: %v", err)
	}
	if ps := n.pendingSwitch(sidA); ps == nil || ps.Launcher != "work" || ps.Model != "opus" {
		t.Fatalf("the switch before the failed request is not back: %+v", ps)
	}
}

// A session Persona did not start gets no record for a switch (a record means Persona started it): one whose launcher
// its process's environment names waits in memory and shows on its row; one whose launcher cannot be told is refused,
// since it could not be brought back on it.
func TestLauncherSwitchOfAnUnrecordedSession(t *testing.T) {
	n, _, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.mu.Lock()
	delete(n.state.Sessions, sidA)
	n.state.mu.Unlock()
	err := pick(t, n, map[string]any{"launcher": "work"})
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchUnsupported {
		t.Fatalf("a launcher nothing names: %v", err)
	}

	inferredAs(t, n, "gw")
	if err := pick(t, n, map[string]any{"launcher": "work", "from": "gw"}); err != nil {
		t.Fatal(err)
	}
	if _, recorded := n.state.get(sidA); recorded {
		t.Fatal("a switch made a record for a session Persona did not start")
	}
	if ps := rowOf(t, n, sidA).PendingSwitch; ps == nil || ps.Launcher != "work" {
		t.Fatalf("row's pending switch = %+v", ps)
	}
	if err := pick(t, n, map[string]any{"launcher": "gw", "from": "gw"}); err != nil {
		t.Fatal(err)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("its own launcher left the switch pending")
	}
}

// inferredAs makes sidA's claude one whose environment names launcher name (inferLauncher): a shell launcher of a
// gateway that is a managed provider too, so its checks run no shell.
func inferredAs(t *testing.T, n *Node, name string) {
	t.Helper()
	const url = "https://own.example.com"
	launcherCache.Lock()
	oldAt, oldList := launcherCache.at, launcherCache.list
	launcherCache.at, launcherCache.list = time.Now(), []proto.Launcher{{Name: name, BaseURL: url}}
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = oldAt, oldList
		launcherCache.Unlock()
	})
	knownProbes(t, name)
	n.managed.mu.Lock()
	n.managed.byID[name] = proto.ManagedLauncher{Name: name, BaseURL: url, Model: "own-model"}
	n.managed.mu.Unlock()
	n.cdirs = &claudeDirs{byPID: map[int]string{}, bySession: map[string]procEnv{sidA: {BaseURL: url}}, scanned: time.Now(),
		scan: func() map[int]procEnv { return nil }}
}

// What a switch is refused for when it is picked, with nothing kept and nothing typed.
func TestLauncherSwitchRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string)
		params map[string]any
		code   string
	}{
		{"launcher not on this computer", nil, map[string]any{"launcher": "nowhere"}, proto.CodeSwitchLauncherMissing},
		{"launcher of another agent", nil, map[string]any{"launcher": "cx"}, proto.CodeSwitchOtherAgent},
		{"another config directory", nil, map[string]any{"launcher": "elsewhere"}, proto.CodeSwitchConfigDir},
		{"the shell's own config directory is another", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			withLoginShellConfigDir(t, filepath.Join(home, ".claude-team"), true)
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchConfigDir},
		{"no default model to be seen", nil, map[string]any{"launcher": "bare"}, proto.CodeSwitchPickModel},
		{"a locked model", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			withShellLaunchers(t, "fixed")
			knownProbes(t, "fixed")
			modelProbeCache.Lock()
			modelProbeCache.results[n.home+"\x00fixed\x00opus"] = modelProbeResult{at: time.Now(), agent: "claude", err: fmt.Errorf("fixed passes its own --model")}
			modelProbeCache.Unlock()
		}, map[string]any{"launcher": "fixed", "model": "opus"}, proto.CodeSwitchModelLocked},
		{"a Codex agent", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			f.reply("session.snapshot", snapshot(nil, nil, []any{agentOf("codex", sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", cwd, "")}, nil))
			listSessions(t, n)
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchUnsupported},
		{"an assistant conversation", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			n.mu.Lock()
			n.procs[sidA] = &assistantProc{}
			n.mu.Unlock()
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchUnsupported},
		{"a folder that is not the conversation's", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			// the transcript is in ~/proj's project folder, and nothing names ~/proj
			other := mkdir(t, filepath.Join(home, "other"))
			n.state.put(sidA, sessionState{Launcher: "claude", Cwd: other})
			f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(sidA, proto.StatusIdle, "w1:p1", "w1:t1", "w1", other, "")}, nil))
			listSessions(t, n)
		}, map[string]any{"launcher": "work"}, proto.CodeSwitchUnsupported},
		{"not running", func(t *testing.T, n *Node, f *fakeHerdr, home, cwd string) {
			f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
			listSessions(t, n)
		}, map[string]any{"launcher": "work"}, proto.CodeModesNotRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transcript := finishedTurn
			if strings.Contains(tc.name, "folder") { // its lines name another folder than the one it is filed under
				transcript = func(cwd string) []string { return finishedTurn(filepath.Join(filepath.Dir(cwd), "other")) }
			}
			n, f, home, cwd, _ := switchNode(t, proto.StatusIdle, transcript)
			if tc.setup != nil {
				tc.setup(t, n, f, home, cwd)
			}
			f.reset()
			err := pick(t, n, tc.params)
			if code, data := refusalOf(t, err); code != tc.code || data["delivered"] != deliveredNone {
				t.Fatalf("refused with %q (%v), want %q", code, err, tc.code)
			}
			if ps := n.pendingSwitch(sidA); ps != nil {
				t.Errorf("a refused switch was kept: %+v", ps)
			}
			if got := drove(f); len(got) != 0 {
				t.Errorf("a refused switch drove the terminal: %q", got)
			}
		})
	}
}

// A launcher whose config directory cannot be told (the login shell did not answer) is let through: a resume that
// finds no conversation fails the start, and the switch rolls back. So is a profile whose directory shares the
// conversation (projects/ through a symlink).
func TestLauncherSwitchConfigDirsLetThrough(t *testing.T) {
	n, _, home, _, _ := switchNode(t, proto.StatusIdle, nil)
	withLoginShellConfigDir(t, "", false)
	if dir, known := n.launcherClaudeDir(t.Context(), "work"); known {
		t.Errorf("a login shell that did not answer told %q", dir)
	}
	if err := pick(t, n, map[string]any{"launcher": "work"}); err != nil {
		t.Fatalf("a directory that cannot be told: %v", err)
	}
	withLoginShellConfigDir(t, "", true)
	if err := os.MkdirAll(filepath.Join(home, ".claude-work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude", "projects"), filepath.Join(home, ".claude-work", "projects")); err != nil {
		t.Fatal(err)
	}
	if err := pick(t, n, map[string]any{"launcher": "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if ps := n.pendingSwitch(sidA); ps == nil || ps.Launcher != "elsewhere" {
		t.Fatalf("pending switch = %+v", ps)
	}
}

// The user's next message carries the switch out: the agent is quit in its pane (Ctrl+C twice, the pane kept) and the
// conversation resumed through the new launcher in the same shell with the model picked, listed as starting
// meanwhile; the record moves to the launcher (its folder kept) and the switch is cleared; then the message goes to the
// new agent.
func TestLauncherSwitchOnTheUsersNextMessage(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	if err := pick(t, n, map[string]any{"launcher": "work", "model": "opus"}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	var mu sync.Mutex
	var starting []proto.Session
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_input" {
			mu.Lock()
			for _, row := range cachedSessions(n) {
				if row.SessionID == sidA {
					starting = append(starting, row)
				}
			}
			mu.Unlock()
		}
	})
	if err := tell(t, n, "hello", true); err != nil {
		t.Fatalf("send: %v\n%s", err, strings.Join(f.trace(), "\n"))
	}
	if got := f.trace("tab.create", "workspace.create", "tab.close", "pane.close"); len(got) != 0 {
		t.Errorf("a tab was opened or closed: %q", got)
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !strings.Contains(starts[0], " launch work ") || !strings.Contains(starts[0], "--resume "+sidA) ||
		!strings.Contains(starts[0], "--model opus") || strings.HasPrefix(starts[0], "cd ") {
		t.Fatalf("typed %q", starts)
	}
	trace := strings.Join(f.trace("pane.send_keys", "pane.send_input", "agent.prompt"), "\n")
	quit := strings.Index(trace, sendKeys("w1:p1", "ctrl+c"))
	start := strings.Index(trace, "pane.send_input")
	prompt := strings.Index(trace, call("agent.prompt", map[string]any{"target": "w1:p1", "text": "hello"}))
	if quit < 0 || start < quit || prompt < start {
		t.Fatalf("not quit, started, then prompted:\n%s", trace)
	}
	mu.Lock()
	if len(starting) != 1 || !starting[0].Starting || starting[0].Launcher != "work" {
		t.Errorf("listed while it switched: %+v", starting)
	}
	mu.Unlock()
	if n.switches().inFlight(sidA) {
		t.Error("still listed as starting")
	}
	st, _ := n.state.get(sidA)
	if st.Launcher != "work" || st.Model != "opus" || st.PendingSwitch != nil || st.Cwd != cwd {
		t.Errorf("record after the switch = %+v", st)
	}
}

// Only the user's message from the chat carries a switch out: any other sender's goes on the launcher the agent runs
// on, and the switch waits. So does the user's while the agent is busy: working, or with background work a quit would
// end, counted from its process's start however long ago, or not read yet.
func TestLauncherSwitchWaitsWhileBusy(t *testing.T) {
	daysAgo := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	background := func(cwd string) []string {
		return append(finishedTurn(cwd),
			`{"type":"assistant","uuid":"a2","timestamp":"`+daysAgo+`","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"sleep 600000","run_in_background":true}}]}}`,
			`{"type":"user","uuid":"u2","timestamp":"`+daysAgo+`","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: b1. Output is being written to: /tmp/tasks/b1.output"}]}}`,
			`{"type":"system","subtype":"turn_duration","uuid":"d2","parentUuid":"u2","durationMs":1000}`)
	}
	for _, tc := range []struct {
		name       string
		status     string
		transcript func(cwd string) []string
		user       bool
		setup      func(t *testing.T, n *Node)
	}{
		{"another sender", proto.StatusIdle, nil, false, nil},
		{"working", proto.StatusWorking, nil, true, nil},
		{"a background command of this process, two days old", proto.StatusIdle, background, true, func(t *testing.T, n *Node) {
			n.state.setUltracode(sidA, ultracodeRecord{Since: time.Now().Add(-72 * time.Hour).UTC()})
		}},
		{"background work not read yet", proto.StatusIdle, nil, true, func(t *testing.T, n *Node) {
			old := bgSyncLimit
			bgSyncLimit = 0
			bgSlot <- struct{}{} // the read in the background waits its turn: still not done when the message comes
			t.Cleanup(func() { <-bgSlot; bgSyncLimit = old })
			n.mu.Lock()
			n.bgScans, n.metas = map[string]*bgScan{}, map[string]quickMeta{}
			n.mu.Unlock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, _, _, _ := switchNode(t, tc.status, tc.transcript)
			if tc.setup != nil {
				tc.setup(t, n)
			}
			n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
			f.reset()
			if err := tell(t, n, "hello", tc.user); err != nil {
				t.Fatalf("send: %v", err)
			}
			if got := f.trace("pane.send_keys", "pane.send_input"); len(got) != 0 {
				t.Errorf("quit or restarted the agent: %q", got)
			}
			if got := f.trace("agent.prompt"); len(got) != 1 {
				t.Errorf("the message did not go on the launcher it runs on: %q", got)
			}
			if ps := n.pendingSwitch(sidA); ps == nil || ps.Launcher != "work" {
				t.Errorf("the switch no longer waits: %+v", ps)
			}
		})
	}
}

// A switch that no longer applies goes without a word: picked over a day ago, or naming the launcher the agent runs
// on by now. The message is sent as always.
func TestLauncherSwitchThatNoLongerApplies(t *testing.T) {
	for _, ps := range []proto.PendingSwitch{
		{Launcher: "work", At: time.Now().Add(-25 * time.Hour)},
		{Launcher: "claude", At: time.Now()},
	} {
		n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
		n.state.setPendingSwitch(sidA, &ps)
		if err := tell(t, n, "hello", true); err != nil {
			t.Fatal(err)
		}
		if got := f.trace("pane.send_keys", "pane.send_input"); len(got) != 0 {
			t.Errorf("%+v: quit or restarted the agent: %q", ps, got)
		}
		if got := f.trace("agent.prompt"); len(got) != 1 {
			t.Errorf("%+v: prompts = %q", ps, got)
		}
		if n.pendingSwitch(sidA) != nil {
			t.Errorf("%+v: still waits", ps)
		}
	}
}

// A check that fails when the message comes (the launcher went meanwhile) drops the switch and refuses the message
// with nothing typed, as a refusal of the switch the client keeps the words for.
func TestLauncherSwitchRefusedWhenTheMessageComes(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "gone", At: time.Now()})
	err := tell(t, n, "hello", true)
	if code, data := refusalOf(t, err); code != proto.CodeSwitchLauncherMissing || data["delivered"] != deliveredNone {
		t.Fatalf("refused with %q (%v)", code, err)
	}
	if got := drove(f); len(got) != 0 {
		t.Errorf("drove the terminal: %q", got)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("a refused switch still waits, for every message after")
	}
}

// The last look before the first Ctrl+C: a draft that came into the box while the checks ran refuses the message as
// any draft does, with nothing pressed, and the switch waits.
func TestLauncherSwitchLooksAgainBeforeTheQuit(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	f.reset()
	empty, typed := screenFixture(t, "claude-2.1.312-empty-162.ansi"), screenFixture(t, "claude-2.1.312-typed-162.ansi")
	var mu sync.Mutex
	reads := 0
	f.after(func(c herdrCall) {
		if c.Method == "pane.read" && c.Params["format"] == "ansi" {
			mu.Lock()
			if reads++; reads == 1 { // the send's own look found the box empty; someone typed at the computer since
				f.setScreens("w1:p1", screenFixture(t, "claude-2.1.312-typed-162.txt"), typed)
			}
			mu.Unlock()
		}
	})
	f.setScreens("w1:p1", screenFixture(t, "claude-2.1.312-empty-162.txt"), empty)
	err := tell(t, n, "hello", true)
	if code, _ := refusalOf(t, err); code != "input_draft" {
		t.Fatalf("refused with %q (%v)", code, err)
	}
	if got := drove(f); len(got) != 0 {
		t.Errorf("drove the terminal: %q", got)
	}
	if ps := n.pendingSwitch(sidA); ps == nil {
		t.Error("the switch no longer waits")
	}
}

// A start on the new launcher that does not come up puts the record back and resumes the agent on its own launcher in
// the same shell, with the model it last switched to and its plan mode; what that start left in front (a claude
// stopped on a sign-in screen) is quit first. The message is not typed, the switch is dropped, and the refusal says
// how it ended: back on its own launcher, or stopped when that does not come up either.
func TestLauncherSwitchFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		backUp bool
		code   string
	}{
		{"back on its own launcher", true, proto.CodeSwitchFailed},
		{"stopped", false, proto.CodeSwitchFailedStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
			n.claudeStart, n.claudeReady = time.Second, time.Second
			n.noteModel(sidA, "sonnet", "")
			if err := pick(t, n, map[string]any{"launcher": "work", "model": "opus"}); err != nil {
				t.Fatal(err)
			}
			f.reset()
			quitsToShell(f, cwd)
			comesUpWhen(f, "w1:p1", func(typed string) bool { return tc.backUp && !strings.Contains(typed, " launch work ") })
			// The new launcher's claude stays in front (its sign-in screen) until Ctrl+C is pressed twice after it.
			var mu sync.Mutex
			stuck, presses := false, 0
			f.after(func(c herdrCall) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case c.Method == "pane.send_input" && strings.Contains(str(c.Params["text"]), " launch work "):
					stuck, presses = true, 0
				case c.Method == "pane.send_keys" && stuck:
					if presses++; presses == 2 {
						stuck = false
					}
				}
			})
			f.handle("pane.process_info", func(map[string]any) any {
				mu.Lock()
				name := "zsh"
				if stuck {
					name = "claude"
				}
				mu.Unlock()
				return map[string]any{"process_info": map[string]any{"foreground_processes": []any{map[string]any{"pid": 1, "name": name, "argv0": name}}}}
			})
			err := tell(t, n, "hello", true)
			if code, data := refusalOf(t, err); code != tc.code || data["delivered"] != deliveredNone {
				t.Fatalf("refused with %q (%v), want %q", code, err, tc.code)
			}
			if got := f.trace("agent.prompt"); len(got) != 0 {
				t.Fatalf("the message was typed: %q", got)
			}
			if got := f.trace("tab.close", "pane.close"); len(got) != 0 {
				t.Errorf("closed the agent's terminal: %q", got)
			}
			starts := typedStarts(f, "w1:p1")
			if len(starts) != 2 || !strings.Contains(starts[0], " launch work ") || strings.Contains(starts[1], " launch work ") ||
				!strings.Contains(starts[1], "--resume "+sidA) || !strings.Contains(starts[1], "--model sonnet") {
				t.Fatalf("typed %q", starts)
			}
			trace := strings.Join(f.trace("pane.send_keys", "pane.send_input"), "\n")
			if i := strings.Index(trace, "launch work"); !strings.Contains(trace[i:], sendKeys("w1:p1", "ctrl+c")) {
				t.Errorf("the claude left in front was not quit before the rollback:\n%s", trace)
			}
			st, _ := n.state.get(sidA)
			if st.Launcher != "claude" || st.Cwd != cwd {
				t.Errorf("record after the failed switch = %+v", st)
			}
			if ps := n.pendingSwitch(sidA); ps != nil {
				t.Errorf("a failed switch still waits: %+v", ps)
			}
			if n.switches().inFlight(sidA) {
				t.Error("left listed as starting")
			}
		})
	}
}

// A session Persona did not start whose switch failed and could not be brought back keeps the record its starts made,
// naming its own launcher: with no process of it left to say which launcher it ran on, that is what its Resume brings
// it back on (not plain claude), and the switch is gone from it.
func TestLauncherSwitchFailureOfAnUnrecordedSessionKeepsItsLauncher(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.claudeStart, n.claudeReady = time.Second, time.Second
	n.state.mu.Lock()
	delete(n.state.Sessions, sidA)
	n.state.mu.Unlock()
	inferredAs(t, n, "gw")
	if err := pick(t, n, map[string]any{"launcher": "work", "model": "opus"}); err != nil {
		t.Fatal(err)
	}
	f.reset()
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return false })
	err := tell(t, n, "hello", true)
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchFailedStopped {
		t.Fatalf("refused with %q (%v)", code, err)
	}
	if starts := typedStarts(f, "w1:p1"); len(starts) != 2 || !strings.Contains(starts[1], " launch gw ") {
		t.Fatalf("typed %q", starts)
	}
	st, kept := n.state.get(sidA)
	if !kept || st.Launcher != "gw" || st.PendingSwitch != nil || st.Cwd != cwd {
		t.Errorf("record = %+v (kept %v)", st, kept)
	}
	if r := n.resumeWith(sidA, sendOpts{Launcher: "hub-pick"}); r.launcher != "gw" {
		t.Errorf("a Resume would bring it back on %q", r.launcher)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("the failed switch still waits")
	}
}

// The resume runs in the conversation's folder: a shell that is elsewhere changes there first, and the record keeps
// its folder.
func TestLauncherSwitchResumesInTheConversationsFolder(t *testing.T) {
	n, f, home, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	f.reset()
	quitsToShell(f, mkdir(t, filepath.Join(home, "elsewhere")))
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	if err := tell(t, n, "hello", true); err != nil {
		t.Fatal(err)
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !strings.HasPrefix(starts[0], "cd "+shellQuote(cwd)+" && ") {
		t.Fatalf("typed %q", starts)
	}
	if st, _ := n.state.get(sidA); st.Cwd != cwd {
		t.Errorf("record's folder = %q", st.Cwd)
	}
}

// A plan started to go on in bypassPermissions keeps the flag that mode needs on the new launcher.
func TestLauncherSwitchKeepsThePlanMode(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.put(sidA, sessionState{Launcher: "claude", Cwd: cwd, StartedAt: time.Now().UTC(), PlanThen: proto.ModeBypass})
	n.noteModes(sidA, "plan", "")
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	f.reset()
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	if err := tell(t, n, "hello", true); err != nil {
		t.Fatal(err)
	}
	starts := typedStarts(f, "w1:p1")
	if len(starts) != 1 || !strings.Contains(starts[0], "--permission-mode plan") || !strings.Contains(starts[0], "--allow-dangerously-skip-permissions") {
		t.Fatalf("typed %q", starts)
	}
	if st, _ := n.state.get(sidA); st.PlanThen != proto.ModeBypass {
		t.Errorf("record's plan mode = %q", st.PlanThen)
	}
}

// From the first Ctrl+C on the switch runs on its own clock: a caller that goes away then (a hub redeploy ends the
// connection) does not leave the agent quit and not started again.
func TestLauncherSwitchOutlivesItsCaller(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	f.reset()
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_keys" {
			cancel()
		}
	})
	b, _ := json.Marshal(map[string]any{"session_id": sidA, "text": "hello", "apply_switch": true})
	_, _ = n.handle(ctx, "session.send", b)
	if starts := typedStarts(f, "w1:p1"); len(starts) != 1 || !strings.Contains(starts[0], " launch work ") {
		t.Fatalf("typed %q", starts)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "work" {
		t.Errorf("record = %+v", st)
	}
}

// An agent that does not quit runs on as it was: nothing is started or typed, and the switch is dropped.
func TestLauncherSwitchWhenTheAgentDoesNotQuit(t *testing.T) {
	withStop(t, 1, 100*time.Millisecond)
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	err := tell(t, n, "hello", true)
	if code, _ := refusalOf(t, err); code != proto.CodeSwitchNotStopped {
		t.Fatalf("refused with %q (%v)", code, err)
	}
	if got := f.trace("pane.send_input", "agent.prompt"); len(got) != 0 {
		t.Errorf("started or typed: %q", got)
	}
	if st, _ := n.state.get(sidA); st.Launcher != "claude" || st.PendingSwitch != nil {
		t.Errorf("record = %+v", st)
	}
}

// The session's own launcher with a model drops a switch that waits and switches the model as without a launcher
// (model_switch.go): an id that is no model is refused there, as always.
func TestLauncherSwitchOwnLauncherWithAModelGoesOn(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
	err := pick(t, n, map[string]any{"launcher": "claude", "model": "-rf"})
	if err == nil || !strings.Contains(err.Error(), "invalid model") {
		t.Fatalf("own launcher with a model: %v", err)
	}
	if n.pendingSwitch(sidA) != nil {
		t.Error("the switch still waits")
	}
	if got := drove(f); len(got) != 0 {
		t.Errorf("drove the terminal: %q", got)
	}
}

// The user's Resume of a stopped agent whose switch waits brings it back on the launcher it ran on, the switch still
// waiting for the next message.
func TestResumeKeepsAPendingSwitch(t *testing.T) {
	n, _, _ := keptAgent(t)
	n.claudeStart, n.claudeReady = 200*time.Millisecond, 200*time.Millisecond // the fake shell never runs claude
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	_, _ = request(t, n, "session.start", map[string]any{"resume_session_id": sidA})
	st, _ := n.state.get(sidA)
	if st.Launcher != "claude" || st.PendingSwitch == nil || st.PendingSwitch.Launcher != "work" {
		t.Fatalf("record after the resume = %+v", st)
	}
}

// The user's Resume waits for a switch being carried out (the agent quit, its new one not up yet) instead of starting
// a second claude in the shell it left: on the next connection's Node of a resident node too (the switch began on the
// last one), which lists the session as starting meanwhile.
func TestResumeWaitsForASwitch(t *testing.T) {
	old, _, _ := keptAgent(t)
	next, _ := testNode(t)
	next.share.Store(old.switches()) // a resident node's Nodes share it (attach.go)
	old.switches().begin(sidA, proto.Session{SessionID: sidA, Launcher: "work", Starting: true})
	if rows := next.switches().listed(map[string]bool{}); len(rows) != 1 || !rows[0].Starting || rows[0].Launcher != "work" {
		t.Errorf("the next Node lists %+v", rows)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		next.awaitSwitch(context.Background(), sidA)
	}()
	select {
	case <-done:
		t.Fatal("the Resume did not wait for the switch")
	case <-time.After(200 * time.Millisecond):
	}
	sent := make(chan struct{})
	go func() { // a message on the next Node waits as well
		defer close(sent)
		if unlock, err := next.inputTurn(context.Background(), sidA); err == nil {
			unlock()
		}
	}()
	select {
	case <-sent:
		t.Fatal("a message did not wait for the switch")
	case <-time.After(200 * time.Millisecond):
	}
	old.switches().end(sidA)
	for _, ch := range []chan struct{}{done, sent} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("still waiting after the switch")
		}
	}
}

// A switch whose Node's connection ended (a resident node's hub reconnected) runs to its end, and what it writes goes
// to the next connection's Node, over the copy that Node loaded from the hub (taken before the switch was carried out:
// the old launcher, the switch still waiting), and through it to the hub: the session is on the new launcher there,
// with no switch waiting.
func TestASwitchOutlivingItsConnectionIsWrittenByTheNextNode(t *testing.T) {
	n, f, _, cwd, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", Model: "opus", At: time.Now()})
	loaded, _ := n.state.get(sidA) // what the hub has

	next, _ := testNode(t)
	next.share.Store(n.switches())
	next.modes = map[string]sessionModes{}
	var mu sync.Mutex
	var written []sessionState
	next.state.persist = func(id string, v sessionState) {
		mu.Lock()
		written = append(written, v)
		mu.Unlock()
	}
	next.state.merge(map[string]sessionState{sidA: loaded})
	// the new claude's hook on the next connection: an ultracode observation of its own, newer than the switch's
	since := ultracodeRecord{Since: time.Now().UTC()}
	next.state.setUltracode(sidA, since)
	next.switches().receive(next) // its state has loaded
	// The old connection is gone: its Node's writes to the hub fail.
	closed := rpc.New("n", io.Discard)
	closed.Close(rpc.ErrClosed)
	n.peer, n.state.persist = closed, n.persistState
	quitsToShell(f, cwd)
	comesUpWhen(f, "w1:p1", func(string) bool { return true })
	_ = tell(t, n, "hello", true) // the message itself goes to a connection that is gone
	if starts := typedStarts(f, "w1:p1"); len(starts) != 1 || !strings.Contains(starts[0], " launch work ") {
		t.Fatalf("typed %q", starts)
	}
	st, ok := next.state.get(sidA)
	if !ok || st.Launcher != "work" || st.Model != "opus" || st.PendingSwitch != nil {
		t.Fatalf("the next Node has %+v", st)
	}
	if st.Ultracode == nil || !st.Ultracode.Since.Equal(since.Since) {
		t.Errorf("the next Node's own observation was written over: %+v", st.Ultracode)
	}
	if shown := next.shownModel(sidA, next.transcriptModel(sidA)); shown != "opus" {
		t.Errorf("the next Node shows model %q", shown)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(written) == 0 || written[len(written)-1].Launcher != "work" || written[len(written)-1].PendingSwitch != nil {
		t.Fatalf("written to the hub: %+v", written)
	}
}

// The probe tells a launcher that unsets $CLAUDE_CONFIG_DIR (env -u): it runs claude with ~/.claude.
func TestProbeTellsUnsetVariables(t *testing.T) {
	out := probeMarker + " base A=1\x00CLAUDE_CONFIG_DIR=/p\x00\n" + probeMarker + " agent claude\n" + probeMarker + " env A=1\x00\n"
	p, ok := parseProbe(out)
	if !ok || len(p.Unset) != 1 || p.Unset[0] != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("probe = %+v", p)
	}
}

// The command a switch types into a shell elsewhere changes to the conversation's folder first, with the shell's own
// way of running one command after another: nu and elvish have no &&.
func TestCdThen(t *testing.T) {
	for _, tc := range []struct{ shell, want string }{
		{"-zsh", "cd '/w x' && claude"},
		{"fish", "cd '/w x' && claude"},
		{"/usr/bin/nu", "cd '/w x'; claude"},
		{"elvish", "cd '/w x'; claude"},
	} {
		if got := cdThen("/w x", "claude", tc.shell); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.shell, got, tc.want)
		}
	}
}

// A stop or archive while a switch is carried out is refused at once, not after the minutes the message carrying it
// out holds the session's typing lock (stopping the agent it starts then would leave the switch for nothing).
func TestStopRefusedAtOnceDuringASwitch(t *testing.T) {
	n, _, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	unlock := n.typing.lock(sidA) // the message carrying the switch out
	defer unlock()
	done := n.markSwitching(sidA)
	defer done()
	refused := make(chan error, 1)
	go func() {
		_, err := request(t, n, "session.stop", map[string]any{"session_id": sidA})
		refused <- err
	}()
	select {
	case err := <-refused:
		if code, _ := refusalOf(t, err); code != notRetirable {
			t.Fatalf("refused with %q (%v)", code, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the stop waited for the switch")
	}
}

// A switch picked over a day ago is not shown as waiting: the next message drops it.
func TestAnOldSwitchIsNotShownAsWaiting(t *testing.T) {
	n, _, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now().Add(-25 * time.Hour)})
	if ps := rowOf(t, n, sidA).PendingSwitch; ps != nil {
		t.Errorf("row shows %+v", ps)
	}
	n.state.setPendingSwitch(sidA, &proto.PendingSwitch{Launcher: "work", At: time.Now()})
	if ps := rowOf(t, n, sidA).PendingSwitch; ps == nil {
		t.Error("a fresh switch is not shown")
	}
}

// While a launcher switch of a session is carried out on the last connection's Node of a resident node (whose typing
// lock this Node does not hold), what would type into its pane or press its keys is refused with nothing done: a
// model, mode or effort change, a rewind, keys and an interrupt, a close.
func TestTypingRefusedWhileASwitchRunsElsewhere(t *testing.T) {
	n, f, _, _, _ := switchNode(t, proto.StatusIdle, nil)
	other := &switchShare{} // the resident node's, shared with the Node the switch runs on
	n.share.Store(other)
	other.begin(sidA, proto.Session{SessionID: sidA, Starting: true})
	f.reset()
	for _, c := range []struct {
		method string
		params map[string]any
	}{
		{"session.modes", map[string]any{"model": "opus"}},
		{"session.modes", map[string]any{"permission_mode": "plan"}},
		{"session.modes", map[string]any{"effort": "high"}},
		{"session.rewind", map[string]any{"prompt": "hi", "occurrence": 1, "restore": RestoreConversation}},
		{"session.keys", map[string]any{"keys": []string{"enter"}}},
		{"session.interrupt", nil},
		{"session.close", nil},
	} {
		p := map[string]any{"session_id": sidA}
		for k, v := range c.params {
			p[k] = v
		}
		_, err := request(t, n, c.method, p)
		if code, _ := refusalOf(t, err); code != proto.CodeSwitchUnderWay {
			t.Errorf("%s %v: %q (%v)", c.method, c.params, code, err)
		}
	}
	if got := drove(f); len(got) != 0 {
		t.Errorf("typed or pressed during the switch: %q", got)
	}
	other.end(sidA)
}

// The records a switch on the last connection's Node writes reach the Node that serves now in the order they were
// written: one written while an earlier one is being given to it goes after it, not before (the earlier one would
// win). And a Node that cannot write either (its connection gone too) leaves them for the next one, without looping.
func TestSwitchShareGivesRecordsInOrder(t *testing.T) {
	old, _ := testNode(t)
	share := old.switches()
	share.begin(sidA, proto.Session{SessionID: sidA, Starting: true})
	older := sessionState{Launcher: "work", Cwd: "/w", PendingSwitch: &proto.PendingSwitch{Launcher: "work"}}
	newer := sessionState{Launcher: "claude", Cwd: "/w"}
	share.keep(old, sidA, older) // held: no Node takes them yet

	next, _ := testNode(t)
	next.share.Store(share)
	var during string
	next.state.persist = func(id string, v sessionState) {
		if v.Launcher == "work" { // while the older one is given, the switch writes again
			share.keep(old, sidA, newer)
			cur, _ := next.state.get(sidA)
			during = cur.Launcher
		}
	}
	share.receive(next)
	if during != "work" {
		t.Errorf("the newer record went in while the older one was being given (now %q)", during)
	}
	if st, _ := next.state.get(sidA); st.Launcher != "claude" || st.PendingSwitch != nil {
		t.Fatalf("the serving Node has %+v", st)
	}

	// A Node whose own writes fail takes no more: the record waits for the next one.
	gone, _ := testNode(t)
	gone.share.Store(share)
	gone.state.persist = func(id string, v sessionState) { share.keep(gone, id, v) }
	done := make(chan struct{})
	go func() {
		defer close(done)
		share.receive(gone)
		share.keep(old, sidA, older)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("giving records to a Node that cannot write them never ends")
	}
	last, _ := testNode(t)
	last.share.Store(share)
	share.receive(last)
	if st, _ := last.state.get(sidA); st.Launcher != "work" {
		t.Errorf("the next Node did not get the record: %+v", st)
	}
	share.end(sidA)
}

// Two marks of one session (two messages taking a switch up at once, or a pick and a message) end one at a time: the
// session is still taken up until the last one ends.
func TestSwitchMarksCount(t *testing.T) {
	s := &switchShare{}
	first, second := s.mark(sidA), s.mark(sidA)
	first()
	if !s.busy(sidA) {
		t.Error("the first mark's end cleared the second")
	}
	second()
	if s.busy(sidA) {
		t.Error("still marked after both ended")
	}
}
