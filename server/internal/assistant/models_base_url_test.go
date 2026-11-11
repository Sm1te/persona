package assistant

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A Messages provider's Base URL pasted with a trailing /v1 (#4886): Claude Code adds /v1/messages to
// ANTHROPIC_BASE_URL itself, so the address is stored without it, and Test connection calls what Agents will.

func storedBase(t *testing.T, s *Service, name string) string {
	t.Helper()
	list := s.storedLaunchers(context.Background())
	i := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == name })
	if i < 0 {
		t.Fatalf("%s not stored: %+v", name, list)
	}
	return list[i].BaseURL
}

func TestSavingDropsATrailingV1FromMessagesBases(t *testing.T) {
	s, _ := launcherTestService(t)
	cases := []struct{ name, body, want string }{
		{"relay", `"base_url":"https://relay.example.net/v1"`, "https://relay.example.net"},
		{"slash", `"base_url":"https://relay.example.net/v1/"`, "https://relay.example.net"},
		{"prefixed", `"base_url":"https://relay.example.net/team/v1"`, "https://relay.example.net/team"},
		{"plain", `"base_url":"https://relay.example.net/anthropic"`, "https://relay.example.net/anthropic"},
		{"api-messages", `"kind":"api","api":"messages","base_url":"https://relay.example.net/v1"`, "https://relay.example.net"},
		// Bases that are not ANTHROPIC_BASE_URL keep their /v1.
		{"api-responses", `"kind":"api","api":"responses","base_url":"https://api.example.com/v1"`, "https://api.example.com/v1"},
		{"api-chat", `"kind":"api","api":"chat","base_url":"https://api.example.com/v1"`, "https://api.example.com/v1"},
		{"codex", `"agent":"codex","base_url":"https://api.example.com/v1"`, "https://api.example.com/v1"},
	}
	for _, c := range cases {
		body := `{"name":"` + c.name + `",` + c.body + `,"model":"m","api_key":"` + fakeKey + `"}`
		if code, out := postLauncher(t, s, "en", body); code != 200 {
			t.Fatalf("%s: %d %s", c.name, code, out["error"])
		}
		if got := storedBase(t, s, c.name); got != c.want {
			t.Errorf("%s: stored %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSavingFromAPresetWithV1StoresItWithout(t *testing.T) {
	s, f, _ := modelService(t)
	f.presets = []LauncherPreset{{ID: "team/gw", Extension: "team", Name: "GW", Kind: "launcher", BaseURL: "https://gw.example.com/v1", Model: "gw-large"}}
	if code, out := postLauncher(t, s, "en", `{"preset":"team/gw","api_key":"`+fakeKey+`"}`); code != 200 {
		t.Fatalf("%d %s", code, out["error"])
	}
	l := s.storedLaunchers(context.Background())
	if len(l) != 1 || l[0].BaseURL != "https://gw.example.com" || l[0].Preset != "team/gw" {
		t.Fatalf("%+v", l)
	}
	// A provider added by hand on that gateway still finds the preset by address.
	if pr, ok := presetFor(f.presets, "", "https://gw.example.com"); !ok || pr.ID != "team/gw" {
		t.Fatalf("by address: %+v %v", pr, ok)
	}
}

func TestUpdatingAStoredV1ProviderDropsIt(t *testing.T) {
	s, mux := launcherTestService(t)
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "legacy", BaseURL: "https://relay.example.net/v1", Model: "m"}})
	if code, raw := launcherCall(t, mux, "PUT", "/api/v1/launchers/legacy", `{"base_url":"https://relay.example.net/v1","model":"m"}`); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if got := storedBase(t, s, "legacy"); got != "https://relay.example.net" {
		t.Fatalf("stored %q", got)
	}
}

// Test connection and an Agent reach one endpoint: the probe's, and the stored base with Claude Code's /v1/messages.
func TestProbeAndLaunchReachTheSameMessagesEndpoint(t *testing.T) {
	s, mux := launcherTestService(t)
	var paths []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/responses") {
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
	}))
	defer gw.Close()
	for _, suffix := range []string{"/v1", "/team/v1", "", "/v1/v1"} {
		paths = nil
		base := gw.URL + suffix
		if out := probeResult(t, mux, "/api/v1/launchers/test", `{"base_url":"`+base+`","api_key":"`+fakeKey+`","model":"m"}`); out["ok"] != true {
			t.Fatalf("%s: %v", suffix, out)
		}
		name := "p" + strings.ReplaceAll(suffix, "/", "-")
		if code, out := postLauncher(t, s, "en", `{"name":"`+name+`","base_url":"`+base+`","model":"m","api_key":"`+fakeKey+`"}`); code != 200 {
			t.Fatalf("%s: %d %s", suffix, code, out["error"])
		}
		launched := strings.TrimPrefix(storedBase(t, s, name), gw.URL) + "/v1/messages"
		if out := probeResult(t, mux, "/api/v1/launchers/test", `{"name":"`+name+`"}`); out["ok"] != true {
			t.Fatalf("%s by name: %v", suffix, out)
		}
		if len(paths) != 2 || paths[0] != launched || paths[1] != launched {
			t.Errorf("%s: tested %v, Agents call %s", suffix, paths, launched)
		}
	}
	// A Responses base keeps its /v1 and is not doubled.
	paths = nil
	if out := probeResult(t, mux, "/api/v1/launchers/test", `{"api":"responses","base_url":"`+gw.URL+`/v1","api_key":"`+fakeKey+`","model":"m"}`); out["ok"] != true {
		t.Fatalf("responses: %v", out)
	}
	if len(paths) != 1 || paths[0] != "/v1/responses" {
		t.Fatalf("responses: %v", paths)
	}
}

