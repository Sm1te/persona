package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// lockedLog is a plugin's log a test reads once the read-aloud is over.
type lockedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// speakPlugin is a plugin with cfg's upstreams and no hub (a read-aloud needs none), its log kept.
func speakPlugin(t *testing.T, cfg Config) (*Plugin, *httptest.Server, *lockedLog) {
	t.Helper()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	logs := &lockedLog{}
	pl := New(cfg, "http://127.0.0.1:1", "hubtoken", log.New(logs, "", 0))
	srv := httptest.NewServer(pl.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(func() { waitFor(t, "every read-aloud to end", func() bool { return pl.active.Load() == 0 }) })
	return pl, srv, logs
}

// speakTunables sets the read-aloud's waits for one test.
func speakTunables(t *testing.T, first, quiet time.Duration) {
	t.Helper()
	was := [2]time.Duration{speakFirstAudio, speakQuiet}
	speakFirstAudio, speakQuiet = first, quiet
	t.Cleanup(func() { speakFirstAudio, speakQuiet = was[0], was[1] })
}

// speak posts body to /speak as role (with the hub's language header), and reads the whole answer.
func speak(t *testing.T, srv *httptest.Server, role string, body any) (*http.Response, []byte) {
	t.Helper()
	raw, ok := body.(string)
	if !ok {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/speak", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set(extwire.HeaderRole, role)
	}
	req.Header.Set(extwire.HeaderLang, "en")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res, out
}

func errorCode(t *testing.T, b []byte) string {
	t.Helper()
	var e struct{ Error, Code string }
	if err := json.Unmarshal(b, &e); err != nil || e.Error == "" {
		t.Fatalf("not an error body: %s", b)
	}
	return e.Code
}

// started answers a live session start the way the measured service does, at 24 kHz.
func started(u *fakeUpstream, c *websocket.Conn) {
	u.send(c, map[string]any{"type": "session.started", "session": map[string]any{"audio": map[string]any{
		"format": map[string]any{"type": "audio/pcm", "rate": 16000}, "output": map[string]any{"format": map[string]any{"rate": 24000}}}}})
}

func audioDelta(pcm []byte) map[string]any {
	return map[string]any{"type": "session.output_audio.delta", "delta": b64(pcm)}
}

func replyEvent(typ string) map[string]any {
	return map[string]any{"type": "response.event", "delegation_id": "r1", "event": map[string]any{"type": typ}}
}

// The text goes to a session of its own, with the read-aloud instructions and no tools, and its reply's audio comes
// back as it is, at the session's rate; the session closes once the reply is over. The text is never logged.
func TestSpeakStreamsTheVoice(t *testing.T) {
	const text = "The regression tests are Bo's, due ZEBRA Friday."
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			started(u, c)
		case "session.commentary.append":
			u.send(c, replyEvent("response.created"))
			u.send(c, map[string]any{"type": "session.output_transcript.delta", "delta": "The regression"})
			u.send(c, audioDelta([]byte{1, 2, 3, 4}))
			u.send(c, audioDelta([]byte{5, 6}))
			u.send(c, replyEvent("response.completed"))
		}
	})
	pl, srv, logs := speakPlugin(t, Config{Owner: "Alex", Notes: "never say these notes",
		Providers: []Provider{{ID: "rt", URL: up.url(), Key: "k"}}})
	res, body := speak(t, srv, roleFull, map[string]any{"text": "  " + text + "\n", "model": "rt/m1", "voice": "v1", "lang": "en-US"})
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "audio/pcm" || res.Header.Get(headerAudioRate) != "24000" ||
		res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("answer %d %v", res.StatusCode, res.Header)
	}
	if !bytes.Equal(body, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("audio %v", body)
	}
	start := up.next("session.start")["session"].(map[string]any)
	instructions, _ := start["instructions"].(string)
	if start["model"] != "m1" || start["tools"] != nil || start["audio"].(map[string]any)["output"].(map[string]any)["voice"] != "v1" ||
		!strings.Contains(instructions, "Your only job is to read texts aloud") || !strings.Contains(instructions, "Alex") ||
		!strings.Contains(instructions, "en-US") || strings.Contains(instructions, "never say these notes") {
		t.Fatalf("session.start: %v", start)
	}
	if f := up.next("session.commentary.append"); f["content"] != text || f["delegation_id"] != nil {
		t.Fatalf("the text: %v", f)
	}
	up.next("session.close")
	waitFor(t, "the read-aloud to stop counting as open", func() bool { return pl.active.Load() == 0 })
	got := logs.String()
	if strings.Contains(got, "ZEBRA") || !strings.Contains(got, "speak 1: rt/m1, voice v1, 48 characters: done; 6 bytes at 24000 Hz") {
		t.Fatalf("log: %s", got)
	}
}

