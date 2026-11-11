package node

import (
	"context"
	"encoding/json"
	"github.com/nwverse/persona/server/internal/compat"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/proto"
)

// Tests start claude without the plugin unless one says which claude it is: asking a real one would make what they
// expect depend on the machine.
func init() { claudeVersionOf = func(context.Context) string { return "" } }

func TestVersionAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"2.1.0": true, "2.1.318": true, "2.2.0-beta": true, "3.0.0": true, "2.0.99": false, "1.9.9": false,
		"": false, "2.1": false, "x.1.0": false} {
		if got := versionAtLeast(v, pluginMinVersion); got != want {
			t.Errorf("%q: %v", v, got)
		}
	}
}

// The agents Persona starts load its plugin for their session (--plugin-dir), with a claude that takes it; with an
// older one, or one whose version is not known, they start as before. The plugin is written when the node starts.
func TestAgentPlugin(t *testing.T) {
	for _, tc := range []struct {
		version string
		flag    bool
	}{{"2.1.318", true}, {"2.0.9", false}, {"", false}} {
		claudeVersionOf = func(context.Context) string { return tc.version }
		n, f, home := herdrNode(t)
		if err := n.writeClaudeConfig(); err != nil {
			t.Fatal(err)
		}
		plugin := filepath.Join(personaDir(home), "agent-plugin")
		if st, err := os.Stat(filepath.Join(plugin, "skills", "persona-office", "scripts", "office.sh")); err != nil || st.Mode()&0o111 == 0 {
			t.Fatalf("the plugin's script: %v", err)
		}
		if _, err := os.Stat(filepath.Join(plugin, ".claude-plugin", "plugin.json")); err != nil {
			t.Fatal(err)
		}
		cwd := mkdir(t, filepath.Join(home, "proj"))
		f.reply("workspace.list", map[string]any{"workspaces": []any{map[string]any{"workspace_id": "w1", "label": "proj"}}})
		startHerdr(f, "w1:p2", proto.StatusIdle)
		f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
		f.screenSteps("w1:p2", isKeys(), screenFixture(t, "claude-2.1.312-empty-162.txt"))
		if _, err := request(t, n, "session.start", map[string]any{"session_id": startID, "cwd": cwd, "async": true}); err != nil {
			t.Fatal(err)
		}
		waitCalls(t, f, "pane.send_input", 1)
		want := startCmd("claude", "--session-id "+startID, personaDir(home))
		if tc.flag { // the hooks go in a plugin too, and Persona passes no --settings (hooksplugin.go)
			want = inAgentEnv(personaDir(home), "HERDR_AGENT=claude claude --session-id "+startID+" --mcp-config "+shellQuote(filepath.Join(personaDir(home), "mcp.json"))+
				" --allowedTools "+shellQuote(pluginToolsRule(personaDir(home)))+
				" --plugin-dir "+shellQuote(plugin)+
				" --plugin-dir "+shellQuote(filepath.Join(personaDir(home), "claude-hooks"))+
				" --plugin-dir "+shellQuote(filepath.Join(personaDir(home), liveRepliesDir)), shellQuote) // live replies: on by default
		}
		if typed := f.trace("pane.send_input"); !slices.Contains(typed, call("pane.send_input", map[string]any{"pane_id": "w1:p2", "keys": []string{"Enter"}, "text": want})) {
			t.Errorf("claude %q: typed %v, want %s", tc.version, typed, want)
		}
	}
	claudeVersionOf = func(context.Context) string { return "" }
}

// The assistant is allowed Persona's tools and its own copy of the office skill's script, at its exact path.
func TestAssistantAllowsTheOfficeScript(t *testing.T) {
	n, home := testNode(t)
	want := assistantAllowedTools + ",mcp__" + compat.MCPServer + ",Bash(sh " + filepath.Join(identity.AssistantDir(home), ".claude", "skills", "persona-office", "scripts", "office.sh") + " *)"
	if got := n.assistantTools(true); got != want {
		t.Fatalf("%s\nwant %s", got, want)
	}
}

// An Agent Persona starts is allowed exactly its own Persona tools (assistantwire.AgentOwnTools, #4966), under both
// names the node's mcp.json registers, and nothing else of Persona's: not its whole server, the phone's tools, a
// connector's, dispatch or the assistant's own, which keep their card. Each is a tool the node names the calling
// Agent's session for (callerTools). Nothing of it goes into the files every session shares or the machine's Claude
// settings: it is the start's own flag, so a session the user starts themselves is asked as before.
func TestAgentAllowsOnlyItsOwnPersonaTools(t *testing.T) {
	n, home := testNode(t)
	n.dir, n.bin = personaDir(home), "/usr/local/bin/personad"
	var want []string
	for _, tool := range []string{"report_to_supervisor", "todo_decisions", "todo_decision_add", "todo_bind_thread", "task_plan", "task_review"} {
		want = append(want, "mcp__persona__"+tool, "mcp__"+compat.MCPServer+"__"+tool)
	}
	got := strings.Split(n.agentAllowedTools(false), ",")
	if !slices.Equal(got, want) {
		t.Fatalf("allowed %q\nwant %q", got, want)
	}
	for _, name := range got {
		if base := name[strings.LastIndex(name, "__")+2:]; !slices.Contains(callerTools, base) {
			t.Errorf("%s: not one of the tools that act for the Agent calling them (callerTools)", name)
		}
	}
	for _, asked := range []string{"dispatch_start", "dispatch_read", "phone_notify", "phone_messages_compose", "phone_calendar_create_event",
		"mail_send", "mail_read", "iphone_do", "agent_start", "agent_send", "notify_user", "persona_settings"} {
		for _, name := range compat.MCPToolNames([]string{asked}) {
			if slices.Contains(got, name) {
				t.Errorf("%s is allowed: it must keep its card", name)
			}
		}
	}
	for _, server := range []string{"mcp__persona", "mcp__" + compat.MCPServer} {
		if slices.Contains(got, server) {
			t.Errorf("Persona's whole server %s is allowed", server)
		}
	}
	if with := n.agentAllowedTools(true); with != strings.Join(want, ",")+","+officeRule(n.agentOfficeScript()) {
		t.Errorf("with the plugin: %s", with)
	}

	if err := writeGeneratedConfig(n.dir, n.bin); err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if b, err := os.ReadFile(filepath.Join(n.dir, "mcp.json")); err != nil || json.Unmarshal(b, &mcp) != nil {
		t.Fatalf("mcp.json: %v", err)
	}
	if mcp.Servers["persona"] == nil || mcp.Servers[compat.MCPServer] == nil {
		t.Fatalf("mcp.json registers %v: the allowed names must be the ones it registers", slices.Collect(maps.Keys(mcp.Servers)))
	}
	settings, err := os.ReadFile(filepath.Join(n.dir, "claude-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(settings), `"permissions"`) || strings.Contains(string(settings), "report_to_supervisor") {
		t.Errorf("the shared settings allow tools: %s", settings)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("the machine's Claude settings were written: %v", err)
	}
}
