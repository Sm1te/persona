package node

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// fakeCodexBus plays the Codex App's IPC bus: it answers initialize, asks the new client a discovery question (as the
// App does for other clients' requests), and has one window owning `owned`.
type fakeCodexBus struct {
	owned  string
	opened atomic.Bool // the App was asked to open `later`: from then on it owns it too
	later  string
	turns  chan map[string]any
	asked  chan bool // the client answered the bus's discovery question
}

func startFakeCodexBus(t *testing.T, owned string) (home string, bus *fakeCodexBus) {
	t.Helper()
	// The home of an App: its sidebar state is there (on Windows the bus is found only next to one). A test that
	// wants another writes its own.
	home, l := listenFakeCodexBus(t) // codex_desktop_unix_test.go, codex_desktop_windows_test.go
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	bus = &fakeCodexBus{owned: owned, turns: make(chan map[string]any, 1), asked: make(chan bool, 1)}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go bus.serve(conn)
		}
	}()
	return home, bus
}

func busWrite(conn net.Conn, v any) {
	b, _ := json.Marshal(v)
	head := make([]byte, 4)
	binary.LittleEndian.PutUint32(head, uint32(len(b)))
	conn.Write(append(head, b...))
}

func busRead(conn net.Conn) (map[string]any, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return nil, err
	}
	b := make([]byte, binary.LittleEndian.Uint32(head))
	if _, err := io.ReadFull(conn, b); err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m)
}

func (b *fakeCodexBus) serve(conn net.Conn) {
	defer conn.Close()
	for {
		m, err := busRead(conn)
		if err != nil {
			return
		}
		id, _ := m["requestId"].(string)
		method, _ := m["method"].(string)
		version, _ := m["version"].(float64)
		params, _ := m["params"].(map[string]any)
		ok := func(result any) {
			busWrite(conn, map[string]any{"type": "response", "requestId": id, "resultType": "success", "method": method, "handledByClientId": "window", "result": result})
		}
		fail := func() {
			busWrite(conn, map[string]any{"type": "response", "requestId": id, "resultType": "error", "error": "no-client-found"})
		}
		switch {
		case m["type"] == "client-discovery-response":
			b.asked <- true
		case method == "initialize":
			ok(map[string]any{"clientId": "persona"})
			busWrite(conn, map[string]any{"type": "client-discovery-request", "requestId": "q1", "request": map[string]any{"method": "ide-context"}})
		case method == "thread-owner-discovery" && version == 1 && (params["conversationId"] == b.owned || b.opened.Load() && params["conversationId"] == b.later):
			ok(map[string]any{"supportsUntrustedAppInput": true})
		case method == "thread-follower-start-turn" && version == 2 && m["targetClientId"] == "window" && (params["conversationId"] == b.owned || b.opened.Load() && params["conversationId"] == b.later):
			b.turns <- params
			ok(map[string]any{"result": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}})
		default:
			fail()
		}
	}
}

func TestSendCodexDesktopRunsTurnInOwningWindow(t *testing.T) {
	home, bus := startFakeCodexBus(t, "thread-a")
	if !codexDesktopAvailable(home) {
		t.Fatal("the bus socket is not seen")
	}
	n := &Node{dir: t.TempDir()}
	if err := n.sendCodexDesktop(context.Background(), codexSource{home: home, thread: "thread-a"}, "hello", nil); err != nil {
		t.Fatal(err)
	}
	p := <-bus.turns
	req := p["turnStart"].(map[string]any)["request"].(map[string]any)
	in := req["input"].([]any)[0].(map[string]any)
	if req["threadId"] != "thread-a" || in["type"] != "text" || in["text"] != "hello" || req["clientUserMessageId"] == "" {
		t.Fatalf("turn request = %v", req)
	}
	select {
	case <-bus.asked:
	default:
		t.Fatal("the bus's discovery question was not answered")
	}
}

func TestSendCodexDesktopOpensAThreadTheAppHasNotLoaded(t *testing.T) {
	home, bus := startFakeCodexBus(t, "thread-a")
	bus.later = "thread-new"
	defer func(f func(string) error) { openCodexThread = f }(openCodexThread)
	var asked string
	openCodexThread = func(thread string) error { asked = thread; bus.opened.Store(true); return nil }
	n := &Node{dir: t.TempDir()}
	if err := n.sendCodexDesktop(context.Background(), codexSource{home: home, thread: "thread-new"}, "hi", nil); err != nil {
		t.Fatal(err)
	}
	if asked != "thread-new" {
		t.Fatalf("opened %q", asked)
	}
	if p := <-bus.turns; p["conversationId"] != "thread-new" {
		t.Fatalf("turn went to %v", p["conversationId"])
	}
}

