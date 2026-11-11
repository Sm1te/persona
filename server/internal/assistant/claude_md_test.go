package assistant

import (
	"context"
	"github.com/nwverse/persona/server/internal/compat"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/prompts"
)

// claudeMDHub is a fake hub with the machines and what the hub says for the assistant's directory.
func claudeMDHub(devices ...Device) *fakeHub {
	f := newFakeHub()
	f.devices = devices
	for _, d := range devices {
		f.sessions[d.ID] = nil
		f.hello.Assistant.Machines = append(f.hello.Assistant.Machines, d.ID)
	}
	if len(devices) > 0 {
		f.hello.Assistant.MachineID = devices[0].ID // the hub's own machine
	}
	return f
}

// The CLAUDE.md the assistant writes into its directory from what the hub tells it (claudeMD, prepare).
func TestGoldenAssistantClaudeMD(t *testing.T) {
	ctx := context.Background()
	f := claudeMDHub(Device{ID: "server", Name: "服务器", Kind: "local"}, Device{ID: "laptop", Name: "笔记本", Kind: "ssh", SSHHost: "laptop.example"},
		Device{ID: "desk", Name: "台式机", Kind: "ssh"})
	f.hello.Owner = "示例用户"
	f.hello.OwnerNotes = "  我在示例公司做后端。\n常用 skills：demo-skill。  "
	f.hello.KB = &KBSettings{ExtraDirs: []string{"/home/me/notes", "/home/me/facts"}} // manager.json's kb section
	s := newTestService(t, f)
	s.kb.dir = "<hub>/data"
	golden(t, "assistant_claude_md.full", s.claudeMD(ctx, "server", false))

	bare := claudeMDHub(Device{ID: "server", Name: "服务器", Kind: "local"})
	s = newTestService(t, bare)
	s.kb.dir = "<hub>/data"
	golden(t, "assistant_claude_md.bare", s.claudeMD(ctx, "server", false))
	// lark-cli (docs/plans/plan-lark-cli.md).
	golden(t, "assistant_claude_md.lark_cli", s.claudeMD(ctx, "server", true))
	// hub.json assistant.persona_view: cards are persona-view, the extensions' components (#1850).
	bare.hello.PersonaView = true
	golden(t, "assistant_claude_md.persona_view", s.claudeMD(ctx, "server", false))
	bare.hello.PersonaView = false

	// prepare writes it with what the hub says of lark-cli, for the machine it prepares.
	bare.larkCLI = true
	if _, err := s.prepare(ctx, "server"); err != nil {
		t.Fatal(err)
	}
	if got := bare.prepared[0]["claude_md"]; got != s.claudeMD(ctx, "server", true) || bare.prepared[0]["dir"] != compat.StateDir+"/assistant" {
		t.Fatalf("assistant.prepare: %v", bare.prepared[0])
	}
}

func TestGoldenAssistantViewRepair(t *testing.T) {
	golden(t, "assistant_view_repair", prompts.RenderAssistantViewRepair(prompts.AssistantViewRepair{Errors: []string{"persona-view block 0: fallback: include a plain text summary"}}))
}

func TestAssistantRichViewPromptExamplesAreValid(t *testing.T) {
	text := prompts.RenderAssistantClaudeMD(prompts.AssistantClaudeMD{PersonaView: true})
	blocks := wire.AssistantViewBlocks(text)
	if len(blocks) != 2 {
		t.Fatalf("prompt has %d examples, want 2", len(blocks))
	}
	for _, block := range blocks {
		if _, err := block.Parse(); err != nil {
			t.Fatalf("prompt teaches an invalid view: %v", err)
		}
	}
	for _, want := range []string{"view_show", "view_update", "view_get", "submit=true", "ask_user", "fallback", "自动运行"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing rich view guidance: %s", want)
		}
	}
	if strings.Contains(text, "助手的卡片点不了") {
		t.Fatal("interactive guidance still teaches read-only cards")
	}
}

// The machines' storage roots are where they say (the node's data directory, the hub's knowledge directory), not the
// default ones; only an assistant on the hub's own machine reads the knowledge directory directly.
func TestAssistantPromptUsesReportedStorageRoots(t *testing.T) {
	ctx := context.Background()
	f := claudeMDHub(Device{ID: "server", Name: "服务器", Kind: "local", Home: "/home/example", DataDir: "/var/lib/assistant-state"})
	s := newTestService(t, f)
	s.kb.dir = "/var/lib/hub state/data"
	text := s.claudeMD(ctx, "server", false)
	for _, want := range []string{"/var/lib/assistant-state/assistant", "/var/lib/hub state/data"} {
		if !strings.Contains(text, want) {
			t.Fatalf("reported storage root missing: %s", want)
		}
	}
	if strings.Contains(text, "~/.persona/") || strings.Contains(text, "~/.persona/") {
		t.Fatal("hardcoded default root overrides reported storage roots")
	}
	if !strings.Contains(text, "也可以用文件工具直接读取此目录") {
		t.Fatal("a local assistant was not allowed to read its local knowledge directory")
	}
	f.devices[0].Kind = "link"
	text = s.claudeMD(ctx, "server", false)
	if strings.Contains(text, "也可以用文件工具直接读取此目录") || !strings.Contains(text, "通过 `kb_*` 工具读取") {
		t.Fatal("a remote assistant was told to read the hub's local files directly")
	}
}

