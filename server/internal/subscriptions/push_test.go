package subscriptions

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var phone = Actor{ID: "device:phone", Name: "Test iPhone"}

func (e *env) push(by Actor, items ...PushItem) PushResult {
	e.t.Helper()
	out, err := e.svc.Push(e.ctx, by, PushRequest{Items: items})
	if err != nil {
		e.t.Fatalf("push: %v", err)
	}
	return out
}

func TestAPushMakesTheDevicesSourceOffAndKeepsWhatIsNew(t *testing.T) {
	e := newEnv(t)
	a := PushItem{Key: "k1", Title: "Parcel arrives today", Body: "Between 2 and 4 pm.", App: "Example Post"}
	b := PushItem{Key: "k2", Title: "Bus delayed", Body: "Line 7 runs 10 minutes late.", App: "Example Transit"}
	out := e.push(phone, a, b)
	sub := out.Subscription
	if out.New != 2 || out.Count != 2 || !IsPush(sub) || sub.Source != phone.ID || sub.State != StateOff || sub.ToInbox || sub.ToKB {
		t.Fatalf("first push: %+v", out)
	}
	if sub.Label != "Shortcuts on Test iPhone" || sub.SourceName != "Test iPhone" || sub.RecipeOrigin != OriginPush || sub.NextRunAt != nil {
		t.Fatalf("the source: %+v", sub)
	}
	if items := e.store.Items(); len(items) != 2 || items[0].Sender != "Example Post" || items[0].Kind != "sub_notice" || items[0].UID != "push:k1" {
		t.Fatalf("held items: %+v", items)
	}
	// The same items again add nothing; a new one does. Each push is a run.
	if again := e.push(phone, a, b, PushItem{Key: "k3", Title: "Library book due"}); again.New != 1 || again.Count != 3 || again.Subscription.ID != sub.ID {
		t.Fatalf("second push: %+v", again)
	}
	_, runs, err := e.svc.Get(e.ctx, sub.ID)
	if err != nil || len(runs) != 2 || runs[0].Trigger != TriggerPush || runs[0].New != 1 || runs[1].New != 2 {
		t.Fatalf("runs: %+v %v", runs, err)
	}
	if len(e.audit) != 1 || e.audit[0] != "-shortcuts subscription_created device:phone" {
		t.Fatalf("audit: %v", e.audit)
	}
	// Another device has its own source.
	if other := e.push(Actor{ID: "device:pad", Name: "Test iPad"}, a); other.Subscription.ID == sub.ID || other.New != 1 {
		t.Fatalf("another device's push: %+v", other)
	}
	// No items: only the source, added by hand; again it is the same one.
	if hand := e.push(phone); hand.Subscription.ID != sub.ID || hand.Count != 0 {
		t.Fatalf("adding by hand: %+v", hand)
	}
	if src, err := e.svc.PushSource(e.ctx, phone.ID); err != nil || src == nil || src.ID != sub.ID {
		t.Fatalf("PushSource: %+v %v", src, err)
	}
	if none, _ := e.svc.PushSource(e.ctx, "device:watch"); none != nil {
		t.Fatalf("a device that never pushed has a source: %+v", none)
	}
}

func TestAnItemWithoutAKeyIsDedupedByItsContentWithinAMinute(t *testing.T) {
	e := newEnv(t)
	it := PushItem{Title: "Door opened", App: "Example Home"}
	if out := e.push(phone, it); out.New != 1 {
		t.Fatalf("first: %+v", out)
	}
	if out := e.push(phone, it); out.New != 0 {
		t.Fatalf("a shortcut that fired twice added it twice: %+v", out)
	}
	e.clock.advance(2 * time.Minute)
	if out := e.push(phone, it); out.New != 1 {
		t.Fatalf("the same words later are a new item: %+v", out)
	}
}

func TestPushedItemsAreCheckedAndCut(t *testing.T) {
	e := newEnv(t)
	future := e.clock.Now().Add(time.Hour)
	past := e.clock.Now().Add(-time.Hour)
	e.push(phone,
		PushItem{Key: "a", Body: "First line of the body\nSecond line", App: "  Example   Mail  ", Link: "javascript:alert(1)", Time: &future},
		PushItem{Key: "b", Title: strings.Repeat("t", 900), Body: strings.Repeat("b", 9000), Link: "https://example.com/item/1", Time: &past})
	items := e.store.Items()
	if a := items[0]; a.Title != "First line of the body" || a.Sender != "Example Mail" || a.Link != "" || !a.Time.Equal(e.clock.Now()) {
		t.Fatalf("a: %+v", a)
	}
	if b := items[1]; len([]rune(b.Title)) > titleMax || len([]rune(b.Body)) > bodyMax || b.Link != "https://example.com/item/1" || !b.Time.Equal(past) {
		t.Fatalf("b: title %d body %d link %q time %v", len(b.Title), len(b.Body), b.Link, b.Time)
	}
	if _, err := e.svc.Push(e.ctx, phone, PushRequest{Items: []PushItem{{App: "Example", Body: "  "}}}); codeOf(err) != CodeBadRequest {
		t.Fatalf("an item with no words: %v", err)
	}
	many := make([]PushItem, pushItemsMost+1)
	for i := range many {
		many[i] = PushItem{Key: fmt.Sprint(i), Title: "x"}
	}
	if _, err := e.svc.Push(e.ctx, phone, PushRequest{Items: many}); codeOf(err) != CodeBadRequest {
		t.Fatalf("too many items a push: %v", err)
	}
	if _, err := e.svc.Push(e.ctx, Actor{}, PushRequest{}); codeOf(err) != CodeBadRequest {
		t.Fatalf("a push without a device: %v", err)
	}
}

