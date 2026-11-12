package assistant

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/i18n"
)

func (s *Service) modelBaseline(ctx context.Context, p ModelPurpose) (ModelBinding, string, error) {
	l := s.requestCtxLang(ctx)
	if p.Parent != "" {
		b, err := s.resolveModel(ctx, p.Parent)
		return b, p.Parent, err
	}
	if p.ID == "agent.delegate" { // "Sorting"'s connection, with a stronger model on it (delegateModel)
		b, err := s.resolveModel(ctx, "inbox.triage")
		if err == nil {
			b = s.delegateBaseline(ctx, b)
		}
		return b, "inbox.triage", err // inheritedLabel names the connection and the model
	}
	if p.Kind == "api" {
		provider := "legacy.ai"
		if p.ID == "inbox.triage" {
			provider = "legacy.triage"
		}
		if p.ID == "kb" {
			provider = "legacy.kb"
		}
		c := s.legacyModelConfig(ctx, provider)
		if c.BaseURL == "" {
			// No hub.json connection for this use: fall back to a provider the assistant can call itself, instead of
			// failing while a keyed Claude connection sits unused (#388). Not stored, so a later explicit choice still
			// wins.
			if name := s.fallbackAPIProvider(ctx); name != "" {
				return ModelBinding{Provider: "launcher." + name, Effort: c.ReasoningEffort}, mc(l, modelAutoSource), nil
			}
		}
		return ModelBinding{Provider: provider, Model: c.Model, Effort: c.ReasoningEffort}, mc(l, "source_hub_config"), nil
	}
	if p.ID == "assistant" { // the assistant's own launcher (its settings, else hub.json's)
		return ModelBinding{Provider: "launcher." + s.settings(ctx).Launcher}, mc(l, "source_assistant_default"), nil
	}
	if p.Kind == "launcher" {
		return ModelBinding{Provider: "launcher.claude"}, mc(l, "source_default"), nil
	}
	return ModelBinding{Provider: p.ID}, mc(l, "source_extension_default"), nil
}

// delegateModel is what "Answering agents' questions" runs on unless the user picks another (2026-11-05, the user's call): it decides for the
// user whether an agent's question is theirs, a cheap triage model is not up to that.
const delegateModel = "claude-opus-5-5"

// delegateBaseline is "Sorting"'s binding b with delegateModel on it when its connection serves Claude models (its
// model is one); b as it is otherwise: a connection to another vendor's models, or one that cannot be called, keeps
// its own model.
func (s *Service) delegateBaseline(ctx context.Context, b ModelBinding) ModelBinding {
	if b.Provider == "" {
		return b
	}
	c, err := s.apiConfig(ctx, b)
	if err != nil || (c.API != "" && c.API != "messages") || !strings.HasPrefix(c.Model, "claude-") {
		return b
	}
	b.Model, b.Effort = delegateModel, ""
	return b
}

// modelAutoSource is (the catalog name of) what modelBaseline calls the default of an API use that runs on
// fallbackAPIProvider's pick; "Inherited from" names the pick (inheritedLabel).
const modelAutoSource = "source_auto"

// inheritedLabel is what "Inherited from" says for p, whose default is base (from: what modelBaseline calls it). Nothing when
// p picks its own connection: none of it is inherited. Where the default is not a fixed place it names what it
// actually runs on: "Answering agents' questions"'s connection and the model on it (Opus only when that connection serves Claude models,
// delegateBaseline), the connection an unconfigured API use falls back to.
func inheritedLabel(l i18n.Lang, p ModelPurpose, base ModelBinding, from string, providers []ModelProvider) string {
	if p.Override.Provider != "" {
		return ""
	}
	name, model := base.Provider, base.Model
	for _, v := range providers {
		if v.ID == base.Provider {
			name, model = cmp.Or(v.Name, v.ID), cmp.Or(model, v.Model)
			break
		}
	}
	switch {
	case p.ID == "agent.delegate" && (p.Override.Model != "" || model == ""):
		return mc(l, "source_delegate_connection", i18n.Args{"provider": name}) // the model is the user's pick, or the connection's own
	case p.ID == "agent.delegate":
		return mc(l, "source_delegate_default", i18n.Args{"provider": name, "model": model})
	case ownModelRecord(p.ID), isExtensionPurpose(p.ID):
		return purposeName(l, p.Parent)
	case from == mc(l, modelAutoSource):
		return mc(l, "source_auto_named", i18n.Args{"provider": name})
	}
	return from
}

// modelNoAPIStatus explains an API use with nothing to call: launchers like "Claude Code default" only start a CLI on a
// computer, so they never show up for these uses. A catalog name.
const modelNoAPIStatus = "status_no_api"

// modelUnverified is the status of a use nothing is known to be wrong with. Not translated: the apps compare it to
// this text (iOS ModelConfigurationView, Android HttpVoiceExtensions) to leave it out.
const modelUnverified = "未验证"

// messagesAPI includes the legacy empty value for the Messages protocol.
func messagesAPI(api string) bool { return api == "" || api == "messages" }

// fallbackAPIProvider names the managed connection an unconfigured API use runs on: the first direct API provider,
// else the first keyed launcher speaking Messages. Other launcher protocols cannot serve these uses. Hidden ones and
// ones without an address, a default model or a key are skipped. "" when there is none.
func (s *Service) fallbackAPIProvider(ctx context.Context) string {
	stored := s.storedLaunchers(ctx)
	hidden := s.hiddenLaunchers(ctx)
	usable := func(l storedLauncher) bool {
		return !slices.Contains(hidden, l.Name) && l.BaseURL != "" && l.Model != "" && l.KeySealed != ""
	}
	for _, l := range stored {
		if l.Kind == "api" && usable(l) {
			return l.Name
		}
	}
	for _, l := range stored {
		if l.Kind != "api" && messagesAPI(l.API) && usable(l) {
			return l.Name
		}
	}
	return ""
}

