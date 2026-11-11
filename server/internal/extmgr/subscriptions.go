package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Source subscriptions (#2038, docs/extensions/subscriptions.md): the core is internal/subscriptions; here it gets the
// manager's connectors, database, audit and broadcasts, and answers /api/v1/subscriptions. Reading is for every
// device; changing anything needs full access (read_only_device otherwise).

// subscriptionsFeature is in the manager's report once subscriptions deliver to the Inbox and the knowledge base
// (#2039, so now): the apps show their entry rows only then, so a subscription the user turns on does what its switch
// says.
const subscriptionsFeature = "source_subscriptions"

// subscriptionPushFeature says a device can hand in its own items (POST /api/v1/subscriptions/-/push, #3848): the iOS
// Shortcuts action tells an older hub apart by it.
const subscriptionPushFeature = "subscription_push"

// subscriptionsShown says whether the report carries subscriptionsFeature.
func (h *Manager) subscriptionsShown() bool { return h.subs != nil && h.subs.Features().Deliver }

// startSubscriptions makes the service and starts its runner (after the connectors, so it finds them).
func (h *Manager) startSubscriptions(ctx context.Context) {
	var store subscriptions.Store
	if d, ok := h.store.(subscriptions.DB); ok {
		store = subscriptions.NewDBStore(d)
	} else {
		store = subscriptions.NewMemStore() // no database (tests): kept in memory only
	}
	h.subs = subscriptions.New(h.subscriptionOptions(store))
	go h.subs.Run(ctx)
}

// subscriptionOptions are the service's parts in this manager, over store.
func (h *Manager) subscriptionOptions(store subscriptions.Store) subscriptions.Options {
	return subscriptions.Options{
		Store:      store,
		Connectors: subscriptionConnectors{h},
		Location:   h.userLocation,
		Audit: func(ctx context.Context, id, action string, by subscriptions.Actor, detail map[string]any) {
			h.audit(ctx, id, action, by.ID, detail)
		},
		// A subscription changed (saved, ran, deleted): the apps showing it read it again.
		Changed: func(s subscriptions.Subscription, removed bool) {
			h.broadcast(map[string]any{"type": "subscription", "id": s.ID, "connector": s.Connector, "revision": s.Revision,
				"state": s.State, "removed": removed})
		},
		Logf:     h.logf,
		Features: subscriptions.Features{Deliver: true, Generate: true},
		// Drafting recipes (#2040): the assistant holds the keys and the purpose subscription.recipe.
		Model:    subscriptionModel{h},
		Models:   h.purposeModelInfo,
		Language: func(ctx context.Context) string { return h.contextLang(ctx).Name() },
		// The destinations (#2039): each subscription is an internal source of the Inbox's rounds (readSubscriptions)
		// and of the knowledge base's archives (kbArchives, subscriptionArchive).
		Lang: h.userLang,
		ExtensionRules: func(ext string) string {
			if h.extensions == nil {
				return ""
			}
			return h.extensionTriageRules(ext)
		},
		Delivered: func(s subscriptions.Subscription) {
			if s.ToInbox {
				h.kickInbox()
			}
			if s.ToKB {
				h.kbKick(s.ID, connector.ScopeChats)
			}
		},
		Forget: h.forgetSubscription,
	}
}

// readSubscriptions adds the subscriptions to an Inbox round as sources a plugin's connector would be (their new items
// are triaged before they enter, and wait without a model unless they carry a fallback tier): every round names them,
// so the Inbox keeps their held-back counts; a round that reads the connectors reads their things too.
func (h *Manager) readSubscriptions(ctx context.Context, r *assistantwire.InboxRound, things bool) {
	if h.subs == nil {
		return
	}
	if !things { // a round of the Feishu chats (a Lark event): the names only, no items read
		list, _, err := h.subs.List(ctx, subscriptions.Filter{})
		if err != nil {
			h.logf("inbox: subscriptions: %v", err)
		}
		for _, s := range list {
			r.Names[s.ID] = s.Label
		}
		return
	}
	srcs, err := h.subs.InboxSources(ctx)
	if err != nil {
		h.logf("inbox: subscriptions: %v", err)
		return
	}
	for _, src := range srcs {
		r.Names[src.ID] = src.Name
		r.Sources = append(r.Sources, assistantwire.InboxSource{ID: src.ID, Name: src.Name, Extension: true, Kinds: src.Kinds,
			Things: src.Things})
	}
}

// subscriptionArchive is a subscription as the knowledge base reads a connector's archive.
type subscriptionArchive struct {
	subs *subscriptions.Service
	id   string
}

func (a subscriptionArchive) Changes(ctx context.Context, cursor string, limit int) ([]connector.ChatBatch, string, bool, error) {
	return a.subs.Changes(ctx, a.id, cursor, limit)
}

// forgetSubscription removes what the Inbox and the knowledge base took from a subscription (a delete with its data,
// which switched it off first). Both parts are asked; either failing leaves the subscription for another delete.
func (h *Manager) forgetSubscription(ctx context.Context, s subscriptions.Subscription) error {
	f, kerr := h.forgetArchive(ctx, s.ID)
	n, ierr := h.forgetInbox(ctx, s.ID)
	if err := errors.Join(kerr, ierr); err != nil {
		if kbOffline(kerr) || kbOffline(ierr) || errors.Is(ierr, errAssistantOffline) {
			return &subscriptions.Error{Status: http.StatusServiceUnavailable, Code: assistantwire.CodeAssistantOffline,
				Message: "the assistant is away: its Inbox items and knowledge-base chats could not be deleted yet"}
		}
		return err
	}
	h.logf("subscription %s: deleted %d chat(s) from the knowledge base and %d Inbox item(s)", s.ID, f.Chats, n)
	return nil
}

// subscriptionModel makes the drafting calls on the purpose subscription.recipe, through the assistant
// (models.purpose_complete): the manager never holds a model's key.
type subscriptionModel struct{ h *Manager }

// purposeModelInfo is what a model purpose runs on now, as the assistant resolves it (models.usable): not usable when
// it cannot be asked.
func (h *Manager) purposeModelInfo(ctx context.Context, purpose string) subscriptions.ModelInfo {
	var res struct {
		Usable   bool   `json:"usable"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := h.callAssistant(ctx, assistantwire.MethodModelsUsable, map[string]any{"purpose": purpose}, &res); err != nil || !res.Usable {
		return subscriptions.ModelInfo{}
	}
	return subscriptions.ModelInfo{OK: true, Provider: res.Provider, Model: res.Model}
}

// subscriptionModelWait bounds one drafting call, its second try for the schema included.
const subscriptionModelWait = 60

func (m subscriptionModel) JSON(ctx context.Context, c subscriptions.ModelCall) (json.RawMessage, error) {
	turns := make([]assistantwire.ModelMessage, len(c.Turns))
	for i, t := range c.Turns {
		turns[i] = assistantwire.ModelMessage{Role: t.Role, Content: t.Content}
	}
	var out assistantwire.ModelCompleteResult
	err := m.h.callAssistant(ctx, assistantwire.MethodModelsPurposeComplete, assistantwire.PurposeComplete{
		Purpose: assistantwire.PurposeSubscriptionRecipe,
		Params: assistantwire.ModelCompleteParams{System: c.System, Messages: turns, MaxTokens: c.MaxTokens, JSONSchema: c.Schema,
			TimeoutSeconds: subscriptionModelWait, Label: c.Label},
	}, &out)
	var re *rpc.Error
	switch {
	case errors.As(err, &re) && re.Code == assistantwire.CodeModelUnavailable:
		return nil, subscriptions.ErrNoModel
	case errors.As(err, &re) && re.Code == assistantwire.CodeModelPaused:
		var p assistantwire.ModelPause
		_ = json.Unmarshal(re.Data, &p)
		e := &subscriptions.Error{Status: http.StatusServiceUnavailable, Code: subscriptions.CodeModelFailed,
			Message: "The model's connection is held for now; try again later."}
		if until, perr := time.Parse(time.RFC3339, p.Until); perr == nil {
			e.RetryAfter = max(1, time.Until(until).Seconds())
		}
		return nil, e
	case err != nil:
		return nil, err
	case len(out.JSON) == 0:
		return nil, errors.New("the reply has no JSON")
	}
	return out.JSON, nil
}

// Subscriptions is the service, for the declarative pages (#2041) and tests; nil before the manager runs.
func (h *Manager) Subscriptions() *subscriptions.Service { return h.subs }

// subscriptionConnectors finds the registered connectors and the extension that brings each.
type subscriptionConnectors struct{ h *Manager }

// info is c as subscriptions see it: its own status only (a list of every connector's would ask each of them).
func (sc subscriptionConnectors) info(ctx context.Context, c connector.Connector) subscriptions.ConnectorInfo {
	r := sc.h.connectors
	out := subscriptions.ConnectorInfo{ID: c.ID(), Conn: c, Status: connector.StatusOf(ctx, c).State, Enabled: r.Enabled(c.ID()),
		Direct: subscriptions.Direct{
			Inbox:   c.Manifest().Has(connector.CapInbox) && r.Feeds(c.ID(), connector.FeedInbox),
			Archive: c.Manifest().Has(connector.CapArchive) && r.Feeds(c.ID(), connector.FeedArchive),
		}}
	if hp := sc.h.extensions.ownerOf(c); hp != nil {
		out.Extension = hp.id
	}
	return out
}

func (sc subscriptionConnectors) Find(ctx context.Context, id string) (subscriptions.ConnectorInfo, bool) {
	c := sc.h.connectors.Get(id)
	if c == nil {
		return subscriptions.ConnectorInfo{}, false
	}
	return sc.info(ctx, c), true
}

// All is every registered connector without asking any for its status: the summary needs only who brings it.
func (sc subscriptionConnectors) All(ctx context.Context) []subscriptions.ConnectorInfo {
	var out []subscriptions.ConnectorInfo
	for _, c := range sc.h.connectors.All() {
		info := subscriptions.ConnectorInfo{ID: c.ID(), Conn: c, Enabled: sc.h.connectors.Enabled(c.ID())}
		if hp := sc.h.extensions.ownerOf(c); hp != nil {
			info.Extension = hp.id
		}
		out = append(out, info)
	}
	return out
}

func (h *Manager) subscriptionRoutes(mux *http.ServeMux) {
	api := subscriptionMux{mux, h}
	api.HandleFunc("GET /api/v1/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		list, features, err := h.subs.List(r.Context(), subscriptions.Filter{Connector: r.URL.Query().Get("connector"),
			Extension: r.URL.Query().Get("extension")})
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"subscriptions": list, "features": features})
	})
	api.HandleFunc("GET /api/v1/subscriptions/summary", func(w http.ResponseWriter, r *http.Request) {
		lines, err := h.subs.Summary(r.Context())
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"connectors": lines})
	})
	api.HandleFunc("GET /api/v1/subscriptions/catalog", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("connector")
		if id == "" { // every connector's, from what was read before: never a call to a connector
			list, err := h.subs.Catalogs(r.Context())
			if err != nil {
				writeSubscriptionErr(w, err)
				return
			}
			writeJSON(w, map[string]any{"catalogs": list})
			return
		}
		c, err := h.subs.Catalog(r.Context(), id)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, c)
	})
	api.HandleFunc("GET /api/v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		s, runs, err := h.subs.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, map[string]any{"subscription": s, "runs": runs})
	})
	// The pages (#2041): every device may draw them; the actions they carry check full access themselves.
	api.HandleFunc("POST /api/v1/subscriptions/-/ui", h.subscriptionUI)
	api.HandleFunc("POST /api/v1/subscriptions/-/dry-run", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, _ subscriptions.Actor) {
		var in subscriptions.DryRunRequest
		if !readSubscriptionJSON(w, r, &in) {
			return
		}
		dr, err := h.subs.DryRun(r.Context(), in)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, dr)
	}))
	api.HandleFunc("POST /api/v1/subscriptions", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, by subscriptions.Actor) {
		var in subscriptions.CreateRequest
		if !readSubscriptionJSON(w, r, &in) {
			return
		}
		s, err := h.subs.Create(r.Context(), in, by)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(s)
	}))
	api.HandleFunc("PUT /api/v1/subscriptions/{id}", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, by subscriptions.Actor) {
		var in subscriptions.Patch
		if !readSubscriptionJSON(w, r, &in) {
			return
		}
		s, err := h.subs.Update(r.Context(), r.PathValue("id"), in, by)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, s)
	}))
	// Drafting a recipe with the model (#2040): {id} regenerates and saves the subscription; "-" drafts for
	// {connector, source} and writes nothing.
	api.HandleFunc("POST /api/v1/subscriptions/{id}/generate", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, by subscriptions.Actor) {
		in := subscriptions.GenerateRequest{}
		if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in); err != nil && !errors.Is(err, io.EOF) { // a body is optional
			writeErrCode(w, http.StatusBadRequest, subscriptions.CodeBadRequest, err)
			return
		}
		if id := r.PathValue("id"); id != "-" {
			in.ID, in.Connector, in.Source = id, "", ""
		}
		out, err := h.subs.Generate(r.Context(), in, by)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, out)
	}))
	api.HandleFunc("POST /api/v1/subscriptions/{id}/run", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, by subscriptions.Actor) {
		s, err := h.subs.RunNow(r.Context(), r.PathValue("id"), by)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(s)
	}))
	// A device's own items (#3848): the iOS Shortcuts action hands them in, and its push source is made on the first
	// push (no items: only the source, added by hand).
	api.HandleFunc("POST /api/v1/subscriptions/-/push", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, by subscriptions.Actor) {
		if principalOf(r.Context()).ID == "" {
			writeErrCode(w, http.StatusBadRequest, subscriptions.CodeBadRequest, errors.New("a push comes from a paired device"))
			return
		}
		var in subscriptions.PushRequest
		if !readSubscriptionJSON(w, r, &in) {
			return
		}
		out, err := h.subs.Push(r.Context(), by, in)
		if err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		writeJSON(w, out)
	}))
	api.HandleFunc("DELETE /api/v1/subscriptions/{id}", h.subscriptionChange(func(w http.ResponseWriter, r *http.Request, by subscriptions.Actor) {
		data, _ := strconv.ParseBool(r.URL.Query().Get("data"))
		if err := h.subs.Delete(r.Context(), r.PathValue("id"), data, by); err != nil {
			writeSubscriptionErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}

// subscriptionChange lets only full-access devices through; the others can read but not change (or dry-run, which
// calls the source).
func (h *Manager) subscriptionChange(fn func(http.ResponseWriter, *http.Request, subscriptions.Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principalOf(r.Context())
		if p.Role != roleFull {
			writeErrCode(w, http.StatusForbidden, subscriptions.CodeReadOnlyDevice, errors.New("this device can read subscriptions but not change them"))
			return
		}
		fn(w, r, subscriptions.Actor{ID: actorOf(r.Context()), Name: p.Name})
	}
}

func readSubscriptionJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(v); err != nil {
		writeErrCode(w, http.StatusBadRequest, subscriptions.CodeBadRequest, err)
		return false
	}
	return true
}

// writeSubscriptionErr writes the service's error as the API's usual body ({error, code}) with what it carries: the
// dry run that refused a recipe, the subscription as it is now on a conflict, how long to wait on rate_limited.
func writeSubscriptionErr(w http.ResponseWriter, err error) {
	var e *subscriptions.Error
	if !errors.As(err, &e) {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	body := map[string]any{"error": e.Message, "code": e.Code}
	if e.DryRun != nil {
		body["dry_run"] = e.DryRun
	}
	if e.Current != nil {
		body["subscription"] = e.Current
	}
	if e.RetryAfter > 0 {
		body["retry_after"] = e.RetryAfter
	}
	if e.Draft != nil {
		body["recipe"] = e.Draft
	}
	writeErrorBody(w, e.Status, body)
}

// subscriptionMux answers 503 while the service is not made yet (a manager that is starting, or handlers in tests
// registered without Run).
type subscriptionMux struct {
	mux *http.ServeMux
	h   *Manager
}

func (m subscriptionMux) HandleFunc(pattern string, fn http.HandlerFunc) {
	m.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if m.h.subs == nil {
			writeErrCode(w, http.StatusServiceUnavailable, "unavailable", errors.New("subscriptions are not running yet"))
			return
		}
		fn(w, r)
	})
}
