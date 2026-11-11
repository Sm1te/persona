package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extension/uistream"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// How the manager runs this code: Run starts what the hub used to start (hub.go's start, in the same order), the
// routes are served over the hub's relay (Accept), the hub's calls and events come in through Request and Event, and
// the assistant's app.calls that are this code's through AppCall.

// Run starts the extensions, connectors and bots and runs their loops until ctx ends; then it stops the programs.
func (h *Manager) Run(ctx context.Context) error {
	h.ctx = ctx
	if ms, ok := h.store.(mailStore); ok && NewMail != nil {
		var err error
		if h.mail, err = NewMail(h.cfg.dir, h.secretKeyPath(), ms, h.cfg.Google, h.cfg.Microsoft); err != nil {
			h.logf("mail disabled: %v", err)
			h.mail = nil
		}
	}
	h.initAgents()
	h.startConnectorGate(ctx) // the connectors' registry, with the gate's rules: connector_gate and the user's own
	h.migrateWeChatState(ctx) // into the channels' state before the assistant and the WeChat program read it
	h.startBots(ctx)
	go h.agentsLoop(ctx)
	go h.inboxLoop(ctx)
	go h.noLockLoop(ctx)
	go h.extensionLeaseLoop(ctx)
	h.extensions = newExtensions(h.cfg.dir, h.cfg.extensionConfigs(h.logf), h.logf)
	h.extensions.stats.redact = h.redactOwnSecrets
	h.startTokenRecord(ctx) // before any extension starts with a token (extension_scopes.go)
	h.startConnectors(ctx)  // the extensions' connectors join here (attachExtensions)
	h.startExtensions(ctx)
	h.startSubscriptions(ctx) // after the connectors it pulls from
	go h.extensionUpgradeLoop(ctx)
	go h.extensionMetricsLoop(ctx)
	go h.pageLoop(ctx)
	go h.sentLedgerLoop(ctx)
	h.apiMux = http.NewServeMux()
	h.registerRoutes(h.apiMux)
	close(h.started)
	h.sendReport()
	go h.inboxManagerConnected() // the assistant is here: its inbox gets a full round now, as when it connected to the hub
	<-ctx.Done()
	h.pageDropExtension("")
	h.stopExtensions()
	h.pageStopDelivery()
	return nil
}

// stopExtensions stops every program (the manager is going: its children go with it).
func (h *Manager) stopExtensions() {
	ps := h.extensions
	if ps == nil {
		return
	}
	ps.mu.Lock()
	procs := make([]*hubExtension, 0, len(ps.byID))
	for _, hp := range ps.byID {
		procs = append(procs, hp)
	}
	ps.mu.Unlock()
	var wg sync.WaitGroup
	for _, hp := range procs {
		if hp.proc == nil {
			hp.closeIdle()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			hp.proc.Stop()
			hp.closeIdle()
		}()
	}
	wg.Wait()
}

// Started is closed once Run has started everything.
func (h *Manager) Started() <-chan struct{} { return h.started }

// ready waits for Run to have started everything (a call that comes in first, right as the manager starts).
func (h *Manager) ready(ctx context.Context) bool {
	select {
	case <-h.started:
		return true
	case <-ctx.Done():
		return false
	}
}

