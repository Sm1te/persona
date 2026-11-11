package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// The standalone terminal (docs/integrations/terminal.md "Standalone terminal"): any pane of the terminal host can be opened as a terminal,
// agent or plain shell. The terminal stream (terminal.go) addresses a session by id; a pane with no agent session has
// none, so it is addressed as "pane:<pane id>". Only the terminal calls (watch, unwatch, screen reads) understand
// that address — session.send, interrupt and the rest still need a session, so a pane address can never be used to
// type into a shell through the chat paths.

// panePrefix starts the stream id of a pane addressed directly.
const panePrefix = "pane:"

// paneListMaxAge: a layout the node took this recently answers panes.list without another snapshot. The node takes
// one on every herdr change anyway; this only covers a first call and a host that stopped telling.
const paneListMaxAge = 3 * time.Second

// terminalPane is the pane a terminal call is about: the pane named by a "pane:" address when the host has it, else
// the pane of the agent session id.
func (n *Node) terminalPane(ctx context.Context, id string) (string, error) {
	if pane, ok := strings.CutPrefix(id, panePrefix); ok {
		return n.shellPane(ctx, pane)
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		if p, ok := n.keptPane(id); ok { // a stopped agent's terminal: the shell it left (stopped.go, #4754)
			return p.PaneID, nil
		}
	}
	return pane, err
}

// shellPane checks that pane is one the host lists (a snapshot is taken when the one kept does not have it: a tab
// opened a moment ago), so a stream cannot be pointed at anything else.
func (n *Node) shellPane(ctx context.Context, pane string) (string, error) {
	if pane == "" {
		return "", errors.New("no pane")
	}
	if n.hasPane(pane) {
		return pane, nil
	}
	if err := n.refreshHosts(ctx); err == nil && n.hasPane(pane) {
		return pane, nil
	}
	return "", fmt.Errorf("pane %s is not running", pane)
}

func (n *Node) hasPane(pane string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, l := range n.layouts {
		for _, p := range l.panes {
			if p.PaneID == pane {
				return true
			}
		}
	}
	return false
}

// listPanes is the terminal browser's tree: from the layouts the node keeps, refreshed first when they are older
// than paneListMaxAge (or a host has none yet).
func (n *Node) listPanes(ctx context.Context) (proto.PanesResponse, error) {
	hs := n.hosts.All()
	if len(hs) == 0 {
		return proto.PanesResponse{}, errNoHost
	}
	n.mu.Lock()
	stale := false
	for _, h := range hs {
		if l, ok := n.layouts[h.Kind()]; !ok || time.Since(l.at) > paneListMaxAge {
			stale = true
		}
	}
	n.mu.Unlock()
	if stale {
		if err := n.refreshHosts(ctx); err != nil && !n.hasLayout() {
			return proto.PanesResponse{}, err
		}
	}
	out := proto.PanesResponse{Hosts: []proto.PaneHost{}}
	caps := map[string]proto.PaneHost{} // what each host can do; asked outside the lock (a first ask may run a process)
	for _, h := range hs {
		c := proto.PaneHost{}
		if st, ok := h.(host.Streamer); ok {
			c.Stream = st.CanObserve()
		}
		if ct, ok := h.(host.Controller); ok {
			c.Control = ct.CanControl()
		}
		_, c.NewTab = h.(host.Launcher)
		c.Worktree = canWorktree(h)
		caps[h.Kind()] = c
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	sessionOf := map[string]string{} // pane -> its agent session
	for id, a := range n.agents {
		if a.PaneID == "" {
			continue
		}
		if old, ok := sessionOf[a.PaneID]; !ok || id < old {
			sessionOf[a.PaneID] = id // the same one every time when two claim a pane
		}
	}
	for _, h := range hs {
		l, ok := n.layouts[h.Kind()]
		if !ok {
			continue
		}
		ph := caps[h.Kind()]
		ph.Kind, ph.Workspaces, ph.Tabs, ph.Panes = h.Kind(), []proto.PaneSpace{}, []proto.PaneTab{}, []proto.PaneItem{}
		for _, s := range l.spaces {
			ph.Workspaces = append(ph.Workspaces, proto.PaneSpace{ID: s.ID, Label: s.Label, Number: s.Number, Cwd: s.Cwd, Branch: s.Branch})
		}
		sort.Slice(ph.Workspaces, func(i, j int) bool {
			a, b := ph.Workspaces[i], ph.Workspaces[j]
			return a.Number < b.Number || a.Number == b.Number && a.ID < b.ID
		})
		for _, t := range l.slots {
			ph.Tabs = append(ph.Tabs, proto.PaneTab{ID: t.ID, WorkspaceID: t.SpaceID, Label: t.Label, Number: t.Number})
		}
		sort.Slice(ph.Tabs, func(i, j int) bool {
			a, b := ph.Tabs[i], ph.Tabs[j]
			return a.Number < b.Number || a.Number == b.Number && a.ID < b.ID
		})
		for _, p := range l.panes {
			if p.PaneID == "" {
				continue
			}
			item := proto.PaneItem{Pane: p.PaneID, Terminal: panePrefix + p.PaneID, TabID: p.TabID,
				WorkspaceID: p.WorkspaceID, Agent: p.Agent, Status: p.AgentStatus, Title: p.Title, Cwd: p.Cwd,
				SessionID: sessionOf[p.PaneID]}
			if id := p.Tokens[personaTabToken]; validID(id) && p.Agent == "" && item.SessionID == "" {
				item.AgentSession = id // the terminal a stopped agent left (stopped.go)
			}
			ph.Panes = append(ph.Panes, item)
		}
		out.Hosts = append(out.Hosts, ph)
	}
	return out, nil
}

func (n *Node) hasLayout() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.layouts) > 0
}

