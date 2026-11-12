package node

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

var launcherCache struct {
	sync.Mutex
	at       time.Time
	list     []proto.Launcher
	fetch    sync.Mutex // one login shell at a time
	fetching bool       // a background refresh (cachedLaunchers) is running
	// codex: how each Codex launcher runs (codex_start.go), its words as the shell would run them.
	codex map[string][]string
	// settings: the --settings a claude alias brings itself, as its words give it (launcher_settings.go).
	settings map[string]string
	// scripts: where each wrapper script on PATH is (pathlaunchers.go).
	scripts map[string]string
}

// launcherScript lists the login shell's launchers in one go (sourcing ~/.zshrc takes a while): what the
// `_claude_launchers` helper prints when the rc file has one, a GS byte, then every alias as its name and its words
// as zsh itself splits and unquotes them (name US word US … RS), so quoting never has to be parsed here (bash prints
// `alias -p` after bashAliasMark instead, launchers_shell.go); then, each after a GS byte, where codex, traex and omp
// are on the PATH, the names of the shell's functions and aliases (one a line: they come before a file on PATH of the
// same name), and its PATH (for the wrapper scripts on it, pathlaunchers.go). All of it between launcherBegin and
// launcherEnd, so what the rc files print around it is never read as a section (launcherOutput).
const launcherScript = `printf '\001persona-launchers-begin\001'
__jl_path=$PATH
_claude_launchers 2>/dev/null || true
printf '\035'
if [ -n "$ZSH_VERSION" ]; then
  eval 'for __k __v in "${(@kv)aliases}"; do
    print -rn -- "$__k"$'\''\x1f'\''
    for __w in "${(@Q)${(z)__v}}"; do print -rn -- "$__w"$'\''\x1f'\''; done
    print -rn -- $'\''\x1e'\''
  done'
elif [ -n "$BASH_VERSION" ]; then
  printf '\002'
  alias -p
fi
printf '\035'
PATH="$PATH:$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin"
if [ -n "$ZSH_VERSION" ]; then whence -p codex 2>/dev/null; else command -v codex 2>/dev/null; fi
printf '\035'
if [ -n "$ZSH_VERSION" ]; then whence -p traex 2>/dev/null; else command -v traex 2>/dev/null; fi
printf '\035'
if [ -n "$ZSH_VERSION" ]; then whence -p omp 2>/dev/null; else command -v omp 2>/dev/null; fi
printf '\035'
if [ -n "$ZSH_VERSION" ]; then eval 'print -rl -- ${(k)functions} ${(k)aliases}'; elif [ -n "$BASH_VERSION" ]; then compgen -A function; compgen -a; fi
printf '\035'
printf '%s' "$__jl_path"
printf '\001persona-launchers-end\001'`

// launcherBegin and launcherEnd frame what launcherScript prints (launcherOutput).
const (
	launcherBegin = "\x01persona-launchers-begin\x01"
	launcherEnd   = "\x01persona-launchers-end\x01"
)

// launcherOutput is what launcherScript itself printed: rc files print what they like to stdout while the shell
// starts (Ubuntu's /etc/bash.bashrc a sudo hint, #6310) and an exit trap after the script, none of it the script's.
// Nothing without the begin marker; to the end of the output without the end marker (a shell cut off by the timeout).
func launcherOutput(out string) string {
	i := strings.LastIndex(out, launcherBegin)
	if i < 0 {
		return ""
	}
	out, _, _ = strings.Cut(out[i+len(launcherBegin):], launcherEnd)
	return out
}

// launchers lists the launchers of this machine's login shell: the `_claude_launchers` helper's (claude wrapper
// functions), then every alias that runs claude (`alias dmcc="ANTHROPIC_BASE_URL=… claude …"`), by name. A name
// the helper lists is not repeated; none at all: plain claude.
func (n *Node) launchers(ctx context.Context) ([]proto.Launcher, error) {
	launcherCache.fetch.Lock()
	defer launcherCache.fetch.Unlock()
	launcherCache.Lock()
	if time.Since(launcherCache.at) < time.Minute && launcherCache.list != nil {
		list := launcherCache.list
		launcherCache.Unlock()
		return list, nil
	}
	launcherCache.Unlock()
	// Not cut short when the caller gives up (the hub waits a few seconds per machine): a slow rc file still finishes
	// and the next ask within the minute gets the list from the cache.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	out, err := loginShell(ctx, launcherScript).Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("list launchers: %w", err)
	}
	list, codex, settings, scripts := mergeLaunchers(string(out))
	launcherCache.Lock()
	launcherCache.at, launcherCache.list, launcherCache.codex, launcherCache.settings = time.Now(), list, codex, settings
	launcherCache.scripts = scripts
	launcherCache.Unlock()
	return list, nil
}

