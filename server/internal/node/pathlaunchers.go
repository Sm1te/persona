package node

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// Wrapper scripts on PATH (#5954): a claude entry point many people write as an executable file, not a ~/.zshrc
// function or alias (`~/.local/bin/claude-work`: a few exports for the gateway and key, then `exec claude … "$@"`).
// The launcher list takes them as launchers of source "path". Their text is only ever read, never run: the probe's
// stubs are shell functions, which a script (another process) does not see, so running one would start the real
// claude. What the probe would have found comes from the text instead (scriptProbe); the probe itself reports a
// launcher the shell finds as a file rather than run it (probeScript), whether or not the list has it.

// scriptHeadLines: how much of a file is read for the claude command, its first screen.
const scriptHeadLines = 40

// scriptMaxSize: anything bigger is no wrapper (a binary, a whole program) and is not opened.
const scriptMaxSize = 64 << 10

// scriptHeadSize: how much of a file is read at most, more than its first screen ever takes.
const scriptHeadSize = 16 << 10

// pathScanTimeout: how long a scan of PATH may take (a directory on a network mount that hangs); past it the last
// scan's scripts stand.
var pathScanTimeout = 3 * time.Second

// notScriptLaunchers are names that stand for an agent itself, never a wrapper's.
var notScriptLaunchers = []string{"claude", "codex", traexAgent, "omp", "agy", "dsh"}

type pathScan struct {
	list     []proto.Launcher
	settings map[string]string
	files    map[string]string
}

var lastPathScan struct {
	sync.Mutex
	running bool
	got     *pathScan
}

// pathLaunchers are the claude wrapper scripts on pathVar (the login shell's PATH), the first of each name only, as
// the shell finds commands. A name in taken (a function or alias, which the shell runs before any file) is passed
// over. settings: their own --settings, by name (launcher_settings.go); files: where each is. Within pathScanTimeout:
// past it, or while one that ran out still runs, the last scan's.
func pathLaunchers(pathVar string, taken map[string]bool) (list []proto.Launcher, settings, files map[string]string) {
	lastPathScan.Lock()
	if lastPathScan.running {
		got := lastPathScan.got
		lastPathScan.Unlock()
		return got.result(taken)
	}
	lastPathScan.running = true
	lastPathScan.Unlock()
	done := make(chan *pathScan, 1)
	go func() {
		s := scanPath(pathVar, taken)
		lastPathScan.Lock()
		lastPathScan.running, lastPathScan.got = false, s
		lastPathScan.Unlock()
		done <- s
	}()
	select {
	case s := <-done:
		return s.result(taken)
	case <-time.After(pathScanTimeout):
		lastPathScan.Lock()
		got := lastPathScan.got
		lastPathScan.Unlock()
		return got.result(taken)
	}
}

// result is the scan without the names taken now (a function defined since it ran is what the shell runs).
func (s *pathScan) result(taken map[string]bool) ([]proto.Launcher, map[string]string, map[string]string) {
	if s == nil {
		return nil, map[string]string{}, map[string]string{}
	}
	list := slices.DeleteFunc(slices.Clone(s.list), func(l proto.Launcher) bool { return taken[l.Name] })
	settings, files := mapsClone(s.settings), mapsClone(s.files)
	for name := range taken {
		delete(settings, name)
		delete(files, name)
	}
	return list, settings, files
}

func mapsClone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func scanPath(pathVar string, taken map[string]bool) *pathScan {
	s := &pathScan{settings: map[string]string{}, files: map[string]string{}}
	first := map[string]bool{}
	seenDir := map[string]bool{}
	for _, dir := range filepath.SplitList(pathVar) {
		if !filepath.IsAbs(dir) || seenDir[dir] {
			continue // "" and "." are the current directory, which says nothing about this machine
		}
		seenDir[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if first[name] || !shellcmd.NameRe.MatchString(name) || !(e.Type().IsRegular() || e.Type()&fs.ModeSymlink != 0) {
				continue
			}
			path := filepath.Join(dir, name)
			st, err := os.Stat(path) // a link (to a dotfiles checkout) counts as what it points to
			if err != nil || !st.Mode().IsRegular() || st.Mode()&0o111 == 0 {
				continue
			}
			first[name] = true // what the shell runs for name; a later directory's file of that name never runs
			if taken[name] || slices.Contains(notScriptLaunchers, name) || st.Size() > scriptMaxSize {
				continue
			}
			head, err := scriptHead(path)
			if err != nil {
				continue
			}
			w, ok := wrapperScript(head)
			if !ok {
				continue
			}
			s.list = append(s.list, proto.Launcher{Name: name, BaseURL: w.shown("ANTHROPIC_BASE_URL"), Model: w.model(), Source: "path"})
			if w.settings != "" {
				s.settings[name] = w.settings
			}
			s.files[name] = path
		}
	}
	slices.SortFunc(s.list, func(a, b proto.Launcher) int { return strings.Compare(a.Name, b.Name) })
	return s
}

