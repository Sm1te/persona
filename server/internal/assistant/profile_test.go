package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// The settings are the assistant's: hub.json's (the hello) are their defaults, a saved machine the hub no longer has
// is not used, and the hub gets what its features need in every snapshot (the profile, with the conversations its
// nodes keep listing).
func TestSettingsAndTheProfile(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	f.hello.Assistant.Launcher, f.hello.OwnerNotes = "hub-l", "  hub.json 的备注 "
	s := newTestService(t, f)
	if v := s.settings(ctx); v.MachineID != "server" || v.Launcher != "hub-l" || s.notes(ctx) != "hub.json 的备注" || !s.mainTasksQuiet(ctx) {
		t.Fatalf("defaults: %+v %q", v, s.notes(ctx))
	}
	notes := "我自己写的"
	setSettings(t, s, func(v *Settings) {
		v.MachineID, v.Main, v.Notes, v.Timezone = "gone", "main-1", &notes, "Asia/Shanghai"
		v.Former = []FormerMain{{MachineID: "box", SessionID: "old-1"}}
	})
	if v := s.settings(ctx); v.MachineID != "server" || s.notes(ctx) != notes || s.location(ctx).String() != "Asia/Shanghai" {
		t.Fatalf("saved: %+v", v)
	}
	s.publish(ctx)
	p := f.lastSnapshot().Profile
	want := []SessionRef{{MachineID: "server", SessionID: "main-1"}, {MachineID: "box", SessionID: "old-1"}}
	if p == nil || p.MachineID != "server" || p.Main != "main-1" || p.Launcher != "hub-l" || p.Timezone != "Asia/Shanghai" ||
		!slices.Equal(p.Keep, want) {
		t.Fatalf("profile: %+v", p)
	}
	// The main conversation is "Persona" on the lists the apps get.
	if i := slices.IndexFunc(f.lastSnapshot().Sessions, func(v SessionView) bool { return v.SessionID == "main-1" }); i < 0 ||
		f.lastSnapshot().Sessions[i].Title != mainTitle {
		t.Fatalf("sessions: %+v", f.lastSnapshot().Sessions)
	}
}

