package assistant

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // image.DecodeConfig and image.Decode read the photos
	_ "image/png"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Meeting photos (docs/assistant/meeting-minutes.md "Phase 3: photos"): the phone adds the photos taken during a
// meeting (POST …/photos), the apps list them, read each image and delete one; the knowledge base keeps them with the
// recording (kb/recording_photos.go). A photo added while the room attends goes into it (roomPhotos, from the feeder):
// the image with its words and a request for meeting_photo_note, whose note the room's tool call stores; one deleted
// after it went in is withdrawn there with a line. Full credentials only, as the other meeting routes, with their
// errors in the contract's order. The hub's models read the photos' words only, never an image.

// The limits of a photo the phone adds (the knowledge base's are its text's, kb.MaxPhotoText and kb.MaxPhotoCaption).
const (
	maxPhotoBody  = 2 << 20 // a POST …/photos body
	maxPhotoBytes = 1 << 20 // its image, decoded
	maxPhotoSide  = 4096    // pixels on the image's long side
)

// Codes of the photo routes' refusals (the contract's), beside the meeting routes'.
const (
	codePhotoNotFound = "photo_not_found"
	codePhotoDeleted  = "photo_deleted"
	codePhotosFull    = "photos_full"
)

// photoSendTries is how many times a photo message (or withdrawal) to a room that looked free fails before the batches
// stop waiting for it. Never counted as sent: the photo stays queued and the withdrawal pending, tried again at every
// look under the same id (the hub's once-only guard never sends it twice, and answers a send left in doubt as such).
const photoSendTries = 3

func (s *Service) meetingPhotoRoutes() {
	s.handle("POST /api/v1/recordings/{id}/photos", s.photoAddRoute)
	s.handle("GET /api/v1/recordings/{id}/photos", func(w http.ResponseWriter, r *http.Request) {
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r) // a read-only knowledge base answers it too
		if e == nil {
			return
		}
		photos, _, err := e.Photos(r.PathValue("id"))
		if err != nil {
			writeMeetingError(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"photos": photos})
	})
	s.handle("GET /api/v1/recordings/{id}/photos/{n}", func(w http.ResponseWriter, r *http.Request) {
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r)
		if e == nil {
			return
		}
		mime, data, err := e.PhotoImage(r.PathValue("id"), photoNumber(r.PathValue("n")))
		if err != nil {
			writeMeetingError(w, err)
			return
		}
		h := w.Header()
		h.Set("Content-Type", mime)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, no-store")
		_, _ = w.Write(data)
	})
	s.handle("DELETE /api/v1/recordings/{id}/photos/{n}", func(w http.ResponseWriter, r *http.Request) {
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r)
		if e == nil {
			return
		}
		id := r.PathValue("id")
		// The knowledge base checks the recording and the photo before it refuses a read-only store, as the contract
		// orders them.
		deleted, err := e.PhotoDelete(id, photoNumber(r.PathValue("n")))
		if err != nil {
			writeMeetingError(w, err)
			return
		}
		if deleted {
			s.meetingKick(id) // a photo that went into the room is withdrawn there
			s.kbWake()        // minutes already written are made again
		}
		apierr.WriteJSON(w, map[string]any{"deleted": deleted})
	})
}

// photoAddRoute is POST /api/v1/recordings/{id}/photos.
func (s *Service) photoAddRoute(w http.ResponseWriter, r *http.Request) {
	if !meetingCaller(w, r) {
		return
	}
	e := s.meetingEngine(w, r)
	if e == nil {
		return
	}
	raw, ok := meetingBody(w, r, maxPhotoBody, "2 MiB")
	if !ok {
		return
	}
	id := r.PathValue("id")
	l, rec, ok := meetingRecording(w, e, id) // 410 and 404 before the body is read as a photo
	if !ok {
		return
	}
	u, err := parsePhoto(raw)
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	// The knowledge base answers a retry (its photo, or 409 op_id_reused) and an id the owner deleted before it refuses
	// a read-only store, so a retry is answered on one too; then not_meeting, transcript_expired and photos_full. The
	// engine is read-only whenever the settings say so (kbGet opens it again when they change).
	p, created, err := e.PhotoAdd(id, u, photoQueued(rec, l, time.Now()))
	if err != nil {
		writeMeetingError(w, err)
		return
	}
	if created {
		s.meetingKick(rec.ID) // into the room, once it is ready
		s.kbWake()            // minutes already written are made again
	}
	apierr.WriteJSON(w, map[string]any{"photo": p})
}

