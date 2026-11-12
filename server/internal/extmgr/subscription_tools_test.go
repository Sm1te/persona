package extmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// The assistant's subscription tools (#5240): they read what the API reads, and every save waits for the user's own
// approval on a card that says what changes.

// toolConn is a connector with one source of made-up notices.
type toolConn struct {
	mu     sync.Mutex
	answer string
	calls  int
}

func (*toolConn) ID() string   { return "example" }
func (*toolConn) Name() string { return "Example" }
func (*toolConn) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapCatalog}}
}
func (*toolConn) Catalog(context.Context) ([]connector.CatalogSource, error) {
	return []connector.CatalogSource{{ID: "notices", Name: "Notices", Tools: []connector.CatalogTool{
		{Name: "list_notices", Title: "List notices", Description: "Made-up notices", ReadOnly: true,
			InputSchema: json.RawMessage(`{"type":"object","properties":{"since":{"type":"string"},"limit":{"type":"integer"}}}`)},
		{Name: "send_notice", Description: "A write by its name", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}}, nil
}
func (c *toolConn) CatalogCall(context.Context, connector.CatalogQuery) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.answer, nil
}
func (c *toolConn) set(answer string) { c.mu.Lock(); c.answer = answer; c.mu.Unlock() }

type toolConns struct{ c *toolConn }

func (tc toolConns) Find(_ context.Context, id string) (subscriptions.ConnectorInfo, bool) {
	return subscriptions.ConnectorInfo{ID: "example", Conn: tc.c, Status: connector.StateOK, Enabled: true}, id == "example"
}
func (tc toolConns) All(ctx context.Context) []subscriptions.ConnectorInfo {
	info, _ := tc.Find(ctx, "example")
	return []subscriptions.ConnectorInfo{info}
}

func toolNotices(n int) string {
	var items []string
	for i := range n {
		items = append(items, fmt.Sprintf(`{"id":"n%d","title":"Notice %d","from":{"name":"Office"},"at":"2026-11-10T08:%02d:00Z","text":"Made-up text"}`, i, i, i))
	}
	return `{"items":[` + strings.Join(items, ",") + `]}`
}

const toolRecipe = `{"mode":"items","tool":"list_notices","args":{"since":"{{since}}"},"items_path":"$.items",` +
	`"fields":{"id":"$.id","title":"$.title","sender":"$.from.name","time":"$.at","body":"$.text"},"kind":"sub_notice","group_by":"source"}`

type toolEnv struct {
	t     *testing.T
	h     *Manager
	conn  *toolConn
	mu    sync.Mutex
	cards []proto.Interaction
	reply func(proto.Interaction) proto.Resolution
}

func allowCard(proto.Interaction) proto.Resolution { yes := true; return proto.Resolution{Allow: &yes} }

func newToolEnv(t *testing.T) *toolEnv {
	e := &toolEnv{t: t, h: newTestManager(t), conn: &toolConn{answer: toolNotices(2)}, reply: allowCard}
	e.h.opts.Lang = func() i18n.Lang { return i18n.En } // the card is in the user's language
	e.h.subs = subscriptions.New(subscriptions.Options{Store: subscriptions.NewMemStore(), Connectors: toolConns{e.conn},
		Lang: func() i18n.Lang { return i18n.En }})
	e.h.opts.Ask = func(_ context.Context, ix proto.Interaction) (proto.Resolution, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.cards = append(e.cards, ix)
		return e.reply(ix), nil
	}
	return e
}

// call runs a tool as the assistant's conversation conv1 does; its JSON answer and whether it is an error.
func (e *toolEnv) call(name, args string) (map[string]any, bool) {
	e.t.Helper()
	res := e.h.callSubscriptionTool(context.Background(), asker{"mac", "conv1"}, name, json.RawMessage(args))
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		e.t.Fatalf("%s answered no JSON: %s", name, res.Content[0].Text)
	}
	return out, res.IsError
}

func (e *toolEnv) lastCard() proto.Interaction {
	e.t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cards) == 0 {
		e.t.Fatal("no card was asked")
	}
	return e.cards[len(e.cards)-1]
}

func (e *toolEnv) cardCount() int { e.mu.Lock(); defer e.mu.Unlock(); return len(e.cards) }

