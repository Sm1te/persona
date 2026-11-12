package assistant

import "github.com/nwverse/persona/server/internal/i18n"

const (
	maxAgentNamingChanges  = 1000
	maxAgentNamingInterval = 8760
)

func agentNamingLimit(v Settings) int {
	if v.AgentAutoNamingMax != nil && *v.AgentAutoNamingMax >= 1 && *v.AgentAutoNamingMax <= maxAgentNamingChanges {
		return *v.AgentAutoNamingMax
	}
	return 1
}

func agentNamingInterval(v Settings) int {
	if v.AgentAutoNamingIntervalHours != nil && *v.AgentAutoNamingIntervalHours >= 0 && *v.AgentAutoNamingIntervalHours <= maxAgentNamingInterval {
		return *v.AgentAutoNamingIntervalHours
	}
	return 0
}

func validateAgentNamingSettings(v Settings) error {
	if n := v.AgentAutoNamingMax; n != nil && (*n < 1 || *n > maxAgentNamingChanges) {
		return i18n.Err("assistant-profile.bad_agent_naming_max")
	}
	if n := v.AgentAutoNamingIntervalHours; n != nil && (*n < 0 || *n > maxAgentNamingInterval) {
		return i18n.Err("assistant-profile.bad_agent_naming_interval")
	}
	return nil
}
