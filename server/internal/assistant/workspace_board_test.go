package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/workspaceboard"
)

type memoryBoardStore struct {
	Store
	mu        sync.Mutex
	state     workspaceboard.State
	snapshots map[string]db.WorkspaceSnapshotRow
}

func newMemoryBoardStore() *memoryBoardStore {
	return &memoryBoardStore{state: workspaceboard.Default(), snapshots: map[string]db.WorkspaceSnapshotRow{}}
}
func (s *memoryBoardStore) WorkspaceBoard(context.Context) (workspaceboard.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := json.Marshal(s.state)
	var out workspaceboard.State
	_ = json.Unmarshal(raw, &out)
	return out, nil
}
func (s *memoryBoardStore) SaveWorkspaceBoard(_ context.Context, expected int64, next workspaceboard.State) (workspaceboard.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != expected {
		return workspaceboard.State{}, db.ErrWorkspaceBoardConflict
	}
	next.Revision = expected + 1
	s.state = next
	return next, nil
}
func (s *memoryBoardStore) ListWorkspaceSnapshots(context.Context) ([]db.WorkspaceSnapshotRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []db.WorkspaceSnapshotRow{}
	for _, v := range s.snapshots {
		v.Data = append([]byte(nil), v.Data...)
		out = append(out, v)
	}
	return out, nil
}
func (s *memoryBoardStore) SaveWorkspaceSnapshot(_ context.Context, mid string, data json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[mid] = db.WorkspaceSnapshotRow{MachineID: mid, Data: append([]byte(nil), data...), UpdatedAt: time.Now()}
	return nil
}

// boardDevs are the computers as the manager's devices have them, for the board: a node answering on each, whether it
// is connected, its sessions, and ids another one absorbed.
type boardDevs struct {
	Devices
	mu       sync.Mutex
	order    []string
	machines map[string]*boardDev
	absorbed map[string]string
}

type boardDev struct {
	id       string
	online   bool
	sessions []proto.Session
	fn       func(ctx context.Context, method string, raw json.RawMessage) (any, error)
}

func newBoardDevs() *boardDevs {
	return &boardDevs{machines: map[string]*boardDev{}, absorbed: map[string]string{}}
}

func (d *boardDevs) canon(id string) string {
	if to, ok := d.absorbed[id]; ok {
		return to
	}
	return id
}
func (d *boardDevs) Has(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.machines[d.canon(id)] != nil
}
func (d *boardDevs) Canonical(id string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.machines[d.canon(id)] != nil {
		return d.canon(id)
	}
	return id
}
func (d *boardDevs) List() []devices.Summary {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []devices.Summary{}
	for _, id := range d.order {
		if m := d.machines[id]; m != nil {
			out = append(out, devices.Summary{ID: id, Name: id, Online: m.online})
		}
	}
	return out
}
func (d *boardDevs) Summary(id string) (devices.Summary, bool) {
	return summaryIn(d.List(), d.Canonical(id))
}
func (d *boardDevs) MachineResources(string) (*proto.Machine, *proto.MetricSample, *bool) {
	return nil, nil, nil
}

func (d *boardDevs) Sessions(id string) ([]proto.Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.machines[d.canon(id)]
	if m == nil {
		return nil, false
	}
	return append([]proto.Session(nil), m.sessions...), true
}

