package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
)

// Typing into someone's terminal is the riskiest thing Persona does. Before a message goes into a running agent's
// pane the node reads the screen and refuses — nothing typed — when
//   - a dialog has the keyboard (permission prompt, picker, rewind): the text would pick an option (dialog_open);
//     A notice that Enter only closes (Claude Code's auto mode billing notice) is closed first when the send asks
//     for it (dismiss_notice); a refusal says what the dialog is (its first line) and whether it is such a notice;
//   - Claude's input box holds a draft typed at the computer, or bash mode: the message would be appended to it and
//     sent together, or run as a command (input_draft, with the box's text). The app may ask to clear that exact
//     draft and send (clear_draft).
// Sends carry the app's client_msg_id: a retry of one already delivered (the response was lost) is acknowledged
// without typing it again. See docs/protocol/protocol.md "Sending messages".

// dialogKeys checks that the session's screen still shows the dialog a card was made from (same revision) and
// returns the keys for option, worked out from the screen as it is now (an unnumbered list moves from where the cursor
// is): keys nil when no option is named (the caller's keys stand). A changed or gone dialog refuses with
// prompt_changed, nothing pressed.
func (n *Node) dialogKeys(ctx context.Context, id, rev, option string) (keys, pre []string, err error) {
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	text, err := n.dialogScreen(ctx, pane)
	if errors.Is(err, errDialogCut) {
		return nil, nil, refusal(notDelivered, dialogCutMessage, nil)
	}
	if err != nil {
		return nil, nil, err
	}
	d := screen.ParseDialog(text)
	if !sameDialog(d, rev) {
		return nil, nil, n.promptChanged(id, pane, rev, d)
	}
	if option == "" {
		return nil, nil, nil
	}
	o := d.Option(option)
	if o == nil {
		return nil, nil, fmt.Errorf("the dialog has no option %q", option)
	}
	if o.Kind == screen.KindTypeSomething {
		return []string{"enter"}, o.Keys, nil
	}
	return o.Keys, nil, nil
}

// sameDialog: d is the dialog whose revision a card carries. A hub from before #5538 made the card with the old
// formula (screen.Dialog.LegacyRev).
func sameDialog(d *screen.Dialog, rev string) bool {
	return d != nil && rev != "" && (d.Rev == rev || d.LegacyRev == rev)
}

// dialogRev is the revision of the dialog on a session.screen read, for the hub to make its card with: the same read
// dialogKeys checks against (plain, visible, screen.DialogLines rows), parsed by the same code. A hub and a node of
// different builds may parse a screen differently; with the node's own revision on the card they still agree.
// "" for any other read, or no dialog.
func dialogRev(text string, lines int, source, format string) string {
	if lines != screen.DialogLines || (source != "" && source != "visible") || format == "ansi" {
		return ""
	}
	if d := screen.ParseDialog(text); d != nil {
		return d.Rev
	}
	return ""
}

// promptChanged refuses an answer to the dialog with revision rev, d being what pane shows now (nil: none). The
// refusal and the log say both revisions and what the node read, so a mismatch can be seen (#5538). The log also
// names the agent's pane, the one session.screen reads for the card, in case the answer read another.
func (n *Node) promptChanged(id, pane, rev string, d *screen.Dialog) error {
	cardPane := "none"
	if a, ok := n.agent(id); ok {
		cardPane = a.PaneID
	}
	data := map[string]any{"rev": rev}
	if d == nil {
		n.logf("session %s: prompt_changed: the card's dialog %s, none in pane %s (the agent's pane: %s)", id, rev, pane,
			cardPane)
	} else {
		data["seen_rev"], data["seen_question"], data["seen_options"] = d.Rev, d.Question, len(d.Options)
		n.logf("session %s: prompt_changed: the card's dialog %s, %s in pane %s (the agent's pane: %s), %d options, question %q",
			id, rev, d.Rev, pane, cardPane, len(d.Options), d.Question)
	}
	return refusal(screen.PromptChanged, "终端上的对话框已经变了（可能在电脑上答过了），这次什么都没按：看看新的再答。", data)
}

