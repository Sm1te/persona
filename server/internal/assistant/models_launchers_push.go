package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the nodes ----

// managedNodeCalls is the new-connection configuration callback: the providers in the machine's scope
// (launchers.catalog), or for a node from before the catalog the same with their secrets (launchers.managed). A failed
// read sends nothing.
func (s *Service) managedNodeCalls(ctx context.Context, machine string) []devicewire.NodeCall {
	if s.machinePending(ctx, machine) {
		return nil
	}
	s.models.mu.Lock()
	stored, err := s.readStoredLaunchers(ctx)
	var keys []machineKey
	if err == nil {
		keys, err = s.readMachineKeys(ctx)
	}
	s.models.mu.Unlock()
	if err != nil {
		s.logf("launchers: prepare node configuration: %v", err)
		return nil
	}
	if _, err := s.nodeLaunchers(stored); err != nil {
		s.logf("launchers: prepare node configuration: %v", err)
		return nil
	}
	catalog, legacy, err := s.nodeLaunchersFor(ctx, stored, keys, machine)
	if err != nil {
		s.logf("launchers: prepare node configuration: %v", err)
		return nil
	}
	c, err := json.Marshal(map[string]any{"launchers": catalog})
	if err != nil {
		return nil
	}
	l, err := json.Marshal(map[string]any{"launchers": legacy})
	if err != nil {
		return nil
	}
	return []devicewire.NodeCall{{MachineID: machine, Method: DeviceLaunchersCatalog, Params: c,
		Fallback: &devicewire.NodeCall{MachineID: machine, Method: DeviceLaunchersManaged, Params: l}}}
}

// managedForNodes: the providers with their secrets as every node from before the catalog would get them (no scope
// applied), the check that every secret opens.
func (s *Service) managedForNodes(ctx context.Context) ([]proto.ManagedLauncher, error) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	stored, err := s.readStoredLaunchers(ctx)
	if err != nil {
		return nil, err
	}
	return s.nodeLaunchers(stored)
}

// nodeLaunchers opens every provider a node can run (only Messages direct API connections besides launchers): one that
// does not open holds every publication back (storeManagedLaunchers), as before scopes.
func (s *Service) nodeLaunchers(stored []storedLauncher) ([]proto.ManagedLauncher, error) {
	out := []proto.ManagedLauncher{}
	for _, l := range stored {
		if l.Kind == "api" && !messagesAPI(l.API) {
			continue
		}
		m, err := s.openStoredLauncher(l)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Service) openStoredLauncher(l storedLauncher) (proto.ManagedLauncher, error) {
	m := l.ManagedLauncher
	m.BaseURL = m.LaunchBaseURL() // a node from before it normalized one stored with a trailing /v1 itself (#4886)
	var err error
	if m.APIKey, err = s.openKey(l.KeySealed); err != nil {
		return m, i18n.Err("assistant-models.err_key_decrypt")
	}
	if _, ok := extension.ReferenceOf(m.APIKey); ok {
		m.APIKey = "" // a reference: only openFor resolves it, for the computer that pulls it (launcher_secrets.go)
	}
	if m.Headers, err = s.openKey(l.HeadersSealed); err != nil {
		return m, i18n.Err("assistant-models.err_key_decrypt")
	}
	if m.Env, err = s.openKey(l.EnvSealed); err != nil {
		return m, i18n.Err("assistant-models.err_key_decrypt")
	}
	m.HasHeaders, m.HasEnv = m.Headers != "", m.Env != ""
	return m, nil
}

// pushManaged tells the online nodes (one machine when machine is not "") the providers in their scope; a node's
// new connection gets them right after it is configured (main.go's devices NodeCalls). Only this manager's own
// machines: the hub's devices.list has no other (docs/assistant/managers.md), and a new connection the hub says is another
// manager's gets no NodeCalls (internal/devices hubConnected). A computer waiting to be confirmed gets none
// (machine_trust.go; the hub does not list it either).
func (s *Service) pushManaged(ctx context.Context, machine string) {
	s.models.mu.Lock()
	defer s.models.mu.Unlock()
	stored, err := s.readStoredLaunchers(ctx)
	if err != nil {
		s.logf("launchers: read managed launchers: %v", err)
		return
	}
	keys, err := s.readMachineKeys(ctx)
	if err != nil {
		s.logf("launchers: read the computers' keys: %v", err)
		return
	}
	if _, err := s.nodeLaunchers(stored); err != nil {
		s.logf("launchers: prepare managed launchers: %v", err)
		return
	}
	s.pushNodeLaunchers(ctx, machine, stored, keys)
}

// pushNodeLaunchers publishes a confirmed snapshot while the caller holds models.mu.
func (s *Service) pushNodeLaunchers(ctx context.Context, machine string, stored []storedLauncher, keys []machineKey) {
	for _, d := range s.devices(ctx) {
		if !d.Online || (machine != "" && d.ID != machine) || s.machinePending(ctx, d.ID) {
			continue
		}
		s.giveNodeLaunchers(ctx, d.ID, stored, keys)
	}
}

// zshrcLaunchers is one machine's rc-file launchers, and how they came in (Sync.State "" before any ask).
type zshrcLaunchers struct {
	MachineID string
	Launchers []proto.Launcher
	Sync      LauncherSync
	// ClaudeVersion: Claude Code's version there, as the hub knows it ("" when it has none or never said).
	ClaudeVersion string
}

// launcherSyncMetadata is the last persisted answer's shell, rc file and time, before a new answer's state is known.
func launcherSyncMetadata(d Device, m launcherMeta) LauncherSync {
	state := LauncherSync{Machine: d.ID, Name: d.Name, Shell: m.Shell, RCFile: cmp.Or(m.RCFile, defaultRCFile)}
	if at := m.SyncedAt; !at.IsZero() {
		state.SyncedAt = &at
	}
	return state
}

func zshrcLauncherSnapshot(devices []Device, seen map[string][]proto.Launcher, states map[string]LauncherSync) []zshrcLaunchers {
	out := make([]zshrcLaunchers, 0, len(devices))
	for _, d := range devices {
		out = append(out, zshrcLaunchers{MachineID: d.ID, Launchers: seen[d.ID], Sync: states[d.ID], ClaudeVersion: d.ClaudeVersion})
	}
	return out
}

// zshrcLaunchersLastSeen answers the persisted snapshot without asking a node, and whether one existed. Sync's state
// stays empty until a refresh has one, while its persisted shell, rc file and time still describe the last answer.
func (s *Service) zshrcLaunchersLastSeen(ctx context.Context) ([]zshrcLaunchers, bool) {
	st := &s.models
	st.zshrcMu.Lock()
	seen, states := st.zshrc, st.zshrcSync
	st.zshrcMu.Unlock()
	cached := seen != nil
	devices := s.devices(ctx)
	if seen == nil {
		seen = map[string][]proto.Launcher{}
		meta := map[string]launcherMeta{}
		if store := s.store(); store != nil {
			found, _ := store.Setting(ctx, launchersZshrcKey, &seen)
			foundMeta, _ := store.Setting(ctx, launchersMetaKey, &meta)
			cached = found || foundMeta
		}
		states = map[string]LauncherSync{}
		for _, d := range devices {
			m, hasMeta := meta[d.ID]
			if !hasMeta && d.Online {
				continue
			}
			state := LauncherSync{Machine: d.ID, Name: d.Name}
			if hasMeta {
				state = launcherSyncMetadata(d, m)
			}
			if !d.Online {
				state.State = "offline"
			}
			states[d.ID] = state
		}
		st.zshrcMu.Lock()
		if st.zshrc != nil {
			seen, states = st.zshrc, st.zshrcSync
			cached = true
		} else if cached {
			st.zshrc, st.zshrcSync = seen, states
		}
		st.zshrcMu.Unlock()
	}
	return zshrcLauncherSnapshot(devices, seen, states), cached
}

// refreshZshrcLaunchers runs one bounded refresh behind the request; its result is visible to a later read.
func (s *Service) refreshZshrcLaunchers(ctx context.Context) {
	st := &s.models
	st.zshrcMu.Lock()
	if st.zshrcRefreshing {
		st.zshrcMu.Unlock()
		return
	}
	st.zshrcRefreshing = true
	st.zshrcMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), launcherSyncTimeout+5*time.Second)
		defer cancel()
		defer func() {
			st.zshrcMu.Lock()
			st.zshrcRefreshing = false
			st.zshrcMu.Unlock()
		}()
		s.zshrcLaunchers(ctx, true)
	}()
}

