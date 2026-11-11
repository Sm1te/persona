package voice

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// A wire is one dialect of the GPT-Live protocol: the frames to send, and what the service's frames mean.
type wire interface {
	url(base, model string) (string, error)
	header() http.Header
	start(s session) any
	audio(pcm []byte) any
	// text is a line the user typed; say is something the model should speak about now (a delegated task came back
	// after its delegation was gone), in as many frames as the dialect takes it.
	text(s string) any
	say(s, id string) []any
	// reply answers a delegation: final is the result the model speaks about (its only commentary), otherwise quiet
	// context.
	reply(delegation, s string, final bool, id string) []any
	// acks: the service says when it took in a commentary sent with event id id (say, a final reply, a tool result)
	// and names that id when it refuses one (evAck, evError). Without, an id is not sent.
	acks() bool
	close() any
	decode(raw []byte) []event
	// outputRate is the rate of the service's audio when session.started does not say.
	outputRate(input int) int
}

// session is what a conversation starts the service with.
type session struct {
	Model, Voice, Instructions string
	InputRate                  int
	Tools                      []toolDecl
	// ReadAloud: the session only reads texts aloud (speak.go). The services are told so by Instructions alone; the
	// fake dialect, which has no voice, answers such a session with a little silence.
	ReadAloud bool
}

type toolDecl struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type evKind int

const (
	evStarted     evKind = iota // the session is up; rate: its output audio rate (0: the wire's default)
	evAudio                     // audio: PCM16 to play
	evUser                      // text of what the user said (replace: the whole line so far; final: it is done)
	evAssistant                 // text of what the model says
	evTurnEnd                   // the model's turn is over (id: the reply's, when the service names its replies)
	evInterrupted               // the model's answer was cut off: drop what has not been played (id: as evTurnEnd)
	evDelegation                // the model hands work over: id, text (what to do, may be empty)
	evToolCall                  // a function call: id (its delegation), callID, name, args
	evAck                       // the service took in what was sent as eventID (it may hold it while the model speaks)
	evError                     // text, code; param (the field it refused), eventID (what it refused), when it says
	evReply                     // the model began a reply (id): the service says when its replies start, end and are cut off
)

type event struct {
	kind             evKind
	text             string
	replace, final   bool
	audio            []byte
	id, callID, name string
	args, code       string
	param, eventID   string
	rate             int
}

func newWire(dialect string) wire {
	switch dialect {
	case DialectCodex:
		return codexWire{}
	case DialectRealtime:
		return realtimeWire{}
	case DialectFake:
		return fakeWire{}
	}
	return liveWire{}
}

// liveWire is the public GPT-Live API (also served, with function tools as an extension, by deployments that speak
// it for their own models).
type liveWire struct{}

func (liveWire) url(base, _ string) (string, error) { return base, nil }
func (liveWire) header() http.Header                { return http.Header{} }

func (liveWire) start(s session) any {
	audio := map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": s.InputRate}}
	if s.Voice != "" {
		audio["output"] = map[string]any{"voice": s.Voice}
	}
	sess := map[string]any{"model": s.Model, "audio": audio, "delegation": map[string]any{"type": "client"}}
	if s.Instructions != "" {
		sess["instructions"] = s.Instructions
	}
	if len(s.Tools) > 0 {
		sess["tools"] = s.Tools
	}
	return map[string]any{"type": "session.start", "session": sess}
}

func (liveWire) audio(pcm []byte) any {
	return map[string]any{"type": "session.input_audio.append", "audio": base64.StdEncoding.EncodeToString(pcm)}
}

// A commentary without a delegation is a turn of the user's (typed input).
func (liveWire) text(s string) any {
	return map[string]any{"type": "session.commentary.append", "delegation_id": nil, "content": clipRunes(s, commentaryRunes)}
}
func (w liveWire) say(s, id string) []any { return []any{withEventID(w.text(s), id)} }

// A thinking is held to the append's token limit; a commentary only to its characters (docs/lessons/voice.md "Errors":
// a delegation's commentary of 1300 Chinese characters went through on the measured service, its limit is 4000
// characters), so a whole answer fits in one. Another service may hold a commentary to the append's tokens: it refuses
// it (evError, param content) and the conversation sends a shorter one (session_speak.go, deliver).
func (liveWire) reply(delegation, s string, final bool, id string) []any {
	if final {
		return []any{withEventID(map[string]any{"type": "session.commentary.append", "delegation_id": delegation,
			"content": clipRunes(s, commentaryRunes)}, id)}
	}
	return []any{map[string]any{"type": "session.thinking.append", "delegation_id": delegation, "content": clipTokens(s, appendTokens)}}
}

// The service echoes a client frame's event_id as client_event_id on its session.commentary.appended and on an error
// about it (docs/lessons/voice.md "Errors").
func (liveWire) acks() bool { return true }

