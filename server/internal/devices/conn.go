package devices

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A machine's connection to its node, over one of its paths at a time (paths.go): the run loop picks the best
// path that can be tried, connects (establish: the node answers node.info), serves the connection until it ends
// (serve), and starts over. While a connection is up, a path that stays clearly better for mesh.path_switch_seconds is
// connected next to it; once its node answers, it takes over and the old one is let go — the apps see no offline in
// between.

var errSwitched = errors.New("moved to a better path")

// nodeConn is one connection to the node, its node.info answered.
type nodeConn struct {
	path   *nodePath
	via    string // the transport it took: local | ssh | tailcat | link | service | hub
	pipe   *nodePipe
	peer   *rpc.Peer
	info   proto.NodeInfo
	served chan error
	cancel context.CancelFunc
}

func (c *nodeConn) close() {
	c.peer.Close(errors.New("closed"))
	c.cancel()
	c.pipe.close()
}

// run keeps the machine connected through its best path until ctx ends.
func (m *Machine) run(ctx context.Context) {
	var conn *nodeConn
	for ctx.Err() == nil {
		if conn == nil {
			if why := m.d.parkReason(m); why != "" {
				if why == "removed" {
					return
				}
				m.park(why)
				m.waitChange(ctx, 0)
				continue
			}
			if d := m.d.meshSettling(); d > 0 {
				// Just started with peers: the group may have another center by now (a larger term); a member must not
				// grab its computers before it hears so.
				m.park("等组里的其他 hub 连上")
				m.waitChange(ctx, d)
				continue
			}
			m.unpark()
			p, wait := m.d.choosePath(m)
			if p == nil {
				m.noPath()
				m.waitChange(ctx, wait)
				continue
			}
			c, err := m.establish(ctx, p)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				m.d.pathEnded(p, false, err, 0, false)
				m.markOffline(err)
				continue
			}
			conn = c
		}
		start := time.Now()
		err := m.serve(ctx, conn)
		next := m.takeNext()
		planned := next != nil
		m.d.pathEnded(conn.path, true, err, time.Since(start), planned)
		conn.close()
		m.detach(err, planned)
		conn = next
	}
	if conn != nil {
		conn.close()
	}
	if next := m.takeNext(); next != nil { // closing waits for its process (open): not under m.mu
		next.close()
	}
}

// wake makes the run loop look again (its paths changed, a link came, the hub's role changed).
func (m *Machine) wake() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// waitChange waits for a reason to look again: a kick, a change of the machines or their paths, or (wait > 0) that
// long; at most a minute.
func (m *Machine) waitChange(ctx context.Context, wait time.Duration) {
	if wait <= 0 || wait > time.Minute {
		wait = time.Minute
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-m.kick:
	case <-m.d.fleetChanged():
	case <-m.d.meshWait():
	case <-t.C:
	}
}

// park: this machine is not connected from here now (why), and says so.
func (m *Machine) park(why string) {
	m.firstTried() // not connected from here: nothing to wait for
	m.mu.Lock()
	changed := m.parked != why
	m.parked = why
	if changed {
		m.err = why
	}
	m.mu.Unlock()
	if changed {
		m.d.changed()
	}
}

func (m *Machine) unpark() {
	m.mu.Lock()
	was := m.parked != ""
	m.parked = ""
	if was {
		m.err = ""
	}
	m.mu.Unlock()
}

// noPath: no path can be tried now; the machine shows why the last one failed (or what it waits for).
func (m *Machine) noPath() {
	m.firstTried()
	why := ""
	for _, p := range m.d.pathsOf(m) {
		if st := m.d.pathState(p); st.err != "" {
			why = st.err
			break
		}
	}
	if why == "" {
		return
	}
	m.mu.Lock()
	changed := !m.online && m.err != why
	if changed {
		m.err, m.dialErrAt = why, time.Now()
	}
	m.mu.Unlock()
	if changed {
		m.d.changed()
	}
}