// mergeLaunchers is launcherScript's output as the launcher list: the helper's, then the aliases it does not name,
// then the wrapper scripts on PATH no function or alias shadows (nothing at all: plain claude), then Codex's — plain
// codex when it is on the PATH, and the aliases that run it (how each runs: codex) — then plain traex when it is on the
// PATH (started without Claude's flags, start_agent.go). settings: the claude aliases' and scripts' own --settings, by
// name (launcher_settings.go); an alias is what the shell runs for its name even when the helper lists the name too.
// scripts: where each wrapper script is.
func mergeLaunchers(out string) (list []proto.Launcher, codex map[string][]string, settings, scripts map[string]string) {
	helper, rest, _ := strings.Cut(launcherOutput(out), "\x1d")
	aliases, rest, _ := strings.Cut(rest, "\x1d")
	if a, ok := strings.CutPrefix(aliases, bashAliasMark); ok {
		aliases = bashAliases(a)
	}
	codexPath, rest, _ := strings.Cut(rest, "\x1d")
	traexPath, rest, _ := strings.Cut(rest, "\x1d")
	ompPath, rest, _ := strings.Cut(rest, "\x1d")
	shellNames, pathVar, _ := strings.Cut(rest, "\x1d")
	list = parseLaunchers(helper)
	seen := map[string]bool{}
	for _, l := range list {
		seen[l.Name] = true
	}
	claudeAliases, settings := parseAliasLaunchers(aliases)
	for _, l := range claudeAliases {
		if !seen[l.Name] {
			list = append(list, l)
		}
	}
	taken := maps.Clone(seen)
	for _, name := range strings.Fields(shellNames) {
		taken[name] = true
	}
	for _, rec := range strings.Split(aliases, "\x1e") {
		if name, _, ok := strings.Cut(rec, "\x1f"); ok {
			taken[name] = true
		}
	}
	pathList, pathSettings, scripts := pathLaunchers(pathVar, taken)
	list = append(list, pathList...)
	maps.Copy(settings, pathSettings)
	if len(list) == 0 {
		list = []proto.Launcher{{Name: "claude", Model: "默认"}}
	}
	codex = map[string][]string{}
	ls, words := parseCodexAliases(aliases)
	if strings.TrimSpace(codexPath) != "" && words["codex"] == nil {
		list = append(list, proto.Launcher{Name: "codex", Model: "默认", Agent: "codex", Source: "zshrc", PromptOptional: true})
		codex["codex"] = []string{"codex"}
	}
	for _, l := range ls {
		if !seen[l.Name] {
			list = append(list, l)
			codex[l.Name] = words[l.Name]
		}
	}
	if strings.TrimSpace(traexPath) != "" && !seen[traexAgent] && codex[traexAgent] == nil {
		list = append(list, proto.Launcher{Name: traexAgent, Model: "默认", Agent: traexAgent, Source: "zshrc"})
	}
	if strings.TrimSpace(ompPath) != "" && !seen["omp"] && codex["omp"] == nil {
		list = append(list, proto.Launcher{Name: "omp", Model: "默认", Agent: "omp", Source: "zshrc"})
	}
	return list, codex, settings, scripts
}

// cachedLaunchers never waits for the login shell: it returns the last list (possibly stale, nil before the first
// one) and starts a refresh in the background when the list is missing or old.
func (n *Node) cachedLaunchers() []proto.Launcher {
	launcherCache.Lock()
	defer launcherCache.Unlock()
	if (launcherCache.list == nil || time.Since(launcherCache.at) >= time.Minute) && !launcherCache.fetching {
		launcherCache.fetching = true
		go func() {
			_, _ = n.launchers(context.Background())
			launcherCache.Lock()
			launcherCache.fetching = false
			launcherCache.Unlock()
		}()
	}
	return launcherCache.list
}

