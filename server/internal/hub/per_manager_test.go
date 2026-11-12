package hub

// What is each manager's own beside its machines (docs/assistant/managers.md "Directories on a computer", "Ownership"): its
// assistant's directories on a computer, and its extensions' tokens.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// A conversation is the assistant's only in the directories of the computer's manager: the built-in's assistant and
// health on its computer, another manager's assistant-<id> and health-<id> on its own. The built-in's directory left
// on a computer moved to another manager is not the new one's; the hub asks that computer for the new manager's
// skills in its own directory.
func TestAssistantDirectoriesAreTheMachinesManagers(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "ma", Name: "ma", Kind: "local"}, {ID: "mb", Name: "mb", Kind: "local"}}
	a := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, quiet)
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", func(method string, _ json.RawMessage) (any, error) {
		if method == devicewire.MethodCall {
			return map[string]any{"skills": []map[string]any{{"name": "b-skill", "source": "user"}}}, nil
		}
		return map[string]any{}, nil
	})
	if _, err := h.moveMachine(context.Background(), "mb", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	info := proto.NodeInfo{Home: "/home/me"}
	a.report(t, devicewire.State{Machine: proto.Machine{ID: "ma", Name: "ma"}, Info: info})
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"}, Info: info})
	eventually(t, 5*time.Second, "ma, mb", func() bool { return h.localMachine("ma") != nil && h.localMachine("mb") != nil })
	ma, mb := h.localMachine("ma"), h.localMachine("mb")
	// Each machine's manager's directory on it.
	cwdA, cwdB := ma.assistantCwdOf(h.machineOwner("ma")), mb.assistantCwdOf(h.machineOwner("mb"))
	if cwdA != "/home/me/.persona/assistant" || cwdB != "/home/me/.persona/assistant-m-0b0b0b" {
		t.Fatalf("directories: %q %q", cwdA, cwdB)
	}
	for _, c := range []struct {
		m    *Machine
		cwd  string
		want bool
	}{
		{ma, "/home/me/.persona/assistant", true},
		{ma, "/home/me/.persona/health", true},
		{ma, "/home/me/.persona/assistant-m-0b0b0b", false},
		{mb, "/home/me/.persona/assistant-m-0b0b0b", true},
		{mb, "/home/me/.persona/health-m-0b0b0b", true},
		{mb, "/home/me/.persona/assistant", false}, // the built-in's, left from before mb moved
		{mb, "/home/me/code", false},
	} {
		if got := c.m.isAssistant(proto.Session{SessionID: "s", Cwd: c.cwd}); got != c.want {
			t.Errorf("%s %s: %v", c.m.cfg.ID, c.cwd, got)
		}
	}
	var res assistantwire.SnapshotResult
	if err := b.call(t, assistantwire.MethodSnapshot, assistantwire.Snapshot{Epoch: "b", Seq: 1,
		Profile: &assistantwire.Profile{MachineID: "mb"}}, &res); err != nil {
		t.Fatal(err)
	}
	if code, out := ownershipGet(t, h, srv, http.MethodGet, "/api/v1/skills?manager=m-0b0b0b", nil); code != http.StatusOK ||
		!strings.Contains(b.log(), `"cwd":"/home/me/.persona/assistant-m-0b0b0b"`) {
		t.Fatalf("B's skills: %d %v\n%s", code, out, b.log())
	}
}

// An extension's token is its manager's: a request with it that names no manager goes to that manager (a route of
// no object, a merged list: its manager's only, an API prefix several serve), not to the default one.
func TestAnExtensionsTokenIsItsManagers(t *testing.T) {
	h, srv, _ := newBusHub(t)
	knows := func(who string, known bool) func(string, json.RawMessage) (any, error) {
		return func(method string, _ json.RawMessage) (any, error) {
			switch method {
			case extwire.MethodAuthorize:
				if known {
					return extwire.Authorized{OK: true, Extension: "bx", Name: "BX"}, nil
				}
				return extwire.Authorized{}, nil
			case assistantwire.MethodHTTP:
				return map[string]any{"status": 200, "body": []byte(`{"who":"` + who + `","items":[{"id":"` + who + `"}]}`)}, nil
			}
			return map[string]any{}, nil
		}
	}
	a := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, knows("a", false))
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", knows("b", true))
	h.keepExtReport(assistantwire.AssistantID, &extwire.Report{EventsDeclared: true, APIs: []extwire.APIPrefix{{Prefix: "bx"}}})
	h.keepExtReport("m-0b0b0b", &extwire.Report{EventsDeclared: true, APIs: []extwire.APIPrefix{{Prefix: "bx"}}})
	get := func(path string) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+extTokenPrefix+"bx-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %v", path, resp.StatusCode, out)
		}
		return out
	}
	if out := get("/api/v1/assistant"); out["who"] != "b" {
		t.Fatalf("a route of no object went to %v", out["who"])
	}
	if items, _ := get("/api/v1/inbox")["items"].([]any); len(items) != 1 || items[0].(map[string]any)["manager"] != "m-0b0b0b" {
		t.Fatalf("the merged list: %v", items)
	}
	// An API prefix both serve: the hub hands the request to B's extension (extensions.accept), not A's.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/bx/things", nil)
	req.Header.Set("Authorization", "Bearer "+extTokenPrefix+"bx-token")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	if !strings.Contains(b.log(), extwire.MethodAccept) || strings.Contains(a.log(), extwire.MethodAccept) {
		t.Fatalf("the prefix both serve went to\nA:\n%s\nB:\n%s", a.log(), b.log())
	}
}

