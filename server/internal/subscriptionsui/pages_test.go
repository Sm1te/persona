package subscriptionsui

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

var now = time.Date(2026, 11, 10, 10, 41, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { t := now.Add(d); return &t }

// fake is a subscriptions.Service kept in memory, recording what the pages asked of it.
type fake struct {
	subs      map[string]subscriptions.Subscription
	runs      map[string][]subscriptions.Run
	cats      []subscriptions.Catalog
	dry       subscriptions.DryRun
	dryReq    *subscriptions.DryRunRequest
	created   []subscriptions.CreateRequest
	patches   map[string][]subscriptions.Patch
	updateErr error
	ran       []string
	deleted   []string
	pushed    []subscriptions.Actor // devices whose push source was added (#3848)
	generate  bool
	model     subscriptions.ModelInfo
	// gen answers Generate; release (when set) holds it until closed.
	gen       func(subscriptions.GenerateRequest) (subscriptions.Generated, error)
	release   chan struct{}
	genAsks   []subscriptions.GenerateRequest
	deleteErr error
}

func (f *fake) Features() subscriptions.Features {
	return subscriptions.Features{Deliver: true, Generate: f.generate}
}

func (f *fake) Generate(_ context.Context, in subscriptions.GenerateRequest, _ subscriptions.Actor) (subscriptions.Generated, error) {
	f.genAsks = append(f.genAsks, in)
	if f.release != nil {
		<-f.release
	}
	return f.gen(in)
}

func (f *fake) ModelOf(_ context.Context, purpose string) subscriptions.ModelInfo {
	if purpose == "inbox.triage" {
		return subscriptions.ModelInfo{OK: true, Provider: "Example Cloud", Model: "small"}
	}
	return f.model
}

func (f *fake) RecipeModel(context.Context) subscriptions.ModelInfo { return f.model }

func (f *fake) List(_ context.Context, fl subscriptions.Filter) ([]subscriptions.Subscription, subscriptions.Features, error) {
	var out []subscriptions.Subscription
	order := []string{"sub_outlook", "sub_slack", "sub_github", "sub_broker", "sub_notion", "sub_new"}
	var more []string // made by a test (a push source): after these, by id
	for id := range f.subs {
		if !slices.Contains(order, id) {
			more = append(more, id)
		}
	}
	slices.Sort(more)
	for _, id := range append(order, more...) {
		s, ok := f.subs[id]
		if !ok || fl.Extension != "" && s.Extension != fl.Extension || fl.Connector != "" && s.Connector != fl.Connector {
			continue
		}
		out = append(out, s)
	}
	return out, subscriptions.Features{Deliver: true}, nil
}

func (f *fake) Get(_ context.Context, id string) (subscriptions.Subscription, []subscriptions.Run, error) {
	s, ok := f.subs[id]
	if !ok {
		return s, nil, &subscriptions.Error{Status: http.StatusNotFound, Code: subscriptions.CodeNotFound, Message: "not found"}
	}
	return s, f.runs[id], nil
}

func (f *fake) Catalog(_ context.Context, id string) (subscriptions.Catalog, error) {
	for _, c := range f.cats {
		if c.Connector == id {
			return c, nil
		}
	}
	return subscriptions.Catalog{}, &subscriptions.Error{Status: http.StatusNotFound, Code: subscriptions.CodeNotFound}
}

func (f *fake) Catalogs(context.Context) ([]subscriptions.Catalog, error) {
	return append([]subscriptions.Catalog(nil), f.cats...), nil
}

func (f *fake) DryRun(_ context.Context, in subscriptions.DryRunRequest) (subscriptions.DryRun, error) {
	f.dryReq = &in
	return f.dry, nil
}

func (f *fake) Create(_ context.Context, in subscriptions.CreateRequest, _ subscriptions.Actor) (subscriptions.Subscription, error) {
	f.created = append(f.created, in)
	s := subscriptions.Subscription{ID: "sub_new", Connector: in.Connector, Source: in.Source, Label: "Calendar", State: subscriptions.StateOff,
		IntervalSeconds: 900, MinIntervalSeconds: 600, Revision: 1, RecipeOrigin: subscriptions.OriginExtension, RecipeAt: now}
	if in.Recipe != nil {
		s.Recipe, s.RecipeOrigin = *in.Recipe, subscriptions.OriginUser
	} else {
		s.Recipe = f.cats[0].Sources[1].Recipes[0]
	}
	f.subs[s.ID] = s
	return s, nil
}

func (f *fake) Update(_ context.Context, id string, p subscriptions.Patch, _ subscriptions.Actor) (subscriptions.Subscription, error) {
	if f.updateErr != nil {
		return subscriptions.Subscription{}, f.updateErr
	}
	if f.patches == nil {
		f.patches = map[string][]subscriptions.Patch{}
	}
	f.patches[id] = append(f.patches[id], p)
	s := f.subs[id]
	if p.ToInbox != nil {
		s.ToInbox = *p.ToInbox
	}
	if p.ToKB != nil {
		s.ToKB = *p.ToKB
	}
	if p.IntervalSeconds != nil {
		s.IntervalSeconds = *p.IntervalSeconds
	}
	if p.DailyAt != nil {
		s.DailyAt = *p.DailyAt
	}
	if p.FallbackTier != nil {
		s.FallbackTier = *p.FallbackTier
	}
	if s.ToInbox || s.ToKB {
		s.State = subscriptions.StateOK
	}
	s.Revision++
	f.subs[id] = s
	return s, nil
}

func (f *fake) RunNow(_ context.Context, id string, _ subscriptions.Actor) (subscriptions.Subscription, error) {
	f.ran = append(f.ran, id)
	return f.subs[id], nil
}

func (f *fake) Delete(_ context.Context, id string, _ bool, _ subscriptions.Actor) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	delete(f.subs, id)
	return nil
}

