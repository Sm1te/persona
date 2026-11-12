package node

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/transcript"
)

// session.rewind drives Claude Code's own /rewind for an idle session (#2436). The pane is a TUI with no API, so the
// node types /rewind, walks the list of prompts to the one the app named, picks what to restore and confirms — and
// before every key it reads the screen again and stops (Esc out of the panel, nothing restored) when it is not what the
// last key should have left. Which prompt is the app's to say (its text, which of the identical ones counted from the
// end, how many prompts came after it); that the screen shows the same, in the list and then in full on the
// confirmation, is checked here. Recorded screens: screen/testdata/claude-2.1.318-rewind-*.
//
// Claude Code does not delete the rewound part of the transcript: the file is a tree (parentUuid) and the next prompt
// is written as a child of the one before the target. The rewound turns stay in the file, and a session read straight
// through shows them (docs/lessons/agents.md "Driving Claude Code `/rewind`").

// Restore choices of session.rewind.
const (
	RestoreConversation = "conversation"
	RestoreCodeAndConv  = "code_and_conversation"
)

// Codes of a rewind that did not happen (a dialog_open / input_draft refusal keeps its own).
const (
	rewindBusy        = "rewind_busy"        // the session is working or waiting on a dialog
	rewindUnsupported = "rewind_unsupported" // not a Claude session of this machine, or nothing to type into
	rewindNothing     = "rewind_nothing"     // Claude has no prompt to go back to
	rewindMismatch    = "rewind_mismatch"    // the prompt is not where the app saw it
	rewindFailed      = "rewind_failed"      // the screen was not what a step needed
)

type rewindOpts struct {
	// Prompt is the user's message to go back to, as the app shows it.
	Prompt string `json:"prompt"`
	// Occurrence: it is the Nth message with this text counting from the end (1 = the last one).
	Occurrence int `json:"occurrence"`
	// PromptsAfter: how many of the user's messages came after it. Optional; when given, the list must agree.
	PromptsAfter *int `json:"prompts_after"`
	// Restore is RestoreConversation or RestoreCodeAndConv.
	Restore string `json:"restore"`
}

// How long the steps wait for the TUI. Variables so the tests need not wait.
var (
	rewindTick       = 40 * time.Millisecond
	rewindStepWait   = 900 * time.Millisecond // a key's effect on screen, and an empty box with nothing restored
	rewindOpenWait   = 5 * time.Second        // /rewind opening, its confirmation screen
	rewindDoneWait   = 10 * time.Second       // the panel closing after the final enter
	rewindDraftWait  = 3 * time.Second        // the prompt back in the box, and out of it again
	rewindMaxSteps   = 2000                   // prompts walked at most
	rewindTotalLimit = 90 * time.Second       // the whole call
	rewindCleanWait  = 3 * time.Second        // closing the panel after a failure
)

func rewindError(code, format string, args ...any) error {
	return &rpc.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// rewindRun is one session.rewind: the pane, and the last screen read.
type rewindRun struct {
	n    *Node
	pane string
}

// read is the pane's visible screen as plain text.
func (r *rewindRun) read(ctx context.Context) (string, error) {
	return r.n.readPane(ctx, r.pane, "visible", 60)
}

// readInput is the screen with colors where the host has them (only colors tell a draft from Claude's grey
// suggestion), else without.
func (r *rewindRun) readInput(ctx context.Context) (string, error) {
	if !r.n.plainScreen(r.pane) {
		if s, err := r.n.readPaneANSI(ctx, r.pane, "visible", 60); err == nil {
			return s, nil
		}
	}
	return r.read(ctx)
}

// until reads the screen every tick until ok accepts it, or the wait is over (false).
func (r *rewindRun) until(ctx context.Context, wait time.Duration, ok func(plain string) bool) (string, bool, error) {
	end := time.Now().Add(wait)
	for {
		s, err := r.read(ctx)
		if err != nil {
			return "", false, err
		}
		if ok(s) {
			return s, true, nil
		}
		if time.Now().After(end) {
			return s, false, nil
		}
		select {
		case <-ctx.Done():
			return s, false, ctx.Err()
		case <-time.After(rewindTick):
		}
	}
}

// press sends one key and waits for the panel to look different from prev (a key that changes nothing — Up on the
// first prompt — leaves it as it is: changed false).
func (r *rewindRun) press(ctx context.Context, prev, key string) (string, bool, error) {
	if err := r.n.sendKeys(ctx, r.pane, key); err != nil {
		return "", false, err
	}
	return r.until(ctx, rewindStepWait, func(s string) bool { return s != prev })
}

// panel is the /rewind panel on a screen of the given kind, nil when the screen shows another.
func panel(plain, kind string) *screen.RewindScreen {
	if p := screen.ParseRewind(plain); p != nil && p.Kind == kind {
		return p
	}
	return nil
}

// close leaves the panel with Esc (twice from the confirmation screen), whatever state the run is in; it uses a
// context of its own so a cancelled request still cleans up.
func (r *rewindRun) close(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rewindCleanWait+time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		s, err := r.read(ctx)
		if err != nil || screen.ParseRewind(s) == nil {
			return
		}
		if _, _, err := r.press(ctx, s, "esc"); err != nil {
			return
		}
	}
}

