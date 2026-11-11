package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/runtimepkg"
)

// runtimeHub is the hub end of a node under test: the events it got and how it answers interactions.
type runtimeHub struct {
	mu       sync.Mutex
	events   []string
	lists    [][]proto.Session
	items    []json.RawMessage
	deltas   []json.RawMessage
	cancels  []string
	asked    []proto.Interaction
	resolve  func(proto.Interaction) proto.Resolution
	nodePeer *rpc.Peer
	// answer takes the other hub calls (mcp.tools, assistant.context…); handled false: an unexpected call.
	answer func(method string, params json.RawMessage) (res any, err error, handled bool)
}

func (b *runtimeHub) lastList() []proto.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lists) == 0 {
		return nil
	}
	return b.lists[len(b.lists)-1]
}

// runtimeNode is a node with a runtime socket in a short temp dir (unix socket paths are short on macOS) and a fake hub.
func runtimeNode(t *testing.T) (*Node, *runtimeHub) {
	t.Helper()
	return runtimeNodeOn(t, nil)
}

// runtimeNodeOn is runtimeNode serving a hub connection on a resident node's shared runtime host (nil: its own socket).
func runtimeNodeOn(t *testing.T, shared *RuntimeHost) (*Node, *runtimeHub) {
	t.Helper()
	return runtimeNodeTapped(t, shared, nil)
}

// runtimeNodeTapped is runtimeNodeOn writing whatever the node writes to the hub to tap first (nil: no tap). A write
// that fails on tap fails, and the hub does not get it.
func runtimeNodeTapped(t *testing.T, shared *RuntimeHost, tap io.Writer) (*Node, *runtimeHub) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "jb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	n, _ := testNode(t)
	n.logger = log.New(io.Discard, "", 0)
	n.runtimes = newRuntimeRegistry()
	n.runtimeSock = filepath.Join(dir, "h.sock")
	n.hosts = herdrHosts(&herdr.Client{Bin: filepath.Join(dir, "no-herdr")}) // herdr unreachable: no socket, no binary
	// No running claude to find: the real scan found the hub's assistant, and the list read its transcripts.
	n.cdirs = newClaudeDirs()
	n.cdirs.scan = func() map[int]procEnv { return nil }
	n.metas, n.bgScans = map[string]quickMeta{}, map[string]*bgScan{}
	n.modes = map[string]sessionModes{}

	ctx, cancel := context.WithCancel(context.Background())
	n2hR, n2hW := io.Pipe()
	h2nR, h2nW := io.Pipe()
	var toHub io.Writer = n2hW
	if tap != nil {
		toHub = io.MultiWriter(tap, n2hW)
	}
	n.peer = rpc.New("n", toHub)
	n.peer.OnRequest = n.handle
	peer := n.peer // served below: a test may give the node another connection later
	hub := rpc.New("h", h2nW)
	end := &runtimeHub{nodePeer: hub, resolve: func(proto.Interaction) proto.Resolution { return proto.Resolution{Dismiss: true} }}
	hub.OnEvent = func(event string, params json.RawMessage) {
		end.mu.Lock()
		defer end.mu.Unlock()
		end.events = append(end.events, event)
		switch event {
		case "sessions.changed":
			var p struct {
				Sessions []proto.Session `json:"sessions"`
			}
			_ = json.Unmarshal(params, &p)
			end.lists = append(end.lists, p.Sessions)
		case "session.items":
			end.items = append(end.items, params)
		case "session.delta":
			end.deltas = append(end.deltas, params)
		case "interaction.cancel":
			var p struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(params, &p)
			end.cancels = append(end.cancels, p.ID)
		}
	}
	hub.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "interaction.create":
			var p struct {
				Interaction proto.Interaction `json:"interaction"`
			}
			_ = json.Unmarshal(params, &p)
			end.mu.Lock()
			end.asked = append(end.asked, p.Interaction)
			resolve := end.resolve
			end.mu.Unlock()
			return resolve(p.Interaction), nil
		}
		end.mu.Lock()
		answer := end.answer
		end.mu.Unlock()
		if answer != nil {
			if res, err, handled := answer(method, params); handled {
				return res, err
			}
		}
		return nil, errors.New("unexpected hub call " + method)
	}
	go func() { _ = hub.Serve(ctx, n2hR) }()
	go func() { _ = peer.Serve(ctx, h2nR) }()
	if shared != nil {
		n.runtimeSock = shared.path
		shared.attach(n)
	} else if _, err := n.listenRuntimeSocket(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = n2hW.Close(); _ = h2nW.Close() })
	// Runs first, while the hub still reads: an engine switch's list, still being built, goes to it and reads package
	// state (botmuxBin) that later tests swap.
	t.Cleanup(n.engLists.Wait)
	return n, end
}

