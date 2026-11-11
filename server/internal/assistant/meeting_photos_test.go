package assistant

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// Meeting photos (#3862 phase 3, docs/assistant/meeting-minutes.md "Phase 3: photos"). All names and texts are
// placeholders.

const (
	photoOne   = "b0000000-0000-4000-8000-000000000001"
	photoTwo   = "b0000000-0000-4000-8000-000000000002"
	photoThree = "b0000000-0000-4000-8000-000000000003"
	photoFour  = "b0000000-0000-4000-8000-000000000004"
)

func photosPath(id string) string { return "/api/v1/recordings/" + id + "/photos" }

// errMeetingTest is a node's failure to take a message.
var errMeetingTest = errors.New("the message was not taken")

// pngData is a w × h PNG in base64.
func pngData(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// jpegData is a w × h JPEG in base64.
func jpegData(t *testing.T, w, h int) string {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

// photoBody is a POST …/photos body: a 16 × 9 PNG taken at seconds into the recording, with fields over it.
func photoBody(t *testing.T, id string, at float64, fields map[string]any) map[string]any {
	t.Helper()
	body := map[string]any{"id": id, "at": at, "mime": "image/png", "data": pngData(t, 16, 9)}
	for k, v := range fields {
		body[k] = v
	}
	return body
}

// addPhoto POSTs a photo of recording id; the photo the answer carries.
func (m *meetingFixture) addPhoto(id string, body map[string]any) map[string]any {
	m.t.Helper()
	p, ok := m.do("POST", photosPath(id), "", body, http.StatusOK)["photo"].(map[string]any)
	if !ok {
		m.t.Fatalf("no photo for %v", body["id"])
	}
	return p
}

// photosOf are the photos GET …/photos lists.
func (m *meetingFixture) photosOf(id string) []map[string]any {
	m.t.Helper()
	var out []map[string]any
	for _, p := range m.do("GET", photosPath(id), "", nil, http.StatusOK)["photos"].([]any) {
		out = append(out, p.(map[string]any))
	}
	return out
}

// raw sends a request with full credentials and wants status; the response and its body as they are.
func (m *meetingFixture) raw(method, path string, want int) (*http.Response, []byte) {
	m.t.Helper()
	req, _ := http.NewRequest(method, m.srv.URL+path, nil)
	resp, err := m.srv.Client().Do(req)
	if err != nil {
		m.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		m.t.Fatalf("%s %s: %d %s, want %d", method, path, resp.StatusCode, body, want)
	}
	return resp, body
}

// The photo routes: a photo stored and listed, its image served as it came, deleted once (a repeat answers false), its
// number never given again, and every refusal in the contract's order: 403, 413, 410 and 404 before the body is read,
// 400 for what is wrong with it, then the same id (its photo, or op_id_reused), an id the owner deleted
// (photo_deleted), not_meeting by the recording's kind.
func TestMeetingPhotoRoutes(t *testing.T) {
	m := newMeetingFixture(t)
	p := photosPath(meetingRec)
	one := photoBody(t, photoOne, 61.5, map[string]any{"caption": " Whiteboard ", "text": "Cap: 4 MB per entry"})
	wantCode(t, m.do("POST", p, "answer_only", one, http.StatusForbidden), "forbidden")
	wantCode(t, m.do("GET", p, "extension", nil, http.StatusForbidden), "forbidden")
	wantCode(t, m.do("POST", p, "", strings.Repeat(" ", maxPhotoBody+1), http.StatusRequestEntityTooLarge), "too_large")
	wantCode(t, m.do("POST", p, "", `{"id":`, http.StatusNotFound), codeRecordingNotFound) // the recording before the body
	wantCode(t, m.do("GET", p, "", nil, http.StatusNotFound), codeRecordingNotFound)
	wantCode(t, m.do("GET", p+"/1", "", nil, http.StatusNotFound), codeRecordingNotFound)
	wantCode(t, m.do("DELETE", p+"/1", "", nil, http.StatusNotFound), codeRecordingNotFound)
	m.upload(meetingRec, 1, map[string]any{"title": "Design review"})

	big := base64.StdEncoding.EncodeToString(make([]byte, maxPhotoBytes+1))
	whole, _ := base64.StdEncoding.DecodeString(pngData(t, 16, 9))
	header := base64.StdEncoding.EncodeToString(whole[:33]) // the signature and IHDR, no image data
	for name, body := range map[string]any{
		"not json":         `{"id":`,
		"no id":            photoBody(t, "", 1, nil),
		"bad id":           photoBody(t, "photo-1", 1, nil),
		"no at":            map[string]any{"id": photoOne, "mime": "image/png", "data": pngData(t, 4, 4)},
		"text at":          photoBody(t, photoOne, 1, map[string]any{"at": "1"}),
		"null at":          photoBody(t, photoOne, 1, map[string]any{"at": nil}),
		"gif":              photoBody(t, photoOne, 1, map[string]any{"mime": "image/gif"}),
		"not base64":       photoBody(t, photoOne, 1, map[string]any{"data": "not base64!"}),
		"empty data":       photoBody(t, photoOne, 1, map[string]any{"data": ""}),
		"not an image":     photoBody(t, photoOne, 1, map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("hello"))}),
		"a header only":    photoBody(t, photoOne, 1, map[string]any{"data": header}),
		"another type":     photoBody(t, photoOne, 1, map[string]any{"mime": "image/jpeg"}),
		"over 1 MiB":       photoBody(t, photoOne, 1, map[string]any{"data": big}),
		"over 4096 pixels": photoBody(t, photoOne, 1, map[string]any{"data": pngData(t, 4097, 1)}),
		"long text":        photoBody(t, photoOne, 1, map[string]any{"text": strings.Repeat("字", kb.MaxPhotoText+1)}),
		"long caption":     photoBody(t, photoOne, 1, map[string]any{"caption": strings.Repeat("字", kb.MaxPhotoCaption+1)}),
		"padded text":      photoBody(t, photoOne, 1, map[string]any{"text": strings.Repeat(" ", kb.MaxPhotoText) + "X"}),
		"padded caption":   photoBody(t, photoOne, 1, map[string]any{"caption": strings.Repeat(" ", kb.MaxPhotoCaption) + "X"}),
		"number text":      photoBody(t, photoOne, 1, map[string]any{"text": 3}),
		"object caption":   photoBody(t, photoOne, 1, map[string]any{"caption": map[string]any{}}),
	} {
		if out := m.do("POST", p, "", body, http.StatusBadRequest); out["code"] != "bad_request" {
			t.Errorf("%s: %v", name, out)
		}
	}
	if ps := m.photosOf(meetingRec); len(ps) != 0 {
		t.Fatalf("a refused photo was stored: %v", ps)
	}

	a := m.addPhoto(meetingRec, one)
	if a["n"] != float64(1) || a["id"] != photoOne || a["at"] != 61.5 || a["mime"] != "image/png" || a["width"] != float64(16) || a["height"] != float64(9) ||
		a["caption"] != "Whiteboard" || a["text"] != "Cap: 4 MB per entry" || a["cited"] != false || a["forwarded"] != false || a["added_at"] == nil ||
		a["note"] != nil || a["digest"] != nil || a["queued"] != nil {
		t.Fatalf("photo %v", a)
	}
	// A retry: the stored photo (the id in any case); another body under its id is refused.
	upper := photoBody(t, strings.ToUpper(photoOne), 61.5, map[string]any{"caption": " Whiteboard ", "text": "Cap: 4 MB per entry"})
	if again := m.addPhoto(meetingRec, upper); again["n"] != float64(1) || again["added_at"] != a["added_at"] {
		t.Fatalf("retry %v", again)
	}
	wantCode(t, m.do("POST", p, "", photoBody(t, photoOne, 61.5, map[string]any{"caption": "Whiteboard"}), http.StatusConflict), codeOpIDReused)

	// at clamped into the recording: it started an hour ago and records still.
	two := photoBody(t, photoTwo, 99999, map[string]any{"mime": "image/jpeg", "data": jpegData(t, 40, 30)})
	if b := m.addPhoto(meetingRec, two); b["n"] != float64(2) || b["at"].(float64) < 3590 || b["at"].(float64) > 3700 || b["mime"] != "image/jpeg" {
		t.Fatalf("after now: %v", b)
	}
	// Blank and null words are absent.
	if c := m.addPhoto(meetingRec, photoBody(t, photoThree, -3, map[string]any{"text": "  ", "caption": nil})); c["n"] != float64(3) || c["at"] != float64(0) ||
		c["text"] != nil || c["caption"] != nil {
		t.Fatalf("before the start: %v", c)
	}
	if ps := m.photosOf(meetingRec); len(ps) != 3 || ps[0]["id"] != photoOne || ps[1]["id"] != photoTwo || ps[2]["id"] != photoThree {
		t.Fatalf("list %v", ps)
	}

	// The image, as it came, never sniffed or kept by a cache.
	resp, body := m.raw("GET", p+"/2", http.StatusOK)
	want, _ := base64.StdEncoding.DecodeString(two["data"].(string))
	if !bytes.Equal(body, want) || resp.Header.Get("Content-Type") != "image/jpeg" || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("image: %d bytes, %v", len(body), resp.Header)
	}
	for _, n := range []string{"9", "0", "abc", "-1"} {
		wantCode(t, m.do("GET", p+"/"+n, "", nil, http.StatusNotFound), codePhotoNotFound)
	}

	// Deleted: gone from the list and the images, false when deleted again, never added again under its id; its
	// number is not given again.
	if out := m.do("DELETE", p+"/2", "", nil, http.StatusOK); out["deleted"] != true {
		t.Fatalf("delete %v", out)
	}
	if out := m.do("DELETE", p+"/2", "", nil, http.StatusOK); out["deleted"] != false {
		t.Fatalf("delete again %v", out)
	}
	wantCode(t, m.do("DELETE", p+"/9", "", nil, http.StatusNotFound), codePhotoNotFound)
	wantCode(t, m.do("GET", p+"/2", "", nil, http.StatusNotFound), codePhotoNotFound)
	wantCode(t, m.do("POST", p, "", two, http.StatusGone), codePhotoDeleted)
	if d := m.addPhoto(meetingRec, photoBody(t, photoFour, 5, nil)); d["n"] != float64(4) {
		t.Fatalf("a number given again: %v", d)
	}

	// Not a meeting by its kind (a note asked for minutes is not one here).
	m.upload(meetingOther, 1, map[string]any{"kind": "note"})
	wantCode(t, m.do("POST", photosPath(meetingOther), "", photoBody(t, photoOne, 1, nil), http.StatusConflict), codeNotMeeting)
	if ps := m.photosOf(meetingOther); len(ps) != 0 {
		t.Fatalf("a note's photos %v", ps)
	}

	// The recording deleted: 410 for every route, before the body.
	m.do("DELETE", "/api/v1/recordings/"+meetingRec, "", nil, http.StatusOK)
	wantCode(t, m.do("POST", p, "", `{"id":`, http.StatusGone), codeRecordingDeleted)
	wantCode(t, m.do("GET", p, "", nil, http.StatusGone), codeRecordingDeleted)
	wantCode(t, m.do("GET", p+"/1", "", nil, http.StatusGone), codeRecordingDeleted)
	wantCode(t, m.do("DELETE", p+"/1", "", nil, http.StatusGone), codeRecordingDeleted)
}

