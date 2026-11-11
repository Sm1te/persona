package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// interjectClaude is a claude that names version in its init, starts answering the first message and never finishes
// it (the conversation stays busy), acknowledges every control request and logs what it reads to ~/stdin.jsonl.
func interjectClaude(t *testing.T, version string) {
	t.Helper()
	fakeClaudeHome(t, `log="$HOME/stdin.jsonl"
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$log"
  case "$line" in
  *'"type":"control_request"'*)
    id=$(printf '%s' "$line" | sed -n 's/.*"request_id":"\([^"]*\)".*/\1/p')
    printf '{"type":"control_response","response":{"subtype":"success","request_id":"%s"}}\n' "$id" ;;
  *'"type":"user"'*)
    if [ ! -f "$HOME/answering" ]; then
      : > "$HOME/answering"
      echo '{"type":"system","subtype":"init","claude_code_version":"`+version+`"}'
      echo '{"type":"stream_event","event":{"type":"message_start","message":{}}}'
    fi ;;
  esac
done
`)
}

func interjectLog(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(os.Getenv("HOME"), "stdin.jsonl"))
	return string(b)
}

// interjectSecond starts a conversation that stays busy answering "first", waits for its init, then sends "second".
// It returns what the fake claude read after "first".
func interjectSecond(t *testing.T, version string, on bool) string {
	t.Helper()
	interjectClaude(t, version)
	n := codexTestNode(t)
	withFakeHub(t, n)
	id, err := n.assistantSend(context.Background(), "", t.TempDir(), "claude", "first", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	n.mu.Lock()
	p := n.procs[id]
	n.mu.Unlock()
	sdkWait(t, "its init", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.claudeVersion == version
	})
	if _, err := n.assistantSend(context.Background(), id, "", "claude", "second", nil, "", "", modelExecution{Interject: on}); err != nil {
		t.Fatal(err)
	}
	sdkWait(t, "the second message", func() bool { return strings.Contains(interjectLog(t), "second") })
	if on && versionAtLeast(version, backgroundTasksMinVersion) {
		sdkWait(t, "background_tasks", func() bool { return strings.Contains(interjectLog(t), "background_tasks") })
	}
	log := interjectLog(t)
	return log[strings.Index(log, `"first"`):]
}

// A message to a conversation that is answering moves its foreground work to the background, after the message is
// in, and tells the model it came mid-turn (#3278).
func TestAssistantInterjectBackgroundsForegroundWork(t *testing.T) {
	after := interjectSecond(t, "2.1.322", true)
	msg, bg := strings.Index(after, `"second"`), strings.Index(after, `"subtype":"background_tasks"`)
	if msg < 0 || bg < 0 || bg < msg {
		t.Fatalf("want the message, then background_tasks:\n%s", after)
	}
	if !strings.Contains(after, `title=\"插话\"`) {
		t.Fatalf("the message does not say it came mid-turn:\n%s", after)
	}
}

// With the setting off, or on a Claude Code without background_tasks, the work stays in the foreground, but the message
// still says it came mid-turn and asks for one sentence on what is going on first (#4883: with the setting off, the
// owner's "what happened?" got six minutes of tool calls and no word).
func TestAssistantInterjectOffOrOldClaude(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		on            bool
	}{{"off", "2.1.322", false}, {"old claude", "2.1.138", true}} {
		t.Run(tc.name, func(t *testing.T) {
			after := interjectSecond(t, tc.version, tc.on)
			if strings.Contains(after, "background_tasks") || strings.Contains(after, "已经转到后台") {
				t.Fatalf("moved work to the background:\n%s", after)
			}
			if !strings.Contains(after, `title=\"插话\"`) || !strings.Contains(after, "在调用下一个工具之前，先用一句话回应用户") {
				t.Fatalf("the mid-turn message carries no note:\n%s", after)
			}
		})
	}
}

// The note asks for one sentence in the user's language before the next tool call: what it is doing, how long or what
// it waits for; it says the work went to the background only when it did.
func TestInterjectNote(t *testing.T) {
	for _, background := range []bool{false, true} {
		text := interjectNote(background)["text"].(string)
		for _, want := range []string{`title="插话"`, "在调用下一个工具之前", "用用户的语言", "你正在做什么，还要多久或在等什么"} {
			if !strings.Contains(text, want) {
				t.Errorf("background %v: no %q in\n%s", background, want, text)
			}
		}
		if got := strings.Contains(text, "已经转到后台"); got != background {
			t.Errorf("background %v: says it went to the background: %v\n%s", background, got, text)
		}
	}
}

// The first message of an idle conversation is no interjection: no note.
func TestAssistantFirstMessageHasNoInterjectNote(t *testing.T) {
	interjectClaude(t, "2.1.322")
	n := codexTestNode(t)
	withFakeHub(t, n)
	id, err := n.assistantSend(context.Background(), "", t.TempDir(), "claude", "first", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	sdkWait(t, "the first message", func() bool { return strings.Contains(interjectLog(t), "first") })
	if strings.Contains(interjectLog(t), "插话") {
		t.Fatalf("a message to an idle conversation carries the note:\n%s", interjectLog(t))
	}
}

// Only a busy Claude on the CLI whose init named a Claude Code with background_tasks interjects; any busy Claude on the
// CLI takes a message mid-turn.
func TestAssistantInterjects(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		agent, rt, v           string
		busy, expects, midTurn bool
	}{
		{"busy", "claude", "", "2.1.139", true, true, true},
		{"idle", "claude", "", "2.1.322", false, false, false},
		{"before init", "claude", "", "", true, false, true},
		{"old", "claude", "", "2.1.138", true, false, true},
		{"codex", "codex", "", "2.1.322", true, false, false},
		{"agent sdk", "claude", "agentsdk", "2.1.322", true, false, false},
	} {
		p := &assistantProc{agent: tc.agent, runtime: tc.rt, claudeVersion: tc.v, busy: tc.busy}
		if got := p.interjects(); got != tc.expects {
			t.Errorf("%s: interjects %v, want %v", tc.name, got, tc.expects)
		}
		if got := p.midTurn(); got != tc.midTurn {
			t.Errorf("%s: midTurn %v, want %v", tc.name, got, tc.midTurn)
		}
	}
}

// A headless conversation lists the background work its transcript names, a command backgrounded by hand included:
// a Lark turn that ends meanwhile is held for the turn that work starts, instead of closing on the first answer.
func TestAssistantSessionsListBackgroundWork(t *testing.T) {
	n := codexTestNode(t)
	n.paths = map[string]string{}
	id := "0e000000-0000-4000-8000-0000000000c1"
	fakeAssistant(n, id, "")
	path := filepath.Join(t.TempDir(), id+".jsonl")
	lines := []string{
		`{"type":"user","uuid":"u0","timestamp":"2099-01-01T00:00:00Z","message":{"role":"user","content":"run it"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2099-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"python3 slow.py","description":"Run a slow test"}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2099-01-01T00:00:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command was manually backgrounded by user with ID: beg3p1s7d. Output is being written to: /tmp/tasks/beg3p1s7d.output."}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.paths[id] = path
	n.mu.Unlock()
	for _, s := range n.assistantSessions() {
		if s.SessionID == id {
			if len(s.Background) != 1 || s.Background[0].ID != "beg3p1s7d" {
				t.Fatalf("background: %+v", s.Background)
			}
			return
		}
	}
	t.Fatal("the conversation is not listed")
}
