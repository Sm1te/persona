package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestForceArchiveConfirmationDatabase(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	ft := newFleetTestOn(t, database)
	ft.f.sessions["mac"] = nil
	row := proto.Session{MachineID: "mac", SessionID: "blocked-agent", Agent: "claude", Status: proto.StatusBlocked, State: proto.StateNeedsYou, UpdatedAt: time.Now().Add(-time.Hour)}
	ft.d.setSessions("mac", []proto.Session{row})
	ft.d.machines["mac"].call = func(method string, raw json.RawMessage) (any, error) {
		if method == "session.close" {
			var got struct {
				Force bool `json:"force"`
			}
			_ = json.Unmarshal(raw, &got)
			if !got.Force {
				t.Error("close did not request cancellation-only force")
			}
		}
		return map[string]any{}, nil
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		ix := proto.Interaction{ID: "force-" + kind, MachineID: "mac", SessionID: row.SessionID, Kind: kind, ExpiresAt: time.Now().Add(time.Hour)}
		ft.s.record(ix)
		ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix, local: func(r proto.Resolution) error {
			if r.Allow != nil && *r.Allow || r.Approve != nil && *r.Approve || r.Always || r.Mode != "" {
				t.Errorf("force archive granted approval: %+v", r)
			}
			if kind == proto.IxScreen && !r.Dismiss {
				t.Errorf("screen was not cancelled: %+v", r)
			}
			return nil
		}}
	}
	result, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
	if err != nil {
		t.Fatalf("force archive: %v", err)
	}
	if !strings.Contains(result, "closed") {
		t.Fatalf("close result missing: %s", result)
	}
	if got := ft.d.callsTo("mac", "session.close"); len(got) != 1 {
		t.Fatalf("close calls: %s", got)
	}
	if len(ft.s.pendingQuestions()) != 0 {
		t.Fatal("confirmations remain pending")
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("stale blocked inventory undid the force archive")
	}
	restarted := newFleetTestOn(t, database)
	restarted.d.setSessions("mac", nil)
	if !archivedIDs(t, restarted.s)[row.SessionID] {
		t.Fatal("force archive did not persist")
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		status, r, err := database.InteractionOutcome(ctx, "force-"+kind)
		wantStatus := "resolved"
		if kind == proto.IxScreen {
			wantStatus = "dismissed"
		}
		if err != nil || status != wantStatus || r == nil {
			t.Fatalf("stored confirmation %s: %s %+v %v", kind, status, r, err)
		}
		if kind == proto.IxPermission && (r.Allow == nil || *r.Allow) || kind == proto.IxPlan && (r.Approve == nil || *r.Approve) || kind == proto.IxScreen && !r.Dismiss || r.Always || r.Mode != "" {
			t.Fatalf("stored confirmation granted or lost its cancellation: %+v", r)
		}
		raw, _ := json.Marshal(r)
		t.Logf("%s persisted %s: %s", kind, status, raw)
	}
}

