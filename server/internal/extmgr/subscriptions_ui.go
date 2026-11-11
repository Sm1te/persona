package extmgr

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
	"github.com/nwverse/persona/server/internal/subscriptionsui"
)

// The subscription pages (#2041, docs/extensions/subscriptions.md "Pages"): drawn here as declarative UI with the
// same request and answer as an extension page, so the apps reuse their renderers. Unlike an extension page, every
// device may look; only a full-access device may press anything that changes a subscription.

func (h *Manager) subscriptionUI(w http.ResponseWriter, r *http.Request) {
	var in uiRequest
	if !readExtensionJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	lang := h.uiFallbackLang(ctx, in.Context.Locale)
	if l := i18n.FromRequest(r); in.Context.Locale == "" && l != "" {
		lang = l
	}
	loc := h.userLocation()
	if in.Context.TZ != "" {
		if l, err := time.LoadLocation(in.Context.TZ); err == nil {
			loc = l
		}
	}
	p := principalOf(ctx)
	version := min(max(in.Context.UIVersion, 1), extension.UIVersion)
	env := subscriptionsui.Env{
		UIVersion: version,
		Lang:      lang,
		Loc:       loc,
		CanEdit:   p.Role == roleFull,
		By:        subscriptions.Actor{ID: actorOf(ctx), Name: p.Name},
		Name:      h.subscriptionConnectorName,
		Native:    h.subscriptionNative,
		Drafts:    h.subscriptionDrafts(),
		Platform:  in.Context.Platform,
	}
	res, err := subscriptionsui.Handle(ctx, h.subs, env, subscriptionsui.Request{State: in.State, Action: in.Action, Values: in.Values})
	switch {
	case errors.Is(err, subscriptionsui.ErrReadOnly):
		writeErrCode(w, http.StatusForbidden, subscriptions.CodeReadOnlyDevice, errors.New("this device can read subscriptions but not change them"))
		return
	case err != nil:
		writeSubscriptionErr(w, err)
		return
	}
	// Drawn for the app's UI version as an extension page is: the view itself (an older app gets a switch as a row it
	// taps, version 6's icons as older ones), then the result's lines and items.
	slot, _ := extension.Slot("extension_page")
	err = extension.FitResult(&res, slot, version, lang)
	if res.View != nil && err == nil {
		_, err = extension.Fit(res.View, slot, version, lang)
	}
	if err != nil {
		h.logf("subscriptions: page does not fit ui_version %d: %v", version, err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, res)
}

// subscriptionConnectorName is a connector as people know it on the pages: its own name, else its id.
func (h *Manager) subscriptionConnectorName(id string) string {
	if c := h.connectors.Get(id); c != nil && c.Name() != "" {
		return c.Name()
	}
	return id
}

// subscriptionNative says where Persona already reads a catalog source's service ("mail:example.com") itself: the
// mail account on that domain, or the connector whose links live on that host; "" when nothing does. Only a hint for
// the pages: subscribing stays possible.
func (h *Manager) subscriptionNative(service string) string {
	kind, domain, ok := strings.Cut(strings.ToLower(service), ":")
	if !ok || domain == "" {
		return ""
	}
	if kind == "mail" && h.mail != nil {
		if accounts, err := h.mail.ListAccounts(); err == nil {
			for _, a := range accounts {
				if _, d, ok := strings.Cut(strings.ToLower(a.Email), "@"); ok && d == domain {
					return a.Email
				}
			}
		}
	}
	for _, c := range h.connectors.All() {
		if !h.connectors.Enabled(c.ID()) {
			continue
		}
		for _, host := range c.Manifest().Hosts {
			if host = strings.ToLower(host); host == domain || strings.HasSuffix(host, "."+domain) {
				return h.subscriptionConnectorName(c.ID())
			}
		}
	}
	return ""
}

// subscriptionDrafts keeps the model drafts the pages start (#2040); one that finishes redraws the pages on every app,
// as an extension's invalidate does for its own.
func (h *Manager) subscriptionDrafts() *subscriptionsui.Drafts {
	h.subDraftsOnce.Do(func() {
		h.subDrafts = subscriptionsui.NewDrafts(func() {
			h.broadcast(map[string]any{"type": "extension_ui", "extension": subscriptionsui.PagesID, "invalidate": true})
		})
	})
	return h.subDrafts
}
