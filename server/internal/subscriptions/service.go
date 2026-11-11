package subscriptions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Intervals, in seconds.
const (
	minInterval         = 300   // nothing more often than every 5 minutes
	minSnapshotInterval = 3600  // a snapshot at most hourly
	defaultInterval     = 900   // every 15 minutes, or the source's own minimum when longer
	defaultSnapshot     = 86400 // a snapshot once a day
	defaultDailyAt      = "08:00"
	catalogFresh        = 10 * time.Minute // a connector's catalog is read again after this
	catalogWait         = 30 * time.Second
)

// ConnectorInfo is a registered connector as subscriptions see it.
type ConnectorInfo struct {
	ID        string
	Extension string // the extension that brings it; "" for a built-in one
	Status    string // connector.StateOK when it can be called
	Enabled   bool
	Direct    Direct
	Conn      connector.Connector
}

func (c ConnectorInfo) ready() bool {
	return c.Conn != nil && c.Enabled && c.Status == connector.StateOK
}

// Connectors finds the registered connectors (the manager's registry).
type Connectors interface {
	Find(ctx context.Context, id string) (ConnectorInfo, bool)
	All(ctx context.Context) []ConnectorInfo
}

// Options are a Service's parts. Store and Connectors are required; the rest have defaults.
type Options struct {
	Store      Store
	Connectors Connectors
	// Location is the user's time zone: {{since_date}}, daily_at, times without a zone.
	Location func() *time.Location
	// Audit records a change (plugin_audit): on connector, action by whom, with detail that holds no content.
	Audit func(ctx context.Context, connectorID, action string, by Actor, detail map[string]any)
	// Changed tells the apps a subscription changed (removed: it was deleted).
	Changed func(s Subscription, removed bool)
	Logf    func(format string, args ...any)
	Clock   Clock
	// Features are the later stages that are in.
	Features Features
	// Model drafts recipes (Generate); nil answers no_model.
	Model Model
	// Language is the name of the language the model writes its note in: the request's, else the user's.
	Language func(ctx context.Context) string
	// Models says what a model purpose runs on now (ModelOf); nil: nothing can be called.
	Models func(ctx context.Context, purpose string) ModelInfo

	// The destinations (#2039, deliver.go).
	// Lang is the user's language, for the texts the manager writes into the Inbox (a catch-up digest, a failure notice).
	Lang func() i18n.Lang
	// ExtensionRules are an extension's own triage rules (its triage_rules setting), "" when it has none.
	ExtensionRules func(extension string) string
	// Delivered tells the destinations a subscription has something new for them: a run kept items, or the Inbox's
	// catch-up is ready.
	Delivered func(s Subscription)
	// Forget removes what the destinations took from a subscription (its Inbox items, its knowledge-base chats), for a
	// delete with its data. nil: nothing to remove.
	Forget func(ctx context.Context, s Subscription) error
}