// sendOpts are the optional fields of session.send.
type sendOpts struct {
	ClientMsgID string `json:"client_msg_id"`
	// ClearDraft is the draft the user saw in a refusal and chose to clear: cleared only if the box still shows it.
	ClearDraft string `json:"clear_draft"`
	// DismissNotice: a notice that Enter only closes («Enter to continue», no options) is closed before the text is
	// typed. Any other dialog is refused as before.
	DismissNotice bool `json:"dismiss_notice"`
	// Launcher (and its model) resumes a session that is not running when nothing on this machine says which
	// launcher it ran with: the hub sends "New Agent"'s (hub/send.go). A recorded or inferred launcher wins.
	Launcher         string `json:"launcher"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	ModelFingerprint string `json:"model_fingerprint"`
	// ApplySwitch: the user's own message from the chat of a session with a pending launcher switch, which carries the
	// switch out before it is typed (launcher_switch.go). Without it (any other sender) the message goes on the launcher
	// the agent runs on and the switch keeps waiting.
	ApplySwitch bool `json:"apply_switch"`
	// readBox: a screen that cannot be read refuses (unreadBoxMessage) instead of passing for an empty box. After a
	// command that may still sit in the box (ultracode.go), not knowing is not enough to type after it.
	readBox bool
}

// resumeLauncher is the launcher an offline session is resumed with to take a message, and the model to run it
// with: the one Persona started it with; else the one whose base URL and model its transcript shows (a session
// started outside Persona); else the hub's pick, with its model; plain claude only when there is none of them. The
// first two keep the model it ran with.
func (n *Node) resumeLauncher(st sessionState, id string, opts sendOpts) (string, modelExecution) {
	ran := modelExecution{Model: st.Model, Fingerprint: st.ModelFingerprint}
	if st.Launcher != "" {
		return st.Launcher, ran
	}
	if l := n.inferLauncher(id); l != "" {
		return l, ran
	}
	if opts.Launcher != "" {
		return opts.Launcher, modelExecution{Model: opts.Model, Effort: opts.Effort, UseEffort: opts.ModelFingerprint != "", Fingerprint: opts.ModelFingerprint}
	}
	return "claude", ran
}

// Delivery states a failed send reports: "none" when nothing reached the pane (safe to resend), "unknown" when the
// failure came after typing started.
const (
	deliveredNone    = "none"
	deliveredUnknown = "unknown"
)

func refusal(code, message string, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["delivered"] = deliveredNone
	b, _ := json.Marshal(data)
	return &rpc.Error{Code: code, Message: message, Data: b}
}

// checkPane refuses a send into a pane that is not ready for typed text, after clearing the draft the user agreed
// to clear. kind is the agent ("claude", "codex", a compatible CLI).
func (n *Node) checkPane(ctx context.Context, pane, kind, clearDraft string) error {
	return n.checkPaneFor(ctx, pane, kind, sendOpts{ClearDraft: clearDraft})
}

// noticeTries is how many notices in a row a send closes at most (one may follow another).
const noticeTries = 3

// checkPaneFor is checkPane with the send's options: the draft to clear and whether to close a notice.
func (n *Node) checkPaneFor(ctx context.Context, pane, kind string, opts sendOpts) error {
	clearDraft := opts.ClearDraft
	// Codex and compatible variants: their own screen (codexask.go) — a draft in the box, an approval or a question overlay refuses;
	// the transcript pager and 0.163's question pane are only shown, and step aside.
	if kind == "codex" || kind == traexAgent {
		if host.ParsePane(pane).Kind == host.Botmux {
			return nil // botmux takes the message as a turn of its own (queued behind a running one): no box to check
		}
		return n.codexReady(ctx, pane, false)
	}
	// Claude in a host that reads screens without colors (host.PlainScreen: a draft and the grey placeholder look the
	// same there), any other agent: only the numbered approvals and pickers.
	if kind != "claude" || n.plainScreen(pane) {
		text, err := n.readPane(ctx, pane, "visible", 60)
		if err != nil && opts.readBox {
			return refusal(notDelivered, unreadBoxMessage, nil)
		}
		if err == nil && opts.DismissNotice {
			if text, err = n.closeNotices(ctx, pane, text, false); err != nil {
				return err
			}
		}
		if err == nil && screen.Choosing(text) {
			return dialogRefusal(text)
		}
		return nil
	}
	unread := false  // the last read failed: it says nothing of the box
	cut := false     // … because herdr cut its every answer (herdr.ErrTruncated): it is there, the screen is not
	colored := false // the last read had colors: only they tell Claude's grey suggestion from a draft
	partial := false // the last read is the screen's bottom rows only (bottomRead): a dialog's head may be above them
	read := func() string {
		s, err := n.readPaneANSI(ctx, pane, "visible", 60) // a big one in two smaller reads (spliceRead, #6039)
		colored = err == nil
		if err != nil {
			s, err = n.readPane(ctx, pane, "visible", 60) // no colors: any text in the box counts as a draft
		}
		partial = false
		if errors.Is(err, herdr.ErrTruncated) {
			// Cut without colors too: the bottom rows, when they show the box or a dialog (bottomRead).
			if b, c, ok := n.bottomRead(ctx, pane); ok {
				s, err, colored, partial = b, nil, c, true
			}
		}
		unread, cut = err != nil, errors.Is(err, herdr.ErrTruncated)
		return s
	}
	shown := read()
	if unread && (opts.readBox || cut) {
		return refusal(notDelivered, unreadBoxMessage, nil)
	}
	// Only a whole screen tells a notice from a dialog whose head is above the bottom rows: Enter there would answer it.
	if opts.DismissNotice && !partial {
		var err error
		if shown, err = n.closeNotices(ctx, pane, shown, true); err != nil {
			return err
		}
	}
	code, in := screen.CheckClaude(shown)
	// What we typed a moment ago may still show while Claude takes it: wait for the box to empty rather than call our
	// own message a draft.
	for wait := time.Now().Add(2 * time.Second); code == screen.InputDraft && n.typing.ours(pane, in.Text) && time.Now().Before(wait); {
		time.Sleep(100 * time.Millisecond)
		shown = read()
		code, in = screen.CheckClaude(shown)
	}
	if code == screen.InputDraft && clearDraft != "" && in.Clearable == clearDraft {
		if err := n.sendKeys(ctx, pane, screen.ClearKeys...); err != nil {
			return refusal(code, draftMessage(in.Text), map[string]any{"draft": in.Text})
		}
		time.Sleep(150 * time.Millisecond)
		shown = read()
		code, in = screen.CheckClaude(shown)
	}
	if unread && (opts.readBox || cut) {
		return refusal(notDelivered, unreadBoxMessage, nil)
	}
	switch code {
	case screen.DialogOpen:
		if partial {
			return dialogRefusalOf(screen.Plain(shown), false)
		}
		return dialogRefusal(screen.Plain(shown))
	case screen.InputDraft:
		// What the box was drawn with, never its text: a style the reader takes for typed words shows here.
		n.logf("send to %s refused: text in the box, read %s: %s", pane, map[bool]string{true: "with colors", false: "without colors"}[colored], screen.BoxStyle(shown))
		data := map[string]any{"draft": in.Text}
		if in.Clearable != "" {
			data["clearable"] = in.Clearable
		}
		return refusal(code, draftMessage(in.Text), data)
	}
	return nil
}

// Refusals of a host that takes input on its own terms (herdr-core): it refused the text with nothing typed, or it
// cannot type text without submitting it.
const (
	notDelivered        = "not_delivered"
	notDeliveredMessage = "电脑上的终端这次没有收下这条消息，什么都没输入：稍后再发。"
	shellOnlyMessage    = "这个会话的 Agent 已经退出，终端回到了命令行，这条消息没有发出去（什么都没输入）：在电脑上重新打开它再发。"
	cannotType          = "cannot_type"
	cannotTypeMessage   = "电脑上的这个终端只能整条发送（粘贴后回车），不能只输入文字，这次什么都没按：在电脑上回答，或者直接发消息。"
	unreadBoxMessage    = "读不到电脑上终端的画面，不知道刚才输入的命令是不是还停在输入框里，这条消息没有发出去（什么都没输入）：稍后再发。"
)

// deliveryRefusal is the refusal for a host's error that typed nothing (host.ErrNotDelivered), nil for any other.
func deliveryRefusal(err error) error {
	if !errors.Is(err, host.ErrNotDelivered) {
		return nil
	}
	if errors.Is(err, host.ErrDialogOpen) {
		return refusal(screen.DialogOpen, dialogMessage, nil)
	}
	if errors.Is(err, errShellOnly) {
		return refusal(notRunning, shellOnlyMessage, nil)
	}
	return refusal(notDelivered, notDeliveredMessage, nil)
}

// typingRefusal refuses, before anything is pressed, keys that type text into a host that cannot type without
// submitting (host.TextTyper) — unless it is only text and enter, which goes as a prompt. Dialog answers with keys to
// press first (a free-text option) are refused here, not after the field was opened. Only a session the node already
// knows is checked: herdr's path makes no extra call.
func (n *Node) typingRefusal(id, text string, keys, pre []string) error {
	if text == "" {
		return nil
	}
	a, ok := n.agent(id)
	if !ok || n.canTypeText(a.PaneID) {
		return nil
	}
	if len(pre) == 0 && len(keys) == 1 && keys[0] == "enter" {
		return nil
	}
	return refusal(cannotType, cannotTypeMessage, nil)
}

const dialogMessage = "电脑上的终端正开着一个对话框（确认、选择列表等），这条消息没有发出去：先回答或关掉它再发。"

// dialogRefusal refuses a send into a pane whose screen (plain) shows a dialog, saying which: its first line, and
// notice when Enter only closes it (a send with dismiss_notice would close it and go through).
func dialogRefusal(plain string) error { return dialogRefusalOf(plain, true) }

// dialogRefusalOf is dialogRefusal for a screen read whole or only its bottom rows (bottomRead): those never make a
// notice, since the dialog's head and other options may be above them.
func dialogRefusalOf(plain string, whole bool) error {
	head, notice := screen.DialogHead(plain), whole && screen.NoticeOnly(plain)
	msg := dialogMessage
	if head != "" {
		msg = "电脑上的终端正开着一个对话框（「" + clipDialog(head, 120) + "」），这条消息没有发出去："
		if notice {
			msg += "它只是一条通知，按回车就关掉，可以让发送先关掉它再发。"
		} else {
			msg += "先回答或关掉它再发。"
		}
	}
	data := map[string]any{"notice": notice}
	if head != "" {
		data["dialog"] = head
	}
	return refusal(screen.DialogOpen, msg, data)
}

func clipDialog(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// closeNotices presses Enter on the notice shown at the bottom of pane (shown: its screen, with colors when ansi),
// and again on another notice that takes its place, up to noticeTries; it returns the screen after. Enter goes in
// only on a notice, and once per notice: after each press the screen is read until the notice is gone or another
// one shows (noticeSettle), never again on the same one — a second Enter on Claude's input box would send its
// suggestion. A notice still there then refuses the send. A failed key press refuses it too: nothing was typed.
func (n *Node) closeNotices(ctx context.Context, pane, shown string, ansi bool) (string, error) {
	read := func() (string, error) {
		if ansi {
			if s, err := n.readPaneANSI(ctx, pane, "visible", 60); err == nil {
				return s, nil
			}
		}
		return n.readPane(ctx, pane, "visible", 60)
	}
	for try := 0; try < noticeTries; try++ {
		plain := screen.Plain(shown)
		if !screen.NoticeOnly(plain) {
			return shown, nil
		}
		head := screen.DialogHead(plain)
		if n.logger != nil {
			n.logger.Printf("closing a notice in %s before sending (dismiss_notice): %s", pane, head)
		}
		if err := n.sendKeys(ctx, pane, "enter"); err != nil {
			return "", dialogRefusal(plain)
		}
		n.forgetDialog(pane)
		gone := false
		for end := time.Now().Add(noticeSettle); ; {
			time.Sleep(noticeSettle / 10)
			s, err := read()
			if err != nil {
				return "", err
			}
			shown = s
			p := screen.Plain(s)
			if !screen.NoticeOnly(p) || screen.DialogHead(p) != head {
				gone = true
				break
			}
			if time.Now().After(end) {
				break
			}
		}
		if !gone {
			return "", dialogRefusal(screen.Plain(shown)) // Enter did not close it: not pressed again
		}
	}
	return shown, nil
}

// noticeSettle is how long Claude has to take a notice off the screen after Enter. Tests shorten it.
var noticeSettle = time.Second

func draftMessage(draft string) string {
	if r := []rune(draft); len(r) > 200 {
		draft = string(r[:200]) + "…"
	}
	if draft == "" {
		return "电脑上的输入框里有没发出去的内容，这条消息没有发出去，免得拼在一起。"
	}
	return "电脑上的输入框里有没发出去的内容：「" + draft + "」。这条消息没有发出去，免得拼在一起。"
}

// sentLog remembers the client_msg_id of recent sends (10 minutes) so a retry is not typed twice.
type sentLog struct {
	mu   sync.Mutex
	sent map[string]*sendRecord
}

type sendRecord struct {
	at   time.Time
	done chan struct{}
	err  error
}

const sentTTL = 10 * time.Minute

// once runs send for a client_msg_id at most once: a retry waits for a send in flight and returns its outcome, or
// succeeds at once after a delivered one. Refused sends (nothing typed) are forgotten, so their retry runs again.
func (l *sentLog) once(id string, send func() error) error {
	if id == "" {
		return send()
	}
	l.mu.Lock()
	if l.sent == nil {
		l.sent = map[string]*sendRecord{}
	}
	for k, r := range l.sent {
		if time.Since(r.at) > sentTTL {
			delete(l.sent, k)
		}
	}
	if r := l.sent[id]; r != nil {
		l.mu.Unlock()
		<-r.done
		return r.err
	}
	r := &sendRecord{at: time.Now(), done: make(chan struct{})}
	l.sent[id] = r
	l.mu.Unlock()
	r.err = send()
	if r.err != nil && !maybeDelivered(r.err) {
		l.mu.Lock()
		delete(l.sent, id)
		l.mu.Unlock()
	}
	close(r.done)
	return r.err
}

// uncertain marks a failure while typing into the pane: the message may have arrived. A refusal (nothing delivered)
// stays one.
func uncertain(err error) error {
	if err == nil {
		return nil
	}
	var re *rpc.Error
	if errors.As(err, &re) && re.Code != "" && deliveredOf(re) == deliveredNone {
		return err
	}
	b, _ := json.Marshal(map[string]any{"delivered": deliveredUnknown})
	return &rpc.Error{Message: err.Error(), Data: b}
}

func maybeDelivered(err error) bool {
	e, ok := err.(*rpc.Error)
	return ok && deliveredOf(e) == deliveredUnknown
}

// deliveredOf is the delivered an RPC error's data says, "" for none.
func deliveredOf(e *rpc.Error) string {
	var d struct {
		Delivered string `json:"delivered"`
	}
	_ = json.Unmarshal(e.Data, &d)
	return d.Delivered
}

// Herdr resumes offline sessions in a new pane, and so does Orca for a Claude session (#5548) once nothing has it
// open: its last pane runs no agent (suspended, in the background) and start's openElsewhereRefusal finds no live
// claude on it.
const (
	notRunning    = "not_running"
	notInFront    = "agent_not_in_front"
	inFrontReason = "Claude 不在前台（可能被挂起），没有发送：在电脑上把它切回前台再发。"
)

// notRunningRefusal refuses, with nothing typed, a send to a session that is not live when the machine's engine
// cannot start sessions (no host.Launcher) or is Orca, instead of trying to resume it there: when the pane it was last
// seen in is still there, its agent is not in front (suspended, in the background); otherwise it is gone. nil when the
// engine can resume it: herdr, and Orca for a Claude session (its own <id>.jsonl; Codex, TRAEX and pi sessions are
// not resumed in Orca).
// orcaContinues: Orca can continue session id once its agent is gone — a Claude session (its own <id>.jsonl, not a
// Codex or TRAEX rollout); Codex, TRAEX and pi sessions are not resumed in Orca (#5548). Send (notRunningRefusal) and
// retire share it, so the task closer never closes an agent a message cannot bring back.
func (n *Node) orcaContinues(id string) bool { return n.claudeTranscript(id, true) != "" }

func (n *Node) notRunningRefusal(ctx context.Context, id string) error {
	h := n.hosts.Get(n.startKind(ctx))
	if h == nil {
		return nil
	}
	if _, ok := h.(host.Launcher); ok && h.Kind() != host.Orca {
		return nil
	}
	n.mu.Lock()
	pane := n.lastPanes[id]
	n.mu.Unlock()
	if pane != "" {
		if ph, ref := n.hosts.Pane(pane); ph != nil {
			if _, err := ph.Agent(ctx, ref); err == nil {
				log.Printf("send %s: not on the live list; its last pane %s is still open: refused as not in front", id, pane)
				return refusal(notInFront, inFrontReason, nil)
			}
		}
	}
	if h.Kind() == host.Orca && n.orcaContinues(id) {
		log.Printf("send %s: not on the live list (last pane %q): resuming it in Orca", id, pane)
		return nil
	}
	log.Printf("send %s: not on the live list (last pane %q): refused as not running", id, pane)
	name := proto.EngineNames[h.Kind()]
	return refusal(notRunning, fmt.Sprintf("这个会话现在没有在运行（可能被挂起或已退出），%s 里的会话要在电脑上重新打开，这次没有发送。", name), nil)
}
