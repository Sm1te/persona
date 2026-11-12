package assistant

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the app's API ----

type launcherInput struct {
	Kind                   string   `json:"kind"`
	Agent                  string   `json:"agent"`
	API                    string   `json:"api"`
	Name                   string   `json:"name"`
	BaseURL                string   `json:"base_url"`
	Model                  string   `json:"model"`
	SmallModel             string   `json:"small_model"`
	SubagentModel          string   `json:"subagent_model"`
	CustomModel            string   `json:"custom_model"`
	CustomModelName        string   `json:"custom_model_name"`
	CustomModelDescription string   `json:"custom_model_description"`
	Args                   []string `json:"args"`
	APIKey                 *string  `json:"api_key"` // nil or "" on an update keeps the key
	// APIKeyReference is a reference to a provider extension's secret in place of the key (launcher_secrets.go); nil
	// or "" keeps the key, and a key given later replaces it.
	APIKeyReference *string `json:"api_key_reference"`
	Auth            string  `json:"auth"` // api_key (x-api-key, the default) or bearer (Authorization: Bearer)
	// Preset is the provider it is added from (GET /api/v1/launchers/presets): what is left empty is filled in, the
	// name too (POST only).
	Preset string `json:"preset,omitempty"`
	// DisplayName is the name shown, any language; nil on an update keeps it; empty on a provider the hub names from a preset is the preset's name.
	DisplayName *string `json:"display_name"`
	// Assign (POST only): the categories (assistant, agent.new) to put on the new provider, each only while it is
	// still at its default (docs/assistant/onboarding.md 5.4).
	Assign []string `json:"assign,omitempty"`

	// #2455's fields (proto.ManagedLauncher). Each is a pointer: left out (an app from before them) keeps what is
	// stored on an update. Headers "" clears them; like the key they are sealed and never sent back.
	Models        *[]proto.ProviderModel `json:"models"`
	OpusModel     *string                `json:"opus_model"`
	SonnetModel   *string                `json:"sonnet_model"`
	HaikuModel    *string                `json:"haiku_model"`
	FableModel    *string                `json:"fable_model"`
	SubagentForce *bool                  `json:"subagent_force"`
	Headers       *string                `json:"headers"`
	Env           *string                `json:"env"` // one NAME=value a line (#5953); "" clears it; sealed like Headers
	Proxy         *string                `json:"proxy"`
	NoProxy       *string                `json:"no_proxy"`
	TimeoutMS     *int                   `json:"timeout_ms"`
	CompactWindow *int                   `json:"compact_window"`
	MaxContext    *int                   `json:"max_context"`
	Effort        *string                `json:"effort"`

	// The device scope (models_launchers_scope.go); left out keeps what is stored.
	scopeInput

	// keyRefSealed and machineRefsSealed are the references above, checked and sealed (sealKeyReferences).
	keyRefSealed      string
	machineRefsSealed map[string]string
}

// checkEffort: a thinking level Claude Code takes (CLAUDE_CODE_EFFORT_LEVEL), or none. whose is the error's key
// (err_effort_<whose>: default, model, model_default), model the model it names.
func checkEffort(whose, model, e string) error {
	if e == "" || slices.Contains(proto.EffortLevels, e) {
		return nil
	}
	return i18n.Err("assistant-models_launchers.err_effort_"+whose, i18n.Args{"model": model, "effort": strconv.Quote(e), "levels": strings.Join(proto.EffortLevels, " / ")})
}

// checkGateway is a gateway's base URL: http(s), a host, nothing that carries a credential.
func checkGateway(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return i18n.Err("assistant-models_launchers.err_base_url")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return i18n.Err("assistant-models_launchers.err_base_url_parts")
	}
	return nil
}

// checkHeaders: one "Name: value" a line (ANTHROPIC_CUSTOM_HEADERS); the error never quotes a value.
func checkHeaders(raw string) error {
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "" || strings.ContainsAny(strings.TrimSpace(name), " \t") {
			return i18n.Err("assistant-models_launchers.err_header_line", i18n.Args{"line": i + 1})
		}
	}
	return nil
}

// envLimit is how many NAME=value lines a provider's env takes (checkEnv).
const envLimit = 50

