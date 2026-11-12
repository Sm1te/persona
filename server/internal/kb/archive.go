package kb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Defined in the contract package kbwire (docs/server/architecture.md "Contract packages"); the old names stay.
type (
	SourceChat    = kbwire.SourceChat
	SourceMessage = kbwire.SourceMessage
)

// Source is where chats come from (Feishu through the Lark extension).
type Source interface {
	Name() string // "lark"
	Chats(ctx context.Context, limit int) ([]SourceChat, error)
	// Messages returns messages newer than after (the latest page when after is ""), oldest first,
	// with the cursor to continue from.
	Messages(ctx context.Context, chat SourceChat, after string, limit int) (msgs []SourceMessage, newer string, more bool, err error)
	// Download saves one media path to dst.
	Download(ctx context.Context, media, dst string) error
}

// attributeChat keeps a connector's ownership separate from its archive-source name.
// Once attributed, another connector cannot append to or take over that chat.
func attributeChat(c *Chat, src Source) error {
	owner := src.Name()
	if named, ok := src.(interface{ ConnectorID() string }); ok && named.ConnectorID() != "" {
		owner = named.ConnectorID()
	}
	if c.Connector != "" && c.Connector != owner {
		return fmt.Errorf("archive connector mismatch for %s", c.ID)
	}
	c.Connector = owner
	return nil
}

// Engine runs the pipeline: archive, compress, evolve, tag, prune (docs/assistant/knowledge.md).
type Engine struct {
	// Changed is installed on Store by ObserveChanges before the pipeline starts.
	Changed func(Change)
	// MineObserved reports only owner identity metadata, never message contents.
	MineObserved func(source, name string, handles []Handle)
	Store        *Store
	Sources      []Source
	Model        Model
	ModelFor     func(string) Model
	Owner        string // how the owner is called (the AI writes the Chinese "I" for them); SetOwner changes it later
	Logf         func(string, ...any)
	// ExtraDirs are directories outside the data dir that Search also reads (Markdown files, read only): notes the
	// user keeps elsewhere, e.g. a git repository of facts. Absolute paths.
	ExtraDirs []string
	// Index mirrors an entity into the hub's Postgres index (optional).
	Index func(kind, id, name string, t Tagging, oneLine string, updated time.Time)
	// Unindex removes index rows (recordings deleted or past their retention; optional).
	Unindex func(kind string, ids []string)
	// RecordingDaysFunc is how many days recordings are kept (the assistant's recording_days; nil or <= 0: 14).
	RecordingDaysFunc func() int
	// MinutesDaysFunc is how many days a meeting's minutes are kept once its transcript went (the assistant's
	// minutes_days; nil or <= 0: forever); finite windows are never fewer than RecordingDaysFunc's (MinutesDays).
	MinutesDaysFunc func() int
	// EarlierMeetingsInAnswers lets the material of an answer (MeetingMaterial) and the room's meeting_earlier hold
	// earlier meetings: only those marked not private (meeting.go, "The private mark"). The manager turns it on.
	EarlierMeetingsInAnswers bool
	// MeetingChanged is told of recording id's meeting parts that changed (PartRoom, PartAsks, PartPrivate,
	// PartFollowup, PartPhotos): the manager's meeting_live event. Called outside the store's locks; optional.
	MeetingChanged func(id string, parts []string)
	// MinutesWritten is told when a recording's minutes for its current revision are written (minutes.go): the
	// assistant pushes them. Not for minutes made again only because a photo was added or deleted. Called from the
	// loop, outside the store's locks.
	MinutesWritten func(ctx context.Context, r Recording, m Minutes)
	// LiveChanged is told of recording id when what the attending feeder does may have changed (live.go): a stored
	// upload of a recording that attends or attended until then, and sentences LiveAppend stored. Called outside the
	// store's locks; optional.
	LiveChanged func(id string)
	// DayRecapWritten is told once a day (Recording.Day, across the day's parts) of the recap the day's push goes with
	// (day_recap_write.go "The push"): one ready at the recap time, else the day's first recap after it, or the final
	// recap of a day that had none; never one asked for before the recap time. The assistant pushes it, to the
	// destinations not in r.RecapPushTook, and returns those that took it now. Called from the recap lane
	// (WriteDayRecaps), outside the store's locks; the push is persisted as due before, so it is told again after a
	// restart, and after an error (the push not taken by all: the next round tells it again, after a short wait that
	// grows with each one failed, with those that took it kept in RecapPushTook), until a call returned nil or a day
	// went by since it became due. Optional: without it the push stays due, told once it is set.
	DayRecapWritten func(ctx context.Context, r Recording, d DayRecap) (took []string, err error)
	// DayRecapClock is the evening routine's time of day (whether or not the routine is on); the recap time is 30
	// minutes before it, in the owner's zone (Store.Zone, the routine's). Nil, or a time that is not one: 21:30.
	DayRecapClock func() (hour, minute int)
	// DayRecordingName is a day recording's name in the owner's language for its day (YYYY-MM-DD), unless the user
	// titled it; nil or "": the knowledge base's own.
	DayRecordingName func(day string) string
	// HandleKey is the hub's key for the senders' identity hints (handles.go); without it none are kept.
	HandleKey []byte
	// TopicsOn says whether the knowledge points curation runs (topics.go; assistant.kb_topics). nil: it does.
	TopicsOn func() bool

	// Limits (zero: defaults).
	ChatsPerRun  int // how many recently active chats an archive run looks at (100)
	FirstPage    int // how many messages a chat seen for the first time starts with (100)
	PagesPerChat int // pages read per chat and run (10)
	// AttachmentDays: attachments of older messages are not downloaded (they would be pruned anyway; 30).
	AttachmentDays int
	// AttachmentDaysBySource: AttachmentDays for the chats of one source (kb.attachment_days_by_source), e.g. years
	// for a chat source whose history is imported with its images; non-positive values are ignored.
	AttachmentDaysBySource map[string]int
	// TextDays: older messages are not written at all (365). Prune deletes such days, and anything that wrote them
	// again (a chat's first page, a mailbox's first read, imported history) would bring them back after every prune.
	TextDays int
	// Selection is the selection document (selection.go) that decides what daily ingestion, the history import and
	// pruning keep, read each time it is needed (nil result: Defaults; KeepEverything when the document cannot be
	// read now, under which nothing is pruned). A nil func means no selection at all: everything is kept for
	// TextDays, as before it.
	Selection func() *Selection

	// failed: attachment downloads that failed this run (logged once per chat); atomic, as a history import
	// (WriteHistory) may download while the archive runs.
	failed atomic.Int32

	find findCache // what Find read last time, kept while the files are unchanged (find_cache.go)

	// Retries are the steps' backoffs, shared by the engines a caller builds over the same store one after another (the
	// hub's settings changed), so they carry over (#4247); nil: the engine's own.
	Retries    *Retries
	ownRetries Retries

	// ownerNow is how the owner is called as the knowledge base loop last read it (SetOwner); its lanes read it while
	// it is set (#4247), so it is not the Owner field. Nil: Owner.
	ownerNow atomic.Pointer[string]

	// clock is what the day recordings take for now (tests); nil: time.Now.
	clock func() time.Time
	// jobLeft is how long a recap job has left (tests); nil: until its context's deadline.
	jobLeft func(ctx context.Context) time.Duration
}

