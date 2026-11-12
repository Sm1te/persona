package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/quota"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The model configuration (models.go, models_launchers.go): its records, providers and keys are the assistant's; the
// hub's facts (the machines' launchers, the plugins') come from the fake hub.

// downStore is a store that stops answering when fail is set.
type downStore struct {
	*memStore
	mu   sync.Mutex
	fail bool
}

func (s *downStore) failing(on bool) { s.mu.Lock(); s.fail = on; s.mu.Unlock() }

func (s *downStore) down() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.fail }

func (s *downStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if s.down() {
		return false, errors.New("database unavailable")
	}
	return s.memStore.Setting(ctx, key, out)
}

func (s *downStore) PutSetting(ctx context.Context, key string, v any) error {
	if s.down() {
		return errors.New("database unavailable")
	}
	return s.memStore.PutSetting(ctx, key, v)
}

func (s *downStore) PutSettings(ctx context.Context, values map[string]any) error {
	if s.down() {
		return errors.New("database unavailable")
	}
	return s.memStore.PutSettings(ctx, values)
}

// modelService is an assistant on a fake hub with one machine (server) whose hub.json has no model connection; the
// test sets f.hello.Models.
func modelService(t *testing.T) (*Service, *fakeHub, *downStore) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.ownModels = true
	st := &downStore{memStore: &memStore{}}
	return newServiceOn(t, f, st), f, st
}

func modelRequest(s *Service, method, id, body string) *httptest.ResponseRecorder {
	path := "/api/v1/model-config"
	if id != "" {
		path += "/purposes/" + id
	}
	r := httptest.NewRecorder()
	s.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(body)))
	return r
}

func request(s *Service, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRecorder()
	s.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(body)))
	return r
}

