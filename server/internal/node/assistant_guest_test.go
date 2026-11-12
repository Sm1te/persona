package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/nwverse/persona/server/internal/compat"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// A guest conversation's process (#5195): Persona's MCP server alone, told it is a guest's, no built-in tools, only
// its own tools allowed, none of the extensions' servers or deny rules. An ordinary one keeps everything.
func TestGuestProcessFlags(t *testing.T) {
	n, _ := testNode(t)
	bin := writeFakeMCPBin(t, t.TempDir(), "example-agent", "Usage: example-agent mcp-server --control")
	t.Setenv("PERSONA_EXAMPLE_AGENT", bin)
	n.larkCfg, n.larkCfgAt = proto.AssistantLark{MCPServers: []proto.AssistantMCPServer{exampleMCP()}}, time.Now().Add(time.Hour)
	n.state.put("guest-1", sessionState{Launcher: "claude", Guest: []string{"lark_agent_reply"}})

	servers := func(mcpJSON string) map[string]map[string]any {
		var cfg struct {
			MCPServers map[string]map[string]any `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(mcpJSON), &cfg); err != nil {
			t.Fatal(err)
		}
		return cfg.MCPServers
	}
	flagValue := func(flags []string, name string) (string, bool) {
		i := slices.Index(flags, name)
		if i < 0 || i+1 >= len(flags) {
			return "", false
		}
		return flags[i+1], true
	}

	mcpJSON, flags := n.claudeAssistantMCPAndTools("guest-1", t.TempDir(), true)
	got := servers(mcpJSON)
	if len(got) != 2 || got["persona"] == nil || got[compat.MCPServer] == nil {
		t.Fatalf("servers = %v, want Persona's alone", got)
	}
	env, _ := got["persona"]["env"].(map[string]any)
	if env["PERSONA_GUEST"] != "1" || env["PERSONA_SESSION_ID"] != "guest-1" || env["PERSONA_ASSISTANT"] != "1" {
		t.Fatalf("persona env = %v", env)
	}
	if v, ok := flagValue(flags, "--tools"); !ok || v != "" {
		t.Fatalf("--tools = %q (%v), want the CLI's no-built-in-tools flag: %q", v, ok, flags)
	}
	if v, _ := flagValue(flags, "--allowedTools"); v != strings.Join(compat.MCPToolNames([]string{"lark_agent_reply"}), ",") {
		t.Fatalf("--allowedTools = %q, want only the guest's tool", v)
	}
	if !slices.Contains(flags, "--strict-mcp-config") {
		t.Fatalf("flags = %q", flags)
	}

	mcpJSON, flags = n.claudeAssistantMCPAndTools("ordinary-1", t.TempDir(), true)
	builtin, hasBuiltin := flagValue(flags, "--tools")
	if servers(mcpJSON)["example"] == nil || !hasBuiltin || builtin != assistantBuiltinTools || slices.Contains(flags, "--strict-mcp-config") {
		t.Fatalf("an ordinary conversation changed: %s %q", mcpJSON, flags)
	}
	if v, _ := flagValue(flags, "--allowedTools"); !slices.Contains(strings.Split(v, ","), assistantAllowedTools) {
		t.Fatalf("an ordinary conversation lost %s: %q", assistantAllowedTools, v)
	}
}

// Started as a guest: a new conversation with its tools kept in its state, never the warm spare, its process started
// with the guest's flags; a later message keeps it on what it started on whatever launcher or runtime it names.
func TestGuestConversationStarts(t *testing.T) {
	fakeClaudeHome(t, "for a in \"$@\"; do printf ' [%s]' \"$a\"; done > \"$HOME/args-$$\"\nread line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	withFakeHub(t, n)
	cwd := t.TempDir()
	if err := n.assistantWarm(cwd, "claude"); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	spare := n.spare
	n.mu.Unlock()
	if spare == nil {
		t.Fatal("no spare to leave alone")
	}
	t.Cleanup(spare.stop)

	id, err := n.assistantSend(withGuest(context.Background(), []string{"lark_agent_reply"}), "", cwd, "claude", "someone asks", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	if id == spare.id {
		t.Fatal("a guest conversation took the warm spare")
	}
	n.mu.Lock()
	still := n.spare == spare
	n.mu.Unlock()
	if !still {
		t.Fatal("the spare was handed out")
	}
	if st, _ := n.state.get(id); !slices.Equal(st.Guest, []string{"lark_agent_reply"}) || st.Cwd != cwd {
		t.Fatalf("state = %+v", st)
	}
	var line string
	files, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "args-*"))
	for _, f := range files {
		if raw, _ := os.ReadFile(f); strings.Contains(string(raw), "["+id+"]") {
			line = string(raw)
		}
	}
	for _, want := range []string{"[--tools] []", "[--strict-mcp-config]", "[--allowedTools] [" + strings.Join(compat.MCPToolNames([]string{"lark_agent_reply"}), ",") + "]"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the guest's process has no %s: %s", want, line)
		}
	}

	// A later message naming another launcher and the Agent SDK still goes to it as it is.
	if _, err := n.assistantSend(context.Background(), id, "", "codex", "more", nil, "", "", modelExecution{Runtime: proto.AssistantRuntimeAgentSDK}); err != nil {
		t.Fatalf("a later message: %v", err)
	}
	if st, _ := n.state.get(id); st.Launcher != "claude" || st.Runtime != "" || st.Guest == nil {
		t.Fatalf("state after a later message = %+v", st)
	}
}

// Only Claude Code runs a guest: another runtime cannot enforce its no-built-in-tools contract.
func TestGuestRefusesOtherRuntimes(t *testing.T) {
	fakeClaudeHome(t, "read line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	withFakeHub(t, n)
	_, err := n.assistantSend(withGuest(context.Background(), []string{"lark_agent_reply"}), "", t.TempDir(), "codex", "hi", nil, "", "")
	if !errors.Is(err, errGuestAgent) {
		t.Fatalf("err = %v, want errGuestAgent", err)
	}
	n.mu.Lock()
	procs := len(n.procs)
	n.mu.Unlock()
	if procs != 0 {
		t.Fatal("a process started")
	}
}

// A guest on the Agent SDK registers its declared tools through an in-process
// MCP server. No built-in tools or user settings are loaded.
func TestGuestAgentSDKStart(t *testing.T) {
	f := sdkTest(t)
	id, err := f.n.assistantSend(withGuest(context.Background(), []string{"larkbridge_docs"}), "", t.TempDir(), "claude", "search", nil, "", "",
		sdkExec(proto.AssistantRuntimeAgentSDK))
	if err != nil {
		t.Fatal(err)
	}
	o := f.start(t)
	if o["sessionId"] != id || o["strictMcpConfig"] != true || o["tools"] != nil || o["plugins"] != nil {
		t.Fatalf("start: %v", o)
	}
	servers, _ := o["guestMcpServers"].(map[string]any)
	server, _ := servers["persona"].(map[string]any)
	env, _ := server["env"].(map[string]any)
	if server["command"] == nil || env["PERSONA_GUEST"] != "1" || env["PERSONA_SESSION_ID"] != id {
		t.Fatalf("guest MCP: %v", o["guestMcpServers"])
	}
	if _, plain := o["mcpServers"].(map[string]any)["persona"]; plain {
		t.Fatalf("guest config must go through the SDK proxy: %v", o)
	}
	if got, _ := o["allowedTools"].([]any); len(got) != 2 || got[0] != "mcp__persona__larkbridge_docs" || got[1] != "mcp__jarvis__larkbridge_docs" {
		t.Fatalf("allowed tools: %#v", o["allowedTools"])
	}
}

// Its directory holds only its instructions; a path out of the home is refused. Tool names are plain identifiers.
func TestGuestPrepareAndTools(t *testing.T) {
	n, _ := testNode(t)
	out, err := n.assistantGuestPrepare(json.RawMessage(`{"dir":".persona/guest","claude_md":"# guest"}`))
	if err != nil {
		t.Fatal(err)
	}
	cwd := out.(map[string]any)["cwd"].(string)
	if cwd != filepath.Join(n.home, ".persona", "guest") {
		t.Fatalf("cwd = %q", cwd)
	}
	entries, _ := os.ReadDir(cwd)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("the directory holds %v", names)
	}
	for _, bad := range []string{`{"dir":"../x"}`, `{"dir":"/tmp/x"}`, `{"dir":""}`} {
		if _, err := n.assistantGuestPrepare(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	for _, c := range []struct {
		tools []string
		ok    bool
	}{
		{[]string{"lark_agent_reply"}, true},
		{nil, false},
		{[]string{"lark_agent_reply,Bash"}, false},
		{[]string{"mcp__persona__x"}, true}, // a name; the hub says which are a guest's
		{[]string{"Bash"}, false},
		{[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, false},
	} {
		if err := checkGuestTools(c.tools); (err == nil) != c.ok {
			t.Errorf("%q: err %v, want ok %v", c.tools, err, c.ok)
		}
	}
}

// The node relays its MCP server's calls with the guest's word for it: what the server says, or what the node knows of
// the conversation (a guest's process started before the server said so is one all the same).
func TestGuestRelayTellsTheHub(t *testing.T) {
	n := codexTestNode(t)
	fh := withFakeHub(t, n)
	n.state.put("guest-1", sessionState{Launcher: "claude", Guest: []string{"lark_agent_reply"}})
	relay := func(req map[string]any) {
		t.Helper()
		a, b := net.Pipe()
		go n.serveSock(context.Background(), a)
		line, _ := json.Marshal(req)
		if _, err := b.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
		if _, err := bufio.NewReader(b).ReadBytes('\n'); err != nil {
			t.Fatal(err)
		}
		_ = b.Close()
	}
	relay(map[string]any{"kind": "mcp.tools", "assistant": true, "session_id": "guest-1"})
	relay(map[string]any{"kind": "mcp.call", "assistant": true, "session_id": "guest-1", "name": "lark_agent_reply"})
	relay(map[string]any{"kind": "mcp.tools", "assistant": true, "session_id": "other", "guest": true})
	relay(map[string]any{"kind": "mcp.call", "assistant": true, "session_id": "ordinary", "name": "kb_search"})
	fh.mu.Lock()
	defer fh.mu.Unlock()
	want := []bool{true, true, true, false}
	if len(fh.mcp) != len(want) {
		t.Fatalf("relayed %v", fh.mcp)
	}
	for i, p := range fh.mcp {
		if got, _ := p["guest"].(bool); got != want[i] {
			t.Errorf("%v: guest %v, want %v", p, got, want[i])
		}
	}
}

// A meeting's room (#3862): started under the id the hub gives, never the warm spare, its tools and kind kept in its
// state; every process it runs in has a guest's flags plus only the project's settings sources and the room's own
// --settings, which excludes the CLAUDE.md files of every directory above it and turns auto-memory off. A start under
// an id the node already has is that conversation: nothing is sent again.
func TestMeetingRoomStartsUnderItsID(t *testing.T) {
	fakeClaudeHome(t, "for a in \"$@\"; do printf ' [%s]' \"$a\"; done > \"$HOME/args-$$\"\nread line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	n.dir = filepath.Join(os.Getenv("HOME"), ".persona")
	if err := os.MkdirAll(n.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	withFakeHub(t, n)
	rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
	out, err := n.assistantGuestPrepare(json.RawMessage(`{"dir":".persona/guest-meeting/` + rec + `","claude_md":"# room","kind":"meeting"}`))
	if err != nil {
		t.Fatal(err)
	}
	prep := out.(map[string]any)
	cwd, _ := prep["cwd"].(string)
	if prep["kind"] != guestMeeting || prep["start_ids"] != true || !strings.HasSuffix(cwd, filepath.Join("guest-meeting", rec)) {
		t.Fatalf("prepare = %v", prep)
	}
	for _, bad := range []string{`{"dir":".persona/guest","kind":"meeting"}`, `{"dir":".persona/guest-meeting/x","kind":"meeting"}`,
		`{"dir":".persona/guest-meeting/` + rec + `","kind":"other"}`} {
		if _, err := n.assistantGuestPrepare(json.RawMessage(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}

	room := "7d4a3c1e-1111-4222-8333-444455556666"
	tools := []string{"meeting_lines", "meeting_earlier"}
	ctx := withNewID(withGuestKind(context.Background(), tools, guestMeeting), room)
	id, err := n.assistantSend(ctx, "", cwd, "claude", "the meeting's header", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	if id != room {
		t.Fatalf("started as %s, want %s", id, room)
	}
	if st, _ := n.state.get(id); !slices.Equal(st.Guest, tools) || st.GuestKind != guestMeeting || st.Cwd != cwd {
		t.Fatalf("state = %+v", st)
	}
	// It signs in as the assistant's conversations did: never a new private profile, a config directory with no login.
	if st, _ := n.state.get(id); st.ConfigDir == "" || strings.Contains(st.ConfigDir, filepath.Join("profiles", "claude")) {
		t.Fatalf("the room runs in profile %q", st.ConfigDir)
	}
	if !n.knowsConversation(room) {
		t.Fatal("the node does not know the room it started")
	}
	var line string
	deadline := time.Now().Add(5 * time.Second)
	for line == "" && time.Now().Before(deadline) {
		files, _ := filepath.Glob(filepath.Join(os.Getenv("HOME"), "args-*"))
		for _, f := range files {
			if raw, _ := os.ReadFile(f); strings.Contains(string(raw), "["+id+"]") {
				line = string(raw)
			}
		}
		if line == "" {
			<-time.After(20 * time.Millisecond) // the process writes its arguments as it starts
		}
	}
	for _, want := range []string{"[--tools] []", "[--strict-mcp-config]", "[--setting-sources] [project]", "[--session-id] [" + room + "]",
		"[--allowedTools] [" + strings.Join(compat.MCPToolNames(tools), ",") + "]"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the room's process has no %s: %s", want, line)
		}
	}
	_, after, _ := strings.Cut(line, "[--settings] [")
	settingsPath, _, _ := strings.Cut(after, "]")
	if filepath.Base(settingsPath) != "assistant-room-"+room+".json" {
		t.Fatalf("the room's own settings are not its --settings: %s", line)
	}
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Excludes []string          `json:"claudeMdExcludes"`
		Env      map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	for _, want := range []string{filepath.Join(home, "CLAUDE.md"), filepath.Join(home, ".persona", "CLAUDE.md"),
		filepath.Join(home, ".claude", "CLAUDE.md"), filepath.Join(filepath.Dir(cwd), "CLAUDE.md")} {
		if !slices.Contains(settings.Excludes, want) {
			t.Errorf("%s is not excluded: %v", want, settings.Excludes)
		}
	}
	if slices.Contains(settings.Excludes, filepath.Join(cwd, "CLAUDE.md")) || settings.Env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" {
		t.Errorf("settings %s", raw)
	}
	if meetingRoomArgs()[0] != "--setting-sources" {
		t.Fatal("args changed")
	}
}

// assistant.send with new_session_id: an id the node has (a process, or a transcript) is that conversation, answered
// without sending anything; a state alone is not (a first start that failed left only that: its start asked again
// starts it). One that is not a session id is refused, as is a new id beside a session's.
func TestNewSessionIDStartsOnce(t *testing.T) {
	n := codexTestNode(t)
	room := "7d4a3c1e-1111-4222-8333-444455556666"
	n.state.put(room, sessionState{Launcher: "claude", Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	if n.knowsConversation(room) {
		t.Fatal("a state alone counts as the conversation: a failed first start is never started again")
	}
	transcript := filepath.Join(n.home, ".claude", "projects", "-home-me--persona-guest-meeting-r", room+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"new_session_id":"`+room+`","text":"again","guest":{"tools":["meeting_lines"],"kind":"meeting"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := out.(map[string]any); m["session_id"] != room || m["existed"] != true {
		t.Fatalf("answer %v", m)
	}
	if _, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"new_session_id":"not an id","text":"x"}`)); err == nil {
		t.Error("a bad id accepted")
	}
	if _, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"session_id":"`+room+`","new_session_id":"`+room+`","text":"x"}`)); err == nil {
		t.Error("a new id beside a session's accepted")
	}
	if _, err := n.handle(context.Background(), "assistant.send", json.RawMessage(`{"text":"x","guest":{"tools":["meeting_lines"],"kind":"other"}}`)); err == nil {
		t.Error("an unknown guest kind accepted")
	}
}

// assistant.guest_delete removes a room's transcript, what Claude Code kept beside it and its directory; it refuses a
// session that is not a meeting's room, and what is gone already is no error.
func TestGuestDeleteRemovesOnlyARoom(t *testing.T) {
	n, home := testNode(t)
	n.dir = filepath.Join(home, ".persona")
	rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
	rel := ".persona/guest-meeting/" + rec
	dir := n.assistantDirOf(rel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	room, other := "7d4a3c1e-1111-4222-8333-444455556666", "8e5b4d2f-2222-4333-8444-555566667777"
	projects := filepath.Join(home, ".claude", "projects")
	roomProject := filepath.Join(projects, claudeProjectName(dir))
	otherProject := filepath.Join(projects, "-home-me-code")
	for _, p := range []string{filepath.Join(roomProject, room+".jsonl"), filepath.Join(roomProject, room, "tool-results", "x.txt"), filepath.Join(otherProject, other+".jsonl")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n.state.put(room, sessionState{Launcher: "claude", Cwd: dir, Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	n.state.put(other, sessionState{Launcher: "claude", Cwd: "/home/me/code"})

	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + other + `"}`)); err == nil {
		t.Fatal("deleted a conversation that is not the room")
	}
	if _, err := os.Stat(filepath.Join(otherProject, other+".jsonl")); err != nil {
		t.Fatalf("the other conversation's transcript: %v", err)
	}
	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":".persona/guest","session_id":"` + room + `"}`)); err == nil {
		t.Fatal("a directory that is not a room's accepted")
	}
	out, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + room + `"}`))
	if err != nil || out.(map[string]any)["deleted"] != true {
		t.Fatalf("delete %v %v", out, err)
	}
	for _, gone := range []string{filepath.Join(roomProject, room+".jsonl"), filepath.Join(roomProject, room), dir} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + room + `"}`)); err != nil {
		t.Errorf("deleted again: %v", err)
	}
}