func ptr[T any](v T) *T { return &v }

func newFake() *fake {
	listSchema := json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string","enum":["primary","work"],"title":"Calendar"},` +
		`"updated_min":{"type":"string","format":"date-time","description":"Only events changed after this time"},"max_results":{"type":"integer","default":25},` +
		`"show_deleted":{"type":"boolean"}},"required":["calendar"]}`)
	f := &fake{subs: map[string]subscriptions.Subscription{
		"sub_outlook": {ID: "sub_outlook", Connector: "apps", Extension: "apps", Source: "outlook", SourceName: "Outlook", Label: "Outlook", Icon: "mail",
			Recipe: subscriptions.Recipe{Mode: "items", Tool: "search_messages", Kind: "sub_mail", GroupBy: "thread",
				Fields: &wire.ConnectorSubscriptionFields{Title: "$.subject", Sender: "$.from.name", Time: "$.receivedDateTime", ID: "$.id", Thread: "$.conversationId"}},
			RecipeOrigin: subscriptions.OriginExtension, RecipeAt: now.Add(-48 * time.Hour), ToInbox: true, ToKB: true, IntervalSeconds: 900,
			State: subscriptions.StateOK, LastOKAt: at(-3 * time.Minute), LastNewCount: 2, NextRunAt: at(12 * time.Minute), MinIntervalSeconds: 600, Revision: 4},
		"sub_slack": {ID: "sub_slack", Connector: "apps", Extension: "apps", Source: "slack", Label: "Slack", Icon: "chat",
			Recipe: subscriptions.Recipe{Mode: "items", Tool: "list_messages", Kind: "sub_message", GroupBy: "thread"}, ToInbox: true, IntervalSeconds: 900,
			State: subscriptions.StateNeedsRecipe, Error: &subscriptions.Problem{Code: subscriptions.CodeToolChanged}, Revision: 2},
		"sub_github": {ID: "sub_github", Connector: "github", Source: "github", Label: "GitHub", Icon: "bell",
			Recipe: subscriptions.Recipe{Mode: "items", Tool: "notifications", Kind: "sub_notice", GroupBy: "source"}, ToInbox: true, IntervalSeconds: 900,
			State: subscriptions.StateFailing, Error: &subscriptions.Problem{Code: subscriptions.CodeCallFailed}, FailCount: 4,
			FailingSince: at(-21 * time.Minute), NextRunAt: at(11 * time.Minute), LastOKAt: at(-36 * time.Minute), Revision: 9},
		"sub_broker": {ID: "sub_broker", Connector: "apps", Extension: "apps", Source: "broker", Label: "Holdings", Icon: "wallet",
			Recipe: subscriptions.Recipe{Mode: "snapshot", Tool: "positions", Kind: "sub_update", GroupBy: "source"}, ToKB: true,
			IntervalSeconds: 86400, DailyAt: "08:00", State: subscriptions.StateOK, LastOKAt: at(-2 * time.Hour), MinIntervalSeconds: 3600, Revision: 1},
	},
		runs: map[string][]subscriptions.Run{"sub_github": {
			{At: now.Add(-time.Minute), MS: 30000, Trigger: "retry", Error: &subscriptions.Problem{Code: subscriptions.CodeCallFailed}},
			{At: now.Add(-36 * time.Minute), MS: 1200, Trigger: "schedule", Count: 25},
			{At: now.Add(-51 * time.Minute), MS: 2000, Trigger: "now", Count: 25, New: 3},
		}},
		cats: []subscriptions.Catalog{{Connector: "apps", Extension: "apps", Status: "ok", Sources: []subscriptions.CatalogSource{
			{ID: "outlook", Name: "Outlook", Icon: "mail", Tools: []subscriptions.CatalogTool{{Name: "search_messages", Title: "Search messages", Subscribable: true}},
				Subscriptions: []string{"sub_outlook"}},
			{ID: "calendar", Name: "Calendar", Icon: "calendar", MinIntervalSeconds: ptr(600), Tools: []subscriptions.CatalogTool{
				{Name: "list_events", Title: "List events", Description: "Events of one calendar", Subscribable: true, InputSchema: listSchema},
				{Name: "get_event", Title: "Get an event", Subscribable: true, InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
				{Name: "create_event", Title: "Create an event", Refused: &subscriptions.Problem{Code: subscriptions.CodeWriteVerb}},
			}, Recipes: []subscriptions.Recipe{{Mode: "items", Tool: "list_events", Kind: "sub_event", GroupBy: "source"}}},
			{ID: "gmail", Name: "Gmail", Icon: "mail", Service: "mail:example.com", Tools: []subscriptions.CatalogTool{{Name: "search", Subscribable: true}}},
			{ID: "danger", Name: "Payments", Tools: []subscriptions.CatalogTool{{Name: "send_money", Refused: &subscriptions.Problem{Code: subscriptions.CodeWriteVerb}}}},
		}}},
	}
	return f
}

