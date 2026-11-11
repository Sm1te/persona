package devices

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// loginShells are the shells on this machine an ssh target's login shell could be: sshd hands the remote command to
// it as `<shell> -c <command>`, so a remote command must mean the same in each (fish and csh are not POSIX shells).
func loginShells(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, sh := range []string{"sh", "bash", "zsh", "dash", "fish", "csh", "tcsh"} {
		if p, err := exec.LookPath(sh); err == nil {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		t.Skip("no shell")
	}
	return found
}

// The command the hub runs over ssh installs the gzipped binary from stdin at the node's path (~ expanded, a path
// with a quote, a backslash or a ! kept whole), leaves no .new file behind, and restarts the machine's resident node,
// whatever the machine's login shell is (#1770: on a machine whose login shell is fish it failed at `b=`).
func TestDeployScriptInstallsTheBinary(t *testing.T) {
	for _, shell := range loginShells(t) {
		for _, bin := range []string{"~/.persona/bin/personad", "~/odd 'dir'/personad", `~/back\\slash \'q/personad`, "~/bang!x/personad"} {
			t.Run(filepath.Base(shell)+"/"+bin, func(t *testing.T) { testDeployScript(t, shell, bin) })
		}
	}
}

func testDeployScript(t *testing.T, shell, bin string) {
	home := t.TempDir()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("#!/bin/sh\necho new\n"))
	_ = zw.Close()
	pkilled := fakePkill(t)
	cmd := exec.Command(shell, "-c", deployScript(bin, false))
	cmd.Env = shellEnv(home, filepath.Dir(pkilled))
	cmd.Stdin = &gz
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got, _ := os.ReadFile(pkilled)
	pattern, ok := strings.CutPrefix(strings.TrimSuffix(string(got), "\n"), "-f ")
	if !ok {
		t.Fatalf("the resident node is not restarted: pkill got %q", got)
	}
	// The pattern finds the resident node persona-link's service runs (cmd/persona-link nodesvc.go: personad, or
	// persona-node on a device), and the one of a computer not cut over to the Persona names yet; it leaves the other
	// Persona processes and the remote command that carries it alone.
	re := regexp.MustCompile(pattern)
	personad := filepath.Join(home, ".persona", "bin", "personad")
	for _, node := range []string{personad + " node --attach", filepath.Join(home, ".persona", "bin", "persona-node") + " node --attach",
		filepath.Join(home, compat.StateDir, "bin", compat.DaemonBinary) + " node --attach"} {
		if !re.MatchString(node) {
			t.Errorf("pkill -f %q does not find the resident node %q", pattern, node)
		}
	}
	for _, other := range []string{personad + " node --stdio", personad + " hub run", personad + " hook", personad + "-wechat run",
		personad + " manager run --dir /x", filepath.Join(home, ".persona", "bin", "persona-node") + " node --stdio",
		filepath.Join(home, ".persona", "bin", "persona-link") + " run", deployScript(bin, false), deployScript(bin, true)} {
		if re.MatchString(other) {
			t.Errorf("pkill -f %q matches %q", pattern, other)
		}
	}
	path := filepath.Join(home, strings.TrimPrefix(bin, "~/"))
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "#!/bin/sh\necho new\n" {
		t.Fatalf("%q %v", data, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode())
	}
	if _, err := os.Stat(path + ".new"); err == nil {
		t.Error(".new left behind")
	}
}

// A staged file that cannot run `version` must never replace the resident binary or stop the working node (#5097).
func TestDeployScriptRejectsAnUnusableBinary(t *testing.T) {
	for _, shell := range loginShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, "personad")
			old := []byte("#!/bin/sh\necho old\n")
			if err := os.WriteFile(bin, old, 0o755); err != nil {
				t.Fatal(err)
			}
			var gz bytes.Buffer
			zw := gzip.NewWriter(&gz)
			_, _ = zw.Write([]byte("not an executable\n"))
			_ = zw.Close()
			pkilled := fakePkill(t)
			cmd := exec.Command(shell, "-c", deployScript(bin, false))
			cmd.Env = shellEnv(home, filepath.Dir(pkilled))
			cmd.Stdin = &gz
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(strings.ToLower(string(out)), "version") {
				t.Fatalf("an unusable staged binary was accepted: %v %s", err, out)
			}
			got, readErr := os.ReadFile(bin)
			if readErr != nil || !bytes.Equal(got, old) {
				t.Fatalf("the resident binary changed: %q %v", got, readErr)
			}
			if called, _ := os.ReadFile(pkilled); len(called) != 0 {
				t.Fatalf("the working node was stopped: %q", called)
			}
			if _, statErr := os.Stat(bin + ".new"); !os.IsNotExist(statErr) {
				t.Fatalf("the rejected staged file remains: %v", statErr)
			}
		})
	}
}

