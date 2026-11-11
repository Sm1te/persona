package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// New agent goes under a leader (docs/design/proposals/owner-at-a-glance.md "New chat opens a direction; New agent goes
// under one", #3608). The New agent sheet asks which leader a new agent belongs under (POST /api/v1/assistant/route/agent),
// pre-fills its "Under" field with the answer, and once the agent started puts it there
// (POST /api/v1/assistant/agents/{mid}/{sid}/supervisor): a supervised task of that leader, as agent_supervise makes one
// for the main conversation, so the leader gets its supervision messages (digestTo) and reviews it. Which leader:
//
//  1. the knowledge-base project that owns the agent's directory, when a current leader is filed under it;
//  2. else the prompt matched against the leaders' names and projects by assistant.route (routeTimeout, as for a New chat);
//  3. else Persona, the main conversation (no leaders, no match, the model slow or failing).

// Why a leader was suggested.
const (
	agentRouteProject = "project"
	agentRouteModel   = "model"
	agentRouteDefault = "default"
)

// agentRouteAnswer is the model's answer (prompts assistant/route_agent).
type agentRouteAnswer struct {
	Leader string `json:"leader"`
}

// agentRoute is where a new agent goes: Supervisor a conversation id (the main conversation's when Main), By why.
type agentRoute struct {
	Supervisor string `json:"supervisor"`
	Main       bool   `json:"main,omitempty"`
	Title      string `json:"title,omitempty"` // the leader's name as the apps show it ("" for the main conversation)
	By         string `json:"by"`
}

// decideAgentRoute is the rule table for a new agent: the leaders filed under a project that owns its directory (in
// dirProjects, by slug) win; else the leader the model named (an id it made up counts for nothing); else main.
func decideAgentRoute(main string, leaders []routeLeader, dirProjects []string, a agentRouteAnswer, err error) agentRoute {
	for _, l := range leaders {
		if l.Project != "" && slices.Contains(dirProjects, l.Project) {
			return agentRoute{Supervisor: l.ID, Title: l.Title, By: agentRouteProject}
		}
	}
	if err == nil {
		if i := slices.IndexFunc(leaders, func(l routeLeader) bool { return l.ID == strings.TrimSpace(a.Leader) }); i >= 0 {
			return agentRoute{Supervisor: leaders[i].ID, Title: leaders[i].Title, By: agentRouteModel}
		}
	}
	return agentRoute{Supervisor: main, Main: true, By: agentRouteDefault}
}

// routeAgent suggests the conversation a new agent on machine in cwd, started with prompt, goes under.
func (s *Service) routeAgent(ctx context.Context, machine, cwd, prompt string) agentRoute {
	main := s.settings(ctx).Main
	leaders := s.routeLeaders(ctx)
	if len(leaders) == 0 {
		return agentRoute{Supervisor: main, Main: true, By: agentRouteDefault}
	}
	var slugs []string
	if e, err := s.kbGet(ctx); err == nil && e != nil && cwd != "" {
		for _, p := range e.ProjectsForDir(machine, cwd) {
			slugs = append(slugs, p.Slug)
		}
	}
	if r := decideAgentRoute(main, leaders, slugs, agentRouteAnswer{}, errors.New("not asked")); r.By == agentRouteProject {
		return r
	}
	prompt = strings.TrimSpace(contextBlock.ReplaceAllString(prompt, ""))
	if prompt == "" {
		return decideAgentRoute(main, leaders, slugs, agentRouteAnswer{}, errors.New("no prompt"))
	}
	in := prompts.RouteInput{Sentence: cutRunes(prompt, 1500)}
	for _, l := range leaders {
		in.Leaders = append(in.Leaders, prompts.RouteLeader{ID: l.ID, Title: l.Title, Project: l.Project})
	}
	var a agentRouteAnswer
	mctx, cancel := context.WithTimeout(ctx, routeTimeout)
	err := s.askRouteModel(mctx, prompts.RenderRouteAgent(), prompts.RenderRouteInput(in), &a)
	cancel()
	if err != nil {
		s.logf("route agent on %s: %v (Persona)", machine, err)
	}
	return decideAgentRoute(main, leaders, slugs, a, err)
}

