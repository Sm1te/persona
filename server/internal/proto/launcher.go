package proto

import (
	"net/url"
	"strings"
)

type Launcher struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// Source: "zshrc" (a function in the machine's ~/.zshrc), "path" (a wrapper script on its PATH, node/pathlaunchers.go) or "persona" (managed in Settings › Models, the manager's assistant/models_launchers.go). Empty
	// from an older node: zshrc.
	Source string `json:"source,omitempty"`
	// Agent is what it starts when not Claude Code: "codex" (plain codex, or an alias running it; docs/integrations/codex.md
	// "Starting from the phone"). A Codex start needs its first message with it.
	Agent string `json:"agent,omitempty"`
	// Assistant: a runtime's launcher that may run the Persona assistant (docs/integrations/runtimes.md "Assistant"), whatever its agent.
	Assistant bool `json:"assistant,omitempty"`
}

// LauncherDetail is what a ~/.zshrc launcher really sets (the node runs it with the agent stubbed out,
// node/launcherprobe.go): every field the page shows; secrets are never in it.
type LauncherDetail struct {
	Agent         string          `json:"agent"` // claude, codex, traex
	BaseURL       string          `json:"base_url,omitempty"`
	Auth          string          `json:"auth,omitempty"` // which variable carries the key (its value is not here)
	Models        []LauncherModel `json:"models,omitempty"`
	ContextWindow int             `json:"context_window,omitempty"`
	CompactWindow int             `json:"compact_window,omitempty"` // when it differs from the window
	Effort        string          `json:"effort,omitempty"`
	TimeoutMS     int             `json:"timeout_ms,omitempty"`
	Args          []string        `json:"args,omitempty"`
	Env           []LauncherEnv   `json:"env,omitempty"` // the other settings, secrets as •••
	Error         string          `json:"error,omitempty"`
}

// LauncherCheck is one "Test" run: a real one-word request through the launcher.
type LauncherCheck struct {
	OK     bool   `json:"ok"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"` // which model answered
	Answer string `json:"answer,omitempty"`
	Millis int    `json:"millis"`
	Error  string `json:"error,omitempty"`
}

// ClaudeAuthStatus is what a node's claude.auth_status found: `claude auth status --json` run as the launcher runs
// claude (docs/assistant/onboarding.md 4.3). It spends no tokens. Only these fields leave the machine: never the account's
// email or organization, never a key (KeySource names where a key came from, e.g. ANTHROPIC_API_KEY).
type ClaudeAuthStatus struct {
	State       string `json:"state"`                  // ClaudeLoggedIn, ClaudeNotLoggedIn or ClaudeAuthUnknown
	AuthMethod  string `json:"auth_method,omitempty"`  // Claude Code's authMethod: claude.ai, api_key, api_key_helper, oauth_token, third_party, none
	APIProvider string `json:"api_provider,omitempty"` // firstParty, bedrock, vertex…
	KeySource   string `json:"key_source,omitempty"`
	Version     string `json:"version,omitempty"` // Claude Code's, when known
	Reason      string `json:"reason,omitempty"`  // why it is unknown: ClaudeAuthOld, ClaudeAuthNotClaude, ClaudeAuthUnreadable, ClaudeAuthTimeout
}

// ClaudeAuthStatus states and the reasons for an unknown one.
const (
	ClaudeLoggedIn    = "logged_in"
	ClaudeNotLoggedIn = "not_logged_in"
	ClaudeAuthUnknown = "unknown"

	ClaudeAuthOld        = "old_claude" // a Claude Code before `claude auth` (2.1.41)
	ClaudeAuthNotClaude  = "not_claude" // the launcher runs Codex or a runtime
	ClaudeAuthUnreadable = "unreadable" // no status in what it printed (claude not found, a launcher that drops the arguments)
	ClaudeAuthTimeout    = "timeout"
)

// LauncherRepair is what a node did for launchers.repair (docs/server/launcher-health.md): one of its fixed repairs.
type LauncherRepair struct {
	OK          bool   `json:"ok"`
	Unsupported bool   `json:"unsupported,omitempty"` // this repair does not apply to the launcher's agent
	Output      string `json:"output,omitempty"`      // the command's last lines, secrets as •••
}

type LauncherModel struct {
	Role  string `json:"role"` // main model, Opus, small model, subagent… (in Chinese)
	Env   string `json:"env"`
	Model string `json:"model"`
}

