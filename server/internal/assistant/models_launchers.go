package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// Settings › Model providers (docs/integrations/launchers.md): every launcher in one list. Those in each machine's ~/.zshrc are found by its
// node and remembered by the hub (it hands them over: launchers.zshrc); those added in the app are the assistant's —
// base URL, model, and the API key AES-GCM sealed with the manager's own secret.key (docs/assistant/managers.md "The built-in
// manager's own files") — and given to every node, which runs them as `personad launch <name>`. Deleting a
// ~/.zshrc launcher only hides it in Persona.

const (
	launchersManagedKey = "launchers.managed" // []storedLauncher
	launchersHiddenKey  = "launchers.hidden"  // []string: ~/.zshrc launcher names hidden in Persona
	// launchersMachineKeysKey: []machineKey, a provider's key for one computer (models_launchers_scope.go).
	launchersMachineKeysKey = "launchers.machine_keys"
	launchersZshrcKey       = "launchers.zshrc" // map machine id → []proto.Launcher, the last list seen
	// launchersMetaKey: map machine id → launcherMeta (its shell, rc file, when its list last came in).
	launchersMetaKey = "launchers.zshrc.meta"
)

// launcherSyncTimeout is how long the list waits for one machine (its node caches a minute and keeps listing past
// it, so a slow rc file is there the next time); the page shows that machine as timed out with its last list.
var launcherSyncTimeout = 10 * time.Second

// launcherMeta is what is kept of a machine's last good launcher list besides the list.
type launcherMeta struct {
	Shell    string    `json:"shell,omitempty"`
	RCFile   string    `json:"rc_file,omitempty"`
	SyncedAt time.Time `json:"synced_at"`
}

// LauncherSync is how one machine's launchers came in this time, for Settings › Model providers (docs/integrations/launchers.md, “Sync state”):
// ok, offline (not connected: its last list shows, marked), timeout (no answer in launcherSyncTimeout) or error
// (Error says why).
type LauncherSync struct {
	Machine  string     `json:"machine"`
	Name     string     `json:"name"`
	State    string     `json:"state"`
	Error    string     `json:"error,omitempty"`
	Shell    string     `json:"shell,omitempty"`
	RCFile   string     `json:"rc_file,omitempty"`
	SyncedAt *time.Time `json:"synced_at,omitempty"` // the last good list, also for one that did not answer now
}

// defaultRCFile is where a node from before the rc_file field found its launchers: it only ran zsh scripts there.
const defaultRCFile = "~/.zshrc"

