package voice

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// hub.json's upstreams first, the form's after them as custom; the form's owner and notes win.
func TestMergeHubJSONFirstThenTheForm(t *testing.T) {
	c, err := Merge(Sources{
		HubJSON: json.RawMessage(`{"providers":[{"id":"rt","url":"wss://rt.example.com/v1/live","key_file":"~/k"}],"default":"rt/m1","owner":"hub owner","notes":"hub notes"}`),
		Form:    json.RawMessage(`{"url":"wss://live.example.com/v1/live","name":"GPT-Live","dialect":"codex","models":["gpt-live-1"],"input_rate":24000,"owner":"form owner"}`),
		Key:     "sk-form",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 2 || c.Providers[0].ID != "rt" || c.Providers[1].ID != FormProvider {
		t.Fatalf("providers %+v", c.Providers)
	}
	f := c.Providers[1]
	if f.URL != "wss://live.example.com/v1/live" || f.Key != "sk-form" || f.Dialect != DialectCodex || f.Name != "GPT-Live" ||
		!slices.Equal(f.Models, []string{"gpt-live-1"}) || f.InputRate != 24000 || f.KeyCommand != "" || f.KeyFile != "" || f.SSH != "" {
		t.Fatalf("the form's upstream %+v", f)
	}
	if c.Default != "rt/m1" || c.Owner != "form owner" || c.Notes != "hub notes" || c.Setup != "" || len(c.Warnings) != 0 {
		t.Fatalf("%+v", c)
	}
	cat := &catalog{cfg: &c}
	if p, model, err := cat.resolve(t.Context(), "custom/gpt-live-1"); err != nil || p.ID != FormProvider || model != "gpt-live-1" {
		t.Fatalf("custom/<model> goes to the form's upstream: %v %s %v", p, model, err)
	}
}

// The form alone (no hub.json entry): its upstream is the only one, its default the default.
func TestMergeTheFormAlone(t *testing.T) {
	c, err := Merge(Sources{Form: json.RawMessage(`{"url":"wss://live.example.com/v1/live","dialect":"live","default_model":"m2"}`), Key: "k"})
	if err != nil || len(c.Providers) != 1 || c.Providers[0].defaultModel() != "m2" || c.Setup != "" {
		t.Fatalf("%+v %v", c, err)
	}
}

// hub.json has an upstream of id custom: it wins, the form's is left out with a warning.
func TestMergeIDTakenByHubJSON(t *testing.T) {
	c, err := Merge(Sources{
		HubJSON: json.RawMessage(`{"providers":[{"id":"custom","url":"wss://mine.example.com/v1/live"}]}`),
		Form:    json.RawMessage(`{"url":"wss://other.example.com/v1/live"}`),
		Key:     "k",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) != 1 || c.Providers[0].URL != "wss://mine.example.com/v1/live" || c.Providers[0].Key != "" {
		t.Fatalf("hub.json's custom stays, without the form's key: %+v", c.Providers)
	}
	if !slices.Equal(c.Warnings, []string{WarnFormIDTaken}) || c.Setup != "" {
		t.Fatalf("%+v", c)
	}
}

// No usable upstream is not an error: Setup says what is missing.
func TestMergeWithoutAnUpstream(t *testing.T) {
	for _, tc := range []struct {
		name  string
		s     Sources
		setup string
		warn  []string
	}{
		{"nothing", Sources{}, SetupURLAndKey, nil},
		{"the form's defaults only", Sources{Form: json.RawMessage(`{"name":"GPT-Live","dialect":"live"}`)}, SetupURLAndKey, nil},
		{"an address without its key", Sources{Form: json.RawMessage(`{"url":"wss://live.example.com/v1/live"}`)}, SetupKey, nil},
		{"a key without an address", Sources{Form: json.RawMessage(`{}`), Key: "k"}, SetupURL, nil},
		{"hub.json without providers", Sources{HubJSON: json.RawMessage(`{"owner":"x","default":"rt/m1"}`)}, SetupURLAndKey, nil},
	} {
		c, err := Merge(tc.s)
		if err != nil || len(c.Providers) != 0 || c.Setup != tc.setup || !slices.Equal(c.Warnings, tc.warn) || c.Default != "" {
			t.Errorf("%s: %+v %v", tc.name, c, err)
		}
	}
	// hub.json's upstream serves: the form's address without a key is only a warning.
	c, err := Merge(Sources{HubJSON: json.RawMessage(`{"providers":[{"id":"rt","url":"wss://rt.example.com/v1/live"}]}`),
		Form: json.RawMessage(`{"url":"wss://live.example.com/v1/live"}`)})
	if err != nil || len(c.Providers) != 1 || c.Setup != "" || !slices.Equal(c.Warnings, []string{WarnFormNoKey}) {
		t.Fatalf("%+v %v", c, err)
	}
}

// hub.json's default naming no upstream is dropped: the first upstream's default instead.
func TestMergeDefaultOfNoUpstream(t *testing.T) {
	c, err := Merge(Sources{HubJSON: json.RawMessage(`{"providers":[{"id":"rt","url":"wss://rt.example.com/v1/live","models":["m1"]}],"default":"gone/m9"}`)})
	if err != nil || c.Default != "" || !slices.Equal(c.Warnings, []string{WarnDefaultUnknown}) {
		t.Fatalf("%+v %v", c, err)
	}
	if p, model, err := (&catalog{cfg: &c}).resolve(t.Context(), ""); err != nil || p.ID != "rt" || model != "m1" {
		t.Fatalf("%v %s %v", p, model, err)
	}
}

// Settings holding providers are the old format (a plugin run by hand): taken whole, as before.
func TestMergeOldFormat(t *testing.T) {
	c, err := Merge(Sources{Form: json.RawMessage(`{"providers":[{"id":"rt","url":"wss://rt.example.com/v1/live","key":"k"}],"owner":"me"}`),
		HubJSON: json.RawMessage(`{"providers":[{"id":"other","url":"wss://other.example.com/v1/live"}]}`), Key: "ignored"})
	if err != nil || len(c.Providers) != 1 || c.Providers[0].ID != "rt" || c.Providers[0].Key != "k" || c.Owner != "me" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Merge(Sources{Form: json.RawMessage(`{"providers":[]}`)}); err == nil {
		t.Fatal("the old format still needs a provider")
	}
}

// A broken hub.json entry or form refuses to start (the hub shows the extension's error).
func TestMergeRefusesBrokenSettings(t *testing.T) {
	for _, s := range []Sources{
		{HubJSON: json.RawMessage(`{"providers":[{"id":"rt","url":"https://rt.example.com"}]}`)},
		{HubJSON: json.RawMessage(`{"providers":[{"id":"rt","url":"wss://a"},{"id":"rt","url":"wss://b"}]}`)},
		{HubJSON: json.RawMessage(`[1]`)},
		{Form: json.RawMessage(`{"url":"https://live.example.com"}`), Key: "k"},
		{Form: json.RawMessage(`{"url":"wss://live.example.com","models":["bad model"]}`), Key: "k"},
		{Form: json.RawMessage(`{"url":"wss://live.example.com","input_rate":8000}`), Key: "k"},
		{Form: json.RawMessage(`not json`)},
	} {
		if _, err := Merge(s); err == nil {
			t.Errorf("%s / %s: no error", s.HubJSON, s.Form)
		}
	}
}

// LoadConfig reads what the hub hands the built-in extension, under the new names and the old.
func TestLoadConfigFromTheHub(t *testing.T) {
	t.Setenv("PERSONA_EXTENSION_CONFIG", `{"url":"wss://live.example.com/v1/live"}`)
	t.Setenv("PERSONA_EXTENSION_SECRETS", `{"key":"sk-form"}`)
	t.Setenv("PERSONA_VOICE_HUB_CONFIG", `{"providers":[{"id":"rt","url":"wss://rt.example.com/v1/live"}]}`)
	c, err := LoadConfig()
	if err != nil || len(c.Providers) != 2 || c.Providers[1].Key != "sk-form" {
		t.Fatalf("%+v %v", c, err)
	}

	// A hub before the built-in: hub.json's config as the plugin's own, under the old name.
	for _, name := range []string{"PERSONA_EXTENSION_CONFIG", "PERSONA_EXTENSION_SECRETS", "PERSONA_VOICE_HUB_CONFIG"} {
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PERSONA_PLUGIN_CONFIG", `{"providers":[{"id":"old","url":"wss://old.example.com/v1/live"}]}`)
	if c, err = LoadConfig(); err != nil || len(c.Providers) != 1 || c.Providers[0].ID != "old" {
		t.Fatalf("%+v %v", c, err)
	}

	t.Setenv("PERSONA_PLUGIN_CONFIG", "")
	t.Setenv("PERSONA_EXTENSION_SECRETS", `{"key": 1}`)
	if _, err = LoadConfig(); err == nil || strings.Contains(err.Error(), "1") {
		t.Fatalf("bad secrets refused without echoing them: %v", err)
	}
}

// Without an upstream the plugin runs: /health and /info answer, a conversation is refused with no_upstream.
func TestPluginWithoutAnUpstream(t *testing.T) {
	cfg, err := Merge(Sources{Form: json.RawMessage(`{"url":"wss://live.example.com/v1/live"}`)})
	if err != nil {
		t.Fatal(err)
	}
	pl := New(cfg, "http://127.0.0.1:1", "hubtoken", log.New(io.Discard, "", 0))
	srv := httptest.NewServer(pl.Handler())
	defer srv.Close()
	get := func(path string) (int, map[string]any) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := get("/health"); code != http.StatusOK {
		t.Fatalf("health %d", code)
	}
	if code, info := get("/info"); code != http.StatusOK || info["protocol"] != float64(2) || info["setup"] != SetupKey || info["warnings"] != nil ||
		fmt.Sprint(info["features"]) != "[target runs asks resume suspend speak]" {
		t.Fatalf("info %d %v", code, info)
	}
	if code, models := get("/models"); code != http.StatusOK || models["models"] == nil || len(models["models"].([]any)) != 0 {
		t.Fatalf("models %d %v", code, models)
	}
	if code, voices := get("/voices"); code != http.StatusServiceUnavailable || voices["code"] != codeNoUpstream {
		t.Fatalf("voices %d %v", code, voices)
	}
	a := dialApp(t, pl, map[string]any{"type": "start"})
	f := a.until(ofType("error"))
	if f["code"] != codeNoUpstream || f["fatal"] != true {
		t.Fatalf("%v", f)
	}
	if _, ok := <-a.frames; ok {
		t.Fatal("the conversation stays open")
	}
}

// The app's model is of an upstream no longer there: the default one, and a notice that is not fatal.
func TestModelOfAGoneUpstreamFallsBack(t *testing.T) {
	hub := newFakeHub(t)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		if f["type"] == "session.start" {
			u.send(c, map[string]any{"type": "session.started", "session": map[string]any{}})
		}
	})
	pl := testPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Key: "k", Models: []string{"m1"}}}}, hub.server(t))
	a := dialApp(t, pl, map[string]any{"type": "start", "model": "custom/gpt-live-1", "voice": "v_custom"})
	ready := a.until(ofType("ready"))
	if ready["model"] != "rt/m1" || ready["voice"] != nil {
		t.Fatalf("ready %v", ready)
	}
	f := a.until(ofType("error"))
	if f["code"] != codeModelUnavailable || f["fatal"] != false || !strings.Contains(f["message"].(string), "custom/gpt-live-1") {
		t.Fatalf("%v", f)
	}
	if start := up.next("session.start")["session"].(map[string]any); start["model"] != "m1" {
		t.Fatalf("session.start %v", start)
	}
}

