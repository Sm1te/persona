package proto

import "testing"

func TestMessagesBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://relay.example.net/v1":               "https://relay.example.net",
		" https://relay.example.net/v1/ ":            "https://relay.example.net",
		"https://relay.example.net":                  "https://relay.example.net",
		"https://relay.example.net/":                 "https://relay.example.net",
		"https://relay.example.net/team/v1":          "https://relay.example.net/team",
		"https://relay.example.net/team":             "https://relay.example.net/team",
		"https://api.deepseek.com/anthropic/v1":      "https://api.deepseek.com/anthropic",
		"https://api.deepseek.com/anthropic":         "https://api.deepseek.com/anthropic",
		"http://127.0.0.1:8080/v1":                   "http://127.0.0.1:8080",
		"https://relay.example.net/v1/v1":            "https://relay.example.net",
		"https://relay.example.net/v1//v1/":          "https://relay.example.net",
		"https://relay.example.net/v10":              "https://relay.example.net/v10",
		"https://relay.example.net/api/v1beta":       "https://relay.example.net/api/v1beta",
		"https://relay.example.net/apiv1":            "https://relay.example.net/apiv1",
		"https://relay.example.net/v1/messages":      "https://relay.example.net/v1/messages",
		"https://v1":                                 "https://v1", // a host named v1 is not a path
		"https://relay.example.net/v1?team=a":        "https://relay.example.net/v1?team=a",
		"https://relay.example.net/v1#top":           "https://relay.example.net/v1#top",
		"relay.example.net/v1":                       "relay.example.net/v1",
		"":                                           "",
		"https://relay.example.net/%E4%B8%AD/v1":     "https://relay.example.net/%E4%B8%AD",
		"https://relay.example.net:8443/gateway/v1/": "https://relay.example.net:8443/gateway",
	} {
		got := MessagesBaseURL(in)
		if got != want {
			t.Errorf("MessagesBaseURL(%q) = %q, want %q", in, got, want)
		}
		if again := MessagesBaseURL(got); again != got {
			t.Errorf("not idempotent: %q → %q → %q", in, got, again)
		}
	}
}

func TestMessagesBase(t *testing.T) {
	for _, c := range []struct {
		l    ManagedLauncher
		want bool
	}{
		{ManagedLauncher{}, true},
		{ManagedLauncher{API: "messages"}, true},
		{ManagedLauncher{Agent: "claude", API: "messages"}, true},
		{ManagedLauncher{Agent: "omp"}, true}, // gets ANTHROPIC_BASE_URL too (node managedEnv)
		{ManagedLauncher{Kind: "api", API: "messages"}, true},
		{ManagedLauncher{Kind: "api", API: "responses"}, false},
		{ManagedLauncher{Kind: "api", API: "chat"}, false},
		{ManagedLauncher{Agent: "codex", API: "messages"}, false}, // OPENAI_BASE_URL keeps its /v1
		{ManagedLauncher{Agent: "agy"}, false},
	} {
		if got := c.l.MessagesBase(); got != c.want {
			t.Errorf("%+v: MessagesBase() = %v, want %v", c.l, got, c.want)
		}
	}
}
