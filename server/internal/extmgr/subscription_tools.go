package extmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/subscriptions"
	"github.com/nwverse/persona/server/internal/subscriptionsui"
)

// The assistant's subscription tools (#5240, docs/extensions/subscriptions.md "Assistant tools"): read the
// subscriptions and what can be subscribed to, dry-run a recipe, and create, change, delete or run one. They call the
// same subscriptions.Service as /api/v1/subscriptions and the pages. Every save is a card on the phone that says what
// changes, applied only on the user's own approval: the assistant's Persona tools are allowed without Claude Code's own
// prompt, so the card is the only consent. Answers are JSON for the model; source content in them (dry runs, samples)
// goes only to the conversation that asked, as on the pages.

var subscriptionToolNames = []string{"subscription_list", "subscription_get", "subscription_sources", "subscription_dry_run",
	"subscription_save", "subscription_run"}

// subscriptionTools are the assistant's while subscriptions run.
func (h *Manager) subscriptionTools() []proto.Tool {
	if h.subs == nil {
		return nil
	}
	out := make([]proto.Tool, 0, len(subscriptionToolNames))
	for _, n := range subscriptionToolNames {
		out = append(out, mcpTool(n))
	}
	return out
}

func isSubscriptionTool(name string) bool { return slices.Contains(subscriptionToolNames, name) }

// subscriptionActor is who the audit names for a change the user approved on the assistant's card.
var subscriptionActor = subscriptions.Actor{ID: "assistant", Name: "Persona"}

// Waits of subscription_run: how long it waits for the run by default and at most, and how often it looks.
var (
	subscriptionRunWait    = 60 * time.Second
	subscriptionRunWaitMax = 120 * time.Second
	subscriptionRunPoll    = 2 * time.Second
)

// callSubscriptionTool runs one for the conversation by.
func (h *Manager) callSubscriptionTool(ctx context.Context, by asker, name string, args json.RawMessage) proto.ToolResult {
	if h.subs == nil {
		return proto.TextResult("subscriptions are not running yet; try again in a moment", true)
	}
	var out any
	var err error
	switch name {
	case "subscription_list":
		out, err = h.toolSubscriptionList(ctx, args)
	case "subscription_get":
		out, err = h.toolSubscriptionGet(ctx, args)
	case "subscription_sources":
		out, err = h.toolSubscriptionSources(ctx, args)
	case "subscription_dry_run":
		out, err = h.toolSubscriptionDryRun(ctx, args)
	case "subscription_save":
		out, err = h.toolSubscriptionSave(ctx, by, args)
	case "subscription_run":
		out, err = h.toolSubscriptionRun(ctx, args)
	default:
		return proto.TextResult("unknown tool "+name, true)
	}
	if err != nil {
		return subscriptionToolErr(err)
	}
	if s, ok := out.(string); ok {
		return proto.TextResult(s, false)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return proto.TextResult(err.Error(), true)
	}
	return proto.TextResult(string(b), false)
}

// subscriptionToolErr is the service's error as the model reads it: the code and sentence, with what the API adds
// (the dry run that refused a recipe, the subscription as it is now on a conflict).
func subscriptionToolErr(err error) proto.ToolResult {
	var e *subscriptions.Error
	if !errors.As(err, &e) {
		return proto.TextResult(err.Error(), true)
	}
	body := map[string]any{"error": e.Message, "code": e.Code}
	if e.DryRun != nil {
		body["dry_run"] = e.DryRun
	}
	if e.Current != nil {
		body["subscription"] = subscriptionLine(*e.Current)
		body["hint"] = "it was changed meanwhile: read it again and use its revision"
	}
	if e.RetryAfter > 0 {
		body["retry_after"] = e.RetryAfter
	}
	b, _ := json.Marshal(body)
	return proto.TextResult(string(b), true)
}

// badArgs is a call the model can fix.
func badArgs(format string, a ...any) error {
	return &subscriptions.Error{Code: subscriptions.CodeBadRequest, Message: fmt.Sprintf(format, a...)}
}

