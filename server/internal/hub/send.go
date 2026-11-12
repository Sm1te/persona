package hub

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Sending a message from the phone (docs/protocol/protocol.md "Sending messages"). The app sends each message with its own
// client_msg_id and resends what got no answer with the same id; the node types it into the agent's terminal. A
// message must arrive once: the hub remembers every id — in memory while a send runs, in Postgres after — so a retry
// is answered from what happened instead of typed again, also after the node reconnected (a new node process knows
// nothing of earlier sends) or the hub restarted. A send whose connection broke while it was being typed is
// "unknown": the retry is not typed, and the app asks the user to look before sending again (with a new id).

const (
	sendDelivered = "delivered"
	sendUnknown   = "unknown"
	sendSending   = "sending"
)

type sendLog struct {
	mu      sync.Mutex
	flights map[string]*sendFlight
}

// sendFlight is one client_msg_id's send: retries arriving while it runs wait for it and get the same answer.
type sendFlight struct {
	done   chan struct{}
	status int
	body   []byte
	at     time.Time
}

// join returns the flight for id and whether this request is the one to send it.
func (l *sendLog) join(id string) (*sendFlight, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.flights == nil {
		l.flights = map[string]*sendFlight{}
	}
	for k, f := range l.flights {
		if time.Since(f.at) > 10*time.Minute {
			delete(l.flights, k)
		}
	}
	if f := l.flights[id]; f != nil {
		return f, false
	}
	f := &sendFlight{done: make(chan struct{}), at: time.Now()}
	l.flights[id] = f
	return f, true
}

// forget drops an id whose send typed nothing, so a retry sends it.
func (l *sendLog) forget(id string, f *sendFlight) {
	l.mu.Lock()
	if l.flights[id] == f {
		delete(l.flights, id)
	}
	l.mu.Unlock()
}

// withoutControls drops what acts on a terminal rather than being read: line breaks and tabs stay.
func withoutControls(s string) string {
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return -1
		}
		return c
	}, s)
}

// switchSendWait is how long the hub waits for a message carrying out a launcher switch: the node's checks before the
// quit take at most a minute and the rest at most five (node/launcher_switch.go switchProbeWait, switchBudget).
const switchSendWait = 7 * time.Minute

// sendWait is how long the hub waits for the node to take message p to session s (as listed; zero when not): 90
// seconds; 5 minutes for one that is starting or not running (a resume, node's claudeStartWait, #1396); switchSendWait
// for the user's message that carries out a switch to another launcher (apply_switch): it quits the agent and starts
// it again, and brings it back on its own launcher when that fails. Decided by the request, not the cached row, which
// may not show a switch picked a moment ago.
func sendWait(p map[string]any, s proto.Session) time.Duration {
	if apply, _ := p["apply_switch"].(bool); apply {
		return switchSendWait
	}
	if s.Starting || s.Status == proto.StatusOffline {
		return 5 * time.Minute
	}
	return 90 * time.Second
}

