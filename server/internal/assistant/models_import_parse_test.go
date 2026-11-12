package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/proto"
)

// Fake keys and hosts only: nothing here is a real gateway, key or header.
const (
	pasteKeyA = "sk-example-paste-a"
	pasteKeyB = "sk-example-paste-b"
)

// gateway is a Claude Code gateway draft's provider.
func gateway(base, auth, key string) pastedProvider {
	return pastedProvider{Kind: "launcher", Agent: "claude", API: "messages", BaseURL: base, Auth: auth, APIKey: key}
}

// withModels is p with models (the first is its model).
func withModels(p pastedProvider, ids ...string) pastedProvider {
	for _, id := range ids {
		p.Models = append(p.Models, proto.ProviderModel{ID: id})
	}
	if len(ids) > 0 {
		p.Model = ids[0]
	}
	return p
}

func checkPaste(t *testing.T, text string, want pasteResult) {
	t.Helper()
	got := parsePaste(text)
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		wj, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("got  %s\nwant %s", gj, wj)
	}
}

func notes(n ...pasteNote) []pasteNote { return n }

func TestParsePasteShell(t *testing.T) {
	kimi := gateway("https://gw.example.com", "bearer", pasteKeyA)
	kimi.Name, kimi.Model, kimi.Args = "kimi", "kimi-k3", []string{"--dangerously-skip-permissions"}
	kimi.Models = []proto.ProviderModel{{ID: "kimi-k3", ContextWindow: 1000000}}
	cases := []struct {
		name, text string
		want       pasteResult
	}{
		{
			name: "a single-quoted alias with a /v1 address",
			text: `alias kimi='ANTHROPIC_BASE_URL=https://gw.example.com/v1/ ANTHROPIC_AUTH_TOKEN="Bearer ` + pasteKeyA + `" ANTHROPIC_MODEL=kimi-k3[1m] claude --dangerously-skip-permissions'`,
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{{Label: "kimi", Aliases: []string{"kimi"}, Provider: kimi}}},
		},
		{
			name: "double quotes, no quotes, an alias without a key",
			text: "alias a=\"ANTHROPIC_BASE_URL=https://a.example.com ANTHROPIC_API_KEY=" + pasteKeyA + " claude\"\n" +
				`alias b=ANTHROPIC_BASE_URL=https://b.example.com\ claude`,
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{
				{Label: "a", Aliases: []string{"a"}, Provider: pastedProvider{Kind: "launcher", Agent: "claude", API: "messages", Name: "a",
					BaseURL: "https://a.example.com", Auth: "api_key", APIKey: pasteKeyA, Args: []string{}}},
				{Label: "b", Aliases: []string{"b"}, Provider: pastedProvider{Kind: "launcher", Agent: "claude", API: "messages", Name: "b",
					BaseURL: "https://b.example.com", Auth: "api_key", Args: []string{}}, Notes: notes(pasteNote{Code: noteNoKey})},
			}},
		},
		{
			name: "exports and a command line with its own variables",
			text: "export ANTHROPIC_BASE_URL=https://gw.example.com\nexport ANTHROPIC_AUTH_TOKEN=\"" + pasteKeyA + "\" DISABLE_TELEMETRY=1\n" +
				"ANTHROPIC_MODEL=glm-5 claude -c",
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "bearer", pasteKeyA), "glm-5")
					p.Args, p.Env = []string{"-c"}, "DISABLE_TELEMETRY=1"
					return p
				}(),
				EnvNames: []string{"DISABLE_TELEMETRY"},
			}}},
		},
		{
			name: ".env lines with quotes and comments",
			text: "# the team's gateway\nANTHROPIC_BASE_URL=\"https://gw.example.com/anthropic\"  # it's ours\n" +
				"export ANTHROPIC_API_KEY='" + pasteKeyA + "'\nAPI_TIMEOUT_MS=600000\nGATEWAY_LABEL=\"team blue\"\nAWS_SECRET_ACCESS_KEY=sk-example-aws\n",
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: func() pastedProvider {
					p := gateway("https://gw.example.com/anthropic", "api_key", pasteKeyA)
					p.TimeoutMS, p.Env = 600000, `GATEWAY_LABEL="team blue"`
					return p
				}(),
				EnvNames: []string{"GATEWAY_LABEL"}, SkippedEnv: []skippedEnv{{Name: "AWS_SECRET_ACCESS_KEY", Reason: "credential"}},
			}}},
		},
		{
			name: "line continuations, CRLF",
			text: "ANTHROPIC_BASE_URL=https://gw.example.com \\\r\n  ANTHROPIC_AUTH_TOKEN=" + pasteKeyA + " \\\r\n  claude --model x\r\nfi\r\n",
			want: pasteResult{Format: "shell", Notes: notes(pasteNote{Code: noteNotClaude, Line: 4}), Drafts: []pasteDraft{{
				Provider: func() pastedProvider {
					p := gateway("https://gw.example.com", "bearer", pasteKeyA)
					p.Args = []string{"--model", "x"}
					return p
				}(),
			}}},
		},
		{
			name: "aliases on one gateway and key are one draft; another key another",
			text: "alias k1='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=" + pasteKeyA + " ANTHROPIC_MODEL=m1 claude'\n" +
				"alias k2='ANTHROPIC_BASE_URL=https://gw.example.com/ ANTHROPIC_AUTH_TOKEN=" + pasteKeyA + " ANTHROPIC_MODEL=m2 claude'\n" +
				"alias k3='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=" + pasteKeyB + " ANTHROPIC_MODEL=m1 claude'\n",
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{
				{Label: "k1, k2", Aliases: []string{"k1", "k2"}, Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "bearer", pasteKeyA), "m1", "m2")
					p.Name, p.Args = "k1", []string{}
					return p
				}()},
				{Label: "k3", Aliases: []string{"k3"}, Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "bearer", pasteKeyB), "m1")
					p.Name, p.Args = "k3", []string{}
					return p
				}()},
			}},
		},
		{
			name: "aliases that differ in more than the model are a draft each",
			text: "export ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=" + pasteKeyA + "\n" +
				"alias d1='ANTHROPIC_MODEL=m1 claude'\nalias d2='CLAUDE_CODE_EFFORT_LEVEL=high ANTHROPIC_MODEL=m2 claude'\n",
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{
				{Label: "d1", Aliases: []string{"d1"}, Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "bearer", pasteKeyA), "m1")
					p.Name, p.Args = "d1", []string{}
					return p
				}()},
				{Label: "d2", Aliases: []string{"d2"}, Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "bearer", pasteKeyA), "m2")
					p.Name, p.Args, p.Effort = "d2", []string{}, "high"
					return p
				}()},
			}},
		},
		{
			name: "other aliases and commands, Claude Code's own sign-in, shell expansions, a user in the address",
			text: "alias ll='ls -la'\nalias cx='OPENAI_API_KEY=sk-example-codex codex'\nalias plain='claude'\n" +
				"alias v='ANTHROPIC_BASE_URL=$GATEWAY claude'\nalias u='ANTHROPIC_BASE_URL=https://user:pw-example@gw.example.com claude'\n" +
				"alias w='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=$(cat ~/.key) claude'\n" +
				"claude\nf() { claude; }\nalias -g\nsource ~/.zshrc.local\n",
			want: pasteResult{Format: "shell", Notes: notes( // in text order
				pasteNote{Code: noteNotClaude, Name: "ll"}, pasteNote{Code: noteNotClaude, Name: "cx"},
				pasteNote{Code: noteNoGateway, Name: "plain"}, pasteNote{Code: noteURLShell, Name: "v"}, pasteNote{Code: noteURLUser, Name: "u"},
				pasteNote{Code: noteNoGateway, Line: 7}, pasteNote{Code: noteLineUnused, Line: 8}, pasteNote{Code: noteLineUnused, Line: 9},
				pasteNote{Code: noteNotClaude, Line: 10},
			), Drafts: []pasteDraft{{Label: "w", Aliases: []string{"w"}, Provider: func() pastedProvider {
				p := gateway("https://gw.example.com", "bearer", "")
				p.Name, p.Args = "w", []string{}
				return p
			}(), Notes: notes(pasteNote{Code: noteKeyShell})}}},
		},
		{
			name: "aliases see every export, a command line only the ones before it",
			text: "alias late='claude'\nclaude\nexport ANTHROPIC_BASE_URL=https://gw.example.com\n",
			want: pasteResult{Format: "shell", Notes: notes(pasteNote{Code: noteNoGateway, Line: 2}), Drafts: []pasteDraft{{
				Label: "late", Aliases: []string{"late"}, Provider: func() pastedProvider {
					p := gateway("https://gw.example.com", "api_key", "")
					p.Name, p.Args = "late", []string{}
					return p
				}(), Notes: notes(pasteNote{Code: noteNoKey})}}},
		},
		{
			name: "an alias defined again counts where its last definition is",
			text: "alias k='ANTHROPIC_BASE_URL=https://old.example.com claude'\nalias j='ANTHROPIC_BASE_URL=https://j.example.com claude'\n" +
				"alias k='ANTHROPIC_BASE_URL=https://new.example.com claude'\n",
			want: pasteResult{Format: "shell", Notes: []pasteNote{}, Drafts: []pasteDraft{
				{Label: "j", Aliases: []string{"j"}, Provider: func() pastedProvider {
					p := gateway("https://j.example.com", "api_key", "")
					p.Name, p.Args = "j", []string{}
					return p
				}(), Notes: notes(pasteNote{Code: noteNoKey})},
				{Label: "k", Aliases: []string{"k"}, Provider: func() pastedProvider {
					p := gateway("https://new.example.com", "api_key", "")
					p.Name, p.Args = "k", []string{}
					return p
				}(), Notes: notes(pasteNote{Code: noteNoKey})},
			}},
		},
		{
			name: "variables without a gateway",
			text: "export ANTHROPIC_MODEL=opus\n",
			want: pasteResult{Format: "shell", Notes: notes(pasteNote{Code: noteNoGateway}), Drafts: []pasteDraft{}},
		},
		{
			name: "an address set to an expansion",
			text: "export ANTHROPIC_BASE_URL=\"${GATEWAY}\"\n",
			want: pasteResult{Format: "shell", Notes: notes(pasteNote{Code: noteURLShell, Line: 1}), Drafts: []pasteDraft{}},
		},
		{name: "variables of something else", text: "DATABASE_URL=postgres://db.example.com\n", want: pasteResult{Notes: notes(pasteNote{Code: noteUnrecognized}), Drafts: []pasteDraft{}}},
		{name: "prose", text: "hello there\nit's a key\n", want: pasteResult{Notes: notes(pasteNote{Code: noteUnrecognized}), Drafts: []pasteDraft{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkPaste(t, c.text, c.want) })
	}
}

