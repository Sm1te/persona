package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/runtimepkg"
)

// fakeSDKBridge stands in for integrations/agent-sdk's bridge.mjs (run by /bin/sh instead of Node.js): it keeps its
// persona_start line and every later stdin line under $SDK_FAKE_DIR, says ready, answers control requests, and plays a
// fixed turn for each user message (no result while $SDK_FAKE_DIR/hold exists).
const fakeSDKBridge = `IFS= read -r start
printf '%s\n' "$start" > "$SDK_FAKE_DIR/start.json"
printf '%s\n' "$0 $*" > "$SDK_FAKE_DIR/argv"
echo '{"type":"persona_bridge","event":"ready","sdk_version":"0.0.0-test","claude_version":"9.9.9"}'
echo '{"type":"system","subtype":"init","tools":[]}'
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$SDK_FAKE_DIR/stdin.jsonl"
  case "$line" in
    *'"type":"control_request"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
      if [ -f "$SDK_FAKE_DIR/control-extra.jsonl" ]; then cat "$SDK_FAKE_DIR/control-extra.jsonl"; fi
      if [ -f "$SDK_FAKE_DIR/control-hold" ]; then
        printf '%s\n' "$id" > "$SDK_FAKE_DIR/control-waiting"
        while [ -f "$SDK_FAKE_DIR/control-hold" ]; do sleep 0.02; done
      fi
      if [ -f "$SDK_FAKE_DIR/control-exit" ]; then exit 0; fi
      case "$line" in
        *'"subtype":"set_permission_mode"'*|*'"subtype":"apply_flag_settings"'*)
          case "$line" in
            *'"subtype":"set_permission_mode"'*) error_file="$SDK_FAKE_DIR/control-mode-error.json" ;;
            *) error_file="$SDK_FAKE_DIR/control-effort-error.json" ;;
          esac
          if [ -f "$error_file" ]; then
            printf '{"type":"control_response","response":{"subtype":"error","request_id":"%s","error":%s}}\n' "$id" "$(cat "$error_file")"
          else
            echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"$id\"}}"
          fi ;;
        *) echo "{\"type\":\"control_response\",\"response\":{\"subtype\":\"error\",\"request_id\":\"$id\",\"error\":\"unsupported\"}}" ;;
      esac ;;
    *'"type":"user"'*)
      echo '{"type":"stream_event","event":{"type":"message_start","message":{"usage":{"input_tokens":5}}},"parent_tool_use_id":null}'
      echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hel"}},"parent_tool_use_id":null}'
      sleep 0.1
      echo '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}},"parent_tool_use_id":null}'
      echo '{"type":"stream_event","event":{"type":"message_stop"},"parent_tool_use_id":null}'
      if [ ! -e "$SDK_FAKE_DIR/hold" ]; then echo '{"type":"result","subtype":"success","result":"hello"}'; fi ;;
  esac
done
`

// fakeCLI is a claude -p that keeps its args and finishes each turn at once.
const fakeCLI = `printf '%s\n' "$*" >> "$SDK_FAKE_DIR/cli-argv"
printf '%s\n' "$CLAUDE_CONFIG_DIR" > "$SDK_FAKE_DIR/cli-profile"
echo '{"type":"system","subtype":"init"}'
while IFS= read -r line; do
  case "$line" in *'"type":"user"'*) echo '{"type":"result","subtype":"success"}' ;; esac
done
`

const testKey = "sk-test-0123456789abcdef"

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type sdkFixture struct {
	n   *Node
	fh  *fakeHub
	dir string // $SDK_FAKE_DIR
	log *lockedBuf
}

