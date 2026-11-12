package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func termClient(h *Hub, role string) *client {
	return &client{hub: h, out: make(chan []byte, 64), subs: map[[2]string]bool{}, role: role}
}

func say(h *Hub, c *client, v map[string]any) {
	b, _ := json.Marshal(v)
	h.onClientMessage(context.Background(), c, b)
}

// firstTerminalError is the error of the first terminal message a client was sent ("" when none).
func firstTerminalError(c *client) string {
	for {
		select {
		case b := <-c.out:
			var m struct {
				Type  string `json:"type"`
				Error string `json:"error"`
			}
			_ = json.Unmarshal(b, &m)
			if m.Type == "terminal" && m.Error != "" {
				return m.Error
			}
		default:
			return ""
		}
	}
}

// A pane is a shell on the computer: only a full device opens its terminal, for watching as well as driving. An
// answer-only device (the watch) still watches the terminal of an agent session as before.
func TestPaneTerminalNeedsAFullDevice(t *testing.T) {
	tn := newTermNode(t)
	h := tn.hub
	answer, full := termClient(h, roleAnswer), termClient(h, roleFull)

	say(h, answer, map[string]any{"type": "terminal_subscribe", "machine_id": "m1", "session_id": "pane:w1:p2"})
	if got := firstTerminalError(answer); !strings.Contains(got, "forbidden") {
		t.Errorf("an answer-only device was told %q", got)
	}
	h.terms.mu.Lock()
	k := len(h.terms.relays)
	h.terms.mu.Unlock()
	if k != 0 {
		t.Fatalf("a relay was made for a device that may not watch: %d", k)
	}

	// Before: an agent session's terminal is still watchable by it.
	say(h, answer, map[string]any{"type": "terminal_subscribe", "machine_id": "m1", "session_id": "s1"})
	if got := tn.await(t, 1); got[0] != "observe" {
		t.Fatalf("node log = %q", got)
	}

	// A full device opens the pane: the node is asked to watch it by its address.
	say(h, full, map[string]any{"type": "terminal_subscribe", "machine_id": "m1", "session_id": "pane:w1:p2"})
	if got := tn.await(t, 2); got[1] != "observe" {
		t.Fatalf("node log = %q", got)
	}
	say(h, full, map[string]any{"type": "terminal_unsubscribe", "machine_id": "m1", "session_id": "pane:w1:p2"})
	say(h, answer, map[string]any{"type": "terminal_unsubscribe", "machine_id": "m1", "session_id": "s1"})
}

func TestPaneScreenReadsNeedAFullDevice(t *testing.T) {
	var reached []string
	next := func(w http.ResponseWriter, r *http.Request) { reached = append(reached, r.PathValue("sid")) }
	guard := paneAddressFullOnly(next)
	call := func(role, sid string) int {
		r := httptest.NewRequest("GET", "/x", nil)
		r.SetPathValue("sid", sid)
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: role, ID: "dev"}))
		w := httptest.NewRecorder()
		guard(w, r)
		return w.Code
	}
	if code := call(roleAnswer, "pane:w1:p2"); code != http.StatusForbidden {
		t.Errorf("answer-only device read a shell: %d", code)
	}
	if code := call(roleExtension, "pane:w1:p2"); code != http.StatusForbidden {
		t.Errorf("an extension token read a shell: %d", code)
	}
	if call(roleFull, "pane:w1:p2") != http.StatusOK || call(roleExtension, "s1") != http.StatusOK {
		t.Error("a full device's read of a pane, or an extension's read of an agent session, was refused")
	}
	if strings.Join(reached, ",") != "pane:w1:p2,s1" {
		t.Errorf("reached = %v", reached)
	}
}

