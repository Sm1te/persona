package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/osproc"
)

// codexArgs are app-server's own flags: the persona MCP server (its session is the conversation's key until the
// thread exists) with a timeout long enough for ask_user, nothing else; sandbox and approvals go with each thread
// and turn.
func codexArgs(bin, key string, windows ...int) []string {
	return codexArgsForHome(bin, key, "", true, windows...)
}

func (n *Node) codexAssistantArgs(key, home string, windows ...int) ([]string, bool) {
	personaMCPOnly := n.hubSupportsPersonaContracts()
	return codexArgsForHome(n.bin, key, home, !personaMCPOnly, windows...), personaMCPOnly
}

// codexArgsForHome: windows[0] is a compact limit to pass (a managed launcher's declared one, #3066); none or 0 passes
// none, and Codex compacts by its config or its model's default.
func codexArgsForHome(bin, key, home string, legacyMCP bool, windows ...int) []string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) } // a JSON string is a TOML basic string
	mcpEnv := "{PERSONA_ASSISTANT=" + q("1") + ",PERSONA_SESSION_ID=" + q(key)
	if home != "" {
		mcpEnv += ",PERSONA_HOME=" + q(home)
	}
	mcpEnv += "}"
	args := []string{
		"-c", "mcp_servers.persona.command=" + q(bin),
		"-c", `mcp_servers.persona.args=["mcp"]`,
		"-c", "mcp_servers.persona.env=" + mcpEnv,
		"-c", fmt.Sprintf("mcp_servers.persona.tool_timeout_sec=%d", codexToolTimeout),
		"-c", "mcp_servers.persona.startup_timeout_sec=30",
		// Commands get the process's environment like Claude's Bash does: the default excludes (*KEY*, *TOKEN*…)
		// would strip the credentials of a gate that still puts them there (auth env, gateproxy.go), and its command
		// would fail.
		"-c", "shell_environment_policy.inherit=" + q("all"),
		"-c", "shell_environment_policy.ignore_default_excludes=true",
		// The assistant's memory is the one the app's "Memory" page shows, shared with Claude (codexMemoryPrompt); Codex's
		// own (~/.codex/memories) is one store for every Codex on the machine, so it stays off here.
		"-c", "features.memories=false",
	}
	if len(windows) > 0 && windows[0] > 0 {
		args = append(args, "-c", fmt.Sprintf("model_auto_compact_token_limit=%d", windows[0]))
	}
	if legacyMCP {
		return compat.CodexMCPAliases(args)
	}
	return args
}

// codexShell runs the launcher with `codex` turned into `codex <its flags> app-server <ours>`: the launcher's
// function sets what it sets (env, -c model_provider, -m …) exactly as when it starts the TUI.
// Use alias-safe definitions and calls: zsh expands aliases while parsing the script.
func codexShell(launcher string) string {
	return `__persona_args=("$@")
function __persona_codex { builtin command codex "$@" app-server "${__persona_args[@]}"; }
if [[ -n "${ZSH_VERSION-}" ]] && (( $+functions[codex] )); then
  # Keep a user-defined codex() launcher intact while routing its final command through the app-server wrapper.
  # Copy it through the functions table: functions -c needs zsh 5.8, and a subscript never expands aliases.
  functions[__persona_user_codex]=$functions[codex]
  function codex { __persona_user_codex "$@"; }
else
  function codex { __persona_codex "$@"; }
fi
function command { if [[ "$1" == codex ]]; then shift; __persona_codex "$@"; else builtin command "$@"; fi; }
` + launcher
}

func (n *Node) spawnCodex(id, cwd, launcher string, resume, spare bool, options ...modelExecution) (*assistantProc, error) {
	return n.spawnCodexWithMode(id, cwd, launcher, resume, spare, "", options...)
}

