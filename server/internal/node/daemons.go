package node

// Daemons (docs/integrations/daemons.md): agent runtimes that are long-running services of their own — another system user, a
// container, another machine — and so cannot reach runtime.sock. The node dials them instead: each one in
// ~/.persona/daemons.json (beside runtime.sock) is a WebSocket endpoint with a bearer token, and once connected it speaks
// the runtime protocol unchanged (docs/protocol/protocol.md "Runtime ↔ node"), one JSON object per text message. Nothing here
// knows any particular daemon: an integration implements the spec on its side (the runtime SDK's listen mode does it
// for JS).
//
// A daemon is trusted less than a runtime on the socket (the same user, by the socket's mode). Between it and the
// runtime host sits a link that keeps it to its own corner:
//   - its runtime id, name and launcher are the config's, not what it says (two daemons cannot collide, and the
//     config's ids are reserved against runtimes on the socket: runtimeRegistry.admit);
//   - its session ids reach the hub as "<id>.<its id>" and its interaction ids are replaced, so its sessions never
//     collide with this machine's or another runtime's — and a call about a session without its prefix (the node
//     asking every runtime who has an unknown session) is answered by the link, never shown to the daemon;
//   - only the protocol's caps (start, send, interrupt, rename, close), no mcp.* (phone and Persona tools), and no
//     pane_id / resume_argv / transcript (a pane to claim, a command to run in the terminal engine, a transcript to read).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/rpc"
)

// DaemonSubprotocol is the WebSocket subprotocol a daemon's endpoint must answer with.
const DaemonSubprotocol = "persona-runtime.v1"

