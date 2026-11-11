package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The meeting's room conversation (#3862, docs/assistant/meeting-minutes.md "The room conversation"). All names and
// texts are placeholders.

// roomFixture is a meetingFixture whose node starts rooms, with the feeder's timers off: the test looks itself.
type roomFixture struct {
	*meetingFixture
	prepared []map[string]any
}

func newRoomFixture(t *testing.T) *roomFixture {
	t.Helper()
	m := newMeetingFixture(t)
	r := &roomFixture{meetingFixture: m}
	m.s.attend.feed.after = func(time.Duration, func()) func() bool { return func() bool { return true } }
	m.f.mu.Lock()
	m.f.nodes = map[string]nodeAnswer{DeviceAssistantGuestPrepare: func(_ string, params json.RawMessage) (any, error) {
		var a map[string]any
		_ = json.Unmarshal(params, &a)
		r.prepared = append(r.prepared, a)
		return map[string]any{"cwd": "/home/me/.persona/guest-meeting/" + path.Base(a["dir"].(string)), "kind": "meeting", "start_ids": true}, nil
	}}
	m.f.sessions["server"] = []proto.Session{{SessionID: "c-main", Assistant: true, Status: proto.StatusIdle, State: proto.StateIdle}}
	m.f.known["c-main"] = true
	m.f.hello.Locale = "en"
	m.f.mu.Unlock()
	shortRoomAsks(t)
	return r
}

// shortRoomAsks makes the room asks' polls short for one test.
func shortRoomAsks(t *testing.T) {
	t.Helper()
	p := askRoomPoll
	askRoomPoll = 5 * time.Millisecond
	t.Cleanup(func() { askRoomPoll = p })
}

// look looks at recording id once, as the feeder's timer would.
func (r *roomFixture) look(id string) time.Time { return r.s.attendLook(context.Background(), id) }