func purposeOf(t *testing.T, s *Service, id string) ModelPurpose {
	t.Helper()
	r := modelRequest(s, "GET", id, "")
	if r.Code != 200 {
		t.Fatalf("%s: %d %s", id, r.Code, r.Body.String())
	}
	var out struct {
		Purpose ModelPurpose `json:"purpose"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Purpose
}

func putLaunchers(t *testing.T, s *Service, list ...storedLauncher) {
	t.Helper()
	if err := s.putSetting(context.Background(), launchersManagedKey, list); err != nil {
		t.Fatal(err)
	}
}

func TestModelConfigurationReadFailureIsNotAnOldHub(t *testing.T) {
	s, _, st := modelService(t)
	st.failing(true)
	for _, method := range []string{"GET", "PATCH"} {
		body := ""
		if method == "PATCH" {
			body = `{"revision":0,"model":"selected"}`
		}
		if r := modelRequest(s, method, "agent.new", body); r.Code != http.StatusInternalServerError {
			t.Fatalf("%s database failure returned %d instead of 500: %s", method, r.Code, r.Body.String())
		}
	}
	st.failing(false)
	if r := modelRequest(s, "GET", "missing", ""); r.Code != http.StatusNotFound {
		t.Fatalf("unknown purpose returned %d", r.Code)
	}
}

func TestModelConfigurationExtensionFailureIsVisible(t *testing.T) {
	s, f, _ := modelService(t)
	f.voice = []assistantwire.VoicePlugin{{ID: "voice", Name: "Voice", Error: "读不到扩展模型列表"}}
	if p := purposeOf(t, s, "plugin.voice"); p.Status != "读不到扩展模型列表" || p.Kind != "plugin" {
		t.Fatalf("plugin failure was hidden: %+v", p)
	}
	f.voice = []assistantwire.VoicePlugin{{ID: "voice", Name: "Voice", Models: []ModelChoice{{ID: "v1"}}, Default: "v1"}}
	if r := modelRequest(s, "PATCH", "plugin.voice", `{"revision":0,"model":"v2"}`); r.Code != 400 {
		t.Fatalf("a model the plugin does not offer: %d %s", r.Code, r.Body.String())
	}
	if r := modelRequest(s, "PATCH", "plugin.voice", `{"revision":0,"model":"v1"}`); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	params := map[string]any{}
	if err := s.applyModel(context.Background(), "plugin.voice", params); err != nil || params["model"] != "v1" {
		t.Fatalf("%v %v", params, err)
	}
}

func TestModelConfigurationPatchIsSparseAndVersioned(t *testing.T) {
	s, f, _ := modelService(t)
	f.hello.Models.AI = ai.Config{API: "responses", BaseURL: "https://api.example.com", APIKeyFile: "/tmp/test-key", Model: "original", ReasoningEffort: "low"}
	ctx := context.Background()
	r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"model":"selected"}`)
	if r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	r = modelRequest(s, "PATCH", "inbox.triage", `{"revision":1,"effort":"high"}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	b, err := s.resolveModel(ctx, "inbox.triage")
	if err != nil || b.Model != "selected" || b.Effort != "high" {
		t.Fatalf("%+v %v", b, err)
	}
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"model":"stale"}`); r.Code != 409 {
		t.Fatalf("stale write accepted: %d", r.Code)
	}
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":2,"model":null}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	b, _ = s.resolveModel(ctx, "inbox.triage")
	if b.Model != "original" || b.Effort != "high" {
		t.Fatalf("restore inheritance: %+v", b)
	}
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"model":"knowledge"}`); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if b, _ = s.resolveModel(ctx, "inbox.triage"); b.Model != "original" {
		t.Fatal("another purpose overwrote triage")
	}
	for _, body := range []string{`{"revision":3,"model":""}`, `{"revision":3,"temperature":0.1}`, `{"model":"x"}`} {
		if r := modelRequest(s, "PATCH", "inbox.triage", body); r.Code != 400 {
			t.Fatalf("invalid patch accepted: %s (%d)", body, r.Code)
		}
	}
}

func TestModelConfigurationPreservesLegacyBaselines(t *testing.T) {
	f := newFakeHub().withMachine()
	f.ownModels = true
	f.hello.Models = HubModels{AI: ai.Config{Model: "base", ReasoningEffort: "low"}, TriageAI: &ai.Config{Model: "triage"},
		JudgeAI: &ai.Config{Model: "judge"}, KBAI: &ai.Config{Model: "knowledge"}}
	f.hello.Assistant.Launcher = "conversation"
	s := newTestService(t, f)
	ctx := context.Background()
	for id, want := range map[string]string{"inbox.triage": "triage", "inbox.judge": "triage", "translate": "knowledge", "kb.compress": "knowledge", "assistant.title": "knowledge"} {
		b, err := s.resolveModel(ctx, id)
		if err != nil || b.Model != want {
			t.Errorf("%s: %+v %v", id, b, err)
		}
	}
	for id, want := range map[string]string{"assistant": "conversation", "lark.agent": "claude", "inbox.agent": "claude"} {
		b, err := s.resolveModel(ctx, id)
		if err != nil || b.Provider != launcherProvider(want) {
			t.Errorf("%s: %+v %v", id, b, err)
		}
	}
	for _, id := range []string{purposeLarkAgent, purposeAgent} {
		if err := s.putSetting(ctx, modelKey(id), modelRecord{Revision: 9, Binding: ModelBinding{Provider: "launcher.old"}}); err != nil {
			t.Fatal(err)
		}
		if b, _ := s.resolveModel(ctx, id); b.Provider != "launcher.claude" {
			t.Fatalf("%s: deprecated use override still applied", id)
		}
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.new"})
	for _, id := range []string{purposeLarkAgent, purposeAgent} {
		if b, _ := s.resolveModel(ctx, id); b.Provider != "launcher.new" {
			t.Fatalf("%s: category selection did not reach it", id)
		}
	}
	// manager.json's inbox.agent.machine (the hub has it) is where the AI twin runs.
	f.hello.Models.InboxAgentMachine = "server"
	f.hello.Assistant.Machines = []string{"server"}
	if p := purposeOf(t, s, "inbox.agent"); p.MachineID != "server" {
		t.Fatalf("%+v", p)
	}
}

func TestAllAPIModelPurposesReachTheSelectedModel(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	var mu sync.Mutex
	var model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" { // a node's port probe (node/ports.go), not this test's client
			http.NotFound(w, r)
			return
		}
		var in struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		model = in.Model
		mu.Unlock()
		if r.Header.Get("x-api-key") != "private-test-key" {
			t.Error("wrong API credential")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "{}"}}})
	}))
	defer srv.Close()
	key, err := s.sealKey("private-test-key")
	if err != nil {
		t.Fatal(err)
	}
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "direct", Kind: "api", API: "messages", BaseURL: srv.URL, Model: "provider-default"}, KeySealed: key})
	revisions := map[string]int{}
	for _, p := range modelPurposes {
		if p.Kind != "api" {
			continue
		}
		category := modelCategoryID(p.ID)
		r := modelRequest(s, "PATCH", category, fmt.Sprintf(`{"revision":%d,"provider":"launcher.direct","model":"%s"}`, revisions[category], p.ID))
		revisions[category]++
		if r.Code != 200 {
			t.Fatalf("%s: %d %s", p.ID, r.Code, r.Body.String())
		}
		if _, err := s.purposeModel(p.ID).Complete(ctx, "", "x", 10); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := model
		mu.Unlock()
		if got != p.ID {
			t.Fatalf("%s sent %s", p.ID, got)
		}
	}
	listing := modelRequest(s, "GET", "", "").Body.String()
	if strings.Contains(listing, "private-test-key") || strings.Contains(listing, key) {
		t.Fatal("credential returned to client")
	}
	// A Messages API provider also runs claude on the nodes (#2455), so it goes to them with its key.
	if managed := mustManagedForNodes(t, s, ctx); len(managed) != 1 || managed[0].APIKey != "private-test-key" {
		t.Fatal("Messages API provider not sent to nodes")
	}
	_ = f
}

// A launcher for a gateway that takes only Bearer keeps that when added in the app, goes to the nodes with it, and the
// assistant's own Messages calls through it send Authorization: Bearer.
func TestBearerLauncherSavedSentAndCalled(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" { // a node's port probe (node/ports.go), not this test's client
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("authorization") != "Bearer private-test-key" || r.Header.Get("x-api-key") != "" {
			t.Errorf("credential sent as authorization %q, x-api-key %q", r.Header.Get("authorization"), r.Header.Get("x-api-key"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
	}))
	defer srv.Close()
	rec := request(s, "POST", "/api/v1/launchers", `{"name":"gw","base_url":"`+srv.URL+`","model":"m","api_key":"private-test-key","auth":"bearer"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"auth":"bearer"`) || strings.Contains(rec.Body.String(), "private-test-key") {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	// Every online node is given it, with the key (they keep it in memory for personad launch).
	f.mu.Lock()
	given := f.managed["server"]
	f.mu.Unlock()
	if len(given) != 1 || !given[0].BearerAuth() || given[0].APIKey != "private-test-key" {
		t.Fatalf("sent to nodes: %+v", given)
	}
	config, err := s.apiConfig(ctx, ModelBinding{Provider: "launcher.gw"})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := ai.New(config).Complete(ctx, "", "hi", 10); err != nil || text != "ok" {
		t.Fatalf("complete: %q %v", text, err)
	}
	// A change from an app that predates auth sends none: the launcher stays bearer. One that says api_key changes it.
	for _, step := range []struct {
		body   string
		bearer bool
	}{
		{`{"base_url":"` + srv.URL + `","model":"m2"}`, true},
		{`{"base_url":"` + srv.URL + `","model":"m2","auth":"api_key"}`, false},
	} {
		if rec := request(s, "PUT", "/api/v1/launchers/gw", step.body); rec.Code != 200 {
			t.Fatalf("update %s: %d %s", step.body, rec.Code, rec.Body.String())
		}
		if managed := mustManagedForNodes(t, s, ctx); len(managed) != 1 || managed[0].BearerAuth() != step.bearer || managed[0].APIKey != "private-test-key" {
			t.Fatalf("after %s: %+v", step.body, managed)
		}
	}
}

func TestModelProviderSwitchDoesNotInheritAnotherProvidersModel(t *testing.T) {
	b := mergeModel(ModelBinding{Provider: "legacy.ai", Model: "old-model", Effort: "high"}, ModelBinding{Provider: "launcher.other"})
	if b.Model != "" || b.Effort != "" {
		t.Fatalf("mixed provider fields: %+v", b)
	}
}

func TestExplicitLauncherResetsPurposeSpecificModel(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.original", Model: "specific", Effort: "high"})
	params := map[string]any{"launcher": "different"}
	if err := s.applyModel(ctx, "agent.new", params); err != nil {
		t.Fatal(err)
	}
	if params["model"] != nil || params["effort"] != nil {
		t.Fatalf("old provider options inherited: %+v", params)
	}
	params = map[string]any{"launcher": "different", "model": "request-model"}
	if err := s.applyModel(ctx, "agent.new", params); err != nil {
		t.Fatal(err)
	}
	if params["model"] != "request-model" {
		t.Fatal("explicit request model lost")
	}
}

func TestModelDatabaseFailureDoesNotFallback(t *testing.T) {
	s, f, st := modelService(t)
	f.hello.Models.AI = ai.Config{BaseURL: "https://api.example.com", Model: "old", APIKeyFile: "/tmp/key"}
	st.failing(true)
	if _, err := s.purposeModel("inbox.triage").Complete(context.Background(), "", "", 1); err == nil {
		t.Fatal("database failure silently treated as unconfigured")
	}
}

// #388: with no manager.json "ai", API uses run on a keyed connection the assistant already has instead of failing, and
// say so.
func TestAPIModelPurposesFallBackToAKeyedConnection(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	var mu sync.Mutex
	var model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" { // a node's port probe (node/ports.go), not this test's client
			http.NotFound(w, r)
			return
		}
		var in struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		model = in.Model
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}})
	}))
	defer srv.Close()
	sent := func() string { mu.Lock(); defer mu.Unlock(); return model }
	key, err := s.sealKey("private-test-key")
	if err != nil {
		t.Fatal(err)
	}

	// Nothing to call: the status and the error say where to fix it, not only hub.json.
	if p := purposeOf(t, s, "inbox.draft"); p.Status != mc(i18n.Default, modelNoAPIStatus) {
		t.Fatalf("unconfigured status: %+v", p)
	}
	if _, err := s.purposeModel("inbox.draft").Complete(ctx, "", "", 10); err == nil || !strings.Contains(err.Error(), "设置 › 模型配置") {
		t.Fatalf("unconfigured error: %v", err)
	}
	if triageUsableOf(t, s) {
		t.Fatal("nothing to call is usable")
	}

	// Keyless, hidden, Responses and model-less launchers are not candidates; a keyed Messages launcher is.
	launchers := []storedLauncher{
		{ManagedLauncher: proto.ManagedLauncher{Name: "nokey", BaseURL: srv.URL, Model: "m"}},
		{ManagedLauncher: proto.ManagedLauncher{Name: "hidden", BaseURL: srv.URL, Model: "m"}, KeySealed: key},
		{ManagedLauncher: proto.ManagedLauncher{Name: "responses", API: "responses", BaseURL: srv.URL, Model: "m"}, KeySealed: key},
		{ManagedLauncher: proto.ManagedLauncher{Name: "nomodel", BaseURL: srv.URL}, KeySealed: key},
		{ManagedLauncher: proto.ManagedLauncher{Name: "claude-gw", BaseURL: srv.URL, Model: "gateway-default"}, KeySealed: key},
	}
	putLaunchers(t, s, launchers...)
	if err := s.putSetting(ctx, launchersHiddenKey, []string{"hidden"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"inbox.triage", "inbox.judge", "inbox.draft", "translate", "kb.compress", "assistant.title", "agent.title"} {
		p := purposeOf(t, s, id)
		if p.Effective.Provider != "launcher.claude-gw" || p.Effective.Model != "gateway-default" || p.Status != "未验证" {
			t.Fatalf("%s did not fall back: %+v", id, p)
		}
		if _, err := s.purposeModel(id).Complete(ctx, "", "", 10); err != nil || sent() != "gateway-default" {
			t.Fatalf("%s: sent %q, %v", id, sent(), err)
		}
	}
	if !triageUsableOf(t, s) {
		t.Fatal("the keyed connection is usable")
	}
	if p := purposeOf(t, s, "kb"); p.InheritedFrom != "自动选择：claude-gw（hub 上能直接调用的接入）" {
		t.Fatalf("fallback not labelled: %+v", p)
	}
	if p := purposeOf(t, s, "translate"); p.InheritedFrom != "信息处理" {
		t.Fatalf("child no longer inherits its parent: %+v", p)
	}

	// A direct API provider comes before a launcher; nothing is written as an override.
	launchers = append(launchers, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "direct", Kind: "api", API: "responses", BaseURL: srv.URL, Model: "api-default"}, KeySealed: key})
	putLaunchers(t, s, launchers...)
	if b, _ := s.resolveModel(ctx, "inbox.draft"); b.Provider != "launcher.direct" {
		t.Fatalf("API provider not preferred: %+v", b)
	}
	if r, _ := s.modelRecord(ctx, "inbox.draft"); r.Revision != 0 || r.Binding != (ModelBinding{}) {
		t.Fatalf("fallback was stored: %+v", r)
	}

	// An explicit choice wins over the fallback, and manager.json's "ai" still wins over it too.
	if r := modelRequest(s, "PATCH", "kb", `{"revision":0,"provider":"launcher.claude-gw","model":"picked"}`); r.Code != 200 {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if b, _ := s.resolveModel(ctx, "inbox.draft"); b.Provider != "launcher.claude-gw" || b.Model != "picked" {
		t.Fatalf("explicit choice lost: %+v", b)
	}
	f.hello.Models.AI = ai.Config{BaseURL: srv.URL, APIKeyFile: "/tmp/test-key", Model: "hub-json"}
	if b, _ := s.resolveModel(ctx, "inbox.triage"); b.Provider != "legacy.triage" || b.Model != "hub-json" {
		t.Fatalf("hub.json baseline replaced: %+v", b)
	}
}

// "Sorting" is not changed to one that cannot be called while plugins that need it are on (the hub names them).
func TestTriageModelInUseByPlugins(t *testing.T) {
	s, f, _ := modelService(t)
	f.needTriage = []string{"Example Notes"}
	r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"provider":"legacy.ai"}`)
	var out map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	if r.Code != 422 || out["code"] != "triage_model_in_use" || fmt.Sprint(out["plugins"]) != "[Example Notes]" {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	f.needTriage = nil
	if r := modelRequest(s, "PATCH", "inbox.triage", `{"revision":0,"provider":"legacy.ai"}`); r.Code == 422 {
		t.Fatalf("nothing needs it: %d %s", r.Code, r.Body.String())
	}
}

