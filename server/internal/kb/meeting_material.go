package kb

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// What the answering model of an attended meeting reads (docs/assistant/meeting-minutes.md "What the hub gives the
// answering model"): an answer may be said aloud in front of other people, who can ask for anything it was given, so
// the boundary is this material, not the prompt. Until #3862 phase 1 adds a private-meeting mark, only this meeting's
// own material is included. EarlierMeetingsInAnswers gates the other three sources below together, and with it only
// meetings marked not private are material ("The private mark", meeting.go).
// The four possible sources are this meeting's own
// upload title and calendar hint, live sentences and answered questions; the minutes of earlier meetings (never their
// transcript, summary, live log or questions); the name and status of the projects those meetings are linked to; and
// the to-do ids of their action items, whose open or done the assistant looks up. Nothing else of the knowledge base
// (chats, people, documents, notes, project goals, summaries or timelines, pinned facts) is read here.

// MeetingMaterial is everything an answer during recording id's meeting may draw on.
type MeetingMaterial struct {
	// Title and Calendar are the upload's own (the user's title, the phone's calendar hint), never the knowledge
	// base's matched calendar item; Language is the recording's.
	Title    string
	Calendar *RecordingCalendar
	Language string
	// Lines are the latest materialLiveRunes of live text, oldest first (an older line cut at its head to fit).
	Lines []LiveSegment
	// Asks are this meeting's questions already answered, oldest first.
	Asks []LiveAsk
	// EarlierAvailable distinguishes an allowed but empty lookup from material excluded by the privacy policy.
	EarlierAvailable bool
	// Earlier are at most materialMaxEarlier earlier meetings, the best ranked first.
	Earlier []EarlierMeeting
}

// EarlierMeeting is an earlier meeting as the material has it: its minutes, reduced to what may be said.
type EarlierMeeting struct {
	ID   string
	Name string // the minutes' title (their when without one)
	When string // the minutes' when: a display string in the user's zone
	// Start and End are the minutes' span (#4713), for an answer's sources.
	Start, End time.Time
	// Minutes holds only the title, when, attendees, points (text, who), decisions and open questions (text), and
	// action items (who, what, by, due_date, todo_id).
	Minutes  Minutes
	Projects []EarlierProject
}

// EarlierProject is a project an earlier meeting is linked to: its name and status, nothing else.
type EarlierProject struct {
	Slug   string
	Name   string
	Status string // active | paused | done
}

// EarlierMaterial is the earlier meetings for a question asked during recording id's meeting (the room's
// meeting_earlier): none while EarlierMeetingsInAnswers is off. ErrRecordingDeleted after a delete, ErrNotFound with no
// upload of it.
func (e *Engine) EarlierMaterial(rawID, query string) ([]EarlierMeeting, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	if _, gone := e.Store.tombstones()[id]; gone {
		return nil, ErrRecordingDeleted
	}
	r, ok := e.Store.Recording(id)
	if !ok {
		return nil, ErrNotFound
	}
	if !e.EarlierMeetingsInAnswers {
		return nil, nil
	}
	return e.earlierMeetings(r, query), nil
}

// MeetingMaterial is the material for a question asked during recording id's meeting; question ranks the earlier
// meetings. ErrRecordingDeleted after a delete, ErrNotFound with no upload of it.
func (e *Engine) MeetingMaterial(rawID, question string) (MeetingMaterial, error) {
	l, r, err := e.LiveRead(rawID)
	if err != nil {
		return MeetingMaterial{}, err
	}
	m := MeetingMaterial{Title: r.Title, Language: r.Language, Lines: latestLines(l.Segments, materialLiveRunes),
		EarlierAvailable: e.EarlierMeetingsInAnswers}
	if c := r.Calendar; c != nil {
		m.Calendar = &RecordingCalendar{Title: c.Title, Attendees: slices.Clone(c.Attendees), Location: c.Location}
	}
	for _, a := range l.Asks {
		if a.State == "answered" {
			a.Sources = slices.Clone(a.Sources)
			m.Asks = append(m.Asks, a)
		}
	}
	if m.EarlierAvailable {
		m.Earlier = e.earlierMeetings(&r, question)
	}
	return m, nil
}

