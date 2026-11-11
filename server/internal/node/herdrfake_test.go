package node

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// fakeHerdr is a scriptable herdr API socket for node tests. It answers one JSON line per connection (like herdr),
// records every call in order, answers each method from a handler (default: an empty result), serves pane.read from
// a per-pane screen that hooks can change after given calls, and can return herdr errors.
//
// The recorder is what the characterization tests pin: the exact herdr calls (method + params) the node makes.
type fakeHerdr struct {
	sock   string
	mu     sync.Mutex
	log    []herdrCall
	on     map[string]func(p map[string]any) any
	screen map[string]string // pane id -> what pane.read returns
	ansi   map[string]string // pane id -> what pane.read with format ansi returns (else screen)
	hooks  []func(c herdrCall)
	// replyLimit simulates herdr's partial socket write; zero sends complete replies.
	replyLimit int
}

// herdrCall is one recorded request.
type herdrCall struct {
	Method string
	Params map[string]any
	Typed  string // a start's send_input: the line typed, `. <start file>`; Params has the file's command (typedstart.go)
}

// String is the canonical form tests compare: the method and its params as JSON with sorted keys.
func (c herdrCall) String() string {
	b, _ := json.Marshal(c.Params)
	return c.Method + " " + string(b)
}

// startFileCommand is the command a start typed through its start file (runAgentCommand) when text is the line that
// sources one: what the tests that pin a start's command compare.
func startFileCommand(text string) (string, bool) {
	_, file, ok := strings.Cut(text, " ")
	if !ok || !strings.HasPrefix(text, ". ") && !strings.HasPrefix(text, "source ") {
		return "", false
	}
	file, _, _ = strings.Cut(file, " && true ")
	file = strings.TrimSuffix(strings.TrimPrefix(file, "'"), "'")
	if !strings.HasPrefix(filepath.Base(file), startScriptPrefix) {
		return "", false
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	_, cmd, _ := strings.Cut(strings.TrimSuffix(string(b), "\n"), "\n") // after its rm of itself
	return cmd, true
}

// herdrFail makes a handler answer with a herdr error instead of a result.
type herdrFail struct{ Code, Message string }

// connDeadline bounds one connection: a client that never writes its line cannot hold up the calls behind it.
const connDeadline = 5 * time.Second

func newFakeHerdr(t *testing.T) *fakeHerdr {
	t.Helper()
	// Under /tmp, not $TMPDIR: macOS limits unix socket paths to 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "hf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fakeHerdr{sock: filepath.Join(dir, "h.sock"), on: map[string]func(map[string]any) any{},
		screen: map[string]string{}, ansi: map[string]string{}}
	ln, err := herdr.Listen(f.sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.serveConn(c) // one at a time: the recorded order is the order the node made the calls
		}
	}()
	return f
}

func (f *fakeHerdr) serveConn(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(connDeadline))
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	_ = json.Unmarshal(line, &req)
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	call := herdrCall{Method: req.Method, Params: req.Params}
	if req.Method == "pane.send_input" {
		if cmd, ok := startFileCommand(str(req.Params["text"])); ok {
			call.Typed = str(req.Params["text"])
			call.Params["text"] = cmd
		}
	}
	f.mu.Lock()
	h := f.on[req.Method]
	var result any = map[string]any{}
	if h == nil && req.Method == "pane.read" {
		pane := str(req.Params["pane_id"])
		text, ok := f.ansi[pane]
		if !ok || req.Params["format"] != "ansi" {
			text = f.screen[pane]
		}
		result = map[string]any{"read": map[string]any{"text": text}}
	}
	f.log = append(f.log, call)
	hooks := slices.Clone(f.hooks)
	limit := f.replyLimit
	f.mu.Unlock()
	if h != nil {
		result = h(req.Params)
	}
	// Hooks run after the answer is chosen: a send_keys that changes the screen shows on the next read.
	for _, hook := range hooks {
		hook(call)
	}
	resp := map[string]any{"id": req.ID}
	if e, ok := result.(herdrFail); ok {
		resp["error"] = map[string]any{"code": e.Code, "message": e.Message}
	} else {
		resp["result"] = result
	}
	b, _ := json.Marshal(resp)
	b = append(b, '\n')
	if limit > 0 && len(b) > limit {
		b = b[:limit]
	}
	_, _ = c.Write(b)
}