// The fake dialect is for hub.json (or a hand-run plugin's config) only: it connects nowhere, has no url, and the
// settings form cannot make one.
func TestFakeDialectOnlyFromHubJSON(t *testing.T) {
	c, err := Merge(Sources{HubJSON: json.RawMessage(`{"providers":[{"id":"dev","dialect":"fake"}]}`)})
	if err != nil || len(c.Providers) != 1 || c.Providers[0].Dialect != DialectFake || c.Setup != "" {
		t.Fatalf("%+v %v", c, err)
	}
	cat := &catalog{cfg: &c}
	if p, model, err := cat.resolve(t.Context(), ""); err != nil || p.ID != "dev" || model != "fake" {
		t.Fatalf("default model: %v %s %v", p, model, err)
	}
	if v, err := cat.voices(t.Context(), ""); err != nil || len(v.Voices) != 0 || v.Voices == nil {
		t.Fatalf("voices: %+v %v", v, err)
	}
	if _, err := Merge(Sources{HubJSON: json.RawMessage(`{"providers":[{"id":"dev","dialect":"fake","url":"wss://x.example.com/v1/live"}]}`)}); err == nil {
		t.Fatal("a fake upstream with a url")
	}
	if _, err := Merge(Sources{Form: json.RawMessage(`{"url":"wss://x.example.com/v1/live","dialect":"fake"}`), Key: "k"}); err == nil {
		t.Fatal("the form made a fake upstream")
	}
}

