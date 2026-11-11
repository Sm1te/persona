package kb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
)

// Ingest durably archives a source's messages. Nonempty IDs are scoped by the
// exact source and chat, and first-write-wins: replay or an edited body with the
// same ID adds nothing. Missing IDs retain legacy append behavior. Old Markdown
// has no recoverable IDs, so only messages ingested with receipts are replay-safe.
//
// With a selection (Engine.Selection), every message goes through it first (Selection.Evaluate): only what it
// stores is written, with its rule's days, and the mine rule's context is kept per chat between batches
// (select_context.go). A chat outside the selection (Selected false) is archived as before.
func (e *Engine) Ingest(ctx context.Context, src Source, sc SourceChat, msgs []SourceMessage) (int, error) {
	if e.Store.ReadOnly() {
		return 0, ErrReadOnly
	}
	// Reserve live ingestion through attachment preparation too: a replay must
	// not overwrite the first version's files. History and compression can still
	// run while a connector downloads, so reread their metadata before commit.
	e.Store.ingestMu.Lock()
	defer e.Store.ingestMu.Unlock()
	cut := e.textCutoff()
	e.Store.chatMu.Lock()
	c, exists, r, err := e.Store.ingestState(src.Name(), sc, cut)
	e.Store.chatMu.Unlock()
	if err != nil {
		return 0, err
	}
	if err := attributeChat(c, src); err != nil {
		return 0, err
	}
	sel, ret, facts := e.selection(), e.Retention(), ChatFactsOf(src.Name(), sc)
	selecting := sel != nil && facts.Selected()
	unchanged := func() (int, error) {
		if exists {
			e.index(KindChat, c.ID, c.Name, c.Tagging, c.OneLine, c.LastAt)
		}
		return 0, nil
	}
	span := 0
	var win mineWindow
	if selecting {
		if !sel.ReadChat(ret, facts).Read {
			return unchanged()
		}
		if span = sel.MineContext(); span > 0 {
			if win, err = e.Store.mineWindow(c.ID, src.Name(), sc.ID); err != nil {
				return 0, err
			}
		}
	}
	type pick struct {
		m    SourceMessage
		d    Decision
		late bool // stored with a later message of the owner's: older than lines written before it
	}
	var picks []pick
	for _, m := range msgs {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if _, seen := r.IDs[m.ID]; m.ID != "" && seen {
			continue
		}
		if !selecting {
			if !m.At.Before(cut) {
				picks = append(picks, pick{m: m, d: Decision{Store: true, Days: or(e.TextDays, 365), AttachmentDays: e.AttachmentDaysFor(src.Name())}})
			}
			continue
		}
		if span > 0 && win.seen(m.ID) {
			continue
		}
		mf := MessageFactsOf(m)
		switch {
		case m.Mine && span > 0:
			for _, b := range win.Before {
				if b.Stored {
					continue
				}
				bf := MessageFactsOf(b.M)
				bf.NearMine = true
				if d := sel.Evaluate(ret, facts, bf); d.Store {
					picks = append(picks, pick{m: b.M, d: d, late: true})
				}
			}
			win.Before, win.After = nil, span
		case win.After > 0:
			mf.NearMine = true
			win.After--
		}
		d := sel.Evaluate(ret, facts, mf)
		if d.Store {
			picks = append(picks, pick{m: m, d: d})
		}
		if span > 0 {
			win.push(m, d.Store, span)
		}
	}
	saveWindow := func() error {
		if span == 0 {
			return nil
		}
		return e.Store.putMineWindow(c.ID, win)
	}
	var lines []ingestLine
	var kept []SourceMessage
	for _, p := range picks {
		m := p.m
		if m.At.Before(e.CutoffFor(p.d.Days)) {
			continue
		}
		if _, seen := r.IDs[m.ID]; m.ID != "" && seen {
			continue
		}
		l := e.buildLine(ctx, src.Download, c, m, false, p.d.AttachmentDays)
		if l.Text == "" {
			continue
		}
		lines = append(lines, ingestLine{Line: l, rule: p.d.Rule, late: p.late})
		if m.ID != "" {
			r.IDs[m.ID] = m.At
		}
		kept = append(kept, m)
	}
	if len(lines) == 0 {
		if err := saveWindow(); err != nil {
			return 0, err
		}
		return unchanged()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	e.Store.chatMu.Lock()
	defer e.Store.chatMu.Unlock()
	c, _, r, err = e.Store.ingestState(src.Name(), sc, cut)
	if err != nil {
		return 0, err
	}
	if err := attributeChat(c, src); err != nil {
		return 0, err
	}
	for i, m := range kept {
		if m.ID != "" {
			r.IDs[m.ID] = m.At
			if r.Lines == nil {
				r.Lines = map[string]messageReceiptLine{}
			}
			r.Lines[m.ID] = messageReceiptLine{Hash: receiptLineHash(lines[i].Line), Mine: m.Mine}
		}
		e.notePerson(c, src.Name(), m)
		c.Mine = c.Mine || m.Mine
		if m.At.After(c.LastAt) {
			c.LastAt = m.At
		}
		// A new ID can arrive in a minute already summarized or refused. Reopen
		// that minute instead of letting the old watermark hide its pending line.
		if !c.CoveredUntil.IsZero() && m.At.Before(c.CoveredUntil) {
			c.CoveredUntil = m.At.Truncate(time.Minute)
		}
	}
	c.Messages += len(lines)
	c.Pending += len(lines)
	usual := ""
	if selecting {
		usual = sel.UsualRule(ret, facts)
		c.Rule = usual
	}
	if err := e.Store.commitIngest(c, lines, r, selecting, usual); err != nil {
		return 0, err
	}
	// After the commit: a window saved before it would say messages are stored that a failed commit never wrote.
	if err := saveWindow(); err != nil {
		e.logf("selection context of %s: %v", c.ID, err)
	}
	e.index(KindChat, c.ID, c.Name, c.Tagging, c.OneLine, c.LastAt)
	return len(lines), nil
}

// Defined in the contract package kbwire (docs/server/architecture.md "Contract packages"); the old names stay.
type (
	Detail    = kbwire.Detail
	MomentRef = kbwire.MomentRef
	Update    = kbwire.Update
	// Provenance and Evidence: attribution.go.
	Provenance = kbwire.Provenance
	Evidence   = kbwire.Evidence
)

var ErrNotFound = errors.New("not found")

// Get reads one entity. recentChars bounds a chat's latest messages in bytes, a recording's transcript in characters
// (0: none).
func (e *Engine) Get(kind, id string, recentChars int) (*Detail, error) {
	if kind == KindRecording {
		id, _ = RecordingID(id) // the phone's UUID in any case
	}
	d := &Detail{Kind: kind, ID: SafeID(id)}
	var meta string
	switch kind {
	case KindChat, KindPerson, KindProject, KindRecording, KindTopic:
		meta = e.Store.path(kind, id, "meta.json")
		d.Path = e.Store.path(kind, id)
		d.Summary = e.Store.Summary(kind, id)
	case KindDoc, KindMeeting, KindEvent:
		meta = filepath.Join(e.Store.Dir, kind, SafeID(id)+".json")
		d.Path = meta
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	var recording map[string][]byte
	var raw []byte
	var err error
	if kind == KindRecording {
		recording, err = e.Store.recordingSnapshot(id)
		raw = recording["meta.json"]
		if err == nil && raw == nil {
			err = ErrNotFound
		}
	} else {
		raw, err = os.ReadFile(meta)
	}
	if err != nil {
		return nil, ErrNotFound
	}
	d.Meta = raw
	if kind == KindRecording {
		e.Store.recFilesMu.RLock()
		m, _ := e.Store.readMeeting(id)
		e.Store.recFilesMu.RUnlock()
		d.Meta = metaWithMeeting(raw, m)
	}
	if kind == KindProject {
		d.Log = e.Store.Log(id, 30)
		if p, ok := e.Store.Project(id); ok {
			for _, x := range p.Moments {
				if m, ok := e.Store.MomentPost(x.ID); ok {
					f := momentForPrompt(m, x.Note, 200, e.Store.Zone())
					d.Moments = append(d.Moments, MomentRef{ID: m.ID, Author: f.Author, Text: f.Text, At: m.At, Comments: m.CommentCount, Note: x.Note})
				}
			}
		}
	}
	if kind == KindRecording {
		var segs []RecordingSegment
		_ = json.Unmarshal(recording["segments.json"], &segs)
		d.Segments = nonNilSegments(segs)
		if raw, err := os.ReadFile(e.Store.path(kind, id, "minutes.json")); err == nil && json.Valid(raw) {
			var r Recording
			if json.Unmarshal(recording["meta.json"], &r) == nil {
				raw = minutesWithSpan(raw, &r, e.Store.Zone())
			}
			d.Minutes = raw
		}
		if recentChars > 0 {
			raw := recording["transcript.md"]
			d.Recent = clip(string(raw), recentChars)
		}
	}
	if kind == KindChat && recentChars > 0 {
		d.Recent = e.Store.Since(id, time.Now().AddDate(0, 0, -7), recentChars)
	}
	if kind == KindTopic {
		d.Points = e.Store.TopicPoints(id)
	}
	var refs struct {
		Projects []string `json:"projects"`
		Chats    []string `json:"chats"`
		People   []string `json:"people"`
		Items    []string `json:"items"` // projects: "docs/<id>", "meetings/<id>", "events/<id>"
		Where    string   `json:"where"` // docs / meetings / events: the chat they came from
	}
	_ = json.Unmarshal(raw, &refs)
	if refs.Where != "" {
		refs.Chats = append(refs.Chats, refs.Where) // entries() drops it when it is not a chat ("calendar")
	}
	d.Links = append(e.entries(refs.Projects, refs.Chats, refs.People), e.itemEntries(refs.Items)...)
	return d, nil
}

// itemEntries names "kind/id" references to docs, meetings and events, newest first, at most 30 of each kind.
func (e *Engine) itemEntries(refs []string) []Entry {
	by := map[string][]Entry{}
	for _, r := range refs {
		kind, id, ok := strings.Cut(r, "/")
		if !ok {
			continue
		}
		if kind == KindRecording {
			if r, ok := e.Store.Recording(id); ok {
				by[kind] = append(by[kind], recordingEntry(r))
			}
			continue
		}
		if it, ok := e.Store.Item(kind, id); ok && it.Title != "" {
			by[kind] = append(by[kind], Entry{Kind: kind, ID: it.ID, Name: it.Title, OneLine: it.Note, Importance: it.Importance,
				Relevance: it.Relevance, Tags: it.Tags, Updated: it.At, Extra: it.URL})
		}
	}
	var out []Entry
	for _, k := range []string{KindDoc, KindMeeting, KindEvent, KindRecording} {
		list := by[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].Updated.After(list[j].Updated) })
		out = append(out, list[:min(len(list), 30)]...)
	}
	return out
}

