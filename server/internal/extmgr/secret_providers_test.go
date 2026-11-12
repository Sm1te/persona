package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	pserve "github.com/nwverse/persona/server/internal/extension/serve"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// secretValueForTest is the one value the fake provider gives: no record, log or event may carry it.
const secretValueForTest = "synthetic-secret-value-7f3a9c"

// serveSecretProviderExtensionForTest resolves exv:// references by their last segment (ok, missing, signed_out,
// limited, refuse, big, boom), and writes each call's method and params, and each secrets.used event, as a line of
// JSON to SECRETS_OUT.
func serveSecretProviderExtensionForTest() {
	out := os.Getenv("SECRETS_OUT")
	var mu sync.Mutex
	note := func(method string, params json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			b, _ := json.Marshal(map[string]any{"method": method, "params": params})
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "example-vault", Version: "1.0.0",
		Namespaces: map[string]pserve.Handler{
			"secrets": func(_ context.Context, method string, params json.RawMessage) (any, error) {
				note(method, params)
				var p struct {
					Reference string `json:"reference"`
				}
				_ = json.Unmarshal(params, &p)
				last := p.Reference[strings.LastIndex(p.Reference, "/")+1:]
				switch method {
				case "used":
					return nil, nil
				case "describe":
					if last == "long" { // a detail on several lines, longer than a card's line
						return wire.SecretsDescribeResult{Title: "Item › long", Detail: "vault Example\n" + strings.Repeat("and more words ", 10)}, nil
					}
					return wire.SecretsDescribeResult{Title: "Item › " + last, Detail: "vault Example"}, nil
				case "resolve":
				default:
					return nil, pserve.UnknownMethod(method)
				}
				switch last {
				case "ok":
					return wire.SecretsResolveResult{Value: secretValueForTest}, nil
				case "missing":
					return nil, &rpc.Error{Code: "not_found", Message: "no such item"}
				case "signed_out":
					return nil, &rpc.Error{Code: "not_signed_in", Message: "token revoked"}
				case "limited":
					return nil, &rpc.Error{Code: "rate_limited", Message: "slow down", Data: json.RawMessage(`{"retry_after": 30}`)}
				case "refuse":
					return nil, &rpc.Error{Code: "refused", Message: "policy\nsays no"}
				case "big":
					return wire.SecretsResolveResult{Value: strings.Repeat("x", extension.SecretMost+1)}, nil
				}
				return nil, errors.New("the vault fell over")
			}}})
}

