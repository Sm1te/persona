package node

// Runtimes (docs/integrations/runtimes.md, wire in docs/protocol/protocol.md "Runtime ↔ node"): agent runtimes without a terminal that keep
// their own sessions — DSH's Web plugin first (docs/integrations/dsh.md) — connect to ~/.persona/runtime.sock and hang their
// sessions, status and interactions into this node. They are not hosts in the internal/host sense (where a terminal
// lives, the machine's engine): a runtime has no panes, it reports its own sessions and content, and it runs beside
// whichever engine the machine uses. Every runtime speaks the same protocol, so a new one needs no code here.
// The wire is the hub ↔ node JSON lines (internal/rpc) on a long-lived connection: the runtime pushes its session list,
// the items of watched sessions and live text; it asks the phone through interaction.create; the node sends it every
// session.* call for a session it lists. The hub and the apps see ordinary sessions and interactions.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// RuntimeProtocol is the runtime wire's version, answered to runtime.hello; it grows only for changes old runtimes misread.
const RuntimeProtocol = 1

// RuntimeSocketPath is where runtimes reach the node.
func RuntimeSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(identity.SocketDir(home), "runtime.sock")
}

// runtimeCallTimeout bounds one node → runtime call; a history page of a long session is the slow one.
var runtimeCallTimeout = 30 * time.Second

// runtimeIDRe: runtime ids and the session ids a runtime may use. Wider than validID (DSH names them session-<uuid>), never a path.
var runtimeIDRe = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// runtimeHello is what a runtime says about itself first.
type runtimeHello struct {
	// Runtime is the runtime's id ("dsh"): its sessions' default agent and space kind.
	Runtime string `json:"runtime"`
	// Name is what the apps call it ("DSH"): its sessions' agent_label, the text of errors about them. Default: Runtime.
	Name string `json:"name,omitempty"`
	// Agent is the agent its sessions run, when it is not the runtime itself (a runtime hosting Claude Code).
	Agent   string `json:"agent,omitempty"`
	Version string `json:"version,omitempty"`
	// Instance identifies the process behind the connection (the SDK makes one per runtime it creates): a reconnect
	// says the same, another process configured with the same runtime id does not and is refused (runtime_conflict).
	// Empty from older runtimes.
	Instance string `json:"instance,omitempty"`
	// Launcher, when set, is offered in the new-session form; session.start with it goes to this runtime.
	Launcher string `json:"launcher,omitempty"`
	// LauncherModel is what the form shows next to the launcher (e.g. the runtime's default model).
	LauncherModel string `json:"launcher_model,omitempty"`
	// Launchers are more entries for the form, each with the agent it starts and its models: a runtime running
	// several agents (Paseo: one per provider, runtimecontrol.go). runtime.launchers replaces them later.
	Launchers []runtimeLauncher `json:"launchers,omitempty"`
	// Package is the code the runtime runs when this node can keep it up to date (runtimepkg, docs/integrations/runtimes.md "Auto-update"):
	// its package name and hash. A different hash than the node's gets an update offer in the reply.
	Package *struct {
		Name string `json:"name"`
		Hash string `json:"hash"`
	} `json:"package,omitempty"`
	Caps struct {
		Start     bool `json:"start"`
		Send      bool `json:"send"`
		Interrupt bool `json:"interrupt"`
		Rename    bool `json:"rename"`
		Close     bool `json:"close"`
		// Assistant: its launcher may run the Persona assistant (assistant_runtime.go; docs/integrations/runtimes.md "Assistant").
		Assistant bool `json:"assistant"`
		// Execution reports the actual per-session execution environment via session.execution.
		Execution bool `json:"execution"`
		// Modes: the phone's permission mode and effort for its sessions go to it (session.modes).
		Modes bool `json:"modes"`
		// Model permits session.modes model changes. Models provides live launchers.models discovery.
		Model  bool `json:"model"`
		Models bool `json:"models"`
		// Check performs the launcher availability probe in the runtime's authenticated context.
		Check bool `json:"check"`
		// Resume: session.start with resume takes a Claude session it does not have over, under that id (the machine's
		// engine continuing a session not running anywhere, runtimecontrol.go).
		Resume bool `json:"resume"`
		// KeepAlways: the runtime keeps no session rule of its own and asks the hub to remember "Always allow for this session" for it
		// (#3974): its permission cards that offer always get the node's key for the tool. A runtime that keeps its own
		// rule (opencode's save rule, Paseo's suggestions) does not say it, so its grant is never widened to the tool.
		KeepAlways bool `json:"keep_always"`
		// Archive: session.archive / session.unarchive go to it, for its own reversible archive (archive.go): a
		// session the user archives in Persona is archived there too, and restored with it.
		Archive bool `json:"archive"`
		// Readonly: a session.start with permission_mode "plan" either starts in a read-only mode before its first
		// prompt runs or is refused with readonly_unsupported; it never falls back to a writable provider (#3238).
		Readonly bool `json:"readonly"`
	} `json:"caps"`
}

