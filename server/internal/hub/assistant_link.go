package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The assistant (internal/assistant) is a process of its own on the bus (docs/assistant/assistant.md "Boundary with hub"); the hub
// starts it beside itself (assistant_process.go). This file is the hub's end: assistantLink answers the assistant's
// calls (manager.hello, device.call, app.call, notify.request, manager.snapshot, ws.broadcast; registered on the bus
// below) with the hub's own functions, the hub keeps the snapshot the assistant last reported (the session lists the
// apps get are decorated with it), and assistantRemote is what the hub asks of the assistant, as messages on its
// connection. The hub never runs the assistant's code.

// assistantState is what one manager last reported and was told: h.assistant is the assistant the hub starts
// (assistantwire.AssistantID), every other manager has its own (manager_state.go).
type assistantState struct {
	mu       sync.RWMutex
	got      bool // a snapshot came in
	epoch    string
	seq      uint64
	sessions map[string]assistantwire.SessionView // by session id; replaced, never written into
	profile  *assistantwire.Profile               // its settings (assistant_profile.go); nil before it reported them

	toolsMu sync.Mutex
	tools   []proto.Tool // its MCP tools (assistant.tools), asked for when it connects
	// toolsFresh: tools were asked for since it last connected (another manager's are asked when needed).
	toolsFresh bool

	helloMu   sync.Mutex
	lastHello string      // the hello answer it last got (manager.hello, manager.updated), as JSON
	updating  atomic.Bool // a manager.updated check is on its way

}

// taskSettleDelay is how long the assistant lets a task's agents stay still before the task counts as done: the
// delay of the hub's own finished push (tests shorten it).
var taskSettleDelay = func(c NotifyConfig) time.Duration { return c.doneDelay() }

// assistantRoutesRelayed relays the apps' requests on the assistant's routes to it (http.request, forwardHTTP: the
// caller's role goes with them); while it is not connected they are answered 503 assistant_offline.
func (h *Hub) assistantRoutesRelayed(api *http.ServeMux) {
	for _, pattern := range assistantwire.Routes {
		if pattern == "GET /api/v1/sessions/archived" || pattern == "POST /api/v1/sessions/{mid}/{sid}/archive" || pattern == "POST /api/v1/sessions/{mid}/{sid}/unarchive" {
			api.HandleFunc(pattern, h.forwardAgentArchives)
			continue
		}
		if key, ok := mergedLists[pattern]; ok { // the todos: every manager's
			api.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) { h.mergeList(w, r, key, h.forwardHTTP) })
			continue
		}
		if pattern == "POST /api/v1/assistant/conversations" {
			api.HandleFunc(pattern, h.startConversationOnce())
			continue
		}
		by := h.byRequest
		switch {
		case strings.Contains(pattern, "/machines/{mid}/"):
			by = h.byMachine
		case strings.Contains(pattern, "/assistant/conversations/{id}/"):
			by = h.byConversation
		case strings.Contains(pattern, "/todos/{id}"):
			by = h.byTodo
		}
		var relay http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) { h.forwardOwned(w, r, by) }
		if pattern == "GET /api/v1/machines/{mid}/launchers/{name}/start-options" {
			next := relay
			relay = h.withMachine(func(w http.ResponseWriter, r *http.Request, m *Machine) {
				if h.machineUntrusted(m.cfg.ID) {
					writeErrCode(w, http.StatusConflict, "machine_pending", errors.New("Confirm this computer before starting a session."))
					return
				}
				next(w, r)
			})
		}
		if slices.Contains(storedTerminalTexts, pattern) {
			relay = h.withoutTerminalControls(relay)
		}
		if kind, ok := meetingReceiptRoutes[pattern]; ok {
			relay = h.withBodyIDReceipt(kind, relay) // meeting_receipts.go
		}
		api.HandleFunc(pattern, relay)
	}
}

// storedTerminalTexts are the assistant's routes that keep text it later puts in an agent's prompt (a todo, its
// decisions, a knowledge-base entry): an extension's token may not send terminal control characters on them either.
var storedTerminalTexts = []string{
	"POST /api/v1/todos",
	"PATCH /api/v1/todos/{id}",
	"POST /api/v1/todos/{id}/decisions",
	"PUT /api/v1/kb/{kind}/{id}",
}

