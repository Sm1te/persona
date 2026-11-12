package node

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// What a ~/.zshrc launcher really sets, for its page in Settings › Models: reading the function's text misses everything a
// helper (`_claude_proxy URL TOKEN MODEL WINDOW`) or a local variable does, so the node runs the launcher in a
// subshell with `claude`, `codex`, `traex`, `command` and `env` stubbed out: instead of starting anything they print
// the environment and arguments the agent would have got. probeScript is that harness; parseProbe reads its output.

const probeMarker = "__PERSONA_LAUNCHER_PROBE__"

// errProbeUnseen is a launcher the stubs never saw start an agent: it reaches claude some way they cannot stand in for
// (a function that runs a script of its own, which execs claude by its absolute path). Nothing is known about what it
// sets, which is not the same as being broken: a send through it goes ahead as it did before the probe, the model
// passed as --model (launcherModelCheck), and its page says what cannot be seen.
var errProbeUnseen = errors.New("看不出这个启动器的网关和模型：它没有直接调用 claude / codex（比如经过别的脚本、按绝对路径启动），试运行时截不到；照常能用")

// probeScript runs launcher `name` with the agents stubbed, unless the shell finds a file for it (PATH): a script runs
// in a process of its own, which the stubs do not reach, so it would start the real agent; it is reported ("file") and
// read instead (pathlaunchers.go). The stubs are defined with `function name {`: in the
// `name() {` form an alias of the same name (`alias claude="claude --…"`, common in ~/.zshrc) is expanded first and
// the whole script fails to parse. Output: "<marker> base" and the shell's environment before
// (NUL-separated KEY=VALUE on one line), "<marker> agent <name>", "<marker> arg <arg>" lines, then "<marker> env" and
// the environment the agent got. Only what the launcher changed counts: the node's own environment (a claude it runs
// in, a gateway exported in ~/.zshrc for everything) is not the launcher's.
//
// The script runs in the login shell (loginShellProgram): zsh, but bash on machines without zsh and Git Bash on
// Windows, so it is written for both: printf rather than zsh's print, the environment read line by line rather than
// with ${(f)…}, and a `;` before the `}` of every one-line function, which bash requires (#1386).
func probeScript(name string, args ...string) string {
	command := name
	for _, arg := range args {
		command += " " + shellQuote(arg)
	}
	return fmt.Sprintf(`
__jp_env() { /usr/bin/env | while IFS= read -r __jp_line; do printf '%%s\0' "$__jp_line"; done; }
__jp_report() {
  printf '%%s\n' "%[1]s agent $1"; shift
  local a; for a in "$@"; do printf '%%s\n' "%[1]s arg $a"; done
  printf '%%s' "%[1]s env "; __jp_env; printf '\n'
  exit 0
}
function claude { __jp_report claude "$@"; }
function codex { __jp_report codex "$@"; }
function traex { __jp_report traex "$@"; }
function dsh { __jp_report dsh "$@"; }
function agy { __jp_report agy "$@"; }
function omp { __jp_report omp "$@"; }
function command { case "$1" in claude|codex|traex|dsh|agy|omp) local w=$1; shift; __jp_report $w "$@" ;; *) builtin command "$@" ;; esac; }
function env {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -u) unset "$2"; shift 2 ;;
      -i|--) shift ;;
      *=*) export "$1"; shift ;;
      *) break ;;
    esac
  done
  "$@"
}
printf '%%s' "%[1]s base "; __jp_env; printf '\n'
__jp_file=
if [ -n "$ZSH_VERSION" ]; then
  case "$(whence -w %[3]s 2>/dev/null)" in *": command"|*": hashed") __jp_file=$(whence -p %[3]s 2>/dev/null) ;; esac
elif [ "$(type -t %[3]s 2>/dev/null)" = file ]; then
  __jp_file=$(type -P %[3]s 2>/dev/null)
fi
if [ -n "$__jp_file" ]; then printf '%%s\n' "%[1]s file $__jp_file"; exit 0; fi
( %[2]s ) </dev/null
`, probeMarker, command, name)
}

