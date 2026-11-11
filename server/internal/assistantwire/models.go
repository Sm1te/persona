package assistantwire

import (
	"github.com/nwverse/persona/server/internal/aiwire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// The model configuration is the assistant's (docs/assistant/model-config.md "Who owns models"): the four categories' records, the
// providers added in Persona with their sealed keys, the hidden ~/.zshrc launchers, the routes of Settings › Model providers /
// Models and "New agent"'s pickers. Every model call is made there. The hub keeps what its plugins have (their
// providers, their voice models, their quota readers) and hands those over as data; the hub.json sections about
// models come in the hello (HubModels).

// HubModels are the models' baselines the manager goes by: its manager.json's "ai", inbox.triage_ai / judge_ai, kb.ai
// and inbox.agent.machine when the hub has that machine, laid over the hello (an older hub sends its hub.json's; the
// API categories' baselines when nothing is chosen for them; key files are paths, read where the manager runs), and
// the hub's: each machine's configured launchers (machines[].launchers: wrappers the node does not list, a type the
// user gave).
type HubModels struct {
	AI                aiwire.Config                   `json:"ai"`
	TriageAI          *aiwire.Config                  `json:"triage_ai,omitempty"`
	JudgeAI           *aiwire.Config                  `json:"judge_ai,omitempty"`
	KBAI              *aiwire.Config                  `json:"kb_ai,omitempty"`
	InboxAgentMachine string                          `json:"inbox_agent_machine,omitempty"`
	Launchers         map[string][]ConfiguredLauncher `json:"launchers,omitempty"`
}

// ConfiguredLauncher is one of hub.json's machines[].launchers: a launcher as GET /api/v1/machines/{mid}/launchers
// lists it, with the agent it starts (agent_type: claude, codex, traex, or a runtime's agent such as dsh).
type ConfiguredLauncher struct {
	proto.Launcher
	AgentType string `json:"agent_type,omitempty"`
}

// The node's launcher methods the assistant calls (docs/protocol/protocol.md "hub ↔ node").
const (
	DeviceLaunchersZshrc      = "launchers.zshrc"       // {} → {aliases: [{alias, env, args, command}]}: the claude aliases with their env (keys too)
	DeviceLaunchersManaged    = "launchers.managed"     // {launchers: [proto.ManagedLauncher]} → {ok}: with their keys
	DeviceLaunchersModels     = "launchers.models"      // {name, refresh, configured?, agent_type?} → proto.LauncherModels
	DeviceLaunchersModelCheck = "launchers.model.check" // {name, model, configured?} → {agent}
	DeviceLaunchersShow       = "launchers.show"        // {name} → {definition}
	DeviceLaunchersCheck      = "launchers.check"       // {name} → proto.LauncherCheck
	DeviceClaudeAuthStatus    = "claude.auth_status"    // {name} → proto.ClaudeAuthStatus (free: no model request)
	DeviceClaudeSettings      = "claude.settings"       // {locale?} → {settings: [{key, value, …}]}: Settings › Claude Code
	DeviceLaunchersHTTP       = "launchers.http"        // {name, http?, configured?} → {base_url} or quota.Response
	DeviceLaunchersRepair     = "launchers.repair"      // {name, action: "upgrade_cli"} → proto.LauncherRepair
)

// EventModelUsed: ModelUsed, one way, hub → manager: something an app picked a model for went through with it (#1644):
// a new Agent's start (POST /api/v1/sessions with model), a running session's switch (…/modes with model), a message
// to one of the assistant's conversations sent with a model picked in its chip. The manager remembers an id the
// launcher's own /model list does not have (assistant/models_custom.go); an older manager ignores it.
const EventModelUsed = "models.used"

// ModelUsed is models.used's params: Launcher is the launcher name it ran with on MachineID.
type ModelUsed struct {
	MachineID string `json:"machine_id"`
	Launcher  string `json:"launcher"`
	Model     string `json:"model"`
}

const (
	// PluginsPresets: {} → {presets: [LauncherPreset]}, the model providers of the plugins switched on and running.
	PluginsPresets = "plugins.presets"
	// PluginsVoice: {} → {plugins: [VoicePlugin]}, the voice plugins and the models each offers.
	PluginsVoice = "plugins.voice"
	// PluginsNeedTriage: {} → {names}, the plugins switched on whose inbox needs message screening (inbox.triage) to be callable.
	PluginsNeedTriage = "plugins.need_triage"
	// PluginsQuota: {name, machine, base_url, tz} → {report}: the quota plugins read launcher name's quota at its gateway
	// base_url; what they send goes back with the key through models.launcher_http (on machine). Errors: the plugins'
	// (quota.ErrUnsupported's text when none reads this gateway).
	PluginsQuota = "plugins.quota"
)

// LauncherPreset is one model provider Settings › Model providers offers: a plugin's (docs/extensions/extensions.md "Model providers") or a built-in
// one (plugin "builtin", docs/assistant/onboarding.md 4.3).
type LauncherPreset struct {
	ID          string                  `json:"id"` // <plugin>/<provider>
	Extension   string                  `json:"plugin"`
	Name        string                  `json:"name"`
	Description string                  `json:"description,omitempty"`
	Kind        string                  `json:"kind"`
	API         string                  `json:"api,omitempty"`
	BaseURL     string                  `json:"base_url"`
	Model       string                  `json:"model,omitempty"`
	SmallModel  string                  `json:"small_model,omitempty"`
	Models      []extwire.ProviderModel `json:"models,omitempty"`
	KeyHint     string                  `json:"key_hint,omitempty"`
	// Auth is how the gateway takes the key: api_key (x-api-key, the default) or bearer. Built-in presets only.
	Auth string `json:"auth,omitempty"`
	// SignupURL is the page to open an account or buy a key at (built-in presets only; the apps open it in-app).
	SignupURL string `json:"signup_url,omitempty"`
	// DisplayName is the provider's name in the request's language ("Zhipu GLM" in Chinese); Name stays the plain one.
	DisplayName string `json:"display_name,omitempty"`
	// ModelsError says why the plugin's own list could not be had (the declared models are still there).
	ModelsError string `json:"models_error,omitempty"`
}

// VoicePlugin is a voice plugin with the models it offers (plugin.<id> in the model configuration); Error says why
// its list could not be read.
type VoicePlugin struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Models  []ModelChoice `json:"models"`
	Default string        `json:"default,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// ModelChoice is one model a plugin offers.
type ModelChoice struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// What the hub's plugins ask of the model configuration (Handler).
const (
	// MethodModelsUsable: {purpose} → {usable, provider?, model?}: an API purpose resolves to an address, a model and
	// a key (a plugin whose inbox needs message screening is switched on only then). When usable, provider is the
	// display name of the connection it runs on and model its model, for a consent that names where content goes.
	MethodModelsUsable = "models.usable"
	// MethodModelsLauncherHTTP: {name, machine?, http?} → {base_url, machine} without http, else the quota.Response: a
	// request to a launcher's gateway, sent by whoever holds its key (the assistant for a provider added in Persona,
	// the machine's node for a ~/.zshrc one), for the quota plugins and the plugins' launcher sections. Errors:
	// CodeNoLauncher, or the machine's (offline…).
	MethodModelsLauncherHTTP = "models.launcher_http"
	// MethodModelsLaunchers: {} → {launchers: [GatewayLauncher]}: every launcher that has a gateway address of its own
	// (a provider added in Persona, a ~/.zshrc one), for the plugins' gateway.launchers; the ones without base_url
	// (the official API) are left out. Names and addresses only — never a key.
	MethodModelsLaunchers = "models.launchers"
)

// GatewayLauncher is one of models.launchers' launchers: its name and its gateway's address as configured (the hub
// trims it to the root and keeps the ones the plugin may reach).
type GatewayLauncher struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

// CodeNoLauncher: models.launcher_http has no launcher by that name.
const CodeNoLauncher = "no_launcher"

// CodeNoGateway: models.launcher_http's launcher has no gateway address of its own (it uses the official API).
const CodeNoGateway = "no_gateway"

// modelRoutes are the model configuration's routes the assistant answers (models_routes.go,
// models_launchers_routes.go there).
var modelRoutes = []string{
	"GET /api/v1/model-config",
	"GET /api/v1/model-config/purposes/{id}",
	"GET /api/v1/model-config/providers/{id}/models",
	"PATCH /api/v1/model-config/purposes/{id}",
	"PUT /api/v1/model-config/purposes/{id}",
	"GET /api/v1/launchers",
	"GET /api/v1/launchers/presets",
	"POST /api/v1/launchers/test",
	"POST /api/v1/launchers/discover",
	"POST /api/v1/launchers/catalog",
	"GET /api/v1/launchers/import",
	"POST /api/v1/launchers/import",
	"GET /api/v1/launchers/{name}",
	"POST /api/v1/launchers",
	"PUT /api/v1/launchers/{name}",
	"DELETE /api/v1/launchers/{name}",
	"POST /api/v1/launchers/{name}/check",
	"GET /api/v1/launchers/health",
	"POST /api/v1/launchers/health/check",
	"POST /api/v1/launchers/{name}/restore",
	"GET /api/v1/launchers/{name}/quota",
	"GET /api/v1/machines/{mid}/launchers",
	"GET /api/v1/machines/{mid}/launchers/{name}/models",
	"GET /api/v1/machines/{mid}/launchers/{name}/start-options",
	"DELETE /api/v1/machines/{mid}/launchers/{name}/models/custom", // ?model=: forget a typed id (#1644)
	// Model profiles (model_profiles.go there, docs/assistant/model-profiles.md).
	"GET /api/v1/model-profiles",
	"PUT /api/v1/model-profiles/{id}",
	"DELETE /api/v1/model-profiles/{id}",
	"POST /api/v1/model-profiles/{id}/apply",
	"POST /api/v1/model-profiles/rollback",
}

func init() { Routes = append(Routes, modelRoutes...) }
