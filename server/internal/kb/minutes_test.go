package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/prompts"
)

// funcModel answers each call with f's reply (an error, or JSON).
type funcModel struct {
	f     func(n int, system, user string) (string, error)
	calls [][2]string
}

func (m *funcModel) JSONMax(_ context.Context, system, user string, _ int, out any) error {
	m.calls = append(m.calls, [2]string{system, user})
	reply, err := m.f(len(m.calls), system, user)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(reply), out)
}

// meetingUpload is a complete meeting recording of the given lines, 10 seconds each, started at start.
func meetingUpload(rev int64, start time.Time, texts ...string) RecordingUpload {
	u := recUpload(rev, start, true, texts...)
	u.Kind = RecordingMeeting
	return u
}

func putRecording(t *testing.T, e *Engine, id string, u RecordingUpload, now time.Time) {
	t.Helper()
	if st, err := e.PutRecording(id, u, now); err != nil || st.Status != RecordingStored {
		t.Fatalf("put %s: %v %v", id, st, err)
	}
}

func recordingNow(t *testing.T, e *Engine, id string) *Recording {
	t.Helper()
	r, ok := e.Store.Recording(id)
	if !ok {
		t.Fatalf("recording %s is gone", id)
	}
	return r
}

const minutesReply = `{
  "title": "发布评审",
  "where": "三号会议室",
  "attendees": ["Ada", "Bo", "Zed"],
  "points": [
    {"text": "导出超过 4 MB 的条目会被丢掉", "line": 2, "who": "Ada"},
    {"text": "编造的要点", "line": 99},
    {"text": "回归测试覆盖不到老版本", "line": "[4]", "who": "Zed"}
  ],
  "decisions": [{"text": "这一版先保留上限", "line": 3}],
  "actions": [
    {"who": "Bo", "what": "补上回归测试", "by": "周五前", "due_date": "2026-11-13", "line": "L4"},
    {"who": "", "what": "确认上限按条还是按录音算", "by": "", "due_date": "2025-01-01", "line": 5}
  ],
  "open": [{"text": "上限按条还是按录音", "line": 5}]
}`