func secretProviderPackage(t *testing.T, id, scheme, out string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "vault"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=secrets exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": %q, "name": "Example Vault", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/vault"}, "env": {"SECRETS_OUT": %q}},
		"contributes": {"secret_providers": [{"id": "vault", "name": "Example Vault", "schemes": [%q]}]}}`,
		id, runtime.GOOS+"-"+runtime.GOARCH, out, scheme)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

func TestSecretProviders(t *testing.T) {
	h, store := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	ctx := context.Background()
	purpose := wire.SecretPurpose{Kind: "extension", ID: "example-notes"}
	code := func(err error) string {
		var se *SecretError
		if !errors.As(err, &se) {
			return fmt.Sprintf("not a SecretError: %v", err)
		}
		return se.Code
	}

	if _, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings"); code(err) != "no_provider" {
		t.Fatalf("no provider installed: %v", err)
	}
	if _, err := h.ResolveSecret(ctx, "https://example.com/x", purpose, "settings"); code(err) != "not_reference" {
		t.Fatalf("an address is not a reference: %v", err)
	}

	out := filepath.Join(t.TempDir(), "calls.jsonl")
	_, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+secretProviderPackage(t, "example-vault", "exv", out)+`"}`)
	if !strings.Contains(fmt.Sprint(pv["permissions"]), "secret_provider") || !strings.Contains(fmt.Sprint(pv["permissions"]), "exv://") {
		t.Fatalf("the install page says it supplies exv:// values: %v", pv["permissions"])
	}
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	waitLong(t, "the provider found", func() bool {
		p, ok := h.SecretProviderOf("exv://Example/item/ok")
		return ok && p.Extension == "example-vault" && p.Name == "Example Vault"
	})

	v, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings")
	if err != nil || v != secretValueForTest {
		t.Fatalf("resolved: %q %v", v, err)
	}
	for ref, want := range map[string]string{
		"exv://Example/item/missing": "not_found", "exv://Example/item/signed_out": "not_signed_in",
		"exv://Example/item/limited": "rate_limited", "exv://Example/item/refuse": "refused",
		"exv://Example/item/big": "too_long", "exv://Example/item/boom": "failed",
	} {
		_, err := h.ResolveSecret(ctx, ref, purpose, "settings")
		if code(err) != want {
			t.Fatalf("%s: want %s, got %v", ref, want, err)
		}
		if strings.Contains(err.Error(), "\n") {
			t.Fatalf("%s: the provider's words on one line: %q", ref, err.Error())
		}
		var se *SecretError
		if errors.As(err, &se) && want == "rate_limited" && se.RetryAfter != 30 {
			t.Fatalf("the provider's wait is kept: %v", se.RetryAfter)
		}
	}
	var se *SecretError
	_, err = h.ResolveSecret(ctx, "exv://Example/item/refuse", purpose, "settings")
	if !errors.As(err, &se) || !strings.Contains(se.Text(i18n.ZhHans), "Example Vault 拒绝了") || !strings.Contains(se.Text(i18n.En), "policy says no") {
		t.Fatalf("the refusal reads in the asker's language with the provider's words: %v", err)
	}

	d, err := h.DescribeSecret(ctx, "exv://Example/item/ok")
	if err != nil || d.Title != "Item › ok" || d.Detail != "vault Example" || d.ProviderName != "Example Vault" {
		t.Fatalf("described: %+v %v", d, err)
	}
	if _, err := h.DescribeSecret(ctx, "exv://Example/item/ok"); err != nil {
		t.Fatal(err)
	}
	// A provider's detail is cut by the host, whatever it sent: one line of secretDetailMost characters and "…".
	d, err = h.DescribeSecret(ctx, "exv://Example/item/long")
	if err != nil || strings.ContainsAny(d.Detail, "\n\t") || !strings.HasPrefix(d.Detail, "vault Example and more words") ||
		!strings.HasSuffix(d.Detail, "…") || utf8.RuneCountInString(d.Detail) != secretDetailMost+1 {
		t.Fatalf("a long detail: %q %v", d.Detail, err)
	}

	// What the provider was told, and what the hub recorded: never the value.
	waitLong(t, "the used event", func() bool {
		raw, _ := os.ReadFile(out)
		return strings.Contains(string(raw), `"method":"used"`)
	})
	raw, _ := os.ReadFile(out)
	if strings.Contains(string(raw), secretValueForTest) {
		t.Fatal("the provider is never handed a value")
	}
	if n := strings.Count(string(raw), `"method":"describe"`); n != 2 { // the ok item once (asked twice), the long one once
		t.Fatalf("a description is kept a while: asked %d times", n)
	}
	var used struct {
		Params wire.SecretsUsedEvent `json:"params"`
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.Contains(line, `"method":"used"`) {
			_ = json.Unmarshal([]byte(line), &used)
		}
	}
	if used.Params.Reference != "exv://Example/item/ok" || used.Params.Purpose != purpose || used.Params.ApprovedBy != "settings" || used.Params.Contribution != "vault" {
		t.Fatalf("secrets.used: %+v", used.Params)
	}
	resolved, failed := 0, 0
	for _, a := range store.audits() {
		if a.plugin != "example-vault" {
			continue
		}
		if strings.Contains(string(a.detail), secretValueForTest) {
			t.Fatalf("an audit row carries the value: %s", a.detail)
		}
		switch a.action {
		case "secret_resolved":
			resolved++
		case "secret_resolve_failed":
			failed++
		default:
			continue
		}
		if a.actor != "settings" || !strings.Contains(string(a.detail), `"reference":"exv://Example/item/`) {
			t.Fatalf("who approved it is the actor, the reference and purpose the detail: %q %s", a.actor, a.detail)
		}
	}
	if resolved != 1 || failed != 7 {
		t.Fatalf("each resolution recorded on the provider: %d resolved, %d failed", resolved, failed)
	}

	// Too many in a minute: refused before reaching it.
	h.extensions.resolves.mu.Lock()
	h.extensions.resolves.most = 8 // the eight above
	h.extensions.resolves.mu.Unlock()
	if _, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings"); code(err) != "rate_limited" {
		t.Fatalf("limited: %v", err)
	}

	// A second extension may not claim the same scheme.
	_, pv2 := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+secretProviderPackage(t, "example-vault-two", "exv", out)+`"}`)
	if c, _ := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv2["preview_id"], pv2["permissions_digest"])); c == 200 {
		t.Fatal("a scheme another extension claims fails the install")
	}

	// Switched off: no provider any more.
	if c, _ := call(t, mux, "PUT", "/api/v1/extensions/example-vault/-/enabled", master, `{"enabled": false}`); c != 200 {
		t.Fatalf("switch off: %d", c)
	}
	waitLong(t, "the provider gone", func() bool { _, ok := h.SecretProviderOf("exv://Example/item/ok"); return !ok })
	if _, err := h.ResolveSecret(ctx, "exv://Example/item/ok", purpose, "settings"); code(err) != "no_provider" {
		t.Fatalf("switched off: %v", err)
	}
}

