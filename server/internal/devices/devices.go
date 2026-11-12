// Package devices is the manager's side of the user's computers (docs/server/machines.md "Who connects to computers"): it connects each one —
// the hub machine's own node as a child process, ssh, tailcat, a Persona Link that dialed the hub (relayed to the
// manager as it is), a connector of kind service, a member hub's computer through that hub — keeps it connected, and
// holds what is known of it: online, node.info, its sessions and their revs, warm start across restarts, the engine,
// herdr autostart, the Claude config directories, the sessions it keeps listing, its node updates. The hub never sees
// a computer: it relays the apps' calls and streams here (devices.call / devices.notify), and shows what this package
// reports (devices.state, devices.event). It runs in `personad assistant`, the chief manager; it does not import the
// hub.
package devices

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Host is the manager's connection to the hub (a bus.Client): Call and Notify answer bus.ErrOffline while it is
// down.
type Host interface {
	Call(ctx context.Context, method string, params, out any) error
	Notify(event string, params any) error
}

// Store is what the package keeps in the hub's Postgres (db.DB): settings, and each node's sessions' state.
type Store interface {
	Setting(ctx context.Context, key string, out any) (bool, error)
	PutSetting(ctx context.Context, key string, v any) error
	TakeSetting(ctx context.Context, key string, out any) (bool, error)
}

// nodeStateStore keeps what a node puts aside of its sessions (state.put / state.load, db.DB); a store without it
// keeps nothing.
type nodeStateStore interface {
	PutNodeSession(ctx context.Context, machine, id string, s db.NodeSession) error
	NodeSessions(ctx context.Context, machine string) (map[string]db.NodeSession, error)
}

// Options are what Devices needs from the manager it runs in.
type Options struct {
	Hub   Host
	Store Store // nil: nothing is kept (tests)
	Logf  func(format string, args ...any)
	// ManagerID is the manager these devices are ("" and "assistant": the built-in one): its health space on a computer
	// is named after it (identity.HealthDirName, healthreports.go).
	ManagerID string
	// Answer answers a node's request that is the manager's own (assistant.model, assistant.context); handled false:
	// not one, it goes to the hub.
	Answer func(ctx context.Context, machine, method string, params json.RawMessage) (out any, handled bool, err error)
	// NodeCalls are the manager's own calls to a node right after the hub's part of its connection (the launchers
	// added in Persona, with their keys: the assistant holds those); nil: none.
	NodeCalls func(ctx context.Context, machine string) []devicewire.NodeCall
	// DialRelay opens the hub's relay channel (devicewire.RelayPath) with query.
	DialRelay func(ctx context.Context, query string) (Relay, error)
	// Connected says whether the connection to the hub is up now (nil: always).
	Connected func() bool
	// Own is the manager's own part of a node's new connection (the extensions' agent filters, internal/extmgr):
	// added to node.configure beside the hub's, given whether the hub is there or not. OnConnected is told once the
	// node is configured and listed (the extensions' programs, Cua and the connectors start their work there);
	// OnEvent sees each of the node's events before the hub does (handled: not passed on).
	Own         func(ctx context.Context, machine string) map[string]json.RawMessage
	OnConnected func(machine string)
	OnEvent     func(machine, event string, params json.RawMessage) (handled bool)
	// DialNode replaces every way to a machine's node with a connection to an in-process one (tests; nil for a
	// machine: its own ways).
	DialNode func(machine string) func(context.Context) (net.Conn, error)
	// Event takes a node's event (and a session list the manager passes on again) before the hub would: what is the
	// manager's of it (the interactions, the sessions' states, the pushes: docs/server/machines.md "Who handles computer events"); false:
	// the hub does not get it. nil: every event goes to the hub.
	Event func(machine, event string, params json.RawMessage) (forward bool)
	// Detached: a machine's node connection ended (not for another one taking over): what waited on it ends.
	Detached func(machine string)
	// Location is the user's time zone ("today" of the token usage); nil: the machine's.
	Location func() *time.Location
	// ConfigFile is the manager's manager.json (internal/managerconf): its usage section (the account-pool gateways
	// and their keys, usagegateways.go) is read and written there. "": none (tests).
	ConfigFile string
	// TailcatKey is the identity file the computers reached over tailcat trust (tunnel.LoadOrCreateHubKey); "": this
	// machine's data directory's (tunnel.HubKeyPath).
	TailcatKey string
}

