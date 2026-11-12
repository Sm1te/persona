package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// presenceTimes sets how long the owner counts as present and the longest a decision waits, for a test.
func presenceTimes(t *testing.T, present, decision time.Duration) {
	t.Helper()
	oldPresent, oldDecision := ownerPresentFor, relayDecisionMax
	ownerPresentFor, relayDecisionMax = present, decision
	t.Cleanup(func() { ownerPresentFor, relayDecisionMax = oldPresent, oldDecision })
}

// stopHolds stops the timers a test's held queues left.
func stopHolds(t *testing.T, s *Service) {
	t.Helper()
	t.Cleanup(func() {
		b := &s.relays
		b.mu.Lock()
		defer b.mu.Unlock()
		for id, tm := range b.holds {
			tm.Stop()
			delete(b.holds, id)
		}
	})
}

// The ranks: the owner's late answer, what waits on a decision, an outcome to report, then the rest.
func TestRelayRanks(t *testing.T) {
	for _, tc := range []struct {
		msg      relayMsg
		rank     int
		decision bool
	}{
		{relayMsg{Tool: askAnswerTool}, relayRankAnswer, true},
		{relayMsg{Tool: superviseTool, Urgent: true}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "needs"}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "interrupted"}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "failed"}, relayRankDecision, true},
		{relayMsg{Tool: noticeTool, State: "done"}, relayRankReport, false},
		{relayMsg{Tool: superviseTool}, relayRankOther, false},
		{relayMsg{Tool: "conversation_send"}, relayRankOther, false},
		{relayMsg{Tool: spawnNoticeTool}, relayRankOther, false},
		{relayMsg{Tool: taskEndTool}, relayRankOther, false},
	} {
		if got := tc.msg.rank(); got != tc.rank || tc.msg.decisionWaiting() != tc.decision {
			t.Errorf("%s %q urgent=%v: rank %d decision %v, want %d %v", tc.msg.Tool, tc.msg.State, tc.msg.Urgent, got,
				tc.msg.decisionWaiting(), tc.rank, tc.decision)
		}
	}
	// A signal waits on a decision when somebody waits on its answer: not every signal that is told soon (urgent).
	for _, tc := range []struct {
		g    superviseSignal
		want bool
	}{
		{superviseSignal{Kind: sigNeedsYou}, true},
		{superviseSignal{Kind: sigIdle}, true},
		{superviseSignal{Kind: sigInterrupted}, true},
		{superviseSignal{Kind: sigFailed}, true},
		{superviseSignal{Kind: sigOrphaned}, true},
		{superviseSignal{Kind: sigAsk}, true},
		{superviseSignal{Kind: sigReport, Report: upReportQuestion}, true},
		{superviseSignal{Kind: sigReport, Report: upReportBlocked}, true},
		{superviseSignal{Kind: sigReport, Report: upReportDone}, false},
		{superviseSignal{Kind: sigReport, Report: upReportProgress}, false},
		{superviseSignal{Kind: sigStarted}, false},
		{superviseSignal{Kind: sigReview}, false},
		{superviseSignal{Kind: sigStalled}, false},
	} {
		if got := tc.g.waitsOnDecision(); got != tc.want {
			t.Errorf("%s %s waits on a decision: %v, want %v", tc.g.Kind, tc.g.Report, got, tc.want)
		}
	}
	if !anyDecision([]superviseSignal{{Kind: sigReview}, {Kind: sigNeedsYou}}) || anyDecision([]superviseSignal{{Kind: sigStarted}, {Kind: sigStalled}}) {
		t.Fatal("a supervision message waits on a decision exactly when one of its signals does")
	}
}

// queuedRelays is a copy of conversation id's queue.
func queuedRelays(s *Service, id string) []relayMsg {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]relayMsg(nil), b.st.Queue[id]...)
}

