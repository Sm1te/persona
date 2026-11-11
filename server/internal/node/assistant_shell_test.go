package node

import (
	"bytes"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The shell the assistant's launcher runs in is the login shell's, never a bare bash on Windows: there it is WSL, and a
// claude handed to WSL exited at once (#1994).
func TestInteractiveShellIsTheLoginShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		// zsh wherever it is installed, except for a bash user with no ~/.zshrc (loginShellProgram). The login shell
		// and home are set here, so the machine running the test does not decide the answer.
		_, zerr := exec.LookPath("zsh")
		for _, tc := range []struct {
			name, login string
			zshrc       bool
			want        string
		}{
			{name: "zsh user", login: "/bin/zsh", want: "zsh"},
			{name: "bash user with a .zshrc", login: "/bin/bash", zshrc: true, want: "zsh"},
			{name: "bash user without a .zshrc", login: "/bin/bash", want: "bash"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("SHELL", tc.login)
				if tc.zshrc {
					if err := os.WriteFile(filepath.Join(home, ".zshrc"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				want := tc.want
				if zerr != nil {
					want = "bash" // no zsh on this machine
				}
				if sh, err := interactiveShell(); err != nil || sh != want {
					t.Fatalf("got %q %v, want %q", sh, err, want)
				}
			})
		}
		return
	}
	sh, err := interactiveShell()
	if loginShellProgram() == "" { // Git Bash not found
		if err == nil || !strings.Contains(err.Error(), "Git Bash") {
			t.Fatalf("no Git Bash: %q %v", sh, err)
		}
		return
	}
	if err != nil || !strings.EqualFold(filepath.Base(sh), "bash.exe") || strings.Contains(strings.ToLower(sh), `\system32\`) {
		t.Fatalf("%q %v", sh, err)
	}
}

func TestAnAssistantThatDoesNotStartSaysWhy(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	const id = "0c000000-0000-4000-8000-000000000021"
	lines := "Welcome\nopen terminal failed: not a terminal\n"
	watchAssistantStderr(logger, &assistantProc{id: id, launcher: "claude", born: time.Now()}, io.NopCloser(strings.NewReader(lines)))
	if out := buf.String(); !strings.Contains(out, "assistant 0c000000: claude exited") || !strings.Contains(out, "Welcome | open terminal failed: not a terminal") {
		t.Fatalf("log: %q", out)
	}

	// One that ran for a while and ended (stopped, idle) says nothing but its error lines.
	buf.Reset()
	watchAssistantStderr(logger, &assistantProc{id: id, launcher: "claude", born: time.Now().Add(-time.Hour)}, io.NopCloser(strings.NewReader("bye\nAPI Error: overloaded\n")))
	if out := buf.String(); out != "assistant 0c000000: API Error: overloaded\n" {
		t.Fatalf("log: %q", out)
	}

	// And one that exits early without a word adds nothing.
	buf.Reset()
	watchAssistantStderr(logger, &assistantProc{id: id, launcher: "claude", born: time.Now()}, io.NopCloser(strings.NewReader("")))
	if buf.Len() != 0 {
		t.Fatalf("log: %q", buf.String())
	}
}
