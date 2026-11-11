package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/i18n"
)

const (
	// briefAnswerRunes is how much of the model's answer (its summary, the calendar and the chats) the card quotes at the top.
	briefAnswerRunes = 1500
	// briefTailBytes is kept free for the "and N more" lines and the note under the sections.
	briefTailBytes = 1500
	// briefSpareEls is the elements kept free for the same: an "and N more" line for each of the five sections, the note under
	// them and the button that opens the conversation.
	briefSpareEls = 7
)

// briefBlock is one row of a section: its Markdown, and the button under it (nil: none).
type briefBlock struct {
	md    string
	plain string
	btn   feishubot.Button
	todo  string // the task a button accepts
}

// briefCard is the card a routine with facts sends instead of its answer: the model's answer first, then the sections,
// each row as on the standing board. It stays inside one card's byte and element budget: what does not fit is counted
// ("and N more"). shown are the tasks to verify whose rows (with their Accept buttons) are on the card.
func (s *Service) briefCard(f *briefFacts, label, answer string) (card *feishubot.Card, fallback string, shown []string) {
	l := f.l
	color := feishubot.Blue
	if len(f.Waiting) > 0 {
		color = feishubot.Orange
	}
	c := feishubot.NewCard(label, color, f.countsText())
	plain := []string{label}
	if answer = strings.TrimSpace(answer); answer != "" {
		// Charts become text first, as the bot's answers do (feishu_answer.go): clipMarkdown drops a persona-card block
		// it cannot draw down to its title, so a chart on a long answer would lose its numbers.
		c.Markdown(feishubot.CardMarkdown(clipMarkdown(feishubot.PersonaCards(answer), briefAnswerRunes)))
		plain = append(plain, clip(answer, briefAnswerRunes))
		if len([]rune(answer)) > briefAnswerRunes {
			c.Note(i18n.T(l, "assistant-briefing.answer_clipped"))
		}
	}
	room := briefBudget{bytes: reportCardMaxBytes - briefTailBytes - len(c.JSON()), els: reportCardMaxElements - briefSpareEls - len(c.Body.Elements)}

	// The north star first: the report opens with it (north_star.go).
	var star []briefBlock
	for _, line := range f.NorthStarLines {
		star = append(star, briefBlock{md: feishubot.EscapeMarkdown(line), plain: line})
	}
	if len(star) > 0 {
		room.section(c, &plain, l, i18n.T(l, "assistant-north_star.heading"), star, len(star))
	}

	var confirmations []briefBlock
	confirmations = append(confirmations, briefBlock{md: feishubot.EscapeMarkdown(f.Confirmations.summary(l)), plain: f.Confirmations.summary(l)})
	for _, line := range f.ConfirmationLines {
		confirmations = append(confirmations, briefBlock{md: feishubot.EscapeMarkdown(line), plain: line})
	}
	room.section(c, &plain, l, i18n.T(l, "assistant-confirmations.heading"), confirmations, len(confirmations))

	// Waiting for you: the board's rows that hold an Agent up.
	var waiting []briefBlock
	for i, r := range f.Waiting {
		waiting = append(waiting, briefBlock{md: boardRowMarkdown(l, i+1, r), plain: boardRowPlain(l, i+1, r)})
	}
	if f.WaitingUnknown { // the cards waiting were not read yet: it does not say that none waits
		line := feishubot.EscapeMarkdown(i18n.T(l, "assistant-briefing.waiting_unknown"))
		c.Markdown(line)
		plain = append(plain, line)
	} else {
		room.section(c, &plain, l, i18n.T(l, "assistant-briefing.head_waiting", i18n.Args{"n": len(f.Waiting)}), waiting, len(f.Waiting))
	}

	// The last twelve hours: what did not go through first, so a long list of what finished does not push it out.
	var past []briefBlock
	for _, x := range f.Failed {
		past = append(past, briefBlock{md: f.failedLine(x, true), plain: f.failedLine(x, false)})
	}
	for _, d := range f.Done {
		line := func(esc func(string) string) string {
			return i18n.T(l, "assistant-briefing.done_line", i18n.Args{"title": esc(clip(oneLine(d.Title), 60)), "time": f.clock(d.At)})
		}
		past = append(past, briefBlock{md: line(feishubot.EscapeMarkdown) + briefLink(l, todoLink(f.web, d.ID)), plain: line(func(s string) string { return s })})
	}
	pastHead := i18n.T(l, "assistant-briefing.head_past", i18n.Args{"hours": int(briefWindow / time.Hour), "done": f.DoneTotal, "failed": f.FailedTotal})
	room.section(c, &plain, l, pastHead, past, f.DoneTotal+f.FailedTotal)

	// To verify, each row with its Accept button.
	var accept []briefBlock
	for i, r := range f.Accept {
		md := boardRowMarkdown(l, i+1, r)
		btnLabel := i18n.T(l, "assistant-reports.accept_pass")
		a, asked := f.Asks[r.id]
		if asked && !a.Conclusion { // a conclusion's row says its verdict already (boardRows); deployed work's says it here (#1226)
			md += "\n" + feishubot.EscapeMarkdown(verdictText(l, &a))
		}
		if ch := f.Changed[r.id]; ch != "" {
			md += "\n" + feishubot.EscapeMarkdown(clip(oneLine(ch), 80))
		}
		if rec := recommendText(l, &a); asked && rec != "" {
			md += "\n" + feishubot.EscapeMarkdown(rec)
			btnLabel = i18n.T(l, "assistant-reports.verdict_take")
		} else if asked && decideAsk(&a) {
			// Nothing recommended: there is nothing for a button to take, the owner answers its card.
			accept = append(accept, briefBlock{md: md, plain: boardRowPlain(l, i+1, r)})
			continue
		} else if asked && a.Conclusion {
			btnLabel = i18n.T(l, "assistant-reports.verdict_ack")
		}
		btn := feishubot.Btn(btnLabel, feishubot.Primary,
			map[string]any{"a": "accept", "id": r.id, "asked_at": r.asked.UTC().Format(timeKeyLayout)})
		accept = append(accept, briefBlock{md: md, plain: boardRowPlain(l, i+1, r), btn: btn, todo: r.id})
	}
	shown = room.section(c, &plain, l, i18n.T(l, "assistant-briefing.head_accept", i18n.Args{"n": len(f.Accept)}), accept, len(f.Accept))

	// Can wait: the questions Agents left for a briefing, each with a "Handled" button (left out when there are none).
	if f.LaterTotal > 0 {
		var later []briefBlock
		for i, r := range f.Later {
			btn := feishubot.Btn(i18n.T(l, "assistant-briefing.later_done"), feishubot.Default, map[string]any{"a": "later_done", "id": r.id})
			later = append(later, briefBlock{md: boardRowMarkdown(l, i+1, r), plain: boardRowPlain(l, i+1, r), btn: btn})
		}
		room.section(c, &plain, l, i18n.T(l, "assistant-briefing.head_later", i18n.Args{"n": f.LaterTotal}), later, f.LaterTotal)
	}

	// In progress: the tasks being worked on with their stage bar (left out when there are none).
	if f.ProgressTotal > 0 {
		var working []briefBlock
		for i, p := range f.Progress {
			stage := ""
			if p.StageKind != "general" {
				stage = stageBar(l, p.Stage)
			}
			md := fmt.Sprintf("**%d** %s\n%s", i+1, feishubot.EscapeMarkdown(clip(oneLine(p.Title), 60)), stage+briefLink(l, todoLink(f.web, p.ID)))
			working = append(working, briefBlock{md: md, plain: fmt.Sprintf("%d. %s", i+1, clip(oneLine(p.Title), 60))})
		}
		room.section(c, &plain, l, i18n.T(l, "assistant-briefing.head_progress", i18n.Args{"n": f.ProgressTotal}), working, f.ProgressTotal)
	}

	// The recommended setup, on the week's first briefing when the numbers name something (setup_suggest.go): what each
	// mechanism would have removed, its cost and where it is turned on. Nothing is turned on from here.
	if f.Setup != nil && len(f.Setup.Suggestions) > 0 {
		intro := i18n.T(l, "assistant-setup_suggest.intro")
		setup := []briefBlock{{md: feishubot.EscapeMarkdown(intro), plain: intro}}
		for _, sg := range f.Setup.Suggestions {
			setup = append(setup, briefBlock{md: feishubot.EscapeMarkdown(sg.Text) + briefLink(l, sg.Link), plain: sg.Text})
		}
		room.section(c, &plain, l, i18n.T(l, "assistant-setup_suggest.heading"), setup, len(setup))
	}

	c.Note(i18n.T(l, "assistant-briefing.note", i18n.Args{"time": f.clock(f.At)}))
	return c, clip(strings.Join(plain, "\n"), reportFallbackMax), shown
}