// The supervision message itself (deliverDigest): held for the owner in an idle conversation, it waits on a decision
// exactly when one of its signals does, also once a later signal merged into the one still queued, and its
// relayDecisionMax then counts from that merge.
func TestSupervisionMessageWaitsOnADecisionByItsSignals(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"one", "two"} {
		todo, err := s.createTodo(ctx, title, "do "+title, "conv1", "")
		if err != nil {
			t.Fatal(err)
		}
		s.superviseTodo(ctx, todo.ID, "conv1", []string{"merged"}, 0)
		ids = append(ids, todo.ID)
	}
	if _, _, err := s.updateSupervision(ctx, ids[0], func(v *Supervision) bool { v.State = reviewPending; return true }); err != nil {
		t.Fatal(err)
	}
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.signal(superviseSignal{Todo: ids[0], Kind: sigReview, Key: "1", Detail: "looks finished"})
	waitFor(t, "the supervision message to queue", func() bool { return s.queuedFor("conv1") == 1 })
	first := queuedRelays(s, "conv1")[0]
	if first.Tool != superviseTool || first.Urgent || first.rank() != relayRankOther || f.sentTo("conv1") != 0 {
		t.Fatalf("a review alone: %+v, %d sent", first, f.sentTo("conv1"))
	}
	time.Sleep(5 * time.Millisecond)
	s.signal(superviseSignal{Todo: ids[1], Kind: sigFailed, Key: "x", Detail: "its computer is offline"})
	waitFor(t, "the failed start to merge in", func() bool {
		q := queuedRelays(s, "conv1")
		return len(q) == 1 && q[0].Urgent
	})
	merged := queuedRelays(s, "conv1")[0]
	if merged.rank() != relayRankDecision || !merged.decisionSince().After(first.QueuedAt) || !merged.QueuedAt.Equal(first.QueuedAt) || f.sentTo("conv1") != 0 {
		t.Fatalf("merged: rank %d, waits on its decision since %s, queued %s (the first %s), %d sent", merged.rank(), merged.decisionSince(), merged.QueuedAt, first.QueuedAt, f.sentTo("conv1"))
	}
}

// A supervision message with a failed start goes straight to the queue as one that waits on a decision.
func TestSupervisionMessageWithADecisionIsQueuedUrgent(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	todo, err := s.createTodo(ctx, "one", "do one", "conv1", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, todo.ID, "conv1", []string{"merged"}, 0)
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.signal(superviseSignal{Todo: todo.ID, Kind: sigFailed, Key: "x", Detail: "its computer is offline"})
	waitFor(t, "the supervision message to queue", func() bool { return s.queuedFor("conv1") == 1 })
	if q := queuedRelays(s, "conv1"); !q[0].Urgent || q[0].rank() != relayRankDecision || f.sentTo("conv1") != 0 {
		t.Fatalf("queued: %+v, %d sent", q[0], f.sentTo("conv1"))
	}
}

// conversation_send to an idle conversation the owner is talking in: queued, the sender is told why, and it goes in
// when the hold ends.
func TestConversationSendWaitsForTheOwner(t *testing.T) {
	presenceTimes(t, 300*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	f.known["conv2"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "conv2", MachineID: "server", Assistant: true, Status: proto.StatusWorking, Title: "Research"})
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	out, err := s.toolConversationSend(ctx, "conv2", json.RawMessage(`{"id":"conv1","text":"the numbers are in"}`))
	if err != nil || !strings.Contains(out, "the user is talking in it") || !strings.Contains(out, "queued (1 waiting)") {
		t.Fatalf("result %q, %v", out, err)
	}
	if q, sent := s.queuedFor("conv1"), f.sentTo("conv1"); q != 1 || sent != 0 {
		t.Fatalf("while the owner talks: %d queued, %d sent", q, sent)
	}
	waitFor(t, "the message once the owner is quiet", func() bool { return f.sentTo("conv1") == 1 && s.queuedFor("conv1") == 0 })
	if n := f.notices()[0]; !strings.Contains(n, "the numbers are in") {
		t.Fatalf("delivered:\n%s", n)
	}
	// The owner is quiet: the next one goes straight in.
	out, err = s.toolConversationSend(ctx, "conv2", json.RawMessage(`{"id":"conv1","text":"one more"}`))
	if err != nil || strings.Contains(out, "queued") || f.sentTo("conv1") != 2 {
		t.Fatalf("after the hold: %q, %v, %d sent", out, err, f.sentTo("conv1"))
	}
}

