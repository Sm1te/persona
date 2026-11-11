package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The hub's tests that drive the assistant through the bus (dialAssistant) give it the hub's own test machines as
// its devices (mirrorDevices): what comes from the nodes is the manager's (assistant/fleet.go), so a test's node
// lists its sessions to it (Machine.setSessions below) and the hub shows what it reports back (fleet.sessions), as
// with a real manager. The calls to a node go through the hub's test machine (its in-process peer).

// mirrorDevices is assistant.Devices over h's test machines.
type mirrorDevices struct {
	h     *Hub
	mu    sync.Mutex
	lists map[string][]proto.Session // what each node listed last (setSessions)
}

func newMirrorDevices(h *Hub) *mirrorDevices {
	return &mirrorDevices{h: h, lists: map[string][]proto.Session{}}
}

func (d *mirrorDevices) Hello(json.RawMessage) {}
func (d *mirrorDevices) BusUp()                {}
func (d *mirrorDevices) Request(context.Context, string, json.RawMessage) (any, bool, error) {
	return nil, false, nil
}
func (d *mirrorDevices) Event(string, json.RawMessage) bool     { return false }
func (d *mirrorDevices) Handler(next http.Handler) http.Handler { return next }
func (d *mirrorDevices) Has(string) bool                        { return false } // node calls go through the hub, as before
func (d *mirrorDevices) SetKeep(map[string][]string)            {}

func (d *mirrorDevices) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	m := d.h.localMachine(id)
	if m == nil {
		return nil, devices.ErrNoMachine
	}
	var out json.RawMessage
	err := m.call(ctx, method, params, &out)
	return out, err
}

func (d *mirrorDevices) List() []devices.Summary {
	var out []devices.Summary
	for _, m := range d.h.machinesNow() {
		m.mu.Lock()
		s := devices.Summary{ID: m.cfg.ID, Name: m.cfg.Name, Kind: m.cfg.Kind, Home: m.info.Home, DataDir: m.info.DataDir, Online: m.online}
		m.mu.Unlock()
		out = append(out, s)
	}
	return out
}

func (d *mirrorDevices) Summary(id string) (devices.Summary, bool) {
	canon := d.Canonical(id)
	for _, s := range d.List() {
		if s.ID == canon {
			return s, true
		}
	}
	return devices.Summary{}, false
}

func (d *mirrorDevices) MachineResources(string) (*proto.Machine, *proto.MetricSample, *bool) {
	return nil, nil, nil
}

func (d *mirrorDevices) Canonical(id string) string {
	if m := d.h.localMachine(id); m != nil {
		return m.cfg.ID
	}
	return id
}

func (d *mirrorDevices) Sessions(id string) ([]proto.Session, bool) {
	m := d.h.localMachine(id)
	if m == nil {
		return nil, false
	}
	d.mu.Lock()
	list, ok := d.lists[m.cfg.ID]
	d.mu.Unlock()
	if !ok {
		list = m.sessionList() // set on the machine by the test itself
	}
	return slices.Clone(list), true
}

func (d *mirrorDevices) SessionsWhere(id string, keep func(*proto.Session) bool) ([]proto.Session, bool) {
	list, ok := d.Sessions(id)
	return slices.DeleteFunc(list, func(s proto.Session) bool { return !keep(&s) }), ok
}

func (d *mirrorDevices) Session(id, session string) (proto.Session, bool) {
	list, _ := d.Sessions(id)
	for _, s := range list {
		if s.SessionID == session {
			return s, true
		}
	}
	return proto.Session{}, false
}

func (d *mirrorDevices) ConnectedAt(id string) time.Time {
	if m := d.h.localMachine(id); m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.connectedAt
	}
	return time.Time{}
}

func (d *mirrorDevices) Usage() assistantwire.UsageSummary {
	return assistantwire.UsageSummary{Machines: []assistantwire.MachineUsage{}}
}

func (d *mirrorDevices) CapacitySnapshot(context.Context) proto.CapacitySnapshot {
	return proto.CapacitySnapshot{}
}

func (d *mirrorDevices) SetAttention(link.Attention) {}

func (d *mirrorDevices) LinkRang(context.Context, string, time.Duration) bool { return false }