// photoNumber is a photo's number in a path; 0 (no photo has it) for anything but a whole number from 1.
func photoNumber(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// parsePhoto reads a POST …/photos body, 400 for what is wrong with it: id a UUID (lowercased), at a number, mime
// image/jpeg or image/png, data the base64 of a whole image of that type of at most maxPhotoBytes and maxPhotoSide
// pixels on its long side, text and caption strings within their limits as sent (null or blank ones absent). Its digest
// covers what makes the photo, so a retry with the same body is the same photo and another body under its id is not.
func parsePhoto(raw []byte) (kb.PhotoUpload, error) {
	var in struct {
		ID      json.RawMessage `json:"id"`
		At      json.RawMessage `json:"at"`
		Mime    json.RawMessage `json:"mime"`
		Data    json.RawMessage `json:"data"`
		Text    json.RawMessage `json:"text"`
		Caption json.RawMessage `json:"caption"`
	}
	var u kb.PhotoUpload
	if err := json.Unmarshal(raw, &in); err != nil {
		return u, i18n.Err("assistant-meeting.bad_json", i18n.Args{"error": err.Error()})
	}
	var id string
	if json.Unmarshal(in.ID, &id) != nil {
		return u, i18n.Err("assistant-meeting.bad_ask_id")
	}
	var ok bool
	if u.ID, ok = kb.RecordingID(id); !ok {
		return u, i18n.Err("assistant-meeting.bad_ask_id")
	}
	if !given(in.At) || json.Unmarshal(in.At, &u.At) != nil {
		return u, i18n.Err("assistant-meeting.bad_photo_at")
	}
	if json.Unmarshal(in.Mime, &u.Mime) != nil || u.Mime != kb.PhotoJPEG && u.Mime != kb.PhotoPNG {
		return u, i18n.Err("assistant-meeting.bad_photo_mime")
	}
	var data string
	if json.Unmarshal(in.Data, &data) != nil {
		return u, i18n.Err("assistant-meeting.bad_photo_data")
	}
	img, err := base64.StdEncoding.DecodeString(data)
	switch {
	case err != nil || len(img) == 0:
		return u, i18n.Err("assistant-meeting.bad_photo_data")
	case len(img) > maxPhotoBytes:
		return u, i18n.Err("assistant-meeting.photo_too_large", i18n.Args{"size": "1 MiB"})
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil || "image/"+format != u.Mime || cfg.Width <= 0 || cfg.Height <= 0 {
		return u, i18n.Err("assistant-meeting.bad_photo_data")
	}
	if max(cfg.Width, cfg.Height) > maxPhotoSide {
		return u, i18n.Err("assistant-meeting.photo_too_wide", i18n.Args{"max": maxPhotoSide})
	}
	// The whole image, once its size is known to be within bounds: a header with nothing after it is not one.
	if _, _, err := image.Decode(bytes.NewReader(img)); err != nil {
		return u, i18n.Err("assistant-meeting.bad_photo_data")
	}
	words := map[string]string{}
	for _, f := range []struct {
		name string
		raw  json.RawMessage
		max  int
		to   *string
	}{{"text", in.Text, kb.MaxPhotoText, &u.Text}, {"caption", in.Caption, kb.MaxPhotoCaption, &u.Caption}} {
		var v string
		if given(f.raw) && json.Unmarshal(f.raw, &v) != nil {
			return u, i18n.Err("assistant-meeting.bad_ask_field", i18n.Args{"field": f.name})
		}
		// The limit is the string's as sent, blanks around it included; stored trimmed.
		if utf8.RuneCountInString(v) > f.max {
			return u, i18n.Err("assistant-meeting.photo_field_too_long", i18n.Args{"field": f.name, "max": f.max})
		}
		words[f.name], *f.to = v, strings.TrimSpace(v)
	}
	u.Data, u.Width, u.Height = img, cfg.Width, cfg.Height
	canon, err := json.Marshal([]any{u.At, u.Mime, data, words["text"], words["caption"]})
	if err != nil {
		return u, err
	}
	sum := sha256.Sum256(canon)
	u.Digest = hex.EncodeToString(sum[:])
	return u, nil
}

// photoQueued: a photo added now to recording rec is to go into its room (once the room is ready): a room meeting
// whose role is not off, whose room has not ended or been deleted, before the meeting's end. Any other photo is stored
// and never forwarded.
func photoQueued(rec kb.Recording, l kb.Live, now time.Time) bool {
	if !rec.RoomMeeting() || !rec.Attending() {
		return false
	}
	switch rec.Conv.Room.State {
	case kb.RoomEnded, kb.RoomDeleted:
		return false
	}
	_, ended := meetingEnd(rec, l, now)
	return !ended
}

// roomPhotos gives recording id's ready room what it is to get of the photos, one message a look: first a photo the
// owner deleted after it went in (withdrawn, also while the role is off), else, with forward (the room attends and the
// meeting has not ended), the oldest photo queued for it: the image, its words and the request for
// meeting_photo_note. An ask goes first (the caller's gate, and the room's one-ask slot held while it sends), and the
// batches wait while the room's turn for it runs (the busy check). A message whose sends failed photoSendTries times
// holds neither the batch nor the messages after it: it is tried again first at every look, then the next one goes.
// When to look again; false when nothing was sent and the batch may go.
func (s *Service) roomPhotos(ctx context.Context, e *kb.Engine, id string, r kb.Recording, now time.Time, forward bool) (time.Time, bool) {
	queued, withdraw := e.PhotosForRoom(id)
	if !forward {
		queued = nil
	}
	if len(withdraw) == 0 && len(queued) == 0 {
		return time.Time{}, false
	}
	room := r.Conv.Room
	target, verdict := s.attendeeCheckFor(ctx, kb.SessionRef{MachineID: room.MachineID, SessionID: room.SessionID}, now, id)
	switch verdict {
	case "":
	case attendeeBusy:
		return now.Add(feedBusyPoll), true
	default:
		return now.Add(feedRetry), true // its computer away: the photos wait for it
	}
	release, _, ok := s.room.take(id)
	if !ok {
		return now.Add(feedBusyPoll), true // an ask is on its way into the room
	}
	defer release()
	loc := s.location(ctx)
	src := &proto.MessageSource{Kind: proto.SourceMeeting, Title: feedMeetingName(r, loc)}
	// One message a look, whatever comes of it: a send whose answer is lost may have started a turn, and the next
	// message waits for the room to be free again (the busy check above, at the next look). The withdrawals first, then
	// the photos, oldest first; a message given up on for now (failed photoSendTries times) after the others, the one
	// that failed least first, so it holds up neither them nor, once given up on, the batches.
	type roomMsg struct {
		msgID string
		w     *kb.PhotoRef
		p     *kb.Photo
	}
	var msgs []roomMsg
	for i := range withdraw {
		msgs = append(msgs, roomMsg{msgID: "photo-withdrawn-" + withdraw[i].ID, w: &withdraw[i]})
	}
	for i := range queued {
		msgs = append(msgs, roomMsg{msgID: "photo-" + queued[i].ID, p: &queued[i]})
	}
	tries := func(m roomMsg) int {
		if n := s.room.photoTriesOf(m.msgID); n >= photoSendTries {
			return n
		}
		return 0
	}
	slices.SortStableFunc(msgs, func(a, b roomMsg) int { return cmp.Compare(tries(a), tries(b)) })
	for _, m := range msgs {
		var text string
		var images []proto.Image
		if m.w != nil {
			text = prompts.RenderMeetingRoomPhotoWithdrawn(prompts.MeetingRoomPhotoWithdrawn{N: m.w.N})
		} else {
			p := m.p
			mime, data, err := e.PhotoImage(id, p.N)
			if err == nil {
				// Forwarded before it goes: a send whose answer is lost leaves a photo the room may have, which a delete
				// withdraws there and the room may note.
				err = e.PhotoSending(id, p.N)
			}
			switch {
			case errors.Is(err, kb.ErrPhotoNotFound):
				continue // deleted since: it never went in, nothing was sent
			case err != nil:
				s.logf("meeting %s: photo %d: %v", short(id), p.N, err)
				return now.Add(feedRetry), true
			}
			text = prompts.RenderMeetingRoomPhoto(prompts.MeetingRoomPhoto{N: p.N, At: meetingClock(p.At), Caption: oneLine(p.Caption), Text: p.Text})
			images = []proto.Image{{Mime: mime, Data: base64.StdEncoding.EncodeToString(data)}}
		}
		sent, n := s.roomSendPhoto(ctx, target.ref, m.msgID, text, src, images)
		switch {
		case sent && m.w != nil:
			if err := e.PhotoWithdrawn(id, m.w.N); err != nil {
				s.logf("meeting %s: photo %d withdrawn: %v", short(id), m.w.N, err)
			}
		case sent:
			if err := e.PhotoForwarded(id, m.p.N); err != nil {
				s.logf("meeting %s: photo %d forwarded: %v", short(id), m.p.N, err)
			}
		case n < photoSendTries:
			return now.Add(feedRetry), true // the batch waits for it
		default:
			return now.Add(feedRetry), false // given up on for now: the batch may go (it checks the room is free)
		}
		return now.Add(feedBusyPoll), true
	}
	return time.Time{}, false
}

// roomSendPhoto sends a photo's message (images beside text) or a withdrawal into the room under msgID, sent once (the
// hub's guard): message source meeting, kept in the text as the asks' are, and no meeting-turn mark (every turn of the
// room is a side exchange). sent once it went (the guard answers one that went before so); else how many times msgID
// failed so far (photoFailed).
func (s *Service) roomSendPhoto(ctx context.Context, ref kb.SessionRef, msgID, text string, src *proto.MessageSource, images []proto.Image) (sent bool, tries int) {
	p := map[string]any{"session_id": ref.SessionID, "text": prompts.MessageSourceText(text, src), "message_source": src,
		"client_msg_id": msgID, "resume_only": true}
	if len(images) > 0 {
		p["images"] = images
	}
	sctx, cancel := context.WithTimeout(ctx, feedSendWait)
	defer cancel()
	err := s.sendOnce(sctx, ref.MachineID, p)
	if err == nil {
		s.room.photoSent(msgID)
		return true, 0
	}
	tries = s.room.photoFailed(msgID)
	s.logf("meeting room %s: %s not sent (%d times): %v", short(ref.SessionID), msgID, tries, err)
	return false, tries
}

// photoTriesOf is how many sends of msgID failed so far.
func (r *roomState) photoTriesOf(msgID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.photoTries[msgID]
}

// photoFailed counts a failed send of msgID; how many there were.
func (r *roomState) photoFailed(msgID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.photoTries == nil {
		r.photoTries = map[string]int{}
	}
	r.photoTries[msgID]++
	return r.photoTries[msgID]
}

// photoSent forgets msgID's failed sends.
func (r *roomState) photoSent(msgID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.photoTries, msgID)
}

// photoNoteTool is the room's meeting_photo_note {photo, text} for recording, its own meeting: the room's note on a
// photo that went into it, 1 to kb.MaxPhotoNote characters. Refused with what is wrong: a number that is no photo of
// this meeting, one that never went in or was withdrawn, a text out of bounds.
func photoNoteTool(e *kb.Engine, recording string, args json.RawMessage) proto.ToolResult {
	var a struct {
		Photo json.RawMessage `json:"photo"`
		Text  *string         `json:"text"`
	}
	if json.Unmarshal(args, &a) != nil || a.Text == nil {
		return proto.TextResult("photo (the photo's number) and text (the note) are required", true)
	}
	n, ok := wholeNumber(a.Photo)
	if !ok || n < 1 || n > 1<<30 {
		return proto.TextResult("photo is the photo's number, as its message names it", true)
	}
	text := strings.TrimSpace(*a.Text)
	if text == "" {
		return proto.TextResult("text is empty: say what in the photo matters for the meeting", true)
	}
	if c := utf8.RuneCountInString(text); c > kb.MaxPhotoNote {
		return proto.TextResult(fmt.Sprintf("text is %d characters, over the %d a note may have: make it shorter", c, kb.MaxPhotoNote), true)
	}
	switch err := e.PhotoNote(recording, int(n), text); {
	case err == nil:
		return proto.TextResult("Noted. Write nothing in reply.", false)
	case errors.Is(err, kb.ErrPhotoNotFound):
		return proto.TextResult(fmt.Sprintf("there is no photo %d in this meeting, or it was withdrawn: do not describe or use it", n), true)
	case errors.Is(err, kb.ErrReadOnly):
		return proto.TextResult("the meeting's photos can't be changed now", true)
	}
	return proto.TextResult("this meeting's photos are not available now", true)
}

// meetingPhotos are photos' words as the prompts take them (each on one line, at its time in the meeting), never an
// image.
func meetingPhotos(texts []kb.PhotoText) []prompts.MeetingPhoto {
	out := make([]prompts.MeetingPhoto, 0, len(texts))
	for _, p := range texts {
		out = append(out, prompts.MeetingPhoto{N: p.N, At: meetingClock(p.At), Caption: oneLine(p.Caption), Text: oneLine(p.Text), Note: oneLine(p.Note)})
	}
	return out
}