// meeting is recording id's meeting.json now.
func (r *roomFixture) meeting(id string) kb.Meeting {
	r.t.Helper()
	m, _, err := r.engine().MeetingRead(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return m
}

// roomUp uploads id as a room meeting in role and starts its room; the room's session id, listed idle.
func (r *roomFixture) roomUp(id, role string) string {
	r.t.Helper()
	out := r.upload(id, 1, map[string]any{"room": map[string]any{"attendance": role}, "title": "Design review", "private": false})
	if room, _ := out["room"].(map[string]any); room["state"] != "none" || out["attendance"] != role {
		r.t.Fatalf("upload: %v", out)
	}
	r.look(id)
	m := r.meeting(id)
	if m.Room == nil || m.Room.State != kb.RoomReady {
		r.t.Fatalf("room %+v", m.Room)
	}
	r.f.mu.Lock()
	r.f.sessions["server"] = append(r.f.sessions["server"], proto.Session{SessionID: m.Room.SessionID, Assistant: true, Status: proto.StatusIdle,
		State: proto.StateIdle, Origin: OriginMeeting})
	r.f.mu.Unlock()
	return m.Room.SessionID
}

func (r *roomFixture) sends() []map[string]any {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return slices.Clone(r.f.sends)
}

func (r *roomFixture) sentOnce() []map[string]any {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return slices.Clone(r.f.sentOnce)
}

// The room starts once the role is not off, after the upload answered: in its own directory with the meeting
// CLAUDE.md, under the session id derived from the recording, as a guest of kind meeting with the meeting tools,
// its first message the meeting header (message source meeting); it is a guest and an origin meeting, titled
// "MMDD | 会议 | title". The upload's 200 carries room; its meta.json never an attendance. A start asked again is the
// same conversation: nothing is sent twice.
func TestMeetingRoomStarts(t *testing.T) {
	r := newRoomFixture(t)
	out := r.upload(meetingRec, 1, map[string]any{"room": map[string]any{"attendance": "off"}, "attendance": "participant", "title": "Design review"})
	if out["attendance"] != "off" || out["room"].(map[string]any)["state"] != "none" {
		t.Fatalf("upload: %v", out)
	}
	if next := r.look(meetingRec); !next.IsZero() || len(r.sends()) != 0 {
		t.Fatalf("a room that stays off started: %v %v", next, r.sends())
	}
	sid := r.roomUp(meetingRec, "participant")
	if sid != meetingSessionID(meetingRec, partRoom) {
		t.Fatalf("room id %s", sid)
	}
	if len(r.prepared) != 1 || r.prepared[0]["kind"] != "meeting" || r.prepared[0]["dir"] != meetingRoomDir+"/"+meetingRec ||
		!strings.Contains(r.prepared[0]["claude_md"].(string), "write nothing") {
		t.Fatalf("prepared %v", r.prepared)
	}
	sends := r.sends()
	if len(sends) != 1 {
		t.Fatalf("sends %v", sends)
	}
	start := sends[0]
	guest, _ := start["guest"].(map[string]any)
	text, _ := start["text"].(string)
	if start["new_session_id"] != sid || start["client_msg_id"] != "meeting-"+meetingRec || guest["kind"] != "meeting" || len(guest["tools"].([]any)) != 3 || start["is_main"] != false ||
		start["config_dir"] != meetingProfile || !strings.Contains(text, "Title: Design review") || !strings.Contains(text, `"kind":"meeting"`) || !strings.Contains(text, "Role: participant") {
		t.Fatalf("start %v", start)
	}
	if rec, ok, err := r.s.roomOf(context.Background(), "server", sid); err != nil || !ok || rec != meetingRec {
		t.Fatalf("room book %s %v %v", rec, ok, err)
	}
	if !r.s.isRoom(sid) || !r.s.meetingTurnNow("server", sid) {
		t.Fatal("the room's turns are not side exchanges")
	}
	origins, _ := r.s.conversationOrigins()
	if o := origins[sid]; o.origin != OriginMeeting || o.ref != meetingRec {
		t.Fatalf("origin %+v", o)
	}
	if by, _ := r.s.titleOf(sid); by != titleByTask {
		t.Fatalf("titled by %q", by)
	}
	raw, err := json.Marshal(r.meeting(meetingRec).RoomView())
	if err != nil || string(raw) != `{"state":"ready","machine_id":"server","session_id":"`+sid+`"}` {
		t.Fatalf("room view %s", raw)
	}

	// Asked again (a restart left it pending): the node has it, so nothing is sent again.
	if _, err := r.engine().MeetingUpdate(meetingRec, func(m *kb.Meeting, _ kb.Recording) error {
		m.Room.State, m.Room.Pending, m.Room.StartingAt = kb.RoomStarting, true, time.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.look(meetingRec)
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomReady || m.Room.SessionID != sid || m.Room.Pending {
		t.Fatalf("settled %+v", m.Room)
	}
	if n := len(r.sends()); n != 2 {
		t.Fatalf("%d starts", n)
	}
}

// A node that cannot run a room: one older than rooms (its prepare does not say it starts them) is node_too_old,
// never retried; a computer offline is machine_offline, retried every minute; a start not ready within 2 minutes is
// start_failed.
func TestMeetingRoomUnavailable(t *testing.T) {
	r := newRoomFixture(t)
	r.f.mu.Lock()
	r.f.nodes[DeviceAssistantGuestPrepare] = func(string, json.RawMessage) (any, error) {
		return map[string]any{"cwd": "/home/me/.persona/guest"}, nil // an older node
	}
	r.f.mu.Unlock()
	r.upload(meetingRec, 1, map[string]any{"room": map[string]any{"attendance": "listener"}})
	r.look(meetingRec)
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomUnavailable || m.Room.Problem != kb.RoomNodeTooOld || len(r.sends()) != 0 {
		t.Fatalf("too old: %+v %v", m.Room, r.sends())
	}
	if next := r.look(meetingRec); !next.After(time.Now().Add(time.Hour)) {
		t.Fatalf("node_too_old looked at again at %v", next)
	}

	r.f.mu.Lock()
	r.f.devices = []Device{{ID: "server", Name: "server", Online: false}}
	delete(r.f.nodes, DeviceAssistantGuestPrepare)
	r.f.mu.Unlock()
	r.upload(meetingOther, 1, map[string]any{"room": map[string]any{"attendance": "listener"}})
	next := r.look(meetingOther)
	m := r.meeting(meetingOther)
	if m.Room.State != kb.RoomUnavailable || m.Room.Problem != kb.RoomMachineOffline || next.After(time.Now().Add(2*time.Minute)) {
		t.Fatalf("offline: %+v next %v", m.Room, next)
	}
	// Retried after a minute: a start that does not get ready in 2 minutes failed.
	if _, err := r.engine().MeetingUpdate(meetingOther, func(m *kb.Meeting, _ kb.Recording) error {
		m.Room.State, m.Room.Problem, m.Room.StartingAt, m.Room.Pending = kb.RoomStarting, "", time.Now().Add(-3*time.Minute), true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.look(meetingOther)
	if m := r.meeting(meetingOther); m.Room.State != kb.RoomUnavailable || m.Room.Problem != kb.RoomStartFailed {
		t.Fatalf("start failed: %+v", m.Room)
	}
	view := r.do("GET", livePath(meetingOther), "", nil, http.StatusOK)
	if room := view["room"].(map[string]any); room["state"] != "unavailable" || room["problem"] != "start_failed" || room["session_id"] != nil {
		t.Fatalf("view %v", view)
	}
}

// The room is fed as an attending session is, but exempt for its own meeting from "Sessions that never attend"
// (it is a guest), with no meeting-turn mark, and told to write nothing; it never attends another meeting.
func TestMeetingRoomFed(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "listener")
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":1,"end":3,"text":"Ada: we keep the cap."}]}`, http.StatusOK)
	before := len(r.sends())
	r.look(meetingRec)
	sends := r.sends()
	if len(sends) != before+1 {
		t.Fatalf("no batch: %v", sends)
	}
	batch := sends[len(sends)-1]
	text, _ := batch["text"].(string)
	src, _ := batch["message_source"].(map[string]any)
	if batch["session_id"] != sid || batch["resume_only"] != true || src["kind"] != proto.SourceMeeting ||
		!strings.Contains(text, "Ada: we keep the cap.") || !strings.Contains(text, "Write nothing in reply.") {
		t.Fatalf("batch %v", batch)
	}
	if running, _ := r.s.meetingTurnRunning(context.Background(), "server", sid); running {
		t.Fatal("a meeting-turn mark on the room")
	}
	if l, _, _ := r.engine().LiveRead(meetingRec); l.Fed != 1 || l.FedTo == nil || l.FedTo.SessionID != sid {
		t.Fatalf("fed %+v", l)
	}

	// The next batch tells the room of the asks meeting.answer took, never of its own.
	if _, err := r.engine().LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error {
		l.FedAt = l.FedAt.Add(-2 * feedBatchEvery)
		now := time.Now().UTC()
		l.Asks = append(l.Asks,
			kb.LiveAsk{ID: askOne, Question: "q1", State: askAnswered, Answer: "ROOM-ANSWER", Via: kb.AskViaRoom, AskedAt: now, Sources: []kb.AskSource{}},
			kb.LiveAsk{ID: askTwo, Question: "q2", State: askAnswered, Answer: "MATERIAL-ANSWER", Via: kb.AskViaMaterial, AskedAt: now, Sources: []kb.AskSource{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.look(meetingRec)
	sends = r.sends()
	if text, _ := sends[len(sends)-1]["text"].(string); !strings.Contains(text, "MATERIAL-ANSWER") || strings.Contains(text, "ROOM-ANSWER") {
		t.Fatalf("asks batch %v", sends[len(sends)-1])
	}

	// Another meeting (a #2981 one) naming the room as its attending session: refused.
	r.upload(meetingOther, 1, map[string]any{"attendance": "listener", "attending_session": map[string]any{"machine_id": "server", "session_id": sid}})
	r.do("POST", livePath(meetingOther), "", `{"from":0,"segments":[{"start":1,"end":3,"text":"Bo: hello"}]}`, http.StatusOK)
	r.look(meetingOther)
	if l, _, _ := r.engine().LiveRead(meetingOther); l.Problem != attendeeNotAllowed {
		t.Fatalf("another meeting fed the room: %+v", l)
	}
}

// The meeting's end: a ready room whose role is on gets the end batch and turns ended once that turn ends; a room
// whose role is off, or still starting, ends at once; one never started stays none. A message into an ended room is
// refused: 409 meeting_ended.
func TestMeetingRoomEnds(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "participant")
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":1,"end":3,"text":"Ada: done for today."}]}`, http.StatusOK)
	end := time.Now().UTC().Format(time.RFC3339)
	r.upload(meetingRec, 2, map[string]any{"room": map[string]any{"attendance": "participant"}, "ended_at": end, "complete": true})
	r.look(meetingRec)
	sends := r.sends()
	if text, _ := sends[len(sends)-1]["text"].(string); !strings.Contains(text, "The meeting has ended") {
		t.Fatalf("end batch %v", sends[len(sends)-1])
	}
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomReady || m.Room.EndingAt.IsZero() {
		t.Fatalf("ended before its turn: %+v", m.Room)
	}
	if _, err := r.s.SendParams(context.Background(), SendParams{MachineID: "server", SessionID: sid, Params: map[string]any{"text": "x"}}); err != nil {
		t.Fatalf("a message while it ends: %v", err)
	}
	// Its turn ended (the row is idle again, newer than the batch).
	r.f.mu.Lock()
	for i := range r.f.sessions["server"] {
		if r.f.sessions["server"][i].SessionID == sid {
			r.f.sessions["server"][i].UpdatedAt = time.Now().Add(time.Second)
		}
	}
	r.f.mu.Unlock()
	r.look(meetingRec)
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomEnded || m.Room.SessionID != sid {
		t.Fatalf("not ended: %+v", m.Room)
	}
	_, err := r.s.SendParams(context.Background(), SendParams{MachineID: "server", SessionID: sid, Params: map[string]any{"text": "x"}})
	if !isCode(err, CodeMeetingEnded) {
		t.Fatalf("a message into an ended room: %v", err)
	}
	// An ask: 409 meeting_ended.
	wantCode(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "x"}, http.StatusConflict), codeMeetingEnded)

	// Role off at the end: ended at once. A room that never came up stays none.
	r.roomUp(meetingOther, "listener")
	r.upload(meetingOther, 2, map[string]any{"room": map[string]any{"attendance": "off"}, "ended_at": end, "complete": true})
	r.look(meetingOther)
	if m := r.meeting(meetingOther); m.Room.State != kb.RoomEnded {
		t.Fatalf("off at the end: %+v", m.Room)
	}
	third := "66666666-6666-4666-8666-666666666666"
	r.upload(third, 1, map[string]any{"room": map[string]any{"attendance": "off"}, "ended_at": end, "complete": true})
	r.look(third)
	if m := r.meeting(third); m.Room.State != kb.RoomNone {
		t.Fatalf("never started: %+v", m.Room)
	}
}

// isCode: err is an rpc error with code.
func isCode(err error, code string) bool {
	var e *rpc.Error
	return errors.As(err, &e) && e.Code == code
}

// The room's tools: its meeting tools only, bound to its recording; meeting_lines reads its lines, meeting_earlier
// only earlier meetings marked not private; any other tool is refused.
func TestMeetingRoomTools(t *testing.T) {
	r := newRoomFixture(t)
	r.engine().EarlierMeetingsInAnswers = true
	r.earlierMeeting("Exports over four megabytes are dropped")
	sid := r.roomUp(meetingRec, "listener")
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":61,"end":64,"text":"Ada: keep the cap."},{"start":3700,"end":3705,"text":"Bo: later."}]}`, http.StatusOK)
	tools := r.s.sessionMCPTools(context.Background(), "server", sid, true, true)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	if !slices.Equal(names, []string{"meeting_lines", "meeting_earlier", "meeting_photo_note"}) {
		t.Fatalf("tools %v", names)
	}
	d := newFleetDevs() // the nodes' requests are the fleet's
	d.add("server", "Server")
	r.s.devs = d
	call := func(name, args string) proto.ToolResult {
		out, ok, err := r.s.NodeRequest(context.Background(), "server", "mcp.call", json.RawMessage(`{"name":"`+name+`","args":`+args+`,"session_id":"`+sid+`","assistant":true,"guest":true}`))
		if !ok || err != nil {
			t.Fatalf("%s: %v %v", name, ok, err)
		}
		return out.(proto.ToolResult)
	}
	text := func(res proto.ToolResult) string {
		var b strings.Builder
		for _, c := range res.Content {
			b.WriteString(c.Text)
		}
		return b.String()
	}
	if got := text(call("meeting_lines", `{"to":100}`)); got != "[1:01] Ada: keep the cap." {
		t.Fatalf("lines %q", got)
	}
	if got := text(call("meeting_earlier", `{"query":"export cap"}`)); !strings.Contains(got, "Keep the cap for this release") {
		t.Fatalf("earlier %q", got)
	}
	r.do("PUT", "/api/v1/recordings/"+earlier+"/private", "", map[string]any{"private": true}, http.StatusOK)
	if got := text(call("meeting_earlier", `{"query":"export cap"}`)); strings.Contains(got, "Keep the cap") {
		t.Fatalf("a private meeting was material: %q", got)
	}
	if res := call("kb_search", `{"query":"x"}`); !res.IsError {
		t.Fatalf("another tool ran: %v", res)
	}
}

// Asking the room: a typed question goes to the room as the owner's own message under ask-<id>-1, and its answer is
// the room's reply, Markdown removed (via room); asked again after a failure it goes under ask-<id>-2. A heard
// question goes with source meeting and keeps question "" with its lines. While the room is unavailable the ask is
// answered from meeting material (via material), and a room ask asked again then fails with room_unavailable.
func TestMeetingRoomAsk(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "participant")
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":10,"end":14,"text":"Persona, who owns the test?"}]}`, http.StatusOK)
	r.f.mu.Lock()
	r.f.items = []proto.Item{
		{ID: "u1", Seq: 1, Role: "user", Kind: proto.KindText, Text: "Who owns the test?", ClientMsgID: askMsgID(askOne, 1)},
		{ID: "a1", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "**Bo** owns it. See [the minutes](https://example.com)."},
	}
	r.f.mu.Unlock()
	a := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "Who owns the test?", "spoken": true}, http.StatusOK))
	if a["state"] != "answered" || a["via"] != "room" || a["answer"] != "Bo owns it. See the minutes." || a["question_source"] != "spoken" || a["speak"] != true {
		t.Fatalf("ask %v", a)
	}
	once := r.sentOnce()
	if len(once) != 1 || once[0]["session_id"] != sid || once[0]["client_msg_id"] != askMsgID(askOne, 1) || once[0]["text"] != "Who owns the test?" ||
		once[0]["message_source"] != nil {
		t.Fatalf("sent %v", once)
	}
	if calls := r.model.called(); len(calls) != 0 {
		t.Fatalf("meeting.answer was asked: %d", len(calls))
	}

	r.f.mu.Lock()
	r.f.items = append(r.f.items, proto.Item{ID: "u2", Seq: 3, Role: "user", Kind: proto.KindText, Text: "heard", ClientMsgID: askMsgID(askTwo, 1)},
		proto.Item{ID: "a2", Seq: 4, Role: "assistant", Kind: proto.KindText, Text: "Bo owns the test."})
	r.f.mu.Unlock()
	at := 15.0
	h := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askTwo, "at": at}, http.StatusOK))
	heard, _ := h["heard"].([]any)
	if h["state"] != "answered" || h["question"] != "" || h["question_source"] != "heard" || len(heard) != 1 || h["answer"] != "Bo owns the test." {
		t.Fatalf("heard %v", h)
	}
	once = r.sentOnce()
	if text, _ := once[1]["text"].(string); !strings.Contains(text, `"kind":"meeting"`) || !strings.Contains(text, "Persona, who owns the test?") {
		t.Fatalf("heard message %v", once[1])
	}

	// The room unavailable: meeting.answer answers at once.
	if _, err := r.engine().MeetingUpdate(meetingRec, func(m *kb.Meeting, _ kb.Recording) error {
		m.Room.State, m.Room.Problem = kb.RoomUnavailable, kb.RoomMachineOffline
		m.Room.RetryAt = time.Now().Add(time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	m := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askThree, "question": "Anything else?"}, http.StatusOK))
	if m["via"] != "material" || m["state"] != "answered" || len(r.model.called()) != 1 {
		t.Fatalf("material %v", m)
	}

	// A failed room ask asked again while the room is unavailable: room_unavailable; once ready, attempt 2.
	if _, err := r.engine().LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error {
		for i := range l.Asks {
			if l.Asks[i].ID == askOne {
				l.Asks[i] = failAsk(l.Asks[i], askNoAnswer)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	again := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "Who owns the test?", "spoken": true}, http.StatusOK))
	if again["state"] != "failed" || again["error"] != askRoomUnavailble || again["via"] != "room" {
		t.Fatalf("asked again while unavailable: %v", again)
	}
	if _, err := r.engine().MeetingUpdate(meetingRec, func(m *kb.Meeting, _ kb.Recording) error {
		m.Room.State, m.Room.Problem = kb.RoomReady, ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.f.mu.Lock()
	r.f.items = append(r.f.items, proto.Item{ID: "u5", Seq: 5, Role: "user", Kind: proto.KindText, Text: "Who owns the test?", ClientMsgID: askMsgID(askOne, 2)},
		proto.Item{ID: "a5", Seq: 6, Role: "assistant", Kind: proto.KindText, Text: "Still Bo."})
	r.f.mu.Unlock()
	third := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "Who owns the test?", "spoken": true}, http.StatusOK))
	once = r.sentOnce()
	if third["answer"] != "Still Bo." || once[len(once)-1]["client_msg_id"] != askMsgID(askOne, 2) {
		t.Fatalf("attempt 2: %v %v", third, once[len(once)-1])
	}
	// The same id with another spoken flag is another request.
	wantCode(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "Who owns the test?"}, http.StatusConflict), codeOpIDReused)
}