var (
	// daemonIDRe: a daemon's id in daemons.json — its runtime id, space kind and session id prefix. No dots: the first
	// dot of a session id ends the daemon's id.
	daemonIDRe = lazyre.New(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	// daemonLauncherRe: a launcher name given in daemons.json.
	daemonLauncherRe = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

	// daemonDialTimeout bounds the handshake.
	daemonDialTimeout = 15 * time.Second
	// daemonCallTimeout bounds a node → daemon call the link forwards (the node's own calls time out sooner).
	daemonCallTimeout = 2 * time.Minute
)

// daemonTimings: how often daemons.json is read again (a changed file takes effect without a restart), the first and
// the longest wait between dials of a daemon that is not there, how often a quiet connection is checked and how long
// its pong may take.
type daemonTimings struct {
	reload, backoffMin, backoffMax, ping, pingWait time.Duration
}

var defaultDaemonTimings = daemonTimings{reload: 10 * time.Second, backoffMin: time.Second, backoffMax: time.Minute,
	ping: 30 * time.Second, pingWait: 15 * time.Second}

func (rh *RuntimeHost) daemonTimings() daemonTimings {
	if rh.dtimes.reload == 0 {
		return defaultDaemonTimings
	}
	return rh.dtimes
}

// daemonMaxMessage bounds one message from a daemon (a page with images in it), as the socket's line limit does.
const daemonMaxMessage = 64 << 20

// daemonCaps are the capabilities a daemon may declare; anything a later protocol adds is off for daemons until it is
// allowed here on purpose.
var daemonCaps = []string{"start", "send", "interrupt", "rename", "close", "execution"}

// daemonMethods are the node → runtime calls a link forwards to its daemon.
var daemonMethods = map[string]bool{"session.items": true, "session.image": true, "session.item": true, "session.watch": true,
	"session.unwatch": true, "session.send": true, "session.interrupt": true, "session.rename": true, "session.close": true,
	"session.start": true, "session.execution": true}

// daemonConfig is one daemon in daemons.json.
type daemonConfig struct {
	// ID names it here: its runtime id, its sessions' space kind and id prefix. Chosen by the user, so two daemons
	// (or a daemon and a runtime on the socket) do not collide whatever they call themselves.
	ID string `json:"id"`
	// Name is what the apps call it (its sessions' agent label); default what it says, else ID.
	Name string `json:"name,omitempty"`
	// URL is its endpoint: ws:// or wss:// (http:// and https:// mean the same).
	URL       string `json:"url"`
	Token     string `json:"token,omitempty"`
	TokenFile string `json:"token_file,omitempty"`
	// Agent, when set, is the agent its sessions run, whatever it says.
	Agent string `json:"agent,omitempty"`
	// Launcher names its entry in the new-session form when it can start sessions; default ID.
	Launcher string `json:"launcher,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// validate checks one entry; the error says which field is wrong, never a value of the token.
func (c *daemonConfig) validate() error {
	c.ID = strings.TrimSpace(c.ID)
	if !daemonIDRe.MatchString(c.ID) {
		return fmt.Errorf("id %q: lowercase letters, digits, _ and -, at most 32, no dots", c.ID)
	}
	c.Name = strings.TrimSpace(c.Name)
	if len([]rune(c.Name)) > 64 {
		return fmt.Errorf("daemon %s: name longer than 64", c.ID)
	}
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || u.Host == "" {
		return fmt.Errorf("daemon %s: url is not a ws:// or wss:// address", c.ID)
	}
	switch u.Scheme {
	case "ws", "wss", "http", "https":
	default:
		return fmt.Errorf("daemon %s: url is not a ws:// or wss:// address", c.ID)
	}
	if u.User != nil {
		return fmt.Errorf("daemon %s: put the credential in token or token_file, not in the url", c.ID)
	}
	c.URL = u.String()
	if (c.Token == "") == (c.TokenFile == "") {
		return fmt.Errorf("daemon %s: give one of token and token_file", c.ID)
	}
	if c.Agent = strings.TrimSpace(c.Agent); c.Agent != "" && !runtimeIDRe.MatchString(c.Agent) {
		return fmt.Errorf("daemon %s: bad agent %q", c.ID, c.Agent)
	}
	if c.Launcher = strings.TrimSpace(c.Launcher); c.Launcher != "" && !daemonLauncherRe.MatchString(c.Launcher) {
		return fmt.Errorf("daemon %s: bad launcher %q", c.ID, c.Launcher)
	}
	return nil
}

// token is the bearer token, read for every dial (a rotated token_file is picked up).
func (c *daemonConfig) token() (string, error) {
	if c.Token != "" {
		return strings.TrimSpace(c.Token), nil
	}
	path := c.TokenFile
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, rest)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("token_file: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", errors.New("token_file is empty")
	}
	return t, nil
}

// endpoint is the url without its path and query, for logs.
func (c *daemonConfig) endpoint() string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return "?"
	}
	return u.Scheme + "://" + u.Host
}

// parseDaemons reads daemons.json: the valid, enabled entries by id, and what was wrong with the others (each
// refused entry is left out; the rest still connect).
func parseDaemons(data []byte) (map[string]daemonConfig, []error) {
	var f struct {
		Daemons []daemonConfig `json:"daemons"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, []error{fmt.Errorf("daemons.json: %w", err)}
	}
	out := map[string]daemonConfig{}
	var errs []error
	// seen counts disabled entries too: a disabled first entry must not let a second one with its id connect.
	seen := map[string]bool{}
	for _, c := range f.Daemons {
		if err := c.validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[c.ID] {
			errs = append(errs, fmt.Errorf("daemon %s is listed twice; neither connects", c.ID))
			delete(out, c.ID)
			continue
		}
		seen[c.ID] = true
		if !c.Disabled {
			out[c.ID] = c
		}
	}
	return out, errs
}

// daemonRun is one daemon being dialed.
type daemonRun struct {
	cfg    daemonConfig
	cancel context.CancelFunc
	done   chan struct{}
}

// daemonsPath is the config beside the runtime socket: ~/.persona/daemons.json for the node's own socket.
func (rh *RuntimeHost) daemonsPath() string {
	return filepath.Join(filepath.Dir(rh.path), "daemons.json")
}

