package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/runtimepkg"
)

// The Paseo engine (docs/integrations/paseo.md). Paseo is a daemon running coding agents through their SDKs, with no terminal:
// the node reaches it through the Paseo bridge (integrations/paseo), a runtime (runtimes.go) that speaks Paseo's
// WebSocket protocol on one side and the runtime protocol on the other. The node carries the bridge (runtimepkg) and
// runs it itself while the machine's engine is Paseo — nothing to install: it writes the bridge out beside the
// runtime socket and runs it with a JavaScript runtime found on the machine (Paseo's own when it can), again when it
// exits, until the engine changes. The bridge dials the runtime socket like any runtime.

// paseoPackage is the bridge's package (runtimepkg.Packages).
const paseoPackage = "persona-paseo-runtime"

// paseoRuntimeID is the runtime id the bridge says hello with (the engine's sessions' space kind).
const paseoRuntimeID = proto.EnginePaseo

// paseoBridgeRestart bounds the wait before the bridge is run again after it exited: from paseoBridgeRestartMin,
// doubling, back to the minimum after it ran for paseoBridgeSteady. Tests shorten them.
var (
	paseoBridgeRestartMin = time.Second
	paseoBridgeRestartMax = time.Minute
	paseoBridgeSteady     = 5 * time.Minute
	paseoBridgeStopWait   = 3 * time.Second
)

// paseoHome is the Paseo home: PASEO_HOME, else ~/.paseo (as Paseo's own CLI).
func paseoHome() string {
	if h := strings.TrimSpace(os.Getenv("PASEO_HOME")); h != "" {
		return proto.ExpandHome(h, userHome())
	}
	return filepath.Join(userHome(), ".paseo")
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// paseoPidFile is what a running Paseo daemon writes in its home: its process and the address it listens on.
type paseoPidFile struct {
	PID    int    `json:"pid"`
	Listen string `json:"listen"`
}

// readPaseoPid reads home's paseo.pid (false when Paseo is not running there).
func readPaseoPid(home string) (paseoPidFile, bool) {
	var p paseoPidFile
	b, err := os.ReadFile(filepath.Join(home, "paseo.pid"))
	if err != nil || json.Unmarshal(b, &p) != nil {
		return paseoPidFile{}, false
	}
	return p, true
}

// paseoReachable: the Paseo daemon of home answers at the address its paseo.pid gives (a TCP or unix connection
// opens). Only there, as Paseo's own CLI: a default port may be another home's daemon.
func paseoReachable(ctx context.Context, home string) bool {
	p, ok := readPaseoPid(home)
	if !ok || p.Listen == "" {
		return false
	}
	listen := p.Listen
	network, addr := "tcp", listen
	if rest, ok := strings.CutPrefix(listen, "unix:"); ok {
		network, addr = "unix", strings.TrimPrefix(rest, "//")
	} else if host, port, err := net.SplitHostPort(listen); err == nil {
		if host == "0.0.0.0" || host == "" {
			host = "127.0.0.1"
		} else if host == "::" {
			host = "::1"
		}
		addr = net.JoinHostPort(host, port)
	}
	d := net.Dialer{Timeout: time.Second}
	c, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// jsRuntime is a JavaScript runtime to run the bridge with: node, or an Electron app's binary run as node.
type jsRuntime struct {
	Path     string
	Electron bool // ELECTRON_RUN_AS_NODE=1: Paseo's desktop app runs its daemon this way
}

// minNodeMajor is the oldest Node.js the bridge runs on.
const minNodeMajor = 22

// findJSRuntime finds what runs the bridge: PERSONA_NODEJS when set; the runtime the Paseo daemon itself runs on (its
// process in paseo.pid: the CLI's node, or the desktop app's binary); else a node on PATH or where installers put it
// (a service's PATH is short). Each is asked its version; too old ones are skipped.
func findJSRuntime(ctx context.Context, home string) (jsRuntime, error) {
	var cands []jsRuntime
	if p := strings.TrimSpace(os.Getenv("PERSONA_NODEJS")); p != "" {
		cands = append(cands, jsRuntime{Path: p, Electron: !isNodeBinary(p)})
	}
	if p, ok := readPaseoPid(home); ok && p.PID > 0 {
		if exe := processExecutable(ctx, p.PID); exe != "" {
			cands = append(cands, jsRuntime{Path: exe, Electron: !isNodeBinary(exe)})
		}
	}
	if p, err := exec.LookPath("node"); err == nil {
		cands = append(cands, jsRuntime{Path: p})
	}
	for _, p := range commonNodePaths() {
		cands = append(cands, jsRuntime{Path: p})
	}
	seen := map[string]bool{}
	var tried []string
	for _, c := range cands {
		if seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		if st, err := os.Stat(c.Path); err != nil || st.IsDir() {
			continue
		}
		v, err := jsVersion(ctx, c)
		if err != nil {
			tried = append(tried, c.Path+": "+err.Error())
			continue
		}
		if major, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)[0]); major < minNodeMajor {
			tried = append(tried, fmt.Sprintf("%s: Node.js %s is older than %d", c.Path, v, minNodeMajor))
			continue
		}
		return c, nil
	}
	if len(tried) > 0 {
		return jsRuntime{}, fmt.Errorf("no usable Node.js (%s)", strings.Join(tried, "; "))
	}
	return jsRuntime{}, errors.New("no Node.js found (set PERSONA_NODEJS)")
}

