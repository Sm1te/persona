package assistant

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// launcherListing is a node's launchers.list: the launchers, and the shell and rc file they came from ("" from an
// older node).
type launcherListing struct {
	Launchers []proto.Launcher `json:"launchers"`
	Shell     string           `json:"shell"`
	RCFile    string           `json:"rc_file"`
}

// listLaunchers asks one machine for its list, giving it launcherSyncTimeout; an offline machine is not asked.
func (s *Service) listLaunchers(ctx context.Context, d Device) (launcherListing, error) {
	var res launcherListing
	if !d.Online {
		return res, apierr.ErrOffline
	}
	cctx, cancel := context.WithTimeout(ctx, launcherSyncTimeout)
	defer cancel()
	err := s.call(cctx, d.ID, DeviceLaunchersList, map[string]any{}, &res)
	if err != nil && cctx.Err() != nil && ctx.Err() == nil {
		err = context.DeadlineExceeded
	}
	return res, err
}

// launcherSyncState names why a machine's list did not come: offline, timeout, or error with the reason.
func launcherSyncState(ctx context.Context, err error) (state, reason string) {
	switch {
	case errors.Is(err, apierr.ErrOffline):
		return "offline", ""
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		return "timeout", ""
	}
	return "error", textfmt.Clip(strings.TrimSpace(err.Error()), 300)
}

// configuredLauncher is machine's hub.json launcher name (machines[].launchers, in the hello): a wrapper the node may
// not list, the agent the user said it runs. The node runs a launcher it does not list only when it is vouched for
// like this (configured, node runnableLauncher).
func (s *Service) configuredLauncher(ctx context.Context, machine, name string) (ConfiguredLauncher, bool) {
	for _, l := range s.hello(ctx).Models.Launchers[machine] {
		if l.Name == name {
			return l, true
		}
	}
	return ConfiguredLauncher{}, false
}

// launcherParams are a node launcher method's params for name on machine: configured (and its agent_type) when
// hub.json names it there.
func (s *Service) launcherParams(ctx context.Context, machine, name string, params map[string]any) map[string]any {
	params["name"] = name
	if l, ok := s.configuredLauncher(ctx, machine, name); ok {
		params["configured"] = true
		if l.AgentType != "" {
			params["agent_type"] = l.AgentType
		}
	}
	return params
}

// machineSource: a launcher a machine defines itself, a ~/.zshrc function or alias ("zshrc") or a wrapper script on
// its PATH ("path", node/pathlaunchers.go); its page shows its text and what a probe found.
func machineSource(source string) bool { return source == "zshrc" || source == "path" }

// listedRCLauncher: Settings › Model providers lists l from a computer's list. A runtime's launcher without model
// configuration (DSH, node/runtimes.go) is not.
func listedRCLauncher(l proto.Launcher) bool {
	return l.Source == "" || machineSource(l.Source) || l.Agent == "agy" || l.Agent == "omp"
}

// launcherViews is the whole list: managed first (newest last), then ~/.zshrc ones by name; hidden ones flagged.
func (s *Service) launcherViews(ctx context.Context) []LauncherView {
	return s.launcherViewsOf(ctx, s.zshrcLaunchers(ctx, false))
}

func (s *Service) launcherViewsOf(ctx context.Context, zshrc []zshrcLaunchers) []LauncherView {
	return s.launcherViewsFrom(ctx, zshrc, s.storedLaunchers(ctx))
}

