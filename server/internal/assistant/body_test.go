package assistant

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/proto"
)

// startBody is what POST /api/v1/assistant/conversations decodes (start.go).
type startBody struct {
	Prompt         string        `json:"prompt"`
	Images         []proto.Image `json:"images"`
	Launcher       string        `json:"launcher"`
	PermissionMode string        `json:"permission_mode"`
	Effort         string        `json:"effort"`
	Main           bool          `json:"main"`
	Purpose        string        `json:"purpose"`
	MachineID      string        `json:"machine_id"`
	Space          string        `json:"space"`
	ReportTasks    bool          `json:"report_tasks"`
	Guest          *GuestSpec    `json:"guest"`
}

// decoded is what came of decoding a body: the value, the error, and the response a route answers with it
// (apierr.Write 400, as start.go and kb_recordings.go do).
type decoded struct {
	value  any
	err    string
	status int
	header http.Header
	body   string
}

// decodeWith decodes body, of a request that says its length, into *v with decode.
func decodeWith[T any](decode func(http.ResponseWriter, *http.Request, any, int64) error, body io.ReadCloser, n int, v T, limit int64) decoded {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/conversations", nil)
	r.Body, r.ContentLength = body, int64(n)
	w := httptest.NewRecorder()
	err := decode(w, r, &v, limit)
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, err)
	}
	return decoded{v, fmt.Sprintf("%T %v", err, err), w.Code, w.Header(), w.Body.String()}
}

// sameAsDecodeBody: decodeLargeBody of a relayed body says what decodeBody (encoding/json's Decoder) says of it.
func sameAsDecodeBody[T any](t *testing.T, b []byte, v T, limit int64) {
	t.Helper()
	got := decodeWith(decodeLargeBody, newRelayedBody(b), len(b), v, limit)
	want := decodeWith(decodeBody, io.NopCloser(bytes.NewReader(b)), len(b), v, limit)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%T of %q (limit %d):\n got %#v\nwant %#v", v, b, limit, got, want)
	}
}

func TestDecodeLargeBodyIsDecodeBody(t *testing.T) {
	for _, c := range []struct {
		name    string
		body    string
		limit   func(n int64) int64 // of a body of n bytes; nil: 1 MiB
		inPlace bool                // decoded where it is, into both (else decodeBody does it)
	}{
		{"valid", `{"prompt":"hi","purpose":"none","images":[{"mime":"image/png","data":"AAEC"}],"guest":{"tools":["a","b"]}}`, nil, true},
		{"valid recording", `{"revision":3,"started_at":"2024-05-01T10:00:00Z","duration":1.5,"segments":[{"start":0,"end":1.5,"text":"x"}],"complete":true}`, nil, true},
		{"other names", `{"PROMPT":"a","Revision":2,"report_Tasks":true}`, nil, true},
		{"escapes", `{"prompt":"\u2028\u2029\ud800x\"\\\/\u00e9","title":"\ud83d\ude00"}`, nil, true},
		{"null", `null`, nil, true},
		{"null field", `{"guest":null,"ended_at":null}`, nil, true},
		{"trailing data", `{"prompt":"a"} {"prompt":"b"}`, nil, true},
		{"trailing garbage", `{"prompt":"a"}}garbage`, nil, true},
		{"trailing whitespace", "{\"prompt\":\"a\"} \n\t\r", nil, true},
		{"leading whitespace", " \n{\"prompt\":\"a\"}", nil, true},
		{"syntax error", `{"prompt":"a",}`, nil, false},
		{"cut short", `{"prompt":`, nil, false},
		{"open", `{`, nil, false},
		{"bad literal", `nul`, nil, false},
		{"control character", "{\"prompt\":\"a\x01\"}", nil, false},
		{"type error", `{"prompt":1,"purpose":"after"}`, nil, false},
		{"type error inside", `{"guest":{"tools":"a"},"revision":"x","title":"after"}`, nil, false},
		{"bad time", `{"started_at":"yesterday","title":"after"}`, nil, false},
		{"too large a number", `{"revision":12345678901234567890,"duration":1e400}`, nil, false},
		{"array", `[]`, nil, false},
		{"string", `"str"`, nil, false},
		{"unknown field", `{"nope":{"deep":[1,2]},"prompt":"a"}`, nil, true},
		{"duplicate key", `{"prompt":"a","prompt":"b","guest":{"tools":["a"]},"guest":{"tools":["b"]}}`, nil, true},
		{"invalid UTF-8", "{\"prompt\":\"a\xffb\xc3\",\"pro\xffmpt\":\"c\"}", nil, true},
		{"empty body", ``, nil, false},
		{"only whitespace", " \n ", nil, false},
		{"at the limit", `{"prompt":"at the limit"}`, func(n int64) int64 { return n }, true},
		{"over the limit", `{"prompt":"over the limit"}`, func(n int64) int64 { return n - 1 }, false},
		{"over the limit, first value within", `{"prompt":"a"}  {"prompt":"b"}`, func(n int64) int64 { return n - 10 }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, limit := []byte(c.body), int64(1<<20)
			if c.limit != nil {
				limit = c.limit(int64(len(b)))
			}
			sameAsDecodeBody(t, b, startBody{}, limit)
			sameAsDecodeBody(t, b, kb.RecordingUpload{}, limit)
			sameAsDecodeBody[any](t, b, nil, limit)
			sameAsDecodeBody(t, b, map[string]string(nil), limit)
			if inPlace := int64(len(b)) <= limit && decodeInPlace(b, new(startBody)) == nil && decodeInPlace(b, new(kb.RecordingUpload)) == nil; inPlace != c.inPlace {
				t.Errorf("decoded in place: %v, want %v", inPlace, c.inPlace)
			}
		})
	}
}

