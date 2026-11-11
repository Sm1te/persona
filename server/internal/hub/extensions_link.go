package hub

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The extensions, the connectors and the bots are the manager's (internal/extmgr, docs/extensions/extensions.md "Who owns what"): the
// registry, the programs, their secrets and tokens, every source, mail, the Feishu and WeChat bots. The hub relays:
//
//   - the apps' requests on their routes (extwire.Routes, the admin UI's /api/v1/admin/extensions…, and the parts of
//     the API the running extensions serve, the report's APIs) go to the manager over a relay stream as they are, so
//     an extension's SSE or WebSocket streams as it did; who asked goes with them (extwire.Header*);
//   - an extension's token on one of the hub's own routes is the manager's to judge (extensions.authorize);
//   - what the hub's own features need of them (the MCP tools of connectors, Cua and ego, a voice extension's
//     models, a quota, the presets) is asked over the bus (extensions.hub_call);
//   - what it shows of them (features, the extensions' prefixes, Cua's and ego's state on each machine) is the
//     manager's last report (extensions.report), kept while the manager is away, as are the last answers of the GET
//     routes the apps draw from (extwire.KeptRoutes).
//
// While the manager is away the routes answer 503 assistant_offline (the kept GETs their last answer).

// extLink is what the hub keeps of the manager's extensions.
type extLink struct {
	report atomic.Pointer[extwire.Report] // the assistant's
	// reports are the other managers' (manager_state.go).
	reportsMu sync.Mutex
	reports   map[string]*extwire.Report
	keptMu    sync.Mutex
	kept      map[string]keptAnswer // GET path?query → its last answer
	// calls are the requests being relayed, by caller, so a revoked device's end (extension_calls.go).
	calls relayedCalls
}

type keptAnswer struct {
	header http.Header
	body   []byte
}

// keptMost bounds a kept answer.
const keptMost = 4 << 20

func init() {
	handleBusEvent(extwire.EventReport, func(h *Hub, c *busConn, params json.RawMessage) {
		var r extwire.Report
		if err := json.Unmarshal(params, &r); err != nil {
			h.logf("bus: [%s] extensions.report: %v", c.ident.ID, err)
			return
		}
		// Each manager's report is its own; what the hub shows is all of them merged (manager_state.go).
		from := c.managerID()
		before := h.extReport()
		old := h.keepExtReport(from, &r)
		var oldBots []extwire.BotIdentity
		if old != nil {
			oldBots = old.BotIdentities
		}
		if !slices.Equal(oldBots, r.BotIdentities) {
			h.managersChanged()
		}
		after := h.extReport()
		h.errorReportsChanged() // error_reports came or went (error_reports.go)
		if old == nil || !slices.Equal(before.Features, after.Features) || !machineExtrasEqual(before.Machines, after.Machines) {
			h.broadcastMachines()
		}
		h.looksReported(before.Looks, after.Looks)               // the extensions' looks (looks.go)
		if h.store != nil && from == assistantwire.AssistantID { // kept across a restart: the assistant's
			if err := h.store.PutSetting(context.Background(), extReportKey, r); err != nil {
				h.logf("extensions: keep the report: %v", err)
			}
		}
	})
}

func init() {
	handleBusEvent(extwire.EventLooksGone, func(h *Hub, _ *busConn, params json.RawMessage) {
		var g extwire.LooksGone
		if json.Unmarshal(params, &g) == nil {
			go h.looksGone(context.Background(), g.IDs)
		}
	})
}

// extReportKey keeps the manager's last report across a hub restart (the hub's own key).
const extReportKey = "hub.extensions_report"

// loadExtReport is the report kept before the hub restarted, until the manager reports again.
func (h *Hub) loadExtReport(ctx context.Context) {
	if h.store == nil {
		return
	}
	var r extwire.Report
	if ok, err := h.store.Setting(ctx, extReportKey, &r); err == nil && ok {
		h.ext.report.CompareAndSwap(nil, &r)
	}
	h.errorReportsChanged()
}

func machineExtrasEqual(a, b map[string]extwire.MachineExtras) bool {
	if len(a) != len(b) {
		return false
	}
	for k, x := range a {
		y, ok := b[k]
		if !ok || string(x.Computer) != string(y.Computer) || string(x.Browser) != string(y.Browser) || string(x.IPhone) != string(y.IPhone) ||
			x.ScreenPort != y.ScreenPort {
			return false
		}
	}
	return true
}

