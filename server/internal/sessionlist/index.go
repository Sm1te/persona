// Package sessionlist provides a bounded, in-memory transport index for session
// adapters. It does not know about runtimes, extensions, or persistent storage.
package sessionlist

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sync"
	"time"
)

const (
	defaultLimit        = 50
	maxLimit            = 200
	maxCache            = 64
	maxSnapshots        = 32
	snapshotTTL         = 5 * time.Minute
	maxJournalRevisions = 128
	maxJournalChanges   = 16384
)

// Item is an adapter's immutable session projection. ID must be a globally
// unique, opaque composite identifier, including any adapter/machine namespace.
// Source is a filterable attribute, independent of Replace's batch owner key.
// PaneSource is an adapter-supplied origin, independent of Source; this index
// never derives it from transport metadata. Full and Summary should be
// JSON-compatible values. Replace detaches exported
// maps, slices, and pointers; hidden state in custom payload types must be
// immutable. Callers must not mutate payloads concurrently with Replace.
type Item struct {
	ID                                                           string
	Machine, Project, Group, State, Source, Provider, PaneSource string
	// BackgroundWaiting: a working row that only waits on background commands (#6045). It stays in State's count
	// (older apps call it working) and is counted apart in Counts.BackgroundWaiting too.
	BackgroundWaiting bool
	// Fold is the item's place in the session hierarchy (#5120); nil for an item folded under none that shows its own
	// State and BackgroundWaiting, as every item without children does. It sits behind a pointer to keep Item in its
	// allocation size class: an adapter builds a whole batch as one []Item, as the hub does on every pass.
	Fold       *Fold
	UpdatedAt  time.Time
	SearchText string
	Summary    any
	Full       any
}

// Fold is how an Item folds. Root is the session id (as parent_session_id names it, not the composite ID) of the item
// on the same Machine this one is folded under (a child session under its topmost listed ancestor), only when that
// item is in the same batch; State is the state the item shows with the items folded under it ("" for its own);
// BackgroundWaiting is Item.BackgroundWaiting for Options.Roots: none of the items folded under it works or needs the
// user either. Options.Roots leaves out the items with a Root and filters and counts the others by their folded state;
// Options.Under selects the items folded under one. Like the rest of an Item it is immutable once handed to Replace:
// copies of the item share it.
type Fold struct {
	Root, State       string
	BackgroundWaiting bool
}

// root is the session v is folded under ("" for none).
func (v *Item) root() string {
	if v.Fold == nil {
		return ""
	}
	return v.Fold.Root
}

// Options selects a filtered full list or an incremental change list. Limit zero
// means the default. Cursor binds the filters, Summary, and Since, but not Limit.
// ID is an adapter-supplied exact composite identity, not a public query key;
// it selects at most one row and cannot be combined with Cursor or Since.
type Options struct {
	Limit                                                                              int
	ID, Cursor, Since, State, Machine, Project, Group, Source, Provider, Q, PaneSource string
	// Manager selects the rows of the batches replaced with that owner (ReplaceOwned); like the other filters, a
	// cursor is bound to it. It is the adapter's to set (not a key Parse reads): the adapter knows which owners exist
	// and who may ask for which.
	Manager string
	// Roots (roots=1) leaves the items folded under another (Fold.Root) out of the rows, the totals and every count,
	// and filters and counts the rest by their folded state (Fold.State). Under (parent_session_id, which needs Machine) selects the
	// items of Machine folded under that session, all depths; the two cannot be combined. Both bind a cursor, as the
	// other filters do.
	Roots   bool
	Under   string
	Summary bool
}

