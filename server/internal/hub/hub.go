// Package hub serves the iPhone / Watch apps and coordinates the nodes on every machine.
package hub

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/hubedge"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/update"
)

type Hub struct {
	passwordLogin passwordLoginState
	// errs: the hub's own error reports on their way to the managers (error_reports.go).
	errs hubErrors

	deployCtx     context.Context // canceled with the hub; accepted node updates resume from Postgres
	nodeWorkers   sync.WaitGroup
	shutdown      *shutdownBudget
	wsStopping    bool // guarded by mu; prevents new WaitGroup additions during shutdown
	wsHandlers    sync.WaitGroup
	wsConnections map[*client]struct{} // includes connections still registering their device session
	relay         relayState           // the connections relayed to the manager: Persona Link (devices_relay.go)
	linkPing      [2]time.Duration     // tests: heartbeat interval and timeout of new links
	listedWait    time.Duration        // tests: how long a started session is awaited in the list (0: listedWaitMax)
	bus           busState             // the services' connections and the roster (bus.go)
	// extProofs are the managers' proofs' nonces seen lately, extRoster the roster's answers lately about managers off
	// the bus that proofs name (extensions_proof.go).
	extProofs proofNonces
	extRoster rosterSeen
	// extLost are when this hub started and when managers' bus connections ended here lately (extensions_proof.go).
	extLost busLosses
	// tests: replaces os.Hostname
	hostname func() (string, error)

	cfg       *Config
	version   string
	build     update.Build // channel, build number, where updates come from (docs/operations/hub-update.md)
	upd       *updater     // updating itself to the publisher's release (selfupdate.go)
	logger    *log.Logger
	traffic   trafficLog // privacy-safe minute counters, flushed to the journal
	machines  []*Machine
	byID      map[string]*Machine
	push      *Pusher
	agentWork agentWork    // whether the manager is there to ask (agents_link.go)
	store     Store        // nil in unit tests: nothing is persisted
	lockTests lockTestWake // wakes the lock test dispatcher when a test is scheduled (locktest.go)

	terms terminals  // the terminal tab's live streams (terminal.go)
	fulls fullRelays // the web's full herdr views (herdrclient.go)

	devices devicesState // the manager's machines as it reports them (devices_link.go)
	trust   machineTrust // the computers waiting to be confirmed (machine_trust.go)
	// machineAdd: one computer is added for a pairing code at a time (machines_add.go), so two get different ids.
	machineAdd sync.Mutex

	// handedOver: the managers' pushes and outbox items delivered lately, so one sent again is delivered once
	// (outbox_relay.go).
	handedOver seenItems

	mutesMu   sync.Mutex
	mutesSend sync.Mutex // one send of the mutes to the managers at a time (fleet_link.go)
	mutes     *Pins      // muted spaces / agents (no pushes); loaded from settings on first use

	todoOwnersMu sync.RWMutex
	todoOwners   map[string]string // todo id -> manager id

	inboxForms   inboxForms   // the inbox lists' items as localized lately (inbox_forms.go)
	inboxAnswers inboxAnswers // the merged inbox answer, reused while no manager's inbox moved (inbox_answers.go)
	// handoffRecorded: the refused approvals of hand-off cards recorded lately (secret_handoff.go).
	handoffRecorded handoffRecorded

	// sessionsSent: each machine's last session list sent to the apps, as encoded (broadcastSessions).
	sessionsMu   sync.Mutex
	sessionsSent map[string][]sentSession
	listCache    runtimeList // opt-in indexed transport rows; initialized by the first paginated reader

	mu      sync.Mutex
	clients map[*client]struct{}
	events  *eventLog // what was broadcast lately, numbered (eventlog.go); guarded by mu
	// rings: the notify events kept for the devices without a push route while they have no ws_notify socket
	// (notifyring.go); guarded by mu.
	rings notifyRings

	// linkDesktops: the computers whose Persona Link says the user is at them (desktops.go); guarded by mu.
	linkDesktops linkDesktops

	// stopping: a planned stop has begun (beginStop, docs/operations/hub-restart.md): what the stop itself causes (machines going
	// offline, their questions ending) is not news for the apps, which reconnect to the next hub in a moment.
	stopping atomic.Bool
	// slot: the hub's edge started this process (hub_edge.go, docs/operations/hub-restart.md "Blue-green switching: the hub's edge"); it listens on the edge's
	// sockets, and an update or a rollback hands over to the next slot instead of exec'ing. nil: the hub listens itself.
	slot *hubedge.Slot
	// deltas: the live text of each session's message being generated, as last relayed (machine, session -> text): a
	// client subscribing in the middle of it gets it after the backlog instead of waiting for the next token.
	deltas     map[[2]string]string
	phoneTools map[string]db.PhoneToolRegistration
	calls      map[string]chan proto.ToolResult
	wokeAt     map[string]time.Time // device -> last silent push to wake it for a tool call
	live       *liveActivities      // the lock screen / Dynamic Island activity (liveactivity.go)

	// phoneToolsMu: one phone tool registration written at a time, so the rows and phoneTools agree on the latest.
	// Never held with mu taken first; the write it covers runs outside mu (#5358).
	phoneToolsMu sync.Mutex
	phoneWrites  phoneWriteQueue

	// askVia replaces fleet.ask in tests that play the manager (waitInteraction).
	askVia func(context.Context, proto.Interaction) (proto.Resolution, error)
	// reported is what the manager told of the nodes' interactions and the fleet (fleet_link.go).
	reported fleetCache

	ports portOpens // ports the user opened in the app (ports.go)
	// assistant: what the assistant (internal/assistant, its own process on the bus) last reported (assistant_link.go).
	assistant assistantState
	owners    ownerCache    // the default manager, kept a moment (ownership.go)
	others    managerStates // the other managers' (manager_state.go)
	moving    moves         // who carries what changing (ownership_moves.go)
	// userLocale: the user's language, an i18n.Lang; langs: each device's language that decides it (locale.go)
	userLocale   atomic.Value
	langs        userLangs
	apps         clientApps   // the apps' versions as they last said (client_apps.go)
	deleted      deletedBook  // conversations taken out of the apps' lists (deleted_sessions.go)
	secret       secretState  // secretkey.go
	devtok       devTokens    // device token lookups (devicetokens.go)
	shareIngress shareIngress // anonymous admission limits; no sharing business state
	public       publicState  // TLS identity, listener and advertised addresses (public.go)
	sends        sendLog      // messages being / just sent, by client_msg_id (send.go)
	fresh        freshStarts  // agents started for a todo without a prompt, until their first message (assistant_todos.go)
	// ext is what the hub keeps of the extensions, connectors and bots, the manager's (extensions_link.go).
	ext   extLink
	admin adminState // the admin UI (admin.go, docs/operations/admin.md)

	apiMux *http.ServeMux // the API's routes: the one a request goes to decides what an extension's token may do

	mesh  meshState // the other hubs of the mesh group (mesh.go)
	fleet fleet     // guards machines and byID (machines.go)

	todoLinks todoLinks // what the assistant reports of its todos' agents (assistant_todos.go)

	// The looks built into personad (persona.go; nil in unit tests) and the tier each tiered look shows (persona_tiers.go).
	builtinLooks fs.FS
	tiers        personaTierState
}

