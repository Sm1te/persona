// Package depguard checks what a program links (go list -deps), for the tests that keep the hub's and the manager's
// programs apart (cmd/persona-hub, cmd/persona-manager; docs/server/server.md "Programs").
package depguard

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Module is the server's module path.
const Module = "github.com/nwverse/persona/server/"

// Platforms are the ones the guards check: the four the programs are released for, and Windows, where the server
// builds too (CI's Windows build).
var Platforms = []string{"linux/amd64", "linux/arm64", "darwin/arm64", "darwin/amd64", "windows/amd64"}

// HubOwn are the hub's own packages: persona-manager links none of them. A contract between the hub and a helper of
// its own (relayproto with the push relay, shareproof with the sharing ingress, passwordpake with the web app's
// password WASM) is the hub's too.
var HubOwn = []string{"cmd/persona-hub", "internal/hub", "internal/hubcli", "internal/hubedge", "internal/apns",
	"internal/mdns", "internal/passwordpake", "internal/proxyproto", "internal/pushcrypto", "internal/relay",
	"internal/relayproto", "internal/shareproof", "internal/webasset"}

// Shared are the packages both programs link (the wire types, the database, the configuration, identity, the update
// code and its proxy, netproxy): the hub links these and its own, nothing else; a new package the hub needs is put in
// one of the two lists on purpose, never one of the manager's.
var Shared = []string{"internal/acceptloop", "internal/aiwire", "internal/apierr", "internal/assistantwire",
	"internal/bus", "internal/channel", "internal/clitext", "internal/compat", "internal/connectorwire", "internal/db",
	"internal/devicewire", "internal/dialogwire", "internal/errreport", "internal/extwire", "internal/i18n",
	"internal/identity", "internal/kbwire", "internal/lazyre", "internal/link", "internal/managerconf", "internal/memtrim", "internal/netaddr",
	"internal/netproxy", "internal/nodeproto", "internal/osproc", "internal/pgdocker", "internal/preview", "internal/procinfo", "internal/prompts",
	"internal/proto", "internal/rpc", "internal/screen", "internal/secretkey", "internal/sessionlist", "internal/sessionorigin", "internal/streams",
	"internal/textfmt", "internal/transcript", "internal/update", "internal/workspaceboard"}

// HubExceptions are manager-side packages the hub still links, and may only shrink: internal/extension (and what it
// pulls in) for the looks the hub serves and the database's extension types, until extensions are a part of their own.
var HubExceptions = []string{"internal/extension", "internal/extension/egress", "internal/extension/sandbox",
	"internal/extension/sourcevalidate"}

// Deps are the module's packages the package in dir links on goos/goarch, relative to the module
// ("internal/hub"). It skips the test without a go tool.
func Deps(t *testing.T, dir, goos, goarch string) []string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool:", err)
	}
	cmd := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps (%s/%s): %v", goos, goarch, err)
	}
	var deps []string
	for _, dep := range strings.Fields(string(out)) {
		if rel, ok := strings.CutPrefix(dep, Module); ok {
			deps = append(deps, rel)
		}
	}
	return deps
}

// Under: pkg is p or a package under it.
func Under(pkg, p string) bool { return pkg == p || strings.HasPrefix(pkg, p+"/") }
