package assistant

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// inboxBook is the inbox's state.
type inboxBook struct {
	lang        func() i18n.Lang // the user's language, for the reasons it writes on items (nil: the default)
	larkAccount int              // lark.account (InboxConfig.LarkAccount); zero is primary account 1
	mu          sync.Mutex
	items       map[string]*InboxItem
	once        sync.Once
	loadMu      sync.Mutex              // one load at a time
	loaded      atomic.Bool             // what the database keeps was read: until then nothing is taken or written
	running     sync.Mutex              // one collection applied at a time
	saving      sync.Mutex              // snapshots and writes in the same order
	needsYouMu  sync.Mutex              // guards needsYou
	needsYou    inboxTeller             // the "Needs you" count's teller (tellInboxNeedsYou)
	mark        map[string]uint64       // bumped on each user parent/done mutation of an id, under mu: a rollback's identity check
	filing      map[string]*inboxFiling // pending filing ownership, independent of an unrelated parent change
	epoch       string                  // the hub's run and its last round applied (InboxRound.Epoch, Seq), under running
	seq         uint64
	triaging    sync.Mutex     // one triage pass at a time (inbox_triage.go)
	handing     sync.Mutex     // one judge / agent pass at a time (inbox_agent.go)
	handAgain   atomic.Bool    // a pass is asked for: the goroutine holding handing runs it (handInbox)
	passHook    func()         // tests: runs as each pass starts
	translating sync.Mutex     // one translation pass at a time (inbox_translate.go)
	replies     sync.Map       // item id -> *inboxReply: replies to one item, one at a time (inbox_triage.go)
	handingOff  sync.Map       // item id -> *sync.Mutex: hand-offs of one item, one at a time (inbox_handoff.go)
	telling     sync.Map       // item id -> *sync.Mutex: deliveries to one item's conversation, one at a time (deliverNotes)
	snapshots   inboxSnapshots // the lists answered with a cursor, for GET /api/v1/inbox?since (inbox_delta.go)
	mutes       []InboxMute
	mutesMu     sync.Mutex // one read-change-save of the kept mutes at a time
	policies    []InboxPolicy
	policiesMu  sync.Mutex // one read-change-save of the kept policies at a time (inbox_policies.go)
	folderCfg   InboxFolderConfig
	folderCfgMu sync.Mutex // one read-change-save of the folder settings at a time (inbox_folders.go)
	foldering   sync.Mutex // one folder pass at a time
	cardSends   cardLocks  // a message card's id → the lock its "Send" holds (sendFromCard)
	// translations are the translations made, by model, text or mail and language (inbox_translate.go).
	translations translateCache

	// What the rounds and the hub's settings said of the sources (under srcMu, which is taken after mu when both
	// are): the plugins' connectors (their new items are triaged before they enter; InboxConfig.Extensions names them
	// from the start), the built-in ones a round read, how they describe their kinds, every connector's name.
	srcMu      sync.RWMutex
	extensions map[string]bool
	builtin    map[string]bool
	kinds      map[string]string // "<connector>/<kind>" → words
	names      map[string]string

	// admitting is one triage of a round's new plugin items at a time (admitInbox): a round meanwhile does not start
	// them again.
	admitting sync.Mutex
	batch     *inboxBatch          // while a collection runs: the items it starts, which enter once triaged
	held      map[string]inboxHeld // by source: the new items its last collection held back (inbox_admit.go)
	// readErrs: the sources the last round could not read, by id, as a short code (assistantwire.ReadFailed…): the
	// apps say which one is behind (InboxTriage.Sources). mailReadErrs: the same of the mail accounts the last mail
	// round could not read, by account id. Memory only.
	readErrs     map[string]string
	mailReadErrs map[string]assistantwire.InboxMailFailed
	grouping     inboxGrouping // the batches being related to events (inbox_group.go)

	cfgMu sync.Mutex
	cfg   assistantwire.InboxConfig // the hub's (InboxConfigMethod), as last read
	cfgAt time.Time

	purgedAt time.Time // the last purge of the done items past inboxKeepHistory (purgeInboxHistory), under mu

	listMemo inboxListMemo // each listed item's JSON for GET /api/v1/inbox, under mu (inbox_list_memo.go)
}

func (b *inboxBook) init() {
	b.once.Do(func() {
		b.items = map[string]*InboxItem{}
		b.extensions, b.builtin, b.kinds, b.names = map[string]bool{}, map[string]bool{}, map[string]string{}, map[string]string{}
	})
}

// fromExtension says whether a connector (an item's source) is a plugin's, as the rounds said: its new items are
// triaged before they enter (inbox_admit.go).
func (b *inboxBook) fromExtension(source string) bool {
	b.srcMu.RLock()
	defer b.srcMu.RUnlock()
	return b.extensions[source]
}

// builtinSource says whether a connector is one of the hub's own, as a round said. A connector's item from a source
// neither a round nor the settings named (the manager just started) has no fallback tier: never noise by default.
func (b *inboxBook) builtinSource(source string) bool {
	b.srcMu.RLock()
	defer b.srcMu.RUnlock()
	return b.builtin[source] && !b.extensions[source]
}

// knowExtensions takes the plugins' connectors the hub's settings name (InboxConfig.Extensions).
func (b *inboxBook) knowExtensions(ids []string) {
	b.srcMu.Lock()
	defer b.srcMu.Unlock()
	for _, id := range ids {
		b.extensions[id] = true
	}
}

// kindWords is how a plugin's connector describes one of its kinds of things ("" when it does not).
func (b *inboxBook) kindWords(source, kind string) string {
	b.srcMu.RLock()
	defer b.srcMu.RUnlock()
	return b.kinds[source+"/"+kind]
}