// isNodeBinary: the file is a node (by name), not an app binary that runs as node only with ELECTRON_RUN_AS_NODE.
func isNodeBinary(path string) bool {
	b := strings.ToLower(filepath.Base(path))
	return b == "node" || b == "node.exe" || strings.HasPrefix(b, "node-") || strings.HasPrefix(b, "nodejs")
}

// commonNodePaths are where Node.js installers put node, newest nvm version first.
func commonNodePaths() []string {
	home := userHome()
	out := []string{"/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node",
		filepath.Join(home, ".volta", "bin", "node"), filepath.Join(home, ".local", "bin", "node")}
	nvm, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "node"))
	sort.Sort(sort.Reverse(sort.StringSlice(nvm)))
	return append(out, nvm...)
}

// jsVersion is the Node.js version a runtime reports.
func jsVersion(ctx context.Context, js jsRuntime) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, js.Path, "-e", "process.stdout.write(process.versions.node)")
	cmd.Env = js.env(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", errors.New("no version")
	}
	return v, nil
}

// env is base to run js with, in a new slice: ELECTRON_RUN_AS_NODE=1 for an app binary, and never for a node. A node
// started from an Electron host (an editor's extension host starts its children with it) inherits the variable; it
// is dropped, or everything the bridge starts would get it too, and an Electron app started from there would run as
// a bare Node.js.
func (js jsRuntime) env(base []string) []string {
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if !strings.HasPrefix(kv, "ELECTRON_RUN_AS_NODE=") {
			out = append(out, kv)
		}
	}
	if js.Electron {
		out = append(out, "ELECTRON_RUN_AS_NODE=1")
	}
	return out
}

// processExecutable is the executable a process of this user runs ("" when it cannot be told).
func processExecutable(ctx context.Context, pid int) string {
	switch runtime.GOOS {
	case "linux":
		p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(p, " (deleted)")
	case "darwin":
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			return ""
		}
		p := strings.TrimSpace(string(out))
		if !filepath.IsAbs(p) {
			return ""
		}
		return p
	}
	return ""
}