// Devices is every computer the manager carries.
type Devices struct {
	opts Options
	logf func(format string, args ...any)

	transfers transfers // computer to computer file transfers running or just over (transfer.go)

	mu    sync.Mutex
	cfg   devicewire.Config
	got   bool          // a config came from the hub
	gotCh chan struct{} // closed when the first one came
	// early: the machines were started from the manager's own list before the hub's first config (ownlist.go); that
	// one then changes them as any later one does. ownMu keeps the writes of that list one at a time, each the config
	// as it is then.
	early   bool
	ownMu   sync.Mutex
	ownSaid string // the last trouble keeping the list said (ownMu)
	// ownHeld: the list running is the own list, held as its own in manager.json (reported: States.OwnList).
	ownHeld bool
	ownWait time.Duration // ownListWait as New found it
	// ready is closed once the machines of the first config are made (the hub's, or the own list's: ownlist.go):
	// nothing is reported before (an empty list would have the hub forget them). The hello's config is taken before
	// BusUp reports, so the hub is reported its own list.
	ready     chan struct{}
	readyOnce sync.Once
	ctx       context.Context
	entries   []*Machine // hub.json's, in order
	byID      map[string]*Machine
	warm      *savedWarm // taken at start, for the machines when they come

	fleet   fleet
	tailcat tailcatDialer
	engines engineChoices
	// clipboard: the machines the user turned clipboard sharing on for (clipboard.go).
	clipboard clipboardSwitches
	// fileTransfer: the machines the user turned file transfer on for (transfer_settings.go).
	fileTransfer fileTransferSwitches
	links        linkSet
	stopping     atomic.Bool
	workers      sync.WaitGroup

	// attention: the assistant's latest "Needs you" summary for Persona Link (attention.go); nil before the first.
	attention atomic.Pointer[link.Attention]

	cfgChanged chan struct{} // closed when the hub's handover changes (meshWait)

	// keep: the sessions each machine's node lists however quiet (the assistant's profile, SetKeep); nil before it
	// said. keepMu: one node.configure keep_sessions at a time.
	keep   map[string][]string
	keepMu sync.Mutex

	reportMu  sync.Mutex
	reportDue chan struct{}
	lastSent  string // the last devices.state sent (stateKey), to send only changes
	// desktopsRep is the desktops reporter (presence.go); ring is closed at the next delivery or confirmation, for
	// LinkRang.
	desktopsRep desktopsReporter
	ringMu      sync.Mutex
	ring        chan struct{}

	packages nodePackages // the node builds for the machines' platforms (packages.go)

	metrics metricsBook // the machines' monitoring (metrics.go)
	usage   usageBook   // their token usage (metrics.go)
	// usageWrites: the usage notifications to store, in order (usage.go's usageLoop); usageWait: how long it takes the
	// ones that come after one to store them together (usageGather as New found it).
	usageWrites chan usageWrite
	usageWait   time.Duration
	// metricWrites: the samples to store (metrics.go's writeMetrics); metricsWait: how long it waits for a tick's other
	// samples (metricsGather as New found it).
	metricWrites chan db.MachineSample
	metricsWait  time.Duration

	usageWin usageWindowsHub // subscription rate-limit windows across machines (usagewindows.go, #2432)
	usageGW  usageGateways   // the account-pool gateways that add accounts to it (usagegateways.go, #2595)

	// reqPrefix and reqSeq make the nodes' requests' keys for the hub (devices.request), unique across managers.
	reqPrefix string
	reqSeq    atomic.Uint64
}

// New makes the manager's devices; Run connects them once the hub handed over its config.
func New(o Options) *Devices {
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	d := &Devices{opts: o, logf: o.Logf, gotCh: make(chan struct{}), ready: make(chan struct{}), byID: map[string]*Machine{}, ownWait: ownListWait,
		reportDue: make(chan struct{}, 1), usageWrites: make(chan usageWrite, 1024), usageWait: usageGather,
		metricWrites: make(chan db.MachineSample, metricsQueue), metricsWait: metricsGather, reqPrefix: strconv.FormatInt(time.Now().UnixNano(), 36)}
	// The account-pool gateways' usage windows (usagegateways.go, #2595): manager.json's usage section as it is now.
	var usage UsageConfig
	if o.ConfigFile != "" {
		if u, err := d.diskUsage(); err == nil {
			usage = u
		} else if !os.IsNotExist(err) {
			d.logf("manager.json usage: %v", err)
		}
	}
	d.usageWin.gateways = d.usageGW.accounts
	d.usageGW.init(usage, d.usageWin.invalidate, d.logf)
	return d
}

