package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
)

// Attending a recorded meeting (docs/assistant/meeting-minutes.md "Attending a meeting", kb/live.go): the phone's
// live sentences (POST / GET …/live) and its questions (POST …/ask, meeting_ask.go). The live log is the knowledge
// base's; feeding it to the attending session is the feeder's (meetingKick). Full credentials only: the watch's
// answer-only token and extension tokens never reach these routes at the hub (they are not in extwire.RouteScopes),
// and any other role is refused here.

// Codes of the attending routes' refusals (the contract's).
const (
	codeRecordingNotFound = "recording_not_found" // no upload of this recording yet: the phone uploads it, then sends again
	codeNotAttending      = "not_attending"
	codeLiveGap           = "live_gap"
	codeLiveFull          = "live_full"
	codeOpIDReused        = "op_id_reused"
	codeAsksFull          = "asks_full"
	codeAskBusy           = "ask_busy"
	codeMeetingEnded      = "meeting_ended" // the room ended: ask in the follow-up
	codeNotMeeting        = "not_meeting"
	codeMeetingRunning    = "meeting_running"
	codeNoMaterial        = "no_material"
)

// The bodies the attending routes read at most.
const (
	maxLiveBody = 1 << 20
	maxAskBody  = 64 << 10
)

func (s *Service) meetingRoutes() {
	s.handle("POST /api/v1/recordings/{id}/live", func(w http.ResponseWriter, r *http.Request) {
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r)
		if e == nil {
			return
		}
		raw, ok := meetingBody(w, r, maxLiveBody, "1 MiB")
		if !ok {
			return
		}
		id := r.PathValue("id")
		_, _, ok = meetingRecording(w, e, id) // 410 and 404 before the body is read as live lines
		if !ok {
			return
		}
		if !s.meetingWritable(r.Context(), e) {
			apierr.WriteCode(w, http.StatusForbidden, assistantwire.CodeKBReadOnly, i18n.Err("assistant-kb_routes.read_only"))
			return
		}
		from, segs, err := parseLive(raw)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		l, rec, err := e.LiveAppend(id, from, segs)
		var gap *kb.LiveGapError
		switch {
		case errors.As(err, &gap):
			body := apierr.LocalizedBody(http.StatusConflict, codeLiveGap, i18n.Err("assistant-meeting.live_gap", i18n.Args{"next": gap.Next}), i18n.OfWriter(w))
			body["next"] = gap.Next
			apierr.WriteBody(w, http.StatusConflict, body)
			return
		case errors.Is(err, kb.ErrLiveFull):
			apierr.WriteCode(w, http.StatusRequestEntityTooLarge, codeLiveFull, i18n.Err("assistant-meeting.live_full"))
			return
		case err != nil:
			writeMeetingError(w, err)
			return
		}
		apierr.WriteJSON(w, s.liveView(r.Context(), l, rec, nil))
	})
	s.handle("GET /api/v1/recordings/{id}/live", func(w http.ResponseWriter, r *http.Request) {
		if !meetingCaller(w, r) {
			return
		}
		e := s.meetingEngine(w, r) // a read-only knowledge base answers it too
		if e == nil {
			return
		}
		l, rec, err := s.asksRead(e, r.PathValue("id"))
		if err != nil {
			writeMeetingError(w, err)
			return
		}
		asks := make([]askView, 0, len(l.Asks))
		for _, a := range l.Asks {
			asks = append(asks, viewAsk(a))
		}
		view := s.liveView(r.Context(), l, rec, asks)
		if rev := e.PhotosRev(rec.ID); rev > 0 { // photos has a revision and no field: GET …/photos reads them
			view.Revs[kb.PartPhotos] = rev
		}
		if parts := r.URL.Query().Get("parts"); parts != "" {
			apierr.WriteJSON(w, view.only(strings.Split(parts, ",")))
			return
		}
		apierr.WriteJSON(w, view)
	})
	s.askRoute()
	s.meetingConversationRoutes()
	s.meetingPhotoRoutes()
}

// meetingCaller: the request carries full credentials; anything else has been answered 403 forbidden.
func meetingCaller(w http.ResponseWriter, r *http.Request) bool {
	if fullRole(r) {
		return true
	}
	apierr.Write(w, http.StatusForbidden, i18n.Err("assistant-meeting.forbidden"))
	return false
}

// meetingEngine is the knowledge base for an attending route: 503 kb_disabled or kb_unavailable when it is not there,
// as the other knowledge-base routes answer. Unlike theirs it lets a read-only one through: the contract refuses
// changes there only after the body and the recording are checked (meetingWritable).
func (s *Service) meetingEngine(w http.ResponseWriter, r *http.Request) *kb.Engine {
	e, err := s.kbGet(r.Context())
	switch {
	case errors.Is(err, errKBOff):
		apierr.WriteCode(w, http.StatusServiceUnavailable, assistantwire.CodeKBDisabled, errKBRouteOff)
	case err != nil:
		apierr.WriteCode(w, http.StatusServiceUnavailable, assistantwire.CodeKBUnavailable, errKBRouteNotOpen)
	}
	return e
}

