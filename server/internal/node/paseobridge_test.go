package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/runtimepkg"
)

// The bridge is written out once per version, and older versions go.
func TestPaseoBridgeIsWrittenOut(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persona-paseo-runtime")
	files := []runtimepkg.File{{Path: "paseo/bridge.mjs", Data: []byte("// v1")}, {Path: "runtime-sdk/index.js", Data: []byte("export {}")}}
	pkg := &runtimepkg.Package{Name: paseoPackage, Files: files, Hash: runtimepkg.Hash(files)}
	got, err := writePackage(dir, pkg)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(got, "paseo", "bridge.mjs")); string(b) != "// v1" || filepath.Base(got) != pkg.Hash {
		t.Fatalf("wrote %s: %q", got, b)
	}
	again, err := writePackage(dir, pkg)
	if err != nil || again != got {
		t.Fatalf("again %s %v", again, err)
	}
	files2 := []runtimepkg.File{{Path: "paseo/bridge.mjs", Data: []byte("// v2")}}
	pkg2 := &runtimepkg.Package{Name: paseoPackage, Files: files2, Hash: runtimepkg.Hash(files2)}
	if _, err := writePackage(dir, pkg2); err != nil {
		t.Fatal(err)
	}
	if dirExists(got) {
		t.Fatal("the older version stayed")
	}
	bad := &runtimepkg.Package{Name: paseoPackage, Files: []runtimepkg.File{{Path: "../escape.js"}}, Hash: "x"}
	if _, err := writePackage(dir, bad); err == nil {
		t.Fatal("a path out of the package was written")
	}
}

// What runs the bridge: PERSONA_NODEJS first; an app binary (Paseo's desktop app) runs as node with
// ELECTRON_RUN_AS_NODE, and a node without it, also when the node inherited it; one too old is skipped.
func TestPaseoBridgeJSRuntime(t *testing.T) {
	t.Setenv("ELECTRON_RUN_AS_NODE", "1") // as under an Electron host, such as an editor's extension host
	dir := t.TempDir()
	fake := func(name, version string) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$ELECTRON_RUN_AS_NODE\" != \"${WANT_ELECTRON:-}\" ]; then exit 3; fi\nprintf %s\n", version)), 0o755)
		return p
	}
	old := fake("node-old", "16.20.0")
	t.Setenv("PERSONA_NODEJS", old)
	t.Setenv("PATH", dir) // nothing else to find
	if js, err := findJSRuntime(context.Background(), t.TempDir()); err == nil && js.Path == old {
		t.Fatal("a Node.js older than the bridge needs was taken")
	}
	app := fake("Paseo", "22.20.0")
	t.Setenv("PERSONA_NODEJS", app)
	t.Setenv("WANT_ELECTRON", "1")
	js, err := findJSRuntime(context.Background(), t.TempDir())
	if err != nil || js.Path != app || !js.Electron {
		t.Fatalf("the app binary: %+v %v", js, err)
	}
	t.Setenv("WANT_ELECTRON", "")
	node := fake("node", "22.16.0")
	t.Setenv("PERSONA_NODEJS", node)
	if js, err := findJSRuntime(context.Background(), t.TempDir()); err != nil || js.Path != node || js.Electron {
		t.Fatalf("node: %+v %v", js, err)
	}
}

// The runtime's environment sets ELECTRON_RUN_AS_NODE itself: once for an app binary, never for a node (dropping an
// inherited one), and the caller's slice is left as it was.
func TestJSRuntimeEnv(t *testing.T) {
	base := []string{"A=1", "ELECTRON_RUN_AS_NODE=1", "B=2"}
	if got := (jsRuntime{Path: "node"}).env(base); !slices.Equal(got, []string{"A=1", "B=2"}) {
		t.Fatalf("node: %q", got)
	}
	if got := (jsRuntime{Path: "Paseo", Electron: true}).env(base); !slices.Equal(got, []string{"A=1", "B=2", "ELECTRON_RUN_AS_NODE=1"}) {
		t.Fatalf("app binary: %q", got)
	}
	if !slices.Equal(base, []string{"A=1", "ELECTRON_RUN_AS_NODE=1", "B=2"}) {
		t.Fatalf("base changed: %q", base)
	}
}

