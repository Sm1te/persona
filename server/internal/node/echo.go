package node

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// Which transcript line is the message an app sent (docs/protocol/protocol.md "Matching pending messages"). The app sends with a
// client_msg_id and shows a pending bubble until the transcript has the message, but Claude Code, Codex and the
// runtimes give that line ids of their own. After a send that reached the pane (or may have), the node expects its
// copy: in the order sent, the first user line written after the transcript's end at the time it was typed whose words
// fit what was typed. Every item of that line (its words, its pictures; the notice a /compact became) goes out with
// the client_msg_id, in pushes and pages alike. The hub keeps what the node expects and found (sent_messages.echo,
// told with session.echo) and hands it back to a node that started again (session.echoes): the link outlives the node
// process, and a message the node was still waiting for is found after the restart too.

// echoRecord is one send the node ties to the transcript: expected while Line is "", found after.
type echoRecord struct {
	SessionID   string    `json:"session_id"`
	ClientMsgID string    `json:"client_msg_id"`
	Text        string    `json:"text,omitempty"` // the start of what was typed (echoTextLen runes)
	Images      int       `json:"images,omitempty"`
	SentAt      time.Time `json:"sent_at"`
	AfterSeq    int       `json:"after_seq"`      // the transcript's last line before it was typed
	Line        string    `json:"line,omitempty"` // the line found: its items' id before "#"
	// scanned: lines through this seq were looked at for it and did not fit (a line stays as it was parsed).
	scanned int
}

const (
	// echoTextLen: what a record keeps of the text; matching looks at no more than echoPrefix of it.
	echoTextLen = 200
	// echoPrefix: a text this long or longer fits a line that starts with the same words (the page cuts long texts,
	// Claude Code turns pictures into chips); a shorter one must be the same.
	echoPrefix = 12
	// echoWithin: a line written later than this after the send is not its copy (Codex writes a message sent mid-turn
	// when the turn ends); the record stops being looked for.
	echoWithin = 2 * time.Hour
	// echoKeep: how long a found line keeps its client_msg_id here. The hub keeps sent_messages for good but hands a
	// restarted node only the last day's echoes (db.Echoes): keep the two in step.
	echoKeep = 24 * time.Hour
	// echoSlack: the transcript's writer runs on this computer; a line stamped a little before the send is still it.
	echoSlack = 5 * time.Second
	// echoPerSession bounds the records of one session.
	echoPerSession = 200
)

type echoLog struct {
	mu   sync.Mutex
	recs map[string][]*echoRecord // session id -> records in the order sent
}

// expect records a send to tie; false when it was known already (a record handed back by the hub).
func (l *echoLog) expect(r echoRecord) bool {
	if r.SessionID == "" || r.ClientMsgID == "" {
		return false
	}
	r.Text = echoClip(r.Text)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.recs == nil {
		l.recs = map[string][]*echoRecord{}
	}
	list := l.recs[r.SessionID]
	for _, o := range list {
		if o.ClientMsgID == r.ClientMsgID {
			if o.Line == "" && r.Line != "" {
				o.Line = r.Line
			}
			return false
		}
	}
	list = append(list, &r)
	sort.SliceStable(list, func(i, j int) bool { return list[i].SentAt.Before(list[j].SentAt) })
	now := time.Now()
	list = slices.DeleteFunc(list, func(o *echoRecord) bool { return now.Sub(o.SentAt) > echoKeep })
	if len(list) > echoPerSession {
		list = list[len(list)-echoPerSession:]
	}
	l.recs[r.SessionID] = list
	return true
}

// record is the session's record of clientMsgID as it is now.
func (l *echoLog) record(id, clientMsgID string) (echoRecord, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.recs[id] {
		if r.ClientMsgID == clientMsgID {
			return *r, true
		}
	}
	return echoRecord{}, false
}

// forget drops the record of a send that did not reach the pane.
func (l *echoLog) forget(id, clientMsgID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recs[id] = slices.DeleteFunc(l.recs[id], func(r *echoRecord) bool { return r.ClientMsgID == clientMsgID })
}

