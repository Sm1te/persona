package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Fake aliases only: no real gateway, key or header.
const (
	importKeyA   = "sk-test-import-a"
	importKeyB   = "sk-test-import-b"
	importHeader = "X-Test-Secret: hdr-test-value"
)

func TestMergeZshrcAliases(t *testing.T) {
	gw := func(alias string, env map[string]string) zshrcAlias {
		e := map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com/", "ANTHROPIC_AUTH_TOKEN": "Bearer " + importKeyA}
		for k, v := range env {
			e[k] = v
		}
		return zshrcAlias{Alias: alias, Env: e, Command: "claude"}
	}
	// own is alias on the gateway as a provider of its own (a variant), with models.
	own := func(alias string, models ...proto.ProviderModel) importGroup {
		g := importGroup{key: importKeyA, importedProvider: importedProvider{HasKey: true, Aliases: []string{alias},
			Sources: []importSource{{Alias: alias, File: "~/.zshrc"}},
			ManagedLauncher: proto.ManagedLauncher{Name: alias, BaseURL: "https://gw.example.com", Auth: "bearer", Models: models,
				Source: "zshrc:" + alias + "@m1", OwnService: proto.OwnServiceUse}}}
		if len(models) > 0 {
			g.Model = models[0].ID
		}
		return g
	}
	cases := []struct {
		name    string
		aliases []zshrcAlias
		known   []knownProvider
		want    []importGroup
	}{
		{
			name: "one alias with every setting",
			aliases: []zshrcAlias{{Alias: "gcc", Command: "claude", File: "~/.bashrc", Args: []string{"--dangerously-skip-permissions"}, Env: map[string]string{
				"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": importKeyA, "ANTHROPIC_MODEL": "glm-5[1m]",
				"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5[1m]", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-air", "ANTHROPIC_SMALL_FAST_MODEL": "glm-air",
				"CLAUDE_CODE_SUBAGENT_MODEL": "glm-5", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE": "1", "CLAUDE_CODE_EFFORT_LEVEL": "high",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "180000", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1000000", "API_TIMEOUT_MS": "600000",
				"ANTHROPIC_CUSTOM_HEADERS": importHeader, "HTTP_PROXY": "http://proxy.example.com:1", "no_proxy": "localhost",
				"ANTHROPIC_CUSTOM_MODEL_OPTION": "glm-x", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "GLM X",
				"ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION": "the fast one", "DISABLE_TELEMETRY": "1",
				"CLAUDE_CODE_OAUTH_TOKEN": "sk-test-oauth",
			}}},
			want: []importGroup{{key: importKeyA, headers: importHeader, env: "DISABLE_TELEMETRY=1", importedProvider: importedProvider{
				HasKey: true, HasEnv: true, EnvNames: []string{"DISABLE_TELEMETRY"},
				SkippedEnv: []skippedEnv{{Name: "CLAUDE_CODE_OAUTH_TOKEN", Reason: "credential"}},
				Aliases:    []string{"gcc"}, Sources: []importSource{{Alias: "gcc", File: "~/.bashrc"}},
				ManagedLauncher: proto.ManagedLauncher{Name: "gcc", BaseURL: "https://gw.example.com", Auth: "api_key", Model: "glm-5",
					Args: []string{"--dangerously-skip-permissions"}, Models: []proto.ProviderModel{{ID: "glm-5", ContextWindow: 1000000}},
					OpusModel: "glm-5", HaikuModel: "glm-air", SmallModel: "glm-air", SubagentModel: "glm-5", SubagentForce: true,
					CustomModel: "glm-x", CustomModelName: "GLM X", CustomModelDescription: "the fast one",
					Effort: "high", CompactWindow: 180000, MaxContext: 1000000, TimeoutMS: 600000, HasHeaders: true,
					Proxy: "http://proxy.example.com:1", NoProxy: "localhost", Source: "zshrc:gcc@m1", OwnService: proto.OwnServiceUse},
			}}},
		},
		{
			name: "same gateway and key merge their models; the first alias names it",
			aliases: []zshrcAlias{
				gw("kimi", map[string]string{"ANTHROPIC_MODEL": "kimi-k3"}),
				gw("kimi2", map[string]string{"ANTHROPIC_MODEL": "kimi-k3[1m]"}),
				gw("kimi3", map[string]string{"ANTHROPIC_MODEL": "kimi-flash"}),
			},
			known: []knownProvider{{name: "stored", baseURL: "https://gw.example.com", key: importKeyA}},
			want: []importGroup{{key: importKeyA, importedProvider: importedProvider{
				HasKey: true, Aliases: []string{"kimi", "kimi2", "kimi3"}, Existing: "stored",
				Sources: []importSource{{Alias: "kimi", File: "~/.zshrc"}, {Alias: "kimi2", File: "~/.zshrc"}, {Alias: "kimi3", File: "~/.zshrc"}},
				ManagedLauncher: proto.ManagedLauncher{Name: "kimi", BaseURL: "https://gw.example.com", Auth: "bearer", Model: "kimi-k3",
					Models: []proto.ProviderModel{{ID: "kimi-k3", ContextWindow: 1000000}, {ID: "kimi-flash"}}, Source: "zshrc:kimi@m1",
					OwnService: proto.OwnServiceUse},
			}, variants: []importGroup{ // the stored one was not made from any of them: each variant would be new
				own("kimi", proto.ProviderModel{ID: "kimi-k3"}), own("kimi2", proto.ProviderModel{ID: "kimi-k3", ContextWindow: 1000000}),
				own("kimi3", proto.ProviderModel{ID: "kimi-flash"}),
			}}},
		},
		{
			name: "another key is another provider; plain claude, codex and bad names",
			aliases: []zshrcAlias{
				gw("a.cc", map[string]string{"ANTHROPIC_MODEL": "m"}),
				gw("1cc", map[string]string{"ANTHROPIC_AUTH_TOKEN": importKeyB}),
				{Alias: "plain", Command: "claude", Env: map[string]string{"ANTHROPIC_MODEL": "opus"}},
				{Alias: "cx", Command: "codex", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://x.example.com"}},
			},
			known: []knownProvider{{name: "other-key", baseURL: "https://gw.example.com", key: importKeyB + "-no"}},
			want: []importGroup{
				{key: importKeyA, importedProvider: importedProvider{HasKey: true, Aliases: []string{"a.cc"},
					Sources: []importSource{{Alias: "a.cc", File: "~/.zshrc"}}, ManagedLauncher: proto.ManagedLauncher{
						Name: "a-cc", BaseURL: "https://gw.example.com", Auth: "bearer", Model: "m", Models: []proto.ProviderModel{{ID: "m"}},
						Source: "zshrc:a.cc@m1", OwnService: proto.OwnServiceUse}}},
				{key: importKeyB, importedProvider: importedProvider{HasKey: true, Aliases: []string{"1cc"},
					Sources: []importSource{{Alias: "1cc", File: "~/.zshrc"}}, ManagedLauncher: proto.ManagedLauncher{
						Name: "zshrc_1cc", BaseURL: "https://gw.example.com", Auth: "bearer", Source: "zshrc:1cc@m1", OwnService: proto.OwnServiceUse}}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeZshrcAliases("m1", c.aliases, c.known)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got  %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestMergeImportedModelsAddsOnlyNew(t *testing.T) {
	models := []proto.ProviderModel{{ID: "a", Name: "A", Efforts: []string{"high"}}, {ID: "b", ContextWindow: 200000}}
	if !mergeImportedModels(&models, []proto.ProviderModel{{ID: "a", ContextWindow: 1000000}, {ID: "b"}, {ID: "c"}}) {
		t.Fatal("no change reported")
	}
	want := []proto.ProviderModel{{ID: "a", Name: "A", Efforts: []string{"high"}, ContextWindow: 1000000}, {ID: "b", ContextWindow: 200000}, {ID: "c"}}
	if !reflect.DeepEqual(models, want) {
		t.Fatalf("%+v", models)
	}
	if mergeImportedModels(&models, []proto.ProviderModel{{ID: "c"}}) {
		t.Fatal("a change reported for nothing new")
	}
}

func importTestHub(t *testing.T, aliases []zshrcAlias, err error) (*Service, http.Handler) {
	s, _, _ := modelService(t)
	old := zshrcAliases
	zshrcAliases = func(context.Context, *Service, string) ([]zshrcAlias, error) { return aliases, err }
	t.Cleanup(func() { zshrcAliases = old })
	return s, s
}

func importRequest(mux http.Handler, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(method, "/api/v1/launchers/import?machine=m1", strings.NewReader(body)))
	return r
}

// Values only an import's secrets hold: none may reach a preview or an answer.
const (
	importEnvSecret   = "env-test-secret-value"
	importProxySecret = "proxy-test-password"
)

var importAliases = []zshrcAlias{
	{Alias: "gcc", Command: "claude", File: "~/.bashrc", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": importKeyA,
		"ANTHROPIC_MODEL": "glm-5", "ANTHROPIC_CUSTOM_HEADERS": importHeader, "DISABLE_TELEMETRY": "1",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS": "64000", "GATEWAY_LABEL": "team blue", "AWS_SECRET_ACCESS_KEY": importEnvSecret,
		"HTTPS_PROXY": "http://user:" + importProxySecret + "@proxy.example.com:8080", "EXTRA_MARK": importEnvSecret + "-kept"}},
	{Alias: "gcc2", Command: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": importKeyA,
		"ANTHROPIC_MODEL": "glm-air"}},
	{Alias: "kcc", Command: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://k.example.com", "ANTHROPIC_AUTH_TOKEN": importKeyB,
		"ANTHROPIC_MODEL": "kimi-k3[1m]"}},
}

func TestLauncherImportPreviewHasNoSecrets(t *testing.T) {
	_, mux := importTestHub(t, importAliases, nil)
	r := importRequest(mux, "GET", "")
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	body := r.Body.String()
	for _, secret := range []string{importKeyA, importKeyB, "hdr-test-value", importEnvSecret, importProxySecret, "team blue", "64000",
		`"api_key":`, `"headers":`, `"env":`} {
		if strings.Contains(body, secret) {
			t.Fatalf("the preview carries %q: %s", secret, body)
		}
	}
	var out struct {
		Machine   string             `json:"machine"`
		Providers []importedProvider `json:"providers"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if out.Machine != "m1" || len(out.Providers) != 2 || !out.Providers[0].HasKey || !out.Providers[0].HasHeaders ||
		len(out.Providers[0].Models) != 2 || out.Providers[1].Models[0].ContextWindow != 1000000 {
		t.Fatalf("%+v", out)
	}
	g := out.Providers[0]
	wantSkipped := []skippedEnv{{Name: "AWS_SECRET_ACCESS_KEY", Reason: "credential"}, {Name: "HTTPS_PROXY", Reason: "credential"}}
	if !g.HasEnv || !slices.Equal(g.EnvNames, []string{"CLAUDE_CODE_MAX_OUTPUT_TOKENS", "DISABLE_TELEMETRY", "EXTRA_MARK", "GATEWAY_LABEL"}) ||
		!reflect.DeepEqual(g.SkippedEnv, wantSkipped) || g.Proxy != "" || g.OwnService != proto.OwnServiceUse {
		t.Fatalf("env: %+v", g)
	}
	// gcc and gcc2 share the gateway and key: each is also a provider of its own; headers and env differ (the proxy
	// with a password is left out, so neither has one).
	if !slices.Equal(g.Differs, []string{"headers", "env"}) || len(g.Variants) != 2 || g.Variants[1].Name != "gcc2" ||
		g.Variants[1].HasEnv || len(g.Variants[1].Models) != 1 || g.Variants[0].Variants != nil || g.Variants[0].Differs != nil {
		t.Fatalf("variants: %+v", g)
	}
	if len(g.Sources) != 2 || g.Sources[0].File != "~/.bashrc" || g.Sources[1].File != "~/.zshrc" || g.Sources[0].Hide != "none" {
		t.Fatalf("sources: %+v", g.Sources)
	}
	// A provider without env still says so (has_env false), and the fields the web reads are there.
	for _, field := range []string{`"has_env":false`, `"own_service":"use"`, `"sources":[`, `"differs":["headers","env"]`, `"variants":[`,
		`"skipped_env":[{"name":"AWS_SECRET_ACCESS_KEY","reason":"credential"}`} {
		if !strings.Contains(body, field) {
			t.Fatalf("no %s in %s", field, body)
		}
	}
}

func TestLauncherImportCommitSealsAndMergesExisting(t *testing.T) {
	s, mux := importTestHub(t, importAliases, nil)
	ctx := context.Background()
	sealed, err := s.sealKey(importKeyB)
	if err != nil {
		t.Fatal(err)
	}
	existing := storedLauncher{KeySealed: sealed, ManagedLauncher: proto.ManagedLauncher{Name: "kimi", BaseURL: "https://k.example.com",
		Model: "kept", Effort: "low", Models: []proto.ProviderModel{{ID: "kept"}}}}
	taken := storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gcc", BaseURL: "https://elsewhere.example.com", Model: "x"}}
	if err := s.putSetting(ctx, launchersManagedKey, []storedLauncher{existing, taken}); err != nil {
		t.Fatal(err)
	}
	r := importRequest(mux, "POST", `{"machine":"m1","providers":["gcc","kcc"]}`)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if want := `{"created":["gcc-2"],"updated":["kimi"],"hidden":[],"kept":[]}`; strings.TrimSpace(r.Body.String()) != want {
		t.Fatalf("%s", r.Body.String())
	}
	for _, s := range []string{importKeyA, importKeyB, "hdr-test-value", importEnvSecret, importProxySecret} {
		if strings.Contains(r.Body.String(), s) {
			t.Fatal("the answer carries a secret")
		}
	}
	list := s.storedLaunchers(ctx)
	if len(list) != 3 {
		t.Fatalf("%+v", list)
	}
	kimi, gcc := list[0], list[2]
	if kimi.Model != "kept" || kimi.Effort != "low" || kimi.KeySealed != sealed ||
		!reflect.DeepEqual(kimi.Models, []proto.ProviderModel{{ID: "kept"}, {ID: "kimi-k3", ContextWindow: 1000000}}) {
		t.Fatalf("an existing provider changed beyond its models: %+v", kimi)
	}
	if gcc.Name != "gcc-2" || gcc.Source != "zshrc:gcc@m1" || gcc.Auth != "" || gcc.APIKey != "" || gcc.Headers != "" || gcc.Env != "" ||
		gcc.HasEnv || len(gcc.Models) != 2 || gcc.OwnService != proto.OwnServiceUse || gcc.Share != shareAll ||
		gcc.Delivery != proto.DeliveryOnDemand {
		t.Fatalf("%+v", gcc)
	}
	raw, _ := json.Marshal(list)
	for _, secret := range []string{importKeyA, "hdr-test-value", importEnvSecret, "team blue"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s stored in the clear", secret)
		}
	}
	env, err := s.openKey(gcc.EnvSealed)
	if want := "CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000\nDISABLE_TELEMETRY=1\nEXTRA_MARK=" + importEnvSecret + "-kept\nGATEWAY_LABEL=\"team blue\""; err != nil || env != want {
		t.Fatalf("env %q %v, want %q", env, err, want)
	}
	if err := checkEnv(env); err != nil {
		t.Fatalf("the imported env does not pass checkEnv: %v", err)
	}
	if key, err := s.openKey(gcc.KeySealed); err != nil || key != importKeyA {
		t.Fatalf("key not sealed: %v", err)
	}
	if hd, err := s.openKey(gcc.HeadersSealed); err != nil || !strings.Contains(hd, "hdr-test-value") {
		t.Fatalf("headers not sealed: %v", err)
	}
	if r := importRequest(mux, "POST", `{"machine":"m1","providers":["nope"]}`); r.Code != 400 {
		t.Fatalf("an unknown provider: %d", r.Code)
	}
}

func TestLauncherImportMachineErrors(t *testing.T) {
	cases := []struct {
		err  error
		code int
		body string
	}{
		{fmt.Errorf("m1: %w", apierr.ErrOffline), 503, `"code":"offline"`},
		{&rpc.Error{Message: "unknown method launchers.zshrc"}, 409, `"code":"node_outdated"`},
		{errNoSuchMachine, 404, ""},
	}
	for _, c := range cases {
		_, mux := importTestHub(t, nil, c.err)
		for _, method := range []string{"GET", "POST"} {
			r := importRequest(mux, method, `{"machine":"m1","providers":["x"]}`)
			if r.Code != c.code || !strings.Contains(r.Body.String(), c.body) {
				t.Fatalf("%v %s: %d %s", c.err, method, r.Code, r.Body.String())
			}
		}
	}
}

func TestLauncherPickerDefault(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	if err := s.saveModelRecord(ctx, "agent.new", 0, ModelBinding{Provider: "launcher.gw", Model: "m1", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveModelRecord(ctx, "assistant", 0, ModelBinding{Runtime: proto.AssistantRuntimeAgentSDK}); err != nil {
		t.Fatal(err)
	}
	if got := s.launcherDefault(ctx, "agent.new"); !reflect.DeepEqual(got, map[string]any{"provider": "gw", "model": "m1", "effort": "high"}) {
		t.Fatalf("agent.new: %+v", got)
	}
	if got := s.launcherDefault(ctx, "assistant"); got["runtime"] != "agentsdk" || got["provider"] != "claude" {
		t.Fatalf("assistant: %+v", got)
	}
	if got := s.launcherDefault(ctx, "kb"); got != nil {
		t.Fatalf("an API use has no launcher default: %+v", got)
	}
	r := httptest.NewRequest("GET", "/api/v1/machines/m1/launchers?purpose=assistant", nil)
	if got := s.pickerDefault(ctx, r); got["runtime"] != "agentsdk" {
		t.Fatalf("?purpose= ignored: %+v", got)
	}
}

// A proxy with a password is not imported: the proxy is stored and returned in the clear. The preview names it.
func TestImportDropsAProxyWithCredentials(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		a := zshrcAlias{Alias: "pcc", Command: "claude", Env: map[string]string{k: "http://u:pw-secret@127.0.0.1:7890"}}
		g := newImportGroup("m1", a, "https://gw.example.com")
		if g.Proxy != "" || !reflect.DeepEqual(g.SkippedEnv, []skippedEnv{{Name: k, Reason: "credential"}}) || g.env != "" {
			t.Fatalf("%s: proxy with credentials imported: %q %+v %q", k, g.Proxy, g.SkippedEnv, g.env)
		}
		a.Env[k] = "http://127.0.0.1:7890"
		if g := newImportGroup("m1", a, "https://gw.example.com"); g.Proxy != "http://127.0.0.1:7890" || g.SkippedEnv != nil || g.env != "" {
			t.Fatalf("%s: plain proxy dropped: %q %+v", k, g.Proxy, g.SkippedEnv)
		}
	}
	// HTTPS_PROXY first, then the lower-case spelling, then HTTP_PROXY; the bypass list in either spelling.
	g := newImportGroup("m1", zshrcAlias{Alias: "pcc", Env: map[string]string{"https_proxy": "http://lower.example.com:1",
		"HTTP_PROXY": "http://plain.example.com:1", "NO_PROXY": "a.example.com", "no_proxy": "b.example.com"}}, "https://gw.example.com")
	if g.Proxy != "http://lower.example.com:1" || g.NoProxy != "a.example.com" {
		t.Fatalf("%q %q", g.Proxy, g.NoProxy)
	}
}

// An alias named like a reserved launcher (claude itself, a /launchers/<word> route) is imported under another name.
func TestImportNameAvoidsReservedNames(t *testing.T) {
	for _, r := range reservedLauncherNames {
		if n := importName(r); slices.Contains(reservedLauncherNames, n) || !managedLauncherName.MatchString(n) {
			t.Errorf("%s imported as %q", r, n)
		}
	}
	if n := importName("glm"); n != "glm" {
		t.Errorf("glm imported as %q", n)
	}
}
