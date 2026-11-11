package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
)

// secret.run (proto.SecretRunParams): the commands the user approved on the password card, run one after another
// with /bin/sh -c in the home directory with PATH proto.SecretRunPath, each in a session and process group of its own,
// stdout and stderr read together. A command that runs one sudo -S gets the password and a newline on its stdin, then
// the pipe closed (sudo reads it there); any other gets an empty stdin. The run stops at the first command that exits
// non-zero, cannot start or runs out of the run's time (its group is stopped: endGroup). A command whose output held
// the password returns none of it (secretRedactor masks it as it is read, and counts). The password bytes are zeroed
// when the run ends. Nothing here logs the params, the password or the output (docs/assistant/assistant.md "Password card").

// secretRunGOOS is the system the node runs on (a variable so a test can play Windows).
var secretRunGOOS = runtime.GOOS

// secretRunKeep is how much of a command's masked output is kept (its end); secretRunClip is how much of that goes
// back; secretRunChunk is the most the masking takes at a time.
const (
	secretRunKeep  = 256 << 10
	secretRunClip  = 16 << 10
	secretRunChunk = 32 << 10
)

// secretRunGrace is how long a command stopped at the run's deadline has between SIGTERM and SIGKILL (a variable so
// tests shorten it); secretRunSweep bounds the SIGKILLs after it (osproc.SweepGroup).
var secretRunGrace = 5 * time.Second

const secretRunSweep = 2 * time.Second

// secretRun answers secret.run. It zeroes params: their base64 is the password's, and nothing reads the request after.
func (n *Node) secretRun(ctx context.Context, params json.RawMessage) (any, error) {
	var p proto.SecretRunParams
	err := json.Unmarshal(params, &p)
	clear(params)
	defer clear(p.Password)
	if err != nil {
		return nil, errors.New("secret.run: bad params") // the decoder's error is not repeated: it may quote them
	}
	if secretRunGOOS == "windows" {
		return nil, errors.New("secret.run: this computer runs Windows, which has no sudo -S to give the password to")
	}
	if p.Check {
		return map[string]any{"ok": true}, nil
	}
	if err := checkSecretRun(p); err != nil {
		return nil, err
	}
	timeout := time.Duration(p.TimeoutMS) * time.Millisecond
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	results := make([]proto.SecretRunResult, 0, len(p.Commands))
	for _, c := range p.Commands {
		r, inTime := runSecretCommand(rctx, n.home, c, p.Password)
		if ctx.Err() != nil { // the caller went away (the connection ended): nobody is left to answer
			return nil, errors.New("secret.run: interrupted, the commands were stopped")
		}
		if !inTime { // it did not run, so it is left out like the ones after it (proto.SecretRunAnswer)
			break
		}
		results = append(results, r)
		if r.ExitCode != 0 || r.TimedOut {
			break
		}
	}
	return proto.SecretRunAnswer{Results: results}, nil
}

// checkSecretRun refuses a run outside the limits the tool keeps to as well.
func checkSecretRun(p proto.SecretRunParams) error {
	if len(p.Commands) == 0 || len(p.Commands) > proto.SecretRunMaxCommands {
		return fmt.Errorf("secret.run: 1 to %d commands, not %d", proto.SecretRunMaxCommands, len(p.Commands))
	}
	for i, c := range p.Commands {
		if why := proto.SecretRunCommandProblem(c); why != "" {
			return fmt.Errorf("secret.run: command %d: %s", i+1, why)
		}
	}
	if len(p.Password) == 0 || len(p.Password) > proto.SecretRunMaxPassword {
		return fmt.Errorf("secret.run: the password must be 1 to %d bytes", proto.SecretRunMaxPassword)
	}
	for b := p.Password; len(b) > 0; { // as bytes: a string of it could not be zeroed
		r, n := utf8.DecodeRune(b)
		if unicode.IsControl(r) { // sudo would read up to a line break, and the rest would be left on stdin
			return errors.New("secret.run: the password has a control character")
		}
		b = b[n:]
	}
	if p.TimeoutMS <= 0 || p.TimeoutMS > proto.SecretRunMaxTimeout.Milliseconds() { // in ms: a Duration product could overflow
		return fmt.Errorf("secret.run: timeout_ms must be more than 0 and at most %d", proto.SecretRunMaxTimeout.Milliseconds())
	}
	return nil
}