// SessionsWhere reads the sessions in place, as the devices do: a measure of the board then counts no copy of the list.
func (d *boardDevs) SessionsWhere(id string, keep func(*proto.Session) bool) ([]proto.Session, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.machines[d.canon(id)]
	if m == nil {
		return nil, false
	}
	return keptWhere(m.sessions, keep), true
}
func (d *boardDevs) CallNode(ctx context.Context, id, method string, params json.RawMessage) (json.RawMessage, error) {
	d.mu.Lock()
	m := d.machines[d.canon(id)]
	d.mu.Unlock()
	if m == nil {
		return nil, devices.ErrNoMachine
	}
	if !m.online || m.fn == nil {
		return nil, apierr.ErrOffline
	}
	out, err := m.fn(ctx, method, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

// add is a computer whose node answers with fn (nil: never connected).
func (d *boardDevs) add(id string, fn func(context.Context, string, json.RawMessage) (any, error)) *boardDev {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := &boardDev{id: id, online: fn != nil, fn: fn}
	d.machines[id] = m
	d.order = append(d.order, id)
	return m
}
func (d *boardDevs) setOnline(id string, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.machines[id].online = on
}
func (d *boardDevs) setSessions(id string, sessions []proto.Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.machines[id].sessions = sessions
}

// newBoardService is a manager with the board's store and computers (st nil: no database).
func newBoardService(t *testing.T, st Store, d *boardDevs) (*Service, *fakeHub) {
	t.Helper()
	f := &fakeHub{}
	o := Options{Hub: f, Logf: testLogf(t)}
	if st != nil {
		o.Store = func() Store { return st }
	}
	if d != nil {
		o.Devices = d
	}
	return New(o), f
}

func boardCall(s *Service, method, path, body string, role string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(RoleHeader, role)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func boardState(t *testing.T, w *httptest.ResponseRecorder) workspaceboard.State {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body)
	}
	var s workspaceboard.State
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func directoryNode(_ context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "workspaces.list":
		return proto.WorkspacesResponse{Workspaces: []proto.Workspace{{ID: "empty", Source: "herdr", SourceID: "empty", Title: "Weather", Cwd: "/work/weather", Available: true}}}, nil
	case "workspace.resolve":
		var p struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Path == "/missing" {
			return nil, errors.New("directory missing")
		}
		return proto.Workspace{ID: "/work/weather", Source: "directory", SourceID: "/work/weather", Title: "Weather", Cwd: "/work/weather", Available: true}, nil
	case "fs.stat":
		return map[string]any{"stats": []map[string]string{{"path": "/work/weather", "kind": "dir"}}}, nil
	default:
		return nil, errors.New("unexpected operation: " + method)
	}
}

// roleAnswer is an answer-only device (a watch): it may read the board, not change it.
const roleAnswer = "answer"

func TestWorkspaceBoardMetadataNeverExecutesSession(t *testing.T) {
	store := newMemoryBoardStore()
	d := newBoardDevs()
	s, f := newBoardService(t, store, d)
	var mu sync.Mutex
	methods := []string{}
	d.add("laptop", func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		mu.Lock()
		methods = append(methods, method)
		mu.Unlock()
		return directoryNode(ctx, method, p)
	})
	st := boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_card","machine_id":"laptop","cwd":"/alias","title":"Fix weather","column_id":"todo"}`, roleFull))
	if len(st.Cards) != 1 || st.Cards[0].Cwd != "/work/weather" {
		t.Fatalf("noncanonical card: %+v", st)
	}
	f.mu.Lock()
	events := append([]string(nil), f.events...)
	f.mu.Unlock()
	if len(events) != 1 || events[0] != "workspace_board" {
		t.Fatalf("the apps were not told of the change as before: %v", events)
	}
	id := st.Cards[0].ID
	mu.Lock()
	methods = nil
	mu.Unlock()
	d.setOnline("laptop", false)
	st = boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":1,"action":"move_card","id":"`+id+`","column_id":"done"}`, roleFull))
	if st.Cards[0].ColumnID != "done" || st.Cards[0].Archived {
		t.Fatal("Done must not archive the workspace")
	}
	boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":2,"action":"update_card","id":"`+id+`","archived":true}`, roleFull))
	st = boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":3,"action":"update_card","id":"`+id+`","archived":false,"pinned":true}`, roleFull))
	if st.Cards[0].Archived || !st.Cards[0].Pinned {
		t.Fatal("restore/pin missing")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 0 {
		t.Fatalf("metadata move invoked a node: %v", methods)
	}
	// A new manager on the same store preserves the card even without any online machine.
	restarted, _ := newBoardService(t, store, newBoardDevs())
	got := boardState(t, boardCall(restarted, "GET", "/api/v1/workspace-board", "", roleFull))
	if got.Revision != 4 || len(got.Cards) != 1 {
		t.Fatalf("restart lost board: %+v", got)
	}
}

func TestWorkspaceBoardValidationAndRoles(t *testing.T) {
	d := newBoardDevs()
	s, _ := newBoardService(t, newMemoryBoardStore(), d)
	d.add("laptop", directoryNode)
	for _, body := range []string{`{"action":"add_column","title":"Other"}`, `{"revision":0,"action":"add_card","machine_id":"unknown","cwd":"/work/weather","column_id":"todo"}`, `{"revision":0,"action":"add_card","machine_id":"laptop","cwd":"/missing","column_id":"todo"}`, `{"revision":0,"action":"add_card","machine_id":"laptop","cwd":"/work/weather","column_id":"todo","source":"forged"}`} {
		w := boardCall(s, "POST", "/api/v1/workspace-board", body, roleFull)
		if w.Code != 400 {
			t.Fatalf("HTTP %d for %s: %s", w.Code, body, w.Body)
		}
	}
	for _, path := range []string{"/api/v1/workspace-board", "/api/v1/workspaces"} {
		w := boardCall(s, "GET", path, "", roleAnswer)
		if w.Code != 200 {
			t.Fatalf("read-only GET %s: %d %s", path, w.Code, w.Body)
		}
		if path == "/api/v1/workspace-board" && !strings.Contains(w.Body.String(), `"can_write":false`) {
			t.Fatal(w.Body.String())
		}
	}
	if w := boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_column","title":"Other"}`, roleAnswer); w.Code != 403 {
		t.Fatal(w.Code)
	}
	boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_column","title":"Other"}`, roleFull))
	if w := boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_column","title":"Stale"}`, roleFull); w.Code != 409 {
		t.Fatal(w.Code)
	}
	unsupported, _ := newBoardService(t, nil, nil)
	if w := boardCall(unsupported, "GET", "/api/v1/workspace-board", "", roleFull); w.Code != 503 {
		t.Fatal(w.Code)
	}
}

