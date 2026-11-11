package hub

// Only hub configuration can vouch for a launcher absent from the node's list.
// This stays shared with session.start, whose work path is already a hub concern.
func setWorkLauncherConfig(m *Machine, name string, params map[string]any) {
	for _, l := range m.cfg.Launchers {
		if l.Name == name {
			params["configured"] = true
			params["configured_agent"] = l.AgentType
			return
		}
	}
}
