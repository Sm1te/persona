package subscriptions

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// Delivery to the Inbox and the knowledge base (#2039): the things and batches a subscription hands them, made up
// from made-up notices.

// note is a made-up notice as the fake tool lists it.
type note struct {
	id, thread, title, at, due, link string
}

func notesJSON(ns ...note) string {
	items := []map[string]any{}
	for _, n := range ns {
		m := map[string]any{"id": n.id, "title": n.title, "from": map[string]any{"name": "Office"}, "at": n.at, "text": "Made-up notice text"}
		for k, v := range map[string]string{"thread": n.thread, "due": n.due, "link": n.link} {
			if v != "" {
				m[k] = v
			}
		}
		items = append(items, m)
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return string(b)
}

// threadRecipe reads every field, grouped by thread.
func threadRecipe() Recipe {
	return Recipe{Mode: "items", Tool: "list_notices", Args: json.RawMessage(`{"since":"{{since}}","limit":25}`), ItemsPath: "$.items",
		Fields: &connector.SubscriptionFields{ID: "$.id", Thread: "$.thread", Title: "$.title", Sender: "$.from.name", Time: "$.at",
			Body: "$.text", Link: "$.link", DueDate: "$.due"},
		Kind: "sub_notice", GroupBy: "thread"}
}

func (e *env) answer(s string) {
	e.conn.setAnswer(func(connector.CatalogQuery) (string, error) { return s, nil })
}

func (e *env) patch(id string, p Patch) Subscription {
	e.t.Helper()
	p.Revision = e.get(id).Revision
	out, err := e.svc.Update(e.ctx, id, p, Actor{ID: "device:phone"})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// inbox is subscription id as a source of an Inbox round.
func (e *env) inbox(id string) InboxSource {
	e.t.Helper()
	srcs, err := e.svc.InboxSources(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, s := range srcs {
		if s.ID == id {
			return s
		}
	}
	e.t.Fatalf("subscription %s is not an Inbox source", id)
	return InboxSource{}
}

// rows are the things that make or move rows (not the end of a notice).
func (e *env) rows(id string) []connector.InboxThing {
	var out []connector.InboxThing
	for _, t := range e.inbox(id).Things {
		if t.Done == "" {
			out = append(out, t)
		}
	}
	return out
}

func rowOf(t *testing.T, rows []connector.InboxThing, thread string) connector.InboxThing {
	t.Helper()
	for _, r := range rows {
		if r.Thread == thread {
			return r
		}
	}
	t.Fatalf("no row of thread %s in %+v", thread, rows)
	return connector.InboxThing{}
}

func ruleTier(t connector.InboxThing) string {
	if t.Extra == nil {
		return ""
	}
	return t.Extra.RuleTier
}

func TestInboxRowsCarryTheItemsAFallbackTierAndTheRules(t *testing.T) {
	e := newEnv(t)
	e.answer(notesJSON(
		note{id: "n1", thread: "t1", title: "Office hours moved", at: "2026-11-10T08:00:00Z", link: "https://example.com/n1"},
		note{id: "n2", thread: "t1", title: "Office hours moved again", at: "2026-11-10T08:30:00Z", link: "http://example.com/n2"},
		note{id: "n3", title: "Form due", at: "2026-11-10T07:00:00Z", due: "2026-11-12T18:00:00Z"},
	))
	sub := e.turnOn(e.create(threadRecipe()))
	if rows := e.rows(sub.ID); len(rows) != 0 {
		t.Fatalf("rows before the first run since the Inbox was turned on: %+v", rows)
	}
	e.svc.RunDue(e.ctx)
	if len(e.delivered) != 1 || e.delivered[0] != sub.ID {
		t.Fatalf("the destinations were told: %v", e.delivered)
	}
	src := e.inbox(sub.ID)
	if src.Name != "Notices" || !strings.Contains(src.Kinds["sub_digest"], "Notices") || src.Kinds[KindFailing] == "" {
		t.Fatalf("the source: %q %v", src.Name, src.Kinds)
	}
	rows := e.rows(sub.ID)
	if len(rows) != 2 {
		t.Fatalf("three caught-up items in two threads: %d rows", len(rows))
	}
	r := rowOf(t, rows, "sub:"+sub.ID+":t1")
	want := connector.InboxThing{Thread: "sub:" + sub.ID + ":t1", Kind: "sub_notice", Label: "Notices", Title: "Office hours moved again",
		Sender: "Office", Preview: "Made-up notice text", At: time.Date(2026, 11, 10, 8, 30, 0, 0, time.UTC), Last: "n2",
		Extra: &connector.ThingExtra{RuleTier: "fyi"}, Tags: []connector.Tag{{Key: "source", Value: sub.ID, Name: "Notices"}}}
	ctx := r.Context
	r.Context = ""
	if !reflect.DeepEqual(r, want) {
		t.Fatalf("the thread's row:\n got %+v\nwant %+v", r, want)
	}
	if !strings.Contains(ctx, "11-10 08:00 · Office · Office hours moved https://example.com/n1") ||
		!strings.Contains(ctx, "Office hours moved again\nOffice · 11-10 08:30\n\nMade-up notice text\n\nhttp://example.com/n2") {
		t.Fatalf("the thread's context:\n%s", ctx)
	}
	due := rowOf(t, rows, "sub:"+sub.ID+":n3")
	if ruleTier(due) != "action" || !strings.Contains(due.Context, "Due 11-12 18:00") || due.URL != "" || due.CanReply {
		t.Fatalf("an item with a due date: %+v", due)
	}
	// The fallback tier is the subscription's.
	for tier, want := range map[string][2]string{"wait": {"", ""}, "fyi": {"fyi", "fyi"}, "action": {"action", "action"}, "": {"fyi", "action"}} {
		e.patch(sub.ID, Patch{FallbackTier: ptr(tier)})
		rows := e.rows(sub.ID)
		if got := [2]string{ruleTier(rowOf(t, rows, "sub:"+sub.ID+":t1")), ruleTier(rowOf(t, rows, "sub:"+sub.ID+":n3"))}; got != want {
			t.Errorf("fallback %q: %v, want %v", tier, got, want)
		}
	}
	// Rules: the subscription's own, else its extension's, else none (the Inbox's own).
	if r := rowOf(t, e.rows(sub.ID), "sub:"+sub.ID+":n3"); r.Rules != "" {
		t.Fatalf("rules without any: %q", r.Rules)
	}
	e.extRules = "Notices from the office are FYI."
	if r := rowOf(t, e.rows(sub.ID), "sub:"+sub.ID+":n3"); r.Rules != e.extRules {
		t.Fatalf("the extension's rules: %q", r.Rules)
	}
	e.patch(sub.ID, Patch{TriageRules: ptr("  Forms need me.  ")})
	if r := rowOf(t, e.rows(sub.ID), "sub:"+sub.ID+":n3"); r.Rules != "Forms need me." {
		t.Fatalf("the subscription's rules: %q", r.Rules)
	}
}

func TestACatchUpOfThirtyIsOneDigestAndUpcomingDeadlinesStayRows(t *testing.T) {
	e := newEnv(t)
	var ns []note
	start := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	for i := range 30 {
		ns = append(ns, note{id: fmt.Sprint("n", i), title: fmt.Sprint("Notice ", i), at: start.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)})
	}
	ns[3].due = "2026-11-11T12:00:00Z" // still ahead: a row of its own
	ns[4].due = "2026-11-09T12:00:00Z" // past: in the digest
	e.answer(notesJSON(ns...))
	sub := e.turnOn(e.create(threadRecipe()))
	e.svc.RunDue(e.ctx)
	rows := e.rows(sub.ID)
	if len(rows) != 2 {
		t.Fatalf("30 caught-up items: %d rows, want the digest and the upcoming deadline", len(rows))
	}
	digest := rowOf(t, rows, "sub:"+sub.ID+":catchup")
	if digest.Kind != "sub_digest" || digest.Title != "Notices: 29 items from the day before it was turned on" || ruleTier(digest) != "fyi" ||
		!digest.At.Equal(start) || !strings.HasPrefix(digest.Last, "digest:") || strings.Count(digest.Context, "\n") != 28 {
		t.Fatalf("the digest: %+v", digest)
	}
	if !strings.HasPrefix(digest.Context, "11-10 08:00 · Office · Notice 0\n") || !strings.Contains(digest.Context, "Notice 4") {
		t.Fatalf("the digest's lines, newest first, the past deadline among them:\n%s", digest.Context)
	}
	if r := rowOf(t, rows, "sub:"+sub.ID+":n3"); ruleTier(r) != "action" {
		t.Fatalf("the upcoming deadline: %+v", r)
	}
	// Later items come in as rows; the digest stays as it was.
	e.answer(notesJSON(append(ns, note{id: "n99", title: "A new notice", at: "2026-11-10T09:10:00Z"})...))
	e.clock.advance(20 * time.Minute)
	e.svc.RunDue(e.ctx)
	rows = e.rows(sub.ID)
	if len(rows) != 3 || rowOf(t, rows, "sub:"+sub.ID+":catchup").Last != digest.Last || rowOf(t, rows, "sub:"+sub.ID+":n99").Title != "A new notice" {
		t.Fatalf("after a later item: %+v", rows)
	}
	// "Wait for the model": no fallback, for the digest too.
	e.patch(sub.ID, Patch{FallbackTier: ptr("wait")})
	if d := rowOf(t, e.rows(sub.ID), "sub:"+sub.ID+":catchup"); d.Extra != nil {
		t.Fatalf("the digest waits for the model: %+v", d.Extra)
	}
}

func TestADigestListsFiftyAndCountsTheRest(t *testing.T) {
	e := newEnv(t)
	var ns []note
	for i := range 60 {
		ns = append(ns, note{id: fmt.Sprint("n", i), title: fmt.Sprint("Notice ", i), at: "2026-11-10T08:00:00Z"})
	}
	e.answer(notesJSON(ns...))
	sub := e.turnOn(e.create(threadRecipe()))
	e.svc.RunDue(e.ctx)
	d := rowOf(t, e.rows(sub.ID), "sub:"+sub.ID+":catchup")
	lines := strings.Split(d.Context, "\n")
	if len(lines) != 51 || lines[50] != "and 10 more" || !strings.Contains(d.Title, "60 items") {
		t.Fatalf("a digest of 60: %d lines, the last %q; %q", len(lines), lines[len(lines)-1], d.Title)
	}
}

func TestTheInboxTurnedOnLaterCatchesUpOnTheDayBefore(t *testing.T) {
	e := newEnv(t)
	e.answer(notesJSON(note{id: "a1", title: "Old one", at: "2026-11-10T08:00:00Z"}, note{id: "a2", title: "Old two", at: "2026-11-10T08:10:00Z"}))
	sub := e.create(threadRecipe())
	e.patch(sub.ID, Patch{ToKB: ptr(true)})
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 0 {
		t.Fatalf("rows with only the knowledge base on: %+v", rows)
	}
	// Two days on, two more come; then the Inbox is turned on.
	e.clock.advance(48 * time.Hour)
	e.answer(notesJSON(note{id: "b1", title: "New one", at: "2026-11-12T08:00:00Z"}, note{id: "b2", title: "New two", at: "2026-11-12T08:10:00Z"}))
	e.svc.RunDue(e.ctx)
	e.clock.advance(time.Minute)
	e.patch(sub.ID, Patch{ToInbox: ptr(true)})
	if rows := e.rows(sub.ID); len(rows) != 0 {
		t.Fatalf("rows before the first run since: %+v", rows)
	}
	told := len(e.delivered)
	e.svc.RunDue(e.ctx) // runs at once (a destination was turned on), brings nothing new, and ends the catch-up
	if len(e.delivered) != told+1 {
		t.Fatal("the Inbox was not told its catch-up is ready")
	}
	rows := e.rows(sub.ID)
	if len(rows) != 2 || rows[0].Last == "a1" || rows[1].Last == "a1" {
		t.Fatalf("the catch-up of the day before: %+v", rows)
	}
}

func TestASnapshotRowOnlyWhenItChanges(t *testing.T) {
	e := newEnv(t)
	e.answer("Balance: 20")
	r := Recipe{Mode: "snapshot", Tool: "balance", Args: json.RawMessage(`{}`), Kind: "sub_update", GroupBy: "source"}
	sub := e.turnOn(e.create(r))
	e.svc.RunDue(e.ctx)
	rows := e.rows(sub.ID)
	if len(rows) != 1 || rows[0].Thread != "sub:"+sub.ID+":snapshot" || rows[0].Kind != "sub_update" || rows[0].Context != "Balance: 20" ||
		!strings.HasPrefix(rows[0].Last, "snap:") {
		t.Fatalf("the first snapshot: %+v", rows)
	}
	first := rows[0].Last
	e.clock.advance(24 * time.Hour)
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 1 || rows[0].Last != first || len(e.store.Items()) != 1 {
		t.Fatalf("an unchanged snapshot: %+v, %d kept", rows, len(e.store.Items()))
	}
	e.answer("Balance: 12")
	e.clock.advance(24 * time.Hour)
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 1 || rows[0].Last == first || rows[0].Context != "Balance: 12" {
		t.Fatalf("a changed snapshot: %+v", rows)
	}
}

func messageIDs(b connector.ChatBatch) []string {
	var out []string
	for _, m := range b.Messages {
		out = append(out, m.ID)
	}
	return out
}

func TestTheKnowledgeBaseGetsAChatAThreadOrOneForTheSubscription(t *testing.T) {
	e := newEnv(t)
	e.answer(notesJSON(
		note{id: "n1", thread: "t1", title: "Office hours moved", at: "2026-11-10T08:00:00Z", link: "https://example.com/n1"},
		note{id: "n2", thread: "t1", title: "Office hours moved again", at: "2026-11-10T08:30:00Z"},
		note{id: "n3", title: "Form due", at: "2026-11-10T07:00:00Z", due: "2026-11-12T18:00:00Z"},
	))
	byThread := e.create(threadRecipe())
	if _, _, _, err := e.svc.Changes(e.ctx, byThread.ID, "", 20); !errors.Is(err, ErrArchiveOff) {
		t.Fatalf("with the knowledge base off: %v", err)
	}
	e.patch(byThread.ID, Patch{ToKB: ptr(true)})
	bySource := threadRecipe()
	bySource.GroupBy = "source"
	whole := e.create(bySource)
	e.patch(whole.ID, Patch{ToKB: ptr(true)})
	e.svc.RunDue(e.ctx)
	if ids, _ := e.svc.Archives(e.ctx); len(ids) != 2 {
		t.Fatalf("archives: %v", ids)
	}

	batches, next, more, err := e.svc.Changes(e.ctx, byThread.ID, "", 20)
	if err != nil || more || next == "" || len(batches) != 2 {
		t.Fatalf("by thread: %+v %q %v %v", batches, next, more, err)
	}
	t1 := batches[0]
	if t1.Chat.ID != "t"+hashOf("t1")[:16] || t1.Chat.Name != "Office hours moved" || t1.Chat.Kind != "subscription" ||
		!reflect.DeepEqual(messageIDs(t1), []string{"n1", "n2"}) {
		t.Fatalf("thread t1's chat: %+v", t1)
	}
	m := t1.Messages[0]
	if m.Text != "【Office hours moved】Made-up notice text\nhttps://example.com/n1" || m.SenderName != "Office" || m.SenderID != "" ||
		!m.At.Equal(time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("a message: %+v", m)
	}
	if m := batches[1].Messages[0]; m.ID != "n3" || !strings.HasSuffix(m.Text, "\nDue 11-12 18:00") {
		t.Fatalf("an item with a due date: %+v", m)
	}
	// From the cursor nothing is new; from the start (a replay) the same messages again, which its receipts drop.
	if again, cur, _, _ := e.svc.Changes(e.ctx, byThread.ID, next, 20); len(again) != 0 || cur != next {
		t.Fatalf("after the cursor: %+v %q", again, cur)
	}
	if replay, _, _, _ := e.svc.Changes(e.ctx, byThread.ID, "", 20); !reflect.DeepEqual(replay, batches) {
		t.Fatalf("a replay differs:\n%+v\n%+v", replay, batches)
	}

	batches, _, _, err = e.svc.Changes(e.ctx, whole.ID, "", 20)
	if err != nil || len(batches) != 1 || batches[0].Chat.ID != "all" || batches[0].Chat.Name != "Notices" || len(batches[0].Messages) != 3 {
		t.Fatalf("by source: %+v %v", batches, err)
	}
}

func TestTheKnowledgeBaseStartsADayBeforeItWasTurnedOn(t *testing.T) {
	e := newEnv(t)
	e.answer(notesJSON(note{id: "a1", title: "Old", at: "2026-11-10T08:00:00Z"}))
	sub := e.create(threadRecipe())
	e.patch(sub.ID, Patch{ToKB: ptr(true), ToInbox: ptr(true)})
	e.svc.RunDue(e.ctx)
	_, cur, _, _ := e.svc.Changes(e.ctx, sub.ID, "", 20)
	e.patch(sub.ID, Patch{ToKB: ptr(false)})
	// While the knowledge base is off two more days of items come; it is turned on again on the fourth.
	for i, day := range []int{11, 12, 13} {
		e.clock.advance(24 * time.Hour)
		e.answer(notesJSON(note{id: fmt.Sprint("b", i), title: "Later", at: fmt.Sprintf("2026-11-%dT08:00:00Z", day)}))
		e.svc.RunDue(e.ctx)
	}
	e.clock.advance(time.Hour)
	e.patch(sub.ID, Patch{ToKB: ptr(true)})
	batches, _, _, err := e.svc.Changes(e.ctx, sub.ID, cur, 20)
	if err != nil || len(batches) != 1 || !reflect.DeepEqual(messageIDs(batches[0]), []string{"b2"}) {
		t.Fatalf("with the old cursor: %+v %v", batches, err)
	}
}

func TestARestartedManagerHandsTheDestinationsTheSameThings(t *testing.T) {
	e := newEnv(t)
	var ns []note
	for i := range 8 {
		ns = append(ns, note{id: fmt.Sprint("n", i), thread: fmt.Sprint("t", i%3), title: fmt.Sprint("Notice ", i), at: "2026-11-10T08:00:00Z"})
	}
	e.answer(notesJSON(ns...))
	sub := e.create(threadRecipe())
	e.patch(sub.ID, Patch{ToKB: ptr(true), ToInbox: ptr(true)})
	e.svc.RunDue(e.ctx)
	things := e.inbox(sub.ID).Things
	batches, next, _, _ := e.svc.Changes(e.ctx, sub.ID, "", 20)
	e.svc = e.service()
	e.clock.advance(20 * time.Minute)
	e.svc.RunDue(e.ctx) // the same answer again
	if again := e.inbox(sub.ID).Things; !reflect.DeepEqual(again, things) {
		t.Fatalf("after a restart and a replay the Inbox gets:\n%+v\nnot\n%+v", again, things)
	}
	if again, cur, _, _ := e.svc.Changes(e.ctx, sub.ID, next, 20); len(again) != 0 || cur != next {
		t.Fatalf("after a restart the knowledge base gets more: %+v", again)
	}
	if replay, _, _, _ := e.svc.Changes(e.ctx, sub.ID, "", 20); !reflect.DeepEqual(replay, batches) {
		t.Fatal("after a restart a replay of the knowledge base differs")
	}
}

func TestADayOfFailuresIsOneFYINoticeDoneOnceItPullsAgain(t *testing.T) {
	e := newEnv(t)
	e.answer(notesJSON(note{id: "n1", title: "One", at: "2026-11-10T08:00:00Z"}))
	sub := e.turnOn(e.create(threadRecipe()))
	e.conn.setAnswer(func(connector.CatalogQuery) (string, error) { return "", errors.New("upstream said no") })
	notice := func() connector.InboxThing {
		for _, th := range e.inbox(sub.ID).Things {
			if th.Thread == "sub:"+sub.ID+":failing" {
				return th
			}
		}
		t.Fatal("no notice thing")
		return connector.InboxThing{}
	}
	e.svc.RunDue(e.ctx)
	if n := notice(); n.Done != "read" {
		t.Fatalf("a first failure: %+v", n)
	}
	for range 26 {
		e.clock.advance(time.Hour)
		e.svc.RunDue(e.ctx)
	}
	n := notice()
	if n.Done != "" || n.Tier != "fyi" || n.Kind != KindFailing || n.Title != "Notices hasn't pulled anything since 11-10 09:00" ||
		n.Preview != "The connector's call failed." || n.Last != fmt.Sprint("failing:", time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC).Unix()) ||
		n.Why == "" {
		t.Fatalf("a day of failures: %+v", n)
	}
	e.clock.advance(time.Hour)
	e.svc.RunDue(e.ctx)
	if again := notice(); again.Last != n.Last {
		t.Fatalf("one notice a failure: %q then %q", n.Last, again.Last)
	}
	e.answer(notesJSON(note{id: "n1", title: "One", at: "2026-11-10T08:00:00Z"}))
	for i := 0; i < 8 && e.get(sub.ID).State != StateOK; i++ {
		e.clock.advance(time.Hour)
		e.svc.RunDue(e.ctx)
	}
	if n := notice(); n.Done != "read" || n.Why != "The subscription pulls again" {
		t.Fatalf("after it pulls again: %+v", n)
	}
}

func TestDeletingWithItsDataHasTheDestinationsForgetIt(t *testing.T) {
	e := newEnv(t)
	sub := e.turnOn(e.create(threadRecipe()))
	e.svc.RunDue(e.ctx)
	e.forget = func(Subscription) error { return errors.New("the assistant is away") }
	if err := e.svc.Delete(e.ctx, sub.ID, true, Actor{ID: "device:phone"}); err == nil {
		t.Fatal("a delete whose data could not be removed succeeded")
	}
	if got := e.get(sub.ID); got.State != StateOff || len(e.forgot) != 1 || e.forgot[0].ToInbox {
		t.Fatalf("after a failed removal: %s, forgot %d", got.State, len(e.forgot))
	}
	e.forget = nil
	if err := e.svc.Delete(e.ctx, sub.ID, true, Actor{ID: "device:phone"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Get(e.ctx, sub.ID); codeOf(err) != CodeNotFound || len(e.forgot) != 2 {
		t.Fatalf("after deleting with its data: %v, forgot %d", err, len(e.forgot))
	}
	other := e.turnOn(e.create(threadRecipe()))
	if err := e.svc.Delete(e.ctx, other.ID, false, Actor{ID: "device:phone"}); err != nil || len(e.forgot) != 2 {
		t.Fatalf("deleting without its data asked the destinations: %v %d", err, len(e.forgot))
	}
}

func TestHeldItemsAreKeptOnlyWhileTheirDestinationsNeedThem(t *testing.T) {
	e := newEnv(t)
	inbox := e.turnOn(e.create(threadRecipe()))
	kb := e.create(threadRecipe())
	e.patch(kb.ID, Patch{ToKB: ptr(true)})
	e.svc.RunDue(e.ctx)
	held := func(id string) int {
		n := 0
		for _, it := range e.store.Items() {
			if it.SubscriptionID == id {
				n++
			}
		}
		return n
	}
	if held(inbox.ID) != 3 || held(kb.ID) != 3 {
		t.Fatalf("held: %d %d", held(inbox.ID), held(kb.ID))
	}
	e.clock.advance(9 * 24 * time.Hour)
	e.svc.RunDue(e.ctx)
	if held(inbox.ID) != 0 || held(kb.ID) != 3 {
		t.Fatalf("after 9 days: %d %d (the Inbox's window is past; the knowledge base keeps them)", held(inbox.ID), held(kb.ID))
	}
	e.clock.advance(22 * 24 * time.Hour)
	e.svc.RunDue(e.ctx)
	if held(kb.ID) != 0 {
		t.Fatalf("after 31 days the knowledge base's still: %d", held(kb.ID))
	}
	if e.store.SeenCount(kb.ID) != 3 {
		t.Fatal("pruning took the dedup keys")
	}
}

// The destinations' marks are kept in the database's document, and a run's commit keeps the catch-up mark only for
// the Inbox switch-on it saw.
func TestTheDestinationsMarksAreKept(t *testing.T) {
	from, mark, kbOn := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 9, 5, 0, 0, time.UTC), time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
	sub := Subscription{ID: "sub_a", ToInbox: true, InboxFrom: &from, InboxCatchup: &mark, KBFrom: &kbOn}
	raw, err := encode(sub)
	if err != nil {
		t.Fatal(err)
	}
	back, err := decode(raw)
	if err != nil || !sameTime(back.InboxFrom, &from) || !sameTime(back.InboxCatchup, &mark) || !sameTime(back.KBFrom, &kbOn) {
		t.Fatalf("decoded: %+v %v", back, err)
	}
	// The Inbox was turned off and on again while a run went: its mark belongs to the earlier switch-on.
	again := from.Add(time.Hour)
	cur := Subscription{ID: "sub_a", ToInbox: true, InboxFrom: &again}
	applyRun(&cur, RunCommit{Sub: sub}, true)
	if cur.InboxCatchup != nil {
		t.Fatalf("a run kept the mark of another switch-on: %v", cur.InboxCatchup)
	}
	cur.InboxFrom = &from
	applyRun(&cur, RunCommit{Sub: sub}, false)
	if !sameTime(cur.InboxCatchup, &mark) {
		t.Fatal("a run's mark for the switch-on it saw was not kept")
	}
}
