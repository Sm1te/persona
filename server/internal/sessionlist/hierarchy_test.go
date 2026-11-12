package sessionlist

import (
	"net/url"
	"testing"
)

func TestParseHierarchy(t *testing.T) {
	for _, key := range []string{"roots", "parent_session_id"} {
		if !Enabled(url.Values{key: {"1"}}) {
			t.Errorf("%s did not enable indexed behavior", key)
		}
	}
	o, err := Parse(url.Values{"roots": {"1"}})
	if err != nil || !o.Roots || o.Under != "" {
		t.Fatalf("roots: %+v %v", o, err)
	}
	o, err = Parse(url.Values{"parent_session_id": {"lead/one"}, "machine_id": {"m"}, "roots": {"false"}})
	if err != nil || o.Roots || o.Under != "lead/one" || o.Machine != "m" {
		t.Fatalf("parent: %+v %v", o, err)
	}
	for _, bad := range []url.Values{
		{"parent_session_id": {"lead"}}, {"parent_session_id": {""}, "machine_id": {"m"}}, {"roots": {"yes"}},
		{"roots": {"1", "1"}}, {"parent_session_id": {"a", "b"}, "machine_id": {"m"}},
		{"roots": {"1"}, "parent_session_id": {"lead"}, "machine_id": {"m"}}, // never a root: always an empty page
	} {
		if _, err := Parse(bad); err == nil || err.(*Error).Code != "invalid_query" {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if _, err := validate(Options{Under: "lead"}); err == nil {
		t.Error("an adapter's Under without Machine was taken")
	}
	if _, err := validate(Options{Under: "lead", Machine: "m", Roots: true}); err == nil {
		t.Error("an adapter's Under with Roots was taken")
	}
}

// Roots leaves the folded items out of rows, totals and every count and counts the rest by their folded state (its
// background-waiting count only for an item none of whose children works or waits); Under selects one root's items.
func TestQueryHierarchy(t *testing.T) {
	var index Index
	lead := testItem("lead", 1)
	lead.State, lead.BackgroundWaiting, lead.Fold = "working", true, &Fold{State: "needs_you"}
	kid := testItem("kid", 2)
	kid.State, kid.Fold = "needs_you", &Fold{Root: "lead", State: "needs_you"}
	solo := testItem("solo", 3)
	solo.BackgroundWaiting, solo.Fold = true, &Fold{State: "working", BackgroundWaiting: true}
	index.Replace("machine-a", []Item{lead, kid, solo})

	all := mustQuery(t, &index, Options{Limit: 10})
	roots := mustQuery(t, &index, Options{Limit: 10, Roots: true})
	if all.Total != 3 || roots.Total != 2 || len(roots.Sessions) != 2 {
		t.Fatalf("all %d, roots %d %v", all.Total, roots.Total, roots.Sessions)
	}
	c, _ := index.Counts(Options{})
	r, _ := index.Counts(Options{Roots: true})
	if c.State["working"] != 2 || c.State["needs_you"] != 1 || c.BackgroundWaiting != 2 {
		t.Fatalf("counts: %+v", c)
	}
	if r.Total != 2 || r.State["needs_you"] != 1 || r.State["working"] != 1 || r.BackgroundWaiting != 1 || r.Machine["machine-a"] != 2 {
		t.Fatalf("roots counts: %+v", r)
	}
	if p := mustQuery(t, &index, Options{Limit: 10, Roots: true, State: "needs_you"}); p.Total != 1 {
		t.Fatalf("roots needs_you: %+v", p)
	}
	if p := mustQuery(t, &index, Options{Limit: 10, State: "needs_you"}); p.Total != 1 || p.Sessions[0].(testPayload).ID != "kid" {
		t.Fatalf("needs_you: %+v", p)
	}
	if p := mustQuery(t, &index, Options{Limit: 10, Machine: "machine-a", Under: "lead"}); p.Total != 1 || p.Sessions[0].(testPayload).ID != "kid" {
		t.Fatalf("under: %+v", p)
	}
	if p := mustQuery(t, &index, Options{Limit: 10, Machine: "machine-b", Under: "lead"}); p.Total != 0 {
		t.Fatalf("under on another machine: %+v", p)
	}
	page := mustQuery(t, &index, Options{Limit: 1, Roots: true})
	if _, err := index.Query(Options{Limit: 1, Cursor: page.NextCursor}); err == nil {
		t.Fatal("a roots cursor read without roots")
	}
}
