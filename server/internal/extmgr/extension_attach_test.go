package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/extwire"
)

// attachablePackage is the scoped extension granted the hub's full token the old way (hub_api), with a skill.
func attachablePackage(t *testing.T) (dir string, manifest []byte) {
	dir = t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "scoped"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=scoped exec "+self+" \"$@\"\n"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "skills", "example-scoped"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "skills", "example-scoped", "SKILL.md"), []byte("---\nname: example-scoped\ndescription: Example.\n---\nUse it.\n"), 0o644)
	manifest = []byte(fmt.Sprintf(`{"manifest_version": 1, "id": "scoped", "name": "Scoped", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/scoped"}, "http": true},
		"activation_events": ["on_startup"],
		"contributes": {"api": [{"id": "x", "prefix": "scopedx"}], "assistant": [{"id": "scoped", "skills": ["skills/example-scoped"]}]},
		"permissions": {"hub_api": true}}`, runtime.GOOS+"-"+runtime.GOARCH))
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), manifest, 0o644)
	return dir, manifest
}

// startAttached runs the scoped extension on its own on sockets in dir, as version (its manifest is manifest), saying
// what initialize gave it; stop ends it and frees the sockets.
func startAttached(t *testing.T, dir string, manifest []byte, version string) (rpcSock, httpSock string, init func() *extension.InitializeParams, stop func()) {
	t.Helper()
	rpcSock, httpSock = filepath.Join(dir, "rpc.sock"), filepath.Join(dir, "http.sock")
	hl, err := net.Listen("unix", httpSock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hl.Close() })
	go http.Serve(hl, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) }))
	var mu sync.Mutex
	var got *extension.InitializeParams
	prog := pserve.Program{ID: "scoped", Version: version, Manifest: manifest,
		Init: func(_ context.Context, ip extension.InitializeParams, _ *pserve.Hub) (map[string]any, error) {
			mu.Lock()
			got = &ip
			mu.Unlock()
			return nil, nil
		}}
	n := &notesProgram{}
	if n.ln, err = net.Listen("unix", rpcSock); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := n.ln.Accept()
			if err != nil {
				return
			}
			n.mu.Lock()
			n.conns = append(n.conns, c)
			n.mu.Unlock()
			go func() {
				defer c.Close()
				_ = pserve.Serve(context.Background(), c, c, prog)
			}()
		}
	}()
	t.Cleanup(n.stop)
	return rpcSock, httpSock, func() *extension.InitializeParams {
			mu.Lock()
			defer mu.Unlock()
			return got
		}, func() {
			n.stop()
			hl.Close()
		}
}

// liveToken says whether extension id holds a token now, and whether tok still opens a route of agents.control.
func liveToken(h *Manager, id, tok string) (holds, opens bool) {
	h.extTokens.mu.Lock()
	_, holds = h.extTokens.byExt[id]
	h.extTokens.mu.Unlock()
	a := h.authorize(extwire.Authorize{Hash: hashToken(tok), Pattern: "POST /api/v1/sessions/{mid}/{sid}/messages", Method: "POST",
		Path: "/api/v1/sessions/m/s/messages"})
	return holds, a.OK || a.Extension != ""
}

