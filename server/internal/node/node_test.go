package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

func TestParseLaunchersSimple(t *testing.T) {
	// `_claude_launchers` pads the name to 26 columns and the base URL to 40.
	out := "claude                     " + strings.Repeat(" ", 40) + "默认\n" +
		"cc_ds                      " + strings.Repeat(" ", 40) + "deepseek\n"
	got := parseLaunchers(out)
	want := []proto.Launcher{
		{Name: "claude", Model: "默认"},
		{Name: "cc_ds", Model: "deepseek"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseLaunchersWithBaseURL(t *testing.T) {
	line := "cc_kimi"
	if len(line) < 26 {
		line += strings.Repeat(" ", 26-len(line))
	}
	base := "https://example.com/anthropic/api"
	line += base + strings.Repeat(" ", 40-len(base)) + "kimi-latest"

	got := parseLaunchers(line + "\n")
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "cc_kimi" || got[0].BaseURL != base || got[0].Model != "kimi-latest" {
		t.Fatalf("unexpected launcher %+v", got[0])
	}
}

// When the base URL overflows its 40-column field the two values run together; the parser keeps them as one string
// rather than losing the line, and the name is still usable.
func TestParseLaunchersBaseURLTooLong(t *testing.T) {
	name := "cc_long" + strings.Repeat(" ", 26-len("cc_long"))
	line := name + strings.Repeat("u", 50) + " model-x"
	got := parseLaunchers(line + "\n")
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "cc_long" {
		t.Fatalf("name = %q", got[0].Name)
	}
	if !strings.HasPrefix(got[0].BaseURL, "uuuu") {
		t.Fatalf("base url = %q", got[0].BaseURL)
	}
	// Everything after column 66 is the model, including the tail of the overflowed URL.
	if got[0].Model != "uuuuuuuuuu model-x" {
		t.Fatalf("model = %q", got[0].Model)
	}
}

func TestParseLaunchersDashMeansNoURL(t *testing.T) {
	line := "claude"
	if len(line) < 26 {
		line += strings.Repeat(" ", 26-len(line))
	}
	line += "-" + strings.Repeat(" ", 39) + "sonnet"
	got := parseLaunchers(line)
	if len(got) != 1 || got[0].BaseURL != "" || got[0].Model != "sonnet" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseLaunchersSkipsJunk(t *testing.T) {
	out := "\n  \n_loading…\n123bad model\nok_1 \n"
	got := parseLaunchers(out)
	for _, l := range got {
		if l.Name != "ok_1" && !shellcmd.NameRe.MatchString(l.Name) {
			t.Fatalf("junk line accepted: %+v", l)
		}
	}
	if len(got) != 1 || got[0].Name != "ok_1" {
		t.Fatalf("got %+v", got)
	}
}

func TestTrustKeys(t *testing.T) {
	screen := func(lines ...string) string { return strings.Join(lines, "\n") }
	for _, tc := range []struct {
		name   string
		screen string
		want   []string
	}{
		{"cursor already on yes", screen(
			"Do you trust this folder?",
			"  > Yes, I trust this folder",
			"    No, exit",
		), []string{"enter"}},
		// "❯" sits on the Yes line here, so no movement is needed.
		{"numbered cursor on yes", screen(
			"Do you trust this folder?",
			"  ❯ 1. Yes, proceed",
			"    2. No, exit",
		), []string{"enter"}},
		{"cursor on no moves up", screen(
			"Do you trust this folder?",
			"   1. Yes, I trust this folder",
			"  ❯ 2. No, exit",
		), []string{"up", "enter"}},
		{"multi-step cursor", screen(
			"So, do you trust this folder?",
			"   1. Yes, I trust this folder",
			"   2. No, exit",
			"   3. Something else",
			"  ❯ 4. Another choice",
		), []string{"up", "up", "up", "enter"}},
		{"no dialog: empty screen", "", nil},
		{"no dialog: ready prompt", "⏺ Hello\n> \n  ? for shortcuts", nil},
		{"no dialog: no yes option rendered yet", "Do you trust this folder?", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustKeys(tc.screen); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("screen %q: got %#v, want %#v", tc.screen, got, tc.want)
			}
		})
	}
}

func TestInputBoxVisible(t *testing.T) {
	visible := []string{
		"⏺ done\n\n> \n  ? for shortcuts",
		"  shift+tab to cycle",
		"mode: bypass permissions",
		"esc to interrupt",
		"────\n❯ \n────\n  ⏸ manual mode on · ← for agents", // 2.1.206, default permission mode
	}
	for _, s := range visible {
		if !inputBoxVisible(s) {
			t.Fatalf("should be visible: %q", s)
		}
	}
	for _, s := range []string{"", "Compiling…", "⏺ Reading file", "Downloading"} {
		if inputBoxVisible(s) {
			t.Fatalf("should not be visible: %q", s)
		}
	}
}

// A machine whose Claude Code has never run shows the first-run theme picker with the cursor already on the first
// choice, so accepting it is one Enter (2.1.318, "Choose the text style that looks best with your terminal").
func TestThemePickerAcceptKeys(t *testing.T) {
	screen := strings.Join([]string{
		"╭──────────────────────────────────────────────╮",
		"│ Choose the text style that looks best with   │",
		"│ your terminal                                │",
		"│                                              │",
		"│ ❯ 1. Dark mode                               │",
		"│   2. Light mode                              │",
		"│   3. Dark mode (colorblind-friendly)         │",
		"│   4. Auto (match terminal)                   │",
		"╰──────────────────────────────────────────────╯",
	}, "\n")
	if got := themePickerKeys(screen); !reflect.DeepEqual(got, []string{"enter"}) {
		t.Fatalf("got %v", got)
	}

	// The cursor on a later choice walks back up to the first one.
	onLight := strings.Join([]string{
		"Choose the text style that looks best with your terminal",
		"  1. Dark mode",
		"❯ 2. Light mode",
		"  3. Auto (match terminal)",
	}, "\n")
	if got := themePickerKeys(onLight); !reflect.DeepEqual(got, []string{"up", "enter"}) {
		t.Fatalf("got %v", got)
	}
}

// Claude Code 2.1.318 on a fresh machine draws its choices without numbers, "Auto (match terminal)" first and the cursor
// on the default (screen fixture captured 2026-11-07). The numbered match never fired on it and the start timed out on
// this screen; Enter takes the highlighted choice.
func TestThemePickerUnnumberedChoices(t *testing.T) {
	picker := screenFixture(t, "claude-2.1.318-theme-picker-120.txt")
	if got := themePickerKeys(picker); !reflect.DeepEqual(got, []string{"enter"}) {
		t.Fatalf("2.1.318 picker: got %v", got)
	}
	if got := firstRunKeys(picker); !reflect.DeepEqual(got, []string{"enter"}) {
		t.Fatalf("firstRunKeys on the picker: got %v", got)
	}
	// The list is drawn but not its cursor yet; the ❯ above the question is the shell prompt that ran claude.
	noCursor := strings.Join([]string{
		"❯ claude --settings /tmp/s.json",
		"Choose the text style that looks best with your terminal",
		"To change this later, run /theme",
		"",
		"    Auto (match terminal)",
		"    Dark mode",
	}, "\n")
	if got := themePickerKeys(noCursor); got != nil {
		t.Fatalf("no cursor in the list yet: got %v", got)
	}
}

// After the style picker a fresh 2.1.318 shows its security notes, which wait only for Enter.
func TestSecurityNotesKeys(t *testing.T) {
	notes := screenFixture(t, "claude-2.1.318-security-notes-120.txt")
	if got := firstRunKeys(notes); !reflect.DeepEqual(got, []string{"enter"}) {
		t.Fatalf("security notes: got %v", got)
	}
	for _, screen := range []string{"", "⏺ done\n\n> \n  ? for shortcuts", "Press Enter to continue…", "Security notes: see below",
		screenFixture(t, "claude-2.1.318-theme-picker-120.txt")} {
		if got := securityNotesKeys(screen); got != nil {
			t.Fatalf("screen %q produced %v", screen, got)
		}
	}
}

// Anything that is not the theme picker produces no keys, so waitReady keeps polling instead of pressing Enter into
// a prompt that would send it as the first message.
func TestThemePickerNotShown(t *testing.T) {
	for _, screen := range []string{
		"",
		"⏺ done\n\n> \n  ? for shortcuts",
		"Do you trust this folder?",
		"Choose the text style…", // a truncated render, before the question completes
		"Select login method:",
	} {
		if got := themePickerKeys(screen); got != nil {
			t.Fatalf("screen %q produced %v", screen, got)
		}
	}
}

// A first-run screen the node cannot complete by itself is named at once rather than after the whole timeout.
func TestFirstRunScreenAwaitingThePerson(t *testing.T) {
	bypass := "WARNING: Claude Code running in Bypass Permissions mode\n❯ No, exit\n  Yes, I accept\nEnter to confirm · Esc to cancel"
	if got := firstRunScreen(bypass); !strings.Contains(got, "Bypass Permissions") || !strings.Contains(got, "default") {
		t.Fatalf("bypass confirmation must name the cause and the normal-mode remedy: %q", got)
	}
	if keys := firstRunKeys(bypass); keys != nil {
		t.Fatalf("bypass permission must not be accepted automatically: %v", keys)
	}
	login := strings.Join([]string{
		"Welcome to Claude Code",
		"Select login method:",
		"❯ 1. Claude account with subscription",
		"  2. Anthropic Console account",
	}, "\n")
	if got := firstRunScreen(login); !strings.Contains(got, "sign-in screen") {
		t.Fatalf("login screen: %q", got)
	}
	if got := firstRunScreen("Choose the text style that looks best with your terminal"); got != "" {
		t.Fatalf("the theme picker is handled, not reported: %q", got)
	}
	if got := firstRunScreen("⏺ done\n\n> \n  ? for shortcuts"); got != "" {
		t.Fatalf("a ready screen is not a first-run screen: %q", got)
	}
}

func TestToolSummary(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"Bash", `{"command":"ls -la","description":"list"}`, "ls -la"},
		{"Read", `{"file_path":"/tmp/a.go"}`, "/tmp/a.go"},
		{"Edit", `{"file_path":"/tmp/b.go","old_string":"x"}`, "/tmp/b.go"},
		{"Write", `{"file_path":"/tmp/c.go","content":"..."}`, "/tmp/c.go"},
		{"NotebookEdit", `{"notebook_path":"/tmp/n.ipynb"}`, "/tmp/n.ipynb"},
		{"WebFetch", `{"url":"https://example.com","prompt":"hi"}`, "https://example.com"},
		{"WebSearch", `{"query":"golang io.Pipe"}`, "golang io.Pipe"},
		{"mcp__persona__mail_send", `{"description":"send mail"}`, "send mail"},
		{"SomeTool", `{"description":"","path":"/p"}`, "/p"},
		{"SomeTool", `{"other":"value"}`, "other: value"},
		{"Bash", ``, ""},
	}
	for _, c := range cases {
		if got := toolSummary(c.name, json.RawMessage([]byte(c.input))); got != c.want {
			t.Fatalf("%s %s: got %q, want %q", c.name, c.input, got, c.want)
		}
	}
}