// Retries are the knowledge base steps' failures and when each may be tried again (retrySet, one per step).
type Retries struct {
	projects   retrySet // projects whose rewrite failed, by slug (RefreshProjects)
	topics     retrySet // sources whose distill call failed ("<kind>/<id>") and topics whose merge failed ("topics/<slug>")
	recordings retrySet // recordings whose summary failed, by id (SummarizeRecordings)
	items      retrySet // documents, meetings and events whose tagging batch failed ("<kind>/<id>", TagItems)
}

func (e *Engine) retries() *Retries {
	if e.Retries != nil {
		return e.Retries
	}
	return &e.ownRetries
}

// SetOwner sets how the owner is called in the models' prompts from now on; safe while the lanes run.
func (e *Engine) SetOwner(owner string) { e.ownerNow.Store(&owner) }

// ownerName is how the owner is called: what SetOwner set last, else Owner.
func (e *Engine) ownerName() string {
	if o := e.ownerNow.Load(); o != nil {
		return *o
	}
	return e.Owner
}

func (e *Engine) modelFor(id string) Model {
	if e.ModelFor != nil {
		return e.ModelFor(id)
	}
	return e.Model
}

func (e *Engine) logf(f string, a ...any) {
	if e.Logf != nil {
		e.Logf("kb: "+f, a...)
	}
}

func (e *Engine) index(kind, id, name string, t Tagging, one string, updated time.Time) {
	if e.Index != nil {
		e.Index(kind, id, name, t, one, updated)
	}
}

func or(n, d int) int {
	if n <= 0 {
		return d
	}
	return n
}