func TestFourModelCategoriesCatalog(t *testing.T) {
	s, _, _ := modelService(t)
	setTasks(s, Task{ID: "one", Title: "不应出现在模型配置里的任务", Enabled: true})
	r := modelRequest(s, "GET", "", "")
	if r.Code != http.StatusOK {
		t.Fatal(r.Body.String())
	}
	var catalog struct {
		Purposes []ModelPurpose `json:"purposes"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, p := range catalog.Purposes {
		ids = append(ids, p.ID)
		if agentsOverride(p.ID) {
			if p.CategoryID != "agent.new" || p.Parent != "agent.new" || len(p.Fields) == 0 {
				t.Fatalf("%s missing independent editor: %+v", p.ID, p)
			}
		} else if p.CategoryID != p.ID || len(p.Uses) == 0 || len(p.Fields) == 0 {
			t.Fatalf("category missing scope or editor: %+v", p)
		}
	}
	if !reflect.DeepEqual(ids, modelConfigIDs) {
		t.Fatalf("catalog is not the ordered categories, the AI twin and Lark /agent: %v", ids)
	}
}

func TestModelUsesAreReadOnlyAndIgnoreDeprecatedOverrides(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	setTasks(s, Task{ID: "one", Title: "任务", Launcher: "old-task", Model: "old-model", Effort: "low", Enabled: true})
	bindings := map[string]ModelBinding{
		"assistant":    {Provider: "launcher.conversation", Model: "conversation", Effort: "high"},
		"agent.new":    {Provider: "launcher.agent", Model: "agent", Effort: "xhigh"},
		"kb":           {Provider: "legacy.kb", Model: "processing"},
		"inbox.triage": {Provider: "legacy.triage", Model: "screening", Effort: "off"},
	}
	for id, binding := range bindings {
		setModel(t, s, id, binding)
	}
	ids := []string{}
	for _, p := range modelPurposes {
		if p.Parent != "" && !ownModelRecord(p.ID) { // the uses with a record of their own are set apart on purpose
			ids = append(ids, p.ID)
		}
	}
	for _, id := range ids {
		old := ModelBinding{Provider: "launcher.deprecated", Model: "deprecated", Effort: "low"}
		setModel(t, s, id, old)
		b, err := s.resolveModel(ctx, id)
		if err != nil || b != bindings[modelCategoryID(id)] {
			t.Fatalf("%s did not follow its category: %+v %v", id, b, err)
		}
		p, err := s.modelPurpose(ctx, id, s.modelProviders(ctx))
		if err != nil || len(p.Fields) != 0 || p.Override != (ModelBinding{}) || p.CategoryID != modelCategoryID(id) {
			t.Fatalf("%s exposed an editable use: %+v %v", id, p, err)
		}
		if r := modelRequest(s, "PATCH", id, `{"revision":1,"model":"another"}`); r.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted an individual override: %d %s", id, r.Code, r.Body.String())
		}
		stored, err := s.modelRecord(ctx, id)
		if err != nil || stored.Binding != old {
			t.Fatalf("deprecated record was destroyed: %+v %v", stored, err)
		}
	}
}

// One scheduled task can run on its own launcher, model and effort; the others keep following Main chat, and the
// pre-#1570 records (model_config.task.<id>) never come back.
func TestATaskCanOverrideMainChatAndOtherTasksFollowIt(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	setTasks(s, Task{ID: "job", Title: "发布守护", Repeat: repeatInterval, EveryMinutes: 30, Enabled: true},
		Task{ID: "other", Title: "巡检", Repeat: repeatInterval, EveryMinutes: 60, Enabled: true})
	f.zshrc = map[string][]proto.Launcher{"server": {{Name: "cheap", Model: "cheap-default"}}}
	main := ModelBinding{Provider: "launcher.claude", Model: "claude-opus-5-5", Effort: "high"}
	setModel(t, s, "assistant", main)
	// What a task had before #1570: still there, and not what the task runs.
	dormant := modelRecord{Revision: 7, Binding: ModelBinding{Provider: "launcher.cheap", Model: "long-ago"}}
	if err := s.store().PutSetting(ctx, modelKey("task.job"), dormant); err != nil {
		t.Fatal(err)
	}
	if b, err := s.resolveModel(ctx, "task.job"); err != nil || b != main {
		t.Fatalf("the dormant record came back: %+v %v", b, err)
	}
	p := purposeOf(t, s, "task.job")
	if p.CategoryID != "assistant" || p.Parent != "assistant" || p.Revision != 0 || p.Override != (ModelBinding{}) ||
		p.InheritedFrom != "主对话" || len(p.Fields) == 0 || !slices.Contains(p.ProviderIDs, "launcher.cheap") ||
		p.Effective != main || p.MachineID != "server" {
		t.Fatalf("task purpose: %+v", p)
	}
	if p := purposeOf(t, s, "task.other"); p.Override != (ModelBinding{}) || p.Effective != main {
		t.Fatalf("a task without an override already differs: %+v", p)
	}
	// The task's own launcher, model and effort; its own record and revision.
	if r := modelRequest(s, "PATCH", "task.job", `{"revision":0,"provider":"launcher.cheap","model":"cheap-model","effort":"low"}`); r.Code != http.StatusOK {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if b, err := s.resolveModel(ctx, "task.job"); err != nil || b != (ModelBinding{Provider: "launcher.cheap", Model: "cheap-model", Effort: "low"}) {
		t.Fatalf("the task's override did not apply: %+v %v", b, err)
	}
	if b, _ := s.resolveModel(ctx, "task.other"); b != main {
		t.Fatalf("another task followed the first one's override: %+v", b)
	}
	if b, _ := s.resolveModel(ctx, "assistant"); b != main {
		t.Fatalf("Main chat changed: %+v", b)
	}
	var old modelRecord
	if _, err := s.store().Setting(ctx, modelKey("task.job"), &old); err != nil || old != dormant {
		t.Fatalf("the dormant record was touched: %+v %v", old, err)
	}
	p = purposeOf(t, s, "task.job")
	if p.Revision != 1 || p.Override.Provider != "launcher.cheap" || p.Override.Model != "cheap-model" || p.InheritedFrom != "" {
		t.Fatalf("task purpose after the save: %+v", p)
	}
	// Back to Main chat with nulls, as any other purpose.
	if r := modelRequest(s, "PATCH", "task.job", `{"revision":1,"provider":null,"model":null,"effort":null}`); r.Code != http.StatusOK {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if b, err := s.resolveModel(ctx, "task.job"); err != nil || b != main {
		t.Fatalf("clearing did not return to Main chat: %+v %v", b, err)
	}
	// The schedule tools' sparse fields: a launcher alone takes that launcher's default model, and "" clears.
	text := func(v string) *string { return &v }
	if err := s.setTaskModel(ctx, "other", text("cheap"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.resolveModel(ctx, "task.other"); b.Provider != "launcher.cheap" || b.Model != "" || b.Effort != "" {
		t.Fatalf("tool launcher: %+v", b)
	}
	if err := s.setTaskModel(ctx, "other", text("no-such-launcher"), nil, nil); err == nil {
		t.Fatal("an unknown launcher was accepted")
	}
	if err := s.setTaskModel(ctx, "other", text(""), nil, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.resolveModel(ctx, "task.other"); b != main {
		t.Fatalf("tool clear: %+v", b)
	}
	// A model alone pins the launcher the task runs on now, so a later Main chat change cannot pair it with another
	// agent; clearing returns it to whatever Main chat is then.
	if err := s.setTaskModel(ctx, "other", nil, text("pinned-model"), nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.resolveModel(ctx, "task.other"); b.Provider != "launcher.claude" || b.Model != "pinned-model" {
		t.Fatalf("model-only did not pin its launcher: %+v", b)
	}
	later := ModelBinding{Provider: "launcher.cheap", Model: "cheap-default"}
	setModel(t, s, "assistant", later)
	if b, _ := s.resolveModel(ctx, "task.other"); b.Provider != "launcher.claude" || b.Model != "pinned-model" {
		t.Fatalf("a later Main chat change moved the task: %+v", b)
	}
	if err := s.setTaskModel(ctx, "other", text(""), nil, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.resolveModel(ctx, "task.other"); b != later {
		t.Fatalf("clearing after the pin: %+v", b)
	}
}

// The schedule tools take a task's launcher / model / effort when they create or edit it.
func TestScheduleToolsSetATasksOwnModel(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	f.zshrc = map[string][]proto.Launcher{"server": {{Name: "cheap", Model: "cheap-default"}}}
	main := ModelBinding{Provider: "launcher.claude", Model: "claude-opus-5-5", Effort: "high"}
	setModel(t, s, "assistant", main)
	tool := func(name, args string) string {
		t.Helper()
		out, err := s.runTool(ctx, "server", "main", name, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	task := func(title string) Task {
		t.Helper()
		sc := s.loadSchedule(ctx)
		sc.mu.Lock()
		defer sc.mu.Unlock()
		for _, t := range sc.tasks {
			if t.Title == title {
				return t
			}
		}
		t.Fatalf("no task %q", title)
		return Task{}
	}
	tool("schedule_routine", `{"title":"巡检","prompt":"看看","time":"09:00","launcher":"cheap","model":"cheap-model","effort":"low"}`)
	rt := task("巡检")
	if b, err := s.resolveModel(ctx, "task."+rt.ID); err != nil ||
		b != (ModelBinding{Provider: "launcher.cheap", Model: "cheap-model", Effort: "low"}) {
		t.Fatalf("routine model: %+v %v", b, err)
	}
	tool("schedule_followup", `{"title":"看一眼","prompt":"看看","at":"30m","new_conversation":true,"launcher":"cheap"}`)
	fu := task("看一眼")
	if b, _ := s.resolveModel(ctx, "task."+fu.ID); b.Provider != "launcher.cheap" || b.Model != "" || b.Effort != "" {
		t.Fatalf("follow-up model: %+v", b)
	}
	// An unknown launcher creates no task that cannot run.
	if _, err := s.runTool(ctx, "server", "main", "schedule_routine",
		json.RawMessage(`{"title":"坏的","prompt":"看看","time":"10:00","launcher":"no-such-launcher"}`)); err == nil {
		t.Fatal("an unknown launcher was accepted")
	}
	if _, ok := func() (Task, bool) {
		sc := s.loadSchedule(ctx)
		sc.mu.Lock()
		defer sc.mu.Unlock()
		for _, t := range sc.tasks {
			if t.Title == "坏的" {
				return t, true
			}
		}
		return Task{}, false
	}(); ok {
		t.Fatal("a routine that could not take its launcher was left behind")
	}
	// Editing an existing routine checks the model before saving the rest of the change.
	if _, err := s.runTool(ctx, "server", "main", "schedule_routine",
		json.RawMessage(`{"id":"`+rt.ID+`","time":"10:30","launcher":"no-such-launcher"}`)); err == nil {
		t.Fatal("an unknown launcher was accepted on an existing routine")
	}
	if got := task("巡检"); got.Time != "09:00" {
		t.Fatalf("the refused edit was partly saved: %+v", got)
	}
	// A follow-up that continues this conversation keeps its launcher; its own needs new_conversation.
	if _, err := s.runTool(ctx, "server", "main", "schedule_followup",
		json.RawMessage(`{"title":"跟进的","prompt":"看看","at":"30m","launcher":"cheap"}`)); err == nil {
		t.Fatal("a model override for a follow-up into this conversation was accepted")
	}
	if _, ok := func() (Task, bool) {
		sc := s.loadSchedule(ctx)
		sc.mu.Lock()
		defer sc.mu.Unlock()
		for _, t := range sc.tasks {
			if t.Title == "跟进的" {
				return t, true
			}
		}
		return Task{}, false
	}(); ok {
		t.Fatal("a refused follow-up was saved")
	}
	// The routine has its own model; Main chat still does not.
	if b, _ := s.resolveModel(ctx, "assistant"); b != main {
		t.Fatalf("Main chat changed: %+v", b)
	}
	// Deleting it clears that, so a later task that got the same id cannot inherit it.
	tool("delete_routine", `{"id":"`+rt.ID+`"}`)
	if r, err := s.modelRecord(ctx, "task."+rt.ID); err != nil || r.Binding != (ModelBinding{}) {
		t.Fatalf("deleted task kept its model configuration: %+v %v", r, err)
	}
}

// A runtime's launcher (DSH) is offered in the new-chat picker under its own source, and is not listed among the
// models of Settings › Models (it is not a ~/.zshrc function to probe, edit or hide).
func TestRuntimeLauncherKeepsItsSource(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	list := []proto.Launcher{{Name: "cc_ds", Model: "m1"}, {Name: "dsh", Model: "DeepSeek Harness", Source: "dsh"}}
	f.launcherList = list
	got, err := s.machineLaunchers(ctx, "server")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Source != "zshrc" || got[1].Name != "dsh" || got[1].Source != "dsh" {
		t.Fatalf("picker %+v", got)
	}
	views := s.launcherViews(ctx)
	if len(views) != 1 || views[0].Name != "cc_ds" || len(views[0].Machines) != 1 {
		t.Fatalf("设置 › 模型 lists %+v", views)
	}
}

// The list: the managed ones first, then the machines' ~/.zshrc ones by name (one row for a name on several machines);
// deleting a ~/.zshrc one hides it (the pickers leave it out), restore brings it back; a managed one in use is not
// deleted under its category, and the nodes are told when one goes.
func TestLaunchersListHideRestoreDelete(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	f.mu.Lock()
	f.sessions["laptop"] = nil
	f.devices = []Device{{ID: "server", Name: "server", Online: true}, {ID: "laptop", Name: "laptop", Online: true}}
	f.zshrc = map[string][]proto.Launcher{"server": {{Name: "work", BaseURL: "https://gw.example.com"}, {Name: "claude"}},
		"laptop": {{Name: "work"}, {Name: "alpha"}, {Name: "claude-demo", BaseURL: "https://gw2.example.com", Source: "path"}}}
	f.mu.Unlock()
	// No key: not what the API categories fall back to (that would keep it from being deleted).
	if rec := request(s, "POST", "/api/v1/launchers", `{"name":"mine","base_url":"https://gw.example.com","model":"m","api_key":""}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := request(s, "POST", "/api/v1/launchers", `{"name":"work","base_url":"https://gw.example.com","model":"m"}`); rec.Code != http.StatusConflict {
		t.Fatalf("a name taken by a ~/.zshrc launcher: %d", rec.Code)
	}
	var list struct {
		Launchers []LauncherView `json:"launchers"`
	}
	rec := request(s, "GET", "/api/v1/launchers", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range list.Launchers {
		names = append(names, v.Name+":"+v.Source+":"+strings.Join(v.Machines, "+"))
	}
	// A wrapper script on the laptop's PATH (#5954) is one of its own launchers too, its source kept. Plain claude (the
	// server lists it) comes last as the built-in launcher (#2854).
	if strings.Join(names, " ") != "mine:persona: alpha:zshrc:laptop claude-demo:path:laptop work:zshrc:server+laptop claude:builtin:server" {
		t.Fatalf("%v %s", names, rec.Body.String())
	}
	if rec := request(s, "DELETE", "/api/v1/launchers/alpha", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"hidden":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	picker, _ := s.machineLaunchers(ctx, "laptop")
	if fmt.Sprint(picker) != fmt.Sprint([]proto.Launcher{{Name: "work", Source: "zshrc"}, {Name: "claude-demo", BaseURL: "https://gw2.example.com", Source: "path"},
		{Name: "mine", BaseURL: "https://gw.example.com", Model: "m", Source: "persona"}}) {
		t.Fatalf("picker: %+v", picker)
	}
	if rec := request(s, "POST", "/api/v1/launchers/alpha/restore", ""); rec.Code != 200 || slicesContain(s.hiddenLaunchers(ctx), "alpha") {
		t.Fatalf("%d %v", rec.Code, s.hiddenLaunchers(ctx))
	}
	setModel(t, s, "agent.new", ModelBinding{Provider: "launcher.mine"})
	if rec := request(s, "DELETE", "/api/v1/launchers/mine", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Agents") {
		t.Fatalf("deleted under its category: %d %s", rec.Code, rec.Body.String())
	}
	setModel(t, s, "agent.new", ModelBinding{})
	if rec := request(s, "DELETE", "/api/v1/launchers/mine", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	f.mu.Lock()
	given := f.managed["server"]
	f.mu.Unlock()
	if given == nil || len(given) != 0 {
		t.Fatalf("the nodes are told it went: %+v", given)
	}
}

// Automatic selection is not a user's assignment: deleting its provider leaves the next automatic pick, while an
// explicit choice still protects the provider underneath it (#3198).
func TestAutomaticProviderDeletionDoesNotCountAsExplicitUse(t *testing.T) {
	s, _, _ := modelService(t)
	key, err := s.sealKey("synthetic-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	putLaunchers(t, s,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "first", Kind: "api", BaseURL: "https://api.example.com", Model: "first-model"}, KeySealed: key},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "second", Kind: "api", BaseURL: "https://api.example.com", Model: "second-model"}, KeySealed: key})
	ctx := context.Background()
	for _, id := range []string{"kb", "inbox.triage", "agent.delegate"} {
		p := purposeOf(t, s, id)
		if p.Override != (ModelBinding{}) || p.Effective.Provider != "launcher.first" {
			t.Fatalf("%s did not start on the automatic provider: %+v", id, p)
		}
	}
	if rec := request(s, http.MethodDelete, "/api/v1/launchers/first", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":true`) {
		t.Fatalf("automatic selection blocked deletion: %d %s", rec.Code, rec.Body.String())
	}
	for _, id := range []string{"kb", "inbox.triage", "agent.delegate"} {
		if b, err := s.resolveModel(ctx, id); err != nil || b.Provider != "launcher.second" {
			t.Fatalf("%s did not move to the next automatic provider: %+v %v", id, b, err)
		}
	}
	setModel(t, s, "kb", ModelBinding{Provider: "launcher.second"})
	use := purposeOf(t, s, "kb").Name
	if rec := request(s, http.MethodDelete, "/api/v1/launchers/second", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), use) {
		t.Fatalf("an explicit provider was not protected: %d %s", rec.Code, rec.Body.String())
	}
}

// When only one provider is eligible, deleting it leaves automatic API categories with no model, so deletion is
// still refused with 409 Conflict naming the affected purposes.
func TestDeletingLastAutomaticProviderStillProtectsBuiltinPurposes(t *testing.T) {
	s, _, _ := modelService(t)
	key, err := s.sealKey("synthetic-provider-key")
	if err != nil {
		t.Fatal(err)
	}
	putLaunchers(t, s,
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "only", Kind: "api", BaseURL: "https://api.example.com", Model: "only-model"}, KeySealed: key})
	p := purposeOf(t, s, "kb")
	if p.Effective.Provider != "launcher.only" {
		t.Fatalf("kb did not start on the automatic provider: %+v", p)
	}
	rec := request(s, http.MethodDelete, "/api/v1/launchers/only", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), p.Name) {
		t.Fatalf("deleting the last automatic provider was not protected: %d %s", rec.Code, rec.Body.String())
	}
}

func slicesContain(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// A node's new connection gets the launchers added in Persona with their keys (main.go's devices NodeCalls), not
// the direct Responses API connections (a Messages one runs claude too, #2455).
func TestANodeThatReconnectsGetsTheManagedLaunchers(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	key, _ := s.sealKey("sk-node")
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", BaseURL: "https://gw.example.com", Model: "m"}, KeySealed: key},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "api", Kind: "api", API: "responses", BaseURL: "https://api.example.com", Model: "m"}, KeySealed: key})
	if g := mustManagedForNodes(t, s, ctx); len(g) != 1 || g[0].Name != "gw" || g[0].APIKey != "sk-node" {
		t.Fatalf("given %+v", g)
	}
}

// The hub's quota plugins and launcher sections reach a managed provider's gateway through the assistant, which puts
// the key in (models.launcher_http); another name is no_launcher (the hub goes to the machines' ~/.zshrc then).
func TestLauncherHTTPSendsWithTheKey(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-gw" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprint(w, `{"key":"sk-gw","today":3}`)
	}))
	defer gw.Close()
	key, _ := s.sealKey("sk-gw")
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", BaseURL: gw.URL, Model: "m"}, KeySealed: key})
	out, err := s.launcherHTTP(ctx, "gw", "", nil)
	if err != nil || fmt.Sprint(out) != "map[base_url:"+gw.URL+" machine:]" {
		t.Fatalf("%v %v", out, err)
	}
	out, err = s.launcherHTTP(ctx, "gw", "", &quota.Request{Method: "GET", URL: "/me", Headers: map[string]string{"Authorization": "Bearer " + quota.KeyPlaceholder}})
	resp, _ := out.(quota.Response)
	if err != nil || resp.Status != 200 || strings.Contains(resp.Body, "sk-gw") || !strings.Contains(resp.Body, quota.KeyPlaceholder) {
		t.Fatalf("%+v %v", out, err)
	}
	var re *rpc.Error
	if _, err := s.launcherHTTP(ctx, "zshrc-one", "", nil); !errors.As(err, &re) || re.Code != assistantwire.CodeNoLauncher {
		t.Fatalf("%v", err)
	}
}