// scriptHead is a script's first scriptHeadLines lines; an error for what is no script (no #!, or binary). Two bytes
// are read first: most of a PATH is programs, not scripts.
func scriptHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	magic := make([]byte, 2)
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != "#!" {
		return "", errors.New("not a script")
	}
	rest, err := io.ReadAll(io.LimitReader(f, scriptHeadSize-2))
	if err != nil {
		return "", err
	}
	b := append(magic, rest...)
	if bytes.IndexByte(b, 0) >= 0 {
		return "", errors.New("not a script")
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 4<<10), scriptHeadSize)
	for sc.Scan() && len(lines) < scriptHeadLines {
		lines = append(lines, sc.Text())
	}
	return strings.Join(lines, "\n"), nil
}

// wrapper is what a wrapper script's text says about the claude it starts.
type wrapper struct {
	env      map[string]string // what claude gets from the script: exported, set in front of it
	args     []string          // claude's arguments, "$@" where the script's own go
	settings string            // its own --settings (the last one, as Claude Code takes it)
	// branch: the claude command is inside an if, a case or a loop: what the text shows may not be what runs.
	branch bool
}

// shown is the variable k as the list shows it: "" when the text alone does not say (another variable, a command's
// output), never a guess.
func (w wrapper) shown(k string) string {
	if v := w.env[k]; !strings.ContainsAny(v, "$`") {
		return v
	}
	return ""
}

// model is the model it starts: --model wins over ANTHROPIC_MODEL, as in Claude Code.
func (w wrapper) model() string {
	for i, a := range w.args {
		if a == "--model" && i+1 < len(w.args) && !strings.ContainsAny(w.args[i+1], "$`") {
			return w.args[i+1]
		}
		if m, ok := strings.CutPrefix(a, "--model="); ok && !strings.ContainsAny(m, "$`") {
			return m
		}
	}
	return w.shown("ANTHROPIC_MODEL")
}

// forwardsArgs: "$@" (or $*) among claude's arguments, which every wrapper Persona can start through has: the start
// passes its own (the session, the settings with Persona's hooks) after the launcher's name.
func forwardsArgs(args []string) bool {
	return slices.ContainsFunc(args, isScriptArgs)
}

func isScriptArgs(a string) bool { return a == "$@" || a == "${@}" || a == "$*" || a == "${*}" }