// requestKey names a node's request to the hub.
func (d *Devices) requestKey() string {
	return d.reqPrefix + "-" + strconv.FormatUint(d.reqSeq.Add(1), 36)
}

// Run keeps the machines connected until ctx ends; then hands their state to the next manager (warm.go) and lets
// them go.
func (d *Devices) Run(ctx context.Context) error {
	// The machines' connections end only after their state is handed over: ctx ending first would show them offline.
	mctx, stopMachines := context.WithCancel(context.WithoutCancel(ctx))
	defer stopMachines()
	d.mu.Lock()
	d.ctx = mctx
	d.mu.Unlock()
	d.loadWarm(ctx)
	d.loadPathUIDs(ctx)
	go d.reportLoop(mctx)
	go d.probeServices(mctx)
	go d.measureLoop(mctx)
	go d.metricsLoop(mctx)
	if st := d.metricsStore(); st != nil {
		d.workers.Add(1) // waited for below: what came before the machines stopped is stored
		go func() {
			defer d.workers.Done()
			d.writeMetrics(mctx, st)
		}()
	}
	go d.usageLoop(mctx)
	d.workers.Add(1)
	go func() { defer d.workers.Done(); d.runShareIngestion(mctx) }()
	go d.usageGW.run(ctx) // the account-pool gateways (usagegateways.go)
	// The hub's hand-over, or after ownListWait the manager's own list (ownlist.go) until it comes.
	wait := time.NewTimer(d.ownWait)
	select {
	case <-d.gotCh:
	case <-wait.C:
		d.startOwnList(mctx)
	case <-ctx.Done():
	}
	wait.Stop()
	select {
	case <-d.gotCh:
		d.startMachines(mctx)
	case <-ctx.Done():
	}
	<-ctx.Done()
	d.stopping.Store(true)
	sctx, cancel := context.WithTimeout(mctx, 5*time.Second)
	d.saveWarm(sctx)
	cancel()
	d.links.closeAll("manager stopping")
	stopMachines()
	d.workers.Wait()
	d.closeTailcat()
	return ctx.Err()
}

// Configure takes the hub's handover: hello's devices section, devices.config. Entries new to it start connecting;
// those gone stop.
func (d *Devices) Configure(cfg devicewire.Config) {
	d.ownMu.Lock()
	cfg = d.withOwnList(cfg) // a manager that keeps its own list: that one (ownlist.go)
	d.mu.Lock()
	first, early := !d.got, d.early
	wasMember := d.cfg.Mesh != nil && d.cfg.Mesh.Member
	old := d.cfg
	d.cfg, d.got = cfg, true
	ctx := d.ctx
	if d.cfgChanged != nil {
		close(d.cfgChanged)
		d.cfgChanged = nil
	}
	d.mu.Unlock()
	d.keepOwnListLocked() // in manager.json, for a start without the hub (ownlist.go)
	d.ownMu.Unlock()
	if first {
		close(d.gotCh) // Run starts them, if it is not running yet
	}
	if first && !early {
		if ctx != nil {
			d.startMachines(ctx)
		}
		return
	}
	if ctx != nil {
		d.startMachines(ctx)
		if first { // after a start from the own list: what its connections could not do without the hub
			d.afterOwnList(ctx)
		}
	}
	d.fleetRegroup()
	// Where the hub is, what it is called, whether the computer waits to be confirmed: Persona Link shows them.
	if was := linkSays(old); was != linkSays(cfg) {
		d.links.sendEndpoints(d.linkEndpoints)
	}
	// The summary carries the web address, and a computer gets it only once confirmed and not a work computer
	// (attentionFor: pending, machines[].trust, a member's records), an empty one otherwise. A computer that has
	// the latest gets nothing.
	d.spreadAttention()
	if member := cfg.Mesh != nil && cfg.Mesh.Member; member != wasMember {
		// The hub joined a group (its computers are the center's to connect now: let them go at once) or became its
		// center (connect them again, its own and the members').
		for _, m := range d.fleetAll() {
			if member {
				m.reconnect()
			} else {
				m.wake()
			}
		}
	}
	d.changed()
}

// linkSays is what the links are told of the hub in cfg (linkEndpoints): with its heartbeat, which they follow, and
// what it calls each computer.
func linkSays(cfg devicewire.Config) string {
	b, _ := json.Marshal([]any{cfg.HubName, cfg.MeshHubs, cfg.Endpoints, cfg.LinkPingMS, cfg.LinkPingTimeoutMS, cfg.Pending,
		entryNames(cfg)})
	return string(b)
}

