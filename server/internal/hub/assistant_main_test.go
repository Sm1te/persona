package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

func mainServer(t *testing.T, h *Hub) func(path, body string) (int, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	h.assistantRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return func(path, body string) (int, map[string]any) {
		res, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		out := map[string]any{}
		_ = json.Unmarshal(b, &out)
		return res.StatusCode, out
	}
}

// A new main conversation: refused while the old one works or when the app showed another; then the old one is put
// away (archived, renamed, still listed by its machine) and the next message to the main conversation starts one that
// is told what the old one left, once.
func TestNewMainConversation(t *testing.T) {
	h, n := conversationsHub(t)
	post := mainServer(t, h)
	n.handoff = map[string]any{"summary": "示例摘要：在整理出游计划", "recent": []map[string]any{
		{"role": "user", "text": "示例问题"}, {"role": "assistant", "text": "示例回答"}}}

	if code, out := post("/api/v1/assistant/main/new", `{"expect":"another-0009"}`); code != http.StatusConflict || out["code"] != "main_changed" {
		t.Fatalf("another main conversation: %d %v", code, out)
	}
	editSessions(n.m, func(list []proto.Session) []proto.Session {
		list[0].Status = proto.StatusWorking
		return list
	})
	if code, out := post("/api/v1/assistant/main/new", `{"expect":"main-0001"}`); code != http.StatusConflict || out["code"] != "busy" {
		t.Fatalf("while it answers: %d %v", code, out)
	}
	editSessions(n.m, func(list []proto.Session) []proto.Session {
		list[0].Status = proto.StatusIdle
		return list
	})
	if code, out := post("/api/v1/assistant/main/new", `{"carry":"sometimes"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown carry: %d %v", code, out)
	}

	code, out := post("/api/v1/assistant/main/new", `{"expect":"main-0001"}`)
	if code != http.StatusOK || out["archived"] != "main-0001" || out["carried"] != true {
		t.Fatalf("new main: %d %v", code, out)
	}
	settleAssistant(h)
	var cfg assistant.Settings
	assistantSetting(t, h, "assistant", &cfg)
	if cfg.Main != "" || len(cfg.Former) != 1 || cfg.Former[0].SessionID != "main-0001" || cfg.Former[0].MachineID != "server" {
		t.Fatalf("config: %+v", cfg)
	}
	eventually(t, 5*time.Second, "the profile keeps listing the old one (the manager tells the node)", func() bool {
		return slices.ContainsFunc(h.assistantProfile().Keep, func(r assistantwire.SessionRef) bool { return r.SessionID == "main-0001" })
	})
	var old proto.Session
	for _, s := range assistantViewOf(t, h, "?archived=1").Conversations {
		if s.SessionID == "main-0001" {
			old = s
		}
	}
	if !old.Archived || !strings.HasPrefix(old.Title, "旧主对话 · 至 ") {
		t.Fatalf("the old one: archived %v, %q", old.Archived, old.Title)
	}
	// Again: nothing left to put away.
	if code, out := post("/api/v1/assistant/main/new", `{}`); code != http.StatusOK || out["archived"] != "" {
		t.Fatalf("without a main conversation: %d %v", code, out)
	}

	code, out = post("/api/v1/assistant/conversations", `{"prompt":"今天有什么安排","main":true}`)
	if code != http.StatusOK {
		t.Fatalf("first message: %d %v", code, out)
	}
	first, _ := n.lastSend(t)["text"].(string)
	if !strings.Contains(first, `title="接上旧主对话"`) || !strings.Contains(first, "示例摘要") || !strings.Contains(first, "用户：示例问题") ||
		!strings.HasSuffix(first, "今天有什么安排") {
		t.Fatalf("the first message:\n%s", first)
	}
	if got := h.assistantProfile().Main; got != "s1" {
		t.Fatalf("main: %q", got)
	}
	// Taken once: a later new main conversation starts with the message alone.
	if code, _ := post("/api/v1/assistant/main/new", `{"carry":"none"}`); code != http.StatusOK {
		t.Fatal(code)
	}
	post("/api/v1/assistant/conversations", `{"prompt":"再来","main":true}`)
	if got, _ := n.lastSend(t)["text"].(string); !strings.Contains(got, "上一段没有能带过来的内容") {
		t.Fatalf("carry none still says where it came from:\n%s", got)
	}
	post("/api/v1/assistant/conversations", `{"prompt":"第三条","main":true}`)
	if got, _ := n.lastSend(t)["text"].(string); got != "第三条" {
		t.Fatalf("the handoff was used again:\n%s", got)
	}
}

// Clearing the main conversation's context (/clear, "Clear context"): refused when the app showed another one; a turn
// it is on does not refuse it but its process is closed; it is put away like a new main conversation (archived,
// renamed, still listed), nothing of it is carried over, and the next message starts a new conversation with that
// message alone.
func TestClearMainConversation(t *testing.T) {
	h, n := conversationsHub(t)
	post := mainServer(t, h)
	n.handoff = map[string]any{"summary": "示例摘要：在整理出游计划", "recent": []map[string]any{}}

	if code, out := post("/api/v1/assistant/main/clear", `{"expect":"another-0009"}`); code != http.StatusConflict || out["code"] != "main_changed" {
		t.Fatalf("another main conversation: %d %v", code, out)
	}
	if code, out := post("/api/v1/assistant/main/clear", `{"expect":`); code != http.StatusBadRequest {
		t.Fatalf("a broken body: %d %v", code, out)
	}
	editSessions(n.m, func(list []proto.Session) []proto.Session {
		list[0].Status = proto.StatusWorking
		return list
	})
	before := n.sent()
	code, out := post("/api/v1/assistant/main/clear", `{"expect":"main-0001"}`)
	if code != http.StatusOK || out["archived"] != "main-0001" {
		t.Fatalf("clear while it answers: %d %v", code, out)
	}
	n.mu.Lock()
	calls := slices.Clone(n.sends[before:])
	n.mu.Unlock()
	if !slices.ContainsFunc(calls, func(c map[string]any) bool { return c["close"] == "main-0001" }) {
		t.Fatalf("the running conversation was not closed: %v", calls)
	}
	settleAssistant(h)
	var cfg assistant.Settings
	assistantSetting(t, h, "assistant", &cfg)
	if cfg.Main != "" || len(cfg.Former) != 1 || cfg.Former[0].SessionID != "main-0001" {
		t.Fatalf("config: %+v", cfg)
	}
	var old proto.Session
	for _, s := range assistantViewOf(t, h, "?archived=1").Conversations {
		if s.SessionID == "main-0001" {
			old = s
		}
	}
	if !old.Archived || !strings.HasPrefix(old.Title, "旧主对话 · 至 ") {
		t.Fatalf("the old one: archived %v, %q", old.Archived, old.Title)
	}
	// Again: nothing left to clear.
	if code, out := post("/api/v1/assistant/main/clear", `{}`); code != http.StatusOK || out["archived"] != "" {
		t.Fatalf("without a main conversation: %d %v", code, out)
	}

	code, out = post("/api/v1/assistant/conversations", `{"prompt":"今天有什么安排","main":true}`)
	if code != http.StatusOK {
		t.Fatalf("first message: %d %v", code, out)
	}
	first := n.lastSend(t)
	if text, _ := first["text"].(string); text != "今天有什么安排" {
		t.Fatalf("the first message after a clear carries something:\n%s", text)
	}
	if sid, _ := first["session_id"].(string); sid != "" {
		t.Fatalf("the first message after a clear resumes %q", sid)
	}
	if got := h.assistantProfile().Main; got != "s1" {
		t.Fatalf("main: %q", got)
	}
}

// A clear drops what an earlier "New main chat" left waiting for the next conversation: the next one starts clean.
func TestClearMainDropsAWaitingHandoff(t *testing.T) {
	h, n := conversationsHub(t)
	post := mainServer(t, h)
	n.handoff = map[string]any{"summary": "示例摘要", "recent": []map[string]any{}}
	if code, out := post("/api/v1/assistant/main/new", `{}`); code != http.StatusOK || out["carried"] != true {
		t.Fatalf("new main: %d %v", code, out)
	}
	if code, out := post("/api/v1/assistant/main/clear", `{}`); code != http.StatusOK || out["archived"] != "" {
		t.Fatalf("clear with none: %d %v", code, out)
	}
	post("/api/v1/assistant/conversations", `{"prompt":"你好","main":true}`)
	if got, _ := n.lastSend(t)["text"].(string); got != "你好" {
		t.Fatalf("the handoff survived the clear:\n%s", got)
	}
}

// A main conversation the user resumed in a terminal is theirs: the clear puts it away but never closes it (the node's
// session.close would close that terminal).
func TestClearMainLeavesATerminalOpen(t *testing.T) {
	h, n := conversationsHub(t)
	post := mainServer(t, h)
	editSessions(n.m, func(list []proto.Session) []proto.Session {
		list[0].PaneID = "w1:p3"
		return list
	})
	if code, out := post("/api/v1/assistant/main/clear", `{"expect":"main-0001"}`); code != http.StatusOK || out["archived"] != "main-0001" {
		t.Fatalf("clear: %d %v", code, out)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, c := range n.sends {
		if _, ok := c["close"]; ok {
			t.Fatalf("closed the user's terminal: %v", n.sends)
		}
	}
}

// Compacting goes to the conversation's node; its refusals come back as 409 with their code.
func TestCompactConversation(t *testing.T) {
	h, n := conversationsHub(t)
	post := mainServer(t, h)
	if code, out := post("/api/v1/assistant/conversations/main-0001/compact", ""); code != http.StatusOK || out["ok"] != true {
		t.Fatalf("compact: %d %v", code, out)
	}
	n.compactErr = &rpc.Error{Code: "busy", Message: "正在回答，等这一轮结束再压缩"}
	if code, out := post("/api/v1/assistant/conversations/main-0001/compact", `{}`); code != http.StatusConflict || out["code"] != "busy" {
		t.Fatalf("while it answers: %d %v", code, out)
	}
	if code, _ := post("/api/v1/assistant/conversations/main-0001/compact", `{"machine_id":"nowhere"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown machine: %d", code)
	}
	if !slices.Equal(n.compacts, []string{"main-0001", "main-0001"}) {
		t.Fatalf("compacted: %v", n.compacts)
	}
}

// Android starts the new main conversation empty and then sends the message: what the old one left waits for that
// message instead of being a turn of its own.
func TestNewMainStartedEmptyCarriesOverInTheFirstMessage(t *testing.T) {
	h, n := conversationsHub(t)
	post := mainServer(t, h)
	n.handoff = map[string]any{"summary": "示例摘要", "recent": []map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /s/{sid}", func(w http.ResponseWriter, r *http.Request) { h.sendMessage(w, r, n.m) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	send := func(text string) string {
		t.Helper()
		res, err := http.Post(srv.URL+"/s/s1", "application/json", strings.NewReader(`{"text":"`+text+`"}`))
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("send: %v %v", res, err)
		}
		_ = res.Body.Close()
		got, _ := n.lastSend(t)["text"].(string)
		return got
	}

	if code, _ := post("/api/v1/assistant/main/new", `{}`); code != http.StatusOK {
		t.Fatal(code)
	}
	if code, out := post("/api/v1/assistant/conversations", `{"prompt":"","main":true}`); code != http.StatusOK {
		t.Fatalf("start empty: %d %v", code, out)
	}
	if got, _ := n.lastSend(t)["text"].(string); got != "" {
		t.Fatalf("started empty with:\n%s", got)
	}
	editSessions(n.m, func(list []proto.Session) []proto.Session {
		return append(list, proto.Session{SessionID: "s1", Cwd: convDir})
	})
	if got := send("你好"); !strings.Contains(got, "示例摘要") || !strings.HasSuffix(got, "你好") {
		t.Fatalf("the first message:\n%s", got)
	}
	if got := send("第二条"); got != "第二条" {
		t.Fatalf("the second message:\n%s", got)
	}
}
