package kb

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// Writing the daily recap (docs/assistant/day-recap.md "How the hub writes the recap"), the recap lane's work
// (WriteDayRecaps). A day recording's lines (its segments with text, by start, up to coverage.to) are cut into chunks
// of the minutes' size that never cross an hour of its clock; the model notes each finished chunk once (kb/day_recap)
// and the notes are kept by the hash of the chunk's lines in recap_notes.json, so text arriving every minute costs a
// call per hour, a late sentence a call for its chunk again, and a PUT that left a chunk's lines alone none. When a
// recap is due (RecapTarget), the chunks not noted yet are noted and one more call merges the notes of at most
// recapMaxChunks of them, with the day's known meetings as hints (kb/day_recap_merge). Lines are numbered by position
// (RecordingSegment.N) and an item citing no line the notes cite is dropped, as in minutes.

const (
	recapMaxChunks    = 60
	recapKeepFirst    = 20               // of the chunks a long day keeps, its first; the rest are its newest
	recapBefore       = 30 * time.Minute // the recap time is this long before the evening routine's
	recapCutoff       = 6 * time.Hour    // after the end of its day, a recording not complete gets its final recap
	recapEveningHour  = 21               // the evening routine's time when DayRecapClock is nil
	recapEveningMin   = 30               //
	recapNoteEntries  = 12               // what a chunk's notes keep
	recapNotePoints   = 8                // of an entry
	recapNoteActions  = 12               //
	recapMergePoints  = 300              // what the merging call reads of all the chunks' notes
	recapMergeActions = 100              //
	recapMaxEntries   = 100              // of a recap's timeline
	recapEntryPoints  = 20               // of an entry
	recapTitleRunes   = 60
	recapSummaryRunes = 600
	recapNoteSummary  = 200
	recapMaxMeetings  = 30
	recapQuiet        = 30 * time.Minute // the cutoff waits until no upload came for this long
	recapMeetingNear  = 15 * time.Minute // how far from an entry a calendar item the model named may start
	dayPushRetry      = time.Minute      // the wait after a push DayRecapWritten did not take, doubled up to 16 minutes
	dayPushGiveUp     = 24 * time.Hour   // after it became due, a push not taken yet is given up on
	dayPushBudget     = time.Minute      // what a round spends on telling pushes: the rest wait for the next one
)

// errRecapStale: the day recording a recap job read was deleted, trimmed or given another base since; the job stops,
// keeps nothing it made, and its next round reads the recording again.
var errRecapStale = errors.New("the recording changed under its recap: deleted, trimmed or replaced")

// WriteDayRecaps is the recap lane's round: it makes the recaps that became due due (and the day's push at its recap
// time), tells DayRecapWritten of the pushes it was not told of yet (a restart in between included, one it failed
// after its wait), mends the files of a recap still due (mendRecapFiles), then works on up to max day recordings (a
// due recap first, then the newest): notes of their finished chunks, and the recap when one is due. How many it
// called the model for. With no model, or the model paused or unreachable, the work stays for a later round.
func (e *Engine) WriteDayRecaps(ctx context.Context, max int) int {
	if e.Store.ReadOnly() {
		return 0
	}
	now := e.now()
	st := e.Store
	var days []string // every round lists the recordings once: a day recording is read again by id
	for _, r := range st.Recordings() {
		if r.Kind == RecordingDay {
			days = append(days, r.ID)
		}
	}
	e.armDayRecaps(days, now)
	e.tellDayRecaps(ctx, days)
	e.mendDayRecaps(days)
	if e.Model == nil && e.ModelFor == nil {
		return 0
	}
	type job struct {
		r   *Recording
		due bool
	}
	var todo []job
	for _, id := range days {
		r, ok := st.Recording(id)
		if !ok || r.Kind != RecordingDay || r.Trimmed() {
			continue
		}
		due := r.RecapDue != nil && !r.RecapDue.failed() && !r.RecapDue.waiting(now)
		if due || r.Chars > 0 && r.RecapNotedText < r.TextRev {
			todo = append(todo, job{r, due})
		}
	}
	sort.SliceStable(todo, func(i, j int) bool {
		if todo[i].due != todo[j].due {
			return todo[i].due
		}
		return todo[i].r.StartedAt.After(todo[j].r.StartedAt)
	})
	n := 0
	for _, j := range todo {
		if n >= max || ctx.Err() != nil {
			break
		}
		called, err := e.writeDayRecap(ctx, j.r.ID, now)
		if called {
			n++
		}
		switch {
		case errors.Is(err, errRecapStale):
			continue // read again at the next round
		case err != nil:
			e.logf("recording %s: recap: %v", j.r.ID, err)
			return n // the model is paused or unreachable: the others would fail alike
		}
	}
	return n
}

// mendDayRecaps has the files of every recap of day recordings ids that is due again say what its recap.json does
// (mendRecapFiles): a PUT that withdrew from it and failed half way is not tried again before the recap is rewritten.
func (e *Engine) mendDayRecaps(ids []string) {
	st := e.Store
	for _, id := range ids {
		if r, ok := st.Recording(id); !ok || r.Kind != RecordingDay || r.RecapDue == nil {
			continue
		}
		st.recMu.Lock()
		if r, ok := st.Recording(id); ok && r.Kind == RecordingDay && r.RecapDue != nil {
			if err := st.mendRecapFiles(r); err != nil {
				e.logf("recording %s: recap files: %v", id, err)
			}
		}
		st.recMu.Unlock()
	}
}

// recapTime is the recap time of the owner's day (YYYY-MM-DD) in the owner's zone: recapBefore the evening routine's.
func (e *Engine) recapTime(day string) time.Time {
	h, m := recapEveningHour, recapEveningMin
	if e.DayRecapClock != nil {
		if hh, mm := e.DayRecapClock(); hh >= 0 && hh < 24 && mm >= 0 && mm < 60 {
			h, m = hh, mm
		}
	}
	zone := e.Store.Zone()
	d, err := time.ParseInLocation(time.DateOnly, day, zone)
	if err != nil {
		return time.Time{}
	}
	return time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, zone).Add(-recapBefore)
}

// dayStart is the start of day (YYYY-MM-DD) in zone, and of the day after.
func dayStart(day string, zone *time.Location) (time.Time, time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, day, zone)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return d, time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, zone), true
}

// daySpan is when day recording r was recorded, as far as the hub knows: from its start to where its coverage
// reaches, or its end; one still open (no ended_at) goes on until now, within its day (the watch cuts it at midnight
// in its zone).
func (e *Engine) daySpan(r *Recording, now time.Time) (time.Time, time.Time) {
	end := r.StartedAt.Add(seconds(r.coverageTo()))
	switch {
	case r.EndedAt != nil:
		end = later(end, *r.EndedAt)
	case !r.Complete:
		if _, midnight, ok := dayStart(r.Day, e.Store.RecordingZone(r)); ok {
			end = later(end, earlier(now, midnight))
		}
	}
	return r.StartedAt, end
}