// fakeRuntime is a runtime: what it was asked, and how it answers.
type fakeRuntime struct {
	peer   *rpc.Peer
	conn   net.Conn
	mu     sync.Mutex
	calls  []string
	params map[string][]json.RawMessage
	answer func(method string, params json.RawMessage) (any, error)
	events []string       // event names the node pushed, in order
	hubs   []bool         // runtime.hub pushes: whether a hub is connected
	helloR map[string]any // what runtime.hello answered
	closed chan struct{}  // closed when the connection ends (helloAt)
}

func (f *fakeRuntime) got(method string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]json.RawMessage(nil), f.params[method]...)
}

func dialRuntime(t *testing.T, n *Node, hello map[string]any) *fakeRuntime {
	t.Helper()
	return dialRuntimeAt(t, n.runtimeSock, hello)
}

// dialRuntimeAt connects a fake runtime to the runtime socket at path and says hello.
func dialRuntimeAt(t *testing.T, path string, hello map[string]any) *fakeRuntime {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRuntime{conn: c, params: map[string][]json.RawMessage{}}
	f.peer = rpc.New("d", c)
	f.peer.OnRequest = func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.params[method] = append(f.params[method], params)
		answer := f.answer
		f.mu.Unlock()
		if answer != nil {
			return answer(method, params)
		}
		return map[string]any{"ok": true}, nil
	}
	f.peer.OnEvent = func(event string, params json.RawMessage) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.events = append(f.events, event)
		if event == "runtime.hub" {
			var p struct {
				Connected bool `json:"connected"`
			}
			_ = json.Unmarshal(params, &p)
			f.hubs = append(f.hubs, p.Connected)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = f.peer.Serve(ctx, c) }()
	t.Cleanup(func() { cancel(); _ = c.Close() })
	var res map[string]any
	cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ccancel()
	if err := f.peer.Call(cctx, "runtime.hello", hello, &res); err != nil {
		t.Fatal(err)
	}
	if p, _ := res["protocol"].(float64); int(p) != RuntimeProtocol {
		t.Fatalf("protocol %v", res["protocol"])
	}
	f.helloR = res
	return f
}

func dshHello() map[string]any {
	return map[string]any{"runtime": "dsh", "name": "DSH", "version": "test", "launcher": "dsh", "launcher_model": "DeepSeek",
		"caps": map[string]any{"start": true, "send": true, "interrupt": true, "rename": true}}
}

func nodeCall(t *testing.T, n *Node, method string, params any) (json.RawMessage, error) {
	t.Helper()
	raw, _ := json.Marshal(params)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := n.handle(ctx, method, raw)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	return b, nil
}

func listed(t *testing.T, n *Node) map[string]proto.Session {
	t.Helper()
	raw, err := nodeCall(t, n, "sessions.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Sessions []proto.Session `json:"sessions"`
	}
	_ = json.Unmarshal(raw, &res)
	out := map[string]proto.Session{}
	for _, s := range res.Sessions {
		out[s.SessionID] = s
	}
	return out
}

func TestRuntimeCloseCapabilityIsIndependentOfTerminal(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		t.Run(fmt.Sprint(allowed), func(t *testing.T) {
			n, _ := runtimeNode(t)
			hello := dshHello()
			hello["caps"].(map[string]any)["close"] = allowed
			host := dialRuntime(t, n, hello)
			_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "close-capability", "status": "working"}}})
			waitFor(t, "runtime session listed", func() bool { _, ok := listed(t, n)["close-capability"]; return ok })
			caps := listed(t, n)["close-capability"].Capabilities
			if caps == nil || caps.Close == nil || *caps.Close != allowed || caps.Terminal {
				t.Fatalf("close %v listed as %+v", allowed, caps)
			}
		})
	}
}

func TestRuntimeSessionsAreListedAndPushed(t *testing.T) {
	n, hub := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{
		{"session_id": "session-0a1b", "title": "fix the build", "cwd": "/w/app", "status": "working", "space_name": "app"},
		{"session_id": "../etc", "title": "not a valid id"},
	}})
	waitFor(t, "sessions.changed with the host's session", func() bool {
		for _, s := range hub.lastList() {
			if s.SessionID == "session-0a1b" {
				return true
			}
		}
		return false
	})
	s := listed(t, n)["session-0a1b"]
	if s.Agent != "dsh" || s.AgentLabel != "DSH" || s.SpaceKind != "dsh" || s.Status != proto.StatusWorking || s.Title != "fix the build" || s.SpaceName != "app" {
		t.Fatalf("listed as %+v", s)
	}
	// The source fields the apps gate controls on (as for Codex app-server sessions): what the runtime declared.
	c := s.Capabilities
	if s.SourceKind != proto.SourceRuntime || s.SourceInstanceID != "dsh" || c == nil || !c.Read || !c.Send || !c.Interrupt || !c.Approve ||
		c.Terminal || c.Attach || s.ReadOnlyReason != "" {
		t.Fatalf("source %q/%q caps %+v reason %q", s.SourceKind, s.SourceInstanceID, c, s.ReadOnlyReason)
	}
	if _, bad := listed(t, n)["../etc"]; bad {
		t.Fatal("an invalid id was listed")
	}
	if s.PaneID != "" {
		t.Fatal("a runtime session has no pane")
	}
}