// inferLauncher names the launcher of a claude Persona did not start (typed in a terminal), from the
// ANTHROPIC_BASE_URL / ANTHROPIC_MODEL the scan saw in its environment; "" when unknown.
func (n *Node) inferLauncher(id string) string {
	env, ok := n.sessionEnv(id)
	if !ok {
		return ""
	}
	return matchLauncher(n.cachedLaunchers(), env.BaseURL, env.Model)
}

// matchLauncher picks the launcher that exports this base URL and model. Several launchers can share one endpoint
// (and a launcher whose model is chosen at run time shows a placeholder such as "(claude-choose)"), so: the one
// launcher with both equal; else the one launcher with that base URL; else nothing, never a guess between several.
func matchLauncher(list []proto.Launcher, baseURL, model string) string {
	if baseURL == "" {
		return "" // the default endpoint: nothing to tell launchers apart by
	}
	var byURL, byBoth []string
	for _, l := range list {
		if l.BaseURL != baseURL {
			continue
		}
		byURL = append(byURL, l.Name)
		if l.Model == model {
			byBoth = append(byBoth, l.Name)
		}
	}
	if len(byBoth) == 1 {
		return byBoth[0]
	}
	if len(byURL) == 1 {
		return byURL[0]
	}
	return ""
}

var nameRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_-]*$`)

var assignRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*=`)

