package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
)

// Starting a Codex with no first message (docs/integrations/codex.md "Starting without a first message", #4961).
//
// Codex makes its thread only when it gets its first message, so a start without one has no thread id to answer with.
// The node runs the launcher in a new tab as for a start with a message (codex-start, with no message), waits for
// Codex's input box (answering the folder trust dialog, skipping an update offer), and answers with a row of its own:
// a waiting row (codexWait), keyed by an id the node makes, for the pane. The row takes the first message from the
// apps (sendFirstCodex) and opens its terminal; the user may as well type the message there. Once the thread exists
// it is attached to the row, never guessed from the directory:
//
//  1. the rollout the pane's own codex process holds open (its HERDR_PANE_ID): Codex 0.153, and any launcher whose
//     thread runs in its own process. Never an app-server's: a daemon a pane started inherits that pane's
//     HERDR_PANE_ID and holds every thread it runs;
//  2. the thread herdr's codex integration names for the pane;
//  3. a first message sent from the apps: the one thread begun since the row whose first message is exactly it and
//     that no other pane runs;
//  4. on the shared app-server daemon (0.163 and later), whose TUI holds no rollout: the only new thread in the
//     directory since the row began, when its first message shows on the screen of exactly one waiting pane.
//
// Anything else attaches nothing and is logged: the row stays, its terminal works, and the thread is listed as Codex
// history like any other. The row goes when codex leaves its pane.

// codexWaitPrefix starts a waiting row's id: a UUID whose first group is fixed, so the row is known as one after the
// node restarts, from the pane binding the hub gives back (loadState), with nothing more stored. A Codex thread's id (a
// UUIDv7) starts with its time in milliseconds, which reaches c0de0000 in some six thousand years; a Claude session's
// is random.
const codexWaitPrefix = "c0de0000-"

func codexWaitID() string { return codexWaitPrefix[:8] + newUUID()[8:] }

func isCodexWaitID(id string) bool { return len(id) == 36 && strings.HasPrefix(id, codexWaitPrefix) }

// codexWait is a Codex opened with no first message, until its thread is known.
type codexWait struct {
	id, pane, cwd string
	// began is when codex was run in the pane: rules 3 and 4 take only threads begun after it.
	began time.Time
	// herdr is the thread herdr's codex integration names for the pane, titled the one its title names (setAgentsAt,
	// codexTitleThread); "" for none.
	herdr, titled string
	// judged counts the looks each new thread in the directory went through without being attached: after
	// codexJudgeLooks it no longer makes every look scan the processes (one Codex outside herdr in the same repository
	// would have it scan every second for as long as the row waits).
	judged map[string]int
	// note is why the last look attached nothing; logged when it changes.
	note string
	// missing counts the looks in a row that found no codex in the pane.
	missing int
}

var (
	// codexWaitTick is how often the waiting rows' threads are looked for.
	codexWaitTick = time.Second
	// codexBoxWait is how long a first message waits for Codex's input box (models loading, MCP servers starting).
	codexBoxWait = 20 * time.Second
	// codexFirstWait is how long a first message waits for its thread once typed.
	codexFirstWait = 45 * time.Second
	// codexWaitGone is how many looks in a row may find no codex in a waiting row's pane before the row goes.
	codexWaitGone = 10
	// codexStartPoll is how often a start or a first message looks at Codex's screen and threads again.
	codexStartPoll = 500 * time.Millisecond
	// codexDialogPause lets a dialog answered at the start go before the next look, which would otherwise answer it again.
	codexDialogPause = time.Second
)

// errCodexBoxLate: codex runs in the new tab but its input box did not come up within codexStartWait.
var errCodexBoxLate = errors.New("codex box late")

// codexLoadingRE: Codex 0.153 draws its input box before its model is known ("model:     loading" in the header).
var codexLoadingRE = lazyre.New(`(?m)^\W*(model|directory):\s+loading\b`)

// codexBusyStarting: Codex starts its MCP servers after the model ("• Starting MCP servers (1/3): …"), and herdr
// shows the pane working meanwhile.
func codexBusyStarting(text string) bool { return strings.Contains(text, "Starting MCP servers") }

