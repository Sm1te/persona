package assistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Importing ~/.zshrc aliases as managed providers (#2455, docs/integrations/launchers.md): the node's launchers.zshrc reads a
// machine's `alias x="K=V … claude [args]"` lines with their env (keys included, only over this RPC); the hub merges
// them by base URL + key into providers. The preview never carries a key or headers; importing reads them again
// and keeps them in the hub, sealed.

// zshrcAlias is one alias launchers.zshrc returns.
type zshrcAlias struct {
	Alias   string            `json:"alias"`
	Env     map[string]string `json:"env"`
	Args    []string          `json:"args,omitempty"`
	Command string            `json:"command"`
}

// importedProvider is one provider of the import preview: the launcher it would save, without the key and headers.
type importedProvider struct {
	proto.ManagedLauncher
	HasKey   bool     `json:"has_key"`
	Aliases  []string `json:"aliases"`
	Existing string   `json:"existing,omitempty"` // the stored provider with the same base URL and key
}

// importGroup is a provider merged from aliases, with its secrets (the manager's only).
type importGroup struct {
	importedProvider
	key, headers string
}

// knownProvider is a stored provider as the merge sees it: name, base URL and opened key.
type knownProvider struct{ name, baseURL, key string }

const contextWindow1M = 1000000

// strip1M drops a model's "[1m]" suffix: the 1M context it asks for becomes the model's context window.
func strip1M(model string) (string, int) {
	if id, ok := strings.CutSuffix(strings.TrimSpace(model), "[1m]"); ok {
		return id, contextWindow1M
	}
	return strings.TrimSpace(model), 0
}

var importNameBad = lazyre.New(`[^A-Za-z0-9_-]+`)

// importName is an alias name made a valid launcher name.
func importName(alias string) string {
	n := importNameBad.ReplaceAllString(alias, "-")
	// Not a valid name, or one taken (an alias named claude would stand in for Claude Code itself).
	if !managedLauncherName.MatchString(n) || slices.Contains(reservedLauncherNames, n) {
		n = "zshrc_" + strings.TrimLeft(n, "-")
	}
	return n
}

func envInt(env map[string]string, k string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(env[k]))
	return max(n, 0)
}

// mergeZshrcAliases turns machine's claude aliases into providers, one per base URL + key, in the order first seen:
// the first alias names it and gives its settings, every alias's ANTHROPIC_MODEL joins its models. Aliases without a
// base URL (plain Claude Code) and ones not running claude are skipped. known marks the ones already stored.
func mergeZshrcAliases(machine string, aliases []zshrcAlias, known []knownProvider) []importGroup {
	var out []importGroup
	for _, a := range aliases {
		if a.Command != "" && a.Command != "claude" {
			continue
		}
		env := a.Env
		base := proto.MessagesBaseURL(env["ANTHROPIC_BASE_URL"]) // stored as a saved provider is (saveLauncher)
		if base == "" {
			continue
		}
		key, auth := strings.TrimSpace(env["ANTHROPIC_API_KEY"]), "api_key"
		if token := strings.TrimSpace(env["ANTHROPIC_AUTH_TOKEN"]); token != "" {
			key, auth = strings.TrimSpace(strings.TrimPrefix(token, "Bearer ")), "bearer"
		}
		i := slices.IndexFunc(out, func(g importGroup) bool { return g.BaseURL == base && g.key == key })
		if i < 0 {
			out = append(out, newImportGroup(machine, a, base, key, auth, known))
			i = len(out) - 1
		} else {
			out[i].Aliases = append(out[i].Aliases, a.Alias)
		}
		if id, window := strip1M(env["ANTHROPIC_MODEL"]); id != "" {
			addImportedModel(&out[i].Models, proto.ProviderModel{ID: id, ContextWindow: window})
		}
	}
	return out
}

func newImportGroup(machine string, a zshrcAlias, base, key, auth string, known []knownProvider) importGroup {
	env := a.Env
	tier := func(k string) string { id, _ := strip1M(env[k]); return id }
	model, _ := strip1M(env["ANTHROPIC_MODEL"])
	proxy := strings.TrimSpace(env["HTTPS_PROXY"])
	if proxy == "" {
		proxy = strings.TrimSpace(env["HTTP_PROXY"])
	}
	// A proxy with a password in it is not imported: the proxy is stored and returned in the clear.
	if u, err := url.Parse(proxy); err == nil && u.User != nil {
		proxy = ""
	}
	force := strings.TrimSpace(env["CLAUDE_CODE_SUBAGENT_MODEL_FORCE"])
	g := importGroup{key: key, headers: strings.TrimSpace(env["ANTHROPIC_CUSTOM_HEADERS"])}
	g.ManagedLauncher = proto.ManagedLauncher{
		Name: importName(a.Alias), BaseURL: base, Auth: auth, Model: model, Args: a.Args,
		OpusModel: tier("ANTHROPIC_DEFAULT_OPUS_MODEL"), SonnetModel: tier("ANTHROPIC_DEFAULT_SONNET_MODEL"),
		HaikuModel: tier("ANTHROPIC_DEFAULT_HAIKU_MODEL"), FableModel: tier("ANTHROPIC_DEFAULT_FABLE_MODEL"),
		SmallModel: tier("ANTHROPIC_SMALL_FAST_MODEL"), SubagentModel: tier("CLAUDE_CODE_SUBAGENT_MODEL"),
		SubagentForce: force == "1" || strings.EqualFold(force, "true"),
		Effort:        strings.TrimSpace(env["CLAUDE_CODE_EFFORT_LEVEL"]),
		CompactWindow: envInt(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW"), MaxContext: envInt(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS"),
		TimeoutMS: envInt(env, "API_TIMEOUT_MS"), Proxy: proxy, NoProxy: strings.TrimSpace(env["NO_PROXY"]),
		HasHeaders: g.headers != "", Source: "zshrc:" + a.Alias + "@" + machine,
	}
	g.HasKey, g.Aliases = key != "", []string{a.Alias}
	if i := slices.IndexFunc(known, func(k knownProvider) bool { return k.baseURL == base && k.key == key }); i >= 0 {
		g.Existing = known[i].name
	}
	return g
}

