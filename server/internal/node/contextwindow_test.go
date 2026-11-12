package node

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestContextWindowOf(t *testing.T) {
	for _, c := range []struct {
		name, model          string
		recorded, used, want int
	}{
		{"claude default", "claude-opus-5-5", 0, 61_000, 200_000},
		{"claude 1m suffix", "claude-opus-5-5[1m]", 0, 10_000, 1_000_000},
		{"claude over 200k is the 1m variant", "claude-sonnet-5-5", 0, 340_000, 1_000_000},
		{"codex records its window", "gpt-5", 475_000, 26_279, 475_000},
		{"recorded beats the id", "claude-opus-5-5", 123_000, 1_000, 123_000},
		{"gateway model is unknown", "some-gateway-model", 0, 5_000, 0},
		{"synthetic is unknown", "<synthetic>", 0, 5_000, 0},
		{"empty is unknown", "", 0, 5_000, 0},
	} {
		if got := contextWindowOf(c.model, c.recorded, c.used); got != c.want {
			t.Errorf("%s: contextWindowOf(%q, %d, %d) = %d, want %d", c.name, c.model, c.recorded, c.used, got, c.want)
		}
	}
}

func TestSetContextOmitsUnknown(t *testing.T) {
	var s proto.Session
	setContext(&s, quickMeta{model: "claude-opus-5-5"})
	if s.ContextTokens != 0 || s.ContextWindow != 0 {
		t.Fatalf("no reply yet must leave the fields empty: %+v", s)
	}
	setContext(&s, quickMeta{model: "claude-opus-5-5", contextTokens: 61_000})
	if s.ContextTokens != 61_000 || s.ContextWindow != 200_000 {
		t.Fatalf("got %d / %d", s.ContextTokens, s.ContextWindow)
	}
}

// A runtime's session runs as the model the runtime names: its "[1m]" is the 1M window, whatever id the transcript
// records for the replies (the id without the suffix; behind a gateway, the gateway's own).
func TestSetRuntimeContext(t *testing.T) {
	for _, c := range []struct {
		name, runAs string
		m           quickMeta
		want        int
	}{
		{"the 1M variant of the model that answered", "claude-fable-5-2[1m]", quickMeta{model: "claude-fable-5-2", contextTokens: 80_000}, 1_000_000},
		{"a gateway answering under its own id", "gw-model-high[1m]", quickMeta{model: "gw-model-trial", contextTokens: 80_000}, 1_000_000},
		{"no variant named", "claude-fable-5-2", quickMeta{model: "claude-fable-5-2", contextTokens: 80_000}, 200_000},
		{"a gateway model with no variant named", "gw-model-high", quickMeta{model: "gw-model-trial", contextTokens: 80_000}, 0},
		{"a window the transcript records", "gw-model[1m]", quickMeta{model: "gw-model", contextTokens: 80_000, contextWindow: 400_000}, 400_000},
		{"answering as a fallback model", "claude-fable-5-2[1m]",
			quickMeta{model: "claude-opus-5-5", contextTokens: 80_000, modelFallback: &proto.ModelRefusal{}}, 200_000},
		{"answered by another Claude model (an overload fallback)", "claude-fable-5-2[1m]", quickMeta{model: "claude-sonnet-5-5", contextTokens: 80_000}, 200_000},
		{"the same model under its dated id", "claude-opus-5-5[1m]", quickMeta{model: "claude-opus-5-5-20261101", contextTokens: 80_000}, 1_000_000},
		{"a gateway's name answered under a Claude id", "gw-model-high[1m]", quickMeta{model: "claude-fable-5-2", contextTokens: 80_000}, 1_000_000},
		{"a transcript that names no model", "claude-fable-5-2[1m]", quickMeta{contextTokens: 80_000}, 1_000_000},
		{"no reply yet", "claude-fable-5-2[1m]", quickMeta{model: "claude-fable-5-2"}, 0},
	} {
		s := proto.Session{Model: c.runAs}
		setRuntimeContext(&s, c.m)
		if int(s.ContextWindow) != c.want || int(s.ContextTokens) != c.m.contextTokens {
			t.Errorf("%s: %d / %d, want %d / %d", c.name, s.ContextTokens, s.ContextWindow, c.m.contextTokens, c.want)
		}
	}
}

func ctxReply(id string, input, cacheRead, cacheCreate int) string {
	return `{"type":"assistant","timestamp":"2026-01-01T00:00:01.000Z","message":{"model":"claude-opus-5-5","id":"` + id +
		`","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":` + strconv.Itoa(input) +
		`,"cache_read_input_tokens":` + strconv.Itoa(cacheRead) + `,"cache_creation_input_tokens":` + strconv.Itoa(cacheCreate) + `,"output_tokens":5}}}`
}

func TestQuickMetaContextClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	user := `{"type":"user","cwd":"/tmp/x","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hi"}}`
	body := user + "\n" + ctxReply("msg_1", 3, 1000, 200) + "\n" + ctxReply("msg_2", 2, 40000, 21000) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	m := readQuickMeta(path, st)
	if m.contextTokens != 2+40000+21000 {
		t.Fatalf("context tokens = %d, want the latest reply's input + cache read + cache creation", m.contextTokens)
	}
	if m.contextWindow != 0 {
		t.Fatalf("Claude transcripts record no window, got %d", m.contextWindow)
	}
	var s proto.Session
	setContext(&s, m)
	if s.ContextWindow != 200_000 {
		t.Fatalf("window = %d", s.ContextWindow)
	}
}

// A tail that holds no reply (one huge line fills it) keeps what the last read knew.
func TestQuickMetaContextCarriedOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	user := `{"type":"user","cwd":"/tmp/x","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hi"}}`
	if err := os.WriteFile(path, []byte(user+"\n"+ctxReply("msg_1", 2, 5000, 100)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	prev := readQuickMeta(path, st)
	big := `{"type":"system","subtype":"x","content":"` + strings.Repeat("a", quickTail+10) + `"}` + "\n"
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(big)
	f.Close()
	st, _ = os.Stat(path)
	m := readQuickMetaSince(path, st, prev)
	if m.contextTokens != prev.contextTokens || m.contextTokens != 5102 {
		t.Fatalf("carried context = %d, prev %d", m.contextTokens, prev.contextTokens)
	}
}

func TestQuickMetaContextCodex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	meta := `{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"t1","cwd":"/tmp/x"}}`
	count := func(in int) string {
		return `{"timestamp":"2026-01-01T00:00:09.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":99999,"output_tokens":10},"last_token_usage":{"input_tokens":` +
			strconv.Itoa(in) + `,"output_tokens":4},"model_context_window":475000}}}`
	}
	// rate-limit-only token_count events carry no info and must not reset the numbers
	idle := `{"timestamp":"2026-01-01T00:00:10.000Z","type":"event_msg","payload":{"type":"token_count","info":null}}`
	body := meta + "\n" + count(7) + "\n" + count(6) + "\n" + idle + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	m := readQuickMeta(path, st)
	if m.contextTokens != 6 || m.contextWindow != 475_000 {
		t.Fatalf("codex context = %d / %d, want 6 / 475000", m.contextTokens, m.contextWindow)
	}
}
