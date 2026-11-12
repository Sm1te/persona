package node

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// legacyManagedEnv is managedEnv as it was before #2455, word for word: a launcher without the new fields must get
// exactly this.
func legacyManagedEnv(l proto.ManagedLauncher) []string {
	small := l.SmallModel
	if small == "" {
		small = l.Model
	}
	env := []string{"ANTHROPIC_BASE_URL=" + l.BaseURL}
	switch {
	case l.APIKey == "":
	case l.BearerAuth():
		env = append(env, "ANTHROPIC_AUTH_TOKEN="+l.APIKey, "ANTHROPIC_API_KEY=")
	default:
		env = append(env, "ANTHROPIC_API_KEY="+l.APIKey, "ANTHROPIC_AUTH_TOKEN=")
	}
	if l.Model != "" {
		for _, k := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
			"ANTHROPIC_DEFAULT_SONNET_MODEL"} {
			env = append(env, k+"="+l.Model)
		}
	}
	if subagent := cmp.Or(l.SubagentModel, l.Model); subagent != "" {
		env = append(env, "CLAUDE_CODE_SUBAGENT_MODEL="+subagent)
	}
	if l.CustomModel != "" {
		env = append(env, "ANTHROPIC_CUSTOM_MODEL_OPTION="+l.CustomModel)
		if l.CustomModelName != "" {
			env = append(env, "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME="+l.CustomModelName)
		}
		if l.CustomModelDescription != "" {
			env = append(env, "ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION="+l.CustomModelDescription)
		}
	}
	if small != "" {
		env = append(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL="+small, "ANTHROPIC_SMALL_FAST_MODEL="+small)
	}
	return append(env, artifactsOff)
}

func TestManagedEnvUnchangedForOldLaunchers(t *testing.T) {
	const gw = "https://gw.example.com"
	for _, l := range []proto.ManagedLauncher{
		{},
		{Name: "a", BaseURL: gw},
		{Name: "a", BaseURL: gw, APIKey: "sk-test-1"},
		{Name: "a", BaseURL: gw, APIKey: "sk-test-1", Auth: "bearer"},
		{Name: "a", BaseURL: gw, APIKey: "sk-test-1", Auth: "api_key", Model: "m1"},
		{Name: "a", BaseURL: gw, APIKey: "sk-test-1", Model: "m1", SmallModel: "s1", SubagentModel: "sub"},
		{Name: "a", BaseURL: gw, Model: "m1", CustomModel: "c1"},
		{Name: "a", BaseURL: gw, Model: "m1", CustomModel: "c1", CustomModelName: "C", CustomModelDescription: "d"},
		{Name: "a", BaseURL: gw, APIKey: "sk-test-1", Auth: "bearer", SmallModel: "s1", Args: []string{"--x"}},
		{Name: "a", APIKey: "sk-test-1", Model: "m1"},
	} {
		want := legacyManagedEnv(l)
		if got := managedEnv(l); !slices.Equal(got, want) {
			t.Errorf("%+v:\n got  %q\n want %q", l, got, want)
		}
		wantMap := map[string]string{}
		for _, kv := range want {
			k, v, _ := strings.Cut(kv, "=")
			wantMap[k] = v
		}
		if got := managedSettingsEnv(l); !maps.Equal(got, wantMap) {
			t.Errorf("settings env %+v:\n got  %v\n want %v", l, got, wantMap)
		}
	}
}