// waitingFrom is the seq after which the session's expected records look for their lines; false when none is
// expected.
func (l *echoLog) waitingFrom(id string) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	from, waiting := 0, false
	for _, r := range l.recs[id] {
		if r.Line != "" || time.Since(r.SentAt) > echoWithin {
			continue
		}
		at := max(r.AfterSeq, r.scanned)
		if !waiting || at < from {
			from = at
		}
		waiting = true
	}
	return from, waiting
}

// match ties the session's expected records to lines among items (the transcript after waitingFrom, in order) and
// returns the ones found now.
func (l *echoLog) match(id string, items []proto.Item) []echoRecord {
	lines := echoLines(items)
	l.mu.Lock()
	defer l.mu.Unlock()
	recs := l.recs[id]
	taken := map[string]bool{}
	for _, r := range recs {
		if r.Line != "" {
			taken[r.Line] = true
		}
	}
	var found []echoRecord
	// A runtime that tied its own line says so on the item: that is the record's line, and no other's.
	for _, ln := range lines {
		for _, it := range ln.items {
			if it.ClientMsgID == "" {
				continue
			}
			taken[ln.key] = true
			for _, r := range recs {
				if r.ClientMsgID == it.ClientMsgID && r.Line == "" {
					r.Line = ln.key
					found = append(found, *r)
				}
			}
		}
	}
	last := 0
	if len(items) > 0 {
		last = items[len(items)-1].Seq
	}
	for _, r := range recs {
		if r.Line != "" || time.Since(r.SentAt) > echoWithin {
			continue
		}
		for _, ln := range lines {
			if taken[ln.key] || ln.seq <= r.AfterSeq || ln.seq <= r.scanned {
				continue
			}
			if !ln.ts.IsZero() && (ln.ts.Before(r.SentAt.Add(-echoSlack)) || ln.ts.After(r.SentAt.Add(echoWithin))) {
				continue
			}
			if echoFits(r, ln.items) {
				r.Line, taken[ln.key] = ln.key, true
				found = append(found, *r)
				break
			}
		}
		if r.Line == "" {
			// Looked at all of these: only later lines can be its copy. A later record may still take one of them
			// (they are claimed in the order sent), so this only moves its own start.
			r.scanned = max(r.scanned, last)
		}
	}
	return found
}

// tag puts the client_msg_id of found records on their lines' items (a copy when anything changes).
func (l *echoLog) tag(id string, items []proto.Item) []proto.Item {
	l.mu.Lock()
	var byLine map[string]string
	for _, r := range l.recs[id] {
		if r.Line != "" {
			if byLine == nil {
				byLine = map[string]string{}
			}
			byLine[r.Line] = r.ClientMsgID
		}
	}
	l.mu.Unlock()
	if byLine == nil {
		return items
	}
	var out []proto.Item
	for i, it := range items {
		c, ok := byLine[echoLineKey(it.ID)]
		if !ok || it.ClientMsgID == c {
			continue
		}
		if out == nil {
			out = slices.Clone(items)
		}
		out[i].ClientMsgID = c
	}
	if out == nil {
		return items
	}
	return out
}

// echoLine is one transcript line's items as the apps see them.
type echoLine struct {
	key   string
	seq   int
	ts    time.Time
	items []proto.Item
}

// echoLineKey: the items of one transcript line share the id before "#" (Claude Code's uuid, Codex's item id).
func echoLineKey(id string) string {
	if i := strings.LastIndexByte(id, '#'); i >= 0 {
		return id[:i]
	}
	return id
}

