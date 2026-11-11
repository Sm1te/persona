package assistant

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The meeting's room conversation (docs/assistant/meeting-minutes.md "Meeting conversation (#3862)", "The room
// conversation"): a room meeting (one whose upload carries room, kb/meeting.go) is attended by a conversation of its
// own, a guest of kind meeting on the assistant's computer, limited to meeting material: Claude Code only, no runtime
// tools, its own directory with a meeting CLAUDE.md, none of the owner's Claude Code setup (node/assistant_guest.go),
// and only the meeting tools, bound to its recording (meeting_room_tools.go). It is a side exchange everywhere
// (meeting_turns.go takes its every turn for a meeting turn).
//
// Its state is meeting.json's (kb.MeetingRoom): none until the role is not off, then starting (made once, also across
// a restart: the creation is written as pending, under a session id derived from the recording, before the node is
// asked), ready, or unavailable with a problem (machine_offline and start_failed retried every minute); ended at the
// meeting's end; deleted at the transcript's cut or with the recording (meetingRoomsSweep). The feeder looks at a
// room meeting as it looks at an attended one (attendLook → roomLook): it moves the room along and feeds it batches.

// Origins of a meeting's conversations (proto.Session.Origin): the room, and the follow-up made after the meeting.
const (
	OriginMeeting         = "meeting"
	OriginMeetingFollowup = "meeting_followup"
)

// titleMeeting is the kind in a meeting conversation's title, "MMDD | 会议 | <meeting title>".
const titleMeeting = "会议" // i18n-ignore: a title's kind, stored as titleKinds are

// The room's times.
const (
	roomStartWithin = 2 * time.Minute // a start not ready by then failed: start_failed
	roomRetry       = time.Minute     // machine_offline and start_failed are tried again
)

// meetingRoomDir is where the rooms' directories are on the assistant's computer, relative to the home as the node
// names them (node/assistant_guest.go guestMeetingDir).
var meetingRoomDir = compat.StateDir + "/guest-meeting"

// The parts of a meeting a conversation is made for: the session id of each is derived from the recording.
const (
	partRoom     = "room"
	partFollowup = "followup"
)

