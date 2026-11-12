package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestBadAgentNames(t *testing.T) {
	for _, name := range []string{"", "Claude Code", "◐ Claude Code", "◑ Claude Code", "⠋ Claude Code", "persona", "guest", "example-project", "用中文工作，最后用中文汇报。 ## 需求（用户原话）", "请全程用中文工作和汇报。 ## 任务", "任务：修复导出", "/loop 检查任务", "这是一次只读技术验收。检查导出", "Task: implement export", "## Task"} {
		if !badAgentName(name, "/work/example-project") {
			t.Errorf("bad name not recognized: %q", name)
		}
	}
	for _, name := range []string{"修复会话标题", "Improve PDF export", "エクスポートを修正", "中文翻译", "Review the design", "Background job retries"} {
		if badAgentName(name, "/work/example-project") {
			t.Errorf("meaningful name rejected: %q", name)
		}
	}
	for _, name := range []string{"◐ 修复导出", "# 修复导出", "Claude Code", strings.Repeat("名", 41), "two\nlines", "<title>"} {
		if validAgentName(name) {
			t.Errorf("invalid model name accepted: %q", name)
		}
	}
}

func TestAgentNameDue(t *testing.T) {
	now := time.Now()
	c := proto.Session{SessionID: "s1", Title: "Claude Code", TitleSource: proto.TitleSourcePrompt, Status: proto.StatusWorking, UpdatedAt: now, OutputTokens: 100}
	if !agentNameDue(Settings{}, agentNameRecord{}, c, now) {
		t.Fatal("fresh defaults must correct a placeholder")
	}
	r := agentNameRecord{Changes: 1, NamedAt: now.Add(-25 * time.Hour), NamedOutput: 90}
	if agentNameDue(Settings{}, r, c, now) {
		t.Fatal("default one change was exceeded")
	}
	max, interval := 3, 24
	v := Settings{AgentAutoNamingMax: &max, AgentAutoNamingIntervalHours: &interval}
	if !agentNameDue(v, r, c, now) {
		t.Fatal("active output did not qualify for daily refresh")
	}
	good := c
	good.Title = "Fix export"
	r.Changes = 0
	if !agentNameDue(v, r, good, now) {
		t.Fatal("periodic opt-in must refresh an existing meaningful name too")
	}
	r.Changes = 1
	for _, mutate := range []func(*Settings, *agentNameRecord, *proto.Session){
		func(v *Settings, r *agentNameRecord, c *proto.Session) { off := false; v.AgentAutoNaming = &off },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { r.Changes = 3 },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.OutputTokens = r.NamedOutput },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.Status = proto.StatusIdle },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.UpdatedAt = now.Add(-time.Hour) },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { r.NamedAt = now.Add(-time.Hour) },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { r.Manual = true },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.TitleManual = true },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.TitleSource = proto.TitleSourceManual },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.TitleSource = proto.TitleSourceUnknown },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.Assistant = true },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { c.Archived = true },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { r.NextAttempt = now.Add(time.Hour) },
		func(v *Settings, r *agentNameRecord, c *proto.Session) { r.Failures = agentNamingFailureLimit },
	} {
		vv, rr, cc := v, r, c
		mutate(&vv, &rr, &cc)
		if agentNameDue(vv, rr, cc, now) {
			t.Errorf("ineligible state accepted: %+v %+v %+v", vv, rr, cc)
		}
	}
	if agentNameKey("a/b", "c") == agentNameKey("a", "b/c") {
		t.Fatal("opaque session keys collide")
	}
}