func (f *fakeHerdr) limitReplies(bytes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replyLimit = bytes
}

func str(v any) string { s, _ := v.(string); return s }

// handle answers method with fn (a herdrFail result is sent as a herdr error).
func (f *fakeHerdr) handle(method string, fn func(p map[string]any) any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on[method] = fn
}

// reply answers method with a fixed result.
func (f *fakeHerdr) reply(method string, result any) {
	f.handle(method, func(map[string]any) any { return result })
}

// setScreen is what pane.read of pane returns from now on (when pane.read has no handler).
func (f *fakeHerdr) setScreen(pane, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screen[pane] = text
}

// setScreens gives pane a plain and a colored (format ansi) screen.
func (f *fakeHerdr) setScreens(pane, plain, ansi string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screen[pane], f.ansi[pane] = plain, ansi
}

// after runs fn after every call (outside the fake's lock, so fn may call setScreen).
func (f *fakeHerdr) after(fn func(c herdrCall)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks = append(f.hooks, fn)
}

// screenSteps shows screens[0] on pane, moving to the next one after each call that step matches.
func (f *fakeHerdr) screenSteps(pane string, step func(c herdrCall) bool, screens ...string) {
	var mu sync.Mutex
	at := 0
	f.setScreen(pane, screens[0])
	f.after(func(c herdrCall) {
		if str(c.Params["pane_id"]) != pane || !step(c) {
			return
		}
		mu.Lock()
		if at+1 < len(screens) {
			at++
		}
		s := screens[at]
		mu.Unlock()
		f.setScreen(pane, s)
	})
}

// calls returns every recorded call so far.
func (f *fakeHerdr) calls() []herdrCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.log)
}

// trace is the recorded calls in canonical form; with methods, only those.
func (f *fakeHerdr) trace(methods ...string) []string {
	var out []string
	for _, c := range f.calls() {
		if len(methods) == 0 || slices.Contains(methods, c.Method) {
			out = append(out, c.String())
		}
	}
	return out
}

// reset forgets the calls recorded so far.
func (f *fakeHerdr) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = nil
}

func isKeys(keys ...string) func(c herdrCall) bool {
	return func(c herdrCall) bool {
		if c.Method != "pane.send_keys" {
			return false
		}
		return len(keys) == 0 || slices.Equal(keysOf(c), keys)
	}
}

func keysOf(c herdrCall) []string {
	var out []string
	if ks, ok := c.Params["keys"].([]any); ok {
		for _, k := range ks {
			out = append(out, str(k))
		}
	}
	return out
}

// call builds the canonical form of an expected call.
func call(method string, params map[string]any) string {
	if params == nil {
		params = map[string]any{}
	}
	// Round-trip so numbers and slices print the way the recorder's decoded params do.
	b, _ := json.Marshal(params)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return herdrCall{Method: method, Params: m}.String()
}

// squeeze collapses runs of the same call into one (polls whose count depends on timing).
func squeeze(trace []string) []string {
	var out []string
	for _, c := range trace {
		if len(out) == 0 || out[len(out)-1] != c {
			out = append(out, c)
		}
	}
	return out
}