// subscriptionEntry is one subscription in a list: what it is, how it is doing.
type subscriptionEntry struct {
	ID              string                 `json:"id"`
	Label           string                 `json:"label"`
	Connector       string                 `json:"connector"`
	Source          string                 `json:"source"`
	SourceName      string                 `json:"source_name,omitempty"`
	Tool            string                 `json:"tool,omitempty"`
	Mode            string                 `json:"mode,omitempty"`
	Kind            string                 `json:"kind,omitempty"`
	Push            bool                   `json:"push,omitempty"`
	IntervalSeconds int                    `json:"interval_seconds,omitempty"`
	DailyAt         string                 `json:"daily_at,omitempty"`
	ToInbox         bool                   `json:"to_inbox"`
	ToKB            bool                   `json:"to_kb"`
	State           string                 `json:"state"`
	Error           *subscriptions.Problem `json:"error,omitempty"`
	LastRunAt       *time.Time             `json:"last_run_at,omitempty"`
	LastOKAt        *time.Time             `json:"last_ok_at,omitempty"`
	NextRunAt       *time.Time             `json:"next_run_at,omitempty"`
	LastNewCount    int                    `json:"last_new_count"`
	Revision        int64                  `json:"revision"`
}

func subscriptionLine(s subscriptions.Subscription) subscriptionEntry {
	e := subscriptionEntry{ID: s.ID, Label: s.Label, Connector: s.Connector, Source: s.Source, SourceName: s.SourceName,
		ToInbox: s.ToInbox, ToKB: s.ToKB, State: s.State, Error: s.Error, LastRunAt: s.LastRunAt, LastOKAt: s.LastOKAt,
		NextRunAt: s.NextRunAt, LastNewCount: s.LastNewCount, Revision: s.Revision}
	if subscriptions.IsPush(s) {
		e.Push = true
		return e
	}
	e.Tool, e.Mode, e.Kind, e.IntervalSeconds, e.DailyAt = s.Recipe.Tool, s.Recipe.Mode, s.Recipe.Kind, s.IntervalSeconds, s.DailyAt
	return e
}

func (h *Manager) toolSubscriptionList(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Connector string `json:"connector"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, badArgs("bad arguments: %v", err)
	}
	list, _, err := h.subs.List(ctx, subscriptions.Filter{Connector: a.Connector})
	if err != nil {
		return nil, err
	}
	out := make([]subscriptionEntry, 0, len(list))
	for _, s := range list {
		out = append(out, subscriptionLine(s))
	}
	return map[string]any{"subscriptions": out, "time_zone": h.userLocation().String()}, nil
}

// heldSample is a held item as subscription_get shows it, its body cut.
type heldSample struct {
	UID     string     `json:"uid"`
	Stored  time.Time  `json:"stored"`
	Title   string     `json:"title"`
	Sender  string     `json:"sender,omitempty"`
	Time    *time.Time `json:"time,omitempty"`
	Thread  string     `json:"thread,omitempty"`
	Link    string     `json:"link,omitempty"`
	DueDate *time.Time `json:"due_date,omitempty"`
	Body    string     `json:"body,omitempty"`
}

const sampleBodyMost = 300

func sampleOf(h subscriptions.Held) heldSample {
	body := h.Body
	if utf8.RuneCountInString(body) > sampleBodyMost {
		body = string([]rune(body)[:sampleBodyMost]) + "…"
	}
	return heldSample{UID: h.UID, Stored: h.Stored, Title: h.Title, Sender: h.Sender, Time: h.Time, Thread: h.Thread, Link: h.Link,
		DueDate: h.DueDate, Body: body}
}

func (h *Manager) toolSubscriptionGet(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		ID    string `json:"id"`
		Runs  *int   `json:"runs"`
		Items *int   `json:"items"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
		return nil, badArgs("give id (subscription_list)")
	}
	s, runs, err := h.subs.Get(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	nRuns, nItems := clampCount(a.Runs, 5, subscriptions.RunsKept), clampCount(a.Items, 5, 20)
	if len(runs) > nRuns {
		runs = runs[:nRuns]
	}
	held, err := h.subs.Recent(ctx, a.ID, nItems)
	if err != nil {
		return nil, err
	}
	items := make([]heldSample, 0, len(held))
	for _, x := range held {
		items = append(items, sampleOf(x))
	}
	return map[string]any{"subscription": s, "runs": runs, "items": items, "time_zone": h.userLocation().String()}, nil
}

