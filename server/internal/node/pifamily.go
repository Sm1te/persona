package node

import "time"

// The agents Persona reads as pi (pi.go): pi itself, and omp (Oh My Pi), a pi fork whose extension API, session files and
// conversation model are pi's (docs/integrations/omp.md). A pane and its keys are the host's; the conversation and the
// control (send, interrupt, cards, live text) are the Persona extension in that same process. Both start in herdr and
// resume there, with their own command.

const (
	piAgent  = "pi"
	ompAgent = "omp"
)

// piFamily says whether agent is one Persona reads as pi.
func piFamily(agent string) bool { return agent == piAgent || agent == ompAgent }

// piFamilyNoExtension is what a running agent of the family says without Persona's extension: its terminal only, and
// how to install the extension.
func piFamilyNoExtension(agent string) string {
	if agent == ompAgent {
		return "这个 omp 没有装 Persona 的扩展：手机上只能看终端。装上（omp install <Persona>/integrations/omp）再重开 omp，就能看对话、发消息"
	}
	return piNoExtension
}

// piFamilyCode is the code a refused send to a running agent of the family carries without its extension (pi's own
// code is pi_no_extension, unchanged).
func piFamilyCode(agent string) string { return agent + "_no_extension" }

// piFamilyIDGrace is how long a start of the family waits for the extension to report the new session's id after the
// host has reported its pane: without one the pane's fallback id (omp-w1-p2) is returned instead.
var piFamilyIDGrace = 8 * time.Second

// paneAlias is a fallback row id (omp-w1-p2) and the pane it is the row of.
type paneAlias struct{ id, pane string }

// resolveAlias turns a fallback row id a client opened (or sent to) into the session its pane turned out to run.
func (n *Node) resolveAlias(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if a, ok := n.idAlias[id]; ok {
		return a.id
	}
	return id
}