// timeKeyLayout writes the time a "Please verify" card was asked in a button: it is compared back to the ask by value.
const timeKeyLayout = "2006-01-02T15:04:05.999999999Z07:00"

// briefBudget is what is left of the card's bytes and elements.
type briefBudget struct{ bytes, els int }

// section adds a heading and as many of its blocks as fit, in one Markdown element, or (when some block has a button)
// one per block; the blocks that do not fit, and those beyond briefRowsMax, are counted in an "and N more" line. total is
// how many rows the section stands for (its blocks may be fewer: the lists are cut before). It returns the todos of the
// blocks with buttons that were added.
func (b *briefBudget) section(c *feishubot.Card, plain *[]string, l i18n.Lang, head string, blocks []briefBlock, total int) (shown []string) {
	head = "**" + feishubot.EscapeMarkdown(head) + "**"
	*plain = append(*plain, head)
	if total == 0 {
		line := head + "\n" + feishubot.EscapeMarkdown(i18n.T(l, "assistant-briefing.none"))
		if n := mdCost(line); n <= b.bytes && b.els >= 1 {
			c.Markdown(line)
			b.bytes, b.els = b.bytes-n, b.els-1
		}
		return nil
	}
	hasButtons := false
	for _, bl := range blocks {
		hasButtons = hasButtons || bl.btn != nil
	}
	body := head // the one Markdown element of a section without buttons, grown row by row
	added := 0
	if hasButtons {
		if n := mdCost(head); n <= b.bytes && b.els >= 1 {
			c.Markdown(head)
			b.bytes, b.els = b.bytes-n, b.els-1
		}
	}
	for _, bl := range blocks {
		if added >= briefRowsMax {
			break
		}
		if hasButtons && bl.btn == nil { // a row without one among rows with buttons (a decision with nothing recommended)
			n := mdCost(bl.md)
			if n > b.bytes || b.els < 1 {
				break
			}
			c.Markdown(bl.md)
			b.bytes, b.els = b.bytes-n, b.els-1
		} else if hasButtons {
			n := feishuElBytes(func(x *feishubot.Card) { x.Markdown(bl.md).Buttons(bl.btn) })
			if n > b.bytes || b.els < 2 {
				break
			}
			c.Markdown(bl.md).Buttons(bl.btn)
			b.bytes, b.els = b.bytes-n, b.els-2
			shown = append(shown, bl.todo)
		} else {
			n := mdCost("\n\n" + bl.md)
			if n > b.bytes {
				break
			}
			body += "\n\n" + bl.md
			b.bytes -= n
		}
		*plain = append(*plain, bl.plain)
		added++
	}
	if !hasButtons && b.els >= 1 {
		c.Markdown(body)
		b.els--
	}
	if more := total - added; more > 0 {
		line := feishubot.EscapeMarkdown(i18n.T(l, "assistant-feishu_cards.board_more", i18n.Args{"count": more}))
		*plain = append(*plain, line)
		c.Markdown(line) // its room is the tail's
	}
	return shown
}

