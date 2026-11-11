package node

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Each wrapper describes options without a value, options with a value, and
// required positional arguments before the executable. Unknown options stop parsing.
type commandWrapper struct {
	flags, values string
	positional    int
	assignments   bool
}

var commandWrappers = map[string]commandWrapper{
	"env":     {flags: "- -i --ignore-environment -0 --null -v --debug", values: "-u --unset -C --chdir --argv0", assignments: true},
	"sudo":    {flags: "-A -b -E -H -K -k -n -P -S --askpass --background --preserve-env --set-home --reset-timestamp --non-interactive --stdin", values: "-u --user -g --group -h --host -p --prompt -C --close-from -D --chdir -R --chroot -T --command-timeout -r --role -t --type --other-user", assignments: true},
	"nice":    {values: "-n --adjustment"},
	"timeout": {flags: "--preserve-status --foreground -v --verbose", values: "-s --signal -k --kill-after", positional: 1},
	"nohup":   {},
	"command": {flags: "-p"},
	"builtin": {},
	"exec":    {flags: "-c -l", values: "-a"},
	"xargs":   {flags: "-0 --null -r --no-run-if-empty -t --verbose -p --interactive -x --exit", values: "-I --replace -d --delimiter -E --eof -L --max-lines -n --max-args -P --max-procs -s --max-chars --process-slot-var"},
	"stdbuf":  {values: "-i --input -o --output -e --error"},
	"time":    {flags: "-p --portability -v --verbose -a --append -q --quiet", values: "-f --format -o --output"},
	"setsid":  {flags: "-c --ctty -f --fork -w --wait"},
	"busybox": {},
}

func wrapperOption(spec commandWrapper, word string) (takes, known bool) {
	return wrapperHasOption(spec.values, word), wrapperHasOption(spec.values+" "+spec.flags, word)
}

func wrapperHasOption(list, word string) bool {
	for _, item := range strings.Fields(list) {
		if item == word {
			return true
		}
	}
	return false
}

// unwrapCommand returns a suffix of words, retaining its indexes for syntax metadata.
func unwrapCommand(words []string) ([]string, bool) {
	for len(words) > 0 {
		name := filepath.Base(words[0])
		spec, wrapper := commandWrappers[name]
		if !wrapper {
			return words, true
		}
		words = words[1:]
		// command's lookup mode prints names rather than invoking them.
		if name == "command" && len(words) > 0 && (words[0] == "-v" || words[0] == "-V") {
			return nil, true
		}
		for len(words) > 0 {
			w := words[0]
			if w == "--" {
				words = words[1:]
				break
			}
			if spec.assignments && isAssignment(w) {
				words = words[1:]
				continue
			}
			if w == "-" && wrapperHasOption(spec.flags, w) {
				words = words[1:]
				continue
			}
			if !strings.HasPrefix(w, "-") || w == "-" {
				break
			}
			// nice also accepts an adjustment as -NUMBER.
			if name == "nice" && len(w) > 1 && strings.Trim(w[1:], "0123456789") == "" {
				words = words[1:]
				continue
			}
			option, _, inline := strings.Cut(w, "=")
			takes, known := wrapperOption(spec, option)
			if strings.HasPrefix(w, "--") {
				if !known || inline && !takes {
					return words, false
				}
			} else {
				// Short options can be combined; a value consumes the rest of the word.
				known, takes, inline = true, false, false
				for j := 1; j < len(w); j++ {
					var found bool
					takes, found = wrapperOption(spec, "-"+w[j:j+1])
					if !found {
						known = false
						break
					}
					if takes {
						inline = j+1 < len(w)
						break
					}
				}
				if !known {
					return words, false
				}
			}
			words = words[1:]
			if takes && !inline {
				if len(words) == 0 {
					return words, false
				}
				words = words[1:]
			}
		}
		if spec.assignments {
			for len(words) > 0 && isAssignment(words[0]) {
				words = words[1:]
			}
		}
		if len(words) < spec.positional {
			return words, false
		}
		words = words[spec.positional:]
	}
	return words, true
}