// A runtime that hosts an agent the apps know (here Claude Code) says so: its sessions carry that agent and no label of
// the runtime's; a runtime without a name is labelled by its id; a hello without a runtime id is refused.
func TestRuntimeAgentAndLabel(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, map[string]any{"runtime": "acme-cloud", "agent": "claude"})
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{
		{"session_id": "c1", "status": "idle"},
		{"session_id": "c2", "agent": "acme-cloud", "status": "idle"},
	}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["c2"]; return ok })
	got := listed(t, n)
	if s := got["c1"]; s.Agent != "claude" || s.AgentLabel != "" || s.SpaceKind != "acme-cloud" {
		t.Fatalf("c1 %+v", s)
	}
	if s := got["c2"]; s.AgentLabel != "acme-cloud" {
		t.Fatalf("c2 %+v", s)
	}

	c, err := net.Dial("unix", n.runtimeSock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p := rpc.New("x", c)
	go func() { _ = p.Serve(context.Background(), c) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Call(ctx, "runtime.hello", map[string]any{"name": "No id"}, nil); err == nil {
		t.Fatal("a hello without a runtime id was taken")
	}
}

func TestRuntimeLeavingTakesItsSessions(t *testing.T) {
	n, hub := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-1", "status": "idle"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-1"]; return ok })
	_ = host.conn.Close()
	waitFor(t, "gone from the pushed list", func() bool {
		for _, s := range hub.lastList() {
			if s.SessionID == "session-1" {
				return false
			}
		}
		return len(hub.lists) > 1
	})
	if _, ok := listed(t, n)["session-1"]; ok {
		t.Fatal("still listed after its host left")
	}
}

func TestRuntimeForwardsSessionCalls(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	host.answer = func(method string, params json.RawMessage) (any, error) {
		if method == "session.items" {
			var items []proto.Item
			for i := 1; i <= 4; i++ { // four tool calls with results: folded on a page counted back from the end
				items = append(items,
					proto.Item{ID: "u", Seq: i * 2, Role: "assistant", Kind: proto.KindToolUse, ToolUseID: "c" + string(rune('0'+i)), ToolName: "bash"},
					proto.Item{ID: "r", Seq: i*2 + 1, Role: "user", Kind: proto.KindToolResult, ToolUseID: "c" + string(rune('0'+i)), Text: "ok"})
			}
			items = append([]proto.Item{{ID: "p", Seq: 1, Role: "user", Kind: proto.KindText, Text: "run it"}}, items...)
			return map[string]any{"items": items, "has_more": false, "history": "h1", "to_seq": 9}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-2", "status": "idle"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-2"]; return ok })

	for i := 0; i < 2; i++ { // the same client_msg_id twice (an app retry): sent once
		if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": "session-2", "text": "hi", "client_msg_id": "m-1"}); err != nil {
			t.Fatal(err)
		}
	}
	if sent := host.got("session.send"); len(sent) != 1 {
		t.Fatalf("host got %d sends", len(sent))
	} else {
		var p struct {
			Text        string `json:"text"`
			ClientMsgID string `json:"client_msg_id"`
		}
		_ = json.Unmarshal(sent[0], &p)
		if p.Text != "hi" || p.ClientMsgID != "m-1" {
			t.Fatalf("sent %s", sent[0])
		}
	}
	if _, err := nodeCall(t, n, "session.interrupt", map[string]any{"session_id": "session-2"}); err != nil || len(host.got("session.interrupt")) != 1 {
		t.Fatalf("interrupt: %v", err)
	}

	raw, err := nodeCall(t, n, "session.items", map[string]any{"session_id": "session-2", "turns": 2, "fold_work": true, "lazy_images": true})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items   []proto.Item `json:"items"`
		History string       `json:"history"`
		ToSeq   int          `json:"to_seq"`
	}
	_ = json.Unmarshal(raw, &page)
	if page.History != "h1" || page.ToSeq != 9 || len(page.Items) != 2 || page.Items[1].Kind != proto.KindFold {
		t.Fatalf("page %s", raw)
	}
	raw, _ = nodeCall(t, n, "session.items", map[string]any{"session_id": "session-2", "after_seq": 3})
	_ = json.Unmarshal(raw, &page)
	if len(page.Items) != 9 {
		t.Fatalf("an after_seq page is not folded: %d items", len(page.Items))
	}

	_, err = nodeCall(t, n, "session.keys", map[string]any{"session_id": "session-2", "keys": []string{"esc"}})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != "unsupported" {
		t.Fatalf("keys: %v", err)
	}
	if raw, err := nodeCall(t, n, "session.rename", map[string]any{"session_id": "session-2", "tab_name": "x"}); err != nil || len(host.got("session.rename")) != 0 {
		t.Fatalf("a tab name goes nowhere: %s %v", raw, err)
	}
	if _, err := nodeCall(t, n, "session.rename", map[string]any{"session_id": "session-2", "title": "New name"}); err != nil || len(host.got("session.rename")) != 1 {
		t.Fatalf("rename: %v", err)
	}
}

