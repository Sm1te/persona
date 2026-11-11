package assistant

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// The startup ID the hub gives a provider added without a preset, and the display name the pickers carry (#5029,
// docs/integrations/launchers.md "Display names").

func TestLauncherSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Team Relay":     "team-relay",
		"我的中转 站":         "",
		"智谱 GLM":         "glm",
		"gw.example.com": "gw-example-com",
		"1min relay":     "p-1min-relay",
		"  My -- Relay!": "my-relay",
		"My_Gateway":     "my_gateway",
		// Cut to 40 bytes, and the - the cut leaves at the end goes too.
		"abcdefghij abcdefghij abcdefghij abcdef xyz": "abcdefghij-abcdefghij-abcdefghij-abcdef",
	} {
		got := launcherSlug(in)
		if got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
		if got != "" && (!managedLauncherName.MatchString(got) || len(got) > 42) {
			t.Errorf("%q: %q breaks the name rule", in, got)
		}
	}
}

// addCustom is POST /api/v1/launchers without a preset, with a model so the rest of the checks pass.
func addCustom(t *testing.T, s *Service, fields string) (int, LauncherView) {
	t.Helper()
	code, out := postLauncher(t, s, "en", `{`+fields+`,"model":"m"}`)
	var v LauncherView
	if code == 200 {
		if err := json.Unmarshal(out["launcher"], &v); err != nil {
			t.Fatal(err)
		}
	}
	return code, v
}

// longHost is a gateway host longer than a display name may be (72 characters).
var longHost = strings.Repeat("a", 60) + ".example.com"

func TestAddWithoutAPresetGetsAName(t *testing.T) {
	s, f, _ := modelService(t)
	f.launcherList = []proto.Launcher{{Name: "rc-relay", Source: "zshrc"}}
	for _, c := range []struct {
		fields, name, display string
	}{
		// From the display name; one with nothing left of it in the rule (Chinese) goes by the host, keeping its name.
		{`"display_name":"我的中转 站","base_url":"https://relay.example.com"`, "relay-example-com", "我的中转 站"},
		{`"display_name":"Team Relay","base_url":"https://gw.example.com"`, "team-relay", "Team Relay"},
		{`"name":"","display_name":" Team Relay ","base_url":"https://gw.example.com"`, "team-relay-2", "Team Relay"},
		// No display name: the host names it, numbered as the ID is.
		{`"display_name":"","base_url":"https://gw.example.com/v1"`, "gw-example-com", "gw.example.com"},
		{`"base_url":"https://GW.example.com:8443"`, "gw-example-com-2", "gw.example.com 2"},
		// Reserved for a route, taken by an rc file's launcher, a leading digit, too long.
		{`"display_name":"Test","base_url":"https://gw.example.com"`, "test-2", "Test"},
		{`"display_name":"RC relay","base_url":"https://gw.example.com"`, "rc-relay-2", "RC relay"},
		{`"display_name":"1min relay","base_url":"https://gw.example.com"`, "p-1min-relay", "1min relay"},
		{`"display_name":"A very long name for the gateway our whole team shares","base_url":"https://gw.example.com"`,
			"a-very-long-name-for-the-gateway-our-who", "A very long name for the gateway our whole team shares"},
		// A host longer than a display name may be: cut to fit, the number kept.
		{`"base_url":"https://` + longHost + `"`, strings.Repeat("a", 40), strings.Repeat("a", 60) + ".exa"},
		{`"base_url":"https://` + longHost + `/v1"`, strings.Repeat("a", 40) + "-2", strings.Repeat("a", 60) + ".e 2"},
	} {
		code, v := addCustom(t, s, c.fields)
		if code != 200 || v.Name != c.name || v.DisplayName != c.display || v.Source != "persona" {
			t.Errorf("%s: %d %q %q, want %q %q", c.fields, code, v.Name, v.DisplayName, c.name, c.display)
		}
	}
	// A name given keeps its checks.
	for fields, want := range map[string]int{
		`"name":"team-relay","base_url":"https://gw.example.com"`: http.StatusConflict,
		`"name":"rc-relay","base_url":"https://gw.example.com"`:   http.StatusConflict,
		`"name":"test","base_url":"https://gw.example.com"`:       http.StatusBadRequest,
		`"name":"my relay","base_url":"https://gw.example.com"`:   http.StatusBadRequest,
	} {
		if code, _ := addCustom(t, s, fields); code != want {
			t.Errorf("%s: %d, want %d", fields, code, want)
		}
	}
	if code, _ := addCustom(t, s, `"name":"own","display_name":"","base_url":"https://own.example.com"`); code != 200 {
		t.Fatalf("a name given: %d", code)
	}
	if v := managedView(t, s, "own"); v.DisplayName != "" {
		t.Fatalf("a name given gets no display name: %+v", v)
	}
}