// A meeting's minutes: every item at the time of the line it cites (others dropped), a person only when named in the
// transcript or the calendar, a due date only when it is a real day from the meeting on; stored next to the recording,
// readable from Get, searchable, told once.
func TestMinutesWritten(t *testing.T) {
	m := &captureModel{reply: minutesReply}
	e := goldenEngine(t, m)
	var told []Minutes
	e.MinutesWritten = func(_ context.Context, r Recording, mm Minutes) { told = append(told, mm) }
	now := time.Date(2026, 11, 10, 16, 0, 0, 0, goldenZone)
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	u := meetingUpload(1, start, "大家好，今天评审导出", "Ada 说导出超过 4 MB 的条目会被丢掉", "那这一版先保留上限", "Bo 你周五前补上回归测试", "上限按条还是按录音算还没定")
	u.Calendar = &RecordingCalendar{Title: " 发布评审 ", Attendees: []string{"Ada", " Cy ", "Ada", ""}, Location: ""}
	putRecording(t, e, recID, u, now)
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending || r.Name != "发布评审" || r.Kind != RecordingMeeting {
		t.Fatalf("after upload: state %q name %q kind %q", r.MinutesState, r.Name, r.Kind)
	}
	if n := e.WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	m.golden(t, "minutes")
	got, err := e.Store.ReadMinutes(recID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || got.Revision != 1 || got.Title != "发布评审" || got.When != "2026-11-10 14:00–14:01" {
		t.Errorf("head %+v", got)
	}
	// The same span for clients to format themselves (#4713), in the user's zone, to the second.
	if got.Start.Format(time.RFC3339Nano) != start.Format(time.RFC3339) || !got.End.Equal(start.Add(time.Minute)) {
		t.Errorf("span %s – %s", got.Start.Format(time.RFC3339Nano), got.End.Format(time.RFC3339Nano))
	}
	// Calendar attendees first; the model's only when the transcript names them (Zed is not).
	if strings.Join(got.Attendees, ",") != "Ada,Cy,Bo" {
		t.Errorf("attendees %v", got.Attendees)
	}
	if got.Where != "" {
		t.Errorf("where %q: the transcript never says it", got.Where)
	}
	if len(got.Points) != 2 || got.Points[0].At != 10 || got.Points[0].Who != "Ada" || got.Points[1].At != 30 || got.Points[1].Who != "" {
		t.Errorf("points %+v", got.Points)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].At != 20 {
		t.Errorf("decisions %+v", got.Decisions)
	}
	if len(got.Actions) != 2 || got.Actions[0] != (MinutesAction{ID: "a1", Who: "Bo", What: "补上回归测试", By: "周五前", DueDate: "2026-11-13", At: 30}) ||
		got.Actions[1].ID != "a2" || got.Actions[1].DueDate != "" {
		t.Errorf("actions %+v", got.Actions)
	}
	if len(got.Open) != 1 || got.Open[0].At != 40 {
		t.Errorf("open %+v", got.Open)
	}
	for _, want := range []string{"# 发布评审", "- 参会：Ada、Cy、Bo", "## 结论与决定", "- [14:00:30] Bo：补上回归测试（周五前）", "## 未决问题"} {
		if !strings.Contains(got.Markdown, want) {
			t.Errorf("markdown lacks %q:\n%s", want, got.Markdown)
		}
	}
	if md, _ := os.ReadFile(e.Store.Path(KindRecording, recID, "minutes.md")); strings.TrimSpace(string(md)) != got.Markdown {
		t.Errorf("minutes.md %q", md)
	}
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesReady || r.MinutesVersion != 1 || r.MinutesRev != 1 {
		t.Errorf("after: %q v%d rev %d", r.MinutesState, r.MinutesVersion, r.MinutesRev)
	}
	if len(told) != 1 || told[0].Version != 1 {
		t.Errorf("told %d times", len(told))
	}
	d, err := e.Get(KindRecording, recID, 100)
	if err != nil || !strings.Contains(string(d.Minutes), `"补上回归测试"`) {
		t.Fatalf("Get: %v %s", err, d.Minutes)
	}
	// Searched in minutes.md, never twice through minutes.json.
	hits := e.Search("补上回归测试", false, 40)
	var files []string
	for _, h := range hits {
		files = append(files, h.Path)
	}
	if want := "recordings/" + recID + "/minutes.md"; !strings.Contains(strings.Join(files, " "), want) || strings.Contains(strings.Join(files, " "), "minutes.json") {
		t.Errorf("search hits %v", files)
	}
	found := e.Find("保留上限", 10)
	if len(found) != 1 || found[0].Entry.ID != recID || !strings.HasSuffix(found[0].Hits[0].Path, "/minutes.md") {
		t.Errorf("find %+v", found)
	}
	// Opening the search result finds what only the minutes say.
	if ms := e.Matches(KindRecording, recID, "覆盖不到老版本", 40); len(ms) != 1 || !strings.HasSuffix(ms[0].Path, "/minutes.md") {
		t.Errorf("matches %+v", ms)
	}
	// Done: nothing more to write, no second push.
	if n := e.WriteMinutes(context.Background(), 2); n != 0 || len(told) != 1 {
		t.Errorf("again: wrote %d, told %d", n, len(told))
	}
}

// An ordinary recording gets no minutes; one overlapping a calendar item of the knowledge base (and long enough) is a
// meeting named after it, the event winning over the video meeting; asked for, any complete recording is one.
func TestMinutesCalendarAndAsking(t *testing.T) {
	calls := 0
	model := &funcModel{f: func(int, string, string) (string, error) {
		calls++
		return `{"title":"随便聊","points":[{"text":"聊了排期","line":1}]}`, nil
	}}
	e := goldenEngine(t, model)
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, goldenZone)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, goldenZone) }
	for _, it := range []*Item{
		{Kind: KindEvent, ID: "e1", Title: "周会", At: at(9, 55)},
		{Kind: KindMeeting, ID: "m1", Title: "周会（视频）", At: at(10, 1)},
		{Kind: KindEvent, ID: "e2", Title: "太早的会", At: at(9, 30)},
		{Kind: KindEvent, ID: "e3", Title: "午饭", At: at(12, 0)},
	} {
		if err := e.Store.PutItem(it); err != nil {
			t.Fatal(err)
		}
	}
	long := recUpload(1, at(10, 0), true, strings.Split(strings.Repeat("聊了排期 ", 40), " ")[:40]...) // 400 s
	putRecording(t, e, recID, long, now)
	const short, alone = "1f8fad5b-d9cb-469f-a165-70867728950e", "2f8fad5b-d9cb-469f-a165-70867728950e"
	putRecording(t, e, short, recUpload(1, at(10, 2), true, "一句话"), now)
	putRecording(t, e, alone, recUpload(1, at(15, 0), true, "自己的笔记"), now)
	if n := e.WriteMinutes(context.Background(), 5); n != 1 || calls != 1 {
		t.Fatalf("wrote %d with %d calls, want only the long one", n, calls)
	}
	r := recordingNow(t, e, recID)
	if r.Meeting == nil || r.Meeting.ID != "e1" || r.Name != "周会" || strings.Join(r.Items, ",") != "events/e1" || r.MinutesState != MinutesReady {
		t.Fatalf("long: %+v %q %v %q", r.Meeting, r.Name, r.Items, r.MinutesState)
	}
	if mm, _ := e.Store.ReadMinutes(recID); mm == nil || mm.Title != "周会" {
		t.Fatalf("minutes %+v", mm)
	}
	d, _ := e.Get(KindRecording, recID, 0)
	if len(d.Links) != 1 || d.Links[0].ID != "e1" {
		t.Errorf("links %+v", d.Links)
	}
	if s := recordingNow(t, e, short); s.Meeting == nil || s.MinutesState != "" {
		t.Errorf("short: matched %+v, state %q (too short to be a meeting by the calendar alone)", s.Meeting, s.MinutesState)
	}
	if a := recordingNow(t, e, alone); a.Meeting != nil || a.MinutesState != "" {
		t.Errorf("alone: %+v %q", a.Meeting, a.MinutesState)
	}
	// Asked for: a meeting from now on, made at the next round.
	if err := e.AskMinutes(alone); err != nil {
		t.Fatal(err)
	}
	if a := recordingNow(t, e, alone); a.MinutesState != MinutesPending || !a.MinutesAsked {
		t.Fatalf("asked: %q", a.MinutesState)
	}
	if n := e.WriteMinutes(context.Background(), 5); n != 1 {
		t.Fatalf("asked: wrote %d", n)
	}
	// Asked again: made again, a new version.
	if err := e.AskMinutes(strings.ToUpper(alone)); err != nil {
		t.Fatal(err)
	}
	e.WriteMinutes(context.Background(), 5)
	if mm, _ := e.Store.ReadMinutes(alone); mm == nil || mm.Version != 2 {
		t.Fatalf("again: %+v", mm)
	}
	if err := e.AskMinutes("3f8fad5b-d9cb-469f-a165-70867728950e"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such recording: %v", err)
	}
	const open = "4f8fad5b-d9cb-469f-a165-70867728950e"
	putRecording(t, e, open, recUpload(1, at(16, 0), false, "还在录"), now)
	if err := e.AskMinutes(open); !errors.Is(err, ErrRecordingIncomplete) {
		t.Errorf("incomplete: %v", err)
	}
}