// reservedID: id belongs to a daemon in the config (a runtime on the socket may not use it).
func (rh *RuntimeHost) reservedID(id string) bool {
	rh.dmu.Lock()
	defer rh.dmu.Unlock()
	_, ok := rh.daemons[id]
	return ok
}

// startDaemons dials the configured daemons until ctx ends, reading the config again every so often (daemonTimings).
func (rh *RuntimeHost) startDaemons(ctx context.Context) {
	rh.syncDaemons(ctx)
	go func() {
		t := time.NewTicker(rh.daemonTimings().reload)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rh.syncDaemons(ctx)
			}
		}
	}()
}

// syncDaemons applies the config if it changed: daemons that went away or changed are disconnected, new ones dialed.
func (rh *RuntimeHost) syncDaemons(ctx context.Context) {
	path := rh.daemonsPath()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		rh.logger.Printf("daemons: %v", err)
		return
	}
	sum := sha256.Sum256(data)
	rh.dmu.Lock()
	if rh.daemonsSum == sum && rh.daemons != nil {
		rh.dmu.Unlock()
		return
	}
	rh.daemonsSum = sum
	rh.dmu.Unlock()

	want := map[string]daemonConfig{}
	if len(data) > 0 {
		var errs []error
		want, errs = parseDaemons(data)
		for _, e := range errs {
			rh.logger.Printf("daemons: %v", e)
		}
		if want == nil {
			want = map[string]daemonConfig{}
		}
		if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
			for _, c := range want {
				if c.Token != "" {
					rh.logger.Printf("daemons: %s holds a token and others can read it; chmod 600 it", path)
					break
				}
			}
		}
	}

	rh.dmu.Lock()
	if rh.daemons == nil {
		rh.daemons = map[string]*daemonRun{}
	}
	var stop []*daemonRun
	for id, r := range rh.daemons {
		if c, ok := want[id]; !ok || c != r.cfg {
			stop = append(stop, r)
			delete(rh.daemons, id)
		}
	}
	var start []daemonConfig
	for id, c := range want {
		if _, ok := rh.daemons[id]; !ok {
			start = append(start, c)
		}
	}
	sort.Slice(start, func(i, j int) bool { return start[i].ID < start[j].ID })
	for _, c := range start {
		dctx, cancel := context.WithCancel(ctx)
		r := &daemonRun{cfg: c, cancel: cancel, done: make(chan struct{})}
		rh.daemons[c.ID] = r // reserved from now on
		var prev *daemonRun
		for _, s := range stop {
			if s.cfg.ID == c.ID {
				prev = s
			}
		}
		go func() {
			// A changed daemon's old connection leaves first: the new one would find its own id still taken.
			if prev != nil {
				<-prev.done
			}
			rh.runDaemon(dctx, r)
		}()
	}
	rh.dmu.Unlock()
	for _, r := range stop {
		r.cancel()
		rh.logger.Printf("daemon %s: config changed or removed, disconnecting", r.cfg.ID)
	}
}

// runDaemon dials one daemon until ctx ends, backing off while it is not there.
func (rh *RuntimeHost) runDaemon(ctx context.Context, r *daemonRun) {
	defer close(r.done)
	c := r.cfg
	tm := rh.daemonTimings()
	wait := tm.backoffMin
	lastErr := ""
	for ctx.Err() == nil {
		began := time.Now()
		connected, err := rh.dialDaemon(ctx, c)
		if ctx.Err() != nil {
			return
		}
		if connected {
			lastErr = ""
			if time.Since(began) > 30*time.Second {
				wait = tm.backoffMin // it was up a while: a drop, not a daemon refusing us
			}
			rh.logger.Printf("daemon %s: disconnected: %v", c.ID, err)
		} else if msg := fmt.Sprint(err); msg != lastErr {
			lastErr = msg // the same failure again is not logged again
			rh.logger.Printf("daemon %s (%s): %v; retrying", c.ID, c.endpoint(), err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, tm.backoffMax)
	}
}

// daemonHTTP is the client the dials use: HTTP/1.1 only (a WebSocket upgrade does not go over HTTP/2).
var daemonHTTP = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ForceAttemptHTTP2 = false
	t.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	return &http.Client{Transport: t}
}()

