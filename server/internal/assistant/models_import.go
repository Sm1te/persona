package assistant

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
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

// Importing shell aliases as managed providers (#2455, #5024, docs/assistant/model-config.md "Importing providers from
// shell aliases"): the node's launchers.zshrc reads a machine's `alias x="K=V … claude [args]"` lines from ~/.zshrc,
// ~/.bashrc and ~/.bash_aliases with their env (keys included, only over this RPC); the hub groups them by base URL +
// key into providers, or makes one provider of each alias (split). The preview never carries a key, headers or an env
// value; importing reads them again and keeps them in the hub, sealed.

// zshrcAlias is one alias launchers.zshrc returns; File is the shell file's label ("" from an older node: ~/.zshrc).
type zshrcAlias struct {
	Alias   string            `json:"alias"`
	Env     map[string]string `json:"env"`
	Args    []string          `json:"args,omitempty"`
	Command string            `json:"command"`
	File    string            `json:"file,omitempty"`
}

// importedProvider is one provider of the import preview: the launcher it would save, without the key, headers and env.
type importedProvider struct {
	proto.ManagedLauncher
	HasKey bool `json:"has_key"`
	// HasEnv: it brings variables of the alias as its sealed env (EnvNames); SkippedEnv are the ones left out.
	HasEnv     bool         `json:"has_env"`
	EnvNames   []string     `json:"env_names,omitempty"`
	SkippedEnv []skippedEnv `json:"skipped_env,omitempty"`
	Aliases    []string     `json:"aliases"`
	// Existing: the stored provider it lands in: the same base URL and key; for a variant, also made from its alias.
	Existing string `json:"existing,omitempty"`
	// CreateAs: the name a provider it creates is saved under when that is not Name (the name is taken: the alias
	// itself is listed, importNames). Name stays what POST takes.
	CreateAs string         `json:"create_as,omitempty"`
	Sources  []importSource `json:"sources"` // one per alias, in Aliases' order
	// Differs: what the aliases of a group set differently, the model aside (importSettings' names); Variants: each
	// alias as its own provider, for a group of two or more.
	Differs  []string           `json:"differs,omitempty"`
	Variants []importedProvider `json:"variants,omitempty"`
}

// importGroup is a provider made of aliases, with its secrets (the manager's only), and each alias as a provider of
// its own (variants, for a group of two or more).
type importGroup struct {
	importedProvider
	key, headers, env string
	variants          []importGroup
}

// preview is g as the preview sends it.
func (g importGroup) preview() importedProvider {
	p := g.importedProvider
	p.Variants = nil
	for _, v := range g.variants {
		p.Variants = append(p.Variants, v.importedProvider)
	}
	return p
}

// knownProvider is a stored provider as the merge sees it: name, base URL, opened key and where it came from.
type knownProvider struct{ name, baseURL, key, source string }

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
// the first alias names it and gives its settings, every alias's ANTHROPIC_MODEL joins its models. A group of two or
// more also has each alias as a provider of its own (variants) and says what they set differently (Differs). Aliases
// without a base URL (plain Claude Code) and ones not running claude are skipped. known marks the ones already stored.
func mergeZshrcAliases(machine string, aliases []zshrcAlias, known []knownProvider) []importGroup {
	var out []importGroup
	for _, a := range aliases {
		if a.Command != "" && a.Command != "claude" {
			continue
		}
		base := proto.MessagesBaseURL(a.Env["ANTHROPIC_BASE_URL"]) // stored as a saved provider is (saveLauncher)
		if base == "" {
			continue
		}
		v := newImportGroup(machine, a, base)
		i := slices.IndexFunc(out, func(g importGroup) bool { return g.BaseURL == base && g.key == v.key })
		if i < 0 {
			g := v
			g.Models, g.Aliases, g.Sources = slices.Clone(v.Models), slices.Clone(v.Aliases), slices.Clone(v.Sources)
			g.variants = []importGroup{v}
			out = append(out, g)
			continue
		}
		out[i].Aliases = append(out[i].Aliases, a.Alias)
		out[i].Sources = append(out[i].Sources, v.Sources...)
		for _, m := range v.Models {
			addImportedModel(&out[i].Models, m)
		}
		out[i].variants = append(out[i].variants, v)
	}
	for i := range out {
		if len(out[i].variants) < 2 {
			out[i].variants = nil
			continue
		}
		out[i].Differs = importDiffers(out[i].variants)
	}
	landExisting(out, known)
	return out
}