func env(lang i18n.Lang, edit bool) Env {
	return Env{Lang: lang, Loc: time.UTC, Now: now, CanEdit: edit, By: subscriptions.Actor{ID: "dev_phone"},
		Name: func(c string) string { return map[string]string{"apps": "Connected apps", "github": "GitHub"}[c] },
		Native: func(service string) string {
			if service == "mail:example.com" {
				return "me@example.com"
			}
			return ""
		}}
}

func handle(t *testing.T, f *fake, e Env, st state, action string, values map[string]string) wire.ActionResult {
	t.Helper()
	res, err := Handle(context.Background(), f, e, Request{State: st.String(), Action: action, Values: values})
	if err != nil {
		t.Fatalf("%v %s: %v", st, action, err)
	}
	return res
}

func draw(t *testing.T, f *fake, e Env, st state) wire.View {
	t.Helper()
	res := handle(t, f, e, st, "", nil)
	if res.View == nil {
		t.Fatalf("%v drew no view", st)
	}
	return *res.View
}

func section(v wire.View, key string) *wire.Section {
	for i := range v.Sections {
		if v.Sections[i].Key == key {
			return &v.Sections[i]
		}
	}
	return nil
}

func item(v wire.View, key string) *wire.Component {
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			if v.Sections[i].Items[j].Key == key {
				return &v.Sections[i].Items[j]
			}
		}
	}
	return nil
}