// No binary for the machine's platform on the hub: say what to do, don't start.
func TestNodeBinaryMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	isolateNodeBinaries(t)
	if _, err := nodeBinary("plan9", "mips", "", ""); err == nil || !strings.Contains(err.Error(), "deploy.sh") {
		t.Fatalf("err = %v", err)
	}
	if _, err := nodeBinary("", "", "", ""); err == nil {
		t.Fatal("no platform yet: want an error")
	}
	dir := nodeBinaryDir()
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "personad-darwin-arm64"), []byte("x"), 0o755)
	if p, err := nodeBinary("darwin", "arm64", "", ""); err != nil || filepath.Base(p) != "personad-darwin-arm64" {
		t.Fatalf("%s %v", p, err)
	}
	// A node that runs persona-node gets persona-node when the hub has it for the hub's version, personad otherwise;
	// one that runs personad (or an older node that does not say) always personad: it may run a hub or a manager too.
	if p, _ := nodeBinary("darwin", "arm64", nodeproto.ProgramPersonaNode, "v2"); filepath.Base(p) != "personad-darwin-arm64" {
		t.Fatalf("persona-node without its build on the hub: %s", p)
	}
	node := filepath.Join(dir, "persona-node-darwin-arm64")
	_ = os.WriteFile(node, []byte("x"), 0o755)
	if p, _ := nodeBinary("darwin", "arm64", nodeproto.ProgramPersonaNode, "v2"); filepath.Base(p) != "personad-darwin-arm64" {
		t.Fatalf("a persona-node that does not say its version: %s", p)
	}
	_ = os.WriteFile(node+".version", []byte("v2\n"), 0o644) // deploy.sh writes it after the binary
	for program, want := range map[string]string{nodeproto.ProgramPersonaNode: "persona-node-darwin-arm64",
		nodeproto.ProgramPersonad: "personad-darwin-arm64", "": "personad-darwin-arm64"} {
		if p, err := nodeBinary("darwin", "arm64", program, "v2"); err != nil || filepath.Base(p) != want {
			t.Errorf("%q: %s %v, want %s", program, p, err, want)
		}
	}
	// The hub moved on (its own update replaces personad-* alone), or no version is known: the earlier deploy's
	// persona-node is another build, never sent.
	for _, hub := range []string{"v3", ""} {
		if p, _ := nodeBinary("darwin", "arm64", nodeproto.ProgramPersonaNode, hub); filepath.Base(p) != "personad-darwin-arm64" {
			t.Errorf("hub %q: %s, want personad", hub, p)
		}
	}
	_ = os.Remove(node) // the version without the file
	if p, _ := nodeBinary("darwin", "arm64", nodeproto.ProgramPersonaNode, "v2"); filepath.Base(p) != "personad-darwin-arm64" {
		t.Errorf("no persona-node file: %s", p)
	}
}

// The ssh deploy keeps personad where it is when the build nodeBinary picked is persona-node, by its file name there.
func TestIsPersonaNodeBuild(t *testing.T) {
	for bin, want := range map[string]bool{"/h/.persona/bin/nodes/persona-node-darwin-arm64": true,
		"/h/.persona/bin/nodes/personad-darwin-arm64": false, "/h/persona-node-dir/personad-linux-amd64": false} {
		if isPersonaNodeBuild(bin) != want {
			t.Errorf("%s: %v", bin, !want)
		}
	}
}