// dialDaemon connects to one daemon and serves it as a runtime until the connection ends. connected: the handshake
// went through (the error is then why it ended).
func (rh *RuntimeHost) dialDaemon(ctx context.Context, c daemonConfig) (connected bool, err error) {
	token, err := c.token()
	if err != nil {
		return false, err
	}
	dctx, cancel := context.WithTimeout(ctx, daemonDialTimeout)
	ws, resp, err := websocket.Dial(dctx, c.URL, &websocket.DialOptions{HTTPClient: daemonHTTP,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}, Subprotocols: []string{DaemonSubprotocol, compat.RuntimeProtocol}})
	cancel()
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) {
			return false, fmt.Errorf("the daemon refused the token (HTTP %d)", resp.StatusCode)
		}
		if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
			return false, fmt.Errorf("no WebSocket endpoint there (HTTP %d)", resp.StatusCode)
		}
		return false, err
	}
	if ws.Subprotocol() != DaemonSubprotocol && ws.Subprotocol() != compat.RuntimeProtocol {
		ws.Close(websocket.StatusPolicyViolation, "expected subprotocol "+DaemonSubprotocol)
		return false, fmt.Errorf("the endpoint did not answer with subprotocol %s", DaemonSubprotocol)
	}
	ws.SetReadLimit(daemonMaxMessage)
	rh.logger.Printf("daemon %s: connected to %s", c.ID, c.endpoint())

	lctx, lcancel := context.WithCancel(ctx)
	defer lcancel()
	nodeEnd, linkEnd := net.Pipe()
	stream := &wsStream{c: ws, ctx: lctx}
	l := &daemonLink{cfg: c}
	l.dp = rpc.New("n", stream)
	l.rp = rpc.New("d", linkEnd)
	l.dp.OnRequest = l.fromDaemon
	l.dp.OnEvent = l.fromDaemonEvent
	l.rp.OnRequest = l.toDaemon
	l.rp.OnEvent = l.toDaemonEvent
	var why error
	var whyOnce sync.Once
	end := func(err error) {
		whyOnce.Do(func() { why = err })
		lcancel()
	}
	l.fail = end
	stop := context.AfterFunc(lctx, func() {
		_ = linkEnd.Close()
		_ = nodeEnd.Close()
		_ = ws.Close(websocket.StatusNormalClosure, "")
	})
	defer stop()
	go func() { end(l.dp.Serve(lctx, stream)) }()
	go func() { _ = l.rp.Serve(lctx, linkEnd); end(errors.New("the node ended the connection")) }()
	tm := rh.daemonTimings()
	go func() {
		t := time.NewTicker(tm.ping)
		defer t.Stop()
		for {
			select {
			case <-lctx.Done():
				return
			case <-t.C:
			}
			pctx, cancel := context.WithTimeout(lctx, tm.pingWait)
			err := ws.Ping(pctx)
			cancel()
			if err != nil && lctx.Err() == nil {
				end(fmt.Errorf("no pong: %w", err))
				return
			}
		}
	}()
	rh.serveConn(lctx, nodeEnd, c.ID)
	end(errors.New("closed"))
	l.dp.Close(io.EOF)
	l.rp.Close(io.EOF)
	return true, why
}

// wsStream is a WebSocket as the JSON-lines stream internal/rpc reads and writes: every message read is followed by a
// newline, every line written goes out as one text message (without its newline).
type wsStream struct {
	c   *websocket.Conn
	ctx context.Context
	r   io.Reader // the message being read
	nl  bool      // a message ended: its newline is next
}

