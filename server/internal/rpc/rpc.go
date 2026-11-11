// Package rpc is a tiny bidirectional JSON-lines RPC used between hub and node over SSH stdio.
//
// Each line is one of:
//
//	{"id":"h1","method":"m","params":{}}      request
//	{"id":"h1","result":{}} / {"id":"h1","error":{"message":"…"}}   response
//	{"event":"e","params":{}}                 one-way event
//	{"event":"rpc.cancel","params":{"id":"h1"}}   the caller no longer waits for request h1 (Peer.Cancels)
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

type EventHandler func(event string, params json.RawMessage)

type message struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Event  string          `json:"event,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	// line is the whole line, written already (Marshaled params); nothing else is set.
	line []byte
}

type Error struct {
	Message string `json:"message"`
	// Code and Data are set by handlers that return an *Error themselves, for callers that act on the kind of
	// failure (a refused send) rather than show the message.
	Code string          `json:"code,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// WireError is its code and data, for the extension SDK (sdk/go/ext) to send as they are.
func (e *Error) WireError() (string, json.RawMessage) { return e.Code, e.Data }

var ErrClosed = errors.New("rpc: connection closed")

var errFrameBudget = errors.New("rpc: runtime frame budget exceeded")

// ErrDropped is what Call answers when the connection closed after the request went out: the other side may have done
// it. It is ErrClosed too (errors.Is), with the same words (a relay further on passes them as they are).
var ErrDropped error = droppedError{}

type droppedError struct{}

func (droppedError) Error() string        { return ErrClosed.Error() }
func (droppedError) Is(target error) bool { return target == ErrClosed }

// Limits are optional transport budgets for untrusted runtime peers. Zero preserves
// existing process/node behavior; configure them before Serve or any concurrent call.
type Limits struct{ FrameBytes, ConcurrentRequests, PendingCalls int }

type Peer struct {
	Limits Limits
	prefix string
	w      io.Writer
	// wsem is a one-slot semaphore around w, not a mutex: a write can block for good (SSH stdin to a frozen node,
	// a stalled relay WebSocket), and whoever waits behind it must still return on its ctx or on Close.
	wsem    chan struct{}
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[string]chan message
	closed  chan struct{}
	once    sync.Once
	err     error

	OnRequest Handler
	OnEvent   EventHandler
	// Cancels: the methods whose calls tell the other side when their ctx ends first (cancelEvent), which cancels
	// that request's ctx. None by default: extensions and connectors speak this protocol too and need not know the
	// event, and most calls are meant to finish on the other side anyway. A peer that does not know the event ignores
	// it, as before: its handler runs on.
	Cancels map[string]bool

	running map[string]context.CancelFunc // requests being handled, by id: what a cancelEvent ends
	// Only bounded peers couple accepted work to closure; legacy peers retain their cancellation semantics.
	runtimeCancel context.CancelFunc
}

// cancelEvent is sent for a request the caller no longer waits for: an approval the hub asks for on the phone stays
// pending otherwise, and allowing it later runs a tool call nobody is waiting on (a stale message sent, #2597).
const cancelEvent = "rpc.cancel"

// connKey carries the connection's context in a handler's ctx (ConnContext).
type connKey struct{}

// ConnContext is the context of the connection a handler's request came in on: it ends with the connection, not with
// the request. A handler's own ctx ends when the handler returns (and when the caller cancels it), so anything meant
// to outlive the request (a watch that keeps pushing after its reply) must hang off this one (#3139: a transcript
// watch that took the request's ctx ended with the reply, and no session's items were pushed any more). A ctx that
// did not come from Serve is its own connection.
func ConnContext(ctx context.Context) context.Context {
	if c, ok := ctx.Value(connKey{}).(context.Context); ok {
		return c
	}
	return ctx
}

// New creates a peer. prefix distinguishes request ids by side ("h" for hub, "n" for node).
func New(prefix string, w io.Writer) *Peer {
	return &Peer{prefix: prefix, w: w, pending: map[string]chan message{}, running: map[string]context.CancelFunc{}, closed: make(chan struct{}), wsem: make(chan struct{}, 1)}
}

// Serve reads messages until r fails; it returns the read error.
func (p *Peer) Serve(ctx context.Context, r io.Reader) error {
	if p.Limits.ConcurrentRequests > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		p.mu.Lock()
		p.runtimeCancel = cancel
		select {
		case <-p.closed:
			cancel()
		default:
		}
		p.mu.Unlock()
	}
	maximum := 64 * 1024 * 1024
	if p.Limits.FrameBytes > 0 {
		maximum = p.Limits.FrameBytes
	}
	sc := newLineReader(r, maximum) // a bufio.Scanner that gives back what a long line grew (linereader.go)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch {
		case m.Method != "":
			// Registered before the handler starts: a cancel right behind the request finds it.
			hctx, cancel := context.WithCancel(context.WithValue(ctx, connKey{}, ctx))
			p.mu.Lock()
			if p.Limits.ConcurrentRequests > 0 && (len(p.running) >= p.Limits.ConcurrentRequests || p.running[m.ID] != nil || m.ID == "") {
				p.mu.Unlock()
				cancel()
				err := errors.New("rpc: runtime request budget exceeded")
				p.Close(err)
				return err
			}
			p.running[m.ID] = cancel
			p.mu.Unlock()
			go p.handle(hctx, m)
		case m.Event == cancelEvent:
			var c struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(m.Params, &c)
			p.mu.Lock()
			cancel := p.running[c.ID]
			p.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case m.Event != "":
			if p.OnEvent != nil {
				p.OnEvent(m.Event, m.Params)
			}
		case m.ID != "":
			p.mu.Lock()
			ch := p.pending[m.ID]
			delete(p.pending, m.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	p.Close(err)
	return err
}

