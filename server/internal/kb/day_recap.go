package kb

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"time"
)

// The daily recap (docs/assistant/day-recap.md "What the hub stores and returns"): a day recording's recap.json
// (DayRecap, what detail.recap is) and recap.md (the same as Markdown with clock times, what the searches and kb_get
// read) in its directory, beside summary.md (the recap's summary, what detail.summary shows). Like minutes they survive
// the trim (recording_trim.go) until minutes_days; recap_notes.json, the notes the recap is merged from
// (day_recap_write.go), is made of the transcript and goes with it. A recap becomes due for a reason (RecapTarget),
// persisted with the recording until a recap is written or the model failed at it; recap_state says which.

const (
	recapFile      = "recap.json"
	recapMarkdown  = "recap.md"
	recapNotesFile = "recap_notes.json"
)

// Why a recap became due (RecapTarget.Reason).
const (
	RecapDueEvening  = "evening"  // the recap time of its day
	RecapDueComplete = "complete" // the recording became complete: the final recap
	RecapDueCutoff   = "cutoff"   // 6 hours after its day ended, not complete: the final recap of what arrived
	RecapDueAsked    = "asked"    // POST …/minutes
	RecapDueRepair   = "repair"   // a PUT set a new base while a recap existed
)

// Why a final recap is final (DayRecap.FinalReason).
const (
	FinalComplete = "complete"
	FinalCutoff   = "cutoff"
)

// RecapTarget is a due recap as it became due: its number (Recording.RecapTargets then), why, the recording's base,
// revision and coverage then, and when. Its tries are counted here, as the minutes' are for a revision: Fails unusable
// answers, Timeouts tries that ran out of time, the next not before RetryAt (minutes_timeout.go); new text neither
// moves nor resets it, and a PUT moves it to the new base (Base). It reads what the hub holds when it runs.
type RecapTarget struct {
	N        int       `json:"n"`
	Reason   string    `json:"reason"`
	Base     int64     `json:"base"`
	Revision int64     `json:"revision"`
	To       float64   `json:"to"`
	At       time.Time `json:"at"`
	Fails    int       `json:"fails,omitempty"`
	Timeouts int       `json:"timeouts,omitempty"`
	RetryAt  time.Time `json:"retry_at,omitzero"`
}

// failed: the model could not write it after the minutes' tries.
func (t *RecapTarget) failed() bool {
	return t.Fails >= minutesTries || t.Timeouts >= minutesTimeoutTries
}

// waiting: a try ran out of time and the next is not due yet.
func (t *RecapTarget) waiting(now time.Time) bool { return now.Before(t.RetryAt) }

// DayRecap is recap.json, and detail.recap of GET /api/v1/kb/recordings/{id}: times are seconds since the recording's
// started_at, the clock of its segments.
type DayRecap struct {
	Version     int             `json:"version"`  // counts the recaps written for this recording
	Revision    int64           `json:"revision"` // the recording revision it was made from
	GeneratedAt time.Time       `json:"generated_at"`
	Day         string          `json:"day"`
	Covers      RecapSpan       `json:"covers"` // up to the end of the last chunk it read
	Final       bool            `json:"final"`
	FinalReason string          `json:"final_reason,omitempty"` // FinalComplete or FinalCutoff, with Final
	Summary     string          `json:"summary"`
	Timeline    []DayRecapEntry `json:"timeline,omitempty"`
	Actions     []MinutesAction `json:"actions,omitempty"`
	Gaps        []CoverageGap   `json:"gaps,omitempty"`    // the coverage's gaps inside Covers
	Partial     bool            `json:"partial,omitempty"` // the day was too long: some of it was not read
	Markdown    string          `json:"markdown"`
}

// RecapSpan is a stretch of the day, seconds since started_at.
type RecapSpan struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
}

// Kinds of a recap's timeline entry.
const (
	RecapMeeting = "meeting"
	RecapOutside = "outside"
)

// DayRecapEntry is one thing of the day's timeline: a meeting (one the hub knows of when Meeting is set) or what was
// said outside them, with its points.
type DayRecapEntry struct {
	From    float64        `json:"from"`
	To      float64        `json:"to"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title"`
	Meeting *MeetingRef    `json:"meeting,omitempty"`
	Points  []MinutesPoint `json:"points"`
}

// planRecap sets a day recording's RecapState from the rest: pending while a recap is due, failed once the model could
// not write it, ready once one was written and none is due, absent before any. A trimmed one is due no more: its
// recap kept is all there will be.
func (r *Recording) planRecap() {
	if r.Kind != RecordingDay {
		return
	}
	if r.Trimmed() {
		r.RecapDue = nil
	}
	switch {
	case r.RecapDue != nil && r.RecapDue.failed():
		r.RecapState = MinutesFailed
	case r.RecapDue != nil:
		r.RecapState = MinutesPending
	case r.RecapVersion > 0:
		r.RecapState = MinutesReady
	default:
		r.RecapState = ""
	}
}