// mergeModel puts over's choices on base: another provider starts from nothing (its model and effort are its own).
func mergeModel(base, over ModelBinding) ModelBinding {
	if over.Provider != "" && over.Provider != base.Provider {
		base = ModelBinding{Provider: over.Provider, Runtime: base.Runtime}
	}
	if over.Model != "" {
		base.Model = over.Model
	}
	if over.Effort != "" {
		base.Effort = over.Effort
	}
	if over.Runtime != "" {
		base.Runtime = over.Runtime
	}
	return base
}

// effectiveModel applies purpose defaults after merging, so choosing another provider cannot clear triage's default.
func effectiveModel(p ModelPurpose, base, over ModelBinding) ModelBinding {
	b := mergeModel(base, over)
	if p.ID == "inbox.triage" && b.Effort == "" {
		b.Effort = "off"
	}
	return b
}

// resolveModel reads category defaults, except the opt-in overrides of Agents (agentsOverride), a task's own and
// independent plugin models.
func (s *Service) resolveModel(ctx context.Context, id string) (ModelBinding, error) {
	return s.resolveModelRecord(ctx, id, nil)
}

// resolveModelRecord is resolveModel on id's record r when the caller has read it already (nil: it reads it).
func (s *Service) resolveModelRecord(ctx context.Context, id string, r *modelRecord) (ModelBinding, error) {
	p, err := s.modelPurposeDef(ctx, id)
	if err != nil {
		return ModelBinding{}, err
	}
	if recordID := modelRecordID(p); recordID != id { // r is id's, not recordID's
		return s.resolveModel(ctx, recordID)
	}
	base, _, err := s.modelBaseline(ctx, p)
	if err != nil {
		return base, err
	}
	if r == nil {
		read, err := s.modelRecord(ctx, id)
		if err != nil {
			return base, i18n.Err("assistant-models.err_read_config", i18n.Args{"error": err.Error()})
		}
		r = &read
	}
	return effectiveModel(p, base, r.Binding), nil
}

func launcherProvider(name string) string {
	if name == "" {
		return ""
	}
	return "launcher." + name
}

// apiConfig is the connection an API binding calls, with its key opened.
func (s *Service) apiConfig(ctx context.Context, b ModelBinding) (ai.Config, error) {
	var c ai.Config
	if strings.HasPrefix(b.Provider, "legacy.") {
		c = s.legacyModelConfig(ctx, b.Provider)
	} else if name, ok := strings.CutPrefix(b.Provider, "launcher."); ok {
		found := false
		for _, l := range s.storedLaunchers(ctx) {
			if l.Name == name {
				if l.Kind != "api" && !messagesAPI(l.API) {
					return c, i18n.Err("assistant-models.err_launcher_not_responses")
				}
				if err := checkAPITransport(l.Kind, l.API, l.Proxy, l.NoProxy, l.HeadersSealed != ""); err != nil {
					return c, err
				}
				c = ai.Config{API: l.API, BaseURL: l.BaseURL, Model: l.Model, Bearer: l.BearerAuth(), ViaMachine: l.ViaMachine}
				key, err := s.openLauncherKey(ctx, l.KeySealed, l.Name, "")
				if err != nil {
					return c, err
				}
				c.APIKey = key
				found = true
				break
			}
		}
		if !found {
			return c, i18n.Err("assistant-models.err_not_callable")
		}
	} else {
		return c, i18n.Err("assistant-models.err_not_api")
	}
	if b.Model != "" {
		c.Model = b.Model
	}
	c.ReasoningEffort = b.Effort
	if c.API != "" && c.API != "messages" && c.API != "responses" && c.API != "chat" {
		return c, i18n.Err("assistant-models.err_api_protocol")
	}
	if c.API != "responses" && c.API != "messages" && c.API != "" && b.Effort != "" {
		c.ReasoningEffort = ""
	}
	return c, nil
}

// purposeModel is purpose id's model, resolved at the start of every request.
func (s *Service) purposeModel(id string) *ai.Client {
	return ai.NewResolved(func(ctx context.Context) (*ai.Client, error) {
		b, err := s.resolveModel(ctx, id)
		if err != nil {
			return nil, err
		}
		c, err := s.apiConfig(ctx, b)
		if err != nil {
			return nil, err
		}
		return s.aiClient(c), nil
	})
}

// modelUsable says whether an API binding can be called: it resolves to an address, a model and a key.
func (s *Service) modelUsable(ctx context.Context, b ModelBinding) bool {
	c, err := s.apiConfig(ctx, b)
	return err == nil && c.Configured()
}

func (s *Service) modelFingerprint(ctx context.Context, id string) (string, error) {
	b, err := s.resolveModel(ctx, id)
	if err != nil {
		return "", err
	}
	return s.modelBindingFingerprint(ctx, b), nil
}

// modelBindingFingerprint changes when what the binding runs on does (the provider's settings, the key sealed anew).
func (s *Service) modelBindingFingerprint(ctx context.Context, b ModelBinding) string {
	raw, _ := json.Marshal(b)
	if strings.HasPrefix(b.Provider, "legacy.") {
		v, _ := json.Marshal(s.legacyModelConfig(ctx, b.Provider))
		raw = append(raw, v...)
	}
	if name, ok := strings.CutPrefix(b.Provider, "launcher."); ok {
		for _, l := range s.storedLaunchers(ctx) {
			if l.Name == name {
				v, _ := json.Marshal(l)
				raw = append(raw, v...)
				break
			}
		}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
