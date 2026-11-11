package extmgr

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/extwire"
)

// serveScopedExtensionForTest says on its HTTP side what the hub gave it: the token in its environment and the one in
// initialize, with the scopes.
func serveScopedExtensionForTest() {
	ln, err := net.Listen("unix", os.Getenv("PERSONA_EXTENSION_SOCKET"))
	if err != nil {
		os.Exit(1)
	}
	var mu sync.Mutex
	var init extension.HubInfo
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"env": os.Getenv("PERSONA_HUB_TOKEN"), "env_url": os.Getenv("PERSONA_HUB_URL"),
			"init": init.Token, "url": init.URL, "scopes": init.Scopes, "manager": os.Getenv("PERSONA_MANAGER"),
			"init_manager": init.Manager, "config": os.Getenv("PERSONA_PLUGIN_CONFIG")})
	}))
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "scoped", Version: cmp.Or(os.Getenv("PERSONA_TEST_PLUGIN_VERSION"), "1.0.0"),
		Init: func(_ context.Context, ip extension.InitializeParams, _ *pserve.Hub) (map[string]any, error) {
			mu.Lock()
			init = ip.Hub
			mu.Unlock()
			return nil, nil
		}})
}

func scopedPackage(t *testing.T, hub string) string { return scopedPackageOf(t, `"hub": `+hub) }

// scopedPackageOf is the scoped extension asking for perms (the inside of its permissions).
func scopedPackageOf(t *testing.T, perms string) string { return scopedPackageAt(t, perms, "1.0.0") }

// scopedPackageAt is scopedPackageOf at version.
func scopedPackageAt(t *testing.T, perms, version string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "scoped"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=scoped PERSONA_TEST_PLUGIN_VERSION="+version+" exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": "scoped", "name": "Scoped", "version": %q,
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/scoped"}, "http": true},
		"activation_events": ["on_startup"], "contributes": {"api": [{"id": "x", "prefix": "scopedx"}]},
		"configuration": {"type":"object","properties":{"example":{"type":"string","title":"Example","default":"before-rpc"}}},
		"permissions": {%s}}`, version, runtime.GOOS+"-"+runtime.GOARCH, perms)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

// An extension granted scopes gets a token of those (the same in its environment and in initialize) that opens their
// routes and no other; it is made anew each start and stops working when the extension is switched off or removed.
func TestExtensionScopedToken(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token

	code, pv := call(t, mux, "POST", "/api/v1/extensions/-/preview", master, `{"source": "dir:`+scopedPackage(t, `["kb.read", "extensions.call:other"]`)+`"}`)
	if code != 200 {
		t.Fatalf("preview: %d %v", code, pv)
	}
	if b, _ := json.Marshal(pv["permissions"]); !strings.Contains(string(b), "读知识库") || strings.Contains(string(b), "完整权限") {
		t.Fatalf("the install screen lists the scopes: %s", b)
	}
	if code, out := call(t, mux, "POST", "/api/v1/extensions/-/install", master,
		fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	given := func() map[string]any {
		var out map[string]any
		waitLong(t, "the extension up with its token", func() bool {
			hp := h.extensions.get("scoped")
			if hp == nil || hp.proc == nil || !hp.proc.Ready() {
				return false
			}
			_, out = call(t, mux, "GET", "/api/v1/extensions/scoped/whoami", master, "")
			return out["init"] != nil && out["init"] != ""
		})
		return out
	}
	got := given()
	tok, _ := got["env"].(string)
	if !strings.HasPrefix(tok, extTokenPrefix) || got["init"] != tok || got["url"] != got["env_url"] || got["url"] == "" {
		t.Fatalf("one token, in the environment and in initialize: %v", got)
	}
	if b, _ := json.Marshal(got["scopes"]); string(b) != `["kb.read","extensions.call:other"]` {
		t.Fatalf("scopes: %s", b)
	}
	if got["url"] != hubLocalURL(h.cfg.Listen) || got["manager"] != "" || got["init_manager"] != "" {
		t.Fatalf("the built-in manager's extension reaches the hub on this machine, and is told no manager: %v", got)
	}
	if got["config"] != `{"example":"before-rpc"}` {
		t.Fatalf("configuration must reach the supervisor before initialize, without secrets: %v", got["config"])
	}

	// On the hub's own routes the hub asks the manager (extensions.authorize, with the route's pattern on its mux);
	// on the manager's the hub's auth in routes() asks the same.
	hubRoute := func(pattern, path string) extwire.Authorized {
		method, _, _ := strings.Cut(pattern, " ")
		return h.authorize(extwire.Authorize{Hash: hashToken(tok), Pattern: pattern, Method: method, Path: path})
	}
	if a := hubRoute("GET /api/v1/kb-search", "/api/v1/kb-search"); !a.OK || a.Extension != "scoped" {
		t.Fatalf("kb.read opens the knowledge base's search: %+v", a)
	}
	for _, c := range []struct{ pattern, path, scope string }{
		{"GET /api/v1/sessions", "/api/v1/sessions", "agents.read"},
		{"PUT /api/v1/kb/{kind}/{id}", "/api/v1/kb/people/x", "kb.write"},
		{"POST /api/v1/sessions/{mid}/{sid}/messages", "/api/v1/sessions/m/s/messages", "agents.control"},
		{"GET /api/v1/ws", "/api/v1/ws", ""},
		{"POST /api/v1/auth/tokens", "/api/v1/auth/tokens", ""},
	} {
		if a := hubRoute(c.pattern, c.path); a.OK || a.Extension != "scoped" || a.Scope != c.scope {
			t.Fatalf("%s: %+v, want refused (scope %q)", c.pattern, a, c.scope)
		}
	}
	for _, c := range []struct{ method, path, scope string }{
		{"GET", "/api/v1/extensions/third/x", "extensions.call:third"},
		{"GET", "/api/v1/extensions", ""},
		{"GET", "/api/v1/extensions/-/market-settings", ""},
		{"PUT", "/api/v1/extensions/-/market-settings", ""},
		{"GET", "/api/v1/plugins", ""},
		{"GET", "/api/v1/extensions/other/-/", ""},
		{"GET", "/api/v1/scopedx/y", ""},
	} {
		code, out := call(t, mux, c.method, c.path, tok, "")
		scope, _ := out["scope"].(string)
		if code != http.StatusForbidden || out["code"] != "permission_denied" || scope != c.scope {
			t.Fatalf("%s %s: %d %v, want 403 permission_denied (scope %q)", c.method, c.path, code, out, c.scope)
		}
	}
	if code, out := call(t, mux, "GET", "/api/v1/extensions/other/x", tok, ""); code == http.StatusForbidden {
		t.Fatalf("extensions.call:other opens the other extension's HTTP: %d %v", code, out)
	}
	// (An extension's token from the internet is refused by the hub before it asks: hub/http.go auth.)
	gone := func(tok string) bool {
		return h.authorize(extwire.Authorize{Hash: hashToken(tok), Pattern: "GET /api/v1/kb-search", Method: "GET", Path: "/api/v1/kb-search"}).Extension == ""
	}

	// Switched off: its token goes. On again: a new one.
	if code, out := call(t, mux, "PUT", "/api/v1/extensions/scoped/-/enabled", master, `{"enabled": false}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if !gone(tok) {
		t.Fatal("switched off, its token still opens")
	}
	if code, out := call(t, mux, "PUT", "/api/v1/extensions/scoped/-/enabled", master, `{"enabled": true}`); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	again := given()
	tok2, _ := again["env"].(string)
	if tok2 == "" || tok2 == tok || again["init"] != tok2 {
		t.Fatalf("a new start, a new token: %v (was %s)", again, tok)
	}
	if !gone(tok) {
		t.Fatal("the old token after a new start")
	}

	// Uninstalled: gone too.
	if code, out := call(t, mux, "DELETE", "/api/v1/extensions/scoped/-/", master, ""); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if !gone(tok2) {
		t.Fatal("uninstalled, its token still opens")
	}
}

