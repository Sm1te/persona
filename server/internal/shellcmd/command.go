package shellcmd

import (
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// NameRe is a name an alias or a launcher may have: the shell's rule for a function name, a dash allowed.
var NameRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// AssignRe is a word that sets a variable (NAME=value).
var AssignRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*=`)

// CommandIndex is where the command of one simple command starts, past the variables set for it (put in env) and
// the wrappers that run what follows (env, with its -u / -i, command, exec, noglob, nocorrect); -1: there is none.
func CommandIndex(cmd []string, env map[string]string) int {
	for i := 0; i < len(cmd); i++ {
		w := cmd[i]
		switch {
		case AssignRe.MatchString(w):
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
