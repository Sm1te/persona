package node

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale/go-winio"
	"golang.org/x/sys/windows"

	"github.com/nwverse/persona/server/internal/proto"
)

// The Codex App's bus on Windows is the named pipe \\.\pipe\codex-ipc (codex_desktop_windows.go). Tests serve a fake
// one under a name of their own (codexDesktopPipe): none may reach the user's App.

var fakePipeSeq atomic.Int64

// fakeCodexPipeName is a pipe name no other test, nor the App, serves.
func fakeCodexPipeName() string {
	return fmt.Sprintf(`\\.\pipe\persona-test-codex-ipc-%d-%d`, os.Getpid(), fakePipeSeq.Add(1))
}

// useCodexPipe points the App bus at name for the test (and forgets whether the last one was up). The App is never
// asked to open a thread either: codex:// would reach the user's App.
func useCodexPipe(t *testing.T, name string) {
	t.Helper()
	old, open := codexDesktopPipe, openCodexThread
	codexDesktopPipe = name
	openCodexThread = func(string) error { return fmt.Errorf("tests do not open codex:// links") }
	resetCodexPipeSeen()
	t.Cleanup(func() { codexDesktopPipe, openCodexThread = old, open; resetCodexPipeSeen() })
}

func resetCodexPipeSeen() {
	codexPipeSeen.Lock()
	codexPipeSeen.name = ""
	codexPipeSeen.Unlock()
}