// sdkTest is a node whose Agent SDK runs fakeSDKBridge, with fakeCLI as the machine's claude for the CLI.
func sdkTest(t *testing.T) *sdkFixture {
	t.Helper()
	fakeClaudeHome(t, fakeCLI)
	dir := t.TempDir()
	t.Setenv("SDK_FAKE_DIR", dir)
	load, find, claude := sdkLoadPackage, sdkFindJS, sdkClaude
	t.Cleanup(func() { sdkLoadPackage, sdkFindJS, sdkClaude = load, find, claude })
	files := []runtimepkg.File{{Path: "agent-sdk/package.json", Data: []byte(`{}`)}, {Path: "agent-sdk/dist/bridge.mjs", Data: []byte(fakeSDKBridge)}}
	sdkLoadPackage = func() (*runtimepkg.Package, error) {
		return &runtimepkg.Package{Name: sdkPackage, Hash: runtimepkg.Hash(files), Files: files}, nil
	}
	sdkFindJS = func(context.Context, string) (jsRuntime, error) { return jsRuntime{Path: "/bin/sh"}, nil }
	sdkClaude = func() (string, error) { return "/opt/test/bin/claude", nil }
	n := codexTestNode(t)
	n.dir = t.TempDir()
	buf := &lockedBuf{}
	n.logger = log.New(buf, "", 0)
	fh := withFakeHub(t, n)
	t.Cleanup(func() {
		n.mu.Lock()
		ids := make([]string, 0, len(n.procs))
		for id := range n.procs {
			ids = append(ids, id)
		}
		n.mu.Unlock()
		for _, id := range ids {
			n.stopAssistant(id)
		}
	})
	return &sdkFixture{n: n, fh: fh, dir: dir, log: buf}
}

func sdkExec(runtime string) modelExecution {
	return modelExecution{Runtime: runtime, Fingerprint: "fp-" + runtime}
}

func sdkWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *sdkFixture) file(name string) string {
	b, _ := os.ReadFile(filepath.Join(f.dir, name))
	return string(b)
}