// meetingSessionID is the session id of recording's conversation part: a UUID derived from both, so a creation asked
// again after a restart asks for the same one.
func meetingSessionID(recording, part string) string {
	sum := sha256.Sum256([]byte("persona-meeting:" + part + ":" + strings.ToLower(recording)))
	sum[6], sum[8] = sum[6]&0x0f|0x40, sum[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// meetingEnd is when recording r's meeting is over for its room ("The meeting's end"): the first upload carrying both
// ended_at and complete, else feedEndWait after the first one carrying ended_at; a meeting no upload ended is over
// feedStale after its last upload, and a trimmed one has ended. Zero while none of these has come; due says it has.
func meetingEnd(r kb.Recording, l kb.Live, now time.Time) (at time.Time, due bool) {
	switch {
	case r.Trimmed():
		return now, true
	case r.EndedAt != nil && r.Complete:
		return cmp.Or(l.EndedAt, now), true
	case !l.EndedAt.IsZero():
		at = l.EndedAt.Add(feedEndWait)
	default:
		at = feedLatest(r.Updated, l.EndedAt).Add(feedStale)
	}
	return at, !now.Before(at)
}

// roomLook is attendLook for a room meeting: it moves the room along (starting it, retrying it, ending it) and feeds
// it what is due; when to look again (zero: when it next changes).
func (s *Service) roomLook(ctx context.Context, e *kb.Engine, id string, l kb.Live, r kb.Recording, now time.Time) time.Time {
	room := r.Conv.Room
	endAt, ended := meetingEnd(r, l, now)
	var next time.Time
	if !ended {
		next = endAt // the end is looked at when it is due
	}
	switch room.State {
	case "", kb.RoomNone:
		if ended || !r.Attending() {
			return time.Time{} // none: the role has been off from the start, also after the end
		}
		return feedEarliest(s.roomStart(ctx, e, id, now), next)
	case kb.RoomStarting:
		switch {
		case ended:
			s.roomSet(e, id, func(rm *kb.MeetingRoom) { rm.State = kb.RoomEnded })
			return time.Time{}
		case now.Sub(room.StartingAt) >= roomStartWithin:
			s.roomSet(e, id, func(rm *kb.MeetingRoom) {
				rm.State, rm.Problem, rm.Pending, rm.RetryAt = kb.RoomUnavailable, kb.RoomStartFailed, false, now.Add(roomRetry)
			})
			s.roomAsksFallBack(id)
			return feedEarliest(now.Add(roomRetry), next)
		}
		return feedEarliest(s.roomStart(ctx, e, id, now), next)
	case kb.RoomUnavailable:
		if ended {
			s.roomSet(e, id, func(rm *kb.MeetingRoom) { rm.State = kb.RoomEnded })
			return time.Time{}
		}
		if room.Problem != kb.RoomMachineOffline && room.Problem != kb.RoomStartFailed {
			return next // runtime_unsupported and node_too_old are not tried again
		}
		if !r.Attending() {
			return next // nothing to attend: tried again once the role is on
		}
		if now.Before(room.RetryAt) {
			return feedEarliest(room.RetryAt, next)
		}
		s.roomSet(e, id, func(rm *kb.MeetingRoom) { rm.State, rm.StartingAt, rm.Pending = kb.RoomStarting, now, true })
		return feedEarliest(s.roomStart(ctx, e, id, now), next)
	case kb.RoomReady:
		if ended {
			return s.roomEnding(ctx, e, id, l, r, now)
		}
		return feedEarliest(s.roomFeed(ctx, e, id, l, r, now, false), next)
	}
	return time.Time{} // ended, deleted: nothing more until the sweep deletes it
}

// roomSet changes recording id's room with fn (kb.MeetingUpdate); what it is after, false when it could not be written.
func (s *Service) roomSet(e *kb.Engine, id string, fn func(rm *kb.MeetingRoom)) (kb.MeetingRoom, bool) {
	m, err := e.MeetingUpdate(id, func(m *kb.Meeting, _ kb.Recording) error {
		if m.Room == nil {
			return errNoRoom
		}
		fn(m.Room)
		return nil
	})
	if err != nil {
		if !errors.Is(err, kb.ErrNotFound) && !errors.Is(err, kb.ErrRecordingDeleted) && !errors.Is(err, errNoRoom) {
			s.logf("meeting %s: room: %v", short(id), err)
		}
		return kb.MeetingRoom{}, false
	}
	return *m.Room, true
}

var errNoRoom = errors.New("not a room meeting")

// roomStart starts recording id's room, or settles a start a restart left pending: under the session id derived
// from the recording, written as pending first, so the node makes it once ("Made once"). When to look again.
func (s *Service) roomStart(ctx context.Context, e *kb.Engine, id string, now time.Time) time.Time {
	machine := s.settings(ctx).MachineID
	sid := meetingSessionID(id, partRoom)
	room, ok := s.roomSet(e, id, func(rm *kb.MeetingRoom) {
		if rm.State != kb.RoomStarting {
			rm.State, rm.StartingAt = kb.RoomStarting, now
		}
		if !rm.Pending || rm.SessionID == "" {
			rm.Pending, rm.MachineID, rm.SessionID = true, machine, sid
		}
	})
	if !ok {
		return time.Time{}
	}
	machine = cmp.Or(room.MachineID, machine)
	unavailable := func(problem string) time.Time {
		s.roomSet(e, id, func(rm *kb.MeetingRoom) {
			rm.State, rm.Problem, rm.Pending, rm.RetryAt = kb.RoomUnavailable, problem, false, now.Add(roomRetry)
		})
		s.roomAsksFallBack(id)
		return now.Add(roomRetry)
	}
	if machine == "" || s.isWorkMachine(machine) {
		return unavailable(kb.RoomMachineOffline)
	}
	if _, online := s.attendeeMachine(ctx, machine); !online {
		return unavailable(kb.RoomMachineOffline)
	}
	_, r, err := e.MeetingRead(id)
	if err != nil {
		return time.Time{}
	}
	got, problem, err := s.roomCreate(ctx, machine, room.SessionID, r)
	switch {
	case problem != "":
		return unavailable(problem)
	case err != nil:
		s.logf("meeting %s: start the room: %v", short(id), err)
		if now.Sub(room.StartingAt) >= roomStartWithin {
			return unavailable(kb.RoomStartFailed)
		}
		return now.Add(feedBusyPoll) // tried again while it may still be ready in time
	}
	s.roomSet(e, id, func(rm *kb.MeetingRoom) {
		rm.State, rm.Problem, rm.Pending, rm.MachineID, rm.SessionID, rm.Started = kb.RoomReady, "", false, machine, got, true
	})
	s.meetingKick(id) // the meeting so far, and the asks that waited for it
	return time.Time{}
}

// roomCreate makes recording r's room on machine under sid, or finds the one a start left there: its directory with
// the meeting CLAUDE.md, the book's guest and origin noted before the node is asked (its first tool calls come before
// the start answers), then the start with the meeting header. The session id it has; a problem (kb.Room*) when the
// node cannot run a room, an error otherwise.
func (s *Service) roomCreate(ctx context.Context, machine, sid string, r kb.Recording) (string, string, error) {
	lang := cmp.Or(strings.TrimSpace(r.Language), string(s.lang(ctx)))
	md := prompts.RenderMeetingRoomClaudeMD(prompts.MeetingRoomClaudeMD{Owner: s.owner(ctx), Language: lang})
	var prep struct {
		Cwd      string `json:"cwd"`
		Kind     string `json:"kind"`
		StartIDs bool   `json:"start_ids"`
	}
	dir := path.Join(meetingRoomDir, r.ID)
	if err := s.call(ctx, machine, DeviceAssistantGuestPrepare, map[string]any{"dir": dir, "claude_md": md, "kind": guestKindMeeting}, &prep); err != nil {
		if unknownMethod(err) {
			return "", kb.RoomNodeTooOld, nil
		}
		if errors.Is(err, ErrNoDevice) || offlineErr(err) {
			return "", kb.RoomMachineOffline, nil
		}
		return "", "", err
	}
	if prep.Kind != guestKindMeeting || !prep.StartIDs || prep.Cwd == "" {
		return "", kb.RoomNodeTooOld, nil // an older node: it would start an ordinary guest, with the owner's setup
	}
	if err := s.guestSetRoom(ctx, machine, sid, r.ID); err != nil {
		return "", "", err // its tool calls could not be told from an ordinary guest's
	}
	s.originSet(ctx, machine, sid, OriginMeeting, r.ID, "")
	s.titleFixed(machine, sid, r.StartedAt, titleMeeting, feedMeetingName(r, s.location(ctx)))
	text := prompts.RenderMeetingRoomHeader(roomHeader(r, lang, s.location(ctx)))
	params := map[string]any{"cwd": prep.Cwd, "text": text, "new_session_id": sid, "is_main": false,
		"guest":          map[string]any{"tools": meetingRoomToolNames(), "kind": guestKindMeeting},
		"message_source": &proto.MessageSource{Kind: proto.SourceMeeting, Title: feedMeetingName(r, s.location(ctx))},
		"client_msg_id":  "meeting-" + r.ID}
	// The main chat's launcher and model, on the CLI even when the main chat runs on the Agent SDK: a room never does.
	if err := s.applyModelCLI(ctx, "assistant", params); err != nil {
		return "", "", err
	}
	delete(params, "permission_mode")
	finish := s.beginAssistantStart(machine)
	defer finish()
	var res struct {
		SessionID string `json:"session_id"`
	}
	params["config_dir"] = meetingProfile
	err := s.call(ctx, machine, DeviceAssistantSend, params, &res)
	if nodeOutdated(err) {
		delete(params, "config_dir") // a node too old to be told a profile: its conversations all start in that one
		err = s.call(ctx, machine, DeviceAssistantSend, params, &res)
	}
	if err != nil {
		switch {
		case guestAgentRefused(err):
			return "", kb.RoomRuntimeUnsupported, nil
		case errors.Is(err, ErrNoDevice) || offlineErr(err):
			return "", kb.RoomMachineOffline, nil
		}
		return "", "", err
	}
	if res.SessionID != "" && res.SessionID != sid {
		return "", kb.RoomNodeTooOld, nil // it did not start under the id: a node that cannot make a room once
	}
	return sid, "", nil
}

// roomHeader is recording r's room header: its upload's title and calendar hint, when it started, its language and
// the room's role.
func roomHeader(r kb.Recording, lang string, loc *time.Location) prompts.MeetingRoomHeader {
	h := prompts.MeetingRoomHeader{Title: feedMeetingName(r, loc), Started: r.StartedAt.In(loc).Format("2006-01-02 15:04 MST"),
		Language: lang, Role: r.AttendanceRole()}
	if c := r.Calendar; c != nil {
		h.Calendar = &prompts.MeetingCalendar{Title: oneLine(c.Title), Location: oneLine(c.Location)}
		for _, a := range c.Attendees {
			if a = oneLine(a); a != "" {
				h.Calendar.Attendees = append(h.Calendar.Attendees, a)
			}
		}
	}
	return h
}

// unknownMethod: a node answered that it does not know the method (an older node).
func unknownMethod(err error) bool {
	return err != nil && strings.Contains(err.Error(), "unknown method")
}

// offlineErr: the call did not reach the machine (its node is not connected).
func offlineErr(err error) bool {
	var re *rpc.Error
	if errors.As(err, &re) && (re.Code == CodeMachineOffline || re.Code == CodeNodeClosed) {
		return true
	}
	return errors.Is(err, rpc.ErrClosed)
}

// guestAgentRefused: the node will not run a room on what it has (not Claude Code, or a Claude Code it cannot start
// that way, or none).
func guestAgentRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "guest conversation runs on Claude Code only") || strings.Contains(msg, proto.CodeClaudeMissing)
}

