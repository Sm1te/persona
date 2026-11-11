package hub

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ---- moving a machine or a source to another manager ----

// machineOwnersKey keeps which of hub.json's machines another manager than the carrier carries: {machine: manager}.
const machineOwnersKey = "managers.machines"

// Not a valid roster ID: failed ownership reads must never select a manager.
const unknownMachineOwner = "!ownership-unavailable"

var (
	errNotMovable  = errors.New("only a computer of this hub's own list can move to another manager")
	errMoveOffline = errors.New("the manager is not connected")
	// errOwnersUnknown: the hub could not read which manager carries which machine; a move now would overwrite it.
	errOwnersUnknown = errors.New("the hub could not read which manager carries which machine; try again")
	// errSourceInDoubt: the import did not answer; whether the new manager has the source could not be told either.
	errSourceInDoubt = errors.New("the new manager did not say whether it took the source")
)

// moves orders a manager's removal against the moves to or from it: moves (machines and sources) hold managers
// shared, a removal exclusive, so it waits for them and the ones after it find the manager gone. A machine's moves
// are one at a time on top of that (d.moving); a source's too (sources).
type moves struct {
	managers sync.RWMutex
	mu       sync.Mutex             // guards sources
	sources  map[string]*sourceLock // a source being moved, while one is
}

// removingManager runs remove (a manager's removal) once no move to or from a manager is under way, and keeps new
// ones waiting until it is done.
func (h *Hub) removingManager(remove func() error) error {
	h.moving.managers.Lock()
	defer h.moving.managers.Unlock()
	return remove()
}

// loadMachineOwners reads machineOwnersKey (a hub that starts, before any manager connects). An error leaves assign
// unread: dedicated local machines are withheld until a successful retry; legacy
// machines keep their existing assignments and carrier fallback.
func (h *Hub) loadMachineOwners(ctx context.Context) error {
	var m map[string]string
	if s := h.managerStore(); s != nil {
		if _, err := s.Setting(ctx, machineOwnersKey, &m); err != nil {
			h.logf("managers: which manager carries which machine: %v", err)
			h.devices.mu.Lock()
			h.devices.assignRead, h.devices.assignFailed = false, true
			h.devices.mu.Unlock()
			return err
		}
	}
	// A machine hub.json no longer has, or an owner the roster no longer has: back to the carrier.
	kept := map[string]string{}
	for mid, o := range m {
		if h.cfgEntry(mid) == nil || o == "" || o == devicesOwner {
			continue
		}
		if has, sure := h.rosterHas(ctx, o); sure && !has {
			continue // its manager was removed (a failed lookup keeps it: it is asked again at the next read)
		}
		kept[mid] = o
	}
	if s := h.managerStore(); s != nil && len(kept) != len(m) {
		if err := s.PutSetting(ctx, machineOwnersKey, kept); err != nil {
			h.logf("managers: which manager carries which machine: %v", err)
		}
	}
	d := &h.devices
	d.mu.Lock()
	d.assign, d.assignRead, d.assignFailed = kept, true, false
	d.mu.Unlock()
	return nil
}

// moveMachine hands hub.json's machine mid to manager to (devicesOwner: back to the carrier). The hub keeps the
// owner (machineOwnersKey) and from now on sends what concerns the machine to the new one; each manager is handed
// its machines again (devices.config): the old one lets it go, the new one connects it. Until the new one reports it
// the machine shows offline. The credentials the hub keeps for it (a service machine's, a Persona Link's) stay with
// the hub and go only to its owner (devices.connector_cred, the link relay).
// Moves go one at a time, each from the owners as the last one left them: two at once each wrote the map without
// the other's machine, and one move was lost on the next start. Owners the hub could not read when it started are
// read first, so a move does not write over them.
func (h *Hub) moveMachine(ctx context.Context, mid, to string) (from string, err error) {
	if h.knownEntry(mid) == nil {
		return "", errNotMovable
	}
	h.moving.managers.RLock() // not while its manager is being removed (removingManager)
	defer h.moving.managers.RUnlock()
	d := &h.devices
	d.moving.Lock()
	defer d.moving.Unlock()
	return h.moveMachineMoving(ctx, mid, to)
}

