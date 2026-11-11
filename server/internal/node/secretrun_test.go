package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
)

// secret.run (secretrun.go). The password is a made-up value that must never come back.

const testSecret = "hunter2-SECRET-xyz"

// secretRunCall calls secret.run on a node at home with these commands and the test password, and returns the
// answer, the error and the params as they are after the call.
func secretRunCall(t *testing.T, commands []string, timeout time.Duration) (proto.SecretRunAnswer, error, json.RawMessage) {
	t.Helper()
	return secretRunCallWith(t, testSecret, commands, timeout)
}

// secretRunCallWith is secretRunCall with another password.
func secretRunCallWith(t *testing.T, password string, commands []string, timeout time.Duration) (proto.SecretRunAnswer, error, json.RawMessage) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("secret.run refuses Windows")
	}
	n := &Node{home: t.TempDir()}
	params, err := json.Marshal(proto.SecretRunParams{Commands: commands, Password: []byte(password), TimeoutMS: timeout.Milliseconds()})
	if err != nil {
		t.Fatal(err)
	}
	out, err := n.handle(context.Background(), proto.MethodSecretRun, params)
	var ans proto.SecretRunAnswer
	if err == nil {
		b, _ := json.Marshal(out)
		if strings.Contains(string(b), password) {
			t.Fatalf("the answer carries the password: %s", b)
		}
		_ = json.Unmarshal(b, &ans)
	} else if strings.Contains(err.Error(), password) {
		t.Fatalf("the error carries the password: %v", err)
	}
	return ans, err, params
}

// fakeSudo is an executable named sudo in a directory of its own running body (sh), for commands that call it by its
// absolute path: the node gives the password only to a command that runs sudo -S.
func fakeSudo(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sudo")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Only a command that runs sudo -S gets the password and a newline on its stdin; any other reads an empty stdin. The
// request's own bytes are zeroed once read.
func TestSecretRunGivesThePasswordOnlyToSudo(t *testing.T) {
	sudo := fakeSudo(t, `read p; echo "sudo read ${#p} bytes"`)
	ans, err, params := secretRunCall(t, []string{sudo + " -S -k -p '' whoami", `read p; echo "read ${#p} bytes"`}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) != 2 || ans.Results[0].ExitCode != 0 || ans.Results[1].ExitCode != 0 {
		t.Fatalf("results = %+v", ans.Results)
	}
	if got := ans.Results[0].Output; got != fmt.Sprintf("sudo read %d bytes\n", len(testSecret)) {
		t.Fatalf("sudo -S read %q, want the password", got)
	}
	if got := ans.Results[1].Output; got != "read 0 bytes\n" {
		t.Fatalf("a command without sudo -S read %q, want nothing", got)
	}
	if strings.Trim(string(params), "\x00") != "" {
		t.Fatal("the request's bytes were not zeroed")
	}
}

// A command whose output (stdout or stderr) held the password returns none of it, withheld; one whose output did not
// returns it whole.
func TestSecretRunWithholdsOutputWithThePassword(t *testing.T) {
	sudo := fakeSudo(t, `read p; echo "listed"; echo "got:$p" >&2`)
	ans, err, _ := secretRunCall(t, []string{sudo + " -S -k -p '' true", "echo fine"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r := ans.Results[0]; r.ExitCode != 0 || !r.OutputWithheld || r.Output != "" || r.Truncated {
		t.Fatalf("output with the password: %+v, want withheld", r)
	}
	if r := ans.Results[1]; r.OutputWithheld || r.Output != "fine\n" {
		t.Fatalf("output without the password: %+v, want it whole", r)
	}
}

// The commands run with PATH proto.SecretRunPath: a sudo (or anything) earlier in the user's own PATH is not what runs.
func TestSecretRunRunsWithAFixedPath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ls", "sudo"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho FAKE\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ans, err, _ := secretRunCall(t, []string{`echo "$PATH"; ls -d /`}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got := ans.Results[0].Output; got != proto.SecretRunPath+"\n/\n" {
		t.Fatalf("output %q, want PATH %s and the real ls", got, proto.SecretRunPath)
	}
}

// The run stops at the first command that fails: the ones after it neither run nor appear.
func TestSecretRunStopsAtTheFirstFailure(t *testing.T) {
	dir := t.TempDir()
	marker := dir + "/third"
	ans, err, _ := secretRunCall(t, []string{"echo one", "echo two; exit 3", "touch " + marker}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) != 2 || ans.Results[0].ExitCode != 0 || ans.Results[1].ExitCode != 3 {
		t.Fatalf("results = %+v, want one then exit 3", ans.Results)
	}
	if strings.TrimSpace(ans.Results[1].Output) != "two" {
		t.Fatalf("second output = %q", ans.Results[1].Output)
	}
	if fileExists(marker) {
		t.Fatal("the command after the failure ran")
	}
}

// A shell can handle the deadline's TERM by exiting successfully; cancellation still ends the run.
func TestSecretRunTermTrapZeroExitRemainsTimedOut(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "blocked-input")
	marker := filepath.Join(dir, "next-command")
	command := "mkfifo " + shellQuote(fifo) + " || exit 1; exec 3<>" + shellQuote(fifo) + "; trap 'echo term; exit 0' TERM; echo ready; read ignored <&3"
	ans, err, _ := secretRunCall(t, []string{command, "echo never > " + shellQuote(marker)}, 700*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) == 0 || !strings.Contains(ans.Results[0].Output, "ready\n") || !strings.Contains(ans.Results[0].Output, "term\n") {
		t.Fatalf("TERM fixture did not reach its ready and handler markers: %+v", ans.Results)
	}
	if len(ans.Results) != 1 || !ans.Results[0].TimedOut || ans.Results[0].ExitCode != -1 {
		t.Fatalf("results = %+v, want one timed out with exit -1", ans.Results)
	}
	if fileExists(marker) {
		t.Fatal("the command after the deadline ran")
	}
}

