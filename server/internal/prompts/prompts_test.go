package prompts

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Names lists every template, parsing all of them.
func Names() []string {
	if err := templates.parseAll(); err != nil {
		panic(fmt.Sprintf("prompts: %v", err))
	}
	var out []string
	for _, t := range templates.root.Templates() {
		if t.Name() != "" {
			out = append(out, t.Name())
		}
	}
	return out
}

// parseAll adds every template file to the set.
func (s *templateSet) parseAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") || strings.HasPrefix(path, "mcp/") {
			return err // mcp/ holds tool definitions, not templates (mcp.go)
		}
		return s.parse(strings.TrimSuffix(path, ".md"))
	})
}

// ToolNames lists every MCP tool defined here.
func ToolNames() []string {
	out := make([]string, 0, len(mcpTools()))
	for n := range mcpTools() {
		out = append(out, n)
	}
	return out
}

// Each prompt's output is pinned by the golden tests of the package that uses it (hub, kb, node: *_golden_test.go);
// these check the package's own rules.

// Every file ends with a newline (it is dropped), so adding one in an editor never changes a prompt.
func TestFilesEndWithANewline(t *testing.T) {
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".md") {
			t.Errorf("%s: only .md files belong here", path)
		}
		raw, _ := files.ReadFile(path)
		if !strings.HasSuffix(string(raw), "\n") {
			t.Errorf("%s does not end with a newline", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every template is used: rendered from Go here, or included by another template.
func TestEveryTemplateIsUsed(t *testing.T) {
	var src strings.Builder
	_ = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")) {
			raw, _ := os.ReadFile(path)
			src.Write(raw)
		}
		return nil
	})
	names := Names()
	if len(names) < 40 {
		t.Fatalf("only %d templates parsed", len(names))
	}
	for _, n := range names {
		if !strings.Contains(src.String(), `"`+n+`"`) {
			t.Errorf("template %s is never rendered or included", n)
		}
	}
}

// A template is parsed the first time it is rendered, with the templates it includes. On a fresh set, rendering any
// one of them parses every template it may run, read here from the printed trees of the fully parsed set, so no data
// and no branch reaches "no such template"; and only those, so a process holds only the prompts it writes (#2475).
func TestEachTemplateParsesItsIncludes(t *testing.T) {
	names := Names()
	included := map[string][]string{}
	for _, name := range names {
		for _, m := range templateNode.FindAllStringSubmatch(templates.root.Lookup(name).Root.String(), -1) {
			included[name] = append(included[name], m[1])
		}
	}
	if len(included) == 0 {
		t.Fatal("no template includes another")
	}
	for _, name := range names {
		fresh := newTemplateSet()
		if _, err := fresh.lookup(name); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		seen := map[string]bool{}
		for todo := []string{name}; len(todo) > 0; {
			n := todo[len(todo)-1]
			if todo = todo[:len(todo)-1]; seen[n] {
				continue
			}
			seen[n] = true
			if fresh.root.Lookup(n) == nil {
				t.Errorf("rendering %s did not parse %s, which it may run", name, n)
			}
			todo = append(todo, included[n]...)
		}
		parsed := 0
		for _, p := range fresh.root.Templates() {
			if p.Name() != "" {
				parsed++
			}
		}
		if parsed != len(seen) {
			t.Errorf("rendering %s parsed %d templates, not only the %d it may run", name, parsed, len(seen))
		}
	}
}

var templateNode = regexp.MustCompile(`\{\{template "([^"]+)"`)

// Templates are parsed while others render: on a fresh set, goroutines render templates that share includes, each
// the same as on the fully parsed set (run it with -race).
func TestFirstRendersRunConcurrently(t *testing.T) {
	now := time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC)
	cases := []struct {
		name string
		data any
	}{
		{"tools/translate_texts", "English"},
		{"tools/translate_mail", "English"},
		{"voice/instructions", VoiceInstructions{}},
		{"voice/report", VoiceReport{}},
		{"assistant/snapshot", Snapshot{Now: now, Zone: "UTC", Calendar: &SnapshotCalendar{}}},
		{"kb/compress_input", KBCompressInput{}},
		{"kb/items_input", KBItemsInput{}},
	}
	Names() // the package's set, every template parsed
	fresh := newTemplateSet()
	var wg sync.WaitGroup
	for g := range 4 {
		for i := range cases {
			c := cases[(i+g)%len(cases)]
			wg.Go(func() {
				if got, want := fresh.render(c.name, c.data), templates.render(c.name, c.data); got != want {
					t.Errorf("%s: a first render on a fresh set gives %q, the parsed set %q", c.name, got, want)
				}
			})
		}
	}
	wg.Wait()
}