// roomFeed feeds recording id's room what is due (a batch, or with end the end batch), as the feeder feeds an
// attending session ("Feeding"), but with no meeting-turn mark, and held while an ask waits for the room or runs in
// it. The photos go before a batch, one a look (meeting_photos.go roomPhotos): none after the meeting's end, and only
// their withdrawals while the role is off; one whose sends keep failing lets the batch go and is looked at again
// soon. When to look again; with end, zero once the end batch went (or there was none to send) and no photo message is
// left failing.
func (s *Service) roomFeed(ctx context.Context, e *kb.Engine, id string, l kb.Live, r kb.Recording, now time.Time, end bool) (next time.Time) {
	if !r.Attending() {
		s.attendNote(e, id, func(l *kb.Live) { l.FedTo, l.Problem = nil, "" }) // turned on again: the meeting so far
		next, _ = s.roomPhotos(ctx, e, id, r, now, false)
		return next
	}
	room := r.Conv.Room
	if roomAskWaiting(l) {
		// An ask a restart left pending is taken up again here too (settleAsksLocked), not only when the asks are
		// read: a phone that only streams lines reads none, and the batches, the end batch too, would wait for it.
		if fresh, _, err := s.asksRead(e, id); err == nil {
			l = fresh
		}
	}
	if roomAskWaiting(l) {
		return now.Add(feedBusyPoll) // an ask goes first; the batch after it
	}
	photos, held := s.roomPhotos(ctx, e, id, r, now, !end)
	if held {
		return photos // the batch waits for the photo's turn
	}
	defer func() { next = feedEarliest(next, photos) }() // a photo message failing: tried again first at the next look
	if !end && !l.FedAt.IsZero() && now.Before(l.FedAt.Add(feedBatchEvery)) {
		return l.FedAt.Add(feedBatchEvery)
	}
	ref := kb.SessionRef{MachineID: room.MachineID, SessionID: room.SessionID}
	fresh := l.FedTo == nil || !s.sameMachine(l.FedTo.MachineID, ref.MachineID) || l.FedTo.SessionID != ref.SessionID
	b := feedBatchOf(l, fresh, end)
	b.room = true
	// The room answered its own asks: they are in its thread already, only the ones meeting.answer took are told.
	b.answers = slices.DeleteFunc(b.answers, func(a kb.LiveAsk) bool { return a.Via == kb.AskViaRoom })
	if b.empty() {
		if end && l.FedTo == nil {
			s.attendNote(e, id, func(l *kb.Live) { l.EndFed = true })
			return time.Time{}
		}
		if !end {
			if !fresh && b.upto > l.Fed {
				s.attendNote(e, id, func(l *kb.Live) { l.Fed = max(l.Fed, b.upto) })
			}
			return time.Time{}
		}
	}
	target, verdict := s.attendeeCheckFor(ctx, ref, now, id)
	switch verdict {
	case "":
	case attendeeBusy:
		return now.Add(feedBusyPoll)
	default:
		return now.Add(feedRetry) // its computer away: the room is told what it missed once back
	}
	sent, err := s.feedSend(ctx, target, r, b)
	switch {
	case feedRefused(err):
		return now.Add(feedBusyPoll)
	case err != nil:
		s.logf("meeting %s: batch to its room: %v", short(id), err)
		return now.Add(feedRetry)
	}
	fedTo := target.ref
	s.attendNote(e, id, func(l *kb.Live) {
		if fresh {
			l.Fed = b.upto
		} else {
			l.Fed = max(l.Fed, b.upto)
		}
		l.FedTo, l.FedAt, l.Problem = &fedTo, sent, ""
		for i := range l.Asks {
			if b.asks[l.Asks[i].ID] {
				l.Asks[i].Fed = true
			}
		}
		if end {
			l.EndFed = true
		}
	})
	if end {
		return now.Add(feedBusyPoll) // its turn ends, then the room
	}
	return sent.Add(feedBatchEvery)
}