func (s *wsStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if s.nl {
			s.nl = false
			p[0] = '\n'
			return 1, nil
		}
		if s.r == nil {
			_, r, err := s.c.Reader(s.ctx)
			if err != nil {
				return 0, err
			}
			s.r = r
		}
		n, err := s.r.Read(p)
		// One message is one JSON object, which may span lines (pretty-printed); the peer reads a line per object.
		// A raw newline in JSON can only be whitespace (never inside a string), so a space means the same.
		for i, b := range p[:n] {
			if b == '\n' {
				p[i] = ' '
			}
		}
		if errors.Is(err, io.EOF) {
			s.r, s.nl = nil, true
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (s *wsStream) Write(p []byte) (int, error) {
	msg := bytes.TrimRight(p, "\n")
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	if err := s.c.Write(ctx, websocket.MessageText, msg); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *wsStream) Close() error { return s.c.Close(websocket.StatusNormalClosure, "") }

// daemonLink stands between a daemon (dp, over its WebSocket) and the runtime host (rp, over a pipe the host serves
// as one more runtime connection), rewriting what crosses: the config's names, namespaced ids, and nothing the daemon
// may not do.
type daemonLink struct {
	cfg daemonConfig
	dp  *rpc.Peer
	rp  *rpc.Peer
	// fail ends the connection (a refused hello: the daemon dials in again later rather than sit there unlisted).
	fail func(error)
}

// outer is the id the hub sees for the daemon's session id: "<daemon>.<id>"; false when either is not a valid id.
// A subagent of one of its sessions, "<id>@<child>", becomes "<daemon>.<id>@<child>".
func (l *daemonLink) outer(id string) (string, bool) {
	if parent, child, ok := splitRuntimeChild(id); ok {
		out, ok := l.outer(parent)
		return out + "@" + child, ok
	}
	if !runtimeIDRe.MatchString(id) {
		return "", false
	}
	out := l.cfg.ID + "." + id
	return out, runtimeIDRe.MatchString(out)
}

// inner is the daemon's own id for a session id the hub uses; false when it is not this daemon's.
func (l *daemonLink) inner(id string) (string, bool) {
	if parent, child, ok := splitRuntimeChild(id); ok {
		in, ok := l.inner(parent)
		return in + "@" + child, ok
	}
	rest, ok := strings.CutPrefix(id, l.cfg.ID+".")
	if !ok || !runtimeIDRe.MatchString(rest) {
		return "", false
	}
	return rest, true
}

// interactionID replaces an interaction id the daemon chose: stable (the same card when it asks again through the next
// connection), and never one of another runtime's.
func (l *daemonLink) interactionID(id string) string {
	sum := sha256.Sum256([]byte(l.cfg.ID + "\x00" + id))
	return "dx_" + hex.EncodeToString(sum[:])[:16]
}

func errUnsupportedForDaemons(what string) error {
	return &rpc.Error{Message: "unsupported: daemons get no " + what, Code: "unsupported"}
}

// fromDaemon answers the daemon's calls: hello in the config's names, its asks with its ids made the hub's.
func (l *daemonLink) fromDaemon(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "runtime.hello":
		var in map[string]any
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fmt.Errorf("bad runtime.hello: %w", err)
		}
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var res map[string]any
		if err := l.rp.Call(hctx, "runtime.hello", l.hello(in), &res); err != nil {
			if l.fail != nil {
				time.AfterFunc(500*time.Millisecond, func() { l.fail(fmt.Errorf("hello refused: %w", err)) }) // after the answer is out
			}
			return nil, err
		}
		// resume_argv, handover, transcript, codex_thread and unlisted_transcripts are for runtimes on the socket: what
		// a daemon lists offline is not resumed in a terminal here, a send it refuses is not handed to another runtime
		// (the link turns that code away), and its sessions are not read from this machine's transcripts, nor are its
		// Codex threads or its past sessions' transcripts this machine's. launchers, modes and resume need what the link
		// does not pass on (more launchers than the config's one, caps beyond daemonCaps).
		if f, ok := res["features"].([]any); ok {
			kept := []any{}
			for _, x := range f {
				if !slices.Contains([]any{"resume_argv", runtimeHandover, "transcript", "launchers", "modes", "resume", "codex_thread", "unlisted_transcripts"}, x) {
					kept = append(kept, x)
				}
			}
			res["features"] = kept
		}
		return res, nil
	case "interaction.create":
		var p map[string]json.RawMessage
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("bad interaction: %w", err)
		}
		var ix map[string]json.RawMessage
		if err := json.Unmarshal(p["interaction"], &ix); err != nil || ix == nil {
			return nil, errors.New("bad interaction")
		}
		var sid, id string
		_ = json.Unmarshal(ix["session_id"], &sid)
		_ = json.Unmarshal(ix["id"], &id)
		outer, ok := l.outer(sid)
		if !ok {
			return nil, fmt.Errorf("bad session id %q", sid)
		}
		ix["session_id"] = rawJSON(outer)
		if id != "" {
			ix["id"] = rawJSON(l.interactionID(id))
		}
		p["interaction"] = rawJSON(ix)
		var res json.RawMessage
		err := l.rp.Call(ctx, "interaction.create", p, &res)
		return res, err
	case "mcp.tools":
		return map[string]any{"tools": []any{}}, nil
	case "mcp.call":
		return nil, errUnsupportedForDaemons("Persona tools")
	}
	return nil, &rpc.Error{Message: "unsupported: unknown method " + method, Code: "unsupported"}
}