// PUT /api/v1/assistant checks what it is given (a machine the hub has, a time zone, a launcher that runs Claude
// Code, Codex or an assistant runtime), puts a launcher into Model configuration › Main chat too, and answers the view.
func TestPutSettings(t *testing.T) {
	f := newFakeHub().withMachine()
	f.devices = []Device{{ID: "server", Name: "server", Kind: "local", Home: "/home/me"}}
	f.hello.Assistant.Machines = []string{"server"}
	s := newTestService(t, f)
	put := func(body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/assistant", strings.NewReader(body)))
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	for _, bad := range []string{`{"machine_id":"nowhere"}`, `{"timezone":"Mars/Base"}`, `{"launcher":"gemini-l"}`} {
		if code, out := put(bad); code != http.StatusBadRequest {
			t.Fatalf("%s: %d %v", bad, code, out)
		}
	}
	code, out := put(`{"launcher":"codex-l","timezone":"Asia/Shanghai","main":"m1","main_tasks_quiet":false,"notes":"关于我"}`)
	if code != http.StatusOK || out["launcher"] != "codex-l" || out["timezone"] != "Asia/Shanghai" || out["main"] != "m1" ||
		out["main_tasks_quiet"] != false || out["notes_effective"] != "关于我" || out["cwd"] != "/home/me/.persona/assistant" {
		t.Fatalf("PUT: %d %v", code, out)
	}
	if r := modelRecordOf(t, s, "assistant"); r.Binding.Provider != "launcher.codex-l" {
		t.Fatalf("模型配置 › 主对话: %+v", r)
	}
	if p := f.lastSnapshot().Profile; p == nil || p.Main != "m1" || p.Timezone != "Asia/Shanghai" {
		t.Fatalf("the hub is told: %+v", p)
	}
	if code, out := put(`{}`); code != http.StatusOK || out["main_background_folded"] != true {
		t.Fatalf("main_background_folded default: %d %v", code, out["main_background_folded"])
	}
	if code, out := put(`{"main_background_folded":false}`); code != http.StatusOK || out["main_background_folded"] != false {
		t.Fatalf("main_background_folded off: %d %v", code, out["main_background_folded"])
	}
	if code, out := put(`{"main":"-"}`); code != http.StatusOK || out["main"] != nil {
		t.Fatalf("clear main: %d %v", code, out)
	}
}

// A zone picked by hand stays (#1855): before any pick every write lands, as before; once a zone is picked, a
// device's own sync (a zone without timezone_picked, as every app sent before) leaves it and the answer still says
// it, while a new pick or going back to the devices' zone (false) lands; the hub's own reading follows.
func TestPickedTimezoneStays(t *testing.T) {
	f := newFakeHub().withMachine()
	f.devices = []Device{{ID: "server", Name: "server", Kind: "local", Home: "/home/me"}}
	f.hello.Assistant.Machines = []string{"server"}
	s := newTestService(t, f)
	put := func(body string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/assistant", strings.NewReader(body)))
		out := map[string]any{}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
		return out
	}
	steps := []struct {
		body, zone string
		picked     any // the answer's timezone_picked (nil: not there)
	}{
		{`{"timezone":"America/New_York"}`, "America/New_York", nil},                     // never picked: a device's sync lands
		{`{"timezone":"Europe/Berlin","timezone_picked":true}`, "Europe/Berlin", true},   // picked on Android
		{`{"timezone":"America/New_York"}`, "Europe/Berlin", true},                       // an iPhone's sync: kept
		{`{"notes":"关于我"}`, "Europe/Berlin", true},                                       // another setting: kept
		{`{"timezone":"Asia/Tokyo","timezone_picked":true}`, "Asia/Tokyo", true},         // another pick lands
		{`{"timezone_picked":true}`, "Asia/Tokyo", true},                                 // a pick without a zone keeps it
		{`{"timezone":"Asia/Shanghai","timezone_picked":false}`, "Asia/Shanghai", false}, // Match phone again
		{`{"timezone":"America/New_York"}`, "America/New_York", false},                   // the devices' syncs land again
	}
	for _, st := range steps {
		out := put(st.body)
		if out["timezone"] != st.zone || out["timezone_picked"] != st.picked {
			t.Fatalf("%s: timezone %v, timezone_picked %v; want %s, %v", st.body, out["timezone"], out["timezone_picked"], st.zone, st.picked)
		}
		if got := s.settings(context.Background()).Timezone; got != st.zone {
			t.Fatalf("%s: the hub reads %s", st.body, got)
		}
	}
}

// Read-only devices (an answer-only watch) may view the assistant but cannot change its settings or write/delete
// memory: the three mutating routes answer 403 before touching anything. No role (a test's own request) and "full"
// keep working, and GET stays open.
func TestSettingsAndMemoryRequireFullRole(t *testing.T) {
	f := newFakeHub().withMachine()
	f.devices = []Device{{ID: "server", Name: "server", Kind: "local", Home: "/home/me"}}
	f.hello.Assistant.Machines = []string{"server"}
	s := newTestService(t, f)
	do := func(method, path, role, body string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if role != "" {
			r.Header.Set(RoleHeader, role)
		}
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPut, "/api/v1/assistant", `{}`},
		{http.MethodPut, "/api/v1/assistant/memory/note-1", `{"content":"x"}`},
		{http.MethodDelete, "/api/v1/assistant/memory/note-1", ``},
	} {
		// Read-only devices and extensions are stopped by the role; full devices go through (the fake services all
		// three, so they answer 200 rather than failing somewhere past the gate).
		for _, role := range []string{"answer", roleExtension} {
			if code := do(tc.method, tc.path, role, tc.body); code != http.StatusForbidden {
				t.Fatalf("%s %s as %s: %d, want 403", tc.method, tc.path, role, code)
			}
		}
		if code := do(tc.method, tc.path, "full", tc.body); code != http.StatusOK {
			t.Fatalf("%s %s as full: %d, want 200", tc.method, tc.path, code)
		}
	}
	// No role header is a full-role request; the empty PUT is a valid no-op settings change.
	if code := do(http.MethodPut, "/api/v1/assistant", "", `{}`); code != http.StatusOK {
		t.Fatalf("PUT without a role header: %d, want 200", code)
	}
	// Reading the settings and memory stays open to a read-only device.
	for _, path := range []string{"/api/v1/assistant", "/api/v1/assistant/memory"} {
		if code := do(http.MethodGet, path, "answer", ""); code != http.StatusOK {
			t.Fatalf("GET %s as answer: %d, want 200", path, code)
		}
	}
}