// features are the hub's features with those of the extensions serving part of the API.
func (h *Hub) features() []string {
	out := append(slices.Clone(proto.Features), featureSessionsList, featureSessionsLookup, featureSessionsPaneSource, featureSessionsManager, featureSessionsPageCounts, featureMachineRename)
	out = append(out, h.agentSessionArchiveFeatures()...)
	out = append(out, h.meetingAttendanceFeatures()...)
	out = append(out, h.meetingConversationFeatures()...)
	out = append(out, h.meetingPhotosFeatures()...)
	// The assistant's card interfaces that answer now (docs/assistant/assistant-views.md): assistant_views is declarative actions
	// (expected_version), live cards and transcript cards; assistant_html_bridge is …/html-actions with both switches
	// on. Neither says a client may run HTML: that is each platform's own security sign-off, else fallback.
	if views, bridge := h.assistantCards(); views {
		out = append(out, "assistant_views")
		if bridge {
			out = append(out, "assistant_html_bridge")
		}
	}
	for _, f := range h.extReport().Features {
		switch f {
		case featureAgentSessionArchives, featureAgentSessionArchiveClose, featureMeetingAttendance, featureMeetingConversation,
			featureMeetingPhotos:
			continue // only manager hello can promise fleet archive support and the meeting features
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// extrasOf is machine m's Cua, ego and iPhone state as the manager reports it (each nil when not on), read from one
// report: extReport merges every manager's report each time it is called.
func (h *Hub) extrasOf(m *Machine) (*proto.Computer, *proto.Browser, *proto.IPhone) {
	x, ok := h.extReport().Machines[m.cfg.ID]
	if !ok {
		return nil, nil, nil
	}
	return decodeExtra[proto.Computer](x.Computer), decodeExtra[proto.Browser](x.Browser), decodeExtra[proto.IPhone](x.IPhone)
}

func decodeExtra[T any](raw json.RawMessage) *T {
	if len(raw) == 0 {
		return nil
	}
	var v T
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return &v
}

// screenAllowed: the app may open m's screen (cua or ego is on for it).
func (h *Hub) screenAllowed(m *Machine) bool { return h.extReport().Machines[m.cfg.ID].ScreenPort }

// extCall asks the default manager what the hub's own features need of the extensions (extwire.Hub*).
func (h *Hub) extCall(ctx context.Context, method string, params, out any) error {
	return h.extCallTo(ctx, h.defaultOwner(ctx), method, params, out)
}

// extCallTo is extCall of manager id's extensions.
func (h *Hub) extCallTo(ctx context.Context, id, method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return h.callBus(ctx, id, extwire.MethodHubCall, extwire.HubCall{Method: method, Params: raw}, out)
}

// emitToExtensions tells manager owner's extensions something the hub saw about one of its sessions (agent.finished,
// agent.needs_you): only the session's manager's, never another's (ownership.go).
func (h *Hub) emitToExtensions(owner, event string, params any) {
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	_ = h.notifyBus(owner, extwire.EventEmit, extwire.Emit{Event: event, Params: raw})
}

// ---- the routes ----

// extensionRoutesRelayed relays the apps' requests on the manager's routes.
func (h *Hub) extensionRoutesRelayed(api *http.ServeMux) {
	for _, pattern := range extwire.Routes {
		relay := func(w http.ResponseWriter, r *http.Request) { h.relayToManager(w, r, false) }
		if pattern == "/api/v1/agents/" { // messages to the personal Agents (terminal_controls.go)
			relay = h.withoutTerminalControls(relay)
		}
		api.HandleFunc(pattern, relay)
	}
	// The lists: every manager's extensions and sources (ownership_lists.go).
	for pattern, key := range mergedLists {
		if p, ok := strings.CutPrefix(pattern, "GET "); ok && slices.Contains(extwire.Routes, p) {
			api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				h.mergeList(w, r, key, func(w http.ResponseWriter, r *http.Request, id string) { h.relayTo(w, r, false, id) })
			})
		}
	}
}

// adminExtensionRoutes relays the admin UI's extension pages (a signed-in admin session).
func (h *Hub) adminExtensionRoutes(mux *http.ServeMux) {
	for _, pattern := range []string{"/api/v1/admin/extensions", "/api/v1/admin/extensions/", "/api/v1/admin/plugins", "/api/v1/admin/plugins/"} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { h.relayToManager(w, r, true) })
	}
}

