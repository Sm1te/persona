package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// An assistant process that exits within assistantEarlyExit of starting failed to start: its last assistantStderrTail
// stderr lines go to the log.
const (
	assistantEarlyExit  = 30 * time.Second
	assistantStderrTail = 5
)

// assistantStartWait: how long a send that started a fresh process waits for its first output before answering. A
// launcher that cannot start (its node socket gone, a shell error) exits within a second or two; the message written
// to it is lost, so the send fails with what it printed instead of leaving the message at "Delivering" for good.
var assistantStartWait = 8 * time.Second

// A launcher whose command is missing for a moment is started again a few times before the send fails (#2581):
// Claude Code's auto-updater reinstalls it with npm, which moves the old install away and writes the new one about
// 2.5 s later, and a machine with old long-running claude processes reinstalls every minute or two. A start in that
// window got "command not found: claude" and the user saw "send to claude: write |1: file already closed".
var (
	assistantStartRetries    = 3
	assistantStartRetryDelay = 2 * time.Second
)

// awaitStart waits up to d for a freshly spawned process to start; one that exits first failed to start, and the
// error says what it printed. Still starting after d counts as started (a slow SessionStart hook).
func (p *assistantProc) awaitStart(d time.Duration) error {
	if p.ready == nil {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.ready:
		return nil
	case <-t.C:
		return nil
	case <-p.exited:
	}
	select {
	case <-p.ready: // it printed and then exited: it did start
		return nil
	default:
	}
	return p.startFailure()
}

