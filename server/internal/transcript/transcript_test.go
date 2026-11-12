package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// parse runs one line through ParseLine and returns the items.
func parse(t *testing.T, line string) ([]proto.Item, *Meta) {
	t.Helper()
	meta := &Meta{}
	return ParseLine(1, []byte(line), meta), meta
}

func only(t *testing.T, items []proto.Item) proto.Item {
	t.Helper()
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d: %+v", len(items), items)
	}
	return items[0]
}

func TestUserStringContent(t *testing.T) {
	items, meta := parse(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-24T10:00:00Z","cwd":"/tmp/p","message":{"role":"user","content":"hello there"}}`)
	it := only(t, items)
	if it.Role != "user" || it.Kind != proto.KindText || it.Text != "hello there" {
		t.Fatalf("unexpected item %+v", it)
	}
	if it.ID != "u1#0" || it.Seq != 1 {
		t.Fatalf("unexpected identity %+v", it)
	}
	if it.TS.IsZero() {
		t.Fatal("timestamp was not copied")
	}
	if meta.Cwd != "/tmp/p" {
		t.Fatalf("meta cwd = %q", meta.Cwd)
	}
}

func TestUserBlocksToolUseAndResultPairing(t *testing.T) {
	args, _ := parse(t, `{"type":"assistant","uuid":"a1","message":{"role":"assistant","model":"claude-test","content":[
		{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls -la"}}]}}`)
	use := only(t, args)
	if use.Kind != proto.KindToolUse || use.ToolUseID != "toolu_1" || use.ToolName != "Bash" {
		t.Fatalf("unexpected tool_use %+v", use)
	}
	var input map[string]string
	if err := json.Unmarshal(use.ToolInput, &input); err != nil || input["command"] != "ls -la" {
		t.Fatalf("tool input = %s (%v)", use.ToolInput, err)
	}

	result, _ := parse(t, `{"type":"user","uuid":"u2","message":{"role":"user","content":[
		{"type":"text","text":"here you go"},
		{"type":"tool_result","tool_use_id":"toolu_1","content":[
			{"type":"text","text":"total 0"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]}]}}`)
	if len(result) != 3 {
		t.Fatalf("want text + tool_result + image, got %+v", result)
	}
	if result[0].Kind != proto.KindText || result[0].Text != "here you go" {
		t.Fatalf("unexpected first block %+v", result[0])
	}
	tr := result[1]
	if tr.Kind != proto.KindToolResult || tr.ToolUseID != "toolu_1" || tr.Text != "total 0" {
		t.Fatalf("unexpected tool_result %+v", tr)
	}
	img := result[2]
	if img.Kind != proto.KindImage || img.ToolUseID != "toolu_1" || img.Image == nil || img.Image.Mime != "image/png" {
		t.Fatalf("unexpected image %+v", img)
	}
}

func TestToolResultErrorFlagAndPlainString(t *testing.T) {
	items, _ := parse(t, `{"type":"user","uuid":"u3","message":{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"permission denied"}]}}`)
	it := only(t, items)
	if !it.IsError || it.Text != "permission denied" {
		t.Fatalf("unexpected result %+v", it)
	}
}

func TestAssistantThinkingTextAndModel(t *testing.T) {
	items, meta := parse(t, `{"type":"assistant","uuid":"a2","message":{"role":"assistant","model":"claude-sonnet","content":[
		{"type":"thinking","thinking":"let me think"},
		{"type":"text","text":"  "},
		{"type":"text","text":"all done"}]}}`)
	if len(items) != 2 {
		t.Fatalf("blank text blocks must be dropped, got %+v", items)
	}
	if items[0].Kind != proto.KindThinking || items[0].Text != "let me think" {
		t.Fatalf("unexpected thinking %+v", items[0])
	}
	if items[1].Kind != proto.KindText || items[1].Text != "all done" {
		t.Fatalf("unexpected text %+v", items[1])
	}
	if meta.Model != "claude-sonnet" || meta.LastText != "all done" {
		t.Fatalf("meta model=%q last=%q", meta.Model, meta.LastText)
	}

	_, synthetic := parse(t, `{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"x"}]}}`)
	if synthetic.Model != "" {
		t.Fatalf("synthetic model must be ignored, got %q", synthetic.Model)
	}
}

func TestCommandMessage(t *testing.T) {
	items, _ := parse(t, `{"type":"user","uuid":"u4","message":{"role":"user","content":"<command-name>/clear</command-name>\n            <command-message>clear</command-message>\n            <command-args></command-args>"}}`)
	it := only(t, items)
	if it.Kind != proto.KindText || it.Text != "/clear" {
		t.Fatalf("unexpected item %+v", it)
	}

	items, _ = parse(t, `{"type":"user","uuid":"u5","message":{"role":"user","content":"<command-name>/model</command-name><command-args>opus</command-args>"}}`)
	if it := only(t, items); it.Text != "/model opus" {
		t.Fatalf("command args were dropped: %q", it.Text)
	}
}

func TestLocalCommandOutputBecomesNotice(t *testing.T) {
	items, _ := parse(t, `{"type":"user","uuid":"u6","message":{"role":"user","content":"<local-command-stdout>Total cost: $0.04</local-command-stdout>"}}`)
	it := only(t, items)
	if it.Role != "system" || it.Kind != proto.KindNotice || it.Text != "Total cost: $0.04" {
		t.Fatalf("unexpected notice %+v", it)
	}
}

func TestSystemReminderIsStrippedFromUserText(t *testing.T) {
	items, _ := parse(t, `{"type":"user","uuid":"u7","message":{"role":"user","content":"real message\n<system-reminder>secret context</system-reminder>"}}`)
	it := only(t, items)
	if it.Text != "real message" {
		t.Fatalf("system-reminder leaked: %q", it.Text)
	}

	if items, _ := parse(t, `{"type":"user","uuid":"u8","message":{"role":"user","content":"<system-reminder>only a reminder</system-reminder>"}}`); len(items) != 0 {
		t.Fatalf("reminder-only message should produce nothing, got %+v", items)
	}
}

// The live snapshot Persona starts an assistant message with (node/assistant_context.go) isn't what the user wrote:
// neither as its own block nor inside the text.
func TestPersonaContextIsStripped(t *testing.T) {
	for _, tag := range []string{"persona-context"} { // the old name in history and from older nodes
		t.Run(tag, func(t *testing.T) { testSnapshotIsStripped(t, tag) })
	}
}

func testSnapshotIsStripped(t *testing.T, tag string) {
	block := `<` + tag + `>\n实时快照（这条消息发出时的；之后的变化用工具查）：\n- 现在：2026-11-01 周日 15:04（Asia/Shanghai）\n</` + tag + `>`
	items, meta := parse(t, `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"text","text":"`+block+`"},{"type":"text","text":"约周五吃饭"}]}}`)
	it := only(t, items)
	if it.Text != "约周五吃饭" || meta.FirstUser != "约周五吃饭" {
		t.Fatalf("got %q (first %q)", it.Text, meta.FirstUser)
	}
	if items, _ := parse(t, `{"type":"user","uuid":"u2","message":{"role":"user","content":"`+block+`\n\n约周五吃饭"}}`); only(t, items).Text != "约周五吃饭" {
		t.Fatalf("in the text: %+v", items)
	}
	codex := `{"timestamp":"2026-11-01T07:04:00Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u3","content":[` +
		`{"type":"text","text":"` + block + `"},{"type":"text","text":"约周五吃饭"}]}}}`
	if it := only(t, ParseLine(1, []byte(codex), &Meta{Codex: true})); it.Text != "约周五吃饭" {
		t.Fatalf("codex: %q", it.Text)
	}
}

// /compact (typed, or sent by Persona to an idle assistant conversation) is a notice, not a message: it must not count
// as activity either, or a compacted conversation would turn up unread.
func TestCompactCommandIsANotice(t *testing.T) {
	items, meta := parse(t, `{"type":"user","uuid":"u1","timestamp":"2026-11-01T07:00:00Z","message":{"role":"user","content":"<command-name>/compact</command-name>\n            <command-message>compact</command-message>\n            <command-args></command-args>"}}`)
	it := only(t, items)
	if it.Role != "system" || it.Kind != proto.KindNotice || it.Text != "压缩上下文" || !meta.LastActivity.IsZero() {
		t.Fatalf("got %+v, activity %v", it, meta.LastActivity)
	}
}

func TestMetaAndSidechainAreSkipped(t *testing.T) {
	if items, _ := parse(t, `{"type":"user","uuid":"u9","isMeta":true,"message":{"role":"user","content":"caveat: ..."}}`); len(items) != 0 {
		t.Fatalf("isMeta line should be skipped, got %+v", items)
	}
	if items, _ := parse(t, `{"type":"assistant","uuid":"a9","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"side"}]}}`); len(items) != 0 {
		t.Fatalf("sidechain line should be skipped, got %+v", items)
	}
}

func TestCompactBoundaryAndSummary(t *testing.T) {
	items, _ := parse(t, `{"type":"system","subtype":"compact_boundary","uuid":"s1"}`)
	if it := only(t, items); it.Kind != proto.KindNotice || it.Role != "system" || it.Text != "上下文已压缩" {
		t.Fatalf("unexpected boundary notice %+v", it)
	}

	if it := only(t, items); it.Compact == nil || it.Compact.Trigger != "" {
		t.Fatalf("a boundary without metadata still says it is one: %+v", it.Compact)
	}
	items, _ = parse(t, `{"type":"system","subtype":"compact_boundary","uuid":"s2","compactMetadata":{"trigger":"manual","preTokens":151467}}`)
	if it := only(t, items); it.Compact == nil || it.Compact.Trigger != "manual" || it.Compact.PreTokens != 151467 {
		t.Fatalf("the boundary's trigger and size: %+v", it.Compact)
	}

	items, _ = parse(t, `{"type":"user","uuid":"u10","isCompactSummary":true,"message":{"role":"user","content":"long summary"}}`)
	if it := only(t, items); it.Kind != proto.KindNotice || it.Text != "已从压缩的上下文继续" {
		t.Fatalf("unexpected summary notice %+v", it)
	}
}

func TestAITitleIsMetaOnly(t *testing.T) {
	items, meta := parse(t, `{"type":"ai-title","aiTitle":"修复构建"}`)
	if len(items) != 0 {
		t.Fatalf("ai-title should not produce items, got %+v", items)
	}
	if meta.Title != "修复构建" {
		t.Fatalf("title = %q", meta.Title)
	}
}

func TestInterruptedNotice(t *testing.T) {
	items, _ := parse(t, `{"type":"user","uuid":"u11","message":{"role":"user","content":"[Request interrupted by user]"}}`)
	if it := only(t, items); it.Kind != proto.KindNotice || it.Text != "已中断" {
		t.Fatalf("unexpected interrupt notice %+v", it)
	}
}

func TestTaskNotificationBecomesNotice(t *testing.T) {
	items, _ := parse(t, `{"type":"user","uuid":"u12","message":{"role":"user","content":"<task-notification><summary>后台完成</summary></task-notification>"}}`)
	// A user message of its own: the agent was idle and the notice woke it.
	if it := only(t, items); it.Kind != proto.KindNotice || it.Text != "后台完成" || !it.Wake {
		t.Fatalf("unexpected notice %+v", it)
	}
}

func TestGarbageLineIsIgnored(t *testing.T) {
	items, meta := parse(t, `not json at all`)
	if len(items) != 0 {
		t.Fatalf("garbage should produce no items, got %+v", items)
	}
	if !meta.Updated.IsZero() {
		t.Fatalf("garbage must not update meta, got %v", meta.Updated)
	}
}

// writeFile writes lines joined by newlines plus the given tail (usually "" or a partial line).
func writeFile(t *testing.T, dir string, lines []string, tail string) string {
	t.Helper()
	path := filepath.Join(dir, "session.jsonl")
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	body += tail
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRefreshIncrementalWithPartialLine(t *testing.T) {
	dir := t.TempDir()
	line1 := `{"type":"user","uuid":"u1","message":{"role":"user","content":"one"}}`
	line2 := `{"type":"user","uuid":"u2","message":{"role":"user","content":"two"}}`
	line3 := `{"type":"user","uuid":"u3","message":{"role":"user","content":"three"}}`
	line4 := `{"type":"user","uuid":"u4","message":{"role":"user","content":"four"}}`
	path := writeFile(t, dir, []string{line1, line2, line3}, line4[:20])

	f := NewFile(path)
	fresh, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 3 {
		t.Fatalf("want 3 complete lines, got %d: %+v", len(fresh), fresh)
	}
	if f.LastSeq() != 3 {
		t.Fatalf("LastSeq = %d", f.LastSeq())
	}

	// A second refresh without changes returns nothing.
	if again, err := f.Refresh(); err != nil || len(again) != 0 {
		t.Fatalf("idle refresh returned %+v, %v", again, err)
	}

	// The rest of the partial line arrives.
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(line4[20:] + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err = f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].Text != "four" || fresh[0].Seq != 4 {
		t.Fatalf("want the fourth line, got %+v", fresh)
	}
	if f.LastSeq() != 4 {
		t.Fatalf("LastSeq = %d", f.LastSeq())
	}
}

// A file that grows in pieces cut anywhere (mid-line, mid-rune) reads to the same items as reading it whole: each
// Refresh picks up exactly the bytes after the last one, the partial line carried over.
func TestRefreshInChunksMatchesWholeRead(t *testing.T) {
	var body strings.Builder
	for i := range 40 {
		fmt.Fprintf(&body, `{"type":"user","uuid":"u%d","message":{"role":"user","content":"第 %d 行 %s"}}`+"\n", i, i, strings.Repeat("x", i*37))
		fmt.Fprintf(&body, `{"type":"assistant","uuid":"a%d","message":{"id":"m%d","role":"assistant","content":[{"type":"text","text":"回答 %d"}]}}`+"\n", i, i, i)
	}
	all := []byte(body.String())
	dir := t.TempDir()
	whole := writeFile(t, dir, nil, string(all))
	want, err := NewFile(whole).Refresh()
	if err != nil || len(want) != 80 {
		t.Fatalf("whole read: %d items, %v", len(want), err)
	}

	path := filepath.Join(dir, "growing.jsonl")
	f := NewFile(path)
	var got []proto.Item
	for at, step := 0, 1; at < len(all); at, step = at+step, step*3%251+1 {
		end := min(at+step, len(all))
		fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fh.Write(all[at:end]); err != nil {
			t.Fatal(err)
		}
		if err := fh.Close(); err != nil {
			t.Fatal(err)
		}
		fresh, err := f.Refresh()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, fresh...)
	}
	if len(got) != len(want) {
		t.Fatalf("chunked read: %d items, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Seq != want[i].Seq || got[i].Kind != want[i].Kind || got[i].Text != want[i].Text {
			t.Fatalf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if again, err := f.Refresh(); err != nil || len(again) != 0 {
		t.Fatalf("idle refresh returned %+v, %v", again, err)
	}
}

func TestRefreshRestartsWhenFileShrinks(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"one"}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"two"}}`,
	}, "")
	f := NewFile(path)
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, []string{`{"type":"user","uuid":"u9","message":{"role":"user","content":"fresh"}}`}, "")
	fresh, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].Text != "fresh" || fresh[0].Seq != 1 {
		t.Fatalf("want a fresh parse, got %+v", fresh)
	}
}

func TestRefreshMissingFile(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "nope.jsonl"))
	if _, err := f.Refresh(); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

// pageFixture builds six lines; the last one yields two items (text + tool_use).
func pageFixture(t *testing.T) *File {
	t.Helper()
	lines := []string{
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"one"}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"two"}}`,
		`{"type":"user","uuid":"u3","message":{"role":"user","content":"three"}}`,
		`{"type":"user","uuid":"u4","message":{"role":"user","content":"four"}}`,
		`{"type":"user","uuid":"u5","message":{"role":"user","content":"five"}}`,
		`{"type":"assistant","uuid":"a6","message":{"role":"assistant","content":[{"type":"text","text":"six"},{"type":"tool_use","id":"t6","name":"Read","input":{"file_path":"/x"}}]}}`,
	}
	f := NewFile(writeFile(t, t.TempDir(), lines, ""))
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestPageFromTheEnd(t *testing.T) {
	f := pageFixture(t)
	items, more, _ := f.Page(0, 2)
	if !more {
		t.Fatal("has_more should be true when older items exist")
	}
	if len(items) != 2 || items[0].Seq != 6 || items[1].Seq != 6 {
		t.Fatalf("a page must not split a line: %+v", items)
	}
	if items[0].Kind != proto.KindText || items[1].Kind != proto.KindToolUse {
		t.Fatalf("unexpected kinds %+v", items)
	}
}

