package kb

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// All-day recordings (recording_day.go): placeholders only.

const dayID = "1a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"

// dayClock is the day recordings' tests' day: 2026-11-11, recorded from 08:00 in the golden zone (Asia/Shanghai).
var dayClock = time.Date(2026, 11, 11, 8, 0, 0, 0, goldenZone)

// dayUpload is a day recording's PUT of texts, a minute apart from the day's start (8:00), covered up to the last.
func dayUpload(rev int64, texts ...string) RecordingUpload {
	u := RecordingUpload{Revision: rev, StartedAt: dayClock, Duration: float64(60 * len(texts)), Language: "zh-CN", Device: "watch",
		Kind: RecordingDay, Day: "2026-11-11", TZ: "Asia/Shanghai"}
	for i, t := range texts {
		u.Segments = append(u.Segments, RecordingSegment{Start: float64(60 * i), End: float64(60*i + 5), Text: t})
	}
	u.Coverage = &RecordingCoverage{To: float64(60 * len(texts))}
	return u
}

// dayEngine is a golden engine whose clock reads now.
func dayEngine(t *testing.T, m Model, now *time.Time) *Engine {
	t.Helper()
	e := goldenEngine(t, m)
	e.clock = func() time.Time { return *now }
	return e
}

func ptr[T any](v T) *T { return &v }

// dayAppend is an append of base at revision rev from position from.
func dayAppend(base, rev int64, from int, segs ...RecordingSegment) RecordingAppend {
	return RecordingAppend{Base: base, Revision: rev, From: ptr(from), Segments: segs}
}

func seg(start float64, text string) RecordingSegment {
	return RecordingSegment{Start: start, End: start + 5, Text: text}
}

func mustPut(t *testing.T, e *Engine, id string, u RecordingUpload, now time.Time) RecordingPut {
	t.Helper()
	put, err := e.PutRecording(id, u, now)
	if err != nil || put.Status != RecordingStored {
		t.Fatalf("put %s: %+v %v", id, put, err)
	}
	return put
}

func mustAppend(t *testing.T, e *Engine, id string, a RecordingAppend) RecordingAppended {
	t.Helper()
	got, err := e.AppendRecording(id, a)
	if err != nil || got.Status != RecordingStored {
		t.Fatalf("append %s: %+v %v", id, got, err)
	}
	return got
}