// routes are the routes the hub relays here (extwire.Routes), as the hub had them.
func (h *Manager) registerRoutes(api *http.ServeMux) {
	h.botRoutes(api)
	h.agentRoutes(api)
	h.connectorRoutes(api)
	h.extensionRoutes(api)
	h.messagingRoutes(api)
	h.adminExtensionRoutes(api) // with the old /api/v1/admin/plugins paths
	h.subscriptionRoutes(api)
	api.HandleFunc("POST /api/v1/error-reports", h.postErrorReports)
	withMachine := func(fn func(http.ResponseWriter, *http.Request, *Machine)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m := h.localMachine(r.PathValue("mid"))
			if m == nil {
				writeErr(w, http.StatusNotFound, errors.New("unknown machine"))
				return
			}
			fn(w, r, m)
		}
	}
	api.HandleFunc("GET /api/v1/machines/{mid}/computer", withMachine(h.getComputer))
	api.HandleFunc("PUT /api/v1/machines/{mid}/computer", withMachine(h.putComputer))
	api.HandleFunc("POST /api/v1/machines/{mid}/computer/{action}", withMachine(h.postComputerJob))
	api.HandleFunc("GET /api/v1/machines/{mid}/computer/clipboard", withMachine(h.computerClipboard))
	api.HandleFunc("POST /api/v1/machines/{mid}/computer/clipboard", withMachine(h.computerClipboard))
	api.HandleFunc("GET /api/v1/machines/{mid}/computer/apps", withMachine(h.getComputerApps))
	api.HandleFunc("DELETE /api/v1/machines/{mid}/computer/apps/{bundle}", withMachine(h.deleteComputerApp))
	api.HandleFunc("DELETE /api/v1/machines/{mid}/computer/apps/revoke/{bundle}", withMachine(h.revokeComputerApp))
	api.HandleFunc("GET /api/v1/machines/{mid}/browser", withMachine(h.getBrowser))
	api.HandleFunc("PUT /api/v1/machines/{mid}/browser", withMachine(h.putBrowser))
	api.HandleFunc("POST /api/v1/machines/{mid}/browser/{action}", withMachine(h.postBrowserJob))
	api.HandleFunc("GET /api/v1/machines/{mid}/browser/cookies", withMachine(h.browserCookies))
	api.HandleFunc("POST /api/v1/machines/{mid}/browser/cookies", withMachine(h.browserCookies))
	api.HandleFunc("DELETE /api/v1/machines/{mid}/browser/cookies", withMachine(h.browserCookies))
	api.HandleFunc("GET /api/v1/machines/{mid}/iphone", withMachine(h.getIPhone))
	api.HandleFunc("PUT /api/v1/machines/{mid}/iphone", withMachine(h.putIPhone))
	api.HandleFunc("POST /api/v1/machines/{mid}/iphone/{action}", withMachine(h.postIPhoneJob))
	api.HandleFunc("GET /api/v1/iphone", h.getIPhoneList)
	api.HandleFunc("PUT /api/v1/iphone/consent", h.putIPhoneConsent)
	if h.mail != nil {
		api.Handle("/api/v1/mail/", h.mail.Handler())
	} else {
		api.HandleFunc("/api/v1/mail/", func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusNotFound, errors.New("mail is not set up"))
		})
	}
}

// ServeHTTP serves one request the hub relayed: who asked is in the hub's headers (principalFrom); the extensions'
// parts of the API (extension_api.go) come before the rest, as at the hub.
func (h *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.serving.Add(1)
	defer h.serving.Add(-1)
	if !h.ready(r.Context()) {
		writeErrCode(w, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
		return
	}
	p := principalFrom(r)
	ctx := withPrincipal(r.Context(), p)
	if i18n.Valid(p.Lang) {
		ctx = i18n.With(ctx, p.Lang)
	}
	r = r.WithContext(ctx)
	w = &localeWriter{ResponseWriter: w, lang: h.requestLang(r)}
	w.Header().Set("Cache-Control", "no-store")
	// The admin UI's routes are for a signed-in admin session only (the hub relays them from its admin mux and says so).
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/") && !p.Admin {
		writeErrCode(w, http.StatusUnauthorized, "login_required", errors.New("sign in to the admin UI"))
		return
	}
	h.extensionAPIs(h.apiMux, h.apiMux).ServeHTTP(w, r)
}

// Serving is how many requests the hub relayed to the extensions are being served now; a WebSocket (a voice call, an
// extension's live view) counts for as long as it is open.
func (h *Manager) Serving() int64 { return h.serving.Load() }

// localeWriter lets apierr translate errors after the hub has relayed the request to this process.
type localeWriter struct {
	http.ResponseWriter
	lang i18n.Lang
}

func (w *localeWriter) Lang() i18n.Lang { return w.lang }

