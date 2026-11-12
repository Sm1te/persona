package devicewire

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestShareCreationLogPrivacyAndAllowlist(t *testing.T) {
	const id = "10000000-0000-4000-8000-000000000001"
	for _, session := range []string{id, "synthetic-session", "", "bad\nshare_creation forged", strings.Repeat("A", 64), "sk-" + strings.Repeat("A", 48), "/private/workspace", strings.Repeat("x", 2000)} {
		var lines []string
		logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
		ShareCreationLog(logf, session, "node_notify", "send_error")
		if len(lines) != 1 || strings.Count(lines[0], "\n") != 0 || len(lines[0]) > 200 {
			t.Fatal("diagnostic line was not bounded")
		}
		var value map[string]string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[0], "share_creation ")), &value); err != nil || len(value) != 3 {
			t.Fatal("invalid diagnostic schema")
		}
		want := "invalid"
		if session == id || session == "synthetic-session" {
			want = session
		}
		if session == "" {
			want = "pending"
		}
		if value["session_id"] != want || value["step"] != "node_notify" || value["result"] != "send_error" {
			t.Fatal("unsafe diagnostic value")
		}
		ShareCreationLog(logf, id, "node_notify", "raw token error")
		ShareCreationLog(logf, id, "unknown", "send_error")
		if len(lines) != 1 {
			t.Fatal("unallowlisted diagnostic emitted")
		}
	}
	ShareCreationLog(nil, id, "node_notify", "send_error")
}
