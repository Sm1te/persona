package node

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// assistantRuntimeHello is a runtime whose launcher can run the assistant (caps.assistant).
func assistantRuntimeHello() map[string]any {
	return map[string]any{"runtime": "dsh", "name": "DSH", "launcher": "dsh",
		"caps": map[string]any{"start": true, "send": true, "interrupt": true, "assistant": true}}
}

type hubCalls struct {
	mu    sync.Mutex
	tools []json.RawMessage
	calls []json.RawMessage
}

// assistantHub answers what the hub answers an assistant on a runtime: the snapshot, its tools, their calls.
func assistantHub(hub *runtimeHub) *hubCalls {
	c := &hubCalls{}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.answer = func(method string, params json.RawMessage) (any, error, bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch method {
		case "assistant.context":
			return map[string]any{"text": "- 现在：测试时间\n- 在线电脑：测试机"}, nil, true
		case "assistant.lark":
			return map[string]any{}, nil, true
		case "mcp.tools":
			c.tools = append(c.tools, params)
			var p struct {
				Assistant bool `json:"assistant"`
			}
			_ = json.Unmarshal(params, &p)
			tools := []proto.Tool{{Name: "phone_contacts", InputSchema: json.RawMessage(`{"type":"object"}`)}}
			if p.Assistant {
				tools = append(tools, proto.Tool{Name: "ask_user", InputSchema: json.RawMessage(`{"type":"object"}`)})
			}
			return map[string]any{"tools": tools}, nil, true
		case "mcp.call":
			c.calls = append(c.calls, params)
			return proto.TextResult("ok", false), nil, true
		}
		return nil, nil, false
	}
	return c
}

func startedAssistant(t *testing.T, n *Node, host *fakeRuntime) string {
	t.Helper()
	host.answer = func(method string, params json.RawMessage) (any, error) {
		if method == "session.start" {
			return map[string]any{"session": map[string]any{"session_id": "session-a1", "cwd": filepath.Join(n.home, ".persona", "assistant"), "status": "idle"}}, nil
		}
		return map[string]any{"ok": true}, nil
	}
	raw, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": filepath.Join(n.home, ".persona", "assistant"), "text": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.SessionID != "session-a1" {
		t.Fatalf("assistant.send %s", raw)
	}
	return res.SessionID
}

func TestAssistantOnARuntimeStartsAndSendsWithTheSnapshot(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	if a, err := n.assistantAgent(context.Background(), "dsh"); err != nil || a != assistantOnRuntime {
		t.Fatalf("assistantAgent: %q %v", a, err)
	}
	sid := startedAssistant(t, n, host)

	starts := host.got("session.start")
	if len(starts) != 1 || !strings.Contains(string(starts[0]), `"assistant":true`) {
		t.Fatalf("session.start %s", starts)
	}
	sends := host.got("session.send")
	if len(sends) != 1 {
		t.Fatalf("%d sends", len(sends))
	}
	var p struct {
		SessionID string `json:"session_id"`
		Text      string `json:"text"`
		Context   string `json:"context"`
		Assistant bool   `json:"assistant"`
		MemoryDir string `json:"memory_dir"`
	}
	_ = json.Unmarshal(sends[0], &p)
	if p.SessionID != sid || p.Text != "hello" || !p.Assistant || p.MemoryDir == "" {
		t.Fatalf("send %s", sends[0])
	}
	if !strings.Contains(p.Context, "测试时间") || !strings.Contains(p.Context, "MEMORY.md") {
		t.Fatalf("the first message's context: %q", p.Context)
	}
	if st, ok := n.state.get(sid); !ok || st.Launcher != "dsh" || !strings.HasSuffix(st.Cwd, "/.persona/assistant") {
		t.Fatalf("state %+v", st)
	}
	if !n.isAssistantSession(sid) || !n.runtimeAssistant(sid) {
		t.Fatal("not known as an assistant conversation")
	}

	// The phone's message to it is the assistant's: the snapshot again (only what changed) and the mode.
	if _, err := nodeCall(t, n, "session.modes", map[string]any{"session_id": sid, "permission_mode": "plan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": sid, "text": "next", "client_msg_id": "m-2"}); err != nil {
		t.Fatal(err)
	}
	sends = host.got("session.send")
	if len(sends) != 2 {
		t.Fatalf("%d sends", len(sends))
	}
	var q struct {
		Text      string `json:"text"`
		Mode      string `json:"mode"`
		Assistant bool   `json:"assistant"`
		Context   string `json:"context"`
	}
	_ = json.Unmarshal(sends[1], &q)
	if q.Text != "next" || q.Mode != "plan" || !q.Assistant || strings.Contains(q.Context, "MEMORY.md") || !strings.Contains(q.Context, "测试时间") {
		t.Fatalf("second send %s", sends[1])
	}
	if s := listed(t, n)[sid]; s.PermissionMode != "plan" {
		t.Fatalf("listed mode %q", s.PermissionMode)
	}

	// A message without a launcher (the node forgot the conversation's state: a restart) still reaches its runtime.
	n.state.put(sid, sessionState{})
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "text": "after a restart"}); err != nil {
		t.Fatalf("without a launcher: %v", err)
	}
	if got := host.got("session.send"); len(got) != 3 || !strings.Contains(string(got[2]), "after a restart") {
		t.Fatalf("sends %d", len(got))
	}

	// Stop from the phone: the runtime's interrupt.
	if _, err := nodeCall(t, n, "session.interrupt", map[string]any{"session_id": sid}); err != nil || len(host.got("session.interrupt")) != 1 {
		t.Fatalf("interrupt: %v", err)
	}
	// Another kind of agent asked for (the main conversation follows "Model settings › Main chat"): the conversation stays on its
	// runtime, the only one that can resume it.
	if _, err := nodeCall(t, n, "assistant.send", map[string]any{"session_id": sid, "launcher": "claude", "text": "on claude"}); err != nil {
		t.Fatalf("a claude launcher for a runtime conversation: %v", err)
	}
	if got := host.got("session.send"); len(got) != 4 || !strings.Contains(string(got[3]), "on claude") {
		t.Fatalf("sends %d", len(got))
	}
}

