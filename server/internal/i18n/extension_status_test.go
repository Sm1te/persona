package i18n

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtensionIdentityPreviewConsentIsLocalizedAndIdempotent(t *testing.T) {
	raw := []byte(`{"preview_id":"fixture","rename_from":"old-name","manifest":{"id":"new-name","name":"Author name"},"warnings":["Author diagnostic"]}`)
	for _, lang := range []Lang{En, ZhHans, Ja} {
		projected := ExtensionClientJSON(lang, raw)
		var body struct {
			Warnings []string `json:"warnings"`
			Manifest struct {
				Name string `json:"name"`
			} `json:"manifest"`
		}
		if err := json.Unmarshal(projected, &body); err != nil {
			t.Fatal(err)
		}
		want := T(lang, "extension-identity.confirmation", Args{"from": "old-name", "to": "new-name"})
		if len(body.Warnings) != 2 || body.Warnings[0] != want || body.Warnings[1] != "Author diagnostic" || body.Manifest.Name != "Author name" {
			t.Fatalf("identity disclosure %s: %+v", lang, body)
		}
		if string(ExtensionClientJSON(lang, projected)) != string(projected) {
			t.Fatal("replay duplicated the identity warning")
		}
	}
}

func TestExtensionStatusCopies(t *testing.T) {
	for _, l := range []Lang{En, ZhHans, Ja} {
		if got := ExtensionStatusText(l, "no such plugin"); got != T(l, "extension-status.missing_extension") || got == "no such plugin" {
			t.Fatalf("explicit %s retained the legacy missing-extension text: %q", l, got)
		}
		for _, tc := range []struct {
			key  string
			args Args
		}{
			{"extension-status.package_missing", Args{"path": "/example/package"}},
			{"extension-status.unknown_point", Args{"name": "author-point"}},
			{"extension-status.dev_engines", Args{"required": ">=10"}},
			{"extension-status.sandbox_no_bwrap", nil},
			{"extension-status.sandbox_probe_dir", Args{"err": "filesystem diagnostic: permission denied"}},
			{"extension-sandbox.probe_tmp", Args{"err": "filesystem diagnostic: permission denied"}},
			{"extension-market_git.git_missing", nil},
			{"extension-market_git.git_too_old", Args{"version": "2.30"}},
		} {
			for _, source := range []Lang{En, ZhHans, Ja} {
				text := T(source, tc.key, tc.args)
				if got := ExtensionStatusText(l, text); got != T(l, tc.key, tc.args) {
					t.Fatalf("%s to %s, %s: %q", source, l, tc.key, got)
				}
			}
		}
		unknown := "SDK diagnostic: title is invalid"
		if summary, detail := ExtensionFailureText(l, unknown, "extension-status.view_invalid"); summary != T(l, "extension-status.view_invalid") || detail != unknown {
			t.Fatalf("diagnostic separation %s: %q / %q", l, summary, detail)
		}
		if got := ExtensionStatusText(l, "arbitrary author content"); got != "arbitrary author content" {
			t.Fatal("unknown author text was rewritten")
		}
	}
}

func TestExtensionAuthEventProjection(t *testing.T) {
	for _, l := range []Lang{En, ZhHans, Ja} {
		for _, owned := range []bool{false, true} {
			canonical := "not answering"
			event := map[string]any{"type": "extension_auth", "extension": "example", "hub_auth_message": owned,
				"event_seq": 42, "auth": map[string]any{"state": "error", "title": "作者名称", "account": "author account", "message": canonical}}
			raw, _ := json.Marshal(event)
			var out map[string]any
			if err := json.Unmarshal(ExtensionAuthEvent(l, raw), &out); err != nil {
				t.Fatal(err)
			}
			auth := out["auth"].(map[string]any)
			want := canonical
			if owned {
				want = T(l, "extension-status.not_answering")
			}
			if auth["message"] != want || auth["title"] != "作者名称" || auth["account"] != "author account" || out["event_seq"] != float64(42) || out["hub_auth_message"] != nil {
				t.Fatalf("%s owned %v: %v", l, owned, out)
			}
			if !strings.Contains(string(raw), `"hub_auth_message"`) {
				t.Fatal("canonical event mutated")
			}
		}
	}
}

func TestExtensionOperationAndPreviewProjection(t *testing.T) {
	raw := []byte(`{"type":"extension_operation","operation":{"id":"example","kind":"install","error":"SDK error: invalid package","steps":[{"id":"nodes","machines":{"errors":{"example-machine":"SSH diagnostic: refused"}}}],"preview":{"preview_id":"example","manifest":{"name":"作者名称","description":"permissions.hub_api is the old way: it gets the hub's full token; ask for the scopes it uses under permissions.hub"},"warnings":["permissions.hub_api is the old way: it gets the hub's full token; ask for the scopes it uses under permissions.hub"]}}}`)
	for _, l := range []Lang{En, ZhHans, Ja} {
		var out struct {
			Operation struct {
				Error, ErrorDetail string
				Steps              []struct {
					Machines struct{ Errors, ErrorDetails map[string]string }
				}
				Preview struct {
					Manifest map[string]string
					Warnings []string
				}
			}
		}
		// Underscored diagnostic keys are checked using a map below; the summary is checked through the typed envelope.
		projected := ExtensionEvent(l, raw)
		if err := json.Unmarshal(projected, &out); err != nil {
			t.Fatal(err)
		}
		if out.Operation.Error != T(l, "extension-status.operation_failed") || out.Operation.Preview.Manifest["name"] != "作者名称" || out.Operation.Preview.Warnings[0] != T(l, "extension-status.legacy_full_token") || out.Operation.Steps[0].Machines.Errors["example-machine"] != T(l, "extension-status.operation_failed") {
			t.Fatalf("%s: %s", l, projected)
		}
		if !strings.Contains(string(projected), `"error_detail":"SDK error: invalid package"`) || !strings.Contains(string(projected), `"error_details":{"example-machine":"SSH diagnostic: refused"}`) || !strings.Contains(string(projected), `"description":"permissions.hub_api is the old way`) {
			t.Fatalf("raw diagnostic/author content lost: %s", projected)
		}
	}
}
