package kb

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// putMeetingMinutes stores minutes for recording id as the minutes round would.
func putMeetingMinutes(t *testing.T, e *Engine, id string, m Minutes) {
	t.Helper()
	if ok, err := e.Store.putMinutes(id, &m); !ok || err != nil {
		t.Fatalf("minutes of %s: %v %v", id, ok, err)
	}
}

// changeRecording rewrites recording id's meta.json with fn applied.
func changeRecording(t *testing.T, e *Engine, id string, fn func(r *Recording)) {
	t.Helper()
	changeRecordingMeta(t, e, id, fn)
}

// markPrivate sets recording id's private mark.
func markPrivate(t *testing.T, e *Engine, id string, private bool) {
	t.Helper()
	if _, err := e.SetPrivate(id, private); err != nil {
		t.Fatalf("mark %s: %v", id, err)
	}
}

func changeRecordingMeta(t *testing.T, e *Engine, id string, fn func(r *Recording)) {
	t.Helper()
	r := recordingNow(t, e, id)
	fn(r)
	if err := e.Store.putEntity(KindRecording, id, r, &r.Tagging, false); err != nil {
		t.Fatal(err)
	}
}

// The material holds only the four sources: this meeting's upload title, calendar hint, live sentences and answered
// questions; earlier meetings' minutes (only from recordings that are meetings by kind or by request); their
// projects' name and status; their actions' to-do ids. Everything marked SECRET is somewhere else in the knowledge
// base and must not appear.
func TestMeetingMaterialOnlyMeetingMaterial(t *testing.T) {
	e := goldenEngine(t, nil)
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	day := 24 * time.Hour
	pinned := "SECRET-PINNED-FACT"

	// This meeting: attended, with a transcript, pinned facts, a summary and a matched calendar item of its own.
	u := attendedUpload(1, now, AttendanceParticipant)
	u.Title = "Design review"
	u.Calendar = &RecordingCalendar{Title: "Design review sync", Attendees: []string{"Ada", "Bo"}, Location: "Room 3"}
	u.Segments = []RecordingSegment{{Start: 0, End: 2, Text: "SECRET-THIS-TRANSCRIPT"}}
	putRecording(t, e, recID, u, now)
	if _, err := e.Apply(KindRecording, recID, Update{Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}
	changeRecording(t, e, recID, func(r *Recording) {
		r.Meeting, r.AITitle = &MeetingRef{Kind: KindEvent, ID: "ev1", Name: "SECRET-MATCHED-ITEM"}, "SECRET-AI-TITLE"
	})
	if err := e.Store.PutSummary(KindRecording, recID, "SECRET-THIS-SUMMARY"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Start: 1, End: 3, Text: "We keep the cap"}, {Start: 4, End: 4}, {Start: 5, End: 7, Text: "Bo has the test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.LiveUpdate(recID, func(l *Live, _ Recording) error {
		l.Asks = []LiveAsk{
			{ID: "a0000000-0000-4000-8000-000000000001", Question: "What did we decide last time?", QuestionSource: "typed", State: "answered", Answer: "We kept the cap.", AskedAt: now},
			{ID: "a0000000-0000-4000-8000-000000000002", Question: "SECRET-PENDING-ASK", QuestionSource: "typed", State: "pending", AskedAt: now},
			{ID: "a0000000-0000-4000-8000-000000000003", Question: "SECRET-FAILED-ASK", QuestionSource: "heard", State: "failed", Error: "model_error", AskedAt: now},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// An earlier meeting by kind, with everything around its minutes that must stay out.
	earlier := "11111111-1111-4111-8111-111111111111"
	eu := meetingUpload(1, now.Add(-7*day), "SECRET-EARLIER-TRANSCRIPT")
	eu.Attendance = AttendanceListener
	putRecording(t, e, earlier, eu, now)
	putMeetingMinutes(t, e, earlier, Minutes{Version: 3, Revision: 1, GeneratedAt: now, Title: "Design review", When: "2026-11-03 14:00–15:10",
		Where: "SECRET-WHERE", Attendees: []string{"Ada", "Bo"}, Partial: true, Markdown: "SECRET-MARKDOWN",
		Points:    []MinutesPoint{{Text: "Exports over 4 MB are dropped.", At: 312, Who: "Ada"}},
		Decisions: []MinutesItem{{Text: "Keep the cap for this release.", At: 1504}},
		Actions:   []MinutesAction{{ID: "a1", Who: "Bo", What: "Add the regression test.", By: "Friday", DueDate: "2026-11-07", At: 2100, TodoID: "todo-1"}},
		Open:      []MinutesItem{{Text: "Per entry or per recording?", At: 2408}}})
	if err := e.Store.PutSummary(KindRecording, earlier, "SECRET-EARLIER-SUMMARY"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.LiveAppend(earlier, 0, []LiveSegment{{Text: "SECRET-EARLIER-LIVE"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.LiveUpdate(earlier, func(l *Live, _ Recording) error {
		l.Asks = []LiveAsk{{ID: "b0000000-0000-4000-8000-000000000001", Question: "SECRET-EARLIER-ASK", State: "answered", Answer: "SECRET-EARLIER-ANSWER"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*Project{
		{Slug: "export", Name: "Export", Status: "active", Goal: "SECRET-GOAL", MyRole: "SECRET-ROLE", OneLine: "SECRET-ONELINE", People: []string{"lark-ada"}},
		{Slug: "archive", Name: "Archive", Status: "done"},
		{Slug: "noise", Name: "SECRET-IGNORED-PROJECT", Status: "ignored"},
		{Slug: "folded", Name: "SECRET-MERGED-PROJECT", Status: "merged", MergedInto: "export"},
	} {
		if err := e.Store.PutProject(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Store.PutSummary(KindProject, "export", "SECRET-PROJECT-SUMMARY"); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AppendLog("export", now, "Persona", "SECRET-PROJECT-LOG"); err != nil {
		t.Fatal(err)
	}
	changeRecording(t, e, earlier, func(r *Recording) {
		r.Projects, r.AITitle, r.Pinned = []string{"export", "noise", "folded", "missing", "archive", "export"}, "SECRET-EARLIER-AI-TITLE", "SECRET-EARLIER-PINNED"
	})

	// A note whose minutes the user asked for: eligible.
	asked := "22222222-2222-4222-8222-222222222222"
	putRecording(t, e, asked, recUpload(1, now.Add(-2*day), true, "SECRET-ASKED-TRANSCRIPT"), now)
	changeRecording(t, e, asked, func(r *Recording) { r.MinutesAsked = true })
	putMeetingMinutes(t, e, asked, Minutes{Version: 1, Title: "Weekly sync", When: "2026-11-08 10:00–10:30",
		Decisions: []MinutesItem{{Text: "Move the demo to Monday.", At: 10}}})

	// Never: a meeting only through a calendar overlap, a note with minutes from when it was one, a note, a meeting
	// without minutes.
	overlap := "33333333-3333-4333-8333-333333333333"
	ou := recUpload(1, now.Add(-3*day), true, "SECRET-OVERLAP-TRANSCRIPT")
	ou.Duration = 3600
	putRecording(t, e, overlap, ou, now)
	changeRecording(t, e, overlap, func(r *Recording) { r.Meeting = &MeetingRef{Kind: KindEvent, ID: "ev2", Name: "SECRET-OVERLAP-ITEM"} })
	putMeetingMinutes(t, e, overlap, Minutes{Version: 1, Title: "SECRET-OVERLAP-MINUTES", When: "x", Points: []MinutesPoint{{Text: "SECRET-OVERLAP-POINT"}}})
	note := "44444444-4444-4444-8444-444444444444"
	putRecording(t, e, note, recUpload(1, now.Add(-day), true, "SECRET-NOTE-TRANSCRIPT"), now)
	putMeetingMinutes(t, e, note, Minutes{Version: 1, Title: "SECRET-NOTE-MINUTES", When: "y"})
	plain := "55555555-5555-4555-8555-555555555555"
	putRecording(t, e, plain, recUpload(1, now.Add(-day), true, "SECRET-PLAIN-NOTE"), now)
	bare := "66666666-6666-4666-8666-666666666666"
	putRecording(t, e, bare, meetingUpload(1, now.Add(-day), "SECRET-BARE-MEETING"), now)
	// Never either: a meeting with minutes marked private, one with no mark (from before the mark, a #3746 phone, the
	// watch), and one whose upload said it is private as it started.
	for i, mark := range []*bool{new(true), nil, nil} {
		id := fmt.Sprintf("8888888%d-8888-4888-8888-888888888888", i)
		mu := meetingUpload(1, now.Add(-time.Duration(i+1)*time.Hour), "SECRET-PRIVATE-TRANSCRIPT")
		if i == 2 {
			mu.Private = new(true)
		}
		putRecording(t, e, id, mu, now)
		putMeetingMinutes(t, e, id, Minutes{Version: 1, Title: "SECRET-PRIVATE-MINUTES export cap", When: "z",
			Decisions: []MinutesItem{{Text: "SECRET-PRIVATE-DECISION export cap"}}})
		if mark != nil {
			markPrivate(t, e, id, *mark)
		}
	}
	// Marked not private: material.
	markPrivate(t, e, earlier, false)
	markPrivate(t, e, asked, false)
	if _, err := e.SetPrivate(overlap, false); !errors.Is(err, ErrNotMeeting) {
		t.Errorf("a recording that is not a meeting takes no mark: %v", err)
	}
	markPrivate(t, e, bare, false) // no minutes: still nothing

	// The rest of the knowledge base.
	chat := &Chat{ID: "lark-team", Source: "lark", SourceID: "team", Kind: "group", Name: "SECRET-CHAT-NAME"}
	if err := e.Store.PutChat(chat); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.AppendMessages(chat, []Line{{At: now.Add(-time.Hour), Sender: "Ada", Text: "SECRET-CHAT-MESSAGE about the export cap"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutSummary(KindChat, chat.ID, "SECRET-CHAT-SUMMARY"); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutPerson(&Person{ID: "lark-ada", Source: "lark", SourceID: "ada", Name: "Ada", Note: "SECRET-PERSON-NOTE"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutItem(&Item{Kind: KindDoc, ID: "d1", Title: "SECRET-DOC about the export cap", At: now}); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.PutItem(&Item{Kind: KindEvent, ID: "ev1", Title: "SECRET-EVENT", At: now}); err != nil {
		t.Fatal(err)
	}

	// Until a per-meeting private mark exists (#3862), production reads none of the earlier meetings,
	// their projects or action-item to-do IDs. This meeting's own material stays available.
	current, err := e.MeetingMaterial(recID, "What did we decide about the export cap?")
	if err != nil || current.EarlierAvailable || len(current.Earlier) != 0 || len(current.Lines) != 2 || len(current.Asks) != 1 || current.Calendar == nil {
		t.Fatalf("default material: %+v, %v", current, err)
	}
	e.EarlierMeetingsInAnswers = true
	mat, err := e.MeetingMaterial(strings.ToUpper(recID), "What did we decide about the export cap?")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(mat)
	for _, dump := range []string{string(raw), fmt.Sprintf("%+v", mat)} {
		if strings.Contains(dump, "SECRET") {
			t.Fatalf("private content in the material:\n%s", dump)
		}
	}
	if mat.Title != "Design review" || mat.Calendar == nil || mat.Calendar.Location != "Room 3" || mat.Language != "zh-CN" {
		t.Errorf("this meeting: %q %+v %q", mat.Title, mat.Calendar, mat.Language)
	}
	if len(mat.Lines) != 2 || mat.Lines[0].Text != "We keep the cap" || mat.Lines[1].Start != 5 {
		t.Errorf("lines %+v", mat.Lines)
	}
	if len(mat.Asks) != 1 || mat.Asks[0].Answer != "We kept the cap." {
		t.Errorf("asks %+v", mat.Asks)
	}
	if !mat.EarlierAvailable || len(mat.Earlier) != 2 || mat.Earlier[0].ID != earlier || mat.Earlier[1].ID != asked {
		t.Fatalf("earlier %+v", mat.Earlier)
	}
	first := mat.Earlier[0]
	if first.Name != "Design review" || first.When != "2026-11-03 14:00–15:10" {
		t.Errorf("named %q at %q", first.Name, first.When)
	}
	// Its minutes have no span of their own (written before #4713): the recording's.
	if er := recordingNow(t, e, earlier); !first.Start.Equal(er.StartedAt.Truncate(time.Second)) || !first.End.Equal(recordingEnd(er).Truncate(time.Second)) {
		t.Errorf("span %s – %s", first.Start, first.End)
	}
	want := Minutes{Title: "Design review", When: "2026-11-03 14:00–15:10", Attendees: []string{"Ada", "Bo"},
		Points:    []MinutesPoint{{Text: "Exports over 4 MB are dropped.", Who: "Ada"}},
		Decisions: []MinutesItem{{Text: "Keep the cap for this release."}},
		Actions:   []MinutesAction{{Who: "Bo", What: "Add the regression test.", By: "Friday", DueDate: "2026-11-07", TodoID: "todo-1"}},
		Open:      []MinutesItem{{Text: "Per entry or per recording?"}}}
	if got, exp := fmt.Sprintf("%+v", first.Minutes), fmt.Sprintf("%+v", want); got != exp {
		t.Errorf("minutes\n%s\nwant\n%s", got, exp)
	}
	if fmt.Sprint(first.Projects) != fmt.Sprint([]EarlierProject{{"export", "Export", "active"}, {"archive", "Archive", "done"}}) {
		t.Errorf("projects %+v", first.Projects)
	}
	if mat.Earlier[1].Name != "Weekly sync" || len(mat.Earlier[1].Projects) != 0 {
		t.Errorf("asked %+v", mat.Earlier[1])
	}

	if _, err := e.MeetingMaterial("77777777-7777-4777-8777-777777777777", ""); err != ErrNotFound {
		t.Errorf("no such recording: %v", err)
	}
	if _, err := e.DeleteRecording(asked, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.MeetingMaterial(asked, ""); !errors.Is(err, ErrRecordingDeleted) {
		t.Errorf("deleted: %v", err)
	}
	if mat, _ := e.MeetingMaterial(recID, ""); len(mat.Earlier) != 1 {
		t.Errorf("a deleted meeting is still material: %+v", mat.Earlier)
	}
}

// Earlier meetings rank by the question, then the same title, the title's words and shared attendees; the more
// recent first among equals; at most materialMaxEarlier.
func TestMeetingMaterialRanking(t *testing.T) {
	e := goldenEngine(t, nil)
	e.EarlierMeetingsInAnswers = true
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	u := attendedUpload(1, now, AttendanceListener)
	u.Title = "Platform weekly"
	u.Calendar = &RecordingCalendar{Attendees: []string{"Ada", "Cy"}}
	putRecording(t, e, recID, u, now)
	ids := make([]string, 9)
	for i := range ids {
		ids[i] = fmt.Sprintf("%08d-0000-4000-8000-000000000000", i+1)
		putRecording(t, e, ids[i], meetingUpload(1, now.Add(-time.Duration(i+1)*time.Hour), "text"), now)
		markPrivate(t, e, ids[i], false)
	}
	minutes := func(title string, attendees []string, decision string) Minutes {
		return Minutes{Version: 1, Title: title, When: title, Attendees: attendees, Decisions: []MinutesItem{{Text: decision}}}
	}
	// ids[0] is the most recent, ids[8] the oldest.
	putMeetingMinutes(t, e, ids[0], minutes("Hiring", nil, "Open two roles."))
	putMeetingMinutes(t, e, ids[1], minutes("Budget", nil, "Cut travel."))
	putMeetingMinutes(t, e, ids[2], minutes("Sales", []string{"ada"}, "New price list."))
	putMeetingMinutes(t, e, ids[3], minutes("Platform roadmap", nil, "Ship search."))
	putMeetingMinutes(t, e, ids[4], minutes("Offsite", nil, "Book the venue."))
	putMeetingMinutes(t, e, ids[5], minutes("Platform weekly", nil, "Nothing new."))
	putMeetingMinutes(t, e, ids[6], minutes("Launch", nil, "The export cap stays at 4 MB."))
	putMeetingMinutes(t, e, ids[7], minutes("Retro", nil, "More tests."))
	putMeetingMinutes(t, e, ids[8], minutes("Design", nil, "Keep the export cap; 导出上限不变."))
	mat, err := e.MeetingMaterial(recID, "导出上限 export cap")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range mat.Earlier {
		got = append(got, m.Name)
	}
	// The question's words (Design has them all), the same title with its words, some of the question's words
	// (Launch), an attendee or a title word (the more recent first), then the most recent of the rest.
	if want := "Design,Platform weekly,Launch,Sales,Platform roadmap,Hiring"; strings.Join(got, ",") != want {
		t.Fatalf("ranked %v, want %s", got, want)
	}
	again, _ := e.MeetingMaterial(recID, "导出上限 export cap")
	if fmt.Sprint(again.Earlier) != fmt.Sprint(mat.Earlier) {
		t.Fatal("not deterministic")
	}
}

func TestLatestLines(t *testing.T) {
	line := func(c string, n int) LiveSegment { return LiveSegment{Text: strings.Repeat(c, n)} }
	segs := []LiveSegment{line("a", 5000), line("b", 5000), {}, line("c", 5000), line("d", 5000)}
	got := latestLines(segs, materialLiveRunes)
	if len(got) != 3 || got[0].Text != strings.Repeat("b", 2000) || got[1].Text != segs[3].Text || got[2].Text != segs[4].Text {
		t.Fatalf("got %d lines, first %d runes", len(got), len([]rune(got[0].Text)))
	}
	if got := latestLines([]LiveSegment{line("中", 3), {}}, 2); len(got) != 1 || got[0].Text != "中中" {
		t.Fatalf("runes: %+v", got)
	}
	if got := latestLines(nil, materialLiveRunes); len(got) != 0 {
		t.Fatalf("none: %+v", got)
	}
}