// No connection or the model paused: still pending, nothing counted. Answers that cite nothing count; the third makes
// them failed, until a new revision or a regenerate.
func TestMinutesFailures(t *testing.T) {
	var err error
	reply := `{"points":[{"text":"编的","line":42}]}`
	model := &funcModel{f: func(int, string, string) (string, error) { return reply, err }}
	e := goldenEngine(t, model)
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour)
	putRecording(t, e, recID, meetingUpload(1, start, "第一句", "第二句"), now)
	err = errors.New("dial tcp: connection refused")
	for range 5 {
		e.WriteMinutes(context.Background(), 2)
	}
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending || r.MinutesFails != 0 {
		t.Fatalf("unreachable: %q fails %d", r.MinutesState, r.MinutesFails)
	}
	err = nil
	for i := range minutesTries {
		e.WriteMinutes(context.Background(), 2)
		want := MinutesPending
		if i == minutesTries-1 {
			want = MinutesFailed
		}
		if r := recordingNow(t, e, recID); r.MinutesState != want || r.MinutesFails != i+1 {
			t.Fatalf("unusable %d: %q fails %d", i+1, r.MinutesState, r.MinutesFails)
		}
	}
	calls := len(model.calls)
	e.WriteMinutes(context.Background(), 2)
	if len(model.calls) != calls {
		t.Fatal("a failed revision is asked again")
	}
	for _, unusable := range []error{ai.ErrNoObject, ai.ErrRefused, fmt.Errorf("%w (max_tokens 6000)", ai.ErrTruncated), &json.SyntaxError{}} {
		if !unusableAnswer(unusable) {
			t.Errorf("%v should count", unusable)
		}
	}
	if unusableAnswer(errors.New("ai: HTTP 503: overloaded")) {
		t.Error("an HTTP error should not count")
	}
	// A new revision starts over.
	putRecording(t, e, recID, meetingUpload(2, start, "第一句", "第二句", "第三句"), now)
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending {
		t.Fatalf("new revision: %q", r.MinutesState)
	}
	reply = `{"title":"t","decisions":[{"text":"定了","line":3}]}`
	if n := e.WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	// No model at all: nothing happens, nothing counted.
	e.Model = nil
	if err := e.AskMinutes(recID); err != nil {
		t.Fatal(err)
	}
	if n := e.WriteMinutes(context.Background(), 2); n != 0 {
		t.Fatal("wrote without a model")
	}
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending {
		t.Fatalf("no model: %q", r.MinutesState)
	}
}

