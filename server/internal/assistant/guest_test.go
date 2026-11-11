package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// guestExt is the extensions' tools for the guest tests: a connector's two (lark_agent_reply, lark_chats) and Cua's.
// mail.attachment requests it is asked are kept in attachments.
type guestExt struct {
	called, sent []string
	attachments  []proto.MailAttachmentRequest
}

func (e *guestExt) AgentTools(context.Context, bool) (agents, assistantOnly []proto.Tool) {
	return nil, []proto.Tool{{Name: "computer_run"}, {Name: "lark_chats"}, {Name: "lark_agent_reply"}}
}

func (e *guestExt) CallTool(_ context.Context, _, _, name string, _ json.RawMessage, _ bool) (proto.ToolResult, bool) {
	if name == "lark_chats" || name == "lark_agent_reply" || name == "computer_run" {
		e.called = append(e.called, name)
		return proto.TextResult("ok "+name, false), true
	}
	return proto.ToolResult{}, false
}

func (e *guestExt) ConnectorTool(name string) bool {
	return name == "lark_chats" || name == "lark_agent_reply"
}

func (e *guestExt) AssistantSummary(context.Context, string, string) (string, bool) { return "", false }

func (e *guestExt) AssistantLark(context.Context) (proto.AssistantLark, error) {
	return proto.AssistantLark{}, nil
}

func (e *guestExt) AnswerComputerElicit(context.Context, string, json.RawMessage) (map[string]any, error) {
	return nil, nil
}

func (e *guestExt) RecordNodeSent(_ context.Context, machine string, params json.RawMessage) (any, error) {
	e.sent = append(e.sent, machine+" "+string(params))
	return struct{}{}, nil
}

func (e *guestExt) MailAttachment(_ context.Context, req proto.MailAttachmentRequest) (proto.MailAttachment, error) {
	e.attachments = append(e.attachments, req)
	return proto.MailAttachment{Filename: "statement.pdf", Mime: "application/pdf", Size: 4, Data: []byte("%PDF")}, nil
}

func guestService(t *testing.T) (*fakeHub, *Service, *guestExt) {
	t.Helper()
	f := newFakeHub().withMachine()
	f.settings = &Settings{MachineID: "server"}
	s := newTestService(t, f)
	ext := &guestExt{}
	s.SetExtensions(ext)
	return f, s, ext
}