func TestPageBeforeSeq(t *testing.T) {
	f := pageFixture(t)
	items, more, _ := f.Page(4, 2)
	if len(items) != 2 || items[0].Seq != 2 || items[1].Seq != 3 {
		t.Fatalf("before_seq is exclusive: %+v", items)
	}
	if !more {
		t.Fatal("seq 1 is still older")
	}

	items, more, _ = f.Page(2, 2)
	if len(items) != 1 || items[0].Seq != 1 || items[0].Text != "one" {
		t.Fatalf("unexpected page %+v", items)
	}
	if more {
		t.Fatal("nothing older than seq 1")
	}
}

func TestAfterSeq(t *testing.T) {
	f := pageFixture(t)
	items, more, _ := f.After(5, 10)
	if len(items) != 2 || more {
		t.Fatalf("want both items of line 6 and nothing more, got %+v more %v", items, more)
	}
	if items[0].Seq != 6 || items[1].Seq != 6 {
		t.Fatalf("unexpected seqs %+v", items)
	}
	if got, more, _ := f.After(0, 3); len(got) != 3 || got[2].Seq != 3 || !more {
		t.Fatalf("limit not honoured: %+v more %v", got, more)
	}
	if got, more, _ := f.After(6, 10); len(got) != 0 || more {
		t.Fatalf("nothing after the last line, got %+v", got)
	}
	// The limit falls inside line 6 (two items): the page takes the whole line.
	if got, _, _ := f.After(4, 2); len(got) != 3 || got[2].Seq != 6 {
		t.Fatalf("a line was split: %+v", got)
	}
}

