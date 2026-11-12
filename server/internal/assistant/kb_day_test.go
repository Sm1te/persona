package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/rpc"
)

// All-day recordings through the manager's HTTP, their recap's push, to-dos, kb_get and the evening routine's facts
// (kb_day.go, briefing_day.go; #4741). All names and texts are placeholders.

const (
	dayRec   = "55555555-5555-4555-8555-555555555555"
	dayPart2 = "66666666-6666-4666-8666-666666666666"
)

// dayModel answers a chunk's notes and the day's merge with fixed answers citing lines 1–3 (the knowledge base keeps
// of a chunk's notes only what cites its own lines), and counts them.
type dayModel struct {
	mu            sync.Mutex
	notes, merges int
}

const (
	dayNotesReply = `{"summary":"A standup and the export review.","entries":[` +
		`{"from_line":1,"to_line":1,"kind":"outside","title":"Standup","points":[{"text":"Standup on the export","line":1}]},` +
		`{"from_line":2,"to_line":3,"kind":"meeting","title":"Export review","points":[{"text":"The export drops big entries","line":2,"who":"Ada"},` +
		`{"text":"Bo takes the flaky test","line":3}]}],"actions":[{"who":"Bo","what":"Fix the flaky upload test","line":3}]}`
	dayMergeReply = `{"summary":"A standup, then the export review.","timeline":[` +
		`{"from_line":1,"to_line":1,"kind":"outside","title":"Standup","meeting":0,"points":[{"text":"Standup on the export","line":1}]},` +
		`{"from_line":2,"to_line":3,"kind":"meeting","title":"Export review","meeting":0,"points":[{"text":"The export drops big entries","line":2},` +
		`{"text":"Bo takes the flaky test","line":3}]}],"actions":[{"who":"Bo","what":"Fix the flaky upload test","by":"tomorrow","line":3}]}`
)

func (m *dayModel) JSONMax(_ context.Context, system, _ string, _ int, out any) error {
	m.mu.Lock()
	reply := dayNotesReply
	if strings.Contains(system, "写这一天的全天回顾") {
		reply, m.merges = dayMergeReply, m.merges+1
	} else {
		m.notes++
	}
	m.mu.Unlock()
	return json.Unmarshal([]byte(reply), out)
}

// dayFixture is the attending routes' fixture with the knowledge base's model answering recaps, in an owner's zone
// where it is about noon now (Etc/GMT±n), so the recap time is either well before now or well after it whatever the
// hour the test runs at.
type dayFixture struct {
	*meetingFixture
	model  *dayModel
	offset int // the owner's zone, hours east of UTC
	owner  *time.Location
}

func newDayFixture(t *testing.T) *dayFixture {
	t.Helper()
	m := newMeetingFixture(t)
	offset := 12 - time.Now().UTC().Hour() // -11 … 12: local time is 12:xx
	d := &dayFixture{meetingFixture: m, model: &dayModel{}, offset: offset, owner: etcZone(t, offset)}
	setSettings(t, m.s, func(v *Settings) { v.Timezone = d.owner.String() }) // before the knowledge base opens in it
	e := m.engine()
	e.Model, e.ModelFor = d.model, func(string) kb.Model { return d.model }
	// The hub's Lark bot (not paired) and channels (none), which the day's push asks of: it waits for them (kb_day.go).
	m.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) { return FeishuStatus{}, nil })
	m.f.answerApp(AppChannels, ChannelsList, func(json.RawMessage) (any, error) { return map[string]any{"channels": []channel.View{}}, nil })
	return d
}

