package hub

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkReadRelayed reads a 22.4 MB request body for the bus: without a Content-Length, as io.ReadAll reads it
// (how every body was read before), and with it, into one buffer of that size.
func BenchmarkReadRelayed(b *testing.B) {
	body := largePrompt(16 << 20)
	for _, c := range []struct {
		name string
		size int64
	}{{"length=unknown", -1}, {"length=known", int64(len(body))}} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/conversations", bytes.NewReader(body))
				r.ContentLength = c.size
				got, err := readRelayed(httptest.NewRecorder(), r)
				if err != nil || len(got) != len(body) {
					b.Fatal(len(got), err)
				}
			}
		})
	}
}