func (h *Hub) sendMessage(w http.ResponseWriter, r *http.Request, m *Machine) {
	p := map[string]any{}
	if r.ContentLength != 0 {
		if err := decodeJSONBody(w, r, &p, 32<<20); err != nil && !errors.Is(err, io.EOF) {
			writeBodyErr(w, err, err)
			return
		}
	}
	if p == nil {
		writeErr(w, http.StatusBadRequest, errors.New("request body must be a JSON object"))
		return
	}
	if id, _ := p["client_msg_id"].(string); strings.HasPrefix(id, assistantwire.FirstMessageIDPrefix) {
		// The manager's own, for a conversation's first message (assistant start.go humanStart): never an app's, so no
		// later message carries one (and passes for that first message). Refused before anything else is done with it.
		writeErrCode(w, http.StatusBadRequest, "client_msg_id_reserved", i18n.Err("hub-send.reserved_id"))
		return
	}
	sid := r.PathValue("sid")
	p["session_id"] = sid
	todoID, _ := p["todo_id"].(string)
	delete(p, "todo_id")
	if todoID != "" && principalOf(r.Context()).Role != roleFull {
		writeErr(w, http.StatusForbidden, errors.New("read-only role cannot modify todos"))
		return
	}
	text, isText := p["text"].(string)
	if isText {
		r = h.captureHumanSubmission(r, text)
	}
	for _, reserved := range []string{"creation_id", "incarnation", "caller", "submission", "share_authority"} {
		delete(p, reserved)
	}
	var start freshStart
	fresh := false
	if isText && strings.TrimSpace(text) != "" && !answerOnly(r) {
		// The first message of an agent started for a todo with none: a new agent's first prompt, with its project's
		// background and the todo's decisions first (#2820; iOS and Android send it without todo_id).
		if f, ok := h.fresh.peek(m.cfg.ID, sid); ok && (todoID == "" || todoID == f.todoID) {
			start, fresh = f, true
		}
	}
	if todoID != "" {
		// The manager checks the todo (404, 409, 503 assistant_offline while it is away); handed to the agent for it,
		// the calls already made on it go first (#1618).
		hand := assistantwire.TodoHandoff{TodoID: todoID, Text: text}
		if fresh {
			hand.New, hand.MachineID, hand.Cwd = true, m.cfg.ID, start.cwd
		}
		decided, ok := h.todoHandoff(r.Context(), w, hand)
		if !ok {
			return
		}
		if isText {
			p["text"] = decided
		}
	} else if fresh {
		p["text"] = h.todoStartText(r.Context(), assistantwire.TodoHandoff{TodoID: start.todoID, Text: text, New: true,
			MachineID: m.cfg.ID, Cwd: start.cwd})
	}
	if answerOnly(r) {
		// An answer-only device says something to a conversation it can see, no more. A message to an id nobody has starts
		// an agent on the computer; which program runs it, and whatever else a send can shape, are a full device's to pick;
		// and it is text for the agent, not keys for the terminal.
		s, ok := m.session(sid)
		if !ok || len(h.shown(m.cfg.ID, []proto.Session{s})) == 0 {
			writeErr(w, http.StatusNotFound, errors.New("no such session"))
			return
		}
		text, _ := p["text"].(string)
		text = withoutControls(text)
		if strings.TrimSpace(text) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("text is required"))
			return
		}
		said := map[string]any{"session_id": sid, "text": text}
		if id, ok := p["client_msg_id"]; ok {
			said["client_msg_id"] = id
		}
		p = said
	}
	if s, ok := m.session(sid); ok && m.isAssistant(s) {
		// The user is talking in it again: back in the list, and it works for them, not for a chain of conversations.
		h.asstOf(h.sessionOwner(m.cfg.ID, sid)).UserSpoke(r.Context(), assistantwire.SessionRef{MachineID: m.cfg.ID, SessionID: sid})
	}
	s, known := m.session(sid)
	picked := "" // a model picked in the chat's chip that the message runs on (#1644)
	assistantConv := false
	note := "" // the main chat runs on another launcher than configured (#4659): told in a delivered send's answer
	if h.isMainOn(m.cfg.ID, sid) || (known && (s.Assistant || m.isAssistant(s))) {
		assistantConv = true
		// One of the assistant's conversations: it says what the message runs on (and, the first one to a new main
		// conversation started empty, what the old one left before it).
		asked, _ := p["model"].(string)
		var err error
		if note, err = h.assistantSend(r.Context(), m, sid, p); err != nil {
			writeAssistantErr(w, err)
			return
		}
		if kept, _ := p["model"].(string); strings.TrimSpace(asked) != "" && kept == strings.TrimSpace(asked) {
			picked = kept
		}
	} else if l, _ := p["launcher"].(string); l == "" && known && resumesWithoutLauncher(s) {
		h.applyResumeLauncher(r.Context(), m, sid, p) // the manager's (agents_link.go)
	}
	// The send carries on when the phone stops waiting (backgrounded, network gone): it may be typing already, and
	// its outcome is what the retry gets. Resuming an offline session can take a while (shell + claude startup).
	// One sent while the session still starts waits for claude to be ready (node/starting.go); one to a session that is
	// not running resumes it first. Either can wait minutes on a busy machine (node's claudeStartWait, #1396).
	row, _ := m.session(sid)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), sendWait(p, row))
	defer cancel()
	id, _ := p["client_msg_id"].(string)
	if ch := channel.FromClientMsgID(id, []string{"voice"}); ch != "" {
		h.channelNoted(ch, m.cfg.ID, sid) // a voice conversation's errand (docs/extensions/channels.md "Source markers")
	}
	l := h.requestLang(r)
	external := false
	if s, ok := m.session(sid); ok {
		external = s.SourceKind != ""
	}
	if external && id == "" {
		callErr(w, &rpc.Error{Code: "codex_send_guard", Message: i18n.T(l, "hub-send.guard_need_id"), Data: json.RawMessage(`{"delivered":"none"}`)})
		return
	}
	modelSent := func(status int) { // the picked model ran: the manager may remember it
		if status == http.StatusOK && picked != "" {
			launcher, _ := p["launcher"].(string)
			h.modelUsed(h.sessionOwner(m.cfg.ID, sid), m.cfg.ID, launcher, picked)
		}
	}
	if id == "" {
		status, body, outcome := h.deliverOnce(ctx, m, p)
		body = withLauncherNote(status, body, note)
		if assistantConv {
			h.conversationSent(m.cfg.ID, sid, status, body, outcome)
		}
		modelSent(status)
		if status == http.StatusOK && fresh {
			h.fresh.done(m.cfg.ID, sid)
		}
		if status == http.StatusOK && todoID != "" {
			h.linkSentTodo(ctx, m, todoID, sid, body)
		}
		writeRaw(w, status, body)
		return
	}
	f, first := h.sends.join(id)
	if !first {
		select {
		case <-f.done:
			writeRaw(w, f.status, f.body)
		case <-r.Context().Done():
		}
		return
	}
	if external && h.store == nil {
		f.status, f.body = callErrBytes(l, &rpc.Error{Code: "codex_send_guard", Message: i18n.T(l, "hub-send.guard_no_record"), Data: json.RawMessage(`{"delivered":"none"}`)})
		close(f.done)
		h.sends.forget(id, f)
		writeRaw(w, f.status, f.body)
		return
	}
	state := ""
	if h.store != nil {
		var err error
		if state, err = h.store.ClaimSend(ctx, id, m.cfg.ID, sid); err != nil {
			if external {
				f.status, f.body = callErrBytes(l, &rpc.Error{Code: "codex_send_guard", Message: i18n.T(l, "hub-send.guard_no_record"), Data: json.RawMessage(`{"delivered":"none"}`)})
				close(f.done)
				h.sends.forget(id, f)
				writeRaw(w, f.status, f.body)
				return
			}
			h.logf("send %s: %v", id, err) // no record: the send still goes, without the guard
		}
	}
	switch state {
	case sendDelivered:
		f.status, f.body = http.StatusOK, withLauncherNote(http.StatusOK, []byte("{}\n"), note)
	case sendSending, sendUnknown: // an earlier hub or connection was sending it and never learned the outcome
		f.status, f.body = callErrBytes(l, unknownDelivery(l, "hub-send.lost_before"))
	default:
		var outcome string
		f.status, f.body, outcome = h.deliverOnce(ctx, m, p)
		f.body = withLauncherNote(f.status, f.body, note)
		if assistantConv {
			h.conversationSent(m.cfg.ID, sid, f.status, f.body, outcome)
		}
		if h.store != nil {
			if err := h.store.SettleSend(context.WithoutCancel(ctx), id, outcome); err != nil {
				h.logf("send %s: %v", id, err)
			}
		}
		if outcome == "" {
			defer h.sends.forget(id, f)
		}
	}
	close(f.done)
	modelSent(f.status)
	if f.status == http.StatusOK && fresh {
		h.fresh.done(m.cfg.ID, sid)
	}
	if f.status == http.StatusOK && todoID != "" {
		h.linkSentTodo(ctx, m, todoID, sid, f.body)
	}
	writeRaw(w, f.status, f.body)
}