func TestRuntimeSendRefusedWithoutTheCap(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, map[string]any{"runtime": "dsh"})
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-3"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-3"]; return ok })
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": "session-3", "text": "hi"}); err == nil {
		t.Fatal("sent without the send cap")
	}
	if len(host.got("session.send")) != 0 {
		t.Fatal("the host was asked to send")
	}
	if s := listed(t, n)["session-3"]; s.Capabilities == nil || s.Capabilities.Send || s.ReadOnlyReason == "" {
		t.Fatalf("a runtime without send is listed read-only with a reason: %+v %q", s.Capabilities, s.ReadOnlyReason)
	}
}

// A row's modes capability (#1843): what the runtime declared, for a row whose agent takes Claude Code's modes or is the
// runtime's own, never a subagent's. Rows of a runtime that did not declare it say false, not nothing.
func TestRuntimeModesCapability(t *testing.T) {
	for _, c := range []struct {
		declared bool
		rt, id   string
		agent    string
		want     bool
	}{
		{true, "orca", "session-1", "claude", true},
		{true, "agy", "session-1", "agy", true},
		{true, "paseo", "session-1", "codex", false},
		{true, "paseo", "session-1", "claude", true},
		{true, "paseo", "session-1", "paseo", false}, // Paseo's catch-all (opencode, grok, ...): its modes refuses them
		{true, "orca", "session-1" + subagentSep + "a1", "claude", false},
		{false, "dsh", "session-1", "dsh", false},
	} {
		if got := runtimeModes(c.declared, c.rt, proto.Session{SessionID: c.id, Agent: c.agent}); got != c.want {
			t.Errorf("%+v: modes %v", c, got)
		}
	}
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, map[string]any{"runtime": "dsh"})
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-4"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-4"]; return ok })
	if s := listed(t, n)["session-4"]; s.Capabilities == nil || s.Capabilities.Modes == nil || *s.Capabilities.Modes {
		t.Fatalf("a runtime without modes lists modes false: %+v", s.Capabilities)
	}
}

func TestRuntimeItemsAndDeltasReachTheHub(t *testing.T) {
	n, hub := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-4"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-4"]; return ok })
	if _, err := nodeCall(t, n, "session.watch", map[string]any{"session_id": "session-4", "since_seq": 5}); err != nil {
		t.Fatal(err)
	}
	if w := host.got("session.watch"); len(w) != 1 {
		t.Fatal("watch not forwarded")
	}
	_ = host.peer.Notify("session.items", map[string]any{"session_id": "session-4", "from_seq": 5, "to_seq": 7, "history": "h",
		"items": []proto.Item{{ID: "a", Seq: 7, Role: "assistant", Kind: proto.KindText, Text: "done"}}})
	_ = host.peer.Notify("session.delta", map[string]any{"session_id": "session-4", "text": "do"})
	_ = host.peer.Notify("session.items", map[string]any{"session_id": "someone-else", "to_seq": 1})
	waitFor(t, "items and delta", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.items) == 1 && len(hub.deltas) == 1
	})
	time.Sleep(50 * time.Millisecond)
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.items) != 1 {
		t.Fatalf("items for a session the host does not list were forwarded: %d", len(hub.items))
	}
	if n.runtimes.watching["session-4"] != 7 {
		t.Fatalf("watched seq %d", n.runtimes.watching["session-4"])
	}
}

func TestRuntimeRewatchesWhenItComesBack(t *testing.T) {
	n, _ := runtimeNode(t)
	first := dialRuntime(t, n, dshHello())
	_ = first.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-5"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-5"]; return ok })
	if _, err := nodeCall(t, n, "session.watch", map[string]any{"session_id": "session-5", "since_seq": 12}); err != nil {
		t.Fatal(err)
	}
	_ = first.conn.Close()
	waitFor(t, "unlisted", func() bool { _, ok := listed(t, n)["session-5"]; return !ok })

	second := dialRuntime(t, n, dshHello())
	_ = second.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-5"}}})
	waitFor(t, "watched again", func() bool { return len(second.got("session.watch")) == 1 })
	var p struct {
		SinceSeq int `json:"since_seq"`
	}
	_ = json.Unmarshal(second.got("session.watch")[0], &p)
	if p.SinceSeq != 12 {
		t.Fatalf("rewatched since %d", p.SinceSeq)
	}
}