// zshrcLaunchers is every machine's rc-file launchers (~/.zshrc, ~/.bashrc) in the machines' order: as last seen (an
// offline one's from the last time, kept in settings launchers.zshrc), or asked of the online nodes again first
// (refresh, or nothing seen since the assistant started; the node caches a minute). Each machine gets
// launcherSyncTimeout, and its Sync says whether its list came in now.
func (s *Service) zshrcLaunchers(ctx context.Context, refresh bool) []zshrcLaunchers {
	st := &s.models
	st.zshrcMu.Lock()
	seen, states := st.zshrc, st.zshrcSync
	st.zshrcMu.Unlock()
	devices := s.devices(ctx)
	if seen == nil || refresh {
		seen = map[string][]proto.Launcher{}
		meta := map[string]launcherMeta{}
		if store := s.store(); store != nil {
			_, _ = store.Setting(ctx, launchersZshrcKey, &seen)
			_, _ = store.Setting(ctx, launchersMetaKey, &meta)
		}
		states = map[string]LauncherSync{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		changed := false
		for _, d := range devices {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := s.listLaunchers(ctx, d)
				mu.Lock()
				defer mu.Unlock()
				state := LauncherSync{Machine: d.ID, Name: d.Name, State: "ok"}
				if err != nil {
					state.State, state.Error = launcherSyncState(ctx, err)
				} else {
					if !slices.Equal(seen[d.ID], res.Launchers) {
						seen[d.ID] = res.Launchers
					}
					meta[d.ID], changed = launcherMeta{Shell: res.Shell, RCFile: res.RCFile, SyncedAt: time.Now()}, true
				}
				if m, ok := meta[d.ID]; ok {
					metadata := launcherSyncMetadata(d, m)
					metadata.State, metadata.Error = state.State, state.Error
					state = metadata
				}
				states[d.ID] = state
			}()
		}
		wg.Wait()
		if store := s.store(); changed && store != nil {
			_ = store.PutSetting(ctx, launchersZshrcKey, seen)
			_ = store.PutSetting(ctx, launchersMetaKey, meta)
		}
		st.zshrcMu.Lock()
		st.zshrc, st.zshrcSync = seen, states
		st.zshrcMu.Unlock()
	}
	return zshrcLauncherSnapshot(devices, seen, states)
}