// Unwrap preserves streaming and WebSocket access to the underlying writer.
func (w *localeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Accept is extwire.MethodAccept: the hub holds a request for the manager; it dials the relay with the id and serves
// the request (and any after it on the same connection) there.
func (h *Manager) Accept(ctx context.Context, id string) error {
	if h.opts.DialRelay == nil {
		return errors.New("no relay")
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := h.opts.DialRelay(dctx, url.Values{"id": {id}}.Encode())
	cancel()
	if err != nil {
		return err
	}
	conn.SetReadLimit(64 << 20)
	go func() {
		cctx, cancel := context.WithCancel(context.Background())
		nc := websocket.NetConn(cctx, conn, websocket.MessageBinary)
		// The server reads from a pipe spliced to the relay, not from the relay itself: when a handler takes the
		// connection over (an extension's WebSocket, which the proxy switches to) the server stops its background read
		// with a read deadline in the past, and on a websocket.NetConn that cancels the read for good and closes the
		// relay, so every WebSocket ended right after its 101.
		mine, theirs := net.Pipe()
		go func() {
			splice(theirs, nc)
			// Only now, not when Serve returns: the server's last write to the pipe returns once the splice has read
			// it, before the splice has written it to the relay, and ending nc's context mid-write closes the relay
			// there, losing the answer's tail.
			cancel()
		}()
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 30 * time.Second}
		_ = srv.Serve(&oneConn{c: mine, done: make(chan struct{})})
	}()
	return nil
}

// splice copies a to b and b to a until either ends, then closes both.
func splice(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// oneConn is a listener of one connection: Accept gives it, then waits until it is closed.
type oneConn struct {
	c    net.Conn
	once sync.Once
	took bool
	done chan struct{}
	mu   sync.Mutex
}

func (l *oneConn) Accept() (net.Conn, error) {
	l.mu.Lock()
	if !l.took {
		l.took = true
		l.mu.Unlock()
		return &closeNotify{Conn: l.c, closed: func() { l.Close() }}, nil
	}
	l.mu.Unlock()
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConn) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *oneConn) Addr() net.Addr { return l.c.LocalAddr() }

// closeNotify ends the listener when the server closes the connection.
type closeNotify struct {
	net.Conn
	closed func()
}

func (c *closeNotify) Close() error {
	err := c.Conn.Close()
	c.closed()
	return err
}

// ---- the hub's calls and events ----

// Hello takes manager.hello's answer (and manager.updated's): the owner, the mesh role, the user's language.
func (h *Manager) Hello(res json.RawMessage) {
	var hl assistantwire.Hello
	if json.Unmarshal(res, &hl) != nil {
		return
	}
	was := h.isMember()
	h.hello.Store(&hl)
	h.isMemberNow.Store(hl.Assistant.Standby)
	if was != hl.Assistant.Standby && h.extensions != nil {
		go h.extensions.roleChanged(context.Background()) // a member's center runs what runs once (mesh_group.go)
		go func() {
			h.restartBot() // the center's Feishu bot answers for the group
			h.sendReport()
		}()
	}
}

// BusUp is told each time the connection to the hub is up: the hub (maybe just restarted) gets the report again, and
// the inbox goes round (a full round: the hub may have missed changes). It returns once the report went (or after a
// minute of the extensions not started): what the manager kept while the hub was away goes after it (the hub takes an
// extension's live update only once it knows the extension, internal/assistant outbox.go).
func (h *Manager) BusUp() {
	h.pageBusReset()
	select {
	case <-h.started:
	case <-time.After(time.Minute):
		return
	}
	h.reportMu.Lock()
	h.reportSent = nil
	h.reportMu.Unlock()
	h.sendReport()
}

// Request takes the hub's extensions.* calls (handled false: not one).
func (h *Manager) Request(ctx context.Context, method string, params json.RawMessage) (any, bool, error) {
	switch method {
	case extwire.MethodUIStream:
		var call extwire.UIStreamCall
		if len(params) > 128<<10 || json.Unmarshal(params, &call) != nil {
			return nil, true, pageError("invalid")
		}
		if !h.ready(ctx) {
			return nil, true, errAssistantOffline
		}
		frame, err := h.uiStream(ctx, call)
		if err != nil {
			requestID := ""
			if env, decodeErr := uistream.DecodeClientEnvelope(call.Envelope); decodeErr == nil && env.Request != nil {
				requestID = env.Request.RequestID
			}
			frame = streamFrame("error", requestID, &wire.UIStreamFailure{Code: pageFailureCode(err), Message: i18n.Localize(err, i18n.Resolve(call.Who.Lang))})
		}
		var envelope struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(frame, &envelope)
		// Snapshots are enqueued by the manager while it still owns publication order.
		if envelope.Kind != "snapshot" {
			h.pageEmit(call.Who, frame)
		}
		return map[string]any{}, true, nil
	case extwire.MethodAccept:
		var a struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(params, &a); err != nil || a.ID == "" {
			return nil, true, &rpc.Error{Code: bus.CodeInvalidParams, Message: "extensions.accept needs an id"}
		}
		return map[string]any{}, true, h.Accept(ctx, a.ID)
	case extwire.MethodAuthorize:
		var a extwire.Authorize
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, true, &rpc.Error{Code: bus.CodeInvalidParams, Message: err.Error()}
		}
		if !h.ready(ctx) {
			return nil, true, errAssistantOffline
		}
		return h.authorize(a), true, nil
	case extwire.MethodHubCall:
		var c extwire.HubCall
		if err := json.Unmarshal(params, &c); err != nil {
			return nil, true, &rpc.Error{Code: bus.CodeInvalidParams, Message: err.Error()}
		}
		if !h.ready(ctx) {
			return nil, true, errAssistantOffline
		}
		out, err := h.hubCall(ctx, c.Method, c.Params)
		return out, true, err
	case extwire.MethodSourceExport, extwire.MethodSourceImport, extwire.MethodSourceRelease:
		if !h.ready(ctx) {
			return nil, true, errAssistantOffline
		}
		out, err := h.sourceCall(method, params)
		return out, true, err
	}
	return nil, false, nil
}