// checkEnv: one NAME=value a line ("export " before it is fine), NAME a shell variable's name; a line starting with #
// is a comment. The rules of the node's envLine (node/launcherenv.go) and the web's envProblem. The error never quotes
// a line: a value may be a secret.
func checkEnv(raw string) error {
	lines := 0
	for i, line := range strings.Split(raw, "\n") {
		line = strings.TrimFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' })
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines++
		name, _, ok := strings.Cut(exportPrefix.ReplaceAllString(line, ""), "=")
		if !ok || !envName.MatchString(strings.TrimSpace(name)) || strings.ContainsRune(line, 0) {
			return i18n.Err("assistant-models_launchers.err_env_line", i18n.Args{"line": i + 1})
		}
		if lines > envLimit {
			return i18n.Err("assistant-models_launchers.err_env_limit")
		}
	}
	return nil
}

var exportPrefix = lazyre.New(`^export\s+`)

var envName = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (in launcherInput) checkProvider() error {
	if in.Models != nil {
		seen := map[string]bool{}
		for _, m := range *in.Models {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				return i18n.Err("assistant-models_launchers.err_model_no_id")
			}
			if seen[id] {
				return i18n.Err("assistant-models_launchers.err_model_duplicate", i18n.Args{"model": id})
			}
			seen[id] = true
			if m.ContextWindow < 0 {
				return i18n.Err("assistant-models_launchers.err_model_context_negative", i18n.Args{"model": id})
			}
			for _, e := range m.Efforts {
				if e == "" {
					return i18n.Err("assistant-models_launchers.err_model_effort_empty", i18n.Args{"model": id})
				}
				if err := checkEffort("model", id, e); err != nil {
					return err
				}
			}
			if err := checkEffort("model_default", id, m.DefaultEffort); err != nil {
				return err
			}
			if m.DefaultEffort != "" && len(m.Efforts) > 0 && !slices.Contains(m.Efforts, m.DefaultEffort) {
				return i18n.Err("assistant-models_launchers.err_model_default_effort_unlisted", i18n.Args{"model": id})
			}
		}
	}
	if in.Effort != nil {
		if err := checkEffort("default", "", *in.Effort); err != nil {
			return err
		}
	}
	for _, v := range []struct {
		key   string
		value *int
	}{{"err_timeout_negative", in.TimeoutMS}, {"err_compact_window_negative", in.CompactWindow}, {"err_max_context_negative", in.MaxContext}} {
		if v.value != nil && *v.value < 0 {
			return i18n.Err("assistant-models_launchers." + v.key)
		}
	}
	if in.Proxy != nil && strings.TrimSpace(*in.Proxy) != "" {
		u, err := url.Parse(strings.TrimSpace(*in.Proxy))
		if err != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5", "socks5h"}, u.Scheme) {
			return i18n.Err("assistant-models_launchers.err_proxy")
		}
		// The proxy is stored and shown as is (not sealed): a password in it would go back to every client.
		if u.User != nil {
			return i18n.Err("assistant-models_launchers.err_proxy_credentials")
		}
	}
	if in.Headers != nil {
		if err := checkHeaders(*in.Headers); err != nil {
			return err
		}
	}
	if in.Env != nil {
		if err := checkEnv(*in.Env); err != nil {
			return err
		}
	}
	for _, v := range []*string{in.OpusModel, in.SonnetModel, in.HaikuModel, in.FableModel} {
		if v != nil && len(strings.TrimSpace(*v)) > 200 {
			return i18n.Err("assistant-models_launchers.err_tier_model_long")
		}
	}
	return nil
}

// applyProvider writes the #2455 fields given into s (headers apart: they are sealed by the caller).
func (in launcherInput) applyProvider(s *storedLauncher) {
	str := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	num := func(dst *int, v *int) {
		if v != nil {
			*dst = *v
		}
	}
	if in.Models != nil {
		var models []proto.ProviderModel
		for _, m := range *in.Models {
			m.ID, m.Name = strings.TrimSpace(m.ID), strings.TrimSpace(m.Name)
			models = append(models, m)
		}
		s.Models = models
	}
	str(&s.OpusModel, in.OpusModel)
	str(&s.SonnetModel, in.SonnetModel)
	str(&s.HaikuModel, in.HaikuModel)
	str(&s.FableModel, in.FableModel)
	str(&s.Proxy, in.Proxy)
	str(&s.NoProxy, in.NoProxy)
	str(&s.Effort, in.Effort)
	num(&s.TimeoutMS, in.TimeoutMS)
	num(&s.CompactWindow, in.CompactWindow)
	num(&s.MaxContext, in.MaxContext)
	if in.SubagentForce != nil {
		s.SubagentForce = *in.SubagentForce
	}
}

var errNoPreset = i18n.Err("assistant-models_launchers.err_no_preset")