func TestADeviceHasAnHourlyLimit(t *testing.T) {
	e := newEnv(t)
	n := 0
	for range pushPerHourMost / pushItemsMost {
		batch := make([]PushItem, pushItemsMost)
		for i := range batch {
			n++
			batch[i] = PushItem{Key: fmt.Sprint(n), Title: "x"}
		}
		e.push(phone, batch...)
	}
	_, err := e.svc.Push(e.ctx, phone, PushRequest{Items: []PushItem{{Key: "over", Title: "x"}}})
	var se *Error
	if !errors.As(err, &se) || se.Code != CodeRateLimited || se.RetryAfter <= 0 {
		t.Fatalf("over the hourly limit: %v", err)
	}
	e.clock.advance(time.Hour)
	e.push(phone, PushItem{Key: "later", Title: "x"})
}

func TestPushedItemsReachTheInboxOnceItIsOn(t *testing.T) {
	e := newEnv(t)
	sub := e.push(phone, PushItem{Key: "a", Title: "Alex", Body: "Lunch at noon?", App: "Example Chat"},
		PushItem{Key: "b", Title: "Alex", Body: "Or one o'clock.", App: "Example Chat"},
		PushItem{Key: "c", Title: "Parcel arrives today", App: "Example Post"}).Subscription
	if len(e.delivered) != 0 {
		t.Fatal("told the destinations while both are off")
	}
	e.clock.advance(time.Minute)
	e.patch(sub.ID, Patch{ToInbox: ptr(true)})
	if len(e.delivered) != 1 {
		t.Fatal("the Inbox was not told what the source holds")
	}
	// What came in the day before comes in now, one row a thread (the app and the title), with no pull.
	rows := e.rows(sub.ID)
	if len(rows) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	for _, r := range rows {
		if r.Label != sub.Label || r.Kind != "sub_notice" {
			t.Fatalf("row: %+v", r)
		}
	}
	e.clock.advance(time.Minute)
	e.push(phone, PushItem{Key: "d", Title: "Bus delayed", App: "Example Transit"})
	if len(e.delivered) != 2 || len(e.rows(sub.ID)) != 3 {
		t.Fatalf("a push while it is on: told %d, rows %+v", len(e.delivered), e.rows(sub.ID))
	}
	if calls := len(e.conn.calls); calls != 0 {
		t.Fatalf("a connector was called %d times", calls)
	}
}

func TestAPushSourceHasNoRecipeScheduleOrPull(t *testing.T) {
	e := newEnv(t)
	sub := e.push(phone, PushItem{Key: "a", Title: "x"}).Subscription
	sub = e.patch(sub.ID, Patch{ToKB: ptr(true), TriageRules: ptr("Everything is FYI."), FallbackTier: ptr("fyi"), Label: ptr("My phone")})
	if sub.Label != "My phone" || !sub.ToKB || sub.NextRunAt != nil || sub.State != StateOK {
		t.Fatalf("its switches, rules and label: %+v", sub)
	}
	r := pushRecipe
	for name, p := range map[string]Patch{"recipe": {Recipe: &r}, "interval": {IntervalSeconds: ptr(3600)}, "daily_at": {DailyAt: ptr("08:00")}} {
		p.Revision = sub.Revision
		if _, err := e.svc.Update(e.ctx, sub.ID, p, phone); codeOf(err) != CodePushSource {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.RunNow(e.ctx, sub.ID, phone); codeOf(err) != CodePushSource {
		t.Fatalf("run now: %v", err)
	}
	if _, err := e.svc.DryRun(e.ctx, DryRunRequest{ID: sub.ID}); codeOf(err) != CodePushSource {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := e.svc.Generate(e.ctx, GenerateRequest{ID: sub.ID}, phone); codeOf(err) != CodePushSource {
		t.Fatalf("drafting: %v", err)
	}
	e.clock.advance(24 * time.Hour)
	e.svc.RunDue(e.ctx)
	if got := e.get(sub.ID); len(got.Runs) != 1 || got.State != StateOK {
		t.Fatalf("the scheduler ran a push source: %+v", got.Runs)
	}
	// Deleting it with its data has the destinations forget it, as any subscription.
	if err := e.svc.Delete(e.ctx, sub.ID, true, phone); err != nil || len(e.forgot) != 1 || len(e.store.Items()) != 0 {
		t.Fatalf("delete: %v, forgot %d, items %d", err, len(e.forgot), len(e.store.Items()))
	}
}
