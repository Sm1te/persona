package node

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// listenRuntimeSocket gives n a runtime socket of its own, at runtimePath, for the connection it serves itself
// (`--stdio` with no resident node: runtimehost.go). A resident node's Nodes share one instead.
func (n *Node) listenRuntimeSocket(ctx context.Context) (*RuntimeHost, error) {
	rh := &RuntimeHost{reg: n.runtimes, path: n.runtimePath(), version: n.Version, logger: n.logger}
	if rh.reg == nil {
		rh.reg = newRuntimeRegistry()
	}
	if rh.reg.logf == nil && n.logger != nil {
		rh.reg.logf = n.logger.Printf
	}
	if err := rh.Listen(ctx); err != nil {
		return nil, err
	}
	rh.attach(n)
	return rh, nil
}

func (n *Node) runtimePath() string {
	if n.runtimeSock != "" {
		return n.runtimeSock
	}
	return RuntimeSocketPath()
}

// runtimeEvent takes a runtime's push for the hub (its list is the host's: RuntimeHost.event).
func (n *Node) runtimeEvent(h *runtimeConn, event string, params json.RawMessage) {
	h.mu.Lock()
	ready := h.ready
	h.mu.Unlock()
	if !ready {
		return // hello first
	}
	switch event {
	case "session.items":
		var p struct {
			SessionID string       `json:"session_id"`
			Items     []proto.Item `json:"items"`
			ToSeq     *int         `json:"to_seq"`
		}
		if json.Unmarshal(params, &p) != nil || !n.runtimes.owns(h, p.SessionID) {
			return
		}
		if n.runtimeTranscript(p.SessionID) != "" {
			return // its items are the transcript's, which the node pushes itself (runtimetranscript.go)
		}
		last := 0
		if len(p.Items) > 0 {
			last = p.Items[len(p.Items)-1].Seq
		}
		if p.ToSeq != nil {
			last = max(last, *p.ToSeq)
		}
		n.runtimes.pushed(p.SessionID, last)
		if n.peer != nil {
			_ = n.peer.Notify("session.items", n.echoedRaw(p.SessionID, params, "items")) // echo.go
		}
	case "session.delta":
		var p struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(params, &p) != nil || !n.runtimes.owns(h, p.SessionID) || n.peer == nil {
			return
		}
		_ = n.peer.Notify("session.delta", params)
	case "interaction.cancel":
		if n.peer != nil {
			_ = n.peer.Notify("interaction.cancel", params)
		}
	}
}

