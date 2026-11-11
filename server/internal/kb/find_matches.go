package kb

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

// Match is one place in an entry that matched: the line, and for a message the ones around it.
type Match struct {
	Path string `json:"path"`
	N    int    `json:"n"`
	Line string `json:"line"`
	// Day is a message's day ("2006-01-02"); Before / After the messages next to it that day (continuation lines
	// folded into their message).
	Day    string   `json:"day,omitempty"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// Matches returns the places in one entry matching query: the profile, timeline and metadata, then messages, newest
// day first; at most limit.
func (e *Engine) Matches(kind, id, query string, limit int) []Match {
	if kind == KindRecording {
		id, _ = RecordingID(id)
	}
	var recording map[string][]byte
	if kind == KindRecording {
		recording, _ = e.Store.recordingSnapshot(id)
	}
	match := matcher(query)
	if match == nil {
		return nil
	}
	var out []Match
	ws := words(query)
	look := func(p string, msgs bool) {
		var raw []byte
		var err error
		if base := filepath.Base(p); kind == KindRecording && base != "summary.md" && base != "minutes.md" && base != recapMarkdown && base != photosText {
			raw = recording[base] // the uploaded files, as committed (summary, minutes, recap and photos are the hub's)
		} else {
			raw, err = os.ReadFile(p)
		}
		if err != nil || !hasAll(fold(string(raw)), ws) {
			return
		}
		rel, _ := filepath.Rel(e.Store.Dir, p)
		rel = filepath.ToSlash(rel)
		if !msgs {
			for i, l := range strings.Split(string(raw), "\n") {
				if len(out) >= limit {
					return
				}
				if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "# ") && match(t) {
					out = append(out, Match{Path: rel, N: i + 1, Line: clip(t, 600)})
				}
			}
			return
		}
		day := strings.TrimSuffix(filepath.Base(p), ".md")
		ms := messages(string(raw))
		// Newest first within the day too, as the days are.
		for i := len(ms) - 1; i >= 0; i-- {
			if len(out) >= limit {
				return
			}
			m := ms[i]
			if !match(m.text) {
				continue
			}
			mt := Match{Path: rel, N: m.n, Line: clip(m.text, 1200), Day: day}
			for j := max(0, i-2); j < i; j++ {
				mt.Before = append(mt.Before, clip(ms[j].text, 300))
			}
			for j := i + 1; j < min(len(ms), i+3); j++ {
				mt.After = append(mt.After, clip(ms[j].text, 300))
			}
			out = append(out, mt)
		}
	}
	switch kind {
	case KindChat, KindPerson, KindProject:
		for _, f := range []string{"summary.md", "log.md", "meta.json"} {
			look(e.Store.path(kind, id, f), false)
		}
		if kind == KindChat {
			// The app opens a result right after a Find, whose cache (findCache.text) holds the chat's days: a day
			// without every word costs a stat there instead of a read and a fold, which for a rare word would be every
			// day the chat has. The days that have them are read again by look, as before. Like Search, this leaves
			// pruning to Find.
			gen := e.find.begin()
			days := e.Store.Days(id)
			for i := len(days) - 1; i >= 0 && len(out) < limit; i-- {
				p := e.Store.path(KindChat, id, "messages", days[i]+".md")
				if t, _, ok := e.find.text(p, gen); !ok || !hasAll(t.lower, ws) {
					continue
				}
				look(p, true)
			}
		}
	case KindRecording:
		for _, f := range []string{"summary.md", "minutes.md", recapMarkdown, "transcript.md", photosText, "meta.json"} {
			look(e.Store.path(kind, id, f), false)
		}
	case KindTopic:
		for _, f := range []string{"summary.md", "meta.json"} {
			look(e.Store.path(kind, id, f), false)
		}
	case KindDoc, KindMeeting, KindEvent:
		look(filepath.Join(e.Store.Dir, kind, SafeID(id)+".json"), false)
	}
	return out
}

type message struct {
	n    int // line number of its first line (1-based)
	text string
}

// messages splits a day file into messages: "- HH:MM name: text" and the indented lines that continue it.
// Other lines (blank lines, headings) do not end a message.
func messages(raw string) []message {
	var out []message
	// The last message's continuation lines, joined into its text once it ends: appending each one to the text
	// would copy the whole message again per line, quadratic in a long paste.
	var more []string
	end := func() {
		if len(more) > 0 {
			m := &out[len(out)-1]
			m.text = m.text + "\n" + strings.Join(more, "\n")
			more = more[:0]
		}
	}
	for i, l := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(l, "- "):
			end()
			out = append(out, message{n: i + 1, text: strings.TrimPrefix(l, "- ")})
		case strings.HasPrefix(l, "  ") && len(out) > 0:
			more = append(more, strings.TrimSpace(l))
		}
	}
	end()
	return out
}

// Defined in the contract package kbwire (docs/server/architecture.md "Contract packages"); the old names stay.
type DayMessage = kbwire.DayMessage

// Day returns a chat's messages of one day ("2006-01-02") and the days before and after it that have any.
func (e *Engine) Day(chatID, day string) (msgs []DayMessage, prev, next string, err error) {
	if _, perr := time.Parse("2006-01-02", day); perr != nil {
		return nil, "", "", ErrNotFound
	}
	raw, rerr := os.ReadFile(e.Store.path(KindChat, chatID, "messages", day+".md"))
	if rerr != nil {
		return nil, "", "", ErrNotFound
	}
	for _, m := range messages(string(raw)) {
		msgs = append(msgs, DayMessage{N: m.n, Text: m.text})
	}
	for _, d := range e.Store.Days(chatID) {
		if d < day {
			prev = d
		} else if d > day && next == "" {
			next = d
		}
	}
	return msgs, prev, next, nil
}