// Redeploying persona-node over ssh never replaces a file that is not persona-node already (an install that made it
// personad after the node said what it runs), in every login shell; persona-node over persona-node, or where there is
// nothing yet, goes in as any build does.
func TestDeployScriptKeepsPersonad(t *testing.T) {
	for _, shell := range loginShells(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			for _, tc := range []struct {
				name, existing string
				replaced       bool
			}{
				{"none", "", true},
				{"persona-node", `{"version":"v1","os":"linux","arch":"amd64","program":"persona-node"}`, true},
				{"personad", `{"version":"v1","os":"linux","arch":"amd64","program":"personad"}`, false},
				{"older personad", "", false}, // a build without node-update-check: usage on stderr, exit 2
			} {
				home := t.TempDir()
				bin := filepath.Join(home, ".persona", "bin", "persona-node")
				old := ""
				if tc.name != "none" {
					old = "#!/bin/sh\n[ \"$1\" = node-update-check ] || exit 0\n"
					if tc.existing != "" {
						old += "echo '" + tc.existing + "'\n"
					} else {
						old += "echo usage >&2; exit 2\n"
					}
					_ = os.MkdirAll(filepath.Dir(bin), 0o755)
					_ = os.WriteFile(bin, []byte(old), 0o755)
				}
				var gz bytes.Buffer
				zw := gzip.NewWriter(&gz)
				_, _ = zw.Write([]byte("#!/bin/sh\necho new\n"))
				_ = zw.Close()
				pkilled := fakePkill(t)
				cmd := exec.Command(shell, "-c", deployScript("~/.persona/bin/persona-node", true))
				cmd.Env = shellEnv(home, filepath.Dir(pkilled))
				cmd.Stdin = &gz
				out, err := cmd.CombinedOutput()
				got, _ := os.ReadFile(bin)
				if tc.replaced {
					if err != nil || string(got) != "#!/bin/sh\necho new\n" {
						t.Errorf("%s: %v %s, file %q", tc.name, err, out, got)
					}
					continue
				}
				if err == nil || !strings.Contains(string(out), "not replacing it with persona-node") || string(got) != old {
					t.Errorf("%s: %v %s, file %q", tc.name, err, out, got)
				}
				if called, _ := os.ReadFile(pkilled); len(called) != 0 {
					t.Errorf("%s: the node was restarted: %q", tc.name, called)
				}
				if _, err := os.Stat(bin + ".new"); err == nil {
					t.Errorf("%s: .new left behind", tc.name)
				}
			}
		})
	}
}

// An ssh deploy writes to the device's file when the node runs from it (persona-node, or personad when the hub has no
// persona-node build), never persona-node to remote_bin (personad on a computer that may run a hub or a manager too);
// a personad node's goes to remote_bin as before. nodeProgram brings persona-node back to a device file that holds
// personad.
func TestRemoteTarget(t *testing.T) {
	if got := nodeProgram(proto.NodeInfo{Program: nodeproto.ProgramPersonad, Executable: "/home/u/.persona/bin/persona-node"}); got != nodeproto.ProgramPersonaNode {
		t.Errorf("personad in the device's file: %s", got)
	}
	if got := nodeProgram(proto.NodeInfo{Program: nodeproto.ProgramPersonad, Executable: "/home/u/.persona/bin/personad"}); got != nodeproto.ProgramPersonad {
		t.Errorf("personad: %s", got)
	}
	node := proto.NodeInfo{Program: nodeproto.ProgramPersonaNode, Executable: "/home/u/.persona/bin/persona-node"}
	full := proto.NodeInfo{Program: nodeproto.ProgramPersonad, Executable: "/home/u/.persona/bin/personad"}
	custom := MachineConfig{RemoteBin: "/opt/persona/personad"}
	for _, tc := range []struct {
		cfg  MachineConfig
		info proto.NodeInfo
		bin  string
		want string
	}{
		{MachineConfig{}, node, "/hub/nodes/persona-node-linux-amd64", "/home/u/.persona/bin/persona-node"},
		{custom, node, "/hub/nodes/persona-node-linux-amd64", "/home/u/.persona/bin/persona-node"},
		{MachineConfig{}, proto.NodeInfo{}, "/hub/nodes/persona-node-linux-amd64", "~/.persona/bin/persona-node"},
		{MachineConfig{}, full, "/hub/nodes/personad-linux-amd64", "~/.persona/bin/personad"},
		{custom, full, "/hub/nodes/personad-linux-amd64", "/opt/persona/personad"},
		{MachineConfig{}, node, "/hub/nodes/personad-linux-amd64", "/home/u/.persona/bin/persona-node"}, // no persona-node build: personad to that file
		// personad landed in the device's file before: the node says personad, its file is still the device's.
		{custom, proto.NodeInfo{Program: nodeproto.ProgramPersonad, Executable: "/home/u/.persona/bin/persona-node"},
			"/hub/nodes/persona-node-linux-amd64", "/home/u/.persona/bin/persona-node"},
	} {
		if got := remoteTarget(tc.cfg, tc.info, tc.bin); got != tc.want {
			t.Errorf("%+v %s: %s, want %s", tc.info, filepath.Base(tc.bin), got, tc.want)
		}
	}
}