// runSecretCommand runs one command until it exits or ctx ends (endGroup), the password on its stdin when it runs
// sudo -S (proto.SecretRunSudo.GetsPassword). inTime is false when ctx's deadline had passed before the command could
// start: the commands before it used up the run's time, and nothing ran.
func runSecretCommand(ctx context.Context, home, command string, password []byte) (res proto.SecretRunResult, inTime bool) {
	res = proto.SecretRunResult{Command: command, ExitCode: -1}
	out := newSecretRedactor(password, secretRunKeep)
	defer out.zero()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = home
	cmd.Env = secretRunEnv()
	killGroupOnCancel(cmd) // its own session and group
	// When ctx ends the group gets SIGTERM: sudo passes it on to the command it runs as root, which this user cannot
	// signal (SIGKILL would end sudo alone). Wait gives it secretRunGrace, then endGroup kills what is left.
	var stopped atomic.Int64
	stop := func() error { // once: exec's Cancel and the watch after Start both see the deadline
		if !stopped.CompareAndSwap(0, time.Now().UnixNano()) {
			return nil
		}
		return osproc.StopGroup(cmd.Process.Pid, false)
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stop()
	}
	cmd.WaitDelay = secretRunGrace
	cmd.Stdout, cmd.Stderr = out, out
	var stdin io.WriteCloser
	var err error
	if proto.SecretRunSudoOf(command).GetsPassword() {
		stdin, err = cmd.StdinPipe()
	} // else cmd.Stdin stays nil: /dev/null
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) { // exec checks ctx before it starts anything
			return res, false
		}
		res.Output = "could not start: " + err.Error()
		return res, true
	}
	// exec calls Cancel only while the shell runs. A shell that exited and left a child holding its output has Wait
	// reading until that child ends (or WaitDelay after the shell's exit), past the deadline: its group is stopped at
	// the deadline all the same (#4363).
	waited := make(chan struct{})
	var watch sync.WaitGroup
	watch.Go(func() {
		select {
		case <-ctx.Done():
			_ = stop()
		case <-waited:
		}
	})
	if stdin != nil {
		// A small write fits the pipe's buffer whether or not the command reads it; one that exits first makes it
		// fail, which changes nothing.
		_, _ = stdin.Write(password)
		_, _ = stdin.Write([]byte{'\n'})
		_ = stdin.Close()
	}
	_ = cmd.Wait() // the exit code is the process state's; ErrWaitDelay (a child kept the output open) changes nothing
	close(waited)
	watch.Wait() // stopped is settled
	if at := stopped.Load(); at != 0 {
		endGroup(cmd.Process.Pid, time.Unix(0, at).Add(secretRunGrace))
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode() // -1: ended by a signal
	}
	if (res.ExitCode != 0 || stopped.Load() != 0) && errors.Is(ctx.Err(), context.DeadlineExceeded) { // stopped at the timeout, whatever it then exited with
		res.TimedOut, res.ExitCode = true, -1
	}
	out.release(true) // Wait read the output to its end
	res.Output, res.Truncated, res.OutputWithheld = out.result()
	return res, true
}

// endGroup ends what is left of process group pid, sent SIGTERM at the run's deadline: until its grace ends
// (secretRunGrace after the SIGTERM) the group may still exit on it; then it gets SIGKILL, and again until it is empty
// (osproc.SweepGroup: a child forked while the signal goes out survives one on macOS). A process sudo started as root
// is out of this user's reach and may outlive it, which the result says.
func endGroup(pid int, graceEnds time.Time) {
	for time.Now().Before(graceEnds) && osproc.Alive(-pid) { // -pid: anyone in the group
		time.Sleep(50 * time.Millisecond)
	}
	_ = osproc.StopGroup(pid, true)
	osproc.SweepGroup(pid, secretRunSweep)
}

