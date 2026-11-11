package assistant

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Opt-in synthetic model acceptance. Supply the resolved sorting ai.Config in a
// private file; this test never reads real conversations or writes hub state.
func TestAgentNameLiveSynthetic(t *testing.T) {
	file := os.Getenv("PERSONA_AGENT_NAMING_EVAL_CONFIG")
	if file == "" {
		t.Skip("opt-in synthetic model acceptance")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal("cannot read the private evaluation configuration")
	}
	var cfg ai.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal("invalid evaluation configuration")
	}
	client := ai.New(cfg)
	cases := []struct{ bad, first, recent string }{
		{"◐ Claude Code", "用中文工作，最后用中文汇报。修复 PDF 导出时图片丢失的问题。", "user: 导出文档里必须保留图片。\nassistant: 正在修复图片路径。"},
		{"guest", "请给购物清单增加按类别分组，方便找东西。", "assistant: 将蔬菜、日用品分别归类。"},
		{"任务：…", "请全程用中文工作和汇报。 ## 任务\n在设置里支持每周备份。", "user: 默认不开启自动备份。\nassistant: 正在添加每周备份设置。"},
		{"/loop …", "每小时检查一次文档导出的测试结果。", "user: 现在先修复导出表格时的列错位，暂时不检查测试结果。\nassistant: 开始定位表格列宽。"},
		{"Claude Code", "Fix search results losing their highlighted keywords.", "user: Keep the matching word highlighted after sorting.\nassistant: Checking result rendering."},
		{"用中文工作，最后用中文汇报。 ## 需求（用户原话）…", "通知設定に毎日のまとめを追加してください。", "user: 初期設定ではオフにしてください。\nassistant: 通知設定を確認しています。"},
	}
	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		r, err := client.Chat(ctx, ai.Request{System: prompts.RenderAgentSessionName(), Messages: []ai.Message{{Role: ai.RoleUser, Content: prompts.RenderAgentSessionNameInput(prompts.AgentSessionNameInput{First: tc.first, Recent: tc.recent})}}, MaxTokens: 256, BoundOutput: true})
		cancel()
		if err != nil {
			t.Fatalf("synthetic naming failed for %q; private provider details omitted", tc.bad)
		}
		name := strings.TrimSpace(strings.Trim(strings.TrimSpace(r.Text), "\"“”『』"))
		if r.Truncated || !validAgentName(name) {
			t.Fatalf("invalid synthetic title for %q: %q (truncated=%v)", tc.bad, name, r.Truncated)
		}
		t.Logf("%q -> %q (input_tokens=%d output_tokens=%d)", tc.bad, name, r.Usage.InputTokens, r.Usage.OutputTokens)
	}
}