// A long transcript is read in chunks, each cited line checked against its chunk, then merged; the merge may cite only
// lines that were read.
func TestMinutesChunked(t *testing.T) {
	var texts []string
	for i := range 300 {
		texts = append(texts, fmt.Sprintf("第 %d 句：%s", i+1, strings.Repeat("讨论内容", 20)))
	}
	model := &funcModel{f: func(n int, system, user string) (string, error) {
		if strings.Contains(system, "已经分段整理过") {
			if !strings.Contains(user, "## 第 1 段") || !strings.Contains(user, "[1] 开场") || strings.Contains(user, "[9999]") {
				return "", fmt.Errorf("merge input:\n%s", user)
			}
			// 150 is a line of the transcript, but no part's notes cite it: the merge can't have read it.
			return `{"title":"长会","points":[{"text":"开场","line":1},{"text":"收尾","line":300},{"text":"没读到的","line":301}],"decisions":[{"text":"定了","line":150}]}`, nil
		}
		first := strings.SplitN(strings.SplitN(user, "转写：\n", 2)[1], " ", 2)[0] // "[N]"
		last := ""
		if strings.Contains(user, "[300] ") {
			last = `,{"text":"收尾","line":300}`
		}
		return fmt.Sprintf(`{"points":[{"text":"开场","line":%q},{"text":"不在这段","line":9999}%s]}`, first, last), nil
	}}
	e := goldenEngine(t, model)
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, goldenZone)
	putRecording(t, e, recID, meetingUpload(1, now.Add(-2*time.Hour), texts...), now)
	if n := e.WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	parts := len(model.calls) - 1
	if parts < 3 {
		t.Fatalf("%d parts", parts)
	}
	if !strings.Contains(model.calls[0][1], "这是第 1 / "+fmt.Sprint(parts)+" 段") {
		t.Errorf("part input:\n%s", model.calls[0][1])
	}
	mm, _ := e.Store.ReadMinutes(recID)
	if mm == nil || len(mm.Points) != 2 || mm.Points[1].At != 2990 || len(mm.Decisions) != 0 || mm.Partial {
		t.Fatalf("minutes %+v", mm)
	}
	chunks, partial := chunkLines([]minutesLine{{N: 1, Text: "aaaa"}, {N: 2, Text: "bbbb"}, {N: 3, Text: "cccc"}}, 10, 25, 2)
	if len(chunks) != 2 || !partial {
		t.Errorf("chunks %v partial %v", chunks, partial)
	}
}