func TestForceArchiveHTTPAndQuestionBoundary(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou })
	ix := proto.Interaction{ID: "waiting-question", MachineID: "mac", SessionID: row.SessionID, Kind: proto.IxQuestion}
	ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix}
	_, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
	if err == nil || len(ft.d.callsTo("mac", "session.close")) != 0 {
		t.Fatal("force archive bypassed an ordinary question")
	}
	ft.s.finishInteraction(ix.ID)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/?force=1", nil)
	r.SetPathValue("sid", row.SessionID)
	r.Header.Set(RoleHeader, roleFull)
	ft.s.setAgentSessionArchive(w, r, "mac", true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"closed":true`) {
		t.Fatalf("forced HTTP close: %d %s", w.Code, w.Body)
	}
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("HTTP close did not retain archive")
	}
	row.Status, row.State = proto.StatusWorking, proto.StateWorking
	ft.d.setSessions("mac", []proto.Session{row})
	if archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("new work did not restore the archived session")
	}
}

func TestForceArchiveKeepsOtherBusyGuards(t *testing.T) {
	for name, edit := range map[string]func(*proto.Session){
		"working":            func(s *proto.Session) { s.Status = proto.StatusWorking; s.State = proto.StateWorking },
		"starting":           func(s *proto.Session) { s.Starting = true },
		"background":         func(s *proto.Session) { s.BackgroundWorking = true },
		"background waiting": func(s *proto.Session) { s.BackgroundWaiting = true },
		"background tasks":   func(s *proto.Session) { s.Background = []proto.BackgroundTask{{ID: "job"}} },
		"workflow":           func(s *proto.Session) { s.Workflows = []proto.Workflow{{Running: true}} },
		"subagent":           func(s *proto.Session) { s.Subagents = []proto.Subagent{{Running: true}} },
		"loop":               func(s *proto.Session) { s.Loop = &proto.LoopInfo{} },
		"queued":             func(s *proto.Session) { s.Queued = []string{"next"} },
		"server":             func(s *proto.Session) { s.Ports = []proto.Port{{Port: 5173}} },
		"assistant":          func(s *proto.Session) { s.Assistant = true },
	} {
		t.Run(name, func(t *testing.T) {
			ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou; edit(s) })
			_, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
			if err == nil {
				t.Fatal("force accepted unsafe state")
			}
			if len(ft.d.callsTo("mac", "session.close")) != 0 || len(archivedIDs(t, ft.s)) != 0 {
				t.Fatal("refusal changed the Agent")
			}
		})
	}
}

func TestForceArchiveCloseFailureAndHTTPRole(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou })
	ft.d.machines["mac"].call = func(string, json.RawMessage) (any, error) { return nil, errors.New("close failed") }
	for _, role := range []string{roleAnswer, roleFull} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/?force=1", nil)
		r.SetPathValue("sid", row.SessionID)
		r.Header.Set(RoleHeader, role)
		ft.s.setAgentSessionArchive(w, r, "mac", true)
		if role == roleAnswer && w.Code != http.StatusForbidden {
			t.Fatalf("answer-only: %d", w.Code)
		}
		if role == roleFull && w.Code == http.StatusOK {
			t.Fatal("failed close claimed success")
		}
	}
	if len(archivedIDs(t, ft.s)) != 0 {
		t.Fatal("failed close hid the blocked Agent")
	}
}

// Observe the durable record between the archive write and the final ClosedAt write.
// A fresh manager has no in-memory fence to mask an unsafe persisted record.
type forceArchiveWindowStore struct {
	*db.DB
	afterArchive   func()
	beforeFinalize func()
}

func (s *forceArchiveWindowStore) PutSetting(ctx context.Context, key string, value any) error {
	if key == agentArchivesKey && s.afterArchive == nil && s.beforeFinalize != nil {
		check := s.beforeFinalize
		s.beforeFinalize = nil
		check()
	}
	if err := s.DB.PutSetting(ctx, key, value); err != nil {
		return err
	}
	if key == agentArchivesKey && s.afterArchive != nil {
		check := s.afterArchive
		s.afterArchive = nil
		check()
	}
	return nil
}

func TestForceArchiveCloseWriteWindowDatabase(t *testing.T) {
	ctx := context.Background()
	database, err := db.Open(ctx, newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	ft := newFleetTestOn(t, database)
	ft.f.sessions["mac"] = nil
	row := proto.Session{MachineID: "mac", SessionID: "window-agent", Agent: "claude", Status: proto.StatusBlocked, State: proto.StateNeedsYou, UpdatedAt: time.Now().Add(-time.Hour)}
	ft.d.setSessions("mac", []proto.Session{row})
	closed := false
	resolved := 0
	checkRestart := func(phase string) {
		restarted := newFleetTestOn(t, database)
		restarted.f.sessions["mac"] = nil
		restarted.d.setSessions("mac", []proto.Session{row})
		if !archivedIDs(t, restarted.s)[row.SessionID] {
			t.Errorf("%s: stale blocked snapshot restored the archive before ClosedAt was finalized", phase)
		}
	}
	window := &forceArchiveWindowStore{DB: database, afterArchive: func() {
		t.Logf("archive write persisted: close completed=%v, cancellations=%d", closed, resolved)
		checkRestart("restart at archive-write window")
	}}
	window.beforeFinalize = func() {
		if !closed || resolved != 3 {
			t.Errorf("final write before close/cancellations: close=%v cancellations=%d", closed, resolved)
		}
		t.Log("close succeeded and all three cards cancelled; reading stale blocked snapshot before final ClosedAt write")
		checkRestart("restart after close before final ClosedAt write")
	}
	ft.s.store = func() Store { return window }
	ft.d.machines["mac"].call = func(method string, raw json.RawMessage) (any, error) {
		if method == "session.close" {
			var records map[string]agentArchive
			if _, err := database.Setting(ctx, agentArchivesKey, &records); err != nil {
				t.Fatal(err)
			}
			rec := records[runtimeListID("mac", row.SessionID)]
			if !rec.Archived || !rec.ClosedAt.After(time.Now()) {
				t.Error("close began without a durable future ClosedAt fence")
			}
			closed = true
		}
		return map[string]any{}, nil
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		ix := proto.Interaction{ID: "window-" + kind, MachineID: "mac", SessionID: row.SessionID, Kind: kind, ExpiresAt: time.Now().Add(time.Hour)}
		ft.s.record(ix)
		ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix, local: func(r proto.Resolution) error {
			resolved++
			if !closed || r.Allow != nil && *r.Allow || r.Approve != nil && *r.Approve || r.Always || r.Mode != "" {
				t.Errorf("unsafe cancellation: %+v", r)
			}
			if kind == proto.IxPermission && (r.Allow == nil || *r.Allow) || kind == proto.IxPlan && (r.Approve == nil || *r.Approve) || kind == proto.IxScreen && !r.Dismiss {
				t.Errorf("wrong cancellation: %+v", r)
			}
			return nil
		}}
	}
	before := time.Now()
	if err := ft.s.forceArchiveConfirmation(ctx, "mac", row.SessionID); err != nil {
		t.Fatal(err)
	}
	if !closed || resolved != 3 {
		t.Fatalf("close=%v cancellations=%d", closed, resolved)
	}
	checkRestart("restart after close")
	if !archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("stale blocked snapshot restored the running manager's archive")
	}
	var records map[string]agentArchive
	if _, err := database.Setting(ctx, agentArchivesKey, &records); err != nil {
		t.Fatal(err)
	}
	at := records[runtimeListID("mac", row.SessionID)].ClosedAt
	if at.Before(before) || at.After(time.Now()) {
		t.Fatalf("ClosedAt was not finalized to the real close time: %v", at)
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		status, r, err := database.InteractionOutcome(ctx, "window-"+kind)
		if err != nil || r == nil {
			t.Fatalf("stored cancellation: %s %+v %v", status, r, err)
		}
		raw, _ := json.Marshal(r)
		t.Logf("%s persisted %s: %s", kind, status, raw)
	}
}

func TestForceArchiveCloseFailureClearsFenceDatabase(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	database, err := db.Open(context.Background(), newDatabase(t), db.ManagerSet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	ft := newFleetTestOn(t, database)
	ft.f.sessions["mac"] = nil
	row := proto.Session{MachineID: "mac", SessionID: "failed-close", Agent: "claude", Status: proto.StatusBlocked, State: proto.StateNeedsYou, UpdatedAt: time.Now().Add(-time.Hour)}
	ft.d.setSessions("mac", []proto.Session{row})
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		ix := proto.Interaction{ID: "failed-" + kind, MachineID: "mac", SessionID: row.SessionID, Kind: kind, ExpiresAt: time.Now().Add(time.Hour)}
		ft.s.record(ix)
		ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix, local: func(proto.Resolution) error { t.Error("failed close resolved a card"); return nil }}
	}
	ft.d.machines["mac"].call = func(method string, _ json.RawMessage) (any, error) {
		if method != "session.close" {
			return map[string]any{}, nil
		}
		var records map[string]agentArchive
		if _, err := database.Setting(context.Background(), agentArchivesKey, &records); err != nil {
			t.Fatal(err)
		}
		rec := records[runtimeListID("mac", row.SessionID)]
		if !rec.Archived || !rec.ClosedAt.After(time.Now()) {
			t.Fatal("no persisted close fence before failing RPC")
		}
		cancel()
		return nil, errors.New("close refused")
	}
	if err := ft.s.forceArchiveConfirmation(ctx, "mac", row.SessionID); err == nil || !strings.Contains(err.Error(), "close refused") {
		t.Fatalf("lost close failure: %v", err)
	}
	var records map[string]agentArchive
	if _, err := database.Setting(context.Background(), agentArchivesKey, &records); err != nil {
		t.Fatal(err)
	}
	if _, ok := records[runtimeListID("mac", row.SessionID)]; ok {
		t.Fatal("failed close left a durable fence")
	}
	if len(ft.s.pendingInteractions()) != 3 {
		t.Fatal("failed close removed cards")
	}
	for _, s := range []*Service{ft.s, newFleetTestOn(t, database).s} {
		if archivedIDs(t, s)[row.SessionID] {
			t.Fatal("failed close hid the Agent after restart")
		}
	}
	rows := ft.s.decorated("mac")
	if len(rows) != 1 || rows[0].SessionID != row.SessionID || rows[0].Archived {
		t.Fatalf("Agent not visible: %+v", rows)
	}
	for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
		status, r, err := database.InteractionOutcome(context.Background(), "failed-"+kind)
		if err != nil || status != "pending" || r != nil {
			t.Fatalf("failed close changed stored card: %s %+v %v", status, r, err)
		}
	}
	t.Log("cancelled close request: durable fence removed, Agent visible, permission/plan/screen still pending")
}

func TestForceArchiveFenceWriteFailureKeepsAgent(t *testing.T) {
	ft, row := closeTestService(t, func(s *proto.Session) { s.Status = proto.StatusBlocked; s.State = proto.StateNeedsYou })
	store := &archiveFailStore{fleetMem: ft.st, failWrite: true}
	ft.s.store = func() Store { return store }
	ix := proto.Interaction{ID: "keep-card", MachineID: "mac", SessionID: row.SessionID, Kind: proto.IxScreen}
	ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix}
	if err := ft.s.forceArchiveConfirmation(context.Background(), "mac", row.SessionID); err == nil {
		t.Fatal("failed fence write claimed success")
	}
	if len(ft.d.callsTo("mac", "session.close")) != 0 || len(ft.s.pendingInteractions()) != 1 || archivedIDs(t, ft.s)[row.SessionID] {
		t.Fatal("failed fence write touched Agent or card")
	}
}

// #4941: a forced archive of a confirmation-blocked Agent denies its permission and plan cards and dismisses its screen
// card, approving nothing; a working Agent with the same cards is refused and keeps them.
func TestForceArchiveCardOutcomes(t *testing.T) {
	for _, status := range []string{proto.StatusBlocked, proto.StatusWorking} {
		t.Run(status, func(t *testing.T) {
			ft, row := closeTestService(t, func(s *proto.Session) {
				s.Status, s.State = status, proto.StateNeedsYou
				if status == proto.StatusWorking {
					s.State = proto.StateWorking
				}
			})
			got := map[string]proto.Resolution{}
			for _, kind := range []string{proto.IxPermission, proto.IxPlan, proto.IxScreen} {
				ix := proto.Interaction{ID: "card-" + kind, MachineID: "mac", SessionID: row.SessionID, Kind: kind, ExpiresAt: time.Now().Add(time.Hour)}
				ft.s.ixMapLocked()[ix.ID] = &pending{ix: ix, local: func(r proto.Resolution) error {
					got[kind] = r
					return nil
				}}
			}
			_, err := archiveTool(t, ft, "agent_archive", map[string]any{"machine": "mac", "session_id": row.SessionID, "force": true})
			if status == proto.StatusWorking {
				if err == nil || len(ft.d.callsTo("mac", "session.close")) != 0 || len(got) != 0 || len(ft.s.pendingInteractions()) != 3 {
					t.Fatalf("working Agent: err %v, closes %d, resolved %+v", err, len(ft.d.callsTo("mac", "session.close")), got)
				}
				return
			}
			if err != nil {
				t.Fatalf("force archive: %v", err)
			}
			no := false
			want := map[string]proto.Resolution{
				proto.IxPermission: {Allow: &no},
				proto.IxPlan:       {Approve: &no},
				proto.IxScreen:     {Dismiss: true},
			}
			for kind, w := range want {
				r, ok := got[kind]
				if !ok {
					t.Fatalf("%s card left unresolved: %+v", kind, got)
				}
				if r.Dismiss != w.Dismiss || (r.Allow == nil) != (w.Allow == nil) || r.Allow != nil && *r.Allow ||
					(r.Approve == nil) != (w.Approve == nil) || r.Approve != nil && *r.Approve || r.Always || r.Mode != "" || r.Option != "" || len(r.Keys) != 0 {
					t.Fatalf("%s card resolved as %+v", kind, r)
				}
			}
			if len(ft.s.pendingInteractions()) != 0 {
				t.Fatal("cards remain pending")
			}
		})
	}
}