// mdCost is the bytes a Markdown element with this content takes in the card.
func mdCost(s string) int {
	b, _ := json.Marshal(s)
	return len(b) + 40
}

// clock is t as the owner's time of day.
func (f *briefFacts) clock(t time.Time) string { return t.In(f.loc).Format("15:04") }

// failedLine is one thing that did not go through, as a line: what it was, what happened, and why; md: for a card's
// Markdown (escaped), else for its plain-text fallback.
func (f *briefFacts) failedLine(x todayFailed, md bool) string {
	esc := func(s string) string { return s }
	if md {
		esc = feishubot.EscapeMarkdown
	}
	key := "failed_task"
	switch {
	case x.Kind == "routine" && x.Skipped:
		key = "failed_skipped"
	case x.Kind == "routine":
		key = "failed_run"
	}
	line := i18n.T(f.l, "assistant-briefing."+key, i18n.Args{"title": esc(clip(oneLine(x.Title), 60)), "time": f.clock(x.At)})
	if x.Why != "" {
		line += " — " + esc(x.Why)
	}
	return line
}

// briefLink is " · [Open](link)" for an address a card link can hold, else nothing.
func briefLink(l i18n.Lang, link string) string {
	if !boardLinkOK(link) {
		return ""
	}
	return " · [" + i18n.T(l, "assistant-feishu_cards.board_open") + "](" + link + ")"
}

// boardRowPlain is a row as one line of the card's plain-text fallback.
func boardRowPlain(l i18n.Lang, n int, r boardRow) string {
	return fmt.Sprintf("%d. %s · %s · %s", n, r.summary, i18n.T(l, "assistant-feishu_cards.board_waited", i18n.Args{"wait": boardWait(l, r.waited)}), r.from)
}

// countsText is the counts the chat list says, in the bot's language.
func (f *briefFacts) countsText() string { return f.countsIn(f.l) }

// countsIn is the counts the push and the chat list say, in l: "waiting for you 3 · not through 1 · to verify 2".
func (f *briefFacts) countsIn(l i18n.Lang) string {
	var parts []string
	add := func(key string, n int) {
		if n > 0 {
			parts = append(parts, i18n.T(l, "assistant-briefing."+key, i18n.Args{"n": n}))
		}
	}
	if !f.WaitingUnknown {
		add("count_waiting", len(f.Waiting))
	}
	add("count_failed", f.FailedTotal)
	add("count_done", f.DoneTotal)
	add("count_accept", len(f.Accept))
	add("count_later", f.LaterTotal)
	if len(parts) == 0 {
		return i18n.T(l, "assistant-briefing.count_none")
	}
	return strings.Join(parts, i18n.T(l, "assistant-briefing.count_sep"))
}