func (s *Service) launcherViewsFrom(ctx context.Context, zshrc []zshrcLaunchers, stored []storedLauncher) []LauncherView {
	var out []LauncherView
	managed := map[string]bool{}
	for _, l := range stored {
		managed[l.Name] = true
		out = append(out, launcherView(l))
	}
	hidden := s.hiddenLaunchers(ctx)
	byName := map[string]*LauncherView{}
	var names []string
	for _, m := range zshrc {
		for _, l := range m.Launchers {
			if managed[l.Name] || l.Name == "claude" || !listedRCLauncher(l) {
				continue
			}
			v := byName[l.Name]
			if v == nil {
				src := "zshrc"
				if l.Source != "" {
					src = l.Source
				}
				v = &LauncherView{Name: l.Name, Source: src, Agent: l.Agent, BaseURL: l.BaseURL, Model: l.Model, Hidden: slices.Contains(hidden, l.Name)}
				byName[l.Name] = v
				names = append(names, l.Name)
			}
			v.Machines = append(v.Machines, m.MachineID)
			if rc := cmp.Or(m.Sync.RCFile, defaultRCFile); v.Source == "zshrc" && !slices.Contains(v.RCFiles, rc) {
				v.RCFiles = append(v.RCFiles, rc)
			}
			if m.Sync.State != "" && m.Sync.State != "ok" {
				v.Stale = append(v.Stale, m.MachineID) // the machine's last list: it is offline or did not answer now
			}
		}
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out
}

// builtinClaude is the plain `claude` of every machine that has Claude Code (the default launcher, “Claude Code
// default” in Model Configuration; assistant.launcher "claude"): one view, its machines and their versions. ok false
// when no machine is known to have it, or a provider added in Persona took the name.
func (s *Service) builtinClaude(ctx context.Context, zshrc []zshrcLaunchers) (LauncherView, bool) {
	if slices.ContainsFunc(s.storedLaunchers(ctx), func(l storedLauncher) bool { return l.Name == "claude" }) {
		return LauncherView{}, false
	}
	// hidden like an rc-file launcher (#4384): out of the pickers, shown under Hidden to restore
	v := LauncherView{Name: "claude", Source: "builtin", Versions: map[string]string{}, Hidden: slices.Contains(s.hiddenLaunchers(ctx), "claude")}
	for _, m := range zshrc {
		listed := slices.ContainsFunc(m.Launchers, func(l proto.Launcher) bool { return l.Name == "claude" })
		if m.ClaudeVersion == "" && !listed {
			continue
		}
		v.Machines = append(v.Machines, m.MachineID)
		if m.ClaudeVersion != "" {
			v.Versions[m.MachineID] = m.ClaudeVersion
		}
		if m.Sync.State != "" && m.Sync.State != "ok" {
			v.Stale = append(v.Stale, m.MachineID)
		}
	}
	return v, len(v.Machines) > 0
}

// launcherSyncList is every machine's sync state, in the machines' order (the ones asked).
func launcherSyncList(zshrc []zshrcLaunchers) []LauncherSync {
	out := []LauncherSync{}
	for _, m := range zshrc {
		if m.Sync.State != "" {
			out = append(out, m.Sync)
		}
	}
	return out
}

// machineLaunchers is what a machine can start (the pickers: new chat, the assistant's model): its own launchers
// (the node's list) but the hidden ones, then the managed ones.
func (s *Service) machineLaunchers(ctx context.Context, machine string) ([]proto.Launcher, error) {
	var res struct {
		Launchers []proto.LauncherEnvelope `json:"launchers"`
	}
	if err := s.call(ctx, machine, DeviceLaunchersList, map[string]any{}, &res); err != nil {
		return nil, err
	}
	hidden := s.hiddenLaunchers(ctx)
	// Only the providers in this computer's scope: one out of it is not named to the node, whose own launcher by
	// that name (if any) is what runs there.
	managed := slices.DeleteFunc(slices.Clone(s.storedLaunchers(ctx)), func(m storedLauncher) bool { return !s.usableOn(ctx, m, machine) })
	out := make([]proto.Launcher, 0, len(res.Launchers)+len(managed))
	for _, envelope := range res.Launchers {
		l := proto.Launcher(envelope)
		if slices.Contains(hidden, l.Name) || slices.ContainsFunc(managed, func(m storedLauncher) bool { return m.Name == l.Name }) {
			continue
		}
		l.Source = compat.Source(l.Source)
		if l.Source == "" { // a runtime's launcher (node/runtimes.go) keeps its own source, e.g. "dsh"
			l.Source = "zshrc"
		}
		out = append(out, l)
	}
	// A node that starts its own Codex launchers with no first message starts a managed one so too (they share
	// codex-start, node/codex_waiting.go); an older node's Codex starts all need it.
	codexOptional := slices.ContainsFunc(res.Launchers, func(l proto.LauncherEnvelope) bool { return l.Agent == "codex" && l.PromptOptional })
	for _, m := range managed {
		if m.Kind == "api" {
			continue
		}
		out = append(out, proto.Launcher{Name: m.Name, BaseURL: m.BaseURL, Model: m.Model, Source: "persona", Agent: m.Agent, Assistant: m.Agent == "agy",
			PromptOptional: m.Agent == "codex" && codexOptional})
	}
	return out, nil
}

// pickerLaunchers is what machine can start, as New Agent's picker lists it: machineLaunchers with hub.json's
// launchers for it applied (sessionLaunchers).
func (s *Service) pickerLaunchers(ctx context.Context, machine string) ([]ConfiguredLauncher, error) {
	ls, err := s.machineLaunchers(ctx, machine)
	if err != nil {
		return nil, err
	}
	discovered := make([]ConfiguredLauncher, 0, len(ls))
	for _, l := range ls {
		discovered = append(discovered, ConfiguredLauncher{Launcher: l})
	}
	return sessionLaunchers(discovered, s.hello(ctx).Models.Launchers[machine]), nil
}

// assistantLaunchers are the machine's launchers backed by a provider the main conversation supports.
func (s *Service) assistantLaunchers(ctx context.Context, machine string) ([]ConfiguredLauncher, error) {
	picker, err := s.pickerLaunchers(ctx, machine)
	if err != nil {
		return nil, err
	}
	return s.assistantLaunchersOf(ctx, machine, picker)
}

// assistantProviders are the launcher names whose current, merged configuration supports the main conversation on
// machine. picker is the node's current list; managed and direct Messages providers are added from their stored records.
func (s *Service) assistantProviders(ctx context.Context, machine string, picker []ConfiguredLauncher) (map[string]bool, error) {
	p, err := s.modelPurposeDef(ctx, "assistant")
	if err != nil {
		return nil, err
	}
	p.MachineID = machine
	stored := s.storedLaunchers(ctx)
	current := slices.Clone(picker)
	for _, managed := range stored {
		if !slices.ContainsFunc(current, func(l ConfiguredLauncher) bool { return l.Name == managed.Name }) {
			current = append(current, ConfiguredLauncher{Launcher: proto.Launcher{
				Name: managed.Name, BaseURL: managed.BaseURL, Model: managed.Model, Source: "persona", Agent: managed.Agent,
			}})
		}
	}
	current = sessionLaunchers(current, s.hello(ctx).Models.Launchers[machine])
	hidden := s.hiddenLaunchers(ctx)
	allowed := map[string]bool{}
	for _, l := range current {
		if l.Name == "" || slices.Contains(hidden, l.Name) && compat.Source(l.Source) != "persona" {
			continue
		}
		if s.modelLauncherCompatible(p, l, stored) {
			allowed[l.Name] = true
		}
	}
	return allowed, nil
}

// assistantLaunchersOf is assistantLaunchers from the machine's picker list (pickerLaunchers).
func (s *Service) assistantLaunchersOf(ctx context.Context, machine string, picker []ConfiguredLauncher) ([]ConfiguredLauncher, error) {
	ctx = withSettingsMemo(ctx)
	allowed, err := s.assistantProviders(ctx, machine, picker)
	if err != nil {
		return nil, err
	}
	stored := s.storedLaunchers(ctx)
	out := []ConfiguredLauncher{}
	seen := map[string]bool{}
	for _, l := range picker {
		if allowed[l.Name] {
			out = append(out, l)
			seen[l.Name] = true
		}
	}
	// Ordinary Agent lists omit direct API providers; Messages providers are already sent to nodes as launchers.
	for _, l := range stored {
		if l.Kind == "api" && allowed[l.Name] && !seen[l.Name] && s.usableOn(ctx, l, machine) {
			out = append(out, ConfiguredLauncher{Launcher: proto.Launcher{Name: l.Name, BaseURL: l.BaseURL, Model: l.Model, Source: "persona"}, AgentType: "claude"})
		}
	}
	return out, nil
}

// launcherHasModel: whether the launcher offers model (its id or what an alias resolves to), from its list as it was
// a few minutes ago at most, or the user typed it for this launcher and machine before (models_custom.go). Unknown (an
// agent with no list, a failed call) counts as yes.
func (s *Service) launcherHasModel(ctx context.Context, machine, launcher, model string) bool {
	if set := s.launcherModelSet(ctx, machine, launcher); set == nil || set[model] {
		return true
	}
	return s.isCustomModel(ctx, machine, launcher, model)
}

// launcherModelSet is the launcher's own /model list on machine (ids and what aliases resolve to), as it was a few
// minutes ago at most; nil when it is not known (an agent with no list, a failed call).
func (s *Service) launcherModelSet(ctx context.Context, machine, launcher string) map[string]bool {
	key := machine + "\x00" + launcher
	c := &s.models
	c.cacheMu.Lock()
	if c.cacheAt != nil && time.Since(c.cacheAt[key]) < 5*time.Minute {
		set := c.have[key]
		c.cacheMu.Unlock()
		return set
	}
	c.cacheMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := s.askLauncherModels(cctx, machine, launcher, false)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, x := range res.Models {
		set[x.ID] = true
		if x.Resolved != "" {
			set[x.Resolved] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	c.cacheMu.Lock()
	if c.cacheAt == nil {
		c.cacheAt, c.have = map[string]time.Time{}, map[string]map[string]bool{}
	}
	c.cacheAt[key], c.have[key] = time.Now(), set
	c.cacheMu.Unlock()
	return set
}

// askLauncherModels is machine's node's /model list of launcher name (node/launcher_models.go), with the models of
// the plugin gateway behind it.
func (s *Service) askLauncherModels(ctx context.Context, machine, name string, refresh bool) (proto.LauncherModels, error) {
	var res proto.LauncherModels
	if err := s.call(ctx, machine, assistantwire.DeviceLaunchersModels, s.launcherParams(ctx, machine, name, map[string]any{"refresh": refresh}), &res); err != nil {
		return res, err
	}
	if res.Models == nil {
		res.Models = []proto.AgentModel{}
	}
	res = s.presetModels(ctx, machine, name, res)
	for i, m := range res.Models {
		id := m.ID
		if m.Resolved != "" {
			id = m.Resolved
		}
		res.Models[i].Price = nil
		if c, ok := catalogModel(id); ok && c.InheritedFrom == "" {
			res.Models[i].Price = c.Price
		}
	}
	return res, nil
}