// Store is the hub's persistence (internal/db on Postgres).
type Store interface {
	CreateLockTest(ctx context.Context, t db.LockTest) error
	LockTest(ctx context.Context, deviceID, testID string) (*db.LockTest, error)
	DueLockTests(ctx context.Context, at time.Time) ([]db.LockTest, error)
	NextLockTestDue(ctx context.Context) (*time.Time, error)
	AttemptLockTest(ctx context.Context, testID string, sessionID int64, at time.Time) (bool, error)
	ReceiveLockTest(ctx context.Context, deviceID, testID string, shown *bool, at time.Time) (*db.LockTest, error)
	RecoverLockTests(ctx context.Context, at time.Time) error
	PruneLockTests(ctx context.Context, before time.Time) error
	InsertDeviceSession(ctx context.Context, s db.DeviceSession) (int64, error)
	UpdateDeviceSessionHeartbeat(ctx context.Context, id int64, interval int) error
	FinishDeviceSession(ctx context.Context, id int64, at time.Time, reason string, interval int) error
	StopDeviceSessions(ctx context.Context, at time.Time) error
	PruneDeviceSessions(ctx context.Context, before time.Time) error
	DeviceSessions(ctx context.Context, deviceID string, since, until time.Time) (db.DeviceSessionHistory, error)
	BeginClientOp(ctx context.Context, op db.ClientOp) (*db.ClientOp, bool, error)
	ClientOp(ctx context.Context, id string) (*db.ClientOp, error)
	FinishClientOp(ctx context.Context, id, state string, result json.RawMessage) error
	RecoverClientOps(ctx context.Context) error
	CleanupClientOps(ctx context.Context, before time.Time) error
	ReplacePhoneTools(ctx context.Context, registration db.PhoneToolRegistration) error
	PhoneTools(ctx context.Context) ([]db.PhoneToolRegistration, error)
	FindQueuedPhoneWrite(ctx context.Context, tool string, args json.RawMessage) (*db.PhoneWrite, error)
	EnqueuePhoneWrite(ctx context.Context, id, tool string, args json.RawMessage) (*db.PhoneWrite, error)
	PhoneWrites(ctx context.Context) ([]db.PhoneWrite, error)
	QueuedPhoneWrites(ctx context.Context) ([]db.PhoneWrite, error)
	ClaimPhoneWrite(ctx context.Context, id string) (bool, error)
	FinishPhoneWrite(ctx context.Context, id, state string, result json.RawMessage) error
	CancelPhoneWrite(ctx context.Context, id string) (*db.PhoneWrite, error)
	RecoverPhoneWrites(ctx context.Context) error
	ClaimSend(ctx context.Context, id, machine, session string) (string, error)
	SettleSend(ctx context.Context, id, state string) error
	UpsertDevice(ctx context.Context, d db.Device) error
	Devices(ctx context.Context) ([]db.Device, error)
	DeleteDevice(ctx context.Context, id string) error
	DeleteDeviceIfCurrent(ctx context.Context, expected db.Device) (bool, error)
	ReconcilePushDevices(ctx context.Context, at time.Time, maxIdle time.Duration) ([]db.PushDeviceRetirement, error)
	SetDevicePrefs(ctx context.Context, id, credential string, prefs json.RawMessage) error
	LiveActivities(ctx context.Context) ([]db.LiveActivity, error)
	SaveLiveActivity(ctx context.Context, v db.LiveActivity) error
	ClaimLiveActivityStart(ctx context.Context, device, startToken string, at time.Time, cooldown, maxAge time.Duration) (time.Time, bool, error)
	Setting(ctx context.Context, key string, out any) (bool, error)
	PutSetting(ctx context.Context, key string, v any) error
	TakeSetting(ctx context.Context, key string, out any) (bool, error)
	CreateDeviceToken(ctx context.Context, t db.DeviceToken, hash string) error
	DeviceTokenByHash(ctx context.Context, hash string) (*db.DeviceToken, error)
	DeviceTokens(ctx context.Context) ([]db.DeviceToken, error)
	RevokeDeviceToken(ctx context.Context, id string) (bool, error)
	TouchDeviceToken(ctx context.Context, id string) error
	CreatePairToken(ctx context.Context, hash string, expiresAt time.Time, machine string) error
	PairTokenByHash(ctx context.Context, hash string) (*db.PairToken, error)
	RedeemPairToken(ctx context.Context, pairHash string, device db.DeviceToken, tokenHash string) (bool, error)
}

