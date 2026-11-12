package devices

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What the hub asks of the manager about the machines (devicewire), and what the manager asks of it.

// Request answers the hub's devices.* calls; handled false: not one of them.
func (d *Devices) Request(ctx context.Context, method string, params json.RawMessage) (any, bool, error) {
	decode := func(v any) error {
		if err := json.Unmarshal(params, v); err != nil {
			return &rpc.Error{Code: "invalid_params", Message: method + ": " + err.Error()}
		}
		return nil
	}
	switch method {
	case devicewire.MethodCall:
		var p devicewire.NodeCall
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		out, err := d.Call(ctx, p)
		return out, true, err
	case devicewire.MethodAccept:
		var p devicewire.Accept
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		return map[string]any{}, true, d.Accept(ctx, p)
	case devicewire.MethodReconnect:
		var p struct {
			MachineID string `json:"machine_id"`
		}
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		return map[string]any{}, true, d.Reconnect(p.MachineID)
	case devicewire.MethodMachinesPut:
		var p devicewire.MachinePut
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		return map[string]any{}, true, d.putMachine(ctx, p.Entry)
	case devicewire.MethodMachinesRemove:
		var p devicewire.MachineRef
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		removed, err := d.removeMachine(ctx, p.MachineID)
		return devicewire.MachineRemoved{Removed: removed}, true, err
	case devicewire.MethodMachinesGet:
		var p devicewire.MachineRef
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		entry, err := d.machineEntry(p.MachineID)
		if err != nil {
			return nil, true, err
		}
		return devicewire.MachineEntry{Entry: entry}, true, nil
	case devicewire.MethodEngineGet:
		var p devicewire.MachineRef
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		c, err := d.engineChoiceToMove(p.MachineID)
		return devicewire.MachineEngine{MachineID: p.MachineID, Choice: c}, true, err
	case devicewire.MethodEnginePut:
		var p devicewire.MachineEngine
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		if p.MachineID == "" || p.Choice == nil {
			return nil, true, &rpc.Error{Code: "invalid_params", Message: method + ": machine_id and choice are required"}
		}
		return map[string]any{}, true, d.setEngineHosts(ctx, p.MachineID, p.Choice.Hosts, p.Choice.Default)
	case devicewire.MethodUpdateNodes:
		go d.updateNodes(context.WithoutCancel(ctx))
		return map[string]any{}, true, nil
	case devicewire.MethodUsageGatewayAccounts:
		return d.UsageGatewayAccounts(), true, nil
	case devicewire.MethodPairConnector:
		var p devicewire.PairConnector
		if err := decode(&p); err != nil {
			return nil, true, err
		}
		addr, version, err := d.PairConnector(ctx, p)
		if err != nil {
			return nil, true, err
		}
		return map[string]any{"address": addr, "version": version}, true, nil
	}
	return nil, false, nil
}

// Event takes the hub's devices.* events; false: not one of them.
func (d *Devices) Event(name string, params json.RawMessage) bool {
	switch name {
	case devicewire.EventNotify:
		var p devicewire.NodeEvent
		if json.Unmarshal(params, &p) == nil {
			d.Notify(p)
		}
	case devicewire.EventConfig:
		var cfg devicewire.Config
		if json.Unmarshal(params, &cfg) == nil {
			d.Configure(cfg)
		}
	case devicewire.EventLinkRTT:
		var p devicewire.LinkRTT
		if json.Unmarshal(params, &p) == nil {
			d.linkRTT(p)
		}
	default:
		return false
	}
	return true
}

// Hello takes the devices section of the hub's answer to manager.hello (every handshake).
func (d *Devices) Hello(res json.RawMessage) {
	// Before the connection counts as up: the hub asks nothing through it yet (hubopen.go).
	d.endHubOpened()
	var h struct {
		Devices *devicewire.Config `json:"devices"`
	}
	if json.Unmarshal(res, &h) == nil && h.Devices != nil {
		d.Configure(*h.Devices)
	}
}

