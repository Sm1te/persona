package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// coldEdges reads every path's metadata with an empty cache, as the first session list after a node starts does: a
// workflow agent's (full), or a subagent's.
func coldEdges(tb testing.TB, paths []string, full bool) {
	tb.Helper()
	edgeCache.Lock()
	edgeCache.m = newLRU[string, *edgeEntry](metaCacheMax)
	edgeCache.Unlock()
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			tb.Fatal(err)
		}
		subagentEdgesCached(p, st, full)
	}
}

// writeAgentTranscripts writes n workflow agent transcripts of about size bytes each, shaped like Claude Code 2.1.3xx
// writes them (measured on a real 575-agent workflow corpus: a third of the bytes tool results, a fifth replies, the
// rest attachments and API request records; a user line's type near its start, a reply's after its message).
func writeAgentTranscripts(tb testing.TB, dir string, n, size int) []string {
	tb.Helper()
	stamp := func(i int) string { // a second per step
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second).Format("2006-01-02T15:04:05.000Z")
	}
	at := stamp(0)
	user := func(i int, content string) string {
		return `{"parentUuid":"p","isSidechain":true,"promptId":"pr","agentId":"a-example","type":"user","message":{"role":"user","content":[{"tool_use_id":"t` +
			fmt.Sprint(i) + `","type":"tool_result","content":"` + content + `"}]},"uuid":"u` + fmt.Sprint(i) + `","timestamp":"` + stamp(i) + `"}` + "\n"
	}
	reply := func(i int, block string) string {
		return `{"parentUuid":"p","isSidechain":true,"agentId":"a-example","message":{"id":"msg_` + fmt.Sprint(i) +
			`","type":"message","role":"assistant","model":"model-a","content":[` + block + `],"stop_reason":"tool_use","usage":{"input_tokens":` +
			fmt.Sprint(1000+i) + `,"cache_read_input_tokens":20000,"cache_creation_input_tokens":300,"output_tokens":40}},"requestId":"r","type":"assistant","uuid":"a` +
			fmt.Sprint(i) + `","timestamp":"` + stamp(i) + `"}` + "\n"
	}
	attachment := `{"parentUuid":"p","isSidechain":true,"agentId":"a-example","attachment":{"type":"hook_success","content":"` +
		strings.Repeat("h", 2000) + `"},"type":"attachment","uuid":"x","timestamp":"` + at + `"}` + "\n"
	result := strings.Repeat(`line of a file read \"quoted\" `, 200)
	var paths []string
	for f := range n {
		var b strings.Builder
		b.WriteString(`{"parentUuid":null,"isSidechain":true,"promptId":"pr","agentId":"a-example","type":"user","message":{"role":"user","content":"` +
			strings.Repeat("do the thing ", 300) + `"},"uuid":"u0","timestamp":"` + at + `"}` + "\n")
		for i := 0; b.Len() < size; i++ {
			if i%7 == 3 { // a call with a large input: the reply's type is far from its start
				b.WriteString(reply(i, `{"type":"tool_use","id":"toolu_`+fmt.Sprint(i)+`","name":"Write","input":{"file_path":"/work/project/f`+
					fmt.Sprint(i)+`.go","content":"`+strings.Repeat("func f() {} // more ", 150)+`"}}`))
			} else {
				b.WriteString(reply(i, `{"type":"tool_use","id":"toolu_`+fmt.Sprint(i)+`","name":"Read","input":{"file_path":"/work/project/f`+fmt.Sprint(i)+`.go"}}`))
			}
			if i%25 == 24 {
				b.WriteString(user(i, strings.Repeat(result, 20))) // now and then a large one
			} else {
				b.WriteString(user(i, result))
			}
			b.WriteString(attachment)
			if i%5 == 0 {
				b.WriteString(reply(i, `{"type":"text","text":"`+strings.Repeat("Looking further. ", 40)+`"}`))
			}
		}
		b.WriteString(reply(1<<20, `{"type":"text","text":"Done: no gaps found."}`))
		p := filepath.Join(dir, fmt.Sprintf("agent-a%d.jsonl", f))
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
			tb.Fatal(err)
		}
		paths = append(paths, p)
	}
	return paths
}