// abort closes the panel and returns err.
func (r *rewindRun) abort(ctx context.Context, err error) error {
	r.close(ctx)
	return err
}

// rewind runs session.rewind for the session id (see the comment at the top of this file).
func (n *Node) rewind(ctx context.Context, id string, o rewindOpts) (map[string]any, error) {
	if strings.TrimSpace(o.Prompt) == "" || o.Occurrence < 1 || (o.PromptsAfter != nil && *o.PromptsAfter < 0) {
		return nil, fmt.Errorf("bad params: rewind needs the prompt, its occurrence and where it is")
	}
	if o.Restore != RestoreConversation && o.Restore != RestoreCodeAndConv {
		return nil, fmt.Errorf("bad params: restore is %q or %q", RestoreConversation, RestoreCodeAndConv)
	}
	if err := n.switchingRefusal(id); err != nil { // a launcher switch, maybe on the last connection's Node
		return nil, err
	}
	// Typing into the pane: one thing at a time (a message sent meanwhile would land in the panel).
	defer n.typing.lock(id)()
	if n.isAssistantSession(id) {
		return nil, rewindError(rewindUnsupported, "助手的对话不能回退。")
	}
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return nil, rewindError(rewindUnsupported, "这个会话没有在终端里运行，不能回退。")
	}
	a, _ := n.agent(id)
	if a.Agent != "" && a.Agent != "claude" {
		return nil, rewindError(rewindUnsupported, "只有 Claude 会话能回退。")
	}
	if !n.canTypeText(pane) {
		return nil, rewindError(rewindUnsupported, "这台电脑的终端不能输入文字，没法打开回退界面。")
	}
	switch a.AgentStatus {
	case proto.StatusWorking, proto.StatusBlocked, proto.StatusOffline:
		return nil, rewindError(rewindBusy, "Claude 正在工作或在等你回答，等它停下来再回退。")
	}
	ctx, cancel := context.WithTimeout(ctx, rewindTotalLimit)
	defer cancel()
	r := &rewindRun{n: n, pane: pane}

	// Nothing is pressed while a dialog has the keyboard or the box holds a draft.
	ansi, err := r.readInput(ctx)
	if err != nil {
		return nil, err
	}
	switch code, in := screen.CheckClaude(ansi); {
	case code == screen.DialogOpen:
		return nil, refusal(code, "电脑上的终端正开着一个对话框，没有回退：先回答或关掉它。", nil)
	case code == screen.InputDraft:
		return nil, refusal(code, "电脑上的输入框里有没发出去的内容，没有回退：先发出或清掉它。", map[string]any{"draft": in.Text})
	case !in.Box:
		return nil, rewindError(rewindFailed, "没在终端上看到 Claude 的输入框，没有回退。")
	}

	// Open the panel: /rewind typed into the box (checked), then enter.
	if err := n.sendText(ctx, pane, "/rewind"); err != nil {
		return nil, err
	}
	if err := r.expectTyped(ctx); err != nil {
		return nil, err
	}
	if err := n.sendKeys(ctx, pane, "enter"); err != nil {
		return nil, err
	}
	s, found, err := r.until(ctx, rewindOpenWait, func(s string) bool { return screen.ParseRewind(s) != nil })
	if err != nil {
		return nil, err
	}
	if !found {
		r.clearBox(ctx) // the box may still hold /rewind
		return nil, rewindError(rewindFailed, "没有打开 Claude 的回退界面，什么都没改。")
	}
	list := panel(s, screen.RewindList)
	if list == nil {
		if p := screen.ParseRewind(s); p != nil && p.Kind == screen.RewindNothing {
			return nil, r.abort(ctx, rewindError(rewindNothing, "Claude 里没有可以回退到的消息。"))
		}
		return nil, r.abort(ctx, rewindError(rewindFailed, "回退界面和预期的不一样，什么都没改。"))
	}

	// Walk up from (current) to the prompt: every step is one Up, and the row it lands on is read. Where the prompt
	// sits is counted as Claude's list counts (asListed).
	if sel := list.Selected(); sel == nil || !sel.Current {
		return nil, r.abort(ctx, rewindError(rewindFailed, "回退列表的光标不在最后，什么都没改。"))
	}
	walk := n.asListed(id, o)
	steps, seen := 0, 0
	for seen < walk.Occurrence {
		if steps >= rewindMaxSteps {
			return nil, r.abort(ctx, rewindError(rewindMismatch, "回退列表太长，没有找到这条消息。"))
		}
		next, changed, err := r.press(ctx, s, "up")
		if err != nil {
			return nil, r.abort(ctx, err)
		}
		list = panel(next, screen.RewindList)
		if !changed || list == nil || list.Selected() == nil || list.Selected().Current {
			return nil, r.abort(ctx, rewindError(rewindMismatch, "Claude 的回退列表里没有找到这条消息（可能已经被回退过）。什么都没改。"))
		}
		s = next
		steps++
		if screen.RewindShows(list.Selected().Text, o.Prompt) {
			seen++
		}
	}
	if walk.PromptsAfter != nil && steps != *walk.PromptsAfter+1 {
		return nil, r.abort(ctx, rewindError(rewindMismatch, "回退列表里这条消息的位置和对话里看到的不一致，什么都没改。可以到电脑上回退。"))
	}

	// The confirmation screen quotes the whole message: it has to be the one asked for.
	if err := n.sendKeys(ctx, pane, "enter"); err != nil {
		return nil, r.abort(ctx, err)
	}
	next, found, err := r.until(ctx, rewindOpenWait, func(s string) bool { return panel(s, screen.RewindOptions) != nil })
	if err != nil {
		return nil, r.abort(ctx, err)
	}
	opts := panel(next, screen.RewindOptions)
	if !found || opts == nil {
		return nil, r.abort(ctx, rewindError(rewindFailed, "选中这条消息后没出现回退选项，什么都没改。"))
	}
	if !screen.RewindShows(opts.Message, o.Prompt) {
		return nil, r.abort(ctx, rewindError(rewindMismatch, "确认界面上的消息和要回退到的不是同一条，什么都没改。"))
	}
	s = next

	// What to restore: by its label, not its number (without code changes there are fewer choices). Code and
	// conversation for a prompt with no code changes after it comes down to the conversation alone.
	want, codeRestored := screen.RewindChoiceConversation, false
	if o.Restore == RestoreCodeAndConv && opts.Choice(screen.RewindChoiceBoth) != nil {
		want, codeRestored = screen.RewindChoiceBoth, true
	}
	target := opts.Choice(want)
	for moves := 0; ; moves++ {
		cur := opts.SelectedChoice()
		if target == nil || cur == nil || moves > 6 {
			return nil, r.abort(ctx, rewindError(rewindFailed, "回退选项里没有「%s」，什么都没改。", want))
		}
		if cur.Number == target.Number {
			break
		}
		key := "down"
		if cur.Number > target.Number {
			key = "up"
		}
		next, _, err := r.press(ctx, s, key)
		if err != nil {
			return nil, r.abort(ctx, err)
		}
		opts = panel(next, screen.RewindOptions)
		if opts == nil || !screen.RewindShows(opts.Message, o.Prompt) {
			return nil, r.abort(ctx, rewindError(rewindFailed, "选择回退方式时界面变了，什么都没改。"))
		}
		target = opts.Choice(want)
		s = next
	}
	if sel := opts.SelectedChoice(); sel == nil || sel.Label != want {
		return nil, r.abort(ctx, rewindError(rewindFailed, "光标没停在「%s」上，什么都没改。", want))
	}

	// Confirm. From here the rewind may have happened whatever goes wrong, so nothing is pressed to undo it.
	if err := n.sendKeys(ctx, pane, "enter"); err != nil {
		return nil, err
	}
	if _, closed, err := r.until(ctx, rewindDoneWait, func(s string) bool {
		return screen.ParseRewind(s) == nil && !screen.Choosing(s)
	}); err != nil {
		return nil, err
	} else if !closed {
		return nil, rewindError(rewindFailed, "确认之后回退界面还开着：到电脑上看看终端。")
	}
	res := map[string]any{"prompt": o.Prompt, "restore": RestoreConversation, "code_restored": codeRestored}
	if codeRestored {
		res["restore"] = RestoreCodeAndConv
	}
	res["input_cleared"] = r.takePromptOutOfBox(ctx, o.Prompt)
	n.forgetRewound(id, o)
	return res, nil
}