func TestToolSummaryTruncatesLongInput(t *testing.T) {
	long := strings.Repeat("x", 1000)
	got := toolSummary("SomeTool", json.RawMessage(`{"payload":"`+long+`"}`))
	if len([]rune(got)) > 201 {
		t.Fatalf("summary too long: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected an ellipsis, got %q", got[len(got)-10:])
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/usr/local/bin/personad": "/usr/local/bin/personad",
		"":                        "''",
		"with space":              "'with space'",
		"it's":                    `'it'\''s'`,
		"$HOME":                   "'$HOME'",
		"a;b":                     "'a;b'",
		"a-b_c.d/e":               "a-b_c.d/e",
		// a backslash outside the quotes, which fish would read as an escape inside them (#2867)
		`C:\x`:  `'C:'\\'x'`,
		`a\';#`: `'a'\\''\'';#'`,
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Fatalf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidID(t *testing.T) {
	for _, id := range []string{"0f5b2c1e-9a44-4b8f-9c31-2f4f1f4b8e11", "abcdef12", "ABCDEF0123"} {
		if !validID(id) {
			t.Fatalf("%q should be valid", id)
		}
	}
	for _, id := range []string{"", "short", "abcdefgh", "../../etc/passwd", "abc def", strings.Repeat("a", 65)} {
		if validID(id) {
			t.Fatalf("%q should be rejected", id)
		}
	}
}

func TestNewUUIDShape(t *testing.T) {
	id := newUUID()
	if !validID(id) {
		t.Fatalf("uuid %q is not a valid id", id)
	}
	if len(id) != 36 || id[14] != '4' {
		t.Fatalf("not a v4 uuid: %q", id)
	}
	if newUUID() == id {
		t.Fatal("uuids must not repeat")
	}
}

func TestSnippetCollapsesWhitespaceAndTruncates(t *testing.T) {
	if got := snippet("a\n\n  b\tc"); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("汉", 250)
	got := snippet(long)
	if len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") {
		t.Fatalf("unexpected snippet length %d", len([]rune(got)))
	}
}

// A layout change alone (panes swapped, a shell pane before an agent closed) moves only pane_index: it is pushed, and
// an index of 0 is not the same as none.
func TestListKeySeesPaneIndex(t *testing.T) {
	at := func(i int) *int { return &i }
	base := proto.Session{SessionID: "s1", Status: proto.StatusIdle, Title: "t", SlotID: "w1:t1",
		UpdatedAt: time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)}
	none, zero, one, zeroAgain := base, base, base, base
	zero.PaneIndex, one.PaneIndex, zeroAgain.PaneIndex = at(0), at(1), at(0)
	key := func(s proto.Session) string { return listKey([]proto.Session{s}) }
	if key(none) == key(zero) {
		t.Fatal("absent and 0 must differ (a shell pane before the agent closed)")
	}
	if key(zero) == key(one) {
		t.Fatal("an index-only change must be pushed (panes swapped)")
	}
	if key(zero) != key(zeroAgain) {
		t.Fatal("the same index is no change")
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("a\nb\nc\nd\n", 2); got != "c\nd" {
		t.Fatalf("got %q", got)
	}
	if got := lastLines("only", 5); got != "only" {
		t.Fatalf("got %q", got)
	}
}

func TestParseClaudeVersion(t *testing.T) {
	for out, want := range map[string]string{
		"2.1.309 (Claude Code)\n": "2.1.309",
		"[oh-my-zsh] autojump not found. Please install it first.\n2.1.303 (Claude Code)\n": "2.1.303",
		"-----\n  欢迎使用 开发机\n║ 1.2.3 is not this ║\n2.1.304 (Claude Code)":                   "2.1.304",
		"zsh: command not found: claude": "",
		"\x1b]1337;RemoteHost=me@box\x07\x1b]1337;ShellIntegrationVersion=14;shell=zsh\x072.1.283 (Claude Code)\n": "2.1.283",
		"\x1b[31mERROR\x1b[39m: gitstatus failed\n\x1b[32m2.1.300\x1b[39m (Claude Code)":                           "2.1.300",
	} {
		if got := parseClaudeVersion(out); got != want {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
}

// An agent that said a line and then only ran tools, whose results pushed that line out of the tail: the list still
// has its words (they were gone, and the task card said "No activity yet" for 7 minutes, #1583), and its step.
func TestQuickMetaKeepsWordsOutOfTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-11-04T11:44:55Z","message":{"role":"user","content":"hub 重启无感"}}`,
		`{"type":"assistant","timestamp":"2026-11-04T11:48:02Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"已建 issue 并认领"}]}}`,
	}
	step := func(i int) {
		lines = append(lines,
			fmt.Sprintf(`{"type":"assistant","timestamp":"2026-11-04T11:%02d:00Z","message":{"id":"t%d","role":"assistant","content":[{"type":"tool_use","id":"u%d","name":"Read","input":{"file_path":"/a/f%d.go"}}]}}`, 49+i/10, i, i, i),
			fmt.Sprintf(`{"type":"user","timestamp":"2026-11-04T11:%02d:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"u%d","content":"%s"}]}}`, 49+i/10, i, strings.Repeat("x", 60<<10)))
	}
	for i := range 10 { // 600 KB of tool results
		step(i)
	}
	write := func() os.FileInfo {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(path)
		return st
	}
	st := write()
	if st.Size() < quickTail+200<<10 {
		t.Fatalf("the words have to be out of the tail: %d bytes", st.Size())
	}
	m := readQuickMeta(path, st) // the first read: looks further back
	if m.lastText != "已建 issue 并认领" || m.doing != "在读 f9.go" {
		t.Fatalf("first read: last text %q, doing %q", m.lastText, m.doing)
	}
	step(10)
	if m = readQuickMetaSince(path, write(), m); m.lastText != "已建 issue 并认领" || m.doing != "在读 f10.go" {
		t.Fatalf("grown: last text %q, doing %q", m.lastText, m.doing)
	}
	// Kept from the last read, not looked for again: a read that knew no words keeps none.
	if k := readQuickMetaSince(path, st, quickMeta{size: st.Size() - 10, lastText: "上一次读到的"}); k.lastText != "上一次读到的" {
		t.Fatalf("kept %q", k.lastText)
	}
	// A last read from before the tail could have missed words since: looked for again.
	if k := readQuickMetaSince(path, st, quickMeta{size: 100, lastText: "很久以前"}); k.lastText != "已建 issue 并认领" {
		t.Fatalf("stale last read: %q", k.lastText)
	}
	lines = append(lines, `{"type":"assistant","timestamp":"2026-11-04T12:10:00Z","message":{"id":"m9","role":"assistant","content":[{"type":"text","text":"服务端改完了"}]}}`)
	if m = readQuickMetaSince(path, write(), m); m.lastText != "服务端改完了" || m.doing != "" {
		t.Fatalf("new words: %q, doing %q", m.lastText, m.doing)
	}
}

// An agent with background subagents: their transcripts are files of their own, so the main agent's words and step
// are its own, however much its subagents say.
func TestQuickMetaWithSubagentsIsTheMainAgents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-11-04T11:44:55Z","message":{"role":"user","content":"调查重连"}}`,
		`{"type":"assistant","timestamp":"2026-11-04T11:46:00Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"两个子任务在调查"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T11:47:00Z","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"a1","name":"Agent","input":{"description":"Client reconnect survey","prompt":"…","run_in_background":true}}]}}`,
		`{"type":"user","timestamp":"2026-11-04T11:47:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"a1","content":"Async agent launched"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T11:48:00Z","message":{"id":"m3","role":"assistant","content":[{"type":"tool_use","id":"g1","name":"Grep","input":{"pattern":"Shutdown"}}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "s", "subagents")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	subLine := `{"type":"assistant","isSidechain":true,"timestamp":"2026-11-04T11:49:00Z","message":{"id":"x1","role":"assistant","content":[{"type":"text","text":"子任务的话"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(sub, "agent-a1.jsonl"), []byte(subLine), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if m := readQuickMeta(path, st); m.lastText != "两个子任务在调查" || m.doing != "在搜 Shutdown" {
		t.Fatalf("last text %q, doing %q", m.lastText, m.doing)
	}
}

// The list's activity is the latest tool call no text came after, read off the transcript's tail.
func TestQuickMetaActivityWhileOnlyToolsRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	lines := []string{
		`{"type":"user","timestamp":"2026-11-04T01:19:57Z","message":{"role":"user","content":"修一下登录"}}`,
		`{"type":"assistant","timestamp":"2026-11-04T01:20:00Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"先看代码"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T01:20:01Z","message":{"id":"m2","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/a/tasks.go"}}]}}`,
		`{"type":"user","timestamp":"2026-11-04T01:20:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"` + strings.Repeat("x", 20<<10) + `"}]}}`,
		`{"type":"assistant","timestamp":"2026-11-04T01:20:13Z","message":{"id":"m3","role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	}
	write := func() os.FileInfo {
		t.Helper()
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(path)
		return st
	}
	m := readQuickMeta(path, write())
	if m.lastText != "先看代码" || m.doing != "在跑测试" || !m.doingAt.Equal(time.Date(2026, 11, 4, 1, 20, 13, 0, time.UTC)) {
		t.Fatalf("doing %q at %v, last text %q", m.doing, m.doingAt, m.lastText)
	}
	doing, at := m.activityOf()
	if doing != "在跑测试" || at == nil || !at.Equal(m.doingAt) {
		t.Fatalf("activityOf %q %v", doing, at)
	}
	lines = append(lines, `{"type":"assistant","timestamp":"2026-11-04T01:24:05Z","message":{"id":"m4","role":"assistant","content":[{"type":"text","text":"测试过了"}]}}`)
	m = readQuickMeta(path, write())
	if doing, at := m.activityOf(); doing != "" || at != nil || m.lastText != "测试过了" {
		t.Fatalf("words come first again: %q %v %q", doing, at, m.lastText)
	}
}

// The summary is a push's body, shown on the lock screen: an argument named like a credential never shows in it,
// however deep it sits (#4734).
func TestToolSummaryHidesCredentials(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`{"api_key":"fake-key-123","repo":"demo"}`, "repo: demo"},
		{`{"password":"fake-pass","user":"bob"}`, "user: bob"},
		{`{"API_KEY":"fake-key-123","Repo":"demo"}`, "Repo: demo"},
		{`{"zeta":"z","alpha":"a"}`, "zeta: z"},
		{`{"token":"fake-token","count":3}`, `{"count":3,"token":"***"}`},
		{`{"config":{"Authorization":"Bearer fake","region":"cn"},"n":1}`, `{"config":{"Authorization":"***","region":"cn"},"n":1}`},
		{`{"items":[{"secret":"fake"},{"name":1}],"n":1}`, `{"items":[{"secret":"***"},{"name":1}],"n":1}`},
		{`{"cookie":"fake-cookie"}`, `{"cookie":"***"}`},
		{`{}`, "mcp__x__call"},
	}
	for _, c := range cases {
		got := toolSummary("mcp__x__call", json.RawMessage(c.input))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.input, got, c.want)
		}
		if strings.Contains(got, "fake") {
			t.Errorf("%s: a credential shows: %q", c.input, got)
		}
	}
}
