package assistant

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
)

// ---- which launchers can run the assistant's conversations ----
//
// The main chat, a scheduled task's conversation and the side conversations run headless on their machine: claude -p
// or codex app-server started through the launcher's shell command (node/assistant_start.go), or a runtime that
// declared caps.assistant. The picker (?purpose=assistant) lists assistantLaunchers (modelProviderCompatible), and the
// check before each message (sendModel) holds a launcher to the same list through mainChatFit. On top of that, the
// pickers leave out a launcher whose latest health check failed (launcher_health_check.go, withHealth): health keeps a
// message from being refused as launcher_missing on a wrong read, but a failing launcher is not refused here, and a
// model configuration that names one is left alone.

// mainChatFit is whether launcher name can run an assistant conversation on machine: in the main chat's list (fit),
// one the machine has that can't run it (refused, with why in the user's language), or not there at all (gone).
// Plain claude fits without asking; a list that can't be read fits too, as launchersHere has it: nothing is refused
// or replaced on a guess.
type mainChatFit int

const (
	mainChatFits mainChatFit = iota
	mainChatRefused
	mainChatGone
)

func (s *Service) mainChatFit(ctx context.Context, machine, name string) (mainChatFit, error) {
	if name == "" || name == "claude" {
		return mainChatFits, nil
	}
	ctx = withUnhidden(withSettingsMemo(ctx), name)
	if m, managed := s.managedNamed(ctx, name); managed { // the same on every machine in its scope: no list to ask
		allowed, err := s.assistantProviders(ctx, machine, nil)
		switch {
		case !s.usableOn(ctx, m, machine):
			return mainChatGone, nil
		case err != nil || allowed[name]:
			return mainChatFits, nil
		}
		return mainChatRefused, s.mainChatRefusal(ctx, machine, name)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	picker, err := s.pickerLaunchers(cctx, machine)
	if err != nil || !slices.ContainsFunc(picker, func(l ConfiguredLauncher) bool { return l.Source != "" && compat.Source(l.Source) != "persona" }) {
		return mainChatFits, nil // the machine's own list did not come: a shell launcher may still be there
	}
	fits, err := s.assistantLaunchersOf(cctx, machine, picker)
	named := func(l ConfiguredLauncher) bool { return l.Name == name }
	switch {
	case err != nil || slices.ContainsFunc(fits, named):
		return mainChatFits, nil
	case slices.ContainsFunc(picker, named):
		return mainChatRefused, s.mainChatRefusal(ctx, machine, name)
	}
	return mainChatGone, nil
}

// finishMainChatModel validates the binding after model resolution, including a default or a legacy fallback that
// was not the caller's explicit pick. A launcher that can't run the assistant there (gone, or not one for it) gives way
// to one of the same agent kind (mainChatInstead), never another: a Codex conversation started on Claude, or the
// other way round, ran without the configured model or credentials (#4659). The model stays when the substitute's list
// has it. With no substitute, nothing is sent and the user is told why. note says which launcher runs instead, in the
// user's language.
func (s *Service) finishMainChatModel(ctx context.Context, machine, purpose string, params map[string]any, picked bool) (note string, err error) {
	name, _ := params["launcher"].(string)
	fit, refusal := s.mainChatFit(ctx, machine, name)
	if fit == mainChatFits {
		return "", nil
	}
	launchers, err := s.assistantLaunchers(ctx, machine)
	if err != nil {
		return "", err
	}
	kind := agentKind(s.launcherAgent(withUnhidden(ctx, name), machine, name))
	d, _ := s.device(ctx, machine)
	where := firstNonEmpty(d.Name, machine)
	to := s.mainChatInstead(ctx, d, name, kind, launchers)
	if to == "" {
		if refusal != nil {
			return "", refusal
		}
		return "", i18n.Err("assistant-start.launcher_no_substitute", i18n.Args{"launcher": name, "machine": where})
	}
	model, _ := params["model"].(string)
	kept := model != "" && s.launcherHasModel(ctx, machine, to, model) // a list it can't read counts as having it
	s.logf("[%s] resolved assistant launcher %s can't run it, using %s (model kept: %v)", machine, name, to, kept)
	params["launcher"] = to
	if !kept {
		delete(params, "model")
	}
	if picked {
		err = s.applyPickedModel(ctx, params)
	} else {
		err = s.applyModel(ctx, purpose, params)
	}
	if err != nil {
		return "", err
	}
	args := i18n.Args{"launcher": name, "machine": where, "to": to, "model": model}
	key := "assistant-start.launcher_swapped"
	if model != "" && !kept {
		key = "assistant-start.launcher_swapped_model"
	}
	note = i18n.T(s.lang(ctx), key, args)
	leaveLauncherNote(ctx, note)
	return note, nil
}

// leaveLauncherNote puts note where withLauncherNote's caller reads it, if it asked.
func leaveLauncherNote(ctx context.Context, note string) {
	if slot, ok := ctx.Value(launcherNoteKey{}).(*string); ok && note != "" {
		*slot = note
	}
}

type launcherNoteKey struct{}

// withLauncherNote gives finishMainChatModel somewhere to leave its note (which launcher runs instead, and why) for
// the caller that answers the app: the start route and assistant.send_params, whose answers carry it as
// launcher_note for the app to show (#4659).
func withLauncherNote(ctx context.Context) (context.Context, *string) {
	n := new(string)
	return context.WithValue(ctx, launcherNoteKey{}, n), n
}

// mainChatInstead is the launcher of agent kind that runs the assistant on machine d in place of name: for Claude,
// plain claude first when d has Claude Code (its node said a version, or its list names claude) and the user did not
// hide it, as launcherInstead; then the main chat's list in order. A launcher whose latest health check failed (signed
// out, out of quota, …) is left out. "" when none fits.
func (s *Service) mainChatInstead(ctx context.Context, d Device, name, kind string, launchers []ConfiguredLauncher) string {
	var names []string
	if kind == "claude" && !slices.Contains(s.hiddenLaunchers(ctx), "claude") &&
		(d.ClaudeVersion != "" || slices.ContainsFunc(launchers, func(l ConfiguredLauncher) bool { return l.Name == "claude" })) {
		names = append(names, "claude")
	}
	for _, l := range launchers {
		if agentKind(cmp.Or(l.AgentType, l.Agent)) == kind && !slices.Contains(names, l.Name) {
			names = append(names, l.Name)
		}
	}
	for _, n := range names {
		if _, bad := s.launcherUnusable(ctx, d.ID, n); n != name && !bad {
			return n
		}
	}
	return ""
}

// agentKind is the agent a launcher starts: claude when it names none (or is gone: it ran the main chat's kind).
func agentKind(agent string) string { return cmp.Or(agent, "claude") }

// mainChatRefusal says that launcher can't run the assistant on machine, for the user's language.
func (s *Service) mainChatRefusal(ctx context.Context, machine, launcher string) error {
	d, _ := s.device(ctx, machine)
	return i18n.Err("assistant-start.launcher_not_main_chat", i18n.Args{"launcher": launcher, "machine": firstNonEmpty(d.Name, machine)})
}