// adminUploadPath is where the browser uploads a package (its body is not JSON); legacyAdminUploadPath before #1844.
const (
	adminUploadPath       = "/api/v1/admin/extensions/upload"
	legacyAdminUploadPath = "/api/v1/admin/plugins/upload"
)

// extensionAPIs serves the requests for an extension's part of the API (the report's prefixes): before the hub's own
// routes for a prefix being moved out of the hub (migratablePrefixes: the extension's wins), after them otherwise (an
// extension cannot take the hub's own API). A send it declared a receipt for keeps the hub's receipt, like the hub's
// own sends (GET /api/v1/ops/{id}).
func (h *Hub) extensionAPIs(next http.Handler, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		seg, under, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
		var api *extwire.APIPrefix
		rep := h.extReport()
		for i := range rep.APIs {
			if rep.APIs[i].Prefix == seg {
				api = &rep.APIs[i]
				break
			}
		}
		if api == nil || (!slices.Contains(migratablePrefixes, seg) && hubOwns(mux, seg)) {
			next.ServeHTTP(w, r)
			return
		}
		// The manager whose extension serves the prefix answers, unless the request names another (or is an extension's:
		// its own manager's, impliedManager). Served by several: the named one, the default, the built-in, the others
		// by id (extPrefixOwner).
		relay := func(w http.ResponseWriter, r *http.Request) {
			named, err := impliedManager(r)
			if err != nil {
				writeErrCode(w, http.StatusForbidden, codeOtherManager, err)
				return
			}
			if id := h.extPrefixOwner(seg, named, h.defaultOwner(r.Context())); id != "" && (named == "" || id == named) {
				h.relayTo(w, r, false, id)
				return
			}
			h.relayToManager(w, r, false)
		}
		for _, rc := range api.Receipts {
			if rc.Matches(r.Method, "/"+under) {
				h.withReplyReceipt(rc.Kind, relay)(w, r)
				return
			}
		}
		relay(w, r)
	})
}

// migratablePrefixes are the hub's own prefixes an extension may take over while they move out of the hub.
var migratablePrefixes = []string{"lark"}

// hubOwns says whether the hub has any route under /api/v1/<prefix>: probed once per prefix.
func hubOwns(mux *http.ServeMux, prefix string) bool {
	if v, ok := ownedPrefixes.Load(prefix); ok {
		return v.(bool)
	}
	owned := false
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, p := range []string{"/api/v1/" + prefix, "/api/v1/" + prefix + "/", "/api/v1/" + prefix + "/x", "/api/v1/" + prefix + "/x/y"} {
			req, _ := http.NewRequest(method, p, nil)
			if _, pattern := mux.Handler(req); pattern != "" {
				owned = true
			}
		}
	}
	ownedPrefixes.Store(prefix, owned)
	return owned
}

var ownedPrefixes sync.Map

// relayToManager is relayTo the manager r is for: its machine's ({mid}), else the one it names, else the default.
func (h *Hub) relayToManager(w http.ResponseWriter, r *http.Request, admin bool) {
	by := h.byRequest
	if r.PathValue("mid") != "" {
		by = h.byMachine
	}
	if id, ok := h.owner(w, r, by); ok {
		h.relayTo(w, r, admin, id)
	}
}

