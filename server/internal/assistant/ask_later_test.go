package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ask_user with wait false raises its card and returns at once; whatever ends the card is told to the asking
// conversation as one message (#4928, ask_later.go). All ids are placeholders.

const askLaterArgs = `{"wait":false,"questions":[{"question":"Merge the release PR now?","header":"Merge","options":[` +
	`{"label":"Merge (Recommended)","description":"CI is green"},{"label":"Wait","description":"until the review is in"}]}]}`

// askLater has c1x1x1 ask without waiting and returns the card it put up and what the tool said. The call must not
// block: it runs on the test's own goroutine.
func (e *askTodoEnv) askLater(args string) (proto.Interaction, string) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before := map[string]bool{}
	for _, q := range e.s.pendingQuestions() {
		before[q.ID] = true
	}
	text, err := e.s.toolAskUser(ctx, "server", "c1x1x1", json.RawMessage(args))
	if err != nil {
		e.t.Fatalf("ask_user: %v", err)
	}
	for _, q := range e.s.pendingQuestions() {
		if !before[q.ID] {
			return q, text
		}
	}
	e.t.Fatal("ask_user with wait false put up no card")
	return proto.Interaction{}, ""
}

// end hands the question's card its end, as askOnPhone (or a hub that keeps the cards) does.
func (e *askTodoEnv) end(id, answer string) error {
	_, err := e.s.Action(context.Background(), Action{Op: "answer", InteractionID: id, Answer: json.RawMessage(answer)})
	return err
}

func (e *askTodoEnv) mustEnd(id, answer string) {
	e.t.Helper()
	if err := e.end(id, answer); err != nil {
		e.t.Fatal(err)
	}
}

// With wait false the tool returns at once, with the card out as a waiting question's is (the same questions, tool
// name and 2-hour expiry) and saying the answer comes later; the owner's answer then goes to the conversation as one
// message that quotes the question and the answer, and the card is gone. A second answer finds no question.
func TestAskUserNoWaitReturnsAtOnceAndTheAnswerComesAsAMessage(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ix, text := e.askLater(askLaterArgs)
	for _, want := range []string{ix.ID, "did not wait", "new message", "end this turn"} {
		if !strings.Contains(text, want) {
			t.Errorf("tool result lacks %q: %q", want, text)
		}
	}
	if ix.ToolName != "ask_user" || ix.Kind != proto.IxQuestion || !ix.CanCancel || !ix.Resumable || len(ix.Questions) != 1 ||
		ix.SessionID != "c1x1x1" || ix.SessionTitle != "Release chat" {
		t.Fatalf("card %+v", ix)
	}
	if wait := ix.ExpiresAt.Sub(ix.CreatedAt); wait != askUserWait {
		t.Errorf("the card waits %v, want %v as a waiting ask_user's", wait, askUserWait)
	}
	if got := e.told("c1x1x1"); len(got) != 0 {
		t.Fatalf("told before any answer: %q", got)
	}
	e.mustEnd(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	got := e.told("c1x1x1")
	if len(got) != 1 {
		t.Fatalf("%d messages to the conversation, want 1: %q", len(got), got)
	}
	when := ix.CreatedAt.In(e.s.location(context.Background())).Format("01-02 15:04")
	for _, want := range []string{when, "Merge the release PR now? → Wait", "wait: false"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("message lacks %q:\n%s", want, got[0])
		}
	}
	if qs := e.s.pendingQuestions(); len(qs) != 0 {
		t.Fatalf("the question is still out: %+v", qs)
	}
	var re *rpc.Error
	if err := e.end(ix.ID, `{"answers":{"Merge the release PR now?":"Merge (Recommended)"}}`); !errors.As(err, &re) || re.Code != assistantwire.CodeGone {
		t.Fatalf("a second answer: %v, want gone", err)
	}
	if got := e.told("c1x1x1"); len(got) != 1 {
		t.Fatalf("a second answer was sent too: %q", got)
	}
	if list := e.askTodos(); len(list) != 0 {
		t.Fatalf("an answered question made a to-do: %+v", list)
	}
}