func namingFixture(t *testing.T, answer func(http.ResponseWriter, *http.Request)) (*Service, *fakeHub, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); answer(w, r) }))
	t.Cleanup(model.Close)
	f := newFakeHub().withMachine()
	f.ownModels = true
	// Different configured defaults prove that the sorting purpose is used, not title processing.
	f.hello.Models.AI = ai.Config{BaseURL: model.URL, APIKey: "fixture", Model: "processing"}
	f.hello.Models.TriageAI = &ai.Config{BaseURL: model.URL, APIKey: "fixture", Model: "sorting", ReasoningEffort: "off"}
	f.sessions["server"] = []proto.Session{{SessionID: "s1", Title: "◐ Claude Code", TitleSource: proto.TitleSourcePrompt, Status: proto.StatusWorking, UpdatedAt: time.Now(), OutputTokens: 123}}
	f.items = []proto.Item{{Seq: 1, Role: "user", Kind: proto.KindText, Text: "用中文工作，最后用中文汇报。修复 PDF 导出时丢失图片的问题。"}, {Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "正在检查导出图片的路径。"}, {Seq: 3, Role: "user", Kind: proto.KindText, Text: "先保证导出文件里能看到图片。"}}
	return newTestService(t, f), f, calls
}

func namingReply(w http.ResponseWriter, text string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "stop_reason": "end_turn", "usage": map[string]int{"input_tokens": 100, "output_tokens": 10}})
}

func TestAgentNameLedgerPersists(t *testing.T) {
	database := todoTestDB(t)
	f := newFakeHub().withMachine()
	s := newServiceOn(t, f, database)
	key := agentNameKey("server", "s1")
	rec := agentNameRecord{Machine: "server", Session: "s1", Title: "Fix export", RawTitle: "Claude Code", Changes: 1, NamedAt: time.Now(), NamedOutput: 100, NextAttempt: time.Now().Add(time.Minute)}
	s.agentNames.mu.Lock()
	if !s.agentNamesLocked(context.Background()) {
		t.Fatal("ledger failed to read")
	}
	if err := s.putAgentNameLocked(context.Background(), key, rec); err != nil {
		t.Fatal(err)
	}
	s.agentNames.mu.Unlock()
	restarted := newServiceOn(t, f, database)
	restarted.agentNames.mu.Lock()
	if !restarted.agentNamesLocked(context.Background()) {
		t.Fatal("restarted ledger failed to read")
	}
	got := restarted.agentNames.records[key]
	restarted.agentNames.mu.Unlock()
	if got.Title != rec.Title || got.Changes != 1 || got.NamedOutput != 100 || got.NextAttempt.IsZero() {
		t.Fatalf("lost durable state: %+v", got)
	}
	if agentNameDue(Settings{}, got, proto.Session{SessionID: "s1", Title: "Claude Code", TitleSource: proto.TitleSourcePrompt}, time.Now()) {
		t.Fatal("restart repeated one-shot call")
	}
}

type failingAgentNameStore struct{ Store }

func (f failingAgentNameStore) Setting(ctx context.Context, key string, v any) (bool, error) {
	if f.Store == nil {
		return false, nil
	}
	return f.Store.Setting(ctx, key, v)
}
func (f failingAgentNameStore) PutSetting(ctx context.Context, key string, v any) error {
	if key == agentNamingKey {
		return errors.New("synthetic ledger write failure")
	}
	if f.Store == nil {
		return nil
	}
	return f.Store.PutSetting(ctx, key, v)
}
func TestAgentNameLedgerWriteFailureDoesNotPublish(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "Fix export") })
	// Admission must fail before model dispatch when the durable lease cannot be saved.
	base := s.store()
	s.store = func() Store { return failingAgentNameStore{base} }
	c := f.sessions["server"][0]
	if s.tryAgentName(context.Background(), "server", c, time.Now()) || calls.Load() != 0 || s.agentDisplayName("server", c) != "" {
		t.Fatal("failed lease was dispatched or published")
	}
}

func TestAgentNameModelAndOneShot(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "sorting" || body["max_tokens"] != float64(256) {
			t.Errorf("wrong model/budget: %+v", body)
		}
		namingReply(w, "修复 PDF 导出图片")
	})
	c := f.sessions["server"][0]
	if !s.tryAgentName(context.Background(), "server", c, time.Now()) {
		t.Fatal("did not call model")
	}
	if got := s.agentDisplayName("server", c); got != "修复 PDF 导出图片" {
		t.Fatalf("shared title: %q", got)
	}
	if s.tryAgentName(context.Background(), "server", c, time.Now()) || calls.Load() != 1 {
		t.Fatal("default generated a second name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.Method == DeviceSessionRename || call.Method == DeviceSessionSend {
			t.Fatalf("background naming touched Agent input: %+v", call)
		}
	}
}