// A message that could not go in (its delivery failed) holds up nothing but itself: the next drain takes another one
// first, whatever its rank, and the one after tries it again.
func TestAMessageThatCannotGoDoesNotBlockTheQueue(t *testing.T) {
	s, f := newQuietService(t)
	idle := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}
	at := time.Now().UTC()
	s.queueRelay("conv1", relayMsg{From: "t1", Tool: noticeTool, State: "done", Text: "report-1", At: at})
	s.queueRelay("conv1", relayMsg{From: "t2", Tool: noticeTool, State: "failed", Text: "decision-1", At: at})
	draining := func() bool {
		s.relays.mu.Lock()
		defer s.relays.mu.Unlock()
		return s.relays.draining["conv1"]
	}
	f.mu.Lock()
	f.failAssistant = errors.New("the launcher did not start")
	f.mu.Unlock()
	s.drainRelays("server", []proto.Session{idle})
	waitFor(t, "the failed delivery", func() bool {
		s.relays.mu.Lock()
		defer s.relays.mu.Unlock()
		return s.relays.stuck["conv1"].Text == "decision-1"
	})
	f.mu.Lock()
	f.failAssistant = nil
	f.mu.Unlock()
	if s.queuedFor("conv1") != 2 || draining() {
		t.Fatalf("after the failure: %d queued", s.queuedFor("conv1"))
	}
	for i := range 2 {
		s.drainRelays("server", []proto.Session{idle})
		waitFor(t, "the next message", func() bool { return f.sentTo("conv1") == i+1 && !draining() })
	}
	if n := f.notices(); !strings.Contains(n[0], "report-1") || !strings.Contains(n[1], "decision-1") || s.queuedFor("conv1") != 0 {
		t.Fatalf("went in as %q, %d left", n, s.queuedFor("conv1"))
	}
}