func TestSecretRunSuccessfulCommandsBeforeDeadlineStaySuccessful(t *testing.T) {
	ans, err, _ := secretRunCall(t, []string{"echo first", "echo second"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) != 2 {
		t.Fatalf("results = %+v, want both successful commands", ans.Results)
	}
	for i, want := range []string{"first\n", "second\n"} {
		if r := ans.Results[i]; r.ExitCode != 0 || r.TimedOut || r.Output != want {
			t.Fatalf("result %d = %+v, want success with output %q", i, r, want)
		}
	}
}

// Running out of the run's time kills the command's whole process group, a child it left in the background too.
func TestSecretRunTimeoutKillsTheGroup(t *testing.T) {
	start := time.Now()
	ans, err, _ := secretRunCall(t, []string{"sleep 60 & echo $!; wait", "echo never"}, 700*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("the run took %v", time.Since(start))
	}
	if len(ans.Results) != 1 || !ans.Results[0].TimedOut || ans.Results[0].ExitCode != -1 {
		t.Fatalf("results = %+v, want one timed out with exit -1", ans.Results)
	}
	// Combined stderr may append the shell's child-termination diagnostic after the fixture's first PID line.
	pidLine, _, _ := strings.Cut(strings.TrimSpace(ans.Results[0].Output), "\n")
	child, err := strconv.Atoi(pidLine)
	if err != nil || child <= 0 {
		t.Fatalf("output %q: %v", ans.Results[0].Output, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for osproc.Alive(child) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if osproc.Alive(child) {
		t.Fatalf("the background child %d outlived the timeout", child)
	}
}

// A group that ignores SIGTERM, a child forked every 50 ms, is gone after the grace: SIGKILL, swept until empty.
func TestSecretRunTimeoutEndsWhatIgnoresSIGTERM(t *testing.T) {
	old := secretRunGrace
	secretRunGrace = 300 * time.Millisecond
	t.Cleanup(func() { secretRunGrace = old })
	ans, err, _ := secretRunCall(t, []string{"trap '' TERM; echo $$; while :; do sleep 60 & sleep 0.05; done"}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) != 1 || !ans.Results[0].TimedOut || ans.Results[0].ExitCode != -1 {
		t.Fatalf("results = %+v, want one timed out", ans.Results)
	}
	group, err := strconv.Atoi(strings.TrimSpace(ans.Results[0].Output))
	if err != nil {
		t.Fatalf("output %q: %v", ans.Results[0].Output, err)
	}
	deadline := time.Now().Add(2 * time.Second) // killed members count until reaped
	for osproc.Alive(-group) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if osproc.Alive(-group) {
		t.Fatalf("process group %d outlived the run", group)
	}
}

// A shell that exits at once, leaving a child that holds its output, still has its group stopped at the run's deadline
// (exec calls Cancel only while the shell runs): the run returns at about its timeout, not when the child would end,
// the child is gone, and the list stops at that command. The next one never started and is not reported as one that
// ran out of time (#4363).
func TestSecretRunTimeoutStopsAChildHoldingTheOutputOfAShellThatExited(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "second")
	start := time.Now()
	ans, err, _ := secretRunCall(t, []string{"sleep 4 & echo $!", "echo second > " + marker}, time.Second)
	took := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if took > 3*time.Second { // the run has 1 s; the child would hold it for 4
		t.Fatalf("the run took %v, want about its 1 s timeout; results = %+v", took, ans.Results)
	}
	if len(ans.Results) != 1 || !ans.Results[0].TimedOut || ans.Results[0].ExitCode != -1 {
		t.Fatalf("results = %+v, want only the first command, timed out with exit -1", ans.Results)
	}
	pidLine, _, _ := strings.Cut(ans.Results[0].Output, "\n")
	child, err := strconv.Atoi(pidLine)
	if err != nil || child <= 0 {
		t.Fatalf("output %q: %v", ans.Results[0].Output, err)
	}
	if osproc.Alive(child) { // the run returns once the group is empty (endGroup)
		t.Fatalf("the background child %d outlived the run", child)
	}
	if fileExists(marker) {
		t.Fatal("the command after the deadline ran")
	}
}

// A command whose turn comes once the run's time is used up does not start and gets no result (inTime false), not one
// that says it ran out of time (#4363).
func TestSecretRunCommandAfterTheDeadlineDoesNotRun(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "late")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now())
	defer cancel()
	if r, inTime := runSecretCommand(ctx, t.TempDir(), "touch "+marker, []byte(testSecret)); inTime {
		t.Fatalf("result %+v, want none: the run's time was used up", r)
	}
	if fileExists(marker) {
		t.Fatal("the command ran after the deadline")
	}
}

