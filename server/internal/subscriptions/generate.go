package subscriptions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Drafting a recipe with a model (#2040, docs/extensions/subscriptions.md "Drafting with a model"), for a source whose
// extension ships none or a subscription whose tool changed. Two structured calls on the purpose subscription.recipe:
// the model picks a tool and its arguments from the source's read-only tools, seeing no result; then, from one sample
// of that tool's answer cut to sampleItems entries a list, sampleString characters a string and sampleBytes in all,
// it maps the fields. The draft goes through the same check as a dry run. A draft that fails is asked for once more
// with the problem, then answered with why. The sample is never stored or logged.

// Model makes the drafting calls: it answers a JSON value that matches c.Schema, and asks once more itself when a
// reply does not. ErrNoModel: nothing can be called.
type Model interface {
	JSON(ctx context.Context, c ModelCall) (json.RawMessage, error)
}

// ModelCall is one structured call: Turns alternate, user first and last.
type ModelCall struct {
	System    string
	Turns     []Turn
	Schema    json.RawMessage
	MaxTokens int
	Label     string // what the call is for, in the log line
}

type Turn struct {
	Role    string `json:"role"` // user or assistant
	Content string `json:"content"`
}

// ErrNoModel is a Model's answer when no model is set up for drafting.
var ErrNoModel = errors.New("no model is set up for drafting recipes")

// What the model sees of a sample, and of the tools it picks from.
const (
	sampleItems  = 3
	sampleString = 120
	sampleBytes  = 8 << 10
	toolsMax     = 40
	toolDescMax  = 300
	toolSchema   = 1500 // bytes of a tool's compact input schema
	noteMax      = 400
	// generateWait bounds a whole drafting, under the hub's five minutes for the manager's answer.
	generateWait = 4 * time.Minute
)

// GenerateRequest drafts a recipe: for subscription ID, saved when it passes, or for Connector and Source not
// subscribed yet, answered and not saved. Revision, with ID, is the one the client saw (0: any).
type GenerateRequest struct {
	ID        string `json:"-"`
	Revision  int64  `json:"revision,omitempty"`
	Connector string `json:"connector,omitempty"`
	Source    string `json:"source,omitempty"`
}

// Generated is a draft that passed every check: the recipe (its items_path found when the model left it out), the
// model's note for the user in the user's language, the dry run that checked it, and the subscription as saved when
// one was regenerated.
type Generated struct {
	Recipe       Recipe        `json:"recipe"`
	Note         string        `json:"note"`
	DryRun       DryRun        `json:"dry_run"`
	Subscription *Subscription `json:"subscription,omitempty"`
}

// Generate drafts a recipe with the model. For a subscription it keeps the switches, the interval when the mode stays
// and the rules, and clears needs_recipe; for a source not subscribed yet nothing is written.
func (s *Service) Generate(ctx context.Context, in GenerateRequest, by Actor) (Generated, error) {
	var sub *Subscription
	if in.ID != "" {
		var err error
		if sub, err = s.o.Store.GetSubscription(ctx, in.ID); err != nil {
			return Generated{}, err
		}
		if sub == nil {
			return Generated{}, notFound("subscription")
		}
		if IsPush(*sub) {
			return Generated{}, pushOnly("recipe")
		}
		if in.Revision != 0 && in.Revision != sub.Revision {
			return Generated{}, conflictWith(sub)
		}
		in.Connector, in.Source = sub.Connector, sub.Source
	}
	if in.Connector == "" || in.Source == "" {
		return Generated{}, errorf(http.StatusBadRequest, CodeBadRequest, "drafting needs a subscription, or connector and source")
	}
	if s.o.Model == nil {
		return Generated{}, noModel()
	}
	ctx, cancel := context.WithTimeout(ctx, generateWait)
	defer cancel()
	c, ok := s.o.Connectors.Find(ctx, in.Connector)
	if !ok {
		return Generated{}, notFound("connector")
	}
	if !c.ready() {
		return Generated{}, errorf(http.StatusServiceUnavailable, CodeNotConnected, "The connector cannot be reached now.")
	}
	sources, err := s.catalog(ctx, c, time.Minute)
	if err != nil {
		p := catalogProblem(err)
		return Generated{}, errorf(statusOf(p.Code), p.Code, p.Message)
	}
	d := drafter{s: s, conn: c, sub: sub, lang: s.o.Language(ctx)}
	for _, src := range sources {
		if src.ID == in.Source {
			d.source = src
		}
	}
	if d.source.ID == "" {
		return Generated{}, notFound("source")
	}
	for _, t := range d.source.Tools {
		if refusal(t) == nil {
			d.tools = append(d.tools, t)
		}
	}
	if len(d.tools) == 0 {
		return Generated{}, errorf(http.StatusUnprocessableEntity, CodeNotReadOnly, "The source has no read-only tool to pull.")
	}
	out, t, err := d.draft(ctx)
	if err != nil {
		code := "error"
		if se := (*Error)(nil); errors.As(err, &se) {
			code = se.Code
		}
		s.o.Logf("subscription draft on %s/%s: %s after %d model call(s)", c.ID, d.source.ID, code, d.calls)
		return Generated{}, err
	}
	s.o.Logf("subscription draft on %s/%s: passed after %d model call(s)", c.ID, d.source.ID, d.calls)
	if sub == nil {
		return out, nil
	}
	saved, err := s.saveDraft(ctx, *sub, t, out.DryRun, by)
	if err != nil {
		var se *Error
		if errors.As(err, &se) {
			se.Draft = &out.Recipe
		}
		return Generated{}, err
	}
	out.Subscription = &saved
	return out, nil
}

// RecipeModel is what drafting runs on now (purpose subscription.recipe): the consent before a sample goes to the
// model names it.
func (s *Service) RecipeModel(ctx context.Context) ModelInfo {
	return s.ModelOf(ctx, assistantwire.PurposeSubscriptionRecipe)
}

// ModelOf is what a model purpose runs on now, such as kb (Information processing) or inbox.triage (Information
// screening), whose providers the destinations' consent names. It asks the assistant; no model is called.
func (s *Service) ModelOf(ctx context.Context, purpose string) ModelInfo {
	if s.o.Models == nil || s.o.Model == nil && purpose == assistantwire.PurposeSubscriptionRecipe {
		return ModelInfo{}
	}
	return s.o.Models(ctx, purpose)
}

func noModel() *Error {
	return errorf(http.StatusServiceUnavailable, CodeNoModel, "No model is set up for drafting recipes.")
}

func conflictWith(cur *Subscription) *Error {
	e := errorf(http.StatusConflict, CodeConflict, "the subscription was changed meanwhile")
	v := shown(*cur)
	e.Current = &v
	return e
}

// saveDraft puts a draft that passed on the subscription as it was read before drafting: a change made meanwhile is
// a conflict.
func (s *Service) saveDraft(ctx context.Context, read Subscription, t target, dr DryRun, by Actor) (Subscription, error) {
	cur, err := s.o.Store.GetSubscription(ctx, read.ID)
	if err != nil {
		return Subscription{}, err
	}
	if cur == nil {
		return Subscription{}, notFound("subscription")
	}
	if cur.Revision != read.Revision {
		return Subscription{}, conflictWith(cur)
	}
	now := s.now()
	next := withRecipe(*cur, t, dr, OriginModel, now)
	next.Revision, next.UpdatedAt = cur.Revision+1, now
	if on(next) {
		next.NextRunAt = &now
	}
	ok, err := s.o.Store.UpdateSubscription(ctx, next, cur.Revision)
	if err != nil {
		return Subscription{}, err
	}
	if !ok {
		c, err := s.o.Store.GetSubscription(ctx, read.ID)
		if err != nil || c == nil {
			return Subscription{}, notFound("subscription")
		}
		return Subscription{}, conflictWith(c)
	}
	s.o.Audit(ctx, next.Connector, "subscription_updated", by, map[string]any{"subscription": next.ID, "fields": []string{"recipe"}, "origin": OriginModel})
	s.o.Changed(shown(next), false)
	if on(next) {
		s.poke()
	}
	return shown(next), nil
}

// drafter is one drafting: the source, its read-only tools and the subscription being regenerated (nil for a new one).
type drafter struct {
	s       *Service
	conn    ConnectorInfo
	source  connector.CatalogSource
	tools   []connector.CatalogTool
	sub     *Subscription
	lang    string
	calls   int
	retried bool // the one more try for a draft that failed is spent
}

// conversation is one structured call's turns so far.
type conversation struct {
	system string
	turns  []Turn
	schema json.RawMessage
	tokens int
	label  string
}

// ask makes the conversation's next call and decodes the answer into out, keeping the answer as the model's turn.
func (d *drafter) ask(ctx context.Context, cv *conversation, out any) error {
	d.calls++
	raw, err := d.s.o.Model.JSON(ctx, ModelCall{System: cv.system, Turns: cv.turns, Schema: cv.schema, MaxTokens: cv.tokens, Label: cv.label})
	if errors.Is(err, ErrNoModel) {
		return noModel()
	}
	if err == nil {
		err = json.Unmarshal(raw, out)
	}
	if err != nil {
		var se *Error
		if errors.As(err, &se) {
			return se
		}
		d.s.o.Logf("subscription draft on %s/%s: the %s call failed", d.conn.ID, d.source.ID, cv.label)
		return errorf(http.StatusBadGateway, CodeModelFailed, "The model could not draft a recipe.")
	}
	cv.turns = append(cv.turns, Turn{Role: "assistant", Content: string(raw)})
	return nil
}

// again asks once more with p, when the one more try is not spent and p is the draft's own (not the connector's).
func (d *drafter) again(cv *conversation, p *Problem) bool {
	if d.retried || p == nil {
		return false
	}
	switch p.Code {
	case CodeNotConnected, CodeRateLimited, CodeDryRunFailed:
		return false
	}
	d.retried = true
	cv.turns = append(cv.turns, Turn{Role: "user", Content: prompts.RenderSubscriptionRetry(prompts.SubscriptionRetry{Code: p.Code, Message: p.Message})})
	return true
}

type pickAnswer struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type mapAnswer struct {
	Mode      string                        `json:"mode"`
	ItemsPath string                        `json:"items_path"`
	Fields    *connector.SubscriptionFields `json:"fields"`
	Kind      string                        `json:"kind"`
	GroupBy   string                        `json:"group_by"`
	Note      string                        `json:"note"`
}

// draft picks, samples, maps and checks: the draft that passed, or the error that says why none did.
func (d *drafter) draft(ctx context.Context) (Generated, target, error) {
	loc := d.s.o.Location()
	since := d.s.now().Add(-firstBack)
	if d.sub != nil && d.sub.SinceAt != nil {
		since = d.sub.SinceAt.Add(-overlap)
	}
	pick := conversation{system: prompts.RenderSubscriptionPick(), schema: d.pickSchema(), tokens: 1024, label: "subscription pick",
		turns: []Turn{{Role: "user", Content: prompts.RenderSubscriptionPickInput(d.pickInput())}}}
	var chosen pickAnswer
	var tool connector.CatalogTool
	var res result
	for {
		if err := d.ask(ctx, &pick, &chosen); err != nil {
			return Generated{}, target{}, err
		}
		var p *Problem
		tool, res, p = d.sample(ctx, chosen, since, loc)
		if p == nil {
			break
		}
		if !d.again(&pick, p) {
			e := errorf(statusOf(p.Code), p.Code, p.Message)
			e.Draft = &Recipe{Tool: chosen.Tool, Args: json.RawMessage(compact(chosen.Args))} // as far as it got
			return Generated{}, target{}, e
		}
	}
	args := compact(chosen.Args)
	in := prompts.SubscriptionMapInput{App: d.conn.ID, Source: d.source.Name, Tool: tool.Name, Description: clip(oneLine(tool.Description), toolDescMax),
		Args: args, Text: !res.isObj, Sample: sampleOf(res)}
	mapping := conversation{system: prompts.RenderSubscriptionMap(prompts.SubscriptionMap{Language: d.lang}), schema: mapSchema, tokens: 2048,
		label: "subscription map", turns: []Turn{{Role: "user", Content: prompts.RenderSubscriptionMapInput(in)}}}
	for {
		var m mapAnswer
		if err := d.ask(ctx, &mapping, &m); err != nil {
			return Generated{}, target{}, err
		}
		recipe := Recipe{Mode: m.Mode, Tool: chosen.Tool, Args: json.RawMessage(args), ItemsPath: m.ItemsPath, Fields: m.Fields, Kind: m.Kind, GroupBy: m.GroupBy}
		if recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
			recipe.ItemsPath = ""
		}
		if recipe.Fields != nil && *recipe.Fields == (connector.SubscriptionFields{}) {
			recipe.Fields = nil
		}
		interval, daily := -1, ""
		if d.sub != nil {
			interval, daily = scheduleFor(*d.sub, recipe.Mode)
		}
		dr, t := d.s.check(ctx, d.conn, d.source.ID, &recipe, d.sub, interval, daily)
		if dr.OK {
			return Generated{Recipe: t.recipe, Note: clip(oneLine(m.Note), noteMax), DryRun: dr}, t, nil
		}
		if !d.again(&mapping, problemOf(dr)) {
			e := refused(dr)
			e.Draft = &recipe
			return Generated{}, target{}, e
		}
	}
}