func Run(ctx context.Context, b update.Build) error {
	claimInherited() // before anything can start a process: runHub starts the manager before it listens (listen.go)
	logger := log.New(os.Stderr, "", log.LstdFlags)
	// An update that keeps failing goes back before anything else can fail, the config included (selfupdate.go).
	updating := newUpdater(&Hub{build: b, version: b.Version, logger: logger}).boot()
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	return runConfigured(ctx, b, cfg, updating, nil)
}

// runConfigured runs the hub until ctx ends; updating: an update is under way, this process confirms it. slot: the
// edge started it (nil: it listens itself).
func runConfigured(ctx context.Context, b update.Build, cfg *Config, updating bool, slot *hubedge.Slot) error {
	budget := newShutdownBudget(cfg)
	defer budget.close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return budget.run(ctx, func() error { return runHub(ctx, b, cfg, budget, updating, slot) })
}

func runHub(ctx context.Context, b update.Build, cfg *Config, budget *shutdownBudget, updating bool, slot *hubedge.Slot) error {
	// The stop asked for (stop) begins with beginStop, before anything sees its context end (restart.go).
	stop := ctx
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	h := &Hub{
		deployCtx: ctx,
		shutdown:  budget, cfg: cfg, version: b.Version, build: b, logger: slotLogger(slot),
		byID: map[string]*Machine{}, clients: map[*client]struct{}{}, calls: map[string]chan proto.ToolResult{},
		slot: slot,
	}
	defer func() { h.flushTraffic(time.Now(), true) }()
	h.extLost.started = time.Now() // managers' extensions may call before their managers are back (extensions_proof.go)
	h.teeErrors()                  // the log's error lines become reports while an extension takes them (error_reports.go)
	h.builtinLooks = builtinPersonaFS()
	h.live = newLiveActivities(h)
	h.upd = newUpdater(h)
	h.upd.confirmed = !updating // an update under way is confirmed by this process (its watchdog waits for that)
	h.upd.logSource()
	database, err := db.Open(ctx, cfg.DatabaseURL, db.HubSet)
	if err != nil {
		return err
	}
	defer func() {
		cancel()
		budget.step(h, "node connections", func(context.Context) error {
			h.nodeWorkers.Wait()
			return nil
		})
		if h.stopping.Load() {
			budget.step(h, "hand over the event log", func(ctx context.Context) error { h.saveEventLog(ctx); return nil })
		}
		budget.step(h, "close database", database.CloseContext)
	}()
	h.store = database
	go func() {
		select {
		case <-stop.Done():
			h.beginStop()
			cancel()
		case <-ctx.Done():
		}
	}()
	if err := h.startClientOps(ctx); err != nil {
		return err
	}
	if err := h.initHubID(ctx); err != nil {
		return err
	}
	h.loadEventLog(ctx)
	// The interactions left pending are the manager's to take back (assistant/fleet_ix.go restoreInteractions).
	h.importLegacy(ctx)
	h.loadUserLang(ctx)
	h.loadPhoneTools()
	if err := h.startPhoneWrites(ctx); err != nil {
		return err
	}
	h.push = newPusher(cfg.dir, h.store, h.logf)
	h.push.traffic = h.recordTraffic
	h.push.desktopOnline = h.desktopOnline
	h.push.desktopOpen = h.desktopOpen
	h.push.hubID = h.hubID
	h.push.userLang = h.userLang
	h.push.userLoc = h.userLocation
	h.push.devicesChanged = func() { go h.fleetClientsChanged() } // a phone that takes MCP forms by push (fleet_link.go)
	go h.push.maintenanceLoop(ctx)
	// The machines are the manager's to connect (devices_link.go); until it reports them they show as before a
	// restart (what it handed over), before the assistant's process starts and takes that hand-over.
	h.loadMachineTrust(ctx) // before the manager connects them and the assistant asks (machine_trust.go)
	go h.machineTrustLoop(ctx)
	h.startDevices(ctx)
	h.initMesh(ctx) // before the manager connects: a member leaves its computers to the center (mesh_group.go)
	go h.terminalKeepalive(ctx)
	go h.fullKeepalive(ctx)
	go h.live.run(ctx)
	h.sayMovedSections()
	// The assistant, its own process on the bus (assistant_process.go); it carries the machines, so the hub's stop
	// waits for it as it waited for the node connections.
	h.nodeWorkers.Add(1)
	go func() {
		defer h.nodeWorkers.Done()
		h.superviseAssistant(ctx)
	}()
	h.loadAssistantProfile(ctx) // the user's zone and the main conversation before the assistant reports (assistant_profile.go)
	h.loadExtReport(ctx)        // what the apps saw of the extensions, until the manager reports (extensions_link.go)
	go h.personaTiersLoop(ctx)
	h.upd.start(ctx)
	go h.adminStartup(ctx) // the admin UI's first-run setup code, while it has no password (admin_setupcode.go)
	if err := h.startPublic(ctx); err != nil {
		return err
	}
	h.startMesh(ctx)
	return h.serveHTTP(ctx)
}

