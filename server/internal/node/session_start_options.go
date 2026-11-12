package node

import (
	"context"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// workStartOptions describes the exact path session.start.work will take. A runtime
// advertising a launcher or a model list does not promise to apply a model override.
type workStartOptions struct {
	AgentType      string             `json:"agent_type"`
	ModelMode      string             `json:"model_mode"` // selectable, fixed, default
	Models         []proto.AgentModel `json:"models,omitempty"`
	FixedModel     string             `json:"fixed_model,omitempty"`
	InlinePrompt   bool               `json:"inline_prompt"`
	PromptRequired bool               `json:"prompt_required"`
	Attachments    bool               `json:"attachments"`

	runtime      *runtimeConn
	runtimeModel string
	host         string
}

func (n *Node) workSessionOptions(ctx context.Context, name, configuredAgent, host string, configured, readCatalog bool) (workStartOptions, error) {
	opts := workStartOptions{ModelMode: "default"}
	if !nameRe.MatchString(name) {
		return opts, workStartRefused("work_launcher_unavailable", "Choose an available launcher on this computer.")
	}
	if runtime, model := n.startRuntime(name); runtime != nil {
		opts.runtime, opts.runtimeModel = runtime, model
		opts.AgentType = configuredAgent
		if l, ok := runtime.launcherNamed(name); ok && opts.AgentType == "" {
			opts.AgentType = l.Agent
		}
		if m, ok := n.managedLauncher(name); ok && opts.AgentType == "" {
			opts.AgentType = m.AgentType()
		}
		if opts.AgentType == "" {
			opts.AgentType = launcherAgentType(name)
		}
		opts.PromptRequired = opts.AgentType == "codex"
		opts.InlinePrompt = true
		return opts, nil
	}
	ctx = withStartHost(ctx, host)
	kind := n.startKind(ctx)
	opts.host = kind
	if err := n.checkStartHost(kind); err != nil {
		return opts, workStartRefused("work_launcher_unavailable", "The session host cannot start this launcher.")
	}
	if _, isRuntime := engineRuntimes[kind]; isRuntime {
		// startOnEngineRuntime would fall back to its own default provider when
		// it cannot find this name. Do not offer that as the selected harness.
		return opts, workStartRefused("work_launcher_unavailable", "Choose a launcher offered by the active runtime.")
	}
	if _, err := n.launcherFor(kind); err != nil {
		return opts, workStartRefused("work_launcher_unavailable", "No session host can start this launcher.")
	}
	managed, isManaged := n.managedLauncher(name)
	if isManaged && managed.AgentType() == "agy" {
		return opts, workStartRefused("work_launcher_unavailable", "The launcher runtime is offline.")
	}
	list, err := n.launchers(ctx)
	found := slices.IndexFunc(list, func(l proto.Launcher) bool { return l.Name == name })
	if !configured && !isManaged && (err != nil || found < 0) {
		return opts, workStartRefused("work_launcher_unavailable", "This computer does not offer that launcher.")
	}

	opts.AgentType = configuredAgent
	if opts.AgentType == "" && found >= 0 {
		opts.AgentType = list[found].Agent
	}
	if opts.AgentType == "" {
		opts.AgentType = launcherAgentType(name)
	}
	if isManaged {
		opts.AgentType = managed.AgentType()
	}
	argv, codex := n.codexLauncher(ctx, name)
	if codex {
		opts.AgentType = "codex"
	}
	if !slices.Contains([]string{"claude", "codex", traexAgent, ompAgent}, opts.AgentType) ||
		(opts.AgentType == "codex" && !codex) || (opts.AgentType == ompAgent && name != ompAgent) {
		return opts, workStartRefused("work_launcher_unavailable", "This harness cannot start through the selected launcher.")
	}
	command := name
	if isManaged {
		command = opts.AgentType
	}
	if err := loginShell(ctx, "command -v "+shellQuote(command)+" >/dev/null").Run(); err != nil {
		return opts, workStartRefused("work_launcher_unavailable", "The launcher command is missing on this computer.")
	}
	// A Codex in a terminal starts with no first message too: it waits in its box for one (codex_waiting.go).
	opts.PromptRequired = false
	opts.InlinePrompt = opts.AgentType != "claude"
	opts.Attachments = opts.AgentType == "claude"
	if !codex && opts.AgentType != "claude" {
		return opts, nil
	}
	if codex && codexOnRemote(argv) {
		// The launcher-specific model probe starts a local app-server; it does
		// not certify the remote server's catalog.
		return opts, nil
	}
	if !codex && !isManaged {
		probe, err := n.launcherProbe(ctx, name, "--model", modelCheckProbe)
		if err != nil {
			if !nativeClaudeFile(name, probe.File) {
				return opts, nil // no positive proof this launcher forwards the flag
			}
			probe = Probe{Agent: "claude", Args: []string{"--model", modelCheckProbe}}
		}
		if probe.Agent != "claude" {
			return opts, workStartRefused("work_launcher_unavailable", "The launcher does not start the advertised harness.")
		}
		if fixed := fixedWorkModel(probe.Args); fixed != "" {
			opts.ModelMode, opts.FixedModel = "fixed", fixed
			return opts, nil
		}
		if forwardedModel(probe, modelCheckProbe) != nil {
			return opts, nil
		}
	}
	if !readCatalog {
		return opts, nil
	}
	catalog, err := n.launcherModels(ctx, name, opts.AgentType, false)
	if err == nil && catalog.Agent == opts.AgentType && catalog.Locked == "" {
		opts.Models = slices.DeleteFunc(slices.Clone(catalog.Models), func(m proto.AgentModel) bool {
			// Claude's "default" is a menu action, not an explicit model ID.
			return m.Custom || m.ID == "default" || !proto.ValidModelID(m.ID)
		})
		if len(opts.Models) != 0 {
			opts.ModelMode = "selectable"
		}
	}
	return opts, nil
}

// launcherProbe cannot stub a native executable. A plain Claude Code
// executable (or its npm entrypoint) has the native CLI argument contract;
// an arbitrary shell wrapper that could not be inspected does not.
func nativeClaudeFile(name, path string) bool {
	if name != "claude" || path == "" {
		return false
	}
	head, err := scriptHead(path)
	if err != nil {
		return err.Error() == "not a script"
	}
	line, _, _ := strings.Cut(head, "\n")
	return strings.HasSuffix(line, "/node") || strings.HasSuffix(line, " node")
}

func fixedWorkModel(args []string) string {
	for i, arg := range args {
		var model string
		if (arg == "-m" || arg == "--model") && i+1 < len(args) {
			model = args[i+1]
		} else if v, ok := strings.CutPrefix(arg, "--model="); ok {
			model = v
		}
		if model != modelCheckProbe && model != "" {
			return model
		}
	}
	return ""
}

func (o workStartOptions) validate(agent, model, prompt string, async bool) error {
	if agent != o.AgentType {
		return workStartRefused("work_harness_changed", "The selected launcher no longer starts that harness.")
	}
	if model != "" && (o.ModelMode != "selectable" || !slices.ContainsFunc(o.Models, func(m proto.AgentModel) bool { return m.ID == model })) {
		return workStartRefused("work_model_unavailable", "The selected launcher cannot start that model.")
	}
	if o.PromptRequired && strings.TrimSpace(prompt) == "" {
		if async && o.AgentType == "codex" {
			return workStartRefused("codex_needs_prompt", "Codex needs the first message to start a session.")
		}
		return workStartRefused("work_prompt_required", "This harness needs a first message.")
	}
	return nil
}

func workStartRefused(code, message string) error { return refusal(code, message, nil) }
