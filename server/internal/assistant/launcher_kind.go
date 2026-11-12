package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/rpc"
)

// A conversation keeps the agent it started on: Claude Code can't resume a Codex thread, nor Codex a Claude transcript
// (node/assistant_agent.go assistantResumeLauncher). A launcher of the other kind picked for a message in an existing
// conversation is refused here, saying so, instead of the node quietly running the message on the conversation's own
// launcher while the app shows the pick (#4662). The Main chat setting, which nobody picked for this message, still
// gives way on the node.

// launcherOtherKind refuses a pick of another agent kind; the app gets 409 launcher_other_kind with the kinds.
type launcherOtherKind struct {
	conversationAgent, launcher, launcherAgent string
}

func (e *launcherOtherKind) message() error {
	return i18n.Err("assistant-start.launcher_other_kind", i18n.Args{
		"launcher": e.launcher, "launcher_agent": harnessName(e.launcherAgent), "conversation_agent": harnessName(e.conversationAgent),
	})
}

func (e *launcherOtherKind) Error() string { return e.message().Error() }

// rpcError is the refusal as the hub passes it on (apierr.Call: 409, data flattened into the body).
func (e *launcherOtherKind) rpcError(l i18n.Lang) *rpc.Error {
	data, _ := json.Marshal(map[string]string{
		"conversation_agent": e.conversationAgent, "launcher": e.launcher, "launcher_agent": e.launcherAgent, "delivered": "none",
	})
	return &rpc.Error{Code: assistantwire.CodeLauncherOtherKind, Message: i18n.Localize(e.message(), l), Data: data}
}

// assistantKind is the conversation kind an agent name stands for: "claude", "codex", or "" (a runtime, or unknown),
// as the node tells a conversation's kind.
func assistantKind(agent string) string {
	switch agent {
	case "claude", "codex":
		return agent
	}
	return ""
}

// otherKindPick refuses launcher, picked for a message to conversation sid on machine, when it runs the other agent
// kind. Anything unknown (a conversation the machine doesn't list, a launcher list that didn't come, a runtime) lets
// the message go as before: nothing is refused on a guess.
func (s *Service) otherKindPick(ctx context.Context, machine, sid, launcher string) error {
	c, known := s.session(ctx, machine, sid)
	conv := assistantKind(c.Agent)
	if !known || conv == "" {
		return nil
	}
	picked := assistantKind(s.launcherAgent(ctx, machine, launcher))
	if picked == "" || picked == conv {
		return nil
	}
	return &launcherOtherKind{conversationAgent: conv, launcher: launcher, launcherAgent: picked}
}

// launcherAgent is the agent launcher name runs on machine: plain claude, a managed launcher's type, or the machine's
// picker entry; "" when it can't tell.
func (s *Service) launcherAgent(ctx context.Context, machine, name string) string {
	if name == "claude" || name == "codex" {
		return name
	}
	if m, managed := s.managedNamed(ctx, name); managed {
		return m.AgentType()
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	picker, err := s.pickerLaunchers(cctx, machine)
	if err != nil {
		return ""
	}
	for _, l := range picker {
		if l.Name == name {
			return l.AgentType
		}
	}
	return ""
}
