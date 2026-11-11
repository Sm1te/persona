package assistant

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// After the meeting: the follow-up (docs/assistant/meeting-minutes.md "After the meeting: the follow-up"). The
// owner's first message about a meeting that is over makes a normal Persona conversation (every tool, its memory and
// live snapshot), origin meeting_followup, whose first message is the owner's own: the meeting's material (the seed)
// in a collapsed context block, then their words. No turn runs on the seed alone. Later calls send their words to it.
// It is made once per recording, under the recording's lock and across restarts: the creation is written as pending
// in meeting.json under a session id derived from the recording before the node is asked.

// followupTranscriptRunes is how much of the transcript of record the seed holds; a longer one gives its start and its
// end.
const followupTranscriptRunes = 100000

// followupPhotoRunes is how much of the photos' recognized text the seed holds, the earliest photos' first; the rest
// is cut (kb_get has it whole).
const followupPhotoRunes = 20000

// followupRoute is POST /api/v1/recordings/{id}/followup.
func (s *Service) followupRoute(w http.ResponseWriter, r *http.Request) {
	if !meetingCaller(w, r) {
		return
	}
	e := s.meetingEngine(w, r)
	if e == nil {
		return
	}
	raw, ok := meetingBody(w, r, maxMeetingSendBody, "32 MiB")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, _, ok := meetingRecording(w, e, id); !ok {
		return
	}
	msg, err := parseMeetingMessage(raw)
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
		return
	}
	m, rec, err := e.MeetingRead(id)
	if err != nil {
		writeMeetingError(w, err)
		return
	}
	switch {
	case meetingRunning(rec, m, time.Now()):
		apierr.WriteCode(w, http.StatusConflict, codeMeetingRunning, i18n.Err("assistant-meeting.meeting_running"))
		return
	case rec.Chars == 0 && !e.Store.HasMinutes(rec.ID):
		apierr.WriteCode(w, http.StatusConflict, codeNoMaterial, i18n.Err("assistant-meeting.no_material"))
		return
	}
	ctx, cancel := meetingSendCtx(r)
	defer cancel()
	conv, created, err := s.followupSend(ctx, e, rec.ID, msg)
	if err != nil {
		writeSendError(w, err)
		return
	}
	apierr.WriteJSON(w, map[string]any{"followup": conv, "created": created, "client_msg_id": "followup-" + msg.ID})
}

// meetingRunning: recording rec's meeting is not over for a follow-up: no ended_at (counted as ended feedStale after
// its last upload, and a trimmed one as ended), or a room still starting, ready or unavailable.
func meetingRunning(rec kb.Recording, m kb.Meeting, now time.Time) bool {
	if rec.Trimmed() {
		return false
	}
	if rec.EndedAt == nil && now.Sub(rec.Updated) < feedStale {
		return true
	}
	if m.Room == nil {
		return false
	}
	switch m.Room.State {
	case kb.RoomStarting, kb.RoomReady, kb.RoomUnavailable:
		return true
	}
	return false
}

// followupSend sends msg to recording id's follow-up, making it with msg the first time; the follow-up and whether
// this call made it.
func (s *Service) followupSend(ctx context.Context, e *kb.Engine, id string, msg meetingMessage) (kb.ConvView, bool, error) {
	unlock := s.meetLocks.lock(id)
	defer unlock()
	m, rec, err := e.MeetingRead(id)
	if err != nil {
		return kb.ConvView{}, false, err
	}
	msgID := "followup-" + msg.ID
	if f := m.Followup; f != nil && f.Pending {
		// A creation a restart left unsettled: the node's conversation under that id is the one (made by this call,
		// its message went with it); else a call other than the one making it drops the record and makes it now, and
		// the one making it makes it again under the same id.
		found, ok := s.followupFound(ctx, id, f)
		found.OpID = f.OpID // the call that made it
		switch {
		case ok && f.OpID == msg.ID:
			s.followupSet(e, id, &found)
			return kb.ConvView{MachineID: found.MachineID, SessionID: found.SessionID}, true, nil
		case ok:
			m.Followup = &found
			s.followupSet(e, id, &found)
		case f.OpID != msg.ID:
			m.Followup = nil
			s.followupSet(e, id, nil)
		}
	}
	if f := m.Followup; f != nil && !f.Pending && f.SessionID != "" {
		if f.OpID == msg.ID {
			// The call that made it, repeated (its answer lost, its receipt left unknown): its words went with the
			// creation; nothing is sent again.
			return kb.ConvView{MachineID: f.MachineID, SessionID: f.SessionID}, true, nil
		}
		p := map[string]any{"session_id": f.SessionID, "text": msg.Text, "client_msg_id": msgID}
		if len(msg.Images) > 0 {
			p["images"] = msg.Images
		}
		s.UserSpoke(ctx, SessionRef{MachineID: f.MachineID, SessionID: f.SessionID})
		params, err := s.SendParams(ctx, SendParams{MachineID: f.MachineID, SessionID: f.SessionID, Params: p})
		if err != nil {
			return kb.ConvView{}, false, err
		}
		if err := s.sendOnce(ctx, f.MachineID, params); err != nil {
			return kb.ConvView{}, false, err
		}
		return kb.ConvView{MachineID: f.MachineID, SessionID: f.SessionID}, false, nil
	}
	machine := s.settings(ctx).MachineID
	if machine == "" {
		return kb.ConvView{}, false, s.missingAssistantMachine(ctx)
	}
	want := meetingSessionID(id, partFollowup)
	pending := &kb.MeetingFollowup{MachineID: machine, SessionID: want, Pending: true, OpID: msg.ID}
	if !s.followupSet(e, id, pending) {
		return kb.ConvView{}, false, errors.New("the meeting could not be written")
	}
	text := s.followupSeed(ctx, e, rec, m, msg.Text)
	sess, err := s.startSignedIn(ctx, StartParams{Text: text, Images: msg.Images, Origin: OriginMeetingFollowup, OriginRef: id, MachineID: machine,
		NewSessionID: want, ClientMsgID: msgID})
	if err != nil {
		return kb.ConvView{}, false, err
	}
	s.titleFixed(sess.MachineID, sess.SessionID, rec.StartedAt, titleMeeting, feedMeetingName(rec, s.location(ctx)))
	made := &kb.MeetingFollowup{MachineID: sess.MachineID, SessionID: sess.SessionID, OpID: msg.ID}
	s.followupSet(e, id, made)
	return kb.ConvView{MachineID: sess.MachineID, SessionID: sess.SessionID}, true, nil
}