// A transient settings read failure keeps serving the settings read last instead of the defaults: one locked-database
// round must not silently turn a 365-day recording retention into 14 days and let the next KB prune delete recordings.
// Only settings never successfully read fall back to the default.
func TestRecordingDaysKeepsLastGoodOnReadFailure(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	st := &memStore{}
	s := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)}))
	expire := func() {
		s.prefs.mu.Lock()
		s.prefs.at = time.Now().Add(-settingsFresh - time.Second)
		s.prefs.mu.Unlock()
	}
	days := func(n int) *int { return &n }

	// Never successfully read, and the read fails: there is no good value to keep.
	expire()
	st.failSetting = errors.New("database locked")
	if _, ok := s.settingsLastGood(ctx); ok {
		t.Fatal("never read + failure: want !ok")
	}
	if d := s.recordingDaysNow(); d != kb.DefaultRecordingDays {
		t.Fatalf("never read + failure: days=%d, want %d", d, kb.DefaultRecordingDays)
	}

	// The user picked 365; a failure past the 5s freshness keeps serving 365 instead of falling back to 14.
	st.failSetting = nil
	setSettings(t, s, func(v *Settings) { v.RecordingDays = days(365) })
	expire()
	st.failSetting = errors.New("database locked")
	if v, ok := s.settingsLastGood(ctx); !ok || v.RecordingDays == nil || *v.RecordingDays != 365 {
		t.Fatalf("failure with last-good: ok=%v days=%v", ok, v.RecordingDays)
	}
	if d := s.recordingDaysNow(); d != 365 {
		t.Fatalf("failure with last-good: days=%d, want 365", d)
	}

	// Plain savedSettings still reports the failure (kbZone must not move the archive on a value it could not re-read).
	if _, ok := s.savedSettings(ctx); ok {
		t.Fatal("savedSettings still reports a failed read")
	}

	// A successful refresh still picks up a value another hub wrote (the 5s freshness behaviour is unchanged).
	st.failSetting = nil
	if err := st.PutSetting(ctx, settingsKey, Settings{RecordingDays: days(30)}); err != nil {
		t.Fatal(err)
	}
	expire()
	if d := s.recordingDaysNow(); d != 30 {
		t.Fatalf("successful refresh: days=%d, want 30", d)
	}

	// Failing again serves the new last-good value (30); once the database recovers the read works again.
	expire()
	st.failSetting = errors.New("database busy")
	if d := s.recordingDaysNow(); d != 30 {
		t.Fatalf("failure after refresh: days=%d, want 30", d)
	}
	st.failSetting = nil
	expire()
	if d := s.recordingDaysNow(); d != 30 {
		t.Fatalf("recovered: days=%d, want 30", d)
	}
}

// A message for one of its conversations: one the machine does not list (and not the main one) keeps what it runs
// on; the main one runs on the main chat's, a gone launcher falls back to the assistant's, a dropped model to the launcher's
// default; the first message to a new main conversation started empty carries what the old one left, once.
func TestSendParams(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	f.setSessions("server", proto.Session{SessionID: "side", Launcher: "gone-l", Model: "m", Assistant: true},
		proto.Session{SessionID: "side2", Launcher: "side-l", Model: "dropped", Assistant: true})
	// gone-l is no launcher of the machine's any more; side-l does not offer "dropped" now.
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "conv"}, {Name: "side-l"}}
	f.agentModels = map[string][]proto.AgentModel{"side-l": {{ID: "m"}}}
	s := newTestService(t, f)
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.conv"})
	if p, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: "unlisted", Params: map[string]any{"text": "hi"}}); err != nil ||
		len(p) != 2 || p["is_main"] != false {
		t.Fatalf("unlisted: %v %v", p, err)
	}
	p, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: "side", Params: map[string]any{"text": "hi"}})
	if err != nil || p["launcher"] != "conv" || p["model"] != nil || p["model_fingerprint"] == nil { // the main chat's
		t.Fatalf("a gone launcher: %v %v", p, err)
	}
	p, err = s.SendParams(ctx, SendParams{MachineID: "server", SessionID: "side2", Params: map[string]any{"text": "hi"}})
	if err != nil || p["launcher"] != "side-l" || p["model"] != nil {
		t.Fatalf("a dropped model: %v %v", p, err)
	}

	// The main conversation put away, the next one started empty: its first message carries the old one's summary.
	setSettings(t, s, func(v *Settings) { v.Main = "main-0" })
	f.setSessions("server", proto.Session{SessionID: "main-0", Assistant: true, Status: proto.StatusIdle})
	if old, carried, err := s.newMain(ctx, false, "main-0"); err != nil || old != "main-0" || carried {
		t.Fatalf("new main: %q %v %v", old, carried, err)
	}
	sess, err := s.start(ctx, StartParams{Main: true})
	if err != nil || s.settings(ctx).Main != sess.SessionID || !slices.Contains(s.settings(ctx).Former, FormerMain{MachineID: "server", SessionID: "main-0",
		Until: s.settings(ctx).Former[0].Until}) {
		t.Fatalf("started: %+v %v %+v", sess, err, s.settings(ctx))
	}
	first, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: sess.SessionID, Params: map[string]any{"text": "你好"}})
	if text, _ := first["text"].(string); err != nil || !strings.Contains(text, "main-0") || !strings.HasSuffix(text, "你好") || first["launcher"] != "conv" {
		t.Fatalf("the first message: %v %v", first, err)
	}
	if again, _ := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: sess.SessionID, Params: map[string]any{"text": "再来"}}); again["text"] != "再来" {
		t.Fatalf("taken once: %v", again)
	}
}

