package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
)

// New chat opens a direction (docs/design/proposals/owner-at-a-glance.md "New chat opens a direction; New agent goes
// under one", #3607). Right after the owner sends a New chat's first message, the hub asks a small model under its own
// purpose, assistant.route, off the conversation's path, whether the sentence opens a direction (a leader, a peer of the
// existing ones) or is a quick chat. One call within routeTimeout: a slow, failed or unreadable answer is a quick chat,
// and nothing shows (no strip, no error). A direction is offered, not made: the offer is kept (assistant.route_offers)
// and told to the apps (direction_offer), and the owner's tap makes it (Start direction), or drops it (Just a quick
// chat). A quick chat can be made a direction later (Make it a direction), and any direction ended (End direction).
// Those taps are the owner's own choice: they need no phone approval, unlike the assistant's persona_settings change of
// assistant.leaders, and the record says by "user", so the hub never redoes them (directions.go).

const routeOffersKey = "assistant.route_offers"

// routeTimeout is all one routing may take (the proposal's "about 2 s"); tests change it.
var routeTimeout = 2 * time.Second

const (
	// routeCrowded: with this many leaders or more, the offer names the closest existing leader to use instead.
	routeCrowded = 6
	// routeTitleMax is a direction's suggested name, in characters.
	routeTitleMax = 20
	// routeMaxTokens: the answer is one small JSON object.
	routeMaxTokens = 400
	// routeProjectsMax: how many knowledge-base projects the model is shown.
	routeProjectsMax = 40
	// routeOfferKeep: an offer nobody answered in this long is dropped.
	routeOfferKeep = 7 * 24 * time.Hour
	// leadersMost is the hard limit on leaders, the same for every way one is made (setLeaders, the owner's taps).
	leadersMost = 20
)

// routeAnswer is the model's answer (prompts assistant/route).
type routeAnswer struct {
	Kind    string `json:"kind"` // direction | quick
	Title   string `json:"title"`
	Project string `json:"project"`
	Closest string `json:"closest"`
}

// routeLeader is a current leader as the model and the rules see it.
type routeLeader struct{ ID, Title, Project string }

// routeOffer is a direction offered for a New chat, waiting for the owner's Start direction or Just a quick chat.
type routeOffer struct {
	MachineID string `json:"machine_id"`
	// Title: the direction's suggested name.
	Title string `json:"title"`
	// Project: the slug of the existing knowledge-base project the sentence names; NewProject: else the name of the
	// project Start direction makes ("" both: none).
	Project    string `json:"project,omitempty"`
	NewProject string `json:"new_project,omitempty"`
	// Closest: with routeCrowded leaders or more, the existing leader to use instead (the strip suggests renaming it or
	// going on there).
	Closest string    `json:"closest,omitempty"`
	At      time.Time `json:"at"`
}

type routeBook struct {
	mu     sync.Mutex
	loaded bool
	m      map[string]*routeOffer // by conversation id
	// ask is the model call (nil: assistant.route's model); tests set it.
	ask func(ctx context.Context, system, input string, out any) error
}

// decideRoute is the rule table: what the model's answer (or its error) makes of the sentence, given the current
// leaders and how a project name is found in the knowledge base (ok false: no such live project). No offer (false) is a
// quick chat.
func decideRoute(sentence string, a routeAnswer, err error, leaders []routeLeader, findProject func(string) (string, bool)) (routeOffer, bool) {
	if err != nil || !strings.EqualFold(strings.TrimSpace(a.Kind), "direction") {
		return routeOffer{}, false
	}
	o := routeOffer{Title: clip(oneLine(a.Title), routeTitleMax)}
	if o.Title == "" {
		o.Title = clip(oneLine(sentence), routeTitleMax)
	}
	if o.Title == "" {
		return routeOffer{}, false
	}
	if p := strings.TrimSpace(a.Project); p != "" {
		if slug, ok := findProject(p); ok {
			o.Project = slug
		} else {
			o.NewProject = clip(oneLine(p), 40)
		}
	}
	if len(leaders) >= routeCrowded {
		closest := strings.TrimSpace(a.Closest)
		if !slices.ContainsFunc(leaders, func(l routeLeader) bool { return l.ID == closest }) {
			closest = ""
		}
		if closest == "" && o.Project != "" { // the model named none: the leader filed under the same project, if one is
			if i := slices.IndexFunc(leaders, func(l routeLeader) bool { return l.Project == o.Project }); i >= 0 {
				closest = leaders[i].ID
			}
		}
		o.Closest = closest
	}
	return o, true
}