// TextCutoff is the start of the oldest day Prune keeps (in the archive's zone): a message before it is past
// TextDays and is not written (the history import stops there too).
func (e *Engine) TextCutoff() time.Time { return e.textCutoff() }

// textCutoff is TextCutoff.
func (e *Engine) textCutoff() time.Time { return e.CutoffFor(e.TextDays) }

// Retention is the engine's retention settings, which the selection's null days fall back to.
func (e *Engine) Retention() Retention {
	return Retention{TextDays: e.TextDays, AttachmentDays: e.AttachmentDays, AttachmentDaysBySource: e.AttachmentDaysBySource}
}

// selection is the document in force: nil when the engine has none (Selection nil), Defaults when it returns nil.
func (e *Engine) selection() *Selection {
	if e.Selection == nil {
		return nil
	}
	if s := e.Selection(); s != nil {
		return s
	}
	d := Defaults()
	return &d
}

// CutoffFor is the start of the oldest day a line kept days days still has (in the archive's zone): a message
// before it is not written (TextCutoff is CutoffFor of TextDays).
func (e *Engine) CutoffFor(days int) time.Time {
	zone := e.Store.Zone()
	d := time.Now().AddDate(0, 0, -or(days, or(e.TextDays, 365))).In(zone)
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, zone)
}

// AttachmentDaysFor is how many days the attachments of a source's chats are kept: its AttachmentDaysBySource,
// else AttachmentDays (30), and never longer than TextDays. An attachment belongs to its line: a message past the
// text retention is not written (so its attachment is never downloaded) and its day file is deleted, so an
// attachment kept longer would be a file no line points to any more.
func (e *Engine) AttachmentDaysFor(source string) int {
	d := or(e.AttachmentDays, 30)
	if days := e.AttachmentDaysBySource[source]; days > 0 {
		d = days
	}
	return min(d, or(e.TextDays, 365))
}

// Prune deletes message days past TextDays and attachment days past each chat's AttachmentDaysFor: the same days
// textCutoff and line stop writing. With a selection, each line's days are its deciding rule's (PruneSelected), and
// documents past the days the user set for them go too (pruneDocs); a selection that cannot be read now
// (Selection.Stopgap) prunes nothing: the next round tries again. The count is of day files, attachment days and documents.
func (e *Engine) Prune(now time.Time) int {
	if sel := e.selection(); sel != nil {
		if sel.Stopgap() {
			return 0
		}
		return e.Store.PruneSelected(now, sel, e.Retention()) + e.pruneDocs(now, sel)
	}
	return e.Store.PruneBySource(now, or(e.TextDays, 365), e.AttachmentDaysFor)
}

// ChatID is the knowledge-base id of a source chat.
func ChatID(source, id string) string { return SafeID(source + "-" + id) }

// PersonID is the knowledge-base id of a sender.
func PersonID(source, id string) string { return SafeID(source + "-" + id) }

// Archive reads what is new in the recently active chats of every source and appends it to the day files.
// It returns the ids of chats that got new messages.
func (e *Engine) Archive(ctx context.Context) ([]string, error) {
	var changed []string
	for _, src := range e.Sources {
		chats, err := src.Chats(ctx, or(e.ChatsPerRun, 100))
		if err != nil {
			return changed, fmt.Errorf("%s chats: %w", src.Name(), err)
		}
		for _, sc := range chats {
			if ctx.Err() != nil {
				return changed, ctx.Err()
			}
			e.failed.Store(0)
			n, err := e.archiveChat(ctx, src, sc)
			if n := e.failed.Load(); n > 0 {
				e.logf("%s: %d attachment(s) not downloaded", sc.Name, n)
			}
			if err != nil {
				e.logf("archive %s %s: %v", src.Name(), sc.ID, err)
				continue
			}
			if n > 0 {
				changed = append(changed, ChatID(src.Name(), sc.ID))
			}
		}
	}
	return changed, nil
}