// etcZone is the IANA zone hours east of UTC (Etc/GMT's signs are the other way round).
func etcZone(t *testing.T, hours int) *time.Location {
	t.Helper()
	name := "Etc/GMT"
	switch {
	case hours > 0:
		name = fmt.Sprintf("Etc/GMT-%d", hours)
	case hours < 0:
		name = fmt.Sprintf("Etc/GMT+%d", -hours)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// today8 is 08:00 of today in loc, and today's date there.
func today8(loc *time.Location) (time.Time, string) {
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 8, 0, 0, 0, loc), n.Format(time.DateOnly)
}

// putDay PUTs day recording id at revision (its base), started at 08:00 today in tz, with fields over the defaults;
// the 200's body.
func (d *dayFixture) putDay(id string, revision int64, tz *time.Location, fields map[string]any) map[string]any {
	d.t.Helper()
	start, day := today8(tz)
	body := map[string]any{"revision": revision, "kind": "day", "day": day, "tz": tz.String(), "part": 1,
		"started_at": start.Format(time.RFC3339), "device": "watch", "language": "en-US", "segments": []any{}, "complete": false,
		"coverage": map[string]any{"to": 0}}
	for k, v := range fields {
		body[k] = v
	}
	return d.do("PUT", "/api/v1/recordings/"+id, "", body, http.StatusOK)
}

func segmentsPath(id string) string { return "/api/v1/recordings/" + id + "/segments" }

// dayLines are three sentences of the day: one at 08:01 and two after 09:00 (two hours of its clock), with an
// interruption between.
func dayLines(base, revision int64) map[string]any {
	return map[string]any{"base": base, "revision": revision, "from": 0, "segments": []map[string]any{
		{"start": 60, "end": 65, "text": "Morning standup about the export."},
		{"start": 3720, "end": 3725, "text": "Ada: the export drops big entries."},
		{"start": 3800, "end": 3805, "text": "Bo will fix the flaky upload test."}},
		"coverage": map[string]any{"to": 3900, "gaps": []map[string]any{{"from": 600, "to": 1500, "reason": "interrupted"}}}}
}

// recap runs the recap lane until recording id's recap is ready (or a few rounds went by).
func (d *dayFixture) recap(id string) *kb.DayRecap {
	d.t.Helper()
	e := d.engine()
	for range 5 {
		e.WriteDayRecaps(context.Background(), 1)
		if r, ok := e.Store.Recording(id); ok && r.RecapState == kb.MinutesReady {
			rc, err := e.ReadDayRecap(id)
			if err != nil {
				d.t.Fatal(err)
			}
			return rc
		}
	}
	r, _ := d.engine().Store.Recording(id)
	d.t.Fatalf("no recap of %s: %+v", id, r)
	return nil
}

// setEvening sets the evening routine's time (enabled or not, it is the recap time's).
func setEvening(t *testing.T, s *Service, at string, enabled bool) {
	t.Helper()
	sc, err := s.lockedSchedule(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sc.mu.Unlock()
	for i := range sc.tasks {
		if sc.tasks[i].ID == "evening" {
			sc.tasks[i].Time, sc.tasks[i].Enabled = at, enabled
			return
		}
	}
	t.Fatal("no evening routine")
}

// The upload of a day recording says its base and the hub's count; appends go by position, a retry stores nothing
// twice, and the refusals come with the contract's codes and bodies.
func TestDaySegmentsHTTP(t *testing.T) {
	d := newDayFixture(t)
	wantCode(t, d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusNotFound), codeRecordingNotFound)

	put := d.putDay(dayRec, 100, d.owner, nil)
	if put["status"] != "stored" || put["revision"] != float64(100) || put["base"] != float64(100) || put["next"] != float64(0) ||
		put["attendance"] != "off" {
		t.Fatalf("PUT: %v", put)
	}
	out := d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	if len(out) != 4 || out["id"] != dayRec || out["revision"] != float64(101) || out["next"] != float64(3) || out["status"] != "stored" {
		t.Fatalf("append: %v", out)
	}
	if out := d.do("POST", segmentsPath(dayRec), "", dayLines(100, 102), http.StatusOK); out["next"] != float64(3) || out["revision"] != float64(102) {
		t.Fatalf("retry: %v", out)
	}
	if segs := d.engine().Store.RecordingSegments(dayRec); len(segs) != 3 || segs[2].N != 3 {
		t.Fatalf("stored %+v", segs)
	}

	// The other fields apply only from a request at least as new as the stored revision: an older one (a slow
	// retry) stores its new positions and changes nothing else.
	title := func(rev int64, from int, title, text string) map[string]any {
		return map[string]any{"base": 100, "revision": rev, "from": from, "title": title,
			"segments": []map[string]any{{"start": 3850, "end": 3855, "text": text}}}
	}
	if out := d.do("POST", segmentsPath(dayRec), "", title(300, 3, "Tuesday", "A fourth sentence."), http.StatusOK); out["next"] != float64(4) {
		t.Fatalf("newer: %v", out)
	}
	if out := d.do("POST", segmentsPath(dayRec), "", title(200, 4, "Older", "A fifth sentence."), http.StatusOK); out["next"] != float64(5) ||
		out["revision"] != float64(300) {
		t.Fatalf("older: %v", out)
	}
	if r, _ := d.engine().Store.Recording(dayRec); r.Title != "Tuesday" || r.Revision != 300 || r.Next != 5 {
		t.Fatalf("after an older request: title %q, revision %d, next %d", r.Title, r.Revision, r.Next)
	}

	gap := d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 100, "revision": 301, "from": 9, "segments": []any{}}, http.StatusConflict)
	if wantCode(t, gap, codeSegmentsGap); gap["next"] != float64(5) || gap["error"] == "" || len(gap) != 3 {
		t.Fatalf("gap: %v", gap)
	}
	changed := d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 99, "revision": 301, "from": 5, "segments": []any{}}, http.StatusConflict)
	if wantCode(t, changed, codeBaseChanged); changed["base"] != float64(100) || changed["next"] != float64(5) || changed["error"] == "" {
		t.Fatalf("base changed: %v", changed)
	}
	many := make([]map[string]any, kb.MaxAppendSegments+1)
	for i := range many {
		many[i] = map[string]any{"start": 4000 + i, "end": 4000 + i, "text": "x"}
	}
	for name, body := range map[string]any{
		"no base":          `{"revision":5,"from":5,"segments":[]}`,
		"revision at base": `{"base":100,"revision":100,"from":5,"segments":[]}`,
		"no from":          `{"base":100,"revision":301,"segments":[]}`,
		"negative from":    `{"base":100,"revision":301,"from":-1,"segments":[]}`,
		"fractional from":  `{"base":100,"revision":301,"from":1.5,"segments":[]}`,
		"a text time":      `{"base":100,"revision":301,"from":5,"segments":[{"start":"1","end":2,"text":"x"}]}`,
		"not json":         `{"base":`,
		"too many":         map[string]any{"base": 100, "revision": 301, "from": 5, "segments": many},
	} {
		if out := d.do("POST", segmentsPath(dayRec), "", body, http.StatusBadRequest); out["code"] != "bad_request" {
			t.Errorf("%s: %v", name, out)
		}
	}
	if out := d.do("POST", segmentsPath(dayRec), "", `{"base":100,"revision":301,"from":5.0,"segments":[]}`, http.StatusOK); out["next"] != float64(5) {
		t.Fatalf("from 5.0: %v", out)
	}
	wantCode(t, d.do("POST", segmentsPath(dayRec), "", strings.Repeat(" ", maxSegmentsBody+1), http.StatusRequestEntityTooLarge), "too_large")
	wantCode(t, d.do("POST", segmentsPath(dayRec), "answer", dayLines(100, 400), http.StatusForbidden), "forbidden")
	wantCode(t, d.do("POST", segmentsPath(dayRec), "extension", dayLines(100, 400), http.StatusForbidden), "forbidden")

	// Another kind: 409 not_day for an append, kind_changed for a PUT either way.
	d.upload(meetingRec, 1, nil)
	wantCode(t, d.do("POST", segmentsPath(meetingRec), "", dayLines(1, 2), http.StatusConflict), codeNotDay)
	_, day := today8(d.owner)
	wantCode(t, d.do("PUT", "/api/v1/recordings/"+meetingRec, "", map[string]any{"revision": 5, "kind": "day", "day": day,
		"started_at": time.Now().UTC().Format(time.RFC3339), "device": "watch", "segments": []any{}}, http.StatusConflict), codeKindChanged)
	start, _ := today8(d.owner)
	wantCode(t, d.do("PUT", "/api/v1/recordings/"+dayRec, "", map[string]any{"revision": 500, "kind": "meeting",
		"started_at": start.Format(time.RFC3339), "device": "watch", "segments": []any{}}, http.StatusConflict), codeKindChanged)

	// A PUT is a new base: an append on the old one is refused with the new base, the new one's goes on from its count.
	put = d.putDay(dayRec, 600, d.owner, map[string]any{"segments": []map[string]any{{"start": 60, "end": 65, "text": "Morning standup about the export."},
		{"start": 70, "end": 75, "text": ""}}})
	if put["revision"] != float64(600) || put["base"] != float64(600) || put["next"] != float64(2) {
		t.Fatalf("repair PUT: %v", put)
	}
	changed = d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 100, "revision": 601, "from": 5, "segments": []any{}}, http.StatusConflict)
	if changed["base"] != float64(600) || changed["next"] != float64(2) {
		t.Fatalf("old base: %v", changed)
	}
	if out := d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 600, "revision": 601, "from": 2,
		"segments": []map[string]any{{"start": 80, "end": 85, "text": "Back on the new base."}}}, http.StatusOK); out["next"] != float64(3) {
		t.Fatalf("new base: %v", out)
	}
	// A stale PUT of a day recording answers the stored revision, so the phone sends again above it.
	if stale := d.putDay(dayRec, 550, d.owner, nil); stale["status"] != "stale" || stale["revision"] != float64(601) || stale["base"] != float64(600) ||
		stale["next"] != float64(3) {
		t.Fatalf("stale PUT: %v", stale)
	}
	recs := d.do("GET", "/api/v1/recordings", "", nil, http.StatusOK)["recordings"].([]any)
	for _, x := range recs {
		if r := x.(map[string]any); r["id"] == dayRec && (r["kind"] != "day" || r["part"] != float64(1) || r["next"] != float64(3) || r["base"] != float64(600)) {
			t.Fatalf("reconciliation: %v", r)
		}
	}

	// Expired: the day started before recording_days now (nothing written), as for a PUT.
	old := time.Now().Add(-50 * time.Hour)
	d.putDay(dayPart2, 10, d.owner, map[string]any{"started_at": old.Format(time.RFC3339), "day": old.In(d.owner).Format(time.DateOnly)})
	setSettings(t, d.s, func(v *Settings) { one := 1; v.RecordingDays = &one })
	if out := d.do("POST", segmentsPath(dayPart2), "", dayLines(10, 11), http.StatusOK); out["status"] != "expired" {
		t.Fatalf("expired: %v", out)
	}

	// Deleted: 410, before a body's 400.
	d.do("DELETE", "/api/v1/recordings/"+dayRec, "", nil, http.StatusOK)
	wantCode(t, d.do("POST", segmentsPath(dayRec), "", `{"base":`, http.StatusGone), codeRecordingDeleted)
}