// Clock is time for the runner: tests drive it.
type Clock interface {
	Now() time.Time
	// Sleep waits d or until ctx is done (its error then).
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Service is the subscriptions' API and runner.
type Service struct {
	o Options

	mu       sync.Mutex
	catalogs map[string]catalogEntry
	reading  map[string]bool // catalogs being read in the background
	gates    map[string]*gate
	busy     map[string]bool // connectors whose subscriptions are running now
	running  int
	asked    map[string]bool // subscriptions asked to run now
	wake     chan struct{}

	// Push sources (push.go): one push at a time, and each device's items in the last hour.
	pushMu sync.Mutex
	pushed map[string][]time.Time
}

type catalogEntry struct {
	sources []connector.CatalogSource
	err     error
	at      time.Time
	conn    ConnectorInfo // as it was when read (Catalogs shows it without asking again)
}

func New(o Options) *Service {
	if o.Location == nil {
		o.Location = func() *time.Location { return time.UTC } // the manager passes the user's (userLocation)
	}
	if o.Audit == nil {
		o.Audit = func(context.Context, string, string, Actor, map[string]any) {}
	}
	if o.Changed == nil {
		o.Changed = func(Subscription, bool) {}
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Language == nil {
		o.Language = func(context.Context) string { return "English" }
	}
	if o.Lang == nil {
		o.Lang = func() i18n.Lang { return i18n.Default }
	}
	if o.ExtensionRules == nil {
		o.ExtensionRules = func(string) string { return "" }
	}
	if o.Delivered == nil {
		o.Delivered = func(Subscription) {}
	}
	return &Service{o: o, catalogs: map[string]catalogEntry{}, reading: map[string]bool{}, gates: map[string]*gate{},
		busy: map[string]bool{}, asked: map[string]bool{}, wake: make(chan struct{}, 1), pushed: map[string][]time.Time{}}
}

func (s *Service) now() time.Time { return s.o.Clock.Now().UTC() }

// shown is s as the API shows it: off while both destinations are off, its floor of intervals filled in.
func shown(sub Subscription) Subscription {
	sub.State = cmpOr(sub.RunState, StateOK)
	if !sub.ToInbox && !sub.ToKB {
		sub.State = StateOff
	}
	sub.MinIntervalSeconds = floorOf(sub.Recipe.Mode, sub.SourceMin)
	if sub.Recipe.Args == nil {
		sub.Recipe.Args = emptyObject
	}
	return sub
}

func on(sub Subscription) bool { return sub.ToInbox || sub.ToKB }

func floorOf(mode string, sourceMin int) int {
	f := max(minInterval, sourceMin)
	if mode == wire.ConnectorSubscriptionModeSnapshot {
		f = max(f, minSnapshotInterval)
	}
	return f
}

func defaultIntervalOf(mode string, sourceMin int) int {
	if mode == wire.ConnectorSubscriptionModeSnapshot {
		return defaultSnapshot
	}
	return max(defaultInterval, sourceMin)
}

// Features are the later stages that are in.
func (s *Service) Features() Features { return s.o.Features }

// List is every subscription, or those of one connector or extension.
func (s *Service) List(ctx context.Context, f Filter) ([]Subscription, Features, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, s.o.Features, err
	}
	out := []Subscription{}
	for _, sub := range all {
		if f.Connector != "" && sub.Connector != f.Connector || f.Extension != "" && sub.Extension != f.Extension {
			continue
		}
		out = append(out, shown(sub))
	}
	return out, s.o.Features, nil
}

// Get is one subscription with its newest runs first.
func (s *Service) Get(ctx context.Context, id string) (Subscription, []Run, error) {
	sub, err := s.o.Store.GetSubscription(ctx, id)
	if err != nil {
		return Subscription{}, nil, err
	}
	if sub == nil {
		return Subscription{}, nil, notFound("subscription")
	}
	runs := slices.Clone(sub.Runs)
	slices.Reverse(runs)
	if runs == nil {
		runs = []Run{}
	}
	return shown(*sub), runs, nil
}

// Summary is each connector's line for the entry rows, from cached catalogs only: a stale or missing one is read
// again in the background and is not known until then.
func (s *Service) Summary(ctx context.Context) ([]ConnectorSummary, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := []ConnectorSummary{}
	for _, c := range s.o.Connectors.All(ctx) {
		line := ConnectorSummary{Connector: c.ID, Extension: c.Extension}
		for _, sub := range all {
			if sub.Connector != c.ID {
				continue
			}
			switch shown(sub).State {
			case StateOff:
			case StateFailing, StateNeedsRecipe:
				line.On++
				line.Failing++
			default:
				line.On++
			}
		}
		s.mu.Lock()
		e, ok := s.catalogs[c.ID]
		s.mu.Unlock()
		if ok && e.err == nil {
			can := subscribable(e.sources)
			line.Subscribable = &can
		} else if ok && e.err != nil {
			no := false
			line.Subscribable = &no
		}
		if c.Conn != nil && c.Enabled && (!ok || s.now().Sub(e.at) > catalogFresh) {
			s.readLater(c.ID)
		}
		out = append(out, line)
	}
	return out, nil
}

func subscribable(sources []connector.CatalogSource) bool {
	for _, src := range sources {
		for _, t := range src.Tools {
			if refusal(t) == nil {
				return true
			}
		}
	}
	return false
}

// readLater reads connector id's catalog in the background, once at a time, if it can be called now.
func (s *Service) readLater(id string) {
	s.mu.Lock()
	if s.reading[id] {
		s.mu.Unlock()
		return
	}
	s.reading[id] = true
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.reading, id)
			s.mu.Unlock()
		}()
		ctx := context.Background()
		if c, ok := s.o.Connectors.Find(ctx, id); ok && c.ready() {
			_, _ = s.catalog(ctx, c, 0)
		}
	}()
}