// Page contains transport values rather than adapter-specific session types.
// Total is the filtered membership at Revision, not the number of delta events.
// Sessions and Remove together consume Limit slots in an incremental page.
type Page struct {
	Sessions   []any    `json:"sessions"`
	Remove     []string `json:"remove"`
	HasMore    bool     `json:"has_more"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Revision   string   `json:"revision"`
	Total      int      `json:"total"`
}

// Counts describes the complete filtered membership, ignoring page size.
// Empty attribute values are counted under the empty string key. PaneSource
// is omitted for adapters that do not supply an origin attribute.
type Counts struct {
	Total      int            `json:"total"`
	Machine    map[string]int `json:"machine"`
	Project    map[string]int `json:"project"`
	Group      map[string]int `json:"group"`
	State      map[string]int `json:"state"`
	PaneSource map[string]int `json:"pane_source,omitempty"`
	// BackgroundWaiting: how many of State["working"] only wait in the background (#6045): a client that shows them
	// apart takes them out of its Working count.
	BackgroundWaiting int    `json:"background_waiting,omitempty"`
	Revision          string `json:"revision"`
}

// Error lets adapters map invalid_query to HTTP 400 and cursor_expired or
// revision_expired to HTTP 409 without interpreting human-readable messages.
type Error struct {
	Code    string
	message string
}

func (e *Error) Error() string { return e.Code + ": " + e.message }

func failure(code, message string) error { return &Error{Code: code, message: message} }

type record struct {
	item   Item
	search string
	// owner is the manager its batch was replaced with (ReplaceOwned), "" for none: what filters.Manager selects by.
	// It is the batch's, not an Item attribute, so the items an adapter builds for every update stay the size they were.
	owner string
}

type change struct{ before, after *record }
type journalEntry struct {
	revision uint64
	changes  map[string]change
}

type filters struct {
	ID, State, Machine, Project, Group, Source, Provider, Q, PaneSource, Manager, Under string
	Roots                                                                               bool
}

type binding struct {
	filters
	Summary bool
	Since   string
}

type cacheKey struct {
	filters
	Since string
}

type event struct {
	id      string
	updated time.Time
	value   *record // nil means a removal
}

type result struct {
	events  []event
	counts  Counts
	ordered bool // counts-only views must not be mistaken for an empty ordered list
}

type cacheEntry struct {
	result *result
	used   uint64
}

type snapshot struct {
	binding binding
	result  *result
	expires time.Time
}

// Index is safe for concurrent use. Its zero value is ready to use; do not copy
// it after first use. Cached views live only for the current revision. Cursors
// retain at most 32 immutable views for five minutes, and the journal retains
// at most 128 revisions and 16,384 changed IDs (whole revisions are evicted).
type Index struct {
	mu             sync.Mutex
	epoch          string
	secret         []byte
	revision       uint64
	floor          uint64
	items          map[string]*record
	counts         Counts // current unfiltered counts, maintained on changed records only
	sources        map[string]map[string]*record
	journal        []journalEntry
	journalChanges int
	cache          map[cacheKey]*cacheEntry
	cacheClock     uint64
	snapshots      map[string]*snapshot
	snapshotOrder  []string
	now            func() time.Time // optional clock used by package tests
}

func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sessionlist: random token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (i *Index) initialize() {
	if i.epoch != "" {
		return
	}
	i.epoch = randomToken()
	i.secret = []byte(randomToken())
	i.items = make(map[string]*record)
	i.sources = make(map[string]map[string]*record)
	i.cache = make(map[cacheKey]*cacheEntry)
	i.snapshots = make(map[string]*snapshot)
	i.counts = emptyCounts(i.revisionToken())
	if i.now == nil {
		i.now = time.Now
	}
}

// Revision returns a lightweight highwater token, including an index-specific
// epoch so clients cannot silently reuse tokens after a restart.
func (i *Index) Revision() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.initialize()
	i.pruneSnapshots()
	return i.revisionToken()
}

// Replace atomically replaces one owner's entire batch. Unchanged batches do
// not advance Revision, even if their input order changes. IDs must be nonempty
// and unique within and across owners; violating this adapter contract panics.
func (i *Index) Replace(source string, items []Item) { i.ReplaceOwned(source, "", items) }

// ReplaceOwned is Replace of a batch that belongs to owner (a manager's id, "" for none): Options.Manager selects
// its rows. A batch whose owner changed is a change of every row of it, even when nothing else of them did: each
// leaves the old owner's filtered list as a removal and enters the new owner's.
func (i *Index) ReplaceOwned(source, owner string, items []Item) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.initialize()
	i.pruneSnapshots()
	old := i.sources[source]
	next := make(map[string]*record, len(items))
	changes := make(map[string]change)
	for _, item := range items {
		if item.ID == "" {
			panic("sessionlist: empty item ID")
		}
		if _, exists := next[item.ID]; exists {
			panic("sessionlist: duplicate item ID in batch")
		}
		if existing := i.items[item.ID]; existing != nil && old[item.ID] == nil {
			panic("sessionlist: item ID belongs to another batch owner")
		}
		// Strip monotonic clock data and normalize zones before comparison.
		item.UpdatedAt = item.UpdatedAt.Round(0).UTC()
		previous := old[item.ID]
		if previous != nil && previous.owner == owner && sameItem(&previous.item, &item) {
			next[item.ID] = previous
			continue
		}
		item.Full = clone(item.Full)
		item.Summary = clone(item.Summary)
		r := &record{item: item, search: normalizeQ(item.SearchText), owner: owner}
		next[item.ID] = r
		changes[item.ID] = change{before: previous, after: r}
	}
	for id, previous := range old {
		if next[id] == nil {
			changes[id] = change{before: previous}
		}
	}
	if len(changes) == 0 {
		return
	}
	for id, c := range changes {
		if c.before != nil {
			addCount(&i.counts, c.before.item, false, -1)
		}
		if c.after != nil {
			addCount(&i.counts, c.after.item, false, 1)
		}
		if c.after == nil {
			delete(i.items, id)
		} else {
			i.items[id] = c.after
		}
	}
	if len(next) == 0 {
		delete(i.sources, source)
	} else {
		i.sources[source] = next
	}
	i.revision++
	i.counts.Revision = i.revisionToken()
	i.journal = append(i.journal, journalEntry{revision: i.revision, changes: changes})
	i.journalChanges += len(changes)
	for len(i.journal) > maxJournalRevisions || i.journalChanges > maxJournalChanges {
		first := i.journal[0]
		i.floor = first.revision
		i.journalChanges -= len(first.changes)
		copy(i.journal, i.journal[1:])
		i.journal[len(i.journal)-1] = journalEntry{}
		i.journal = i.journal[:len(i.journal)-1]
	}
	// Do not retain historical views except bounded cursor snapshots.
	i.cache = make(map[cacheKey]*cacheEntry)
}

// sameItem is reflect.DeepEqual(*a, *b) without walking what is plainly the same: the attributes compared as values,
// a payload that is the very bytes stored (an item Items returned, handed back) by its slice. Anything else, such as
// a payload built again, is left to reflect.DeepEqual.
func sameItem(a, b *Item) bool {
	if !samePayload(a.Full, b.Full) || !samePayload(a.Summary, b.Summary) {
		return false
	}
	x, y := *a, *b
	x.Full, x.Summary, y.Full, y.Summary = nil, nil, nil, nil
	return x == y || reflect.DeepEqual(x, y)
}

func samePayload(a, b any) bool {
	if x, ok := a.(json.RawMessage); ok {
		if y, ok := b.(json.RawMessage); ok && len(x) > 0 && len(x) == len(y) && &x[0] == &y[0] {
			return true
		}
	}
	return reflect.DeepEqual(a, b)
}

// RemoveSource removes an owner's entire batch in one revision.
func (i *Index) RemoveSource(source string) { i.Replace(source, nil) }

// Items returns the current items with the given IDs, in order, and the zero
// Item for an ID the index does not hold, all under one lock. The items are
// copies, but their Full and Summary are the index's own: callers only read
// them, or hand them back to Replace unchanged, which then keeps the record.
func (i *Index) Items(ids []string) []Item {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]Item, len(ids))
	for k, id := range ids {
		if r := i.items[id]; r != nil {
			out[k] = r.item
		}
	}
	return out
}

func (i *Index) pruneSnapshots() {
	now := i.now()
	for len(i.snapshotOrder) > 0 {
		id := i.snapshotOrder[0]
		if s := i.snapshots[id]; s != nil && now.Before(s.expires) {
			break
		}
		i.dropOldestSnapshot()
	}
}

func (i *Index) dropOldestSnapshot() {
	delete(i.snapshots, i.snapshotOrder[0])
	copy(i.snapshotOrder, i.snapshotOrder[1:])
	i.snapshotOrder[len(i.snapshotOrder)-1] = ""
	i.snapshotOrder = i.snapshotOrder[:len(i.snapshotOrder)-1]
}
