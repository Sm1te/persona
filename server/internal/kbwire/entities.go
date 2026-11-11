package kbwire

import (
	"encoding/json"
	"time"
)

// DayMessage is one message of a day page.
type DayMessage struct {
	SentBy       string     `json:"sent_by,omitempty"`
	SentLedgerID int64      `json:"sent_ledger_id,omitempty"`
	RecalledAt   *time.Time `json:"recalled_at,omitempty"`
	N            int        `json:"n"`
	Text         string     `json:"text"`
}

// Detail is everything about one entity: its metadata, compressed context, a project's timeline, a chat's latest text.
type Detail struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	Path    string          `json:"path"` // absolute, for reading more with file tools
	Meta    json.RawMessage `json:"meta"`
	Summary string          `json:"summary,omitempty"`
	Log     []string        `json:"log,omitempty"`
	Recent  string          `json:"recent,omitempty"`
	// Links are the linked projects, chats and people, named and ranked.
	Links []Entry `json:"links,omitempty"`
	// Moments: a project's Moments posts (LinkMoments), newest first.
	Moments []MomentRef `json:"moments,omitempty"`
	// Segments: a recording's transcript (the app shows it; Recent has it as text for the assistant).
	Segments []RecordingSegment `json:"segments,omitempty"`
	// Minutes: a recording's meeting minutes (minutes.json, docs/assistant/meeting-minutes.md), once written.
	Minutes json.RawMessage `json:"minutes,omitempty"`
	// Recap: an all-day recording's recap (recap.json, docs/assistant/day-recap.md), once written.
	Recap json.RawMessage `json:"recap,omitempty"`
	// Points: a topic's knowledge points (points.json, docs/assistant/knowledge.md "Topics"), grouped by Path.
	Points []TopicPoint `json:"points,omitempty"`
}

// TopicPoint is one knowledge point of a topic: a concept, decision, practice, pitfall, fact or open question, under
// up to two levels of subtopics (Path), with the entries it came from.
type TopicPoint struct {
	ID      string        `json:"id"`
	Path    []string      `json:"path,omitempty"`
	Kind    string        `json:"kind"` // concept | decision | practice | pitfall | fact | question
	Text    string        `json:"text"`
	Why     string        `json:"why,omitempty"` // a decision's reason, a pitfall's cause
	Sources []TopicSource `json:"sources,omitempty"`
	When    string        `json:"when,omitempty"` // YYYY-MM-DD: last confirmed
}

// TopicSource is an entry a knowledge point came from: a project's timeline or a chat's summary, and the day.
type TopicSource struct {
	Kind string `json:"kind"` // projects | chats
	ID   string `json:"id"`
	Day  string `json:"day,omitempty"`
}

// MomentRef is a Moments post as a project shows it.
type MomentRef struct {
	ID       string    `json:"id"`
	Author   string    `json:"author"`
	Text     string    `json:"text"` // clipped
	At       time.Time `json:"at"`
	Comments int       `json:"comments"`
	Note     string    `json:"note,omitempty"` // how it bears on the project
}

// Update is a change by hand (the app) or by the assistant. Set fields only; tagging set here is locked.
type Update struct {
	Pinned         *string  `json:"pinned,omitempty"`
	Lock           *bool    `json:"lock,omitempty"`
	RemovePeople   []string `json:"remove_people,omitempty"`
	RemoveChats    []string `json:"remove_chats,omitempty"`
	RemoveAliases  []string `json:"remove_aliases,omitempty"`
	RemoveProjects []string `json:"remove_projects,omitempty"`
	Name           string   `json:"name,omitempty"`
	Kind           string   `json:"project_kind,omitempty"` // projects: short | long
	Status         string   `json:"status,omitempty"`
	Goal           string   `json:"goal,omitempty"`
	MyRole         string   `json:"my_role,omitempty"`
	Note           string   `json:"note,omitempty"` // people / items
	Importance     string   `json:"importance,omitempty"`
	Relevance      *int     `json:"relevance,omitempty"`
	Tags           []string `json:"tags,omitempty"`    // nil preserves; non-nil replaces, including an empty slice
	Summary        string   `json:"summary,omitempty"` // replaces summary.md
	Log            string   `json:"log,omitempty"`     // projects: appended to the timeline
	Source         string   `json:"source,omitempty"`  // who writes the log line (default Persona)
	Aliases        []string `json:"aliases,omitempty"`
	Chats          []string `json:"chats,omitempty"`    // projects: chat ids to link
	People         []string `json:"people,omitempty"`   // projects: person ids to link
	Dirs           []string `json:"dirs,omitempty"`     // projects: "<machine>:<path>" code directories
	Projects       []string `json:"projects,omitempty"` // chats / people / items: project slugs to link
	// MergeInto (projects): fold this project into that one (MergeProjects); nothing else in the update applies.
	MergeInto string `json:"merge_into,omitempty"`
}

// RecordingSegment is one transcribed stretch: seconds from the recording's start.
type RecordingSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	// N is an all-day recording's line number: its upload position plus one, what the recap cites
	// (docs/assistant/day-recap.md); 0 (absent) for every other recording.
	N int `json:"n,omitempty"`
}

// Hit is one search result.
type Hit struct {
	Path string `json:"path"` // relative to the data dir; absolute for a file in one of the ExtraDirs
	Line string `json:"line"`
}

// Entry is a row of a ranked list.
type Entry struct {
	Kind       string    `json:"kind"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	OneLine    string    `json:"one_line,omitempty"`
	Importance string    `json:"importance,omitempty"`
	Relevance  int       `json:"relevance"`
	Tags       []string  `json:"tags,omitempty"`
	Updated    time.Time `json:"updated"`
	Extra      string    `json:"extra,omitempty"`  // projects: short / long · status; chats: dm / group…
	Source     string    `json:"source,omitempty"` // chats: the source they came from (lark, mail, wechat, github…)
}