func TestClipKeepsUTF8Intact(t *testing.T) {
	long := strings.Repeat("汉", maxTextLen)
	got := clip(long)
	if len(got) <= maxTextLen {
		t.Fatalf("clip should have truncated, len=%d", len(got))
	}
	if strings.ContainsRune(got, '�') {
		t.Fatal("clip cut a rune in half")
	}
	if clip("short") != "short" {
		t.Fatal("short text must pass through")
	}
}

func TestImageTooLargeIsDropped(t *testing.T) {
	data := strings.Repeat("A", maxImageLen+4)
	line := `{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + data + `"}}]}}`
	items, _ := parse(t, line)
	if len(items) != 0 {
		t.Fatalf("oversized image should be dropped, got %d items", len(items))
	}
}

func TestClipJSONKeepsRunesIntact(t *testing.T) {
	// A leading ASCII byte shifts the cut point into the middle of a rune.
	body := "a" + strings.Repeat("汉", maxInputLen)
	raw := json.RawMessage(`{"content":"` + body + `"}`)
	var out map[string]string
	if err := json.Unmarshal(clipJSON(raw), &out); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out["content"], '�') {
		t.Fatal("clipJSON cut a rune in half")
	}
	// clipTo appends a "\n…(truncated, N bytes in all)" suffix (in Chinese), so allow a little slack over the limit.
	if len(out["content"]) > maxInputLen/2+64 {
		t.Fatalf("content was not shortened: %d bytes", len(out["content"]))
	}
}

func TestRecapDropsConfigHint(t *testing.T) {
	var m Meta
	items := ParseLine(1, []byte(`{"type":"system","subtype":"away_summary","uuid":"u","content":"目标是 X。下一步 Y。 (disable recaps in /config)"}`), &m)
	if len(items) != 1 || items[0].Kind != proto.KindRecap || items[0].Text != "目标是 X。下一步 Y。" {
		t.Fatalf("items = %+v", items)
	}
}

func TestFirstUserFromContentBlocks(t *testing.T) {
	var m Meta
	ParseLine(1, []byte(`{"type":"user","uuid":"u","message":{"role":"user","content":[{"type":"text","text":"服务器负载"}]}}`), &m)
	if m.FirstUser != "服务器负载" {
		t.Fatalf("first user = %q", m.FirstUser)
	}
}