// catalog is c's catalog, read again when the one kept is older than fresh.
func (s *Service) catalog(ctx context.Context, c ConnectorInfo, fresh time.Duration) ([]connector.CatalogSource, error) {
	s.mu.Lock()
	e, ok := s.catalogs[c.ID]
	s.mu.Unlock()
	if ok && fresh > 0 && s.now().Sub(e.at) <= fresh {
		return e.sources, e.err
	}
	cctx, cancel := context.WithTimeout(ctx, catalogWait)
	defer cancel()
	sources, err := connector.ReadCatalog(cctx, c.Conn)
	s.mu.Lock()
	kept := c
	kept.Conn = nil
	s.catalogs[c.ID] = catalogEntry{sources: sources, err: err, at: s.now(), conn: kept}
	s.mu.Unlock()
	return sources, err
}

func catalogProblem(err error) *Problem {
	var re *rpc.Error
	if errors.As(err, &re) && re.Code == wire.ConnectorErrorCodeNotReadOnly {
		return &Problem{CodeNotReadOnly, "The connector's catalog marks a tool as one that changes something."}
	}
	p, _, conn := callProblem(err)
	if conn {
		return p
	}
	return &Problem{CodeCallFailed, "The connector's catalog could not be read."}
}

// Catalog is what a connector offers, read from it now.
func (s *Service) Catalog(ctx context.Context, id string) (Catalog, error) {
	c, ok := s.o.Connectors.Find(ctx, id)
	if !ok {
		return Catalog{}, notFound("connector")
	}
	if !c.ready() {
		out := Catalog{Connector: c.ID, Extension: c.Extension, Status: c.Status, Direct: c.Direct, Sources: []CatalogSource{}}
		out.Error = &Problem{CodeNotConnected, "The connector cannot be reached now."}
		return out, nil
	}
	sources, err := s.catalog(ctx, c, 0)
	all, lerr := s.o.Store.ListSubscriptions(ctx)
	if lerr != nil {
		return Catalog{}, lerr
	}
	return catalogOf(c, sources, err, all), nil
}

// Catalogs are the catalogs kept from earlier reads, one a connector still registered, without asking any connector:
// one not read yet is left out and read in the background, as is one kept longer than catalogFresh. Status and
// Direct are as they were at that read.
func (s *Service) Catalogs(ctx context.Context) ([]Catalog, error) {
	all, err := s.o.Store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	out := []Catalog{}
	for _, c := range s.o.Connectors.All(ctx) {
		s.mu.Lock()
		e, ok := s.catalogs[c.ID]
		s.mu.Unlock()
		if c.Conn != nil && c.Enabled && (!ok || s.now().Sub(e.at) > catalogFresh) {
			s.readLater(c.ID)
		}
		if !ok {
			continue
		}
		kept := e.conn
		kept.Extension = c.Extension
		out = append(out, catalogOf(kept, e.sources, e.err, all))
	}
	slices.SortFunc(out, func(a, b Catalog) int { return strings.Compare(a.Connector, b.Connector) })
	return out, nil
}

// catalogOf is a connector's catalog as the API shows it: each tool subscribable or refused with why, the sources'
// own recipes and the subscriptions on each.
func catalogOf(c ConnectorInfo, sources []connector.CatalogSource, err error, all []Subscription) Catalog {
	out := Catalog{Connector: c.ID, Extension: c.Extension, Status: c.Status, Direct: c.Direct, Sources: []CatalogSource{}}
	if err != nil {
		out.Error = catalogProblem(err)
		return out
	}
	for _, src := range sources {
		cs := CatalogSource{ID: src.ID, Name: src.Name, Icon: src.Icon, Service: src.Service, MinIntervalSeconds: src.MinIntervalSeconds,
			MaxCallsPerHour: src.MaxCallsPerHour, Tools: []CatalogTool{}, Recipes: slices.Clone(src.Recipes), Subscriptions: []string{}}
		if cs.Recipes == nil {
			cs.Recipes = []Recipe{}
		}
		for _, t := range src.Tools {
			ct := CatalogTool{Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: t.InputSchema, Refused: refusal(t)}
			ct.Subscribable = ct.Refused == nil
			cs.Tools = append(cs.Tools, ct)
		}
		for _, sub := range all {
			if sub.Connector == c.ID && sub.Source == src.ID {
				cs.Subscriptions = append(cs.Subscriptions, sub.ID)
			}
		}
		out.Sources = append(out.Sources, cs)
	}
	return out
}