// startConversationOnce relays a new conversation's start (its first message) with an operation receipt (#1349): an app
// whose answer was lost asks again with the same X-Persona-Op-Id and gets the first answer instead of a second
// conversation. An accepted start finishes even if the caller went away. Offline nothing reached the assistant, so no
// receipt is kept (the same op id tries again once it is back); cut off on the way it may have started
// (assistant_in_doubt, kept as unknown).
func (h *Hub) startConversationOnce() http.HandlerFunc {
	receipt := h.withClientOp("assistant_start", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Persona-Op-Id") != "" {
			r = r.WithContext(context.WithoutCancel(r.Context()))
		}
		h.forwardSend(w, r, managerOfContext(r))
	})
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.owner(w, r, h.byRequest)
		if !ok {
			return
		}
		r = withManager(withHumanStart(r), id) // a person's own app: its first message is theirs (secret_handoff.go)
		if h.busConnOf(id) == nil {
			writeErrCode(w, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
			return
		}
		receipt(w, r)
	}
}

// assistantLink is the hub's side of assistant.Hub: the bus handlers below answer each call with its ...From method
// for the manager that made it (the tests' in-process forms are in assistant_link_helpers_test.go).
type assistantLink struct{ h *Hub }

// Hello is what of the hub's own the manager needs: the machines and where the hub would run the assistant, the
// cards' switches and the finished push's timings (hub.json's, the hub's), the user's language. The manager's own
// sections (owner, notes, agents, kb, models, a card's wait) are in its manager.json, not here (docs/operations/config.md
// "manager.json").
func (l assistantLink) Hello(context.Context) (assistantwire.Hello, error) {
	h := l.h
	// SecretHandoffGate: this hub checks who approves a hand-off card (secret_handoff.go).
	out := assistantwire.Hello{Locale: string(h.userLang()), SecretHandoffGate: true}
	if m, ok := h.mutesEvent(); ok { // the hub's mutes, before the manager pushes anything (fleet_link.go)
		out.Mutes = m.Mutes
	}
	if h.cfg == nil {
		return out, nil
	}
	out.Assistant = assistantwire.Placement{MachineID: h.defaultAssistantMachine(), Standby: h.isMember()}
	out.Assistant.Machines = h.machineIDs()
	for _, id := range out.Assistant.Machines {
		if h.machineIsWork(id) {
			out.Assistant.WorkMachines = append(out.Assistant.WorkMachines, id)
		}
	}
	out.PersonaView, out.HTMLViews = h.assistantCards()
	out.DoneDelayMS = taskSettleDelay(h.cfg.Notify).Milliseconds()
	// The finished push's timings: the manager's to apply (fleet_push.go there).
	out.Notify = assistantwire.NotifyTimings{DoneDelayMS: taskSettleDelay(h.cfg.Notify).Milliseconds(),
		LongTurnMS: h.cfg.Notify.longTurn().Milliseconds(), BackgroundHoldMS: h.cfg.Notify.backgroundHold().Milliseconds()}
	out.Models = h.hubModels() // the launchers hub.json's machines add (assistant_models.go)
	return out, nil
}