// Renaming one of its conversations: not the main one (400), a Codex one only in the assistant's titles.
func TestRename(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	f.setSessions("server", proto.Session{SessionID: "cx", Agent: "codex", Assistant: true}, proto.Session{SessionID: "cl", Assistant: true})
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.Main = "main-1" })
	if _, err := s.Rename(ctx, RenameParams{MachineID: "server", SessionID: "main-1", Params: map[string]any{"title": "x"}}); err == nil ||
		!strings.Contains(err.Error(), "主对话") {
		t.Fatalf("main: %v", err)
	}
	if _, err := s.Rename(ctx, RenameParams{MachineID: "server", SessionID: "cx", Params: map[string]any{"title": "codex 的"}}); err != nil || f.sent() != 0 {
		t.Fatalf("codex: %v, %d node calls", err, f.sent())
	}
	if _, err := s.Rename(ctx, RenameParams{MachineID: "server", SessionID: "cl", Params: map[string]any{"title": "claude 的"}}); err != nil || f.sent() != 1 {
		t.Fatalf("claude: %v, %d node calls", err, f.sent())
	}
	s.publish(ctx)
	titles := map[string]string{}
	for _, v := range f.lastSnapshot().Sessions {
		titles[v.SessionID] = v.Title
	}
	if titles["cx"] != "codex 的" || titles["cl"] != "claude 的" {
		t.Fatalf("titles: %v", titles)
	}
}

// persona_settings: the list has the assistant's settings and the hub's; the assistant's own are read, and changed
// once the user approves on the phone (its own card); the hub's go to the hub.
func TestSettingsTool(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast", Model: "m-fast"}}
	s := newTestService(t, f)
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.claude", Model: "chosen", Effort: "high"})
	tool := func(args string) string {
		r := s.Tool(ctx, ToolCall{Name: "persona_settings", Arguments: json.RawMessage(args), MachineID: "server", SessionID: "c1"})
		return r.Content[0].Text
	}
	if out := tool(`{}`); !strings.Contains(out, "- assistant.launcher (Persona 的启动器)") || !strings.Contains(out, "- inbox.rules (收件箱分拣规则)") ||
		strings.Index(out, "assistant.notes") > strings.Index(out, "inbox.rules") {
		t.Fatalf("list:\n%s", out)
	}
	if out := tool(`{"action":"get","key":"inbox.rules"}`); !strings.HasPrefix(out, "the hub's: ") {
		t.Fatalf("a hub setting: %s", out)
	}
	answer := func(allow bool) {
		q, ok := awaitCard(s)
		if !ok {
			t.Error("no card on the phone")
			return
		}
		raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
		if q.Kind != proto.IxPermission || q.ToolName != "persona_settings" || q.SessionID != "c1" {
			t.Errorf("card: %+v", q)
		}
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
			t.Error(err)
		}
	}
	go answer(false)
	if out := tool(`{"action":"set","key":"assistant.notes","value":"示例备注"}`); !strings.Contains(out, "declined") || s.notes(ctx) != "" {
		t.Fatalf("declined: %s", out)
	}
	go answer(true)
	if out := tool(`{"action":"set","key":"assistant.notes","value":"示例备注"}`); !strings.Contains(out, "updated") || s.notes(ctx) != "示例备注" {
		t.Fatalf("approved: %s", out)
	}
	if saved, _ := s.savedSettings(ctx); saved.Launcher != "" {
		t.Fatal("notes changed legacy model inheritance")
	}
	go answer(true)
	if out := tool(`{"action":"set","key":"assistant.launcher","value":"fast"}`); !strings.Contains(out, "fast") ||
		modelRecordOf(t, s, "assistant").Binding != (ModelBinding{Provider: "launcher.fast"}) {
		t.Fatalf("launcher: %s %+v", out, modelRecordOf(t, s, "assistant"))
	}
	if out := tool(`{"action":"set","key":"assistant.launcher","value":"nope"}`); !strings.Contains(out, "no launcher") {
		t.Fatalf("unknown launcher: %s", out)
	}
	if out := tool(`{"action":"get","key":"assistant.main_tasks_quiet"}`); out != "true" {
		t.Fatalf("quiet: %s", out)
	}
}