// target is what a dry run or a save works on: the connector, the source in its catalog, the tool and the recipe.
type target struct {
	conn   ConnectorInfo
	source connector.CatalogSource
	tool   connector.CatalogTool
	recipe Recipe
	sub    *Subscription
}

// DryRun calls the recipe once and checks it, writing nothing.
func (s *Service) DryRun(ctx context.Context, in DryRunRequest) (DryRun, error) {
	var sub *Subscription
	if in.ID != "" {
		var err error
		if sub, err = s.o.Store.GetSubscription(ctx, in.ID); err != nil {
			return DryRun{}, err
		}
		if sub == nil {
			return DryRun{}, notFound("subscription")
		}
		if IsPush(*sub) {
			return DryRun{}, pushOnly("recipe")
		}
		in.Connector, in.Source = sub.Connector, sub.Source
		if in.Recipe == nil {
			r := sub.Recipe
			in.Recipe = &r
		}
	}
	if in.Connector == "" || in.Source == "" {
		return DryRun{}, errorf(http.StatusBadRequest, CodeBadRequest, "a dry run needs id, or connector and source")
	}
	c, ok := s.o.Connectors.Find(ctx, in.Connector)
	if !ok {
		return DryRun{}, notFound("connector")
	}
	interval, daily := -1, ""
	if sub != nil && (in.Recipe == nil || in.Recipe.Mode == sub.Recipe.Mode) {
		interval, daily = sub.IntervalSeconds, sub.DailyAt
	}
	dr, _ := s.check(ctx, c, in.Source, in.Recipe, sub, interval, daily)
	return dr, nil
}