// routeNewChat routes New chat id on machine, whose first message is sentence: in the background, at most
// routeTimeout; an offer is kept and told, anything else leaves no trace but a log line.
func (s *Service) routeNewChat(machine, id, sentence string) {
	sentence = strings.TrimSpace(contextBlock.ReplaceAllString(sentence, ""))
	if sentence == "" || id == "" {
		return
	}
	ctx := context.Background()
	cfg := s.settings(ctx)
	if machine != cfg.MachineID || id == cfg.Main || slices.Contains(cfg.Leaders, id) {
		return
	}
	leaders := s.routeLeaders(ctx)
	e, kbErr := s.kbGet(ctx)
	find := func(name string) (string, bool) {
		if kbErr != nil || e == nil {
			return "", false
		}
		p := e.FindProject(name)
		if p == nil || !p.Live() || p.Status == "ignored" {
			return "", false
		}
		return p.Slug, true
	}
	var projects []string
	if kbErr == nil && e != nil {
		projects = routeProjects(e)
	}
	system := prompts.RenderRoute(prompts.Route{Language: answerLanguage(s.hello(ctx).Locale), TitleMax: routeTitleMax,
		Crowded: len(leaders) >= routeCrowded, Leaders: len(leaders)})
	in := prompts.RouteInput{Sentence: cutRunes(sentence, 1500), Projects: projects}
	for _, ld := range leaders {
		in.Leaders = append(in.Leaders, prompts.RouteLeader{ID: ld.ID, Title: ld.Title, Project: ld.Project})
	}
	var a routeAnswer
	mctx, cancel := context.WithTimeout(ctx, routeTimeout) // the model's budget: the lookups above are local
	err := s.askRouteModel(mctx, system, prompts.RenderRouteInput(in), &a)
	cancel()
	o, ok := decideRoute(sentence, a, err, leaders, find)
	if err != nil {
		s.logf("route %s: %v (a quick chat)", short(id), err)
	}
	if !ok {
		return
	}
	o.MachineID, o.At = machine, time.Now().UTC()
	b := &s.offers
	b.mu.Lock()
	// The owner may have made it a direction (or the main conversation) while the model answered: nothing to offer.
	// makeDirection takes the offer after it changed the leaders, under this lock, so one of the two always sees the other.
	if now := s.settings(ctx); id == now.Main || slices.Contains(now.Leaders, id) || !s.routeOffersLocked(ctx) {
		b.mu.Unlock()
		return
	}
	b.m[id] = &o
	s.saveRouteOffersLocked(ctx)
	b.mu.Unlock()
	s.logf("route %s: direction %q offered", short(id), o.Title)
	s.event(ctx, map[string]any{"type": "direction_offer", "conversation": id, "offer": o})
}

// askRouteModel asks the model (or the test's stand-in).
func (s *Service) askRouteModel(ctx context.Context, system, input string, out any) error {
	if ask := s.offers.ask; ask != nil {
		return ask(ctx, system, input, out)
	}
	return s.purposeModel("assistant.route").JSONMax(ctx, system, input, routeMaxTokens, out)
}

// routeLeaders are the current leaders with their titles and projects.
func (s *Service) routeLeaders(ctx context.Context) []routeLeader {
	cfg := s.settings(ctx)
	titles, _ := s.conversationTitles()
	views, _ := s.directionViews(ctx)
	var out []routeLeader
	for _, id := range cfg.Leaders {
		if id == "" || id == cfg.Main {
			continue
		}
		ld := routeLeader{ID: id, Title: titles[id].title}
		if d := views[id]; d != nil {
			ld.Project = d.Project
		}
		out = append(out, ld)
	}
	return out
}

// routeProjects are the names of the live projects the model may file a direction under, long ones first, then by
// the most recently updated.
func routeProjects(e *kb.Engine) []string {
	var list []*kb.Project
	for _, p := range e.Store.Projects() {
		if p.Live() && p.Status != "ignored" && p.Status != "done" && p.MergedInto == "" {
			list = append(list, p)
		}
	}
	slices.SortFunc(list, func(a, b *kb.Project) int {
		if (a.Kind == "long") != (b.Kind == "long") {
			return cond(a.Kind == "long", -1, 1)
		}
		return b.Updated.Compare(a.Updated)
	})
	var out []string
	for _, p := range list {
		if len(out) == routeProjectsMax {
			break
		}
		out = append(out, p.Name)
	}
	return out
}

// ---- the offers ----