// Deleted while the model was writing: the minutes don't bring the directory back. A new revision keeps the old
// minutes showing (pending) until new ones are written; confirmed to-dos are recorded for their version only.
func TestMinutesDeleteRevisionAndTodos(t *testing.T) {
	var e *Engine
	deleteDuring := false
	model := &funcModel{f: func(int, string, string) (string, error) {
		if deleteDuring {
			if _, err := e.DeleteRecording(recID, time.Now()); err != nil {
				return "", err
			}
		}
		return `{"title":"t","actions":[{"what":"写计划","line":1},{"what":"发邮件","line":2}]}`, nil
	}}
	e = goldenEngine(t, model)
	now := time.Now()
	putRecording(t, e, recID, meetingUpload(1, now.Add(-time.Hour), "写计划", "发邮件"), now)
	e.WriteMinutes(context.Background(), 2)
	if _, err := e.SetMinutesTodos(recID, 2, map[string]string{"a1": "todo_x"}, nil); !errors.Is(err, ErrMinutesStale) {
		t.Fatalf("stale version: %v", err)
	}
	mm, err := e.SetMinutesTodos(recID, 1, map[string]string{"a1": "todo_x"}, nil)
	if err != nil || mm.Actions[0].TodoID != "todo_x" || mm.Actions[1].TodoID != "" {
		t.Fatalf("set: %v %+v", err, mm)
	}
	if again, _ := e.Store.ReadMinutes(recID); again.Actions[0].TodoID != "todo_x" {
		t.Fatal("not stored")
	}
	// Confirmed from version 1 while version 2 was written: linked by task in version 2.
	mm.Version, mm.Actions[0].TodoID = 2, ""
	if _, err := e.Store.putMinutes(recID, mm); err != nil {
		t.Fatal(err)
	}
	mm, err = e.SetMinutesTodos(recID, 1, map[string]string{"a1": "todo_x"}, map[string]string{MinutesTaskKey(mm.Actions[0]): "todo_x"})
	if err != nil || mm.Version != 2 || mm.Actions[0].TodoID != "todo_x" || mm.Actions[1].TodoID != "" {
		t.Fatalf("by task: %v %+v", err, mm)
	}
	putRecording(t, e, recID, meetingUpload(2, now.Add(-time.Hour), "写计划", "发邮件", "补一句"), now)
	r := recordingNow(t, e, recID)
	if old, _ := e.Store.ReadMinutes(recID); r.MinutesState != MinutesPending || old == nil || old.Revision != 1 {
		t.Fatalf("new revision: %q %+v", r.MinutesState, old)
	}
	if states, _ := e.RecordingStates(); len(states) != 1 || states[0].MinutesState != MinutesPending {
		t.Fatalf("states %+v", states)
	}
	deleteDuring = true
	e.WriteMinutes(context.Background(), 2)
	if _, err := os.Stat(e.Store.Path(KindRecording, recID)); !os.IsNotExist(err) {
		t.Fatalf("the deleted recording's directory is back: %v", err)
	}
	if _, err := e.Store.ReadMinutes(recID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("minutes after delete: %v", err)
	}
}

