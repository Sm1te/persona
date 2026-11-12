package assistant

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// What a new or resumed Agent runs on and what its tab is called (docs/assistant/model-config.md "Choosing a model for a new Agent"): the
// manager's. The hub asks before it starts one the app asked for (agents.start_params), before a message resumes one
// nothing says the launcher of (agents.resume_params), and for a tab's name (agents.tab_name: an Agent started in the
// background, the Feishu card's); the assistant's own starts (agents_start, a task's agents) come here directly.

// The Agents business's names on the bus (assistantwire/agents.go).
type (
	WorkReport        = assistantwire.WorkReport
	SessionWork       = assistantwire.SessionWork
	AgentStartParams  = assistantwire.AgentStartParams
	AgentResumeParams = assistantwire.AgentResumeParams
	SessionSeen       = assistantwire.SessionSeen
)

const (
	MethodAgentStartParams  = assistantwire.MethodAgentStartParams
	MethodAgentResumeParams = assistantwire.MethodAgentResumeParams
	MethodAgentTabName      = assistantwire.MethodAgentTabName
	EventSessionSeen        = assistantwire.EventSessionSeen
	CodeGone                = assistantwire.CodeGone
)

// AgentStartParams is agents.start_params: the start's params with what the Agent runs on (Model Settings › Agents, over it
// the launcher, model and effort the app picked for this start) and, when asked, its tab's name. A resumed session
// keeps what it runs on (the node knows). A model configuration that cannot run is the app's to hear (bad_request).
func (s *Service) AgentStartParams(ctx context.Context, p AgentStartParams) (map[string]any, error) {
	params := p.Params
	if params == nil {
		params = map[string]any{}
	}
	if resume, _ := params["resume_session_id"].(string); resume == "" {
		own := pickOf(params)
		if err := s.applyStartTier(ctx, params); err != nil {
			return nil, badRequest(err, s.appLang(ctx, p.Lang))
		}
		if err := s.applyModel(ctx, "agent.new", params); err != nil {
			return nil, badRequest(err, s.appLang(ctx, p.Lang)) // a typed model that is no id among them
		}
		if params["launch"] != "engine" {
			return nil, badRequest(fmt.Errorf("purpose %q requires engine launch for session.start", "agent.new"), s.appLang(ctx, p.Lang))
		}
		// Held to what the computer has (#2640): a launcher picked that it lacks is refused, a default it lacks gives
		// way to one it has.
		if m, ok := s.device(ctx, p.MachineID); ok {
			note, _, err := s.fitLauncher(ctx, m, own, params)
			if err != nil {
				return nil, badRequest(err, s.appLang(ctx, p.Lang))
			}
			leaveLauncherNote(ctx, note) // the app says so (agents_bus.go launcher_note, #4661)
		}
		if name, _ := params["launcher"].(string); name != "" {
			if err := s.launcherScopeErr(ctx, p.MachineID, name); err != nil {
				return nil, err // a provider out of this computer's scope (models_launchers_scope.go)
			}
		}
	}
	s.setPlanThen(ctx, p.MachineID, params)
	if name, _ := params["tab_name"].(string); strings.TrimSpace(name) == "" && p.NameFrom != "" {
		if name := s.tabName(ctx, p.NameFrom); name != "" {
			params["tab_name"] = name
		}
	}
	return params, nil
}

// AgentResumeParams is agents.resume_params: a message that resumes an Agent started outside Persona (nothing on its
// machine says its launcher) goes on Model Settings › Agents' launcher and model rather than plain claude, which may not be
// signed in there (node/sendsafe.go resumeLauncher). When that cannot run as a launcher the message goes as it was.
func (s *Service) AgentResumeParams(ctx context.Context, p AgentResumeParams) (map[string]any, error) {
	params := p.Params
	if params == nil {
		params = map[string]any{}
	}
	q := map[string]any{}
	if err := s.applyModel(ctx, "agent.new", q); err != nil {
		s.logf("resume %s: 新建 Agent's launcher not applied: %v", p.SessionID, err)
		return params, nil
	}
	// One the computer lacks gives way to one it has (#2640; a default is never refused).
	if m, ok := s.device(ctx, p.MachineID); ok {
		if _, _, err := s.fitLauncher(ctx, m, startPick{}, q); err != nil {
			s.logf("resume %s: the New Agent launcher not applied: %v", p.SessionID, err)
			return params, nil
		}
	}
	if name, _ := q["launcher"].(string); s.launcherScopeErr(ctx, p.MachineID, name) != nil {
		return params, nil // out of this computer's scope: the message goes as it was
	}
	for _, k := range []string{"launcher", "model", "effort", "model_fingerprint"} {
		if v, ok := q[k]; ok {
			params[k] = v
		}
	}
	return params, nil
}