// A read-only knowledge base lists and serves the photos, and answers a retry (its photo, op_id_reused, photo_deleted)
// before it refuses a new photo or a deletion with kb_read_only; an unknown photo is 404 first.
func TestMeetingPhotosReadOnly(t *testing.T) {
	m := newMeetingFixture(t)
	p := photosPath(meetingRec)
	m.upload(meetingRec, 1, nil)
	one, two := photoBody(t, photoOne, 1, nil), photoBody(t, photoTwo, 2, nil)
	m.addPhoto(meetingRec, one)
	m.addPhoto(meetingRec, two)
	m.do("DELETE", p+"/2", "", nil, http.StatusOK)
	m.f.mu.Lock()
	m.f.hello.KB = &KBSettings{ReadOnly: true}
	m.f.mu.Unlock()
	if a := m.addPhoto(meetingRec, one); a["n"] != float64(1) {
		t.Fatalf("retry %v", a)
	}
	wantCode(t, m.do("POST", p, "", photoBody(t, photoOne, 9, nil), http.StatusConflict), codeOpIDReused)
	wantCode(t, m.do("POST", p, "", two, http.StatusGone), codePhotoDeleted)
	wantCode(t, m.do("POST", p, "", photoBody(t, photoThree, 3, nil), http.StatusForbidden), "kb_read_only")
	wantCode(t, m.do("POST", photosPath(meetingOther), "", photoBody(t, photoThree, 3, nil), http.StatusNotFound), codeRecordingNotFound)
	if ps := m.photosOf(meetingRec); len(ps) != 1 {
		t.Fatalf("list %v", ps)
	}
	m.raw("GET", p+"/1", http.StatusOK)
	wantCode(t, m.do("DELETE", p+"/9", "", nil, http.StatusNotFound), codePhotoNotFound)
	wantCode(t, m.do("DELETE", p+"/1", "", nil, http.StatusForbidden), "kb_read_only")
	wantCode(t, m.do("DELETE", p+"/2", "", nil, http.StatusForbidden), "kb_read_only")
}