func (h *Hub) logf(format string, args ...any) { h.logger.Printf(format, args...) }

func (h *Hub) broadcastMachines() {
	if h.pruneSessionListMachines() {
		h.broadcastSessionListNotice()
	}
	h.broadcast(map[string]any{"type": "machines", "machines": h.machineList()})
	h.meshTouch() // a member's record says which of its computers the center can reach through it
	h.assistantUpdated()
}

// sessionsChanged sends the apps the machine's list as the hub has it (the manager's last, fleet_link.go).
func (h *Hub) sessionsChanged(m *Machine) {
	h.broadcastSessions(m, m.keptSessions())
}

func (h *Hub) machineName(id string) string {
	if m := h.localMachine(id); m != nil {
		return m.name()
	}
	return id
}

// ---- MCP tools ----

func (h *Hub) loadPhoneTools() {
	if h.store == nil {
		return
	}
	tools, err := h.store.PhoneTools(context.Background())
	if err != nil {
		h.logf("load phone tools: %v", err)
		return
	}
	h.mu.Lock()
	h.phoneTools = make(map[string]db.PhoneToolRegistration, len(tools))
	for _, r := range tools {
		h.phoneTools[r.DeviceID] = r
	}
	h.mu.Unlock()
}

// phoneToolsTimeout bounds a phone tool registration's write (the pool has no statement_timeout); tests shorten it.
var phoneToolsTimeout = 5 * time.Second