// A follow-up or scheduled task that runs in a conversation the owner is talking in waits as well: it stays due,
// nothing of it is consumed or recorded, and it runs when the hold ends. "Run now" and a task's first daily run are
// not held, and no run waits more than relayDecisionMax past its time.
func TestScheduledRunWaitsForTheOwner(t *testing.T) {
	presenceTimes(t, 300*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	ctx := context.Background()
	due := time.Now().Add(-time.Second)
	setTasks(s, Task{ID: "fu_1", Title: "look again", Prompt: "look at the build again", Repeat: repeatOnce, At: due, Enabled: true,
		MachineID: "server", SessionID: "conv1"})
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	now := time.Now()
	s.runDue(ctx, now)
	s.sched.inflight.Wait()
	if task, _ := taskByID(s, "fu_1"); !task.Enabled || !task.LastRun.IsZero() || f.sentTo("conv1") != 0 {
		t.Fatalf("while the owner talks: %+v, %d sent", task, f.sentTo("conv1"))
	}
	if next := s.nextDue(ctx, now); !next.After(now) || next.After(now.Add(300*time.Millisecond)) {
		t.Fatalf("looked at again %s after now, want when the hold ends", next.Sub(now))
	}
	time.Sleep(320 * time.Millisecond)
	s.runDue(ctx, time.Now())
	s.sched.inflight.Wait()
	if task, _ := taskByID(s, "fu_1"); task.Enabled || f.sentTo("conv1") != 1 {
		t.Fatalf("after the hold: %+v, %d sent", task, f.sentTo("conv1"))
	}

	// The hold itself, at the real times.
	presenceTimes(t, 2*time.Minute, 5*time.Minute)
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	present := map[string]time.Time{"conv1": at.Add(-30 * time.Second), "own1": at.Add(-30 * time.Second)}
	for _, tc := range []struct {
		name string
		t    Task
		due  time.Time
		want time.Duration
	}{
		{"into the conversation the owner talks in", Task{SessionID: "conv1"}, at, 90 * time.Second},
		{"into its own conversation the owner talks in", Task{RunSessionID: "own1"}, at, 90 * time.Second},
		{"due 4 minutes ago: one more", Task{SessionID: "conv1"}, at.Add(-4 * time.Minute), time.Minute},
		{"due 5 minutes ago: it runs", Task{SessionID: "conv1"}, at.Add(-5 * time.Minute), 0},
		{"into another conversation", Task{SessionID: "conv9"}, at, 0},
		{"into a new conversation", Task{}, at, 0},
	} {
		if got := runHold(present, tc.t, tc.due, at); got != tc.want {
			t.Errorf("%s: held %s, want %s", tc.name, got, tc.want)
		}
	}
}

// The queue goes out by rank, the one that queued first within a rank, whatever order the messages came in.
func TestQueuedRelaysGoOutByRank(t *testing.T) {
	s, f := newQuietService(t)
	busy := proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusWorking}
	f.setSessions("server", busy)
	at := time.Now().UTC()
	for _, m := range []relayMsg{
		{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "other-1"},
		{From: "t1", Tool: noticeTool, State: "done", Text: "report-1"},
		{From: superviseTool, Tool: superviseTool, Text: "supervise-plain"},
		{From: "t2", Tool: noticeTool, State: "done", Text: "report-2"},
		{From: "t3", Tool: noticeTool, State: "failed", Text: "decision-notice"},
		{From: "t4", Tool: askAnswerTool, Text: "answer-1"},
		{From: superviseTool, Tool: superviseTool, Urgent: true, Text: "never queued twice"},
	} {
		if m.Tool == superviseTool && m.Urgent {
			continue // one supervision message per conversation waits (replaceQueuedSupervise)
		}
		m.At = at
		if s.queueRelay("conv1", m) == 0 {
			t.Fatal("not queued")
		}
	}
	idle := busy
	idle.Status = proto.StatusIdle
	want := []string{"answer-1", "decision-notice", "report-1", "report-2", "other-1", "supervise-plain"}
	for i := range want {
		s.drainRelays("server", []proto.Session{idle})
		waitFor(t, "the next queued message", func() bool { return f.sentTo("conv1") == i+1 })
	}
	for i, text := range f.notices() {
		if !strings.Contains(text, want[i]) {
			t.Fatalf("message %d is %q, want %q\nall: %q", i, text, want[i], f.notices())
		}
	}
	if n := s.queuedFor("conv1"); n != 0 {
		t.Fatalf("%d left queued", n)
	}
}

// Within ownerPresentFor of the owner's message nothing from the system goes into the conversation, idle as it is;
// when the hold ends it goes in on its own, with no change of the machine's list.
func TestSystemMessagesWaitWhileTheOwnerTalks(t *testing.T) {
	presenceTimes(t, 300*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "from another chat", At: time.Now().UTC()})
	if q, sent := s.queuedFor("conv1"), f.sentTo("conv1"); q != 1 || sent != 0 {
		t.Fatalf("while the owner talks: %d queued, %d sent", q, sent)
	}
	s.drainRelays("server", []proto.Session{{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}})
	time.Sleep(50 * time.Millisecond)
	if sent := f.sentTo("conv1"); sent != 0 {
		t.Fatalf("a change of the list delivered %d held message(s)", sent)
	}
	waitFor(t, "the held message once the owner is quiet", func() bool { return f.sentTo("conv1") == 1 && s.queuedFor("conv1") == 0 })
	// The owner is quiet: the next one goes straight in.
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "again", At: time.Now().UTC()})
	if sent := f.sentTo("conv1"); sent != 2 {
		t.Fatalf("after the hold: %d sent", sent)
	}
}