func TestAgentNameFailureCooldown(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	c := f.sessions["server"][0]
	if !s.tryAgentName(context.Background(), "server", c, time.Now()) {
		t.Fatal("first attempt missing")
	}
	for i := 0; i < 100; i++ {
		if s.tryAgentName(context.Background(), "server", c, time.Now()) {
			t.Fatal("retry storm")
		}
	}
	if calls.Load() != 1 || s.agentDisplayName("server", c) != "" {
		t.Fatal("failure changed name or retried")
	}
}

func TestAgentNameManualAndSettingsRace(t *testing.T) {
	for _, manual := range []bool{true, false} {
		t.Run(map[bool]string{true: "manual", false: "disabled"}[manual], func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			s, f, _ := namingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
				namingReply(w, "修复 PDF 导出图片")
			})
			c := f.sessions["server"][0]
			done := make(chan struct{})
			go func() { defer close(done); s.tryAgentName(context.Background(), "server", c, time.Now()) }()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("model never started")
			}
			if manual {
				if _, err := s.Rename(context.Background(), RenameParams{MachineID: "server", SessionID: "s1", Params: map[string]any{"title": "我选的名字"}}); err != nil {
					t.Fatal(err)
				}
			} else {
				setSettings(t, s, func(v *Settings) { off := false; v.AgentAutoNaming = &off })
			}
			close(release)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("model never ended")
			}
			if manual {
				if got := s.agentDisplayName("server", c); got != "我选的名字" {
					t.Fatalf("manual rename overwritten: %q", got)
				}
			} else if got := s.agentDisplayName("server", c); got != "" {
				t.Fatalf("disabled save admitted: %q", got)
			}
		})
	}
}

func TestAgentNamePeriodicRefreshUsesNewOutputAndTotalLimit(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复 PDF 导出图片") })
	maximum, hours := 2, 24
	setSettings(t, s, func(v *Settings) { v.AgentAutoNamingMax = &maximum; v.AgentAutoNamingIntervalHours = &hours })
	c := f.sessions["server"][0]
	key := agentNameKey("server", c.SessionID)
	s.agentNames.mu.Lock()
	if !s.agentNamesLocked(context.Background()) {
		t.Fatal("ledger load")
	}
	rec := agentNameRecord{Machine: "server", Session: c.SessionID, Title: "整理导出文档", RawTitle: c.Title, Changes: 1, NamedAt: time.Now().Add(-25 * time.Hour), NamedOutput: c.OutputTokens}
	if err := s.putAgentNameLocked(context.Background(), key, rec); err != nil {
		t.Fatal(err)
	}
	s.agentNames.mu.Unlock()
	if s.tryAgentName(context.Background(), "server", c, time.Now()) || calls.Load() != 0 {
		t.Fatal("periodic refresh without new output")
	}
	c.OutputTokens++
	f.setSessions("server", c)
	if !s.tryAgentName(context.Background(), "server", c, time.Now()) || calls.Load() != 1 {
		t.Fatal("new output did not trigger refresh")
	}
	if got := s.agentDisplayName("server", c); got != "修复 PDF 导出图片" {
		t.Fatalf("refresh result: %q", got)
	}
	if s.tryAgentName(context.Background(), "server", c, time.Now().Add(25*time.Hour)) || calls.Load() != 1 {
		t.Fatal("periodic refresh exceeded total limit")
	}
}