// check is a full validation of a recipe on a source: read-only, arguments, a real call and its fields, dedup and
// the interval it would run at (-1: the default for the recipe; daily set: once a day). With no recipe, the source's
// first own one is used. t is filled as far as it got.
func (s *Service) check(ctx context.Context, c ConnectorInfo, sourceID string, recipe *Recipe, sub *Subscription, interval int, daily string) (DryRun, target) {
	dr := DryRun{Checks: []Check{{Key: CheckReadOnly}, {Key: CheckArgs}, {Key: CheckFields}, {Key: CheckDedup}, {Key: CheckInterval}},
		Items: []Preview{}, Paths: []PathValue{}}
	t := target{conn: c, sub: sub}
	fail := func(p *Problem) (DryRun, target) {
		dr.Error = p
		return dr, t
	}
	if !c.ready() {
		return fail(&Problem{CodeNotConnected, "The connector cannot be reached now."})
	}
	sources, err := s.catalog(ctx, c, time.Minute)
	if err != nil {
		p := catalogProblem(err)
		if p.Code == CodeNotReadOnly {
			dr.Checks[0].Error = p
		}
		return fail(p)
	}
	for _, src := range sources {
		if src.ID == sourceID {
			t.source = src
		}
	}
	if t.source.ID == "" {
		p := &Problem{CodeNotReadOnly, "The connector does not offer this source."}
		dr.Checks[0].Error = p
		return fail(p)
	}
	if recipe == nil {
		if len(t.source.Recipes) == 0 {
			return fail(&Problem{CodeNeedsRecipe, "The source has no recipe of its own: give one."})
		}
		r := t.source.Recipes[0]
		recipe = &r
	}
	t.recipe = *recipe
	if p := checkRecipe(t.recipe); p != nil {
		if p.Code == CodeBadArgs {
			dr.Checks[1].Error = p
		}
		return fail(p)
	}
	_, tool, p := admit(sources, sourceID, t.recipe.Tool)
	t.tool = tool
	if p != nil {
		dr.Checks[0].Error = p
		return fail(p)
	}
	dr.Checks[0].OK = true
	loc := s.o.Location()
	since := s.now().Add(-firstBack)
	if sub != nil && sub.SinceAt != nil {
		since = sub.SinceAt.Add(-overlap)
	}
	args, err := fillArgs(t.recipe.Args, vars(since, loc))
	if err == nil {
		err = checkArgs(tool.InputSchema, args)
	}
	if err != nil {
		p := &Problem{CodeBadArgs, "The arguments do not fit the tool: " + firstLine(err.Error())}
		dr.Checks[1].Error = p
		return fail(p)
	}
	dr.Checks[1].OK = true
	if interval < 0 {
		interval = defaultIntervalOf(t.recipe.Mode, intOr(t.source.MinIntervalSeconds))
	}
	if fl := floorOf(t.recipe.Mode, intOr(t.source.MinIntervalSeconds)); daily == "" && interval < fl {
		dr.Checks[4].Error = &Problem{CodeIntervalTooShort, fmt.Sprintf("The interval is shorter than %d seconds.", fl)}
	} else {
		dr.Checks[4].OK = true
	}
	label := t.source.Name
	if sub != nil {
		label = sub.Label
	}
	pl := pull(ctx, c.Conn, sourceID, t.recipe, since, loc, label)
	dr.Shape, dr.ItemsPath = pl.shape, pl.itemsPath
	if t.recipe.ItemsPath == "" && t.recipe.Mode != wire.ConnectorSubscriptionModeSnapshot {
		t.recipe.ItemsPath = pl.itemsPath // saved as found: later runs read the same place
	}
	if pl.problem != nil {
		switch pl.problem.Code {
		case CodeNoList, CodeTextNeedsSnap:
			dr.Checks[2].Error = pl.problem
		case CodeBadArgs:
			dr.Checks[1].OK, dr.Checks[1].Error = false, pl.problem
		case CodeNotReadOnly:
			dr.Checks[0].OK, dr.Checks[0].Error = false, pl.problem
		}
		return fail(pl.problem)
	}
	if pl.snapshot != nil {
		dr.Count = 1
		dr.Checks[2].OK, dr.Checks[3].OK = true, true
		body := clip(pl.snapshot.Body, previewMax)
		dr.Items = append(dr.Items, Preview{UID: pl.snapshot.UID, New: sub == nil || sub.SnapshotHash != pl.snapHash, Title: pl.snapshot.Title, Body: body})
	} else {
		dr.Count, dr.WeakDedup = len(pl.items), pl.weak
		switch {
		case pl.noTitle:
			dr.Checks[2].Error = &Problem{CodeNoTitle, fmt.Sprintf("No item has a title at %s.", fieldPath(t.recipe, "title"))}
		case pl.timeBad:
			dr.Checks[2].Error = &Problem{CodeBadTime, "A time or due date in the items cannot be read."}
		default:
			dr.Checks[2].OK = true
		}
		seen := map[string]bool{}
		if sub != nil {
			if seen, err = s.o.Store.SeenKeys(ctx, sub.ID, uids(pl.items)); err != nil {
				return fail(&Problem{CodeDryRunFailed, "The subscription's seen items could not be read."})
			}
		}
		fresh := newOnes(pl.items, seen)
		// The same answer again, with this one's items seen, must bring nothing new: dedup works.
		again := map[string]bool{}
		for _, m := range pl.items {
			again[m.item.UID] = true
		}
		if len(newOnes(pl.items, again)) == 0 {
			dr.Checks[3].OK = true
		} else {
			dr.Checks[3].Error = &Problem{CodeNotDeduped, "Pulling the same items again would bring them in again."}
		}
		for i, m := range pl.items {
			if i >= 3 {
				break
			}
			dr.Items = append(dr.Items, preview(m, !seen[m.item.UID]))
		}
		if pl.first != nil {
			dr.Paths = leaves(pl.first)
		}
		limit := pageLimit(pl.args)
		dr.MaybeMore = limit > 0 && len(pl.items) >= limit && len(fresh) == len(pl.items)
	}
	dr.OK = true
	for _, ch := range dr.Checks {
		if !ch.OK {
			dr.OK = false
		}
	}
	return dr, t
}

func intOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func fieldPath(r Recipe, name string) string {
	if r.Fields != nil && name == "title" && r.Fields.Title != "" {
		return r.Fields.Title
	}
	return "(no path)"
}

func preview(m mapped, fresh bool) Preview {
	p := Preview{UID: m.item.UID, New: fresh, Title: m.item.Title, Sender: m.item.Sender, Thread: m.item.Thread, Link: m.item.Link,
		Body: clip(m.item.Body, previewMax)}
	if m.item.Time != nil {
		v := m.item.Time.Format(time.RFC3339)
		p.Time = &v
	}
	if m.item.DueDate != nil {
		v := m.item.DueDate.Format(time.RFC3339)
		p.DueDate = &v
	}
	return p
}

// refused is the API error for a recipe that did not pass check.
func refused(dr DryRun) *Error {
	p := problemOf(dr)
	e := errorf(statusOf(p.Code), p.Code, p.Message)
	e.DryRun = &dr
	return e
}