// What the phone may send: a kind of meeting or note (any case), a calendar hint within its limits.
func TestRecordingUploadKindAndCalendar(t *testing.T) {
	e := goldenEngine(t, nil)
	now := time.Now()
	u := recUpload(1, now.Add(-time.Minute), false, "一句")
	u.Kind = " Meeting "
	u.Calendar = &RecordingCalendar{Title: "  ", Attendees: []string{" ", ""}}
	putRecording(t, e, recID, u, now)
	if r := recordingNow(t, e, recID); r.Kind != RecordingMeeting || r.Calendar != nil || r.MinutesState != MinutesPending {
		t.Fatalf("%q %+v %q", r.Kind, r.Calendar, r.MinutesState)
	}
	u.Revision, u.Kind = 2, ""
	putRecording(t, e, recID, u, now)
	if r := recordingNow(t, e, recID); r.Kind != RecordingNote || r.MinutesState != "" {
		t.Fatalf("note: %q %q", r.Kind, r.MinutesState)
	}
	for _, bad := range []RecordingUpload{
		{Kind: "lecture"},
		{Calendar: &RecordingCalendar{Title: strings.Repeat("长", maxCalendarTitle+1)}},
		{Calendar: &RecordingCalendar{Title: "t", Attendees: make([]string, maxCalendarAttendees+1)}},
		{Calendar: &RecordingCalendar{Title: "t", Attendees: []string{strings.Repeat("名", maxCalendarName+1)}}},
	} {
		bad.Revision, bad.StartedAt, bad.Device = 3, now, "phone"
		if _, err := e.PutRecording(recID, bad, now); !errors.Is(err, ErrBadRecording) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	var l modelLine
	for in, want := range map[string]modelLine{`12`: 12, `"12"`: 12, `"[12]"`: 12, `"L7"`: 7, `"#3"`: 3, `0`: 0, `-2`: 0, `1.5`: 0, `"x"`: 0, `null`: 0, `{}`: 0} {
		l = 0
		if err := json.Unmarshal([]byte(in), &l); err != nil || l != want {
			t.Errorf("%s: %d %v", in, l, err)
		}
	}
}

// The merging call of a long meeting: the parts' notes, each item at its cited line.
func TestGoldenMinutesMerge(t *testing.T) {
	start := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	in := prompts.KBMinutesMergeInput{
		Meeting: prompts.KBMinutesInput{Title: "发布评审", Meeting: "周会", Start: start, End: start.Add(2 * time.Hour), Weekday: "周二",
			Minutes: 120, Language: "zh-CN", Attendees: []string{"Ada", "Bo"}, Location: "三号会议室"},
		Parts: []prompts.KBMinutesPart{
			{Part: 1, From: "14:00:00", To: "14:59:50", Attendees: []string{"Ada"},
				Points:    []prompts.KBMinutesNote{{Line: 3, Text: "导出超过 4 MB 的条目会被丢掉", Who: "Ada"}},
				Decisions: []prompts.KBMinutesNote{{Line: 40, Text: "这一版先保留上限"}}},
			{Part: 2, From: "15:00:00", To: "15:59:50", Where: "三号会议室",
				Actions: []prompts.KBMinutesNote{{Line: 410, Text: "补上回归测试", Who: "Bo", By: "周五前", DueDate: "2026-11-13"}},
				Open:    []prompts.KBMinutesNote{{Line: 700, Text: "上限按条还是按录音"}}},
		},
	}
	golden(t, "minutes_merge.system", prompts.RenderKBMinutesMerge(prompts.KBOwner{Owner: "示例用户"}))
	golden(t, "minutes_merge.user", prompts.RenderKBMinutesMergeInput(in))
}

// A calendar item collected after the recording ended still names it within minutesMatchWait; after that, a recording
// nothing overlaps is left alone.
func TestMinutesLateCalendar(t *testing.T) {
	e := goldenEngine(t, &funcModel{f: func(int, string, string) (string, error) {
		return `{"title":"t","points":[{"text":"聊了排期","line":1}]}`, nil
	}})
	now := time.Now()
	long := strings.Split(strings.Repeat("聊了排期 ", 40), " ")[:40] // 400 s
	putRecording(t, e, recID, recUpload(1, now.Add(-10*time.Minute), true, long...), now)
	const old = "5f8fad5b-d9cb-469f-a165-70867728950e"
	putRecording(t, e, old, recUpload(1, now.Add(-3*time.Hour), true, long...), now)
	e.WriteMinutes(context.Background(), 2)
	if r := recordingNow(t, e, recID); r.Meeting != nil || r.MatchedRev != 0 || r.MinutesState != "" {
		t.Fatalf("no item yet: %+v rev %d %q", r.Meeting, r.MatchedRev, r.MinutesState)
	}
	if r := recordingNow(t, e, old); r.MatchedRev != 1 {
		t.Fatalf("an old recording nothing overlaps is not settled: rev %d", r.MatchedRev)
	}
	for _, it := range []*Item{
		{Kind: KindMeeting, ID: "m9", Title: "临时讨论", At: now.Add(-9 * time.Minute)},
		{Kind: KindMeeting, ID: "m8", Title: "更早的讨论", At: now.Add(-3*time.Hour + time.Minute)},
	} {
		if err := e.Store.PutItem(it); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.WriteMinutes(context.Background(), 2); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	if r := recordingNow(t, e, recID); r.Meeting == nil || r.Meeting.ID != "m9" || r.Name != "临时讨论" || r.MinutesState != MinutesReady {
		t.Fatalf("late item: %+v %q %q", r.Meeting, r.Name, r.MinutesState)
	}
	if r := recordingNow(t, e, old); r.Meeting != nil {
		t.Fatalf("settled recording matched later: %+v", r.Meeting)
	}
}

// Made again: a version past both meta.json's and minutes.json's (a crash may have left them apart), to-dos already
// made carried over to the same task for the same person, the same task for two people kept as two; asked again
// while a model call was running, the minutes it brings don't settle the new request.
func TestMinutesRemade(t *testing.T) {
	var e *Engine
	askDuring := false
	model := &funcModel{f: func(int, string, string) (string, error) {
		if askDuring {
			askDuring = false
			if err := e.AskMinutes(recID); err != nil {
				return "", err
			}
		}
		return `{"title":"t","actions":[{"who":"Ada","what":"写计划","line":1},{"who":"Bo","what":"写计划","line":1},{"what":"发邮件","line":2}]}`, nil
	}}
	e = goldenEngine(t, model)
	now := time.Now()
	putRecording(t, e, recID, meetingUpload(1, now.Add(-time.Hour), "Ada 和 Bo 都写计划", "发邮件"), now)
	e.WriteMinutes(context.Background(), 2)
	m, _ := e.Store.ReadMinutes(recID)
	if m == nil || len(m.Actions) != 3 {
		t.Fatalf("actions %+v", m)
	}
	if _, err := e.SetMinutesTodos(recID, 1, map[string]string{"a2": "todo_bo"}, nil); err != nil {
		t.Fatal(err)
	}
	// minutes.json one version ahead of meta.json.
	m, _ = e.Store.ReadMinutes(recID)
	m.Version = 5
	if _, err := e.Store.putMinutes(recID, m); err != nil {
		t.Fatal(err)
	}
	askDuring = true
	if err := e.AskMinutes(recID); err != nil {
		t.Fatal(err)
	}
	e.WriteMinutes(context.Background(), 2)
	m, _ = e.Store.ReadMinutes(recID)
	if m.Version != 6 || m.Actions[1].Who != "Bo" || m.Actions[1].TodoID != "todo_bo" || m.Actions[0].TodoID != "" {
		t.Fatalf("remade: v%d %+v", m.Version, m.Actions)
	}
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesPending || r.MinutesVersion != 6 {
		t.Fatalf("asked during the call: %q v%d", r.MinutesState, r.MinutesVersion)
	}
	e.WriteMinutes(context.Background(), 2)
	if r := recordingNow(t, e, recID); r.MinutesState != MinutesReady || r.MinutesVersion != 7 {
		t.Fatalf("then: %q v%d", r.MinutesState, r.MinutesVersion)
	}
	for _, unusable := range []error{ai.ErrUnterminated, fmt.Errorf("part 1 of 2: %w", ai.ErrNoObject)} {
		if !unusableAnswer(unusable) {
			t.Errorf("%v should count", unusable)
		}
	}
}

// What the model's answer must hold to count: something citing a line it read; a copy citing nothing doesn't hide one
// that does; a date only for a deadline said; a name in Latin letters only as a whole word written so.
func TestMinutesAnswerRules(t *testing.T) {
	e := goldenEngine(t, nil)
	r := &Recording{ID: recID, StartedAt: time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone), Duration: 600,
		Calendar: &RecordingCalendar{Attendees: []string{"Ada"}}}
	read := []minutesLine{{N: 1, At: 0, Text: "we will ship on Friday, planning with Ann"}, {N: 2, At: 10, Text: "张三 来写测试"}}
	build := func(reply string) (*Minutes, error) {
		var a minutesAnswer
		if err := json.Unmarshal([]byte(reply), &a); err != nil {
			t.Fatal(err)
		}
		return e.buildMinutes(r, a, read, goldenZone)
	}
	for _, empty := range []string{`{}`, `{"title":"无法总结"}`, `{"points":[{"text":"","line":1}]}`} {
		if _, err := build(empty); !errors.Is(err, errMinutesUnusable) {
			t.Errorf("%s: %v", empty, err)
		}
	}
	m, err := build(`{"attendees":["Will","Ann","Annie","张三","Ada","Zoe"],
	  "decisions":[{"text":"Ship Friday","line":999},{"text":"Ship Friday","line":1}],
	  "actions":[{"who":"Will","what":"ship","line":1},{"who":"张三","what":"写测试","due_date":"2026-11-12","line":2},
	             {"who":"Ann","what":"plan","by":"周四","due_date":"2026-11-12","line":1},{"what":"old","by":"去年","due_date":"2025-11-12","line":1}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Decisions) != 1 || m.Decisions[0].At != 0 {
		t.Errorf("decisions %+v", m.Decisions)
	}
	if got := strings.Join(m.Attendees, ","); got != "Ada,Ann,张三" {
		t.Errorf("attendees %s", got)
	}
	byWhat := map[string]MinutesAction{}
	for _, a := range m.Actions {
		byWhat[a.What] = a
	}
	if len(m.Actions) != 4 || byWhat["ship"].Who != "" || byWhat["plan"].Who != "Ann" || byWhat["plan"].DueDate != "2026-11-12" ||
		byWhat["old"].DueDate != "" || byWhat["写测试"].Who != "张三" || byWhat["写测试"].DueDate != "" {
		t.Errorf("actions %+v", m.Actions)
	}
	for in, want := range map[[2]string]bool{{"Ann said", "Ann"}: true, {"planning", "Ann"}: false, {"we will", "Will"}: false,
		{"Will, go", "Will"}: true, {"ask O'Neil", "O'Neil"}: true, {"张三来了", "张三"}: true, {"Bo2 left", "Bo"}: false} {
		if got := spoken(in[0], in[1]); got != want {
			t.Errorf("spoken(%q, %q) = %v", in[0], in[1], got)
		}
	}
	if dueDate("2026-11-10", "2026-11-10") != "2026-11-10" || dueDate("2026-11-09", "2026-11-10") != "" || dueDate("2027-11-11", "2026-11-10") != "" {
		t.Error("dueDate range")
	}
}

// A recording that stops being a meeting keeps the minutes it has and asks for no more; at most max are tried a round,
// written or not.
func TestMinutesNoLongerMeetingAndRoundBound(t *testing.T) {
	model := &funcModel{f: func(int, string, string) (string, error) { return `{"points":[{"text":"x","line":1}]}`, nil }}
	e := goldenEngine(t, model)
	now := time.Now()
	putRecording(t, e, recID, meetingUpload(1, now.Add(-time.Hour), "一句"), now)
	e.WriteMinutes(context.Background(), 2)
	u := recUpload(2, now.Add(-time.Hour), true, "一句", "又一句")
	putRecording(t, e, recID, u, now)
	if r := recordingNow(t, e, recID); r.MinutesState != "" {
		t.Fatalf("no longer a meeting: %q", r.MinutesState)
	}
	if m, err := e.Store.ReadMinutes(recID); err != nil || m.Version != 1 {
		t.Fatalf("its minutes: %+v %v", m, err)
	}
	calls := len(model.calls)
	e.WriteMinutes(context.Background(), 2)
	if len(model.calls) != calls {
		t.Fatal("made again for a recording that is no meeting")
	}
	model.f = func(int, string, string) (string, error) { return `{"points":[{"text":"x","line":99}]}`, nil }
	for i, id := range []string{"6f8fad5b-d9cb-469f-a165-70867728950e", "7f8fad5b-d9cb-469f-a165-70867728950e", "8f8fad5b-d9cb-469f-a165-70867728950e"} {
		putRecording(t, e, id, meetingUpload(1, now.Add(-time.Duration(i+1)*time.Minute), "一句"), now)
	}
	calls = len(model.calls)
	e.WriteMinutes(context.Background(), 2)
	if got := len(model.calls) - calls; got != 2 {
		t.Fatalf("%d tried in a round, want 2", got)
	}
}

// Minutes written before they carried start and end (#4713) get them from their recording in a recording's detail;
// every other field stays as stored, and minutes that have them are sent as they are.
func TestMinutesWithSpan(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*3600)
	started := time.Date(2026, 11, 10, 6, 0, 0, 500_000_000, time.UTC)
	ended := started.Add(70 * time.Minute)
	r := &Recording{StartedAt: started, EndedAt: &ended}
	old := []byte(`{"version":2,"title":"Design review","when":"2026-11-10 14:00–15:10","markdown":"# x","future":{"kept":true}}`)
	var got map[string]any
	if err := json.Unmarshal(minutesWithSpan(old, r, zone), &got); err != nil {
		t.Fatal(err)
	}
	if got["start"] != "2026-11-10T14:00:00+08:00" || got["end"] != "2026-11-10T15:10:00+08:00" {
		t.Errorf("span %v – %v", got["start"], got["end"])
	}
	if got["version"] != float64(2) || got["when"] != "2026-11-10 14:00–15:10" || got["markdown"] != "# x" || got["future"] == nil {
		t.Errorf("stored fields changed: %v", got)
	}
	current := []byte(`{"version":3,"when":"x","start":"2026-11-10T14:00:00+08:00","end":"2026-11-10T15:10:00+08:00"}`)
	for name, c := range map[string]struct {
		raw []byte
		r   *Recording
	}{"has its span": {current, r}, "no recording": {old, nil}, "recording without a start": {old, &Recording{}}, "not JSON": {[]byte("{"), r}} {
		if out := minutesWithSpan(c.raw, c.r, zone); string(out) != string(c.raw) {
			t.Errorf("%s: %s", name, out)
		}
	}
}