// A main chat on the Agent SDK does not keep its room from starting: the room takes the main chat's launcher and model
// on the CLI, the only runtime a guest conversation runs on.
func TestMeetingRoomRunsOnTheCLI(t *testing.T) {
	r := newRoomFixture(t)
	if err := r.s.saveModelRecord(context.Background(), "assistant", 0, ModelBinding{Runtime: proto.AssistantRuntimeAgentSDK}); err != nil {
		t.Fatal(err)
	}
	main := map[string]any{}
	if err := r.s.applyModel(context.Background(), "assistant", main); err != nil || main["runtime"] != proto.AssistantRuntimeAgentSDK {
		t.Fatalf("the main chat's runtime %v %v", main, err)
	}
	r.roomUp(meetingRec, "participant")
	sends := r.sends()
	if len(sends) != 1 || sends[0]["runtime"] != nil || sends[0]["launcher"] != main["launcher"] || sends[0]["model_fingerprint"] == main["model_fingerprint"] {
		t.Fatalf("room start %v, main chat %v", sends, main)
	}
}

// A room whose turn ended in Claude Code's own words for a failed request (signed out) gives no answer: the ask fails
// with model_error, never "Not logged in · Please run /login" as the room's reply, and the room turns unavailable so
// the next ask is answered from meeting material.
func TestMeetingRoomAskTurnError(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "participant")
	signedOut := "Not logged in · Please run /login"
	r.f.mu.Lock()
	r.f.items = []proto.Item{
		{ID: "u1", Seq: 1, Role: "user", Kind: proto.KindText, Text: "Who owns the test?", ClientMsgID: askMsgID(askOne, 1)},
		{ID: "a1", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: signedOut},
	}
	for i, c := range r.f.sessions["server"] {
		if c.SessionID == sid {
			c.Status, c.State, c.TurnError, c.TurnErrorKind = proto.StatusDone, proto.StateUnread, signedOut, proto.TurnErrorUser
			r.f.sessions["server"][i] = c
		}
	}
	r.f.mu.Unlock()
	a := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "Who owns the test?"}, http.StatusOK))
	if a["state"] != "failed" || a["error"] != askModelError || a["via"] != "room" || a["answer"] != nil && a["answer"] != "" {
		t.Fatalf("ask %v", a)
	}
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomUnavailable || m.Room.Problem != kb.RoomStartFailed || m.Room.RetryAt.IsZero() {
		t.Fatalf("room %+v", m.Room)
	}
	b := meetingAskOf(t, r.do("POST", askPath(meetingRec), "", map[string]any{"id": askTwo, "question": "Anything else?"}, http.StatusOK))
	if b["via"] != "material" || b["state"] != "answered" || len(r.model.called()) != 1 {
		t.Fatalf("material %v", b)
	}
}