// A service that never says its reply is over: the reply ends once its audio stops for a while.
func TestSpeakEndsOnQuiet(t *testing.T) {
	speakTunables(t, 5*time.Second, 100*time.Millisecond)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			started(u, c)
		case "session.commentary.append":
			u.send(c, audioDelta([]byte{7, 7}))
		}
	})
	_, srv, logs := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Key: "k"}}})
	res, body := speak(t, srv, "", map[string]any{"text": "好的"}) // no role: an old socket-only deployment, the hub's own
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, []byte{7, 7}) {
		t.Fatalf("answer %d %v", res.StatusCode, body)
	}
	up.next("session.close")
	if !strings.Contains(logs.String(), "done (quiet)") {
		t.Fatalf("log: %s", logs.String())
	}
}

// What it refuses before any session: another extension or an answer-only device, and a body it cannot read.
func TestSpeakRefusesBeforeASession(t *testing.T) {
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) { started(u, c) })
	_, srv, _ := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url()}}})
	for _, tc := range []struct {
		name, role string
		body       any
		status     int
		code       string
	}{
		{"another extension", "extension", map[string]any{"text": "hi"}, http.StatusForbidden, codeForbidden},
		{"an answer-only device", "answer", map[string]any{"text": "hi"}, http.StatusForbidden, codeForbidden},
		{"not JSON", roleFull, "text=hi", http.StatusBadRequest, codeBadRequest},
		{"no text", roleFull, map[string]any{"text": " \n "}, http.StatusBadRequest, codeBadRequest},
		{"too long", roleFull, map[string]any{"text": strings.Repeat("字", speakMostRunes+1)}, http.StatusBadRequest, codeBadRequest},
		{"a malformed model", roleFull, map[string]any{"text": "hi", "model": "m1"}, http.StatusBadRequest, codeBadRequest},
		{"a malformed voice", roleFull, map[string]any{"text": "hi", "voice": "a voice!"}, http.StatusBadRequest, codeBadRequest},
	} {
		res, body := speak(t, srv, tc.role, tc.body)
		if res.StatusCode != tc.status || errorCode(t, body) != tc.code || res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s", tc.name, res.StatusCode, body)
		}
	}
	if n := up.count(func(map[string]any) bool { return true }); n != 0 {
		t.Fatalf("the service got %d frames", n)
	}
}

