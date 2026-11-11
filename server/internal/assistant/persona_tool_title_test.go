package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// A card for one of Persona's own MCP tools is titled with its name for people, in the user's language, whichever
// namespace the agent called it by (#4966): never the raw mcp__persona__… name. Other tools get none: the apps name
// those themselves.
func TestPersonaToolTitle(t *testing.T) {
	legacy := "mcp__" + compat.MCPServer + "__"
	for _, tc := range []struct {
		lang  i18n.Lang
		name  string
		input string
		want  string
	}{
		{i18n.ZhHans, "mcp__persona__report_to_supervisor", `{"kind":"done","text":"x"}`, "向督办汇报 · 完成"},
		{i18n.En, "mcp__persona__report_to_supervisor", `{"kind":"done","text":"x"}`, "Report to supervisor · Done"},
		{i18n.Ja, legacy + "report_to_supervisor", `{"kind":"blocked"}`, "監督役への報告 · 行き詰まり"},
		{i18n.En, "mcp__persona__report_to_supervisor", `{"kind":"question","urgent":false}`, "Report to supervisor · Question"},
		{i18n.En, "mcp__persona__report_to_supervisor", `{"kind":"progress","step":2}`, "Report to supervisor · Progress"},
		// A kind it does not know, or none: the tool alone.
		{i18n.En, "mcp__persona__report_to_supervisor", `{"kind":"family_phone"}`, "Report to supervisor"},
		{i18n.En, "mcp__persona__report_to_supervisor", `{"kind":"done.x"}`, "Report to supervisor"},
		{i18n.En, "mcp__persona__report_to_supervisor", `not json`, "Report to supervisor"},
		{i18n.ZhHans, "mcp__persona__todo_decision_add", `{}`, "记下一个决定"},
		{i18n.En, "mcp__plugin_persona__todo_decisions", `{}`, "The task's decisions"},
		{i18n.ZhHans, "mcp__persona__dispatch_start", `{}`, "交给另一台电脑"},
		{i18n.En, "mcp__persona__agent_start", `{}`, "New agent"},
		// The phone's and mail tools as the chat's rows name them; any other of Persona's by its words.
		{i18n.En, "mcp__persona__phone_contacts_search", `{}`, "iPhone · contacts search"},
		{i18n.ZhHans, "mcp__persona__mail_send", `{}`, "邮件 · send"},
		{i18n.En, "mcp__persona__agent_read", `{}`, "Persona · agent read"},
		// Not one of Persona's tools.
		{i18n.En, "Bash", `{}`, ""},
		{i18n.En, "mcp__github__create_issue", `{}`, ""},
		{i18n.En, "mcp__persona", `{}`, ""},
		{i18n.En, "mcp__persona__", `{}`, ""},
		{i18n.En, "report_to_supervisor", `{"kind":"done"}`, ""},
	} {
		if got := personaToolTitle(tc.lang, tc.name, json.RawMessage(tc.input)); got != tc.want {
			t.Errorf("%s %s %s: %q, want %q", tc.lang, tc.name, tc.input, got, tc.want)
		}
	}
	// Every tool an Agent calls without a card, the dispatch tools that still ask, and every kind of report has its
	// own name in each language.
	for _, l := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		for _, tool := range append(append([]string{}, assistantwire.AgentOwnTools...), "dispatch_start", "dispatch_read") {
			if toolTitle(l, tool) == "" {
				t.Errorf("%s: %s has no name for people", l, tool)
			}
		}
		for _, kind := range []string{"progress", "question", "blocked", "done"} {
			if got := personaToolTitle(l, "mcp__persona__report_to_supervisor", json.RawMessage(`{"kind":"`+kind+`"}`)); got == toolTitle(l, "report_to_supervisor") {
				t.Errorf("%s: a %s report has no kind in its title: %q", l, kind, got)
			}
		}
	}
}

// The manager titles a node's card for one of Persona's MCP tools itself, replacing whatever the node said; a card
// for any other tool keeps none. Where the title says what the call does (a report's kind), the card also names the
// tool alone, for what "Always allow" covers.
func TestNodeCardForAPersonaToolIsTitledByTheManager(t *testing.T) {
	ft := newFleetTest(t)
	s := ft.s
	l := s.lang(context.Background())
	no := false
	report := i18n.T(l, "assistant-tool_titles.report_to_supervisor")
	for i, tc := range []struct {
		name, input, want, wantName string
	}{
		{"mcp__persona__dispatch_start", `{"machine":"m2","prompt":"x"}`, i18n.T(l, "assistant-tool_titles.dispatch_start"), ""},
		{"mcp__persona__phone_notify", `{"title":"x"}`, i18n.T(l, "assistant-tool_titles.family_phone") + " · notify", ""},
		{"mcp__persona__report_to_supervisor", `{"kind":"done","text":"x"}`,
			report + " · " + i18n.T(l, "assistant-tool_titles.report_to_supervisor_done"), report},
		{"mcp__persona__report_to_supervisor", `{"text":"x"}`, report, ""},
		{"Bash", `{"command":"ls"}`, "", ""},
	} {
		id := fmt.Sprintf("ix_t_%d", i) // a resumable card asked again under its id is the same card
		ix := alwaysIx(id, "agent")
		ix.ToolName, ix.ToolInput = tc.name, json.RawMessage(tc.input)
		ix.ToolTitle, ix.ToolNameTitle = "said by the node", "said by the node"
		go func() { _, _ = s.nodeInteraction(context.Background(), ix) }()
		got := ft.waitPending(1)[0]
		if got.ToolTitle != tc.want || got.ToolNameTitle != tc.wantName {
			t.Errorf("%s %s: tool_title %q, tool_name_title %q; want %q, %q", tc.name, tc.input, got.ToolTitle, got.ToolNameTitle, tc.want, tc.wantName)
		}
		_ = s.resolve(id, proto.Resolution{Allow: &no})
		ft.waitPending(0)
	}
}