// waitCodexBox waits for the input box of the Codex a start without a message ran: what it takes for the row to be
// offered (codexStartWait at most). The folder trust dialog and an update offer are answered on the way as
// waitCodexThread does. nil once the box shows with the model known; errCodexBoxLate when codex runs but its box did
// not come in time; the launcher's failure (codex-start's line) at once.
func (n *Node) waitCodexBox(ctx context.Context, pane string) error {
	deadline := time.Now().Add(codexStartWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(codexStartPoll):
		}
		text, err := n.readPane(ctx, pane, "visible", screen.DialogLines)
		if err != nil {
			continue
		}
		if i := strings.LastIndex(text, codexStartError); i >= 0 {
			return errors.New(strings.TrimSpace(strings.SplitN(text[i:], "\n", 2)[0]))
		}
		if n.answerCodexStartDialog(ctx, pane, text) {
			continue
		}
		if screen.ReadCodex(text).View == screen.CodexComposer && !codexLoadingRE.MatchString(text) {
			return nil
		}
	}
	if agent, err := n.paneAgent(ctx, pane); err == nil && agent != nil && agent.Agent == "codex" {
		return errCodexBoxLate
	}
	return &rpc.Error{Code: "codex_no_box", Message: fmt.Sprintf("Codex 已经在电脑上的新标签里打开了，但 %d 秒内没看到它的输入框", int(codexStartWait.Seconds()))}
}

// answerCodexStartDialog answers what Codex shows before its input box as the user would for a start they asked for:
// the folder is trusted (they chose it), an update offer is skipped — with Skip's own key only, never enter, which
// would take the highlighted "Update now" and run the installer. true when it pressed a key.
func (n *Node) answerCodexStartDialog(ctx context.Context, pane, text string) bool {
	if keys := codexTrustKeys(text); keys != nil {
		_ = n.sendKeys(ctx, pane, keys...)
		_ = sleepCtx(ctx, codexDialogPause)
		return true
	}
	if keys := codexSkipUpdateKeys(text); keys != nil {
		_ = n.sendKeys(ctx, pane, keys...)
		_ = sleepCtx(ctx, codexDialogPause)
		return true
	}
	return false
}

// codexSkipUpdateKeys are the keys that skip Codex's update offer ("✨ Update available! …", "› 1. Update now (runs
// …)", "2. Skip", "3. Skip until next version", "Press enter to continue"), nil when the screen does not show it. Its
// number picks an option at once (Codex 0.153.4, 2026-11-12: "2" closed the offer); a list whose Skip would need enter
// is left alone.
func codexSkipUpdateKeys(text string) []string {
	if !strings.Contains(text, "Update available") {
		return nil
	}
	d := screen.ParseDialog(text)
	if d == nil {
		return nil
	}
	for _, o := range d.Options {
		if o.Label == "Skip" && len(o.Keys) > 0 && !strings.Contains(strings.Join(o.Keys, " "), "enter") {
			return o.Keys
		}
	}
	return nil
}

// openCodexWait makes the waiting row of a Codex the start opened with no message and answers with it.
func (n *Node) openCodexWait(ctx context.Context, pane, cwd, launcher, mode string, began time.Time) *proto.Session {
	w := &codexWait{id: codexWaitID(), pane: pane, cwd: cwd, began: began}
	st := sessionState{Pane: pane, Launcher: launcher, Cwd: cwd, StartedAt: began.UTC()}
	if mode == "plan" { // as a start with a message keeps it (codex_start.go), for the thread to take
		st.AppMode = mode
	}
	n.state.put(w.id, st)
	n.mu.Lock()
	n.panes[pane] = w.id // given back by the hub after a restart, which is how the row is known again (codexWaitOf)
	n.addCodexWaitLocked(w)
	n.mu.Unlock()
	n.logf("codex %s: open with no first message, row %s", pane, w.id)
	_ = n.refreshHosts(ctx)
	a, ok := n.agent(w.id)
	if !ok {
		a = host.Agent{Agent: "codex", PaneID: pane, Cwd: cwd, AgentStatus: proto.StatusIdle}
	}
	s := n.sessionFor(w.id, a)
	codexWaitRow(s, a)
	s.UpdatedAt = began.UTC()
	return s
}

