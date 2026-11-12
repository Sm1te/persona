// Package ai calls Messages, Responses or Chat Completions APIs for small structured
// tasks in the app (e.g. turning a pasted blob into a mail account). The key is read from its key file at call time
// and never stored by Persona.
package ai

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/aiwire"
	"github.com/nwverse/persona/server/internal/prompts"
)

// Defined in the contract package aiwire (docs/server/architecture.md "Contract packages"); the old names stay.
type Config = aiwire.Config

// errNotConfigured is shown in the app as is (e.g. under "AI Draft Reply"), so it says where to fix it there.
var errNotConfigured = errors.New("还没有能直接调用的模型：在 设置 › 模型供应商 添加带 Key 的接入，再到 设置 › 模型配置 为这项选择它" +
	`（或在助手的 manager.json 写 "ai"）`)

// IsNotConfigured tells a request that had no model to go to (nothing set up for it) from one that failed.
func IsNotConfigured(err error) bool { return errors.Is(err, errNotConfigured) }

type Client struct {
	cfg     Config
	http    *http.Client
	resolve func(context.Context) (*Client, error)
}

// NewResolved resolves the execution configuration at the start of every request.
func NewResolved(resolve func(context.Context) (*Client, error)) *Client {
	return &Client{resolve: resolve}
}

// Config returns the immutable connection configuration, never an API response.
func (c *Client) Config() Config { return c.cfg }

