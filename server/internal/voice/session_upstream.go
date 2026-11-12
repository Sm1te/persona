package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/prompts"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// upstreamError is a failure of the realtime service the app shows by its code.
type upstreamError struct {
	code string
	err  error
}

func (e *upstreamError) Error() string { return e.err.Error() }
func (e *upstreamError) Unwrap() error { return e.err }

// upstreamCode is the code of a failure to start the upstream session ("" when it is not one the app knows).
func upstreamCode(err error) string {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return ue.code
	}
	return ""
}

// connect opens an upstream session and waits for it to start; the first one answers the app's start with ready.
// woken: it is the session a wake opens (suspend.go), not one after a drop: its instructions say the voice was paused.
func (c *conversation) connect(woken bool) error {
	c.mu.Lock()
	p, w, model, voice := c.p, c.w, c.model, c.voice
	history := voiceLines(c.history)
	if !c.started && !c.resumed {
		history = nil
	}
	c.mu.Unlock()

	began := time.Now()
	dctx, cancel := context.WithTimeout(c.ctx, startTimeout)
	defer cancel()
	_, auto := p.toolsMode()
	if auto && c.pl.refusesTools(p.ID) {
		c.toolless.Store(true) // another session found that its service refuses them (#1420)
	}
	bound := c.bind(dctx) // the bound conversation as it is now, read while the socket connects
	up, err := c.dial(dctx, p, w, model)
	if err != nil {
		return err
	}
	online, err := bound()
	if err != nil {
		_ = up.CloseNow()
		return err
	}
	c.mu.Lock()
	snap, waiting := c.snap, c.waiting()
	c.mu.Unlock()
	in := prompts.VoiceInstructions{
		Lang: string(c.language()), Owner: c.pl.cfg.Owner, Notes: c.pl.cfg.Notes, History: clipHistory(history),
		Target: snap, Waiting: waiting, Paused: woken,
	}
	tools := c.toolSet()
	rate, err := c.startSession(dctx, up, w, p, model, voice, in, tools)
	if err != nil && len(tools) > 0 && auto && refusedTools(err) {
		// The service does not take function tools (#1420): the same session without them, once. Remembered only once
		// that one starts: a session refused all the same was not refused for its tools.
		c.logf("provider %s: the service refused the function tools as the session started (%s); starting it again without them", p.ID, failure(err))
		if up, err = c.dial(dctx, p, w, model); err == nil {
			rate, err = c.startSession(dctx, up, w, p, model, voice, in, nil)
		}
		if err == nil {
			c.toolless.Store(true)
			if c.pl.forgoTools(p.ID) {
				c.logf("provider %s: its sessions go without function tools from now on", p.ID)
			}
		}
	}
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = up.CloseNow()
		return errors.New("closed")
	}
	c.up, c.ready, c.connectedAt = up, true, time.Now()
	gen := c.gen
	first := !c.started
	why := "first"
	switch {
	case woken:
		why = "woken"
	case !first:
		why = fmt.Sprintf("reconnect %d", c.reconnects)
	}
	c.logf("upstream: session %d started in %v (%s), %d Hz, tools %s", sessionNo(gen), logDur(c.connectedAt.Sub(began)), why, rate,
		c.toolsLog(p, c.toolSet()))
	// A session after a drop is told the bound conversation as it is now: an agent's words the last session had not
	// taken in (its turn went on meanwhile) close the user-speech gate, as a result sent does. The same snapshot does
	// not: a request the user made just before the drop still counts.
	if words := agentWords(snap); words != c.snapWords {
		if !first {
			c.gate.told(c.connectedAt)
		}
		c.snapWords = words
	}
	if first {
		c.started, c.outRate = true, rate
		ready := map[string]any{"type": "ready", "input_rate": p.inputRate(), "output_rate": rate, "model": c.modelID, "voice": nilIfEmpty(voice)}
		if c.version >= 2 {
			ready["protocol"], ready["features"], ready["target"] = infoProtocol, features, c.target.frame()
		}
		c.sendApp(ready)
		if !online {
			c.failCode(codeMachineOffline, "这台电脑离线，说的话要等它上线才发得出去", false)
		}
	} else {
		if rate != c.outRate {
			c.logf("reconnected session speaks at %d Hz, the app plays %d", rate, c.outRate)
		}
		if c.version >= 2 {
			c.sendApp(map[string]any{"type": "link", "state": linkLive})
		} else {
			c.sendApp(map[string]any{"type": "tool", "id": "reconnect", "done": true})
		}
	}
	resume := c.resume
	c.resume = nil
	c.mu.Unlock()
	go c.readUpstream(up, gen)
	if !first {
		c.runs.reconnected()
		return nil
	}
	c.runs.arm() // its cards (asks.go): the call is set up; before the runs handed back, which a card may cover
	if len(resume) > 0 {
		c.runs.resume(resume)
	}
	if c.version >= 2 {
		c.runs.observeTarget(linkKey{c.target.MachineID, c.target.SessionID}) // after the runs handed back
	}
	return nil
}