// relayTo passes r on to manager id over a relay stream and streams its answer back. Who asked goes with it in the
// hub's headers (any the client sent are dropped); the hub's credentials stay with the hub. While the manager is
// away: 503 assistant_offline, or the last answer of a kept GET.
func (h *Hub) relayTo(w http.ResponseWriter, r *http.Request, admin bool, id string) {
	guardedApps, guardedRevoke, ok := computerAppsPrecondition(w, r, id)
	if !ok {
		return
	}
	keep := r.Method == http.MethodGet && slices.Contains(extwire.KeptRoutes, r.URL.Path)
	key := r.URL.Path + "?" + r.URL.RawQuery
	if id != devicesOwner {
		key = id + " " + key // each manager's own (the carrier's keys as before)
	}
	if h.busConnOf(id) == nil && (id != devicesOwner || !h.awaitManager(r.Context(), linkManagerWait)) {
		if keep && h.writeKept(w, key) {
			return
		}
		writeErrCode(w, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
		return
	}
	// Who asked, as the hub's auth found it. Set on the outgoing request in Rewrite, after the proxy has dropped the
	// hop-by-hop headers: set on r, a client's "Connection: X-Persona-…" would make the proxy drop them again.
	p := principalOf(r.Context())
	who := http.Header{}
	set := func(k, v string) {
		if v != "" {
			who.Set(k, v)
		}
	}
	set(extwire.HeaderHub, h.hubID())
	set(extwire.HeaderDevice, p.ID)
	set(extwire.HeaderDeviceName, p.Name)
	set(extwire.HeaderRole, p.Role)
	set(extwire.HeaderExtension, p.Extension)
	set(extwire.HeaderScopes, strings.Join(p.Scopes, ","))
	set(extwire.HeaderLang, string(h.requestLang(r)))
	set(extwire.HeaderRemote, remoteIP(r).String())
	if p.PairHash != "" {
		set(extwire.HeaderPaired, "1")
	}
	if h.fromThisMachine(r) {
		set(extwire.HeaderOwner, "1")
	}
	if admin {
		set(extwire.HeaderAdmin, "1")
	}
	// Kept under its caller's credential: revoking a device ends its WebSockets and streams (extension_calls.go).
	r, done := h.trackRelayed(r)
	defer done()
	var rec *keptRecorder
	if keep {
		rec = &keptRecorder{ResponseWriter: w, status: http.StatusOK}
		w = rec
	}
	// What may change the manager's inbox, as in relayHTTP: before it goes, and when it is answered (inbox_answers.go).
	changes := r.Method != http.MethodGet && r.Method != http.MethodHead
	if changes {
		h.inboxAnswers.changed(id)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "manager"
			pr.Out.Host = "manager"
			if guardedRevoke {
				// A legacy manager receives its existing endpoint only after this hub enforced the owner.
				segments := strings.SplitN(pr.Out.URL.EscapedPath(), "/", 8)
				segments[7] = strings.TrimPrefix(segments[7], "revoke/")
				pr.Out.URL.RawPath = strings.Join(segments, "/")
				pr.Out.URL.Path, _ = url.PathUnescape(pr.Out.URL.RawPath)
			}
			for k := range pr.Out.Header {
				if strings.HasPrefix(strings.ToLower(k), "x-persona-") {
					pr.Out.Header.Del(k)
				}
			}
			for k, v := range who {
				pr.Out.Header[k] = v
			}
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			if q := pr.Out.URL.Query(); q.Has("token") { // a WebSocket's credential in the query: the hub's too
				q.Del("token")
				pr.Out.URL.RawQuery = q.Encode()
			}
			pr.Out.Header["X-Forwarded-For"] = nil
		},
		ModifyResponse: func(res *http.Response) error {
			if changes {
				h.inboxAnswers.changed(id)
			}
			if apps, _ := computerAppsRoute(r); apps {
				res.Header.Del(computerAppsOwnerHeader)
				if guardedApps && res.StatusCode >= 200 && res.StatusCode < 300 {
					res.Header.Set(computerAppsOwnerHeader, id)
				}
			}
			return nil
		},
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return h.dialManager(ctx, id)
		}, DisableKeepAlives: true, ResponseHeaderTimeout: 5 * time.Minute},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if changes {
				h.inboxAnswers.changed(id)
			}
			if errors.Is(err, errBusOffline) || h.busConnOf(id) == nil {
				writeErrCode(w, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
				return
			}
			writeErr(w, http.StatusBadGateway, err)
		},
	}
	proxy.ServeHTTP(w, r)
	if rec != nil && rec.status == http.StatusOK && !rec.over {
		h.ext.keptMu.Lock()
		if h.ext.kept == nil {
			h.ext.kept = map[string]keptAnswer{}
		}
		h.ext.kept[key] = keptAnswer{header: rec.Header().Clone(), body: rec.body}
		h.ext.keptMu.Unlock()
	}
}

// fromThisMachine says whether r carries the master token over loopback: installing runs code on this machine, so
// only its owner there (`personad extension`) may.
func (h *Hub) fromThisMachine(r *http.Request) bool {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return remoteIP(r).IsLoopback() && tok != "" && h.cfg != nil && tok == h.cfg.Token
}