// Direct invocations and applets exposed by busybox use the same shell table.
var commandShells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"ash": true, "hush": true, "mksh": true, "pdksh": true, "posh": true, "yash": true,
	"oksh": true, "loksh": true, "csh": true, "tcsh": true, "rbash": true, "rksh": true,
}

// A shell consumes the word after the option containing c, including -lc and -cl.
// Match basenames with optional numeric versions, such as ksh93 and bash5.2.
func shellScriptIndex(words []string) int {
	if len(words) == 0 || !commandShells[strings.TrimRight(filepath.Base(words[0]), "0123456789.")] {
		return -1
	}
	for i := 1; i < len(words); i++ {
		option := words[i]
		if !strings.HasPrefix(option, "-") && !strings.HasPrefix(option, "+") || option == "--" {
			return -1
		}
		if wrapperHasOption("-o -O +o +O --rcfile --init-file", option) {
			i++
			continue
		}
		if option == "--command" || strings.HasPrefix(option, "-") && !strings.HasPrefix(option, "--") && strings.Contains(option[1:], "c") {
			if i+1 < len(words) {
				return i + 1
			}
			return -1
		}
	}
	return -1
}

// executionWord retains whether expansion happens now, rather than when literal
// shell/eval code is parsed again. Thus sh -c 'echo "$HOME"' remains ordinary.
func executionWord(word *syntax.Word) (value string, dynamic bool) {
	var out strings.Builder
	syntax.Walk(word, func(node syntax.Node) bool {
		switch p := node.(type) {
		case *syntax.Lit:
			out.WriteString(strings.ReplaceAll(p.Value, "\\", ""))
		case *syntax.SglQuoted:
			out.WriteString(p.Value)
			return false
		case nil, *syntax.Word, *syntax.DblQuoted:
		default:
			dynamic = true
			out.WriteByte('$')
			return false
		}
		return true
	})
	return out.String(), dynamic
}

// dynamicCommandName reports expansions in an executable position. The test
// command's literal brackets are syntax, not a glob or brace expansion.
func dynamicCommandName(name string) bool {
	if name == "[" || name == "[[" {
		return false
	}
	return strings.ContainsAny(name, "$`*?[]{}")
}

// Inspect partial syntax trees too: a shell can execute complete statements before
// reporting a later syntax error. Only executable positions expand the gate's scope.
func shellCommandUnknown(cmd string, includeCLI bool, depth int) bool {
	if depth > 16 {
		return true
	}
	tree, _ := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	unknown := false
	syntax.Walk(tree, func(node syntax.Node) bool {
		if unknown {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]string, len(call.Args))
		dynamic := make([]bool, len(call.Args))
		for i, word := range call.Args {
			words[i], dynamic[i] = executionWord(word)
		}
		rest, parsed := unwrapCommand(words)
		offset := len(words) - len(rest)
		if !parsed {
			for i, w := range rest {
				unknown = unknown || dynamic[offset+i] || strings.ContainsAny(w, "$`*?[]{}") || larkCLIRe.MatchString(w)
			}
			return !unknown
		}
		if len(rest) == 0 {
			return true
		}
		unknown = dynamic[offset] || dynamicCommandName(rest[0]) || includeCLI && isLarkCLI(rest[0])
		name := filepath.Base(rest[0])
		if name == "eval" || name == "source" || name == "." {
			for _, d := range dynamic[offset+1:] {
				unknown = unknown || d
			}
			if name == "eval" && !unknown {
				unknown = shellCommandUnknown(strings.Join(rest[1:], " "), includeCLI, depth+1)
			}
		}
		if i := shellScriptIndex(rest); i >= 0 {
			unknown = unknown || dynamic[offset+i] || shellCommandUnknown(rest[i], includeCLI, depth+1)
		}
		return !unknown
	})
	return unknown
}
