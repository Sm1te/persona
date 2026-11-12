package node

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
)

// withClaudeVersion makes the claude version the tests' nodes ask for v until the test ends.
func withClaudeVersion(t *testing.T, v string) {
	t.Helper()
	claudeVersionOf = func(context.Context) string { return v }
	t.Cleanup(func() { claudeVersionOf = func(context.Context) string { return "" } })
}

// The node writes each set of hooks twice: in its settings file and as a plugin, the same hooks in both. The Agents'
// MessageDisplay is a plugin of its own (TestLiveRepliesPlugin).
func TestHooksPluginsCarryTheSettingsHooks(t *testing.T) {
	home := t.TempDir()
	n := &Node{home: home, dir: mkdir(t, filepath.Join(home, ".persona")), bin: "/x/personad"}
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	versions := map[string]bool{}
	for _, kind := range []string{"claude", "assistant"} {
		plugin := filepath.Join(n.dir, kind+"-hooks")
		var manifest map[string]any
		b, err := os.ReadFile(filepath.Join(plugin, ".claude-plugin", "plugin.json"))
		if err != nil || json.Unmarshal(b, &manifest) != nil || manifest["name"] != hooksPluginName {
			t.Fatalf("%s: manifest %s (%v)", kind, b, err)
		}
		if v, _ := manifest["version"].(string); !strings.HasPrefix(v, "1.0.0-") || versions[v] {
			t.Errorf("%s: version %q: the hooks' hash, not another set's", kind, v)
		} else {
			versions[v] = true
		}
		inPlugin, _ := readSettings(t, filepath.Join(plugin, "hooks", "hooks.json"))["hooks"].(map[string]any)
		inSettings := readSettings(t, filepath.Join(n.dir, kind+"-settings.json"))["hooks"]
		if inPlugin == nil || !reflect.DeepEqual(inPlugin, inSettings) {
			t.Errorf("%s: the plugin's hooks %v, the settings' %v", kind, inPlugin, inSettings)
		}
	}
	// The assistant's have the Feishu gate on Bash, every command. The agents' have a Bash hook only on a Mac, and only
	// for commands running cua-driver (cua_gate.go, #4349).
	bash := func(kind string) (all, cuaOnly bool) {
		pre := readSettings(t, filepath.Join(n.dir, kind+"-hooks", "hooks", "hooks.json"))["hooks"].(map[string]any)["PreToolUse"].([]any)
		for _, m := range pre {
			if m.(map[string]any)["matcher"] != "Bash" {
				continue
			}
			cmd, _ := m.(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
			if strings.HasSuffix(cmd, " "+nodeclient.CuaOnlyFlag) {
				cuaOnly = true
			} else {
				all = true
			}
		}
		return all, cuaOnly
	}
	agentsAll, agentsCua := bash("claude")
	assistantAll, assistantCua := bash("assistant")
	if agentsAll || agentsCua != (runtime.GOOS == "darwin") || !assistantAll || assistantCua {
		t.Errorf("the Bash gates: agents every command %v / cua-driver only %v, assistant %v / %v", agentsAll, agentsCua, assistantAll, assistantCua)
	}
}

// With a claude that loads plugins, a start passes no --settings of Persona's but a managed launcher's env, so a
// launcher's own (an alias's, a function's) is the one claude keeps; the hooks are the plugin. With an older claude,
// one whose version is not known, or the plugin not written, as before: the hooks in --settings, merged with an
// alias's own or a managed launcher's env.
func TestClaudeStartSettings(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	n := &Node{home: home, dir: mkdir(t, filepath.Join(home, ".persona")), bin: "/x/personad"}
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".claude/settings.work.json"), `{"env":{"ANTHROPIC_AUTH_TOKEN":"tok","ANTHROPIC_BASE_URL":"https://gw.example.com"}}`)
	withLauncherSettings(t, map[string]string{"work": "~/.claude/settings.work.json"})
	n.setManaged([]proto.ManagedLauncher{{Name: "m", BaseURL: "https://managed.example.com", Model: "m1", APIKey: "k"}})
	ctx := context.Background()
	merged := func(name string) string { return filepath.Join(n.dir, "launcher-settings", name) }

	for _, kind := range []string{"claude", "assistant"} {
		ours, plugin := filepath.Join(n.dir, kind+"-settings.json"), filepath.Join(n.dir, kind+"-hooks")

		n.claudeV.set("2.1.318")
		for _, launcher := range []string{"claude", "work", "a_function"} {
			if s, h := n.claudeStartSettings(ctx, launcher, kind, cwd); s != "" || h != plugin {
				t.Errorf("%s %s: --settings %q --plugin-dir %q, want none and %s", kind, launcher, s, h, plugin)
			}
		}
		s, h := n.claudeStartSettings(ctx, "m", kind, cwd)
		if h != plugin || s != merged("m-env-settings.json") {
			t.Fatalf("%s managed: --settings %q --plugin-dir %q", kind, s, h)
		}
		got := readSettings(t, s)
		if env, _ := got["env"].(map[string]any); env["ANTHROPIC_BASE_URL"] != "https://managed.example.com" || env["ANTHROPIC_API_KEY"] != "k" {
			t.Errorf("%s managed: the gateway is missing: %v", kind, got)
		}
		if _, ok := got["hooks"]; ok {
			t.Errorf("%s managed: hooks in --settings beside the plugin would run twice: %v", kind, got)
		}

		for _, v := range []string{"2.0.9", ""} {
			n.claudeV.set(v)
			if s, h := n.claudeStartSettings(ctx, "claude", kind, cwd); s != ours || h != "" {
				t.Errorf("%s claude %q: --settings %q --plugin-dir %q", kind, v, s, h)
			}
			if s, h := n.claudeStartSettings(ctx, "work", kind, cwd); s != merged("work-"+kind+"-settings.json") || h != "" {
				t.Errorf("%s alias %q: --settings %q --plugin-dir %q", kind, v, s, h)
			}
			s, h := n.claudeStartSettings(ctx, "m", kind, cwd)
			if s != merged("m-"+kind+"-settings.json") || h != "" || len(hookCommands(t, readSettings(t, s), "Stop")) != 1 {
				t.Errorf("%s managed %q: --settings %q --plugin-dir %q", kind, v, s, h)
			}
		}
	}

	if err := os.RemoveAll(filepath.Join(n.dir, "claude-hooks")); err != nil {
		t.Fatal(err)
	}
	n.claudeV.set("2.1.318")
	if s, h := n.claudeStartSettings(ctx, "claude", "claude", cwd); s != filepath.Join(n.dir, "claude-settings.json") || h != "" {
		t.Errorf("no plugin written: --settings %q --plugin-dir %q", s, h)
	}
}

