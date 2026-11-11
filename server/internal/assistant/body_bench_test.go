package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
)

// largeStart is a new conversation's body with n bytes in base64 as its prompt (an attached file): 16 MiB make the
// 22.4 MB body of a large upload.
func largeStart(n int) []byte {
	raw := make([]byte, n)
	for i := range raw {
		raw[i] = byte(i*7 + i>>8)
	}
	b, _ := json.Marshal(map[string]string{"prompt": base64.StdEncoding.EncodeToString(raw), "purpose": "none"})
	return b
}

// startDecoder is a route that decodes a new conversation's body as POST /api/v1/assistant/conversations does, and
// answers 400 (as for a purpose that does not exist).
var startDecoder = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	var in struct{ Prompt, Purpose string }
	if err := decodeLargeBody(w, r, &in, 32<<20); err != nil || in.Purpose != "none" {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
})

// BenchmarkDecode22MB serves a relayed 22.4 MB new conversation (16 MiB of base64 in its prompt) to a route that
// decodes it: the manager's side of one large upload, after the bus.
func BenchmarkDecode22MB(b *testing.B) {
	p := assistantwire.HTTPRequest{Method: http.MethodPost, Path: "/api/v1/assistant/conversations", Body: largeStart(16 << 20)}
	b.SetBytes(int64(len(p.Body)))
	b.ReportAllocs()
	for b.Loop() {
		res, err := serveHTTP(context.Background(), startDecoder, p, nil)
		if err != nil || res.Status != http.StatusBadRequest {
			b.Fatal(res.Status, err)
		}
	}
}

// BenchmarkDecode1K serves a relayed 1 KiB new conversation to the same route, as most are: decoding a large upload
// where it is must not cost the small ones.
func BenchmarkDecode1K(b *testing.B) {
	p := assistantwire.HTTPRequest{Method: http.MethodPost, Path: "/api/v1/assistant/conversations", Body: largeStart(700)}
	b.SetBytes(int64(len(p.Body)))
	b.ReportAllocs()
	for b.Loop() {
		res, err := serveHTTP(context.Background(), startDecoder, p, nil)
		if err != nil || res.Status != http.StatusBadRequest {
			b.Fatal(res.Status, err)
		}
	}
}