// maxDisplayName is how long a provider's display name may be, in characters (err_display_name).
const maxDisplayName = 64

// gatewayHost is the host of a provider's Base URL, lower case; "" without one.
func gatewayHost(baseURL string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// onlyHost says whether a bypass list names host and nothing else, as iOS reads No Proxy back.
func onlyHost(list, host string) bool {
	hosts := strings.FieldsFunc(strings.ToLower(list), func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	return host != "" && len(hosts) == 1 && hosts[0] == host
}

func (in launcherInput) check() error {
	if in.Kind != "" && in.Kind != "api" && in.Kind != "launcher" {
		return i18n.Err("assistant-models_launchers.err_kind")
	}
	if in.Agent != "" && in.Agent != "claude" && in.Agent != "codex" && in.Agent != "agy" && in.Agent != "omp" {
		return i18n.Err("assistant-models_launchers.err_kind")
	}
	if in.API != "" && in.API != "messages" && in.API != "responses" && in.API != "chat" {
		return i18n.Err("assistant-models_launchers.err_api")
	}
	if in.Kind != "api" && in.API == "chat" {
		return i18n.Err("assistant-models_launchers.err_chat_launcher")
	}
	if in.Kind != "api" && (in.Agent == "" || in.Agent == "claude") && in.API == "responses" {
		return i18n.Err("assistant-models_launchers.err_claude_messages")
	}
	if in.Auth != "" && in.Auth != "api_key" && in.Auth != "bearer" {
		return i18n.Err("assistant-models_launchers.err_auth")
	}
	if keyGiven(in.APIKey) && keyGiven(in.APIKeyReference) {
		return i18n.Err("assistant-models_launchers.err_key_value_or_reference")
	}
	for m, ref := range in.MachineKeyReferences {
		if keyGiven(ref) && keyGiven(in.MachineKeys[m]) {
			return i18n.Err("assistant-models_launchers.err_key_value_or_reference")
		}
	}
	if in.Kind == "api" && (len(in.Args) != 0 || in.SmallModel != "" || in.SubagentModel != "" || in.CustomModel != "") {
		return i18n.Err("assistant-models_launchers.err_api_launch_fields")
	}
	for _, v := range []struct{ name, value string }{{"err_subagent_model_long", in.SubagentModel}, {"err_custom_model_long", in.CustomModel}} {
		if len(strings.TrimSpace(v.value)) > 200 {
			return i18n.Err("assistant-models_launchers." + v.name)
		}
	}
	if in.CustomModelName == "" && in.CustomModelDescription != "" {
		return i18n.Err("assistant-models_launchers.err_custom_model_name")
	}
	if !managedLauncherName.MatchString(in.Name) || slices.Contains(reservedLauncherNames, in.Name) {
		return i18n.Err("assistant-models_launchers.err_name")
	}
	if in.DisplayName != nil {
		if d := strings.TrimSpace(*in.DisplayName); utf8.RuneCountInString(d) > maxDisplayName || strings.IndexFunc(d, unicode.IsControl) >= 0 {
			return i18n.Err("assistant-models_launchers.err_display_name")
		}
	}
	if strings.TrimSpace(in.BaseURL) != "" || in.Kind == "api" || in.Agent == "" || in.Agent == "claude" {
		if err := checkGateway(in.BaseURL); err != nil {
			return err
		}
	}
	if err := in.checkProvider(); err != nil {
		return err
	}
	if strings.TrimSpace(in.Model) == "" {
		return i18n.Err("assistant-models_launchers.err_model_required")
	}
	for _, a := range in.Args {
		if !strings.HasPrefix(a, "-") {
			return i18n.Err("assistant-models_launchers.err_arg_dash", i18n.Args{"arg": strconv.Quote(a)})
		}
	}
	return nil
}

var (
	errChatProtocolChange = i18n.Err("assistant-models_launchers.err_chat_protocol_change")
	errAPITransport       = i18n.Err("assistant-models_launchers.err_api_transport")
	errChatTransport      = i18n.Err("assistant-models_launchers.err_chat_transport")
	errChatLaunchSettings = i18n.Err("assistant-models_launchers.err_chat_launch_settings")
)

// checkAPITransport rejects settings the manager's direct calls cannot honor. Launcher
// transport settings remain available to the node; an empty kind is a legacy launcher.
func checkAPITransport(kind, api, proxy, noProxy string, hasHeaders bool) error {
	if !hasHeaders && strings.TrimSpace(proxy) == "" && strings.TrimSpace(noProxy) == "" {
		return nil
	}
	if api == "chat" {
		return errChatTransport
	}
	if kind == "api" {
		return errAPITransport
	}
	return nil
}

// saveLauncher stores in (over old on an update) and tells the nodes.
func (s *Service) saveLauncher(ctx context.Context, in launcherInput, old *storedLauncher) (storedLauncher, error) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	list, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return storedLauncher{}, err
	}
	if old == nil && slices.ContainsFunc(list, func(l storedLauncher) bool { return l.Name == in.Name }) {
		return storedLauncher{}, i18n.Err("assistant-models_launchers.err_name_taken", i18n.Args{"name": in.Name})
	}
	// Refresh the stored object under the same lock as profile switches: omitted fields keep today's values.
	if old != nil {
		i := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == in.Name })
		if i < 0 {
			return storedLauncher{}, i18n.Err("assistant-models_launchers.err_not_managed")
		}
		old = &list[i]
		if old.API == "chat" && in.API != "chat" {
			return storedLauncher{}, errChatProtocolChange
		}
	}
	if in.Kind == "api" || in.API == "chat" {
		var proxy, noProxy string
		hasHeaders := false
		if old != nil {
			proxy, noProxy, hasHeaders = old.Proxy, old.NoProxy, old.HeadersSealed != ""
		}
		if in.Proxy != nil {
			proxy = *in.Proxy
		}
		if in.NoProxy != nil {
			noProxy = *in.NoProxy
		}
		if in.Headers != nil {
			hasHeaders = strings.TrimSpace(*in.Headers) != ""
		}
		if err := checkAPITransport(in.Kind, in.API, proxy, noProxy, hasHeaders); err != nil {
			return storedLauncher{}, err
		}
	}
	now := time.Now().UTC()
	l := storedLauncher{Created: now, Updated: now}
	if old != nil {
		l = *old
		l.Updated = now
	}
	l.Name, l.BaseURL, l.Model, l.SmallModel = in.Name, strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"),
		strings.TrimSpace(in.Model), strings.TrimSpace(in.SmallModel)
	l.SubagentModel, l.CustomModel = strings.TrimSpace(in.SubagentModel), strings.TrimSpace(in.CustomModel)
	l.CustomModelName, l.CustomModelDescription = strings.TrimSpace(in.CustomModelName), strings.TrimSpace(in.CustomModelDescription)
	l.Kind, l.API = in.Kind, in.API
	switch {
	case in.Kind == "api" || in.Agent == "claude":
		l.Agent = ""
	case in.Agent != "":
		l.Agent = in.Agent
	} // no agent in the request (an app without it): an update keeps what is stored
	// Stored as the agent reaches it (the address Test connection called): a pasted ".../v1" would run as /v1/v1/messages.
	l.BaseURL = l.LaunchBaseURL()
	l.Args = in.Args
	if in.Preset != "" {
		l.Preset = in.Preset
	}
	if in.DisplayName != nil {
		l.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	switch {
	case in.API == "responses" || in.API == "chat": // always goes as Bearer: nothing to keep
		l.Auth = ""
	case in.Auth == "bearer":
		l.Auth = in.Auth
	case in.Auth == "api_key":
		l.Auth = ""
	} // no auth in the request (an app from before it): a change keeps what is stored
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		sealed, err := s.sealKey(strings.TrimSpace(*in.APIKey))
		if err != nil {
			return l, err
		}
		l.KeySealed = sealed
	}
	if in.keyRefSealed != "" {
		l.KeySealed = in.keyRefSealed
	}
	in.applyProvider(&l)
	// A bypass list that is the gateway's host alone (iOS's Proxy › No Proxy) follows the gateway to a new address: the
	// web form and an import send it back as it was, and the old host would leave the new gateway on the proxy (#6238).
	if old != nil && l.BaseURL != old.BaseURL && onlyHost(l.NoProxy, gatewayHost(old.BaseURL)) {
		l.NoProxy = gatewayHost(l.BaseURL)
	}
	if in.Headers != nil { // left out keeps them, "" clears them
		l.HeadersSealed = ""
		if hd := strings.TrimSpace(*in.Headers); hd != "" {
			sealed, err := s.sealKey(hd)
			if err != nil {
				return l, err
			}
			l.HeadersSealed = sealed
		}
	}
	if in.Env != nil { // left out keeps it, "" clears it
		l.EnvSealed = ""
		if env := strings.TrimSpace(*in.Env); env != "" {
			sealed, err := s.sealKey(env)
			if err != nil {
				return l, err
			}
			l.EnvSealed = sealed
		}
	}
	if l.API == "chat" && (l.EnvSealed != "" || l.TimeoutMS != 0 || l.CompactWindow != 0 || l.MaxContext != 0 ||
		l.Effort != "" || l.OpusModel != "" || l.SonnetModel != "" || l.HaikuModel != "" || l.FableModel != "" || l.SubagentForce) {
		return storedLauncher{}, errChatLaunchSettings
	}
	if err := in.applyScope(&l, old == nil, len(in.Assign) > 0); err != nil {
		return storedLauncher{}, err
	}
	keys, err := s.readMachineKeys(ctx)
	if err != nil {
		return storedLauncher{}, err
	}
	newKeys, _, err := s.withMachineKeys(keys, l.Name, in.MachineKeys, in.MachineKeyReferences, in.machineRefsSealed)
	if err != nil {
		return storedLauncher{}, err
	}
	l.APIKey, l.Headers, l.HasHeaders, l.Env, l.HasEnv = "", "", false, "", false // only ever stored sealed
	l.Withheld, l.Rev = false, ""
	if i := slices.IndexFunc(list, func(x storedLauncher) bool { return x.Name == l.Name }); i >= 0 {
		list[i] = l
	} else {
		list = append(list, l)
	}
	if err := s.storeManagedWithKeys(ctx, list, keys, newKeys, nil); err != nil {
		return l, err
	}
	return l, nil
}