func TestWorkspacesIncludeEmptyAndPersistOffline(t *testing.T) {
	store := newMemoryBoardStore()
	d := newBoardDevs()
	s, _ := newBoardService(t, store, d)
	d.add("laptop", directoryNode)
	d.add("server", directoryNode)
	w := boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	if w.Code != 200 {
		t.Fatal(w.Body)
	}
	var result struct {
		Workspaces []proto.Workspace `json:"workspaces"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Workspaces) != 2 || result.Workspaces[0].ID == result.Workspaces[1].ID {
		t.Fatalf("empty or cross-machine spaces lost: %s", w.Body)
	}
	chosen := result.Workspaces[0]
	if chosen.MachineID != "laptop" {
		t.Fatal(chosen)
	}
	raw, _ := json.Marshal(map[string]any{"revision": 0, "action": "add_card", "machine_id": "laptop", "workspace_id": chosen.ID, "column_id": "todo"})
	st := boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", string(raw), roleFull))
	if st.Cards[0].Source != "herdr" {
		t.Fatal(st)
	}
	d.setOnline("laptop", false)
	w = boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	for _, ws := range result.Workspaces {
		if ws.MachineID == "laptop" && (ws.Online || ws.Available || ws.LastSeen == "") {
			t.Fatalf("offline snapshot misrepresented: %+v", ws)
		}
	}
	rd := newBoardDevs()
	rd.add("laptop", nil)
	restarted, _ := newBoardService(t, store, rd)
	w = boardCall(restarted, "GET", "/api/v1/workspaces", "", roleFull)
	if !strings.Contains(w.Body.String(), chosen.ID) {
		t.Fatalf("restart lost last workspace: %s", w.Body)
	}
}

func TestWorkspaceBoardConcurrentMutations(t *testing.T) {
	s, _ := newBoardService(t, newMemoryBoardStore(), nil)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_column","title":"Review"}`, roleFull).Code
		}()
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for c := range results {
		counts[c]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
}

