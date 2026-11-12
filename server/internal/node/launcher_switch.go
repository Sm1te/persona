package node

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// Switching a running Claude Code Agent to another launcher (model provider) of the same engine (#5035,
// docs/integrations/claude-integration.md "Switching a running Agent to another provider"). Claude Code takes its
// gateway, key and model from the environment it starts with, so no command typed into it changes them: the agent is
// quit and the same conversation resumed (`--resume <id>`) through the other launcher, in the same terminal. That
// happens with the user's next message from the chat, not when the provider is picked:
//
//   - session.modes with a launcher other than the session's own checks the pick (planSwitch) and keeps it as the
//     session's pending switch (Session.pending_switch); the session's own launcher drops one that waits
//     (launcherModes). A pick made against a launcher the session no longer runs on (from) is refused as stale.
//     Nothing is quit or typed then.
//   - The next session.send with apply_switch (the user's message from the chat that shows the switch; any other
//     sender's goes on the launcher the agent runs on) carries it out inside its turn (applySwitch). While the agent
//     works, waits for the user, starts or has background work a quit would end, the message goes on its launcher as
//     always and the switch waits for a later one. Otherwise everything is worked out first (the launcher's checks,
//     the conversation's directory, the model to run, how to come back), the agent is looked at once more, and only
//     then quit with Ctrl+C; once only a shell is in front and no claude has the conversation open any more, the
//     launcher's command runs in that shell with --resume, as the user's Resume does in a kept terminal. Then the
//     message is typed to the new agent. Meanwhile the session is listed as starting: messages and the user's Resume
//     wait for it, stop and archive refuse.
//   - A switch that fails is dropped, whatever failed (refused by the checks, an agent that did not quit, a start that
//     did not come up), so nothing tries it again by itself; nothing of the message is typed. A start that did not
//     come up has its pane cleared of what it left (a claude on a sign-in screen) and the agent resumed on its own
//     launcher in the same shell (switch_failed); when even that does not come up, the agent has stopped
//     (switch_failed_stopped): Resume on its terminal page brings it back on its own launcher.
//
// What is refused, and why: only a Claude Code Agent running in a terminal Persona can type into (a Codex or another
// CLI keeps its launcher, a runtime runs its sessions itself, an assistant conversation switches in its own chip), on
// a launcher Persona can tell (one it started it with, or one its process's environment names: coming back needs it);
// a launcher that is on this computer and runs Claude Code; with a model, one that passes --model on (not locked);
// without one, a launcher whose default model can be seen (resumed without --model, Claude Code would go on with the
// model of the launcher it ran on, #2487); and one whose Claude config directory holds the conversation at its latest:
// --resume looks there only, and the sign-in is kept there too, so a launcher of another profile would answer "No
// conversation found", or run on another account. A launcher whose directory cannot be told (a script the probe cannot
// follow, a probe or login shell that failed) is let through: a resume that finds no conversation ends the start, and
// the switch rolls back.
//
// The pending switch is kept in the session's state record (the hub's Postgres): a node restart neither drops it nor
// carries it out without a message. A session Persona did not start has no record and gets none for this (a record
// means "Persona started it", docs/lessons/agents.md): its switch waits in the node's memory, and a node restart
// forgets it, so its row shows no pending switch any more and nothing is switched behind the user's back. A switch
// picked more than switchMaxAge ago is dropped when a message comes.
//
// Time: the checks before the quit take at most switchProbeWait; from the first Ctrl+C on, everything runs on its own
// clock (the caller going away does not leave the agent half switched), at most switchBudget: the quit (about 8 s),
// the shell coming back (switchExitWait), the start on the new launcher (switchStartWait), and after a failed one the
// pane cleared again and the start on its own launcher (the same bounds). The hub waits switchSendWait for a message
// with apply_switch (hub/send.go), longer than both together.

const (
	switchMaxAge    = 24 * time.Hour
	switchProbeWait = time.Minute
	switchExitWait  = 10 * time.Second
	switchStartWait = 90 * time.Second
	switchBudget    = 5 * time.Minute
)

// ownLauncher is the launcher session id runs on, as its row says it, and whether that is known: Persona's record, a
// runtime's row, the one its process's environment names (inferLauncher); else plain claude, as the apps take it, not
// known.
func (n *Node) ownLauncher(id string) (string, bool) {
	if st, ok := n.state.get(id); ok && st.Launcher != "" {
		return st.Launcher, true
	}
	if s, _, listed := n.runtimes.rowOf(id); listed && s.Launcher != "" {
		return s.Launcher, true
	}
	if l := n.inferLauncher(id); l != "" {
		return l, true
	}
	return "claude", false
}

// pendingSwitch is the switch session id waits with, nil when none: its record's, else the node's memory's.
func (n *Node) pendingSwitch(id string) *proto.PendingSwitch {
	if st, ok := n.state.get(id); ok && st.PendingSwitch != nil {
		ps := *st.PendingSwitch
		return &ps
	}
	return n.unrecordedSwitch(id)
}