func TestSendCodexDesktopRefusesThreadTheAppHasNotLoaded(t *testing.T) {
	home, bus := startFakeCodexBus(t, "thread-a")
	defer func(f func(string) error, w time.Duration) { openCodexThread, codexDesktopLoadWait = f, w }(openCodexThread, codexDesktopLoadWait)
	openCodexThread = func(string) error { return nil } // the App never loads it
	codexDesktopLoadWait = 2 * time.Second
	n := &Node{dir: t.TempDir()}
	err := n.sendCodexDesktop(context.Background(), codexSource{home: home, thread: "thread-b"}, "hello", nil)
	if code, data := refusalOf(t, err); code != "codex_desktop_not_loaded" || data["delivered"] != deliveredNone {
		t.Fatalf("refusal = %s %v", code, data)
	}
	select {
	case p := <-bus.turns:
		t.Fatalf("a turn was started: %v", p)
	default:
	}
}

func TestCodexDesktopAvailableNeedsTheSocket(t *testing.T) {
	noFakeCodexBus(t)
	if codexDesktopAvailable(t.TempDir()) || codexDesktopAvailable("") {
		t.Fatal("no App bus here")
	}
}

func TestCodexDesktopTurnFromRolloutTail(t *testing.T) {
	n := &Node{}
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	write := func(lines ...string) {
		f, _ := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		for _, l := range lines {
			f.WriteString(l + "\n")
		}
		f.Close()
	}
	write(`{"type":"session_meta","payload":{}}`, `{"type":"event_msg","payload":{"type":"task_started"}}`)
	if got := n.codexDesktopTurn(p); got != proto.StatusWorking {
		t.Fatalf("running turn = %s", got)
	}
	write(`{"type":"event_msg","payload":{"type":"task_complete"}}`)
	if got := n.codexDesktopTurn(p); got != proto.StatusDone {
		t.Fatalf("finished turn = %s", got)
	}
}

func TestCodexDesktopFollowsTheAppsSidebar(t *testing.T) {
	home := t.TempDir()
	state := `{
		"electron-thread-read-state-v1": {"version": 1, "unreadByIdentity": {"acct": {"local:h": ["t-unread"]}}},
		"projectless-thread-ids": ["t-chat"],
		"thread-project-assignments": {"t-assigned": {"projectKind": "local", "projectId": "p1"}},
		"local-projects": {"p1": {"name": "Alpha", "rootPaths": ["/work/alpha"]}, "p2": {"name": "Beta", "rootPaths": ["/work/beta"]}}
	}`
	os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(state), 0o600)
	done := filepath.Join(home, "done.jsonl")
	os.WriteFile(done, []byte(`{"type":"event_msg","payload":{"type":"task_started"}}`+"\n"+`{"type":"event_msg","payload":{"type":"task_complete"}}`+"\n"), 0o600)
	n := &Node{home: home}
	now := time.Now()
	list := []proto.Session{
		{SessionID: "t-assigned", Cwd: "/elsewhere", UpdatedAt: now},
		{SessionID: "t-chat", Cwd: "/work/beta/x", UpdatedAt: now},
		{SessionID: "t-old", Cwd: "/work/beta/y", UpdatedAt: now.Add(-100 * time.Hour), Status: proto.StatusOffline},
		{SessionID: "t-unread", Cwd: "/work/beta", UpdatedAt: now},
	}
	for i := range list {
		n.placeCodexDesktop(&list[i], codexSource{home: home, thread: list[i].SessionID, path: done}, list[i].SessionID, false)
	}
	slotCodexDesktop(list)
	names := []string{list[0].SpaceName, list[1].SpaceName, list[2].SpaceName, list[3].SpaceName}
	if names[0] != "Alpha" || names[1] != "Codex 对话" || names[2] != "Beta" || names[3] != "Beta" {
		t.Fatalf("spaces = %v", names)
	}
	if list[3].Status != proto.StatusDone || list[0].Status != proto.StatusIdle || list[2].Status != proto.StatusOffline {
		t.Fatalf("status: unread %s, read %s, old %s", list[3].Status, list[0].Status, list[2].Status)
	}
	if list[3].SlotIndex != 0 || list[2].SlotIndex != 1 {
		t.Fatalf("slots: %d %d", list[3].SlotIndex, list[2].SlotIndex)
	}
}