// parseAliasLaunchers reads launcherScript's alias records and keeps the aliases that start claude: after the
// variables set in front of it (`A=1 B=2 claude …`), through env / command / exec / noglob, by path or not, with
// `export A=1;` before it in the same alias. Their gateway and model come from ANTHROPIC_BASE_URL and ANTHROPIC_MODEL
// (or --model). Codex's are parseCodexAliases'. Sorted by name: the shell keeps no order. settings: the ones that
// bring their own --settings, by name (launcher_settings.go).
func parseAliasLaunchers(out string) (list []proto.Launcher, settings map[string]string) {
	settings = map[string]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		words := strings.Split(rec, "\x1f")
		if len(words) < 2 {
			continue
		}
		name := words[0]
		if !nameRe.MatchString(name) || name == "claude" {
			continue
		}
		if l, s, ok := aliasLauncher(words[1 : len(words)-1]); ok {
			l.Name = name
			list = append(list, l)
			if s != "" {
				settings[name] = s
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, settings
}

// parseCodexAliases keeps the aliases that run Codex: `export` statements and one codex command (through env /
// command / exec / noglob, with variables set in front). How each runs is its words for codex-start: the variables,
// then the command and its arguments. Its model is -m / --model or -c model=…; an alias that does more (`cd x && codex`)
// is left out.
func parseCodexAliases(out string) ([]proto.Launcher, map[string][]string) {
	var list []proto.Launcher
	words := map[string][]string{}
	for _, rec := range strings.Split(out, "\x1e") {
		w := strings.Split(rec, "\x1f")
		if len(w) < 2 || !nameRe.MatchString(w[0]) {
			continue
		}
		argv, model, ok := codexAliasArgv(w[1 : len(w)-1])
		if !ok {
			continue
		}
		if model == "" {
			model = "默认"
		}
		list = append(list, proto.Launcher{Name: w[0], Model: model, Agent: "codex", Source: "zshrc", PromptOptional: true})
		words[w[0]] = argv
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, words
}

// codexAliasArgv reads an alias's words as one codex command; ok false when it is anything else.
func codexAliasArgv(words []string) (argv []string, model string, ok bool) {
	var exported []string
	found := false
	for len(words) > 0 {
		end := len(words)
		for i, w := range words {
			if w == ";" || w == "&&" || w == "||" || w == "|" || w == "&" {
				end = i
				break
			}
		}
		cmd := words[:end]
		if end < len(words) {
			words = words[end+1:]
		} else {
			words = nil
		}
		if len(cmd) == 0 {
			continue
		}
		if cmd[0] == "export" {
			for _, w := range cmd[1:] {
				if assignRe.MatchString(w) {
					exported = append(exported, w)
				}
			}
			continue
		}
		if found {
			return nil, "", false // more than one command
		}
		env := map[string]string{}
		i := commandIndex(cmd, env)
		if i < 0 || filepath.Base(cmd[i]) != "codex" {
			return nil, "", false
		}
		found = true
		argv = append([]string(nil), exported...)
		for _, w := range cmd[:i] {
			if assignRe.MatchString(w) {
				argv = append(argv, w)
			}
		}
		argv = append(argv, cmd[i:]...)
		for j := i + 1; j < len(cmd); j++ {
			switch {
			case (cmd[j] == "-m" || cmd[j] == "--model") && j+1 < len(cmd):
				model = cmd[j+1]
			case strings.HasPrefix(cmd[j], "--model="):
				model = strings.TrimPrefix(cmd[j], "--model=")
			case (cmd[j] == "-c" || cmd[j] == "--config") && j+1 < len(cmd) && strings.HasPrefix(cmd[j+1], "model="):
				model = strings.Trim(strings.TrimPrefix(cmd[j+1], "model="), `"'`)
			}
		}
	}
	return argv, model, found
}

// commandIndex is where the command of one simple command starts, past the variables set for it (put in env) and
// the wrappers that run what follows (env, with its -u / -i, command, exec, noglob, nocorrect); -1: there is none.
func commandIndex(cmd []string, env map[string]string) int {
	for i := 0; i < len(cmd); i++ {
		w := cmd[i]
		switch {
		case assignRe.MatchString(w):
			k, v, _ := strings.Cut(w, "=")
			env[k] = v
		case w == "env" || w == "command" || w == "exec" || w == "noglob" || w == "nocorrect":
		case w == "-u" && i > 0 && i+1 < len(cmd): // env -u NAME
			delete(env, cmd[i+1])
			i++
		case strings.HasPrefix(w, "-") && i > 0 && (cmd[i-1] == "env" || cmd[i-1] == "-i"): // env -i
		default:
			return i
		}
	}
	return -1
}

// aliasLauncher finds the claude command in an alias's words and what it is started with; settings is its own
// --settings (the last one, as Claude Code takes it), "" for none: launcher_settings.go.
func aliasLauncher(words []string) (l proto.Launcher, settings string, ok bool) {
	exported := map[string]string{}
	for len(words) > 0 {
		end := len(words)
		for i, w := range words {
			if w == ";" || w == "&&" || w == "||" || w == "|" || w == "&" {
				end = i
				break
			}
		}
		cmd := words[:end]
		if end < len(words) {
			words = words[end+1:]
		} else {
			words = nil
		}
		if len(cmd) > 0 && cmd[0] == "export" {
			for _, w := range cmd[1:] {
				if k, v, ok := strings.Cut(w, "="); ok && assignRe.MatchString(w) {
					exported[k] = v
				}
			}
			continue
		}
		env := map[string]string{}
		for k, v := range exported {
			env[k] = v
		}
		i := commandIndex(cmd, env)
		if i < 0 {
			continue
		}
		if filepath.Base(cmd[i]) != "claude" {
			continue
		}
		l = proto.Launcher{BaseURL: env["ANTHROPIC_BASE_URL"], Model: env["ANTHROPIC_MODEL"], Source: "zshrc"}
		for j := i + 1; j < len(cmd); j++ {
			if cmd[j] == "--model" && j+1 < len(cmd) && l.Model == "" {
				l.Model = cmd[j+1]
			} else if m, ok := strings.CutPrefix(cmd[j], "--model="); ok && l.Model == "" {
				l.Model = m
			}
			if cmd[j] == "--settings" && j+1 < len(cmd) {
				settings = cmd[j+1]
			} else if s, ok := strings.CutPrefix(cmd[j], "--settings="); ok {
				settings = s
			}
		}
		return l, settings, true
	}
	return proto.Launcher{}, "", false
}

// parseLaunchers parses `_claude_launchers` output: name padded to 26, base URL padded to 40, then model.
func parseLaunchers(out string) []proto.Launcher {
	var list []proto.Launcher
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 || !nameRe.MatchString(f[0]) {
			continue
		}
		l := proto.Launcher{Name: f[0]}
		rest := ""
		if len(line) > 26 {
			rest = line[26:]
		}
		if len(rest) > 40 {
			l.BaseURL = strings.TrimSpace(rest[:40])
			l.Model = strings.TrimSpace(rest[40:])
		} else {
			l.BaseURL = strings.TrimSpace(rest)
		}
		if l.BaseURL == "-" {
			l.BaseURL = ""
		}
		list = append(list, l)
	}
	return list
}
