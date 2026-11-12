package assistant

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The model providers Settings › Model providers adds a provider from (the user fills in only the key): the built-in ones
// (presets_builtin.json, docs/assistant/onboarding.md 4.3) and the plugins' (docs/extensions/extensions.md "Model providers", as the hub has them:
// plugins.presets). A launcher on a preset's gateway offers the gateway's models in "New Agent".

type LauncherPreset = assistantwire.LauncherPreset

// builtinPlugin is the plugin field of the built-in presets; their ids are builtin/<vendor>.
const builtinPlugin = "builtin"

//go:embed presets_builtin.json
var builtinPresetsJSON []byte

// builtinPreset is one entry of presets_builtin.json: a preset and where it can be opened without a VPN or a foreign
// card (cn, global or both), which orders the list for the request's language.
type builtinPreset struct {
	LauncherPreset
	Region string `json:"region"`
}

// builtinPresets are read on first use, not whenever personad starts (a hook too, #1308).
var builtinPresets = sync.OnceValue(func() []builtinPreset {
	var list []builtinPreset
	if err := json.Unmarshal(builtinPresetsJSON, &list); err != nil {
		panic("presets_builtin.json: " + err.Error())
	}
	return list
})

// presetSlug is the vendor part of a preset id (builtin/deepseek → deepseek, <plugin>/<provider> → <provider>).
func presetSlug(id string) string {
	if _, after, ok := strings.Cut(id, "/"); ok {
		return after
	}
	return id
}

// builtinLauncherPresets are the built-in presets for l: names and descriptions in l, the ones that can be opened from
// where l is spoken first (Chinese: the vendors in China, else Anthropic and the international endpoints), each group
// in the file's order.
func builtinLauncherPresets(l i18n.Lang) []LauncherPreset {
	first := func(region string) bool {
		if l == i18n.ZhHans {
			return region != "global"
		}
		return region != "cn"
	}
	var head, tail []LauncherPreset
	for _, b := range builtinPresets() {
		pr := b.LauncherPreset
		pr.Extension = builtinPlugin
		pr.Models = slices.Clone(pr.Models)
		key := strings.ReplaceAll(presetSlug(pr.ID), "-", "_")
		pr.DisplayName = i18n.T(l, "assistant-models_presets.name_"+key)
		pr.Description = i18n.T(l, "assistant-models_presets.desc_"+key)
		if first(b.Region) {
			head = append(head, pr)
		} else {
			tail = append(tail, pr)
		}
	}
	return append(head, tail...)
}

// mergePresets is the built-in presets with the plugins' after them; a plugin's with a built-in one's id takes its
// place.
func mergePresets(builtin, plugins []LauncherPreset) []LauncherPreset {
	out := slices.Clone(builtin)
	for _, pr := range plugins {
		if i := slices.IndexFunc(out, func(b LauncherPreset) bool { return b.ID == pr.ID }); i >= 0 {
			out[i] = pr
			continue
		}
		out = append(out, pr)
	}
	return out
}

// launcherPresets are the built-in providers and the plugins' now, in the request's language.
func (s *Service) launcherPresets(ctx context.Context) []LauncherPreset {
	var res struct {
		Presets []LauncherPreset `json:"presets"`
	}
	if err := s.appCall(ctx, AppPlugins, assistantwire.PluginsPresets, map[string]any{}, &res); err != nil {
		s.logf("launchers: the plugins' providers: %v", err)
	}
	return mergePresets(builtinLauncherPresets(s.requestCtxLang(ctx)), res.Presets)
}

// presetByID is the preset id names, if there is one.
func (s *Service) presetByID(ctx context.Context, id string) (LauncherPreset, bool) {
	for _, pr := range s.launcherPresets(ctx) {
		if pr.ID == id {
			return pr, true
		}
	}
	return LauncherPreset{}, false
}

// presetModel is a preset's default model: its own, else the first it lists.
func presetModel(pr LauncherPreset) string {
	if pr.Model == "" && len(pr.Models) > 0 {
		return pr.Models[0].ID
	}
	return pr.Model
}

