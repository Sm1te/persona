package node

import (
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

const ompSID = "01a1c0a8-4c8b-7733-a605-0167c66a56e4"

// ompPaneAgent is an omp in a herdr pane as herdr lists it; with file, as herdr's omp integration reports its session.
func ompPaneAgent(pane, cwd, file string) map[string]any {
	a := map[string]any{"agent": "omp", "agent_status": proto.StatusIdle, "pane_id": pane, "tab_id": "w1:t3", "workspace_id": "w1",
		"cwd": cwd, "terminal_title": "omp - work"}
	if file != "" {
		a["agent_session"] = map[string]any{"agent": "omp", "kind": "path", "source": "herdr:omp", "value": file}
	}
	return a
}

// ompExtension is the Persona omp extension in an omp process: a runtime listing what it runs.
func ompExtension(t *testing.T, rh *RuntimeHost, runtime string, rows ...map[string]any) *fakeRuntime {
	t.Helper()
	rt := dialRuntimeAt(t, rh.path, map[string]any{"runtime": runtime, "agent": "omp", "name": "omp",
		"caps": map[string]any{"send": true, "interrupt": true, "rename": true}})
	_ = rt.peer.Notify("runtime.sessions", map[string]any{"sessions": rows})
	waitFor(t, "the omp extension's list", func() bool {
		for _, r := range rows {
			if rh.reg.ownerOf(r["session_id"].(string)) == nil {
				return false
			}
		}
		return true
	})
	return rt
}

// An omp in a herdr pane is one session, as a pi there is: herdr's row completed by the extension, its control at the
// extension, and no read-only notice.
func TestOmpInHerdrIsOneHerdrSession(t *testing.T) {
	n, f, _, rh := piHerdrNode(t)
	rt := ompExtension(t, rh, "omp-4242", map[string]any{"session_id": ompSID, "status": "working", "title": "fix the parser",
		"last_text": "Reading the grammar.", "model": "claude-x", "rev": 7, "pane_id": "w1:p3", "cwd": "/w"})
	seedAgents(t, n, f, ompPaneAgent("w1:p3", "/w", ""))

	s := onlyRow(t, listSessions(t, n), ompSID)
	if s.Agent != "omp" || s.PaneID != "w1:p3" || s.SpaceKind != "herdr" {
		t.Fatalf("not herdr's row: %+v", s)
	}
	if s.Title != "fix the parser" || s.LastText != "Reading the grammar." || s.Model != "claude-x" || s.Rev != 7 || s.ReadOnlyReason != "" {
		t.Fatalf("not completed by the extension: %+v", s)
	}
	if _, err := request(t, n, "session.send", map[string]any{"session_id": ompSID, "text": "go on", "client_msg_id": "m-1"}); err != nil {
		t.Fatal(err)
	}
	if len(rt.got("session.send")) != 1 {
		t.Fatalf("send did not go to the extension: %d", len(rt.got("session.send")))
	}
}

// Without Persona's extension an omp pane is listed by its pane: the terminal only, a send refused with its own code,
// Esc to interrupt.
func TestOmpInHerdrWithoutTheExtension(t *testing.T) {
	n, f, _, _ := piHerdrNode(t)
	seedAgents(t, n, f, ompPaneAgent("w1:p4", "/w", ""))
	id := otherSession(host.Agent{Agent: "omp", PaneID: "w1:p4"})
	s := onlyRow(t, listSessions(t, n), id)
	if s.Agent != "omp" || s.PaneID != "w1:p4" || s.ReadOnlyReason != piFamilyNoExtension(ompAgent) {
		t.Fatalf("%+v", s)
	}
	res, err := request(t, n, "session.items", map[string]any{"session_id": id, "turns": 2})
	if err != nil || !strings.Contains(string(mustJSON(res)), "omp install") {
		t.Fatalf("items %v %v", res, err)
	}
	_, err = request(t, n, "session.send", map[string]any{"session_id": id, "text": "hi", "client_msg_id": "m-2"})
	if !isRefusal(err, "omp_no_extension") {
		t.Fatalf("send: %v", err)
	}
	f.reset()
	if _, err := request(t, n, "session.interrupt", map[string]any{"session_id": id}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.trace(), "\n"); strings.Contains(got, "agent.prompt") || !strings.Contains(got, "escape") {
		t.Fatalf("herdr calls: %v", f.trace())
	}
}

// The extension connects after the pane was listed: the pane's row moves to the session's id, and only one row is left.
func TestOmpRowMovesToTheExtensionsSession(t *testing.T) {
	n, f, _, rh := piHerdrNode(t)
	seedAgents(t, n, f, ompPaneAgent("w1:p5", "/w", ""))
	fallback := otherSession(host.Agent{Agent: "omp", PaneID: "w1:p5"})
	onlyRow(t, listSessions(t, n), fallback)

	ompExtension(t, rh, "omp-4243", map[string]any{"session_id": ompSID, "status": "idle", "title": "later",
		"pane_id": "w1:p5", "cwd": "/w"})
	list := listSessions(t, n)
	if s := onlyRow(t, list, ompSID); s.Title != "later" || s.ReadOnlyReason != "" {
		t.Fatalf("%+v", s)
	}
	for _, s := range list {
		if s.SessionID == fallback {
			t.Fatalf("the fallback row is still listed: %+v", list)
		}
	}
}
