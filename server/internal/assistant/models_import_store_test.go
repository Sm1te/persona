package assistant

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// settingsFaultStore is a memStore that counts reads by key, fails reads of failRead, fails a batch write that holds
// failBatch, and runs onRead before a read is answered.
type settingsFaultStore struct {
	*memStore
	mu        sync.Mutex
	reads     map[string]int
	failRead  string
	failBatch string
	onRead    func(st *settingsFaultStore, key string, n int)
}

func newSettingsFaultStore() *settingsFaultStore {
	return &settingsFaultStore{memStore: &memStore{}, reads: map[string]int{}}
}

func (st *settingsFaultStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	st.mu.Lock()
	st.reads[key]++
	n, hook := st.reads[key], st.onRead
	st.mu.Unlock()
	if hook != nil {
		hook(st, key, n)
	}
	if key == st.failRead {
		return false, errors.New("database unavailable")
	}
	return st.memStore.Setting(ctx, key, out)
}

func (st *settingsFaultStore) PutSettings(ctx context.Context, values map[string]any) error {
	if _, ok := values[st.failBatch]; ok && st.failBatch != "" {
		return errors.New("database unavailable")
	}
	return st.memStore.PutSettings(ctx, values)
}

func (st *settingsFaultStore) readCount(key string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.reads[key]
}

// oneAtATimeStore writes one setting at a time (no PutSettings) and fails writes of failPut.
type oneAtATimeStore struct {
	Store
	failPut string
}

func (st *oneAtATimeStore) PutSetting(ctx context.Context, key string, v any) error {
	if key == st.failPut {
		return errors.New("database unavailable")
	}
	return st.Store.PutSetting(ctx, key, v)
}

var hideListed = map[string][]proto.Launcher{"m1": {{Name: "acc", BaseURL: "https://a.example.com"}}}

// The preview reads what the hide check needs only when Persona lists one of the aliases, and never fails over it:
// facts it cannot read leave every alias none. The import refuses to hide what it cannot check, writing nothing.
func TestImportPreviewHideCheckIsLazyAndNeverFails(t *testing.T) {
	t.Run("nothing listed: nothing read", func(t *testing.T) {
		st := newSettingsFaultStore()
		_, mux, _ := hideTestHubOn(t, st, []zshrcAlias{hideAlias}, map[string][]proto.Launcher{}, nil)
		if got := previewSources(t, mux); got[0].Hide != "none" {
			t.Fatalf("%+v", got)
		}
		if n := st.readCount(modelProfilesKey) + st.readCount(launchersMachineKeysKey); n != 0 {
			t.Fatalf("the hide check's settings were read %d times", n)
		}
	})
	t.Run("listed, the facts do not read", func(t *testing.T) {
		st := newSettingsFaultStore()
		st.failRead = modelProfilesKey
		second := hideAlias
		second.Alias, second.Env = "acc2", map[string]string{"ANTHROPIC_BASE_URL": "https://a.example.com", "ANTHROPIC_API_KEY": importKeyA,
			"ANTHROPIC_MODEL": "a2", "CLAUDE_CODE_EFFORT_LEVEL": "high"}
		s, mux, _ := hideTestHubOn(t, st, []zshrcAlias{hideAlias, second}, hideListed, nil)
		got := previewProviders(t, mux)
		if len(got) != 1 || len(got[0].Variants) != 2 {
			t.Fatalf("%+v", got)
		}
		for _, src := range append(slices.Clone(got[0].Sources), got[0].Variants[0].Sources[0], got[0].Variants[1].Sources[0]) {
			if src.Hide != "none" || src.File == "" {
				t.Fatalf("%+v", src)
			}
		}
		r := importRequest(mux, "POST", `{"machine":"m1","providers":["acc"],"hide_aliases":true}`)
		if r.Code == 200 {
			t.Fatalf("hid without checking: %s", r.Body.String())
		}
		if list := s.storedLaunchers(context.Background()); len(list) != 0 {
			t.Fatalf("saved %+v", list)
		}
		// Without hiding, the import does not need them.
		if res := importAnswer(t, mux, `{"machine":"m1","providers":["acc"]}`); !slices.Equal(res.Created, []string{"acc-2"}) {
			t.Fatalf("%+v", res)
		}
	})
}