// A day upload: its own fields checked and kept, base and next set (empty segments counted, each segment keeping its
// position), never a meeting nor attended, named after its day, and listed for reconciliation with them.
func TestDayRecordingUpload(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	for _, c := range []struct {
		name string
		edit func(u *RecordingUpload)
	}{
		{"no day", func(u *RecordingUpload) { u.Day = "" }},
		{"not a date", func(u *RecordingUpload) { u.Day = "2026-13-01" }},
		{"loose date", func(u *RecordingUpload) { u.Day = "2026-1-1" }},
		{"unknown zone", func(u *RecordingUpload) { u.TZ = "Mars/Olympus" }},
		{"local zone", func(u *RecordingUpload) { u.TZ = "Local" }},
		{"part above 99", func(u *RecordingUpload) { u.Part = 100 }},
		{"negative part", func(u *RecordingUpload) { u.Part = -1 }},
		{"no number", func(u *RecordingUpload) { u.Coverage = &RecordingCoverage{To: math.NaN()} }},
		{"gap no number", func(u *RecordingUpload) {
			u.Coverage = &RecordingCoverage{To: 1, Gaps: []CoverageGap{{From: math.Inf(1), To: 2}}}
		}},
	} {
		u := dayUpload(1, "早上好")
		c.edit(&u)
		if _, err := e.PutRecording(dayID, u, now); !errors.Is(err, ErrBadRecording) {
			t.Errorf("%s: %v, want ErrBadRecording", c.name, err)
		}
	}
	if _, ok := e.Store.Recording(dayID); ok {
		t.Fatal("a refused upload was stored")
	}

	u := dayUpload(100, "早上好", "", "  今天先看导出  ")
	u.Calendar = &RecordingCalendar{Title: "周会"}
	u.Attendance = AttendanceListener
	u.SetRoom(AttendanceParticipant)
	u.Private = ptr(false)
	put := mustPut(t, e, dayID, u, now)
	if put.Revision != 100 || put.Base != 100 || put.Next != 3 || put.Attendance != AttendanceOff ||
		put.AttendanceProblem != AttendanceNotMeeting || put.Room || put.Meeting != nil {
		t.Fatalf("put %+v", put)
	}
	r := recordingNow(t, e, dayID)
	if r.Kind != RecordingDay || r.Day != "2026-11-11" || r.TZ != "Asia/Shanghai" || r.Part != 1 || r.Base != 100 || r.Next != 3 ||
		r.Segments != 2 || r.Calendar != nil || r.Attendance != "" || r.MinutesState != "" || r.RecapState != "" {
		t.Fatalf("stored %+v", r)
	}
	if r.MatchedRev != 100 || r.SummarizedRev != 100 {
		t.Errorf("an older manager would match or summarize it: matched %d summarized %d", r.MatchedRev, r.SummarizedRev)
	}
	if r.Name != "全天录音 2026-11-11" {
		t.Errorf("name %q", r.Name)
	}
	if _, err := os.Stat(e.Store.Path(KindRecording, dayID, meetingFile)); err == nil {
		t.Error("a day recording got a meeting.json")
	}
	segs := e.Store.RecordingSegments(dayID)
	if len(segs) != 2 || segs[0].N != 1 || segs[1].N != 3 || segs[1].Text != "今天先看导出" {
		t.Fatalf("segments %+v", segs)
	}
	if raw, _ := os.ReadFile(e.Store.Path(KindRecording, dayID, "segments.json")); !strings.Contains(string(raw), `"n": 3`) {
		t.Errorf("segments.json has no positions:\n%s", raw)
	}

	// Named in the owner's language, unless titled.
	e.DayRecordingName = func(day string) string { return "Day · " + day }
	u = dayUpload(101, "早上好")
	put = mustPut(t, e, dayID, u, now)
	if r := recordingNow(t, e, dayID); r.Name != "Day · 2026-11-11" || put.Next != 1 {
		t.Errorf("localized name %q, next %d", r.Name, put.Next)
	}
	u = dayUpload(102, "早上好")
	u.Title, u.Part = "去上海", 2
	mustPut(t, e, dayID, u, now)
	if r := recordingNow(t, e, dayID); r.Name != "去上海" || r.Part != 2 {
		t.Errorf("titled %q part %d", r.Name, r.Part)
	}

	// Reconciliation: kind, day, part, next and base on a day recording only.
	putRecording(t, e, recID, recUpload(1, dayClock, true, "一条笔记"), now)
	kept, _ := e.RecordingStates()
	raw, _ := json.Marshal(kept)
	var states []map[string]any
	_ = json.Unmarshal(raw, &states)
	for _, s := range states {
		switch s["id"] {
		case dayID:
			if s["kind"] != "day" || s["day"] != "2026-11-11" || s["part"] != float64(2) || s["next"] != float64(1) || s["base"] != float64(102) {
				t.Errorf("day state %v", s)
			}
		case recID:
			for _, k := range []string{"kind", "day", "part", "next", "base", "recap_state"} {
				if _, ok := s[k]; ok {
					t.Errorf("a note's state has %s: %v", k, s)
				}
			}
		}
	}
	// Detail: the day's fields, next even at 0, and the segments by start.
	u = dayUpload(103)
	u.Segments = []RecordingSegment{seg(120, "后说的"), seg(10, "先说的")}
	mustPut(t, e, dayID, u, now)
	d, err := e.Get(KindRecording, dayID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	_ = json.Unmarshal(d.Meta, &meta)
	for _, k := range []string{"kind", "day", "tz", "part", "coverage", "next", "base"} {
		if _, ok := meta[k]; !ok {
			t.Errorf("detail.meta lacks %s: %s", k, d.Meta)
		}
	}
	for _, k := range []string{"minutes_state", "meeting", "calendar", "attendance"} {
		if _, ok := meta[k]; ok {
			t.Errorf("detail.meta of a day recording has %s", k)
		}
	}
	if len(d.Segments) != 2 || d.Segments[0].Text != "先说的" || d.Segments[0].N != 2 {
		t.Errorf("detail segments %+v", d.Segments)
	}
	mustPut(t, e, dayID, dayUpload(104), now)
	d, _ = e.Get(KindRecording, dayID, 0)
	if !strings.Contains(string(d.Meta), `"next":0`) {
		t.Errorf("detail.meta without next: %s", d.Meta)
	}
}

// A recording's kind changes neither to nor from day; a day PUT over the limits is day_full with the hub's count, and
// stores nothing.
func TestDayRecordingKindAndLimits(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	putRecording(t, e, recID, meetingUpload(1, dayClock, "开会"), now)
	if _, err := e.PutRecording(recID, dayUpload(2, "早上好"), now); !errors.Is(err, ErrKindChanged) {
		t.Errorf("meeting to day: %v", err)
	}
	mustPut(t, e, dayID, dayUpload(2, "早上好", "第二句"), now)
	if _, err := e.PutRecording(dayID, recUpload(3, dayClock, false, "笔记"), now); !errors.Is(err, ErrKindChanged) {
		t.Errorf("day to note: %v", err)
	}
	// A stale day PUT says the stored revision, base and count.
	if put, err := e.PutRecording(dayID, dayUpload(1, "旧的"), now); err != nil || put.Status != RecordingStale || put.Revision != 2 ||
		put.Next != 2 || put.Base != 2 {
		t.Errorf("stale: %+v %v", put, err)
	}
	many := dayUpload(3)
	many.Segments = make([]RecordingSegment, maxRecordingSegments+1)
	var full *DayFullError
	if _, err := e.PutRecording(dayID, many, now); !errors.As(err, &full) || full.Next != 2 || !errors.Is(err, ErrDayFull) {
		t.Errorf("positions: %v", err)
	}
	big := dayUpload(3, strings.Repeat("长", maxRecordingText/3+1))
	if _, err := e.PutRecording(dayID, big, now); !errors.As(err, &full) || full.Next != 2 {
		t.Errorf("text: %v", err)
	}
	// The same upload of another kind is still 400.
	note := recUpload(5, dayClock, false)
	note.Segments = make([]RecordingSegment, maxRecordingSegments+1)
	if _, err := e.PutRecording("2f8fad5b-d9cb-469f-a165-70867728950e", note, now); !errors.Is(err, ErrBadRecording) {
		t.Errorf("a note over the limits: %v", err)
	}
	if r := recordingNow(t, e, dayID); r.Revision != 2 || r.Next != 2 {
		t.Errorf("a refused PUT changed it: %+v", r)
	}
}

// Coverage is mended, never refused for order or overlap.
func TestDayCoverageMended(t *testing.T) {
	long := strings.Repeat("x", 40)
	c, err := mendCoverage(&RecordingCoverage{To: -5, Gaps: []CoverageGap{
		{From: 300, To: 200, Reason: " paused "},      // ends swapped
		{From: 250, To: 400, Reason: "paused"},        // overlaps the one before, same reason: one
		{From: 400, To: 450, Reason: "paused"},        // touches it, same reason: one
		{From: 420, To: 500, Reason: "interrupted"},   // overlaps another reason: starts where it ends
		{From: 430, To: 440, Reason: "erased"},        // inside one before: gone
		{From: -10, To: 10, Reason: long},             // clamped, reason cut
		{From: 600, To: 600, Reason: "lost"},          // empty
		{From: 700, To: 800, Reason: "something new"}, // unknown reason kept
		{From: 800, To: 810, Reason: GapErased},       // touches another reason: apart
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []CoverageGap{{0, 10, long[:maxGapReason]}, {200, 450, "paused"}, {450, 500, "interrupted"}, {700, 800, "something new"},
		{800, 810, GapErased}}
	if c.To != 0 || !slices.Equal(c.Gaps, want) {
		t.Errorf("mended %+v\nwant %+v", c, want)
	}
	// More than maxCoverageGaps: the closest joined.
	var gaps []CoverageGap
	for i := range maxCoverageGaps + 20 {
		at := float64(i * 100)
		if i%2 == 1 {
			at -= 50 // every other one closer to the one before
		}
		gaps = append(gaps, CoverageGap{From: at, To: at + 10, Reason: GapPaused})
	}
	c, _ = mendCoverage(&RecordingCoverage{To: 1e6, Gaps: gaps})
	if len(c.Gaps) != maxCoverageGaps {
		t.Fatalf("%d gaps", len(c.Gaps))
	}
	for i := 1; i < len(c.Gaps); i++ {
		if c.Gaps[i].From < c.Gaps[i-1].To {
			t.Fatalf("overlapping after joining: %+v %+v", c.Gaps[i-1], c.Gaps[i])
		}
	}
	if c.Gaps[0] != (CoverageGap{0, 60, GapPaused}) {
		t.Errorf("first joined %+v", c.Gaps[0])
	}
	// Only gaps of the same reason are joined while that is enough, never erased ones: two paused gaps 90 s apart
	// before a paused and an interrupted one 5 s apart.
	gaps = nil
	for i := range maxCoverageGaps + 1 {
		reason := GapPaused
		if i%2 == 1 {
			reason = GapInterrupted
		}
		gaps = append(gaps, CoverageGap{From: float64(i * 100), To: float64(i*100 + 10), Reason: reason})
	}
	gaps[1].Reason, gaps[3].Reason = GapErased, GapPaused
	gaps[11].From, gaps[11].To = 1015, 1020
	c, _ = mendCoverage(&RecordingCoverage{To: 1e6, Gaps: gaps})
	if len(c.Gaps) != maxCoverageGaps || c.Gaps[1] != (CoverageGap{100, 110, GapErased}) || c.Gaps[2] != (CoverageGap{200, 310, GapPaused}) ||
		!slices.Contains(c.Gaps, CoverageGap{1000, 1010, GapPaused}) || !slices.Contains(c.Gaps, CoverageGap{1015, 1020, GapInterrupted}) {
		t.Errorf("same reason first: %d gaps, %+v", len(c.Gaps), c.Gaps[:12])
	}
	// Reasons alternating past the limit (a phone gone wrong): the closest joined whatever their reason, never erased.
	gaps = nil
	for i := range maxCoverageGaps + 10 {
		reason := GapPaused
		if i%2 == 1 {
			reason = GapInterrupted
		}
		if i == 1 {
			reason = GapErased
		}
		gaps = append(gaps, CoverageGap{From: float64(i * 20), To: float64(i*20 + 10), Reason: reason})
	}
	c, _ = mendCoverage(&RecordingCoverage{To: 1e6, Gaps: gaps})
	if len(c.Gaps) != maxCoverageGaps || c.Gaps[1] != (CoverageGap{20, 30, GapErased}) {
		t.Errorf("alternating: %d gaps, %+v", len(c.Gaps), c.Gaps[:3])
	}
}

// Appending by position: a retry stores nothing twice, a gap or another base is refused with the hub's count, an empty
// segment takes its position, any time order is kept sorted by start, and the other fields apply only from a request at
// least as new as the stored revision.
func TestDayAppend(t *testing.T) {
	now := dayClock.Add(2 * time.Hour)
	e := dayEngine(t, nil, &now)
	mustPut(t, e, dayID, dayUpload(1000, "八点", "八点零一"), now)
	got := mustAppend(t, e, dayID, dayAppend(1000, 1001, 2, seg(180, "八点零三"), seg(240, "")))
	if got.Next != 4 || got.Revision != 1001 || got.ID != dayID {
		t.Fatalf("append %+v", got)
	}
	// The retry: the same positions skipped, nothing stored twice.
	if got := mustAppend(t, e, dayID, dayAppend(1000, 1001, 2, seg(180, "八点零三"), seg(240, ""))); got.Next != 4 {
		t.Fatalf("retry %+v", got)
	}
	// Overlapping: only the new one. A backlog sentence said earlier lands in time order.
	mustAppend(t, e, dayID, dayAppend(1000, 1002, 3, seg(240, ""), seg(90, "补上的一句")))
	segs := e.Store.RecordingSegments(dayID)
	var texts []string
	var ns []int
	for _, s := range segs {
		texts, ns = append(texts, s.Text), append(ns, s.N)
	}
	if strings.Join(texts, "|") != "八点|八点零一|补上的一句|八点零三" || !slices.Equal(ns, []int{1, 2, 5, 3}) {
		t.Fatalf("segments %v %v", texts, ns)
	}
	r := recordingNow(t, e, dayID)
	if r.Next != 5 || r.Segments != 4 || r.Base != 1000 || r.Revision != 1002 || r.MatchedRev != 1002 || r.SummarizedRev != 1002 {
		t.Fatalf("after: %+v", r)
	}
	if md, _ := os.ReadFile(e.Store.Path(KindRecording, dayID, "transcript.md")); !strings.Contains(string(md), "08:01:30 补上的一句") {
		t.Errorf("transcript.md:\n%s", md)
	}

	var gap *SegmentsGapError
	if _, err := e.AppendRecording(dayID, dayAppend(1000, 1003, 7, seg(300, "跳过了"))); !errors.As(err, &gap) || gap.Next != 5 ||
		!errors.Is(err, ErrSegmentsGap) {
		t.Errorf("gap: %v", err)
	}

	// The other fields: only when present and from a request at least as new; ended_at and complete only set.
	end := dayClock.Add(16 * time.Hour)
	a := dayAppend(1000, 1010, 5, seg(400, "新的"))
	a.Duration, a.EndedAt, a.Title, a.Complete = ptr(400.0), &end, ptr("出差"), ptr(true)
	a.Coverage = &RecordingCoverage{To: 4000, Gaps: []CoverageGap{{From: 500, To: 600, Reason: GapPaused}}}
	a.Bookmarks = &[]RecordingBookmark{{At: 30, Label: "b"}, {At: 10}}
	if got := mustAppend(t, e, dayID, a); !got.Complete || !got.Wake {
		t.Errorf("completing append %+v", got)
	}
	r = recordingNow(t, e, dayID)
	if r.Duration != 400 || r.EndedAt == nil || r.Title != "出差" || r.Name != "出差" || !r.Complete || r.Coverage.To != 4000 ||
		len(r.Bookmarks) != 2 || r.Bookmarks[0].At != 10 {
		t.Fatalf("fields %+v", r)
	}
	// A slow retry (older revision): its new positions are stored, nothing else; the revision stays the newest.
	old := dayAppend(1000, 1005, 6, seg(500, "慢的重试"))
	old.Duration, old.Complete, old.Title = ptr(1.0), ptr(false), ptr("旧标题")
	if got := mustAppend(t, e, dayID, old); got.Revision != 1010 || got.Next != 7 {
		t.Errorf("older %+v", got)
	}
	// complete false and an absent ended_at never undo them.
	b := dayAppend(1000, 1011, 7)
	b.Complete = ptr(false)
	mustAppend(t, e, dayID, b)
	r = recordingNow(t, e, dayID)
	if r.Duration != 400 || r.Title != "出差" || !r.Complete || r.EndedAt == nil || r.Segments != 6 || r.Revision != 1011 {
		t.Errorf("after the older append %+v", r)
	}

	// Another PUT sets the base: an append on the old one is refused with the new base and count.
	mustPut(t, e, dayID, dayUpload(2000, "重新上传"), now)
	var changed *BaseChangedError
	if _, err := e.AppendRecording(dayID, dayAppend(1000, 2001, 1, seg(60, "旧的基"))); !errors.As(err, &changed) ||
		changed.Base != 2000 || changed.Next != 1 || !errors.Is(err, ErrBaseChanged) {
		t.Errorf("old base: %v", err)
	}
	if got := mustAppend(t, e, dayID, dayAppend(2000, 2001, 1, seg(60, "新的基"))); got.Next != 2 {
		t.Errorf("new base %+v", got)
	}
}

// Each error of an append in the contract's order, and the day limits.
func TestDayAppendErrors(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	mustPut(t, e, dayID, dayUpload(10, "一"), now)
	putRecording(t, e, recID, recUpload(10, dayClock, false, "笔记"), now)
	const none = "4a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"
	bad := dayAppend(10, 10, 1) // revision not above base
	for _, c := range []struct {
		name string
		id   string
		a    RecordingAppend
		want error
	}{
		{"not a uuid", "nope", bad, ErrNotFound},
		{"none", none, bad, ErrNotFound},
		{"no base", dayID, dayAppend(0, 11, 1), ErrBadRecording},
		{"revision not above base", dayID, bad, ErrBadRecording},
		{"no from", dayID, RecordingAppend{Base: 10, Revision: 11}, ErrBadRecording},
		{"negative from", dayID, dayAppend(10, 11, -1), ErrBadRecording},
		{"too many", dayID, dayAppend(10, 11, 1, make([]RecordingSegment, MaxAppendSegments+1)...), ErrBadRecording},
		{"no number", dayID, dayAppend(10, 11, 1, seg(math.NaN(), "x")), ErrBadRecording},
		{"not day", recID, dayAppend(10, 11, 1), ErrNotDay},
		{"not day before base", recID, dayAppend(99, 100, 1), ErrNotDay},
		{"base before gap", dayID, dayAppend(9, 11, 5), ErrBaseChanged},
		{"gap", dayID, dayAppend(10, 11, 2), ErrSegmentsGap},
	} {
		if _, err := e.AppendRecording(c.id, c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	// Full: positions or text past the limits, nothing of the request stored.
	r := recordingNow(t, e, dayID)
	r.Next = maxRecordingSegments - 1
	if err := e.Store.putEntity(KindRecording, dayID, r, &r.Tagging, false); err != nil {
		t.Fatal(err)
	}
	var full *DayFullError
	if _, err := e.AppendRecording(dayID, dayAppend(10, 11, maxRecordingSegments-1, seg(1, "a"), seg(2, "b"))); !errors.As(err, &full) ||
		full.Next != maxRecordingSegments-1 {
		t.Errorf("positions: %v", err)
	}
	if got := mustAppend(t, e, dayID, dayAppend(10, 11, maxRecordingSegments-1, seg(1, "a"))); got.Next != maxRecordingSegments {
		t.Errorf("the last position %+v", got)
	}
	mustPut(t, e, dayID, dayUpload(20, "一"), now)
	half := strings.Repeat("长", maxRecordingText/6)
	mustAppend(t, e, dayID, dayAppend(20, 21, 1, seg(1, half)))
	if _, err := e.AppendRecording(dayID, dayAppend(20, 22, 2, seg(2, half), seg(3, "多一点"))); !errors.As(err, &full) || full.Next != 2 {
		t.Errorf("text: %v", err)
	}
	if r := recordingNow(t, e, dayID); r.Next != 2 || r.Revision != 21 {
		t.Errorf("a full append stored something: %+v", r)
	}

	// Older manager: a day recording whose meta lost its base answers base_changed with base 0.
	r = recordingNow(t, e, dayID)
	r.Base = 0
	_ = e.Store.putEntity(KindRecording, dayID, r, &r.Tagging, false)
	var changed *BaseChangedError
	if _, err := e.AppendRecording(dayID, dayAppend(20, 22, 2)); !errors.As(err, &changed) || changed.Base != 0 || changed.Next != 2 {
		t.Errorf("lost base: %v", err)
	}
	kept, _ := e.RecordingStates()
	for _, s := range kept {
		if s.ID == dayID && (s.Base == nil || *s.Base != 0) {
			t.Errorf("reconciliation without base 0: %+v", s)
		}
	}

	// Expired: before the window (before not_day), or trimmed; nothing written.
	later := now.AddDate(0, 0, 30)
	now = later
	if got, err := e.AppendRecording(recID, dayAppend(10, 11, 1)); err != nil || got.Status != RecordingExpired {
		t.Errorf("expired before not_day: %+v %v", got, err)
	}
	if got, err := e.AppendRecording(dayID, dayAppend(0, 11, 1)); !errors.Is(err, ErrBadRecording) {
		t.Errorf("400 before expired: %+v %v", got, err)
	}
	if got, err := e.AppendRecording(dayID, dayAppend(20, 30, 2, seg(5, "过期"))); err != nil || got.Status != RecordingExpired || got.Next != 2 {
		t.Errorf("expired %+v %v", got, err)
	}
	if put, err := e.PutRecording(dayID, dayUpload(40, "过期"), later); err != nil || put.Status != RecordingExpired {
		t.Errorf("expired PUT %+v %v", put, err)
	}

	// Deleted: 410 before anything else; read-only after not found.
	now = dayClock.Add(time.Hour)
	ro, err := OpenReadOnly(e.Store.Dir)
	if err != nil {
		t.Fatal(err)
	}
	re := &Engine{Store: ro, clock: e.clock}
	if _, err := re.AppendRecording(dayID, bad); !errors.Is(err, ErrReadOnly) {
		t.Errorf("read-only: %v", err)
	}
	if _, err := re.AppendRecording(none, bad); !errors.Is(err, ErrNotFound) {
		t.Errorf("read-only, none: %v", err)
	}
	if _, err := e.DeleteRecording(dayID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AppendRecording(dayID, bad); !errors.Is(err, ErrRecordingDeleted) {
		t.Errorf("deleted: %v", err)
	}
}

// A trimmed day recording takes no append nor PUT (expired) and makes no recap again.
func TestDayAppendTrimmed(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	mustPut(t, e, dayID, dayUpload(10, "一"), now)
	writeTestRecap(t, e, dayID)
	now = dayClock.AddDate(0, 0, 20)
	e.PruneRecordings(now)
	r := recordingNow(t, e, dayID)
	if !r.Trimmed() || r.RecapState != MinutesReady {
		t.Fatalf("trimmed %+v", r)
	}
	now = dayClock.Add(time.Hour)
	e.RecordingDaysFunc = func() int { return 60 } // back in the window: still expired
	if got, err := e.AppendRecording(dayID, dayAppend(10, 11, 1, seg(70, "二"))); err != nil || got.Status != RecordingExpired {
		t.Errorf("append %+v %v", got, err)
	}
	if err := e.AskMinutes(dayID); !errors.Is(err, ErrTranscriptExpired) {
		t.Errorf("ask: %v", err)
	}
}

// DayRecordings: a day's day recordings, by part.
func TestDayRecordings(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	const part2 = "2a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d"
	u := dayUpload(1, "二")
	u.Part = 2
	mustPut(t, e, part2, u, now)
	mustPut(t, e, dayID, dayUpload(1, "一"), now)
	other := dayUpload(1, "昨天")
	other.Day = "2026-11-10"
	mustPut(t, e, "3a2b3c4d-5e6f-4a1b-8c2d-3e4f5a6b7c8d", other, now)
	putRecording(t, e, recID, recUpload(1, dayClock, false, "笔记"), now)
	got := e.DayRecordings("2026-11-11")
	if len(got) != 2 || got[0].ID != dayID || got[1].ID != part2 {
		t.Errorf("day recordings %+v", got)
	}
}

// An append never takes the place of the day: stored segments that cannot be read, do not parse or are not all there
// refuse it (a 500 for the phone to send again), with nothing written.
func TestDayAppendUnreadableSegments(t *testing.T) {
	now := dayClock.Add(time.Hour)
	e := dayEngine(t, nil, &now)
	mustPut(t, e, dayID, dayUpload(1, "早上好", "开会了"), now)
	path := e.Store.Path(KindRecording, dayID, "segments.json")
	good, _ := os.ReadFile(path)
	for _, c := range []struct {
		name string
		bad  func()
	}{
		{"unparsable", func() { _ = os.WriteFile(path, []byte("[{not json"), 0o600) }},
		{"missing", func() { _ = os.Remove(path) }},
		{"unreadable", func() { _ = os.Remove(path); _ = os.Mkdir(path, 0o700) }},
		{"fewer", func() { _ = os.WriteFile(path, []byte(`[{"start": 0, "end": 5, "text": "早上好", "n": 1}]`), 0o600) }},
	} {
		c.bad()
		meta, _ := os.ReadFile(e.Store.Path(KindRecording, dayID, "meta.json"))
		if got, err := e.AppendRecording(dayID, dayAppend(1, 2, 2, seg(200, "新的一句"))); err == nil {
			t.Errorf("%s: appended %+v", c.name, got)
		}
		if after, _ := os.ReadFile(e.Store.Path(KindRecording, dayID, "meta.json")); string(after) != string(meta) {
			t.Errorf("%s: meta.json written", c.name)
		}
		_ = os.RemoveAll(path)
		_ = os.WriteFile(path, good, 0o600)
	}
	mustAppend(t, e, dayID, dayAppend(1, 2, 2, seg(200, "新的一句")))
	if segs := e.Store.RecordingSegments(dayID); len(segs) != 3 {
		t.Errorf("segments %+v", segs)
	}
}

// A day's hours are hours of absolute time in its zone: the hour a fall-back repeats is two chunks, each ending when
// its hour does, and coverage passing from one into the other finishes the first (and wakes the recap lane).
func TestDayChunksDST(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip(err)
	}
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, la) // 01:00–02:00 comes twice that night
	lines := []minutesLine{{N: 1, At: 5400, Text: "一点半，夏令时"}, {N: 2, At: 9000, Text: "一点半，标准时"}}
	chunks := dayChunks(lines, start, la, 9600, nil) // to 01:40 PST
	if len(chunks) != 2 {
		t.Fatalf("chunks %+v", chunks)
	}
	if c := chunks[0]; c.end != 7200 || !c.finished || len(c.lines) != 1 {
		t.Errorf("01:00 PDT: %+v", c)
	}
	if c := chunks[1]; c.end != 9600 || c.finished || len(c.lines) != 1 {
		t.Errorf("01:00 PST: %+v", c)
	}
	if h := hourStart(start.Add(9000*time.Second), la); !h.Equal(start.Add(7200 * time.Second)) {
		t.Errorf("hourStart %v", h)
	}
	now := start.Add(3 * time.Hour)
	e := dayEngine(t, nil, &now)
	u := dayUpload(1)
	u.StartedAt, u.Day, u.TZ = start, "2026-11-01", "America/Los_Angeles"
	u.Segments = []RecordingSegment{seg(5400, "一点半")}
	u.Coverage = &RecordingCoverage{To: 6600} // 01:50 PDT
	mustPut(t, e, dayID, u, now)
	a := dayAppend(1, 2, 1)
	a.Coverage = &RecordingCoverage{To: 7300} // 01:01 PST
	if got := mustAppend(t, e, dayID, a); !got.Wake {
		t.Error("the repeated hour began without a wake")
	}
}