// tabName is a short name for the herdr tab of a new agent, from its first message (purpose agent.title, a few
// seconds at most). "" when there is no message or the model fails: the node names it agent-<n>.
func (s *Service) tabName(ctx context.Context, prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if r := []rune(prompt); len(r) > 1500 {
		prompt = string(r[:1500])
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Reasoning models think first: 20 tokens came back empty.
	text, err := s.purposeModel("agent.title").Complete(cctx, prompts.RenderTabName(), prompt, 200)
	if err != nil {
		if !ai.IsPaused(err) {
			s.logf("tab name: %v", err)
		}
		return ""
	}
	name := tabSlug(text)
	if name == "" {
		s.logf("tab name: nothing usable in %q", clip(oneLine(text), 80))
	}
	return name
}

// tabSlug keeps what a tab name may be: lowercase ASCII letters and digits, single hyphens, at most 24 characters.
func tabSlug(s string) string {
	if i := strings.IndexByte(strings.TrimSpace(s), '\n'); i > 0 {
		s = strings.TrimSpace(s)[:i]
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ' || r == '/':
			dash = true
		}
		if b.Len() >= 24 {
			break
		}
	}
	return strings.Trim(b.String()[:min(b.Len(), 24)], "-")
}

// defaultTabName: a name nobody chose — the old fixed "persona", herdr's numbers, the agent-<n> fallback.
var defaultTabName = lazyre.New(`^(persona(-\d+)?|\d+|agent-\d+)?$`)

// TabRename is one tab of POST /api/v1/tabs/autoname: renamed, or (dry run) what it would be called.
type TabRename struct {
	MachineID string `json:"machine_id"`
	SessionID string `json:"session_id"`
	Space     string `json:"space"`
	Title     string `json:"title"`
	From      string `json:"from"`
	To        string `json:"to,omitempty"`
	Error     string `json:"error,omitempty"`
}

// autonameTabs renames the herdr tabs of running agents whose names nobody chose (all of them with all) after
// their session's title, the way new tabs are named. One tab per agent; agents without a title keep theirs.
func (s *Service) autonameTabs(ctx context.Context, dryRun, all bool) []TabRename {
	type tab struct {
		machine string
		c       proto.Session
	}
	var todo []tab
	taken := map[string]map[string]bool{} // machine/workspace → names in use
	for _, d := range s.devices(ctx) {
		list, _ := s.sessions(ctx, d.ID)
		seen := map[string]bool{}
		for _, c := range list {
			if c.Assistant || c.SpaceKind != "herdr" || c.TabID == "" || c.Status == proto.StatusOffline || seen[c.TabID] {
				continue
			}
			seen[c.TabID] = true
			ws := d.ID + "/" + c.SpaceID
			if taken[ws] == nil {
				taken[ws] = map[string]bool{}
			}
			taken[ws][c.SlotName] = true
			if (all || defaultTabName.MatchString(c.SlotName)) && strings.TrimSpace(c.Title) != "" {
				todo = append(todo, tab{d.ID, c})
			}
		}
	}
	// Names first (the model, a few at a time), then pick unique ones per space in a fixed order.
	names := make([]string, len(todo))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, t := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if names[i] = s.tabName(ctx, t.c.Title); names[i] == "" { // the small model comes back empty now and then
				names[i] = s.tabName(ctx, t.c.Title)
			}
		}()
	}
	wg.Wait()
	out := make([]TabRename, 0, len(todo))
	for i, t := range todo {
		r := TabRename{MachineID: t.machine, SessionID: t.c.SessionID, Space: t.c.SpaceName, Title: t.c.Title, From: t.c.SlotName}
		if names[i] == "" {
			r.Error = "no name from the model"
			out = append(out, r)
			continue
		}
		ws := taken[t.machine+"/"+t.c.SpaceID]
		delete(ws, t.c.SlotName) // its own old name is free again
		r.To = uniqueName(names[i], ws)
		ws[r.To] = true
		if !dryRun && r.To != r.From {
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if err := s.call(cctx, t.machine, DeviceSessionRename, map[string]any{"session_id": t.c.SessionID, "tab_name": r.To}, nil); err != nil {
				r.Error = err.Error()
			}
			cancel()
		}
		out = append(out, r)
	}
	return out
}

// uniqueName is name, or name-2, name-3… when taken.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", name, i); !taken[n] {
			return n
		}
	}
}