// An installed package attached to its program running on its own keeps its package (its skills still reach the
// assistant), and that program is told no path of this machine and gets a token of every scope, never the hub's own:
// not the permission mode's, which its manifest does not name, and no route open to full credentials only, such as
// attaching or detaching any extension, its own included. Detached, the package's program runs here again and the
// attached one's token opens nothing. Only from this machine, with the hub's own token.
func TestAttachedPackageKeepsItsPackageAndGetsAScopedToken(t *testing.T) {
	shortHostTimings(t)
	h, store := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	manifest := installAttachable(t, h, mux)
	ready := func(attached bool) {
		t.Helper()
		waitLong(t, fmt.Sprintf("running (attached %v)", attached), func() bool {
			hp := h.extensions.get("scoped")
			return hp != nil && hp.proc != nil && hp.proc.Ready() && hp.proc.Spec().Attached == attached
		})
	}
	ready(false)
	skills := func() bool {
		return slices.ContainsFunc(h.extensions.extensionSkills(), func(s ExtensionSkill) bool {
			return s.Extension == "scoped" && s.Name == "example-scoped" && len(s.Files["SKILL.md"]) > 0
		})
	}
	if !skills() {
		t.Fatal("no skill from the package")
	}

	dir, err := os.MkdirTemp("/tmp", "att-") // a unix socket path stays short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	rpcSock, httpSock, init, _ := startAttached(t, dir, manifest, "1.0.0")
	source := `{"source": "remote:rpc=` + rpcSock + `,http=` + httpSock + `"}`
	if code, out := callFrom(t, mux, "POST", "/api/v1/extensions/scoped/-/attach", master, source, "[fd00::10]:1234"); code != 403 || out["code"] != "this_machine_only" {
		t.Fatalf("attach from another machine: %d %v", code, out)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/attach", master, `{"source": "remote:rpc=`+rpcSock+`"}`); code != 400 || out["code"] != "attach_failed" {
		t.Fatalf("without the HTTP socket it serves: %d %v", code, out)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/attach", master, source); code != 200 || out["id"] != "scoped" {
		t.Fatalf("attach: %d %v", code, out)
	}
	ready(true)
	var ip *extension.InitializeParams
	waitLong(t, "the attached program initialized", func() bool { ip = init(); return ip != nil })
	tok := ip.Hub.Token
	if !strings.HasPrefix(tok, extTokenPrefix) || tok == master || ip.Hub.URL == "" {
		t.Fatalf("an attached program gets a token of its own, never the hub's: %q at %q", tok, ip.Hub.URL)
	}
	if ip.Extension.Dir != "" || ip.Extension.DataDir != "" || ip.Extension.Version != "1.0.0" {
		t.Fatalf("paths of the manager's machine: %+v", ip.Extension)
	}
	if slices.Contains(ip.Hub.Scopes, extension.ScopeAgentsPermissionMode) || !slices.Contains(ip.Hub.Scopes, extension.ScopeAgentsControl) ||
		slices.Contains(ip.Hub.Scopes, extension.ScopeModels) {
		t.Fatalf("scopes: %v", ip.Hub.Scopes)
	}
	for _, e := range h.extensions.get("scoped").proc.Spec().Env {
		if strings.HasPrefix(e, "PERSONA_HUB_TOKEN=") {
			t.Fatalf("an attached one has no environment of the hub's: %s", e)
		}
	}
	authorize := func(tok, pattern, path string) extwire.Authorized {
		method, _, _ := strings.Cut(pattern, " ")
		return h.authorize(extwire.Authorize{Hash: hashToken(tok), Pattern: pattern, Method: method, Path: path})
	}
	if a := authorize(tok, "POST /api/v1/sessions/{mid}/{sid}/modes", "/api/v1/sessions/m/s/modes"); a.OK || a.Extension != "scoped" ||
		a.Scope != extension.ScopeAgentsPermissionMode || slices.Contains(a.Scopes, extension.ScopeAgentsPermissionMode) {
		t.Fatalf("a token of every scope changes permission modes: %+v", a)
	}
	if e, ok := h.extTokens.lookupHash(hashToken(tok)); !ok || !e.all || slices.Contains(e.principal().Scopes, extension.ScopeAgentsPermissionMode) {
		t.Fatalf("the attached token's principal: %+v", e.principal())
	}
	if a := authorize(tok, "POST /api/v1/sessions/{mid}/{sid}/rename", "/api/v1/sessions/m/s/rename"); !a.OK || a.Scope != extension.ScopeAgentsControl {
		t.Fatalf("rename is agents.control: %+v", a)
	}
	if a := authorize(tok, "POST /api/v1/auth/tokens", "/api/v1/auth/tokens"); a.OK {
		t.Fatalf("a route open to full credentials only: %+v", a)
	}
	// Attaching and detaching are the hub's own token's: neither the attached token nor one of every scope named
	// outright, with the call scope of the extension itself, gets past the auth; nor an extension's principal past
	// the routes.
	named := h.extTokens.issue("other", "Other", append(extension.EveryScope(), extension.ScopeAgentsPermissionMode, extension.ScopeCallPrefix+"scoped"))
	t.Cleanup(func() { h.extTokens.revoke("other", db.ExtensionTokenStopped) })
	for _, path := range []string{"/api/v1/extensions/scoped/-/attach", "/api/v1/extensions/scoped/-/detach", "/api/v1/extensions/other/-/attach"} {
		for _, tk := range []string{tok, named} {
			if code, out := call(t, mux, "POST", path, tk, source); code != 403 || out["code"] != "permission_denied" || out["scope"] != nil {
				t.Fatalf("%s with an extension's token: %d %v", path, code, out)
			}
		}
		if code, out := call(t, mux, "POST", path, deviceToken("extension:scoped", roleExtension), source); code != 403 {
			t.Fatalf("%s as an extension: %d %v", path, code, out)
		}
	}
	if p := h.extensions.reg.Get("scoped"); !p.Record.AttachedPackage() || p.Record.Remote.RPCSocket != rpcSock {
		t.Fatalf("a refused attach or detach changed it: %+v", p.Record.Remote)
	}
	if !skills() {
		t.Fatal("attached, its skill no longer reaches the assistant")
	}
	if p := h.extensions.reg.Get("scoped"); p.Dir == "" || p.Problem != "" || !p.Record.AttachedPackage() {
		t.Fatalf("attached: %q %q", p.Dir, p.Problem)
	}

	if code, out := callFrom(t, mux, "POST", "/api/v1/extensions/scoped/-/detach", master, "", "[fd00::10]:1234"); code != 403 || out["code"] != "this_machine_only" {
		t.Fatalf("detach from another machine: %d %v", code, out)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/detach", master, ""); code != 200 {
		t.Fatalf("detach: %d %v", code, out)
	}
	ready(false)
	if a := authorize(tok, "POST /api/v1/sessions/{mid}/{sid}/modes", "/api/v1/sessions/m/s/modes"); a.Extension != "" {
		t.Fatalf("detached, the attached program's token still opens: %+v", a)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/detach", master, ""); code != 400 || out["code"] != "detach_failed" {
		t.Fatalf("not attached: %d %v", code, out)
	}
	store.mu.Lock()
	audit := strings.Join(store.audit, "\n")
	store.mu.Unlock()
	if !strings.Contains(audit, "scoped attach ") || !strings.Contains(audit, "scoped detach ") {
		t.Fatalf("audit:\n%s", audit)
	}
}