func TestNewShellIsAFullDevicesAndNamesNoDirectory(t *testing.T) {
	tn := newTermNode(t)
	h := tn.hub
	m := h.byID["m1"]
	post := func(role, body string) int {
		r := httptest.NewRequest("POST", "/api/v1/machines/m1/panes", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: role, ID: "dev"}))
		w := httptest.NewRecorder()
		h.newShell(w, r, m)
		return w.Code
	}
	if code := post(roleAnswer, `{"workspace_id":"w1"}`); code != http.StatusForbidden {
		t.Errorf("answer-only device opened a shell: %d", code)
	}
	if code := post(roleFull, `{}`); code != http.StatusBadRequest {
		t.Errorf("no workspace: %d", code)
	}
	if code := post(roleFull, `{"workspace_id":"w1","cwd":"/etc"}`); code != http.StatusOK {
		t.Errorf("full device: %d", code)
	}
	if got := tn.await(t, 1); len(got) != 1 || got[0] != "panes.new" {
		t.Errorf("node log = %q", got)
	}
}

// The routes are not on the lists of what an answer-only device (or a link) may call.
func TestPaneRoutesAreNotForAnswerOnlyDevices(t *testing.T) {
	for _, tc := range [][2]string{
		{http.MethodGet, "/api/v1/machines/m1/panes"}, {http.MethodPost, "/api/v1/machines/m1/panes"},
		{http.MethodGet, "/api/v1/sessions/m1/pane:w1:p2/screen"}, {http.MethodPost, "/api/v1/sessions/m1/pane:w1:p2/screen/before"},
	} {
		if allowed(principal{ID: "dev_watch", Role: roleAnswer}, tc[0], tc[1]) {
			t.Errorf("an answer-only device may %s %s", tc[0], tc[1])
		}
	}
}

// ── the menu actions (close / split / rename / run an agent / git) ──────────────────────────────────────────────

var actionBodies = map[string]string{
	"panes/close":      `{"pane":"w1:p2"}`,
	"panes/split":      `{"pane":"w1:p2","direction":"right"}`,
	"panes/agent":      `{"pane":"w1:p2","launcher":"claude"}`,
	"panes/git":        `{"pane":"w1:p2","op":"fetch"}`,
	"tabs/close":       `{"tab_id":"w1:t2"}`,
	"tabs/rename":      `{"tab_id":"w1:t2","label":"build"}`,
	"worktree/prepare": `{"workspace_id":"w1"}`,
	"worktree/source":  `{"workspace_id":"w1","kind":"orca"}`,
	"worktree/preset":  `{"workspace_id":"w1","kind":"orca","name":"UI","directories":"src"}`,
	"worktree/delete":  `{"workspace_id":"w1","kind":"orca","preview":true}`,
	"worktree/rename":  `{"workspace_id":"w1","kind":"orca","name":"Renamed tree"}`,
	"worktree/create":  `{"workspace_id":"w1","branch":"feat/x","path":"/tmp/wt/feat-x"}`,
	"worktree/add":     `{"cwd":"/tmp/proj","name_hint":"fix the crash"}`,
	"worktree/status":  `{"cwd":"/tmp/wt/persona-fix-the-crash"}`,
	"worktree/remove":  `{"path":"/tmp/wt/persona-fix-the-crash","delete_branch":true}`,
	// #4754 Close finished terminals: the panes the user confirmed
	"panes/close_finished": `{"panes":"w1:p4,w2:p1"}`,
}

func postAction(h *Hub, role, path, body string) int {
	r := httptest.NewRequest("POST", "/api/v1/machines/m1/"+path, strings.NewReader(body))
	r.SetPathValue("mid", "m1")
	r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: role, ID: "dev"}))
	w := httptest.NewRecorder()
	h.nodeAction(nodeActions[path])(w, r)
	return w.Code
}

func TestNodeActionsAreMountedOnTheHTTPRouter(t *testing.T) {
	tn := newTermNode(t)
	mux := http.NewServeMux()
	tn.hub.paneRoutes(mux)
	for path, body := range actionBodies {
		r := httptest.NewRequest("POST", "/api/v1/machines/m1/"+path, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), principalKey{}, principal{Role: roleFull, ID: "dev"}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("mounted route %s: HTTP %d", path, w.Code)
		}
	}
}