func (n *Node) spawnCodexWithMode(id, cwd, launcher string, resume, spare bool, mode string, options ...modelExecution) (*assistantProc, error) {
	execution := executionOption(options)
	if err := execution.check(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		return nil, err
	}
	root, memDir := n.codexDirs(cwd)
	key := "proc-" + randHex(8)
	shell, err := interactiveShell()
	if err != nil {
		return nil, err
	}
	compaction := n.assistantCompactionPolicy(launcher, cwd, "", execution)
	args, personaMCPOnly := n.codexAssistantArgs(key, n.dir, compaction.codexLimit())
	cmd := exec.Command(shell, append([]string{"-ic", codexShell(n.launcherCommand(launcher)), "persona"}, args...)...)
	cmd.Dir = root
	cmd.Env = identity.Environment(os.Environ(), "PERSONA_ASSISTANT=1")
	gateEnv, releaseGates, nameGates := n.assistantLarkEnvNamed(cwd, id) // its addresses at the gate proxy go with the process
	started := false
	defer func() {
		if !started {
			releaseGates()
		}
	}()
	cmd.Env = identity.Environment(cmd.Env, gateEnv...)
	cmd.SysProcAttr = osproc.SessionGroupAttr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	instructions := instructionsDigest(cwd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", launcher, err)
	}
	started = true
	p := &assistantProc{id: id, agent: "codex", launcher: launcher, cwd: cwd, cmd: cmd, stdin: stdin, exited: make(chan struct{}),
		born: time.Now(), lastActive: time.Now(), model: execution, fingerprint: n.executionFingerprint(launcher, execution),
		instructions: instructions, personaMCPOnly: personaMCPOnly}
	c := &codexConn{n: n, p: p, key: key, memDir: memDir, pending: map[int64]chan codexReply{}, asks: map[string]context.CancelFunc{}, files: map[string][]string{},
		nameGates: nameGates}
	p.cx = c
	n.mu.Lock()
	n.akeys[key] = p
	n.mu.Unlock()
	go logLines(stderr, func(l string) {
		if strings.Contains(l, "ERROR") && !strings.Contains(l, "unrecognized configuration") {
			n.logger.Printf("assistant codex %s: %s", key, l)
		}
	})
	liveAssistants.Store(p, struct{}{})
	go c.read(stdout)
	go func() {
		_ = cmd.Wait()
		releaseGates()
		c.mu.Lock()
		c.closed = true
		for _, cancel := range c.asks {
			cancel()
		}
		c.mu.Unlock()
		liveAssistants.Delete(p)
		close(p.exited)
		if n.keeper != nil {
			n.keeper.forget(p)
		}
		n := p.ownerOr(n)
		n.mu.Lock()
		delete(n.akeys, key)
		wasSpare := n.spare == p
		if wasSpare {
			n.spare = nil
		}
		var recovery *assistantTurnRecovery
		if p.id != "" && n.procs[p.id] == p {
			delete(n.procs, p.id)
			recovery = n.recoveries[p.id] // do not confuse a replaced process's late exit with the current one
		}
		n.mu.Unlock()
		if wasSpare || p.id == "" {
			return
		}
		if recovery != nil {
			n.finishAssistantRecoveryOnExit(p, recovery)
			return
		}
		n.emitDelta(p.id, "")
		n.notifySessions()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), codexStartTimeout)
	defer cancel()
	if err := c.start(ctx, id, root, resume, mode); err != nil {
		p.stop()
		return nil, fmt.Errorf("%s（codex app-server）没有启动起来: %w", launcher, err)
	}
	if !spare {
		n.mu.Lock()
		n.procs[p.id] = p
		n.mu.Unlock()
	}
	return p, nil
}

// codexDirs is the workspace and the memory directory (made if missing) as Codex gets them, symlinks resolved: both
// are writable roots, and its macOS sandbox refuses one with a symlink in it (~/.persona → .persona, a ~/.claude kept in
// dotfiles), turning every command down before it runs. Only Codex sees the real paths; the session keeps its cwd.
func (n *Node) codexDirs(cwd string) (root, memDir string) {
	memDir = n.memoryDir(cwd)
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		n.logger.Printf("assistant codex: memory dir: %v", err)
	}
	return n.realDir(cwd), n.realDir(memDir)
}

func (n *Node) realDir(dir string) string {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		n.logger.Printf("assistant codex: resolve %s: %v", dir, err)
		return dir
	}
	return real
}
