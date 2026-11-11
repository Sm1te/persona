package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// assistantNode is the assistant's machine as runTask sees it: assistant.send with no session starts one (s1, s2…),
// with a session continues it, and with resume_only refuses one it doesn't have (cleaned up, never written).
type assistantNode struct {
	m     *Machine
	mu    sync.Mutex
	known map[string]bool
	made  int // conversations started, for the next id
	sends []map[string]any
	kept  []string     // the last node.configure keep_sessions
	items []proto.Item // session.items answers this (assistant_titles_test.go)
	// assistant.handoff answers handoff; assistant.compact notes the session and answers compactErr
	// (assistant_main_test.go).
	handoff    map[string]any
	compacts   []string
	compactErr error
}

func newAssistantNode(t *testing.T, h *Hub) *assistantNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h2nR, h2nW := io.Pipe()
	n2hR, n2hW := io.Pipe()
	hubSide := rpc.New("h", h2nW)
	nodeSide := rpc.New("n", n2hW)
	n := &assistantNode{known: map[string]bool{}}
	nodeSide.OnRequest = func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var p map[string]any
		_ = json.Unmarshal(params, &p)
		n.mu.Lock()
		defer n.mu.Unlock()
		switch method {
		case "assistant.prepare":
			return map[string]any{"cwd": "/home/me/.persona/assistant"}, nil
		case "node.configure":
			n.kept = nil
			for _, k := range p["keep_sessions"].([]any) {
				n.kept = append(n.kept, k.(string))
			}
			return map[string]any{}, nil
		case "session.items":
			return map[string]any{"items": n.items}, nil
		case "session.rename":
			n.sends = append(n.sends, map[string]any{"rename": p})
			return map[string]any{"ok": true}, nil
		case "assistant.handoff":
			if n.handoff == nil {
				return nil, fmt.Errorf("conversation %v is gone", p["session_id"])
			}
			return n.handoff, nil
		case "assistant.compact":
			n.compacts = append(n.compacts, fmt.Sprint(p["session_id"]))
			if n.compactErr != nil {
				return nil, n.compactErr
			}
			return map[string]any{"ok": true}, nil
		case "session.send":
			n.sends = append(n.sends, p)
			return map[string]any{}, nil
		case "session.interrupt":
			n.sends = append(n.sends, map[string]any{"interrupt": p["session_id"]})
			return map[string]any{"ok": true}, nil
		case "session.close":
			n.sends = append(n.sends, map[string]any{"close": p["session_id"]})
			return map[string]any{"ok": true}, nil
		case "assistant.send":
			n.sends = append(n.sends, p)
			id, _ := p["session_id"].(string)
			if id == "" {
				n.made++
				id = fmt.Sprintf("s%d", n.made)
				n.known[id] = true
			} else if p["resume_only"] == true && !n.known[id] {
				return nil, fmt.Errorf("conversation %s is gone", id)
			}
			return map[string]any{"session_id": id}, nil
		}
		return nil, fmt.Errorf("unexpected %s", method)
	}
	go func() { _ = hubSide.Serve(ctx, n2hR) }()
	go func() { _ = nodeSide.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = h2nW.Close(); _ = n2hW.Close() })
	n.m = newMachine(h, MachineConfig{ID: "server", Name: "server", Kind: "local"})
	n.m.peer = hubSide
	h.machines = append(h.machines, n.m)
	h.byID["server"] = n.m
	return n
}

func (n *assistantNode) lastSend(t *testing.T) map[string]any {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.sends) == 0 {
		t.Fatal("nothing was sent")
	}
	return n.sends[len(n.sends)-1]
}

func (n *assistantNode) sent() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.sends)
}