// startFailure is the error for a fresh process that exited before it started: how long it lived and its last stderr
// lines. It waits a little for the process to exit and its stderr to be read to the end: a write to it fails as soon
// as it is gone, before the output saying why has been read.
func (p *assistantProc) startFailure() error {
	select {
	case <-p.exited:
	case <-time.After(time.Second):
	}
	if p.stderrDone != nil {
		select {
		case <-p.stderrDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	p.mu.Lock()
	tail := strings.Join(p.stderrTail, " | ")
	p.mu.Unlock()
	if tail == "" {
		tail = "没有输出"
	}
	return fmt.Errorf("%s 没有启动起来（%s 后退出），消息没有送达：%s", p.launcher, time.Since(p.born).Round(100*time.Millisecond), tail)
}

// missingCommand: the process exited because its shell found no such command (exit status 127, "command not found"),
// as while Claude Code is being reinstalled.
func (p *assistantProc) missingCommand() bool {
	select {
	case <-p.exited:
	default:
		return false
	}
	if p.cmd != nil && p.cmd.ProcessState != nil && p.cmd.ProcessState.ExitCode() == 127 {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.ContainsFunc(p.stderrTail, func(l string) bool { return strings.Contains(l, "command not found") })
}

// startRefusal is err, the failed start of p, coded when what is missing is plain from what it printed
// (docs/assistant/onboarding.md 4.6): Claude Code itself (claude_missing: the shell has no claude, or `personad launch` found
// none), or the launcher (launcher_missing: a ~/.zshrc function or alias that is gone). The hub says it in plain words
// and keeps err's text as the detail (apierr.Call); nothing was delivered.
func (p *assistantProc) startRefusal(err error) error {
	p.mu.Lock()
	tail := slices.Clone(p.stderrTail)
	p.mu.Unlock()
	code := missingFrom(p.launcher, tail, p.missingCommand())
	if code == "" {
		return err
	}
	data, _ := json.Marshal(map[string]string{"delivered": "none", "launcher": p.launcher})
	return &rpc.Error{Code: code, Message: err.Error(), Data: data}
}

// zshNotFound and bashNotFound find the command a shell could not find: zsh's "zsh:1: command not found: x", bash's
// "bash: x: command not found" (also "bash: line 1: x: command not found").
var (
	zshNotFound  = lazyre.New(`command not found: (\S+)`)
	bashNotFound = lazyre.New(`([^\s:]+): command not found`)
)

// missingFrom is what the start of launcher was missing by the lines it printed: proto.CodeClaudeMissing,
// proto.CodeLauncherMissing, or "" when that is not what failed. Only a missing "claude" or a missing launcher
// itself is coded, the last such line winning: the launcher runs under `zsh -ic`, so ~/.zshrc often prints
// "command not found" for other commands (compdef, nvm…) that have nothing to do with the failure. notFound: it
// exited as a missing command does (status 127); without a line naming the command, that alone codes it, unless a
// line names some other missing command (then which one failed is not plain).
func missingFrom(launcher string, lines []string, notFound bool) string {
	for _, l := range lines {
		if strings.Contains(l, "claude not found in PATH") { // claudeExecutable, from a managed launcher's personad launch
			return proto.CodeClaudeMissing
		}
	}
	code, other := "", false
	for _, l := range lines {
		word := ""
		if m, ok := parseShellMiss(l); ok { // any shell's whole line (shellmiss.go): dash, ksh and fish too
			word = m.Word
		} else if m := zshNotFound.FindStringSubmatch(l); m != nil {
			word = m[1]
		} else if m := bashNotFound.FindStringSubmatch(l); m != nil {
			word = m[1]
		}
		switch {
		case word == "":
		case filepath.Base(word) == "claude":
			code = proto.CodeClaudeMissing
		case launcher != "" && word == launcher:
			code = proto.CodeLauncherMissing
		default:
			other = true
		}
	}
	switch {
	case code != "":
		return code
	case !notFound || other:
		return ""
	case launcher == "" || launcher == "claude":
		return proto.CodeClaudeMissing
	}
	return proto.CodeLauncherMissing
}

// spawnAssistant starts the conversation's process for its launcher's agent: claude -p (spawnClaude) or codex
// app-server (spawnCodex). A new Codex conversation (id "") gets its id from Codex.
func (n *Node) spawnAssistant(id, cwd, launcher string, resume, spare bool, options ...modelExecution) (*assistantProc, error) {
	if n.agentOf(launcher) == "codex" {
		return n.spawnCodex(id, cwd, launcher, resume, spare, options...)
	}
	if id == "" {
		id = newUUID()
	}
	return n.spawnClaude(id, cwd, launcher, resume, spare, options...)
}

func (n *Node) claudeAssistantMCPAndTools(id, cwd string, legacyMCP bool) (mcpJSON string, toolFlags []string) {
	if guest := n.guestOf(id); guest != nil { // assistant_guest.go: nothing else of the below
		return n.guestMCPAndTools(id, guest, legacyMCP)
	}
	// Per-process MCP config: the assistant-only tools (ask_user, agents_*, lark_*…) and the conversation they act for.
	servers := map[string]any{"persona": map[string]any{
		"command": n.bin, "args": []string{"mcp"}, "env": assistantMCPEnv(id, n.dir),
	}}
	// The extensions' MCP servers whose command is on this machine (assistant_mcp.go).
	extra, allow, deny := assistantMCPServers(n.assistantLark(context.Background()).MCPServers)
	for name, s := range extra {
		servers[name] = s
	}
	mcp, _ := json.Marshal(map[string]any{"mcpServers": compat.MCPRegistrationServers(servers, legacyMCP)})
	allow = append(allow, healthReportRules(cwd)...) // healthdeny.go
	toolFlags = []string{"--mcp-config", string(mcp), "--allowedTools", strings.Join(append([]string{n.assistantTools(legacyMCP)}, allow...), ","), "--tools", assistantBuiltinTools}
	// One --disallowedTools with a word per rule (it takes several, up to the next flag): a path may hold a comma.
	if len(deny) > 0 {
		deny = []string{strings.Join(deny, ",")}
	}
	deny = append(deny, n.healthDenyRules(cwd)...) // healthdeny.go
	if len(deny) > 0 {
		toolFlags = append(toolFlags, append([]string{"--disallowedTools"}, deny...)...)
	}
	return string(mcp), toolFlags
}

// assistantCLICommand is the command that runs the conversation's claude on the CLI: an interactive shell so ~/.zshrc
// defines the launcher function, "$@" passing our flags through. In the agent sandbox when it is on (agentsandbox.go).
// A managed launcher's shell is not wrapped: it runs `personad launch`, which fetches the launcher's settings outside
// the sandbox and wraps only the agent it execs (launchExec).
func (n *Node) assistantCLICommand(shell, launcher, cwd string, args, env []string) *exec.Cmd {
	argv := append([]string{shell, "-ic", n.launcherCommandIn(launcher, envValue(env, "CLAUDE_CONFIG_DIR"), shellQuote) + ` "$@"`, "persona"}, args...)
	if _, managed := n.managedLauncher(launcher); !managed {
		base := env
		argv, env, _ = wrapAgent("assistant", n.home, n.dir, cwd, settingsArgs(args),
			func() string { return claudeInner(args, base, cwd, n.home, n.claudeConfigDirs()) }, argv, env, n.logger.Printf)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = cwd, env
	return cmd
}

func (n *Node) spawnClaude(id, cwd, launcher string, resume, spare bool, options ...modelExecution) (*assistantProc, error) {
	execution := executionOption(options)
	if err := execution.check(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		return nil, err
	}
	var err error
	execution.ConfigDir, err = n.assistantProfileSelection(context.Background(), id, launcher, execution.Runtime, execution.ConfigDir, resume)
	if err != nil {
		return nil, err
	}
	configDir := n.assistantConfigDir(launcher, execution.ConfigDir)
	configPath := n.assistantConfigPath(execution.ConfigDir)
	if err := os.MkdirAll(configPath, 0o700); err != nil {
		return nil, err
	}
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages"}
	// Persona's hooks: a plugin, or in --settings for a claude that loads none (hooksplugin.go).
	settings, hooks := n.claudeStartSettings(context.Background(), launcher, "assistant", cwd, configPath)
	compaction := n.assistantCompactionPolicy(launcher, cwd, settings, execution)
	if hooks == "" && n.launcherOwnSettings(context.Background(), launcher) != "" {
		compaction.ownSettings = ""
	}
	var settingsErr error
	settings, settingsErr = n.assistantCompactionSettings(id, cwd, settings, compaction)
	if settingsErr != nil {
		return nil, settingsErr
	}
	if n.guestKindOf(id) == guestMeeting { // a meeting's room: none of the owner's Claude Code setup (assistant_guest.go)
		if n.guestOf(id) == nil {
			return nil, errRoomRecord // never with the assistant's tools
		}
		if settings, settingsErr = n.meetingRoomSettingsFile(id, cwd, configPath, settings, hooks == ""); settingsErr != nil {
			return nil, settingsErr
		}
		args = append(args, meetingRoomArgs()...)
	}
	if settings != "" {
		args = append(args, "--settings", settings)
	}
	if hooks != "" {
		args = append(args, "--plugin-dir", hooks)
	}
	// Snapshot the connection capability once for both runtime paths and their permission lists. Historical tool_use
	// blocks under the old namespace remain valid on a resume with only Persona offered (verified on Claude Code 2.1.322);
	// an old hub that still addresses the legacy namespace keeps the alias.
	legacyMCP := !n.hubSupportsPersonaContracts()
	_, toolFlags := n.claudeAssistantMCPAndTools(id, cwd, legacyMCP)
	args = append(args, toolFlags...)
	model := execution.Model
	if model == "" && resume && execution.Fingerprint != "" && n.guestOf(id) == nil {
		// The model configuration names none: the launcher's default, not the model the session ran on last
		// (assistant_resume_model.go). A guest keeps what it started on.
		model = n.resumeModel(launcher, cwd, settings, configPath)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	// bypassPermissions only takes as a flag at start; the other modes are set with a control request (syncAssistantModes).
	var startMode string
	if !spare {
		if mode, _ := n.modesOf(id); mode == proto.ModeBypass {
			startMode = mode
			args = append(args, "--permission-mode", mode)
		}
	}
	if resume {
		args = append(args, "--resume", id)
	} else {
		args = append(args, "--session-id", id)
	}
	// Persona's own tools are few and used all the time: load them up front instead of behind ToolSearch.
	env := identity.Environment(os.Environ(), append([]string{"PERSONA_ASSISTANT=1", "ENABLE_TOOL_SEARCH=false"}, compaction.env()...)...)
	env = n.claudeProcessEnv(env, configDir)
	guest := n.guestOf(id) != nil
	if guest && execution.Runtime == proto.AssistantRuntimeAgentSDK && n.guestKindOf(id) == guestMeeting {
		return nil, errGuestAgent // a meeting room keeps its CLI-only settings (assistant_guest.go)
	}
	gateEnv, releaseGates := n.assistantLarkEnv(cwd, id) // its addresses at the gate proxy go with the process
	if guest {
		releaseGates()
		gateEnv, releaseGates = nil, func() {} // no commands to gate: it has no Bash
	}
	cleanupMCP := func() {}
	started := false
	defer func() {
		if !started {
			cleanupMCP()
			releaseGates()
		}
	}()
	env = identity.Environment(env, gateEnv...)
	var bridge sdkBridge
	var fallback string
	if execution.Runtime == proto.AssistantRuntimeAgentSDK {
		if bridge, fallback = n.sdkPrepare(context.Background(), launcher); fallback != "" {
			n.logger.Printf("assistant %s: running on the CLI, not the Agent SDK: %s", short(id), fallback)
		}
	}
	var cmd *exec.Cmd
	var first []byte // the bridge's persona_start
	if bridge.script != "" {
		if guest {
			// A CLI --tools selection drops MCP tools too. Keep the same Persona
			// stdio server, but register its declared tools through the SDK.
			mcpConfig, flags := n.guestMCPAndTools(id, n.guestOf(id), legacyMCP)
			var mcp struct {
				MCPServers map[string]any `json:"mcpServers"`
			}
			if err := json.Unmarshal([]byte(mcpConfig), &mcp); err != nil {
				return nil, err
			}
			persona, ok := mcp.MCPServers["persona"]
			if !ok {
				return nil, errors.New("guest MCP config has no persona server")
			}
			for name := range mcp.MCPServers {
				if name != "persona" && name != compat.MCPServer {
					return nil, errors.New("guest MCP config contains an unexpected server")
				}
			}
			start := sdkStart{id: id, cwd: cwd, model: model, configDir: execution.ConfigDir, permissionMode: startMode,
				resume: resume, mcp: map[string]any{"mcpServers": map[string]any{}}, guestMCP: map[string]any{"persona": persona},
				settings: settings, compaction: &compaction}
			for i := 0; i < len(flags); i++ {
				switch flags[i] {
				case "--allowedTools":
					i++
					start.allowedTools = flags[i]
				case "--disallowedTools":
					i++
					start.deny = strings.Split(flags[i], ",")
				}
			}
			start.strictMCP = true
			var err error
			if cmd, first, err = n.sdkCommand(bridge, launcher, start, env); err != nil {
				return nil, err
			}
		} else {
			// The Agent SDK runtime gets only Persona's own MCP server: it has no --disallowedTools, so the Lark extension's
			// control tools (whose approval_decide must stay off) are not mounted here.
			mcpConfig := map[string]any{"mcpServers": map[string]any{"persona": map[string]any{
				"command": n.bin, "args": []string{"mcp"}, "env": map[string]string{"PERSONA_ASSISTANT": "1", "PERSONA_SESSION_ID": id},
			}}}
			compat.MCPRegistrationServers(mcpConfig["mcpServers"].(map[string]any), legacyMCP)
			start := sdkStart{id: id, cwd: cwd, model: model, configDir: execution.ConfigDir, permissionMode: startMode, resume: resume, mcp: mcpConfig,
				allowedTools: strings.Join(append([]string{n.assistantTools(legacyMCP)}, healthReportRules(cwd)...), ","), tools: assistantBuiltinTools, deny: n.healthDenyRules(cwd), settings: settings, plugin: hooks, compaction: &compaction}
			var err error
			if cmd, first, err = n.sdkCommand(bridge, launcher, start, env); err != nil {
				return nil, err
			}
		}
	} else {
		shell, err := interactiveShell()
		if err != nil {
			return nil, err
		}
		args, cleanupMCP, err = assistantCLIMCPFlags(args)
		if err != nil {
			return nil, fmt.Errorf("prepare assistant MCP config: %w", err)
		}
		cmd = n.assistantCLICommand(shell, launcher, cwd, args, env)
	}
	cmd.SysProcAttr = osproc.GroupAttr() // so stop() can kill the whole group
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, closeStderrWriter, err := assistantStderrPipe(cmd)
	if err != nil {
		return nil, err
	}
	instructions := instructionsDigest(cwd)
	startErr := cmd.Start()
	closeStderrWriter()
	if startErr != nil {
		_ = stderr.Close()
		return nil, fmt.Errorf("start %s: %w", launcher, startErr)
	}
	started = true
	p := &assistantProc{id: id, agent: "claude", launcher: launcher, cwd: cwd, cmd: cmd, stdin: stdin, exited: make(chan struct{}), ready: make(chan struct{}),
		stderrDone: make(chan struct{}), born: time.Now(), lastActive: time.Now(), mode: startMode, canBypass: startMode == proto.ModeBypass, model: execution, fingerprint: n.executionFingerprint(launcher, execution),
		instructions: instructions, fallback: fallback, personaMCPOnly: !legacyMCP, compactIdleAt: compaction.idle(), declaredWindow: compaction.declared()}
	if first != nil {
		p.runtime = proto.AssistantRuntimeAgentSDK
		if err := p.write(first); err != nil {
			_ = osproc.StopGroup(cmd.Process.Pid, true)
			_ = cmd.Wait()
			_ = stderr.Close()
			releaseGates() // started, but the exit watcher below never runs
			return nil, fmt.Errorf("start the agent sdk bridge: %w", err)
		}
	}
	if !spare {
		n.mu.Lock()
		n.procs[id] = p
		n.mu.Unlock()
	}
	go watchAssistantStderr(n.logger, p, stderr)
	liveAssistants.Store(p, struct{}{})
	go n.readAssistant(p, stdout)
	go func() {
		_ = cmd.Wait()
		cleanupMCP()
		releaseGates()
		liveAssistants.Delete(p)
		close(p.exited)
		if n.keeper != nil {
			n.keeper.forget(p) // exited between two connections
		}
		n := p.ownerOr(n)
		n.mu.Lock()
		wasSpare := n.spare == p
		if wasSpare {
			n.spare = nil
		}
		var recovery *assistantTurnRecovery
		if n.procs[id] == p {
			delete(n.procs, id)
			recovery = n.recoveries[id] // a current process that exits without a result must not leave a working placeholder
		}
		n.mu.Unlock()
		if wasSpare {
			return
		}
		if recovery != nil {
			n.finishAssistantRecoveryOnExit(p, recovery)
			return
		}
		n.emitDelta(id, "")
		n.notifySessions()
	}()
	return p, nil
}

func assistantStderrPipe(cmd *exec.Cmd) (io.ReadCloser, func(), error) {
	if cmd.Stderr != nil {
		return nil, nil, errors.New("exec: Stderr already set")
	}
	if cmd.Process != nil {
		return nil, nil, errors.New("exec: StderrPipe after process started")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = w
	return r, func() { _ = w.Close() }, nil
}

// watchAssistantStderr logs an assistant process's error lines, and, when its stderr ends (it exited) within
// assistantEarlyExit of born, its last lines whatever they say: it did not start. WSL's "open terminal failed: not a
// terminal" has no "error" in it, and the assistant stayed silent with nothing in any log (#1994).
// The tail is kept on the process too (without blank lines and terminal colors): a send that started it reports it when
// it exits before starting (awaitStart).
func watchAssistantStderr(logger *log.Logger, p *assistantProc, stderr io.ReadCloser) {
	id, launcher, born := p.id, p.launcher, p.born
	if p.stderrDone != nil {
		defer close(p.stderrDone)
	}
	defer stderr.Close()
	var tail []string
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		l := sc.Text()
		if strings.Contains(l, "rror") || strings.Contains(l, "agent-sdk bridge: guest tools:") {
			logger.Printf("assistant %s: %s", id[:8], l)
		}
		if clean := strings.TrimSpace(ansiRe.ReplaceAllString(l, "")); clean != "" {
			if tail = append(tail, clean); len(tail) > assistantStderrTail {
				tail = tail[1:]
			}
			p.mu.Lock()
			p.stderrTail = append([]string(nil), tail...)
			p.mu.Unlock()
		}
	}
	if err := sc.Err(); err != nil {
		logger.Printf("assistant %s: stderr read: %v", id[:8], err)
		_, _ = io.Copy(io.Discard, stderr)
	}
	if lived := time.Since(born); lived < assistantEarlyExit && len(tail) > 0 {
		logger.Printf("assistant %s: %s exited %s after it started: %s", id[:8], launcher, lived.Round(time.Millisecond),
			strings.Join(tail, " | "))
	}
}

// ansiRe: terminal color and style sequences (an interactive zsh's prompt plugins print them on stderr).
var ansiRe = lazyre.New(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