func clampCount(p *int, def, most int) int {
	if p == nil {
		return def
	}
	return min(max(*p, 0), most)
}

// recipeFormat is what a recipe may say, from the code: subscription_sources hands it with every answer.
func recipeFormat() map[string]any {
	return map[string]any{
		"keys":   subscriptions.RecipeKeys,
		"fields": subscriptions.FieldKeys,
		"field_meaning": map[string]string{
			"id":       "the item's stable id: dedup key (without one, items are told apart by title, time and sender)",
			"thread":   "items with the same thread share one Inbox row and one knowledge-base chat (group_by thread)",
			"title":    "required: what the Inbox row says",
			"sender":   "who it is from",
			"time":     "when it happened: RFC 3339, a date and time (user's zone), a date, or Unix seconds / milliseconds",
			"body":     "the text (kept up to 8000 characters)",
			"link":     "where to open it (https links become the row's link)",
			"due_date": "a deadline: a row with one is Needs you without a triage model",
		},
		"paths":         "relative to one item: $, .key (letters, digits, _ and -) and [index]; no wildcards, filters or expressions",
		"items_path":    "where the list of items is in the result ($ by default); leave it empty to have the dry run find the result's one top-level list",
		"template_vars": map[string]string{"{{since}}": "RFC 3339 time: an hour before the newest item already pulled, a day back on the first run", "{{since_date}}": "the same as a date, YYYY-MM-DD in the user's zone"},
		"modes": map[string]string{
			wire.ConnectorSubscriptionModeItems:    "new items, each kept once (a JSON result with a list)",
			wire.ConnectorSubscriptionModeSnapshot: "the result as one state kept when it changes (any result; text can only be a snapshot); at most hourly, daily by default",
		},
		"kinds":    wire.ConnectorSubscriptionKinds,
		"group_by": wire.ConnectorSubscriptionGroups,
		"intervals": "interval_seconds is at least 300 and the source's min_interval_seconds; a snapshot at least 3600, by default daily at daily_at (HH:MM). " +
			"items default to every 900 seconds",
	}
}

