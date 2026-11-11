// Package extwire is what the hub and the manager say to each other about the extensions, the connectors and the
// bots (docs/extensions/extensions.md "Who owns what"): the manager (internal/extmgr, in `personad assistant`) has all of them — the
// registry with its secrets, the programs, the connectors and their credentials, mail, the Feishu and WeChat bots —
// and answers their routes; the hub relays the apps' requests to it as they are and keeps the last of what the
// manager reports for the apps (features, the extensions' parts of the API). Both import this package, neither
// imports the other.
package extwire

import (
	"encoding/json"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Routes are the routes the manager answers (net/http patterns): the hub relays each request on them to the manager
// over a relay stream (RelayKind), so a response streams (SSE, a WebSocket) as it did. The manager registers these
// and no others (extmgr.TestRoutesAreTheWireOnes).
var Routes = []string{
	"/api/v1/extensions",
	"/api/v1/extensions/",
	"/api/v1/admin/extensions",
	"/api/v1/admin/extensions/",
	"/api/v1/plugins",
	"/api/v1/plugins/",
	"/api/v1/connectors",
	"/api/v1/connectors/",
	"/api/v1/bots/",
	"/api/v1/channels/{id}/bind",
	"/api/v1/mail/",
	"/api/v1/imessage",
	"/api/v1/imessage/",
	"/api/v1/agents",
	"/api/v1/agents/",
	"/api/v1/messaging",
	"/api/v1/messaging/",
	"/api/v1/devices/{id}/widgets",
	"/api/v1/machines/{mid}/computer",
	"/api/v1/machines/{mid}/computer/",
	"/api/v1/machines/{mid}/browser",
	"/api/v1/machines/{mid}/browser/",
	"/api/v1/machines/{mid}/iphone",
	"/api/v1/machines/{mid}/iphone/",
	"/api/v1/iphone",
	"/api/v1/iphone/",
	"/api/v1/error-reports",
	"/api/v1/subscriptions",
	"/api/v1/subscriptions/",
}

// KeptRoutes are GET routes whose last answer the hub keeps and gives while the manager is away, so the apps keep
// drawing what the extensions put in them (their UI, themes, widgets, shortcuts, prompts) until it is back.
var KeptRoutes = []string{
	"/api/v1/extensions",
	"/api/v1/extensions/-/ui",
	"/api/v1/extensions/-/themes",
	"/api/v1/extensions/-/prompts",
	"/api/v1/extensions/-/widgets",
	"/api/v1/extensions/-/shortcuts",
	"/api/v1/extensions/-/triage-templates",
}

// Headers the hub sets on a relayed request: who asked, as its auth found it. Any the client sent are dropped.
const (
	HeaderHub        = "X-Persona-Hub"
	HeaderDevice     = "X-Persona-Device"      // the device token's id ("" for the master token)
	HeaderDeviceName = "X-Persona-Device-Name" // its name
	HeaderRole       = "X-Persona-Role"        // full | answer | extension | …
	HeaderPaired     = "X-Persona-Paired"      // "1": a pairing token, not one of the user's own signed-in apps
	HeaderExtension  = "X-Persona-Extension"   // an extension token's extension
	HeaderScopes     = "X-Persona-Scopes"      // its scopes, comma-separated
	HeaderOwner      = "X-Persona-Owner"       // "1": the master token over loopback (installing runs code here)
	HeaderAdmin      = "X-Persona-Admin"       // "1": a signed-in admin session (the admin UI's routes)
	HeaderRemote     = "X-Persona-Remote"      // the caller's address
	HeaderLang       = "X-Persona-Lang"        // the language the hub would answer it in
)

// Hub → manager.
const (
	// MethodAuthorize: Authorize → Authorized. An extension's token on one of the hub's own routes: the manager made
	// the token and says whose it is and whether its scopes open the route.
	MethodAuthorize = "extensions.authorize"
	// EventEmit: Emit, one-way: something the hub saw that the extensions subscribe to (agent.finished, agent.needs_you).
	EventEmit = "extensions.emit"
	// EventErrorReports: ErrorReports, one-way: the hub's own errors (its log's error lines, panics it recovered),
	// sent only while the report's features have error_reports; the manager scrubs them like the apps' and gives
	// them to the extensions (docs/extensions/extensions.md "Error reports").
	EventErrorReports = "extensions.error_reports"
	// MethodHubCall: HubCall → the answer: what the hub's own features ask of the manager's (Hub*).
	MethodHubCall = "extensions.hub_call"
)

// What the hub's own features ask of the manager (HubCall.Method).
const (
	// HubGitHubLogin: {} → {login}: the GitHub connector's signed-in login (the persona tiers).
	HubGitHubLogin = "github_login"
)

// Manager → hub.
const (
	// EventReport: Report, one-way: what the hub shows of the extensions while it relays (and after, while the
	// manager is away). Whole each time.
	EventReport = "extensions.report"
	// EventLooksGone: LooksGone, one-way: an extension with looks was removed (after the report without them): a
	// chosen look of its that no one gives now goes back to the default.
	EventLooksGone = "extensions.looks_gone"
	// MethodAppCall is app.call (assistantwire.AppCall) with these apps.
)

// The hub's apps the manager calls (assistantwire.AppCall.AppID / Method).
const (
	AppHub = "hub"
	// HubPushBackground: {payload, devices} → {}: a silent push to those devices (an extension's widget).
	HubPushBackground = "push.background"
)

// Authorize is MethodAuthorize's params: the token's hash, and the route the request is for (its pattern on the
// hub's mux, its method and its escaped path: the one the hub relays, so the check is made on the route the request
// reaches).
type Authorize struct {
	Hash    string `json:"hash"`
	Pattern string `json:"pattern"`
	Method  string `json:"method"`
	Path    string `json:"path"`
}

// Authorized is MethodAuthorize's answer. OK false with Extension set: a live token without the scope (Scope says
// which); Extension "": no such token.
type Authorized struct {
	OK        bool     `json:"ok"`
	Extension string   `json:"extension,omitempty"`
	Name      string   `json:"name,omitempty"`
	Scopes    []string `json:"scopes,omitempty"`
	Scope     string   `json:"scope,omitempty"`
}

// Emit is EventEmit's params.
type Emit struct {
	Event  string          `json:"event"`
	Params json.RawMessage `json:"params"`
}

// ErrorReports is EventErrorReports' params.
type ErrorReports struct {
	Reports []wire.ErrorReport `json:"reports"`
}

// HubCall is MethodHubCall's params.
type HubCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Report is EventReport's params.
type Report struct {
	// Features are the extensions' features the apps see beside the hub's (hello, health).
	Features []string `json:"features,omitempty"`
	// APIs are the parts of the app's API under /api/v1/<prefix>/ the running extensions serve.
	APIs []APIPrefix `json:"apis,omitempty"`
	// Machines are what the hub shows on each machine of the built-in extensions (cua, ego).
	Machines map[string]MachineExtras `json:"machines,omitempty"`
	// ScreenKeepAwake: a Mac whose screen the app watches stays awake meanwhile (the plugin cua's setting).
	ScreenKeepAwake bool `json:"screen_keep_awake,omitempty"`
	// Looks are the looks of the extensions that are on (docs/extensions/extensions.md "Avatars"), with their files: the hub serves
	// them as persona packages beside its own.
	Looks []LookPackage `json:"looks,omitempty"`
	// BotIdentities identify enabled bots for cross-manager conflict warnings. Never include credentials.
	BotIdentities []BotIdentity `json:"bot_identities,omitempty"`
	// EventsDeclared: APIs carry the types each extension declares for api.broadcast (APIPrefix.Events), and the hub
	// passes the manager's messages of those types only. An older manager's report has none: the hub cannot tell its
	// extensions' types, and passes any type but its own from it.
	EventsDeclared bool `json:"events_declared,omitempty"`
}

// BotIdentity is a platform's stable identifier, not a display name or an authentication token.
type BotIdentity struct {
	Kind string `json:"kind"` // feishu or wechat
	ID   string `json:"id"`   // Feishu app ID or the bound WeChat user's ID
}

// LookPackage is one extension's look: the manifest's item and the files it names (SVGs cleaned).
type LookPackage struct {
	Extension     string            `json:"extension"`
	ExtensionName string            `json:"extension_name"`
	Version       string            `json:"version"`
	Item          json.RawMessage   `json:"item"`
	Files         map[string][]byte `json:"files"`
}

// APIPrefix is one extension's part of the API. Receipts: the sends under it the hub keeps a receipt for, as for its
// own (GET /api/v1/ops/{id}).
type APIPrefix struct {
	Prefix   string       `json:"prefix"`
	Receipts []APIReceipt `json:"receipts,omitempty"`
	// Events are the types its api.broadcast sends the apps: the hub passes on a manager's message of such a type
	// (besides the managers' own types) only from the manager whose extension declares it.
	Events []string `json:"events,omitempty"`
}

// MachineExtras is one machine's computer (Cua), browser (ego) and paired iPhones' (iphone) state, nil when not on.
type MachineExtras struct {
	Computer json.RawMessage `json:"computer,omitempty"`
	Browser  json.RawMessage `json:"browser,omitempty"`
	IPhone   json.RawMessage `json:"iphone,omitempty"`
	// ScreenPort: the computer's screen may be opened (the plugin cua or ego is on for it).
	ScreenPort bool `json:"screen_port,omitempty"`
}

// Setting is one of the manager's persona_settings entries (HubSettingsList).
type Setting struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	About   string   `json:"about"`
	Value   string   `json:"value,omitempty"`
	Actions []string `json:"actions"`
}

