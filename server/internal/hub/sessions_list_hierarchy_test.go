package hub

import (
	"encoding/json"
	"net/http/httptest"
	neturl "net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/sessionlist"
)

// hierarchyHub is a hub whose machine "demo" lists a parent with three children folded under it (two levels), a row
// on its own and a child folded under a session the hub does not list; machine "other" lists a child of a session of
// the same id on "demo".
func hierarchyHub(t *testing.T) (*Hub, *Machine) {
	t.Helper()
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "demo"})
	other := newMachine(h, MachineConfig{ID: "other"})
	h.machines, h.byID["demo"], h.byID["other"] = []*Machine{m, other}, m, other
	at := func(n int) time.Time { return time.Unix(int64(1000+n), 0) }
	row := func(id, state string, n int, under string, kids *proto.SessionChildren) proto.Session {
		s := proto.Session{MachineID: "demo", SessionID: id, Title: id, State: state, Cwd: "/demo/app", UpdatedAt: at(n)}
		s.SetFold(under, kids)
		return s
	}
	m.sessions = []proto.Session{
		row("lead", proto.StateIdle, 1, "", &proto.SessionChildren{Total: 3, Working: 1, NeedsYou: 1}),
		row("mid", proto.StateWorking, 2, "lead", nil),
		row("leaf", proto.StateNeedsYou, 3, "lead", nil),
		row("leaf2", proto.StateUnread, 4, "lead", nil),
		row("solo", proto.StateWorking, 5, "", nil),
		row("stray", proto.StateIdle, 6, "hidden", nil), // its parent is not listed: a row of its own
	}
	x := row("x", proto.StateIdle, 7, "lead", nil)
	x.MachineID = "other"
	other.sessions = []proto.Session{x}
	return h, m
}

func pageIDs(page listTestPage) []string {
	var out []string
	for _, s := range page.Sessions {
		out = append(out, s.MachineID+"/"+s.SessionID)
	}
	return out
}

func hierarchyCounts(t *testing.T, h *Hub, query string) sessionlist.Counts {
	t.Helper()
	w := httptest.NewRecorder()
	h.listSessions(w, httptest.NewRequest("GET", "/api/v1/sessions?counts=1&"+query, nil))
	var c sessionlist.Counts
	if err := json.Unmarshal(w.Body.Bytes(), &c); w.Code != 200 || err != nil {
		t.Fatalf("counts %s: %d %s %v", query, w.Code, w.Body, err)
	}
	return c
}

