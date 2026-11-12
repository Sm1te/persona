package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Nested subagents name their launcher (parentAgentId) and forks say so (isFork); one whose parent is past the
// cutoff is still listed, naming the parent the list leaves out.
func TestSubagentsLaunchRelation(t *testing.T) {
	const parent = "aaaaaaaa-0000-0000-0000-000000000001"
	home := t.TempDir()
	tp := filepath.Join(home, ".claude", "projects", "-src-app", parent+".jsonl")
	dir := filepath.Join(strings.TrimSuffix(tp, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	write := func(id, meta string, age time.Duration) {
		t.Helper()
		at := now.Add(-age)
		line := `{"type":"user","timestamp":"` + at.UTC().Format(time.RFC3339) + `","isSidechain":true}` + "\n"
		jp := filepath.Join(dir, "agent-"+id+".jsonl")
		if os.WriteFile(jp, []byte(line), 0o600) != nil || os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), []byte(meta), 0o600) != nil {
			t.Fatal("write subagent files")
		}
		if err := os.Chtimes(jp, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("a1", `{"agentType":"general-purpose","description":"示例：主任务","toolUseId":"toolu_a1","spawnDepth":1}`, time.Minute)
	write("b2", `{"agentType":"Explore","description":"示例：已结束","finished":true,"spawnDepth":1}`, 2*time.Minute)
	write("c3", `{"agentType":"general-purpose","finished":true,"parentAgentId":"a1","spawnDepth":2}`, 3*time.Minute)
	write("e5", `{"agentType":"fork","finished":true,"parentAgentId":"c3","spawnDepth":3,"isFork":true}`, 4*time.Minute)
	write("old0", `{"agentType":"general-purpose","finished":true,"spawnDepth":1}`, 25*time.Hour)
	write("kid9", `{"agentType":"Explore","finished":true,"parentAgentId":"old0","spawnDepth":2}`, 5*time.Minute)

	n := &Node{home: home, paths: map[string]string{parent: tp}}
	subs := n.subagents(parent)
	got := map[string]string{}
	for _, a := range subs {
		got[a.ID] = a.ParentID
		if a.Fork != (a.ID == "e5") {
			t.Fatalf("%s fork = %v", a.ID, a.Fork)
		}
	}
	want := map[string]string{"a1": "", "b2": "", "c3": "a1", "e5": "c3", "kid9": "old0"}
	if len(got) != len(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for id, p := range want {
		if q, ok := got[id]; !ok || q != p {
			t.Fatalf("%s: parent %q (listed %v), want %q", id, q, ok, p)
		}
	}
	if subs[0].ID != "a1" || !subs[0].Running {
		t.Fatalf("the running one first: %+v", subs[0])
	}
	// On the wire the relation is left out for the session's own subagents.
	for _, a := range subs {
		b, _ := json.Marshal(a)
		s := string(b)
		switch a.ID {
		case "a1":
			if strings.Contains(s, "parent_id") || strings.Contains(s, `"fork"`) {
				t.Fatalf("a top-level subagent carries the relation: %s", s)
			}
		case "e5":
			if !strings.Contains(s, `"parent_id":"c3"`) || !strings.Contains(s, `"fork":true`) {
				t.Fatalf("a nested fork without its relation: %s", s)
			}
		}
	}
}

// subagentTree makes a session transcript with an empty subagents directory and returns the node, the session id
// and the directory.
func subagentTree(tb testing.TB) (*Node, string, string) {
	tb.Helper()
	const parent = "aaaaaaaa-0000-0000-0000-000000000002"
	home := tb.TempDir()
	tp := filepath.Join(home, ".claude", "projects", "-src-app", parent+".jsonl")
	dir := filepath.Join(strings.TrimSuffix(tp, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte("{}\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	return &Node{home: home, paths: map[string]string{parent: tp}}, parent, dir
}

// writeSubagent writes agent-<id>.jsonl, last touched age ago, and its meta.
func writeSubagent(tb testing.TB, dir, id, meta string, age time.Duration) {
	tb.Helper()
	at := time.Now().Add(-age)
	line := `{"type":"user","timestamp":"` + at.UTC().Format(time.RFC3339) + `","isSidechain":true}` + "\n"
	jp := filepath.Join(dir, "agent-"+id+".jsonl")
	if os.WriteFile(jp, []byte(line), 0o600) != nil || os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), []byte(meta), 0o600) != nil {
		tb.Fatal("write subagent files")
	}
	if err := os.Chtimes(jp, at, at); err != nil {
		tb.Fatal(err)
	}
}

// The session list asks for every live session's subagents each second: 30 finished ones, half of them past the
// one-day cutoff.
func BenchmarkSubagents(b *testing.B) {
	n, parent, dir := subagentTree(b)
	desc := strings.Repeat("x", 300)
	for i := range 30 {
		meta := `{"agentType":"general-purpose","description":"` + desc + `","toolUseId":"toolu_` + strconv.Itoa(i) + `","finished":true}`
		writeSubagent(b, dir, "s"+strconv.Itoa(i), meta, time.Duration(i)*2*time.Hour)
	}
	if got := len(n.subagents(parent)); got != 12 {
		b.Fatalf("listed %d subagents, want 12", got)
	}
	b.ReportAllocs()
	for b.Loop() {
		n.subagents(parent)
	}
}

// The listing caches parsed metas: one rewritten in place (the subagent finished) is read again, one past the
// cutoff is never read, and a deleted subagent drops out of the list.
func TestSubagentsMetaCache(t *testing.T) {
	n, parent, dir := subagentTree(t)
	writeSubagent(t, dir, "live1", `{"agentType":"general-purpose","description":"示例：在跑"}`, time.Minute)
	writeSubagent(t, dir, "gone2", `{"agentType":"Explore","finished":true}`, 2*time.Minute)
	writeSubagent(t, dir, "old3", `{"agentType":"Explore","finished":true}`, 30*time.Hour)

	byID := func() map[string]bool {
		t.Helper()
		got := map[string]bool{}
		for _, a := range n.subagents(parent) {
			got[a.ID] = a.Running
		}
		return got
	}
	if got := byID(); len(got) != 2 || !got["live1"] || got["gone2"] {
		t.Fatalf("first listing %v, want live1 running and gone2 finished", got)
	}
	old := filepath.Join(dir, "agent-old3.meta.json")
	metaCache.Lock()
	_, cached := metaCache.m.peek(old)
	metaCache.Unlock()
	if cached {
		t.Fatal("the meta of a subagent past the cutoff was read")
	}

	// Claude Code rewrites the meta in place when the subagent finishes.
	mp := filepath.Join(dir, "agent-live1.meta.json")
	if err := os.WriteFile(mp, []byte(`{"agentType":"general-purpose","description":"示例：在跑","finished":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := byID(); len(got) != 2 || got["live1"] {
		t.Fatalf("after finishing %v, want live1 not running", got)
	}

	for _, p := range []string{"agent-gone2.jsonl", "agent-gone2.meta.json"} {
		if err := os.Remove(filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	if got := byID(); len(got) != 1 {
		t.Fatalf("after deleting gone2 %v, want only live1", got)
	}
}

// A meta that does not parse (caught mid-write) is cached as a failure and left out until the file changes: once
// rewritten whole, the subagent is listed.
func TestSubagentsMetaCacheParseFailure(t *testing.T) {
	n, parent, dir := subagentTree(t)
	writeSubagent(t, dir, "half4", `{"agentType":"gene`, time.Minute)
	mp := filepath.Join(dir, "agent-half4.meta.json")
	for range 2 { // the second listing is served from the cached failure
		if got := n.subagents(parent); len(got) != 0 {
			t.Fatalf("listed %+v from a meta that does not parse", got)
		}
	}
	metaCache.Lock()
	e, cached := metaCache.m.peek(mp)
	metaCache.Unlock()
	if !cached || e.ok {
		t.Fatalf("parse failure cached = %v, ok = %v; want a cached failure", cached, e.ok)
	}
	if err := os.WriteFile(mp, []byte(`{"agentType":"general-purpose","description":"示例：写完"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := n.subagents(parent)
	if len(got) != 1 || got[0].ID != "half4" || got[0].Description != "示例：写完" || !got[0].Running {
		t.Fatalf("after the meta was written whole: %+v", got)
	}
}

func edgeReply(id, at, model, content, usage string) string {
	if usage != "" {
		usage = `,"usage":` + usage
	}
	return `{"type":"assistant","uuid":"` + id + `","timestamp":"` + at + `","isSidechain":true,"message":{"role":"assistant","model":"` + model + `","content":` + content + usage + `}}` + "\n"
}

// apiErrorReply is the reply Claude Code writes itself when a request fails, in the shape it has in real transcripts
// (2.1.318, a workflow agent's last line): model <synthetic>, isApiErrorMessage, a usage of all zeros.
func apiErrorReply(id, at, text string) string {
	return `{"parentUuid":"p-` + id + `","isSidechain":true,"agentId":"a-example","type":"assistant","uuid":"` + id + `","timestamp":"` + at +
		`","message":{"id":"m-` + id + `","container":null,"model":"<synthetic>","role":"assistant","stop_reason":"stop_sequence",` +
		`"stop_sequence":"","type":"message","usage":{"input_tokens":0,"output_tokens":0,"cache_creation_input_tokens":0,` +
		`"cache_read_input_tokens":0,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":null,` +
		`"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}},"content":[{"type":"text","text":"` + text +
		`"}],"context_management":null},"requestId":"r-` + id + `","error":"unknown","isApiErrorMessage":true,"apiErrorStatus":500}` + "\n"
}

func TestSubagentEdgeDetails(t *testing.T) {
	const start = "2026-01-01T00:00:00Z"
	const end = "2026-01-01T00:01:00Z"
	const usage = `{"input_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":3,"output_tokens":4}`
	read := edgeReply("a", start, "model-a", `[{"type":"tool_use","id":"read1","name":"Read","input":{"file_path":"/work/project/example.go"}}]`, usage)
	server := edgeReply("b", end, "model-b", `[{"type":"server_tool_use","id":"search1","name":"WebSearch","input":{"query":"example\nquery"}}]`, usage)
	finished := `[{"type":"text","text":"Finished"}]`
	for _, tt := range []struct {
		name, body, model, last string
		tokens, calls           int
		tool                    *proto.WorkflowTool
		lastAt                  string
	}{
		{"latest usage is not accumulated", read + server, "model-b", "", 10, 2,
			&proto.WorkflowTool{ID: "search1", Name: "WebSearch", Summary: "在搜网页 example"}, end},
		{"a synthetic reply without usage", read + server + edgeReply("c", end, "<synthetic>", finished, ""),
			"model-b", "Finished", 10, 2, &proto.WorkflowTool{ID: "search1", Name: "WebSearch", Summary: "在搜网页 example"}, end},
		// A failed workflow agent's last line: its usage of zeros is not the agent's.
		{"an API error keeps the usage", read + server + apiErrorReply("c", end, "API Error: 500 Server error mid-response"),
			"model-b", "API Error: 500 Server error mid-response", 10, 2, &proto.WorkflowTool{ID: "search1", Name: "WebSearch", Summary: "在搜网页 example"}, end},
		{"duplicate tool id counts once", read + read + server, "model-b", "", 10, 2,
			&proto.WorkflowTool{ID: "search1", Name: "WebSearch", Summary: "在搜网页 example"}, end},
		{"zero usage keeps the earlier usage", read + edgeReply("b", end, "model-a", finished,
			`{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0}`),
			"model-a", "Finished", 10, 1, &proto.WorkflowTool{ID: "read1", Name: "Read", Summary: "在读 example.go"}, end},
		{"empty usage keeps the earlier usage", read + edgeReply("b", end, "model-a", finished, `{}`),
			"model-a", "Finished", 10, 1, &proto.WorkflowTool{ID: "read1", Name: "Read", Summary: "在读 example.go"}, end},
		// A gateway's replies without input on a turn (transcript/usage_test.go's shape), then the whole turn's input
		// added up on its last (#3300).
		{"a gateway's sum of a turn is not a usage", read + edgeReply("b", end, "model-a", finished, `{"input_tokens":0,"output_tokens":1}`) +
			edgeReply("c", end, "model-a", finished, `{"input_tokens":1700000,"output_tokens":12}`),
			"model-a", "Finished", 10, 1, &proto.WorkflowTool{ID: "read1", Name: "Read", Summary: "在读 example.go"}, end},
		{"no assistant metadata", `{"type":"user","timestamp":"` + start + `"}` + "\n",
			"", "", 0, 0, nil, start},
		{"large tool output preserves earlier details", read + `{"type":"user","timestamp":"` + end + `","message":{"content":"` + strings.Repeat("x", 128<<10) + `"}}` + "\n" +
			`{"type":"system","timestamp":"2026-01-02T00:00:00Z"}` + "\n", "model-a", "", 10, 1,
			&proto.WorkflowTool{ID: "read1", Name: "Read", Summary: "在读 example.go"}, end},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent-test.jsonl")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got := subagentEdgesCached(path, st, true)
			if got.started.Format(time.RFC3339) != start || got.lastAt.Format(time.RFC3339) != tt.lastAt ||
				got.model != tt.model || got.last != tt.last || got.tokens != tt.tokens || got.calls != tt.calls || !reflect.DeepEqual(got.latestTool, tt.tool) {
				t.Fatalf("details = %+v, tool = %+v; want %+v", got, got.latestTool, tt)
			}
			if again := subagentEdgesCached(path, st, true); !reflect.DeepEqual(got, again) {
				t.Fatalf("unchanged file changed details: %+v -> %+v", got, again)
			}
			// A subagent's: the same steps, latest call and times (#5602), usage and model (#5030).
			sub := filepath.Join(t.TempDir(), "agent-test.jsonl")
			if err := os.WriteFile(sub, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if st, err = os.Stat(sub); err != nil {
				t.Fatal(err)
			}
			if s := subagentEdgesCached(sub, st, false); !reflect.DeepEqual(s, got) {
				t.Fatalf("a subagent's = %+v, tool = %+v; want %+v", s, s.latestTool, got)
			}
		})
	}
}

func TestSubagentEdgesAppendAndRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	reply := func(id string, tokens int) string {
		return edgeReply(id, "2026-01-01T00:00:00Z", "model-a", `[{"type":"tool_use","id":"`+id+`","name":"Read","input":{}}]`, `{"input_tokens":`+strconv.Itoa(tokens)+`}`)
	}
	first, second := reply("a", 10), reply("b", 20)
	for _, tt := range []struct {
		name, op, body string
		calls, tokens  int
	}{
		{"first scan", "write", first, 1, 10},
		{"partial append", "append", second[:len(second)/2], 1, 10},
		{"finished append", "append", second[len(second)/2:], 2, 20},
		{"repeated tool", "append", first, 2, 10},
		{"truncated file", "write", second, 1, 20},
		{"larger rewrite", "write", first + second, 2, 20},
		{"replacement file", "replace", second + reply("c", 30) + reply("d", 40), 3, 40},
	} {
		t.Run(tt.name, func(t *testing.T) {
			switch tt.op {
			case "append":
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString(tt.body)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "replace":
				if err := os.WriteFile(path+".new", []byte(tt.body), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+".new", path); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got := subagentEdgesCached(path, st, true)
			if got.calls != tt.calls || got.tokens != tt.tokens {
				t.Fatalf("calls/tokens = %d/%d, want %d/%d", got.calls, got.tokens, tt.calls, tt.tokens)
			}
		})
	}
}

// A running workflow agent is already cached when its last lines come (the workflow cache reads it again every
// 1.5 s), so an API error or a gateway's reply without input is read on its own, from the end of what was read before
// (#5511): the usage read before stands, an error's <synthetic> is not a model, and the words and the time move on.
func TestSubagentEdgesAppendedRepliesKeepUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	const usage = `{"input_tokens":1,"cache_read_input_tokens":2,"cache_creation_input_tokens":3,"output_tokens":4}`
	const apiError = "API Error: 500 Server error mid-response"
	for _, tt := range []struct {
		name, body, model, last, lastAt string
	}{
		{"first scan", edgeReply("a", "2026-01-01T00:00:00Z", "model-a", `[{"type":"tool_use","id":"t1","name":"Read","input":{}}]`, usage),
			"model-a", "", "2026-01-01T00:00:00Z"},
		{"an API error", apiErrorReply("b", "2026-01-01T00:01:00Z", apiError), "model-a", apiError, "2026-01-01T00:01:00Z"},
		// A real reply: its model is the latest, its usage without input is not.
		{"a gateway's reply without input", edgeReply("c", "2026-01-01T00:02:00Z", "model-b", `[{"type":"text","text":"Retrying"}]`,
			`{"input_tokens":0,"output_tokens":1}`), "model-b", "Retrying", "2026-01-01T00:02:00Z"},
		{"all-zero usage", edgeReply("d", "2026-01-01T00:03:00Z", "model-b", `[{"type":"text","text":"Still here"}]`,
			`{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":0}`), "model-b", "Still here",
			"2026-01-01T00:03:00Z"},
		{"another API error", apiErrorReply("e", "2026-01-01T00:04:00Z", apiError), "model-b", apiError, "2026-01-01T00:04:00Z"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(tt.body)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got := subagentEdgesCached(path, st, true)
			if got.calls != 1 || got.tokens != 10 || got.model != tt.model || got.last != tt.last ||
				got.lastAt.Format(time.RFC3339) != tt.lastAt || got.latestTool == nil || got.latestTool.Name != "Read" {
				t.Fatalf("edges = %+v, tool %+v; want calls 1, tokens 10, model %q, last %q at %s", got, got.latestTool, tt.model, tt.last, tt.lastAt)
			}
		})
	}
}

func TestSubagentEdgesSkipOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	body := `{"type":"user","timestamp":"2026-01-01T00:00:00Z"}` + "\n" + strings.Repeat("x", usageMaxLine+1) + "\n" +
		edgeReply("after", "2026-01-01T00:01:00Z", "model-a", `[{"type":"tool_use","id":"a","name":"Read","input":{}}]`, `{"input_tokens":10}`)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := subagentEdgesCached(path, st, true); got.calls != 1 || got.tokens != 10 || got.model != "model-a" {
		t.Fatalf("details after oversized line = %+v", got)
	}
}

// A listed subagent says how many tool calls it made and which it made last, and its updated_at is when it last wrote
// a message, not its file's mtime (#5602: the mtime moved about every second, so the list changed on every push and
// the apps drew every chat again).
func TestSubagentListsStepsLastToolAndContentTime(t *testing.T) {
	const parent = "aaaaaaaa-0000-0000-0000-000000000009"
	home := t.TempDir()
	tp := filepath.Join(home, ".claude", "projects", "-src-app", parent+".jsonl")
	dir := filepath.Join(strings.TrimSuffix(tp, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second)
	ts := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339) }
	lines := strings.Join([]string{
		`{"type":"user","timestamp":"` + ts(0) + `","isSidechain":true,"message":{"role":"user","content":"look at the tests"}}`,
		`{"type":"assistant","timestamp":"` + ts(time.Second) + `","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_s1","name":"Read","input":{"file_path":"/src/app/a_test.go"}}]}}`,
		`{"type":"user","timestamp":"` + ts(2*time.Second) + `","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_s1","content":"ok"}]}}`,
		`{"type":"assistant","timestamp":"` + ts(3*time.Second) + `","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_s2","name":"Bash","input":{"command":"go test ./...","description":"run the tests"}}]}}`,
	}, "\n") + "\n"
	jp := filepath.Join(dir, "agent-s1.jsonl")
	if os.WriteFile(jp, []byte(lines), 0o600) != nil || os.WriteFile(filepath.Join(dir, "agent-s1.meta.json"), []byte(`{"agentType":"general-purpose","description":"示例：跑测试","toolUseId":"toolu_p1"}`), 0o600) != nil {
		t.Fatal("write subagent files")
	}
	n := &Node{home: home, paths: map[string]string{parent: tp}}
	subs := n.subagents(parent)
	if len(subs) != 1 {
		t.Fatalf("listed %+v", subs)
	}
	a := subs[0]
	if a.Steps != 2 {
		t.Fatalf("steps %d, want 2", a.Steps)
	}
	if a.LastTool == nil || a.LastTool.ID != "toolu_s2" || a.LastTool.Name != "Bash" || a.LastTool.Summary == "" {
		t.Fatalf("last tool %+v", a.LastTool)
	}
	if want := at.Add(3 * time.Second); !a.UpdatedAt.Equal(want) {
		t.Fatalf("updated_at %v, want its last message's %v", a.UpdatedAt, want)
	}
	// the file is touched without new content: the list says the same
	later := time.Now()
	if err := os.Chtimes(jp, later, later); err != nil {
		t.Fatal(err)
	}
	if again := n.subagents(parent)[0]; !again.UpdatedAt.Equal(a.UpdatedAt) {
		t.Fatalf("updated_at moved with the mtime: %v", again.UpdatedAt)
	}
}

// A listed subagent says which model it ran on and how many tokens its latest reply had (#5030), from its own
// transcript: Claude Code's error replies (<synthetic>) are neither. A later reply moves both, at the list's pace: a
// growing file is read again at most every metaMinInterval.
func TestSubagentListsModelAndTokens(t *testing.T) {
	n, parent, dir := subagentTree(t)
	jp := filepath.Join(dir, "agent-m1.jsonl")
	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	ts := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339) }
	appendLines := func(lines ...string) {
		t.Helper()
		f, err := os.OpenFile(jp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(strings.Join(lines, ""))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	listed := func() proto.Subagent {
		t.Helper()
		subs := n.subagents(parent)
		if len(subs) != 1 {
			t.Fatalf("listed %+v", subs)
		}
		return subs[0]
	}
	if err := os.WriteFile(filepath.Join(dir, "agent-m1.meta.json"), []byte(`{"agentType":"Explore","description":"示例：找测试"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	appendLines(`{"type":"user","timestamp":"`+ts(0)+`","isSidechain":true,"message":{"role":"user","content":"find the tests"}}`+"\n",
		edgeReply("r1", ts(time.Second), "claude-haiku-4-5", `[{"type":"tool_use","id":"toolu_m1","name":"Grep","input":{"pattern":"func Test"}}]`,
			`{"input_tokens":100,"cache_read_input_tokens":20000,"cache_creation_input_tokens":300,"output_tokens":50}`))
	if a := listed(); a.Model != "claude-haiku-4-5" || a.Tokens != 20450 || a.Steps != 1 {
		t.Fatalf("first reply: model %q tokens %d steps %d", a.Model, a.Tokens, a.Steps)
	}

	appendLines(`{"type":"user","timestamp":"`+ts(2*time.Second)+`","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_m1","content":"ok"}]}}`+"\n",
		edgeReply("r2", ts(3*time.Second), "claude-sonnet-4-5", `[{"type":"text","text":"Found them"}]`,
			`{"input_tokens":200,"cache_read_input_tokens":20500,"cache_creation_input_tokens":0,"output_tokens":80}`),
		apiErrorReply("r3", ts(4*time.Second), "API Error: 500 Server error mid-response"))
	if a := listed(); a.Model != "claude-haiku-4-5" || a.Tokens != 20450 {
		t.Fatalf("read again before metaMinInterval: model %q tokens %d", a.Model, a.Tokens)
	}
	expireEdges(jp)
	if a := listed(); a.Model != "claude-sonnet-4-5" || a.Tokens != 20780 || a.LastText != "API Error: 500 Server error mid-response" {
		t.Fatalf("after a later reply and an error: model %q tokens %d last %q", a.Model, a.Tokens, a.LastText)
	}
}

// Asked for as a workflow agent's after a subagent's, or the other way round, a file is read only for the lines it
// gained since, never whole again. Claude Code only appends; the call rewritten in place here (past the bytes that
// tell a rewritten file, edgeMemo.head) only shows whether the lines before were read again.
func TestSubagentEdgesEitherWayReadsOnlyNewLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	prompt := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"` + strings.Repeat("p", 300) + `"}}` + "\n"
	call := func(id string, tokens int) string {
		return edgeReply(id, "2026-01-01T00:00:01Z", "model-a", `[{"type":"tool_use","id":"`+id+`","name":"Read","input":{}}]`,
			`{"input_tokens":`+strconv.Itoa(tokens)+`}`)
	}
	if err := os.WriteFile(path, []byte(prompt+call("t1", 5)), 0o600); err != nil {
		t.Fatal(err)
	}
	read := func(full bool, calls, tokens int) {
		t.Helper()
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		expireEdges(path)
		if got := subagentEdgesCached(path, st, full); got.calls != calls || got.tokens != tokens || got.model != "model-a" {
			t.Fatalf("full %v: calls %d tokens %d model %q, want %d, %d, model-a", full, got.calls, got.tokens, got.model, calls, tokens)
		}
	}
	read(false, 1, 5)
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	words := edgeReply("t1", "2026-01-01T00:00:01Z", "model-a", `[{"type":"text","text":"`+strings.Repeat("w", 29)+`"}]`, `{"input_tokens":5}`)
	if len(words) != len(call("t1", 5)) {
		t.Fatalf("the rewritten line is %d bytes, not %d", len(words), len(call("t1", 5)))
	}
	_, err = f.WriteAt([]byte(words), int64(len(prompt)))
	if err == nil {
		_, err = f.WriteAt([]byte(call("t2", 7)), int64(len(prompt)+len(words)))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	read(true, 2, 7) // read whole again, t1 would be gone: 1 call
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(call("t3", 9))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	read(false, 3, 9)
}

// A subagent's metadata is all of a workflow agent's: its first timestamp, its steps and latest call (#5602), its last
// exchange, its last words however far back a large tool output pushed them, and its latest usage and model (#5030).
func TestSubagentEdgesLastWords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	body := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"go"}}` + "\n" +
		edgeReply("a", "2026-01-01T00:00:01Z", "model-a", `[{"type":"text","text":"Found it"}]`, `{"input_tokens":5}`) +
		edgeReply("b", "2026-01-01T00:00:02Z", "model-a", `[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/x/a.go"}}]`, `{"input_tokens":6}`) +
		`{"type":"user","timestamp":"2026-01-01T00:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"` +
		strings.Repeat("y", 300<<10) + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	read := &proto.WorkflowTool{ID: "t1", Name: "Read", Summary: "在读 a.go"}
	got := subagentEdgesCached(path, st, false)
	if got.last != "Found it" || got.started.Format(time.RFC3339) != "2026-01-01T00:00:00Z" || got.calls != 1 ||
		!reflect.DeepEqual(got.latestTool, read) || got.lastAt.Format(time.RFC3339) != "2026-01-01T00:00:03Z" || got.tokens != 6 || got.model != "model-a" {
		t.Fatalf("a subagent's edges = %+v, tool %+v", got, got.latestTool)
	}
	// Asked for as a workflow agent's, the unchanged file is not read again, and the other way round.
	if full := subagentEdgesCached(path, st, true); full != got {
		t.Fatalf("as a workflow agent's: %+v", full)
	}
	if again := subagentEdgesCached(path, st, false); again != got {
		t.Fatalf("as a subagent's again: %+v", again)
	}
}

// Read from the end, a line too long to keep (a picture) is skipped, and the words before it are found.
func TestSubagentEdgesLastWordsBeforeOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	body := `{"type":"user","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		edgeReply("a", "2026-01-01T00:00:01Z", "model-a", `[{"type":"text","text":"Before the picture"}]`, "") +
		strings.Repeat("x", usageMaxLine+1) + "\n" + `{"type":"system","timestamp":"2026-01-01T00:00:02Z"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := subagentEdgesCached(path, st, false); got.last != "Before the picture" || got.started.IsZero() {
		t.Fatalf("edges = %+v", got)
	}
}

// Many list requests read the same growing file at once: each file is read by one at a time, and every call counted
// once (go test -race).
func TestSubagentEdgesConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-test.jsonl")
	reply := func(i int) string {
		id := strconv.Itoa(i)
		return edgeReply(id, "2026-01-01T00:00:00Z", "model-a", `[{"type":"tool_use","id":"t`+id+`","name":"Read","input":{}}]`, `{"input_tokens":`+id+`}`)
	}
	if err := os.WriteFile(path, []byte(reply(1)), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := range 6 {
		wg.Go(func() {
			for range 40 {
				if st, err := os.Stat(path); err == nil {
					subagentEdgesCached(path, st, g%2 == 0)
				}
			}
		})
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 30; i++ {
		if _, err := f.WriteString(reply(i)); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	wg.Wait()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := subagentEdgesCached(path, st, true); got.calls != 30 || got.tokens != 30 {
		t.Fatalf("after concurrent reads: calls %d tokens %d, want 30 and 30", got.calls, got.tokens)
	}
}

// lineKind tells the lines apart where Claude Code puts their type, and never takes an assistant line for another
// because of what a tool call's input holds near the line's start.
func TestLineKind(t *testing.T) {
	big := strings.Repeat("z", 8<<10)
	for _, c := range []struct {
		name, line string
		want       byte
	}{
		{"user", `{"parentUuid":"p","isSidechain":true,"promptId":"x","agentId":"a","type":"user","message":{"role":"user","content":"` + big + `"},"timestamp":"t"}`, 'u'},
		{"reply, type after the message", `{"parentUuid":"p","isSidechain":true,"agentId":"a","message":{"role":"assistant","content":[{"type":"text","text":"` + big + `"}]},"type":"assistant","uuid":"u"}`, 'a'},
		{"reply, type first", `{"parentUuid":"p","isSidechain":true,"agentId":"a","type":"assistant","uuid":"u","message":{"role":"assistant","content":[{"type":"text","text":"` + big + `"}]}}`, 'a'},
		{"reply whose input has an attachment", `{"parentUuid":"p","agentId":"a","message":{"content":[{"type":"tool_use","id":"t","name":"mcp__mail__send","input":{"attachment":{"name":"a.pdf"},"body":"` + big + `"}}]},"type":"assistant"}`, 'a'},
		{"reply whose input has a user message", `{"parentUuid":"p","agentId":"a","message":{"content":[{"type":"tool_use","id":"t","name":"mcp__x__chat","input":{"messages":[{"type":"user","text":"hi"}],"pad":"` + big + `"}}]},"type":"assistant"}`, 'a'},
		{"reply with a key after its type far from the end", `{"message":{"content":[]},"type":"assistant","extra":"` + big + `"}`, 'a'},
		{"attachment", `{"parentUuid":"p","isSidechain":true,"agentId":"a","attachment":{"type":"hook_success","content":"` + big + `"},"type":"attachment","uuid":"u"}`, 0},
		{"API request", `{"type":"api-request-blob","sessionId":"s","body":"` + big + `"}`, 0},
		{"system", `{"type":"system","subtype":"turn_duration","durationMs":1}`, 0},
		{"user, type after the message", `{"message":{"role":"user","content":"x"},"type":"user","pad":"` + big + `"}`, 'u'},
	} {
		if got := lineKind([]byte(c.line)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A subagent that reports with SubagentHandback (Claude Code 2.1.318+, #5886): the hand-back is its report, not a step
// it took: two commands are two steps, its latest call the second command, its last words the report.
func TestSubagentHandbackIsNoStep(t *testing.T) {
	const parent = "aaaaaaaa-0000-0000-0000-000000000010"
	home := t.TempDir()
	tp := filepath.Join(home, ".claude", "projects", "-src-app", parent+".jsonl")
	dir := filepath.Join(strings.TrimSuffix(tp, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tp, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	lines := strings.Join([]string{
		`{"type":"user","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"user","content":"run two commands"}}`,
		`{"type":"assistant","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"I'll run the first command."},{"type":"tool_use","id":"toolu_c1","name":"Bash","input":{"command":"echo one"}}]}}`,
		`{"type":"user","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_c1","content":"one"}]}}`,
		`{"type":"assistant","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_c2","name":"Bash","input":{"command":"echo two"}}]}}`,
		`{"type":"user","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_c2","content":"two"}]}}`,
		`{"type":"assistant","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_hb","name":"SubagentHandback","input":{"message":"Both ran: one, two."}}]}}`,
		`{"type":"user","timestamp":"` + ts + `","isSidechain":true,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_hb","content":"{\"success\":true}"}]}}`,
	}, "\n") + "\n"
	if os.WriteFile(filepath.Join(dir, "agent-h1.jsonl"), []byte(lines), 0o600) != nil || os.WriteFile(filepath.Join(dir, "agent-h1.meta.json"), []byte(`{"agentType":"general-purpose","description":"run two commands","toolUseId":"toolu_p2"}`), 0o600) != nil {
		t.Fatal("write subagent files")
	}
	n := &Node{home: home, paths: map[string]string{parent: tp}}
	subs := n.subagents(parent)
	if len(subs) != 1 {
		t.Fatalf("listed %+v", subs)
	}
	a := subs[0]
	if a.Steps != 2 {
		t.Fatalf("steps %d, want 2 (the hand-back is no step)", a.Steps)
	}
	if a.LastTool == nil || a.LastTool.ID != "toolu_c2" {
		t.Fatalf("last tool %+v, want the second command", a.LastTool)
	}
	if a.LastText != "Both ran: one, two." {
		t.Fatalf("last text %q, want its report", a.LastText)
	}
}