// mirrors are the tests' hubs' mirror devices.
var mirrors sync.Map // *Hub → *mirrorDevices

func mirrorOf(h *Hub) *mirrorDevices {
	v, _ := mirrors.LoadOrStore(h, newMirrorDevices(h))
	return v.(*mirrorDevices)
}

// setSessions is the machine's node listing its sessions (sessions.changed): to the manager when one is connected,
// whose list the hub then shows (waited for); without one the hub shows it as it came.
func (m *Machine) setSessions(list []proto.Session) {
	for i := range list {
		list[i].MachineID = m.cfg.ID
	}
	h := m.hub
	v, ok := testAssistants.Load(h)
	if !ok {
		reportSessions(h, m, slices.Clone(list)...)
		return
	}
	d := mirrorOf(h)
	d.mu.Lock()
	d.lists[m.cfg.ID] = slices.Clone(list)
	d.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"sessions": list})
	v.(*testAssistant).svc.NodeEvent(m.cfg.ID, "sessions.changed", b)
	waitListed(h, m, list)
}

// waitListed waits (2 s at most) for the hub to show m's sessions as list has them (their ids and statuses).
func waitListed(h *Hub, m *Machine, list []proto.Session) {
	want := map[string]string{}
	for _, s := range list {
		want[s.SessionID] = s.Status
	}
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		got := m.sessionList()
		if len(got) != len(want) {
			continue
		}
		same := true
		for _, s := range got {
			if st, ok := want[s.SessionID]; !ok || st != s.Status {
				same = false
			}
		}
		if same {
			return
		}
	}
}

// editSessions changes m's sessions under its lock, as takeFleetSessions writes them from the bus. With the
// assistant connected it first waits for what the manager already told (settleAssistant), so an older list cannot
// land over the edit; the manager's next list is read from the machine (mirrorDevices.Sessions) and keeps it.
func editSessions(m *Machine, edit func([]proto.Session) []proto.Session) {
	settleAssistant(m.hub)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = edit(m.sessions)
	m.raws, m.rawsOf = nil, keptList{} // edited in place maybe: no longer what the manager's bytes gave
}

// nodeAsks is a node's request on m as the manager takes it (devices.Options.Answer): the assistant's own, then the
// fleet's (interactions, MCP tools, assistant.lark), else the hub's (devices.request). Without a connected manager
// a test's MCP call goes where the manager would send it: the hub's tools, then the phone's.
func nodeAsks(ctx context.Context, m *Machine, method string, params json.RawMessage) (any, error) {
	h := m.hub
	if v, ok := testAssistants.Load(h); ok {
		if !v.(*testAssistant).bh.Connected() { // the manager is gone: the node's connection is its, nothing answers
			return nil, &rpc.Error{Code: assistantwire.CodeAssistantOffline, Message: i18n.Localize(errAssistantOffline, h.userLang())}
		}
		svc := v.(*testAssistant).svc
		if out, ok, err := svc.NodeAnswer(ctx, m.cfg.ID, method, params); ok {
			return out, err
		}
		if out, ok, err := svc.NodeRequest(ctx, m.cfg.ID, method, params); ok {
			return out, err
		}
		return m.onRequest(ctx, method, params)
	}
	switch method {
	case "mcp.call":
		var p struct {
			Name      string          `json:"name"`
			Args      json.RawMessage `json:"args"`
			SessionID string          `json:"session_id"`
			Assistant bool            `json:"assistant"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		if r, ok := fakeExtOf(h).CallTool(ctx, m.cfg.ID, p.SessionID, p.Name, p.Args, p.Assistant); ok {
			return r, nil
		}
		return h.callTool(ctx, m.cfg.ID, p.Name, p.Args), nil
	case "assistant.lark": // the extensions' (the manager's, faked)
		return fakeExtOf(h).AssistantLark(ctx)
	}
	return m.onRequest(ctx, method, params)
}

func (d *mirrorDevices) Follow(ctx context.Context, machine, session string, on bool) error {
	m := d.h.localMachine(machine)
	if m == nil {
		return devices.ErrNoMachine
	}
	if on { // the test's node sends its items to the hub's machine (its peer); the manager reads the transcript
		m.addWatcher(ctx, session)
	} else {
		m.removeWatcher(session)
	}
	return nil
}