// A herdr start with a claude that loads plugins: the managed launcher's env is its only --settings, then Persona's
// skills and hooks plugins, and the live replies plugin (on unless the machine's setting is off).
func TestHerdrStartManagedWithHooksPlugin(t *testing.T) {
	withClaudeVersion(t, "2.1.318")
	n, f, home := herdrNode(t)
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := request(t, n, "launchers.managed", map[string]any{"launchers": []any{
		map[string]any{"name": "work_gw", "base_url": "https://gateway.example.com", "model": "m"}}}); err != nil {
		t.Fatal(err)
	}
	cwd := mkdir(t, filepath.Join(home, "proj"))
	f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
	startHerdr(f, "w1:p2", proto.StatusIdle)
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.screenSteps("w1:p2", isKeys(), screenFixture(t, "claude-2.1.312-empty-162.txt"))
	if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "cwd": cwd, "launcher": "work_gw", "async": true}); err != nil {
		t.Fatal(err)
	}
	waitCalls(t, f, "pane.send_input", 1)
	dir := personaDir(home)
	want := inAgentEnv(dir, "HERDR_AGENT=claude /tmp/persona-test/personad launch work_gw --session-id "+startID+
		" --settings "+shellQuote(filepath.Join(dir, "launcher-settings", "work_gw-env-settings.json"))+
		" --mcp-config "+shellQuote(filepath.Join(dir, "mcp.json"))+
		" --allowedTools "+shellQuote(pluginToolsRule(dir))+
		" --plugin-dir "+shellQuote(filepath.Join(dir, "agent-plugin"))+
		" --plugin-dir "+shellQuote(filepath.Join(dir, "claude-hooks"))+
		" --plugin-dir "+shellQuote(filepath.Join(dir, "claude-live-replies")), shellQuote)
	if typed := f.trace("pane.send_input"); !slices.Contains(typed, call("pane.send_input", map[string]any{"pane_id": "w1:p2", "keys": []string{"Enter"}, "text": want})) {
		t.Errorf("typed %v, want %s", typed, want)
	}
}
