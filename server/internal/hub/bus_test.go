package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What the tests register besides the hub's own: an event, and a method that answers with what it was given.
var busTestEvents struct {
	sync.Mutex
	got []string
}

func init() {
	handleBusEvent("test.event", func(_ *Hub, c *busConn, params json.RawMessage) {
		busTestEvents.Lock()
		busTestEvents.got = append(busTestEvents.got, c.ident.ID+" "+string(params))
		busTestEvents.Unlock()
	})
	handleBus("test.echo", func(_ *Hub, _ context.Context, c *busConn, params json.RawMessage) (any, error) {
		return map[string]any{"from": c.ident.ID, "params": params}, nil
	})
}

// newBusHub is a hub on the lane's test database with no bus identities and no machine moved to a manager, served
// over plain HTTP on loopback. The database is opened for the hub's set (h.store, the third result); the assistant a
// test connects gets it opened for the manager's set (managerDB).
func newBusHub(t *testing.T) (*Hub, *httptest.Server, *db.DB) {
	t.Helper()
	dsn := os.Getenv("PERSONA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PERSONA_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := db.Open(ctx, dsn, db.HubSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	clearBus := func() {
		list, _ := store.BusIdentities(context.Background())
		for _, b := range list {
			_, _ = store.DeleteBusIdentity(context.Background(), b.ID)
		}
		// A test's moves are kept in the database: the next test's hub would read them (ownership_moves.go).
		_ = store.PutSetting(context.Background(), machineOwnersKey, map[string]string{})
	}
	clearBus()
	t.Cleanup(clearBus)
	h := newTestHub(t)
	h.store = store
	useManagerDB(t, h, dsn) // the assistant a test connects is a manager: its own handle on the database
	srv := httptest.NewServer(h.routes())
	t.Cleanup(srv.Close)
	t.Cleanup(func() { // before the server closes
		for _, c := range h.stopBus() {
			_ = c.ws.CloseNow()
		}
	})
	return h, srv, store
}

