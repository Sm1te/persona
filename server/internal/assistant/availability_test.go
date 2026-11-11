package assistant

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// availEvents are the details of the availability events of kind the service recorded, oldest first.
func availEvents(s *Service, kind string) []map[string]any {
	var out []map[string]any
	for _, e := range testEvents(s) {
		if e.Kind == kind {
			out = append(out, e.Detail)
		}
	}
	return out
}

// The owner's message to an idle conversation: the wait runs to the first change of the conversation's last text,
// and the round it started is the owner's. A supervision round after it is a background round; a message that comes
// during it is counted as having come on a background round, and is answered by the text that follows once nothing
// of the owner's is queued any more.
func TestAvailabilityReplyWaitAndRounds(t *testing.T) {
	ft := newFleetTest(t)
	ft.f.sessions["mac"], ft.f.known["conv"] = nil, true // the fake hub answers the sends to it
	s := ft.s
	m, _ := s.fleetMachine("mac")
	conv := proto.Session{SessionID: "conv", Agent: "claude", Cwd: m.assistantCwd(), Status: proto.StatusIdle, LastText: "earlier"}
	ref := SessionRef{MachineID: "mac", SessionID: "conv"}
	list := func(status, text string, queued ...string) {
		c := conv
		c.Status, c.LastText, c.Queued = status, text, queued
		ft.sessions("mac", c)
	}
	list(proto.StatusIdle, "earlier")

	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "earlier")
	if got := availEvents(s, replyWaitEvent); len(got) != 0 {
		t.Fatalf("a reply before any text: %v", got)
	}
	list(proto.StatusWorking, "Looking.")
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 1 || waits[0]["state"] != waitIdle || waits[0]["unanswered"] != nil {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusWorking, "Done.") // more text of the same reply: not another wait
	list(proto.StatusDone, "Done.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 1 })
	if r := availEvents(s, roundEvent)[0]; r["source"] != roundOwner {
		t.Fatalf("round %v", r)
	}
	if got := availEvents(s, replyWaitEvent); len(got) != 1 {
		t.Fatalf("the round's end counted the answered message again: %v", got)
	}

	// A supervision message starts the next round; the owner writes while it runs.
	if err := s.deliverRelay(t.Context(), "mac", "conv", relayMsg{Tool: superviseTool, Text: "Synthetic supervision."}); err != nil {
		t.Fatal(err)
	}
	list(proto.StatusWorking, "Done.")
	waitUntil(t, func() bool {
		s.avail.mu.Lock()
		defer s.avail.mu.Unlock()
		return s.avail.rounds[[2]string{"mac", "conv"}].source == proto.SourceSupervision
	})
	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "Checking the PR.", "my question") // the round's own text: the owner's message still waits
	if got := availEvents(s, replyWaitEvent); len(got) != 1 {
		t.Fatalf("the background round's text counted as the reply: %v", got)
	}
	list(proto.StatusWorking, "Here is the answer.")
	waits = availEvents(s, replyWaitEvent)
	if len(waits) != 2 || waits[1]["state"] != waitBackground {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusDone, "Here is the answer.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 2 })
	if r := availEvents(s, roundEvent)[1]; r["source"] != proto.SourceSupervision {
		t.Fatalf("round %v", r)
	}
}

// A reply whose text the session list never showed changing (the same words as before) is counted when its round
// ends; a message nobody answers is recorded unanswered at the bound instead of waiting for ever.
func TestAvailabilityReplyWaitFallbacks(t *testing.T) {
	ft := newFleetTest(t)
	s := ft.s
	m, _ := s.fleetMachine("mac")
	conv := proto.Session{SessionID: "conv", Agent: "claude", Cwd: m.assistantCwd(), Status: proto.StatusIdle, LastText: "OK"}
	ref := SessionRef{MachineID: "mac", SessionID: "conv"}
	ft.sessions("mac", conv)
	s.UserSpoke(t.Context(), ref)
	conv.Status = proto.StatusWorking
	ft.sessions("mac", conv)
	conv.Status = proto.StatusDone
	ft.sessions("mac", conv)
	waitUntil(t, func() bool { return len(availEvents(s, replyWaitEvent)) == 1 })
	if w := availEvents(s, replyWaitEvent)[0]; w["by"] != "turn_end" || w["state"] != waitIdle {
		t.Fatalf("reply wait %v", w)
	}

	old, oldFor := replyWaitMax, availInputFor
	replyWaitMax = 0
	t.Cleanup(func() { replyWaitMax, availInputFor = old, oldFor })
	s.UserSpoke(t.Context(), ref)
	conv.Status = proto.StatusWorking
	ft.sessions("mac", conv) // the turn started
	time.Sleep(time.Millisecond)
	ft.sessions("mac", conv) // and wrote nothing
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 2 || waits[1]["unanswered"] != true || waits[1]["wait"] != 0.0 {
		t.Fatalf("reply waits %v", waits)
	}
	if n := s.avail.waiting.Load(); n != 0 {
		t.Fatalf("%d waits left", n)
	}

	// A message that started no turn (its send was refused) is no wait, not an unanswered one.
	replyWaitMax, availInputFor = old, 0
	conv.Status = proto.StatusInterrupted
	ft.sessions("mac", conv)
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 2 })
	s.UserSpoke(t.Context(), ref)
	time.Sleep(time.Millisecond)
	ft.sessions("mac", conv)
	if got := availEvents(s, replyWaitEvent); len(got) != 2 || s.avail.waiting.Load() != 0 {
		t.Fatalf("an undelivered message counted as a wait: %v, %d waiting", got, s.avail.waiting.Load())
	}
}

