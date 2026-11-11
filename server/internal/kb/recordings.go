package kb

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Recordings (docs/assistant/knowledge.md "Recordings", docs/protocol/protocol.md "Recording sync"): the phone uploads each recording's transcript,
// keyed by its UUID, the whole state every time (revision orders them), so replays, reordering and a transcript
// completed later all land on one entry. Only text: the audio stays on the phone. A directory per recording:
// meta.json, segments.json, transcript.md (what the searches read), summary.md (the model's, once it is complete), and
// for an attended meeting live.json (live.go), for a meeting with photos photos.json, photos.md and attachments/
// (recording_photos.go). Uploads journal the accepted files until all replacements commit (recording_commit.go). Past
// recording_days a recording with minutes keeps only meta.json, its minutes and the photos they cite until
// minutes_days (recording_trim.go); any other goes.
// recordings/.deleted.json is the tombstones: a late upload of a deleted one is refused. Kept recordingDays.

// KindRecording is the recordings' kind and directory.
const KindRecording = "recordings"

// DefaultRecordingDays is how long recordings are kept unless the user says otherwise (assistant.recording_days).
const DefaultRecordingDays = 14

// DefaultMinutesDays is how long a meeting's minutes are kept unless the user says otherwise (assistant.minutes_days);
// 0 means forever; a finite window is never shorter than the recording itself (MinutesDaysFor).
const DefaultMinutesDays = 0

// Limits of one upload.
const (
	maxRecordingSegments = 20000
	maxRecordingText     = 4 << 20
)

// Defined in the contract package kbwire (docs/server/architecture.md "Contract packages"); the old names stay.
type RecordingSegment = kbwire.RecordingSegment

// RecordingBookmark is a mark the user set while recording: seconds from the start.
type RecordingBookmark struct {
	At    float64 `json:"at"`
	Label string  `json:"label,omitempty"`
}

// RecordingCalendar is the event of the phone's own calendar a recording belongs to: a hint for its name and the
// minutes' attendees and place (docs/assistant/meeting-minutes.md "Field contract").
type RecordingCalendar struct {
	Title     string   `json:"title"`
	Attendees []string `json:"attendees,omitempty"`
	Location  string   `json:"location,omitempty"`
}

// Recording kinds: a meeting gets minutes (minutes.go); a note (or none) only the summary.
const (
	RecordingNote    = "note"
	RecordingMeeting = "meeting"
)

// Limits of an upload's calendar hint.
const (
	maxCalendarTitle     = 200
	maxCalendarAttendees = 100
	maxCalendarName      = 100
)

// RecordingUpload is PUT /api/v1/recordings/{id}'s body.
type RecordingUpload struct {
	Revision  int64               `json:"revision"`
	StartedAt time.Time           `json:"started_at"`
	EndedAt   *time.Time          `json:"ended_at,omitempty"`
	Duration  float64             `json:"duration"`
	Language  string              `json:"language"`
	Device    string              `json:"device"`
	Title     string              `json:"title"`
	Bookmarks []RecordingBookmark `json:"bookmarks"`
	Segments  []RecordingSegment  `json:"segments"`
	Complete  bool                `json:"complete"`
	Kind      string              `json:"kind"`
	Calendar  *RecordingCalendar  `json:"calendar,omitempty"`
	// Attendance is the role Persona attends the meeting in ("" or AttendanceOff: none) and AttendingSession the
	// session that attends (nil: the main chat); see normalizeAttendance and live.go.
	Attendance       string      `json:"attendance,omitempty"`
	AttendingSession *SessionRef `json:"attending_session,omitempty"`
	// Private is the meeting's private mark as it starts (meeting.go): taken only while the recording has none.
	Private *bool `json:"private,omitempty"`
	// badRole, badSession: the JSON held a value of the wrong type for these two fields (UnmarshalJSON), which
	// turns attending off rather than failing the upload.
	badRole, badSession bool
	// room is the upload's room (meeting.go), nil without one; roomProblem what was wrong with its role.
	room        *roomUpload
	roomProblem string
}

