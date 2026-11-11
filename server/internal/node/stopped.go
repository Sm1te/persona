package node

import (
	"context"
	"log"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// Stopped agents are read-only (#4754, docs/integrations/engines.md "Agents run in a terminal"). An agent of a
// terminal engine (herdr, Herdr App, Orca, Tern) that is not running — stopped by Persona (retire.go), closed by the
// user, exited by itself or cut off by a restart — keeps its conversation and its terminal and takes no messages: a
// send to it is refused as agent_stopped with nothing typed, where it used to be resumed (claude --resume, codex
// resume) in a new tab. The one way back is the user's Resume on its terminal page: session.start with
// resume_session_id, which types the resume into the shell the agent left behind when its tab is still there
// (inKeptTerminal), else opens a new tab as before. Left as they were: assistant conversations, sessions of Claude's
// desktop app, of runtimes and of engines that run agents themselves (Paseo, Cindy), and a branch nothing has started
// yet (fork.go), whose first message starts it.

const agentStopped = "agent_stopped"

// stoppedReason is a stopped agent's read_only_reason (the apps show their own words for it).
const stoppedReason = "Agent 已停止：对话和终端可以看，它不再收消息；要它接着做，在终端页点「恢复」"

// desktopSession: Claude's desktop app names transcript id (a session of its sidebar, archived or not, or one it went
// on from): the app's session, which a message continues as before (claudedesktop_open.go), not a stopped agent.
func (n *Node) desktopSession(id string) bool {
	x := readClaudeDesktopIndex(n.home)
	_, named := x.visible[id]
	return named || len(x.earlier[id]) > 0
}

// stoppedRefusal refuses a send to session id's stopped agent: nothing was typed.
func (n *Node) stoppedRefusal(id string) error {
	log.Printf("send %s: the agent has stopped: refused, nothing typed", id)
	return refusal(agentStopped, "这个 Agent 已经停了：对话和终端还在，它不再收消息，这次没有发送。要它接着做，在它的终端页点「恢复」。",
		map[string]any{"delivered": "none"})
}

// freshFork says session id is a branch nothing has started yet (fork.go): its first message starts it.
func (n *Node) freshFork(id string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.freshForks[id]
}

// forkStarted: session id's branch has run, and is an agent like any other from now on.
func (n *Node) forkStarted(id string) {
	n.mu.Lock()
	delete(n.freshForks, id)
	n.mu.Unlock()
}

// markStopped makes a stopped agent's row read-only: it can be read and its terminal opened (the shell it left, or
// Resume when there is none), nothing else.
func (n *Node) markStopped(s *proto.Session) {
	if s.SourceKind != "" || s.Assistant || s.Capabilities != nil || s.PaneID != "" || s.AgentSurface == "desktop" {
		return
	}
	if s.Status != proto.StatusOffline && s.Status != proto.StatusInterrupted {
		return
	}
	switch s.Agent {
	case "", "claude", "codex", traexAgent:
	default:
		return
	}
	if h, _, known := n.runtimes.belongs(s.SessionID); h != nil || known || n.runtimes.unlistedTranscripts()[s.SessionID] {
		return
	}
	kind := s.Host
	if kind == "" {
		st, _ := n.state.get(s.SessionID)
		pane := st.Pane
		if pane == "" {
			n.mu.Lock()
			pane = n.lastPanes[s.SessionID]
			n.mu.Unlock()
		}
		if pane != "" {
			kind = host.ParsePane(pane).Kind
		} else {
			kind = n.defaultKind() // Older transcripts may have no recorded host.
		}
	}
	if _, runtime := engineRuntimes[kind]; runtime || n.hosts.Get(kind) == nil {
		return
	}
	if n.isAssistantSession(s.SessionID) || n.runtimeAssistant(s.SessionID) || n.freshFork(s.SessionID) {
		return
	}
	s.Capabilities = &proto.SessionCapabilities{Read: true, Terminal: true}
	s.ReadOnlyReason = stoppedReason
}

// keptPane is the pane session id's agent left behind: one Persona opened for it (personaTabToken), with no agent in
// it now. ok false when there is none (the user closed it, or a herdr restart dropped its mark).
func (n *Node) keptPane(id string) (p host.Agent, ok bool) {
	if !validID(id) {
		return p, false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	inUse := map[string]bool{}
	for _, a := range n.agents {
		inUse[a.PaneID] = true
	}
	for _, l := range n.layouts {
		for _, q := range l.panes {
			if q.PaneID != "" && q.Tokens[personaTabToken] == id && q.Agent == "" && !inUse[q.PaneID] {
				return q, true
			}
		}
	}
	return p, false
}

// inKeptTerminal has the resume of session id (the user's Resume) run in the shell its agent left behind, when that
// shell is at rest in the session's directory cwd (claude --resume finds a session by its folder); otherwise ctx as
// it was, and the resume opens a new tab. The pane is the user's from then on, as one picked for "Run Agent Here":
// a resume that fails leaves it open.
func (n *Node) inKeptTerminal(ctx context.Context, id, cwd string) context.Context {
	if adoptedPane(ctx) != nil {
		return ctx
	}
	p, ok := n.keptPane(id) // in the hosts' last snapshots, which every change in the engine refreshes
	if !ok {
		return ctx
	}
	h, ref, err := n.paneHost(p.PaneID)
	if err != nil {
		return ctx
	}
	launch, err := n.launcherFor(n.startKind(ctx))
	if err != nil {
		return ctx
	}
	if lh, ok := launch.(host.Host); !ok || lh.Kind() != h.Kind() {
		return ctx
	}
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return ctx
	}
	names, err := f.PaneForeground(ctx, ref)
	if err != nil || len(names) == 0 || !allShells(names) {
		return ctx // something else is in front: someone is using it
	}
	if dir := paneDir(p); cwd != "" && (dir == "" || filepath.Clean(dir) != filepath.Clean(cwd)) {
		return ctx
	}
	log.Printf("resume %s: in the terminal it left (%s)", id, p.PaneID)
	return withAdoptedPane(ctx, &host.Created{SlotID: p.TabID, SpaceID: p.WorkspaceID, Pane: ref}, names)
}

// allShells: every program in front of a pane is a shell.
func allShells(names []string) bool {
	for _, name := range names {
		if !shellProgram(name) {
			return false
		}
	}
	return true
}

// finishedTerminal is a terminal a stopped agent left, as panes.close_finished lists it.
type finishedTerminal struct {
	Pane        string `json:"pane"`
	SessionID   string `json:"session_id"`
	TabID       string `json:"tab_id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Why         string `json:"why,omitempty"` // a kept one: why it was not closed
}

// closeFinished is the user's "Close finished terminals" (#4754): it closes the terminals stopped agents left — panes
// Persona opened for a session (personaTabToken) with no agent in them and only shells in front, looked at again now —
// only those in only (comma-separated pane ids, what the user confirmed) when given. preview lists them and closes
// nothing. Nothing calls it on a timer.
func (n *Node) closeFinished(ctx context.Context, only string, preview bool) (map[string]any, error) {
	want := map[string]bool{}
	for _, p := range strings.Split(only, ",") {
		if p = strings.TrimSpace(p); p != "" {
			want[p] = true
		}
	}
	type cand struct {
		p host.Agent
		h host.Host
	}
	var cands []cand
	n.mu.Lock()
	inUse := map[string]bool{}
	for _, a := range n.agents {
		inUse[a.PaneID] = true
	}
	for _, h := range n.hosts.All() {
		for _, p := range n.layouts[h.Kind()].panes {
			if p.PaneID == "" || !validID(p.Tokens[personaTabToken]) || p.Agent != "" || inUse[p.PaneID] || len(want) > 0 && !want[p.PaneID] {
				continue
			}
			cands = append(cands, cand{p, h})
		}
	}
	n.mu.Unlock()
	closed, kept := []finishedTerminal{}, []finishedTerminal{}
	for _, c := range cands {
		t := finishedTerminal{Pane: c.p.PaneID, SessionID: c.p.Tokens[personaTabToken], TabID: c.p.TabID, WorkspaceID: c.p.WorkspaceID}
		fg, ok := c.h.(host.ForegroundNamer)
		org, ok2 := c.h.(host.Organizer)
		if !ok || !ok2 {
			t.Why = "this engine cannot close it from here"
			kept = append(kept, t)
			continue
		}
		ref := n.hosts.Ref(c.p.PaneID)
		names, err := fg.PaneForeground(ctx, ref)
		if err != nil || len(names) == 0 || !allShells(names) {
			t.Why = "something runs in it" // the user is using it
			kept = append(kept, t)
			continue
		}
		if !preview {
			if err := org.Close(ctx, ref, c.p.WorkspaceID, c.p.TabID); err != nil {
				t.Why = err.Error()
				kept = append(kept, t)
				continue
			}
			log.Printf("finished terminal %s (session %s): closed by the user", c.p.PaneID, t.SessionID)
		}
		closed = append(closed, t)
	}
	if !preview && len(closed) > 0 {
		_ = n.refreshHosts(ctx)
		n.notifySessions()
	}
	return map[string]any{"closed": closed, "kept": kept, "preview": preview}, nil
}