// KindTags is the most used tags among entries (one kind's list), most used first.
func KindTags(list []Entry, n int) []string {
	count := map[string]int{}
	for _, en := range list {
		for _, t := range en.Tags {
			count[t]++
		}
	}
	tags := make([]string, 0, len(count))
	for t := range count {
		tags = append(tags, t)
	}
	sort.Slice(tags, func(i, j int) bool {
		if count[tags[i]] != count[tags[j]] {
			return count[tags[i]] > count[tags[j]]
		}
		return tags[i] < tags[j]
	})
	return tags[:min(len(tags), n)]
}

func tagUpdate(u Update, t *Tagging) {
	if u.Importance != "" || u.Relevance != nil {
		if u.Lock == nil || *u.Lock {
			t.Locked = true
		}
	}
	if importanceRe.MatchString(u.Importance) {
		t.Importance = u.Importance
	}
	if u.Relevance != nil && *u.Relevance >= 0 && *u.Relevance <= 100 {
		t.Relevance = *u.Relevance
	}
	if u.Tags != nil {
		t.Tags = u.Tags
	}
}

// Apply changes one entity as the user (the app, the assistant); for projects it creates the project when it does
// not exist (id = name or slug). Locked fields it touches become the user's: an extension's provenance on them goes.
func (e *Engine) Apply(kind, id string, u Update) (string, error) {
	if e.Store.ReadOnly() {
		return "", ErrReadOnly
	}
	e.Store.applyMu.Lock()
	defer e.Store.applyMu.Unlock()
	out, err := e.apply(kind, id, u)
	if err == nil && u.MergeInto == "" {
		err = e.claim(kind, out, u, "", nil, nil)
	}
	return out, err
}