// deviceCallFrom answers device.call from manager from: the machines it lists are its own (the others' are in "others":
// known, not its to use).
func (l assistantLink) deviceCallFrom(ctx context.Context, from string, p assistantwire.DeviceCall) (json.RawMessage, error) {
	h := l.h
	if p.Device == assistantwire.DeviceAll {
		switch p.Method {
		case assistantwire.DeviceDevicesList:
			list := []assistantwire.Device{}
			others := []string{} // ids the hub knows that are not listed: untrusted for now, or absorbed by another entry
			for _, m := range h.machinesNow() {
				if h.machineUntrusted(m.cfg.ID) || h.machineOwner(m.cfg.ID) != from { // none of these (machine_trust.go, ownership.go)
					others = append(others, m.cfg.ID)
					continue
				}
				m.mu.Lock()
				online, info := m.online, m.info
				m.mu.Unlock()
				list = append(list, assistantwire.Device{ID: m.cfg.ID, Name: m.name(), Kind: m.cfg.Kind, SSHHost: m.cfg.SSHHost, Home: info.Home,
					DataDir: info.DataDir, Online: online, Reachable: m.reachable(), Version: info.Version,
					ModelConfiguration: info.ModelConfiguration, ClaudeVersion: info.ClaudeVersion})
			}
			d := &h.devices
			d.mu.Lock()
			for from := range d.absorbed {
				others = append(others, from)
			}
			d.mu.Unlock()
			// complete: the list is every machine there is (the manager reported them and the pending computers were
			// read), so an id in neither it nor others was removed.
			return json.Marshal(map[string]any{"devices": list, "phone_online": h.phoneClient() != nil,
				"complete": h.devicesUp() && !h.trustUnknown(), "others": others, "apps": h.clientAppList(ctx)})
		}
	}
	m := h.localMachine(p.Device)
	if m == nil || h.machineUntrusted(m.cfg.ID) {
		return nil, noDevice(p.Device)
	}
	// The node is reached only while the machine is still from's (busNode.Call checks it again as it sends): a move
	// after the check does not make this call reach the new manager's node.
	ctx = withCaller(ctx, from)
	if out, ok, err := h.channelsDeviceCall(ctx, m, p); ok { // following a turn, sending once (assistant_channels.go)
		return out, err
	}
	switch p.Method {
	case assistantwire.DeviceSessionsList:
		list := h.decorated(m)
		if h.machineOwner(m.cfg.ID) != from { // moved while it was read
			return nil, noDevice(p.Device)
		}
		return json.Marshal(map[string]any{"sessions": list, "muted": h.mutedSessions(m.cfg.ID, list)})
	case assistantwire.DeviceSessionGet:
		var a struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(p.Params, &a); err != nil {
			return nil, err
		}
		s, ok := m.session(a.SessionID)
		if h.machineOwner(m.cfg.ID) != from { // moved while it was read
			return nil, noDevice(p.Device)
		}
		if ok {
			s.Assistant = m.isAssistant(s)
			if s.Assistant {
				s.Space = assistantSpace(s.Cwd)
			}
		}
		return json.Marshal(map[string]any{"session": s, "ok": ok})
	}
	var out json.RawMessage
	if err := m.call(ctx, p.Method, p.Params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// appCallFrom answers app.call from manager from: one naming a machine only on a machine of its own (ownership.go), and
// what concerns a manager (its settings, its extensions' pushes) is from's.
func (l assistantLink) appCallFrom(ctx context.Context, from string, p assistantwire.AppCall) (json.RawMessage, error) {
	h := l.h
	var named struct {
		MachineID string `json:"machine_id"`
	}
	_ = json.Unmarshal(p.Params, &named)
	if named.MachineID != "" && h.machineOwner(named.MachineID) != from {
		return nil, &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("%s on machine %q: another manager's",
			clip(p.Method, 64), clip(named.MachineID, 64))}
	}
	if out, ok, err := h.assistantAppCall(ctx, from, p); ok { // assistant_apps.go
		return out, err
	}
	if out, ok, err := h.extAppCall(ctx, from, p); ok { // what the manager's extensions ask of the hub (extensions_link.go)
		return out, err
	}
	if out, ok, err := h.fleetAppCall(ctx, p); ok { // what the hub still runs for the nodes (fleet_link.go)
		return out, err
	}
	if p.Method == assistantwire.AppTool {
		return h.appTool(ctx, from, p.AppID, p.Params)
	}
	return nil, fmt.Errorf("app %s has no method %s", p.AppID, p.Method)
}

// appTool runs the one of the assistant's tools that is still the hub's to run for manager from: the hub's own
// settings (persona_settings without the assistant's, settings_tool.go there).
func (h *Hub) appTool(ctx context.Context, from, app string, params json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		MachineID string          `json:"machine_id"`
		SessionID string          `json:"session_id"`
	}
	if err := json.Unmarshal(params, &a); err != nil {
		return nil, err
	}
	if len(a.Arguments) == 0 {
		a.Arguments = json.RawMessage(`{}`)
	}
	var res string
	var err error
	switch app + " " + compat.Tool(a.Name) {
	case assistantwire.AppSettings + " persona_settings":
		m := h.localMachine(a.MachineID)
		if m == nil {
			return nil, fmt.Errorf("%w %q", assistantwire.ErrNoDevice, a.MachineID)
		}
		res, err = h.toolPersonaSettings(ctx, from, m, a.SessionID, a.Arguments)
	default:
		return nil, fmt.Errorf("app %s has no tool %s", app, a.Name)
	}
	if err != nil {
		return json.Marshal(proto.TextResult(err.Error(), true))
	}
	return json.Marshal(proto.TextResult(res, false))
}