// secretRunEnv is the node's environment with PATH proto.SecretRunPath.
func secretRunEnv() []string {
	env := []string{"PATH=" + proto.SecretRunPath}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return env
}

// secretRedactor is a command's stdout and stderr. It replaces every occurrence of the password with proto.Redacted
// as the output comes, holding back only the last len(password)-1 bytes (they may begin one the next write
// completes), and keeps the end of the masked output in kept: what it keeps never holds the password, wherever kept
// cuts it. exec writes to it from one goroutine at a time when it is both Stdout and Stderr.
type secretRedactor struct {
	password []byte
	held     []byte // read and not masked yet; never grows (cap secretRunChunk+len(password))
	kept     *secretOutputBuffer
	found    bool // the output held the password
}

func newSecretRedactor(password []byte, keep int) *secretRedactor {
	return &secretRedactor{password: password, held: make([]byte, 0, secretRunChunk+len(password)),
		kept: newSecretOutputBuffer(keep)}
}

func (r *secretRedactor) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		c := min(len(p), secretRunChunk)
		r.held = append(r.held, p[:c]...)
		p = p[c:]
		r.release(false)
	}
	return n, nil
}

// release masks what is held and passes it on to kept, but for the last len(password)-1 bytes unless end.
func (r *secretRedactor) release(end bool) {
	pos := 0
	for len(r.password) > 0 {
		i := bytes.Index(r.held[pos:], r.password)
		if i < 0 {
			break
		}
		_, _ = r.kept.Write(r.held[pos : pos+i])
		_, _ = r.kept.Write([]byte(proto.Redacted))
		pos += i + len(r.password)
		r.found = true
	}
	upto := len(r.held)
	if !end {
		upto = max(pos, len(r.held)-(len(r.password)-1))
	}
	_, _ = r.kept.Write(r.held[pos:upto])
	rest := copy(r.held, r.held[upto:])
	clear(r.held[rest:])
	r.held = r.held[:rest]
}

// result is the masked output's last secretRunClip bytes (from a character's start), and whether anything before them
// was left out; none of it (withheld) when the output held the password.
func (r *secretRedactor) result() (output string, truncated, withheld bool) {
	if r.found {
		return "", false, true
	}
	out, truncated := r.kept.tail()
	if len(out) > secretRunClip {
		out, truncated = out[len(out)-secretRunClip:], true
	}
	for len(out) > 0 && !utf8.RuneStart(out[0]) {
		out = out[1:]
	}
	return string(out), truncated, false
}

// zero clears what was held and kept: a command may have printed the password in another form.
func (r *secretRedactor) zero() {
	clear(r.held[:cap(r.held)])
	r.kept.zero()
}

// secretOutputBuffer keeps at least the last keep bytes written to it, in one buffer of twice that which never grows
// (zero clears all of it). What it keeps moves to the front only when the buffer is full: once per keep bytes written
// at most, so a command printing line by line costs no more than one that prints in large writes. exec writes to it
// from one goroutine at a time when it is both Stdout and Stderr.
type secretOutputBuffer struct {
	buf     []byte
	keep    int
	dropped bool // bytes before the ones the buffer holds were dropped
}

func newSecretOutputBuffer(keep int) *secretOutputBuffer {
	return &secretOutputBuffer{buf: make([]byte, 0, 2*keep), keep: keep}
}

func (b *secretOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= b.keep { // p's end alone is what is kept
		b.dropped = b.dropped || len(b.buf) > 0 || len(p) > b.keep
		p, b.buf = p[len(p)-b.keep:], b.buf[:0]
	} else if len(b.buf)+len(p) > cap(b.buf) {
		from := len(b.buf) - (b.keep - len(p))
		copy(b.buf, b.buf[from:])
		b.buf, b.dropped = b.buf[:b.keep-len(p)], true
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

// tail is the last keep bytes written, and whether anything before them was.
func (b *secretOutputBuffer) tail() ([]byte, bool) {
	if over := len(b.buf) - b.keep; over > 0 {
		return b.buf[over:], true
	}
	return b.buf, b.dropped
}

func (b *secretOutputBuffer) zero() { clear(b.buf[:cap(b.buf)]) }