// A room's transcript is an assistant conversation's, also when no process runs it: its projects folder, either
// spelling of the state directory, and its state.
func TestMeetingRoomIsAnAssistantConversation(t *testing.T) {
	rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
	for _, base := range []string{"-home-me--persona-guest-meeting-" + rec, "-home-me-" + nonAlnum.ReplaceAllString(compat.StateDir, "-") + "-guest-meeting-" + rec} {
		if !isAssistantProject(base, "-home-me--persona-assistant") {
			t.Errorf("%s is not an assistant's", base)
		}
	}
	for _, base := range []string{"-home-me--persona-guest", "-home-me-code-guest-meeting-notes", "-home-me--persona-guest-meeting-x"} {
		if isAssistantProject(base, "-home-me--persona-assistant") {
			t.Errorf("%s is an assistant's", base)
		}
	}
	n, _ := testNode(t)
	room := "7d4a3c1e-1111-4222-8333-444455556666"
	n.state.put(room, sessionState{Launcher: "claude", Cwd: "/somewhere/guest-meeting/" + rec, Guest: []string{"meeting_lines"}, GuestKind: guestMeeting})
	if !n.isAssistantSession(room) {
		t.Fatal("a room with no process is not an assistant conversation")
	}
}

// A new room that names no profile signs in as the assistant's conversations did ("legacy"), never in a new private
// profile, a config directory with no login; a profile it names, a resumed room, another guest and a scoped node keep
// the usual choice.
func TestMeetingRoomStartProfile(t *testing.T) {
	n, _ := testNode(t)
	for _, c := range []struct {
		asked, kind string
		resume      bool
		want        string
	}{
		{"", guestMeeting, false, meetingRoomProfile},
		{"~/.claude-work", guestMeeting, false, "~/.claude-work"},
		{"", guestMeeting, true, ""},
		{"", "", false, ""},
	} {
		if got := n.startProfile(c.asked, c.kind, c.resume); got != c.want {
			t.Errorf("startProfile(%q, %q, %v) = %q, want %q", c.asked, c.kind, c.resume, got, c.want)
		}
	}
	n.sessionScope = &nodeSessionScope{}
	if got := n.startProfile("", guestMeeting, false); got != "" {
		t.Errorf("a scoped node's room asks for %q: the scope is its only profile", got)
	}
}