// Without an upstream set up: 503 no_upstream, as a call's error.
func TestSpeakWithoutAnUpstream(t *testing.T) {
	cfg, err := Merge(Sources{Form: json.RawMessage(`{"url":"wss://live.example.com/v1/live"}`)})
	if err != nil {
		t.Fatal(err)
	}
	pl := New(cfg, "http://127.0.0.1:1", "hubtoken", log.New(io.Discard, "", 0))
	srv := httptest.NewServer(pl.Handler())
	defer srv.Close()
	res, body := speak(t, srv, roleFull, map[string]any{"text": "hi"})
	if res.StatusCode != http.StatusServiceUnavailable || errorCode(t, body) != codeNoUpstream {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

// The service refuses before any audio: the phone is told by the calls' codes, and falls back to its own voice.
func TestSpeakUpstreamFailures(t *testing.T) {
	var status atomic.Int32
	srvUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"no"}}`, int(status.Load()))
	}))
	defer srvUp.Close()
	url := "ws" + strings.TrimPrefix(srvUp.URL, "http") + "/v1/live"
	// The session's handshake (a model given), and the service's model list (none given: its default is read).
	_, listed, logs := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: url, Key: "secret-key", Models: []string{"m1"}}}})
	_, unlisted, _ := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: url, Key: "secret-key"}}})
	for _, srv := range []*httptest.Server{listed, unlisted} {
		status.Store(http.StatusUnauthorized)
		res, body := speak(t, srv, roleFull, map[string]any{"text": "hi"})
		if res.StatusCode != http.StatusBadGateway || errorCode(t, body) != codeUpstreamForbidden {
			t.Fatalf("a refused key: %d %s", res.StatusCode, body)
		}
		status.Store(http.StatusServiceUnavailable)
		res, body = speak(t, srv, roleFull, map[string]any{"text": "hi"})
		if res.StatusCode != http.StatusServiceUnavailable || errorCode(t, body) != codeUpstreamUnavailable {
			t.Fatalf("a service that is down: %d %s", res.StatusCode, body)
		}
		status.Store(http.StatusBadRequest)
		res, body = speak(t, srv, roleFull, map[string]any{"text": "hi"})
		if res.StatusCode != http.StatusBadGateway || errorCode(t, body) != codeUpstreamFailed || strings.Contains(string(body), srvUp.URL[7:]) {
			t.Fatalf("another refusal: %d %s", res.StatusCode, body)
		}
	}
	if got := logs.String(); strings.Contains(got, "secret-key") || strings.Contains(got, srvUp.URL[7:]) ||
		!strings.Contains(got, "failed, 502 upstream_forbidden") {
		t.Fatalf("log: %s", got)
	}

	// The session starts, then the reply fails before any audio.
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			started(u, c)
		case "session.commentary.append":
			u.send(c, map[string]any{"type": "error", "error": map[string]any{"message": "boom", "code": "server_error"}})
		}
	})
	_, srv, _ := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Key: "k"}}})
	res, body := speak(t, srv, roleFull, map[string]any{"text": "hi"})
	if res.StatusCode != http.StatusBadGateway || errorCode(t, body) != codeUpstreamFailed {
		t.Fatalf("an error before audio: %d %s", res.StatusCode, body)
	}
}

// A service that refuses the session for its voice (a voice of another model) reads it in its default voice.
func TestSpeakRetriesWithoutTheVoice(t *testing.T) {
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			audio, _ := f["session"].(map[string]any)["audio"].(map[string]any)
			if _, voiced := audio["output"]; voiced {
				u.send(c, map[string]any{"type": "error", "error": map[string]any{"message": "unknown voice", "code": "invalid_value",
					"param": "session.audio.output.voice"}})
				return
			}
			started(u, c)
		case "session.commentary.append":
			u.send(c, audioDelta([]byte{3, 3}))
			u.send(c, replyEvent("response.completed"))
		}
	})
	_, srv, logs := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Key: "k"}}})
	res, body := speak(t, srv, roleFull, map[string]any{"text": "hi", "voice": "v_gone"})
	if res.StatusCode != http.StatusOK || !bytes.Equal(body, []byte{3, 3}) {
		t.Fatalf("answer %d %v", res.StatusCode, body)
	}
	if n := up.count(func(f map[string]any) bool { return f["type"] == "session.start" }); n != 2 {
		t.Fatalf("%d session starts", n)
	}
	if got := logs.String(); !strings.Contains(got, "refused the session with voice v_gone (invalid_value); again with the default voice") ||
		!strings.Contains(got, "voice default") {
		t.Fatalf("log: %s", got)
	}
}

// The phone hangs up (Stop Speaking, a new answer): the session closes at once.
func TestSpeakClosesTheSessionWhenThePhoneHangsUp(t *testing.T) {
	speakTunables(t, 5*time.Second, time.Minute)
	closed := make(chan struct{})
	srvUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer close(closed)
		u := &fakeUpstream{t: t}
		for {
			_, b, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var f map[string]any
			_ = json.Unmarshal(b, &f)
			switch f["type"] {
			case "session.start":
				started(u, c)
			case "session.commentary.append":
				u.send(c, audioDelta([]byte{1, 1})) // and more is still to come
			}
		}
	}))
	defer srvUp.Close()
	_, srv, logs := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: "ws" + strings.TrimPrefix(srvUp.URL, "http"), Models: []string{"m1"}}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/speak", strings.NewReader(`{"text":"hi"}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if b := make([]byte, 2); res.StatusCode != http.StatusOK {
		t.Fatalf("answer %d", res.StatusCode)
	} else if _, err := io.ReadFull(res.Body, b); err != nil || !bytes.Equal(b, []byte{1, 1}) {
		t.Fatalf("first audio %v %v", b, err)
	}
	cancel()
	_ = res.Body.Close()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the session stayed open")
	}
	waitFor(t, "the log to say the app hung up", func() bool { return strings.Contains(logs.String(), "the app hung up") })
}