func (h *Manager) toolSubscriptionSources(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		Connector string `json:"connector"`
		Source    string `json:"source"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, badArgs("bad arguments: %v", err)
	}
	if a.Connector != "" { // read now, with every tool's input schema
		c, err := h.subs.Catalog(ctx, a.Connector)
		if err != nil {
			return nil, err
		}
		if a.Source != "" {
			c.Sources = slices.DeleteFunc(c.Sources, func(s subscriptions.CatalogSource) bool { return s.ID != a.Source })
			if len(c.Sources) == 0 && c.Error == nil {
				return nil, badArgs("connector %s has no source %q", a.Connector, a.Source)
			}
		}
		return map[string]any{"catalog": c, "name": h.subscriptionConnectorName(c.Connector), "recipe_format": recipeFormat()}, nil
	}
	// Every connector, from what was read before (never a call): the tools' names, for picking one to read in full.
	type tool struct {
		Name    string `json:"name"`
		Title   string `json:"title,omitempty"`
		Refused string `json:"refused,omitempty"`
	}
	type source struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Service       string   `json:"service,omitempty"`
		Tools         []tool   `json:"tools"`
		Recipes       int      `json:"own_recipes"`
		Subscriptions []string `json:"subscriptions"`
	}
	type connectorLine struct {
		Connector string                 `json:"connector"`
		Name      string                 `json:"name"`
		Status    string                 `json:"status,omitempty"`
		Error     *subscriptions.Problem `json:"error,omitempty"`
		Sources   []source               `json:"sources"`
	}
	cats, err := h.subs.Catalogs(ctx)
	if err != nil {
		return nil, err
	}
	read := map[string]bool{}
	out := []connectorLine{}
	for _, c := range cats {
		read[c.Connector] = true
		line := connectorLine{Connector: c.Connector, Name: h.subscriptionConnectorName(c.Connector), Status: c.Status, Error: c.Error, Sources: []source{}}
		for _, s := range c.Sources {
			src := source{ID: s.ID, Name: s.Name, Service: s.Service, Tools: []tool{}, Recipes: len(s.Recipes), Subscriptions: s.Subscriptions}
			for _, t := range s.Tools {
				x := tool{Name: t.Name, Title: t.Title}
				if t.Refused != nil {
					x.Refused = t.Refused.Code
				}
				src.Tools = append(src.Tools, x)
			}
			line.Sources = append(line.Sources, src)
		}
		out = append(out, line)
	}
	var notRead []string
	if lines, err := h.subs.Summary(ctx); err == nil {
		for _, l := range lines {
			if !read[l.Connector] {
				notRead = append(notRead, l.Connector)
			}
		}
	}
	res := map[string]any{"connectors": out, "recipe_format": recipeFormat(),
		"hint": "call again with connector (and source) for each tool's description and input_schema, read now"}
	if len(notRead) > 0 {
		res["not_read_yet"] = notRead // being read in the background, or not connected
	}
	return res, nil
}

// recipeArg is the recipe a call gives: for a subscription, merged onto its own (RFC 7386: keys given replace, null
// removes, objects merge), else taken whole. unknown are its keys the format does not know.
func recipeArg(raw json.RawMessage, base *subscriptions.Recipe) (recipe *subscriptions.Recipe, unknown []string, err error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil, nil
	}
	unknown = subscriptions.UnknownKeys(raw)
	merged := raw
	if base != nil {
		b, err := json.Marshal(base)
		if err != nil {
			return nil, nil, err
		}
		if merged, err = mergePatch(b, raw); err != nil {
			return nil, nil, badArgs("recipe is not a JSON object: %v", err)
		}
	}
	var r subscriptions.Recipe
	if err := json.Unmarshal(merged, &r); err != nil {
		return nil, nil, badArgs("recipe: %v", err)
	}
	return &r, unknown, nil
}

// mergePatch applies patch to doc as RFC 7386 does.
func mergePatch(doc, patch json.RawMessage) (json.RawMessage, error) {
	var p any
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, err
	}
	pm, ok := p.(map[string]any)
	if !ok {
		return nil, errors.New("a patch is an object")
	}
	var d any
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	return json.Marshal(mergeValue(d, pm))
}

func mergeValue(doc any, patch map[string]any) any {
	dm, ok := doc.(map[string]any)
	if !ok {
		dm = map[string]any{}
	}
	for k, v := range patch {
		switch x := v.(type) {
		case nil:
			delete(dm, k)
		case map[string]any:
			dm[k] = mergeValue(dm[k], x)
		default:
			dm[k] = x
		}
	}
	return dm
}

// tried is a dry run's answer with the recipe it tried (its items_path as found), to save as it is.
func tried(dr subscriptions.DryRun, r *subscriptions.Recipe) map[string]any {
	out := map[string]any{"dry_run": dr}
	if r != nil {
		rr := *r
		if rr.ItemsPath == "" && rr.Mode != wire.ConnectorSubscriptionModeSnapshot && dr.ItemsPath != "" {
			rr.ItemsPath = dr.ItemsPath
		}
		if rr.Args == nil {
			rr.Args = json.RawMessage(`{}`)
		}
		out["recipe"] = rr
	}
	return out
}

func (h *Manager) toolSubscriptionDryRun(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		ID        string          `json:"id"`
		Connector string          `json:"connector"`
		Source    string          `json:"source"`
		Recipe    json.RawMessage `json:"recipe"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, badArgs("bad arguments: %v", err)
	}
	in := subscriptions.DryRunRequest{ID: a.ID, Connector: a.Connector, Source: a.Source}
	var base *subscriptions.Recipe
	if a.ID != "" {
		in.Connector, in.Source = "", ""
		s, _, err := h.subs.Get(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		base = &s.Recipe
	}
	r, unknown, err := recipeArg(a.Recipe, base)
	if err != nil {
		return nil, err
	}
	in.Recipe, in.Unknown = r, unknown
	dr, err := h.subs.DryRun(ctx, in)
	if err != nil {
		return nil, err
	}
	if r == nil && base != nil {
		r = base
	}
	return tried(dr, r), nil
}

