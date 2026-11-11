package kb

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/text/unicode/norm"
)

// Searching for the app (docs/assistant/knowledge.md "Search"): results are entries, not lines. Every line containing all the
// words is found (archived messages newest day first), grouped by the entry it belongs to, the entries ranked
// (name matches, then importance, relevance, how much matched, recency), a few lines kept for each. Opening one, Matches
// gives every place in that entry with a few messages around each, and Day the whole day a message is in.

// Found is one line of an entry that matched.
type Found struct {
	Path string `json:"path"` // relative to the data dir
	Line string `json:"line"`
	// N is the line's number in its file (1-based): messages are found again by it on the day page.
	N int `json:"n"`
}

// Result is one entry found by a search: the entry as ranked lists show it and its first lines.
type Result struct {
	Entry Entry   `json:"entry"`
	Count int     `json:"count"` // lines matched in all, at most foundCap
	Hits  []Found `json:"hits"`
	// InName: the name itself matched (those come first).
	InName bool `json:"in_name,omitempty"`
}

const (
	hitsPerResult = 3
	foundCap      = 99
)

// fold is the form text and queries are compared in: lowercased, then composed (NFC), so "café" typed precomposed
// finds the decomposed "cafe\u0301" macOS often writes, and the other way round. Already composed text (nearly all of
// the archive) passes norm's quick check without being copied.
func fold(s string) string { return norm.NFC.String(strings.ToLower(s)) }

// words is the query folded and split; a file without all of them is skipped whole.
func words(query string) []string { return strings.Fields(fold(query)) }

// hasAll is the whole-file check: most files have none of the words, and one pass over the file is much cheaper than
// a match per line (the archive is ~400k lines; everything is read, the oldest days too).
func hasAll(lowered string, ws []string) bool {
	for _, w := range ws {
		if !strings.Contains(lowered, w) {
			return false
		}
	}
	return true
}

// matcher returns whether a line has every word of the query (case- and normalization-insensitive, fold); nil for an
// empty query.
func matcher(query string) func(string) bool {
	words := words(query)
	if len(words) == 0 {
		return nil
	}
	return func(l string) bool {
		l = fold(l)
		for _, w := range words {
			if !strings.Contains(l, w) {
				return false
			}
		}
		return true
	}
}

// searchable reports whether a file of the data dir (path relative to it) holds text a search may return. Bookkeeping
// is not: a person's hashed identity hints (handles.json, handles.go), a connector's cursor (cursors/<source>.json),
// history imports' progress (history/, history.go), the selection's bookkeeping (select/, a chat's retention.json),
// the Moments verdicts of a project (moments-judged.json, moments_link.go) and recording undo snapshots mean nothing
// to a model, and the hints are kept out of every tool and prompt. Nor are a topic's points.json and a source's
// distill mark (.topics.json, topics.go). A meeting's live log (live.json, live.go) is never
// searched: other people's words said while Persona attended, and the questions asked of it. Nor is photos.json
// (recording_photos.go: digests, deletions; photos.md has the photos' words), and the photos themselves are in
// attachments/, which the walk never enters.
func searchable(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case parts[0] == "cursors" || parts[0] == "history" || parts[0] == selectDir || parts[0] == trashDirName:
		return false
	case len(parts) == 3 && parts[0] == KindChat && parts[2] == retentionFile:
		return false
	case len(parts) == 3 && parts[0] == KindRecording && (parts[2] == recordingJournal || parts[2] == "minutes.json"):
		return false // minutes.md has the minutes' text
	case len(parts) >= 3 && parts[0] == KindRecording && strings.HasPrefix(parts[2], liveFile):
		return false // live.json, and its temporary file while it is replaced
	case len(parts) >= 3 && parts[0] == KindRecording && strings.HasPrefix(parts[2], meetingFile):
		return false // meeting.json (meeting.go): the manager's bookkeeping of the room and the mark
	case len(parts) >= 3 && parts[0] == KindRecording && strings.HasPrefix(parts[2], photosFile):
		return false // photos.json (recording_photos.go): digests and deletions; photos.md has the photos' words
	case len(parts) == 3 && parts[0] == KindPerson && parts[2] == "handles.json":
		return false
	case len(parts) == 3 && parts[0] == KindProject && parts[2] == "moments-judged.json":
		return false
	case len(parts) == 3 && parts[0] == KindTopic && parts[2] == topicPointsFile:
		return false // summary.md has the points' text; the candidates waiting in it are the model's, unchecked
	case len(parts) == 3 && parts[2] == topicMarkFile:
		return false
	}
	return true
}