func keys(s *wire.Section) []string {
	var out []string
	if s != nil {
		for _, c := range s.Items {
			out = append(out, c.Key)
		}
	}
	return out
}

// The list: failing and needs-recipe first, then on; available sources are those with a subscribable tool and no
// subscription, a source Persona already reads is marked, and one extension's page lists only its own.
func TestList(t *testing.T) {
	f := newFake()
	v := draw(t, f, env(i18n.En, true), state{})
	if got := keys(section(v, "attention")); strings.Join(got, ",") != "s.sub_slack,s.sub_github" {
		t.Errorf("attention: %v", got)
	}
	if got := keys(section(v, "on")); strings.Join(got, ",") != "s.sub_outlook,s.sub_broker" {
		t.Errorf("on: %v", got)
	}
	if got := keys(section(v, "avail.apps")); strings.Join(got, ",") != "a.apps/calendar,a.apps/gmail" {
		t.Errorf("available (no subscription, at least one subscribable tool): %v", got)
	}
	if g := item(v, "a.apps/gmail"); g.Badge == nil || g.Badge.Tone != "caution" {
		t.Errorf("a source Persona reads natively: %+v", g)
	}
	if s := item(v, "s.sub_github"); s.Badge == nil || s.Badge.Tone != "danger" || !strings.Contains(s.Subtitle, "10:52") {
		t.Errorf("a failing subscription says so and when it retries: %+v", s)
	}
	if r := item(v, "s.sub_broker"); r.Icon != "wallet" {
		t.Errorf("UI version 6 draws the source's own icon: %q", r.Icon)
	}
	old := env(i18n.En, true)
	old.UIVersion = 4
	if r := item(draw(t, f, old, state{}), "s.sub_broker"); r.Icon != "money" {
		t.Errorf("an older app gets an icon it knows, not none: %q", r.Icon)
	}
	v = draw(t, f, env(i18n.En, true), state{Ext: "apps"})
	if item(v, "s.sub_github") != nil {
		t.Error("an extension's list shows another connector's subscription")
	}
}

// A destination's switch asks before it turns on, never before it turns off.
func TestSwitchesConfirmBeforeTurningOn(t *testing.T) {
	f := newFake()
	s := f.subs["sub_outlook"]
	s.ToKB = false
	f.subs["sub_outlook"] = s
	v := draw(t, f, env(i18n.ZhHans, true), state{P: pageSource, ID: "sub_outlook"})
	in, kb := item(v, "d.to_inbox"), item(v, "d.to_kb")
	if in.Control == nil || !in.Control.On || in.Control.Action.Confirm != "" {
		t.Errorf("an on switch asks before turning off: %+v", in.Control)
	}
	if kb.Control == nil || kb.Control.On || !strings.Contains(kb.Control.Action.Confirm, "信息处理") {
		t.Errorf("an off switch does not say what turning it on does: %+v", kb.Control)
	}
	res := handle(t, f, env(i18n.ZhHans, true), state{P: pageSource, ID: "sub_outlook"}, "dest", map[string]string{"to": "to_kb", "to_kb": "true"})
	if p := f.patches["sub_outlook"]; len(p) != 1 || p[0].ToKB == nil || !*p[0].ToKB || p[0].Revision != 4 || p[0].ToInbox != nil {
		t.Fatalf("patch: %+v", p)
	}
	if c := item(*res.View, "d.to_kb").Control; !c.On {
		t.Error("the page drawn after does not show the switch on")
	}
}