// A field the data lacks is an error, not an empty string in a prompt.
func TestMissingFieldPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("rendering with the wrong data should panic")
		}
	}()
	render("inbox/triage", struct{ Owner string }{"x"})
}

func TestSmallPrompts(t *testing.T) {
	for got, want := range map[string]string{
		RenderReadonlyTaskPlanExit():       "This task is read-only. Stay in plan mode; do not request implementation approval. Return your findings and report completion to your supervisor with report_to_supervisor.",
		RenderReadonlyTaskConstraint():     "本任务只读：不改项目文件、不提交、不对外写入",
		RenderQuestionCancelled():          "The user closed this question without answering. Continue without an answer; do not repeat this question.",
		RenderQuestionUserSpoke():          "The user did not pick an option: they wrote a new message in this conversation instead. It is delivered when you end this turn, so end the turn now and do not call ask_user again in it. Read their message then: it may be the answer; if you still need one, ask again after reading it.",
		RenderRefused("他说不行"):              "（没有执行）他说不行",
		RenderLauncherCheck():              "Reply with exactly one word: ok",
		RenderJSONOnly("system"):           "system\n\nReply with a single JSON object only, no prose, no code fences.",
		RenderTranslateMailInput("S", "T"): "主题：S\n\nT",
		RenderCodexInstructions("mem"):     "mem",
		SnapshotCut():                      "- 快照到此截断；其余用 agents_list、todo_list、inbox 查",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// Every MCP tool file is offered by some server code (named in a Go file outside this package), and has a
// description and an object schema.
func TestEveryMCPToolIsUsed(t *testing.T) {
	var src strings.Builder
	_ = filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") &&
			!strings.HasPrefix(path, filepath.Join("..", "prompts")) {
			raw, _ := os.ReadFile(path)
			src.Write(raw)
		}
		return nil
	})
	names := ToolNames()
	if len(names) < 20 {
		t.Fatalf("only %d MCP tools", len(names))
	}
	for _, n := range names {
		tool := Tool(n)
		if !strings.Contains(src.String(), `"`+n+`"`) {
			t.Errorf("MCP tool %s is never offered", n)
		}
		if strings.TrimSpace(tool.Description) == "" || !strings.HasPrefix(string(tool.InputSchema), `{"type":"object"`) {
			t.Errorf("%s: description %q, schema %s", n, tool.Description, tool.InputSchema)
		}
	}
}

// The "## MCP tools" table in docs/assistant/prompts.md has a row for every mcp/<dir>/, that row names each of the directory's
// tools, and every tool it names exists.
func TestMCPToolsTableListsEveryTool(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "assistant", "prompts.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(raw), "\n## MCP tools\n")
	if !ok {
		t.Fatal(`docs/assistant/prompts.md has no "## MCP tools" section`)
	}
	section, _, _ = strings.Cut(section, "\n## ")
	rows := map[string]string{} // directory -> its Tools cell
	for _, line := range strings.Split(section, "\n") {
		cells := strings.Split(line, " | ")
		if len(cells) < 3 || !strings.HasPrefix(cells[0], "| `mcp/") {
			continue
		}
		dir := strings.TrimSuffix(strings.TrimPrefix(cells[0], "| `mcp/"), "/`")
		if _, dup := rows[dir]; dup {
			t.Errorf("mcp/%s/ has two rows", dir)
		}
		rows[dir] = cells[1]
	}
	dirs, err := fs.ReadDir(files, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		cell, ok := rows[d.Name()]
		if !ok {
			t.Errorf("docs/assistant/prompts.md has no row for mcp/%s/", d.Name())
			continue
		}
		delete(rows, d.Name())
		tools, _ := fs.ReadDir(files, "mcp/"+d.Name())
		for _, f := range tools {
			if n := strings.TrimSuffix(f.Name(), ".md"); !strings.Contains(cell, "`"+n+"`") {
				t.Errorf("the mcp/%s/ row does not list `%s`", d.Name(), n)
			}
		}
		for i, part := range strings.Split(cell, "`") {
			if i%2 == 1 && !slices.Contains(ToolNames(), part) {
				t.Errorf("the mcp/%s/ row lists `%s`, which is not an MCP tool", d.Name(), part)
			}
		}
	}
	for dir := range rows {
		t.Errorf("docs/assistant/prompts.md has a row for mcp/%s/, which does not exist", dir)
	}
}

func TestUnknownToolPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown tool should panic")
		}
	}()
	Tool("no_such_tool")
}

func TestTurnContext(t *testing.T) {
	got := RenderTurnContext(TurnContext{Sections: []string{"- 现在：x"}})
	if want := "<persona-context>\n实时快照里和上一份比变了的（没列出的照旧）：\n- 现在：x\n</persona-context>"; got != want {
		t.Fatalf("got %q", got)
	}
}

// The evening review asks what meetings happened today and when tomorrow's first one is. The snapshot only carries the
// meetings still to come (snapshot.go Snapshot.Upcoming), so both have to come from reading the calendar (#4596).
func TestEveningRoutineReadsTheCalendar(t *testing.T) {
	got := RenderEveningRoutine()
	for _, want := range []string{"今天开了哪些会", "phone_calendar_events", "飞书日历", "明天最早的安排"} {
		if !strings.Contains(got, want) {
			t.Errorf("the evening routine does not say %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "已经开完的不在") {
		t.Errorf("the evening routine does not say the snapshot leaves out the meetings already over:\n%s", got)
	}
}

// At 21:30 every meeting of the day is over and the snapshot has an empty calendar: that line must not read as "no
// meetings today", which is what a review would say (#4596).
func TestSnapshotCalendarSaysFinishedMeetingsAreNotListed(t *testing.T) {
	now := time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC) // a Friday evening
	got := RenderSnapshot(Snapshot{Now: now, Zone: "UTC", Calendar: &SnapshotCalendar{}})
	if strings.Contains(got, "今天接下来没有会\n") {
		t.Errorf("the empty calendar still reads as a day without meetings:\n%s", got)
	}
	if want := "已经开完的不在这里"; !strings.Contains(got, want) {
		t.Errorf("the empty calendar does not say %q:\n%s", want, got)
	}
	// With a meeting still to come the line names it, and says the same about the ones over.
	got = RenderSnapshot(Snapshot{Now: now, Zone: "UTC", Calendar: &SnapshotCalendar{Events: []SnapshotEvent{
		{Start: now.Add(90 * time.Minute), End: now.Add(120 * time.Minute), Title: "夜间发布会"}}}})
	if !strings.Contains(got, "夜间发布会") {
		t.Errorf("a meeting still to come is left out:\n%s", got)
	}
}

// The triage's one-line work status is shown to the user as is: in their language when it isn't Chinese, and without
// the internal ids an Agent's last words carry (#2363).
func TestAgentTriageLineLanguageAndIDs(t *testing.T) {
	zh := RenderAgentTriage(AgentTriage{})
	en := RenderAgentTriage(AgentTriage{Language: "English"})
	if strings.Contains(zh, "用English写") || !strings.Contains(en, "，用English写。") {
		t.Fatalf("line language: zh has it %v, en has it %v", strings.Contains(zh, "用English写"), strings.Contains(en, "，用English写。"))
	}
	for _, s := range []string{zh, en} {
		if !strings.Contains(s, "pane id、session id、批次号") {
			t.Fatal("the line rule must keep internal ids out")
		}
	}
}

// The Progress row's paraphrase of a report follows the work line's rule (#3181): the user's language, a length,
// no subject and no internal ids.
func TestAgentReportLineLanguageAndIDs(t *testing.T) {
	zh := RenderAgentReportLine(AgentReportLine{Max: 30})
	en := RenderAgentReportLine(AgentReportLine{Language: "English", Max: 60})
	if strings.Contains(zh, "用English写") || !strings.Contains(zh, "不超过 30 个字，") || !strings.Contains(en, "用English写，不超过 60 个字符，") {
		t.Fatalf("line language and length:\n%s\n---\n%s", zh, en)
	}
	for _, s := range []string{zh, en} {
		if !strings.Contains(s, "不要写「Agent」「它」这类主语") || !strings.Contains(s, "pane id、session id、批次号") {
			t.Fatal("the line rule must keep the subject and internal ids out")
		}
	}
}
