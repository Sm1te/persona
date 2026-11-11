package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/lark"
	larkcliadapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/rpc"
)

type fakeHostCLI struct {
	version     string
	auth        larkcliadapter.AuthStatus
	chats       larkcliadapter.ChatList
	search      json.RawMessage
	searchLimit int
	err         error
}

func (f *fakeHostCLI) CheckVersion(context.Context) error { return nil }
func (f *fakeHostCLI) Version(context.Context) (string, error) {
	return f.version, f.err
}
func (f *fakeHostCLI) AuthStatus(context.Context) (larkcliadapter.AuthStatus, error) {
	return f.auth, f.err
}
func (f *fakeHostCLI) ListChats(context.Context, int) (larkcliadapter.ChatList, error) {
	return f.chats, f.err
}
func (f *fakeHostCLI) ListMessages(context.Context, string, string, string, int) (larkcliadapter.MessagePage, error) {
	return larkcliadapter.MessagePage{}, f.err
}
func (f *fakeHostCLI) SearchLimit(_ context.Context, _, _ string, limit int) (json.RawMessage, error) {
	f.searchLimit = limit
	return f.search, f.err
}

func TestExtensionHostCLIRequiresConfiguredGrantAndDeclaration(t *testing.T) {
	h := newTestManager(t)
	spec := extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.72"}
	if _, err := h.extensionHostCLICall(context.Background(), "example", spec, []byte(`{"operation":"auth_status"}`)); err == nil {
		t.Fatal("an integration without manager consent must fail")
	}
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Extensions: true}}
	if _, err := h.extensionHostCLICall(context.Background(), "example", extension.HostIntegrationSpec{ID: "other", Version: ">=1.0.0"}, []byte(`{}`)); err == nil {
		t.Fatal("an undeclared integration must fail")
	}
	h.hostCLIState = &hostCLIState{runner: &fakeHostCLI{version: "1.0.98", auth: larkcliadapter.AuthStatus{LoggedIn: true, UserName: "Example", AppID: "not-exported"}}}
	got, err := h.extensionHostCLICall(context.Background(), "example", spec, []byte(`{"operation":"auth_status"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	var out struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Result["logged_in"] != true || out.Result["user_name"] != "Example" {
		t.Fatalf("unexpected auth status: %s", raw)
	}
	if _, exists := out.Result["app_id"]; exists {
		t.Fatalf("app binding crossed the typed boundary: %s", raw)
	}
	if _, err := h.extensionHostCLICall(context.Background(), "example", spec, []byte(`{"operation":"search","kind":"docs","query":"handbook"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionHostCLISearchForwardsLimit(t *testing.T) {
	h := newTestManager(t)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Extensions: true}}
	runner := &fakeHostCLI{version: "1.0.98", search: json.RawMessage(`{"results":[]}`)}
	h.hostCLIState = &hostCLIState{runner: runner}
	spec := extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.0"}
	for _, tc := range []struct {
		name  string
		limit int
	}{
		{name: "requested", limit: 3},
		{name: "default", limit: 0},
		{name: "manager maximum", limit: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(hostCLIRequest{Operation: "search", Kind: "docs", Query: "example", Limit: tc.limit})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.extensionHostCLICall(context.Background(), "example", spec, raw); err != nil {
				t.Fatal(err)
			}
			if runner.searchLimit != tc.limit {
				t.Fatalf("search limit = %d, want %d", runner.searchLimit, tc.limit)
			}
		})
	}
}

func TestExtensionHostCLIRejectsVersionsAndUnsafeOperations(t *testing.T) {
	h := newTestManager(t)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Extensions: true}}
	h.hostCLIState = &hostCLIState{runner: &fakeHostCLI{version: "1.0.71", search: json.RawMessage(`{"ok":true}`)}}
	for name, raw := range map[string]string{
		"unknown":  `{"operation":"shell","query":"anything"}`,
		"write":    `{"operation":"send","chat_id":"chat","query":"text"}`,
		"kind":     `{"operation":"search","kind":"unknown","query":"x"}`,
		"no_query": `{"operation":"search","kind":"docs"}`,
		"no_chat":  `{"operation":"messages"}`,
		"limit":    `{"operation":"chats","limit":51}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h.extensionHostCLICall(context.Background(), "example", extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.0"}, []byte(raw)); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	if _, err := h.extensionHostCLICall(context.Background(), "example", extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.72"}, []byte(`{"operation":"chats"}`)); err == nil {
		t.Fatal("an unsupported CLI version accepted")
	}
}

func TestExtensionHostCLILimitsAndRedactsFailures(t *testing.T) {
	h := newTestManager(t)
	h.cfg.Lark = lark.Config{CLI: &lark.CLIConfig{Extensions: true}}
	large := make([]byte, (1<<20)+1)
	for i := range large {
		large[i] = 'x'
	}
	h.hostCLIState = &hostCLIState{runner: &fakeHostCLI{version: "1.0.98", search: large}}
	if _, err := h.extensionHostCLICall(context.Background(), "example", extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.0"}, []byte(`{"operation":"search","kind":"docs","query":"x"}`)); err == nil {
		t.Fatal("an oversized result accepted")
	}
	h.hostCLIState = &hostCLIState{runner: &fakeHostCLI{version: "1.0.98", err: &larkcliadapter.Error{Kind: larkcliadapter.KindTimeout}}}
	_, err := h.extensionHostCLICall(context.Background(), "example", extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.0"}, []byte(`{"operation":"auth_status"}`))
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != extension.CodeTimeout {
		t.Fatalf("timeout classification: %v", err)
	}
}

func TestHostIntegrationSpec(t *testing.T) {
	for _, spec := range []extension.HostIntegrationSpec{{ID: "unknown", Version: ">=1.0.0"}, {ID: "lark-cli", Version: ""}, {ID: "lark-cli", Version: "1.0.0"}} {
		if err := spec.Validate(); err == nil {
			t.Fatalf("invalid spec accepted: %+v", spec)
		}
	}
	spec := extension.HostIntegrationSpec{ID: "lark-cli", Version: ">=1.0.72 <2.0.0"}
	if !spec.Allows("1.0.98") || spec.Allows("1.0.71") || spec.Allows("2.0.0") || spec.Allows("not-a-version") {
		t.Fatal("version range is not enforced")
	}
}