func (e *Engine) archiveChat(ctx context.Context, src Source, sc SourceChat) (int, error) {
	id := ChatID(src.Name(), sc.ID)
	c, ok := e.Store.Chat(id)
	if !ok {
		c = &Chat{ID: id, Source: src.Name(), SourceID: sc.ID, Kind: sc.Kind, Name: sc.Name, Created: time.Now().UTC()}
	}
	if err := attributeChat(c, src); err != nil {
		return 0, err
	}
	if sc.Name != "" {
		c.Name = sc.Name
	}
	if sc.Kind != "" {
		c.Kind = sc.Kind
	}
	// Nothing new since the last run (the list's time is the last message's).
	if ok && !sc.UpdatedAt.IsZero() && !sc.UpdatedAt.After(c.LastAt) {
		return 0, nil
	}
	cursor := c.Cursor
	limit := 100
	if cursor == "" {
		limit = or(e.FirstPage, 100)
	}
	total := 0
	cut := e.textCutoff()
	for page := 0; page < or(e.PagesPerChat, 10); page++ {
		msgs, newer, more, err := src.Messages(ctx, sc, cursor, limit)
		if err != nil {
			// Earlier pages are in the day files already: keep their cursor, or the next run writes them again.
			if total > 0 {
				if serr := e.saveArchived(c, cursor, total, sc.UpdatedAt); serr != nil {
					e.logf("archive %s: save progress: %v", c.ID, serr)
				}
			}
			return total, err
		}
		var lines []Line
		lastAt := c.LastAt
		for _, m := range msgs {
			// Already archived: the cursor's own message, or (topic groups, read from the newest page each time) anything
			// not newer than the last archived message. Past the retention: the newest page of a quiet chat goes back years.
			if m.Position != "" && m.Position == cursor || c.Messages > 0 && !m.At.After(c.LastAt) || m.At.Before(cut) {
				continue
			}
			l := e.line(ctx, src.Download, c, m)
			if l.Text == "" {
				continue
			}
			lines = append(lines, l)
			e.notePerson(c, src.Name(), m)
			c.Mine = c.Mine || m.Mine
			if m.At.After(c.LastAt) {
				c.LastAt = m.At
			}
		}
		if err := e.Store.AppendMessages(c, lines); err != nil {
			// This page is not archived: LastAt goes back to before it, or the next run skips its messages as seen.
			// The earlier pages are, so keep their cursor as when a fetch fails.
			c.LastAt = lastAt
			if total > 0 {
				if serr := e.saveArchived(c, cursor, total, sc.UpdatedAt); serr != nil {
					e.logf("archive %s: save progress: %v", c.ID, serr)
				}
			}
			return total, err
		}
		total += len(lines)
		if newer != "" {
			cursor = newer
		}
		// Topic groups only page backwards: one page per run, the newest.
		if !more || sc.Topic || newer == "" {
			break
		}
	}
	return total, e.saveArchived(c, cursor, total, sc.UpdatedAt)
}

// saveArchived records how far a chat's archive got: the cursor to go on from and the n lines written. c is the copy
// the run started from and changed as it read; the chat is read again under chatMu and gets only what the run owns
// (name, kind, cursor, LastAt, the owner's part, the people it noted, the counters), so what a history import or a
// compression wrote meanwhile stays. c gets the chat as written.
func (e *Engine) saveArchived(c *Chat, cursor string, n int, listed time.Time) error {
	e.Store.chatMu.Lock()
	defer e.Store.chatMu.Unlock()
	if err := e.Store.recoverIngest(c.ID); err != nil {
		return err
	}
	cur, ok := e.Store.Chat(c.ID)
	if !ok {
		cur = c
		cur.Messages, cur.Pending = 0, 0
	} else {
		cur.Name, cur.Kind = c.Name, c.Kind
		cur.Mine = cur.Mine || c.Mine
		for _, p := range c.People {
			cur.People = addUnique(cur.People, p)
		}
		if c.LastAt.After(cur.LastAt) {
			cur.LastAt = c.LastAt
		}
	}
	cur.Cursor = cursor
	cur.Messages += n
	cur.Pending += n
	if cur.LastAt.IsZero() {
		cur.LastAt = listed
	}
	if err := e.Store.PutChat(cur); err != nil {
		return err
	}
	*c = *cur
	e.index(KindChat, c.ID, c.Name, c.Tagging, c.OneLine, c.LastAt)
	return nil
}

var docLink = lazyre.New(`https?://[\w.-]+\.(?:feishu\.cn|larksuite\.com|larkoffice\.com)/(?:docx|docs|wiki|sheets|base|mindnotes|slides)/[A-Za-z0-9]+`)

// DownloadFunc saves one media path of a message to dst (Source.Download, or a connector's attachments).
type DownloadFunc func(ctx context.Context, media, dst string) error

// line turns a message into an archive line; images and files are saved under attachments/<day>/ while the message
// is within its source's attachment days (AttachmentDaysFor). download nil: none are.
func (e *Engine) line(ctx context.Context, download DownloadFunc, c *Chat, m SourceMessage) Line {
	return e.buildLine(ctx, download, c, m, false, e.AttachmentDaysFor(c.Source))
}