// A device that may only look sees every page, with nothing that changes anything; its actions are refused.
func TestReadOnlyDevice(t *testing.T) {
	f := newFake()
	v := draw(t, f, env(i18n.En, false), state{P: pageSource, ID: "sub_outlook"})
	if v.Sections[0].Key != "read_only" {
		t.Error("no line saying why nothing can be changed")
	}
	if c := item(v, "d.to_inbox").Control; !c.Disabled {
		t.Error("a switch is enabled on a read-only device")
	}
	if item(v, "b.run") != nil || item(v, "b.delete") != nil {
		t.Error("buttons that change something are shown")
	}
	if r := item(v, "f.interval"); r == nil || r.Action == nil || r.Action.Push == nil {
		t.Error("a row that only opens a page should still open it")
	}
	v = draw(t, f, env(i18n.En, false), state{P: pageRules, ID: "sub_outlook"})
	if r := item(v, "r.text"); r == nil || r.Type != "row" {
		t.Errorf("a field is still editable: %+v", r)
	}
	if _, err := Handle(context.Background(), f, env(i18n.En, false), Request{State: state{P: pageSource, ID: "sub_outlook"}.String(), Action: "run_now"}); err != ErrReadOnly {
		t.Fatalf("an action from a read-only device: %v", err)
	}
	if len(f.ran) > 0 {
		t.Fatal("it ran")
	}
}

// Turning on a destination of a source nobody subscribed to makes the subscription with the extension's recipe.
func TestNewSourceTurnsOn(t *testing.T) {
	f := newFake()
	v := draw(t, f, env(i18n.En, true), state{P: pageSource, C: "apps", S: "calendar"})
	if c := item(v, "d.to_inbox").Control; c == nil || c.Disabled || c.Action.Confirm == "" {
		t.Fatalf("with the extension's own recipe the switches work and ask first: %+v", c)
	}
	res := handle(t, f, env(i18n.En, true), state{P: pageSource, C: "apps", S: "calendar"}, "dest", map[string]string{"to": "to_inbox", "to_inbox": "true"})
	if len(f.created) != 1 || f.created[0].Recipe != nil || f.created[0].Source != "calendar" {
		t.Fatalf("created: %+v", f.created)
	}
	if !strings.Contains(res.View.State, `"id":"sub_new"`) || !f.subs["sub_new"].ToInbox {
		t.Fatalf("the page did not become the new subscription's: %s %+v", res.View.State, f.subs["sub_new"])
	}
	v = draw(t, f, env(i18n.En, true), state{P: pageSource, C: "apps", S: "gmail"})
	if c := item(v, "d.to_inbox").Control; c == nil || !c.Disabled {
		t.Error("without a recipe the switches can't be turned on yet")
	}
	if item(v, "native") == nil {
		t.Error("no hint that Persona already reads this mail")
	}
}

// Picking an interval applies at once and the page stays with the choice moved; a snapshot offers times of day.
func TestIntervalPicker(t *testing.T) {
	f := newFake()
	v := draw(t, f, env(i18n.En, true), state{P: pageInterval, ID: "sub_outlook"})
	if item(v, "i.300") != nil {
		t.Error("an interval below the source's minimum is offered")
	}
	if b := item(v, "i.900").Badge; b == nil {
		t.Error("the current interval isn't marked")
	}
	res := handle(t, f, env(i18n.En, true), state{P: pageInterval, ID: "sub_outlook"}, "interval", map[string]string{"seconds": "1800"})
	p := f.patches["sub_outlook"][0]
	if *p.IntervalSeconds != 1800 || p.DailyAt == nil || *p.DailyAt != "" {
		t.Fatalf("patch: %+v", p)
	}
	if res.Pop || item(*res.View, "i.1800").Badge == nil || item(*res.View, "i.900").Badge != nil {
		t.Error("the choice did not move on the same page")
	}
	v = draw(t, f, env(i18n.En, true), state{P: pageInterval, ID: "sub_broker"})
	if item(v, "i.at.08:00").Badge == nil || item(v, "i.1800") != nil {
		t.Error("a snapshot: times of day, nothing under an hour")
	}
}

