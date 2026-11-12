package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The hierarchy's members are keys of the row itself, each only when set, and come back as they went (#5120).
func TestSessionHierarchyWire(t *testing.T) {
	started := time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC)
	var s Session
	s.SessionID = "child"
	s.SetStartedAt(started)
	s.SetAskingParentSessionID("parent")
	s.SetFold("root", "", nil)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"started_at":"2026-11-12T08:30:00Z"`, `"asking_parent_session_id":"parent"`, `"folded_under":"root"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s has no %s", b, want)
		}
	}
	if strings.Contains(string(b), `"children"`) || strings.Contains(string(b), `"Hierarchy"`) {
		t.Errorf("%s: no children, and no key of the hierarchy's own", b)
	}
	var back Session
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, s) {
		t.Fatalf("round trip: %+v, want %+v", back.Hierarchy, s.Hierarchy)
	}

	var parent Session
	parent.SessionID = "root"
	parent.SetFold("", "", &SessionChildren{Total: 3, Working: 2})
	b, _ = json.Marshal(parent)
	if !strings.Contains(string(b), `"children":{"total":3,"working":2}`) || strings.Contains(string(b), "folded_under") {
		t.Fatalf("parent row: %s", b)
	}
	var none Session
	b, _ = json.Marshal(none)
	for _, key := range []string{"started_at", "asking_parent_session_id", "folded_under", "folded_as", "children", "workers"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("a row with no hierarchy writes %s: %s", key, b)
		}
	}
	if err := json.Unmarshal(b, &none); err != nil || none.Hierarchy != nil {
		t.Fatalf("a row with no hierarchy decodes to one: %+v %v", none.Hierarchy, err)
	}
}