// echoLines groups items into the lines that can be a message's copy: ones with something the user wrote (not
// another session's message, not a tool's result), or the notice a /compact became.
func echoLines(items []proto.Item) []echoLine {
	var out []echoLine
	at := map[string]int{}
	for _, it := range items {
		k := echoLineKey(it.ID)
		i, ok := at[k]
		if !ok {
			i = len(out)
			at[k] = i
			out = append(out, echoLine{key: k, seq: it.Seq, ts: it.TS})
		}
		out[i].items = append(out[i].items, it)
	}
	return slices.DeleteFunc(out, func(ln echoLine) bool {
		for _, it := range ln.items {
			// Another session's message is not the user's send — except one signed peerSender: the node's own send
			// through a claude's peer inbox (claudepeer.go), which the session writes as a peer's message.
			if it.From != "" && it.From != peerSender || it.Task != nil {
				return true
			}
		}
		for _, it := range ln.items {
			if it.Role == "user" && (it.Kind == proto.KindText || it.Kind == proto.KindImage) ||
				it.Role == "system" && it.Kind == proto.KindNotice && it.Text == transcript.CompactNotice {
				return false
			}
		}
		return true
	})
}

var echoDropped = []*lazyre.Regexp{
	transcript.ContextRe,
	transcript.ViewErrorsRe, // the assistant's view feedback before the message
	lazyre.New(`(?s)<system-reminder>.*?</system-reminder>`), // a hook's context on the user's line
	lazyre.New(`(?s)<attached_images>.*?</attached_images>`),
	lazyre.New(`<[\\/]*pasted_content[^>]*>`),
	boxChips, // [Image #1], [Pasted text #2 …]
}

// echoClip is what a record keeps of a text: without the notes (a cut must not leave half of one), its start.
func echoClip(s string) string {
	for _, re := range echoDropped {
		s = re.ReplaceAllString(s, "")
	}
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > echoTextLen {
		s = string(rs[:echoTextLen])
	}
	return s
}

// echoWords is the comparable form of a text: the notes Persona and the agents add removed, no whitespace, without
// the line the page adds where it cut a long one.
func echoWords(s string) []rune {
	if i := strings.LastIndex(s, "\n…（已截断"); i >= 0 {
		s = s[:i]
	}
	for _, re := range echoDropped {
		s = re.ReplaceAllString(s, "")
	}
	return []rune(strings.Join(strings.Fields(s), ""))
}

// echoFits: the line's words are what was typed — the same, or for a long text the same start. A message of only
// pictures fits a line with none; a bare command fits the command with the arguments picked for it; /compact fits the
// notice it became.
func echoFits(r *echoRecord, line []proto.Item) bool {
	if inert := inertControls(r.Text); inert != r.Text { // a terminal is pasted the inert text (hostPrompt), a runtime the text
		e := *r
		e.Text = inert
		if echoFits(&e, line) {
			return true
		}
	}
	var words []rune
	user, notice := false, false
	first := "" // the line's first word
	for _, it := range line {
		switch {
		case it.Role == "user" && it.Kind == proto.KindText:
			if f := strings.Fields(it.Text); first == "" && len(f) > 0 {
				first = f[0]
			}
			words, user = append(words, echoWords(it.Text)...), true
		case it.Role == "user" && it.Kind == proto.KindImage:
			user = true
		case it.Role == "system" && it.Kind == proto.KindNotice:
			notice = true
		}
	}
	sent := echoWords(r.Text)
	if !user {
		return notice && string(sent) == "/compact"
	}
	if len(sent) == 0 {
		return r.Images > 0 && len(words) == 0
	}
	if bare := strings.TrimSpace(r.Text); strings.HasPrefix(bare, "/") && !strings.ContainsFunc(bare, unicode.IsSpace) && first == bare {
		return true // a command picked its arguments in the terminal (/model opens a picker: "/model opus")
	}
	if len(sent) < echoPrefix {
		return string(words) == string(sent)
	}
	return len(words) >= echoPrefix && string(words[:echoPrefix]) == string(sent[:echoPrefix])
}

// echoBase is where the session's transcript ends before a send is typed (what it has so far is read, and pushed to
// a watching hub, first); 0 when there is none yet (a session still starting).
func (n *Node) echoBase(id string) int {
	f, err := n.file(id)
	if err != nil {
		return 0
	}
	n.refresh(id, f)
	return f.LastSeq()
}