// Every one of them changes a computer: answer-only devices and extension tokens are refused before the node hears of
// it, a full device gets through.
func TestNodeActionsNeedAFullDevice(t *testing.T) {
	if len(actionBodies) != len(nodeActions) {
		t.Fatalf("a menu action has no test body: %d bodies for %d routes", len(actionBodies), len(nodeActions))
	}
	tn := newTermNode(t)
	for path, body := range actionBodies {
		for _, role := range []string{roleAnswer, roleExtension} {
			if code := postAction(tn.hub, role, path, body); code != http.StatusForbidden {
				t.Errorf("%s: %s was let through: %d", path, role, code)
			}
		}
	}
	if got := tn.await(t, 0); len(got) != 0 {
		t.Fatalf("the node heard of a refused request: %q", got)
	}
	for path, body := range actionBodies {
		if code := postAction(tn.hub, roleFull, path, body); code != http.StatusOK {
			t.Errorf("%s: full device = %d", path, code)
		}
	}
	want := map[string]bool{"panes.close": true, "panes.split": true, "panes.agent": true, "panes.git": true, "tabs.close": true, "tabs.rename": true,
		"worktree.source": true, "worktree.preset": true, "worktree.delete": true, "worktree.rename": true, "worktree.prepare": true, "worktree.create": true, "worktree.add": true, "worktree.status": true, "worktree.remove": true}
	for _, m := range tn.await(t, len(want)) {
		delete(want, m)
	}
	if len(want) != 0 {
		t.Errorf("never reached the node: %v", want)
	}
}

// What a request may carry is the named fields: a directory or a command in the body is dropped, a missing field is a
// 400 and the node is not called.
func TestNodeActionsPassOnlyTheirFields(t *testing.T) {
	tn := newTermNode(t)
	h := tn.hub
	if code := postAction(h, roleFull, "panes/git", `{"pane":"w1:p2","op":"push","cwd":"/etc","command":"rm -rf ~","force":"true"}`); code != http.StatusOK {
		t.Fatalf("git = %d", code)
	}
	tn.await(t, 1)
	tn.mu.Lock()
	got := tn.args["panes.git"]
	tn.mu.Unlock()
	if got != `{"op":"push","pane":"w1:p2"}` {
		t.Errorf("the node was sent %s", got)
	}
	// worktree/remove's switches (#6115): booleans only, and only when true; a string "true" is not one
	if code := postAction(h, roleFull, "worktree/remove", `{"path":"/tmp/wt/x","delete_branch":true,"force":"true","command":"rm -rf ~"}`); code != http.StatusOK {
		t.Fatalf("remove = %d", code)
	}
	tn.await(t, 1)
	tn.mu.Lock()
	got = tn.args["worktree.remove"]
	tn.mu.Unlock()
	if got != `{"delete_branch":true,"path":"/tmp/wt/x"}` {
		t.Errorf("the node was sent %s", got)
	}
	for path, body := range map[string]string{
		"panes/close": `{}`, "panes/split": `{"pane":"w1:p2"}`, "panes/agent": `{"pane":"w1:p2"}`, "panes/git": `{"pane":"w1:p2"}`,
		"tabs/close": `{"tab_id":7}`, "tabs/rename": `{"tab_id":"w1:t2"}`, "panes/close ": `not json`,
		"worktree/prepare": `{}`, "worktree/create": `{"workspace_id":"w1","branch":"feat/x"}`,
		"worktree/rename": `{"workspace_id":"w1"}`,
		"worktree/add":    `{"branch":"x"}`, "worktree/status": `{}`, "worktree/remove": `{"force":true}`,
	} {
		path = strings.TrimSpace(path)
		if code := postAction(h, roleFull, path, body); code != http.StatusBadRequest {
			t.Errorf("%s %s = %d", path, body, code)
		}
	}
	if got := tn.await(t, 2); len(got) != 2 { // the git call and the remove above
		t.Errorf("a refused request reached the node: %q", got)
	}
}