// establish connects through p: each of its transports in turn (ssh, then tailcat), until the node answers node.info.
func (m *Machine) establish(ctx context.Context, p *nodePath) (*nodeConn, error) {
	vias := []string{viaHub}
	if p.src != nil {
		vias = p.src.transports()
	}
	var failed []string
	for _, via := range vias {
		c, err := m.establishVia(ctx, p, via)
		if err == nil {
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		failed = append(failed, via+"："+err.Error())
		if via == "ssh" && p.src.cfg.TailcatAddr != "" {
			p.src.noteDirectFailed()
		}
	}
	if len(failed) == 1 {
		_, msg, _ := strings.Cut(failed[0], "：")
		return nil, errors.New(msg)
	}
	return nil, errors.New(strings.Join(failed, "；"))
}

func (m *Machine) establishVia(ctx context.Context, p *nodePath, via string) (*nodeConn, error) {
	cctx, cancel := context.WithCancel(ctx)
	var l *nodePipe
	var err error
	if via == viaHub {
		l, err = m.d.openThroughHub(cctx, p.hub, p.mid)
	} else {
		l, err = p.src.open(cctx, via)
	}
	if err != nil {
		cancel()
		if via == "tailcat" {
			p.src.tailcatReset()
		}
		return nil, err
	}
	peer := rpc.New("h", l.w)
	// A model request sent through this computer stops there when the manager stops waiting (an older node ignores it).
	peer.Cancels = map[string]bool{proto.MethodModelHTTP: true}
	// Every event goes on to the hub, the forwarded ports' bytes among them (the hub is their end, ports.go there).
	peer.OnEvent = m.onEvent
	peer.OnRequest = m.onRequest
	c := &nodeConn{path: p, via: via, pipe: l, peer: peer, served: make(chan error, 1), cancel: cancel}
	go func() { c.served <- peer.Serve(cctx, l.r) }()

	ictx, icancel := context.WithTimeout(cctx, 40*time.Second)
	// The engine first, before anything else the node is asked: it then never lists or watches the wrong one.
	m.useEngine(ictx, peer)
	err = peer.Call(ictx, "node.info", map[string]any{}, &c.info)
	icancel()
	if err != nil {
		c.close()
		if via == "tailcat" {
			p.src.tailcatReset()
		}
		if msg := l.lastErr(); msg != "" {
			return nil, fmt.Errorf("%s", msg)
		}
		return nil, err
	}
	return c, nil
}

// serve makes c the machine's connection and serves it until it ends (or a better path took over: m.next).
func (m *Machine) serve(ctx context.Context, c *nodeConn) error {
	p := c.path
	m.d.pathConnected(p)
	m.mu.Lock()
	m.peer, m.info, m.online, m.err, m.lastSeen = c.peer, c.info, true, "", time.Now().UTC()
	m.connectedAt = m.lastSeen
	m.forgetOpenedLocked()     // a new connection has nothing of the hub's open yet
	m.herdrAsked = time.Time{} // node.info just said; this node may answer herdr.server.status where the last did not
	m.endWarmLocked()
	m.via, m.path = c.via, ""
	m.active, m.transport, m.uid = p, c.via, c.info.MachineUID
	if c.via == "ssh" && p.src == m {
		m.directFailed = time.Time{}
	}
	onlineAt := time.Now()
	rewatch := map[string]int{}
	updates := false // listed: a queued update and a build check follow (below)
	for id := range m.watched {
		rewatch[id] = m.lastSeq[id]
	}
	for id := range m.followed { // the manager's own follows (Follow)
		rewatch[id] = m.lastSeq[id]
	}
	for id := range m.unsettled { // a new connection watches nothing yet: an unwatch it did not take is moot
		if !m.followed[id] {
			delete(m.unsettled, id)
		}
	}
	connectedAt := m.connectedAt
	m.mu.Unlock()
	if c.via == "ssh" && p.src != nil && p.src != m {
		p.src.mu.Lock()
		p.src.directFailed = time.Time{}
		p.src.mu.Unlock()
	}
	info := c.info
	m.d.onSystem(m.cfg.ID, info.System) // what it is, for when it is offline (metrics.go)
	where := c.via
	if c.via == viaHub {
		where = "hub " + m.d.hubNameOf(p.hub)
	}
	m.d.logf("[%s] connected over %s: %s %s/%s herdr %s claude %s", m.cfg.ID, where, info.Hostname, info.OS, info.Arch, info.HerdrVersion, info.ClaudeVersion)
	m.tellLinks(true, "")
	compat := compatOf(info)
	if compat.Incompatible != "" {
		m.d.logf("[%s] node %s speaks node protocol %d (min manager %d, missing %v): this manager speaks %d (min node %d), %s: update it",
			m.cfg.ID, info.Version, info.Protocol, info.ProtocolMin, compat.Missing, nodeproto.Version, nodeproto.MinVersion, compat.Incompatible)
	} else if v := m.d.nodeTarget(info); buildDiffers(v, info.Version) {
		m.d.logf("[%s] node %s is not build %s (node protocol %d): an update is offered", m.cfg.ID, info.Version, v, info.Protocol)
	}
	// The hub has it connected before it is asked to hand the node what is the hub's (devices.connected).
	m.d.reportNow()
	m.d.learnUID(p, info.MachineUID) // the paths reaching the same computer become one machine (paths_group.go)
	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	if c.via == "tailcat" && p.src == m {
		go m.watchTailcat(sctx, c.peer, c.pipe.activity)
	}
	if a := c.pipe.activity; a != nil {
		// Once ctx ends (the hub stopping, the entry removed) watchTailcat is gone: this is what fails a write still
		// stuck on a dead connection, so run returns and the manager's wait for its workers ends.
		defer context.AfterFunc(ctx, func() { _ = a.Close() })()
	}

	// A node this manager cannot drive stays connected, so it shows and can be updated (machine.call refuses the
	// rest), but is neither configured nor listed nor watched: what it would answer is not known.
	if compat.Incompatible == "" {
		// The hub's part (Codex, managed launchers, its sends; it starts the plugins' work on the computer) with it.
		if reply, theirs := m.d.hubConnected(ctx, m, connectedAt); theirs {
			m.configureNode(ctx, c.peer, nil, false) // another manager's now: nothing of this one's (bus.go hubConnected)
		} else {
			m.configureNode(ctx, c.peer, m.d.withOwn(ctx, m.cfg.ID, reply), true)
		}
		var list struct {
			Sessions []proto.Session `json:"sessions"`
		}
		revision := m.sessionsRevision()
		if err := c.peer.Call(ctx, "sessions.list", map[string]any{}, &list); err == nil {
			m.setInitialSessions(c.peer, revision, list.Sessions)
			m.d.forward(m.cfg.ID, "sessions.changed", map[string]any{"sessions": m.sessionList()})
			updates = true
		}
	} else {
		rewatch = nil
	}
	m.firstTried() // configured and listed: the calls that waited for it go now
	if updates {
		// A queued update goes on, and a node back on another build is given the one it should run (deploy_rpc.go,
		// deploy.go). Which build that is may take the release site (packages.go): looked up off this path, so the
		// calls that waited for the machine and its live sessions never wait on it.
		go func() {
			m.nodeUpdateConnected(ctx, c.peer, info)
			m.updateOnConnect(ctx)
		}()
	}
	if f := m.d.opts.OnConnected; f != nil && compat.Incompatible == "" {
		go f(m.cfg.ID)
	}
	var refused []string
	for id, since := range rewatch {
		if c.peer.Call(ctx, "session.watch", map[string]any{"session_id": id, "since_seq": since}, nil) == nil {
			m.mu.Lock()
			if m.followed[id] {
				delete(m.unsettled, id) // a Follow that failed is taken now
			}
			m.mu.Unlock()
		} else if ctx.Err() == nil {
			refused = append(refused, id)
		}
	}
	if len(refused) > 0 {
		m.d.logf("[%s] the new connection refused session.watch for %d session(s); asking again", m.cfg.ID, len(refused))
		go m.rewatchRefused(sctx, c.peer, refused, rewatchWait, rewatchTries)
	}
	go m.watchPaths(sctx, ctx, c)
	if info.ClaudeVersion == "" {
		go m.retryClaudeVersion(sctx, c.peer)
	}

	// The connection ends when the node's output does, or when the peer is closed (reconnect, a better path): then
	// an ssh process is still there and its output never ends — waiting only for it left the machine "online" with
	// every call failing until the hub restarted. Returning cancels the connection's context, which kills the process.
	var err error
	select {
	case err = <-c.served:
	case <-c.peer.Done():
		if err = c.peer.Err(); err == nil {
			err = errors.New("reconnect requested")
		}
	case <-ctx.Done():
		err = ctx.Err()
	}
	if c.via == "ssh" && p.src != nil && time.Since(onlineAt) > 10*time.Minute { // ssh held: going back to it worked
		p.src.mu.Lock()
		p.src.flaps, p.src.switchedBack = 0, time.Time{}
		p.src.mu.Unlock()
	}
	if errors.Is(err, io.EOF) {
		if msg := c.pipe.lastErr(); msg != "" {
			err = fmt.Errorf("node exited: %s", msg)
		} else {
			err = errors.New("node exited")
		}
	}
	return err
}

// rewatchRefused asks a new connection again, while it lasts, to watch the sessions whose watch it refused when it
// came (a node just started may not find a transcript yet). One left refused would keep an app's open chat without
// its items until the app subscribed again, while the hub takes the session as watched (#6372). A session neither the
// hub nor a Follow wants any more is let go; after rewatchTries the node is taken at its word, logged once.
func (m *Machine) rewatchRefused(ctx context.Context, peer *rpc.Peer, ids []string, wait time.Duration, tries int) {
	for try := 0; len(ids) > 0; try++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		var left []string
		for _, id := range ids {
			m.mu.Lock()
			want, since := m.watched[id] || m.followed[id], m.lastSeq[id]
			m.mu.Unlock()
			if !want {
				continue
			}
			if err := peer.Call(ctx, "session.watch", map[string]any{"session_id": id, "since_seq": since}, nil); err != nil {
				if ctx.Err() != nil {
					return
				}
				if try+1 >= tries {
					m.d.logf("[%s] session.watch %s on the new connection: %v (gave up)", m.cfg.ID, id, err)
					continue
				}
				left = append(left, id)
				continue
			}
			m.mu.Lock()
			if m.followed[id] {
				delete(m.unsettled, id)
			}
			m.mu.Unlock()
		}
		ids = left
		wait = min(wait*2, 30*time.Second)
	}
}

