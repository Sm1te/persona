package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The built-in providers (presets_builtin.json, docs/assistant/onboarding.md 4.3 / 5.4): what each declares, how they merge with
// the plugins', the names a provider added from one gets, and assign.

func presetsIn(t *testing.T, s *Service, lang string) []LauncherPreset {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/v1/launchers/presets", nil)
	r.Header.Set("Accept-Language", lang)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, withRequestLang(r)) // as the bus hands it over
	if w.Code != 200 {
		t.Fatalf("presets: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Presets []LauncherPreset `json:"presets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Presets
}

func presetIDs(list []LauncherPreset) []string {
	ids := make([]string, len(list))
	for i, p := range list {
		ids[i] = p.ID
	}
	return ids
}

// postLauncher is POST /api/v1/launchers in lang, decoded.
func postLauncher(t *testing.T, s *Service, lang, body string) (int, map[string]json.RawMessage) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/launchers", strings.NewReader(body))
	r.Header.Set("Accept-Language", lang)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, withRequestLang(r)) // as the bus hands it over
	out := map[string]json.RawMessage{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestBuiltinPresetsAreComplete(t *testing.T) {
	if len(builtinPresets()) == 0 {
		t.Fatal("no built-in presets")
	}
	seen := map[string]bool{}
	for _, b := range builtinPresets() {
		p := b.LauncherPreset
		if !strings.HasPrefix(p.ID, builtinPlugin+"/") || seen[p.ID] {
			t.Errorf("%q: the id is builtin/<vendor> and unique", p.ID)
		}
		seen[p.ID] = true
		if p.Extension != "" {
			t.Errorf("%s: plugin is set when served, not in the file", p.ID)
		}
		if p.Name == "" {
			t.Errorf("%s: no name", p.ID)
		}
		switch p.Kind {
		case "launcher": // Claude Code runs on it: an Anthropic-compatible endpoint, with a small model
			if p.API != "messages" || p.SmallModel == "" {
				t.Errorf("%s: a launcher is on Messages with a small model: %+v", p.ID, p)
			}
		case "api": // the manager calls it directly (a vendor without a Messages endpoint): Bearer, no small model
			if p.API != "responses" || p.Auth != "bearer" || p.SmallModel != "" {
				t.Errorf("%s: a direct API is on Responses, Bearer, without a small model: %+v", p.ID, p)
			}
		default:
			t.Errorf("%s: kind %q", p.ID, p.Kind)
		}
		if err := checkGateway(p.BaseURL); err != nil || !strings.HasPrefix(p.BaseURL, "https://") || strings.HasSuffix(p.BaseURL, "/") {
			t.Errorf("%s: base_url %q is an https root without a trailing slash (%v)", p.ID, p.BaseURL, err)
		}
		if p.Auth != "api_key" && p.Auth != "bearer" {
			t.Errorf("%s: auth %q", p.ID, p.Auth)
		}
		if p.Model == "" || len(p.Models) == 0 {
			t.Errorf("%s: model and the model list", p.ID)
		}
		for _, m := range []string{p.Model, p.SmallModel} {
			if m == "" {
				continue
			}
			if !slices.ContainsFunc(p.Models, func(x extension.ProviderModel) bool { return x.ID == m }) {
				t.Errorf("%s: %s is not in its model list", p.ID, m)
			}
		}
		for _, m := range p.Models {
			if m.ID == "" || m.Name == "" {
				t.Errorf("%s: a model without an id or a name: %+v", p.ID, m)
			}
		}
		if u, err := url.Parse(p.SignupURL); err != nil || u.Scheme != "https" || u.Host == "" {
			t.Errorf("%s: signup_url %q", p.ID, p.SignupURL)
		}
		if !slices.Contains([]string{"cn", "global", "both"}, b.Region) {
			t.Errorf("%s: region %q", p.ID, b.Region)
		}
		if p.KeyHint != "" || p.Description != "" || p.DisplayName != "" {
			t.Errorf("%s: texts come from the catalog (assistant-models_presets)", p.ID)
		}
		key := strings.ReplaceAll(presetSlug(p.ID), "-", "_")
		for _, l := range i18n.Langs {
			for _, k := range []string{"name_" + key, "desc_" + key} {
				full := "assistant-models_presets." + k
				if v := i18n.T(l, full); v == full || v == "" {
					t.Errorf("%s: %s has no %s text", p.ID, l, k)
				}
			}
		}
	}
	// Every built-in can be added with only a key: what presetFill gives passes the launcher check.
	for _, p := range builtinLauncherPresets(i18n.ZhHans) {
		in := launcherInput{Name: "x", Preset: p.ID}
		in.Kind, in.API, in.BaseURL, in.Auth, in.Model, in.SmallModel = p.Kind, p.API, p.BaseURL, p.Auth, presetModel(p), p.SmallModel
		in.DisplayName = &p.DisplayName
		if err := in.check(); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
}

func TestOpenAIPresetSavesAndProbesResponses(t *testing.T) {
	s, _, _ := modelService(t)
	code, out := postLauncher(t, s, "en", `{"preset":"builtin/openai","api_key":"sk-test"}`)
	if code != http.StatusOK {
		t.Fatalf("save: %d %s", code, out)
	}
	var saved LauncherView
	if err := json.Unmarshal(out["launcher"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Name != "openai" || saved.DisplayName != "OpenAI (official)" || saved.Kind != "api" || saved.API != "responses" ||
		saved.BaseURL != "https://api.openai.com/v1" || saved.Model != "gpt-5.4" || !saved.HasKey || saved.SmallModel != "" {
		t.Fatalf("official direct API preset: %+v", saved)
	}
	var path, auth string
	var payload map[string]any
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer gw.Close()
	// Override only the endpoint to keep this test local; protocol, key and model still come from the preset.
	result := s.providerTest(context.Background(), probeInput{Preset: "builtin/openai", APIKey: fakeKey, BaseURL: gw.URL + "/v1"})
	if result["ok"] != true || path != "/v1/responses" || auth != "Bearer "+fakeKey || payload["model"] != "gpt-5.4" || payload["stream"] != true || payload["store"] != false {
		t.Fatalf("Responses probe: result=%v path=%s body=%v", result, path, payload)
	}
}

func TestPresetsMergeBuiltinAndPluginsInTheRequestLanguage(t *testing.T) {
	s, f, _ := modelService(t)
	f.presets = []LauncherPreset{
		{ID: "builtin/deepseek", Extension: "team", Name: "Team DeepSeek", Kind: "launcher", BaseURL: "https://gw.example.com"},
		{ID: "team/relay", Extension: "team", Name: "Relay", Kind: "launcher", BaseURL: "https://relay.example.com"},
	}
	zh := presetsIn(t, s, "zh-CN")
	en := presetsIn(t, s, "en-US")
	if len(zh) != len(builtinPresets())+1 || len(en) != len(zh) {
		t.Fatalf("built-in plus the plugin's new one: %v / %v", presetIDs(zh), presetIDs(en))
	}
	if en[0].ID != "builtin/anthropic" {
		t.Fatalf("English lists Anthropic first: %v", presetIDs(en))
	}
	ia := slices.Index(presetIDs(zh), "builtin/anthropic")
	for _, id := range []string{"builtin/deepseek", "builtin/kimi", "builtin/glm", "builtin/bailian", "builtin/minimax"} {
		if i := slices.Index(presetIDs(zh), id); i < 0 || i > ia {
			t.Fatalf("Chinese lists %s before Anthropic: %v", id, presetIDs(zh))
		}
	}
	if presetIDs(zh)[len(zh)-1] != "team/relay" || presetIDs(en)[len(en)-1] != "team/relay" {
		t.Fatal("the plugins' come after the built-in ones")
	}
	for _, list := range [][]LauncherPreset{zh, en} {
		i := slices.Index(presetIDs(list), "builtin/deepseek")
		if list[i].Extension != "team" || list[i].BaseURL != "https://gw.example.com" || list[i].SignupURL != "" {
			t.Fatalf("a plugin's preset with a built-in's id takes its place: %+v", list[i])
		}
	}
	glm := zh[slices.Index(presetIDs(zh), "builtin/glm")]
	if glm.Extension != "builtin" || glm.DisplayName != "智谱 GLM" || glm.Name != "Zhipu GLM" || glm.Description == "" || glm.SignupURL == "" {
		t.Fatalf("the built-in in Chinese: %+v", glm)
	}
	if g := en[slices.Index(presetIDs(en), "builtin/glm")]; g.DisplayName != "Zhipu GLM" {
		t.Fatalf("the built-in in English: %+v", g)
	}
}

func TestLauncherNameFor(t *testing.T) {
	none := func(string) bool { return false }
	for _, c := range []struct {
		preset, want string
		n            int
	}{
		{"builtin/deepseek", "deepseek", 1},
		{"builtin/kimi-intl", "kimi-intl", 1},
		{"team/My Gateway", "my-gateway", 1},
		{"team/My -- Gateway", "my-gateway", 1}, // a run of others is one -
		{"team/9router", "p-9router", 1},
		{"team/智谱", "provider", 1},
		{"team/test", "test-2", 2}, // reserved: a /api/v1/launchers/<word> route
	} {
		got, n := launcherNameFor(c.preset, none)
		if got != c.want || n != c.n {
			t.Errorf("%s: %q %d, want %q %d", c.preset, got, n, c.want, c.n)
		}
		if !managedLauncherName.MatchString(got) {
			t.Errorf("%s: %q breaks the name rule", c.preset, got)
		}
	}
	taken := map[string]bool{"glm": true, "glm-2": true}
	if got, n := launcherNameFor("builtin/glm", func(n string) bool { return taken[n] }); got != "glm-3" || n != 3 {
		t.Fatalf("taken names count up: %q %d", got, n)
	}
}

func TestAddFromABuiltinPresetWithOnlyTheKey(t *testing.T) {
	s, _, _ := modelService(t)
	code, out := postLauncher(t, s, "zh-Hans", `{"preset":"builtin/glm","name":"","display_name":"","api_key":"sk-test"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	var v LauncherView
	if err := json.Unmarshal(out["launcher"], &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "glm" || v.DisplayName != "智谱 GLM" || !v.HasKey || v.BaseURL != "https://open.bigmodel.cn/api/anthropic" ||
		v.Auth != "bearer" || v.Model != "glm-5.3" || v.SmallModel != "glm-5.3-flash" {
		t.Fatalf("filled in from the preset: %+v", v)
	}
	if _, ok := out["assigned"]; ok {
		t.Fatal("no assign asked, none answered")
	}
	code, out = postLauncher(t, s, "en", `{"preset":"builtin/glm","display_name":"智谱（工作）","api_key":"sk-test"}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	_ = json.Unmarshal(out["launcher"], &v)
	if v.Name != "glm-2" || v.DisplayName != "智谱（工作）" {
		t.Fatalf("a second one: %+v", v)
	}
	// Without a preset the display name can be Chinese too (an empty name there: TestAddWithoutAPresetGetsAName).
	if code, out := postLauncher(t, s, "zh-Hans", `{"name":"mine","display_name":"我的模型","base_url":"https://gw.example.com","model":"m"}`); code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if code, _ := postLauncher(t, s, "zh-Hans", `{"preset":"builtin/glm","display_name":"a\nb"}`); code != 400 {
		t.Fatalf("a display name with a line break: %d", code)
	}
	providers := s.modelProviders(context.Background())
	if i := slices.IndexFunc(providers, func(p ModelProvider) bool { return p.ID == "launcher.glm" }); i < 0 || providers[i].Name != "智谱 GLM" {
		t.Fatalf("模型配置 shows the display name: %+v", providers)
	}
}

// A built-in direct API (a vendor without a Messages endpoint, such as Grok) is added with only the key as a provider
// the manager calls itself, without a small model, and its key test speaks the preset's protocol.
func TestAddFromABuiltinDirectAPIPreset(t *testing.T) {
	var path string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer gw.Close()
	tested := 0
	for _, b := range builtinPresets() {
		p := b.LauncherPreset
		if p.Kind != "api" {
			continue
		}
		tested++
		s, _, _ := modelService(t)
		code, out := postLauncher(t, s, "en", `{"preset":"`+p.ID+`","name":"","display_name":"","api_key":"key-test"}`)
		if code != 200 {
			t.Fatalf("%s: %d %s", p.ID, code, out)
		}
		var v LauncherView
		if err := json.Unmarshal(out["launcher"], &v); err != nil {
			t.Fatal(err)
		}
		if v.Name != presetSlug(p.ID) || !v.HasKey || v.Kind != "api" || v.API != p.API || v.BaseURL != p.BaseURL ||
			v.Model != presetModel(p) || v.SmallModel != "" {
			t.Fatalf("%s: filled in from the preset: %+v", p.ID, v)
		}
		path = ""
		res := probeResult(t, s, "/api/v1/launchers/test", `{"preset":"`+p.ID+`","base_url":"`+gw.URL+`","api_key":"`+fakeKey+`"}`)
		if res["ok"] != true || res["model"] != presetModel(p) || path != "/v1/"+p.API {
			t.Fatalf("%s: the key test on its protocol: %v (%s)", p.ID, res, path)
		}
	}
	if tested == 0 {
		t.Fatal("no built-in direct API to test (Grok was one)")
	}
}

// Two providers the hub names from one preset tell apart in Model configuration; one the user named keeps that name.
func TestProvidersFromOnePresetKeepDistinctNames(t *testing.T) {
	s, f, _ := modelService(t)
	f.presets = []LauncherPreset{{ID: "team/gw", Extension: "team", Name: "Team Gateway", Kind: "launcher",
		BaseURL: "https://gw.example.com", Models: []extension.ProviderModel{{ID: "gw-large"}}}}
	for _, body := range []string{
		`{"preset":"builtin/glm","api_key":"sk-test"}`,
		`{"preset":"builtin/glm","api_key":"sk-test"}`,
		`{"preset":"builtin/glm","name":"work-glm","api_key":"sk-test"}`,
		`{"preset":"team/gw","name":"work-gw","api_key":"sk-test"}`,
	} {
		if code, out := postLauncher(t, s, "zh-Hans", body); code != 200 {
			t.Fatalf("%s: %d %s", body, code, out)
		}
	}
	names := map[string]string{}
	for _, p := range s.modelProviders(context.Background()) {
		names[p.ID] = p.Name
	}
	for id, want := range map[string]string{
		"launcher.glm":      "智谱 GLM",
		"launcher.glm-2":    "智谱 GLM 2",
		"launcher.work-glm": "work-glm",
		"launcher.work-gw":  "work-gw",
	} {
		if names[id] != want {
			t.Errorf("%s shows %q, want %q (%v)", id, names[id], want, names)
		}
	}
}

// A launcher that only happens to point at a vendor's public endpoint does not get the built-in preset's models; one
// added from the preset does.
func TestBuiltinPresetsMatchLaunchersOnlyByID(t *testing.T) {
	builtin := builtinLauncherPresets(i18n.Lang("zh-Hans"))
	i := slices.IndexFunc(builtin, func(p LauncherPreset) bool { return p.ID == "builtin/glm" })
	if i < 0 {
		t.Fatal("no builtin/glm")
	}
	glm := builtin[i]
	if _, ok := presetFor(builtin, "", glm.BaseURL); ok {
		t.Fatal("a built-in preset matched by address")
	}
	pr, ok := presetFor(builtin, "builtin/glm", glm.BaseURL)
	if !ok || pr.ID != "builtin/glm" {
		t.Fatalf("by id: %+v %v", pr, ok)
	}
	res := withPresetModels(proto.LauncherModels{}, pr)
	if len(res.Models) == 0 || res.Models[0].Description != pr.DisplayName || pr.DisplayName == pr.Name {
		t.Fatalf("models labelled with the localized name: %+v (%q / %q)", res.Models, pr.DisplayName, pr.Name)
	}
	// A plugin's preset on the same gateway still matches by address.
	plugin := append(slices.Clone(builtin), LauncherPreset{ID: "team/gw", Extension: "team", Kind: "launcher", BaseURL: "https://gw.example.com/"})
	if pr, ok := presetFor(plugin, "", "https://GW.example.com"); !ok || pr.ID != "team/gw" {
		t.Fatalf("plugin preset by address: %+v %v", pr, ok)
	}
}

func keptOf(t *testing.T, out map[string]json.RawMessage) ([]string, []keptPurpose) {
	t.Helper()
	var assigned []string
	var kept []keptPurpose
	if err := json.Unmarshal(out["assigned"], &assigned); err != nil {
		t.Fatalf("assigned: %v %s", err, out["assigned"])
	}
	if err := json.Unmarshal(out["kept"], &kept); err != nil {
		t.Fatalf("kept: %v %s", err, out["kept"])
	}
	return assigned, kept
}

func TestAssignOnlyChangesCategoriesStillAtTheirDefault(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	code, out := postLauncher(t, s, "zh-Hans", `{"preset":"builtin/deepseek","api_key":"sk-test","assign":["assistant","agent.new"]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	assigned, kept := keptOf(t, out)
	if !slices.Equal(assigned, []string{"assistant", "agent.new"}) || len(kept) != 0 {
		t.Fatalf("a fresh hub: %v %v", assigned, kept)
	}
	for _, id := range []string{"assistant", "agent.new"} {
		if b, err := s.resolveModel(ctx, id); err != nil || b.Provider != "launcher.deepseek" || b.Model != "" {
			t.Fatalf("%s: %+v %v", id, b, err)
		}
	}
	// Both are chosen now: a second provider leaves them.
	code, out = postLauncher(t, s, "zh-Hans", `{"preset":"builtin/kimi","api_key":"sk-test","assign":["assistant","agent.new"]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	assigned, kept = keptOf(t, out)
	if len(assigned) != 0 || !slices.Equal(kept, []keptPurpose{{"assistant", keptCustomized}, {"agent.new", keptCustomized}}) {
		t.Fatalf("chosen ones are kept: %v %v", assigned, kept)
	}
	if b, _ := s.resolveModel(ctx, "assistant"); b.Provider != "launcher.deepseek" {
		t.Fatalf("主对话 moved: %+v", b)
	}

	// A model picked for Agents counts as a choice; the main chat with nothing chosen still moves.
	s, _, _ = modelService(t)
	if r := modelRequest(s, "PATCH", "agent.new", `{"revision":0,"model":"claude-opus-5-5"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	code, out = postLauncher(t, s, "zh-Hans", `{"preset":"builtin/glm","api_key":"sk-test","assign":["assistant","agent.new"]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	assigned, kept = keptOf(t, out)
	if !slices.Equal(assigned, []string{"assistant"}) || !slices.Equal(kept, []keptPurpose{{"agent.new", keptCustomized}}) {
		t.Fatalf("%v %v", assigned, kept)
	}
	if b, _ := s.resolveModel(ctx, "agent.new"); b.Provider != "launcher.claude" || b.Model != "claude-opus-5-5" {
		t.Fatalf("Agents' choice kept: %+v", b)
	}

	// A direct API on Responses cannot run the main chat or Agents: both stay where they were (an older client may
	// still ask).
	s, f, _ = modelService(t)
	f.presets = []LauncherPreset{{ID: "team/responses", Extension: "team", Name: "Responses", Kind: "api", API: "responses",
		BaseURL: "https://api.example.com", Model: "r-large"}}
	code, out = postLauncher(t, s, "zh-Hans", `{"preset":"team/responses","api_key":"sk-test","assign":["assistant","agent.new"]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	assigned, kept = keptOf(t, out)
	if len(assigned) != 0 || !slices.Equal(kept, []keptPurpose{{"assistant", keptIncompatible}, {"agent.new", keptIncompatible}}) {
		t.Fatalf("a direct API is not assigned: %v %v", assigned, kept)
	}
	for _, id := range []string{"assistant", "agent.new"} {
		if b, _ := s.resolveModel(ctx, id); b.Provider == "launcher.responses" {
			t.Fatalf("%s moved to a direct API: %+v", id, b)
		}
	}

	// The main chat's launcher chosen in Settings › Assistant (or hub.json) is a choice too.
	s, f, _ = modelService(t)
	f.hello.Assistant.Launcher = "work"
	code, out = postLauncher(t, s, "zh-Hans", `{"preset":"builtin/glm","api_key":"sk-test","assign":["assistant"]}`)
	if code != 200 {
		t.Fatalf("%d %s", code, out)
	}
	if assigned, kept = keptOf(t, out); len(assigned) != 0 || !slices.Equal(kept, []keptPurpose{{"assistant", keptCustomized}}) {
		t.Fatalf("%v %v", assigned, kept)
	}

	// An empty list answers empty lists; an unknown category is refused before anything is stored.
	code, out = postLauncher(t, s, "zh-Hans", `{"preset":"builtin/kimi","api_key":"sk-test","assign":[]}`)
	if assigned, kept = keptOf(t, out); code != 200 || assigned == nil || kept == nil || len(assigned)+len(kept) != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, _ := postLauncher(t, s, "zh-Hans", `{"preset":"builtin/minimax","api_key":"sk-test","assign":["kb"]}`); code != 400 {
		t.Fatalf("assign kb: %d", code)
	}
	if slices.ContainsFunc(s.storedLaunchers(ctx), func(l storedLauncher) bool { return l.Name == "minimax" }) {
		t.Fatal("a refused request stored the provider")
	}
}

func TestProviderTestWithAPresetUsesItsDefaultModel(t *testing.T) {
	s, f, _ := modelService(t)
	var model, auth string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, auth = body.Model, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
	}))
	defer gw.Close()
	f.presets = []LauncherPreset{{ID: "team/gw", Extension: "team", Name: "GW", Kind: "launcher", BaseURL: gw.URL,
		Models: []extension.ProviderModel{{ID: "gw-large"}, {ID: "gw-small"}}}}
	out := probeResult(t, s, "/api/v1/launchers/test", `{"preset":"team/gw","api_key":"`+fakeKey+`"}`)
	if out["ok"] != true || out["model"] != "gw-large" || model != "gw-large" || auth != "Bearer "+fakeKey {
		t.Fatalf("the preset's address and default model: %v (sent %q)", out, model)
	}
	out = probeResult(t, s, "/api/v1/launchers/test", `{"preset":"team/gw","api_key":"`+fakeKey+`","model":"gw-small"}`)
	if out["ok"] != true || model != "gw-small" {
		t.Fatalf("a model given wins: %v", out)
	}
	out = probeResult(t, s, "/api/v1/launchers/test", `{"preset":"team/none","api_key":"`+fakeKey+`"}`)
	if out["ok"] != false || out["error"] == "" {
		t.Fatalf("an unknown preset: %v", out)
	}
	// /discover resolves the preset the same way: its address when none is given, a base_url given wins.
	var listed string
	modelsGW := func(id string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				w.WriteHeader(404)
				return
			}
			listed = id
			_, _ = w.Write([]byte(`{"data":[{"id":"` + id + `"}]}`))
		}))
	}
	presetGW, otherGW := modelsGW("from-preset"), modelsGW("from-base-url")
	defer presetGW.Close()
	defer otherGW.Close()
	f.presets = []LauncherPreset{{ID: "team/gw", Extension: "team", Name: "GW", Kind: "launcher", BaseURL: presetGW.URL,
		Models: []extension.ProviderModel{{ID: "gw-large"}}}}
	out = probeResult(t, s, "/api/v1/launchers/discover", `{"preset":"team/gw","api_key":"`+fakeKey+`"}`)
	if out["error"] != nil || listed != "from-preset" {
		t.Fatalf("discover with a preset: %v (listed %q)", out, listed)
	}
	out = probeResult(t, s, "/api/v1/launchers/discover", `{"preset":"team/gw","base_url":"`+otherGW.URL+`","api_key":"`+fakeKey+`"}`)
	if out["error"] != nil || listed != "from-base-url" {
		t.Fatalf("a base_url given beats the preset's: %v (listed %q)", out, listed)
	}
	// The built-in ones resolve the same way.
	pr, ok := s.presetByID(context.Background(), "builtin/deepseek")
	if !ok || presetModel(pr) != "deepseek-v4-pro" {
		t.Fatalf("%+v %v", pr, ok)
	}
}