// writeKept answers with the last answer kept for key; false when there is none.
func (h *Hub) writeKept(w http.ResponseWriter, key string) bool {
	h.ext.keptMu.Lock()
	k, ok := h.ext.kept[key]
	h.ext.keptMu.Unlock()
	if !ok {
		return false
	}
	for name, v := range k.header {
		w.Header()[name] = v
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(k.body)
	return true
}

// keptRecorder copies what it writes, to keep it.
type keptRecorder struct {
	http.ResponseWriter
	status int
	body   []byte
	over   bool
}

func (k *keptRecorder) WriteHeader(code int) { k.status = code; k.ResponseWriter.WriteHeader(code) }

func (k *keptRecorder) Write(p []byte) (int, error) {
	if !k.over {
		if len(k.body)+len(p) > keptMost {
			k.over, k.body = true, nil
		} else {
			k.body = append(k.body, p...)
		}
	}
	return k.ResponseWriter.Write(p)
}

// Flush goes through http.ResponseController, as codeWriter.Flush does.
func (k *keptRecorder) Flush() { _ = http.NewResponseController(k.ResponseWriter).Flush() }

func (k *keptRecorder) Unwrap() http.ResponseWriter { return k.ResponseWriter }

// dialManager opens a relay stream manager id serves HTTP on (extwire.MethodAccept).
func (h *Hub) dialManager(ctx context.Context, id string) (net.Conn, error) {
	if h.busConnOf(id) == nil {
		return nil, errBusOffline
	}
	taken, err := h.handToManagerVia(ctx, id, extwire.MethodAccept, nil)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithCancel(context.Background())
	nc := websocket.NetConn(cctx, taken.conn, websocket.MessageBinary)
	return &relayConn{Conn: nc, done: func() {
		cancel()
		close(taken.done)
	}}, nil
}

// relayConn ends its relay when it is closed.
type relayConn struct {
	net.Conn
	once sync.Once
	done func()
}

func (c *relayConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.done)
	return err
}

// pushPayloadAllowed: a push manager from's extensions ask for names only its own machine and session (machine_id,
// session_id: ownsRef), and only a pending card of its own (interaction_id), so it cannot look like another manager's
// question and get the user's answer from the notification.
func (h *Hub) pushPayloadAllowed(from string, payload map[string]any) error {
	machine, _ := payload["machine_id"].(string)
	session, _ := payload["session_id"].(string)
	ix, _ := payload["interaction_id"].(string)
	if !h.ownsRef(from, machine, session) {
		return &rpc.Error{Code: devicewire.CodeNoDevice, Message: "a push about another manager's machine or session"}
	}
	if card, ok := h.pendingInteraction(ix); ix != "" && (!ok || cmp.Or(card.Manager, assistantwire.AssistantID) != from) {
		return &rpc.Error{Code: devicewire.CodeNoDevice, Message: "a push about another manager's card"}
	}
	return nil
}

// localizedPayload is the payload of an extension's push.localized for a device in lang. One it left lang out of
// gets the one its text would fall back to (i18n.T: the fallback language, then the default), else the first by
// language code, so the pick does not change between pushes. nil: no language has one.
func localizedPayload(payloads map[string]map[string]any, lang i18n.Lang) map[string]any {
	for _, l := range []i18n.Lang{lang, i18n.Fallback, i18n.Default} {
		if pl := payloads[string(l)]; len(pl) > 0 {
			return pl
		}
	}
	for _, l := range slices.Sorted(maps.Keys(payloads)) {
		if pl := payloads[l]; len(pl) > 0 {
			return pl
		}
	}
	return nil
}

// ---- an extension's token on the hub's own routes ----

// extTokenPrefix starts every extension's token (the manager makes them).
const extTokenPrefix = "pext_"

// roleExtension is an extension's token.
const roleExtension = "extension"