// latestLines is the last live sentences with text, at most n runes of it, oldest first.
func latestLines(segs []LiveSegment, n int) []LiveSegment {
	var out []LiveSegment
	for i := len(segs) - 1; i >= 0 && n > 0; i-- {
		sg := segs[i]
		if sg.Text == "" {
			continue
		}
		if c := utf8.RuneCountInString(sg.Text); c > n {
			r := []rune(sg.Text)
			sg.Text = string(r[len(r)-n:])
			n = 0
		} else {
			n -= c
		}
		out = append(out, sg)
	}
	slices.Reverse(out)
	return out
}

// earlierMeetings are the meetings the material may hold besides r, ranked for question. Eligible: another recording
// still stored whose upload says it is a meeting (kind meeting) or whose minutes the user asked for, marked not
// private (a meeting with no mark counts as private), with minutes written. Never one that is a meeting only because
// a calendar item overlapped it, and never a note.
func (e *Engine) earlierMeetings(r *Recording, question string) []EarlierMeeting {
	here := meetingTerms{question: termsOf(question)}
	for _, t := range []string{r.Title, calendarHintTitle(r)} {
		if t = fold(strings.TrimSpace(t)); t != "" && !slices.Contains(here.titles, t) {
			here.titles = append(here.titles, t)
		}
	}
	here.titleTerms = termsOf(strings.Join(here.titles, " "))
	if r.Calendar != nil {
		for _, a := range r.Calendar.Attendees {
			here.attendees = append(here.attendees, fold(a))
		}
	}
	type ranked struct {
		m     EarlierMeeting
		score int
		r     *Recording
	}
	var all []ranked
	zone := e.Store.Zone()
	for _, o := range e.Store.Recordings() {
		if o.ID == r.ID || !o.IsMeeting() || !o.MarkedNotPrivate() {
			continue
		}
		mins, err := e.Store.ReadMinutes(o.ID)
		if err != nil {
			continue
		}
		mins.spanOf(o, zone)
		// Named by its minutes only: the recording's name may be the summary's title, made from the transcript.
		em := EarlierMeeting{ID: o.ID, Name: cmp.Or(mins.Title, mins.When), When: mins.When, Start: mins.Start, End: mins.End,
			Minutes:  materialMinutes(mins),
			Projects: e.materialProjects(o.Projects)}
		all = append(all, ranked{m: em, score: here.score(&em), r: o})
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if !a.r.StartedAt.Equal(b.r.StartedAt) {
			return a.r.StartedAt.After(b.r.StartedAt) // the more recent first
		}
		return a.r.ID < b.r.ID
	})
	out := make([]EarlierMeeting, 0, min(len(all), materialMaxEarlier))
	for _, x := range all[:min(len(all), materialMaxEarlier)] {
		out = append(out, x.m)
	}
	return out
}

// calendarHintTitle is the title of the phone's calendar hint only (calendarTitle falls back to the matched item).
func calendarHintTitle(r *Recording) string {
	if r.Calendar != nil {
		return r.Calendar.Title
	}
	return ""
}

// materialMinutes is m reduced to what the material may hold.
func materialMinutes(m *Minutes) Minutes {
	out := Minutes{Title: m.Title, When: m.When, Attendees: slices.Clone(m.Attendees)}
	for _, p := range m.Points {
		out.Points = append(out.Points, MinutesPoint{Text: p.Text, Who: p.Who})
	}
	for _, d := range m.Decisions {
		out.Decisions = append(out.Decisions, MinutesItem{Text: d.Text})
	}
	for _, a := range m.Actions {
		out.Actions = append(out.Actions, MinutesAction{Who: a.Who, What: a.What, By: a.By, DueDate: a.DueDate, TodoID: a.TodoID})
	}
	for _, o := range m.Open {
		out.Open = append(out.Open, MinutesItem{Text: o.Text})
	}
	return out
}

