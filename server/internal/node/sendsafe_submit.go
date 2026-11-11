package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
	"github.com/nwverse/persona/server/internal/transcript"
)

// pastePlaceholder is Claude's placeholder for a long paste, after the `[Image #n]` chips of the pictures the same
// paste attached.
var pastePlaceholder = lazyre.New(`^\s*(?:\[Image #\d+\]\s*)*\[Pasted text #\d+`)

// How long a prompt may sit in Claude's input box before ensureSubmitted presses Enter for it.
var submitGrace = 2 * time.Second

// graceLook is how often the transcript is looked at during submitGrace: a prompt it already has ends the grace (#1707).
var graceLook = 100 * time.Millisecond

// submitWindow is how long after typing ensureSubmitted waits for the transcript to take a prompt, looking at the
// input box every submitPoll (#6428). Zero: one look, nothing confirmed (the tests whose fakes never write the
// transcript, TestMain).
var (
	submitWindow = 15 * time.Second
	submitPoll   = time.Second
)

// ensureSubmitted presses Enter once when what was just prompted is in Claude's input box after submitGrace (cut short
// once the transcript has the prompt: waitGrace).
// Seen with images (2026-10-31 and 11-01, Claude Code 2.1.206, a photo from the phone): the text and its
// `[Image #n]` chip sat in the box, the agent `done`, until Enter was pressed on the computer; 42 tries in an
// isolated herdr did not repeat it, so the cause is unknown. Only the box after the grace counts: right after the
// paste it can show something else for a moment (the first version gave up on that and missed one). A box that
// shows anything else is left alone.
//
// One look after the grace missed it too (#6428, 2026-11-07, Claude Code 2.1.310, two screenshots of 1-2 MB from the
// web): the send answered 200, and the text with its chips sat in the box for the next hour. So until the transcript
// shows the prompt went in (watch: a line written after typing began that is this prompt), the box is looked at again
// every submitPoll, up to submitWindow after typing, and Enter is pressed once when it shows the prompt. A prompt still
// there at the end, Enter pressed, is an error with delivered "unknown": it is on the computer, not in the
// conversation. One look, as before, without a transcript to watch (no file, a slash command or `!` line, which the
// transcript never takes as a prompt) or a screen to look at (botmux, a read that failed); a window that ends with the
// box showing anything else is logged and counts as sent, as before. It returns the screen it read last ("" when
// none), for afterPrompt.
func (n *Node) ensureSubmitted(ctx context.Context, pane, text string, typedAt time.Time, watch *promptWatch) (string, error) {
	if !waitGrace(ctx, watch) {
		return "", nil
	}
	end := typedAt.Add(submitWindow)
	var last string
	entered := false
	for look := 1; ; look++ {
		s, err := n.readPaneANSI(ctx, pane, "visible", 60)
		if errors.Is(err, herdr.ErrTruncated) {
			// A pane herdr cannot send whole even without colors: its bottom rows, which hold the box (#6308).
			if b, _, ok := n.bottomRead(ctx, pane); ok {
				s, err = b, nil
			}
		}
		if err != nil {
			if look == 1 {
				return "", nil // no screen: as before, nothing says it was lost
			}
			if n.logger != nil {
				n.logger.Printf("cannot read %s %s after a prompt: %v", pane, time.Since(typedAt).Round(100*time.Millisecond), err)
			}
		} else {
			last = s
			if screen.Guide(screen.Plain(s)) != "" {
				return s, nil // it took the prompt, or came up after it: afterPrompt
			}
		}
		if submitWindow > 0 && watch.took() {
			return last, nil
		}
		confirm := submitWindow > 0 && watch.usable() // not a transcript that shrank: it tells nothing more (took)
		in := screen.ClaudeInput(s)
		stuck := err == nil && in.Box && in.Draft && pendingPrompt(in.Text, text)
		switch {
		case stuck && !entered:
			if n.logger != nil {
				n.logger.Printf("prompt still in the input box of %s %s after typing, pressing enter", pane, time.Since(typedAt).Round(100*time.Millisecond))
			}
			_ = n.sendKeys(ctx, pane, "enter")
			entered = true
			end = later(end, time.Now().Add(submitGrace)) // the Enter gets its time too
		case stuck && !time.Now().Before(end):
			if n.logger != nil {
				n.logger.Printf("prompt still in the input box of %s after Enter, %s after typing", pane, time.Since(typedAt).Round(time.Second))
			}
			return last, uncertain(errors.New(stuckPromptMessage))
		}
		if !confirm {
			return last, nil
		}
		if !time.Now().Before(end) {
			if n.logger != nil {
				n.logger.Printf("the transcript has not shown the prompt to %s %s after typing", pane, time.Since(typedAt).Round(time.Second))
			}
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, nil
		case <-time.After(submitPoll):
		}
	}
}