// addImportedModel adds m to models, or raises a known one's context window; reports whether models changed.
func addImportedModel(models *[]proto.ProviderModel, m proto.ProviderModel) bool {
	for i := range *models {
		if (*models)[i].ID == m.ID {
			if m.ContextWindow <= (*models)[i].ContextWindow {
				return false
			}
			(*models)[i].ContextWindow = m.ContextWindow
			return true
		}
	}
	*models = append(*models, m)
	return true
}

// mergeImportedModels puts the imported models into an existing provider's: new ids added, the rest untouched
// except an unknown context window. Reports whether anything changed.
func mergeImportedModels(dst *[]proto.ProviderModel, from []proto.ProviderModel) bool {
	changed := false
	for _, m := range from {
		changed = addImportedModel(dst, m) || changed
	}
	return changed
}

// errNoSuchMachine: the import names a machine the hub does not have.
var errNoSuchMachine = i18n.Err("assistant-models_import.err_no_machine")

// zshrcAliases asks machine's node for its ~/.zshrc claude aliases (a var so tests give their own).
var zshrcAliases = func(ctx context.Context, s *Service, machine string) ([]zshrcAlias, error) {
	if _, ok := s.device(ctx, machine); !ok {
		return nil, errNoSuchMachine
	}
	var out struct {
		Aliases []zshrcAlias `json:"aliases"`
	}
	err := s.call(ctx, machine, assistantwire.DeviceLaunchersZshrc, struct{}{}, &out)
	return out.Aliases, err
}

// importGroups is machine's aliases merged against the stored providers.
func (s *Service) importGroups(ctx context.Context, machine string) ([]importGroup, error) {
	aliases, err := zshrcAliases(ctx, s, machine)
	if err != nil {
		return nil, err
	}
	s.models.mu.Lock()
	stored, err := s.readStoredLaunchers(ctx)
	s.models.mu.Unlock()
	if err != nil {
		return nil, err
	}
	known, err := s.knownProviders(stored)
	if err != nil {
		return nil, err
	}
	return mergeZshrcAliases(machine, aliases, known), nil
}

// knownProviders opens only the keys needed to match aliases against every stored provider.
// A key failure cannot be treated as an absent provider; unrelated sealed headers and environment stay opaque.
// A key that is a reference stays the reference (launcher_secrets.go): it matches no alias's key and is not resolved.
func (s *Service) knownProviders(stored []storedLauncher) ([]knownProvider, error) {
	known := make([]knownProvider, 0, len(stored))
	for _, l := range stored {
		key, err := s.openKey(l.KeySealed)
		if err != nil {
			return nil, i18n.Err("assistant-models.err_key_decrypt")
		}
		// One stored before saving dropped a trailing /v1 is the same gateway.
		known = append(known, knownProvider{name: l.Name, baseURL: l.LaunchBaseURL(), key: key})
	}
	return known, nil
}

// importErr answers a failed launchers.zshrc: 404 for an unknown machine, 503 offline, 409 node_outdated.
func importErr(w http.ResponseWriter, err error) {
	var re *rpc.Error
	switch {
	case errors.Is(err, errManagedLaunchersRead):
		apierr.Write(w, http.StatusInternalServerError, err)
	case errors.Is(err, errNoSuchMachine):
		apierr.Write(w, http.StatusNotFound, err)
	case errors.Is(err, apierr.ErrOffline):
		apierr.WriteCode(w, http.StatusServiceUnavailable, "offline", i18n.Err("assistant-models_import.err_offline"))
	case errors.As(err, &re) && strings.HasPrefix(re.Message, "unknown method"):
		apierr.WriteCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.Err("assistant-models_import.outdated"))
	default:
		apierr.Call(w, err)
	}
}

