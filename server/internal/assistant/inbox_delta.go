package assistant

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"hash/maphash"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GET /api/v1/inbox?since=<cursor> (#4740, docs/protocol/protocol.md "Inbox delta"): the items added or changed since
// the list the cursor names, and the ids gone from it, instead of the whole list. On a busy hub the list held about
// 1,650 items (3.4 MB, nearly all of them done ones kept for undo) while it changed about twice a minute, one or two
// items at a time, and every app fetched all of it on each {"type":"inbox"}.
//
// The manager keeps the last lists it answered with a cursor: per item, a sum of the bytes it was listed with. A delta
// is the list now against the list then: an item whose bytes differ, or that is new, is in `items`; an id the list no
// longer has is in `removed`, whatever took it out (filed long enough ago, forgotten, dropped, a rollback). A list
// answered again with the same items gets the cursor it had (inboxSnapshots.note), so repeated reads by several apps
// keep the few kept lists apart. A cursor this run did not hand out, one dropped (more than inboxSnapshotsMax newer
// lists) or left unused for inboxSnapshotTTL, and one for the other `sources`, get the whole list again with a new
// cursor: costly once, never wrong. So does a `since` that names no list on purpose: the apps send "0" before they hold
// a cursor (an empty query value is not sent).

const (
	inboxSnapshotsMax = 16
	inboxSnapshotTTL  = time.Hour
)

// inboxSnapshot is a list a cursor names: each item's sum by id, and a sum of the whole (ids and sums in order).
type inboxSnapshot struct {
	n     uint64
	all   bool
	whole uint64
	items map[string]uint64
	used  time.Time
}

// inboxSnapshots are the lists kept for `since`, newest last.
type inboxSnapshots struct {
	mu    sync.Mutex
	once  sync.Once
	seed  maphash.Seed
	epoch string // this run's: a cursor of another run names nothing here
	next  uint64
	kept  []*inboxSnapshot
}

func (k *inboxSnapshots) start() {
	k.once.Do(func() {
		k.seed = maphash.MakeSeed()
		var b [6]byte
		_, _ = rand.Read(b[:])
		k.epoch = hex.EncodeToString(b[:])
	})
}

func (k *inboxSnapshots) cursor(sn *inboxSnapshot) string {
	return k.epoch + "." + strconv.FormatUint(sn.n, 10)
}

// find is the kept list cursor names for these sources, nil when none is (another run, dropped, unused too long).
func (k *inboxSnapshots) find(cursor string, all bool, now time.Time) *inboxSnapshot {
	k.start()
	epoch, num, ok := strings.Cut(cursor, ".")
	if !ok || epoch != k.epoch {
		return nil
	}
	n, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.expire(now)
	for _, sn := range k.kept {
		if sn.n == n && sn.all == all {
			return sn
		}
	}
	return nil
}

// note keeps the list answered now (ids and their bytes, in order) and returns its snapshot: one kept already with the
// same items, used again, else a new one, the oldest going past inboxSnapshotsMax.
func (k *inboxSnapshots) note(all bool, ids []string, raw [][]byte, now time.Time) *inboxSnapshot {
	k.start()
	items := make(map[string]uint64, len(ids))
	var whole maphash.Hash
	whole.SetSeed(k.seed)
	var num [8]byte
	for i, id := range ids {
		sum := maphash.Bytes(k.seed, raw[i])
		items[id] = sum
		_, _ = whole.WriteString(id)
		_ = whole.WriteByte(0)
		_, _ = whole.Write(strconv.AppendUint(num[:0], sum, 16))
		_ = whole.WriteByte(0)
	}
	sum := whole.Sum64()
	k.mu.Lock()
	defer k.mu.Unlock()
	k.expire(now)
	for _, sn := range k.kept {
		if sn.all == all && sn.whole == sum && len(sn.items) == len(items) {
			sn.used = now
			return sn
		}
	}
	k.next++
	sn := &inboxSnapshot{n: k.next, all: all, whole: sum, items: items, used: now}
	k.kept = append(k.kept, sn)
	if len(k.kept) > inboxSnapshotsMax {
		k.kept = k.kept[len(k.kept)-inboxSnapshotsMax:]
	}
	return sn
}

// expire drops the lists unused for inboxSnapshotTTL, under mu.
func (k *inboxSnapshots) expire(now time.Time) {
	keep := k.kept[:0]
	for _, sn := range k.kept {
		if now.Sub(sn.used) < inboxSnapshotTTL {
			keep = append(keep, sn)
		}
	}
	clear(k.kept[len(keep):])
	k.kept = keep
}

// writeInboxSince answers GET /api/v1/inbox?since=: the delta from the list since names, or the whole list, either with
// the cursor of the list now. Without the memo's bytes it answers the whole list as before, with no cursor.
func (s *Service) writeInboxSince(w http.ResponseWriter, all bool, since string) {
	raws, ids, asOf, ok := s.inboxListRawIDs(all)
	if !inboxListMemoOn || !ok {
		s.writeInboxList(w, all)
		return
	}
	k := &s.inbox.snapshots
	old := k.find(since, all, asOf)
	now := k.note(all, ids, raws, asOf)
	cursor := k.cursor(now)
	if old == nil {
		writeInboxAnswer(w, raws, nil, cursor, false, asOf, s.inboxTriage())
		return
	}
	changed, removed := inboxDelta(old, now, ids, raws)
	writeInboxAnswer(w, changed, removed, cursor, true, asOf, s.inboxTriage())
}

// inboxDelta is the list now (ids and their bytes in order, with its snapshot) against the one then: the bytes of the
// items new or changed, in the list's order, and the ids gone, sorted.
func inboxDelta(then, now *inboxSnapshot, ids []string, raws [][]byte) (changed [][]byte, removed []string) {
	for i, id := range ids {
		if sum, ok := then.items[id]; !ok || sum != now.items[id] {
			changed = append(changed, raws[i])
		}
	}
	for id := range then.items {
		if _, ok := now.items[id]; !ok {
			removed = append(removed, id)
		}
	}
	slices.Sort(removed)
	return changed, removed
}

// writeInboxAnswer writes a list answered for `since`, its keys sorted as encoding/json writes a map's: as_of, cursor,
// delta (a delta only), items, removed (a delta only: {"id"} objects, so the hub stamps their manager as it stamps the
// items'), triage.
func writeInboxAnswer(w http.ResponseWriter, items [][]byte, removed []string, cursor string, delta bool, asOf time.Time, triage InboxTriage) {
	at, err1 := json.Marshal(asOf)
	tr, err2 := json.Marshal(triage)
	cur, _ := json.Marshal(cursor)
	if err1 != nil || err2 != nil {
		http.Error(w, "inbox: cannot encode the list", http.StatusInternalServerError)
		return
	}
	var b bytes.Buffer
	b.WriteString(`{"as_of":`)
	b.Write(at)
	b.WriteString(`,"cursor":`)
	b.Write(cur)
	if delta {
		b.WriteString(`,"delta":true`)
	}
	b.WriteString(`,"items":[`)
	for i, raw := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(raw)
	}
	b.WriteByte(']')
	if delta {
		b.WriteString(`,"removed":[`)
		for i, id := range removed {
			if i > 0 {
				b.WriteByte(',')
			}
			q, _ := json.Marshal(id)
			b.WriteString(`{"id":`)
			b.Write(q)
			b.WriteByte('}')
		}
		b.WriteByte(']')
	}
	b.WriteString(`,"triage":`)
	b.Write(tr)
	b.WriteString("}\n")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(b.Bytes())
}