// Event takes the hub's extensions.* events (handled false: not one).
func (h *Manager) Event(name string, params json.RawMessage) bool {
	if name == extwire.EventUIConnectionGone {
		var who extwire.UIPrincipal
		if json.Unmarshal(params, &who) == nil {
			go h.pageDisconnect(who)
		}
		return true
	}
	if name == extwire.EventErrorReports {
		h.hubErrorReports(params)
		return true
	}
	if name != extwire.EventEmit {
		return false
	}
	var e extwire.Emit
	if json.Unmarshal(params, &e) == nil && h.extensions != nil {
		var p map[string]any
		_ = json.Unmarshal(e.Params, &p)
		h.extensions.emit(e.Event, p)
	}
	return true
}

// authorize says whose extension token hash is and whether it opens the hub's route.
func (h *Manager) authorize(a extwire.Authorize) extwire.Authorized {
	e, ok := h.extTokens.lookupHash(a.Hash)
	if !ok || h.extensions != nil && h.extensions.reg != nil && h.extensions.reg.CanonicalID(e.extension) != e.extension {
		// Owner aliases do not grant a retired program identity any authority.
		return extwire.Authorized{}
	}
	p := e.principal()
	out := extwire.Authorized{Extension: p.Extension, Name: p.Name, Scopes: p.Scopes}
	if dotSegment(a.Path) {
		// A "." or ".." segment, escaped or not, is no route's: a decoded path (an older hub) would be cleaned onto
		// another route than the one the escaped path is served on (#5182).
		return out
	}
	pattern := a.Pattern
	if slices.Contains(extwire.Routes, pattern) && h.apiMux != nil {
		// one of the routes the hub relays here: which of the manager's own it is, matched on the path it is served on
		if req, err := http.NewRequest(a.Method, a.Path, nil); err == nil {
			_, pattern = h.apiMux.Handler(req)
		}
	}
	out.OK, out.Scope = e.opens(pattern, a.Path)
	return out
}