// addCodexWaitLocked registers w and starts looking for its thread; the caller holds n.mu.
func (n *Node) addCodexWaitLocked(w *codexWait) {
	if n.codexWaiting == nil {
		n.codexWaiting = map[string]*codexWait{}
	}
	n.codexWaiting[w.pane] = w
	if !n.codexWaitLooking {
		n.codexWaitLooking = true
		go n.lookForCodexThreads()
	}
}

// codexWaitOf is the waiting row of a Codex pane, nil for none. After a restart the row is known again from the
// pane's binding to a waiting row's id and that id's record.
func (n *Node) codexWaitOf(pane string) *codexWait {
	n.mu.Lock()
	w := n.codexWaiting[pane]
	id := n.panes[pane]
	n.mu.Unlock()
	if w != nil || !isCodexWaitID(id) {
		return w
	}
	st, ok := n.state.get(id)
	if !ok || st.Pane != pane {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if w = n.codexWaiting[pane]; w == nil && n.panes[pane] == id {
		w = &codexWait{id: id, pane: pane, cwd: st.Cwd, began: st.StartedAt}
		n.addCodexWaitLocked(w)
	}
	return w
}

// codexWaitByID is the waiting row id names, nil when it is none (or its thread came).
func (n *Node) codexWaitByID(id string) *codexWait {
	if !isCodexWaitID(id) {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, w := range n.codexWaiting {
		if w.id == id {
			return w
		}
	}
	return nil
}

// codexAttachedThread is the thread a waiting row became, "" while it waits or for any other id: a client that
// looked before the thread came still names the row.
func (n *Node) codexAttachedThread(id string) string {
	if !isCodexWaitID(id) {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.codexAttached[id].thread
}

// codexAttach is the thread a waiting row became, and when.
type codexAttach struct {
	thread string
	at     time.Time
}

// codexAttachedFor is how long requests that still name a row reach its thread: a page open on the row follows the
// pane to the thread within seconds (the apps), a node restart forgets them all.
const codexAttachedFor = time.Hour

// codexWaitRow makes a listed row the waiting row it is: Codex is open but no turn has run, whatever herdr says of
// the pane (Codex starting its MCP servers reads as working, then done), unless a dialog holds it.
func codexWaitRow(s *proto.Session, a host.Agent) {
	s.AwaitingFirstMessage = true
	s.Agent = "codex"
	s.Title = "" // not the terminal's ("codex", then the directory Codex shows): the directory, as for any untitled row
	if s.Cwd != "" {
		s.Title = filepath.Base(s.Cwd)
	}
	s.CodexRoute, s.CodexRouteNote = "", ""
	s.LastText, s.Asks = "", ""
	if a.AgentStatus == proto.StatusBlocked {
		s.Status = proto.StatusBlocked
	} else {
		s.Status = proto.StatusIdle
	}
	s.Capabilities = &proto.SessionCapabilities{Read: true, Send: true, Terminal: true}
}

// codexHeld is what the Codex processes hold open now: by herdr pane, the threads its own codex process (not an
// app-server) writes; and every thread such a process holds, with its pane ("" outside herdr).
type codexHeld struct {
	byPane map[string][]heldThread
	owner  map[string]string
	ok     bool // every process could be read
}

type heldThread struct{ home, id, path string }

// codexHeldThreads scans the Codex processes and the rollouts they hold open.
func (n *Node) codexHeldThreads() codexHeld {
	held := codexHeld{byPane: map[string][]heldThread{}, owner: map[string]string{}}
	var procs []codexProcess
	listed := true
	if n.codices != nil {
		n.codices.mu.Lock()
		scan, checked := n.codices.scan, n.codices.checked
		n.codices.mu.Unlock()
		switch {
		case checked != nil:
			procs, listed = checked()
		case scan != nil:
			procs = scan()
		default:
			procs, listed = scanCodexProcessesChecked()
		}
	}
	var tuis []codexProcess
	for _, p := range procs {
		if p.endpoint == "" { // an app-server holds the threads of every client it serves
			tuis = append(tuis, p)
		}
	}
	open, read := scanOpenRollouts(tuis)
	held.ok = listed && read
	for _, p := range tuis {
		home := canonicalCodexHome(firstNonEmpty(p.home, n.codexHome()), n.home)
		pane := ""
		if p.pane != "" {
			pane = n.herdrPaneIn(p.pane, p.herdrSession)
		}
		for _, path := range open[p.pid] {
			if !pathWithin(path, home) {
				continue
			}
			thread := rolloutThreadID(home, path)
			if thread == "" || metaOf(home, path).sub {
				continue
			}
			id := n.codexPublicID(home, thread)
			held.owner[id] = pane
			if pane != "" {
				held.byPane[pane] = append(held.byPane[pane], heldThread{home: home, id: id, path: path})
			}
		}
	}
	return held
}

// newThread is a thread begun since a waiting row, with its first message.
type newThread struct {
	id, first string
	began     time.Time
}

// newCodexThreads are the threads begun in w's directory since w began that have their first message, no other
// pane runs and no Codex process holds open for a pane other than w's.
func (n *Node) newCodexThreads(w *codexWait, held codexHeld) []newThread {
	resolved := w.cwd
	if r, err := filepath.EvalSymlinks(w.cwd); err == nil {
		resolved = r
	}
	homes := []string{n.codexHome()}
	for _, h := range n.codexInventory().Homes {
		if h != homes[0] {
			homes = append(homes, h)
		}
	}
	var out []newThread
	seen := map[string]bool{}
	for _, home := range homes {
		for _, r := range rollouts(home) {
			if r.sub || (r.cwd != w.cwd && r.cwd != resolved) || r.mtime.Before(w.began.Add(-time.Second)) {
				continue
			}
			at, first := codexThreadStart(r.path)
			if at.Before(w.began.Add(-2*time.Second)) || strings.TrimSpace(first) == "" {
				continue
			}
			id := n.codexPublicID(home, r.id)
			if seen[id] {
				continue
			}
			seen[id] = true
			if owner, ok := held.owner[id]; ok && owner != w.pane {
				continue // another pane's codex writes it
			}
			if a, live := n.agent(id); live && a.PaneID != w.pane {
				continue
			}
			out = append(out, newThread{id: id, first: first, began: at})
		}
	}
	return out
}

// codexWaitThread is the thread rules 1 and 2 give a waiting row, and which rule; "" for none.
func (n *Node) codexWaitThread(w *codexWait, held codexHeld) (string, string) {
	n.mu.Lock()
	named, titled := w.herdr, w.titled
	n.mu.Unlock()
	if ts := held.byPane[w.pane]; len(ts) == 1 {
		return ts[0].id, "its codex process holds the rollout"
	} else if len(ts) > 1 {
		return "", fmt.Sprintf("its codex process holds %d rollouts", len(ts))
	}
	if named != "" {
		if owner, ok := held.owner[named]; !ok || owner == w.pane {
			return named, "herdr names the thread"
		}
		return "", "herdr names thread " + named + ", which another pane's codex writes"
	}
	if titled != "" {
		if owner, ok := held.owner[titled]; !ok || owner == w.pane {
			return titled, "its title names the thread"
		}
		return "", "its title names thread " + titled + ", which another pane's codex writes"
	}
	return "", ""
}

// codexTitleThread is the thread a waiting pane's title names ("<name> | <directory>"), when exactly one thread of
// the pane's Codex home has that whole name: what a /resume in that Codex shows, which on the shared app-server no
// process of the pane's holds (rule 1) and no new thread begins (rules 3, 4). A name cut short ("…") or shared by two
// threads names none.
func (n *Node) codexTitleThread(a host.Agent) string {
	i := strings.LastIndex(a.Title, " | ")
	if i <= 0 {
		return ""
	}
	name := strings.TrimSpace(a.Title[:i])
	name = strings.TrimPrefix(name, "[ . ] Action Required | ")
	name = strings.TrimPrefix(name, "[ ! ] Action Required | ")
	if name == "" || strings.HasSuffix(name, "...") || strings.HasSuffix(name, "…") {
		return ""
	}
	home := n.codexPaneHome(a.PaneID)
	var found []string
	for _, id := range threadsNamed(home, func(t string) bool { return t == name }) {
		if p := rolloutPath(home, id); p != "" && metaOf(home, p).sub {
			continue
		}
		found = append(found, id)
	}
	if len(found) != 1 {
		return ""
	}
	return n.codexPublicID(home, found[0])
}

// lookForCodexThreads looks for the waiting rows' threads every codexWaitTick until none waits.
func (n *Node) lookForCodexThreads() {
	for tick := 0; ; tick++ {
		time.Sleep(codexWaitTick)
		n.mu.Lock()
		if len(n.codexWaiting) == 0 {
			n.codexWaitLooking = false
			n.mu.Unlock()
			return
		}
		waits := make([]*codexWait, 0, len(n.codexWaiting))
		for _, w := range n.codexWaiting {
			waits = append(waits, w)
		}
		n.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		n.lookForCodexThreadsOnce(ctx, waits, tick%codexWaitScanEvery == 0)
		cancel()
	}
}

// codexWaitScanEvery: with no new thread in a waiting row's directory and none named by herdr, the Codex processes
// are scanned only every this many looks (a row may wait for hours; a scan lists every process's environment). A
// thread rule 1 finds with no new rollout is an older one the user resumed in that Codex.
const codexWaitScanEvery = 15

// lookForCodexThreadsOnce is one look: each row whose thread rule 1 or 2 knows is attached, then rule 4 over the
// rest; a row whose pane runs no codex any more goes. The processes are scanned when a row has a thread to find or
// full says so.
func (n *Node) lookForCodexThreadsOnce(ctx context.Context, waits []*codexWait, full bool) {
	var live []*codexWait
	for _, w := range waits {
		if n.codexWaitGone(ctx, w) {
			continue
		}
		live = append(live, w)
		n.mu.Lock()
		named := w.herdr != "" || w.titled != ""
		n.mu.Unlock()
		if named {
			full = true
		}
		for _, t := range n.newCodexThreads(w, codexHeld{}) {
			if w.judged[t.id] < codexJudgeLooks {
				full = true
			}
		}
	}
	if !full {
		return
	}
	held := n.codexHeldThreads()
	var rest []*codexWait
	for _, w := range live {
		if thread, how := n.codexWaitThread(w, held); thread != "" {
			n.attachCodexWait(ctx, w, thread, how)
			continue
		} else if how != "" {
			n.codexWaitNote(w, how)
			continue
		}
		rest = append(rest, w)
	}
	n.attachCodexByScreen(ctx, rest, held)
	for _, w := range rest {
		if n.codexWaitByID(w.id) != w {
			continue
		}
		if w.judged == nil {
			w.judged = map[string]int{}
		}
		for _, t := range n.newCodexThreads(w, held) {
			w.judged[t.id]++
		}
	}
}

// codexJudgeLooks: a new thread that no rule attached in this many looks is looked at again only with the periodic
// full scan (codexWaitScanEvery).
const codexJudgeLooks = 3

// codexWaitGone drops a waiting row whose pane runs no codex any more (codexWaitGone looks in a row): the user quit
// Codex before it had a thread, or closed the tab. Its terminal is left as it is.
func (n *Node) codexWaitGone(ctx context.Context, w *codexWait) bool {
	if _, live := n.agent(w.id); live {
		w.missing = 0
		return false
	}
	if a, err := n.paneAgent(ctx, w.pane); err == nil && a != nil && a.Agent == "codex" {
		w.missing = 0
		return false
	}
	if w.missing++; w.missing < codexWaitGone {
		return false
	}
	n.mu.Lock()
	if n.codexWaiting[w.pane] == w {
		delete(n.codexWaiting, w.pane)
	}
	if n.panes[w.pane] == w.id {
		delete(n.panes, w.pane)
	}
	n.mu.Unlock()
	n.state.setPane(w.id, "")
	n.logf("codex %s: left its pane before it had a thread; row %s goes", w.pane, w.id)
	n.notifySessions()
	return true
}

// attachCodexByScreen is rule 4, for the rows whose codex process holds no rollout (the TUI of a shared app-server):
// a row takes a thread only when that thread is the one new thread in its directory since it began, the thread is
// at least two seconds old (a codex process of its own would hold it by then: rule 1), and its first message shows
// on the screen of this row's pane and of no other waiting row's.
func (n *Node) attachCodexByScreen(ctx context.Context, waits []*codexWait, held codexHeld) {
	if len(waits) == 0 {
		return
	}
	screens := map[string]string{}
	screenOf := func(pane string) string {
		if s, ok := screens[pane]; ok {
			return s
		}
		text, _ := n.readPane(ctx, pane, "recent", 200)
		screens[pane] = text
		return text
	}
	n.mu.Lock()
	all := make([]*codexWait, 0, len(n.codexWaiting))
	for _, w := range n.codexWaiting {
		all = append(all, w)
	}
	n.mu.Unlock()
	for _, w := range waits {
		threads := n.newCodexThreads(w, held)
		switch {
		case len(threads) == 0:
			continue
		case len(threads) > 1:
			n.codexWaitNote(w, fmt.Sprintf("%d new threads in its directory", len(threads)))
			continue
		case time.Since(threads[0].began) < 2*time.Second:
			continue
		}
		t := threads[0]
		var on []string
		for _, o := range all {
			if codexMessageShown(screenOf(o.pane), t.first) {
				on = append(on, o.pane)
			}
		}
		switch {
		case len(on) == 1 && on[0] == w.pane:
			n.attachCodexWait(ctx, w, t.id, "its first message shows on this pane only")
		case len(on) == 0:
			n.codexWaitNote(w, "thread "+t.id+": its first message shows on no waiting pane")
		default:
			n.codexWaitNote(w, fmt.Sprintf("thread %s: its first message shows on %d waiting panes", t.id, len(on)))
		}
	}
}

// codexMessageShown: the screen shows message as a user message of Codex's: a "› " line with the lines indented
// under it, the same text once the wrapping and the indent are taken out. The input box is such a block too, and
// counts (a message still in a box makes the match ambiguous, never wrong).
func codexMessageShown(text, message string) bool {
	want := strings.Join(strings.Fields(message), " ")
	if want == "" {
		return false
	}
	lines := strings.Split(screen.Plain(text), "\n")
	for i := 0; i < len(lines); i++ {
		l := strings.TrimLeft(lines[i], " ")
		rest, ok := strings.CutPrefix(l, "› ")
		if !ok {
			continue
		}
		block := []string{rest}
		for j := i + 1; j < len(lines) && strings.HasPrefix(lines[j], "  ") && strings.TrimSpace(lines[j]) != ""; j++ {
			block = append(block, lines[j])
		}
		if strings.Join(strings.Fields(strings.Join(block, " ")), " ") == want {
			return true
		}
	}
	return false
}

// codexWaitNote logs why a waiting row has no thread yet, once per reason.
func (n *Node) codexWaitNote(w *codexWait, note string) {
	n.mu.Lock()
	changed := w.note != note
	w.note = note
	n.mu.Unlock()
	if changed {
		n.logf("codex %s: row %s not attached: %s", w.pane, w.id, note)
	}
}

// attachCodexWait makes thread the session of w's pane: the start's record (launcher, directory, time, read-only mode,
// creation receipt) moves to the thread, the pane is bound to it, and requests that still name the row go to it.
func (n *Node) attachCodexWait(ctx context.Context, w *codexWait, thread, how string) bool {
	n.mu.Lock()
	waits := n.codexWaiting[w.pane] == w
	n.mu.Unlock()
	if !waits {
		return false
	}
	// The thread's record first, then the pane moves to it in the same step that ends the wait: a list built in between
	// would find the pane still bound to the row's id and make the row again (codexWaitOf).
	st, _ := n.state.get(w.id)
	n.state.put(thread, sessionState{Pane: w.pane, Launcher: st.Launcher, Cwd: st.Cwd, StartedAt: st.StartedAt, AppMode: st.AppMode,
		CreationID: st.CreationID, Incarnation: st.Incarnation})
	n.mu.Lock()
	if n.codexWaiting[w.pane] != w {
		n.mu.Unlock()
		return false
	}
	delete(n.codexWaiting, w.pane)
	n.panes[w.pane] = thread
	if n.codexAttached == nil {
		n.codexAttached = map[string]codexAttach{}
	}
	for id, a := range n.codexAttached { // a client that still names a row asks within minutes, not hours
		if time.Since(a.at) > codexAttachedFor {
			delete(n.codexAttached, id)
		}
	}
	n.codexAttached[w.id] = codexAttach{thread: thread, at: time.Now()}
	n.mu.Unlock()
	n.state.setPane(w.id, "")
	n.logf("codex %s: thread %s attached to row %s (%s)", w.pane, thread, w.id, how)
	_ = n.refreshHosts(ctx)
	n.notifySessions()
	return true
}

// sendFirstCodex types the first message into a waiting row's Codex and answers with the session of the thread it
// began; nil (and no error) when the message went in but its thread could not be told apart (rule 3 found two), in
// which case the row stays until rule 1 or 2 knows it.
func (n *Node) sendFirstCodex(ctx context.Context, w *codexWait, text string, images []proto.Image) (*proto.Session, error) {
	if len(images) > 0 {
		return nil, refusal("codex_first_text", "Codex 的第一条消息只能是文字：去掉图片或文件再发，这次什么都没输入。", nil)
	}
	if strings.TrimSpace(text) == "" {
		return nil, refusal("codex_first_text", "第一条消息是空的，这次什么都没输入。", nil)
	}
	if err := n.codexBoxFor(ctx, w.pane); err != nil {
		return nil, err
	}
	text = inertControls(text) // as typed: the thread's first message is matched against it
	sent := time.Now()
	if err := n.codexType(ctx, w.pane, text, func(draft string) bool { return sameText(draft, text) || pastedRE.MatchString(draft) }); err != nil {
		return nil, err
	}
	n.typing.typed(w.pane, text)
	thread, how, err := n.waitFirstThread(ctx, w, text, sent)
	if err != nil {
		return nil, err
	}
	if thread == "" {
		n.codexWaitNote(w, how)
		return nil, nil
	}
	n.attachCodexWait(ctx, w, thread, how)
	return n.codexWaitSession(w, thread), nil
}

// codexWaitSession is the session of the thread w became, as listed, else as its pane runs it.
func (n *Node) codexWaitSession(w *codexWait, thread string) *proto.Session {
	a, ok := n.agent(thread)
	if !ok {
		a = host.Agent{Agent: "codex", PaneID: w.pane, Cwd: w.cwd, AgentStatus: proto.StatusWorking}
	}
	s := n.sessionFor(thread, a)
	s.Agent = "codex"
	return s
}

// waitFirstThread waits for the thread a first message typed at sent began: rule 1 or 2, else rule 3. "" with the
// reason when rule 3 found more than one; an error when none came in codexFirstWait (the message went in: it may
// still make its thread, so it is not to be sent again).
func (n *Node) waitFirstThread(ctx context.Context, w *codexWait, text string, sent time.Time) (string, string, error) {
	deadline := time.Now().Add(codexFirstWait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", "", uncertain(ctx.Err())
		case <-time.After(codexStartPoll):
		}
		if n.codexWaitByID(w.id) != w { // attached meanwhile (the look found it first)
			if t := n.codexAttachedThread(w.id); t != "" {
				return t, "attached by the look", nil
			}
			return "", "", codexFirstUnconfirmed("Codex 在会话出现前离开了这个标签")
		}
		held := n.codexHeldThreads()
		if thread, how := n.codexWaitThread(w, held); thread != "" {
			return thread, how, nil
		}
		var match []string
		for _, t := range n.newCodexThreads(w, held) {
			if sameText(t.first, text) && !t.began.Before(sent.Add(-2*time.Second)) {
				match = append(match, t.id)
			}
		}
		switch {
		case len(match) == 1:
			return match[0], "its first message is the one sent", nil
		case len(match) > 1:
			return "", fmt.Sprintf("%d new threads begin with the message sent", len(match)), nil
		}
	}
	return "", "", codexFirstUnconfirmed(fmt.Sprintf("消息已经输入 Codex，但 %d 秒内没等到它的会话出现：在电脑上看看", int(codexFirstWait.Seconds())))
}

// codexFirstUnconfirmed: the first message was typed, its thread did not show: it may still come, so the message is
// not to be sent again (delivered unknown).
func codexFirstUnconfirmed(message string) error {
	b, _ := json.Marshal(map[string]any{"delivered": deliveredUnknown})
	return &rpc.Error{Code: "codex_first_unconfirmed", Message: message, Data: b}
}

// codexBoxFor brings a waiting row's Codex to its empty input box for the first message (codexBoxWait at most): the
// trust dialog and an update offer are answered, a model still loading and MCP servers starting are waited out, the
// transcript pager and 0.163's question pane are folded away (codexReady). A draft typed at the computer, an approval,
// a question or a dialog not ours to answer refuses with nothing typed; so does a box that does not come in time.
func (n *Node) codexBoxFor(ctx context.Context, pane string) error {
	deadline := time.Now().Add(codexBoxWait)
	for {
		text, err := n.readPane(ctx, pane, "visible", screen.DialogLines)
		if err == nil && !n.answerCodexStartDialog(ctx, pane, text) {
			s, err := n.codexScreen(ctx, pane)
			if err == nil {
				switch s.View {
				case screen.CodexComposer:
					if s.Draft != "" {
						return refusal(screen.InputDraft, codexDraftMessage(s.Draft), map[string]any{"draft": s.Draft})
					}
					if !codexLoadingRE.MatchString(text) && !codexBusyStarting(text) {
						return nil
					}
				case screen.CodexTranscript, screen.CodexAsyncPane, screen.CodexApproval, screen.CodexQuestion, screen.CodexDialog:
					if err := n.codexReady(ctx, pane, true); err != nil {
						return err
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return refusal("codex_not_ready", "电脑上的 Codex 还没到输入框（还在加载，或开着别的界面），这次什么都没输入。", nil)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(codexStartPoll):
		}
	}
}

// codexWaitCalls answers what a waiting row has no thread for: an empty conversation, nothing to watch, no modes.
// Its terminal calls are the pane's as usual (handled false).
func (n *Node) codexWaitCalls(method, id string) (any, error, bool) {
	switch method {
	case "session.items":
		return map[string]any{"items": []proto.Item{}, "has_more": false, "history": "codex-wait:" + id, "to_seq": 0}, nil, true
	case "session.item", "session.image":
		return nil, fmt.Errorf("no item"), true
	case "session.watch", "session.unwatch":
		return okResult(nil), nil, true
	case "session.modes", "session.queue.edit", "session.queue.send_now", "session.fork":
		return nil, refusal("codex_waiting", "Codex 还没收到第一条消息：这个操作要等它有了会话", nil), true
	}
	return nil, nil, false
}

// sendFirstCodexCall is session.send to a waiting row: the first message, once per client_msg_id, answered with the
// session of the thread it began ("session", left out when it could not be told apart). The apps' echo of the
// message is the thread's: its first line carries the client_msg_id as a message to the thread would (echo.go).
func (n *Node) sendFirstCodexCall(ctx context.Context, w *codexWait, params json.RawMessage, text string, images []proto.Image) (any, error) {
	var opts sendOpts
	_ = json.Unmarshal(params, &opts)
	var s *proto.Session
	sent := time.Now()
	err := n.sends.once(opts.ClientMsgID, func() error {
		// One first message at a time (two phones, a quick second tap): the second finds the thread the first began
		// and goes to it as any message does.
		unlock, err := n.inputTurn(ctx, w.id)
		if err != nil {
			return err
		}
		defer unlock()
		if n.codexWaitByID(w.id) != w {
			thread := n.codexAttachedThread(w.id)
			if thread == "" {
				return n.stoppedRefusal(w.id) // Codex left its tab before a first message
			}
			// as session.send to the thread: its echo counted from where its transcript is now (node.go)
			if err := n.sendEchoed(thread, opts.ClientMsgID, text, len(images), n.echoBase(thread), func() error {
				return n.send(ctx, thread, text, images, opts)
			}); err != nil {
				return err
			}
			s = n.codexWaitSession(w, thread)
			return nil
		}
		if exitCommand(text) && len(images) == 0 { // "/exit": Codex quits as any agent's would, nothing typed (control.go)
			return n.exitAgent(ctx, w.id)
		}
		s, err = n.sendFirstCodex(ctx, w, text, images)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"ok": true}
	if s != nil && s.SessionID != w.id {
		out["session"] = s
		if opts.ClientMsgID != "" && n.echoes.expect(echoRecord{SessionID: s.SessionID, ClientMsgID: opts.ClientMsgID, Text: text, SentAt: sent}) {
			if cur, ok := n.echoes.record(s.SessionID, opts.ClientMsgID); ok {
				n.notifyEcho(cur)
			}
		}
	}
	return out, nil
}

// withSessionID is params with session_id replaced: calls that read their own fields from params see the same id.
func withSessionID(params json.RawMessage, id string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(params, &m) != nil || m == nil {
		return params
	}
	m["session_id"], _ = json.Marshal(id)
	b, err := json.Marshal(m)
	if err != nil {
		return params
	}
	return b
}