// unrecordedSwitch is the switch session id waits with in the node's memory (a session with no record), nil when none.
func (n *Node) unrecordedSwitch(id string) *proto.PendingSwitch { return n.switches().pendingOf(id) }

// putPendingSwitch sets session id's pending switch (nil: none) where it is kept, and tells the hub.
func (n *Node) putPendingSwitch(id string, ps *proto.PendingSwitch) {
	if n.state.setPendingSwitch(id, ps) {
		n.switches().setPending(id, nil)
	} else {
		n.switches().setPending(id, ps)
	}
	n.notifySessions()
}

// launcherModes is session.modes with a launcher (from: the launcher the client saw the session on, "" from an older
// client). One other than the session's own is a switch to it: checked, then kept as the session's pending switch
// with the model and effort, the permission mode applied after (handled). The session's own launcher drops a switch
// that waits; handled false then when the request asks for more (a model, mode or effort), which goes on as it would
// without a launcher.
func (n *Node) launcherModes(ctx context.Context, id, launcher, from, model, mode, effort string) (handled bool, err error) {
	launcher, from = strings.TrimSpace(launcher), strings.TrimSpace(from)
	if !validID(id) {
		return true, fmt.Errorf("invalid session id %q", id)
	}
	// What the request changes must not be overwritten by the hub's copy of the record still on its way (#5510).
	if err := n.state.loaded(ctx); err != nil {
		return true, err
	}
	ps := proto.PendingSwitch{Launcher: launcher, Model: strings.TrimSpace(model), Effort: effort}
	// The checks run a login shell, ask the hub for a provider's secrets, read the launcher's models: not while holding
	// up the session's messages.
	checked := false
	if own, _ := n.ownLauncher(id); launcher != own {
		if _, err := n.planSwitch(ctx, id, ps); err != nil {
			return true, err
		}
		checked = true
	}
	// A switch being carried out right now (from the moment a message takes it up) takes minutes: the pick is refused
	// at once rather than held past the hub's wait, and made again against what the agent runs on then.
	if n.restarting(id) {
		return true, refusal(proto.CodeSwitchStale, "the agent is being started again (a launcher switch, a resume): pick again once it runs", nil)
	}
	// One at a time with the messages: one carrying a switch out writes the record anew, and would drop a pick made
	// meanwhile; this one waits for it, and is then taken against the launcher the agent runs on by then.
	unlock := n.typing.lock(id)
	own, _ := n.ownLauncher(id)
	if from != "" && from != own || launcher != own && !checked {
		unlock()
		return true, refusal(proto.CodeSwitchStale, fmt.Sprintf("the session runs on launcher %s now, not %s", own, cmp.Or(from, launcher)), nil)
	}
	if launcher == own {
		if n.pendingSwitch(id) != nil {
			n.putPendingSwitch(id, nil)
			n.logf("session %s: launcher switch cancelled", id)
		}
		unlock()
		return model == "" && mode == "" && effort == "", nil
	}
	before := n.pendingSwitch(id)
	ps.At = time.Now().UTC()
	n.putPendingSwitch(id, &ps)
	unlock()
	n.logf("session %s: switch to launcher %s (model %q, effort %q) waits for the next message", id, launcher, ps.Model, effort)
	if mode == "" {
		return true, nil
	}
	if err := n.setModes(ctx, id, mode, ""); err != nil {
		// The request did not go through: the switch it recorded goes too, the one before it back.
		unlock := n.typing.lock(id)
		if cur := n.pendingSwitch(id); cur != nil && *cur == ps {
			n.putPendingSwitch(id, before)
		}
		unlock()
		return true, err
	}
	return true, nil
}

// switchUnsupported is the refusal of a switch for a session that cannot have one, why in the node's words.
func switchUnsupported(why string) error {
	return refusal(proto.CodeSwitchUnsupported, "only a Claude Code Agent running in a terminal can switch to another launcher: "+why, nil)
}

// switchPlan is what carrying out a switch needs, all of it worked out before the agent is quit.
type switchPlan struct {
	a   host.Agent
	h   host.Host
	ref host.PaneRef
	// cwd is the conversation's directory: the one its transcript's project folder is named after.
	cwd string
	// forward is what the agent starts with on the new launcher: the model picked, else the launcher's default.
	forward modelExecution
}