// storeManagedLaunchers commits the complete raw snapshot under models.mu and publishes it when its
// node credentials can be opened. An unrelated corrupt credential defers publication without discarding
// readable records or preventing repair. A post-commit read failure cannot clear the nodes' providers.
func (s *Service) storeManagedLaunchers(ctx context.Context, list []storedLauncher) error {
	return s.storeManagedAnd(ctx, list, nil)
}

// errNoSettingsBatch: the store writes one setting at a time, so storeManagedAnd wrote nothing.
var errNoSettingsBatch = errors.New("the store cannot write several settings in one transaction")

// storeManagedAnd is storeManagedLaunchers that also writes more (other settings) in the same transaction: all of it
// or nothing. A store that cannot is errNoSettingsBatch, nothing written.
func (s *Service) storeManagedAnd(ctx context.Context, list []storedLauncher, more map[string]any) error {
	keys, err := s.readMachineKeys(ctx)
	if err != nil {
		return err
	}
	// a provider's keys for single computers go with it
	kept := slices.DeleteFunc(slices.Clone(keys), func(k machineKey) bool {
		return !slices.ContainsFunc(list, func(l storedLauncher) bool { return l.Name == k.Launcher })
	})
	return s.storeManagedWithKeys(ctx, list, keys, kept, more)
}