// presetFill fills in what a launcher added from a preset leaves empty.
func (s *Service) presetFill(ctx context.Context, in *launcherInput) error {
	if in.Preset == "" {
		return nil
	}
	pr, ok := s.presetByID(ctx, in.Preset)
	if !ok {
		return errNoPreset
	}
	if in.Kind == "" {
		in.Kind = pr.Kind
	}
	if in.API == "" {
		in.API = pr.API
	}
	if in.BaseURL == "" {
		in.BaseURL = pr.BaseURL
	}
	if in.Auth == "" {
		in.Auth = pr.Auth
	}
	if in.Model == "" {
		in.Model = presetModel(pr)
	}
	if in.SmallModel == "" && in.Kind != "api" {
		in.SmallModel = pr.SmallModel
	}
	return nil
}

// presetDisplayName is the display name a provider gets when the hub named it from preset (launcherNameFor gave it
// n, 1 for the bare slug): the preset's localized name, with the same number after it when n > 1 ("Zhipu GLM 2"), so
// two providers from one preset tell apart in Model configuration. A provider the user named keeps showing that name.
func (s *Service) presetDisplayName(ctx context.Context, preset string, n int) string {
	pr, ok := s.presetByID(ctx, preset)
	if !ok {
		return ""
	}
	return numberedName(cmp.Or(pr.DisplayName, pr.Name), n)
}

// numberedName is a display name the hub gives a provider it named with n (1 for the bare slug): with the same number
// after it when n > 1 ("Zhipu GLM 2"), name cut so the whole fits maxDisplayName (a long gateway host).
func numberedName(name string, n int) string {
	suffix := ""
	if n > 1 {
		suffix = " " + strconv.Itoa(n)
	}
	if r := []rune(name); len(r)+len(suffix) > maxDisplayName {
		name = strings.TrimRight(string(r[:maxDisplayName-len(suffix)]), ".- ")
	}
	return name + suffix
}

// launcherNameFor is the name a provider added from preset gets when none is given: the vendor's slug, -2, -3… after
// it while taken. n is 1 for the bare slug, else the number after it.
func launcherNameFor(preset string, taken func(string) bool) (name string, n int) {
	return freeLauncherName(cmp.Or(launcherSlug(presetSlug(preset)), "provider"), taken)
}

// customLauncherName is the name a provider added without a preset gets when none is given (#5029): the slug of its
// display name, else of its gateway's host (gw.example.com → gw-example-com), else provider; -2, -3… after it while
// taken. n is as launcherNameFor's.
func customLauncherName(displayName, host string, taken func(string) bool) (name string, n int) {
	return freeLauncherName(cmp.Or(launcherSlug(displayName), launcherSlug(host), "provider"), taken)
}

// launcherSlug is s made to fit the launcher name rule (managedLauncherName): lower case, each run of other characters
// one -, none at either end, at most 40 bytes, p- before a leading digit. "" when nothing of s is left ("智谱").
func launcherSlug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug != "" && slug[0] >= '0' && slug[0] <= '9' {
		slug = "p-" + slug
	}
	return slug
}

// freeLauncherName is base while no launcher has it (taken) and no route keeps it (reservedLauncherNames), else the
// first free one of base-2, base-3…; n is 1 for base itself, else the number after it.
func freeLauncherName(base string, taken func(string) bool) (name string, n int) {
	used := func(n string) bool { return taken(n) || slices.Contains(reservedLauncherNames, n) }
	if !used(base) {
		return base, 1
	}
	for i := 2; ; i++ {
		if n := base + "-" + strconv.Itoa(i); !used(n) {
			return n, i
		}
	}
}

// presetFor is the launcher-kind preset a launcher reaches its gateway through: the one it was added from, else the one
// whose address is the launcher's gateway (a provider added by hand, or a ~/.zshrc launcher, on a plugin's gateway;
// built-in presets match only by id).
func presetFor(presets []LauncherPreset, preset, baseURL string) (LauncherPreset, bool) {
	var byURL *LauncherPreset
	want := normalGateway(baseURL)
	for i, pr := range presets {
		if pr.Kind != "launcher" {
			continue
		}
		if preset != "" && pr.ID == preset {
			return pr, true
		}
		// Built-in presets are vendors' public endpoints: a launcher only gets their models when it was added from one,
		// not because it happens to point at api.anthropic.com.
		if byURL == nil && want != "" && pr.Extension != builtinPlugin && normalGateway(pr.BaseURL) == want {
			byURL = &presets[i]
		}
	}
	if byURL != nil {
		return *byURL, true
	}
	return LauncherPreset{}, false
}

