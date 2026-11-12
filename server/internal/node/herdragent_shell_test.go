package node

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The command a Claude start types into a herdr pane, and herdr's resume of it (eval of resume_argv), run by real
// shells (#3454): HERDR_AGENT=claude reaches the claude the launcher runs — a binary on the PATH, a function, an alias
// that sets a variable of its own, claude_start_dir's CLAUDE_CONFIG_DIR in front — with the node's arguments, and is
// neither left set in the shell nor passed to what runs there next (herdr would take that for Claude too). A shell that
// is not installed is skipped (CI's runner has bash only). The fakes write what they got to a file: an interactive
// shell's own output (Ubuntu's /etc/bash.bashrc greets a sudo user on stdout) is no part of it.
func TestHerdrAgentReachesClaudeInRealShells(t *testing.T) {
	posixDefs := "ccf() { claude --model from-function \"$@\"; }\nalias ccv='ALIAS_MARK=from-alias claude --model from-alias'\n"
	shells := []struct {
		name, rc, defs string
		flags          []string // before the command line
		shellVar       string   // HERDR_AGENT as the shell has it, "unset" when it has none
	}{
		{name: "bash", rc: ".bashrc", defs: posixDefs, flags: []string{"-ic"}, shellVar: `"${HERDR_AGENT-unset}"`},
		{name: "zsh", rc: ".zshrc", defs: posixDefs, flags: []string{"-ic"}, shellVar: `"${HERDR_AGENT-unset}"`},
		{name: "fish", rc: filepath.Join(".config", "fish", "config.fish"),
			defs:     "function ccf; claude --model from-function $argv; end\nalias ccv 'ALIAS_MARK=from-alias claude --model from-alias'\n",
			flags:    []string{"-c"},
			shellVar: "(set -q HERDR_AGENT; and echo $HERDR_AGENT; or echo unset)"},
	}
	launchers := []struct {
		name, launcher, startDir string
		pre                      []string // the launcher's own words before the node's arguments
		mark                     string   // the variable the alias sets
		configDir                string   // CLAUDE_CONFIG_DIR under the shell's home
	}{
		{name: "binary", launcher: "claude"},
		{name: "function", launcher: "ccf", pre: []string{"--model", "from-function"}},
		{name: "alias with a variable", launcher: "ccv", pre: []string{"--model", "from-alias"}, mark: "from-alias"},
		{name: "claude_start_dir", launcher: "claude", startDir: "~/.claude-work", configDir: ".claude-work"},
	}
	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			bin, err := exec.LookPath(sh.name)
			if err != nil {
				t.Skipf("%s is not installed", sh.name)
			}
			withPaneShell(t, bin) // herdr's resume is typed into this shell too (fish has no unset: agentenv.go)
			home := t.TempDir()
			path := filepath.Join(home, "bin")
			ran := filepath.Join(home, "ran")
			fakeProgram(t, filepath.Join(path, "claude"),
				`printf '%s\0' "${HERDR_AGENT-unset}" "${ALIAS_MARK-}" "${CLAUDE_CONFIG_DIR-}" "$@" >>"$HOME/ran"`)
			fakeProgram(t, filepath.Join(path, "probe"), `printf 'after\0%s\0%s\0' "${HERDR_AGENT-unset}" "$1" >>"$HOME/ran"`)
			writeFile(t, filepath.Join(home, sh.rc), sh.defs)
			for _, l := range launchers {
				for _, resume := range []bool{false, true} {
					name := l.name
					if resume {
						name += " resumed"
					}
					t.Run(name, func(t *testing.T) {
						if err := os.Remove(ran); err != nil && !os.IsNotExist(err) {
							t.Fatal(err)
						}
						n, _ := testNode(t)
						n.dir = filepath.Join(home, "persona dir") // quoted on the command line
						if l.startDir != "" {
							n.claudeStartDir.Store(&l.startDir)
						}
						spec := claudeLaunch{Launcher: l.launcher}
						line := withHerdrAgent(typedHerdrAgent(&herdrHost{}, "claude", bin), n.claudeCommandLine(spec, "--session-id "+startID, shellQuote))
						args := []string{"--session-id", startID}
						if resume {
							argv, ok := n.claudeResumeArgv(spec, startID)
							if !ok {
								t.Fatal("no resume command")
							}
							line, args = argv[0]+" "+shellQuote(argv[1]), []string{"--resume", startID}
						}
						line += "; probe " + sh.shellVar

						// Only a guard against a hang (a fresh fake on macOS waits for the system's checks, #2307).
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cmd := exec.CommandContext(ctx, bin, append(sh.flags, line)...)
						cmd.Env = []string{"HOME=" + home, "ZDOTDIR=" + home, "PATH=" + path + ":/usr/bin:/bin"}
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("%s: %v: %s", line, err, out)
						}
						out, err := os.ReadFile(ran)
						if err != nil {
							t.Fatalf("%s: nothing ran: %v", line, err)
						}
						configDir := ""
						if l.configDir != "" {
							configDir = filepath.Join(home, l.configDir)
						}
						want := append([]string{"claude", l.mark, configDir}, l.pre...)
						want = append(append(want, args...), "--mcp-config", filepath.Join(n.dir, "mcp.json"), "--allowedTools", ownToolsRule)
						want = append(want, "after", "unset", "unset")
						if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !reflect.DeepEqual(got, want) {
							t.Fatalf("%s\nran %q\nwant %q", line, got, want)
						}
					})
				}
			}
		})
	}
}

// fakeProgram writes an executable sh script running body.
func fakeProgram(t *testing.T, path, body string) {
	t.Helper()
	writeFile(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
