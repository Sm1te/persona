package kb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Attending a meeting (docs/assistant/meeting-minutes.md "Attending a meeting"): a meeting recording uploaded with a
// role (listener or participant) gets a live log, the finished sentences the phone appends by position while it
// records (POST /api/v1/recordings/{id}/live), which the assistant feeds to the attending session and answers the
// room's questions from. It is live.json in the recording's directory: the sentences by position, what was fed to
// which session, and the questions asked with their answers. The upload still owns the transcript; the live log is
// the hub's, so it is not in an upload's undo journal (recordingFiles). Never searched (find.go searchable) and never
// shown by Get; a delete or the retention takes it with the directory.

// Attendance roles. Off is stored as "" (omitted in meta.json).
const (
	AttendanceOff         = "off"
	AttendanceListener    = "listener"
	AttendanceParticipant = "participant"
)

// What was wrong with an upload's attendance fields (RecordingPut.AttendanceProblem); attending is off then.
const (
	AttendanceUnknownRole = "unknown_role" // attendance is not off, listener or participant
	AttendanceNotMeeting  = "not_meeting"  // a role on a recording whose kind is not meeting
	AttendanceBadSession  = "bad_session"  // attending_session has exactly one id, or one over maxSessionIDRunes
)

// Limits of attending.
const (
	maxSessionIDRunes = 200
	// MaxLiveRequest is how many sentences one POST …/live carries at most (the HTTP layer answers 400 above it;
	// LiveAppend refuses them too).
	MaxLiveRequest   = 200
	maxLiveText      = 4000 // bytes of one sentence: longer ones are cut at a character boundary
	maxLivePositions = 20000
	maxLiveBytes     = 4 << 20 // of live text in all
	// What MeetingMaterial gives of the live sentences, and of earlier meetings.
	materialLiveRunes  = 12000
	materialMaxEarlier = 6
)

const liveFile = "live.json"

// SessionRef names one session the way the rest of the API does.
type SessionRef struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
}

// LiveSegment is one finished sentence of the live log: seconds from the recording's start, as the upload's
// segments. Text "" is a placeholder position (an empty sentence), never fed.
type LiveSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// AskSource is an earlier meeting (kind recordings) or a project (kind projects) an answer drew on; When, Start and End
// are the meeting's minutes' (#4713: clients format Start / End and show When only without them).
type AskSource struct {
	Kind  string    `json:"kind"`
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	When  string    `json:"when,omitempty"`
	Start time.Time `json:"start,omitzero"`
	End   time.Time `json:"end,omitzero"`
}

// LiveAsk is a question asked during the meeting (POST /api/v1/recordings/{id}/ask) and its answer.
type LiveAsk struct {
	ID       string `json:"id"`
	Question string `json:"question"`
	// QuestionSent is the question as the request had it ("" without one), what a retry with the same id is matched on.
	QuestionSent   string      `json:"question_sent,omitempty"`
	QuestionSource string      `json:"question_source"`
	At             *float64    `json:"at,omitempty"`
	LiveNext       *int        `json:"live_next,omitempty"`
	State          string      `json:"state"` // pending | answered | failed
	Answer         string      `json:"answer"`
	Lang           string      `json:"lang"`
	Speak          bool        `json:"speak"`
	Sources        []AskSource `json:"sources"`
	Error          string      `json:"error,omitempty"`
	AskedAt        time.Time   `json:"asked_at"`
	AnsweredAt     *time.Time  `json:"answered_at,omitempty"`
	// Fed is the hub's: the answer already went to the attending session in a batch.
	Fed bool `json:"fed,omitempty"`
	// Room meetings (docs/assistant/meeting-minutes.md "Asking the room"). Via is the way it is answered (AskViaRoom or
	// AskViaMaterial), fixed when its first attempt goes out ("" before); Spoken the request's spoken (a Talk span);
	// Heard the lines a heard question was made of; Attempts the messages it sent the room, the latest last.
	Via      string        `json:"via,omitempty"`
	Spoken   bool          `json:"spoken,omitempty"`
	Heard    []LiveSegment `json:"heard,omitempty"`
	Attempts []AskAttempt  `json:"attempts,omitempty"`
}