// waitGrace waits out submitGrace, or less once watch shows the transcript took the prompt (#1707): Claude usually
// writes it well within a second, and the rest of the grace only guards prompts the transcript never gets (one stuck in
// the box, one a guide took), which still wait all of it. The one screen read after it still looks for a guide. No
// watch, or no confirming (submitWindow 0): the whole grace, as before. False when ctx ended.
func waitGrace(ctx context.Context, watch *promptWatch) bool {
	end := time.Now().Add(submitGrace)
	look := submitGrace
	if submitWindow > 0 && watch.usable() {
		look = graceLook
	}
	for {
		left := time.Until(end)
		if left <= 0 || look < submitGrace && watch.took() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(min(left, look)):
		}
	}
}

// later is the later of a and b.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// localInput: a line Claude Code runs itself instead of sending it to the model (a slash command, a `!` shell line),
// which the transcript never takes as a prompt.
func localInput(text string) bool {
	t := strings.TrimLeft(text, " \t")
	return strings.HasPrefix(t, "/") || strings.HasPrefix(t, "!")
}

// stuckPromptMessage: the prompt stayed in Claude's input box, Enter pressed (ensureSubmitted).
const stuckPromptMessage = "消息已经打进电脑上 Claude 的输入框，但按了回车还停在框里，没进对话：在电脑上按回车发出去，或者清掉它再重发。"

// A guide of Claude Code's own (screen.Guide) can open the moment a message is submitted: 2.1.318's "Teach auto mode
// about your environment?" took the typed text and its Enter, the transcript never got the message, and the send still
// answered 200 (#5504). The check before typing cannot see it (it was not there yet), so afterPrompt looks at the
// screen ensureSubmitted reads once the prompt has had submitGrace (no extra read): a guide at the bottom is declined
// with Esc (never Enter, which would start what it offers), and the message counts as delivered only once the
// transcript has it. Otherwise it is typed again into the emptied box, once; still not in, or no transcript to tell
// (typing again could run it twice), the send fails with delivered "unknown" instead of succeeding.

// guideTries is how many times a message a guide took is typed in all.
const guideTries = 2

// afterPrompt follows a prompt typed into Claude in pane at typedAt: ensureSubmitted, then the guide check above.
// watch reads the transcript from where it ended before the typing (nil: nothing to watch).
func (n *Node) afterPrompt(ctx context.Context, id, pane, text string, typedAt time.Time, watch *promptWatch) error {
	for try := 1; ; try++ {
		s, err := n.ensureSubmitted(ctx, pane, text, typedAt, watch)
		if err != nil {
			return err
		}
		plain := screen.Plain(s) // no screen: as before, nothing says it was lost
		guide := screen.Guide(plain)
		if guide == "" {
			return nil
		}
		head := screen.DialogHead(plain)
		if n.logger != nil {
			n.logger.Printf("a guide opened in %s as a message was submitted, declining it: %s", pane, head)
		}
		if err := n.declineGuide(ctx, pane); err != nil {
			return uncertain(fmt.Errorf(guideStuckMessage, clipDialog(head, 120)))
		}
		if watch.took() {
			return nil // it went in before the guide opened
		}
		if n.transcriptPath(id) == "" || try == guideTries {
			return uncertain(fmt.Errorf(guideLostMessage, clipDialog(head, 120)))
		}
		s, err = n.readPaneANSI(ctx, pane, "visible", 60)
		if err != nil {
			return uncertain(fmt.Errorf(guideLostMessage, clipDialog(head, 120)))
		}
		if code, _ := screen.CheckClaude(s); code != "" {
			return uncertain(fmt.Errorf(guideLostMessage, clipDialog(head, 120))) // the box is not empty: not typed again
		}
		if n.logger != nil {
			n.logger.Printf("the message did not reach the transcript of %s, typing it again", id)
		}
		watch = n.watchPrompt(id, text)
		typedAt = time.Now()
		if err := n.prompt(ctx, pane, text); err != nil {
			if r := deliveryRefusal(err); r != nil {
				return r
			}
			return uncertain(err)
		}
		n.typing.typed(pane, text)
	}
}