// A task's message that would have gone straight into the idle main conversation waits for the owner as well: it is
// queued, shown waiting, and goes in when the hold ends.
func TestTaskMessageToAnIdleConversationWaitsForTheOwner(t *testing.T) {
	presenceTimes(t, 400*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	f.judge = `{"verdict":"done","reason":"done"}`
	startedTask(t, s, f)
	s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "conv1"})
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "finished the work", 0)
	waitFor(t, "the message to queue", func() bool { return s.queuedFor("conv1") == 1 })
	if n := f.notices(); len(n) != 0 {
		t.Fatalf("sent while the owner talks: %q", n)
	}
	waitFor(t, "the message once the owner is quiet", func() bool { return len(f.notices()) == 1 && s.queuedFor("conv1") == 0 })
	if n := f.notices()[0]; !strings.Contains(n, "finished the work") {
		t.Fatalf("message:\n%s", n)
	}
}

// A conversation the owner did not speak in is not held: only the one they talk in.
func TestPresenceHoldsOnlyTheConversationSpokenIn(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	f.known["lead1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lead1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "lead1"})
	msg := relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "hello", At: time.Now().UTC()}
	s.relayOrQueue(ctx, "conv1", msg)
	s.relayOrQueue(ctx, "lead1", msg)
	if a, b := f.sentTo("conv1"), f.sentTo("lead1"); a != 1 || b != 0 {
		t.Fatalf("sent %d to the main conversation and %d to the leader the owner talks in", a, b)
	}
}