func (f *sdkFixture) start(t *testing.T) map[string]any {
	t.Helper()
	sdkWait(t, "persona_start", func() bool { return strings.HasSuffix(f.file("start.json"), "\n") })
	var line struct {
		Type    string         `json:"type"`
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal([]byte(f.file("start.json")), &line); err != nil || line.Type != "persona_start" {
		t.Fatalf("first stdin line: %q (%v)", f.file("start.json"), err)
	}
	return line.Options
}

func (f *sdkFixture) proc(id string) *assistantProc {
	f.n.mu.Lock()
	defer f.n.mu.Unlock()
	return f.n.procs[id]
}

// idle waits for the end of the turn as the node publishes it (assistantSessions): an idle process and no model
// recovery still holding the turn. busy alone is not enough: a recovery that moves to the next model registers the
// replacement process before it marks it busy and sends it the retry, and that gap is still the same turn (#3815).
func (f *sdkFixture) idle(t *testing.T, id string) {
	t.Helper()
	sdkWait(t, "the turn's result", func() bool { return f.turnOver(id) })
}

// turnOver: conversation id has a registered process that is not busy, and no recovery holds its turn.
func (f *sdkFixture) turnOver(id string) bool {
	f.n.mu.Lock()
	p, recovering := f.n.procs[id], f.n.recoveries[id] != nil
	f.n.mu.Unlock()
	if p == nil || recovering {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.busy
}

func (f *sdkFixture) session(t *testing.T, id string) proto.Session {
	t.Helper()
	for _, s := range f.n.assistantSessions() {
		if s.SessionID == id {
			return s
		}
	}
	t.Fatalf("no session %s", id)
	return proto.Session{}
}

// The CLI flags map onto the SDK's options one by one.
func TestSDKOptionsMapTheCLIFlags(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(settings, []byte(`{"hooks":{"Stop":[]},"env":{"KEEP":"1","ANTHROPIC_MODEL":"old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := sdkStart{id: "id-1", cwd: "/w", model: "m1", permissionMode: proto.ModeBypass, resume: true,
		mcp: map[string]any{"mcpServers": map[string]any{"persona": map[string]any{"command": "personad"}}}, allowedTools: "a,b",
		settings: settings, plugin: "/hooks", launcherArgs: []string{"--model", "x", "--fallback-model", "m2", "--strict", "--max-turns=3", "word"},
		env: map[string]string{"ANTHROPIC_MODEL": "m1"}}
	o, err := s.options("/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(o)
	want := `{"allowedTools":["a","b"],"cwd":"/w","extraArgs":{"fallback-model":"m2","max-turns":"3","strict":null},` +
		`"includePartialMessages":true,"mcpServers":{"persona":{"command":"personad"}},"model":"m1",` +
		`"pathToClaudeCodeExecutable":"/bin/claude","permissionMode":"bypassPermissions","plugins":[{"path":"/hooks","type":"local"}],` +
		`"resume":"id-1","settings":{"env":{"ANTHROPIC_MODEL":"m1","KEEP":"1"},"hooks":{"Stop":[]}}}`
	if string(b) != want {
		t.Fatalf("options:\n got %s\nwant %s", b, want)
	}
	s.resume, s.permissionMode, s.plugin, s.settings, s.env, s.model, s.launcherArgs = false, "", "", "", nil, "", nil
	o, _ = s.options("/bin/claude")
	if o["sessionId"] != "id-1" || o["resume"] != nil || o["settings"] != nil || o["plugins"] != nil || o["extraArgs"] != nil || o["model"] != nil {
		t.Fatalf("a new conversation: %v", o)
	}
}

// A guest's stdio config stays separate until the bridge registers its declared tools through the SDK.
func TestSDKOptionsKeepGuestMCPTools(t *testing.T) {
	s := sdkStart{id: "guest-1", cwd: "/guest", mcp: map[string]any{"mcpServers": map[string]any{}},
		guestMCP:     map[string]any{"persona": map[string]any{"command": "personad", "env": map[string]string{"PERSONA_GUEST": "1"}}},
		allowedTools: "mcp__persona__larkbridge_docs", strictMCP: true}
	o, err := s.options("/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := o["tools"]; exists || o["strictMcpConfig"] != true || o["plugins"] != nil {
		t.Fatalf("guest start: %v", o)
	}
	if got, _ := o["guestMcpServers"].(map[string]any)["persona"].(map[string]any)["command"]; got != "personad" {
		t.Fatalf("guest MCP proxy: %v", o["guestMcpServers"])
	}
}

// A managed launcher on the SDK: its key is in the bridge's environment and the pinned settings env, never on a
// command line.
func TestAssistantSDKStartKeepsTheKeyOffTheCommandLine(t *testing.T) {
	f := sdkTest(t)
	f.n.setManaged([]proto.ManagedLauncher{{Name: "gw", BaseURL: "https://gw.example.com", APIKey: testKey, Model: "m1",
		Args: []string{"--fallback-model", "m2"}}})
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "gw", "hello", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	o := f.start(t)
	if o["sessionId"] != id || o["pathToClaudeCodeExecutable"] != "/opt/test/bin/claude" || o["includePartialMessages"] != true {
		t.Fatalf("options: %v", o)
	}
	if ex, _ := o["extraArgs"].(map[string]any); ex["fallback-model"] != "m2" {
		t.Fatalf("the launcher's args: %v", o["extraArgs"])
	}
	if m, _ := o["mcpServers"].(map[string]any); m["persona"] == nil {
		t.Fatalf("mcpServers: %v", o["mcpServers"])
	}
	env, _ := o["settings"].(map[string]any)["env"].(map[string]any)
	if env["ANTHROPIC_API_KEY"] != testKey || env["ANTHROPIC_BASE_URL"] != "https://gw.example.com" {
		t.Fatalf("settings env not pinned: %v", env)
	}
	settings := o["settings"]
	delete(o, "settings")
	if b, _ := json.Marshal(o); strings.Contains(string(b), testKey) {
		t.Fatalf("the key outside settings.env: %s", b)
	}
	o["settings"] = settings
	p := f.proc(id)
	if strings.Contains(strings.Join(p.cmd.Args, " "), testKey) || strings.Contains(f.file("argv"), testKey) {
		t.Fatalf("the key on the command line: %v", p.cmd.Args)
	}
	if !strings.Contains(strings.Join(p.cmd.Env, "\n"), "ANTHROPIC_API_KEY="+testKey) {
		t.Fatal("the key is not in the bridge's environment")
	}
	if strings.Contains(f.log.String(), testKey) {
		t.Fatal("the key in the log")
	}
}

// The main conversation's provider that follows the computer's proxy adds its bypass list to the one its claude would
// read without it, the user's settings env's, else the node's (#6238); one with its own proxy keeps its own.
func TestSDKStartMergesAFollowingProvidersBypassList(t *testing.T) {
	agentSandboxFakes(t, macSandbox)
	b := sdkBridge{js: jsRuntime{Path: "/opt/bin/node"}, script: "/x/bridge.mjs", claude: "/opt/bin/claude"}
	n := agentNode(t, false)
	profile := filepath.Join(n.home, "profile")
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "settings.json"), `{"env":{"NO_PROXY":"wrong.example"}}`)
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	n.setManaged([]proto.ManagedLauncher{
		{Name: "f", Kind: "api", BaseURL: "https://gw.example.com", Model: "m1", NoProxy: "gw.example.com"},
		{Name: "o", Kind: "api", BaseURL: "https://gw.example.com", Model: "m1", Proxy: "http://proxy.example:3128", NoProxy: "gw.example.com"},
		{Name: "p", Kind: "api", BaseURL: "https://gw.example.com", Model: "m1", NoProxy: "gw.example.com", Env: "CLAUDE_CONFIG_DIR=" + other},
	})
	pinned := func(launcher string) (any, any) {
		t.Helper()
		s := sdkStart{id: "s1", cwd: filepath.Join(n.dir, "assistant"), configDir: profile, mcp: map[string]any{}}
		cmd, first, err := n.sdkCommand(b, launcher, s, []string{"PATH=/usr/bin", "no_proxy=localhost"})
		if err != nil {
			t.Fatal(err)
		}
		if got := envValue(cmd.Environ(), "CLAUDE_CONFIG_DIR"); got != profile {
			t.Fatalf("SDK process uses %q, want the recorded profile %q", got, profile)
		}
		var start struct {
			Options struct{ Settings struct{ Env map[string]any } }
		}
		if err := json.Unmarshal(first, &start); err != nil {
			t.Fatal(err)
		}
		if got := start.Options.Settings.Env["CLAUDE_CONFIG_DIR"]; got != profile {
			t.Fatalf("SDK settings use %v, want the recorded profile %q", got, profile)
		}
		return start.Options.Settings.Env["NO_PROXY"], start.Options.Settings.Env["no_proxy"]
	}
	if up, low := pinned("f"); up != "gw.example.com,localhost" || low != up {
		t.Errorf("with the node's list: %v %v", up, low)
	}
	// The user's settings set the upper-case one: it is that spelling's; the lower-case one is still the node's.
	writeFile(t, filepath.Join(profile, "settings.json"), `{"env":{"NO_PROXY":".corp.example"}}`)
	if up, low := pinned("f"); up != "gw.example.com,.corp.example" || low != "gw.example.com,localhost" {
		t.Errorf("with the user's settings' list: %v %v", up, low)
	}
	if up, low := pinned("o"); up != "gw.example.com" || low != up {
		t.Errorf("its own proxy: %v %v", up, low)
	}
	if up, low := pinned("p"); up != "gw.example.com,.corp.example" || low != "gw.example.com,localhost" {
		t.Errorf("a later provider edit moved the recorded profile: %v %v", up, low)
	}
	l, _ := n.managedLauncher("p")
	path, err := n.mergeManagedSettings("p", l, "", profile)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := readLauncherSettings(path, n.home, n.home)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := settings["env"].(map[string]any)
	if _, pinned := env["CLAUDE_CONFIG_DIR"]; pinned || env["NO_PROXY"] != "gw.example.com,.corp.example" {
		t.Fatalf("CLI settings moved the recorded profile: %v", env)
	}
}

// Deltas and the result come through as from the CLI; the modes go as control requests the bridge answers.
func TestAssistantSDKStreamsAndControlRequests(t *testing.T) {
	f := sdkTest(t)
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "acceptEdits", "high", sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	sdkWait(t, "the delta", func() bool {
		f.fh.mu.Lock()
		defer f.fh.mu.Unlock()
		for _, d := range f.fh.deltas {
			if d == "hello" {
				return true
			}
		}
		return false
	})
	in := f.file("stdin.jsonl")
	if !strings.Contains(in, `"subtype":"set_permission_mode"`) || !strings.Contains(in, `"mode":"acceptEdits"`) ||
		!strings.Contains(in, `"subtype":"apply_flag_settings"`) || !strings.Contains(in, `"effortLevel":"high"`) {
		t.Fatalf("control requests: %s", in)
	}
	logs := f.log.String()
	if strings.Contains(logs, "control request failed") || !strings.Contains(logs, "agent sdk 0.0.0-test ready") {
		t.Fatalf("log: %s", logs)
	}
	if s := f.session(t, id); s.Runtime != proto.AssistantRuntimeAgentSDK || s.RuntimeFallback != "" {
		t.Fatalf("session runtime %q fallback %q", s.Runtime, s.RuntimeFallback)
	}
	if f.proc(id).sdkVersion != "0.0.0-test" {
		t.Fatal("sdk version not kept")
	}
	if f.file("cli-argv") != "" {
		t.Fatal("the CLI ran too")
	}
}

// Interrupting stops the bridge (and the claude it runs, its process group) like the CLI.
func TestAssistantSDKInterrupt(t *testing.T) {
	f := sdkTest(t)
	if err := os.WriteFile(filepath.Join(f.dir, "hold"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	p := f.proc(id)
	sdkWait(t, "the turn", func() bool { return strings.Contains(f.file("stdin.jsonl"), `"type":"user"`) })
	if err := f.n.assistantInterrupt(id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge is still running")
	}
	if f.proc(id) != nil {
		t.Fatal("still kept")
	}
}

// Without the package, Node.js 22+ or claude, the conversation runs on the CLI and says why.
func TestAssistantSDKFallsBackToTheCLI(t *testing.T) {
	cases := []struct {
		name, reason string
		break_       func()
	}{
		{"package", "没有带 Agent SDK 桥", func() { sdkLoadPackage = func() (*runtimepkg.Package, error) { return nil, nil } }},
		{"node", "没有 Node.js 22 及以上", func() {
			sdkFindJS = func(context.Context, string) (jsRuntime, error) {
				return jsRuntime{}, errors.New("node: Node.js v20.1.0 is older than 22")
			}
		}},
		{"claude", "找不到 claude", func() { sdkClaude = func() (string, error) { return "", errors.New("not found") } }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := sdkTest(t)
			tc.break_()
			id, err := f.n.assistantSend(context.Background(), "", t.TempDir(), "claude", "hello", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK))
			if err != nil {
				t.Fatal(err)
			}
			f.idle(t, id)
			s := f.session(t, id)
			if s.Runtime != "" || !strings.Contains(s.RuntimeFallback, tc.reason) {
				t.Fatalf("runtime %q fallback %q", s.Runtime, s.RuntimeFallback)
			}
			if !strings.Contains(f.file("cli-argv"), "--session-id "+id) || f.file("start.json") != "" {
				t.Fatalf("did not run on the CLI: %q", f.file("cli-argv"))
			}
			if !strings.Contains(f.log.String(), "running on the CLI, not the Agent SDK: ") || !strings.Contains(f.log.String(), tc.reason) {
				t.Fatalf("log: %s", f.log.String())
			}
		})
	}
}

// A launcher that is a shell command on this machine can't run on the SDK.
func TestSDKPrepareRefusesAShellLauncher(t *testing.T) {
	f := sdkTest(t)
	if _, why := f.n.sdkPrepare(context.Background(), "kcc"); !strings.Contains(why, "kcc") {
		t.Fatalf("reason %q", why)
	}
	if b, why := f.n.sdkPrepare(context.Background(), "claude"); why != "" || b.claude != "/opt/test/bin/claude" ||
		!strings.HasSuffix(b.script, filepath.Join("agent-sdk", "dist", "bridge.mjs")) {
		t.Fatalf("claude: %+v %q", b, why)
	}
}

// Switching an idle conversation from the CLI to the SDK starts the bridge resuming it.
func TestAssistantSDKRuntimeChangeRestartsWithResume(t *testing.T) {
	f := sdkTest(t)
	cwd := t.TempDir()
	id, err := f.n.assistantSend(context.Background(), "", cwd, "claude", "hello", nil, "", "", sdkExec(""))
	if err != nil {
		t.Fatal(err)
	}
	f.idle(t, id)
	cli := f.proc(id)
	transcript := filepath.Join(t.TempDir(), id+".jsonl")
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.n.mu.Lock()
	f.n.paths[id] = transcript
	f.n.mu.Unlock()

	if _, err := f.n.assistantSend(context.Background(), id, cwd, "claude", "again", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK)); err != nil {
		t.Fatal(err)
	}
	if o := f.start(t); o["resume"] != id || o["sessionId"] != nil {
		t.Fatalf("not resumed: %v", o)
	}
	select {
	case <-cli.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the CLI process was not stopped")
	}
	if st, _ := f.n.state.get(id); st.Runtime != proto.AssistantRuntimeAgentSDK {
		t.Fatalf("state runtime %q", st.Runtime)
	}
	// A session.send without a runtime keeps the one the conversation runs on.
	if r := f.n.sessionRuntime(id, "", false); r != proto.AssistantRuntimeAgentSDK {
		t.Fatalf("session.send runtime %q", r)
	}
	if r := f.n.sessionRuntime(id, proto.AssistantRuntimeCLI, false); r != "" {
		t.Fatalf("cli asked: %q", r)
	}
	// The hub resolved the main chat's configuration (a fingerprint came) and sent no runtime: the CLI, not the one before.
	if r := f.n.sessionRuntime(id, "", true); r != "" {
		t.Fatalf("hub-resolved CLI ran on %q", r)
	}
}

// A runtime switch while it answers waits (#1554): the message goes to the running bridge.
func TestAssistantSDKBusyKeepsTheRunningProcess(t *testing.T) {
	f := sdkTest(t)
	if err := os.WriteFile(filepath.Join(f.dir, "hold"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	id, err := f.n.assistantSend(context.Background(), "", cwd, "claude", "one", nil, "", "", sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	p := f.proc(id)
	sdkWait(t, "the turn", func() bool { return strings.Contains(f.file("stdin.jsonl"), `"one"`) })
	if _, err := f.n.assistantSend(context.Background(), id, cwd, "claude", "two", nil, "", "", sdkExec("")); err != nil {
		t.Fatal(err)
	}
	sdkWait(t, "the second message", func() bool { return strings.Contains(f.file("stdin.jsonl"), `"two"`) })
	if f.proc(id) != p || !p.alive() || f.file("cli-argv") != "" {
		t.Fatal("a busy conversation was restarted on the CLI")
	}
	if s := f.session(t, id); s.Runtime != proto.AssistantRuntimeAgentSDK {
		t.Fatalf("runtime %q", s.Runtime)
	}
}

// The JS runtime is looked up once, while it is still there: each spawn would otherwise start `node -e` per candidate.
func TestCachedJSRuntimeLooksUpOnce(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "node")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fail := false
	find := cachedJSRuntime(func(context.Context, string) (jsRuntime, error) {
		calls++
		if fail {
			return jsRuntime{}, errors.New("no node")
		}
		return jsRuntime{Path: bin}, nil
	})
	for range 3 {
		if js, err := find(context.Background(), dir); err != nil || js.Path != bin {
			t.Fatal(js, err)
		}
	}
	if calls != 1 {
		t.Fatalf("looked up %d times", calls)
	}
	os.Remove(bin) // gone: looked up again, and a failure is not remembered
	fail = true
	if _, err := find(context.Background(), dir); err == nil {
		t.Fatal("a removed runtime was still returned")
	}
	if _, err := find(context.Background(), dir); err == nil || calls != 3 {
		t.Fatalf("failure remembered: %d", calls)
	}
}

// The bridge's error line (printed just before it exits) is not a start: only its ready, or the SDK's own output, is.
func TestSDKBridgeErrorIsNotAStart(t *testing.T) {
	n := codexTestNode(t)
	n.logger = log.New(&lockedBuf{}, "", 0)
	for _, c := range []struct {
		line    string
		started bool
	}{
		{`{"type":"persona_bridge","event":"error","error":"start: boom"}`, false},
		{`{"type":"persona_bridge","event":"ready","sdk_version":"0.0.0-test"}`, true},
		{`{"type":"system","subtype":"init","tools":[]}`, true},
	} {
		p := &assistantProc{id: "abcdef0123456789", ready: make(chan struct{})}
		n.readAssistant(p, strings.NewReader(c.line+"\n"))
		select {
		case <-p.ready:
			if !c.started {
				t.Errorf("%s: counted as a start", c.line)
			}
		default:
			if c.started {
				t.Errorf("%s: not counted as a start", c.line)
			}
		}
	}
}

// An alias's path values come as the shell would have expanded them: the SDK runs no shell.
func TestExtraArgsExpandsHome(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	got := extraArgs([]string{"--settings", "~/.claude/s.json", "--add-dir=~/w", "--dangerously-skip-permissions"})
	if got["settings"] != "/home/me/.claude/s.json" || got["add-dir"] != "/home/me/w" || got["dangerously-skip-permissions"] != nil {
		t.Fatalf("%v", got)
	}
}

// An npm-installed claude under nvm is discovered by sdkPrepare and prepended to the bridge PATH.
func TestSDKPrepareAndCommandWithNVMClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir()) // restricted service PATH missing nvm

	nvmBin := filepath.Join(home, ".nvm", "versions", "node", "v22.5.0", "bin")
	if err := os.MkdirAll(nvmBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeClaude := filepath.Join(nvmBin, "claude")
	fakeNode := filepath.Join(nvmBin, "node")
	if err := os.WriteFile(fakeClaude, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeNode, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	load, find, claude := sdkLoadPackage, sdkFindJS, sdkClaude
	t.Cleanup(func() { sdkLoadPackage, sdkFindJS, sdkClaude = load, find, claude })
	files := []runtimepkg.File{
		{Path: "agent-sdk/package.json", Data: []byte(`{}`)},
		{Path: "agent-sdk/dist/bridge.mjs", Data: []byte(fakeSDKBridge)},
	}
	sdkLoadPackage = func() (*runtimepkg.Package, error) {
		return &runtimepkg.Package{Name: sdkPackage, Hash: runtimepkg.Hash(files), Files: files}, nil
	}
	sdkFindJS = func(context.Context, string) (jsRuntime, error) {
		return jsRuntime{Path: fakeNode}, nil
	}
	sdkClaude = claudeExecutable // use real resolver

	n := &Node{home: home, dir: t.TempDir(), bin: "personad"}
	bridge, fallback := n.sdkPrepare(context.Background(), "claude")
	if fallback != "" {
		t.Fatalf("unexpected fallback: %s", fallback)
	}
	if bridge.claude != fakeClaude {
		t.Fatalf("bridge claude = %q, want %q", bridge.claude, fakeClaude)
	}

	start := sdkStart{
		id:           "s-nvm",
		cwd:          t.TempDir(),
		allowedTools: "bash",
		mcp:          map[string]any{"mcpServers": map[string]any{}},
	}
	cmd, _, err := n.sdkCommand(bridge, "claude", start, []string{"PATH=/usr/bin:/bin"})
	if err != nil {
		t.Fatal(err)
	}
	cmdPATH := envValue(cmd.Env, "PATH")
	if !strings.HasPrefix(cmdPATH, nvmBin+string(os.PathListSeparator)) {
		t.Fatalf("bridge PATH %q does not start with nvm directory %q", cmdPATH, nvmBin)
	}

	// When claude is missing, fallback provides service environment notice and actionable remediation
	if err := os.Remove(fakeClaude); err != nil {
		t.Fatal(err)
	}
	_, missingFallback := n.sdkPrepare(context.Background(), "claude")
	if !strings.Contains(missingFallback, proto.FallbackNoClaude) {
		t.Fatalf("missingFallback does not contain FallbackNoClaude: %q", missingFallback)
	}
	if !strings.Contains(missingFallback, "node service environment") {
		t.Fatalf("missingFallback does not mention service environment: %q", missingFallback)
	}
	if !strings.Contains(missingFallback, "symlink into ~/.local/bin") {
		t.Fatalf("missingFallback does not suggest symlink: %q", missingFallback)
	}
}
