package kb

// Golden tests for the knowledge base's prompts (docs/assistant/prompts.md): what the model is asked, byte for byte, compared
// with testdata/prompts/<name>.golden. Regenerate after an intended change with
//
//	UPDATE_GOLDEN=1 go test ./internal/kb -run Golden
//
// and read the diff. All inputs are placeholders.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "prompts", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (UPDATE_GOLDEN=1 writes it)", name, err)
	}
	if got != string(want) {
		t.Errorf("%s differs from %s\n--- got ---\n%s\n--- want ---\n%s", name, path, got, want)
	}
}

type captureModel struct {
	mu    sync.Mutex // the knowledge points steps call it topicWorkers at a time
	reply string
	calls [][2]string // system, user
}

func (m *captureModel) JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error {
	m.mu.Lock()
	m.calls = append(m.calls, [2]string{system, user})
	m.mu.Unlock()
	return json.Unmarshal([]byte(m.reply), out)
}

func (m *captureModel) golden(t *testing.T, name string) {
	t.Helper()
	if len(m.calls) != 1 {
		t.Fatalf("%s: %d model calls, want 1", name, len(m.calls))
	}
	golden(t, name+".system", m.calls[0][0])
	golden(t, name+".user", m.calls[0][1])
	m.calls = nil
}

// goldenZone is the user's time zone in the golden tests: the archive is written in it, and the times below are built in
// it, so the goldens read the same on a hub in UTC and on a laptop in Shanghai.
var goldenZone = func() *time.Location {
	l, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}
	return l
}()

func goldenEngine(t *testing.T, m Model) *Engine {
	t.Helper()
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Rezone(goldenZone); err != nil { // the user's zone, whatever the machine's
		t.Fatal(err)
	}
	return &Engine{Store: st, Model: m, Owner: "示例用户", Logf: func(string, ...any) {}}
}

func TestGoldenReadme(t *testing.T) {
	e := goldenEngine(t, nil)
	raw, err := os.ReadFile(filepath.Join(e.Store.Dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "readme", string(raw))
}

func TestGoldenCompress(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, goldenZone)
	for _, c := range []struct {
		name string
		chat Chat
		kb   bool // projects, tags and a summary exist
	}{
		{"group_empty", Chat{ID: "lark-f1", Source: "lark", SourceID: "f1", Kind: "group", Name: "发布群"}, false},
		{"dm_kb", Chat{ID: "lark-f2", Source: "lark", SourceID: "f2", Kind: "dm", Name: "张三"}, true},
		{"mail_automated", Chat{ID: "mail-acc1-noreply@example.com", Source: "mail", SourceID: "acc1-noreply@example.com", Kind: "mail", Name: "示例账单"}, false},
		{"moment", Chat{ID: "moment-p1", Source: "moment", SourceID: "p1", Kind: "moment", Name: "一条帖子"}, false},
	} {
		m := &captureModel{reply: `{}`}
		e := goldenEngine(t, m)
		ch := c.chat
		ch.Created, ch.LastAt, ch.Pending = at, at, 2
		if c.kb {
			_ = e.Store.PutProject(&Project{Slug: "demo-release", Name: "示例发布", Aliases: []string{"发布", "灰度"}, Status: "active", OneLine: "周五灰度", Tagging: Tagging{Tags: []string{"发布", "后端"}}})
			_ = e.Store.PutProject(&Project{Slug: "demo-old", Name: "合并掉的", Status: "merged", MergedInto: "demo-release"})
			_ = e.Store.PutProject(&Project{Slug: "demo-done", Name: "做完的", Status: "done"})
			_ = e.Store.PutProject(&Project{Slug: "demo-infra", Name: "示例基建", Status: "active", Tagging: Tagging{Tags: []string{"后端"}}})
			ch.Tags = []string{"发布"}
		}
		if err := e.Store.PutChat(&ch); err != nil {
			t.Fatal(err)
		}
		if c.kb {
			_ = e.Store.PutSummary(KindChat, ch.ID, "# 张三\n\n## 这是什么\n私聊")
		}
		_ = e.Store.AppendMessages(&ch, []Line{{At: at, Sender: "张三", Text: "周五灰度能提前吗"}, {At: at.Add(time.Minute), Sender: "我", Text: "我看下\n第二行"}})
		if err := e.Compress(ctx, &ch); err != nil {
			t.Fatal(err)
		}
		m.golden(t, "compress."+c.name)
	}
}

