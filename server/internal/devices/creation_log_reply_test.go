package devices

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/devicewire"
)

func TestShareCreationLogReplyDoesNotGrantOwnership(t *testing.T) {
	for _, mode := range []string{"missing", "mismatch", "failed"} {
		t.Run(mode, func(t *testing.T) {
			var logs []string
			d := New(Options{Logf: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }})
			prepared := json.RawMessage(`{"creation_id":"private-expected","incarnation":"private-incarnation"}`)
			out := json.RawMessage(`{"session":{"session_id":"synthetic-reply"}}`)
			var callErr error
			want := "receipt_missing"
			if mode == "mismatch" {
				out = json.RawMessage(`{"session":{"session_id":"synthetic-reply"},"creation_id":"private-different","incarnation":"private-incarnation"}`)
				want = "receipt_mismatch"
			}
			if mode == "failed" {
				callErr, want = errors.New("token=private-error"), "start_failed"
			}
			d.observeCreationCall(devicewire.NodeCall{Method: "session.start"}, prepared, out, "intent_created", callErr)
			joined := strings.Join(logs, "\n")
			if !strings.Contains(joined, `"result":"`+want+`"`) || strings.Contains(joined, "private-") {
				t.Fatal("missing or private reply diagnostic")
			}
		})
	}
}