// The refusals' order: 503 before anything, 413 too_large before the recording is looked at, 404 before 403
// kb_read_only, which comes before 400.
func TestDaySegmentsRefusalOrder(t *testing.T) {
	d := newDayFixture(t)
	d.putDay(dayRec, 100, d.owner, nil)
	d.f.mu.Lock()
	d.f.hello.KB = &KBSettings{ReadOnly: true}
	d.f.mu.Unlock()
	wantCode(t, d.do("POST", segmentsPath(dayRec), "", strings.Repeat(" ", maxSegmentsBody+1), http.StatusRequestEntityTooLarge), "too_large")
	wantCode(t, d.do("POST", segmentsPath(dayPart2), "", `{"base":`, http.StatusNotFound), codeRecordingNotFound)
	wantCode(t, d.do("POST", segmentsPath(dayRec), "", `{"base":`, http.StatusForbidden), "kb_read_only")
	d.f.mu.Lock()
	d.f.hello.KB = &KBSettings{Off: true}
	d.f.mu.Unlock()
	wantCode(t, d.do("POST", segmentsPath(dayRec), "", strings.Repeat(" ", maxSegmentsBody+1), http.StatusServiceUnavailable), "kb_disabled")
}

// Past its limits an append stores nothing and says 413 day_full with the count; a PUT over them is 413 day_full too.
func TestDaySegmentsDayFull(t *testing.T) {
	d := newDayFixture(t)
	empty := make([]map[string]any, 20000)
	for i := range empty {
		empty[i] = map[string]any{"start": i, "end": i, "text": ""}
	}
	if put := d.putDay(dayRec, 100, d.owner, map[string]any{"segments": empty}); put["next"] != float64(20000) {
		t.Fatalf("PUT: %v", put)
	}
	full := d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 100, "revision": 101, "from": 20000,
		"segments": []map[string]any{{"start": 1, "end": 2, "text": "One more."}}}, http.StatusRequestEntityTooLarge)
	if wantCode(t, full, codeDayFull); full["next"] != float64(20000) || full["error"] == "" {
		t.Fatalf("append: %v", full)
	}
	if r, _ := d.engine().Store.Recording(dayRec); r.Next != 20000 || r.Revision != 100 {
		t.Fatalf("stored something: next %d, revision %d", r.Next, r.Revision)
	}
	over := d.do("PUT", "/api/v1/recordings/"+dayRec, "", map[string]any{"revision": 200, "kind": "day", "day": time.Now().In(d.owner).Format(time.DateOnly),
		"started_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), "device": "watch", "segments": append(empty, empty[0])},
		http.StatusRequestEntityTooLarge)
	if wantCode(t, over, codeDayFull); over["next"] != float64(20000) {
		t.Fatalf("PUT over: %v", over)
	}
}

// The minutes' routes serve a day recording's recap: asking writes it from what the hub has (no push before the
// recap time), the detail and kb_get carry it, its suggested to-dos are confirmed once with notes naming the day and
// the clock time in the recording's zone; the day's push comes once, at the recap time, whatever the parts and
// versions after it.
func TestDayRecapAskTodosAndPush(t *testing.T) {
	d := newDayFixture(t)
	tz := etcZone(t, d.offset+1) // the recording's own zone, an hour east of the owner's
	path := "/api/v1/recordings/" + dayRec
	d.putDay(dayRec, 100, tz, nil)
	d.do("POST", path+"/minutes", "", nil, http.StatusConflict) // no text yet
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	if got := d.do("POST", path+"/minutes", "", nil, http.StatusAccepted); got["state"] != "pending" {
		t.Fatalf("ask: %v", got)
	}
	rc := d.recap(dayRec)
	if rc.Version != 1 || len(rc.Timeline) != 2 || rc.Timeline[1].Kind != kb.RecapMeeting || len(rc.Actions) != 1 || rc.Actions[0].ID != "a1" {
		t.Fatalf("recap %+v", rc)
	}
	if p := d.f.pushed(); len(p) != 0 {
		t.Fatalf("an asked recap pushed before the recap time: %+v", p)
	}
	r, _ := d.engine().Store.Recording(dayRec)
	if r.MinutesState != "" || r.Meeting != nil || r.MinutesAsked {
		t.Fatalf("became a meeting: %+v", r)
	}

	var detail struct {
		Detail struct {
			Meta    map[string]any `json:"meta"`
			Summary string         `json:"summary"`
			Recap   kb.DayRecap    `json:"recap"`
		} `json:"detail"`
	}
	raw, _ := json.Marshal(d.do("GET", "/api/v1/kb/recordings/"+dayRec, "", nil, http.StatusOK))
	_ = json.Unmarshal(raw, &detail)
	if detail.Detail.Meta["kind"] != "day" || detail.Detail.Meta["recap_state"] != "ready" || detail.Detail.Recap.Version != 1 ||
		!strings.Contains(detail.Detail.Summary, "export review") {
		t.Fatalf("detail %+v", detail.Detail)
	}

	// Confirming: a todo_day_ to-do once, however often; its notes name the day and when it was said there.
	todos := path + "/minutes/todos"
	var out struct {
		Todos []Todo      `json:"todos"`
		Recap kb.DayRecap `json:"recap"`
	}
	conf := func(version int, actions ...string) {
		t.Helper()
		raw, _ := json.Marshal(d.do("POST", todos, "", map[string]any{"version": version, "actions": actions}, http.StatusOK))
		out.Todos, out.Recap = nil, kb.DayRecap{}
		_ = json.Unmarshal(raw, &out)
	}
	conf(1, "a1")
	_, day := today8(tz)
	clock := r.StartedAt.Add(3800 * time.Second).In(tz).Format("15:04")
	if len(out.Todos) != 1 || !strings.HasPrefix(out.Todos[0].ID, "todo_day_") || out.Todos[0].Title != "Bo: Fix the flaky upload test" ||
		!strings.Contains(out.Todos[0].Notes, day) || !strings.Contains(out.Todos[0].Notes, clock) || !strings.Contains(out.Todos[0].Notes, "tomorrow") ||
		out.Recap.Actions[0].TodoID != out.Todos[0].ID || out.Todos[0].ID != dayRecapTodoID(dayRec, 1, "a1") {
		t.Fatalf("confirmed %+v (want %s at %s)", out, day, clock)
	}
	first := out.Todos[0].ID
	if conf(1, "a1", "a1"); len(out.Todos) != 1 || out.Todos[0].ID != first {
		t.Fatalf("again %+v", out)
	}
	wantCode(t, d.do("POST", todos, "", map[string]any{"version": 7, "actions": []string{"a1"}}, http.StatusConflict), codeMinutesStale)
	d.do("POST", todos, "", map[string]any{"version": 1, "actions": []string{"a9"}}, http.StatusNotFound)
	d.do("POST", todos, "answer", map[string]any{"version": 1, "actions": []string{"a1"}}, http.StatusForbidden)

	// kb_get: the recap before the transcript, with where its suggested to-dos stand; kb_list marks it.
	got, err := d.s.toolKB(context.Background(), "kb_get", json.RawMessage(`{"kind":"recordings","id":"`+dayRec+`"}`))
	if err != nil || !strings.Contains(got, "recap.md:\n# "+day) || !strings.Contains(got, "Suggested to-dos as they stand") ||
		!strings.Contains(got, "a1 Bo: Fix the flaky upload test (by tomorrow) → to-do "+first+" open") ||
		strings.Index(got, "recap.md:") > strings.Index(got, "transcript.md:") {
		t.Fatalf("kb_get: %v\n%s", err, got)
	}
	if list, _ := d.s.toolKB(context.Background(), "kb_list", json.RawMessage(`{"kind":"recordings"}`)); !strings.Contains(list, " all-day recap-ready") {
		t.Fatalf("kb_list: %s", list)
	}

	// The recap time comes (the evening routine's moved before now, though off): the ready recap pushes, once.
	setEvening(t, d.s, "06:30", false)
	d.engine().WriteDayRecaps(context.Background(), 1)
	pushes := d.f.pushed()
	l := d.s.lang(context.Background())
	if len(pushes) != 1 || pushes[0].ID != "ntf_dayrecap_"+r.Day || pushes[0].Kind != NotifyPush || pushes[0].SessionID != "c-main" ||
		pushes[0].Title != i18n.T(l, "assistant-day-recap.ready_title") ||
		pushes[0].Body != i18n.T(l, "assistant-day-recap.ready_body", i18n.Args{"day": r.Day, "meetings": 0, "actions": 1}) { // its meeting matches none the hub knows (#5051)
		t.Fatalf("pushes %+v", pushes)
	}
	// A later version, and the day's second part, push nothing more.
	d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 100, "revision": 102, "from": 3,
		"segments": []map[string]any{{"start": 4000, "end": 4005, "text": "One more thing."}}}, http.StatusOK)
	d.do("POST", path+"/minutes", "", nil, http.StatusAccepted)
	if rc := d.recap(dayRec); rc.Version != 2 || rc.Actions[0].TodoID != first {
		t.Fatalf("second version %+v", rc)
	}
	d.putDay(dayPart2, 200, tz, map[string]any{"part": 2})
	d.do("POST", segmentsPath(dayPart2), "", dayLines(200, 201), http.StatusOK)
	d.recap(dayPart2)
	d.engine().WriteDayRecaps(context.Background(), 1)
	if p := d.f.pushed(); len(p) != 1 {
		t.Fatalf("pushed again: %+v", p)
	}
}