// newShell opens a plain shell: a new tab in workspace ws of the host kind (herdr tab create --no-focus, so the
// computer's own view does not jump). It starts in the workspace's directory as the host reported it — the phone does
// not choose one — and is named shell, shell-2…. Only a user's explicit action calls this.
func (n *Node) newShell(ctx context.Context, kind, ws string) (proto.NewShellResult, error) {
	if ws == "" {
		return proto.NewShellResult{}, errors.New("no workspace")
	}
	var launch host.Launcher
	for _, h := range n.hosts.All() {
		if l, ok := h.(host.Launcher); ok && (kind == "" || h.Kind() == kind) {
			if _, has := n.spaceOf(h.Kind(), ws); has {
				launch = l
				break
			}
		}
	}
	if launch == nil {
		return proto.NewShellResult{}, fmt.Errorf("workspace %s is not on a host that can open tabs", ws)
	}
	space, _ := n.spaceOf(launch.(host.Host).Kind(), ws)
	cwd := space.Cwd
	if cwd == "" { // a workspace the host reports no directory for: the home directory, like a new terminal window
		cwd, _ = os.UserHomeDir()
	}
	created, err := launch.CreateSlot(ctx, ws, cwd, tabLabel("shell", n.tabLabels(ctx, launch, ws)))
	if err != nil {
		return proto.NewShellResult{}, fmt.Errorf("创建终端标签页失败：%w", err)
	}
	if created == nil || created.Pane.ID == "" {
		return proto.NewShellResult{}, errors.New("创建终端标签页失败：宿主没有返回窗格")
	}
	pane := created.Pane.String()
	_ = n.refreshHosts(ctx, launch.(host.Host)) // the new pane is in the layout the terminal call checks
	return proto.NewShellResult{Pane: pane, Terminal: panePrefix + pane, TabID: created.SlotID}, nil
}

// spaceOf is a workspace of a host's last layout.
func (n *Node) spaceOf(kind, ws string) (host.Space, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	s, ok := n.layouts[kind].spaces[ws]
	return s, ok
}

// paneForegroundWait bounds how long panes.list waits for the programs in front of its plain panes.
var paneForegroundWait = 1500 * time.Millisecond

// paneForegrounds names the program in front of each pane with no agent (herdr: pane.process_info) for the terminal
// list (#4754): "zsh" at a prompt, "vim", "npm"… Nothing on the screen is read; a pane the host cannot answer for in
// time keeps none.
func (n *Node) paneForegrounds(ctx context.Context, out *proto.PanesResponse) {
	ctx, cancel := context.WithTimeout(ctx, paneForegroundWait)
	defer cancel()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for hi := range out.Hosts {
		fg, ok := n.hosts.Get(out.Hosts[hi].Kind).(host.ForegroundNamer)
		if !ok {
			continue
		}
		for pi := range out.Hosts[hi].Panes {
			p := &out.Hosts[hi].Panes[pi]
			if p.Agent != "" {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				if names, err := fg.PaneForeground(ctx, n.hosts.Ref(p.Pane)); err == nil {
					p.Foreground = frontProgram(names)
				}
			}()
		}
	}
	wg.Wait()
}

// frontProgram is the program a pane shows in front: the first that is not a shell, else its shell, without a login
// shell's leading "-" or a directory.
func frontProgram(names []string) string {
	pick := ""
	for _, name := range names {
		if !shellProgram(name) {
			pick = name
			break
		}
		if pick == "" {
			pick = name
		}
	}
	return filepath.Base(strings.TrimPrefix(pick, "-"))
}