// A runtime may put the client_msg_id it was sent a message with on the message's line (the node-to-runtime
// session.send, as Paseo does). For an assistant conversation that id is the message's own: the app's, or none when it
// came without one, so the apps match their pending bubble by it, or by its words. The node used to send every message
// under an id of its own, which such a runtime put on the line: the app kept its bubble and showed the message twice
// (#5225, as #5200).
func TestAssistantOnARuntimeSendsTheMessagesOwnID(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	var mu sync.Mutex
	started, seq := 0, map[string]int{}
	host.answer = func(method string, params json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "session.start":
			started++
			return map[string]any{"session": map[string]any{"session_id": fmt.Sprintf("session-a%d", started), "cwd": filepath.Join(n.home, ".persona", "assistant"), "status": "idle"}}, nil
		case "session.send": // a runtime that ties its own line: the user's line carries the id it was sent with
			var p struct {
				SessionID   string `json:"session_id"`
				Text        string `json:"text"`
				ClientMsgID string `json:"client_msg_id"`
			}
			_ = json.Unmarshal(params, &p)
			seq[p.SessionID]++
			s := seq[p.SessionID]
			_ = host.peer.Notify("session.items", map[string]any{"session_id": p.SessionID, "from_seq": s - 1, "to_seq": s,
				"items": []proto.Item{{ID: fmt.Sprintf("%d#0", s), Seq: s, Role: "user", Kind: proto.KindText, Text: p.Text, ClientMsgID: p.ClientMsgID}}})
		}
		return map[string]any{"ok": true}, nil
	}
	// pushed is the client_msg_id the hub got on the line with text, once it got it.
	pushed := func(text string) string {
		t.Helper()
		var id string
		waitFor(t, "the line "+text, func() bool {
			hub.mu.Lock()
			defer hub.mu.Unlock()
			for _, raw := range hub.items {
				var b struct {
					Items []proto.Item `json:"items"`
				}
				_ = json.Unmarshal(raw, &b)
				for _, it := range b.Items {
					if it.Text == text {
						id = it.ClientMsgID
						return true
					}
				}
			}
			return false
		})
		return id
	}
	// sentID is the client_msg_id the runtime got the message with text under; absent is "", never a key with "".
	sentID := func(text string) string {
		t.Helper()
		for _, raw := range host.got("session.send") {
			var p map[string]any
			_ = json.Unmarshal(raw, &p)
			if p["text"] != text {
				continue
			}
			id, has := p["client_msg_id"]
			if !has {
				return ""
			}
			if s, _ := id.(string); s != "" {
				return s
			}
			t.Fatalf("%q went with client_msg_id %v", text, id)
		}
		t.Fatalf("%q never reached the runtime", text)
		return ""
	}
	assistantDir := filepath.Join(n.home, ".persona", "assistant")

	// A new conversation's first message as the manager sends it (no id since #5200): the line carries none, and the
	// app finds it by its words.
	raw, err := nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": assistantDir, "text": "第一条"})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(raw, &res)
	sid := res.SessionID
	if sid != "session-a1" || !n.runtimeAssistant(sid) {
		t.Fatalf("assistant.send %s", raw)
	}
	if got, line := sentID("第一条"), pushed("第一条"); got != "" || line != "" {
		t.Fatalf("a first message without an id went with %q, its line carries %q", got, line)
	}

	// The app's message, with its id: the runtime gets that id, and the line carries it.
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": sid, "text": "第二条", "client_msg_id": "app-2"}); err != nil {
		t.Fatal(err)
	}
	if got, line := sentID("第二条"), pushed("第二条"); got != "app-2" || line != "app-2" {
		t.Fatalf("the app's message went with %q, its line carries %q; want app-2", got, line)
	}

	// An older app's message, without one: none on the line either.
	if _, err := nodeCall(t, n, "session.send", map[string]any{"session_id": sid, "text": "第三条"}); err != nil {
		t.Fatal(err)
	}
	if got, line := sentID("第三条"), pushed("第三条"); got != "" || line != "" {
		t.Fatalf("a message without an id went with %q, its line carries %q", got, line)
	}

	// A new conversation's first message with its id (one a person's own app opened): the line carries it.
	raw, err = nodeCall(t, n, "assistant.send", map[string]any{"launcher": "dsh", "cwd": assistantDir, "text": "新对话", "client_msg_id": "app-new"})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &res)
	if res.SessionID != "session-a2" {
		t.Fatalf("assistant.send %s", raw)
	}
	if got, line := sentID("新对话"), pushed("新对话"); got != "app-new" || line != "app-new" {
		t.Fatalf("a first message with its id went with %q, its line carries %q; want app-new", got, line)
	}
}

