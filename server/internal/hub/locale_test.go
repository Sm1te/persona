package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The first full client's client_hello.locale becomes the user's language, and each one's its connection's; an
// answer-only one (the watch) only its connection's; an invalid or missing one changes nothing.
func TestClientHelloLocale(t *testing.T) {
	h := newTestHub(t)
	full := &client{out: make(chan []byte, 16), role: roleFull, hub: h}
	if h.userLang() != i18n.ZhHans || full.lang() != i18n.ZhHans {
		t.Fatal("nothing said yet should be zh-Hans")
	}
	h.onClientHello(full, []byte(`{"locale":"en"}`))
	if full.lang() != i18n.En || h.userLang() != i18n.En {
		t.Fatalf("full client: conn %s, user %s", full.lang(), h.userLang())
	}
	h.onClientHello(full, []byte(`{"platform":"web"}`))
	if full.lang() != i18n.En {
		t.Fatal("a hello without locale must keep the connection's")
	}
	h.onClientHello(full, []byte(`{"locale":"fr"}`))
	if h.userLang() != i18n.En {
		t.Fatal("an unsupported code must not change the user's language")
	}
	watch := &client{out: make(chan []byte, 16), role: roleAnswer, hub: h}
	h.onClientHello(watch, []byte(`{"locale":"ja"}`))
	if watch.lang() != i18n.Ja || h.userLang() != i18n.En {
		t.Fatalf("watch: conn %s, user %s", watch.lang(), h.userLang())
	}
	quiet := &client{out: make(chan []byte, 16), role: roleFull, hub: h, acceptLang: i18n.Ja}
	if quiet.lang() != i18n.Ja {
		t.Fatal("the upgrade request's Accept-Language is the connection's until a hello says one")
	}
}

// helloFrom is a new full-role connection of device (a device credential, "" for the main token) saying hello.
func helloFrom(h *Hub, device, hello string) *client {
	c := &client{out: make(chan []byte, 16), role: roleFull, hub: h, device: device}
	h.onClientHello(c, []byte(hello))
	return c
}

// Devices in different languages connecting in turn leave the user's language alone; only a device changing its own
// language changes it, on a new connection (the Apple apps take a new language at their next launch) or on the same
// one (a hello sent again).
func TestUserLangFollowsOnlyADevicesOwnChange(t *testing.T) {
	h := newTestHub(t)
	helloFrom(h, "dev_mac", `{"locale":"en"}`)
	if h.userLang() != i18n.En {
		t.Fatalf("the first device's language is the user's: %s", h.userLang())
	}
	for range 3 {
		helloFrom(h, "dev_phone", `{"locale":"zh-Hans"}`)
		helloFrom(h, "dev_mac", `{"locale":"en"}`)
		helloFrom(h, "dev_sim", `{"locale":"ja"}`)
		if h.userLang() != i18n.En {
			t.Fatalf("another device connecting must not change it: %s", h.userLang())
		}
	}
	helloFrom(h, "dev_phone", `{"locale":"ja"}`)
	if h.userLang() != i18n.Ja {
		t.Fatalf("the phone changed its language: %s", h.userLang())
	}
	helloFrom(h, "dev_mac", `{"locale":"en"}`)
	helloFrom(h, "dev_sim", `{"locale":"ja"}`)
	if h.userLang() != i18n.Ja {
		t.Fatalf("devices saying what they said before must not change it: %s", h.userLang())
	}
	mac := helloFrom(h, "dev_mac", `{"locale":"en"}`)
	h.onClientHello(mac, []byte(`{"platform":"web"}`))
	if h.userLang() != i18n.Ja {
		t.Fatal("a hello without a locale changes nothing")
	}
	h.onClientHello(mac, []byte(`{"locale":"zh-Hans"}`))
	if h.userLang() != i18n.ZhHans {
		t.Fatalf("the Mac changed its language on its open connection: %s", h.userLang())
	}

	// The main token names no device: its hello's device_id does, and one with neither only gives a first language.
	helloFrom(h, "", `{"device_id":"web-1","locale":"en"}`)
	helloFrom(h, "", `{"locale":"en"}`)
	helloFrom(h, "pair_x", `{"locale":"en"}`)
	if h.userLang() != i18n.ZhHans {
		t.Fatalf("new or unnamed devices must not change it: %s", h.userLang())
	}
	helloFrom(h, "", `{"device_id":"web-1","locale":"ja"}`)
	if h.userLang() != i18n.Ja {
		t.Fatalf("a device named by its hello changed its language: %s", h.userLang())
	}
	watch := &client{out: make(chan []byte, 16), role: roleAnswer, hub: h, device: "dev_watch"}
	h.onClientHello(watch, []byte(`{"locale":"en"}`))
	h.onClientHello(watch, []byte(`{"locale":"zh-Hans"}`))
	if h.userLang() != i18n.Ja {
		t.Fatal("an answer-only device never changes it")
	}
}

