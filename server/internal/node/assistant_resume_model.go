package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Claude Code resumes a session on the model the session last ran on, unless --model, ANTHROPIC_MODEL or one of the
// ANTHROPIC_DEFAULT_*_MODEL variables names one; a "model" in its settings does not stop that. An assistant
// conversation whose model configuration names no model runs on its launcher's default, and the app says so. Resumed
// without --model it kept the model of an earlier start instead, and once the gateway dropped that model every turn
// failed while the app still said "default" (#2487). resumeModel says what to pass instead.

// claudeModelVars make Claude Code run the model they name rather than the resumed session's.
var claudeModelVars = []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}

// resumeModel is the --model a Claude Code conversation resumed in cwd through launcher with no configured model
// starts with (settings: the --settings it gets): what a new session of that launcher would run on, the "model" of
// Claude Code's settings (managed, --settings, the project's local and shared ones, the user's, in that order), else
// "default". "" when the launcher names a model itself (its --model, a model variable in its environment, the shell's
// or a settings env), so Claude Code runs that one anyway, and when how it starts cannot be seen: a managed launcher
// (it always names its model), a wrapper script, a probe that failed.
func (n *Node) resumeModel(launcher, cwd, settings, configDir string) string {
	if _, managed := n.managedLauncher(launcher); managed {
		return ""
	}
	model, _ := n.resumeModelOf(launcher, cwd, settings, configDir)
	return model
}

// resumeModelOf is resumeModel for a launcher that is not managed, and whether what it starts claude with could be
// seen: false for a launcher the probe cannot follow (a wrapper script, a probe that failed or found another agent)
// and for a settings "model" that is no model id; "" with true when the launcher names its model itself.
func (n *Node) resumeModelOf(launcher, cwd, settings, configDir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := n.launcherProbe(ctx, launcher)
	if err != nil || p.Agent != "claude" || p.Shell == nil {
		return "", false
	}
	if indexOf(p.Args, "-m", "--model") >= 0 || slices.ContainsFunc(p.Args, func(a string) bool { return strings.HasPrefix(a, "--model=") }) {
		return "", true
	}
	env := make(map[string]any, len(p.Shell)+len(p.Env))
	for _, vars := range []map[string]string{p.Shell, p.Env} {
		for k, v := range vars {
			env[k] = v
		}
	}
	configDir = proto.ExpandHome(configDir, n.home)
	if settings == "" {
		settings = settingsArg(p.Args)
	}
	// Highest first: what the administrator sets, then --settings, then the project's, then the user's.
	var files []string
	files = append(files, claudeManagedSettings...)
	for _, d := range claudeManagedSettingsDirs {
		if ents, err := os.ReadDir(d); err == nil {
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".json") {
					files = append(files, filepath.Join(d, e.Name()))
				}
			}
		}
	}
	if settings != "" {
		files = append(files, settings)
	}
	files = append(files, filepath.Join(cwd, ".claude", "settings.local.json"), filepath.Join(cwd, ".claude", "settings.json"),
		filepath.Join(configDir, "settings.json"))
	model := ""
	for _, f := range files {
		s, err := readLauncherSettings(f, cwd, n.home)
		if err != nil {
			continue
		}
		if senv, ok := s["env"].(map[string]any); ok {
			for k, v := range senv {
				env[k] = v
			}
		}
		if m, ok := s["model"].(string); ok && model == "" {
			model = strings.TrimSpace(m)
		}
	}
	for _, k := range claudeModelVars {
		if v, _ := env[k].(string); v != "" {
			return "", true
		}
	}
	if model == "" {
		return "default", true
	}
	if !proto.ValidModelID(model) {
		return "", false
	}
	return model, true
}