// saveArgs are subscription_save's.
type saveArgs struct {
	Action       string          `json:"action"`
	ID           string          `json:"id"`
	Revision     int64           `json:"revision"`
	Connector    string          `json:"connector"`
	Source       string          `json:"source"`
	Label        *string         `json:"label"`
	Recipe       json.RawMessage `json:"recipe"`
	Existing     string          `json:"existing"`
	Interval     *int            `json:"interval_seconds"`
	DailyAt      *string         `json:"daily_at"`
	TriageRules  *string         `json:"triage_rules"`
	FallbackTier *string         `json:"fallback_tier"`
	ToInbox      *bool           `json:"to_inbox"`
	ToKB         *bool           `json:"to_kb"`
	Pause        bool            `json:"pause"`
	DeleteData   bool            `json:"delete_data"`
}

// patch is what an update or a new subscription's settings change, from a: pause turns both destinations off.
func (a saveArgs) patch(revision int64, recipe *subscriptions.Recipe) (subscriptions.Patch, error) {
	p := subscriptions.Patch{Revision: revision, Label: a.Label, Recipe: recipe, Existing: a.Existing, IntervalSeconds: a.Interval,
		DailyAt: a.DailyAt, TriageRules: a.TriageRules, FallbackTier: a.FallbackTier, ToInbox: a.ToInbox, ToKB: a.ToKB}
	if a.Pause {
		if a.ToInbox != nil && *a.ToInbox || a.ToKB != nil && *a.ToKB {
			return p, badArgs("pause turns both destinations off: leave out to_inbox and to_kb")
		}
		off := false
		p.ToInbox, p.ToKB = &off, &off
	}
	if a.Existing != "" && !slices.Contains([]string{subscriptions.ExistingDigest, subscriptions.ExistingBaseline, subscriptions.ExistingDeliver}, a.Existing) {
		return p, badArgs("existing is digest, baseline or deliver")
	}
	if a.FallbackTier != nil && !slices.Contains([]string{"", "fyi", "action", "wait"}, *a.FallbackTier) {
		return p, badArgs(`fallback_tier is "", fyi, action or wait`)
	}
	return p, nil
}

// empty: p changes nothing on cur.
func emptyPatch(cur subscriptions.Subscription, p subscriptions.Patch) bool {
	next := subscriptions.Patched(cur, p)
	return p.Recipe == nil && next.Label == cur.Label && next.TriageRules == cur.TriageRules && next.FallbackTier == cur.FallbackTier &&
		next.DailyAt == cur.DailyAt && next.IntervalSeconds == cur.IntervalSeconds && next.ToInbox == cur.ToInbox && next.ToKB == cur.ToKB
}

// checkSchedule refuses before asking what Update would refuse after: an interval below the floor, a daily time
// that is not HH:MM or not for a snapshot.
func checkSchedule(next subscriptions.Subscription, p subscriptions.Patch) error {
	if p.DailyAt != nil && *p.DailyAt != "" {
		if next.Recipe.Mode != wire.ConnectorSubscriptionModeSnapshot {
			return badArgs("daily_at is for snapshots only")
		}
		if t, err := time.Parse("15:04", *p.DailyAt); err != nil || t.Format("15:04") != *p.DailyAt {
			return badArgs("daily_at is HH:MM")
		}
	}
	if p.IntervalSeconds != nil && next.DailyAt == "" && *p.IntervalSeconds < next.MinIntervalSeconds {
		return &subscriptions.Error{Code: subscriptions.CodeIntervalTooShort,
			Message: fmt.Sprintf("the interval must be at least %d seconds", next.MinIntervalSeconds)}
	}
	return nil
}

func (h *Manager) toolSubscriptionSave(ctx context.Context, by asker, args json.RawMessage) (any, error) {
	var a saveArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, badArgs("bad arguments: %v", err)
	}
	switch a.Action {
	case "create":
		return h.saveCreate(ctx, by, a)
	case "update", "delete":
		if a.ID == "" || a.Revision == 0 {
			return nil, badArgs("%s needs id and the revision subscription_get or subscription_list showed", a.Action)
		}
		cur, _, err := h.subs.Get(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if cur.Revision != a.Revision {
			c := cur
			return nil, &subscriptions.Error{Code: subscriptions.CodeConflict, Message: "the subscription was changed meanwhile", Current: &c}
		}
		if a.Action == "delete" {
			return h.saveDelete(ctx, by, a, cur)
		}
		return h.saveUpdate(ctx, by, a, cur)
	}
	return nil, badArgs("action is create, update or delete")
}