// An extension's token acts only on its own manager's machines, sessions and cards, as its manager does: a route
// about another manager's machine (forwarded or the hub's own), session or pending card is refused 403
// other_manager before anything is done; its own manager's go through. The apps' devices are as before.
func TestAnExtensionsTokenOnlyOnItsManagersThings(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "ma", Name: "ma", Kind: "local"}, {ID: "mb", Name: "mb", Kind: "local"}}
	answer := func(who string, knows bool) func(string, json.RawMessage) (any, error) {
		return func(method string, _ json.RawMessage) (any, error) {
			switch method {
			case extwire.MethodAuthorize:
				if knows {
					return extwire.Authorized{OK: true, Extension: "bx", Name: "BX"}, nil
				}
				return extwire.Authorized{}, nil
			case assistantwire.MethodHTTP:
				return map[string]any{"status": 200, "body": []byte(`{"who":"` + who + `"}`)}, nil
			}
			return map[string]any{"who": who}, nil
		}
	}
	a := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, answer("a", false))
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", answer("b", true))
	if _, err := h.moveMachine(context.Background(), "mb", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	a.report(t, devicewire.State{Machine: proto.Machine{ID: "ma", Name: "ma"}})
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"}})
	eventually(t, 5*time.Second, "ma, mb", func() bool { return h.localMachine("ma") != nil && h.localMachine("mb") != nil })
	h.takeFleetSessions(assistantwire.FleetSessions{MachineID: "ma", Sessions: []proto.Session{{MachineID: "ma", SessionID: "s3"}}})
	for _, f := range []struct {
		g  *guardManager
		ix proto.Interaction
	}{{a, proto.Interaction{ID: "ix_a", MachineID: "ma", SessionID: "s3", Kind: proto.IxPermission}},
		{b, proto.Interaction{ID: "ix_b", MachineID: "mb", SessionID: "b1", Kind: proto.IxPermission}}} {
		if err := f.g.peer.Notify(assistantwire.EventFleetInteraction, assistantwire.FleetInteraction{Interaction: f.ix}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, 5*time.Second, "both cards", func() bool { return h.interactionOwner("ix_a") != "" && h.interactionOwner("ix_b") != "" })
	do := func(tok, method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	ext := extTokenPrefix + "bx-token"
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/sessions?summary=0&limit=50&machine_id=ma&session_id=s3", ""}, // exact lookup is not an ownership bypass
		{http.MethodGet, "/api/v1/machines/ma/herdr", ""},                                       // forwarded by machine
		{http.MethodPost, "/api/v1/sessions/ma/s3/messages", `{"text":"x"}`},                    // the hub's own, by session
		{http.MethodPost, "/api/v1/machines/ma/confirm", ""},                                    // machines/{id}
		{http.MethodPost, "/api/v1/interactions/ix_a/resolve", `{"allow":true}`},                // A's card
	} {
		if code, out := do(ext, c.method, c.path, c.body); code != http.StatusForbidden || out["code"] != codeOtherManager {
			t.Fatalf("%s %s with B's extension: %d %v", c.method, c.path, code, out)
		}
	}
	for _, l := range strings.Split(a.log(), "\n") { // asked whose token it is (extensions.authorize), nothing else
		if l != "" && !strings.HasPrefix(l, extwire.MethodAuthorize+" ") && !strings.HasPrefix(l, "assistant.tools") &&
			!strings.Contains(l, `"method":"fs.watch"`) && // (the hub's own on ma's connection)
			!strings.HasPrefix(l, devicewire.MethodEngineGet+" ") { // (the move's own: mb's engine choice)
			t.Fatalf("A was reached: %s", l)
		}
	}
	if code, out := do(ext, http.MethodGet, "/api/v1/sessions?machine_id=mb&session_id=missing", ""); code != http.StatusOK || out["total"] != float64(0) {
		t.Fatalf("B's exact lookup with B's extension: %d %v", code, out)
	}
	if code, out := do(ext, http.MethodGet, "/api/v1/machines/mb/herdr", ""); code != http.StatusOK || out["who"] != "b" {
		t.Fatalf("B's machine with B's extension: %d %v", code, out)
	}
	if code, out := do(ext, http.MethodPost, "/api/v1/interactions/ix_b/resolve", `{"allow":true}`); code != http.StatusOK || out["who"] != "b" {
		t.Fatalf("B's card with B's extension: %d %v", code, out)
	}
	// An app's device is as before: A's machine goes to A.
	if code, out := do(h.cfg.Token, http.MethodGet, "/api/v1/machines/ma/herdr", ""); code != http.StatusOK || out["who"] != "a" {
		t.Fatalf("the app on A's machine: %d %v", code, out)
	}
}

// routeValues reads the values the API mux will set, before it serves the request.
func TestRouteValues(t *testing.T) {
	r := httptest.NewRequest(http.MethodPut, "/api/v1/todos/t1/spaces/m%20a/dir/sub", nil)
	route, v := routeValues("PUT /api/v1/todos/{id}/spaces/{mid}/{space...}", r)
	if route != "/api/v1/todos/{id}/spaces/{mid}/{space...}" || v["id"] != "t1" || v["mid"] != "m a" || v["space"] != "dir/sub" {
		t.Fatalf("%q %v", route, v)
	}
	if _, v := routeValues("GET /api/v1/machines/{mid}/herdr", httptest.NewRequest(http.MethodGet, "/api/v1/machines/ma/herdr", nil)); v["mid"] != "ma" {
		t.Fatalf("%v", v)
	}
}

// With two managers, a health report uploaded to a machine goes in its manager's health space: to the built-in's
// machine as ever, to B's machine with B's health_dir (health-m-0b0b0b), each through its own manager.
func TestHealthUploadsWithTwoManagers(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.cfg.Machines = []MachineConfig{{ID: "ma", Name: "ma", Kind: "local"}, {ID: "mb", Name: "mb", Kind: "local"}}
	node := func(method string, params json.RawMessage) (any, error) {
		if method != devicewire.MethodCall {
			return map[string]any{}, nil
		}
		var c devicewire.NodeCall
		_ = json.Unmarshal(params, &c)
		var p struct {
			HealthDir string `json:"health_dir"`
		}
		_ = json.Unmarshal(c.Params, &p)
		switch c.Method {
		case "file.upload.begin":
			out := map[string]any{"upload_id": "u1"}
			if p.HealthDir != "" {
				out["health_dir"] = p.HealthDir
			}
			return out, nil
		case "file.upload.end":
			return proto.UploadedFile{Path: "/r/report.pdf", Name: "report.pdf", Size: 3}, nil
		}
		return map[string]any{}, nil
	}
	a := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, node)
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", node)
	if _, err := h.moveMachine(context.Background(), "mb", "m-0b0b0b"); err != nil {
		t.Fatal(err)
	}
	info := proto.NodeInfo{Home: "/home/me", Version: "v1", Protocol: nodeproto.Version, Capabilities: nodeproto.Announced}
	a.report(t, devicewire.State{Machine: proto.Machine{ID: "ma", Name: "ma"}, Info: info})
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"}, Info: info})
	eventually(t, 5*time.Second, "ma, mb", func() bool { return h.localMachine("ma") != nil && h.localMachine("mb") != nil })
	post := func(mid string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/machines/"+mid+"/files?name=report.pdf&dest=health", strings.NewReader("pdf"))
		req.Header.Set("Authorization", "Bearer "+h.cfg.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("upload to %s: %d", mid, resp.StatusCode)
		}
	}
	post("ma")
	post("mb")
	begins := func(f *guardManager) []string {
		var out []string
		for _, l := range strings.Split(f.log(), "\n") {
			if strings.Contains(l, `"method":"file.upload.begin"`) {
				out = append(out, l)
			}
		}
		return out
	}
	if got := begins(a); len(got) != 1 || strings.Contains(got[0], "health_dir") || !strings.Contains(got[0], `"machine_id":"ma"`) {
		t.Fatalf("the built-in's machine: %v", got)
	}
	if got := begins(b); len(got) != 1 || !strings.Contains(got[0], `"health_dir":"health-m-0b0b0b"`) || !strings.Contains(got[0], `"machine_id":"mb"`) {
		t.Fatalf("B's machine: %v", got)
	}
}