func busToken(t *testing.T, h *Hub, id, name string) string {
	t.Helper()
	tok, err := h.createBusToken(context.Background(), id, name)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// rawBus is a service connection driven by hand: a peer serving it (it answers echo), and why it ended.
type rawBus struct {
	ws    *websocket.Conn
	peer  *rpc.Peer
	ended chan error
}

func dialRawBus(t *testing.T, srv *httptest.Server, tok string) *rawBus {
	t.Helper()
	return dialRawBusWith(t, srv, tok, echoService)
}

// dialRawBusWith is dialRawBus answering the hub's calls with handle.
func dialRawBusWith(t *testing.T, srv *httptest.Server, tok string, handle rpc.Handler) *rawBus {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ws, err := bus.Dial(ctx, srv.URL, tok, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	conn := bus.NewConn(ctx, ws)
	r := &rawBus{ws: ws, peer: rpc.New("s", conn), ended: make(chan error, 1)}
	r.peer.OnRequest = handle
	go func() { r.ended <- r.peer.Serve(ctx, conn) }()
	return r
}

func (r *rawBus) call(method string, params, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.peer.Call(ctx, method, params, out)
}

// closedWith waits for the hub to close the connection with code.
func (r *rawBus) closedWith(t *testing.T, code websocket.StatusCode) {
	t.Helper()
	select {
	case err := <-r.ended:
		if got := websocket.CloseStatus(err); got != code {
			t.Fatalf("closed with %d (%v), want %d", got, err, code)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("not closed (want %d)", code)
	}
}

// echoService answers the hub's echo with what it was given, and fails anything else with a code of its own.
func echoService(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == "echo" {
		return map[string]any{"got": params}, nil
	}
	return nil, &rpc.Error{Code: "no_such_thing", Message: "the fake service has no " + method}
}

func rpcCode(err error) string {
	var coded *rpc.Error
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// fakeService is a service on bus.Client: it records the hello answers and the hub's events.
type fakeService struct {
	c      *bus.Client
	hellos chan json.RawMessage
	events chan string
	count  atomic.Int32
}

func startService(t *testing.T, srv *httptest.Server, tok, method string, params any) *fakeService {
	t.Helper()
	f := &fakeService{hellos: make(chan json.RawMessage, 16), events: make(chan string, 16)}
	f.c = &bus.Client{Hub: srv.URL, Token: tok, HelloMethod: method, HelloParams: func() any { return params },
		OnHello: func(res json.RawMessage) error {
			f.count.Add(1)
			select {
			case f.hellos <- res:
			default:
			}
			return nil
		},
		OnRequest: echoService,
		OnEvent: func(event string, params json.RawMessage) {
			select {
			case f.events <- event + " " + string(params):
			default:
			}
		},
		Backoff: 20 * time.Millisecond, MaxBackoff: 300 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = f.c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func (f *fakeService) hello(t *testing.T, out any) {
	t.Helper()
	select {
	case res := <-f.hellos:
		if err := json.Unmarshal(res, out); err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no hello answer")
	}
}

func (f *fakeService) event(t *testing.T, prefix string) string {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e := <-f.events:
			if strings.HasPrefix(e, prefix) {
				return e
			}
		case <-timeout:
			t.Fatalf("no event %q", prefix)
		}
	}
}

// manager.hello: the hub answers with the identity, what of its own the manager needs and the managers, and puts
// the connection in the roster; the other managers hear who is online. The manager's own sections (owner, notes, kb,
// a card's wait…) are its manager.json's, not in it.
func TestBusHello(t *testing.T) {
	h, srv, _ := newBusHub(t)
	tok := busToken(t, h, "assistant", "助手")
	deskTok := busToken(t, h, "desk", "工作")

	f := startService(t, srv, tok, "manager.hello", map[string]any{"id": "assistant", "host": "box", "os": "linux", "arch": "amd64",
		"deploy": "service", "version": "test-1", "apps": []any{}})
	var reply busHelloReply
	f.hello(t, &reply)
	// The machines go to the manager that carries them (devices_link.go): the assistant, during the migration.
	if reply.Devices == nil || reply.Devices.HubVersion != h.version || reply.Devices.Machines == nil {
		t.Fatalf("hello's devices %+v", reply.Devices)
	}
	reply.Devices = nil
	want := busHelloReply{Name: "助手", Role: bus.RoleManager,
		Managers: []busManagerBrief{{ID: "assistant", Name: "助手", Role: bus.RoleManager, Online: true}, {ID: "desk", Name: "工作", Role: bus.RoleManager}},
		Hello: assistantwire.Hello{DoneDelayMS: taskSettleDelay(h.cfg.Notify).Milliseconds(), Locale: "zh-Hans", // the user's language
			Mutes: &assistantwire.Mutes{Spaces: []string{}, Sessions: []string{}}, // the hub's mutes (none)
			// the finished push's timings: the manager's to apply (fleet_push.go there)
			Notify:            assistantwire.NotifyTimings{DoneDelayMS: 60000, LongTurnMS: 60000, BackgroundHoldMS: 7200000},
			SecretHandoffGate: true}} // this hub checks who approves a hand-off card (secret_handoff.go)
	if !jsonEqual(reply, want) {
		t.Fatalf("hello answer %+v, want %+v", reply, want)
	}
	roster := h.busRoster()
	if len(roster) != 1 || roster[0].ID != "assistant" || roster[0].Role != bus.RoleManager || roster[0].Host != "box" || roster[0].Version != "test-1" ||
		roster[0].Since.IsZero() {
		t.Fatalf("roster %+v", roster)
	}

	startService(t, srv, deskTok, "manager.hello", map[string]any{"id": "desk"})
	e := f.event(t, "managers ")
	if !strings.Contains(e, `{"id":"desk","name":"工作","role":"manager","online":true}`) {
		t.Fatalf("managers event %s", e)
	}
	eventually(t, 5*time.Second, "both in the roster", func() bool { return len(h.busRoster()) == 2 })
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// A handshake that names another identity than the token's is refused and hung up.
func TestBusWrongIdentity(t *testing.T) {
	h, srv, _ := newBusHub(t)
	tok := busToken(t, h, "assistant", "")
	for _, params := range []map[string]any{{"id": "someone-else"}, {}} {
		r := dialRawBus(t, srv, tok)
		if err := r.call("manager.hello", params, nil); rpcCode(err) != bus.CodeWrongIdentity {
			t.Fatalf("hello %v: %v", params, err)
		}
		r.closedWith(t, bus.CloseRefused)
	}
	if roster := h.busRoster(); len(roster) != 0 {
		t.Fatalf("roster %+v", roster)
	}

	// bus.Client takes a refused handshake for a rejected token.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ended := make(chan string, 4)
	c := &bus.Client{Hub: srv.URL, Token: tok, HelloMethod: "manager.hello", HelloParams: func() any { return map[string]any{"id": "x"} },
		MaxBackoff: time.Minute, Logf: func(f string, a ...any) {
			select {
			case ended <- fmt.Sprintf(f, a...):
			default:
			}
		}}
	go func() { _ = c.Run(ctx) }()
	select {
	case line := <-ended:
		if !strings.Contains(line, bus.ErrRejected.Error()) || !strings.Contains(line, "again in 1m0s") {
			t.Fatalf("client logged %q", line)
		}
	case <-ctx.Done():
		t.Fatal("the client did not give up")
	}
}

// Only a bus token opens the bus, and it opens nothing else; a revoked or replaced token is cut off at once.
func TestBusRejectsTokens(t *testing.T) {
	h, srv, store := newBusHub(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, tok := range []string{"", "jb_" + randomHex(24), h.cfg.Token} {
		if _, err := bus.Dial(ctx, srv.URL, tok, nil); !errors.Is(err, bus.ErrRejected) {
			t.Fatalf("token %.6q…: %v", tok, err)
		}
	}
	tok := busToken(t, h, "assistant", "")
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/machines", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a bus token on the apps' API: %v %v", resp, err)
	}

	// Revoked: the live connection closes as revoked, and the token no longer dials.
	r := dialRawBus(t, srv, tok)
	if err := r.call("manager.hello", map[string]any{"id": "assistant"}, nil); err != nil {
		t.Fatal(err)
	}
	if ok, err := h.revokeBusToken(ctx, "assistant"); !ok || err != nil {
		t.Fatalf("revoke: %v %v", ok, err)
	}
	r.closedWith(t, bus.CloseRevoked)
	if _, err := bus.Dial(ctx, srv.URL, tok, nil); !errors.Is(err, bus.ErrRejected) {
		t.Fatalf("a revoked token: %v", err)
	}
	if ok, err := h.revokeBusToken(ctx, "assistant"); ok || err != nil {
		t.Fatalf("revoking again: %v %v", ok, err)
	}

	// A new token for an identity replaces the old one.
	old := busToken(t, h, "assistant", "")
	r = dialRawBus(t, srv, old)
	fresh := busToken(t, h, "assistant", "")
	r.closedWith(t, bus.CloseRevoked)
	if _, err := bus.Dial(ctx, srv.URL, old, nil); !errors.Is(err, bus.ErrRejected) {
		t.Fatalf("the replaced token: %v", err)
	}
	if err := dialRawBus(t, srv, fresh).call("manager.hello", map[string]any{"id": "assistant"}, nil); err != nil {
		t.Fatal(err)
	}

	// What createBusToken refuses; an identity of another role (none is made yet) is refused at the upgrade.
	for _, id := range []string{"", "Bad", "a.b", strings.Repeat("a", 33)} {
		if _, err := h.createBusToken(ctx, id, ""); err == nil {
			t.Errorf("createBusToken(%q) took it", id)
		}
	}
	other := "jb_" + randomHex(24)
	if err := store.SaveBusIdentity(ctx, db.BusIdentity{ID: "later", Role: "app"}, hashToken(other)); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Dial(ctx, srv.URL, other, nil); !errors.Is(err, bus.ErrRejected) {
		t.Fatalf("a token of another role: %v", err)
	}
}

// A second connection of the same identity takes over when it says its handshake; the first is hung up.
func TestBusReplacesTheOlderConnection(t *testing.T) {
	h, srv, _ := newBusHub(t)
	tok := busToken(t, h, "assistant", "")
	first := dialRawBus(t, srv, tok)
	if err := first.call("manager.hello", map[string]any{"id": "assistant", "host": "one"}, nil); err != nil {
		t.Fatal(err)
	}
	second := dialRawBus(t, srv, tok)
	if err := first.call("ping", nil, nil); err != nil {
		t.Fatalf("the first before the second's handshake: %v", err)
	}
	if err := second.call("manager.hello", map[string]any{"id": "assistant", "host": "two"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := first.call("ping", nil, nil); err == nil {
		t.Fatal("the replaced connection still calls")
	}
	first.closedWith(t, bus.CloseReplaced)
	if err := second.call("ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := h.callBus(context.Background(), "assistant", "echo", map[string]any{"n": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if roster := h.busRoster(); len(roster) != 1 || roster[0].Host != "two" {
		t.Fatalf("roster %+v", roster)
	}
}

// Methods go both ways once the handshake went through: the service calls what busMethods registers (its role
// permitting), the hub calls and notifies the service; nothing is taken before the handshake.
func TestBusCallsBothWays(t *testing.T) {
	h, srv, _ := newBusHub(t)
	tok := busToken(t, h, "assistant", "")
	busTestEvents.Lock()
	busTestEvents.got = nil
	busTestEvents.Unlock()

	r := dialRawBus(t, srv, tok)
	if err := r.call("ping", nil, nil); rpcCode(err) != bus.CodeStaleConnection {
		t.Fatalf("ping before the handshake: %v", err)
	}
	_ = r.peer.Notify("test.event", map[string]any{"n": "before"})
	if err := h.callBus(context.Background(), "assistant", "echo", nil, nil); !errors.Is(err, errBusOffline) {
		t.Fatalf("calling a service before its handshake: %v", err)
	}
	if err := r.call("manager.hello", map[string]any{"id": "assistant"}, nil); err != nil {
		t.Fatal(err)
	}
	var pong map[string]any
	if err := r.call("ping", nil, &pong); err != nil || len(pong) != 0 {
		t.Fatalf("ping: %v %v", pong, err)
	}
	if err := r.call("no.such", nil, nil); rpcCode(err) != bus.CodeUnknownMethod {
		t.Fatalf("an unknown method: %v", err)
	}
	var echo struct {
		From   string         `json:"from"`
		Params map[string]any `json:"params"`
	}
	if err := r.call("test.echo", map[string]any{"a": 1}, &echo); err != nil || echo.From != "assistant" || echo.Params["a"] != float64(1) {
		t.Fatalf("a registered method: %+v %v", echo, err)
	}
	_ = r.peer.Notify("test.event", map[string]any{"n": "after"})
	_ = r.peer.Notify("no.such.event", map[string]any{})
	eventually(t, 5*time.Second, "the event after the handshake", func() bool {
		busTestEvents.Lock()
		defer busTestEvents.Unlock()
		return slices.Equal(busTestEvents.got, []string{`assistant {"n":"after"}`})
	})

	// The hub calls the service: its answer, its own error code, an event.
	ctx := context.Background()
	var out struct {
		Got map[string]any `json:"got"`
	}
	if err := h.callBus(ctx, "assistant", "echo", map[string]any{"text": "你好"}, &out); err != nil || out.Got["text"] != "你好" {
		t.Fatalf("echo: %+v %v", out, err)
	}
	if err := h.callBus(ctx, "assistant", "other", nil, nil); rpcCode(err) != "no_such_thing" {
		t.Fatalf("the service's error: %v", err)
	}
	if err := h.callBus(ctx, "nobody", "echo", nil, nil); !errors.Is(err, errBusOffline) {
		t.Fatalf("an identity that is not connected: %v", err)
	}

	// bus.Client the same way, and the hub's event reaches it.
	deskTok := busToken(t, h, "desk", "")
	f := startService(t, srv, deskTok, "manager.hello", map[string]any{"id": "desk"})
	if err := f.c.Wait(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}
	if err := f.c.Call(ctxTimeout(t), "test.echo", nil, &echo); err != nil || echo.From != "desk" {
		t.Fatalf("a method through bus.Client: %+v %v", echo, err)
	}
	if err := h.notifyBus("desk", "hub.event", map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if e := f.event(t, "hub.event "); e != `hub.event {"x":1}` {
		t.Fatalf("event %q", e)
	}
	if err := h.callBus(ctx, "desk", "echo", map[string]any{"a": true}, &out); err != nil || out.Got["a"] != true {
		t.Fatalf("echo through bus.Client: %+v %v", out, err)
	}
	_ = r.ws.CloseNow()
	eventually(t, 5*time.Second, "the closed connection leaves the roster", func() bool {
		return errors.Is(h.callBus(ctx, "assistant", "echo", nil, nil), errBusOffline)
	})
}

func ctxTimeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// A connection that does not say its handshake in time is hung up.
func TestBusHelloTimeout(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.bus.helloTimeout = 150 * time.Millisecond
	tok := busToken(t, h, "assistant", "")
	start := time.Now()
	r := dialRawBus(t, srv, tok)
	r.closedWith(t, bus.CloseRefused)
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("hung up after %s", d)
	}
	eventually(t, 5*time.Second, "no connection left", func() bool {
		h.bus.mu.Lock()
		defer h.bus.mu.Unlock()
		return len(h.bus.live) == 0
	})
}

// The hub pings: a service that answers stays connected, one that stops answering is dropped.
func TestBusPing(t *testing.T) {
	h, srv, _ := newBusHub(t)
	h.bus.pingEvery, h.bus.pingTimeout = 40*time.Millisecond, 100*time.Millisecond
	tok := busToken(t, h, "assistant", "")
	f := startService(t, srv, tok, "manager.hello", map[string]any{"id": "assistant"})
	if err := f.c.Wait(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond) // many pings
	if n := f.count.Load(); n != 1 || len(h.busRoster()) != 1 {
		t.Fatalf("%d handshakes, roster %+v", n, h.busRoster())
	}

	// A service that says hello and then stops reading never answers a ping.
	deaf := busToken(t, h, "deaf", "")
	ctx := ctxTimeout(t)
	ws, err := bus.Dial(ctx, srv.URL, deaf, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"id":"s1","method":"manager.hello","params":{"id":"deaf"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := ws.Read(ctx); err != nil || !strings.Contains(string(msg), `"result"`) {
		t.Fatalf("hello answer %s %v", msg, err)
	}
	eventually(t, 5*time.Second, "the deaf service dropped", func() bool {
		return h.busConnOf("deaf") == nil
	})
	if h.busConnOf("assistant") == nil {
		t.Fatal("the answering service was dropped too")
	}
}

// bus.Client comes back after the hub drops it; the hub's shutdown closes the connections as going away and takes
// no new ones.
func TestBusClientReconnectsAndShutdown(t *testing.T) {
	h, srv, _ := newBusHub(t)
	tok := busToken(t, h, "assistant", "")
	f := startService(t, srv, tok, "manager.hello", map[string]any{"id": "assistant"})
	if err := f.c.Wait(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}
	h.busHangUp("assistant", websocket.StatusGoingAway, "test")
	eventually(t, 5*time.Second, "a second handshake", func() bool { return f.count.Load() == 2 && h.busConnOf("assistant") != nil })

	r := dialRawBus(t, srv, tok)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.stopWebSockets(ctx); err != nil {
		t.Fatal(err)
	}
	r.closedWith(t, websocket.StatusGoingAway)
	if _, err := bus.Dial(ctx, srv.URL, tok, nil); err == nil || errors.Is(err, bus.ErrRejected) {
		t.Fatalf("dialing a stopping hub: %v", err)
	}
}

// From the internet the bus is refused over plaintext, and failed tokens count against the source as on the apps' API.
func TestBusFromTheInternet(t *testing.T) {
	h, _, _ := newBusHub(t)
	h.cfg.Security.AuthFailures = 1
	mux := h.routes()
	requireCode(t, securityRequest(mux, "GET", bus.Path, "jb_wrong", "", "198.51.100.1:1", false), 403, "tls_required")
	requireCode(t, securityRequest(mux, "GET", bus.Path, "jb_wrong", "", "198.51.100.1:1", true), 401, "")
	requireCode(t, securityRequest(mux, "GET", bus.Path, "jb_wrong", "", "198.51.100.1:1", true), 429, "rate_limited")
	requireCode(t, securityRequest(mux, "GET", bus.Path, "jb_wrong", "", "127.0.0.1:1", false), 401, "")
}