// serveSecretUserExtensionForTest is an extension with a secret "key": it writes the secrets its initialize brought
// to USER_OUT (one JSON line a start) and checks a new key (settings_check), refusing any with "refuse" in it.
func serveSecretUserExtensionForTest() {
	out := os.Getenv("USER_OUT")
	_ = pserve.Serve(context.Background(), os.Stdin, os.Stdout, pserve.Program{ID: "example-user", Version: "1.0.0",
		Init: func(_ context.Context, p extension.InitializeParams, _ *pserve.Hub) (map[string]any, error) {
			if f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
				b, _ := json.Marshal(p.Secrets)
				_, _ = f.Write(append(b, '\n'))
				_ = f.Close()
			}
			return nil, nil
		},
		Namespaces: map[string]pserve.Handler{
			"settings": func(_ context.Context, method string, params json.RawMessage) (any, error) {
				var in struct {
					Secrets map[string]string `json:"secrets"`
				}
				_ = json.Unmarshal(params, &in)
				if strings.Contains(in.Secrets["key"], "refuse") {
					return map[string]any{"ok": false, "error": "the gateway refused " + in.Secrets["key"]}, nil
				}
				return map[string]any{"ok": true}, nil
			}}})
}

func secretUserPackage(t *testing.T, out string) string {
	dir := t.TempDir()
	self, _ := os.Executable()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "user"), []byte("#!/bin/sh\nPERSONA_TEST_PLUGIN=secretuser exec "+self+" \"$@\"\n"), 0o755)
	manifest := fmt.Sprintf(`{"manifest_version": 1, "id": "example-user", "name": "Example User", "version": "1.0.0",
		"engines": {"plugin_protocol": 1}, "runtime": {"command": {%q: "bin/user"}, "env": {"USER_OUT": %q}},
		"activation_events": ["on_startup"], "settings_check": true,
		"contributes": {"ui": [{"id": "page", "title": "Gateway", "placement": "extension_page"}]},
		"secrets": [{"name": "key", "title": "Gateway key", "required": true}]}`, runtime.GOOS+"-"+runtime.GOARCH, out)
	_ = os.WriteFile(filepath.Join(dir, extension.ManifestFile), []byte(manifest), 0o644)
	return dir
}