// setPhoneTools saves a phone's tools, then lists them. The write runs outside h.mu, the hub's global lock: a stalled
// database used to stop every broadcast and client message with it (#5358).
func (h *Hub) setPhoneTools(registration db.PhoneToolRegistration) {
	defer h.wakePhoneWrites()
	h.phoneToolsMu.Lock()
	defer h.phoneToolsMu.Unlock()
	if h.store != nil {
		ctx, cancel := context.WithTimeout(context.Background(), phoneToolsTimeout)
		err := h.store.ReplacePhoneTools(ctx, registration)
		cancel()
		if err != nil {
			h.logf("save phone tools: %v", err)
			return
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Revoked while the rows were written: revoked() already cleared this device under h.mu, so it must not come back.
	if registration.CredentialID != "" && h.isDeviceRevoked(registration.CredentialID) {
		return
	}
	if h.phoneTools == nil {
		h.phoneTools = map[string]db.PhoneToolRegistration{}
	}
	registration.UpdatedAt = time.Now()
	h.phoneTools[registration.DeviceID] = registration
}

// phoneToolList is the phones' tools, one of each name (the longest description wins): what the manager lists
// first for every conversation and agent (app.call phone, fleet_link.go).
func (h *Hub) phoneToolList() []proto.Tool {
	h.mu.Lock()
	ids := make([]string, 0, len(h.phoneTools))
	for id := range h.phoneTools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var tools []proto.Tool
	byName := map[string]int{}
	for _, id := range ids {
		for _, tool := range h.phoneTools[id].Tools {
			if i, ok := byName[tool.Name]; ok {
				if len([]rune(tool.Description)) > len([]rune(tools[i].Description)) ||
					(tool.Description == tools[i].Description && len(tool.InputSchema) > len(tools[i].InputSchema)) {
					tools[i] = tool
				}
			} else {
				byName[tool.Name] = len(tools)
				tools = append(tools, tool)
			}
		}
	}
	h.mu.Unlock()
	if tools == nil {
		tools = []proto.Tool{}
	}
	return h.withPhoneWriteTools(tools)
}

func (h *Hub) callPhone(ctx context.Context, name string, args json.RawMessage) proto.ToolResult {
	if result, handled := h.phoneWriteTool(ctx, name, args); handled {
		return result
	}
	args, queue, err := phoneQueueOption(name, args)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	if queue && h.store != nil {
		lookupCtx, cancel := context.WithTimeout(ctx, phoneToolsTimeout)
		existing, err := h.store.FindQueuedPhoneWrite(lookupCtx, name, args)
		cancel()
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		if existing != nil {
			h.wakePhoneWrites()
			go h.requestPhoneWake(name)
			return phoneWriteResult(existing)
		}
	}
	c := h.phoneClient(name)
	if c == nil && queue {
		return h.enqueuePhoneWrite(ctx, name, args)
	}
	if c == nil && phoneOnScreenTools[name] {
		return proto.TextResult(phoneNotOnScreen, true)
	}
	if c == nil {
		c = h.wakePhone(ctx, name)
	}
	if c == nil {
		return proto.TextResult("The user's iPhone is not connected and did not wake up in the background: ask them to open the Persona app, then retry.", true)
	}
	id := randomHex(8)
	ch := make(chan proto.ToolResult, 1)
	h.mu.Lock()
	h.calls[id] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.calls, id)
		h.mu.Unlock()
	}()
	c.sendJSON(map[string]any{"type": "tool_call", "call_id": id, "name": name, "args": args})
	wait, confirmed := phoneCallWait, phoneConfirmedTools[name]
	if confirmed {
		wait = phoneConfirmWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	select {
	case r := <-ch:
		return r
	case <-ctx.Done():
		if confirmed {
			return proto.TextResult("Timed out waiting for the user to approve this on the phone. The approval may still be "+
				"open there, or the change may already be saved: check first (list the events / reminders / alarms / contacts) "+
				"and do not just call it again.", true)
		}
		return proto.TextResult("Timed out waiting for the iPhone (is the Persona app still open?).", true)
	}
}

// phoneCallWait is how long the hub waits for the phone to answer a call; phoneConfirmWait the same for a write the
// user approves on the phone first (as long as an ask_user question): the phone may be in a pocket, and an approval
// that comes after the hub stopped waiting is still carried out on iOS, so a short wait made the agent ask again
// and save the same thing twice (#4427).
var (
	phoneCallWait    = 2 * time.Minute
	phoneConfirmWait = 30 * time.Minute
)

// phoneConfirmedTools are the phone's tools that wait for the user before they finish (docs/apple/phone-tools.md
// "Confirmation flow"): writes approved on a card, and the ones the user finishes in a system sheet (a message they
// send themselves, #2279).
var phoneConfirmedTools = map[string]bool{
	"phone_calendar_create_event": true,
	"phone_calendar_update_event": true, // #2276
	"phone_calendar_delete_event": true,
	"phone_reminders_create":      true,
	"phone_reminders_complete":    true,
	"phone_reminders_update":      true,
	"phone_reminders_delete":      true,
	"phone_health_write_sample":   true,
	"phone_alarm_create":          true, // #2278
	"phone_timer_start":           true,
	"phone_alarm_cancel":          true,
	"phone_messages_compose":      true, // #2279: the user sends or cancels in the compose sheet
	"phone_call_start":            true, // #2280
	"phone_shortcuts_register":    true, // #2283
	"phone_home_control":          true, // #2285
	"phone_home_run_scene":        true,
	"phone_contacts_create":       true, // #2286
	"phone_contacts_update":       true,
}

// phoneOnScreenTools work only while Persona is the app on the phone's screen (docs/apple/phone-tools.md
// "Foreground restrictions"): iOS gives an app in the background no clipboard, so a silent push would wake Persona for a
// call it can only refuse (#4192). With no phone connected that offers one, the hub answers at once instead, as it
// already does for an Android phone, which stops offering them when Persona leaves the screen (and is never woken by
// push). A phone connected in the background still gets the call and refuses it itself.
var phoneOnScreenTools = map[string]bool{
	"phone_clipboard_get": true,
	"phone_clipboard_set": true,
}

// phoneNotOnScreen is the answer to a phoneOnScreenTools call no connected phone can take.
const phoneNotOnScreen = "Persona is not open on the user's phone, and the phone lets only the app on screen use the " +
	"clipboard: ask the user to open Persona, then retry."

// requestPhoneWake selects a provider and asks iOS to wake it without waiting for a connection.
// It reports whether a provider can be woken, including when a recent request throttles this push.
func (h *Hub) requestPhoneWake(name string) bool {
	if !h.push.ready() {
		return false
	}
	h.mu.Lock()
	var candidates []db.PhoneToolRegistration
	for _, r := range h.phoneTools {
		if r.Platform != "" && r.Platform != "ios" {
			continue
		}
		for _, tool := range r.Tools {
			if tool.Name == name {
				candidates = append(candidates, r)
				break
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].UpdatedAt.Equal(candidates[j].UpdatedAt) {
			return candidates[i].DeviceID < candidates[j].DeviceID
		}
		return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt)
	})
	if h.wokeAt == nil {
		h.wokeAt = map[string]time.Time{}
	}
	wakeIDs := map[string]bool{}
	for _, r := range candidates {
		if time.Since(h.wokeAt[r.DeviceID]) < 30*time.Second {
			break
		}
		h.wokeAt[r.DeviceID] = time.Now()
		wakeIDs[r.DeviceID] = true
		if r.CredentialID != "" {
			wakeIDs[r.CredentialID] = true
		}
		// Old push registrations have no credential binding until the app next registers.
		if r.Platform == "" {
			wakeIDs["legacy"] = true
		}
		break
	}
	h.mu.Unlock()
	if len(candidates) == 0 {
		return false
	}
	if len(wakeIDs) > 0 {
		h.push.wakeDevices("tool", wakeIDs)
	}
	return true
}

// wakePhone asks iOS to wake the app in the background (silent push) and waits briefly for it to connect.
func (h *Hub) wakePhone(ctx context.Context, name string) *client {
	if !h.requestPhoneWake(name) {
		return nil
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		if c := h.phoneClient(name); c != nil {
			h.logf("phone woke up for a tool call in %d ms", time.Since(start).Milliseconds())
			return c
		}
		select {
		case <-ctx.Done():
			h.logf("phone did not wake up for a tool call within 20 s")
			return nil
		case <-t.C:
		}
	}
}

func (h *Hub) toolResult(id string, r proto.ToolResult) {
	h.mu.Lock()
	ch := h.calls[id]
	h.mu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}