// Create makes a subscription with both destinations off.
func (s *Service) Create(ctx context.Context, in CreateRequest, by Actor) (Subscription, error) {
	if in.Connector == "" || in.Source == "" {
		return Subscription{}, errorf(http.StatusBadRequest, CodeBadRequest, "connector and source are required")
	}
	if !slices.Contains([]string{"", OriginUser, OriginModel}, in.RecipeOrigin) || in.RecipeOrigin != "" && in.Recipe == nil {
		return Subscription{}, errorf(http.StatusBadRequest, CodeBadRequest, "recipe_origin is user or model, with a recipe")
	}
	c, ok := s.o.Connectors.Find(ctx, in.Connector)
	if !ok {
		return Subscription{}, notFound("connector")
	}
	dr, t := s.check(ctx, c, in.Source, in.Recipe, nil, -1, "")
	if !dr.OK {
		return Subscription{}, refused(dr)
	}
	now := s.now()
	origin := OriginExtension
	if in.Recipe != nil {
		origin = OriginUser
		if in.RecipeOrigin == OriginModel {
			origin = OriginModel
		}
	}
	srcMin := intOr(t.source.MinIntervalSeconds)
	sub := Subscription{ID: newID(), Connector: c.ID, Extension: c.Extension, Source: t.source.ID, SourceName: t.source.Name,
		Icon: t.source.Icon, Service: t.source.Service, Label: cmpOr(oneLine(in.Label), t.source.Name),
		Recipe: t.recipe, RecipeOrigin: origin, RecipeAt: now, Tool: t.recipe.Tool, ToolFingerprint: fingerprint(t.tool),
		IntervalSeconds: defaultIntervalOf(t.recipe.Mode, srcMin), RunState: StateOK, WeakDedup: dr.WeakDedup, Shape: dr.Shape,
		SourceMin: srcMin, MaxCallsPerHour: intOr(t.source.MaxCallsPerHour), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if t.recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
		sub.DailyAt = defaultDailyAt
	}
	if err := s.o.Store.CreateSubscription(ctx, sub); err != nil {
		return Subscription{}, err
	}
	s.o.Audit(ctx, c.ID, "subscription_created", by, map[string]any{"subscription": sub.ID, "source": sub.Source, "tool": sub.Tool, "origin": origin})
	s.o.Logf("subscription %s: made on %s/%s", sub.ID, c.ID, sub.Source)
	s.o.Changed(shown(sub), false)
	return shown(sub), nil
}

var dailyRE = lazyre.New(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// scheduleFor is the interval and daily time a recipe in mode would run at on sub, for check: its own while the mode
// stays, else the mode's default (-1) and, for a snapshot, the default daily time.
func scheduleFor(sub Subscription, mode string) (int, string) {
	if mode == sub.Recipe.Mode {
		return sub.IntervalSeconds, sub.DailyAt
	}
	if mode == wire.ConnectorSubscriptionModeSnapshot {
		return -1, defaultDailyAt
	}
	return -1, ""
}

// withRecipe is sub with a recipe that passed check (t, dr) from origin: its run state starts over, and a new mode
// takes that mode's default schedule. The switches, label and rules stay.
func withRecipe(sub Subscription, t target, dr DryRun, origin string, now time.Time) Subscription {
	next := sub
	next.Recipe, next.Tool, next.RecipeOrigin, next.RecipeAt = t.recipe, t.recipe.Tool, origin, now
	next.ToolFingerprint, next.WeakDedup, next.Shape = fingerprint(t.tool), dr.WeakDedup, dr.Shape
	next.SourceMin, next.MaxCallsPerHour = intOr(t.source.MinIntervalSeconds), intOr(t.source.MaxCallsPerHour)
	next.RunState, next.Error, next.FailCount, next.FailingSince, next.ShapeMisses = StateOK, nil, 0, nil, 0
	if t.recipe.Mode != sub.Recipe.Mode {
		next.SnapshotHash = ""
		next.IntervalSeconds = defaultIntervalOf(t.recipe.Mode, next.SourceMin)
		next.DailyAt = ""
		if t.recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
			next.DailyAt = defaultDailyAt
		}
	}
	return next
}