// Probe is what a launcher would run: the agent, its arguments, the environment it sets. File: the launcher is a file
// on PATH, which the probe did not run (the stubs cannot reach into it): it is read instead (pathlaunchers.go).
type Probe struct {
	Agent string
	Args  []string
	Env   map[string]string
	File  string
	// Shell is the shell's environment before the launcher ran (what ~/.zshrc exports for everything); nil when it
	// was not seen (a wrapper script read from its text).
	Shell map[string]string
	// Unset are the variables of Shell the agent did not get (`env -u NAME`, `unset NAME` in the launcher), sorted.
	Unset []string
}

func parseProbe(out string) (Probe, bool) {
	p := Probe{Env: map[string]string{}}
	base := map[string]string{}
	p.Shell = base
	found := false
	pairs := func(s string, into map[string]string) {
		for _, kv := range strings.Split(s, "\x00") {
			if k, v, ok := strings.Cut(kv, "="); ok && k != "" {
				into[k] = v
			}
		}
	}
	envSeen := false
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		rest, ok := strings.CutPrefix(line, probeMarker+" ")
		if !ok {
			continue
		}
		switch {
		case strings.HasPrefix(rest, "agent "):
			p.Agent, found = strings.TrimPrefix(rest, "agent "), true
		case strings.HasPrefix(rest, "arg "):
			p.Args = append(p.Args, strings.TrimPrefix(rest, "arg "))
		case strings.HasPrefix(rest, "base "):
			pairs(strings.TrimPrefix(rest, "base "), base)
		case strings.HasPrefix(rest, "env "):
			pairs(strings.TrimPrefix(rest, "env "), p.Env)
			envSeen = true
		case strings.HasPrefix(rest, "file "):
			p.File = strings.TrimPrefix(rest, "file ")
		}
	}
	if envSeen {
		for k := range base {
			if _, ok := p.Env[k]; !ok {
				p.Unset = append(p.Unset, k)
			}
		}
		sort.Strings(p.Unset)
	}
	for k, v := range p.Env {
		if old, ok := base[k]; ok && old == v {
			delete(p.Env, k) // the shell had it already
		}
	}
	return p, found
}

var probeCache = struct {
	sync.Mutex
	at  map[string]time.Time
	got map[string]proto.LauncherDetail
}{at: map[string]time.Time{}, got: map[string]proto.LauncherDetail{}}

// launcherProbeCached probes at most every 10 minutes per launcher: running one may run what it runs first (a script
// that fetches a token), so opening its page again should not repeat that.
func (n *Node) launcherProbeCached(ctx context.Context, name string) proto.LauncherDetail {
	probeCache.Lock()
	if at, ok := probeCache.at[name]; ok && time.Since(at) < 10*time.Minute {
		d := probeCache.got[name]
		probeCache.Unlock()
		return d
	}
	probeCache.Unlock()
	var d proto.LauncherDetail
	if p, err := n.launcherProbe(ctx, name); err == nil {
		d = LauncherDetail(p)
	} else if errors.Is(err, errProbeUnseen) {
		d = proto.LauncherDetail{Agent: launcherAgentType(name), Error: err.Error()}
	} else {
		d = proto.LauncherDetail{Error: err.Error()}
	}
	probeCache.Lock()
	probeCache.at[name], probeCache.got[name] = time.Now(), d
	probeCache.Unlock()
	return d
}