// The bridge is told the node's mcp.json, the one beside the runtime socket, to give its new Agents the Persona tools,
// and which of them its Agents call without a card: the same as a Claude Agent's start allows (#4966).
func TestPaseoBridgeArgsNameTheNodesMCPConfig(t *testing.T) {
	got := paseoBridgeArgs("/pkg", "/data/persona/runtime.sock", "/home/u/.paseo")
	want := []string{"/pkg/paseo/bridge.mjs", "--socket", "/data/persona/runtime.sock", "--paseo-home", "/home/u/.paseo",
		"--mcp-config", "/data/persona/mcp.json", "--allow-tools", ownToolsRule}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// fakeBridge makes the bridge a shell script that marks each start in dir and lives until stopped (or exits at once
// when quit is set).
func fakeBridge(t *testing.T, quit bool) string {
	t.Helper()
	dir := t.TempDir()
	old := paseoBridgeCommand
	t.Cleanup(func() { paseoBridgeCommand = old })
	paseoBridgeCommand = func(ctx context.Context, rh *RuntimeHost) (*exec.Cmd, error) {
		script := fmt.Sprintf("echo started >> %q; echo hello from the bridge", filepath.Join(dir, "starts"))
		if !quit {
			script += "; trap 'echo stopped >> " + filepath.Join(dir, "starts") + "; exit 0' TERM; while :; do sleep 0.05; done"
		}
		return exec.Command("/bin/sh", "-c", script), nil
	}
	oldMin := paseoBridgeRestartMin
	paseoBridgeRestartMin = 20 * time.Millisecond
	t.Cleanup(func() { paseoBridgeRestartMin = oldMin })
	return filepath.Join(dir, "starts")
}

func starts(path string) []string {
	b, _ := os.ReadFile(path)
	return strings.Fields(string(b))
}

// The bridge runs while the engine is Paseo: engine.use starts it, another engine stops it; one that exits is run
// again.
func TestPaseoBridgeFollowsTheEngine(t *testing.T) {
	marks := fakeBridge(t, false)
	n, _ := runtimeNode(t)
	if _, err := nodeCall(t, n, "engine.use", map[string]any{"engine": proto.EnginePaseo}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the bridge started", func() bool { return len(starts(marks)) == 1 })
	if _, err := nodeCall(t, n, "engine.use", map[string]any{"engine": proto.EnginePaseo}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if len(starts(marks)) != 1 {
		t.Fatal("started twice for the same engine")
	}
	if _, err := nodeCall(t, n, "engine.use", map[string]any{"engine": proto.EngineHerdr}); err != nil {
		t.Fatal(err)
	}
	if got := starts(marks); len(got) != 2 || got[1] != "stopped" {
		t.Fatalf("not stopped: %v", got)
	}

	quitting := fakeBridge(t, true)
	if _, err := nodeCall(t, n, "engine.use", map[string]any{"engine": proto.EnginePaseo}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "run again after it exited", func() bool { return len(starts(quitting)) >= 2 })
	n.runtimeHost.setPaseoBridge(false)
}

// node.info's Paseo: available when its daemon answers at paseo.pid's address, or its bridge is connected (then with
// Paseo's version). Never at a default port: that may be another home's daemon.
func TestPaseoEngineDetected(t *testing.T) {
	n, _ := runtimeNode(t)
	home := t.TempDir()
	t.Setenv("PASEO_HOME", home)
	e := n.paseoEngine(context.Background())
	if e.ID != proto.EnginePaseo || e.Name != "Paseo" || e.Available {
		t.Fatalf("without Paseo: %+v", e)
	}
	_ = os.WriteFile(filepath.Join(home, "paseo.pid"), []byte(`{"pid":1,"listen":null}`), 0o600)
	if e := n.paseoEngine(context.Background()); e.Available {
		t.Fatalf("a Paseo still starting: %+v", e)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "listen": ln.Addr().String()})
	_ = os.WriteFile(filepath.Join(home, "paseo.pid"), b, 0o600)
	if e := n.paseoEngine(context.Background()); !e.Available {
		t.Fatalf("Paseo listening: %+v", e)
	}
	hello := paseoEngineHello()
	hello["version"] = "0.14.0"
	dialRuntime(t, n, hello)
	if e := n.paseoEngine(context.Background()); !e.Available || e.Version != "0.14.0" {
		t.Fatalf("bridge connected: %+v", e)
	}
	found := false
	for _, x := range n.engines(context.Background(), "") {
		found = found || x.ID == proto.EnginePaseo
	}
	if !found {
		t.Fatal("node.info's engines leave Paseo out")
	}
}

// A line longer than the buffer is cut and the lines after it still come (#3959): the program's log is read to its end.
func TestLogLinesReadsPastALongLine(t *testing.T) {
	var got []string
	long := strings.Repeat("y", 2<<20) // past any buffer the reader may have
	logLines(strings.NewReader("one\r\n"+long+"\n"+strings.Repeat("z", 3000)+"\ntwo"), func(l string) { got = append(got, l) })
	want := []string{"one", long[:2000] + "…", strings.Repeat("z", 2000) + "…", "two"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %d lines, want %d: %.60q", len(got), len(want), got)
	}
}
