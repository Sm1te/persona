package node

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

const modelHTTPTestKey = "sk-test-never-shown-0123456789"

// model.http sends the POST as given and brings back the whole streamed answer; the key appears in no log, no file
// under the node's home and no error, whatever fails.
func TestModelHTTPSendsAndNeverKeepsTheKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	stream := "event: message_start\ndata: {\"type\":\"message_start\"}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.Header.Get("x-api-key") != modelHTTPTestKey || string(body) != `{"stream":true}` {
			t.Errorf("upstream got %s %q key ok=%v", r.Method, body, r.Header.Get("x-api-key") == modelHTTPTestKey)
		}
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("retry-after", "7")
		f := w.(http.Flusher)
		for _, part := range strings.SplitAfter(stream, "\n\n") { // in pieces, as a stream comes
			_, _ = io.WriteString(w, part)
			f.Flush()
		}
	}))
	defer srv.Close()

	call := func(url string) (proto.ModelHTTPAnswer, error) {
		raw, _ := json.Marshal(proto.ModelHTTPParams{URL: url, Header: map[string][]string{"X-Api-Key": {modelHTTPTestKey}},
			Body: []byte(`{"stream":true}`), TimeoutMS: 5000})
		out, err := modelHTTP(context.Background(), raw)
		if bytes.Contains(raw, []byte(modelHTTPTestKey)) {
			t.Error("params still hold the key after the call")
		}
		if err != nil {
			return proto.ModelHTTPAnswer{}, err
		}
		return out.(proto.ModelHTTPAnswer), nil
	}
	a, err := call(srv.URL + "/v1/messages")
	if err != nil || a.Status != 200 || string(a.Body) != stream || http.Header(a.Header).Get("Retry-After") != "7" {
		t.Fatalf("answer %d %q %v, err %v", a.Status, a.Body, a.Header, err)
	}

	// Failures: nobody listening, a URL that isn't http(s), params that don't decode.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	var errs []string
	for _, u := range []string{dead.URL + "/v1/messages", "file:///etc/passwd"} {
		if _, err := call(u); err == nil {
			t.Errorf("%s: no error", u)
		} else {
			errs = append(errs, err.Error())
		}
	}
	bad := json.RawMessage(`{"url":1,"header":{"x-api-key":["` + modelHTTPTestKey + `"]}}`)
	if _, err := modelHTTP(context.Background(), bad); err == nil {
		t.Error("bad params: no error")
	} else {
		errs = append(errs, err.Error())
	}
	for _, e := range errs {
		if strings.Contains(e, modelHTTPTestKey) {
			t.Errorf("error shows the key: %s", e)
		}
	}
	if strings.Contains(logs.String(), modelHTTPTestKey) {
		t.Errorf("log shows the key: %s", logs.String())
	}
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(modelHTTPTestKey)) {
				t.Errorf("%s holds the key", p)
			}
		}
		return nil
	})
}

// A model list's GET goes as such, and a redirect comes back as it is: the key never follows it to another host.
func TestModelHTTPGetsAndNeverFollowsARedirect(t *testing.T) {
	var followed bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method %s", r.Method)
		}
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	raw, _ := json.Marshal(proto.ModelHTTPParams{Method: http.MethodGet, URL: srv.URL + "/v1/models", Header: map[string][]string{"X-Api-Key": {modelHTTPTestKey}}})
	out, err := modelHTTP(context.Background(), raw)
	if err != nil || out.(proto.ModelHTTPAnswer).Status != http.StatusFound || followed {
		t.Fatalf("%v %v followed=%v", out, err, followed)
	}
	raw, _ = json.Marshal(proto.ModelHTTPParams{Method: http.MethodDelete, URL: srv.URL})
	if _, err := modelHTTP(context.Background(), raw); err == nil {
		t.Fatal("DELETE went out")
	}
}
