package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
)

// The fleet's tests (fleet_*_test.go): a manager with machines whose nodes a test plays (fleetDevs), on a fake hub
// that keeps what it was told (fakeHub.told, pushes) and an in-memory interactions table (fleetMem).

// fleetDevs is the manager's device layer for the fleet's tests: machines with the sessions their nodes list, and a
// node whose calls a test answers.
type fleetDevs struct {
	mu       sync.Mutex
	machines map[string]*fleetNode
	order    []string
	usage    assistantwire.UsageSummary
	capacity proto.CapacitySnapshot // what CapacitySnapshot answers (capacity_test.go)
	// attention is every Persona Link summary handed over (SetAttention), in order.
	attention []link.Attention
	// linkDesktop: the manager has a Persona Link desktop; confirmed: the cards its app confirmed it handed to
	// Notification Center (confirm; LinkRang).
	linkDesktop bool
	confirmed   map[string]bool
}

type fleetNode struct {
	name, home, trust string
	sessions          []proto.Session
	connected         time.Time
	// call answers the node's methods (nil: {}); calls are the ones it got.
	call  func(method string, params json.RawMessage) (any, error)
	calls []fleetCall
}

type fleetCall struct {
	method string
	params json.RawMessage
}

func newFleetDevs() *fleetDevs { return &fleetDevs{machines: map[string]*fleetNode{}} }

// add puts a machine on the devices.
func (d *fleetDevs) add(id, name string) *fleetNode {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := &fleetNode{name: name, home: "/home/u"}
	d.machines[id] = n
	d.order = append(d.order, id)
	return n
}

// setSessions is the node's list now (as devices keeps it before the manager is told).
func (d *fleetDevs) setSessions(id string, list []proto.Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range list {
		list[i].MachineID = id
	}
	d.machines[id].sessions = list
}

func (d *fleetDevs) callsTo(id, method string) []json.RawMessage {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []json.RawMessage
	for _, c := range d.machines[id].calls {
		if c.method == method {
			out = append(out, c.params)
		}
	}
	return out
}

func (d *fleetDevs) Hello(json.RawMessage) {}
func (d *fleetDevs) BusUp()                {}
func (d *fleetDevs) Request(context.Context, string, json.RawMessage) (any, bool, error) {
	return nil, false, nil
}
func (d *fleetDevs) Event(string, json.RawMessage) bool     { return false }
func (d *fleetDevs) Handler(next http.Handler) http.Handler { return next }
func (d *fleetDevs) Has(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.machines[id] != nil
}
func (d *fleetDevs) SetKeep(map[string][]string) {}

func (d *fleetDevs) CallNode(_ context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	d.mu.Lock()
	n := d.machines[id]
	if n == nil {
		d.mu.Unlock()
		return nil, devices.ErrNoMachine
	}
	n.calls = append(n.calls, fleetCall{method, params})
	call := n.call
	d.mu.Unlock()
	if call == nil {
		return json.RawMessage(`{}`), nil
	}
	out, err := call(method, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func (d *fleetDevs) List() []devices.Summary {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []devices.Summary
	for _, id := range d.order {
		n := d.machines[id]
		out = append(out, devices.Summary{ID: id, Name: n.name, Home: n.home, Trust: n.trust, Online: true, Reachable: true})
	}
	return out
}

func (d *fleetDevs) MachineResources(string) (*proto.Machine, *proto.MetricSample, *bool) {
	return nil, nil, nil
}

func (d *fleetDevs) Canonical(id string) string { return id }

// Summary is List's entry for id, found without the others as the devices do.
func (d *fleetDevs) Summary(id string) (devices.Summary, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.machines[id]
	if n == nil || !slices.Contains(d.order, id) {
		return devices.Summary{}, false
	}
	return devices.Summary{ID: id, Name: n.name, Home: n.home, Trust: n.trust, Online: true, Reachable: true}, true
}

func (d *fleetDevs) Sessions(id string) ([]proto.Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.machines[id]
	if n == nil {
		return nil, false
	}
	return append([]proto.Session(nil), n.sessions...), true
}

// SessionsWhere copies out only what keep accepts, as the devices do: a benchmark of a view then counts no copy of the
// whole list.
func (d *fleetDevs) SessionsWhere(id string, keep func(*proto.Session) bool) ([]proto.Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.machines[id]
	if n == nil {
		return nil, false
	}
	return keptWhere(n.sessions, keep), true
}

// keptWhere is the sessions of list keep accepts, copied (Devices.SessionsWhere).
func keptWhere(list []proto.Session, keep func(*proto.Session) bool) []proto.Session {
	var out []proto.Session
	for i := range list {
		if keep(&list[i]) {
			out = append(out, list[i])
		}
	}
	return out
}

// Session finds one session in place, as devices.Session does: copying the whole list here would make a benchmark of a
// lookup measure this fake instead.
func (d *fleetDevs) Session(id, session string) (proto.Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := d.machines[id]; n != nil {
		for i := range n.sessions {
			if n.sessions[i].SessionID == session {
				return n.sessions[i], true
			}
		}
	}
	return proto.Session{}, false
}

func (d *fleetDevs) ConnectedAt(id string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := d.machines[id]; n != nil {
		return n.connected
	}
	return time.Time{}
}

func (d *fleetDevs) Usage() assistantwire.UsageSummary {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.usage
}

func (d *fleetDevs) CapacitySnapshot(context.Context) proto.CapacitySnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.capacity
}

func (d *fleetDevs) SetAttention(a link.Attention) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attention = append(d.attention, a)
}

