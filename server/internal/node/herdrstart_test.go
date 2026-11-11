package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The package's tests never start a real herdr: a node whose (fake) herdr is down would otherwise run `herdr server`
// on the machine running the tests. Tests of starting replace it (withLaunchHerdr).
func init() {
	launchHerdr = func(context.Context, string, string) (<-chan error, error) {
		return nil, errors.New("tests do not start a real herdr")
	}
	herdr.ReportSeq = func() int64 { return 7 } // traces compare reports whole
}

// fakeLaunch counts starts of herdr's server and, unless fail says why not, serves its socket like a started one.
type fakeLaunch struct {
	t      *testing.T
	starts atomic.Int32
	delay  time.Duration
	fail   error // the server exits with it
	bins   []string
	mu     sync.Mutex
}

func withLaunchHerdr(t *testing.T, fl *fakeLaunch) {
	t.Helper()
	fl.t = t
	prevLaunch, prevFind, prevWait := launchHerdr, findHerdr, herdrReadyWait
	launchHerdr = fl.launch
	findHerdr = func(context.Context, string) string { return "/opt/test/bin/herdr" }
	herdrReadyWait = 3 * time.Second
	t.Cleanup(func() { launchHerdr, findHerdr, herdrReadyWait = prevLaunch, prevFind, prevWait })
}

func (fl *fakeLaunch) launch(_ context.Context, bin, sock string) (<-chan error, error) {
	fl.starts.Add(1)
	fl.mu.Lock()
	fl.bins = append(fl.bins, bin)
	fl.mu.Unlock()
	exited := make(chan error, 1)
	if fl.fail != nil {
		exited <- fl.fail
		return exited, nil
	}
	go func() {
		time.Sleep(fl.delay) // a server takes a moment to listen: concurrent callers find it down meanwhile
		serveSocket(fl.t, sock)
	}()
	return exited, nil
}

// serveSocket accepts (and closes) connections on sock until the test ends.
func serveSocket(t *testing.T, sock string) {
	ln, err := herdr.Listen(sock)
	if err != nil {
		t.Errorf("listen %s: %v", sock, err)
		return
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
}

// downHerdrNode is a node on herdr whose socket nothing serves (herdr not running), under /tmp (socket path limit).
func downHerdrNode(t *testing.T) (*Node, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "herdr", "herdr.sock")
	n, _ := testNode(t)
	n.hosts = herdrHosts(&herdr.Client{Socket: sock})
	return n, sock
}

// Down, installed and allowed: the server is started once, from the login shell's herdr, and the call returns once
// its socket answers — eight requests at once (a burst from the phone) still start one server.
func TestEnsureHerdrStartsOneServer(t *testing.T) {
	n, sock := downHerdrNode(t)
	fl := &fakeLaunch{delay: 150 * time.Millisecond}
	withLaunchHerdr(t, fl)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = n.ensureHerdr(context.Background(), "test")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
	if got := fl.starts.Load(); got != 1 {
		t.Fatalf("%d servers started, want 1", got)
	}
	if fl.bins[0] != "/opt/test/bin/herdr" || !herdrAlive(context.Background(), sock) {
		t.Fatalf("started %v; alive %v", fl.bins, herdrAlive(context.Background(), sock))
	}
	// Running now: the next request does not start another.
	if err := n.ensureHerdr(context.Background(), "test"); err != nil || fl.starts.Load() != 1 {
		t.Fatalf("again: %v, %d starts", err, fl.starts.Load())
	}
}