// hubConnected hands the node, through the hub, what is the hub's (Codex settings, the sends it
// remembers), before the manager asks the node for its sessions; the hub also starts what it does on a connection
// (the plugins' programs, Cua). The hub not there: it does that when it is back (it sees the connection then).
// theirs: the hub refused it as another manager's machine (it moved away while this manager still had it): the
// manager gives it nothing of its own (its launchers with their keys, its extensions' part).
func (d *Devices) hubConnected(ctx context.Context, m *Machine, at time.Time) (reply *devicewire.ConnectedReply, theirs bool) {
	if !d.busUp() {
		return nil, false
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out devicewire.ConnectedReply
	if err := d.opts.Hub.Call(cctx, devicewire.MethodConnected, devicewire.Connected{MachineID: m.cfg.ID, ConnectedAt: at}, &out); err != nil {
		d.logf("[%s] the hub's part of the connection: %v", m.cfg.ID, err)
		var re *rpc.Error
		return nil, errors.As(err, &re) && re.Code == devicewire.CodeNoDevice
	}
	return &out, false
}

// withOwn adds the manager's own part of a connection (Options.Own) to the hub's reply (nil: the hub is not there).
func (d *Devices) withOwn(ctx context.Context, machine string, hub *devicewire.ConnectedReply) *devicewire.ConnectedReply {
	if d.opts.Own == nil {
		return hub
	}
	own := d.opts.Own(ctx, machine)
	if len(own) == 0 {
		return hub
	}
	out := &devicewire.ConnectedReply{Configure: map[string]json.RawMessage{}}
	if hub != nil {
		out.Calls = hub.Calls
		for k, v := range hub.Configure {
			out.Configure[k] = v
		}
	}
	for k, v := range own {
		out.Configure[k] = v
	}
	return out
}

// rehook does the hub's part of every live connection again: the hub is new (it restarted) or the manager's
// connection to it is.
func (d *Devices) rehook(ctx context.Context) {
	for _, m := range d.machinesNow() {
		m.mu.Lock()
		peer, at := m.peer, m.connectedAt
		m.mu.Unlock()
		if peer == nil {
			continue
		}
		go func() {
			if reply, _ := d.hubConnected(ctx, m, at); reply != nil {
				m.configureNode(ctx, peer, d.withOwn(ctx, m.cfg.ID, reply), true)
			}
		}()
	}
}

// keptOn is the sessions the machine's node lists however quiet; ok false while the assistant has not said.
func (d *Devices) keptOn(machine string) ([]string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.keep == nil {
		return nil, false
	}
	return slices.Clone(d.keep[machine]), true
}

// SetKeep takes the sessions each machine's node lists however long they are quiet (the assistant's profile: the
// main conversation, the ones it replaced, scheduled tasks' own) and tells the nodes whose list changed; the first
// time, every node. A node that connects later gets it with its configuration.
func (d *Devices) SetKeep(keep map[string][]string) {
	d.mu.Lock()
	before := d.keep
	d.keep = keep
	d.mu.Unlock()
	for _, m := range d.machinesNow() {
		if before != nil && slices.Equal(before[m.cfg.ID], keep[m.cfg.ID]) {
			continue
		}
		go func() {
			// One at a time, and what the latest report says when it goes: two reports close together (a new main
			// conversation, then its first message) must not reach the node in the other order.
			d.keepMu.Lock()
			defer d.keepMu.Unlock()
			list, ok := d.keptOn(m.cfg.ID)
			if !ok {
				return
			}
			if list == nil {
				list = []string{}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := m.call(ctx, "node.configure", map[string]any{"keep_sessions": list}, nil); err != nil && !isOffline(err) {
				d.logf("[%s] keep sessions: %v", m.cfg.ID, err)
			}
		}()
	}
}

// Handler serves the apps' routes on machines that are the manager's (devicewire.Routes), relayed by the hub; other
// paths go to next.
func (d *Devices) Handler(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	route := func(pattern string, fn func(http.ResponseWriter, *http.Request, *Machine)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			m := d.machineByID(r.PathValue("mid"))
			if m == nil {
				writeErr(w, http.StatusNotFound, errUnknownMachine)
				return
			}
			fn(w, r, m)
		})
	}
	route("POST /api/v1/machines/{mid}/reconnect", func(w http.ResponseWriter, r *http.Request, m *Machine) {
		m.reconnect()
		writeJSON(w, map[string]any{"ok": true})
	})
	route("PUT /api/v1/machines/{mid}/engine", d.putEngine)
	route("GET /api/v1/machines/{mid}/herdr", d.getHerdr)
	route("PUT /api/v1/machines/{mid}/herdr", d.putHerdr)
	route("POST /api/v1/machines/{mid}/herdr/start", d.postHerdrStart)
	route("GET /api/v1/machines/{mid}/botmux", d.getBotmux)
	route("PUT /api/v1/machines/{mid}/botmux", d.putBotmux)
	route("GET /api/v1/machines/{mid}/health/reports", d.listHealthReports)
	route("DELETE /api/v1/machines/{mid}/health/reports/{name}", d.deleteHealthReport)
	route("POST /api/v1/machines/{mid}/deploy", func(w http.ResponseWriter, r *http.Request, m *Machine) {
		if err := m.startDeploy(); err != nil {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	route("GET /api/v1/machines/{mid}/claude-dirs", d.getClaudeDirs)
	route("GET /api/v1/machines/{mid}/claude-live-replies", d.getLiveReplies)
	route("PUT /api/v1/machines/{mid}/claude-live-replies", d.putLiveReplies)
	route("GET /api/v1/machines/{mid}/clipboard", d.getClipboard)
	route("PUT /api/v1/machines/{mid}/clipboard", d.putClipboard)
	route("GET /api/v1/machines/{mid}/clipboard/settings", d.getClipboardSettings)
	route("PUT /api/v1/machines/{mid}/clipboard/settings", d.putClipboardSettings)
	route("GET /api/v1/machines/{mid}/file-transfer/settings", d.getFileTransferSettings)
	route("PUT /api/v1/machines/{mid}/file-transfer/settings", d.putFileTransferSettings)
	d.transferRoutes(mux)
	d.metricsRoutes(mux)
	d.usageRoutes(mux)
	d.usageWindowRoutes(mux)
	d.adminUsageRoutes(mux)
	d.shareRoutes(mux)
	route("PUT /api/v1/machines/{mid}/claude-dirs/blocked", d.putClaudeDirsBlocked)
	mux.Handle("/", next)
	return mux
}