// problemOf is the first problem a dry run found.
func problemOf(dr DryRun) *Problem {
	if dr.Error != nil {
		return dr.Error
	}
	for _, ch := range dr.Checks {
		if ch.Error != nil {
			return ch.Error
		}
	}
	return &Problem{CodeDryRunFailed, "The dry run did not pass."}
}

// sample checks the picked tool and arguments, then calls it once for what came after since: the tool and its
// answer, or the problem with the pick.
func (d *drafter) sample(ctx context.Context, a pickAnswer, since time.Time, loc *time.Location) (connector.CatalogTool, result, *Problem) {
	var tool connector.CatalogTool
	for _, t := range d.tools {
		if t.Name == a.Tool {
			tool = t
		}
	}
	if tool.Name == "" {
		return tool, result{}, &Problem{CodeNotReadOnly, "The tool is not one of the source's read-only tools."}
	}
	args, err := fillArgs(a.Args, vars(since, loc))
	if err == nil {
		err = checkArgs(tool.InputSchema, args)
	}
	if err != nil {
		return tool, result{}, &Problem{CodeBadArgs, "The arguments do not fit the tool: " + firstLine(err.Error())}
	}
	raw, _ := json.Marshal(args)
	cctx, cancel := context.WithTimeout(ctx, callLimit)
	defer cancel()
	out, err := connector.CallCatalog(cctx, d.conn.Conn, connector.CatalogQuery{Source: d.source.ID, Tool: tool.Name, Args: raw})
	if err != nil {
		p, _, _ := callProblem(err)
		return tool, result{}, p
	}
	return tool, readResult(out), nil
}

