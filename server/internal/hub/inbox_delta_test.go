package hub

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// sinceManager answers since as a manager does (assistant/inbox_delta.go), from canned lists: cursor "c<n>" is the
// n-th; a cursor it holds gets the delta to now, any other the whole list.
type sinceManager struct {
	lists   [][]string // the items' ids (each item {"id":id,"title":id}) of each list it had, newest last
	fail    bool
	noSince bool // an older manager: the whole list, no cursor
	asked   []string
}

func (m *sinceManager) answer(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	m.asked = append(m.asked, since)
	if m.fail {
		http.Error(w, `{"error":"down"}`, http.StatusServiceUnavailable)
		return
	}
	now := m.lists[len(m.lists)-1]
	item := func(id string) json.RawMessage { return json.RawMessage(`{"id":"` + id + `","title":"` + id + `"}`) }
	out := map[string]any{"as_of": "t", "triage": map[string]any{}}
	items := []json.RawMessage{}
	if m.noSince {
		for _, id := range now {
			items = append(items, item(id))
		}
		out["items"] = items
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	out["cursor"] = "c" + string(rune('0'+len(m.lists)-1))
	n := -1
	if strings.HasPrefix(since, "c") && len(since) == 2 {
		n = int(since[1] - '0')
	}
	if n < 0 || n >= len(m.lists) {
		for _, id := range now {
			items = append(items, item(id))
		}
		out["items"] = items
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	old := m.lists[n]
	removed := []map[string]string{}
	for _, id := range old {
		if !slices.Contains(now, id) {
			removed = append(removed, map[string]string{"id": id})
		}
	}
	for _, id := range now {
		if !slices.Contains(old, id) {
			items = append(items, item(id))
		}
	}
	out["items"], out["delta"], out["removed"] = items, true, removed
	_ = json.NewEncoder(w).Encode(out)
}

type sinceAnswer struct {
	Cursor string `json:"cursor"`
	Delta  bool   `json:"delta"`
	Items  []struct {
		ID      string `json:"id"`
		Manager string `json:"manager"`
	} `json:"items"`
	Removed []struct {
		ID      string `json:"id"`
		Manager string `json:"manager"`
	} `json:"removed"`
}

func askSince(t *testing.T, managers map[string]*sinceManager, ids []string, since string) (sinceAnswer, bool) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/inbox?sources=all&since="+since, nil)
	w := httptest.NewRecorder()
	ok := answerSince(w, r, "items", ids, func(w http.ResponseWriter, r *http.Request, id string) {
		if r.URL.Query().Get("sources") != "all" {
			t.Fatalf("the manager was not asked the request's query: %s", r.URL.RawQuery)
		}
		managers[id].answer(w, r)
	})
	var a sinceAnswer
	if ok {
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
	} else if w.Body.Len() > 0 || w.Code != http.StatusOK {
		t.Fatal("answerSince wrote something and said to answer as without since")
	}
	return a, ok
}

func stampedIDs(a sinceAnswer) []string {
	var out []string
	for _, it := range a.Items {
		out = append(out, it.Manager+"/"+it.ID)
	}
	return out
}

// Each manager is asked with its part of the merged cursor; a delta from every one is a delta, stamped with its
// manager; one that cannot follow its cursor makes it the whole merged list, every manager asked again without one.
func TestMergedInboxSince(t *testing.T) {
	a := &sinceManager{lists: [][]string{{"1", "2"}}}
	b := &sinceManager{lists: [][]string{{"x"}}}
	managers := map[string]*sinceManager{"assistant": a, "m-b": b}
	order := []string{"assistant", "m-b"}

	whole, ok := askSince(t, managers, order, "")
	if !ok || whole.Delta || whole.Cursor == "" || !slices.Equal(stampedIDs(whole), []string{"assistant/1", "assistant/2", "m-b/x"}) {
		t.Fatalf("since empty: %v %+v", ok, whole)
	}
	if got := inboxCursors(whole.Cursor); got["assistant"] != "c0" || got["m-b"] != "c0" {
		t.Fatalf("merged cursor %q holds %v", whole.Cursor, got)
	}

	a.lists = append(a.lists, []string{"2", "3"}) // 1 gone, 3 new
	b.lists = append(b.lists, []string{"x", "y"})
	d, ok := askSince(t, managers, order, whole.Cursor)
	if !ok || !d.Delta || !slices.Equal(stampedIDs(d), []string{"assistant/3", "m-b/y"}) {
		t.Fatalf("delta: %v %+v", ok, d)
	}
	if len(d.Removed) != 1 || d.Removed[0].ID != "1" || d.Removed[0].Manager != "assistant" {
		t.Fatalf("removed %+v, want assistant/1", d.Removed)
	}
	if a.asked[len(a.asked)-1] != "c0" || b.asked[len(b.asked)-1] != "c0" {
		t.Fatalf("each manager asked with its own cursor: %v %v", a.asked, b.asked)
	}

	// m-b restarted: its cursor names nothing; the answer is the whole merged list, the assistant asked again too.
	b.lists = [][]string{{"x", "y", "z"}}
	re, ok := askSince(t, managers, order, d.Cursor)
	if !ok || re.Delta || !slices.Equal(stampedIDs(re), []string{"assistant/2", "assistant/3", "m-b/x", "m-b/y", "m-b/z"}) {
		t.Fatalf("after a restart: %v %+v", ok, re)
	}
	if a.asked[len(a.asked)-1] != "" {
		t.Fatalf("the assistant was not asked again for its whole list: %v", a.asked)
	}

	// A failing manager, or one that knows no cursors, and the request goes as without since.
	b.fail = true
	if _, ok := askSince(t, managers, order, re.Cursor); ok {
		t.Fatal("a failing manager was answered as a delta")
	}
	b.fail, b.noSince = false, true
	if _, ok := askSince(t, managers, order, re.Cursor); ok {
		t.Fatal("a manager without cursors was answered with one")
	}
}

func TestInboxCursorsRoundTrip(t *testing.T) {
	c := inboxCursor([]string{"assistant", "m<&>"}, []string{"e.1", "f.2"})
	if got := inboxCursors(c); got["assistant"] != "e.1" || got["m<&>"] != "f.2" || len(got) != 2 {
		t.Fatalf("%q → %v", c, got)
	}
	for _, bad := range []string{"", "x", "m1.!!", "m1." + "e30"} {
		if got := inboxCursors(bad); len(got) != 0 {
			t.Fatalf("%q read as %v", bad, got)
		}
	}
}