// entryNames is what cfg's entries call each computer, by id (an entry that names it nothing: its id).
func entryNames(cfg devicewire.Config) map[string]string {
	out := map[string]string{}
	for _, raw := range cfg.Machines {
		var mc struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &mc) == nil && mc.ID != "" {
			out[mc.ID] = cmp.Or(mc.Name, mc.ID)
		}
	}
	return out
}

// config is the hub's handover as it is now.
func (d *Devices) config() devicewire.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg
}

func (d *Devices) hubVersion() string { return d.config().HubVersion }

// errPendingUnknown: the hub has not said which machines wait to be confirmed.
var errPendingUnknown = errors.New("the hub has not said which computers wait to be confirmed")

// PendingMachineIDs are the machines waiting for the user to confirm them, as the hub's handover has them (the hub
// keeps machine_trust); errPendingUnknown before the first handover and while the hub cannot read its list.
func (d *Devices) PendingMachineIDs(context.Context) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.got || d.cfg.PendingUnknown {
		return nil, errPendingUnknown
	}
	return slices.Clone(d.cfg.Pending), nil
}

// startMachines makes a machine for every hub.json entry it has none for and starts its run loop; an entry no
// longer there stops.
func (d *Devices) startMachines(ctx context.Context) {
	var started, replaced, renamed []*Machine
	d.mu.Lock()
	cfg := d.cfg // read with the machines it is laid over: an edit meanwhile is never undone by an older list
	seen := map[string]bool{}
	var keep []*Machine
	for _, raw := range cfg.Machines {
		var mc MachineConfig
		if json.Unmarshal(raw, &mc) != nil || mc.ID == "" || seen[mc.ID] {
			continue
		}
		seen[mc.ID] = true
		old := d.byID[mc.ID]
		if old != nil && sameConfig(old.cfg, mc) {
			old.mu.Lock()
			old.raw = raw // keys it does not read may have changed: reported as the hub has them (states)
			old.mu.Unlock()
			if old.setName(mc.Name) { // renamed: the same machine, connected as it is
				renamed = append(renamed, old)
			}
			keep = append(keep, old)
			continue
		}
		// New, or its entry changed (a hub restarted with hub.json edited): a machine of its own, connected afresh
		// (its entry is read without m.mu everywhere, so it never changes under a running machine).
		m := newMachine(d, mc)
		m.raw = raw
		if old != nil {
			replaced = append(replaced, old)
			old.mu.Lock()
			for id := range old.watched { // what the hub watches is watched again on the new connection
				m.watched[id], m.lastSeq[id] = true, old.lastSeq[id]
			}
			// So are the manager's own follows (Follow: a channel's turn), and a follow the old node did not take
			// stays unsettled: the assistant keeps its mark and never asks again.
			for id := range old.followed {
				if m.followed == nil {
					m.followed = map[string]bool{}
				}
				m.followed[id], m.lastSeq[id] = true, old.lastSeq[id]
				if old.unsettled[id] {
					if m.unsettled == nil {
						m.unsettled = map[string]bool{}
					}
					m.unsettled[id] = true
				}
			}
			old.mu.Unlock()
		} else {
			d.applyWarmLocked(m)
		}
		m.ctx, m.stop = context.WithCancel(ctx)
		d.byID[mc.ID] = m
		keep = append(keep, m)
		started = append(started, m)
	}
	var gone []*Machine
	for _, m := range d.entries {
		if !seen[m.cfg.ID] {
			gone = append(gone, m)
			delete(d.byID, m.cfg.ID)
		}
	}
	d.entries = keep
	d.mu.Unlock()
	for _, m := range gone {
		d.logf("[%s] no longer in the list: let go", m.cfg.ID)
		m.stop()
		m.closeLink("no longer in the list") // acceptLink refuses it when it comes back
		d.forgetMachine(m.cfg.ID)
	}
	for _, m := range renamed {
		d.logf("[%s] renamed", m.cfg.ID)
	}
	for _, m := range replaced {
		d.logf("[%s] its entry changed: connected again with it", m.cfg.ID)
		m.stop()
		m.closeLink("its entry changed") // Persona Link comes back to the new machine
	}
	if len(started) > 0 || len(gone) > 0 {
		d.fleetRegroup()
	}
	for _, m := range started {
		d.workers.Add(1)
		go func() {
			defer d.workers.Done()
			m.run(m.ctx)
		}()
		if m.cfg.Kind == kindService {
			d.workers.Add(1)
			go func() {
				defer d.workers.Done()
				m.attentionLoop(m.ctx)
			}()
		}
	}
	d.readyOnce.Do(func() { close(d.ready) })
	d.changed()
}