// armRecap makes a recap of r due for reason, as r is now; one still due is settled by the same recap, so it gives
// way (the newer reason says what kind of recap it is: a final one's is complete or cutoff).
func (r *Recording) armRecap(reason string, now time.Time) {
	r.RecapTargets++
	r.RecapDue = &RecapTarget{N: r.RecapTargets, Reason: reason, Base: r.Base, Revision: r.Revision, To: r.coverageTo(),
		At: now.UTC()}
	r.planRecap()
}

// askDayRecap is AskMinutes for day recording r (as read under recMu, which the caller holds): a recap from what the
// hub has now, never a push; ErrRecordingIncomplete while it has no text. Asking again is trying again: the chunks the
// model gave up on, or that wait after running out of time, are noted afresh (retryRecapNotes).
func (e *Engine) askDayRecap(r *Recording) error {
	if r.Chars == 0 {
		return ErrRecordingIncomplete
	}
	if err := e.Store.retryRecapNotes(r.ID); err != nil {
		return err
	}
	r.RecapAsks++
	r.armRecap(RecapDueAsked, e.now())
	return e.Store.putEntity(KindRecording, r.ID, r, &r.Tagging, false)
}

// hasRecap: recording id has a recap that can be read.
func (s *Store) hasRecap(id string) bool {
	_, err := s.readRecap(id)
	return err == nil
}