// The ways an ask is answered (LiveAsk.Via).
const (
	AskViaRoom     = "room"
	AskViaMaterial = "material"
)

// AskAttempt is one message a room ask sent the room: its attempt number k (1 for the first), its client_msg_id
// (ask-<id>-<k>), the room's last line before it (where its turn is read from) and when it went out.
type AskAttempt struct {
	K      int       `json:"k"`
	MsgID  string    `json:"msg_id"`
	Base   int       `json:"base"`
	SentAt time.Time `json:"sent_at,omitzero"`
}

// Live is live.json: the live log of an attended meeting and what became of it.
type Live struct {
	Segments []LiveSegment `json:"segments"` // index = position
	Bytes    int           `json:"bytes"`    // the live text's bytes in all (maxLiveBytes)
	Fed      int           `json:"fed"`      // positions given to FedTo at least once
	// FedTo is the session last fed (resolved: the main chat by its real ids), FedAt when.
	FedTo *SessionRef `json:"fed_to,omitempty"`
	FedAt time.Time   `json:"fed_at,omitzero"`
	// EndedAt is when the hub first saw an upload carrying ended_at (PutRecording); EndFed that the end batch went out.
	EndedAt time.Time `json:"ended_at,omitzero"`
	EndFed  bool      `json:"end_fed,omitempty"`
	// Problem is the last feeding problem (attendee_unavailable, attendee_not_allowed), the feeder's.
	Problem string `json:"problem,omitempty"`
	// Full: sentences were refused for the limits (ErrLiveFull), so the phone stopped sending them (problem live_full).
	Full bool      `json:"full,omitempty"`
	Asks []LiveAsk `json:"asks,omitempty"`
	// AsksRev is the asks' revision (the meeting_live event's asks): it goes up whenever they change (LiveUpdate).
	AsksRev int64 `json:"asks_rev,omitempty"`
}

var (
	// ErrNotAttending: the stored upload has attendance off.
	ErrNotAttending = errors.New("Persona does not attend this recording")
	// ErrLiveGap: from is above the positions held; a LiveGapError says how many there are.
	ErrLiveGap = errors.New("live sentences are missing before from")
	// ErrLiveFull: the live log reached maxLivePositions or maxLiveBytes; nothing of the request was stored.
	ErrLiveFull = errors.New("the live log of this recording is full")
	// ErrBadLive wraps what is wrong with a live request (a negative from, too many sentences).
	ErrBadLive = errors.New("invalid live sentences")
)

// LiveGapError is ErrLiveGap with how many positions the hub holds: the phone sends again from Next. errors.As finds
// it as a LiveGapError and as a *LiveGapError.
type LiveGapError struct{ Next int }

func (e LiveGapError) Error() string { return fmt.Sprintf("%v: send from %d", ErrLiveGap, e.Next) }
func (e LiveGapError) Unwrap() error { return ErrLiveGap }

func (e LiveGapError) As(target any) bool {
	if p, ok := target.(**LiveGapError); ok {
		c := e
		*p = &c
		return true
	}
	return false
}

// AttendanceRole is the role r is attended in: AttendanceOff, AttendanceListener or AttendanceParticipant. A room
// meeting's is its room's (meeting.json), never meta.json's; a trimmed recording is attended no more.
func (r Recording) AttendanceRole() string {
	role := r.Attendance
	if r.RoomMeeting() {
		role = r.Conv.Room.Attendance
	}
	if role == "" || r.TrimmedAt != nil {
		return AttendanceOff
	}
	return role
}