// A room's own --settings keeps of the start's settings and of its profile's settings.json only the environment,
// login and model (the start's over the profile's, env by name), and Persona's own hooks when no plugin carries them:
// an alias launcher's settings file or the profile's (user settings files) brings none of its hooks, plugins, output
// style, status line, permissions or skill switches into the room. The profile's file is the only place a gateway's
// token may be (--setting-sources project leaves it out): without it the room was not logged in.
func TestMeetingRoomSettingsKeepNothingOfTheLaunchers(t *testing.T) {
	n, home := testNode(t)
	n.dir = filepath.Join(home, ".persona")
	rec, room := "0f8fad5b-d9cb-469f-a165-70867728950e", "7d4a3c1e-1111-4222-8333-444455556666"
	cwd := filepath.Join(n.dir, "guest-meeting", rec)
	writeJSON := func(path string, v any) {
		t.Helper()
		raw, _ := json.Marshal(v)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ours := filepath.Join(n.dir, "assistant-settings.json")
	writeJSON(ours, hookSettings("/opt/persona/personad", n.dir, true))
	writeJSON(filepath.Join(home, ".claude", "settings.work.json"), map[string]any{
		"env":            map[string]any{"ANTHROPIC_BASE_URL": "https://gateway.example.com"},
		"apiKeyHelper":   "work-key-helper",
		"model":          "work-model",
		"hooks":          map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "cat ~/notes.md"}}}}},
		"outputStyle":    "Explanatory",
		"enabledPlugins": map[string]any{"notes@me": true},
		"statusLine":     map[string]any{"type": "command", "command": "my-status"},
		"permissions":    map[string]any{"allow": []any{"Bash"}},
		"skillOverrides": map[string]any{"notes": true},
	})
	profile := filepath.Join(home, ".claude")
	writeJSON(filepath.Join(profile, "settings.json"), map[string]any{
		"env":          map[string]any{"ANTHROPIC_BASE_URL": "https://home-gateway.example.com", "ANTHROPIC_AUTH_TOKEN": "profile-token"},
		"apiKeyHelper": "home-key-helper",
		"hooks":        map[string]any{"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "cat ~/diary.md"}}}}},
		"outputStyle":  "Learning",
		"permissions":  map[string]any{"defaultMode": "bypassPermissions"},
	})
	merged, err := n.mergeLauncherSettings("work", "~/.claude/settings.work.json", ours, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, personaHooks := range []bool{true, false} {
		path, err := n.meetingRoomSettingsFile(room, cwd, profile, merged, personaHooks)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"notes.md", "diary.md", "outputStyle", "Explanatory", "Learning", "enabledPlugins", "notes@me", "statusLine", "my-status",
			"permissions", "bypassPermissions", "skillOverrides", "home-gateway", "home-key-helper"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("hooks in settings %v: the room's settings carry the launcher's %s: %s", personaHooks, bad, raw)
			}
		}
		var s struct {
			Env          map[string]string `json:"env"`
			APIKeyHelper string            `json:"apiKeyHelper"`
			Model        string            `json:"model"`
			Hooks        map[string]any    `json:"hooks"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		if s.Env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" || s.Env["ANTHROPIC_AUTH_TOKEN"] != "profile-token" ||
			s.Env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" || s.APIKeyHelper != "work-key-helper" || s.Model != "work-model" {
			t.Errorf("hooks in settings %v: the launcher's environment or login lost: %s", personaHooks, raw)
		}
		if personaHooks != strings.Contains(string(raw), "/opt/persona/personad") || personaHooks != (s.Hooks != nil) {
			t.Errorf("hooks in settings %v: Persona's hooks: %s", personaHooks, raw)
		}
	}
}

// A room whose record lost its kind (stored by a manager that did not keep it) is still a room by its directory: its
// processes run as a room's, and it can be deleted. One that lost its tools too never runs: its process would get the
// assistant's.
func TestMeetingRoomByItsDirectory(t *testing.T) {
	n, home := testNode(t)
	n.dir = filepath.Join(home, ".persona")
	rec, room := "0f8fad5b-d9cb-469f-a165-70867728950e", "7d4a3c1e-1111-4222-8333-444455556666"
	rel := ".persona/guest-meeting/" + rec
	dir := n.assistantDirOf(rel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	n.state.put(room, sessionState{Launcher: "claude", Cwd: dir, Guest: []string{"meeting_lines"}})
	if n.guestKindOf(room) != guestMeeting || !n.isAssistantSession(room) {
		t.Fatal("a room that lost its kind is not a room")
	}
	other := "8e5b4d2f-2222-4333-8444-555566667777"
	n.state.put(other, sessionState{Launcher: "claude", Cwd: filepath.Join(n.dir, "guest"), Guest: []string{"lark_send"}})
	if n.guestKindOf(other) != "" {
		t.Fatal("an extension's guest taken for a room")
	}
	if _, err := n.assistantGuestDelete(json.RawMessage(`{"dir":"` + rel + `","session_id":"` + room + `"}`)); err != nil {
		t.Fatalf("a room that lost its kind is not deleted: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("its directory: %v", err)
	}

	n = codexTestNode(t)
	n.dir = filepath.Join(n.home, ".persona")
	dir = n.assistantDirOf(rel)
	n.state.put(room, sessionState{Launcher: "claude", Cwd: dir})
	if _, err := n.assistantSend(context.Background(), room, "", "", "a batch", nil, "", ""); !errors.Is(err, errRoomRecord) {
		t.Fatalf("a room without its tools resumed: %v", err)
	}
	if _, err := n.spawnClaude(room, dir, "claude", true, false); !errors.Is(err, errRoomRecord) {
		t.Fatalf("a room without its tools started: %v", err)
	}
}

// A new conversation's first message with its client_msg_id and no given id (one a person's own app opened, the
// manager's humanStart): once it started, the node expects that message under the conversation's id, as sent before it
// was typed and after nothing, ties it to the conversation's first user line and tags that line's items with it.
func TestANewConversationsFirstMessageIDIsTiedOnceItStarted(t *testing.T) {
	fakeClaudeHome(t, "read line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	withFakeHub(t, n)
	const text = "Fix the release CI with my token."
	before := time.Now()
	params := json.RawMessage(`{"cwd":"` + t.TempDir() + `","launcher":"claude","text":"` + text + `","client_msg_id":"start-x"}`)
	out, err := n.handle(context.Background(), "assistant.send", params)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := out.(map[string]any)["session_id"].(string)
	if id == "" {
		t.Fatalf("answer %v", out)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	r, ok := n.echoes.record(id, "start-x")
	if !ok || r.Text != text || r.AfterSeq != 0 || r.SentAt.Before(before) || r.SentAt.After(time.Now()) {
		t.Fatalf("its first message is not expected under its id: %+v %v", r, ok)
	}
	line := []proto.Item{{ID: "u1", Seq: 1, Role: "user", Kind: proto.KindText, Text: text, TS: time.Now()},
		{ID: "u1#1", Seq: 1, Role: "user", Kind: proto.KindImage, TS: time.Now()}}
	if found := n.echoes.match(id, line); len(found) != 1 || found[0].Line != "u1" {
		t.Fatalf("its first line was not found: %+v", found)
	}
	for _, it := range n.echoes.tag(id, line) {
		if it.ClientMsgID != "start-x" {
			t.Fatalf("an item of its first line carries %q", it.ClientMsgID)
		}
	}
}

// A start under a given id with its first message's client_msg_id (a meeting's room, its follow-up, Just me's new main
// chat): the node expects that message in the transcript under the id, as a session.send's, and a start asked again
// is that conversation.
func TestNewSessionIDFirstMessageID(t *testing.T) {
	fakeClaudeHome(t, "read line\necho '{\"type\":\"system\",\"subtype\":\"init\"}'\nsleep 30\n")
	n := codexTestNode(t)
	withFakeHub(t, n)
	id := "7d4a3c1e-1111-4222-8333-444455556666"
	params := json.RawMessage(`{"new_session_id":"` + id + `","cwd":"` + t.TempDir() + `","launcher":"claude","text":"Draft the email to the vendor.","client_msg_id":"followup-x"}`)
	out, err := n.handle(context.Background(), "assistant.send", params)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.stopAssistant(id) })
	if out.(map[string]any)["session_id"] != id {
		t.Fatalf("answer %v", out)
	}
	if r, ok := n.echoes.record(id, "followup-x"); !ok || r.Text != "Draft the email to the vendor." || r.AfterSeq != 0 {
		t.Fatalf("its first message is not expected under its id: %+v %v", r, ok)
	}
	out, err = n.handle(context.Background(), "assistant.send", params)
	if err != nil || out.(map[string]any)["existed"] != true {
		t.Fatalf("asked again: %v %v", out, err)
	}
}