// availConv is a fleet test with one idle assistant conversation "conv" on "mac": list has its node list it.
func availConv(t *testing.T) (ft *fleetTest, ref SessionRef, list func(status, text string, queued ...string)) {
	ft = newFleetTest(t)
	ft.f.sessions["mac"], ft.f.known["conv"] = nil, true // the fake hub answers the sends to it
	m, _ := ft.s.fleetMachine("mac")
	conv := proto.Session{SessionID: "conv", Agent: "claude", Cwd: m.assistantCwd(), Status: proto.StatusIdle, LastText: "earlier"}
	list = func(status, text string, queued ...string) {
		c := conv
		c.Status, c.LastText, c.Queued = status, text, queued
		ft.sessions("mac", c)
	}
	list(proto.StatusIdle, "earlier")
	return ft, SessionRef{MachineID: "mac", SessionID: "conv"}, list
}

// superviseRound starts a supervision round in the conversation and waits for the book to have it.
func superviseRound(t *testing.T, ft *fleetTest, list func(status, text string, queued ...string), text string) {
	t.Helper()
	if err := ft.s.deliverRelay(t.Context(), "mac", "conv", relayMsg{Tool: superviseTool, Text: "Synthetic supervision."}); err != nil {
		t.Fatal(err)
	}
	list(proto.StatusWorking, text)
	waitUntil(t, func() bool {
		ft.s.avail.mu.Lock()
		defer ft.s.avail.mu.Unlock()
		return ft.s.avail.rounds[[2]string{"mac", "conv"}].source == proto.SourceSupervision
	})
}

// A round that stops without a turn's end (its process went away, the owner pressed stop) is over and recorded as
// stopped: the owner's message hours later comes to an idle conversation and starts a round of their own, not a
// continuation of the old one.
func TestAvailabilityStoppedRound(t *testing.T) {
	ft, ref, list := availConv(t)
	s := ft.s
	superviseRound(t, ft, list, "earlier")
	list(proto.StatusOffline, "earlier")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 1 })
	if r := availEvents(s, roundEvent)[0]; r["source"] != proto.SourceSupervision || r["stopped"] != true {
		t.Fatalf("round %v", r)
	}

	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "Here.") // resumed: offline to working
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 1 || waits[0]["state"] != waitIdle {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusDone, "Here.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 2 })
	if r := availEvents(s, roundEvent)[1]; r["source"] != roundOwner || r["stopped"] != nil || r["seconds"].(float64) > 60 {
		t.Fatalf("round %v", r)
	}

	// A round left in the book whose stop was never seen (the list was lost with its node) does not make the
	// conversation busy, and the round the owner's message starts replaces it.
	s.avail.mu.Lock()
	s.avail.rounds[[2]string{"mac", "conv"}] = availRound{source: proto.SourceSupervision, started: time.Now().Add(-5 * time.Hour)}
	s.avail.mu.Unlock()
	list(proto.StatusOffline, "Here.")
	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "Again.")
	if waits = availEvents(s, replyWaitEvent); len(waits) != 2 || waits[1]["state"] != waitIdle {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusDone, "Again.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 3 })
	if r := availEvents(s, roundEvent)[2]; r["source"] != roundOwner || r["seconds"].(float64) > 60 {
		t.Fatalf("round %v", r)
	}
}