// moveMachineMoving is moveMachine with d.moving held.
func (h *Hub) moveMachineMoving(ctx context.Context, mid, to string) (from string, err error) {
	d := &h.devices
	// hub.json's entry, or a kept one's as its manager last reported it (owner_required is kept: machines_kept.go).
	if mc := h.knownEntry(mid); mc != nil && mc.OwnerRequired && to == devicesOwner {
		return h.entryOwner(mid), errors.New("a dedicated local machine requires an explicit non-carrier manager")
	}
	if err := h.readOwnersMoving(ctx); err != nil {
		return h.machineOwner(mid), err
	}
	// Still in the roster (a removal does not run while this does: removingManager).
	if to != devicesOwner && !h.knownManager(ctx, to) {
		return h.entryOwner(mid), fmt.Errorf("%w: %q", errUnknownManager, clip(to, 64))
	}
	from = h.entryOwner(mid)
	if from == to {
		return from, nil
	}
	if err := h.carryEngine(ctx, mid, from, to); err != nil {
		return from, err
	}
	if err := h.retireShareMachine(ctx, mid); err != nil {
		return from, err
	}
	if h.keptBy(mid) != "" && h.cfgEntry(mid) == nil { // its manager keeps it (machines_kept.go)
		if err := h.moveKept(ctx, mid, from, to); err != nil {
			return from, err
		}
		h.handMachineEntry(mid, from, to)
		h.machineHandedOver(mid, to)
		h.logf("managers: [%s] moved from %s to %s", mid, from, to)
		return from, nil
	}
	d.mu.Lock()
	next := map[string]string{}
	for k, v := range d.assign {
		next[k] = v
	}
	if to == devicesOwner {
		delete(next, mid)
	} else {
		next[mid] = to
	}
	d.mu.Unlock()
	if s := h.managerStore(); s != nil {
		if err := s.PutSetting(ctx, machineOwnersKey, next); err != nil {
			return from, err
		}
	}
	d.mu.Lock()
	d.assign = next
	delete(d.putTried, mid) // to is put it afresh (settleOwnList)
	d.mu.Unlock()
	h.handMachineEntry(mid, from, to) // shown as last reported until to reports it (machine_entries.go)
	h.machineHandedOver(mid, to)
	h.logf("managers: [%s] moved from %s to %s", mid, from, to)
	// A manager that keeps its own list does not go by the hand-over: from lets it go here, and to is put it from
	// hub.json once it reports (settleOwnList, after this move).
	if h.keepsList(from) {
		var out devicewire.MachineRemoved
		if err := h.keeperCall(ctx, from, devicewire.MethodMachinesRemove, devicewire.MachineRef{MachineID: mid}, &out); err != nil {
			h.logf("managers: [%s] moved away from %s, which could not take it out of its own list: %v", mid, from, err)
		}
	}
	if h.keepsList(to) {
		go h.settleOwnList(to)
	}
	return from, nil
}

// carryEngine puts machine mid's engine choice, as from keeps it, in to before the move (#3611): each manager keeps its
// machines' choices in its own settings, and a machine its new manager has none for would go back to herdr. A from that
// can't say (offline, removed, older) leaves to as it is, logged: a move away from a broken manager still goes; so does
// a to too old to keep one. A choice to fails to take stops the move, so a change of owner never changes the engine.
func (h *Hub) carryEngine(ctx context.Context, mid, from, to string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var got devicewire.MachineEngine
	if err := h.callBus(cctx, from, devicewire.MethodEngineGet, devicewire.MachineRef{MachineID: mid}, &got); err != nil {
		h.logf("managers: [%s] engine choice not carried from %s: %v", mid, from, err)
		return nil
	}
	if got.Choice == nil {
		return nil
	}
	err := h.callBus(cctx, to, devicewire.MethodEnginePut, devicewire.MachineEngine{MachineID: mid, Choice: got.Choice}, nil)
	if re := (*rpc.Error)(nil); errors.As(err, &re) && re.Code == bus.CodeUnknownMethod {
		h.logf("managers: [%s] %s is too old to keep its engine choice: moved without it", mid, to)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s could not take machine %s's engine choice: %w", to, mid, err)
	}
	return nil
}

// readOwnersMoving reads the owners if the hub could not when it started, and hands the machines another manager
// carries to it now. d.moving is held.
func (h *Hub) readOwnersMoving(ctx context.Context) error {
	d := &h.devices
	d.mu.Lock()
	read := d.assignRead
	d.mu.Unlock()
	if read {
		return nil
	}
	if err := h.loadMachineOwners(ctx); err != nil {
		return fmt.Errorf("%w: %v", errOwnersUnknown, err)
	}
	for _, mc := range h.knownEntries() {
		if m := h.localMachine(mc.ID); m != nil && m.ownerOrCarrier() != h.entryOwner(mc.ID) {
			h.machineHandedOver(mc.ID, h.entryOwner(mc.ID))
		}
	}
	return nil
}

// machineHandedOver makes machine mid manager to's in what the hub shows and routes, offline until it reports it,
// withdraws the other managers' cards on it, cuts the relays another manager has for it (Persona Link comes back to
// to), tells the managers their mutes, and hands every manager its machines again.
func (h *Hub) machineHandedOver(mid, to string) {
	h.withdrawCardsOn(mid, to)
	h.cutRelays("", mid)
	defer h.mutesChanged()
	if m := h.localMachine(mid); m != nil {
		m.mu.Lock()
		m.owner = to
		m.mu.Unlock()
		why := i18n.Localize(errAssistantOffline, h.userLang())
		h.offlineLocal(m, why)
		h.connectionEnded(m, why)
		h.broadcastMachines()
		h.sessionsChanged(m)
	}
	h.devicesConfigChanged()
}