// A herdr that answers is left alone; so is a machine whose engine is not classic herdr (Herdr App's herdr-core has its
// own lifecycle), and one where the user turned starting off — then a `herdr server stop` stays stopped.
func TestEnsureHerdrLeavesItAlone(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		n, sock := downHerdrNode(t)
		fl := &fakeLaunch{}
		withLaunchHerdr(t, fl)
		_ = os.MkdirAll(filepath.Dir(sock), 0o700)
		serveSocket(t, sock)
		if err := n.ensureHerdr(context.Background(), "test"); err != nil || fl.starts.Load() != 0 {
			t.Fatalf("%v, %d starts", err, fl.starts.Load())
		}
	})
	for _, engine := range []string{proto.EngineHerdrCore, proto.EngineOrca, proto.EnginePaseo, proto.EngineCindy} {
		t.Run(engine, func(t *testing.T) {
			n, _ := downHerdrNode(t)
			n.eng.name = engine
			fl := &fakeLaunch{}
			withLaunchHerdr(t, fl)
			if err := n.ensureHerdr(context.Background(), "test"); err != nil || fl.starts.Load() != 0 {
				t.Fatalf("%v, %d starts", err, fl.starts.Load())
			}
		})
	}
	t.Run("off", func(t *testing.T) {
		n, _ := downHerdrNode(t)
		fl := &fakeLaunch{}
		withLaunchHerdr(t, fl)
		if _, err := request(t, n, "node.configure", map[string]any{"herdr_autostart": false}); err != nil {
			t.Fatal(err)
		}
		if err := n.ensureHerdr(context.Background(), "test"); err != nil || fl.starts.Load() != 0 {
			t.Fatalf("%v, %d starts", err, fl.starts.Load())
		}
		// Turned on again (and a node.configure that does not mention it leaves it as it is).
		_, _ = request(t, n, "node.configure", map[string]any{"herdr_autostart": true})
		_, _ = request(t, n, "node.configure", map[string]any{"botmux": false})
		if err := n.ensureHerdr(context.Background(), "test"); err != nil || fl.starts.Load() != 1 {
			t.Fatalf("on again: %v, %d starts", err, fl.starts.Load())
		}
	})
}