// A provider stored with a trailing /v1 before the hub dropped it on saving (#4886) still launches against
// <base>/v1/messages, as its Test connection did; Codex and agy bases are not ANTHROPIC_BASE_URL and keep theirs.
func TestManagedEnvDropsATrailingV1FromMessagesBases(t *testing.T) {
	for _, c := range []struct {
		l         proto.ManagedLauncher
		key, want string
	}{
		{proto.ManagedLauncher{Name: "a", BaseURL: "https://gw.example.com/v1", APIKey: "sk-test-1"}, "ANTHROPIC_BASE_URL", "https://gw.example.com"},
		{proto.ManagedLauncher{Name: "a", BaseURL: "https://gw.example.com/team/v1", Model: "m1"}, "ANTHROPIC_BASE_URL", "https://gw.example.com/team"},
		{proto.ManagedLauncher{Name: "a", Kind: "api", API: "messages", BaseURL: "https://gw.example.com/v1"}, "ANTHROPIC_BASE_URL", "https://gw.example.com"},
		{proto.ManagedLauncher{Name: "a", Agent: "omp", BaseURL: "https://gw.example.com/v1"}, "ANTHROPIC_BASE_URL", "https://gw.example.com"},
		{proto.ManagedLauncher{Name: "a", Agent: "codex", API: "messages", BaseURL: "https://api.example.com/v1"}, "OPENAI_BASE_URL", "https://api.example.com/v1"},
		{proto.ManagedLauncher{Name: "a", Agent: "agy", BaseURL: "https://gemini.example.com/v1"}, "GOOGLE_GEMINI_BASE_URL", "https://gemini.example.com/v1"},
	} {
		if got := envValue(managedEnv(c.l), c.key); got != c.want {
			t.Errorf("%+v: %s=%q, want %q", c.l, c.key, got, c.want)
		}
		if c.key == "ANTHROPIC_BASE_URL" {
			if got := managedSettingsEnv(c.l)[c.key]; got != c.want {
				t.Errorf("settings env %+v: %s=%q, want %q", c.l, c.key, got, c.want)
			}
			if got := providerEnv(c.l, "", "", true)[c.key]; got != c.want {
				t.Errorf("main conversation %+v: %s=%q, want %q", c.l, c.key, got, c.want)
			}
		}
	}
}