// asListed is where the app's prompt sits in Claude's /rewind list (#5397): the app counts every message of the user's
// it shows, and Claude leaves out a local slash command such as /rename (transcript.File.AsListed). The app's numbers
// stand when the transcript cannot say (not read, or the prompt not where the app saw it): the list on the screen is
// checked against them all the same.
func (n *Node) asListed(id string, o rewindOpts) rewindOpts {
	if o.PromptsAfter == nil {
		return o
	}
	f, err := n.file(id)
	if err != nil {
		return o
	}
	n.refresh(id, f)
	if occ, after, ok := f.AsListed(o.Prompt, o.Occurrence, *o.PromptsAfter); ok {
		o.Occurrence, o.PromptsAfter = occ, &after
	}
	return o
}

// forgetRewound leaves the rewound turns out of the session's transcript from now on. Claude writes nothing to its file
// for a rewind until the next message, and keeps the turns in it even then (transcript/branch.go): the file is told
// here, and the clients watching the session get a batch with a new history token, which makes them read it again.
func (n *Node) forgetRewound(id string, o rewindOpts) {
	f, err := n.file(id)
	if err != nil {
		return
	}
	n.refresh(id, f) // the lines written up to now are known to the tree, and pushed first
	mu := n.pushLock(id)
	mu.Lock()
	defer mu.Unlock()
	if span, ok := f.Rewound(o.Prompt, o.Occurrence); ok {
		n.applyRewound(id, f, span)
	}
}