// More of what the hub asks of the manager (HubCall.Method).
const (
	// HubSettingsList: {} → {settings: [Setting]}: the manager's persona_settings entries (mail, sources' rules).
	HubSettingsList = "settings.list"
	// HubSettingsTool: {machine_id, session_id, arguments} → {text}: persona_settings on one of them (the card is the
	// hub's: the manager asks HubWaitInteraction).
	HubSettingsTool = "settings.tool"
	// HubKBArchives, … see the routes.
	// HubAudit: Audit → {}: a request of an extension's token the hub refused, for that extension's audit in its manager
	// (plugin_audit). The manager takes the actions below only.
	HubAudit = "audit"
)

// What the hub records with HubAudit (Audit.Action).
const (
	// AuditPermissionModeRefused: {machine_id, session_id, reason}: a permission mode change it may not make (not a
	// conversation it opened, without agents.permission_mode, or with more than the permission mode).
	AuditPermissionModeRefused = "permission_mode_refused"
	// AuditTerminalControlRefused: {route, path}: a request with terminal control characters, which an extension may
	// not send (hub's terminal_controls.go).
	AuditTerminalControlRefused = "terminal_control_refused"
	// AuditSecretHandoffRefused: {interaction_id, machine_id, session_id, role, reason, device_name?}: an approval of a
	// hand-off card from a device or client that may not give one (hub's secret_handoff.go). Extension is a provider
	// the card hands a value from and Actor who tried ("device:<id>", "extension:<id>", "hub token"); the card's
	// variables, references and values are not in it.
	AuditSecretHandoffRefused = "secret_handoff_approve_refused"
)

