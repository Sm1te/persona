package node

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

func TestNodeNamingMetadataIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	n := &Node{metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
	padding := `{"type":"api-request-blob","data":"` + strings.Repeat("x", quickTail+1) + `"}` + "\n"
	head := `{"type":"user","cwd":"/work/project","message":{"role":"user","content":"Fix the build"}}` + "\n" +
		`{"type":"custom-title","customTitle":"Claude Code"}` + "\n" +
		`{"type":"assistant","message":{"id":"a1","role":"assistant","content":[],"usage":{"input_tokens":1000,"output_tokens":20}}}` + "\n"
	if err := os.WriteFile(path, []byte(head+padding+`{"type":"ai-title","aiTitle":"Generated again"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read := func() quickMeta {
		t.Helper()
		m := n.metas[path]
		m.readAt = time.Time{} // advance the existing throttled refresh without waiting
		n.metas[path] = m
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return n.meta(path, st)
	}
	m := read()
	if m.title != "Claude Code" || !m.titleManual || m.titleSource != "manual" || m.outputTokens != 20 {
		t.Fatalf("historical metadata: title=%q manual=%v source=%q output=%d", m.title, m.titleManual, m.titleSource, m.outputTokens)
	}
	appendRows := func(rows string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(rows); err != nil {
			t.Fatal(err)
		}
	}
	// An input/context change and repeated usage do not count as new generated output.
	appendRows(`{"type":"assistant","message":{"id":"a1","role":"assistant","content":[],"usage":{"input_tokens":8000,"output_tokens":20}}}` + "\n")
	if got := read().outputTokens; got != 20 {
		t.Fatalf("repeated usage output=%d", got)
	}
	// Output counts even when a screenshot pushes the usage fields outside the scanner's first buffer.
	appendRows(`{"type":"assistant","message":{"id":"a2","role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("x", 300<<10) + `"}],"usage":{"input_tokens":9000,"output_tokens":7}}}` + "\n")
	m = read()
	s := proto.Session{Title: m.title}
	setContext(&s, m)
	if s.OutputTokens != 27 || !s.TitleManual || s.TitleSource.String() != "manual" {
		t.Fatalf("session metadata: output=%d manual=%v source=%q", s.OutputTokens, s.TitleManual, s.TitleSource)
	}
	before := listKey([]proto.Session{s})
	s.OutputTokens++
	if before == listKey([]proto.Session{s}) {
		t.Fatal("generated output did not change the published list key")
	}
	before = listKey([]proto.Session{s})
	s.TitleSource = proto.TitleSourceAuto
	if before == listKey([]proto.Session{s}) {
		t.Fatal("title provenance did not change the published list key")
	}
}

func TestNodeNamingMetadataSources(t *testing.T) {
	for _, tc := range []struct {
		name, rows, source string
		output             int
	}{
		{"automatic", `{"type":"ai-title","aiTitle":"Generated"}` + "\n", "auto", 0},
		{"prompt", `{"type":"user","cwd":"/work/project","message":{"role":"user","content":"Fix the build"}}` + "\n", "prompt", 0},
		{"codex", `{"type":"session_meta","payload":{"cwd":"/work/project"}}` + "\n" + `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":321},"last_token_usage":{"input_tokens":5000}}}}` + "\n", "", 321},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(tc.rows), 0600); err != nil {
				t.Fatal(err)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			n := &Node{metas: map[string]quickMeta{}, bgScans: map[string]*bgScan{}}
			m := n.meta(path, st)
			s := proto.Session{Title: m.title}
			untitled(&s, m)
			setContext(&s, m)
			if s.TitleManual || s.TitleSource.String() != tc.source || s.OutputTokens != tc.output {
				t.Fatalf("source=%q manual=%v output=%d", s.TitleSource, s.TitleManual, s.OutputTokens)
			}
		})
	}
}

func TestToolOnlyTitleKeepsKnownPromptProvenance(t *testing.T) {
	s := proto.Session{Title: "◐ Claude Code"}
	setContext(&s, quickMeta{titleSource: "prompt", outputTokens: 10})
	if s.TitleSource != proto.TitleSourcePrompt || s.OutputTokens != 10 {
		t.Fatal("tool fallback lost its proved transcript provenance")
	}
	unknown := proto.Session{Title: "◐ Claude Code"}
	setContext(&unknown, quickMeta{})
	if unknown.TitleSource != proto.TitleSourceUnknown || unknown.TitleManual {
		t.Fatal("missing metadata implied known title provenance")
	}
}

func TestIncompleteNamingScanDoesNotProveAutomaticTitle(t *testing.T) {
	m := quickMeta{title: "Generated", titleSource: "auto", size: 100}
	n := &Node{bgScans: map[string]*bgScan{"s": {off: 50}}}
	n.mergeNamingMetadata("s", &m)
	if m.titleSource != "" {
		t.Fatal("an incomplete or failed historical scan proved automatic provenance")
	}
}

