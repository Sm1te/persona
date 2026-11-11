package transcript

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// Every item of a Claude Code reply carries the model its line names (#5034): its thinking, words and tool calls alike;
// Claude Code's own "<synthetic>" lines (an API error) and the user's side carry none, and do not change the session's.
func TestClaudeReplyModel(t *testing.T) {
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-12T08:00:00Z","message":{"role":"user","content":"look at it"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-12T08:00:01Z","message":{"role":"assistant","model":"claude-opus-5-5","id":"msg_1","content":[{"type":"thinking","thinking":"Reading first."},{"type":"text","text":"Reading the file."},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/work/a.go"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-11-12T08:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"package a"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-12T08:00:03Z","isApiErrorMessage":true,"error":"rate_limit","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"API Error: 429 slow down"}]}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-11-12T08:00:04Z","message":{"role":"user","content":"go on"}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-11-12T08:00:05Z","message":{"role":"assistant","model":"claude-sonnet-4-5-20250929","id":"msg_2","content":[{"type":"text","text":"Done."}]}}`,
	}
	var m Meta
	got := map[string]string{}
	for i, line := range lines {
		for _, it := range ParseLine(i+1, []byte(line), &m) {
			got[it.ID] = it.Model
		}
		if i == 3 && m.Model != "claude-opus-5-5" {
			t.Fatalf("a <synthetic> line changed the session's model to %q", m.Model)
		}
	}
	want := map[string]string{
		"u1#0": "", "a1#0": "claude-opus-5-5", "a1#1": "claude-opus-5-5", "a1#2": "claude-opus-5-5", "u2#0": "",
		"a2#0": "", "u3#0": "", "a3#0": "claude-sonnet-4-5-20250929",
	}
	for id, model := range want {
		if have, ok := got[id]; !ok || have != model {
			t.Errorf("%s: model %q (present %v), want %q", id, have, ok, model)
		}
	}
	if m.Model != "claude-sonnet-4-5-20250929" {
		t.Fatalf("session model %q", m.Model)
	}
}

// A Codex turn's agent items carry its turn_context model; a turn that records none carries none (not the turn
// before's: a window starting at that turn could not know it), and the user's words and tool results never do.
func TestCodexReplyModel(t *testing.T) {
	const ts = `"timestamp":"2026-11-12T08:00:00.000Z"`
	ev := func(payload string) string { return `{` + ts + `,"type":"event_msg","payload":` + payload + `}` }
	item := func(it string) string { return ev(`{"type":"item_completed","item":` + it + `}`) }
	lines := []string{
		`{` + ts + `,"type":"session_meta","payload":{"id":"01a1","cwd":"/r/fake","cli_version":"0.170.0"}}`,
		ev(`{"type":"task_started","turn_id":"t0"}`),
		`{` + ts + `,"type":"turn_context","payload":{"turn_id":"t0","model":"gpt-6.1","effort":"high"}}`,
		item(`{"type":"UserMessage","id":"u0","content":[{"type":"text","text":"ask"}]}`),
		item(`{"type":"Reasoning","id":"r0","summary_text":["Thinking it over."]}`),
		item(`{"type":"CommandExecution","id":"c0","command":["ls"],"status":"completed","aggregated_output":"a.go","exit_code":0}`),
		item(`{"type":"AgentMessage","id":"m0","content":[{"type":"Text","text":"Here it is."}]}`),
		ev(`{"type":"task_complete","turn_id":"t0"}`),
		ev(`{"type":"task_started","turn_id":"t1"}`),
		item(`{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"again"}]}`),
		item(`{"type":"AgentMessage","id":"m1","content":[{"type":"Text","text":"No turn context."}]}`),
		ev(`{"type":"task_complete","turn_id":"t1"}`),
		ev(`{"type":"task_started","turn_id":"t2"}`),
		`{` + ts + `,"type":"turn_context","payload":{"turn_id":"t2","model":"gpt-6.1-sol"}}`,
		`{` + ts + `,"type":"history_mutation","payload":{"display_completions":[{"turn_id":"t2","item":{"type":"AgentMessage","id":"m2","content":[{"type":"Text","text":"Newer format."}]}}]}}`,
		ev(`{"type":"task_complete","turn_id":"t2"}`),
	}
	m := Meta{Codex: true}
	got := map[string]proto.Item{}
	for i, line := range lines {
		for _, it := range ParseLine(i+1, []byte(line), &m) {
			got[it.ID] = it
		}
	}
	want := map[string]string{"u0#0": "", "r0#0": "gpt-6.1", "c0#0": "gpt-6.1", "c0#1": "", "m0#0": "gpt-6.1", "u1#0": "", "m1#0": "", "m2#0": "gpt-6.1-sol"}
	for id, model := range want {
		it, ok := got[id]
		if !ok || it.Model != model {
			t.Errorf("%s: model %q (present %v), want %q", id, it.Model, ok, model)
		}
	}
	if got["c0#0"].Role != "assistant" || got["c0#1"].Role != "user" {
		t.Fatalf("command items: %+v %+v", got["c0#0"], got["c0#1"])
	}
}

// The turn models make no difference between a full read and one opened by the file's end (window.go): its windows
// start at a turn, before the turn's turn_context.
func TestCodexReplyModelFullEqualsWindowed(t *testing.T) {
	defer func(w int64) { windowBytes = w }(windowBytes)
	var lines []string
	turn := 0
	for _, line := range codexTurns(60) {
		lines = append(lines, line)
		if strings.Contains(line, `"type":"task_started"`) {
			if turn%3 != 2 { // every third turn records no model
				lines = append(lines, fmt.Sprintf(`{"timestamp":"2026-11-10T08:00:00.000Z","type":"turn_context","payload":{"turn_id":"t%d","model":"gpt-model-%d"}}`, turn, turn%2))
			}
			turn++
		}
	}
	data := []byte(strings.Join(lines, "\n") + "\n")
	p := filepath.Join(t.TempDir(), "rollout-2026-11-10T08-00-00-01a1.jsonl")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	windowBytes = 1 << 40
	full := NewFile(p)
	if _, err := full.Refresh(); err != nil {
		t.Fatal(err)
	}
	all, _, _ := full.Page(0, 1<<30)
	models := 0
	for _, it := range all {
		if it.Model != "" {
			models++
		}
	}
	if models != 40*4 { // 40 turns with a model, 4 agent messages each
		t.Fatalf("%d items carry a model, want %d", models, 40*4)
	}
	windowBytes = int64(len(data)) / 8
	f := NewFile(p)
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if f.base == 0 {
		t.Fatal("the file was not opened by its end: the test checks nothing")
	}
	var got []proto.Item
	for before := 0; ; {
		items, more, _ := f.Page(before, 7)
		got = append(append([]proto.Item(nil), items...), got...)
		if !more || len(items) == 0 {
			break
		}
		before = items[0].Seq
	}
	if !bytes.Equal(marshal(t, got), marshal(t, all)) {
		t.Fatalf("a windowed read differs from a full read (%d and %d items)", len(got), len(all))
	}
}