func (s *Service) routeOffersLocked(ctx context.Context) bool {
	b := &s.offers
	if b.loaded {
		return true
	}
	m := map[string]*routeOffer{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, routeOffersKey, &m); err != nil {
			s.logf("load route offers: %v", err)
			return false
		}
	}
	if m == nil {
		m = map[string]*routeOffer{}
	}
	b.m, b.loaded = m, true
	return true
}

func (s *Service) saveRouteOffersLocked(ctx context.Context) {
	for id, o := range s.offers.m {
		if time.Since(o.At) > routeOfferKeep {
			delete(s.offers.m, id)
		}
	}
	st := s.store()
	if st == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := st.PutSetting(ctx, routeOffersKey, s.offers.m); err != nil {
		s.logf("save route offers: %v", err)
	}
}

// takeRouteOffer removes conversation id's offer and returns it (nil: none). told: the apps hear it is gone.
func (s *Service) takeRouteOffer(ctx context.Context, id string) (*routeOffer, error) {
	b := &s.offers
	b.mu.Lock()
	if !s.routeOffersLocked(ctx) {
		b.mu.Unlock()
		return nil, errors.New("the direction offers could not be read")
	}
	o := b.m[id]
	if o != nil {
		delete(b.m, id)
		s.saveRouteOffersLocked(ctx)
	}
	b.mu.Unlock()
	if o != nil {
		s.event(context.WithoutCancel(ctx), map[string]any{"type": "direction_offer", "conversation": id, "offer": nil})
	}
	return o, nil
}

// ---- the owner's taps ----

// errNoOffer: Start direction on a conversation with no offer.
var errNoOffer = errors.New("no direction is offered for this conversation: make it one with …/make")