// recordedWithin: day recording r's span as of now (daySpan) overlaps from–to.
func (e *Engine) recordedWithin(r *Recording, now, from, to time.Time) bool {
	start, end := e.daySpan(r, now)
	return start.Before(to) && (end.After(from) || !start.Before(from))
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// armDayRecaps makes due the recaps that became due by now (docs/assistant/day-recap.md "When a recap is due"), each
// once and persisted with the recording: the final one of a recording that became complete, or 6 hours after its day
// ended without (once no upload came for recapQuiet: a day uploaded long after it is recapped whole); and at the recap
// time of the owner's day, one for each recording recorded that day with text its newest recap did not read
// (TextRev), in the same write as the day's push (dayPushAtRecapTime).
func (e *Engine) armDayRecaps(ids []string, now time.Time) {
	owner := e.Store.Zone()
	today := now.In(owner).Format(time.DateOnly)
	todayStart, tomorrow, _ := dayStart(today, owner)
	evening := !now.Before(e.recapTime(today))
	tonight := func(r *Recording) bool { return evening && e.recordedWithin(r, now, todayStart, tomorrow) }
	st := e.Store
	for _, id := range ids {
		r, ok := st.Recording(id)
		if !ok || r.Kind != RecordingDay || r.Trimmed() {
			continue
		}
		if !e.recapsArmed(r, now, today, tonight(r)) { // r is this loop's own copy: a look at what would change
			continue
		}
		st.recMu.Lock()
		cur, ok := st.Recording(r.ID)
		if ok && cur.Kind == RecordingDay && !cur.Trimmed() {
			at := cur.RecapEvening
			if e.recapsArmed(cur, now, today, tonight(cur)) {
				if at != cur.RecapEvening {
					st.dayPushAtRecapTime(cur, now)
				}
				if err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false); err != nil {
					e.logf("recording %s: recap due: %v", cur.ID, err)
				}
			}
		}
		st.recMu.Unlock()
	}
}

// recapsArmed makes due on r what became due by now (see armDayRecaps; tonight: it was recorded on the owner's today
// and its recap time has come); false when nothing did.
func (e *Engine) recapsArmed(r *Recording, now time.Time, today string, tonight bool) bool {
	armed := false
	if tonight && r.RecapEvening != today {
		r.RecapEvening, armed = today, true
		if r.Chars > 0 && (r.RecapVersion == 0 || r.RecapText < r.TextRev) {
			r.armRecap(RecapDueEvening, now)
		}
	}
	if r.Chars == 0 {
		return armed // no text when a recap comes due: no recap
	}
	switch {
	case r.Complete && !r.RecapComplete:
		r.RecapComplete = true
		r.armRecap(RecapDueComplete, now)
		armed = true
	case !r.Complete && !r.RecapCutoff && !now.Before(e.dayCutoff(r)) && !now.Before(r.Updated.Add(recapQuiet)):
		r.RecapCutoff = true
		r.armRecap(RecapDueCutoff, now)
		armed = true
	}
	return armed
}

// dayCutoff is when day recording r gets its final recap without being complete: recapCutoff after its day ended,
// in its zone.
func (e *Engine) dayCutoff(r *Recording) time.Time {
	_, end, ok := dayStart(r.Day, e.Store.RecordingZone(r))
	if !ok {
		return r.StartedAt.Add(24*time.Hour + recapCutoff)
	}
	return end.Add(recapCutoff)
}

// recapLines are day recording r's lines as the model reads them: its segments with text by start, numbered by
// position, up to how far its text reaches (to: coverage.to; without a coverage, the end of the last segment).
func recapLines(r *Recording, segs []RecordingSegment) (lines []minutesLine, to float64) {
	to = r.coverageTo()
	segs = slices.Clone(segs)
	sortDaySegments(segs)
	if r.Coverage == nil {
		for _, sg := range segs {
			to = max(to, sg.Start, sg.End)
		}
	}
	for i, sg := range segs {
		t := strings.Join(strings.Fields(sg.Text), " ")
		if t == "" || sg.Start > to {
			continue // lines past coverage.to wait for it
		}
		lines = append(lines, minutesLine{N: cmp.Or(sg.N, i+1), At: sg.Start, Text: clip(t, minutesLineRunes)})
	}
	return lines, to
}

// dayChunk is a chunk of a day's lines: within one hour of its clock (hour), at most minutesChunk runes. Finished:
// coverage.to passed its hour, or it is full (the hour goes on in another chunk), and no held gap (heldGaps: audio the
// watch still holds) lies in its hour. End is where it ends, in seconds since started_at (its hour's end, or to when
// sooner; a full one's where the next begins); Hash identifies its lines (positions, times and texts).
type dayChunk struct {
	lines    []minutesLine
	hour     time.Time
	end      float64
	finished bool
	hash     string
}

// dayChunks cuts lines (by start) of a day recording started at start into chunks, coverage reaching to, held its
// gaps whose text is still to come (heldGaps).
func dayChunks(lines []minutesLine, start time.Time, zone *time.Location, to float64, held []CoverageGap) []dayChunk {
	var out []dayChunk
	n := 0
	for _, l := range lines {
		h := hourStart(start.Add(seconds(l.At)), zone)
		w := len([]rune(l.Text)) + 16
		last := len(out) - 1
		switch {
		case last < 0 || !h.Equal(out[last].hour):
			out, n = append(out, dayChunk{hour: h}), 0
		case n > 0 && n+w > minutesChunk:
			out[last].finished = true // full
			out, n = append(out, dayChunk{hour: h}), 0
		}
		out[len(out)-1].lines = append(out[len(out)-1].lines, l)
		n += w
	}
	reached := start.Add(seconds(to))
	for i := range out {
		c := &out[i]
		end := c.hour.Add(time.Hour)
		from, until := c.hour.Sub(start).Seconds(), end.Sub(start).Seconds()
		c.finished = (c.finished || !reached.Before(end)) &&
			!slices.ContainsFunc(held, func(g CoverageGap) bool { return g.From < until && g.To > from })
		c.end = min(to, until)
		if i+1 < len(out) && out[i+1].hour.Equal(c.hour) {
			c.end = out[i+1].lines[0].At
		}
		h := sha256.New()
		for _, l := range c.lines {
			fmt.Fprintf(h, "%d\x00%s\x00%s\n", l.N, strconv.FormatFloat(l.At, 'g', -1, 64), l.Text)
		}
		c.hash = hex.EncodeToString(h.Sum(nil)[:16])
	}
	return out
}

// heldGaps are day recording r's gaps whose text is still to come as of now: its pending ones (the watch still holds
// the audio), which keep the chunks of their hours unfinished, so an hour a backlog drains into is noted once it
// settled rather than at every sentence. None past its cutoff: what came by then is what there is.
func (e *Engine) heldGaps(r *Recording, now time.Time) []CoverageGap {
	if r.Coverage == nil || r.Complete || !now.Before(e.dayCutoff(r)) {
		return nil
	}
	var held []CoverageGap
	for _, g := range r.Coverage.Gaps {
		if g.Reason == GapPending {
			held = append(held, g)
		}
	}
	return held
}

