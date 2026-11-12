package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// pickFixture is mainChatFixture with the main conversation "main" and a side conversation "side" (on cx) on "server",
// Model configuration › Main chat on gw, and two more providers: "far", kept off every computer, and "own", which
// skips computers with their own Claude Code sign-in (as "server" has).
func pickFixture(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	s, f := mainChatFixture(t)
	putLaunchers(t, s,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", BaseURL: "https://gw.example", Model: "m1"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "codex-gw", Agent: "codex", BaseURL: "https://gw.example/v1", Model: "gpt"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "far", BaseURL: "https://far.example", Model: "m2"}, Share: shareNone},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "own", BaseURL: "https://own.example", Model: "m3", OwnService: proto.OwnServiceSkip}})
	f.ownServices = map[string]proto.OwnServiceState{"server/claude": {Agent: "claude", Known: true, Own: true}}
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.gw"})
	setSettings(t, s, func(v *Settings) { v.Main = "main" })
	f.setSessions("server", proto.Session{SessionID: "main", Assistant: true, Launcher: "gw"},
		proto.Session{SessionID: "side", Assistant: true, Launcher: "cx", Model: "gpt-5"},
		proto.Session{SessionID: "agent", Launcher: "claude"})
	return s, f
}

// appRequest is an app's request as the hub relays it (http.request), in lang.
func appRequest(t *testing.T, s *Service, method, path, body, lang string) *httptest.ResponseRecorder {
	t.Helper()
	path, query, _ := strings.Cut(path, "?")
	res, err := serveHTTP(context.Background(), s, assistantwire.HTTPRequest{Method: method, Path: path, Query: query, Body: []byte(body),
		Header: map[string][]string{"Accept-Language": {lang}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	w.Code = res.Status
	w.Body.Write(res.Body)
	return w
}

// putPick is an app's PUT …/model in English.
func putPick(t *testing.T, s *Service, sid, body string) *httptest.ResponseRecorder {
	t.Helper()
	return appRequest(t, s, "PUT", "/api/v1/assistant/conversations/"+sid+"/model", body, "en")
}

// errorBody is an error answer's code and words.
func errorBody(t *testing.T, r *httptest.ResponseRecorder) (code, msg string) {
	t.Helper()
	var b struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil {
		t.Fatalf("%d %s: %v", r.Code, r.Body.String(), err)
	}
	return b.Code, b.Error
}

func send(t *testing.T, s *Service, sid string, params map[string]any) (map[string]any, error) {
	t.Helper()
	params["text"] = "hi"
	return s.SendParams(context.Background(), SendParams{MachineID: "server", SessionID: sid, Lang: "en", Params: params})
}

// A pick is kept with the conversation: the route answers it, the snapshot carries it to every device as model_pick,
// and DELETE goes back to following the default (twice is no error).
func TestModelPickIsKeptAndCleared(t *testing.T) {
	s, f := pickFixture(t)
	r := putPick(t, s, "main", `{"launcher":" cx ","model":"gpt-5","effort":"high"}`)
	if r.Code != 200 {
		t.Fatalf("PUT: %d %s", r.Code, r.Body.String())
	}
	var out struct {
		OK        bool             `json:"ok"`
		ModelPick *proto.ModelPick `json:"model_pick"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || !out.OK || out.ModelPick == nil ||
		*out.ModelPick != (proto.ModelPick{Launcher: "cx", Model: "gpt-5", Effort: "high"}) {
		t.Fatalf("PUT answer: %s %v", r.Body.String(), err)
	}
	view := func() *proto.ModelPick {
		for _, v := range f.lastSnapshot().Sessions {
			if v.SessionID == "main" {
				return v.ModelPick
			}
		}
		return nil
	}
	if p := view(); p == nil || p.Launcher != "cx" || p.Model != "gpt-5" || p.Effort != "high" || f.lastSnapshot().Partial {
		t.Fatalf("snapshot: %+v", p)
	}
	if raw, _ := json.Marshal(proto.Session{SessionID: "main", ModelPick: view()}); !strings.Contains(string(raw), `"model_pick":{"launcher":"cx","model":"gpt-5","effort":"high"}`) {
		t.Fatalf("conversation JSON: %s", raw)
	}
	// The book is in the store: a restarted manager has it.
	again := newServiceOn(t, f, s.store())
	if p, ok := again.modelPickOf(context.Background(), "server", "main"); !ok || p.Launcher != "cx" {
		t.Fatalf("after a restart: %+v %v", p, ok)
	}
	for range 2 {
		if r := request(s, "DELETE", "/api/v1/assistant/conversations/main/model", ""); r.Code != 200 {
			t.Fatalf("DELETE: %d %s", r.Code, r.Body.String())
		}
	}
	if p := view(); p != nil {
		t.Fatalf("still picked: %+v", p)
	}
	// A launcher alone: its own default model.
	if r := putPick(t, s, "side", `{"launcher":"claude"}`); r.Code != 200 || !strings.Contains(r.Body.String(), `"model_pick":{"launcher":"claude"}`) {
		t.Fatalf("launcher alone: %d %s", r.Code, r.Body.String())
	}
}

// A message that picks nothing runs on the conversation's pick, the main one's and a side one's alike; one that picks
// a launcher or a model (an older app) runs on that; an effort sent with the message wins over the pick's.
func TestSendRunsOnThePick(t *testing.T) {
	s, _ := pickFixture(t)
	if r := putPick(t, s, "main", `{"launcher":"cx","model":"gpt-5","effort":"high"}`); r.Code != 200 {
		t.Fatalf("PUT main: %d %s", r.Code, r.Body.String())
	}
	if r := putPick(t, s, "side", `{"launcher":"claude","model":"opus"}`); r.Code != 200 {
		t.Fatalf("PUT side: %d %s", r.Code, r.Body.String())
	}
	p, err := send(t, s, "main", map[string]any{})
	if err != nil || p["launcher"] != "cx" || p["model"] != "gpt-5" || p["effort"] != "high" || p["model_fingerprint"] == nil {
		t.Fatalf("main on its pick: %v %v", p, err)
	}
	if p, err := send(t, s, "side", map[string]any{}); err != nil || p["launcher"] != "claude" || p["model"] != "opus" || p["effort"] != nil {
		t.Fatalf("side on its pick: %v %v", p, err)
	}
	if p, err := send(t, s, "main", map[string]any{"effort": "low"}); err != nil || p["launcher"] != "cx" || p["effort"] != "low" {
		t.Fatalf("the message's effort: %v %v", p, err)
	}
	if p, err := send(t, s, "main", map[string]any{"launcher": "claude"}); err != nil || p["launcher"] != "claude" || p["model"] != nil {
		t.Fatalf("a message's own launcher: %v %v", p, err)
	}
	if p, err := send(t, s, "main", map[string]any{"model": "sonnet"}); err != nil || p["launcher"] != "gw" || p["model"] != "sonnet" {
		t.Fatalf("a message's own model (the default launcher, as before): %v %v", p, err)
	}
	if r := request(s, "DELETE", "/api/v1/assistant/conversations/main/model", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if p, err := send(t, s, "main", map[string]any{}); err != nil || p["launcher"] != "gw" || p["model"] != nil {
		t.Fatalf("back on the default: %v %v", p, err)
	}
}

// The route refuses what could never run: a launcher out of the computer's device scope or missing there (with the
// codes the apps act on), one that can't run the assistant, a model that is no id or a launcher that takes none, an
// effort no agent knows, a conversation that isn't the assistant's, a guest conversation, a read-only device.
func TestModelPickRefusals(t *testing.T) {
	s, f := pickFixture(t)
	f.modelsLocked = map[string]string{"cx": "it passes --model itself"}
	f.answerNode(assistantwire.DeviceLaunchersModels, func(_ string, params json.RawMessage) (any, error) {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &p)
		res := proto.LauncherModels{Agent: launcherAgent(p.Name), Models: []proto.AgentModel{}, Locked: f.modelsLocked[p.Name]}
		if p.Name == "agy-rt" {
			res.Agent = "agy" // an agent the node starts with no model
		}
		return res, nil
	})
	for _, c := range []struct {
		name, sid, body string
		status          int
		code, words     string
	}{
		{"out of scope", "main", `{"launcher":"far"}`, 409, proto.CodeLauncherOutOfScope, "far stays with the hub"},
		{"own sign-in", "main", `{"launcher":"own"}`, 409, proto.CodeLauncherOwnService, "own"},
		{"missing", "main", `{"launcher":"gone"}`, 409, proto.CodeLauncherMissing, "“gone”"},
		{"can't run the assistant", "main", `{"launcher":"cc-relay"}`, 400, "", "can’t run assistant chats"},
		{"managed Codex", "side", `{"launcher":"codex-gw"}`, 400, "", "can’t run assistant chats"},
		{"no launcher", "main", `{"model":"opus"}`, 400, "", "Choose a model provider"},
		{"no model id", "main", `{"launcher":"claude","model":"-rf"}`, 400, "", "-rf"},
		{"locked", "main", `{"launcher":"cx","model":"gpt-5"}`, 400, "", "sets its own model"},
		{"takes no model", "main", `{"launcher":"agy-rt","model":"m"}`, 400, "", "doesn’t take a model choice"},
		{"effort", "main", `{"launcher":"claude","effort":"turbo"}`, 400, "", "turbo"},
		{"an Agent", "agent", `{"launcher":"claude"}`, 404, "", "isn't on"},
		{"unknown", "nope", `{"launcher":"claude"}`, 404, "", "isn't on"},
		{"unknown machine", "side", `{"launcher":"claude","machine_id":"elsewhere"}`, 404, "", "unknown machine"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := putPick(t, s, c.sid, c.body)
			code, words := errorBody(t, r)
			if r.Code != c.status || c.code != "" && code != c.code || !strings.Contains(words, c.words) {
				t.Fatalf("%d %s", r.Code, r.Body.String())
			}
		})
	}
	if _, ok := s.modelPicksSet(); !ok {
		t.Fatal("book unreadable")
	}
	if picks, _ := s.modelPicksSet(); len(picks) != 0 {
		t.Fatalf("a refused pick was kept: %v", picks)
	}
	// Without the launcher list nothing is refused on a guess, and a typed model the list lacks is kept (#1644).
	f.modelsLocked = nil
	if r := putPick(t, s, "main", `{"launcher":"cx","model":"vendor/typed-1"}`); r.Code != 200 {
		t.Fatalf("typed model: %d %s", r.Code, r.Body.String())
	}

	if err := s.putSetting(context.Background(), guestsKey, map[string]*guestConv{"side": {MachineID: "server", Tools: []string{"x"}, At: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	s.guests = guestBook{}
	if r := putPick(t, s, "side", `{"launcher":"claude"}`); r.Code != 409 || !strings.Contains(r.Body.String(), "guest conversation") {
		t.Fatalf("guest: %d %s", r.Code, r.Body.String())
	}

	for _, method := range []string{"PUT", "DELETE"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/assistant/conversations/main/model", strings.NewReader(`{"launcher":"claude"}`))
		req.Header.Set(RoleHeader, "read")
		s.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("read-only %s: %d %s", method, w.Code, w.Body.String())
		}
	}
}

// A pick that can't run any more refuses the message with why (the codes the hub words and the apps act on) and is
// never swapped for another launcher; the default's own fallback stays as it was.
func TestSendRefusesAPickThatCantRun(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		launcher, code, words string
	}{
		{"far", proto.CodeLauncherOutOfScope, "far stays with the hub"},
		{"own", proto.CodeLauncherOwnService, "own"},
		{"gone", proto.CodeLauncherMissing, "gone"},
		{"cc-relay", CodeBadRequest, "can’t run assistant chats, so this message wasn’t sent"},
	} {
		t.Run(c.launcher, func(t *testing.T) {
			s, f := pickFixture(t)
			// Picked while it could run: kept as it was.
			if err := s.setModelPick(ctx, "main", &modelPick{MachineID: "server", Launcher: c.launcher, Model: "m", At: time.Now()}); err != nil {
				t.Fatal(err)
			}
			p, err := send(t, s, "main", map[string]any{})
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != c.code || !strings.Contains(re.Message+string(re.Data), c.words) {
				t.Fatalf("sent anyway or the wrong refusal: %v %v", p, err)
			}
			if len(f.sends) != 0 {
				t.Fatalf("reached the node: %v", f.sends)
			}
			if _, ok := s.modelPickOf(ctx, "server", "main"); !ok {
				t.Fatal("the refused pick was dropped")
			}
		})
	}
	// The default's launcher that can't run it still gives way to one that can (finishMainChatModel), as before.
	s, _ := pickFixture(t)
	setModel(t, s, "assistant", ModelBinding{Provider: "launcher.codex-gw"})
	if p, err := send(t, s, "main", map[string]any{}); err != nil || p["launcher"] == "codex-gw" || p["launcher"] == nil {
		t.Fatalf("default fallback: %v %v", p, err)
	}
}

// A pick names its machine: the same session id on another machine runs as it would without one.
func TestModelPickIsTheConversationsMachines(t *testing.T) {
	s, _ := pickFixture(t)
	if err := s.setModelPick(context.Background(), "side", &modelPick{MachineID: "other", Launcher: "claude", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if p, err := send(t, s, "side", map[string]any{}); err != nil || p["launcher"] != "cx" {
		t.Fatalf("another machine's pick applied: %v %v", p, err)
	}
}

// The main chat's picker says what it can't run here and why; hidden launchers stay out, and so does what runs.
func TestMainChatPickerListsWhatItCantRun(t *testing.T) {
	s, _ := pickFixture(t)
	stored := s.storedLaunchers(context.Background())
	putLaunchers(t, s, append(stored,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "resp", Kind: "api", API: "responses", BaseURL: "https://r.example", Model: "x"}, DisplayName: "Resp API"},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "chat", Kind: "api", API: "chat", BaseURL: "https://c.example", Model: "x"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "msgs", Kind: "api", API: "messages", BaseURL: "https://m.example", Model: "x"}},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "far-resp", Kind: "api", API: "responses", BaseURL: "https://f.example", Model: "x"}, Share: shareNone})...)
	if err := s.putSetting(context.Background(), launchersHiddenKey, []string{"traex"}); err != nil {
		t.Fatal(err)
	}
	r := appRequest(t, s, "GET", "/api/v1/machines/server/launchers?purpose=assistant", "", "en")
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	var out struct {
		Launchers   []ConfiguredLauncher  `json:"launchers"`
		Unavailable []unavailableLauncher `json:"unavailable"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range out.Unavailable {
		got = append(got, u.Name+":"+u.Kind+":"+u.Agent+":"+u.API+":"+u.Code)
		if u.Reason == "" {
			t.Errorf("%s has no reason", u.Name)
		}
	}
	want := []string{
		"cc-relay:launcher:claude::not_main_chat",
		"codex-gw:launcher:codex::not_main_chat",
		"far:launcher:::launcher_out_of_scope",
		"own:launcher:::launcher_own_service",
		"resp:api::responses:api_protocol",
		"chat:api::chat:api_protocol",
		"far-resp:api::responses:api_protocol",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unavailable:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, u := range out.Unavailable {
		switch u.Name {
		case "resp":
			if u.DisplayName != "Resp API" || !strings.Contains(u.Reason, "Responses API") {
				t.Errorf("resp: %+v", u)
			}
		case "chat":
			if !strings.Contains(u.Reason, "Chat Completions API") {
				t.Errorf("chat: %+v", u)
			}
		case "far":
			if !strings.Contains(u.Reason, "Settings › Model providers") {
				t.Errorf("far: %+v", u)
			}
		}
	}
	for _, l := range out.Launchers {
		if slices.ContainsFunc(out.Unavailable, func(u unavailableLauncher) bool { return u.Name == l.Name }) {
			t.Errorf("%s is both listed and unavailable", l.Name)
		}
	}
	if !slices.ContainsFunc(out.Launchers, func(l ConfiguredLauncher) bool { return l.Name == "msgs" }) {
		t.Error("a Messages API provider is not listed")
	}
	// In the request's language; New Agent's picker has no such list.
	if w := appRequest(t, s, "GET", "/api/v1/machines/server/launchers?purpose=assistant", "", "zh-Hans"); !strings.Contains(w.Body.String(), "跑不了助手对话") {
		t.Fatalf("zh-Hans: %s", w.Body.String())
	}
	if r := request(s, "GET", "/api/v1/machines/server/launchers?purpose=agent.new", ""); strings.Contains(r.Body.String(), `"unavailable"`) {
		t.Fatalf("agent.new: %s", r.Body.String())
	}
}

// With the fleet on, the conversation the apps get carries its pick, and a change reaches them (the snapshot names
// the machine as changed); an Agent's session never gets one.
func TestFleetConversationCarriesItsPick(t *testing.T) {
	ft := newFleetTest(t)
	machine, _ := ft.s.fleetMachine("mac")
	conv := proto.Session{SessionID: "conv", Agent: "claude", Cwd: machine.assistantCwd(), Status: proto.StatusIdle}
	agent := proto.Session{SessionID: "agent", Agent: "claude", Cwd: "/home/u/src", PaneID: "p1", Status: proto.StatusIdle}
	ft.d.setSessions("mac", []proto.Session{conv, agent})
	ctx := context.Background()
	for _, id := range []string{"conv", "agent"} {
		if err := ft.s.setModelPick(ctx, id, &modelPick{MachineID: "mac", Launcher: "claude", Model: "opus", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	picks := map[string]*proto.ModelPick{}
	for _, sess := range ft.s.decoratedUnarchived("mac") {
		picks[sess.SessionID] = sess.ModelPick
	}
	if p := picks["conv"]; p == nil || p.Launcher != "claude" || p.Model != "opus" {
		t.Fatalf("conversation: %+v", p)
	}
	if picks["agent"] != nil {
		t.Fatalf("an Agent got a pick: %+v", picks["agent"])
	}
	old := map[string]SessionView{"conv": {MachineID: "mac", SessionID: "conv"}}
	now := map[string]SessionView{"conv": {MachineID: "mac", SessionID: "conv", ModelPick: &proto.ModelPick{Launcher: "claude"}}}
	if got := changedMachines(old, now); !slices.Equal(got, []string{"mac"}) {
		t.Fatalf("a new pick changes nothing: %v", got)
	}
	if got := changedMachines(now, now); len(got) != 0 {
		t.Fatalf("the same pick changed: %v", got)
	}
}

// The book keeps at most maxModelPicks picks, dropping the oldest.
func TestModelPicksAreBounded(t *testing.T) {
	s, _ := pickFixture(t)
	ctx := context.Background()
	start := time.Now().Add(-time.Hour)
	for i := range maxModelPicks + 2 {
		id := "c" + strconv.Itoa(i)
		if err := s.setModelPick(ctx, id, &modelPick{MachineID: "server", Launcher: "claude", At: start.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	picks, _ := s.modelPicksSet()
	if len(picks) != maxModelPicks {
		t.Fatalf("%d picks", len(picks))
	}
	for _, p := range picks {
		if p.At.Before(start.Add(2 * time.Second)) {
			t.Fatalf("an oldest pick stayed: %v", p.At)
		}
	}
}

// Warming with no launcher gets the main conversation's pick ready, not the default's.
func TestWarmTakesTheMainConversationsPick(t *testing.T) {
	s, f := pickFixture(t)
	warmed := func() map[string]any {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i := len(f.calls) - 1; i >= 0; i-- {
			if f.calls[i].Method == DeviceAssistantWarm {
				var p map[string]any
				_ = json.Unmarshal(f.calls[i].Params, &p)
				return p
			}
		}
		return nil
	}
	s.warm("")
	if p := warmed(); p == nil || p["launcher"] != "gw" {
		t.Fatalf("default: %v", p)
	}
	if r := putPick(t, s, "main", `{"launcher":"cx","model":"gpt-5"}`); r.Code != 200 {
		t.Fatalf("PUT: %d %s", r.Code, r.Body.String())
	}
	s.warm("")
	if p := warmed(); p == nil || p["launcher"] != "cx" || p["model"] != "gpt-5" || p["effort"] != nil {
		t.Fatalf("pick: %v", p)
	}
	s.warm("claude") // a launcher named: that one, as before
	if p := warmed(); p == nil || p["launcher"] != "claude" {
		t.Fatalf("named: %v", p)
	}
}

// A new conversation takes a model with its first message, as a message does, and keeps what it was started with as
// its pick: its later messages run on it. Nothing named, nothing kept; a launcher that is gone falls back as before,
// with its model dropped and no pick kept.
func TestStartTakesAModelAndKeepsThePick(t *testing.T) {
	start := func(t *testing.T, s *Service, body string) (int, map[string]any) {
		t.Helper()
		r := appRequest(t, s, "POST", "/api/v1/assistant/conversations", body, "en")
		var out struct {
			Session proto.Session `json:"session"`
		}
		_ = json.Unmarshal(r.Body.Bytes(), &out)
		got := map[string]any{"body": r.Body.String(), "sid": out.Session.SessionID}
		if out.Session.ModelPick != nil {
			got["pick"] = *out.Session.ModelPick
		}
		return r.Code, got
	}
	t.Run("launcher, model and effort", func(t *testing.T) {
		s, f := pickFixture(t)
		code, got := start(t, s, `{"prompt":"hi","launcher":"cx","model":"vendor/typed-1","effort":"high"}`)
		want := proto.ModelPick{Launcher: "cx", Model: "vendor/typed-1", Effort: "high"}
		if code != 200 || got["pick"] != want {
			t.Fatalf("%d %v", code, got)
		}
		if p := f.lastSend(t); p["launcher"] != "cx" || p["model"] != "vendor/typed-1" || p["effort"] != "high" {
			t.Fatalf("first message: %v", p)
		}
		sid := got["sid"].(string)
		f.setSessions("server", proto.Session{SessionID: sid, Assistant: true, Launcher: "cx"})
		if p, err := send(t, s, sid, map[string]any{}); err != nil || p["launcher"] != "cx" || p["model"] != "vendor/typed-1" || p["effort"] != "high" {
			t.Fatalf("next message: %v %v", p, err)
		}
	})
	t.Run("a model for the default launcher", func(t *testing.T) {
		s, f := pickFixture(t)
		code, got := start(t, s, `{"prompt":"hi","model":"m9"}`)
		if want := (proto.ModelPick{Launcher: "gw", Model: "m9"}); code != 200 || got["pick"] != want {
			t.Fatalf("%d %v", code, got)
		}
		if p := f.lastSend(t); p["launcher"] != "gw" || p["model"] != "m9" {
			t.Fatalf("first message: %v", p)
		}
	})
	t.Run("an older app's launcher alone", func(t *testing.T) {
		s, _ := pickFixture(t)
		if code, got := start(t, s, `{"prompt":"hi","launcher":"claude"}`); code != 200 || got["pick"] != (proto.ModelPick{Launcher: "claude"}) {
			t.Fatalf("%d %v", code, got)
		}
	})
	t.Run("the main conversation", func(t *testing.T) {
		s, _ := pickFixture(t)
		code, got := start(t, s, `{"prompt":"hi","main":true,"launcher":"cx"}`)
		if code != 200 || got["pick"] != (proto.ModelPick{Launcher: "cx"}) {
			t.Fatalf("%d %v", code, got)
		}
		if p, ok := s.modelPickOf(context.Background(), "server", got["sid"].(string)); !ok || p.Launcher != "cx" || !s.isMain(context.Background(), got["sid"].(string)) {
			t.Fatalf("main pick: %+v %v", p, ok)
		}
	})
	t.Run("nothing named", func(t *testing.T) {
		s, _ := pickFixture(t)
		if code, got := start(t, s, `{"prompt":"hi"}`); code != 200 || got["pick"] != nil {
			t.Fatalf("%d %v", code, got)
		}
		if picks, _ := s.modelPicksSet(); len(picks) != 0 {
			t.Fatalf("kept: %v", picks)
		}
	})
	t.Run("a launcher that is gone", func(t *testing.T) {
		s, f := pickFixture(t)
		code, got := start(t, s, `{"prompt":"hi","launcher":"gone","model":"m9"}`)
		if code != 200 || got["pick"] != nil {
			t.Fatalf("%d %v", code, got)
		}
		if p := f.lastSend(t); p["launcher"] != "gw" || p["model"] != nil {
			t.Fatalf("first message: %v", p)
		}
	})
	t.Run("refused before anything starts", func(t *testing.T) {
		s, f := pickFixture(t)
		f.modelsLocked = map[string]string{"cx": "it passes --model itself"}
		for body, words := range map[string]string{
			`{"prompt":"hi","launcher":"claude","model":"-rf"}`: "-rf",
			`{"prompt":"hi","launcher":"cx","model":"gpt-5"}`:   "sets its own model",
		} {
			code, got := start(t, s, body)
			if code != 400 || !strings.Contains(got["body"].(string), words) {
				t.Fatalf("%s: %d %v", body, code, got)
			}
		}
		if len(f.sends) != 0 {
			t.Fatalf("started: %v", f.sends)
		}
		if picks, _ := s.modelPicksSet(); len(picks) != 0 {
			t.Fatalf("kept: %v", picks)
		}
	})
}