// The first session list after a node starts reads every workflow agent and every subagent of the last day: 40
// transcripts of 2 MB.
func BenchmarkSubagentEdgesCold(b *testing.B) {
	paths := writeAgentTranscripts(b, b.TempDir(), 40, 2<<20)
	var total int64
	for _, p := range paths {
		st, _ := os.Stat(p)
		total += st.Size()
	}
	for _, full := range []bool{true, false} {
		b.Run(map[bool]string{true: "workflow_agent", false: "subagent"}[full], func(b *testing.B) {
			coldEdges(b, paths, full)
			data, err := os.ReadFile(paths[0])
			if err != nil {
				b.Fatal(err)
			}
			st, _ := os.Stat(paths[0])
			want := referenceEdges(data)
			if diff := edgesDiff(subagentEdgesCached(paths[0], st, full), want); want.last != "Done: no gaps found." || diff != "" {
				b.Fatalf("edges: %s", diff)
			}
			b.SetBytes(total)
			b.ReportAllocs()
			for b.Loop() {
				coldEdges(b, paths, full)
			}
		})
	}
}

// referenceEdges is what a workflow agent's metadata is over data's complete lines, found the slow way: every line
// parsed, the way the subagent page reads it (transcript.ParseLine).
func referenceEdges(data []byte) subagentEdge {
	var e subagentEdge
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return e
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(data[:end], []byte("\n")) {
		if len(line)+1 > usageMaxLine { // skipped, as eachLine skips it
			continue
		}
		line = bytes.TrimSpace(line)
		var row edgeRow
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		if e.started.IsZero() {
			e.started = row.Timestamp
		}
		if (row.Type == "user" || row.Type == "assistant") && !row.Timestamp.IsZero() {
			e.lastAt = row.Timestamp
		}
		if row.Type != "assistant" || row.Message == nil {
			continue
		}
		for _, b := range row.Message.Content {
			if b.Type == "tool_use" || b.Type == "server_tool_use" {
				if b.ID == "" || !seen[b.ID] {
					e.calls++
				}
				seen[b.ID] = true
			}
		}
		meta := transcript.Meta{Sidechain: true}
		items := transcript.ParseLine(0, line, &meta)
		for i := len(items) - 1; i >= 0; i-- {
			if it := items[i]; it.Kind == proto.KindToolUse {
				e.latestTool = &proto.WorkflowTool{ID: it.ToolUseID, Name: it.ToolName, Summary: transcript.Activity(it.ToolName, it.ToolInput)}
				break
			}
		}
		if meta.LastText != "" {
			e.last = snippet(meta.LastText)
		}
		if t, ok := row.tokens(); ok {
			e.tokens = t
		}
		if m := row.Message.Model; m != "" && m != "<synthetic>" {
			e.model = m
		}
	}
	return e
}

// edgesDiff says how got differs from want ("" when it does not).
func edgesDiff(got, want subagentEdge) string {
	var diff []string
	field := func(name string, g, w any) {
		if fmt.Sprint(g) != fmt.Sprint(w) {
			diff = append(diff, fmt.Sprintf("%s %v, want %v", name, g, w))
		}
	}
	field("started", got.started.UTC(), want.started.UTC())
	field("lastAt", got.lastAt.UTC(), want.lastAt.UTC())
	field("last", got.last, want.last)
	field("model", got.model, want.model)
	field("tokens", got.tokens, want.tokens)
	field("calls", got.calls, want.calls)
	var gt, wt proto.WorkflowTool
	if got.latestTool != nil {
		gt = *got.latestTool
	}
	if want.latestTool != nil {
		wt = *want.latestTool
	}
	field("latest tool", gt, wt)
	return strings.Join(diff, "; ")
}