// storedLauncher is a provider added in Persona as kept in settings: the key sealed.
type storedLauncher struct {
	proto.ManagedLauncher
	KeySealed string `json:"key_sealed,omitempty"`
	// HeadersSealed: Headers (ANTHROPIC_CUSTOM_HEADERS) sealed like the key; the embedded Headers is never stored.
	HeadersSealed string `json:"headers_sealed,omitempty"`
	EnvSealed     string `json:"env_sealed,omitempty"` // Env (#5953) sealed the same way; Env itself is never stored
	Preset        string `json:"preset,omitempty"`     // the preset (built-in or a plugin's) it was added from
	// DisplayName is what the apps call it (any language, "Zhipu GLM"); Name stays the ASCII id the launch uses.
	DisplayName string `json:"display_name,omitempty"`
	// Share and ScopeMachines are its device scope (models_launchers_scope.go): "all", "selected", "none"; "" is a
	// record from before scopes, every confirmed computer as then.
	Share         string    `json:"share,omitempty"`
	ScopeMachines []string  `json:"scope_machines,omitempty"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
}

// LauncherView is one launcher for the app: where it comes from, which machines have it; never the key.
type LauncherView struct {
	Kind                   string   `json:"kind,omitempty"`
	Agent                  string   `json:"agent,omitempty"`
	API                    string   `json:"api,omitempty"`
	Name                   string   `json:"name"`
	DisplayName            string   `json:"display_name,omitempty"` // managed: the name shown, "" shows Name
	Source                 string   `json:"source"`                 // zshrc (an rc file's) | persona | agy | builtin (plain claude)
	BaseURL                string   `json:"base_url,omitempty"`
	Model                  string   `json:"model,omitempty"`
	SmallModel             string   `json:"small_model,omitempty"`
	SubagentModel          string   `json:"subagent_model,omitempty"`
	CustomModel            string   `json:"custom_model,omitempty"`
	CustomModelName        string   `json:"custom_model_name,omitempty"`
	CustomModelDescription string   `json:"custom_model_description,omitempty"`
	Args                   []string `json:"args,omitempty"`
	HasKey                 bool     `json:"has_key,omitempty"`
	Auth                   string   `json:"auth,omitempty"`     // managed: api_key (x-api-key) or bearer
	Machines               []string `json:"machines,omitempty"` // zshrc: the machines whose rc file defines it; builtin: that have claude
	Hidden                 bool     `json:"hidden,omitempty"`
	// RCFiles (source zshrc only): the files it was found in (~/.zshrc, ~/.bashrc: each machine's login shell's), for the group title.
	RCFiles []string `json:"rc_files,omitempty"`
	// Stale (zshrc, builtin): the machines of Machines that did not answer this time (offline, timed out, failed):
	// what they had the last time.
	Stale []string `json:"stale,omitempty"`
	// Versions (builtin): Claude Code's version on each machine that said.
	Versions map[string]string `json:"versions,omitempty"`
	// Managed only (#2455, proto.ManagedLauncher's fields): never the headers themselves, only has_headers.
	Models        []proto.ProviderModel `json:"models,omitempty"`
	OpusModel     string                `json:"opus_model,omitempty"`
	SonnetModel   string                `json:"sonnet_model,omitempty"`
	HaikuModel    string                `json:"haiku_model,omitempty"`
	FableModel    string                `json:"fable_model,omitempty"`
	SubagentForce bool                  `json:"subagent_force,omitempty"`
	HasHeaders    bool                  `json:"has_headers,omitempty"`
	HasEnv        bool                  `json:"has_env,omitempty"` // never the environment itself
	Proxy         string                `json:"proxy,omitempty"`
	NoProxy       string                `json:"no_proxy,omitempty"`
	TimeoutMS     int                   `json:"timeout_ms,omitempty"`
	CompactWindow int                   `json:"compact_window,omitempty"`
	MaxContext    int                   `json:"max_context,omitempty"`
	Effort        string                `json:"effort,omitempty"`
	// Origin is ManagedLauncher.Source ("" made by hand, "zshrc:<alias>@<machine>", "preset:<id>"); "source" is taken.
	Origin string `json:"origin,omitempty"`
	// Managed only: the device scope (docs/integrations/launchers.md "Device scope and on-demand delivery"). LegacyScope:
	// saved before scopes, read as all (work computers too) + push + use. KeyMachines: the computers with a key of their own.
	Share         string   `json:"share,omitempty"`
	ScopeMachines []string `json:"scope_machines,omitempty"`
	Delivery      string   `json:"delivery,omitempty"`
	OwnService    string   `json:"own_service,omitempty"`
	KeyMachines   []string `json:"key_machines,omitempty"`
	LegacyScope   bool     `json:"legacy_scope,omitempty"`
	// KeyReference (managed): the key is a reference to a provider extension's secret: the reference and what it
	// names, never the value (launcher_secrets.go). MachineKeyReferences: the same for the computers' own keys.
	KeyReference         *extmgr.SecretReferenceView            `json:"key_reference,omitempty"`
	MachineKeyReferences map[string]*extmgr.SecretReferenceView `json:"machine_key_references,omitempty"`
	keySealed            string                                 // the stored key, for withKeyReferences
	// Definition per machine (zshrc launchers, detail only): the function body, secrets masked.
	Definitions map[string]string `json:"definitions,omitempty"`
	// Details per machine (zshrc launchers, detail only): what the launcher really sets when run (node/launcherprobe.go).
	Details map[string]proto.LauncherDetail `json:"details,omitempty"`
}

// launcherView is a managed launcher for the app: never the key or the headers.
func launcherView(l storedLauncher) LauncherView {
	return LauncherView{Kind: l.Kind, Agent: l.Agent, API: l.API, Name: l.Name, DisplayName: l.DisplayName, Source: "persona", BaseURL: l.BaseURL, Model: l.Model, SmallModel: l.SmallModel,
		SubagentModel: l.SubagentModel, CustomModel: l.CustomModel, CustomModelName: l.CustomModelName,
		CustomModelDescription: l.CustomModelDescription, Args: l.Args, HasKey: l.KeySealed != "", Auth: l.Auth,
		Models: l.Models, OpusModel: l.OpusModel, SonnetModel: l.SonnetModel, HaikuModel: l.HaikuModel, FableModel: l.FableModel,
		SubagentForce: l.SubagentForce, HasHeaders: l.HeadersSealed != "", HasEnv: l.EnvSealed != "", Proxy: l.Proxy, NoProxy: l.NoProxy, TimeoutMS: l.TimeoutMS,
		CompactWindow: l.CompactWindow, MaxContext: l.MaxContext, Effort: l.Effort, Origin: l.Source,
		Share: l.share(), ScopeMachines: l.ScopeMachines, Delivery: l.delivery(), OwnService: l.ownService(), LegacyScope: l.Share == "",
		keySealed: l.KeySealed}
}

var managedLauncherName = lazyre.New(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// reservedLauncherNames are taken by claude itself and the /api/v1/launchers/<word> routes.
var reservedLauncherNames = []string{"claude", "presets", "test", "discover", "import", "health"}

// ---- the key ----

// The providers' keys are sealed with the manager's own secret.key (internal/secretkey; the built-in manager's is a
// copy of the data directory's, which sealed them before they were the assistant's, docs/assistant/managers.md "The built-in
// manager's own files").

func (s *Service) sealKey(plain string) (string, error) { return s.models.secret.Seal(plain) }

func (s *Service) openKey(sealed string) (string, error) { return s.models.secret.Open(sealed) }

// ---- what is stored ----

type managedSnapshotKey struct{}

// withManagedSnapshot lets nested model resolution use the same checked provider list as the
// operation holding models.mu, including fallback and in-use decisions.
func withManagedSnapshot(ctx context.Context, list []storedLauncher) context.Context {
	return context.WithValue(ctx, managedSnapshotKey{}, list)
}

type settingsMemoKey struct{}

// settingsMemo is what one read-only request has read of the settings, by key: Settings › Model configuration
// resolves every purpose against the same provider list and records, which would otherwise be read again for each.
type settingsMemo struct {
	mu   sync.Mutex
	vals map[string]memoVal
}

type memoVal struct {
	raw   json.RawMessage
	found bool
}

// withSettingsMemo lets memoSetting read each key once for the rest of ctx. Only for requests that write none of
// the keys read through it: a write in the same ctx would not be seen.
func withSettingsMemo(ctx context.Context) context.Context {
	if _, ok := ctx.Value(settingsMemoKey{}).(*settingsMemo); ok {
		return ctx
	}
	return context.WithValue(ctx, settingsMemoKey{}, &settingsMemo{vals: map[string]memoVal{}})
}

// memoSetting is st.Setting, read once per key when ctx has a memo (withSettingsMemo). Every call decodes into out
// anew, so callers that change what they got do not change what the next one gets; a failed read is not kept.
func memoSetting(ctx context.Context, st Store, key string, out any) (bool, error) {
	m, ok := ctx.Value(settingsMemoKey{}).(*settingsMemo)
	if !ok {
		return st.Setting(ctx, key, out)
	}
	m.mu.Lock()
	v, hit := m.vals[key]
	m.mu.Unlock()
	if !hit {
		var raw json.RawMessage
		found, err := st.Setting(ctx, key, &raw)
		if err != nil {
			return false, err
		}
		v = memoVal{raw: raw, found: found}
		m.mu.Lock()
		m.vals[key] = v
		m.mu.Unlock()
	}
	if !v.found {
		return false, nil
	}
	return true, json.Unmarshal(v.raw, out)
}

func (s *Service) storedLaunchers(ctx context.Context) []storedLauncher {
	list, _ := s.storedLaunchersKnown(ctx)
	return list
}

// storedLaunchersKnown is storedLaunchers with whether the list is really known: false means the setting could not be
// read, which is not the same as no managed launcher. Callers that judge whether a launcher still exists must not read
// an unknown list as "it is gone": a message would be refused as launcher_missing while the provider is still
// configured (#4437). No database configured counts as no managed launchers, as storedLaunchers always did.
func (s *Service) storedLaunchersKnown(ctx context.Context) ([]storedLauncher, bool) {
	if list, ok := ctx.Value(managedSnapshotKey{}).([]storedLauncher); ok {
		return list, true
	}
	st := s.store()
	if st == nil {
		return nil, true // no database configured: no managed launchers, as before
	}
	var list []storedLauncher
	if _, err := memoSetting(ctx, st, launchersManagedKey, &list); err != nil {
		return nil, false
	}
	return list, true
}

var errManagedLaunchersRead = errors.New("managed provider configuration unavailable")

// readStoredLaunchers preserves every raw sealed record for mutation decisions. A missing setting is empty;
// storage and JSON failures abort the operation. Decryption belongs to consumers that need credentials,
// so an explicitly replaced or removed corrupt credential remains repairable.
func (s *Service) readStoredLaunchers(ctx context.Context) (list []storedLauncher, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", errManagedLaunchersRead, err)
		}
	}()
	st := s.store()
	if st == nil {
		return nil, i18n.Err("assistant-models.err_no_database")
	}
	if _, err := st.Setting(ctx, launchersManagedKey, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// readHiddenLaunchers reads launchers.hidden from the store itself (never a request's memo) for a change to it, made
// under models.mu so no other hide or restore is lost; a failed read fails the change instead of emptying the list.
func (s *Service) readHiddenLaunchers(ctx context.Context) ([]string, error) {
	st := s.store()
	if st == nil {
		return nil, i18n.Err("assistant-models.err_no_database")
	}
	var list []string
	_, err := st.Setting(ctx, launchersHiddenKey, &list)
	return list, err
}

func (s *Service) hiddenLaunchers(ctx context.Context) []string {
	var list []string
	if st := s.store(); st != nil {
		_, _ = memoSetting(ctx, st, launchersHiddenKey, &list)
	}
	if n, _ := ctx.Value(unhiddenKey{}).(string); n != "" && slices.Contains(list, n) {
		list = slices.DeleteFunc(slices.Clone(list), func(h string) bool { return h == n })
	}
	return list
}

type unhiddenKey struct{}

// withUnhidden has hiddenLaunchers leave name out: a launcher that a configuration or a conversation still names keeps
// running when it is hidden, as the hide route says; hiding takes it out of the pickers only (#4659).
func withUnhidden(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, unhiddenKey{}, name)
}

func (s *Service) putSetting(ctx context.Context, key string, v any) error {
	st := s.store()
	if st == nil {
		return errors.New("no database")
	}
	return st.PutSetting(ctx, key, v)
}