// planSwitch checks the switch ps of session id and works out how to carry it out: the session (switchSession), the
// launcher it can be told to run on now, the conversation's directory, the launcher's checks, and the model to start
// with when none was picked.
func (n *Node) planSwitch(ctx context.Context, id string, ps proto.PendingSwitch) (*switchPlan, error) {
	if ps.Model != "" && !proto.ValidModelID(ps.Model) {
		return nil, fmt.Errorf("invalid model %q", ps.Model)
	}
	if err := validModes("", ps.Effort); err != nil {
		return nil, err
	}
	a, h, ref, err := n.switchSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, known := n.ownLauncher(id); !known {
		return nil, switchUnsupported("Persona cannot tell which launcher it runs on (it did not start it, and its environment names none), so it could not bring it back on it")
	}
	cwd := n.switchCwd(id, a)
	if cwd == "" {
		return nil, switchUnsupported("Persona cannot tell which folder its conversation belongs to")
	}
	dir, dirKnown, err := n.checkSwitchLauncher(ctx, id, ps.Launcher, ps.Model)
	if err != nil {
		return nil, err
	}
	forward := modelExecution{Model: ps.Model, PlanThen: n.planThen(id)}
	if forward.Model == "" {
		model, ok := n.switchModel(ps.Launcher, cwd, dir, dirKnown)
		if !ok {
			return nil, refusal(proto.CodeSwitchPickModel, fmt.Sprintf("what launcher %s runs by default cannot be seen: pick one of its models", ps.Launcher), nil)
		}
		forward.Model = model
	}
	return &switchPlan{a: a, h: h, ref: ref, cwd: cwd, forward: forward}, nil
}

// switchSession checks that session id is one a switch can restart: a Claude Code Agent running in a pane of a
// terminal host that can say what runs in it and start an agent there. It returns the agent, its host and its pane.
func (n *Node) switchSession(ctx context.Context, id string) (host.Agent, host.Host, host.PaneRef, error) {
	var a host.Agent
	var ref host.PaneRef
	if !validID(id) {
		return a, nil, ref, fmt.Errorf("invalid session id %q", id)
	}
	switch {
	case n.isAssistantSession(id) || n.runtimeAssistant(id):
		return a, nil, ref, switchUnsupported("an assistant conversation switches in its own chip")
	case n.externalCodex(id) || n.codexPath(id) != "" || n.traexPath(id) != "":
		return a, nil, ref, switchUnsupported("this is a Codex session")
	}
	if h, _, known := n.runtimes.belongs(id); h != nil || known {
		return a, nil, ref, switchUnsupported("a runtime runs this session")
	}
	if _, ok := n.terminalOnly(id); ok {
		return a, nil, ref, switchUnsupported("another agent runs in this terminal")
	}
	if _, err := n.paneFor(ctx, id); err != nil {
		return a, nil, ref, n.noPaneForModes(id)
	}
	a, live := n.agent(id)
	if !live {
		return a, nil, ref, n.noPaneForModes(id) // a claude in a terminal Persona does not drive
	}
	if a.Agent != "" && a.Agent != "claude" {
		return a, nil, ref, switchUnsupported(a.Agent + " runs in this terminal")
	}
	kind := host.ParsePane(a.PaneID).Kind
	if _, runtime := engineRuntimes[kind]; runtime || kind == host.Botmux {
		return a, nil, ref, switchUnsupported(kind + " runs this agent itself")
	}
	h, ref, err := n.paneHost(a.PaneID)
	if err != nil {
		return a, nil, ref, switchUnsupported(err.Error())
	}
	if _, ok := h.(host.ForegroundNamer); !ok {
		return a, nil, ref, switchUnsupported(h.Kind() + " cannot say what runs in the agent's terminal")
	}
	launch, err := n.launcherFor(h.Kind())
	if err != nil {
		return a, nil, ref, switchUnsupported(err.Error())
	}
	if lh, ok := launch.(host.Host); !ok || lh.Kind() != h.Kind() {
		return a, nil, ref, switchUnsupported(h.Kind() + " cannot start an agent in the agent's terminal")
	}
	return a, h, ref, nil
}

// switchCwd is the directory session id's conversation belongs to, which `claude --resume` must run in: Claude Code
// finds a conversation by the project folder named after the directory it started in (claudeProjectName). The first of
// the record's directory, the agent's, and the one its transcript names whose name (or its real path's) is the
// transcript's project folder; "" when none is. With no transcript yet (it starts under its id), the record's or the
// agent's.
func (n *Node) switchCwd(id string, a host.Agent) string {
	st, _ := n.state.get(id)
	path := n.transcriptPath(id)
	if path == "" {
		return cmp.Or(st.Cwd, a.Cwd)
	}
	project := filepath.Base(filepath.Dir(path))
	cands := []string{st.Cwd, a.Cwd}
	if fi, err := os.Stat(path); err == nil {
		cands = append(cands, n.meta(path, fi).cwd)
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if claudeProjectName(c) == project {
			return c
		}
		if real, err := filepath.EvalSymlinks(c); err == nil && claudeProjectName(real) == project {
			return c
		}
	}
	return ""
}

// asSwitchError is err, a check's refusal while a message carries a switch out, as a refusal of the switch: one of
// its own (switch_*) as it is; another (a provider's secrets that could not be fetched, a session no longer running)
// as switch_unsupported with its words, so the client keeps the message in the box as for the others.
func asSwitchError(err error) error {
	var re *rpc.Error
	if errors.As(err, &re) && strings.HasPrefix(re.Code, "switch_") {
		return err
	}
	return switchUnsupported(err.Error())
}