// Quota gateway linkage and OpenAI Realtime (/v1/realtime) dialect: https and loopback http gateway_urls map to wss/ws
// /v1/live, non-loopback http and management keys are rejected, and /v1/realtime speaks the realtimeWire dialect.
func TestGatewayProviderAndRealtimeDialect(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
		ok   bool
	}{
		{"https://gw.example.com", "wss://gw.example.com/v1/live", true},
		{"https://gw.example.com/v1/", "wss://gw.example.com/v1/live", true},
		{"http://127.0.0.1:8080", "ws://127.0.0.1:8080/v1/live", true},
		{"http://localhost:8080/v1", "ws://localhost:8080/v1/live", true},
		{"http://[::1]:8080", "ws://[::1]:8080/v1/live", true},
		{"http://127.0.0.1", "ws://127.0.0.1/v1/live", true},
		{"http://gw.example.com:8080", "", false},
		{"http://192.0.2.10:8080", "", false},
		{"ftp://127.0.0.1:8080", "", false},
	} {
		got, ok := GatewayLiveURL(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("GatewayLiveURL(%q) = (%q, %v), want (%q, %v)", tc.raw, got, ok, tc.want, tc.ok)
		}
	}

	if _, ok := GatewayProviderFromConfig(json.RawMessage(`{"gateway_url":"https://gw.example.com","key_kind":"management"}`), "Relay", "mgmt-key"); ok {
		t.Fatal("management key must not create a voice provider")
	}
	prov, ok := GatewayProviderFromConfig(json.RawMessage(`{"gateway_url":"http://127.0.0.1:8080","key_kind":"api"}`), "Relay", "gw-api-key")
	if !ok || prov.ID != GatewayProvider || prov.Name != "Relay" || prov.URL != "ws://127.0.0.1:8080/v1/live" || prov.Dialect != DialectCodex || prov.DefaultModel != "gpt-live-1-codex" {
		t.Fatalf("GatewayProviderFromConfig: %+v (%v)", prov, ok)
	}
	gwRaw, _ := json.Marshal(prov)
	cfg, err := Merge(Sources{Gateway: gwRaw})
	if err != nil || cfg.Setup != "" || len(cfg.Providers) != 1 || cfg.Providers[0].ID != GatewayProvider {
		t.Fatalf("Merge with Gateway: %+v (%v)", cfg, err)
	}

	// OpenAI Realtime dialect (/v1/realtime, gpt-realtime).
	rtCfg, err := Merge(Sources{Form: json.RawMessage(`{"url":"wss://gw.example.com/v1/realtime","dialect":"realtime"}`), Key: "gw-key"})
	if err != nil || len(rtCfg.Providers) != 1 || rtCfg.Providers[0].Dialect != DialectRealtime || rtCfg.Providers[0].defaultModel() != "gpt-realtime" {
		t.Fatalf("Merge realtime dialect: %+v (%v)", rtCfg, err)
	}
	cat := &catalog{cfg: &rtCfg}
	models, def, err := cat.models(t.Context(), func(string, ...any) {})
	if err != nil || def != "custom/gpt-realtime" || len(models) != 2 {
		t.Fatalf("realtime models: %+v def=%q err=%v", models, def, err)
	}
	w := newWire(DialectRealtime)
	if u, err := w.url("wss://gw.example.com/v1/realtime", "gpt-realtime"); err != nil || u != "wss://gw.example.com/v1/realtime?model=gpt-realtime" {
		t.Fatalf("realtime wire url: %q (%v)", u, err)
	}
	if evs := w.decode([]byte(`{"type":"session.created"}`)); len(evs) != 1 || evs[0].kind != evStarted {
		t.Fatalf("realtime session.created: %+v", evs)
	}
}
