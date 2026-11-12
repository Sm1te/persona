package node

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// paneFor returns the pane hosting a live session, refreshing herdr state once if needed.
func (n *Node) paneFor(ctx context.Context, id string) (string, error) {
	if a, ok := n.agent(id); ok {
		return a.PaneID, nil
	}
	if err := n.refreshHosts(ctx); err == nil { // the whole snapshot: agents the host does not detect (a compatible CLI) too
		if a, ok := n.agent(id); ok {
			return a.PaneID, nil
		}
	}
	// A botmux session parked for too long to be listed is still botmux's: a reply goes to botmux, which resumes it,
	// not to a claude --resume of ours.
	if h, ok := n.hosts.Get(host.Botmux).(*botmuxHost); ok {
		if _, a, err := h.row(ctx, id); err == nil {
			return a.PaneID, nil
		}
	}
	// A claude the user started in tmux has no host agent: its own record names the pane (#2562).
	if pane := n.outsidePaneOf(id); pane != "" {
		return pane, nil
	}
	return "", fmt.Errorf("session %s is not running", id)
}

func (n *Node) send(ctx context.Context, id, text string, images []proto.Image, opts sendOpts) error {
	// Two sends at once (quick taps, two phones) would both find the box empty and type into each other, or the second
	// would see the first still in the box and be refused as a draft: one at a time, in the order they came.
	unlock, err := n.inputTurn(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	if n.externalCodex(id) {
		if n.codexResumable(ctx, id) { // nothing runs it: a stopped agent takes no messages (stopped.go, #4754)
			return n.stoppedRefusal(id)
		}
		return n.sendCodex(ctx, id, text, images)
	}
	if r := n.agentsListRefusal(id); r != nil { // claudebg.go
		return r
	}
	if _, live := n.agent(id); !live {
		// A claude outside Persona's panes has it open (the desktop app, another terminal): its peer inbox, or a
		// refusal saying why not; never a second claude on the same transcript (claudebg.go).
		if handled, err := n.sendOutside(ctx, id, text, images); handled {
			return err
		}
	}
	n.dropSuggestion(id)
	if exitCommand(text) && len(images) == 0 && !n.isAssistantSession(id) && !n.runtimeAssistant(id) {
		return n.exitAgent(ctx, id)
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		ctx = n.withResumeHost(ctx, id)
	}
	if err != nil && (n.codexPath(id) != "" || n.traexPath(id) != "") {
		return n.stoppedRefusal(id) // a stopped Codex or TRAEX takes no messages (stopped.go, #4754)
	}
	kind := "claude"
	if err == nil {
		if a, ok := n.agent(id); ok && a.Agent != "" {
			kind = a.Agent
		}
		if kind == "codex" && n.codexJoined(id) {
			// On its app-server (codex_daemon.go): a turn or a steer, nothing typed — a draft in its box stays as it is.
			return n.sendCodex(ctx, id, text, images)
		}
		if err := n.checkPaneFor(ctx, pane, kind, opts); err != nil { // sendsafe.go
			return err
		}
		if kind == "claude" && opts.ApplySwitch {
			// The user's own message carries out the switch to another launcher they picked (launcher_switch.go): the
			// agent is quit and resumed there, in the same terminal, and the message goes to it. While the agent is busy
			// the message goes on the launcher it runs on and the switch waits; a failed switch is dropped with nothing
			// typed. A switch kept with the record waits for the hub's copy of it after a node restart.
			_ = n.state.loaded(ctx)
			if ps := n.pendingSwitch(id); ps != nil {
				switched := false
				if pane, switched, err = n.applySwitch(ctx, id, pane, *ps); err != nil {
					return err
				}
				// The new claude came up behind a dialog of its own (start returns early then): the text would answer it.
				if switched {
					if err := n.checkPane(ctx, pane, "claude", ""); err != nil {
						return err
					}
				}
			}
		}
	} else {
		// A session of the desktop app's sidebar that nothing runs: the app opens it, and the message goes to its inbox,
		// not to a claude --resume of ours (claudedesktop_open.go).
		if handled, err := n.sendThroughDesktopApp(ctx, id, text, images); handled {
			return err
		}
		// A session continuing in a runtime is taken over there (runtimecontrol.go).
		if _, ok := engineRuntimes[n.startKind(ctx)]; ok {
			return n.continueInRuntime(ctx, id, text, images, opts.ClientMsgID)
		}
		if r := n.notRunningRefusal(ctx, id); r != nil {
			return r
		}
		// A claude outside Persona's panes that has it open (Claude's desktop app, another terminal, a background run)
		// is no stopped agent: refused as open elsewhere, as before (openelsewhere.go).
		if r := n.openElsewhereRefusal(id); r != nil {
			return r
		}
		// A stopped agent takes no messages; its terminal page's Resume brings it back (stopped.go, #4754). Only a
		// branch nothing has started yet (fork.go) is started by its first message, with the launcher it came from,
		// and a session of Claude's desktop app the app could not take is resumed as before.
		if !n.freshFork(id) && !n.desktopSession(id) {
			return n.stoppedRefusal(id)
		}
		r := n.resumeWith(id, opts)
		s, err := n.start(ctx, r.cwd, r.launcher, "", id, "", r.mode, r.effort, r.tab, "", "", r.execution)
		if err != nil {
			return err
		}
		n.forkStarted(id)
		pane = s.PaneID
		// start returns early when Claude comes up blocked (a dialog of its own): the text would answer it.
		if err := n.checkPane(ctx, pane, "claude", ""); err != nil {
			return err
		}
	}
	if kind == "claude" {
		// A queued ultracode switch goes in before this message while Claude rests; at work, the message joins Claude's
		// own queue and the switch waits for the turn to end (ultracode.go). Whatever becomes of the switch (its
		// ultracode_note says), the message is sent: only a command that may still sit in the input box is not typed
		// over.
		if typed, err := n.applyUltracode(ctx, id, pane); typed && err != nil {
			if err := n.ultracodeBoxClear(ctx, pane); err != nil {
				return err
			}
		}
	}
	paths, err := n.saveImages(images)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		// In a tag so it never mixes with what the user typed; the phone shows these as thumbnails.
		text = strings.TrimSpace(text) + "\n\n<attached_images>\n" + strings.Join(paths, "\n") + "\n</attached_images>"
	}
	text = inertControls(text) // as the host pastes it (hostPrompt): the box and the transcript are matched against it
	var watch *promptWatch
	if kind == "claude" {
		text = keepImagePaths(text)
		watch = n.watchPrompt(id, text) // where the transcript ends before the typing: what comes after may be this (sendsafe_submit.go)
	}
	typedAt := time.Now()
	err = n.prompt(ctx, pane, text)
	if errors.Is(err, host.ErrAgentBlocked) && (kind == "codex" || kind == traexAgent) {
		// herdr refuses to prompt a Codex with an async question open; its box (checked above) takes the message.
		err = n.codexType(ctx, pane, text, func(draft string) bool { return stillPrompted(draft, text) || pastedRE.MatchString(draft) })
	}
	if r := deliveryRefusal(err); r != nil { // the host refused it with nothing typed (herdr-core, Orca's front)
		log.Printf("send %s: the host refused it, nothing typed: %v", id, err) // which check refused, in its words
		return r
	}
	if err == nil {
		n.typing.typed(pane, text)
		n.lostFirst.clear(id) // the message the start could not deliver went in now, or another one
		if kind == "claude" {
			return n.afterPrompt(ctx, id, pane, text, typedAt, watch) // sendsafe_submit.go
		}
	}
	return uncertain(err)
}

