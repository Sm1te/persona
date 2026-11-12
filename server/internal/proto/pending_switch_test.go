package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Session.pending_switch (#5035) on the wire: the launcher and the time always, model and effort only when picked;
// nothing for a session with no switch waiting.
func TestPendingSwitchWire(t *testing.T) {
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	b, err := json.Marshal(Session{SessionID: "s1", PendingSwitch: &PendingSwitch{Launcher: "work", Model: "opus", At: at}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"pending_switch":{"launcher":"work","model":"opus","at":"2026-11-12T10:00:00Z"}`; !strings.Contains(string(b), want) {
		t.Fatalf("%s lacks %s", b, want)
	}
	if b, _ = json.Marshal(Session{SessionID: "s1"}); strings.Contains(string(b), "pending_switch") {
		t.Fatalf("a session with no switch has one: %s", b)
	}
	var back Session
	if err := json.Unmarshal(b, &back); err != nil || back.PendingSwitch != nil {
		t.Fatalf("decoded %+v %v", back.PendingSwitch, err)
	}
}
