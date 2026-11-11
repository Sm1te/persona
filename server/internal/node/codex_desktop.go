package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// The Codex desktop App keeps a local message bus — <CODEX_HOME>/ipc/ipc.sock, on Windows the named pipe
// \\.\pipe\codex-ipc (codex_desktop_unix.go, codex_desktop_windows.go): its windows ask each other to run a turn in a
// thread one of them owns. Persona joins that bus as one more client, so a message from the phone runs in the App
// window that owns the thread — it shows in the App, and no second writer opens the rollout.
//
// Frames are a 4-byte little-endian length and a JSON object. Every request carries the version the App expects for
// its method; without it no client claims the request and the bus answers "no-client-found".

const codexDesktopTimeout = 60 * time.Second

// codexDesktopLoadWait: how long a thread the App has not loaded (one a thread just created, one not opened since the
// App started) is given to load after the App is asked to open it.
var codexDesktopLoadWait = 30 * time.Second

var codexDesktopVersions = map[string]int{
	"thread-owner-discovery":     1,
	"thread-follower-start-turn": 2,
}

var errCodexDesktopNotLoaded = errors.New("Codex App 没能打开这个对话：在电脑的 Codex App 里打开它一次再发")

type codexDesktopFrame struct {
	Type            string          `json:"type"`
	RequestID       string          `json:"requestId,omitempty"`
	Method          string          `json:"method,omitempty"`
	ResultType      string          `json:"resultType,omitempty"`
	Error           string          `json:"error,omitempty"`
	HandledByClient string          `json:"handledByClientId,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Params          any             `json:"params,omitempty"`
	Version         int             `json:"version,omitempty"`
	TargetClientID  string          `json:"targetClientId,omitempty"`
	Response        any             `json:"response,omitempty"`
}

type codexDesktop struct{ conn net.Conn }

func dialCodexDesktop(ctx context.Context, home string) (*codexDesktop, error) {
	conn, err := dialCodexDesktopBus(ctx, home)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c := &codexDesktop{conn: conn}
	if _, err := c.request("initialize", map[string]any{"clientType": "persona"}, ""); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *codexDesktop) close() { c.conn.Close() }

func (c *codexDesktop) write(f codexDesktopFrame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	buf := make([]byte, 4+len(b))
	binary.LittleEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err = c.conn.Write(buf)
	return err
}

func (c *codexDesktop) read() (codexDesktopFrame, error) {
	var f codexDesktopFrame
	var head [4]byte
	if _, err := io.ReadFull(c.conn, head[:]); err != nil {
		return f, err
	}
	n := binary.LittleEndian.Uint32(head[:])
	if n == 0 || n > 64<<20 {
		return f, fmt.Errorf("codex app ipc: bad frame length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(c.conn, b); err != nil {
		return f, err
	}
	return f, json.Unmarshal(b, &f)
}

// request sends one request and waits for its response; broadcasts are skipped, and the bus asking whether Persona
// handles someone else's request is told no at once (it would otherwise wait out its timeout for every request).
func (c *codexDesktop) request(method string, params any, target string) (codexDesktopFrame, error) {
	id := newUUID()
	if err := c.write(codexDesktopFrame{Type: "request", RequestID: id, Method: method, Params: params, Version: codexDesktopVersions[method], TargetClientID: target}); err != nil {
		return codexDesktopFrame{}, err
	}
	for {
		f, err := c.read()
		if err != nil {
			return f, err
		}
		switch f.Type {
		case "client-discovery-request":
			if err := c.write(codexDesktopFrame{Type: "client-discovery-response", RequestID: f.RequestID, Response: map[string]any{"canHandle": false}}); err != nil {
				return f, err
			}
		case "response":
			if f.RequestID != id {
				continue
			}
			if f.ResultType != "success" {
				return f, fmt.Errorf("codex app ipc %s: %s", method, f.Error)
			}
			return f, nil
		}
	}
}

// sendCodexDesktop runs a turn in a thread the Codex App has loaded.
func (n *Node) sendCodexDesktop(ctx context.Context, src codexSource, text string, images []proto.Image) error {
	paths, err := n.saveImages(images)
	if err != nil {
		return refusal("bad_image", err.Error(), nil)
	}
	input := []map[string]any{}
	if text != "" {
		input = append(input, map[string]any{"type": "text", "text": text, "text_elements": []any{}})
	}
	for _, p := range paths {
		input = append(input, map[string]any{"type": "localImage", "path": p})
	}
	ctx, cancel := context.WithTimeout(ctx, codexDesktopTimeout)
	defer cancel()
	c, err := dialCodexDesktop(ctx, src.home)
	if err != nil {
		return refusal("codex_read_only", "连不上电脑上的 Codex App："+err.Error(), nil)
	}
	defer c.close()
	owner, err := c.owner(src.thread)
	if err != nil {
		// Not loaded: the App loads a thread it is asked to open (codex://threads/<id>, in front — opened in the
		// background it does not), then its window owns it. An opener that cannot ask (no codex:// handler) skips the wait.
		if openCodexThread(src.thread) == nil {
			deadline := time.Now().Add(codexDesktopLoadWait)
			for err != nil && time.Now().Before(deadline) && ctx.Err() == nil {
				time.Sleep(time.Second)
				owner, err = c.owner(src.thread)
			}
		}
	}
	if err != nil {
		return refusal("codex_desktop_not_loaded", errCodexDesktopNotLoaded.Error(), nil)
	}
	turn := map[string]any{
		"conversationId": src.thread,
		"turnStart": map[string]any{
			"request": map[string]any{"threadId": src.thread, "clientUserMessageId": newUUID(), "input": input},
			"context": map[string]any{},
		},
	}
	if _, err := c.request("thread-follower-start-turn", turn, owner.HandledByClient); err != nil {
		// The owner took the request: whether the turn started is not known.
		return uncertain(err)
	}
	n.notifyCodex()
	return nil
}

// The Codex App's threads are listed like its sidebar: grouped by its projects, newest first, and those of the last
// few days with a state like herdr's — working while a turn runs; after it, done ("Unread") while the App's sidebar
// still marks the thread unread, idle once it was looked at in the App (or, as for any agent, in Persona).
// A process holding the rollout open only ever said "unknown", which the phone folds away with the quiet ones.
const codexDesktopRecent = 72 * time.Hour

// codexAppState is what the App keeps in <CODEX_HOME>/.codex-global-state.json about its sidebar: the unread threads,
// its projects and which thread belongs to which. Read again when the file changes.
type codexAppState struct {
	unread      map[string]bool
	projectless map[string]bool
	assigned    map[string]string // thread → project id
	projects    map[string]codexAppProject
	sidebar     []string // the threads of both lists that are valid ids, sorted: what codexAppThreads looks up

	order []string // the App's projects in its sidebar's order (project-order)

	namesOnce sync.Once
	names     map[string]string // a project's folder (real path) → the App's name for it; see folderName
	places    map[string]int    // a project's folder (real path) → its place in order, from 1; see folderPlace
}

type codexAppProject struct {
	Name      string   `json:"name"`
	RootPaths []string `json:"rootPaths"`
}

type codexAppStateCache struct {
	mtime time.Time
	size  int64
	state *codexAppState
}

var codexAppStates sync.Map // home → codexAppStateCache

func loadCodexAppState(home string) *codexAppState {
	p := filepath.Join(home, ".codex-global-state.json")
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if v, ok := codexAppStates.Load(home); ok {
		c := v.(codexAppStateCache)
		if c.mtime.Equal(st.ModTime()) && c.size == st.Size() {
			return c.state
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var raw struct {
		ReadState struct {
			UnreadByIdentity map[string]map[string][]string `json:"unreadByIdentity"`
		} `json:"electron-thread-read-state-v1"`
		Projectless []string `json:"projectless-thread-ids"`
		Assignments map[string]struct {
			ProjectID string `json:"projectId"`
		} `json:"thread-project-assignments"`
		Projects map[string]codexAppProject `json:"local-projects"`
		Order    []string                   `json:"project-order"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	s := &codexAppState{unread: map[string]bool{}, projectless: map[string]bool{}, assigned: map[string]string{}, projects: raw.Projects, order: raw.Order}
	for _, hosts := range raw.ReadState.UnreadByIdentity {
		for host, ids := range hosts {
			if len(host) < 6 || host[:6] != "local:" {
				continue
			}
			for _, id := range ids {
				s.unread[id] = true
			}
		}
	}
	for _, id := range raw.Projectless {
		s.projectless[id] = true
	}
	for id, a := range raw.Assignments {
		s.assigned[id] = a.ProjectID
	}
	add := func(id string) {
		if validID(id) && len(s.sidebar) < codexAppSidebarMax {
			s.sidebar = append(s.sidebar, id)
		}
	}
	for id := range s.projectless {
		add(id)
	}
	for id := range s.assigned {
		add(id)
	}
	slices.Sort(s.sidebar)
	s.sidebar = slices.Compact(s.sidebar)
	codexAppStates.Store(home, codexAppStateCache{mtime: st.ModTime(), size: st.Size(), state: s})
	return s
}