// listenFakeCodexBus serves a fake App bus where the App on Windows does: a named pipe, one per session — here one
// per test — with the App's home elsewhere. Its buffers are libuv's (the App is Node): with go-winio's default of none,
// a write waits for the other side to read, and the bus's question after initialize deadlocks with the next request.
func listenFakeCodexBus(t *testing.T) (home string, l net.Listener) {
	t.Helper()
	name := fakeCodexPipeName()
	l, err := winio.ListenPipe(name, &winio.PipeConfig{InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	useCodexPipe(t, name)
	return t.TempDir(), l
}

// noFakeCodexBus: the App bus is a pipe nobody serves.
func noFakeCodexBus(t *testing.T) { useCodexPipe(t, fakeCodexPipeName()) }

// The pipe is seen from the namespace's listing, without connecting: a check that opened it would take one of the
// App's instances (os.Stat does, through CreateFile).
func TestCodexPipeUpDoesNotConnect(t *testing.T) {
	name := fakeCodexPipeName()
	if codexPipeUp(name) {
		t.Fatal("a pipe nobody serves is up")
	}
	l, err := winio.ListenPipe(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
			accepted <- struct{}{}
		}
	}()
	time.Sleep(200 * time.Millisecond) // the listener waits for a client by now
	resetCodexPipeSeen()
	if !codexPipeUp(name) {
		t.Fatal("the pipe is not seen")
	}
	select {
	case <-accepted:
		t.Fatal("seeing the pipe connected to it")
	case <-time.After(500 * time.Millisecond):
	}
}

// The App's chat is sendable only while its bus is there and its home is the App's (the pipe does not say which home).
func TestCodexDesktopAvailableNeedsThePipeAndTheAppsHome(t *testing.T) {
	home, _ := startFakeCodexBus(t, "thread-a")
	if !codexDesktopAvailable(home) {
		t.Fatal("the App's home is not seen")
	}
	if codexDesktopAvailable(t.TempDir()) {
		t.Fatal("a home without the App's state is the App's")
	}
	noFakeCodexBus(t)
	if codexDesktopAvailable(home) {
		t.Fatal("no bus, yet the App is seen")
	}
}

// A thread the App has not loaded: the App is asked to open it; once the open fails (no codex:// handler), nothing
// is waited for and nothing is sent.
func TestSendCodexDesktopRefusesAtOnceWhenTheLinkCannotOpen(t *testing.T) {
	home, bus := startFakeCodexBus(t, "thread-a")
	defer func(f func(string) error) { openCodexThread = f }(openCodexThread)
	openCodexThread = func(string) error { return fmt.Errorf("codex:// is not registered") }
	n := &Node{dir: t.TempDir()}
	start := time.Now()
	err := n.sendCodexDesktop(context.Background(), codexSource{home: home, thread: "thread-b"}, "hello", nil)
	if code, data := refusalOf(t, err); code != "codex_desktop_not_loaded" || data["delivered"] != deliveredNone {
		t.Fatalf("refusal = %s %v", code, data)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("waited %s for an App that was never asked", time.Since(start))
	}
	select {
	case p := <-bus.turns:
		t.Fatalf("a turn was started: %v", p)
	default:
	}
}

// An image goes to the App as a local file the node saved, next to the text.
func TestSendCodexDesktopSendsImagesAsLocalFiles(t *testing.T) {
	home, bus := startFakeCodexBus(t, "thread-a")
	n := &Node{dir: t.TempDir()}
	img := proto.Image{Mime: "image/png", Data: "iVBORw0KGgo="}
	if err := n.sendCodexDesktop(context.Background(), codexSource{home: home, thread: "thread-a"}, "look", []proto.Image{img}); err != nil {
		t.Fatal(err)
	}
	p := <-bus.turns
	in := p["turnStart"].(map[string]any)["request"].(map[string]any)["input"].([]any)
	if len(in) != 2 || in[1].(map[string]any)["type"] != "localImage" {
		t.Fatalf("input = %v", in)
	}
	path, _ := in[1].(map[string]any)["path"].(string)
	if !strings.HasPrefix(path, n.dir) || !strings.HasSuffix(path, ".png") {
		t.Fatalf("image path %q not under %s", path, n.dir)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// The App's chat lists with the App's sidebar on Windows paths too: a project's root and a thread's directory in
// another case are the same place.
func TestCodexDesktopProjectRootIgnoresCase(t *testing.T) {
	a := &codexAppState{projects: map[string]codexAppProject{"p1": {Name: "Alpha", RootPaths: []string{`C:\Work\Alpha`}}}}
	if id, name := a.project("t", `c:\work\alpha\sub`); id != "p1" || name != "Alpha" {
		t.Fatalf("project = %q %q", id, name)
	}
	if id, _ := a.project("t", `C:\Work\AlphaBeta`); id != "" {
		t.Fatalf("a sibling with the same prefix is in the project: %q", id)
	}
}

// TestCodexHolderHelper is the child the open-rollout scan reads: it holds a file open (as Codex holds its rollout)
// until killed.
func TestCodexHolderHelper(t *testing.T) {
	path := os.Getenv("PERSONA_CODEX_HOLD")
	if path == "" {
		t.Skip("helper process only")
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		os.Exit(3)
	}
	defer f.Close()
	os.WriteFile(path+".ready", nil, 0o600)
	time.Sleep(time.Minute)
}

// holdOpen starts a child that holds path open and waits until it does.
func holdOpen(t *testing.T, path string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexHolderHelper$")
	cmd.Env = append(os.Environ(), "PERSONA_CODEX_HOLD="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(path + ".ready"); err == nil {
			return cmd
		}
		if time.Now().After(deadline) {
			t.Fatal("the holder did not open the file")
		}
	}
}

// The rollouts a process holds open come from its handle table: what another process holds is seen (with its path
// as Windows resolves it), a file it does not hold is not, and a process that has gone is no failure.
func TestScanOpenRolloutsReadsTheHandleTable(t *testing.T) {
	dir := t.TempDir()
	held := filepath.Join(dir, "rollout-2026-11-07T10-00-00-0e000000-0000-4000-8000-000000000001.jsonl")
	other := filepath.Join(dir, "rollout-2026-11-07T10-00-00-0e000000-0000-4000-8000-000000000002.jsonl")
	for _, p := range []string{held, other} {
		if err := os.WriteFile(p, []byte(`{"type":"session_meta","payload":{}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := holdOpen(t, held)
	open, ok := scanOpenRollouts([]codexProcess{{pid: cmd.Process.Pid}})
	if !ok {
		t.Fatal("the holder could not be read")
	}
	paths := open[cmd.Process.Pid]
	if len(paths) != 1 || !sameDir(paths[0], held) || filepath.Base(paths[0]) != filepath.Base(held) {
		t.Fatalf("held open: %v, want %s", paths, held)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	open, ok = scanOpenRollouts([]codexProcess{{pid: cmd.Process.Pid}})
	if !ok || len(open) != 0 {
		t.Fatalf("after the holder exited: %v %v", open, ok)
	}
}

// A Codex CLI running in a plain terminal holds its rollout open: the node sees it as running, so it is not offered
// for a resume (a second `codex resume` would write the same thread), and its state comes from the rollout.
func TestCodexHeldByARunningCLIIsNotResumable(t *testing.T) {
	n, _, _ := codexHistoryNode(t)
	path := rolloutPath(n.codexHome(), startThread)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(event("task_started") + "\n")
	f.Close()
	cmd := holdOpen(t, path)
	n.codices.scan = func() []codexProcess { return []codexProcess{{pid: cmd.Process.Pid}} } // a CLI: no endpoint
	n.codices.checked = func() ([]codexProcess, bool) { return n.codices.scan(), true }
	n.discoverCodex()
	s := byID(listSessions(t, n))[startThread]
	if s.Capabilities == nil || s.Capabilities.Send || s.Status != proto.StatusWorking || !strings.HasPrefix(s.ReadOnlyReason, codexHolderCLI) {
		t.Fatalf("running CLI's thread: status %q reason %q caps %+v", s.Status, s.ReadOnlyReason, s.Capabilities)
	}
	if n.codexResumable(t.Context(), startThread) {
		t.Fatal("resumable while a Codex holds its rollout")
	}
	// The list's scan may be up to 30 seconds old: a resume looks again.
	n.codices.mu.Lock()
	n.codices.liveRollouts = nil
	n.codices.mu.Unlock()
	if !n.codexHeldNow(path) {
		t.Fatal("a resume does not see the CLI holding the rollout")
	}
}

// The process scan finds codex.exe, reads CODEX_HOME and the herdr pane from its environment, and app-server's
// --listen from its command line. A child named codex.exe (this test binary copied) plays it.
func TestScanCodexProcessesOSReadsCodexExe(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "codex.exe")
	b, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, b, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "codex home")
	cmd := exec.Command(exe, "-test.run=^TestProcHelper$", "app-server", "--listen", "ws://127.0.0.1:4500")
	cmd.Env = append(os.Environ(), "PERSONA_PROC_HELPER=1", "CODEX_HOME="+home, "HERDR_PANE_ID=w1:p1", "OPENAI_API_KEY=never-kept")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var got *codexProcess
	for deadline := time.Now().Add(10 * time.Second); got == nil && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		procs, ok := scanCodexProcessesOS()
		if !ok {
			t.Fatal("the scan could not read a codex.exe of this session")
		}
		if i := slices.IndexFunc(procs, func(p codexProcess) bool { return p.pid == cmd.Process.Pid && p.home != "" }); i >= 0 {
			got = &procs[i]
		}
	}
	if got == nil {
		t.Fatal("codex.exe not found")
	}
	if got.home != home || got.pane != "w1:p1" || got.endpoint != "ws://127.0.0.1:4500" {
		t.Fatalf("got %+v", *got)
	}
}

// A control socket Codex serves on Windows is an AF_UNIX file: private when its owner is this user and its access
// list lets nobody else in; a socket others may use is refused.
func TestPrivateSocketOnWindows(t *testing.T) {
	dir, err := os.MkdirTemp("", "cxs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := restrictToMe(dir); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := privateSocket(socket); err != nil {
		t.Fatalf("a socket only this user may use: %v", err)
	}
	if err := grantEveryone(socket); err != nil {
		t.Fatal(err)
	}
	if privateSocket(socket) == nil {
		t.Fatal("a socket everyone may use is private")
	}
	if _, _, err := validateCodexEndpoint("unix://" + socket); err == nil {
		t.Fatal("an endpoint everyone may use is accepted")
	}
}

// A drive path is a Unix endpoint, not a URL with a host and a port.
func TestValidateCodexEndpointTakesADrivePath(t *testing.T) {
	dir, err := os.MkdirTemp("", "cxs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if err := restrictToMe(dir); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, _, err := validateCodexEndpoint("unix://" + socket); err != nil {
		t.Fatalf("unix://%s: %v", socket, err)
	}
	if _, _, err := validateCodexEndpoint("unix://" + socket + "?x=1"); err == nil {
		t.Fatal("a query is accepted")
	}
}

// restrictToMe gives path an access list of this user alone (as Codex makes its control directory).
func restrictToMe(path string) error {
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(me.User.Sid)},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// grantEveryone adds read and write for Everyone to path's access list.
func grantEveryone(path string) error {
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, _ := sd.DACL()
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ | windows.GENERIC_WRITE, AccessMode: windows.GRANT_ACCESS, Inheritance: windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_WELL_KNOWN_GROUP, TrusteeValue: windows.TrusteeValueFromSID(everyone)},
	}}, old)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// npm's codex.cmd is never run with a message (cmd.exe would read it as commands): codex-start runs the codex.exe it
// starts, with the variables its codex.js sets. Another batch file is refused with a message and run without one.
func TestCodexStartProgramSkipsNpmsBatchShim(t *testing.T) {
	prefix := t.TempDir()
	shim := filepath.Join(prefix, "codex.cmd")
	pkg := filepath.Join(prefix, "node_modules", "@openai", "codex")
	exe := filepath.Join(pkg, "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	for _, p := range []string{shim, filepath.Join(pkg, "bin", "codex.js"), exe} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path, env, err := codexStartProgram(shim, true, []string{"A=1", "CODEX_MANAGED_BY_BUN=1"})
	if err != nil || path != exe {
		t.Fatalf("ran %q (%v), want %q", path, err, exe)
	}
	if !slices.Contains(env, "A=1") || slices.Contains(env, "CODEX_MANAGED_BY_BUN=1") || !slices.Contains(env, "CODEX_MANAGED_BY_NPM=1") ||
		!slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "CODEX_MANAGED_PACKAGE_ROOT=") }) {
		t.Fatalf("env = %v", env)
	}
	other := filepath.Join(t.TempDir(), "cx.bat")
	if err := os.WriteFile(other, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := codexStartProgram(other, true, nil); err == nil {
		t.Fatal("a batch file was given the message")
	}
	if path, _, err := codexStartProgram(other, false, nil); err != nil || path != other {
		t.Fatalf("without a message: %q %v", path, err)
	}
	if path, _, err := codexStartProgram(exe, true, nil); err != nil || path != exe {
		t.Fatalf("an exe: %q %v", path, err)
	}
}