// materialProjects are the projects of slugs that are active, paused or done (not ignored or merged): name and
// status only.
func (e *Engine) materialProjects(slugs []string) []EarlierProject {
	var out []EarlierProject
	for _, slug := range slugs {
		p, ok := e.Store.Project(slug)
		if !ok || !slices.Contains([]string{"active", "paused", "done"}, p.Status) ||
			slices.ContainsFunc(out, func(x EarlierProject) bool { return x.Slug == p.Slug }) {
			continue
		}
		out = append(out, EarlierProject{Slug: p.Slug, Name: p.Name, Status: p.Status})
	}
	return out
}

// meetingTerms is what ranks earlier meetings for a question asked in this one.
type meetingTerms struct {
	question   map[string]bool
	titles     []string // this meeting's titles, folded (the user's, the calendar hint's)
	titleTerms map[string]bool
	attendees  []string // folded
}

// score ranks an earlier meeting: the question's terms its minutes or projects hold count most, then the same title
// as this meeting, the title's terms, and the attendees both have. Recency breaks ties (earlierMeetings).
func (t meetingTerms) score(m *EarlierMeeting) int {
	var text strings.Builder
	add := func(s ...string) {
		for _, x := range s {
			text.WriteString(x)
			text.WriteByte('\n')
		}
	}
	mm := &m.Minutes
	add(mm.Title)
	add(mm.Attendees...)
	for _, p := range mm.Points {
		add(p.Text, p.Who)
	}
	for _, d := range mm.Decisions {
		add(d.Text)
	}
	for _, a := range mm.Actions {
		add(a.Who, a.What, a.By)
	}
	for _, o := range mm.Open {
		add(o.Text)
	}
	for _, p := range m.Projects {
		add(p.Name)
	}
	has := termsOf(text.String())
	score := 0
	for w := range t.question {
		if has[w] {
			score += 3
		}
	}
	title := fold(strings.TrimSpace(mm.Title))
	if title != "" && slices.Contains(t.titles, title) {
		score += 8
	}
	titleHas := termsOf(mm.Title)
	for w := range t.titleTerms {
		if titleHas[w] {
			score += 2
		}
	}
	for _, a := range mm.Attendees {
		if slices.Contains(t.attendees, fold(a)) {
			score += 2
		}
	}
	return score
}

// termsOf is the terms of s for ranking, folded: words of letters and digits (two runes or more, a few function
// words left out) and, for scripts written without spaces (Han, kana, Hangul), each pair of adjacent characters (a
// lone one by itself).
func termsOf(s string) map[string]bool {
	out := map[string]bool{}
	var word, run []rune
	flushWord := func() {
		if len(word) >= 2 && !stopTerms[string(word)] {
			out[string(word)] = true
		}
		word = word[:0]
	}
	flushRun := func() {
		if len(run) == 1 {
			out[string(run)] = true
		}
		for i := 0; i+1 < len(run); i++ {
			out[string(run[i:i+2])] = true
		}
		run = run[:0]
	}
	for _, c := range fold(s) {
		switch {
		case unicode.In(c, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			flushWord()
			run = append(run, c)
		case unicode.IsLetter(c) || unicode.IsDigit(c):
			flushRun()
			word = append(word, c)
		default:
			flushWord()
			flushRun()
		}
	}
	flushWord()
	flushRun()
	return out
}

// stopTerms are words too common to rank by.
var stopTerms = map[string]bool{
	"the": true, "and": true, "for": true, "what": true, "did": true, "does": true, "about": true, "was": true,
	"were": true, "who": true, "how": true, "when": true, "with": true, "that": true, "this": true, "our": true,
	"you": true, "are": true, "is": true, "of": true, "to": true, "in": true, "on": true, "we": true, "it": true,
	"do": true, "an": true, "at": true, "be": true, "by": true, "or": true, "as": true, "last": true, "time": true,
}
