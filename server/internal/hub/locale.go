package hub

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// The user's language (docs/protocol/i18n.md "Protocol"): the language the user chose in their apps, kept in Postgres
// settings. Whatever the hub writes for people without a request in front of it reads in it: pushes to a device that
// gave no locale of its own, the assistant's answers, injected messages, task notifications, the bots' replies. Until
// a client says one it is zh-Hans, what the hub wrote before.
//
// It moves only when a device's own interface language changes: each full-role device's last client_hello.locale is
// kept (deviceLocalesSetting), and a device saying a different language than it did before is the user choosing
// another one. Another device connecting in its own language, or the user picking up another device, is not: a
// Mac in English and an iPhone in Chinese used to flip it at every reconnect, and the Inbox summaries with it.

const (
	userLocaleSetting    = "user_locale"
	deviceLocalesSetting = "hub.device_locales"
	deviceLocalesMax     = 64  // the devices whose language changed last (or which were seen first last) are kept
	deviceLangKeyMax     = 128 // a hello's device_id longer than this is not a device to remember
)

// deviceLocale is what one device last said in client_hello.locale, and when that changed (or was first said).
type deviceLocale struct {
	Locale i18n.Lang `json:"locale"`
	At     time.Time `json:"at"`
}

// userLangs is the state behind the user's language; mu serializes the hellos that decide it.
type userLangs struct {
	mu         sync.Mutex
	userLoaded bool                    // user_locale was read (or there is no store)
	loaded     bool                    // byDevice was read from the store
	byDevice   map[string]deviceLocale // deviceLangKey -> its last locale
}

func (h *Hub) loadUserLang(ctx context.Context) {
	h.langs.mu.Lock()
	defer h.langs.mu.Unlock()
	h.loadUserLangLocked(ctx)
}

func (h *Hub) loadUserLangLocked(ctx context.Context) {
	if h.store == nil {
		h.langs.userLoaded = true
		return
	}
	var s string
	if _, err := h.store.Setting(ctx, userLocaleSetting, &s); err != nil {
		h.logf("load %s: %v", userLocaleSetting, err)
		return // read again at the next hello, before any decides it
	}
	h.langs.userLoaded = true
	if l := i18n.Lang(s); i18n.Valid(l) {
		h.userLocale.Store(l)
	}
}

func (h *Hub) loadDeviceLocalesLocked(ctx context.Context) {
	g := &h.langs
	if g.loaded {
		return
	}
	g.byDevice = map[string]deviceLocale{}
	if h.store == nil {
		g.loaded = true
		return
	}
	var stored map[string]deviceLocale
	if _, err := h.store.Setting(ctx, deviceLocalesSetting, &stored); err != nil {
		h.logf("load %s: %v", deviceLocalesSetting, err)
		return // read again at the next hello; until then no device is known, so none changes the user's language
	}
	for k, v := range stored {
		if i18n.Valid(v.Locale) {
			g.byDevice[k] = v
		}
	}
	g.loaded = true
}

// userLang is the user's language, zh-Hans when none was said.
func (h *Hub) userLang() i18n.Lang {
	if l, ok := h.userLocale.Load().(i18n.Lang); ok && i18n.Valid(l) {
		return l
	}
	return i18n.Default
}

// noteUserLang takes the language l a full-role client said in client_hello from the device key (deviceLangKey). The
// user's language becomes l when that device said another language before (the user changed it in that app, or the
// system's changed under Match System), or when there is no user language yet (the first device's is taken); a
// device saying one for the first time, or again the same, only has it remembered. A key of "" (a client that names
// no device) can only give the first language.
func (h *Hub) noteUserLang(key string, l i18n.Lang) {
	if !i18n.Valid(l) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g := &h.langs
	g.mu.Lock()
	if !g.userLoaded {
		h.loadUserLangLocked(ctx)
	}
	if !g.userLoaded {
		g.mu.Unlock()
		return // without the saved language, a device's report must not replace it
	}
	cur, set := h.userLocale.Load().(i18n.Lang)
	if key != "" {
		h.loadDeviceLocalesLocked(ctx)
	}
	known, same := false, false
	if key != "" && g.loaded {
		var prev deviceLocale
		prev, known = g.byDevice[key]
		same = known && prev.Locale == l
		if !same {
			g.byDevice[key] = deviceLocale{Locale: l, At: time.Now().UTC().Truncate(time.Second)}
			pruneDeviceLocales(g.byDevice)
			if h.store != nil {
				if err := h.store.PutSetting(ctx, deviceLocalesSetting, g.byDevice); err != nil {
					h.logf("save %s: %v", deviceLocalesSetting, err)
				}
			}
		}
	}
	// The first language is taken, and saved even when it is the default so the next device does not take its
	// place; after that only a device that said another one before moves it.
	changed := (!set || known && !same) && !(set && cur == l)
	if changed {
		h.userLocale.Store(l)
		if h.store != nil {
			if err := h.store.PutSetting(ctx, userLocaleSetting, string(l)); err != nil {
				h.logf("save %s: %v", userLocaleSetting, err)
			}
		}
	}
	g.mu.Unlock()
	if changed {
		h.logf("user language: %s", l)
		h.assistantUpdated() // its CLAUDE.md asks it to answer in this language
	}
}

// deviceLangKey is the device c speaks for, for its language: its device credential, else (the main token, or a
// pairing token about to be traded for a credential) the device_id its hello gave; "" when it names neither.
func deviceLangKey(c *client, meta clientMeta) string {
	if c.device != "" && !strings.HasPrefix(c.device, "pair_") {
		return c.device
	}
	if id := strings.TrimSpace(meta.DeviceID); id != "" && len(id) <= deviceLangKeyMax {
		return "hello:" + id
	}
	return ""
}

// pruneDeviceLocales keeps the deviceLocalesMax devices whose language was said or changed last.
func pruneDeviceLocales(m map[string]deviceLocale) {
	if len(m) <= deviceLocalesMax {
		return
	}
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b string) int {
		if c := m[b].At.Compare(m[a].At); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	for _, k := range keys[deviceLocalesMax:] {
		delete(m, k)
	}
}

// requestLang is the language of a request's response: its Accept-Language, else the user's.
func (h *Hub) requestLang(r *http.Request) i18n.Lang {
	if l := i18n.From(r.Context()); l != "" {
		return l
	}
	return i18n.FromRequest(r).Or(h.userLang())
}

// lang is the language of what goes to this connection: what it said in client_hello, else its upgrade request's
// Accept-Language, else the user's.
func (c *client) lang() i18n.Lang {
	c.mu.Lock()
	l := i18n.Lang(c.meta.Locale)
	c.mu.Unlock()
	if i18n.Valid(l) {
		return l
	}
	if i18n.Valid(c.acceptLang) {
		return c.acceptLang
	}
	if c.hub != nil {
		return c.hub.userLang()
	}
	return i18n.Default
}

// contextLang is the language of what ctx is doing for people: its request's (i18n.With), else the user's.
func (h *Hub) contextLang(ctx context.Context) i18n.Lang {
	if l := i18n.From(ctx); i18n.Valid(l) {
		return l
	}
	if h == nil {
		return i18n.Default
	}
	return h.userLang()
}