func TestPendingNamingScanDoesNotProveAutomaticTitle(t *testing.T) {
	m := quickMeta{title: "Generated", size: 100}
	n := &Node{bgScans: map[string]*bgScan{"s": {pending: true, meta: transcript.Meta{Title: "Manual", TitleManual: true, TitleSource: "manual", OutputTokens: 42}}}}
	n.mergeNamingMetadata("s", &m)
	if m.titleSource != "" || m.outputTokens != 0 {
		t.Fatal("pending scan published unverified metadata")
	}
	n.bgScans["s"].pending = false
	n.bgScans["s"].off = m.size // the historical scan really completed, not merely stopped
	n.mergeNamingMetadata("s", &m)
	if m.title != "Manual" || !m.titleManual || m.titleSource != "manual" || m.outputTokens != 42 {
		t.Fatal("completed scan did not restore historical manual title and cumulative output")
	}
}

// The streamed background-task scan finds what parsing every short marker line finds, across a line far larger than
// its read buffer and a last line that is only half written when one scan runs.
func TestBackgroundTasksStreamed(t *testing.T) {
	for _, name := range []string{"claude-notifications.jsonl", "claude-session.jsonl", "claude-subagent.jsonl"} {
		src, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(bytes.TrimRight(src, "\n"), []byte("\n"))
		var want transcript.Meta
		for _, l := range lines {
			if len(l) <= 64<<10 && containsAny(l, bgMarkers) {
				transcript.ParseLine(0, l, &want)
			}
		}
		huge := append(append([]byte(`{"type":"user","note":"`), bytes.Repeat([]byte("run_in_background "), 40000)...), `"}`...)
		half := len(lines) / 2
		var head bytes.Buffer
		for _, l := range lines[:half] {
			head.Write(l)
			head.WriteByte('\n')
		}
		head.Write(huge)
		head.WriteByte('\n')
		rest := append(bytes.Join(lines[half:], []byte("\n")), '\n')

		path := filepath.Join(t.TempDir(), "s.jsonl")
		n := &Node{bgScans: map[string]*bgScan{}}
		for _, upto := range []int{len(rest) / 2, len(rest)} {
			if err := os.WriteFile(path, append(bytes.Clone(head.Bytes()), rest[:upto]...), 0o600); err != nil {
				t.Fatal(err)
			}
			n.backgroundTasks(path)
		}
		if got := n.bgScans[path].meta.Background; !reflect.DeepEqual(got, want.Background) {
			t.Errorf("%s: background tasks\n got %+v\nwant %+v", name, got, want.Background)
		}
		if st, _ := os.Stat(path); n.bgScans[path].off != st.Size() {
			t.Errorf("%s: scanned up to %d of %d bytes", name, n.bgScans[path].off, st.Size())
		}
	}
}

