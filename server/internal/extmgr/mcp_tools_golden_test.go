package extmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/connector/github"
	"github.com/nwverse/persona/server/internal/connector/imessage"
	"github.com/nwverse/persona/server/internal/mail"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// golden compares got with testdata/prompts/<name>.golden (UPDATE_GOLDEN=1 writes it; docs/assistant/prompts.md).
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompts", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (UPDATE_GOLDEN=1 writes it)", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

// The MCP tools of the manager's the hub offers (hubConnectorTools: the connectors', Cua's, ego's and the
// subscriptions'), as they go over the wire (the golden is indented for reading). The hub lists them with its own (its mcp_tools golden):
// tools for every agent, assistant_tools after the assistant's own.
func TestGoldenConnectorTools(t *testing.T) {
	h := newTestManager(t)
	show := func(assistant bool) string {
		raw, _ := json.Marshal(map[string]any{"assistant": assistant})
		out, err := testHubCall(context.Background(), h, hubConnectorTools, raw)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	h.mail = (*mail.Service)(nil) // Tools() reads nothing from the service
	// Subscriptions run in every manager: their tools are the assistant's (subscription_tools.go).
	h.subs = subscriptions.New(subscriptions.Options{Store: subscriptions.NewMemStore(), Connectors: subscriptionConnectors{h}})
	register := func() { // the connectors as the manager registers them (mail's and Feishu's tools come from them)
		h.connectors = connector.NewRegistry(nil)
		h.registerConnectors()
	}
	register()
	golden(t, "connector_tools.assistant", show(true))
	golden(t, "connector_tools.agent", show(false))
	// With GitHub signed in: its read tools, for the assistant only.
	withoutGitHub(h) // every manager registers a GitHub, signed out until the user signs in
	if err := h.addConnector(signedInGitHub{github.New(github.Config{}, "Mac", nil)}); err != nil {
		t.Fatal(err)
	}
	golden(t, "connector_tools.assistant_with_github", show(true))
	golden(t, "connector_tools.agent", show(false))
	// With iMessage handed to Persona (the switch is on by default): its read tools too.
	register()
	if err := h.addConnector(readableIMessage{imessage.NewRemote(imessage.Config{Machine: "mac"}, "Mac", nil)}); err != nil {
		t.Fatal(err)
	}
	golden(t, "connector_tools.assistant_with_imessage", show(true))
	golden(t, "connector_tools.agent", show(false))
}

// withoutGitHub leaves the registered connectors but GitHub (the test adds one of its own).
func withoutGitHub(h *Manager) {
	r := connector.NewRegistry(nil)
	for _, c := range h.connectors.All() {
		if c.ID() != "github" {
			_ = r.Add(c)
		}
	}
	h.connectors = r
}

type readableIMessage struct{ *imessage.Remote }

func (readableIMessage) Status(context.Context) connector.Status {
	return connector.Status{State: connector.StateOK}
}

type signedInGitHub struct{ *github.Connector }

func (signedInGitHub) Status(context.Context) connector.Status {
	return connector.Status{State: connector.StateOK}
}
