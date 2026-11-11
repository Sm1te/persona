package hub

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// What comes from the nodes is the manager's (internal/assistant fleet_*.go; docs/server/machines.md "Who handles computer events"):
// the interactions, each Agent's state, which pushes go and when, the Live Activity's content, the nodes' MCP tools,
// the machines' monitoring. The hub shows and delivers what it reports:
//
//   - fleet.sessions: a machine's session list as the apps get it — kept on the machine (its sessions), sent to the
//     apps (broadcastSessions), answered on GET /api/v1/sessions;
//   - fleet.interaction / fleet.interaction_resolved / fleet.interactions: the pending interactions, kept here,
//     sent to the apps, answered on GET /api/v1/interactions and to a connecting app;
//   - fleet.live: the Live Activity's content and the island's numbers (liveactivity.go pushes it per device);
//   - notify.request: a push to deliver (push_send.go, notifyws.go): which devices take it is each device's settings.
//
// What is kept stays while the manager is away (the apps see the last of it); answers to the cards need it (the
// relayed routes answer 503 assistant_offline). The hub tells it what of the apps it needs (fleet.clients, fleet.seen,
// fleet.mutes), and still runs the phone's tools, the connectors', Cua's and ego's for it (app.call phone, hub).

// fleetCache is what the manager last told of the interactions and the fleet.
type fleetCache struct {
	mu           sync.Mutex
	interactions map[string]proto.Interaction
	muted        map[string][]string                 // machine → its sessions whose pushes are off, as the manager said
	live         *assistantwire.FleetLive            // the assistant's
	lives        map[string]*assistantwire.FleetLive // the other managers' (manager_state.go)
	clients      assistantwire.Clients               // what fleet.clients said last
	clientsSent  bool
	// clientsSend holds one fleet.clients from working out what is true to telling it (fleetClientsChanged).
	clientsSend sync.Mutex
}

func init() {
	handleBusEvent(assistantwire.EventFleetSessions, func(h *Hub, c *busConn, params json.RawMessage) {
		rest, rows, scanned := proto.ScanSessionList(params)
		if !scanned || !proto.DecodeChangedSessions {
			var p assistantwire.FleetSessions
			if json.Unmarshal(params, &p) != nil || h.ownsMachine(c, p.MachineID) != nil {
				return // only its manager lists a machine's sessions (ownership.go)
			}
			h.takeFleetSessions(p)
			return
		}
		// The list apart from the rest (proto.ScanSessionList): decoded the same, but only its sessions whose bytes
		// changed since the machine's last list (Machine.lists); what does not decode drops the whole event, as before.
		var p assistantwire.FleetSessions
		if json.Unmarshal(rest, &p) != nil || h.ownsMachine(c, p.MachineID) != nil {
			return
		}
		m := h.localMachine(p.MachineID)
		if m == nil {
			return
		}
		list, raws, err := m.lists.Decode(rows)
		if err != nil {
			return
		}
		p.Sessions = list
		h.takeFleetSessionsFrom(m, p, raws)
	})
	handleBusEvent(assistantwire.EventFleetInteraction, func(h *Hub, c *busConn, params json.RawMessage) {
		var p assistantwire.FleetInteraction
		if json.Unmarshal(params, &p) != nil || p.Interaction.ID == "" {
			return
		}
		if !h.cardAllowed(c.managerID(), p.Interaction) {
			h.logf("fleet: [%s] card %s on another manager's machine %q: dropped", c.managerID(), clip(p.Interaction.ID, 64), clip(p.Interaction.MachineID, 64))
			return
		}
		p.Interaction.Manager = c.managerID() // its manager's (ownership.go)
		f := &h.reported
		f.mu.Lock()
		if o, ok := f.interactions[p.Interaction.ID]; ok && o.Manager != p.Interaction.Manager {
			f.mu.Unlock()
			return // another manager's card: not replaced
		}
		if f.interactions == nil {
			f.interactions = map[string]proto.Interaction{}
		}
		f.interactions[p.Interaction.ID] = p.Interaction
		f.mu.Unlock()
		h.broadcast(map[string]any{"type": "interaction", "interaction": p.Interaction})
		if !h.cardAllowed(c.managerID(), p.Interaction) { // its machine moved meanwhile (dropCard)
			h.dropCard(c.managerID(), p.Interaction.ID)
		}
	})
	handleBusEvent(assistantwire.EventFleetResolved, func(h *Hub, c *busConn, params json.RawMessage) {
		var p assistantwire.FleetResolved
		if json.Unmarshal(params, &p) != nil || p.ID == "" {
			return
		}
		f := &h.reported
		f.mu.Lock()
		if ix, ok := f.interactions[p.ID]; ok && ix.Manager != c.managerID() {
			f.mu.Unlock()
			return // another manager's
		}
		delete(f.interactions, p.ID)
		f.mu.Unlock()
		h.push.withdraw(p.ID) // a suspended app hears it by push (#4349)
		event := map[string]any{"type": "interaction_resolved", "id": p.ID}
		if p.AnsweredBy != "" {
			event["answered_by"] = p.AnsweredBy // the app marks a question the assistant answered (docs/assistant/assistant.md)
			if p.Reason != "" {
				event["reason"] = p.Reason // "Assistant answered: X, because Y"
			}
			if len(p.Answers) > 0 {
				event["answers"] = p.Answers // what "Change answer" (POST …/reanswer) starts from
			}
		}
		h.broadcast(event)
	})
	handleBusEvent(assistantwire.EventFleetInteractions, func(h *Hub, c *busConn, params json.RawMessage) {
		var p assistantwire.FleetInteractions
		if json.Unmarshal(params, &p) != nil {
			return
		}
		h.takeFleetInteractionsFrom(c.managerID(), p.Interactions)
	})
	handleBusEvent(assistantwire.EventFleetLive, func(h *Hub, c *busConn, params json.RawMessage) {
		var p assistantwire.FleetLive
		if json.Unmarshal(params, &p) != nil {
			return
		}
		f := &h.reported
		f.mu.Lock()
		if id := c.managerID(); id == assistantwire.AssistantID {
			f.live = &p
		} else {
			if f.lives == nil {
				f.lives = map[string]*assistantwire.FleetLive{}
			}
			f.lives[id] = &p
		}
		f.mu.Unlock()
		h.live.changed()
	})
}

