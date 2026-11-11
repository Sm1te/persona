package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A Persona-managed provider's device scope (docs/integrations/launchers.md "Device scope and on-demand delivery"):
// which computers it runs on (share, scope_machines), when its secrets travel (delivery), whether a computer whose
// harness has a usable service of its own takes it (own_service), and a key for one computer (launchers.machine_keys).
// A computer in scope is named the provider without its secrets (launchers.catalog) and asks for them when it needs
// them (launchers.pull, answered here for the connection's machine). The scope is the boundary: a computer outside it
// gets nothing of the provider, does not list it, and no start runs it there.

const (
	shareAll      = "all"
	shareSelected = "selected"
	shareNone     = "none"
)

// The node methods of the scope (docs/protocol/protocol.md "hub ↔ node").
const (
	// DeviceLaunchersCatalog: {launchers: [proto.ManagedLauncher]} → {ok}: the providers in the machine's scope, the
	// ones it pulls without their secrets (withheld). An older node answers unknown method: it gets launchers.managed.
	DeviceLaunchersCatalog = "launchers.catalog"
	// DeviceLaunchersOwnService: {agent} → proto.OwnServiceState.
	DeviceLaunchersOwnService = "launchers.own_service"
	// NodeLaunchersPull is the node's request: {name, own_service?} → {launcher: proto.ManagedLauncher}.
	NodeLaunchersPull = "launchers.pull"
)

// machineKey is one computer's own key for a provider, sealed like the provider's.
type machineKey struct {
	Launcher  string `json:"launcher"`
	Machine   string `json:"machine"`
	KeySealed string `json:"key_sealed"`
}

// share is the provider's scope as it reads: a record from before scopes is every computer.
func (l storedLauncher) share() string {
	if l.Share == "" {
		return shareAll
	}
	return l.Share
}

func (l storedLauncher) delivery() string {
	if l.Delivery == "" {
		return proto.DeliveryPush
	}
	return l.Delivery
}

func (l storedLauncher) ownService() string {
	if l.OwnService == "" {
		return proto.OwnServiceUse
	}
	return l.OwnService
}

// rev is what changes when the provider is saved (a key for one computer too: that saves it).
func (l storedLauncher) rev() string { return strconv.FormatInt(l.Updated.UnixNano(), 36) }

