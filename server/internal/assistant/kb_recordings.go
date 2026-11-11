package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
)

// The phone's recording sync (docs/protocol/protocol.md "Recording sync", kb/recordings.go): transcripts into the knowledge base,
// keyed by the recording's UUID. The hub relays these like the other knowledge-base routes.

// codeRecordingDeleted: the recording was deleted here; the phone drops it instead of uploading it again.
const codeRecordingDeleted = "recording_deleted"

func (s *Service) recordingRoutes(engine func(http.ResponseWriter, *http.Request) *kb.Engine) {
	s.handle("PUT /api/v1/recordings/{id}", func(w http.ResponseWriter, r *http.Request) {
		e := engine(w, r)
		if e == nil {
			return
		}
		var u kb.RecordingUpload
		// The transcript is at most 4 MB of text; JSON escaping and the segments' numbers on top.
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&u); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		put, err := e.PutRecording(r.PathValue("id"), u, time.Now())
		status := put.Status
		var full kb.DayFullError
		switch {
		case errors.Is(err, kb.ErrRecordingDeleted):
			apierr.WriteCode(w, http.StatusGone, codeRecordingDeleted, err)
			return
		case errors.Is(err, kb.ErrKindChanged): // to or from an all-day recording (kb_day.go)
			apierr.WriteCode(w, http.StatusConflict, codeKindChanged, i18n.Err("assistant-day-recap.kind_changed"))
			return
		case errors.As(err, &full): // a day over its limits: the phone splits it into parts
			writeDayFull(w, full.Next)
			return
		case errors.Is(err, kb.ErrBadRecording):
			apierr.Write(w, http.StatusBadRequest, err)
			return
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		id, _ := kb.RecordingID(r.PathValue("id"))
		day := dayUpload(u)
		if status == kb.RecordingStored && (u.Complete || day) {
			s.kbWake() // its summary and minutes now, not at the next round; a day's recap repaired or final
		}
		// The role in effect, always (an older manager leaves it out, so the phone stops attending), and what was
		// wrong with the upload's attendance fields, which never fail it.
		out := map[string]any{"id": id, "revision": u.Revision, "status": status, "attendance": put.Attendance}
		if day {
			putDayFields(out, put)
		}
		if put.AttendanceProblem != "" {
			out["attendance_problem"] = put.AttendanceProblem
		}
		if put.Room { // a room meeting (meeting_room.go): its room's state, at least none; only an older manager leaves it out
			room := &kb.RoomView{State: kb.RoomNone}
			if put.Meeting != nil && put.Meeting.Room != nil {
				room = put.Meeting.RoomView()
			}
			out["room"] = room
		}
		apierr.WriteJSON(w, out)
	})
	s.handle("DELETE /api/v1/recordings/{id}", func(w http.ResponseWriter, r *http.Request) {
		e := engine(w, r)
		if e == nil {
			return
		}
		deleted, err := e.DeleteRecording(r.PathValue("id"), time.Now())
		switch {
		case errors.Is(err, kb.ErrBadRecording):
			apierr.Write(w, http.StatusBadRequest, err)
			return
		case err != nil:
			apierr.Write(w, http.StatusInternalServerError, err)
			return
		}
		if deleted { // its room goes with it (meeting_room.go); the retention pass asks again if the node is away
			go s.meetingRoomsSweep(context.WithoutCancel(r.Context()), e)
		}
		apierr.WriteJSON(w, map[string]any{"deleted": deleted})
	})
	s.handle("GET /api/v1/recordings", func(w http.ResponseWriter, r *http.Request) {
		e := engine(w, r)
		if e == nil {
			return
		}
		kept, deleted := e.RecordingStates()
		apierr.WriteJSON(w, map[string]any{"days": e.RecordingDays(), "recordings": kept, "deleted": deleted})
	})
	s.minutesRoutes(engine)
	s.meetingRoutes()
	s.dayRoutes()
}
