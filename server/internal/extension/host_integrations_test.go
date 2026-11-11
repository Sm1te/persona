package extension

import "testing"

func TestHostIntegrationsManifest(t *testing.T) {
	base := `{"manifest_version":1,"id":"example","name":"Example","version":"1.0.0","engines":{"plugin_protocol":1},"runtime":{"command":"bin/example"},"contributes":{"assistant":[{"id":"example","instructions":"assistant.md"}]}`
	for name, tc := range map[string]struct {
		insert string
		ok     bool
	}{
		"valid":   {`,"host_integrations":[{"id":"lark-cli","version":">=1.0.72 <2.0.0"}]`, true},
		"unknown": {`,"host_integrations":[{"id":"other","version":">=1.0.0"}]`, false},
		"version": {`,"host_integrations":[{"id":"lark-cli","version":"1.0.0"}]`, false},
		"field":   {`,"host_integrations":[{"id":"lark-cli","version":">=1.0.0","command":"anything"}]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			m, err := Parse([]byte(base + tc.insert + `}`))
			if (err == nil) != tc.ok {
				t.Fatalf("Parse: %v, manifest %v", err, m)
			}
		})
	}
	m, err := Parse([]byte(base + `,"host_integrations":[{"id":"lark-cli","version":">=1.0.72"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := hostIntegrationConsent(m); len(got) != 1 || got[0] != "lark-cli" {
		t.Fatalf("consent: %v", got)
	}
}
