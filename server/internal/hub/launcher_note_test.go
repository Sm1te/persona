package hub

import (
	"net/http"
	"testing"
)

// A delivered send's answer carries the manager's launcher_note (#4659); a failed one, or one without a note, is as the
// node answered.
func TestSendAnswerCarriesTheLauncherNote(t *testing.T) {
	if got := string(withLauncherNote(http.StatusOK, []byte("{}\n"), "wrap can't run it here")); got != "{\"launcher_note\":\"wrap can't run it here\"}\n" {
		t.Fatalf("delivered: %s", got)
	}
	if got := string(withLauncherNote(http.StatusOK, []byte(`{"queued":true}`), "n")); got != "{\"launcher_note\":\"n\",\"queued\":true}\n" {
		t.Fatalf("with the node's fields: %s", got)
	}
	for _, c := range []struct {
		status int
		body   string
		note   string
	}{{http.StatusOK, "{}\n", ""}, {http.StatusBadGateway, `{"error":"x"}`, "n"}} {
		if got := string(withLauncherNote(c.status, []byte(c.body), c.note)); got != c.body {
			t.Errorf("%d %q: %s", c.status, c.note, got)
		}
	}
}