// selectChunks is the chunks a recap reads: all of them, or past recapMaxChunks the day's first recapKeepFirst and
// its newest, the middle of the day left out (partial).
func selectChunks(chunks []dayChunk) ([]dayChunk, bool) {
	if len(chunks) <= recapMaxChunks {
		return chunks, false
	}
	keep := append(slices.Clone(chunks[:recapKeepFirst]), chunks[len(chunks)-(recapMaxChunks-recapKeepFirst):]...)
	return keep, true
}

// recapNotes is recap_notes.json: each chunk's notes by the hash of its lines, and the tries that failed (counted for
// the base they were made on); Base is the base it was last written for.
type recapNotes struct {
	Base   int64            `json:"base"`
	Chunks []recapChunkNote `json:"chunks"`
}

// recapChunkNote is one chunk's notes (Noted), or its tries so far: Fails unusable answers and Timeouts tries that
// ran out of time on base Base, the next not before RetryAt; given up after the minutes' tries (left out, partial).
type recapChunkNote struct {
	Hash     string           `json:"hash"`
	FromN    int              `json:"from_n"`
	ToN      int              `json:"to_n"`
	Noted    bool             `json:"noted,omitempty"`
	Summary  string           `json:"summary,omitempty"`
	Entries  []recapNoteEntry `json:"entries,omitempty"`
	Actions  []recapNoteItem  `json:"actions,omitempty"`
	Base     int64            `json:"base,omitempty"`
	Fails    int              `json:"fails,omitempty"`
	Timeouts int              `json:"timeouts,omitempty"`
	RetryAt  time.Time        `json:"retry_at,omitzero"`
}

// recapNoteEntry is a candidate timeline entry of a chunk's notes.
type recapNoteEntry struct {
	FromLine int             `json:"from_line"`
	ToLine   int             `json:"to_line"`
	Kind     string          `json:"kind"`
	Title    string          `json:"title"`
	Points   []recapNoteItem `json:"points"`
}

// recapNoteItem is a point (Text, Who) or a candidate action (Text is what; Who, By and DueDate as said), citing Line.
type recapNoteItem struct {
	Line    int    `json:"line"`
	Text    string `json:"text"`
	Who     string `json:"who,omitempty"`
	By      string `json:"by,omitempty"`
	DueDate string `json:"due_date,omitempty"`
}

// tries is how many tries of the chunk on base failed, and how many ran out of time.
func (c *recapChunkNote) tries(base int64) (fails, timeouts int) {
	if c == nil || c.Base != base {
		return 0, 0
	}
	return c.Fails, c.Timeouts
}

// gaveUp: the model could not note the chunk on base after the minutes' tries.
func (c *recapChunkNote) gaveUp(base int64) bool {
	f, t := c.tries(base)
	return f >= minutesTries || t >= minutesTimeoutTries
}

func (c *recapChunkNote) waiting(base int64, now time.Time) bool {
	return c != nil && c.Base == base && now.Before(c.RetryAt)
}

func (n *recapNotes) chunk(hash string) *recapChunkNote {
	for i := range n.Chunks {
		if n.Chunks[i].Hash == hash {
			return &n.Chunks[i]
		}
	}
	return nil
}

// readRecapNotes is recording id's recap_notes.json; empty without one (or one that cannot be read: noted again).
func (s *Store) readRecapNotes(id string) recapNotes {
	n, _, _ := s.recapNotesFile(id)
	return n
}