// authorizeExtension asks the manager whose token tok is and whether it opens r's route on the hub's mux: the
// principal (ok false: no such token), and when it does not open the route, the scope it lacks (opens false).
//
// sure (when ok is false): every manager asked answered that the token is not its own. A manager that timed out or
// is not ready yet gave no verdict, and neither then does this; one not on the bus is not asked (listManagers).
func (h *Hub) authorizeExtension(ctx context.Context, tok string, r *http.Request) (p principal, ok, opens bool, scope string, sure bool) {
	pattern, method, path := "", "", ""
	if h.apiMux != nil && r != nil {
		_, pattern = h.apiMux.Handler(r)
		// The escaped path: the one relayHTTP passes on and the manager serves. Decoded, an escaped dot segment would
		// be cleaned away and the check made on another route than the one the request reaches (#5182).
		method, path = r.Method, r.URL.EscapedPath()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The token is one manager's extension's: each is asked, the default first, until one knows it.
	var a extwire.Authorized
	manager := ""
	sure = true
	asked := time.Now()
	for _, id := range h.listManagers(ctx) {
		a = extwire.Authorized{}
		err := h.callBus(ctx, id, extwire.MethodAuthorize, extwire.Authorize{Hash: hashToken(tok), Pattern: pattern,
			Method: method, Path: path}, &a)
		if err == nil && a.Extension != "" {
			manager = id
			break
		}
		if err != nil && !errors.Is(err, errBusOffline) { // (the default manager, listed first, may be off the bus)
			sure = false
		}
	}
	if a.Extension == "" {
		return principal{}, false, false, "", sure
	}
	p = principal{ID: "extension:" + a.Extension, Name: a.Name, Role: roleExtension, Extension: a.Extension, Scopes: a.Scopes,
		Manager: manager, extHash: hashToken(tok), extAt: asked}
	return p, true, a.OK, a.Scope, true
}

// tokenHash says whether s is a hash as hashToken makes one.
func tokenHash(s string) bool {
	if len(s) != 2*sha256.Size {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// answerOnly says whether the request comes from an answer-only device (a watch).
func answerOnly(r *http.Request) bool { return principalOf(r.Context()).Role == roleAnswer }

// ---- what the manager asks of the hub (app.call hub.*) ----

// managerCollapseID is the apns-collapse-id of a push a manager asked for with collapse id id: the same id from the same
// manager still replaces its earlier push, but no manager's id replaces another manager's push, one the hub named (a
// to-do's, its update notice) or a notify API caller's, and any id fits APNs' 64 bytes. An empty id stays empty: a push
// of its own.
func managerCollapseID(manager, id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.New()
	sum.Write(binary.AppendUvarint(nil, uint64(len(manager))))
	sum.Write([]byte(manager))
	sum.Write([]byte(id))
	return "ext:" + hex.EncodeToString(sum.Sum(nil)[:16])
}

func (h *Hub) extAppCall(ctx context.Context, from string, p assistantwire.AppCall) (json.RawMessage, bool, error) {
	if p.AppID != extwire.AppHub {
		return nil, false, nil
	}
	var out any
	var err error
	var a struct {
		Interaction   proto.Interaction         `json:"interaction"`
		InteractionID string                    `json:"interaction_id"`
		Resolution    proto.Resolution          `json:"resolution"`
		Payload       map[string]any            `json:"payload"`
		Payloads      map[string]map[string]any `json:"payloads"`
		CollapseID    string                    `json:"collapse_id"`
		Devices       []string                  `json:"devices"`
		Purpose       string                    `json:"purpose"`
		Name          string                    `json:"name"`
		Machine       string                    `json:"machine"`
		Sender        string                    `json:"sender"`
		Request       json.RawMessage           `json:"request"`
		Hash          string                    `json:"hash"`
	}
	if len(p.Params) > 0 {
		if err := json.Unmarshal(p.Params, &a); err != nil {
			return nil, true, err
		}
	}
	switch p.Method {
	case extwire.HubPushLocalized:
		if localizedPayload(a.Payloads, i18n.Default) == nil {
			return nil, true, errors.New("hub: push.localized without a payload in any language")
		}
		for _, pl := range a.Payloads {
			if err := h.pushPayloadAllowed(from, pl); err != nil {
				return nil, true, err
			}
		}
		// the manager's extension's: not to the devices that turned that manager's pushes off
		h.push.sendFrom(from, func(l i18n.Lang) map[string]any { return localizedPayload(a.Payloads, l) }, managerCollapseID(from, a.CollapseID), nil, quietNone, nil)
		out = map[string]any{}
	case extwire.HubPushBackground:
		if err := h.pushPayloadAllowed(from, a.Payload); err != nil {
			return nil, true, err
		}
		h.push.sendAs(a.Payload, "", "background", "5", func(d Device) bool { return slices.Contains(a.Devices, d.DeviceID) })
		out = map[string]any{}
	case extwire.HubMailTargets:
		out = map[string]any{"targets": h.push.mailTargets()}
	case extwire.HubTokenRevoked:
		if !tokenHash(a.Hash) {
			return nil, true, errors.New("hub: extensions.token_revoked without a token's hash")
		}
		h.dropRevokedToken(from, a.Hash)
		out = map[string]any{}
	default:
		return nil, true, errors.New("hub: no method " + p.Method)
	}
	if err != nil {
		return nil, true, err
	}
	raw, err := json.Marshal(out)
	return raw, true, err
}
