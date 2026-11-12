package assistant

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Every variable of an alias either fills a field, goes into the provider's env, or is named as left out with why.
func TestImportEnvRules(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		lines   string
		names   []string
		skipped []skippedEnv
	}{
		{
			name: "kept, sorted, quoted where needed",
			env: map[string]string{"DISABLE_TELEMETRY": "1", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "64000",
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "TEAM": "a b", "MONKEY": "x", "KEYBOARD_LAYOUT": "us", "TOKENIZER": "fast"},
			lines: "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1\nCLAUDE_CODE_MAX_OUTPUT_TOKENS=64000\nDISABLE_TELEMETRY=1\n" +
				"KEYBOARD_LAYOUT=us\nMONKEY=x\nTEAM=\"a b\"\nTOKENIZER=fast",
			names: []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_MAX_OUTPUT_TOKENS", "DISABLE_TELEMETRY",
				"KEYBOARD_LAYOUT", "MONKEY", "TEAM", "TOKENIZER"},
		},
		{
			name: "the variables with a field never go into env",
			env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com", "ANTHROPIC_API_KEY": "sk-test", "ANTHROPIC_AUTH_TOKEN": "sk-test",
				"ANTHROPIC_CUSTOM_HEADERS": "X-A: b", "ANTHROPIC_MODEL": "m", "ANTHROPIC_DEFAULT_OPUS_MODEL": "m",
				"ANTHROPIC_DEFAULT_SONNET_MODEL": "m", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "m", "ANTHROPIC_DEFAULT_FABLE_MODEL": "m",
				"ANTHROPIC_SMALL_FAST_MODEL": "m", "CLAUDE_CODE_SUBAGENT_MODEL": "m", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE": "1",
				"CLAUDE_CODE_EFFORT_LEVEL": "high", "CLAUDE_CODE_AUTO_COMPACT_WINDOW": "1", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "1",
				"API_TIMEOUT_MS": "1", "ANTHROPIC_CUSTOM_MODEL_OPTION": "m", "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": "M",
				"ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION": "d", "HTTP_PROXY": "http://p.example.com:1", "https_proxy": "http://p.example.com:1",
				"NO_PROXY": "localhost", "no_proxy": "localhost"},
		},
		{
			name: "credentials by name, any case, whole words only",
			env: map[string]string{"AWS_SECRET_ACCESS_KEY": "x", "CLAUDE_CODE_OAUTH_TOKEN": "x", "OPENAI_API_KEY": "x", "MY_APIKEY": "x",
				"SESSION_COOKIES": "x", "DB_PASSWORD": "x", "X_PASSWD": "x", "GOOGLE_APPLICATION_CREDENTIALS": "x", "gh_token": "x",
				"VAULT_SECRETS": "x", "SSH_KEYS": "x", "A_CREDENTIAL": "x", "COOKIE": "x"},
			skipped: []skippedEnv{{"AWS_SECRET_ACCESS_KEY", "credential"}, {"A_CREDENTIAL", "credential"}, {"CLAUDE_CODE_OAUTH_TOKEN", "credential"},
				{"COOKIE", "credential"}, {"DB_PASSWORD", "credential"}, {"GOOGLE_APPLICATION_CREDENTIALS", "credential"},
				{"MY_APIKEY", "credential"}, {"OPENAI_API_KEY", "credential"}, {"SESSION_COOKIES", "credential"}, {"SSH_KEYS", "credential"},
				{"VAULT_SECRETS", "credential"}, {"X_PASSWD", "credential"}, {"gh_token", "credential"}},
		},
		{
			name: "a value with a user is a credential, a proxy one too, whether Go parses it or not",
			env: map[string]string{"MIRROR": "https://me:pw@mirror.example.com", "http_proxy": "http://me:pw@proxy.example.com:1",
				"ALL_PROXY": "socks5://me@proxy.example.com:1", "PLAIN": "https://mirror.example.com", "MAIL": "me@example.com",
				"ODD": "https://me:p^w{x@mirror.example.com", "BARE": "me:pw@mirror.example.com:8080", "RELATIVE": "//me:pw@mirror.example.com",
				"AT_IN_PATH": "https://mirror.example.com/@team?x=a@b"},
			lines: "AT_IN_PATH=https://mirror.example.com/@team?x=a@b\nPLAIN=https://mirror.example.com",
			names: []string{"AT_IN_PATH", "PLAIN"},
			skipped: []skippedEnv{{"ALL_PROXY", "credential"}, {"BARE", "credential"}, {"MAIL", "credential"}, {"MIRROR", "credential"},
				{"ODD", "credential"}, {"RELATIVE", "credential"}, {"http_proxy", "credential"}},
		},
		{
			name: "what the shell would expand, or no line holds",
			env: map[string]string{"FROM_HOME": "$HOME/x", "TILDE": "~/x", "CMD": "`date`", "BRACED": "${X}", "LINES": "a\nb",
				"NUL": "a\x00b", "BOTH": `say "it's"`, "CR": "a\rb", "MIDDLE_TILDE": "a~b"},
			lines: "MIDDLE_TILDE=a~b",
			names: []string{"MIDDLE_TILDE"},
			skipped: []skippedEnv{{"BOTH", "shell"}, {"BRACED", "shell"}, {"CMD", "shell"}, {"CR", "shell"}, {"FROM_HOME", "shell"},
				{"LINES", "shell"}, {"NUL", "shell"}, {"TILDE", "shell"}},
		},
		{
			name: "names of one computer",
			env: map[string]string{"PATH": "/usr/bin", "HOME": "/home/me", "PWD": "/", "OLDPWD": "/", "SHELL": "/bin/zsh", "USER": "me",
				"LOGNAME": "me", "TMPDIR": "/tmp", "CLAUDE_CONFIG_DIR": "/home/me/.claude-work"},
			skipped: []skippedEnv{{"CLAUDE_CONFIG_DIR", "computer"}, {"HOME", "computer"}, {"LOGNAME", "computer"}, {"OLDPWD", "computer"},
				{"PATH", "computer"}, {"PWD", "computer"}, {"SHELL", "computer"}, {"TMPDIR", "computer"}, {"USER", "computer"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, names, skipped := importEnv(c.env)
			if lines != c.lines || !slices.Equal(names, c.names) || !reflect.DeepEqual(skipped, c.skipped) {
				t.Fatalf("got %q %v %v\nwant %q %v %v", lines, names, skipped, c.lines, c.names, c.skipped)
			}
			if err := checkEnv(lines); err != nil {
				t.Fatalf("checkEnv: %v", err)
			}
		})
	}
}