// The knowledge base off: 503 for every route, before anything is read.
func TestMeetingPhotosWithoutKnowledgeBase(t *testing.T) {
	m := newMeetingFixture(t)
	m.f.mu.Lock()
	m.f.hello.KB = &KBSettings{Off: true}
	m.f.mu.Unlock()
	p := photosPath(meetingRec)
	wantCode(t, m.do("POST", p, "", strings.Repeat(" ", maxPhotoBody+1), http.StatusServiceUnavailable), "kb_disabled")
	wantCode(t, m.do("GET", p, "", nil, http.StatusServiceUnavailable), "kb_disabled")
	wantCode(t, m.do("GET", p+"/1", "", nil, http.StatusServiceUnavailable), "kb_disabled")
	wantCode(t, m.do("DELETE", p+"/1", "", nil, http.StatusServiceUnavailable), "kb_disabled")
}

// A meeting keeps 50 photos: the 51st is 413 photos_full, a retry of one it has is still its photo.
func TestMeetingPhotosFull(t *testing.T) {
	m := newMeetingFixture(t)
	m.upload(meetingRec, 1, nil)
	id := func(i int) string { return fmt.Sprintf("c0000000-0000-4000-8000-%012d", i) }
	for i := range kb.MaxPhotos {
		m.addPhoto(meetingRec, photoBody(t, id(i), float64(i), nil))
	}
	wantCode(t, m.do("POST", photosPath(meetingRec), "", photoBody(t, id(kb.MaxPhotos), 1, nil), http.StatusRequestEntityTooLarge), codePhotosFull)
	if a := m.addPhoto(meetingRec, photoBody(t, id(7), 7, nil)); a["n"] != float64(8) {
		t.Fatalf("retry %v", a)
	}
}

// photoRoomListed lists recording id's ready room on the node, idle; its session id.
func (r *roomFixture) photoRoomListed(id string) string {
	r.t.Helper()
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

// photoSends are the photo messages and withdrawals sent once into rooms.
func (r *roomFixture) photoSends() []map[string]any {
	var out []map[string]any
	for _, p := range r.sentOnce() {
		if id, _ := p["client_msg_id"].(string); strings.HasPrefix(id, "photo-") {
			out = append(out, p)
		}
	}
	return out
}

// setRoomRow changes how the node lists the room's session.
func (r *roomFixture) setRoomRow(sid string, fn func(c *proto.Session)) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	for i := range r.f.sessions["server"] {
		if r.f.sessions["server"][i].SessionID == sid {
			fn(&r.f.sessions["server"][i])
		}
	}
}

// A photo taken while the room comes up waits for it, then goes in as a message from the meeting: the image inline,
// its words in a context block and the request for meeting_photo_note, under photo-<id> (sent once), with no
// meeting-turn mark. It is forwarded from then on.
func TestMeetingPhotoIntoTheRoom(t *testing.T) {
	r := newRoomFixture(t)
	r.upload(meetingRec, 1, map[string]any{"room": map[string]any{"attendance": "listener"}, "title": "Design review", "private": false})
	body := photoBody(t, photoOne, 30, map[string]any{"caption": "Whiteboard", "text": "Q3 budget"})
	if p := r.addPhoto(meetingRec, body); p["forwarded"] != false {
		t.Fatalf("photo %v", p)
	}
	r.look(meetingRec) // the room starts
	sid := r.photoRoomListed(meetingRec)
	if len(r.photoSends()) != 0 {
		t.Fatal("sent before the room was ready")
	}
	r.look(meetingRec)
	sent := r.photoSends()
	if len(sent) != 1 {
		t.Fatalf("photo messages %v", sent)
	}
	msg := sent[0]
	text, _ := msg["text"].(string)
	src, _ := msg["message_source"].(map[string]any)
	images, _ := msg["images"].([]any)
	img, _ := images[0].(map[string]any)
	if msg["session_id"] != sid || msg["client_msg_id"] != "photo-"+photoOne || msg["resume_only"] != true || src["kind"] != proto.SourceMeeting ||
		len(images) != 1 || img["mime"] != "image/png" || img["data"] != body["data"] || !strings.Contains(text, `"kind":"meeting"`) ||
		!strings.Contains(text, "Photo 1 was taken in the meeting at 0:30") || !strings.Contains(text, "Caption: Whiteboard") ||
		!strings.Contains(text, "Q3 budget") || !strings.Contains(text, "meeting_photo_note") {
		t.Fatalf("photo message %v", msg)
	}
	if running, _ := r.s.meetingTurnRunning(context.Background(), "server", sid); running {
		t.Fatal("a meeting-turn mark on the room")
	}
	if ps := r.photosOf(meetingRec); ps[0]["forwarded"] != true {
		t.Fatalf("not forwarded %v", ps)
	}
	r.look(meetingRec)
	if len(r.photoSends()) != 1 {
		t.Fatal("sent twice")
	}
}

