package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// viaDevs answers model.http as a node would, or with err.
type viaDevs struct {
	Devices
	calls atomic.Int32
	err   error
	saw   proto.ModelHTTPParams
}

func (d *viaDevs) Has(id string) bool         { return id == "server" }
func (d *viaDevs) Canonical(id string) string { return id }
func (d *viaDevs) List() []devices.Summary    { return nil }

func (d *viaDevs) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	d.calls.Add(1)
	if method != proto.MethodModelHTTP || id != "server" {
		return nil, fmt.Errorf("unexpected %s on %s", method, id)
	}
	if d.err != nil {
		return nil, d.err
	}
	if err := json.Unmarshal(params, &d.saw); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, d.saw.URL, bytes.NewReader(d.saw.Body))
	req.Header = d.saw.Header
	req.Header.Set("X-Sent-By", "node")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return json.Marshal(proto.ModelHTTPAnswer{Status: resp.StatusCode, Header: resp.Header, Body: body})
}

const viaTestKey = "sk-via-test-0123456789"

// An API connection with via_machine sends its streamed request through that computer's node with the key, and reads
// the same answer; without it the manager sends it as before and no node is asked. An offline computer and an old
// node fail with errors that say so, and no error carries the key.
func TestAPIConnectionSendsThroughAComputer(t *testing.T) {
	var direct, viaNodeHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Sent-By") == "node" {
			viaNodeHits.Add(1)
		} else {
			direct.Add(1)
		}
		if r.Header.Get("x-api-key") != viaTestKey {
			http.Error(w, "no key", 401)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, ev := range []string{`{"type":"message_start","message":{"model":"m"}}`, `{"type":"content_block_start","content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hel"}}`, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, `{"type":"message_stop"}`} {
			fmt.Fprintf(w, "data: %s\n\n", ev)
			w.(http.Flusher).Flush()
		}
	}))
	defer upstream.Close()

	s, _, _ := modelService(t)
	devs := &viaDevs{}
	save := func(method, path string, body map[string]any) (int, string) {
		raw, _ := json.Marshal(body)
		s.devs = devs // only for the call: the rest of the service has no fleet here
		defer func() { s.devs = nil }()
		return launcherCall(t, s, method, path, string(raw))
	}
	conn := map[string]any{"name": "direct", "kind": "api", "api": "messages", "base_url": upstream.URL, "model": "m", "api_key": viaTestKey, "auth": "api_key"}
	if code, res := save("POST", "/api/v1/launchers", conn); code != 200 {
		t.Fatalf("create: %d %s", code, res)
	}
	ask := func() (string, error) {
		c, err := s.apiConfig(context.Background(), ModelBinding{Provider: "launcher.direct"})
		if err != nil {
			return "", err
		}
		s.devs = devs // only for the call: the rest of the service has no fleet here
		defer func() { s.devs = nil }()
		return s.aiClient(c).Complete(context.Background(), "sys", "hi", 64)
	}

	// Unset: the manager sends it itself.
	if text, err := ask(); err != nil || text != "hello" || direct.Load() != 1 || devs.calls.Load() != 0 {
		t.Fatalf("direct: %q %v (direct %d, node calls %d)", text, err, direct.Load(), devs.calls.Load())
	}

	// Set: through the computer's node, streamed answer read the same.
	delete(conn, "api_key")
	conn["via_machine"] = "server"
	if code, res := save("PUT", "/api/v1/launchers/direct", conn); code != 200 {
		t.Fatalf("set via_machine: %d %s", code, res)
	}
	if code, res := launcherCall(t, s, "GET", "/api/v1/launchers", ""); code != 200 || !strings.Contains(res, `"via_machine":"server"`) {
		t.Fatalf("list: %d %s", code, res)
	}
	if text, err := ask(); err != nil || text != "hello" || viaNodeHits.Load() != 1 || direct.Load() != 1 || devs.calls.Load() != 1 {
		t.Fatalf("via: %q %v (via node %d, direct %d)", text, err, viaNodeHits.Load(), direct.Load())
	}
	if devs.saw.URL != upstream.URL+"/v1/messages" || !strings.Contains(string(devs.saw.Body), `"stream":true`) || devs.saw.TimeoutMS <= 0 {
		t.Fatalf("model.http params: %+v", devs.saw)
	}
	// Test connection goes the same way; the form's choice overrides the stored one.
	probe := func(in probeInput) map[string]any {
		s.devs = devs
		defer func() { s.devs = nil }()
		return s.providerTest(context.Background(), in)
	}
	if out := probe(probeInput{Name: "direct"}); out["ok"] != true || viaNodeHits.Load() != 2 || direct.Load() != 1 {
		t.Fatalf("test through the computer: %v (via node %d, direct %d)", out, viaNodeHits.Load(), direct.Load())
	}
	here := ""
	if out := probe(probeInput{Name: "direct", ViaMachine: &here}); out["ok"] != true || viaNodeHits.Load() != 2 || direct.Load() != 2 {
		t.Fatalf("test from the manager: %v (via node %d, direct %d)", out, viaNodeHits.Load(), direct.Load())
	}

	// Offline, unknown and old: errors that say so, never with the key.
	lang := s.lang(context.Background())
	offline := i18n.T(lang, "assistant-models.err_via_offline", i18n.Args{"name": "server"})
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("server: %w", apierr.ErrOffline), offline},
		{rpc.ErrClosed, offline},
		{fmt.Errorf("server: %w", devices.ErrNoMachine), i18n.Localize(errViaMachine("server"), lang)},
		{&rpc.Error{Code: proto.CodeNodeOutdated, Message: "This computer’s node can’t send model requests yet. Update the node"}, "Update the node"},
	} {
		devs.err = tc.err
		_, err := ask()
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), viaTestKey) {
			t.Fatalf("%v: got %v, want it to say %q", tc.err, err, tc.want)
		}
	}
	if direct.Load() != 2 {
		t.Fatal("a failed call through the computer went out directly")
	}

	// Cleared: back to the manager.
	devs.err = nil
	conn["via_machine"] = ""
	if code, res := save("PUT", "/api/v1/launchers/direct", conn); code != 200 {
		t.Fatalf("clear via_machine: %d %s", code, res)
	}
	if _, err := ask(); err != nil || direct.Load() != 3 {
		t.Fatalf("cleared: %v (direct %d)", err, direct.Load())
	}

	// Refused: an unknown computer, or a launcher connection.
	if code, _ := save("PUT", "/api/v1/launchers/direct", map[string]any{"name": "direct", "kind": "api", "api": "messages", "base_url": upstream.URL, "model": "m", "via_machine": "nowhere"}); code != 400 {
		t.Fatalf("unknown computer: %d", code)
	}
	if code, _ := save("POST", "/api/v1/launchers", map[string]any{"name": "cli", "base_url": upstream.URL, "model": "m", "api_key": "k", "via_machine": "server"}); code != 400 {
		t.Fatalf("launcher with via_machine: %d", code)
	}
}