// releaseMachines gives the machines removed manager id carried back to the carrier. Called under removingManager.
func (h *Hub) releaseMachines(ctx context.Context, id string) {
	d := &h.devices
	d.moving.Lock() // the whole release: a move meanwhile is not undone by it, nor one of its machines missed
	defer d.moving.Unlock()
	if err := h.readOwnersMoving(ctx); err != nil { // owners not read yet would keep the removed one's machines
		h.logf("managers: %s's machines back to %s: %v", id, devicesOwner, err)
		return
	}
	d.mu.Lock()
	var mine, keptMine []string
	for mid, o := range d.assign {
		if o == id {
			mine = append(mine, mid)
		}
	}
	for mid, o := range d.kept {
		if o == id {
			keptMine = append(keptMine, mid)
		}
	}
	d.mu.Unlock()
	// The machines it kept in its own list went with it: their entries (their secrets) were in its manager.json only.
	for _, mid := range keptMine {
		if err := h.setKept(ctx, mid, ""); err != nil {
			h.logf("managers: [%s] kept by %s, which was removed: %v", mid, id, err)
			continue
		}
		d.mu.Lock()
		reserved := d.assign[mid] != ""
		d.mu.Unlock()
		if reserved { // a dedicated machine's reservation goes with it (machines_owned_local.go)
			if err := h.assignOwner(ctx, mid, devicesOwner); err != nil {
				h.logf("managers: [%s] kept by %s, which was removed: its owner record: %v", mid, id, err)
			}
		}
		h.forgetMachineEntry(mid)
		h.machineGone(mid)
		h.dropPending(ctx, mid, "gone with the manager that kept it") // no pending place or Persona Link left behind
		h.logf("managers: [%s] kept by %s in its own list: gone with it", mid, id)
	}
	for _, mid := range mine {
		if k := h.keptBy(mid); k != "" && k != id && h.cfgEntry(mid) == nil {
			// Kept by another manager now: a stale reservation of id's, dropped, never a move (machines_kept.go).
			if err := h.assignOwner(ctx, mid, devicesOwner); err != nil {
				h.logf("managers: [%s] kept by %s: %s's old reservation: %v", mid, k, id, err)
			}
			continue
		}
		if mc := h.knownEntry(mid); mc != nil && mc.OwnerRequired {
			if err := h.releaseDedicatedMachineMoving(ctx, mid); err != nil {
				h.logf("managers: [%s] release dedicated owner: %v", mid, err)
			}
			continue
		}
		if _, err := h.moveMachineMoving(ctx, mid, devicesOwner); err != nil {
			h.logf("managers: [%s] back to %s: %v", mid, devicesOwner, err)
		}
	}
}

// withdrawCardsOn drops the pending cards on machine mid of another manager than to (mid moved to it: its sessions
// and the questions about them are to's now), those naming one of its sessions without the machine too; the apps
// are told they are over. A card naming no session of mid's stays its manager's.
func (h *Hub) withdrawCardsOn(mid, to string) {
	on := map[string]bool{}
	if m := h.localMachine(mid); m != nil {
		for _, s := range m.sessionList() {
			on[s.SessionID] = true
		}
	}
	f := &h.reported
	f.mu.Lock()
	var gone []string
	for id, ix := range f.interactions {
		if cmp.Or(ix.Manager, assistantwire.AssistantID) == to {
			continue
		}
		if ix.MachineID == mid || ix.MachineID == "" && ix.SessionID != "" && on[ix.SessionID] {
			gone = append(gone, id)
			delete(f.interactions, id)
		}
	}
	f.mu.Unlock()
	for _, id := range gone {
		h.broadcast(map[string]any{"type": "interaction_resolved", "id": id})
	}
}

// forgetMachineOwner drops who carries machine mid, a hub.json entry removed (removeMachineLive): an entry added
// again later starts at the carrier.
func (h *Hub) forgetMachineOwner(ctx context.Context, mid string) {
	_ = h.retireShareMachine(ctx, mid)
	d := &h.devices
	d.moving.Lock()
	defer d.moving.Unlock()
	d.mu.Lock()
	_, had := d.assign[mid]
	next := make(map[string]string, len(d.assign))
	for k, v := range d.assign {
		if k != mid {
			next[k] = v
		}
	}
	read := d.assignRead
	d.mu.Unlock()
	if !had || !read {
		return
	}
	if s := h.managerStore(); s != nil {
		if err := s.PutSetting(ctx, machineOwnersKey, next); err != nil {
			h.logf("managers: [%s] forget who carried it: %v", mid, err)
			return
		}
	}
	d.mu.Lock()
	d.assign = next
	d.mu.Unlock()
}