// The assistant's instructions said "the one you are on" only of the hub's own machine and gave everything else an
// `ssh <alias>` command, but the aliases are the hub machine's and a link machine has no ssh: an assistant on a laptop
// was told to ssh into itself through a name only the hub knows (2026-10-31).
func TestAssistantMachinesFromWhereItRuns(t *testing.T) {
	machines := []Device{
		{ID: "server", Name: "服务器", Kind: "local"},
		{ID: "box", Name: "台式机", Kind: "ssh", SSHHost: "box-alias"},
		{ID: "laptop", Name: "笔记本", Kind: "link"},
	}
	lines := func(at string) map[string]string {
		s := newTestService(t, claudeMDHub(machines...))
		setSettings(t, s, func(v *Settings) { v.MachineID = at })
		ctx := context.Background()
		out := map[string]string{}
		for _, line := range strings.Split(s.claudeMD(ctx, s.settings(ctx).MachineID, false), "\n") {
			for _, m := range machines {
				if strings.HasPrefix(line, "  - "+m.Name) {
					out[m.ID] = line
				}
			}
		}
		if len(out) != len(machines) {
			t.Fatalf("the instructions leave out machines: %v", out)
		}
		return out
	}
	here, ssh, noCommand := "就是你所在的这台机器", "`ssh box-alias '<命令>'`", "不能直接在它上面执行命令"
	for _, tc := range []struct {
		at   string
		want map[string]string
	}{
		{"server", map[string]string{"server": here, "box": ssh, "laptop": noCommand}},
		{"box", map[string]string{"server": noCommand, "box": here, "laptop": noCommand}},
		{"laptop", map[string]string{"server": noCommand, "box": noCommand, "laptop": here}},
	} {
		got := lines(tc.at)
		for id, want := range tc.want {
			if !strings.Contains(got[id], want) {
				t.Errorf("assistant on %s, line for %s = %q, want %q", tc.at, id, got[id], want)
			}
			if tc.at != "server" && strings.Contains(got[id], "ssh ") {
				t.Errorf("assistant on %s is told to ssh: %q", tc.at, got[id])
			}
		}
	}
}

func TestGoldenMainHandoff(t *testing.T) {
	golden(t, "main_handoff", prompts.RenderMainHandoff(prompts.MainHandoff{FromID: "a1b2c3d4-0000-4000-8000-000000000001",
		Until: "11月4日 21:30", Summary: "示例摘要：用户在整理周末出游计划，酒店已订好，还差火车票。",
		Recent: []prompts.HandoffTurn{{Role: "user", Text: "示例问题：火车票买了吗"}, {Role: "assistant", Text: "示例回答：还没有，等你确认时间"}},
		Text:   "今天有什么安排"}))
	golden(t, "main_handoff.recent_only", prompts.RenderMainHandoff(prompts.MainHandoff{FromID: "a1b2c3d4-0000-4000-8000-000000000001",
		Until: "11月4日 21:30", Recent: []prompts.HandoffTurn{{Role: "user", Text: "示例问题"}}, Text: "接着说"}))
	golden(t, "main_handoff.empty", prompts.RenderMainHandoff(prompts.MainHandoff{FromID: "a1b2c3d4-0000-4000-8000-000000000001",
		Until: "11月4日 21:30", Text: "你好"}))
}

// The CLAUDE.md asks for the user's language (docs/protocol/i18n.md); zh-Hans, or a hub that says none, keeps it as it was.
func TestAssistantClaudeMDLanguage(t *testing.T) {
	ctx := context.Background()
	f := claudeMDHub(Device{ID: "server", Name: "服务器", Kind: "local"})
	s := newTestService(t, f)
	s.kb.dir = "<hub>/data"
	before := s.claudeMD(ctx, "server", false)
	if !strings.Contains(before, "；用中文。") {
		t.Fatal("the Chinese CLAUDE.md lost 用中文")
	}
	f.hello.Locale = "zh-Hans"
	if got := s.claudeMD(ctx, "server", false); got != before {
		t.Fatal("zh-Hans changed the CLAUDE.md")
	}
	for locale, want := range map[string]string{"en": "用English回答", "ja": "用日本語回答"} {
		f.hello.Locale = locale
		got := s.claudeMD(ctx, "server", false)
		if !strings.Contains(got, want) || strings.Contains(got, "；用中文。") {
			t.Fatalf("%s: no %q", locale, want)
		}
	}
}

// Process text, the lines between tool calls, is in the user's language too (#4883: the only line a Chinese owner saw
// in six minutes of work was English), in every language the CLAUDE.md is written for, and in the health space's.
func TestAssistantClaudeMDProcessTextLanguage(t *testing.T) {
	ctx := context.Background()
	f := claudeMDHub(Device{ID: "server", Name: "服务器", Kind: "local"})
	s := newTestService(t, f)
	s.kb.dir = "<hub>/data"
	const rule = "工作中写给用户看的每一句（工具调用之间的说明、进度、在等什么）也用这种语言，不因为读的代码、日志或资料是英文就改用英文。"
	for _, locale := range []string{"", "zh-Hans", "en", "ja"} {
		f.hello.Locale = locale
		if got := s.claudeMD(ctx, "server", false); !strings.Contains(got, rule) {
			t.Errorf("locale %q: no process-text language rule", locale)
		}
	}
	if !strings.Contains(s.healthClaudeMD(ctx, "server"), rule) {
		t.Error("health CLAUDE.md: no process-text language rule")
	}
}