// hello is what the host hears: the config's id, name, agent and launcher; only the caps daemons may have.
func (l *daemonLink) hello(in map[string]any) map[string]any {
	str := func(k string) string {
		s, _ := in[k].(string)
		return strings.TrimSpace(s)
	}
	// Its instance is the link's: a daemon's next connection is the same runtime again (the node closes the stale one),
	// whatever the daemon says about its process.
	out := map[string]any{"runtime": l.cfg.ID, "instance": "daemon:" + l.cfg.ID}
	name := l.cfg.Name
	if name == "" {
		name = str("name")
	}
	if name == "" || len([]rune(name)) > 64 {
		name = l.cfg.ID
	}
	out["name"] = name
	if agent := l.cfg.Agent; agent != "" {
		out["agent"] = agent
	} else if agent = str("agent"); agent != "" {
		out["agent"] = agent
	}
	if v := str("version"); v != "" {
		out["version"] = v
	}
	given, _ := in["caps"].(map[string]any)
	caps := map[string]any{}
	for _, k := range daemonCaps {
		b, _ := given[k].(bool)
		caps[k] = b
	}
	out["caps"] = caps
	if caps["start"] == true && (l.cfg.Launcher != "" || str("launcher") != "") {
		launcher := l.cfg.Launcher
		if launcher == "" {
			launcher = l.cfg.ID // the daemon's own name for it could be anyone's
		}
		out["launcher"] = launcher
		if m := str("launcher_model"); m != "" {
			out["launcher_model"] = m
		}
	}
	return out
}

// row makes one of the daemon's session rows the hub's: its id namespaced, its space too.
func (l *daemonLink) row(s map[string]json.RawMessage) (map[string]json.RawMessage, bool) {
	var id string
	_ = json.Unmarshal(s["session_id"], &id)
	outer, ok := l.outer(id)
	if !ok {
		return nil, false
	}
	s["session_id"] = rawJSON(outer)
	// No terminal of this machine's, and no name but the config's (the node labels its sessions).
	for _, k := range []string{"pane_id", "resume_argv", "transcript", "codex_thread", "tab_id", "workspace_id", "agent_label"} {
		delete(s, k)
	}
	var space string
	if json.Unmarshal(s["space_id"], &space) == nil && space != "" {
		s["space_id"] = rawJSON(l.cfg.ID + ":" + space) // the apps group by space id: not with another runtime's
	}
	// The session that started it (docs/integrations/runtimes.md, a row of its own with a parent) is another of the
	// daemon's: named by its id here too, or not at all.
	for _, k := range []string{"parent_session_id", "root_session_id", "asking_parent_session_id"} {
		if _, named := s[k]; !named {
			continue
		}
		var ref string
		if json.Unmarshal(s[k], &ref) == nil && ref != "" {
			if o, ok := l.outer(ref); ok {
				s[k] = rawJSON(o)
				continue
			}
		}
		delete(s, k)
	}
	return s, true
}

