package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The Agents business is the manager's (internal/assistant, agents_*.go; docs/assistant/agent-work.md "Responsibilities"): the triage of
// an agent's turn, "Continue" after a transient model error, the wakes, the cards it opens and their answers, its switches,
// state and history; the tab names of new agents; what a new or resumed agent runs on. The hub carries it: it tells
// the manager what happened (session.turn_started / turn_ended, session.seen), shows what it reports (the snapshot's
// work on the session lists, its cards with the other interactions) and asks it to fill in a start
// (agents.start_params, agents.resume_params, agents.tab_name). While the manager is not connected the lists keep
// what it reported last (a long overdue wake no longer counts as one) and answers to its cards wait for it
// (heldActs); a start right after the hub started waits for it to connect, and without it there is no start at all:
// the computers are the manager's to reach (devices_link.go), so it is answered 503 assistant_offline.

// AgentsConfig is the manager's "agents" defaults (Hello.Agents): its manager.json's, not the hub's.
type AgentsConfig = assistantwire.AgentsConfig

// workCardTool is the tool_name of the manager's cards about an agent's turn.
const workCardTool = assistantwire.WorkCardTool

// agentWork is whether the manager is there to wait for (waitManager).
type agentWork struct {
	mu sync.RWMutex

	everUp    bool      // it connected since the hub started
	firstWait time.Time // when a start first waited for it (never connected yet)
}

// ---- telling the manager, asking it ----

// Waiting for the manager: a hub that just started takes requests (queued on its socket, the apps' replays) before its
// child has shaken hands, and one that restarts is back in a second or two. A start or a resume waits for it this
// long. Once it connected, a manager that is gone is not waited for: what follows needs it anyway (the machines are
// its), and is answered 503 assistant_offline at once.
var managerStartWait = 60 * time.Second // after the hub started, for a manager that never connected

// waitManager waits, within ctx (less a few seconds for the call that follows), for the manager to be connected,
// only while it has not connected since the hub started; false when it is not.
func (h *Hub) waitManager(ctx context.Context) bool { return h.waitManagerOf(ctx, h.defaultOwner(ctx)) }

// waitManagerOf is waitManager for manager id.
func (h *Hub) waitManagerOf(ctx context.Context, id string) bool {
	if h.busConnOf(id) != nil {
		return true
	}
	aw := &h.agentWork
	now := time.Now()
	aw.mu.Lock()
	if aw.everUp {
		aw.mu.Unlock()
		return false
	}
	if aw.firstWait.IsZero() {
		aw.firstWait = now
	}
	until := aw.firstWait.Add(managerStartWait)
	aw.mu.Unlock()
	if d, ok := ctx.Deadline(); ok && d.Add(-5*time.Second).Before(until) {
		until = d.Add(-5 * time.Second)
	}
	for time.Now().Before(until) && ctx.Err() == nil {
		time.Sleep(100 * time.Millisecond)
		if h.busConnOf(id) != nil {
			return true
		}
	}
	return h.busConnOf(id) != nil
}

// managerConnected: the manager's handshake went through. A build other than the hub's may not split the Agents
// business the same way (both triage, or neither): said in the log.
func (h *Hub) managerConnected() {
	aw := &h.agentWork
	aw.mu.Lock()
	aw.everUp = true
	aw.mu.Unlock()
	if c := h.busConnOf(assistantwire.AssistantID); c != nil && nodeMismatch(h.version, c.presence.Version) {
		h.logf("assistant: its build %q is not the hub's %q: the Agents triage may run in both or in neither", c.presence.Version, h.version)
	}
}

// agentStartParams has the manager fill in params, an Agent the app starts on m (POST /api/v1/sessions): what it runs
// on (Models › Agents, the launcher / model / effort picked for this start over it) and, from nameFrom when it is not
// "", its tab's name. An error is the start's refusal (*startRefused, the app's to see: 400), errManagerOffline (the
// manager is not connected after waiting for it while the hub starts, and without it nothing reaches the machine:
// 503), or the manager's failure to answer (502). note is the manager's word that a default the computer lacks gave
// way to another launcher (#4661), for the app.
func (h *Hub) agentStartParams(ctx context.Context, m *Machine, params map[string]any, nameFrom string) (note string, err error) {
	var res struct {
		Params       map[string]any `json:"params"`
		LauncherNote string         `json:"launcher_note"`
	}
	err = errBusOffline
	if owner := h.machineOwner(m.cfg.ID); h.waitManagerOf(ctx, owner) {
		err = h.asstOf(owner).call(ctx, assistantwire.MethodAgentStartParams, assistantwire.AgentStartParams{MachineID: m.cfg.ID,
			Params: params, NameFrom: nameFrom, Lang: string(h.contextLang(ctx))}, &res)
	}
	var re *rpc.Error
	switch {
	case err == nil:
		if res.Params != nil {
			clear(params)
			for k, v := range res.Params {
				params[k] = v
			}
		}
		return res.LauncherNote, nil
	case errors.As(err, &re) && re.Code == assistantwire.CodeBadRequest:
		return "", &startRefused{re.Message}
	case errors.Is(err, errBusOffline):
		return "", fmt.Errorf("%s: %w", m.cfg.ID, errManagerOffline)
	}
	return "", err
}

