package assistant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
)

// GET /api/v1/inbox keeps each listed item's JSON and writes the answer from those bytes. It used to copy every item
// (listed) and encode all of them again on every GET: about 3.8 ms and 1.9 MB at 400 items, the manager's largest
// share of an inbox push, which every app answers with a GET.
//
// listed depends on the item alone (no clock, no request, no setting), so an item's bytes stay right until the item
// changes. A change the rollback bookkeeping stamps (touched: settle, a filing, a parent link) forgets the item's
// bytes at once; every change is saved through saveInboxPrepared, which forgets them as it starts (before it waits for
// another save), as it takes the rows and after its finish (a failed row's undo). Saved is not soon enough for a
// change that makes a call before its save (a reply, a hand-off's Agent, a group append, an event's conversation
// being started) or is never saved itself (a reply kept open by a move meanwhile): those forget as they change the
// item, in chainAdd (every chain entry), putEvent and startInboxAgents. An item replaced under its id (loadInbox,
// admission, the history, grouping) is told apart by its pointer, a dropped one by drop and the sweep below. Tests
// check every kept item against a fresh encoding on every list (inboxMemoVerify).

// inboxListMemoOn: off, GET /api/v1/inbox encodes every item again, as before the memo.
const inboxListMemoOn = true

// inboxMemoVerify is set by the package's tests (TestMain): every list also encodes each kept item again and notes a
// difference (inboxMemoStale), so a change of an item that forgets nothing (touched, chainAdd, saveInboxPrepared)
// fails the run.
var inboxMemoVerify bool

// inboxListMemo is the items' kept bytes, under inboxBook.mu.
type inboxListMemo struct {
	kept    map[string]inboxListed
	tok     uint64 // the last token handed to an encoding in flight
	encoded int    // items encoded for a GET so far (tests)
}

// inboxListed is one item's bytes as listed, for that item (pointer): nil raw is an encoding in flight, stored only by
// the GET holding tok, and only while nothing forgot it meanwhile.
type inboxListed struct {
	it  *InboxItem
	raw []byte
	tok uint64
}

// inboxListOrder is the list's order: newest first, and by id among items of the same moment (mail and Feishu items
// are often filed together). The items come from a map: without the id, the same list came out in another order on
// every request, so the merged answer's validator never matched (#4739).
func inboxListOrder(at1 time.Time, id1 string, at2 time.Time, id2 string) int {
	if c := at2.Compare(at1); c != 0 {
		return c
	}
	return strings.Compare(id1, id2)
}

// forgetListed drops the kept bytes of the items changed, under b.mu.
func (b *inboxBook) forgetListed(changed []*InboxItem) {
	for _, it := range changed {
		delete(b.listMemo.kept, it.ID)
	}
}

// inboxListRow is one listed item of a GET: its bytes, or the copy to encode once b.mu is let go.
type inboxListRow struct {
	it  *InboxItem
	at  time.Time
	raw []byte
	c   *InboxItem
	tok uint64
}

// inboxListRaw is inboxListAt as GET /api/v1/inbox writes it: each item's JSON, newest first, and as_of. Only the
// items without kept bytes are copied (under b.mu) and encoded (after it). False when an item cannot be encoded.
func (s *Service) inboxListRaw(all bool) ([][]byte, time.Time, bool) {
	out, _, now, ok := s.inboxListBytes(all, false)
	return out, now, ok
}

// inboxListRawIDs is inboxListRaw with each item's id beside its bytes (GET /api/v1/inbox?since, inbox_delta.go).
func (s *Service) inboxListRawIDs(all bool) ([][]byte, []string, time.Time, bool) {
	return s.inboxListBytes(all, true)
}