// snapshotFrom keeps what manager from reported (manager.snapshot) for the hub's own features.
func (l assistantLink) snapshotFrom(_ context.Context, from string, s assistantwire.Snapshot) (assistantwire.SnapshotResult, error) {
	h := l.h
	a := h.assistantOf(from)
	views := make(map[string]assistantwire.SessionView, len(s.Sessions))
	for _, v := range s.Sessions {
		views[v.SessionID] = v
	}
	a.mu.Lock()
	if a.got && s.Epoch == a.epoch && s.Seq <= a.seq {
		a.mu.Unlock()
		return assistantwire.SnapshotResult{}, assistantwire.ErrStaleSnapshot
	}
	oldProfile := a.profile
	a.got, a.epoch, a.seq, a.sessions = true, s.Epoch, s.Seq, views
	if s.Profile != nil {
		a.profile = s.Profile
	}
	a.mu.Unlock()
	// The hub keeps one profile as its own (assistant_profile.go): the assistant it starts.
	if s.Profile != nil && !jsonSame(oldProfile, s.Profile) && from == assistantwire.AssistantID {
		h.keepAssistantProfile(context.Background(), s.Profile)
	}
	// What the nodes keep listing (s.Profile.Keep) the manager tells them itself (internal/devices SetKeep).
	// The session lists' decoration (its titles, todos, work, channels, inbox agents) and its own cards on the phone
	// are the manager's (fleet.go there).
	h.takeTodoReport(from, s.Todos) // the task notices on the items (injected.go)
	return assistantwire.SnapshotResult{OK: true, Seq: s.Seq}, nil
}

// ---- on the bus: what the assistant calls ----

func init() {
	handleBus(assistantwire.MethodDevice, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p assistantwire.DeviceCall
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: assistantwire.MethodDevice + ": " + err.Error()}
		}
		// A manager acts only on its own machines (ownership.go deviceCallAllowed); the list of them is its own too.
		if err := h.deviceCallAllowed(c.managerID(), p); err != nil {
			return nil, err
		}
		out, err := assistantLink{h}.deviceCallFrom(ctx, c.managerID(), p)
		return out, busCodedError(err)
	})
	handleBus(assistantwire.MethodApp, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p assistantwire.AppCall
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: assistantwire.MethodApp + ": " + err.Error()}
		}
		if p.TimeoutMS > 0 { // the assistant stops waiting then: so does the hub (a model call)
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
			defer cancel()
		}
		out, err := assistantLink{h}.appCallFrom(ctx, c.managerID(), p)
		return out, busCodedError(err)
	})
	handleBus(assistantwire.MethodNotify, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var p assistantwire.NotifyRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: assistantwire.MethodNotify + ": " + err.Error()}
		}
		from, id := c.managerID(), p.Notification.ID
		if id != "" && !h.firstHandOver(ctx, from, id) {
			return map[string]bool{"ok": true}, nil // delivered already: it came again (outbox_relay.go)
		}
		// Only about its own machines, sessions and cards (fleet_link.go deliverPushFrom).
		if err := h.deliverPushFrom(from, p.Notification); err != nil {
			if id != "" {
				h.forgetHandOver(from, id)
			}
			return nil, busCodedError(err)
		}
		return map[string]bool{"ok": true}, nil
	})
	handleBus(assistantwire.MethodSnapshot, func(h *Hub, ctx context.Context, c *busConn, params json.RawMessage) (any, error) {
		var s assistantwire.Snapshot
		if err := json.Unmarshal(params, &s); err != nil {
			return nil, &rpc.Error{Code: bus.CodeInvalidParams, Message: assistantwire.MethodSnapshot + ": " + err.Error()}
		}
		res, err := assistantLink{h}.snapshotFrom(ctx, c.managerID(), s)
		return res, busCodedError(err)
	})
	// ws.broadcast is relayBroadcastFrom's (bus_forward.go).
}