// checkSwitchLauncher checks that launcher can resume session id: it is on this computer, runs Claude Code, takes
// model when one is given, and keeps Claude Code's data where the conversation is. It returns that config directory
// and whether it could be told (launcherClaudeDir).
func (n *Node) checkSwitchLauncher(ctx context.Context, id, launcher, model string) (string, bool, error) {
	if !shellcmd.NameRe.MatchString(launcher) {
		return "", false, fmt.Errorf("invalid launcher %q", launcher)
	}
	// A provider whose secrets this computer has not fetched yet: its environment (which may name a config
	// directory) comes with them; the manager refuses one out of this computer's scope.
	if err := n.ensureManaged(ctx, launcher); err != nil {
		return "", false, err
	}
	if !n.knownLauncher(ctx, launcher) {
		return "", false, refusal(proto.CodeSwitchLauncherMissing, fmt.Sprintf("launcher %s is not on this computer", launcher), nil)
	}
	if h, _ := n.startRuntime(launcher); h != nil {
		return "", false, refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s belongs to a runtime, not to Claude Code", launcher), nil)
	}
	if agent := n.launcherModelsAgent(ctx, launcher, ""); agent != "claude" {
		return "", false, refusal(proto.CodeSwitchOtherAgent, fmt.Sprintf("launcher %s runs %s, not Claude Code", launcher, agent), nil)
	}
	if model != "" {
		if _, err := n.launcherModelCheck(ctx, launcher, model); err != nil {
			return "", false, refusal(proto.CodeSwitchModelLocked, fmt.Sprintf("launcher %s would not run model %s: %v", launcher, model, err), nil)
		}
	}
	dir, known := n.launcherClaudeDir(ctx, launcher)
	return dir, known, n.checkSwitchConfigDir(id, launcher, dir, known)
}

// checkSwitchConfigDir refuses launcher for session id when its Claude config directory dir does not hold the
// conversation at its latest (the newest copy, transcriptPath): --resume there would not find it, or would go on from
// an older point. A directory that cannot be told (known false) passes: its start fails safely and the switch rolls
// back. So does a session with no transcript yet (it starts under its id).
func (n *Node) checkSwitchConfigDir(id, launcher, dir string, known bool) error {
	path := n.transcriptPath(id)
	if path == "" {
		return nil
	}
	project := filepath.Dir(path)
	projects := filepath.Dir(project)
	if filepath.Base(projects) != "projects" {
		return nil
	}
	if !known {
		n.logf("session %s: launcher %s: its Claude config directory cannot be told; the switch goes ahead", id, launcher)
		return nil
	}
	mine := filepath.Join(dir, "projects", filepath.Base(project), filepath.Base(path))
	if fileExists(mine) && holdsNewestCopy(mine, path) {
		return nil
	}
	return refusal(proto.CodeSwitchConfigDir, fmt.Sprintf("launcher %s runs Claude Code with the config directory %s, and this conversation is in %s",
		launcher, dir, filepath.Dir(projects)), nil)
}

// loginShellConfigDir is $CLAUDE_CONFIG_DIR of the interactive login shell ("" when it sets none), and whether the
// shell answered: what a launcher that sets no directory of its own runs claude with in a terminal. A variable so tests
// stay shell-free.
var loginShellConfigDir = func(ctx context.Context) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, onboardingShellEnvTimeout)
	defer cancel()
	out, err := loginShell(ctx, `printf '%s\n' "__PERSONA_CONFIG_DIR__$CLAUDE_CONFIG_DIR" </dev/null`).Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if d, ok := strings.CutPrefix(strings.TrimSpace(line), "__PERSONA_CONFIG_DIR__"); ok {
			return d, true
		}
	}
	return "", false
}

// launcherClaudeDir is the Claude config directory a terminal Agent started through launcher runs with: the one the
// launcher sets (a managed provider's environment, the machine's start directory for plain claude, what the probe saw a
// shell launcher export, ~/.claude when it unsets the variable), else the shell's ($CLAUDE_CONFIG_DIR of the login
// shell, else ~/.claude). known false when it cannot be told: a launcher the probe cannot follow or that is a script on
// PATH (read, not run), a login shell that did not answer, a value that is empty or relative. The environment of the
// pane's own shell is not read: it is taken to be the login shell's.
func (n *Node) launcherClaudeDir(ctx context.Context, launcher string) (string, bool) {
	if n.sessionScope != nil {
		return n.sessionScope.claude, true
	}
	shell := func() (string, bool) {
		d, ok := loginShellConfigDir(ctx)
		if !ok {
			return "", false
		}
		if d = strings.TrimSpace(d); d != "" {
			return absConfigDir(d, n.home)
		}
		return filepath.Join(n.home, ".claude"), true
	}
	if l, managed := n.managedLauncher(launcher); managed {
		if d, set := providerEnv(l, "", "", false)["CLAUDE_CONFIG_DIR"]; set {
			return absConfigDir(d, n.home)
		}
		return shell()
	}
	if launcher == "claude" {
		if d := n.claudeStartDir.Load(); d != nil && *d != "" {
			return absConfigDir(*d, n.home)
		}
		return shell()
	}
	p, err := n.launcherProbe(ctx, launcher)
	if err != nil || p.Shell == nil {
		return "", false
	}
	if d, set := p.Env["CLAUDE_CONFIG_DIR"]; set {
		return absConfigDir(d, n.home)
	}
	for _, k := range p.Unset {
		if k == "CLAUDE_CONFIG_DIR" {
			return filepath.Join(n.home, ".claude"), true
		}
	}
	if d := p.Shell["CLAUDE_CONFIG_DIR"]; d != "" {
		return absConfigDir(d, n.home)
	}
	return filepath.Join(n.home, ".claude"), true
}