func TestQueuedInputShowsAsUserText(t *testing.T) {
	var m Meta
	items := ParseLine(1, []byte(`{"type":"attachment","uuid":"a","attachment":{"type":"queued_command","prompt":"顺便修一下这个","commandMode":"prompt","origin":{"kind":"human"}}}`), &m)
	if len(items) != 1 || items[0].Role != "user" || items[0].Kind != proto.KindText || items[0].Text != "顺便修一下这个" || !items[0].Queued {
		t.Fatalf("items = %+v", items)
	}
	// Notifications the harness queues are not user input.
	if got := ParseLine(2, []byte(`{"type":"attachment","uuid":"b","attachment":{"type":"queued_command","prompt":"x","commandMode":"task-notification"}}`), &m); len(got) != 0 {
		t.Fatalf("task notification shown: %+v", got)
	}
	if got := ParseLine(3, []byte(`{"type":"attachment","uuid":"c","attachment":{"type":"environment"}}`), &m); len(got) != 0 {
		t.Fatalf("other attachment shown: %+v", got)
	}
}

func TestLastActivityIgnoresSystemLines(t *testing.T) {
	var m Meta
	ParseLine(1, []byte(`{"type":"assistant","uuid":"a","timestamp":"2026-10-20T10:00:00Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`), &m)
	ParseLine(2, []byte(`{"type":"system","subtype":"away_summary","uuid":"s","timestamp":"2026-10-24T10:00:00Z","content":"recap"}`), &m)
	ParseLine(3, []byte(`{"type":"ai-title","aiTitle":"t","timestamp":"2026-10-24T11:00:00Z"}`), &m)
	if got := m.LastActivity.Format("2006-01-02"); got != "2026-10-20" {
		t.Fatalf("last activity = %s", got)
	}
}

func TestPastedContentTagsAreStripped(t *testing.T) {
	var m Meta
	items := ParseLine(1, []byte(`{"type":"user","uuid":"u","message":{"role":"user","content":"\n\n<pasted_content id=\"ae9b\">\n我用语音输入的一段话\n</pasted_content id=\"ae9b\">"}}`), &m)
	if len(items) != 1 || items[0].Text != "我用语音输入的一段话" {
		t.Fatalf("items = %+v", items)
	}
	nested := ParseLine(2, []byte(`{"type":"user","uuid":"v","message":{"role":"user","content":"a <\\pasted_content id=\"x\">b<\\/pasted_content id=\"x\">"}}`), &m)
	if len(nested) != 1 || nested[0].Text != "a b" {
		t.Fatalf("nested = %+v", nested)
	}
}

func TestQueueOperations(t *testing.T) {
	var m Meta
	for i, l := range []string{
		`{"type":"queue-operation","operation":"enqueue","content":"A"}`,
		`{"type":"queue-operation","operation":"enqueue","content":"B"}`,
		`{"type":"queue-operation","operation":"enqueue","content":"C"}`,
		`{"type":"queue-operation","operation":"dequeue"}`,
		`{"type":"queue-operation","operation":"remove","content":"C","reason":"absorbed_mid_turn"}`,
	} {
		ParseLine(i+1, []byte(l), &m)
	}
	if len(m.Queue) != 1 || m.Queue[0] != "B" {
		t.Fatalf("queue = %v", m.Queue)
	}
	ParseLine(9, []byte(`{"type":"queue-operation","operation":"popAll","content":"B"}`), &m)
	if len(m.Queue) != 0 {
		t.Fatalf("queue after popAll = %v", m.Queue)
	}
}

func TestDeveloperInfoAndQueueTags(t *testing.T) {
	var m Meta
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-10-24T10:00:00Z","gitBranch":"main","version":"2.1.310","message":{"role":"user","content":"hi"}}`,
		`{"type":"permission-mode","permissionMode":"bypassPermissions"}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-24T10:00:05Z","message":{"id":"m1","role":"assistant","content":[{"type":"thinking","thinking":"x"}],"usage":{"input_tokens":2,"cache_read_input_tokens":1000,"cache_creation_input_tokens":10,"output_tokens":50}}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-24T10:00:06Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"cache_read_input_tokens":1000,"cache_creation_input_tokens":10,"output_tokens":50}}}`,
		`{"type":"system","subtype":"compact_boundary","uuid":"c"}`,
		`{"type":"queue-operation","operation":"enqueue","content":"\n<pasted_content id=\"ab\">\n语音输入\n</pasted_content id=\"ab\">"}`,
	}
	for i, l := range lines {
		ParseLine(i+1, []byte(l), &m)
	}
	if m.GitBranch != "main" || m.Version != "2.1.310" || m.PermissionMode != "bypassPermissions" ||
		m.ContextTokens != 1012 || m.OutputTokens != 50 || m.Compactions != 1 || m.Turns != 1 || m.StartedAt.IsZero() {
		t.Fatalf("meta = %+v", m)
	}
	if len(m.Queue) != 1 || m.Queue[0] != "语音输入" {
		t.Fatalf("queue = %q", m.Queue)
	}
}

func TestBackgroundTasks(t *testing.T) {
	meta := &Meta{}
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-25T07:40:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"./scripts/release/release.sh","description":"Upload to TestFlight","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-10-25T07:40:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command running in background with ID: b85polnoj. Output is being written to: /tmp/tasks/b85polnoj.output. You will be notified when it completes"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-10-25T07:40:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Agent","input":{"description":"Survey code","prompt":"x"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-25T07:40:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":"Async agent launched successfully.\nagentId: a3497dfa0fb026376 (internal ID)"}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-10-25T07:40:04Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu3","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-10-25T07:40:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu3","content":"a b"}]}}`,
	}
	for i, l := range lines {
		ParseLine(i+1, []byte(l), meta)
	}
	if len(meta.Background) != 2 {
		t.Fatalf("want 2 running, got %+v", meta.Background)
	}
	sh := meta.Background[0]
	if sh.ID != "b85polnoj" || sh.Kind != "shell" || sh.Command != "./scripts/release/release.sh" || sh.OutputFile != "/tmp/tasks/b85polnoj.output" {
		t.Fatalf("shell task: %+v", sh)
	}
	if meta.Background[1].ID != "a3497dfa0fb026376" || meta.Background[1].Kind != "agent" {
		t.Fatalf("agent task: %+v", meta.Background[1])
	}

	// Finishing: queued as a notification (never shows in the input queue), absorbed as an attachment → a notice.
	note := "<task-notification>\n<task-id>b85polnoj</task-id>\n<status>completed</status>\n<summary>Background command \"Upload\" completed (exit code 0)</summary>\n</task-notification>"
	q, _ := json.Marshal(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": note})
	ParseLine(7, q, meta)
	if len(meta.Queue) != 0 || len(meta.Background) != 1 {
		t.Fatalf("queue %v background %+v", meta.Queue, meta.Background)
	}
	att, _ := json.Marshal(map[string]any{"type": "attachment", "uuid": "x", "attachment": map[string]any{
		"type": "queued_command", "prompt": "<task-notification>\n<task-id>a3497dfa0fb026376</task-id>\n<summary>Agent done</summary>\n</task-notification>",
		"commandMode": "task-notification", "origin": map[string]any{"kind": "task-notification"}}})
	it := only(t, ParseLine(8, att, meta))
	if it.Kind != proto.KindNotice || it.Text != "Agent done" || it.Queued || it.Wake { // absorbed mid-turn: no wake
		t.Fatalf("notice: %+v", it)
	}
	if len(meta.Background) != 0 {
		t.Fatalf("still running: %+v", meta.Background)
	}
}

