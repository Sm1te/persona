package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- a new Agent's launcher on the computer it starts on (#2640) ----
//
// Model configuration › Agents (agent.new) is the one launcher purpose with no computer, and a model profile's tiers
// have none either: their launcher may be a ~/.zshrc function or a wrapper script only some computers have. Every new
// Agent (a task, agent_start, New Agent in the apps, a message resuming an Agent started outside Persona) is held to
// what its computer can run before the start goes out, rather than the terminal answering "command not found"
// (docs/server/launcher-health.md, "A launcher the computer doesn't have").

// startPick is what a start's caller named itself, before a tier and Model configuration › Agents fill in the rest.
type startPick struct{ launcher, effort string }

// pickOf is the caller's own pick in a start's params.
func pickOf(params map[string]any) startPick {
	l, _ := params["launcher"].(string)
	e, _ := params["effort"].(string)
	return startPick{launcher: strings.TrimSpace(l), effort: strings.TrimSpace(e)}
}

// resolveStart fills params (a session.start's, with the caller's own launcher, model, effort and tier, if any) with
// what the new Agent runs on: the tier, then Model configuration › Agents. It answers the caller's own pick, for
// fitLauncher.
func (s *Service) resolveStart(ctx context.Context, params map[string]any) (startPick, error) {
	own := pickOf(params)
	if err := s.applyStartTier(ctx, params); err != nil {
		return own, err
	}
	return own, s.applyModel(ctx, "agent.new", params)
}

// fitLauncher holds params' launcher (resolveStart's) to what computer m can start. One the caller named that m does
// not have is refused, naming the ones it has. One a default gave (the agent.new binding, a profile, a tier) gives way
// to one m has (launcherInstead), with that launcher's own default model and the caller's effort; note says so in the
// user's language, for the task's why and the tool's answer. A list that can't be read (m offline, a timeout) counts
// as having it, as mainChatFit does: nothing is refused or replaced on a guess.
func (s *Service) fitLauncher(ctx context.Context, m Device, own startPick, params map[string]any) (note string, err error) {
	name, _ := params["launcher"].(string)
	if s.launcherEverywhere(ctx, name) {
		return "", nil
	}
	list, known := s.launchersHere(withUnhidden(ctx, name), m.ID) // a hidden default still runs (#4659)
	if !known || slices.ContainsFunc(list, func(l proto.Launcher) bool { return l.Name == name }) {
		return "", nil
	}
	if own.launcher != "" {
		return "", i18n.Err("assistant-tasks.launcher_not_on_machine", i18n.Args{
			"machine": firstNonEmpty(m.Name, m.ID), "launcher": name, "launchers": strings.Join(launcherNamesOn(m, list), ", "),
		})
	}
	to := launcherInstead(m, list)
	for _, k := range []string{"model", "effort", "model_fingerprint", "runtime"} {
		delete(params, k)
	}
	params["launcher"] = to
	if own.effort != "" {
		params["effort"] = own.effort
	}
	if err := s.applyModel(ctx, "agent.new", params); err != nil {
		return "", err
	}
	s.logf("start on %s: launcher %s is not there, using %s", m.ID, name, to)
	return i18n.T(s.lang(ctx), "assistant-tasks.launcher_swapped", i18n.Args{
		"launcher": name, "machine": firstNonEmpty(m.Name, m.ID), "to": to,
	}), nil
}

// launchersHere is what machine can start: machineLaunchers (its own launchers but the hidden ones, and the managed
// ones) and those hub.json names for it (a wrapper the node may not list, configuredLauncher). known false when its
// list can't be read, or the answer names none of its own: a node that read its shell lists plain claude at least
// (node mergeLaunchers), so such an answer is no list.
func (s *Service) launchersHere(ctx context.Context, machine string) (list []proto.Launcher, known bool) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list, err := s.machineLaunchers(cctx, machine)
	if err != nil || !slices.ContainsFunc(list, func(l proto.Launcher) bool { return compat.Source(l.Source) != "persona" }) {
		return nil, false
	}
	for _, c := range s.hello(ctx).Models.Launchers[machine] {
		if c.Name != "" && !slices.ContainsFunc(list, func(l proto.Launcher) bool { return l.Name == c.Name }) {
			list = append(list, c.Launcher)
		}
	}
	return list, true
}

// launcherEverywhere: every machine has name (plain claude; a managed launcher, which the node runs through personad
// launch), so its list need not be asked.
func (s *Service) launcherEverywhere(ctx context.Context, name string) bool {
	return name == "" || name == "claude" || slices.ContainsFunc(s.storedLaunchers(ctx), func(l storedLauncher) bool { return l.Name == name })
}

// launcherInstead is the launcher a default m does not have gives way to: plain claude when m has Claude Code (its
// node said a version, or its list names claude), else the first in its list that starts an agent with a prompt (not a
// runtime's), else claude all the same.
func launcherInstead(m Device, list []proto.Launcher) string {
	if m.ClaudeVersion != "" || slices.ContainsFunc(list, func(l proto.Launcher) bool { return l.Name == "claude" }) {
		return "claude"
	}
	if i := slices.IndexFunc(list, startableLauncher); i >= 0 {
		return list[i].Name
	}
	return "claude"
}

// startableLauncher: l starts an agent with a prompt: one of the machine's own, or a managed one (healthLaunchers).
func startableLauncher(l proto.Launcher) bool {
	return l.Name != "" && (l.Source == "" || machineSource(l.Source) || compat.Source(l.Source) == "persona")
}

// launcherNamesOn are the names a refusal lists: plain claude first when m has Claude Code, then its list's.
func launcherNamesOn(m Device, list []proto.Launcher) []string {
	var names []string
	if m.ClaudeVersion != "" {
		names = append(names, "claude")
	}
	for _, l := range list {
		if l.Name != "" && !slices.Contains(names, l.Name) {
			names = append(names, l.Name)
		}
	}
	if len(names) == 0 {
		names = append(names, "claude")
	}
	return names
}