// confirm is the Persona Link desktop's app confirming it handed these cards' notifications to Notification Center.
func (d *fleetDevs) confirm(cards ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.confirmed == nil {
		d.confirmed = map[string]bool{}
	}
	for _, c := range cards {
		d.confirmed[c] = true
	}
}

// LinkRang is the devices' (internal/devices LinkRang): true once the desktop's app confirmed card, false at once
// without a desktop, and when the time is up or ctx ends.
func (d *fleetDevs) LinkRang(ctx context.Context, card string, within time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	for {
		d.mu.Lock()
		on, rang := d.linkDesktop, d.confirmed[card]
		d.mu.Unlock()
		if !on || rang {
			return rang
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// handedAttention is every summary handed over so far.
func (d *fleetDevs) handedAttention() []link.Attention {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.attention)
}

// fleetMem is the in-memory store of the fleet's tests: the assistant's settings, the interactions table, "Always Allow in Session",
// the seen times.
type fleetMem struct {
	memStore
	fmu    sync.Mutex
	status map[string]string
	res    map[string]*proto.Resolution
	recs   []db.InteractionRecord
	always map[string]map[string]bool // session → keys
	seen   map[[2]string]time.Time
	// insertGate, when set before the cards come, holds each interaction write until it is closed (a slow database).
	insertGate chan struct{}
}

func newFleetMem() *fleetMem {
	return &fleetMem{status: map[string]string{}, res: map[string]*proto.Resolution{}, always: map[string]map[string]bool{},
		seen: map[[2]string]time.Time{}}
}

func (m *fleetMem) InsertInteraction(ctx context.Context, ix proto.Interaction) error {
	if m.insertGate != nil {
		select {
		case <-m.insertGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m.fmu.Lock()
	defer m.fmu.Unlock()
	if _, ok := m.status[ix.ID]; !ok {
		m.status[ix.ID] = "pending"
		m.recs = append(m.recs, db.InteractionRecord{Interaction: ix, Status: "pending"})
	}
	return nil
}

func (m *fleetMem) FinishInteraction(_ context.Context, id, status string, res *proto.Resolution) error {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	if m.status[id] == "pending" {
		m.status[id], m.res[id] = status, res
		for i := range m.recs {
			if m.recs[i].Interaction.ID == id {
				m.recs[i].Status = status
			}
		}
	}
	return nil
}

func (m *fleetMem) InteractionOutcome(_ context.Context, id string) (string, *proto.Resolution, error) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	return m.status[id], m.res[id], nil
}

func (m *fleetMem) Interactions(_ context.Context, machine, session string, _ int) ([]db.InteractionRecord, error) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	out := []db.InteractionRecord{}
	for _, r := range m.recs {
		if (machine == "" || r.Interaction.MachineID == machine) && (session == "" || r.Interaction.SessionID == session) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *fleetMem) AllowAlways(_ context.Context, session, _ string, keys []string) error {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	if m.always[session] == nil {
		m.always[session] = map[string]bool{}
	}
	for _, k := range keys {
		m.always[session][k] = true
	}
	return nil
}

func (m *fleetMem) AlwaysAllowed(_ context.Context, session string, keys []string) (bool, error) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	for _, k := range keys {
		if !m.always[session][k] {
			return false, nil
		}
	}
	return len(keys) > 0, nil
}

func (m *fleetMem) MarkSeen(_ context.Context, machine, session string, at time.Time) error {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	m.seen[[2]string{machine, session}] = at
	return nil
}

func (m *fleetMem) SeenTimes(context.Context) (map[[2]string]time.Time, error) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	out := map[[2]string]time.Time{}
	for k, v := range m.seen {
		out[k] = v
	}
	return out, nil
}

var _ fleetStore = (*fleetMem)(nil)

// fleetTest is a manager with the fleet on: s, its devices d, its hub f, its store st.
type fleetTest struct {
	t  *testing.T
	s  *Service
	d  *fleetDevs
	f  *fakeHub
	st *fleetMem
}

// newFleetTest is a manager carrying the machine "mac" (named "Mac") with a store, on a fake hub.
func newFleetTest(t *testing.T) *fleetTest {
	t.Helper()
	f := newFakeHub()
	d := newFleetDevs()
	d.add("mac", "Mac")
	st := newFleetMem()
	s := New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t), Devices: d})
	return &fleetTest{t: t, s: s, d: d, f: f, st: st}
}