// roomAskWaiting: an ask of the meeting waits for the room or runs in it (pending, and not answered from material).
func roomAskWaiting(l kb.Live) bool {
	for _, a := range l.Asks {
		if a.State == askPending && a.Via != kb.AskViaMaterial {
			return true
		}
	}
	return false
}

// roomEnding ends a ready room at the meeting's end: with the role on, it gets the end batch and turns ended once
// that turn ends, feedEndWait after the end at the latest (an end batch not delivered by then is dropped); with the
// role off at once.
func (s *Service) roomEnding(ctx context.Context, e *kb.Engine, id string, l kb.Live, r kb.Recording, now time.Time) time.Time {
	room := r.Conv.Room
	if room.EndingAt.IsZero() {
		if rm, ok := s.roomSet(e, id, func(rm *kb.MeetingRoom) { rm.EndingAt = now }); ok {
			room = &rm
		}
	}
	deadline := room.EndingAt.Add(feedEndWait)
	done := func() time.Time {
		s.roomSet(e, id, func(rm *kb.MeetingRoom) { rm.State = kb.RoomEnded })
		s.roomAsksFallBack(id)
		return time.Time{}
	}
	if !r.Attending() || !now.Before(deadline) {
		return done()
	}
	if l.EndFed {
		row, found, err := s.attendeeRow(ctx, room.MachineID, room.SessionID)
		if err == nil && (!found || !roomWorking(row) && !row.UpdatedAt.Before(l.FedAt)) {
			return done() // the end batch's turn ended
		}
		return feedEarliest(now.Add(feedBusyPoll), deadline)
	}
	next := s.roomFeed(ctx, e, id, l, r, now, true)
	if l2, _, err := e.LiveRead(id); err == nil && l2.EndFed && l2.FedTo == nil {
		return done() // nothing was ever said to it: no end to tell
	}
	return feedEarliest(cmp.Or(next, now.Add(feedBusyPoll)), deadline)
}

