package node

import (
	"context"

	"github.com/nwverse/persona/server/internal/host"
)

// Every tab Persona opens for an agent is marked with personaTabToken (herdr keeps a pane's metadata tokens until the
// pane goes; a herdr restart drops them). When the agent exits — by itself, by /exit, or stopped by Persona
// (retire.go) — the tab stays with the shell it ran in: Persona never closes an agent's terminal on its own, only the
// user does (#4754, docs/integrations/engines.md "Agents run in a terminal"). Until then the node closed such a tab
// once its shell had sat untouched for ten minutes (#851); that sweep is gone. The mark names the session the
// terminal was opened for.

// personaTabToken marks a pane Persona opened for an agent; its value is the session id when the start knew it.
const personaTabToken = "persona_tab"

// markPersonaTab marks pane as one Persona opened for session id (empty when not known yet).
func markPersonaTab(ctx context.Context, launch host.Launcher, pane host.PaneRef, id string) {
	r, ok := launch.(host.MetadataReporter)
	if !ok || pane.ID == "" {
		return
	}
	if id == "" {
		id = "agent"
	}
	_ = r.ReportMetadata(ctx, pane, "persona", map[string]*string{personaTabToken: &id})
}