// absConfigDir is a CLAUDE_CONFIG_DIR value as a directory: ~ expanded; not known when empty or relative.
func absConfigDir(d, home string) (string, bool) {
	d = proto.ExpandHome(strings.TrimSpace(d), home)
	if d == "" || !filepath.IsAbs(d) {
		return "", false
	}
	return filepath.Clean(d), true
}

// switchModel is the model a switch to launcher starts the conversation in cwd with when none was picked: what a new
// session of that launcher runs (resumeModelOf; its settings read in dir, the default one when that cannot be told),
// "" when the launcher names its model itself. ok false when that cannot be seen: a managed provider that names no
// model, a launcher the probe cannot follow.
func (n *Node) switchModel(launcher, cwd, dir string, dirKnown bool) (string, bool) {
	if l, managed := n.managedLauncher(launcher); managed {
		env := providerEnv(l, "", "", false)
		for _, k := range claudeModelVars {
			if env[k] != "" {
				return "", true
			}
		}
		return "", false
	}
	if !dirKnown {
		dir = n.defaultClaudeDir()
	}
	return n.resumeModelOf(launcher, cwd, "", dir)
}

// switchBusy says why a switch of session id waits for a later message, "" when it can go now: its agent works, waits
// for the user or still starts, or has background work that quitting it would end (backgroundWork).
func (n *Node) switchBusy(ctx context.Context, id string) string {
	_ = n.refreshHosts(ctx)
	a, ok := n.agent(id)
	if !ok {
		return ""
	}
	if a.AgentStatus == proto.StatusWorking || a.AgentStatus == proto.StatusBlocked {
		return "the agent is " + a.AgentStatus
	}
	if r := n.starts.get(id); r != nil && r.ended.IsZero() || n.switches().inFlight(id) {
		return "the agent is starting"
	}
	return n.backgroundWork(id)
}