func TestParsePasteSettings(t *testing.T) {
	cases := []struct {
		name, text string
		want       pasteResult
	}{
		{
			name: "env, model, apiKeyHelper, headers, values that are no strings",
			text: `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example.com/", "ANTHROPIC_AUTH_TOKEN": "` + pasteKeyA + `",
				"ANTHROPIC_CUSTOM_HEADERS": "X-Team: blue\nX-Api-Key: sk-example-hdr\nAuthorization: Basic sk-example-basic",
				"API_TIMEOUT_MS": 600000, "DISABLE_TELEMETRY": "1", "ENABLE_X": true},
				"model": "glm-5", "apiKeyHelper": "~/bin/key.sh", "permissions": {"allow": []}}`,
			want: pasteResult{Format: "settings", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "bearer", pasteKeyA), "glm-5")
					p.Headers, p.Env = "X-Team: blue", "DISABLE_TELEMETRY=1"
					return p
				}(),
				EnvNames:       []string{"DISABLE_TELEMETRY"},
				SkippedHeaders: []skippedHeader{{Name: "X-Api-Key", Reason: "credential"}, {Name: "Authorization", Reason: "credential"}},
				Notes:          notes(pasteNote{Code: noteKeyHelper}),
			}}},
		},
		{
			name: "env's model wins; no key",
			text: `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_MODEL": "m-env"}, "model": "m-top"}`,
			want: pasteResult{Format: "settings", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: withModels(gateway("https://gw.example.com", "api_key", ""), "m-env"), Notes: notes(pasteNote{Code: noteNoKey}),
			}}},
		},
		{name: "Claude Code's own sign-in", text: `{"model": "opus"}`, want: pasteResult{Format: "settings", Notes: notes(pasteNote{Code: noteNoGateway}), Drafts: []pasteDraft{}}},
		{name: "invalid JSON", text: `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example.com",`, want: pasteResult{Notes: notes(pasteNote{Code: noteInvalidJSON}), Drafts: []pasteDraft{}}},
		{name: "other JSON", text: `[{"env": {}}]`, want: pasteResult{Notes: notes(pasteNote{Code: noteUnrecognized}), Drafts: []pasteDraft{}}},
		{name: "an object of something else", text: `{"name": "x"}`, want: pasteResult{Notes: notes(pasteNote{Code: noteUnrecognized}), Drafts: []pasteDraft{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkPaste(t, c.text, c.want) })
	}
}

