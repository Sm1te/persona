package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
)

// startConv is an app's POST /api/v1/assistant/conversations in English: its status, the new session, and its pick.
func startConv(t *testing.T, s *Service, body string) (int, proto.Session) {
	t.Helper()
	r := appRequest(t, s, "POST", "/api/v1/assistant/conversations", body, "en")
	var out struct {
		Session proto.Session `json:"session"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if r.Code != 200 {
		t.Logf("start: %d %s", r.Code, r.Body.String())
	}
	return r.Code, out.Session
}

// A start that names only an effort (iOS sends its last effort with every start) keeps no pick: the conversation, a
// new main one too, follows Model configuration as before.
func TestStartWithOnlyAnEffortKeepsNoPick(t *testing.T) {
	for _, body := range []string{`{"prompt":"hi","effort":"high"}`, `{"prompt":"hi","main":true,"effort":"high"}`} {
		s, _ := pickFixture(t)
		code, sess := startConv(t, s, body)
		if code != 200 || sess.ModelPick != nil {
			t.Fatalf("%s: %d %+v", body, code, sess.ModelPick)
		}
		if picks, _ := s.modelPicksSet(); len(picks) != 0 {
			t.Fatalf("%s: kept %v", body, picks)
		}
	}
}

// A pick with no model runs what a start with that launcher runs: Model configuration › Main chat's model when the
// launcher is the one configured there (following later changes), the launcher's own default otherwise.
func TestAPickWithoutAModelRunsLikeTheStart(t *testing.T) {
	s, f := pickFixture(t)
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.gw", Model: "cfg-1"})
	code, sess := startConv(t, s, `{"prompt":"hi","launcher":"gw"}`)
	if code != 200 || sess.ModelPick == nil || f.lastSend(t)["model"] != "cfg-1" {
		t.Fatalf("start: %d %+v %v", code, sess.ModelPick, f.lastSend(t))
	}
	f.setSessions("server", proto.Session{SessionID: "main", Assistant: true}, proto.Session{SessionID: sess.SessionID, Assistant: true, Launcher: "gw"})
	if p, err := send(t, s, sess.SessionID, map[string]any{}); err != nil || p["launcher"] != "gw" || p["model"] != "cfg-1" {
		t.Fatalf("turn 2: %v %v", p, err)
	}
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.gw", Model: "cfg-2"})
	if p, err := send(t, s, sess.SessionID, map[string]any{}); err != nil || p["model"] != "cfg-2" {
		t.Fatalf("after Model configuration changed: %v %v", p, err)
	}
	// Another launcher than the configured one: its own default, as its start ran.
	if r := putPick(t, s, sess.SessionID, `{"launcher":"claude"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if p, err := send(t, s, sess.SessionID, map[string]any{}); err != nil || p["launcher"] != "claude" || p["model"] != nil {
		t.Fatalf("another launcher: %v %v", p, err)
	}
}

// modesDevices answers the hub's devices.call as the devices would once the node took it.
type modesDevices struct{ fakeDevices }

func (d *modesDevices) Request(_ context.Context, method string, _ json.RawMessage) (any, bool, error) {
	if method == devicewire.MethodCall {
		return json.RawMessage(`{}`), true, nil
	}
	return nil, false, nil
}

// An effort set on a picked conversation through POST …/modes (the apps' effort chip) becomes the pick's, so the next
// message runs on it instead of putting the pick's back.
func TestModesEffortBecomesThePicks(t *testing.T) {
	s, _ := pickFixture(t)
	if r := putPick(t, s, "side", `{"launcher":"claude","model":"opus","effort":"high"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	b := &BusHub{svc: s, helloed: make(chan struct{}), opts: BusOptions{Devices: &modesDevices{}}}
	b.helloOnce.Do(func() { close(b.helloed) })
	call := func(sid, method string, params map[string]any) {
		t.Helper()
		params["session_id"] = sid
		raw, _ := json.Marshal(params)
		req, _ := json.Marshal(devicewire.NodeCall{MachineID: "server", Method: method, Params: raw})
		if _, err := b.request(context.Background(), devicewire.MethodCall, req); err != nil {
			t.Fatal(err)
		}
	}
	call("side", "session.modes", map[string]any{"permission_mode": "plan"}) // no effort: the pick's stays
	if p, _ := s.modelPickOf(context.Background(), "server", "side"); p.Effort != "high" {
		t.Fatalf("a mode alone changed the effort: %+v", p)
	}
	call("side", "session.modes", map[string]any{"effort": "low"})
	if p, _ := s.modelPickOf(context.Background(), "server", "side"); p.Effort != "low" || p.Launcher != "claude" || p.Model != "opus" {
		t.Fatalf("pick: %+v", p)
	}
	if p, err := send(t, s, "side", map[string]any{}); err != nil || p["effort"] != "low" {
		t.Fatalf("next message: %v %v", p, err)
	}
	call("main", "session.modes", map[string]any{"effort": "max"}) // no pick there: none is made
	if _, ok := s.modelPickOf(context.Background(), "server", "main"); ok {
		t.Fatal("a pick was made for a conversation without one")
	}
}

// Past maxModelPicks the oldest go, but never the main conversation's, however old.
func TestModelPicksKeepTheMainConversations(t *testing.T) {
	s, _ := pickFixture(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	if err := s.setModelPick(ctx, "main", &modelPick{MachineID: "server", Launcher: "cx", At: start}); err != nil {
		t.Fatal(err)
	}
	for i := range maxModelPicks + 1 {
		if err := s.setModelPick(ctx, "c"+strconv.Itoa(i), &modelPick{MachineID: "server", Launcher: "claude", At: start.Add(time.Duration(i+1) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	picks, _ := s.modelPicksSet()
	if _, ok := picks["main"]; !ok || len(picks) != maxModelPicks {
		t.Fatalf("main kept: %v, %d picks", ok, len(picks))
	}
	if _, ok := picks["c0"]; ok {
		t.Fatal("the oldest other pick stayed")
	}
}

// /clear starts the main conversation over with the same pick; New main chat starts fresh on the default.
func TestClearCarriesTheMainPick(t *testing.T) {
	ctx := context.Background()
	s, f := pickFixture(t)
	if r := putPick(t, s, "main", `{"launcher":"cx","model":"gpt-5","effort":"high"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if _, err := s.clearMain(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// The start's effort (iOS sends its last one with every start) is the first message's; the pick keeps its own.
	code, sess := startConv(t, s, `{"prompt":"hi","main":true,"effort":"low"}`)
	if code != 200 || sess.ModelPick == nil || *sess.ModelPick != (proto.ModelPick{Launcher: "cx", Model: "gpt-5", Effort: "high"}) {
		t.Fatalf("after /clear: %d %+v", code, sess.ModelPick)
	}
	if p := f.lastSend(t); p["launcher"] != "cx" || p["model"] != "gpt-5" || p["effort"] != "low" {
		t.Fatalf("first message after /clear: %v", p)
	}
	// Carried once: the next new main conversation does not get it again.
	if _, _, err := s.newMain(ctx, false, ""); err != nil {
		t.Fatal(err)
	}
	code, sess = startConv(t, s, `{"prompt":"hi","main":true}`)
	if code != 200 || sess.ModelPick != nil || f.lastSend(t)["launcher"] != "gw" {
		t.Fatalf("New main chat: %d %+v %v", code, sess.ModelPick, f.lastSend(t))
	}
}

// Warming with no launcher gets the main conversation's pick ready where the main conversation runs.
func TestWarmFindsTheMainPickOnItsMachine(t *testing.T) {
	s, f := pickFixture(t)
	f.sessions["other"] = nil
	setSettings(t, s, func(v *Settings) { v.MainMachineID = "other" })
	if err := s.setModelPick(context.Background(), "main", &modelPick{MachineID: "other", Launcher: "cx", Model: "gpt-5", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.warm("")
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == DeviceAssistantWarm {
			var p map[string]any
			_ = json.Unmarshal(f.calls[i].Params, &p)
			if f.calls[i].Device != "other" || p["launcher"] != "cx" || p["model"] != "gpt-5" {
				t.Fatalf("warmed %s with %v", f.calls[i].Device, p)
			}
			return
		}
	}
	t.Fatal("nothing warmed")
}

// The refusals that reach people are in their language.
func TestModelPickErrorsAreTranslated(t *testing.T) {
	s, _ := pickFixture(t)
	if r := appRequest(t, s, "PUT", "/api/v1/assistant/conversations/bad%20id/model", `{"launcher":"claude"}`, "zh-Hans"); r.Code != 400 || !strings.Contains(r.Body.String(), "对话") {
		t.Fatalf("bad id: %d %s", r.Code, r.Body.String())
	}
	if r := appRequest(t, s, "DELETE", "/api/v1/assistant/conversations/bad%20id/model", "", "zh-Hans"); r.Code != 400 || !strings.Contains(r.Body.String(), "对话") {
		t.Fatalf("bad id (DELETE): %d %s", r.Code, r.Body.String())
	}
	st := s.store().(*memStore)
	st.failKey(modelPicksKey, errors.New("database down"))
	s.modelPicks = modelPickBook{}
	if r := appRequest(t, s, "PUT", "/api/v1/assistant/conversations/side/model", `{"launcher":"claude"}`, "zh-Hans"); r.Code != http.StatusInternalServerError || !strings.Contains(r.Body.String(), "模型") {
		t.Fatalf("unreadable book: %d %s", r.Code, r.Body.String())
	}
}

// gatedStore holds writes of the picks until open is closed, telling entered when one comes.
type gatedStore struct {
	*memStore
	entered, open chan struct{}
}

func (g *gatedStore) PutSetting(ctx context.Context, key string, v any) error {
	if key == modelPicksKey {
		g.entered <- struct{}{}
		<-g.open
	}
	return g.memStore.PutSetting(ctx, key, v)
}

// Reading the picks (every snapshot, every message) does not wait on a pick being written to the database.
func TestPicksReadWhileOneIsWritten(t *testing.T) {
	f := newFakeHub().withMachine()
	st := &gatedStore{memStore: &memStore{}, entered: make(chan struct{}, 1), open: make(chan struct{})}
	s := newServiceOn(t, f, st)
	if _, ok := s.modelPicksSet(); !ok {
		t.Fatal("book unreadable")
	}
	done := make(chan error, 1)
	go func() {
		done <- s.setModelPick(context.Background(), "side", &modelPick{MachineID: "server", Launcher: "claude", At: time.Now()})
	}()
	<-st.entered
	read := make(chan struct{})
	go func() { s.modelPicksSet(); s.modelPickOf(context.Background(), "server", "side"); close(read) }()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		close(st.open)
		t.Fatal("reading the picks waited on the database write")
	}
	close(st.open)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if p, ok := s.modelPickOf(context.Background(), "server", "side"); !ok || p.Launcher != "claude" {
		t.Fatalf("after the write: %+v %v", p, ok)
	}
}

// A cleared main conversation's pick that can't run any more refuses the next main start once, saying why with the
// codes a message on it gets, and is dropped then: the start after runs on the default. Never a silent fallback.
func TestACarriedPickThatCantRunIsRefusedOnce(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		launcher, code, words string
		status                int
	}{
		{"cc-relay", "", "can’t run assistant chats", 400},
		{"far", proto.CodeLauncherOutOfScope, "far stays with the hub", 409},
		{"gone", proto.CodeLauncherMissing, "gone", 409},
	} {
		t.Run(c.launcher, func(t *testing.T) {
			s, f := pickFixture(t)
			if err := s.setModelPick(ctx, "main", &modelPick{MachineID: "server", Launcher: c.launcher, At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.clearMain(ctx, ""); err != nil {
				t.Fatal(err)
			}
			r := appRequest(t, s, "POST", "/api/v1/assistant/conversations", `{"prompt":"hi","main":true}`, "en")
			code, words := errorBody(t, r)
			if r.Code != c.status || c.code != "" && code != c.code || !strings.Contains(words+r.Body.String(), c.words) {
				t.Fatalf("start after /clear: %d %s", r.Code, r.Body.String())
			}
			if len(f.sends) != 0 {
				t.Fatalf("it ran: %v", f.sends)
			}
			code2, sess := startConv(t, s, `{"prompt":"hi","main":true}`)
			if code2 != 200 || sess.ModelPick != nil || f.lastSend(t)["launcher"] != "gw" {
				t.Fatalf("the start after: %d %+v %v", code2, sess.ModelPick, f.lastSend(t))
			}
		})
	}
}

// New main chat with no main conversation (after /clear) drops the carried pick: the next one starts on the default.
func TestNewMainChatAfterClearDropsTheCarriedPick(t *testing.T) {
	ctx := context.Background()
	s, f := pickFixture(t)
	if r := putPick(t, s, "main", `{"launcher":"cx"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if _, err := s.clearMain(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.newMain(ctx, false, ""); err != nil {
		t.Fatal(err)
	}
	if code, sess := startConv(t, s, `{"prompt":"hi","main":true}`); code != 200 || sess.ModelPick != nil || f.lastSend(t)["launcher"] != "gw" {
		t.Fatalf("%d %+v %v", code, sess.ModelPick, f.lastSend(t))
	}
}

// /clear twice in a row (the second with no main conversation) keeps the pick waiting for the next one.
func TestClearTwiceKeepsTheCarriedPick(t *testing.T) {
	ctx := context.Background()
	s, f := pickFixture(t)
	if r := putPick(t, s, "main", `{"launcher":"cx","model":"gpt-5"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	for range 2 {
		if _, err := s.clearMain(ctx, ""); err != nil {
			t.Fatal(err)
		}
	}
	code, sess := startConv(t, s, `{"prompt":"hi","main":true}`)
	if code != 200 || sess.ModelPick == nil || sess.ModelPick.Launcher != "cx" || f.lastSend(t)["model"] != "gpt-5" {
		t.Fatalf("%d %+v %v", code, sess.ModelPick, f.lastSend(t))
	}
}

// Warming after /clear (no main conversation yet) gets the carried pick ready, which the next one starts on.
func TestWarmAfterClearTakesTheCarriedPick(t *testing.T) {
	ctx := context.Background()
	s, f := pickFixture(t)
	if r := putPick(t, s, "main", `{"launcher":"cx","model":"gpt-5"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if _, err := s.clearMain(ctx, ""); err != nil {
		t.Fatal(err)
	}
	s.warm("")
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == DeviceAssistantWarm {
			var p map[string]any
			_ = json.Unmarshal(f.calls[i].Params, &p)
			if f.calls[i].Device != "server" || p["launcher"] != "cx" || p["model"] != "gpt-5" {
				t.Fatalf("warmed %s with %v", f.calls[i].Device, p)
			}
			return
		}
	}
	t.Fatal("nothing warmed")
}

// writeWatch is a hub that, as each snapshot is published, records whether a pick change still held the book's write
// lock (a publish under it holds up every other change, and anything the publish waits on).
type writeWatch struct {
	*fakeHub
	s    *Service
	held []bool
}

func (h *writeWatch) Snapshot(ctx context.Context, snap Snapshot) (SnapshotResult, error) {
	if h.s != nil {
		free := h.s.modelPicks.write.TryLock()
		if free {
			h.s.modelPicks.write.Unlock()
		}
		h.fakeHub.mu.Lock()
		h.held = append(h.held, !free)
		h.fakeHub.mu.Unlock()
	}
	return h.fakeHub.Snapshot(ctx, snap)
}

// A pick change tells the apps after it let go of the book.
func TestModelPickPublishesOutsideTheWriteLock(t *testing.T) {
	f := newFakeHub().withMachine()
	h := &writeWatch{fakeHub: f}
	st := &memStore{}
	s := withSettings(t, f, New(Options{Hub: h, Store: func() Store { return st }, Logf: testLogf(t)}))
	h.s = s
	if err := s.setModelPick(context.Background(), "side", &modelPick{MachineID: "server", Launcher: "claude", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(h.held) == 0 {
		t.Fatal("nothing published")
	}
	for _, held := range h.held {
		if held {
			t.Fatal("published while still holding the book's write lock")
		}
	}
}