// buildLine is line with the message's attachment days (the selection's decision, else AttachmentDaysFor); history
// (WriteHistory) downloads images only, writes a placeholder naming the kind of any other attachment, and notes no
// documents (imported history is archive only: nothing for the model to tag).
func (e *Engine) buildLine(ctx context.Context, download DownloadFunc, c *Chat, m SourceMessage, history bool, attDays int) Line {
	sender := m.SenderName
	if m.Mine {
		sender = "我" // i18n-ignore: archive data
	}
	if sender == "" {
		sender = "?"
	}
	text := strings.TrimSpace(m.Text)
	if m.Type == "SYSTEM" && text == "" {
		return Line{}
	}
	day := m.At.In(e.Store.Zone()).Format("2006-01-02")
	recent := time.Since(m.At) <= time.Duration(attDays)*24*time.Hour
	if history {
		if !isImage(m.Type) && len(m.Media) == 0 && text == "" {
			if p := mediaPlaceholder(m.Type); p != attachmentPlaceholder {
				text = p // a voice message or video that came without its media reference
			}
		}
		for i, media := range m.Media {
			if !historyImage(m.Type, media) {
				kind := m.Type
				if kind == "POST" { // a post's embedded video or file, by its reference
					kind = mediaKind(kind, media)
				}
				text += " " + mediaPlaceholder(kind)
				continue
			}
			text += " " + e.attach(ctx, download, c, m, i, media, day, recent)
		}
		return Line{At: m.At, Sender: sender, Text: strings.TrimSpace(text)}
	}
	if !recent {
		if len(m.Media) > 0 {
			text += " " + attachmentPlaceholder
		}
		return Line{At: m.At, Sender: sender, Text: strings.TrimSpace(text)}
	}
	for i, media := range m.Media {
		text += " " + e.attach(ctx, download, c, m, i, media, day, true)
	}
	for _, u := range docLink.FindAllString(text, 5) {
		e.noteDoc(u, c, m.At)
	}
	return Line{At: m.At, Sender: sender, Text: text}
}

// attachmentPlaceholder stands for an attachment that is not saved (too old, not downloaded, or failed).
const attachmentPlaceholder = "[附件]" // i18n-ignore: archive data

// attach saves a message's i-th media under attachments/<day>/messages/ and returns the link the line gets, or the
// placeholder when it is not saved.
func (e *Engine) attach(ctx context.Context, download DownloadFunc, c *Chat, m SourceMessage, i int, media, day string, recent bool) string {
	if !recent || download == nil {
		return attachmentPlaceholder
	}
	name := attachmentName(m.ID, i, media, m.Type)
	dst := e.Store.path(KindChat, c.ID, "attachments", day, "messages", name)
	if err := download(ctx, media, dst); err != nil {
		if !errors.Is(err, ErrDownloadAborted) { // an aborted write keeps nothing of the line anyway
			e.failed.Add(1)
		}
		return attachmentPlaceholder
	}
	rel, _ := filepath.Rel(e.Store.path(KindChat, c.ID), dst)
	return fmt.Sprintf("%s(%s)", attachmentPlaceholder, rel)
}

// A filename must keep the raw identity, including punctuation, case and long
// suffixes. Lowercase full hashes stay distinct on case-insensitive filesystems.
// Missing IDs have no replay identity: give each delivery's attachment its own
// name instead of overwriting the previous anonymous message's file. Existing
// links and files keep their names; no legacy filename is reused or rewritten.
func attachmentName(id string, i int, media, typ string) string {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
	if id == "" {
		key = "anonymous-" + strings.ToLower(rand.Text())
	}
	return fmt.Sprintf("message-%s-%d%s", key, i, extOf(media, typ))
}

// isImage: a message whose media are pictures (a rich-text post's are its inline images).
func isImage(typ string) bool { return typ == "IMAGE" || typ == "POST" }

// historyImage: imported history downloads this media of a message. An image message's are pictures; a rich-text
// post's are mostly its inline images, but a post can embed a video or a file too, so one whose reference names a
// file type that is no picture is not downloaded (the import downloads images only; the downloader also refuses
// what does not come back as an image).
func historyImage(typ, media string) bool {
	switch typ {
	case "IMAGE":
		return true
	case "POST":
		return mediaKind(typ, media) == typ
	}
	return false
}

