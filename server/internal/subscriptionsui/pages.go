// Package subscriptionsui draws the source subscription pages (#2041, docs/extensions/subscriptions.md "Pages") as
// declarative UI: the list, a source, its recipe, picking a tool and setting it up, its runs, and the small pickers.
// extmgr serves it at POST /api/v1/subscriptions/-/ui with the request and answer of an extension page, so every app
// draws it with the renderer it already has. Everything people read is localized here; the service gives codes.
package subscriptionsui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Service is what the pages use of subscriptions.Service.
type Service interface {
	List(ctx context.Context, f subscriptions.Filter) ([]subscriptions.Subscription, subscriptions.Features, error)
	Get(ctx context.Context, id string) (subscriptions.Subscription, []subscriptions.Run, error)
	Catalog(ctx context.Context, connector string) (subscriptions.Catalog, error)
	// Catalogs is every connector's catalog as cached, without asking any connector.
	Catalogs(ctx context.Context) ([]subscriptions.Catalog, error)
	DryRun(ctx context.Context, in subscriptions.DryRunRequest) (subscriptions.DryRun, error)
	Create(ctx context.Context, in subscriptions.CreateRequest, by subscriptions.Actor) (subscriptions.Subscription, error)
	Update(ctx context.Context, id string, p subscriptions.Patch, by subscriptions.Actor) (subscriptions.Subscription, error)
	RunNow(ctx context.Context, id string, by subscriptions.Actor) (subscriptions.Subscription, error)
	Delete(ctx context.Context, id string, withData bool, by subscriptions.Actor) error
	// Features says which later stages are in (Generate: #2040).
	Features() subscriptions.Features
	// Generate drafts a recipe with the model (#2040): for a subscription, saved; for a source, only answered.
	Generate(ctx context.Context, in subscriptions.GenerateRequest, by subscriptions.Actor) (subscriptions.Generated, error)
	// ModelOf is what a model purpose runs on now ("kb", "inbox.triage"); RecipeModel the one drafting uses.
	ModelOf(ctx context.Context, purpose string) subscriptions.ModelInfo
	RecipeModel(ctx context.Context) subscriptions.ModelInfo
	// Push makes or fills the device's push source (#3848); no items only makes it.
	Push(ctx context.Context, by subscriptions.Actor, in subscriptions.PushRequest) (subscriptions.PushResult, error)
}

// Env is who asks and what the pages need from the rest of the manager.
type Env struct {
	Lang i18n.Lang
	Loc  *time.Location
	Now  time.Time
	// CanEdit: a full-access device. Others see every page with its switches and buttons disabled.
	CanEdit bool
	By      subscriptions.Actor
	// Name is a connector's name as people know it (its extension's); the id when it says "".
	Name func(connector string) string
	// Native says where Persona already reads a catalog source's service ("mail:example.com") itself, such as
	// "me@example.com" for a mail account; "" when it does not.
	Native func(service string) string
	// UIVersion is the UI version the app draws (6 when 0): an older app gets icons it knows.
	UIVersion int
	// Drafts are the model drafts the manager keeps (#2040); nil: drafting is not offered.
	Drafts *Drafts
	// Platform is the asking app's (the UI context's): only "ios" offers to add its own Shortcuts source (#3848).
	Platform string
}

// Request is the page's state and, when something was pressed, the action and its values.
type Request struct {
	State  string
	Action string
	Values map[string]string
}

// ErrReadOnly answers an action from a device that may only look.
var ErrReadOnly = errors.New("read_only_device")

// Handle draws the page the state names, or does the action and answers what the app shows next.
func Handle(ctx context.Context, svc Service, env Env, req Request) (wire.ActionResult, error) {
	if env.Loc == nil {
		env.Loc = time.UTC // the manager passes the user's; never the machine's
	}
	if env.UIVersion == 0 {
		env.UIVersion = 6
	}
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	if env.Name == nil {
		env.Name = func(c string) string { return c }
	}
	if env.Native == nil {
		env.Native = func(string) string { return "" }
	}
	p := &pages{ctx: ctx, svc: svc, env: env, st: parseState(req.State)}
	if req.Action != "" {
		if !env.CanEdit {
			return wire.ActionResult{}, ErrReadOnly
		}
		return p.act(req.Action, req.Values)
	}
	v := p.draw()
	return wire.ActionResult{View: &v}, nil
}

