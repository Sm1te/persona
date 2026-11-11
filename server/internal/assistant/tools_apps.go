package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// The tools on the knowledge base, which is the assistant's own (kb_*, kb.go), and on what the hub keeps: its
// interaction queue (interaction_answer) and its inbox (inbox_draft), apps that take and return data (app.call
// interactions / inbox). The arguments, the rules and what the model reads are here.

// ---- kb_* (prompts/mcp/kb) ----

func (s *Service) toolKB(ctx context.Context, name string, args json.RawMessage) (string, error) {
	e, err := s.kbGet(ctx)
	if err != nil {
		return "", err
	}
	switch name {
	case "kb_search":
		a := struct {
			Query    string `json:"query"`
			Kind     string `json:"kind"`
			Messages *bool  `json:"messages"`
			Limit    int    `json:"limit"`
		}{}
		_ = json.Unmarshal(args, &a)
		if a.Limit <= 0 {
			a.Limit = 40
		}
		messages, limit := a.Messages == nil || *a.Messages, min(a.Limit, 200)
		var hits []kb.Hit
		switch {
		case a.Kind == "":
			hits = e.Search(a.Query, messages, limit)
		case kbRPCKind(a.Kind):
			hits = e.SearchKind(a.Query, a.Kind, messages, limit)
		default:
			return "", fmt.Errorf("kind %q: one of projects, chats, people, docs, meetings, events, recordings, topics", a.Kind)
		}
		if len(hits) == 0 {
			// A neutral, localized line: it can be relayed to the owner, so it never names the store's disk path.
			l := s.lang(ctx)
			if strings.TrimSpace(a.Query) == "" {
				return i18n.T(l, "assistant-kb_tools.search_empty_no_query"), nil
			}
			return i18n.T(l, "assistant-kb_tools.search_empty", i18n.Args{"query": a.Query}), nil
		}
		var b strings.Builder
		for _, hit := range hits {
			fmt.Fprintf(&b, "%s: %s\n", hit.Path, hit.Line)
		}
		return b.String(), nil
	case "kb_list":
		var a struct {
			Kind, Tag string
			Limit     int
		}
		_ = json.Unmarshal(args, &a)
		if a.Limit <= 0 {
			a.Limit = 30
		}
		items := e.List(a.Kind, a.Tag)
		if a.Kind == kb.KindRecording {
			items = s.recordingLines(ctx, e, items, a.Limit)
		}
		if len(items) > a.Limit {
			items = items[:a.Limit]
		}
		if a.Kind == kb.KindMeeting || a.Kind == kb.KindEvent { // when it is (or was), so "last week's" can be told
			loc := s.location(ctx)
			for i, x := range items {
				if !x.Updated.IsZero() {
					items[i].Extra = strings.TrimSpace(x.Updated.In(loc).Format("2006-01-02 15:04") + " " + x.Extra)
				}
			}
		}
		if len(items) == 0 {
			return i18n.T(s.lang(ctx), "assistant-kb_tools.list_empty"), nil
		}
		var b strings.Builder
		for _, x := range items {
			fmt.Fprintf(&b, "- %s · %s [%s 相关%d] %s %s%s\n", x.ID, x.Name, orDash(x.Importance), x.Relevance,
				strings.Join(x.Tags, ","), x.Extra, prefix(" — ", x.OneLine))
		}
		return b.String(), nil
	case "kb_get":
		var a struct{ Kind, ID string }
		_ = json.Unmarshal(args, &a)
		id := a.ID
		if a.Kind == kb.KindProject { // a project by its name too
			if p := e.FindProject(id); p != nil {
				id = p.Slug
			}
		}
		d, err := e.Get(a.Kind, id, 12000)
		if err != nil {
			return "", fmt.Errorf("%s/%s: %w", a.Kind, a.ID, err)
		}
		// meta.json as the file has it (one edited by hand into something else: its text, as it is).
		meta := []byte(d.Meta)
		var indented bytes.Buffer
		if json.Valid(d.Meta) && json.Indent(&indented, bytes.TrimSpace(d.Meta), "", "  ") == nil {
			meta = append(indented.Bytes(), '\n')
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Path: %s\n\nmeta.json:\n%s\n", d.Path, meta)
		if d.Summary != "" {
			fmt.Fprintf(&b, "\nsummary.md:\n%s\n", d.Summary)
		}
		if len(d.Log) > 0 {
			fmt.Fprintf(&b, "\nlog.md (latest):\n%s\n", strings.Join(d.Log, "\n"))
		}
		if len(d.Moments) > 0 {
			loc := s.location(ctx)
			b.WriteString("\n公司圈 posts about it (full text: moments/posts/<id>.md):\n")
			for _, m := range d.Moments {
				fmt.Fprintf(&b, "- %s %s · %d comments · %s — %s\n", m.ID, m.At.In(loc).Format("2006-01-02"), m.Comments, m.Note, m.Text)
			}
		}
		if len(d.Minutes) > 0 { // a meeting's minutes, before the transcript the 12k cut may end early
			var m kb.Minutes
			if json.Unmarshal(d.Minutes, &m) == nil && m.Markdown != "" {
				fmt.Fprintf(&b, "\nminutes.md:\n%s\n", m.Markdown)
				b.WriteString(s.minutesActionLines(ctx, d.ID, &m))
			}
		}
		if len(d.Recap) > 0 { // an all-day recording's recap (kb_day.go): a whole day's transcript is far past the cut
			var rc kb.DayRecap
			if json.Unmarshal(d.Recap, &rc) == nil && rc.Markdown != "" {
				fmt.Fprintf(&b, "\nrecap.md:\n%s\n", rc.Markdown)
				b.WriteString(s.recapActionLines(ctx, d.ID, &rc))
			}
		}
		// A meeting's photos, their words (photos.md): after the minutes' action items, before the transcript.
		if a.Kind == kb.KindRecording {
			if photos := e.PhotosMarkdown(d.ID); photos != "" {
				fmt.Fprintf(&b, "\nphotos.md:\n%s", photos)
			}
		}
		if a.Kind == kb.KindMeeting || a.Kind == kb.KindEvent {
			b.WriteString(s.recordingsAt(ctx, e, a.Kind, d.ID))
		}
		var trimmed struct {
			At   *time.Time `json:"trimmed_at"`
			Kind string     `json:"kind"`
		}
		switch {
		case a.Kind != kb.KindRecording || json.Unmarshal(d.Meta, &trimmed) != nil || trimmed.At == nil:
		case trimmed.Kind == kb.RecordingDay:
			fmt.Fprintf(&b, "\ntranscript.md: expired %s (assistant.recording_days); only the recap above is kept, until "+
				"assistant.minutes_days after the day (0 means forever, the default), and it can't be written again.\n", trimmed.At.In(s.location(ctx)).Format("2006-01-02"))
		default:
			fmt.Fprintf(&b, "\ntranscript.md: expired %s (assistant.recording_days); only the minutes above are kept, until "+
				"assistant.minutes_days after the meeting (0 means forever, the default), and they can't be made again.\n", trimmed.At.In(s.location(ctx)).Format("2006-01-02"))
		}
		switch {
		case d.Recent != "" && a.Kind == kb.KindRecording:
			fmt.Fprintf(&b, "\ntranscript.md:\n%s\n", d.Recent)
		case d.Recent != "":
			fmt.Fprintf(&b, "\nMessages, last 7 days:\n%s\n", d.Recent)
		}
		return b.String(), nil
	case "kb_update":
		if e.Store.ReadOnly() {
			return "", kb.ErrReadOnly
		}
		var a struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
			kb.UserUpdate
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		id, err := e.ApplyUser(a.Kind, a.ID, a.UserUpdate)
		if err != nil {
			return "", err
		}
		if a.Unmerge {
			return "Unmerged " + a.Kind + "/" + id + ".", nil
		}
		return "Saved " + a.Kind + "/" + id + ".", nil
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func prefix(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

// ---- interaction_answer (prompts/mcp/agents) ----

// toolInteractionAnswer answers another agent's question for the user (docs/assistant/assistant.md). Only a question may be
// answered here: a permission, a plan and a terminal dialog are the user's to decide, and an assistant conversation
// (this one among them) is never answered by the assistant.
func (s *Service) toolInteractionAnswer(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Machine       string          `json:"machine"`
		SessionID     string          `json:"session_id"`
		InteractionID string          `json:"interaction_id"`
		Answers       json.RawMessage `json:"answers"`
		Reason        string          `json:"reason"`
	}
	_ = json.Unmarshal(args, &a)
	am, c, err := s.findAgent(ctx, a.Machine, a.SessionID)
	if err != nil {
		return "", err
	}
	if c.Assistant || c.SessionID == self {
		return "", errors.New("that interaction belongs to an assistant conversation; only the user answers it")
	}
	id := strings.TrimSpace(a.InteractionID)
	var got struct {
		Interaction proto.Interaction `json:"interaction"`
		OK          bool              `json:"ok"`
		Status      string            `json:"status"`
	}
	if err := s.appCall(ctx, AppInteractions, assistantwire.InteractionsGet, map[string]any{"interaction_id": id}, &got); err != nil {
		return "", err
	}
	if !got.OK {
		return "", interactionEnded(id, got.Status)
	}
	ix := got.Interaction
	if ix.Kind != proto.IxQuestion {
		return "", fmt.Errorf("interaction %s is a %s (approval), only the user can answer it in the app", ix.ID, ix.Kind)
	}
	if ix.MachineID != am.ID || ix.SessionID != c.SessionID {
		return "", fmt.Errorf("interaction %s belongs to another agent: machine=%s session_id=%s", ix.ID, ix.MachineID, ix.SessionID)
	}
	if time.Now().After(ix.ExpiresAt) {
		return "", fmt.Errorf("interaction %s expired", ix.ID)
	}
	answers, err := interactionAnswers(ix, a.Answers)
	if err != nil {
		return "", err
	}
	// A card handed to this conversation before the user (ask_route.go): its answer is the task's decision.
	todo, routed := s.routedTo(ix.ID, self)
	var res struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	if err := s.appCall(ctx, AppInteractions, assistantwire.InteractionsAnswer, map[string]any{"machine_id": ix.MachineID,
		"session_id": ix.SessionID, "interaction_id": ix.ID, "answers": answers, "reason": clip(oneLine(a.Reason), 120)}, &res); err != nil {
		return "", err
	}
	if !res.OK {
		return "", interactionEnded(ix.ID, res.Status)
	}
	if routed {
		s.askAnswered(ctx, todo, self, ix, answers, a.Reason)
	}
	var b strings.Builder
	b.WriteString("Answered for the user:\n")
	for _, q := range ix.Questions {
		fmt.Fprintf(&b, "- %s → %s\n", q.Question, answers[q.Question])
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// interactionEnded says how an interaction that is no longer pending ended (status, from the hub's history), or that
// there is none.
func interactionEnded(id, status string) error {
	switch status {
	case "resolved":
		return fmt.Errorf("interaction %s was already answered", id)
	case "dismissed":
		return fmt.Errorf("interaction %s was dismissed", id)
	case "expired":
		return fmt.Errorf("interaction %s expired", id)
	case "cancelled":
		return fmt.Errorf("interaction %s was cancelled", id)
	}
	return fmt.Errorf("no pending interaction %s (use agents_list)", id)
}

// interactionAnswers checks a question's answers against the questions themselves: every question answered, by the
// wording it was asked, with options it offers (an option written out with its description counts as its label,
// optionLabel). A multi-select takes several labels and becomes the app's own form, "A, B". Free text is refused: the
// user answers that in the app, or through agent_send.
func interactionAnswers(ix proto.Interaction, args json.RawMessage) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args, &raw) != nil {
		return nil, fmt.Errorf("answers is required: an object of question → option label")
	}
	asked := map[string]bool{}
	for _, q := range ix.Questions {
		asked[q.Question] = true
	}
	for k := range raw {
		if !asked[k] {
			return nil, fmt.Errorf("%q is not a question this interaction asks; it asks: %s", k, askedQuestions(ix))
		}
	}
	answers := make(map[string]string, len(ix.Questions))
	for _, q := range ix.Questions {
		v, ok := raw[q.Question]
		if !ok {
			return nil, fmt.Errorf("every question needs an answer, %q has none (options: %s)", q.Question, optionLabels(q))
		}
		var one string
		var many []string
		if json.Unmarshal(v, &many) != nil {
			if json.Unmarshal(v, &one) != nil {
				return nil, fmt.Errorf("%q takes an option label or an array of them (options: %s)", q.Question, optionLabels(q))
			}
			if !q.MultiSelect {
				answers[q.Question] = optionLabel(q, one)
			} else {
				many = []string{one}
			}
		}
		if many != nil {
			if !q.MultiSelect && len(many) > 1 {
				return nil, fmt.Errorf("%q takes one option; pick one of: %s", q.Question, optionLabels(q))
			}
			for i, a := range many {
				many[i] = optionLabel(q, a)
			}
			answers[q.Question] = strings.Join(many, ", ")
		}
		if err := checkOptions(q, answers[q.Question]); err != nil {
			return nil, err
		}
	}
	return answers, nil
}

// checkOptions refuses an answer whose labels are not the question's options, listing them.
func checkOptions(q proto.Question, answer string) error {
	offered := map[string]bool{}
	for _, o := range q.Options {
		offered[o.Label] = true
	}
	picked := 0
	for _, label := range strings.Split(answer, ", ") {
		if !offered[label] {
			return fmt.Errorf("%q is not one of %q's options: %s", label, q.Question, optionLabels(q))
		}
		picked++
	}
	if picked == 0 {
		return fmt.Errorf("%q needs an option: %s", q.Question, optionLabels(q))
	}
	if !q.MultiSelect && picked > 1 {
		return fmt.Errorf("%q takes one option; pick one of: %s", q.Question, optionLabels(q))
	}
	return nil
}

// optionLabel is the label of the option answer names: answer itself when it is a label. The delegation input lists
// an option as "label：description" (prompts/agents/delegate_input.md), and a model sometimes copies the whole line
// (#3929), so an answer that writes out exactly one option, its label and then its own description, is that option's
// label too: with "：", ":" or a dash between them, or the description in brackets, and spaces or quotes around the
// label or the whole answer. Anything else, the model's own words after a label or text that reads as two options,
// comes back as it is, for checkOptions to refuse.
func optionLabel(q proto.Question, answer string) string {
	for _, o := range q.Options {
		if o.Label == answer {
			return answer
		}
	}
	label := ""
	for _, o := range q.Options {
		if !writesOut(answer, o) {
			continue
		}
		if label != "" && label != o.Label {
			return answer
		}
		label = o.Label
	}
	if label == "" {
		return answer
	}
	return label
}

// labelSeparators may come between an option's label and its description; a longer one before its prefix.
var labelSeparators = []string{"——", "--", "：", ":", "—", "–", "－", "-"} // i18n-ignore: punctuation matched in a model's answer

// descBrackets may hold the description after an option's label.
var descBrackets = [][2]string{{"（", "）"}, {"(", ")"}} // i18n-ignore: punctuation matched in a model's answer

// labelQuotes may wrap an option's label, or a whole answer.
var labelQuotes = [][2]string{{`"`, `"`}, {"'", "'"}, {"`", "`"}, {"“", "”"}, {"‘", "’"}, {"「", "」"}, {"『", "』"}} // i18n-ignore: punctuation matched in a model's answer

// writesOut: answer is option o written out, its label alone or followed by its own description.
func writesOut(answer string, o proto.QuestionOption) bool {
	label := strings.TrimSpace(o.Label)
	if label == "" {
		return false
	}
	s := unquoted(answer)
	if s == label {
		return true
	}
	desc := plainDescription(o.Description)
	if desc == "" {
		return false
	}
	rest, ok := cutLabel(s, label)
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	for _, sep := range labelSeparators {
		if d, ok := strings.CutPrefix(rest, sep); ok {
			return plainDescription(d) == desc
		}
	}
	for _, b := range descBrackets {
		if d, ok := strings.CutPrefix(rest, b[0]); ok {
			d, ok = strings.CutSuffix(d, b[1])
			return ok && plainDescription(d) == desc
		}
	}
	return false
}

// unquoted is s without the spaces and quotes around it.
func unquoted(s string) string {
	s = strings.TrimSpace(s)
	for again := true; again; {
		again = false
		for _, p := range labelQuotes {
			if len(s) > len(p[0])+len(p[1]) && strings.HasPrefix(s, p[0]) && strings.HasSuffix(s, p[1]) {
				s, again = strings.TrimSpace(s[len(p[0]):len(s)-len(p[1])]), true
				break
			}
		}
	}
	return s
}

// cutLabel is what follows label at the start of s, the label bare or in quotes.
func cutLabel(s, label string) (string, bool) {
	if rest, ok := strings.CutPrefix(s, label); ok {
		return rest, true
	}
	for _, p := range labelQuotes {
		if rest, ok := strings.CutPrefix(s, p[0]+label+p[1]); ok {
			return rest, true
		}
	}
	return "", false
}

// plainDescription is an option's description as compared: its words one space apart, no full stop at the end.
func plainDescription(s string) string {
	return strings.TrimRight(strings.Join(strings.Fields(s), " "), "。.") // i18n-ignore: punctuation matched in a model's answer
}

func optionLabels(q proto.Question) string {
	labels := make([]string, len(q.Options))
	for i, o := range q.Options {
		labels[i] = o.Label
	}
	return strings.Join(labels, ", ")
}

func askedQuestions(ix proto.Interaction) string {
	qs := make([]string, len(ix.Questions))
	for i, q := range ix.Questions {
		qs[i] = q.Question
	}
	return strings.Join(qs, "; ")
}

// ---- inbox_draft (prompts/mcp/inbox) ----

// placeholderRE: a blank a draft leaves for the user to fill in — text in full-width lenticular brackets, {{…}}.
var placeholderRE = lazyre.New(`【[^】\n]*】|\{\{[^}\n]*\}\}`)

// draftPlaceholder is the first blank in a draft left to fill in; empty when it has none.
func draftPlaceholder(text string) string {
	return placeholderRE.FindString(text)
}

// toolInboxDraft gives the inbox the draft of the AI twin working in conversation self (docs/assistant/inbox.md, inboxDraft):
// it waits on the item for the user to send.
func (s *Service) toolInboxDraft(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Text, Note string
		Changes    []DocChange
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Text) == "" {
		return "", errors.New("text is required")
	}
	// The user sends it with one tap: a blank left to fill in (a bracketed "by what time") would go out as it is.
	if p := draftPlaceholder(a.Text); p != "" {
		return "", fmt.Errorf("the draft leaves %s to fill in, and it goes out as it is: leave out what you could not find (say so in note) and submit again", p)
	}
	itemID, refused, err := s.inboxDraft(ctx, self, strings.TrimSpace(a.Text), clip(strings.TrimSpace(a.Note), 600), a.Changes)
	if err != nil { // not kept: the event is back where the turn found it, so the draft can be handed in again
		return "", err
	}
	if itemID == "" {
		return "", errors.New("this conversation was not started for an inbox item")
	}
	// Only moves the item offers (a row it lists, one of the column's values).
	if refused != nil {
		return "", fmt.Errorf("change %+v is not one the item lists (ref, field and one of its values)", *refused)
	}
	return "The draft is in the user's inbox; they will send it. You're done.", nil
}

// recordingsAt is what kb_get adds to calendar item kind/id (meetings or events, id as stored): the recordings matched
// to it by time (kb/minutes.go matchRecordings), which hold its minutes; "" when there are none.
func (s *Service) recordingsAt(ctx context.Context, e *kb.Engine, kind, id string) string {
	var at []kb.Entry
	for _, r := range e.Store.Recordings() {
		if r.Meeting != nil && r.Meeting.Kind == kind && kb.SafeID(r.Meeting.ID) == id {
			at = append(at, kb.Entry{Kind: kb.KindRecording, ID: r.ID, Name: r.Name, Updated: r.StartedAt})
		}
	}
	if len(at) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nRecorded at it (kb_get kind=recordings for the minutes and the transcript):\n")
	for _, x := range s.recordingLines(ctx, e, at, 10) {
		fmt.Fprintf(&b, "- %s · %s %s\n", x.ID, x.Name, x.Extra)
	}
	return b.String()
}

// recordingLines is kb_list's first limit recordings newest first (what "the meeting just now" asks for), each saying
// when it started, for how long, and whether it has minutes.
func (s *Service) recordingLines(ctx context.Context, e *kb.Engine, items []kb.Entry, limit int) []kb.Entry {
	loc := s.location(ctx)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Updated.After(items[j].Updated) })
	items = items[:min(limit, len(items))]
	for i, x := range items {
		r, ok := e.Store.Recording(x.ID)
		if !ok {
			continue
		}
		extra := r.StartedAt.In(loc).Format("2006-01-02 15:04") + " " + strconv.Itoa(int(r.Duration/60)) + "min " + r.Device
		switch r.MinutesState {
		case kb.MinutesReady:
			extra += " minutes"
		case kb.MinutesPending:
			extra += " minutes-pending"
		case kb.MinutesFailed:
			extra += " minutes-failed"
		}
		if r.Kind == kb.RecordingDay { // an all-day recording (kb_day.go): its recap, not minutes
			extra += " all-day"
			if r.RecapState != "" {
				extra += " recap-" + r.RecapState
			}
		}
		if r.Trimmed() {
			extra += " transcript-expired"
		}
		items[i].Extra = extra
	}
	return items
}