// project: the App project a thread is listed under — the one it was put in, else the one whose root holds its cwd.
func (a *codexAppState) project(thread, cwd string) (id, name string) {
	if a == nil {
		return "", ""
	}
	if pid := a.assigned[thread]; pid != "" {
		if p, ok := a.projects[pid]; ok && p.Name != "" {
			return pid, p.Name
		}
	}
	best := ""
	for pid, p := range a.projects {
		for _, root := range p.RootPaths {
			root = filepath.Clean(root)
			if pathWithin(cwd, root) && len(root) > len(best) {
				best, id, name = root, pid, p.Name
			}
		}
	}
	return id, name
}

// pathWithin: dir is root or under it (both clean). Windows paths do not tell case apart (the App and a rollout may
// spell a drive or a folder differently), and a drive's root ends in its separator already.
func pathWithin(dir, root string) bool {
	if runtime.GOOS == "windows" {
		dir, root = strings.ToLower(filepath.Clean(dir)), strings.TrimSuffix(strings.ToLower(root), `\`)
	}
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// folderName: the App's name for the project whose folder is root (a real path, as codexProjectRoot gives a
// repository's root); "" when no project of the App has that folder. The App's sidebar shows a project by the name it
// keeps, which need not be the folder's: a project the user renamed, a ChatGPT project the App keeps in
// <CODEX_HOME>/.chatgpt-projects/g-p-<id>. A folder has one name whichever chat asks: of two projects with the same
// folder, the name that sorts first.
func (a *codexAppState) folderName(root string) string {
	if a == nil || root == "" {
		return ""
	}
	a.folders()
	return a.names[folderKey(root)]
}

// folderPlace: where the App's sidebar lists the project whose folder is root (as for folderName), from 1 down its
// project-order; 0 when no project with that folder is in the order (the order lists the projects the App's own
// sidebar shows; a ChatGPT project is not among them). Of two projects with the same folder, the first in the order.
func (a *codexAppState) folderPlace(root string) int {
	if a == nil || root == "" {
		return 0
	}
	a.folders()
	return a.places[folderKey(root)]
}

// folders reads the projects' folders once: real paths, as codexProjectRoot gives a repository's root.
func (a *codexAppState) folders() {
	a.namesOnce.Do(func() {
		a.names, a.places = map[string]string{}, map[string]int{}
		place := map[string]int{}
		for i, id := range a.order {
			if _, ok := place[id]; !ok {
				place[id] = i + 1
			}
		}
		for id, p := range a.projects {
			name := strings.TrimSpace(p.Name)
			for _, r := range p.RootPaths {
				if !filepath.IsAbs(r) {
					continue
				}
				k := folderKey(realPath(filepath.Clean(r)))
				if old, ok := a.names[k]; name != "" && (!ok || name < old) {
					a.names[k] = name
				}
				if i := place[id]; i > 0 && (a.places[k] == 0 || i < a.places[k]) {
					a.places[k] = i
				}
			}
		}
	})
}

// folderKey: Windows paths do not tell case apart (see pathWithin).
func folderKey(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// inNoProject: the App keeps chat thread in no project (its sidebar's chats outside every project).
func (a *codexAppState) inNoProject(thread string) bool { return a != nil && a.projectless[thread] }

// listed: the thread is one of the App's sidebar chats (put in a project, or a chat without one). Other tools that
// run the App's own codex binary write "Codex Desktop" rollouts too; the App does not list those.
func (a *codexAppState) listed(thread string) bool {
	return a != nil && (a.assigned[thread] != "" || a.projectless[thread])
}

// placeCodexDesktop puts an App thread in its sidebar project — a subagent in its parent's (`group`) — and, for recent
// ones, gives it its state. A subagent's finished turn is not something to read on its own: it is idle.
func (n *Node) placeCodexDesktop(s *proto.Session, src codexSource, group string, subagent bool) {
	app := loadCodexAppState(src.home)
	s.SpaceKind = "codex_app"
	if pid, name := app.project(group, s.Cwd); pid != "" && name != "" && (app == nil || !app.projectless[group]) {
		s.SpaceID, s.SpaceName = "codex-app:"+pid, name
	} else {
		s.SpaceID, s.SpaceName = "codex-app:chats", "Codex 对话"
	}
	if src.path == "" || s.UpdatedAt.IsZero() || time.Since(s.UpdatedAt) >= codexDesktopRecent {
		return
	}
	s.Status = n.codexDesktopTurn(src.path)
	if s.Status == proto.StatusDone && (subagent || app == nil || !app.unread[src.thread]) {
		s.Status = proto.StatusIdle // looked at in the App
	}
}

// slotCodexDesktop numbers each project's threads newest first (thread ids are time-ordered), as the App lists them.
func slotCodexDesktop(list []proto.Session) {
	next := map[string]int{}
	for i := len(list) - 1; i >= 0; i-- { // list is sorted by session id, oldest first
		s := &list[i]
		if s.SpaceKind != "codex_app" {
			continue
		}
		s.SlotIndex = next[s.SpaceID]
		next[s.SpaceID]++
	}
}

type codexTurnMark struct {
	size   int64
	status string
}

// codexDesktopTurn: working while the rollout's last turn has started and not ended, done after; read from the tail
// and kept until the file grows.
func (n *Node) codexDesktopTurn(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return proto.StatusUnknown
	}
	if v, ok := codexTurnMarks.Load(path); ok && v.(codexTurnMark).size == st.Size() {
		return v.(codexTurnMark).status
	}
	status := proto.StatusDone
	if f, err := os.Open(path); err == nil {
		const tail = 256 << 10
		off := max(st.Size()-tail, 0)
		b := make([]byte, st.Size()-off)
		_, err = f.ReadAt(b, off)
		f.Close()
		if err == nil {
			for _, line := range bytes.Split(b, []byte("\n")) {
				var e struct {
					Type    string `json:"type"`
					Payload struct {
						Type string `json:"type"`
					} `json:"payload"`
				}
				if !bytes.Contains(line, []byte(`"event_msg"`)) || json.Unmarshal(line, &e) != nil || e.Type != "event_msg" {
					continue
				}
				switch e.Payload.Type {
				case "task_started":
					status = proto.StatusWorking
				case "task_complete", "turn_aborted":
					status = proto.StatusDone
				}
			}
		}
	}
	codexTurnMarks.Store(path, codexTurnMark{size: st.Size(), status: status})
	return status
}

var codexTurnMarks sync.Map

// codexOrigin is where a rollout's thread came from, from its first line (session_meta): the Codex App writes
// originator "Codex Desktop" or "codex_work_desktop" (source "vscode" for its chats, "exec" for its background runs, a thread_spawn object for
// subagents), the Codex CLI "codex-tui" / "cli", `codex exec` and the SDKs over it "codex_exec" / "codex_sdk_*" with
// source "exec" (their subagents keep the originator). Only the App's threads go through its IPC bus.
type codexOrigin struct {
	desktop bool
	cli     bool
	// exec: a one-shot run a program started (`codex exec`, an SDK, the App's background runs) or a subagent of one:
	// nobody's conversation, not listed in Agents (programRun).
	exec   bool
	sub    bool
	root   string // session_id identifies the shared root on newer Codex versions
	parent string // the thread that spawned this subagent
}

var codexOrigins sync.Map // path → codexOrigin (the first line never changes)

func readCodexOrigin(path string) codexOrigin {
	if v, ok := codexOrigins.Load(path); ok {
		return v.(codexOrigin)
	}
	f, err := os.Open(path)
	if err != nil {
		return codexOrigin{}
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(f, 64<<10).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return codexOrigin{}
	}
	var m struct {
		Type    string `json:"type"`
		Payload struct {
			Originator   string          `json:"originator"`
			Parent       string          `json:"parent_thread_id"`
			SessionID    string          `json:"session_id"`
			ThreadSource string          `json:"thread_source"`
			Source       json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &m) != nil || m.Type != "session_meta" {
		return codexOrigin{} // not written yet: read again next time
	}
	o := codexOrigin{desktop: m.Payload.Originator == "Codex Desktop" || m.Payload.Originator == "codex_work_desktop", cli: m.Payload.Originator == "codex-tui" || m.Payload.Originator == "codex_cli_rs"}
	var source string
	if json.Unmarshal(m.Payload.Source, &source) != nil {
		source = "" // an object: a subagent
	}
	o.exec = source == "exec" || m.Payload.Originator == "codex_exec" || strings.HasPrefix(m.Payload.Originator, "codex_sdk")
	var spawn struct {
		Subagent struct {
			ThreadSpawn struct {
				Parent string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(m.Payload.Source, &spawn) == nil {
		o.parent = spawn.Subagent.ThreadSpawn.Parent
	}
	o.parent = firstNonEmpty(m.Payload.Parent, o.parent)
	var fields map[string]json.RawMessage
	if json.Unmarshal(m.Payload.Source, &fields) == nil {
		_, o.sub = fields["subagent"]
	}
	o.sub = o.sub || o.parent != "" || source == "subagent" || m.Payload.ThreadSource == "subagent"
	if o.sub {
		o.root = m.Payload.SessionID
	}
	codexOrigins.Store(path, o)
	return o
}

// codexDesktopChat: a chat of the Codex App running on this machine (not a CLI thread, not a subagent of one).
func codexDesktopChat(src codexSource) bool { return codexDesktopChatIn(src, nil) }

// codexDesktopChatIn is codexDesktopChat with the App's presence looked up once per home in a list pass (codexListPass).
func codexDesktopChatIn(src codexSource, pass *codexListPass) bool {
	if !pass.desktopAvailable(src.home) {
		return false
	}
	if src.path == "" { // its rollout is gone: one the App's sidebar lists and its database has (codex_appdb.go)
		_, ok := codexAppThreadOf(src)
		return ok
	}
	o := readCodexOrigin(src.path)
	return o.desktop && o.parent == "" && loadCodexAppState(src.home).listed(src.thread)
}

// owner: the App window that owns the thread.
func (c *codexDesktop) owner(thread string) (codexDesktopFrame, error) {
	f, err := c.request("thread-owner-discovery", map[string]any{"hostId": "local", "conversationId": thread}, "")
	if err == nil && f.HandledByClient == "" {
		err = errCodexDesktopNotLoaded
	}
	return f, err
}