// What the install screen says of the hub's token, and what an upgrade asking for more must be agreed to.
func TestHubScopesConsent(t *testing.T) {
	scoped := extension.Permissions{Hub: []string{"kb.read"}}
	full := extension.Permissions{HubAPI: true}
	both := extension.Permissions{HubAPI: true, Hub: []string{"kb.read"}}
	mode := extension.Permissions{Hub: []string{extension.ScopeAgentsPermissionMode}}
	for _, c := range []struct {
		name      string
		have, ask extension.Permissions
		covers    bool
	}{
		{"same scope", scoped, scoped, true},
		{"one more scope", scoped, extension.Permissions{Hub: []string{"kb.read", "inbox.read"}}, false},
		{"fewer scopes", extension.Permissions{Hub: []string{"kb.read", "inbox.read"}}, scoped, true},
		{"the full token covers scopes", full, scoped, true},
		{"scopes do not cover the full token", scoped, full, false},
		{"hub_api with scopes is the scopes", scoped, both, true},
		{"from the full token to hub_api with scopes", full, both, true},
		{"the full token covers itself", full, full, true},
		{"the full token does not cover the permission mode", full, mode, false},
		{"the full token does not cover the permission mode among others", full,
			extension.Permissions{Hub: []string{"kb.read", extension.ScopeAgentsPermissionMode}}, false},
		{"the permission mode covers itself", mode, mode, true},
		{"other scopes do not cover the permission mode", extension.Permissions{Hub: extension.EveryScope()}, mode, false},
	} {
		if got := c.have.Covers(c.ask); got != c.covers {
			t.Errorf("%s: Covers = %v", c.name, got)
		}
	}
	if !full.FullHub() || both.FullHub() || scoped.FullHub() {
		t.Fatal("FullHub")
	}
}

