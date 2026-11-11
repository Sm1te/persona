package extension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCatalogCaseSensitiveMaps(t *testing.T) {
	var manifest map[string]any
	if err := json.Unmarshal([]byte(exampleManifest), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["configuration"] = map[string]any{"type": "object", "properties": map[string]any{"Foo": map[string]any{"type": "string"}, "foo": map[string]any{"type": "string"}}}
	raw, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Case sensitive", "extensions": []any{map[string]any{"manifest": manifest, "packages": []any{map[string]any{"url": "pkg.tar.gz", "sha256": hex.EncodeToString(make([]byte, 32))}}}}})
	ix, warnings, err := ParseIndex(raw)
	if err != nil || len(warnings) != 0 || len(ix.Extensions) != 1 {
		t.Fatalf("valid index rejected: %v %v", err, warnings)
	}
	key, sign := testKey(t)
	for _, tc := range []struct {
		raw []byte
		pin string
	}{{raw, ""}, {sign(raw), key}} {
		if _, err = ParseCatalog(tc.raw, tc.pin); err != nil {
			t.Fatalf("case-sensitive configuration rejected: %v", err)
		}
	}
}

func TestCatalogRejectsUnicodeSignatureAlias(t *testing.T) {
	key, sign := testKey(t)
	raw := sign([]byte(`{"market_version":1,"name":"Example","extensions":[]}`))
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(raw, &envelope)
	var signatures []map[string]json.RawMessage
	_ = json.Unmarshal(envelope["signatures"], &signatures)
	signatures[0]["ſig"] = signatures[0]["sig"]
	envelope["signatures"], _ = json.Marshal(signatures)
	raw, _ = json.Marshal(envelope)
	if _, err := ParseCatalog(raw, key); err == nil {
		t.Fatal("Unicode alias of sig accepted")
	}
}

func TestOfficialEntryProvenanceCannotBeForgedOrEdited(t *testing.T) {
	was := Official
	key, sign := testKey(t)
	Official = OfficialMarket{Spec: "github:example/plugins", SigningKey: key}
	t.Cleanup(func() { Official = was })
	var manifest map[string]any
	_ = json.Unmarshal([]byte(exampleManifest), &manifest)
	payload, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Official", "extensions": []any{map[string]any{"manifest": manifest, "packages": []any{map[string]any{"url": "package.tar.gz", "sha256": hex.EncodeToString(make([]byte, 32))}}}}})
	for _, mode := range []string{"plain-index", "unsigned", "different-key", "package-edited", "parsed-manifest-edited", "manifest-edited", "serialized-copy"} {
		t.Run(mode, func(t *testing.T) {
			f, err := ParseCatalog(sign(payload), key)
			if err != nil {
				t.Fatal(err)
			}
			e := f.Index.Extensions[0]
			switch mode {
			case "plain-index":
				ix, _, err := ParseIndex(payload)
				if err != nil {
					t.Fatal(err)
				}
				e = ix.Extensions[0]
			case "unsigned":
				f, err := ParseCatalog(sign(payload), "")
				if err != nil {
					t.Fatal(err)
				}
				e = f.Index.Extensions[0]
			case "different-key":
				other, otherSign := testKey(t)
				f, err := ParseCatalog(otherSign(payload), other)
				if err != nil {
					t.Fatal(err)
				}
				e = f.Index.Extensions[0]
			case "package-edited":
				e.Packages[0].URL = "other.tar.gz"
			case "parsed-manifest-edited":
				e.Parsed.Name = "Other"
			case "manifest-edited":
				e.Manifest = []byte(`{}`)
			case "serialized-copy":
				b, _ := json.Marshal(e)
				e = MarketEntry{}
				_ = json.Unmarshal(b, &e)
				e.Parsed = f.Index.Extensions[0].Parsed
			}
			r := NewRegistry(t.TempDir(), newMemStore(), nil, 202611100000)
			mk := &Market{Spec: MarketSpec{Kind: "github", Owner: "example", Repo: "plugins"}, TrustedKey: key}
			if _, err := r.PreviewMarket(t.Context(), mk, "official", &e, ""); err == nil {
				t.Fatal("caller-supplied or modified entry acquired official provenance")
			}
		})
	}
}

func TestCatalogProtocolNamesAndOpaqueMaps(t *testing.T) {
	for name := range Points {
		if catalogItemTypes[name] == nil {
			t.Fatalf("catalog field validation missing built-in point %s", name)
		}
	}
	key, sign := testKey(t)
	for _, raw := range []string{
		`{"market_version":1,"name":"Unknown metadata","extensions":[],"extra":{"Foo":1,"foo":2,"sig":3,"ſig":4}}`,
		`{"market_version":1,"name":"Unicode keys","extensions":[],"extra":{"K":1,"k":2,"nested":{"A":1,"a":2}}}`,
	} {
		for _, tc := range []struct {
			raw []byte
			pin string
		}{{[]byte(raw), ""}, {sign([]byte(raw)), key}} {
			if _, err := ParseCatalog(tc.raw, tc.pin); err != nil {
				t.Fatalf("opaque map was normalized: %v", err)
			}
		}
	}
	for _, raw := range []string{
		`{"market_version":1,"Name":"Alias","extensions":[]}`,
		`{"market_version":1,"name":"Example","Extensions":[]}`,
		`{"market_version":1,"name":"Example","extensions":[],"extra":{"nested":{"a":1,"a":2}}}`,
		`{"market_version":1,"name":"Example","extensions":[],"extra":{"a":1,"a":2}}`,
		`{"market_version":1,"name":"Example","extensions":[{"packages":[{"url":"x","URL":"y"}]}]}`,
		`{"market_version":1,"name":"Example","extensions":[{"manifest":{"permissions":{"usage_gateways":true,"uſage_gateways":false}}}]}`,
		`{"market_version":1,"name":"Example","extensions":[{"manifest":{"contributes":{"providers":[{"id":"x","Id":"y"}]}}}]}`,
		`{"market_version":1,"name":"Example","extensions":[{"manifest":{"engines":{"plugin_protocol":1,"Plugin_Protocol":1}}}]}`,
	} {
		if _, err := ParseCatalog(sign([]byte(raw)), key); err == nil {
			t.Fatalf("ambiguous object accepted: %s", raw)
		}
	}
	valid := sign([]byte(`{"market_version":1,"name":"Example","extensions":[]}`))
	for _, raw := range [][]byte{
		bytes.Replace(valid, []byte(`"payload"`), []byte(`"Payload"`), 1),
		bytes.Replace(valid, []byte(`"signatures"`), []byte(`"ſignatures"`), 1),
		bytes.Replace(valid, []byte(`"sig"`), []byte(`"ſig"`), 1),
		bytes.Replace(valid, []byte(`"key_id"`), []byte(`"Key_id"`), 1),
		bytes.Replace(valid, []byte(`"alg":"ed25519"`), []byte(`"alg":"ed25519","unknown":1`), 1),
		bytes.Replace(valid, []byte(`Example`), []byte{0xff}, 1),
	} {
		if _, err := ParseCatalog(raw, key); err == nil {
			t.Fatalf("bad envelope accepted: %q", raw)
		}
	}
}

func TestGitMirrorRelativePackageRefreshesCheckout(t *testing.T) {
	was := Official
	key, sign := testKey(t)
	Official = OfficialMarket{Spec: "github:example/plugins", SigningKey: key}
	t.Cleanup(func() { Official = was })
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), "plugins.git")
	// A url rewrite in a temporary home's global config keeps every clone/fetch on the owned local source. Every git
	// reads ~/.gitconfig; GIT_CONFIG_COUNT would need git 2.31, and an older git would reach the real host instead.
	home := t.TempDir()
	t.Setenv("HOME", home)
	run(t, home, "config", "--file", filepath.Join(home, ".gitconfig"), "url."+bare+".insteadOf", "https://github.com/example/plugins.git")
	run(t, work, "init", "-q", "-b", "main")
	write := func(data []byte) {
		if err := os.MkdirAll(filepath.Join(work, "packages"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, "packages/pkg.tar.gz"), data, 0600); err != nil {
			t.Fatal(err)
		}
		run(t, work, "add", "-A")
		run(t, work, "commit", "-q", "-m", "package fixture")
	}
	write([]byte("old archive"))
	run(t, t.TempDir(), "clone", "-q", "--bare", work, bare)
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "stale"}[stale], func(t *testing.T) {
			m := &Market{Spec: MarketSpec{Kind: "git", URL: "https://github.com/example/plugins.git", Ref: "main"}, Dir: t.TempDir(), allowLocal: true}
			if stale {
				if err := m.syncGit(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			pkg := []byte("updated archive " + t.Name())
			write(pkg)
			run(t, work, "push", "-q", bare, "main")
			sum := sha256.Sum256(pkg)
			packageRef := &MarketPackage{URL: "packages/pkg.tar.gz", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(pkg))}
			var manifest map[string]any
			_ = json.Unmarshal([]byte(exampleManifest), &manifest)
			payload, _ := json.Marshal(map[string]any{"market_version": 1, "name": "Git mirror", "extensions": []any{map[string]any{"manifest": manifest, "packages": []any{packageRef}}}})
			catalog := sign(payload)
			mirror := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(catalog) }))
			defer mirror.Close()
			m.CatalogMirror, m.Client = mirror.URL+"/"+IndexFile, mirror.Client()
			f, err := m.FetchSigned(t.Context())
			if err != nil || len(f.Index.Extensions) != 1 {
				t.Fatalf("mirror catalog: %v", err)
			}
			if !stale {
				if _, err := os.Stat(m.checkout()); !os.IsNotExist(err) {
					t.Fatal("mirror cloned before a relative download")
				}
			}
			// Installation constructs a fresh Market, without fetch-time state.
			installer := *m
			file, _, err := installer.Download(context.Background(), f.Index.Extensions[0].PackageFor(Platform()), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(file)
			if err != nil || string(got) != string(pkg) {
				t.Fatalf("package = %q, error = %v", got, err)
			}
		})
	}
}