// state is a page: P names it ("" the list), the rest says which subscription, connector, source or tool.
type state struct {
	P   string `json:"p,omitempty"`
	ID  string `json:"id,omitempty"`
	C   string `json:"c,omitempty"`
	S   string `json:"s,omitempty"`
	T   string `json:"t,omitempty"`
	Ext string `json:"ext,omitempty"`
	// I is where a tool's items are (a recipe's items_path), as the setup page's dry run found it.
	I string `json:"i,omitempty"`
	// G: the setup page starts from the model's draft for this source or subscription (#2040).
	G bool `json:"g,omitempty"`
}

const (
	pageList     = ""
	pageSource   = "source"
	pageRecipe   = "recipe"
	pageTools    = "tools"
	pageSetup    = "setup"
	pageRuns     = "runs"
	pageInterval = "interval"
	pageFallback = "fallback"
	pageRules    = "rules"
	pageRaw      = "raw"
)

func parseState(raw string) state {
	var s state
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &s) // an unreadable state is the list
	}
	return s
}

func (s state) String() string {
	b, _ := json.Marshal(s)
	return string(b)
}

type pages struct {
	ctx context.Context
	svc Service
	env Env
	st  state
	// models are the purposes' models as asked once for this request.
	models map[string]subscriptions.ModelInfo
}

// modelOf is what a purpose runs on, asked of the assistant at most once a request ("recipe": drafting's).
func (p *pages) modelOf(purpose string) subscriptions.ModelInfo {
	if m, ok := p.models[purpose]; ok {
		return m
	}
	var m subscriptions.ModelInfo
	if purpose == "recipe" {
		m = p.svc.RecipeModel(p.ctx)
	} else {
		m = p.svc.ModelOf(p.ctx, purpose)
	}
	if p.models == nil {
		p.models = map[string]subscriptions.ModelInfo{}
	}
	p.models[purpose] = m
	return m
}

// canDraft: the manager keeps drafts and the model stage is in.
func (p *pages) canDraft() bool { return p.env.Drafts != nil && p.svc.Features().Generate }

func (p *pages) t(key string, args ...i18n.Args) string {
	return i18n.T(p.env.Lang, "subscriptions-pages."+key, args...)
}

func (p *pages) draw() wire.View {
	var v wire.View
	switch p.st.P {
	case pageSource:
		v = p.source()
	case pageRecipe:
		v = p.recipe(nil, nil)
	case pageTools:
		v = p.tools()
	case pageSetup:
		v = p.setup(nil, nil, nil)
	case pageRuns:
		v = p.runs()
	case pageInterval:
		v = p.interval()
	case pageFallback:
		v = p.fallback()
	case pageRules:
		v = p.rules()
	case pageRaw:
		v = p.raw("")
	default:
		v = p.list()
	}
	if v.State == "" {
		v.State = p.st.String()
	}
	if !p.env.CanEdit {
		p.readOnly(&v)
	}
	return v
}

// readOnly is a page for a device that may only look: switches disabled, fields shown as their values, buttons and
// pickers that would change something gone, and a line at the top saying why.
func (p *pages) readOnly(v *wire.View) {
	for i := range v.Sections {
		items := v.Sections[i].Items[:0]
		for _, c := range v.Sections[i].Items {
			switch {
			case c.Control != nil:
				c.Control.Disabled = true
			case c.Type == "button" && (c.Action == nil || c.Action.Push == nil):
				continue
			case c.Type == "row" && c.Action != nil && c.Action.Push == nil:
				c.Action = nil
			case c.Type == "field":
				val := c.Value
				for _, ch := range c.Choices {
					if ch.Value == c.Value {
						val = ch.Title
					}
				}
				c = wire.Component{Type: "row", Key: c.Key, Title: c.Label, Subtitle: val}
			}
			items = append(items, c)
		}
		v.Sections[i].Items = items
	}
	note := wire.Section{Key: "read_only", Items: []wire.Component{text("read_only", p.t("read_only"), "secondary")}}
	v.Sections = append([]wire.Section{note}, v.Sections...)
}

// problemText is a service problem in words people read.
func (p *pages) problemText(pr *subscriptions.Problem) string {
	if pr == nil {
		return ""
	}
	key := "problem." + pr.Code
	if i18n.Has("subscriptions-pages." + key) {
		return p.t(key)
	}
	return p.t("problem.other")
}

// errorResult is a failed action: the page drawn again, with a toast saying what went wrong.
func (p *pages) errorResult(err error) (wire.ActionResult, error) {
	var se *subscriptions.Error
	if !errors.As(err, &se) {
		return wire.ActionResult{}, err
	}
	msg := p.problemText(&subscriptions.Problem{Code: se.Code, Message: se.Message})
	if se.Status == http.StatusConflict {
		msg = p.t("toast.conflict")
	}
	v := p.draw()
	return wire.ActionResult{View: &v, Toast: msg}, nil
}