// herdr.server.status is what the hub asks when an app reads the Agents list's herdr row (#1916): it follows the
// socket, not what node.info said when the node connected.
func TestHerdrServerStatus(t *testing.T) {
	n, sock := downHerdrNode(t)
	status := func() map[string]any {
		t.Helper()
		out, err := request(t, n, "herdr.server.status", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := status(); got["running"] != false || got["known"] != true {
		t.Fatalf("down: %v", got)
	}
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	serveSocket(t, sock)
	if got := status(); got["running"] != true || got["known"] != true {
		t.Fatalf("up: %v", got)
	}
	n.hosts = herdrHosts(nil) // Herdr App's herdr-core: no classic socket, nothing claimed
	if got := status(); got["known"] != false {
		t.Fatalf("no classic herdr: %v", got)
	}
}

// herdr + Herdr App: the classic herdr half is started like herdr alone.
func TestEnsureHerdrStartsTheCombinedEnginesHerdr(t *testing.T) {
	n, _ := downHerdrNode(t)
	n.eng.name = proto.EngineHerdrBoth
	fl := &fakeLaunch{}
	withLaunchHerdr(t, fl)
	if err := n.ensureHerdr(context.Background(), "test"); err != nil || fl.starts.Load() != 1 {
		t.Fatalf("%v, %d starts", err, fl.starts.Load())
	}
}

// Not installed: nothing started, and the start is refused as before (engine_missing, the install hint).
func TestEnsureHerdrNotInstalled(t *testing.T) {
	n, _ := downHerdrNode(t)
	fl := &fakeLaunch{}
	withLaunchHerdr(t, fl)
	findHerdr = func(context.Context, string) string { return "" }
	if err := n.ensureHerdr(context.Background(), "test"); !errors.Is(err, errHerdrNotInstalled) || fl.starts.Load() != 0 {
		t.Fatalf("%v, %d starts", err, fl.starts.Load())
	}
	_, err := request(t, n, "session.start", map[string]any{"cwd": n.home})
	if !isRefusal(err, engineMissing) || !strings.Contains(err.Error(), "没装") {
		t.Fatalf("err %v", err)
	}
}

// A server that exits right after starting: the start is refused in the app's words (engine_missing, said that it
// did not come up), with why in the detail, and nothing created.
func TestStartWhenHerdrWillNotStart(t *testing.T) {
	n, _ := downHerdrNode(t)
	fl := &fakeLaunch{fail: errors.New("exit status 1: config.toml: parse error")}
	withLaunchHerdr(t, fl)
	_, err := request(t, n, "session.start", map[string]any{"cwd": n.home})
	if !isRefusal(err, engineMissing) || !strings.Contains(err.Error(), "自动启动也没成功") {
		t.Fatalf("err %v", err)
	}
	if fl.starts.Load() != 1 {
		t.Fatalf("%d starts", fl.starts.Load())
	}
	if d := refusalData(t, err); !strings.Contains(d, "parse error") {
		t.Fatalf("detail %s", d)
	}
}

// refusalData is a refusal's data as JSON.
func refusalData(t *testing.T, err error) string {
	t.Helper()
	var re *rpc.Error
	if !errors.As(err, &re) {
		t.Fatalf("not a refusal: %v", err)
	}
	return string(re.Data)
}

// A start finds herdr down: the node starts it, then goes on to make the workspace in it — also a start into a
// workspace it names (a task's worktree, #4754), which goes straight to the tab.
func TestStartStartsHerdrFirst(t *testing.T) {
	for _, ws := range []string{"", "w1"} {
		n, f, home := herdrNode(t)
		real := f.sock
		dir, err := os.MkdirTemp("/tmp", "hs")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		down := filepath.Join(dir, "herdr.sock")
		h := n.hosts.Get(host.Herdr).(*herdrHost)
		h.c.Socket = down // herdr is not running…
		fl := &fakeLaunch{}
		withLaunchHerdr(t, fl)
		launchHerdr = func(_ context.Context, _, sock string) (<-chan error, error) { // …until it is started: the fake
			fl.starts.Add(1)
			return make(chan error), os.Symlink(real, sock)
		}
		f.reply("workspace.list", map[string]any{"workspaces": []any{}})
		f.reply("workspace.create", herdrFail{Code: "internal", Message: "stop here"})
		f.reply("tab.create", herdrFail{Code: "internal", Message: "stop here"})
		began := time.Now()
		params := map[string]any{"cwd": home}
		next := "workspace.list"
		if ws != "" {
			params["workspace_id"], next = ws, "tab.create"
		}
		_, _ = request(t, n, "session.start", params)
		if fl.starts.Load() != 1 || len(f.trace(next)) != 1 || time.Since(began) > 2*time.Second {
			t.Fatalf("workspace %q: %d starts in %s; calls %v", ws, fl.starts.Load(), time.Since(began), f.trace())
		}
	}
}

// The server's environment is the user's login shell's, not the node's: a launcher's ANTHROPIC_*, proxies, the
// node's PATH and herdr's own pane variables (a node started from a herdr pane) never reach it — the variables that
// say who and where the user is, and where herdr's socket and config file are, do (the config file the server reads is
// the one herdr.WorktreesDir reads).
func TestHerdrServerEnvIsClean(t *testing.T) {
	environ := []string{"HOME=/Users/me", "USER=me", "LOGNAME=me", "TMPDIR=/tmp/me/", "LANG=zh_CN.UTF-8",
		"XDG_CONFIG_HOME=/Users/me/.config", "HERDR_SOCKET_PATH=/Users/me/.config/herdr/herdr.sock",
		"HERDR_CONFIG_PATH=/Users/me/herdr.toml", "ANTHROPIC_API_KEY=sk-test", "ANTHROPIC_BASE_URL=https://gateway.example.com", "ANTHROPIC_MODEL=m",
		"CLAUDE_CONFIG_DIR=/Users/me/.claude-work", "HTTPS_PROXY=http://proxy.example.com:8080", "http_proxy=http://p",
		"ALL_PROXY=socks5://p", "NO_PROXY=localhost", "PATH=/Users/me/.persona/bin:/usr/bin", "HERDR_PANE_ID=w1:p1",
		"HERDR_ENV=1", "TERM_PROGRAM=herdr", "SHELL=/bin/bash", "NODE_OPTIONS=--x"}
	got := herdrServerEnv(environ, "/bin/zsh")
	want := []string{"HOME=/Users/me", "USER=me", "LOGNAME=me", "TMPDIR=/tmp/me/", "LANG=zh_CN.UTF-8",
		"XDG_CONFIG_HOME=/Users/me/.config", "HERDR_SOCKET_PATH=/Users/me/.config/herdr/herdr.sock",
		"HERDR_CONFIG_PATH=/Users/me/herdr.toml", "SHELL=/bin/zsh", "TERM=xterm-256color", "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	if !slices.Equal(got, want) {
		t.Fatalf("env\n got %q\nwant %q", got, want)
	}
	argv := herdrServerArgv("/bin/zsh", "/opt/homebrew/bin/herdr", got)
	if argv[0] != "/usr/bin/env" || argv[1] != "-i" || !slices.Equal(argv[2:len(got)+2], got) ||
		!slices.Equal(argv[len(got)+2:], []string{"/bin/zsh", "-l", "-c", "exec /opt/homebrew/bin/herdr server"}) {
		t.Fatalf("argv %q", argv)
	}
	if a := herdrServerArgv("/bin/zsh", "/Users/me/my tools/herdr", nil); a[len(a)-1] != "exec '/Users/me/my tools/herdr' server" {
		t.Fatalf("quoted %q", a[len(a)-1])
	}
}

// The data directory a node was told to use (PERSONA_HOME) reaches the server, and so every pane's shell: `personad
// launch` and the hooks find the node's socket by it (nodeclient.SocketPath). Without it a pane looks for
// ~/.persona/node.sock while the node listens in the custom directory.
func TestHerdrServerEnvKeepsTheDataDirectory(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "data")
	for _, name := range []string{"PERSONA_HOME", "JARVIS_HOME"} { // the second is the older name the node still reads
		t.Run(name, func(t *testing.T) {
			environ := []string{"HOME=/Users/me", name + "=" + custom, "ANTHROPIC_API_KEY=sk-test"}
			got := herdrServerEnv(environ, "/bin/sh")
			if !slices.Contains(got, name+"="+custom) {
				t.Fatalf("%s is not in the server's environment: %q", name, got)
			}
			if slices.Contains(got, "ANTHROPIC_API_KEY=sk-test") {
				t.Fatalf("the clean environment let a secret through: %q", got)
			}
			if runtime.GOOS == "windows" {
				return
			}
			// What a pane's shell sees, started the way the node starts the server.
			argv := append([]string{"-i"}, got...)
			out, err := exec.Command("/usr/bin/env", append(argv, "/bin/sh", "-c", "printf %s \"$"+name+"\"")...).Output()
			if err != nil || string(out) != custom {
				t.Fatalf("a process in the clean environment sees %s=%q (%v), want %q", name, out, err, custom)
			}
		})
	}
}

// Two nodes on one account with different data directories do not share one herdr server (#6482): the node of a
// custom PERSONA_HOME talks to a herdr session of its own, and starts that server with the session in its environment,
// so the panes (and the personad launch in them) belong to that node. The node of the default data directory keeps the
// default server, and a server already running there — started by another data directory's node before this — is not
// the custom node's.
func TestHerdrServerOfACustomDataDirIsItsOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the session layout is not checked on Windows")
	}
	home := t.TempDir()
	short, err := os.MkdirTemp("/tmp", "hs") // a unix socket's path is short (104 bytes on macOS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	cfg := filepath.Join(short, "cfg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_SESSION", "")
	clientOf := func(persona string) *herdr.Client {
		t.Setenv("PERSONA_HOME", persona)
		return engineHostsFor(proto.EngineChoice{Hosts: []string{proto.EngineHerdr}}, nil)[0].(*herdrHost).c
	}
	def, a, b := clientOf(""), clientOf(filepath.Join(home, "test-a")), clientOf(filepath.Join(home, "test-b"))
	if def.Session != "" || def.Socket != filepath.Join(cfg, "herdr", "herdr.sock") {
		t.Fatalf("the default data directory moved: session %q, socket %s", def.Session, def.Socket)
	}
	if a.Session == "" || b.Session == "" || a.Session == b.Session || a.Socket == def.Socket || a.Socket == b.Socket {
		t.Fatalf("nodes share a server: %q %s / %q %s / default %s", a.Session, a.Socket, b.Session, b.Socket, def.Socket)
	}
	// A server answering on the default socket (another data directory's) is not an answer for the custom node.
	if err = os.MkdirAll(filepath.Dir(def.Socket), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := herdr.Listen(def.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !herdrAlive(context.Background(), def.Socket) || herdrAlive(context.Background(), a.Socket) {
		t.Fatalf("default alive %v, custom alive %v", herdrAlive(context.Background(), def.Socket), herdrAlive(context.Background(), a.Socket))
	}
	// What the server of the custom directory starts with.
	env := withHerdrSession(herdrServerEnv(append(os.Environ(), "ANTHROPIC_API_KEY=sk-test"), "/bin/sh"), a.Session)
	if !slices.Contains(env, "HERDR_SESSION="+a.Session) || slices.Contains(env, "ANTHROPIC_API_KEY=sk-test") {
		t.Fatalf("server env %q", env)
	}
	if got := withHerdrSession([]string{"HOME=/h"}, ""); !slices.Equal(got, []string{"HOME=/h"}) {
		t.Fatalf("default server env changed: %q", got)
	}
	for _, own := range []string{"HERDR_SESSION=mine", "HERDR_SOCKET_PATH=/s/h.sock"} { // what the user set stands
		if got := withHerdrSession([]string{own}, a.Session); !slices.Equal(got, []string{own}) {
			t.Fatalf("%s overridden: %q", own, got)
		}
	}
}

// The node starting with herdr down starts it — after the hub said it may (node.configure), not before.
func TestHerdrAtNodeStartWaitsForTheHub(t *testing.T) {
	n, _ := downHerdrNode(t)
	fl := &fakeLaunch{}
	withLaunchHerdr(t, fl)
	prev := herdrStartWait
	herdrStartWait = time.Hour
	t.Cleanup(func() { herdrStartWait = prev })
	done := make(chan struct{})
	go func() { n.herdrAtStart(context.Background()); close(done) }()
	time.Sleep(50 * time.Millisecond)
	if fl.starts.Load() != 0 {
		t.Fatal("started before the hub said")
	}
	n.setHerdrAutostart(false)
	<-done
	if fl.starts.Load() != 0 {
		t.Fatal("started although turned off")
	}

	n2, _ := downHerdrNode(t)
	herdrStartWait = 10 * time.Millisecond // an older hub never says: on
	n2.herdrAtStart(context.Background())
	if fl.starts.Load() != 1 {
		t.Fatalf("%d starts", fl.starts.Load())
	}
}

// The resume command herdr runs for a Persona-started Claude: herdr told it is Claude (HERDR_AGENT, as at the start),
// the launcher and Persona's flags, quoted without apostrophes (herdr refuses them), never a bare `claude --resume`.
func TestClaudeResumeArgv(t *testing.T) {
	n, home := testNode(t)
	n.dir, n.bin = filepath.Join(home, ".persona"), "/Users/me/.persona/bin/personad"
	n.setManaged([]proto.ManagedLauncher{{Name: "work_gw", BaseURL: "https://gateway.example.com", Model: "m"}})
	argv, ok := n.claudeResumeArgv(claudeLaunch{Launcher: "work_gw", Settings: "/Users/me/.persona/launcher-settings/work_gw.json",
		Model: "opus[1m]", Hooks: "/Users/me/my hooks"}, startID)
	want := inAgentEnv(n.dir, "HERDR_AGENT=claude /Users/me/.persona/bin/personad launch work_gw --resume "+startID+
		" --settings /Users/me/.persona/launcher-settings/work_gw.json --mcp-config "+filepath.Join(n.dir, "mcp.json")+
		` --model "opus[1m]" --plugin-dir "/Users/me/my hooks"`, dquote)
	if !ok || len(argv) != 2 || argv[0] != "eval" || argv[1] != want {
		t.Fatalf("argv %q\nwant eval %q", argv, want)
	}
	if strings.Contains(argv[1], "'") {
		t.Fatal("an apostrophe in resume_argv")
	}
	for _, v := range []string{`a "b" $c \d` + "`e` ${HOME} $(id) !x", "plain/path-1.json", "opus[1m]", "中文 目录"} {
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+dquote(v)).Output()
		if err != nil || string(out) != v {
			t.Errorf("dquote(%q) = %s: the shell read %q (%v)", v, dquote(v), out, err)
		}
	}
	// A path with an apostrophe cannot be said: no resume command (and the start is not failed for it).
	if _, ok := n.claudeResumeArgv(claudeLaunch{Launcher: "claude", Settings: "/Users/me/it's/s.json"}, startID); ok {
		t.Fatal("apostrophe accepted")
	}
	// claude_start_dir (#1459) stays in the command: "$HOME" expands in the restored pane's shell.
	d := "~/.claude-work"
	n.claudeStartDir.Store(&d)
	if argv, _ := n.claudeResumeArgv(claudeLaunch{Launcher: "claude"}, startID); !strings.HasPrefix(argv[1],
		inAgentEnv(n.dir, `HERDR_AGENT=claude CLAUDE_CONFIG_DIR="$HOME"/.claude-work claude --resume `+startID, dquote)) {
		t.Fatalf("argv %q", argv)
	}
	// A Codex's resume says it is Codex; a compatible CLI and pi have no name in herdr: nothing in front.
	for _, tc := range []struct{ kind, launcher, want string }{
		{"codex", "cx", "HERDR_AGENT=codex cx resume " + startID},
		{traexAgent, "tx", "tx resume " + startID},
		{piAgent, "pi", "pi resume " + startID},
	} {
		if argv, ok := n.agentResumeArgv(agentLaunch{Agent: tc.kind, Launcher: tc.launcher}, startID); !ok || argv[1] != inAgentEnv(n.dir, tc.want, dquote) {
			t.Errorf("%s: argv %q, want eval %q", tc.kind, argv, tc.want)
		}
	}
}

