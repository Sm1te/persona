package assistant

import (
	"context"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// agentRequest answers the hub's calls of the Agents business (assistantwire/agents.go) with s.
func (b *BusHub) agentRequest(ctx context.Context, s *Service, method string, decode func(any) error) (any, error) {
	switch method {
	case assistantwire.MethodAgentStartParams:
		var p AgentStartParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		ctx, note := withLauncherNote(ctx)
		params, err := s.AgentStartParams(ctx, p)
		if err != nil {
			return nil, err
		}
		if *note != "" {
			return map[string]any{"params": params, "launcher_note": *note}, nil
		}
		return map[string]any{"params": params}, nil
	case assistantwire.MethodAgentResumeParams:
		var p AgentResumeParams
		if err := decode(&p); err != nil {
			return nil, err
		}
		params, err := s.AgentResumeParams(ctx, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"params": params}, nil
	default: // MethodAgentTabName
		var p struct {
			Prompt string `json:"prompt"`
		}
		if err := decode(&p); err != nil {
			return nil, err
		}
		return map[string]any{"name": s.tabName(ctx, p.Prompt)}, nil
	}
}
