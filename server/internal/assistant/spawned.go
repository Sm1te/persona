package assistant

import (
	"cmp"
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Failed assistant turns and the conversations conversation_new opens (#4669, docs/assistant/assistant.md "Managing
// its own conversations"). An assistant turn that failed (the node's turn error: is_error, or an answer that is only an
// auth or upstream error) is logged with its conversation and launcher, never its words, which may carry a credential.
// A conversation conversation_new opened is followed until it reports back with conversation_send: a turn of it that
// fails, or one that ends without it ever having reported, leaves one notice in the conversation that opened it
// (OriginRef), under the relay's hop and rate limits as a conversation_send from it would be.

const (
	// spawnNoticeTool is the relay kind of a spawned conversation's notice to the one that opened it.
	spawnNoticeTool = "spawn_notice"
	// taskEndTool is the relay kind of a task's end told to the conversation that planned it (taskEndedNotice).
	taskEndTool = "task_end"
	// spawnedKeep is how long a spawned conversation is followed: one that has not ended a turn by then is the
	// user's own conversation by now.
	spawnedKeep = 7 * 24 * time.Hour
)

// spawnedConv is a conversation conversation_new opened: by which conversation and when; whether it reported back
// with conversation_send, and whether its parent was told it did not (or that it failed).
type spawnedConv struct {
	Parent   string    `json:"parent"`
	At       time.Time `json:"at"`
	Reported bool      `json:"reported,omitempty"`
	Noticed  bool      `json:"noticed,omitempty"`
}

// spawnReported: conversation from sent a message to to; when from is a conversation to opened, it reported back.
func (s *Service) spawnReported(from, to string) {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return
	}
	c, ok := b.st.Spawned[from]
	if !ok || c.Parent != to || c.Reported {
		return
	}
	c.Reported = true
	b.st.Spawned[from] = c
	s.saveRelayLocked()
}

// assistantTurnSettled: a turn of machine's session sid ended and stayed still. For an assistant conversation, a
// failed turn is logged, and a spawned conversation's parent is told when it should be (spawnedTurnSettled).
func (s *Service) assistantTurnSettled(ctx context.Context, machine, sid string) {
	sess, ok := s.decoratedSession(machine, sid)
	if !ok { // not in the node's held list (yet): as its machine lists it
		sess, ok = s.session(ctx, machine, sid)
	}
	if !ok || !sess.Assistant || sess.Status == proto.StatusWorking || sess.Starting {
		return
	}
	// The node's word, else its last words when they are the error (an older node, fleet.go).
	sess.TurnError, sess.TurnErrorKind = latestTurnError(sess)
	failed := strings.TrimSpace(sess.TurnError) != ""
	if failed {
		// The conversation and launcher, and what kind of error: never its words, which may quote a credential.
		s.logf("assistant %s: turn failed (launcher %s, kind %s, code %s)", sid, cmp.Or(sess.Launcher, "claude"),
			cmp.Or(sess.TurnErrorKind, "-"), cmp.Or(sess.TurnErrorCode, turnErrorCode(sess.TurnError), "-"))
	}
	s.spawnedTurnSettled(ctx, sess, failed)
}

// spawnedTurnSettled tells the conversation that opened sess (conversation_new) once that sess failed, or that it
// ended a turn without ever reporting back. A turn with more to come (a message queued for it, background work that
// wakes it) is not the end unless it failed. The notice is a message from sess, so it is held to the relay's hop and
// rate limits as sess's own conversation_send would be: past them the parent is not told, and the log says so.
func (s *Service) spawnedTurnSettled(ctx context.Context, sess proto.Session, failed bool) {
	sid := sess.SessionID
	if !failed && (s.queuedFor(sid) > 0 || backgroundRunning(sess)) {
		return
	}
	b := &s.relays
	b.mu.Lock()
	if !s.relayLocked(ctx) {
		b.mu.Unlock()
		return
	}
	c, ok := b.st.Spawned[sid]
	if !ok || c.Noticed || !failed && c.Reported {
		b.mu.Unlock()
		return
	}
	c.Noticed = true // one notice, whatever comes of it
	b.st.Spawned[sid] = c
	s.saveRelayLocked()
	b.mu.Unlock()
	name, hop, err := s.relayFrom(ctx, sid)
	if err == nil {
		err = s.relayAllowed(sid, c.Parent, hop, time.Now())
	}
	if err != nil {
		s.logf("spawned conversation %s: %s not told: %v", short(sid), short(c.Parent), err)
		return
	}
	n := prompts.SpawnNotice{ID: sid, Title: name, Link: sessionLink(s.webBase(ctx), sess.MachineID, sid, true), Failed: failed}
	if failed {
		n.Error = clip(oneLine(sess.TurnError), 300)
	} else {
		n.Last = clip(oneLine(sess.LastText), 300)
	}
	msg := relayMsg{From: sid, FromTitle: name, Tool: spawnNoticeTool, Text: prompts.RenderSpawnNotice(n), Hop: hop, At: time.Now().UTC()}
	what := "ended without reporting back"
	if failed {
		what = "failed"
	}
	s.logf("spawned conversation %s %s: telling %s", short(sid), what, short(c.Parent))
	s.relayOrQueue(ctx, c.Parent, msg)
}

// relayOrQueue puts msg into assistant conversation to: now, or queued behind its turn (drainRelays sends it then).
func (s *Service) relayOrQueue(ctx context.Context, to string, msg relayMsg) {
	machine := s.settings(ctx).MachineID
	c, ok := s.session(ctx, machine, to)
	if !ok || c.Status == proto.StatusWorking || c.Starting || s.relayWaits(to, msg) {
		if s.queueRelay(to, msg) > 0 {
			s.publish(ctx) // shown waiting above its composer
			if ok && c.Status != proto.StatusWorking && !c.Starting {
				s.drainRelays(machine, []proto.Session{c})
			}
		}
		return
	}
	if err := s.deliverRelay(ctx, machine, to, msg); err != nil {
		s.logf("relay %s to %s: %v; queued", msg.Tool, short(to), err)
		if s.queueRelay(to, msg) > 0 {
			s.publish(ctx)
		}
	}
}

// taskEndedNotice tells the conversation that planned task t, when the main conversation supervised it for that
// conversation (a side conversation's task_plan, #4669), that t ended: accepted or cancelled, with its link. The main
// conversation reviewed it; this is the planning conversation's record, so it is no hop and is not pushed.
func (s *Service) taskEndedNotice(ctx context.Context, t Todo, accepted bool, note string) {
	v := t.Supervision
	if t.Origin == "" || v == nil || v.Supervisor == "" || v.Supervisor == t.Origin || s.isMain(ctx, t.Origin) {
		return
	}
	verdict := reviewCancelled
	if accepted {
		verdict = reviewAccepted
	}
	text := prompts.RenderTaskEnded(prompts.TaskEnded{ID: t.ID, Title: t.Title, Link: todoLink(s.webBase(ctx), t.ID), Verdict: verdict,
		Note: clip(oneLine(note), 200)})
	s.relayOrQueue(ctx, t.Origin, relayMsg{From: t.ID, FromTitle: "任务「" + t.Title + "」", Tool: taskEndTool, Text: text, At: time.Now().UTC()}) // i18n-ignore: the queue's label, as the task notices'
}