// refusedRecipe is the answer for a recipe that is not saved: what its dry run found.
func refusedRecipe(dr subscriptions.DryRun, r *subscriptions.Recipe, why string) map[string]any {
	out := tried(dr, r)
	out["saved"], out["why"] = false, why
	return out
}

// unknownRefused: a recipe with keys the format does not know is not saved (they would be dropped without a word).
func unknownRefused(unknown []string) bool { return len(unknown) > 0 }

func (h *Manager) saveCreate(ctx context.Context, by asker, a saveArgs) (any, error) {
	if a.Connector == "" || a.Source == "" {
		return nil, badArgs("create needs connector and source (subscription_sources)")
	}
	if a.Existing != "" {
		return nil, badArgs("existing is for changing a recipe")
	}
	r, unknown, err := recipeArg(a.Recipe, nil)
	if err != nil {
		return nil, err
	}
	dr, err := h.subs.DryRun(ctx, subscriptions.DryRunRequest{Connector: a.Connector, Source: a.Source, Recipe: r, Unknown: unknown})
	if err != nil {
		return nil, err
	}
	if unknownRefused(unknown) {
		return refusedRecipe(dr, r, "the recipe has keys the format does not know (warnings): fix or remove them"), nil
	}
	if !dr.OK {
		return refusedRecipe(dr, r, "the dry run did not pass"), nil
	}
	src := h.cachedSource(ctx, a.Connector, a.Source)
	if src == nil {
		return nil, &subscriptions.Error{Code: subscriptions.CodeNotFound, Message: "the source is not in the connector's catalog"}
	}
	recipe := r
	if recipe == nil {
		if len(src.Recipes) == 0 {
			return nil, &subscriptions.Error{Code: subscriptions.CodeNeedsRecipe, Message: "the source has no recipe of its own: give one"}
		}
		recipe = &src.Recipes[0]
	}
	if recipe.ItemsPath == "" && recipe.Mode != wire.ConnectorSubscriptionModeSnapshot {
		rr := *recipe
		rr.ItemsPath = dr.ItemsPath
		recipe = &rr
	}
	srcMin := 0
	if src.MinIntervalSeconds != nil {
		srcMin = *src.MinIntervalSeconds
	}
	// The new one as Create makes it, then the settings given, as the card shows them.
	fresh := subscriptions.Subscription{Connector: a.Connector, Source: a.Source, SourceName: src.Name, Label: src.Name,
		Recipe: *recipe, IntervalSeconds: subscriptions.DefaultInterval(recipe.Mode, srcMin), SourceMin: srcMin}
	if recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
		fresh.DailyAt = "08:00"
	}
	p, err := a.patch(1, nil)
	if err != nil {
		return nil, err
	}
	after := subscriptions.Patched(fresh, p)
	if err := checkSchedule(after, p); err != nil {
		return nil, err
	}
	text := subscriptionsui.Card(ctx, h.subs, h.subscriptionCardEnv(), subscriptionsui.Change{Action: "create", After: after, Source: src,
		Connector: h.subscriptionConnectorName(a.Connector), DryRun: &dr})
	if res, ok := h.askSubscriptionCard(ctx, by, after.Label, "create", text); !ok {
		return res, nil
	}
	created, err := h.subs.Create(ctx, subscriptions.CreateRequest{Connector: a.Connector, Source: a.Source, Recipe: r, Label: deref(a.Label)},
		subscriptionActor)
	if err != nil {
		return nil, err
	}
	p.Label = nil // given to Create
	if !emptyPatch(created, p) {
		p.Revision = created.Revision
		if created, err = h.subs.Update(ctx, created.ID, p, subscriptionActor); err != nil {
			return map[string]any{"saved": true, "subscription": nil, "warning": "made with both destinations off; its other settings failed: " + err.Error()}, nil
		}
	}
	return map[string]any{"saved": true, "subscription": subscriptionLine(created), "warnings": dr.Warnings,
		"undo": "subscription_save with action delete (the user confirms again)"}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (h *Manager) saveUpdate(ctx context.Context, by asker, a saveArgs, cur subscriptions.Subscription) (any, error) {
	if subscriptions.IsPush(cur) && (len(bytes.TrimSpace(a.Recipe)) > 0 || a.Interval != nil || a.DailyAt != nil) {
		return nil, &subscriptions.Error{Code: "push_source", Message: "a push source has no recipe or schedule: its device sends its items"}
	}
	r, unknown, err := recipeArg(a.Recipe, &cur.Recipe)
	if err != nil {
		return nil, err
	}
	if unknownRefused(unknown) { // even when what is left is the same recipe: the change asked for does nothing
		d, err := h.subs.DryRun(ctx, subscriptions.DryRunRequest{ID: cur.ID, Recipe: r, Unknown: unknown})
		if err != nil {
			return nil, err
		}
		return refusedRecipe(d, r, "the recipe has keys the format does not know (warnings): fix or remove them"), nil
	}
	if r != nil && subscriptions.SameRecipe(*r, cur.Recipe) {
		r = nil // nothing to change there
	}
	if a.Existing != "" && r == nil {
		return nil, badArgs("existing goes with a recipe that changes")
	}
	p, err := a.patch(cur.Revision, r)
	if err != nil {
		return nil, err
	}
	if emptyPatch(cur, p) {
		return map[string]any{"saved": false, "why": "nothing would change", "subscription": subscriptionLine(cur)}, nil
	}
	var dr *subscriptions.DryRun
	if r != nil {
		d, err := h.subs.DryRun(ctx, subscriptions.DryRunRequest{ID: cur.ID, Recipe: r, Unknown: unknown})
		if err != nil {
			return nil, err
		}
		if !d.OK {
			return refusedRecipe(d, r, "the dry run did not pass"), nil
		}
		if r.ItemsPath == "" && r.Mode != wire.ConnectorSubscriptionModeSnapshot && d.ItemsPath != "" {
			rr := *r
			rr.ItemsPath = d.ItemsPath
			r, p.Recipe = &rr, &rr
		}
		dr = &d
	}
	after := subscriptions.Patched(cur, p)
	if err := checkSchedule(after, p); err != nil {
		return nil, err
	}
	text := subscriptionsui.Card(ctx, h.subs, h.subscriptionCardEnv(), subscriptionsui.Change{Action: "update", Before: &cur, After: after,
		Source: h.cachedSource(ctx, cur.Connector, cur.Source), Connector: h.subscriptionConnectorName(cur.Connector), DryRun: dr,
		Existing: cmpOr(p.Existing, subscriptions.ExistingDigest)})
	if res, ok := h.askSubscriptionCard(ctx, by, cur.Label, "update", text); !ok {
		return res, nil
	}
	saved, err := h.subs.Update(ctx, cur.ID, p, subscriptionActor)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"saved": true, "subscription": subscriptionLine(saved), "previous": previousOf(cur, p),
		"undo": "subscription_save update with previous and the new revision puts it back (the user confirms again)"}
	if dr != nil {
		out["warnings"], out["existing"] = dr.Warnings, cmpOr(p.Existing, subscriptions.ExistingDigest)
	}
	return out, nil
}

