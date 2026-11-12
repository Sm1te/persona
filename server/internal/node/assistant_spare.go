package node

import (
	"context"
	"fmt"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// A spare process started when the app opens the "Chat" tab, so the first message skips claude's startup.
// It is only adopted while fresh (what it loaded at start — CLAUDE.md, memory, tools — may have changed since).
const assistantSpareMaxAge = 10 * time.Minute

func (n *Node) adoptSpareFor(cwd, launcher, mode string, options ...modelExecution) *assistantProc {
	if mode == proto.ModeBypass { // a Codex spare is fine: its policy goes with each turn
		n.mu.Lock()
		sp := n.spare
		n.mu.Unlock()
		if sp == nil || sp.agent != "codex" {
			return nil
		}
	}
	return n.adoptSpare(cwd, launcher, options...)
}

// adoptSpare hands the warm spare process to a new conversation when it fits (same directory and launcher, fresh).
func (n *Node) adoptSpare(cwd, launcher string, options ...modelExecution) *assistantProc {
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.spare
	if p == nil || !p.alive() || time.Since(p.born) > assistantSpareMaxAge ||
		((p.agent == "claude" || p.agent == "codex") && p.personaMCPOnly != n.hubSupportsPersonaContracts()) ||
		(launcher != "" && p.launcher != launcher) || (cwd != "" && p.cwd != cwd) || p.fingerprint != n.executionFingerprint(p.launcher, executionOption(options)) {
		return nil
	}
	if selected := executionOption(options).ConfigDir; selected != "" && !sameAssistantProfile(p.model.ConfigDir, selected) {
		return nil
	}
	n.spare = nil
	n.procs[p.id] = p
	return p
}

// assistantWarm makes sure a fresh spare process is waiting for the next new conversation.
func (n *Node) assistantWarm(cwd, launcher string, options ...modelExecution) error {
	if n.assistantRuntime(launcher) != nil { // a runtime is running already: nothing to start ahead
		if executionOption(options).ConfigDir != "" {
			return fmt.Errorf("config_dir is only supported for Claude assistant conversations")
		}
		return nil
	}
	execution := executionOption(options)
	if err := execution.check(); err != nil {
		return err
	}
	if launcher == "" || !shellcmd.NameRe.MatchString(launcher) {
		return fmt.Errorf("invalid launcher %q", launcher)
	}
	if err := n.ensureManaged(context.Background(), launcher); err != nil {
		return err
	}
	if agent, err := n.assistantAgent(context.Background(), launcher); err != nil {
		return err
	} else if agent == "claude" {
		execution.ConfigDir, err = n.assistantProfileSelection(context.Background(), "", launcher, execution.Runtime, execution.ConfigDir, false)
		if err != nil {
			return err
		}
	} else if execution.ConfigDir != "" {
		return fmt.Errorf("config_dir is only supported for Claude assistant conversations")
	}
	n.mu.Lock()
	old := n.spare
	if old != nil && old.alive() && old.launcher == launcher && old.cwd == cwd &&
		((old.agent != "claude" && old.agent != "codex") || old.personaMCPOnly == n.hubSupportsPersonaContracts()) &&
		(old.agent != "claude" || sameAssistantProfile(old.model.ConfigDir, execution.ConfigDir)) &&
		old.fingerprint == n.executionFingerprint(launcher, execution) && time.Since(old.born) < assistantSpareMaxAge/2 {
		n.mu.Unlock()
		return nil
	}
	n.spare = nil
	n.mu.Unlock()
	if old != nil {
		old.stop()
	}
	p, err := n.spawnAssistant("", cwd, launcher, false, true, execution)
	if err != nil {
		return err
	}
	n.mu.Lock()
	if n.spare != nil { // a concurrent warm won
		n.mu.Unlock()
		p.stop()
		return nil
	}
	n.spare = p
	n.mu.Unlock()
	return nil
}