// normalGateway compares launcher gateway addresses: no scheme case, host case, trailing slash or trailing /v1
// differences (a launcher saved from ".../v1" is stored without it, proto.MessagesBaseURL).
func normalGateway(u string) string {
	return strings.ToLower(proto.MessagesBaseURL(u))
}

// withPresetModels adds a preset's models to a launcher's /model list (its own entries first, then the gateway's that
// are not among them): a gateway with a hundred models otherwise shows only the launcher's one in "New Agent".
func withPresetModels(res proto.LauncherModels, pr LauncherPreset) proto.LauncherModels {
	seen := map[string]bool{}
	for _, m := range res.Models {
		seen[m.ID] = true
		if m.Resolved != "" {
			seen[m.Resolved] = true
		}
	}
	for _, m := range pr.Models {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		res.Models = append(res.Models, proto.AgentModel{ID: m.ID, Name: cmp.Or(m.Name, m.ID), Description: cmp.Or(pr.DisplayName, pr.Name)})
	}
	return res
}

// withProviderModels adds a managed provider's own models (its `models`) to a launcher's /model list, after the
// entries already there: Claude Code's /model shows only its tier aliases, so a gateway's other models could not be
// picked in "New Agent" or sent with a message (launcherHasModel) though --model runs them.
func withProviderModels(res proto.LauncherModels, l storedLauncher) proto.LauncherModels {
	seen := map[string]bool{}
	for _, m := range res.Models {
		seen[m.ID] = true
		if m.Resolved != "" {
			seen[m.Resolved] = true
		}
	}
	for _, m := range l.Models {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		res.Models = append(res.Models, proto.AgentModel{ID: m.ID, Name: cmp.Or(m.Name, m.ID), Description: cmp.Or(l.DisplayName, l.Name),
			Efforts: m.Efforts, DefaultEffort: m.DefaultEffort})
	}
	return res
}

// presetModels adds to a Claude Code /model list the models of the managed provider behind launcher name (its own
// `models`), then those of the plugin preset behind it (managed: its preset or gateway; ~/.zshrc: the gateway the
// node read from it).
func (s *Service) presetModels(ctx context.Context, machine, name string, res proto.LauncherModels) proto.LauncherModels {
	if res.Agent != "" && res.Agent != "claude" {
		return res
	}
	presets := s.launcherPresets(ctx)
	for _, l := range s.storedLaunchers(ctx) {
		if l.Name == name {
			if l.Kind == "api" {
				return res
			}
			res = withProviderModels(res, l)
			if pr, ok := presetFor(presets, l.Preset, l.BaseURL); ok {
				return withPresetModels(res, pr)
			}
			return res
		}
	}
	if len(presets) == 0 {
		return res
	}
	if l, ok := s.knownZshrcLauncher(ctx, machine, name); ok && l.Agent == "" {
		if pr, ok := presetFor(presets, "", l.BaseURL); ok {
			return withPresetModels(res, pr)
		}
	}
	return res
}

// knownZshrcLauncher is machine's ~/.zshrc launcher name as the hub last saw it, so the picker does not wait on a
// second node call; only a launcher never seen is asked for now.
func (s *Service) knownZshrcLauncher(ctx context.Context, machine, name string) (proto.Launcher, bool) {
	find := func(list []proto.Launcher) (proto.Launcher, bool) {
		for _, l := range list {
			if l.Name == name {
				return l, true
			}
		}
		return proto.Launcher{}, false
	}
	for _, m := range s.zshrcLaunchers(ctx, false) {
		if m.MachineID == machine {
			if l, ok := find(m.Launchers); ok {
				return l, true
			}
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := s.machineLaunchers(cctx, machine)
	if err != nil {
		return proto.Launcher{}, false
	}
	return find(list)
}