// roots=1 leaves the children folded under a listed parent out of the rows, the total and every count, and counts
// and filters the parent by its folded state; parent_session_id lists one parent's children, all depths (#5120).
func TestRuntimeListHierarchy(t *testing.T) {
	h, _ := hierarchyHub(t)
	roots := runtimePage(t, h, "summary=1&limit=50&roots=1")
	if got := pageIDs(roots); !slices.Equal(got, []string{"other/x", "demo/stray", "demo/solo", "demo/lead"}) || roots.Total != 4 {
		t.Fatalf("roots: %v (total %d)", got, roots.Total)
	}
	lead := roots.Sessions[3]
	if lead.Children() != (proto.SessionChildren{Total: 3, Working: 1, NeedsYou: 1}) || lead.State != proto.StateIdle {
		t.Fatalf("the parent's summary row: state %s, children %+v", lead.State, lead.Children())
	}
	c := hierarchyCounts(t, h, "roots=1")
	if c.Total != 4 || c.State[proto.StateNeedsYou] != 1 || c.State[proto.StateWorking] != 1 || c.State[proto.StateIdle] != 2 ||
		c.State[proto.StateUnread] != 0 || c.Machine["demo"] != 3 || c.Project["/demo/app"] != 4 {
		t.Fatalf("roots counts: %+v", c)
	}
	if page := runtimePage(t, h, "summary=1&roots=1&state=needs_you"); !slices.Equal(pageIDs(page), []string{"demo/lead"}) {
		t.Fatalf("roots, needs you: %v", pageIDs(page))
	}
	if page := runtimePage(t, h, "summary=1&roots=1&state=idle&machine_id=demo"); !slices.Equal(pageIDs(page), []string{"demo/stray"}) {
		t.Fatalf("roots, idle: %v", pageIDs(page))
	}
	under := runtimePage(t, h, "summary=1&machine_id=demo&parent_session_id=lead")
	if got := pageIDs(under); !slices.Equal(got, []string{"demo/leaf2", "demo/leaf", "demo/mid"}) || under.Total != 3 {
		t.Fatalf("under lead: %v (total %d)", got, under.Total)
	}
	if c := hierarchyCounts(t, h, "machine_id=demo&parent_session_id=lead"); c.Total != 3 || c.State[proto.StateNeedsYou] != 1 {
		t.Fatalf("under lead counts: %+v", c)
	}
	if page := runtimePage(t, h, "summary=1&machine_id=other&parent_session_id=lead"); page.Total != 0 {
		t.Fatalf("another machine's session of the same id: %v", pageIDs(page))
	}
	// with_counts gives the same view's counts.
	w := httptest.NewRecorder()
	h.listSessions(w, httptest.NewRequest("GET", "/api/v1/sessions?summary=1&roots=1&with_counts=1", nil))
	var both struct {
		Total  int                `json:"total"`
		Counts sessionlist.Counts `json:"counts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &both); err != nil || both.Total != 4 || both.Counts.Total != 4 || both.Counts.State[proto.StateNeedsYou] != 1 {
		t.Fatalf("with_counts: %s %v", w.Body, err)
	}
	if !slices.Contains(h.features(), featureSessionsHierarchy) || slices.Contains(proto.Features, featureSessionsHierarchy) {
		t.Error("sessions_hierarchy must be advertised by the hub")
	}
}

// Without the keys nothing changes: every row is listed and counted by its own state.
func TestRuntimeListHierarchyOlderQueries(t *testing.T) {
	h, _ := hierarchyHub(t)
	all := runtimePage(t, h, "summary=1&limit=50")
	if all.Total != 7 || len(all.Sessions) != 7 {
		t.Fatalf("all: %v (total %d)", pageIDs(all), all.Total)
	}
	c := hierarchyCounts(t, h, "")
	if c.Total != 7 || c.State[proto.StateIdle] != 3 || c.State[proto.StateWorking] != 2 || c.State[proto.StateNeedsYou] != 1 || c.State[proto.StateUnread] != 1 {
		t.Fatalf("counts: %+v", c)
	}
	if page := runtimePage(t, h, "summary=1&state=needs_you"); !slices.Equal(pageIDs(page), []string{"demo/leaf"}) {
		t.Fatalf("needs you: %v", pageIDs(page))
	}
	for _, q := range []string{"roots=0", "roots=false"} {
		if page := runtimePage(t, h, "summary=1&limit=50&"+q); page.Total != 7 {
			t.Fatalf("%s: %v", q, pageIDs(page))
		}
	}
	// A list with no hierarchy says nothing of one.
	plain := newTestHub(t)
	m := newMachine(plain, MachineConfig{ID: "demo"})
	plain.machines, plain.byID["demo"] = []*Machine{m}, m
	m.sessions = []proto.Session{{MachineID: "demo", SessionID: "a", State: proto.StateIdle, UpdatedAt: time.Unix(1, 0)}}
	for _, q := range []string{"summary=1&limit=50", "summary=0&limit=50"} {
		w := httptest.NewRecorder()
		plain.listSessions(w, httptest.NewRequest("GET", "/api/v1/sessions?"+q, nil))
		for _, key := range []string{"folded_under", "children", "started_at", "asking_parent_session_id"} {
			if strings.Contains(w.Body.String(), `"`+key+`"`) {
				t.Fatalf("%s writes %s: %s", q, key, w.Body)
			}
		}
	}
}

// parent_session_id needs machine_id, both keys take one value, roots a boolean; a cursor and a delta are bound to them.
func TestRuntimeListHierarchyValidationAndBinding(t *testing.T) {
	h, m := hierarchyHub(t)
	for _, query := range []string{"parent_session_id=lead", "parent_session_id=&machine_id=demo", "roots=yes",
		"roots=1&roots=0", "machine_id=demo&parent_session_id=a&parent_session_id=b", "roots=1&machine_id=demo&parent_session_id=lead",
		"counts=1&roots=1&machine_id=demo&parent_session_id=lead"} {
		w := httptest.NewRecorder()
		h.listSessions(w, httptest.NewRequest("GET", "/api/v1/sessions?"+query, nil))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_query") {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body)
		}
	}
	first := runtimePage(t, h, "summary=1&limit=1&roots=1")
	if !first.HasMore || first.Cursor == "" {
		t.Fatalf("first: %+v", first)
	}
	for _, query := range []string{"summary=1&limit=1&cursor=", "summary=1&limit=1&machine_id=demo&parent_session_id=lead&cursor="} {
		w := httptest.NewRecorder()
		h.listSessions(w, httptest.NewRequest("GET", "/api/v1/sessions?"+query+neturl.QueryEscape(first.Cursor), nil))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_query") {
			t.Fatalf("a roots cursor read %s: %d %s", query, w.Code, w.Body)
		}
	}
	if next := runtimePage(t, h, "summary=1&limit=1&roots=1&cursor="+neturl.QueryEscape(first.Cursor)); next.Total != 4 {
		t.Fatalf("next: %+v", next)
	}
	// mid comes out of its parent (its parent let it go): the roots delta adds it, the parent's delta removes it.
	under := runtimePage(t, h, "summary=1&machine_id=demo&parent_session_id=lead")
	m.mu.Lock()
	m.sessions = slices.Clone(m.sessions)
	m.sessions[1].SetFold("", nil)
	m.sessions[0].SetFold("", &proto.SessionChildren{Total: 2, NeedsYou: 1})
	m.mu.Unlock()
	h.sessionsChanged(m)
	delta := runtimePage(t, h, "summary=1&roots=1&since="+neturl.QueryEscape(first.Revision))
	if got := pageIDs(delta); !slices.Contains(got, "demo/mid") || delta.Total != 5 || len(delta.Remove) != 0 {
		t.Fatalf("roots delta: %v remove %v total %d", got, delta.Remove, delta.Total)
	}
	gone := runtimePage(t, h, "summary=1&machine_id=demo&parent_session_id=lead&since="+neturl.QueryEscape(under.Revision))
	if !slices.Equal(gone.Remove, []string{runtimeListID("demo", "mid")}) || gone.Total != 2 {
		t.Fatalf("under delta: %v remove %v total %d", pageIDs(gone), gone.Remove, gone.Total)
	}
}
