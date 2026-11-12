package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/screen"
)

func desktopQuestionNode(t *testing.T, lines ...string) (*Node, *fakeCodexBus, string, string) {
	t.Helper()
	home, bus := startFakeCodexBus(t, cxID)
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte(`{"projectless-thread-ids":["`+cxID+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "sessions", time.Now().Format("2006/01/02"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-11-01T21-40-06-"+cxID+".jsonl")
	meta := `{"type":"session_meta","payload":{"id":"` + cxID + `","cwd":"/work/project","originator":"Codex Desktop","source":"vscode"}}`
	data := meta + "\n"
	for _, line := range lines {
		data += cxToday(line) + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	n := remoteTestNode(t)
	n.configureCodex(proto.CodexConfig{Homes: []string{home}})
	id := n.registerCodex(home, cxID, path)
	return n, bus, id, path
}

func TestCodexDesktopAsyncQuestionHasCardWithoutTerminal(t *testing.T) {
	n, _, id, _ := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	cards := n.codexCardsNow(context.Background(), id)
	if len(cards) != 1 || cards[0].ix.ToolName != "request_user_input_async" || cards[0].ix.SessionID != id || len(cards[0].ix.Questions[0].Options) != 3 {
		t.Fatalf("desktop question cards = %+v", cards)
	}
}

func TestCodexDesktopQuestionCapabilityDoesNotGrantApprovals(t *testing.T) {
	n, _, id, _ := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	for _, s := range n.codexSessions(map[string]bool{}) {
		if s.SessionID == id {
			if s.Capabilities == nil || !s.Capabilities.AnswerQuestions || !s.Capabilities.Send || s.Capabilities.Approve || s.Capabilities.Attach {
				t.Fatalf("desktop capabilities = %+v", s.Capabilities)
			}
			return
		}
	}
	t.Fatal("desktop session not listed")
}

func TestCodexDesktopAsyncQuestionReplyUsesOwningApp(t *testing.T) {
	n, bus, id, path := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	card := onlyCard(t, n)
	oldPoll, oldConfirm := codexPoll, codexConfirm
	codexPoll, codexConfirm = time.Millisecond, time.Second
	t.Cleanup(func() { codexPoll, codexConfirm = oldPoll, oldConfirm })
	got := make(chan string, 1)
	go func() {
		p := <-bus.turns
		req := p["turnStart"].(map[string]any)["request"].(map[string]any)
		text := req["input"].([]any)[0].(map[string]any)["text"].(string)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			b, _ := json.Marshal(map[string]any{"type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "content": []any{map[string]any{"type": "text", "text": text}}}}})
			_, err = f.Write(append(b, '\n'))
			f.Close()
		}
		if err != nil {
			got <- "write failed"
			return
		}
		got <- text
	}()
	if err := n.codexAnswer(context.Background(), id, card.ix.ID, proto.Resolution{Answers: map[string]string{"Which color should the banner be?": "Green"}}); err != nil {
		t.Fatal(err)
	}
	text := <-got
	want := `<send_user_message_question_reply>[{"questionItemId":"[\"request_user_input_async\",\"call_a1\",0]","question":"Which color should the banner be?","answer":"Green"}]</send_user_message_question_reply>`
	if text != want {
		t.Fatalf("desktop reply = %q, want %q", text, want)
	}
	if cards := n.codexCardsNow(context.Background(), id); len(cards) != 0 {
		t.Fatalf("answered card still shown: %+v", cards)
	}
	if err := n.codexAnswer(context.Background(), id, card.ix.ID, proto.Resolution{Answers: map[string]string{"Which color should the banner be?": "Blue"}}); err == nil {
		t.Fatal("the same question was answered twice")
	}
	select {
	case p := <-bus.turns:
		t.Fatalf("duplicate answer started another turn: %v", p)
	default:
	}
}

func TestCodexDesktopQuestionsIgnoreSyncAndClosedAsync(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{"synchronous question", []string{cxSyncCall}},
		{"answered in App", []string{cxAsyncCall, cxAsyncOut, cxUserLine("a new message")}},
		{"interrupted", []string{cxAsyncCall, cxAsyncOut, `{"type":"event_msg","payload":{"type":"turn_aborted"}}`}},
		{"async blocked by sync", []string{cxAsyncCall, cxAsyncOut, cxSyncCall}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, id, _ := desktopQuestionNode(t, tc.lines...)
			if cards := n.codexCardsNow(context.Background(), id); len(cards) != 0 {
				t.Fatalf("unsupported/stale question cards = %+v", cards)
			}
		})
	}
}

func TestCodexDesktopQuestionRefusesPermissionResolution(t *testing.T) {
	n, bus, id, _ := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	card := onlyCard(t, n)
	allow := true
	if err := n.codexAnswer(context.Background(), id, card.ix.ID, proto.Resolution{Allow: &allow}); err == nil {
		t.Fatal("permission resolution answered a question")
	}
	select {
	case p := <-bus.turns:
		t.Fatalf("permission resolution reached App IPC: %v", p)
	default:
	}
}

func TestCodexDesktopQuestionDoesNotRestoreExpiredQuestion(t *testing.T) {
	n, _, id, path := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-codexAskMaxAge - time.Hour).UTC().Format(time.RFC3339Nano)
	if err := os.WriteFile(path, cxStamp.ReplaceAll(data, []byte(`"timestamp":"`+old+`"`)), 0600); err != nil {
		t.Fatal(err)
	}
	if cards := n.codexCardsNow(context.Background(), id); len(cards) != 0 {
		t.Fatalf("expired question restored: %+v", cards)
	}
}

func TestCodexDesktopQuestionUnknownDeliveryCannotBeRetried(t *testing.T) {
	for _, disconnect := range []bool{true, false} {
		t.Run(fmt.Sprint("disconnect=", disconnect), func(t *testing.T) {
			n, bus, id, _ := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
			bus.dropTurnResponse.Store(disconnect)
			card := onlyCard(t, n)
			oldPoll, oldConfirm := codexPoll, codexConfirm
			codexPoll, codexConfirm = time.Millisecond, 15*time.Millisecond
			t.Cleanup(func() { codexPoll, codexConfirm = oldPoll, oldConfirm })
			answer := proto.Resolution{Answers: map[string]string{"Which color should the banner be?": "Green"}}
			if err := n.codexAnswer(context.Background(), id, card.ix.ID, answer); !maybeDelivered(err) {
				t.Fatalf("unconfirmed IPC answer must remain uncertain: %v", err)
			}
			select {
			case <-bus.turns:
			default:
				t.Fatal("the App did not take the first turn")
			}
			<-bus.asked
			if err := n.codexAnswer(context.Background(), id, card.ix.ID, answer); refusalCode(err) != screen.PromptChanged {
				t.Fatalf("retry should refuse without delivery, got %v", err)
			}
			// A watcher pause must not make an unconfirmed answer retryable after the normal card hold expires.
			n.cx.mu.Lock()
			d := n.cx.dismissed[card.ix.ID]
			d.seen = time.Now().Add(-2 * codexHold)
			n.cx.dismissed[card.ix.ID] = d
			n.cx.mu.Unlock()
			n.syncCodexCards(context.Background())
			err := n.codexAnswer(context.Background(), id, card.ix.ID, answer)
			if code := refusalCode(err); code != screen.PromptChanged {
				t.Fatalf("retry should refuse without delivery, got %v", err)
			}
			select {
			case p := <-bus.turns:
				t.Fatalf("unknown answer sent a second turn: %v", p)
			default:
			}
		})
	}
}

func TestCodexDesktopQuestionRejectsChangedQuestion(t *testing.T) {
	n, bus, id, path := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	card := onlyCard(t, n)
	appendLine(t, path, cxUserLine("a newer request"))
	appendLine(t, path, cxToday(strings.ReplaceAll(cxAsyncItemLine, "call_a1", "call_a2")))
	err := n.codexAnswer(context.Background(), id, card.ix.ID, proto.Resolution{Answers: map[string]string{"Which color should the banner be?": "Green"}})
	if refusalCode(err) != screen.PromptChanged {
		t.Fatalf("stale question accepted: %v", err)
	}
	select {
	case p := <-bus.turns:
		t.Fatalf("stale answer reached App IPC: %v", p)
	default:
	}
	if now := onlyCard(t, n); now.ix.ID == card.ix.ID || now.ask.CallID != "call_a2" {
		t.Fatalf("replacement question = %+v", now)
	}
}

func TestCodexDesktopQuestionFiltersArchivedAndSubagent(t *testing.T) {
	for _, kind := range []string{"archived", "subagent"} {
		t.Run(kind, func(t *testing.T) {
			n, _, id, path := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
			if kind == "archived" {
				src, _ := n.codexSource(id)
				dir := filepath.Join(src.home, "archived_sessions")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, filepath.Join(dir, filepath.Base(path))); err != nil {
					t.Fatal(err)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = bytes.Replace(data, []byte(`"source":"vscode"`), []byte(`"source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent","depth":1}}}`), 1)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if cards := n.codexCardsNow(context.Background(), id); len(cards) != 0 {
				t.Fatalf("%s question exposed: %+v", kind, cards)
			}
		})
	}
}

func TestCodexDesktopQuestionPartialReplyKeepsRemainingQuestionAnswerable(t *testing.T) {
	var line map[string]any
	if err := json.Unmarshal([]byte(cxAsyncItemLine), &line); err != nil {
		t.Fatal(err)
	}
	item := line["payload"].(map[string]any)["item"].(map[string]any)
	item["questions"] = append(item["questions"].([]any), map[string]any{"title": "Which size?", "options": []string{"Small", "Large"}})
	data, _ := json.Marshal(line)
	n, bus, id, path := desktopQuestionNode(t, string(data))
	oldPoll, oldConfirm := codexPoll, codexConfirm
	codexPoll, codexConfirm = time.Millisecond, time.Second
	t.Cleanup(func() { codexPoll, codexConfirm = oldPoll, oldConfirm })
	for _, answer := range []map[string]string{{"Which color should the banner be?": "Green"}, {"Which size?": "Large"}} {
		card := onlyCard(t, n)
		recorded := make(chan error, 1)
		go func() {
			p := <-bus.turns
			text := p["turnStart"].(map[string]any)["request"].(map[string]any)["input"].([]any)[0].(map[string]any)["text"].(string)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err == nil {
				_, err = f.WriteString(cxUserLine(text) + "\n")
				f.Close()
			}
			recorded <- err
		}()
		if err := n.codexAnswer(context.Background(), id, card.ix.ID, proto.Resolution{Answers: answer}); err != nil {
			t.Fatal(err)
		}
		if err := <-recorded; err != nil {
			t.Fatal(err)
		}
		<-bus.asked
	}
	if cards := n.codexCardsNow(context.Background(), id); len(cards) != 0 {
		t.Fatalf("fully answered question remains: %+v", cards)
	}
}

// Drives the built node's discovery notification and answer RPC with an isolated App IPC bus and rollout.
func TestCodexDesktopQuestionNodeProcess(t *testing.T) {
	bin := os.Getenv("PERSONA_CODEX_NODE_BINARY")
	if bin == "" {
		t.Skip("set PERSONA_CODEX_NODE_BINARY to the built personad")
	}
	n, bus, id, path := desktopQuestionNode(t, cxAsyncCall, cxAsyncOut)
	src, _ := n.codexSource(id)
	// The profile is the directory containing sessions, not the real user's Codex home.
	profile := src.home
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "node", "--stdio")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "ORCA_USER_DATA_PATH=" + filepath.Join(home, ".orca")}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); stdin.Close(); _ = cmd.Wait() }()
	peer := rpc.New("test", stdin)
	peer.OnRequest = func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]any{"sessions": map[string]any{}}, nil
	}
	cards := make(chan []proto.Interaction, 8)
	peer.OnEvent = func(event string, raw json.RawMessage) {
		if event != "codex.interactions" {
			return
		}
		var out struct {
			Interactions []proto.Interaction `json:"interactions"`
		}
		if json.Unmarshal(raw, &out) == nil {
			select {
			case cards <- out.Interactions:
			default:
			}
		}
	}
	go peer.Serve(ctx, stdout)
	no := false
	if err := peer.Call(ctx, "node.configure", map[string]any{"machine_id": "test", "codex": proto.CodexConfig{Discovery: &no, Homes: []string{profile}}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := peer.Call(ctx, "engine.use", map[string]any{"engine": "herdr-core"}, nil); err != nil {
		t.Fatal(err)
	}
	var card proto.Interaction
	for card.ID == "" {
		select {
		case got := <-cards:
			if len(got) == 1 && got[0].ToolName == "request_user_input_async" {
				card = got[0]
			}
		case <-ctx.Done():
			t.Fatal("real node did not publish desktop question")
		}
	}
	gotReply := make(chan string, 1)
	go func() {
		p := <-bus.turns
		text := p["turnStart"].(map[string]any)["request"].(map[string]any)["input"].([]any)[0].(map[string]any)["text"].(string)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err == nil {
			_, err = f.WriteString(cxUserLine(text) + "\n")
			f.Close()
		}
		if err != nil {
			gotReply <- "write failed"
			return
		}
		gotReply <- text
	}()
	if err := peer.Call(ctx, "codex.answer", map[string]any{"session_id": card.SessionID, "id": card.ID, "resolution": proto.Resolution{Answers: map[string]string{"Which color should the banner be?": "Blue"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := <-gotReply; !strings.Contains(got, `"answer":"Blue"`) || !strings.Contains(got, "send_user_message_question_reply") {
		t.Fatalf("real node sent %q", got)
	}
	if err := peer.Call(ctx, "codex.answer", map[string]any{"session_id": card.SessionID, "id": card.ID, "resolution": proto.Resolution{Answers: map[string]string{"Which color should the banner be?": "Green"}}}, nil); err == nil {
		t.Fatal("real node accepted an already-answered question")
	}
}