// entryOf maps a path relative to the data dir to the entry it belongs to: chats/<id>/…, docs/<id>.json.
func entryOf(rel string) (kind, id string, ok bool) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	switch parts[0] {
	case KindChat, KindPerson, KindProject, KindRecording, KindTopic:
		if len(parts) < 3 {
			return "", "", false
		}
		return parts[0], parts[1], true
	case KindDoc, KindMeeting, KindEvent:
		if id, ok := strings.CutSuffix(parts[1], ".json"); ok && len(parts) == 2 {
			return parts[0], id, true
		}
	}
	return "", "", false
}

// shown reports whether Matches reads the file at rel (relative to the data dir) for the entry kind/id it belongs to:
// Find only counts lines from those, so every result it gives can be located once opened. Other files under an entry
// (a project's moments-judged.json, a person's handles.json) are bookkeeping, not something to show.
func shown(kind, rel string) bool {
	// rest is what follows <kind>/<id>/ (entryOf has checked there is one).
	_, rest, _ := strings.Cut(filepath.ToSlash(rel), "/")
	_, rest, _ = strings.Cut(rest, "/")
	switch kind {
	case KindChat, KindPerson, KindProject:
		if rest == "summary.md" || rest == "log.md" || rest == "meta.json" {
			return true
		}
		// Store.Days: the .md files directly in a chat's messages/.
		day, ok := strings.CutPrefix(rest, "messages/")
		return kind == KindChat && ok && strings.HasSuffix(day, ".md") && !strings.Contains(day, "/")
	case KindRecording:
		return rest == "summary.md" || rest == "minutes.md" || rest == "transcript.md" || rest == photosText || rest == "meta.json"
	case KindTopic:
		return rest == "summary.md" || rest == "meta.json"
	case KindDoc, KindMeeting, KindEvent:
		return true // entryOf already keeps only <kind>/<id>.json
	}
	return false
}