// inboxListBytes is inboxListRaw, with the ids too when withIDs (a plain GET allocates nothing more for them).
func (s *Service) inboxListBytes(all, withIDs bool) ([][]byte, []string, time.Time, bool) {
	b := &s.inbox
	m := &b.listMemo
	b.mu.Lock()
	now := time.Now()
	cutoff := now.Add(-inboxKeepDone)
	if m.kept == nil {
		m.kept = make(map[string]inboxListed, len(b.items))
	}
	rows := make([]inboxListRow, 0, len(b.items))
	misses := 0
	for _, it := range b.items {
		if it.DoneAt != nil && it.DoneAt.Before(cutoff) || !all && it.Connector != nil {
			continue
		}
		it.arrived(now)
		row := inboxListRow{it: it, at: it.UpdatedAt}
		if e := m.kept[it.ID]; e.it == it && e.raw != nil {
			row.raw = e.raw
			if inboxMemoVerify {
				verifyListed(it, e.raw)
			}
		} else {
			c := it.listed()
			m.tok++
			row.c, row.tok = &c, m.tok
			m.kept[it.ID] = inboxListed{it: it, tok: m.tok}
			misses++
		}
		rows = append(rows, row)
	}
	if len(m.kept) > len(b.items) { // items gone some other way than drop (a new item's row undone)
		for id, e := range m.kept {
			if b.items[id] != e.it {
				delete(m.kept, id)
			}
		}
	}
	b.mu.Unlock()
	slices.SortFunc(rows, func(x, y inboxListRow) int { return inboxListOrder(x.at, x.it.ID, y.at, y.it.ID) })
	out := make([][]byte, len(rows))
	var ids []string
	if withIDs {
		ids = make([]string, len(rows))
	}
	for i := range rows {
		if withIDs {
			ids[i] = rows[i].it.ID
		}
		if rows[i].c != nil {
			raw, err := json.Marshal(rows[i].c) // by pointer, as an element of the []InboxItem it was encoded in
			if err != nil {
				return nil, nil, now, false
			}
			rows[i].raw = raw
		}
		out[i] = rows[i].raw
	}
	if misses > 0 {
		b.mu.Lock()
		for _, r := range rows {
			if r.c == nil {
				continue
			}
			if e, ok := m.kept[r.it.ID]; ok && e.raw == nil && e.tok == r.tok {
				m.kept[r.it.ID] = inboxListed{it: r.it, raw: r.raw}
			}
		}
		m.encoded += misses
		b.mu.Unlock()
	}
	return out, ids, now, true
}

// writeInboxList writes GET /api/v1/inbox's answer for all (and POST /api/v1/inbox/refresh's): the bytes
// apierr.WriteJSON wrote of map[string]any{"items": items, "as_of": asOf, "triage": triage}, its keys sorted, HTML
// escaped as encoding/json escapes it and a newline at the end, with each item's kept bytes in place.
func (s *Service) writeInboxList(w http.ResponseWriter, all bool) {
	if inboxListMemoOn {
		if items, asOf, ok := s.inboxListRaw(all); ok && writeInboxItems(w, items, asOf, s.inboxTriage()) {
			return
		}
	}
	items, asOf := s.inboxListAt(all)
	apierr.WriteJSON(w, map[string]any{"items": items, "as_of": asOf, "triage": s.inboxTriage()})
}

// writeInboxItems writes the answer from the items' bytes; false, nothing written, when as_of or triage cannot be
// encoded (encoding/json then writes nothing either).
func writeInboxItems(w http.ResponseWriter, items [][]byte, asOf time.Time, triage InboxTriage) bool {
	at, err := json.Marshal(asOf)
	if err != nil {
		return false
	}
	tr, err := json.Marshal(triage)
	if err != nil {
		return false
	}
	const head, mid, tail = `{"as_of":`, `,"items":[`, `],"triage":`
	n := len(head) + len(at) + len(mid) + max(len(items)-1, 0) + len(tail) + len(tr) + len("}\n")
	for _, raw := range items {
		n += len(raw)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if g, ok := w.(interface{ Grow(int) }); ok { // the bus's recorder (serveHTTP): one buffer of the answer's size
		g.Grow(n)
	}
	put := func(p []byte) { _, _ = w.Write(p) }
	put(append(append([]byte(head), at...), mid...))
	for i, raw := range items {
		if i > 0 {
			put(comma)
		}
		put(raw)
	}
	put(append(append(append([]byte(tail), tr...), '}'), '\n'))
	return true
}

var comma = []byte{','}

// verifyListed (tests) notes an item whose kept bytes are not its encoding now. Under b.mu.
func verifyListed(it *InboxItem, kept []byte) {
	c := it.listed()
	if fresh, err := json.Marshal(&c); err == nil && !bytes.Equal(fresh, kept) {
		inboxMemoStale.note(fmt.Sprintf("inbox item %s listed from stale bytes\nkept:  %s\nfresh: %s\n%s", it.ID, kept, fresh, debug.Stack()))
	}
}

// inboxMemoStale is what verifyListed found.
var inboxMemoStale staleNotes

type staleNotes struct {
	mu    sync.Mutex
	notes []string
}

func (n *staleNotes) note(s string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.notes) < 20 {
		n.notes = append(n.notes, s)
	}
}

func (n *staleNotes) taken() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.notes
	n.notes = nil
	return out
}
