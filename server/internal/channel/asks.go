package channel

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// A channel without buttons (asks: text) gets what an agent asks — a question, a permission, a plan, a terminal
// dialog — as text, and the owner's next message answers it: a number, "allow" / "deny", "approve" (in Chinese), or words. The latest one
// asked is the one a reply answers; one answered somewhere else (the app) is let go, and the reply is then an
// ordinary message (docs/extensions/channels.md "Questions and approvals").

// Ask is something asked in a channel as text, waiting for the owner's reply.
type Ask struct {
	ID        string           `json:"id"`
	Channel   string           `json:"channel"`
	MachineID string           `json:"machine_id"`
	SessionID string           `json:"session_id"`
	Kind      string           `json:"kind"`
	Title     string           `json:"title,omitempty"`
	Questions []proto.Question `json:"questions,omitempty"`
	// Answers so far, a question at a time (Step is the one asked now).
	Answers map[string]string `json:"answers,omitempty"`
	Step    int               `json:"step,omitempty"`
	// Options are a terminal dialog's option keys, in the order they were numbered.
	Options []string  `json:"options,omitempty"`
	At      time.Time `json:"at"`
	// Later: the assistant's ask_user raised with wait false (proto.Interaction.AskedWithoutWaiting). No turn waits on
	// it, so only a reply that reads as its answer (ReadsAsAnswer) is one; other words are an ordinary message.
	Later bool `json:"later,omitempty"`
}

// NewAsk is ix as something to ask in channel.
func NewAsk(channel string, ix proto.Interaction, now time.Time) *Ask {
	return &Ask{ID: ix.ID, Channel: channel, MachineID: ix.MachineID, SessionID: ix.SessionID, Kind: ix.Kind, Title: askTitle(ix),
		Questions: ix.Questions, At: now, Later: ix.AskedWithoutWaiting()}
}

// askTitle is the bracketed title (full-width lenticular brackets) an ask starts with: its conversation's title without the blocks a first message starts with
// (stripped before anything clips it), else the project.
func askTitle(ix proto.Interaction) string {
	if t := transcript.Title(ix.SessionTitle); t != "" || ix.SessionTitle == "" || ix.Cwd == "" {
		return t
	}
	return path.Base(ix.Cwd)
}

// AskText is the text asking ix in l (it fills in a's options); false when it cannot be answered in a channel (the
// text then says where).
func AskText(l i18n.Lang, ix proto.Interaction, a *Ask) (string, bool) {
	head := ""
	if t := FirstLine(transcript.Title(a.Title)); t != "" { // an ask kept from before NewAsk cleaned it
		head = i18n.T(l, "channel-asks.head", i18n.Args{"title": t})
	}
	switch {
	case len(ix.Fields) > 0:
		return head + i18n.T(l, "channel-asks.fields_in_app", i18n.Args{"summary": FirstLine(ix.Summary)}), false
	case ix.Kind == proto.IxPermission:
		what := strings.TrimSpace(ix.Summary)
		if what == "" {
			what = toolBrief(ix.ToolInput)
		}
		if ix.Reason != "" {
			what += i18n.T(l, "channel-asks.reason", i18n.Args{"reason": ix.Reason})
		}
		return head + i18n.T(l, "channel-asks.permission", i18n.Args{"tool": toolName(l, ix), "what": Clip(what, 1500)}), true
	case ix.Kind == proto.IxPlan:
		return head + i18n.T(l, "channel-asks.plan", i18n.Args{"plan": Clip(ix.Plan, 3000)}), true
	case ix.Kind == proto.IxQuestion && len(ix.Questions) > 0:
		return head + QuestionText(l, a, 0), true
	case ix.Kind == proto.IxScreen && ix.Dialog != nil && len(ix.Dialog.Options) > 0 && !ix.Dialog.Multi:
		a.Options = nil
		var lines []string
		for i, o := range ix.Dialog.Options {
			a.Options = append(a.Options, o.Key)
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, o.Label))
		}
		q := strings.TrimSpace(ix.Dialog.Question)
		if ix.Dialog.Detail != "" {
			q = strings.TrimSpace(ix.Dialog.Detail) + "\n" + q
		}
		return head + i18n.T(l, "channel-asks.screen", i18n.Args{"question": Clip(q, 1500), "options": strings.Join(lines, "\n")}), true
	}
	return head + i18n.T(l, "channel-asks.other", i18n.Args{"summary": FirstLine(ix.Summary)}), false
}