// sealImported makes g a stored launcher: the key and headers sealed.
func (s *Service) sealImported(g importGroup, now time.Time) (storedLauncher, error) {
	l := storedLauncher{ManagedLauncher: g.ManagedLauncher, Created: now, Updated: now}
	l.HasHeaders = false                         // the preview's; a stored launcher's comes from its sealed headers
	_ = scopeInput{}.applyScope(&l, true, false) // a new provider's scope (docs/integrations/launchers.md "Device scope and on-demand delivery")
	if l.Auth == "api_key" {
		l.Auth = "" // stored as the default
	}
	if g.key != "" {
		sealed, err := s.sealKey(g.key)
		if err != nil {
			return l, err
		}
		l.KeySealed = sealed
	}
	if g.headers != "" {
		sealed, err := s.sealKey(g.headers)
		if err != nil {
			return l, err
		}
		l.HeadersSealed = sealed
	}
	l.APIKey, l.Headers = "", "" // only ever stored sealed
	return l, nil
}

// importLaunchers saves the picked groups: new ones created, ones already stored get the new models only.
func (s *Service) importLaunchers(ctx context.Context, groups []importGroup, names []string) (created, updated []string, err error) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	list, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return nil, nil, err
	}
	known, err := s.knownProviders(list)
	if err != nil {
		return nil, nil, err
	}
	taken := map[string]bool{}
	for _, v := range s.launcherViewsFrom(ctx, s.zshrcLaunchers(ctx, false), list) {
		taken[v.Name] = true
	}
	now := time.Now().UTC()
	for _, name := range names {
		i := slices.IndexFunc(groups, func(g importGroup) bool { return g.Name == name })
		if i < 0 {
			return nil, nil, i18n.Err("assistant-models_import.err_nothing_named", i18n.Args{"name": name})
		}
		g := groups[i]
		// The preview may precede another mutation; match against today's checked snapshot.
		g.Existing = ""
		for _, k := range known {
			if k.baseURL == g.BaseURL && k.key == g.key {
				g.Existing = k.name
				break
			}
		}
		if g.Existing != "" {
			j := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == g.Existing })
			if j >= 0 && mergeImportedModels(&list[j].Models, g.Models) {
				list[j].Updated = now
			}
			updated = append(updated, g.Existing)
			continue
		}
		for n := 2; taken[g.Name]; n++ {
			g.Name = fmt.Sprintf("%s-%d", name, n)
		}
		l, err := s.sealImported(g, now)
		if err != nil {
			return nil, nil, err
		}
		taken[l.Name] = true
		list = append(list, l)
		created = append(created, l.Name)
	}
	if err := s.storeManagedLaunchers(ctx, list); err != nil {
		return nil, nil, err
	}
	return created, updated, nil
}

func (s *Service) launcherImportRoutes() {
	s.handle("GET /api/v1/launchers/import", func(w http.ResponseWriter, r *http.Request) {
		machine := r.URL.Query().Get("machine")
		if machine == "" {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-models_import.err_machine_required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		groups, err := s.importGroups(ctx, machine)
		if err != nil {
			importErr(w, err)
			return
		}
		providers := make([]importedProvider, 0, len(groups))
		for _, g := range groups {
			providers = append(providers, g.importedProvider) // the key and headers stay in g
		}
		apierr.WriteJSON(w, map[string]any{"machine": machine, "providers": providers})
	})
	s.handle("POST /api/v1/launchers/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Machine   string   `json:"machine"`
			Providers []string `json:"providers"`
		}
		if err := decodeBody(w, r, &in, 64<<10); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		if in.Machine == "" || len(in.Providers) == 0 {
			apierr.Write(w, http.StatusBadRequest, i18n.Err("assistant-models_import.err_machine_providers_required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		groups, err := s.importGroups(ctx, in.Machine)
		if err != nil {
			importErr(w, err)
			return
		}
		created, updated, err := s.importLaunchers(ctx, groups, in.Providers)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errManagedLaunchersRead) {
				status = http.StatusInternalServerError
			}
			apierr.Write(w, status, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"created": nonNil(created), "updated": nonNil(updated)})
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// pickerDefault is GET /api/v1/machines/{mid}/launchers's default: ?purpose= (assistant, or "New Agent"'s agent.new
// when absent).
func (s *Service) pickerDefault(ctx context.Context, r *http.Request) map[string]any {
	purpose := r.URL.Query().Get("purpose")
	if purpose == "" {
		purpose = "agent.new"
	}
	return s.launcherDefault(ctx, purpose)
}

// launcherDefault is what a launcher picker shows as "Default: …": purpose's effective provider (the launcher's name),
// model and effort, and for the main chat its runtime. Nil when purpose is not a launcher use.
func (s *Service) launcherDefault(ctx context.Context, purpose string) map[string]any {
	p, err := s.modelPurpose(ctx, purpose, s.modelProviders(ctx))
	if err != nil || p.Kind != "launcher" {
		return nil
	}
	b := p.Effective
	out := map[string]any{"provider": strings.TrimPrefix(b.Provider, "launcher."), "model": b.Model, "effort": b.Effort}
	if len(p.Runtimes) > 0 {
		runtime := b.Runtime
		if runtime == "" {
			runtime = proto.AssistantRuntimeCLI
		}
		out["runtime"] = runtime
	}
	return out
}