func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) key() (string, error) {
	if c.cfg.APIKey != "" {
		return c.cfg.APIKey, nil
	}
	path := c.cfg.APIKeyFile
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ai: read api key: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Complete sends one user message with a system prompt and returns the text reply. A reply the upstream cut off at
// maxTokens comes back as far as it got.
func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (string, error) {
	text, _, err := c.complete(ctx, system, user, maxTokens)
	return text, err
}

// complete is Complete that also says whether the reply stopped at maxTokens (the Messages API's stop_reason).
func (c *Client) complete(ctx context.Context, system, user string, maxTokens int) (text string, truncated bool, err error) {
	r, err := c.Chat(ctx, Request{System: system, Messages: []Message{{Role: RoleUser, Content: user}}, MaxTokens: maxTokens})
	return r.Text, r.Truncated, err
}

// The roles of a conversation's turns.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn of a conversation: the user's, or the model's own earlier answer.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is a conversation to continue: a system prompt and turns that start and end with the user's.
type Request struct {
	System    string
	Messages  []Message
	MaxTokens int // output limit for Messages and Chat Completions
	// BoundOutput also enforces MaxTokens on Responses. Cost-sensitive background calls
	// fail closed when a relay refuses the bound; legacy callers keep their existing behavior.
	BoundOutput bool
}

// Usage is what one request spent, as the upstream counted it (zero where it did not say).
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// Add is u and v together (a call made of several requests).
func (u Usage) Add(v Usage) Usage {
	return Usage{InputTokens: u.InputTokens + v.InputTokens, OutputTokens: u.OutputTokens + v.OutputTokens,
		CacheReadInputTokens: u.CacheReadInputTokens + v.CacheReadInputTokens, CacheCreationInputTokens: u.CacheCreationInputTokens + v.CacheCreationInputTokens}
}

// Reply is the model's answer: its text, whether it stopped at MaxTokens, the model the upstream says answered
// (the one asked for when it does not say) and what it spent.
type Reply struct {
	Text      string
	Truncated bool
	Model     string
	Usage     Usage
}

// Chat sends a conversation and returns the reply. A context with a deadline bounds the request by it instead of the
// client's 60 seconds (a long answer).
func (c *Client) Chat(ctx context.Context, r Request) (Reply, error) {
	if c.resolve != nil {
		resolved, err := c.resolve(ctx)
		if err != nil {
			return Reply{}, err
		}
		return resolved.Chat(ctx, r)
	}
	if !c.cfg.Configured() {
		return Reply{}, errNotConfigured
	}
	key, err := c.key()
	if err != nil {
		return Reply{}, err
	}
	// Every client of one address and key goes through the same gate, which holds requests back while the upstream
	// is out of quota or busy (limit.go).
	g := gateFor(c.cfg.BaseURL, key)
	probe, err := g.admit(ctx)
	if err != nil {
		return Reply{}, err
	}
	hc := c.http
	if _, ok := ctx.Deadline(); ok && hc != nil && hc.Timeout != 0 {
		copied := *hc
		copied.Timeout = 0
		hc = &copied
	}
	var reply Reply
	var answered bool
	switch c.cfg.API {
	case "responses":
		reply, answered, err = c.respond(ctx, hc, key, r)
	case "chat":
		reply, answered, err = c.chatCompletions(ctx, hc, key, r)
	default:
		reply, answered, err = c.messages(ctx, hc, key, r)
	}
	if reply.Model == "" {
		reply.Model = c.cfg.Model
	}
	return reply, g.done(probe, answered, err)
}

// apiEndpoint preserves deployment prefixes and accepts bases with or without /v1.
func apiEndpoint(base, path string) string {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/" + path
}

// messages calls the Messages API; answered is whether the upstream said 200, Truncated whether the reply stopped at
// MaxTokens. A model that thinks spends its thinking from the same MaxTokens, so a long answer can stop half written.
// It streams (#4245): a gateway's cap on a non-streamed request's total time (often 120 s) otherwise fails every
// answer a slow model needs longer for. A server that ignores "stream" and answers one JSON message is read as before.
func (c *Client) messages(ctx context.Context, hc *http.Client, key string, r Request) (reply Reply, answered bool, err error) {
	turns := make([]any, 0, len(r.Messages))
	for _, m := range r.Messages {
		turns = append(turns, map[string]any{"role": m.Role, "content": m.Content})
	}
	bodyMap := map[string]any{"model": c.cfg.Model, "max_tokens": r.MaxTokens, "system": r.System, "messages": turns, "stream": true}
	if c.cfg.ReasoningEffort != "" {
		switch strings.ToLower(strings.TrimSpace(c.cfg.ReasoningEffort)) {
		case "off", "none", "disabled":
			switch {
			case strings.Contains(strings.ToLower(c.cfg.Model), "sonnet-5-5"):
				// Sonnet 5.5 disables up-front thinking with between_tools; low also limits any between-tool work.
				bodyMap["thinking"] = map[string]any{"type": "between_tools"}
				bodyMap["output_config"] = map[string]any{"effort": "low"}
			case messagesThinkingDisabledUnsupported(c.cfg.Model):
				// These models reject thinking.disabled; the issue's off-or-lowest contract uses low effort instead.
				bodyMap["output_config"] = map[string]any{"effort": "low"}
			default:
				bodyMap["thinking"] = map[string]any{"type": "disabled"}
			}
		case "low", "medium", "high", "xhigh", "max":
			bodyMap["output_config"] = map[string]any{"effort": c.cfg.ReasoningEffort}
		}
	}
	body, _ := json.Marshal(bodyMap)
	req, err := http.NewRequestWithContext(ctx, "POST", apiEndpoint(c.cfg.BaseURL, "messages"), bytes.NewReader(body))
	if err != nil {
		return reply, false, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream")
	if c.cfg.Bearer {
		req.Header.Set("authorization", "Bearer "+key)
	} else {
		req.Header.Set("x-api-key", key)
	}
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := hc.Do(req)
	if err != nil {
		return reply, false, fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return reply, false, newHTTPError(resp, raw)
	}
	in := bufio.NewReader(resp.Body)
	if wholeMessage(in) {
		return readMessage(in)
	}
	return readMessageStream(in)
}

// wholeMessage says the answer is one JSON message rather than an event stream: its first byte past any white space
// opens an object (an event stream starts with a field or a comment). A body that can't be read is left to the stream
// reader, which reports it.
func wholeMessage(in *bufio.Reader) bool {
	for {
		b, err := in.Peek(1)
		if err != nil {
			return false
		}
		switch b[0] {
		case ' ', '\t', '\r', '\n':
			_, _ = in.ReadByte()
		default:
			return b[0] == '{'
		}
	}
}

// messageUsage is a Messages API usage object, whose fields come in parts across a stream.
type messageUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

// into sets the fields u has on to: message_delta's usage carries the totals so far, of only some fields. A zero
// is not taken: some relays send input_tokens 0 in message_delta, which would wipe message_start's real count.
func (u *messageUsage) into(to *Usage) {
	if u == nil {
		return
	}
	for _, f := range []struct {
		from *int
		to   *int
	}{{u.InputTokens, &to.InputTokens}, {u.OutputTokens, &to.OutputTokens},
		{u.CacheReadInputTokens, &to.CacheReadInputTokens}, {u.CacheCreationInputTokens, &to.CacheCreationInputTokens}} {
		if f.from != nil && *f.from != 0 {
			*f.to = *f.from
		}
	}
}

// errorOf reads a Messages error: an object with its type and message (type falling back to a code), or only a
// message string. ok is false when raw holds no error at all.
func errorOf(raw json.RawMessage) (kind, msg string, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", false
	}
	if json.Unmarshal(raw, &msg) == nil {
		return "error", msg, true
	}
	var o struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return "", "", false
	}
	return cmp.Or(o.Type, o.Code, "error"), o.Message, true
}