// takeFleetSessions keeps a machine's list as the manager reported it and sends it to the apps.
func (h *Hub) takeFleetSessions(p assistantwire.FleetSessions) {
	if m := h.localMachine(p.MachineID); m != nil {
		h.takeFleetSessionsFrom(m, p, nil)
	}
}

// takeFleetSessionsFrom is takeFleetSessions for machine m, with raws, what each session came in (nil: unknown).
func (h *Hub) takeFleetSessionsFrom(m *Machine, p assistantwire.FleetSessions, raws []string) {
	list := p.Sessions
	if list == nil {
		list = []proto.Session{}
	}
	m.setSessionListRaws(list, raws)
	f := &h.reported
	f.mu.Lock()
	if f.muted == nil {
		f.muted = map[string][]string{}
	}
	f.muted[m.cfg.ID] = p.Muted
	f.mu.Unlock()
	h.broadcastSessions(m, m.keptSessions())
}

// takeFleetInteractions is takeFleetInteractionsFrom the assistant the hub starts.
func (h *Hub) takeFleetInteractions(list []proto.Interaction) {
	h.takeFleetInteractionsFrom(assistantwire.AssistantID, list)
}

// takeFleetInteractionsFrom replaces manager owner's pending interactions with what it has now that it
// (re)connected: those gone are over for the apps, the others are sent as they are. The other managers' stay.
func (h *Hub) takeFleetInteractionsFrom(owner string, list []proto.Interaction) {
	// Only cards on its own machines, or naming none (ownership.go cardAllowed).
	list = slices.DeleteFunc(slices.Clone(list), func(ix proto.Interaction) bool { return !h.cardAllowed(owner, ix) })
	f := &h.reported
	f.mu.Lock()
	// Another manager's card with the same id stays its (it is not this one's to replace).
	list = slices.DeleteFunc(list, func(ix proto.Interaction) bool {
		o, ok := f.interactions[ix.ID]
		return ok && o.Manager != owner && o.Manager != ""
	})
	now := make(map[string]proto.Interaction, len(list))
	for i := range list {
		list[i].Manager = owner
		now[list[i].ID] = list[i]
	}
	old := map[string]proto.Interaction{}
	all := make(map[string]proto.Interaction, len(f.interactions)+len(now))
	for id, ix := range f.interactions {
		if ix.Manager == owner || ix.Manager == "" {
			old[id] = ix
		} else {
			all[id] = ix
		}
	}
	for id, ix := range now {
		all[id] = ix
	}
	f.interactions = all
	f.mu.Unlock()
	defer func() {
		for _, ix := range list { // after the apps were told: its machine moved meanwhile (dropCard)
			if !h.cardAllowed(owner, ix) {
				h.dropCard(owner, ix.ID)
			}
		}
	}()
	for id := range old {
		if _, ok := now[id]; !ok {
			h.push.withdraw(id)
			h.broadcast(map[string]any{"type": "interaction_resolved", "id": id})
		}
	}
	for _, ix := range list {
		if o, ok := old[ix.ID]; !ok || !sameJSON(o, ix) {
			h.broadcast(map[string]any{"type": "interaction", "interaction": ix})
		}
	}
}