// A conversation whose node lists no queue (Codex, a runtime, an older Claude Code): the text a background round
// writes after the owner's message is that round's own, not the reply. A round following at once is the owner's and
// its first text the reply, however long before the round's end the message came; with no such round the message
// was taken into the running one, and that round's end stands in for the reply.
func TestAvailabilityReplyWaitWithoutQueue(t *testing.T) {
	ft, ref, list := availConv(t)
	s := ft.s
	oldFor := availInputFor
	availInputFor = time.Millisecond // the message is noted long before the round it waits behind ends
	t.Cleanup(func() { availInputFor = oldFor })
	superviseRound(t, ft, list, "earlier")
	s.UserSpoke(t.Context(), ref)
	time.Sleep(5 * time.Millisecond)
	list(proto.StatusWorking, "Checking the PR.")
	list(proto.StatusWorking, "Merged it.")
	if got := availEvents(s, replyWaitEvent); len(got) != 0 {
		t.Fatalf("the background round's text counted as the reply: %v", got)
	}
	list(proto.StatusDone, "Merged it.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 1 })
	list(proto.StatusWorking, "Merged it.")
	list(proto.StatusWorking, "Here is the answer.")
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 1 || waits[0]["state"] != waitBackground || waits[0]["by"] != nil {
		t.Fatalf("reply waits %v", waits)
	}
	list(proto.StatusDone, "Here is the answer.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 2 })
	if r := availEvents(s, roundEvent)[1]; r["source"] != roundOwner {
		t.Fatalf("the owner's queued message started a round of %v", r["source"])
	}

	// Taken into the running round: no round follows.
	oldGrace := availQueuedGrace
	availQueuedGrace = 0
	t.Cleanup(func() { availQueuedGrace = oldGrace })
	superviseRound(t, ft, list, "Here is the answer.")
	s.UserSpoke(t.Context(), ref)
	list(proto.StatusWorking, "Both done.")
	list(proto.StatusDone, "Both done.")
	waitUntil(t, func() bool {
		s.avail.mu.Lock()
		defer s.avail.mu.Unlock()
		return !s.avail.waits[[2]string{"mac", "conv"}].ended.IsZero()
	})
	time.Sleep(time.Millisecond)
	list(proto.StatusDone, "Both done.")
	waits = availEvents(s, replyWaitEvent)
	if len(waits) != 2 || waits[1]["state"] != waitBackground || waits[1]["by"] != "turn_end" {
		t.Fatalf("reply waits %v", waits)
	}
}

// The hub says the owner spoke before it knows a message for a replay of one it already delivered: the replay opens
// no wait, whichever of its two signals comes first. A conversation that leaves its node's list ends its wait.
func TestAvailabilityReplayAndGone(t *testing.T) {
	ft, ref, list := availConv(t)
	s := ft.s
	send := func(id string) {
		_, _ = s.SendParams(t.Context(), SendParams{MachineID: "mac", SessionID: "conv", Params: map[string]any{"text": "Hello", "client_msg_id": id}})
	}
	s.UserSpoke(t.Context(), ref)
	send("m1")
	list(proto.StatusWorking, "Hi.")
	list(proto.StatusDone, "Hi.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 1 })
	if got := availEvents(s, replyWaitEvent); len(got) != 1 {
		t.Fatalf("reply waits %v", got)
	}
	s.UserSpoke(t.Context(), ref) // the replay, after a silent reconnect
	send("m1")
	if n := s.avail.waiting.Load(); n != 0 {
		t.Fatalf("a replay opened a wait (%d)", n)
	}
	send("m1")
	s.UserSpoke(t.Context(), ref)
	if n := s.avail.waiting.Load(); n != 0 {
		t.Fatalf("a replay whose send came first opened a wait (%d)", n)
	}
	// A message the hub could not send is taken back; one that failed after an earlier message opened the wait is not.
	s.UserSpoke(t.Context(), ref)
	s.ConversationSent(t.Context(), ConversationSent{MachineID: "mac", SessionID: "conv", Outcome: assistantwire.ConversationFailed})
	s.waitWorkEvents()
	if n := s.avail.waiting.Load(); n != 0 {
		t.Fatalf("a failed send opened a wait (%d)", n)
	}
	superviseRound(t, ft, list, "Hi.") // the replay's input is not the next round's
	list(proto.StatusDone, "Hi.")
	waitUntil(t, func() bool { return len(availEvents(s, roundEvent)) == 2 })

	// A new message during a round, and the conversation is gone before a reply.
	superviseRound(t, ft, list, "Hi.")
	s.UserSpoke(t.Context(), ref)
	send("m2")
	if n := s.avail.waiting.Load(); n != 1 {
		t.Fatalf("%d waits", n)
	}
	old := replyWaitMax
	replyWaitMax = 0
	t.Cleanup(func() { replyWaitMax = old })
	time.Sleep(time.Millisecond)
	ft.sessions("mac")
	waits := availEvents(s, replyWaitEvent)
	if len(waits) != 2 || waits[1]["unanswered"] != true || s.avail.waiting.Load() != 0 {
		t.Fatalf("reply waits %v, %d waiting", waits, s.avail.waiting.Load())
	}
}

// A queued message records, once delivered, how long it waited and how long the queue was when it was added; one
// that takes a queued message's place has waited since the first.
func TestAvailabilityQueueWait(t *testing.T) {
	f := newFakeHub().withMachine()
	f.known["receiver"] = true
	s := newTestService(t, f)
	first := relayMsg{From: "c1", FromTitle: "Research", Tool: "conversation_send", Text: "Read the result.", Hop: 1, At: time.Now().UTC()}
	if n := s.queueRelay("receiver", first); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if n := s.queueRelay("receiver", relayMsg{Tool: superviseTool, Text: "Synthetic supervision."}); n != 2 {
		t.Fatalf("queued %d", n)
	}
	queued := s.relays.st.Queue["receiver"][1]
	if queued.Depth != 2 || queued.QueuedAt.IsZero() {
		t.Fatalf("queued %+v", queued)
	}
	if !s.replaceQueuedSupervise("receiver", relayMsg{Tool: superviseTool, Text: "A later one."}) {
		t.Fatal("not replaced")
	}
	if got := s.relays.st.Queue["receiver"][1]; got.Text != "A later one." || !got.QueuedAt.Equal(queued.QueuedAt) || got.Depth != 2 {
		t.Fatalf("the replacement lost when the first was queued: %+v", got)
	}
	now := s.availabilityQueued(t.Context(), time.Now())
	if len(now) != 1 || now[0].Conversation != "receiver" || now[0].Depth != 2 {
		t.Fatalf("queued now %+v", now)
	}

	idle := []proto.Session{{SessionID: "receiver", Status: proto.StatusIdle}}
	s.drainRelays("server", idle)
	waitUntil(t, func() bool { return len(availEvents(s, queueWaitEvent)) == 1 })
	if e := availEvents(s, queueWaitEvent)[0]; e["kind"] != proto.SourceConversation || e["depth"] != 1 {
		t.Fatalf("queue wait %v", e)
	}
	s.drainRelays("server", idle)
	waitUntil(t, func() bool { return len(availEvents(s, queueWaitEvent)) == 2 })
	if e := availEvents(s, queueWaitEvent)[1]; e["kind"] != proto.SourceSupervision || e["depth"] != 2 {
		t.Fatalf("queue wait %v", e)
	}
}

// A card's end records who ended it; the read tells the assistant's answers apart by the events that already say
// whose they were, and counts a card whose answer was changed later once.
func TestAvailabilityAskOutcome(t *testing.T) {
	s := newStorelessService(t, newFakeHub().withMachine())
	asked := time.Now().Add(-time.Minute)
	ix := func(id, kind, tool string) proto.Interaction {
		return proto.Interaction{ID: id, MachineID: "mac", SessionID: "s1", Kind: kind, ToolName: tool, CreatedAt: asked}
	}
	now := time.Now()
	s.availAskEnded(InteractionResolved{Interaction: ix("q1", proto.IxQuestion, ""), Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("q1", proto.IxQuestion, ""), Status: "resolved"}, now) // re-answered
	by := proto.Resolution{AnsweredBy: proto.AnsweredByAssistant}
	s.availAskEnded(InteractionResolved{Interaction: ix("q2", proto.IxQuestion, ""), Resolution: by, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("q3", proto.IxQuestion, ""), Resolution: by, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("p1", proto.IxPermission, ""), Resolution: by, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("p2", proto.IxPermission, ""), Resolution: proto.Resolution{Remembered: true}, Status: "resolved"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("a1", proto.IxQuestion, "ask_user"), Status: "dismissed"}, now)
	s.availAskEnded(InteractionResolved{Interaction: ix("f1", proto.IxQuestion, ""), Status: "failed"}, now) // still open
	s.availAskEnded(InteractionResolved{Interaction: ix("a2", proto.IxQuestion, "ask_user"),
		Resolution: proto.Resolution{Dismiss: true, Reason: proto.DismissedExpired}, Status: "dismissed"}, now)
	s.delegateMark(ix("q2", proto.IxQuestion, ""), "delegate_start")
	s.workEvent("mac", "s1", "standing_authorized", map[string]any{"interaction": "p1"})

	asks := []taskAsk{{kind: "decision", who: askWhoDefault, after: 86400}, {kind: "decision", who: askByOwner, after: 60}}
	got := summarizeAvailability(1, time.Time{}, testEvents(s), time.UTC, asks, nil).AskOutcome
	want := map[string]map[string]int{
		proto.IxQuestion:   {askByOwner: 1, askWhoDelegate: 1, askWhoSupervisor: 1},
		proto.IxPermission: {askWhoStanding: 1, askByRemembered: 1},
		"ask_user":         {askWhoDismissed: 1, askWhoExpired: 1},
		"decision":         {askWhoDefault: 1, askByOwner: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("ask outcome %+v", got)
	}
	for kind, who := range want {
		if len(got[kind]) != len(who) {
			t.Fatalf("%s: %+v", kind, got[kind])
		}
		for w, n := range who {
			if got[kind][w].N != n {
				t.Fatalf("%s by %s: %+v, want %d", kind, w, got[kind][w], n)
			}
		}
	}
	if st := got[proto.IxQuestion][askByOwner]; st.Median < 59 || st.Median > 61 {
		t.Fatalf("answered after %+v, want about a minute", st)
	}
	// The delegate took the card up before the window the read covers: its answer is still the delegate's, and the
	// outcomes from before the window are not counted.
	events := testEvents(s)
	for i := range events {
		events[i].At = now
		if events[i].Kind == "delegate_start" || events[i].Detail["interaction"] == "q1" {
			events[i].At = now.Add(-2 * time.Hour)
		}
	}
	got = summarizeAvailability(1, now.Add(-time.Hour), events, time.UTC, nil, nil).AskOutcome
	if q := got[proto.IxQuestion]; q[askWhoDelegate].N != 1 || q[askWhoSupervisor].N != 1 || q[askByOwner].N != 0 {
		t.Fatalf("a shorter window: %+v", q)
	}
}

// The read: waits by what the conversation was doing, the main conversation's per day, round time by what started
// the round with the share the owner did not start, and queue waits by kind.
func TestSummarizeAvailability(t *testing.T) {
	day := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	ev := func(at time.Time, session, kind string, detail map[string]any) db.WorkEvent {
		b, _ := json.Marshal(detail) // as the store returns them
		var d map[string]any
		_ = json.Unmarshal(b, &d)
		return db.WorkEvent{At: at, MachineID: "server", SessionID: session, Kind: kind, Detail: d}
	}
	events := []db.WorkEvent{
		ev(day, "main", replyWaitEvent, map[string]any{"state": waitIdle, "wait": 2.0, "main": true, "open_tasks": 3}),
		ev(day, "main", replyWaitEvent, map[string]any{"state": waitIdle, "wait": 4.0, "main": true, "open_tasks": 12}),
		ev(day, "main", replyWaitEvent, map[string]any{"state": waitBackground, "wait": 90.0, "main": true}),
		ev(day.Add(24*time.Hour), "main", replyWaitEvent, map[string]any{"state": waitIdle, "wait": 1800.0, "main": true, "unanswered": true}),
		ev(day, "side", replyWaitEvent, map[string]any{"state": waitOwnerRound, "wait": 7.0, "main": false}),
		ev(day, "main", roundEvent, map[string]any{"source": roundOwner, "seconds": 30.0, "main": true}),
		ev(day, "main", roundEvent, map[string]any{"source": proto.SourceSupervision, "seconds": 60.0, "main": true}),
		ev(day, "main", roundEvent, map[string]any{"source": proto.SourceSupervision, "seconds": 30.0, "main": true}),
		ev(day, "main", roundEvent, map[string]any{"source": roundOther, "seconds": 600.0, "main": true}),
		ev(day, "side", roundEvent, map[string]any{"source": roundOwner, "seconds": 5.0, "main": false}),
		ev(day, "main", queueWaitEvent, map[string]any{"kind": proto.SourceSupervision, "wait": 600.0, "depth": 3, "main": true}),
		ev(day, "side", queueWaitEvent, map[string]any{"kind": proto.SourceSupervision, "wait": 900.0, "depth": 4, "main": false}),
		ev(day, "main", queueWaitEvent, map[string]any{"kind": roundNotice, "wait": 20.0, "depth": 1, "main": true}),
	}
	got := summarizeAvailability(48, day.Add(-time.Hour), events, time.UTC, nil, []queuedNow{{Conversation: "main", Main: true, Depth: 1, OldestSeconds: 5}})
	if w := got.ReplyWait.Main[waitIdle]; w.N != 3 || w.Median != 4 || w.P90 != 1800 || w.Unanswered != 1 {
		t.Fatalf("main idle %+v", w)
	}
	if w := got.ReplyWait.Main[waitBackground]; w.N != 1 || w.P90 != 90 {
		t.Fatalf("main background %+v", w)
	}
	if _, ok := got.ReplyWait.Main[waitOwnerRound]; ok || got.ReplyWait.All[waitOwnerRound].N != 1 {
		t.Fatalf("another conversation's wait: %+v", got.ReplyWait)
	}
	if d := got.ReplyWait.MainDays; len(d) != 2 || d[0].Day != "2026-03-03" || d[0].Unanswered != 1 || d[1].N != 3 || d[1].P90 != 90 ||
		d[0].OpenTasks != nil || d[1].OpenTasks == nil || *d[1].OpenTasks != 12 {
		t.Fatalf("main days %+v", d)
	}
	if len(got.BackgroundShare) != 2 {
		t.Fatalf("background share %+v", got.BackgroundShare)
	}
	main, side := got.BackgroundShare[0], got.BackgroundShare[1]
	if main.Conversation != "main" || !main.Main || main.Seconds[proto.SourceSupervision] != 90 || main.Rounds[proto.SourceSupervision] != 2 ||
		main.Rounds[roundOwner] != 1 || main.Seconds[roundOther] != 600 || main.BackgroundShare != 0.75 { // "other" is on neither side

		t.Fatalf("main rounds %+v", main)
	}
	if side.Conversation != "side" || side.Main || side.BackgroundShare != 0 {
		t.Fatalf("side rounds %+v", side)
	}
	if q := got.QueueWait.ByKind[proto.SourceSupervision]; q.N != 2 || q.MaxSeconds != 900 || q.MaxDepth != 4 {
		t.Fatalf("queue wait %+v", q)
	}
	if q := got.QueueWait.MainByKind[proto.SourceSupervision]; q.N != 1 || q.P90 != 600 || q.MaxSeconds != 600 || q.MaxDepth != 3 {
		t.Fatalf("the main conversation's queue wait %+v", q)
	}
	if len(got.QueueWait.QueuedNow) != 1 || got.QueueWait.ByKind[roundNotice].MaxDepth != 1 {
		t.Fatalf("queue %+v", got.QueueWait)
	}
	empty, err := json.Marshal(summarizeAvailability(24, time.Time{}, nil, time.UTC, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"hours":24,"reply_wait":{"all":{},"main":{},"main_days":[]},"background_share":[],"queue_wait":{"by_kind":{},"main_by_kind":{},"queued_now":[]},"ask_outcome":{}}`; string(empty) != want {
		t.Fatalf("no events:\n%s\nwant\n%s", empty, want)
	}
}

// The route is a full device's, reads a week unless asked otherwise, and says so when there is no database.
func TestAvailabilityRoute(t *testing.T) {
	f := newFakeHub().withMachine()
	st := &workMemStore{}
	s := newServiceOn(t, f, st)
	s.workEvent("server", "main", roundEvent, map[string]any{"source": roundOwner, "seconds": 12.5, "main": true})
	s.waitWorkEvents()
	if w := serveAs(s, roleAnswer, "GET", "/api/v1/assistant/availability", ""); w.Code != http.StatusForbidden {
		t.Fatalf("answer-only device: %d %s", w.Code, w.Body)
	}
	w := serve(s, "GET", "/api/v1/assistant/availability", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got availabilityStats
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hours != 24*7 || len(got.BackgroundShare) != 1 || got.BackgroundShare[0].Seconds[roundOwner] != 12.5 {
		t.Fatalf("read %+v", got)
	}
	if w := serve(s, "GET", "/api/v1/assistant/availability?hours=24", ""); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	none := newTestService(t, newFakeHub().withMachine())
	if w := serve(none, "GET", "/api/v1/assistant/availability", ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no database: %d %s", w.Code, w.Body)
	}
}