// meetingWritable: the knowledge base takes changes now (not read-only, in the settings or as opened).
func (s *Service) meetingWritable(ctx context.Context, e *kb.Engine) bool {
	return !s.kbReadOnly(ctx) && !e.Store.ReadOnly()
}

// meetingBody is the request's body, at most most bytes; over it has been answered 413 too_large (said as size).
func meetingBody(w http.ResponseWriter, r *http.Request, most int64, size string) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, most+1))
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return nil, false
	}
	if int64(len(raw)) > most {
		apierr.Write(w, http.StatusRequestEntityTooLarge, i18n.Err("assistant-meeting.too_large", i18n.Args{"size": size}))
		return nil, false
	}
	return raw, true
}

// meetingRecording is recording id with its live log; else 410 recording_deleted or 404 recording_not_found has been
// answered.
func meetingRecording(w http.ResponseWriter, e *kb.Engine, id string) (kb.Live, kb.Recording, bool) {
	l, rec, err := e.LiveRead(id)
	if err != nil {
		writeMeetingError(w, err)
		return l, rec, false
	}
	return l, rec, true
}

// writeMeetingError answers the knowledge base's refusals of a meeting route as the contract codes them (the photos'
// too, meeting_photos.go).
func writeMeetingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, kb.ErrRecordingDeleted):
		apierr.WriteCode(w, http.StatusGone, codeRecordingDeleted, i18n.Err("assistant-meeting.deleted"))
	case errors.Is(err, kb.ErrNotFound):
		apierr.WriteCode(w, http.StatusNotFound, codeRecordingNotFound, i18n.Err("assistant-meeting.no_recording"))
	case errors.Is(err, kb.ErrReadOnly):
		apierr.WriteCode(w, http.StatusForbidden, assistantwire.CodeKBReadOnly, i18n.Err("assistant-kb_routes.read_only"))
	case errors.Is(err, kb.ErrNotAttending):
		apierr.WriteCode(w, http.StatusConflict, codeNotAttending, i18n.Err("assistant-meeting.not_attending"))
	case errors.Is(err, kb.ErrBadLive), errors.Is(err, kb.ErrBadPhoto):
		apierr.Write(w, http.StatusBadRequest, err)
	case errors.Is(err, kb.ErrNotMeeting):
		apierr.WriteCode(w, http.StatusConflict, codeNotMeeting, i18n.Err("assistant-meeting.not_meeting"))
	case errors.Is(err, kb.ErrTranscriptExpired):
		apierr.WriteCode(w, http.StatusConflict, codeTranscriptExpired, i18n.Err("assistant-meeting.transcript_expired"))
	case errors.Is(err, kb.ErrPhotoNotFound):
		apierr.WriteCode(w, http.StatusNotFound, codePhotoNotFound, i18n.Err("assistant-meeting.photo_not_found"))
	case errors.Is(err, kb.ErrPhotoDeleted):
		apierr.WriteCode(w, http.StatusGone, codePhotoDeleted, i18n.Err("assistant-meeting.photo_deleted"))
	case errors.Is(err, kb.ErrOpIDReused):
		apierr.WriteCode(w, http.StatusConflict, codeOpIDReused, i18n.Err("assistant-meeting.photo_id_reused"))
	case errors.Is(err, kb.ErrPhotosFull):
		apierr.WriteCode(w, http.StatusRequestEntityTooLarge, codePhotosFull, i18n.Err("assistant-meeting.photos_full", i18n.Args{"max": kb.MaxPhotos}))
	default:
		apierr.Write(w, http.StatusInternalServerError, err)
	}
}