// fromDaemonEvent passes the daemon's pushes on, its ids made the hub's.
func (l *daemonLink) fromDaemonEvent(event string, params json.RawMessage) {
	switch event {
	case "runtime.sessions":
		var p struct {
			Sessions []map[string]json.RawMessage `json:"sessions"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		out := make([]map[string]json.RawMessage, 0, len(p.Sessions))
		for _, s := range p.Sessions {
			if s == nil {
				continue
			}
			if r, ok := l.row(s); ok {
				out = append(out, r)
			}
		}
		_ = l.rp.Notify("runtime.sessions", map[string]any{"sessions": out})
	case "session.items", "session.delta":
		var p map[string]json.RawMessage
		if json.Unmarshal(params, &p) != nil || p == nil {
			return
		}
		var id string
		_ = json.Unmarshal(p["session_id"], &id)
		outer, ok := l.outer(id)
		if !ok {
			return
		}
		p["session_id"] = rawJSON(outer)
		_ = l.rp.Notify(event, p)
	case "interaction.cancel":
		var p struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(params, &p) != nil || p.ID == "" {
			return
		}
		_ = l.rp.Notify("interaction.cancel", map[string]any{"id": l.interactionID(p.ID)})
	}
}

// toDaemon forwards the node's call about one of the daemon's sessions, with the daemon's own id. A session id
// without its prefix is not its: answered here, the daemon never sees it.
func (l *daemonLink) toDaemon(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if !daemonMethods[method] {
		return nil, &rpc.Error{Message: "unsupported: " + method, Code: "unsupported"}
	}
	p := map[string]json.RawMessage{}
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("bad %s: %w", method, err)
		}
	}
	if method != "session.start" {
		var id string
		_ = json.Unmarshal(p["session_id"], &id)
		inner, ok := l.inner(id)
		if !ok {
			return nil, &rpc.Error{Message: "not a session of daemon " + l.cfg.ID, Code: "not_found"}
		}
		p["session_id"] = rawJSON(inner)
	}
	cctx, cancel := context.WithTimeout(ctx, daemonCallTimeout)
	defer cancel()
	var out json.RawMessage
	if err := l.dp.Call(cctx, method, p, &out); err != nil {
		// A send handed over goes to another runtime that answers for the session (Node.handOver): never out of the
		// daemon's corner. It is refused here instead, with nothing sent.
		if rpcCode(err) == runtimeHandover {
			return nil, refusal("not_running", fmt.Sprintf("daemon %s did not take the message; nothing was sent", l.cfg.ID), nil)
		}
		return nil, err
	}
	if method != "session.start" {
		return out, nil
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(out, &res) != nil {
		return nil, fmt.Errorf("daemon %s: bad session.start answer", l.cfg.ID)
	}
	var s map[string]json.RawMessage
	if json.Unmarshal(res["session"], &s) != nil || s == nil {
		return nil, fmt.Errorf("daemon %s started a session without a valid id", l.cfg.ID)
	}
	r, ok := l.row(s)
	if !ok {
		return nil, fmt.Errorf("daemon %s started a session without a valid id", l.cfg.ID)
	}
	res["session"] = rawJSON(r)
	return res, nil
}

// toDaemonEvent passes on what the node tells every runtime (whether a hub is connected).
func (l *daemonLink) toDaemonEvent(event string, params json.RawMessage) {
	if event == "runtime.hub" {
		_ = l.dp.Notify(event, params)
	}
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