func TestParsePasteCurl(t *testing.T) {
	cases := []struct {
		name, text string
		want       pasteResult
	}{
		{
			name: "Messages: x-api-key, headers, a proxy, a JSON body over several lines",
			text: "curl https://gw.example.com/v1/messages \\\n  -H \"x-api-key: " + pasteKeyA + "\" \\\n" +
				"  -H 'anthropic-version: 2023-06-01' -H \"content-type: application/json\" \\\n" +
				"  -H \"X-Team: blue\" -H \"X-Org-Token: sk-example-org\" -x http://proxy.example.com:8080 --noproxy localhost \\\n" +
				"  -u user:pw-example -k -sS -X POST --cert /tmp/c.pem \\\n  -d '{\n    \"model\": \"claude-x\",\n    \"max_tokens\": 10\n  }'\n",
			want: pasteResult{Format: "curl", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: func() pastedProvider {
					p := withModels(gateway("https://gw.example.com", "api_key", pasteKeyA), "claude-x")
					p.Headers, p.Proxy, p.NoProxy = "X-Team: blue", "http://proxy.example.com:8080", "localhost"
					return p
				}(),
				SkippedHeaders: []skippedHeader{{Name: "anthropic-version", Reason: "protocol"}, {Name: "content-type", Reason: "protocol"},
					{Name: "X-Org-Token", Reason: "credential"}},
				Notes: notes(pasteNote{Code: noteCurlOption, Name: "-u"}, pasteNote{Code: noteCurlOption, Name: "-k"},
					pasteNote{Code: noteCurlOption, Name: "--cert"}),
			}}},
		},
		{
			name: "Chat Completions: Bearer, a direct API takes no headers or proxy",
			text: "KEY=1 curl -sSL https://api.example.com/v1/chat/completions -H \"Authorization: Bearer " + pasteKeyB + "\" -H \"X-Team: blue\" " +
				"-x http://proxy.example.com:1 --noproxy localhost --json '{\"model\":\"gpt-x\"}'\necho done\n",
			want: pasteResult{Format: "curl", Notes: notes(pasteNote{Code: noteLineUnused, Line: 2}), Drafts: []pasteDraft{{
				Provider:       withModels(pastedProvider{Kind: "api", API: "chat", BaseURL: "https://api.example.com/v1", Auth: "bearer", APIKey: pasteKeyB}, "gpt-x"),
				SkippedHeaders: []skippedHeader{{Name: "X-Team", Reason: "unsupported"}},
				Notes:          notes(pasteNote{Code: noteCurlOption, Name: "-x"}, pasteNote{Code: noteCurlOption, Name: "--noproxy"}),
			}}},
		},
		{
			name: "Responses, the key a variable",
			text: `curl https://api.example.com/prefix/v1/responses/ -H 'Authorization: Bearer $OPENAI_API_KEY'`,
			want: pasteResult{Format: "curl", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: pastedProvider{Kind: "api", API: "responses", BaseURL: "https://api.example.com/prefix/v1", Auth: "bearer"},
				Notes:    notes(pasteNote{Code: noteKeyShell}),
			}}},
		},
		{
			name: "models, -H run together",
			text: `curl "https://gw.example.com/anthropic/v1/models" -H"x-api-key: ` + pasteKeyA + `"`,
			want: pasteResult{Format: "curl", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: gateway("https://gw.example.com/anthropic", "api_key", pasteKeyA),
			}}},
		},
		{
			name: "messages without /v1, no key",
			text: `curl --url https://gw.example.com/api/messages -d @body.json`,
			want: pasteResult{Format: "curl", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: gateway("https://gw.example.com/api", "", ""), Notes: notes(pasteNote{Code: noteNoKey}),
			}}},
		},
		{
			name: "another path",
			text: `curl "https://gw.example.com/proxy/run/?debug=1#x" -H "x-api-key: ` + pasteKeyA + `"`,
			want: pasteResult{Format: "curl", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Provider: gateway("https://gw.example.com/proxy/run", "api_key", pasteKeyA),
				Notes:    notes(pasteNote{Code: noteCurlPath, Name: "/proxy/run/"}),
			}}},
		},
		{name: "the URL a variable", text: "# call it\ncurl $GATEWAY/v1/messages -H \"x-api-key: " + pasteKeyA + "\"",
			want: pasteResult{Format: "curl", Notes: notes(pasteNote{Code: noteURLShell, Line: 2}), Drafts: []pasteDraft{}}},
		{name: "a user in the URL", text: "curl https://user:pw-example@gw.example.com/v1/messages",
			want: pasteResult{Format: "curl", Notes: notes(pasteNote{Code: noteURLUser, Line: 1}), Drafts: []pasteDraft{}}},
		{name: "no URL", text: "curl -sS", want: pasteResult{Notes: notes(pasteNote{Code: noteUnrecognized}), Drafts: []pasteDraft{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkPaste(t, c.text, c.want) })
	}
}