// Not on the lists of what an answer-only device may call, and credentials-only in the extension scopes.
func TestNodeActionRoutesAreFullCredentialsOnly(t *testing.T) {
	data, err := os.ReadFile("testdata/route_scopes.txt")
	if err != nil {
		t.Fatal(err)
	}
	for path := range nodeActions {
		url := "/api/v1/machines/m1/" + path
		if allowed(principal{ID: "dev_watch", Role: roleAnswer}, http.MethodPost, url) {
			t.Errorf("an answer-only device may POST %s", url)
		}
		if allowed(principal{ID: "dev_link", Role: roleLink}, http.MethodPost, url) {
			t.Errorf("a link may POST %s", url)
		}
		line := "-                    POST /api/v1/machines/{mid}/" + path + "\n"
		if !strings.Contains(string(data), line) {
			t.Errorf("route_scopes.txt has no full-credentials-only line for %s", path)
		}
	}
}

// #2431: "New worktree…" passes the workspace, the branch, the path and the host kind, and nothing else (no base ref, no
// force, no hook).
func TestWorktreeCreatePassesOnlyItsFields(t *testing.T) {
	tn := newTermNode(t)
	body := `{"workspace_id":"w1","kind":"herdr","branch":"feat/x","path":"/tmp/wt/feat-x","force":"true","base":"main","command":"rm -rf ~","cwd":"/etc"}`
	if code := postAction(tn.hub, roleFull, "worktree/create", body); code != http.StatusOK {
		t.Fatalf("create = %d", code)
	}
	tn.await(t, 1)
	tn.mu.Lock()
	got := tn.args["worktree.create"]
	tn.mu.Unlock()
	if got != `{"branch":"feat/x","kind":"herdr","path":"/tmp/wt/feat-x","workspace_id":"w1"}` {
		t.Errorf("the node was sent %s", got)
	}
	for _, role := range []string{roleAnswer, roleLink, roleExtension} {
		if code := postAction(tn.hub, role, "worktree/create", body); code != http.StatusForbidden {
			t.Errorf("%s: create = %d", role, code)
		}
		if code := postAction(tn.hub, role, "worktree/prepare", `{"workspace_id":"w1"}`); code != http.StatusForbidden {
			t.Errorf("%s: prepare = %d", role, code)
		}
	}
	if got := tn.await(t, 1); len(got) != 1 {
		t.Errorf("a refused request reached the node: %q", got)
	}
}

func TestAutomaticWorktreeCreateRequiresABooleanFlagAndFullCredentials(t *testing.T) {
	tn := newTermNode(t)
	for _, body := range []string{
		`{"workspace_id":"w1"}`, `{"workspace_id":"w1","auto_name":"true"}`,
		`{"workspace_id":"w1","auto_name":false}`, `{"auto_name":true}`,
	} {
		if code := postAction(tn.hub, roleFull, "worktree/create", body); code != http.StatusBadRequest {
			t.Fatalf("invalid automatic create %s = %d", body, code)
		}
	}
	body := `{"workspace_id":"w1","kind":"orca","parent_workspace_id":"folder","auto_name":true,"command":"ignored"}`
	for _, role := range []string{roleAnswer, roleLink, roleExtension} {
		if code := postAction(tn.hub, role, "worktree/create", body); code != http.StatusForbidden {
			t.Fatalf("%s: automatic create = %d", role, code)
		}
	}
	if code := postAction(tn.hub, roleFull, "worktree/create", body); code != http.StatusOK {
		t.Fatalf("automatic create = %d", code)
	}
	if got := tn.await(t, 1); len(got) != 1 {
		t.Fatalf("refused requests reached the node: %v", got)
	}
	tn.mu.Lock()
	got := tn.args["worktree.create"]
	tn.mu.Unlock()
	if got != `{"auto_name":true,"kind":"orca","parent_workspace_id":"folder","workspace_id":"w1"}` {
		t.Fatalf("automatic create fields: %s", got)
	}
}