// cfgMachines is every hub.json entry.
func (d *Devices) cfgMachines() []*Machine {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.entries)
}

// cfgMachine is the hub.json entry with this id.
func (d *Devices) cfgMachine(id string) *Machine {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.byID[id]
}

// changed: what the hub shows of the machines may have changed; a devices.state goes out shortly.
func (d *Devices) changed() {
	select {
	case d.reportDue <- struct{}{}:
	default:
	}
}

// reportLoop sends devices.state when something changed, at most every 50 ms (a run of changes is one message).
func (d *Devices) reportLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.reportDue:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
		d.report(false)
	}
}

// report sends the hub every machine as it is now: only when it changed, unless all.
func (d *Devices) report(all bool) {
	if d.stopping.Load() {
		return // the next manager says it (warm.go); the machines going away now is not news
	}
	select {
	case <-d.ready:
	default:
		return // the machines are not made yet; the first report follows when they are (startMachines)
	}
	defer d.requestDesktops(all) // a change of computers or of who may count goes with it, after it (presence.go)
	d.reportMu.Lock()
	defer d.reportMu.Unlock()
	st := d.states()
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	// When a machine was last seen moves with every session list (the hub sees those itself): not news alone.
	key := stateKey(st)
	if !all && key == d.lastSent {
		return
	}
	if err := d.opts.Hub.Notify(devicewire.EventState, json.RawMessage(b)); err == nil {
		d.lastSent = key
	}
}

// stateKey is a report without what changes all the time (last seen), to tell whether it is news.
func stateKey(st devicewire.States) string {
	for i := range st.Machines {
		st.Machines[i].Machine.LastSeen = time.Time{}
	}
	b, _ := json.Marshal(st)
	return string(b)
}

// reportNow sends the state at once (a node just connected: the hub must have it before devices.connected).
func (d *Devices) reportNow() { d.report(false) }

// states is devices.state's params.
func (d *Devices) states() devicewire.States {
	out := devicewire.States{Machines: []devicewire.State{}}
	for _, m := range d.machinesNow() {
		out.Machines = append(out.Machines, m.state())
	}
	d.fleet.mu.Lock()
	for from, to := range d.fleet.absorbed {
		if out.Absorbed == nil {
			out.Absorbed = map[string]string{}
		}
		out.Absorbed[from.cfg.ID] = to.cfg.ID
	}
	d.fleet.mu.Unlock()
	d.mu.Lock()
	out.OwnList = d.ownHeld
	d.mu.Unlock()
	for _, m := range d.cfgMachines() {
		avail, rtt := m.reachableHere()
		// Its entry too, an absorbed one's included: the hub keeps what each manager has (hub machine_entries.go).
		m.mu.Lock()
		raw := m.raw
		m.mu.Unlock()
		out.Entries = append(out.Entries, devicewire.RecMachine{ID: m.cfg.ID, Cfg: raw, Kind: kindOf(m.cfg), Avail: avail, RTTMS: rtt})
	}
	return out
}

// busUp: the connection to the hub is up now.
func (d *Devices) busUp() bool { return d.opts.Connected == nil || d.opts.Connected() }

// BusUp is told once the connection to the hub is up (after every handshake): the hub gets every machine and every
// session list again (it may have restarted and know nothing), and does its part of every live connection again.
func (d *Devices) BusUp() {
	select { // the machines of the first config first (the hello's is taken before this): their report is the hub's list
	case <-d.ready:
	case <-time.After(10 * time.Second):
	}
	d.report(true)
	d.mu.Lock()
	ctx := d.ctx
	d.mu.Unlock()
	if ctx != nil {
		d.rehook(ctx)
	}
	for _, m := range d.machinesNow() {
		m.mu.Lock()
		list := slices.Clone(m.sessions)
		has := m.online || m.warmLocked(time.Now()) || len(list) > 0
		m.mu.Unlock()
		if has {
			if list == nil {
				list = []proto.Session{}
			}
			d.forward(m.cfg.ID, "sessions.changed", map[string]any{"sessions": list})
		}
	}
}