// What waits on a decision is held relayDecisionMax at most, counted from when it queued, while the owner keeps
// talking; the rest keeps waiting.
func TestDecisionWaitingMessagesAreHeldAtMost(t *testing.T) {
	presenceTimes(t, 2*time.Minute, 5*time.Minute)
	b := &relayBook{st: relayState{Queue: map[string][]relayMsg{}}}
	now := time.Now()
	b.ownerSpokeLocked("conv1", now.Add(-30*time.Second))
	for _, tc := range []struct {
		name string
		msg  relayMsg
		want time.Duration
	}{
		{"an ordinary message waits out the owner", relayMsg{Tool: "conversation_send", At: now.Add(-10 * time.Minute)}, 90 * time.Second},
		{"a fresh decision waits as long", relayMsg{Tool: superviseTool, Urgent: true, At: now}, 90 * time.Second},
		{"a decision that waited 4 minutes has one more", relayMsg{Tool: superviseTool, Urgent: true, At: now.Add(-4 * time.Minute)}, time.Minute},
		{"a decision that waited 5 minutes goes", relayMsg{Tool: askAnswerTool, At: now.Add(-5 * time.Minute)}, 0},
		{"a decision that does not say when it queued goes", relayMsg{Tool: askAnswerTool}, 0},
		// Since #5006 a queued message says when it queued (queued_at): the 5 minutes count from then, not from when
		// the message was written.
		{"a decision written long ago that queued a minute ago", relayMsg{Tool: askAnswerTool, At: now.Add(-time.Hour), QueuedAt: now.Add(-time.Minute)}, 90 * time.Second},
		{"a decision that queued 4 minutes ago has one more", relayMsg{Tool: superviseTool, Urgent: true, At: now, QueuedAt: now.Add(-4 * time.Minute)}, time.Minute},
		{"a decision that queued 5 minutes ago goes", relayMsg{Tool: askAnswerTool, At: now, QueuedAt: now.Add(-5 * time.Minute)}, 0},
		{"a message that began to wait on a decision a minute ago", relayMsg{Tool: noticeTool, State: "needs", QueuedAt: now.Add(-10 * time.Minute), DecisionAt: now.Add(-time.Minute)}, 90 * time.Second},
	} {
		if got := b.holdLocked("conv1", tc.msg, now); got != tc.want {
			t.Errorf("%s: held %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := b.holdLocked("other", relayMsg{Tool: "conversation_send", At: now}, now); got != 0 {
		t.Fatalf("a conversation the owner did not speak in is held %s", got)
	}
	// The pick skips what is held and says how long until the first may go.
	b.st.Queue["conv1"] = []relayMsg{
		{Tool: "conversation_send", Text: "other", At: now.Add(-10 * time.Minute)},
		{Tool: superviseTool, Urgent: true, Text: "soon", At: now.Add(-4 * time.Minute)},
	}
	if i, wait := b.pickLocked("conv1", now); i != -1 || wait != time.Minute {
		t.Fatalf("pick %d, wait %s", i, wait)
	}
	if i, wait := b.pickLocked("conv1", now.Add(time.Minute)); i != 1 || wait != 0 {
		t.Fatalf("after the decision's hold: pick %d, wait %s", i, wait)
	}
	b.frontLocked("conv1", 1)
	if q := b.st.Queue["conv1"]; q[0].Text != "soon" || q[1].Text != "other" {
		t.Fatalf("the picked message is not at the head: %+v", q)
	}
}

// End to end: the owner keeps talking, a decision that has waited its time goes in and the ordinary message does not.
func TestDecisionGoesInWhileTheOwnerKeepsTalking(t *testing.T) {
	presenceTimes(t, time.Hour, 150*time.Millisecond)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "ordinary", At: time.Now().UTC()})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "t1", Tool: askAnswerTool, Text: "the late answer", At: time.Now().UTC()})
	if q, sent := s.queuedFor("conv1"), f.sentTo("conv1"); q != 2 || sent != 0 {
		t.Fatalf("at first: %d queued, %d sent", q, sent)
	}
	waitFor(t, "the decision after its longest hold", func() bool { return f.sentTo("conv1") == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := f.notices(); len(n) != 1 || !strings.Contains(n[0], "the late answer") || s.queuedFor("conv1") != 1 {
		t.Fatalf("sent %q, %d still queued", n, s.queuedFor("conv1"))
	}
}

// A supervision message or task notice that takes a queued one's place has waited since the first queued, for its
// hold and for its queue wait alike; one that only now waits on a decision counts its hold from now, and its queue
// wait still from when the first queued.
func TestReplacedQueuedMessageKeepsItsWait(t *testing.T) {
	presenceTimes(t, 2*time.Minute, 5*time.Minute)
	s, _ := newQuietService(t)
	first := time.Now().Add(-4 * time.Minute).UTC()
	s.queueRelay("conv1", relayMsg{From: superviseTool, Tool: superviseTool, Urgent: true, Text: "one"})
	s.queueRelay("conv1", relayMsg{From: "t1", Tool: noticeTool, State: "needs", Text: "needs"})
	s.queueRelay("conv1", relayMsg{From: "t2", Tool: noticeTool, State: "done", Text: "done"})
	s.relays.mu.Lock()
	for i := range s.relays.st.Queue["conv1"] {
		s.relays.st.Queue["conv1"][i].QueuedAt = first // they queued 4 minutes ago
	}
	s.relays.mu.Unlock()
	before := time.Now()
	if !s.replaceQueuedSupervise("conv1", relayMsg{From: superviseTool, Tool: superviseTool, Urgent: true, Text: "both", At: before.UTC()}) {
		t.Fatal("not replaced")
	}
	if !s.replaceQueuedNotice("conv1", relayMsg{From: "t1", Tool: noticeTool, State: "failed", Text: "failed", At: before.UTC()}) {
		t.Fatal("notice not replaced")
	}
	if !s.replaceQueuedNotice("conv1", relayMsg{From: "t2", Tool: noticeTool, State: "needs", Text: "now needs", At: before.UTC()}) {
		t.Fatal("second notice not replaced")
	}
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	q := b.st.Queue["conv1"]
	if len(q) != 3 || q[0].Text != "both" || !q[0].Urgent || q[1].Text != "failed" || q[2].Text != "now needs" {
		t.Fatalf("queue: %+v", q)
	}
	for i, m := range q {
		if !m.QueuedAt.Equal(first) || m.Depth != i+1 {
			t.Fatalf("message %d lost when the first queued (its queue wait): %+v", i, m)
		}
	}
	if !q[0].decisionSince().Equal(first) || !q[1].decisionSince().Equal(first) {
		t.Fatalf("a decision that replaced a decision counts from %s and %s, want %s", q[0].decisionSince(), q[1].decisionSince(), first)
	}
	if since := q[2].decisionSince(); since.Before(before.Add(-time.Second)) || since.After(time.Now()) {
		t.Fatalf("a message that only now waits on a decision counts from %s, want now", since)
	}
	// The owner keeps talking: the two that waited 4 minutes have one more, the new decision its full hold.
	now := time.Now()
	b.ownerSpokeLocked("conv1", now)
	if h := b.holdLocked("conv1", q[1], now); h <= 0 || h > time.Minute {
		t.Fatalf("the replaced decision is held %s, want at most a minute", h)
	}
	if h := b.holdLocked("conv1", q[2], now); h != 2*time.Minute {
		t.Fatalf("the new decision is held %s, want the owner's 2 minutes", h)
	}
	// A notice that waits on nothing any more takes its place: no decision time is left behind.
	if got := (relayMsg{Tool: noticeTool, State: "done"}).replacing(q[2], now); !got.DecisionAt.IsZero() || !got.QueuedAt.Equal(first) {
		t.Fatalf("an outcome that replaced a decision: %+v", got)
	}
}

// The queue wait (#4924) of a message held for the owner in an idle conversation is the whole hold: from when it
// queued to when it went in.
func TestQueueWaitOfAHeldMessage(t *testing.T) {
	presenceTimes(t, 300*time.Millisecond, time.Hour)
	s, f := newQuietService(t)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	queued := time.Now()
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "held", At: queued.Add(-time.Hour).UTC()})
	if q, sent := s.queuedFor("conv1"), f.sentTo("conv1"); q != 1 || sent != 0 {
		t.Fatalf("while the owner talks: %d queued, %d sent", q, sent)
	}
	if len(availEvents(s, queueWaitEvent)) != 0 {
		t.Fatal("a queue wait recorded before the message went in")
	}
	waitFor(t, "the held message's queue wait", func() bool { return len(availEvents(s, queueWaitEvent)) == 1 })
	longest := time.Since(queued).Seconds()
	e := availEvents(s, queueWaitEvent)[0]
	// From the owner's message to the end of the hold it waited ownerPresentFor, less what passed before it queued.
	if wait, _ := e["wait"].(float64); wait < 0.2 || wait > longest || e["kind"] != proto.SourceConversation || e["depth"] != 1 {
		t.Fatalf("queue wait %v, want between 0.2 and %.2f seconds", e, longest)
	}
	// One that goes straight in once the owner is quiet waited in no queue.
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "straight in", At: time.Now().UTC()})
	if n := len(availEvents(s, queueWaitEvent)); f.sentTo("conv1") != 2 || n != 1 {
		t.Fatalf("%d sent, %d queue waits", f.sentTo("conv1"), n)
	}
}