// rewatchWait is the first wait before a refused watch is asked again (it doubles, up to 30 s), rewatchTries how
// many times it is asked.
var (
	rewatchWait  = time.Second
	rewatchTries = 8
)

// watchPaths, while c is the connection, leaves it for a clearly better path once that has stayed better long
// enough (connecting the new one first), and lets it go when its path now reaches another computer or this machine
// is to be parked. ctx is c's (it ends with c); runCtx the run loop's, which the new connection lives on: made on ctx,
// it was cancelled the moment c let go — its node's requests failed and an ssh or local node was killed.
func (m *Machine) watchPaths(ctx, runCtx context.Context, c *nodeConn) {
	since := map[string]time.Time{}
	t := time.NewTicker(pathCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.d.fleetChanged():
		}
		if ctx.Err() != nil {
			return
		}
		if why := m.d.parkReason(m); why != "" || !m.hasPath(c.path) {
			if why == "" {
				why = "这条路通向的是另一台电脑"
			}
			c.peer.Close(errors.New(why))
			return
		}
		q := m.d.betterPath(m, c.path, since)
		if q == nil {
			continue
		}
		m.d.logf("[%s] trying a better path: %s instead of %s", m.cfg.ID, pathName(m.d, q), pathName(m.d, c.path))
		// Given up if c ends while connecting; once connected, the new connection outlives c.
		ectx, cancel := context.WithCancel(runCtx)
		stop := context.AfterFunc(ctx, cancel)
		next, err := m.establish(ectx, q)
		stop()
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return
			}
			m.d.pathEnded(q, false, err, 0, false)
			m.d.logf("[%s] the better path did not work: %v", m.cfg.ID, err)
			delete(since, q.key)
			continue
		}
		inner := next.cancel
		next.cancel = func() { inner(); cancel() } // ectx goes when the new connection does
		m.mu.Lock()
		if ctx.Err() != nil { // c ended meanwhile and run looked for m.next already (serve returns before takeNext)
			m.mu.Unlock()
			next.close()
			return
		}
		m.next = next
		m.mu.Unlock()
		c.peer.Close(errSwitched)
		return
	}
}