func TestRuntimeInteractionGoesToThePhone(t *testing.T) {
	n, hub := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-6", "title": "deploy", "cwd": "/w/app"}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-6"]; return ok })
	allow := true
	hub.mu.Lock()
	hub.resolve = func(ix proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow} }
	hub.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var res proto.Resolution
	err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
		"kind": "permission", "session_id": "session-6", "tool_name": "bash", "tool_use_id": "call_1",
		"tool_input": map[string]any{"command": "rm -rf build"}, "summary": "rm -rf build"}}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if res.Allow == nil || !*res.Allow {
		t.Fatalf("resolution %+v", res)
	}
	hub.mu.Lock()
	ix := hub.asked[0]
	hub.mu.Unlock()
	if ix.ID == "" || ix.SessionTitle != "deploy" || ix.Cwd != "/w/app" || !ix.Resumable || ix.ExpiresAt.IsZero() {
		t.Fatalf("asked %+v", ix)
	}
	if ix.Agent != "dsh" || ix.AgentLabel != "DSH" { // drawn as DSH's card, not Claude's (whose "Always Allow in Session" DSH cannot keep)
		t.Fatalf("agent %q label %q", ix.Agent, ix.AgentLabel)
	}

	// Asked again for the same tool call (through the next node): the same id, so the hub hangs it on the same card.
	_ = host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
		"kind": "permission", "session_id": "session-6", "tool_name": "bash", "tool_use_id": "call_1"}}, &res)
	hub.mu.Lock()
	again := hub.asked[1].ID
	hub.mu.Unlock()
	if again != ix.ID {
		t.Fatalf("ids %s / %s", ix.ID, again)
	}

	hub.mu.Lock()
	hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Dismiss: true} }
	hub.mu.Unlock()
	res = proto.Resolution{}
	_ = host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
		"kind": "question", "session_id": "session-6", "questions": []map[string]any{{"question": "Which?", "options": []map[string]any{{"label": "A"}}}}}}, &res)
	if !res.Dismiss {
		t.Fatalf("a dismissed card answers dismiss: %+v", res)
	}
	if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
		"kind": "permission", "session_id": "not-listed"}}, &res); err == nil {
		t.Fatal("asked for a session the host does not list")
	}
}

func TestRuntimeStartFromTheLauncher(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	host.answer = func(method string, params json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": map[string]any{"session_id": "session-new", "cwd": "/w/app", "status": "idle"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	launcherCache.Lock()
	launcherCache.at, launcherCache.list = time.Now(), []proto.Launcher{{Name: "claude"}}
	launcherCache.Unlock()
	t.Cleanup(func() {
		launcherCache.Lock()
		launcherCache.at, launcherCache.list = time.Time{}, nil
		launcherCache.Unlock()
	})
	raw, err := nodeCall(t, n, "launchers.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var ls struct {
		Launchers []proto.Launcher `json:"launchers"`
	}
	_ = json.Unmarshal(raw, &ls)
	if len(ls.Launchers) != 2 || ls.Launchers[1].Name != "dsh" || ls.Launchers[1].Source != "dsh" || ls.Launchers[1].Agent != "dsh" {
		t.Fatalf("launchers %s", raw)
	}
	raw, err = nodeCall(t, n, "session.start", map[string]any{"cwd": "/w/app", "launcher": "dsh", "async": true})
	if err != nil {
		t.Fatal(err)
	}
	var started struct {
		Session proto.Session `json:"session"`
	}
	_ = json.Unmarshal(raw, &started)
	if started.Session.SessionID != "session-new" || started.Session.Agent != "dsh" {
		t.Fatalf("started %s", raw)
	}
	if _, ok := listed(t, n)["session-new"]; !ok {
		t.Fatal("not listed right after the start")
	}
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": "session-new", "text": "go"}); err != nil {
		t.Fatal(err)
	}
}