// previousOf are cur's values of what p changes: the arguments that put it back.
func previousOf(cur subscriptions.Subscription, p subscriptions.Patch) map[string]any {
	out := map[string]any{}
	if p.Label != nil {
		out["label"] = cur.Label
	}
	if p.Recipe != nil {
		out["recipe"] = cur.Recipe
	}
	if p.IntervalSeconds != nil || p.Recipe != nil && p.Recipe.Mode != cur.Recipe.Mode {
		out["interval_seconds"] = cur.IntervalSeconds
	}
	if p.DailyAt != nil || p.Recipe != nil && p.Recipe.Mode != cur.Recipe.Mode {
		out["daily_at"] = cur.DailyAt
	}
	if p.TriageRules != nil {
		out["triage_rules"] = cur.TriageRules
	}
	if p.FallbackTier != nil {
		out["fallback_tier"] = cur.FallbackTier
	}
	if p.ToInbox != nil {
		out["to_inbox"] = cur.ToInbox
	}
	if p.ToKB != nil {
		out["to_kb"] = cur.ToKB
	}
	return out
}

func (h *Manager) saveDelete(ctx context.Context, by asker, a saveArgs, cur subscriptions.Subscription) (any, error) {
	text := subscriptionsui.Card(ctx, h.subs, h.subscriptionCardEnv(), subscriptionsui.Change{Action: "delete", After: cur,
		Source: h.cachedSource(ctx, cur.Connector, cur.Source), Connector: h.subscriptionConnectorName(cur.Connector), DeleteData: a.DeleteData})
	if res, ok := h.askSubscriptionCard(ctx, by, cur.Label, "delete", text); !ok {
		return res, nil
	}
	if err := h.subs.Delete(ctx, cur.ID, a.DeleteData, subscriptionActor); err != nil {
		return nil, err
	}
	return map[string]any{"saved": true, "deleted": cur.ID, "deleted_data": a.DeleteData}, nil
}

