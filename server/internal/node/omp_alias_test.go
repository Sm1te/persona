package node

import (
	"context"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
)

// ompStartHerdr answers the start of an omp in ws's pane w1:p2: the pane is reported as omp once the command was typed.
func ompStartHerdr(f *fakeHerdr) {
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	f.after(func(c herdrCall) {
		if c.Method == "pane.send_input" {
			f.reply("session.snapshot", snapshot(nil, nil, []any{ompPaneAgent("w1:p2", "/w", "")}, nil))
		}
	})
}

// withPiFamilyGrace shortens the wait for the extension's session id.
func withPiFamilyGrace(t *testing.T, grace time.Duration) {
	t.Helper()
	prev := piFamilyIDGrace
	piFamilyIDGrace = grace
	t.Cleanup(func() { piFamilyIDGrace = prev })
}

// The extension reports the new omp's session while the start waits: the start answers with the session id, not the
// pane's fallback row.
func TestOmpStartWaitsForTheExtension(t *testing.T) {
	withPiFamilyGrace(t, 3*time.Second)
	n, f, _, rh := piHerdrNode(t)
	// The extension lists the session in the pane the start is about to open (its hello comes before the tab exists).
	ompExtension(t, rh, "omp-1", map[string]any{"session_id": ompSID, "status": "idle", "pane_id": "w1:p2", "cwd": "/w"})
	ompStartHerdr(f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := n.startOtherAgent(ctx, n.hosts.Get(host.Herdr).(*herdrHost), n.home, "omp", ompAgent, "hi", "", "w1", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID != ompSID || s.Agent != "omp" {
		t.Fatalf("started %+v, want the extension's session", s)
	}
}

// No extension: after the short grace the start answers with the pane's fallback row, and omp runs.
func TestOmpStartFallsBackToThePanesID(t *testing.T) {
	withPiFamilyGrace(t, 200*time.Millisecond)
	n, f, _, _ := piHerdrNode(t)
	ompStartHerdr(f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	began := time.Now()
	s, err := n.startOtherAgent(ctx, n.hosts.Get(host.Herdr).(*herdrHost), n.home, "omp", ompAgent, "hi", "", "w1", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := otherSession(host.Agent{Agent: ompAgent, PaneID: "w1:p2"}); s.SessionID != want || s.Agent != "omp" {
		t.Fatalf("started %+v, want %s", s, want)
	}
	if took := time.Since(began); took > piFamilyIDGrace+2*time.Second {
		t.Fatalf("took %s with a %s grace", took, piFamilyIDGrace)
	}
}

// A fallback row the pane later names: calls on the fallback id reach the session, the list holds one row, and a pane
// that is gone takes the alias with it.
func TestOmpFallbackRowAliasesTheSession(t *testing.T) {
	n, f, _, rh := piHerdrNode(t)
	f.reply("session.snapshot", snapshot(nil, nil, []any{ompPaneAgent("w1:p5", "/w", "")}, nil))
	fallback := otherSession(host.Agent{Agent: ompAgent, PaneID: "w1:p5"})
	if s := onlyRow(t, listSessions(t, n), fallback); s.Agent != ompAgent {
		t.Fatalf("%+v", s)
	}

	rt := ompExtension(t, rh, "omp-2", map[string]any{"session_id": ompSID, "status": "idle", "pane_id": "w1:p5", "cwd": "/w"})
	list := listSessions(t, n)
	if s := onlyRow(t, list, ompSID); s.ReadOnlyReason != "" {
		t.Fatalf("%+v", s)
	}
	for _, s := range list {
		if s.SessionID == fallback {
			t.Fatalf("the fallback row is still listed: %+v", list)
		}
	}
	if _, err := request(t, n, "session.items", map[string]any{"session_id": fallback, "turns": 2}); err != nil {
		t.Fatal(err)
	}
	if len(rt.got("session.items")) != 1 {
		t.Fatalf("items on the fallback id did not reach the extension: %d", len(rt.got("session.items")))
	}

	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	_ = listSessions(t, n) // the pane is gone
	if got := n.resolveAlias(fallback); got != fallback {
		t.Fatalf("alias of a closed pane still points at %s", got)
	}
}