// putAgentUnder puts agent sid on machine mid under conversation lead (the main conversation or a current leader; ""
// is the main conversation): an agent of no task becomes a supervised task of lead, with its card in lead's
// conversation; one already on a task the hub tracks moves to lead (its signals follow from the next one); one on the
// owner's own to-do stays theirs. made says a task was made.
func (s *Service) putAgentUnder(ctx context.Context, mid, sid, lead, title string) (Todo, bool, error) {
	cfg := s.settings(ctx)
	if lead == "" {
		lead = cfg.Main
	}
	if lead == "" {
		return Todo{}, false, errors.New("there is no main conversation to supervise it")
	}
	if lead != cfg.Main && !s.isLeader(ctx, lead) {
		return Todo{}, false, fmt.Errorf("%s is not a leader conversation (GET /api/v1/assistant/leaders lists them)", lead)
	}
	am, ok := s.device(ctx, mid)
	if !ok {
		return Todo{}, false, fmt.Errorf("%w: no machine %q", errAgentNotFound, mid)
	}
	c, ok := s.session(ctx, am.ID, sid)
	if !ok {
		return Todo{}, false, fmt.Errorf("%w: no session %s on %s", errAgentNotFound, sid, am.Name)
	}
	if c.Assistant {
		return Todo{}, false, errors.New("that is an assistant conversation, not a coding agent")
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return Todo{}, false, err
	}
	if t, ok := s.openTodoOfAgent(am.ID, c.SessionID); ok {
		if t.Origin == "" {
			return Todo{}, false, fmt.Errorf("%s is on the owner's own to-do %q: it stays theirs", agentLabel(c), t.Title)
		}
		v, moved, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if v.Supervisor == lead {
				return false
			}
			v.Supervisor = lead
			return true
		})
		if err != nil {
			return Todo{}, false, err
		}
		if moved {
			s.taskCard(ctx, lead, t.ID, wakeActive) // its card follows it (the old conversation keeps its history)
		}
		t.Supervision = &v
		return t, false, nil
	}
	notes := i18n.T(s.lang(ctx), "assistant-tasks.adopt_notes", i18n.Args{"agent": agentLabel(c), "machine": am.Name, "cwd": c.Cwd})
	todo, made, err := s.adoptAgent(ctx, lead, am, c, title, "", notes, nil, 0, false)
	if err != nil {
		return Todo{}, false, err
	}
	if made {
		s.taskCard(ctx, lead, todo.ID, wakeActive)
	}
	return todo, made, nil
}

var errAgentNotFound = errors.New("not found")

// agentRouteRoutes: the New agent sheet's two calls (docs/protocol/protocol.md "REST (App → hub)").
func (s *Service) agentRouteRoutes() {
	s.handle("POST /api/v1/assistant/route/agent", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) { // it costs a model call: only a device that may start the agent asks
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot start agents"))
			return
		}
		var in struct {
			MachineID string `json:"machine_id"`
			Cwd       string `json:"cwd"`
			Prompt    string `json:"prompt"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), routeTimeout+3*time.Second)
		defer cancel()
		apierr.WriteJSON(w, s.routeAgent(ctx, in.MachineID, in.Cwd, in.Prompt))
	})
	s.handle("POST /api/v1/assistant/agents/{mid}/{sid}/supervisor", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot hand out tasks"))
			return
		}
		var in struct {
			Supervisor string `json:"supervisor"` // a leader conversation; "" the main conversation
			Title      string `json:"title"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		todo, made, err := s.putAgentUnder(r.Context(), r.PathValue("mid"), r.PathValue("sid"), strings.TrimSpace(in.Supervisor), in.Title)
		if err != nil {
			apierr.Write(w, cond(errors.Is(err, errAgentNotFound), http.StatusNotFound, http.StatusConflict), err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"todo": todo, "made": made})
	})
}