// backgroundWork says what background work session id's agent has going, "" when none: what a quit would end. A
// background task counts from its claude process's start on (its process boundary, ultracode.go), however long ago,
// where the session list leaves out the ones started over a day ago (backgroundTasks); a session whose process is not
// known gets that rule. A transcript whose first read has not finished counts as busy: what runs is not known yet.
func (n *Node) backgroundWork(id string) string {
	p := n.transcriptPath(id)
	if p == "" {
		return ""
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	m := n.meta(p, fi)
	if m.bgWait {
		return "its background work is still being read"
	}
	if c := n.liveBackground(p, n.processSince(id)); c > 0 {
		return fmt.Sprintf("%d background task(s)", c)
	}
	s := proto.Session{SessionID: id}
	n.setBackground(&s, m)
	for _, w := range s.Workflows {
		if w.Running {
			return "a workflow"
		}
	}
	for _, sub := range n.subagents(id) {
		if sub.Running {
			return "a subagent"
		}
	}
	if m.loop != nil {
		return "a /loop"
	}
	return ""
}

// processSince is when session id's claude process started as far as the node saw it (its ultracode process
// boundary: a start or resume of Persona's, a SessionStart hook), zero when it did not.
func (n *Node) processSince(id string) time.Time {
	n.mu.Lock()
	var since time.Time
	if m, ok := n.modes[id]; ok && m.Ultracode != nil {
		since = m.Ultracode.Since
	}
	n.mu.Unlock()
	if since.IsZero() {
		if rec, ok := n.state.ultracode(id); ok {
			since = rec.Since
		}
	}
	return since
}

// liveBackground counts the background commands and monitors path's transcript has going (its last scan, brought up
// to date): those started since its process did (since), or within a day when that is not known; a Monitor past its
// expiry has stopped. Background subagents and workflows are counted elsewhere (backgroundWork).
func (n *Node) liveBackground(path string, since time.Time) int {
	if _, _, _, pending := n.backgroundTasks(path); pending {
		return 1
	}
	n.mu.Lock()
	sc := n.bgScans[path]
	n.mu.Unlock()
	if sc == nil {
		return 0
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	count := 0
	for _, t := range sc.meta.Background {
		switch {
		case t.Kind == "agent" || t.Kind == "workflow":
		case t.ExpiresAt != nil && !time.Now().Before(*t.ExpiresAt):
		case since.IsZero() && time.Since(t.StartedAt) >= 24*time.Hour:
		case !since.IsZero() && t.StartedAt.Before(since):
		default:
			count++
		}
	}
	return count
}

// ranExecution is what session id runs now on its own launcher, to bring it back with after a failed switch: the model
// last switched to from the app (model_switch.go's /model, kept for the node's lifetime), else the record's (prev; ""
// lets --resume bring back the model the conversation last ran), and the mode a plan goes on in. Not the transcript's
// model: a gateway may answer under another name than the id it takes.
func (n *Node) ranExecution(id string, prev sessionState) modelExecution {
	ex := modelExecution{Model: prev.Model, Fingerprint: prev.ModelFingerprint, PlanThen: prev.PlanThen}
	n.mu.Lock()
	var picked string
	if m, ok := n.modes[id]; ok && m.Model != nil {
		picked = m.Model.Model
	}
	n.mu.Unlock()
	if picked != "" && picked != prev.Model && proto.ValidModelID(picked) {
		ex.Model, ex.Fingerprint = picked, ""
	}
	return ex
}

// markSwitching marks session id as having a switch carried out by a message, from before its checks until it ends
// (the returned func): restarting says so.
func (n *Node) markSwitching(id string) (done func()) { return n.switches().mark(id) }

// restarting: session id's agent is being started again right now, by a message carrying out a launcher switch (from
// its checks on, on this connection's Node or the last one's) or a start listed as starting: a pick or a stop is
// refused rather than held for it.
func (n *Node) restarting(id string) bool {
	if n.switches().busy(id) {
		return true
	}
	r := n.starts.get(id)
	return r != nil && r.ended.IsZero()
}

// cdThen is cmd typed after changing to dir, for a shell among shells: `cd <dir> && cmd`, and `cd <dir>; cmd` in nu
// and elvish, which have no &&.
func cdThen(dir, cmd string, shells ...string) string {
	sep := " && "
	for _, s := range shells {
		switch strings.TrimPrefix(filepath.Base(s), "-") {
		case "nu", "elvish":
			sep = "; "
		}
	}
	return "cd " + shellQuote(dir) + sep + cmd
}

// cdFirstKey marks a start that carries a switch out in a shell that is not in the conversation's folder: its typed
// command changes there first (start, cdThen).
type cdFirstKey struct{}

func withCdFirst(ctx context.Context) context.Context {
	return context.WithValue(ctx, cdFirstKey{}, true)
}

func cdFirst(ctx context.Context) bool { v, _ := ctx.Value(cdFirstKey{}).(bool); return v }

// awaitSwitch waits, before the user's Resume of session id, for a message carrying out a launcher switch right now:
// it quits the agent and starts it again in its terminal, listed as starting (applySwitch), maybe on the last
// connection's Node. After it the Resume finds the agent running, or resumes it as it is then. A start of the session
// in the background is waited for as well.
func (n *Node) awaitSwitch(ctx context.Context, id string) {
	if !validID(id) {
		return
	}
	_ = n.starts.wait(ctx, id)
	_ = n.switches().wait(ctx, id)
	n.typing.lock(id)() // the message's turn, whose switch registers only once it is under way
}

// applySwitch carries out session id's pending switch ps before the user's message is typed, inside send's turn, and
// returns the pane the message goes to, and whether the switch was carried out. A switch that waits (a busy agent)
// or no longer applies (too old, the launcher the agent runs on by now) leaves the message to go as always; one that
// fails is dropped and refuses the message with nothing typed.
func (n *Node) applySwitch(ctx context.Context, id, pane string, ps proto.PendingSwitch) (string, bool, error) {
	own, _ := n.ownLauncher(id)
	if ps.Launcher == own || time.Since(ps.At) > switchMaxAge {
		n.logf("session %s: switch to launcher %s picked %s dropped: on it already, or picked too long ago", id, ps.Launcher, ps.At.Format(time.RFC3339))
		n.putPendingSwitch(id, nil)
		return pane, false, nil
	}
	if why := n.switchBusy(ctx, id); why != "" {
		n.logf("session %s: switch to launcher %s waits (%s); the message goes on %s", id, ps.Launcher, why, own)
		return pane, false, nil
	}
	// From here until it ends, picks and stops are refused at once (restarting): the checks below take up to a minute
	// before the session is listed as starting.
	defer n.markSwitching(id)()
	pctx, cancel := context.WithTimeout(ctx, switchProbeWait)
	plan, err := n.planSwitch(pctx, id, ps)
	var backErr error // why the agent could not be brought back on its own launcher
	if err == nil {
		_, _, backErr = n.checkSwitchLauncher(pctx, id, own, "")
	}
	cancel()
	if err != nil {
		n.logf("session %s: switch to launcher %s dropped: %v", id, ps.Launcher, err)
		n.putPendingSwitch(id, nil)
		return "", false, asSwitchError(err)
	}
	prev, had := n.state.get(id)
	r := n.resumeWith(id, sendOpts{}) // its modes
	back := n.ranExecution(id, prev)
	// The last look before the first Ctrl+C: the checks took a while, and a turn may have started (a background task
	// reporting back, a wakeup), a dialog come up or someone typed at the computer meanwhile.
	if why := n.switchBusy(ctx, id); why != "" {
		n.logf("session %s: switch to launcher %s waits (%s); the message goes on %s", id, ps.Launcher, why, own)
		return pane, false, nil
	}
	if err := n.checkPaneFor(ctx, pane, "claude", sendOpts{}); err != nil {
		return "", false, err // as for any message: nothing done, the switch waits
	}
	row := proto.Session{SessionID: id, Cwd: plan.cwd, Launcher: ps.Launcher, Agent: "claude", Status: proto.StatusIdle,
		Starting: true, StartedByPersona: had, UpdatedAt: time.Now().UTC(), WorkspaceID: plan.a.WorkspaceID, PendingSwitch: &ps}
	if !n.switches().begin(id, row) {
		n.logf("session %s: switch to launcher %s refused: another one is under way", id, ps.Launcher)
		return "", false, switchUnderWay() // not typed into a pane that one is quitting and starting again (whether or not it ended since)
	}
	var serving *Node
	defer func() {
		// last: what the switch wrote is written by then, here or by the Node serving now (the next connection's),
		// whose list then shows the row on its launcher again
		if serving = n.switches().end(id); serving != nil && serving != n {
			serving.notifySessions()
		}
		n.notifySessions()
	}()
	// From the first Ctrl+C on, on its own clock: a caller that goes away (a hub redeploy ends the connection) must
	// not leave the agent half switched.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), switchBudget)
	defer cancel()
	newPane, err := n.carryOut(wctx, id, own, ps, plan, r.mode, cmp.Or(ps.Effort, r.effort), r.effort, back, backErr, prev, had)
	if err != nil {
		n.putPendingSwitch(id, nil) // dropped: nothing tries it again by itself
		return "", false, err
	}
	n.switched(id, ps)
	if to := n.switches().serving(); to != nil && to != n { // what that Node's list shows meanwhile
		to.noteModes(id, r.mode, cmp.Or(ps.Effort, r.effort))
		to.noteSwitchedModel(id, ps)
	}
	n.logf("session %s: now on launcher %s", id, ps.Launcher)
	return newPane, true, nil
}

// carryOut quits session id's agent and starts it on ps's launcher in the same shell, as plan says; after a start that
// did not come up it clears the pane and resumes the agent on its own launcher (back, with backEffort; not when
// backErr says it cannot be), putting the record back as it was (prev, or dropping the one the start made when there
// was none: then the one the start made, naming its own launcher). It returns the pane the agent runs in, or the
// refusal that says how it ended.
func (n *Node) carryOut(ctx context.Context, id, own string, ps proto.PendingSwitch, plan *switchPlan, mode, effort, backEffort string,
	back modelExecution, backErr error, prev sessionState, had bool) (string, error) {
	a, h, ref := plan.a, plan.h, plan.ref
	n.logf("session %s: switching from launcher %s to %s (model %q) in %s", id, own, ps.Launcher, plan.forward.Model, a.PaneID)
	if quit, err := n.quitAgent(ctx, id, a.PaneID); err != nil || !quit {
		return "", refusal(proto.CodeSwitchNotStopped, fmt.Sprintf("the agent did not quit (%v); it runs on %s as before", err, own), nil)
	}
	shells, dir, err := n.shellAfterQuit(ctx, id, a, h, ref)
	if err != nil {
		n.logf("session %s: switch: %v", id, err)
		return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("the agent quit, but its terminal did not come back to a shell prompt: %v", err), nil)
	}
	start := func(launcher, effort string, execution modelExecution, shells []string, dir string) (*proto.Session, error) {
		sctx := withAdoptedPane(withStartHost(ctx, h.Kind()), &host.Created{SlotID: a.TabID, SpaceID: a.WorkspaceID, Pane: ref}, shells)
		if dir == "" || filepath.Clean(dir) != filepath.Clean(plan.cwd) {
			sctx = withCdFirst(sctx) // the shell is elsewhere: --resume finds the conversation by its folder
		}
		sctx, cancel := context.WithTimeout(sctx, switchStartWait)
		defer cancel()
		return n.start(sctx, plan.cwd, launcher, "", id, a.WorkspaceID, mode, effort, "", "", "claude", execution)
	}
	s, startErr := start(ps.Launcher, effort, plan.forward, shells, dir)
	if startErr == nil {
		return s.PaneID, nil
	}
	n.logf("session %s: the agent did not come up on launcher %s: %v", id, ps.Launcher, startErr)
	n.state.restoreLaunch(id, prev, had, own) // what the start wrote over: a Resume brings back the launcher it ran on
	err = backErr
	if err == nil {
		// What the start left in front (a claude stopped on a sign-in screen of the new launcher's directory) goes first.
		if shells, dir, err = n.clearPane(ctx, id, a, h, ref); err == nil {
			_, err = start(own, backEffort, back, shells, dir)
			n.state.restoreLaunch(id, prev, had, own) // the start wrote a new record, whether it came up or not
		}
	}
	if err != nil {
		// A session Persona did not start keeps the record its starts made, naming its own launcher: what a Resume
		// brings it back on, now that no process of it says which launcher it ran on.
		n.logf("session %s: could not bring the agent back on launcher %s: %v", id, own, err)
		return "", refusal(proto.CodeSwitchFailedStopped, fmt.Sprintf("the agent did not come up on launcher %s (%v), nor again on %s (%v); it has stopped",
			ps.Launcher, startErr, own, err), nil)
	}
	n.logf("session %s: back on launcher %s", id, own)
	return "", refusal(proto.CodeSwitchFailed, fmt.Sprintf("the agent did not come up on launcher %s (%v); it is back on %s", ps.Launcher, startErr, own), nil)
}

// switched clears what was kept for session id's switch to ps once its agent runs on the new launcher: the switch (the
// start's new record still has the one it was carried over with), and the model picked on the old launcher, which the
// list showed until a reply.
func (n *Node) switched(id string, ps proto.PendingSwitch) {
	n.state.setPendingSwitch(id, nil)
	n.switches().setPending(id, nil)
	n.noteSwitchedModel(id, ps)
	n.notifySessions()
}

// noteSwitchedModel is what the list shows of session id's model once its switch to ps is done: the model picked, until
// a reply names one; not a model picked on the old launcher.
func (n *Node) noteSwitchedModel(id string, ps proto.PendingSwitch) {
	n.mu.Lock()
	if m, ok := n.modes[id]; ok {
		m.Model = nil
		n.modes[id] = m
	}
	n.mu.Unlock()
	if ps.Model != "" {
		n.noteModel(id, ps.Model, n.transcriptModel(id)) // shown until a reply names the model
	}
}

// switchingRefusal refuses what would type into session id's pane or press its keys while a launcher switch of it is
// carried out, on this connection's Node or the last one's (switch_share.go), whose typing lock this Node's does not
// hold: the agent is being quit and started again there. nil when none is.
func (n *Node) switchingRefusal(id string) error {
	if !n.switches().busy(id) {
		return nil
	}
	return switchUnderWay()
}

// switchUnderWay is the refusal of something that would type into a pane a launcher switch is quitting and starting again.
func switchUnderWay() error {
	return refusal(proto.CodeSwitchUnderWay, "the agent is moving to another launcher right now: try again once it runs", nil)
}

// shellAfterQuit waits, once session id's agent left its pane a, until only shells are in front of the pane and no
// claude has the conversation open any more (Claude Code writes into its transcript as it exits), and returns those
// shells and the pane's directory as the host lists it now ("" when it does not say).
func (n *Node) shellAfterQuit(ctx context.Context, id string, a host.Agent, h host.Host, ref host.PaneRef) ([]string, string, error) {
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return nil, "", fmt.Errorf("%s cannot say what runs in %s", h.Kind(), a.PaneID)
	}
	deadline := time.Now().Add(switchExitWait)
	for {
		names, err := f.PaneForeground(ctx, ref)
		switch {
		case err != nil:
		case len(names) == 0 || !allShells(names):
			err = fmt.Errorf("%q runs in front of %s", names, a.PaneID)
		case n.openElsewhereRefusal(id) != nil:
			err = errors.New("a claude still has the conversation open")
		default:
			dir := ""
			if p, err := n.listedPane(ctx, a.PaneID); err == nil {
				dir = paneDir(p)
			}
			return names, dir, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, "", err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// clearPane brings the shell back in front of pane a after a start that did not come up there: a claude it left
// running (stopped on a sign-in or another screen of its own) is quit with Ctrl+C, as quitAgent does, then as
// shellAfterQuit.
func (n *Node) clearPane(ctx context.Context, id string, a host.Agent, h host.Host, ref host.PaneRef) ([]string, string, error) {
	f, ok := h.(host.ForegroundNamer)
	if !ok {
		return nil, "", fmt.Errorf("%s cannot say what runs in %s", h.Kind(), a.PaneID)
	}
	atShell := func() bool {
		names, err := f.PaneForeground(ctx, ref)
		return err == nil && len(names) > 0 && allShells(names)
	}
	for round := 0; round < stopRounds && !atShell(); round++ {
		_ = n.sendKeys(ctx, a.PaneID, "ctrl+c")
		time.Sleep(200 * time.Millisecond)
		_ = n.sendKeys(ctx, a.PaneID, "ctrl+c")
		for deadline := time.Now().Add(stopWait); time.Now().Before(deadline) && ctx.Err() == nil && !atShell(); {
			time.Sleep(250 * time.Millisecond)
		}
	}
	_ = n.refreshHosts(ctx)
	return n.shellAfterQuit(ctx, id, a, h, ref)
}
