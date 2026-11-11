package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// A question asked without waiting (#4928, ask_later.go) holds no turn, so the conversation and the channels go on
// around its card: what the owner writes meanwhile, a channel run's answer, a text channel's next message. All ids
// are placeholders.

func noWaitDeadlineArgs(deadline string) string {
	args := strings.Replace(strings.Replace(askDeadlineArgsJSON, "%s", deadline, 1), "%s", "true", 1)
	return strings.Replace(args, `{"deadline"`, `{"wait":false,"deadline"`, 1)
}

const expiredAnswer = `{"dismiss":true,"reason":"` + proto.DismissedExpired + `"}`

// Asked without waiting, a reversible question whose card waits until its deadline takes its recommended option when
// nobody answered and the owner said nothing: the conversation is told so in one message, and its to-do is closed.
func TestAskUserNoWaitTakesTheDefaultAtItsDeadline(t *testing.T) {
	old := askInlineSlack
	askInlineSlack = 2 * time.Minute // the card is ended at once, a minute before its 1m deadline
	t.Cleanup(func() { askInlineSlack = old })
	e := newAskTodoEnv(t, nil)
	ix, _ := e.askLater(noWaitDeadlineArgs("1m"))
	e.mustEnd(ix.ID, expiredAnswer)
	got := e.told("c1x1x1")
	if len(got) != 1 || !strings.Contains(got[0], "taken by default") || !strings.Contains(got[0], "Merge the release PR now? → Merge") {
		t.Fatalf("told %q", got)
	}
	if todo := e.onlyAskTodo(); todo.DoneAt == nil {
		t.Fatal("its to-do is still open")
	}
}

// The owner wrote in the conversation while the card was up (their words may have been the answer, or a "no"): a
// card that then runs out unanswered takes no default and makes no to-do. The conversation is told once, and told to
// go by what the owner wrote or to ask again.
func TestAskUserNoWaitTakesNoDefaultAfterTheUserWroteInTheChat(t *testing.T) {
	old := askInlineSlack
	askInlineSlack = 2 * time.Minute
	t.Cleanup(func() { askInlineSlack = old })
	for _, c := range []struct{ name, args string }{
		{"reversible deadline", noWaitDeadlineArgs("1m")},
		{"no deadline", askLaterArgs},
	} {
		t.Run(c.name, func(t *testing.T) {
			presenceTimes(t, 200*time.Millisecond, time.Hour)
			e := newAskTodoEnv(t, nil)
			stopHolds(t, e.s)
			ix, _ := e.askLater(c.args)
			e.s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "c1x1x1"})
			e.mustEnd(ix.ID, expiredAnswer)
			_ = e.end(ix.ID, expiredAnswer) // handed over again: gone
			// The owner is talking there: the system's word that the card ran out waits for them (#4925).
			if q, sent := e.s.queuedFor("c1x1x1"), len(e.told("c1x1x1")); q != 1 || sent != 0 {
				t.Fatalf("while the owner talks: %d queued, %d sent", q, sent)
			}
			waitFor(t, "told once the owner is quiet", func() bool { return len(e.told("c1x1x1")) >= 1 })
			got := e.told("c1x1x1")
			if len(got) != 1 {
				t.Fatalf("%d messages, want 1: %q", len(got), got)
			}
			for _, want := range []string{"Merge the release PR now?", "没有转成待办", "再问一次"} {
				if !strings.Contains(got[0], want) {
					t.Errorf("message lacks %q:\n%s", want, got[0])
				}
			}
			for _, not := range []string{"taken by default", "Go ahead", "todo_ask_", "→"} {
				if strings.Contains(got[0], not) {
					t.Errorf("message has %q:\n%s", not, got[0])
				}
			}
			if list := e.askTodos(); len(list) != 0 {
				t.Fatalf("to-dos: %+v", list)
			}
		})
	}
}

// The owner writing in another conversation says nothing about this one's question: it still becomes a to-do.
func TestAskUserNoWaitWritingElsewhereChangesNothing(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ix, _ := e.askLater(askLaterArgs)
	e.s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "m1"})
	e.mustEnd(ix.ID, expiredAnswer)
	todo := e.onlyAskTodo()
	if got := e.told("c1x1x1"); len(got) != 1 || !strings.Contains(got[0], todo.ID) {
		t.Fatalf("told %q, want one message naming %s", got, todo.ID)
	}
}