// Update changes a subscription made at p.Revision.
func (s *Service) Update(ctx context.Context, id string, p Patch, by Actor) (Subscription, error) {
	cur, err := s.o.Store.GetSubscription(ctx, id)
	if err != nil {
		return Subscription{}, err
	}
	if cur == nil {
		return Subscription{}, notFound("subscription")
	}
	if p.Revision != cur.Revision {
		return Subscription{}, conflictWith(cur)
	}
	if IsPush(*cur) && (p.Recipe != nil || p.IntervalSeconds != nil || p.DailyAt != nil) {
		return Subscription{}, pushOnly("recipe or schedule")
	}
	next, prev := *cur, cur.Revision
	now := s.now()
	var changed []string
	if p.Recipe != nil {
		c, ok := s.o.Connectors.Find(ctx, cur.Connector)
		if !ok {
			return Subscription{}, errorf(http.StatusServiceUnavailable, CodeNotConnected, "the connector is not registered")
		}
		interval, daily := scheduleFor(*cur, p.Recipe.Mode)
		if p.IntervalSeconds != nil {
			interval = *p.IntervalSeconds
		}
		if p.DailyAt != nil {
			daily = *p.DailyAt
		}
		dr, t := s.check(ctx, c, cur.Source, p.Recipe, cur, interval, daily)
		if !dr.OK {
			return Subscription{}, refused(dr)
		}
		next = withRecipe(next, t, dr, OriginUser, now)
		changed = append(changed, "recipe")
	}
	if p.Label != nil {
		next.Label = cmpOr(clip(oneLine(*p.Label), 200), next.SourceName)
		changed = append(changed, "label")
	}
	if p.TriageRules != nil {
		next.TriageRules = clip(*p.TriageRules, 4000)
		changed = append(changed, "triage_rules")
	}
	if p.FallbackTier != nil {
		if !slices.Contains([]string{"", "fyi", "action", "wait"}, *p.FallbackTier) {
			return Subscription{}, errorf(http.StatusBadRequest, CodeBadRequest, "fallback_tier is \"\", fyi, action or wait")
		}
		next.FallbackTier = *p.FallbackTier
		changed = append(changed, "fallback_tier")
	}
	schedule := false
	if p.DailyAt != nil {
		if *p.DailyAt != "" && (next.Recipe.Mode != wire.ConnectorSubscriptionModeSnapshot || !dailyRE.MatchString(*p.DailyAt)) {
			return Subscription{}, errorf(http.StatusBadRequest, CodeBadRequest, "daily_at is HH:MM, for snapshots only")
		}
		next.DailyAt, schedule = *p.DailyAt, true
		changed = append(changed, "daily_at")
	}
	if p.IntervalSeconds != nil {
		next.IntervalSeconds, schedule = *p.IntervalSeconds, true
		changed = append(changed, "interval_seconds")
	}
	if fl := floorOf(next.Recipe.Mode, next.SourceMin); !IsPush(next) && next.DailyAt == "" && next.IntervalSeconds < fl {
		return Subscription{}, &Error{Status: http.StatusUnprocessableEntity, Code: CodeIntervalTooShort,
			Message: fmt.Sprintf("the interval must be at least %d seconds", fl)}
	}
	wasOn, turnedOn := on(*cur), false
	if p.ToInbox != nil {
		next.ToInbox = *p.ToInbox
		changed = append(changed, "to_inbox")
		// The Inbox catches up from a day before it was turned on, once the first run since has read the source.
		switch {
		case next.ToInbox && !cur.ToInbox:
			next.InboxFrom, next.InboxCatchup, turnedOn = &now, nil, true
		case !next.ToInbox:
			next.InboxFrom, next.InboxCatchup = nil, nil
		}
	}
	if p.ToKB != nil {
		next.ToKB = *p.ToKB
		changed = append(changed, "to_kb")
		switch {
		case next.ToKB && !cur.ToKB:
			next.KBFrom, turnedOn = &now, true
		case !next.ToKB:
			next.KBFrom = nil
		}
	}
	switch {
	case !on(next):
		next.NextRunAt = nil
	case !wasOn || p.Recipe != nil || turnedOn: // a new recipe or destination runs soon, also after needs_recipe stopped it
		next.NextRunAt = &now
	case schedule:
		n := s.nextRun(next, now, false)
		if next.LastRunAt != nil && next.DailyAt == "" {
			if b := next.LastRunAt.Add(time.Duration(next.IntervalSeconds) * time.Second); b.Before(n) {
				n = b
			}
		}
		next.NextRunAt = &n
	}
	if IsPush(next) {
		// Nothing to pull: its catch-up ends when its Inbox is turned on (what came in the day before comes in now).
		next.NextRunAt = nil
		if next.ToInbox && !cur.ToInbox {
			next.InboxCatchup = &now
		}
	}
	if len(changed) == 0 {
		return shown(*cur), nil
	}
	next.Revision, next.UpdatedAt = prev+1, now
	ok, err := s.o.Store.UpdateSubscription(ctx, next, prev)
	if err != nil {
		return Subscription{}, err
	}
	if !ok {
		c, err := s.o.Store.GetSubscription(ctx, id)
		if err != nil || c == nil {
			return Subscription{}, notFound("subscription")
		}
		return Subscription{}, conflictWith(c)
	}
	s.o.Audit(ctx, next.Connector, "subscription_updated", by, map[string]any{"subscription": id, "fields": changed})
	s.o.Changed(shown(next), false)
	if IsPush(next) && turnedOn {
		s.o.Delivered(next) // what it holds reaches the destination turned on
	} else if on(next) && (!wasOn || p.Recipe != nil || turnedOn) {
		s.poke()
	}
	return shown(next), nil
}