// An ask goes ahead of a photo, and a photo ahead of a batch; nothing goes while the room's turn runs.
func TestMeetingPhotoOrder(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "listener")
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":10,"end":14,"text":"Ada: the budget is on the board."}]}`, http.StatusOK)
	r.look(meetingRec) // the meeting so far
	batches := len(r.sends())
	r.f.mu.Lock()
	r.f.items = []proto.Item{
		{ID: "u1", Seq: 1, Role: "user", Kind: proto.KindText, Text: "q", ClientMsgID: askMsgID(askOne, 1)},
		{ID: "a1", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "The answer."},
	}
	r.f.mu.Unlock()
	if _, err := r.engine().LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error {
		l.FedAt = l.FedAt.Add(-2 * feedBatchEvery)
		l.Asks = append(l.Asks, kb.LiveAsk{ID: askOne, QuestionSent: "q", Question: "q", QuestionSource: askTyped, State: askPending, Via: kb.AskViaRoom,
			Sources: []kb.AskSource{}, AskedAt: time.Now().UTC(), Attempts: []kb.AskAttempt{{K: 1, MsgID: askMsgID(askOne, 1), SentAt: time.Now().UTC()}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	r.addPhoto(meetingRec, photoBody(t, photoOne, 20, nil))
	r.do("POST", livePath(meetingRec), "", `{"from":1,"segments":[{"start":30,"end":34,"text":"Bo: and the cap stays."}]}`, http.StatusOK)
	r.look(meetingRec)
	if len(r.photoSends()) != 0 || len(r.sends()) != batches {
		t.Fatal("something went ahead of the ask")
	}
	r.idle()
	r.setRoomRow(sid, func(c *proto.Session) { c.Status, c.State = proto.StatusWorking, proto.StateWorking })
	r.look(meetingRec)
	if len(r.photoSends()) != 0 || len(r.sends()) != batches {
		t.Fatal("sent while the room's turn runs")
	}
	r.setRoomRow(sid, func(c *proto.Session) { c.Status, c.State = proto.StatusIdle, proto.StateIdle })
	r.look(meetingRec)
	if len(r.photoSends()) != 1 || len(r.sends()) != batches {
		t.Fatalf("the photo after the ask: %v, %d batches", r.photoSends(), len(r.sends())-batches)
	}
	r.look(meetingRec)
	if sends := r.sends(); len(sends) != batches+1 || !strings.Contains(sends[len(sends)-1]["text"].(string), "the cap stays") {
		t.Fatalf("the batch after the photo: %v", sends[batches:])
	}
}

// A photo added while the role is off, or once the meeting has ended, is stored and never forwarded, also once the
// role is on again.
func TestMeetingPhotoNotForwarded(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	r.upload(meetingRec, 2, map[string]any{"room": map[string]any{"attendance": "off"}})
	r.addPhoto(meetingRec, photoBody(t, photoOne, 5, nil))
	r.upload(meetingRec, 3, map[string]any{"room": map[string]any{"attendance": "listener"}})
	r.look(meetingRec)
	end := time.Now().UTC().Format(time.RFC3339)
	r.upload(meetingRec, 4, map[string]any{"room": map[string]any{"attendance": "listener"}, "ended_at": end, "complete": true})
	r.addPhoto(meetingRec, photoBody(t, photoTwo, 6, nil))
	r.look(meetingRec)
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 0 {
		t.Fatalf("forwarded %v", sent)
	}
	for _, p := range r.photosOf(meetingRec) {
		if p["forwarded"] != false {
			t.Fatalf("photo %v", p)
		}
	}
}

// A photo deleted after it went into the room is withdrawn there while the room is ready, also with the role off: a
// line from the meeting under photo-withdrawn-<id>, sent once. One that never went in is not.
func TestMeetingPhotoWithdrawn(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "listener")
	r.addPhoto(meetingRec, photoBody(t, photoOne, 5, nil))
	r.addPhoto(meetingRec, photoBody(t, photoTwo, 6, nil))
	r.look(meetingRec)
	r.look(meetingRec)
	if len(r.photoSends()) != 2 {
		t.Fatalf("forwarded %v", r.photoSends())
	}
	r.upload(meetingRec, 2, map[string]any{"room": map[string]any{"attendance": "off"}})
	r.addPhoto(meetingRec, photoBody(t, photoThree, 7, nil)) // not forwarded: the role is off
	r.do("DELETE", photosPath(meetingRec)+"/1", "", nil, http.StatusOK)
	r.do("DELETE", photosPath(meetingRec)+"/3", "", nil, http.StatusOK)
	r.look(meetingRec)
	sent := r.photoSends()
	if len(sent) != 3 {
		t.Fatalf("sent %v", sent)
	}
	w := sent[2]
	text, _ := w["text"].(string)
	src, _ := w["message_source"].(map[string]any)
	if w["client_msg_id"] != "photo-withdrawn-"+photoOne || w["session_id"] != sid || w["images"] != nil || src["kind"] != proto.SourceMeeting ||
		!strings.Contains(text, "Photo 1 was withdrawn") || !strings.Contains(text, "Write nothing in reply") {
		t.Fatalf("withdrawal %v", w)
	}
	r.look(meetingRec)
	if len(r.photoSends()) != 3 {
		t.Fatalf("sent again %v", r.photoSends()[3:])
	}
	if _, withdraw := r.engine().PhotosForRoom(meetingRec); len(withdraw) != 0 {
		t.Fatalf("still to withdraw %v", withdraw)
	}
	if ps := r.photosOf(meetingRec); len(ps) != 1 || ps[0]["id"] != photoTwo {
		t.Fatalf("listed %v", ps)
	}
}

// photoNode has the room's node take a photo message or withdrawal (session.send_once) as fn says by its client_msg_id:
// a nil error takes it, an error refuses it, and lost takes it all the same (its answer lost on the way back).
func (r *roomFixture) photoNode(fn func(msgID string) (lost bool, err error)) {
	r.f.answerNode(DeviceSessionSendOnce, func(_ string, params json.RawMessage) (any, error) {
		var a map[string]any
		_ = json.Unmarshal(params, &a)
		msgID, _ := a["client_msg_id"].(string)
		lost, err := fn(msgID)
		if err == nil || lost {
			r.f.sentOnce = append(r.f.sentOnce, a)
		}
		return map[string]any{}, err
	})
}

// photoTries counts the sends of msgID asked of the node, taken or not.
func (r *roomFixture) photoTries(msgID string) int {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	n := 0
	for _, c := range r.f.calls {
		var a map[string]any
		if c.Method == DeviceSessionSendOnce && json.Unmarshal(c.Params, &a) == nil && a["client_msg_id"] == msgID {
			n++
		}
	}
	return n
}

// A photo's message the node keeps failing to take holds the batches for photoSendTries looks, then lets them go, and
// the photos after it; it is never counted as sent: it stays queued, tried again first at every look under its id
// until it goes in. A withdrawal alike: never dropped for failing.
func TestMeetingPhotoSendFailing(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	r.do("POST", livePath(meetingRec), "", `{"from":0,"segments":[{"start":10,"end":14,"text":"Ada: the budget is on the board."}]}`, http.StatusOK)
	r.look(meetingRec) // the meeting so far
	batches := len(r.sends())
	r.addPhoto(meetingRec, photoBody(t, photoOne, 20, nil))
	r.addPhoto(meetingRec, photoBody(t, photoTwo, 21, nil))
	r.do("POST", livePath(meetingRec), "", `{"from":1,"segments":[{"start":30,"end":34,"text":"Bo: and the cap stays."}]}`, http.StatusOK)
	e := r.engine()
	if _, err := e.LiveUpdate(meetingRec, func(l *kb.Live, _ kb.Recording) error { l.FedAt = l.FedAt.Add(-2 * feedBatchEvery); return nil }); err != nil {
		t.Fatal(err)
	}
	one, two := "photo-"+photoOne, "photo-"+photoTwo
	r.photoNode(func(msgID string) (bool, error) {
		if msgID == one {
			return false, errMeetingTest
		}
		return false, nil
	})
	for i := 1; i < photoSendTries; i++ {
		r.look(meetingRec)
		if r.photoTries(one) != i || r.photoTries(two) != 0 || len(r.sends()) != batches {
			t.Fatalf("look %d: %d tries, photo 2 %d, %d batches", i, r.photoTries(one), r.photoTries(two), len(r.sends())-batches)
		}
	}
	// The third failure gives it up for now: nothing else is sent into the room in that look (the failed send may
	// have started a turn), and the batch may go (it checks that the room is free).
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 0 || r.photoTries(one) != photoSendTries || len(r.sends()) != batches+1 {
		t.Fatalf("after %d tries: %v, %d batches", photoSendTries, sent, len(r.sends())-batches)
	}
	r.look(meetingRec) // the next photo goes ahead of the one given up on
	if sent := r.photoSends(); len(sent) != 1 || sent[0]["client_msg_id"] != two || r.photoTries(one) != photoSendTries {
		t.Fatalf("then: %v, %d tries", sent, r.photoTries(one))
	}
	r.look(meetingRec) // tried again, alone in its look
	if r.photoTries(one) != photoSendTries+1 || len(r.photoSends()) != 1 {
		t.Fatalf("again: %d tries, %v", r.photoTries(one), r.photoSends())
	}
	if queued, _ := e.PhotosForRoom(meetingRec); len(queued) != 1 || queued[0].N != 1 {
		t.Fatalf("counted as sent: queued %+v", queued)
	}
	r.photoNode(func(string) (bool, error) { return false, nil })
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 2 || sent[1]["client_msg_id"] != one {
		t.Fatalf("once the node takes it: %v", sent)
	}
	if queued, _ := e.PhotosForRoom(meetingRec); len(queued) != 0 {
		t.Fatalf("queued %+v", queued)
	}

	// Its withdrawal failing as often stays to send, and goes once the node takes it.
	r.do("DELETE", photosPath(meetingRec)+"/1", "", nil, http.StatusOK)
	r.photoNode(func(string) (bool, error) { return false, errMeetingTest })
	for range photoSendTries + 1 {
		r.look(meetingRec)
	}
	if _, withdraw := e.PhotosForRoom(meetingRec); len(withdraw) != 1 || withdraw[0].N != 1 || r.photoTries("photo-withdrawn-"+photoOne) != photoSendTries+1 {
		t.Fatalf("withdrawal dropped: %+v after %d tries", withdraw, r.photoTries("photo-withdrawn-"+photoOne))
	}
	r.photoNode(func(string) (bool, error) { return false, nil })
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 3 || sent[2]["client_msg_id"] != "photo-withdrawn-"+photoOne {
		t.Fatalf("withdrawal %v", sent)
	}
	if _, withdraw := e.PhotosForRoom(meetingRec); len(withdraw) != 0 {
		t.Fatalf("still to withdraw %+v", withdraw)
	}
}

// A photo whose send went in but whose answer was lost (or a restart cut it off) is forwarded from before the send:
// the room may note it, a delete withdraws it there, and while it is listed it is sent again under its id (the hub's
// guard answers delivered) until a send succeeds.
func TestMeetingPhotoSendInDoubt(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	r.addPhoto(meetingRec, photoBody(t, photoOne, 5, nil))
	r.addPhoto(meetingRec, photoBody(t, photoTwo, 6, nil))
	lost := func(string) (bool, error) { return true, context.DeadlineExceeded }
	taken := func(string) (bool, error) { return false, nil }
	r.photoNode(lost)
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 1 || sent[0]["client_msg_id"] != "photo-"+photoOne {
		t.Fatalf("sent %v", sent)
	}
	if ps := r.photosOf(meetingRec); ps[0]["forwarded"] != true || ps[1]["forwarded"] != false {
		t.Fatalf("listed %v", ps)
	}
	e := r.engine()
	if queued, _ := e.PhotosForRoom(meetingRec); len(queued) != 2 {
		t.Fatalf("queued %+v", queued)
	}
	if out := photoNoteTool(e, meetingRec, json.RawMessage(`{"photo":1,"text":"The Q3 budget"}`)); out.IsError {
		t.Fatalf("a note on a photo in doubt: %+v", out)
	}
	r.photoNode(taken)
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 2 || sent[1]["client_msg_id"] != "photo-"+photoOne {
		t.Fatalf("sent again %v", sent)
	}
	if queued, _ := e.PhotosForRoom(meetingRec); len(queued) != 1 || queued[0].N != 2 {
		t.Fatalf("queued %+v", queued)
	}

	// Photo 2's answer lost too, then deleted: withdrawn there, not sent again.
	r.photoNode(lost)
	r.look(meetingRec)
	r.do("DELETE", photosPath(meetingRec)+"/2", "", nil, http.StatusOK)
	if _, withdraw := e.PhotosForRoom(meetingRec); len(withdraw) != 1 || withdraw[0].N != 2 {
		t.Fatalf("withdraw %+v", withdraw)
	}
	r.photoNode(taken)
	r.look(meetingRec)
	r.look(meetingRec)
	if sent := r.photoSends(); len(sent) != 4 || sent[2]["client_msg_id"] != "photo-"+photoTwo || sent[3]["client_msg_id"] != "photo-withdrawn-"+photoTwo {
		t.Fatalf("sent %v", sent)
	}
}

// meeting_photo_note: the room's note on a photo of its own meeting that went into it, within 1000 characters;
// refused for an unknown photo, one not forwarded, one withdrawn, another meeting's, and by a room started before the
// tool existed.
func TestMeetingPhotoNoteTool(t *testing.T) {
	r := newRoomFixture(t)
	sid := r.roomUp(meetingRec, "listener")
	other := r.roomUp(meetingOther, "listener")
	r.addPhoto(meetingRec, photoBody(t, photoOne, 5, nil))
	for _, id := range []string{photoOne, photoTwo, photoThree} {
		r.addPhoto(meetingOther, photoBody(t, id, 5, nil))
	}
	r.look(meetingRec)
	for range 3 {
		r.look(meetingOther)
	}
	r.addPhoto(meetingRec, photoBody(t, photoTwo, 6, nil)) // queued, not in the room yet
	d := newFleetDevs()
	d.add("server", "Server")
	r.s.devs = d
	call := func(session, args string) proto.ToolResult {
		t.Helper()
		out, ok, err := r.s.NodeRequest(context.Background(), "server", "mcp.call",
			json.RawMessage(`{"name":"meeting_photo_note","args":`+args+`,"session_id":"`+session+`","assistant":true,"guest":true}`))
		if !ok || err != nil {
			t.Fatalf("call: %v %v", ok, err)
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
	rev := r.engine().PhotosRev(meetingRec)
	if res := call(sid, `{"photo":1,"text":"  The Q3 budget on the board is 40k.  "}`); res.IsError {
		t.Fatalf("note: %s", text(res))
	}
	if ps := r.photosOf(meetingRec); ps[0]["note"] != "The Q3 budget on the board is 40k." || r.engine().PhotosRev(meetingRec) != rev+1 {
		t.Fatalf("noted %v", ps[0])
	}
	for name, c := range map[string]struct{ args, want string }{
		"too long":      {`{"photo":1,"text":"` + strings.Repeat("字", kb.MaxPhotoNote+1) + `"}`, "1000"},
		"empty":         {`{"photo":1,"text":"  "}`, "empty"},
		"no text":       {`{"photo":1}`, "required"},
		"not a number":  {`{"photo":"one","text":"x"}`, "number"},
		"unknown":       {`{"photo":9,"text":"x"}`, "no photo 9"},
		"not forwarded": {`{"photo":2,"text":"x"}`, "no photo 2"},
		"another's":     {`{"photo":3,"text":"x"}`, "no photo 3"}, // the other meeting's photo 3 went into its own room
	} {
		if res := call(sid, c.args); !res.IsError || !strings.Contains(text(res), c.want) {
			t.Errorf("%s: %v %s", name, res.IsError, text(res))
		}
	}
	if ps := r.photosOf(meetingOther); ps[2]["note"] != nil {
		t.Fatalf("another meeting's photo noted %v", ps[2])
	}
	r.do("DELETE", photosPath(meetingRec)+"/1", "", nil, http.StatusOK)
	if res := call(sid, `{"photo":1,"text":"again"}`); !res.IsError || !strings.Contains(text(res), "withdrawn") {
		t.Fatalf("withdrawn: %s", text(res))
	}

	// A room started before the tool existed keeps the tools it started with: not listed, refused.
	if err := r.s.guestPut(context.Background(), other, &guestConv{MachineID: "server", Tools: []string{"meeting_lines", "meeting_earlier"},
		Kind: guestKindMeeting, Recording: meetingOther}); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range r.s.sessionMCPTools(context.Background(), "server", other, true, true) {
		names = append(names, tl.Name)
	}
	if !slices.Equal(names, []string{"meeting_lines", "meeting_earlier"}) {
		t.Fatalf("an older room's tools %v", names)
	}
	if res := call(other, `{"photo":1,"text":"x"}`); !res.IsError || !strings.Contains(text(res), "unknown tool") {
		t.Fatalf("an older room noted: %s", text(res))
	}
}

// GET …/live has the photos' revision (none before the first photo) and ?parts=photos only that; detail.meta has
// neither the photos nor their revision; the meeting_live event says photos changed with their revision.
func TestMeetingPhotosLive(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	if revs := r.do("GET", livePath(meetingRec), "", nil, http.StatusOK)["revs"].(map[string]any); revs["photos"] != nil {
		t.Fatalf("a revision before any photo: %v", revs)
	}
	r.addPhoto(meetingRec, photoBody(t, photoOne, 5, nil))
	view := r.do("GET", livePath(meetingRec), "", nil, http.StatusOK)
	if view["revs"].(map[string]any)["photos"] != float64(1) || view["photos"] != nil {
		t.Fatalf("view %v", view)
	}
	only := r.do("GET", livePath(meetingRec)+"?parts=photos", "", nil, http.StatusOK)
	if raw, _ := json.Marshal(only); string(raw) != `{"revs":{"photos":1}}` {
		t.Fatalf("parts=photos %s", raw)
	}
	r.do("DELETE", photosPath(meetingRec)+"/1", "", nil, http.StatusOK)
	if got := r.do("GET", livePath(meetingRec)+"?parts=photos,room", "", nil, http.StatusOK); got["revs"].(map[string]any)["photos"] != float64(2) || got["room"] == nil {
		t.Fatalf("after the delete %v", got)
	}
	waitFor(t, "a meeting_live event for the photos", func() bool {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		for _, ev := range r.f.liveEvents {
			changed, _ := ev["changed"].([]any)
			revs, _ := ev["revs"].(map[string]any)
			if slices.Contains(changed, any("photos")) && revs["photos"] != nil && ev["recording_id"] == meetingRec {
				return true
			}
		}
		return false
	})
	d, err := r.engine().Get(kb.KindRecording, meetingRec, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(d.Meta), "photo") {
		t.Fatalf("detail.meta %s", d.Meta)
	}
}

// The follow-up's seed has the words of the photos still listed (after the minutes) and the room's notes on them
// (with the room thread), never a deleted photo's or an image; a trimmed recording seeds its minutes only.
func TestMeetingPhotosInTheFollowup(t *testing.T) {
	r := newRoomFixture(t)
	r.roomUp(meetingRec, "listener")
	body := photoBody(t, photoOne, 65, map[string]any{"caption": "Whiteboard", "text": "PHOTO-ONE-TEXT"})
	r.addPhoto(meetingRec, body)
	r.addPhoto(meetingRec, photoBody(t, photoTwo, 70, map[string]any{"text": "PHOTO-TWO-TEXT"}))
	r.look(meetingRec)
	r.look(meetingRec)
	e := r.engine()
	if err := e.PhotoNote(meetingRec, 1, "PHOTO-ONE-NOTE"); err != nil {
		t.Fatal(err)
	}
	if err := e.PhotoNote(meetingRec, 2, "PHOTO-TWO-NOTE"); err != nil {
		t.Fatal(err)
	}
	r.do("DELETE", photosPath(meetingRec)+"/2", "", nil, http.StatusOK)
	end := time.Now().UTC().Format(time.RFC3339)
	r.upload(meetingRec, 2, map[string]any{"room": map[string]any{"attendance": "listener"}, "ended_at": end, "complete": true, "title": "Design review",
		"segments": []map[string]any{{"start": 0, "end": 9, "text": "We keep the cap."}}})
	if _, err := e.MeetingUpdate(meetingRec, func(m *kb.Meeting, _ kb.Recording) error { m.Room.State = kb.RoomEnded; return nil }); err != nil {
		t.Fatal(err)
	}
	r.do("POST", "/api/v1/recordings/"+meetingRec+"/followup", "", map[string]any{"id": callOne, "text": "Draft the email."}, http.StatusOK)
	sends := r.sends()
	seed, _ := sends[len(sends)-1]["text"].(string)
	photos, notes, transcript := strings.Index(seed, "PHOTO-ONE-TEXT"), strings.Index(seed, "PHOTO-ONE-NOTE"), strings.Index(seed, "We keep the cap.")
	if photos < 0 || notes < photos || transcript < notes || !strings.Contains(seed, "Photo 1 (1:05): Whiteboard") ||
		strings.Contains(seed, "PHOTO-TWO") || strings.Contains(seed, body["data"].(string)) {
		t.Fatalf("seed:\n%s", seed)
	}

	// Trimmed: the minutes only, though they cite a photo that stays.
	r.minutesFor(meetingRec, `{"title":"Design review","decisions":[{"text":"Keep the budget","photo":1}]}`)
	if ps := r.photosOf(meetingRec); len(ps) != 1 || ps[0]["cited"] != true {
		t.Fatalf("cited %v", ps)
	}
	e.PruneRecordings(time.Now().AddDate(0, 0, 30))
	m, rec, err := e.MeetingRead(meetingRec)
	if err != nil || !rec.Trimmed() {
		t.Fatalf("not trimmed: %v", err)
	}
	if ps := r.photosOf(meetingRec); len(ps) != 1 {
		t.Fatalf("the cited photo went: %v", ps)
	}
	wantCode(t, r.do("POST", photosPath(meetingRec), "", photoBody(t, photoThree, 1, nil), http.StatusConflict), codeTranscriptExpired)
	if seed := r.s.followupSeed(context.Background(), e, rec, m, "x"); strings.Contains(seed, "PHOTO-ONE") || !strings.Contains(seed, "Keep the budget") {
		t.Fatalf("trimmed seed:\n%s", seed)
	}
}

// meeting.answer reads the photos' words as this meeting's material, never a deleted photo's or an image.
func TestMeetingAnswerReadsPhotos(t *testing.T) {
	m := newMeetingFixture(t)
	m.upload(meetingRec, 1, map[string]any{"attendance": "participant", "title": "Design review"})
	body := photoBody(t, photoOne, 70, map[string]any{"caption": "Whiteboard", "text": "Cap: 4 MB\nper entry"})
	m.addPhoto(meetingRec, body)
	m.addPhoto(meetingRec, photoBody(t, photoTwo, 80, map[string]any{"text": "DELETED-PHOTO-TEXT"}))
	m.do("DELETE", photosPath(meetingRec)+"/2", "", nil, http.StatusOK)
	meetingAskOf(t, m.do("POST", askPath(meetingRec), "", map[string]any{"id": askOne, "question": "What is the cap?"}, http.StatusOK))
	calls := m.model.called()
	if len(calls) != 1 {
		t.Fatalf("%d calls", len(calls))
	}
	in := calls[0].User
	if !strings.Contains(in, "[1:10] photo 1 · caption: Whiteboard · text: Cap: 4 MB per entry") || strings.Contains(in, "DELETED-PHOTO") ||
		strings.Contains(in, body["data"].(string)) {
		t.Fatalf("input:\n%s", in)
	}
}

// A follow-up made right after a photo's delete was answered has none of its words, though the minutes were not made
// again yet: the delete took the items citing it out of them. kb_get has none either.
func TestMeetingPhotoDeletedBeforeTheFollowup(t *testing.T) {
	m := newMeetingFixture(t)
	m.upload(meetingRec, 1, map[string]any{"title": "Design review", "complete": true, "ended_at": time.Now().UTC().Format(time.RFC3339),
		"segments": []map[string]any{{"start": 0, "end": 9, "text": "We keep the cap."}}})
	m.addPhoto(meetingRec, photoBody(t, photoOne, 5, map[string]any{"text": "PHOTO-ONLY-WORDS"}))
	m.minutesFor(meetingRec, `{"title":"Design review","points":[{"text":"PHOTO-ONLY-WORDS","photo":1},{"text":"We keep the cap","line":1}]}`)
	m.do("DELETE", photosPath(meetingRec)+"/1", "", nil, http.StatusOK)
	e := m.engine()
	meeting, rec, err := e.MeetingRead(meetingRec)
	if err != nil {
		t.Fatal(err)
	}
	if seed := m.s.followupSeed(context.Background(), e, rec, meeting, "Draft a summary."); strings.Contains(seed, "PHOTO-ONLY") ||
		!strings.Contains(seed, "We keep the cap") {
		t.Fatalf("seed:\n%s", seed)
	}
	if out, _ := runTool(m.s, "c1", "kb_get", map[string]any{"kind": "recordings", "id": meetingRec}); strings.Contains(out, "PHOTO-ONLY") {
		t.Fatalf("kb_get:\n%s", out)
	}
}

// kb_get prints a recording's photos (photos.md) after the minutes' action items and before the transcript; a deleted
// photo is not there.
func TestMeetingPhotosInKBGet(t *testing.T) {
	m := newMeetingFixture(t)
	m.upload(meetingRec, 1, map[string]any{"title": "Design review", "complete": true, "ended_at": time.Now().UTC().Format(time.RFC3339),
		"segments": []map[string]any{{"start": 0, "end": 9, "text": "Bo adds the test."}}})
	m.addPhoto(meetingRec, photoBody(t, photoOne, 5, map[string]any{"caption": "Whiteboard", "text": "Q3 budget"}))
	m.addPhoto(meetingRec, photoBody(t, photoTwo, 6, map[string]any{"text": "DELETED-PHOTO-TEXT"}))
	m.do("DELETE", photosPath(meetingRec)+"/2", "", nil, http.StatusOK)
	m.minutesFor(meetingRec, `{"title":"Design review","actions":[{"who":"Bo","what":"Add the test","line":1}]}`)
	out, err := runTool(m.s, "c1", "kb_get", map[string]any{"kind": "recordings", "id": meetingRec})
	if err != nil {
		t.Fatal(err)
	}
	actions, photos, transcript := strings.Index(out, "Action items as they stand"), strings.Index(out, "\nphotos.md:\n[P1] "), strings.Index(out, "\ntranscript.md:")
	if actions < 0 || photos < actions || transcript < photos || !strings.Contains(out, "photo: Whiteboard · Q3 budget\n") || strings.Contains(out, "DELETED-PHOTO") {
		t.Fatalf("kb_get:\n%s", out)
	}
	m.do("DELETE", photosPath(meetingRec)+"/1", "", nil, http.StatusOK)
	if out, _ := runTool(m.s, "c1", "kb_get", map[string]any{"kind": "recordings", "id": meetingRec}); strings.Contains(out, "photos.md") {
		t.Fatalf("kb_get without photos:\n%s", out)
	}
}