// Setting up a tool: the arguments come from its schema in order; the first dry run leaves items_path empty and the
// page keeps what the service found; the field pickers start on good guesses; saving makes the subscription.
func TestSetup(t *testing.T) {
	f := newFake()
	st := state{P: pageSetup, C: "apps", S: "calendar", T: "list_events"}
	v := draw(t, f, env(i18n.En, true), st)
	args := keys(section(v, "args"))
	if strings.Join(args, ",") != "a.calendar,a.updated_min,a.max_results,a.show_deleted" {
		t.Fatalf("arguments in the schema's order: %v", args)
	}
	if c := item(v, "a.calendar"); c.Kind != "choice" || len(c.Choices) != 2 || c.Value != "primary" {
		t.Errorf("a required enum is a choice without an empty one: %+v", c)
	}
	if c := item(v, "a.updated_min"); c.Value != "{{since}}" || c.Detail == "" {
		t.Errorf("a date-time argument starts at {{since}}: %+v", c)
	}
	if c := item(v, "a.max_results"); c.Kind != "number" || c.Value != "25" {
		t.Errorf("an integer with its default: %+v", c)
	}
	if section(v, "map") != nil {
		t.Error("fields to map before any dry run")
	}
	f.dry = subscriptions.DryRun{OK: true, ItemsPath: "$.items", Count: 2, Shape: &subscriptions.Shape{Type: "json", Arrays: []string{"items"}},
		Items: []subscriptions.Preview{{UID: "e1", New: true, Title: "Lab meeting"}},
		Paths: []subscriptions.PathValue{{Path: "$.id", Example: "e1"}, {Path: "$.summary", Example: "Lab meeting"},
			{Path: "$.start.dateTime", Example: "2026-11-12T15:00:00Z"}, {Path: "$.htmlLink", Example: "https://calendar.example.com/e1"}},
		Checks: []subscriptions.Check{{Key: "read_only", OK: true}}}
	form := map[string]string{"arg.calendar": "work", "arg.updated_min": "{{since}}", "arg.max_results": "10", "arg.show_deleted": "false"}
	res := handle(t, f, env(i18n.En, true), st, "dry_run", form)
	if r := f.dryReq.Recipe; r == nil || r.ItemsPath != "" || string(r.Args) != `{"calendar":"work","updated_min":"{{since}}","max_results":10,"show_deleted":false}` {
		t.Fatalf("dry run asked: %+v %s", f.dryReq, f.dryReq.Recipe.Args)
	}
	v = *res.View
	if !strings.Contains(v.State, `"i":"$.items"`) {
		t.Errorf("the found items_path isn't kept: %s", v.State)
	}
	if c := item(v, "a.calendar"); c.Value != "work" {
		t.Error("the typed arguments are lost")
	}
	for k, want := range map[string]string{"m.title": "$.summary", "m.time": "$.start.dateTime", "m.link": "$.htmlLink", "m.id": "$.id", "m.sender": ""} {
		if c := item(v, k); c == nil || c.Value != want {
			t.Errorf("%s preselected %+v, want %q", k, c, want)
		}
	}
	form["f.title"], form["f.time"], form["f.id"], form["kind"], form["group_by"] = "$.summary", "$.start.dateTime", "$.id", "sub_event", "source"
	res = handle(t, f, env(i18n.En, true), state{P: pageSetup, C: "apps", S: "calendar", T: "list_events", I: "$.items"}, "save", form)
	if len(f.created) != 1 {
		t.Fatal("nothing created")
	}
	r := f.created[0].Recipe
	if r.ItemsPath != "$.items" || r.Kind != "sub_event" || r.Fields.Title != "$.summary" || r.Fields.ID != "$.id" || r.Tool != "list_events" {
		t.Fatalf("saved recipe: %+v %+v", r, r.Fields)
	}
	if res.Toast == "" || !strings.Contains(res.View.State, `"p":"source"`) {
		t.Error("after saving, the new subscription's page")
	}
}

