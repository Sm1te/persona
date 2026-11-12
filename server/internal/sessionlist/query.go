package sessionlist

import (
	"maps"
	"reflect"
	"sort"
	"strings"
)

// Query returns a full list or, with Since, collapsed changes since a retained
// revision. Continuations are frozen: both their order and revision remain
// unchanged while new batches arrive, even if the original journal is evicted.
func (i *Index) Query(options Options) (Page, error) {
	o, err := validate(options)
	if err != nil {
		return Page{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.initialize()
	i.pruneSnapshots()
	var r *result
	var id string
	var offset int
	if o.Cursor != "" {
		s, snapshotID, position, err := i.cursorSnapshot(o)
		if err != nil {
			return Page{}, err
		}
		r, id, offset = s.result, snapshotID, position
	} else {
		if o.Since != "" {
			if _, err := i.sinceRevision(o.Since); err != nil {
				return Page{}, err
			}
		}
		r = i.queryResult(o)
		if len(r.events) > o.Limit {
			id = randomToken()
			if len(i.snapshots) == maxSnapshots {
				i.dropOldestSnapshot()
			}
			i.snapshots[id] = &snapshot{binding: o.binding(), result: r, expires: i.now().Add(snapshotTTL)}
			i.snapshotOrder = append(i.snapshotOrder, id)
		}
	}
	end := min(offset+o.Limit, len(r.events))
	p := Page{Sessions: make([]any, 0, end-offset), Remove: make([]string, 0), Revision: r.counts.Revision, Total: r.counts.Total, HasMore: end < len(r.events)}
	for _, e := range r.events[offset:end] {
		if e.value == nil {
			p.Remove = append(p.Remove, e.id)
			continue
		}
		value := e.value.item.Full
		if o.Summary {
			value = e.value.item.Summary
		}
		p.Sessions = append(p.Sessions, clone(value))
	}
	if p.HasMore {
		p.NextCursor = i.cursorToken(id, end)
	}
	return p, nil
}

// Counts uses the same filtered view as Query. Without Cursor it reports current
// membership, validating Since if supplied; with Cursor it reports that frozen
// view's membership and revision rather than a newer revision.
func (i *Index) Counts(options Options) (Counts, error) {
	o, err := validate(options)
	if err != nil {
		return Counts{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.initialize()
	i.pruneSnapshots()
	if o.Cursor != "" {
		s, _, _, err := i.cursorSnapshot(o)
		if err != nil {
			return Counts{}, err
		}
		return copyCounts(s.result.counts), nil
	}
	if o.Since != "" {
		if _, err := i.sinceRevision(o.Since); err != nil {
			return Counts{}, err
		}
	}
	return copyCounts(i.countsResult(o.filters()).counts), nil
}

func copyCounts(c Counts) Counts {
	c.Machine = maps.Clone(c.Machine)
	c.Project = maps.Clone(c.Project)
	c.Group = maps.Clone(c.Group)
	c.State = maps.Clone(c.State)
	c.PaneSource = maps.Clone(c.PaneSource)
	return c
}

func (f filters) matches(r *record) bool {
	if r == nil {
		return false
	}
	v := r.item
	return (f.ID == "" || f.ID == v.ID) &&
		(!f.Roots || v.root() == "") && (f.Under == "" || f.Under == v.root()) &&
		(f.State == "" || f.State == stateOf(v, f.Roots)) &&
		(f.Machine == "" || f.Machine == v.Machine) &&
		(f.Project == "" || f.Project == v.Project) &&
		(f.Group == "" || f.Group == v.Group) &&
		(f.Source == "" || f.Source == v.Source) &&
		(f.PaneSource == "" || f.PaneSource == v.PaneSource) &&
		(f.Manager == "" || f.Manager == r.owner) &&
		(f.Provider == "" || f.Provider == v.Provider) &&
		(f.Q == "" || strings.Contains(r.search, f.Q))
}

func sortEvents(events []event) {
	sort.Slice(events, func(a, b int) bool {
		if events[a].updated.Equal(events[b].updated) {
			return events[a].id < events[b].id
		}
		return events[a].updated.After(events[b].updated)
	})
}

func (i *Index) cached(key cacheKey) *result {
	entry := i.cache[key]
	if entry == nil {
		return nil
	}
	i.cacheClock++
	entry.used = i.cacheClock
	return entry.result
}

func (i *Index) remember(key cacheKey, r *result) *result {
	if i.cache[key] == nil && len(i.cache) == maxCache {
		var oldestKey cacheKey
		oldest := ^uint64(0)
		for k, entry := range i.cache {
			if entry.used < oldest {
				oldestKey, oldest = k, entry.used
			}
		}
		delete(i.cache, oldestKey)
	}
	i.cacheClock++
	i.cache[key] = &cacheEntry{result: r, used: i.cacheClock}
	return r
}

func emptyCounts(revision string) Counts {
	return Counts{Machine: make(map[string]int), Project: make(map[string]int), Group: make(map[string]int), State: make(map[string]int), PaneSource: make(map[string]int), Revision: revision}
}

// stateOf is the state v is filtered and counted by: its folded state for Options.Roots (Fold.State, its own state
// when the adapter gives none).
func stateOf(v Item, roots bool) string {
	if roots && v.Fold != nil && v.Fold.State != "" {
		return v.Fold.State
	}
	return v.State
}

func addCount(c *Counts, v Item, roots bool, delta int) {
	c.Total += delta
	if v.PaneSource != "" {
		c.PaneSource[v.PaneSource] += delta
		if c.PaneSource[v.PaneSource] == 0 {
			delete(c.PaneSource, v.PaneSource)
		}
	}
	if v.BackgroundWaiting && (!roots || v.Fold == nil || v.Fold.BackgroundWaiting) {
		c.BackgroundWaiting += delta
	}
	for _, pair := range []struct {
		m   map[string]int
		key string
	}{
		{c.Machine, v.Machine}, {c.Project, v.Project}, {c.Group, v.Group}, {c.State, stateOf(v, roots)},
	} {
		pair.m[pair.key] += delta
		if pair.m[pair.key] == 0 {
			delete(pair.m, pair.key)
		}
	}
}

// exactResult deliberately bypasses ordered views and their caches. A miss,
// changed revision or new filter still touches only the selected map entry.
func (i *Index) exactResult(f filters) *result {
	r := &result{counts: emptyCounts(i.revisionToken()), ordered: true}
	if item := i.items[f.ID]; f.matches(item) {
		addCount(&r.counts, item.item, f.Roots, 1)
		r.events = []event{{id: f.ID, updated: item.item.UpdatedAt, value: item}}
	}
	return r
}

// countsResult never builds or sorts an ordered list. Unfiltered counts are
// maintained on updates, so a one-row delta does not scan 8,000 unrelated rows
// merely to report Total. Filtered counts are scanned only on a cache miss.
func (i *Index) countsResult(f filters) *result {
	if f.ID != "" {
		return i.exactResult(f)
	}
	key := cacheKey{filters: f}
	if r := i.cached(key); r != nil {
		return r
	}
	var counts Counts
	if f == (filters{}) {
		counts = copyCounts(i.counts)
	} else {
		counts = emptyCounts(i.revisionToken())
		for _, item := range i.items {
			if f.matches(item) {
				addCount(&counts, item.item, f.Roots, 1)
			}
		}
	}
	return i.remember(key, &result{counts: counts})
}

func (i *Index) fullResult(f filters) *result {
	if f.ID != "" {
		return i.exactResult(f)
	}
	key := cacheKey{filters: f}
	if r := i.cached(key); r != nil && r.ordered {
		return r
	}
	r := &result{counts: i.countsResult(f).counts, ordered: true}
	for id, item := range i.items {
		if !f.matches(item) {
			continue
		}
		r.events = append(r.events, event{id: id, updated: item.item.UpdatedAt, value: item})
	}
	sortEvents(r.events)
	return i.remember(key, r)
}

func (i *Index) queryResult(o Options) *result {
	if o.Since == "" {
		return i.fullResult(o.filters())
	}
	key := cacheKey{filters: o.filters(), Since: o.Since}
	if r := i.cached(key); r != nil {
		return r
	}
	// Query already validated this baseline. Cursor continuations never touch
	// the journal, so retaining a frozen delta does not retain journal history.
	_, since, _ := parseRevision(o.Since)
	collapsed := make(map[string]change)
	for _, entry := range i.journal {
		if entry.revision <= since {
			continue
		}
		for id, c := range entry.changes {
			if previous, exists := collapsed[id]; exists {
				c.before = previous.before
			}
			collapsed[id] = c
		}
	}
	r := &result{counts: i.countsResult(o.filters()).counts}
	f := o.filters()
	for id, c := range collapsed {
		before, after := f.matches(c.before), f.matches(c.after)
		if after {
			if before && reflect.DeepEqual(c.before.item, c.after.item) {
				continue
			}
			r.events = append(r.events, event{id: id, updated: c.after.item.UpdatedAt, value: c.after})
		} else if before {
			updated := c.before.item.UpdatedAt
			if c.after != nil {
				updated = c.after.item.UpdatedAt
			}
			r.events = append(r.events, event{id: id, updated: updated})
		}
	}
	sortEvents(r.events)
	return i.remember(key, r)
}