// readMachineKeys is every provider's keys for single computers; a failed read fails the operation.
func (s *Service) readMachineKeys(ctx context.Context) ([]machineKey, error) {
	st := s.store()
	if st == nil {
		return nil, i18n.Err("assistant-models.err_no_database")
	}
	var list []machineKey
	if _, err := st.Setting(ctx, launchersMachineKeysKey, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// withKeyMachines fills the managed views' key_machines: the computers with a key of their own (a failed read: none),
// and the keys that are references (withKeyReferences). Only Settings › Model providers shows them; Model
// configuration does not read the setting.
func (s *Service) withKeyMachines(ctx context.Context, views []LauncherView) []LauncherView {
	var list []machineKey
	if st := s.store(); st != nil {
		_, _ = memoSetting(ctx, st, launchersMachineKeysKey, &list)
	}
	s.withKeyReferences(ctx, views, list)
	for i := range views {
		if views[i].Source != "persona" {
			continue
		}
		for _, k := range list {
			if k.Launcher == views[i].Name {
				views[i].KeyMachines = append(views[i].KeyMachines, k.Machine)
			}
		}
	}
	return views
}

// inScopeList: machine is one of ids (as the devices know it: an id another one absorbed counts).
func (s *Service) inScopeList(ids []string, machine string) bool {
	return slices.ContainsFunc(ids, func(id string) bool { return id == machine || s.sameMachine(id, machine) })
}

// scopeReason is why provider l may not run on machine by its scope ("" when it may): the key of its message.
// Trust (a computer waiting to be confirmed) is the caller's.
func (s *Service) scopeReason(l storedLauncher, machine string) string {
	switch l.Share {
	case "": // from before scopes: every confirmed computer, work ones too, as then
		return ""
	case shareNone:
		return "err_scope_none"
	case shareSelected:
		if !s.inScopeList(l.ScopeMachines, machine) {
			return "err_scope_machine"
		}
	default:
		if s.isWorkMachine(machine) && !s.inScopeList(l.ScopeMachines, machine) {
			return "err_scope_work"
		}
	}
	return ""
}

// scopeText is the words of a refusal for provider l on machine in language lang.
func (s *Service) scopeText(ctx context.Context, lang i18n.Lang, key string, l storedLauncher, machine string, more ...i18n.Args) string {
	name := machine
	if d, ok := s.device(ctx, machine); ok && d.Name != "" {
		name = d.Name
	}
	args := i18n.Args{"provider": cmp.Or(l.DisplayName, l.Name), "machine": name}
	for _, m := range more {
		for k, v := range m {
			args[k] = v
		}
	}
	return i18n.T(lang, "assistant-models_launchers."+key, args)
}

// managedNamed is the stored provider by name; false for any other launcher.
func (s *Service) managedNamed(ctx context.Context, name string) (storedLauncher, bool) {
	if name == "" {
		return storedLauncher{}, false
	}
	for _, l := range s.storedLaunchers(ctx) {
		if l.Name == name {
			return l, true
		}
	}
	return storedLauncher{}, false
}

// launcherScopeErr refuses launcher name on machine when it is a Persona-managed provider out of the computer's scope,
// or one that skips a computer with its own service where the node said it has one (known only from what was asked
// before: the node's own pull is the last word). nil for every other launcher.
func (s *Service) launcherScopeErr(ctx context.Context, machine, name string) error {
	l, ok := s.managedNamed(ctx, name)
	if !ok || machine == "" {
		return nil
	}
	if key := s.scopeReason(l, machine); key != "" {
		return &rpc.Error{Code: proto.CodeLauncherOutOfScope, Message: s.scopeText(ctx, s.lang(ctx), key, l, machine)}
	}
	if l.SkipsOwnService() {
		if st, ok := s.ownServiceCached(machine, l.AgentType()); ok && st.Known && st.Own {
			return s.ownServiceErr(ctx, s.lang(ctx), l, machine)
		}
	}
	return nil
}

func (s *Service) ownServiceErr(ctx context.Context, lang i18n.Lang, l storedLauncher, machine string) error {
	return &rpc.Error{Code: proto.CodeLauncherOwnService,
		Message: s.scopeText(ctx, lang, "err_own_service", l, machine, i18n.Args{"harness": harnessName(l.AgentType())})}
}

// harnessName is how an agent CLI is called in the messages.
func harnessName(agent string) string {
	switch agent {
	case "codex":
		return "Codex"
	case "agy":
		return "Antigravity"
	case "omp":
		return "Oh My Pi"
	}
	return "Claude Code"
}

// startScopeMethods are the node calls that run a launcher (params' launcher): a scope refuses them (service.go call).
var startScopeMethods = map[string]bool{DeviceSessionStart: true, DeviceAssistantSend: true, DeviceAssistantWarm: true, DeviceSessionSend: true}

// ---- the computers' own service ----

// ownSeen is a machine's answer for one harness and when it came.
type ownSeen struct {
	st proto.OwnServiceState
	at time.Time
}

var (
	ownServiceFresh = 10 * time.Minute
	ownServiceRetry = time.Minute
	ownServiceWait  = 8 * time.Second
)

func (s *Service) ownServiceCached(machine, agent string) (proto.OwnServiceState, bool) {
	m := &s.models
	m.ownMu.Lock()
	defer m.ownMu.Unlock()
	seen, ok := m.own[machine+"/"+agent]
	if !ok {
		return proto.OwnServiceState{}, false
	}
	fresh := ownServiceFresh
	if !seen.st.Known {
		fresh = ownServiceRetry
	}
	return seen.st, time.Since(seen.at) < fresh
}

// ownService asks machine's node whether agent's harness has a usable service of its own there (cached; an older node
// or one that does not answer: unknown).
func (s *Service) ownService(ctx context.Context, machine, agent string) proto.OwnServiceState {
	if st, ok := s.ownServiceCached(machine, agent); ok {
		return st
	}
	st := proto.OwnServiceState{Agent: agent}
	cctx, cancel := context.WithTimeout(ctx, ownServiceWait)
	defer cancel()
	if err := s.call(cctx, machine, DeviceLaunchersOwnService, map[string]any{"agent": agent}, &st); err != nil {
		st = proto.OwnServiceState{Agent: agent}
	}
	m := &s.models
	m.ownMu.Lock()
	if m.own == nil {
		m.own = map[string]ownSeen{}
	}
	m.own[machine+"/"+agent] = ownSeen{st: st, at: time.Now()}
	m.ownMu.Unlock()
	return st
}

// usableOn: provider l may be listed on machine's pickers (its scope, and its own-service rule as the node says).
func (s *Service) usableOn(ctx context.Context, l storedLauncher, machine string) bool {
	if s.scopeReason(l, machine) != "" {
		return false
	}
	if l.SkipsOwnService() {
		if st := s.ownService(ctx, machine, l.AgentType()); st.Known && st.Own {
			return false
		}
	}
	return true
}

// ---- what the nodes get ----

// openFor is the provider with its secrets for machine: that computer's own key over the provider's.
func (s *Service) openFor(ctx context.Context, l storedLauncher, keys []machineKey, machine string) (proto.ManagedLauncher, error) {
	m, err := s.openStoredLauncher(l)
	if err != nil {
		return m, err
	}
	// A reference resolves here, for this computer (launcher_secrets.go); one that does not refuses with its words.
	if m.APIKey, err = s.openLauncherKey(ctx, s.keySealedFor(l, keys, machine), l.Name, machine); err != nil {
		return m, err
	}
	m.Rev = l.rev()
	return m, nil
}

// keySealedFor is the key of l that machine uses: its own when it has one, else the provider's.
func (s *Service) keySealedFor(l storedLauncher, keys []machineKey, machine string) string {
	for _, k := range keys {
		if k.Launcher == l.Name && (k.Machine == machine || s.sameMachine(k.Machine, machine)) && k.KeySealed != "" {
			return k.KeySealed
		}
	}
	return l.KeySealed
}

// nodeLaunchersFor is what machine is told: the providers in its scope without their secrets (catalog), and, for a
// node from before the catalog, the same with them (legacy: not those that skip a computer known to have its own
// service). A provider whose secrets do not open is left out of legacy (err says which went wrong).
func (s *Service) nodeLaunchersFor(ctx context.Context, stored []storedLauncher, keys []machineKey, machine string) (catalog, legacy []proto.ManagedLauncher, err error) {
	catalog, legacy = []proto.ManagedLauncher{}, []proto.ManagedLauncher{}
	for _, l := range stored {
		if l.Kind == "api" && !messagesAPI(l.API) {
			continue
		}
		if s.scopeReason(l, machine) != "" {
			continue
		}
		pub := l.ManagedLauncher
		pub.BaseURL, pub.Rev = pub.LaunchBaseURL(), l.rev()
		catalog = append(catalog, pub.Public())
		if l.SkipsOwnService() {
			if st, ok := s.ownServiceCached(machine, l.AgentType()); ok && st.Known && st.Own {
				continue
			}
		}
		if _, ok := s.keyReference(s.keySealedFor(l, keys, machine)); ok {
			// A node from before the catalog would be handed the value at every connection, used or not: a key that
			// is a reference goes only to a node that pulls it.
			continue
		}
		full, e := s.openFor(ctx, l, keys, machine)
		if e != nil {
			err = e
			continue
		}
		full.Rev = ""
		legacy = append(legacy, full)
	}
	return catalog, legacy, err
}

// isUnknownMethod: a node from before the method.
func isUnknownMethod(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown method")
}

// giveNodeLaunchers tells one online machine its providers: the catalog, or launchers.managed for an older node.
func (s *Service) giveNodeLaunchers(ctx context.Context, machine string, stored []storedLauncher, keys []machineKey) {
	catalog, legacy, err := s.nodeLaunchersFor(ctx, stored, keys, machine)
	if err != nil {
		s.logf("launchers: %s: a provider's secrets do not open: %v", machine, err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = s.call(cctx, machine, DeviceLaunchersCatalog, map[string]any{"launchers": catalog}, nil)
	if isUnknownMethod(err) {
		err = s.call(cctx, machine, assistantwire.DeviceLaunchersManaged, map[string]any{"launchers": legacy}, nil)
	}
	if err != nil {
		s.logf("launchers: give %s its providers: %v", machine, err)
	}
}

// ---- the node's pull ----

// pullLauncher answers a node's launchers.pull: provider name with its secrets for the connection's machine, when it
// is in that computer's scope and, for one that skips a computer with its own service, the node says it has none.
func (s *Service) pullLauncher(ctx context.Context, machine string, params json.RawMessage) (any, error) {
	var p struct {
		Name       string                 `json:"name"`
		OwnService *proto.OwnServiceState `json:"own_service"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	lang := s.lang(ctx)
	stored, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(stored, func(l storedLauncher) bool { return l.Name == p.Name })
	if i < 0 || (stored[i].Kind == "api" && !messagesAPI(stored[i].API)) {
		return nil, &rpc.Error{Code: proto.CodeLauncherOutOfScope, Message: i18n.T(lang, "assistant-models_launchers.err_pull_unknown", i18n.Args{"provider": p.Name})}
	}
	l := stored[i]
	if key := s.scopeReason(l, machine); key != "" {
		return nil, &rpc.Error{Code: proto.CodeLauncherOutOfScope, Message: s.scopeText(ctx, lang, key, l, machine)}
	}
	if p.OwnService != nil && p.OwnService.Known {
		s.models.ownMu.Lock()
		if s.models.own == nil {
			s.models.own = map[string]ownSeen{}
		}
		s.models.own[machine+"/"+l.AgentType()] = ownSeen{st: *p.OwnService, at: time.Now()}
		s.models.ownMu.Unlock()
	}
	if l.SkipsOwnService() && p.OwnService != nil && p.OwnService.Known && p.OwnService.Own {
		return nil, s.ownServiceErr(ctx, lang, l, machine)
	}
	keys, err := s.readMachineKeys(ctx)
	if err != nil {
		return nil, err
	}
	full, err := s.openFor(ctx, l, keys, machine)
	if _, ref := s.keyReference(s.keySealedFor(l, keys, machine)); err != nil && ref {
		return nil, &rpc.Error{Code: proto.CodeLauncherKeyUnresolved, Message: i18n.Localize(err, lang)}
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"launcher": full}, nil
}

// ---- what a save takes ----

// scopeInput are the scope fields of POST / PUT /api/v1/launchers (left out keeps what is stored).
type scopeInput struct {
	Share         *string            `json:"share"`
	ScopeMachines *[]string          `json:"scope_machines"`
	Delivery      *string            `json:"delivery"`
	OwnService    *string            `json:"own_service"`
	MachineKeys   map[string]*string `json:"machine_keys"` // machine → key; "" (or null) removes it
	// MachineKeyReferences: machine → a reference in place of its key (launcher_secrets.go); "" (or null) removes it.
	MachineKeyReferences map[string]*string `json:"machine_key_references"`
}

var errScopeEmpty = i18n.Err("assistant-models_launchers.err_scope_empty")

// checkScope: the values are known ones, and the machines named are the user's (when the list can be read).
func (s *Service) checkScope(ctx context.Context, in scopeInput) error {
	if in.Share != nil && !slices.Contains([]string{shareAll, shareSelected, shareNone}, *in.Share) {
		return i18n.Err("assistant-models_launchers.err_share")
	}
	if in.Delivery != nil && *in.Delivery != proto.DeliveryOnDemand && *in.Delivery != proto.DeliveryPush {
		return i18n.Err("assistant-models_launchers.err_delivery")
	}
	if in.OwnService != nil && *in.OwnService != proto.OwnServiceSkip && *in.OwnService != proto.OwnServiceUse {
		return i18n.Err("assistant-models_launchers.err_own_service_value")
	}
	named := []string{}
	if in.ScopeMachines != nil {
		named = append(named, *in.ScopeMachines...)
	}
	for m := range in.MachineKeys {
		named = append(named, m)
	}
	for m := range in.MachineKeyReferences {
		named = append(named, m)
	}
	if len(named) == 0 {
		return nil
	}
	devices := s.devices(ctx)
	if len(devices) == 0 {
		return nil // the hub does not answer: not the request's fault
	}
	for _, id := range named {
		if !slices.ContainsFunc(devices, func(d Device) bool { return d.ID == id || s.sameMachine(d.ID, id) }) {
			return i18n.Err("assistant-models_launchers.err_scope_unknown_machine", i18n.Args{"machine": strconv.Quote(id)})
		}
	}
	return nil
}

// applyScope writes the scope fields given into l; a new provider (isNew) takes the defaults for what is left out:
// every computer, on demand, skipping computers with their own service unless it is assigned to purposes right away
// or is a direct API connection (picked for what the manager calls itself, not to fill a computer's gap).
func (in scopeInput) applyScope(l *storedLauncher, isNew, assigned bool) error {
	if isNew {
		l.Share, l.Delivery, l.OwnService = shareAll, proto.DeliveryOnDemand, proto.OwnServiceSkip
		if assigned || l.Kind == "api" {
			l.OwnService = proto.OwnServiceUse
		}
	}
	explicit := in.Share != nil || in.ScopeMachines != nil || in.Delivery != nil || in.OwnService != nil
	if explicit && l.Share == "" { // a record from before scopes becomes explicit as it read
		l.Share, l.Delivery, l.OwnService = shareAll, l.delivery(), l.ownService()
	}
	if in.Share != nil {
		l.Share = *in.Share
	}
	if in.ScopeMachines != nil {
		var ids []string
		for _, id := range *in.ScopeMachines {
			if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		l.ScopeMachines = ids
	}
	if in.Delivery != nil {
		l.Delivery = *in.Delivery
	}
	if in.OwnService != nil {
		l.OwnService = *in.OwnService
	}
	if l.Share == shareSelected && len(l.ScopeMachines) == 0 {
		return errScopeEmpty
	}
	return nil
}

// withMachineKeys is keys with provider name's changes from in sealed in (nil when there are none): keys, and
// references (refs, checked and sealed in sealedRefs by sealKeyReferences).
func (s *Service) withMachineKeys(keys []machineKey, name string, in, refs map[string]*string, sealedRefs map[string]string) ([]machineKey, bool, error) {
	if len(in) == 0 && len(refs) == 0 {
		return keys, false, nil
	}
	out := slices.Clone(keys)
	drop := func(machine string) {
		out = slices.DeleteFunc(out, func(k machineKey) bool { return k.Launcher == name && k.Machine == machine })
	}
	for machine, v := range in {
		if !keyGiven(v) && keyGiven(refs[machine]) {
			continue // the reference below takes its place
		}
		drop(machine)
		if !keyGiven(v) {
			continue
		}
		sealed, err := s.sealKey(strings.TrimSpace(*v))
		if err != nil {
			return nil, false, err
		}
		out = append(out, machineKey{Launcher: name, Machine: machine, KeySealed: sealed})
	}
	for machine, v := range refs {
		if keyGiven(in[machine]) {
			continue // check() refused both; a key given wins
		}
		drop(machine)
		if sealed := sealedRefs[machine]; keyGiven(v) && sealed != "" {
			out = append(out, machineKey{Launcher: name, Machine: machine, KeySealed: sealed})
		}
	}
	return out, true, nil
}

// withoutMachineKeys is keys without provider name's.
func withoutMachineKeys(keys []machineKey, name string) []machineKey {
	return slices.DeleteFunc(slices.Clone(keys), func(k machineKey) bool { return k.Launcher == name })
}

// purposeScopeStatus is Model configuration's word for a launcher purpose bound to machine whose provider is out of
// that computer's scope ("" when it is not).
func (s *Service) purposeScopeStatus(ctx context.Context, lang i18n.Lang, provider, machine string) string {
	name, ok := strings.CutPrefix(provider, "launcher.")
	if !ok || machine == "" {
		return ""
	}
	l, ok := s.managedNamed(ctx, name)
	if !ok {
		return ""
	}
	if key := s.scopeReason(l, machine); key != "" {
		return s.scopeText(ctx, lang, key, l, machine)
	}
	if l.SkipsOwnService() {
		if st, ok := s.ownServiceCached(machine, l.AgentType()); ok && st.Known && st.Own {
			var re *rpc.Error
			if errors.As(s.ownServiceErr(ctx, lang, l, machine), &re) {
				return re.Message
			}
		}
	}
	return ""
}