// parseLive reads a POST …/live body: from (required, a whole number of 0 or more) and the sentences (at most
// kb.MaxLiveRequest; each time a number). Anything else wrong with a sentence is mended by the knowledge base, never
// refused: an absent time is 0, text that is not a string an empty placeholder.
func parseLive(raw []byte) (int, []kb.LiveSegment, error) {
	var in struct {
		From     json.RawMessage   `json:"from"`
		Segments []json.RawMessage `json:"segments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return 0, nil, i18n.Err("assistant-meeting.bad_json", i18n.Args{"error": err.Error()})
	}
	from, ok := wholeNumber(in.From)
	if !ok || from < 0 {
		return 0, nil, i18n.Err("assistant-meeting.bad_from")
	}
	if len(in.Segments) > kb.MaxLiveRequest {
		return 0, nil, i18n.Err("assistant-meeting.too_many_segments", i18n.Args{"max": kb.MaxLiveRequest})
	}
	segs := make([]kb.LiveSegment, len(in.Segments))
	for i, rawSeg := range in.Segments {
		var sg struct {
			Start, End, Text json.RawMessage
		}
		if err := json.Unmarshal(rawSeg, &sg); err != nil {
			return 0, nil, i18n.Err("assistant-meeting.bad_json", i18n.Args{"error": err.Error()})
		}
		for _, t := range []struct {
			raw json.RawMessage
			to  *float64
		}{{sg.Start, &segs[i].Start}, {sg.End, &segs[i].End}} {
			if t.raw != nil && json.Unmarshal(t.raw, t.to) != nil {
				return 0, nil, i18n.Err("assistant-meeting.bad_time")
			}
		}
		_ = json.Unmarshal(sg.Text, &segs[i].Text)
	}
	return int(from), segs, nil
}

// wholeNumber is raw as a whole JSON number (12 or 12.0); false for anything else, null and absent included.
func wholeNumber(raw json.RawMessage) (int64, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] == '"' {
		return 0, false
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil || n == "" {
		return 0, false
	}
	if v, err := strconv.ParseInt(string(n), 10, 64); err == nil {
		return v, true
	}
	f, err := n.Float64()
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// meetingLiveView is what …/live answers: the live log's count, the role in effect and the session being fed. With
// attending off it is only the role, the counts and the questions.
type meetingLiveView struct {
	Next             int           `json:"next"`
	Attendance       string        `json:"attendance"`
	AttendingSession *attendeeView `json:"attending_session,omitempty"`
	Fed              int           `json:"fed"`
	Problem          string        `json:"problem,omitempty"`
	Asks             *[]askView    `json:"asks,omitempty"` // GET only
	// The meeting conversation (#3862): the private mark (absent until chosen), a room meeting's room, the follow-up
	// once made, and each part's revision (GET only).
	Private  *bool            `json:"private,omitempty"`
	Room     *kb.RoomView     `json:"room,omitempty"`
	Followup *kb.ConvView     `json:"followup,omitempty"`
	Revs     map[string]int64 `json:"revs,omitempty"`
}

// only is v cut to parts (GET …/live?parts=asks,room): those of them it has, with their revisions.
func (v meetingLiveView) only(parts []string) map[string]any {
	out := map[string]any{}
	revs := map[string]int64{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch p {
		case kb.PartAsks:
			if v.Asks != nil {
				out[p] = *v.Asks
			}
		case kb.PartRoom:
			if v.Room != nil {
				out[p] = v.Room
			}
		case kb.PartPrivate:
			if v.Private != nil {
				out[p] = *v.Private
			}
		case kb.PartFollowup:
			if v.Followup != nil {
				out[p] = v.Followup
			}
		case kb.PartPhotos:
			// Only its revision: the photos are read with GET …/photos.
		default:
			continue
		}
		if rev, ok := v.Revs[p]; ok {
			revs[p] = rev
		}
	}
	out["revs"] = revs
	return out
}

// attendeeView is the session an attended meeting is fed to; Main says it is the main chat.
type attendeeView struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Main      bool   `json:"main,omitempty"`
}

// liveView is recording rec's live state l as …/live answers it; asks (nil: left out) are its questions.
func (s *Service) liveView(ctx context.Context, l kb.Live, rec kb.Recording, asks []askView) meetingLiveView {
	out := meetingLiveView{Next: len(l.Segments), Attendance: rec.AttendanceRole(), Fed: min(l.Fed, len(l.Segments))}
	if asks != nil {
		out.Asks = &asks
	}
	out.Revs = map[string]int64{kb.PartAsks: l.AsksRev}
	if m := rec.Conv; m != nil {
		out.Private, out.Room, out.Followup = m.Private, m.RoomView(), m.FollowupView()
		for _, p := range []string{kb.PartRoom, kb.PartPrivate, kb.PartFollowup} {
			if rev, ok := m.Revs[p]; ok {
				out.Revs[p] = rev
			}
		}
	}
	if asks == nil {
		out.Revs = nil // POST …/live: GET reads them
	}
	if rec.RoomMeeting() {
		if l.Full { // the room's own problems are its room's
			out.Problem = codeLiveFull
		}
		return out
	}
	if !rec.Attending() {
		return out
	}
	if ref, main, ok := s.meetingAttendee(ctx, rec); ok {
		out.AttendingSession = &attendeeView{MachineID: ref.MachineID, SessionID: ref.SessionID, Main: main}
	}
	switch {
	case l.Full:
		out.Problem = codeLiveFull
	default:
		out.Problem = l.Problem
	}
	return out
}

// meetingAttendee is the session recording rec's meeting goes to now: the one its upload names, else the main chat
// as it is now (false when there is none); main says it is the main chat.
func (s *Service) meetingAttendee(ctx context.Context, rec kb.Recording) (ref kb.SessionRef, main, ok bool) {
	if a := rec.AttendingSession; a != nil {
		return *a, s.isMainOn(ctx, a.MachineID, a.SessionID), true
	}
	set := s.settings(ctx)
	if set.Main == "" || set.MachineID == "" {
		return kb.SessionRef{}, false, false
	}
	return kb.SessionRef{MachineID: set.MachineID, SessionID: set.Main}, true, true
}