// After a herdr restart, herdr resumes Persona's Claude with Persona's command (regression #1879: the restore ran a
// bare `claude --resume <id>` in eight panes, which dropped the gateway, the MCP config and the hooks). The pane's
// Claude moving to another session (/clear) moves the command with it; a SessionStart of the same session, or in a
// pane Persona did not start (the user's own claude), reports nothing.
func TestPersonaClaudeIsNotResumedBare(t *testing.T) {
	n, f, home := herdrNode(t)
	launch := n.hosts.Get(host.Herdr).(*herdrHost)
	spec := claudeLaunch{Launcher: "work_claude", Settings: filepath.Join(personaDir(home), "claude-settings.json")}
	n.holdResume(context.Background(), launch, "w1:p2", startID, spec)
	cmd := func(id string) string { return startCmd("work_claude", "--resume "+id, personaDir(home)) }
	held := resumeHeld("w1:p2", startID, cmd(startID))
	assertTrace(t, f.trace(), held[:])
	for _, c := range f.calls() {
		if argv, ok := c.Params["resume_argv"].([]any); ok && strings.HasPrefix(argv[1].(string), "claude --resume") {
			t.Fatalf("bare resume: %v", argv)
		}
	}

	f.reset()
	n.resumeOnSessionStart("w1:p2", startID) // the same session: already said
	n.resumeOnSessionStart("w1:p9", sidB)    // a pane Persona did not start
	time.Sleep(50 * time.Millisecond)
	assertTrace(t, f.trace(), nil)

	n.resumeOnSessionStart("w1:p2", sidB) // /clear: a new session in the same pane
	waitCalls(t, f, "pane.report_agent_session", 1)
	moved := resumeHeld("w1:p2", sidB, cmd(sidB))
	assertTrace(t, f.trace(), moved[:])
}