// TestDecodeLargeBodyOtherwise: a value already set, a body read already, or one that is not relayed: decodeBody.
func TestDecodeLargeBodyOtherwise(t *testing.T) {
	set := startBody{Space: "health", Purpose: "set"}
	for _, b := range []string{`{"prompt":"a"}`, `{"prompt":"a","purpose":1}`, `{"guest":{"tools":["a"]}}`} {
		sameAsDecodeBody(t, []byte(b), set, 1<<20)

		read := newRelayedBody([]byte(b))
		_, _ = read.Read(make([]byte, 1))
		got := decodeWith(decodeLargeBody, read, len(b), startBody{}, 1<<20)
		want := decodeWith(decodeBody, io.NopCloser(bytes.NewReader([]byte(b)[1:])), len(b), startBody{}, 1<<20)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("read already %q:\n got %#v\nwant %#v", b, got, want)
		}

		got = decodeWith(decodeLargeBody, io.NopCloser(bytes.NewReader([]byte(b))), len(b), startBody{}, 1<<20)
		want = decodeWith(decodeBody, io.NopCloser(bytes.NewReader([]byte(b))), len(b), startBody{}, 1<<20)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("not relayed %q:\n got %#v\nwant %#v", b, got, want)
		}
	}
}

// TestRelayedBodyReads: a relayed request's body reads as before (bytes.NewReader), and is read once decoded.
func TestRelayedBodyReads(t *testing.T) {
	body := []byte(`{"prompt":"a"} trailing`)
	seen := map[string]string{}
	for _, p := range []assistantwire.HTTPRequest{
		{Method: http.MethodPost, Path: "/read", Body: body},
		{Method: http.MethodPost, Path: "/copy", Body: body},
		{Method: http.MethodPost, Path: "/decode", Body: body},
		{Method: http.MethodPost, Path: "/empty"},
	} {
		_, err := serveHTTP(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b bytes.Buffer
			var err error
			switch r.URL.Path {
			case "/read":
				_, err = b.ReadFrom(struct{ io.Reader }{r.Body})
			case "/copy":
				_, err = io.Copy(&b, r.Body)
			case "/decode":
				var in startBody
				err = decodeLargeBody(w, r, &in, 1<<20)
				b.WriteString(in.Prompt + " then ")
				_, _ = b.ReadFrom(r.Body)
			case "/empty":
				_, err = b.ReadFrom(r.Body)
				if r.Body != http.NoBody {
					err = fmt.Errorf("body %T", r.Body)
				}
			}
			seen[r.URL.Path] = fmt.Sprintf("%T %q %v %d", r.Body, b.String(), err, r.ContentLength)
		}), p, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{
		"/read":   `*assistant.relayedBody "{\"prompt\":\"a\"} trailing" <nil> 23`,
		"/copy":   `*assistant.relayedBody "{\"prompt\":\"a\"} trailing" <nil> 23`,
		"/decode": `*assistant.relayedBody "a then " <nil> 23`,
		"/empty":  `http.noBody "" <nil> 0`,
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("got %q\nwant %q", seen, want)
	}
}

// TestDecodeLargeBodyCopiesOnce: a relayed 5.6 MB new conversation is decoded where it is, its prompt the one copy (the
// Decoder read it again, into buffers it grows: four times the body).
func TestDecodeLargeBodyCopiesOnce(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector's allocations")
	}
	p := assistantwire.HTTPRequest{Method: http.MethodPost, Path: "/api/v1/assistant/conversations", Body: largeStart(4 << 20)}
	var least uint64
	defer debug.SetGCPercent(debug.SetGCPercent(-1)) // no GC empties a pool in the middle of a run
	for i := range 3 {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		res, err := serveHTTP(context.Background(), startDecoder, p, nil)
		runtime.ReadMemStats(&after)
		if err != nil || res.Status != http.StatusBadRequest {
			t.Fatal(res.Status, err)
		}
		if n := after.TotalAlloc - before.TotalAlloc; i == 0 || n < least {
			least = n
		}
	}
	if most := uint64(len(p.Body))*103/100 + 256<<10; least > most {
		t.Errorf("decoding a relayed %d-byte body allocated %d bytes, want at most %d", len(p.Body), least, most)
	}
}
