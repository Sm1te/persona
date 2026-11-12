package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
)

// Params json.Marshal wrote go out in the line the envelope's encoder writes for them: HTML characters, U+2028 and
// control characters escaped and invalid UTF-8 replaced in params, an event the encoder would escape or leave out
// written by the encoder, null for nil params and the same error (nothing written) for empty ones.
func TestNotifyMarshaledIsTheSameLine(t *testing.T) {
	values := []any{
		map[string]any{"sessions": []map[string]any{{"title": `<b>fix</b> & "ship" \ it`, "text": "a b c \x00\x1f\x7f", "bad": "\xff\xfe \xe2\x82"}}},
		map[string]any{"sessions": nil}, map[string]any{"sessions": []int{}}, nil, "é", 12.5, []any{true, false, map[string]any{}},
	}
	for _, event := range []string{"sessions.changed", "", "a<b", "é", `q"`} {
		for _, v := range values {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			err = New("t", &got).Notify(event, Marshaled(raw))
			want, werr := oldNotify(event, json.RawMessage(raw))
			if err != nil || werr != nil || !bytes.Equal(got.Bytes(), want) {
				t.Errorf("Notify(%q, %s):\n%q, %v\nwas %q, %v", event, raw, got.Bytes(), err, want, werr)
			}
		}
	}
	for _, raw := range []Marshaled{nil, {}} { // null, and an error
		var got bytes.Buffer
		err := New("t", &got).Notify("e", raw)
		want, werr := oldNotify("e", json.RawMessage(raw))
		if !bytes.Equal(got.Bytes(), want) || fmt.Sprint(err) != fmt.Sprint(werr) {
			t.Errorf("Notify of %#v wrote %q, %v; was %q, %v", raw, got.Bytes(), err, want, werr)
		}
	}
	if b, err := json.Marshal(map[string]any{"p": Marshaled(`{"a":1}`)}); err != nil || string(b) != `{"p":{"a":1}}` {
		t.Errorf("Marshaled inside a value: %s, %v", b, err)
	}
}