func TestWorkspaceBoardSessionOnlySurvivesUnavailableHost(t *testing.T) {
	store := newMemoryBoardStore()
	d := newBoardDevs()
	s, _ := newBoardService(t, store, d)
	d.add("laptop", func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		if method == "workspaces.list" {
			return nil, errors.New("host not running")
		}
		return directoryNode(ctx, method, p)
	})
	d.setSessions("laptop", []proto.Session{{SessionID: "external", Cwd: "/work/weather", SpaceName: "Weather"}})
	w := boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	var list proto.WorkspacesResponse
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Workspaces) != 1 || !list.Workspaces[0].Available {
		t.Fatalf("session-only directory lost: %s", w.Body)
	}
	raw, _ := json.Marshal(map[string]any{"revision": 0, "action": "add_card", "machine_id": "laptop", "workspace_id": list.Workspaces[0].ID, "column_id": "todo"})
	boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", string(raw), roleFull))
	// Discovery, including an unbound external session directory, was already committed before restart.
	rd := newBoardDevs()
	rd.add("laptop", nil)
	restarted, _ := newBoardService(t, store, rd)
	w = boardCall(restarted, "GET", "/api/v1/workspaces", "", roleFull)
	var restored proto.WorkspacesResponse
	_ = json.Unmarshal(w.Body.Bytes(), &restored)
	if len(restored.Workspaces) != 1 || restored.Workspaces[0].LastSeen == "" || restored.Workspaces[0].Online {
		t.Fatalf("restart lost session snapshot: %s", w.Body)
	}
}

func TestWorkspaceBoardCanonicalMachineAndMissingDirectory(t *testing.T) {
	d := newBoardDevs()
	s, _ := newBoardService(t, newMemoryBoardStore(), d)
	var missing bool
	d.add("canonical", func(ctx context.Context, method string, p json.RawMessage) (any, error) {
		if method == "fs.stat" && missing {
			return map[string]any{"stats": []map[string]string{{"path": "/work/weather", "kind": "missing"}}}, nil
		}
		return directoryNode(ctx, method, p)
	})
	d.absorbed["alias"] = "canonical" // the same computer: "alias" reaches it
	st := boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_card","machine_id":"alias","cwd":"/work/weather","column_id":"todo"}`, roleFull))
	if st.Cards[0].MachineID != "canonical" {
		t.Fatal("alias not normalized")
	}
	if w := boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":1,"action":"add_card","machine_id":"canonical","cwd":"/work/weather","column_id":"todo"}`, roleFull); w.Code != 400 {
		t.Fatalf("duplicate alias binding accepted: %s", w.Body)
	}
	missing = true
	w := boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	var list proto.WorkspacesResponse
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Workspaces) != 1 || list.Workspaces[0].Available {
		t.Fatalf("deleted directory reported available: %s", w.Body)
	}
}

func TestWorkspaceBoardLateMachineAlias(t *testing.T) {
	store := newMemoryBoardStore()
	d := newBoardDevs()
	s, _ := newBoardService(t, store, d)
	d.add("alias", directoryNode)
	bound := boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":0,"action":"add_card","machine_id":"alias","cwd":"/work/weather","column_id":"review"}`, roleFull))
	id := bound.Cards[0].ID
	extra := qualifyWorkspace("alias", proto.Workspace{Source: "herdr", SourceID: "empty", Cwd: "/work/empty", Title: "Empty"})
	raw, _ := json.Marshal([]proto.Workspace{extra})
	_ = store.SaveWorkspaceSnapshot(context.Background(), "alias", raw)
	d.add("canonical", directoryNode)
	d.mu.Lock()
	delete(d.machines, "alias")
	d.order = []string{"canonical"}
	d.absorbed["alias"] = "canonical"
	d.mu.Unlock()
	projected := boardState(t, boardCall(s, "GET", "/api/v1/workspace-board", "", roleFull))
	card := projected.Cards[0]
	if card.ID != id || card.ColumnID != "review" || card.MachineID != "canonical" || card.WorkspaceID == bound.Cards[0].WorkspaceID {
		t.Fatalf("late alias lost identity/routing: %+v", card)
	}
	stored, _ := store.WorkspaceBoard(context.Background())
	if stored.Cards[0].MachineID != "alias" {
		t.Fatal("presentation changed durable binding")
	}
	if w := boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":1,"action":"add_card","machine_id":"canonical","cwd":"/work/weather","column_id":"todo"}`, roleFull); w.Code != 400 {
		t.Fatalf("late alias allowed duplicate: %s", w.Body)
	}
	updated := boardState(t, boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":1,"action":"update_card","id":"`+id+`","archived":true}`, roleFull))
	if updated.Cards[0].MachineID != "canonical" || !updated.Cards[0].Archived {
		t.Fatal("mutation response lost projected identity")
	}
	if w := boardCall(s, "POST", "/api/v1/workspace-board", `{"revision":2,"action":"add_card","machine_id":"canonical","cwd":"/work/weather","column_id":"todo"}`, roleFull); w.Code != 400 {
		t.Fatal("archived alias allowed duplicate")
	}
	d.setOnline("canonical", false)
	var list proto.WorkspacesResponse
	w := boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Workspaces) != 2 {
		t.Fatalf("absorbed alias snapshot lost: %s", w.Body)
	}
	for _, item := range list.Workspaces {
		if item.MachineID != "canonical" || item.Online || item.Available {
			t.Fatalf("cached alias routing incorrect: %+v", item)
		}
	}
	// A newer complete catalog wins, rather than reviving deleted alias spaces.
	_ = store.SaveWorkspaceSnapshot(context.Background(), "canonical", json.RawMessage(`[]`))
	w = boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Workspaces) != 1 || list.Workspaces[0].Cwd != "/work/weather" {
		t.Fatalf("stale alias catalog revived: %s", w.Body)
	}
}

