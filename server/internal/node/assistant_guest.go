package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Guest conversations (the hub's assistant/guest.go, docs/assistant/assistant.md ("Guest conversations")): an assistant conversation that
// answers someone else for the user. It runs on Claude Code on the CLI or the Agent SDK (no other agent or runtime),
// never in the warm spare, and every CLI process has no built-in tools (--tools ""). The SDK uses tools: [] and an
// in-process server for its declared MCP tools. Both load only Persona's MCP server (strict MCP config, no user's or
// extensions' servers), and only their own tools. The hub lists and runs only those for it and gives it no live
// snapshot; the tools it has are kept in its sessionState.Guest.

// maxGuestTools is the hub's bound (assistant/guest.go).
const maxGuestTools = 8

var guestToolName = lazyre.New(`^[a-z][a-z0-9_]{0,63}$`)

// checkGuestTools: one to maxGuestTools tool names, plain identifiers (they go into --allowedTools).
func checkGuestTools(tools []string) error {
	if len(tools) == 0 || len(tools) > maxGuestTools {
		return fmt.Errorf("a guest conversation takes 1 to %d tools", maxGuestTools)
	}
	for _, t := range tools {
		if !guestToolName.MatchString(t) {
			return fmt.Errorf("bad guest tool %q", t)
		}
	}
	return nil
}

// guestParams is assistant.send's guest: the tools of Persona's it may use, and its kind ("" an extension's,
// guestMeeting a meeting's room).
type guestParams struct {
	Tools []string `json:"tools"`
	Kind  string   `json:"kind,omitempty"`
}

// guestMeeting is the kind of a meeting's room conversation (docs/assistant/meeting-minutes.md "The room
// conversation"): its own directory below guestMeetingDir, and nothing of the owner's Claude Code setup in any of its
// processes (meetingRoomArgs).
const guestMeeting = "meeting"

// guestMeetingDir is where the rooms' directories are, one per meeting, relative to the home as the hub names them.
var guestMeetingDir = compat.StateDir + "/guest-meeting"

type guestKey struct{}

type guestStart struct {
	tools []string
	kind  string
}

// withGuest marks ctx's assistant.send as starting a guest conversation with tools.
func withGuest(ctx context.Context, tools []string) context.Context {
	return withGuestKind(ctx, tools, "")
}

// withGuestKind is withGuest for a guest of kind.
func withGuestKind(ctx context.Context, tools []string, kind string) context.Context {
	return context.WithValue(ctx, guestKey{}, guestStart{tools: tools, kind: kind})
}

func guestFrom(ctx context.Context) []string {
	g, _ := ctx.Value(guestKey{}).(guestStart)
	return g.tools
}

func guestKindFrom(ctx context.Context) string {
	g, _ := ctx.Value(guestKey{}).(guestStart)
	return g.kind
}

type newIDKey struct{}

// withNewID asks ctx's assistant.send for a new conversation to start it under id (new_session_id).
func withNewID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, newIDKey{}, id)
}

func newIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(newIDKey{}).(string)
	return id
}

// knowsConversation: the node has conversation id already (a process, or its transcript): a start under it is that
// conversation, not a second one. A state alone is not: a guest's is written before its first process starts, and
// one that failed to start left nothing to resume; its start asked again starts it.
func (n *Node) knowsConversation(id string) bool {
	n.mu.Lock()
	p := n.procs[id]
	n.mu.Unlock()
	return p != nil || n.transcriptPath(id) != ""
}

// errGuestAgent refuses a guest conversation anything but Claude Code on the CLI or the Agent SDK.
var errGuestAgent = errors.New("a guest conversation runs on Claude Code only (its built-in tools can be taken away there)")