func TestGitMirrorCatalogDoesNotCloneForHTTPPackage(t *testing.T) {
	was := Official
	key, sign := testKey(t)
	Official = OfficialMarket{Spec: "github:example/plugins", SigningKey: key}
	t.Cleanup(func() { Official = was })
	raw := sign([]byte(`{"market_version":1,"name":"Mirror","extensions":[]}`))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer srv.Close()
	spec, err := ParseMarket("git:https://github.com/example/plugins.git")
	if err != nil {
		t.Fatal(err)
	}
	m := &Market{Spec: spec, Dir: t.TempDir(), CatalogMirror: srv.URL + "/" + IndexFile, Client: srv.Client()}
	t.Setenv("PATH", t.TempDir())
	if _, err := m.FetchSigned(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.checkout()); !os.IsNotExist(err) {
		t.Fatalf("catalog mirror unnecessarily cloned: %v", err)
	}
	pkg := &MarketPackage{URL: srv.URL + "/pkg.tar.gz", SHA256: hex.EncodeToString(sha256Bytes(raw)), Size: int64(len(raw))}
	if _, _, err := m.Download(context.Background(), pkg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "artifactType": OCIArtifactType, "config": map[string]any{}, "layers": []any{map[string]any{"mediaType": OCILayerType, "digest": "sha256:" + ociSum(raw), "size": len(raw), "annotations": map[string]string{"org.opencontainers.image.title": "pkg.tar.gz"}}}})
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/manifests/") {
			_, _ = w.Write(manifest)
		} else {
			_, _ = w.Write(raw)
		}
	}))
	defer registry.Close()
	pkg.URL = "oci:" + strings.TrimPrefix(registry.URL, "http://") + "/example/market/pkg@sha256:" + ociSum(manifest)
	if _, _, err := m.Download(t.Context(), pkg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.checkout()); !os.IsNotExist(err) {
		t.Fatal("OCI package unnecessarily created a checkout")
	}
}