// Audit is HubAudit's params: what extension Extension's token was refused, or (AuditSecretHandoffRefused) whose
// approval of a card handing over a value of provider Extension was. Detail holds ids and reasons only.
type Audit struct {
	Extension string         `json:"extension"`
	Action    string         `json:"action"`
	Actor     string         `json:"actor,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// More of the hub's apps the manager calls.
const (
	// HubPushLocalized: {payloads: {lang: payload}} → {}: a push to every device in its language. A device whose language
	// has no payload gets the fallback's (en), else the default's (zh-Hans), else the first by code; none at all is an error.
	HubPushLocalized = "push.localized"
	// HubMailTargets: {} → {targets}: the languages the devices want mail translated into.
	HubMailTargets = "push.mail_targets"
	// HubTokenRevoked: {hash} → {}: the manager dropped its extension token of that hash (Authorize's): the hub ends at
	// once what the token has open through it (the calls and WebSockets on the relay streams to the manager), as for a
	// revoked device.
	HubTokenRevoked = "extensions.token_revoked"
)

// MethodAccept: {id} → {}: the hub holds a request for the manager (a relayed route): it dials the relay with the id.
const MethodAccept = "extensions.accept"

// LooksGone is EventLooksGone's params: the removed extension's looks' ids.
type LooksGone struct {
	IDs []string `json:"ids"`
}