// dotSegment says whether path has a "." or ".." segment, each segment unescaped on its own as the mux reads it.
func dotSegment(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if s, err := url.PathUnescape(seg); err != nil || s == "." || s == ".." {
			return true
		}
	}
	return false
}

// hubCall answers what the hub's own features ask of this code.
func (h *Manager) hubCall(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var a struct {
		Tool      string          `json:"tool"`
		Summary   string          `json:"summary"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Assistant bool            `json:"assistant"`
		MachineID string          `json:"machine_id"`
		SessionID string          `json:"session_id"`
		Purpose   string          `json:"purpose"`
		Base      string          `json:"base"`
		TZ        string          `json:"tz"`
		Sender    string          `json:"sender"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: err.Error()}
		}
	}
	switch method {
	case extwire.HubGitHubLogin:
		login, known := h.githubLogin(ctx)
		return map[string]any{"login": login, "known": known}, nil
	case extwire.HubSettingsList:
		return map[string]any{"settings": h.settingsList()}, nil
	case extwire.HubSettingsTool:
		text, err := h.toolPersonaSettings(ctx, a.MachineID, a.SessionID, a.Arguments)
		return map[string]any{"text": text}, err
	case extwire.HubAudit:
		var au extwire.Audit
		if err := json.Unmarshal(params, &au); err != nil || au.Extension == "" ||
			au.Action != extwire.AuditPermissionModeRefused && au.Action != extwire.AuditTerminalControlRefused &&
				au.Action != extwire.AuditSecretHandoffRefused {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: "extensions: not an audit the hub records"}
		}
		actor := "extension:" + au.Extension
		if au.Action == extwire.AuditSecretHandoffRefused { // the provider's record of someone else's try
			id, ok := h.extensions.secretProviderID(au.Extension)
			if !ok {
				return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: "extensions: " + au.Extension + " is no secret provider here"}
			}
			au.Extension, actor = id, handoffActor(au.Actor) // under its current id, where its page reads it
		}
		h.audit(ctx, au.Extension, au.Action, actor, au.Detail)
		return map[string]any{}, nil
	}
	return nil, fmt.Errorf("extensions: no hub call %q", method)
}

// AppCall answers the assistant's app.calls that are this code's (handled false: the hub's): the inbox's sources,
// the channels' transport, the connectors' snapshots, the extensions' parts of its directory.
func (h *Manager) AppCall(ctx context.Context, p assistantwire.AppCall) (json.RawMessage, bool, error) {
	switch p.AppID {
	case assistantwire.AppInbox, assistantwire.AppFeishu, assistantwire.AppChannels, assistantwire.AppConnectors, assistantwire.AppPlugins:
	case assistantwire.AppInteractions:
		if p.Method != assistantwire.InteractionsOwnerAction {
			return nil, false, nil
		}
	default:
		return nil, false, nil
	}
	if !h.ready(ctx) {
		return nil, true, errAssistantOffline
	}
	ctx = withActor(ctx, actorAssistant) // what changes here is the assistant's doing (the audit's actor)
	if p.AppID == assistantwire.AppInbox {
		return h.inboxAppCall(ctx, p)
	}
	if out, ok, err := h.kbSourcesCall(ctx, p); ok { // the knowledge base's sources (kb_sources.go)
		return out, true, err
	}
	if out, ok, err := h.modelsAppCall(ctx, p); ok { // the extensions' providers, voice models, quota (models_link.go)
		return out, true, err
	}
	if p.AppID == assistantwire.AppConnectors || p.AppID == assistantwire.AppPlugins {
		out, err := h.assistantAppCall(ctx, p)
		if err != nil {
			return nil, true, err
		}
		raw, err := json.Marshal(out)
		return raw, true, err
	}
	return h.channelsAppCall(ctx, p)
}

// ---- the report ----