// LauncherModels is what a launcher's agent offers in its /model menu, for "New agent"'s model picker
// (node/launcher_models.go). No models: its agent has no list to ask for (a compatible CLI, a runtime's).
type LauncherModels struct {
	Agent  string       `json:"agent"`
	Models []AgentModel `json:"models"`
	// Locked: why a model picked here would not reach the agent (the launcher fixes its own --model); empty: it would.
	Locked string `json:"locked,omitempty"`
}

// ModelPrice is the catalog's reference price in USD per one million tokens. It is display metadata, not a gateway quote.
type ModelPrice struct {
	Input     float64  `json:"input"`
	Output    float64  `json:"output"`
	CacheRead *float64 `json:"cache_read,omitempty"`
}

// AgentModel is one model of the list: Claude Code's initialize, Codex's model/list.
type AgentModel struct {
	ID          string `json:"id"` // what --model takes (Claude's aliases too: opus, sonnet…)
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Resolved: the model an alias stands for with this launcher's settings (opus → its model id).
	Resolved string `json:"resolved,omitempty"`
	// Default: what the agent runs when no model is given; picking it is the same as picking none.
	Default bool `json:"default,omitempty"`
	// Efforts: the thinking efforts it takes, weakest first; none: no effort setting.
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	// Custom: an id the user typed that the agent's own list does not have, remembered on the hub for this machine
	// and launcher after it ran (#1644, assistant/models_custom.go); listed after the agent's own, most recent first.
	// Its efforts are not known, so none are given.
	Custom bool `json:"custom,omitempty"`
	// Price is the built-in catalog's exact-entry reference price; nil when unknown or inherited from another model.
	Price *ModelPrice `json:"price,omitempty"`
}

type LauncherEnv struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ManagedLauncher is a launcher kept by the hub (Settings › Models): what `personad launch <name>` sets before it runs claude.
// The key only goes to nodes (in memory), never to the app.
type ManagedLauncher struct {
	Kind       string `json:"kind,omitempty"`  // launcher (default) or api
	Agent      string `json:"agent,omitempty"` // claude (default), codex, or agy
	API        string `json:"api,omitempty"`   // messages, responses, or chat for direct API providers
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	SmallModel string `json:"small_model,omitempty"`
	// SubagentModel is CLAUDE_CODE_SUBAGENT_MODEL; "" keeps the model (the old behaviour).
	SubagentModel string `json:"subagent_model,omitempty"`
	// CustomModel is the gateway's /model extra entry (ANTHROPIC_CUSTOM_MODEL_OPTION) and how it reads.
	CustomModel            string `json:"custom_model,omitempty"`
	CustomModelName        string `json:"custom_model_name,omitempty"`
	CustomModelDescription string `json:"custom_model_description,omitempty"`
	// Args go before the caller's (e.g. --dangerously-skip-permissions).
	Args   []string `json:"args,omitempty"`
	APIKey string   `json:"api_key,omitempty"`
	// Auth is how the key goes to the gateway: "api_key" (x-api-key, ANTHROPIC_API_KEY; the default) or "bearer"
	// (Authorization: Bearer, ANTHROPIC_AUTH_TOKEN), for gateways that take only that.
	Auth string `json:"auth,omitempty"`

	// The provider as cindy models it (#2455): what is known of each model the gateway serves, the model per Claude
	// Code tier, and the gateway's own needs. All optional; an older launcher reads as before.
	//
	// Models: the gateway's models with their context window and thinking levels (the first is not the default; Model
	// is). A model whose window is over 200K runs as "<id>[1m]" (CLI and SDK), its compact window capped to it.
	Models []ProviderModel `json:"models,omitempty"`
	// The model of each Claude Code tier (ANTHROPIC_DEFAULT_*_MODEL); "" is Model.
	OpusModel   string `json:"opus_model,omitempty"`
	SonnetModel string `json:"sonnet_model,omitempty"`
	HaikuModel  string `json:"haiku_model,omitempty"`
	FableModel  string `json:"fable_model,omitempty"`
	// SubagentForce: CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1 (the subagent model wins over an agent's own model:).
	SubagentForce bool `json:"subagent_force,omitempty"`
	// Headers: ANTHROPIC_CUSTOM_HEADERS, one "Name: value" a line. A secret like the key: sealed at rest, never sent
	// back to clients (they see HasHeaders).
	Headers    string `json:"headers,omitempty"`
	HasHeaders bool   `json:"has_headers,omitempty"`
	// Proxy / NoProxy: HTTP_PROXY and HTTPS_PROXY / NO_PROXY for the gateway.
	Proxy   string `json:"proxy,omitempty"`
	NoProxy string `json:"no_proxy,omitempty"`
	// Env: more environment for the launch, one NAME=value a line (#5953), set last: it wins over what Persona sets,
	// the shell's environment and the user's settings env. A secret like the key: sealed at rest, never sent back to
	// clients (they see HasEnv).
	Env    string `json:"env,omitempty"`
	HasEnv bool   `json:"has_env,omitempty"`
	// TimeoutMS: API_TIMEOUT_MS; 0 is 3000000 for a gateway (BaseURL set), Claude Code's own default otherwise.
	TimeoutMS int `json:"timeout_ms,omitempty"`
	// CompactWindow: CLAUDE_CODE_AUTO_COMPACT_WINDOW; 0 is the default model's context window (none when unknown).
	// Assistant conversations, the main one included, follow it like agents do (#3066).
	CompactWindow int `json:"compact_window,omitempty"`
	// MaxContext: CLAUDE_CODE_MAX_CONTEXT_TOKENS; 0 is the default model's context window when it is over 200K.
	MaxContext int `json:"max_context,omitempty"`
	// Effort: the default thinking level when the caller gives none (passed as --effort by personad launch, #2307).
	Effort string `json:"effort,omitempty"`
	// Source: where it came from: "" (made by hand), "zshrc:<alias>@<machine id>" (imported), "preset:<id>".
	Source string `json:"source,omitempty"`

	// Device scope and on-demand delivery (docs/integrations/launchers.md "Device scope and on-demand delivery").
	// Delivery: "on_demand" (a computer fetches the secrets the first time it needs them) or "push" (each computer in
	// scope fetches them as soon as it connects); "" is a record from before scopes: push.
	Delivery string `json:"delivery,omitempty"`
	// OwnService: "skip" (not used on a computer whose harness has a usable service of its own) or "use"; "" is use.
	OwnService string `json:"own_service,omitempty"`
	// Withheld (launchers.catalog only): APIKey, Headers and Env are left out; the node asks launchers.pull for them.
	Withheld bool `json:"withheld,omitempty"`
	// Rev changes whenever the provider is saved: a node drops what it pulled of an older one.
	Rev string `json:"rev,omitempty"`
}

