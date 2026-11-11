package extmgr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// A device's own items (#3848): only a paired full-access device pushes, its source is made on the first push, and the
// apps are told the hub takes them.
func TestSubscriptionPushRoute(t *testing.T) {
	h := newTestManager(t)
	mux := http.NewServeMux()
	h.subscriptionRoutes(mux)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.startSubscriptions(ctx)
	call := func(p principal, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/subscriptions/-/push", strings.NewReader(body))
		mux.ServeHTTP(rec, req.WithContext(withPrincipal(req.Context(), p)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	item := `{"items":[{"key":"k1","title":"Parcel arrives today","app":"Example Post"}]}`
	if code, out := call(principal{ID: "dev_watch", Role: roleAnswer}, item); code != http.StatusForbidden || out["code"] != "read_only_device" {
		t.Fatalf("from an answer-only device: %d %v", code, out)
	}
	if code, out := call(principal{Role: roleFull}, item); code != http.StatusBadRequest || out["code"] != "bad_request" {
		t.Fatalf("without a device: %d %v", code, out)
	}
	phone := principal{ID: "dev_phone", Name: "Test iPhone", Role: roleFull}
	code, out := call(phone, item)
	sub, _ := out["subscription"].(map[string]any)
	if code != http.StatusOK || out["new"] != 1.0 || sub["connector"] != "-shortcuts" || sub["source"] != "device:dev_phone" || sub["state"] != "off" {
		t.Fatalf("a push: %d %v", code, out)
	}
	if code, out := call(phone, item); code != http.StatusOK || out["new"] != 0.0 || out["count"] != 1.0 {
		t.Fatalf("the same item again: %d %v", code, out)
	}
	if code, out := call(phone, `{"items":[{"app":"Example"}]}`); code != http.StatusBadRequest || out["code"] != "bad_request" {
		t.Fatalf("an item with no words: %d %v", code, out)
	}
	run := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/subscriptions/"+sub["id"].(string)+"/run", nil)
	mux.ServeHTTP(run, req.WithContext(withPrincipal(req.Context(), phone)))
	if run.Code != http.StatusBadRequest || !strings.Contains(run.Body.String(), "push_source") {
		t.Fatalf("run now on a push source: %d %s", run.Code, run.Body.String())
	}
	if h.extensions != nil && !slices.Contains(h.features(), subscriptionPushFeature) {
		t.Fatalf("features without %s: %v", subscriptionPushFeature, h.features())
	}
}