// storeManagedWithKeys is storeManagedAnd with the computers' keys as they are to be (old: as read).
func (s *Service) storeManagedWithKeys(ctx context.Context, list []storedLauncher, old, keys []machineKey, more map[string]any) error {
	_, publicationErr := s.nodeLaunchers(list)
	if publicationErr == nil {
		for _, k := range keys {
			if _, err := s.openKey(k.KeySealed); err != nil {
				publicationErr = i18n.Err("assistant-models.err_key_decrypt")
				break
			}
		}
	}
	if keys == nil {
		keys = []machineKey{}
	}
	if len(more) > 0 {
		batch, ok := s.store().(interface {
			PutSettings(context.Context, map[string]any) error
		})
		if !ok {
			return errNoSettingsBatch
		}
		writes := maps.Clone(more)
		writes[launchersManagedKey] = list
		if !slices.Equal(old, keys) {
			writes[launchersMachineKeysKey] = keys
		}
		if err := batch.PutSettings(ctx, writes); err != nil {
			return err
		}
	} else {
		if !slices.Equal(old, keys) {
			if err := s.putSetting(ctx, launchersMachineKeysKey, keys); err != nil {
				return err
			}
		}
		if err := s.putSetting(ctx, launchersManagedKey, list); err != nil {
			return err
		}
	}
	if publicationErr != nil {
		s.logf("launchers: saved providers; node publication deferred: %v", publicationErr)
		return nil
	}
	s.pushNodeLaunchers(ctx, "", list, keys)
	return nil
}

// keyGiven: a key or reference that is there (nil and blank are not).
func keyGiven(v *string) bool { return v != nil && strings.TrimSpace(*v) != "" }