func TestParsePasteCindy(t *testing.T) {
	cases := []struct {
		name, text string
		want       pasteResult
	}{
		{
			name: "one provider: its Messages endpoint, models as strings and objects, the other endpoint noted",
			text: `{"kind": "custom", "name": "Example AI", "auth": {"method": "apiKey", "apiKey": "` + pasteKeyA + `"},
				"endpoints": [
					{"protocol": "openai-responses", "baseUrl": "https://example.invalid/v1", "apiKey": "sk-example-codex"},
					{"protocol": "anthropic-messages", "baseUrl": "https://example.invalid/anthropic/v1", "headers": {"X-Tenant": "example"},
						"models": ["m1", {"id": "m2", "name": "M Two", "contextWindow": 1000000, "reasoningEfforts": ["low", "high", "turbo"],
							"reasoningDefaultEffort": "high"}, {"id": "m3", "reasoningEfforts": ["low"], "reasoningDefaultEffort": "max"}, "m1"]}
				]}`,
			want: pasteResult{Format: "cindy", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Label: "Example AI",
				Provider: pastedProvider{Kind: "launcher", Agent: "claude", API: "messages", DisplayName: "Example AI",
					BaseURL: "https://example.invalid/anthropic", Auth: "api_key", APIKey: pasteKeyA, Model: "m1", Headers: "X-Tenant: example",
					Models: []proto.ProviderModel{{ID: "m1"}, {ID: "m2", Name: "M Two", ContextWindow: 1000000, Efforts: []string{"low", "high"},
						DefaultEffort: "high"}, {ID: "m3", Efforts: []string{"low"}}}},
				Notes: notes(pasteNote{Code: noteCindyEndpoint, Name: "openai-responses"}),
			}}},
		},
		{
			name: "Responses only: a direct API, the provider's models",
			text: `{"name": "R", "auth": {"apiKey": "` + pasteKeyB + `"}, "models": ["r1"],
				"endpoints": [{"protocol": "openai-responses", "baseUrl": "https://example.invalid/v1/"}]}`,
			want: pasteResult{Format: "cindy", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Label:    "R",
				Provider: withModels(pastedProvider{Kind: "api", API: "responses", DisplayName: "R", BaseURL: "https://example.invalid/v1", Auth: "bearer", APIKey: pasteKeyB}, "r1"),
			}}},
		},
		{
			name: "Chat with headers, Google noted, no auth",
			text: `{"kind": "custom", "name": "C", "auth": {"method": "none"}, "endpoints": [
				{"protocol": "google-generative-ai", "baseUrl": "https://example.invalid/google"},
				{"protocol": "openai-chat", "baseUrl": "http://127.0.0.1:4000/v1", "headers": {"X-Tenant": "example", "X-Api-Key": "sk-example-h",
					"Content-Type": "application/json", "X-Count": 3}}]}`,
			want: pasteResult{Format: "cindy", Notes: []pasteNote{}, Drafts: []pasteDraft{{
				Label:    "C",
				Provider: pastedProvider{Kind: "api", API: "chat", DisplayName: "C", BaseURL: "http://127.0.0.1:4000/v1"},
				SkippedHeaders: []skippedHeader{{Name: "Content-Type", Reason: "protocol"}, {Name: "X-Api-Key", Reason: "credential"},
					{Name: "X-Tenant", Reason: "unsupported"}},
				Notes: notes(pasteNote{Code: noteCindyAuth, Name: "none"}, pasteNote{Code: noteCindyEndpoint, Name: "google-generative-ai"}),
			}}},
		},
		{
			name: "a batch: Google only, presets, built-in keys, OAuth, a user in the address, a good one",
			text: `{"providers": [
				{"kind": "custom", "name": "G", "auth": {"method": "apiKey", "apiKey": "sk-example-g"},
					"endpoints": [{"protocol": "google-generative-ai", "baseUrl": "https://example.invalid/g"}]},
				{"kind": "preset", "preset": "openrouter", "apiKey": "sk-example-p"},
				{"kind": "builtin", "provider": "gemini", "apiKey": "sk-example-b"},
				{"kind": "custom", "name": "O", "auth": {"method": "oauth", "tokenUrl": "https://example.invalid/token"},
					"endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://example.invalid/o"}]},
				{"kind": "custom", "name": "U", "auth": {"apiKey": "sk-example-u"},
					"endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://user:pw-example@example.invalid/u"}]},
				{"kind": "custom", "name": "Good", "auth": {"apiKey": "` + pasteKeyA + `"},
					"endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://example.invalid/good", "apiKey": "` + pasteKeyB + `"}]}
			]}`,
			want: pasteResult{Format: "cindy", Notes: notes(pasteNote{Code: noteCindyEndpoint, Name: "google-generative-ai"},
				pasteNote{Code: noteCindyKind, Name: "preset"}, pasteNote{Code: noteCindyKind, Name: "builtin"},
				pasteNote{Code: noteCindyAuth, Name: "oauth"}, pasteNote{Code: noteURLUser, Name: "U"}),
				Drafts: []pasteDraft{{Label: "Good", Provider: pastedProvider{Kind: "launcher", Agent: "claude", API: "messages", DisplayName: "Good",
					BaseURL: "https://example.invalid/good", Auth: "api_key", APIKey: pasteKeyB}}}},
		},
		{name: "invalid JSON", text: `{"kind": "custom", "endpoints": [}`, want: pasteResult{Notes: notes(pasteNote{Code: noteInvalidJSON}), Drafts: []pasteDraft{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkPaste(t, c.text, c.want) })
	}
}