// applyRewound passes on that the file left a rewound turn out (span: where it stands now, with its new token). The
// caller holds the session's push lock.
func (n *Node) applyRewound(id string, f *transcript.File, span transcript.Span) {
	n.mu.Lock()
	_, watched := n.watches[id]
	// The session list's quick read is told too, and reads the file again at once: its last words, and an API error
	// in them, may be the rewound turn's.
	path := f.Where()
	m := n.metas[path]
	m.rewound, m.mtime, m.readAt = f.RewoundLines(), time.Time{}, time.Time{}
	n.metas[path] = m
	n.mu.Unlock()
	if watched {
		n.pushItems(id, nil, span)
	}
}

// expectTyped checks that the box holds just /rewind (and Claude's command menu), clearing it and stopping when
// it holds anything else.
func (r *rewindRun) expectTyped(ctx context.Context) error {
	end := time.Now().Add(rewindStepWait)
	var in screen.Input
	for {
		s, err := r.readInput(ctx)
		if err != nil {
			return err
		}
		in = screen.ClaudeInput(s)
		if strings.TrimSpace(in.Text) == "/rewind" {
			return nil
		}
		if time.Now().After(end) {
			break
		}
		time.Sleep(rewindTick)
	}
	if in.Draft && screen.RewindDraftIs(in.Text, "/rewind") {
		r.clearBox(ctx)
	}
	return rewindError(rewindFailed, "输入框里不是 /rewind，没有回退。")
}

// clearBox empties the box when it holds just what this run typed (/rewind).
func (r *rewindRun) clearBox(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rewindCleanWait)
	defer cancel()
	s, err := r.readInput(ctx)
	if err != nil {
		return
	}
	if in := screen.ClaudeInput(s); in.Draft && strings.TrimSpace(in.Text) == "/rewind" {
		_ = r.n.sendKeys(ctx, r.pane, screen.ClearKeys...)
	}
}

// takePromptOutOfBox: Claude puts the rewound prompt back into its input box; the app puts it into its own, so the
// box is emptied (the keys are pressed once and only on a box that holds the prompt, as the box was empty when the rewind
// began). The panel can close before that restore arrives, so an empty box is final only after the clear was pressed,
// or after staying empty for one key-effect grace. It reports whether the box is empty in the end.
func (r *rewindRun) takePromptOutOfBox(ctx context.Context, prompt string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rewindDraftWait+rewindCleanWait)
	defer cancel()
	end := time.Now().Add(rewindDraftWait)
	pressed := false
	var emptySince time.Time
	for {
		s, err := r.readInput(ctx)
		if err != nil {
			return false
		}
		now := time.Now()
		switch in := screen.ClaudeInput(s); {
		case !in.Box:
			emptySince = time.Time{}
		case !in.Draft:
			if pressed {
				return true
			}
			if emptySince.IsZero() {
				emptySince = now
			}
			if now.Sub(emptySince) >= rewindStepWait {
				return true // Claude restored nothing during the grace
			}
		case !pressed && screen.RewindDraftIs(in.Text, prompt):
			emptySince = time.Time{}
			// A one-row box is emptied the way a send clears a draft (no hint on screen); a taller one only by Ctrl+C.
			keys := []string{"ctrl+c"}
			if in.Clearable != "" {
				keys = screen.ClearKeys
			}
			if err := r.n.sendKeys(ctx, r.pane, keys...); err != nil {
				return false
			}
			pressed = true
		case !pressed:
			return false // something else is in the box: not ours to clear
		}
		if now.After(end) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(rewindTick):
		}
	}
}