func postConversation(t *testing.T, s *Service, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/conversations", strings.NewReader(body))
	r.Header.Set(RoleHeader, "extension")
	r.Header.Set(ExtensionHeader, "larkext")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

// A guest conversation (#5195) starts in its own directory, with only its instructions there (none of the assistant's
// connectors, CLIs or skills prepared), and the node is asked to start it as a guest with its tools; it hands out no
// tasks. The hub remembers it.
func TestGuestConversationStart(t *testing.T) {
	f, s, _ := guestService(t)
	w := postConversation(t, s, `{"prompt":"张三问：部署脚本在哪","purpose":"lark.agent","report_tasks":true,"guest":{"tools":["lark_agent_reply"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Session proto.Session `json:"session"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(f.prepared) != 0 || len(f.guestPrepared) != 1 {
		t.Fatalf("prepared %v, guest prepared %v", f.prepared, f.guestPrepared)
	}
	if p := f.guestPrepared[0]; p["dir"] != guestDir || !strings.Contains(p["claude_md"].(string), "访客对话") {
		t.Fatalf("guest prepare: %v", p)
	}
	send := f.sends[len(f.sends)-1]
	guest, _ := send["guest"].(map[string]any)
	if send["cwd"] != "/home/me/.persona/guest" || guest == nil || !slices.Equal(anyStrings(guest["tools"]), []string{"lark_agent_reply"}) {
		t.Fatalf("assistant.send: %v", send)
	}
	tools, ok, err := s.guestOf(context.Background(), "server", out.Session.SessionID)
	if err != nil || !ok || !slices.Equal(tools, []string{"lark_agent_reply"}) {
		t.Fatalf("guestOf = %v %v %v", tools, ok, err)
	}
	if _, reporter := s.reporterOf(context.Background(), out.Session.SessionID); reporter {
		t.Fatal("a guest conversation was made a task reporter")
	}
	// An ordinary one is as before.
	if w := postConversation(t, s, `{"prompt":"hi"}`); w.Code != http.StatusOK || len(f.prepared) != 1 {
		t.Fatalf("an ordinary conversation: %d, prepared %d", w.Code, len(f.prepared))
	}
	if _, guest := f.sends[len(f.sends)-1]["guest"]; guest {
		t.Fatal("an ordinary conversation was started as a guest")
	}
}

// A guest's first tool discovery happens before assistant.send answers on the Agent SDK. Its registered id and
// declared tools must already exist at that point, not after the node has started a model with an empty list.
func TestGuestConversationRegistersBeforeDiscovery(t *testing.T) {
	f, s, _ := guestService(t)
	devices := newFleetDevs()
	devices.add("server", "server")
	s.devs = devices
	setHub(s, &startingPrivacyHub{Hub: f, onStart: func(p DeviceCall) error {
		var send struct {
			NewSessionID string `json:"new_session_id"`
		}
		if err := json.Unmarshal(p.Params, &send); err != nil {
			t.Fatal(err)
		}
		if send.NewSessionID == "" {
			t.Fatal("guest start has no registered session id")
		}
		names := func() []string {
			raw, _ := json.Marshal(map[string]any{"assistant": true, "session_id": send.NewSessionID, "guest": true})
			out, handled, err := s.NodeRequest(context.Background(), "server", "mcp.tools", raw)
			if err != nil || !handled {
				t.Fatalf("mcp.tools during start: %v handled=%v", err, handled)
			}
			var names []string
			for _, tool := range out.(map[string]any)["tools"].([]proto.Tool) {
				names = append(names, tool.Name)
			}
			return names
		}()
		if !slices.Equal(names, []string{"lark_agent_reply"}) {
			t.Fatalf("tools during guest start: %v", names)
		}
		return nil
	}})
	w := postConversation(t, s, `{"prompt":"search","guest":{"tools":["lark_agent_reply"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	send := f.sends[len(f.sends)-1]
	if out.Session.SessionID == "" || send["new_session_id"] != out.Session.SessionID {
		t.Fatalf("registered and started ids differ: %q %q", send["new_session_id"], out.Session.SessionID)
	}
}

// A failed start must not leave a registered guest with no process behind it.
func TestGuestConversationFailedStartForgetsRegistration(t *testing.T) {
	f, s, _ := guestService(t)
	f.failAssistant = errors.New("cannot start")
	guestID := ""
	f.onAssistantSend = func(_ *fakeHub, send map[string]any) {
		guestID, _ = send["new_session_id"].(string)
	}
	w := postConversation(t, s, `{"prompt":"search","guest":{"tools":["lark_agent_reply"]}}`)
	if w.Code == http.StatusOK {
		t.Fatalf("start unexpectedly succeeded: %s", w.Body)
	}
	if guestID == "" {
		t.Fatal("the failed guest was not started under its registered id")
	}
	if _, guest, err := s.guestOf(context.Background(), "server", guestID); err != nil || guest {
		t.Fatalf("failed guest is still registered: guest=%v err=%v", guest, err)
	}
}

// A guest keeps the purpose's launcher, model and Agent SDK choice. Its
// no-built-in-tools restriction is the node's, on either runtime.
func TestGuestConversationKeepsTheSDK(t *testing.T) {
	f, s, _ := guestService(t)
	if err := s.saveModelRecord(context.Background(), "lark.agent", 0, ModelBinding{Provider: "launcher.claude", Runtime: proto.AssistantRuntimeAgentSDK}); err != nil {
		t.Fatal(err)
	}
	w := postConversation(t, s, `{"prompt":"search","purpose":"lark.agent","guest":{"tools":["lark_chats"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	send := f.sends[len(f.sends)-1]
	if send["runtime"] != proto.AssistantRuntimeAgentSDK {
		t.Fatalf("guest runtime: %v", send["runtime"])
	}
	if send["launcher"] == nil || send["model_fingerprint"] == nil {
		t.Fatalf("guest lost its model binding: %v", send)
	}
	// The purpose's selection reaches the node without changing the guest contract.
	binding, err := s.resolveModel(context.Background(), "lark.agent")
	if err != nil || binding.Runtime != proto.AssistantRuntimeAgentSDK {
		t.Fatalf("main runtime: %q (%v)", binding.Runtime, err)
	}
}

// guest.tools are a connector's tools only, one to eight of them; a guest is not the main conversation or in a space;
// a node without guest conversations starts none.
func TestGuestConversationRefusals(t *testing.T) {
	f, s, _ := guestService(t)
	for _, body := range []string{
		`{"prompt":"x","guest":{"tools":[]}}`,
		`{"prompt":"x","guest":{"tools":["agent_start"]}}`,  // the assistant's own
		`{"prompt":"x","guest":{"tools":["computer_run"]}}`, // Cua's
		`{"prompt":"x","guest":{"tools":["phone_contacts"]}}`,
		`{"prompt":"x","guest":{"tools":["lark_agent_reply","lark_agent_reply"]}}`,
		`{"prompt":"x","main":true,"guest":{"tools":["lark_agent_reply"]}}`,
		`{"prompt":"x","space":"health","guest":{"tools":["lark_agent_reply"]}}`,
	} {
		if w := postConversation(t, s, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	f.noGuest = true
	w := postConversation(t, s, `{"prompt":"x","guest":{"tools":["lark_agent_reply"]}}`)
	if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "personad") {
		t.Fatalf("an old node: %d %s", w.Code, w.Body)
	}
	if len(f.sends) != 0 || len(f.prepared) != 0 {
		t.Fatalf("something started: sends %v, prepared %v", f.sends, f.prepared)
	}
}

// A guest conversation is listed and may call only its own tools; an ordinary conversation keeps every tool, and a
// conversation the node says is a guest the hub does not know gets none.
func TestGuestConversationTools(t *testing.T) {
	ft := newFleetTest(t) // the nodes' requests are the fleet's
	ft.f.hello.Assistant.MachineID = "mac"
	s := ft.s
	ext := &guestExt{}
	s.SetExtensions(ext)
	ctx := context.Background()
	if err := s.guestSet(ctx, "mac", "g1", []string{"lark_agent_reply"}); err != nil {
		t.Fatal(err)
	}
	names := func(session string, guest bool) []string {
		raw, _ := json.Marshal(map[string]any{"assistant": true, "session_id": session, "guest": guest})
		out, handled, err := s.NodeRequest(ctx, "mac", "mcp.tools", raw)
		if err != nil || !handled {
			t.Fatal(err, handled)
		}
		var got []string
		for _, tool := range out.(map[string]any)["tools"].([]proto.Tool) {
			got = append(got, tool.Name)
		}
		return got
	}
	call := func(session, name string, guest bool) proto.ToolResult {
		raw, _ := json.Marshal(map[string]any{"assistant": true, "session_id": session, "name": name, "args": map[string]any{}, "guest": guest})
		out, _, err := s.NodeRequest(ctx, "mac", "mcp.call", raw)
		if err != nil {
			t.Fatal(err)
		}
		return out.(proto.ToolResult)
	}
	if got := names("g1", true); !slices.Equal(got, []string{"lark_agent_reply"}) {
		t.Fatalf("the guest's tools: %v", got)
	}
	if got := names("o1", false); !slices.Contains(got, "agent_start") || !slices.Contains(got, "lark_chats") || !slices.Contains(got, "computer_run") {
		t.Fatalf("an ordinary conversation's tools: %v", got)
	}
	if got := names("unknown", true); len(got) != 0 {
		t.Fatalf("an unknown guest's tools: %v", got)
	}
	// On another machine the same id is not that guest.
	raw, _ := json.Marshal(map[string]any{"assistant": true, "session_id": "g1"})
	if out, _, _ := s.NodeRequest(ctx, "laptop", "mcp.tools", raw); len(out.(map[string]any)["tools"].([]proto.Tool)) < 2 {
		t.Fatal("another machine's conversation was taken for the guest")
	}

	if res := call("g1", "lark_agent_reply", true); res.IsError {
		t.Fatalf("its own tool: %+v", res)
	}
	for _, name := range []string{"lark_chats", "computer_run", "agent_start", "kb_search", "phone_contacts"} {
		if res := call("g1", name, false); !res.IsError {
			t.Errorf("the guest called %s: %+v", name, res)
		}
	}
	if res := call("unknown", "lark_chats", true); !res.IsError {
		t.Error("an unknown guest called a tool")
	}
	if !slices.Equal(ext.called, []string{"lark_agent_reply"}) {
		t.Fatalf("the extensions ran %v", ext.called)
	}
	if res := call("o1", "lark_chats", false); res.IsError {
		t.Fatalf("an ordinary conversation's call: %+v", res)
	}
}

// A guest conversation gets no live snapshot, and a later message keeps what it started on.
func TestGuestConversationContextAndSends(t *testing.T) {
	_, s, _ := guestService(t)
	ctx := context.Background()
	if err := s.guestSet(ctx, "server", "g1", []string{"lark_agent_reply"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"session_id": "g1"})
	out, handled, err := s.NodeAnswer(ctx, "server", "assistant.context", raw)
	if err != nil || !handled {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(out); string(b) != "{}" {
		t.Fatalf("the guest's snapshot: %s", b)
	}
	params, err := s.SendParams(ctx, SendParams{MachineID: "server", SessionID: "g1", Params: map[string]any{
		"text": "more", "launcher": "codex", "model": "m", "runtime": "agent_sdk", "permission_mode": "bypassPermissions"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(params) != 2 || params["text"] != "more" || params["is_main"] != false {
		t.Fatalf("a guest's send params: %v", params)
	}
}

// An extension asks what the hub's assistant can do before relying on guest conversations.
func TestAssistantCapabilities(t *testing.T) {
	_, s, _ := guestService(t)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/assistant/capabilities", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"guest_conversations":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func anyStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	if out == nil {
		if ss, ok := v.([]string); ok {
			return ss
		}
	}
	return out
}

func TestGoldenGuestClaudeMD(t *testing.T) {
	f, s, _ := guestService(t)
	f.hello.Owner = "示例用户"
	if _, err := s.guestPrepare(context.Background(), "server"); err != nil {
		t.Fatal(err)
	}
	golden(t, "guest_claude_md", f.guestPrepared[0]["claude_md"].(string))
}

// While the record of guests cannot be read, a conversation's tools are refused (the guest's could be any), but the
// user's own conversations keep their snapshot and their picks: the node keeps those from a guest by itself.
func TestGuestRecordUnreadable(t *testing.T) {
	ft := newFleetTest(t)
	s := ft.s
	s.SetExtensions(&guestExt{})
	ft.st.failSetting = errors.New("database down")
	ctx := context.Background()
	raw, _ := json.Marshal(map[string]any{"assistant": true, "session_id": "o1", "name": "lark_chats", "args": map[string]any{}})
	out, _, err := s.NodeRequest(ctx, "mac", "mcp.call", raw)
	if err != nil || !out.(proto.ToolResult).IsError {
		t.Fatalf("a call while guests are unreadable: %+v %v", out, err)
	}
	if s.isGuest(ctx, "mac", "o1") {
		t.Fatal("an ordinary conversation counted as a guest")
	}
	params, err := s.SendParams(ctx, SendParams{MachineID: "mac", SessionID: "o1", Params: map[string]any{"text": "hi", "launcher": "claude"}})
	if err != nil || params["launcher"] != "claude" {
		t.Fatalf("an ordinary conversation's pick: %v %v", params, err)
	}
}
