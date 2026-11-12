package node

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/screen"
)

// A multi-select question of Claude Code's AskUserQuestion read off the screen (screen.Dialog.Multi, 2.1.316):
// check boxes that a digit or enter toggles without moving the cursor — from an option's row: on the Submit row a
// digit does nothing, and on the free-text field ("Type something", which typing checks and clearing unchecks) it is
// typed as text —, a Submit / Next row under them, then
// a review tab ("1. Submit answers") once every question is answered. Keys sent together get lost, so it is answered
// one key at a time, reading the screen again after each: boxes ticked at the computer meanwhile and a cursor
// anywhere are taken as they are. Measured in docs/integrations/claude-integration.md "Screen cards for multiselect questions".

// dialogStep is how long the TUI gets to take a key before the screen is read again.
const dialogStep = 350 * time.Millisecond

// multiMove is the next thing to do to bring a multi-select question to the answer: keys to press one by one, text
// to type into the free-text field (the cursor is on it), or submit (the cursor is on the Submit / Next row: enter).
type multiMove struct {
	keys   []string
	text   string
	submit bool
}

// nextMultiMove works out the next move from the dialog as it is now: want are the option keys to have checked, text
// what the free-text field should hold (empty: unchecked). An error when the dialog is not one it can answer.
func nextMultiMove(d *screen.Dialog, want map[string]bool, text string) (multiMove, error) {
	var field *screen.Option
	rows := []string{} // the cursor's rows top down: the options, the field, the Submit row
	for i := range d.Options {
		o := &d.Options[i]
		switch o.Kind {
		case screen.KindTypeSomething:
			field = o
		case screen.KindChat:
			continue
		}
		rows = append(rows, o.Key)
	}
	rows = append(rows, screen.CursorSubmit)
	at := slices.Index(rows, d.Cursor)
	if d.Cursor == "" {
		return multiMove{}, fmt.Errorf("没在终端上找到光标")
	}
	if at < 0 { // on "Chat about this", below the Submit row
		return multiMove{keys: []string{"up"}}, nil
	}
	onField := field != nil && d.Cursor == field.Key
	// A digit toggles its option only from an option's row: the field would take it as text, the Submit row ignores it.
	offOptions := onField || d.Cursor == screen.CursorSubmit
	for _, o := range d.Options {
		if o.Kind == "" && o.Checked != want[o.Key] {
			if offOptions {
				return multiMove{keys: []string{"up"}}, nil
			}
			return multiMove{keys: []string{o.Key}}, nil
		}
	}
	if field != nil {
		switch {
		case field.Text == text && field.Checked == (text != ""):
		case field.Text == "" && text == "": // ticked with nothing in it: untick
			if offOptions {
				return multiMove{keys: []string{"up"}}, nil
			}
			return multiMove{keys: []string{field.Key}}, nil
		case !onField:
			return multiMove{keys: []string{toward(at, slices.Index(rows, field.Key))}}, nil
		case field.Text != "":
			return multiMove{keys: []string{"ctrl+e", "ctrl+u"}}, nil // clearing unchecks it
		default:
			return multiMove{text: text}, nil // typing checks it
		}
	} else if text != "" {
		return multiMove{}, fmt.Errorf("这道题没有可以自己填写的一项")
	}
	if d.Cursor != screen.CursorSubmit {
		return multiMove{keys: []string{toward(at, len(rows)-1)}}, nil
	}
	return multiMove{submit: true}, nil
}

func toward(from, to int) string {
	if from < to {
		return "down"
	}
	return "up"
}

// dialogMulti answers the multi-select question of the dialog a card was made from (same revision): the options in
// want checked and the rest not, the free-text field holding text, then Submit / Next; a review tab that follows
// with every question answered is submitted too. A changed or gone dialog refuses with prompt_changed before any key.
func (n *Node) dialogMulti(ctx context.Context, id, rev string, want []string, text string) error {
	if err := n.switchingRefusal(id); err != nil { // a launcher switch, maybe on the last connection's Node
		return err
	}
	defer n.typing.lock(id)()
	pane, err := n.paneFor(ctx, id)
	if err != nil {
		return err
	}
	var cut bool // the last read saw a dialog it could not read all of (errDialogCut)
	read := func() *screen.Dialog {
		s, err := n.dialogScreen(ctx, pane)
		cut = errors.Is(err, errDialogCut)
		if err != nil {
			n.logf("session %s: reading pane %s for its dialog: %v", id, pane, err) // else it looks like no dialog
			return nil
		}
		return screen.ParseDialog(s)
	}
	text = strings.Join(strings.Fields(text), " ") // one line: a newline would be enter
	d := read()
	if cut {
		return refusal(notDelivered, dialogCutMessage, nil)
	}
	if !sameDialog(d, rev) || !d.Multi {
		return n.promptChanged(id, pane, rev, d)
	}
	if text != "" && !n.canTypeText(pane) {
		return refusal(cannotType, cannotTypeMessage, nil)
	}
	wanted := map[string]bool{}
	for _, k := range want {
		if o := d.Option(k); o == nil || o.Kind != "" {
			return fmt.Errorf("the dialog has no option %q to check", k)
		}
		wanted[k] = true
	}
	for step := 0; ; step++ {
		if step > 0 {
			time.Sleep(dialogStep)
			if d = read(); !sameDialog(d, rev) {
				return fmt.Errorf("作答到一半，终端上的对话框变了（电脑上有人在操作？），已经按下的键没有撤回：在电脑上看看")
			}
		}
		if step > 4*len(d.Options)+8 { // each option, the field, the moves: never this many
			return fmt.Errorf("没能在终端上勾好这些选项，停在了半路：在电脑上看看")
		}
		m, err := nextMultiMove(d, wanted, text)
		if err != nil {
			return err
		}
		switch {
		case m.submit:
			if err := n.sendKeys(ctx, pane, "enter"); err != nil {
				return err
			}
			return n.submitReview(ctx, pane)
		case m.text != "":
			err = n.sendText(ctx, pane, m.text)
		default:
			for i, k := range m.keys {
				if i > 0 {
					time.Sleep(dialogStep)
				}
				if err = n.sendKeys(ctx, pane, k); err != nil {
					break
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

// submitReview: after the last question of an AskUserQuestion with a tab bar is answered, Claude shows a review tab;
// with every question answered it is submitted ("1. Submit answers"), the answers having been given on the phone.
// One with a question left unanswered stays for its own card (it says which).
func (n *Node) submitReview(ctx context.Context, pane string) error {
	time.Sleep(dialogStep)
	s, err := n.dialogScreen(ctx, pane)
	if errors.Is(err, errDialogCut) { // a review page too long to read: not submitted unseen
		return errors.New("答案都选好了，但终端上的确认页太长，读不全，没有替你提交：在电脑上按「Submit answers」")
	}
	if err != nil {
		return nil
	}
	d := screen.ParseDialog(s)
	if d == nil || !d.Review || d.Incomplete || len(d.Options) == 0 {
		return nil
	}
	submit := d.Options[0]
	if submit.Label != "Submit answers" {
		return nil
	}
	if err := n.sendKeys(ctx, pane, submit.Key); err != nil {
		return err
	}
	time.Sleep(dialogStep)
	if s, err = n.dialogScreen(ctx, pane); err == nil {
		if d := screen.ParseDialog(s); d != nil && d.Review {
			return fmt.Errorf("按了「Submit answers」，确认页还在：在电脑上看看")
		}
	}
	return nil
}