func TestImportDropsATrailingV1AndMatchesAStoredV1Provider(t *testing.T) {
	aliases := []zshrcAlias{{Alias: "rcc", Command: "claude", Env: map[string]string{
		"ANTHROPIC_BASE_URL": "https://gw.example.com/v1/", "ANTHROPIC_API_KEY": importKeyA, "ANTHROPIC_MODEL": "glm-5"}}}
	groups := mergeZshrcAliases("m1", aliases, nil)
	if len(groups) != 1 || groups[0].BaseURL != "https://gw.example.com" {
		t.Fatalf("%+v", groups)
	}
	s, mux := importTestHub(t, aliases, nil)
	sealed, err := s.sealKey(importKeyA)
	if err != nil {
		t.Fatal(err)
	}
	// Stored before saving dropped the /v1: the same gateway and key, so the import adds its model there.
	putLaunchers(t, s, storedLauncher{KeySealed: sealed, ManagedLauncher: proto.ManagedLauncher{Name: "relay", BaseURL: "https://gw.example.com/v1", Model: "kept"}})
	r := importRequest(mux, "POST", `{"machine":"m1","providers":["rcc"]}`)
	if want := `{"created":[],"updated":["relay"],"hidden":[],"kept":[]}`; r.Code != 200 || strings.TrimSpace(r.Body.String()) != want {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
}

// A form still holding ".../v1" for a provider stored without it tests with the stored key: the same gateway.
func TestProbeByNameKeepsTheKeyForTheSameGatewayWithV1(t *testing.T) {
	s, mux := launcherTestService(t)
	var auth []string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("x-api-key"))
		_, _ = w.Write([]byte(`{"type":"message","content":[]}`))
	}))
	defer gw.Close()
	if code, out := postLauncher(t, s, "en", `{"name":"relay","base_url":"`+gw.URL+`/v1","model":"m","api_key":"`+fakeKey+`"}`); code != 200 {
		t.Fatalf("%d %s", code, out["error"])
	}
	for _, typed := range []string{gw.URL + "/v1", gw.URL + "/v1/", gw.URL} {
		auth = nil
		if out := probeResult(t, mux, "/api/v1/launchers/test", `{"name":"relay","base_url":"`+typed+`"}`); out["ok"] != true || len(auth) != 1 || auth[0] != fakeKey {
			t.Fatalf("%s: %v (key sent: %v)", typed, out, auth)
		}
	}
	// Another address on the same host is another gateway: no stored key.
	auth = nil
	_ = probeResult(t, mux, "/api/v1/launchers/test", `{"name":"relay","base_url":"`+gw.URL+`/other"}`)
	if len(auth) != 1 || auth[0] != "" {
		t.Fatalf("the stored key went to another address: %v", auth)
	}
}

// A provider stored with /v1 before saving dropped it reaches every node without it, a node from before this change too.
func TestNodesGetAStoredV1MessagesBaseWithoutIt(t *testing.T) {
	s, _ := launcherTestService(t)
	putLaunchers(t, s,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "legacy", BaseURL: "https://gw.example.com/team/v1", Model: "m"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "cx", Agent: "codex", API: "messages", BaseURL: "https://api.example.com/v1", Model: "m"}})
	want := map[string]string{"legacy": "https://gw.example.com/team", "cx": "https://api.example.com/v1"}
	check := func(what string, list []proto.ManagedLauncher) {
		t.Helper()
		if len(list) != len(want) {
			t.Fatalf("%s: %+v", what, list)
		}
		for _, l := range list {
			if l.BaseURL != want[l.Name] {
				t.Errorf("%s %s: %q, want %q", what, l.Name, l.BaseURL, want[l.Name])
			}
		}
	}
	ctx := context.Background()
	stored, err := s.readStoredLaunchers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pushed, err := s.nodeLaunchers(stored)
	if err != nil {
		t.Fatal(err)
	}
	check("pushed", pushed)
	keys, _ := s.readMachineKeys(ctx)
	catalog, legacy, err := s.nodeLaunchersFor(ctx, stored, keys, "mac")
	if err != nil {
		t.Fatal(err)
	}
	check("catalog", catalog)
	check("legacy", legacy)
	if storedBase(t, s, "legacy") != "https://gw.example.com/team/v1" {
		t.Fatal("delivering it rewrote the stored record")
	}
}
