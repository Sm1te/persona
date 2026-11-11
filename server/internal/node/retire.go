package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Stopping an agent and keeping its terminal (#4754, docs/integrations/engines.md "Agents run in a terminal"). An agent
// runs on top of a terminal: Persona quits the agent process of a finished task, an idle agent (hub tasks_close.go,
// tasks_idle.go) or a cancelled task's (supervise_leader.go), and its pane stays with the shell it was typed into in
// front, as when the agent exits by itself. Only the user closes a terminal (Close, Archive and close, a pane's
// close). The session id, its transcript and, in the node's state, the launcher, directory and model it ran with stay.
//
// session.stop quits any agent running in a pane of the machine's engine; with retire it first checks what
// session.retire always did: stopped (not working, not waiting), not starting, not an assistant conversation, not a
// session another tool owns. session.retire is what a hub before #4754 asks for a finished or idle agent: it now stops
// the agent the same way and keeps the pane too. Only "Archive and close", which the user pressed, still closes the
// terminal: session.retire with close_terminal (a node before #4754 closes it without being asked). A session without
// a terminal (another tool's, an engine that runs agents itself) is refused: no_terminal to session.stop, which the
// hub then closes as before, not_retirable to session.retire as it always was.

const (
	notRetirable = "not_retirable"
	noTerminal   = "no_terminal"
	notStopped   = "not_stopped"
)

// stopRounds and stopWait bound session.stop: each round presses Ctrl+C twice and waits up to stopWait for the agent
// to leave its pane. A working Claude takes a round to interrupt its turn and another to quit (tests shorten them).
var (
	stopRounds = 3
	stopWait   = 2 * time.Second
)

// retire stops session id's agent when it is still and keeps its pane, or closes the pane when closeTerminal (the
// user's "Archive and close"); done false (and no error) when it was not running.
func (n *Node) retire(ctx context.Context, id string, closeTerminal bool) (done bool, err error) {
	if !closeTerminal {
		return n.stop(ctx, id, true)
	}
	unlock, _, ok, err := n.stoppable(id, true)
	if err != nil || !ok {
		return false, err
	}
	defer unlock()
	if err := n.close(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

// stop quits session id's agent and keeps its pane (retire: only one that is still); stopped false (and no error)
// when it was not running.
func (n *Node) stop(ctx context.Context, id string, retire bool) (stopped bool, err error) {
	unlock, a, ok, err := n.stoppable(id, retire)
	if err != nil || !ok {
		return false, err
	}
	defer unlock()
	return n.quitAgent(ctx, id, a.PaneID)
}

// stoppable checks that session id's agent may be stopped now (retire: only a still one) and holds its typing lock
// (unlock releases it) when ok; ok false with no error when it is not running.
func (n *Node) stoppable(id string, retire bool) (unlock func(), a host.Agent, ok bool, err error) {
	if !validID(id) {
		return nil, a, false, fmt.Errorf("invalid session id %q", id)
	}
	no := func(code, why string) (func(), host.Agent, bool, error) {
		if retire {
			code = notRetirable // what hubs before #4754 know
		}
		return nil, a, false, refusal(code, why, nil)
	}
	_, terminalOnly := n.terminalOnly(id)
	switch h, _, known := n.runtimes.belongs(id); {
	case n.isAssistantSession(id) || n.runtimeAssistant(id):
		return no(notRetirable, "assistant conversations are not stopped")
	case n.externalCodex(id) || h != nil || known || terminalOnly:
		return no(noTerminal, "this session belongs to another tool")
	}
	unlock = n.typing.lock(id) // not while a message is on its way in (a send may be resuming it right now)
	fail := func(code, why string) (func(), host.Agent, bool, error) {
		unlock()
		return no(code, why)
	}
	if r := n.starts.get(id); r != nil && r.ended.IsZero() {
		return fail(notRetirable, "still starting")
	}
	if a, ok = n.agent(id); !ok {
		unlock()
		return nil, a, false, nil
	}
	kind := host.ParsePane(a.PaneID).Kind
	if _, runtime := engineRuntimes[kind]; runtime {
		return fail(noTerminal, "this engine runs its agents without a terminal")
	}
	if n.hosts.Get(kind) == nil {
		return fail(noTerminal, "this session host is not available")
	}
	if retire {
		if a.AgentStatus == proto.StatusWorking || a.AgentStatus == proto.StatusBlocked {
			return fail(notRetirable, "the agent is "+a.AgentStatus)
		}
		switch a.Agent {
		case "", "claude", "codex", traexAgent:
		default:
			return fail(notRetirable, "cannot stop a "+a.Agent)
		}
	}
	return unlock, a, true, nil
}

// quitAgent presses Ctrl+C twice in pane until session id's agent is no longer in it (or in any pane), and never
// closes the pane: the shell the agent ran in stays. An agent that does not quit is refused as not_stopped.
func (n *Node) quitAgent(ctx context.Context, id, pane string) (bool, error) {
	for range stopRounds {
		_ = n.sendKeys(ctx, pane, "ctrl+c")
		time.Sleep(200 * time.Millisecond)
		if err := n.sendKeys(ctx, pane, "ctrl+c"); err != nil {
			return false, err
		}
		for deadline := time.Now().Add(stopWait); ; {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if err := n.refreshHosts(ctx); err == nil {
				if a, ok := n.agent(id); !ok || a.PaneID != pane {
					return true, nil
				}
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	return false, refusal(notStopped, "the agent did not quit", nil)
}

// resumed is what a Claude session that is not running is resumed with: the directory, launcher and model it was
// started with, the modes it ran in and its title as the tab's name.
type resumed struct {
	cwd, launcher, mode, effort, tab string
	execution                        modelExecution
}

// resumeWith is how a Claude session that is not running comes back, by a message (send) or by a start that resumes
// it and leaves these out (session.start, fillResume).
func (n *Node) resumeWith(id string, opts sendOpts) resumed {
	st, _ := n.state.get(id)
	r := resumed{cwd: st.Cwd, tab: n.resumeTabName(id)}
	r.launcher, r.execution = n.resumeLauncher(st, id, opts)
	if r.cwd == "" {
		if p := n.transcriptPath(id); p != "" {
			if fi, err := os.Stat(p); err == nil {
				r.cwd = n.meta(p, fi).cwd
			}
		}
	}
	r.mode, r.effort = n.modesOf(id)
	return r
}

// fillResume: a start that resumes a Claude session that is not running (the app's Resume Session, #4328) keeps what
// the session ran with for whatever the start leaves out (asked), as a message that resumes it does; before, it ran
// on the launcher's default model and wrote that over the one the session remembered. The model comes along only
// with the launcher it was picked for. A Codex or compatible thread resumes its own way (codex_resume.go), and a
// running session is not started again: both keep what was asked.
func (n *Node) fillResume(id string, asked resumed) resumed {
	if !validID(id) {
		return asked
	}
	if _, live := n.agent(id); live || n.codexPath(id) != "" || n.traexPath(id) != "" {
		return asked
	}
	r := n.resumeWith(id, sendOpts{})
	out := asked
	out.launcher = cmp.Or(asked.launcher, r.launcher)
	if out.launcher == r.launcher && asked.execution.Model == "" && asked.execution.Fingerprint == "" {
		out.execution.Model, out.execution.Fingerprint = r.execution.Model, r.execution.Fingerprint
	}
	out.cwd = cmp.Or(asked.cwd, r.cwd)
	out.mode = cmp.Or(asked.mode, r.mode)
	out.effort = cmp.Or(asked.effort, r.effort)
	out.tab = cmp.Or(asked.tab, r.tab)
	return out
}

// resumeTabName names the tab a session is resumed in: its title (Claude's AI title, or the one it was given), so
// the tab reads as it did before it was closed rather than agent-<n>.
func (n *Node) resumeTabName(id string) string {
	p := n.transcriptPath(id)
	if p == "" {
		return ""
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	return n.meta(p, fi).title
}

// resumeOtherWith resumes a Codex or a compatible CLI that is not running, with text as its first message, the user's
// Resume (#4754): with the launcher, directory and model to resume with (st): the session's own, or
// what a Codex thread started outside Persona is resumed with (codex_resume.go).
func (n *Node) resumeOtherWith(ctx context.Context, id string, st sessionState, text string, images []proto.Image) error {
	ctx = n.withResumeHost(ctx, id)
	if r := n.notRunningRefusal(ctx, id); r != nil {
		return r
	}
	launch, err := n.launcherFor(n.startKind(ctx))
	if err != nil {
		return err
	}
	if fi, err := os.Stat(st.Cwd); err != nil || !fi.IsDir() {
		return fmt.Errorf("directory %s does not exist", st.Cwd)
	}
	codex := n.codexPath(id) != ""
	var argv []string
	if codex {
		var ok bool
		argv, ok = n.codexLauncher(ctx, st.Launcher)
		if !ok {
			return fmt.Errorf("启动器「%s」不再运行 Codex，没法接着这个会话", st.Launcher)
		}
		if argv, err = n.codexReadonlyArgs(st.Launcher, argv, st.AppMode); err != nil {
			return err // Refuse conflicting permissions before creating a workspace, tab or attachment.
		}
		if st.Model != "" {
			argv = append(withoutModelArgs(argv), "--model", st.Model)
		}
	}
	paths, err := n.saveImages(images)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		text = strings.TrimSpace(text) + "\n\n<attached_images>\n" + strings.Join(paths, "\n") + "\n</attached_images>"
	}
	ws, err := n.workspaceFor(ctx, launch, st.Cwd)
	if err != nil {
		return err
	}
	tab := n.resumeTabName(id)
	if !codex { // a compatible CLI: `<launcher> resume <id> <message>`
		_, err = n.startOtherAgent(ctx, launch, st.Cwd, st.Launcher, traexAgent, text, id, ws, tab)
		return uncertain(err)
	}
	return uncertain(n.resumeCodex(ctx, launch, id, st, argv, text, ws, tab))
}

// resumeCodex runs `<launcher words> resume <id> -- <message>` (through codex-start, like a new Codex) in a new tab
// and waits until the engine shows that Codex in it.
func (n *Node) resumeCodex(ctx context.Context, launch host.Launcher, id string, st sessionState, argv []string, prompt, ws, tabName string) error {
	if codexOnRemote(argv) {
		argv = append(argv, "-C", st.Cwd)
	}
	argv = append(argv, "resume", id)
	dir, err := os.MkdirTemp(n.dir, "codex-start-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "start.json")
	b, _ := json.Marshal(codexStartFile{Argv: argv, Prompt: prompt, Agent: herdrAgentFor(launch, "codex")})
	if err := os.WriteFile(file, b, 0o600); err != nil {
		return err
	}
	created, err := n.newTab(ctx, launch, st.Cwd, ws, tabName, id)
	if err != nil {
		return err
	}
	pane := created.Pane.String()
	time.Sleep(300 * time.Millisecond) // the fresh shell loads ~/.zshrc; typed input is buffered either way
	shells := paneShells(ctx, created)
	done, err := n.runAgentCommand(ctx, launch, created, ws, "", n.agentCommand(shellQuote(n.bin)+" codex-start "+shellQuote(file), shellQuote, shells...), shells...) // typedstart.go
	if err != nil {
		return err // the tab is closed: no pane to bind
	}
	defer done()
	n.bindPane(pane, id) // the pane runs this thread (kept in its state): no guessing another rollout for it
	wait, cancel := context.WithTimeout(ctx, codexStartWait)
	defer cancel()
	for {
		if err := n.refreshHosts(wait); err == nil {
			if a, ok := n.agent(id); ok && a.PaneID == pane {
				return nil
			}
		}
		select {
		case <-wait.Done():
			screen, _ := n.readPane(context.WithoutCancel(ctx), pane, "recent-unwrapped", 30)
			return fmt.Errorf("Codex 没有在新标签页里接上会话 %s\n%s", id, lastLines(screen, 12))
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// newTab opens a tab in workspace ws for cwd, named tabName (or agent-<n>); with no workspace, a new one named after
// the directory, whose first tab it is. The pane is marked as Persona's (personaTabToken, the session id when it is
// already known): once its agent exits, the tab stays with the shell, linked to the session (personatab.go).
func (n *Node) newTab(ctx context.Context, launch host.Launcher, cwd, ws, tabName, id string) (*host.Created, error) {
	if c := adoptedPane(ctx); c != nil { // "Run Agent Here": the pane the user picked, not a new tab (panes_actions.go)
		return c, nil
	}
	if c, ok := ctx.Value(reusedTabKey{}).(*host.Created); ok && c != nil { // a failed start's kept tab (startremedy.go)
		return c, nil
	}
	var created *host.Created
	var err error
	if ws == "" {
		if created, err = launch.CreateSpace(ctx, cwd, filepath.Base(cwd)); err == nil && created.SlotID != "" {
			var taken map[string]bool
			if _, ok := launch.(*herdrCoreHost); ok {
				taken = n.tabLabels(ctx, launch, created.SpaceID) // CreateSpace can reuse an app space by its folder.
			}
			_ = renameSlot(ctx, launch, created.SlotID, tabLabel(tabName, taken))
		}
	} else {
		created, err = launch.CreateSlot(ctx, ws, cwd, tabLabel(tabName, n.tabLabels(ctx, launch, ws)))
	}
	if err != nil {
		return nil, fmt.Errorf("创建终端标签页失败：%w", err)
	}
	markPersonaTab(ctx, launch, created.Pane, id)
	return created, nil
}
