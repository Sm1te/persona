package subscriptions

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kbwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Delivery to the Inbox and the knowledge base (#2039, docs/extensions/subscriptions.md "Destinations"). Every
// subscription is an internal source of the manager. The Inbox reads its things every collection round
// (InboxSources), made from the held items the way a connector answers Collect from its memory; the knowledge base
// pages through the held items from a cursor it keeps itself (Changes). Nothing here records what a destination
// took: the Inbox knows that by thread and last, the knowledge base by its cursor and message-id receipts, so a
// replay or a restarted manager hands them the same things again and they add nothing.

const (
	catchupBack     = 24 * time.Hour     // the Inbox catches up on a day before it was turned on; the knowledge base starts there too
	digestOver      = 3                  // more catch-up items than this come in as one digest
	digestLinesMost = 50                 // lines of a digest; the rest are counted
	catchupReadMost = 500                // catch-up items read for a digest
	inboxReadMost   = 300                // the newest items a round reads of one subscription
	inboxWindow     = 7 * 24 * time.Hour // the Inbox's: an older item starts nothing
	kbPageMost      = 100                // items a knowledge-base call takes
	previewMost     = 200
	contextBodyMost = 3000
	threadLinesMost = 10 // a thread's earlier items listed above its newest
	failingNotice   = 24 * time.Hour
)

// KindFailing is the kind of a subscription's failure notice in the Inbox, beside the recipes' kinds.
const KindFailing = "sub_failing"

// itemKinds are the kinds a subscription's Inbox items can have, each described for the models.
var itemKinds = append(slices.Clone(wire.ConnectorSubscriptionKinds), KindFailing)

// InboxSource is one subscription as a source of an Inbox collection round.
type InboxSource struct {
	ID     string            // the subscription's id: its items' source
	Name   string            // its label
	Kinds  map[string]string // what its kinds of items are, for the models
	Things []connector.InboxThing
}

// InboxSources are the subscriptions as Inbox sources: one's items while its Inbox is on, a notice once it has failed
// for a day, and the notice's end once it pulls again.
func (s *Service) InboxSources(ctx context.Context) ([]InboxSource, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := []InboxSource{}
	for _, sub := range all {
		src := InboxSource{ID: sub.ID, Name: sub.Label, Kinds: kindsOf(sub.Label), Things: []connector.InboxThing{}}
		catchup, live, err := s.inboxHeld(ctx, sub, now)
		if err != nil {
			return nil, err
		}
		d := s.deliverer(sub, now)
		src.Things = append(src.Things, d.inboxThings(catchup, live)...)
		src.Things = append(src.Things, d.failing())
		out = append(out, src)
	}
	return out, nil
}

func kindsOf(label string) map[string]string {
	out := make(map[string]string, len(itemKinds))
	for _, k := range itemKinds {
		out[k] = prompts.RenderSubscriptionKind(prompts.SubscriptionKind{Kind: k, Label: label})
	}
	return out
}

// inboxFrom is when sub's Inbox was turned on; nil while it is off. One turned on before the mark was kept counts
// from when it was made.
func inboxFrom(sub Subscription) *time.Time {
	switch {
	case !sub.ToInbox:
		return nil
	case sub.InboxFrom != nil:
		return sub.InboxFrom
	}
	return &sub.CreatedAt
}

// kbFrom is when sub's knowledge base was turned on; nil while it is off.
func kbFrom(sub Subscription) *time.Time {
	switch {
	case !sub.ToKB:
		return nil
	case sub.KBFrom != nil:
		return sub.KBFrom
	}
	return &sub.CreatedAt
}

// inboxHeld are the held items the Inbox gets of sub, once the first run since it was turned on has read the
// source: the catch-up (stored from a day before it was turned on up to that run) and the ones stored later, the
// newest of the Inbox's window.
func (s *Service) inboxHeld(ctx context.Context, sub Subscription, now time.Time) (catchup, live []Held, err error) {
	from, mark := inboxFrom(sub), sub.InboxCatchup
	if from == nil || mark == nil || mark.Before(*from) {
		return nil, nil, nil
	}
	start := from.Add(-catchupBack)
	if c := sub.CatchupFrom; c != nil && c.After(start) {
		start = *c // a recipe changed with existing digest: the catch-up is what the new recipe found
	}
	// The catch-up is read while it is in the Inbox's window, which ends before its first items can be pruned (a
	// digest of fewer items would read as a new move).
	if now.Sub(start) < inboxWindow {
		held, err := s.o.Store.HeldItems(ctx, sub.ID, ItemQuery{Since: start, Limit: catchupReadMost})
		if err != nil {
			return nil, nil, err
		}
		for _, h := range held {
			if !h.Stored.After(*mark) {
				catchup = append(catchup, h)
			}
		}
	}
	since := now.Add(-inboxKeep)
	if since.Before(start) {
		since = start
	}
	held, err := s.o.Store.HeldItems(ctx, sub.ID, ItemQuery{Since: since, Limit: inboxReadMost, Newest: true})
	if err != nil {
		return nil, nil, err
	}
	for _, h := range held {
		if h.Stored.After(*mark) {
			live = append(live, h)
		}
	}
	return catchup, live, nil
}

// deliverer makes one subscription's things and batches for the destinations.
type deliverer struct {
	sub   Subscription
	now   time.Time
	lang  i18n.Lang
	loc   *time.Location
	rules string
}

func (s *Service) deliverer(sub Subscription, now time.Time) deliverer {
	rules := strings.TrimSpace(sub.TriageRules)
	if rules == "" && sub.Extension != "" {
		rules = strings.TrimSpace(s.o.ExtensionRules(sub.Extension))
	}
	return deliverer{sub: sub, now: now, lang: s.o.Lang(), loc: s.o.Location(), rules: clip(rules, 4000)}
}

// heldAt is when an item happened as the destinations see it: its time, never later than when it was stored.
func heldAt(h Held) time.Time {
	if h.Time != nil && h.Time.Before(h.Stored) {
		return *h.Time
	}
	return h.Stored
}

// inboxThings are the Inbox's things of the catch-up and later items: a snapshot's newest state; the catch-up's
// items as rows, or past digestOver as one digest with the ones still due kept as rows; then a row a thread.
func (d deliverer) inboxThings(catchup, live []Held) []connector.InboxThing {
	var snaps, rows []Held
	var digest []Held
	for _, h := range catchup {
		switch {
		case h.Snapshot:
			snaps = append(snaps, h)
		case h.DueDate != nil && h.DueDate.After(*d.sub.InboxCatchup):
			rows = append(rows, h) // still due when it came in: never buried in the digest
		default:
			digest = append(digest, h)
		}
	}
	if len(digest) <= digestOver {
		rows, digest = append(rows, digest...), nil
	}
	for _, h := range live {
		if h.Snapshot {
			snaps = append(snaps, h)
		} else {
			rows = append(rows, h)
		}
	}
	var out []connector.InboxThing
	if len(snaps) > 0 {
		out = append(out, d.snapshotThing(slices.MaxFunc(snaps, func(a, b Held) int { return int(a.Seq - b.Seq) })))
	}
	if len(digest) > 0 {
		out = append(out, d.digestThing(digest))
	}
	threads := map[string][]Held{}
	var order []string
	for _, h := range rows {
		key := cmpOr(h.Thread, h.UID)
		if _, ok := threads[key]; !ok {
			order = append(order, key)
		}
		threads[key] = append(threads[key], h)
	}
	for _, key := range order {
		out = append(out, d.threadThing(key, threads[key]))
	}
	return out
}

func (d deliverer) thread(key string) string { return "sub:" + d.sub.ID + ":" + key }

func (d deliverer) tags() []connector.Tag {
	return []connector.Tag{{Key: "source", Value: d.sub.ID, Name: d.sub.Label}}
}

// fallback is the tier an item gets when no model can triage it (the subscription's fallback_tier): by default FYI,
// and Needs you for one with a due date; "wait" none, so it waits for the model.
func (d deliverer) fallback(due bool) *connector.ThingExtra {
	tier := connector.TierFYI
	switch d.sub.FallbackTier {
	case "wait":
		return nil
	case "action":
		tier = connector.TierAction
	case "fyi":
	default:
		if due {
			tier = connector.TierAction
		}
	}
	return &connector.ThingExtra{RuleTier: string(tier)}
}

// threadThing is one row for a thread's items (or one item's): its newest is the row, the earlier ones are listed
// above it in the context.
func (d deliverer) threadThing(key string, items []Held) connector.InboxThing {
	slices.SortStableFunc(items, func(a, b Held) int {
		if c := heldAt(a).Compare(heldAt(b)); c != 0 {
			return c
		}
		return int(a.Seq - b.Seq)
	})
	last := items[len(items)-1]
	due := false
	var b strings.Builder
	earlier := items[:len(items)-1]
	if len(earlier) > threadLinesMost {
		earlier = earlier[len(earlier)-threadLinesMost:]
	}
	for _, h := range items {
		due = due || h.DueDate != nil || h.Kind == wire.ConnectorSubscriptionKindSubDeadline
	}
	for _, h := range earlier {
		b.WriteString(d.line(h))
		b.WriteString("\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(d.block(last.Item, heldAt(last)))
	t := connector.InboxThing{Thread: d.thread(key), Kind: cmpOr(last.Kind, d.sub.Recipe.Kind), Label: d.sub.Label,
		Title: last.Title, Sender: last.Sender, Preview: clip(oneLine(last.Body), previewMost), Context: b.String(),
		At: heldAt(last), Last: last.UID, Rules: d.rules, Extra: d.fallback(due), Tags: d.tags()}
	if strings.HasPrefix(last.Link, "https://") {
		t.URL = last.Link
	}
	return t
}

// block is an item for the Inbox's context: title, who and when, its due date, its body (cut), its link.
func (d deliverer) block(it Item, when time.Time) string {
	var lines []string
	if it.Title != "" {
		lines = append(lines, it.Title)
	}
	who := d.when(when)
	if it.Sender != "" {
		who = it.Sender + " · " + who
	}
	lines = append(lines, who)
	if it.DueDate != nil {
		lines = append(lines, i18n.T(d.lang, "subscriptions-deliver.due", i18n.Args{"when": d.when(*it.DueDate)}))
	}
	if body := strings.TrimSpace(it.Body); body != "" {
		lines = append(lines, "", clip(body, contextBodyMost))
	}
	if it.Link != "" {
		lines = append(lines, "", it.Link)
	}
	return strings.Join(lines, "\n")
}

// line is an item in one line: when, who, what, its link.
func (d deliverer) line(h Held) string {
	parts := []string{d.when(heldAt(h))}
	if h.Sender != "" {
		parts = append(parts, h.Sender)
	}
	parts = append(parts, cmpOr(h.Title, clip(oneLine(h.Body), 80)))
	s := strings.Join(parts, " · ")
	if h.Link != "" {
		s += " " + h.Link
	}
	return s
}

func (d deliverer) when(t time.Time) string { return t.In(d.loc).Format("01-02 15:04") }

// digestThing is the catch-up as one row: a line an item, at most digestLinesMost, triaged once.
func (d deliverer) digestThing(items []Held) connector.InboxThing {
	slices.SortStableFunc(items, func(a, b Held) int {
		if c := heldAt(b).Compare(heldAt(a)); c != 0 {
			return c
		}
		return int(b.Seq - a.Seq)
	})
	var lines, uids []string
	for i, h := range items {
		uids = append(uids, h.UID)
		if i < digestLinesMost {
			lines = append(lines, d.line(h))
		}
	}
	if more := len(items) - digestLinesMost; more > 0 {
		lines = append(lines, i18n.T(d.lang, "subscriptions-deliver.digest_more", i18n.Args{"count": more}))
	}
	slices.Sort(uids)
	title := i18n.T(d.lang, "subscriptions-deliver.digest_title", i18n.Args{"label": d.sub.Label, "count": len(items)})
	thread := d.thread("catchup")
	if c := d.sub.CatchupFrom; c != nil {
		// What a changed recipe found at once: a row of its own, not the first catch-up's again.
		title = i18n.T(d.lang, "subscriptions-deliver.digest_title_recipe", i18n.Args{"label": d.sub.Label, "count": len(items)})
		thread = d.thread("catchup-" + strconv.FormatInt(c.Unix(), 10))
	}
	return connector.InboxThing{Thread: thread, Kind: wire.ConnectorSubscriptionKindSubDigest, Label: d.sub.Label,
		Title: title, Preview: clip(oneLine(cmpOr(items[0].Title, items[0].Body)), previewMost), Context: strings.Join(lines, "\n"),
		At: heldAt(items[0]), Last: "digest:" + hashOf(uids...)[:24], Rules: d.rules, Extra: d.fallback(false), Tags: d.tags()}
}

// snapshotThing is a snapshot that changed: the state as the source answered it.
func (d deliverer) snapshotThing(h Held) connector.InboxThing {
	return connector.InboxThing{Thread: d.thread("snapshot"), Kind: cmpOr(h.Kind, wire.ConnectorSubscriptionKindSubUpdate),
		Label: d.sub.Label, Title: cmpOr(h.Title, d.sub.Label), Preview: clip(oneLine(h.Body), previewMost),
		Context: clip(h.Body, contextBodyMost), At: h.Stored, Last: h.UID, Rules: d.rules, Extra: d.fallback(false), Tags: d.tags()}
}

// failing is the subscription's failure notice: FYI by rule once it has not pulled for failingNotice (one a
// failure, while it is on), else the end of one that may be open.
func (d deliverer) failing() connector.InboxThing {
	since := d.sub.FailingSince
	if !on(d.sub) || since == nil || d.now.Sub(*since) < failingNotice {
		done := connector.InboxThing{Thread: d.thread("failing"), Kind: KindFailing, Done: connector.DoneRead}
		if on(d.sub) {
			done.Why = i18n.T(d.lang, "subscriptions-deliver.recovered")
		}
		return done
	}
	reason := i18n.T(d.lang, "subscriptions-deliver.reason.other")
	if d.sub.Error != nil {
		switch d.sub.Error.Code {
		case CodeCallFailed, CodeNotReadOnly, CodeNoList, CodeShapeChanged, CodeToolChanged:
			reason = i18n.T(d.lang, "subscriptions-deliver.reason."+d.sub.Error.Code)
		}
	}
	return connector.InboxThing{Thread: d.thread("failing"), Kind: KindFailing, Label: d.sub.Label,
		Title:   i18n.T(d.lang, "subscriptions-deliver.failing_title", i18n.Args{"label": d.sub.Label, "since": d.when(*since)}),
		Preview: reason, Context: reason, At: since.Add(failingNotice), Last: "failing:" + strconv.FormatInt(since.Unix(), 10),
		Tier: connector.TierFYI, Why: i18n.T(d.lang, "subscriptions-deliver.failing_why"), Tags: d.tags()}
}

// ---- the knowledge base ----

// Archives are the subscriptions the knowledge base reads now: those with it on.
func (s *Service) Archives(ctx context.Context) ([]string, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, sub := range all {
		if sub.ToKB {
			out = append(out, sub.ID)
		}
	}
	return out, nil
}

// IsSubscription says whether id names a subscription (an internal source, not a connector).
func (s *Service) IsSubscription(ctx context.Context, id string) bool {
	sub, err := s.o.Store.GetSubscription(ctx, id)
	return err == nil && sub != nil
}

// ErrArchiveOff answers Changes for a subscription whose knowledge base is off (or that is gone).
var ErrArchiveOff = errorf(http.StatusConflict, "archive_off", "the subscription does not go to the knowledge base")

// Changes are subscription id's held items after cursor (a stored position; "" the first time) as archive batches:
// a chat a thread or one for the whole subscription (the recipe's group_by), a message an item under its uid. It
// starts a day before the knowledge base was turned on, also with a cursor from before it was last off. The
// knowledge base moves its cursor once it has written them.
func (s *Service) Changes(ctx context.Context, id, cursor string, _ int) ([]connector.ChatBatch, string, bool, error) {
	sub, err := s.o.Store.GetSubscription(ctx, id)
	if err != nil {
		return nil, cursor, false, err
	}
	if sub == nil || kbFrom(*sub) == nil {
		return nil, cursor, false, ErrArchiveOff
	}
	from := kbFrom(*sub)
	after, _ := strconv.ParseInt(cursor, 10, 64)
	held, err := s.o.Store.HeldItems(ctx, id, ItemQuery{After: after, Since: from.Add(-catchupBack), Limit: kbPageMost})
	if err != nil {
		return nil, cursor, false, err
	}
	if len(held) == 0 {
		return nil, cursor, false, nil
	}
	d := s.deliverer(*sub, s.now())
	var out []connector.ChatBatch
	index := map[string]int{}
	for _, h := range held {
		chat := d.chatOf(h)
		i, ok := index[chat.ID]
		if !ok {
			i = len(out)
			index[chat.ID] = i
			out = append(out, connector.ChatBatch{Chat: chat})
		}
		out[i].Messages = append(out[i].Messages, d.message(h))
	}
	return out, strconv.FormatInt(held[len(held)-1].Seq, 10), len(held) == kbPageMost, nil
}

// chatOf is the knowledge-base chat an item goes to: its thread's (named by the thread's first item the knowledge base
// sees), or the subscription's own; a snapshot always the subscription's.
func (d deliverer) chatOf(h Held) kbwire.SourceChat {
	if h.Snapshot || d.sub.Recipe.GroupBy != wire.ConnectorSubscriptionGroupThread {
		return kbwire.SourceChat{ID: "all", Name: d.sub.Label, Kind: "subscription"}
	}
	key := cmpOr(h.Thread, h.UID)
	return kbwire.SourceChat{ID: "t" + hashOf(key)[:16], Name: cmpOr(h.Title, d.sub.Label), Kind: "subscription"}
}

// message is an item as a knowledge-base message: 【title】 and the body, its due date and link on lines of their own.
func (d deliverer) message(h Held) kbwire.SourceMessage {
	var b strings.Builder
	if h.Title != "" && (!h.Snapshot || h.Title != d.sub.Label) { // a snapshot's default title is the chat's name
		b.WriteString("【" + h.Title + "】")
	}
	b.WriteString(strings.TrimSpace(h.Body))
	if h.DueDate != nil {
		b.WriteString("\n" + i18n.T(d.lang, "subscriptions-deliver.due", i18n.Args{"when": d.when(*h.DueDate)}))
	}
	if h.Link != "" {
		b.WriteString("\n" + h.Link)
	}
	return kbwire.SourceMessage{ID: h.UID, SenderName: h.Sender, Type: "TEXT", Text: strings.TrimSpace(b.String()), At: heldAt(h)}
}