// That the owner wrote is kept with the question's record: a manager restarted in between still takes no default.
func TestAskUserNoWaitRemembersTheUserWroteAcrossARestart(t *testing.T) {
	presenceTimes(t, 200*time.Millisecond, time.Hour)
	st := &memStore{}
	e := newAskTodoEnv(t, st)
	ix, _ := e.askLater(askLaterArgs)
	e.s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "c1x1x1"})
	again := newAskTodoEnv(t, st)
	stopHolds(t, again.s)
	if qs := again.s.pendingQuestions(); len(qs) != 1 {
		t.Fatalf("the restarted manager reports %+v", qs)
	}
	again.mustEnd(ix.ID, expiredAnswer)
	waitFor(t, "told once the owner is quiet", func() bool { return len(again.told("c1x1x1")) >= 1 }) // held for them meanwhile (#4925)
	if got := again.told("c1x1x1"); len(got) != 1 || !strings.Contains(got[0], "没有转成待办") {
		t.Fatalf("told %q", got)
	}
	if list := again.askTodos(); len(list) != 0 {
		t.Fatalf("to-dos: %+v", list)
	}
}

// A manager restarted with a no-wait question out whose first read of the records failed (or that has not made its
// first snapshot yet), and that is then asked a new question: reading the records for the new one does not stand in
// for putting the old ones back. The old card would otherwise be cancelled under the owner and its conversation never
// told (#26).
func TestAskUserNoWaitAskedBeforeTheRecordsWereRestored(t *testing.T) {
	st := &memStore{}
	e := newAskTodoEnv(t, st)
	ix, _ := e.askLater(askLaterArgs)
	failRead := func(err error) {
		st.mu.Lock()
		st.failKeys = map[string]error{askLaterKey: err}
		st.mu.Unlock()
	}
	failRead(errors.New("the store is away"))
	again := newAskTodoEnv(t, st)
	if qs := again.s.pendingQuestions(); len(qs) != 0 {
		t.Fatalf("questions back though the records could not be read: %+v", qs)
	}
	if _, err := again.s.toolAskUser(context.Background(), "server", "c1x1x1", json.RawMessage(askLaterArgs)); err == nil {
		t.Fatal("a question was asked though its record could not be kept")
	}
	failRead(nil)
	if _, err := again.s.toolAskUser(context.Background(), "server", "c1x1x1", json.RawMessage(askLaterArgs)); err != nil {
		t.Fatal(err)
	}
	qs := again.s.pendingQuestions()
	if len(qs) != 2 || qs[0].ID != ix.ID {
		t.Fatalf("the restarted manager reports %+v, want %s and the new one", qs, ix.ID)
	}
	again.mustEnd(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	if got := again.told("c1x1x1"); len(got) != 1 || !strings.Contains(got[0], "→ Wait") {
		t.Fatalf("told %q", got)
	}
}

// ---- channels ----

// A channel run whose turn asked without waiting and ended is answered with the turn's closing words: the card is
// not something the turn waits on. A waiting ask_user's card (and any other card of the conversation) still holds
// the run.
func TestChannelRunIsNotHeldByANoWaitQuestion(t *testing.T) {
	later := proto.Interaction{ID: "ix_later", MachineID: "server", SessionID: "s1", Kind: proto.IxQuestion, ToolName: "ask_user", Resumable: true}
	waiting := proto.Interaction{ID: "ix_wait", MachineID: "server", SessionID: "s1", Kind: proto.IxQuestion, ToolName: "ask_user"}
	hook := proto.Interaction{ID: "ix_hook", MachineID: "server", SessionID: "s1", Kind: proto.IxPermission, ToolName: "Bash", Resumable: true}
	other := proto.Interaction{ID: "ix_other", MachineID: "server", SessionID: "s2", Kind: proto.IxQuestion, ToolName: "ask_user"}
	for _, c := range []struct {
		name  string
		cards []proto.Interaction
		held  bool
	}{
		{"asked without waiting", []proto.Interaction{later, other}, false},
		{"a waiting ask_user", []proto.Interaction{waiting}, true},
		{"an agent's resumable card", []proto.Interaction{hook}, true},
		{"both", []proto.Interaction{later, waiting}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, sent, r := queuedRunHub(t, "")
			f := s.hub.(*pagedHub).fakeHub
			f.mu.Lock()
			f.hubCards = c.cards
			f.mu.Unlock()
			if got := s.hasPending(context.Background(), "server", "s1"); got != c.held {
				t.Fatalf("hasPending %v, want %v", got, c.held)
			}
			s.checkChannelRun(r.ID)
			if left := len(s.channelRuns("examplechat")); c.held && (left != 1 || len(*sent) != 0) {
				t.Fatalf("a run behind a waiting card was ended: %d runs left, sent %v", left, *sent)
			} else if !c.held && (left != 0 || len(*sent) != 1) {
				t.Fatalf("the run is held by a card no turn waits on: %d runs left, sent %v", left, *sent)
			}
		})
	}
}