// UnmarshalJSON reads an upload like the default does, except that a value of the wrong type for attendance or
// attending_session does not fail it (the same request carries the transcript): normalizeAttendance turns attending
// off for it.
func (u *RecordingUpload) UnmarshalJSON(data []byte) error {
	type plain RecordingUpload
	aux := struct {
		*plain
		Attendance       json.RawMessage `json:"attendance"`
		AttendingSession json.RawMessage `json:"attending_session"`
		Room             json.RawMessage `json:"room"`
		Private          json.RawMessage `json:"private"`
	}{plain: (*plain)(u)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	u.readRoomAndPrivate(aux.Room, aux.Private)
	u.Attendance, u.AttendingSession, u.badRole, u.badSession = "", nil, false, false
	if raw := aux.Attendance; !jsonNull(raw) {
		u.badRole = json.Unmarshal(raw, &u.Attendance) != nil
	}
	if raw := aux.AttendingSession; !jsonNull(raw) {
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) != nil {
			u.badSession = true
			return nil
		}
		ref := &SessionRef{}
		for key, dst := range map[string]*string{"machine_id": &ref.MachineID, "session_id": &ref.SessionID} {
			if v := keys[key]; !jsonNull(v) && json.Unmarshal(v, dst) != nil {
				u.badSession = true
			}
		}
		if !u.badSession {
			u.AttendingSession = ref
		}
	}
	return nil
}

// jsonNull: a field absent or null.
func jsonNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}

// normalizeAttendance settles the attendance fields of an upload clean accepted (its Kind is set), never failing it:
// an unknown role, a role on a recording that is not a meeting, or a session with exactly one id or one over
// maxSessionIDRunes leaves attending off, and the problem says which. Off is "" with no session; a session with no
// ids is nil (the main chat). Watch recordings are not refused here: the phone never gives them a role.
func (u *RecordingUpload) normalizeAttendance() (problem string) {
	role := strings.ToLower(strings.TrimSpace(u.Attendance))
	session := u.AttendingSession
	u.Attendance, u.AttendingSession = "", nil
	switch {
	case u.badRole:
		return AttendanceUnknownRole
	case role == "" || role == AttendanceOff:
		return ""
	case role != AttendanceListener && role != AttendanceParticipant:
		return AttendanceUnknownRole
	case u.Kind != RecordingMeeting:
		return AttendanceNotMeeting
	case u.badSession:
		return AttendanceBadSession
	}
	if session != nil {
		ref := SessionRef{MachineID: strings.TrimSpace(session.MachineID), SessionID: strings.TrimSpace(session.SessionID)}
		switch {
		case (ref.MachineID == "") != (ref.SessionID == ""),
			utf8.RuneCountInString(ref.MachineID) > maxSessionIDRunes, utf8.RuneCountInString(ref.SessionID) > maxSessionIDRunes:
			return AttendanceBadSession
		case ref.MachineID != "":
			u.AttendingSession = &ref
		}
	}
	u.Attendance = role
	return ""
}

// readLive is recording id's live.json; an empty Live when there is none yet.
func (s *Store) readLive(id string) (Live, error) {
	var l Live
	raw, err := os.ReadFile(s.path(KindRecording, id, liveFile))
	if os.IsNotExist(err) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return Live{}, fmt.Errorf("live log of %s: %w", id, err)
	}
	return l, nil
}

// putLive writes recording id's live.json as putMinutes writes the minutes: not when the recording was deleted or
// trimmed meanwhile (false then), so a delete stays a delete and a trim keeps no live log. The trim (recording_trim.go)
// holds recFilesMu from its meta.json to its last removal, so a read-modify-write that read the live log before it
// cannot write it back after. The caller holds liveMu.
func (s *Store) putLive(id string, l *Live) (bool, error) {
	if s.readOnly {
		return false, ErrReadOnly
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return false, err
	}
	s.recFilesMu.Lock()
	defer s.recFilesMu.Unlock()
	if err := s.recoverRecordingLocked(id); err != nil {
		return false, err
	}
	meta, err := os.ReadFile(s.path(KindRecording, id, "meta.json"))
	if err != nil {
		return false, nil
	}
	var trim struct {
		TrimmedAt *time.Time `json:"trimmed_at"`
	}
	if json.Unmarshal(meta, &trim) == nil && trim.TrimmedAt != nil {
		return false, nil
	}
	// Synced before it replaces the old one: the phone was told next, and does not send those positions again.
	return true, writeRecordingFile(s.path(KindRecording, id, liveFile), append(raw, '\n'))
}