// The values of ManagedLauncher.Delivery and OwnService.
const (
	DeliveryOnDemand = "on_demand"
	DeliveryPush     = "push"
	OwnServiceSkip   = "skip"
	OwnServiceUse    = "use"
)

// Pushed: every computer in scope fetches the provider's secrets as soon as it connects.
func (l ManagedLauncher) Pushed() bool { return l.Delivery == "" || l.Delivery == DeliveryPush }

// SkipsOwnService: the provider is not used on a computer whose harness already has a usable service of its own.
func (l ManagedLauncher) SkipsOwnService() bool { return l.OwnService == OwnServiceSkip }

// Public is the provider without its secrets, as launchers.catalog names it to a node that pulls them.
func (l ManagedLauncher) Public() ManagedLauncher {
	l.APIKey, l.Headers, l.Env, l.HasHeaders, l.HasEnv, l.Withheld = "", "", "", false, false, true
	return l
}

// OwnServiceState is what a node found of a harness's own service (launchers.own_service, launchers.pull): Known
// false when it could not tell (no detection for that harness, a timeout). Via names how, never a secret: "login",
// "api_key", "launcher".
type OwnServiceState struct {
	Agent string `json:"agent"`
	Known bool   `json:"known"`
	Own   bool   `json:"own"`
	Via   string `json:"via,omitempty"`
}

// Codes of a refused launchers.pull and of a start the manager refuses for a provider's scope.
const (
	CodeLauncherOutOfScope = "launcher_out_of_scope"
	CodeLauncherOwnService = "launcher_own_service"
	// CodeLauncherKeyUnresolved: the key that computer uses is a reference its provider extension did not resolve
	// (off, signed out, not found…); the message is the provider's reason (docs/extensions/secret-providers.md).
	CodeLauncherKeyUnresolved = "launcher_key_unresolved"
)