// wait true, like no wait at all, holds the call until the answer: the default did not change.
func TestAskUserWaitTrueStillHoldsTheTurn(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		text, _ := e.s.toolAskUser(ctx, "server", "c1x1x1", json.RawMessage(strings.Replace(askLaterArgs, `"wait":false`, `"wait":true`, 1)))
		done <- text
	}()
	var ix proto.Interaction
	waitFor(t, "the question out", func() bool {
		qs := e.s.pendingQuestions()
		if len(qs) == 1 {
			ix = qs[0]
		}
		return len(qs) == 1
	})
	select {
	case text := <-done:
		t.Fatalf("returned before the answer: %q", text)
	case <-time.After(50 * time.Millisecond):
	}
	if ix.Resumable {
		t.Error("a waiting question's card is resumable")
	}
	e.mustEnd(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	if text := <-done; !strings.Contains(text, "The user answered") {
		t.Fatalf("tool result %q", text)
	}
	if got := e.told("c1x1x1"); len(got) != 0 {
		t.Fatalf("a waiting question's answer was sent as a message too: %q", got)
	}
}

// An answer that comes while the conversation is on a turn waits in its queue and goes when the turn ends.
func TestAskUserNoWaitAnswerQueuesBehindARunningTurn(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ix, _ := e.askLater(askLaterArgs)
	e.sessions("server", proto.Session{SessionID: "m1", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "c1x1x1", Title: "Release chat", Assistant: true, Status: proto.StatusWorking})
	e.mustEnd(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	if got := e.told("c1x1x1"); len(got) != 0 {
		t.Fatalf("sent into a running turn: %q", got)
	}
	if n := e.s.queuedFor("c1x1x1"); n != 1 {
		t.Fatalf("%d queued, want 1", n)
	}
	if qs := e.s.pendingQuestions(); len(qs) != 0 {
		t.Fatalf("the question is still out while its answer waits in the queue: %+v", qs)
	}
	e.sessions("server", proto.Session{SessionID: "m1", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "c1x1x1", Title: "Release chat", Assistant: true, Status: proto.StatusIdle})
	waitFor(t, "the answer delivered", func() bool { return len(e.told("c1x1x1")) == 1 })
	if got := e.told("c1x1x1"); !strings.Contains(got[0], "→ Wait") {
		t.Fatalf("delivered %q", got)
	}
}

// A card closed without an answer (dismissed, or cancelled with its close button) is told once, as closed, and makes
// no to-do.
func TestAskUserNoWaitClosedCardIsToldOnce(t *testing.T) {
	for _, c := range []struct{ name, answer string }{
		{"dismissed", `{"dismiss":true}`},
		{"cancelled", `{"cancel":true}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newAskTodoEnv(t, nil)
			ix, _ := e.askLater(askLaterArgs)
			e.mustEnd(ix.ID, c.answer)
			_ = e.end(ix.ID, c.answer) // handed over again: gone
			got := e.told("c1x1x1")
			if len(got) != 1 {
				t.Fatalf("%d messages, want 1: %q", len(got), got)
			}
			if !strings.Contains(got[0], "Merge the release PR now?") || !strings.Contains(got[0], "没有得到回答") || strings.Contains(got[0], "→") {
				t.Fatalf("message:\n%s", got[0])
			}
			if list := e.askTodos(); len(list) != 0 {
				t.Fatalf("to-dos: %+v", list)
			}
		})
	}
}

// A card that expires unanswered becomes the to-do any expired ask_user becomes, and the conversation is told so in
// one message naming it; closing that to-do later tells it once more, by the to-do's own path.
func TestAskUserNoWaitExpiredCardBecomesATodoAndSaysSo(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ix, _ := e.askLater(askLaterArgs)
	e.mustEnd(ix.ID, `{"dismiss":true,"reason":"`+proto.DismissedExpired+`"}`)
	todo := e.onlyAskTodo()
	if todo.ID != askTodoID(ix) || todo.Title != "Merge the release PR now?" {
		t.Fatalf("to-do %+v", todo)
	}
	got := e.told("c1x1x1")
	if len(got) != 1 || !strings.Contains(got[0], todo.ID) || !strings.Contains(got[0], "Merge the release PR now?") {
		t.Fatalf("told %q", got)
	}
	e.must("PATCH", "/api/v1/todos/"+todo.ID, `{"done":true}`)
	waitFor(t, "told the to-do was closed", func() bool { return len(e.told("c1x1x1")) == 2 })
}

// The owner writing in the conversation does not take a no-wait question back (no turn is held behind it): the card
// stays up and its answer still goes, once.
func TestAskUserNoWaitCardStaysWhenTheUserWritesInTheChat(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ix, _ := e.askLater(askLaterArgs)
	e.s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "c1x1x1"})
	if qs := e.s.pendingQuestions(); len(qs) != 1 || qs[0].ID != ix.ID {
		t.Fatalf("the question was taken back: %+v", qs)
	}
	if got := e.told("c1x1x1"); len(got) != 0 {
		t.Fatalf("told %q", got)
	}
	e.mustEnd(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	if got := e.told("c1x1x1"); len(got) != 1 || !strings.Contains(got[0], "→ Wait") {
		t.Fatalf("told %q", got)
	}
}

// With several questions, the message lists each answer given and says the others got none.
func TestAskUserNoWaitSaysWhichQuestionsGotNoAnswer(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	ix, _ := e.askLater(`{"wait":false,"questions":[{"question":"Merge?","options":[{"label":"Yes"},{"label":"No"}]},` +
		`{"question":"Tag it?","options":[{"label":"Yes"},{"label":"No"}]}]}`)
	e.mustEnd(ix.ID, `{"answers":{"Tag it?":"No"}}`)
	got := e.told("c1x1x1")
	if len(got) != 1 || !strings.Contains(got[0], "Tag it? → No") || strings.Contains(got[0], "Merge? →") || !strings.Contains(got[0], "没有答") {
		t.Fatalf("told %q", got)
	}
}

// A manager restart does not lose a no-wait question: the next manager reads it back from the assistant's settings,
// reports the same card (same id, so the queue claims it again instead of asking anew), and takes its answer. Once
// told, the record is gone.
func TestAskUserNoWaitSurvivesAManagerRestart(t *testing.T) {
	st := &memStore{}
	e := newAskTodoEnv(t, st)
	ix, _ := e.askLater(askLaterArgs)
	again := newAskTodoEnv(t, st) // a new manager on the same store
	qs := again.s.pendingQuestions()
	if len(qs) != 1 || qs[0].ID != ix.ID || !qs[0].Resumable || !qs[0].ExpiresAt.Equal(ix.ExpiresAt) || len(qs[0].Questions) != 1 {
		t.Fatalf("the restarted manager reports %+v, want card %s back", qs, ix.ID)
	}
	again.mustEnd(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`)
	if got := again.told("c1x1x1"); len(got) != 1 || !strings.Contains(got[0], "→ Wait") {
		t.Fatalf("told %q", got)
	}
	if qs := newAskTodoEnv(t, st).s.pendingQuestions(); len(qs) != 0 {
		t.Fatalf("an answered question is back after another restart: %+v", qs)
	}
}

// A card whose time ran out while no manager was there is ended by the next one with a bare dismiss (the history says
// cancelled): it is an expiry all the same, so the question becomes a to-do and the conversation is told, instead of
// waiting for an answer that cannot come.
func TestAskUserNoWaitExpiredAcrossARestartIsResolved(t *testing.T) {
	st := &memStore{}
	e := newAskTodoEnv(t, st)
	old := askUserWait
	askUserWait = -time.Minute // its time is up by the time the next manager starts
	ix, _ := e.askLater(askLaterArgs)
	askUserWait = old
	again := newAskTodoEnv(t, st)
	if qs := again.s.pendingQuestions(); len(qs) != 1 {
		t.Fatalf("the restarted manager reports %+v", qs)
	}
	again.mustEnd(ix.ID, `{"dismiss":true}`)
	todo := again.onlyAskTodo()
	if got := again.told("c1x1x1"); len(got) != 1 || !strings.Contains(got[0], todo.ID) {
		t.Fatalf("told %q, want one message naming %s", got, todo.ID)
	}
}

// An end that cannot be told (the conversation is gone and there is no main one) is not lost: the question stays out,
// the hand-over fails so it is tried again, and the retry says what was kept the first time, once.
func TestAskUserNoWaitEndIsToldAgainAfterAFailedSend(t *testing.T) {
	st := &memStore{}
	e := newAskTodoEnv(t, st)
	ix, _ := e.askLater(askLaterArgs)
	e.sessions("server", proto.Session{SessionID: "m1", Assistant: true, Status: proto.StatusIdle}) // the asker is gone
	setSettings(t, e.s, func(v *Settings) { v.Main = "" })
	if err := e.end(ix.ID, `{"answers":{"Merge the release PR now?":"Wait"}}`); err == nil {
		t.Fatal("an answer that went nowhere was taken")
	}
	if qs := e.s.pendingQuestions(); len(qs) != 1 {
		t.Fatalf("the question is gone though nobody was told: %+v", qs)
	}
	var kept []askLater
	if ok, err := st.Setting(context.Background(), askLaterKey, &kept); err != nil || !ok || len(kept) != 1 || !strings.Contains(kept[0].Message, "→ Wait") {
		t.Fatalf("record %+v, %v", kept, err)
	}
	setSettings(t, e.s, func(v *Settings) { v.Main = "m1" })
	// Handed over again (keepAnswer), whatever the hand-over says now: what was kept goes.
	e.mustEnd(ix.ID, `{"dismiss":true}`)
	if got := e.told("m1"); len(got) != 1 || !strings.Contains(got[0], "→ Wait") {
		t.Fatalf("the main conversation was told %q", got)
	}
	if ok, _ := st.Setting(context.Background(), askLaterKey, &kept); !ok || len(kept) != 0 {
		t.Fatalf("record after it was told: %+v", kept)
	}
}

// No more than askLaterMax questions asked without waiting are out at once.
func TestAskUserNoWaitIsBounded(t *testing.T) {
	e := newAskTodoEnv(t, nil)
	for i := 0; i < askLaterMax; i++ {
		e.askLater(askLaterArgs)
	}
	if _, err := e.s.toolAskUser(context.Background(), "server", "c1x1x1", json.RawMessage(askLaterArgs)); err == nil ||
		!strings.Contains(err.Error(), "not asked") {
		t.Fatalf("one more: %v", err)
	}
	if qs := e.s.pendingQuestions(); len(qs) != askLaterMax {
		t.Fatalf("%d questions out", len(qs))
	}
}

// On the manager's own interaction queue the no-wait card is a card like any other (recorded, on the phone), it
// outlives its node's connection (no call waits on it there), and an answer given on the queue (an app, the watch, a
// Lark or channel card: they all resolve it) reaches the conversation as the message.
func TestAskUserNoWaitCardOnTheQueueIsAnsweredFromAnywhere(t *testing.T) {
	a := newAskRig(t)
	// The rig's hub reaches no node, so the message waits in the conversation's queue: queued is what waits there.
	queued := func(s *Service) string {
		b := &s.relays
		b.mu.Lock()
		defer b.mu.Unlock()
		if !s.relayLocked(context.Background()) {
			return ""
		}
		var out []string
		for _, m := range b.st.Queue["s1"] {
			if m.Tool == askAnswerTool {
				out = append(out, m.Text)
			}
		}
		return strings.Join(out, "\n----\n")
	}
	ix := a.askLater(t, a.s)
	if status, _, _ := a.mem.InteractionOutcome(context.Background(), ix.ID); status != "pending" {
		t.Fatalf("history status %q, want pending", status)
	}
	a.s.NodeDetached("mac")
	if _, up := a.s.pendingIx(ix.ID); !up {
		t.Fatal("the node's connection ending took the card down")
	}
	if err := a.s.resolve(ix.ID, proto.Resolution{Answers: map[string]string{"Push it to beta?": "Yes"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the answer on its way to the conversation", func() bool { return strings.Contains(queued(a.s), "Push it to beta? → Yes") })
	waitFor(t, "the question gone", func() bool { return len(a.s.pendingQuestions()) == 0 })
	if n := strings.Count(queued(a.s), "Push it to beta? → Yes"); n != 1 {
		t.Fatalf("the answer waits %d times:\n%s", n, queued(a.s))
	}

	// A manager started on the same history and settings while a card waits puts the same card back on its queue and
	// takes the answer.
	ix = a.askLater(t, a.s)
	next := withSettings(t, a.f, New(Options{Hub: a.f, Store: func() Store { return a.mem }, Logf: testLogf(t), Devices: hookDevs{a.d, a.nudgeRig}}))
	next.fleet.interactions = map[string]*pending{}
	next.publish(context.Background())
	waitFor(t, "the card back on the new manager's queue", func() bool { _, up := next.pendingIx(ix.ID); return up })
	if err := next.resolve(ix.ID, proto.Resolution{Answers: map[string]string{"Push it to beta?": "No"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second answer on its way", func() bool { return strings.Contains(queued(next), "Push it to beta? → No") })
}

// A manager that starts over a store which kept the card pending takes it back with a grace period, as any resumable
// card (restoreInteractions): the record's question claims it, which calls the period off (its timer is stopped), so
// the card is not cancelled under the owner, and its answer still goes.
func TestAskUserNoWaitCardIsClaimedBackAfterARestart(t *testing.T) {
	a := newAskRig(t)
	ix := a.askLater(t, a.s)
	next := withSettings(t, a.f, New(Options{Hub: a.f, Store: func() Store { return a.mem }, Logf: testLogf(t), Devices: hookDevs{a.d, a.nudgeRig}}))
	p := &pending{ix: ix, done: make(chan proto.Resolution, 1)} // as restoreInteractions takes a resumable card back
	next.fleet.mu.Lock()
	next.fleet.interactions = map[string]*pending{ix.ID: p}
	next.orphanLocked(p)
	next.fleet.mu.Unlock()
	next.publish(context.Background())
	waitFor(t, "the card claimed", func() bool {
		next.fleet.mu.Lock()
		defer next.fleet.mu.Unlock()
		return p.orphaned == nil
	})
	if _, up := next.pendingIx(ix.ID); !up {
		t.Fatal("the card is gone though its question claimed it")
	}
	if err := next.resolve(ix.ID, proto.Resolution{Answers: map[string]string{"Push it to beta?": "Yes"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the question gone once its answer went", func() bool { return len(next.pendingQuestions()) == 0 })
}

// askLater has s1 ask without waiting on s and waits until its card is on the queue.
func (a *askRig) askLater(t *testing.T, s *Service) proto.Interaction {
	t.Helper()
	args := strings.Replace(askUserArgs, `{"questions"`, `{"wait":false,"questions"`, 1)
	if _, err := s.toolAskUser(context.Background(), "mac", "s1", json.RawMessage(args)); err != nil {
		t.Fatal(err)
	}
	var ix proto.Interaction
	waitFor(t, "the card on the queue", func() bool {
		for _, q := range s.pendingQuestions() {
			if _, up := s.pendingIx(q.ID); up {
				ix = q
				return true
			}
		}
		return false
	})
	return ix
}

func TestGoldenAskLater(t *testing.T) {
	golden(t, "ask_later.tool_result", prompts.RenderQuestionAskedLater(prompts.QuestionAskedLater{ID: "ix_0a1b2c", Until: "11-08 17:04"}))
	e := newAskTodoEnv(t, nil)
	setSettings(t, e.s, func(v *Settings) { v.Timezone = "UTC" })
	ix := proto.Interaction{ID: "ix_0a1b2c", ToolName: "ask_user", CreatedAt: time.Date(2026, 11, 8, 15, 4, 0, 0, time.UTC),
		ExpiresAt: time.Now().Add(time.Hour), Questions: []proto.Question{{Question: "Merge the release PR now?"}, {Question: "Tag it?"}}}
	ctx := context.Background()
	golden(t, "ask_later.answered", e.s.renderAskLater(ctx, ix, proto.Resolution{Answers: map[string]string{"Merge the release PR now?": "Merge", "Tag it?": "No"}}))
	golden(t, "ask_later.answered_in_part", e.s.renderAskLater(ctx, ix, proto.Resolution{Answers: map[string]string{"Tag it?": "No"}}))
	golden(t, "ask_later.closed", e.s.renderAskLater(ctx, ix, proto.Resolution{Dismiss: true}))
	golden(t, "ask_later.expired_after_user_wrote", prompts.RenderAskLater(prompts.AskLater{When: "11-08 15:04", Questions: []string{"Merge the release PR now?"}, Spoke: true}))
	golden(t, "ask_later.expired", prompts.RenderAskLater(prompts.AskLater{When: "11-08 15:04", Questions: []string{"Merge the release PR now?"},
		Expired: strings.TrimSpace(prompts.RenderQuestionMovedToTodo(prompts.QuestionMovedToTodo{TodoID: "todo_ask_0a1b2c", Title: "Merge the release PR now?"}))}))
}