// A foreground command moved to the background by hand (Ctrl+B, or a message sent mid-turn, #3278) runs on as a
// background task until its notification, as one started in the background does; a foreground command that just ends
// leaves nothing behind.
func TestManuallyBackgroundedCommand(t *testing.T) {
	meta := &Meta{}
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-10T08:55:18Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"python3 slow.py","description":"Run a slow test"}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-10T08:55:24Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Command was manually backgrounded by user with ID: bmk9rnfdz. Output is being written to: /tmp/tasks/bmk9rnfdz.output. If it is still running after 30m in the background, it will be stopped and you will be notified."}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-10T08:55:25Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Bash","input":{"command":"date","description":"Get the time"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-11-10T08:55:26Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":"Tue Nov 10 16:55:26 CST 2026"}]}}`,
	}
	for i, l := range lines {
		ParseLine(i+1, []byte(l), meta)
	}
	if len(meta.Background) != 1 || len(meta.fgBash) != 0 {
		t.Fatalf("background %+v, foreground left %v", meta.Background, meta.fgBash)
	}
	if b := meta.Background[0]; b.ID != "bmk9rnfdz" || b.Kind != "shell" || b.Command != "python3 slow.py" || b.Description != "Run a slow test" ||
		b.OutputFile != "/tmp/tasks/bmk9rnfdz.output" {
		t.Fatalf("task: %+v", b)
	}
	note := "<task-notification>\n<task-id>bmk9rnfdz</task-id>\n<status>completed</status>\n<summary>Background command \"Run a slow test\" completed (exit code 0)</summary>\n</task-notification>"
	q, _ := json.Marshal(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": note})
	ParseLine(5, q, meta)
	if len(meta.Background) != 0 {
		t.Fatalf("still running after its notification: %+v", meta.Background)
	}
}

func TestPageTurnsStartsAtAPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	var b strings.Builder
	for turn := 1; turn <= 3; turn++ {
		fmt.Fprintf(&b, `{"type":"user","uuid":"u%d","message":{"role":"user","content":"question %d"}}`+"\n", turn, turn)
		for k := 0; k < 5; k++ {
			fmt.Fprintf(&b, `{"type":"assistant","uuid":"a%d%d","message":{"role":"assistant","content":[{"type":"text","text":"step %d"}]}}`+"\n", turn, k, k)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	items, more, _ := f.PageTurns(0, 2, 2000)
	if !more || len(items) != 12 || items[0].Text != "question 2" {
		t.Fatalf("last two turns: more=%v n=%d first=%q", more, len(items), items[0].Text)
	}
	older, more, _ := f.PageTurns(items[0].Seq, 2, 2000)
	if more || len(older) != 6 || older[0].Text != "question 1" {
		t.Fatalf("older page: more=%v n=%d", more, len(older))
	}
}

func TestLoopFromScheduleWakeup(t *testing.T) {
	meta := &Meta{}
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-25T03:26:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"w1","name":"ScheduleWakeup","input":{"delaySeconds":1800,"prompt":"/loop 监控 stg","reason":"30 分钟兜底"}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-10-25T03:26:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"w1","content":"Next wakeup scheduled for 11:57:00 (in 1859s). If you owe the user a status update"}]}}`,
	}
	for i, l := range lines {
		ParseLine(i+1, []byte(l), meta)
	}
	l := meta.Loop
	if l == nil || l.Kind != "wakeup" || l.Prompt != "/loop 监控 stg" || l.IntervalSeconds != 1800 || l.Ticks != 1 {
		t.Fatalf("loop: %+v", l)
	}
	if want := time.Date(2026, 10, 25, 3, 26, 1, 0, time.UTC).Add(1859 * time.Second); !l.NextAt.Equal(want) {
		t.Fatalf("next at %v, want %v", l.NextAt, want)
	}
	stop := `{"type":"assistant","uuid":"a2","timestamp":"2026-10-25T04:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"w2","name":"ScheduleWakeup","input":{"stop":true}}]}}`
	ParseLine(3, []byte(stop), meta)
	if meta.Loop != nil {
		t.Fatalf("stopped loop still there: %+v", meta.Loop)
	}
}

func TestCustomTitleBeatsAITitle(t *testing.T) {
	var m Meta
	ParseLine(1, []byte(`{"type":"ai-title","aiTitle":"Generated"}`), &m)
	if m.TitleManual || m.TitleSource != "auto" {
		t.Fatalf("generated title provenance: manual=%v source=%q", m.TitleManual, m.TitleSource)
	}
	ParseLine(2, []byte(`{"type":"custom-title","customTitle":"我起的名字"}`), &m)
	ParseLine(3, []byte(`{"type":"ai-title","aiTitle":"Generated again"}`), &m)
	if m.Title != "我起的名字" || !m.TitleManual || m.TitleSource != "manual" {
		t.Fatalf("title = %q, manual=%v source=%q", m.Title, m.TitleManual, m.TitleSource)
	}
}