// sendEchoed runs send and records it to tie; once it reached the pane, or may have, the hub is told (it hands the
// record back after a restart), and a send that surely did not is forgotten.
//
// The record is expected before the words are typed: Claude Code writes the line as soon as it takes the message, and
// a watch that pushed it before the send returned sent it without its client_msg_id, never to carry it in a push
// again (the app's bubble then waited for a page).
func (n *Node) sendEchoed(id, clientMsgID, text string, images int, after int, send func() error) error {
	if clientMsgID == "" {
		return send()
	}
	r := echoRecord{SessionID: id, ClientMsgID: clientMsgID, Text: text, Images: images, SentAt: time.Now(), AfterSeq: after}
	fresh := n.echoes.expect(r)
	err := send()
	if !fresh {
		return err
	}
	if err == nil || maybeDelivered(err) {
		if cur, ok := n.echoes.record(id, clientMsgID); ok {
			n.notifyEcho(cur) // found already (a push ran meanwhile): the hub hears the line too
		}
	} else {
		n.echoes.forget(id, clientMsgID)
	}
	return err
}

// expectFirstLine ties a new conversation's first message to its line once the conversation's id is known (a start
// that did not name it): sent at at, after nothing, the hub told as after a send.
func (n *Node) expectFirstLine(id, clientMsgID, text string, images int, at time.Time) {
	if !n.echoes.expect(echoRecord{SessionID: id, ClientMsgID: clientMsgID, Text: text, Images: images, SentAt: at}) {
		return
	}
	if cur, ok := n.echoes.record(id, clientMsgID); ok {
		n.notifyEcho(cur)
	}
}

func (n *Node) notifyEcho(r echoRecord) {
	if n.peer == nil {
		return
	}
	r.Text = echoClip(r.Text)
	_ = n.peer.Notify("session.echo", r)
}

// echoed ties the session's expected sends to its transcript (f; nil when it is not open) and returns items with the
// client_msg_ids of the lines found.
func (n *Node) echoed(id string, f *transcript.File, items []proto.Item) []proto.Item {
	if from, ok := n.echoes.waitingFrom(id); ok && f != nil {
		page, _, _ := f.After(from, 2000)
		for _, r := range n.echoes.match(id, page) {
			n.notifyEcho(r)
		}
	}
	return n.echoes.tag(id, items)
}

// restoreEchoes takes back the records the hub kept (session.echoes, after the node started).
func (n *Node) restoreEchoes(list []echoRecord) {
	for _, r := range list {
		n.echoes.expect(r)
	}
}

// known: the session has records (found or expected), so its items are worth looking at.
func (l *echoLog) known(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recs[id]) > 0
}

// A runtime's session (runtimes.go) has no transcript here: its items are tied as they pass, a batch (a push or a
// page) at a time, and the transcript's end before a send is its latest item. A runtime that knows the line itself
// may set client_msg_id on its items; that is kept. Before a send the node knows the latest seq only of a session it
// streams (no extra call to the runtime); otherwise the send's time alone says which lines came after it.

// echoedRaw ties and tags the items under field of a runtime's batch; anything that does not read as one passes as
// it came.
func (n *Node) echoedRaw(id string, raw json.RawMessage, field string) json.RawMessage {
	if !n.echoes.known(id) {
		return raw
	}
	var batch map[string]json.RawMessage
	var items []proto.Item
	if json.Unmarshal(raw, &batch) != nil || json.Unmarshal(batch[field], &items) != nil || len(items) == 0 {
		return raw
	}
	for _, r := range n.echoes.match(id, items) {
		n.notifyEcho(r)
	}
	tagged := n.echoes.tag(id, items)
	if tagged[0].ClientMsgID == items[0].ClientMsgID && slices.Equal(echoIDs(tagged), echoIDs(items)) {
		return raw // nothing changed
	}
	b, err := json.Marshal(tagged)
	if err != nil {
		return raw
	}
	batch[field] = b
	out, err := json.Marshal(batch)
	if err != nil {
		return raw
	}
	return out
}

func echoIDs(items []proto.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ClientMsgID
	}
	return out
}