// newImportGroup is alias a as a provider of its own: every setting of its env, its model as its models, the
// variables Persona has no field for as its env (importEnv).
func newImportGroup(machine string, a zshrcAlias, base string) importGroup {
	env := a.Env
	key, auth := importKey(env)
	tier := func(k string) string { id, _ := strip1M(env[k]); return id }
	model, window := strip1M(env["ANTHROPIC_MODEL"])
	proxy, noProxy := importProxy(env)
	force := strings.TrimSpace(env["CLAUDE_CODE_SUBAGENT_MODEL_FORCE"])
	g := importGroup{key: key, headers: strings.TrimSpace(env["ANTHROPIC_CUSTOM_HEADERS"])}
	g.env, g.EnvNames, g.SkippedEnv = importEnv(env)
	g.ManagedLauncher = proto.ManagedLauncher{
		Name: importName(a.Alias), BaseURL: base, Auth: auth, Model: model, Args: a.Args,
		OpusModel: tier("ANTHROPIC_DEFAULT_OPUS_MODEL"), SonnetModel: tier("ANTHROPIC_DEFAULT_SONNET_MODEL"),
		HaikuModel: tier("ANTHROPIC_DEFAULT_HAIKU_MODEL"), FableModel: tier("ANTHROPIC_DEFAULT_FABLE_MODEL"),
		SmallModel: tier("ANTHROPIC_SMALL_FAST_MODEL"), SubagentModel: tier("CLAUDE_CODE_SUBAGENT_MODEL"),
		SubagentForce:          force == "1" || strings.EqualFold(force, "true"),
		CustomModel:            strings.TrimSpace(env["ANTHROPIC_CUSTOM_MODEL_OPTION"]),
		CustomModelName:        strings.TrimSpace(env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"]),
		CustomModelDescription: strings.TrimSpace(env["ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION"]),
		Effort:                 strings.TrimSpace(env["CLAUDE_CODE_EFFORT_LEVEL"]),
		CompactWindow:          envInt(env, "CLAUDE_CODE_AUTO_COMPACT_WINDOW"), MaxContext: envInt(env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS"),
		TimeoutMS: envInt(env, "API_TIMEOUT_MS"), Proxy: proxy, NoProxy: noProxy,
		HasHeaders: g.headers != "", Source: "zshrc:" + a.Alias + "@" + machine,
		// The alias makes its computer count as having a service of its own: skip would keep the provider off it.
		OwnService: proto.OwnServiceUse,
	}
	if model != "" {
		g.Models = []proto.ProviderModel{{ID: model, ContextWindow: window}}
	}
	g.HasKey, g.HasEnv, g.Aliases = key != "", g.env != "", []string{a.Alias}
	g.Sources = []importSource{{Alias: a.Alias, File: cmp.Or(a.File, defaultRCFile)}}
	return g
}

// importKey is an alias's key and how it is sent: ANTHROPIC_AUTH_TOKEN (without "Bearer ") as bearer, else
// ANTHROPIC_API_KEY.
func importKey(env map[string]string) (key, auth string) {
	if token := strings.TrimSpace(env["ANTHROPIC_AUTH_TOKEN"]); token != "" {
		return strings.TrimSpace(strings.TrimPrefix(token, "Bearer ")), "bearer"
	}
	return strings.TrimSpace(env["ANTHROPIC_API_KEY"]), "api_key"
}

// existingFor is the stored provider an import lands in: the same base URL and key, and for one alias's own
// provider (alias not ""), made from that alias on any computer (its source).
func existingFor(known []knownProvider, base, key, alias string) string {
	for _, k := range known {
		if k.baseURL != base || k.key != key {
			continue
		}
		if alias == "" || strings.HasPrefix(k.source, "zshrc:"+alias+"@") {
			return k.name
		}
	}
	return ""
}

// landExisting sets where groups and their variants land (existingFor): a variant by its alias too.
func landExisting(groups []importGroup, known []knownProvider) {
	for i := range groups {
		g := &groups[i]
		g.Existing = existingFor(known, g.BaseURL, g.key, "")
		for j := range g.variants {
			v := &g.variants[j]
			v.Existing = existingFor(known, v.BaseURL, v.key, v.Aliases[0])
		}
	}
}

// importSettings are the settings the aliases of a group may set differently, the model aside, in the preview's
// order (Differs): a merged group keeps the first alias's.
var importSettings = []struct {
	name string
	same func(a, b importGroup) bool
}{
	{"tiers", func(a, b importGroup) bool {
		return a.OpusModel == b.OpusModel && a.SonnetModel == b.SonnetModel && a.HaikuModel == b.HaikuModel &&
			a.FableModel == b.FableModel && a.SmallModel == b.SmallModel && a.SubagentModel == b.SubagentModel &&
			a.SubagentForce == b.SubagentForce
	}},
	{"effort", func(a, b importGroup) bool { return a.Effort == b.Effort }},
	{"args", func(a, b importGroup) bool { return slices.Equal(a.Args, b.Args) }},
	{"limits", func(a, b importGroup) bool {
		return a.CompactWindow == b.CompactWindow && a.MaxContext == b.MaxContext && a.TimeoutMS == b.TimeoutMS
	}},
	{"proxy", func(a, b importGroup) bool { return a.Proxy == b.Proxy && a.NoProxy == b.NoProxy }},
	{"headers", func(a, b importGroup) bool { return a.headers == b.headers }},
	{"env", func(a, b importGroup) bool { return a.env == b.env }},
	{"auth", func(a, b importGroup) bool { return a.Auth == b.Auth }},
	{"custom_model", func(a, b importGroup) bool {
		return a.CustomModel == b.CustomModel && a.CustomModelName == b.CustomModelName &&
			a.CustomModelDescription == b.CustomModelDescription
	}},
}

// importDiffers is what the aliases (each as its own provider) set differently, the model aside.
func importDiffers(variants []importGroup) []string {
	var out []string
	for _, setting := range importSettings {
		if slices.ContainsFunc(variants[1:], func(v importGroup) bool { return !setting.same(variants[0], v) }) {
			out = append(out, setting.name)
		}
	}
	return out
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

// zshrcAliases asks machine's node for the claude aliases of its shell files (a var so tests give their own).
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

// importGroups is machine's aliases merged against the stored providers, and those providers.
func (s *Service) importGroups(ctx context.Context, machine string) ([]importGroup, []storedLauncher, error) {
	aliases, err := zshrcAliases(ctx, s, machine)
	if err != nil {
		return nil, nil, err
	}
	s.models.mu.Lock()
	stored, err := s.readStoredLaunchers(ctx)
	s.models.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	known, err := s.knownProviders(stored)
	if err != nil {
		return nil, nil, err
	}
	return mergeZshrcAliases(machine, aliases, known), stored, nil
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
		known = append(known, knownProvider{name: l.Name, baseURL: l.LaunchBaseURL(), key: key, source: l.Source})
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

// importedLauncher is g as a new stored launcher, its secrets not yet sealed: every computer, on demand, and taken on
// a computer with a service of its own (the alias there makes it count as having one).
func importedLauncher(g importGroup, now time.Time) storedLauncher {
	l := storedLauncher{ManagedLauncher: g.ManagedLauncher, Created: now, Updated: now}
	l.HasHeaders, l.HasEnv = false, false // the preview's; a stored launcher's come from its sealed ones
	use := proto.OwnServiceUse
	_ = scopeInput{OwnService: &use}.applyScope(&l, true, false) // docs/integrations/launchers.md "Device scope and on-demand delivery"
	if l.Auth == "api_key" {
		l.Auth = "" // stored as the default
	}
	l.APIKey, l.Headers, l.Env = "", "", "" // only ever stored sealed
	return l
}

// sealImported makes g a stored launcher: the key, headers and env sealed.
func (s *Service) sealImported(g importGroup, now time.Time) (storedLauncher, error) {
	l := importedLauncher(g, now)
	for _, secret := range []struct {
		value string
		to    *string
	}{{g.key, &l.KeySealed}, {g.headers, &l.HeadersSealed}, {g.env, &l.EnvSealed}} {
		if secret.value == "" {
			continue
		}
		sealed, err := s.sealKey(secret.value)
		if err != nil {
			return l, err
		}
		*secret.to = sealed
	}
	return l, nil
}

// importTaken are the names a provider an import creates cannot take: every launcher Persona lists (the rc-file ones
// of every computer, the stored providers).
func (s *Service) importTaken(ctx context.Context, rc []zshrcLaunchers, stored []storedLauncher) map[string]bool {
	taken := map[string]bool{}
	for _, v := range s.launcherViewsFrom(ctx, rc, stored) {
		taken[v.Name] = true
	}
	return taken
}

// claimImportName is the name a new provider called name is saved under: the first of name, name-2, name-3… not
// taken, which is taken from then on.
func claimImportName(taken map[string]bool, name string) string {
	out := name
	for n := 2; taken[out]; n++ {
		out = fmt.Sprintf("%s-%d", name, n)
	}
	taken[out] = true
	return out
}

// importNames names what an import of groups would create (CreateAs, when it is not Name), the one rule of the preview
// and the import: groups in the preview's order, against taken (importTaken); a group and its variants each against
// what earlier groups may take (the user picks merged or split later), and every name a group may take is taken for
// the groups after it. What lands in an existing provider is named nothing.
func importNames(groups []importGroup, taken map[string]bool) {
	name := func(taken map[string]bool, g *importGroup) {
		g.CreateAs = ""
		if g.Existing == "" {
			if n := claimImportName(taken, g.Name); n != g.Name {
				g.CreateAs = n
			}
		}
	}
	for i := range groups {
		g := &groups[i]
		merged, split := maps.Clone(taken), maps.Clone(taken)
		name(merged, g)
		for j := range g.variants {
			name(split, &g.variants[j])
		}
		maps.Copy(taken, merged)
		maps.Copy(taken, split)
	}
}

// importTarget is the provider g lands in: the stored one it merges into, else the one it makes.
func importTarget(g importGroup, stored []storedLauncher) storedLauncher {
	if i := slices.IndexFunc(stored, func(l storedLauncher) bool { return l.Name == g.Existing }); g.Existing != "" && i >= 0 {
		return stored[i]
	}
	return importedLauncher(g, time.Time{})
}

// withImportSources fills the hide check of g's aliases and its variants' (aliasHide), against what they land in.
func (s *Service) withImportSources(ctx context.Context, f importHideFacts, g *importGroup) error {
	target := importTarget(*g, f.stored)
	for i, src := range g.Sources {
		checked, err := s.aliasHide(ctx, f, src.Alias, target, g.key)
		if err != nil {
			return err
		}
		checked.File = src.File
		g.Sources[i] = checked
	}
	for i := range g.variants {
		if err := s.withImportSources(ctx, f, &g.variants[i]); err != nil {
			return err
		}
	}
	return nil
}

// withoutHide answers every alias of g and its variants hide none: the preview could not tell.
func withoutHide(g *importGroup) {
	for i := range g.Sources {
		g.Sources[i] = importSource{Alias: g.Sources[i].Alias, File: g.Sources[i].File, Hide: hideNone}
	}
	for i := range g.variants {
		withoutHide(&g.variants[i])
	}
}

// importPreviewSources fills the preview's hide checks. Only when Persona lists one of the aliases are the facts read
// at all; when they cannot be read, or a check fails, the preview still answers, every alias none (logged): the
// preview never fails over hiding, the import refuses to hide what it cannot check.
func (s *Service) importPreviewSources(ctx context.Context, machine string, rc []zshrcLaunchers, stored []storedLauncher, groups []importGroup) {
	listed := importHideFacts{rc: rc, stored: stored}
	if slices.ContainsFunc(groups, func(g importGroup) bool { return slices.ContainsFunc(g.Aliases, listed.listed) }) {
		f, err := s.importHideFacts(ctx, machine, rc, stored)
		for i := 0; err == nil && i < len(groups); i++ {
			err = s.withImportSources(ctx, f, &groups[i])
		}
		if err == nil {
			return
		}
		s.logf("launchers import: %s: hide check: %v", machine, err)
	}
	for i := range groups {
		withoutHide(&groups[i])
	}
}

// importPicks are the groups in.Providers names, in that order and each once, a group in.Split names as its variants
// (one provider per alias); a name that is no group, or a split one that is not picked, is an error.
func importPicks(groups []importGroup, in importBody) ([]importGroup, error) {
	for _, name := range in.Split {
		if !slices.Contains(in.Providers, name) {
			return nil, i18n.Err("assistant-models_import.err_nothing_named", i18n.Args{"name": name})
		}
	}
	var out []importGroup
	for k, name := range in.Providers {
		i := slices.IndexFunc(groups, func(g importGroup) bool { return g.Name == name })
		if i < 0 {
			return nil, i18n.Err("assistant-models_import.err_nothing_named", i18n.Args{"name": name})
		}
		if slices.Contains(in.Providers[:k], name) {
			continue // named twice: imported once
		}
		if slices.Contains(in.Split, name) && len(groups[i].variants) > 0 {
			out = append(out, groups[i].variants...)
		} else {
			out = append(out, groups[i])
		}
	}
	return out, nil
}

// importRemoteAliases asks, before the import takes the lock, the other computers its hide check will ask: the same
// check (aliasHide) on what it would import as things are now, its answers kept. Under the lock the check reads only
// these. Nothing when it is not hiding, or what the check reads cannot be read (the import then refuses anyway).
func (s *Service) importRemoteAliases(ctx context.Context, groups []importGroup, in importBody, rc []zshrcLaunchers, stored []storedLauncher) map[string]remoteAliases {
	picks, err := importPicks(groups, in)
	if !in.HideAliases || err != nil {
		return nil
	}
	ctx = withSettingsMemo(ctx) // read-only
	f, err := s.importHideFacts(ctx, in.Machine, rc, stored)
	if err != nil {
		return nil
	}
	for _, g := range picks {
		target := importTarget(g, stored)
		for _, alias := range g.Aliases {
			if _, err := s.aliasHide(ctx, f, alias, target, g.key); err != nil {
				return f.remote
			}
		}
	}
	return f.remote
}

// errImportNotHidden: the providers were saved but launchers.hidden was not (a store that writes one setting at a time).
var errImportNotHidden = i18n.Err("assistant-models_import.err_not_hidden")

// importBody is POST /api/v1/launchers/import's body: the groups to import (by name), the ones of them to import
// as one provider per alias, and whether to hide the aliases where that is safe. A body without split or
// hide_aliases (an older app) merges every group and hides nothing.
type importBody struct {
	Machine     string   `json:"machine"`
	Providers   []string `json:"providers"`
	Split       []string `json:"split"`
	HideAliases bool     `json:"hide_aliases"`
}

// importResult is what an import did: the providers created and updated, the aliases hidden, and the ones that
// stay with why (hide in_use or elsewhere).
type importResult struct {
	Created []string       `json:"created"`
	Updated []string       `json:"updated"`
	Hidden  []string       `json:"hidden"`
	Kept    []importSource `json:"kept"`
}

// importLaunchers saves the picked groups: new ones created, ones already stored get the new models only; a split
// group the same way for each of its aliases. With hide, under the same lock, each imported alias whose hide check
// against the providers as saved is ok joins launchers.hidden.
// rc (s.zshrcLaunchers) and remote (other computers' aliases, importRemoteAliases) are read before the lock: under
// it no node is asked, and a computer not asked before counts as elsewhere.
func (s *Service) importLaunchers(ctx context.Context, groups []importGroup, in importBody, rc []zshrcLaunchers, remote map[string]remoteAliases) (importResult, error) {
	res := importResult{Created: []string{}, Updated: []string{}, Hidden: []string{}, Kept: []importSource{}}
	if _, err := importPicks(groups, in); err != nil {
		return res, err
	}
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	list, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return res, err
	}
	known, err := s.knownProviders(list)
	if err != nil {
		return res, err
	}
	// The preview may precede another mutation: where each lands and the names it takes are worked out again against
	// today's checked snapshot, by the preview's rules.
	landExisting(groups, known)
	importNames(groups, s.importTaken(ctx, rc, list))
	now := time.Now().UTC()
	type landing struct{ alias, provider, key string }
	var landed []landing
	picks, _ := importPicks(groups, in) // the names were checked above; the groups now as landExisting and importNames left them
	for _, g := range picks {
		target := g.Existing
		if target != "" {
			j := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == g.Existing })
			if j >= 0 && mergeImportedModels(&list[j].Models, g.Models) {
				list[j].Updated = now
			}
			if !slices.Contains(res.Updated, target) {
				res.Updated = append(res.Updated, target)
			}
		} else {
			g.Name = cmp.Or(g.CreateAs, g.Name)
			l, err := s.sealImported(g, now)
			if err != nil {
				return res, err
			}
			list = append(list, l)
			res.Created = append(res.Created, l.Name)
			target = l.Name
		}
		for _, a := range g.Aliases {
			landed = append(landed, landing{a, target, g.key})
		}
	}
	if in.HideAliases {
		// Checked against the list as it is saved, before it is: a failed check leaves everything as it was. Nothing is
		// written until the checks are done, so they share one read of each setting.
		hctx := withSettingsMemo(ctx)
		f, err := s.importHideFacts(hctx, in.Machine, rc, list)
		if err != nil {
			return res, err
		}
		f.ask, f.remote = false, maps.Clone(remote) // no node is asked under the lock
		if f.remote == nil {
			f.remote = map[string]remoteAliases{}
		}
		for _, a := range landed {
			j := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == a.provider })
			if j < 0 {
				continue
			}
			src, err := s.aliasHide(hctx, f, a.alias, list[j], a.key)
			if err != nil {
				return res, err
			}
			switch src.Hide {
			case hideOK:
				res.Hidden = append(res.Hidden, a.alias)
			case hideInUse, hideElsewhere:
				res.Kept = append(res.Kept, src)
			}
		}
	}
	if len(res.Hidden) == 0 {
		return res, s.storeManagedLaunchers(ctx, list)
	}
	// launchers.hidden only: the shell files stay as they are, and Hidden restores an alias. Read again here, not
	// from the checks' memo: under the lock, so no other hide or restore is lost.
	hidden, err := s.readHiddenLaunchers(ctx)
	if err != nil {
		return res, err
	}
	for _, a := range res.Hidden {
		if !slices.Contains(hidden, a) {
			hidden = append(hidden, a)
		}
	}
	err = s.storeManagedAnd(ctx, list, map[string]any{launchersHiddenKey: hidden}) // one transaction
	if errors.Is(err, errNoSettingsBatch) {
		if err := s.storeManagedLaunchers(ctx, list); err != nil {
			return res, err
		}
		if err := s.putSetting(ctx, launchersHiddenKey, hidden); err != nil {
			s.logf("launchers import: providers saved, aliases not hidden: %v", err)
			return res, errImportNotHidden
		}
		return res, nil
	}
	return res, err
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
		groups, stored, err := s.importGroups(ctx, machine)
		if err != nil {
			importErr(w, err)
			return
		}
		ctx = withSettingsMemo(ctx) // read-only from here: every alias's check resolves the same purposes
		rc := s.zshrcLaunchers(ctx, false)
		importNames(groups, s.importTaken(ctx, rc, stored))
		s.importPreviewSources(ctx, machine, rc, stored, groups)
		providers := make([]importedProvider, 0, len(groups))
		for _, g := range groups {
			providers = append(providers, g.preview()) // the key, headers and env stay in g
		}
		apierr.WriteJSON(w, map[string]any{"machine": machine, "providers": providers})
	})
	s.handle("POST /api/v1/launchers/import", func(w http.ResponseWriter, r *http.Request) {
		var in importBody
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
		groups, stored, err := s.importGroups(ctx, in.Machine)
		if err != nil {
			importErr(w, err)
			return
		}
		// What the nodes say is asked first, without the configuration lock.
		rc := s.zshrcLaunchers(ctx, false)
		remote := s.importRemoteAliases(ctx, groups, in, rc, stored)
		res, err := s.importLaunchers(ctx, groups, in, rc, remote)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, errManagedLaunchersRead) || errors.Is(err, errImportNotHidden) {
				status = http.StatusInternalServerError
			}
			apierr.Write(w, status, err)
			return
		}
		apierr.WriteJSON(w, res)
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