func TestNamingMetadataMatchesFullParse(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"Fix the build"}}`,
		`{"type":"ai-title","aiTitle":"Generated"}`,
		`{"type":"custom-title","customTitle":"Claude Code"}`,
		`{"type":"ai-title","aiTitle":"Generated again"}`,
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"Checking"}],"usage":{"input_tokens":1000,"output_tokens":20}}}`,
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[],"usage":{"input_tokens":2000,"output_tokens":20}}}`,
		`{"type":"assistant","message":{"id":"m1","role":"assistant","content":[],"usage":{"input_tokens":3000,"output_tokens":25}}}`,
		`{"type":"assistant","isSidechain":true,"message":{"id":"side","role":"assistant","content":[],"usage":{"output_tokens":999}}}`,
		`{"type":"assistant","message":{"id":"m2","role":"assistant","content":[],"usage":{"input_tokens":4000,"output_tokens":10}}}`,
		`{"type":"api-request-blob","message":{"id":"fake","usage":{"output_tokens":9999}},"customTitle":"Not a rename"}`,
	}
	var full, observed Meta
	for i, line := range lines {
		ParseLine(i, []byte(line), &full)
		observed.ObserveNamingMetadata([]byte(line))
	}
	for _, m := range []*Meta{&full, &observed} {
		if m.Title != "Claude Code" || !m.TitleManual || m.TitleSource != "manual" || m.OutputTokens != 35 {
			t.Fatalf("title=%q manual=%v source=%q output=%d", m.Title, m.TitleManual, m.TitleSource, m.OutputTokens)
		}
	}
	var codex Meta
	codex.ObserveNamingMetadata([]byte(`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"output_tokens":123},"last_token_usage":{"input_tokens":9000,"output_tokens":3}}}}`))
	codex.ObserveNamingMetadata([]byte(`{"type":"event_msg","payload":{"type":"token_count","info":null},"note":{"output_tokens":999}}`))
	if codex.OutputTokens != 123 || codex.TitleSource != "" {
		t.Fatalf("Codex naming metadata: output=%d source=%q", codex.OutputTokens, codex.TitleSource)
	}
}

func TestCrossSessionMessage(t *testing.T) {
	msg := "Another Claude session sent a message:\n<cross-session-message from=\"uds:/run/x.sock\" from-name=\"ark-d1\" from-mode=\"bypass\">\n存量查完了，开始实现 B。\n</cross-session-message>\n\nThis came from another Claude session — not typed by your user."
	line, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u", "message": map[string]any{"role": "user", "content": msg}})
	it := only(t, ParseLine(1, line, &Meta{}))
	if it.From != "ark-d1" || it.Text != "存量查完了，开始实现 B。" || it.Role != "user" {
		t.Fatalf("item: %+v", it)
	}
	var m Meta
	m.applyQueue("enqueue", msg)
	if len(m.Queue) != 1 || m.Queue[0] != "【来自 ark-d1】存量查完了，开始实现 B。" {
		t.Fatalf("queue: %q", m.Queue)
	}
}

// A peer's message to an idle session (Claude Code 2.1.310 on): a hidden user line with the sender in its origin. It
// reads as the message, the sender's name on it, and opens the turn it starts, so the session shows as working; it is
// no prompt of the user's. A subagent's handback, with a peer origin too, stays hidden.
func TestPeerMessageToAnIdleSession(t *testing.T) {
	msg := "Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/x.sock\" from-name=\"ark-d1\" from-mode=\"bypass\">\n交接三个 issue。\n</cross-session-message>\n\nThis came from another Claude session — not typed by your user."
	peer := func(origin map[string]any, content any) []byte {
		line, _ := json.Marshal(map[string]any{"type": "user", "uuid": "p", "isMeta": true, "timestamp": "2026-11-06T14:54:49.365Z",
			"promptSource": "system", "turnOrigin": "peer", "origin": origin, "message": map[string]any{"role": "user", "content": content}})
		return line
	}
	m := Meta{TurnClosed: true}
	it := only(t, ParseLine(1, peer(map[string]any{"kind": "peer", "from": "uds:/tmp/x.sock", "name": "ark-d1", "body": "交接三个 issue。"}, msg), &m))
	if it.From != "ark-d1" || it.Text != "交接三个 issue。" || it.Role != "user" || it.Queued {
		t.Fatalf("item: %+v", it)
	}
	if !m.TurnOpen || m.TurnClosed || !m.LastPromptAt.IsZero() {
		t.Fatalf("turn open %v closed %v, last prompt %v", m.TurnOpen, m.TurnClosed, m.LastPromptAt)
	}
	// Text blocks, and a body without the tag: the origin says who and what.
	it = only(t, ParseLine(2, peer(map[string]any{"kind": "peer", "name": "ark-d2", "body": "B 已推。"}, []map[string]any{{"type": "text", "text": "B 已推。"}}), &Meta{}))
	if it.From != "ark-d2" || it.Text != "B 已推。" {
		t.Fatalf("blocks: %+v", it)
	}
	if items := ParseLine(3, peer(map[string]any{"kind": "peer", "from": "a1b2c3", "name": "general-purpose", "handback": true, "handbackReportText": "All tests pass."},
		"Another Claude session sent a message:\n<agent-message from=\"a1b2c3\">…</agent-message>"), &Meta{}); len(items) != 0 {
		t.Fatalf("a handback shows: %+v", items)
	}
	if items := ParseLine(4, peer(map[string]any{"kind": "human"}, "<local-command-caveat>…</local-command-caveat>"), &Meta{}); len(items) != 0 {
		t.Fatalf("another hidden line shows: %+v", items)
	}
}

// As Claude Code writes it to disk: the last turn ended, the message queued and taken, the hidden line, the reply, the
// turn's end. One item, the queue empty again, the turn opened and closed; the session's title and the wake flag are
// not the peer's message.
func TestPeerMessageInTranscriptOrder(t *testing.T) {
	msg := "Another Claude session sent a message:\n<cross-session-message from=\"uds:/tmp/x.sock\" from-name=\"ark-d1\">\nTake over issue 1.\n</cross-session-message>"
	enq, _ := json.Marshal(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": "<cross-session-message from=\"uds:/tmp/x.sock\" from-name=\"ark-d1\">\nTake over issue 1.\n</cross-session-message>"})
	peer, _ := json.Marshal(map[string]any{"type": "user", "uuid": "p1", "parentUuid": "d0", "isMeta": true, "timestamp": "2026-11-06T14:54:49.365Z",
		"origin": map[string]any{"kind": "peer", "name": "ark-d1", "body": "Take over issue 1."}, "message": map[string]any{"role": "user", "content": msg}})
	lines := []string{
		`{"type":"system","subtype":"turn_duration","uuid":"d0","durationMs":1000,"timestamp":"2026-11-06T14:50:00Z"}`,
		string(enq),
		`{"type":"queue-operation","operation":"dequeue"}`,
		string(peer),
		`{"type":"assistant","uuid":"a1","parentUuid":"p1","message":{"role":"assistant","content":[{"type":"text","text":"On it."}]},"timestamp":"2026-11-06T14:54:55Z"}`,
		`{"type":"system","subtype":"turn_duration","uuid":"d1","parentUuid":"a1","durationMs":6000,"timestamp":"2026-11-06T14:54:56Z"}`,
	}
	var m Meta
	var items []proto.Item
	for i, l := range lines {
		items = append(items, ParseLine(i, []byte(l), &m)...)
		if i == 3 && (!m.TurnOpen || m.TurnClosed) {
			t.Fatalf("after the peer's line: open %v closed %v", m.TurnOpen, m.TurnClosed)
		}
	}
	var peers []proto.Item
	for _, it := range items {
		if it.Role == "user" {
			peers = append(peers, it)
		}
	}
	if len(peers) != 1 || peers[0].From != "ark-d1" || peers[0].Text != "Take over issue 1." {
		t.Fatalf("user items: %+v", peers)
	}
	if len(m.Queue) != 0 || m.TurnOpen || !m.TurnClosed || m.FirstUser != "" || m.LastTurnWake || !m.LastPromptAt.IsZero() {
		t.Fatalf("meta: queue %q open %v closed %v first %q wake %v prompt %v", m.Queue, m.TurnOpen, m.TurnClosed, m.FirstUser, m.LastTurnWake, m.LastPromptAt)
	}
}

// The body the origin carries wins over the tagged one (they are the same in every transcript seen); a tag with
// nothing in it does not hide a message the origin has. A subagent's message to another agent in its own transcript
// (the subagent's file, Meta.Sidechain) reads as from that agent. With no name and no tag, the stand-in says another session sent it, not the user.
func TestPeerMessageBodies(t *testing.T) {
	line := func(content any, origin map[string]any, extra map[string]any) []byte {
		o := map[string]any{"type": "user", "uuid": "p", "isMeta": true, "origin": origin, "message": map[string]any{"role": "user", "content": content}}
		for k, v := range extra {
			o[k] = v
		}
		b, _ := json.Marshal(o)
		return b
	}
	empty := "<cross-session-message from=\"uds:/x\" from-name=\"ark-d1\">\n</cross-session-message>"
	if it := only(t, ParseLine(1, line(empty, map[string]any{"kind": "peer", "name": "ark-d1", "body": "B is pushed."}, nil), &Meta{})); it.Text != "B is pushed." {
		t.Fatalf("empty tag: %+v", it)
	}
	agent := "Another Claude session sent a message:\n<agent-message from=\"a1b2c3\">Found the cause.</agent-message>"
	if it := only(t, ParseLine(2, line(agent, map[string]any{"kind": "peer", "from": "a1b2c3", "name": "general-purpose", "body": "Found the cause."},
		map[string]any{"isSidechain": true}), &Meta{Sidechain: true})); it.From != "general-purpose" || it.Text != "Found the cause." {
		t.Fatalf("a subagent's message: %+v", it)
	}
	if it := only(t, ParseLine(3, line("hello", map[string]any{"kind": "peer", "body": "hello"}, nil), &Meta{})); it.From != FromSession {
		t.Fatalf("no name: %+v", it)
	}
	att, _ := json.Marshal(map[string]any{"type": "attachment", "uuid": "q", "attachment": map[string]any{
		"type": "queued_command", "commandMode": "prompt", "prompt": "plain words", "origin": map[string]any{"kind": "peer", "body": "plain words"}}})
	if it := only(t, ParseLine(4, att, &Meta{})); it.From != FromSession || !it.Queued {
		t.Fatalf("absorbed, no name: %+v", it)
	}
}

func TestPeerMessageAbsorbedMidTurn(t *testing.T) {
	att, _ := json.Marshal(map[string]any{"type": "attachment", "uuid": "p", "attachment": map[string]any{
		"type": "queued_command", "commandMode": "prompt",
		"prompt": "<cross-session-message from=\"uds:/x\" from-name=\"ark-d1\">\nB 已推。\n</cross-session-message>",
		"origin": map[string]any{"kind": "peer", "name": "ark-d1", "body": "B 已推。"}}})
	it := only(t, ParseLine(1, att, &Meta{}))
	if it.From != "ark-d1" || it.Text != "B 已推。" || !it.Queued {
		t.Fatalf("item: %+v", it)
	}
}

func TestClaudeSpecialTags(t *testing.T) {
	// Agent teams: a teammate's report reads as a message from it.
	items, _ := parse(t, `{"type":"user","uuid":"t1","message":{"role":"user","content":"<teammate-message teammate_id=\"docs\" color=\"blue\" summary=\"done\">\nReport is in /tmp/r.md\n</teammate-message>"}}`)
	if it := only(t, items); it.From != "docs" || it.Text != "Report is in /tmp/r.md" {
		t.Fatalf("teammate: %+v", it)
	}
	// "! cmd": the user's line, then its output as a Bash call carrying the command.
	meta := &Meta{}
	in := ParseLine(1, []byte(`{"type":"user","uuid":"b1","message":{"role":"user","content":"<bash-input>ls /tmp</bash-input>"}}`), meta)
	out := ParseLine(2, []byte(`{"type":"user","uuid":"b2","message":{"role":"user","content":"<bash-stdout>a\nb</bash-stdout><bash-stderr></bash-stderr>"}}`), meta)
	if it := only(t, in); it.Text != "! ls /tmp" || it.Role != "user" {
		t.Fatalf("bash input: %+v", it)
	}
	if len(out) != 2 || out[0].ToolName != "Bash" || string(out[0].ToolInput) != `{"command":"ls /tmp"}` ||
		out[1].Text != "a\nb" || out[1].IsError || out[0].ToolUseID != out[1].ToolUseID {
		t.Fatalf("bash output: %+v", out)
	}
	// The local-command caveat is context for the model only.
	items, _ = parse(t, `{"type":"user","uuid":"c1","message":{"role":"user","content":"<local-command-caveat>Caveat: generated by local commands.</local-command-caveat>"}}`)
	if len(items) != 0 {
		t.Fatalf("caveat should vanish: %+v", items)
	}
}

func TestLastTurnWake(t *testing.T) {
	meta := &Meta{}
	ParseLine(1, []byte(`{"type":"user","uuid":"u1","message":{"role":"user","content":"go"}}`), meta)
	if meta.LastTurnWake {
		t.Fatal("a message is not a wake")
	}
	ParseLine(2, []byte(`{"type":"user","uuid":"w1","message":{"role":"user","content":"<task-notification><summary>Background command \"x\" completed</summary></task-notification>"}}`), meta)
	if !meta.LastTurnWake {
		t.Fatal("the task report started the turn")
	}
	ParseLine(3, []byte(`{"type":"user","uuid":"u2","message":{"role":"user","content":"next"}}`), meta)
	if meta.LastTurnWake {
		t.Fatal("the user's next message starts a user turn again")
	}
}

func TestPermissionModeAndEffort(t *testing.T) {
	meta := &Meta{}
	for i, l := range []string{
		`{"type":"user","timestamp":"2026-10-28T16:00:00Z","permissionMode":"default","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-10-28T16:00:05Z","effort":"high","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"yo"}]}}`,
		`{"type":"permission-mode","permissionMode":"plan"}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-10-28T16:00:09Z","effort":"low","message":{"id":"m2","role":"assistant","content":[]}}`,
	} {
		ParseLine(i+1, []byte(l), meta)
	}
	if meta.PermissionMode != "plan" || meta.Effort != "high" {
		t.Fatalf("mode %q effort %q", meta.PermissionMode, meta.Effort)
	}
	if want := time.Date(2026, 10, 28, 16, 0, 5, 0, time.UTC); !meta.EffortAt.Equal(want) || !meta.ModeAt.Equal(want) {
		t.Fatalf("effortAt %v modeAt %v", meta.EffortAt, meta.ModeAt) // the mode line has no time: the previous line's
	}
}