func TestGitRelativePackageDoesNotUseStaleCheckoutOnFailure(t *testing.T) {
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), "plugins.git")
	run(t, work, "init", "-q", "-b", "main")
	data := []byte("archive fixture")
	if err := os.WriteFile(filepath.Join(work, "pkg.tar.gz"), data, 0600); err != nil {
		t.Fatal(err)
	}
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "fixture")
	run(t, t.TempDir(), "clone", "-q", "--bare", work, bare)
	m := &Market{Spec: MarketSpec{Kind: "git", URL: bare}, Dir: t.TempDir(), allowLocal: true}
	pkg := &MarketPackage{URL: "pkg.tar.gz", SHA256: ociSum(data), Size: int64(len(data))}
	if _, _, err := m.Download(t.Context(), pkg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := m.Download(ctx, pkg, t.TempDir()); err == nil {
		t.Fatal("canceled download read old checkout")
	}
	ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if _, _, err := m.Download(ctx, pkg, t.TempDir()); err == nil {
		t.Fatal("expired download read old checkout")
	}
	if err := os.Rename(bare, bare+".unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Download(t.Context(), pkg, t.TempDir()); err == nil {
		t.Fatal("failed sync read old checkout despite correct old hash")
	}
}

func sha256Bytes(b []byte) []byte { s := sha256.Sum256(b); return s[:] }