// launcherProbe runs the probe on this machine (a login shell, so ~/.zshrc defines the launcher).
func (n *Node) launcherProbe(ctx context.Context, name string, args ...string) (Probe, error) {
	if !shellcmd.NameRe.MatchString(name) {
		return Probe{}, fmt.Errorf("invalid launcher %q", name)
	}
	if path := n.scriptLauncher(ctx, name); path != "" {
		// A wrapper script on PATH is read, not run: the stubs below would not reach into it (pathlaunchers.go).
		p, err := scriptProbe(path, args)
		if err != nil {
			return Probe{}, err
		}
		return withSettingsEnv(p, n.home), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := loginShell(ctx, probeScript(name, args...))
	killGroupOnCancel(cmd)
	out, _ := cmd.Output()
	p, ok := parseProbe(string(out))
	if p.File != "" { // a file on PATH the list does not have as a wrapper script (yet): read, never run
		sp, err := scriptProbe(p.File, args)
		if err != nil {
			return p, err
		}
		return withSettingsEnv(sp, n.home), nil
	}
	if !ok {
		return Probe{}, errProbeUnseen
	}
	return withSettingsEnv(p, n.home), nil
}

// withSettingsEnv puts the env of the --settings a claude launcher passes on top of what the probe saw. Claude Code
// reads that file itself once it runs, so a gateway and key set there (`alias work="claude --settings
// ~/.claude/settings.work.json"`) are not in the environment the stub printed, and the launcher looked like one with
// no gateway: no quota, no gateway on its page. Settings env wins over the process env, as in Claude Code. A file
// that cannot be read leaves the probe as it was. home: where ~ and a relative path are from.
func withSettingsEnv(p Probe, home string) Probe {
	if p.Agent != "claude" {
		return p
	}
	arg := settingsArg(p.Args)
	if arg == "" {
		return p
	}
	s, err := readLauncherSettings(arg, home, home)
	if err != nil {
		return p
	}
	env, _ := s["env"].(map[string]any)
	if len(env) == 0 {
		return p
	}
	merged := make(map[string]string, len(p.Env)+len(env))
	maps.Copy(merged, p.Env)
	for k, v := range env {
		switch v := v.(type) {
		case string:
			merged[k] = v
		case float64:
			merged[k] = strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			merged[k] = strconv.FormatBool(v)
		}
	}
	p.Env = merged
	return p
}

// settingsArg is the last --settings in args, "" for none: Claude Code keeps only the last one.
func settingsArg(args []string) string {
	s := ""
	for i, a := range args {
		if a == "--settings" && i+1 < len(args) {
			s = args[i+1]
		} else if v, ok := strings.CutPrefix(a, "--settings="); ok {
			s = v
		}
	}
	return s
}

// --- what the probe means ---

// secretEnv: variables whose values are never shown.
var secretEnv = lazyre.New(`(?i)(KEY|TOKEN|SECRET|PASSWORD|AUTH|COOKIE|CREDENTIAL)`)

// shownArgs are a probe's arguments for the page: an inline --settings (JSON whose env can hold the key) as {…}, and
// anything else that is a key by its form masked (maskSecrets).
func shownArgs(args []string) []string {
	if args == nil {
		return nil
	}
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case i > 0 && args[i-1] == "--settings" && strings.HasPrefix(strings.TrimSpace(a), "{"):
			out[i] = "{…}"
		case strings.HasPrefix(a, "--settings=") && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(a, "--settings=")), "{"):
			out[i] = "--settings={…}"
		default:
			out[i] = maskSecrets(a)
		}
	}
	return out
}

// launcherEnvKeys: the variables that configure the agent (the rest of a shell's environment is noise).
var launcherEnvKeys = lazyre.New(`^(ANTHROPIC_|CLAUDE_|CLAUDE_CODE_|API_TIMEOUT_MS$|DISABLE_|MAX_THINKING_TOKENS$|MAX_MCP_OUTPUT_TOKENS$|BASH_|MCP_|OPENAI_|CODEX_|HTTPS?_PROXY$|https?_proxy$|NO_PROXY$|no_proxy$)`)