func TestAPIErrorRetriesAndLenientErrorField(t *testing.T) {
	meta := &Meta{}
	var texts []string
	for i, l := range []string{
		`{"type":"system","subtype":"api_error","uuid":"e1","error":{"formatted":"Overloaded"},"retryAttempt":1,"maxRetries":3}`,
		`{"type":"system","subtype":"api_error","uuid":"e2","error":{"formatted":"Overloaded"},"retryAttempt":2,"maxRetries":3}`,
		`{"type":"system","subtype":"api_error","uuid":"e3","error":{"message":"Overloaded"},"retryAttempt":3,"maxRetries":3}`,
		`{"type":"system","subtype":"api_error","uuid":"e4","content":"old style"}`,
		// Some lines carry "error" as a plain string: the line must still parse.
		`{"type":"assistant","uuid":"a1","error":"rate_limit","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"limit hit"}]}}`,
	} {
		for _, it := range ParseLine(i+1, []byte(l), meta) {
			texts = append(texts, it.Text)
		}
	}
	want := []string{"API 错误：Overloaded（正在重试）", "API 错误：Overloaded（重试 3 次后放弃）", "API 错误：old style", "limit hit"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", texts, want)
	}
}

func TestLocalCommandLines(t *testing.T) {
	meta := &Meta{}
	var got []string
	for i, l := range []string{
		`{"type":"system","subtype":"local_command","uuid":"c1","content":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args>opus</command-args>"}`,
		`{"type":"system","subtype":"local_command","uuid":"c2","content":"<local-command-stdout>Set model to \u001b[1mOpus\u001b[22m</local-command-stdout>"}`,
		`{"type":"system","subtype":"local_command","uuid":"c3","content":"<local-command-stdout></local-command-stdout>"}`,
	} {
		for _, it := range ParseLine(i+1, []byte(l), meta) {
			got = append(got, it.Role+":"+it.Text)
		}
	}
	if want := "user:/model opus|system:Set model to Opus"; strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestHistoryIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	write := func(p string, lines ...string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	first := `{"type":"user","uuid":"u1","message":{"role":"user","content":"one"}}`
	write(path, first, `{"type":"user","uuid":"u2","message":{"role":"user","content":"two"}}`)
	f := NewFile(path)
	_, sp, err := f.RefreshSpan()
	if err != nil || sp.History == "" || sp.From != 0 || sp.To != 2 {
		t.Fatalf("first read: %+v %v", sp, err)
	}
	// Another reader of the same file (a restarted node) sees the same history.
	g := NewFile(path)
	if _, sp2, _ := g.RefreshSpan(); sp2.History != sp.History {
		t.Fatalf("history changed across readers: %q vs %q", sp.History, sp2.History)
	}
	// Replaced by another file (new inode): a new history, seqs from 1 again.
	other := filepath.Join(dir, "other.jsonl")
	write(other, first)
	if err := os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	_, sp3, _ := f.RefreshSpan()
	if sp3.History == sp.History || sp3.From != 0 || sp3.To != 1 {
		t.Fatalf("after replace: %+v (was %q)", sp3, sp.History)
	}
	// Truncated in place and rewritten with the same first line: the read count tells it apart.
	fh, _ := os.OpenFile(path, os.O_TRUNC|os.O_WRONLY, 0)
	fh.Close()
	_, _, _ = f.RefreshSpan()
	write(path, first)
	_, sp4, _ := f.RefreshSpan()
	if sp4.History == sp3.History {
		t.Fatalf("a truncated file kept its history %q", sp4.History)
	}
	// Nothing new: an empty span at the end.
	if fresh, sp5, _ := f.RefreshSpan(); len(fresh) != 0 || sp5.From != sp5.To || sp5.To != 1 {
		t.Fatalf("idle refresh: %d items %+v", len(fresh), sp5)
	}
}

func TestSubagentFileKeepsSidechainAfterRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-x.jsonl")
	line := `{"type":"assistant","isSidechain":true,"uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	_ = os.WriteFile(path, []byte(line+"\n"+line+"\n"), 0o600)
	f := NewSubagentFile(path)
	_, _ = f.Refresh()
	_ = os.WriteFile(path, []byte(line+"\n"), 0o600) // shorter: start over
	if items, _ := f.Refresh(); len(items) != 1 {
		t.Fatalf("a rewritten subagent transcript lost its lines: %d items", len(items))
	}
}

func TestLongToolOutputIsCutForPages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	out := strings.Repeat("第 n 行的输出\n", 2000)
	lines := []string{
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"seq"}}]}}`,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":` + strconv.Quote(out) + `}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	fresh, err := f.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	items, _, _ := f.Page(0, 10)
	for _, got := range [][]proto.Item{fresh, items} { // pushes and pages alike
		r := got[1]
		if r.FullSize != len(out) || len(r.Text) > pageOutputLen+100 || !strings.HasSuffix(r.Text, "…（已截断，点开看全文，共 "+strconv.Itoa(len(out))+" 字节）") {
			t.Fatalf("full_size %d, %d bytes, ends %q", r.FullSize, len(r.Text), r.Text[len(r.Text)-60:])
		}
		if !strings.HasSuffix(strings.TrimSuffix(r.Text, "\n…（已截断，点开看全文，共 "+strconv.Itoa(len(out))+" 字节）"), "第 n 行的输出") {
			t.Fatal("the cut should end on a whole line")
		}
	}
	full, err := f.Full("u1#0")
	if err != nil || full.Text != out || full.FullSize != 0 {
		t.Fatalf("Full gave %d bytes, full_size %d", len(full.Text), full.FullSize)
	}
	if _, err := f.Full("nope"); err == nil {
		t.Fatal("unknown id")
	}
	// The line changed under it (rewritten in place, same size): an error, not the cut copy passed off as whole.
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, bytes.Replace(data, []byte(`"uuid":"u1"`), []byte(`"uuid":"u9"`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Full("u1#0"); err == nil {
		t.Fatal("Full must fail when the line no longer holds the item")
	}
}

// A message with a picture typed while the agent worked is absorbed mid-turn as a queued_command whose prompt is
// blocks: the picture is part of the message (it used to be dropped, and the phone showed the text alone).
func TestQueuedPromptKeepsItsImages(t *testing.T) {
	line := `{"type":"attachment","uuid":"a1","attachment":{"type":"queued_command","commandMode":"prompt","prompt":[` +
		`{"type":"text","text":"[Image #1]看这个"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]}}`
	items, _ := parse(t, line)
	if len(items) != 2 || items[0].Kind != proto.KindText || !items[0].Queued ||
		items[1].Kind != proto.KindImage || !items[1].Queued || items[1].Image == nil || items[1].ID != "a1#1" {
		t.Fatalf("items = %+v", items)
	}
}

// Newer Claude Code opens an async agent's result with a warning that mentions "the agentId below" before the real
// "agentId: <id>" line: the task must take the real id, so its notification ends it (#3419).
func TestAsyncAgentIDAfterWarning(t *testing.T) {
	meta := &Meta{}
	lines := []string{
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-06T08:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Agent","input":{"description":"Survey code","prompt":"x","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-06T08:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"Async agent launched successfully. (This tool result is internal metadata — never quote or paste any part of it, including the agentId below, into a user-facing reply.)\nagentId: a0123456789abcdef (internal ID - do not mention to user. Use SendMessage with to: 'a0123456789abcdef' to continue this agent.)\nThe agent is working in the background. You will be notified automatically when it completes."}]}}`,
	}
	for i, l := range lines {
		ParseLine(i+1, []byte(l), meta)
	}
	if len(meta.Background) != 1 || meta.Background[0].ID != "a0123456789abcdef" || meta.Background[0].Kind != "agent" {
		t.Fatalf("background = %+v", meta.Background)
	}
	note := "<task-notification>\n<task-id>a0123456789abcdef</task-id>\n<status>completed</status>\n<summary>Agent \"Survey code\" completed</summary>\n</task-notification>"
	q, _ := json.Marshal(map[string]any{"type": "queue-operation", "operation": "enqueue", "content": note})
	ParseLine(3, q, meta)
	if len(meta.Background) != 0 {
		t.Fatalf("finished agent still running: %+v", meta.Background)
	}
}