func pathName(d *Devices, p *nodePath) string {
	if p.via == viaHub {
		return "hub " + d.hubNameOf(p.hub)
	}
	return p.via + " (" + p.src.cfg.ID + ")"
}

func (m *Machine) hasPath(p *nodePath) bool {
	for _, q := range m.d.pathsOf(m) {
		if q.key == p.key {
			return true
		}
	}
	return false
}

func (m *Machine) takeNext() *nodeConn {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := m.next
	m.next = nil
	return n
}

// detach lets the connection that ended go: its terminals and the questions waiting on it end (hooks with resumable
// ones come back through the next connection). Unless another connection takes over at once (planned), the machine is
// offline.
func (m *Machine) detach(err error, planned bool) {
	if m.stopped() {
		// Its entry changed or went away: the machine taking its id (or none) speaks for that computer now. Reporting
		// this connection's end would be read as the replacement's, ending the questions waiting on it (#1949).
		m.mu.Lock()
		m.peer, m.active, m.transport = nil, nil, ""
		m.mu.Unlock()
		return
	}
	if m.d.stopping.Load() {
		// The manager is stopping for a restart: the next one shows the machine as it was while its node reconnects
		// (warm.go). Telling the apps it went offline, or ending its questions, would only be noise.
		m.mu.Lock()
		m.peer, m.active, m.transport = nil, nil, ""
		m.mu.Unlock()
		return
	}
	// The hub ends what ran on the connection (terminals, the live text, its questions) when it sees it go.
	m.mu.Lock()
	m.peer = nil
	m.active, m.transport = nil, ""
	if !planned && m.online && m.reconnectGrace > 0 && m.cfg.Kind != kindLink {
		// A node the hub reaches itself that comes straight back (a resident node exec'ing a new binary, a relay
		// replaced) is not news: the machine shows as it was for reconnectGrace, then offline as below if it is still
		// gone (warm.go). A link going away is the computer leaving: offline at once (TestLinkMachineComesOnline…).
		m.online = false
		if err != nil {
			m.err = err.Error()
		}
		ch := make(chan struct{})
		m.warmUntil, m.warmed = time.Now().Add(m.reconnectGrace), ch
		m.mu.Unlock()
		m.d.logf("[%s] disconnected: %v (shown as online for %s while it reconnects)", m.cfg.ID, err, m.reconnectGrace)
		time.AfterFunc(m.reconnectGrace, func() { m.warmExpired(ch) })
		m.d.changed()
		if f := m.d.opts.Detached; f != nil {
			go f(m.cfg.ID)
		}
		return
	}
	if !planned {
		m.online = false
		m.via, m.path = "", ""
		if err != nil {
			m.err = err.Error()
		}
		for i := range m.sessions {
			if m.sessions[i].Status != proto.StatusOffline {
				m.sessions[i].Status = proto.StatusUnknown
			}
		}
	}
	m.mu.Unlock()
	if planned {
		m.d.logf("[%s] %v", m.cfg.ID, errSwitched)
	} else {
		m.d.logf("[%s] offline: %v", m.cfg.ID, err)
		if err != nil {
			m.tellLinks(false, err.Error())
		}
	}
	m.d.changed()
	if f := m.d.opts.Detached; f != nil {
		go f(m.cfg.ID) // the questions waiting on it end, its sessions show unknown (the manager's fleet)
	}
}

