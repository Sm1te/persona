package assistant

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The owner's message preempts a background round (docs/assistant/assistant.md "Interjecting while it answers",
// #4926). A round the owner did not start (a supervision message, a scheduled task or follow-up, another
// conversation's message, a task's notice) can sit behind a long foreground command for minutes. When the owner's own
// message reaches a conversation on such a round, it carries interject_background whatever the setting says, so the
// node moves the running command to the background and the model reads the owner at once. For a round the owner
// started, the setting decides as before.
//
// A mark per conversation says which message started the round it is on. It is set when the manager sends a
// background message into a conversation not seen working (call), and it lasts as long as that round: every message
// of the owner's during it is preempting, not only the first, and reading it changes nothing, so a message that fails
// to go out costs nothing. It ends when a session list shows the conversation stopped (roundsSeen), or when another
// message of the manager's starts a round there.
//
// A background message sent into a working conversation joins the round in flight, whose starter stands, unless
// Claude has no tool boundary left to take it in at: then it runs as a turn of its own when that round ends. The
// manager cannot tell which from the list, so the message is remembered (next) and becomes the mark when the round
// is seen to end; the mark is dropped again if the conversation then stays stopped, or if the owner speaks before it
// is seen working (their message starts the round).
//
// The marks are in memory only: after a restart the setting decides until the next background round, which is what
// best effort already means here. Whether a command is running at all is the node's to say
// (assistantProc.interjects: an idle conversation is unaffected), not the manager's list, which can be seconds old.

// backgroundRoundKeep is how long a background round's mark is kept when no session list ever says it stopped.
const backgroundRoundKeep = 24 * time.Hour

// backgroundRoundStartWait is how long a mark waits for a session list to show its round working before it is
// taken for a round that never ran, or ran and ended between two lists.
const backgroundRoundStartWait = time.Minute

// roundNotice is the mark's kind for a task's notice, which carries no message source of its own.
const roundNotice = "notice"

// backgroundRound is one conversation's mark.
type backgroundRound struct {
	kind    string    // the kind of message that started the round it is on; "" while that is no background one (only next is set)
	at      time.Time // when that message was sent, or the round before it was seen to end (queued)
	running bool      // a session list has shown the conversation working since
	queued  bool      // its message was sent into a working conversation, and the round it waited behind has ended
	next    string    // the kind of a background message sent while the conversation was working, "" when none
}

type preemptBook struct {
	mu     sync.Mutex
	rounds map[string]backgroundRound // by conversation id
}

// backgroundRoundKind is the kind of round text with source starts when it is one the owner did not start, else "".
// Persona continuing the owner's own work (a usage limit that reset, a wake for what it waits on) is not one, as it
// is no background round in the apps ("Background rounds").
func backgroundRoundKind(text string, source *proto.MessageSource) string {
	if source == nil {
		if in := prompts.ParseInjected(text); in != nil && in.Kind == proto.InjectedTaskNotice {
			return roundNotice
		}
		return ""
	}
	switch source.Kind {
	case proto.SourceSupervision, proto.SourceConversation, proto.SourcePatrol:
		return source.Kind
	case proto.SourceFollowup:
		if in := prompts.ParseInjected(text); in != nil && (in.State == "limit_continue" || in.State == "wake") {
			return ""
		}
		return source.Kind
	}
	return ""
}

// roundStart is an assistant.send read before it is sent: of kind ("" when it is no background message) to
// conversation id ("" for a new conversation, named by the node's answer), which the last session list showed
// working (joins) or not.
type roundStart struct {
	id, kind string
	joins    bool
}

// roundStarting reads the assistant.send p to machine before it is sent: its conversation is working right after.
func (s *Service) roundStarting(machine string, p map[string]any, kind string) *roundStart {
	id, _ := p["session_id"].(string)
	return &roundStart{id: id, kind: kind, joins: id != "" && s.seenWorking(machine, id)}
}

// sent: the send was accepted with res. A message into a conversation not seen working starts its round, and the
// mark is that round's; a background one into a working conversation is remembered for when that round ends.
func (r *roundStart) sent(s *Service, res json.RawMessage) {
	if r == nil {
		return
	}
	id := r.id
	if id == "" {
		var out struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(res, &out) != nil || out.SessionID == "" {
			return
		}
		id = out.SessionID
	}
	b := &s.preempt
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for k, m := range b.rounds {
		if now.Sub(m.at) > backgroundRoundKeep {
			delete(b.rounds, k)
		}
	}
	if b.rounds == nil {
		b.rounds = map[string]backgroundRound{}
	}
	switch m, marked := b.rounds[id]; {
	case r.joins && r.kind == "":
	case r.joins:
		if !marked {
			m = backgroundRound{at: now, running: true} // the round in flight is not a background one
		}
		m.next = r.kind
		b.rounds[id] = m
	case r.kind == "":
		delete(b.rounds, id)
	default:
		b.rounds[id] = backgroundRound{kind: r.kind, at: now}
	}
}

// roundsSeen takes a node's session list (setSessions): a marked conversation it shows working is on its round, and
// one it shows stopped after that has ended it. A background message that waited behind the ended round may start
// the next one. A mark never seen working is dropped after backgroundRoundStartWait.
func (s *Service) roundsSeen(list []proto.Session) {
	b := &s.preempt
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.rounds) == 0 {
		return
	}
	now := time.Now()
	for i := range list { // by index: a range value copies every (large) session it passes
		id, status := list[i].SessionID, list[i].Status
		m, marked := b.rounds[id]
		if !marked {
			continue
		}
		switch {
		case status == proto.StatusWorking:
			if !m.running {
				m.running = true
				b.rounds[id] = m
			}
		case status != proto.StatusDone && status != proto.StatusIdle && status != proto.StatusInterrupted:
			// offline, unknown: nothing said about its round (a reconnect can come back to the same turn)
		case m.running && m.next != "":
			b.rounds[id] = backgroundRound{kind: m.next, at: now, queued: true}
		case m.running || now.Sub(m.at) > backgroundRoundStartWait:
			delete(b.rounds, id)
		}
	}
}

// ownerPreempts: the owner's message is about to go to conversation id; whether the round there is one the owner did
// not start, so the message carries interject_background. Read only: the mark stands for the owner's later messages
// in the same round, and for this one sent again.
func (s *Service) ownerPreempts(id string) bool {
	b := &s.preempt
	b.mu.Lock()
	m, ok := b.rounds[id]
	b.mu.Unlock()
	if !ok || m.kind == "" || (!m.running && time.Since(m.at) > backgroundRoundStartWait) {
		return false
	}
	s.logf("assistant: the owner's message reaches %s on a round a %s message started: it moves foreground work to the background if a command still runs there (a node or Claude Code too old for it leaves the message for the next tool boundary)", short(id), m.kind)
	return true
}

// ownerSent: the owner's message to conversation id is on its way. A mark whose message waited behind another round
// and has not been seen working since is dropped: the list shows the conversation stopped, so the owner's message
// starts the round.
func (s *Service) ownerSent(id string) {
	b := &s.preempt
	b.mu.Lock()
	defer b.mu.Unlock()
	if m, ok := b.rounds[id]; ok && m.queued && !m.running {
		delete(b.rounds, id)
	}
}

// seenWorking: machine's last session list showed conversation id working.
func (s *Service) seenWorking(machine, id string) bool {
	if s.devs != nil {
		machine = s.devs.Canonical(machine)
	}
	return s.fleetPrevious(machine)[id].Status == proto.StatusWorking
}