// makeDirection makes assistant conversation id a leader as the owner chose: filed under project (a slug of an existing
// knowledge-base project), or a new project named newProject, or none; title, when given, names it unless the owner
// named it already. The record says by "user"; leader.changed follows (directionsSync).
func (s *Service) makeDirection(ctx context.Context, id, project, newProject, title string) (*leaderView, error) {
	cfg := s.settings(ctx)
	if id == cfg.Main {
		return nil, errors.New("the main conversation supervises already: a direction is a side conversation")
	}
	if id == cfg.BackgroundSupervisor {
		return nil, errors.New("the background supervisor is built in: a direction is another side conversation")
	}
	c, ok := s.session(ctx, cfg.MachineID, id)
	if !ok || !c.Assistant {
		return nil, fmt.Errorf("%s is not a Persona conversation on the assistant's machine", id)
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	already := slices.Contains(cfg.Leaders, id)
	if !already && len(cfg.Leaders) >= leadersMost {
		return nil, fmt.Errorf("at most %d directions: end one first", leadersMost)
	}
	if project != "" { // checked before anything changes
		slug, err := s.todoProject(ctx, project)
		if err != nil {
			return nil, err
		}
		project = slug
	}
	// The leaders first (the limit is checked again there, against a tap at the same moment): what follows only adds to
	// a direction that exists.
	if !already {
		if _, err := s.updateSettings(context.WithoutCancel(ctx), func(v *Settings) error {
			if !slices.Contains(v.Leaders, id) {
				if len(v.Leaders) >= leadersMost {
					return fmt.Errorf("at most %d directions: end one first", leadersMost)
				}
				v.Leaders = append(slices.Clone(v.Leaders), id)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		s.settingsChanged(ctx)
	}
	if project == "" && newProject != "" {
		if e, err := s.kbGet(ctx); err == nil && e != nil {
			slug, err := e.Apply(kb.KindProject, newProject, kb.Update{Name: newProject, Kind: "long"})
			if err != nil {
				s.logf("direction %s: project %q: %v (filed under none)", short(id), newProject, err)
			} else {
				project = slug
			}
		}
	}
	now := time.Now().UTC()
	b := &s.directions
	b.mu.Lock()
	if !s.directionsLocked(ctx) {
		b.mu.Unlock()
		return nil, errors.New("the directions could not be read")
	}
	d := b.m[id]
	if d == nil {
		d = &direction{MachineID: cfg.MachineID, At: now}
		b.m[id] = d
	} else if d.EndedAt != nil { // a direction again: since now, keeping its name, project and last progress
		d.At = now
	}
	d.By, d.EndedAt = directionByUser, nil
	if project != "" {
		d.Project = project
	}
	if t := clip(oneLine(title), directionNameMost); t != "" && d.Name == "" { // the name the owner accepted or gave (#3679)
		d.Name, d.NameBy = t, directionByUser
	}
	s.saveDirectionsLocked(ctx)
	view := leaderView{By: d.By, At: d.At}
	view.ID, view.Project = id, d.Project
	b.mu.Unlock()
	if title = strings.TrimSpace(title); title != "" {
		if by, _ := s.titleOf(id); by != titleByUser {
			s.titleSet(c.MachineID, id, title, titleByAssistant)
		}
	}
	if titles, ok := s.conversationTitles(); ok {
		view.Title = titles[id].title
	}
	if d := s.directionOf(id); d != nil {
		pn, pok := s.projectNames(ctx)
		view.Name = nameOf(d, view.Title, pn, pok)
	}
	_, _ = s.takeRouteOffer(ctx, id)
	s.directionsSoon()
	return &view, nil
}

// endDirection takes leader id out of assistant.leaders as the owner chose: its record is ended (directionsSync), its
// tasks' signals go back to the main conversation, nothing is deleted. One that is no leader is a no-op.
func (s *Service) endDirection(ctx context.Context, id string) (bool, error) {
	cfg := s.settings(ctx)
	if !slices.Contains(cfg.Leaders, id) {
		return false, nil
	}
	if s.store() == nil {
		return false, errors.New("no database on this hub")
	}
	if _, err := s.updateSettings(ctx, func(v *Settings) error {
		v.Leaders = slices.DeleteFunc(slices.Clone(v.Leaders), func(l string) bool { return l == id })
		return nil
	}); err != nil {
		return false, err
	}
	b := &s.directions
	b.mu.Lock()
	if s.directionsLocked(ctx) {
		if d := b.m[id]; d != nil {
			d.By = directionByUser
			s.saveDirectionsLocked(ctx)
		}
	}
	b.mu.Unlock()
	s.settingsChanged(ctx)
	s.directionsSoon()
	return true, nil
}

// routeRoutes are the owner's taps on a direction (docs/protocol/protocol.md "REST (App → hub)").
func (s *Service) routeRoutes() {
	s.handle("GET /api/v1/assistant/directions/offers", func(w http.ResponseWriter, r *http.Request) {
		type offerView struct {
			Conversation string `json:"conversation"`
			routeOffer
		}
		b := &s.offers
		b.mu.Lock()
		if !s.routeOffersLocked(r.Context()) {
			b.mu.Unlock()
			apierr.Write(w, http.StatusServiceUnavailable, errors.New("the direction offers could not be read"))
			return
		}
		list := make([]offerView, 0, len(b.m))
		for id, o := range b.m {
			if time.Since(o.At) <= routeOfferKeep {
				list = append(list, offerView{id, *o})
			}
		}
		b.mu.Unlock()
		slices.SortFunc(list, func(a, b offerView) int { return b.At.Compare(a.At) })
		apierr.WriteJSON(w, map[string]any{"offers": list})
	})
	s.handle("DELETE /api/v1/assistant/directions/offers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change directions"))
			return
		}
		o, err := s.takeRouteOffer(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"dropped": o != nil})
	})
	direction := func(start bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !fullRole(r) {
				apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change directions"))
				return
			}
			var in struct {
				Project string `json:"project"` // an existing project's name or slug, over the offer's
				Title   string `json:"title"`   // over the offer's
			}
			if r.ContentLength != 0 {
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
					apierr.Write(w, http.StatusBadRequest, err)
					return
				}
			}
			id := r.PathValue("id")
			ctx := r.Context()
			project, newProject, title := strings.TrimSpace(in.Project), "", strings.TrimSpace(in.Title)
			if start {
				b := &s.offers
				b.mu.Lock()
				ok := s.routeOffersLocked(ctx)
				var o *routeOffer
				if ok && b.m[id] != nil {
					cp := *b.m[id]
					o = &cp
				}
				b.mu.Unlock()
				if !ok {
					apierr.Write(w, http.StatusServiceUnavailable, errors.New("the direction offers could not be read"))
					return
				}
				if o == nil {
					apierr.Write(w, http.StatusNotFound, errNoOffer)
					return
				}
				if project == "" {
					project, newProject = o.Project, o.NewProject
				}
				if title == "" {
					title = o.Title
				}
			}
			v, err := s.makeDirection(ctx, id, project, newProject, title)
			if err != nil {
				apierr.Write(w, http.StatusConflict, err)
				return
			}
			apierr.WriteJSON(w, map[string]any{"leader": v})
		}
	}
	s.handle("POST /api/v1/assistant/directions/{id}/start", direction(true))
	s.handle("POST /api/v1/assistant/directions/{id}/make", direction(false))
	s.handle("POST /api/v1/assistant/directions/{id}/end", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change directions"))
			return
		}
		ended, err := s.endDirection(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ended": ended})
	})
}