// userLang is the user's language for what b writes for people.
func (b *inboxBook) userLang() i18n.Lang {
	if b.lang == nil {
		return i18n.Default
	}
	return b.lang()
}

// byRules is an item's reason when its rules, not the model, filed it.
func (b *inboxBook) byRules() string { return i18n.T(b.userLang(), "assistant-inbox.by_rules") }

// mutedReason is an item's reason when mute name closed it.
func (b *inboxBook) mutedReason(name string) string {
	return i18n.T(b.userLang(), "assistant-inbox.muted", i18n.Args{"name": name})
}

// sourceName is an inbox source for people: Lark (Feishu), mail, a connector's name as the hub last said it.
func (b *inboxBook) sourceName(source string) string {
	switch source {
	case "lark":
		return i18n.T(b.userLang(), "assistant-inbox.source_lark")
	case "mail":
		return i18n.T(b.userLang(), "assistant-inbox.source_mail")
	}
	b.srcMu.RLock()
	defer b.srcMu.RUnlock()
	if n := b.names[source]; n != "" {
		return n
	}
	return source
}

// drop forgets an item, under b.mu, with its reply and delivery locks: they would otherwise stay for the life of the
// hub. A delivery holding or waiting for the lock finds the item gone and stops (deliverNotes).
func (b *inboxBook) drop(id string) {
	delete(b.items, id)
	delete(b.listMemo.kept, id)
	delete(b.filing, id)
	b.replies.Delete(id)
	b.telling.Delete(id)
	b.handingOff.Delete(id)
}

// touched stamps a new per-id parent/done mutation generation under b.mu: a rollback checks its stamp to tell its
// own write apart from a concurrent same-valued one (two reopens, two unlinks), which value equality cannot. The item
// changed, so GET /api/v1/inbox encodes it again (inbox_list_memo.go).
func (b *inboxBook) touched(id string) uint64 {
	delete(b.listMemo.kept, id)
	if b.mark == nil {
		b.mark = map[string]uint64{}
	}
	b.mark[id]++
	return b.mark[id]
}

// ---- helpers ----

// threadIndex is the items (and the running round's) by thread, for a collection that looks up one thread per
// thing: openFor on each scanned the whole inbox, a week of chats for each of Feishu's few hundred unread ones.
// key is the item's thread among the ones looked up, false for an item of another source; in, when set, says whether
// key(it) is thread, ok, without making the key (openFor asks it of each item filed under a thread). Under b.mu.
type threadIndex struct {
	key func(*InboxItem) (string, bool)
	in  func(it *InboxItem, thread string) bool
	by  map[string][]*InboxItem
}

func (b *inboxBook) threads(key func(*InboxItem) (string, bool)) *threadIndex {
	x := &threadIndex{key: key, by: map[string][]*InboxItem{}}
	b.each(x.file)
	return x
}

// file files an item threads meets: each comes once, so it is not looked for first, as note does.
func (x *threadIndex) file(it *InboxItem) {
	if k, ok := x.key(it); ok {
		x.by[k] = append(x.by[k], it)
	}
}

// note files it under its thread: an item added, or one whose thread may have changed. An entry under a thread it
// has left is skipped by the lookups.
func (x *threadIndex) note(it *InboxItem) {
	if k, ok := x.key(it); ok && !slices.Contains(x.by[k], it) {
		x.by[k] = append(x.by[k], it)
	}
}

// openFor finds the open item of a thread (one this round started too); done is the newest done one. A thread can
// have two open items (an older one moved back after a newer one started): the newest gathers what comes next, the
// same one every collection, so the older one does not become a copy of it (#3796).
func (x *threadIndex) openFor(thread string) (open, done *InboxItem) {
	for _, it := range x.by[thread] {
		if !x.under(it, thread) {
			continue
		}
		if it.open() {
			if open == nil || newerItem(it, open) {
				open = it
			}
		} else if done == nil || it.UpdatedAt.After(done.UpdatedAt) {
			done = it
		}
	}
	return open, done
}

// under says whether it is still filed under thread: its thread may have changed since it was.
func (x *threadIndex) under(it *InboxItem, thread string) bool {
	if x.in != nil {
		return x.in(it, thread)
	}
	k, ok := x.key(it)
	return ok && k == thread
}

// newerItem: a holds a later move than b (by its time, then its id, so a tie picks the same one every time).
func newerItem(a, b *InboxItem) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

// each calls f for every item, and every item the running round started, under b.mu.
func (b *inboxBook) each(f func(*InboxItem)) {
	for _, it := range b.items {
		f(it)
	}
	if b.batch != nil {
		for _, it := range b.batch.items {
			f(it)
		}
	}
}

// has says whether an item (or one the running round started) has id, under b.mu.
func (b *inboxBook) has(id string) bool {
	_, ok := b.items[id]
	return ok || b.batch != nil && b.batch.items[id] != nil
}

// add keeps an item a collection starts (from: the source it came from), under b.mu. A plugin connector's, while a
// round runs, goes to the round's batch and enters the list once triaged (admitInbox); any other straight in.
func (b *inboxBook) add(from string, it *InboxItem) {
	if b.batch == nil || !b.fromExtension(from) {
		b.items[it.ID] = it
		return
	}
	b.batch.items[it.ID] = it
	b.batch.from[it.ID] = from
}

// readSource notes that the running round read a source: its held-back count is recomputed after the round.
func (b *inboxBook) readSource(id, name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.batch != nil {
		b.batch.read[id] = name
	}
}
