package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

func TestAgentNamingSettingsDefaultsAndWrites(t *testing.T) {
	s := newTestService(t, newFakeHub().withMachine())
	v := s.view(context.Background(), false)
	if !v.AgentAutoNaming || v.AgentAutoNamingMax != 1 || v.AgentAutoNamingIntervalHours != 0 {
		t.Fatalf("defaults: %+v", v)
	}
	put := func(body, owner string) (int, assistantView) {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/assistant", strings.NewReader(body))
		if owner != "" {
			r.Header.Set(assistantwire.ManagerHeader, owner)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var out assistantView
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	for _, body := range []string{
		`{"agent_auto_naming_max":0}`, `{"agent_auto_naming_max":1001}`,
		`{"agent_auto_naming_max":1.5}`, `{"agent_auto_naming_interval_hours":-1}`,
		`{"agent_auto_naming_interval_hours":8761}`, `{"agent_auto_naming_interval_hours":1.5}`,
	} {
		if code, _ := put(body, "assistant"); code != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, code)
		}
	}
	if code, _ := put(`{"agent_auto_naming":false}`, ""); code != http.StatusBadRequest {
		t.Fatalf("unnamed manager: %d", code)
	}
	if code, _ := put(`{"agent_auto_naming":false}`, "other"); code != http.StatusForbidden {
		t.Fatalf("other manager: %d", code)
	}
	code, got := put(`{"agent_auto_naming":false,"agent_auto_naming_max":5,"agent_auto_naming_interval_hours":24}`, "assistant")
	if code != http.StatusOK || got.AgentAutoNaming || got.AgentAutoNamingMax != 5 || got.AgentAutoNamingIntervalHours != 24 {
		t.Fatalf("saved: %d %+v", code, got)
	}
	code, got = put(`{"agent_auto_naming":true,"agent_auto_naming_interval_hours":0}`, "assistant")
	if code != http.StatusOK || !got.AgentAutoNaming || got.AgentAutoNamingMax != 5 || got.AgentAutoNamingIntervalHours != 0 {
		t.Fatalf("sparse reset: %d %+v", code, got)
	}
	read := httptest.NewRecorder()
	s.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/assistant", nil))
	var reloaded assistantView
	if err := json.Unmarshal(read.Body.Bytes(), &reloaded); err != nil || reloaded.AgentAutoNamingMax != 5 || !reloaded.AgentAutoNaming {
		t.Fatalf("reload: %v %+v", err, reloaded)
	}
}
