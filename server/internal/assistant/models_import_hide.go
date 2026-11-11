package assistant

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Hiding the imported aliases (docs/assistant/model-config.md "Importing providers from shell aliases"): after an
// import the alias and the provider made of it would show side by side, so the import can hide the alias in Persona
// (launchers.hidden; the shell file is not touched). Only where the provider stands in for it everywhere: an alias
// something still names stays (hiding never moves a start to another launcher, #4661), and so does one a computer
// would not get the provider for. The preview and the import ask the same question (aliasHide).

// What hiding an imported alias would do.
const (
	hideNone      = "none"      // nothing to hide: Persona does not list it, it is hidden already, or it is claude
	hideOK        = "ok"        // safe to hide
	hideInUse     = "in_use"    // something uses it: it stays
	hideElsewhere = "elsewhere" // a computer would not get the provider in its place: it stays
)

// importSource is one alias of an imported provider: its file and whether hiding it is safe (uses: what uses it,
// elsewhere: the computers where the provider would not stand in for it).
type importSource struct {
	Alias     string   `json:"alias"`
	File      string   `json:"file,omitempty"`
	Hide      string   `json:"hide"`
	Uses      []string `json:"uses,omitempty"`
	Elsewhere []string `json:"elsewhere,omitempty"`
}

// importHideFacts is what the hide check reads, once a request.
type importHideFacts struct {
	machine     string           // the computer imported from
	rc          []zshrcLaunchers // every computer's rc-file launchers, as last seen
	hidden      []string
	stored      []storedLauncher // the providers, as saved (the import: as it saves them)
	machineKeys []machineKey     // the providers' keys for single computers
	devices     []Device
	profiles    []ModelProfile
	tasks       []taskLauncher
	uses        map[string][]string // aliasUses by alias: a group's aliases are asked again for its variants
	// remote: another computer's own aliases (launchers.zshrc), asked once a request and only when needed; ask false
	// (the import under its lock) reads only what was asked before, a computer not asked counting as elsewhere.
	remote map[string]remoteAliases
	ask    bool
}

// taskLauncher is a scheduled task whose own record names a provider.
type taskLauncher struct{ label, provider string }

// remoteAliases is what a computer answered for its aliases: they hold keys, so they are compared and never kept.
type remoteAliases struct {
	aliases []zshrcAlias
	err     error
}

// remoteAliasWait is how long the hide check waits for another computer's aliases: past it, that computer is elsewhere.
var remoteAliasWait = 5 * time.Second

// importHideFacts reads what aliasHide needs. rc is s.zshrcLaunchers, stored the provider list it judges against.
func (s *Service) importHideFacts(ctx context.Context, machine string, rc []zshrcLaunchers, stored []storedLauncher) (importHideFacts, error) {
	f := importHideFacts{machine: machine, rc: rc, hidden: s.hiddenLaunchers(ctx), stored: stored, devices: s.devices(ctx),
		uses: map[string][]string{}, remote: map[string]remoteAliases{}, ask: true}
	keys, err := s.readMachineKeys(ctx)
	if err != nil {
		return f, err
	}
	f.machineKeys = keys
	ps, _, err := s.readModelProfiles(ctx) // the templates made when none is stored copy the purposes, checked anyway
	if err != nil {
		return f, err
	}
	f.profiles = ps.Profiles
	tasks := s.taskViews(ctx)
	if tasks == nil {
		return f, i18n.Err("assistant-models.err_read_tasks")
	}
	for _, t := range tasks {
		r, err := s.modelRecord(ctx, "task."+t.ID)
		if err != nil {
			return f, err
		}
		if r.Binding.Provider != "" {
			f.tasks = append(f.tasks, taskLauncher{label: t.Label, provider: r.Binding.Provider})
		}
	}
	return f, nil
}

// listed says whether Persona lists alias on any computer (listsAlias): only then is there anything to hide.
func (f importHideFacts) listed(alias string) bool {
	return slices.ContainsFunc(f.rc, func(m zshrcLaunchers) bool { _, ok := f.listsAlias(m, alias); return ok })
}

// listsAlias is the launcher named alias in m's rc-file list the way Settings › Model providers lists one
// (launcherViewsFrom): a provider of that name hides it, plain claude and a runtime's own launcher are not listed.
func (f importHideFacts) listsAlias(m zshrcLaunchers, alias string) (proto.Launcher, bool) {
	if slices.ContainsFunc(f.stored, func(l storedLauncher) bool { return l.Name == alias }) {
		return proto.Launcher{}, false
	}
	i := slices.IndexFunc(m.Launchers, func(l proto.Launcher) bool { return l.Name == alias && listedRCLauncher(l) })
	if i < 0 {
		return proto.Launcher{}, false
	}
	return m.Launchers[i], true
}

