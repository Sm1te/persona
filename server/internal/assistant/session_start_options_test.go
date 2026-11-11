package assistant

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// Start options are answered by the manager, not a new hub business route.
// The machine's configured launcher is the only evidence for a launcher absent
// from the node; query parameters cannot supply it.
func TestWorkSessionOptionsManagerPreservesScope(t *testing.T) {
	f, s, _ := guestService(t)
	f.devicesFull = true
	f.hello.Models.Launchers = map[string][]ConfiguredLauncher{
		"server": {{Launcher: proto.Launcher{Name: "work"}, AgentType: "codex"}},
	}
	f.nodes = map[string]nodeAnswer{}
	var got map[string]any
	f.nodes["session.start.options"] = func(_ string, raw json.RawMessage) (any, error) {
		_ = json.Unmarshal(raw, &got)
		return map[string]any{"agent_type": "codex", "models": []proto.AgentModel{{ID: "chosen"}}}, nil
	}
	r := serve(s, http.MethodGet, "/api/v1/machines/server/launchers/work/start-options?configured_agent=other", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"chosen"`) {
		t.Fatalf("%d %s", r.Code, r.Body)
	}
	if got["name"] != "work" || got["configured"] != true || got["configured_agent"] != "codex" {
		t.Fatalf("node target: %+v", got)
	}
}

func TestWorkSessionOptionsManagerRefusesExcludedMachine(t *testing.T) {
	f, s, _ := guestService(t)
	f.devicesFull = true
	f.devicesOther = []string{"server"}
	f.nodes = map[string]nodeAnswer{}
	called := false
	f.nodes["session.start.options"] = func(_ string, _ json.RawMessage) (any, error) {
		called = true
		return map[string]any{}, nil
	}
	r := serve(s, http.MethodGet, "/api/v1/machines/server/launchers/work/start-options", "")
	if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "machine_pending") || called {
		t.Fatalf("%d %s (called %v)", r.Code, r.Body, called)
	}
	f.devicesFull = false
	r = serve(s, http.MethodGet, "/api/v1/machines/server/launchers/work/start-options", "")
	if r.Code != http.StatusConflict || called {
		t.Fatalf("an incomplete device list: %d %s (called %v)", r.Code, r.Body, called)
	}
}

func TestWorkSessionOptionsManagerUsesOnlyItsNode(t *testing.T) {
	f, s, _ := guestService(t)
	f.devicesFull = true
	f.nodes = map[string]nodeAnswer{}
	called := false
	f.nodes["session.start.options"] = func(_ string, raw json.RawMessage) (any, error) {
		called = true
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["configured"] != nil || p["configured_agent"] != nil {
			t.Errorf("untrusted input reached node: %+v", p)
		}
		return map[string]any{}, nil
	}
	r := serve(s, http.MethodGet, "/api/v1/machines/server/launchers/other/start-options?configured=1&configured_agent=codex", "")
	if r.Code != http.StatusOK || !called {
		t.Fatalf("%d %s (called %v)", r.Code, r.Body, called)
	}
}
