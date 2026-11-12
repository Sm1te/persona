package node

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// What a runtime that is a machine's engine needs beyond listing and driving its sessions (Paseo, docs/integrations/paseo.md):
// several launchers (one per provider it runs), the form's choices passed on when a session starts, the phone's
// permission mode and effort (caps.modes, runtimes.go), and — as the engine — every new session and every session to
// continue on this machine.

// runtimeLauncher is one of a runtime's entries for the new-session form (runtime.hello's launchers).
type runtimeLauncher struct {
	Name string `json:"name"`
	// Model is shown next to it (the provider's default model).
	Model string `json:"model,omitempty"`
	// Agent is what it starts: "claude" for Claude Code (the form's Claude choices, the apps' Claude controls), else
	// the runtime's own (or another the apps know).
	Agent string `json:"agent,omitempty"`
	// Models are its models for the form (launchers.models), the default marked.
	Models []proto.AgentModel `json:"models,omitempty"`
}

// Bounds on a runtime's launchers.
const (
	maxRuntimeLaunchers = 64
	maxLauncherModels   = 200
	maxLauncherText     = 200
)

// cleanLaunchers keeps the launchers a runtime may offer: names a launcher may have (shellcmd.NameRe, the shell's
// rule), each once, agents that are ids, text clipped, the lists bounded.
func cleanLaunchers(list []runtimeLauncher) []runtimeLauncher {
	clip := func(s string) string {
		if r := []rune(strings.TrimSpace(s)); len(r) > maxLauncherText {
			return string(r[:maxLauncherText])
		}
		return strings.TrimSpace(s)
	}
	out := make([]runtimeLauncher, 0, len(list))
	seen := map[string]bool{}
	for _, l := range list {
		if len(out) == maxRuntimeLaunchers {
			break
		}
		if !shellcmd.NameRe.MatchString(l.Name) || seen[l.Name] || l.Agent != "" && !runtimeIDRe.MatchString(l.Agent) {
			continue
		}
		seen[l.Name] = true
		l.Model = clip(l.Model)
		models := make([]proto.AgentModel, 0, min(len(l.Models), maxLauncherModels))
		for _, m := range l.Models {
			if len(models) == maxLauncherModels {
				break
			}
			if m.ID = clip(m.ID); m.ID == "" {
				continue
			}
			m.Name, m.Description, m.Resolved, m.DefaultEffort = clip(m.Name), clip(m.Description), clip(m.Resolved), clip(m.DefaultEffort)
			efforts := m.Efforts[:0:0]
			for _, e := range m.Efforts {
				if validModes("", e) == nil {
					efforts = append(efforts, e)
				}
			}
			m.Efforts = efforts
			models = append(models, m)
		}
		l.Models = models
		out = append(out, l)
	}
	return out
}

// launcherList is every launcher h offers: hello's launcher, then its launchers.
func (h *runtimeConn) launcherList() []runtimeLauncher {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []runtimeLauncher
	if h.hello.Launcher != "" {
		entry := runtimeLauncher{Name: h.hello.Launcher, Model: h.hello.LauncherModel}
		for _, l := range h.hello.Launchers {
			if l.Name == h.hello.Launcher {
				if l.Model != "" && entry.Model == "" {
					entry.Model = l.Model
				}
				entry.Agent = l.Agent
				entry.Models = l.Models
				break
			}
		}
		out = append(out, entry)
	}
	for _, l := range h.hello.Launchers {
		if l.Name != h.hello.Launcher {
			out = append(out, l)
		}
	}
	return out
}

// launcherNamed is h's launcher of that name.
func (h *runtimeConn) launcherNamed(name string) (runtimeLauncher, bool) {
	for _, l := range h.launcherList() {
		if l.Name == name {
			return l, true
		}
	}
	return runtimeLauncher{}, false
}

// setLaunchers replaces h's launchers (runtime.launchers: a runtime whose providers changed, or came in after hello).
func (h *runtimeConn) setLaunchers(list []runtimeLauncher) {
	h.mu.Lock()
	h.hello.Launchers = list
	h.mu.Unlock()
}

// runtimeLauncherModels is launchers.models for a runtime's launcher: the models it said (none: an empty list, the
// form offers no choice); false for a launcher no runtime runs (runtimeOf).
func (n *Node) runtimeLauncherModels(name string) (proto.LauncherModels, bool) {
	return launcherModelsOf(n.runtimeOf(name), name)
}