// markOffline: a connection could not be made; the machine shows why.
func (m *Machine) markOffline(err error) {
	defer m.firstTried()
	if m.d.stopping.Load() {
		return // a connection that failed because the hub is stopping (detach)
	}
	m.mu.Lock()
	m.online = false
	if err != nil {
		m.err, m.dialErrAt = err.Error(), time.Now()
	}
	m.mu.Unlock()
	m.d.logf("[%s] offline: %v", m.cfg.ID, err)
	if err != nil {
		m.tellLinks(false, err.Error())
	}
	m.d.changed()
}

func (m *Machine) activePath() *nodePath {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

func (m *Machine) activePathKey() string {
	if p := m.activePath(); p != nil {
		return p.key
	}
	return ""
}

func (m *Machine) transportNow() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transport
}

func (m *Machine) nodeUID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.uid
}

// tellLinks says on the links of this computer's persona-links (its own entry's and those of entries with the same
// computer) whether the hub got through to the resident node.
func (m *Machine) tellLinks(online bool, err string) {
	for _, p := range m.d.pathsOf(m) {
		if p.src != nil && p.src.cfg.Kind == kindLink {
			p.src.tellLink(online, err)
		}
	}
}

// reachableHere: this hub can try its hub.json entry's way to the node now as far as it knows, and its last round
// trip there (ms, 0 unknown). A member says so in its record; the center uses it to choose.
func (m *Machine) reachableHere() (bool, int) {
	if m.dialNode != nil || m.nodeCmd != nil {
		return true, 0
	}
	switch m.cfg.Kind {
	case "local":
		return true, 0
	case kindLink:
		l := m.currentLink()
		if l == nil {
			return false, 0
		}
		return true, int(l.rtt.Load()) // the hub's heartbeat on it (devices.link_rtt); 0 before the first

	case kindService:
		return m.serviceReachable()
	}
	return true, 0 // ssh, tailcat: known only by trying
}