// writePackage writes a package's files out under dir/<hash> once (a version written before is reused) and returns
// that directory. Written beside and renamed in, so a half-written version is never run.
func writePackage(dir string, pkg *runtimepkg.Package) (string, error) {
	final := filepath.Join(dir, pkg.Hash)
	if st, err := os.Stat(final); err == nil && st.IsDir() {
		return final, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(dir, ".write-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, f := range pkg.Files {
		if !runtimepkg.ValidPath(f.Path) {
			return "", fmt.Errorf("bad path %q in package %s", f.Path, pkg.Name)
		}
		p := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(p, f.Data, 0o600); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, final); err != nil && !dirExists(final) {
		return "", err
	}
	// Older versions go; the one written now stays.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && e.Name() != pkg.Hash && !strings.HasPrefix(e.Name(), ".") {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	return final, nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// paseoBridge runs the bridge for a runtime host while the engine wants it.
type paseoBridge struct {
	mu     sync.Mutex
	cancel context.CancelFunc // ends the running loop; nil while it does not run
	done   chan struct{}
	status string // what the last run said went wrong, for node.info ("" while it runs fine)
}

// setPaseoBridge runs the bridge (on) or stops it (off) for this runtime host. Called whenever a hub connection says
// which engine the machine uses.
func (rh *RuntimeHost) setPaseoBridge(on bool) {
	b := &rh.paseo
	b.mu.Lock()
	defer b.mu.Unlock()
	if on == (b.cancel != nil) {
		return
	}
	if !on {
		b.cancel()
		b.cancel = nil
		done := b.done
		b.mu.Unlock()
		<-done
		b.mu.Lock()
		rh.logger.Printf("paseo bridge: stopped (the engine is not Paseo any more)")
		return
	}
	base := rh.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	b.cancel, b.done, b.status = cancel, make(chan struct{}), ""
	go rh.paseoBridgeLoop(ctx, b.done)
}

// paseoBridgeStatus says why the bridge is not running when it is wanted ("" otherwise).
func (rh *RuntimeHost) paseoBridgeStatus() string {
	rh.paseo.mu.Lock()
	defer rh.paseo.mu.Unlock()
	if rh.paseo.cancel == nil {
		return ""
	}
	return rh.paseo.status
}

func (rh *RuntimeHost) setPaseoStatus(s string) {
	rh.paseo.mu.Lock()
	rh.paseo.status = s
	rh.paseo.mu.Unlock()
}

// paseoBridgeLoop runs the bridge until ctx ends, again after it exits (backing off).
func (rh *RuntimeHost) paseoBridgeLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	wait := paseoBridgeRestartMin
	for ctx.Err() == nil {
		started := time.Now()
		err := rh.runPaseoBridge(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > paseoBridgeSteady {
			wait = paseoBridgeRestartMin
		}
		if err != nil {
			rh.setPaseoStatus(err.Error())
			rh.logger.Printf("paseo bridge: %v (again in %s)", err, wait)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, paseoBridgeRestartMax)
	}
}

// paseoBridgeCommand is how the bridge is run (tests replace it with a fake that need not be JavaScript).
var paseoBridgeCommand = func(ctx context.Context, rh *RuntimeHost) (*exec.Cmd, error) {
	pkg, err := runtimepkg.Load(paseoPackage)
	if err != nil {
		return nil, err
	}
	if pkg == nil {
		return nil, errors.New("this personad carries no Paseo bridge (built without go generate ./internal/runtimepkg)")
	}
	dir, err := writePackage(filepath.Join(filepath.Dir(rh.path), "runtime-packages", paseoPackage), pkg)
	if err != nil {
		return nil, fmt.Errorf("write the bridge: %w", err)
	}
	home := paseoHome()
	js, err := findJSRuntime(ctx, home)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(js.Path, paseoBridgeArgs(dir, rh.path, home)...)
	cmd.Env = js.env(append(os.Environ(), "PERSONA_RUNTIME_SOCKET="+rh.path))
	cmd.Dir = dir
	return cmd, nil
}

// paseoBridgeArgs is the bridge's command line after the JavaScript runtime. The node's mcp.json (beside the runtime
// socket) is what the bridge gives each new Agent as its Persona tools, and of those the Agent's own run without a card
// (--allow-tools: agentAllowedTools without the office rule, which the bridge's Agents do not get).
func paseoBridgeArgs(dir, socket, home string) []string {
	return []string{filepath.Join(dir, "paseo", "bridge.mjs"), "--socket", socket, "--paseo-home", home,
		"--mcp-config", filepath.Join(filepath.Dir(socket), "mcp.json"),
		"--allow-tools", strings.Join(compat.MCPAllowedToolNames(assistantwire.AgentOwnTools, true), ",")}
}

// runPaseoBridge runs the bridge once, until it exits or ctx ends (then it is stopped: SIGTERM to its group, SIGKILL
// after paseoBridgeStopWait). Its output goes to the log, line by line.
func (rh *RuntimeHost) runPaseoBridge(ctx context.Context) error {
	cmd, err := paseoBridgeCommand(ctx, rh)
	if err != nil {
		return err
	}
	cmd.SysProcAttr = osproc.GroupAttr()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	// Held open while the bridge runs: it exits when this ends, so a node that dies (killed, no time to stop its
	// group) leaves no bridge behind holding the runtime id against the next node's.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	rh.setPaseoStatus("")
	rh.logger.Printf("paseo bridge: running (pid %d)", cmd.Process.Pid)
	logged := make(chan struct{})
	go func() {
		defer close(logged)
		logLines(out, func(line string) { rh.logger.Printf("paseo bridge: %s", line) })
	}()
	exited := make(chan error, 1)
	go func() {
		<-logged
		exited <- cmd.Wait()
	}()
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("exited: %w", err)
		}
		return errors.New("exited")
	case <-ctx.Done():
		_ = osproc.StopGroup(cmd.Process.Pid, false)
		select {
		case <-exited:
		case <-time.After(paseoBridgeStopWait):
			_ = osproc.StopGroup(cmd.Process.Pid, true)
			<-exited
		}
		return ctx.Err()
	}
}

// logLines hands each line of r to f (long lines cut), until r ends. A line longer than the buffer is cut too and
// its rest dropped, and the lines after it still come: nothing else reads r, and a program whose log nobody reads
// is stuck on its next write once the pipe is full (#3959).
func logLines(r io.Reader, f func(string)) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		b, err := br.ReadSlice('\n')
		line := strings.TrimRight(string(b), "\r\n") // copied: the next read reuses b
		long := errors.Is(err, bufio.ErrBufferFull)
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = br.ReadSlice('\n')
		}
		if len(b) > 0 {
			if long || len(line) > 2000 {
				line = line[:min(len(line), 2000)] + "…"
			}
			f(line)
		}
		if err != nil {
			return
		}
	}
}

// paseoEngine is the Paseo engine as found on this machine for node.info: available when the bridge is connected or
// the Paseo daemon answers; its version is Paseo's (what the bridge said in its hello); note says why the bridge
// cannot run when it should.
func (n *Node) paseoEngine(ctx context.Context) proto.Engine {
	e := proto.Engine{ID: proto.EnginePaseo, Name: proto.EngineNames[proto.EnginePaseo]}
	var h *runtimeConn
	if n.runtimes != nil {
		n.runtimes.mu.Lock()
		h = n.runtimes.byRuntime(paseoRuntimeID)
		n.runtimes.mu.Unlock()
	}
	if h != nil {
		h.mu.Lock()
		e.Version = h.hello.Version
		h.mu.Unlock()
		e.Available = true
		return e
	}
	e.Available = paseoReachable(ctx, paseoHome())
	if n.runtimeHost != nil && e.Available {
		if s := n.runtimeHost.paseoBridgeStatus(); s != "" {
			e.Note = "Paseo 在运行，但 Persona 连不上它：" + s
		}
	}
	return e
}