func (p *Peer) handle(ctx context.Context, m message) {
	defer func() {
		p.mu.Lock()
		cancel := p.running[m.ID]
		delete(p.running, m.ID)
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}()
	var res any
	var err error
	if p.OnRequest == nil {
		err = fmt.Errorf("no handler for %s", m.Method)
	} else {
		res, err = p.OnRequest(ctx, m.Method, m.Params)
	}
	out := message{ID: m.ID}
	var coded *Error
	if errors.As(err, &coded) {
		out.Error = coded
	} else if err != nil {
		out.Error = &Error{Message: err.Error()}
	} else {
		b, merr := marshal(res)
		if merr != nil {
			out.Error = &Error{Message: merr.Error()}
		} else {
			out.Result = b
		}
	}
	if err := p.write(out); errors.Is(err, errFrameBudget) {
		// The request must get an error, not a permanently unresolved promise.
		// If even the error cannot fit, disconnect so all pending calls fail.
		if err := p.write(message{ID: m.ID, Error: &Error{Message: errFrameBudget.Error()}}); err != nil {
			p.Close(err)
		}
	}
}

func (p *Peer) write(m message) error {
	return p.writeContext(context.Background(), m)
}

// encodeLine is marshal(m) and its newline, encoded into one buffer: the same bytes and errors without copying the
// line once more to append the newline.
func encodeLine(m message) ([]byte, error) {
	if m.line != nil {
		return m.line, nil
	}
	var b bytes.Buffer
	if err := plainEncoder(&b).Encode(m); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// marshal is json.Marshal(v) with < > & as they are. Escaped, they cost six bytes each, and a transcript's HTML or
// code went out at up to three times its size in every page and push (#2907); a later encoder keeps escapes it is
// given, so only the first hop can leave them out. Escapes already in a json.RawMessage stay as they are, and U+2028
// and U+2029 are still escaped, as encoding/json does without HTML escaping.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := plainEncoder(&b).Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// plainEncoder is an encoder to w that leaves < > & as they are (marshal).
func plainEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

func (p *Peer) writeContext(ctx context.Context, m message) error {
	b, err := encodeLine(m)
	if err != nil {
		return err
	}
	if p.Limits.FrameBytes > 0 && len(b) > p.Limits.FrameBytes {
		return errFrameBudget
	}
	// Wait for the writer, but not past the caller or the connection: the write ahead may never finish. A write
	// already under way cannot be interrupted.
	select {
	case p.wsem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.closed:
		return ErrClosed
	}
	defer func() { <-p.wsem }()
	// Both may have become ready together and select picks at random: check again so a cancelled request never
	// reaches the wire.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-p.closed:
		return ErrClosed
	default:
	}
	_, err = p.w.Write(b)
	return err
}

// Call sends a request and decodes the result into out (if non-nil). Params that are Appended are written straight
// into the request's line.
func (p *Peer) Call(ctx context.Context, method string, params any, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := fmt.Sprintf("%s%d", p.prefix, p.nextID.Add(1))
	ch := make(chan message, 1)
	p.mu.Lock()
	if p.Limits.PendingCalls > 0 && len(p.pending) >= p.Limits.PendingCalls {
		p.mu.Unlock()
		return errors.New("rpc: runtime pending-call budget exceeded")
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	var req message
	if a, ok := params.(Appended); ok {
		if size, ok := callSize(id, method, a); ok {
			if p.Limits.FrameBytes > 0 && size > p.Limits.FrameBytes { // before the line is made
				return errFrameBudget
			}
			req.line = callLine(id, method, a, size)
		} else {
			params = json.RawMessage(a.Append(make([]byte, 0, a.Size))) // the encoder writes the envelope
		}
	}
	if req.line == nil {
		raw, err := marshal(params)
		if err != nil {
			return err
		}
		req = message{ID: id, Method: method, Params: raw}
	}
	if err := p.writeContext(ctx, req); err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		if p.Cancels[method] {
			params, _ := json.Marshal(map[string]string{"id": id})
			_ = p.write(message{Event: cancelEvent, Params: params})
		}
		return ctx.Err()
	case <-p.closed:
		return ErrDropped
	}
}

// Notify sends a one-way event.
func (p *Peer) Notify(event string, params any) error {
	if raw, ok := params.(Marshaled); ok {
		if line, ok := marshaledLine(event, raw); ok {
			return p.write(message{line: line})
		}
		params = json.RawMessage(raw)
	}
	// Bytes already encoded go into the envelope as they are: its encoder checks and compacts them as this one would,
	// with the same errors, so the line is the same without encoding them twice (a computer's session list each time
	// it changes). An empty one is what json.Marshal says it is (an error).
	if raw, ok := params.(json.RawMessage); ok && len(raw) > 0 {
		return p.write(message{Event: event, Params: raw})
	}
	raw, err := marshal(params)
	if err != nil {
		return err
	}
	return p.write(message{Event: event, Params: raw})
}

func (p *Peer) Close(err error) {
	p.once.Do(func() {
		p.err = err
		close(p.closed)
		p.mu.Lock()
		if p.runtimeCancel != nil {
			p.runtimeCancel()
		}
		p.mu.Unlock()
	})
}

func (p *Peer) Done() <-chan struct{} { return p.closed }

// Err is why the peer was closed (what Close was given); nil while it is open.
func (p *Peer) Err() error {
	select {
	case <-p.closed:
		return p.err
	default:
		return nil
	}
}