// startSession starts the upstream session on up, declaring tools (nil: none), and waits for the service to start it:
// the rate of its audio. A failure closes up; the service's refusal is a *sessionRefused.
func (c *conversation) startSession(ctx context.Context, up upLink, w wire, p *Provider, model, voice string,
	in prompts.VoiceInstructions, tools []string) (int, error) {
	in.Tools = c.toolMode(tools)
	s := session{Model: model, Voice: voice, InputRate: p.inputRate(), Instructions: fitInstructions(in), Tools: toolDecls(tools)}
	return startUpstream(ctx, up, w, p, s)
}

// startUpstream sends session s's start on up and waits for the service to start it: the rate of its audio. A failure
// closes up; the service's refusal is a *sessionRefused. A call's sessions and a read-aloud's (speak.go) start alike.
func startUpstream(ctx context.Context, up upLink, w wire, p *Provider, s session) (int, error) {
	if err := writeJSON(ctx, up, w.start(s)); err != nil {
		_ = up.CloseNow()
		return 0, err
	}
	for {
		typ, b, err := up.Read(ctx)
		if err != nil {
			_ = up.CloseNow()
			return 0, fmt.Errorf("the session did not start: %w", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		for _, e := range w.decode(b) {
			switch e.kind {
			case evStarted:
				if e.rate == 0 {
					return w.outputRate(p.inputRate()), nil
				}
				return e.rate, nil
			case evError:
				_ = up.CloseNow()
				var err error = &sessionRefused{code: e.code, param: e.param, text: e.text}
				if e.code == codeUpstreamUnavailable {
					err = &upstreamError{code: codeUpstreamUnavailable, err: err}
				}
				return 0, err
			}
		}
	}
}

// sessionRefused is the service's error frame in answer to a session start.
type sessionRefused struct{ code, param, text string }

func (e *sessionRefused) Error() string {
	return firstNonEmpty(e.text, e.code, "the service refused the session")
}

// fieldCodes are the codes of an error about a field the service does not know or take (docs/lessons/voice.md
// "Errors": unknown_parameter, unsupported_extension).
var fieldCodes = []string{"unknown_parameter", "unsupported_parameter", "unsupported_extension", "unsupported_capability"}

// refusedTools: the service refused a session start for its function tools (#1420). Its error frame names them (param
// session.tools), or names no field and says "tools" or is about a field it does not know or take: the tools are all a
// session start has beyond what one without them has. Or it closed the socket before the session started, saying
// "tools". Never a refused key or handshake, a service that is down, nor a socket that just went away.
func refusedTools(err error) bool {
	var ue *upstreamError
	if errors.As(err, &ue) {
		return false
	}
	var r *sessionRefused
	if errors.As(err, &r) {
		if r.param != "" {
			return namesTools(r.param)
		}
		return namesTools(r.text) || slices.Contains(fieldCodes, r.code)
	}
	var ce websocket.CloseError
	return errors.As(err, &ce) && namesTools(ce.Reason)
}

func namesTools(s string) bool { return strings.Contains(strings.ToLower(s), "tools") }

// instructionsMost is the most a session's instructions run to, in runes: the measured service started a session with
// 19000 characters of them and refused one with 25000, the limit counting what it adds itself (docs/lessons/voice.md
// "Instructions size"); refused, a session does not start at all. A long call's history (historyLines of up to 200
// runes) with a full snapshot comes near that in English.
const instructionsMost = 16000

// fitInstructions renders in, the oldest lines of its history left out as far as it takes to keep within
// instructionsMost.
func fitInstructions(in prompts.VoiceInstructions) string {
	s := prompts.RenderVoiceInstructions(in)
	for runeLen(s) > instructionsMost && len(in.History) > 0 {
		in.History = in.History[1:]
		s = prompts.RenderVoiceInstructions(in)
	}
	return s
}

// dial opens the upstream socket: the service's WebSocket, or the fake dialect's stand-in.
func (c *conversation) dial(ctx context.Context, p *Provider, w wire, model string) (upLink, error) {
	return dialUpstream(ctx, p, w, model)
}

// dialUpstream opens provider p's socket for model, with its credential: the service's WebSocket, or the fake
// dialect's stand-in.
func dialUpstream(ctx context.Context, p *Provider, w wire, model string) (upLink, error) {
	if p.Dialect == DialectFake {
		return newFakeLink(), nil
	}
	key, err := p.credential(ctx)
	if err != nil {
		return nil, err
	}
	u, err := w.url(p.URL, model)
	if err != nil {
		return nil, err
	}
	header := w.header()
	if key != "" {
		header.Set("Authorization", "Bearer "+key)
	}
	opts := &websocket.DialOptions{HTTPHeader: header, HTTPClient: p.httpClient()}
	up, res, err := websocket.Dial(ctx, u, opts)
	if err != nil {
		return nil, dialError(err, res)
	}
	up.SetReadLimit(16 << 20)
	return up, nil
}

// toolSet is which of the plugin's function tools the session declares (docs/assistant/voice.md "Permissions"): all of them for a full
// device; for another extension calling (role extension) only ask_persona, to the main conversation, which is what it may
// reach: the others reach past that (the knowledge base, any agent), and without a tool the measured service hands
// nothing over at all (docs/lessons/voice.md "Other behavior"); none for an upstream that takes no tools (Provider.Tools
// false, the codex and fake dialects, a service that refused them: toolless).
func (c *conversation) toolSet() []string {
	declare, _ := c.p.toolsMode()
	switch {
	case !declare || c.toolless.Load():
		return nil
	case c.version >= 2:
		// Bound to a conversation: what hands work over goes to it (delegate_task); send_to_agent and ask_persona would
		// go past it. The measured service hands nothing over without a tool (docs/lessons/voice.md "Other behavior").
		return boundTools
	case c.role == "" || c.role == roleFull:
		return toolNames
	}
	return []string{"ask_persona"}
}

// tools: the session declares function tools (a delegation comes with its call).
func (c *conversation) tools() bool { return len(c.toolSet()) > 0 }

// toolMode is what the instructions say of tools (prompts.VoiceInstructions.Tools) for a session that declares tools.
func (c *conversation) toolMode(tools []string) string {
	switch {
	case c.target != nil && len(tools) > 0:
		return "bound"
	case len(tools) == len(toolNames):
		return "all"
	case len(tools) > 0:
		return "ask_persona"
	}
	return ""
}

// dialError adds what the service answered to a refused handshake (the library keeps the first KB of its body).
func dialError(err error, res *http.Response) error {
	if res == nil {
		return err
	}
	if res.Body == nil {
		res.Body = http.NoBody
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	msg := strings.TrimSpace(string(raw))
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	err = fmt.Errorf("HTTP %d", res.StatusCode)
	if msg != "" {
		err = fmt.Errorf("HTTP %d: %s", res.StatusCode, clipRunes(msg, 200))
	}
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &upstreamError{code: codeUpstreamForbidden, err: err}
	case http.StatusServiceUnavailable:
		return &upstreamError{code: codeUpstreamUnavailable, err: err}
	}
	return err
}

func (c *conversation) readUpstream(up upLink, gen int) {
	for {
		typ, b, err := up.Read(c.ctx)
		if err != nil {
			c.lost(gen, err)
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		c.mu.Lock()
		if c.gen != gen || c.closed {
			c.mu.Unlock()
			return
		}
		for _, e := range c.w.decode(b) {
			c.handle(gen, e)
		}
		c.mu.Unlock()
	}
}

// handle applies one upstream event (mu held).
func (c *conversation) handle(gen int, e event) {
	switch e.kind {
	case evAudio:
		now := time.Now()
		c.replyVoiced = true
		if c.dropAudio && (now.Sub(c.lastAudio) > 1500*time.Millisecond || c.replyAfterCut(now)) {
			c.dropAudio, c.cutAt = false, time.Time{} // a pause: this is the next answer
		}
		c.lastAudio = now
		if c.dropAudio {
			return // the rest of an answer cut off: the user never hears it, so it answers nothing (nudge.go)
		}
		c.modelActed(now) // before its caption of the user's line closes, if that is still open: this answers it
		c.pace.played(now, len(e.audio), c.outRate)
		c.closeUser()
		select {
		case c.out <- outFrame{audio: e.audio}:
		case <-c.ctx.Done():
		}
	case evUser:
		c.dropAudio, c.cutAt = false, time.Time{}
		spoke := c.spokeAt(time.Now()) // by the user's words before these
		if strings.TrimSpace(e.text) != "" {
			c.gate.user = time.Now()
			c.userAt = c.gate.user
		}
		c.userLineDone(c.userText(e.text, e.replace, e.final, spoke)) // a line the service ends itself (codex)
	case evAssistant:
		if strings.TrimSpace(e.text) != "" {
			c.modelActed(time.Now())
		}
		c.asstText(e.text, e.replace, e.final, e.name)
	case evReply:
		c.signals, c.turnsEnd = true, true
		c.reply, c.replyVoiced = e.id, false
		c.replying = true
	case evTurnEnd:
		c.dropAudio, c.cutAt = false, time.Time{}
		c.turnsEnd = true
		c.replying = false
		if c.createOwed {
			c.createOwed = false
			go c.sendUp(gen, responseCreate)
		}
		if e.id != "" {
			c.signals = true
			if c.replied = append(c.replied, e.id); len(c.replied) > repliedMost {
				c.replied = c.replied[1:]
			}
			if e.id == c.reply && !c.replyVoiced {
				if c.silent = append(c.silent, e.id); len(c.silent) > repliedMost {
					c.silent = c.silent[1:]
				}
			}
		}
		c.closeAsst()
	case evInterrupted:
		if e.id != "" {
			c.signals = true
		}
		if e.id != "" && slices.Contains(c.replied, e.id) {
			// A reply that was over already, superseded by the next one (a tool's result, a say): nothing of it is left
			// to drop, and a clear would cut the end of it the app still plays (docs/lessons/voice.md"Other behavior"). On
			// either protocol (#4719): protocol 1's apps play behind the upstream the same way.
			return
		}
		c.sendApp(map[string]any{"type": "clear"})
		c.pace.playEnd = time.Now() // the app drops what it had not played
		c.closeAsst()
	case evDelegation:
		c.modelActed(time.Now())
		c.closeUser()
		c.pace.delegated = time.Now()
		if e.id == "" {
			return
		}
		c.delegations[e.id] = gen
		c.count(func(l *callLog) { l.delegations++ })
		at, reply := c.gate.agent, c.reply // the gate as it was when the model handed this over, and its reply doing so
		if c.tools() {
			c.logf("delegation %s: made in session %d, waiting %v for its function call", e.id, sessionNo(gen), toolCallWait)
			id, text := e.id, e.text
			c.pending[id] = time.AfterFunc(toolCallWait, func() {
				c.mu.Lock()
				defer c.mu.Unlock()
				if _, ok := c.pending[id]; ok && !c.closed {
					delete(c.pending, id)
					c.logf("delegation %s: no function call came: handed over as it is", id)
					c.startTask(id, text, at, reply)
				}
			})
			return
		}
		c.logf("delegation %s: made in session %d", e.id, sessionNo(gen))
		c.startTask(e.id, e.text, at, reply)
	case evToolCall:
		c.modelActed(time.Now())
		c.closeUser()
		c.pace.delegated = time.Now()
		c.count(func(l *callLog) { l.calls++ })
		c.logf("function call %s (call %s, delegation %s, session %d)", compat.Tool(e.name), firstNonEmpty(e.callID, "-"),
			firstNonEmpty(e.id, "-"), sessionNo(gen))
		if t := c.pending[e.id]; t != nil {
			t.Stop()
			delete(c.pending, e.id)
		}
		c.runTool(gen, e)
	case evAck:
		if s := c.unacked[e.eventID]; s != nil {
			delete(c.unacked, e.eventID)
			if s.agent {
				c.gate.told(time.Now()) // the model has read them only now
			}
		}
	case evError:
		c.logf("upstream: session error (%s, %s): %s", e.code, firstNonEmpty(e.param, "-"), clipRunes(e.text, errorRunes))
		c.serviceRefused(e)
	}
}

// sendUp writes to the current upstream session, if it is still gen (any gen when gen < 0).
func (c *conversation) sendUp(gen int, frames ...any) bool {
	c.mu.Lock()
	up, cur, ready := c.up, c.gen, c.ready
	if ready && up != nil && (gen < 0 || gen == cur) && c.replying {
		// A response.create while a reply is under way is refused: it goes when that reply is over (evTurnEnd).
		frames = slices.DeleteFunc(slices.Clone(frames), func(f any) bool {
			if isResponseCreate(f) {
				c.createOwed = true
				return true
			}
			return false
		})
	}
	c.mu.Unlock()
	if !ready || up == nil || (gen >= 0 && gen != cur) {
		return false
	}
	for _, f := range frames {
		if err := c.writeUp(up, f); err != nil {
			return false
		}
	}
	return true
}

// responseCreate asks the service for a response (realtime dialect).
var responseCreate = map[string]any{"type": "response.create"}

func isResponseCreate(f any) bool {
	m, ok := f.(map[string]any)
	return ok && m["type"] == "response.create"
}

// lost: the upstream session went away while the app is still here; a new one is told what was said.
func (c *conversation) lost(gen int, why error) {
	c.mu.Lock()
	if c.closed || gen != c.gen {
		c.mu.Unlock()
		return
	}
	held := time.Duration(0)
	if !c.connectedAt.IsZero() {
		held = time.Since(c.connectedAt).Round(time.Second)
	}
	c.logf("upstream: session %d lost after %v: %v", sessionNo(gen), held, why)
	if !c.connectedAt.IsZero() && time.Since(c.connectedAt) >= stableAfter {
		c.reconnects = 0 // it held: a drop now is not one of a run of them (docs/assistant/voice.md "Reconnect and resume")
	}
	if c.reconnects >= maxReconnects {
		c.mu.Unlock()
		c.failCode(codeUpstreamFailed, "语音连接断了，重连也没成功。点下面的按钮重新开始", true)
		return
	}
	c.reconnects++
	c.count(func(l *callLog) { l.reconnects++ })
	c.gen++
	c.ready = false
	c.replying, c.createOwed = false, false // that session's reply, and what waited on it
	old := c.up
	c.up = nil
	for id, t := range c.pending {
		t.Stop()
		delete(c.pending, id)
	}
	clear(c.unacked) // that session's: an ack or refusal of it never comes
	c.pace.playEnd = time.Now()
	c.sendApp(map[string]any{"type": "clear"})
	c.closeAsst()
	c.closeUser()
	if c.owed.result {
		c.owed = owedAnswer{} // what was handed to that session went with it; the user's line is in the next one's history
	}
	if c.version >= 2 {
		c.sendApp(map[string]any{"type": "link", "state": linkReconnecting})
	} else {
		c.sendApp(map[string]any{"type": "tool", "id": "reconnect", "label": "连接断了，重新连接中", "done": false})
	}
	c.mu.Unlock()
	if old != nil {
		_ = old.CloseNow()
	}
	go func() {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-c.ctx.Done():
					return
				case <-time.After(time.Duration(attempt) * 2 * time.Second):
				}
			}
			if err = c.connect(false); err == nil {
				c.flushRing() // a woken session that dropped before it had what was kept while paused (suspend.go)
				return
			}
			if c.ctx.Err() != nil {
				return
			}
			c.logf("reconnect (try %d of 3) failed: %s", attempt+1, failure(err))
		}
		c.failCode(codeUpstreamFailed, "语音连接断了，重连没成功："+err.Error(), true)
	}()
}