// Past recording_days a day recording keeps its recap (trimmed, as one with minutes): it can't be written again, the
// phone's uploads of it are expired, and its to-dos can still be confirmed; kb_get says only the recap is left.
func TestDayRecapAfterTheTrim(t *testing.T) {
	d := newDayFixture(t)
	path := "/api/v1/recordings/" + dayRec
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	d.do("POST", path+"/minutes", "", nil, http.StatusAccepted)
	d.recap(dayRec)
	d.engine().PruneRecordings(time.Now().AddDate(0, 0, 20))
	if r, _ := d.engine().Store.Recording(dayRec); !r.Trimmed() || r.RecapState != kb.MinutesReady {
		t.Fatalf("not trimmed with its recap: %+v", r)
	}
	wantCode(t, d.do("POST", path+"/minutes", "", nil, http.StatusConflict), codeTranscriptExpired)
	if out := d.do("POST", segmentsPath(dayRec), "", map[string]any{"base": 100, "revision": 102, "from": 3, "segments": []any{}}, http.StatusOK); out["status"] != "expired" {
		t.Fatalf("append: %v", out)
	}
	if put := d.putDay(dayRec, 200, d.owner, nil); put["status"] != "expired" || put["base"] != float64(100) || put["next"] != float64(3) {
		t.Fatalf("PUT: %v", put)
	}
	if out := d.do("POST", path+"/minutes/todos", "", map[string]any{"version": 1, "actions": []string{"a1"}}, http.StatusOK); len(out["todos"].([]any)) != 1 {
		t.Fatalf("to-dos: %v", out)
	}
	got, err := d.s.toolKB(context.Background(), "kb_get", json.RawMessage(`{"kind":"recordings","id":"`+dayRec+`"}`))
	if err != nil || !strings.Contains(got, "recap.md:") || !strings.Contains(got, "only the recap above is kept") || strings.Contains(got, "transcript.md:\n") {
		t.Fatalf("kb_get: %v\n%s", err, got)
	}
}

// The push from a recap written at the recap time: the day waited for its first recap, which pushes in its write.
func TestDayRecapPushAtTheRecapTime(t *testing.T) {
	d := newDayFixture(t)
	setEvening(t, d.s, "06:30", true)
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	rc := d.recap(dayRec)
	pushes := d.f.pushed()
	if len(pushes) != 1 || pushes[0].ID != "ntf_dayrecap_"+rc.Day || pushes[0].Title != i18n.T(d.s.lang(context.Background()), "assistant-day-recap.ready_title") {
		t.Fatalf("pushes %+v", pushes)
	}
	if r, _ := d.engine().Store.Recording(dayRec); !r.RecapPushed || r.RecapPush {
		t.Fatalf("push not marked told: %+v", r)
	}
}

// The recap time is the evening routine's, 30 minutes before (kb's): its time whether or not it is on; none in the
// schedule is the default.
func TestDayRecapClockIsTheEveningRoutines(t *testing.T) {
	s := newServiceOn(t, newFakeHub(), newKBStore())
	if h, m := s.eveningClock(); h != 21 || m != 30 {
		t.Fatalf("default %d:%d", h, m)
	}
	setEvening(t, s, "19:15", false)
	if h, m := s.eveningClock(); h != 19 || m != 15 {
		t.Fatalf("off %d:%d", h, m)
	}
	sc, _ := s.lockedSchedule(context.Background())
	sc.tasks = slices.DeleteFunc(sc.tasks, func(t Task) bool { return t.ID == "evening" })
	sc.mu.Unlock()
	if h, m := s.eveningClock(); h != -1 || m != -1 {
		t.Fatalf("without one %d:%d", h, m)
	}
	if got := s.dayRecordingName("2026-11-11"); got != i18n.T(s.lang(context.Background()), "assistant-day-recap.name", i18n.Args{"date": "2026-11-11"}) {
		t.Fatalf("name %q", got)
	}
}

