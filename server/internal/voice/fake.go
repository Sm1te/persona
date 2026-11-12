package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// The fake dialect (DialectFake, hub.json only; docs/assistant/voice.md "Files"): no realtime service at all. A conversation
// runs against an in-process stand-in: a line the app types is handed over as a delegation, word for word, and what the
// model would be told to speak about (a result, a refusal) comes back as its line in the captions, as it was rendered.
// So a voice conversation can be run against a real hub and real agents without a GPT-Live service.

// upLink is the upstream session's socket: a WebSocket, or the fake dialect's stand-in.
type upLink interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, p []byte) error
	Close(code websocket.StatusCode, reason string) error
	CloseNow() error
}

type fakeWire struct{}

func (fakeWire) url(string, string) (string, error) { return "", nil }
func (fakeWire) header() http.Header                { return http.Header{} }
func (fakeWire) audio([]byte) any                   { return map[string]any{"type": "fake.audio"} }
func (fakeWire) text(s string) any                  { return map[string]any{"type": "fake.text", "text": s} }
func (fakeWire) say(s, _ string) []any              { return []any{map[string]any{"type": "fake.say", "text": s}} }
func (fakeWire) acks() bool                         { return false } // it takes everything in at once
func (fakeWire) close() any                         { return map[string]any{"type": "fake.close"} }
func (fakeWire) outputRate(in int) int              { return in }

// start says whether the session only reads texts aloud (speak.go): the stand-in then says them as audio too.
func (fakeWire) start(s session) any {
	return map[string]any{"type": "fake.start", "read_aloud": s.ReadAloud}
}

func (fakeWire) reply(delegation, s string, final bool, _ string) []any {
	return []any{map[string]any{"type": "fake.reply", "delegation_id": delegation, "text": s, "final": final}}
}

func (fakeWire) decode(raw []byte) []event {
	var f struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Text  string `json:"text"`
		Audio []byte `json:"audio"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	switch f.Type {
	case "fake.started":
		return []event{{kind: evStarted}}
	case "fake.voice":
		if len(f.Audio) == 0 {
			return nil
		}
		return []event{{kind: evAudio, audio: f.Audio}}
	case "fake.delegation":
		return []event{{kind: evDelegation, id: f.ID, text: f.Text}}
	case "fake.line":
		return []event{{kind: evAssistant, text: f.Text, replace: true, final: true}, {kind: evTurnEnd}}
	}
	return nil
}

// fakeLink is the fake dialect's upstream: it answers fakeWire's frames as described above.
type fakeLink struct {
	out    chan []byte
	closed chan struct{}
	once   sync.Once

	mu        sync.Mutex
	n         int  // delegations made
	readAloud bool // the session only reads texts aloud (speak.go): what it is told to say comes as audio too
}

// fakeSpoken is the audio the fake dialect says a text with in a session that reads texts aloud: a tenth of a second
// of silence at its rate (the input's, 16 kHz unless set), in two pieces.
const fakeSpoken = 1600

func newFakeLink() *fakeLink {
	return &fakeLink{out: make(chan []byte, 256), closed: make(chan struct{})}
}

func (f *fakeLink) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case b := <-f.out:
		return websocket.MessageText, b, nil
	case <-f.closed:
		return 0, nil, net.ErrClosed
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (f *fakeLink) Write(ctx context.Context, _ websocket.MessageType, p []byte) error {
	var m struct {
		Type      string `json:"type"`
		Text      string `json:"text"`
		Final     bool   `json:"final"`
		ReadAloud bool   `json:"read_aloud"`
	}
	if err := json.Unmarshal(p, &m); err != nil {
		return err
	}
	switch m.Type {
	case "fake.start":
		f.mu.Lock()
		f.readAloud = m.ReadAloud
		f.mu.Unlock()
		return f.emit(ctx, map[string]any{"type": "fake.started"})
	case "fake.text":
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("fake%d", f.n)
		f.mu.Unlock()
		return f.emit(ctx, map[string]any{"type": "fake.delegation", "id": id, "text": m.Text})
	case "fake.say":
		f.mu.Lock()
		readAloud := f.readAloud
		f.mu.Unlock()
		if readAloud {
			for range 2 {
				if err := f.emit(ctx, map[string]any{"type": "fake.voice", "audio": make([]byte, fakeSpoken)}); err != nil {
					return err
				}
			}
		}
		return f.emit(ctx, map[string]any{"type": "fake.line", "text": m.Text})
	case "fake.reply":
		if m.Final { // what the model would speak about; a quiet one stays quiet
			return f.emit(ctx, map[string]any{"type": "fake.line", "text": m.Text})
		}
	case "fake.close":
		return f.CloseNow()
	}
	return nil
}

func (f *fakeLink) emit(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case f.out <- b:
		return nil
	case <-f.closed:
		return net.ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeLink) Close(websocket.StatusCode, string) error { return f.CloseNow() }

func (f *fakeLink) CloseNow() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}
