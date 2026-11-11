package prompts

import (
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

// KBOwner is the knowledge base models' system prompts: compressing a chat (kb/compress), rewriting a project
// (kb/project), tagging documents, meetings and events (kb/items).
type KBOwner struct {
	Owner string
}

func RenderKBCompress(o KBOwner) string { return render("kb/compress", o) }
func RenderKBProject(o KBOwner) string  { return render("kb/project", o) }
func RenderKBItems(o KBOwner) string    { return render("kb/items", o) }

// RenderKBRecording is the system prompt for summarizing a recording's transcript (kb/recording).
func RenderKBRecording(o KBOwner) string { return render("kb/recording", o) }

// RenderKBReadme is the README.md at the top of the data dir (kb/readme).
func RenderKBReadme() string { return render("kb/readme", nil) }

// KnownProject is a project the models may name (kb/known_projects).
type KnownProject struct {
	Pinned  string
	Name    string
	Aliases []string
	OneLine string // where it is now: lets the model see that differently named work is the same project
}

// KBCompressInput is a chat's summary so far and its new messages (kb/compress_input).
type KBCompressInput struct {
	Pinned    string
	People    []KBPinnedPerson
	Name      string
	Kind      string // dm | group | bot | mail | moment | pr | issue
	Automated bool   // mail from a notification / marketing address
	Projects  []KnownProject
	Tags      []string
	Summary   string
	News      string
}

func RenderKBCompressInput(in KBCompressInput) string { return render("kb/compress_input", in) }

// KBPinnedPerson is a person whose confirmed facts constrain compression.
type KBPinnedPerson struct{ Name, Pinned string }

// KBProjectChat is a chat linked to a project, its summary clipped.
type KBProjectChat struct {
	ID, Summary string
}

// KBProjectInput is a project's file, timeline and chats (kb/project_input).
type KBProjectInput struct {
	Pinned     string
	Name, Kind string
	Aliases    []string
	Tags       []string
	Summary    string
	Log        []string
	Chats      []KBProjectChat
	Moments    []KBMoment // Moments posts judged to be about it
}

// KBMoment is a Moments post put before a model: who, when, how many comments, its text clipped, and (when judged
// before) how it bears on the project.
type KBMoment struct {
	ID, Author, At, Text, Note string
	Comments                   int
}

// KBDedupeProject is one side of a possible duplicate.
type KBDedupeProject struct {
	Key                          string // "A" | "B"
	Name, OneLine, Goal, Summary string
	Aliases, Chats               []string
	People                       int
}

// KBDedupeInput is two projects that may be one (kb/dedupe_input); Shared is how many people they have in common.
type KBDedupeInput struct {
	A, B   KBDedupeProject
	Shared int
}

// RenderKBDedupe is the system prompt deciding whether two projects are the same work (kb/dedupe).
func RenderKBDedupe(o KBOwner) string             { return render("kb/dedupe", o) }
func RenderKBDedupeInput(in KBDedupeInput) string { return render("kb/dedupe_input", in) }

// KBMomentsLinkInput is a project and Moments posts its keywords found (kb/moments_link_input).
type KBMomentsLinkInput struct {
	Name, OneLine, Goal, Summary string
	Aliases, Keywords            []string
	Posts                        []KBMoment
}

// RenderKBMomentsLink is the system prompt picking the posts that are about a project (kb/moments_link).
func RenderKBMomentsLink(o KBOwner) string { return render("kb/moments_link", o) }
func RenderKBMomentsLinkInput(in KBMomentsLinkInput) string {
	return render("kb/moments_link_input", in)
}

func RenderKBProjectInput(in KBProjectInput) string { return render("kb/project_input", in) }

// KBItem is one document, meeting or event to tag.
type KBItem struct {
	Pinned string
	Key    string // kind/id
	Kind   string // docs | meetings | events
	Title  string
	At     time.Time // local time; zero when unknown
	Where  string
}

// KBItemsInput is a batch of items to tag (kb/items_input).
type KBItemsInput struct {
	Projects []KnownProject
	Tags     []string
	Items    []KBItem
}

func RenderKBItemsInput(in KBItemsInput) string { return render("kb/items_input", in) }

// KBRecordingInput is one recording to summarize (kb/recording_input): Start in the user's zone, the transcript as
// transcript.md has it (clipped).
type KBRecordingInput struct {
	Pinned, Title, Device, Language string
	Start                           time.Time
	Minutes                         int
	Projects                        []KnownProject
	Tags                            []string
	Transcript                      string
}

func RenderKBRecordingInput(in KBRecordingInput) string { return render("kb/recording_input", in) }

// RenderKBMinutes is the system prompt for a meeting recording's minutes, or a part's notes (kb/minutes).
func RenderKBMinutes(o KBOwner) string { return render("kb/minutes", o) }

// RenderKBMinutesMerge is the system prompt merging the parts' notes of a long meeting into its minutes
// (kb/minutes_merge).
func RenderKBMinutesMerge(o KBOwner) string { return render("kb/minutes_merge", o) }

// KBMinutesInput is a meeting recording to make minutes of (kb/minutes_input, kb/minutes_meeting): times in the user's
// zone; Title the user's or the calendar's; Meeting the knowledge base's calendar item; Attendees and Location the
// phone calendar's. Part of Parts (both 1 for a transcript read at once); Transcript is that part's numbered lines,
// the meeting's photos ("[P3] … photo: …", their words only) among them.
type KBMinutesInput struct {
	Pinned, Title, Meeting, Location, Weekday, Language string
	Attendees                                           []string
	Start, End                                          time.Time
	Minutes                                             int
	Part, Parts                                         int
	Transcript                                          string
}

func RenderKBMinutesInput(in KBMinutesInput) string { return render("kb/minutes_input", in) }

// KBMinutesNote is an item of a part's notes: Line the transcript line it cites, or Photo the photo (Line 0 then); Who,
// By and DueDate as the part said.
type KBMinutesNote struct {
	Line, Photo            int
	Text, Who, By, DueDate string
}

// KBMinutesPart is a part's notes, From and To its first and last lines' clock times.
type KBMinutesPart struct {
	Part                             int
	From, To, Where                  string
	Attendees                        []string
	Points, Decisions, Actions, Open []KBMinutesNote
}

// KBMinutesMergeInput is a long meeting's parts' notes to merge (kb/minutes_merge_input).
type KBMinutesMergeInput struct {
	Meeting KBMinutesInput
	Parts   []KBMinutesPart
}

func RenderKBMinutesMergeInput(in KBMinutesMergeInput) string {
	return render("kb/minutes_merge_input", in)
}

// KBDigestEntry is one project or chat in the digest.
type KBDigestEntry struct {
	ID, Name, Importance, OneLine string
	Relevance                     int
}

// KBDigest is the most important projects and chats, for the assistant's live snapshot (kb/digest).
type KBDigest struct {
	Projects, Chats []KBDigestEntry
}

func RenderKBDigest(d KBDigest) string { return render("kb/digest", d) }

// Defined in the contract package kbwire (docs/server/architecture.md "Contract packages"); the old names stay.
type (
	KBContext = kbwire.ChatContext
	KBProject = kbwire.ChatProject
)