// messageFailure is the error a Messages error reported after the 200 stands for, and whether it counts as an
// answer: a limit (overloaded_error, rate_limit_error) holds the gate like HTTP 529 / 429 and is no answer; any
// other is a streamFailure, Transient as its type's HTTP status would be (api_error 500, timeout_error 504).
func messageFailure(kind, msg string) (bool, error) {
	if limit := streamLimitFor("error", kind, msg); limit != nil {
		return false, limit
	}
	return true, &streamFailure{status: messageErrorStatus[strings.ToLower(kind)], kind: kind, msg: msg}
}

// streamCut is the error of a stream that ended before its end event: the connection was cut, and whatever text
// came is only part of the answer (io.ErrUnexpectedEOF, so Transient), not a short reply. what names the end missed.
func streamCut(gotText bool, what string) error {
	if !gotText {
		return fmt.Errorf("ai: stream ended without a reply: %w", io.ErrUnexpectedEOF)
	}
	return fmt.Errorf("ai: stream ended before the %s completed: %w", what, io.ErrUnexpectedEOF)
}

// readMessage reads a whole Messages API answer (a server that didn't stream).
func readMessage(body io.Reader) (Reply, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 4<<20))
	if err != nil {
		return Reply{}, true, fmt.Errorf("ai: read response: %w", err)
	}
	var out struct {
		Type    string `json:"type"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string          `json:"stop_reason"`
		Usage      Usage           `json:"usage"`
		Error      json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Reply{}, true, fmt.Errorf("ai: decode: %w", err)
	}
	// An error body sent with 200 (a gateway that ignored "stream" and failed) is the failure it reports.
	if kind, msg, ok := errorOf(out.Error); out.Type == "error" || (out.Type == "" && ok && len(out.Content) == 0) {
		answered, err := messageFailure(cmp.Or(kind, "error"), msg)
		return Reply{}, answered, err
	}
	var b strings.Builder
	for _, part := range out.Content {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	return messageReply(b.String(), out.StopReason, out.Model, out.Usage)
}

// messageReply is the reply of a finished message, or the refusal it was.
func messageReply(text, stopReason, model string, usage Usage) (Reply, bool, error) {
	if stopReason == "refusal" { // declined, usually with no text at all: not a reply that lacks its JSON
		return Reply{Model: model, Usage: usage}, true, &RefusalError{StopReason: stopReason}
	}
	return Reply{Text: text, Truncated: stopReason == "max_tokens", Model: model, Usage: usage}, true, nil
}

// readMessageStream reads a Messages API event stream, framed by SSEEvents: the model and usage from message_start,
// the text of the text blocks (content_block_start, text_delta; thinking and tool input are skipped), the stop reason
// and the usage totals from message_delta. message_stop ends it. A stream that ends with neither message_stop nor a
// stop reason was cut (streamCut). An error event, read leniently (errorOf: an object or a string, with or without
// the event's own type), is the failure it reports (messageFailure). Data that isn't JSON is skipped.
func readMessageStream(body io.Reader) (Reply, bool, error) {
	var (
		b              strings.Builder
		model          string
		stop           string
		usage          Usage
		failed         error
		failedAnswered bool
		stopped        bool
	)
	err := SSEEvents(body, func(data string) bool {
		var ev struct {
			Type string `json:"type"`
			// Message is message_start's message, or an error's text where a relay puts it at the top.
			Message      json.RawMessage `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *messageUsage   `json:"usage"`
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return false
		}
		kind, msg, isError := errorOf(ev.Error)
		if ev.Type == "" && isError { // an error event without its type: the error is what it carries
			ev.Type = "error"
		}
		switch ev.Type {
		case "message_start":
			var m struct {
				Model string        `json:"model"`
				Usage *messageUsage `json:"usage"`
			}
			if json.Unmarshal(ev.Message, &m) == nil {
				model = m.Model
				m.Usage.into(&usage)
			}
		case "content_block_start":
			if ev.ContentBlock.Type == "text" {
				b.WriteString(ev.ContentBlock.Text)
			}
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" {
				b.WriteString(ev.Delta.Text)
			}
		case "message_delta":
			stop = cmp.Or(ev.Delta.StopReason, stop)
			ev.Usage.into(&usage)
		case "message_stop":
			stopped = true
			return true
		case "error":
			if msg == "" {
				_ = json.Unmarshal(ev.Message, &msg) // a relay's {"type":"error","message":"..."}
			}
			failedAnswered, failed = messageFailure(cmp.Or(kind, "error"), msg)
			return true
		}
		return false
	})
	if err != nil {
		return Reply{}, true, fmt.Errorf("ai: read stream: %w", err)
	}
	if failed != nil {
		return Reply{}, failedAnswered, failed
	}
	if !stopped && stop == "" {
		return Reply{}, true, streamCut(b.Len() > 0, "message")
	}
	return messageReply(b.String(), stop, model, usage)
}