// The hub's plugins list the launchers that have a gateway (models.launchers, for gateway.launchers): names and
// addresses, never a key; one without an address of its own is left out.
func TestModelsLaunchersListsTheGatewaysWithoutKeys(t *testing.T) {
	s, _, _ := modelService(t)
	key, _ := s.sealKey("sk-listed")
	putLaunchers(t, s, storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "gw", BaseURL: "https://gw.example.com/v1", Model: "m"}, KeySealed: key},
		storedLauncher{ManagedLauncher: proto.ManagedLauncher{Name: "official", Model: "m"}, KeySealed: key})
	out, ok, err := s.modelsRequest(context.Background(), assistantwire.MethodModelsLaunchers, json.RawMessage(`{}`))
	if !ok || err != nil {
		t.Fatalf("models.launchers: %v %v", ok, err)
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"launchers":[{"name":"gw","base_url":"https://gw.example.com/v1"}]}` || strings.Contains(string(b), "sk-listed") {
		t.Fatalf("%s", b)
	}
}

// The keys are sealed with the hub's secret.key, so the keys of the providers added before they were the assistant's
// still open; a missing file is made (once, whoever comes first).
func TestProviderKeysUseTheHubsSecretKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.key")
	a := New(Options{Hub: newFakeHub(), SecretKey: path})
	sealed, err := a.sealKey("sk-old")
	if err != nil || sealed == "" || sealed == "sk-old" {
		t.Fatalf("%q %v", sealed, err)
	}
	if b, err := os.ReadFile(path); err != nil || len(b) != 32 {
		t.Fatalf("secret.key: %d bytes, %v", len(b), err)
	}
	b := New(Options{Hub: newFakeHub(), SecretKey: path}) // a restart, or the hub: the same file
	if plain, err := b.openKey(sealed); err != nil || plain != "sk-old" {
		t.Fatalf("%q %v", plain, err)
	}
}

func TestLauncherInputCheck(t *testing.T) {
	ok := launcherInput{Name: "jv_strong", BaseURL: "https://gw.example.com", Model: "m1", Args: []string{"--dangerously-skip-permissions"}}
	if err := ok.check(); err != nil {
		t.Fatal(err)
	}
	for _, auth := range []string{"api_key", "bearer"} {
		ok.Auth = auth
		if err := ok.check(); err != nil {
			t.Fatalf("auth %s: %v", auth, err)
		}
	}
	for _, bad := range []launcherInput{
		{Name: "x", BaseURL: "https://gw.example.com", Model: "m", Auth: "basic"},
		{Name: "claude", BaseURL: "https://gw.example.com", Model: "m"},
		{Name: "a b", BaseURL: "https://gw.example.com", Model: "m"},
		{Name: "x", BaseURL: "gw.example.com", Model: "m"},
		{Name: "x", BaseURL: "https://gw.example.com"},
		{Name: "x", BaseURL: "https://gw.example.com", Model: "m", Args: []string{"; rm -rf ~"}},
		{Name: "x", BaseURL: "https://gw.example.com", Model: "m", Kind: "api", SubagentModel: "s"},
		{Name: "x", BaseURL: "https://gw.example.com", Model: "m", Kind: "api", CustomModel: "c"},
		{Name: "x", BaseURL: "https://gw.example.com", Model: "m", CustomModelDescription: "no name"},
	} {
		if bad.check() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestPresetForPrefersTheAddedFromPresetThenTheGateway(t *testing.T) {
	presets := []LauncherPreset{
		{ID: "gw/api", Kind: "api", BaseURL: "https://gw.example.com"},
		{ID: "gw/team", Kind: "launcher", BaseURL: "https://gw.example.com/"},
		{ID: "gw/relay", Kind: "launcher", BaseURL: "https://relay.example.com"},
	}
	if pr, ok := presetFor(presets, "gw/relay", "https://gw.example.com"); !ok || pr.ID != "gw/relay" {
		t.Fatal("the preset it was added from wins:", pr.ID, ok)
	}
	if pr, ok := presetFor(presets, "", "HTTPS://GW.example.com"); !ok || pr.ID != "gw/team" {
		t.Fatal("by gateway, launcher kind only, case and trailing slash ignored:", pr.ID, ok)
	}
	if _, ok := presetFor(presets, "", "https://other.example.com"); ok {
		t.Fatal("no preset for another gateway")
	}
	if _, ok := presetFor(presets, "", ""); ok {
		t.Fatal("no gateway matches nothing")
	}
}

func TestWithPresetModelsKeepsTheLaunchersOwnFirst(t *testing.T) {
	res := proto.LauncherModels{Agent: "claude", Models: []proto.AgentModel{
		{ID: "default", Default: true}, {ID: "opus", Resolved: "m-1"}, {ID: "m-2"},
	}}
	pr := LauncherPreset{Name: "Example Gateway", Models: []extension.ProviderModel{
		{ID: "m-1"}, {ID: "m-2"}, {ID: "m-3", Name: "Model Three"}, {ID: ""}, {ID: "m-4"}, {ID: "m-3"},
	}}
	got := withPresetModels(res, pr).Models
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if fmt.Sprint(ids) != "[default opus m-2 m-3 m-4]" {
		t.Fatal(ids)
	}
	if got[3].Name != "Model Three" || got[3].Description != "Example Gateway" || got[4].Name != "m-4" {
		t.Fatalf("%+v", got[3:])
	}
}

// A preset's models count for a send's check too: a model picked from the gateway's list is not dropped.
func TestPresetModelsCountForTheSendCheck(t *testing.T) {
	s, f, _ := modelService(t)
	f.presets = []LauncherPreset{{ID: "gw/team", Kind: "launcher", BaseURL: "https://gw.example.com", Models: []extension.ProviderModel{{ID: "gw-small"}}}}
	f.zshrc = map[string][]proto.Launcher{"server": {{Name: "gw-alias", BaseURL: "https://gw.example.com/"}}}
	f.agentModels = map[string][]proto.AgentModel{"gw-alias": {{ID: "default", Default: true}}}
	if !s.launcherHasModel(context.Background(), "server", "gw-alias", "gw-small") {
		t.Fatal("a send with a preset model is checked against the list that offered it")
	}
	if s.launcherHasModel(context.Background(), "server", "gw-alias", "gw-gone") {
		t.Fatal("a model in no list")
	}
}

// TestAutoTabNameLive names a few tasks with a real model:
// PERSONA_LIVE_AI='{"base_url":"…","api_key_file":"…","model":"…"}' go test -run AutoTabNameLive -v ./internal/assistant
func TestAutoTabNameLive(t *testing.T) {
	raw := os.Getenv("PERSONA_LIVE_AI")
	if raw == "" {
		t.Skip("PERSONA_LIVE_AI not set")
	}
	var cfg ai.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	f := newFakeHub()
	f.hello.Models.AI = cfg
	s := newTestService(t, f)
	for _, p := range []string{"iPhone Duo 展开点击切换 agent，侧边会先收起再展开请修复", "把终端从 tab 栏去掉",
		"研究和实现怎么做飞书的 AI 分身：自动收集重要信息，判断是否需要处理，用 agent 处理并草拟回复", "fix the flaky login test on CI"} {
		start := time.Now()
		name := s.tabName(context.Background(), p)
		t.Logf("%-6s %q ← %s", time.Since(start).Round(10*time.Millisecond), name, p)
		if name == "" {
			t.Errorf("no name for %q", p)
		}
	}
}

// triageUsableOf is models.usable inbox.triage, as the hub asks it.
func triageUsableOf(t *testing.T, s *Service) bool {
	t.Helper()
	out, ok, err := s.modelsRequest(context.Background(), assistantwire.MethodModelsUsable, json.RawMessage(`{"purpose":"inbox.triage"}`))
	if !ok || err != nil {
		t.Fatalf("models.usable: %v %v", ok, err)
	}
	return out.(map[string]any)["usable"].(bool)
}

func TestSessionLauncherTypes(t *testing.T) {
	list := sessionLaunchers([]ConfiguredLauncher{
		{Launcher: proto.Launcher{Name: "claude", Model: "default"}},
		{Launcher: proto.Launcher{Name: "codex"}},
		{Launcher: proto.Launcher{Name: "wrapper", Model: "configured-model"}},
	}, []ConfiguredLauncher{
		{Launcher: proto.Launcher{Name: "wrapper"}, AgentType: "traex"},
		{Launcher: proto.Launcher{Name: "other-wrapper"}, AgentType: "codex"},
	})
	if len(list) != 4 || list[0].AgentType != "claude" || list[1].AgentType != "codex" ||
		list[2].AgentType != "traex" || list[2].Model != "configured-model" || list[3].AgentType != "codex" {
		t.Fatalf("launchers = %+v", list)
	}
	// The old iOS launcher model ignores the optional type and retains its existing fields.
	raw, _ := json.Marshal(list)
	var legacy []proto.Launcher
	if json.Unmarshal(raw, &legacy) != nil || len(legacy) != 4 || legacy[0].Name != "claude" {
		t.Fatalf("legacy launchers = %s", raw)
	}
}

func TestUpdateManagedLauncherPreservesAgentWhenOmitted(t *testing.T) {
	s, _, _ := modelService(t)
	if w := serve(s, "POST", "/api/v1/launchers", `{"name":"my-codex","kind":"launcher","agent":"codex","model":"gpt-5.4"}`); w.Code != 200 {
		t.Fatalf("create codex launcher: %d %s", w.Code, w.Body)
	}
	// An older client (such as iOS/Mac LauncherInput without an agent field) omits agent and base_url on PUT.
	if w := serve(s, "PUT", "/api/v1/launchers/my-codex", `{"name":"my-codex","kind":"launcher","model":"gpt-5.5"}`); w.Code != 200 {
		t.Fatalf("update codex launcher without agent: %d %s", w.Code, w.Body)
	}
	got := s.storedLaunchers(context.Background())
	if len(got) != 1 || got[0].Agent != "codex" || got[0].Model != "gpt-5.5" {
		t.Fatalf("expected agent=codex preserved, got %+v", got)
	}
}

func mustManagedForNodes(t *testing.T, s *Service, ctx context.Context) []proto.ManagedLauncher {
	t.Helper()
	list, err := s.managedForNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestAskLauncherModelsAddsOnlyExactCatalogPrices(t *testing.T) {
	s, f, _ := modelService(t)
	own := &proto.ModelPrice{Input: 9, Output: 99}
	f.agentModels = map[string][]proto.AgentModel{"relay": {
		{ID: "opus", Resolved: "claude-opus-5-5"},
		{ID: "other-alias", Resolved: "claude-opus-5-5", Price: own},
		{ID: "claude-opus-5-6", Price: own},
		{ID: "private-model", Price: &proto.ModelPrice{Input: 4}},
	}}
	res, err := s.askLauncherModels(context.Background(), "server", "relay", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Models) != 4 || res.Models[0].Price == nil || res.Models[0].Price.Input != 4 || res.Models[0].Price.Output != 20 {
		t.Fatalf("exact model price: %+v", res.Models)
	}
	if got := res.Models[1].Price; got == own || got == nil || got.Input != 4 || got.Output != 20 {
		t.Fatalf("an external price survived or replaced the exact reference price: %+v", res.Models)
	}
	if res.Models[2].Price != nil || res.Models[3].Price != nil {
		t.Fatalf("an inherited or unknown model kept or gained a non-catalog price: %+v", res.Models)
	}
}

// A managed provider's own models are listed after Claude Code's /model entries (which already resolve to some of them)
// and count for a send's check: one picked in "New Agent" is not dropped.
func TestProviderModelsListedAndCountForTheSendCheck(t *testing.T) {
	s, f, _ := modelService(t)
	putLaunchers(t, s, storedLauncher{DisplayName: "Relay", ManagedLauncher: proto.ManagedLauncher{Name: "relay", Model: "gw/a",
		Models: []proto.ProviderModel{{ID: "gw/a"}, {ID: "gw/b", Name: "Model B", Efforts: []string{"low", "high"}}, {ID: ""}, {ID: "gw/c"}, {ID: "gw/b"}}}})
	f.agentModels = map[string][]proto.AgentModel{"relay": {{ID: "default", Default: true, Resolved: "gw/a"}, {ID: "opus", Resolved: "gw/a"}}}
	res, err := s.askLauncherModels(context.Background(), "server", "relay", false)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range res.Models {
		ids = append(ids, m.ID)
	}
	if fmt.Sprint(ids) != "[default opus gw/b gw/c]" {
		t.Fatal(ids)
	}
	if b := res.Models[2]; b.Name != "Model B" || b.Description != "Relay" || fmt.Sprint(b.Efforts) != "[low high]" || res.Models[3].Name != "gw/c" {
		t.Fatalf("%+v", res.Models[2:])
	}
	if !s.launcherHasModel(context.Background(), "server", "relay", "gw/c") {
		t.Fatal("a send with the provider's model is checked against the list that offered it")
	}
	if s.launcherHasModel(context.Background(), "server", "relay", "gw/gone") {
		t.Fatal("a model in no list")
	}
}

func TestTriageDefaultEffortIsOffAndHonoursMessagesAPI(t *testing.T) {
	s, _, _ := modelService(t)
	ctx := context.Background()

	b, err := s.resolveModel(ctx, "inbox.triage")
	if err != nil {
		t.Fatal(err)
	}
	if b.Effort != "off" {
		t.Fatalf("expected triage default effort off, got %q", b.Effort)
	}

	key, err := s.sealKey("test-key")
	if err != nil {
		t.Fatal(err)
	}
	putLaunchers(t, s,
		storedLauncher{
			ManagedLauncher: proto.ManagedLauncher{Name: "anthropic", Kind: "api", API: "messages", BaseURL: "https://api.anthropic.com", Model: "claude-haiku-5-5"},
			KeySealed:       key,
		},
		storedLauncher{
			ManagedLauncher: proto.ManagedLauncher{Name: "other", Kind: "api", API: "messages", BaseURL: "https://api.example.com", Model: "claude-haiku-5-5"},
			KeySealed:       key,
		})

	p := purposeOf(t, s, "inbox.triage")
	if !slices.Contains(p.Fields, "effort") {
		t.Fatalf("expected Fields to include effort, got %v", p.Fields)
	}

	r := modelRequest(s, http.MethodPatch, "inbox.triage", fmt.Sprintf(`{"revision":%d,"provider":"launcher.other"}`, p.Revision))
	if r.Code != http.StatusOK {
		t.Fatalf("select Messages provider: %d %s", r.Code, r.Body.String())
	}
	b, err = s.resolveModel(ctx, "inbox.triage")
	if err != nil || b.Provider != "launcher.other" || b.Effort != "off" {
		t.Fatalf("provider-only selection must keep triage's default off: %+v, %v", b, err)
	}
	cfg, err := s.apiConfig(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReasoningEffort != "off" {
		t.Fatalf("expected apiConfig ReasoningEffort to be off, got %q", cfg.ReasoningEffort)
	}

	p = purposeOf(t, s, "inbox.triage")
	r = modelRequest(s, http.MethodPatch, "inbox.triage", fmt.Sprintf(`{"revision":%d,"effort":"low"}`, p.Revision))
	if r.Code != http.StatusOK {
		t.Fatalf("save Messages effort: %d %s", r.Code, r.Body.String())
	}
	b, err = s.resolveModel(ctx, "inbox.triage")
	if err != nil || b.Effort != "low" {
		t.Fatalf("explicit Messages effort was not preserved: %+v, %v", b, err)
	}
}