// QuestionText asks a's question i in l.
func QuestionText(l i18n.Lang, a *Ask, i int) string {
	q := a.Questions[i]
	var b strings.Builder
	if len(a.Questions) > 1 {
		b.WriteString(i18n.T(l, "channel-asks.question_step", i18n.Args{"n": i + 1, "total": len(a.Questions)}))
	}
	b.WriteString(i18n.T(l, "channel-asks.question_asks"))
	if q.Header != "" && q.Header != q.Question {
		b.WriteString(i18n.T(l, "channel-asks.question_header", i18n.Args{"header": q.Header}))
	}
	b.WriteString(q.Question)
	for j, o := range q.Options {
		fmt.Fprintf(&b, "\n%d. %s", j+1, o.Label)
		if o.Description != "" {
			b.WriteString(" — " + o.Description)
		}
	}
	switch {
	case len(q.Options) > 0 && q.MultiSelect:
		b.WriteString(i18n.T(l, "channel-asks.reply_numbers"))
	case len(q.Options) > 0:
		b.WriteString(i18n.T(l, "channel-asks.reply_number"))
	default:
		b.WriteString(i18n.T(l, "channel-asks.reply_free"))
	}
	return b.String()
}

// The words a reply answers with, in any of the three languages whatever the user's is now (the texts asking say the
// user's language's, but someone may answer in another): compared lowercased, without trailing punctuation.
var (
	yes = []string{"允许", "同意", "确认", "可以", "好", "好的", "好了", "好吧", "行", "行吧", "批准",
		"y", "yes", "ok", "okay", "yeah", "yep", "approved", "allow", "sure", "approve",
		"許可", "はい", "承認", "いいよ", "いいです"}
	no = []string{"拒绝", "不批准", "不允许", "不同意", "不行", "不要", "取消", "n", "no", "deny",
		"reject", "cancel", "拒否", "いいえ", "だめ", "ダメ", "キャンセル", "却下"}
	planRun     = []string{"批准并自动执行", "批准并运行", "批准运行", "自动执行", "approve and run", "run", "auto", "承認して自動実行", "自動実行"}
	planApprove = []string{"批准", "approve", "承認"}
)

// trailing is the punctuation a reply may end with, ignored when reading it: sentence stops, commas, a wave; not a
// word of the answer itself.
const trailing = "。.．,，、!！?？~～"

func is(text string, words []string) bool {
	t := strings.ToLower(strings.TrimRight(strings.TrimSpace(text), trailing))
	return slices.Contains(words, t)
}

// IsApprovalWord says whether text is only a word that approves or refuses a card (yes, no, the plan's approve), in
// any of the three languages: a voice conversation does not send it on while its conversation waits on such a card,
// since a card is answered on the screen (docs/assistant/voice.md "Sending, gates, and 409").
func IsApprovalWord(text string) bool {
	return is(text, yes) || is(text, no) || is(text, planApprove)
}

// numberEnd is the punctuation an option number may end with (a full stop, an enumeration comma, !); not ? — "1?"
// asks what 1 is.
const numberEnd = "。.．、!！"

// circledOne is ①; circled digits run ①…⑳ (U+2460…U+2473), the shapes a phone's keyboard offers for a list number.
const (
	circledOne = '①'
	circledMax = 20
)

// numberWrappers are the paired brackets a phone puts around a typed number, whole: (1) and [1] in
// half- and full-width forms, and the CJK corner and lenticular brackets. A right parenthesis after the bare digits,
// 1) in half- or full-width, is stripped below.
var numberWrappers = [][2]rune{
	{'（', '）'}, {'(', ')'}, {'【', '】'}, {'[', ']'}, {'「', '」'}, {'『', '』'},
}

// number reads text as an option number: full-width digits (a Chinese or Japanese keyboard's) count, a trailing full
// stop too, and so do the shapes phones type a number in: a circled digit ①, a paired bracket around the whole
// number, or a trailing right parenthesis. A leading zero is no number, and a question mark stays a question ("1?",
// "①?").
func number(text string) (int, bool) {
	t := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(text), numberEnd))
	if t == "" {
		return 0, false
	}
	if r := []rune(t); len(r) == 1 {
		if n := int(r[0]) - int(circledOne) + 1; n >= 1 && n <= circledMax {
			return n, true
		}
	}
	t = unwrappedNumber(t)
	var b strings.Builder
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= '０' && r <= '９':
			b.WriteRune('0' + r - '０')
		default:
			return 0, false
		}
	}
	if d := b.String(); d != "" && d[0] != '0' {
		n, err := strconv.Atoi(d)
		return n, err == nil
	}
	return 0, false
}

// unwrappedNumber takes the phone shapes off a number typed whole: a paired bracket around it (full-width or not), or a right
// parenthesis after it 1). Anything that is not exactly the wrapper around the digits is returned as it was.
func unwrappedNumber(text string) string {
	r := []rune(text)
	if len(r) >= 3 {
		for _, w := range numberWrappers {
			if r[0] == w[0] && r[len(r)-1] == w[1] {
				return string(r[1 : len(r)-1])
			}
		}
	}
	if len(r) >= 2 && (r[len(r)-1] == ')' || r[len(r)-1] == '）') {
		return string(r[:len(r)-1])
	}
	return text
}