// Beyond 50 kept lines (checkEnv's limit) the rest, by name, are left out as limit.
func TestImportEnvLimit(t *testing.T) {
	env := map[string]string{}
	for i := range 55 {
		env[fmt.Sprintf("VAR_%02d", i)] = "1"
	}
	lines, names, skipped := importEnv(env)
	if len(names) != 50 || names[49] != "VAR_49" || len(skipped) != 5 || skipped[0] != (skippedEnv{"VAR_50", "limit"}) ||
		skipped[4] != (skippedEnv{"VAR_54", "limit"}) {
		t.Fatalf("%v %v", names, skipped)
	}
	if err := checkEnv(lines); err != nil {
		t.Fatal(err)
	}
}

// The lines the node reads back as the value (node TestImportedEnvLinesReadBack holds the same expectations: the
// packages differ), and the values no line holds.
func TestImportEnvLine(t *testing.T) {
	for _, c := range []struct{ name, value, line string }{
		{"DISABLE_TELEMETRY", "1", "DISABLE_TELEMETRY=1"},
		{"EMPTY", "", "EMPTY="},
		{"SPACED", "a b", `SPACED="a b"`},
		{"EDGES", " padded ", `EDGES=" padded "`},
		{"HASH", "a#b", `HASH="a#b"`},
		{"AFTER", "x #y", `AFTER="x #y"`},
		{"SINGLE", "it's", `SINGLE="it's"`},
		{"DOUBLE", `say "hi"`, `DOUBLE='say "hi"'`},
		{"TABBED", "a\tb", "TABBED=\"a\tb\""},
		{"BACKSLASH", `C:\x\y`, `BACKSLASH=C:\x\y`},
		{"EQUALS", "a=b", "EQUALS=a=b"},
		{"URL", "https://example.com/x?y=1", "URL=https://example.com/x?y=1"},
	} {
		line, ok := importEnvLine(c.name, c.value)
		if !ok || line != c.line {
			t.Errorf("%s=%q: %q %v, want %q", c.name, c.value, line, ok, c.line)
		}
		if err := checkEnv(line); err != nil {
			t.Errorf("%s: checkEnv: %v", line, err)
		}
	}
	for _, c := range [][2]string{{"LINES", "a\nb"}, {"RETURN", "a\rb"}, {"NUL", "a\x00b"}, {"BOTH", `"it's"`}, {"BAD-NAME", "x"}, {"1X", "x"}} {
		if line, ok := importEnvLine(c[0], c[1]); ok {
			t.Errorf("%s=%q written as %q", c[0], c[1], line)
		}
	}
}

// A proxy whose user and password Go's URL parser refuses (Node takes them), one without a scheme and one starting
// with // still carry a password: in every spelling the proxy is left empty and the variable named as a credential,
// and no part of the password reaches the preview. A plain proxy is imported.
func TestImportProxyCredentialsGoCannotParse(t *testing.T) {
	for i, c := range []struct{ value, password string }{
		{"http://user:Ab^9{x@proxy.example:8080", "Ab^9{x"},
		{"user:Sch3m3less-pw@proxy.example:8080", "Sch3m3less-pw"},
		{"//user:Rel4tive-pw@proxy.example:8080", "Rel4tive-pw"},
	} {
		spelling := importProxyEnv[i%len(importProxyEnv)]
		alias := zshrcAlias{Alias: "pcc", Command: "claude", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://gw.example.com",
			"ANTHROPIC_API_KEY": importKeyA, "ANTHROPIC_MODEL": "m", spelling: c.value}}
		g := newImportGroup("m1", alias, "https://gw.example.com")
		if g.Proxy != "" || g.env != "" || !reflect.DeepEqual(g.SkippedEnv, []skippedEnv{{spelling, "credential"}}) {
			t.Fatalf("%s=%s: proxy %q env %q skipped %+v", spelling, c.value, g.Proxy, g.env, g.SkippedEnv)
		}
		_, mux := importTestHub(t, []zshrcAlias{alias}, nil)
		r := importRequest(mux, "GET", "")
		if r.Code != 200 || strings.Contains(r.Body.String(), c.password) || !strings.Contains(r.Body.String(), `"name":"`+spelling+`","reason":"credential"`) {
			t.Fatalf("%s: %d %s", c.value, r.Code, r.Body.String())
		}
	}
	plain := zshrcAlias{Alias: "pcc", Command: "claude", Env: map[string]string{"HTTPS_PROXY": "http://proxy.example:8080"}}
	if g := newImportGroup("m1", plain, "https://gw.example.com"); g.Proxy != "http://proxy.example:8080" || g.SkippedEnv != nil {
		t.Fatalf("plain proxy: %q %+v", g.Proxy, g.SkippedEnv)
	}
}