// liveState is recording id (a stored id) with its live log: ErrRecordingDeleted after a delete, ErrNotFound with
// no upload of it.
func (s *Store) liveState(id string) (Live, Recording, error) {
	if _, gone := s.tombstones()[id]; gone {
		return Live{}, Recording{}, ErrRecordingDeleted
	}
	r, ok := s.Recording(id)
	if !ok {
		return Live{}, Recording{}, ErrNotFound
	}
	l, err := s.readLive(id)
	return l, *r, err
}

// liveEnded notes in recording id's live log when the hub first saw it ended, for a recording that attends
// (attending) or has a live log already. The caller holds recMu.
func (s *Store) liveEnded(id string, attending bool, now time.Time) error {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	if !attending {
		if _, err := os.Stat(s.path(KindRecording, id, liveFile)); err != nil {
			return nil
		}
	}
	l, err := s.readLive(id)
	if err != nil || !l.EndedAt.IsZero() {
		return err
	}
	l.EndedAt = now.UTC()
	_, err = s.putLive(id, &l)
	return err
}

// cleanLive is a sentence as the live log keeps it: a bad one is mended, never refused, since positions stay
// contiguous. A negative start is 0, an end before the start the start; the text is one line, cut to maxLiveText bytes
// at a character boundary.
func cleanLive(sg LiveSegment) LiveSegment {
	if math.IsNaN(sg.Start) || math.IsInf(sg.Start, 0) || sg.Start < 0 {
		sg.Start = 0
	}
	if math.IsNaN(sg.End) || math.IsInf(sg.End, 0) || sg.End < sg.Start {
		sg.End = sg.Start
	}
	sg.Text = cutBytes(strings.Join(strings.Fields(strings.ToValidUTF8(sg.Text, "�")), " "), maxLiveText)
	return sg
}

// cutBytes is s within n bytes, cut at a character boundary.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// LiveAppend stores sentences of recording id's live log, segs[0] at position from: the positions held already are
// skipped (a replay or a retry stores nothing twice) and the rest appended. Errors in the contract's order:
// ErrRecordingDeleted, ErrNotFound (no upload yet), ErrReadOnly, ErrBadLive (a negative from, over MaxLiveRequest
// sentences), ErrNotAttending (the stored upload has attendance off), a LiveGapError (from above the positions held),
// ErrLiveFull (the request would pass maxLivePositions or maxLiveBytes; nothing of it is stored). The Live returned is
// the log after it, also with ErrLiveGap and ErrLiveFull. Engine.LiveChanged hears of every append.
func (e *Engine) LiveAppend(rawID string, from int, segs []LiveSegment) (Live, Recording, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return Live{}, Recording{}, ErrNotFound
	}
	l, r, stored, err := e.liveAppend(id, from, segs)
	if stored && e.LiveChanged != nil {
		e.LiveChanged(id)
	}
	return l, r, err
}

func (e *Engine) liveAppend(id string, from int, segs []LiveSegment) (Live, Recording, bool, error) {
	st := e.Store
	st.liveMu.Lock()
	defer st.liveMu.Unlock()
	l, r, err := st.liveState(id)
	switch {
	case err != nil:
		return l, r, false, err
	case st.ReadOnly():
		return l, r, false, ErrReadOnly
	case from < 0:
		return l, r, false, fmt.Errorf("%w: from must be 0 or more", ErrBadLive)
	case len(segs) > MaxLiveRequest:
		return l, r, false, fmt.Errorf("%w: at most %d sentences a request", ErrBadLive, MaxLiveRequest)
	case !r.Attending():
		return l, r, false, ErrNotAttending
	case from > len(l.Segments):
		return l, r, false, LiveGapError{Next: len(l.Segments)}
	}
	fresh := segs[min(len(l.Segments)-from, len(segs)):]
	if len(fresh) == 0 {
		return l, r, false, nil
	}
	add, size := make([]LiveSegment, len(fresh)), 0
	for i, sg := range fresh {
		add[i] = cleanLive(sg)
		size += len(add[i].Text)
	}
	if len(l.Segments)+len(add) > maxLivePositions || l.Bytes+size > maxLiveBytes {
		if !l.Full {
			l.Full = true
			if _, err := st.putLive(id, &l); err != nil {
				e.logf("recording %s: live full: %v", id, err)
			}
		}
		return l, r, false, ErrLiveFull
	}
	next := l
	next.Segments = append(append(make([]LiveSegment, 0, len(l.Segments)+len(add)), l.Segments...), add...)
	next.Bytes += size
	if ok, err := st.putLive(id, &next); err != nil {
		return l, r, false, err
	} else if !ok {
		return l, r, false, st.goneError(id)
	}
	return next, r, true, nil
}