// Each device's language and the user's are kept, so a redeploy decides as before; the language saved before
// devices were remembered stays until a device changes its own; nothing is decided while the saved one can't be read.
func TestUserLangKeptAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	store := &settingsStore{vals: map[string][]byte{}}
	h := newTestHub(t)
	h.store = store
	h.loadUserLang(ctx)
	helloFrom(h, "dev_phone", `{"locale":"zh-Hans"}`)
	helloFrom(h, "dev_mac", `{"locale":"en"}`)
	if got := string(store.vals[userLocaleSetting]); got != `"zh-Hans"` {
		t.Fatalf("the first language is saved even when it is the default: %s", got)
	}

	again := newTestHub(t)
	again.store = store
	again.loadUserLang(ctx)
	puts := store.puts
	helloFrom(again, "dev_mac", `{"locale":"en"}`)
	helloFrom(again, "dev_phone", `{"locale":"zh-Hans"}`)
	if again.userLang() != i18n.ZhHans || store.puts != puts {
		t.Fatalf("after a restart the devices are known: user %s, %d writes", again.userLang(), store.puts-puts)
	}
	helloFrom(again, "dev_mac", `{"locale":"ja"}`)
	if again.userLang() != i18n.Ja || string(store.vals[userLocaleSetting]) != `"ja"` {
		t.Fatalf("a known device's change is the user's: %s / %s", again.userLang(), store.vals[userLocaleSetting])
	}
	var saved map[string]deviceLocale
	if err := json.Unmarshal(store.vals[deviceLocalesSetting], &saved); err != nil || saved["dev_mac"].Locale != i18n.Ja ||
		saved["dev_phone"].Locale != i18n.ZhHans {
		t.Fatalf("saved %s (%v)", store.vals[deviceLocalesSetting], err)
	}

	// A hub that saved a language before it remembered devices: they are new to it, so it stays.
	old := &settingsStore{vals: map[string][]byte{userLocaleSetting: []byte(`"en"`)}}
	up := newTestHub(t)
	up.store = old
	up.loadUserLang(ctx)
	helloFrom(up, "dev_phone", `{"locale":"zh-Hans"}`)
	if up.userLang() != i18n.En {
		t.Fatalf("the saved language stays until a device changes its own: %s", up.userLang())
	}
	helloFrom(up, "dev_phone", `{"locale":"ja"}`)
	if up.userLang() != i18n.Ja {
		t.Fatalf("%s", up.userLang())
	}

	// The saved language could not be read at start: a hello must not take its place until it can.
	broken := &unreadableSettings{settingsStore: &settingsStore{vals: map[string][]byte{userLocaleSetting: []byte(`"ja"`)}}}
	broken.fail.Store(true)
	b := newTestHub(t)
	b.store = broken
	b.loadUserLang(ctx)
	helloFrom(b, "dev_mac", `{"locale":"en"}`)
	if b.userLang() != i18n.ZhHans || broken.puts != 0 {
		t.Fatalf("nothing is decided without the saved language: %s, %d writes", b.userLang(), broken.puts)
	}
	broken.fail.Store(false)
	helloFrom(b, "dev_mac", `{"locale":"en"}`)
	if b.userLang() != i18n.Ja {
		t.Fatalf("once read, the saved language is the user's: %s", b.userLang())
	}
}

// unreadableSettings fails every read while fail is set.
type unreadableSettings struct {
	*settingsStore
	fail atomic.Bool
}

func (s *unreadableSettings) Setting(ctx context.Context, key string, out any) (bool, error) {
	if s.fail.Load() {
		return false, errors.New("database unavailable")
	}
	return s.settingsStore.Setting(ctx, key, out)
}

