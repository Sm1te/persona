package kb

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Meeting minutes (docs/assistant/meeting-minutes.md): a complete recording that is a meeting (the phone said so, the
// user asked, or it overlaps a calendar item of the knowledge base) gets minutes.json (Minutes, what detail.minutes
// is) and minutes.md (the same as Markdown, what the searches and kb_get read) in its directory, so a delete takes them
// with it; the retention keeps them past the transcript, until minutes_days (recording_trim.go). The transcript goes to
// the model as numbered lines; the model cites line numbers, which become seconds here, and an item citing no line it
// read is dropped. Long transcripts are read in chunks whose notes one more call merges.

// Minutes is minutes.json, and detail.minutes of GET /api/v1/kb/recordings/{id}.
type Minutes struct {
	Version     int             `json:"version"`  // counts the minutes written for this recording
	Revision    int64           `json:"revision"` // the recording revision they were made from
	GeneratedAt time.Time       `json:"generated_at"`
	Title       string          `json:"title"`
	When        string          `json:"when"` // Start–End in one fixed format, for older clients and the Markdown
	Where       string          `json:"where,omitempty"`
	Attendees   []string        `json:"attendees,omitempty"`
	Points      []MinutesPoint  `json:"points,omitempty"`
	Decisions   []MinutesItem   `json:"decisions,omitempty"`
	Actions     []MinutesAction `json:"actions,omitempty"`
	Open        []MinutesItem   `json:"open,omitempty"`
	Partial     bool            `json:"partial,omitempty"` // the transcript's end was not read
	Markdown    string          `json:"markdown"`
	// Start and End are the recording's span When says, in the user's zone (#4713). Clients format them with their own
	// localized dates and show When only without them (minutes from an older hub).
	Start time.Time `json:"start,omitzero"`
	End   time.Time `json:"end,omitzero"`
}

// MinutesItem is a decision or an open question; At is seconds from the recording's start (a segment's start).
type MinutesItem struct {
	Text string  `json:"text"`
	At   float64 `json:"at"`
}

// MinutesPoint is a point discussed; Who only when the transcript or the calendar names the person.
type MinutesPoint struct {
	Text string  `json:"text"`
	At   float64 `json:"at"`
	Who  string  `json:"who,omitempty"`
}

// MinutesAction is an action item: a suggested to-do until the user confirms it (TodoID then).
type MinutesAction struct {
	ID      string  `json:"id"`
	Who     string  `json:"who,omitempty"`
	What    string  `json:"what"`
	By      string  `json:"by,omitempty"`       // the deadline as said
	DueDate string  `json:"due_date,omitempty"` // YYYY-MM-DD, when By is one date
	At      float64 `json:"at"`
	TodoID  string  `json:"todo_id,omitempty"`
}

var (
	// ErrMinutesStale: the minutes were written again since the version a request names.
	ErrMinutesStale = errors.New("the minutes changed since; read them again")
	// ErrRecordingIncomplete: the transcript is not complete yet, so there is nothing to make minutes of.
	ErrRecordingIncomplete = errors.New("the transcript is not complete yet")
	// errMinutesUnusable: the model answered, but nothing in it cites the transcript.
	errMinutesUnusable = errors.New("no item of the answer cites a transcript line")
)

const (
	minutesTries         = 3      // unusable answers for one revision before the minutes are failed
	minutesMinCalendar   = 5 * 60 // seconds: shorter recordings are not meetings just because a calendar item overlaps
	minutesCalendarAhead = 15 * time.Minute
	// minutesMatchWait: a recording no calendar item matched is looked at again until this long after it ended, the
	// calendar items being collected every half hour (a meeting's video call is listed once it has been held).
	minutesMatchWait   = time.Hour
	minutesOneCall     = 16000 // runes of transcript read in one call; longer ones go in chunks
	minutesChunk       = 12000
	minutesMaxChunks   = 40
	minutesCallTimeout = 6 * time.Minute  // a call's at the first try, twice a summary's (minutes_timeout.go)
	minutesJobTimeout  = 45 * time.Minute // all the calls of one recording: the knowledge-base round waits for them
	minutesMaxItems    = 100
	minutesTextRunes   = 400
	minutesLineRunes   = 4000 // a transcript line longer than this is cut (one segment, not the whole meeting)
	// What the merging call reads of all the parts' notes: per section, at most this many items over all parts (an
	// equal share each, at least 2), each at most minutesNoteRunes.
	minutesMergeItems = 60
	minutesNoteRunes  = 160
)

// isMeeting: the recording gets minutes.
func (r *Recording) isMeeting() bool {
	return r.Kind == RecordingMeeting || r.MinutesAsked || r.Meeting != nil && r.Duration >= minutesMinCalendar
}

// planMinutes sets MinutesState from the rest: pending until minutes of the current revision are written, failed after
// minutesTries unusable answers for it or minutesTimeoutTries tries that ran out of time; minutes of an older revision
// keep showing meanwhile. A meeting that completed with no text gets none ("", docs/assistant/meeting-minutes.md
// "Attendance fields"): an attended one is uploaded before anything was said, and may end so.
func (r *Recording) planMinutes() {
	switch {
	case !r.isMeeting():
		r.MinutesState = "" // minutes made while it was one stay readable; nothing more is made
	case r.Trimmed():
		r.MinutesState = MinutesReady // the transcript is gone: the minutes kept are all there will be
	case r.Complete && r.Chars == 0:
		r.MinutesState = ""
	case r.MinutesRev > 0 && r.MinutesRev >= r.Revision:
		r.MinutesState = MinutesReady
	case r.MinutesFailRev == r.Revision && (r.MinutesFails >= minutesTries || r.MinutesTimeouts >= minutesTimeoutTries):
		r.MinutesState = MinutesFailed
	default:
		r.MinutesState = MinutesPending
	}
}

// ReadMinutes is recording id's minutes, ErrNotFound when it has none (or there is no such recording).
func (s *Store) ReadMinutes(id string) (*Minutes, error) {
	id, ok := RecordingID(id)
	if !ok {
		return nil, ErrNotFound
	}
	raw, err := os.ReadFile(s.path(KindRecording, id, "minutes.json"))
	if err != nil {
		return nil, ErrNotFound
	}
	var m Minutes
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// putMinutes writes minutes.json and minutes.md of recording id, unless it was deleted meanwhile (false then). The
// caller holds recMu.
func (s *Store) putMinutes(id string, m *Minutes) (bool, error) {
	if s.readOnly {
		return false, ErrReadOnly
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return false, err
	}
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if err := s.recoverRecordingLocked(id); err != nil {
		return false, err
	}
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	// writeFile makes missing directories: a recording deleted meanwhile must stay gone.
	if _, err := os.Stat(s.path(KindRecording, id, "meta.json")); err != nil {
		return false, nil
	}
	if err := writeFile(s.path(KindRecording, id, "minutes.json"), append(raw, '\n')); err != nil {
		return false, err
	}
	if err := writeFile(s.path(KindRecording, id, "minutes.md"), []byte(strings.TrimSpace(m.Markdown)+"\n")); err != nil {
		return false, err
	}
	return true, nil
}

// AskMinutes has recording id's minutes made (again) at the next round, whatever its kind: ErrNotFound when there is
// no such recording, ErrTranscriptExpired once it was trimmed, ErrRecordingIncomplete before its transcript is
// complete, and also for a complete one with no text (409 as well: there is nothing to make minutes of, and its
// minutes_state stays absent).
func (e *Engine) AskMinutes(rawID string) error {
	if e.Store.ReadOnly() {
		return ErrReadOnly
	}
	id, ok := RecordingID(rawID)
	if !ok {
		return ErrNotFound
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok {
		return ErrNotFound
	}
	if r.Trimmed() {
		return ErrTranscriptExpired
	}
	if !r.Complete || r.Chars == 0 {
		return ErrRecordingIncomplete
	}
	r.MinutesAsked, r.MinutesRev, r.MatchedRev = true, 0, 0
	r.clearMinutesFailures()
	r.MinutesAsks++
	r.planMinutes()
	return st.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// SetMinutesTodos records the to-dos made of actions (action id → to-do id) in version of recording id's minutes;
// ErrNotFound without minutes. Minutes written again since get them by task instead (byTask: MinutesTaskKey → to-do
// id), on their actions without one; without byTask that is ErrMinutesStale.
func (e *Engine) SetMinutesTodos(rawID string, version int, todos, byTask map[string]string) (*Minutes, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	m, err := st.ReadMinutes(id)
	if err != nil {
		return nil, err
	}
	changed := false
	switch {
	case m.Version == version:
		for i := range m.Actions {
			if t, ok := todos[m.Actions[i].ID]; ok && t != "" && m.Actions[i].TodoID != t {
				m.Actions[i].TodoID, changed = t, true
			}
		}
	case byTask == nil:
		return m, ErrMinutesStale
	default:
		for i, a := range m.Actions {
			if t := byTask[MinutesTaskKey(a)]; t != "" && a.TodoID == "" {
				m.Actions[i].TodoID, changed = t, true
			}
		}
	}
	if !changed {
		return m, nil
	}
	if ok, err := st.putMinutes(id, m); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound
	}
	st.notify(KindRecording, id, "summary")
	return m, nil
}

// WriteMinutes matches complete recordings to the calendar and writes the minutes of up to max meetings that need
// them, newest first; how many it wrote. With no model, or the model paused or unreachable, they stay pending. One
// whose last try ran out of time waits for its next (minutes_timeout.go).
func (e *Engine) WriteMinutes(ctx context.Context, max int) int {
	if e.Store.ReadOnly() {
		return 0
	}
	now := time.Now()
	e.matchRecordings(now)
	if e.Model == nil && e.ModelFor == nil {
		return 0
	}
	var todo []*Recording
	for _, r := range e.Store.Recordings() {
		if r.Complete && r.Chars > 0 && r.MinutesState == MinutesPending && !r.minutesWaiting(now) && !r.Trimmed() {
			todo = append(todo, r)
		}
	}
	sort.Slice(todo, func(i, j int) bool { return todo[i].StartedAt.After(todo[j].StartedAt) })
	n := 0
	for i, r := range todo {
		if i >= max || ctx.Err() != nil { // at most max tried a round, written or not
			break
		}
		jctx, cancel := context.WithTimeout(ctx, minutesJobTimeout)
		err := e.writeMinutes(jctx, r.ID)
		cancel()
		if err == nil {
			n++
			continue
		}
		e.logf("recording %s: minutes: %v", r.ID, err)
		switch {
		case timedOut(ctx, err):
			// Slow, not wrong: tried again at a later round with more time, the others first meanwhile.
			e.minutesTimedOut(r.ID, r.Revision, time.Now())
		case unusableAnswer(err):
			e.minutesFailed(r.ID, r.Revision)
		default:
			return n // the model is paused or unreachable: the others would fail alike; still pending next round
		}
	}
	return n
}

// unusableAnswer: the model answered but the answer can't make minutes (counted towards failed); anything else (no
// connection, paused, an HTTP error) leaves them pending.
func unusableAnswer(err error) bool {
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.Is(err, errMinutesUnusable) || errors.Is(err, ai.ErrNoObject) || errors.Is(err, ai.ErrUnterminated) || errors.Is(err, ai.ErrTruncated) ||
		errors.Is(err, ai.ErrRefused) || errors.As(err, &syntax) || errors.As(err, &typ)
}

// minutesFailed counts an unusable answer for revision rev of recording id.
func (e *Engine) minutesFailed(id string, rev int64) {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok || r.Revision != rev {
		return
	}
	r.minutesFailuresFor(rev)
	r.MinutesFails++
	r.planMinutes()
	_ = st.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// matchRecordings links each complete recording not matched at its current revision to the calendar item it overlaps;
// one that none overlaps is looked at again until minutesMatchWait after it ended.
func (e *Engine) matchRecordings(now time.Time) {
	var due []string
	for _, r := range e.Store.Recordings() {
		if r.Complete && r.MatchedRev < r.Revision && !r.Trimmed() {
			due = append(due, r.ID)
		}
	}
	if len(due) == 0 {
		return
	}
	events, meetings := e.Store.Items(KindEvent), e.Store.Items(KindMeeting)
	st := e.Store
	for _, id := range due {
		st.recMu.Lock()
		r, ok := st.Recording(id)
		if ok && r.Complete && r.MatchedRev < r.Revision && !r.Trimmed() {
			if old := r.Meeting; old != nil {
				r.Items = removeStrings(r.Items, []string{old.Kind + "/" + old.ID})
			}
			it := matchCalendar(r, events, meetings)
			if it == nil && r.Meeting == nil && now.Sub(recordingEnd(r)) < minutesMatchWait {
				st.recMu.Unlock()
				continue // nothing yet: maybe at the next collection of calendar items
			}
			r.Meeting = nil
			if it != nil {
				r.Meeting = &MeetingRef{Kind: it.Kind, ID: it.ID, Name: oneLine(it.Title)}
				r.Items = addUnique(r.Items, it.Kind+"/"+it.ID)
			}
			r.MatchedRev = r.Revision
			r.Name = st.recordingName(r)
			r.planMinutes()
			if st.putEntity(KindRecording, r.ID, r, &r.Tagging, false) == nil {
				e.index(KindRecording, r.ID, r.Name, r.Tagging, r.OneLine, r.StartedAt)
			}
		}
		st.recMu.Unlock()
	}
}

// matchCalendar is the calendar item r overlaps: one starting at most minutesCalendarAhead before r and before r
// ends, the closest start winning, an event (the scheduled one) before a meeting (the video call held).
func matchCalendar(r *Recording, kinds ...[]*Item) *Item {
	start, end := r.StartedAt, recordingEnd(r)
	for _, items := range kinds {
		var best *Item
		var gap time.Duration
		for _, it := range items {
			if it.At.IsZero() || strings.TrimSpace(it.Title) == "" || it.At.Before(start.Add(-minutesCalendarAhead)) || !it.At.Before(end) {
				continue
			}
			g := it.At.Sub(start)
			if g < 0 {
				g = -g
			}
			if best == nil || g < gap {
				best, gap = it, g
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

// recordingEnd is when r ended: ended_at, else its start plus what was recorded (at least a minute).
func recordingEnd(r *Recording) time.Time {
	end := r.StartedAt.Add(time.Duration(r.Duration * float64(time.Second)))
	if r.EndedAt != nil && r.EndedAt.After(end) {
		end = *r.EndedAt
	}
	if !end.After(r.StartedAt.Add(time.Minute)) {
		end = r.StartedAt.Add(time.Minute)
	}
	return end
}

// minutesLine is a transcript line as the model reads it: N is what it cites.
type minutesLine struct {
	N    int
	At   float64
	Text string
}

// minutesAnswer is the model's minutes (kb/minutes, kb/minutes_merge): items cite transcript lines by number.
type minutesAnswer struct {
	Title     string   `json:"title"`
	Where     string   `json:"where"`
	Attendees []string `json:"attendees"`
	Points    []struct {
		Text string    `json:"text"`
		Line modelLine `json:"line"`
		Who  string    `json:"who"`
	} `json:"points"`
	Decisions []struct {
		Text string    `json:"text"`
		Line modelLine `json:"line"`
	} `json:"decisions"`
	Actions []struct {
		Who     string    `json:"who"`
		What    string    `json:"what"`
		By      string    `json:"by"`
		DueDate string    `json:"due_date"`
		Line    modelLine `json:"line"`
	} `json:"actions"`
	Open []struct {
		Text string    `json:"text"`
		Line modelLine `json:"line"`
	} `json:"open"`
}

// modelLine is a cited line number as models write it: 12, "12", "[12]" or "L12"; anything else is 0 (no line).
type modelLine int

func (l *modelLine) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	text := string(data)
	if len(data) > 0 && data[0] == '"' {
		if json.Unmarshal(data, &text) != nil {
			return nil
		}
	}
	text = strings.Trim(strings.TrimSpace(text), "[]#Ll ")
	if f, err := strconv.ParseFloat(text, 64); err == nil && f == float64(int(f)) && f > 0 {
		*l = modelLine(f)
	}
	return nil
}

// writeMinutes makes and stores the minutes of recording id from what is committed now.
func (e *Engine) writeMinutes(ctx context.Context, id string) error {
	files, err := e.Store.recordingSnapshot(id)
	if err != nil {
		return err
	}
	if files["meta.json"] == nil {
		return nil // deleted
	}
	var r Recording
	if err := json.Unmarshal(files["meta.json"], &r); err != nil {
		return err
	}
	if !r.Complete || r.Chars == 0 || r.MinutesState != MinutesPending || r.Trimmed() {
		return nil
	}
	var segs []RecordingSegment
	if err := json.Unmarshal(files["segments.json"], &segs); err != nil {
		return err
	}
	zone := e.Store.Zone()
	lines := make([]minutesLine, 0, len(segs))
	for i, sg := range segs {
		if t := strings.Join(strings.Fields(sg.Text), " "); t != "" {
			lines = append(lines, minutesLine{N: i + 1, At: sg.Start, Text: clip(t, minutesLineRunes)})
		}
	}
	chunks, partial := chunkLines(lines, minutesOneCall, minutesChunk, minutesMaxChunks)
	if len(chunks) == 0 {
		return nil
	}
	in := e.minutesInput(&r, zone)
	model := e.modelFor("kb.compress")
	callTime := minutesCallTime(r.minutesTimeouts())
	call := func(system, user string, out *minutesAnswer) error {
		cctx, cancel := context.WithTimeout(ctx, callTime)
		defer cancel()
		return model.JSONMax(cctx, system, user, 6000, out)
	}
	system := prompts.RenderKBMinutes(prompts.KBOwner{Owner: e.ownerName()})
	var read []minutesLine
	var ans minutesAnswer
	if len(chunks) == 1 {
		read = chunks[0]
		in.Part, in.Parts, in.Transcript = 1, 1, renderMinutesLines(chunks[0], r.StartedAt, zone)
		if err := call(system, prompts.RenderKBMinutesInput(in), &ans); err != nil {
			return err
		}
	} else {
		merge := prompts.KBMinutesMergeInput{Meeting: in}
		for i, c := range chunks {
			in.Part, in.Parts, in.Transcript = i+1, len(chunks), renderMinutesLines(c, r.StartedAt, zone)
			var notes minutesAnswer
			if err := call(system, prompts.RenderKBMinutesInput(in), &notes); err != nil {
				return fmt.Errorf("part %d of %d: %w", i+1, len(chunks), err)
			}
			read = append(read, c...)
			merge.Parts = append(merge.Parts, notesForMerge(i+1, notes, c, r.StartedAt, zone, max(2, minutesMergeItems/len(chunks))))
		}
		merge.Meeting.Transcript = ""
		err := call(prompts.RenderKBMinutesMerge(prompts.KBOwner{Owner: e.ownerName()}), prompts.RenderKBMinutesMergeInput(merge), &ans)
		if err != nil {
			return fmt.Errorf("merging %d parts: %w", len(chunks), err)
		}
		read = notedLines(read, merge.Parts) // the merging call saw the notes, not the transcript
	}
	m, err := e.buildMinutes(&r, ans, read, zone)
	if err != nil {
		return err
	}
	m.Partial = partial
	return e.storeMinutes(ctx, &r, m)
}

// minutesInput is what the model is told about recording r besides its transcript.
func (e *Engine) minutesInput(r *Recording, zone *time.Location) prompts.KBMinutesInput {
	in := prompts.KBMinutesInput{Pinned: r.Pinned, Title: cmp.Or(r.Title, calendarTitle(r)), Start: r.StartedAt.In(zone),
		End: recordingEnd(r).In(zone), Weekday: weekdayZH[r.StartedAt.In(zone).Weekday()], Minutes: int(r.Duration / 60),
		Language: r.Language}
	if r.Meeting != nil {
		in.Meeting = r.Meeting.Name
	}
	if c := r.Calendar; c != nil {
		in.Attendees, in.Location = c.Attendees, c.Location
	}
	return in
}

var weekdayZH = [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// calendarTitle is the phone calendar's title of r, else the matched calendar item's ("" without either).
func calendarTitle(r *Recording) string {
	if r.Calendar != nil && r.Calendar.Title != "" {
		return r.Calendar.Title
	}
	if r.Meeting != nil {
		return r.Meeting.Name
	}
	return ""
}

// chunkLines cuts lines into chunks of about size runes at line boundaries: one chunk when all of them fit in one,
// at most max chunks (partial: lines were left out).
func chunkLines(lines []minutesLine, one, size, max int) (chunks [][]minutesLine, partial bool) {
	total := 0
	for _, l := range lines {
		total += len([]rune(l.Text)) + 16
	}
	if total <= one {
		if len(lines) == 0 {
			return nil, false
		}
		return [][]minutesLine{lines}, false
	}
	var cur []minutesLine
	n := 0
	for i, l := range lines {
		w := len([]rune(l.Text)) + 16
		if n > 0 && n+w > size {
			chunks = append(chunks, cur)
			cur, n = nil, 0
			if len(chunks) == max {
				return chunks, i < len(lines)
			}
		}
		cur = append(cur, l)
		n += w
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks, false
}

// renderMinutesLines is the transcript as the model reads it: "[N] HH:MM:SS text" per line in the user's zone.
func renderMinutesLines(lines []minutesLine, start time.Time, zone *time.Location) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "[%d] %s %s\n", l.N, clockAt(start, l.At, zone), l.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

func clockAt(start time.Time, sec float64, zone *time.Location) string {
	return start.Add(time.Duration(sec * float64(time.Second))).In(zone).Format("15:04:05")
}

// notesForMerge is a chunk's notes for the merging call, keeping only items citing a line of that chunk, at most each
// of a section, each clipped to minutesNoteRunes.
func notesForMerge(part int, a minutesAnswer, chunk []minutesLine, start time.Time, zone *time.Location, each int) prompts.KBMinutesPart {
	in := map[int]bool{}
	for _, l := range chunk {
		in[l.N] = true
	}
	p := prompts.KBMinutesPart{Part: part, From: clockAt(start, chunk[0].At, zone), To: clockAt(start, chunk[len(chunk)-1].At, zone),
		Where: oneLine(a.Where), Attendees: a.Attendees}
	note := func(text string, line modelLine, extra ...string) (prompts.KBMinutesNote, bool) {
		text = clipRunes(text, minutesNoteRunes)
		if text == "" || !in[int(line)] {
			return prompts.KBMinutesNote{}, false
		}
		n := prompts.KBMinutesNote{Line: int(line), Text: text}
		if len(extra) > 0 {
			n.Who = oneLine(extra[0])
		}
		if len(extra) > 2 {
			n.By, n.DueDate = oneLine(extra[1]), strings.TrimSpace(extra[2])
		}
		return n, true
	}
	for _, x := range a.Points {
		if n, ok := note(x.Text, x.Line, x.Who); ok {
			p.Points = append(p.Points, n)
		}
	}
	for _, x := range a.Decisions {
		if n, ok := note(x.Text, x.Line); ok {
			p.Decisions = append(p.Decisions, n)
		}
	}
	for _, x := range a.Actions {
		if n, ok := note(x.What, x.Line, x.Who, x.By, x.DueDate); ok {
			p.Actions = append(p.Actions, n)
		}
	}
	for _, x := range a.Open {
		if n, ok := note(x.Text, x.Line); ok {
			p.Open = append(p.Open, n)
		}
	}
	p.Points, p.Decisions, p.Actions, p.Open = firstN(p.Points, each), firstN(p.Decisions, each), firstN(p.Actions, each), firstN(p.Open, each)
	return p
}

// notedLines is the lines of read the parts' notes cite.
func notedLines(read []minutesLine, parts []prompts.KBMinutesPart) []minutesLine {
	cited := map[int]bool{}
	for _, p := range parts {
		for _, list := range [][]prompts.KBMinutesNote{p.Points, p.Decisions, p.Actions, p.Open} {
			for _, n := range list {
				cited[n.Line] = true
			}
		}
	}
	var out []minutesLine
	for _, l := range read {
		if cited[l.N] {
			out = append(out, l)
		}
	}
	return out
}

func firstN[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clipText(s string) string { return clipRunes(s, minutesTextRunes) }

// clipRunes is s on one line, at most n runes ("…" marking a cut).
func clipRunes(s string, n int) string { return clip(strings.Join(strings.Fields(s), " "), n) }

// buildMinutes checks the model's answer against the lines it read: every item must cite one of them, a person must be
// named in them or in the calendar, a due date must be a real day from the meeting on (within a year).
func (e *Engine) buildMinutes(r *Recording, a minutesAnswer, read []minutesLine, zone *time.Location) (*Minutes, error) {
	at := map[int]float64{}
	var saidB strings.Builder
	for _, l := range read {
		at[l.N] = l.At
		saidB.WriteString(l.Text)
		saidB.WriteByte('\n')
	}
	raw := saidB.String()
	said := strings.ToLower(raw)
	var calendar []string
	if r.Calendar != nil {
		calendar = r.Calendar.Attendees
	}
	named := func(who string) string {
		who = oneLine(who)
		if who == "" {
			return ""
		}
		for _, c := range calendar {
			if strings.EqualFold(c, who) {
				return c
			}
		}
		if spoken(raw, who) {
			return who
		}
		return ""
	}
	cited, dropped := 0, 0
	line := func(l modelLine) (float64, bool) {
		sec, ok := at[int(l)]
		if ok {
			cited++
		} else {
			dropped++
		}
		return sec, ok
	}
	m := &Minutes{Title: cmp.Or(r.Title, calendarTitle(r), oneLine(a.Title), r.Name), When: minutesWhen(r, zone)}
	m.Start, m.End = minutesSpan(r, zone)
	if r.Calendar != nil && r.Calendar.Location != "" {
		m.Where = r.Calendar.Location
	} else if w := oneLine(a.Where); w != "" && strings.Contains(said, strings.ToLower(w)) {
		m.Where = w
	}
	m.Attendees = append(m.Attendees, calendar...)
	for _, p := range a.Attendees {
		if n := named(p); n != "" && !slices.Contains(m.Attendees, n) {
			m.Attendees = append(m.Attendees, n)
		}
	}
	seen := map[string]bool{}
	fresh := func(section, text string) bool {
		k := section + "\x00" + text
		if text == "" || seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	// The citation first: an item citing nothing read must not hide a later copy of it that does.
	for _, x := range a.Points {
		if t := clipText(x.Text); t != "" {
			if sec, ok := line(x.Line); ok && fresh("p", t) {
				m.Points = append(m.Points, MinutesPoint{Text: t, At: sec, Who: named(x.Who)})
			}
		}
	}
	for _, x := range a.Decisions {
		if t := clipText(x.Text); t != "" {
			if sec, ok := line(x.Line); ok && fresh("d", t) {
				m.Decisions = append(m.Decisions, MinutesItem{Text: t, At: sec})
			}
		}
	}
	day := r.StartedAt.In(zone).Format("2006-01-02")
	for _, x := range a.Actions {
		if t := clipText(x.What); t != "" {
			// The same task for two people is two actions.
			if sec, ok := line(x.Line); ok && fresh("a"+oneLine(x.Who), t) {
				by := oneLine(x.By)
				due := ""
				if by != "" { // a date only for a deadline someone said
					due = dueDate(x.DueDate, day)
				}
				m.Actions = append(m.Actions, MinutesAction{Who: named(x.Who), What: t, By: by, DueDate: due, At: sec})
			}
		}
	}
	for _, x := range a.Open {
		if t := clipText(x.Text); t != "" {
			if sec, ok := line(x.Line); ok && fresh("o", t) {
				m.Open = append(m.Open, MinutesItem{Text: t, At: sec})
			}
		}
	}
	// Nothing that cites the transcript (an empty or unrelated answer) is not minutes.
	if cited == 0 {
		return nil, errMinutesUnusable
	}
	sort.SliceStable(m.Points, func(i, j int) bool { return m.Points[i].At < m.Points[j].At })
	sort.SliceStable(m.Decisions, func(i, j int) bool { return m.Decisions[i].At < m.Decisions[j].At })
	sort.SliceStable(m.Actions, func(i, j int) bool { return m.Actions[i].At < m.Actions[j].At })
	sort.SliceStable(m.Open, func(i, j int) bool { return m.Open[i].At < m.Open[j].At })
	m.Points, m.Decisions, m.Actions, m.Open = capItems(m.Points), capItems(m.Decisions), capItems(m.Actions), capItems(m.Open)
	for i := range m.Actions {
		m.Actions[i].ID = "a" + strconv.Itoa(i+1)
	}
	return m, nil
}

func capItems[T any](s []T) []T {
	if len(s) > minutesMaxItems {
		return s[:minutesMaxItems]
	}
	return s
}

// dueDate is s when it is a YYYY-MM-DD day from day (the meeting's, YYYY-MM-DD) to a year after it, else "". Days are
// compared as calendar dates (UTC midnights), so a time zone's missing midnight can't move one.
func dueDate(s string, day string) string {
	d, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	from, err2 := time.Parse("2006-01-02", day)
	if err != nil || err2 != nil || d.Before(from) || d.After(from.AddDate(1, 0, 0)) {
		return ""
	}
	return d.Format("2006-01-02")
}

// spoken: name is said in text. A name in Latin letters must be a whole word written as given ("Ann" is not in
// "planning", "Will" is not "will"); any other (Chinese and the like, which have no word breaks) is found as it is.
func spoken(text, name string) bool {
	latin := true
	for _, c := range name {
		if c > unicode.MaxASCII {
			latin = false
			break
		}
	}
	if !latin {
		return strings.Contains(strings.ToLower(text), strings.ToLower(name))
	}
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		if !wordByte(text, start-1) && !wordByte(text, end) {
			return true
		}
		i = start + 1
	}
}

// wordByte: text[i] is an ASCII letter or digit (false outside text).
func wordByte(text string, i int) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	c := text[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// minutesSpan is the recording's start and end in the user's zone, to the second: the span minutes are about.
func minutesSpan(r *Recording, zone *time.Location) (time.Time, time.Time) {
	return r.StartedAt.In(zone).Truncate(time.Second), recordingEnd(r).In(zone).Truncate(time.Second)
}

// spanOf fills Start and End of minutes written before they were (#4713) from their recording.
func (m *Minutes) spanOf(r *Recording, zone *time.Location) {
	if m.Start.IsZero() && r != nil && !r.StartedAt.IsZero() {
		m.Start, m.End = minutesSpan(r, zone)
	}
}

// minutesWithSpan is minutes.json as a recording's detail sends it: minutes written before Start and End were get them
// from the recording (spanOf), every other field as stored.
func minutesWithSpan(raw []byte, r *Recording, zone *time.Location) []byte {
	var stored struct {
		Start time.Time `json:"start"`
	}
	if json.Unmarshal(raw, &stored) != nil || !stored.Start.IsZero() || r == nil || r.StartedAt.IsZero() {
		return raw
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw
	}
	start, end := minutesSpan(r, zone)
	fields["start"], _ = json.Marshal(start)
	fields["end"], _ = json.Marshal(end)
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

// minutesWhen is "2006-01-02 15:04–16:10" in the user's zone (the end's date too when it is another day).
func minutesWhen(r *Recording, zone *time.Location) string {
	start, end := r.StartedAt.In(zone), recordingEnd(r).In(zone)
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return start.Format("2006-01-02 15:04") + "–" + end.Format("15:04")
	}
	return start.Format("2006-01-02 15:04") + "–" + end.Format("2006-01-02 15:04")
}

// keepTodos carries the to-dos made of the previous minutes' actions over to the same task for the same person in m,
// so minutes made again don't offer what the user already added.
func keepTodos(m, old *Minutes) {
	made := map[string]string{}
	for _, a := range old.Actions {
		if a.TodoID != "" {
			made[MinutesTaskKey(a)] = a.TodoID
		}
	}
	for i, a := range m.Actions {
		if t, ok := made[MinutesTaskKey(a)]; ok {
			m.Actions[i].TodoID = t
		}
	}
}

// MinutesTaskKey is what makes an action the same task in minutes made again: who and what.
func MinutesTaskKey(a MinutesAction) string { return a.Who + "\x00" + a.What }

// renderMinutes is minutes.md: the minutes with clock times, sections without anything left out.
func renderMinutes(m *Minutes, start time.Time, zone *time.Location) string {
	at := func(sec float64) string { return "[" + clockAt(start, sec, zone) + "] " }
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n- 时间：%s\n", m.Title, m.When)
	if m.Where != "" {
		fmt.Fprintf(&b, "- 地点：%s\n", m.Where)
	}
	if len(m.Attendees) > 0 {
		fmt.Fprintf(&b, "- 参会：%s\n", strings.Join(m.Attendees, "、"))
	}
	if len(m.Points) > 0 {
		b.WriteString("\n## 讨论要点\n\n")
		for _, p := range m.Points {
			b.WriteString("- " + at(p.At) + p.Text)
			if p.Who != "" {
				b.WriteString("（" + p.Who + "）")
			}
			b.WriteByte('\n')
		}
	}
	section := func(title string, items []MinutesItem) {
		if len(items) == 0 {
			return
		}
		b.WriteString("\n## " + title + "\n\n")
		for _, x := range items {
			b.WriteString("- " + at(x.At) + x.Text + "\n")
		}
	}
	section("结论与决定", m.Decisions)
	if len(m.Actions) > 0 {
		b.WriteString("\n## 待办\n\n")
		for _, a := range m.Actions {
			b.WriteString("- " + at(a.At))
			if a.Who != "" {
				b.WriteString(a.Who + "：")
			}
			b.WriteString(a.What)
			if by := cmp.Or(a.By, a.DueDate); by != "" {
				b.WriteString("（" + by + "）")
			}
			b.WriteByte('\n')
		}
	}
	section("未决问题", m.Open)
	if m.Partial {
		b.WriteString("\n> 转写太长，结尾部分没有整理进纪要。\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// storeMinutes writes m for snapshot r (the revision read) unless the recording was deleted meanwhile, then tells
// MinutesWritten once they are the current revision's.
func (e *Engine) storeMinutes(ctx context.Context, r *Recording, m *Minutes) error {
	st := e.Store
	st.recMu.Lock()
	cur, ok := st.Recording(r.ID)
	if !ok {
		st.recMu.Unlock()
		return nil
	}
	// The version after both meta's and the file's: a write of minutes.json that meta.json never caught up with (a
	// crash between the two) must not give different actions the version an app may still confirm.
	m.Version, m.Revision, m.GeneratedAt = cur.MinutesVersion+1, r.Revision, time.Now().UTC()
	if old, err := st.ReadMinutes(cur.ID); err == nil {
		m.Version = max(m.Version, old.Version+1)
		keepTodos(m, old)
	}
	m.Markdown = renderMinutes(m, r.StartedAt, st.Zone())
	if wrote, err := st.putMinutes(cur.ID, m); err != nil || !wrote {
		st.recMu.Unlock()
		return err
	}
	cur.MinutesVersion = m.Version
	cur.clearMinutesFailures()
	if cur.MinutesAsks == r.MinutesAsks { // else the user asked again meanwhile: made again at the next round
		cur.MinutesRev = r.Revision
	}
	cur.planMinutes()
	err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false)
	st.notify(KindRecording, cur.ID, "summary")
	ready := cur.MinutesState == MinutesReady
	snap := *cur
	st.recMu.Unlock()
	if err != nil {
		return err
	}
	if ready && e.MinutesWritten != nil {
		e.MinutesWritten(ctx, snap, *m)
	}
	return nil
}