func TestAssistantLauncherNeedsTheCapability(t *testing.T) {
	n, _ := runtimeNode(t)
	_ = dialRuntime(t, n, dshHello()) // a runtime without caps.assistant
	if n.assistantRuntime("dsh") != nil {
		t.Fatal("a runtime that did not declare assistant runs the assistant")
	}
	raw, _ := nodeCall(t, n, "launchers.list", map[string]any{})
	if strings.Contains(string(raw), `"assistant":true`) {
		t.Fatalf("launchers %s", raw)
	}
}

func TestAssistantToolsOnlyForTheAssistantsConversations(t *testing.T) {
	n, hub := runtimeNode(t)
	calls := assistantHub(hub)
	host := dialRuntime(t, n, assistantRuntimeHello())
	plain := dialRuntime(t, n, map[string]any{"runtime": "other", "caps": map[string]any{"send": true}})
	sid := startedAssistant(t, n, host)
	_ = host.peer.Notify("runtime.sessions", map[string]any{"sessions": []map[string]any{
		{"session_id": sid, "cwd": filepath.Join(n.home, ".persona", "assistant"), "status": "idle"},
		{"session_id": "session-w", "cwd": "/w/app", "status": "idle"},
	}})
	waitFor(t, "listed", func() bool { _, ok := listed(t, n)["session-w"]; return ok })

	toolNames := func(f *fakeRuntime, assistant bool) []string {
		var res struct {
			Tools []proto.Tool `json:"tools"`
		}
		if err := f.peer.Call(context.Background(), "mcp.tools", map[string]any{"assistant": assistant}, &res); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range res.Tools {
			out = append(out, x.Name)
		}
		return out
	}
	if got := toolNames(plain, true); strings.Contains(strings.Join(got, ","), "ask_user") {
		t.Fatalf("a runtime that does not run the assistant got %v", got)
	}
	if got := strings.Join(toolNames(host, true), ","); !strings.Contains(got, "ask_user") {
		t.Fatalf("the assistant's runtime got %v", got)
	}

	call := func(f *fakeRuntime, session string) map[string]any {
		var res proto.ToolResult
		if err := f.peer.Call(context.Background(), "mcp.call", map[string]any{"name": "ask_user", "args": map[string]any{}, "session_id": session, "assistant": true}, &res); err != nil {
			t.Fatal(err)
		}
		calls.mu.Lock()
		defer calls.mu.Unlock()
		var p map[string]any
		_ = json.Unmarshal(calls.calls[len(calls.calls)-1], &p)
		return p
	}
	if p := call(host, sid); p["assistant"] != true || p["session_id"] != sid {
		t.Fatalf("the assistant's call %v", p)
	}
	if p := call(host, "session-w"); p["assistant"] == true {
		t.Fatalf("a call from another conversation passed as the assistant's: %v", p)
	}
	if p := call(plain, sid); p["assistant"] == true {
		t.Fatalf("another runtime's call passed as the assistant's: %v", p)
	}
}