// LauncherDetail turns a probe into the page's fields: agent, gateway, how it authenticates, every model tier, the
// context window, effort, flags, and the other settings — secrets masked.
func LauncherDetail(p Probe) proto.LauncherDetail {
	d := proto.LauncherDetail{Agent: p.Agent, Args: shownArgs(p.Args)}
	get := func(k string) string { return p.Env[k] }
	d.BaseURL = firstNonEmpty(get("ANTHROPIC_BASE_URL"), get("OPENAI_BASE_URL"))
	switch {
	case get("ANTHROPIC_AUTH_TOKEN") != "":
		d.Auth = "ANTHROPIC_AUTH_TOKEN"
	case get("ANTHROPIC_API_KEY") != "":
		d.Auth = "ANTHROPIC_API_KEY"
	case get("OPENAI_API_KEY") != "":
		d.Auth = "OPENAI_API_KEY"
	case get("CLAUDE_CODE_OAUTH_TOKEN") != "":
		d.Auth = "CLAUDE_CODE_OAUTH_TOKEN"
	}
	for _, m := range []struct{ label, key string }{
		{"主模型", "ANTHROPIC_MODEL"}, {"Fable", "ANTHROPIC_DEFAULT_FABLE_MODEL"}, {"Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL"},
		{"Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL"}, {"Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL"},
		{"小模型", "ANTHROPIC_SMALL_FAST_MODEL"}, {"子 Agent", "CLAUDE_CODE_SUBAGENT_MODEL"},
	} {
		if v := get(m.key); v != "" {
			d.Models = append(d.Models, proto.LauncherModel{Role: m.label, Env: m.key, Model: v})
		}
	}
	if d.Agent != "claude" {
		if i := indexOf(p.Args, "-m", "--model"); i >= 0 && i+1 < len(p.Args) {
			d.Models = append(d.Models, proto.LauncherModel{Role: "主模型", Env: "-m", Model: p.Args[i+1]})
		}
	}
	if w := firstNonEmpty(get("CLAUDE_CODE_MAX_CONTEXT_TOKENS"), get("CLAUDE_CODE_AUTO_COMPACT_WINDOW")); w != "" {
		d.ContextWindow, _ = strconv.Atoi(w)
	}
	if a := get("CLAUDE_CODE_AUTO_COMPACT_WINDOW"); a != "" && a != get("CLAUDE_CODE_MAX_CONTEXT_TOKENS") {
		d.CompactWindow, _ = strconv.Atoi(a)
	}
	d.Effort = get("CLAUDE_CODE_EFFORT_LEVEL")
	if t := get("API_TIMEOUT_MS"); t != "" {
		d.TimeoutMS, _ = strconv.Atoi(t)
	}
	shown := map[string]bool{"ANTHROPIC_BASE_URL": true, "OPENAI_BASE_URL": true, "CLAUDE_CODE_MAX_CONTEXT_TOKENS": true,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW": true, "CLAUDE_CODE_EFFORT_LEVEL": true, "API_TIMEOUT_MS": true, d.Auth: true}
	for _, m := range d.Models {
		shown[m.Env] = true
	}
	var keys []string
	for k := range p.Env {
		if launcherEnvKeys.MatchString(k) && !shown[k] && p.Env[k] != "" { // set empty: cleared, nothing to show
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := p.Env[k]
		if secretEnv.MatchString(k) && v != "" {
			v = "•••"
		}
		v = maskSecrets(v) // a key in a header (ANTHROPIC_CUSTOM_HEADERS), a proxy's password
		d.Env = append(d.Env, proto.LauncherEnv{Key: k, Value: v})
	}
	return d
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func indexOf(list []string, want ...string) int {
	for i, s := range list {
		for _, w := range want {
			if s == w {
				return i
			}
		}
	}
	return -1
}

// killGroupOnCancel: on timeout kill everything the shell started (a token script, a hung network call) and stop
// waiting for its output soon after; killing only the shell left Output() waiting on the children's open pipe. Its own
// session, not just its own group: an interactive login shell under a node with a terminal (personad node in the
// foreground, go test in a terminal) was stopped for good reaching for that terminal (#3265).
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = osproc.SessionGroupAttr()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return osproc.StopGroup(cmd.Process.Pid, true)
	}
	cmd.WaitDelay = 2 * time.Second
}