// goneError is why recording id takes no live log any more: deleted, trimmed (attending it is over) or never uploaded.
func (s *Store) goneError(id string) error {
	if _, gone := s.tombstones()[id]; gone {
		return ErrRecordingDeleted
	}
	if r, ok := s.Recording(id); ok && r.Trimmed() {
		return ErrNotAttending
	}
	return ErrNotFound
}

// LiveRead is recording id's live log (empty before any) and its stored upload, also on a read-only knowledge base:
// ErrRecordingDeleted after a delete, ErrNotFound with no upload of it.
func (e *Engine) LiveRead(rawID string) (Live, Recording, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return Live{}, Recording{}, ErrNotFound
	}
	return e.Store.liveState(id)
}

// LiveUpdate changes recording id's live log with fn (given it and the stored upload) and writes the result unless fn
// fails or changed nothing; the live log after it. ErrNotFound when the recording's meta.json is gone (also deleted
// meanwhile) or the recording was trimmed meanwhile, ErrReadOnly on a read-only knowledge base. fn runs with the live log locked: it must be quick and call
// no Engine method of the live log or the recordings.
func (e *Engine) LiveUpdate(rawID string, fn func(l *Live, r Recording) error) (Live, error) {
	id, ok := RecordingID(rawID)
	if !ok {
		return Live{}, ErrNotFound
	}
	l, asks, err := e.liveUpdate(id, fn)
	if asks {
		e.meetingChanged(id, []string{PartAsks})
	}
	return l, err
}

func (e *Engine) liveUpdate(id string, fn func(l *Live, r Recording) error) (Live, bool, error) {
	st := e.Store
	st.liveMu.Lock()
	defer st.liveMu.Unlock()
	r, ok := st.Recording(id)
	if !ok {
		return Live{}, false, ErrNotFound
	}
	if st.ReadOnly() {
		return Live{}, false, ErrReadOnly
	}
	l, err := st.readLive(id)
	if err != nil {
		return Live{}, false, err
	}
	before, err := json.Marshal(l)
	if err != nil {
		return Live{}, false, err
	}
	asksBefore, _ := json.Marshal(l.Asks)
	if err := fn(&l, *r); err != nil {
		return Live{}, false, err
	}
	after, err := json.Marshal(l)
	if err != nil || bytes.Equal(before, after) {
		return l, false, err
	}
	asksAfter, _ := json.Marshal(l.Asks)
	asks := !bytes.Equal(asksBefore, asksAfter)
	if asks {
		l.AsksRev++
	}
	if ok, err := st.putLive(id, &l); err != nil {
		return Live{}, false, err
	} else if !ok {
		return Live{}, false, ErrNotFound
	}
	return l, asks, nil
}

// AttendingRecordings is every recording whose stored attendance is not off and whose end batch has not gone out,
// oldest first: what the feeder picks up again after a restart.
func (e *Engine) AttendingRecordings() []Recording {
	var out []Recording
	for _, r := range e.Store.Recordings() {
		if !r.Attending() {
			continue
		}
		if l, err := e.Store.readLive(r.ID); err == nil && l.EndFed {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}