// What the manager checks of an extension's token on a route (extensions.authorize): the table's scope, and an
// extension's own HTTP by the extension it names. The table over every hub route is the hub's test (TestRouteScopes).
func TestScopeOpens(t *testing.T) {
	for _, c := range []struct {
		scopes        []string
		pattern, path string
		ok            bool
		scope         string
	}{
		{[]string{extension.ScopeKBRead}, "GET /api/v1/kb/{kind}", "/api/v1/kb/chats", true, extension.ScopeKBRead},
		{nil, "GET /api/v1/kb/{kind}", "/api/v1/kb/chats", false, extension.ScopeKBRead},
		{[]string{extension.ScopeKBRead}, "PUT /api/v1/kb/{kind}/{id}", "/api/v1/kb/chats/x", false, extension.ScopeKBWrite},
		{[]string{extension.ScopeCallPrefix + "other"}, "/api/v1/extensions/{id}/", "/api/v1/extensions/other/x", true, extension.ScopeCallPrefix + "other"},
		{[]string{extension.ScopeCallPrefix + "other"}, "/api/v1/extensions/{id}/", "/api/v1/extensions/third/x", false, extension.ScopeCallPrefix + "third"},
		{[]string{extension.ScopeCallPrefix + "other"}, "/api/v1/extensions/{id}/", "/api/v1/extensions/other/-/config", false, ""},
		{[]string{extension.ScopeKBRead}, "POST /api/v1/extensions/-/install", "/api/v1/extensions/-/install", false, ""},
	} {
		if ok, scope := scopeOpens(c.scopes, c.pattern, c.path); ok != c.ok || scope != c.scope {
			t.Errorf("%v on %s %s: %v %q, want %v %q", c.scopes, c.pattern, c.path, ok, scope, c.ok, c.scope)
		}
	}
	// The hub asks with its pattern; a relayed route is matched on the manager's own mux.
	h := newTestManager(t)
	h.routes()
	tok := h.extTokens.issue("example-x", "X", []string{extension.ScopeCallPrefix + "other"})
	a := h.authorize(extwire.Authorize{Hash: hashToken(tok), Pattern: "/api/v1/extensions/", Method: "GET", Path: "/api/v1/extensions/other/page"})
	if !a.OK || a.Extension != "example-x" || a.Scope != extension.ScopeCallPrefix+"other" {
		t.Fatalf("%+v", a)
	}
	if a := h.authorize(extwire.Authorize{Hash: hashToken("pext_nope"), Pattern: "GET /api/v1/kb/{kind}", Method: "GET", Path: "/api/v1/kb/chats"}); a.Extension != "" || a.OK {
		t.Fatalf("an unknown token: %+v", a)
	}
}

// An extension's token is checked on the route its request is served on. The hub asks with the escaped path it relays
// (an older one with the decoded path): an escaped dot segment cleaned away in one and kept in the other would check
// one route and serve another, and an escaped "-" is the hub's own segment (#5182).
func TestAuthorizeEscapedPaths(t *testing.T) {
	h := newTestManager(t)
	h.routes()
	tok := h.extTokens.issue("example-x", "X", []string{extension.ScopeAgentsRead, extension.ScopeCallPrefix + "other"})
	for _, c := range []struct {
		path string
		ok   bool
	}{
		{"/api/v1/extensions/other/page", true},
		{"/api/v1/extensions/other/a%2Fb", true},
		{"/api/v1/extensions/third/%2E%2E/%2E%2E/agents", false},
		{"/api/v1/extensions/third/../../agents", false}, // as an older hub asks
		{"/api/v1/extensions/third/x%2F..%2F..%2F..%2Fagents", false},
		{"/api/v1/extensions/other/%2E%2E/third/page", false},
		{"/api/v1/extensions/other/%2D/config", false},
		{"/api/v1/extensions/other/%2d", false},
		{"/api/v1/extensions/%2D/install", false},
	} {
		a := h.authorize(extwire.Authorize{Hash: hashToken(tok), Pattern: "/api/v1/extensions/", Method: "GET", Path: c.path})
		if a.OK != c.ok || a.Extension != "example-x" {
			t.Errorf("%s: %+v, want ok %v", c.path, a, c.ok)
		}
	}
	for path, want := range map[string]string{
		"/api/v1/extensions/other/x":        extension.ScopeCallPrefix + "other",
		"/api/v1/extensions/ot%68er/x":      extension.ScopeCallPrefix + "other",
		"/api/v1/extensions/a%2Fb/x":        extension.ScopeCallPrefix + "a/b",
		"/api/v1/extensions/other/%2D/conf": "",
		"/api/v1/extensions/other/-":        "",
		"/api/v1/extensions/%2D/x":          "",
		"/api/v1/extensions/other%zz/x":     "",
	} {
		if got := extwire.ExtensionCallScope(path); got != want {
			t.Errorf("ExtensionCallScope(%s) = %q, want %q", path, got, want)
		}
	}
}