// A reference saved as another extension's secret: resolved each time that extension starts, shown as "From …",
// listed by its provider, and the extension waits for its provider when that is off.
func TestSecretReferencesInExtensionSecrets(t *testing.T) {
	h, store := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	install := func(dir string) {
		t.Helper()
		c, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+dir+`"}`)
		if c != 200 {
			t.Fatalf("preview %s: %d %v", dir, c, pv)
		}
		if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
			t.Fatalf("install %s: %d %v", dir, c, body)
		}
	}
	calls := filepath.Join(t.TempDir(), "calls.jsonl")
	starts := filepath.Join(t.TempDir(), "starts.jsonl")
	install(secretProviderPackage(t, "example-vault", "exv", calls))
	install(secretUserPackage(t, starts))
	status := func() string {
		_, d := call(t, mux, "GET", "/api/v1/extensions/example-user/-/", master, "")
		return fmt.Sprint(d["status"], d["needs_setup"])
	}
	if s := status(); !strings.Contains(s, "needs_setup") {
		t.Fatalf("no key yet: %s", s)
	}

	put := func(body string) (int, map[string]any) {
		return call(t, mux, "PUT", "/api/v1/extensions/example-user/-/secrets/key", master, body)
	}
	if c, _ := put(`{"value": "x", "reference": "exv://Example/item/ok"}`); c != 400 {
		t.Fatalf("a value and a reference: %d", c)
	}
	if c, body := put(`{"reference": "nop://Example/item/ok"}`); c != 400 || !strings.Contains(fmt.Sprint(body), "nop://") {
		t.Fatalf("a scheme no extension supplies: %d %v", c, body)
	}
	if c, body := put(`{"reference": "https://example.com/key"}`); c != 400 {
		t.Fatalf("an address is not a reference: %d %v", c, body)
	}
	if c, body := put(`{"reference": "exv://Example/item/ok"}`); c != 200 {
		t.Fatalf("saved: %d %v", c, body)
	}
	waitLong(t, "started with the value", func() bool {
		raw, _ := os.ReadFile(starts)
		return strings.Contains(string(raw), `"key":"`+secretValueForTest+`"`)
	})

	_, d := call(t, mux, "GET", "/api/v1/extensions/example-user/-/", master, "")
	shown := fmt.Sprint(d["secrets"])
	if !strings.Contains(shown, "exv://Example/item/ok") || !strings.Contains(shown, "Example Vault") || !strings.Contains(shown, "Item › ok · vault Example") ||
		strings.Contains(shown, secretValueForTest) || strings.Contains(shown, "hint") {
		t.Fatalf("the field reads From Example Vault: the reference and what it names, never the value: %s", shown)
	}
	_, pd := call(t, mux, "GET", "/api/v1/extensions/example-vault/-/", master, "")
	if users := fmt.Sprint(pd["secret_users"]); !strings.Contains(users, "example-user") || !strings.Contains(users, "Gateway key") || !strings.Contains(users, "exv://Example/item/ok") {
		t.Fatalf("the provider lists who uses its references: %s", users)
	}

	// Checked as the value it stands for, now that it runs: a refused one is not saved.
	if c, body := put(`{"reference": "exv://Example/item/missing"}`); c != 400 || strings.Contains(fmt.Sprint(body), secretValueForTest) {
		t.Fatalf("a reference that does not resolve is not saved: %d %v", c, body)
	}

	// The provider off: the extension waits for it; on again: it runs.
	if c, _ := call(t, mux, "PUT", "/api/v1/extensions/example-vault/-/enabled", master, `{"enabled": false}`); c != 200 {
		t.Fatalf("switch the provider off: %d", c)
	}
	waitLong(t, "waiting for its provider", func() bool { return strings.Contains(status(), "exv://") })
	if c, _ := call(t, mux, "PUT", "/api/v1/extensions/example-vault/-/enabled", master, `{"enabled": true}`); c != 200 {
		t.Fatalf("switch the provider on: %d", c)
	}
	waitLong(t, "running again", func() bool { return !strings.Contains(status(), "needs_setup") })

	var setRef, resolved bool
	for _, a := range store.audits() {
		if strings.Contains(string(a.detail), secretValueForTest) {
			t.Fatalf("an audit row carries the value: %s %s", a.action, a.detail)
		}
		if a.plugin == "example-user" && a.action == "secret_set" && strings.Contains(string(a.detail), `"reference":"exv://Example/item/ok"`) {
			setRef = true
		}
		if a.plugin == "example-vault" && a.action == "secret_resolved" && a.actor == "settings" && strings.Contains(string(a.detail), `"id":"example-user"`) {
			resolved = true
		}
	}
	if !setRef || !resolved {
		t.Fatalf("saving the reference and each resolution are recorded: set %v resolved %v", setRef, resolved)
	}
}