func TestWorkspacesFailedSourcesKeepQualifiedAliasSnapshot(t *testing.T) {
	store := newMemoryBoardStore()
	main, unread := true, false
	cached := []proto.Workspace{
		{ID: "retained", Source: "orca", SourceID: "retained", Title: "Retained", Cwd: "/work/retained/.",
			ParentSourceID: "root", ProjectID: "project", ProjectName: "Project", ProjectPath: "/work/project",
			IsMain: &main, Unread: &unread, SidebarStatus: "working", WorkspaceKind: "worktree", Head: "example", Index: 3,
			Branch: "topic", Online: true, Available: true, LastSeen: "2026-01-01T00:00:00Z"},
		{ID: "current", Source: "orca", SourceID: "current", Title: "Old title", Cwd: "/work/current"},
		{ID: "legacy", MachineID: "alias", Title: "Legacy", Cwd: "/work/legacy/.", LastSeen: "2026-01-01T00:00:00Z"},
		{ID: "removed", Source: "herdr", SourceID: "removed", Title: "Removed", Cwd: "/work/removed"},
	}
	cached[0] = qualifyWorkspace("alias", cached[0])
	cached[1] = qualifyWorkspace("alias", cached[1])
	raw, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	// The latest alias catalog wins even when an older canonical snapshot was listed first.
	store.snapshots["canonical"] = db.WorkspaceSnapshotRow{MachineID: "canonical", Data: json.RawMessage(`[]`), UpdatedAt: time.Unix(1, 0)}
	store.snapshots["alias"] = db.WorkspaceSnapshotRow{MachineID: "alias", Data: raw, UpdatedAt: time.Unix(2, 0)}
	d := newBoardDevs()
	d.absorbed["alias"] = "canonical"
	current := proto.Workspace{ID: "current", Source: "orca", SourceID: "current", Title: "Current", Cwd: "/work/current", Available: true}
	d.add("canonical", func(context.Context, string, json.RawMessage) (any, error) {
		return proto.WorkspacesResponse{Workspaces: []proto.Workspace{current}, Errors: []proto.WorkspaceSourceError{
			{Source: "orca", Error: "catalog unavailable"}, {Source: "workspace", Error: "catalog unavailable"},
		}}, nil
	})
	s, _ := newBoardService(t, store, d)
	read := func(t *testing.T, service *Service, expectedErrors int) map[string]proto.Workspace {
		t.Helper()
		w := boardCall(service, "GET", "/api/v1/workspaces", "", roleFull)
		var response struct {
			Workspaces []proto.Workspace         `json:"workspaces"`
			Errors     []workspaceDiscoveryError `json:"errors"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 {
			t.Fatalf("workspace catalog: HTTP %d, error %v, body %s", w.Code, err, w.Body)
		}
		if len(response.Workspaces) != 3 || len(response.Errors) != expectedErrors {
			t.Fatalf("workspace catalog lost cached rows or errors: %s", w.Body)
		}
		for _, failure := range response.Errors {
			if failure.MachineID != "canonical" {
				t.Fatalf("failure retained an alias: %+v", failure)
			}
		}
		out := map[string]proto.Workspace{}
		for _, item := range response.Workspaces {
			out[item.ID] = item
		}
		return out
	}
	got := read(t, s, 2)
	want := map[string]proto.Workspace{}
	for _, item := range []proto.Workspace{current, cached[0], cached[2]} {
		item = qualifyWorkspace("canonical", item)
		item.Online = true
		if item.SourceID == "current" {
			item.LastSeen = got[item.ID].LastSeen
			if _, err := time.Parse(time.RFC3339Nano, item.LastSeen); err != nil {
				t.Fatal("current workspace lost its discovery time")
			}
		} else {
			item.Available = false
		}
		want[item.ID] = item
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qualified failed-source catalog: got %+v, want %+v", got, want)
	}
	// Qualification must precede duplicate checks, not just final response encoding: the stored catalog has no
	// second copy of the current row, preserves Orca metadata and drops the successful source's deleted space.
	var persisted []proto.Workspace
	if err := json.Unmarshal(store.snapshots["canonical"].Data, &persisted); err != nil || len(persisted) != len(want) {
		t.Fatalf("persisted catalog has duplicate or missing rows: %+v, error %v", persisted, err)
	}
	for _, item := range persisted {
		if !reflect.DeepEqual(item, want[item.ID]) {
			t.Fatalf("persisted workspace differs: %+v", item)
		}
	}
	if string(store.snapshots["alias"].Data) != string(raw) {
		t.Fatal("projecting an alias changed its durable snapshot")
	}
	saved := string(store.snapshots["canonical"].Data)
	for _, c := range []struct {
		name   string
		online bool
		errors int
	}{{"offline restart", false, 0}, {"failed catalog restart", true, 1}} {
		t.Run(c.name, func(t *testing.T) {
			rd := newBoardDevs()
			rd.absorbed["alias"] = "canonical"
			if c.online {
				rd.add("canonical", func(context.Context, string, json.RawMessage) (any, error) {
					return nil, errors.New("catalog unavailable")
				})
			} else {
				rd.add("canonical", nil)
			}
			restarted, _ := newBoardService(t, store, rd)
			got := read(t, restarted, c.errors)
			for id, item := range want {
				item.Online, item.Available = c.online, false
				if !reflect.DeepEqual(got[id], item) {
					t.Fatalf("restart changed cached metadata or identity: got %+v, want %+v", got[id], item)
				}
			}
			if string(store.snapshots["canonical"].Data) != saved {
				t.Fatal("an unavailable node rewrote its last successful snapshot")
			}
		})
	}
}

func TestWorkspacesUseVerifiedSessionDirectory(t *testing.T) {
	d := newBoardDevs()
	s, _ := newBoardService(t, newMemoryBoardStore(), d)
	d.add("laptop", directoryNode)
	sessions := []proto.Session{{SessionID: "external", Cwd: "/work/alias", WorkspaceCwd: "/work/weather"}}
	d.setSessions("laptop", sessions)
	w := boardCall(s, "GET", "/api/v1/workspaces", "", roleFull)
	var list proto.WorkspacesResponse
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Workspaces) != 1 || list.Workspaces[0].Cwd != "/work/weather" {
		t.Fatalf("symlink produced duplicate directories: %s", w.Body)
	}
	candidate := workspaceForSession("laptop", sessions[0])
	if candidate.Cwd != "/work/weather" || candidate.SourceID != "/work/weather" {
		t.Fatalf("session fallback ignored verified path: %+v", candidate)
	}
}

// PUT /api/v1/workspace-board/settings: an over-the-cap body is 413 too_large, not "needs enabled".
func TestWorkspaceBoardSettingsBodyLimit(t *testing.T) {
	s, _ := newBoardService(t, newSettingsBoardStore(), nil)
	body := `{"enabled":true,"note":"` + strings.Repeat("x", 16<<10) + `"}`
	w := boardCall(s, "PUT", "/api/v1/workspace-board/settings", body, roleFull)
	if w.Code != 413 || !strings.Contains(w.Body.String(), proto.CodeTooLarge) {
		t.Fatalf("oversized settings body: %d %s", w.Code, w.Body)
	}
}