// Runs: newest first, failures marked, nothing but counts.
func TestRuns(t *testing.T) {
	v := draw(t, newFake(), env(i18n.En, true), state{P: pageRuns, ID: "sub_github"})
	if b := item(v, "r.0").Badge; b == nil || b.Tone != "danger" {
		t.Error("a failed run isn't marked")
	}
	if b := item(v, "r.2").Badge; b == nil || b.Text != "+3" {
		t.Error("new items aren't counted")
	}
	if st := item(v, "stats"); st.Stats[2].Value != "1" || st.Stats[1].Value != "3" {
		t.Errorf("stats: %+v", st.Stats)
	}
}

// A change made elsewhere meanwhile: the page drawn again, saying so.
func TestConflict(t *testing.T) {
	f := newFake()
	f.updateErr = &subscriptions.Error{Status: http.StatusConflict, Code: subscriptions.CodeConflict}
	res := handle(t, f, env(i18n.En, true), state{P: pageFallback, ID: "sub_outlook"}, "fallback", map[string]string{"tier": "action"})
	if res.View == nil || !strings.Contains(res.Toast, "changed elsewhere") {
		t.Fatalf("conflict: %+v", res)
	}
}

// Deleting the stored content while the assistant is away (#2039): the subscription is off, nothing is deleted yet,
// and the toast says to try again.
func TestDeleteWhileTheAssistantIsAway(t *testing.T) {
	f := newFake()
	f.deleteErr = &subscriptions.Error{Status: http.StatusServiceUnavailable, Code: "assistant_offline"}
	res := handle(t, f, env(i18n.En, true), state{P: pageSource, ID: "sub_outlook"}, "delete", nil)
	if res.Pop || res.View == nil || !strings.Contains(res.Toast, "assistant is away") {
		t.Fatalf("a delete the assistant could not finish: %+v", res)
	}
}

// Every page, in every language, for a full and a read-only device: it fits what the apps draw (UI version 6, and 4
// for iOS), and no key or placeholder shows through.
func TestEveryPageFits(t *testing.T) {
	slot, _ := extension.Slot("extension_page")
	pages := []state{{}, {Ext: "apps"}, {P: pageSource, ID: "sub_outlook"}, {P: pageSource, ID: "sub_github"}, {P: pageSource, ID: "sub_slack"},
		{P: pageSource, ID: "sub_broker"}, {P: pageSource, C: "apps", S: "calendar"}, {P: pageSource, C: "apps", S: "gmail"},
		{P: pageRecipe, ID: "sub_outlook"}, {P: pageTools, C: "apps", S: "calendar"}, {P: pageSetup, C: "apps", S: "calendar", T: "list_events"},
		{P: pageRuns, ID: "sub_github"}, {P: pageRuns, ID: "sub_outlook"}, {P: pageInterval, ID: "sub_outlook"}, {P: pageInterval, ID: "sub_broker"},
		{P: pageFallback, ID: "sub_outlook"}, {P: pageRules, ID: "sub_outlook"}, {P: pageRaw, ID: "sub_outlook"}, {P: pageSource, ID: "gone"}}
	for _, lang := range i18n.Langs {
		for _, edit := range []bool{true, false} {
			for _, st := range pages {
				for _, version := range []int{extension.UIVersion, 4} {
					v := draw(t, newFake(), env(lang, edit), st)
					b, _ := json.Marshal(v)
					s := strings.ReplaceAll(strings.ReplaceAll(string(b), "{{since}}", ""), "{{since_date}}", "")
					if strings.Contains(s, "subscriptions-pages.") {
						t.Errorf("%s %v: a key shows through", lang, st)
					}
					for _, ph := range []string{"{count}", "{time}", "{source}", "{noun}", "{every}", "{problem}", "{when}", "{span}", "{name}", "{dest}", "{tool}"} {
						if strings.Contains(s, ph) {
							t.Errorf("%s %v: %s unfilled", lang, st, ph)
						}
					}
					if _, err := extension.Fit(&v, slot, version, lang); err != nil {
						t.Errorf("%s %v v%d: %v", lang, st, version, err)
					}
				}
			}
		}
	}
}