// dropCard withdraws card id of manager owner's that was kept as its machine moved away. The move's withdrawal
// (withdrawCardsOn) may have run between the check and keeping it, or between keeping it and telling the apps, so a
// card is checked again once the apps were told: then it goes, and the apps are told it is over, even when the
// withdrawal took it already (it was shown again after). Another manager's card with the id stays.
func (h *Hub) dropCard(owner, id string) {
	f := &h.reported
	f.mu.Lock()
	ix, ok := f.interactions[id]
	other := ok && cmp.Or(ix.Manager, assistantwire.AssistantID) != owner
	if ok && !other {
		delete(f.interactions, id)
	}
	f.mu.Unlock()
	if !other {
		h.push.withdraw(id)
		h.broadcast(map[string]any{"type": "interaction_resolved", "id": id})
	}
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// decorated is a machine's sessions as the apps get them: the manager's last list.
func (h *Hub) decorated(m *Machine) []proto.Session {
	list := m.sessionList()
	h.stampSessions(m, list)
	return list
}

// stampSessions puts on each of m's sessions the manager it belongs to: its machine's (ownership.go).
func (h *Hub) stampSessions(m *Machine, list []proto.Session) {
	owner := h.machineOwner(m.cfg.ID)
	for i := range list {
		list[i].Manager = owner
	}
}

// stamped is list with manager on each session, as stampSessions puts it: list itself when every session has it
// already, else a copy. list is never changed.
func stamped(list []proto.Session, manager string) []proto.Session {
	for i := range list {
		if list[i].Manager != manager {
			out := slices.Clone(list)
			for j := i; j < len(out); j++ {
				out[j].Manager = manager
			}
			return out
		}
	}
	return list
}

// decoratedSession is one of m's sessions as the apps get it: that one copied and stamped, not the whole list.
func (h *Hub) decoratedSession(m *Machine, sid string) (proto.Session, bool) {
	s, ok := m.session(sid)
	if ok {
		s.Manager = h.machineOwner(m.cfg.ID)
	}
	return s, ok
}

// listedWaitMax is how long awaitListed waits at most (Hub.listedWait shortens it in tests: their fake nodes start
// sessions that are never listed, so each start would wait all of it).
const listedWaitMax = 2 * time.Second

// awaitListed waits a little (listedWaitMax at most) for the manager's list of m to have session sid: a session just
// started is shown as the manager has it.
func (h *Hub) awaitListed(ctx context.Context, m *Machine, sid string) (proto.Session, bool) {
	wait := listedWaitMax
	if h.listedWait > 0 {
		wait = h.listedWait
	}
	for end := time.Now().Add(wait); ; {
		if s, ok := m.session(sid); ok {
			return s, true
		}
		if time.Now().After(end) || ctx.Err() != nil {
			return proto.Session{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// mutedSessions are those of the machine's sessions whose pushes are off, as the manager said.
func (h *Hub) mutedSessions(mid string, _ []proto.Session) []string {
	f := &h.reported
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.muted[mid])
}

// pendingInteractions are the pending interactions as the manager told them, oldest first.
func (h *Hub) pendingInteractions() []proto.Interaction {
	f := &h.reported
	f.mu.Lock()
	out := make([]proto.Interaction, 0, len(f.interactions))
	for _, ix := range f.interactions {
		out = append(out, ix)
	}
	f.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// pendingInteraction is one of them; false when it is not pending.
func (h *Hub) pendingInteraction(id string) (proto.Interaction, bool) {
	f := &h.reported
	f.mu.Lock()
	defer f.mu.Unlock()
	ix, ok := f.interactions[id]
	return ix, ok
}

// fleetLive is the Live Activity's content and numbers as the manager told them last.
// The Live Activity is one per phone: it shows the default manager's, else the assistant's (manager_state.go).
func (h *Hub) fleetLive() (FleetState, FleetStats) {
	st, stats, _ := h.fleetLiveOf()
	return st, stats
}

// fleetLiveOf is fleetLive with the manager whose it is: the activity counts that manager's computers' agents only.
func (h *Hub) fleetLiveOf() (FleetState, FleetStats, string) {
	def := h.defaultOwner(context.Background())
	f := &h.reported
	f.mu.Lock()
	defer f.mu.Unlock()
	live, from := f.live, assistantwire.AssistantID
	if l := f.lives[def]; l != nil && def != assistantwire.AssistantID {
		live, from = l, def
	}
	if live == nil {
		return FleetState{}, FleetStats{}, from
	}
	return live.State, live.Stats, from
}

// waitInteraction asks the user on the phone for one of the hub's own features (a mail send, Cua's apps, ego's
// scripts, persona_settings): the manager shows the card and waits for its answer (fleet.ask). Without the manager
// nothing can be asked: a dismiss.
func (h *Hub) waitInteraction(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	// No expiry given: the manager's own wait for a card (its manager.json hook_timeout_minutes) applies.
	if h.askVia != nil { // tests play the manager
		return h.askVia(ctx, ix)
	}
	var r proto.Resolution
	if err := h.asstOf(h.machineOwner(ix.MachineID)).call(ctx, assistantwire.MethodFleetAsk, map[string]any{"interaction": ix}, &r); err != nil {
		h.logf("[%s] ask %s: %v", ix.MachineID, ix.ToolName, err)
		return proto.Resolution{Dismiss: true}, nil
	}
	return r, nil
}

// ---- what the hub tells the manager of the apps ----

// fleetClientsChanged tells the managers what the apps can take now, when it changed (fleet.clients): an app that
// takes MCP forms and links is connected, or a phone that registered for them would be pushed one (#2433). It runs
// after an app connects, says hello or goes, and after the devices or their settings change. One at a time, each with
// what is true then, so an older state never overtakes a newer one.
func (h *Hub) fleetClientsChanged() {
	f := &h.reported
	f.clientsSend.Lock()
	defer f.clientsSend.Unlock()
	c := assistantwire.Clients{Elicitation: h.hasElicitationClient(), ElicitationPush: h.push.pushesElicitation(h.desktopOnline())}
	f.mu.Lock()
	same := f.clientsSent && f.clients == c
	f.clients, f.clientsSent = c, true
	f.mu.Unlock()
	if !same {
		if err := h.notifyManagers(assistantwire.EventClients, c); err != nil {
			f.mu.Lock()
			f.clientsSent = false
			f.mu.Unlock()
		}
	}
}

// clientsSettle runs on a manager connection's first word after its handshake, as mutesSettle does, and tells it what
// the apps can take now (fleet.clients). The hub says it again only when it changes, and at a handshake only to the
// assistant: a manager connected since would not know a phone takes MCP forms by push, which seldom changes (#2433).
// After the first word it lands after the hello's answer.
func (h *Hub) clientsSettle(c *busConn) {
	if c == nil || c.ident.Role != bus.RoleManager || !c.clientsSettled.CompareAndSwap(false, true) {
		return
	}
	go func() {
		h.fleetClientsChanged() // a change not told yet goes to every manager
		f := &h.reported
		f.clientsSend.Lock()
		defer f.clientsSend.Unlock()
		f.mu.Lock()
		clients := f.clients
		f.mu.Unlock()
		if h.busCurrent(c) {
			_ = c.peer.Notify(assistantwire.EventClients, clients)
		}
	}()
}

// fleetConnected: the manager's handshake went through: it is told what of the apps it needs.
func (h *Hub) fleetConnected() {
	f := &h.reported
	f.mu.Lock()
	f.clientsSent = false
	f.mu.Unlock()
	h.fleetClientsChanged()
}

// markSeen tells the manager the user looked at a session (an app's WebSocket seen).
func (h *Hub) markSeen(machine, session string) {
	_ = h.notifyBus(h.sessionOwner(machine, session), assistantwire.EventSeen, assistantwire.FleetSeen{MachineID: machine, SessionID: session})
}

// mutesChanged tells every manager the muted spaces / agents (the hub's settings mutes) after a change and after a
// machine moved; each also gets them in its hello's answer (assistant_link.go). They are the hub's: a manager never
// reads them from a database. Each manager's fleet.mutes has its own machines' only (mutesFor). Mutes that could not
// be read are not sent: the managers keep the last ones (an empty set would push every muted agent). Sends are one at
// a time, each with the mutes as they are then, so an older set never overtakes a newer one.
func (h *Hub) mutesChanged() {
	h.mutesSend.Lock()
	defer h.mutesSend.Unlock()
	m, ok := h.mutesEvent()
	if !ok {
		return
	}
	for _, peer := range h.busRoster() {
		if peer.Role == bus.RoleManager {
			_ = h.notifyBus(peer.ID, assistantwire.EventMutes, h.mutesFor(peer.ID, m))
		}
	}
}

// mutesFor is m as manager id gets it: the mutes of its own machines only ("<machine>/<space or session>",
// ownership.go), never another manager's.
func (h *Hub) mutesFor(id string, m assistantwire.FleetMutes) assistantwire.FleetMutes {
	if m.Mutes == nil {
		return m
	}
	mine := func(keys []string) []string {
		if keys == nil {
			return nil
		}
		out := make([]string, 0, len(keys))
		for _, k := range keys {
			if mid, _, _ := strings.Cut(k, "/"); h.machineOwner(mid) == id {
				out = append(out, k)
			}
		}
		return out
	}
	return assistantwire.FleetMutes{Mutes: &assistantwire.Mutes{Spaces: mine(m.Mutes.Spaces), Sessions: mine(m.Mutes.Sessions),
		Allowed: mine(m.Mutes.Allowed)}}
}

// mutesRetry hands manager id the mutes once they can be read (they were not when it connected), while it is
// connected; it gives up after mutesRetries tries (the manager keeps the ones it had; the next change sends them).
func (h *Hub) mutesRetry(id string) {
	for range mutesRetries {
		time.Sleep(mutesRetryEvery)
		if h.busConnOf(id) == nil {
			return
		}
		h.mutesSend.Lock()
		m, ok := h.mutesEvent()
		if ok {
			_ = h.notifyBus(id, assistantwire.EventMutes, h.mutesFor(id, m))
		}
		h.mutesSend.Unlock()
		if ok {
			return
		}
	}
	h.logf("mutes: still unreadable: [%s] keeps the ones it had", id)
}

// mutesRetryEvery and mutesRetries: how often and how many times mutesRetry tries (tests shorten them).
var (
	mutesRetryEvery = 5 * time.Second
	mutesRetries    = 60
)

func (h *Hub) mutesEvent() (assistantwire.FleetMutes, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, ok := h.readMutes(ctx)
	return assistantwire.FleetMutes{Mutes: &assistantwire.Mutes{Spaces: p.Spaces, Sessions: p.Sessions, Allowed: p.Allowed}}, ok
}

// mutesSettle runs on a manager connection's first word after its handshake and sends it the mutes once more. The
// hello's answer and a fleet.mutes sent while it was on its way race at the manager (the answer is taken on another
// goroutine than the events, and events of its last connection may still be queued): either could be taken last.
// The manager says nothing before it took the answer (bus.Client), so these land after it. A manager from before the
// answer carried the mutes has them from here.
func (h *Hub) mutesSettle(c *busConn) {
	if c == nil || c.ident.Role != bus.RoleManager || !c.mutesSettled.CompareAndSwap(false, true) {
		return
	}
	go func() {
		h.mutesSend.Lock()
		defer h.mutesSend.Unlock()
		if m, ok := h.mutesEvent(); ok && h.busCurrent(c) { // unreadable: mutesRetry's
			_ = c.peer.Notify(assistantwire.EventMutes, h.mutesFor(c.managerID(), m))
		}
	}()
}

// fleetRoutesRelayed relays the apps' requests on the manager's routes on the interactions and the sessions' states
// (it answers them as the hub did); while it is not connected they are answered 503 assistant_offline. A device
// that may only answer is held to a card's own choices first, here.
func (h *Hub) fleetRoutesRelayed(api *http.ServeMux) {
	api.HandleFunc("POST /api/v1/interactions/{id}/resolve", h.withoutTerminalControls(h.withClientOp("resolve", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			writeBodyErr(w, err, err)
			return
		}
		var res proto.Resolution
		if err := json.Unmarshal(body, &res); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		r, ok := h.handoffGate(w, r, r.PathValue("id"), res) // who may approve a hand-off card (secret_handoff.go)
		if !ok {
			return
		}
		if answerOnly(r) && (!answerMayResolve(res) || h.picksFreeText(r.PathValue("id"), res)) {
			writeErr(w, http.StatusForbidden, errors.New("this device can only answer with a card's own choices"))
			return
		}
		if pr := principalOf(r.Context()); pr.Role == roleExtension {
			if why, mode, mid, sid := h.extensionResolveRefused(pr, r.PathValue("id"), res); why != "" { // extension_modes.go
				h.refuseExtensionMode(w, r, pr, mid, sid, mode, why)
				return
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h.forwardOwned(w, r, h.byInteraction)
	})))
	// Body client_id uses the same durable operation receipts as resolve.
	api.HandleFunc("POST /api/v1/interactions/{id}/choose", h.withReplyReceipt("choose", h.serveChoose))
	// "Change answer": a question the assistant answered gets the user's answer (assistant/fleet_delegate.go); a device that may
	// only answer cards does not send agents messages.
	api.HandleFunc("POST /api/v1/interactions/{id}/reanswer", h.withoutTerminalControls(func(w http.ResponseWriter, r *http.Request) {
		if answerOnly(r) {
			writeErr(w, http.StatusForbidden, errors.New("this device can only answer with a card's own choices"))
			return
		}
		h.forwardOwned(w, r, h.byRequest) // answered already: no longer pending, the request names its manager
	}))
	api.HandleFunc("GET /api/v1/interactions/history", func(w http.ResponseWriter, r *http.Request) {
		if h.requestHasElicitation(r) {
			r.Header.Set(assistantwire.ElicitationHeader, "1")
		} else {
			r.Header.Del(assistantwire.ElicitationHeader)
		}
		h.forwardOwned(w, r, h.byRequest)
	})
	api.HandleFunc("POST /api/v1/sessions/{mid}/{sid}/seen", h.withMachine(func(w http.ResponseWriter, r *http.Request, _ *Machine) {
		h.forwardOwned(w, r, h.bySession)
	}))
}

// ---- what the manager asks of the hub for the nodes ----

// fleetAppCall answers the manager's app.call for what is still the hub's to run for the nodes: the phone's tools;
// false for the others. (The connectors', Cua's and ego's tools, the gated commands' text, lark-cli's settings and the
// channels' owner actions are the manager's extensions': internal/extmgr, in its process.)
func (h *Hub) fleetAppCall(ctx context.Context, p assistantwire.AppCall) (json.RawMessage, bool, error) {
	var a struct {
		Assistant     bool            `json:"assistant"`
		MachineID     string          `json:"machine_id"`
		SessionID     string          `json:"session_id"`
		Name          string          `json:"name"`
		Args          json.RawMessage `json:"args"`
		ToolName      string          `json:"tool_name"`
		Summary       string          `json:"summary"`
		InteractionID string          `json:"interaction_id"`
		Action        string          `json:"action"`
	}
	if len(p.Params) > 0 {
		if err := json.Unmarshal(p.Params, &a); err != nil {
			return nil, true, err
		}
	}
	var out any
	switch p.AppID + " " + p.Method {
	case assistantwire.AppPhone + " " + assistantwire.PhoneTools:
		out = map[string]any{"tools": h.phoneToolList()}
	case assistantwire.AppPhone + " " + assistantwire.PhoneCall:
		if a.Name == runtimeCLITool || !strings.HasPrefix(a.Name, "phone_") {
			out = proto.TextResult("unknown tool "+a.Name, true)
		} else {
			out = h.callPhone(ctx, a.Name, a.Args)
		}
	default:
		return nil, false, nil
	}
	b, err := json.Marshal(out)
	return b, true, err
}

// ---- the pushes the manager decided on ----

// deliverPush is deliverPushFrom the assistant the hub starts.
func (h *Hub) deliverPush(n assistantwire.Notification) error {
	return h.deliverPushFrom(assistantwire.AssistantID, n)
}

// deliverPushFrom delivers what manager from asks (notify.request): an assistant's push, an agent's finished turn, a
// card. Only the center has apps and devices. Only about its own (ownership.go): a card's push is for a card it
// reported that is pending, and the hub pushes its own copy of it; any other push names a machine and session of its
// own, or none. What the extensions are told of it (agent.finished, agent.needs_you) goes to from's only, and the
// devices that muted from (NotifyPrefs.MutedManagers) get none of it.
func (h *Hub) deliverPushFrom(from string, n assistantwire.Notification) error {
	switch n.Kind {
	case assistantwire.NotifyInput:
		if n.Interaction == nil {
			return errors.New("an input push needs its interaction")
		}
		ix, ok := h.pendingInteraction(n.Interaction.ID)
		if !ok || cmp.Or(ix.Manager, assistantwire.AssistantID) != from {
			return &rpc.Error{Code: devicewire.CodeNoDevice, Message: "an input push is for a pending card the manager reported"}
		}
		h.notifyInteraction(from, ix, n.Subtitle, n.LinkCovered)
		return nil
	case assistantwire.NotifyPush, assistantwire.NotifyDone, assistantwire.NotifyReply, assistantwire.NotifyDecide:
		if !h.ownsRef(from, n.DeviceID, n.SessionID) {
			return &rpc.Error{Code: devicewire.CodeNoDevice, Message: fmt.Sprintf("a push about machine %q, session %q: another manager's",
				clip(n.DeviceID, 64), clip(n.SessionID, 64))}
		}
	}
	switch n.Kind {
	case assistantwire.NotifyPush: // one with an id shows once, sent again however late (a day recap's push)
		h.notifyDoneUnder(from, "assistant", n.DeviceID, n.Subtitle, n.SessionID, n.Title, n.Body, pushWants{}, managerPushCollapseID(from, n.ID))
	case assistantwire.NotifyDone:
		h.notifyDone(from, "done", n.DeviceID, n.Subtitle, n.SessionID, n.Title, n.Body,
			pushWants{Kind: "done", Took: time.Duration(n.TookMS) * time.Millisecond, Long: time.Duration(n.LongMS) * time.Millisecond})
	case assistantwire.NotifyReply:
		h.notifyReply(from, n.DeviceID, n.Subtitle, n.SessionID, n.Title, n.Body)
	case assistantwire.NotifyDecide:
		if n.Todo == nil || n.Todo.ID == "" || n.Todo.AskedAt.IsZero() || n.Todo.Recommend == "" {
			return errors.New("a decision's push needs its to-do, when it was asked and the option its OK takes")
		}
		h.notifyDecide(from, n.DeviceID, n.Subtitle, n.SessionID, n.Title, n.Body, *n.Todo)
	default:
		return fmt.Errorf("unknown notification kind %q", n.Kind)
	}
	return nil
}