const (
	guideLostMessage  = "电脑上的 Claude Code 在收这条消息时弹出了它自己的引导对话框（「%s」），消息被它吞掉了，已经按 Esc 关掉，但没能确认消息进了对话：看看电脑上的终端，需要的话再发一次。"
	guideStuckMessage = "电脑上的 Claude Code 在收这条消息时弹出了它自己的引导对话框（「%s」），按 Esc 没有关掉，消息可能没进对话：在电脑上关掉它再发。"
)

// promptWatch reads what a session's transcript gets after a prompt is typed: from where the file ended just before
// the typing, so nothing written earlier counts (the previous message, a notification), and with no tail limit (a
// prompt with images is megabytes of base64 on one line: the tail a quick read takes never holds it whole, #6428).
type promptWatch struct {
	path string
	off  int64
	rest []byte // the start of a line not written whole yet
	want []rune // the prompt's words (echoWords)
	seen bool
	// after filters rows when startup must attach after the transcript file appears; older matching prompts are not delivery.
	after time.Time
	lost  bool // the file shrank: written anew, its lines are no longer what came after the typing
}

// usable: the watch can still tell (not nil, its file not written anew).
func (w *promptWatch) usable() bool { return w != nil && !w.lost }

// watchPrompt starts watching id's transcript for text, nil when there is none to watch or the text is local input.
func (n *Node) watchPrompt(id, text string) *promptWatch {
	if localInput(text) {
		return nil
	}
	p := n.transcriptPath(id)
	if p == "" {
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	return &promptWatch{path: p, off: st.Size(), want: echoWords(text)}
}

// took reports whether a line written since the watch began is the prompt: a message to the agent, or input queued
// while Claude works, whose words start as the prompt's do (a prompt of pictures only: a message of the user's with a
// picture, not one a tool's result carries). Once seen, it stays so; a file that shrank is not read again (lost).
func (w *promptWatch) took() bool {
	if w == nil || w.lost {
		return w != nil && w.seen
	}
	if w.seen {
		return true
	}
	f, err := os.Open(w.path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	if st.Size() < w.off { // written anew: what it holds now may be lines from before the typing
		w.lost = true
		return false
	}
	if st.Size() == w.off {
		return false
	}
	buf := make([]byte, st.Size()-w.off)
	got, _ := f.ReadAt(buf, w.off)
	w.off += int64(got)
	lines := bytes.Split(append(w.rest, buf[:got]...), []byte("\n"))
	w.rest = append([]byte(nil), lines[len(lines)-1]...)
	for _, line := range lines[:len(lines)-1] {
		if w.isPrompt(line) {
			w.seen = true
			return true
		}
	}
	return false
}

// promptWords is how many of the prompt's first words a line must have: enough to tell it from another message,
// few enough for the line's own form of a long one.
const promptWords = 24

func (w *promptWatch) isPrompt(line []byte) bool {
	var e struct {
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Operation string          `json:"operation"`
		Content   json.RawMessage `json:"content"`
	}
	if json.Unmarshal(line, &e) != nil {
		return false
	}
	if !w.after.IsZero() {
		var at time.Time
		if json.Unmarshal(e.Timestamp, &at) != nil || at.Before(w.after.Add(-time.Second)) {
			return false
		}
	}
	var words []rune
	image := false
	switch e.Type {
	case "queue-operation":
		var content string
		if e.Operation != "enqueue" || json.Unmarshal(e.Content, &content) != nil {
			return false
		}
		words, image = echoWords(content), imageChip.MatchString(content)
	case "user":
		var meta transcript.Meta
		for _, it := range transcript.ParseLine(0, line, &meta) {
			switch {
			case it.Role == "user" && it.Kind == proto.KindText && it.From == "":
				words = append(words, echoWords(it.Text)...)
			case it.Role == "user" && it.Kind == proto.KindImage && it.ToolUseID == "": // not a tool result's picture
				image = true
			}
		}
	default:
		return false
	}
	if len(w.want) == 0 {
		return image
	}
	k := min(len(w.want), promptWords)
	return len(words) >= k && string(words[:k]) == string(w.want[:k])
}

// declineGuide presses Esc once on the guide at the bottom of pane and waits up to noticeSettle for it to go; an
// error when it is still there (Esc is not pressed again) or the screen cannot be read.
func (n *Node) declineGuide(ctx context.Context, pane string) error {
	if err := n.sendKeys(ctx, pane, screen.GuideKeys...); err != nil {
		return err
	}
	n.forgetDialog(pane)
	for end := time.Now().Add(noticeSettle); ; {
		time.Sleep(noticeSettle / 10)
		s, err := n.dialogScreen(ctx, pane)
		if err != nil {
			return err
		}
		if screen.Guide(s) == "" {
			return nil
		}
		if time.Now().After(end) {
			return errors.New("the guide is still open")
		}
	}
}

var (
	boxChips     = lazyre.New(`\[(Image|Pasted text) #\d+[^\]]*\]`)
	imageChip    = lazyre.New(`\[Image #\d+\]`)
	attachedNote = lazyre.New(`(?s)<attached_images>.*?</attached_images>`)
)

// longPaste: text Claude may show in its box as a `[Pasted text #n …]` placeholder (several lines, or long), so a
// placeholder there after it was typed is likely it; for a short text the placeholder is someone else's paste.
// Claude takes the pictures' bare path lines out of the paste and keeps their `path:` lines (keepImagePaths): words
// with such a block are always more than the two line breaks Claude Code (2.1.316) shows in its box.
func longPaste(text string) bool {
	t := strings.TrimSpace(attachedNote.ReplaceAllString(text, ""))
	if t != "" && strings.Contains(attachedNote.FindString(text), "\n"+imagePathLine) {
		return true
	}
	return strings.Count(t, "\n") >= 2 || len([]rune(t)) >= 800
}

// pendingPrompt matches our just-pasted input, including text hidden behind Claude's paste placeholder.
// Callers must hold the input turn and stop checking once delivery is confirmed; a placeholder alone is not
// ownership of an arbitrary draft. A short prompt cannot own a long-paste placeholder.
func pendingPrompt(box, prompted string) bool {
	return stillPrompted(box, prompted) || longPaste(prompted) && pastePlaceholder.MatchString(box) &&
		strings.TrimSpace(boxChips.ReplaceAllString(box, "")) == ""
}

// stillPrompted: the input box shows the start of what was prompted. Claude turns an image path into an `[Image #n]`
// chip and a long paste into `[Pasted text #n …]`, so chips are dropped and only the first words (of the first line
// with any, the note of attached images left out) are compared.
func stillPrompted(box, prompted string) bool {
	squash := func(s string) []rune {
		return []rune(strings.Join(strings.Fields(boxChips.ReplaceAllString(s, "")), ""))
	}
	first := ""
	for _, l := range strings.Split(attachedNote.ReplaceAllString(prompted, ""), "\n") {
		if strings.TrimSpace(l) != "" {
			first = l
			break
		}
	}
	b, p := squash(box), squash(first)
	if len(p) > 12 {
		p = p[:12]
	}
	return len(p) > 0 && len(b) >= len(p) && string(b[:len(p)]) == string(p)
}