// persona_settings' three on/off settings (setBoolSetting): the same value changes nothing and asks nothing; another
// asks on the phone with the setting's own card, and only an approval saves it; a value that is not a bool is refused.
func TestBoolSettingsSet(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t, newFakeHub().withMachine())
	tool := func(args string) (string, bool) {
		r := s.Tool(ctx, ToolCall{Name: "persona_settings", Arguments: json.RawMessage(args), MachineID: "server", SessionID: "c1"})
		return r.Content[0].Text, r.IsError
	}
	word := map[bool]string{true: i18n.T(i18n.Default, "assistant-settings_tool.on"), false: i18n.T(i18n.Default, "assistant-settings_tool.off")}
	// answer approves (or not) the one card, which must say card.
	answer := func(allow bool, card string) {
		q, ok := awaitCard(s)
		if !ok {
			t.Error("no card on the phone")
			return
		}
		if q.Summary != card {
			t.Errorf("card %q, want %q", q.Summary, card)
		}
		raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
			t.Error(err)
		}
	}
	for _, c := range []struct {
		name string
		def  bool
	}{{"main_tasks_quiet", true}, {"start_requested_tasks", true}, {"done_needs_delivery", false}, {"interject_background", false}, {"agent_tasks", false}, {"main_background_folded", true}, {"inbox_signals", false}} {
		key := "assistant." + c.name
		get := func() string { out, _ := tool(`{"action":"get","key":"` + key + `"}`); return out }
		set := func(value string) (string, bool) {
			return tool(`{"action":"set","key":"` + key + `","value":` + value + `}`)
		}
		if out := get(); out != fmt.Sprint(c.def) {
			t.Fatalf("%s default: %s", key, out)
		}
		if out, isErr := set(fmt.Sprint(c.def)); isErr || !(strings.Contains(out, "没有变化") || strings.Contains(out, "is unchanged")) || len(s.pendingQuestions()) != 0 {
			t.Fatalf("%s set to what it is: %s", key, out)
		}
		if out, isErr := set(`"yes"`); !isErr || !strings.Contains(out, "true or false") {
			t.Fatalf("%s set to a string: %s", key, out)
		}
		card := i18n.T(i18n.Default, "assistant-settings_tool.change_"+c.name, i18n.Args{"before": word[c.def], "after": word[!c.def]})
		go answer(false, card)
		if out, _ := set(fmt.Sprint(!c.def)); !strings.Contains(out, "declined") || get() != fmt.Sprint(c.def) {
			t.Fatalf("%s declined: %s", key, out)
		}
		go answer(true, card)
		if out, _ := set(fmt.Sprint(!c.def)); !strings.HasPrefix(out, key+" is "+fmt.Sprint(!c.def)) || get() != fmt.Sprint(!c.def) {
			t.Fatalf("%s approved: %s", key, out)
		}
	}
}

// The agents of open todos are kept listed however long they have been quiet (#4510): a busy machine's newest
// transcripts crowded a finished, exited task agent out of the list and its task showed "Starting Agent" for ever. A
// done todo's or a released agent is not kept.
func TestProfileKeepsOpenTodoAgents(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.Main = "main-1" })
	f.setSessions("box", proto.Session{SessionID: "a1", Status: proto.StatusIdle}, proto.Session{SessionID: "a2", Status: proto.StatusIdle},
		proto.Session{SessionID: "a3", Status: proto.StatusIdle})
	e := &todoEnv{t: t, s: s, f: f}
	open, done, released := e.create("Open"), e.create("Done"), e.create("Released")
	e.must("PUT", "/api/v1/todos/"+open.ID+"/agents/box/a1", "")
	e.must("PUT", "/api/v1/todos/"+done.ID+"/agents/box/a2", "")
	e.must("PUT", "/api/v1/todos/"+released.ID+"/agents/box/a3", "")
	e.must("PATCH", "/api/v1/todos/"+done.ID, `{"done":true}`)
	e.must("DELETE", "/api/v1/todos/"+released.ID+"/agents/box/a3", "")
	s.publish(ctx)
	p := f.lastSnapshot().Profile
	want := []SessionRef{{MachineID: "server", SessionID: "main-1"}, {MachineID: "box", SessionID: "a1"}}
	if p == nil || !slices.Equal(p.Keep, want) {
		t.Fatalf("keep: %+v", p)
	}
}