// The directory lock holds against another holder (a second node on the account: flock is per open file).
func TestLockHerdrDirExcludes(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockHerdrDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u, err := lockHerdrDir(dir)
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second holder got the lock")
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not released")
	}
}

// Persona's Claudes already running when this node came up (started by an older node, or before it restarted) get
// their resume command once the node sees them; the user's own claude in the next pane does not.
func TestRunningPersonaClaudeGetsItsResumeCommand(t *testing.T) {
	n, f, home := herdrNode(t)
	n.state.put(startID, sessionState{Pane: "w1:p2", Launcher: "work_claude", Cwd: home, StartedAt: time.Now()})
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		claudeAgent(startID, proto.StatusIdle, "w1:p2", "w1:t2", "w1", home, ""),
		claudeAgent(sidB, proto.StatusIdle, "w1:p3", "w1:t3", "w1", home, ""), // the user's own
	}, nil))
	listSessions(t, n)
	waitCalls(t, f, "pane.report_agent_session", 1)
	time.Sleep(100 * time.Millisecond)
	reports := f.trace("pane.report_agent_session")
	if len(reports) != 1 || !strings.Contains(reports[0], `"pane_id":"w1:p2"`) ||
		!strings.Contains(reports[0], `"eval",`+jsonPrefix(inAgentEnv(n.dir, "HERDR_AGENT=claude work_claude --resume "+startID, dquote))) {
		t.Fatalf("reports %v", reports)
	}
	// Once: the next refresh reports nothing again.
	listSessions(t, n)
	time.Sleep(100 * time.Millisecond)
	if got := f.trace("pane.report_agent_session"); len(got) != 1 {
		t.Fatalf("reported again: %v", got)
	}
}