func noWaitCard() proto.Interaction {
	return proto.Interaction{ID: "ix_later", MachineID: "server", SessionID: "m1", Kind: proto.IxQuestion, ToolName: "ask_user", Resumable: true,
		Questions: []proto.Question{{Question: "Merge the release PR now?", Options: []proto.QuestionOption{{Label: "Merge"}, {Label: "Wait"}}}}}
}

// In a text channel, a question asked without waiting may be up for hours while the conversation looks idle: an
// ordinary message sent meanwhile is a message for the conversation, not the card's answer. The card stays up, and a
// reply that names one of its options still answers it.
func TestChannelMessageIsNotTakenAsANoWaitQuestionsAnswer(t *testing.T) {
	s, f, notes, resolves := newTextAskService(t, noWaitCard())
	s.channelReceive("examplechat", channel.Message{ID: "r1", Text: "what's on my calendar tomorrow?"}, "act")
	if len(*resolves) != 0 {
		t.Fatalf("an ordinary message answered the card: %v", *resolves)
	}
	if got := sendOnceText(f); !strings.Contains(got, "what's on my calendar tomorrow?") {
		t.Fatalf("the message did not go to the conversation: %q", got)
	}
	if strings.Contains(noteTexts(notes), "已回答") {
		t.Fatalf("the owner was told it was answered: %q", noteTexts(notes))
	}
	if a := askInState(s, "ix_later"); a == nil || a.Step != 0 || len(a.Answers) != 0 {
		t.Fatalf("the ask after an ordinary message: %+v", a)
	}
	s.channelReceive("examplechat", channel.Message{ID: "r2", Text: "2"}, "act")
	if len(*resolves) != 1 {
		t.Fatalf("%d resolutions after naming an option, want 1: %v", len(*resolves), *resolves)
	}
	r, _ := (*resolves)[0]["resolution"].(map[string]any)
	if a, _ := r["answers"].(map[string]any); a["Merge the release PR now?"] != "Wait" {
		t.Fatalf("resolution %v", r)
	}
}

// A waiting ask_user holds its turn, so free words in the channel are its answer, as before.
func TestChannelFreeWordsStillAnswerAWaitingQuestion(t *testing.T) {
	ix := noWaitCard()
	ix.ID, ix.Resumable = "ix_wait", false
	s, f, _, resolves := newTextAskService(t, ix)
	s.channelReceive("examplechat", channel.Message{ID: "r1", Text: "merge it after lunch"}, "act")
	if len(*resolves) != 1 {
		t.Fatalf("%d resolutions, want 1", len(*resolves))
	}
	r, _ := (*resolves)[0]["resolution"].(map[string]any)
	if a, _ := r["answers"].(map[string]any); a["Merge the release PR now?"] != "merge it after lunch" {
		t.Fatalf("resolution %v", r)
	}
	if got := sendOnceText(f); got != "" {
		t.Fatalf("the answer went on to the conversation: %q", got)
	}
}

// Words that do not answer the no-wait question asked last answer the card asked before it that does hold a turn.
func TestChannelReplyPassesANoWaitQuestionToTheCardThatWaits(t *testing.T) {
	perm := proto.Interaction{ID: "ix_p", MachineID: "server", SessionID: "a1", Kind: proto.IxPermission}
	s, f, _, resolves := newTextAskService(t, perm, noWaitCard())
	s.channelReceive("examplechat", channel.Message{ID: "r1", Text: "允许"}, "act")
	if len(*resolves) != 1 || (*resolves)[0]["interaction_id"] != "ix_p" {
		t.Fatalf("resolutions %v, want the permission's", *resolves)
	}
	if got := sendOnceText(f); got != "" {
		t.Fatalf("the reply went on to the conversation: %q", got)
	}
	if askInState(s, "ix_later") == nil {
		t.Fatal("the no-wait question stopped waiting in the channel")
	}
}