func TestAgentNameUnchangedResultKeepsConfiguredInterval(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复 PDF 导出图片") })
	maximum, hours := 5, 24
	setSettings(t, s, func(v *Settings) { v.AgentAutoNamingMax = &maximum; v.AgentAutoNamingIntervalHours = &hours })
	c := f.sessions["server"][0]
	key := agentNameKey("server", c.SessionID)
	s.agentNames.mu.Lock()
	if !s.agentNamesLocked(context.Background()) {
		t.Fatal("ledger load")
	}
	rec := agentNameRecord{Machine: "server", Session: c.SessionID, Title: "修复 PDF 导出图片", RawTitle: c.Title, Changes: 1, NamedAt: time.Now().Add(-25 * time.Hour), NamedOutput: c.OutputTokens - 1}
	if err := s.putAgentNameLocked(context.Background(), key, rec); err != nil {
		t.Fatal(err)
	}
	s.agentNames.mu.Unlock()
	now := time.Now()
	if !s.tryAgentName(context.Background(), "server", c, now) || calls.Load() != 1 {
		t.Fatal("periodic check missing")
	}
	s.agentNames.mu.Lock()
	got := s.agentNames.records[key]
	s.agentNames.mu.Unlock()
	if got.Changes != 1 || got.NamedOutput != c.OutputTokens || !got.NamedAt.Equal(now) {
		t.Fatalf("unchanged name did not advance its cost baseline: %+v", got)
	}
	c.OutputTokens++
	c.UpdatedAt = now.Add(2 * time.Hour)
	v := s.settings(context.Background())
	if agentNameDue(v, got, c, now.Add(2*time.Hour)) {
		t.Fatal("daily setting retried an unchanged name after only two hours")
	}
	c.UpdatedAt = now.Add(25 * time.Hour)
	if !agentNameDue(v, got, c, now.Add(25*time.Hour)) {
		t.Fatal("daily refresh never became due again")
	}
}

func TestAgentNamesDrainAnIdleInventoryWithoutAnotherEvent(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复导出") })
	ctx, cancel := context.WithCancel(context.Background())
	s.running.Store(&ctx)
	t.Cleanup(cancel)
	s.agentNames.interval = 5 * time.Millisecond
	list := []proto.Session{}
	for _, id := range []string{"s1", "s2", "s3"} {
		list = append(list, proto.Session{SessionID: id, Title: "Claude Code", TitleSource: proto.TitleSourcePrompt, Status: proto.StatusIdle})
	}
	f.setSessions("server", list...)
	s.agentNamesChanged(DeviceSessions{MachineID: "server", Sessions: list})
	deadline := time.NewTimer(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer deadline.Stop()
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("one initial list did not drain all eligible idle names")
		case <-tick.C:
			s.agentNames.mu.Lock()
			complete := !s.agentNames.running && s.agentNames.timer == nil
			for _, c := range list {
				complete = complete && s.agentNames.records[agentNameKey("server", c.SessionID)].Changes == 1
			}
			s.agentNames.mu.Unlock()
			if complete {
				if calls.Load() != 3 {
					t.Fatalf("one-shot inventory calls=%d", calls.Load())
				}
				return
			}
		}
	}
}

func TestAgentNameContextBoundsAndGolden(t *testing.T) {
	s, f, _ := namingFixture(t, func(w http.ResponseWriter, r *http.Request) { namingReply(w, "修复导出") })
	f.items[0].Text = strings.Repeat("首条请求", 3000)
	for seq := 4; seq <= 7; seq++ {
		f.items = append(f.items, proto.Item{Seq: seq, Role: "assistant", Kind: proto.KindText, Text: strings.Repeat("最近回复", 3000)})
	}
	first, recent, err := s.agentNameDialogue(context.Background(), "server", "s1")
	if err != nil || !utf8.ValidString(first) || !utf8.ValidString(recent) || utf8.RuneCountInString(first) > agentNamingFirstRunes || utf8.RuneCountInString(recent) > agentNamingRecentRunes {
		t.Fatalf("bounds: %v first=%d recent=%d", err, utf8.RuneCountInString(first), utf8.RuneCountInString(recent))
	}
	golden(t, "agent_session_name.system", prompts.RenderAgentSessionName())
	golden(t, "agent_session_name.input", prompts.RenderAgentSessionNameInput(prompts.AgentSessionNameInput{First: "Fix image export.", Recent: "user: Keep the image in the exported file.\nassistant: Checking the export path."}))
}