// Only the devices whose language was said or changed last are kept.
func TestPruneDeviceLocales(t *testing.T) {
	m := map[string]deviceLocale{}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range deviceLocalesMax + 6 {
		m[fmt.Sprintf("dev_%02d", i)] = deviceLocale{Locale: i18n.En, At: start.Add(time.Duration(i) * time.Minute)}
	}
	pruneDeviceLocales(m)
	if len(m) != deviceLocalesMax {
		t.Fatalf("kept %d", len(m))
	}
	if _, ok := m["dev_05"]; ok {
		t.Fatal("the oldest must go")
	}
	if _, ok := m[fmt.Sprintf("dev_%02d", deviceLocalesMax+5)]; !ok {
		t.Fatal("the newest must stay")
	}
}

// Each device's push reads in its language; one without falls back to the user's.
func TestPushLanguagePerDevice(t *testing.T) {
	p := &Pusher{userLang: func() i18n.Lang { return i18n.Ja }}
	if p.langOf(Device{Locale: "en"}) != i18n.En || p.langOf(Device{}) != i18n.Ja {
		t.Fatal("langOf")
	}
	ix := proto.Interaction{Kind: proto.IxPermission, Agent: "codex"}
	title := func(l i18n.Lang) any {
		return interactionPayloadIn(l, ix, "box")["aps"].(map[string]any)["alert"].(map[string]any)["title"]
	}
	if title(i18n.ZhHans) != "Codex 需要你授权" || title(i18n.En) != "Codex needs your permission" {
		t.Fatalf("titles %v / %v", title(i18n.ZhHans), title(i18n.En))
	}
	if interactionPayload(ix, "box")["aps"].(map[string]any)["alert"].(map[string]any)["title"] != "Codex 需要你授权" {
		t.Fatal("the default payload must stay Chinese")
	}
	done := donePayloadIn(i18n.En, "m", "box", "s", "title", "")
	if done["aps"].(map[string]any)["alert"].(map[string]any)["body"] != "Done" {
		t.Fatal("done body")
	}
	if done["aps"].(map[string]any)["category"] != "SESSION_DONE" {
		t.Fatal("a done push carries the category iOS replies from")
	}
}

// An i18n.Message error reads in the request's language; other errors are as they were.
func TestErrorInRequestLanguage(t *testing.T) {
	h := newTestHub(t)
	handler := logRequests(h, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plain" {
			writeErr(w, http.StatusBadRequest, errors.New("device_id is required"))
			return
		}
		writeErr(w, http.StatusConflict, i18n.Err("push.done_body"))
	}))
	call := func(path, lang string) string {
		r := httptest.NewRequest("GET", path, nil)
		if lang != "" {
			r.Header.Set("Accept-Language", lang)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Body.String()
	}
	if got := call("/x", "ja"); !strings.Contains(got, "完了しました") {
		t.Fatalf("ja: %s", got)
	}
	if got := call("/x", "en-US,en;q=0.9"); !strings.Contains(got, `"Done"`) {
		t.Fatalf("browser header: %s", got)
	}
	if got := call("/x", ""); !strings.Contains(got, "已完成") {
		t.Fatalf("no header is the user's language: %s", got)
	}
	if got := call("/plain", "ja"); !strings.Contains(got, "device_id is required") {
		t.Fatalf("plain error: %s", got)
	}
}

// An injected message's line reads in the user's language; the message the model reads stays as it is.
func TestInjectedLineLanguage(t *testing.T) {
	in := &proto.Injected{Kind: proto.InjectedTaskNotice, State: "done", Title: "写周报", Line: "任务「写周报」做完了"}
	if injectedLine(i18n.ZhHans, in) != in.Line {
		t.Fatal("zh-Hans must stay what ParseInjected wrote")
	}
	if got := injectedLine(i18n.En, in); got != "Task “写周报” is done" {
		t.Fatalf("en: %q", got)
	}
	lost := &proto.Injected{Kind: proto.InjectedSystem, State: "lost_tasks", Line: "node 重启过，3 个后台任务停了"}
	if got := injectedLine(i18n.Ja, lost); got != "ノードが再起動し、3件のバックグラウンドタスクが停止しました" {
		t.Fatalf("ja: %q", got)
	}
	one := &proto.Injected{Kind: proto.InjectedSystem, State: "lost_tasks", Line: "node 重启过，1 个后台任务停了"}
	if got := injectedLine(i18n.En, one); got != "The node restarted and 1 background task stopped" {
		t.Fatalf("en one: %q", got)
	}
}
