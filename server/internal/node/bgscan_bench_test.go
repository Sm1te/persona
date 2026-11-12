package node

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/nwverse/persona/server/internal/transcript"
)

// growingLine is one line a busy session appends between two session lists; it names a background marker, so the
// scan parses it.
const growingLine = `{"type":"user","uuid":"u","timestamp":"2026-11-06T08:00:00Z","message":{"role":"user","content":[` +
	`{"type":"tool_result","tool_use_id":"toolu_x","content":"Command running in background with ID: none"}]}}` + "\n"

// growingTranscript is a transcript backgroundTasks has already read once, open for appending.
func growingTranscript(tb testing.TB) (*Node, string, *os.File) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "s.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { f.Close() })
	n := &Node{bgScans: map[string]*bgScan{}}
	n.backgroundTasks(path)
	return n, path, f
}

// BenchmarkBackgroundTasksGrowing is the session list's background-task scan of a transcript that grew by one line
// since the last list, as each busy session's is every few seconds.
func BenchmarkBackgroundTasksGrowing(b *testing.B) {
	n, path, f := growingTranscript(b)
	line := []byte(growingLine)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := f.Write(line); err != nil {
			b.Fatal(err)
		}
		n.backgroundTasks(path)
	}
}

// bytesPerRun is testing.AllocsPerRun counting bytes: the average heap bytes one call of fn allocates.
func bytesPerRun(runs int, fn func()) float64 {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	fn() // warm up
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)
	return float64(after.TotalAlloc-before.TotalAlloc) / float64(runs)
}

// Scanning what a transcript gained since the last session list reuses the read buffer instead of allocating 256 KiB
// each time. The bound leaves room for -race, whose sync.Pool drops a quarter of what is put back (about 90 KB a
// scan there, about 2 KB without it).
func TestBackgroundTasksGrowingReusesReader(t *testing.T) {
	n, path, f := growingTranscript(t)
	line := []byte(growingLine)
	per := bytesPerRun(400, func() {
		if _, err := f.Write(line); err != nil {
			t.Fatal(err)
		}
		n.backgroundTasks(path)
	})
	if per > 128<<10 {
		t.Errorf("a one-line scan allocates %.0f bytes, want under %d", per, 128<<10)
	}
	if st, _ := os.Stat(path); n.bgScans[path].off != st.Size() {
		t.Errorf("scanned up to %d of %d bytes", n.bgScans[path].off, st.Size())
	}
}

// referenceBackground is what scanBackground finds in src read whole: the complete short lines it parses, in order.
func referenceBackground(src []byte) transcript.Meta {
	var m transcript.Meta
	for {
		i := bytes.IndexByte(src, '\n')
		if i < 0 {
			return m
		}
		l := src[:i]
		src = src[i+1:]
		if len(l) < 64<<10 && (containsAny(l, bgMarkers) || containsAny(l, transcript.GoalMarkers) || containsAny(l, transcript.UltracodeMarkers) ||
			containsAny(l, transcript.PlanMarkers) || containsAny(l, m.PlanPending())) {
			transcript.ParseLine(0, bytes.Clone(l), &m)
		}
		m.ObserveNamingMetadata(l)
	}
}