// RunNow asks for a run as soon as the connector's limits allow, also while both destinations are off.
func (s *Service) RunNow(ctx context.Context, id string, by Actor) (Subscription, error) {
	sub, err := s.o.Store.GetSubscription(ctx, id)
	if err != nil {
		return Subscription{}, err
	}
	if sub == nil {
		return Subscription{}, notFound("subscription")
	}
	if IsPush(*sub) {
		return Subscription{}, pushOnly("pull")
	}
	s.mu.Lock()
	s.asked[id] = true
	s.mu.Unlock()
	s.o.Audit(ctx, sub.Connector, "subscription_run_now", by, map[string]any{"subscription": id})
	s.poke()
	out := shown(*sub)
	now := s.now()
	out.NextRunAt = &now
	return out, nil
}

// Delete removes a subscription with its dedup keys and pending items. withData also has the destinations remove what
// they took from it (#2039): its destinations are switched off first, so they take nothing more meanwhile; when the
// removal fails the subscription stays (off), and deleting again finishes it. It is recorded either way.
func (s *Service) Delete(ctx context.Context, id string, withData bool, by Actor) error {
	sub, err := s.o.Store.GetSubscription(ctx, id)
	if err != nil {
		return err
	}
	if sub == nil {
		return notFound("subscription")
	}
	if withData && s.o.Forget != nil {
		if sub, err = s.switchOff(ctx, *sub); err != nil {
			return err
		}
		if err := s.o.Forget(ctx, *sub); err != nil {
			s.o.Logf("subscription %s: delete its data: %v", id, err)
			return err
		}
	}
	if err := s.o.Store.DeleteSubscription(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.asked, id)
	s.mu.Unlock()
	s.o.Audit(ctx, sub.Connector, "subscription_deleted", by, map[string]any{"subscription": id, "data": withData})
	s.o.Logf("subscription %s: deleted (data %v)", id, withData)
	s.o.Changed(shown(*sub), true)
	return nil
}

// switchOff turns both destinations of sub off (a delete with its data, before the destinations remove theirs),
// at whatever revision it is now.
func (s *Service) switchOff(ctx context.Context, sub Subscription) (*Subscription, error) {
	for range 3 {
		if !on(sub) {
			return &sub, nil
		}
		next, prev := sub, sub.Revision
		next.ToInbox, next.ToKB, next.InboxFrom, next.InboxCatchup, next.KBFrom, next.NextRunAt = false, false, nil, nil, nil, nil
		next.Revision, next.UpdatedAt = prev+1, s.now()
		ok, err := s.o.Store.UpdateSubscription(ctx, next, prev)
		if err != nil {
			return nil, err
		}
		if ok {
			s.o.Changed(shown(next), false)
			return &next, nil
		}
		cur, err := s.o.Store.GetSubscription(ctx, sub.ID)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			return nil, notFound("subscription")
		}
		sub = *cur
	}
	return nil, errorf(http.StatusConflict, CodeConflict, "the subscription was changed meanwhile")
}

func newID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return "sub_" + hex.EncodeToString(b)
}

// MarshalJSON writes an Error as the API's error body.
func (e *Error) MarshalJSON() ([]byte, error) {
	type body struct {
		Code       string  `json:"code"`
		Message    string  `json:"message"`
		RetryAfter float64 `json:"retry_after,omitempty"`
	}
	return json.Marshal(body{e.Code, e.Message, e.RetryAfter})
}