func (e *Engine) apply(kind, id string, u Update) (string, error) {
	now := time.Now().UTC()
	switch kind {
	case KindProject:
		if u.MergeInto != "" {
			from, into := e.Store.Project(id)
			if !into {
				from = e.FindProject(id)
			}
			to, ok := e.Store.Project(u.MergeInto)
			if !ok {
				to = e.FindProject(u.MergeInto)
			}
			if from == nil || to == nil {
				return "", ErrNotFound
			}
			if err := e.mergeProjects(to.Slug, from.Slug, "手动合并", true); err != nil {
				return "", err
			}
			return to.Slug, nil
		}
		p, ok := e.Store.Project(id)
		if !ok {
			p = e.FindProject(id)
		}
		if p == nil {
			name := u.Name
			if name == "" {
				name = id
			}
			if Slug(name) == "" {
				return "", errors.New("a project needs a name")
			}
			p = &Project{Slug: Slug(name), Name: name, Kind: "short", Status: "active", Created: now}
		}
		if u.Name != "" {
			p.Name = u.Name
		}
		if u.Kind == "short" || u.Kind == "long" {
			p.Kind = u.Kind
		}
		if u.Status == "active" || u.Status == "paused" || u.Status == "done" || u.Status == "ignored" {
			p.Status = u.Status
		}
		if u.Goal != "" {
			p.Goal = oneLine(u.Goal)
		}
		if u.MyRole != "" {
			p.MyRole = oneLine(u.MyRole)
		}
		tagUpdate(u, &p.Tagging)
		protectUpdate(u, &p.Tagging)
		p.Aliases = addUnique(p.Aliases, u.Aliases...)
		p.Dirs = addUnique(p.Dirs, u.Dirs...)
		p.People = addUnique(p.People, u.People...)
		for _, pid := range u.People {
			_, _, err := e.Store.updatePerson(pid, true, nil, func(person *Person) bool {
				person.Projects = addUnique(person.Projects, p.Slug)
				if u.Lock == nil || *u.Lock {
					person.LockedFields = addUnique(person.LockedFields, "projects")
				}
				return true
			})
			if err != nil {
				return "", err
			}
		}
		for _, cid := range u.Chats {
			c, ok, err := e.Store.editChat(cid, func(c *Chat) {
				c.Projects = addUnique(c.Projects, p.Slug)
				if u.Lock == nil || *u.Lock {
					c.LockedFields = addUnique(c.LockedFields, "projects")
				}
			})
			if err != nil {
				return "", err
			}
			if ok {
				p.Chats = addUnique(p.Chats, c.ID)
				p.People = addUnique(p.People, c.People...)
			}
		}
		if u.Log != "" {
			src := u.Source
			if src == "" {
				src = "Persona"
			}
			if err := e.Store.AppendLog(p.Slug, now, src, u.Log); err != nil {
				return "", err
			}
			if u.Summary == "" {
				p.Dirty = true
			}
		}
		p.Aliases = removeStrings(p.Aliases, u.RemoveAliases)
		p.People = removeStrings(p.People, u.RemovePeople)
		p.Chats = removeStrings(p.Chats, u.RemoveChats)
		p.Updated = now
		if err := e.Store.putEntity(KindProject, p.Slug, p, &p.Tagging, true); err != nil {
			return "", err
		}
		if u.Summary != "" {
			if err := e.Store.putSummary(KindProject, p.Slug, u.Summary, true); err != nil {
				return "", err
			}
		}
		if err := e.unlink(KindProject, p.Slug, u); err != nil {
			return "", err
		}
		e.index(KindProject, p.Slug, p.Name, p.Tagging, p.OneLine, p.Updated)
		return p.Slug, nil
	case KindChat:
		var linked []*Project
		for _, slug := range u.Projects {
			if pr, ok := e.Store.Project(slug); ok {
				linked = append(linked, pr)
			}
		}
		// The chat is read again under chatMu and only the user's fields change: what an ingest wrote meanwhile stays.
		c, ok, err := e.Store.editChat(id, func(c *Chat) {
			tagUpdate(u, &c.Tagging)
			protectUpdate(u, &c.Tagging)
			if u.Name != "" {
				c.Name = u.Name
			}
			for _, pr := range linked {
				c.Projects = addUnique(c.Projects, pr.Slug)
			}
			c.People = removeStrings(c.People, u.RemovePeople)
			c.Projects = removeStrings(c.Projects, u.RemoveProjects)
		})
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrNotFound
		}
		for _, pr := range linked {
			pr.Chats = addUnique(pr.Chats, c.ID)
			if u.Lock == nil || *u.Lock {
				pr.LockedFields = addUnique(pr.LockedFields, "chats")
			}
			if err := e.Store.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, true); err != nil {
				return "", err
			}
		}
		if u.Summary != "" {
			if err := e.Store.putSummary(KindChat, c.ID, u.Summary, true); err != nil {
				return "", err
			}
		}
		if err := e.unlink(KindChat, c.ID, u); err != nil {
			return "", err
		}
		e.index(KindChat, c.ID, c.Name, c.Tagging, c.OneLine, c.LastAt)
		return c.ID, nil
	case KindPerson:
		if u.Status != "" && u.Status != "active" && u.Status != "ignored" && u.Status != "self" {
			return "", fmt.Errorf("unknown person status %q", u.Status)
		}
		p, ok := e.Store.Person(id)
		if !ok {
			return "", ErrNotFound
		}
		var linked []string
		for _, slug := range u.Projects {
			if pr, ok := e.Store.Project(slug); ok {
				linked = append(linked, pr.Slug)
				pr.People = addUnique(pr.People, p.ID)
				if u.Lock == nil || *u.Lock {
					pr.LockedFields = addUnique(pr.LockedFields, "people")
				}
				if err := e.Store.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, true); err != nil {
					return "", err
				}
			}
		}
		// Changed on the person as read again under personMu: what the pipeline linked meanwhile stays.
		p, _, err := e.Store.updatePerson(id, true, nil, func(p *Person) bool {
			tagUpdate(u, &p.Tagging)
			protectUpdate(u, &p.Tagging)
			if u.Note != "" {
				p.Note = oneLine(u.Note)
			}
			if u.Status != "" {
				p.Status = u.Status
			}
			p.Projects = addUnique(p.Projects, linked...)
			p.Chats = removeStrings(p.Chats, u.RemoveChats)
			p.Projects = removeStrings(p.Projects, u.RemoveProjects)
			return true
		})
		if err != nil {
			return "", err
		}
		if p == nil {
			return "", ErrNotFound // forgotten meanwhile
		}
		if err := e.unlink(KindPerson, p.ID, u); err != nil {
			return "", err
		}
		e.WritePersonSummary(p.ID)
		e.index(KindPerson, p.ID, p.Name, p.Tagging, p.Note, p.LastSeen)
		return p.ID, nil
	case KindDoc, KindMeeting, KindEvent:
		it, ok := e.Store.Item(kind, id)
		if !ok {
			return "", ErrNotFound
		}
		tagUpdate(u, &it.Tagging)
		protectUpdate(u, &it.Tagging)
		if u.Note != "" {
			it.Note = oneLine(u.Note)
		}
		for _, slug := range u.Projects {
			if pr, ok := e.Store.Project(slug); ok {
				it.Projects = addUnique(it.Projects, pr.Slug)
				pr.Items = addUnique(pr.Items, kind+"/"+SafeID(it.ID))
				if u.Lock == nil || *u.Lock {
					pr.LockedFields = addUnique(pr.LockedFields, "items")
				}
				if err := e.Store.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, true); err != nil {
					return "", err
				}
			}
		}
		it.Projects = removeStrings(it.Projects, u.RemoveProjects)
		if err := e.Store.putEntity(kind, it.ID, it, &it.Tagging, true); err != nil {
			return "", err
		}
		if err := e.unlink(kind, it.ID, u); err != nil {
			return "", err
		}
		e.index(kind, it.ID, it.Title, it.Tagging, it.Note, it.At)
		return it.ID, nil
	case KindTopic:
		// The curation owns the points (topics.go); by hand the name, the one line, aliases, tags and pinned facts,
		// which the merge reads and never overwrites.
		if u.Summary != "" {
			return "", errors.New("a topic's summary is written from its points: correct it with pinned facts")
		}
		e.Store.topicMu.Lock()
		defer e.Store.topicMu.Unlock()
		t, ok := e.Store.Topic(id)
		if !ok {
			return "", ErrNotFound
		}
		tagUpdate(u, &t.Tagging)
		protectUpdate(u, &t.Tagging)
		if u.Name != "" {
			t.Name = oneLine(u.Name)
		}
		if u.Note != "" {
			t.Note = oneLine(u.Note)
		}
		t.Aliases = removeStrings(addUnique(t.Aliases, u.Aliases...), u.RemoveAliases)
		if err := e.Store.putEntity(KindTopic, t.Slug, t, &t.Tagging, true); err != nil {
			return "", err
		}
		if u.Name != "" || u.Note != "" {
			f, err := e.Store.topicFile(t.Slug)
			if err != nil {
				return "", err
			}
			if err := e.Store.writeTopicSummary(t, f.Points); err != nil {
				return "", err
			}
		}
		e.index(KindTopic, t.Slug, t.Name, t.Tagging, t.Note, t.Updated)
		return t.Slug, nil
	case KindRecording:
		id, _ = RecordingID(id)
		// The phone owns the content (PutRecording); by hand only the tags, the pinned facts and the projects.
		e.Store.recMu.Lock()
		defer e.Store.recMu.Unlock()
		r, ok := e.Store.Recording(id)
		if !ok {
			return "", ErrNotFound
		}
		tagUpdate(u, &r.Tagging)
		protectUpdate(u, &r.Tagging)
		for _, slug := range u.Projects {
			if pr, ok := e.Store.Project(slug); ok {
				r.Projects = addUnique(r.Projects, pr.Slug)
				pr.Items = addUnique(pr.Items, kind+"/"+r.ID)
				if u.Lock == nil || *u.Lock {
					pr.LockedFields = addUnique(pr.LockedFields, "items")
				}
				if err := e.Store.putEntity(KindProject, pr.Slug, pr, &pr.Tagging, true); err != nil {
					return "", err
				}
			}
		}
		r.Projects = removeStrings(r.Projects, u.RemoveProjects)
		if err := e.Store.putEntity(kind, r.ID, r, &r.Tagging, true); err != nil {
			return "", err
		}
		if err := e.unlink(kind, r.ID, u); err != nil {
			return "", err
		}
		e.index(kind, r.ID, r.Name, r.Tagging, r.OneLine, r.StartedAt)
		return r.ID, nil
	}
	return "", fmt.Errorf("unknown kind %q", kind)
}