// The pooled reader finds exactly what reading the whole file finds, across lines longer than the buffer and lines
// cut mid-write, while other transcripts' scans reuse the buffer between calls (nothing the meta keeps may point
// into it).
func TestBackgroundTasksPooledMatchesWholeRead(t *testing.T) {
	var src bytes.Buffer
	for _, name := range []string{"claude-background.jsonl", "claude-compact.jsonl", "claude-session.jsonl", "claude-subagent.jsonl", "claude-ultracode.jsonl"} {
		b, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
		if name == "claude-compact.jsonl" { // markers in lines the scan skips: longer than the buffer, and over 64 KiB
			src.WriteString(`{"type":"user","note":"` + string(bytes.Repeat([]byte("run_in_background "), 20000)) + `"}` + "\n")
			src.WriteString(`{"type":"user","note":"` + string(bytes.Repeat([]byte("CronCreate "), 8000)) + `"}` + "\n")
		}
	}
	call := `{"type":"assistant","uuid":"c1","timestamp":"2026-11-06T08:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_p","name":"TodoWrite",` +
		`"input":{"todos":[{"content":"a","status":"completed","activeForm":"Doing a"},{"content":"b","status":"in_progress","activeForm":"Doing b"}]}}]}}` + "\n"
	result := `{"type":"user","uuid":"r1","timestamp":"2026-11-06T08:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_p","content":"ok"}]}}` + "\n"
	goal := `{"type":"attachment","uuid":"g1","timestamp":"2026-11-05T08:00:00Z","attachment":{"type":"goal_status","met":false,"sentinel":true,"condition":"ship it"}}` + "\n"
	shell := `{"type":"assistant","uuid":"c2","timestamp":"2026-11-06T08:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_b","name":"Bash",` +
		`"input":{"command":"make watch","description":"Watch the build","run_in_background":true}}]}}` + "\n" +
		`{"type":"user","uuid":"r2","timestamp":"2026-11-06T08:01:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_b",` +
		`"content":"Command running in background with ID: bgshell1. Output is being written to: /work/project/bgshell1.output"}]}}` + "\n"
	cron := `{"type":"assistant","uuid":"c3","timestamp":"2026-11-06T08:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_c","name":"CronCreate",` +
		`"input":{"cron":"*/5 * * * *","prompt":"check the build","recurring":true}}]}}` + "\n"
	src.WriteString(goal + call + result + shell + cron)
	data := src.Bytes()
	want := referenceBackground(data)
	if len(want.Background) == 0 || want.Loop == nil || want.Goal == nil || want.Plan == nil {
		t.Fatalf("the fixture should hold tasks, a loop, a goal and a plan: %d tasks, loop %v, goal %v, plan %v", len(want.Background), want.Loop, want.Goal, want.Plan)
	}

	dir := t.TempDir()
	path, other := filepath.Join(dir, "s.jsonl"), filepath.Join(dir, "other.jsonl")
	scribble := bytes.Repeat([]byte(`{"type":"assistant","uuid":"x","message":{"role":"assistant","content":[{"type":"text","text":"zzzzzzzz"}]}}`+"\n"), 4000)
	n := &Node{bgScans: map[string]*bgScan{}}
	var sc *bgScan
	for upto := 0; ; upto += 37<<10 + 13 { // cuts land mid-line
		upto = min(upto, len(data))
		if err := os.WriteFile(path, data[:upto], 0o600); err != nil {
			t.Fatal(err)
		}
		n.backgroundTasks(path)
		if err := os.WriteFile(other, scribble, 0o600); err != nil {
			t.Fatal(err)
		}
		delete(n.bgScans, other)
		n.backgroundTasks(other)
		if upto == len(data) {
			sc = n.bgScans[path]
			break
		}
	}
	if sc.off != int64(len(data)) {
		t.Errorf("scanned up to %d of %d bytes", sc.off, len(data))
	}
	if !reflect.DeepEqual(sc.meta.Background, want.Background) {
		t.Errorf("background tasks\n got %+v\nwant %+v", sc.meta.Background, want.Background)
	}
	if !reflect.DeepEqual(sc.meta, want) { // everything else the parsed lines set, unexported state included
		t.Error("the pooled scan's meta differs from reading the whole file")
	}
	_, gotLoop, gotGoal, _ := n.backgroundTasks(path)
	if gotGoal == nil || !reflect.DeepEqual(*gotGoal, *want.Goal) {
		t.Errorf("goal %+v, want %+v", gotGoal, want.Goal)
	}
	if gotLoop == nil || !reflect.DeepEqual(*gotLoop, *want.Loop) {
		t.Errorf("loop %+v, want %+v", gotLoop, want.Loop)
	}
	if p := n.bgPlan(path); p == nil || !reflect.DeepEqual(*p, *want.Plan) {
		t.Errorf("plan %+v, want %+v", p, want.Plan)
	}
}