// readRecap is recording id's (a stored id) recap.json, ErrNotFound without one.
func (s *Store) readRecap(id string) (*DayRecap, error) {
	raw, err := os.ReadFile(s.path(KindRecording, id, recapFile))
	if err != nil {
		return nil, ErrNotFound
	}
	var rc DayRecap
	if err := json.Unmarshal(raw, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}

// ReadDayRecap is day recording id's recap, ErrNotFound when it has none (or there is no such recording).
func (e *Engine) ReadDayRecap(rawID string) (*DayRecap, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	return e.Store.readRecap(id)
}

// putRecap writes recap.md of recording id, summary.md from its summary (none while it is blank), then recap.json,
// unless it was deleted meanwhile (false then). recap.json goes last, so a write that failed half way leaves the
// recap.json before it, and the next write (a PUT tried again, mendRecapFiles) writes them all again: what an erase
// withdrew never stays in recap.md or summary.md behind a recap.json without it. The caller holds recMu.
func (s *Store) putRecap(id string, rc *DayRecap) (bool, error) {
	if s.readOnly {
		return false, ErrReadOnly
	}
	raw, err := json.MarshalIndent(rc, "", "  ")
	if err != nil {
		return false, err
	}
	wrote, err := s.recapFilesLocked(id, func() error {
		if err := writeFile(s.path(KindRecording, id, recapMarkdown), []byte(strings.TrimSpace(rc.Markdown)+"\n")); err != nil {
			return err
		}
		if rc.Summary != "" {
			return nil
		}
		// summary.md is the recap's, unless the user wrote it (putSummary leaves a locked one alone too).
		if locked, err := s.summaryLocked(id); err != nil || locked {
			return err
		}
		if err := os.Remove(s.path(KindRecording, id, "summary.md")); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	if err != nil || !wrote {
		return wrote, err
	}
	if rc.Summary != "" {
		if err := s.putSummary(KindRecording, id, rc.Summary, false); err != nil {
			return true, err
		}
	}
	if wrote, err = s.recapFilesLocked(id, func() error {
		return writeFile(s.path(KindRecording, id, recapFile), append(raw, '\n'))
	}); err != nil || !wrote {
		return wrote, err
	}
	s.notify(KindRecording, id, "summary")
	return true, nil
}

// recapFilesLocked runs write on recording id's files under recFilesMu and metaMu, unless the recording was deleted
// meanwhile (false then: writeFile makes missing directories, and a recording deleted must stay gone).
func (s *Store) recapFilesLocked(id string, write func() error) (bool, error) {
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if err := s.recoverRecordingLocked(id); err != nil {
		return false, err
	}
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	if _, err := os.Stat(s.path(KindRecording, id, "meta.json")); err != nil {
		return false, nil
	}
	return true, write()
}

// summaryLocked: the user wrote recording id's summary (its meta.json locks it). The caller holds metaMu.
func (s *Store) summaryLocked(id string) (bool, error) {
	var t Tagging
	if _, err := readJSON(s.entityPath(KindRecording, id), &t); err != nil {
		return false, err
	}
	return t.fieldLocked("summary"), nil
}

// mendRecapFiles writes recording r's recap.md and summary.md again from its recap.json when they do not say what it
// does (a write that failed half way, putRecap). A trimmed recording keeps no summary.md, and one the user wrote is
// theirs. The caller holds recMu.
func (s *Store) mendRecapFiles(r *Recording) error {
	if s.readOnly {
		return nil
	}
	rc, err := s.readRecap(r.ID)
	if err != nil {
		return nil // none (one that cannot be read shows nothing either)
	}
	md, _ := os.ReadFile(s.path(KindRecording, r.ID, recapMarkdown))
	mdOK := string(md) == strings.TrimSpace(rc.Markdown)+"\n"
	sumOK := r.Trimmed()
	if !sumOK {
		sum, err := os.ReadFile(s.path(KindRecording, r.ID, "summary.md"))
		sumOK = rc.Summary == "" && os.IsNotExist(err) || rc.Summary != "" && err == nil && string(sum) == strings.TrimSpace(rc.Summary)+"\n"
	}
	if !sumOK {
		s.metaMu.Lock()
		sumOK, err = s.summaryLocked(r.ID)
		s.metaMu.Unlock()
		if err != nil {
			return err
		}
	}
	switch {
	case mdOK && sumOK:
		return nil
	case sumOK:
		_, err := s.recapFilesLocked(r.ID, func() error {
			return writeFile(s.path(KindRecording, r.ID, recapMarkdown), []byte(strings.TrimSpace(rc.Markdown)+"\n"))
		})
		return err
	}
	_, err = s.putRecap(r.ID, rc)
	return err
}

// repairDayRecap is what a PUT of day recording r (as stored before it, read under recMu, which the caller holds)
// with upload u (cleaned) that sets a new base does to its recap, before the upload is committed (docs/assistant/
// day-recap.md "When a recap is due"): from a recap made before it goes at once every entry overlapping an erased gap
// of u's coverage (its title may quote the erased words), every point and action whose time no longer starts a line
// with text in u, the entries left with no point, and the summary. An action made a to-do goes too: the to-do stays,
// and r.RecapTodos gives it back to the same action in a later version (keepRecapTodos). A recap withdrawn already (a
// PUT tried again after one that failed) has recap.md and summary.md written again from it when they still differ.
// The notes of the chunks whose lines the PUT changed go with them (pruneRecapNotes). recapped: r has a recap
// (readable or not).
func (e *Engine) repairDayRecap(r *Recording, u *RecordingUpload) (recapped bool, err error) {
	st := e.Store
	rc, err := st.readRecap(r.ID)
	if err != nil {
		// None, or one that cannot be read (nothing of it is shown either): only a recap written before counts.
		return r.RecapVersion > 0, nil
	}
	if withdrawRepaired(rc, u) {
		// Another version: the actions changed, and a confirmation naming the one before must not land on them.
		rc.Version = max(rc.Version, r.RecapVersion) + 1
		zone := st.Zone()
		if u.TZ != "" {
			if l, err := dayLocation(u.TZ); err == nil {
				zone = l
			}
		}
		rc.Markdown = renderDayRecap(rc, u.StartedAt, zone)
		if _, err := st.putRecap(r.ID, rc); err != nil {
			return true, err
		}
		r.RecapVersion = rc.Version
		return true, nil
	}
	return true, st.mendRecapFiles(r)
}

// rebaseRecap is what a PUT that set a new base does to r's recap targets: one still due moves to the new base with
// its reason and tries (it reads what the hub holds when it runs); else, when r has a recap (recapped) or a target the
// model failed at, a repair is due at once.
func (r *Recording) rebaseRecap(recapped bool, now time.Time) {
	switch t := r.RecapDue; {
	case t != nil && !t.failed():
		t.Base = r.Base
	case recapped || t != nil:
		r.armRecap(RecapDueRepair, now)
	}
}

// withdrawRepaired takes out of rc what upload u no longer says (see repairDayRecap) and gives it u's gaps; false when
// nothing changed.
func withdrawRepaired(rc *DayRecap, u *RecordingUpload) bool {
	starts := map[float64]bool{}
	for _, sg := range u.Segments { // cleaned: only those with text
		starts[sg.Start] = true
	}
	var erased, gaps []CoverageGap
	if c := u.Coverage; c != nil {
		gaps = gapsWithin(c.Gaps, rc.Covers.From, rc.Covers.To)
		for _, g := range c.Gaps {
			if g.Reason == GapErased {
				erased = append(erased, g)
			}
		}
	}
	overlaps := func(from, to float64) bool {
		return slices.ContainsFunc(erased, func(g CoverageGap) bool { return g.From <= to && g.To >= from })
	}
	// gone: at no longer starts a line with text, or (the phone's own log disagreeing) lies in an erased gap.
	gone := func(at float64) bool { return !starts[at] || overlaps(at, at) }
	changed := rc.Summary != ""
	rc.Summary = "" // it may quote anything the PUT took out
	timeline := rc.Timeline[:0]
	for _, en := range rc.Timeline {
		if overlaps(en.From, en.To) {
			changed = true
			continue
		}
		n := len(en.Points)
		en.Points = slices.DeleteFunc(en.Points, func(p MinutesPoint) bool { return gone(p.At) })
		changed = changed || len(en.Points) != n
		if len(en.Points) > 0 {
			timeline = append(timeline, en)
		}
	}
	rc.Timeline = timeline
	n := len(rc.Actions)
	rc.Actions = slices.DeleteFunc(rc.Actions, func(a MinutesAction) bool { return gone(a.At) })
	changed = changed || len(rc.Actions) != n
	if !slices.Equal(rc.Gaps, gaps) {
		rc.Gaps, changed = gaps, true
	}
	return changed
}

// gapsWithin is the part of gaps inside from–to, each cut to it.
func gapsWithin(gaps []CoverageGap, from, to float64) []CoverageGap {
	var out []CoverageGap
	for _, g := range gaps {
		if g.To <= from || g.From >= to {
			continue
		}
		g.From, g.To = max(g.From, from), min(g.To, to)
		out = append(out, g)
	}
	return out
}

// RecapTodo is a to-do made of a recap's action, remembered whatever the version: the action's who and what
// (recapTaskKey) and when it was said.
type RecapTodo struct {
	Task string  `json:"task"`
	At   float64 `json:"at"`
	Todo string  `json:"todo"`
}

// recapTodoNear is how far apart in time two actions with the same who and what may be and still be one task: one
// said again later in the day is another.
const recapTodoNear = 15 * 60

// recapTaskKey is what makes a recap's action the same task in any version: who and what, trimmed and case-folded,
// hashed (meta.json keeps it, and is searched).
func recapTaskKey(who, what string) string {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	h := sha256.Sum256([]byte(norm(who) + "\x00" + norm(what)))
	return hex.EncodeToString(h[:12])
}

// keepRecapTodos gives rc's actions the to-dos made of the same task in any version of r's recap: same who and what,
// said within recapTodoNear of it, the nearest first; each to-do goes to one action at most. True when it gave any.
func keepRecapTodos(rc *DayRecap, todos []RecapTodo) bool {
	used, gave := map[string]bool{}, false
	for i := range rc.Actions {
		a := &rc.Actions[i]
		if a.TodoID != "" {
			used[a.TodoID] = true
		}
	}
	for i := range rc.Actions {
		a := &rc.Actions[i]
		if a.TodoID != "" {
			continue
		}
		key, best, gap := recapTaskKey(a.Who, a.What), "", math.Inf(1)
		for _, t := range todos {
			if d := math.Abs(t.At - a.At); t.Task == key && !used[t.Todo] && d <= recapTodoNear && d < gap {
				best, gap = t.Todo, d
			}
		}
		if best != "" {
			a.TodoID, used[best], gave = best, true, true
		}
	}
	return gave
}

// rememberTodo notes that to-do todo was made of action a.
func (r *Recording) rememberTodo(a MinutesAction, todo string) bool {
	t := RecapTodo{Task: recapTaskKey(a.Who, a.What), At: a.At, Todo: todo}
	if slices.Contains(r.RecapTodos, t) {
		return false
	}
	r.RecapTodos = append(r.RecapTodos, t)
	return true
}

// SetDayRecapTodos records the to-dos made of actions of version of day recording id's recap (confirmed: each action
// as that version has it, TodoID the to-do made of it), as SetMinutesTodos does for minutes; ErrNotFound without a
// recap. Each is remembered with its action whatever the recap says now (Recording.RecapTodos: its who and what, and
// when it was said), so any version has it on the same task said within 15 minutes, one action each
// (keepRecapTodos): the version confirmed on that action, one written since (a rewrite while the to-dos were made)
// where it has the task near the same time and nowhere else, and one written later even after a version that left it
// out.
func (e *Engine) SetDayRecapTodos(rawID string, version int, confirmed []MinutesAction) (*DayRecap, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return nil, ErrNotFound
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	r, ok := st.Recording(id)
	if !ok {
		return nil, ErrNotFound
	}
	rc, err := st.readRecap(id)
	if err != nil {
		return nil, err
	}
	changed, remembered := false, false
	for _, a := range confirmed {
		if a.TodoID == "" {
			continue
		}
		remembered = r.rememberTodo(a, a.TodoID) || remembered
		if rc.Version != version {
			continue
		}
		i := slices.IndexFunc(rc.Actions, func(b MinutesAction) bool { return b.ID == a.ID })
		if i >= 0 && rc.Actions[i].TodoID != a.TodoID {
			rc.Actions[i].TodoID, changed = a.TodoID, true
		}
	}
	changed = keepRecapTodos(rc, r.RecapTodos) || changed
	if changed {
		if ok, err := st.putRecap(id, rc); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrNotFound
		}
	}
	if remembered {
		if err := st.putEntity(KindRecording, r.ID, r, &r.Tagging, false); err != nil {
			return nil, err
		}
	}
	return rc, nil
}

// Gap reasons as recap.md says them (i18n-ignore: knowledge-base data, like the minutes' headings).
var gapReasonZH = map[string]string{GapPaused: "暂停", GapInterrupted: "被打断", GapStopped: "停止", GapPending: "还没传完",
	GapLost: "丢失", GapErased: "已擦除"}

// renderDayRecap is recap.md: the recap with clock times in zone, the meetings and what was said outside them apart,
// then the suggested to-dos; sections without anything are left out (i18n-ignore: knowledge-base data).
func renderDayRecap(rc *DayRecap, start time.Time, zone *time.Location) string {
	clock := func(sec float64) string { return start.Add(seconds(sec)).In(zone).Format("15:04") }
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 全天回顾\n\n", rc.Day)
	if rc.Summary != "" {
		b.WriteString(rc.Summary + "\n\n")
	}
	fmt.Fprintf(&b, "- 时段：%s–%s", clock(rc.Covers.From), clock(rc.Covers.To))
	if !rc.Final {
		b.WriteString("（截至目前）")
	}
	b.WriteByte('\n')
	if len(rc.Gaps) > 0 {
		var gaps []string
		for _, g := range rc.Gaps {
			gaps = append(gaps, clock(g.From)+"–"+clock(g.To)+"（"+cmp.Or(gapReasonZH[g.Reason], g.Reason, "原因不明")+"）")
		}
		b.WriteString("- 没有录到：" + strings.Join(gaps, "、") + "\n")
	}
	section := func(title, kind string) {
		first := true
		for _, en := range rc.Timeline {
			if en.Kind != kind {
				continue
			}
			if first {
				b.WriteString("\n## " + title + "\n\n")
				first = false
			}
			b.WriteString("- " + clock(en.From) + "–" + clock(en.To) + " " + en.Title)
			if m := en.Meeting; m != nil && m.Name != "" && m.Name != en.Title {
				b.WriteString("（" + m.Name + "）")
			}
			b.WriteByte('\n')
			for _, p := range en.Points {
				b.WriteString("  - [" + clock(p.At) + "] " + p.Text)
				if p.Who != "" {
					b.WriteString("（" + p.Who + "）")
				}
				b.WriteByte('\n')
			}
		}
	}
	section("会议", RecapMeeting)
	section("会议以外", RecapOutside)
	if len(rc.Actions) > 0 {
		b.WriteString("\n## 建议待办\n\n")
		for _, a := range rc.Actions {
			b.WriteString("- [" + clock(a.At) + "] ")
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
	if rc.Partial {
		b.WriteString("\n> 这一天太长，中间有些时段没有整理进回顾。\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// dayDetail is detail.meta and detail.recap of recording id, given its meta.json: a day recording's meta always says
// next and base (0 included, as reconciliation does), and its recap.json is read once it has one; nil for any other
// recording.
func (e *Engine) dayDetail(id string, meta []byte) ([]byte, []byte) {
	var head struct {
		Kind string           `json:"kind"`
		Next *json.RawMessage `json:"next"`
		Base *json.RawMessage `json:"base"`
	}
	if json.Unmarshal(meta, &head) != nil || head.Kind != RecordingDay {
		return meta, nil
	}
	if head.Next == nil || head.Base == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(meta, &m) == nil && m != nil {
			for _, k := range []string{"next", "base"} {
				if _, ok := m[k]; !ok {
					m[k] = json.RawMessage("0")
				}
			}
			if out, err := json.Marshal(m); err == nil {
				meta = out
			}
		}
	}
	raw, err := os.ReadFile(e.Store.path(KindRecording, id, recapFile))
	if err != nil || !json.Valid(raw) {
		return meta, nil
	}
	return meta, raw
}