// ProviderModel is one model a provider's gateway serves (cindy's ProviderRuntimeModelConfig, the Anthropic part).
type ProviderModel struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// ContextWindow in tokens; 0 is unknown (Claude Code then assumes 200K).
	ContextWindow int `json:"context_window,omitempty"`
	// Efforts: the thinking levels it takes (low, medium, high, xhigh, max); empty is unknown (all are offered).
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"default_effort,omitempty"`
	// InheritedFrom is the catalog id an unlisted newer generation took a missing context window or thinking
	// level from. Empty for an exact catalog entry, and when the gateway already reported every one of those.
	InheritedFrom string `json:"inherited_from,omitempty"`
	// Price is the exact catalog entry's reference price. It is never inherited from an earlier generation.
	Price *ModelPrice `json:"price,omitempty"`
}

// The assistant's runtimes for a Claude launcher (model_config.assistant's "runtime", the assistant.send / .start /
// .warm params' "runtime"): the Claude Code CLI in stream-json mode (the default), or the Claude Agent SDK through the
// node's Node bridge (integrations/agent-sdk, #2455), which falls back to the CLI where no Node 22+ is found.
const (
	AssistantRuntimeCLI      = "cli"
	AssistantRuntimeAgentSDK = "agentsdk"
)

// Why an assistant conversation that asked for the Agent SDK runs on the CLI (Session.RuntimeFallback), as the node
// writes it: in Chinese, the ones ending in a full-width colon followed by the cause, FallbackShellLauncher with the launcher's name.
// The hub rewrites them in the app's language (hub/transcript_text.go runtimeFallbackText).
const (
	FallbackShellLauncher  = "启动器 %s 是这台电脑上的 shell 命令，Agent SDK 用不了它的设置"
	FallbackReadBridge     = "读不到 Agent SDK 桥："
	FallbackNoBridge       = "这个 personad 没有带 Agent SDK 桥"
	FallbackWriteBridge    = "写不出 Agent SDK 桥："
	FallbackNoBridgeScript = "Agent SDK 桥的包里没有 bridge.mjs"
	FallbackNoNode         = "没有 Node.js 22 及以上："
	FallbackNoClaude       = "找不到 claude："
)

// BearerAuth: the launcher's key goes as Authorization: Bearer (ANTHROPIC_AUTH_TOKEN) rather than x-api-key.
func (l ManagedLauncher) BearerAuth() bool { return l.Auth == "bearer" }

// AgentType is the agent CLI this managed launcher runs ("claude" by default, or "codex" / "agy").
func (l ManagedLauncher) AgentType() string {
	if l.Agent != "" {
		return l.Agent
	}
	return "claude"
}

// MessagesBase: the provider's Base URL reaches its agent as ANTHROPIC_BASE_URL (node managedEnv), which gets
// /v1/messages added to it: a Messages provider whose agent is not Codex or agy (they take OPENAI_BASE_URL /
// GOOGLE_GEMINI_BASE_URL). Responses and Chat Completions bases keep their /v1.
func (l ManagedLauncher) MessagesBase() bool {
	if a := l.AgentType(); a == "codex" || a == "agy" {
		return false
	}
	return l.API == "" || l.API == "messages"
}

// LaunchBaseURL is the Base URL as the provider's agent takes it: a Messages base through MessagesBaseURL, any other as
// stored. The hub stores and delivers it, and the node launches with it, so a provider saved before the hub dropped a
// trailing /v1 still reaches what Test connection called.
func (l ManagedLauncher) LaunchBaseURL() string {
	if l.MessagesBase() {
		return MessagesBaseURL(l.BaseURL)
	}
	return l.BaseURL
}

// MessagesBaseURL is a gateway address as ANTHROPIC_BASE_URL takes it. Claude Code and the Anthropic SDKs add
// /v1/messages themselves, so the /v1 many gateways put at the end of their documented address would make
// /v1/v1/messages: the /v1 ending the path goes with any trailing slashes, a deployment prefix before it stays
// ("https://gw/prefix/v1/" → "https://gw/prefix"). It is idempotent (a run of /v1 goes whole): the hub stores its
// result and the node applies it again at launch, and both must reach what Test connection called. Anything else (no
// host, a query) is only trimmed.
func MessagesBaseURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" {
		return base
	}
	authority := strings.Index(base, "//") + len("//")
	slash := strings.IndexByte(base[authority:], '/')
	if slash < 0 {
		return base // no path
	}
	root, path := base[:authority+slash], base[authority+slash:]
	for {
		rest, ok := strings.CutSuffix(path, "/v1")
		if !ok {
			return root + path
		}
		path = strings.TrimRight(rest, "/")
	}
}