// busCodedError puts what the assistant's code tells errors apart by on the wire as a code: a machine the hub does
// not have, one that is offline, a node link that dropped, a stale snapshot, a paused model. A node's own coded
// refusal (*rpc.Error) goes as it is; anything else as its message.
func busCodedError(err error) error {
	var re *rpc.Error
	switch {
	case err == nil || errors.As(err, &re):
		return err
	case errors.Is(err, assistantwire.ErrNoDevice):
		return &rpc.Error{Code: assistantwire.CodeNoDevice, Message: err.Error()}
	case errors.Is(err, errOffline):
		return &rpc.Error{Code: assistantwire.CodeMachineOffline, Message: err.Error()}
	case errors.Is(err, rpc.ErrClosed):
		return &rpc.Error{Code: assistantwire.CodeNodeClosed, Message: err.Error()}
	case errors.Is(err, assistantwire.ErrStaleSnapshot):
		return &rpc.Error{Code: assistantwire.CodeStaleSnapshot, Message: err.Error()}
	}
	return err
}

// assistantHelloFor is manager.hello's answer for manager id, kept as what it last got.
func (h *Hub) assistantHelloFor(ctx context.Context, id string) assistantwire.Hello {
	hello, _ := assistantLink{h}.Hello(ctx)
	hello = h.placedFor(hello, id)
	hello.Mutes = h.mutesFor(id, assistantwire.FleetMutes{Mutes: hello.Mutes}).Mutes // its own machines' only, as fleet.mutes (fleet_link.go)
	raw, _ := json.Marshal(updatedHello(hello))
	a := h.assistantOf(id)
	a.helloMu.Lock()
	a.lastHello = string(raw)
	a.helloMu.Unlock()
	return hello
}

// placedFor is hello's placement for manager id, which acts only on the machines it owns (#5760, ownership.go): the
// machines it owns, the hub's default machine when it owns that one (else its first own one, a local one first; ""
// with none), and the launchers of its own machines only. Its manager.json's assistant.machine goes over that only
// when it names one of them (assistant/ownconfig.go).
func (h *Hub) placedFor(hello assistantwire.Hello, id string) assistantwire.Hello {
	own := []string{}
	for _, mid := range hello.Assistant.Machines {
		if h.machineOwner(mid) == id {
			own = append(own, mid)
		}
	}
	hello.Assistant.Machines = own
	hello.Assistant.WorkMachines = slices.DeleteFunc(slices.Clone(hello.Assistant.WorkMachines), func(mid string) bool { return !slices.Contains(own, mid) })
	eligible := slices.DeleteFunc(slices.Clone(own), func(mid string) bool { return h.machineIsWork(mid) })
	if !slices.Contains(eligible, hello.Assistant.MachineID) {
		hello.Assistant.MachineID = ""
		for _, m := range h.machinesNow() {
			if slices.Contains(eligible, m.cfg.ID) && (hello.Assistant.MachineID == "" || m.cfg.Kind == "local") {
				hello.Assistant.MachineID = m.cfg.ID
				if m.cfg.Kind == "local" {
					break
				}
			}
		}
		if hello.Assistant.MachineID == "" && len(eligible) > 0 {
			hello.Assistant.MachineID = eligible[0]
		}
	}
	if hello.Models.Launchers != nil {
		mine := map[string][]assistantwire.ConfiguredLauncher{}
		for mid, l := range hello.Models.Launchers {
			if slices.Contains(own, mid) {
				mine[mid] = l
			}
		}
		hello.Models.Launchers = nil
		if len(mine) > 0 {
			hello.Models.Launchers = mine
		}
	}
	return hello
}