// assistantGuestPrepare makes a guest conversation's directory and writes only its instructions there. A meeting's room
// (kind meeting) gets a directory of its own below guestMeetingDir; the answer then says the node starts rooms
// ("kind", "start_ids": a start under a given session id), which a hub needs before it opens one: an older node
// answers neither.
func (n *Node) assistantGuestPrepare(params json.RawMessage) (any, error) {
	var a struct {
		Dir      string `json:"dir"`
		ClaudeMD string `json:"claude_md"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(params, &a); err != nil || a.Dir == "" || strings.Contains(a.Dir, "..") || filepath.IsAbs(a.Dir) {
		return nil, errors.New("bad guest dir")
	}
	switch a.Kind {
	case "":
	case guestMeeting:
		if !meetingRoomDir(a.Dir) {
			return nil, errors.New("bad guest dir")
		}
	default:
		return nil, fmt.Errorf("unknown guest kind %q", a.Kind)
	}
	dir, err := n.preparedDir(a.Dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(a.ClaudeMD), 0o600); err != nil {
			return nil, err
		}
	}
	if a.Kind == guestMeeting {
		return map[string]any{"cwd": dir, "kind": guestMeeting, "start_ids": true}, nil
	}
	return map[string]any{"cwd": dir}, nil
}

// meetingRoomDir: rel names one room's directory, guestMeetingDir/<recording id> (in either spelling of the state
// directory).
func meetingRoomDir(rel string) bool {
	sub, ok := compat.StateSubpath(filepath.ToSlash(rel))
	if !ok {
		return false
	}
	parent, name := path.Split(sub)
	return strings.Trim(parent, "/") == "guest-meeting" && uuidRe.MatchString(name)
}

var uuidRe = lazyre.New(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// assistantGuestDelete removes a meeting's room (assistant.guest_delete {dir, session_id}, at its transcript's cut or
// with its recording): its process, its transcript and every file saved for it in its directory. Only a room's: the
// session must be a meeting guest of this node, or (its state gone) have its transcript under the room's directory's
// project; anything else is refused. Removing what is gone already is not an error.
func (n *Node) assistantGuestDelete(params json.RawMessage) (any, error) {
	var a struct {
		Dir       string `json:"dir"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(params, &a); err != nil || !meetingRoomDir(a.Dir) || !validID(a.SessionID) {
		return nil, errors.New("bad room")
	}
	dir := n.assistantDirOf(a.Dir)
	transcript := n.transcriptPath(a.SessionID)
	st, known := n.state.get(a.SessionID)
	switch {
	case known && n.roomState(st) && filepath.Clean(st.Cwd) == filepath.Clean(dir):
	case known:
		return nil, errors.New("not this meeting's room")
	case transcript != "" && filepath.Base(filepath.Dir(transcript)) != claudeProjectName(dir):
		return nil, errors.New("not this meeting's room")
	}
	// Claude Code writes its last lines into the transcript as it exits (the last prompt, its costs): removed before
	// then, the file came back with the owner's last question in it (#4665). One still running is asked again.
	n.cancelAssistantRecovery(a.SessionID)
	if p := n.takeAssistant(a.SessionID); p != nil && stopAndWait([]*assistantProc{p}) > 0 {
		return nil, errors.New("the room's process is still running")
	}
	if transcript != "" {
		if err := os.Remove(transcript); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		// What Claude Code keeps beside it (tool results, subagents), and its folder for the session in the profile.
		if err := os.RemoveAll(strings.TrimSuffix(transcript, ".jsonl")); err != nil {
			return nil, err
		}
		if projects := filepath.Dir(filepath.Dir(transcript)); filepath.Base(projects) == "projects" {
			if err := os.RemoveAll(filepath.Join(filepath.Dir(projects), "session-env", a.SessionID)); err != nil {
				return nil, err
			}
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// claudeProjectName is the name Claude Code gives the projects folder of the conversations run in dir.
func claudeProjectName(dir string) string {
	var b strings.Builder
	for _, c := range filepath.Clean(dir) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// guestKindOf is conversation id's guest kind ("" for none or an extension's guest).
func (n *Node) guestKindOf(id string) string {
	if id == "" {
		return ""
	}
	st, _ := n.state.get(id)
	if n.roomState(st) {
		return guestMeeting
	}
	return st.GuestKind
}

// roomState: st is a meeting's room: its kind says so, or, a record that lost its kind (kept by a manager that did not
// store it), its directory is a room's.
func (n *Node) roomState(st sessionState) bool {
	return st.GuestKind == guestMeeting || st.GuestKind == "" && n.roomCwd(st.Cwd)
}

// roomCwd: cwd is a meeting room's directory on this node, guestMeetingDir/<recording id> in its state directory (in
// either spelling).
func (n *Node) roomCwd(cwd string) bool {
	if cwd == "" {
		return false
	}
	cwd = filepath.Clean(cwd)
	name := filepath.Base(cwd)
	if !uuidRe.MatchString(name) {
		return false
	}
	for _, rel := range []string{guestMeetingDir + "/" + name, ".persona/guest-meeting/" + name} {
		if filepath.Clean(n.assistantDirOf(rel)) == cwd {
			return true
		}
	}
	return false
}

// errRoomRecord refuses to run a meeting's room whose record lost its tools: without them its process would get the
// assistant's.
var errRoomRecord = errors.New("this meeting's room lost its record on this computer: it does not run without it")

// meetingRoomArgs are what a room's process runs with besides a guest's flags, so that only the meeting CLAUDE.md
// and what the node passes itself reach the model (docs/assistant/meeting-minutes.md "Nothing of the owner's Claude
// Code setup"): only the project's settings sources (never the user's: their CLAUDE.md, hooks, output style,
// plugins and permissions), and the room's own --settings, which carries the launcher's environment and excludes
// every CLAUDE.md of the directories above the room's (~/CLAUDE.md…), which the project source would load too.
func meetingRoomArgs() []string {
	return []string{"--setting-sources", "project"}
}

// meetingRoomSettings adds to a room's settings s what keeps the owner's instructions out: the CLAUDE.md files of
// every directory above cwd and of its profile (configDir, "" for none) excluded, no auto-memory.
func meetingRoomSettings(s map[string]any, cwd, home, configDir string) {
	var excludes []string
	for dir := filepath.Dir(filepath.Clean(cwd)); ; dir = filepath.Dir(dir) {
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", filepath.Join(".claude", "CLAUDE.md")} {
			excludes = append(excludes, filepath.Join(dir, name))
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	if home != "" {
		excludes = append(excludes, filepath.Join(home, ".claude", "CLAUDE.md"))
	}
	for _, cfg := range []string{os.Getenv("CLAUDE_CONFIG_DIR"), configDir} {
		if f := filepath.Join(cfg, "CLAUDE.md"); cfg != "" && !slices.Contains(excludes, f) {
			excludes = append(excludes, f)
		}
	}
	s["claudeMdExcludes"] = excludes
	env, _ := s["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	s["env"] = env
}

// meetingRoomKeys are what a room's settings keep of the start's (a launcher's own --settings merged with Persona's)
// and of its profile's own settings.json: the environment, the login and the model. Never the rest of those files:
// they are user settings files (`claude --settings ~/.claude/settings.work.json`, ~/.claude/settings.json) whose
// hooks, plugins, output style, status line or permissions would reach the room, since flag settings load whatever
// --setting-sources says.
var meetingRoomKeys = []string{"env", "model", "apiKeyHelper", "awsAuthRefresh", "awsCredentialExport", "forceLoginMethod", "forceLoginOrgUUID",
	"enableArtifact"}

// meetingRoomProfile is the profile a new room of a node with no session scope signs in with when its start names
// none: the one the assistant's conversations had before each new one got a private profile (assistant_config_dir.go
// "legacy"). A private profile is a new config directory with no login in it (no credentials, no settings.json with a
// gateway's token), and the room answered every question "Not logged in".
const meetingRoomProfile = "legacy"

// startProfile is the profile (config_dir) a start asks assistantProfileSelection for: asked, or meetingRoomProfile for
// a new conversation of guest kind guestKind that is a room and names none.
func (n *Node) startProfile(asked, guestKind string, resume bool) string {
	if !resume && asked == "" && n.sessionScope == nil && guestKind == guestMeeting {
		return meetingRoomProfile
	}
	return asked
}

// meetingRoomSettingsFile writes room id's settings: meetingRoomKeys of its profile's settings.json (configDir's,
// "" for none), which --setting-sources project leaves out (a gateway and its token in its env, an apiKeyHelper), with
// those of settings (a file or JSON, "" for none) over them as Claude Code ranks them (env merged by name), Persona's
// own hooks when they go in --settings (personaHooks: no plugin carries them) and meetingRoomSettings.
func (n *Node) meetingRoomSettingsFile(id, cwd, configDir, settings string, personaHooks bool) (string, error) {
	s := map[string]any{}
	keep := func(from map[string]any) {
		for _, k := range meetingRoomKeys {
			v, ok := from[k]
			if !ok {
				continue
			}
			if env, isEnv := v.(map[string]any); isEnv && k == "env" {
				merged, _ := s["env"].(map[string]any)
				if merged == nil {
					merged = map[string]any{}
				}
				for name, value := range env {
					merged[name] = value
				}
				v = merged
			}
			s[k] = v
		}
	}
	if configDir != "" {
		// Anything wrong with it: none of it, as Claude Code would start without it too.
		if user, err := readLauncherSettings(filepath.Join(configDir, "settings.json"), cwd, n.home); err == nil {
			keep(user)
		}
	}
	if settings != "" {
		start, err := readLauncherSettings(settings, cwd, n.home)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		keep(start)
	}
	if personaHooks { // from Persona's own file: the merged one holds the launcher's hooks too
		if ours, err := readLauncherSettings(filepath.Join(n.dir, "assistant-settings.json"), cwd, n.home); err == nil && ours["hooks"] != nil {
			s["hooks"] = ours["hooks"]
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	meetingRoomSettings(s, cwd, n.home, configDir)
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	dir := n.dir
	if dir == "" {
		dir = filepath.Join(n.home, ".persona")
	}
	return writeSettings(dir, "assistant-room-"+id+".json", b)
}

// guestOf is conversation id's guest tools (nil: not a guest).
func (n *Node) guestOf(id string) []string {
	if id == "" {
		return nil
	}
	st, _ := n.state.get(id)
	return st.Guest
}

// guestMCPAndTools are a guest conversation's process flags: Persona's MCP server alone, told it is a guest's, and
// only its tools allowed. The Agent SDK maps these to its stdio proxy with no built-in tools (assistant_start.go).
func (n *Node) guestMCPAndTools(id string, tools []string, legacyMCP bool) (mcpJSON string, toolFlags []string) {
	env := assistantMCPEnv(id, n.dir)
	env["PERSONA_GUEST"] = "1"
	mcp, _ := json.Marshal(map[string]any{"mcpServers": compat.MCPRegistrationServers(map[string]any{"persona": map[string]any{"command": n.bin, "args": []string{"mcp"}, "env": env}}, legacyMCP)})
	allowed := compat.MCPAllowedToolNames(tools, legacyMCP)
	return string(mcp), []string{"--mcp-config", string(mcp), "--strict-mcp-config", "--tools", "", "--allowedTools", strings.Join(allowed, ",")}
}

// assistantDirOf is where a directory the hub names relative to HOME for the assistant (".persona/assistant",
// ".persona/health", ".persona/guest", or the former name's spelling of them, which a hub sends for older nodes) is on
// this node: in the state directory the node runs on (stateRoot), whichever spelling it came in. Joined to HOME as
// written, the former spelling recreated the former directory once its alias was gone (#1681).
func (n *Node) assistantDirOf(rel string) string {
	sub, ok := compat.StateSubpath(filepath.ToSlash(rel))
	if !ok || n.dir == "" {
		return filepath.Join(n.home, rel)
	}
	return filepath.Join(n.stateRoot(), filepath.FromSlash(sub))
}

// stateRoot is the node's state directory as its assistant's sessions run in it. A custom PERSONA_HOME is that root, the
// data_dir the hub reports the assistant's directory in (#5505). On the default layouts it is whichever of ~/.persona and
// the former state directory (compat.StateDir) is a directory rather than an alias of the other and is the node's: an
// installation still kept under the former name keeps the path Claude Code knows its sessions and memory by
// (docs/operations/rename.md), and a migrated one never gets the former directory back. n.dir as it is when it is
// neither (not made yet).
func (n *Node) stateRoot() string {
	dir := filepath.Clean(n.dir)
	if n.customDataDir() {
		return dir
	}
	want, err := os.Stat(dir)
	if err != nil {
		return dir
	}
	for _, name := range []string{".persona", compat.StateDir} {
		p := filepath.Join(n.home, name)
		if st, err := os.Lstat(p); err == nil && st.IsDir() && os.SameFile(st, want) {
			return p
		}
	}
	return dir
}

// preparedDir is assistantDirOf(rel) for assistant.prepare and assistant.guest_prepare, which make it: below the node's
// state directory only, never the state directory itself (#1681). A state directory that is not there (moved or removed
// under a running node) is refused rather than made again empty, which would split the assistant's files in two.
func (n *Node) preparedDir(rel string) (string, error) {
	dir := n.assistantDirOf(rel)
	if _, ok := compat.StateSubpath(filepath.ToSlash(rel)); ok && n.dir != "" {
		root := n.stateRoot()
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			return "", fmt.Errorf("the state directory %s is not there: not making the assistant's directory in a new one", root)
		}
	}
	return dir, nil
}

// customDataDir: the node's data directory is a root of its own (PERSONA_HOME), not one of the default ones.
func (n *Node) customDataDir() bool {
	if n.dir == "" {
		return false
	}
	d := filepath.Clean(n.dir)
	return d != filepath.Join(n.home, ".persona") && d != filepath.Join(n.home, compat.StateDir)
}