func TestGoldenProject(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, goldenZone)
	for _, c := range []struct {
		name string
		full bool
	}{{"empty", false}, {"full", true}} {
		m := &captureModel{reply: `{}`}
		e := goldenEngine(t, m)
		p := &Project{Slug: "demo-release", Name: "示例发布", Kind: "short", Status: "active", Dirty: true}
		if c.full {
			p.Aliases = []string{"发布", "灰度"}
			p.Tagging.Tags = []string{"发布"}
			long := ""
			for range 1600 {
				long += "长"
			}
			for i, id := range []string{"lark-f1", "lark-f2", "lark-f3", "lark-f4", "lark-f5", "lark-f6", "lark-f7", "lark-f8", "lark-f9"} {
				p.Chats = append(p.Chats, id)
				sum := "# 会话 " + id + "\n\n进展"
				if i == 1 {
					sum = long
				}
				_ = e.Store.PutChat(&Chat{ID: id})
				_ = e.Store.PutSummary(KindChat, id, sum)
			}
			p.Chats = append(p.Chats, "lark-none")
			// Two linked Moments posts (the more commented first), one gone from the archive.
			_ = e.Store.PutMomentPost(&MomentPost{ID: "m1", Author: "王五", Text: "示例发布的新功能好用吗", CommentCount: 3, At: at}, nil)
			_ = e.Store.PutMomentPost(&MomentPost{ID: "m2", Anonymous: true, Text: "灰度后卡顿\n第二行", CommentCount: 12, At: at.Add(time.Hour)}, nil)
			p.Moments = []ProjectMoment{{ID: "m1", Note: "用户提问", At: at}, {ID: "m2", Note: "用户反馈卡顿", At: at}, {ID: "gone", At: at}}
		}
		if err := e.Store.PutProject(p); err != nil {
			t.Fatal(err)
		}
		if c.full {
			_ = e.Store.PutSummary(KindProject, p.Slug, "# 示例发布\n\n## 目标\n周五灰度")
			_ = e.Store.AppendLog(p.Slug, at, "lark-f1", "定了周五灰度")
			_ = e.Store.AppendLog(p.Slug, at.Add(time.Hour), "lark-f2", "提前到周四")
		}
		if n := e.RefreshProjects(ctx, 5); n != 1 {
			t.Fatalf("refreshed %d", n)
		}
		m.golden(t, "project."+c.name)
	}
}

func TestGoldenItems(t *testing.T) {
	m := &captureModel{reply: `{"items":[]}`}
	e := goldenEngine(t, m)
	_ = e.Store.PutProject(&Project{Slug: "demo-release", Name: "示例发布", Status: "active", Tagging: Tagging{Tags: []string{"发布"}}})
	_ = e.Store.PutChat(&Chat{ID: "lark-f1", Source: "lark", SourceID: "f1", Kind: "group", Name: "发布群"})
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, goldenZone)
	for _, it := range []*Item{
		{Kind: KindEvent, ID: "e1", Title: "周会", At: at, Where: "calendar"},
		{Kind: KindMeeting, ID: "m1", Title: "评审会", Where: "lark-f1"},
		{Kind: KindDoc, ID: "d/1", Title: "设计文档", At: at, Where: "lark-f1"},
	} {
		_ = e.Store.PutItem(it)
	}
	if _, err := e.TagItems(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	m.golden(t, "items")
}

func TestGoldenDigest(t *testing.T) {
	e := goldenEngine(t, nil)
	golden(t, "digest.empty", e.Prompt(6, 8))
	for i, p := range []*Project{
		{Slug: "a", Name: "示例发布", Status: "active", Kind: "short", Tagging: Tagging{Importance: "P0", Relevance: 95}, OneLine: "周五灰度"},
		{Slug: "b", Name: "没打标的", Status: "active", Kind: "long"},
		{Slug: "c", Name: "做完的", Status: "done", Kind: "short", Tagging: Tagging{Importance: "P0", Relevance: 99}},
		{Slug: "d", Name: "忽略的", Status: "ignored", Kind: "short", Tagging: Tagging{Importance: "P0", Relevance: 99}},
		{Slug: "e", Name: "示例基建", Status: "active", Kind: "long", Tagging: Tagging{Importance: "P2", Relevance: 40}, OneLine: "迁移中"},
	} {
		p.Updated = time.Date(2026, 10, 30, 10, i, 0, 0, time.UTC)
		_ = e.Store.PutProject(p)
	}
	golden(t, "digest.projects", e.Prompt(1, 8))
	for _, c := range []*Chat{
		{ID: "lark-f1", Kind: "group", Name: "发布群", OneLine: "在推发布", Tagging: Tagging{Importance: "P1", Relevance: 70}},
		{ID: "lark-f2", Kind: "dm", Name: "张三", Tagging: Tagging{Importance: "P3", Relevance: 90}},
		{ID: "lark-f3", Kind: "dm", Name: "李四", OneLine: "约饭"},
	} {
		_ = e.Store.PutChat(c)
	}
	golden(t, "digest.full", e.Prompt(6, 8))
	golden(t, "digest.chats_only", e.Prompt(0, 8))
}