// legacyConfig is what of hub.json the hub used to hand the built-in manager in its hello (managerconf.Legacy), raw:
// passed on, never read here. Only a built-in manager the hub did not start gets it (managerHello): the hub's child
// took them from this hub.json itself.
func (h *Hub) legacyConfig() json.RawMessage {
	if h.cfg == nil || h.cfg.dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(h.cfg.dir, "hub.json"))
	if err != nil {
		return nil
	}
	return managerconf.Legacy(b)
}

// assistantUpdated tells the connected assistant its hello answer changed (manager.updated): this hub's mesh role, the
// machines (and with them the default machine). It is called wherever one of them may have changed, often (every
// machine update): bursts are taken together, and only a different answer is sent.
func (h *Hub) assistantUpdated() {
	for _, p := range h.busRoster() {
		if p.Role == bus.RoleManager && p.ID != assistantwire.AssistantID {
			h.managerUpdated(p.ID)
		}
	}
	h.managerUpdated(assistantwire.AssistantID)
}

// managerUpdated is assistantUpdated for manager id.
func (h *Hub) managerUpdated(id string) {
	a := h.assistantOf(id)
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		a.updating.Store(false)
		if h.busConnOf(id) == nil {
			return // it gets a fresh one when it connects
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		hello, _ := assistantLink{h}.Hello(ctx)
		hello = updatedHello(h.placedFor(hello, id))
		raw, _ := json.Marshal(hello)
		a.helloMu.Lock()
		same := a.lastHello == string(raw)
		a.lastHello = string(raw)
		a.helloMu.Unlock()
		if !same {
			_ = h.notifyBus(id, assistantwire.EventUpdated, hello)
		}
	}()
}

// updatedHello is hello as manager.updated sends it: without the mutes, which go by fleet.mutes after the handshake
// (a change of them alone is no new hello: the manager would read its profile again for nothing).
func updatedHello(hello assistantwire.Hello) assistantwire.Hello {
	hello.Mutes = nil
	return hello
}

// assistantConnected runs when the assistant's handshake went through: its tools are asked for again (a new build
// may have others) and kept for when it is not connected (its tools then answer assistant_offline); what waited for it
// goes.
func (h *Hub) assistantConnected() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var res struct {
		Tools []proto.Tool `json:"tools"`
	}
	h.managerConnected() // agents_link.go
	h.fleetConnected()   // what of the apps it needs (fleet_link.go)
	generation := h.assistantToolsGeneration()
	if err := h.callBus(ctx, assistantwire.AssistantID, assistantwire.MethodTools, nil, &res); err != nil {
		h.logf("assistant: tools: %v", err)
		return
	}
	if !h.storeAssistantTools(generation, res.Tools) {
		return
	}
}

// ---- on the bus: what the hub asks of the assistant ----

// asst is the assistant, reached over the bus.
func (h *Hub) asst() assistantRemote { return assistantRemote{h: h} }

// asstOf is manager id's assistant (ownership.go: the one a machine, a session, an interaction belongs to).
func (h *Hub) asstOf(id string) assistantRemote { return assistantRemote{h: h, id: id} }

// assistantRemote is assistant.Handler over the assistant's bus connection: calls for what the hub needs an answer
// to, one-way events for what it saw happen. Not connected, a call fails with assistant_offline and an event is not
// sent (the assistant asks for what it missed when it is back: its snapshot, the session lists).
type assistantRemote struct {
	h  *Hub
	id string // the manager; "" is the default one (ownership.go)
}

// to is the manager the call goes to.
func (a assistantRemote) to(ctx context.Context) string {
	if a.id != "" {
		return a.id
	}
	return a.h.defaultOwner(ctx)
}

// errAssistantOffline is what an assistant tool or request answers while the assistant is not connected.
var errAssistantOffline = i18n.Err("hub-assistant_link.offline")

func (a assistantRemote) call(ctx context.Context, method string, params, out any) error {
	err := a.h.callBus(ctx, a.to(ctx), method, params, out)
	if errors.Is(err, errBusOffline) {
		return &rpc.Error{Code: assistantwire.CodeAssistantOffline, Message: errAssistantOffline.Error()}
	}
	return err
}