// mediaKind is the type a media reference says it is by its file extension (VIDEO, FILE, AUDIO), else typ.
func mediaKind(typ, media string) string {
	ext := strings.ToLower(filepath.Ext(strings.SplitN(media, "?", 2)[0]))
	switch ext {
	case ".mp4", ".mov", ".m4v", ".avi", ".mkv", ".webm":
		return "VIDEO"
	case ".mp3", ".m4a", ".aac", ".amr", ".wav", ".ogg", ".opus", ".silk":
		return "AUDIO"
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".rar", ".7z", ".txt", ".csv", ".key", ".pages", ".numbers":
		return "FILE"
	}
	return typ
}

// mediaPlaceholder names the kind of an attachment imported history does not download.
func mediaPlaceholder(typ string) string {
	switch typ {
	case "VIDEO", "MEDIA":
		return "[视频]" // i18n-ignore: archive data
	case "FILE":
		return "[文件]" // i18n-ignore: archive data
	case "AUDIO", "VOICE":
		return "[语音]" // i18n-ignore: archive data
	}
	return attachmentPlaceholder
}

func extOf(media, typ string) string {
	if ext := filepath.Ext(strings.SplitN(media, "?", 2)[0]); ext != "" && len(ext) <= 6 {
		return ext
	}
	if typ == "IMAGE" || typ == "POST" {
		return ".jpg"
	}
	return ""
}

// notePerson links the sender and the chat both ways. The person is read again and written under personMu
// (updatePerson), so another writer's chats, projects or tags are kept.
func (e *Engine) notePerson(c *Chat, source string, m SourceMessage) {
	if m.Mine {
		if e.MineObserved != nil {
			handles := slices.Clone(m.SenderHandles)
			if m.SenderID != "" {
				handles = append(handles, Handle{Kind: source, Value: m.SenderID})
			}
			e.MineObserved(source, m.SenderName, handles)
		}
		return
	}
	if m.SenderID == "" {
		return
	}
	pid := PersonID(source, m.SenderID)
	linked := false
	create := func() *Person {
		return &Person{ID: pid, Source: source, SourceID: m.SenderID, Name: m.SenderName, Connector: c.Connector}
	}
	p, written, err := e.Store.updatePerson(pid, false, create, func(p *Person) bool {
		if p.Status == "ignored" || (p.Connector != "" && p.Connector != c.Connector) {
			return false
		}
		linked = true
		attributed := false
		if p.Connector == "" && c.Connector != "" {
			known := len(p.Chats) > 0
			for _, id := range p.Chats {
				if id == c.ID {
					continue
				}
				other, exists := e.Store.Chat(id)
				if !exists || other.Connector != c.Connector {
					known = false
					break
				}
			}
			if known {
				p.Connector = c.Connector
				attributed = true
			}
		}
		if m.SenderName != "" {
			p.Name = m.SenderName
		}
		before, seen := len(p.Chats), p.LastSeen
		p.Chats = addUnique(p.Chats, c.ID)
		if m.At.After(p.LastSeen) {
			p.LastSeen = m.At
		}
		e.addHandles(pid, m.SenderHandles)
		// Only write when something a reader cares about changed (not on every message of a busy group).
		return !p.persisted || attributed || len(p.Chats) != before || p.LastSeen.Sub(seen) > time.Hour
	})
	if linked {
		c.People = addUnique(c.People, pid)
	}
	if written && err == nil {
		e.index(KindPerson, p.ID, p.Name, p.Tagging, p.Note, p.LastSeen)
	}
}

// noteDoc remembers a document link seen in a chat. Under a selection it writes no stub for a document the user keeps
// from being read (a per-item read false) nor one already past its days (Selection.DocDays: a per-item days, as a stub
// has no signals), so neither an excluded nor a pruned document comes back through an old message that links it.
func (e *Engine) noteDoc(u string, chat *Chat, at time.Time) {
	id := SafeID(u[strings.LastIndex(u, "/")+1:])
	if _, ok := e.Store.Item(KindDoc, id); ok {
		return
	}
	now := time.Now().UTC()
	it := &Item{Kind: KindDoc, ID: id, URL: u, At: at, Where: chat.ID, Source: chat.Connector, Seen: now}
	if sel := e.selection(); sel != nil {
		if x := sel.Item(chat.Connector, ItemDoc, id); x != nil && !x.Read {
			return
		}
		if days, ok := sel.DocDays(DocFacts{Source: chat.Connector, ID: id}); ok && docExpired(it, now, days) {
			return
		}
	}
	_ = e.Store.PutItem(it)
}
