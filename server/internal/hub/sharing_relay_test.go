package hub

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func TestShareTrustedRelayAndAnonymousCenterDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := setupTestDB(t)
	h := newTestHub(t)
	h.store = store
	h.cfg.PublicURL = "http://app.example.test:18660"
	h.cfg.Sharing = &SharingConfig{PublicOrigin: "http://share.example.test:18661"}
	if err := h.initHubID(ctx); err != nil {
		t.Fatal(err)
	}
	issuer := h.hubID()
	subject, err := store.CreateOwnerSubject(ctx, issuer, "Synthetic owner", "synthetic-admin", "Synthetic acceptance")
	if err != nil {
		t.Fatal(err)
	}
	const deviceToken = "synthetic-enrolled-device-token"
	const credential = "synthetic-enrolled-device"
	if err = store.CreateDeviceToken(ctx, db.DeviceToken{ID: credential, Name: "Synthetic device", Role: roleFull, Kind: "web"}, hashToken(deviceToken)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.EnrollOwner(ctx, issuer, credential, subject.Subject, "synthetic-admin", "Synthetic enrollment", 0); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveBusIdentity(ctx, db.BusIdentity{ID: devicesOwner, Role: "manager", Name: "Synthetic manager"}, "synthetic-manager-hash"); err != nil {
		t.Fatal(err)
	}
	const machine = "synthetic-relay-machine"
	h.cfg.Machines = []MachineConfig{{ID: machine, Kind: "local"}}
	h.byID[machine] = &Machine{cfg: MachineConfig{ID: machine}}
	route, err := store.ShareRouting(ctx, issuer, machine, devicesOwner)
	if err != nil {
		t.Fatal(err)
	}
	rawToken := make([]byte, 48)
	copy(rawToken, route.Handle)
	if _, err = rand.Read(rawToken[16:]); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	hubIn, managerOut := io.Pipe()
	managerIn, hubOut := io.Pipe()
	hp, mp := rpc.New("synthetic-hub", hubOut), rpc.New("synthetic-manager", managerOut)
	var mu sync.Mutex
	var captured []assistantwire.HTTPRequest
	mode := "ok"
	mp.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var req assistantwire.HTTPRequest
		if method != assistantwire.MethodHTTP || json.Unmarshal(params, &req) != nil {
			return nil, errors.New("unexpected synthetic request")
		}
		mu.Lock()
		captured = append(captured, req)
		kind := mode
		mu.Unlock()
		res := assistantwire.HTTPResponse{Status: 200, Body: []byte(`{"revision":1,"content_version":1,"messages":[],"next_cursor":null}`), ShareAuthority: &route.Authority, Header: map[string][]string{"Set-Cookie": {"synthetic=private"}, "Location": {"https://example.test/"}, "WWW-Authenticate": {"Bearer"}, "X-Private": {"synthetic"}}}
		if kind == "missing-authority" {
			res.ShareAuthority = nil
		}
		if kind == "unsafe-error" {
			res.Status = 500
			res.Body = []byte(`{"code":"upstream","error":"` + token + `"}`)
		}
		return res, nil
	}
	go hp.Serve(ctx, hubIn)
	go mp.Serve(ctx, managerIn)
	t.Cleanup(func() {
		cancel()
		hp.Close(io.EOF)
		mp.Close(io.EOF)
		hubIn.Close()
		managerIn.Close()
		hubOut.Close()
		managerOut.Close()
	})
	h.bus.current = map[string]*busConn{devicesOwner: {peer: hp, presence: busPresence{Capabilities: []string{devicewire.SharingCapability}}}}
	routes := h.routes()
	request := func(method, target, body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:12345"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(ManagerHeader, devicesOwner)
		r.Header.Set("Cookie", "synthetic=private")
		r.Header.Set(assistantwire.AdminHeader, "1")
		r.Header.Set("X-Persona-Caller", "forged")
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+deviceToken)
		}
		w := httptest.NewRecorder()
		routes.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "http://app.example.test:18660/api/v1/sessions/"+machine+"/synthetic-session/share-catalog", "", true)
	if w.Code != 200 {
		t.Fatalf("management relay: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	req := captured[len(captured)-1]
	mu.Unlock()
	if req.Caller == nil || req.Caller.Kind != "device" || req.Caller.OwnerSubject != subject.Subject || req.Caller.Issuer != issuer || req.Caller.BindingRevision != 1 || req.ShareAuthority == nil || *req.ShareAuthority != route.Authority {
		t.Fatal("trusted caller/assignment absent")
	}
	for _, key := range []string{"Authorization", "Cookie", assistantwire.AdminHeader, "X-Persona-Caller"} {
		if len(req.Header[key]) != 0 {
			t.Fatal("untrusted identity/header forwarded")
		}
	}
	for _, key := range []string{"Set-Cookie", "Location", "WWW-Authenticate", "X-Private"} {
		if w.Header().Get(key) != "" {
			t.Fatalf("upstream %s forwarded", key)
		}
	}
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token+"?manager=forged&token=forged", "", true)
	if w.Code != 200 {
		t.Fatalf("anonymous public relay: %d", w.Code)
	}
	mu.Lock()
	req = captured[len(captured)-1]
	mu.Unlock()
	if req.Caller == nil || req.Caller.Kind != "anonymous" || req.Caller.OwnerSubject != "" || strings.Contains(req.Query, "token") || strings.Contains(req.Query, "manager") {
		t.Fatal("public inherited credential/selector")
	}
	// The same exact exemption is composed into the center dispatcher, rather than auth's full-role default.
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, nil)
	r.RemoteAddr = "127.0.0.1:12345"
	h.mesh.centerAPI.Load().(http.Handler).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("center anonymous route: %d", w.Code)
	}
	mu.Lock()
	mode = "missing-authority"
	mu.Unlock()
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, "", false)
	if w.Code != 404 {
		t.Fatal("public response without private authority accepted")
	}
	mu.Lock()
	mode = "unsafe-error"
	mu.Unlock()
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, "", false)
	if w.Code != 404 || strings.Contains(w.Body.String(), token) {
		t.Fatal("public error leaked bearer")
	}
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	if _, err = store.RevokeDeviceToken(ctx, credential); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "http://app.example.test:18660/api/v1/agent-shares", "", true)
	if w.Code != 403 && w.Code != 401 {
		t.Fatal("cached revoked credential admitted")
	}
	if err = store.RetireShareMachine(ctx, issuer, machine); err != nil {
		t.Fatal(err)
	}
	w = request("GET", "http://share.example.test:18661/api/public/agent-shares/"+token, "", false)
	if w.Code != 404 {
		t.Fatal("old manager authority served public snapshot")
	}
	t.Log("synthetic actual hub RPC relay: fresh enrollment, forged identity stripped, response header allowlist, explicit anonymous direct+center, missing authority, safe errors, revoked credential and machine fence PASS")
}

