package kb

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// roomUploadJSON decodes an upload body as the HTTP layer does.
func roomUploadJSON(t *testing.T, body string) RecordingUpload {
	t.Helper()
	var u RecordingUpload
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	return u
}

// metaKeys are the keys of recording id's meta.json on disk.
func metaKeys(t *testing.T, e *Engine, id string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(e.Store.path(KindRecording, id, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// A room meeting keeps its role in meeting.json, never in meta.json: after an upload, a role change and the trim,
// meta.json carries no attendance and no attending_session, so an older manager after a rollback feeds nothing.
// The top-level attendance fields are ignored beside room; an upload without room turns the role off and the meeting
// stays a room meeting.
func TestRoomMeetingNeverInMeta(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e := goldenEngine(t, nil)
	var changed [][]string
	e.MeetingChanged = func(id string, parts []string) { changed = append(changed, parts) }
	start := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	body := func(rev int, extra string) string {
		return `{"revision":` + string(rune('0'+rev)) + `,"started_at":"` + start + `","device":"phone","kind":"meeting","segments":[]` + extra + `}`
	}
	noMeta := func(step string) {
		t.Helper()
		keys := metaKeys(t, e, recID)
		if _, ok := keys["attendance"]; ok {
			t.Errorf("%s: meta.json carries attendance: %s", step, keys["attendance"])
		}
		if _, ok := keys["attending_session"]; ok {
			t.Errorf("%s: meta.json carries attending_session", step)
		}
	}

	put, err := e.PutRecording(recID, roomUploadJSON(t, body(1, `,"room":{"attendance":"participant"},"attendance":"listener","attending_session":{"machine_id":"m","session_id":"s"},"private":false`)), now)
	if err != nil || put.Status != RecordingStored || put.Attendance != AttendanceParticipant || !put.Room || put.AttendanceProblem != "" {
		t.Fatalf("put %+v %v", put, err)
	}
	noMeta("upload")
	r := recordingNow(t, e, recID)
	if !r.RoomMeeting() || r.AttendanceRole() != AttendanceParticipant || r.AttendingSession != nil || !r.MarkedNotPrivate() {
		t.Fatalf("stored %+v %+v", r, r.Conv)
	}
	if v := r.Conv.RoomView(); v == nil || v.State != RoomNone || v.SessionID != "" {
		t.Fatalf("room %+v", v)
	}
	if len(changed) != 1 || !slices.Equal(changed[0], []string{PartRoom, PartPrivate}) || r.Conv.Revs[PartRoom] != 1 || r.Conv.Revs[PartPrivate] != 1 {
		t.Fatalf("changed %v revs %v", changed, r.Conv.Revs)
	}

	// A role change; private is taken only while there is none.
	put, _ = e.PutRecording(recID, roomUploadJSON(t, body(2, `,"room":{"attendance":"listener"},"private":true`)), now)
	r = recordingNow(t, e, recID)
	if put.Attendance != AttendanceListener || r.AttendanceRole() != AttendanceListener || !r.MarkedNotPrivate() {
		t.Fatalf("role change %+v %+v", put, r.Conv)
	}
	noMeta("role change")

	// Without room: off, still a room meeting, its top-level attendance ignored.
	put, _ = e.PutRecording(recID, roomUploadJSON(t, body(3, `,"attendance":"participant"`)), now)
	r = recordingNow(t, e, recID)
	if put.Attendance != AttendanceOff || !put.Room || !r.RoomMeeting() || r.Attending() || r.Attendance != "" {
		t.Fatalf("without room %+v %+v", put, r)
	}
	noMeta("without room")
	if _, _, err := e.LiveAppend(recID, 0, []LiveSegment{{Text: "x"}}); !errors.Is(err, ErrNotAttending) {
		t.Errorf("off: %v", err)
	}

	// The trim keeps meeting.json (the mark, the room to delete); still nothing in meta.json, and attended no more.
	e.PutRecording(recID, roomUploadJSON(t, body(4, `,"room":{"attendance":"participant"}`)), now)
	r = recordingNow(t, e, recID)
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Title: "Design review", When: "w"})
	if trimmed, _, err := e.Store.trimRecording(r, now); err != nil || !trimmed {
		t.Fatalf("trim %v %v", trimmed, err)
	}
	noMeta("trim")
	r = recordingNow(t, e, recID)
	if !r.RoomMeeting() || r.Attending() || !r.MarkedNotPrivate() {
		t.Fatalf("trimmed %+v %+v", r, r.Conv)
	}
	if _, err := os.Stat(e.Store.path(KindRecording, recID, meetingFile)); err != nil {
		t.Fatalf("the trim removed meeting.json: %v", err)
	}
	if !searchable("recordings/"+recID+"/meta.json") || searchable("recordings/"+recID+"/"+meetingFile) {
		t.Error("meeting.json is searched")
	}
}

// room takes #2981's roles and rules: an unknown role or one on a recording that is not a meeting is off with the
// problem; a room that is not an object counts as absent; a private that is not a boolean is ignored, and a note's
// private is never taken.
func TestRoomUploadRoles(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	start := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, c := range []struct {
		name, extra       string
		room, private     bool
		role, problem     string
		markedNotPrivate  bool
		markedAnyPrivates bool
	}{
		{"listener", `"kind":"meeting","room":{"attendance":"listener"}`, true, false, AttendanceListener, "", false, false},
		{"off", `"kind":"meeting","room":{"attendance":"off"},"private":false`, true, false, AttendanceOff, "", true, true},
		{"empty room", `"kind":"meeting","room":{}`, true, false, AttendanceOff, "", false, false},
		{"unknown", `"kind":"meeting","room":{"attendance":"speaker"}`, true, false, AttendanceOff, AttendanceUnknownRole, false, false},
		{"not a string", `"kind":"meeting","room":{"attendance":3}`, true, false, AttendanceOff, AttendanceUnknownRole, false, false},
		{"a note", `"kind":"note","room":{"attendance":"listener"},"private":false`, true, false, AttendanceOff, AttendanceNotMeeting, false, false},
		{"room not an object", `"kind":"meeting","room":"participant","attendance":"listener"`, false, false, AttendanceListener, "", false, false},
		{"private not a boolean", `"kind":"meeting","room":{"attendance":"listener"},"private":"no"`, true, false, AttendanceListener, "", false, false},
		{"private alone", `"kind":"meeting","private":true`, false, true, AttendanceOff, "", false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := goldenEngine(t, nil)
			u := roomUploadJSON(t, `{"revision":1,"started_at":"`+start+`","device":"phone","segments":[],`+c.extra+`}`)
			put, err := e.PutRecording(recID, u, now)
			if err != nil || put.Room != c.room || put.Attendance != c.role || put.AttendanceProblem != c.problem {
				t.Fatalf("put %+v %v", put, err)
			}
			r := recordingNow(t, e, recID)
			if r.RoomMeeting() != c.room || r.MarkedNotPrivate() != c.markedNotPrivate || (r.Conv != nil && r.Conv.Private != nil) != c.markedAnyPrivates {
				t.Fatalf("stored %+v", r.Conv)
			}
		})
	}
}