// A session that says nothing in time: 504 no_audio, and the session is closed.
func TestSpeakWithoutAudioInTime(t *testing.T) {
	speakTunables(t, 200*time.Millisecond, time.Minute)
	up := newFakeUpstream(t, func(u *fakeUpstream, c *websocket.Conn, f map[string]any) {
		switch f["type"] {
		case "session.start":
			started(u, c)
		case "session.commentary.append":
			// A reply with no audio (it only wrote text): not the end of what it says.
			u.send(c, replyEvent("response.completed"))
		}
	})
	_, srv, _ := speakPlugin(t, Config{Providers: []Provider{{ID: "rt", URL: up.url(), Key: "k"}}})
	began := time.Now()
	res, body := speak(t, srv, roleFull, map[string]any{"text": "hi"})
	if res.StatusCode != http.StatusGatewayTimeout || errorCode(t, body) != codeNoAudio || time.Since(began) > 3*time.Second {
		t.Fatalf("%d %s after %v", res.StatusCode, body, time.Since(began))
	}
}

// The fake dialect reads it as a little silence at its rate; a model of an upstream no longer set up falls back to
// the default one, without the voice it named.
func TestSpeakFakeDialectAndAGoneModel(t *testing.T) {
	_, srv, logs := speakPlugin(t, Config{Providers: []Provider{{ID: "dev", Dialect: DialectFake}}})
	res, body := speak(t, srv, roleFull, map[string]any{"text": "hi", "model": "gone/m1", "voice": "v1"})
	if res.StatusCode != http.StatusOK || res.Header.Get(headerAudioRate) != "16000" || len(body) != 2*fakeSpoken {
		t.Fatalf("answer %d %v %d bytes", res.StatusCode, res.Header, len(body))
	}
	if got := logs.String(); !strings.Contains(got, `model "gone/m1" is gone`) || !strings.Contains(got, "dev/fake, voice default") {
		t.Fatalf("log: %s", got)
	}
}

// The read-aloud instructions, byte for byte (UPDATE_GOLDEN=1 go test ./internal/voice -run Golden).
func TestGoldenReadAloudPrompt(t *testing.T) {
	owners := map[i18n.Lang]string{i18n.ZhHans: "老板", i18n.En: "Alex", i18n.Ja: "アレックス"}
	for _, l := range i18n.Langs {
		lang := string(l)
		golden(t, "read_aloud.owner."+lang, prompts.RenderVoiceReadAloud(prompts.VoiceReadAloud{Lang: lang, Owner: owners[l], TextLang: "zh-CN"}))
		golden(t, "read_aloud.plain."+lang, prompts.RenderVoiceReadAloud(prompts.VoiceReadAloud{Lang: lang}))
	}
}