// The import reads launchers.hidden again right before it writes, not from what its checks read: a name hidden in
// between is kept.
func TestImportHidesOnTheListAsItIsNow(t *testing.T) {
	st := newSettingsFaultStore()
	s, mux, _ := hideTestHubOn(t, st, []zshrcAlias{hideAlias}, hideListed, nil)
	st.onRead = func(st *settingsFaultStore, key string, n int) {
		// The checks read launchers.hidden, then the providers' keys for single computers: something else is hidden
		// after the first and before the import writes.
		if key == launchersMachineKeysKey && n == 1 {
			_ = st.memStore.PutSetting(context.Background(), launchersHiddenKey, []string{"late"})
		}
	}
	res := importAnswer(t, mux, `{"machine":"m1","providers":["acc"],"hide_aliases":true}`)
	if !slices.Equal(res.Hidden, []string{"acc"}) {
		t.Fatalf("%+v", res)
	}
	st.onRead = nil
	if hidden := s.hiddenLaunchers(context.Background()); !slices.Equal(hidden, []string{"late", "acc"}) {
		t.Fatalf("launchers.hidden %v", hidden)
	}
}

// The providers and launchers.hidden are written in one transaction: when it fails, neither is.
func TestImportStoresProvidersAndHiddenTogether(t *testing.T) {
	st := newSettingsFaultStore()
	st.failBatch = launchersHiddenKey
	s, mux, _ := hideTestHubOn(t, st, []zshrcAlias{hideAlias}, hideListed, nil)
	if r := importRequest(mux, "POST", `{"machine":"m1","providers":["acc"],"hide_aliases":true}`); r.Code == 200 {
		t.Fatalf("%s", r.Body.String())
	}
	ctx := context.Background()
	if list := s.storedLaunchers(ctx); len(list) != 0 {
		t.Fatalf("providers saved without the hidden list: %+v", list)
	}
	if hidden := s.hiddenLaunchers(ctx); len(hidden) != 0 {
		t.Fatalf("%v", hidden)
	}
	st.failBatch = ""
	if res := importAnswer(t, mux, `{"machine":"m1","providers":["acc"],"hide_aliases":true}`); !slices.Equal(res.Created, []string{"acc-2"}) ||
		!slices.Equal(res.Hidden, []string{"acc"}) {
		t.Fatalf("%+v", res)
	}
	if hidden := s.hiddenLaunchers(ctx); !slices.Equal(hidden, []string{"acc"}) || len(s.storedLaunchers(ctx)) != 1 {
		t.Fatalf("%v", hidden)
	}
}