// sendReport tells the hub what it shows of the extensions, when it changed since it was told last.
func (h *Manager) sendReport() {
	select {
	case <-h.started:
	default:
		return
	}
	r := h.report()
	raw, err := json.Marshal(r)
	if err != nil {
		return
	}
	h.reportMu.Lock()
	same := string(raw) == string(h.reportSent)
	if !same {
		h.reportSent = raw
		h.reportBotIdentities = r.BotIdentities
	}
	h.reportMu.Unlock()
	if same || h.opts.Hub == nil {
		return
	}
	if err := h.opts.Hub.Notify(extwire.EventReport, json.RawMessage(raw)); err != nil {
		h.reportMu.Lock()
		h.reportSent = nil // told again when the hub is back (BusUp)
		h.reportMu.Unlock()
	}
}

// report is what the hub shows of the extensions now.
func (h *Manager) report() extwire.Report {
	r := extwire.Report{Features: h.features(), Machines: map[string]extwire.MachineExtras{}, BotIdentities: h.botIdentities(),
		EventsDeclared: true}
	if h.extensions != nil {
		r.ScreenKeepAwake = h.cua().awake()
		r.Looks = h.extensionLooks()
	}
	if ps := h.extensions; ps != nil {
		for _, p := range ps.reg.List() {
			if !p.Record.Enabled || p.Problem != "" {
				continue
			}
			hp := ps.get(p.Record.ID)
			if hp == nil || hp.proc == nil || hp.proxy == nil {
				continue
			}
			for _, a := range p.Manifest.APIs() {
				r.APIs = append(r.APIs, extwire.APIPrefix{Prefix: a.Prefix, Receipts: a.Receipts, Events: a.Events})
			}
		}
	}
	for _, m := range h.machinesNow() {
		var x extwire.MachineExtras
		if c := h.computerOf(m); c != nil {
			x.Computer, _ = json.Marshal(c)
			x.ScreenPort = x.ScreenPort || c.Enabled
		}
		if b := h.browserOf(m); b != nil {
			x.Browser, _ = json.Marshal(b)
			x.ScreenPort = x.ScreenPort || b.Enabled
		}
		if p := h.iphoneOf(m); p != nil {
			x.IPhone, _ = json.Marshal(p)
		}
		if x.Computer != nil || x.Browser != nil || x.IPhone != nil {
			r.Machines[m.cfg.ID] = x
		}
	}
	return r
}

// ---- what a node does that is this code's ----

// Own is the manager's own part of a node's new connection: the extensions' agent filters (agent_filters.go).
func (h *Manager) Own(ctx context.Context, machine string) map[string]json.RawMessage {
	select {
	case <-h.started:
	default:
		return nil // the filters go to every node once the extensions are loaded (attachExtensions)
	}
	b, err := json.Marshal(h.agentFilters())
	if err != nil {
		return nil
	}
	return map[string]json.RawMessage{"agent_filters": b}
}

// NodeConnected: machine's node is configured and listed: the extensions' work on it starts (the hub's
// machineConnected did this).
func (h *Manager) NodeConnected(machine string) {
	select {
	case <-h.started:
	case <-time.After(2 * time.Minute):
		return
	}
	m := h.machineByID(machine)
	if m == nil {
		return
	}
	h.connectors.MachineOnline(machine)
	bg := context.Background()
	h.reconcileNodePrograms(bg, m) // cua, ego and the extensions' programs on this computer (builtin_programs.go)
}

// errNoAppMethod: an app.call of one of this code's apps it has no method for.
var errNoAppMethod = errors.New("no such method")

// ---- the nodes' MCP tools and gated commands that are the extensions' (the assistant's fleet_tools.go asks) ----

// AgentTools are the tools of the connectors that are ok for every agent, and for the assistant's conversations
// also Cua's, ego's and the connectors' only it gets (in the order the agents always got them).
func (h *Manager) AgentTools(ctx context.Context, assistant bool) (agents, assistantOnly []proto.Tool) {
	if !h.ready(ctx) {
		return nil, nil
	}
	agents = h.connectors.ToolsFor(ctx, true)
	if assistant {
		assistantOnly = append(assistantOnly, h.computerTools()...)
		assistantOnly = append(assistantOnly, h.browserTools()...)
		assistantOnly = append(assistantOnly, h.iphoneTools()...)
		assistantOnly = append(assistantOnly, h.connectors.ToolsFor(ctx, false)...)
	}
	return agents, assistantOnly
}