// A big transcript's first read runs off the caller's path: the first call answers "not known yet" at once, a later
// one has the tasks, and appended lines are read in line again.
func TestBackgroundTasksBigFirstReadIsAsync(t *testing.T) {
	defer func(v int64) { bgSyncLimit = v }(bgSyncLimit)
	bgSyncLimit = 1 << 10
	src, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "claude-notifications.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var want transcript.Meta
	for _, l := range bytes.Split(bytes.TrimRight(src, "\n"), []byte("\n")) {
		if len(l) <= 64<<10 && containsAny(l, bgMarkers) {
			transcript.ParseLine(0, l, &want)
		}
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	n := &Node{bgScans: map[string]*bgScan{}}
	if _, _, _, pending := n.backgroundTasks(path); !pending {
		t.Fatal("a big first read should not run on the caller's path")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, _, pending := n.backgroundTasks(path); !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the background read never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := n.bgScans[path].meta.Background; !reflect.DeepEqual(got, want.Background) {
		t.Errorf("background tasks\n got %+v\nwant %+v", got, want.Background)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","message":{"role":"user","content":"hi"}}` + "\n")
	f.Close()
	if _, _, _, pending := n.backgroundTasks(path); pending {
		t.Fatal("a small append should be read in line")
	}
}

// A subagent transcript that has not changed is not read again for the session list.
func TestSubagentEdgesCached(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-x.jsonl")
	write := func(text string) os.FileInfo {
		line := `{"type":"assistant","timestamp":"2026-10-29T01:00:00Z","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(path)
		return st
	}
	st := write("one")
	first := subagentEdgesCached(path, st, false).last
	again := subagentEdgesCached(path, st, false).last
	if first != again {
		t.Fatalf("cached read differs: %q vs %q", first, again)
	}
	edgeCache.Lock()
	e, _ := edgeCache.m.peek(path)
	edgeCache.Unlock()
	e.mu.Lock()
	e.readAt = time.Time{} // as if read long ago
	e.mu.Unlock()
	st = write("two, longer")
	if last := subagentEdgesCached(path, st, false).last; last == first && first != "" {
		t.Fatalf("a changed file should be read again, still %q", last)
	}
}

// A goal set at the start of a long transcript is still known once the start is out of the quick tail
// (docs/integrations/agent-goal.md), and its later check is read from the appended lines.
func TestBackgroundTasksKeepsGoal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	set := `{"type":"attachment","uuid":"g1","timestamp":"2026-11-05T08:00:00Z","attachment":{"type":"goal_status","met":false,"sentinel":true,"condition":"ship it"}}` + "\n"
	filler := bytes.Repeat([]byte(`{"type":"assistant","uuid":"x","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}`+"\n"), quickTail/64)
	if err := os.WriteFile(path, append([]byte(set), filler...), 0o600); err != nil {
		t.Fatal(err)
	}
	n := &Node{bgScans: map[string]*bgScan{}}
	if _, _, g, _ := n.backgroundTasks(path); g == nil || g.Objective != "ship it" || g.Status != "active" {
		t.Fatalf("goal after the first read: %+v", g)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"attachment","uuid":"g2","timestamp":"2026-11-05T09:00:00Z","attachment":{"type":"goal_status","met":true,"condition":"ship it","reason":"shipped"}}` + "\n")
	f.Close()
	if _, _, g, _ := n.backgroundTasks(path); g == nil || g.Status != "complete" || g.Reason != "shipped" {
		t.Fatalf("goal after the appended check: %+v", g)
	}
}

// The agent's plan (docs/integrations/agent-goal.md "Plan") written far before the quick tail is known, and follows the calls
// appended later, through the result lines that do not name their tool; the session list's meta carries it.
func TestBackgroundTasksKeepsPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	call := func(id, tool, input string) string {
		return `{"type":"assistant","uuid":"c` + id + `","timestamp":"2026-11-06T08:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"` + tool + `","input":` + input + `}]}}` + "\n"
	}
	result := func(id, at string) string {
		return `{"type":"user","uuid":"r` + id + `","timestamp":"` + at + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}` + "\n"
	}
	list := `{"todos":[{"content":"a","status":"completed","activeForm":"Doing a"},{"content":"b","status":"completed","activeForm":"Doing b"},` +
		`{"content":"c","status":"completed","activeForm":"Doing c"},{"content":"d","status":"in_progress","activeForm":"Reproducing the crash"},` +
		`{"content":"e","status":"pending","activeForm":"Doing e"}]}`
	filler := bytes.Repeat([]byte(`{"type":"assistant","uuid":"x","message":{"role":"assistant","content":[{"type":"text","text":"working"}]}}`+"\n"), quickTail/64)
	head := call("toolu_1", "TodoWrite", list) + result("toolu_1", "2026-11-06T08:00:01Z")
	if err := os.WriteFile(path, append([]byte(head), filler...), 0o600); err != nil {
		t.Fatal(err)
	}
	n := &Node{bgScans: map[string]*bgScan{}, metas: map[string]quickMeta{}}
	st, _ := os.Stat(path)
	m := n.meta(path, st)
	if p := m.plan; p == nil || p.Done != 3 || p.Total != 5 || p.Current != "Reproducing the crash" {
		t.Fatalf("plan from far back: %+v", p)
	}

	// The next step: the result line comes in a later write than its call, with filler between them.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(strings.Replace(list, `"d","status":"in_progress"`, `"d","status":"completed"`, 1), `"e","status":"pending"`, `"e","status":"in_progress"`, 1)
	f.WriteString(call("toolu_2", "TodoWrite", next))
	n.bgPlan(path) // nothing new yet
	n.backgroundTasks(path)
	f.Write(filler[:bytes.Index(filler[4096:], []byte("\n"))+4097]) // whole lines
	f.WriteString(result("toolu_2", "2026-11-06T08:05:00Z"))
	f.Close()
	n.backgroundTasks(path)
	if p := n.bgPlan(path); p == nil || p.Done != 4 || p.Current != "Doing e" || !p.At.Equal(time.Date(2026, 11, 6, 8, 5, 0, 0, time.UTC)) {
		t.Fatalf("plan after the appended call: %+v", p)
	}
}

// A foreground command backgrounded by hand (a message sent mid-turn, #3278) is background work in the session's scan
// too, which reads only the lines that name background work: a Lark turn that ends meanwhile waits for it.
func TestBackgroundTasksSeesManuallyBackgrounded(t *testing.T) {
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-10T09:25:52Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"python3 slow.py","description":"Run a slow test"}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-10T09:26:07Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command was manually backgrounded by user with ID: beg3p1s7d. Output is being written to: /tmp/tasks/beg3p1s7d.output. If it is still running after 30m in the background, it will be stopped and you will be notified."}]}}`,
	}
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := &Node{bgScans: map[string]*bgScan{}}
	n.backgroundTasks(path)
	if got := n.bgScans[path].meta.Background; len(got) != 1 || got[0].ID != "beg3p1s7d" || got[0].Kind != "shell" {
		t.Fatalf("background tasks: %+v", got)
	}
}