// The details say where an extension's data is on the manager's machine, the directory its program is given there, to
// the master token over loopback and to the admin UI, and still while its package is attached; never to a device or to
// the master token from another machine, and not for one the manager never gives one: installed from remote:, from
// hub.json, or with no program.
func TestExtensionDetailsDataDirForTheOwnerOnly(t *testing.T) {
	shortHostTimings(t)
	h := newTestManager(t)
	h.store = newExtensionStore()
	startTestExtensions(t, h, ExtensionConfig{ID: "voice", Name: "Voice", Kind: "voice", Socket: "/nonexistent/data-dir-test.sock"})
	mux := adminRoutes(h)
	master := h.cfg.Token
	manifest := installAttachable(t, h, mux)
	ready := func(attached bool) {
		t.Helper()
		waitLong(t, fmt.Sprintf("running (attached %v)", attached), func() bool {
			hp := h.extensions.get("scoped")
			return hp != nil && hp.proc != nil && hp.proc.Ready() && hp.proc.Spec().Attached == attached
		})
	}
	ready(false)
	want := h.extensions.reg.DataDir("scoped")
	if !slices.Contains(h.extensions.get("scoped").proc.Spec().Env, "PERSONA_PLUGIN_DATA="+want) {
		t.Fatalf("its program is given another directory than %s: %v", want, h.extensions.get("scoped").proc.Spec().Env)
	}
	dataDir := func(code int, out map[string]any) any {
		t.Helper()
		if code != 200 {
			t.Fatalf("details: %d %v", code, out)
		}
		return out["data_dir"]
	}
	const details = "/api/v1/extensions/scoped/-/"
	if got := dataDir(call(t, mux, "GET", details, master, "")); got != want {
		t.Fatalf("the master token over loopback: %v, want %s", got, want)
	}
	if got := dataDir(adminCall(t, mux, "GET", "/api/v1/admin/extensions/scoped", "")); got != want {
		t.Fatalf("the admin UI: %v, want %s", got, want)
	}
	if got := dataDir(call(t, mux, "GET", details, deviceToken("dev_phone", roleFull), "")); got != nil {
		t.Fatalf("a phone: %v", got)
	}
	if got := dataDir(callFrom(t, mux, "GET", details, master, "", "[fd00::10]:1234")); got != nil {
		t.Fatalf("the master token from another machine: %v", got)
	}

	dir, err := os.MkdirTemp("/tmp", "att-") // a unix socket path stays short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	rpcSock, httpSock, _, _ := startAttached(t, dir, manifest, "1.0.0")
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/attach", master, `{"source": "remote:rpc=`+rpcSock+`,http=`+httpSock+`"}`); code != 200 {
		t.Fatalf("attach: %d %v", code, out)
	}
	ready(true)
	if got := dataDir(call(t, mux, "GET", details, master, "")); got != want {
		t.Fatalf("attached: %v, want %s", got, want)
	}

	reg := h.extensions.reg
	remote, err := extension.Parse([]byte(fmt.Sprintf(`{"manifest_version":1,"id":"example-remote","name":"Example remote","version":"1.0.0","engines":{"extension_protocol":1},"runtime":{"command":{%q:"bin/remote"}},"activation_events":["on_startup"],"contributes":{"providers":[{"id":"remote-provider","name":"Example","base_url":"https://provider.example.com"}]}}`, runtime.GOOS+"-"+runtime.GOARCH)))
	if err != nil {
		t.Fatal(err)
	}
	reg.RemoteManifest = func(context.Context, extension.Remote) (*extension.Manifest, error) { return remote, nil }
	pv, err := reg.Preview(t.Context(), "remote:rpc="+filepath.Join(dir, "remote.sock"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reg.Install(t.Context(), pv.ID, pv.Digest, "test"); err != nil {
		t.Fatal(err)
	}
	if got := dataDir(call(t, mux, "GET", "/api/v1/extensions/example-remote/-/", master, "")); got != nil {
		t.Fatalf("installed from remote: %v", got)
	}

	if got := dataDir(call(t, mux, "GET", "/api/v1/extensions/voice/-/", master, "")); got != nil {
		t.Fatalf("hub.json's: %v", got)
	}
	declarative := t.TempDir()
	if err := os.WriteFile(filepath.Join(declarative, extension.ManifestFile), []byte(`{"manifest_version": 1, "id": "example-models", "name": "Example Models", "version": "1.0.0",
		"contributes": {"providers": [{"id": "fast", "name": "Fast", "base_url": "https://fast.example.com"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installForTest(t, h, declarative)
	if got := dataDir(call(t, mux, "GET", "/api/v1/extensions/example-models/-/", master, "")); got != nil {
		t.Fatalf("no program: %v", got)
	}
}

// installAttachable installs attachablePackage from the app.
func installAttachable(t *testing.T, h *Manager, mux http.Handler) []byte {
	t.Helper()
	pkg, manifest := attachablePackage(t)
	code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", h.cfg.Token, `{"source": "dir:`+pkg+`"}`)
	if code != 200 {
		t.Fatalf("preview: %d %v", code, pv)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", h.cfg.Token,
		fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	return manifest
}

// An attached package whose program says another version is not used, and the page says to run the package's version
// or detach it to upgrade: an upgrade is refused while it is attached. Its manifest said the package's version, so it
// was asked to initialize: the token that gave it goes with the refusal, and none is left once nothing more reaches it.
func TestAttachedPackageAtAnotherVersion(t *testing.T) {
	shortHostTimings(t)
	h, _ := extensionTestHub(t)
	mux := h.routes()
	manifest := installAttachable(t, h, mux)
	dir, err := os.MkdirTemp("/tmp", "att-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	rpcSock, httpSock, init, stop := startAttached(t, dir, manifest, "1.1.0") // its manifest still says 1.0.0
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/attach", h.cfg.Token, `{"source": "remote:rpc=`+rpcSock+`,http=`+httpSock+`"}`); code != 200 {
		t.Fatalf("attach: %d %v", code, out)
	}
	var v ExtensionView
	waitLong(t, "the version it says", func() bool {
		v = h.extensions.view(t.Context(), h.extensions.reg.Get("scoped"))
		return v.Attached != nil && v.Attached.Version == "1.1.0" && v.Error != ""
	})
	if v.Version != "1.0.0" || !strings.Contains(v.Error, "1.1.0") || !strings.Contains(v.Error, "personad extension detach scoped") ||
		strings.Contains(v.Error, "personad extension upgrade") {
		t.Fatalf("%s: %q", v.Status, v.Error)
	}
	ip := init()
	if ip == nil || !strings.HasPrefix(ip.Hub.Token, extTokenPrefix) {
		t.Fatalf("initialize: %+v", ip)
	}
	stop()
	waitLong(t, "no token once it is refused", func() bool {
		holds, opens := liveToken(h, "scoped", ip.Hub.Token)
		return !holds && !opens
	})
}

// A program on an attached package's sockets that says it is another version (the owner replaced it there) is refused
// before initialize: it gets no token, settings or secrets, and the token of the program before it goes. So is one that
// does not answer manifest, whatever initialize would say.
func TestAttachedPackageReplacedByAnotherVersionGetsNothing(t *testing.T) {
	shortHostTimings(t)
	h, _ := extensionTestHub(t)
	mux := h.routes()
	manifest := installAttachable(t, h, mux)
	dir, err := os.MkdirTemp("/tmp", "att-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	rpcSock, httpSock, init, stop := startAttached(t, dir, manifest, "1.0.0")
	if code, out := call(t, mux, "POST", "/api/v1/extensions/scoped/-/attach", h.cfg.Token, `{"source": "remote:rpc=`+rpcSock+`,http=`+httpSock+`"}`); code != 200 {
		t.Fatalf("attach: %d %v", code, out)
	}
	var ip *extension.InitializeParams
	waitLong(t, "the attached program initialized", func() bool { ip = init(); return ip != nil })
	stop()
	newer := []byte(strings.Replace(string(manifest), `"version": "1.0.0"`, `"version": "1.1.0"`, 1))
	_, _, initNewer, stopNewer := startAttached(t, dir, newer, "1.1.0")
	var v ExtensionView
	waitLong(t, "the other version refused", func() bool {
		v = h.extensions.view(t.Context(), h.extensions.reg.Get("scoped"))
		return v.Attached != nil && v.Attached.Version == "1.1.0" && strings.Contains(v.Error, "1.1.0")
	})
	if got := initNewer(); got != nil {
		t.Fatalf("a program at another version was initialized: token %q, %d secrets", got.Hub.Token, len(got.Secrets))
	}
	waitLong(t, "no token left", func() bool {
		holds, opens := liveToken(h, "scoped", ip.Hub.Token)
		return !holds && !opens
	})
	stopNewer()
	_, _, initSilent, _ := startAttached(t, dir, nil, "1.0.0")
	waitLong(t, "a program that does not answer manifest refused", func() bool {
		v = h.extensions.view(t.Context(), h.extensions.reg.Get("scoped"))
		return strings.Contains(v.Error, "no manifest here")
	})
	if got := initSilent(); got != nil {
		t.Fatalf("a program that does not answer manifest was initialized: token %q, %d secrets", got.Hub.Token, len(got.Secrets))
	}
}

// The every-scope token is an attached package's only: a package installed from remote: and granted hub_api gets no
// token in initialize, as before packages could be attached; an attached one gets every scope but
// agents.permission_mode and models.
func TestOnlyAnAttachedPackageGetsATokenOfEveryScope(t *testing.T) {
	h := newTestManager(t)
	initOf := func(id string, pkg bool) extension.InitializeParams {
		t.Helper()
		kbExtensionSpec(t, h, id, extension.Permissions{HubAPI: true})
		p := h.extensions.reg.Get(id)
		p.Record.Source = extension.SourceInstalled
		dir := t.TempDir()
		p.Record.Remote = &extension.Remote{RPCSocket: filepath.Join(dir, "rpc.sock"), HTTPSocket: filepath.Join(dir, "http.sock"), Package: pkg}
		spec, err := h.extensions.specOf(t.Context(), p, &hubExtension{id: id})
		if err != nil || !spec.Attached || spec.Version != "1.0.0" {
			t.Fatal(id, err, spec.Version)
		}
		ip, err := spec.Init(t.Context())
		if err != nil {
			t.Fatal(id, err)
		}
		return ip
	}
	if ip := initOf("example-remote", false); ip.Hub.Token != "" || len(ip.Hub.Scopes) > 0 {
		t.Fatalf("installed from remote: with hub_api: %q %v", ip.Hub.Token, ip.Hub.Scopes)
	}
	ip := initOf("example-attached", true)
	want := slices.DeleteFunc(extension.EveryScope(), func(s string) bool { return s == extension.ScopeModels })
	if !strings.HasPrefix(ip.Hub.Token, extTokenPrefix) || !slices.Equal(ip.Hub.Scopes, want) {
		t.Fatalf("an attached package with hub_api: %q %v", ip.Hub.Token, ip.Hub.Scopes)
	}
}

// The hub hands the manager a permission mode change and a request with terminal control characters it refused an
// extension's token for its audit, and nothing else.
func TestHubAuditTakesRefusedPermissionModes(t *testing.T) {
	h, store := extensionTestHub(t)
	ctx := t.Context()
	if _, err := h.hubCall(ctx, extwire.HubAudit, json.RawMessage(`{"extension":"scoped","action":"permission_mode_refused",
		"detail":{"machine_id":"m","session_id":"s","reason":"not_opened"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.hubCall(ctx, extwire.HubAudit, json.RawMessage(`{"extension":"scoped","action":"terminal_control_refused",
		"detail":{"route":"POST /api/v1/sessions/{mid}/{sid}/messages","path":"/api/v1/sessions/m/s/messages"}}`)); err != nil {
		t.Fatal(err)
	}
	for _, params := range []string{`{"extension":"scoped","action":"uninstall"}`, `{"action":"permission_mode_refused"}`, `[]`} {
		if _, err := h.hubCall(ctx, extwire.HubAudit, json.RawMessage(params)); err == nil {
			t.Fatalf("%s: recorded", params)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !slices.Equal(store.audit, []string{"scoped permission_mode_refused extension:scoped", "scoped terminal_control_refused extension:scoped"}) {
		t.Fatalf("audit: %q", store.audit)
	}
}

// A refused approval of a hand-off card is recorded on the provider, with who tried as the actor the hub names; an
// actor that is none of the kinds the records use is "unknown". An extension that is no provider here gets none.
func TestHubAuditTakesRefusedHandoffApprovals(t *testing.T) {
	h, store := extensionTestHub(t)
	ctx := t.Context()
	record := func(ext, actor string) error {
		params, _ := json.Marshal(extwire.Audit{Extension: ext, Action: extwire.AuditSecretHandoffRefused, Actor: actor,
			Detail: map[string]any{"interaction_id": "ix_1", "reason": "client"}})
		_, err := h.hubCall(ctx, extwire.HubAudit, params)
		return err
	}
	if err := record("example-vault", "device:d1"); err == nil {
		t.Fatal("recorded on an extension that is not installed")
	}
	mux := h.routes()
	master := h.cfg.Token
	_, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master,
		`{"source": "dir:`+secretProviderPackage(t, "example-vault", "exv", filepath.Join(t.TempDir(), "calls.jsonl"))+`"}`)
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	store.mu.Lock()
	store.audit = nil // the install's own records
	store.mu.Unlock()
	for _, actor := range []string{"device:d1", "extension:other", "hub token", "admin", "device:x\nforged", ""} {
		if err := record("example-vault", actor); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	want := []string{"example-vault secret_handoff_approve_refused device:d1", "example-vault secret_handoff_approve_refused extension:other",
		"example-vault secret_handoff_approve_refused hub token", "example-vault secret_handoff_approve_refused unknown",
		"example-vault secret_handoff_approve_refused unknown", "example-vault secret_handoff_approve_refused unknown"}
	if !slices.Equal(store.audit, want) {
		t.Fatalf("audit: %q", store.audit)
	}
}

// An attached extension reads the assistant's conversations by its own grant only, as one the manager runs does: its
// token's scopes and agents.control do not open them.
func TestAttachedExtensionReadsConversationsByItsOwnGrant(t *testing.T) {
	h := newTestManager(t)
	h.opts.Assistant = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"messages":[]}`), nil
	}
	attached := func(id string, granted extension.Permissions) func(context.Context, string, json.RawMessage) (any, error) {
		kbExtensionSpec(t, h, id, granted)
		p := h.extensions.reg.Get(id)
		dir := t.TempDir()
		p.Record.Remote = &extension.Remote{RPCSocket: filepath.Join(dir, "rpc.sock"), HTTPSocket: filepath.Join(dir, "http.sock")}
		spec, err := h.extensions.specOf(t.Context(), p, &hubExtension{id: id})
		if err != nil || !spec.Attached {
			t.Fatal(id, err)
		}
		return spec.Handle
	}
	reader := attached("example-reader", extension.Permissions{Hub: []string{extension.ScopeAssistantRead}})
	full := attached("example-full", extension.Permissions{HubAPI: true})
	control := attached("example-control", extension.Permissions{Hub: []string{extension.ScopeAgentsControl, extension.ScopeAgentsPermissionMode}})
	params := json.RawMessage(`{"id":"c1"}`)
	for _, read := range []func(context.Context, string, json.RawMessage) (any, error){reader, full} {
		if _, err := read(t.Context(), assistantwire.MethodAssistantMessages, params); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := control(t.Context(), assistantwire.MethodAssistantConversations, params); err == nil {
		t.Fatal("an attached extension without assistant.read read the conversations")
	}
	h.extensions.reg.Get("example-reader").Record.Granted = extension.Permissions{}
	if _, err := reader(t.Context(), assistantwire.MethodAssistantConversations, params); err == nil {
		t.Fatal("an attached extension still reads once its grant is gone")
	}
}
