package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nwverse/persona/server/internal/rpc"
)

// largePrompt is a new conversation's body with n bytes in base64 as its prompt (an attached file): 16 MiB make the
// 22.4 MB body of a large upload.
func largePrompt(n int) []byte {
	raw := make([]byte, n)
	for i := range raw {
		raw[i] = byte(i*7 + i>>8)
	}
	b, _ := json.Marshal(map[string]string{"prompt": base64.StdEncoding.EncodeToString(raw), "purpose": "none"})
	return b
}

// answeringManager is a hub whose manager answers every request at once with a 400 and nothing else, reading no more
// of the line than its id: what a relay costs is the hub's side alone.
func answeringManager(tb testing.TB) *Hub {
	tb.Helper()
	in, out := io.Pipe()
	hp := rpc.New("h", answerAtOnce{out})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = hp.Serve(ctx, in) }()
	tb.Cleanup(func() { cancel(); hp.Close(io.EOF); _ = out.Close(); _ = in.Close() })
	h := &Hub{logger: log.New(io.Discard, "", 0)}
	h.bus.current = map[string]*busConn{busAssistant: {peer: hp}}
	return h
}

// answerAtOnce answers each request line written to it with {"id":…,"result":{"status":400}}.
type answerAtOnce struct{ w io.Writer }

func (a answerAtOnce) Write(line []byte) (int, error) {
	id, _, _ := bytes.Cut(bytes.TrimPrefix(line, []byte(`{"id":"`)), []byte(`"`))
	_, err := fmt.Fprintf(a.w, `{"id":%q,"result":{"status":400}}`+"\n", id)
	return len(line), err
}

// relayOnce relays body as an app's POST of a new conversation, with its Content-Length.
func relayOnce(tb testing.TB, h *Hub, body []byte) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/conversations", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.relayHTTP(w, r, busAssistant, true, false)
	if w.Code != http.StatusBadRequest {
		tb.Fatalf("relayed: %d %s", w.Code, w.Body)
	}
}

// BenchmarkRelay22MB relays a 22.4 MB request body (16 MiB of base64 in its prompt) to a manager that answers at
// once: the slot's side of one large upload.
func BenchmarkRelay22MB(b *testing.B) {
	h, body := answeringManager(b), largePrompt(16<<20)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		relayOnce(b, h, body)
	}
}

// BenchmarkRelay1K relays a 1 KiB request body, as most requests have: the relay of a large upload must not cost
// the small ones.
func BenchmarkRelay1K(b *testing.B) {
	h, body := answeringManager(b), largePrompt(700)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		relayOnce(b, h, body)
	}
}
