package shellcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClaudeAliases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "aliases.zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	got := ClaudeAliases(string(b))
	want := []Alias{
		{Name: "gcc1", Args: []string{"--dangerously-skip-permissions"}, Env: map[string]string{
			"ANTHROPIC_BASE_URL": "https://gw-one.example.com/api/anthropic", "ANTHROPIC_AUTH_TOKEN": "sk-test-one",
			"ANTHROPIC_MODEL": "glm-test-5", "ANTHROPIC_SMALL_FAST_MODEL": "glm-test-5-air"}},
		{Name: "dcc2", Args: []string{}, Env: map[string]string{
			"CLAUDE_CODE_SUBAGENT_MODEL": "deepseek-test-flash"}},
		{Name: "mcc", Args: []string{"--model", "minimax-test-m2", "--settings", "~/.claude/settings.mcc.json"},
			Env: map[string]string{"HTTPS_PROXY": "http://proxy.example.com:7890", "ANTHROPIC_BASE_URL": "https://gw-three.example.com",
				"ANTHROPIC_AUTH_TOKEN": "sk-test-three", "ANTHROPIC_CUSTOM_HEADERS": "X-Test-Team: alpha"}},
		{Name: "ncc", Args: []string{}, Env: map[string]string{}},
		// Defined twice: the last one, where it is.
		{Name: "kcc", Args: []string{}, Env: map[string]string{
			"ANTHROPIC_BASE_URL": "https://gw-two.example.com/anthropic", "ANTHROPIC_API_KEY": "sk-test-two-new",
			"ANTHROPIC_MODEL": "kimi-test-k2[1m]"}},
	}
	if !reflect.DeepEqual(got, want) {
		gj, _ := json.MarshalIndent(got, "", " ")
		t.Fatalf("got %s", gj)
	}
}

func TestAliasDefs(t *testing.T) {
	for _, c := range []struct {
		words []string
		want  []Def
	}{
		{[]string{"alias", "a=b c", "-g", "x", "1bad=y", "ok-name=z"}, []Def{{"a", "b c"}, {"ok-name", "z"}}},
		{[]string{"alias", "-g", "G=| grep"}, []Def{{"G", "| grep"}}},
		{[]string{"alias", "x"}, nil},
		{[]string{"export", "a=b"}, nil},
		{nil, nil},
	} {
		if got := AliasDefs(c.words); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, want %+v", c.words, got, c.want)
		}
	}
}

func TestClaudeCommand(t *testing.T) {
	for _, c := range []struct {
		value string
		want  Alias
		ok    bool
	}{
		{"ANTHROPIC_MODEL=m claude -c", Alias{Env: map[string]string{"ANTHROPIC_MODEL": "m"}, Args: []string{"-c"}}, true},
		{"export A=1; B=2 env -u A /usr/local/bin/claude", Alias{Env: map[string]string{"B": "2"}, Args: []string{}}, true},
		{"cd /tmp && A=1 claude", Alias{Env: map[string]string{"A": "1"}, Args: []string{}}, true},
		{"A=1 codex", Alias{}, false},
		{"echo 'open", Alias{}, false},
	} {
		got, ok := ClaudeCommand(c.value)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v %v, want %+v %v", c.value, got, ok, c.want, c.ok)
		}
	}
}