func assertTrace(t *testing.T, got, want []string) {
	t.Helper()
	if slices.Equal(got, want) {
		return
	}
	t.Fatalf("herdr calls differ\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
}

// snapshot is a session.snapshot result.
func snapshot(workspaces, tabs, agents, panes []any) map[string]any {
	nonNil := func(v []any) []any {
		if v == nil {
			return []any{}
		}
		return v
	}
	return map[string]any{"snapshot": map[string]any{"workspaces": nonNil(workspaces), "tabs": nonNil(tabs),
		"agents": nonNil(agents), "panes": nonNil(panes)}}
}

// claudeAgent is a herdr agent entry for a claude whose session herdr knows ("" leaves agent_session out).
func claudeAgent(session, status, pane, tab, ws, cwd, title string) map[string]any {
	return agentOf("claude", session, status, pane, tab, ws, cwd, title)
}

func agentOf(kind, session, status, pane, tab, ws, cwd, title string) map[string]any {
	a := map[string]any{"agent": kind, "agent_status": status, "pane_id": pane, "tab_id": tab, "workspace_id": ws,
		"cwd": cwd, "terminal_title_stripped": title, "terminal_title": title}
	if session != "" {
		a["agent_session"] = map[string]any{"kind": "id", "value": session}
	}
	return a
}

// ---- Node adapters ----------------------------------------------------------------------------------------------
//
// The tests drive the node through its request handler (what the hub calls) and see herdr only through the fake.
// Everything below touches node internals; when herdr moves behind a host interface, this block is what changes.

// herdrNode is a Node wired to a fresh fake herdr, with a temp home (~/.claude/projects exists, empty) and every map
// the herdr paths touch. The two package-level caches that outlive a test (the offline list and the compatible CLI's panes and
// statuses) are cleared before and after it; the other caches are keyed by the test's own temp paths.
func herdrNode(t *testing.T) (*Node, *fakeHerdr, string) {
	t.Helper()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	// herdr's config is the temp home's, not one the environment points at (herdr.WorktreesDir)
	t.Setenv("HERDR_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	f := newFakeHerdr(t)
	n, home := testNode(t)
	n.hosts, n.dir, n.bin = herdrHosts(&herdr.Client{Socket: f.sock}), filepath.Join(home, ".persona"), "/tmp/persona-test/personad"
	n.panes, n.panePIDs, n.modes, n.driving = map[string]string{}, map[string]panePID{}, map[string]sessionModes{}, map[string]bool{}
	n.files, n.metas, n.bgScans = map[string]*transcript.File{}, map[string]quickMeta{}, map[string]*bgScan{}
	for _, d := range []string{n.dir, filepath.Join(home, ".claude", "projects")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resetGlobalCaches := func() {
		offlineCache.Lock()
		offlineCache.at, offlineCache.list = time.Time{}, nil
		offlineCache.Unlock()
		traexCache.Lock()
		traexCache.panes, traexCache.status = map[int]string{}, map[string]traexState{}
		traexCache.Unlock()
	}
	resetGlobalCaches()
	t.Cleanup(resetGlobalCaches)
	withFirstPromptWaits(t, 400*time.Millisecond, 3*time.Second)
	t.Cleanup(n.firstWait.Wait) // before the waits are put back: a check still going reads them
	t.Cleanup(n.engLists.Wait)  // before the caches are reset: an engine switch's list, still being built, reads them
	t.Cleanup(n.starts.close)   // before firstWait, caches and the fake socket: starts can still use all of them
	return n, f, home
}

// withFirstPromptWaits shortens how long a start waits for its first message to reach the transcript, and for the
// input box to come back before typing it again (firstprompt.go).
func withFirstPromptWaits(t *testing.T, wait, recover time.Duration) {
	oldWait, oldRecover, oldPoll := firstPromptWait, firstPromptRecover, firstPromptPoll
	firstPromptWait, firstPromptRecover, firstPromptPoll = wait, recover, 20*time.Millisecond
	t.Cleanup(func() { firstPromptWait, firstPromptRecover, firstPromptPoll = oldWait, oldRecover, oldPoll })
}

// request runs one hub request through the node's handler; the result comes back as decoded JSON.
func request(t *testing.T, n *Node, method string, params map[string]any) (map[string]any, error) {
	t.Helper()
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	res, err := n.handle(context.Background(), method, b)
	out := map[string]any{}
	if res != nil {
		rb, _ := json.Marshal(res)
		_ = json.Unmarshal(rb, &out)
	}
	return out, err
}

// listSessions is sessions.list: a fresh look at herdr and the sessions it gives.
func listSessions(t *testing.T, n *Node) []proto.Session {
	t.Helper()
	res, err := request(t, n, "sessions.list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	return decodeSessions(t, res["sessions"])
}

func decodeSessions(t *testing.T, v any) []proto.Session {
	t.Helper()
	b, _ := json.Marshal(v)
	var list []proto.Session
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

// cachedSessions is the list built from the last look at herdr (what pushes carry), asking herdr nothing.
func cachedSessions(n *Node) []proto.Session { return n.sessionList(context.Background(), false) }

// seedAgents makes herdr report agents (and nothing else) and lets the node see them, then forgets those calls.
func seedAgents(t *testing.T, n *Node, f *fakeHerdr, agents ...any) {
	t.Helper()
	f.reply("session.snapshot", snapshot(nil, nil, agents, nil))
	listSessions(t, n)
	f.reset()
}

// bindPane is what a hook event (or a start) tells the node: this pane runs this session.
func bindPane(n *Node, pane, id string) { n.bindPane(pane, id) }

// seedTraexPane stands for HERDR_PANE_ID in a live process's environment (read with ps / /proc otherwise).
func seedTraexPane(pid int, pane string) {
	traexCache.Lock()
	traexCache.panes[pid] = pane
	traexCache.Unlock()
}

// ageTraexStatus makes every remembered a compatible CLI status older than its 2 s cache.
func ageTraexStatus() {
	traexCache.Lock()
	for p, s := range traexCache.status {
		s.at = s.at.Add(-time.Minute)
		traexCache.status[p] = s
	}
	traexCache.Unlock()
}

// withSuggestReads shortens when the screen is read for a suggestion after a turn.
func withSuggestReads(t *testing.T, at ...time.Duration) {
	old := suggestReads
	suggestReads = at
	t.Cleanup(func() { suggestReads = old })
}

// withSubmitGrace shortens how long a prompt may sit in the box before Enter is pressed for it.
func withSubmitGrace(t *testing.T, grace time.Duration) {
	g := submitGrace
	submitGrace = grace
	t.Cleanup(func() { submitGrace = g })
}

// withSubmitWindow shortens how long a send waits for the transcript to take its prompt, and how often it looks.
func withSubmitWindow(t *testing.T, window, poll time.Duration) {
	w, p := submitWindow, submitPoll
	submitWindow, submitPoll = window, poll
	t.Cleanup(func() { submitWindow, submitPoll = w, p })
}

// hubPush is one notification the node sent the hub.
type hubPush struct {
	Event  string          `json:"event"`
	Params json.RawMessage `json:"params"`
}

// pollRepo keeps optional Git decoration stable before a poll test changes its transcript or workflow. Otherwise
// a cold Git lookup can produce an unrelated sessions.changed on the next tick.
func pollRepo(t *testing.T) string {
	t.Helper()
	cwd := physicalTempDir(t)
	writeGitFile(t, filepath.Join(cwd, ".git", "HEAD"), "ref: refs/heads/main\n")
	if got := awaitGitPlace(t, gitPlaces, cwd); got != (gitPlace{cwd, cwd, "main"}) {
		t.Fatalf("poll repository: %+v", got)
	}
	return cwd
}

// pollNode runs the node's herdr poll loop (no event stream: a snapshot every tick) with a hub connection, and
// returns what it pushes; stop ends the loop and waits until it has returned.
func pollNode(t *testing.T, n *Node) (pushes <-chan hubPush, stop func()) {
	t.Helper()
	out, pw := hubPushes(n)
	n.events = newHerdrEvents()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.pollAgents(ctx); close(done) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
			pw.Close()
		})
	}
	t.Cleanup(stop)
	return out, stop
}

// hubPushes gives the node a hub connection and returns what it pushes there; closing pw ends it.
func hubPushes(n *Node) (<-chan hubPush, *io.PipeWriter) {
	pr, pw := io.Pipe()
	n.peer = rpc.New("n", pw)
	out := make(chan hubPush, 64)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 1<<20), 1<<24)
		for sc.Scan() {
			var m hubPush
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				out <- m
			}
		}
	}()
	return out, pw
}

// rememberStarted is what the hub's state store says after a node restart: Persona started this session.
func rememberStarted(n *Node, id string) { n.state.put(id, sessionState{Launcher: "claude"}) }