// maxForward bounds one node message passed on to the hub: the bus takes 64 MiB a line, the wrapper included, and a
// line it cannot take would drop the whole connection. The node's own lines have the same limit, so this only cuts
// the last bytes; what is big on purpose (downloads, uploads, node updates, a port's bytes) comes in pieces of 1 MiB
// at most.
const maxForward = 60 << 20

var errTooLarge = &rpc.Error{Code: "too_large", Message: "the node's answer is too large to pass on"}

// forward passes a node's event on to the hub, after the manager took what is its own of it (Options.Event).
func (d *Devices) forward(machine, event string, params any) {
	raw, ok := params.(json.RawMessage)
	if !ok {
		raw, _ = json.Marshal(params)
	}
	if f := d.opts.Event; f != nil && !f(machine, event, raw) {
		return
	}
	if len(raw) > maxForward {
		d.logf("[%s] %s of %d bytes not passed on to the hub: too large", machine, event, len(raw))
		return
	}
	if err := d.opts.Hub.Notify(devicewire.EventNode, devicewire.NodeEvent{MachineID: machine, Event: event, Params: raw}); err != nil &&
		d.busUp() {
		d.logf("[%s] %s to the hub: %v", machine, event, err)
	}
}

// Call is devices.call: a call to a machine's node, coded for the hub (no_device, machine_offline, node_closed;
// the node's own coded errors as they are).
func (d *Devices) Call(ctx context.Context, p devicewire.NodeCall) (json.RawMessage, error) {
	m := d.machineByID(p.MachineID)
	if m == nil {
		return nil, &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("no machine %q", p.MachineID)}
	}
	if p.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	params := p.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	if refusesHandoff(p.Method, params) {
		return nil, errHandoffRefused
	}
	m.noteWatch(p.Method, params)
	if p.Method == "session.unwatch" && m.follows(params) {
		return json.RawMessage(`{}`), nil // the manager still follows it: the node keeps sending its items
	}
	var receipt, preparation string
	var prepareErr error
	params, receipt, preparation, prepareErr = d.prepareShareCall(ctx, p, params)
	if prepareErr != nil {
		d.observeCreationCall(p, nil, nil, preparation, prepareErr)
		return nil, wireError(prepareErr)
	}
	if refusesHandoff(p.Method, params) { // and on what actually goes (prepareShareCall re-marshals it)
		return nil, errHandoffRefused
	}
	var out json.RawMessage
	err := m.call(ctx, p.Method, params, &out)
	d.observeCreationCall(p, params, out, preparation, err)
	d.finishShareCall(ctx, p, receipt, out, err)
	m.noteOpened(p.Method, params, err)
	if err != nil {
		return nil, wireError(err)
	}
	if p.Method == "sessions.list" { // the hub had the node list them again (its agent filters): the manager's list
		var list struct {
			Sessions []proto.Session `json:"sessions"`
		}
		if json.Unmarshal(out, &list) == nil && list.Sessions != nil {
			m.setSessions(list.Sessions)
			d.forward(m.cfg.ID, "sessions.changed", map[string]any{"sessions": m.sessionList()})
		}
	}
	if out == nil {
		out = json.RawMessage(`{}`)
	}
	if len(out) > maxForward {
		return nil, errTooLarge
	}
	return out, nil
}

// wireError codes an error for the bus: what the hub turns back into its own. What may have reached the node (its
// connection dropped with the call out, the time ran out) is coded apart from what never left (offline).
func wireError(err error) error {
	var re *rpc.Error
	switch {
	case errors.As(err, &re):
		return re
	case errors.Is(err, rpc.ErrDropped), errors.Is(err, context.Canceled): // the request had gone out (cut short: the
		// manager stopping, its bus connection going)
		return &rpc.Error{Code: devicewire.CodeNodeClosed, Message: err.Error()}
	case errors.Is(err, errOffline), errors.Is(err, rpc.ErrClosed): // closed before it went out
		return &rpc.Error{Code: devicewire.CodeMachineOffline, Message: err.Error()}
	case errors.Is(err, context.DeadlineExceeded):
		return &rpc.Error{Code: devicewire.CodeDeadline, Message: err.Error()}
	}
	return err
}

// Notify is devices.notify: a one-way event to a machine's node, queued for it (notes.go): the bus's read loop goes
// on at once.
func (d *Devices) Notify(p devicewire.NodeEvent) {
	m := d.machineByID(p.MachineID)
	if m == nil {
		return
	}
	params := p.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	m.noteClosed(p.Event, params)
	m.queueNote(p.Event, params)
}