// expireEdges makes path's cached metadata as old as a subagent's that is read again (metaMinInterval).
func expireEdges(path string) {
	edgeCache.Lock()
	ent, ok := edgeCache.m.peek(path)
	edgeCache.Unlock()
	if ok {
		ent.mu.Lock()
		ent.readAt = time.Time{}
		ent.mu.Unlock()
	}
}

// A workflow agent's metadata from transcripts laid out the way Claude Code writes them (a reply's type after its
// message: lineKind's slower paths), the last ending in an API error, is what parsing every line finds: read cold, and
// read again each time the file grew by an arbitrary number of bytes (cut mid-line, mid-chunk). So is a subagent's
// (#5602, #5030), and one asked for as either in turn: each read takes the lines the other left off at.
func TestSubagentEdgesRealLayout(t *testing.T) {
	paths := writeAgentTranscripts(t, t.TempDir(), 2, 300<<10)
	f, err := os.OpenFile(paths[1], os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(apiErrorReply("err", "2026-01-20T00:00:00Z", "API Error: 500 Server error mid-response"))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(5511, 1))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want := referenceEdges(data)
		if want.calls < 10 || want.tokens == 0 || want.model != "model-a" || want.latestTool == nil || want.latestTool.ID == "" || want.last == "" {
			t.Fatalf("%s: the fixture is not what it should be: %+v", filepath.Base(p), want)
		}
		cuts := []int{len(data)}
		for range 12 {
			cuts = append(cuts, rng.IntN(len(data)))
		}
		slices.Sort(cuts)
		for _, mode := range []struct {
			name string
			full func(read int) bool
		}{
			{"a workflow agent's", func(int) bool { return true }},
			{"a subagent's", func(int) bool { return false }},
			{"each in turn", func(read int) bool { return read%2 == 1 }},
		} {
			full := mode.full(0)
			cold := filepath.Join(t.TempDir(), filepath.Base(p))
			if err := os.WriteFile(cold, data, 0o600); err != nil {
				t.Fatal(err)
			}
			st, err := os.Stat(cold)
			if err != nil {
				t.Fatal(err)
			}
			if diff := edgesDiff(subagentEdgesCached(cold, st, full), referenceEdges(data)); diff != "" {
				t.Fatalf("%s as %s, cold: %s", filepath.Base(p), mode.name, diff)
			}

			grown := filepath.Join(t.TempDir(), filepath.Base(p))
			done := 0
			for i, cut := range cuts {
				f, err := os.OpenFile(grown, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write(data[done:cut])
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				done = cut
				st, err := os.Stat(grown)
				if err != nil {
					t.Fatal(err)
				}
				expireEdges(grown)
				if diff := edgesDiff(subagentEdgesCached(grown, st, mode.full(i)), referenceEdges(data[:cut])); diff != "" {
					t.Fatalf("%s as %s, grown to %d of %d bytes: %s", filepath.Base(p), mode.name, cut, len(data), diff)
				}
			}
		}
	}
}

// PERSONA_BENCH_SUBAGENTS=<dir> times a cold read of every agent-*.jsonl in subagents directories under dir (a real
// Claude Code projects directory, only read). Skipped otherwise.
func TestSubagentEdgesColdRealDir(t *testing.T) {
	root := os.Getenv("PERSONA_BENCH_SUBAGENTS")
	if root == "" {
		t.Skip("PERSONA_BENCH_SUBAGENTS not set")
	}
	var paths []string
	var bytes int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "agent-") && strings.HasSuffix(d.Name(), ".jsonl") &&
			strings.Contains(p, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			if st, err := d.Info(); err == nil {
				paths = append(paths, p)
				bytes += st.Size()
			}
		}
		return nil
	})
	coldEdges(t, paths, true) // warm the page cache
	for _, full := range []bool{true, false} {
		const rounds = 3
		best := time.Duration(1 << 62)
		for range rounds {
			start := time.Now()
			coldEdges(t, paths, full)
			best = min(best, time.Since(start))
		}
		t.Logf("%d agent transcripts, %.1f MB, as a workflow agent's %v: cold read %v (best of %d)", len(paths),
			float64(bytes)/(1<<20), full, best, rounds)
	}
}
