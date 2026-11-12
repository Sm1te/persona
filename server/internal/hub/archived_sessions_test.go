package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// agentSessionArchivesSupported: every owner supports archives now (the feature the apps are told).
func (h *Hub) agentSessionArchivesSupported() bool {
	return slices.Contains(h.agentSessionArchiveFeatures(), featureAgentSessionArchives)
}

// These tests exercise the Hub's real HTTP and manager bus boundary. Archive
// persistence, restart and automatic restoration belong to the manager tests.
func archiveRelayHub(t *testing.T, failingStatus ...int) (*Hub, *httptest.Server, *guardManager, *guardManager) {
	t.Helper()
	h, srv, _ := newBusHub(t)
	if err := h.store.PutSetting(t.Context(), deletedSessionsKey, map[string]time.Time{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.store.PutSetting(context.Background(), deletedSessionsKey, map[string]time.Time{}); err != nil {
			t.Error(err)
		}
	})
	h.cfg.Machines = []MachineConfig{{ID: "ma", Name: "ma", Kind: "local"}, {ID: "mb", Name: "mb", Kind: "local"}}
	changed := time.Now().UTC()
	answer := func(mid, agent string, extension bool) func(string, json.RawMessage) (any, error) {
		return func(method string, raw json.RawMessage) (any, error) {
			switch method {
			case extwire.MethodAuthorize:
				if !extension {
					return extwire.Authorized{}, nil
				}
				return extwire.Authorized{OK: true, Extension: "bx", Name: "BX", Scopes: []string{extwire.ScopeAgentsRead}}, nil
			case assistantwire.MethodHTTP:
				var request assistantwire.HTTPRequest
				if err := json.Unmarshal(raw, &request); err != nil {
					return nil, err
				}
				if mid == "mb" && request.Method == http.MethodGet && len(failingStatus) > 0 {
					return assistantwire.HTTPResponse{Status: failingStatus[0], Body: []byte(`{"code":"archive_read_failed"}`)}, nil
				}
				var body any = map[string]any{"ok": true, "owner": mid}
				if request.Method == http.MethodGet {
					other := "ma"
					if mid == other {
						other = "mb"
					}
					body = map[string]any{"sessions": []proto.Session{{MachineID: mid, SessionID: "same", Agent: agent,
						Title: agent + " archive", Status: proto.StatusOffline, State: proto.StateOffline,
						Archived: true, UpdatedAt: changed.Add(-time.Hour), ArchiveChangedAt: &changed},
						// A manager cannot claim another manager's session in its response.
						{MachineID: other, SessionID: "foreign", Agent: agent, Archived: true}}}
				}
				data, err := json.Marshal(body)
				if err != nil {
					return nil, err
				}
				return assistantwire.HTTPResponse{Status: http.StatusOK, Body: data}, nil
			}
			return map[string]any{}, nil
		}
	}
	a := connectGuardManager(t, h, srv.URL, assistantwire.AssistantID, answer("ma", "claude", false))
	b := connectGuardManager(t, h, srv.URL, "m-0b0b0b", answer("mb", "codex", true))
	archiveManagerCapability(t, a, true)
	archiveManagerCapability(t, b, true)
	if _, err := h.moveMachine(context.Background(), "mb", b.id); err != nil {
		t.Fatal(err)
	}
	a.report(t, devicewire.State{Machine: proto.Machine{ID: "ma", Name: "ma"}})
	b.report(t, devicewire.State{Machine: proto.Machine{ID: "mb", Name: "mb"}})
	eventually(t, 5*time.Second, "archive machine owners", func() bool {
		return h.localMachine("ma") != nil && h.localMachine("mb") != nil
	})
	return h, srv, a, b
}

func archiveManagerCapability(t *testing.T, manager *guardManager, supported bool) {
	t.Helper()
	capabilities := []string{assistantwire.CapabilityConfigOwn}
	if supported {
		capabilities = append(capabilities, assistantwire.CapabilityAgentSessionArchives)
	}
	if err := manager.call(t, busHello, map[string]any{"id": manager.id, "capabilities": capabilities}, nil); err != nil {
		t.Fatal(err)
	}
}

func archiveHTTPCalls(t *testing.T, manager *guardManager) []assistantwire.HTTPRequest {
	t.Helper()
	var calls []assistantwire.HTTPRequest
	for _, line := range strings.Split(manager.log(), "\n") {
		if raw, ok := strings.CutPrefix(line, assistantwire.MethodHTTP+" "); ok {
			var request assistantwire.HTTPRequest
			if err := json.Unmarshal([]byte(raw), &request); err != nil {
				t.Fatalf("invalid recorded manager request: %v", err)
			}
			calls = append(calls, request)
		}
	}
	return calls
}