func withEventID(frame any, id string) any {
	if m, ok := frame.(map[string]any); ok && id != "" {
		m["event_id"] = id
	}
	return frame
}

func (liveWire) close() any            { return map[string]any{"type": "session.close"} }
func (liveWire) outputRate(in int) int { return in }

func (liveWire) decode(raw []byte) []event {
	var f struct {
		Type    string `json:"type"`
		Delta   string `json:"delta"`
		Session struct {
			Audio struct {
				Format struct {
					Rate int `json:"rate"`
				} `json:"format"`
				Output struct {
					Format struct {
						Rate int `json:"rate"`
					} `json:"format"`
				} `json:"output"`
			} `json:"audio"`
		} `json:"session"`
		Delegation struct {
			ID      string        `json:"id"`
			Target  string        `json:"target"`
			Content []contentPart `json:"content"`
		} `json:"delegation"`
		DelegationID  string          `json:"delegation_id"`
		Event         json.RawMessage `json:"event"`
		ClientEventID string          `json:"client_event_id"`
		Error         struct {
			Message       string `json:"message"`
			Code          string `json:"code"`
			Param         string `json:"param"`
			ClientEventID string `json:"client_event_id"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	switch f.Type {
	case "session.started":
		rate := f.Session.Audio.Output.Format.Rate
		if rate == 0 {
			rate = f.Session.Audio.Format.Rate
		}
		return []event{{kind: evStarted, rate: rate}}
	case "session.output_audio.delta":
		pcm, err := base64.StdEncoding.DecodeString(f.Delta)
		if err != nil || len(pcm) == 0 {
			return nil
		}
		return []event{{kind: evAudio, audio: pcm}}
	case "session.input_transcript.delta":
		return []event{{kind: evUser, text: f.Delta}}
	case "session.output_transcript.delta":
		return []event{{kind: evAssistant, text: f.Delta, name: "transcript"}}
	case "session.delegation.created":
		// Only a client delegation is work for us; "responses" ones carry the service's own response lifecycle.
		if f.Delegation.Target != "" && f.Delegation.Target != "client" {
			return nil
		}
		return []event{{kind: evDelegation, id: f.Delegation.ID, text: inputText(f.Delegation.Content)}}
	case "response.event":
		return liveResponseEvent(f.DelegationID, f.Event)
	case "session.commentary.appended":
		if f.ClientEventID == "" {
			return nil
		}
		return []event{{kind: evAck, eventID: f.ClientEventID}}
	case "error":
		return []event{{kind: evError, text: f.Error.Message, code: f.Error.Code, param: f.Error.Param,
			eventID: firstNonEmpty(f.Error.ClientEventID, f.ClientEventID)}}
	}
	return nil
}

// liveResponseEvent reads the Responses event a response.event carries: a function call (bare, or as the finished
// item of response.output_item.done), the model's text, the start and end of its reply. A reply's events come on its
// own (responses) delegation: delegation names the reply (docs/lessons/voice.md "Other behavior").
func liveResponseEvent(delegation string, raw json.RawMessage) []event {
	var e struct {
		Type   string `json:"type"`
		Delta  string `json:"delta"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
		Args   string `json:"arguments"`
		Item   struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Args   string `json:"arguments"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	switch e.Type {
	case "function_call":
		return []event{{kind: evToolCall, id: delegation, callID: e.CallID, name: e.Name, args: e.Args}}
	case "response.output_item.done":
		if e.Item.Type == "function_call" {
			return []event{{kind: evToolCall, id: delegation, callID: e.Item.CallID, name: e.Item.Name, args: e.Item.Args}}
		}
	case "response.output_text.delta":
		return []event{{kind: evAssistant, text: e.Delta, name: "text"}}
	case "response.created":
		return []event{{kind: evReply, id: delegation}}
	case "response.completed", "response.failed":
		return []event{{kind: evTurnEnd, id: delegation}}
	case "response.incomplete", "response.cancelled":
		return []event{{kind: evInterrupted, id: delegation}}
	}
	return nil
}

// codexWire is what Codex speaks to gpt-live-1-codex (codex-rs codex-api realtime_websocket, "frameless bidi").
type codexWire struct{}

func (codexWire) url(base, model string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if model != "" {
		q := u.Query()
		q.Set("model", model)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (codexWire) header() http.Header { return http.Header{"Openai-Alpha": {"quicksilver=v2"}} }

func (codexWire) start(s session) any {
	sess := map[string]any{"instructions": s.Instructions, "delegation": map[string]any{"type": "client"}}
	if s.Voice != "" {
		sess["audio"] = map[string]any{"output": map[string]any{"voice": s.Voice}}
	}
	return map[string]any{"type": "session.update", "session": sess}
}

func (codexWire) audio(pcm []byte) any {
	return map[string]any{"type": "input_audio.append", "audio": base64.StdEncoding.EncodeToString(pcm)}
}

// Context for the whole session; speakable is what the model talks about (an omitted channel means the same).
func (codexWire) text(s string) any {
	return map[string]any{"type": "session.context.append", "channel": "speakable", "content": inputContent(clipBytes(s, codexAppendBytes))}
}

// say goes in pieces of at most 500 bytes, as Codex splits its own appends; a whole report (its closing tag too).
func (codexWire) say(s, _ string) []any {
	var out []any
	for _, chunk := range chunkBytes(clipRunes(s, commentaryRunes), codexAppendBytes) {
		out = append(out, map[string]any{"type": "session.context.append", "channel": "speakable", "content": inputContent(chunk)})
	}
	return out
}

func (codexWire) reply(delegation, s string, final bool, _ string) []any {
	channel := "commentary"
	if final {
		channel = "speakable"
	}
	var out []any
	for _, chunk := range chunkBytes(clipRunes(s, commentaryRunes), codexAppendBytes) {
		out = append(out, map[string]any{
			"type": "delegation.context.append", "delegation_item_id": delegation, "channel": channel, "content": inputContent(chunk),
		})
	}
	return out
}

// Codex's realtime protocol has no ack of an append that we know of: what is sent counts as taken in at once.
func (codexWire) acks() bool         { return false }
func (codexWire) close() any         { return map[string]any{"type": "session.close"} }
func (codexWire) outputRate(int) int { return 24000 }

func (codexWire) decode(raw []byte) []event {
	var f struct {
		Type  string `json:"type"`
		Audio string `json:"audio"`
		Item  struct {
			ID      string        `json:"id"`
			Type    string        `json:"type"`
			Target  string        `json:"target"`
			Text    string        `json:"text"`
			Content []contentPart `json:"content"`
		} `json:"item"`
		Turn struct {
			Role       string `json:"role"`
			Transcript string `json:"transcript"`
		} `json:"turn"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	switch f.Type {
	case "session.started":
		return []event{{kind: evStarted}}
	case "output_audio.delta":
		pcm, err := base64.StdEncoding.DecodeString(f.Audio)
		if err != nil || len(pcm) == 0 {
			return nil
		}
		return []event{{kind: evAudio, audio: pcm}}
	case "input_transcript.added":
		return []event{{kind: evUser, text: f.Item.Text}}
	case "output_transcript.added":
		return []event{{kind: evAssistant, text: f.Item.Text}}
	case "turn.done":
		switch f.Turn.Role {
		case "user":
			return []event{{kind: evUser, text: f.Turn.Transcript, replace: true, final: true}}
		case "assistant":
			return []event{{kind: evAssistant, text: f.Turn.Transcript, replace: true, final: true}, {kind: evTurnEnd}}
		}
	case "delegation.created":
		if f.Item.Type != "delegation" || f.Item.Target != "client" || f.Item.ID == "" {
			return nil
		}
		return []event{{kind: evDelegation, id: f.Item.ID, text: inputText(f.Item.Content)}}
	case "error":
		msg := f.Message
		if msg == "" {
			var e struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			}
			if json.Unmarshal(f.Error, &e) == nil && e.Message != "" {
				return []event{{kind: evError, text: e.Message, code: e.Code}}
			}
			msg = strings.TrimSpace(string(f.Error))
		}
		return []event{{kind: evError, text: msg}}
	}
	return nil
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func inputText(parts []contentPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "input_text" {
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func inputContent(s string) []contentPart { return []contentPart{{Type: "input_text", Text: s}} }

const (
	appendTokens     = 450  // GPT-Live: a thinking's content is limited to 500 tokens
	commentaryRunes  = 3900 // GPT-Live: a commentary's to 4000 characters (docs/lessons/voice.md "Errors")
	codexAppendBytes = 500  // Codex splits its context appends at 500 bytes
)

// clipTokens keeps the start of s within about n tokens (a CJK character is about one, other text about four
// characters to one).
func clipTokens(s string, n int) string {
	budget := float64(n)
	for i, r := range s {
		budget -= runeTokens(r)
		if budget < 0 {
			return strings.TrimSpace(s[:i]) + "…"
		}
	}
	return s
}

// tokens is about how many tokens s is (as clipTokens counts).
func tokens(s string) float64 {
	n := 0.0
	for _, r := range s {
		n += runeTokens(r)
	}
	return n
}

func runeTokens(r rune) float64 {
	if r >= 0x2E80 {
		return 1
	}
	return 0.25
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return chunkBytes(s, n)[0]
}

// chunkBytes splits s into pieces of at most n bytes, at character boundaries.
func chunkBytes(s string, n int) []string {
	var out []string
	for len(s) > n {
		end := n
		for end > 0 && !utf8RuneStart(s[end]) {
			end--
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return append(out, s)
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// realtimeWire is the OpenAI Realtime v2 WebSocket wire (/v1/realtime, gpt-realtime / gpt-realtime-mini).
type realtimeWire struct{}

func (realtimeWire) url(base, model string) (string, error) { return (codexWire{}).url(base, model) }
func (realtimeWire) header() http.Header                    { return http.Header{} }

// start is the GA session.update: type is required, the audio formats go under audio.input and audio.output, and the
// voice only in audio.output (session.audio.format and session.voice are refused as unknown parameters).
func (realtimeWire) start(s session) any {
	rate := s.InputRate
	if rate <= 0 {
		rate = 24000
	}
	format := map[string]any{"type": "audio/pcm", "rate": rate}
	output := map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}}
	if s.Voice != "" {
		output["voice"] = s.Voice
	}
	audio := map[string]any{
		"input":  map[string]any{"format": format, "transcription": map[string]any{"model": "gpt-4o-mini-transcribe"}},
		"output": output,
	}
	sess := map[string]any{"type": "realtime", "audio": audio}
	if s.Instructions != "" {
		sess["instructions"] = s.Instructions
	}
	if len(s.Tools) > 0 {
		sess["tools"] = s.Tools
	}
	return map[string]any{"type": "session.update", "session": sess}
}

func (realtimeWire) audio(pcm []byte) any {
	return map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(pcm)}
}

func (realtimeWire) text(s string) any {
	return map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "message",
			"role":    "user",
			"content": inputContent(clipRunes(s, commentaryRunes)),
		},
	}
}

func (w realtimeWire) say(s, _ string) []any {
	return []any{w.text(s), map[string]any{"type": "response.create"}}
}

func (w realtimeWire) reply(delegation, s string, final bool, _ string) []any {
	if delegation != "" {
		item := map[string]any{
			"type": "conversation.item.create",
			"item": map[string]any{
				"type":    "function_call_output",
				"call_id": delegation,
				"output":  clipRunes(s, commentaryRunes),
			},
		}
		if final {
			return []any{item, map[string]any{"type": "response.create"}}
		}
		return []any{item}
	}
	if final {
		return w.say(s, "")
	}
	return []any{w.text(s)}
}

func (realtimeWire) acks() bool         { return false }
func (realtimeWire) close() any         { return map[string]any{"type": "session.close"} }
func (realtimeWire) outputRate(int) int { return 24000 }

func (realtimeWire) decode(raw []byte) []event {
	var f struct {
		Type       string `json:"type"`
		Delta      string `json:"delta"`
		Transcript string `json:"transcript"`
		ResponseID string `json:"response_id"`
		CallID     string `json:"call_id"`
		Name       string `json:"name"`
		Arguments  string `json:"arguments"`
		Response   struct {
			ID string `json:"id"`
		} `json:"response"`
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	switch f.Type {
	case "session.created", "session.updated", "session.started":
		return []event{{kind: evStarted, rate: 24000}}
	case "response.audio.delta", "response.output_audio.delta":
		pcm, err := base64.StdEncoding.DecodeString(f.Delta)
		if err != nil || len(pcm) == 0 {
			return nil
		}
		return []event{{kind: evAudio, audio: pcm}}
	case "conversation.item.input_audio_transcription.delta":
		return []event{{kind: evUser, text: f.Delta}}
	case "conversation.item.input_audio_transcription.completed":
		return []event{{kind: evUser, text: f.Transcript, replace: true, final: true}}
	case "response.audio_transcript.delta", "response.output_audio_transcript.delta":
		return []event{{kind: evAssistant, text: f.Delta, name: "transcript"}}
	// A response's id is in response.id (response_id is for the events within it).
	case "response.created":
		return []event{{kind: evReply, id: firstNonEmpty(f.Response.ID, f.ResponseID)}}
	case "response.done", "response.completed":
		return []event{{kind: evTurnEnd, id: firstNonEmpty(f.Response.ID, f.ResponseID)}}
	// The service's VAD heard the user: it cancels the response under way (interrupt_response), and the app drops
	// what it has not played of it.
	case "input_audio_buffer.speech_started":
		return []event{{kind: evInterrupted}}
	// A function call is taken from its arguments' done only: response.output_item.done repeats it, and a second
	// evToolCall would hand the same call over twice.
	case "response.function_call_arguments.done":
		if f.CallID == "" || f.Name == "" {
			return nil
		}
		return []event{{kind: evDelegation, id: f.CallID}, {kind: evToolCall, id: f.CallID, callID: f.CallID, name: f.Name, args: f.Arguments}}
	case "error":
		return []event{{kind: evError, text: f.Error.Message, code: f.Error.Code, param: f.Error.Param}}
	}
	return nil
}