// CallTool runs a node's MCP call when it is one of these (handled false: none of them). A card it asks belongs to
// machine's conversation session; ctx ending (the node's caller went away) withdraws it.
func (h *Manager) CallTool(ctx context.Context, machine, session, name string, args json.RawMessage, assistant bool) (proto.ToolResult, bool) {
	if !h.ready(ctx) {
		return proto.ToolResult{}, false
	}
	switch {
	case h.connectors.Offers(name) && (assistant || h.connectors.ForAgents(name)):
		return h.callConnector(ctx, asker{machine, session}, name, args), true
	case assistant && isComputerTool(name):
		return h.callComputerTool(ctx, session, name, args), true
	case assistant && isBrowserTool(name):
		return h.callBrowserTool(ctx, session, name, args), true
	case assistant && isIPhoneTool(name):
		return h.callIPhoneTool(ctx, session, name, args), true
	}
	return proto.ToolResult{}, false
}

// ConnectorTool says whether name is a registered connector's tool (not Cua's or ego's).
func (h *Manager) ConnectorTool(name string) bool {
	return h.connectors != nil && h.connectors.Offers(name)
}

// AssistantSummary is the text a gated command's card shows, as its extension names what the ids are; false: none.
func (h *Manager) AssistantSummary(ctx context.Context, tool, summary string) (string, bool) {
	if !h.ready(ctx) || h.extensions == nil {
		return summary, false
	}
	return h.extensions.assistantSummary(ctx, tool, summary)
}

// AssistantLark is what the assistant's machine gets for its skills' commands: lark-cli's settings and rules and the
// extensions' command gates (only that machine may be given it: the caller checks).
func (h *Manager) AssistantLark(ctx context.Context) (proto.AssistantLark, error) {
	if !h.ready(ctx) {
		return proto.AssistantLark{}, errAssistantOffline
	}
	return h.assistantLark(ctx)
}

// extensionLooks are the looks of the extensions that are on, with their files (SVGs cleaned), for the hub.
func (h *Manager) extensionLooks() []extwire.LookPackage {
	var out []extwire.LookPackage
	h.extensions.declared(func(p *extension.Extension) {
		if p.Dir == "" {
			return
		}
		for _, l := range p.Manifest.Looks() {
			files := map[string][]byte{}
			ok := true
			for _, f := range l.Files() {
				b, err := extension.ReadLookFile(p.Dir, f)
				if err == nil && strings.HasSuffix(f, ".svg") {
					b, err = extension.CleanSVG(b)
				}
				if err != nil {
					h.logf("look %s of %s left out: %s: %v", l.ID, p.Record.ID, f, err)
					ok = false
					break
				}
				files[f] = b
			}
			if !ok {
				continue
			}
			item, _ := json.Marshal(l)
			out = append(out, extwire.LookPackage{Extension: p.Record.ID, ExtensionName: p.Full.Name, Version: p.Record.Version,
				Item: item, Files: files})
		}
	})
	return out
}

// secretProviderID is the current id of installed extension id (an old alias's too) when it contributes a secret
// provider, switched on or not.
func (ps *extensions) secretProviderID(id string) (string, bool) {
	if ps == nil || ps.reg == nil {
		return "", false
	}
	p := ps.reg.Snapshot(id)
	if p == nil || p.Full == nil || len(p.Full.SecretProviders()) == 0 {
		return "", false
	}
	return p.Record.ID, true
}

// handoffActor is who the hub says tried to approve a hand-off card, as the other records name an actor ("device:<id>",
// "extension:<id>", "hub token"); anything else is "unknown". Clipped: it is a column a page lists.
func handoffActor(a string) string {
	if a == "hub token" || (strings.HasPrefix(a, "device:") || strings.HasPrefix(a, "extension:")) && len(a) <= 160 &&
		!strings.ContainsFunc(a, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return a
	}
	return "unknown"
}