func TestGoldenDedupe(t *testing.T) {
	m := &captureModel{reply: `{"same":false}`}
	e := goldenEngine(t, m)
	_ = e.Store.PutChat(&Chat{ID: "lark-f1", Kind: "group", Name: "示例需求群"})
	_ = e.Store.PutChat(&Chat{ID: "lark-f2", Kind: "group", Name: "示例内部群"})
	people := []string{"lark-u1", "lark-u2", "lark-u3", "lark-u4"}
	_ = e.Store.PutProject(&Project{Slug: "a", Name: "示例需求", Status: "active", OneLine: "走查中", Goal: "上线多窗口", Chats: []string{"lark-f1"}, People: people})
	_ = e.Store.PutProject(&Project{Slug: "b", Name: "示例方案", Aliases: []string{"方案二期"}, Status: "active", Chats: []string{"lark-f2", "lark-none"}, People: people[:3]})
	_ = e.Store.PutSummary(KindProject, "a", "# 示例需求\n\n## 目标\n多窗口")
	if n := e.DedupeProjects(context.Background(), 5); n != 0 {
		t.Fatalf("merged %d", n)
	}
	m.golden(t, "dedupe")
}

func TestGoldenMomentsLink(t *testing.T) {
	m := &captureModel{reply: `{"posts":[]}`}
	e := goldenEngine(t, m)
	at := time.Now().Add(-48 * time.Hour).Truncate(24 * time.Hour)
	_ = e.Store.PutProject(&Project{Slug: "a", Name: "示例发布", Aliases: []string{"发布"}, Keywords: []string{"示例App", "灰度"}, Status: "active", OneLine: "周五灰度"})
	_ = e.Store.PutSummary(KindProject, "a", "# 示例发布\n\n## 目标\n周五灰度")
	_ = e.Store.PutMomentPost(&MomentPost{ID: "m1", Author: "王五", Text: "示例App 灰度了吗", CommentCount: 3, At: at}, nil)
	_ = e.Store.PutMomentPost(&MomentPost{ID: "m2", Anonymous: true, Text: "今天的灰度\n第二行", CommentCount: 0, At: at}, nil)
	_ = e.Store.PutMomentPost(&MomentPost{ID: "m3", Author: "赵六", Text: "无关的帖子", At: at}, nil)
	if n := e.LinkMoments(context.Background(), 3); n != 0 {
		t.Fatalf("linked %d", n)
	}
	if len(m.calls) == 1 {
		// The post dates move with the clock: put a fixed one in the golden.
		m.calls[0][1] = strings.ReplaceAll(m.calls[0][1], at.In(goldenZone).Format("2006-01-02"), "2026-10-30")
	}
	m.golden(t, "moments_link")
}

// The day recap's merge (kb/day_recap_merge), rendered directly: the known meetings of every kind, a long day left
// partial, a chunk with no summary, entries with and without points' speakers, and actions with and without deadlines.
func TestGoldenDayRecapMerge(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 11, h, m, 0, 0, goldenZone) }
	day := prompts.KBDayRecapDay{Pinned: "示例用户 在示例公司做发布", Title: "出差", Day: "2026-11-11", Weekday: "周三",
		Zone: "Asia/Shanghai", Language: "zh-CN", Part: 2}
	in := prompts.KBDayRecapMergeInput{Day: day, Partial: true, Meetings: []prompts.KBDayMeeting{
		{N: 1, Kind: KindRecording, Title: "设计评审", From: at(9, 0), To: at(9, 45)},
		{N: 2, Kind: KindMeeting, Title: "周会（视频）", From: at(14, 0)},
		{N: 3, Kind: KindEvent, Title: "午饭约", From: at(12, 0)},
	}, Chunks: []prompts.KBDayRecapChunk{
		{From: "09:01", To: "09:44", Summary: "评审导出的上限", Entries: []prompts.KBDayRecapEntry{
			{FromLine: 12, ToLine: 40, Kind: "meeting", Title: "导出评审", Points: []prompts.KBMinutesNote{
				{Line: 15, Text: "导出超过 4 MB 的条目会被丢掉", Who: "Ada"}, {Line: 31, Text: "这一版先保留上限"}}},
		}, Actions: []prompts.KBMinutesNote{{Line: 38, Text: "补上回归测试", Who: "Bo", By: "周五前", DueDate: "2026-11-13"}}},
		{From: "12:03", To: "12:40", Entries: []prompts.KBDayRecapEntry{
			{FromLine: 90, ToLine: 120, Kind: "outside", Title: "午饭", Points: []prompts.KBMinutesNote{{Line: 95, Text: "聊到下周出差"}}},
		}, Actions: []prompts.KBMinutesNote{{Line: 101, Text: "订周一的机票"}}},
	}}
	golden(t, "day_recap_merge.system", prompts.RenderKBDayRecapMerge(prompts.KBOwner{Owner: "示例用户"}))
	golden(t, "day_recap_merge.user", prompts.RenderKBDayRecapMergeInput(in))
	in.Meetings, in.Partial, in.Day = nil, false, prompts.KBDayRecapDay{Day: "2026-11-11", Weekday: "周三", Zone: "Asia/Shanghai"}
	golden(t, "day_recap_merge.no_meetings.user", prompts.RenderKBDayRecapMergeInput(in))
}
