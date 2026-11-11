package subscriptions

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Push sources (#3848, docs/extensions/subscriptions.md "Push sources"): a subscription its device fills itself, from
// the iOS Shortcuts action Handle Notification, instead of a recipe pulling a connector. Its connector is PushConnector
// and its source the device; it has no recipe, interval or runs on a schedule, and goes through the same destinations,
// triage and history as any other subscription.

// PushConnector is a push source's connector: reserved, as no extension or connector id starts with "-".
const PushConnector = "-shortcuts"

// TriggerPush is a push's run in a subscription's history.
const TriggerPush = "push"

// CodePushSource refuses what only a pulled subscription has (a recipe, a schedule, Run Now, drafting).
const CodePushSource = "push_source"

// The limits of a push.
const (
	pushItemsMost   = 20  // items one push takes
	pushPerHourMost = 300 // items a device pushes an hour
	pushAppMost     = 100
	pushKeyMost     = 128
	pushLinkMost    = 2000
	pushAhead       = 5 * time.Minute // a time further ahead than this is taken as now
)

// PushItem is one item a device hands in.
type PushItem struct {
	// Key dedupes it: the same key again adds nothing. Without one, a hash of the app, title, body and minute.
	Key   string     `json:"key,omitempty"`
	Title string     `json:"title,omitempty"`
	Body  string     `json:"body,omitempty"`
	App   string     `json:"app,omitempty"` // where it came from: its sender, and with the title its thread
	Link  string     `json:"link,omitempty"`
	Time  *time.Time `json:"time,omitempty"` // when it happened; default now
}

// PushRequest is a push: no items only makes the device's source (adding it by hand).
type PushRequest struct {
	Items []PushItem `json:"items"`
}

// PushResult is what a push kept: the source as it is now, how many items were new and how many came.
type PushResult struct {
	Subscription Subscription `json:"subscription"`
	New          int          `json:"new"`
	Count        int          `json:"count"`
}

// IsPush says whether sub is a push source.
func IsPush(sub Subscription) bool { return sub.Connector == PushConnector }

// pushRecipe is what a push source's items are, in a recipe's terms (the pages and the destinations read it): items,
// notices, grouped by thread (the app and title).
var pushRecipe = Recipe{Mode: wire.ConnectorSubscriptionModeItems, Kind: wire.ConnectorSubscriptionKindSubNotice, GroupBy: "thread"}

func pushOnly(what string) *Error {
	return errorf(http.StatusBadRequest, CodePushSource, "a push source has no "+what+": its device sends its items")
}

// PushSource is the device's push source, nil while it has none.
func (s *Service) PushSource(ctx context.Context, device string) (*Subscription, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	for _, sub := range all {
		if IsPush(sub) && sub.Source == device {
			out := shown(sub)
			return &out, nil
		}
	}
	return nil, nil
}

// Push keeps what a device hands in as its push source's items, making the source on its first push with both
// destinations off (as every new subscription). New items reach the destinations that are on; the push is a run in
// the source's history.
func (s *Service) Push(ctx context.Context, by Actor, in PushRequest) (PushResult, error) {
	if by.ID == "" {
		return PushResult{}, errorf(http.StatusBadRequest, CodeBadRequest, "a push comes from a paired device")
	}
	if len(in.Items) > pushItemsMost {
		return PushResult{}, errorf(http.StatusRequestEntityTooLarge, CodeBadRequest, "at most 20 items a push")
	}
	now := s.now()
	items := make([]Item, 0, len(in.Items))
	for _, p := range in.Items {
		it, ok := pushedItem(p, now)
		if !ok {
			return PushResult{}, errorf(http.StatusBadRequest, CodeBadRequest, "an item needs a title or a body")
		}
		items = append(items, it)
	}
	// One push at a time: two pushes of the same item add it once, and the hourly count holds.
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	sent := slices.DeleteFunc(s.pushed[by.ID], func(t time.Time) bool { return now.Sub(t) >= time.Hour })
	if len(sent)+len(items) > pushPerHourMost {
		s.pushed[by.ID] = sent
		retry := time.Hour - now.Sub(sent[0])
		return PushResult{}, &Error{Status: http.StatusTooManyRequests, Code: CodeRateLimited,
			Message: "too many items from this device in an hour", RetryAfter: retry.Seconds()}
	}
	cur, err := s.pushSource(ctx, by, now)
	if err != nil {
		return PushResult{}, err
	}
	if len(items) == 0 {
		s.pushed[by.ID] = sent
		return PushResult{Subscription: shown(*cur)}, nil
	}
	for range items {
		sent = append(sent, now)
	}
	s.pushed[by.ID] = sent
	uids := make([]string, len(items))
	for i, it := range items {
		uids[i] = it.UID
	}
	known, err := s.o.Store.SeenKeys(ctx, cur.ID, uids)
	if err != nil {
		return PushResult{}, err
	}
	var fresh []Item
	var seen []Seen
	in1 := map[string]bool{}
	for _, it := range items {
		if known[it.UID] || in1[it.UID] {
			continue
		}
		in1[it.UID] = true
		it.SubscriptionID = cur.ID
		fresh, seen = append(fresh, it), append(seen, Seen{UID: it.UID, ContentHash: hashOf(it.Title, it.Body)})
	}
	next := *cur
	next.RunState, next.Error, next.FailCount, next.FailingSince, next.PausedUntil = StateOK, nil, 0, nil, nil
	next.LastOKAt, next.LastNewCount, next.NextRunAt = &now, len(fresh), nil
	next.SinceAt = &now
	run := Run{At: now, Trigger: TriggerPush, New: len(fresh), Count: len(items)}
	if !s.commit(ctx, *cur, next, &commitData{seen: seen, items: fresh}, &run, now, true) {
		return PushResult{}, errorf(http.StatusInternalServerError, CodeCallFailed, "the items could not be kept")
	}
	if len(fresh) > 0 {
		s.o.Logf("subscription %s: %d pushed, %d new", cur.ID, len(items), len(fresh))
		if on(next) {
			s.o.Delivered(next)
		}
	}
	s.prune(ctx, next, now)
	out, err := s.o.Store.GetSubscription(ctx, cur.ID)
	if err != nil || out == nil {
		return PushResult{Subscription: shown(next), New: len(fresh), Count: len(items)}, nil
	}
	return PushResult{Subscription: shown(*out), New: len(fresh), Count: len(items)}, nil
}

// pushSource is by's push source, made now when it has none. Its label names the device, in the user's language.
func (s *Service) pushSource(ctx context.Context, by Actor, now time.Time) (*Subscription, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	for _, sub := range all {
		if IsPush(sub) && sub.Source == by.ID {
			return &sub, nil
		}
	}
	device := cmpOr(clip(oneLine(by.Name), 100), "iPhone")
	label := i18n.T(s.o.Lang(), "subscriptions-deliver.push_label", i18n.Args{"device": device})
	sub := Subscription{ID: newID(), Connector: PushConnector, Source: by.ID, SourceName: device, Label: label, Icon: "bell",
		Recipe: pushRecipe, RecipeOrigin: OriginPush, RecipeAt: now, RunState: StateOK, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := s.o.Store.CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	s.o.Audit(ctx, PushConnector, "subscription_created", by, map[string]any{"subscription": sub.ID, "source": "push"})
	s.o.Logf("subscription %s: push source made for a device", sub.ID)
	s.o.Changed(shown(sub), false)
	return &sub, nil
}

// pushedItem is p as a held item, false when it has neither a title nor a body. The title is the body's first line
// when it has none; a link that is not http(s) is dropped; a time ahead of now is now.
func pushedItem(p PushItem, now time.Time) (Item, bool) {
	body := strings.TrimSpace(p.Body)
	title := oneLine(p.Title)
	if title == "" {
		first, _, _ := strings.Cut(body, "\n")
		title = oneLine(first)
	}
	if title == "" {
		return Item{}, false
	}
	title = clip(title, titleMax)
	app := clip(oneLine(p.App), pushAppMost)
	when := now
	if p.Time != nil && !p.Time.IsZero() && !p.Time.After(now.Add(pushAhead)) {
		when = p.Time.UTC()
	}
	key := clip(strings.TrimSpace(p.Key), pushKeyMost)
	if key == "" {
		key = hashOf(app, title, body, when.Truncate(time.Minute).Format(time.RFC3339))[:32]
	}
	it := Item{UID: "push:" + key, Thread: "app:" + hashOf(app, title)[:16], Title: title, Sender: app, Time: &when,
		Body: clip(body, bodyMax), Kind: wire.ConnectorSubscriptionKindSubNotice}
	if u, err := url.Parse(strings.TrimSpace(p.Link)); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && len(p.Link) <= pushLinkMost {
		it.Link = u.String()
	}
	return it, true
}