// agyRuntimeModels is the Antigravity runtime's own launcher models, which a managed launcher named agy does not hide:
// what a managed Antigravity launcher starts (startRuntime) and offers.
func (n *Node) agyRuntimeModels() (proto.LauncherModels, bool) {
	return launcherModelsOf(n.runtimes.launcherRuntime("agy"), "agy")
}

// launcherModelsOf is runtime h's advertisement for its launcher name; false without a runtime.
func launcherModelsOf(h *runtimeConn, name string) (proto.LauncherModels, bool) {
	if h == nil {
		return proto.LauncherModels{}, false
	}
	l, _ := h.launcherNamed(name)
	agent := l.Agent
	if agent == "" {
		agent = h.agent()
	}
	models := l.Models
	if models == nil {
		models = []proto.AgentModel{}
	}
	return proto.LauncherModels{Agent: agent, Models: models}, true
}

// runtimeStartParams are what session.start passes to a runtime.
type runtimeStartParams struct {
	Cwd, Prompt, Launcher         string
	Model, PermissionMode, Effort string
	WorkspaceID                   string // a space of the runtime's to start in (one of its sessions' space_id)
	Resume                        string // the Claude session to take over and continue (caps.resume)
	Shell                         bool   // Launcher is a login shell's launcher: the runtime picks its own of that name
	OpenElsewhere                 bool   // a running claude has Resume open: only a runtime running it may take it
}

// engineRuntimes maps the engines that are a runtime to that runtime's id (engine.go).
var engineRuntimes = map[string]string{
	proto.EnginePaseo: proto.EnginePaseo,
	proto.EngineCindy: proto.EngineCindy,
}

// engineRuntime is the runtime that is the host of a start in ctx (Paseo: the one the start named, else the machine's
// default host), when it is connected and starts sessions; nil when that host is a terminal (herdr, Orca…).
func (n *Node) engineRuntime(ctx context.Context) *runtimeConn {
	id, ok := engineRuntimes[n.startKind(ctx)]
	if !ok {
		return nil
	}
	n.runtimes.mu.Lock()
	h := n.runtimes.byRuntime(id)
	n.runtimes.mu.Unlock()
	if h == nil {
		return nil
	}
	h.mu.Lock()
	start := h.hello.Caps.Start
	h.mu.Unlock()
	if !start {
		return nil
	}
	return h
}

// engineRuntimeAway is why a session cannot start or continue on a machine whose engine is a runtime that is not
// connected (Paseo not running, or its bridge not up yet). Nothing was started or sent.
func (n *Node) engineRuntimeAway(ctx context.Context) error {
	name := proto.EngineNames[n.startKind(ctx)]
	return refusal(engineMissing, fmt.Sprintf("这台电脑的引擎是 %s，但 %s 现在没有连上 Persona（没在运行，或刚启动还没连上），不能在这里新开或接着会话，这次没有发送。"+
		"在电脑上打开 %s 后再试。", name, name, name), nil)
}

// startOnEngineRuntime starts a session on the machine's engine runtime: a launcher of the login shell's, or a session
// to continue (resume), go there too — on such a machine there is no terminal to start them in.
func (n *Node) startOnEngineRuntime(ctx context.Context, p runtimeStartParams) (*proto.Session, error) {
	h := n.engineRuntime(ctx)
	if h == nil {
		return nil, n.engineRuntimeAway(ctx)
	}
	if p.Resume != "" {
		return n.takeOverInRuntime(ctx, h, p)
	}
	// The engine runs every start with a provider of its own: one it has by the launcher's name (a managed launcher made
	// from it carries the same configuration), else its default (shell_launcher). A managed launcher's own settings
	// apply only where personad launch runs it (runtimeStart), never inside the engine.
	if _, own := h.launcherNamed(p.Launcher); !own && p.Launcher != "" {
		p.Shell = true
	}
	return n.runtimeStartOn(ctx, h, p)
}