// normalizeRuntimeSessions keeps the sessions a runtime may list (valid ids, once each) and fills what the apps expect:
// the agent and its label, the space kind (the runtime's id). It works in place: the rows kept are list's first ones,
// in list's own array, the rest cleared, so list is the caller's to give up.
func normalizeRuntimeSessions(h *runtimeConn, list []proto.Session) []proto.Session {
	h.mu.Lock()
	rt, agent, label, caps, daemon := h.hello.Runtime, h.hello.Agent, h.hello.Name, h.hello.Caps, h.daemon != ""
	h.mu.Unlock()
	if agent == "" {
		agent = rt
	}
	if label == "" {
		label = rt
	}
	readOnly := ""
	if !caps.Send {
		readOnly = label + " 没有开放从手机发消息"
	}
	seen := make(map[string]bool, len(list))
	panes := map[string]string{}
	defer func() {
		h.mu.Lock()
		if h.panes == nil {
			h.panes = map[string]string{}
		}
		for id, pane := range panes {
			h.panes[id] = pane
		}
		h.mu.Unlock()
	}()
	out := list[:0] // row i is written at or before i, after it was read
	for _, s := range list {
		if !runtimeIDRe.MatchString(s.SessionID) || seen[s.SessionID] {
			continue
		}
		seen[s.SessionID] = true
		s.MachineID, s.State, s.PendingInteractions = "", "", 0 // the hub's to fill
		s.StartedByPersona, s.Assistant = false, false
		// Of the hierarchy (docs/protocol/protocol.md "Session hierarchy") the runtime says when the session began
		// (started_at); what it folds under and the rollup are the manager's, the parent a blocked child asks for the
		// node's (below), from what a daemon's own node named (daemons.go) when the row names no other.
		asking := s.AskingParentSessionID()
		s.SetFold("", "", nil)
		s.SetAskingParentSessionID("")
		if s.Agent == "" {
			s.Agent = agent
		}
		if s.AgentLabel == "" && (s.Agent == rt || daemon && s.Agent != "claude") {
			// The runtime runs its own agent (DSH): the apps call it by the runtime's name. A daemon is called by the
			// name its config gives, whatever agent it runs (a platform running DSH), unless that is Claude Code.
			s.AgentLabel = label
		}
		if s.SpaceKind == "" {
			s.SpaceKind = rt
		}
		// The source fields Codex app-server sessions use too (docs/protocol/protocol.md "Codex sources and capabilities"): the apps gate
		// sending, interrupting, answering cards and every terminal control on capabilities, the hub guards sends.
		// What a runtime declared, never more: no terminal, no attach, cards always answerable (they are its own asks).
		s.SourceKind, s.SourceInstanceID, s.SourceThreadID, s.ParentThreadID = proto.SourceRuntime, rt, "", ""
		if rt == paseoRuntimeID || rt == cindyRuntimeID {
			s.Host = rt // an engine host, though no terminal (#2576)
		} else {
			s.Host = ""
		}
		s.Capabilities = &proto.SessionCapabilities{Read: true, Send: caps.Send, Interrupt: caps.Interrupt, Approve: true,
			Close: &caps.Close, Modes: proto.CanModes(runtimeModes(caps.Modes, rt, s)),
			Model: proto.CanModes(caps.Modes && caps.Model && !strings.Contains(s.SessionID, subagentSep))}
		s.ReadOnlyReason, s.Attached, s.CodexRoute, s.CodexRouteNote = readOnly, false, "", ""
		// The pane is the engine's to show (pi.go joins it with the engine's pane), not part of the runtime's row.
		if s.PaneID != "" && validPane(s.PaneID) {
			panes[s.SessionID] = s.PaneID
		}
		s.PaneID, s.TabID, s.WorkspaceID = "", "", ""
		switch s.Status {
		case proto.StatusWorking, proto.StatusDone, proto.StatusIdle, proto.StatusOffline:
		case proto.StatusBlocked:
			// A session another one started (is_subagent, parent_session_id) that waits for the user is a row of its
			// own while it does (#4494): the apps that fold every child away (iOS, Android, older web) leave out no
			// session that needs the user, whichever rows they count. Its parent is named again once it was answered.
			// Meanwhile asking_parent_session_id names it (#5120): the apps that show children as cards in their
			// parent's chat keep the card, and its question, there.
			parent := s.ParentSessionID
			if parent == "" && s.RootSessionID != s.SessionID {
				parent = s.RootSessionID
			}
			s.SetAskingParentSessionID(cmp.Or(parent, asking))
			s.IsSubagent, s.ParentSessionID, s.RootSessionID = false, "", ""
		default:
			s.Status = proto.StatusUnknown
		}
		if s.Title == "" && s.Cwd != "" {
			s.Title = filepath.Base(s.Cwd)
		}
		if s.UpdatedAt.IsZero() {
			s.UpdatedAt = time.Now().UTC()
		}
		s.UpdatedAt = s.UpdatedAt.UTC()
		runtimeWork(&s)
		out = append(out, s)
	}
	clear(list[len(out):]) // the rows left out hold no strings
	return out
}

// runtimeWatch asks a runtime to push a session's new items, from since on; for a session whose conversation is a
// transcript here (transcript: true), only its live text.
func (n *Node) runtimeWatch(h *runtimeConn, id string, since int) {
	ctx, cancel := context.WithTimeout(context.Background(), runtimeCallTimeout)
	defer cancel()
	params := map[string]any{"session_id": id, "since_seq": since}
	if n.runtimeTranscript(id) != "" {
		params["since_seq"], params["transcript"] = 0, true
	}
	if err := h.peer.Call(ctx, "session.watch", params, nil); err != nil {
		n.logger.Printf("runtime %s: watch %s: %v", h.id(), id, err)
	}
}