// recapNotesFile is recording id's recap_notes.json, and whether there is one: empty when it is not JSON, an error
// when it is there but could not be read (what it holds is not known).
func (s *Store) recapNotesFile(id string) (recapNotes, bool, error) {
	var n recapNotes
	raw, err := os.ReadFile(s.path(KindRecording, id, recapNotesFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return n, false, nil
	case err != nil:
		return n, true, err
	}
	_ = json.Unmarshal(raw, &n)
	return n, true, nil
}

// putRecapNotes writes n as recording id's recap_notes.json (none when it has no chunk left), unless the recording
// was deleted meanwhile. The caller holds recMu.
func (s *Store) putRecapNotes(id string, n recapNotes) error {
	raw, err := json.Marshal(n)
	if err != nil {
		return err
	}
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	path := s.path(KindRecording, id, recapNotesFile)
	if len(n.Chunks) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if _, err := os.Stat(s.path(KindRecording, id, "meta.json")); err != nil {
		return nil // writeFile makes missing directories: a recording deleted meanwhile must stay gone
	}
	return writeFile(path, append(raw, '\n'))
}

// keepRecapNotes writes recording id's notes again with only the chunks of keep (its chunks now) when they hold
// others: notes are kept per chunk while its lines are unchanged, and those of lines changed may quote what is gone
// (an erase). Notes that are there but cannot be read are an error: what they quote is not known, so whatever
// changed the lines (a PUT) fails with them, and is sent again. The caller holds recMu.
func (s *Store) keepRecapNotes(id string, keep []dayChunk) error {
	n, there, err := s.recapNotesFile(id)
	if err != nil {
		return fmt.Errorf("the recap's notes: %w", err)
	}
	if !there {
		return nil
	}
	current := map[string]bool{}
	for _, c := range keep {
		current[c.hash] = true
	}
	kept := slices.DeleteFunc(slices.Clone(n.Chunks), func(c recapChunkNote) bool { return !current[c.Hash] })
	if len(kept) == len(n.Chunks) && len(kept) > 0 {
		return nil
	}
	n.Chunks = kept // one that cannot be read keeps nothing either
	return s.putRecapNotes(id, n)
}

// pruneRecapNotes keeps of day recording r's notes those of its chunks as segs (cleaned, by position) cut them:
// what a PUT does to them, under recMu (which the caller holds) before it commits.
func (s *Store) pruneRecapNotes(r *Recording, segs []RecordingSegment) error {
	lines, to := recapLines(r, segs)
	return s.keepRecapNotes(r.ID, dayChunks(lines, r.StartedAt, s.RecordingZone(r), to, nil))
}

// retryRecapNotes forgets the tries of recording id's chunks not noted (asked again: they are tried afresh). The
// caller holds recMu.
func (s *Store) retryRecapNotes(id string) error {
	n := s.readRecapNotes(id) // none that can be read: none to forget
	k := len(n.Chunks)
	if n.Chunks = slices.DeleteFunc(n.Chunks, func(c recapChunkNote) bool { return !c.Noted }); len(n.Chunks) == k {
		return nil
	}
	return s.putRecapNotes(id, n)
}

// updateRecapNotes changes recording id's notes with fn and writes them, keeping only the chunks of keep (the
// recording's chunks now), only while it still has base and is neither deleted nor trimmed (errRecapStale
// otherwise); the notes as written.
func (e *Engine) updateRecapNotes(id string, base int64, keep []dayChunk, fn func(n *recapNotes)) (recapNotes, error) {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if !st.recapCurrent(id, base) {
		return recapNotes{}, errRecapStale
	}
	n := st.readRecapNotes(id)
	fn(&n)
	current := map[string]bool{}
	for _, c := range keep {
		current[c.hash] = true
	}
	n.Base = base
	n.Chunks = slices.DeleteFunc(n.Chunks, func(c recapChunkNote) bool { return !current[c.Hash] })
	return n, st.putRecapNotes(id, n)
}

// recapCurrent: day recording id is still there with base, and not trimmed: what a recap job read of it holds. The
// caller holds recMu.
func (s *Store) recapCurrent(id string, base int64) bool {
	cur, ok := s.Recording(id)
	return ok && cur.Kind == RecordingDay && !cur.Trimmed() && cur.Base == base
}

// recapStale: day recording id changed since a job read it on base (recapCurrent, under recMu).
func (e *Engine) recapStale(id string, base int64) bool {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	return !st.recapCurrent(id, base)
}

// dayAnswer is the model's answer: a chunk's notes (kb/day_recap: Entries) or the day's recap (kb/day_recap_merge:
// Timeline, an entry's Meeting the number of a known meeting or 0), items citing lines by number.
type dayAnswer struct {
	Summary  string           `json:"summary"`
	Entries  []dayAnswerEntry `json:"entries"`
	Timeline []dayAnswerEntry `json:"timeline"`
	Actions  []struct {
		Who     string    `json:"who"`
		What    string    `json:"what"`
		By      string    `json:"by"`
		DueDate string    `json:"due_date"`
		Line    modelLine `json:"line"`
	} `json:"actions"`
}

type dayAnswerEntry struct {
	FromLine modelLine `json:"from_line"`
	ToLine   modelLine `json:"to_line"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Meeting  modelLine `json:"meeting"`
	Points   []struct {
		Text string    `json:"text"`
		Line modelLine `json:"line"`
		Who  string    `json:"who"`
	} `json:"points"`
}

// entryKind is a timeline entry's kind as the model wrote it: meeting, or else outside.
func entryKind(k string) string {
	if strings.EqualFold(strings.TrimSpace(k), RecapMeeting) {
		return RecapMeeting
	}
	return RecapOutside
}

// chunkNotes is the model's notes of chunk c as they are kept: only items citing a line of c, clipped.
func chunkNotes(a dayAnswer, c dayChunk) recapChunkNote {
	at := map[int]float64{}
	for _, l := range c.lines {
		at[l.N] = l.At
	}
	note := recapChunkNote{Hash: c.hash, FromN: c.lines[0].N, ToN: c.lines[len(c.lines)-1].N, Noted: true,
		Summary: clipRunes(a.Summary, recapNoteSummary)}
	for _, x := range a.Entries {
		en := recapNoteEntry{Kind: entryKind(x.Kind), Title: clipRunes(x.Title, recapTitleRunes)}
		first, last := 0, 0
		for _, p := range x.Points {
			t := clipRunes(p.Text, minutesNoteRunes)
			sec, ok := at[int(p.Line)]
			if t == "" || !ok {
				continue
			}
			en.Points = append(en.Points, recapNoteItem{Line: int(p.Line), Text: t, Who: oneLine(p.Who)})
			if first == 0 || sec < at[first] {
				first = int(p.Line)
			}
			if last == 0 || sec > at[last] {
				last = int(p.Line)
			}
		}
		if len(en.Points) == 0 {
			continue
		}
		en.Points = firstN(en.Points, recapNotePoints)
		en.FromLine, en.ToLine = first, last
		if _, ok := at[int(x.FromLine)]; ok && at[int(x.FromLine)] <= at[first] {
			en.FromLine = int(x.FromLine)
		}
		if _, ok := at[int(x.ToLine)]; ok && at[int(x.ToLine)] >= at[last] {
			en.ToLine = int(x.ToLine)
		}
		note.Entries = append(note.Entries, en)
	}
	note.Entries = firstN(note.Entries, recapNoteEntries)
	for _, x := range a.Actions {
		if t := clipRunes(x.What, minutesNoteRunes); t != "" {
			if _, ok := at[int(x.Line)]; ok {
				note.Actions = append(note.Actions, recapNoteItem{Line: int(x.Line), Text: t, Who: oneLine(x.Who),
					By: oneLine(x.By), DueDate: strings.TrimSpace(x.DueDate)})
			}
		}
	}
	note.Actions = firstN(note.Actions, recapNoteActions)
	return note
}

// dayMeeting is a meeting the hub knows of on a day recording's day (a hint for the merge): one of the user's meeting
// recordings overlapping it (Kind recordings, From and To its span), or a meetings / events item starting that day
// (To zero).
type dayMeeting struct {
	Ref      MeetingRef
	From, To time.Time
}

// dayMeetings are day recording r's known meetings as of now, by start, at most recapMaxMeetings.
func (e *Engine) dayMeetings(r *Recording, zone *time.Location, now time.Time) []dayMeeting {
	var out []dayMeeting
	from, to := e.daySpan(r, now)
	for _, o := range e.Store.Recordings() {
		if o.ID == r.ID || !o.isMeeting() || o.StartedAt.After(to) || !recordingEnd(o).After(from) {
			continue
		}
		out = append(out, dayMeeting{Ref: MeetingRef{Kind: KindRecording, ID: o.ID, Name: o.Name}, From: o.StartedAt,
			To: recordingEnd(o)})
	}
	if start, end, ok := dayStart(r.Day, zone); ok {
		for _, kind := range []string{KindMeeting, KindEvent} {
			for _, it := range e.Store.Items(kind) {
				if t := oneLine(it.Title); t != "" && !it.At.Before(start) && it.At.Before(end) {
					out = append(out, dayMeeting{Ref: MeetingRef{Kind: kind, ID: it.ID, Name: t}, From: it.At})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return firstN(out, recapMaxMeetings)
}

// dayRecapDay is what the model is told of day recording r besides its text.
func (e *Engine) dayRecapDay(r *Recording, zone *time.Location) prompts.KBDayRecapDay {
	d := prompts.KBDayRecapDay{Pinned: r.Pinned, Title: r.Title, Day: r.Day, Zone: zone.String(), Language: r.Language}
	if t, err := time.ParseInLocation(time.DateOnly, r.Day, zone); err == nil {
		d.Weekday = weekdayZH[t.Weekday()]
	}
	if r.Part > 1 {
		d.Part = r.Part
	}
	return d
}

// renderDayLines is a chunk's lines as the model reads them: "[N] HH:MM text".
func renderDayLines(lines []minutesLine, start time.Time, zone *time.Location) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "[%d] %s %s\n", l.N, start.Add(seconds(l.At)).In(zone).Format("15:04"), l.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeDayRecap notes the finished chunks of day recording id that have no notes yet, from what is committed now, and
// writes its recap when one is due (as of now). called: the model was asked. An error is one that leaves the work for
// a later round (the model paused or unreachable), or errRecapStale: the recording was deleted, trimmed or given
// another base meanwhile, which is looked at before every call, so nothing it no longer has is sent to the model. An
// unusable answer or a call that ran out of its own time is counted here; the job running out of its time counts
// nothing (the rest at the next round). A recap due keeps a call's time of the job for the merge: noting stops when
// less is left, and the merge reads what was noted (partial).
func (e *Engine) writeDayRecap(ctx context.Context, id string, now time.Time) (called bool, err error) {
	files, err := e.Store.recordingSnapshot(id)
	if err != nil || files["meta.json"] == nil {
		return false, err
	}
	var r Recording
	if err := json.Unmarshal(files["meta.json"], &r); err != nil {
		return false, err
	}
	if r.Kind != RecordingDay || r.Trimmed() {
		return false, nil
	}
	var segs []RecordingSegment
	if raw := files["segments.json"]; raw != nil {
		if err := json.Unmarshal(raw, &segs); err != nil {
			return false, err
		}
	}
	jctx, cancel := context.WithTimeout(ctx, minutesJobTimeout)
	defer cancel()
	// A call ran out of its own time, while the round and the job go on.
	ranOut := func(err error) bool {
		return errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && jctx.Err() == nil
	}
	jobOver := func(err error) bool { return errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil }
	st := e.Store
	zone := st.RecordingZone(&r)
	lines, to := recapLines(&r, segs)
	chunks := dayChunks(lines, r.StartedAt, zone, to, e.heldGaps(&r, now))
	target := r.RecapDue
	due := target != nil && !target.failed() && !target.waiting(now)
	if due && len(lines) == 0 {
		return false, e.settleRecap(id, target.N) // no text to recap: none
	}
	sel, partial := selectChunks(chunks)
	notes := st.readRecapNotes(id)
	model := e.modelFor("kb.compress")
	owner := prompts.KBOwner{Owner: e.ownerName()}
	day := e.dayRecapDay(&r, zone)
	settled := true // every finished chunk read has its notes, or was given up
	short := false  // the job's time left is the merge's: the chunks not noted yet are left out of it
	for _, c := range sel {
		cn := notes.chunk(c.hash)
		switch {
		case !c.finished && !due: // the hour in progress: noted only for a recap
			continue
		case cn != nil && (cn.Noted || cn.gaveUp(r.Base)):
			continue
		case cn.waiting(r.Base, now):
			settled = false
			continue
		case jctx.Err() != nil:
			return called, nil // the job's time is up: the rest at the next round
		}
		_, timeouts := cn.tries(r.Base)
		need := minutesCallTime(timeouts)
		if due {
			need += minutesCallTime(target.Timeouts)
		}
		if called && e.timeLeft(jctx) < need { // the first call of a job always has its time
			settled = false
			if !due {
				return called, nil // the job's time is up: the rest at the next round
			}
			short = true
			break
		}
		if e.recapStale(id, r.Base) {
			return called, errRecapStale
		}
		called = true
		in := prompts.KBDayRecapInput{Day: day, From: recapClock(r.StartedAt, c.lines[0].At, zone),
			To: recapClock(r.StartedAt, c.lines[len(c.lines)-1].At, zone), Transcript: renderDayLines(c.lines, r.StartedAt, zone)}
		var ans dayAnswer
		cctx, ccancel := context.WithTimeout(jctx, minutesCallTime(timeouts))
		err := model.JSONMax(cctx, prompts.RenderKBDayRecap(owner), prompts.RenderKBDayRecapInput(in), 6000, &ans)
		ccancel()
		switch {
		case err == nil:
			note := chunkNotes(ans, c)
			// The notes as written replace the ones read: a write refused (errRecapStale) ends the job, so the notes are
			// never read back empty and chunks noted already sent again.
			if notes, err = e.updateRecapNotes(id, r.Base, chunks, func(n *recapNotes) {
				if old := n.chunk(c.hash); old != nil {
					*old = note
					return
				}
				n.Chunks = append(n.Chunks, note)
			}); err != nil {
				return called, err
			}
		case ranOut(err) || unusableAnswer(err):
			settled = false
			timedOut := ranOut(err)
			e.logf("recording %s: recap notes of %s: %v", id, in.From, err)
			if notes, err = e.recapNotesFailed(id, r.Base, chunks, c, timedOut, now); err != nil {
				return called, err
			}
			if timedOut && due {
				// Slow, not wrong: the recap waits with the chunk, counted against it too, so a model that never
				// finishes one still can't keep it pending for ever.
				e.recapFailed(id, target.N, true, now)
				return called, nil
			}
		case jobOver(err):
			return called, nil // the job's time is up: the rest at the next round, nothing counted
		default:
			return called, err
		}
	}
	if settled {
		if err := e.recapNoted(id, r.Base, r.TextRev, chunks); err != nil {
			return called, err
		}
	}
	if !due {
		return called, nil
	}
	// The merge reads every chunk's notes; one still waiting holds it back, one given up (or not noted for want of
	// time) is left out.
	var read []minutesLine
	var kept []recapChunkNote
	for _, c := range sel {
		cn := notes.chunk(c.hash)
		if cn == nil || !cn.Noted {
			switch {
			case cn != nil && cn.gaveUp(r.Base):
			case short && !cn.waiting(r.Base, now):
			default:
				return called, nil
			}
			partial = true
			continue
		}
		read = append(read, c.lines...)
		kept = append(kept, *cn)
	}
	if len(kept) == 0 {
		if short {
			return called, nil // nothing noted yet for want of time: the next round has the job's whole time
		}
		// Nothing could be noted (every chunk given up): the model failed at this recap.
		e.recapFailed(id, target.N, false, now)
		return called, nil
	}
	if e.recapStale(id, r.Base) {
		return called, errRecapStale
	}
	meetings := e.dayMeetings(&r, zone, now)
	merge := prompts.KBDayRecapMergeInput{Day: day, Partial: partial}
	for i, m := range meetings {
		h := prompts.KBDayMeeting{N: i + 1, Kind: m.Ref.Kind, Title: m.Ref.Name, From: m.From.In(zone)}
		if !m.To.IsZero() {
			h.To = m.To.In(zone)
		}
		merge.Meetings = append(merge.Meetings, h)
	}
	points, actions := max(3, recapMergePoints/len(kept)), max(2, recapMergeActions/len(kept))
	noted, bounds := map[int]bool{}, map[int]bool{} // lines the notes' items cite, and their entries' spans
	for _, cn := range kept {
		part := notesForDay(cn, points, actions)
		c := findChunk(sel, cn.Hash)
		part.From, part.To = recapClock(r.StartedAt, c.lines[0].At, zone), recapClock(r.StartedAt, c.lines[len(c.lines)-1].At, zone)
		for _, en := range part.Entries {
			bounds[en.FromLine], bounds[en.ToLine] = true, true
			for _, p := range en.Points {
				noted[p.Line] = true
			}
		}
		for _, a := range part.Actions {
			noted[a.Line] = true
		}
		merge.Chunks = append(merge.Chunks, part)
	}
	called = true
	var ans dayAnswer
	cctx, ccancel := context.WithTimeout(jctx, minutesCallTime(target.Timeouts))
	err = model.JSONMax(cctx, prompts.RenderKBDayRecapMerge(owner), prompts.RenderKBDayRecapMergeInput(merge), 8000, &ans)
	ccancel()
	switch {
	case err == nil:
	case ranOut(err):
		e.logf("recording %s: recap: %v", id, err)
		e.recapFailed(id, target.N, true, now)
		return called, nil
	case jobOver(err):
		return called, nil // the job's time is up: the merge at the next round, nothing counted
	case unusableAnswer(err):
		e.logf("recording %s: recap: %v", id, err)
		e.recapFailed(id, target.N, false, now)
		return called, nil
	default:
		return called, err
	}
	coversTo := findChunk(sel, kept[len(kept)-1].Hash).end // the end of the last chunk read
	rc, err := e.buildDayRecap(&r, ans, read, noted, bounds, meetings, coversTo)
	if err != nil {
		e.logf("recording %s: recap: %v", id, err)
		e.recapFailed(id, target.N, false, now)
		return called, nil
	}
	rc.Partial = partial
	if r.Complete {
		rc.Final, rc.FinalReason = true, FinalComplete
	} else if target.Reason == RecapDueCutoff || !now.Before(e.dayCutoff(&r)) {
		rc.Final, rc.FinalReason = true, FinalCutoff
	}
	text := r.TextRev // what it read all of; with chunks left out for want of time, newer text is still to read
	if short {
		text = 0
	}
	return called, e.storeDayRecap(ctx, &r, target, rc, recapRead{text: text, chunks: chunks}, now)
}

// timeLeft is how long ctx has before its deadline (e.jobLeft in tests).
func (e *Engine) timeLeft(ctx context.Context) time.Duration {
	if e.jobLeft != nil {
		return e.jobLeft(ctx)
	}
	if d, ok := ctx.Deadline(); ok {
		return time.Until(d)
	}
	return math.MaxInt64
}

func findChunk(chunks []dayChunk, hash string) dayChunk {
	for _, c := range chunks {
		if c.hash == hash {
			return c
		}
	}
	return dayChunk{}
}

func recapClock(start time.Time, sec float64, zone *time.Location) string {
	return start.Add(seconds(sec)).In(zone).Format("15:04")
}

// notesForDay is a chunk's notes for the merging call: at most points points over its entries (an entry with none
// left out) and actions actions.
func notesForDay(cn recapChunkNote, points, actions int) prompts.KBDayRecapChunk {
	part := prompts.KBDayRecapChunk{Summary: cn.Summary}
	left := points
	for _, en := range cn.Entries {
		if left <= 0 {
			break
		}
		e := prompts.KBDayRecapEntry{FromLine: en.FromLine, ToLine: en.ToLine, Kind: en.Kind, Title: en.Title}
		for _, p := range firstN(en.Points, left) {
			e.Points = append(e.Points, prompts.KBMinutesNote{Line: p.Line, Text: p.Text, Who: p.Who})
		}
		left -= len(e.Points)
		part.Entries = append(part.Entries, e)
	}
	for _, a := range firstN(cn.Actions, actions) {
		part.Actions = append(part.Actions, prompts.KBMinutesNote{Line: a.Line, Text: a.Text, Who: a.Who, By: a.By, DueDate: a.DueDate})
	}
	return part
}

// buildDayRecap checks the model's recap against the lines read: every point and action must cite a line a point or
// action of the notes cites (noted), a person must be named in what was read, a due date must be a real day from the
// recording's day on (within a year). An entry's span is its first and last lines' when the notes have them (noted or
// an entry's bounds), and always takes in its points; one in a meeting recording's span is that meeting, else the
// known meeting the model named by number when it is there at that time (meetingNear), else a calendar item starting
// in it; a meeting named that is not keeps the kind the model judged, without it. The same action said again within
// recapTodoNear is one; said again later in the day it is another. An answer that cites nothing and says nothing is
// unusable; one with only a summary is a recap of a day with nothing to note.
func (e *Engine) buildDayRecap(r *Recording, a dayAnswer, read []minutesLine, noted, bounds map[int]bool,
	meetings []dayMeeting, coversTo float64) (*DayRecap, error) {
	at := map[int]float64{}
	var saidB strings.Builder
	for _, l := range read {
		at[l.N] = l.At
		saidB.WriteString(l.Text)
		saidB.WriteByte('\n')
	}
	raw := saidB.String()
	named := func(who string) string {
		if who = oneLine(who); who != "" && spoken(raw, who) {
			return who
		}
		return ""
	}
	cited := 0
	line := func(l modelLine) (float64, bool) {
		sec, ok := at[int(l)]
		if !ok || !noted[int(l)] {
			return 0, false
		}
		cited++
		return sec, true
	}
	rc := &DayRecap{Day: r.Day, Covers: RecapSpan{From: 0, To: coversTo}, Summary: clipRunes(a.Summary, recapSummaryRunes)}
	if r.Coverage != nil {
		rc.Gaps = gapsWithin(r.Coverage.Gaps, 0, coversTo)
	}
	for _, x := range a.Timeline {
		en := DayRecapEntry{Kind: entryKind(x.Kind), Title: clipRunes(x.Title, recapTitleRunes)}
		seen := map[string]bool{}
		for _, p := range x.Points {
			t := clipText(p.Text)
			if t == "" || seen[t] {
				continue
			}
			if sec, ok := line(p.Line); ok {
				seen[t] = true
				en.Points = append(en.Points, MinutesPoint{Text: t, At: sec, Who: named(p.Who)})
			}
		}
		if len(en.Points) == 0 {
			continue
		}
		sort.SliceStable(en.Points, func(i, j int) bool { return en.Points[i].At < en.Points[j].At })
		en.Points = firstN(en.Points, recapEntryPoints)
		en.From, en.To = en.Points[0].At, en.Points[len(en.Points)-1].At
		span := func(l modelLine) (float64, bool) {
			sec, ok := at[int(l)]
			return sec, ok && (noted[int(l)] || bounds[int(l)])
		}
		if sec, ok := span(x.FromLine); ok && sec < en.From {
			en.From = sec
		}
		if sec, ok := span(x.ToLine); ok && sec > en.To {
			en.To = sec
		}
		// Which known meeting it is: the meeting recording it lies in, else the one the model named, else a calendar
		// item starting in it.
		from, to := r.StartedAt.Add(seconds(en.From)), r.StartedAt.Add(seconds(en.To))
		mid := from.Add(to.Sub(from) / 2)
		inRecording := slices.IndexFunc(meetings, func(m dayMeeting) bool {
			return m.Ref.Kind == KindRecording && !mid.Before(m.From) && !mid.After(m.To)
		})
		startsIn := slices.IndexFunc(meetings, func(m dayMeeting) bool {
			return m.Ref.Kind != KindRecording && !m.From.Before(from) && !m.From.After(to)
		})
		switch i := int(x.Meeting); {
		case inRecording >= 0:
			ref := meetings[inRecording].Ref
			en.Meeting, en.Kind = &ref, RecapMeeting
		case i >= 1 && i <= len(meetings) && meetingNear(meetings[i-1], from, to):
			ref := meetings[i-1].Ref
			en.Meeting, en.Kind = &ref, RecapMeeting
		case startsIn >= 0:
			ref := meetings[startsIn].Ref
			en.Meeting, en.Kind = &ref, RecapMeeting
		}
		if en.Title == "" {
			en.Title = clipRunes(en.Points[0].Text, 40)
			if en.Meeting != nil && en.Meeting.Name != "" {
				en.Title = en.Meeting.Name
			}
		}
		rc.Timeline = append(rc.Timeline, en)
	}
	said := map[string][]float64{} // an action's task (recapTaskKey) → when each one kept was said
	for _, x := range a.Actions {
		t := clipText(x.What)
		if t == "" {
			continue
		}
		sec, ok := line(x.Line)
		if !ok {
			continue
		}
		// The same task for two people is two actions, and so is one said again hours later.
		who := named(x.Who)
		key := recapTaskKey(who, t)
		if slices.ContainsFunc(said[key], func(at float64) bool { return math.Abs(at-sec) <= recapTodoNear }) {
			continue
		}
		said[key] = append(said[key], sec)
		by := oneLine(x.By)
		due := ""
		if by != "" {
			due = dueDate(x.DueDate, r.Day)
		}
		rc.Actions = append(rc.Actions, MinutesAction{Who: who, What: t, By: by, DueDate: due, At: sec})
	}
	if cited == 0 && rc.Summary == "" {
		return nil, errMinutesUnusable
	}
	sort.SliceStable(rc.Timeline, func(i, j int) bool {
		if rc.Timeline[i].From != rc.Timeline[j].From {
			return rc.Timeline[i].From < rc.Timeline[j].From
		}
		return rc.Timeline[i].To < rc.Timeline[j].To
	})
	rc.Timeline = firstN(rc.Timeline, recapMaxEntries)
	sort.SliceStable(rc.Actions, func(i, j int) bool { return rc.Actions[i].At < rc.Actions[j].At })
	rc.Actions = capItems(rc.Actions)
	for i := range rc.Actions {
		rc.Actions[i].ID = "a" + strconv.Itoa(i+1)
	}
	return rc, nil
}

// recapRead is what a recap read of its recording: text, the TextRev it read all of (0: it left some out for want of
// time), and chunks, the recording's chunks then (whose notes are kept).
type recapRead struct {
	text   int64
	chunks []dayChunk
}

// meetingNear: known meeting m is there when an entry from–to is: a meeting recording's span overlaps it, a calendar
// item starts in it or within recapMeetingNear of it.
func meetingNear(m dayMeeting, from, to time.Time) bool {
	if !m.To.IsZero() {
		return !m.From.After(to) && !m.To.Before(from)
	}
	return !m.From.Before(from.Add(-recapMeetingNear)) && !m.From.After(to.Add(recapMeetingNear))
}

// storeDayRecap writes recap rc of snapshot r (the revision read) for target, unless the recording was deleted or
// trimmed meanwhile or another PUT set its base (errRecapStale); the target is settled unless another became due
// since, and the notes of chunks it no longer has go. When the day's push is due with this recap (dayPushWithRecap),
// it is marked in the same write of meta.json that records the recap, and told at once.
func (e *Engine) storeDayRecap(ctx context.Context, r *Recording, target *RecapTarget, rc *DayRecap, read recapRead, now time.Time) error {
	st := e.Store
	st.recMu.Lock()
	if !st.recapCurrent(r.ID, r.Base) {
		st.recMu.Unlock()
		return errRecapStale
	}
	cur, _ := st.Recording(r.ID)
	if err := st.keepRecapNotes(cur.ID, read.chunks); err != nil {
		st.recMu.Unlock()
		return err
	}
	// The version after both meta's and the file's, as for minutes (storeMinutes).
	rc.Version, rc.Revision, rc.GeneratedAt = cur.RecapVersion+1, r.Revision, now.UTC()
	if old, err := st.readRecap(cur.ID); err == nil {
		rc.Version = max(rc.Version, old.Version+1)
	}
	keepRecapTodos(rc, cur.RecapTodos)
	rc.Markdown = renderDayRecap(rc, r.StartedAt, st.RecordingZone(r))
	if wrote, err := st.putRecap(cur.ID, rc); err != nil || !wrote {
		st.recMu.Unlock()
		return err
	}
	e.dayPushWithRecap(cur, target, rc, now) // before RecapVersion: whether the day had a recap
	cur.RecapVersion, cur.RecapRev, cur.RecapText, cur.RecapFinal = rc.Version, r.Revision, read.text, rc.Final
	if rc.Final && cur.RecapFinalAt == nil {
		at := rc.GeneratedAt
		cur.RecapFinalAt = &at
	}
	if cur.RecapDue != nil && cur.RecapDue.N == target.N {
		cur.RecapDue = nil
	}
	cur.planRecap()
	err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false)
	push := cur.RecapPush
	st.recMu.Unlock()
	if err != nil {
		return err
	}
	if push {
		e.tellDayRecap(ctx, r.ID)
	}
	return nil
}

// The day's push (docs/assistant/day-recap.md "The push"): one a day (Recording.Day), across its parts. At the recap
// time it is due at once from a recording's ready recap, else the day waits for its first recap, which takes it in the
// same write; a final recap of a day that had none takes it too. A recap asked for before the recap time never does.
// Due is persisted (RecapPush, from RecapPushSince) before DayRecapWritten is told, and done (RecapPushed) once it
// took it (returned nil), so a restart in between tells it again (tellDayRecaps), and so does a later round after one
// it failed, dayPushRetry after that call ended (doubled for each one failed since, up to 16 minutes). The
// destinations that took it are kept with it (RecapPushTook, from what each call says), so it is told again with
// them and goes only to the others, restarts included. dayPushGiveUp after it became due, one still failing is given
// up on (RecapPushed and RecapPushFailed), logged once. A round tells the push waiting longest first, and starts no
// more once it spent dayPushBudget on them, so pushes failing slowly never hold up the recaps.

// dayPushAtRecapTime is the day's push at r's recap time now, in the same write (the caller holds recMu): due from r's
// recap at once when it is ready, else the day waits; nothing once a part of the day has it.
func (s *Store) dayPushAtRecapTime(r *Recording, now time.Time) {
	if r.RecapPush || r.RecapPushed || s.dayPushLocked(r.Day, r.ID).pushed {
		return
	}
	if r.RecapState == MinutesReady {
		r.dayPushDue(now)
		return
	}
	r.RecapWait = true
}

// dayPushDue marks the day's push due on r from now, sent to none yet.
func (r *Recording) dayPushDue(now time.Time) {
	r.RecapPush, r.RecapWait, r.RecapPushSince = true, false, now.UTC()
	r.RecapPushTries, r.RecapPushAt, r.RecapPushTook = 0, time.Time{}, nil
}

// dayPushWithRecap marks on cur the day's push when it is due with cur's new recap rc, written for target (see above;
// the caller holds recMu and writes cur).
func (e *Engine) dayPushWithRecap(cur *Recording, target *RecapTarget, rc *DayRecap, now time.Time) {
	if cur.RecapPush || cur.RecapPushed || target.Reason == RecapDueAsked && now.Before(e.recapTime(cur.Day)) {
		return
	}
	day := e.Store.dayPushLocked(cur.Day, cur.ID)
	switch {
	case day.pushed:
		cur.RecapWait = false
	case cur.RecapWait || day.waits || rc.Final && cur.RecapVersion == 0 && !day.recapped:
		cur.dayPushDue(now)
	}
}

// dayPush is what a day's parts say of its push: one has it (due or told), one waits for the day's first recap, one
// has a recap.
type dayPush struct{ pushed, waits, recapped bool }

// dayPushLocked is what day's recordings other than except say of its push. The caller holds recMu.
func (s *Store) dayPushLocked(day, except string) dayPush {
	var p dayPush
	for _, r := range s.Recordings() {
		if r.Kind != RecordingDay || r.Day != day || r.ID == except {
			continue
		}
		p.pushed = p.pushed || r.RecapPush || r.RecapPushed
		p.waits = p.waits || r.RecapWait
		p.recapped = p.recapped || r.RecapVersion > 0
	}
	return p
}

// tellDayRecaps tells DayRecapWritten of the pushes of day recordings ids that are due and not told yet (marked this
// round, or before a restart) or due again after one it failed, the one waiting longest first, until the round spent
// dayPushBudget on them: the others are told at the next round.
func (e *Engine) tellDayRecaps(ctx context.Context, ids []string) {
	if e.DayRecapWritten == nil {
		return
	}
	start := e.now()
	type due struct {
		id string
		at time.Time
	}
	var todo []due
	for _, id := range ids {
		if r, ok := e.Store.Recording(id); ok && r.RecapPush && !start.Before(r.RecapPushAt) {
			todo = append(todo, due{id, r.RecapPushAt})
		}
	}
	sort.SliceStable(todo, func(i, j int) bool { return todo[i].at.Before(todo[j].at) })
	for _, d := range todo {
		if ctx.Err() != nil || e.now().Sub(start) >= dayPushBudget {
			return
		}
		e.tellDayRecap(ctx, d.id) // reads it again, and tells only one whose push is due
	}
}

// tellDayRecap calls DayRecapWritten for day recording id, whose push is due (and not waiting after one it failed),
// with its recap (outside the store's locks), then marks it told, keeping the destinations the call says took it;
// one it failed stays due, tried again after a wait, until dayPushGiveUp after it became due. Without the hook it
// stays due, for when the assistant sets it.
func (e *Engine) tellDayRecap(ctx context.Context, id string) {
	if e.DayRecapWritten == nil {
		return
	}
	st := e.Store
	r, ok := st.Recording(id)
	if !ok || !r.RecapPush || e.now().Before(r.RecapPushAt) {
		return
	}
	rc, err := st.readRecap(id)
	var took []string
	var failed error
	if err == nil {
		took, failed = e.DayRecapWritten(ctx, *r, *rc)
	}
	now := e.now() // the wait is from the end of the call, which may have waited on its destinations
	st.recMu.Lock()
	defer st.recMu.Unlock()
	cur, ok := st.Recording(id)
	if !ok || !cur.RecapPush {
		return
	}
	cur.RecapPushTook = tookPush(cur.RecapPushTook, took)
	if cur.RecapPushSince.IsZero() {
		cur.RecapPushSince = now.UTC() // marked due before it was kept: due from its first call
	}
	switch {
	case failed != nil && now.Sub(cur.RecapPushSince) >= dayPushGiveUp:
		e.logf("recording %s: recap push: given up %s after it became due, %d tries: %v", id, dayPushGiveUp, cur.RecapPushTries+1, failed)
		cur.RecapPush, cur.RecapPushed, cur.RecapPushFailed = false, true, true
		cur.RecapPushTries, cur.RecapPushAt = 0, time.Time{}
	case failed != nil:
		cur.RecapPushTries++
		wait := dayPushRetry << min(cur.RecapPushTries-1, 4)
		cur.RecapPushAt = now.Add(wait).UTC()
		e.logf("recording %s: recap push: %v (again in %s)", id, failed, wait)
	default:
		cur.RecapPush, cur.RecapPushed = false, err == nil // a recap gone meanwhile pushes nothing: another part's may
		cur.RecapPushTries, cur.RecapPushAt, cur.RecapPushSince, cur.RecapPushTook = 0, time.Time{}, time.Time{}, nil
	}
	if err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false); err != nil {
		e.logf("recording %s: recap push: %v", id, err)
	}
}

// tookPush is the destinations of a push that took it, kept, with those that took it now: each once, sorted.
func tookPush(kept, now []string) []string {
	out := slices.Clone(kept)
	for _, to := range now {
		if to != "" && !slices.Contains(out, to) {
			out = append(out, to)
		}
	}
	slices.Sort(out)
	return out
}

// recapFailed counts a try of target n of recording id that ran out of time (timedOut, and puts the next one off) or
// whose answer was unusable.
func (e *Engine) recapFailed(id string, n int, timedOut bool, now time.Time) {
	e.updateRecap(id, func(r *Recording) bool {
		t := r.RecapDue
		if t == nil || t.N != n {
			return false
		}
		if timedOut {
			t.Timeouts++
			t.RetryAt = now.Add(minutesRetry << min(t.Timeouts-1, 8)).UTC()
		} else {
			t.Fails++
		}
		r.planRecap()
		return true
	})
}

// settleRecap settles target n of recording id with no recap: the recording has no text to make one of.
func (e *Engine) settleRecap(id string, n int) error {
	return e.updateRecap(id, func(r *Recording) bool {
		if r.RecapDue == nil || r.RecapDue.N != n {
			return false
		}
		r.RecapDue = nil
		r.planRecap()
		return true
	})
}

// recapNoted marks recording id's finished chunks of text (TextRev) on base noted, and keeps the notes of chunks
// only (its chunks then); errRecapStale when it was deleted, trimmed or given another base since.
func (e *Engine) recapNoted(id string, base, text int64, chunks []dayChunk) error {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if !st.recapCurrent(id, base) {
		return errRecapStale
	}
	if err := st.keepRecapNotes(id, chunks); err != nil {
		return err
	}
	r, _ := st.Recording(id)
	if r.RecapNotedText >= text {
		return nil
	}
	r.RecapNotedText = text
	return st.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// updateRecap changes day recording id's meta.json with fn (false: nothing to write).
func (e *Engine) updateRecap(id string, fn func(r *Recording) bool) error {
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok || r.Kind != RecordingDay || r.Trimmed() || !fn(r) {
		return nil
	}
	return st.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// recapNotesFailed counts a try of chunk c of recording id on base that ran out of time (timedOut: the next one put
// off) or whose answer was unusable; the notes as written (updateRecapNotes).
func (e *Engine) recapNotesFailed(id string, base int64, chunks []dayChunk, c dayChunk, timedOut bool, now time.Time) (recapNotes, error) {
	return e.updateRecapNotes(id, base, chunks, func(n *recapNotes) {
		cn := n.chunk(c.hash)
		if cn == nil {
			n.Chunks = append(n.Chunks, recapChunkNote{Hash: c.hash, FromN: c.lines[0].N, ToN: c.lines[len(c.lines)-1].N})
			cn = &n.Chunks[len(n.Chunks)-1]
		}
		if cn.Base != base {
			cn.Base, cn.Fails, cn.Timeouts, cn.RetryAt = base, 0, 0, time.Time{}
		}
		if timedOut {
			cn.Timeouts++
			cn.RetryAt = now.Add(minutesRetry << min(cn.Timeouts-1, 8)).UTC()
		} else {
			cn.Fails++
		}
	})
}