// A tab's worker says why it folds under its lead, and the lead how many of the rows folded under it are its workers;
// both come back as they went, and a row that is neither writes neither key (#5180).
func TestSessionHierarchyTabTeamWire(t *testing.T) {
	var worker Session
	worker.SessionID = "worker"
	worker.SetFold("lead", FoldedAsWorker, nil)
	b, err := json.Marshal(worker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"folded_under":"lead","folded_as":"worker"`) || strings.Contains(string(b), `"children"`) {
		t.Fatalf("worker row: %s", b)
	}
	var lead Session
	lead.SessionID = "lead"
	lead.SetFold("", "", &SessionChildren{Total: 3, Working: 1, NeedsYou: 1, Workers: 2})
	lb, _ := json.Marshal(lead)
	if !strings.Contains(string(lb), `"children":{"total":3,"working":1,"needs_you":1,"workers":2}`) || strings.Contains(string(lb), "folded_as") {
		t.Fatalf("lead row: %s", lb)
	}
	for _, c := range []struct {
		in   []byte
		want Session
	}{{b, worker}, {lb, lead}} {
		var back Session
		if err := json.Unmarshal(c.in, &back); err != nil || !reflect.DeepEqual(back, c.want) {
			t.Fatalf("round trip of %s: %+v %v", c.in, back.Hierarchy, err)
		}
	}
	var child Session
	child.SetFold("lead", "", &SessionChildren{Total: 1})
	if cb, _ := json.Marshal(child); strings.Contains(string(cb), "folded_as") || strings.Contains(string(cb), "workers") {
		t.Fatalf("a child folded under a lead: %s", cb)
	}
	ix, _ := json.Marshal(Interaction{ID: "i1", FoldedUnder: "lead", FoldedAs: FoldedAsWorker})
	if !strings.Contains(string(ix), `"folded_under":"lead","folded_as":"worker"`) {
		t.Fatalf("interaction: %s", ix)
	}
	if ix, _ := json.Marshal(Interaction{ID: "i1"}); strings.Contains(string(ix), "folded_as") {
		t.Fatalf("an interaction of no worker: %s", ix)
	}
}

// A row decoded over one that had a hierarchy keeps what the bytes do not say, as every other field does, and never
// writes into the hierarchy the old row shares with its copies.
func TestSessionHierarchyDecodeKeepsSharedOne(t *testing.T) {
	var old Session
	old.SetFold("root", "", &SessionChildren{Total: 1})
	old.SetStartedAt(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	copyOf := old
	shared := old.Hierarchy
	if err := json.Unmarshal([]byte(`{"folded_under":"other","children":{"total":5}}`), &copyOf); err != nil {
		t.Fatal(err)
	}
	if shared.FoldedUnder != "root" || shared.Children.Total != 1 {
		t.Fatalf("decoding wrote into the shared hierarchy: %+v %+v", shared, shared.Children)
	}
	if copyOf.FoldedUnder() != "other" || copyOf.Children().Total != 5 || !copyOf.StartedAt().Equal(old.StartedAt()) {
		t.Fatalf("decoded %+v", copyOf.Hierarchy)
	}
	if err := json.Unmarshal([]byte(`{"folded_under":"","children":null,"started_at":"0001-01-01T00:00:00Z"}`), &copyOf); err != nil {
		t.Fatal(err)
	}
	if copyOf.Hierarchy != nil {
		t.Fatalf("an emptied hierarchy stays: %+v", copyOf.Hierarchy)
	}
}

// The setters never write into the hierarchy a row shares with its copies, and make none when nothing changes.
func TestSessionHierarchySetters(t *testing.T) {
	var a Session
	a.SetFold("", "", nil)
	a.SetAskingParentSessionID("")
	a.SetStartedAt(time.Time{})
	if a.Hierarchy != nil {
		t.Fatal("empty values made a hierarchy")
	}
	a.SetFold("root", "", nil)
	b := a
	b.SetFold("other", "", &SessionChildren{Total: 2})
	if a.FoldedUnder() != "root" || a.Children() != (SessionChildren{}) {
		t.Fatalf("a copy's setter changed the row: %+v", a.Hierarchy)
	}
	h := b.Hierarchy
	if b.SetFold("other", "", &SessionChildren{Total: 2}); b.Hierarchy != h {
		t.Fatal("an unchanged fold made a new hierarchy")
	}
	if n := testing.AllocsPerRun(100, func() { b.SetFold("other", "", &SessionChildren{Total: 2}) }); n != 0 {
		t.Fatalf("an unchanged fold allocates %v times", n)
	}
	if b.SetFold("other", FoldedAsWorker, &SessionChildren{Total: 2}); b.Hierarchy == h || h.FoldedAs != "" || b.FoldedAs() != FoldedAsWorker {
		t.Fatalf("folded_as alone changed: %+v (was %+v)", b.Hierarchy, h)
	}
	w := b.Hierarchy
	if b.SetFold("other", FoldedAsWorker, &SessionChildren{Total: 2}); b.Hierarchy != w {
		t.Fatal("an unchanged worker fold made a new hierarchy")
	}
	b.SetFold("other", "", &SessionChildren{Total: 2, Workers: 1})
	if b.FoldedAs() != "" || b.Children().Workers != 1 || w.Children.Workers != 0 {
		t.Fatalf("a rollup's workers: %+v (was %+v)", b.Hierarchy, w)
	}
	b.SetFold("", "", nil)
	if b.Hierarchy != nil {
		t.Fatalf("a cleared fold keeps %+v", b.Hierarchy)
	}
}

func TestFoldedState(t *testing.T) {
	for _, c := range []struct {
		state    string
		awaits   bool
		children *SessionChildren
		want     string
	}{
		{StateIdle, false, nil, StateIdle},
		{StateNeedsYou, false, &SessionChildren{Total: 1, Working: 1}, StateNeedsYou},
		{StateWorking, false, &SessionChildren{Total: 1, NeedsYou: 1}, StateNeedsYou},
		{StateIdle, true, &SessionChildren{Total: 2, NeedsYou: 1, Working: 1}, StateNeedsYou},
		{StateIdle, false, &SessionChildren{Total: 1, Working: 1}, StateWorking},
		{StateOffline, false, &SessionChildren{Total: 1, Working: 1}, StateWorking},
		{StateUnread, false, &SessionChildren{Total: 1, Working: 1}, StateUnread},
		{StateIdle, true, &SessionChildren{Total: 1, Working: 1}, StateIdle},
		{StateWorking, false, &SessionChildren{Total: 3}, StateWorking},
		{StateUnread, false, &SessionChildren{Total: 3}, StateUnread},
	} {
		s := Session{State: c.state, AwaitsReply: c.awaits}
		s.SetFold("", "", c.children)
		if got := s.FoldedState(); got != c.want {
			t.Errorf("%s (awaits %v) with %+v: %s, want %s", c.state, c.awaits, c.children, got, c.want)
		}
	}
}

func TestSessionIsChild(t *testing.T) {
	for _, c := range []struct {
		s    Session
		want bool
	}{
		{Session{SessionID: "a"}, false},
		{Session{SessionID: "a", RootSessionID: "a"}, false},
		{Session{SessionID: "a", RootSessionID: "r"}, true},
		{Session{SessionID: "a", IsSubagent: true}, true},
		{Session{SessionID: "a", ParentSessionID: "p"}, true},
		{Session{SessionID: "a", Hierarchy: &SessionHierarchy{AskingParentSessionID: "p"}}, true},
	} {
		if got := c.s.IsChild(); got != c.want {
			t.Errorf("%+v: %v", c.s, got)
		}
	}
}

func TestTokens32(t *testing.T) {
	if Tokens32(1_000_000) != 1_000_000 || Tokens32(1<<40) != 1<<31-1 || Tokens32(-1<<40) != -1<<31 {
		t.Fatal("Tokens32 does not saturate")
	}
}