// prompt submits text to the agent in a pane. A host only prompts agents it detects (herdr); for the others (a compatible CLI)
// the text is pasted into the pane and enter pressed, only while a program other than a shell runs in front of it
// (typeable).
func (n *Node) prompt(ctx context.Context, pane, text string) error {
	err := n.hostPrompt(ctx, pane, text)
	if err == nil || !errors.Is(err, host.ErrNoAgent) {
		return err
	}
	if err := n.typeable(ctx, pane); err != nil {
		return err
	}
	paste := pasteStart + inertControls(text) + pasteEnd
	if err := n.sendText(ctx, pane, paste); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond) // the TUI takes the pasted text before the enter that submits it
	return n.sendKeys(ctx, pane, "enter")
}

// errShellOnly: the pane runs nothing but a shell in front (its agent exited), which would run typed text as a
// command; nothing was typed (#1185).
var errShellOnly = errors.New("only a shell in the pane")

// typeable: a pane whose agent the host does not know takes typed text only while a program other than a shell runs
// in front of it. The shell itself is in front once the agent exits, so "any process" is not enough, and pasting does
// not help: the enter after it runs the whole paste. A host that cannot say, or a failed check, types nothing.
func (n *Node) typeable(ctx context.Context, pane string) error {
	h, ref, err := n.paneHost(pane)
	if err != nil {
		return err
	}
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return host.Mark(fmt.Errorf("%s cannot say what runs in %s", h.Kind(), pane), host.ErrNotDelivered)
	}
	names, err := f.PaneForeground(ctx, ref)
	if err != nil {
		return host.Mark(fmt.Errorf("cannot check what runs in %s: %w", pane, err), host.ErrNotDelivered)
	}
	for _, name := range names {
		if !shellProgram(name) {
			return nil
		}
	}
	return host.Mark(host.Mark(fmt.Errorf("no agent runs in %s, only %q", pane, names), host.ErrNotDelivered), errShellOnly)
}