// runtimeConn is one connected runtime.
type runtimeConn struct {
	serial int
	peer   *rpc.Peer

	mu sync.Mutex
	// drop ends the connection (RuntimeHost.serve's); nil in tests that make connections by hand.
	drop     func()
	hello    runtimeHello
	ready    bool            // runtime.hello arrived
	sessions []proto.Session // its last runtime.sessions
	watched  map[string]bool // sessions this connection was asked to watch
	// What its list said beyond the rows the apps see: the terminal pane a session runs in (pane_id: the extension of a
	// pi in a herdr pane, pi.go), how the terminal engine resumes one it lists offline (resume_argv), the Claude
	// session whose transcript holds a session's conversation (transcript: a runtime running Claude Code through its
	// SDK, Paseo; runtimetranscript.go), and the Codex thread a session's conversation is (codex_thread: a runtime
	// running Codex, Paseo; runtimecodex.go).
	panes        map[string]string
	resume       map[string][]string
	transcripts  map[string]string
	codexThreads map[string]string
	// daemon is its id in daemons.json when the node dialed it (daemons.go), "" for a runtime on the socket.
	daemon string
	// pid is the process at the other end of the runtime socket; 0 when not known (a daemon's link, the Orca bridge,
	// a system that does not tell). A runtime running the assistant vouches for its descendants' MCP servers
	// (mcpAssistantOf).
	pid int
}

// isDaemon: the node dialed it (a daemon in daemons.json), it did not connect to the socket.
func (h *runtimeConn) isDaemon() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.daemon != ""
}

// id is the runtime's id ("" before hello).
func (h *runtimeConn) id() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello.Runtime
}

// agent is the agent its sessions run.
func (h *runtimeConn) agent() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hello.Agent != "" {
		return h.hello.Agent
	}
	return h.hello.Runtime
}

// assistant: the runtime runs the Persona assistant's conversations (caps.assistant).
func (h *runtimeConn) assistant() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hello.Caps.Assistant
}

// label is what the apps call the runtime.
func (h *runtimeConn) label() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hello.Name != "" {
		return h.hello.Name
	}
	return h.hello.Runtime
}

// splitRuntimeChild splits the id of a runtime session's subagent, "<parent>@<child>" (the id Claude's subagents have
// too: docs/protocol/protocol.md "Runtime ↔ node"); false for any other id.
func splitRuntimeChild(id string) (parent, child string, ok bool) {
	parent, child, ok = strings.Cut(id, "@")
	return parent, child, ok && runtimeIDRe.MatchString(parent) && runtimeIDRe.MatchString(child)
}

// Bounds on what one runtime row may carry about the work it left running.
const (
	maxRuntimeSubagents  = 50
	maxRuntimeBackground = 50
	maxRuntimeTaskText   = 500
)

// runtimeWork keeps a row's subagents and background tasks to what the apps can use: subagents with a valid id, each
// readable as "<session>@<id>" (whatever the runtime wrote there), background tasks with an id; text clipped, the
// lists bounded. A runtime's background task has no output file of this machine's to open.
func runtimeWork(s *proto.Session) {
	clip := func(t string) string {
		if r := []rune(t); len(r) > maxRuntimeTaskText {
			return string(r[:maxRuntimeTaskText]) + "…"
		}
		return t
	}
	subs := s.Subagents[:0:0]
	seen := map[string]bool{}
	for _, a := range s.Subagents {
		if len(subs) == maxRuntimeSubagents || !runtimeIDRe.MatchString(a.ID) || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		a.SessionID = s.SessionID + "@" + a.ID
		a.Description, a.LastText, a.Type, a.Model = clip(a.Description), clip(a.LastText), clip(a.Type), clip(a.Model)
		subs = append(subs, a)
	}
	s.Subagents = subs
	if len(s.Subagents) == 0 {
		s.Subagents = nil
	}
	bg := s.Background[:0:0]
	for _, t := range s.Background {
		if len(bg) == maxRuntimeBackground || strings.TrimSpace(t.ID) == "" {
			continue
		}
		if t.Kind != "agent" {
			t.Kind = "shell"
		}
		t.ID, t.Description, t.Command, t.OutputFile = clip(t.ID), clip(t.Description), clip(t.Command), ""
		bg = append(bg, t)
	}
	s.Background = bg
	if len(s.Background) == 0 {
		s.Background = nil
	}
}

// setResume records how the engine resumes the sessions this runtime lists (resume_argv), replacing the last list's.
func (h *runtimeConn) setResume(m map[string][]string) {
	h.mu.Lock()
	h.resume = m
	h.mu.Unlock()
}

// resumeArgv is how the terminal engine resumes session id, as this runtime said; nil when it did not.
func (h *runtimeConn) resumeArgv(id string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.resume[id]...)
}

// row is this runtime's row for session id.
func (h *runtimeConn) row(id string) (proto.Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if s.SessionID == id {
			return s, true
		}
	}
	return proto.Session{}, false
}

// listLen is how many rows its last runtime.sessions kept.
func (h *runtimeConn) listLen() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sessions)
}

func (h *runtimeConn) sessionsCopy() []proto.Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]proto.Session(nil), h.sessions...)
}