// meetingProfile is the Claude profile (config_dir) a meeting's conversations, its room and its follow-up, start in: the
// one the assistant's conversations had before each new one got a private profile, a config directory with no login
// in it (docs/assistant/meeting-minutes.md "The room conversation").
const meetingProfile = "legacy"

// startSignedIn starts p in meetingProfile, or as it is on a node too old to be told a profile, whose conversations
// all start in that one: the hub refuses the profile before anything starts (devices/machine.go), so the second start
// is the only one.
func (s *Service) startSignedIn(ctx context.Context, p StartParams) (proto.Session, error) {
	p.ConfigDir = meetingProfile
	sess, err := s.start(ctx, p)
	if nodeOutdated(err) {
		p.ConfigDir = ""
		return s.start(ctx, p)
	}
	return sess, err
}

// nodeOutdated: the hub refused a call for something the node is too old for.
func nodeOutdated(err error) bool {
	var re *rpc.Error
	return errors.As(err, &re) && re.Code == proto.CodeNodeOutdated
}

// followupSet writes recording id's follow-up (nil: none); false when it could not be written.
func (s *Service) followupSet(e *kb.Engine, id string, f *kb.MeetingFollowup) bool {
	if _, err := e.MeetingUpdate(id, func(m *kb.Meeting, _ kb.Recording) error { m.Followup = f; return nil }); err != nil {
		s.logf("meeting %s: follow-up: %v", short(id), err)
		return false
	}
	return true
}

// followupFound is the follow-up a pending creation f made, when there is one: the conversation under its id on its
// computer, else one whose origin says it is this recording's follow-up (a node that started it under its own id).
func (s *Service) followupFound(ctx context.Context, id string, f *kb.MeetingFollowup) (kb.MeetingFollowup, bool) {
	if _, ok := s.session(ctx, f.MachineID, f.SessionID); ok {
		return kb.MeetingFollowup{MachineID: f.MachineID, SessionID: f.SessionID}, true
	}
	for _, o := range s.originsOf(ctx, OriginMeetingFollowup) {
		if o.ref == id {
			return kb.MeetingFollowup{MachineID: o.machine, SessionID: o.session}, true
		}
	}
	return kb.MeetingFollowup{}, false
}

// followupSeed is the follow-up's first message: the meeting's material in a context block (prompts/meeting/
// followup_seed.md), then the owner's text.
func (s *Service) followupSeed(ctx context.Context, e *kb.Engine, rec kb.Recording, m kb.Meeting, text string) string {
	loc := s.location(ctx)
	d := prompts.MeetingFollowupSeed{Title: feedMeetingName(rec, loc), Started: rec.StartedAt.In(loc).Format("2006-01-02 15:04 MST"),
		RecordingID: rec.ID, Trimmed: rec.Trimmed(), Text: text}
	if rec.Calendar != nil {
		d.Calendar = oneLine(rec.Calendar.Title)
	}
	if m.Private != nil {
		d.PrivateSet, d.Private = true, *m.Private
	}
	if mins, err := e.Store.ReadMinutes(rec.ID); err == nil {
		d.Minutes = strings.TrimSpace(mins.Markdown)
	}
	if rec.Trimmed() {
		return prompts.RenderMeetingFollowupSeed(d)
	}
	// The photos still listed: their words after the minutes, the room's notes on them with the room thread. One the
	// owner deleted is in neither (a follow-up made before keeps its copy).
	budget := followupPhotoRunes
	for _, p := range meetingPhotos(e.PhotoTexts(rec.ID)) {
		if n := utf8.RuneCountInString(p.Text); n > budget {
			p.Text, d.PhotosCut = cutRunes(p.Text, budget), true
		}
		budget -= utf8.RuneCountInString(p.Text)
		if p.Caption != "" || p.Text != "" {
			d.Photos = append(d.Photos, p)
		}
		if p.Note != "" {
			d.Notes = append(d.Notes, p)
		}
	}
	if l, _, err := e.LiveRead(rec.ID); err == nil {
		for _, a := range l.Asks {
			if a.State == askAnswered {
				q := a.Question
				if q == "" && len(a.Heard) > 0 {
					q = a.Heard[len(a.Heard)-1].Text
				}
				d.Thread = append(d.Thread, prompts.MeetingQA{Question: oneLine(q), Answer: oneLine(a.Answer)})
			}
		}
	}
	var lines []string
	for _, sg := range e.Store.RecordingSegments(rec.ID) {
		lines = append(lines, "["+meetingClock(sg.Start)+"] "+oneLine(sg.Text))
	}
	d.Transcript, d.Cut = cutMiddle(strings.Join(lines, "\n"), followupTranscriptRunes)
	return prompts.RenderMeetingFollowupSeed(d)
}

// cutMiddle is s within n runes: its start and its end with the middle left out; true when it was cut.
func cutMiddle(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	half := n / 2
	return string(r[:half]) + "\n…\n" + string(r[len(r)-half:]), true
}