// newFleetTestNoStore is newFleetTest without a database.
func newFleetTestNoStore(t *testing.T) *fleetTest {
	t.Helper()
	f := newFakeHub()
	d := newFleetDevs()
	d.add("mac", "Mac")
	s := New(Options{Hub: f, Logf: testLogf(t), Devices: d})
	return &fleetTest{t: t, s: s, d: d, f: f}
}

// event is a node's event on machine as the devices hand it over.
func (ft *fleetTest) event(machine, name string, params any) bool {
	b, _ := json.Marshal(params)
	return ft.s.NodeEvent(machine, name, b)
}

// sessions has machine's node list sessions (sessions.changed).
func (ft *fleetTest) sessions(machine string, list ...proto.Session) {
	ft.d.setSessions(machine, append([]proto.Session(nil), list...))
	ft.event(machine, "sessions.changed", map[string]any{"sessions": list})
}

// waitPending waits for n pending interactions.
func (ft *fleetTest) waitPending(n int) []proto.Interaction {
	ft.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := ft.s.pendingInteractions()
		if len(got) == n {
			return got
		}
		if time.Now().After(deadline) {
			ft.t.Fatalf("pending = %d, want %d", len(got), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// toldOf are the params of what the hub was told of kind event, in order.
func (ft *fleetTest) toldOf(event string) []json.RawMessage {
	ft.f.mu.Lock()
	defer ft.f.mu.Unlock()
	var out []json.RawMessage
	for _, t := range ft.f.told {
		if t.event == event {
			out = append(out, t.params)
		}
	}
	return out
}

// lastList is the last list the hub was told for machine (fleet.sessions).
func (ft *fleetTest) lastList(machine string) []proto.Session {
	ft.t.Helper()
	list := ft.toldOf(assistantwire.EventFleetSessions)
	for i := len(list) - 1; i >= 0; i-- {
		var p assistantwire.FleetSessions
		if json.Unmarshal(list[i], &p) == nil && p.MachineID == machine {
			return p.Sessions
		}
	}
	return nil
}

// pushesOf are the pushes the hub was asked for of kind, waited for a little (they go off the caller's way).
func (ft *fleetTest) pushesOf(kind string, n int, wait time.Duration) []Notification {
	deadline := time.Now().Add(wait)
	for {
		ft.f.mu.Lock()
		var out []Notification
		for _, p := range ft.f.pushes {
			if p.Kind == kind {
				out = append(out, p)
			}
		}
		ft.f.mu.Unlock()
		if len(out) >= n || time.Now().After(deadline) {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ids are the pending interactions' ids, sorted.
func ids(list []proto.Interaction) []string {
	out := []string{}
	for _, ix := range list {
		out = append(out, ix.ID)
	}
	sort.Strings(out)
	return out
}

var errNode = errors.New("the node refused")

func init() { _ = apierr.ErrOffline }

func (d *fleetDevs) Follow(context.Context, string, string, bool) error { return nil }