// The connection callback supplies empty hub configuration, never creation provenance.
type receiptIntegrationHost struct{ hub *Hub }

func (receiptIntegrationHost) Call(_ context.Context, _ string, _ any, out any) error {
	return json.Unmarshal([]byte(`{}`), out)
}
func (host receiptIntegrationHost) Notify(event string, params any) error {
	if event != devicewire.EventState {
		return nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var states devicewire.States
	if err := json.Unmarshal(raw, &states); err != nil {
		return err
	}
	host.hub.applyDeviceStatesFrom(devicesOwner, states)
	return nil
}

func TestShareCreationAuthenticatedHTTPReceiptIntegration(t *testing.T) {
	for _, mode := range []string{"matching", "absent", "unknown", "mismatched", "work-matching", "kept-matching"} {
		t.Run(mode, func(t *testing.T) { shareCreationHTTPReceiptIntegration(t, mode) })
	}
}

func shareCreationHTTPReceiptIntegration(t *testing.T, mode string) {
	method := "session.start"
	if mode == "work-matching" {
		method = devicewire.MethodStartWorkSession
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := setupTestDB(t)
	h := newTestHub(t)
	h.store = store
	if err := h.initHubID(ctx); err != nil {
		t.Fatal("authority initialization failed")
	}
	issuer := h.hubID()
	subject, err := store.CreateOwnerSubject(ctx, issuer, "Synthetic owner", "synthetic-admin", "Receipt integration")
	if err != nil {
		t.Fatal("subject creation failed")
	}
	token := "synthetic-receipt-device-token-" + mode
	credential := "synthetic-receipt-device-" + mode
	if err := store.CreateDeviceToken(ctx, db.DeviceToken{ID: credential, Name: "Synthetic device", Role: roleFull, Kind: "web"}, hashToken(token)); err != nil {
		t.Fatal("credential creation failed")
	}
	if _, err := store.EnrollOwner(ctx, issuer, credential, subject.Subject, "synthetic-admin", "Receipt integration", 0); err != nil {
		t.Fatal("enrollment failed")
	}
	if err := store.SaveBusIdentity(ctx, db.BusIdentity{ID: devicesOwner, Role: "manager", Name: "Synthetic manager"}, "synthetic-manager-hash"); err != nil {
		t.Fatal("manager registration failed")
	}
	machine := "synthetic-receipt-machine-" + mode
	h.cfg.Machines = []MachineConfig{{ID: machine, Kind: "local"}}
	if mode == "kept-matching" {
		h.cfg.Machines = nil
		h.devices.kept = map[string]string{machine: devicesOwner}
	}
	h.byID[machine] = &Machine{cfg: MachineConfig{ID: machine}, hub: h}
	route, err := store.ShareRouting(ctx, issuer, machine, devicesOwner)
	if err != nil {
		t.Fatal("routing failed")
	}
	type observation struct {
		creation, incarnation string
		reservedStripped      bool
	}
	observed := make(chan observation, 1)
	ready := make(chan struct{}, 1)
	var node *rpc.Peer
	var nodeMu sync.Mutex
	d := devices.New(devices.Options{Store: store, Hub: receiptIntegrationHost{hub: h}, DialNode: func(string) func(context.Context) (net.Conn, error) {
		return func(c context.Context) (net.Conn, error) {
			managerSide, nodeSide := net.Pipe()
			p := rpc.New("synthetic-receipt-node", nodeSide)
			nodeMu.Lock()
			node = p
			nodeMu.Unlock()
			methodForStart := method
			p.OnRequest = func(c context.Context, method string, params json.RawMessage) (any, error) {
				switch method {
				case "node.info":
					return proto.NodeInfo{OS: "linux", Arch: "amd64", Home: "/synthetic", Capabilities: []string{devicewire.CreationCapability, "share_projection.v1"}}, nil
				case "sessions.list":
					select {
					case ready <- struct{}{}:
					default:
					}
					return map[string]any{"sessions": []proto.Session{}}, nil
				case methodForStart:
					var fields map[string]json.RawMessage
					if json.Unmarshal(params, &fields) != nil {
						return nil, errors.New("invalid synthetic start")
					}
					var o observation
					_ = json.Unmarshal(fields["creation_id"], &o.creation)
					_ = json.Unmarshal(fields["incarnation"], &o.incarnation)
					o.reservedStripped = fields["caller"] == nil && fields["owner_subject"] == nil && fields["submission"] == nil
					observed <- o
					return map[string]any{"session": map[string]any{"session_id": "synthetic-fresh-session", "cwd": "/synthetic/receipt"}}, nil
				}
				return map[string]any{}, nil
			}
			go func() { _ = p.Serve(c, nodeSide); nodeSide.Close() }()
			return managerSide, nil
		}
	}})
	rawMachine, _ := json.Marshal(devices.MachineConfig{ID: machine, Kind: "local"})
	managerCfg := devicewire.Config{Machines: []json.RawMessage{rawMachine}, ShareAuthorities: map[string]devicewire.ShareAuthority{machine: route.Authority}}
	if mode == "kept-matching" {
		// The isolated node fixture supplies its own machine entry; authority must
		// come from the real hub handover, not a manually injected matching map.
		managerCfg.ShareAuthorities = h.devicesConfigFor(devicesOwner).ShareAuthorities
	}
	d.Configure(managerCfg)
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("manager shutdown timed out")
		}
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("synthetic node connection timed out")
	}
	hubIn, managerOut := io.Pipe()
	managerIn, hubOut := io.Pipe()
	hp, mp := rpc.New("synthetic-receipt-hub", hubOut), rpc.New("synthetic-receipt-manager", managerOut)
	calls := make(chan devicewire.NodeCall, 1)
	metadataSeen := make(chan struct{}, 8)
	mp.OnRequest = func(c context.Context, method string, params json.RawMessage) (any, error) {
		select {
		case metadataSeen <- struct{}{}:
		default:
		}
		if method == devicewire.MethodCall {
			var call devicewire.NodeCall
			if json.Unmarshal(params, &call) != nil {
				return nil, errors.New("invalid manager envelope")
			}
			if devicewire.IsSessionStart(call.Method) {
				calls <- call
			}
		}
		if method == assistantwire.MethodAgentStartParams {
			var input assistantwire.AgentStartParams
			if json.Unmarshal(params, &input) != nil {
				return nil, errors.New("invalid synthetic start metadata")
			}
			return map[string]any{"params": input.Params}, nil
		}
		if out, handled, err := d.Request(c, method, params); handled {
			return out, err
		}
		return map[string]any{}, nil
	}
	go hp.Serve(ctx, hubIn)
	go mp.Serve(ctx, managerIn)
	t.Cleanup(func() {
		hp.Close(io.EOF)
		mp.Close(io.EOF)
		hubIn.Close()
		managerIn.Close()
		hubOut.Close()
		managerOut.Close()
	})
	h.bus.mu.Lock()
	h.bus.current = map[string]*busConn{devicesOwner: {peer: hp, presence: busPresence{Capabilities: []string{devicewire.SharingCapability}}}}
	h.bus.mu.Unlock()
	params := map[string]any{"machine_id": machine, "cwd": "/synthetic/receipt", "prompt": "Synthetic input", "creation_id": "forged", "incarnation": "forged", "caller": map[string]any{"owner_subject": "forged"}}
	if mode == "work-matching" {
		params["work_session"], params["launcher"], params["agent_type"] = true, "claude", "claude"
	}
	body, err := json.Marshal(params)
	if err != nil {
		t.Fatal("synthetic HTTP body encoding failed")
	}
	r := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(string(body)))
	r.RemoteAddr = "127.0.0.1:12345"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(ManagerHeader, devicesOwner)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Persona-Caller", "forged")
	w := httptest.NewRecorder()
	h.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		var fault struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &fault)
		t.Fatalf("authenticated start status: %d; upstream_seen=%t offline=%t auth=%t scope=%t manager=%t origin=%t csrf=%t", w.Code, len(metadataSeen) > 0, fault.Code == "assistant_offline", strings.Contains(fault.Error, "token"), strings.Contains(fault.Error, "scope"), strings.Contains(fault.Error, "manager"), strings.Contains(fault.Error, "origin"), strings.Contains(strings.ToLower(fault.Error), "csrf"))
	}
	var started struct {
		Session struct {
			ID  string `json:"session_id"`
			Cwd string `json:"cwd"`
		} `json:"session"`
	}
	if json.Unmarshal(w.Body.Bytes(), &started) != nil || started.Session.ID != "synthetic-fresh-session" || started.Session.Cwd != "/synthetic/receipt" {
		t.Fatal("start session or directory mismatch")
	}
	var call devicewire.NodeCall
	select {
	case call = <-calls:
	case <-time.After(time.Second):
		t.Fatal("start envelope absent")
	}
	if call.Method != method || call.Caller == nil || call.Caller.Kind != "device" || call.Caller.OwnerSubject != subject.Subject || call.Caller.CredentialID != credential || call.Caller.BindingRevision != 1 || call.Caller.Issuer != issuer || call.Authority == nil || *call.Authority != route.Authority {
		t.Fatal("trusted caller or routing authority mismatch")
	}
	var o observation
	select {
	case o = <-observed:
	case <-time.After(time.Second):
		t.Fatal("node start absent")
	}
	if o.creation == "" || o.incarnation == "" || o.creation == "forged" || o.incarnation == "forged" || !o.reservedStripped {
		t.Fatal("creation intent absent or client provenance accepted")
	}
	owner, err := store.SessionShareOwner(ctx, machine, "synthetic-fresh-session")
	if err != nil || owner != nil {
		t.Fatal("start reply without durable receipt claimed ownership")
	}
	nodeMu.Lock()
	p := node
	nodeMu.Unlock()
	// An RPC on the same stream fences the preceding state.put: the manager
	// processes the event before it can answer state.load. No timing inference
	// or start reply may substitute for a matching persisted receipt.
	if mode != "matching" && mode != "work-matching" && mode != "kept-matching" {
		state := map[string]any{}
		if mode == "unknown" {
			state = map[string]any{"creation_id": "synthetic-unknown-creation", "incarnation": o.incarnation}
		}
		if mode == "mismatched" {
			state = map[string]any{"creation_id": o.creation, "incarnation": "synthetic-wrong-incarnation"}
		}
		if err := p.Notify("state.put", map[string]any{"session_id": "synthetic-fresh-session", "state": state}); err != nil {
			t.Fatal("negative receipt event failed")
		}
		var loaded struct {
			Sessions map[string]db.NodeSession `json:"sessions"`
		}
		if err := p.Call(ctx, "state.load", map[string]any{}, &loaded); err != nil {
			t.Fatal("negative receipt persistence fence failed")
		}
		owner, err = store.SessionShareOwner(ctx, machine, "synthetic-fresh-session")
		if err != nil || owner != nil {
			t.Fatal("absent or mismatched receipt claimed ownership")
		}
		t.Log("absent or mismatched durable receipt remains ownership-unbound PASS")
		return
	}
	if err := p.Notify("state.put", map[string]any{"session_id": "synthetic-fresh-session", "state": map[string]any{"creation_id": o.creation, "incarnation": o.incarnation}}); err != nil {
		t.Fatal("receipt event failed")
	}
	var loaded struct {
		Sessions map[string]db.NodeSession `json:"sessions"`
	}
	if err := p.Call(ctx, "state.load", map[string]any{}, &loaded); err != nil {
		t.Fatal("matching receipt persistence fence failed")
	}
	state, exists := loaded.Sessions["synthetic-fresh-session"]
	if !exists || state.CreationID != o.creation || state.Incarnation != o.incarnation {
		t.Fatal("durable receipt reload absent or unequal")
	}
	owner, err = store.SessionShareOwner(ctx, machine, "synthetic-fresh-session")
	if err != nil {
		t.Fatal("ownership lookup failed")
	}
	if owner == nil || owner.Subject != subject.Subject || owner.Issuer != issuer || owner.Incarnation != o.incarnation || owner.Generation != route.Authority.Generation {
		t.Fatal("persisted receipt ownership absent or unequal")
	}
	t.Log("authenticated caller, routing authority, creation intent, node receipt and durable ownership presence/equality PASS")
}