// made is a subscription made through the service, its Inbox on.
func (e *toolEnv) made() subscriptions.Subscription {
	e.t.Helper()
	var r subscriptions.Recipe
	_ = json.Unmarshal([]byte(toolRecipe), &r)
	ctx := context.Background()
	s, err := e.h.subs.Create(ctx, subscriptions.CreateRequest{Connector: "example", Source: "notices", Recipe: &r}, subscriptions.Actor{ID: "device:phone"})
	if err != nil {
		e.t.Fatal(err)
	}
	if s, err = e.h.subs.Update(ctx, s.ID, subscriptions.Patch{Revision: s.Revision, ToInbox: ptrTo(true)}, subscriptions.Actor{ID: "device:phone"}); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func ptrTo[T any](v T) *T { return &v }

func TestSubscriptionToolsAreTheAssistantsWhileSubscriptionsRun(t *testing.T) {
	h := newTestManager(t)
	if len(h.subscriptionTools()) != 0 {
		t.Fatal("tools before the service runs")
	}
	h.subs = subscriptions.New(subscriptions.Options{Store: subscriptions.NewMemStore(), Connectors: toolConns{&toolConn{}}})
	var names []string
	for _, tl := range h.subscriptionTools() {
		names = append(names, tl.Name)
		if !strings.HasPrefix(string(tl.InputSchema), `{"type":"object"`) || tl.Description == "" {
			t.Errorf("%s: %s", tl.Name, tl.InputSchema)
		}
	}
	if strings.Join(names, ",") != strings.Join(subscriptionToolNames, ",") || !isSubscriptionTool("subscription_save") || isSubscriptionTool("subscription") {
		t.Fatalf("tools: %v", names)
	}
}

func TestSubscriptionListGetAndSources(t *testing.T) {
	e := newToolEnv(t)
	s := e.made()
	e.h.subs.RunDue(context.Background())
	out, isErr := e.call("subscription_list", `{}`)
	subs, _ := out["subscriptions"].([]any)
	if isErr || len(subs) != 1 {
		t.Fatalf("list: %v", out)
	}
	line := subs[0].(map[string]any)
	if line["id"] != s.ID || line["label"] != "Notices" || line["tool"] != "list_notices" || line["state"] != "ok" || line["to_inbox"] != true ||
		line["last_new_count"] != float64(2) || line["revision"] != float64(s.Revision) || line["interval_seconds"] != float64(900) {
		t.Fatalf("a list line: %v", line)
	}
	out, isErr = e.call("subscription_get", fmt.Sprintf(`{"id":%q,"items":1}`, s.ID))
	items, _ := out["items"].([]any)
	runs, _ := out["runs"].([]any)
	sub, _ := out["subscription"].(map[string]any)
	if isErr || len(items) != 1 || len(runs) != 1 || sub["recipe"].(map[string]any)["fields"] == nil {
		t.Fatalf("get: %v", out)
	}
	if it := items[0].(map[string]any); it["title"] != "Notice 1" || it["sender"] != "Office" {
		t.Fatalf("the newest item: %v", it)
	}
	if out, isErr := e.call("subscription_get", `{"id":"sub_none"}`); !isErr || out["code"] != "not_found" {
		t.Fatalf("an unknown one: %v", out)
	}
	out, isErr = e.call("subscription_sources", `{"connector":"example"}`)
	b, _ := json.Marshal(out)
	if isErr || !strings.Contains(string(b), `"input_schema":{"type":"object"`) || !strings.Contains(string(b), `"refused":{"code":"write_verb"`) {
		t.Fatalf("sources of a connector: %s", b)
	}
	format := out["recipe_format"].(map[string]any)
	if fmt.Sprint(format["fields"]) != "[id thread title sender time body link due_date]" || format["template_vars"].(map[string]any)["{{since_date}}"] == nil ||
		fmt.Sprint(format["modes"].(map[string]any)["snapshot"]) == "" {
		t.Fatalf("the recipe format: %v", format)
	}
	out, _ = e.call("subscription_sources", `{}`)
	b, _ = json.Marshal(out["connectors"])
	if !strings.Contains(string(b), `"name":"list_notices"`) || !strings.Contains(string(b), `"refused":"write_verb"`) {
		t.Fatalf("every connector's, as read: %s", b)
	}
}

// A dry run of a change merges it onto the subscription's recipe and warns about keys the format does not know.
func TestSubscriptionDryRunMergesAChangeAndWarns(t *testing.T) {
	e := newToolEnv(t)
	s := e.made()
	out, isErr := e.call("subscription_dry_run", fmt.Sprintf(`{"id":%q,"recipe":{"fields":{"author":"$.from.name","sender":null},"args":{"limit":5}}}`, s.ID))
	dr, _ := out["dry_run"].(map[string]any)
	r, _ := out["recipe"].(map[string]any)
	if isErr || dr["ok"] != true || r == nil {
		t.Fatalf("dry run: %v", out)
	}
	fields := r["fields"].(map[string]any)
	if fields["sender"] != nil || fields["title"] != "$.title" || fmt.Sprint(r["args"]) != "map[limit:5 since:{{since}}]" {
		t.Fatalf("the merged recipe: %v", r)
	}
	b, _ := json.Marshal(dr["warnings"])
	if !strings.Contains(string(b), `"code":"unknown_key","key":"fields.author"`) {
		t.Fatalf("warnings: %s", b)
	}
	if e.cardCount() != 0 {
		t.Fatal("a dry run asked the user")
	}
}

func TestSubscriptionSaveAsksTheUserAndAppliesOnlyTheirApproval(t *testing.T) {
	e := newToolEnv(t)
	s := e.made()
	save := func(rev int64, label string) (map[string]any, bool) {
		return e.call("subscription_save", fmt.Sprintf(`{"action":"update","id":%q,"revision":%d,"label":%q}`, s.ID, rev, label))
	}
	out, isErr := save(s.Revision, "Work mail")
	if isErr || out["saved"] != true {
		t.Fatalf("an approved change: %v", out)
	}
	card := e.lastCard()
	if card.Kind != proto.IxPermission || card.ToolName != "subscription_save" || card.MachineID != "mac" || card.SessionID != "conv1" ||
		!strings.Contains(card.Summary, `Change "Notices"?`) || !strings.Contains(card.Summary, "• Name: Notices → Work mail") {
		t.Fatalf("the card: %+v", card)
	}
	if prev := out["previous"].(map[string]any); prev["label"] != "Notices" || len(prev) != 1 {
		t.Fatalf("previous: %v", prev)
	}
	cur, _, _ := e.h.subs.Get(context.Background(), s.ID)
	if cur.Label != "Work mail" {
		t.Fatalf("not saved: %q", cur.Label)
	}
	// Declined, unanswered and answered for the user: nothing changes, and only a decline says the user declined.
	no := false
	for _, c := range []struct {
		reply proto.Resolution
		why   string
	}{
		{proto.Resolution{Allow: &no}, "declined"},
		{proto.Resolution{}, "did not answer"},
		{proto.Resolution{Allow: ptrTo(true), AnsweredBy: proto.AnsweredByAssistant}, "did not answer"},
		{proto.Resolution{Allow: ptrTo(true), Dismiss: true}, "did not answer"},
	} {
		e.reply = func(proto.Interaction) proto.Resolution { return c.reply }
		out, isErr := save(cur.Revision, "Other")
		if isErr || out["saved"] != false || !strings.Contains(fmt.Sprint(out["why"]), c.why) {
			t.Fatalf("%+v: %v", c.reply, out)
		}
		if now, _, _ := e.h.subs.Get(context.Background(), s.ID); now.Label != "Work mail" || now.Revision != cur.Revision {
			t.Fatalf("%+v changed it: %+v", c.reply, now)
		}
	}
	e.reply = allowCard
	// A stale revision is a conflict with the subscription as it is, before any card.
	n := e.cardCount()
	if out, isErr := save(s.Revision, "Stale"); !isErr || out["code"] != "conflict" || out["subscription"] == nil || e.cardCount() != n {
		t.Fatalf("a stale revision: %v", out)
	}
	// Nothing to change asks nothing.
	if out, _ := save(cur.Revision, "Work mail"); out["saved"] != false || e.cardCount() != n {
		t.Fatalf("no change: %v", out)
	}
}

func TestSubscriptionSaveDryRunsARecipeFirst(t *testing.T) {
	e := newToolEnv(t)
	s := e.made()
	n := e.cardCount()
	// Keys the format does not know are not saved, nor asked about.
	out, _ := e.call("subscription_save", fmt.Sprintf(`{"action":"update","id":%q,"revision":%d,"recipe":{"fields":{"url":"$.link"}}}`, s.ID, s.Revision))
	if out["saved"] != false || !strings.Contains(fmt.Sprint(out["why"]), "does not know") || e.cardCount() != n {
		t.Fatalf("an unknown key: %v", out)
	}
	// A recipe that fails its dry run is not saved.
	out, _ = e.call("subscription_save", fmt.Sprintf(`{"action":"update","id":%q,"revision":%d,"recipe":{"items_path":"$.nothing"}}`, s.ID, s.Revision))
	if out["saved"] != false || out["dry_run"].(map[string]any)["ok"] != false || e.cardCount() != n {
		t.Fatalf("a failing recipe: %v", out)
	}
	// A recipe that passes: the card says what the new one does with what it finds at once.
	out, isErr := e.call("subscription_save", fmt.Sprintf(`{"action":"update","id":%q,"revision":%d,"recipe":{"args":{"limit":30}},"existing":"baseline"}`, s.ID, s.Revision))
	if isErr || out["saved"] != true || out["existing"] != "baseline" {
		t.Fatalf("a recipe change: %v", out)
	}
	card := e.lastCard().Summary
	if !strings.Contains(card, `• Recipe: still uses "List notices", reading its results differently`) || !strings.Contains(card, "A dry run just now found 2 items.") ||
		!strings.Contains(card, "only recorded, not added") {
		t.Fatalf("the card of a recipe change:\n%s", card)
	}
	if prev := out["previous"].(map[string]any); prev["recipe"] == nil {
		t.Fatalf("previous: %v", prev)
	}
	// existing alone is refused: it goes with a recipe that changes.
	cur, _, _ := e.h.subs.Get(context.Background(), s.ID)
	if out, isErr := e.call("subscription_save", fmt.Sprintf(`{"action":"update","id":%q,"revision":%d,"existing":"deliver"}`, s.ID, cur.Revision)); !isErr || out["code"] != "bad_request" {
		t.Fatalf("existing alone: %v", out)
	}
}

func TestSubscriptionSaveCreatesPausesAndDeletes(t *testing.T) {
	e := newToolEnv(t)
	out, isErr := e.call("subscription_save", `{"action":"create","connector":"example","source":"notices","recipe":`+toolRecipe+`,"label":"Office notices","to_inbox":true}`)
	if isErr || out["saved"] != true {
		t.Fatalf("create: %v", out)
	}
	card := e.lastCard().Summary
	if !strings.Contains(card, "Subscribe to Notices from example?") || !strings.Contains(card, "• Name: Office notices") || !strings.Contains(card, "• Add to Inbox: On") ||
		!strings.Contains(card, "Triaged by the Information filtering model") || strings.Contains(card, "Turn on Add to Inbox?") {
		t.Fatalf("the create card:\n%s", card)
	}
	line := out["subscription"].(map[string]any)
	id, rev := line["id"].(string), int64(line["revision"].(float64))
	if line["label"] != "Office notices" || line["to_inbox"] != true || line["to_kb"] != false {
		t.Fatalf("made: %v", line)
	}
	out, isErr = e.call("subscription_save", fmt.Sprintf(`{"action":"update","id":%q,"revision":%d,"pause":true}`, id, rev))
	if isErr || out["subscription"].(map[string]any)["state"] != "off" || !strings.Contains(e.lastCard().Summary, "• Add to Inbox: On → Off") {
		t.Fatalf("pause: %v\n%s", out, e.lastCard().Summary)
	}
	rev = int64(out["subscription"].(map[string]any)["revision"].(float64))
	out, isErr = e.call("subscription_save", fmt.Sprintf(`{"action":"delete","id":%q,"revision":%d,"delete_data":true}`, id, rev))
	if isErr || out["deleted"] != id {
		t.Fatalf("delete: %v", out)
	}
	if card := e.lastCard().Summary; !strings.Contains(card, `Delete "Office notices"?`) || !strings.Contains(card, "can't be undone") {
		t.Fatalf("the delete card:\n%s", card)
	}
	if _, _, err := e.h.subs.Get(context.Background(), id); err == nil {
		t.Fatal("still there")
	}
}

func TestSubscriptionRunWaitsForTheRun(t *testing.T) {
	poll := subscriptionRunPoll
	subscriptionRunPoll = 10 * time.Millisecond
	t.Cleanup(func() { subscriptionRunPoll = poll })
	e := newToolEnv(t)
	s := e.made()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { // the runner's loop, without its start-up wait
		for ctx.Err() == nil {
			e.h.subs.RunDue(ctx)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	out, isErr := e.call("subscription_run", fmt.Sprintf(`{"id":%q,"wait_seconds":10}`, s.ID))
	if isErr || out["ran"] != true {
		t.Fatalf("run: %v", out)
	}
	run := out["run"].(map[string]any)
	items, _ := out["new_items"].([]any)
	if run["trigger"] != "now" || run["new"] != float64(2) || len(items) != 2 {
		t.Fatalf("the run: %v", out)
	}
	cancel()
	if out, _ := e.call("subscription_run", fmt.Sprintf(`{"id":%q,"wait_seconds":0}`, s.ID)); out["ran"] != false || out["why"] == nil {
		t.Fatalf("a run not waited for: %v", out)
	}
}