func (d *drafter) pickInput() prompts.SubscriptionPickInput {
	in := prompts.SubscriptionPickInput{App: d.conn.ID, Source: d.source.Name, Service: d.source.Service}
	for i, t := range d.tools {
		if i >= toolsMax {
			break
		}
		in.Tools = append(in.Tools, prompts.SubscriptionTool{Name: t.Name, Title: clip(oneLine(t.Title), toolDescMax),
			Description: clip(oneLine(t.Description), toolDescMax), Schema: clipBytes(compact(t.InputSchema), toolSchema)})
	}
	return in
}

// pickSchema is the answer of the first call: one of the tools offered, and an object of arguments.
func (d *drafter) pickSchema() json.RawMessage {
	names := []string{}
	for i, t := range d.tools {
		if i < toolsMax {
			names = append(names, t.Name)
		}
	}
	return mustJSON(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"tool", "args"},
		"properties": map[string]any{"tool": map[string]any{"type": "string", "enum": names}, "args": map[string]any{"type": "object"}}})
}

// mapSchema is the answer of the second call: the rest of the recipe and the note.
var mapSchema = func() json.RawMessage {
	str := map[string]any{"type": "string"}
	fields := map[string]any{}
	for _, k := range []string{"id", "thread", "title", "sender", "time", "body", "link", "due_date"} {
		fields[k] = str
	}
	return mustJSON(map[string]any{"type": "object", "additionalProperties": false, "required": []string{"mode", "fields", "kind", "group_by", "note"},
		"properties": map[string]any{
			"mode":       map[string]any{"type": "string", "enum": wire.ConnectorSubscriptionModes},
			"items_path": str,
			"fields":     map[string]any{"type": "object", "additionalProperties": false, "properties": fields},
			"kind":       map[string]any{"type": "string", "enum": wire.ConnectorSubscriptionKinds},
			"group_by":   map[string]any{"type": "string", "enum": wire.ConnectorSubscriptionGroups},
			"note":       map[string]any{"type": "string", "maxLength": noteMax},
		}})
}()

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// compact is raw on one line, "{}" when it is empty.
func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if len(bytes.TrimSpace(raw)) == 0 || json.Compact(&b, raw) != nil {
		return "{}"
	}
	return b.String()
}