// More than pasteMost drafts: the first ones, and too_many.
func TestParsePasteTooMany(t *testing.T) {
	var b strings.Builder
	var providers []string
	for i := range pasteMost + 1 {
		fmt.Fprintf(&b, "alias a%d='ANTHROPIC_BASE_URL=https://gw%d.example.com claude'\n", i, i)
		providers = append(providers, fmt.Sprintf(`{"name": "p%d", "endpoints": [{"protocol": "openai-chat", "baseUrl": "https://p%d.example.com/v1"}]}`, i, i))
	}
	for _, text := range []string{b.String(), `{"providers": [` + strings.Join(providers, ",") + `]}`} {
		res := parsePaste(text)
		if len(res.Drafts) != pasteMost || res.Drafts[pasteMost-1].Provider.BaseURL == "" ||
			!reflect.DeepEqual(res.Notes, notes(pasteNote{Code: noteTooMany})) {
			t.Fatalf("%d drafts, notes %+v", len(res.Drafts), res.Notes)
		}
	}
}

func pasteRequest(s *Service, body string) *httptest.ResponseRecorder {
	return request(s, "POST", "/api/v1/launchers/import/parse", body)
}

func TestParsePasteRouteRefuses(t *testing.T) {
	s, _, _ := modelService(t)
	missing := pasteRequest(s, `{}`)
	for _, c := range []struct {
		body string
		code int
	}{
		{`{}`, 400},
		{`{"text": " \n\t "}`, 400},
		{`{"text": 5}`, 400},
		{`{"text": "` + strings.Repeat("x", 300<<10) + `"}`, 413},
	} {
		r := pasteRequest(s, c.body)
		// A refused text says what is missing, the same way each time, never what the decoder read.
		if r.Code != c.code || c.code == 400 && r.Body.String() != missing.Body.String() {
			t.Fatalf("%.40s: %d %s", c.body, r.Code, r.Body.String())
		}
	}
	if r := pasteRequest(s, `{"text": "hello"}`); r.Code != 200 || strings.TrimSpace(r.Body.String()) != `{"format":"","drafts":[],"notes":[{"code":"unrecognized"}]}` {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
}

// Matches: every stored provider on the draft's base URL (stored with a /v1 or not), the ones with its key first; a
// key that is a reference never matches. A shell alias's suggested name avoids every name Persona lists.
func TestParsePasteRouteMatchesAndNames(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	seal := func(key string) string {
		sealed, err := s.sealKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return sealed
	}
	ref := extension.SealedReference("op://vault/item/key")
	stored := []storedLauncher{
		{KeySealed: seal(pasteKeyB), ManagedLauncher: proto.ManagedLauncher{Name: "other_key", BaseURL: "https://gw.example.com", Model: "x"}},
		{KeySealed: seal(ref), ManagedLauncher: proto.ManagedLauncher{Name: "by_ref", BaseURL: "https://gw.example.com", Model: "x"}},
		{KeySealed: seal(pasteKeyA), DisplayName: "Team gateway", ManagedLauncher: proto.ManagedLauncher{Name: "kimi", BaseURL: "https://gw.example.com/v1", Model: "x"}},
		{KeySealed: seal(pasteKeyA), ManagedLauncher: proto.ManagedLauncher{Name: "elsewhere", BaseURL: "https://other.example.com", Model: "x"}},
		{ManagedLauncher: proto.ManagedLauncher{Name: "keyless", BaseURL: "https://gw.example.com", Model: "x"}},
	}
	if err := s.putSetting(ctx, launchersManagedKey, stored); err != nil {
		t.Fatal(err)
	}
	text := "alias kimi='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=" + pasteKeyA + " claude'\n" +
		"alias kimi_2='ANTHROPIC_BASE_URL=https://gw.example.com claude'\n"
	cindy := `{"name": "R", "auth": {"apiKey": "` + strings.ReplaceAll(ref, "\x00", `\u0000`) + `"},
		"endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://gw.example.com"}]}`
	for _, c := range []struct {
		text  string
		names []string
		want  [][]pasteMatch
	}{
		{text, []string{"kimi-2", "kimi_2"}, [][]pasteMatch{
			{{Name: "kimi", DisplayName: "Team gateway", SameKey: true}, {Name: "other_key"}, {Name: "by_ref"}, {Name: "keyless"}},
			{{Name: "other_key"}, {Name: "by_ref"}, {Name: "kimi", DisplayName: "Team gateway"}, {Name: "keyless"}},
		}},
		{cindy, []string{""}, [][]pasteMatch{{{Name: "other_key"}, {Name: "by_ref"}, {Name: "kimi", DisplayName: "Team gateway"}, {Name: "keyless"}}}},
	} {
		body, _ := json.Marshal(map[string]string{"text": c.text})
		r := pasteRequest(s, string(body))
		var out pasteResult
		if err := json.Unmarshal(r.Body.Bytes(), &out); r.Code != 200 || err != nil || len(out.Drafts) != len(c.want) {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
		for i, d := range out.Drafts {
			if d.Provider.Name != c.names[i] || !reflect.DeepEqual(d.Matches, c.want[i]) {
				t.Fatalf("draft %d: %q %+v", i, d.Provider.Name, d.Matches)
			}
		}
		if strings.Contains(r.Body.String(), pasteKeyB) || strings.Contains(r.Body.String(), "op://") && !strings.Contains(c.text, "op://") {
			t.Fatalf("a stored key left the manager: %s", r.Body.String())
		}
	}
}

// The answer carries the pasted key and no other secret of the text: not a credential variable, header, a proxy's or
// address's password, or curl's -u.
func TestParsePasteRouteKeepsSecrets(t *testing.T) {
	s, _, _ := modelService(t)
	const secret = "pw-example-secret"
	for _, text := range []string{
		"alias s='ANTHROPIC_BASE_URL=https://gw.example.com ANTHROPIC_AUTH_TOKEN=" + pasteKeyA + " AWS_SECRET_ACCESS_KEY=" + secret +
			" HTTPS_PROXY=http://u:" + secret + "@proxy.example.com:1 ANTHROPIC_CUSTOM_HEADERS=\"X-Org-Token: " + secret + "\" claude'\n" +
			"alias t='ANTHROPIC_BASE_URL=https://u:" + secret + "@gw.example.com claude'\n",
		`{"env": {"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": "` + pasteKeyA + `", "MY_PASSWORD": "` + secret + `",
			"ANTHROPIC_CUSTOM_HEADERS": "Authorization: Bearer ` + secret + `"}}`,
		"curl https://gw.example.com/v1/messages -H 'x-api-key: " + pasteKeyA + "' -H 'X-Session-Cookie: " + secret + "' -u u:" + secret +
			" -x http://u:" + secret + "@proxy.example.com:1 -H 'Authorization: Bearer " + secret + "'",
		"curl https://u:" + secret + "@gw.example.com/v1/messages",
		`{"providers": [{"name": "A", "auth": {"apiKey": "` + pasteKeyA + `"}, "endpoints": [{"protocol": "anthropic-messages",
			"baseUrl": "https://gw.example.com", "headers": {"X-Auth-Token": "` + secret + `"}}]},
			{"name": "B", "endpoints": [{"protocol": "anthropic-messages", "baseUrl": "https://u:` + secret + `@gw.example.com"}]}]}`,
	} {
		body, _ := json.Marshal(map[string]string{"text": text})
		r := pasteRequest(s, string(body))
		if r.Code != 200 || strings.Contains(r.Body.String(), secret) {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
		if strings.Contains(text, pasteKeyA) && !strings.Contains(r.Body.String(), pasteKeyA) {
			t.Fatalf("the pasted key is not in the answer: %s", r.Body.String())
		}
	}
}

// alias on the import reads only that alias of the machine: one it does not have previews nothing and imports nothing.
func TestLauncherImportOneAlias(t *testing.T) {
	s, mux := importTestHub(t, importAliases, nil)
	get := func(alias string) []importedProvider {
		t.Helper()
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest("GET", "/api/v1/launchers/import?machine=m1&alias="+alias, nil))
		var out struct {
			Providers []importedProvider `json:"providers"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &out); r.Code != 200 || err != nil || out.Providers == nil {
			t.Fatalf("%d %s", r.Code, r.Body.String())
		}
		return out.Providers
	}
	if got := get("gcc2"); len(got) != 1 || !reflect.DeepEqual(got[0].Aliases, []string{"gcc2"}) || got[0].Variants != nil {
		t.Fatalf("%+v", got)
	}
	if got := get("nope"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	if got := get(""); len(got) != 2 {
		t.Fatalf("without alias: %+v", got)
	}
	if r := importRequest(mux, "POST", `{"machine":"m1","alias":"kcc","providers":["gcc"]}`); r.Code != 400 {
		t.Fatalf("an alias not asked for imported: %d %s", r.Code, r.Body.String())
	}
	r := importRequest(mux, "POST", `{"machine":"m1","alias":"gcc2","providers":["gcc2"]}`)
	if r.Code != 200 || strings.TrimSpace(r.Body.String()) != `{"created":["gcc2"],"updated":[],"hidden":[],"kept":[]}` {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if list := s.storedLaunchers(context.Background()); len(list) != 1 || len(list[0].Models) != 1 || list[0].Models[0].ID != "glm-air" {
		t.Fatalf("%+v", list)
	}
}
