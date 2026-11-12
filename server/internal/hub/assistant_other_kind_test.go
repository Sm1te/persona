package hub

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The manager's refusal of a launcher of another agent kind (#4662) reaches the app as 409 launcher_other_kind, with
// the kinds flat in the body and delivered "none", not as the plain 400 of a bad request.
func TestAssistantOtherKindRefusalIs409WithItsData(t *testing.T) {
	w := httptest.NewRecorder()
	writeAssistantErr(w, &rpc.Error{Code: assistantwire.CodeLauncherOtherKind, Message: "cx runs Codex",
		Data: json.RawMessage(`{"conversation_agent":"claude","launcher":"cx","launcher_agent":"codex","delivered":"none"}`)})
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || body["code"] != "launcher_other_kind" || body["error"] != "cx runs Codex" || body["conversation_agent"] != "claude" ||
		body["launcher"] != "cx" || body["launcher_agent"] != "codex" || body["delivered"] != "none" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