// clipBytes is s cut to at most n bytes, on a character boundary.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// cutLimits are how much of a JSON answer a sample keeps: entries a list, characters a string, keys an object
// (0: all), levels deep (0: all).
type cutLimits struct{ items, chars, keys, depth int }

// sampleOf is an answer as the model sees it: a text answer's start, or a JSON answer cut to sampleItems entries a
// list and sampleString characters a string, cut further until it is at most sampleBytes.
func sampleOf(r result) string {
	if !r.isObj {
		return clip(r.text, sampleString)
	}
	for _, l := range []cutLimits{{sampleItems, sampleString, 0, 0}, {2, sampleString, 0, 0}, {1, sampleString, 0, 0},
		{1, 60, 30, 0}, {1, 40, 15, 6}, {1, 20, 8, 4}} {
		if b := encodeSample(cutValue(r.json, l, 0)); len(b) <= sampleBytes {
			return b
		}
	}
	return clipBytes(encodeSample(cutValue(r.json, cutLimits{1, 20, 8, 4}, 0)), sampleBytes)
}

func cutValue(v any, l cutLimits, depth int) any {
	switch x := v.(type) {
	case string:
		return clip(x, l.chars)
	case []any:
		if l.depth > 0 && depth >= l.depth {
			return []any{}
		}
		out := []any{}
		for i, e := range x {
			if i >= l.items {
				break
			}
			out = append(out, cutValue(e, l, depth+1))
		}
		return out
	case map[string]any:
		if l.depth > 0 && depth >= l.depth {
			return map[string]any{}
		}
		out := map[string]any{}
		keys := sortedKeys(x)
		for i, k := range keys {
			if l.keys > 0 && i >= l.keys {
				break
			}
			out[clip(k, l.chars)] = cutValue(x[k], l, depth+1)
		}
		return out
	}
	return v
}

// encodeSample writes a cut value as JSON on one line, keys sorted, <, > and & as they are.
func encodeSample(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return string(bytes.TrimRight(b.Bytes(), "\n"))
}