// After a restart an ask pending in the room is taken up again, not failed: its message is found in the room and its
// turn's reply settles it.
func TestMeetingRoomAskResumes(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	r.f.mu.Lock()
	r.f.items = []proto.Item{
		{ID: "u1", Seq: 1, Role: "user", Kind: proto.KindText, Text: "q", ClientMsgID: askMsgID(askOne, 1)},
		{ID: "a1", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "The answer."},
	}
	r.f.mu.Unlock()
	if _, err := r.engine().LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error {
		l.Asks = append(l.Asks, kb.LiveAsk{ID: askOne, QuestionSent: "q", Question: "q", QuestionSource: askTyped, State: askPending, Via: kb.AskViaRoom,
			Sources: []kb.AskSource{}, AskedAt: time.Now().UTC(), Attempts: []kb.AskAttempt{{K: 1, MsgID: askMsgID(askOne, 1), SentAt: time.Now().UTC()}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.asksOf(meetingRec) // a read takes it up
	r.idle()
	asks := r.asksOf(meetingRec)
	if len(asks) != 1 || asks[0]["state"] != "answered" || asks[0]["answer"] != "The answer." {
		t.Fatalf("resumed %v", asks)
	}
	if len(r.sentOnce()) != 0 {
		t.Fatal("sent again though the room had it")
	}
}

// The feeder takes up an ask a restart left pending in the room as a read of the asks does: a phone that only streams
// lines reads none, and the batches wait for that ask. Its room turn settles it, then the batch goes.
func TestMeetingRoomAskResumesForTheFeeder(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	r.f.mu.Lock()
	r.f.items = []proto.Item{
		{ID: "u1", Seq: 1, Role: "user", Kind: proto.KindText, Text: "q", ClientMsgID: askMsgID(askOne, 1)},
		{ID: "a1", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "The answer."},
	}
	r.f.mu.Unlock()
	if _, err := r.engine().LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error {
		l.Asks = append(l.Asks, kb.LiveAsk{ID: askOne, QuestionSent: "q", Question: "q", QuestionSource: askTyped, State: askPending, Via: kb.AskViaRoom,
			Sources: []kb.AskSource{}, AskedAt: time.Now().UTC(), Attempts: []kb.AskAttempt{{K: 1, MsgID: askMsgID(askOne, 1), SentAt: time.Now().UTC()}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.s.attendResume(r.engine())
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":10,"end":14,"text":"Ada: we keep the cap."}]}`, http.StatusOK)
	sends := len(r.sends())
	r.look(meetingRec)
	r.idle()
	l, _, err := r.engine().LiveRead(meetingRec)
	if err != nil || len(l.Asks) != 1 || l.Asks[0].State != askAnswered || l.Asks[0].Answer != "The answer." {
		t.Fatalf("the feeder left the ask: %+v %v", l.Asks, err)
	}
	r.look(meetingRec)
	if got := r.sends(); len(got) != sends+1 || !strings.Contains(got[len(got)-1]["text"].(string), "we keep the cap") {
		t.Fatalf("no batch after the ask: %v", got[sends:])
	}
}

// GET …/live: private, room and followup, each part's revision, and ?parts= for only some of them; the meeting_live
// event says what changed with the revisions, nothing of the meeting.
func TestMeetingLiveParts(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	view := r.do("GET", livePath(meetingRec), "", nil, http.StatusOK)
	revs, _ := view["revs"].(map[string]any)
	if view["private"] != false || view["room"].(map[string]any)["state"] != "ready" || revs["room"] == nil || revs["private"] == nil ||
		view["attending_session"] != nil || view["attendance"] != "listener" {
		t.Fatalf("view %v", view)
	}
	only := r.do("GET", livePath(meetingRec)+"?parts=private,asks", "", nil, http.StatusOK)
	if _, has := only["room"]; has || only["private"] != false || only["asks"] == nil || len(only["revs"].(map[string]any)) != 2 {
		t.Fatalf("parts %v", only)
	}
	r.do("PUT", "/api/v1/recordings/"+meetingRec+"/private", "", map[string]any{"private": true}, http.StatusOK)
	waitFor(t, "a meeting_live event", func() bool {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		return slices.Contains(r.f.events, "meeting_live")
	})
	d, err := r.engine().Get(kb.KindRecording, meetingRec, 0)
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	_ = json.Unmarshal(d.Meta, &meta)
	if meta["private"] != true || meta["attendance"] != "listener" || meta["room"].(map[string]any)["state"] != "ready" || meta["revs"] != nil {
		t.Fatalf("detail.meta %v", meta)
	}
}

// PUT …/private: the contract's errors in order; a note is not_meeting.
func TestMeetingPrivateRoute(t *testing.T) {
	r := newRoomFixture(t)
	p := "/api/v1/recordings/" + meetingRec + "/private"
	r.do("PUT", p, "answer_only", map[string]any{"private": true}, http.StatusForbidden)
	r.do("PUT", p, "", strings.Repeat("x", 2000), http.StatusRequestEntityTooLarge)
	wantCode(t, r.do("PUT", p, "", map[string]any{"private": true}, http.StatusNotFound), codeRecordingNotFound)
	r.upload(meetingRec, 1, map[string]any{"kind": "note"})
	r.do("PUT", p, "", map[string]any{"private": "yes"}, http.StatusBadRequest)
	r.do("PUT", p, "", `{"private":null}`, http.StatusBadRequest)
	r.do("PUT", p, "", `{}`, http.StatusBadRequest)
	wantCode(t, r.do("PUT", p, "", map[string]any{"private": true}, http.StatusConflict), codeNotMeeting)
	r.upload(meetingRec, 2, nil)
	r.do("PUT", p, "", `{"private":null}`, http.StatusBadRequest)
	if m := r.meeting(meetingRec); m.Private != nil {
		t.Fatalf("a null private set the mark: %v", *m.Private)
	}
	if out := r.do("PUT", p, "", map[string]any{"private": true}, http.StatusOK); out["private"] != true {
		t.Fatalf("%v", out)
	}
	r.do("DELETE", "/api/v1/recordings/"+meetingRec, "", nil, http.StatusOK)
	wantCode(t, r.do("PUT", p, "", map[string]any{"private": false}, http.StatusGone), codeRecordingDeleted)
}

// At the transcript's cut the room is deleted: the node's assistant.guest_delete with its directory and session, the
// room forgotten (guest and origin) and deleted in meeting.json. A node that cannot be asked keeps it ended, asked
// again at the next pass. A room whose recording is gone is found by its origin and deleted too.
func TestMeetingRoomDeleted(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "listener")
	e := r.engine()
	if _, err := e.MeetingUpdate(meetingRec, func(m *kb.Meeting, _ kb.Recording) error { m.Room.State = kb.RoomEnded; return nil }); err != nil {
		t.Fatal(err)
	}
	var deletes []map[string]any
	fail := errors.New("unknown method assistant.guest_delete")
	r.f.mu.Lock()
	r.f.nodes[DeviceAssistantGuestDelete] = func(_ string, params json.RawMessage) (any, error) {
		var a map[string]any
		_ = json.Unmarshal(params, &a)
		deletes = append(deletes, a)
		if fail != nil {
			return nil, fail
		}
		return map[string]any{"deleted": true}, nil
	}
	r.f.mu.Unlock()
	// Trimmed: its transcript cut (minutes kept).
	r.upload(meetingRec, 2, map[string]any{"room": map[string]any{"attendance": "listener"}, "complete": true,
		"ended_at": time.Now().UTC().Format(time.RFC3339), "segments": []map[string]any{{"start": 0, "end": 9, "text": "We keep the cap"}}})
	r.minutesFor(meetingRec, `{"title":"Design review","decisions":[{"text":"Keep the cap","line":1}]}`)
	e.PruneRecordings(time.Now().AddDate(0, 0, 30))
	if rec, _ := e.Store.Recording(meetingRec); rec == nil || !rec.Trimmed() {
		t.Fatal("not trimmed")
	}
	r.s.meetingRoomsSweep(context.Background(), e)
	if len(deletes) != 1 || deletes[0]["session_id"] != sid || deletes[0]["dir"] != meetingRoomDir+"/"+meetingRec {
		t.Fatalf("deletes %v", deletes)
	}
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomEnded || m.Room.SessionID != sid {
		t.Fatalf("a node too old: %+v", m.Room)
	}
	fail = nil
	r.s.meetingRoomsSweep(context.Background(), e)
	if m := r.meeting(meetingRec); m.Room.State != kb.RoomDeleted || m.Room.SessionID != "" {
		t.Fatalf("deleted: %+v", m.Room)
	}
	if r.s.isRoom(sid) {
		t.Fatal("the room is still in the book")
	}
	if o, _ := r.s.conversationOrigins(); o[sid].origin != "" {
		t.Fatal("its origin is still there")
	}

	// A recording deleted: its room goes with it, found by its origin.
	other := r.roomUp(meetingOther, "listener")
	r.do("DELETE", "/api/v1/recordings/"+meetingOther, "", nil, http.StatusOK)
	waitFor(t, "the deleted recording's room deleted", func() bool { return !r.s.isRoom(other) })
}
