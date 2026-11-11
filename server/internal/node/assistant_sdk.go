package node

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/runtimepkg"
)

// The assistant on the Agent SDK (#2455): instead of `claude -p` through the launcher's shell command, the node runs
// the bridge (integrations/agent-sdk, package sdkPackage) with Node.js 22+. Its first stdin line is persona_start with
// the SDK query() options the CLI flags map to; after it, stdin and stdout are the CLI's stream-json, so everything
// else (readAssistant, modes.go's control requests, /compact, interrupt and reaping by process group) works on both.
// Without Node.js 22+, the package, or claude, or for a launcher that is a shell command the SDK can't run, the
// conversation runs on the CLI and says why (proto.Session.RuntimeFallback).

const sdkPackage = "persona-agent-sdk-runtime"

// sdkBridgePath is the bridge script inside the package.
var sdkBridgePath = filepath.Join("agent-sdk", "dist", "bridge.mjs")

// What the SDK runs on; tests replace them.
var (
	sdkLoadPackage = func() (*runtimepkg.Package, error) { return runtimepkg.Load(sdkPackage) }
	sdkFindJS      = cachedJSRuntime(findJSRuntime)
	sdkClaude      = claudeExecutable
)

// cachedJSRuntime remembers the runtime find found (each spawn would start `node -e` for every candidate) while it is
// still there and PERSONA_NODEJS is the same; a failure is not remembered, so installing Node is picked up.
func cachedJSRuntime(find func(context.Context, string) (jsRuntime, error)) func(context.Context, string) (jsRuntime, error) {
	var mu sync.Mutex
	var key string
	var got *jsRuntime
	return func(ctx context.Context, home string) (jsRuntime, error) {
		k := home + "\x00" + os.Getenv("PERSONA_NODEJS")
		mu.Lock()
		defer mu.Unlock()
		if got != nil && key == k {
			if st, err := os.Stat(got.Path); err == nil && !st.IsDir() {
				return *got, nil
			}
		}
		js, err := find(ctx, home)
		if err != nil {
			got = nil
			return js, err
		}
		key, got = k, &js
		return js, nil
	}
}

// assistantRuntimeOf is a runtime param as kept: "" for the CLI (also "cli" and anything unknown), else agentsdk.
func assistantRuntimeOf(r string) string {
	if r == proto.AssistantRuntimeAgentSDK {
		return r
	}
	return ""
}

// sessionRuntime is the runtime a message to conversation id runs on: the one asked for, else the one it ran on
// (a hub that sends none with session.send). resolved: the hub filled in the model configuration (a fingerprint
// came), where no runtime is the CLI — the hub drops the param for it — not "as before".
func (n *Node) sessionRuntime(id, asked string, resolved bool) string {
	if asked != "" || resolved {
		return assistantRuntimeOf(asked)
	}
	st, _ := n.state.get(id)
	return st.Runtime
}

// sdkBridge is what runs the bridge: Node.js, the bridge script, and the claude the SDK starts.
type sdkBridge struct {
	js     jsRuntime
	script string
	claude string
}

// sdkPrepare finds what the bridge needs, or says why the conversation falls back to the CLI.
func (n *Node) sdkPrepare(ctx context.Context, launcher string) (sdkBridge, string) {
	if _, managed := n.managedLauncher(launcher); !managed && launcher != "claude" {
		return sdkBridge{}, fmt.Sprintf(proto.FallbackShellLauncher, launcher)
	}
	pkg, err := sdkLoadPackage()
	if err != nil {
		return sdkBridge{}, proto.FallbackReadBridge + err.Error()
	}
	if pkg == nil {
		return sdkBridge{}, proto.FallbackNoBridge
	}
	dir, err := writePackage(filepath.Join(n.dir, "runtime-packages", sdkPackage), pkg)
	if err != nil {
		return sdkBridge{}, proto.FallbackWriteBridge + err.Error()
	}
	script := filepath.Join(dir, sdkBridgePath)
	if _, err := os.Stat(script); err != nil {
		return sdkBridge{}, proto.FallbackNoBridgeScript
	}
	js, err := sdkFindJS(ctx, userHome())
	if err != nil {
		return sdkBridge{}, proto.FallbackNoNode + err.Error()
	}
	claude, err := sdkClaude()
	if err != nil {
		return sdkBridge{}, proto.FallbackNoClaude + err.Error()
	}
	return sdkBridge{js: js, script: script, claude: claude}, ""
}

