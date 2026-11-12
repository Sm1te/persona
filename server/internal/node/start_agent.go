package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Agents other than Claude Code that Persona starts without Claude's flags: a compatible CLI (on the PATH as traex), and a
// launcher the hub says runs Codex or a compatible CLI (hub.json machines[].launchers, a wrapper the login shell's list does not
// show as one). A Codex launcher the login shell lists starts through codex_start.go instead.

// agentLauncher is a launcher as launchers.list answers: with the agent it starts, for hubs that read agent_type.
type agentLauncher struct {
	proto.Launcher
	AgentType string `json:"agent_type,omitempty"`
}

// ompOptionsRefusal: omp's model and thinking level come from its own config.yml, not from a start's arguments.
const ompOptionsRefusal = "omp 的模型和思考强度由 omp 自己的配置决定，这次没有启动。"

// launcherAgentType is the agent a launcher starts when nothing else says: codex, traex and omp by name, else claude.
func launcherAgentType(name string) string {
	if name == "codex" || name == traexAgent || name == ompAgent {
		return name
	}
	return "claude"
}

func typedLaunchers(list []proto.Launcher) []agentLauncher {
	out := make([]agentLauncher, 0, len(list))
	seen := map[string]bool{}
	for _, l := range list {
		if !seen[l.Name] {
			t := l.Agent
			if t == "" {
				t = launcherAgentType(l.Name)
			}
			out = append(out, agentLauncher{Launcher: l, AgentType: t})
			seen[l.Name] = true
		}
	}
	return out
}

// otherAgentCommand is the launcher's command line, with the first message as its argument (typedArg), for a pane whose
// shell is shells.
func otherAgentCommand(launcher, resume, prompt string, shells ...string) string {
	cmd := launcher
	if resume != "" {
		cmd += " resume " + shellQuote(resume)
	}
	if prompt != "" {
		cmd += " " + shellQuote(typedArg(prompt, shells...))
	}
	return cmd
}

// typedArg is text to quote on a command line that may be typed into the shell (typedstart.go), whose line editor acts
// on keys inside quotes too: its controls inert (^U or ^C would edit the line), a tab as a space (it completes the
// quoted word, closes the quote and leaves the rest to the shell), and outside the sh family (takesAssign) a line
// break as a space (csh ends the command at one, quote open or not).
func typedArg(text string, shells ...string) string {
	text = strings.ReplaceAll(inertControls(text), "\t", " ")
	if !takesAssign(shells...) {
		text = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(text)
	}
	return text
}

// startOtherAgent opens a new tab in ws (a new space when ""), runs the launcher there (resuming a session when
// resume is set, with the first message as its argument) and waits until the host reports an agent of that kind in
// the tab with a session id that was not running before (or the one resumed).
func (n *Node) startOtherAgent(ctx context.Context, launch host.Launcher, cwd, launcher, kind, prompt, resume, ws, tabName string) (*proto.Session, error) {
	created, err := n.newTab(ctx, launch, cwd, ws, tabName, resume) // retire.go
	if err != nil {
		return nil, err
	}
	pane := created.Pane.String()
	// Exclude existing sessions when the normal scan has to infer a Codex rollout.
	n.mu.Lock()
	existing := make(map[string]bool, len(n.agents))
	for id := range n.agents {
		existing[id] = true
	}
	n.mu.Unlock()
	if err := n.settleShell(ctx, pane); err != nil { // the fresh shell at rest first (shellsettle.go, #2570)
		return nil, n.startTabFailed(ctx, launch, created, ws, resume, err)
	}
	// A Codex wrapper is told to herdr as Codex (hostherdr.go); a compatible CLI is herdr's to detect. The node's
	// directory in front, the assistant's environment left out (agentenv.go).
	shells := paneShells(ctx, created)
	// The first message keeps its line breaks only in a start file: typed out, the line may meet csh, which runs each
	// line on its own (typedArg), and Orca does not say which shell it opened.
	argShells := shells
	if !n.typesStartFile(launch, shells...) {
		argShells = nil
	}
	cmd := withHerdrAgent(typedHerdrAgent(launch, kind, shells...), otherAgentCommand(launcher, resume, prompt, argShells...))
	// Not in the agent sandbox (agentsandbox.go).
	n.notePaneStart(launcher)
	done, err := n.runAgentCommand(ctx, launch, created, ws, "", n.agentCommand(cmd, shellQuote, shells...), shells...) // typedstart.go
	if err != nil {
		return nil, err
	}
	defer done()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var grace time.Time // when a family start gives up on the extension reporting the session (pifamily.go)
	for {
		select {
		case <-ctx.Done():
			screen, _ := n.readPane(context.Background(), pane, "recent-unwrapped", 30)
			return nil, fmt.Errorf("wait for %s session: %w\n%s", kind, ctx.Err(), lastLines(screen, 12))
		case <-ticker.C:
		}
		if err := n.refreshHosts(ctx); err != nil {
			continue
		}
		n.mu.Lock()
		var id string
		var agent host.Agent
		for candidate, a := range n.agents {
			if a.PaneID == pane && a.Agent == kind && candidate != "" &&
				((resume == "" && !existing[candidate]) || candidate == resume) {
				id, agent = candidate, a
				break
			}
		}
		n.mu.Unlock()
		if id == "" {
			continue
		}
		if piFamily(kind) && id == otherSession(agent) {
			// The host knows the pane but not its session yet: the extension may report it in a moment.
			if grace.IsZero() {
				grace = time.Now().Add(piFamilyIDGrace)
			}
			if time.Now().Before(grace) {
				continue
			}
		}
		n.state.put(id, sessionState{Pane: pane, Launcher: launcher, Cwd: cwd, StartedAt: time.Now().UTC()})
		n.bindPane(pane, id)
		session := n.sessionFor(id, agent)
		session.Agent = kind
		n.holdResume(ctx, launch, pane, id, agentLaunch{Agent: kind, Launcher: launcher, ID: id})
		return session, nil
	}
}