func TestProviderEnv(t *testing.T) {
	const gw = "https://gw.example.com"
	models := []proto.ProviderModel{
		{ID: "big", ContextWindow: 1000000, DefaultEffort: "high"},
		{ID: "small", ContextWindow: 128000, DefaultEffort: "low"},
		{ID: "std", ContextWindow: 200000},
	}
	base := proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: gw, APIKey: "sk-test-k", Model: "big", Models: models}
	with := func(f func(*proto.ManagedLauncher)) proto.ManagedLauncher {
		l := base
		l.Models = slices.Clone(models)
		f(&l)
		return l
	}
	type want map[string]string // "-" is absent
	cases := []struct {
		name          string
		l             proto.ManagedLauncher
		model, effort string
		main          bool
		want          want
	}{
		{"1m added", base, "", "", false, want{"ANTHROPIC_MODEL": "big[1m]", "ANTHROPIC_DEFAULT_OPUS_MODEL": "big[1m]",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "big[1m]", "ANTHROPIC_DEFAULT_FABLE_MODEL": "big[1m]",
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "big[1m]", "ANTHROPIC_SMALL_FAST_MODEL": "big[1m]",
			"CLAUDE_CODE_SUBAGENT_MODEL": "big[1m]"}},
		{"1m not added twice", base, "big[1m]", "", false, want{"ANTHROPIC_MODEL": "big[1m]",
			"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1000000"}},
		{"unknown model as is", base, "other", "", false, want{"ANTHROPIC_MODEL": "other",
			"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "-", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "-", "CLAUDE_CODE_EFFORT_LEVEL": "-"}},
		{"200K is not 1m", base, "std", "", false, want{"ANTHROPIC_MODEL": "std", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "-",
			"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "200000"}},
		{"api key", base, "", "", false, want{"ANTHROPIC_API_KEY": "sk-test-k", "ANTHROPIC_AUTH_TOKEN": ""}},
		{"bearer", with(func(l *proto.ManagedLauncher) { l.Auth = "bearer" }), "", "", false,
			want{"ANTHROPIC_AUTH_TOKEN": "sk-test-k", "ANTHROPIC_API_KEY": ""}},
		{"bearer prefix dropped", with(func(l *proto.ManagedLauncher) { l.Auth = "bearer"; l.APIKey = "Bearer sk-test-k" }),
			"", "", false, want{"ANTHROPIC_AUTH_TOKEN": "sk-test-k"}},
		{"tiers fall back", with(func(l *proto.ManagedLauncher) { l.OpusModel = "o"; l.HaikuModel = "h"; l.SmallModel = "sf" }),
			"", "", false, want{"ANTHROPIC_DEFAULT_OPUS_MODEL": "o", "ANTHROPIC_DEFAULT_SONNET_MODEL": "big[1m]",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL": "h", "ANTHROPIC_SMALL_FAST_MODEL": "sf", "ANTHROPIC_DEFAULT_FABLE_MODEL": "big[1m]"}},
		{"tiers explicit", with(func(l *proto.ManagedLauncher) { l.SonnetModel = "s"; l.FableModel = "f" }), "", "", false,
			want{"ANTHROPIC_DEFAULT_SONNET_MODEL": "s", "ANTHROPIC_DEFAULT_FABLE_MODEL": "f"}},
		{"subagent force", with(func(l *proto.ManagedLauncher) { l.SubagentModel = "sub"; l.SubagentForce = true }), "", "", false,
			want{"CLAUDE_CODE_SUBAGENT_MODEL": "sub", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE": "1"}},
		{"no subagent force", base, "", "", false, want{"CLAUDE_CODE_SUBAGENT_MODEL_FORCE": "-"}},
		{"max context from window", base, "", "", false, want{"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1000000"}},
		{"max context set", with(func(l *proto.ManagedLauncher) { l.MaxContext = 300000 }), "small", "", false,
			want{"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "300000"}},
		{"max context not for small", base, "small", "", false, want{"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "-"}},
		{"compact from window", base, "small", "", false, want{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "128000"}},
		{"compact set", with(func(l *proto.ManagedLauncher) { l.CompactWindow = 150000 }), "", "", false,
			want{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "150000"}},
		{"compact main follows the launcher", with(func(l *proto.ManagedLauncher) { l.CompactWindow = 150000 }), "", "", true,
			want{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "150000"}},
		{"compact main from window", base, "small", "", true, want{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "128000"}},
		{"compact main: unknown model, none", base, "other", "", true,
			want{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "-", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "-"}},
		{"effort of provider not in env", with(func(l *proto.ManagedLauncher) { l.Effort = "medium" }), "", "", false,
			want{"CLAUDE_CODE_EFFORT_LEVEL": "-"}},
		{"effort of model not in env", base, "small", "", false, want{"CLAUDE_CODE_EFFORT_LEVEL": "-"}},
		{"effort pinned by env", with(func(l *proto.ManagedLauncher) { l.Env = "CLAUDE_CODE_EFFORT_LEVEL=high" }), "", "", false,
			want{"CLAUDE_CODE_EFFORT_LEVEL": "high"}},
		{"effort pinned by env ignored for main", with(func(l *proto.ManagedLauncher) { l.Env = "CLAUDE_CODE_EFFORT_LEVEL=high" }), "", "", true,
			want{"CLAUDE_CODE_EFFORT_LEVEL": "-"}},
		{"headers", with(func(l *proto.ManagedLauncher) { l.Headers = "X-Test: v1" }), "", "", false,
			want{"ANTHROPIC_CUSTOM_HEADERS": "X-Test: v1"}},
		{"no headers", base, "", "", false, want{"ANTHROPIC_CUSTOM_HEADERS": "-"}},
		{"proxy", with(func(l *proto.ManagedLauncher) { l.Proxy = "http://proxy.example.com:8080"; l.NoProxy = "localhost" }),
			"", "", false, want{"HTTP_PROXY": "http://proxy.example.com:8080", "HTTPS_PROXY": "http://proxy.example.com:8080",
				"NO_PROXY": "localhost"}},
		{"no proxy", base, "", "", false, want{"HTTP_PROXY": "-", "HTTPS_PROXY": "-", "NO_PROXY": "-"}},
		{"timeout default", base, "", "", false, want{"API_TIMEOUT_MS": "3000000",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "ANTHROPIC_BASE_URL": gw}},
		{"timeout set", with(func(l *proto.ManagedLauncher) { l.TimeoutMS = 60000 }), "", "", false,
			want{"API_TIMEOUT_MS": "60000"}},
		{"no gateway no timeout", with(func(l *proto.ManagedLauncher) { l.BaseURL = "" }), "", "", false,
			want{"API_TIMEOUT_MS": "-", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "-"}},
		{"artifacts off", base, "", "", false, want{"CLAUDE_CODE_DISABLE_ARTIFACT": "1"}},
	}
	for _, c := range cases {
		got := providerEnv(c.l, c.model, c.effort, c.main)
		for k, v := range c.want {
			gv, ok := got[k]
			if v == "-" {
				if ok {
					t.Errorf("%s: %s=%q, want unset", c.name, k, gv)
				}
				continue
			}
			if !ok || gv != v {
				t.Errorf("%s: %s=%q (set %v), want %q", c.name, k, gv, ok, v)
			}
		}
	}
}

// An explicit tier, small or subagent model with a window of 1M or more in the provider's models gets [1m]: Claude
// Code runs it with the 1M window only then, and drops the marker before calling the gateway (#5149). One between 200K
// and 1M stays unmarked: marked, Claude Code would count it as 1M, and nothing per model holds it to its size.
func TestProviderEnvExplicitModelsGetTheirOwnWindow(t *testing.T) {
	models := []proto.ProviderModel{
		{ID: "std", ContextWindow: 200000}, {ID: "small", ContextWindow: 128000}, {ID: "mid", ContextWindow: 400000},
		{ID: "wide", ContextWindow: 1000000}, {ID: "wider", ContextWindow: 2000000},
	}
	vars := []struct {
		key string
		set func(*proto.ManagedLauncher, string)
	}{
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", func(l *proto.ManagedLauncher, m string) { l.OpusModel = m }},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", func(l *proto.ManagedLauncher, m string) { l.SonnetModel = m }},
		{"ANTHROPIC_DEFAULT_FABLE_MODEL", func(l *proto.ManagedLauncher, m string) { l.FableModel = m }},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", func(l *proto.ManagedLauncher, m string) { l.HaikuModel = m }},
		{"ANTHROPIC_SMALL_FAST_MODEL", func(l *proto.ManagedLauncher, m string) { l.SmallModel = m }},
		{"CLAUDE_CODE_SUBAGENT_MODEL", func(l *proto.ManagedLauncher, m string) { l.SubagentModel = m }},
	}
	for _, c := range []struct{ name, model, want string }{
		{"1M window", "wide", "wide[1m]"},
		{"over 1M", "wider", "wider[1m]"},
		{"between 200K and 1M", "mid", "mid"},
		{"marked already", "wide[1m]", "wide[1m]"},
		{"marked, listed without a window", "other[1m]", "other[1m]"},
		{"200K", "std", "std"},
		{"under 200K", "small", "small"},
		{"no window known", "other", "other"},
	} {
		for _, v := range vars {
			for _, session := range []string{"std", "wide"} {
				// A launcher provider: an API one has no small or subagent model to save.
				l := proto.ManagedLauncher{Name: "p", BaseURL: "https://gw.example.com", APIKey: "sk-test-k",
					Model: session, Models: models}
				v.set(&l, c.model)
				env := providerEnv(l, "", "", false)
				if got := env[v.key]; got != c.want {
					t.Errorf("%s, session model %s: %s=%q, want %q", c.name, session, v.key, got, c.want)
				}
				if want := map[string]string{"std": "std", "wide": "wide[1m]"}[session]; env["ANTHROPIC_MODEL"] != want {
					t.Errorf("%s, %s set: ANTHROPIC_MODEL=%q, want %q", c.name, v.key, env["ANTHROPIC_MODEL"], want)
				}
			}
		}
	}
	// The Haiku tier follows an explicit small model by that model's window, and a tier left empty the session's model.
	l := proto.ManagedLauncher{Name: "p", BaseURL: "https://gw.example.com", APIKey: "sk-test-k", Model: "std",
		Models: models, SmallModel: "wide", SonnetModel: "small"}
	env := providerEnv(l, "", "", false)
	for k, want := range map[string]string{
		"ANTHROPIC_MODEL": "std", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "wide[1m]", "ANTHROPIC_SMALL_FAST_MODEL": "wide[1m]",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "small", "ANTHROPIC_DEFAULT_OPUS_MODEL": "std", "CLAUDE_CODE_SUBAGENT_MODEL": "std",
	} {
		if env[k] != want {
			t.Errorf("small model wide: %s=%q, want %q", k, env[k], want)
		}
	}
	// A session started on another model: the empty tiers follow it, the explicit ones keep their own. A session model
	// between 200K and 1M is still marked, its window held by the process's compact window and context limit.
	for _, c := range []struct{ session, model, sonnet string }{{"wider", "wider[1m]", "small"}, {"mid", "mid[1m]", "small"}} {
		env = providerEnv(l, c.session, "", false)
		for k, want := range map[string]string{
			"ANTHROPIC_MODEL": c.model, "ANTHROPIC_DEFAULT_OPUS_MODEL": c.model, "ANTHROPIC_DEFAULT_SONNET_MODEL": c.sonnet,
			"ANTHROPIC_DEFAULT_HAIKU_MODEL": "wide[1m]",
		} {
			if env[k] != want {
				t.Errorf("session on %s: %s=%q, want %q", c.session, k, env[k], want)
			}
		}
	}
}

func TestProviderEnvClear(t *testing.T) {
	in := []string{"ANTHROPIC_BASE_URL=x", "ANTHROPIC_CUSTOM_HEADERS=h", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1",
		"CLAUDE_CODE_EFFORT_LEVEL=max", "CLAUDE_CODE_MAX_CONTEXT_TOKENS=1", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=1",
		"API_TIMEOUT_MS=1", "HTTP_PROXY=p", "HTTPS_PROXY=p", "NO_PROXY=n", "PATH=/bin", "HOME=/h",
		"CLAUDE_CODE_DISABLE_ARTIFACT=1", "API_TIMEOUT_MS_EXTRA=1"}
	got := withoutEnv(in, providerEnvClear)
	if want := []string{"HTTP_PROXY=p", "HTTPS_PROXY=p", "NO_PROXY=n", "PATH=/bin", "HOME=/h", "CLAUDE_CODE_DISABLE_ARTIFACT=1",
		"API_TIMEOUT_MS_EXTRA=1"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if len(in) != 14 {
		t.Fatal("the input was changed")
	}
}

// `personad launch` drops from the shell what it always did for an old launcher (its proxy and timeout stay), and
// everything providerEnv may set for a provider.
func TestEnvClearFor(t *testing.T) {
	shell := []string{"ANTHROPIC_API_KEY=x", "CLAUDE_CODE_USE_BEDROCK=1", "CLAUDE_CODE_OAUTH_TOKEN=x", "HTTPS_PROXY=p",
		"API_TIMEOUT_MS=1", "PATH=/bin"}
	if got := withoutEnv(shell, envClearFor(proto.ManagedLauncher{Name: "old"})); !slices.Equal(got,
		[]string{"HTTPS_PROXY=p", "API_TIMEOUT_MS=1", "PATH=/bin"}) {
		t.Errorf("old launcher: %q", got)
	}
	// A provider keeps the shell's proxy (a gateway reachable only through it) unless it has its own.
	if got := withoutEnv(shell, envClearFor(proto.ManagedLauncher{Name: "p", Kind: "api"})); !slices.Equal(got, []string{"HTTPS_PROXY=p", "PATH=/bin"}) {
		t.Errorf("provider: %q", got)
	}
	if got := withoutEnv(append(shell, "https_proxy=q"), envClearFor(proto.ManagedLauncher{Name: "p", Kind: "api", Proxy: "http://127.0.0.1:1"})); !slices.Equal(got, []string{"PATH=/bin"}) {
		t.Errorf("provider with a proxy: %q", got)
	}
	n := &Node{}
	n.setManaged([]proto.ManagedLauncher{{Name: "p", Kind: "api"}})
	if c, _ := n.launcherEnv("p")["clear"].([]string); !slices.Equal(c, providerEnvClear) {
		t.Errorf("launcher.env clear: %q", c)
	}
}

func TestSetManagedKeepsMessagesProviders(t *testing.T) {
	n := &Node{}
	n.setManaged([]proto.ManagedLauncher{
		{Name: "launcher"},
		{Name: "api-default", Kind: "api"},
		{Name: "api-messages", Kind: "api", API: "messages"},
		{Name: "api-responses", Kind: "api", API: "responses"},
	})
	for name, want := range map[string]bool{"launcher": true, "api-default": true, "api-messages": true, "api-responses": false} {
		if _, ok := n.managedLauncher(name); ok != want {
			t.Errorf("%s managed: %v, want %v", name, ok, want)
		}
	}
}

// An assistant conversation keeps its own thinking level; its compact window is the launcher's, as an agent's (#3066).
func TestMainConversationKeepsItsOwnWindowAndLevel(t *testing.T) {
	l := proto.ManagedLauncher{Name: "p", Kind: "api", BaseURL: "https://gw.example.com", APIKey: "sk-test-k", Effort: "high",
		Models: []proto.ProviderModel{{ID: "big", ContextWindow: 1000000}}}
	main := providerEnv(l, "big", "max", true)
	if main["ANTHROPIC_MODEL"] != "big[1m]" || main["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "1000000" {
		t.Fatalf("main: %v", main)
	}
	// it switches its level while it runs (apply_flag_settings): an environment variable would win over that
	if _, ok := main["CLAUDE_CODE_EFFORT_LEVEL"]; ok {
		t.Fatalf("main conversation got a thinking level env: %v", main)
	}
	if agent := providerEnv(l, "big", "", false); agent["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "1000000" {
		t.Fatalf("agent: %v", agent)
	} else if _, ok := agent["CLAUDE_CODE_EFFORT_LEVEL"]; ok {
		t.Fatalf("agent got a thinking level env: %v", agent)
	}
	pinnedL := proto.ManagedLauncher{Name: "p", Kind: "api", Env: "CLAUDE_CODE_EFFORT_LEVEL=high"}
	if pinnedAgent := providerEnv(pinnedL, "", "", false); pinnedAgent["CLAUDE_CODE_EFFORT_LEVEL"] != "high" {
		t.Fatalf("pinned agent: %v", pinnedAgent)
	}
	// what is pinned over the user's settings leaves the per-start decisions to the start's environment
	pinned := pinnedEnv(providerEnv(l, "big", "", false))
	if _, ok := pinned["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; ok || pinned["ANTHROPIC_MODEL"] != "big[1m]" || pinned["ANTHROPIC_API_KEY"] != "sk-test-k" {
		t.Fatalf("pinned: %v", pinned)
	}
	if _, ok := pinned["CLAUDE_CODE_EFFORT_LEVEL"]; ok {
		t.Fatalf("pinned has the level: %v", pinned)
	}
}

func TestLaunchKeepsTheAssistantsOwnWindowAndLevel(t *testing.T) {
	clear := []string{"ANTHROPIC_*", "CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CONFIG_DIR"}
	env := []string{"ANTHROPIC_MODEL=m", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=1000000", "CLAUDE_CODE_EFFORT_LEVEL=high", "CLAUDE_CONFIG_DIR=/tmp/provider-profile"}
	c, e := launchEnvFor(clear, env, true)
	if !slices.Equal(c, []string{"ANTHROPIC_*"}) || !slices.Equal(e, []string{"ANTHROPIC_MODEL=m"}) {
		t.Fatalf("assistant: %v %v", c, e)
	}
	if c, e := launchEnvFor(clear, env, false); !slices.Equal(c, clear) || !slices.Equal(e, env) {
		t.Fatalf("agent: %v %v", c, e)
	}
}

// A profile must change actual launcher defaults, not just purpose overrides: health probes start the managed
// launcher with no model override, while small and subagent calls use these aliases.
func TestEconomyLauncherDefaultsReachHealthAndSubagents(t *testing.T) {
	l := proto.ManagedLauncher{Name: "profile-launcher", Model: "available-supervisor", SmallModel: "long-context", SubagentModel: "mechanical-model", OpusModel: "available-supervisor", SonnetModel: "available-supervisor", FableModel: "available-supervisor", HaikuModel: "long-context"}
	env := providerEnv(l, "", "", false)
	for key, want := range map[string]string{
		"ANTHROPIC_MODEL": "available-supervisor", "ANTHROPIC_DEFAULT_OPUS_MODEL": "available-supervisor", "ANTHROPIC_DEFAULT_SONNET_MODEL": "available-supervisor", "ANTHROPIC_DEFAULT_FABLE_MODEL": "available-supervisor", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "long-context", "ANTHROPIC_SMALL_FAST_MODEL": "long-context", "CLAUDE_CODE_SUBAGENT_MODEL": "mechanical-model",
	} {
		if got := env[key]; got != want {
			t.Fatalf("%s=%q want %q", key, got, want)
		}
	}
	n := &Node{bin: "/test/personad"}
	n.setManaged([]proto.ManagedLauncher{l})
	if cmd := n.launcherCommand(l.Name); !strings.Contains(cmd, "launch "+l.Name) {
		t.Fatalf("health would bypass managed configuration: %s", cmd)
	}
	got, ok := n.managedLauncher(l.Name)
	if !ok || got.Model != l.Model || got.SubagentModel != l.SubagentModel {
		t.Fatal("node did not receive profile defaults")
	}
}