// messagesThinkingDisabledUnsupported reports models whose Messages API rejects thinking.disabled. Match the ID
// anywhere so provider-prefixed and dated variants follow the same rule; Fable/Mythos 5.x always require thinking.
func messagesThinkingDisabledUnsupported(model string) bool {
	model = strings.ToLower(model)
	for _, id := range []string{"opus-5-5", "sonnet-5-5", "fable-5", "mythos-5", "mythos-preview"} {
		if strings.Contains(model, id) {
			return true
		}
	}
	return false
}

// respond calls the Responses API. It always streams: some compatible endpoints (Codex-backed relays) reject
// stream=false, and the text is just the concatenated output_text deltas. The system prompt goes in as a developer
// message rather than `instructions`, which those relays replace with their own. Events are framed as SSE
// (SSEEvents), and a stream that ends before its terminal event (completed, incomplete or failed) was cut: an error
// wrapping io.ErrUnexpectedEOF, not a short reply.
func (c *Client) respond(ctx context.Context, hc *http.Client, key string, r Request) (reply Reply, answered bool, err error) {
	input := []any{map[string]any{"role": "developer", "content": r.System}}
	for _, m := range r.Messages {
		input = append(input, map[string]any{"role": m.Role, "content": m.Content})
	}
	req := map[string]any{"model": c.cfg.Model, "store": false, "stream": true, "input": input}
	if r.BoundOutput && r.MaxTokens > 0 {
		req["max_output_tokens"] = r.MaxTokens
	}
	if c.cfg.ReasoningEffort != "" {
		effort := c.cfg.ReasoningEffort
		switch strings.ToLower(strings.TrimSpace(effort)) {
		case "off", "none", "disabled":
			effort = "none"
		}
		req["reasoning"] = map[string]any{"effort": effort}
	}
	body, _ := json.Marshal(req)
	hreq, err := http.NewRequestWithContext(ctx, "POST", apiEndpoint(c.cfg.BaseURL, "responses"), bytes.NewReader(body))
	if err != nil {
		return reply, false, err
	}
	hreq.Header.Set("content-type", "application/json")
	hreq.Header.Set("accept", "text/event-stream")
	hreq.Header.Set("authorization", "Bearer "+key)
	resp, err := hc.Do(hreq)
	if err != nil {
		return reply, false, fmt.Errorf("ai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return reply, false, newHTTPError(resp, raw)
	}
	var b strings.Builder
	refused := false // a refusal content part came: the model declined, whatever else the stream says
	// event acts on one event's data, and done says it ended the stream, with the reply and error to return. ended
	// returns them: a limit the stream reported is no answer, so it holds the gate like the HTTP 429 it stands for.
	ended := func(got Reply, err error) (Reply, bool, error) {
		var limit *streamLimit
		return got, !errors.As(err, &limit), err
	}
	event := func(data string) (reply Reply, done bool, err error) {
		var ev struct {
			Type    string `json:"type"`
			Delta   string `json:"delta"`
			Message string `json:"message"`
			Code    string `json:"code"` // an error event's
			// Part is the content part of response.content_part.added/done: type output_text or refusal.
			Part struct {
				Type string `json:"type"`
			} `json:"part"`
			Response struct {
				Model string `json:"model"`
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
				IncompleteDetails *struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
				Usage *struct {
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					InputTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return reply, false, nil
		}
		finished := func() Reply {
			r := Reply{Text: b.String(), Model: ev.Response.Model}
			if u := ev.Response.Usage; u != nil { // input_tokens counts the cached ones too
				r.Usage = Usage{InputTokens: u.InputTokens - u.InputTokensDetails.CachedTokens, OutputTokens: u.OutputTokens,
					CacheReadInputTokens: u.InputTokensDetails.CachedTokens}
			}
			return r
		}
		switch ev.Type {
		case "response.output_text.delta":
			b.WriteString(ev.Delta)
		case "response.refusal.delta", "response.refusal.done":
			refused = true
		case "response.content_part.added", "response.content_part.done":
			if ev.Part.Type == "refusal" {
				refused = true
			}
		case "response.completed":
			reply = finished()
			if refused { // a refusal part, whatever text came with it: the same as the Messages API's stop_reason
				return Reply{Model: reply.Model, Usage: reply.Usage}, true, &RefusalError{StopReason: "refusal"}
			}
			return reply, true, nil
		case "response.incomplete":
			reason := ""
			if d := ev.Response.IncompleteDetails; d != nil {
				reason = d.Reason
			}
			if reason == "content_filter" || refused { // stopped by a filter: declined, not a failure to retry
				if reason != "content_filter" {
					reason = "refusal"
				}
				reply = finished()
				return Reply{Model: reply.Model, Usage: reply.Usage}, true, &RefusalError{StopReason: reason}
			}
			msg := ev.Message
			if ev.Response.Error != nil {
				msg = ev.Response.Error.Message
			}
			return reply, true, fmt.Errorf("ai: %s: %s", ev.Type, msg)
		case "response.failed", "error":
			msg, code := ev.Message, ev.Code
			if e := ev.Response.Error; e != nil {
				msg, code = e.Message, cmp.Or(e.Code, code)
			}
			if limit := streamLimitFor(ev.Type, code, msg); limit != nil { // a limit told after the 200 holds the gate too
				return reply, true, limit
			}
			return reply, true, fmt.Errorf("ai: %s: %s", ev.Type, msg)
		}
		return reply, false, nil
	}
	var (
		got     Reply
		gotErr  error
		stopped bool
	)
	if err := SSEEvents(resp.Body, func(data string) bool {
		got, stopped, gotErr = event(data)
		return stopped
	}); err != nil {
		return reply, true, fmt.Errorf("ai: read stream: %w", err)
	}
	if stopped {
		return ended(got, gotErr)
	}
	return reply, true, streamCut(b.Len() > 0, "response") // no terminal event
}

// JSON asks for a JSON object and decodes the first one in the reply into out.
func (c *Client) JSON(ctx context.Context, system, user string, out any) error {
	return c.JSONMax(ctx, system, user, 1024, out)
}

// JSONMax is JSON with room for a longer answer (summaries). A reply cut off at maxTokens before its object closed is
// asked for once more with twice the room; cut off again, the error says so (ErrTruncated) instead of blaming the JSON.
func (c *Client) JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error {
	system = prompts.RenderJSONOnly(system)
	for attempt := 0; ; attempt++ {
		text, truncated, err := c.complete(ctx, system, user, maxTokens)
		if err != nil {
			return err
		}
		obj, err := FirstObject(text)
		if err == nil {
			return unmarshalReply([]byte(obj), out)
		}
		if !truncated {
			return err
		}
		if attempt > 0 {
			return fmt.Errorf("%w (max_tokens %d)", ErrTruncated, maxTokens)
		}
		maxTokens *= 2
	}
}

// ErrNoObject: the reply holds no JSON object at all.
var ErrNoObject = errors.New("ai: no JSON object in reply")

// ErrUnterminated: the reply's JSON object never closes (and the reply was not cut off at max_tokens).
var ErrUnterminated = errors.New("ai: unterminated JSON object in reply")

// ErrTruncated: the reply stopped at max_tokens before the answer was complete, even with the room doubled.
var ErrTruncated = errors.New("ai: reply cut off at max_tokens")

// ErrRefused: the model declined the request (the Messages API's stop_reason "refusal", e.g. a safety classifier).
// The same input is declined again, so sending it as is once more only spends a call.
var ErrRefused = errors.New("ai: the model declined to answer")

// RefusalError is ErrRefused with what the upstream said: StopReason is "refusal" (the Messages API's stop_reason, a
// Responses API refusal part or a Chat Completions refusal delta) or "content_filter" (an incomplete response's reason
// or a Chat Completions finish_reason). errors.Is(err, ErrRefused) holds for it; the Reply returned with it still has
// the call's Model and Usage.
type RefusalError struct {
	StopReason string
}

func (e *RefusalError) Error() string { return ErrRefused.Error() }
func (e *RefusalError) Unwrap() error { return ErrRefused }

// FirstObject extracts the first balanced {...} from model output (tolerating fences and chatter).
func FirstObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", ErrNoObject
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case esc:
			esc = false
		case ch == '\\' && inStr:
			esc = true
		case ch == '"':
			inStr = !inStr
		case inStr:
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", ErrUnterminated
}

// unmarshalReply is json.Unmarshal for a model's object, whose strings may hold what JSON does not allow there: an
// escape JSON lacks (#2151: \' as JavaScript and Python write a quote, a lone backslash as in a Windows path), or a
// control character written as it is (#4872: the line breaks of a summary in paragraphs, a tab). Those are repaired
// only when the object does not parse as it is, so valid JSON is never touched; when the repair does not help, the
// first error stands, as it is the one that says what the model wrote.
func unmarshalReply(data []byte, out any) error {
	err := json.Unmarshal(data, out)
	var syntax *json.SyntaxError
	if err == nil || !errors.As(err, &syntax) {
		return err
	}
	if fixed, changed := fixEscapes(data); changed && json.Unmarshal(fixed, out) == nil {
		return nil
	}
	return err
}

// fixEscapes rewrites, inside strings, what JSON does not allow there: \' becomes a quote, any other unknown \x a
// literal backslash followed by x, and a control character (U+0000–U+001F) written as it is its escape. The escapes
// JSON has are kept as they are, and so is everything outside strings (line breaks and tabs are whitespace there).
func fixEscapes(data []byte) ([]byte, bool) {
	var b bytes.Buffer
	b.Grow(len(data) + 8)
	changed, inStr := false, false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		switch {
		case !inStr:
			inStr = ch == '"'
		case ch == '"':
			inStr = false
		case ch == '\\' && i+1 < len(data):
			switch next := data[i+1]; next {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				b.WriteByte(ch)
				b.WriteByte(next)
				i++
			case '\'':
				b.WriteByte(next)
				i++
				changed = true
			default:
				b.WriteString(`\\`)
				changed = true
			}
			continue
		case ch < 0x20:
			b.WriteString(controlEscape(ch))
			changed = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.Bytes(), changed
}

// controlEscape is JSON's escape for a control character: the short one where JSON has it, \u00XX for the rest.
func controlEscape(ch byte) string {
	switch ch {
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	}
	return fmt.Sprintf(`\u%04x`, ch)
}
