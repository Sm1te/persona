package shellcmd

import (
	"maps"
	"testing"
)

func TestCommandIndex(t *testing.T) {
	for _, c := range []struct {
		cmd  []string
		want int
		env  map[string]string
	}{
		{[]string{"A=1", "B=x=y", "claude", "-c"}, 2, map[string]string{"A": "1", "B": "x=y"}},
		{[]string{"env", "-i", "A=1", "exec", "noglob", "codex"}, 5, map[string]string{"A": "1"}},
		{[]string{"A=1", "env", "-u", "A", "claude"}, 4, map[string]string{}},
		{[]string{"command", "nocorrect", "claude"}, 2, map[string]string{}},
		{[]string{"A=1"}, -1, map[string]string{"A": "1"}},
		{nil, -1, map[string]string{}},
	} {
		env := map[string]string{}
		if got := CommandIndex(c.cmd, env); got != c.want || !maps.Equal(env, c.env) {
			t.Errorf("%q: got %d %v, want %d %v", c.cmd, got, env, c.want, c.env)
		}
	}
}