// takeOverInRuntime has the engine runtime take a Claude session over (session.start with resume): it continues it
// under the same id, its transcript the conversation. Refused when the runtime cannot (no caps.resume) or a running
// claude has the session open elsewhere (a second writer of the transcript).
func (n *Node) takeOverInRuntime(ctx context.Context, h *runtimeConn, p runtimeStartParams) (*proto.Session, error) {
	h.mu.Lock()
	resume := h.hello.Caps.Resume
	h.mu.Unlock()
	if !resume {
		return nil, refusal(notRunning, fmt.Sprintf("%s 不能接着这台电脑上没在运行的会话，这次没有发送。", h.label()), nil)
	}
	// A running claude has it open: the runtime's own (an agent of its it does not list) is taken as it is; anyone
	// else's would get a second writer, and the runtime refuses (open_elsewhere): the refusal says where it runs.
	elsewhere := n.openElsewhereRefusal(p.Resume)
	p.OpenElsewhere = elsewhere != nil
	if p.Cwd == "" {
		st, _ := n.state.get(p.Resume)
		p.Cwd = st.Cwd
	}
	if p.Cwd == "" {
		if path := n.transcriptPath(p.Resume); path != "" {
			if fi, err := os.Stat(path); err == nil {
				p.Cwd = n.meta(path, fi).cwd
			}
		}
	}
	if p.PermissionMode == "" && p.Effort == "" {
		p.PermissionMode, p.Effort = n.modesOf(p.Resume)
	}
	s, err := n.runtimeStartOn(ctx, h, p)
	if elsewhere != nil && rpcCode(err) == openElsewhere {
		return nil, elsewhere
	}
	if err != nil {
		return nil, err
	}
	if s.SessionID != p.Resume {
		return nil, fmt.Errorf("%s took session %s over as %s", h.label(), p.Resume, s.SessionID)
	}
	return s, nil
}

// continueInRuntime is a message to a Claude session not running anywhere on a machine whose engine is a runtime: the
// runtime takes the session over, and the message goes to it there (the caller keeps the echo, as for any send).
func (n *Node) continueInRuntime(ctx context.Context, id, text string, images []proto.Image, clientMsgID string) error {
	h := n.engineRuntime(ctx)
	if h == nil {
		return n.engineRuntimeAway(ctx)
	}
	if _, err := n.takeOverInRuntime(ctx, h, runtimeStartParams{Resume: id}); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	err := h.peer.Call(cctx, "session.send", map[string]any{"session_id": id, "text": text, "images": images, "client_msg_id": clientMsgID}, nil)
	if err != nil && !isRPCError(err) {
		err = fmt.Errorf("%s: %w", h.label(), err)
	}
	return err
}

// liveRuntimeModels asks opt-in runtimes for their current native catalog. Older runtimes keep their advertised list.
func (n *Node) liveRuntimeModels(ctx context.Context, name string, refresh bool, fallback proto.LauncherModels) (proto.LauncherModels, error) {
	h := n.runtimes.launcherRuntime(name)
	if h == nil {
		return fallback, nil
	}
	h.mu.Lock()
	supported := h.hello.Caps.Models
	h.mu.Unlock()
	if !supported {
		return fallback, nil
	}
	ctx, cancel := context.WithTimeout(ctx, runtimeCallTimeout)
	defer cancel()
	var out proto.LauncherModels
	if err := h.peer.Call(ctx, "launchers.models", map[string]any{"launcher": name, "refresh": refresh}, &out); err != nil {
		return proto.LauncherModels{}, err
	}
	// Apply the same bounds as runtime.launchers before exposing a native response.
	clean := cleanLaunchers([]runtimeLauncher{{Name: name, Agent: fallback.Agent, Models: out.Models}})
	out.Agent, out.Models = fallback.Agent, clean[0].Models
	if out.Models == nil {
		out.Models = []proto.AgentModel{}
	}
	return out, nil
}

// runtimeLauncherCheck opts into the runtime's authenticated availability probe. A refused probe never falls back to
// the login shell, whose credentials may be different. Older runtimes keep the existing shell check.
func (n *Node) runtimeLauncherCheck(ctx context.Context, name, cwd, prompt string) (proto.LauncherCheck, bool) {
	h := n.runtimes.launcherRuntime(name)
	if h == nil {
		return proto.LauncherCheck{}, false
	}
	h.mu.Lock()
	supported := h.hello.Caps.Check
	h.mu.Unlock()
	if !supported {
		return proto.LauncherCheck{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, launcherCheckTimeout)
	defer cancel()
	start := time.Now()
	var result proto.LauncherCheck
	if err := h.peer.Call(ctx, "launchers.check", map[string]any{"launcher": name, "cwd": cwd, "prompt": prompt}, &result); err != nil {
		result = proto.LauncherCheck{Error: err.Error()}
	}
	launcher, _ := h.launcherNamed(name)
	result.Agent = launcher.Agent
	if result.Agent == "" {
		result.Agent = h.agent()
	}
	result.Millis = int(time.Since(start).Milliseconds())
	return result, true
}