func TestGatedToolGoesThroughTheGate(t *testing.T) {
	n, hub := runtimeNode(t)
	assistantHub(hub)
	skills := filepath.Join(n.home, ".persona", "skills")
	t.Setenv("PERSONA_SKILLS_DIR", skills)
	n.dir = filepath.Join(n.home, ".persona")
	script := filepath.Join(skills, "example", "scripts", "ex.sh")
	_ = os.MkdirAll(filepath.Dir(script), 0o755)
	_ = os.WriteFile(filepath.Join(skills, "example", "SKILL.md"), []byte("---\nname: example\n---\n"), 0o644)
	ran := filepath.Join(n.home, "ran")
	_ = os.WriteFile(script, []byte("#!/bin/bash\necho \"$*\" >> "+shellQuote(ran)+"\necho done \"$*\"\n"), 0o755)
	g := exampleGate("")
	g.Tool = &proto.GateTool{Name: "example_api", Description: "Run ex."}
	n.mu.Lock()
	n.larkCfg, n.larkCfgAt = proto.AssistantLark{Gates: []proto.CommandGate{g}}, time.Now().Add(time.Hour)
	n.mu.Unlock()
	const gatedTool = "example_api"
	host := dialRuntime(t, n, assistantRuntimeHello())
	sid := startedAssistant(t, n, host)

	var tools struct {
		Tools []proto.Tool `json:"tools"`
	}
	if err := host.peer.Call(context.Background(), "mcp.tools", map[string]any{"assistant": true}, &tools); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toolList(tools.Tools), gatedTool) {
		t.Fatalf("tools %v", toolList(tools.Tools))
	}
	run := func(args ...string) proto.ToolResult {
		var res proto.ToolResult
		if err := host.peer.Call(context.Background(), "mcp.call", map[string]any{"name": gatedTool, "args": map[string]any{"args": args}, "session_id": sid, "assistant": true}, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run("GET", "/me"); res.IsError || !strings.Contains(res.Content[0].Text, "done GET /me") {
		t.Fatalf("a read: %+v", res)
	}
	hub.mu.Lock()
	hub.resolve = func(ix proto.Interaction) proto.Resolution {
		no := false
		return proto.Resolution{Allow: &no, Message: "not now"}
	}
	hub.mu.Unlock()
	if res := run("POST", "/conversations/oc_test/messages", `{"text":"hi"}`); !res.IsError || !strings.Contains(res.Content[0].Text, "not now") {
		t.Fatalf("a refused write: %+v", res)
	}
	hub.mu.Lock()
	asked := len(hub.asked)
	hub.mu.Unlock()
	if asked != 1 {
		t.Fatalf("%d cards", asked)
	}
	b, _ := os.ReadFile(ran)
	if got := strings.TrimSpace(string(b)); got != "GET /me" {
		t.Fatalf("ol ran %q", got)
	}
	// Not the assistant's conversation: the gated tool is not run here (it goes to the hub, which has no such tool).
	var res proto.ToolResult
	if err := host.peer.Call(context.Background(), "mcp.call", map[string]any{"name": gatedTool, "args": map[string]any{"args": []string{"GET", "/other"}}, "session_id": "session-other", "assistant": true}, &res); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(ran); strings.Contains(string(b), "/other") {
		t.Fatal("the gated tool ran for a conversation that is not the assistant's")
	}
}

func toolList(ts []proto.Tool) string {
	var out []string
	for _, x := range ts {
		out = append(out, x.Name)
	}
	return strings.Join(out, ",")
}