// askSubscriptionCard asks the user to approve text on the phone, in the conversation by. ok false: not approved, and
// the answer says how it ended (only the user's own answer counts: an expired, closed or proxy-answered card is no
// decline).
func (h *Manager) askSubscriptionCard(ctx context.Context, by asker, label, action, text string) (any, bool) {
	shown, _ := json.Marshal(map[string]string{"subscription": label, "action": action})
	r, _ := h.approveCard(ctx, by, "subscription_save", text, shown)
	switch {
	case r.UserAllowed():
		return nil, true
	case r.UserDenied():
		h.logf("subscription %s: declined on the phone", action)
		return map[string]any{"saved": false, "why": "The user declined on the phone: nothing changed. Do not retry unless they ask."}, false
	}
	return map[string]any{"saved": false, "why": prompts.RenderSettingUnanswered()}, false
}

// subscriptionCardEnv is the card's language and zone: the user's.
func (h *Manager) subscriptionCardEnv() subscriptionsui.Env {
	return subscriptionsui.Env{Lang: h.userLang(), Loc: h.userLocation(), Now: time.Now(), Name: h.subscriptionConnectorName}
}

// cachedSource is a connector's source as last read (nil when not read or not there): names and tool titles for the
// card. A dry run just before has read it.
func (h *Manager) cachedSource(ctx context.Context, connector, source string) *subscriptions.CatalogSource {
	cats, err := h.subs.Catalogs(ctx)
	if err != nil {
		return nil
	}
	for _, c := range cats {
		if c.Connector != connector {
			continue
		}
		for i := range c.Sources {
			if c.Sources[i].ID == source {
				return &c.Sources[i]
			}
		}
	}
	return nil
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (h *Manager) toolSubscriptionRun(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		ID   string `json:"id"`
		Wait *int   `json:"wait_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.ID == "" {
		return nil, badArgs("give id (subscription_list)")
	}
	wait := subscriptionRunWait
	if a.Wait != nil {
		wait = min(max(time.Duration(*a.Wait)*time.Second, 0), subscriptionRunWaitMax)
	}
	asked := time.Now() // the run it starts begins after this (a run under way already is not it)
	if _, err := h.subs.RunNow(ctx, a.ID, subscriptionActor); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		s, runs, err := h.subs.Get(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if len(runs) > 0 && !runs[0].At.Before(asked) {
			run := runs[0]
			items := []heldSample{}
			if run.New > 0 {
				held, err := h.subs.Recent(ctx, a.ID, min(run.New, 5))
				if err == nil {
					for _, x := range held {
						if !x.Stored.Before(run.At) {
							items = append(items, sampleOf(x))
						}
					}
				}
			}
			return map[string]any{"ran": true, "run": run, "subscription": subscriptionLine(s), "new_items": items}, nil
		}
		if !time.Now().Before(deadline) {
			return map[string]any{"ran": false, "subscription": subscriptionLine(s),
				"why": "asked; it runs as soon as the connector's limits allow (state and next_run_at say when): read it later with subscription_get"}, nil
		}
		t := time.NewTimer(subscriptionRunPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