// A store that writes one setting at a time: the providers go first; when hiding then fails, the answer says the
// providers were imported but the aliases were not hidden (500), in the user's words.
func TestImportSaysTheProvidersWereImportedWhenHidingFails(t *testing.T) {
	st := &oneAtATimeStore{Store: &memStore{}, failPut: launchersHiddenKey}
	s, mux, _ := hideTestHubOn(t, st, []zshrcAlias{hideAlias}, hideListed, nil)
	r := importRequest(mux, "POST", `{"machine":"m1","providers":["acc"],"hide_aliases":true}`)
	want := i18n.T(s.requestCtxLang(context.Background()), "assistant-models_import.err_not_hidden")
	if r.Code != 500 || !strings.Contains(r.Body.String(), want) {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	if list := s.storedLaunchers(context.Background()); len(list) != 1 || list[0].Name != "acc-2" {
		t.Fatalf("%+v", list)
	}
}

// Hiding an rc-file launcher and restoring one change launchers.hidden under the configuration lock, as the import
// does, so none of them loses another's change.
func TestHideAndRestoreTakeTheConfigurationLock(t *testing.T) {
	s, _ := hideTestHub(t, nil, hideListed)
	ctx := context.Background()
	for _, c := range []struct{ method, path, want string }{
		{"DELETE", "/api/v1/launchers/acc", "acc"},
		{"POST", "/api/v1/launchers/acc/restore", ""},
	} {
		s.models.mu.Lock()
		done := make(chan int, 1)
		go func() { done <- request(s, c.method, c.path, "").Code }()
		select {
		case code := <-done:
			s.models.mu.Unlock()
			t.Fatalf("%s %s answered %d without the lock", c.method, c.path, code)
		case <-time.After(150 * time.Millisecond):
		}
		s.models.mu.Unlock()
		if code := <-done; code != 200 {
			t.Fatalf("%s %s: %d", c.method, c.path, code)
		}
		if hidden := s.hiddenLaunchers(ctx); strings.Join(hidden, ",") != c.want {
			t.Fatalf("%s: %v", c.path, hidden)
		}
	}
}

// Asking another computer for its aliases never puts their keys in the preview or the answer.
func TestImportAskingAnotherComputerReturnsNoKey(t *testing.T) {
	theirs := hideAlias
	theirs.Env = map[string]string{"ANTHROPIC_BASE_URL": "https://a.example.com", "ANTHROPIC_AUTH_TOKEN": "sk-test-their-own-key"}
	rc := map[string][]proto.Launcher{"m1": hideListed["m1"], "m2": {{Name: "acc", BaseURL: "https://a.example.com"}}}
	_, mux, asked := hideTestHubOn(t, newSettingsFaultStore(), []zshrcAlias{hideAlias}, rc, map[string]hideRemote{"m2": {aliases: []zshrcAlias{theirs}}})
	for _, method := range []string{"GET", "POST"} {
		r := importRequest(mux, method, `{"machine":"m1","providers":["acc"],"hide_aliases":true}`)
		for _, secret := range []string{importKeyA, "sk-test-their-own-key"} {
			if r.Code != 200 || strings.Contains(r.Body.String(), secret) {
				t.Fatalf("%s %d: %s", method, r.Code, r.Body.String())
			}
		}
		if !strings.Contains(r.Body.String(), `"elsewhere":["Studio"]`) {
			t.Fatalf("%s: %s", method, r.Body.String())
		}
	}
	if !slices.Equal(*asked, []string{"m2", "m2"}) {
		t.Fatalf("asked %v", *asked)
	}
}

// Under the lock the check reads only what was asked before it: a computer not asked then counts as elsewhere, and
// no node is asked.
func TestImportCheckUnderTheLockAsksNoNode(t *testing.T) {
	rc := map[string][]proto.Launcher{"m1": hideListed["m1"], "m2": {{Name: "acc", BaseURL: "https://a.example.com"}}}
	s, _, asked := hideTestHubOn(t, newSettingsFaultStore(), []zshrcAlias{hideAlias}, rc, map[string]hideRemote{"m2": {aliases: []zshrcAlias{hideAlias}}})
	ctx := context.Background()
	f, err := s.importHideFacts(ctx, "m1", s.zshrcLaunchers(ctx, false), nil)
	if err != nil {
		t.Fatal(err)
	}
	var g importGroup
	g.BaseURL = "https://a.example.com"
	target := importedLauncher(g, time.Time{})
	f.ask = false
	if src, err := s.aliasHide(ctx, f, "acc", target, importKeyA); err != nil || src.Hide != "elsewhere" || len(*asked) != 0 {
		t.Fatalf("%+v %v asked %v", src, err, *asked)
	}
	f.ask = true
	if src, err := s.aliasHide(ctx, f, "acc", target, importKeyA); err != nil || src.Hide != "ok" || !slices.Equal(*asked, []string{"m2"}) {
		t.Fatalf("%+v %v asked %v", src, err, *asked)
	}
}