// Reconnect is devices.reconnect / POST …/reconnect.
func (d *Devices) Reconnect(id string) error {
	m := d.machineByID(id)
	if m == nil {
		return &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("no machine %q", id)}
	}
	m.reconnect()
	return nil
}

// List is the machines as the assistant's devices.list has them, and whether each takes calls now.
func (d *Devices) List() []Summary {
	out := []Summary{}
	for _, m := range d.machinesNow() {
		out = append(out, d.summaryOf(m))
	}
	return out
}

// summaryOf is one machine as List has it.
func (d *Devices) summaryOf(m *Machine) Summary {
	m.mu.Lock()
	s := Summary{ID: m.cfg.ID, Name: m.name(), Kind: m.cfg.Kind, SSHHost: m.cfg.SSHHost, Home: m.info.Home,
		DataDir: m.info.DataDir, Online: m.online}
	m.mu.Unlock()
	s.Trust = d.machineTrust(m)
	s.Reachable = m.reachable()
	return s
}

// Summary is List's entry for the machine id names (an entry another one absorbed: that one's), false when List has
// none: the first listed machine with Canonical(id) as its id, found without the summaries of the others (their trust
// reads every path's configuration). The fleet asks for one machine on every list a node sends.
func (d *Devices) Summary(id string) (Summary, bool) {
	m := d.listed(d.Canonical(id))
	if m == nil {
		return Summary{}, false
	}
	return d.summaryOf(m), true
}

// listed is the first of machinesNow with this id, read in place rather than from a copy of the list; nil when none
// has it.
func (d *Devices) listed(id string) *Machine {
	f := &d.fleet
	f.mu.Lock()
	order := f.order // replaced whole by a regroup, never written into
	f.mu.Unlock()
	if order == nil {
		d.mu.Lock()
		defer d.mu.Unlock()
		order = d.entries
	}
	for _, m := range order {
		if m.cfg.ID == id {
			return m
		}
	}
	return nil
}

// View is one machine with what its node said of itself (node.info), and its sessions as the node listed them.
type View struct {
	Summary
	Info     proto.NodeInfo
	Sessions []proto.Session
}

// Views are the machines with their node.info (the extensions' code reads the computers' OS, programs, capabilities).
func (d *Devices) Views() []View {
	out := []View{}
	for _, m := range d.machinesNow() {
		m.mu.Lock()
		v := View{Summary: Summary{ID: m.cfg.ID, Name: m.name(), Kind: m.cfg.Kind, SSHHost: m.cfg.SSHHost, Home: m.info.Home,
			DataDir: m.info.DataDir, Online: m.online}, Info: m.info, Sessions: slices.Clone(m.sessions)}
		m.mu.Unlock()
		v.Trust = d.machineTrust(m)
		v.Reachable = m.reachable()
		out = append(out, v)
	}
	return out
}

// Relist asks a machine's node for its sessions again and passes the list on (what it leaves out changed: the
// extensions' agent filters).
func (d *Devices) Relist(ctx context.Context, id string) error {
	m := d.machineByID(id)
	if m == nil {
		return ErrNoMachine
	}
	var list struct {
		Sessions []proto.Session `json:"sessions"`
	}
	if err := m.call(ctx, "sessions.list", json.RawMessage(`{}`), &list); err != nil {
		return err
	}
	m.setSessions(list.Sessions)
	d.forward(m.cfg.ID, "sessions.changed", map[string]any{"sessions": m.sessionList()})
	return nil
}

// Summary is one machine for the assistant (assistantwire.Device).
type Summary struct {
	ID, Name, Kind, SSHHost, Home, DataDir, Trust string
	Online, Reachable                             bool
}

// Has reports whether id is one of the machines (an entry another one absorbed included).
func (d *Devices) Has(id string) bool { return d.machineByID(id) != nil }

// NodeAnnounced reports whether machine id's node is connected and announced capability in its handshake, never
// assumed for an older node: the assistant checks secret_env before it shows a hand-off card.
func (d *Devices) NodeAnnounced(id, capability string) bool {
	m := d.machineByID(id)
	if m == nil {
		return false
	}
	m.mu.Lock()
	info, connected := m.info, m.peer != nil
	m.mu.Unlock()
	return connected && info.Protocol > 0 && slices.Contains(info.Capabilities, capability)
}

