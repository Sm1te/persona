// Package devicewire is what the hub and the manager that carries the user's computers say to each other over the
// bus about them (docs/protocol/protocol.md "Computers (devices.*)", docs/server/machines.md "Who connects to computers"): the manager (internal/devices, in
// `personad assistant`) connects every computer — the local node, ssh, tailcat, Persona Link in both modes, the ones
// reached through another hub of the mesh — and holds what is known of each; the hub never dials or accepts a
// computer. It hands the manager hub.json's machines as defaults, relays the apps' calls and streams to it, and keeps
// what the manager reports to show the apps. Both sides import this package; neither imports the other.
package devicewire

import (
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Manager → hub.
const (
	// EventState {machines: [State], absorbed: {entry id: machine id}, entries: [Entry]}: every machine as the manager
	// has it now, in the order the apps list them; sent whole whenever any of it changed (coalesced), and first thing
	// after every handshake.
	EventState = "devices.state"
	// EventNode {machine_id, event, params}: one of a node's one-way events, as it came, in the order they came.
	EventNode = "devices.event"
	// EventDesktops Desktops: the manager's computers whose Persona Link says the user is at them now, all of them, on
	// every presence the Persona Links say (once a minute while active), after a devices.state when it changed, and in
	// full after each handshake. Apart from devices.state, whose every change goes to the apps as their machine list.
	// Older hubs ignore it.
	EventDesktops = "devices.desktops"
	// MethodRequest {machine_id, method, params, request} → the hub's answer to a node's request the hub answers
	// (interaction.create, assistant.lark, mcp.tools, mcp.call).
	MethodRequest = "devices.request"
	// EventRequestEnd {machine_id, request}: the node connection a devices.request came on ended before the hub
	// answered it; the hub stops on it as it would on a node of its own (a resumable question stays for its hook to
	// claim again through the next connection).
	EventRequestEnd = "devices.request_end"
	// MethodConnected {machine_id, connected_at} → ConnectedReply: a node just connected (or the manager's connection
	// to the hub is new): what the hub adds to the node's configuration and the calls it gets right after (Codex
	// settings, managed launchers, the sends the hub remembers), before the manager asks it for its sessions; the hub
	// starts what it does on a connection (Cua, ego, the plugins' programs) too.
	MethodConnected = "devices.connected"
	// MethodConnectorCred {machine_id} → ConnectorCred: a computer of kind service's pin and credential, kept sealed by
	// the hub; MethodConnectorPaired ConnectorCred → {}: pairing made new ones, the hub seals and keeps them.
	MethodConnectorCred   = "devices.connector_cred"
	MethodConnectorPaired = "devices.connector_paired"
	// MethodRename Rename → RenameReply: a computer's Persona Link asked for another name (persona-link name, the
	// tray, the menu bar); the hub writes it to hub.json and hands the entries over again (EventConfig). Refused with
	// an rpc.Error of a RenameCode*.
	MethodRename = "devices.rename"
)

// Rename is devices.rename's params: Name "" is the computer's id again.
type Rename struct {
	MachineID string `json:"machine_id"`
	Name      string `json:"name"`
}

// RenameReply is the name the hub calls the computer now.
type RenameReply struct {
	Name string `json:"name"`
}

// Why a rename was refused (devices.rename's rpc.Error codes; the link's renamed carries them as they are).
const (
	RenameCodeInvalidName = "invalid_name" // too long, or not one line
	RenameCodePending     = "pending"      // the computer waits for the user to confirm it
)

// Hub → manager.
const (
	// MethodCall {machine_id, method, params, timeout_ms?} → the node's result: the apps' and the hub's calls to a
	// machine's node. session.watch / session.unwatch also tell the manager what to watch again after a reconnect.
	MethodCall = "devices.call"
	// EventNotify {machine_id, event, params}: a one-way event to a node (terminal input, a port's bytes).
	EventNotify = "devices.notify"
	// EventConfig Config: what the hub hands the manager changed (an entry added on the admin page, the mesh, the
	// hub's addresses).
	EventConfig = "devices.config"
	// MethodAccept Accept → {}: a connection for the manager came to the hub (a Persona Link, a stream through this
	// hub for the center); the manager takes it by dialing RelayPath?id=.
	MethodAccept = "devices.accept"
	// MethodUpdateNodes {} → {}: the hub updated itself; redeploy the machines whose node is not its build.
	MethodUpdateNodes = "devices.update_nodes"
	// MethodPairConnector {link, machine_id, address, proxy} → {address, version}: pair a computer of kind service
	// (the admin page).
	MethodPairConnector = "devices.pair_connector"
	// MethodReconnect {machine_id} → {}: connect the machine's node again (the apps' reconnect, the admin page).
	MethodReconnect = "devices.reconnect"
	// MethodUsageGatewayAccounts {} → proto.GatewayAccounts: the account lists of the account-pool gateways among the
	// usage sources, from their last read (never a key; docs/server/usage-windows.md "The gateway's account list"). The extensions'
	// usage.gateway_accounts (internal/extmgr, permission usage_gateways) asks it in the manager's process.
	MethodUsageGatewayAccounts = "devices.usage_gateway_accounts"
	// EventLinkRTT LinkRTT: a Persona Link's round trip, as the hub's heartbeat on it measured it (its paths are
	// compared by it).
	EventLinkRTT = "devices.link_rtt"
	// MethodMachinesPut MachinePut → {}, MethodMachinesRemove MachineRef → MachineRemoved, MethodMachinesGet
	// MachineRef → MachineEntry: the hub's edits of a manager's own list of computers, and an entry as the manager
	// has it (its secrets too: a move hands it to the new manager), for a manager the hub handed MachinesOwn
	// (assistantwire.CapabilityMachinesOwn; docs/assistant/managers.md "Each manager's computers"). Another manager
	// refuses them CodeNotOwnList; get on a machine it does not have: CodeNoDevice.
	MethodMachinesPut    = "devices.machines.put"
	MethodMachinesRemove = "devices.machines.remove"
	MethodMachinesGet    = "devices.machines.get"
	// MethodEngineGet MachineRef → MachineEngine, MethodEnginePut MachineEngine → {}: a machine's engine choice as the
	// manager keeps it in its own settings (Choice nil: never chosen). A move reads it from the manager the machine
	// leaves and puts it in the one it goes to, so a change of owner does not change the engine (#3611).
	MethodEngineGet = "devices.engine.get"
	MethodEnginePut = "devices.engine.put"
)

// MachineEngine is one machine's engine choice (devices.engine.get's answer, devices.engine.put's params).
type MachineEngine struct {
	MachineID string              `json:"machine_id"`
	Choice    *proto.EngineChoice `json:"choice,omitempty"`
}

// CodeNotOwnList: the manager does not keep its own list of computers (the hub did not hand it MachinesOwn, or its
// list was not taken over as its own): the hub edits hub.json for it instead.
const CodeNotOwnList = "not_own_list"

// MachinePut is devices.machines.put's params: an entry, added, or put in place of the one with its id.
type MachinePut struct {
	Entry json.RawMessage `json:"entry"`
}

// MachineRef names one machine.
type MachineRef struct {
	MachineID string `json:"machine_id"`
}

// MachineRemoved is devices.machines.remove's answer: whether the list had it.
type MachineRemoved struct {
	Removed bool `json:"removed"`
}

// MachineEntry is devices.machines.get's answer.
type MachineEntry struct {
	Entry json.RawMessage `json:"entry"`
}

// LinkRTT is devices.link_rtt's params: the relay's id (devices.accept's) and the round trip.
type LinkRTT struct {
	ID    string `json:"id"`
	RTTMS int    `json:"rtt_ms"`
}

// RelayPath is the second channel of a manager's bus token: a WebSocket that the hub joins to a connection it holds
// for the manager (?id=, after devices.accept) or that it opens for it (?hub=&machine=: a node stream through that
// hub of the mesh). Binary messages carry a byte stream, text ones a Persona Link's messages, as they are; the close
// code and reason go through both ways.
const RelayPath = "/api/v1/bus/relay"

// Accept kinds.
const (
	AcceptLink       = "link"        // a Persona Link's control connection (GET /api/v1/link)
	AcceptLinkStream = "link_stream" // a stream it opened (GET /api/v1/link/stream?id=)
	AcceptNode       = "node"        // the center asks for a stream to one of this hub's computers (mesh)
)

// Accept is devices.accept's params.
type Accept struct {
	ID        string `json:"id"` // the relay's
	Kind      string `json:"kind"`
	MachineID string `json:"machine_id"`
	TokenID   string `json:"token_id,omitempty"` // link: the credential it came with
	StreamID  string `json:"stream_id,omitempty"`
	Source    string `json:"source,omitempty"` // link: where it came from (masked), for the log
}

// Config is what the hub hands the manager: the defaults of what the manager carries, as the hub's hello has them
// (manager.hello's answer, field devices) and devices.config again whenever they change.
type Config struct {
	ShareRoutingHandle []byte                    `json:"share_routing_handle,omitempty"`
	SharePublicOrigin  string                    `json:"share_public_origin,omitempty"`
	ShareAuthorities   map[string]ShareAuthority `json:"share_authorities,omitempty"`
	// AutoUpdateNodes is an explicit opt-in; older hubs omit it and leave nodes unchanged.
	AutoUpdateNodes bool `json:"auto_update_nodes,omitempty"`
	// Machines are hub.json's machines (and those the admin page added since), each the JSON hub.json has.
	Machines []json.RawMessage `json:"machines"`
	// MachinesOwn: the manager's own list (its manager.json) is the list, and the hub relays its edits
	// (devices.machines.*). Machines is then taken over only the first time, as the manager's own; once its report says
	// it holds that list (States.OwnList), the hub hands none (null).
	MachinesOwn bool `json:"machines_own,omitempty"`
	// HubID, HubName, HubVersion: the hub's; a node of another build than HubVersion is flagged (and redeployed).
	HubID      string `json:"hub_id"`
	HubName    string `json:"hub_name"`
	HubVersion string `json:"hub_version"`
	// Endpoints are the hub's https addresses and MeshHubs how many hubs its mesh has: what Persona Link is told.
	Endpoints []LinkEndpoint `json:"endpoints,omitempty"`
	MeshHubs  int            `json:"mesh_hubs,omitempty"`
	// WebURL is where people open the web app: hub.json's public_url when it is https, its sub-path included (no
	// trailing slash); "" without one. Persona Link's "Needs you" summary carries it (link.Attention.WebURL).
	WebURL string `json:"web_url,omitempty"`
	// TailcatDERPMapURL is hub.json's tailcat.derp_map_url.
	TailcatDERPMapURL string `json:"tailcat_derp_map_url,omitempty"`
	// NodePackagesURL is where the manager fetches the node builds the hub has none of (docs/operations/node-update.md "Node
	// packages"): a release's dl/ (hub.json's node_packages.url, else the hub build's update URL; "" none), through
	// NodePackagesProxy (an http:// proxy, internal/netproxy; "" direct).
	NodePackagesURL   string `json:"node_packages_url,omitempty"`
	NodePackagesProxy string `json:"node_packages_proxy,omitempty"`
	// LinkPingMS / LinkPingTimeoutMS: the heartbeats of the links and streams, hub.json's link section (0: the
	// protocol's, link.PingEvery / PingTimeout; tests shorten them). The manager tells Persona Link them too.
	LinkPingMS        int `json:"link_ping_ms,omitempty"`
	LinkPingTimeoutMS int `json:"link_ping_timeout_ms,omitempty"`
	// Mesh is the group this hub is in (nil: none).
	Mesh *Mesh `json:"mesh,omitempty"`
	// Pending are the machines waiting for the user to confirm them (machine_trust), sorted: their Persona Link is
	// told so (link.Message.Pending).
	Pending []string `json:"pending,omitempty"`
	// PendingUnknown: the hub could not read which machines wait (its database did not answer); until a handover
	// without it, the manager treats every machine as not confirmed yet.
	PendingUnknown bool `json:"pending_unknown,omitempty"`
}

// LinkEndpoint is one address Persona Link may use (internal/link.Endpoint).
type LinkEndpoint struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

// Mesh is what the manager needs of the mesh group (docs/server/mesh.md "Multiple paths to one computer"): whether this hub leaves its computers to
// the center, and the members' computers the center reaches through them.
type Mesh struct {
	// Member: this hub is a member; its computers are connected by the center (through it), not from here.
	Member     bool   `json:"member,omitempty"`
	CenterName string `json:"center_name,omitempty"`
	// SettleUntil: just started with peers, the group may have another center by now; connect nothing before.
	SettleUntil time.Time `json:"settle_until,omitempty"`
	// Hubs are the other hubs as this one sees them.
	Hubs map[string]MeshHub `json:"hubs,omitempty"`
	// Members are the records of the hubs that take this one for their center: their computers.
	Members []MemberRecord `json:"members,omitempty"`
	// PathSwitchMS: how long a better path must stay better before the switch.
	PathSwitchMS int64 `json:"path_switch_ms,omitempty"`
}

// MeshHub is another hub of the group.
type MeshHub struct {
	Name      string `json:"name"`
	Reachable bool   `json:"reachable"`
	RTTMS     int    `json:"rtt_ms,omitempty"` // -1 unknown
}

// MemberRecord is a member's computers.
type MemberRecord struct {
	HubID    string       `json:"hub_id"`
	Machines []RecMachine `json:"machines"`
}

// RecMachine is a computer in a hub's hub.json as that hub's record has it (and, for this hub's own, as the manager
// says the record should have it: Entry).
type RecMachine struct {
	ID    string          `json:"id"`
	Cfg   json.RawMessage `json:"cfg"`
	Kind  string          `json:"kind"`
	Avail bool            `json:"avail,omitempty"`
	RTTMS int             `json:"rtt_ms,omitempty"`
}

// State is one machine as the manager has it.
type State struct {
	// Machine is what the apps get, but for what the hub adds (system, computer, browser).
	Machine proto.Machine `json:"machine"`
	// Config is the machine's entry: hub.json's, or a member's (a derived machine), as the hub gave it.
	Config json.RawMessage `json:"config"`
	Info   proto.NodeInfo  `json:"info"`
	// Connected: its node is connected, a call would go out now (Machine.Online counts the warm start too: what the
	// apps see); ConnectedAt: when the node's connection began (a new one is a reconnect).
	Connected   bool      `json:"connected,omitempty"`
	ConnectedAt time.Time `json:"connected_at,omitempty"`
	// WarmUntil: shown as before the manager's restart until then, while its node reconnects.
	WarmUntil time.Time `json:"warm_until,omitempty"`
	Derived   bool      `json:"derived,omitempty"`
	// Paths are its ways, for the admin page.
	Paths []PathInfo `json:"paths,omitempty"`
}

// Desktops is EventDesktops' params.
type Desktops struct {
	Desktops []Desktop `json:"desktops"`
}

// Desktop is a computer whose Persona Link says the user is at it (docs/operations/link.md "Presence"); the hub counts
// it as a desktop for "Phone Muted" for TTLMS from when it takes this, on its own clock (at most DesktopTTLMax), while
// Notifies holds.
type Desktop struct {
	MachineID string `json:"machine_id"`
	// Since: when this run of presence began, on the manager's clock: only for the apps to show.
	Since    time.Time `json:"since"`
	TTLMS    int64     `json:"ttl_ms"`
	Notifies bool      `json:"notifies"`
}

// DesktopTTLMax caps a desktop's TTL, twice Persona Link's presence TTL (link.PresenceTTL): however a manager counts,
// no presence keeps phones quiet longer without being said again.
const DesktopTTLMax = 4 * time.Minute

// PathInfo is a path as the admin page and the machine list show it.
type PathInfo struct {
	Via    string `json:"via"`
	HubID  string `json:"hub_id"`
	ID     string `json:"id,omitempty"`
	OK     bool   `json:"ok"`
	RTTMS  int    `json:"rtt_ms,omitempty"`
	Active bool   `json:"active,omitempty"`
	Error  string `json:"error,omitempty"`
}

// States is devices.state's params.
type States struct {
	Machines []State `json:"machines"`
	// Absorbed: a hub.json entry whose computer another entry has → that machine's id (the id still reaches it).
	Absorbed map[string]string `json:"absorbed,omitempty"`
	// Entries are the manager's own entries, every one it was handed (an absorbed one too): each one's entry as it
	// has it (Cfg; a manager from before leaves it out) and how it reaches it. The hub's mesh record lists them, and
	// the hub keeps what each manager has from them (hub machine_entries.go).
	Entries []RecMachine `json:"entries,omitempty"`
	// OwnList: the manager keeps Entries as its own list in its manager.json (it was handed MachinesOwn and holds the
	// list as its own): the hub may take them out of hub.json.
	OwnList bool `json:"own_list,omitempty"`
}

// NodeEvent is devices.event's and devices.notify's params.
type NodeEvent struct {
	MachineID string          `json:"machine_id"`
	Event     string          `json:"event"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// NodeCall is devices.call's and devices.request's params.
type NodeCall struct {
	Caller     *Caller         `json:"caller,omitempty"`
	Submission *Submission     `json:"submission,omitempty"`
	Authority  *ShareAuthority `json:"share_authority,omitempty"`
	MachineID  string          `json:"machine_id"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params,omitempty"`
	// TimeoutMS bounds the call on the manager's side too, a little before the caller gives up: the manager's
	// CodeDeadline answer comes back in time.
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
	// Request is devices.request's key, the manager's (EventRequestEnd names it).
	Request string `json:"request,omitempty"`
	// Fallback (a connection's calls only): sent instead when the node answers unknown method, a node from before
	// Method (launchers.catalog falls back to launchers.managed).
	Fallback *NodeCall `json:"fallback,omitempty"`
}

// RequestEnd is devices.request_end's params.
type RequestEnd struct {
	MachineID string `json:"machine_id"`
	Request   string `json:"request"`
}

// Connected is devices.connected's params.
type Connected struct {
	MachineID   string    `json:"machine_id"`
	ConnectedAt time.Time `json:"connected_at"`
}

// ConnectedReply is devices.connected's answer.
type ConnectedReply struct {
	// Configure is added to the node.configure the manager sends (key → value).
	Configure map[string]json.RawMessage `json:"configure,omitempty"`
	// Calls go to the node right after it, in order.
	Calls []NodeCall `json:"calls,omitempty"`
}

// ConnectorCred is a computer of kind service's pin and credential (the token in the clear: the bus is the manager's).
type ConnectorCred struct {
	MachineID string `json:"machine_id"`
	FP        string `json:"fp"`
	Token     string `json:"token"`
	Version   string `json:"version,omitempty"`
}

// PairConnector is devices.pair_connector's params.
type PairConnector struct {
	Link      string `json:"link"`
	MachineID string `json:"machine_id"`
	Address   string `json:"address,omitempty"`
	Proxy     string `json:"proxy,omitempty"`
}

// Routes are the apps' routes on machines the manager answers itself (the hub relays them as http.request, as the
// assistant's): its own settings of each machine and what only it can do.
var Routes = []string{
	"POST /api/v1/machines/{mid}/reconnect",
	"PUT /api/v1/machines/{mid}/engine",
	"GET /api/v1/machines/{mid}/herdr",
	"PUT /api/v1/machines/{mid}/herdr",
	"POST /api/v1/machines/{mid}/herdr/start",
	"GET /api/v1/machines/{mid}/botmux",
	"PUT /api/v1/machines/{mid}/botmux",
	"GET /api/v1/machines/{mid}/health/reports",
	"DELETE /api/v1/machines/{mid}/health/reports/{name}",
	"POST /api/v1/machines/{mid}/deploy",
	"GET /api/v1/machines/{mid}/claude-dirs",
	"PUT /api/v1/machines/{mid}/claude-dirs/blocked",
	"GET /api/v1/machines/{mid}/claude-live-replies",
	"PUT /api/v1/machines/{mid}/claude-live-replies",
	// Sharing the machine's clipboard with the phone (#3064, internal/devices clipboard.go).
	"GET /api/v1/machines/{mid}/clipboard",
	"PUT /api/v1/machines/{mid}/clipboard",
	"GET /api/v1/machines/{mid}/clipboard/settings",
	"PUT /api/v1/machines/{mid}/clipboard/settings",
	// The file transfer switch (#3065, internal/devices transfer_settings.go).
	"GET /api/v1/machines/{mid}/file-transfer/settings",
	"PUT /api/v1/machines/{mid}/file-transfer/settings",
}

// TransferRoutes are computer to computer file transfers (internal/devices transfer.go, docs/protocol/file-transfer.md),
// relayed the same way: a POST to the manager of its source computer (from.machine in the body), GET and DELETE to the
// one the request names (?manager=, as the transfer's events say), else the default.
var TransferRoutes = []string{
	"POST /api/v1/files/transfer",
	"GET /api/v1/files/transfer/{id}",
	"DELETE /api/v1/files/transfer/{id}",
}

// MetricsRoutes are the machines' monitoring's routes (internal/devices metrics.go), relayed the same way: every
// machine's latest sample and its last hour, one machine's history, the token usage (all, and one machine's range).
var MetricsRoutes = []string{
	"GET /api/v1/metrics",
	"GET /api/v1/machines/{id}/metrics",
	"GET /api/v1/machines/{id}/usage",
	"GET /api/v1/usage",
}

// UsageRoutes are the subscription usage windows' routes (internal/devices usagewindows.go, docs/server/usage-windows.md),
// relayed the same way: the manager asks the machines it connects and the account-pool gateways, and merges them.
var UsageRoutes = []string{
	"GET /api/v1/usage/windows",
}

// AdminUsageRoutes are the admin UI's usage sources page (internal/devices admin_usage.go): the hub relays them from
// its admin routes only (a signed-in admin session, assistantwire.AdminHeader), never from the apps' API.
var AdminUsageRoutes = []string{
	"GET /api/v1/admin/usage-sources",
	"PUT /api/v1/admin/usage-sources",
	"POST /api/v1/admin/usage-sources",
	"PUT /api/v1/admin/usage-sources/{id}",
	"DELETE /api/v1/admin/usage-sources/{id}",
	"POST /api/v1/admin/usage-sources/test",
}

// Error codes that cross the bus (the node's own coded errors go through as they are).
const (
	CodeNoDevice       = "no_device"       // the manager has no such machine
	CodeMachineOffline = "machine_offline" // its node is not connected
	CodeNodeClosed     = "node_closed"     // the connection to its node dropped during the call
	CodeDeadline       = "deadline"        // the call's time ran out with the node on it (it may have done it)
	// CodeHandoffRefused: secret.stage, or a session.start with a secret_ticket, asked by the hub or another manager:
	// only the manager that connects the computer makes a hand-off, after its own card (docs/extensions/secret-providers.md).
	CodeHandoffRefused = "handoff_refused"
)