// isolateNodeBinaries points the node binary dir at a directory of this test: an exported PERSONA_HOME (an assistant's
// shell on the hub machine has one) or *_NODE_BINARIES would otherwise send these tests' files into the real one.
func isolateNodeBinaries(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PERSONA_NODE_BINARIES", dir)
	t.Setenv("PERSONA_NODE_BUNDLED", "")                             // the machine running the test may be a Docker hub's
	t.Setenv("PERSONA_HOME", filepath.Join(t.TempDir(), ".persona")) // the package cache, nowhere real
}

// shellEnv is the environment the deploy script runs in under each login shell: HOME is home, the PATH is stubs then the
// system's tools only, and nothing makes a shell read the developer's rc files (BASH_ENV, ENV, ZDOTDIR, fish through
// XDG_CONFIG_HOME) or points at their state (PERSONA_*).
func shellEnv(home, stubs string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "PATH", k == "HOME", k == "BASH_ENV", k == "ENV", k == "ZDOTDIR", strings.HasPrefix(k, "XDG_"),
			strings.HasPrefix(k, "PERSONA_"):
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "ZDOTDIR="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PATH="+stubs+string(os.PathListSeparator)+"/usr/bin:/bin")
}

// fakePkill puts a pkill first on PATH that only records its arguments (the returned file) and kills nothing. The
// deploy script ends with `pkill -f '[p]ersonad node --attach'`; run for real on a developer's machine it stops the
// resident node serving that machine (it did, every time the hub tests ran on a Mac with one: the machine went offline
// for a minute and a half).
func fakePkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "pkill.calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(calls) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "pkill"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return calls
}

// A word anyShellQuote quotes reads back unchanged in every login shell, quotes, backslashes and the characters each
// shell treats specially included.
func TestAnyShellQuoteReadsTheSameInEveryShell(t *testing.T) {
	words := []string{"", "plain", "it's", `back\slash`, `\\`, `\'`, `'\''`, `a"b`, "a!b", "!!", "!$", "$HOME ~ * ? [x] {a,b} `id` $(id) ! ; & | < > # %"}
	for _, shell := range loginShells(t) {
		for _, w := range words {
			out, err := exec.Command(shell, "-c", "printf '%s|' "+anyShellQuote(w)).CombinedOutput()
			if err != nil || string(out) != w+"|" {
				t.Errorf("%s: %q read back as %q (%v)", filepath.Base(shell), w, out, err)
			}
		}
	}
}

// The remote command that starts a node over ssh is a plain command with arguments, which every login shell runs
// alike (#1770): it starts the node at ~/.persona/bin/personad with `node --stdio`.
func TestNodeCommandRunsInEveryShell(t *testing.T) {
	m := newMachine(newTestDevices(t), MachineConfig{ID: "box", Name: "Box", Kind: "ssh"})
	args := m.command(context.Background()).Args
	remote := args[len(args)-1]
	for _, shell := range loginShells(t) {
		home := t.TempDir()
		calls := filepath.Join(home, "calls")
		bin := filepath.Join(home, ".persona", "bin", "personad")
		_ = os.MkdirAll(filepath.Dir(bin), 0o755)
		_ = os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s|' \"$@\" > "+shellQuote(calls)+"\n"), 0o755)
		cmd := exec.Command(shell, "-c", remote)
		cmd.Env = append(os.Environ(), "HOME="+home)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %q: %v: %s", filepath.Base(shell), remote, err, out)
		}
		if got, _ := os.ReadFile(calls); string(got) != "node|--stdio|" {
			t.Errorf("%s: %q ran the node with %q", filepath.Base(shell), remote, got)
		}
	}
}