// On the queue, a no-wait card that runs out after the owner wrote in the conversation closes saying "Replied in the
// chat", not "Moved to to-dos": it makes none.
func TestAskUserNoWaitCardClosesAsRepliedInChatAfterTheUserWrote(t *testing.T) {
	a := newAskRig(t)
	old := askUserWait
	askUserWait = 400 * time.Millisecond
	t.Cleanup(func() { askUserWait = old })
	ix := a.askLater(t, a.s)
	b := &a.s.fbot
	b.mu.Lock()
	a.s.keepBotCardLocked(ix.ID, &botCard{MessageID: "om_" + ix.ID, Thread: "om_run", Ix: ix, At: a.now})
	b.mu.Unlock()
	ctx := context.Background()
	a.s.UserSpoke(ctx, SessionRef{MachineID: "mac", SessionID: "s1"})
	waitFor(t, "the Lark card closed", func() bool { return a.closed() != "" })
	l := a.s.botLang(ctx)
	if got := a.closed(); !strings.Contains(got, i18n.T(l, "assistant-feishu_cards.replied_in_chat")) ||
		strings.Contains(got, i18n.T(l, "assistant-feishu_cards.moved_to_todos")) {
		t.Fatalf("the Lark card closed as %q", got)
	}
	waitFor(t, "the question gone", func() bool { return len(a.s.pendingQuestions()) == 0 })
	if status, _, _ := a.mem.InteractionOutcome(ctx, ix.ID); status != "expired" {
		t.Errorf("history status = %q, want expired", status)
	}
}

// The owner's answer on a no-wait card is the owner speaking, not the system (#4925, #4928): it goes into the
// conversation at once though they wrote there a moment ago, ahead of what the system has waiting for them, which
// stays held. A card that was closed unanswered is the system's to tell and waits as any late answer does.
func TestAskUserNoWaitAnswerIsNotHeldForTheOwnersPresence(t *testing.T) {
	presenceTimes(t, time.Hour, time.Hour)
	e := newAskTodoEnv(t, nil)
	stopHolds(t, e.s)
	ctx := context.Background()
	answered, _ := e.askLater(askLaterArgs)
	closed, _ := e.askLater(strings.Replace(askLaterArgs, "Merge the release PR now?", "Tag it as well?", 1))
	e.s.UserSpoke(ctx, SessionRef{MachineID: "server", SessionID: "c1x1x1"})
	e.s.relayOrQueue(ctx, "c1x1x1", relayMsg{From: "c2", FromTitle: "Research", Tool: "conversation_send", Text: "from another chat", At: time.Now().UTC()})
	if q, sent := e.s.queuedFor("c1x1x1"), len(e.told("c1x1x1")); q != 1 || sent != 0 {
		t.Fatalf("the system's message while the owner talks: %d queued, %d sent", q, sent)
	}
	e.mustEnd(answered.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	waitFor(t, "the owner's answer delivered", func() bool { return len(e.told("c1x1x1")) == 1 })
	if got := e.told("c1x1x1"); !strings.Contains(got[0], "Merge the release PR now? → Wait") {
		t.Fatalf("delivered %q", got)
	}
	e.mustEnd(closed.ID, `{"dismiss":true}`)
	time.Sleep(50 * time.Millisecond)
	if q, sent := e.s.queuedFor("c1x1x1"), len(e.told("c1x1x1")); q != 2 || sent != 1 {
		t.Fatalf("after the answer: %d queued (want the other chat's message and the closed card), %d sent", q, sent)
	}
	// Among what waits, the answer is first by rank, and only it is free of the hold.
	b := &e.s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	own := relayMsg{Tool: askAnswerTool, Owner: true, At: now, QueuedAt: now}
	if own.rank() != relayRankAnswer || b.holdLocked("c1x1x1", own, now) != 0 {
		t.Fatalf("the owner's answer: rank %d, held %s", own.rank(), b.holdLocked("c1x1x1", own, now))
	}
	for _, m := range b.st.Queue["c1x1x1"] {
		if m.Owner || b.holdLocked("c1x1x1", m, now) == 0 {
			t.Fatalf("a message of the system is not held: %+v", m)
		}
	}
}
