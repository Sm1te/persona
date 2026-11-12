package rpc

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"testing"
)

// uploadRequest is an http.request's params as far as their size goes: a few short fields, then the body.
type uploadRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   []byte `json:"body,omitempty"`
}

// dropAfterWrite closes its peer once a line is written, so the Call returns without an answer.
type dropAfterWrite struct{ p *Peer }

func (d *dropAfterWrite) Write(b []byte) (int, error) {
	d.p.Close(io.EOF)
	return len(b), nil
}

// BenchmarkCallLarge writes one request whose params carry a 22.4 MB body (a 29.8 MB line), as a value the encoder
// writes and as Appended params: what a relayed upload costs in rpc, each way.
func BenchmarkCallLarge(b *testing.B) {
	body := make([]byte, 22_369_743)
	for i := range body {
		body[i] = byte(i*7 + i>>8)
	}
	req := uploadRequest{Method: "POST", Path: "/api/v1/assistant/conversations", Body: body}
	head, err := Marshal(uploadRequest{Method: req.Method, Path: req.Path})
	if err != nil {
		b.Fatal(err)
	}
	head = head[:len(head)-1]
	const mid, tail = `,"body":"`, `"}`
	appended := Appended{
		Size: len(head) + len(mid) + base64.StdEncoding.EncodedLen(len(body)) + len(tail),
		Append: func(line []byte) []byte {
			line = append(append(line, head...), mid...)
			return append(base64.StdEncoding.AppendEncode(line, body), tail...)
		},
	}
	for _, c := range []struct {
		name   string
		params any
	}{{"params=value", req}, {"params=appended", appended}} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				w := &dropAfterWrite{}
				w.p = New("t", w)
				if err := w.p.Call(context.Background(), "http.request", c.params, nil); !errors.Is(err, ErrDropped) {
					b.Fatal(err)
				}
			}
		})
	}
}