// The manager's hello names recording_day, which the hub lists only from the default manager.
func TestBusHelloNamesRecordingDay(t *testing.T) {
	b := NewBusHub(BusOptions{Hub: "http://[fd00::20]:18660", Token: "t"})
	p, _ := b.client.HelloParams().(map[string]any)
	caps, _ := p["capabilities"].([]string)
	if !slices.Contains(caps, assistantwire.CapabilityRecordingDay) || !slices.Contains(caps, assistantwire.CapabilityMeetingAttendance) {
		t.Fatalf("capabilities %v", caps)
	}
}

// The evening routine's facts say today's all-day recording to the model only: its coverage, each part's recap or
// state, the timeline's titles and the to-dos not confirmed; never in the card, and not for another routine.
func TestEveningFactsSayTodaysDayRecording(t *testing.T) {
	d := newDayFixture(t)
	ctx := context.Background()
	if f := d.s.briefFactsAt(ctx, time.Now(), "evening"); f.Day != nil {
		t.Fatalf("a day without a day recording: %+v", f.Day)
	}
	// One of an earlier day, closed then, is not today's.
	old := time.Now().Add(-40 * time.Hour)
	ended := old.Add(2 * time.Hour)
	d.putDay(meetingOther, 50, d.owner, map[string]any{"started_at": old.Format(time.RFC3339), "ended_at": ended.Format(time.RFC3339),
		"day": old.In(d.owner).Format(time.DateOnly), "complete": true, "segments": []map[string]any{{"start": 1, "end": 2, "text": "An old day."}},
		"coverage": map[string]any{"to": 7200}})
	if f := d.s.briefFactsAt(ctx, time.Now(), "evening"); f.Day != nil {
		t.Fatalf("an earlier day's recording: %+v", f.Day)
	}
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	d.do("POST", "/api/v1/recordings/"+dayRec+"/minutes", "", nil, http.StatusAccepted)
	rc := d.recap(dayRec)
	d.putDay(dayPart2, 200, d.owner, map[string]any{"part": 2, "duration": 1800})

	now := time.Now()
	f := d.s.briefFactsAt(ctx, now, "evening")
	if f.Day == nil || len(f.Day.Parts) != 2 || len(f.Day.Timeline) != 2 || len(f.Day.Actions) != 1 || len(f.Day.Gaps) != 1 {
		t.Fatalf("facts %+v", f.Day)
	}
	text := f.modelText()
	made := rc.GeneratedAt.In(d.owner).Format("15:04")
	// Open, it was recorded until now; its text goes as far as the phone uploaded it.
	recorded := "录音时段 08:00–" + now.In(d.owner).Format("15:04") + "，文字转写到 09:05，"
	for _, want := range []string{"的全天录音", recorded + "其间没录到的：被打断（来电、Siri 等占用了麦克风） 1 段共 15 分钟（08:10–08:25）",
		"第 1 部分：回顾写于 " + made + "，读到 ", "概要：A standup, then the export review.", "第 2 部分：录了 30 分钟，但没有转写出文字",
		"08:01–08:01 会议以外：Standup", "09:02–09:03 会议：Export review", "Bo：Fix the flaky upload test", "所以请在回答里"} {
		if !strings.Contains(text, want) {
			t.Errorf("facts lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "The export drops big entries") {
		t.Errorf("facts carry the points:\n%s", text)
	}
	card, fallback, _ := d.s.briefCard(f, "晚报", "ok")
	if strings.Contains(card.JSON(), "Export review") || strings.Contains(fallback, "Export review") || strings.Contains(f.countsText(), "Export review") {
		t.Fatalf("the card or the push carries the day: %s\n%s", card.JSON(), fallback)
	}
	if f := d.s.briefFactsAt(ctx, time.Now(), "morning"); f.Day != nil || strings.Contains(f.modelText(), "全天录音") {
		t.Fatalf("the morning routine: %+v", f.Day)
	}
	// Confirmed, a to-do is not a suggestion any more.
	d.do("POST", "/api/v1/recordings/"+dayRec+"/minutes/todos", "", map[string]any{"version": 1, "actions": []string{"a1"}}, http.StatusOK)
	if f := d.s.briefFactsAt(ctx, time.Now(), "evening"); len(f.Day.Actions) != 0 || strings.Contains(f.modelText(), "还没确认的建议待办") {
		t.Fatalf("a confirmed to-do: %+v", f.Day.Actions)
	}
}

// The section stays about 4000 runes: entries from the middle of the day go first, the day's start and its latest
// hours stay.
func TestEveningFactsDayIsBounded(t *testing.T) {
	d := &prompts.BriefDay{Date: "2026-11-11", From: "08:00", To: "22:00"}
	for i := range 200 {
		d.Timeline = append(d.Timeline, prompts.BriefDayEntry{From: fmt.Sprintf("e%03d", i), To: "x", Title: strings.Repeat("长", 50)})
	}
	for i := range 50 {
		d.Actions = append(d.Actions, prompts.BriefDayAction{Who: "Bo", What: fmt.Sprintf("Thing %d", i)})
	}
	d.Parts = []prompts.BriefDayPart{{Part: 1, Made: "21:00", Upto: "20:58", Summary: strings.Repeat("概", 300)}}
	briefDayBound(d)
	if n := prompts.BriefDayRunes(d); n > briefDayRunes || n < briefDayRunes-200 {
		t.Fatalf("%d runes", n)
	}
	if d.Left == 0 || len(d.Timeline)+d.Left != 200 || d.Timeline[0].From != "e000" || d.Timeline[len(d.Timeline)-1].From != "e199" {
		t.Fatalf("kept %d, left %d: first %s, last %s", len(d.Timeline), d.Left, d.Timeline[0].From, d.Timeline[len(d.Timeline)-1].From)
	}
	if len(d.Actions) != 50 || d.ActionsLeft != 0 {
		t.Fatalf("actions went before the timeline: %d", len(d.Actions))
	}
}

// The day's push is done only once the phone, the Lark bot and every channel with pushes on took it (day-recap.md
// "The push"): one that failed leaves it due in the knowledge base, and telling it again (with those that took it,
// which the knowledge base keeps) sends it to the others, with the same id and keys; a refusal that sending again
// cannot help counts as taken: the hub's, a channel with nowhere to send it, a channel's own coded refusal.
func TestDayRecapPushTakenByAll(t *testing.T) {
	d := newDayFixture(t)
	setEvening(t, d.s, "06:30", true)
	larkFails, chanFails := 1, 1 // the answers run under d.f.mu
	var chanErr error
	d.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_owner", PairedAt: time.Unix(100, 0), Features: FeishuFeatures{Pushes: true}}, nil
	})
	d.f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		if larkFails > 0 {
			larkFails--
			return nil, errors.New("Lark is busy")
		}
		return map[string]any{"message_id": "om_day"}, nil
	})
	d.f.answerApp(AppChannels, ChannelsList, func(json.RawMessage) (any, error) {
		return map[string]any{"channels": []channel.View{{ID: "examplechat", Name: "examplechat", Source: channel.SourcePlugin, Kind: channel.KindIM,
			Installed: true, Enabled: true, State: channel.StateConnected, Capabilities: channel.Capabilities{Push: true}}}}, nil
	})
	d.f.answerApp(AppChannels, ChannelsSend, func(json.RawMessage) (any, error) {
		if chanFails > 0 {
			chanFails--
			return nil, errors.New("the channel's program restarts")
		}
		return map[string]any{}, chanErr
	})
	d.s.chans.mu.Lock()
	d.s.loadChannelsLocked(context.Background())
	d.s.channelPrefLocked("examplechat").Features = map[string]bool{channel.FeaturePushes: true}
	d.s.chans.mu.Unlock()
	d.f.mu.Lock()
	d.f.failNotify = 1
	d.f.mu.Unlock()

	// Written at the recap time, the recap takes the push, which nobody takes: it stays due.
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	rc := d.recap(dayRec)
	e := d.engine()
	r, _ := e.Store.Recording(dayRec)
	posts, sends := d.f.appParams(AppFeishu, FeishuPost), d.f.appParams(AppChannels, ChannelsSend)
	if !r.RecapPush || r.RecapPushed || r.RecapPushTries != 1 || r.RecapPushTook != nil || len(d.f.pushed()) != 0 || len(posts) != 1 || len(sends) != 1 {
		t.Fatalf("a push nobody took: %d posts, %d sends, %+v", len(posts), len(sends), r)
	}
	took := r.RecapPushTook
	tell := func() error {
		t.Helper()
		r, _ := e.Store.Recording(dayRec)
		r.RecapPushTook = took // what the knowledge base keeps of the calls before (kb's tellDayRecap)
		now, err := e.DayRecapWritten(context.Background(), *r, *rc)
		took = append(took, now...)
		return err
	}
	// Told again: the phone and the channel take it, the Lark bot not yet.
	d.f.mu.Lock()
	larkFails = 1
	d.f.mu.Unlock()
	if err := tell(); err == nil {
		t.Fatal("done before the Lark bot took it")
	}
	if slices.Sort(took); !slices.Equal(took, []string{channel.App, "examplechat"}) {
		t.Fatalf("took it: %v", took)
	}
	// Once more: only the Lark bot gets it again, and takes it.
	if err := tell(); err != nil {
		t.Fatal(err)
	}
	id := "ntf_dayrecap_" + rc.Day
	pushes := d.f.pushed()
	posts, sends = d.f.appParams(AppFeishu, FeishuPost), d.f.appParams(AppChannels, ChannelsSend)
	if len(pushes) != 1 || pushes[0].ID != id || len(posts) != 3 || len(sends) != 2 || len(took) != 3 || took[2] != channel.Feishu {
		t.Fatalf("sent again to those that took it: %d pushes, %d posts, %d sends, took %v", len(pushes), len(posts), len(sends), took)
	}
	for _, p := range posts {
		if p["uuid"] != id+"_0" {
			t.Errorf("the Lark copy's key %v", p["uuid"])
		}
	}
	for _, p := range sends {
		if p["ref"] != id+":examplechat" || p["kind"] != channel.OutPush || p["text"] != "🔔 "+pushes[0].Title+"\n\n"+pushes[0].Body {
			t.Errorf("the channel's copy %v", p)
		}
	}
	// Told afresh, nothing taken yet, everything goes again: the hub refusing the phone's and the channel having
	// nowhere to send it, or refusing it with a code of its own, are taken as they are, not sent again and again.
	for _, refusal := range []error{&rpc.Error{Code: CodeCannotSend, Message: "no conversation yet"}, &rpc.Error{Code: "bot_blocked", Message: "the user blocked the bot"}} {
		took = nil
		d.f.mu.Lock()
		d.f.refuseNotify = &rpc.Error{Code: "no_device", Message: "another manager's"}
		chanErr = refusal
		d.f.mu.Unlock()
		if err := tell(); err != nil {
			t.Fatalf("a refusal is sent again: %v", err)
		}
		if slices.Sort(took); !slices.Equal(took, []string{channel.App, "examplechat", channel.Feishu}) {
			t.Errorf("refused, taken: %v", took)
		}
	}
	if posts := d.f.appParams(AppFeishu, FeishuPost); len(posts) != 5 || posts[4]["uuid"] != id+"_0" {
		t.Fatalf("the Lark bot's copy again: %v", posts)
	}
	// A send that got no answer (no code: a time-out, the program not running) is not a refusal: sent again.
	took = nil
	d.f.mu.Lock()
	chanErr = errors.New("channels.send timed out")
	d.f.mu.Unlock()
	if err := tell(); err == nil || slices.Contains(took, "examplechat") {
		t.Fatalf("a channel that did not answer took it: %v %v", err, took)
	}
}