// roomWorking: the room's row says a turn runs (or is about to).
func roomWorking(row proto.Session) bool {
	return row.Status == proto.StatusWorking || row.Starting || row.State == proto.StateWorking
}

// roomAsksFallBack wakes the asks of recording id waiting for its room: it turned unavailable or ended, so they go to
// meeting.answer (meeting_room_ask.go).
func (s *Service) roomAsksFallBack(id string) {
	s.room.wake(id)
}

// meetingRoomsSweep deletes the rooms whose transcript is cut ("Deletion"), at every retention pass: a room of a
// trimmed recording, and one its origin record names for a recording that is gone, trimmed, or whose meeting.json does
// not name it (an older manager's trim or delete after a rollback removed the file). A node offline or too old for
// assistant.guest_delete is asked again at the next pass; the room stays ended meanwhile.
func (s *Service) meetingRoomsSweep(ctx context.Context, e *kb.Engine) {
	if e == nil || e.Store.ReadOnly() {
		return
	}
	type room struct{ recording, machine, session string }
	var rooms []room
	seen := map[string]bool{}
	for _, r := range e.RoomMeetings() {
		rm := r.Conv.Room
		if r.Trimmed() && rm.SessionID != "" && rm.State != kb.RoomDeleted {
			rooms = append(rooms, room{r.ID, rm.MachineID, rm.SessionID})
			seen[rm.SessionID] = true
		}
	}
	for _, o := range s.originsOf(ctx, OriginMeeting) {
		if seen[o.session] {
			continue
		}
		m, r, err := e.MeetingRead(o.ref)
		switch {
		case errors.Is(err, kb.ErrNotFound), errors.Is(err, kb.ErrRecordingDeleted):
		case err != nil:
			continue
		case r.Trimmed(), m.Room == nil, m.Room.SessionID != o.session:
		default:
			continue // its meeting's room, still within its recording's days
		}
		rooms = append(rooms, room{o.ref, o.machine, o.session})
	}
	for _, rm := range rooms {
		s.roomDelete(ctx, e, rm.recording, rm.machine, rm.session)
	}
}