func archiveReplyRows(t *testing.T, body any) []proto.Session {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Sessions []proto.Session `json:"sessions"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		t.Fatal(err)
	}
	return reply.Sessions
}

func TestAgentSessionArchivesRelayToOwnerAndMergeManagers(t *testing.T) {
	h, srv, a, b := archiveRelayHub(t)
	for _, action := range []string{"archive", "unarchive"} {
		path := "/api/v1/sessions/mb/same/" + action
		code, body := ownershipGet(t, h, srv, http.MethodPost, path, nil)
		if code != http.StatusOK || body["ok"] != true || body["owner"] != "mb" {
			t.Fatalf("%s: %d %+v", path, code, body)
		}
		calls := archiveHTTPCalls(t, b)
		if len(calls) == 0 {
			t.Fatal("write was acknowledged without reaching its manager")
		}
		last := calls[len(calls)-1]
		if last.Path != path || last.Method != http.MethodPost || len(last.Header[assistantwire.RoleHeader]) != 1 || last.Header[assistantwire.RoleHeader][0] != roleFull || last.Header["Authorization"] != nil {
			t.Fatalf("wrong relay envelope: %+v", last)
		}
	}
	if calls := archiveHTTPCalls(t, a); len(calls) != 0 {
		t.Fatalf("unrelated manager received writes: %+v", calls)
	}
	code, body := ownershipGet(t, h, srv, http.MethodGet, "/api/v1/sessions/archived?machine_id=mb", nil)
	if rows := archiveReplyRows(t, body); code != http.StatusOK || len(rows) != 1 || rows[0].MachineID != "mb" || rows[0].Agent != "codex" {
		t.Fatalf("machine-scoped list: %d %+v", code, body)
	}
	if calls := archiveHTTPCalls(t, a); len(calls) != 0 {
		t.Fatalf("machine-scoped read reached unrelated manager: %+v", calls)
	}
	code, body = ownershipGet(t, h, srv, http.MethodGet, "/api/v1/sessions/archived", nil)
	rows := archiveReplyRows(t, body)
	if code != http.StatusOK || len(rows) != 2 {
		t.Fatalf("merged archives: %d %+v", code, body)
	}
	for _, row := range rows {
		if row.Manager != h.machineOwner(row.MachineID) || !row.Archived || row.ArchiveChangedAt == nil || row.ArchiveChangedAt.IsZero() {
			t.Fatalf("manager identity or visibility lost: %+v", row)
		}
	}
	if rows[0].MachineID == rows[1].MachineID || rows[0].SessionID != rows[1].SessionID {
		t.Fatalf("same session id must remain distinct across machines: %+v", rows)
	}
}

func TestAgentSessionArchivesExtensionAndRoleBoundary(t *testing.T) {
	_, srv, a, b := archiveRelayHub(t)
	for _, path := range []string{"/api/v1/sessions/archived", "/api/v1/sessions/archived?machine_id=mb"} {
		code, body := extDo(t, srv.URL, http.MethodGet, path, "", nil)
		var decoded map[string]any
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatal(err)
		}
		rows := archiveReplyRows(t, decoded)
		if code != http.StatusOK || len(rows) != 1 || rows[0].MachineID != "mb" {
			t.Fatalf("extension list %s: %d %s", path, code, body)
		}
	}
	if calls := archiveHTTPCalls(t, a); len(calls) != 0 {
		t.Fatalf("extension read reached another manager: %+v", calls)
	}
	before := len(archiveHTTPCalls(t, b))
	for _, request := range [][2]string{
		{http.MethodGet, "/api/v1/sessions/archived?machine_id=ma"},
		{http.MethodPost, "/api/v1/sessions/ma/same/archive"},
		{http.MethodPost, "/api/v1/sessions/mb/same/archive"},
		{http.MethodPost, "/api/v1/sessions/mb/same/unarchive"},
	} {
		if code, body := extDo(t, srv.URL, request[0], request[1], "{}", nil); code != http.StatusForbidden {
			t.Fatalf("extension must not mutate or cross owners: %v: %d %s", request, code, body)
		}
	}
	if len(archiveHTTPCalls(t, a)) != 0 || len(archiveHTTPCalls(t, b)) != before {
		t.Fatal("forbidden extension request reached a manager")
	}
	for _, role := range []string{roleAnswer, roleLink} {
		p := principal{Role: role}
		for _, action := range []string{"archive", "unarchive"} {
			if allowed(p, http.MethodPost, "/api/v1/sessions/mb/same/"+action) {
				t.Fatalf("%s may mutate archives", role)
			}
		}
	}
	if !allowed(principal{Role: roleAnswer}, http.MethodGet, "/api/v1/sessions/archived") {
		t.Fatal("answer-only role cannot read archives")
	}
}

func TestAgentSessionArchivesCapabilitiesAndOfflineAreExplicit(t *testing.T) {
	h, srv, _, b := archiveRelayHub(t)
	if !h.agentSessionArchivesSupported() || !slices.Contains(h.features(), featureAgentSessionArchives) {
		t.Fatal("all capable online managers must advertise archives")
	}
	archiveManagerCapability(t, b, false)
	if h.agentSessionArchivesSupported() || slices.Contains(h.features(), featureAgentSessionArchives) {
		t.Fatal("mixed manager versions must hide the archive feature")
	}
	check := func(want int) {
		t.Helper()
		for _, request := range [][2]string{{http.MethodGet, "/api/v1/sessions/archived"},
			{http.MethodGet, "/api/v1/sessions/archived?machine_id=mb"},
			{http.MethodPost, "/api/v1/sessions/mb/same/archive"}, {http.MethodPost, "/api/v1/sessions/mb/same/unarchive"}} {
			if code, body := ownershipGet(t, h, srv, request[0], request[1], nil); code != want {
				t.Fatalf("%v: got %d want %d: %+v", request, code, want, body)
			}
		}
	}
	check(http.StatusNotImplemented)
	archiveManagerCapability(t, b, true)
	if !h.agentSessionArchivesSupported() {
		t.Fatal("capability update did not enable archives")
	}
	if err := h.busConnOf(b.id).ws.CloseNow(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "archive manager offline", func() bool { return h.busConnOf(b.id) == nil })
	if h.agentSessionArchivesSupported() || slices.Contains(h.features(), featureAgentSessionArchives) {
		t.Fatal("offline owner must hide the archive feature")
	}
	check(http.StatusServiceUnavailable)
}

func TestAgentSessionArchivesRelayFiltersDeleteTimestamps(t *testing.T) {
	h, srv, _, _ := archiveRelayHub(t)
	if err := h.noteDeleted(t.Context(), "mb", "same", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/sessions/archived", "/api/v1/sessions/archived?machine_id=mb"} {
		code, body := ownershipGet(t, h, srv, http.MethodGet, path, nil)
		if rows := archiveReplyRows(t, body); code != http.StatusOK || !slices.ContainsFunc(rows, func(s proto.Session) bool { return s.MachineID == "mb" }) {
			t.Fatalf("old delete hid a newer archive: %s: %d %+v", path, code, body)
		}
	}
	if err := h.noteDeleted(t.Context(), "mb", "same", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	code, body := ownershipGet(t, h, srv, http.MethodGet, "/api/v1/sessions/archived", nil)
	if rows := archiveReplyRows(t, body); code != http.StatusOK || len(rows) != 1 || rows[0].MachineID != "ma" {
		t.Fatalf("later delete must hide only its machine's archive: %d %+v", code, body)
	}
	code, body = ownershipGet(t, h, srv, http.MethodGet, "/api/v1/sessions/archived?machine_id=mb", nil)
	if rows := archiveReplyRows(t, body); code != http.StatusOK || len(rows) != 0 {
		t.Fatalf("single-manager relay bypassed delete filter: %d %+v", code, body)
	}
}

func TestAgentSessionArchivesMergedReadRejectsPartialSuccess(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h, srv, a, b := archiveRelayHub(t, status)
			if !h.agentSessionArchivesSupported() {
				t.Fatal("fixture must pass capability and online checks before the read fails")
			}
			code, body := ownershipGet(t, h, srv, http.MethodGet, "/api/v1/sessions/archived", nil)
			if code != status || body["code"] != "archive_read_failed" {
				t.Fatalf("manager failure became a partial success: got %d %+v, want %d", code, body, status)
			}
			if _, exists := body["sessions"]; exists {
				t.Fatalf("partial archives leaked into failed response: %+v", body)
			}
			if len(archiveHTTPCalls(t, a)) != 1 || len(archiveHTTPCalls(t, b)) != 1 {
				t.Fatal("must exercise an actual successful manager response followed by a failed manager response")
			}
		})
	}
}

func TestAgentSessionArchivesOrdinaryAndPagedInventory(t *testing.T) {
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "computer"})
	h.machines, h.byID[m.cfg.ID] = []*Machine{m}, m
	for _, agent := range []string{"claude", "codex"} {
		m.sessions = append(m.sessions, proto.Session{MachineID: m.cfg.ID, SessionID: agent, Agent: agent,
			Status: proto.StatusOffline, State: proto.StateOffline, UpdatedAt: time.Now().Add(-time.Hour)})
	}
	if page := runtimePage(t, h, "summary=1&limit=50"); page.Total != 2 {
		t.Fatalf("initial inventory: %+v", page)
	}
	for i := range m.sessions {
		m.sessions[i].Archived, m.sessions[i].ArchiveChangedAt = true, new(time.Now())
	}
	h.sessionsChanged(m)
	if page := runtimePage(t, h, "summary=1&limit=50"); page.Total != 0 {
		t.Fatalf("manager archives leaked into paged inventory: %+v", page)
	}
	if rows := runtimePage(t, h, "").Sessions; len(rows) != 0 {
		t.Fatalf("manager archives leaked into ordinary inventory: %+v", rows)
	}
	for i := range m.sessions {
		m.sessions[i].Archived, m.sessions[i].ArchiveChangedAt = false, new(time.Now())
	}
	h.sessionsChanged(m)
	if page := runtimePage(t, h, "summary=1&limit=50"); page.Total != 2 {
		t.Fatalf("manager restores missing from paged inventory: %+v", page)
	}
}

func TestAgentSessionArchivesRestoreDeletePrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		delta   time.Duration
		visible bool
	}{{"older restore", -time.Second, false}, {"same timestamp", 0, false}, {"newer restore", time.Second, true}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHub(t)
			h.store = &settingsStore{vals: map[string][]byte{}}
			deleted := time.Now().UTC()
			if err := h.noteDeleted(t.Context(), "computer", "same", deleted); err != nil {
				t.Fatal(err)
			}
			row := proto.Session{MachineID: "computer", SessionID: "same", Agent: "codex", Status: proto.StatusOffline,
				UpdatedAt: deleted.Add(-time.Hour), ArchiveChangedAt: new(deleted.Add(tc.delta))}
			if got := len(h.shown("computer", []proto.Session{row})) == 1; got != tc.visible {
				t.Fatalf("visibility=%v want %v", got, tc.visible)
			}
			row.MachineID = "other"
			if len(h.shown("other", []proto.Session{row})) != 1 {
				t.Fatal("delete leaked across machines with the same session id")
			}
		})
	}
}

func archiveCloseCapability(t *testing.T, manager *guardManager, close bool) {
	t.Helper()
	capabilities := []string{assistantwire.CapabilityConfigOwn, assistantwire.CapabilityAgentSessionArchives}
	if close {
		capabilities = append(capabilities, assistantwire.CapabilityAgentSessionArchiveClose)
	}
	if err := manager.call(t, busHello, map[string]any{"id": manager.id, "capabilities": capabilities}, nil); err != nil {
		t.Fatal(err)
	}
}

// "Archive and close" (?close=1) reaches only a manager that does it: an older one would archive and leave the agent
// open while the app says it closed it.
func TestAgentSessionArchiveCloseNeedsEveryManager(t *testing.T) {
	h, srv, a, b := archiveRelayHub(t)
	if slices.Contains(h.features(), featureAgentSessionArchiveClose) {
		t.Fatal("close advertised before any manager named it")
	}
	archiveCloseCapability(t, a, true)
	archiveCloseCapability(t, b, false)
	if f := h.features(); !slices.Contains(f, featureAgentSessionArchives) || slices.Contains(f, featureAgentSessionArchiveClose) {
		t.Fatalf("mixed managers: %v", f)
	}
	before := len(archiveHTTPCalls(t, b))
	code, body := ownershipGet(t, h, srv, http.MethodPost, "/api/v1/sessions/mb/same/archive?close=1", nil)
	if code != http.StatusNotImplemented || body["code"] != "agent_archive_close_unsupported" {
		t.Fatalf("close to an old manager: %d %+v", code, body)
	}
	if len(archiveHTTPCalls(t, b)) != before {
		t.Fatal("close request reached a manager that cannot close")
	}
	if code, body := ownershipGet(t, h, srv, http.MethodPost, "/api/v1/sessions/mb/same/archive", nil); code != http.StatusOK {
		t.Fatalf("plain archive must still work: %d %+v", code, body)
	}
	archiveCloseCapability(t, b, true)
	if f := h.features(); !slices.Contains(f, featureAgentSessionArchiveClose) {
		t.Fatalf("every manager closes: %v", f)
	}
	code, body = ownershipGet(t, h, srv, http.MethodPost, "/api/v1/sessions/mb/same/archive?close=1", nil)
	calls := archiveHTTPCalls(t, b)
	if code != http.StatusOK || len(calls) == 0 || calls[len(calls)-1].Query != "close=1" {
		t.Fatalf("close relay: %d %+v %+v", code, body, calls)
	}
	if calls := archiveHTTPCalls(t, a); len(calls) != 0 {
		t.Fatalf("unrelated manager received the close: %+v", calls)
	}
}