// unavailableWhy is what keeps the entry's way from being tried.
func (m *Machine) unavailableWhy() string {
	switch m.cfg.Kind {
	case kindLink:
		m.mu.Lock()
		l, down := m.link, m.linkDown
		m.mu.Unlock()
		if l != nil && l.ctx.Err() != nil { // going away: why
			if c := context.Cause(l.ctx); c != nil {
				return c.Error()
			}
		}
		if down != nil {
			return down.Error()
		}
		return "等这台电脑的 Persona Link 连上来"
	case kindService:
		return m.serviceWhy()
	}
	return ""
}

// claudeVersionRetry is how long the hub waits before asking a node again for its Claude version while node.info came
// back without one; the last wait repeats until the connection ends.
var claudeVersionRetry = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// retryClaudeVersion asks the node for node.info again until it reports a Claude version, then shows it (#542). The
// node finds the version by running `claude --version` in a login shell, which fails now and then on connecting (the
// machine just woke, a slow ~/.zshrc, Claude updating itself); asked only then, the machine showed no version until it
// reconnected. A machine without Claude is asked again every hour while it stays connected.
func (m *Machine) retryClaudeVersion(ctx context.Context, peer *rpc.Peer) {
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(claudeVersionRetry[min(i, len(claudeVersionRetry)-1)]):
		}
		var info proto.NodeInfo
		cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := peer.Call(cctx, "node.info", map[string]any{}, &info)
		cancel()
		if err != nil || info.ClaudeVersion == "" {
			continue
		}
		m.mu.Lock()
		current := m.peer == peer
		if current {
			m.info.ClaudeVersion = info.ClaudeVersion
		}
		m.mu.Unlock()
		if current {
			m.d.logf("[%s] claude %s", m.cfg.ID, info.ClaudeVersion)
			m.d.changed()
		}
		return
	}
}