// A message that goes in ahead of earlier ones by its rank records its own queue wait and the queue's length when
// it was added; those it passed record theirs when they go in, the longer for it.
func TestQueueWaitOfReRankedMessages(t *testing.T) {
	s, f := newQuietService(t)
	s.queueRelay("conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "ordinary"})
	s.queueRelay("conv1", relayMsg{From: "t1", Tool: noticeTool, State: "done", Text: "report"})
	s.queueRelay("conv1", relayMsg{From: "t4", Tool: askAnswerTool, Text: "answer"})
	now := time.Now().UTC()
	s.relays.mu.Lock()
	for i, ago := range []time.Duration{30 * time.Minute, 20 * time.Minute, 10 * time.Minute} {
		s.relays.st.Queue["conv1"][i].QueuedAt = now.Add(-ago)
	}
	s.relays.mu.Unlock()
	idle := []proto.Session{{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle}}
	for i := range 3 {
		s.drainRelays("server", idle)
		waitFor(t, "the next queue wait", func() bool { return len(availEvents(s, queueWaitEvent)) == i+1 })
	}
	if n := f.notices(); len(n) != 3 || !strings.Contains(n[0], "answer") || !strings.Contains(n[1], "report") || !strings.Contains(n[2], "ordinary") {
		t.Fatalf("went in as %q", n)
	}
	for i, want := range []struct {
		kind  string
		depth int
		wait  float64
	}{{roundNotice, 3, 600}, {roundNotice, 2, 1200}, {proto.SourceConversation, 1, 1800}} {
		e := availEvents(s, queueWaitEvent)[i]
		if wait, _ := e["wait"].(float64); e["kind"] != want.kind || e["depth"] != want.depth || wait < want.wait || wait > want.wait+30 {
			t.Fatalf("queue wait %d is %v, want %+v", i, e, want)
		}
	}
}

// A manager that restarts with messages held for the owner has no timer for them: the supervision tick hands them on
// once the hold ended, and not before.
func TestHeldRelaysSurviveARestart(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	st := &memStore{}
	s, f := newQuietServiceOn(t, st)
	stopHolds(t, s)
	ctx := context.Background()
	s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "conv1"})
	s.relayOrQueue(ctx, "conv1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "held", At: time.Now().UTC()})
	if s.queuedFor("conv1") != 1 {
		t.Fatal("not held")
	}
	restarted := withSettings(t, f, New(Options{Hub: f, Store: func() Store { return st }, Logf: testLogf(t)}))
	setSettings(t, restarted, func(v *Settings) { v.Main = "conv1" })
	stopHolds(t, restarted)
	if restarted.queuedFor("conv1") != 1 || restarted.relays.st.Spoke["conv1"].IsZero() {
		t.Fatalf("restored: %d queued, spoke %v", restarted.queuedFor("conv1"), restarted.relays.st.Spoke)
	}
	restarted.drainHeldRelays(ctx, time.Now())
	time.Sleep(50 * time.Millisecond)
	if sent := f.sentTo("conv1"); sent != 0 {
		t.Fatalf("%d sent while the owner still counts as present", sent)
	}
	ownerPresentFor = 0 // the hold is over
	restarted.drainHeldRelays(ctx, time.Now())
	waitFor(t, "the held message after the restart", func() bool { return f.sentTo("conv1") == 1 && restarted.queuedFor("conv1") == 0 })
	restarted.relays.mu.Lock()
	_, kept := restarted.relays.st.Spoke["conv1"]
	restarted.relays.mu.Unlock()
	if kept {
		t.Fatal("the owner's last message is kept after its hold ended and its queue was handed on")
	}
}