// Find searches every entry and returns at most limit, ranked; none when limit <= 0.
func (e *Engine) Find(query string, limit int) []Result {
	match := matcher(query)
	if match == nil || limit <= 0 {
		return nil
	}
	recording := e.Store.recordingSearchView()
	ws := words(query)
	// A file whose text folds to itself (nearly every one) has lines that do too, so they are matched as written
	// instead of folded one by one: lowercasing works rune by rune, and NFC neither composes nor reorders across a
	// line break or a space (no canonical decomposition holds one), so trimming a line does not change how it folds.
	// The two spaces NFC does change (U+2000, U+2001) cannot be in such a file: they would have changed its fold.
	matchFolded := func(t string) bool { return hasAll(t, ws) }
	// Files are looked at in parallel (the archive is ~25 MB); what each found is added in file order, so the lines
	// kept for an entry are its profile's first, then the newest days'. The app searches as the user types, so a
	// file unchanged since the last search is not read again (findCache): one stat each instead of read + lowercase.
	all := e.files().all
	type fileHits struct {
		key   string
		stat  fileStat
		found []Found // what fileFound keeps
		count int
	}
	per := make([]fileHits, len(all))
	gen := e.find.begin()
	var next atomic.Int64
	var wg sync.WaitGroup
	for range max(1, min(8, runtime.NumCPU())) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(all) {
					return
				}
				f := all[i]
				p, rel, kind, id := f.path, f.rel, f.kind, f.id // kind is "" for a file Matches does not read (shown)
				if kind == "" {
					continue
				}
				fh := fileHits{key: kind + "/" + id}
				c, st, ok := e.searchText(p, gen, recording)
				fh.stat = st
				if ok && hasAll(c.lower, ws) {
					m := match
					if c.same {
						m = matchFolded
					}
					fh.found, fh.count = fileFound(c.raw, rel, m)
				}
				per[i] = fh
			}
		}()
	}
	wg.Wait()
	e.find.prune(gen)
	// The entries the lists show depend only on the JSON files (meta.json, docs/<id>.json): kept while none changed.
	var metas []fileStat
	for i, fh := range per {
		if fh.key != "" && strings.HasSuffix(all[i].path, ".json") {
			metas = append(metas, fh.stat)
		}
	}
	known := e.find.entries(metas, func() map[string]Entry {
		known := map[string]Entry{}
		for _, k := range []string{KindProject, KindChat, KindPerson, KindDoc, KindMeeting, KindEvent, KindTopic} {
			for _, en := range e.List(k, "") {
				known[k+"/"+en.ID] = en
			}
		}
		for id, files := range recording {
			var r Recording
			if raw := files["meta.json"]; raw != nil && json.Unmarshal(raw, &r) == nil {
				known[KindRecording+"/"+id] = recordingEntry(&r)
			}
		}
		return known
	})
	by := map[string]*Result{}
	for _, fh := range per {
		if fh.count == 0 {
			continue
		}
		en, ok := known[fh.key]
		if !ok {
			continue // an entry the lists do not show (a doc without a title yet): nothing to open
		}
		r := by[fh.key]
		if r == nil {
			r = &Result{Entry: en}
			by[fh.key] = r
		}
		r.Count = min(foundCap, r.Count+fh.count)
		for _, f := range fh.found {
			if len(r.Hits) < hitsPerResult && !hasLine(r.Hits, f.Line) {
				r.Hits = append(r.Hits, f)
			}
		}
	}
	// Names: an entry whose name matches is found even when no file line does.
	for key, en := range known {
		if match(en.Name) {
			if by[key] == nil {
				by[key] = &Result{Entry: en}
			}
			by[key].InName = true
		}
	}
	out := make([]Result, 0, len(by))
	for _, r := range by {
		r.Hits = sortHits(r.Hits)
		out = append(out, *r)
	}
	imp := func(s string) int {
		if s == "" {
			return 4
		}
		return int(s[1] - '0')
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.InName != b.InName {
			return a.InName
		}
		if imp(a.Entry.Importance) != imp(b.Entry.Importance) {
			return imp(a.Entry.Importance) < imp(b.Entry.Importance)
		}
		if a.Entry.Relevance != b.Entry.Relevance {
			return a.Entry.Relevance > b.Entry.Relevance
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if !a.Entry.Updated.Equal(b.Entry.Updated) {
			return a.Entry.Updated.After(b.Entry.Updated)
		}
		return a.Entry.Kind+a.Entry.ID < b.Entry.Kind+b.Entry.ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// files lists the profile files (summaries, metadata, timelines) and the message days, newest day first. What it
// returns is shared with findCache: callers only read it.
func (e *Engine) files() *dirWalk {
	c := &e.find
	c.walkMu.Lock()
	defer c.walkMu.Unlock()
	return e.walkLocked()
}

// walkLocked is files with walkMu held. Unchanged directories cost a stat each: their listings are kept with every
// child's paths and kind, and the lists are gathered into scratch slices, copied only when something changed.
func (e *Engine) walkLocked() *dirWalk {
	c := &e.find
	c.walkGen++
	changed := false
	files, days := c.scratch[0][:0], c.scratch[1][:0]
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		ents, fresh := c.listDir(dir, rel)
		changed = changed || fresh
		for i := range ents {
			switch d := &ents[i]; {
			case d.dir:
				// Attachments hold no text; history/ and select/ are bookkeeping, not searched (see searchable).
				if d.name != "attachments" && d.rel != "history" && d.rel != selectDir {
					walk(d.path, d.rel)
				}
			case d.day:
				days = append(days, d)
			case d.file:
				files = append(files, d)
			}
		}
	}
	walk(e.Store.Dir, "")
	c.scratch = [2][]*dirEnt{files, days}
	for d, l := range c.dirs {
		if l.gen != c.walkGen {
			delete(c.dirs, d)
			changed = true
		}
	}
	if !changed && c.walked {
		return c.walk
	}
	w := &dirWalk{files: slices.Clone(files), walkDays: slices.Clone(days), days: slices.Clone(days)}
	sort.SliceStable(w.days, func(i, j int) bool { return w.days[i].name > w.days[j].name })
	w.all = append(slices.Clip(w.files), w.days...)
	c.walk, c.walked = w, !changed
	return w
}

// searchOrder is the order Search reads the data dir's files in: the profile files as walked, the message days
// newest first and the Moments posts (.md) newest first.
type searchOrder struct {
	files, days, moments []*dirEnt
}

// searchFiles returns the data dir's files in Search's order, worked out once per change of the walk. The days are
// in Search's own order (sort.Slice of the walk's, which can put same-named days of different chats the other way
// round from Find's). Shared: callers only read it.
func (e *Engine) searchFiles() *searchOrder {
	c := &e.find
	c.walkMu.Lock()
	defer c.walkMu.Unlock()
	w := e.walkLocked()
	if w.search != nil {
		return w.search
	}
	o := &searchOrder{}
	for i, l := range [][]*dirEnt{w.files, w.walkDays} {
		for _, d := range l {
			switch {
			case d.moment: // read after everything else, whatever else it would be
				if strings.HasSuffix(d.name, ".md") {
					o.moments = append(o.moments, d)
				}
			case i == 0:
				o.files = append(o.files, d)
			default:
				o.days = append(o.days, d)
			}
		}
	}
	sort.Slice(o.days, func(i, j int) bool { return o.days[i].name > o.days[j].name })
	sort.Slice(o.moments, func(i, j int) bool { return o.moments[i].path > o.moments[j].path }) // newer post ids first
	w.search = o
	return o
}

// sortHits puts what says most first: the profile, the timeline, messages, then metadata (a tag, a member's name).
func sortHits(h []Found) []Found {
	rank := func(f Found) int {
		switch {
		case strings.HasSuffix(f.Path, "/summary.md"), strings.HasSuffix(f.Path, "/minutes.md"):
			return 0
		case strings.HasSuffix(f.Path, "/log.md"):
			return 1
		case strings.Contains(f.Path, "/messages/"), strings.HasSuffix(f.Path, "/transcript.md"), strings.HasSuffix(f.Path, "/"+photosText):
			return 2
		}
		return 3
	}
	sort.SliceStable(h, func(i, j int) bool { return rank(h[i]) < rank(h[j]) })
	return h
}

func hasLine(h []Found, line string) bool {
	for _, f := range h {
		if f.Line == line {
			return true
		}
	}
	return false
}

// fileFound is what Find keeps of one file (rel, relative to the data dir): its first hitsPerResult distinct lines
// that match, clipped as results show them and numbered as in the file, and how many lines match, at most foundCap.
// No other line of the file can be shown: an entry already holding h lines takes at most hitsPerResult-h more, and
// at most h of the file's distinct lines repeat those.
func fileFound(raw, rel string, match func(string) bool) (found []Found, count int) {
	path := filepath.ToSlash(rel)
	for n, rest, more := 1, raw, true; more; n++ {
		var l string
		l, rest, more = strings.Cut(rest, "\n")
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "# ") || !match(t) {
			continue
		}
		count++
		if len(found) < hitsPerResult {
			if line := clip(t, 300); !hasLine(found, line) {
				found = append(found, Found{Path: path, Line: line, N: n})
			}
		}
		if count >= foundCap && len(found) == hitsPerResult {
			break
		}
	}
	return found, min(count, foundCap)
}