// linkSentTodo links a todo to the session its message went to: a Codex that waited for its first message answers
// with the thread that message began (node/codex_waiting.go), and that thread is the todo's from now on.
func (h *Hub) linkSentTodo(ctx context.Context, m *Machine, todoID, sid string, body []byte) {
	var reply struct {
		Session *proto.Session `json:"session"`
	}
	var title, cwd, agent string
	if json.Unmarshal(body, &reply) == nil && reply.Session != nil && reply.Session.SessionID != "" {
		sid = reply.Session.SessionID
		title, cwd, agent = reply.Session.Title, reply.Session.Cwd, reply.Session.Agent
	}
	if s, ok := m.session(sid); ok {
		title, cwd, agent = s.Title, s.Cwd, s.Agent
	}
	if _, err := h.asst().TodoLink(ctx, assistantwire.TodoLinkParams{TodoID: todoID, MachineID: m.cfg.ID, SessionID: sid,
		Title: title, Cwd: cwd, Agent: agent}); err != nil {
		h.logf("sendMessage: link todo %s for %s/%s failed: %v", todoID, m.cfg.ID, sid, err)
	}
}

// deliverOnce asks the node to send and says what became of it: delivered, unknown (it may have been typed), or ""
// (nothing typed: refused, offline, failed before typing).
func (h *Hub) deliverOnce(ctx context.Context, m *Machine, p map[string]any) (int, []byte, string) {
	var out json.RawMessage
	err := m.call(ctx, "session.send", p, &out)
	switch outcome := sendOutcome(err); {
	case err == nil:
		if len(out) == 0 {
			out = json.RawMessage("{}")
		}
		return http.StatusOK, append(out, '\n'), outcome
	case outcome == sendUnknown && !hasDelivered(err):
		l := h.contextLang(ctx)
		status, body := callErrBytes(l, unknownDelivery(l, "hub-send.lost_during"))
		return status, body, outcome
	default:
		status, body := callErrBytes(h.contextLang(ctx), apierr.OnMachine(err, m.name())) // claude_missing on "<name>'s MacBook"
		return status, body, outcome
	}
}