// aliasHide is whether hiding alias is safe once provider l, with key (the alias's own: an existing provider was
// matched by it), stands in for it: none, in_use, elsewhere or ok (in that order). It changes nothing.
func (s *Service) aliasHide(ctx context.Context, f importHideFacts, alias string, l storedLauncher, key string) (importSource, error) {
	src := importSource{Alias: alias, Hide: hideNone}
	if slices.Contains(reservedLauncherNames, alias) || slices.Contains(f.hidden, alias) || !f.listed(alias) {
		return src, nil
	}
	uses, err := s.aliasUses(ctx, f, alias)
	if err != nil {
		return src, err
	}
	if len(uses) > 0 {
		src.Hide, src.Uses = hideInUse, uses
		return src, nil
	}
	if elsewhere := s.aliasElsewhere(ctx, f, alias, l, key); len(elsewhere) > 0 {
		src.Hide, src.Elsewhere = hideElsewhere, elsewhere
		return src, nil
	}
	src.Hide = hideOK
	return src, nil
}

// aliasUses are what names alias as its launcher, in words: Model configuration's purposes (extensions' too), a
// scheduled task's own launcher, a model profile's purposes (in use or not), and a profile's Agent presets.
func (s *Service) aliasUses(ctx context.Context, f importHideFacts, alias string) ([]string, error) {
	if uses, ok := f.uses[alias]; ok {
		return uses, nil
	}
	provider := launcherProvider(alias)
	uses, err := s.providerModelUses(withManagedSnapshot(ctx, f.stored), provider)
	if err != nil {
		return nil, err
	}
	lang := s.requestCtxLang(ctx)
	add := func(use string) {
		if !slices.Contains(uses, use) {
			uses = append(uses, use)
		}
	}
	for _, t := range f.tasks {
		if t.provider == provider {
			add(i18n.T(lang, "assistant-models_import.use_task", i18n.Args{"name": t.label}))
		}
	}
	for _, p := range f.profiles {
		name := cmp.Or(p.Name, p.ID)
		for _, b := range p.Purposes {
			if b.Provider == provider {
				add(i18n.T(lang, "assistant-models_import.use_profile", i18n.Args{"name": name}))
				break
			}
		}
		for _, tier := range slices.Sorted(maps.Keys(p.Tiers)) {
			if p.Tiers[tier].Launcher == alias {
				add(i18n.T(lang, "assistant-models_import.use_tier", i18n.Args{"tier": tier, "profile": name}))
			}
		}
	}
	f.uses[alias] = uses
	return uses, nil
}

// aliasElsewhere are the computers (their names) that list alias and would not get provider l in its place, with the
// same account: out of its scope, every one when it skips a computer with a service of its own (the alias stays in
// the file, so the computer keeps counting as having one), one whose key for l (a key of its own, else key) is not
// key, and another computer whose own alias of that name does not reach l's address with key (asked of its node:
// offline, too old, no such alias there, or a function, all count as not).
func (s *Service) aliasElsewhere(ctx context.Context, f importHideFacts, alias string, l storedLauncher, key string) []string {
	var out []string
	add := func(id string) {
		name := id
		if i := slices.IndexFunc(f.devices, func(d Device) bool { return d.ID == id }); i >= 0 && f.devices[i].Name != "" {
			name = f.devices[i].Name
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	mine := func(id string) bool { return id == f.machine || s.sameMachine(id, f.machine) }
	stays := func(id string) bool {
		return s.scopeReason(l, id) != "" || l.SkipsOwnService() || !s.sameKeyOn(f, l, id, key)
	}
	if stays(f.machine) { // the alias is in its file, whatever its last list said
		add(f.machine)
	}
	for _, m := range f.rc {
		if _, ok := f.listsAlias(m, alias); !ok || mine(m.MachineID) {
			continue
		}
		if stays(m.MachineID) || !s.sameAliasOn(ctx, f, m.MachineID, alias, l.LaunchBaseURL(), key) {
			add(m.MachineID)
		}
	}
	return out
}

// sameKeyOn: provider l runs on machine with key: its key for that computer, when it has one, opens to key.
func (s *Service) sameKeyOn(f importHideFacts, l storedLauncher, machine, key string) bool {
	sealed := s.keySealedFor(l, f.machineKeys, machine)
	if sealed == "" || sealed == l.KeySealed {
		return true // the provider's own key: the one the import matched (or will seal)
	}
	own, err := s.openKey(sealed)
	return err == nil && own == key
}

// sameAliasOn: machine's own alias reaches base with key (importKey), as its node says now.
func (s *Service) sameAliasOn(ctx context.Context, f importHideFacts, machine, alias, base, key string) bool {
	got, ok := f.remote[machine]
	if !ok {
		if !f.ask {
			return false
		}
		cctx, cancel := context.WithTimeout(ctx, remoteAliasWait)
		got.aliases, got.err = zshrcAliases(cctx, s, machine)
		cancel()
		f.remote[machine] = got
	}
	if got.err != nil {
		return false
	}
	i := slices.IndexFunc(got.aliases, func(a zshrcAlias) bool { return a.Alias == alias })
	if i < 0 || (got.aliases[i].Command != "" && got.aliases[i].Command != "claude") {
		return false
	}
	theirs, _ := importKey(got.aliases[i].Env)
	return proto.MessagesBaseURL(got.aliases[i].Env["ANTHROPIC_BASE_URL"]) == base && theirs == key
}