// startRefused is the manager's refusal of a start (the request itself is wrong: 400).
type startRefused struct{ msg string }

func (e *startRefused) Error() string { return e.msg }

// applyResumeLauncher adds "New agent"'s launcher and model to p, a message that resumes sid on m (the manager's
// agents.resume_params: asked with no params, what it answers goes over p's). Without the manager nothing is added:
// the send that follows goes through it too and is answered 503 assistant_offline.
func (h *Hub) applyResumeLauncher(ctx context.Context, m *Machine, sid string, p map[string]any) {
	var res struct {
		Params map[string]any `json:"params"`
	}
	err := errBusOffline
	if owner := h.sessionOwner(m.cfg.ID, sid); h.waitManagerOf(ctx, owner) {
		err = h.asstOf(owner).call(ctx, assistantwire.MethodAgentResumeParams, assistantwire.AgentResumeParams{MachineID: m.cfg.ID,
			SessionID: sid, Params: map[string]any{}}, &res)
	}
	if err != nil {
		h.logf("resume %s: the manager did not say what it runs on: %v", sid, err)
		return
	}
	for _, k := range []string{"launcher", "model", "effort", "model_fingerprint"} {
		if v, ok := res.Params[k]; ok {
			p[k] = v
		}
	}
}

// autoTabName is a short name for the herdr tab of a new agent, from its first message (the manager's
// agents.tab_name). "" when there is no message, or no name came: the node names it agent-<n>.
func (h *Hub) autoTabName(ctx context.Context, prompt string) string {
	var res struct {
		Name string `json:"name"`
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if !h.waitManager(cctx) {
		return ""
	}
	if err := h.asst().call(cctx, assistantwire.MethodAgentTabName, map[string]any{"prompt": prompt}, &res); err != nil {
		h.logf("tab name: %v", err)
		return ""
	}
	return res.Name
}

// startSettleWait is how long the hub watches a start the node answered before it ran (async) for how it went: longer
// than a start may take on the node (node/claudeready.go startWait), within the time a failed one stays listed with its
// error (node/starting.go startFailedFor). startSettlePoll is how often it looks.
const (
	startSettleWait = 10 * time.Minute
	startSettlePoll = 250 * time.Millisecond
)

// modelUsedOnceStarted is modelUsed for a start the node answered before it ran (async, node/starting.go): the
// launcher's model check (a launcher that fixes its own --model, an agent that takes none) and the agent coming up
// happen after the answer. The model is told once the session is listed as started, and not when its start failed
// (listed with its error) or it never showed in time.
func (h *Hub) modelUsedOnceStarted(machine, sid, launcher, model string) {
	for end := time.Now().Add(startSettleWait); time.Now().Before(end); time.Sleep(startSettlePoll) {
		var s proto.Session
		ok := false
		if m := h.localMachine(machine); m != nil { // looked up each time: the machine may reconnect meanwhile
			s, ok = m.session(sid)
		}
		switch {
		case !ok || s.Starting:
			continue // not listed yet, or still starting
		case s.StartError != "":
			return
		}
		h.modelUsed(h.machineOwner(machine), machine, launcher, model)
		return
	}
}

// modelUsed tells the manager of machine that model, picked in an app, ran there with launcher (models.used, #1644):
// a start, a running session's switch or a message to an assistant conversation that went through with it. The
// manager remembers an id the launcher's own /model list does not have and offers it there again. Best effort: a
// manager that is away misses it, an older one ignores it.
func (h *Hub) modelUsed(owner, machine, launcher, model string) {
	model = strings.TrimSpace(model)
	if launcher == "" || model == "" {
		return
	}
	err := h.notifyBus(owner, assistantwire.EventModelUsed, assistantwire.ModelUsed{MachineID: machine, Launcher: launcher, Model: model})
	if err != nil && !errors.Is(err, errBusOffline) {
		h.logf("model used %s on %s: %v", launcher, machine, err)
	}
}