// conversationSent tells the conversation's manager how an app's message to it went, delivered or not (the north
// star's availability, docs/assistant/north-star.md): only a send that ran, not a retry answered from its record.
func (h *Hub) conversationSent(mid, sid string, status int, body []byte, outcome string) {
	r := assistantwire.ConversationSent{MachineID: mid, SessionID: sid, Outcome: assistantwire.ConversationFailed}
	switch {
	case status == http.StatusOK:
		r.Outcome = assistantwire.ConversationDelivered
	case outcome == sendUnknown:
		r.Outcome = assistantwire.ConversationUnknown
	default:
		var e struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
			Error  string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		r.Code, r.Error = e.Code, cmp.Or(e.Detail, e.Error)
	}
	h.asstOf(h.sessionOwner(mid, sid)).notify(assistantwire.EventConversationSent, r)
}

// sendOutcome reads a node call's error: what may have reached the terminal.
func sendOutcome(err error) string {
	if err == nil {
		return sendDelivered
	}
	if errors.Is(err, errOffline) {
		return ""
	}
	var re *rpc.Error
	if errors.As(err, &re) { // the node answered: it marks failures after typing began
		var d struct {
			Delivered string `json:"delivered"`
		}
		_ = json.Unmarshal(re.Data, &d)
		if d.Delivered == sendUnknown {
			return sendUnknown
		}
		return ""
	}
	// No answer (the connection closed, the deadline passed): the node had the request and may have typed it.
	return sendUnknown
}

func hasDelivered(err error) bool {
	var re *rpc.Error
	return errors.As(err, &re) && bytes.Contains(re.Data, []byte(`"delivered"`))
}

// unknownDelivery says, in l, why (a hub-send key) the outcome is unknown.
func unknownDelivery(l i18n.Lang, why string) error {
	return &rpc.Error{Message: i18n.T(l, "hub-send.unknown", i18n.Args{"why": i18n.T(l, why)}), Data: json.RawMessage(`{"delivered":"unknown"}`)}
}

// callErrBytes is callErr's response as bytes, so waiting retries can get the same one.
// callErrBytes is what callErr answers for err, in language l (coded refusals are explained in it, apierr.Call).
func callErrBytes(l i18n.Lang, err error) (int, []byte) {
	rec := &bufferedResponse{header: http.Header{}, lang: l}
	callErr(rec, err)
	return rec.status, rec.body.Bytes()
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	lang   i18n.Lang
}

func (b *bufferedResponse) Lang() i18n.Lang { return b.lang } // i18n.Carrier

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(code int)        { b.status = code }

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// resumesWithoutLauncher: a message to this session resumes it (it is not running) and nothing on its machine says
// which launcher it ran with (started outside Persona, no launcher matches its base URL and model). The node would
// fall back to plain claude, which may not be signed in there (node/sendsafe.go resumeLauncher).
func resumesWithoutLauncher(s proto.Session) bool {
	return s.Status == proto.StatusOffline && s.Launcher == "" && !s.Assistant && s.SourceKind == "" &&
		(s.Agent == "" || s.Agent == "claude")
}