// A child that holds the output only briefly and ends within the run's time changes nothing: Wait reads its output to
// the end, and the next command runs.
func TestSecretRunWaitsForAChildThatEndsInTime(t *testing.T) {
	ans, err, _ := secretRunCall(t, []string{"(sleep 0.3; echo late) & echo early", "echo second"}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Results) != 2 {
		t.Fatalf("results = %+v, want both commands", ans.Results)
	}
	for i, want := range []string{"early\nlate\n", "second\n"} {
		if r := ans.Results[i]; r.ExitCode != 0 || r.TimedOut || r.Output != want {
			t.Fatalf("result %d = %+v, want exit 0 with output %q", i, r, want)
		}
	}
}

// Output past what is kept: only its end comes back.
func TestSecretRunClipsTheOutput(t *testing.T) {
	ans, err, _ := secretRunCall(t, []string{"yes x | head -n 20000"}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	r := ans.Results[0]
	if r.ExitCode != 0 || !r.Truncated || len(r.Output) != secretRunClip || strings.Trim(r.Output, "x\n") != "" {
		t.Fatalf("exit %d, truncated %v, %d bytes", r.ExitCode, r.Truncated, len(r.Output))
	}
}

// A long password printed past what is kept is still found and the output withheld, nothing of it in the answer.
func TestSecretRunWithholdsALongPasswordPastWhatIsKept(t *testing.T) {
	long := strings.Repeat("hunter2-SECRET-", 133) // 1995 bytes; 200 lines of it are past the 256 KiB kept
	sudo := fakeSudo(t, `read p; yes "$p" | head -n 200`)
	ans, err, _ := secretRunCallWith(t, long, []string{sudo + " -S -k -p '' true"}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r := ans.Results[0]; r.ExitCode != 0 || !r.OutputWithheld || r.Output != "" {
		t.Fatalf("result %+v, want the output withheld", r)
	}
}

// The buffer holds the last keep bytes of what was written, however it was cut into writes, and says when anything
// before them was dropped.
func TestSecretOutputBufferKeepsTheEnd(t *testing.T) {
	const keep = 64
	stream := make([]byte, 0, 1000)
	for i := 0; len(stream) < cap(stream); i++ {
		stream = append(stream, byte('a'+i%26))
	}
	for _, sizes := range [][]int{{1}, {3, 7}, {63}, {64}, {65}, {200, 1}, {1000}} {
		b := newSecretOutputBuffer(keep)
		for i, n := 0, 0; n < len(stream); i++ {
			w := min(sizes[i%len(sizes)], len(stream)-n)
			_, _ = b.Write(stream[n : n+w])
			n += w
			got, dropped := b.tail()
			want := stream[max(0, n-keep):n]
			if string(got) != string(want) || dropped != (n > keep) || cap(b.buf) != 2*keep {
				t.Fatalf("writes of %v, after %d bytes: kept %q (dropped %v, cap %d), want %q", sizes, n, got, dropped, cap(b.buf), want)
			}
		}
	}
}

// The password split across writes, one byte at a time even, is still found and masked in what is kept; nothing is
// held back once the output ended; the output is withheld; and zero clears what was read.
func TestSecretRedactorMasksAcrossWrites(t *testing.T) {
	stream := "abc " + testSecret + " def " + testSecret + testSecret + " " + testSecret[:5]
	want := "abc *** def ****** " + testSecret[:5] // a password's start that never completes is just output
	for _, size := range []int{1, 2, 7, len(testSecret) - 1, len(testSecret), 1000} {
		r := newSecretRedactor([]byte(testSecret), 1024)
		for n := 0; n < len(stream); n += size {
			_, _ = r.Write([]byte(stream[n:min(n+size, len(stream))]))
		}
		r.release(true)
		if kept, _ := r.kept.tail(); string(kept) != want {
			t.Fatalf("writes of %d: kept %q, want %q", size, kept, want)
		}
		if out, truncated, withheld := r.result(); out != "" || truncated || !withheld {
			t.Fatalf("writes of %d: result %q (truncated %v, withheld %v), want it withheld", size, out, truncated, withheld)
		}
		r.zero()
		if strings.Trim(string(r.held[:cap(r.held)])+string(r.kept.buf[:cap(r.kept.buf)]), "\x00") != "" {
			t.Fatal("zero left bytes behind")
		}
	}
	r := newSecretRedactor([]byte(testSecret), 1024)
	_, _ = r.Write([]byte("abc " + testSecret[:9] + " def"))
	r.release(true)
	if out, truncated, withheld := r.result(); out != "abc "+testSecret[:9]+" def" || truncated || withheld {
		t.Fatalf("output without the password: %q (truncated %v, withheld %v)", out, truncated, withheld)
	}
}

// What is kept is masked already: a cut through the output can leave a piece of "***", never of the password.
func TestSecretRedactorCutKeepsNoPiece(t *testing.T) {
	r := newSecretRedactor([]byte(testSecret), 64)
	_, _ = r.Write([]byte(strings.Repeat(testSecret+" ", 40)))
	r.release(true)
	kept, dropped := r.kept.tail()
	if !dropped || len(kept) != 64 || strings.Trim(string(kept), "* ") != "" {
		t.Fatalf("kept %q (dropped %v)", kept, dropped)
	}
}

// check asks only whether the node can run one: nothing runs and no password is needed.
func TestSecretRunCheck(t *testing.T) {
	n := &Node{home: t.TempDir()}
	out, err := n.handle(context.Background(), proto.MethodSecretRun, json.RawMessage(`{"check":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := out.(map[string]any); m["ok"] != true {
		t.Fatalf("check = %v", out)
	}
}

// A node on Windows refuses, the check included: there is no sudo -S there.
func TestSecretRunRefusesWindows(t *testing.T) {
	old := secretRunGOOS
	secretRunGOOS = "windows"
	t.Cleanup(func() { secretRunGOOS = old })
	n := &Node{home: t.TempDir()}
	for _, params := range []string{`{"check":true}`, `{"commands":["true"],"password":"aHVudGVyMg==","timeout_ms":1000}`} {
		if _, err := n.handle(context.Background(), proto.MethodSecretRun, json.RawMessage(params)); err == nil {
			t.Fatalf("%s: no refusal on Windows", params)
		}
	}
}

// Runs outside the limits are refused before anything starts, without the password in the error.
func TestSecretRunRefusesOutOfLimits(t *testing.T) {
	for name, change := range map[string]func(p *proto.SecretRunParams){
		"no command":     func(p *proto.SecretRunParams) { p.Commands = nil },
		"six commands":   func(p *proto.SecretRunParams) { p.Commands = []string{"true", "true", "true", "true", "true", "true"} },
		"two lines":      func(p *proto.SecretRunParams) { p.Commands = []string{"true\ntrue"} },
		"escape":         func(p *proto.SecretRunParams) { p.Commands = []string{"echo \x1b[2Khidden"} },
		"line separator": func(p *proto.SecretRunParams) { p.Commands = []string{"true \u2028# ; rm -rf x"} },
		"blank":          func(p *proto.SecretRunParams) { p.Commands = []string{"  "} },
		"too long": func(p *proto.SecretRunParams) {
			p.Commands = []string{strings.Repeat("x", proto.SecretRunMaxCommandLen+1)}
		},
		"zero width":                 func(p *proto.SecretRunParams) { p.Commands = []string{"brew\u200b services start x"} },
		"no-break space":             func(p *proto.SecretRunParams) { p.Commands = []string{"ls\u00a0/tmp"} },
		"cyrillic":                   func(p *proto.SecretRunParams) { p.Commands = []string{"ec\u0430o hi"} },
		"no password":                func(p *proto.SecretRunParams) { p.Password = nil },
		"password with a line break": func(p *proto.SecretRunParams) { p.Password = []byte("hunter2\nrm -rf x") },
		"password with NUL":          func(p *proto.SecretRunParams) { p.Password = []byte("hunter2\x00x") },
		"password with DEL":          func(p *proto.SecretRunParams) { p.Password = []byte("hunter2\x7f") },
		"no timeout":                 func(p *proto.SecretRunParams) { p.TimeoutMS = 0 },
		"timeout wraps":              func(p *proto.SecretRunParams) { p.TimeoutMS = 18446744073710 }, // times 1e6 ns wraps past 2^64 to under a second
		"timeout > max":              func(p *proto.SecretRunParams) { p.TimeoutMS = (proto.SecretRunMaxTimeout + time.Second).Milliseconds() },
	} {
		p := proto.SecretRunParams{Commands: []string{"true"}, Password: []byte(testSecret), TimeoutMS: 1000}
		change(&p)
		raw, _ := json.Marshal(p)
		n := &Node{home: t.TempDir()}
		_, err := n.handle(context.Background(), proto.MethodSecretRun, raw)
		if err == nil {
			t.Errorf("%s: not refused", name)
		} else if strings.Contains(err.Error(), testSecret) {
			t.Errorf("%s: the error carries the password: %v", name, err)
		}
	}
}