// The stored shape: what the last version wrote reads as before, a message without the new fields writes none of
// them, and the last version (which does not know them) reads what this one writes.
func TestRelayStateReadsAcrossVersions(t *testing.T) {
	old := `{"queue":{"conv1":[{"from":"c2","from_title":"Research","tool":"conversation_send","text":"hi","hop":1,"at":"2026-11-01T00:00:00Z"}]},"hops":{"conv1":{"hop":1,"at":"2026-11-01T00:00:00Z"}}}`
	var rs relayState
	if err := json.Unmarshal([]byte(old), &rs); err != nil {
		t.Fatal(err)
	}
	if q := rs.Queue["conv1"]; len(q) != 1 || q[0].Text != "hi" || q[0].Urgent || rs.Spoke != nil {
		t.Fatalf("read: %+v", rs)
	}
	out, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "urgent") || strings.Contains(string(out), "spoke") || strings.Contains(string(out), "decision_at") {
		t.Fatalf("new fields written for a queue that has none: %s", out)
	}
	rs.Queue["conv1"][0].Urgent = true
	rs.Spoke = map[string]time.Time{"conv1": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	out, err = json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	// The last version's shape: no urgent, no spoke; unknown fields are skipped.
	var prev struct {
		Queue map[string][]struct {
			From, Tool, Text string
			At               time.Time
		} `json:"queue"`
		Hops map[string]relayHop `json:"hops"`
	}
	if err := json.Unmarshal(out, &prev); err != nil {
		t.Fatal(err)
	}
	if q := prev.Queue["conv1"]; len(q) != 1 || q[0].Text != "hi" || q[0].Tool != "conversation_send" || prev.Hops["conv1"].Hop != 1 {
		t.Fatalf("the last version reads: %+v", prev)
	}
}