func TestPollAgentsPublishesWithoutHerdr(t *testing.T) {
	n, hub := runtimeNode(t)
	n.events = newHerdrEvents()
	n.metas = map[string]quickMeta{}
	n.bgScans = map[string]*bgScan{}
	n.panePIDs = map[string]panePID{}
	n.modes = map[string]sessionModes{}
	n.driving = map[string]bool{}
	n.panes = map[string]string{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.pollAgents(ctx)
	// The host's list changes without a runtime.sessions push reaching publish directly: the poll has to carry it.
	h := n.runtimes.add(nil)
	h.mu.Lock()
	h.ready, h.hello.Runtime = true, "dsh"
	h.mu.Unlock()
	n.runtimes.setSessions(h, []proto.Session{{SessionID: "session-poll", Status: proto.StatusIdle, Agent: "dsh"}})
	waitFor(t, "a list pushed by the poll with herdr missing", func() bool {
		for _, s := range hub.lastList() {
			if s.SessionID == "session-poll" {
				return true
			}
		}
		return false
	})
}

// A runtime's session can list the subagents and background tasks it left running (DSH's subagents, workflows,
// background shells): the rows keep the valid ones, each subagent readable as "<session>@<child>"; the calls and
// pushes about a subagent go to (and come from) the runtime of its parent, whatever it is not.
func TestRuntimeSubagentsAndBackground(t *testing.T) {
	n, hub := runtimeNode(t)
	host := dialRuntime(t, n, dshHello())
	host.answer = func(method string, params json.RawMessage) (any, error) {
		if method == "session.items" {
			return map[string]any{"items": []proto.Item{{ID: "1#0", Seq: 1, Role: "assistant", Kind: proto.KindText, Text: "from the child"}},
				"has_more": false, "history": "dsh:session-7@child-1", "to_seq": 1}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	started := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{
		"session_id": "session-7", "status": "done",
		"subagents": []map[string]any{
			{"id": "child-1", "session_id": "whatever", "description": "Map the code", "running": true, "tool_use_id": "call-1", "started_at": started,
				"model": strings.Repeat("m", maxRuntimeTaskText+1), "tokens": 1234},
			{"id": "../bad", "description": "not a valid id", "running": true},
			{"id": "child-1", "description": "the same child twice"},
			{"id": "child-2", "description": "done already", "running": false},
		},
		"background": []map[string]any{
			{"id": "workflow-1", "kind": "agent", "description": "review", "output_file": "/etc/passwd", "started_at": started},
			{"id": "bash-1", "kind": "anything", "command": "sleep 60"},
			{"id": " ", "kind": "shell"},
		},
	}}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-7"]; return ok })
	s := listed(t, n)["session-7"]
	if len(s.Subagents) != 2 || s.Subagents[0].SessionID != "session-7@child-1" || !s.Subagents[0].Running ||
		s.Subagents[0].ToolUseID != "call-1" || !s.Subagents[0].StartedAt.Equal(started) || s.Subagents[1].SessionID != "session-7@child-2" {
		t.Fatalf("subagents %+v", s.Subagents)
	}
	if a := s.Subagents[0]; a.Model != strings.Repeat("m", maxRuntimeTaskText)+"…" || a.Tokens != 1234 { // #5030: its model clipped like its text
		t.Fatalf("subagent model %q tokens %d", a.Model, a.Tokens)
	}
	if len(s.Background) != 2 || s.Background[0].Kind != "agent" || s.Background[0].OutputFile != "" || s.Background[1].Kind != "shell" {
		t.Fatalf("background %+v", s.Background)
	}

	// Paging and watching the subagent go to the parent's runtime with the subagent's id.
	raw, err := nodeCall(t, n, "session.items", map[string]any{"session_id": "session-7@child-1", "turns": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "from the child") {
		t.Fatalf("page %s", raw)
	}
	var p struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(host.got("session.items")[0], &p)
	if p.SessionID != "session-7@child-1" {
		t.Fatalf("asked the runtime about %q", p.SessionID)
	}
	if _, err := nodeCall(t, n, "session.watch", map[string]any{"session_id": "session-7@child-1", "since_seq": 0}); err != nil {
		t.Fatal(err)
	}
	_ = host.peer.Notify("session.items", map[string]any{"session_id": "session-7@child-1", "from_seq": 1, "to_seq": 2, "history": "h",
		"items": []proto.Item{{ID: "2#0", Seq: 2, Role: "assistant", Kind: proto.KindText, Text: "more"}}})
	_ = host.peer.Notify("session.delta", map[string]any{"session_id": "session-7@child-1", "text": "mo"})
	_ = host.peer.Notify("session.items", map[string]any{"session_id": "session-8@child-1", "to_seq": 1}) // not its session
	waitFor(t, "the subagent's items and live text at the hub", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.items) == 1 && len(hub.deltas) == 1
	})
	time.Sleep(50 * time.Millisecond)
	hub.mu.Lock()
	got := len(hub.items)
	hub.mu.Unlock()
	if got != 1 {
		t.Fatalf("pushes for a subagent of a session the runtime does not list went through: %d", got)
	}

	// An id that only looks like a subagent's is no runtime's.
	for _, id := range []string{"session-7@", "@child-1", "session-7@a/b"} {
		if h, _, _ := n.runtimes.belongs(id); h != nil {
			t.Fatalf("%q belongs to a runtime", id)
		}
	}
}

// A session of a runtime's that another of its sessions started (is_subagent, parent_session_id: an agent another
// Paseo agent created, #4494) is listed under it, with its root filled in, however deep. One that waits for the user
// is a row of its own while it does: it names no parent, so no app folds it away or leaves it out of a count.
func TestRuntimeSessionAnotherStarted(t *testing.T) {
	n, _ := runtimeNode(t)
	host := dialRuntime(t, n, paseoHello())
	rows := func(leaf string) []map[string]any {
		return []map[string]any{
			{"session_id": "lead", "status": "idle", "cwd": "/w/app"},
			{"session_id": "mid", "status": "idle", "cwd": "/w/app", "is_subagent": true, "parent_session_id": "lead"},
			{"session_id": "leaf", "status": leaf, "cwd": "/w/app", "is_subagent": true, "parent_session_id": "mid", "root_session_id": "lead"},
		}
	}
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows("working")})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["leaf"]; return ok })
	got := listed(t, n)
	if mid, leaf := got["mid"], got["leaf"]; !mid.IsSubagent || mid.ParentSessionID != "lead" || mid.RootSessionID != "lead" ||
		!leaf.IsSubagent || leaf.ParentSessionID != "mid" || leaf.RootSessionID != "lead" || got["lead"].IsSubagent {
		t.Fatalf("mid under %q (root %q), leaf under %q (root %q)", mid.ParentSessionID, mid.RootSessionID, leaf.ParentSessionID, leaf.RootSessionID)
	}

	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows("blocked")})
	waitFor(t, "waiting for the user", func() bool { return listed(t, n)["leaf"].Status == proto.StatusBlocked })
	got = listed(t, n)
	if leaf := got["leaf"]; leaf.IsSubagent || leaf.ParentSessionID != "" || leaf.RootSessionID != "" {
		t.Fatalf("a session that waits for the user still names a parent: %+v", leaf)
	}
	if mid := got["mid"]; !mid.IsSubagent || mid.ParentSessionID != "lead" {
		t.Fatalf("the others keep theirs: %+v", mid)
	}

	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": rows("idle")})
	waitFor(t, "answered: under its parent again", func() bool { s := listed(t, n)["leaf"]; return s.ParentSessionID == "mid" && s.RootSessionID == "lead" })
}

