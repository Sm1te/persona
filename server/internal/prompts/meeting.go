package prompts

// The answers Persona gives during a meeting it attends (meeting/, docs/assistant/meeting-minutes.md "What the hub
// gives the answering model"): one model call with no tools. The system prompt follows the material policy; the input holds only the
// meeting material the caller built (kb.MeetingMaterial), every text of it other people's words.

// MeetingAnswer is the system prompt of an answer (meeting/answer): it takes the owner's name, today's date in
// the user's zone, the answer's language (a BCP 47 tag) and whether earlier meetings may be used, never the
// assistant's notes, memory or snapshot.
type MeetingAnswer struct {
	Owner, Today, Language string
	EarlierAvailable       bool
}

func RenderMeetingAnswer(d MeetingAnswer) string { return render("meeting/answer", d) }

// MeetingAnswerInput is the material of one answer (meeting/answer_input). Lines are this meeting's live sentences so
// far, Asks the questions already answered in it, Photos the words of the photos taken in it, Earlier the earlier
// meetings' minutes. Question is the typed one; without it the question is in Heard, the lines of the 45 seconds
// before the tap at HeardAt. Times are clock strings from the recording's start; the caller puts every text on one
// line.
type MeetingAnswerInput struct {
	Title, Language   string
	Calendar          *MeetingCalendar
	Lines             []MeetingLine
	Asks              []MeetingQA
	Photos            []MeetingPhoto
	EarlierAvailable  bool
	Earlier           []MeetingEarlier
	Question, HeardAt string
	Heard             []MeetingLine
}

// MeetingCalendar is the phone's calendar hint of this meeting.
type MeetingCalendar struct {
	Title, Location string
	Attendees       []string
}

// MeetingLine is a live sentence and when it was said.
type MeetingLine struct{ At, Text string }

// MeetingQA is a question already answered in this meeting.
type MeetingQA struct{ Question, Answer string }

// MeetingPhoto is the words of a photo taken in the meeting, never the image: its number, its time in the meeting, the
// owner's caption, the text the phone recognized in it and the room's note on it (each "" when there is none).
type MeetingPhoto struct {
	N                       int
	At, Caption, Text, Note string
}

// MeetingEarlier is an earlier meeting's minutes (its id is what the answer's sources name).
type MeetingEarlier struct {
	ID, Title, When string
	Attendees       []string
	Points          []MeetingPoint
	Decisions, Open []string
	Actions         []MeetingAction
	Projects        []MeetingProject
}

// MeetingPoint is a point of earlier minutes and who made it.
type MeetingPoint struct{ Text, Who string }

// MeetingAction is an action item of earlier minutes as said; Todo is its to-do's state: open, done, suggestion (never
// made a to-do) or "" (not known).
type MeetingAction struct{ Who, What, By, DueDate, Todo string }

// MeetingProject is a project an earlier meeting belongs to: its slug (what the answer's sources name), name and status.
type MeetingProject struct{ Slug, Name, Status string }

func RenderMeetingAnswerInput(in MeetingAnswerInput) string {
	return render("meeting/answer_input", in)
}

// MeetingRoomClaudeMD is a meeting room's CLAUDE.md (meeting/room_claude): the owner's name and the meeting's language,
// nothing else of the owner's.
type MeetingRoomClaudeMD struct{ Owner, Language string }

func RenderMeetingRoomClaudeMD(d MeetingRoomClaudeMD) string { return render("meeting/room_claude", d) }

// MeetingRoomHeader is a room's first message (meeting/room_header): the upload's title and calendar hint, when it
// started (the user's zone), its language and the room's role.
type MeetingRoomHeader struct {
	Title, Started, Language, Role string
	Calendar                       *MeetingCalendar
}

func RenderMeetingRoomHeader(d MeetingRoomHeader) string { return render("meeting/room_header", d) }

// MeetingRoomHeard is a heard question for the room (meeting/room_heard): the lines before the tap at At.
type MeetingRoomHeard struct {
	Owner, At string
	Lines     []MeetingLine
}

func RenderMeetingRoomHeard(d MeetingRoomHeard) string { return render("meeting/room_heard", d) }

// MeetingRoomPhoto is the text that goes into the room with a photo, the image beside it (meeting/room_photo): its
// number, its time in the meeting, the owner's caption and the text the phone recognized in it.
type MeetingRoomPhoto struct {
	N                 int
	At, Caption, Text string
}

func RenderMeetingRoomPhoto(d MeetingRoomPhoto) string { return render("meeting/room_photo", d) }

// MeetingRoomPhotoWithdrawn tells the room that photo N was deleted after it went in (meeting/room_photo_withdrawn).
type MeetingRoomPhotoWithdrawn struct{ N int }

func RenderMeetingRoomPhotoWithdrawn(d MeetingRoomPhotoWithdrawn) string {
	return render("meeting/room_photo_withdrawn", d)
}

// MeetingJustMe is a Just me message to the main chat (meeting/just_me): the meeting's title and calendar title (its
// data), the recording, the time in the meeting, then the owner's text.
type MeetingJustMe struct {
	Title, Calendar, RecordingID, At, Text string
}

func RenderMeetingJustMe(d MeetingJustMe) string { return render("meeting/just_me", d) }

// MeetingFollowupSeed is a follow-up's first message (meeting/followup_seed): the meeting's material, then the owner's
// text. Photos are the listed photos with a caption or recognized text (PhotosCut: some of the texts were cut), Notes
// those with the room's note on them, which belong to the room thread. Transcript is the transcript of record (Cut:
// its start and end only); a trimmed recording has none, nor photos or a thread.
type MeetingFollowupSeed struct {
	Title, Calendar, Started, RecordingID string
	PrivateSet, Private                   bool
	Minutes                               string
	Photos                                []MeetingPhoto
	PhotosCut                             bool
	Thread                                []MeetingQA
	Notes                                 []MeetingPhoto
	Transcript                            string
	Cut, Trimmed                          bool
	Text                                  string
}

func RenderMeetingFollowupSeed(d MeetingFollowupSeed) string {
	return render("meeting/followup_seed", d)
}