func (a assistantRemote) notify(event string, params any) {
	_ = a.h.notifyBus(a.to(context.Background()), event, params)
}

// assistantToolsWait is how long a question about the assistant's tools waits for it while it has not connected since
// the hub started (tests shorten it).
var assistantToolsWait = 10 * time.Second

// Tools are the assistant's own MCP tools, as it said when it last connected; while it has not connected since the
// hub started, the hub waits a little for it (a node that asks right then keeps the list for its conversation's life).
func (a assistantRemote) Tools(ctx context.Context) []proto.Tool {
	if id := a.to(ctx); id != assistantwire.AssistantID {
		return a.h.managerTools(ctx, id)
	}
	for wait := time.Now().Add(assistantToolsWait); ; {
		tools := a.h.assistantToolsNow()
		if tools != nil || time.Now().After(wait) || ctx.Err() != nil {
			return tools
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// assistantToolsNow are the assistant's tools as it last listed them; nil before it first connected.
func (h *Hub) assistantToolsNow() []proto.Tool {
	st := &h.assistant
	st.toolsMu.Lock()
	defer st.toolsMu.Unlock()
	return st.tools
}

// Tool runs one of the assistant's tools (assistant.tool). ask_user and persona_settings wait for the phone: up to
// the question's half hour, and then some.
func (a assistantRemote) Tool(ctx context.Context, call assistantwire.ToolCall) proto.ToolResult {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	var res proto.ToolResult
	if err := a.call(ctx, assistantwire.MethodTool, call, &res); err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return res
}

func (a assistantRemote) Context(ctx context.Context, p assistantwire.ContextParams) (assistantwire.ContextResult, error) {
	var res assistantwire.ContextResult
	err := a.call(ctx, assistantwire.MethodContext, p, &res)
	return res, err
}

func (a assistantRemote) Action(ctx context.Context, act assistantwire.Action) (assistantwire.ActionResult, error) {
	var res assistantwire.ActionResult
	err := a.call(ctx, assistantwire.MethodAction, act, &res)
	return res, err
}

func (a assistantRemote) UserSpoke(_ context.Context, r assistantwire.SessionRef) {
	a.notify(assistantwire.EventUserSpoke, r)
}

func (a assistantRemote) UserStopped(_ context.Context, r assistantwire.SessionRef) {
	a.notify(assistantwire.EventUserStopped, r)
}

func (a assistantRemote) TurnStarted(_ context.Context, r assistantwire.SessionRef) {
	a.notify(assistantwire.EventTurnStarted, r)
}

func (a assistantRemote) TurnEnded(_ context.Context, r assistantwire.SessionRef) {
	a.notify(assistantwire.EventTurnEnded, r)
}

func (a assistantRemote) SessionsChanged(_ context.Context, d assistantwire.DeviceSessions) {
	a.notify(assistantwire.EventSessionsChanged, d)
}

func (a assistantRemote) InteractionResolved(_ context.Context, r assistantwire.InteractionResolved) {
	a.notify(assistantwire.EventInteractionResolved, r)
}

func (a assistantRemote) InteractionRaised(_ context.Context, r assistantwire.InteractionRaised) {
	a.notify(assistantwire.EventInteractionRaised, r)
}

// The todos' (assistant_todos.go).

func (a assistantRemote) TodoHandoff(ctx context.Context, p assistantwire.TodoHandoff) (assistantwire.TodoHandoffResult, error) {
	var res assistantwire.TodoHandoffResult
	err := a.call(ctx, assistantwire.MethodTodoHandoff, p, &res)
	return res, err
}

func (a assistantRemote) TodoLink(ctx context.Context, p assistantwire.TodoLinkParams) (assistantwire.TodoLinkResult, error) {
	var res assistantwire.TodoLinkResult
	err := a.call(ctx, assistantwire.MethodTodoLink, p, &res)
	return res, err
}

func (a assistantRemote) TodoSpaceRenamed(_ context.Context, r assistantwire.TodoSpaceRenamed) {
	a.notify(assistantwire.EventTodoSpaceRenamed, r)
}

// ---- the hub's own parts that tell the assistant, or ask it ----

// ---- what the hub answers the assistant from its other parts ----