// ProjectsForDir returns the projects whose code directories contain dir on machine (Agents working there).
func (e *Engine) ProjectsForDir(machine, dir string) []*Project {
	var out []*Project
	for _, p := range e.Store.Projects() {
		if !p.Live() {
			continue
		}
		for _, d := range p.Dirs {
			m, path, ok := strings.Cut(d, ":")
			if !ok {
				m, path = "", d
			}
			if (m == "" || m == machine) && (dir == path || strings.HasPrefix(dir, strings.TrimRight(path, "/")+"/")) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// entries names and ranks linked entities (at most 30 chats and 30 people).
func (e *Engine) entries(projects, chats, people []string) []Entry {
	var out []Entry
	for _, slug := range projects {
		if p, ok := e.Store.Project(slug); ok && p.Live() {
			out = append(out, Entry{Kind: KindProject, ID: p.Slug, Name: p.Name, OneLine: p.OneLine, Importance: p.Importance,
				Relevance: p.Relevance, Tags: p.Tags, Updated: p.Updated, Extra: p.Kind + " · " + p.Status})
		}
	}
	var cs, ps []Entry
	for _, id := range chats {
		if c, ok := e.Store.Chat(id); ok {
			cs = append(cs, Entry{Kind: KindChat, ID: c.ID, Name: c.Name, OneLine: c.OneLine, Importance: c.Importance,
				Relevance: c.Relevance, Tags: c.Tags, Updated: c.LastAt, Extra: c.Kind})
		}
	}
	for _, id := range people {
		if p, ok := e.Store.Person(id); ok && p.Status != "ignored" && p.Status != "self" {
			ps = append(ps, Entry{Kind: KindPerson, ID: p.ID, Name: p.Name, OneLine: p.Note, Importance: p.Importance,
				Relevance: p.Relevance, Tags: p.Tags, Updated: p.LastSeen})
		}
	}
	Rank(cs)
	Rank(ps)
	return append(append(out, cs[:min(len(cs), 30)]...), ps[:min(len(ps), 30)]...)
}