// A runtime running another version of a package the node carries is offered the node's in the hello reply, and
// fetches it with runtime.package; one running the same is not (docs/integrations/runtimes.md "Auto-update").
func TestRuntimePackageUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(runtimepkg.OverrideEnv, dir)
	root := filepath.Join(dir, "persona-dsh-runtime")
	for path, data := range map[string]string{"dsh/lib/plugin.js": "export const api = 1", "runtime-sdk/index.js": "export {}"} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755)
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pkg, err := runtimepkg.Load("persona-dsh-runtime")
	if err != nil || pkg == nil {
		t.Fatal(err)
	}
	n, _ := runtimeNode(t)

	hello := dshHello()
	hello["package"] = map[string]any{"name": "persona-dsh-runtime", "hash": "an-older-one"}
	old := dialRuntime(t, n, hello)
	offer, _ := old.helloR["update"].(map[string]any)
	if offer["name"] != "persona-dsh-runtime" || offer["hash"] != pkg.Hash || offer["version"] == nil {
		t.Fatalf("hello answered %v", old.helloR)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got struct {
		Name  string `json:"name"`
		Hash  string `json:"hash"`
		Files []struct {
			Path string `json:"path"`
			Data string `json:"data"`
		} `json:"files"`
	}
	if err := old.peer.Call(ctx, "runtime.package", map[string]any{"name": "persona-dsh-runtime"}, &got); err != nil {
		t.Fatal(err)
	}
	var files []runtimepkg.File
	for _, f := range got.Files {
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, runtimepkg.File{Path: f.Path, Data: data})
	}
	if got.Name != "persona-dsh-runtime" || got.Hash != pkg.Hash || runtimepkg.Hash(files) != pkg.Hash || len(files) != 2 {
		t.Fatalf("package %s %s, %d files", got.Name, got.Hash, len(files))
	}
	if err := old.peer.Call(ctx, "runtime.package", map[string]any{"name": "something-else"}, &got); err == nil {
		t.Fatal("a package the node does not carry was served")
	}

	// Running the node's own: nothing offered. Without a package (an older runtime): nothing either.
	same := dshHello()
	same["runtime"], same["launcher"] = "dsh2", "dsh2"
	same["package"] = map[string]any{"name": "persona-dsh-runtime", "hash": pkg.Hash}
	if r := dialRuntime(t, n, same); r.helloR["update"] != nil {
		t.Fatalf("offered its own package: %v", r.helloR)
	}
	plain := dshHello()
	plain["runtime"], plain["launcher"] = "dsh3", "dsh3"
	if r := dialRuntime(t, n, plain); r.helloR["update"] != nil {
		t.Fatalf("offered a package to a runtime without one: %v", r.helloR)
	}
}