// A launcher key that is a reference: kept for secretKeep per launcher and computer, dropped when the launcher's
// settings change, when its provider is switched off and when a resolution of that reference fails; the provider's
// details list the launchers holding its references.
func TestLauncherSecretsKept(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "calls.jsonl")
	_, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master, `{"source": "dir:`+secretProviderPackage(t, "example-vault", "exv", out)+`"}`)
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	waitLong(t, "the provider running", func() bool {
		_, hp, err := h.extensions.secretProvider("exv://Example/item/ok")
		return err == nil && hp != nil
	})
	const ref = "exv://Example/item/ok"
	resolves := func() (n int) {
		raw, _ := os.ReadFile(out)
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"method":"resolve"`) {
				n++
			}
		}
		return n
	}
	get := func(launcher, machine string) {
		t.Helper()
		if v, err := h.LauncherSecret(ctx, ref, launcher, machine); err != nil || v != secretValueForTest {
			t.Fatalf("%s on %q: %q %v", launcher, machine, v, err)
		}
	}

	get("gateway", "")
	get("gateway", "")
	if n := resolves(); n != 1 {
		t.Fatalf("kept after the first: %d resolutions", n)
	}
	raw, _ := os.ReadFile(out)
	if !strings.Contains(string(raw), `"purpose":{"kind":"launcher","id":"gateway"}`) {
		t.Fatalf("the provider is told which launcher: %s", raw)
	}
	get("gateway", "mac")
	if n := resolves(); n != 2 || !strings.Contains(readFile(t, out), `"machine":"mac"`) {
		t.Fatalf("each computer's use is its own: %d", n)
	}

	h.ForgetLauncherSecrets("gateway") // its settings changed
	get("gateway", "")
	if n := resolves(); n != 3 {
		t.Fatalf("forgotten: %d", n)
	}

	h.extensions.kept.mu.Lock() // secretKeep later
	for k, v := range h.extensions.kept.kept {
		v.at = v.at.Add(-secretKeep)
		h.extensions.kept.kept[k] = v
	}
	h.extensions.kept.mu.Unlock()
	get("gateway", "")
	if n := resolves(); n != 4 {
		t.Fatalf("expired: %d", n)
	}

	// Any resolution of the reference that fails drops the kept copies (here: refused before reaching the provider).
	h.extensions.resolves.mu.Lock()
	h.extensions.resolves.most = 1
	h.extensions.resolves.mu.Unlock()
	if _, err := h.ResolveSecret(ctx, ref, wire.SecretPurpose{Kind: "extension", ID: "example-user"}, "settings"); err == nil {
		t.Fatal("limited")
	}
	h.extensions.resolves.mu.Lock()
	h.extensions.resolves.most = secretResolvesMost
	h.extensions.resolves.mu.Unlock()
	get("gateway", "")
	if n := resolves(); n != 5 {
		t.Fatalf("after a failure: %d", n)
	}

	// The provider's details list the launchers holding its references, not others'.
	h.opts.LauncherReferences = func(context.Context) []LauncherReference {
		return []LauncherReference{{Launcher: "gateway", LauncherName: "Gateway", Reference: ref},
			{Launcher: "gateway", LauncherName: "Gateway", Machine: "mac", Reference: ref},
			{Launcher: "other", LauncherName: "Other", Reference: "nop://Example/item/ok"}}
	}
	_, d := call(t, mux, "GET", "/api/v1/extensions/example-vault/-/", master, "")
	users, _ := json.Marshal(d["secret_users"])
	if !strings.Contains(string(users), `{"kind":"launcher","launcher":"gateway","launcher_name":"Gateway","reference":"exv://Example/item/ok"}`) ||
		!strings.Contains(string(users), `"machine":"mac"`) || strings.Contains(string(users), "nop://") {
		t.Fatalf("secret_users: %s", users)
	}

	// Switched off: nothing kept answers for it.
	if c, _ := call(t, mux, "PUT", "/api/v1/extensions/example-vault/-/enabled", master, `{"enabled": false}`); c != 200 {
		t.Fatalf("switch off: %d", c)
	}
	waitLong(t, "the provider gone", func() bool { _, ok := h.SecretProviderOf(ref); return !ok })
	if v, err := h.LauncherSecret(ctx, ref, "gateway", ""); err == nil || v != "" {
		t.Fatalf("switched off, still answered: %q %v", v, err)
	}
	h.extensions.kept.mu.Lock()
	left := len(h.extensions.kept.kept)
	h.extensions.kept.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d kept after the provider went", left)
	}
}

// A value resolved while something was forgotten is not kept: its provider may have just gone.
func TestKeptSecretsAfterAForget(t *testing.T) {
	var k keptSecrets
	key := keptKey{reference: "exv://a/b/c", kind: "launcher", id: "gw"}
	_, gen, _ := k.get(key)
	k.forgetProvider("example-vault")
	k.put(key, "v", "example-vault", gen)
	if _, _, ok := k.get(key); ok {
		t.Fatal("kept across a forget")
	}
	_, gen, _ = k.get(key)
	k.put(key, "v", "example-vault", gen)
	if v, _, ok := k.get(key); !ok || v != "v" {
		t.Fatalf("not kept: %q %v", v, ok)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, _ := os.ReadFile(path)
	return string(raw)
}