// An ssh that fails before reading the binary (auth failure, connect timeout) must not leave the goroutine that gzips
// the binary into it blocked on the pipe for good (#2377).
func TestDeployOverFailedSSHDoesNotLeakTheWriter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\necho 'Permission denied (publickey).' >&2\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Incompressible and far larger than any pipe buffer, so the writer is still writing when ssh exits.
	data := make([]byte, 8<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "personad")
	if err := os.WriteFile(bin, data, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Machine{cfg: MachineConfig{ID: "test-node"}}
	err := m.deploy(context.Background(), bin, "test")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("deploy: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		var stacks bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&stacks, 2)
		if !strings.Contains(stacks.String(), "(*Machine).deploy.func") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gzip writer is still running after deploy returned:\n%s", stacks.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A deploy whose machine does not come back must not say what it "still runs": the hub is not connected to it, and
// the version it knows is the node from before the deploy (#3393). The last connection error says what went wrong.
func TestDeployThatDoesNotComeBackSaysSo(t *testing.T) {
	wait, poll := deployWait, deployPoll
	deployWait, deployPoll = 50*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { deployWait, deployPoll = wait, poll })
	d := newTestDevices(t)

	m := newMachine(d, MachineConfig{ID: "away", Name: "away", Kind: "ssh"})
	m.info.Version = "old-build"
	m.err = "reconnect requested" // how the old connection ended: no reason why the new one is not there
	go func() {
		time.Sleep(10 * time.Millisecond)
		m.markOffline(errors.New("ssh: connect to host away port 22: Connection refused"))
	}()
	err := m.waitDeployed(context.Background(), d.hubVersion())
	if err == nil {
		t.Fatal("waitDeployed: no error for a machine that never came back")
	}
	if strings.Contains(err.Error(), "old-build") || strings.Contains(err.Error(), "跑的还是") {
		t.Errorf("says what an unconnected machine runs: %v", err)
	}
	if !strings.Contains(err.Error(), "没有重新连上") || !strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("does not say it did not come back, and why: %v", err)
	}

	// Back, but on another build: that is what it still runs.
	m2 := newMachine(d, MachineConfig{ID: "back", Name: "back", Kind: "ssh"})
	m2.online, m2.info.Version = true, "old-build"
	if err := m2.waitDeployed(context.Background(), d.hubVersion()); err == nil || !strings.Contains(err.Error(), "跑的还是 old-build") {
		t.Errorf("online on another build: %v", err)
	}
}

// Our own reconnect's "reconnect requested" is not why a redeployed machine is not back.
func TestDeployThatDoesNotComeBackHidesItsOwnReconnect(t *testing.T) {
	wait, poll := deployWait, deployPoll
	deployWait, deployPoll = 30*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { deployWait, deployPoll = wait, poll })
	d := newTestDevices(t)
	m := newMachine(d, MachineConfig{ID: "away", Name: "away", Kind: "ssh"})
	m.info.Version, m.err = "old-build", "reconnect requested"
	err := m.waitDeployed(context.Background(), d.hubVersion())
	if err == nil || strings.Contains(err.Error(), "reconnect requested") || !strings.Contains(err.Error(), "没有重新连上") {
		t.Errorf("waitDeployed: %v", err)
	}
}

func TestWaitText(t *testing.T) {
	for d, want := range map[time.Duration]string{2 * time.Minute: "2 分钟", 90 * time.Second: "90 秒", 50 * time.Millisecond: "0 秒"} {
		if got := waitText(i18n.ZhHans, d); got != want {
			t.Errorf("waitText(%v) = %q, want %q", d, got, want)
		}
	}
}