// TypedAnswerKinds are the kinds of card ReadAnswer takes a channel's text answer for: what an owner's message in a
// channel may answer (the hub's owner action for the message, extmgr issueMessageAction).
var TypedAnswerKinds = []string{proto.IxPermission, proto.IxPlan, proto.IxScreen, proto.IxQuestion}

// ReadAnswer reads text as the answer to a: the resolution once complete (done), or what to say in l when text does
// not answer it (again). A question of several takes one answer per message: a.Step moves on, and the caller asks
// QuestionText(l, a, a.Step) next.
func ReadAnswer(l i18n.Lang, a *Ask, text string) (r proto.Resolution, done bool, again string) {
	t, f := true, false
	switch a.Kind {
	case proto.IxPermission:
		switch {
		case is(text, yes):
			r.Allow = &t
		case is(text, no):
			r.Allow = &f
		default:
			return r, false, i18n.T(l, "channel-asks.again_permission")
		}
		return r, true, ""
	case proto.IxPlan:
		n, numbered := number(text)
		switch {
		case numbered && n == 1 || is(text, planRun):
			r.Approve, r.Mode = &t, "bypassPermissions"
		case numbered && n == 2 || is(text, planApprove) || is(text, yes):
			r.Approve, r.Mode = &t, "acceptEdits"
		case is(text, no):
			r.Approve = &f
		default:
			r.Approve, r.Feedback = &f, text
		}
		return r, true, ""
	case proto.IxScreen:
		n, ok := number(text)
		if !ok || n < 1 || n > len(a.Options) {
			return r, false, i18n.T(l, "channel-asks.again_screen", i18n.Args{"max": len(a.Options)})
		}
		r.Option = a.Options[n-1]
		return r, true, ""
	case proto.IxQuestion:
		if a.Step >= len(a.Questions) {
			return r, false, i18n.T(l, "channel-asks.again_question")
		}
		q := a.Questions[a.Step]
		if a.Answers == nil {
			a.Answers = map[string]string{}
		}
		a.Answers[q.Question] = optionAnswer(q, text)
		a.Step++
		if a.Step < len(a.Questions) {
			return r, false, ""
		}
		r.Answers = a.Answers
		return r, true, ""
	}
	return r, false, i18n.T(l, "channel-asks.again_app")
}

// ReadsAsAnswer says whether text reads as a reply to a, not an ordinary message: Allow / Deny, a plan's numbers or
// approve words, a terminal dialog's option number, or one of the current question's options. Free words do not: they
// may be a new message. It is used for an ask that ended elsewhere, whose late reply must not go into the conversation.
func ReadsAsAnswer(a *Ask, text string) bool {
	switch a.Kind {
	case proto.IxPermission:
		return is(text, yes) || is(text, no)
	case proto.IxPlan:
		n, numbered := number(text)
		return numbered && (n == 1 || n == 2) || is(text, planRun) || is(text, planApprove) || is(text, yes) || is(text, no)
	case proto.IxScreen:
		n, ok := number(text)
		return ok && n >= 1 && n <= len(a.Options)
	case proto.IxQuestion:
		step := min(a.Step, len(a.Questions)-1)
		if step < 0 || len(a.Questions[step].Options) == 0 {
			return false
		}
		_, ok := MatchOption(a.Questions[step], text)
		return ok
	}
	return false
}

// optionAnswer is text as an answer to q: the options it names (MatchOption: numbers, ordinals, labels; several for a
// multi-select) become their labels, anything else is the answer itself.
func optionAnswer(q proto.Question, text string) string {
	if label, ok := MatchOption(q, text); ok {
		return label
	}
	return strings.TrimSpace(text)
}

// Answered is the line in l saying what a reply did.
func Answered(l i18n.Lang, r proto.Resolution) string {
	key := "answered"
	switch {
	case r.Allow != nil && *r.Allow:
		key = "allowed"
	case r.Allow != nil:
		key = "denied"
	case r.Approve != nil && *r.Approve:
		key = "approved"
	case r.Approve != nil && r.Feedback != "":
		key = "sent_back_with_note"
	case r.Approve != nil:
		key = "sent_back"
	}
	return i18n.T(l, "channel-asks."+key)
}

func toolName(l i18n.Lang, ix proto.Interaction) string {
	if ix.ToolTitle != "" { // the manager's own tools, by their name for people
		return ix.ToolTitle
	}
	if ix.ToolName != "" {
		return ix.ToolName
	}
	return i18n.T(l, "channel-asks.an_action")
}

// toolBrief is a tool call's input in a line or two: its command, file or URL when it has one, else its first other
// text argument, else the input with every argument named like a credential masked (#4734).
func toolBrief(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "url", "pattern", "query"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	if s := proto.ArgGist(input); s != "" {
		return Clip(s, 300)
	}
	return Clip(proto.RedactArgs(input), 300)
}

// FirstLine is s's first line, clipped.
func FirstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return Clip(line, 200)
}

// Clip is s cut to n runes, with an ellipsis when cut.
func Clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