// roomDelete deletes recording's room session on machine: the node removes its transcript and files, the hub forgets
// it, and meeting.json says deleted (while the recording keeps one). False when the node could not be asked.
func (s *Service) roomDelete(ctx context.Context, e *kb.Engine, recording, machine, session string) bool {
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := s.call(dctx, machine, DeviceAssistantGuestDelete, map[string]any{"dir": path.Join(meetingRoomDir, recording), "session_id": session}, nil)
	if err != nil {
		s.logf("meeting %s: delete its room: %v", short(recording), err)
		s.roomSet(e, recording, func(rm *kb.MeetingRoom) {
			if rm.SessionID == session && rm.State != kb.RoomDeleted {
				rm.State = kb.RoomEnded // a copy is still on the assistant's computer
			}
		})
		return false
	}
	s.roomSet(e, recording, func(rm *kb.MeetingRoom) {
		if rm.SessionID == session {
			rm.State, rm.Problem, rm.MachineID, rm.SessionID, rm.Pending = kb.RoomDeleted, "", "", "", false
		}
	})
	if err := s.guestDrop(ctx, session); err != nil {
		s.logf("meeting %s: forget its room: %v", short(recording), err)
	}
	s.originDrop(ctx, session)
	s.sessionsChanged(machine)
	return true
}

// roomRefusesSend refuses a message into a room that ended (POST /api/v1/sessions/{mid}/{sid}/messages): 409
// meeting_ended, the apps write in the follow-up instead. Nil for any other conversation.
func (s *Service) roomRefusesSend(ctx context.Context, machine, session, lang string) error {
	rec, ok, err := s.roomOf(ctx, machine, session)
	if err != nil || !ok {
		return nil
	}
	e, err := s.kbGet(ctx)
	if err != nil {
		return nil
	}
	m, _, err := e.MeetingRead(rec)
	if err == nil && m.Room != nil && m.Room.SessionID == session && m.Room.State != kb.RoomEnded && m.Room.State != kb.RoomDeleted {
		return nil
	}
	return &rpc.Error{Code: CodeMeetingEnded, Message: i18n.T(s.appLang(ctx, lang), "assistant-meeting.meeting_ended")}
}