// Sending a 260 KiB session list json.Marshal wrote allocates its line, nothing more: as json.RawMessage it was 418 KB
// (#2900), the envelope's encoder copying the list into buffers it grows. The bound is the line plus 3%.
func TestNotifyMarshaledAllocatesTheLine(t *testing.T) {
	p, raw := New("t", io.Discard), marshaledList(260<<10)
	line := len(`{"event":"sessions","params":}`+"\n") + len(raw)
	per := testing.AllocsPerRun(20, func() { _ = p.Notify("sessions", raw) })
	if per > 2 { // the line, and the params' interface value
		t.Errorf("a Notify of %d bytes json.Marshal wrote allocated %.0f times, want at most twice", len(raw), per)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 20 {
		_ = p.Notify("sessions", raw)
	}
	runtime.ReadMemStats(&after)
	if per, most := float64(after.TotalAlloc-before.TotalAlloc)/20, 1.03*float64(line); per > most {
		t.Errorf("a Notify of a %d-byte line allocated %.0f bytes, want at most %.0f", line, per, most)
	}
}

// marshaledList is sessionList(size) as json.Marshal writes it (its HTML characters escaped).
func marshaledList(size int) Marshaled {
	b, err := json.Marshal(sessionList(size))
	if err != nil {
		panic(err)
	}
	return b
}

// BenchmarkNotifyMarshaled sends the session lists of BenchmarkNotifyRaw as json.Marshal wrote them, as a node sends
// sessions.changed.
func BenchmarkNotifyMarshaled(b *testing.B) {
	for _, size := range []int{39 << 10, 260 << 10} {
		b.Run(fmt.Sprintf("%dK", size>>10), func(b *testing.B) {
			p, raw := New("t", io.Discard), marshaledList(size)
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			for range b.N {
				if err := p.Notify("sessions", raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// oldCall is the line Call wrote before it took Appended params, and writes for a value still: params through the
// encoder, then the envelope and its newline.
func oldCall(id, method string, params any) ([]byte, error) {
	raw, err := marshal(params)
	if err != nil {
		return nil, err
	}
	return encodeLine(message{ID: id, Method: method, Params: raw})
}

// lineCatcher keeps what a peer writes and closes it then, so a Call returns without an answer.
type lineCatcher struct {
	p    *Peer
	line []byte
}

func (c *lineCatcher) Write(b []byte) (int, error) {
	c.line = append(c.line, b...)
	c.p.Close(io.EOF)
	return len(b), nil
}

// callWrote is the line one Call of method with params writes on a peer with the frame budget given, or Call's error.
func callWrote(method string, params any, budget int) ([]byte, error) {
	w := &lineCatcher{}
	w.p = New("t", w)
	w.p.Limits.FrameBytes = budget
	if err := w.p.Call(context.Background(), method, params, nil); !errors.Is(err, ErrDropped) {
		return w.line, err
	}
	return w.line, nil
}

// appended is raw as Appended params.
func appended(raw []byte) Appended {
	return Appended{Size: len(raw), Append: func(b []byte) []byte { return append(b, raw...) }}
}

// Appended params Marshal wrote go out in the line Call writes for the same value: HTML characters as they are,
// U+2028 and control characters escaped and invalid UTF-8 replaced in params, a method the encoder would escape or
// leave out written by the encoder, and the same error (nothing written) for empty ones.
func TestCallAppendedIsTheSameLine(t *testing.T) {
	values := []any{
		map[string]any{"header": map[string][]string{"X-A": {`<b>&</b>`, "  ", "\xff\xfe", "日本"}}, "body": []byte{0, 1, 2}},
		nil, "é", 12.5, []any{true, map[string]any{}},
	}
	for _, method := range []string{"http.request", "", "a<b", "é", `q"`} {
		for _, v := range values {
			raw, err := Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			got, err := callWrote(method, appended(raw), 0)
			want, werr := oldCall("t1", method, v)
			value, verr := callWrote(method, v, 0) // and what Call writes for the value now
			if err != nil || werr != nil || verr != nil || !bytes.Equal(got, want) || !bytes.Equal(got, value) {
				t.Errorf("Call(%q, %s):\n%q, %v\nwas %q, %v\nas a value %q, %v", method, raw, got, err, want, werr, value, verr)
			}
		}
	}
	got, err := callWrote("m", appended([]byte{}), 0)
	_, werr := oldCall("t1", "m", json.RawMessage{})
	if len(got) != 0 || err == nil || fmt.Sprint(err) != fmt.Sprint(werr) {
		t.Errorf("empty Appended params: wrote %q, %v; was %v", got, err, werr)
	}
}

// The frame budget holds for Appended params before their line is made: a line of exactly the budget goes out, one
// byte more is the budget's error with nothing written, as for the same params as a value, and Append never runs.
func TestCallAppendedFrameBudget(t *testing.T) {
	raw := []byte(`{"body":"` + strings.Repeat("QUJD", 64) + `"}`)
	line, err := oldCall("t1", "http.request", json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{len(line), len(line) - 1} {
		ran := false
		a := appended(raw)
		add := a.Append
		a.Append = func(b []byte) []byte { ran = true; return add(b) }
		got, err := callWrote("http.request", a, budget)
		value, verr := callWrote("http.request", json.RawMessage(raw), budget)
		fits := budget == len(line)
		if !bytes.Equal(got, value) || err != verr || (fits && (err != nil || !bytes.Equal(got, line))) ||
			(!fits && (err != errFrameBudget || len(got) != 0 || ran)) {
			t.Errorf("budget %d for a %d-byte line: wrote %d bytes, %v (Append ran: %v); as a value %d bytes, %v",
				budget, len(line), len(got), err, ran, len(value), verr)
		}
	}
}