func TestCodexOriginTellsAppChatsFromCLIAndSubagents(t *testing.T) {
	home, _ := startFakeCodexBus(t, "x")
	write := func(name, meta string) codexSource {
		p := filepath.Join(home, name+".jsonl")
		os.WriteFile(p, []byte(`{"type":"session_meta","payload":`+meta+"}\n"), 0o600)
		return codexSource{home: home, thread: name, path: p}
	}
	os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(`{"projectless-thread-ids":["app","work"]}`), 0o600)
	app := write("app", `{"originator":"Codex Desktop","source":"vscode"}`)
	work := write("work", `{"originator":"codex_work_desktop","source":"vscode"}`)
	tool := write("tool", `{"originator":"Codex Desktop","source":"vscode"}`)
	workTool := write("work-tool", `{"originator":"codex_work_desktop","source":"vscode"}`)
	cli := write("cli", `{"originator":"codex-tui","source":"cli"}`)
	sub := write("sub", `{"originator":"Codex Desktop","source":{"subagent":{"thread_spawn":{"parent_thread_id":"app","depth":1}}}}`)
	workSub := write("work-sub", `{"originator":"codex_work_desktop","source":{"subagent":{"thread_spawn":{"parent_thread_id":"work","depth":1}}}}`)
	if !codexDesktopChat(app) || !codexDesktopChat(work) || codexDesktopChat(cli) || codexDesktopChat(sub) || codexDesktopChat(workSub) || codexDesktopChat(tool) || codexDesktopChat(workTool) {
		t.Fatal("only a chat the App lists goes through its bus")
	}
	if o := readCodexOrigin(cli.path); !o.cli || o.desktop {
		t.Fatalf("cli origin = %+v", o)
	}
	if o := readCodexOrigin(sub.path); o.parent != "app" || !o.desktop {
		t.Fatalf("subagent origin = %+v", o)
	}
	if o := readCodexOrigin(workSub.path); o.parent != "work" || !o.desktop {
		t.Fatalf("work subagent origin = %+v", o)
	}
}

// A Codex thread's row says when the thread began: its rollout's first line, read once with its origin and shared by
// every row of it (#5120). A first line with no time says nothing.
func TestCodexRowStartedAt(t *testing.T) {
	n, home := testNode(t)
	write := func(name, line string) codexSource {
		p := filepath.Join(home, name+".jsonl")
		os.WriteFile(p, []byte(line+"\n"), 0o600)
		return codexSource{home: home, thread: name, path: p}
	}
	timed := write("timed", `{"timestamp":"2026-11-12T08:30:00.123Z","type":"session_meta","payload":{"originator":"codex-tui","source":"cli"}}`)
	untimed := write("untimed", `{"type":"session_meta","payload":{"originator":"codex-tui","source":"cli"}}`)
	row := func(src codexSource) proto.Session {
		s := proto.Session{SessionID: src.thread, Capabilities: &proto.SessionCapabilities{}}
		n.decorateCodexSession(&s, src, "", map[string]string{})
		return s
	}
	a, b := row(timed), row(timed)
	if want := time.Date(2026, 11, 12, 8, 30, 0, 123e6, time.UTC); !a.StartedAt().Equal(want) || a.Hierarchy != b.Hierarchy {
		t.Fatalf("started %v (shared %v), want %v", a.StartedAt(), a.Hierarchy == b.Hierarchy, want)
	}
	if s := row(untimed); s.Hierarchy != nil {
		t.Fatalf("no time, no hierarchy: %+v", s.Hierarchy)
	}
}

func TestFileFollowsAMovedTranscript(t *testing.T) {
	n, home := testNode(t)
	id := "0e000000-0000-4000-8000-000000000001"
	a, b := filepath.Join(home, "a.jsonl"), filepath.Join(home, "b.jsonl")
	os.WriteFile(a, []byte(`{"type":"session_meta","payload":{}}`+"\n"), 0o600)
	n.paths[id] = a
	f, err := n.file(id)
	if err != nil {
		t.Fatal(err)
	}
	os.Rename(a, b)
	n.paths[id] = b
	if g, _ := n.file(id); g != f || g.Where() != b {
		t.Fatalf("still reading %s", f.Where())
	}
}