// CallNode is a call to a machine's node from the manager itself (the assistant's device.call): errors as the
// hub's were (apierr.ErrOffline, rpc.ErrClosed, the node's *rpc.Error).
func (d *Devices) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	m := d.machineByID(id)
	if m == nil {
		return nil, ErrNoMachine
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var out json.RawMessage
	if err := m.call(ctx, method, params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrNoMachine: no machine has the id.
var ErrNoMachine = errors.New("no such machine")

// Session is one session as its node listed it.
func (d *Devices) Session(id, session string) (proto.Session, bool) {
	m := d.machineByID(id)
	if m == nil {
		return proto.Session{}, false
	}
	return m.session(session)
}

// Sessions are a machine's sessions as its node listed them; false when there is no such machine.
func (d *Devices) Sessions(id string) ([]proto.Session, bool) {
	m := d.machineByID(id)
	if m == nil {
		return nil, false
	}
	return m.sessionList(), true
}

// SessionsWhere are those of a machine's sessions keep accepts, copied out without the rest; false when there is no
// such machine. keep reads each session in place, under the machine's lock: it must not change or keep it, nor call
// the devices.
func (d *Devices) SessionsWhere(id string, keep func(*proto.Session) bool) ([]proto.Session, bool) {
	m := d.machineByID(id)
	if m == nil {
		return nil, false
	}
	return m.sessionsWhere(keep), true
}

// Canonical is the id of the machine id names (an entry another one absorbed: that one's); id itself when there is
// none.
func (d *Devices) Canonical(id string) string {
	if m := d.machineByID(id); m != nil {
		return m.cfg.ID
	}
	return id
}

// ConnectedAt is when the machine's node's current connection began (zero: none).
func (d *Devices) ConnectedAt(id string) time.Time {
	m := d.machineByID(id)
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectedAt
}

// follows: the session params names is one the manager follows itself.
func (m *Machine) follows(params json.RawMessage) bool {
	var p struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.followed[p.SessionID]
}

// Follow has the manager follow a session itself (on: its items and the text it writes come to the manager as they
// are made, a channel's turn) or stop: the node watches it while the hub or the manager does.
func (d *Devices) Follow(ctx context.Context, machine, session string, on bool) error {
	m := d.machineByID(machine)
	if m == nil {
		return ErrNoMachine
	}
	m.mu.Lock()
	if m.followed == nil {
		m.followed = map[string]bool{}
	}
	had, hub, unsettled := m.followed[session], m.watched[session], m.unsettled[session]
	since := m.lastSeq[session]
	// The mark is what the manager wants, whatever the node answers: a new connection watches every followed session
	// again (conn.go), so a follow that failed while the node was away is still repaired there.
	if on {
		m.followed[session] = true
	} else {
		delete(m.followed, session)
		m.forgetSeqLocked(session)
	}
	m.mu.Unlock()
	var err error
	switch {
	case on && (!had || unsettled) && !hub:
		err = m.call(ctx, "session.watch", map[string]any{"session_id": session, "since_seq": since}, nil)
	case !on && (had || unsettled) && !hub:
		err = m.call(ctx, "session.unwatch", map[string]any{"session_id": session}, nil)
	default:
		return nil
	}
	m.settleFollow(session, on, err)
	return err
}

// settleFollow notes whether the node took Follow's watch / unwatch: one it did not take is unsettled, and the next
// Follow of the session asks the node again even when the mark already says so. A later Follow that changed the mark
// meanwhile settles it itself.
func (m *Machine) settleFollow(session string, on bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.followed[session] != on {
		return
	}
	if err == nil {
		delete(m.unsettled, session)
		return
	}
	if m.unsettled == nil {
		m.unsettled = map[string]bool{}
	}
	m.unsettled[session] = true
}

// wrapped preserves a catalog message and its cause, including node RPC codes used for retries.
type wrapped struct {
	message error
	err     error
}

func (w *wrapped) Error() string   { return w.message.Error() }
func (w *wrapped) Unwrap() []error { return []error{w.message, w.err} }

func explain(message, err error) error { return &wrapped{message: message, err: err} }

// A hand-off is the manager's own, after its card (CallNode, in this process): never the hub's or another manager's
// to ask for (Devices.Call), so nobody else can stage values or start an Agent with them.
var errHandoffRefused = &rpc.Error{Code: devicewire.CodeHandoffRefused, Message: "secret hand-offs are made only by the manager that connects this computer"}

// refusesHandoff: a hub-relayed call that would stage values or start with a ticket.
func refusesHandoff(method string, params json.RawMessage) bool {
	return method == proto.MethodSecretStage || devicewire.IsSessionStart(method) && startHasSecretTicket(params)
}