// "Always Allow in Session" on a runtime's card (#3974): the node names the grant, never the runtime: one key for this runtime's
// tool, only on a permission card that offers it, never in the health space or on a card with fields.
func TestRuntimeAskAlwaysKeys(t *testing.T) {
	n, hub := runtimeNode(t)
	hello := dshHello()
	hello["caps"].(map[string]any)["keep_always"] = true // DSH asks the hub to keep it (caps.keep_always)
	host := dialRuntime(t, n, hello)
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{
		{"session_id": "session-7", "title": "deploy", "cwd": "/w/app"},
		{"session_id": "session-h", "title": "health", "cwd": "/home/me/.persona/health"},
	}})
	waitFor(t, "listed", func() bool { m := listed(t, n); _, a := m["session-7"]; _, b := m["session-h"]; return a && b })
	allow := true
	hub.mu.Lock()
	hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow, Always: true} }
	hub.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ask := func(ix map[string]any) proto.Interaction {
		t.Helper()
		var res proto.Resolution
		if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": ix}, &res); err != nil {
			t.Fatal(err)
		}
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.asked[len(hub.asked)-1]
	}
	for _, c := range []struct {
		name string
		ix   map[string]any
		want []string
	}{
		{"offered", map[string]any{"kind": "permission", "session_id": "session-7", "tool_name": "Bash", "tool_use_id": "c1",
			"always_label": "本会话不再问 Bash"}, []string{"runtime:dsh:tool:Bash"}},
		{"the runtime's own keys are dropped", map[string]any{"kind": "permission", "session_id": "session-7", "tool_name": "Bash",
			"tool_use_id": "c2", "always_label": "本会话不再问 Bash", "always_keys": []string{"tool:*", "runtime:dsh:tool:Edit"}},
			[]string{"runtime:dsh:tool:Bash"}},
		{"not offered", map[string]any{"kind": "permission", "session_id": "session-7", "tool_name": "Bash", "tool_use_id": "c3",
			"always_keys": []string{"runtime:dsh:tool:Bash"}}, nil},
		{"no tool", map[string]any{"kind": "permission", "session_id": "session-7", "tool_use_id": "c4", "always_label": "x"}, nil},
		{"a card with fields", map[string]any{"kind": "permission", "session_id": "session-7", "tool_name": "Bash", "tool_use_id": "c5",
			"always_label": "x", "fields": []map[string]any{{"key": "pw", "label": "密码", "secret": true}}}, nil},
		{"the health space", map[string]any{"kind": "permission", "session_id": "session-h", "tool_name": "Bash", "tool_use_id": "c6",
			"always_label": "x"}, nil},
		{"a question", map[string]any{"kind": "question", "session_id": "session-7", "tool_use_id": "c7", "always_label": "x",
			"always_keys": []string{"runtime:dsh:tool:Bash"},
			"questions":   []map[string]any{{"question": "Which?", "options": []map[string]any{{"label": "A"}}}}}, nil},
	} {
		if got := ask(c.ix); !slices.Equal(got.AlwaysKeys, c.want) {
			t.Errorf("%s: always keys %q, want %q", c.name, got.AlwaysKeys, c.want)
		}
	}
}

// A runtime that did not ask the hub to keep its grants gets no key, whatever its card offers: opencode's always is its
// own save rule and Paseo's its suggestions (Bash(npm test)), narrower than the whole tool (#3974 review).
func TestRuntimeAskAlwaysKeysOnlyWhenAsked(t *testing.T) {
	for _, c := range []struct {
		runtime, label string
	}{
		{"opencode", "总是允许 npm test *"},
		{"paseo", "这个项目不再问：Bash(npm test)"},
		{"dsh", "本会话不再问 Bash"}, // an older DSH plugin, without caps.keep_always
	} {
		t.Run(c.runtime, func(t *testing.T) {
			n, hub := runtimeNode(t)
			host := dialRuntime(t, n, map[string]any{"runtime": c.runtime, "name": c.runtime, "version": "test",
				"caps": map[string]any{"send": true}})
			_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{{"session_id": "session-8", "cwd": "/w/app"}}})
			waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-8"]; return ok })
			allow := true
			hub.mu.Lock()
			hub.resolve = func(proto.Interaction) proto.Resolution { return proto.Resolution{Allow: &allow, Always: true} }
			hub.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var res proto.Resolution
			if err := host.peer.Call(ctx, "interaction.create", map[string]any{"interaction": map[string]any{
				"kind": "permission", "session_id": "session-8", "tool_name": "Bash", "tool_use_id": "c1",
				"always_label": c.label, "always_keys": []string{"runtime:" + c.runtime + ":tool:Bash"}}}, &res); err != nil {
				t.Fatal(err)
			}
			hub.mu.Lock()
			got := hub.asked[len(hub.asked)-1]
			hub.mu.Unlock()
			if len(got.AlwaysKeys) != 0 {
				t.Fatalf("always keys %q, want none", got.AlwaysKeys)
			}
			if got.AlwaysLabel != c.label {
				t.Fatalf("the runtime's own always offer was changed: %q", got.AlwaysLabel)
			}
		})
	}
}
