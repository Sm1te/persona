package extmgr

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Always rules for hand-offs: one per reference × launcher × computer, for the provider that claims the reference; the
// provider's details list them, one can be removed there, and they go with the provider's uninstall.
func TestHandoffRules(t *testing.T) {
	h, _ := extensionTestHub(t)
	mux := h.routes()
	master := h.cfg.Token
	ctx := context.Background()
	const ref = "exv://Example/item/ok"
	if h.HandoffRuleCovers(ctx, ref, "claude", "box") {
		t.Fatal("covered before any provider or rule")
	}
	_, pv := call(t, mux, "POST", "/api/v1/plugins/-/preview", master,
		`{"source": "dir:`+secretProviderPackage(t, "example-vault", "exv", filepath.Join(t.TempDir(), "calls.jsonl"))+`"}`)
	if c, body := call(t, mux, "POST", "/api/v1/plugins/-/install", master, fmt.Sprintf(`{"preview_id": %q, "permissions_digest": %q}`, pv["preview_id"], pv["permissions_digest"])); c != 200 {
		t.Fatalf("install: %d %v", c, body)
	}
	waitLong(t, "the provider found", func() bool { _, ok := h.SecretProviderOf(ref); return ok })
	if err := h.AddHandoffRules(ctx, []HandoffRule{{Reference: ref, Launcher: "claude", Machine: "box", Provider: "example-vault", By: "device:d1", At: time.Now()},
		{Reference: ref, Launcher: "claude", Machine: "other", Provider: "another-vault", At: time.Now()}, // not the provider now
		{Reference: "nope://x/y", Launcher: "claude", Machine: "box", Provider: "example-vault"}}); err != nil {
		t.Fatal(err)
	}
	if !h.HandoffRuleCovers(ctx, ref, "claude", "box") || h.HandoffRuleCovers(ctx, ref, "claude", "other") ||
		h.HandoffRuleCovers(ctx, ref, "gateway", "box") {
		t.Fatal("a rule covers only its reference, launcher and computer")
	}
	_, detail := call(t, mux, "GET", "/api/v1/extensions/example-vault/-/", master, "")
	rules, _ := detail["handoff_rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("the provider's details list %v", detail["handoff_rules"])
	}
	rule := rules[0].(map[string]any)
	if rule["reference"] != ref || rule["launcher"] != "claude" || rule["machine"] != "box" || rule["by"] != "device:d1" {
		t.Fatalf("rule %v", rule)
	}
	if code, _ := call(t, mux, "DELETE", "/api/v1/extensions/example-vault/-/handoff-rules/0123456789abcdef", master, ""); code != 404 {
		t.Fatalf("removing no such rule: %d", code)
	}
	if code, out := call(t, mux, "DELETE", "/api/v1/extensions/example-vault/-/handoff-rules/"+rule["id"].(string), master, ""); code != 200 || out["handoff_rules"] != nil {
		t.Fatalf("removing the rule: %d %v", code, out["handoff_rules"])
	}
	if h.HandoffRuleCovers(ctx, ref, "claude", "box") {
		t.Fatal("a removed rule still covers")
	}
	// A rule older than the provider's install (one a removal left behind, an id taken again) covers nothing.
	if err := h.AddHandoffRules(ctx, []HandoffRule{{Reference: ref, Launcher: "claude", Machine: "box", Provider: "example-vault",
		At: time.Now().Add(-24 * time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	if h.HandoffRuleCovers(ctx, ref, "claude", "box") {
		t.Fatal("a rule from before the install covers")
	}
	// A provider whose id changes takes its rules along.
	if err := h.moveHandoffRules(ctx, "example-vault", "example-vault-2"); err != nil {
		t.Fatal(err)
	}
	if list, _ := h.handoffRules(ctx); len(list) != 1 || list[0].Provider != "example-vault-2" {
		t.Fatalf("moved: %+v", list)
	}
	if err := h.moveHandoffRules(ctx, "example-vault-2", "example-vault"); err != nil {
		t.Fatal(err)
	}
	if err := h.AddHandoffRules(ctx, []HandoffRule{{Reference: ref, Launcher: "claude", Machine: "box", Provider: "example-vault", At: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, mux, "DELETE", "/api/v1/extensions/example-vault/-/", master, ""); code != 200 {
		t.Fatalf("uninstall: %d", code)
	}
	waitLong(t, "the rules gone with the provider", func() bool {
		list, _ := h.handoffRules(ctx)
		return len(list) == 0
	})
}