func (n *Node) saveImages(images []proto.Image) ([]string, error) {
	if len(images) == 0 {
		return nil, nil
	}
	dir := filepath.Join(n.dir, "uploads", time.Now().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var paths []string
	for _, img := range images {
		data, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return nil, fmt.Errorf("bad image data: %w", err)
		}
		ext := ".jpg"
		switch img.Mime {
		case "image/png":
			ext = ".png"
		case "image/heic":
			ext = ".heic"
		case "image/gif":
			ext = ".gif"
		case "image/webp":
			ext = ".webp"
		}
		p := filepath.Join(dir, newUUID()[:8]+ext)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// imagePathLine starts the line that repeats a photo's path in an <attached_images> block as text (keepImagePaths).
// No space after the colon: older Claude Code (2.1.296) cuts a pasted line at every space before "/" and attaches each
// piece that is an image path, so "path: <path>" attached the picture a second time (#4361). Messages sent before
// still have "path: <path>"; the apps read both.
const imagePathLine = "path:"

// keepImagePaths gives each path in the <attached_images> block that ends text (the node's own, or one a client wrote
// after uploading the photos itself, as Android's composer does) a second line, "path:<path>". Claude Code's paste
// turns a line that is only a path to a readable image into an `[Image #n]` and drops it. The path: line stays text on
// Claude Code 2.1.316 and later, so the agent keeps the path; older versions drop it without attaching anything (they
// tell the model the picture's source path themselves). The apps take only lines starting with "/" or "~" as photos. A
// block with any other line (prose that ends in the tag, or path: lines already added) is left as it is. With words
// before it, the block makes Claude fold the paste into a `[Pasted text #n]` placeholder (longPaste).
func keepImagePaths(text string) string {
	const open = "<attached_images>"
	rest, ok := strings.CutSuffix(strings.TrimRight(text, " \t\r\n"), "</attached_images>")
	i := strings.LastIndex(rest, open)
	if !ok || i < 0 || strings.TrimSpace(rest[i+len(open):]) == "" {
		return text
	}
	var b strings.Builder
	b.WriteString(rest[:i+len(open)] + "\n")
	for _, l := range strings.Split(rest[i+len(open):], "\n") {
		if l = strings.TrimSpace(l); l == "" {
			continue
		}
		if !filepath.IsAbs(l) && !strings.HasPrefix(l, "~") {
			return text
		}
		b.WriteString(l + "\n" + imagePathLine + l + "\n")
	}
	return b.String() + "</attached_images>"
}

func (n *Node) keys(ctx context.Context, id string, keys []string, text string) error {
	if err := n.switchingRefusal(id); err != nil { // a launcher switch, maybe on the last connection's Node
		return err
	}
	n.dropSuggestion(id) // whatever goes in replaces or sends it
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return err
	}
	if text != "" && !n.canTypeText(pane) {
		// A host that cannot type without submitting: text and then enter is a prompt; anything else was refused
		// before any key was pressed (typingRefusal).
		if len(keys) != 1 || keys[0] != "enter" {
			return refusal(cannotType, cannotTypeMessage, nil)
		}
		err := n.hostPrompt(ctx, pane, text)
		if r := deliveryRefusal(err); r != nil {
			return r
		}
		return err
	}
	if text != "" {
		if err := n.sendText(ctx, pane, text); err != nil {
			return err
		}
	}
	if len(keys) > 0 {
		return n.sendKeys(ctx, pane, keys...)
	}
	return nil
}