// The mark: PUT …/private sets it any time (a trimmed recording too), only on a meeting as #2981 counts them; the
// upload's first private never undoes it; detail.meta shows it with the room.
func TestPrivateMarkAndDetail(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e := goldenEngine(t, nil)
	if _, err := e.SetPrivate(recID, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("no upload: %v", err)
	}
	note := "11111111-1111-4111-8111-111111111111"
	putRecording(t, e, note, recUpload(1, now, true, "hello"), now)
	if _, err := e.SetPrivate(note, true); !errors.Is(err, ErrNotMeeting) {
		t.Errorf("a note: %v", err)
	}
	changeRecordingMeta(t, e, note, func(r *Recording) { r.MinutesAsked = true })
	if _, err := e.SetPrivate(note, false); err != nil {
		t.Errorf("minutes asked: %v", err)
	}

	u := meetingUpload(1, now, "We keep the cap")
	u.SetRoom(AttendanceParticipant)
	u.Private = new(false)
	putRecording(t, e, recID, u, now)
	m, err := e.SetPrivate(recID, true)
	if err != nil || m.Private == nil || !*m.Private || m.Revs[PartPrivate] != 2 {
		t.Fatalf("mark %+v %v", m, err)
	}
	u.Revision, u.Private = 2, new(false)
	putRecording(t, e, recID, u, now)
	if r := recordingNow(t, e, recID); r.MarkedNotPrivate() {
		t.Fatal("an upload undid the mark")
	}
	if _, err := e.MeetingUpdate(recID, func(m *Meeting, _ Recording) error {
		m.Room.State, m.Room.MachineID, m.Room.SessionID, m.Room.Started = RoomReady, "server", "room-1", true
		m.Followup = &MeetingFollowup{MachineID: "server", SessionID: "fu-1"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d, err := e.Get(KindRecording, recID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Attendance       string          `json:"attendance"`
		AttendingSession json.RawMessage `json:"attending_session"`
		Private          *bool           `json:"private"`
		Room             *RoomView       `json:"room"`
		Followup         *ConvView       `json:"followup"`
	}
	if err := json.Unmarshal(d.Meta, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Attendance != AttendanceParticipant || meta.AttendingSession != nil || meta.Private == nil || !*meta.Private ||
		meta.Room == nil || *meta.Room != (RoomView{State: RoomReady, MachineID: "server", SessionID: "room-1"}) ||
		meta.Followup == nil || meta.Followup.SessionID != "fu-1" {
		t.Fatalf("detail.meta %s", d.Meta)
	}
	if strings.Contains(string(d.Meta), "revs") || strings.Contains(string(d.Meta), "op_id") {
		t.Fatalf("bookkeeping in detail.meta: %s", d.Meta)
	}

	// A trimmed recording takes the mark too.
	putMeetingMinutes(t, e, recID, Minutes{Version: 1, Title: "x", When: "y"})
	if _, _, err := e.Store.trimRecording(recordingNow(t, e, recID), now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetPrivate(recID, false); err != nil {
		t.Fatalf("trimmed: %v", err)
	}
	if _, err := e.DeleteRecording(recID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetPrivate(recID, false); !errors.Is(err, ErrRecordingDeleted) {
		t.Errorf("deleted: %v", err)
	}
}

// The room's ids show only once it started, never with none or deleted; its problem only while unavailable.
func TestRoomView(t *testing.T) {
	for _, c := range []struct {
		room MeetingRoom
		want RoomView
	}{
		{MeetingRoom{}, RoomView{State: RoomNone}},
		{MeetingRoom{State: RoomStarting, MachineID: "m", SessionID: "s", Pending: true}, RoomView{State: RoomStarting}},
		{MeetingRoom{State: RoomReady, MachineID: "m", SessionID: "s", Started: true}, RoomView{State: RoomReady, MachineID: "m", SessionID: "s"}},
		{MeetingRoom{State: RoomUnavailable, Problem: RoomNodeTooOld}, RoomView{State: RoomUnavailable, Problem: RoomNodeTooOld}},
		{MeetingRoom{State: RoomUnavailable, Problem: RoomStartFailed, MachineID: "m", SessionID: "s"}, RoomView{State: RoomUnavailable, Problem: RoomStartFailed}},
		{MeetingRoom{State: RoomEnded, Problem: RoomStartFailed, MachineID: "m", SessionID: "s", Started: true}, RoomView{State: RoomEnded, MachineID: "m", SessionID: "s"}},
		{MeetingRoom{State: RoomDeleted, MachineID: "m", SessionID: "s", Started: true}, RoomView{State: RoomDeleted}},
	} {
		room := c.room
		if got := (Meeting{Room: &room}).RoomView(); *got != c.want {
			t.Errorf("%+v: %+v, want %+v", c.room, *got, c.want)
		}
	}
	if (Meeting{}).RoomView() != nil || (Meeting{Followup: &MeetingFollowup{SessionID: "f", Pending: true}}).FollowupView() != nil {
		t.Error("views of what is not there")
	}
}

// Asks changing raise their revision and tell MeetingChanged; other changes of the live log do not.
func TestAsksRevision(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e := goldenEngine(t, nil)
	putRecording(t, e, recID, attendedUpload(1, now, AttendanceListener), now)
	var changed []string
	e.MeetingChanged = func(_ string, parts []string) { changed = append(changed, parts...) }
	l, err := e.LiveUpdate(recID, func(l *Live, _ Recording) error { l.Asks = append(l.Asks, LiveAsk{ID: "a"}); return nil })
	if err != nil || l.AsksRev != 1 || !slices.Equal(changed, []string{PartAsks}) {
		t.Fatalf("%+v %v %v", l, err, changed)
	}
	l, _ = e.LiveUpdate(recID, func(l *Live, _ Recording) error { l.Fed = 3; return nil })
	if l.AsksRev != 1 || len(changed) != 1 {
		t.Fatalf("a change that is not the asks: %+v %v", l, changed)
	}
}

// An upload that changes a meeting field never writes back what it read before taking meeting.json's lock: the
// private mark a PUT …/private set meanwhile stays, and its revision does not go back down.
func TestUploadKeepsAMeetingUpdateMadeMeanwhile(t *testing.T) {
	now := time.Date(2026, 11, 10, 14, 0, 0, 0, goldenZone)
	e := goldenEngine(t, nil)
	start := now.Add(-time.Hour).UTC().Format(time.RFC3339)
	body := func(rev int, role string) string {
		return `{"revision":` + string(rune('0'+rev)) + `,"started_at":"` + start + `","device":"phone","kind":"meeting","segments":[],"room":{"attendance":"` + role + `"},"private":false}`
	}
	if _, err := e.PutRecording(recID, roomUploadJSON(t, body(1, AttendanceListener)), now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SetPrivate(recID, true); err != nil { // after the upload below read the recording, before its lock
		t.Fatal(err)
	}
	u := roomUploadJSON(t, body(2, AttendanceParticipant))
	u.Kind = RecordingMeeting
	m, parts, err := e.putMeetingFields(recID, &u)
	if err != nil {
		t.Fatal(err)
	}
	if m.Private == nil || !*m.Private || m.Revs[PartPrivate] != 2 || !slices.Equal(parts, []string{PartRoom}) {
		t.Fatalf("the mark set meanwhile was written over: %+v %v %v", m.Private, m.Revs, parts)
	}
	if r := recordingNow(t, e, recID); r.MarkedNotPrivate() || r.Conv.Room.Attendance != AttendanceParticipant {
		t.Fatalf("stored %+v", r.Conv)
	}
}