// Persona's Codex and other agents (a compatible CLI, pi) also get resume commands through their launcher,
// instead of herdr falling back to bare builtin resume.
func TestPersonaOtherAgentsResumeWithLauncher(t *testing.T) {
	n, f, home := herdrNode(t)
	launch := n.hosts.Get(host.Herdr).(*herdrHost)

	// 1. Direct holdResume on codex
	spec := agentLaunch{Agent: "codex", Launcher: "work_codex", ID: startID}
	n.holdResume(context.Background(), launch, "w1:p4", startID, spec)
	held := resumeHeldAgent("codex", "w1:p4", startID, inAgentEnv(n.dir, "HERDR_AGENT=codex work_codex resume "+startID, dquote))
	assertTrace(t, f.trace(), held[:])

	// 2. Backfill for running Codex agent
	f.reset()
	n.state.put(sidB, sessionState{Pane: "w1:p5", Launcher: "custom_traex", Cwd: home, StartedAt: time.Now()})
	f.reply("session.snapshot", snapshot(nil, nil, []any{
		agentOf("traex", sidB, proto.StatusIdle, "w1:p5", "w1:t5", "w1", home, ""),
		agentOf("codex", "00000000-0000-4000-8000-000000000099", proto.StatusIdle, "w1:p6", "w1:t6", "w1", home, ""), // user's own
	}, nil))
	listSessions(t, n)
	waitCalls(t, f, "pane.report_agent_session", 1)
	time.Sleep(100 * time.Millisecond)
	reports := f.trace("pane.report_agent_session")
	if len(reports) != 1 || !strings.Contains(reports[0], `"pane_id":"w1:p5"`) ||
		!strings.Contains(reports[0], `"agent":"traex"`) ||
		!strings.Contains(reports[0], `"eval",`+jsonPrefix(inAgentEnv(n.dir, "custom_traex resume "+sidB, dquote))) {
		t.Fatalf("reports %v", reports)
	}
}
