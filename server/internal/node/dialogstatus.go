package node

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

// A Claude Code dialog that is not a tool's question can hold its terminal while herdr calls the agent done or idle:
// 2026-11-05, Claude Code 2.1.309's auto mode billing notice ("We're changing auto mode… Enter to continue · Esc to
// cancel") stopped three agents for minutes, listed as "Done, unseen", and messages to them were refused as dialog_open
// (#2553). So the node looks at a resting Claude's screen now and then (paneDialogEvery, in the background: the list
// never waits for a read) and lists one with a dialog at the bottom as blocked, the dialog's first line its reason.

// paneDialogEvery is how often a resting Claude's screen is read for a dialog. Tests shorten it.
var paneDialogEvery = 5 * time.Second

type paneDialog struct {
	at      time.Time // when the last read was started
	head    string    // the dialog's first line on the last read, "" when none
	reading bool
	session string // the session in the pane, for terminalrewind.go
	// rewindLooked: what the last look for a rewind in the terminal was made on (terminalrewind.go): the same is not
	// looked at again.
	rewindLooked string
}

// paneDialogs remembers, per pane, the dialog its last screen read showed.
type paneDialogs struct {
	mu sync.Mutex
	m  map[string]*paneDialog
}

// dialogOn is the first line of the dialog the pane showed when last read, "" when none (or not read yet). The first
// read comes once the pane has rested paneDialogEvery (a turn that just ended is not read at once), then each read
// older than that starts a new one in the background; a change it finds pushes the list again.
func (n *Node) dialogOn(id, pane string) string {
	d := &n.dialogs
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]*paneDialog{}
	}
	p := d.m[pane]
	if p == nil {
		p = &paneDialog{at: time.Now()}
		d.m[pane] = p
	}
	p.session = id
	if !p.reading && time.Since(p.at) >= paneDialogEvery {
		p.reading, p.at = true, time.Now()
		go n.readDialog(pane, p)
	}
	return p.head
}

func (n *Node) readDialog(pane string, p *paneDialog) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	text, err := n.dialogScreen(ctx, pane)
	cancel()
	head := ""
	if err == nil {
		head = screen.DialogHead(text)
	}
	if err == nil && head == "" {
		n.dialogs.mu.Lock()
		id := p.session
		n.dialogs.mu.Unlock()
		n.seeTerminalRewind(id, pane, text, p) // a rewind done in the terminal leaves its prompt in the box
	}
	n.dialogs.mu.Lock()
	changed := err == nil && head != p.head
	if err == nil {
		p.head = head
	}
	p.reading = false
	n.dialogs.mu.Unlock()
	if changed && n.events != nil {
		n.events.poke(true) // a list rebuild carries it to the hub
	}
}

// forgetDialog drops what a pane's last read showed: keys went into it (the dialog may be gone).
func (n *Node) forgetDialog(pane string) {
	n.dialogs.mu.Lock()
	delete(n.dialogs.m, pane)
	n.dialogs.mu.Unlock()
	n.forgetAllowDialog(pane)
}

// forgetDialogs drops the panes no longer live.
func (n *Node) forgetDialogs(live map[string]bool) {
	n.dialogs.mu.Lock()
	for pane, p := range n.dialogs.m {
		if !live[pane] && !p.reading {
			delete(n.dialogs.m, pane)
		}
	}
	n.dialogs.mu.Unlock()
}

// waitsOnUser: session id waits for the user the way its row says (sessionListIn): its host calls it blocked, or it is
// a resting Claude whose terminal shows a dialog herdr does not report (a /permissions picker, #4941). The screen is
// read now, not taken from the list's last read: a forced close must not act on a dialog already gone. A dialog too
// tall to read whole is still a dialog. A working Agent is never read and never waits.
func (n *Node) waitsOnUser(ctx context.Context, id string) bool {
	a, ok := n.agent(id)
	if !ok {
		return false
	}
	if a.AgentStatus == proto.StatusBlocked {
		return true
	}
	n.mu.Lock()
	onList := n.onAgentsList[id]
	n.mu.Unlock()
	if !restingClaude(a, a.AgentStatus) || onList || n.isDriving(id) {
		return false
	}
	text, err := n.dialogScreen(ctx, a.PaneID)
	return errors.Is(err, errDialogCut) || err == nil && screen.DialogHead(text) != ""
}