// runtimeAsk forwards a runtime's question / plan / permission to the phone and returns the answer; {dismiss: true}
// when nobody answered (expired, handed back to the computer, no hub). A node going away under a resumable
// interaction answers with code node_gone: the runtime asks the next node with the same id.
func (n *Node) runtimeAsk(ctx context.Context, h *runtimeConn, params json.RawMessage) (any, error) {
	var p struct {
		Interaction proto.Interaction `json:"interaction"`
		// TimeoutSeconds: how long the runtime waits at most (default: the hook timeout).
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("bad interaction: %w", err)
	}
	ix := p.Interaction
	switch ix.Kind {
	case proto.IxQuestion, proto.IxPlan, proto.IxPermission:
	default:
		return nil, fmt.Errorf("unsupported interaction kind %q", ix.Kind)
	}
	if !n.runtimes.owns(h, ix.SessionID) {
		return nil, fmt.Errorf("session %s is not listed by this runtime", ix.SessionID)
	}
	rt := h.id()
	if ix.ID == "" || !runtimeIDRe.MatchString(ix.ID) {
		key := ix.ToolUseID
		if key == "" {
			key = newUUID()
		}
		sum := sha256.Sum256([]byte(rt + "\x00" + ix.SessionID + "\x00" + ix.Kind + "\x00" + key))
		ix.ID = "ix_" + hex.EncodeToString(sum[:])[:16]
	}
	// Who asks, as on a Codex card (proto.Interaction.Agent): the apps draw a runtime's card as its own agent's
	// ("Allow" / "Deny", its reason, "Always allow" only with an always_label it gives), not as Claude's, whose "Always allow for this session" a
	// runtime cannot keep. A runtime hosting Claude Code (agent "claude") gets Claude's card.
	h.mu.Lock()
	agent, label := h.hello.Agent, h.hello.Name
	h.mu.Unlock()
	if agent == "" {
		agent = rt
	}
	if label == "" {
		label = rt
	}
	// A runtime running several agents (Paseo: Claude Code and others) says which one each session runs: the card is
	// that agent's.
	if s, ok := h.row(ix.SessionID); ok && s.Agent != "" && s.Agent != agent {
		agent = s.Agent
		if s.AgentLabel != "" {
			label = s.AgentLabel
		}
	}
	if agent == "claude" {
		ix.Agent, ix.AgentLabel = "", ""
	} else {
		ix.Agent, ix.AgentLabel = agent, label
	}
	now := time.Now().UTC()
	ix.MachineID, ix.Screen, ix.Dialog, ix.Secret = "", "", nil, false
	// "Always allow for this session" kept by the hub for a runtime's permission card (#3974): what it grants is the node's word, never
	// the runtime's — one key for this runtime's tool, which the manager keeps under this session id, so it never
	// reaches another session or another tool. Only for a runtime that asked for it in its hello (caps.keep_always: it
	// keeps no rule itself; a runtime whose always is a narrower rule of its own must not get the whole tool), only
	// when the card offers it (an always_label), and never in the health space or on a card with fields.
	h.mu.Lock()
	keepAlways := h.hello.Caps.KeepAlways
	h.mu.Unlock()
	ix.AlwaysKeys = nil
	if keepAlways && ix.Kind == proto.IxPermission && strings.TrimSpace(ix.AlwaysLabel) != "" && strings.TrimSpace(ix.ToolName) != "" &&
		len(ix.Fields) == 0 && !runtimeHealth(h, ix) {
		ix.AlwaysKeys = []string{"runtime:" + rt + ":tool:" + ix.ToolName}
	}
	ix.CreatedAt = now
	timeout := nodeclient.HookTimeout
	if p.TimeoutSeconds > 0 && time.Duration(p.TimeoutSeconds)*time.Second < timeout {
		timeout = time.Duration(p.TimeoutSeconds) * time.Second
	}
	ix.ExpiresAt = now.Add(timeout)
	ix.Resumable = ix.ToolUseID != "" // its id is the same when the runtime asks again through the next node
	for _, s := range h.sessionsCopy() {
		if s.SessionID == ix.SessionID {
			if ix.SessionTitle == "" {
				ix.SessionTitle = s.Title
			}
			if ix.Cwd == "" {
				ix.Cwd = s.Cwd
			}
		}
	}
	res, ok, err := n.ask(ctx, ix)
	if errors.Is(err, errNodeGone) {
		return nil, &rpc.Error{Message: "node going away", Code: "node_gone"}
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return proto.Resolution{Dismiss: true}, nil
	}
	return res, nil
}

// runtimeHealth: the card's session is in the health space (its cwd or the row's), where every write is confirmed
// (docs/assistant/health.md "Outbound"). A directory inside the space counts: a tool's `cd` may have left either there
// (identity.InHealthSpace, #2368).
func runtimeHealth(h *runtimeConn, ix proto.Interaction) bool {
	if identity.InHealthSpace(ix.Cwd) {
		return true
	}
	s, ok := h.row(ix.SessionID)
	return ok && (s.Space == identity.SpaceHealth || identity.InHealthSpace(s.Cwd))
}

// runtimeModes is a runtime row's modes capability: the runtime declared caps.modes (the node refuses session.modes
// otherwise, runtimes_calls.go), the row is not a subagent's, and its agent takes Claude Code's modes (claude: Orca,
// Paseo's Claude providers) or is the runtime's own (agy). Paseo has no agent of its own: its bridge calls every agent
// other than Claude Code and Codex "paseo" (integrations/paseo/lib/map.js appAgent), and refuses a permission mode for
// any of them that is not Claude (bridge.js modes). So a Paseo row running Codex, opencode or any other agent gets none.
func runtimeModes(declared bool, rt string, s proto.Session) bool {
	own := s.Agent == rt && rt != paseoRuntimeID
	return declared && !strings.Contains(s.SessionID, subagentSep) && (s.Agent == "claude" || own)
}
