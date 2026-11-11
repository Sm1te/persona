//go:build !windows

package node

import (
	"errors"
	"os/exec"
	"strconv"
	"testing"
)

// Only a shell that could not find claude is Claude Code missing; a run that timed out, failed otherwise or never
// started leaves the version unknown.
func TestClaudeProbeResult(t *testing.T) {
	exit := func(code int, stderr string) error {
		err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("exit %d: %v", code, err)
		}
		ee.Stderr = []byte(stderr)
		return ee
	}
	for _, tc := range []struct {
		name     string
		out      string
		err      error
		timedOut bool
		version  string
		missing  bool
	}{
		{name: "found", out: "2.1.322 (Claude Code)\n", version: "2.1.322"},
		{name: "zsh not found", err: exit(127, "zsh:1: command not found: claude\n"), missing: true},
		{name: "bash not found", err: exit(127, "bash: line 1: claude: command not found\n"), missing: true},
		{name: "127 alone", err: exit(127, ""), missing: true},
		{name: "rc noise only", err: exit(1, "zsh: command not found: compdef\n")},
		{name: "timed out", err: exit(137, ""), timedOut: true},
		{name: "other failure", err: exit(2, "Error: something went wrong\n")},
		{name: "no shell", err: exec.ErrNotFound},
	} {
		v, missing := claudeProbeResult([]byte(tc.out), tc.err, tc.timedOut)
		if v != tc.version || missing != tc.missing {
			t.Errorf("%s: (%q, %v), want (%q, %v)", tc.name, v, missing, tc.version, tc.missing)
		}
	}
}
