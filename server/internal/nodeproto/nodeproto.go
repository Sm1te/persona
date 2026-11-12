// Package nodeproto is the node protocol's handshake contract (manager ↔ device, docs/protocol/protocol.md "Node handshake"):
// the protocol version and the capability names a node announces in node.info, and the version a manager speaks.
// It holds only constants and name lists and imports nothing, so the manager, the node and any other implementation
// can share it without depending on each other.
//
// Compatibility is decided by these, not by build strings: a node and a manager built from different commits work
// together as long as their protocol ranges overlap and the node has the capabilities the manager requires. A build
// difference is only an upgrade hint.
package nodeproto

// PersonaContracts is the optional bidirectional naming capability. It does not
// change the node protocol floor or become a required capability.
const PersonaContracts = "persona_contracts_v1"

// ManagerUpdateInventory is the optional read-only local update.inventory
// contract. A legacy peer remains compatible, but cannot produce idle evidence.
const ManagerUpdateInventory = "manager_update_inventory_v1"

// Version is the node protocol this code speaks. Raise it only for a change an older peer cannot follow (a method
// whose params or results changed meaning); additions go in as capabilities instead.
//
// Protocol 0 is the implicit version of nodes from before the handshake: they leave protocol and capabilities out.
const Version = 1

// MinVersion is the oldest announcing node protocol a manager built from this code still drives. Nodes from before
// the handshake (protocol 0) are not judged by it: the manager keeps working with them as before.
const MinVersion = 1

// MinManagerVersion is what a node built from this code announces as protocol_min: the oldest manager protocol it
// still answers.
const MinManagerVersion = 1

// Capabilities a node announces (stable names: never renamed or reused; a node drops one only with a protocol bump).
const (
	// Sessions: sessions.list / session.watch / session.unwatch / session.start / session.send and the session events.
	Sessions = "sessions"
	// ModelConfiguration: assistant.send / assistant.warm / session.start / session.send take a launcher's model and
	// model_fingerprint (docs/assistant/model-config.md). Older nodes said so with node.info's model_configuration.
	ModelConfiguration = "model_configuration"
	// AssistantConfigDir: assistant.send / assistant.warm / session.send honor an explicit
	// config_dir and preserve the selected Claude profile when resuming a conversation.
	AssistantConfigDir = "assistant_config_dir"
	// NodeUpdate: the node.update.* methods (docs/operations/node-update.md). Whether an update can run right now is still
	// node.info's node_update / node_update_reason.
	NodeUpdate = "node_update"
	// Engines: engine.use and node.info's engine / engines (docs/integrations/engines.md).
	Engines = "engines"
	// MachineUID: node.info's machine_uid (docs/server/mesh.md).
	MachineUID = "machine_uid"
	// HerdrServer: herdr.server.status / herdr.server.start and node.info's herdr_running / herdr_running_known.
	HerdrServer = "herdr_server"
	// PluginNodeCall: plugin.node.call for the programs in node.info's builtin_programs.
	PluginNodeCall = "plugin_node_call"
	// Ultracode: session.start takes ultracode and session.ultracode switches it (docs/protocol/protocol.md "Claude Code
	// ultracode"). A node without it would start a session and drop the field without a word.
	Ultracode = "ultracode"
	// HealthSpaces: file.upload.begin (dest=health) and health.reports.list / health.reports.delete take health_dir,
	// another manager's health space (health-<id>, docs/assistant/managers.md "Directories on a computer"). A node without it
	// would use the built-in manager's.
	HealthSpaces = "health_spaces"
	// Preview: preview.servers / preview.start / preview.open, dev server previews in a headless browser on the
	// computer (docs/web/preview.md).
	Preview = "preview"
	// SecretRun: secret.run, commands the user approved on the password card run with the password they typed on
	// their stdin (docs/assistant/assistant.md "Password card"). A node on Windows announces it and refuses the run.
	SecretRun = "secret_run"
	// SecretEnv: secret.stage and session.start's secret_ticket, values the user approved on the hand-off card put
	// in one Agent start's environment (docs/extensions/secret-providers.md "The hand-off to one Agent run").
	SecretEnv = "secret_env"
	// Clipboard: clipboard.get / clipboard.set and node.info's clipboard (docs/operations/clipboard.md).
	Clipboard = "clipboard"
	// LauncherSwitch: session.modes takes launcher, a switch of a running Claude Code Agent to another launcher that
	// its next session.send carries out (docs/integrations/claude-integration.md "Switching a running Agent to another
	// provider"). A node without it would drop launcher and apply the model on the launcher the Agent runs on.
	LauncherSwitch = "launcher_switch"
	// ModelHTTP: model.http, a direct-API model request an API connection sends through this computer
	// (docs/assistant/model-config.md "Sending through a computer").
	ModelHTTP = "model_http"
)

// Error codes in the manager's answers to a resident node's replayed notifications (a notify with spool_id,
// docs/protocol/protocol.md "When the manager is away"). Both mean "not now, through no fault of the notification": the node
// keeps it without counting a try, and only its age limits it.
const (
	// ErrSpoolBusy: the manager is still handing an earlier try of it to the hub.
	ErrSpoolBusy = "spool_busy"
	// ErrHubAway: the manager has no connection to the hub right now.
	ErrHubAway = "hub_away"
)

// Required are the capabilities a manager built from this code cannot drive a node without.
var Required = []string{Sessions}

// The programs a node can run as (node.info's program): personad, everything (it may run a hub or a manager too), or
// persona-node, the device's own (docs/operations/link.md "The device's program"). A node from before program was reported leaves
// it out: it is personad.
const (
	ProgramPersonad    = "personad"
	ProgramPersonaNode = "persona-node"
)

// Announced is what a node built from this code announces.
var Announced = []string{ShareProjection, CreationReceipt, PersonaContracts, ManagerUpdateInventory, Sessions, ModelConfiguration, AssistantConfigDir, NodeUpdate, Engines, MachineUID, HerdrServer, PluginNodeCall, Ultracode, HealthSpaces, Preview, SecretRun, Clipboard, SecretEnv, LauncherSwitch, ModelHTTP}

// ShareProjection is the bounded complete-source sharing projection, not light Items.
const ShareProjection = "share_projection.v1"

// CreationReceipt preserves opaque creation IDs on every ordinary new-start path.
const CreationReceipt = "creation_receipt.v1"