func managedView(t *testing.T, s *Service, name string) LauncherView {
	t.Helper()
	var out struct {
		Launcher LauncherView `json:"launcher"`
	}
	if err := json.Unmarshal(getJSON(t, s, "/api/v1/launchers/"+name), &out); err != nil {
		t.Fatal(err)
	}
	return out.Launcher
}

// PUT renames a provider for people; its ID stays, and leaving display_name out keeps it.
func TestPutRenamesAProvider(t *testing.T) {
	s, _, _ := modelService(t)
	if code, _ := addCustom(t, s, `"display_name":"Team Relay","base_url":"https://gw.example.com"`); code != 200 {
		t.Fatalf("add: %d", code)
	}
	for _, body := range []string{
		`{"name":"other","display_name":" 团队中转 ","base_url":"https://gw.example.com","model":"m"}`,
		`{"base_url":"https://gw.example.com","model":"m2"}`,
	} {
		if r := request(s, "PUT", "/api/v1/launchers/team-relay", body); r.Code != 200 {
			t.Fatalf("%s: %d %s", body, r.Code, r.Body)
		}
		if v := managedView(t, s, "team-relay"); v.DisplayName != "团队中转" {
			t.Fatalf("%s: %+v", body, v)
		}
	}
	if r := request(s, "GET", "/api/v1/launchers/other", ""); r.Code != http.StatusNotFound {
		t.Fatalf("the name in a PUT's body renamed the ID: %d", r.Code)
	}
}

// New Agent's picker carries a managed provider's display name; an rc file's launcher (one by a provider's name on a
// computer out of its scope too) and a provider without one carry none.
func TestMachineLaunchersCarryDisplayNames(t *testing.T) {
	s, f := scopeService(t)
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "rc-relay", Source: "zshrc", Agent: "claude"}}
	for _, fields := range []string{
		`"display_name":"团队中转","base_url":"https://gw.example.com"`,
		`"name":"plain","base_url":"https://gw.example.com"`,
		`"name":"box","display_name":"Box gateway","base_url":"https://box.example.com","share":"selected","scope_machines":["server"]`,
	} {
		if code, _ := addCustom(t, s, fields); code != 200 {
			t.Fatalf("%s: %d", fields, code)
		}
	}
	// mac's rc file got a box of its own after the provider was added.
	f.zshrc = map[string][]proto.Launcher{"mac": {{Name: "claude"}, {Name: "box", Source: "zshrc", Agent: "claude"}}}
	shown := func(path string) map[string]any {
		t.Helper()
		var out struct {
			Launchers []map[string]any `json:"launchers"`
		}
		if err := json.Unmarshal(getJSON(t, s, path), &out); err != nil {
			t.Fatal(err)
		}
		names := map[string]any{}
		for _, l := range out.Launchers {
			names[l["name"].(string)] = l["display_name"]
		}
		return names
	}
	if got := shown("/api/v1/machines/server/launchers"); got["gw-example-com"] != "团队中转" || got["box"] != "Box gateway" ||
		got["plain"] != nil || got["rc-relay"] != nil || len(got) != 5 {
		t.Fatalf("server: %v", got)
	}
	if got := shown("/api/v1/machines/server/launchers?purpose=assistant"); got["gw-example-com"] != "团队中转" || got["plain"] != nil {
		t.Fatalf("the main chat's picker: %v", got)
	}
	if got := shown("/api/v1/machines/mac/launchers"); len(got) != 4 || got["box"] != nil || got["gw-example-com"] != "团队中转" {
		t.Fatalf("mac, where box is its rc file's: %v", got)
	}
}