// After a restart the push goes only to the destinations that did not take it: the knowledge base keeps those that
// did with the recording (the assistant keeps nothing), so the phone and a channel that took it get no second copy.
func TestDayRecapPushAfterRestart(t *testing.T) {
	d := newDayFixture(t)
	setEvening(t, d.s, "06:30", true)
	larkFails := 1
	d.f.answerApp(AppFeishu, FeishuStatusGet, func(json.RawMessage) (any, error) {
		return FeishuStatus{Ready: true, Enabled: true, Owner: "ou_owner", PairedAt: time.Unix(100, 0), Features: FeishuFeatures{Pushes: true}}, nil
	})
	d.f.answerApp(AppFeishu, FeishuPost, func(json.RawMessage) (any, error) {
		if larkFails > 0 {
			larkFails--
			return nil, errors.New("Lark is busy")
		}
		return map[string]any{"message_id": "om_day"}, nil
	})
	d.f.answerApp(AppChannels, ChannelsList, func(json.RawMessage) (any, error) {
		return map[string]any{"channels": []channel.View{{ID: "examplechat", Name: "examplechat", Source: channel.SourcePlugin, Kind: channel.KindIM,
			Installed: true, Enabled: true, State: channel.StateConnected, Capabilities: channel.Capabilities{Push: true}}}}, nil
	})
	d.f.answerApp(AppChannels, ChannelsSend, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	d.s.chans.mu.Lock()
	d.s.loadChannelsLocked(context.Background())
	d.s.channelPrefLocked("examplechat").Features = map[string]bool{channel.FeaturePushes: true}
	d.s.chans.mu.Unlock()

	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	rc := d.recap(dayRec)
	e := d.engine()
	r, _ := e.Store.Recording(dayRec)
	if !r.RecapPush || !slices.Equal(r.RecapPushTook, []string{channel.App, "examplechat"}) || len(d.f.pushed()) != 1 {
		t.Fatalf("the phone and the channel took it, the Lark bot not: %+v", r)
	}
	// Restarted: told with the recording as stored.
	took, err := e.DayRecapWritten(context.Background(), *r, *rc)
	if err != nil || !slices.Equal(took, []string{channel.Feishu}) {
		t.Fatalf("after a restart: took %v, %v", took, err)
	}
	if p, s, posts := d.f.pushed(), d.f.appParams(AppChannels, ChannelsSend), d.f.appParams(AppFeishu, FeishuPost); len(p) != 1 || len(s) != 1 || len(posts) != 2 {
		t.Fatalf("sent again to those that took it: %d pushes, %d sends, %d posts", len(p), len(s), len(posts))
	}
}

// The phone's key among a push's destinations is a built-in channel's id, which no plugin channel has: a plugin
// channel named "phone" that took it does not stand for the phone, nor the phone for it.
func TestDayRecapPushPhoneKeyIsReserved(t *testing.T) {
	if !slices.Contains(channel.Reserved, dayPushPhone) {
		t.Fatalf("the phone's key %q can be a plugin channel's id", dayPushPhone)
	}
	d := newDayFixture(t)
	d.f.answerApp(AppChannels, ChannelsList, func(json.RawMessage) (any, error) {
		return map[string]any{"channels": []channel.View{{ID: "phone", Name: "phone", Source: channel.SourcePlugin, Kind: channel.KindIM,
			Installed: true, Enabled: true, State: channel.StateConnected, Capabilities: channel.Capabilities{Push: true}}}}, nil
	})
	d.f.answerApp(AppChannels, ChannelsSend, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	d.s.chans.mu.Lock()
	d.s.loadChannelsLocked(context.Background())
	d.s.channelPrefLocked("phone").Features = map[string]bool{channel.FeaturePushes: true}
	d.s.chans.mu.Unlock()
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	d.do("POST", "/api/v1/recordings/"+dayRec+"/minutes", "", nil, http.StatusAccepted)
	rc := d.recap(dayRec)
	e := d.engine()
	r, _ := e.Store.Recording(dayRec)
	r.RecapPushTook = []string{"phone"} // the plugin channel took it, the phone not yet
	took, err := e.DayRecapWritten(context.Background(), *r, *rc)
	if err != nil || !slices.Contains(took, dayPushPhone) || slices.Contains(took, "phone") || len(d.f.pushed()) != 1 ||
		len(d.f.appParams(AppChannels, ChannelsSend)) != 0 {
		t.Fatalf("took %v, %v: %d pushes, %d sends", took, err, len(d.f.pushed()), len(d.f.appParams(AppChannels, ChannelsSend)))
	}
}

// A channel's copy of a push sent again has a ref of its own (the push's id and the channel's): a program with
// several channels and one cache of refs sends each its copy.
func TestDayRecapPushRefPerChannel(t *testing.T) {
	d := newDayFixture(t)
	views := []channel.View{}
	for _, id := range []string{"chata", "chatb"} {
		views = append(views, channel.View{ID: id, Name: id, Source: channel.SourcePlugin, Kind: channel.KindIM, Installed: true, Enabled: true,
			State: channel.StateConnected, Capabilities: channel.Capabilities{Push: true}})
	}
	d.f.answerApp(AppChannels, ChannelsList, func(json.RawMessage) (any, error) { return map[string]any{"channels": views}, nil })
	d.f.answerApp(AppChannels, ChannelsSend, func(json.RawMessage) (any, error) { return map[string]any{}, nil })
	d.s.chans.mu.Lock()
	d.s.loadChannelsLocked(context.Background())
	for _, v := range views {
		d.s.channelPrefLocked(v.ID).Features = map[string]bool{channel.FeaturePushes: true}
	}
	d.s.chans.mu.Unlock()
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", dayLines(100, 101), http.StatusOK)
	d.do("POST", "/api/v1/recordings/"+dayRec+"/minutes", "", nil, http.StatusAccepted)
	rc := d.recap(dayRec)
	r, _ := d.engine().Store.Recording(dayRec)
	if _, err := d.engine().DayRecapWritten(context.Background(), *r, *rc); err != nil {
		t.Fatal(err)
	}
	refs := map[string]any{}
	for _, p := range d.f.appParams(AppChannels, ChannelsSend) {
		refs[p["channel"].(string)] = p["ref"]
	}
	id := "ntf_dayrecap_" + rc.Day
	if len(refs) != 2 || refs["chata"] != id+":chata" || refs["chatb"] != id+":chatb" {
		t.Fatalf("refs %v", refs)
	}
}

// The push counts the day's meetings and suggested to-dos across its parts' recaps; a meeting only when the hub knows
// of it, which neither part's is (#5051, TestDayRecapCountsOnlyKnownMeetings).
func TestDayRecapPushCountsEveryPart(t *testing.T) {
	d := newDayFixture(t)
	for i, id := range []string{dayRec, dayPart2} {
		base := int64(100 * (i + 1))
		d.putDay(id, base, d.owner, map[string]any{"part": i + 1})
		d.do("POST", segmentsPath(id), "", dayLines(base, base+1), http.StatusOK)
		d.do("POST", "/api/v1/recordings/"+id+"/minutes", "", nil, http.StatusAccepted)
		if rc := d.recap(id); len(rc.Actions) != 1 || rc.Timeline[1].Kind != kb.RecapMeeting {
			t.Fatalf("recap of %s: %+v", id, rc)
		}
	}
	if p := d.f.pushed(); len(p) != 0 {
		t.Fatalf("pushed before the recap time: %+v", p)
	}
	setEvening(t, d.s, "06:30", false)
	d.engine().WriteDayRecaps(context.Background(), 1)
	_, day := today8(d.owner)
	want := i18n.T(d.s.lang(context.Background()), "assistant-day-recap.ready_body", i18n.Args{"day": day, "meetings": 0, "actions": 2})
	if p := d.f.pushed(); len(p) != 1 || p[0].Body != want {
		t.Fatalf("pushes %+v, want %q", p, want)
	}
}

// The evening facts of a read-only knowledge base take the owner's today in the owner's zone (its store is not put
// in it); what was recorded is the recording's span, and how far its text goes is said apart.
func TestEveningFactsDayReadOnlyInTheOwnersZone(t *testing.T) {
	d := newDayFixture(t)
	// An owner about 12 hours from the host's zone, so their days part.
	_, local := time.Now().Zone()
	hours := int(math.Round(float64(local) / 3600))
	owner := hours + 12
	if hours > 0 {
		owner = hours - 12
	}
	loc := etcZone(t, owner)
	setSettings(t, d.s, func(v *Settings) { v.Timezone = loc.String() })
	y := time.Now().In(loc).AddDate(0, 0, -1)
	start := time.Date(y.Year(), y.Month(), y.Day(), 8, 0, 0, 0, loc)
	end, now := start.Add(time.Hour), start.Add(5*time.Hour)
	d.putDay(dayRec, 100, loc, map[string]any{"started_at": start.Format(time.RFC3339), "ended_at": end.Format(time.RFC3339),
		"day": y.Format(time.DateOnly), "complete": true, "segments": []map[string]any{{"start": 60, "end": 65, "text": "Morning standup."}},
		"coverage": map[string]any{"to": 1800}})
	d.f.mu.Lock()
	d.f.hello.KB = &KBSettings{ReadOnly: true}
	d.f.mu.Unlock()
	f := d.s.briefFactsAt(context.Background(), now, "evening")
	if f.Day == nil || f.Day.Date != y.Format(time.DateOnly) {
		t.Fatalf("the owner's day in a read-only knowledge base: %+v", f.Day)
	}
	if text := f.modelText(); !strings.Contains(text, "录音时段 08:00–09:00，文字转写到 08:30") {
		t.Fatalf("recorded and transcribed:\n%s", text)
	}
}

// The section stays within 4000 runes whatever a valid day holds: 500 gaps of as many reasons, a dozen parts. The
// reasons with the most time and the first and latest parts are listed, the others counted.
func TestEveningFactsDayBoundsGapsAndParts(t *testing.T) {
	d := newDayFixture(t)
	var gaps []map[string]any
	for i := range 500 {
		reason := fmt.Sprintf("reason-%03d-", i)
		gaps = append(gaps, map[string]any{"from": 4000 + 2*i, "to": 4001 + 2*i, "reason": reason + strings.Repeat("x", 32-len(reason))})
	}
	lines := dayLines(100, 101)
	lines["coverage"] = map[string]any{"to": 6000, "gaps": gaps}
	d.putDay(dayRec, 100, d.owner, nil)
	d.do("POST", segmentsPath(dayRec), "", lines, http.StatusOK)
	d.do("POST", "/api/v1/recordings/"+dayRec+"/minutes", "", nil, http.StatusAccepted)
	d.recap(dayRec)
	for part := 2; part <= 12; part++ {
		d.putDay(fmt.Sprintf("77777777-7777-4777-8777-%012d", part), int64(100*part), d.owner, map[string]any{"part": part, "duration": 600})
	}
	f := d.s.briefFactsAt(context.Background(), time.Now(), "evening")
	if f.Day == nil {
		t.Fatal("no day")
	}
	if n := prompts.BriefDayRunes(f.Day); n > briefDayRunes {
		t.Errorf("%d runes", n)
	}
	day := f.Day
	if len(day.Gaps) != briefDayReasons || day.GapReasonsLeft != 494 || day.GapsLeft != 494 || day.GapMinutesLeft != 494 ||
		!strings.HasPrefix(day.Gaps[0].Reason, "reason-000-") {
		t.Errorf("gaps %d listed, %d reasons left (%d, %d min): %+v", len(day.Gaps), day.GapReasonsLeft, day.GapsLeft, day.GapMinutesLeft, day.Gaps[0])
	}
	if len(day.Parts) != briefDayParts || day.PartsLeft != 7 || day.Parts[0].Part != 1 || day.Parts[len(day.Parts)-1].Part != 12 {
		t.Errorf("parts %+v, %d left", day.Parts, day.PartsLeft)
	}
	text := f.modelText()
	for _, want := range []string{"；另有 494 种原因共 494 段 494 分钟", "  - 中间另有 7 个部分没列出\n", "Standup"} {
		if !strings.Contains(text, want) {
			t.Errorf("facts lack %q:\n%s", want, text)
		}
	}
}

// At its longest, every part of the section is bounded: 30 parts with long summaries, 500 gap reasons each with its
// longest stretches, a long timeline and many to-dos still make at most 4000 runes.
func TestEveningFactsDayBoundsEverything(t *testing.T) {
	d := &prompts.BriefDay{Date: "2026-11-11", From: "11-10 23:58", To: "11-11 23:59", Transcribed: "11-11 23:30"}
	for i := range 500 {
		reason := fmt.Sprintf("reason-%03d-", i)
		g := prompts.BriefDayGap{Reason: reason + strings.Repeat("x", 32-len(reason)), Count: 5 + i%7, Minutes: 10 + i%50, More: true}
		for range briefDayRanges {
			g.Ranges = append(g.Ranges, "11-10 23:59–11-11 00:30")
		}
		d.Gaps = append(d.Gaps, g)
	}
	for i := range 30 {
		d.Parts = append(d.Parts, prompts.BriefDayPart{Part: i + 1, Made: "11-12 00:10", Upto: "11-11 23:59", Final: true, Partial: true,
			State: "failed", Summary: strings.Repeat("概", briefDaySummary)})
	}
	for i := range 200 {
		d.Timeline = append(d.Timeline, prompts.BriefDayEntry{From: fmt.Sprintf("e%03d", i), To: "11-11 23:59", Title: strings.Repeat("长", briefDayTitle)})
	}
	for range 50 {
		d.Actions = append(d.Actions, prompts.BriefDayAction{Who: strings.Repeat("人", 40), What: strings.Repeat("事", briefDayWhat)})
	}
	briefDayBound(d)
	if n := prompts.BriefDayRunes(d); n > briefDayRunes {
		t.Fatalf("%d runes", n)
	}
	if len(d.Parts) != briefDayParts || d.PartsLeft != 25 || d.Parts[0].Part != 1 || d.Parts[len(d.Parts)-1].Part != 30 {
		t.Errorf("parts %d, %d left", len(d.Parts), d.PartsLeft)
	}
	// The reasons with the most time (59 minutes: every 50th from the 49th), in the day's order.
	if len(d.Gaps) != briefDayReasons || d.GapReasonsLeft != 494 || !strings.HasPrefix(d.Gaps[0].Reason, "reason-049-") ||
		!strings.HasPrefix(d.Gaps[5].Reason, "reason-299-") {
		t.Errorf("gaps %d listed, %d left: %s … %s", len(d.Gaps), d.GapReasonsLeft, d.Gaps[0].Reason, d.Gaps[len(d.Gaps)-1].Reason)
	}
}

// The push counts only meetings the hub knows of (#5051): a day with one recorded meeting, and a one-to-one, a hallway
// chat and lunch the model still called meetings, reads "1 meeting"; the same meeting in a second entry counts once.
func TestDayRecapCountsOnlyKnownMeetings(t *testing.T) {
	known := &kb.MeetingRef{Kind: kb.KindRecording, ID: "rec-standup", Name: "Standup"}
	d := kb.DayRecap{Timeline: []kb.DayRecapEntry{
		{Kind: kb.RecapMeeting, Title: "Standup", Meeting: known},
		{Kind: kb.RecapMeeting, Title: "One-to-one about the release"},
		{Kind: kb.RecapMeeting, Title: "Hallway chat"},
		{Kind: kb.RecapMeeting, Title: "Lunch", Meeting: &kb.MeetingRef{}},
		{Kind: kb.RecapOutside, Title: "Writing the report"},
		{Kind: kb.RecapMeeting, Title: "Standup, after the break", Meeting: known},
	}, Actions: []kb.MinutesAction{{What: "send the notes"}}}
	if meetings, actions := dayRecapCounts(nil, "2026-11-11", "r1", d); meetings != 1 || actions != 1 {
		t.Fatalf("meetings %d actions %d, want 1 and 1", meetings, actions)
	}
}