// fixedClaudeArgs: claude's own arguments in a wrapper are flags and their values only. -p / --print, or a word that
// is no flag's value (a prompt, a subcommand like `mcp add`), is a script that uses claude for something, no wrapper.
// A word after a flag is its value unless the flag takes none, or the word reads as a sentence (a prompt) after a flag
// that takes no text.
func fixedClaudeArgs(args []string) bool {
	for i, a := range args {
		switch {
		case isScriptArgs(a):
		case a == "-p" || a == "--print":
			return false
		case strings.HasPrefix(a, "-"):
		case i > 0 && strings.HasPrefix(args[i-1], "--") && !strings.Contains(args[i-1], "=") && !slices.Contains(claudeSwitches, args[i-1]):
			if strings.ContainsAny(a, " \t") && !strings.HasPrefix(a, "{") && !strings.HasPrefix(a, "[") && !slices.Contains(claudeTextFlags, args[i-1]) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// claudeSwitches are Claude Code flags that take no value: a word after one is no value of theirs.
var claudeSwitches = []string{"--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "--verbose", "--continue",
	"--ide", "--strict-mcp-config", "--chrome", "--no-chrome", "--fork-session", "--replay-user-messages", "--include-partial-messages"}

// claudeTextFlags take free text (spaces and all) as their value.
var claudeTextFlags = []string{"--append-system-prompt", "--system-prompt"}

// varRef is $NAME or ${NAME}; any other ${…} form (${A:-b}, ${A%/}) is left as written, which makes the value unknown.
var varRef = lazyre.New(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// singleQuotedDollar: a line with a $ in single quotes, which the shell keeps as written (the words the line is split
// into have lost their quotes, so its values are not expanded at all).
var singleQuotedDollar = lazyre.New(`'[^']*\$[^']*'`)

// heredocStart: a here-document, whose body lines are text, not commands (<<EOF, <<-'EOF', <<"EOF"; not <<< word).
var heredocStart = lazyre.New(`(?:^|[^<])<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// blockOpen / blockClose / blockWords: a branch or a loop and its parts. A claude command inside one may not be what
// runs.
var (
	blockOpen  = []string{"if", "case", "while", "until", "for", "select"}
	blockClose = []string{"fi", "esac", "done"}
	blockWords = []string{"then", "else", "elif", "do"}
)

// wrapperScript reads a script's first screen line by line as the shell would run it top to bottom: assignments and
// exports (a value naming a variable the script set before gets its value), unset, then the first command that is
// claude (by name or path, through exec / env / command), which must pass the script's arguments on and give claude
// nothing but flags of its own, not as part of a pipeline. A line it cannot follow (a function, a group) is skipped,
// and so is a here-document's text; a claude inside an if or a loop counts, marked as a branch.
func wrapperScript(text string) (wrapper, bool) {
	vars := map[string]string{}
	exported := map[string]bool{}
	depth := 0 // how many branches and loops the line is in
	heredoc := ""
	for _, line := range logicalLines(text) {
		if heredoc != "" {
			if strings.TrimLeft(strings.TrimSpace(line), "\t") == heredoc {
				heredoc = ""
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if m := heredocStart.FindStringSubmatch(line); m != nil {
			heredoc = m[1]
			continue
		}
		literal := singleQuotedDollar.MatchString(line)
		expand := func(v string) string {
			if literal {
				return v
			}
			return varRef.ReplaceAllStringFunc(v, func(ref string) string {
				m := varRef.FindStringSubmatch(ref)
				if x, ok := vars[m[1]+m[2]]; ok {
					return x
				}
				return ref
			})
		}
		segs, ok := shellcmd.Split(line)
		if !ok {
			continue
		}
		seps := separators(line, segs)
		for si, seg := range segs {
			cmd := seg.Words
			if len(cmd) == 0 {
				continue
			}
			switch {
			case slices.Contains(blockOpen, cmd[0]):
				depth++
				continue // its condition is no command of the script's own
			case slices.Contains(blockClose, cmd[0]):
				depth = max(0, depth-1)
				continue
			case slices.Contains(blockWords, cmd[0]):
				cmd = cmd[1:] // `then exec claude …` on one line: the command after the word
				if len(cmd) == 0 {
					continue
				}
			}
			// After && or ||, or inside a branch or a loop, a command may not run.
			guarded := depth > 0 || si > 0 && (seps[si-1] == "&&" || seps[si-1] == "||")
			switch {
			case cmd[0] == "export":
				for _, w := range cmd[1:] {
					if k, v, ok := strings.Cut(w, "="); ok && shellcmd.AssignRe.MatchString(w) {
						vars[k], exported[k] = expand(v), true
					} else if shellcmd.NameRe.MatchString(w) {
						exported[w] = true
					}
				}
			case cmd[0] == "unset":
				for _, k := range cmd[1:] {
					delete(vars, k)
					delete(exported, k)
				}
			case !slices.ContainsFunc(cmd, func(w string) bool { return !shellcmd.AssignRe.MatchString(w) }): // A=1 B=2
				for _, w := range cmd {
					k, v, _ := strings.Cut(w, "=")
					vars[k] = expand(v)
				}
			case cmd[0] == "exit" && !guarded:
				return wrapper{}, false // the script ends before any claude
			default:
				words := slices.Clone(cmd)
				for i, w := range words { // /usr/bin/env claude: env in the command's place only
					if shellcmd.AssignRe.MatchString(w) || w == "exec" || w == "command" || w == "noglob" || w == "nocorrect" || i > 0 && strings.HasPrefix(w, "-") {
						continue
					}
					if filepath.Base(w) == "env" {
						words[i] = "env"
						continue
					}
					break
				}
				front := map[string]string{}
				i := shellcmd.CommandIndex(words, front)
				if i < 0 {
					continue
				}
				if filepath.Base(words[i]) != "claude" {
					if slices.Contains(words[:i], "exec") {
						return wrapper{}, false // the script becomes something else here
					}
					continue
				}
				args := words[i+1:]
				if !forwardsArgs(args) || !fixedClaudeArgs(args) || si > 0 && seps[si-1] == "|" || si < len(seps) && seps[si] == "|" {
					return wrapper{}, false // a script that runs claude for its own ends, no wrapper
				}
				w := wrapper{env: map[string]string{}, branch: guarded}
				if !envIgnored(words[:i]) {
					for k := range exported {
						if v, ok := vars[k]; ok {
							w.env[k] = v
						}
					}
				}
				for k, v := range front {
					w.env[k] = expand(v)
				}
				for _, a := range args {
					w.args = append(w.args, expand(a))
				}
				w.settings = settingsArg(w.args)
				return w, true
			}
		}
	}
	return wrapper{}, false
}

// envIgnored: `env -i` (--ignore-environment, or POSIX `env -`) in front of the command: claude gets only what is set
// after it.
func envIgnored(front []string) bool {
	for i, w := range front {
		if w == "env" && i+1 < len(front) && (front[i+1] == "-i" || front[i+1] == "--ignore-environment" || front[i+1] == "-") {
			return true
		}
	}
	return false
}

// separators are what joins each segment of line to the next: "|", "||", "&&", ";", "&" or "" (a new line); a
// segment the text cannot be found again for gives "".
func separators(line string, segs []shellcmd.Segment) []string {
	out := make([]string, max(0, len(segs)-1))
	at, ends := 0, make([]int, len(segs))
	starts := make([]int, len(segs))
	for i, s := range segs {
		j := strings.Index(line[at:], s.Text)
		if j < 0 {
			return out
		}
		starts[i], ends[i] = at+j, at+j+len(s.Text)
		at = ends[i]
	}
	for i := range out {
		sep := strings.TrimSpace(line[ends[i]:starts[i+1]])
		if sep == "|&" {
			sep = "|"
		}
		out[i] = sep
	}
	return out
}

// logicalLines is text as the shell reads its lines: a line ending in a backslash goes on with the next one, except a
// comment's.
func logicalLines(text string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(text, "\n") {
		trailing := len(line) - len(strings.TrimRight(line, `\`))
		if trailing%2 == 1 && (cur.Len() > 0 || !strings.HasPrefix(strings.TrimSpace(line), "#")) {
			cur.WriteString(line[:len(line)-1])
			continue
		}
		cur.WriteString(line)
		out = append(out, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// scriptLauncher is where launcher name's script is when it is one on PATH, "" otherwise. The list is read first when
// it has not been since the node started.
func (n *Node) scriptLauncher(ctx context.Context, name string) string {
	launcherCache.Lock()
	loaded := !launcherCache.at.IsZero()
	launcherCache.Unlock()
	if !loaded {
		_, _ = n.launchers(ctx)
	}
	launcherCache.Lock()
	defer launcherCache.Unlock()
	return launcherCache.scripts[name]
}

// scriptProbe is the probe of a wrapper script, read from its text (see the top): claude, its arguments with args in
// place of the script's own, and the environment the script gives it. A value the text does not say (another
// variable, a command's output) is left out rather than passed on as written: it would go to a gateway as a key. A
// claude inside a branch is not seen: which one runs, the text cannot say.
func scriptProbe(path string, args []string) (Probe, error) {
	head, err := scriptHead(path)
	if err != nil {
		return Probe{}, errProbeUnseen
	}
	w, ok := wrapperScript(head)
	if !ok || w.branch {
		return Probe{}, errProbeUnseen
	}
	p := Probe{Agent: "claude", Env: map[string]string{}}
	for _, a := range w.args {
		if isScriptArgs(a) {
			p.Args = append(p.Args, args...)
		} else {
			p.Args = append(p.Args, a)
		}
	}
	for k, v := range w.env {
		if v == "" || w.shown(k) != "" {
			p.Env[k] = v
		}
	}
	return p, nil
}

// scriptText is a wrapper script for its launcher page: the path (from ~), then its first screen, anything that looks
// like a key or token masked.
func scriptText(path, home string) (string, error) {
	head, err := scriptHead(path)
	if err != nil {
		return "", err
	}
	shown := path
	if rel, err := filepath.Rel(home, path); err == nil && home != "" && !strings.HasPrefix(rel, "..") {
		shown = "~/" + filepath.ToSlash(rel)
	}
	return "# " + shown + "\n" + redactSecrets(strings.TrimSpace(head)), nil
}