// sdkStart is a claude start as spawnClaude puts it together, CLI flag by flag.
type sdkStart struct {
	id, cwd, model, permissionMode string
	configDir                      string
	resume                         bool
	mcp                            map[string]any // --mcp-config's {"mcpServers": …}
	allowedTools                   string         // --allowedTools
	tools                          string         // --tools: built-ins only; MCP tools stay available
	deny                           []string       // permission rules denied whatever allows them (healthdeny.go)
	settings                       string         // --settings: a file, or ""
	plugin                         string         // --plugin-dir, or ""
	launcherArgs                   []string       // a managed launcher's own claude args
	env                            map[string]string
	compaction                     *assistantCompaction
}

// options are the persona_start options: the SDK's query() options for the start (CONTRACT §3).
func (s sdkStart) options(claude string) (map[string]any, error) {
	o := map[string]any{
		"cwd":                        s.cwd,
		"mcpServers":                 s.mcp["mcpServers"],
		"includePartialMessages":     true,
		"pathToClaudeCodeExecutable": claude,
	}
	if tools := strings.Split(s.allowedTools, ","); s.allowedTools != "" {
		o["allowedTools"] = tools
	}
	if s.tools != "" {
		o["tools"] = strings.Split(s.tools, ",")
	}
	if s.model != "" {
		o["model"] = s.model
	}
	if s.permissionMode != "" {
		o["permissionMode"] = s.permissionMode
	}
	if s.resume {
		o["resume"] = s.id
	} else {
		o["sessionId"] = s.id
	}
	if s.plugin != "" {
		o["plugins"] = []map[string]string{{"type": "local", "path": s.plugin}}
	}
	settings := map[string]any{}
	if s.settings != "" {
		b, err := os.ReadFile(s.settings)
		if err != nil && !errors.Is(err, fs.ErrNotExist) { // not written yet: claude would go without it too
			return nil, fmt.Errorf("read the start's settings: %w", err)
		}
		if err == nil && json.Unmarshal(b, &settings) != nil {
			return nil, errors.New("the start's settings are not a JSON object")
		}
	}
	if len(s.env) > 0 { // pinned over the user's ~/.claude/settings.json env
		env, _ := settings["env"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		for k, v := range pinnedEnv(s.env) {
			env[k] = v
		}
		settings["env"] = env
	}
	if s.compaction != nil {
		env, _ := settings["env"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		for _, kv := range s.compaction.env() {
			k, v, _ := strings.Cut(kv, "=")
			env[k] = v
		}
		settings["env"] = env
	}
	if len(s.deny) > 0 {
		perms, _ := settings["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		have, _ := perms["deny"].([]any)
		for _, r := range s.deny {
			have = append(have, r)
		}
		perms["deny"] = have
		settings["permissions"] = perms
	}
	if len(settings) > 0 {
		o["settings"] = settings
	}
	args := s.launcherArgs
	if s.model != "" {
		args = withoutModelArgs(args)
	}
	if extra := extraArgs(args); len(extra) > 0 {
		o["extraArgs"] = extra
	}
	return o, nil
}

// extraArgs is CLI args as the SDK's extraArgs: --flag value → {"flag": "value"}, a flag with no value → null.
// Words that are not flags are dropped: the SDK can't pass them (it passes every key as --key). A value is a path as
// the shell would have expanded it (an alias's `--settings ~/…`): no shell runs here.
func extraArgs(args []string) map[string]any {
	out := map[string]any{}
	for i := 0; i < len(args); i++ {
		name, ok := strings.CutPrefix(args[i], "--")
		if !ok || name == "" {
			continue
		}
		if k, v, has := strings.Cut(name, "="); has {
			out[k] = proto.ExpandHome(v, userHome())
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out[name] = proto.ExpandHome(args[i+1], userHome())
			i++
			continue
		}
		out[name] = nil
	}
	return out
}

// sdkCommand is the bridge's command and its first stdin line. base is the assistant's environment; a managed
// launcher's provider env replaces whatever the node inherited for a gateway, as `personad launch` does. Nothing secret
// goes on the command line: the key is in the environment and in the persona_start line.
func (n *Node) sdkCommand(b sdkBridge, launcher string, s sdkStart, base []string) (*exec.Cmd, []byte, error) {
	configDir := n.assistantConfigDir(launcher, s.configDir)
	base = n.claudeProcessEnv(base, configDir)
	if l, managed := n.managedLauncher(launcher); managed {
		// the main conversation's provider env (launcherenv.go); its values hold the key: never logged
		s.env, s.launcherArgs = providerEnv(l, s.model, "", true), l.Args
		if configDir != "" {
			s.env["CLAUDE_CONFIG_DIR"] = configDir
		} else {
			delete(s.env, "CLAUDE_CONFIG_DIR") // "default": unset, as claudeProcessEnv does
		}
		if mergesNoProxy(l) {
			// As `personad launch` and its settings file do (#6238): the provider's hosts are added to the bypass lists
			// this claude would read without it, the user's settings env's (applied over the environment), else the node's.
			configPath := configDir
			if configPath == "" {
				configPath = filepath.Join(n.home, ".claude")
			}
			user := n.userNoProxy(l, configPath)
			s.env["NO_PROXY"], s.env["no_proxy"] = addNoProxy(s.env["NO_PROXY"], func(k string) string {
				return cmp.Or(user[k], envValue(base, k))
			})
		}
		base = withoutEnv(base, envClearFor(l))
		own := envList(s.env)
		base = identity.Environment(base, own...)
		// as `personad launch` does: a proxy inherited that no longer answers holds every request (deadproxy.go)
		base = withoutDeadProxies(base, own, l.BaseURL, func(msg string) { n.logf("assistant: launcher %s: %s", launcher, msg) })
	}
	if s.env == nil {
		s.env = map[string]string{}
	}
	if configDir != "" {
		s.env["CLAUDE_CONFIG_DIR"] = configDir
	} else {
		delete(s.env, "CLAUDE_CONFIG_DIR") // "default": unset, as claudeProcessEnv does
	}
	if s.compaction != nil {
		base = identity.Environment(base, s.compaction.env()...)
	}
	if claudeDir := filepath.Dir(b.claude); claudeDir != "" && claudeDir != "." {
		base = prependEnvPath(base, claudeDir)
	}
	opts, err := s.options(b.claude)
	if err != nil {
		return nil, nil, err
	}
	line, err := json.Marshal(map[string]any{"type": "persona_start", "options": opts})
	if err != nil {
		return nil, nil, err
	}
	// The bridge in the agent sandbox when it is on (agentsandbox.go): the claude it starts is in it too.
	settings := settingsArgs(s.launcherArgs)
	innerArgs := append([]string(nil), s.launcherArgs...)
	if s.settings != "" {
		settings, innerArgs = append(settings, s.settings), append(innerArgs, "--settings", s.settings)
	}
	argv, env, _ := wrapAgent("assistant-sdk", n.home, n.dir, s.cwd, settings,
		func() string { return claudeInner(innerArgs, base, s.cwd, n.home, n.claudeConfigDirs()) }, []string{b.js.Path, b.script}, b.js.env(base), n.logger.Printf)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = s.cwd
	cmd.Env = env
	return cmd, line, nil
}

// sdkBridgeEvent logs a line of the bridge's own (persona_bridge), and says whether line was one.
func (n *Node) sdkBridgeEvent(p *assistantProc, line []byte) bool {
	var ev struct {
		Type       string `json:"type"`
		Event      string `json:"event"`
		SDKVersion string `json:"sdk_version"`
		Error      string `json:"error"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Type != "persona_bridge" {
		return false
	}
	switch ev.Event {
	case "ready":
		p.mu.Lock()
		p.sdkVersion = ev.SDKVersion
		p.mu.Unlock()
		p.started()
		n.logger.Printf("assistant %s: agent sdk %s ready", short(p.id), ev.SDKVersion)
	case "error":
		n.logger.Printf("assistant %s: agent sdk bridge: %s", short(p.id), ev.Error)
	case "exit":
		n.logger.Printf("assistant %s: agent sdk bridge exiting", short(p.id))
	}
	return true
}

// prependEnvPath puts dir first on PATH, for an npm shim that runs `env node`.
func prependEnvPath(env []string, dir string) []string {
	if dir == "" || dir == "." {
		return env
	}
	found := false
	out := make([]string, len(env))
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
			found = true
		} else {
			out[i] = kv
		}
	}
	if !found {
		p := os.Getenv("PATH")
		if p == "" {
			out = append(out, "PATH="+dir)
		} else {
			out = append(out, "PATH="+dir+string(os.PathListSeparator)+p)
		}
	}
	return out
}