// MeetingRef is the knowledge base's calendar item (meetings or events) a recording overlaps.
type MeetingRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Minutes states (meta.json's minutes_state; "" when the recording is not a meeting).
const (
	MinutesPending = "pending"
	MinutesReady   = "ready"
	MinutesFailed  = "failed"
)

// Recording is a recording's meta.json.
type Recording struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`            // the user's title, else the model's, else the start time
	Title     string              `json:"title,omitempty"` // the user's
	Revision  int64               `json:"revision"`
	StartedAt time.Time           `json:"started_at"`
	EndedAt   *time.Time          `json:"ended_at,omitempty"`
	Duration  float64             `json:"duration"`
	Language  string              `json:"language,omitempty"`
	Device    string              `json:"device"`
	Bookmarks []RecordingBookmark `json:"bookmarks,omitempty"`
	Complete  bool                `json:"complete"`
	Segments  int                 `json:"segments"`
	Chars     int                 `json:"chars"`
	Projects  []string            `json:"projects,omitempty"`
	OneLine   string              `json:"one_line,omitempty"`
	AITitle   string              `json:"ai_title,omitempty"`
	// SummarizedRev is the revision summary.md was written from (0: none yet).
	SummarizedRev int64     `json:"summarized_rev,omitempty"`
	Updated       time.Time `json:"updated"`
	// Meeting minutes (minutes.go). Kind and Calendar are the phone's, replaced by every upload; the rest is the hub's.
	Kind     string             `json:"kind,omitempty"`
	Calendar *RecordingCalendar `json:"calendar,omitempty"`
	// Attending the meeting (live.go): the phone's too, replaced by every upload. Attendance is "" when off (never
	// AttendanceOff on disk); AttendingSession is nil for the main chat, and always when attendance is off.
	Attendance       string      `json:"attendance,omitempty"`
	AttendingSession *SessionRef `json:"attending_session,omitempty"`
	// Meeting is the overlapping calendar item, matched once per revision (MatchedRev); Items links it ("events/<id>").
	Meeting      *MeetingRef `json:"meeting,omitempty"`
	Items        []string    `json:"items,omitempty"`
	MatchedRev   int64       `json:"matched_rev,omitempty"`
	MinutesState string      `json:"minutes_state,omitempty"`
	MinutesAsked bool        `json:"minutes_asked,omitempty"` // the user asked for minutes: a meeting whatever its kind
	// MinutesRev is the revision minutes.json was made from (0: none, or asked to be made again); MinutesVersion counts
	// the minutes written; MinutesFails the unusable answers for MinutesFailRev (failed after minutesTries), and
	// MinutesTimeouts its tries that ran out of time, the next not before MinutesRetryAt (minutes_timeout.go).
	MinutesRev      int64     `json:"minutes_rev,omitempty"`
	MinutesVersion  int       `json:"minutes_version,omitempty"`
	MinutesFails    int       `json:"minutes_fails,omitempty"`
	MinutesFailRev  int64     `json:"minutes_fail_rev,omitempty"`
	MinutesTimeouts int       `json:"minutes_timeouts,omitempty"`
	MinutesRetryAt  time.Time `json:"minutes_retry_at,omitzero"`
	// MinutesAsks counts the requests to make them again (the user's, and a photo added or deleted): minutes made from
	// before the latest one don't settle it. MinutesQuiet is the MinutesAsks of minutes made again only for a photo,
	// which are not pushed (photosRemake); 0 once they are written.
	MinutesAsks  int `json:"minutes_asks,omitempty"`
	MinutesQuiet int `json:"minutes_quiet,omitempty"`
	// TrimmedAt is the hub's: when its transcript went with recording_days and only meta.json and the minutes stayed
	// (recording_trim.go); nil while it is whole.
	TrimmedAt *time.Time `json:"trimmed_at,omitempty"`
	Tagging

	// Conv is the recording's meeting.json (meeting.go: its room, private mark and follow-up), read with it; nil
	// without one. Never written to meta.json.
	Conv *Meeting `json:"-"`
}

// Trimmed: the transcript went with recording_days; only the metadata and the minutes are left.
func (r *Recording) Trimmed() bool { return r.TrimmedAt != nil }

// RecordingState is a recording as the phone reconciles against it (GET /api/v1/recordings).
type RecordingState struct {
	ID           string    `json:"id"`
	Revision     int64     `json:"revision"`
	Complete     bool      `json:"complete"`
	StartedAt    time.Time `json:"started_at"`
	MinutesState string    `json:"minutes_state,omitempty"`
	// TrimmedAt: only its minutes are kept (Recording.TrimmedAt). Listed all the same, at the revision it had, so a phone
	// that still holds it sees the hub has it and sends nothing (an upload of it would be expired).
	TrimmedAt *time.Time `json:"trimmed_at,omitempty"`
}

// What PutRecording did.
const (
	RecordingStored  = "stored"
	RecordingStale   = "stale"
	RecordingExpired = "expired"
)

// RecordingPut is PutRecording's result: Status is RecordingStored, RecordingStale or RecordingExpired; Attendance the
// role in effect after it (AttendanceOff, AttendanceListener or AttendanceParticipant: the stored recording's, so the
// one already there for a stale upload and off for an expired one), and AttendanceProblem what was wrong with this
// upload's attendance fields ("" or an Attendance* problem), whatever Status is.
type RecordingPut struct {
	Status            string
	Attendance        string
	AttendanceProblem string
	// Room: the recording is a room meeting (meeting.go), or this upload asked for one; Meeting is its meeting.json
	// after it (nil without one).
	Room    bool
	Meeting *Meeting
}

var (
	// ErrRecordingDeleted: the recording was deleted (a tombstone); the phone must not upload it again.
	ErrRecordingDeleted = errors.New("this recording was deleted")
	// ErrBadRecording wraps what is wrong with an upload.
	ErrBadRecording = errors.New("invalid recording")
	uuidRe          = lazyre.New(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// RecordingID is id as stored (lowercase), false when it is not a UUID.
func RecordingID(id string) (string, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	return id, uuidRe.MatchString(id)
}

func badRecording(f string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadRecording, fmt.Sprintf(f, a...))
}

// recordingDays is days, or the default when unset.
func recordingDays(days int) int {
	if days <= 0 {
		return DefaultRecordingDays
	}
	return days
}

func (s *Store) Recording(id string) (*Recording, bool) {
	var r Recording
	raw, err := s.recordingRead(id, "meta.json")
	if err != nil || raw == nil || json.Unmarshal(raw, &r) != nil {
		return nil, false
	}
	r.persisted = true
	s.recFilesMu.RLock()
	m, err := s.readMeeting(id)
	s.recFilesMu.RUnlock()
	if err != nil {
		// Unreadable: as a room meeting whose role is off (it feeds nothing, takes no question), and private.
		m = &Meeting{Room: &MeetingRoom{State: RoomNone}}
	}
	r.Conv = m
	return &r, true
}

// Recordings lists them all (the tombstones file is not one).
func (s *Store) Recordings() []*Recording {
	var out []*Recording
	for _, id := range s.IDs(KindRecording) {
		if strings.HasPrefix(id, ".") {
			continue
		}
		if r, ok := s.Recording(id); ok {
			out = append(out, r)
		}
	}
	return out
}

// RecordingSegments reads a recording's segments.
func (s *Store) RecordingSegments(id string) []RecordingSegment {
	var segs []RecordingSegment
	raw, err := s.recordingRead(id, "segments.json")
	if err == nil {
		_ = json.Unmarshal(raw, &segs)
	}
	return segs
}

func (s *Store) tombstonesPath() string { return filepath.Join(s.Dir, KindRecording, ".deleted.json") }

func (s *Store) tombstones() map[string]time.Time {
	m := map[string]time.Time{}
	_, _ = readJSON(s.tombstonesPath(), &m)
	return m
}

// recordingName is what a recording is called in lists: the user's title, the phone calendar's, the matched calendar
// item's, the model's, else when it started.
func (s *Store) recordingName(r *Recording) string {
	switch {
	case r.Title != "":
		return r.Title
	case r.Calendar != nil && r.Calendar.Title != "":
		return r.Calendar.Title
	case r.Meeting != nil && r.Meeting.Name != "":
		return r.Meeting.Name
	case r.AITitle != "":
		return r.AITitle
	}
	return "录音 " + r.StartedAt.In(s.Zone()).Format("2006-01-02 15:04")
}

// clean checks an upload and puts its segments in order (empty ones dropped).
func (u *RecordingUpload) clean() error {
	if u.Revision <= 0 {
		return badRecording("revision must be > 0")
	}
	if u.StartedAt.IsZero() {
		return badRecording("started_at is required")
	}
	if u.Device != "phone" && u.Device != "watch" {
		return badRecording("device must be phone or watch")
	}
	if len(u.Segments) > maxRecordingSegments {
		return badRecording("at most %d segments", maxRecordingSegments)
	}
	segs := u.Segments[:0]
	total := 0
	for _, sg := range u.Segments {
		sg.Text = strings.TrimSpace(sg.Text)
		if sg.Text == "" {
			continue
		}
		total += len(sg.Text)
		segs = append(segs, sg)
	}
	if total > maxRecordingText {
		return badRecording("the transcript is over %d bytes", maxRecordingText)
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	u.Segments = segs
	sort.SliceStable(u.Bookmarks, func(i, j int) bool { return u.Bookmarks[i].At < u.Bookmarks[j].At })
	u.Title = oneLine(u.Title)
	u.Language = strings.TrimSpace(u.Language)
	switch u.Kind = strings.ToLower(strings.TrimSpace(u.Kind)); u.Kind {
	case "", RecordingNote:
		u.Kind = RecordingNote
	case RecordingMeeting:
	default:
		return badRecording("kind must be meeting or note")
	}
	return u.cleanCalendar()
}

// cleanCalendar trims the calendar hint, dropping it when it says nothing.
func (u *RecordingUpload) cleanCalendar() error {
	c := u.Calendar
	if c == nil {
		return nil
	}
	c.Title, c.Location = strings.Join(strings.Fields(c.Title), " "), strings.Join(strings.Fields(c.Location), " ")
	if len([]rune(c.Title)) > maxCalendarTitle || len([]rune(c.Location)) > maxCalendarTitle {
		return badRecording("calendar title and location are at most %d characters", maxCalendarTitle)
	}
	if len(c.Attendees) > maxCalendarAttendees {
		return badRecording("at most %d calendar attendees", maxCalendarAttendees)
	}
	names := c.Attendees[:0]
	for _, n := range c.Attendees {
		n = strings.Join(strings.Fields(n), " ")
		if len([]rune(n)) > maxCalendarName {
			return badRecording("a calendar attendee is at most %d characters", maxCalendarName)
		}
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	c.Attendees = names
	if c.Title == "" && c.Location == "" && len(c.Attendees) == 0 {
		u.Calendar = nil
	}
	return nil
}

// PutRecording stores an upload (the whole state of recording id): RecordingStale when a newer revision is there,
// RecordingExpired when it started before the retention or was trimmed, ErrRecordingDeleted after a delete.
// Attendance fields that are wrong never fail it: attending is then off, and the result says why
// (normalizeAttendance). Engine.LiveChanged hears of a stored upload of a recording that attends or attended until now.
func (e *Engine) PutRecording(rawID string, u RecordingUpload, now time.Time) (RecordingPut, error) {
	put, live, err := e.putRecording(rawID, u, now)
	if err == nil && live && e.LiveChanged != nil {
		e.LiveChanged(put.id)
	}
	return put.RecordingPut, err
}

type recordingPut struct {
	RecordingPut
	id string
}

// putRecording is PutRecording; live reports a stored upload the attending feeder should look at.
func (e *Engine) putRecording(rawID string, u RecordingUpload, now time.Time) (put recordingPut, live bool, err error) {
	put.Attendance = AttendanceOff
	put.Room = u.room != nil
	if e.Store.ReadOnly() {
		return put, false, ErrReadOnly
	}
	id, ok := RecordingID(rawID)
	if !ok {
		return put, false, badRecording("id must be a UUID")
	}
	put.id = id
	if err := u.clean(); err != nil {
		return put, false, err
	}
	put.AttendanceProblem = u.normalizeAttendance()
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	if _, gone := st.tombstones()[id]; gone {
		return put, false, ErrRecordingDeleted
	}
	if u.StartedAt.Before(now.UTC().AddDate(0, 0, -recordingDays(e.RecordingDays()))) {
		put.Status = RecordingExpired
		return put, false, nil
	}
	r, exists := st.Recording(id)
	if exists {
		put.Room, put.Meeting = put.Room || r.RoomMeeting(), r.Conv
	}
	if exists && r.Trimmed() {
		// Its transcript went with the retention (recording_days made longer since lets it back in the window): the
		// minutes stay what they are, and the hub takes no transcript back.
		put.Status = RecordingExpired
		return put, false, nil
	}
	if exists && r.Revision > u.Revision {
		put.Status, put.Attendance = RecordingStale, r.AttendanceRole()
		return put, false, nil
	}
	wasAttending := exists && r.Attending()
	meeting, parts, err := e.putMeetingFields(id, &u)
	if err != nil {
		return put, false, err
	}
	defer e.meetingChanged(id, parts)
	if meeting != nil && meeting.Room != nil {
		put.Room, put.AttendanceProblem = true, u.roomProblem
	}
	put.Meeting = meeting
	// A newer transcript (not a retry of the accepted upload): its minutes are pushed as before.
	advanced := !exists || u.Revision > r.Revision
	if !exists {
		r = &Recording{ID: id}
	}
	r.Conv = meeting
	r.Revision, r.StartedAt, r.EndedAt, r.Duration = u.Revision, u.StartedAt.UTC(), u.EndedAt, u.Duration
	if r.EndedAt != nil {
		t := r.EndedAt.UTC()
		r.EndedAt = &t
	}
	r.Language, r.Device, r.Title, r.Bookmarks, r.Complete = u.Language, u.Device, u.Title, u.Bookmarks, u.Complete
	r.Kind, r.Calendar = u.Kind, u.Calendar
	r.Attendance, r.AttendingSession = u.Attendance, u.AttendingSession // never a room meeting's (putMeetingFields)
	r.Segments, r.Chars = len(u.Segments), 0
	for _, sg := range u.Segments {
		r.Chars += len([]rune(sg.Text))
	}
	if advanced {
		r.MinutesQuiet = 0 // a retry keeps a generation made again only for a photo quiet
	}
	r.planMinutes() // after Chars: a meeting that completed with no text gets none
	r.Name = st.recordingName(r)
	r.Updated = now.UTC()
	if err := st.commitRecording(r, u.Segments); err != nil {
		return put, false, err
	}
	put.Status, put.Attendance = RecordingStored, r.AttendanceRole()
	st.notify(KindRecording, id, "metadata")
	st.notify(KindRecording, id, "messages")
	e.index(KindRecording, id, r.Name, r.Tagging, r.OneLine, r.StartedAt)
	if r.EndedAt != nil {
		// The end batch is timed from when the hub first saw the end (live.go); the upload stands either way, and the
		// next one carrying ended_at tries again.
		if err := st.liveEnded(id, r.Attending() || r.RoomMeeting(), now); err != nil {
			e.logf("recording %s: live end: %v", id, err)
		}
	}
	return put, wasAttending || r.Attending() || r.RoomMeeting(), nil
}

func nonNilSegments(s []RecordingSegment) []RecordingSegment {
	if s == nil {
		return []RecordingSegment{}
	}
	return s
}

// renderTranscript is transcript.md: a line per segment (and bookmark) at its clock time in the user's zone.
func (s *Store) renderTranscript(r *Recording, segs []RecordingSegment) string {
	zone := s.Zone()
	at := func(sec float64) string {
		return r.StartedAt.Add(time.Duration(sec * float64(time.Second))).In(zone).Format("15:04:05")
	}
	type line struct {
		sec  float64
		text string
	}
	var lines []line
	for _, sg := range segs {
		lines = append(lines, line{sg.Start, "- " + at(sg.Start) + " " + strings.Join(strings.Fields(sg.Text), " ")})
	}
	for _, b := range r.Bookmarks {
		lines = append(lines, line{b.At, "- " + at(b.At) + " [书签] " + oneLine(b.Label)})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].sec < lines[j].sec })
	var b strings.Builder
	b.WriteString("# 录音 · ")
	if t := cmp.Or(r.Title, r.AITitle); t != "" {
		b.WriteString(t + " · ")
	}
	fmt.Fprintf(&b, "%s · %s\n\n", r.StartedAt.In(zone).Format("2006-01-02 15:04"), zone)
	for _, l := range lines {
		b.WriteString(strings.TrimRight(l.text, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// DeleteRecording removes recording id (files and index) and leaves a tombstone; false when there was none.
func (e *Engine) DeleteRecording(rawID string, now time.Time) (bool, error) {
	if e.Store.ReadOnly() {
		return false, ErrReadOnly
	}
	id, ok := RecordingID(rawID)
	if !ok {
		return false, badRecording("id must be a UUID")
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	t := st.tombstones()
	if _, ok := t[id]; !ok {
		t[id] = now.UTC()
		if err := writeJSON(st.tombstonesPath(), t); err != nil {
			return false, err
		}
	}
	r, existed := st.Recording(id)
	if !existed {
		return false, nil
	}
	st.recFilesMu.Lock()
	err := os.RemoveAll(st.path(KindRecording, id))
	st.recFilesMu.Unlock()
	if err != nil {
		return false, err
	}
	e.unlinkRecording(id, r.Projects)
	e.unindex(KindRecording, []string{id})
	st.notify(KindRecording, id, "metadata")
	return true, nil
}

// unlinkRecording takes a gone recording off its projects.
func (e *Engine) unlinkRecording(id string, projects []string) {
	for _, slug := range projects {
		if p, ok := e.Store.Project(slug); ok {
			p.Items = removeStrings(p.Items, []string{KindRecording + "/" + id})
			_ = e.Store.putEntity(KindProject, p.Slug, p, &p.Tagging, false)
		}
	}
}

// RecordingStates is every recording kept (trimmed ones too) and every tombstone, for the phone to reconcile.
func (e *Engine) RecordingStates() (kept []RecordingState, deleted []string) {
	kept, deleted = []RecordingState{}, []string{}
	for _, r := range e.Store.Recordings() {
		kept = append(kept, RecordingState{ID: r.ID, Revision: r.Revision, Complete: r.Complete, StartedAt: r.StartedAt,
			MinutesState: r.MinutesState, TrimmedAt: r.TrimmedAt})
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].StartedAt.After(kept[j].StartedAt) })
	for id := range e.Store.tombstones() {
		deleted = append(deleted, id)
	}
	sort.Strings(deleted)
	return kept, deleted
}

// PruneRecordings applies retention to files and the index. Past recording_days, minutes are kept forever or within
// a finite minutes_days window, and the recording is trimmed to them (recording_trim.go); other recordings go.
// Interrupted trims finish whatever the window. Tombstones older than recording_days go too.
// It returns how many recordings were deleted, not counting trimmed ones.
func (e *Engine) PruneRecordings(now time.Time) int {
	if e.Store.ReadOnly() {
		return 0
	}
	cut := now.UTC().AddDate(0, 0, -recordingDays(e.RecordingDays()))
	minutesDays := e.MinutesDays()
	minutesCut := now.UTC().AddDate(0, 0, -minutesDays)
	st := e.Store
	// The trims' photo changes are told once recMu is let go (deferred calls run last first).
	var photos []string
	defer func() {
		for _, id := range photos {
			e.meetingChanged(id, []string{PartPhotos})
		}
	}()
	st.recMu.Lock()
	defer st.recMu.Unlock()
	var gone []string
	for _, r := range st.Recordings() {
		if !r.StartedAt.Before(cut) {
			// Within recording_days only a trimmed one has anything to do: the files an interrupted trim left (a crash
			// after its meta.json, a failed removal) go even once recording_days made longer lets it back in the window.
			if r.Trimmed() && e.trimPruned(r, now) {
				photos = append(photos, r.ID)
			}
			continue
		}
		if (minutesDays == 0 || !r.StartedAt.Before(minutesCut)) && (r.Trimmed() || st.hasMinutes(r.ID)) {
			if e.trimPruned(r, now) {
				photos = append(photos, r.ID)
			}
			continue
		}
		st.recFilesMu.Lock()
		err := os.RemoveAll(st.path(KindRecording, r.ID))
		st.recFilesMu.Unlock()
		if err == nil {
			e.unlinkRecording(r.ID, r.Projects)
			st.notify(KindRecording, r.ID, "metadata")
			gone = append(gone, r.ID)
		}
	}
	e.unindex(KindRecording, gone)
	// Tombstones keep the recording_days window, not minutes_days. One only has to outlive the uploads it refuses, and
	// an upload of a recording that started before that window is expired anyway (putRecording), whether the recording
	// is gone, trimmed or deleted by the user. A tombstone is never older than its recording's start (it is written when
	// the recording is deleted), so one that falls out of the window stands for a recording past it: keeping it longer,
	// as long as a trimmed recording could have stayed, would refuse nothing more. Its other use, the phone dropping a
	// copy deleted elsewhere, ends with the window too: the phone drops every recording older than days itself.
	t := st.tombstones()
	n := len(t)
	for id, at := range t {
		if at.Before(cut) {
			delete(t, id)
		}
	}
	if len(t) != n {
		_ = writeJSON(st.tombstonesPath(), t)
	}
	return len(gone)
}

// trimPruned trims recording r for PruneRecordings (which holds recMu), or finishes its trim: idempotent, it says
// what changed and indexes the recording again only when something did. photos: PartPhotos' revision moved (the
// caller tells MeetingChanged once it let recMu go).
func (e *Engine) trimPruned(r *Recording, now time.Time) (photos bool) {
	st := e.Store
	trimmed, photos, err := st.trimRecording(r, now)
	if err != nil {
		e.logf("recording %s: trim: %v", r.ID, err)
	} else if trimmed {
		st.notify(KindRecording, r.ID, "metadata")
		st.notify(KindRecording, r.ID, "messages")
		st.notify(KindRecording, r.ID, "summary")
		e.index(KindRecording, r.ID, r.Name, r.Tagging, r.OneLine, r.StartedAt)
	}
	return photos
}

// RecordingDays is the retention in days (Engine.RecordingDaysFunc, the assistant's setting), the default without one.
func (e *Engine) RecordingDays() int {
	if e.RecordingDaysFunc != nil {
		return recordingDays(e.RecordingDaysFunc())
	}
	return DefaultRecordingDays
}

// MinutesDays is how many days a meeting's minutes are kept (Engine.MinutesDaysFunc, the assistant's setting, the
// default without one): 0 means forever; otherwise never fewer than RecordingDays.
func (e *Engine) MinutesDays() int {
	d := 0
	if e.MinutesDaysFunc != nil {
		d = e.MinutesDaysFunc()
	}
	return MinutesDaysFor(d, e.RecordingDays())
}

// MinutesDaysFor is the minutes' retention in effect for the setting minutes (<= 0: DefaultMinutesDays) and the
// recordings' days: 0 means forever; minutes never go before the recording they belong to.
func MinutesDaysFor(minutes, recording int) int {
	if minutes <= 0 {
		return DefaultMinutesDays
	}
	return max(minutes, recordingDays(recording))
}

func (e *Engine) unindex(kind string, ids []string) {
	if e.Unindex != nil && len(ids) > 0 {
		e.Unindex(kind, ids)
	}
}

// recordingSummary is the model's answer for a recording (kb/recording).
type recordingSummary struct {
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	OneLine    string     `json:"one_line"`
	Importance string     `json:"importance"`
	Relevance  modelScore `json:"relevance"`
	Tags       []string   `json:"tags"`
	Projects   []struct {
		Name   string `json:"name"`
		Update string `json:"update"`
	} `json:"projects"`
}

// recordingPromptChars bounds the transcript the model reads (the start and the end kept).
const recordingPromptChars = 24000

// SummarizeRecordings writes the summary and tags of complete recordings whose latest revision has none yet, newest
// first, at most max model calls per run (failed ones included; one that needed no call, changed meanwhile, costs none);
// how many it did. A recording whose summary failed waits like a project whose rewrite failed (retryWait) and the next
// one goes on, so one transcript the model keeps failing on neither costs a call every round nor holds back the others;
// a new revision of it is tried at once. A paused connection or no model set up stops the run without counting: the
// rest stay due.
func (e *Engine) SummarizeRecordings(ctx context.Context, max int) int {
	if e.Model == nil && e.ModelFor == nil {
		return 0
	}
	now := time.Now()
	var todo []*Recording
	due := map[string]bool{}
	for _, r := range e.Store.Recordings() {
		if r.Complete && r.SummarizedRev < r.Revision && r.Chars > 0 && !r.Trimmed() {
			due[r.ID] = true
			if e.retries().recordings.waits(r.ID, recordingAbout(r), now) {
				continue
			}
			todo = append(todo, r)
		}
	}
	e.retries().recordings.keep(due) // summarized, trimmed or gone in the meantime
	sort.Slice(todo, func(i, j int) bool { return todo[i].StartedAt.After(todo[j].StartedAt) })
	n, calls := 0, 0
	for _, r := range todo {
		if calls >= max || ctx.Err() != nil {
			break
		}
		called, err := e.summarizeRecording(ctx, r)
		if called {
			calls++
		}
		if err != nil {
			if deferred(ctx, err) { // the connection is held, no model, or the run ended: they stay due
				break
			}
			f := e.retries().recordings.fail(r.ID, recordingAbout(r))
			e.logf("recording %s: %v (failed %d time(s) in a row, next try in %s)", r.ID, err, f.failures, retryWait(f.failures))
			continue
		}
		e.retries().recordings.clear(r.ID)
		if called {
			n++
		}
	}
	return n
}

// recordingAbout is what a recording's summary is asked about: its revision (a new upload is tried at once).
func recordingAbout(r *Recording) string { return strconv.FormatInt(r.Revision, 10) }

// summarizeRecording writes r's summary; called: the model was asked (not when r changed meanwhile so that it needs
// none, or could not be read).
func (e *Engine) summarizeRecording(ctx context.Context, r *Recording) (called bool, err error) {
	files, err := e.Store.recordingSnapshot(r.ID)
	if err != nil {
		return false, err
	}
	var snapshot Recording
	if files["meta.json"] == nil {
		return false, nil
	}
	if err := json.Unmarshal(files["meta.json"], &snapshot); err != nil {
		return false, err
	}
	r = &snapshot
	if !r.Complete || r.Chars == 0 || r.SummarizedRev >= r.Revision || r.Trimmed() {
		return false, nil
	}
	var segs []RecordingSegment
	if err := json.Unmarshal(files["segments.json"], &segs); err != nil {
		return false, err
	}
	called = true
	zone := e.Store.Zone()
	in := prompts.KBRecordingInput{Pinned: r.Pinned, Title: r.Title, Device: r.Device, Language: r.Language,
		Start: r.StartedAt.In(zone), Minutes: int(r.Duration / 60), Projects: e.knownProjects(), Tags: e.TopTags(40),
		Transcript: clipMiddle(e.Store.renderTranscript(r, segs), recordingPromptChars)}
	var out recordingSummary
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	err = e.modelFor("kb.compress").JSONMax(cctx, prompts.RenderKBRecording(prompts.KBOwner{Owner: e.ownerName()}), prompts.RenderKBRecordingInput(in), 4000, &out)
	cancel()
	if err != nil {
		return true, err
	}
	st := e.Store
	st.recMu.Lock()
	defer st.recMu.Unlock()
	cur, ok := st.Recording(r.ID)
	if !ok || cur.Trimmed() {
		return true, nil // deleted or trimmed meanwhile: no summary of a transcript that is gone
	}
	now := time.Now().UTC()
	if s := strings.TrimSpace(out.Summary); s != "" && !cur.fieldLocked("summary") {
		if err := st.putSummary(KindRecording, cur.ID, s+"\n", false); err != nil {
			return true, err
		}
	}
	applyTags(&cur.Tagging, out.Importance, int(out.Relevance), out.Tags, now)
	cur.OneLine = oneLine(out.OneLine)
	if t := oneLine(out.Title); t != "" {
		cur.AITitle = t
	}
	cur.Name = st.recordingName(cur)
	// It summarized r's revision: a newer upload meanwhile is summarized again.
	cur.SummarizedRev = r.Revision
	for _, p := range out.Projects {
		pr := e.FindProject(p.Name)
		if pr == nil || !pr.Live() {
			continue
		}
		cur.Projects = addUnique(cur.Projects, pr.Slug)
		pr.Items = addUnique(pr.Items, KindRecording+"/"+cur.ID)
		if u := oneLine(p.Update); u != "" {
			_ = st.AppendLog(pr.Slug, now, "录音 · "+cur.Name, u)
			pr.Dirty = true
		}
		_ = st.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, false)
	}
	if err := st.putEntity(KindRecording, cur.ID, cur, &cur.Tagging, false); err != nil {
		return true, err
	}
	e.index(KindRecording, cur.ID, cur.Name, cur.Tagging, cur.OneLine, cur.StartedAt)
	return true, nil
}

// clipMiddle keeps the first and last of s within n runes, marking the cut.
func clipMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := n * 2 / 3
	return string(r[:head]) + "\n…（中间省略）…\n" + string(r[len(r)-(n-head):])
}
