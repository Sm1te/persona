package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// The hook inputs below have the shape Claude Code 2.1.319 gave in an isolated run (#1309): the fields and how they
// change from batch to batch are as measured, the words are made up.
const (
	liveSession = "eb3c877f-aa6d-498b-9024-e15027f3897e"
	livePrompt  = "2cd09ccb-fb9e-4567-8581-b54e44a81200"
	liveTurn    = "e8dcd8e3-514a-4de4-ba97-7320b5d6e9b2"
	liveFirst   = "b581460a-c8ed-45d5-b278-263d6aee37e5" // the words before the tool call
	liveSecond  = "3b73f1a1-0c2e-4d55-9a61-7f0f8a1c2d3e" // the words after it: a message of its own
)

// liveEvents records, in the order the node sent them, its session.items (as "items:<the agent's words>") and
// session.delta (as "delta:<text>") events.
type liveEvents struct {
	mu  sync.Mutex
	log []string
}

func (e *liveEvents) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.log...)
}

func (e *liveEvents) deltas() []string {
	var out []string
	for _, s := range e.all() {
		if d, ok := strings.CutPrefix(s, "delta:"); ok {
			out = append(out, d)
		}
	}
	return out
}

func (e *liveEvents) wait(t *testing.T, n int) []string {
	t.Helper()
	waitFor(t, fmt.Sprintf("%d events", n), func() bool { return len(e.all()) >= n })
	time.Sleep(30 * time.Millisecond) // and nothing more after them
	return e.all()
}

func withLiveHub(t *testing.T, n *Node) *liveEvents {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	n2hR, n2hW := io.Pipe()
	h2nR, h2nW := io.Pipe()
	n.peer = rpc.New("n", n2hW)
	hub := rpc.New("h", h2nW)
	ev := &liveEvents{}
	hub.OnEvent = func(event string, params json.RawMessage) {
		var p struct {
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
			Items     []struct {
				Role, Kind, Text string
			} `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		if p.SessionID != liveSession {
			t.Errorf("%s for %s", event, p.SessionID)
		}
		ev.mu.Lock()
		defer ev.mu.Unlock()
		switch event {
		case "session.delta":
			ev.log = append(ev.log, "delta:"+p.Text)
		case "session.items":
			var words []string
			for _, it := range p.Items {
				if it.Role == "assistant" && it.Kind == "text" {
					words = append(words, it.Text)
				}
			}
			ev.log = append(ev.log, "items:"+strings.Join(words, "|"))
		}
	}
	go func() { _ = hub.Serve(ctx, n2hR) }()
	go func() { _ = n.peer.Serve(ctx, h2nR) }()
	t.Cleanup(func() { cancel(); _ = n2hW.Close(); _ = h2nW.Close() })
	return ev
}

// replyNode is a node with the session's transcript (its prompt so far) read once, as a watch's first read does; watched
// says whether the hub watches it. The transcript then goes on only when the test calls n.refresh, as the watch pump would.
func replyNode(t *testing.T, watched bool) (*Node, *transcript.File, string, *liveEvents) {
	t.Helper()
	n := codexTestNode(t)
	home := n.home
	n.runtimes = newRuntimeRegistry()
	dir := mkdir(t, filepath.Join(home, ".claude", "projects", "-work"))
	path := filepath.Join(dir, liveSession+".jsonl")
	writeFile(t, path, livePromptLine("Write six lines about the sea, list the folder, then six about mountains.")+"\n")
	n.paths[liveSession] = path
	f, err := n.file(liveSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Refresh(); err != nil {
		t.Fatal(err)
	}
	if watched {
		n.watches[liveSession] = &watch{cancel: func() {}}
	}
	return n, f, path, withLiveHub(t, n)
}

// stamp is a transcript line's timestamp d from now. The node holds a message to the turn it began in by when its first
// batch came: what the transcript wrote after that is of the turn, or ends it.
func stamp(d time.Duration) string { return time.Now().Add(d).UTC().Format(time.RFC3339Nano) }

func livePromptLine(text string) string {
	b, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"user","uuid":"dcdeede8-0000-4000-8000-000000000001","promptId":%q,"timestamp":%q,"message":{"role":"user","content":%s}}`, livePrompt, stamp(-time.Minute), b)
}

// liveTextLine is a text block as the transcript writes it, its timestamp a moment ago (line timestamps are up to
// seconds older than when the line is written, measured).
func liveTextLine(uuid, msg, text string) string {
	b, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"timestamp":%q,"message":{"id":%q,"role":"assistant","content":[{"type":"text","text":%s}]}}`, uuid, stamp(-time.Second), msg, b)
}

func liveToolLines() string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"72a19147-0000-4000-8000-000000000002","timestamp":%q,"message":{"id":"msg_01first","role":"assistant","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls -la"}}]}}`, stamp(-time.Second)) + "\n" +
		fmt.Sprintf(`{"type":"user","uuid":"aecfac56-0000-4000-8000-000000000003","promptId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"total 0"}]}}`, livePrompt, stamp(0))
}

// endLine is a line that ends a turn, written now: an interrupt (Esc, or Stop on the phone: no Stop hook), the model
// error a turn failed on (StopFailure, not Stop), the turn's duration (after its Stop), a background task waking the
// agent for a new turn.
func endLine(kind string) string { return endLineAt(kind, 10*time.Millisecond) }

func endLineAt(kind string, d time.Duration) string {
	ts := stamp(d)
	switch kind {
	case "interrupt":
		return fmt.Sprintf(`{"type":"user","uuid":"5be0c0de-0000-4000-8000-000000000010","promptId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`, livePrompt, ts)
	case "api error":
		return fmt.Sprintf(`{"type":"assistant","uuid":"5be0c0de-0000-4000-8000-000000000011","timestamp":%q,"isApiErrorMessage":true,"message":{"id":"0b6d5a8e-0000-4000-8000-000000000012","model":"<synthetic>","role":"assistant","content":[{"type":"text","text":"API Error: 529 Overloaded"}]}}`, ts)
	case "gave up":
		return fmt.Sprintf(`{"type":"system","subtype":"api_error","uuid":"5be0c0de-0000-4000-8000-000000000013","timestamp":%q,"error":{"message":"Overloaded"},"retryAttempt":10,"maxRetries":10}`, ts)
	case "turn_duration":
		return fmt.Sprintf(`{"type":"system","subtype":"turn_duration","uuid":"5be0c0de-0000-4000-8000-000000000014","parentUuid":"ab922be1-0000-4000-8000-000000000001","timestamp":%q,"durationMs":9000}`, ts)
	case "wake":
		return fmt.Sprintf(`{"type":"user","uuid":"5be0c0de-0000-4000-8000-000000000015","timestamp":%q,"message":{"role":"user","content":"<task-notification><summary>Background command done</summary></task-notification>"}}`, ts)
	}
	panic(kind)
}

// batch is a MessageDisplay input as Claude Code gives it.
func batch(msg string, index int, final bool, delta string) []byte {
	b, _ := json.Marshal(map[string]any{
		"session_id": liveSession, "transcript_path": "/home/me/.claude/projects/-work/" + liveSession + ".jsonl",
		"cwd": "/home/me/work", "prompt_id": livePrompt, "hook_event_name": "MessageDisplay", "turn_id": liveTurn,
		"message_id": msg, "index": index, "final": final, "delta": delta,
	})
	return b
}

func turnHook(n *Node, event string) {
	raw, _ := json.Marshal(map[string]any{"session_id": liveSession, "prompt_id": livePrompt, "hook_event_name": event,
		"permission_mode": "auto", "last_assistant_message": "Mountain 1."})
	_, _ = n.handleHook(context.Background(), sockRequest{Kind: "hook", Input: raw})
}

// A reply that ends in a tool call: its lines stream as Claude shows them, a growing session.delta, and stay while the
// tool runs (its text is written only with the tool's result); the items that bring them replace them by prefix, the
// next message streams on its own, and the Stop leaves nothing behind.
func TestLiveReplyStreamsUntilTheTranscriptHasIt(t *testing.T) {
	n, f, path, ev := replyNode(t, true)
	first := "Sea 1: waves.\nSea 2: tides.\nSea 3: salt."
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	n.displayHook(batch(liveFirst, 1, false, "Sea 2: tides.\n"))
	n.displayHook(batch(liveFirst, 2, true, "Sea 3: salt.")) // the last line comes with final, without its newline
	got := ev.wait(t, 3)
	want := []string{"delta:Sea 1: waves.\n", "delta:Sea 1: waves.\nSea 2: tides.\n", "delta:" + first}
	if strings.Join(got, "‖") != strings.Join(want, "‖") {
		t.Fatalf("while Claude shows it: %q, want %q", got, want)
	}

	// The tool runs: nothing comes, the live text stays. Then the text, the call and its result land together.
	appendLines(t, path, liveTextLine("ab922be1-0000-4000-8000-000000000001", "msg_01first", first), liveToolLines())
	n.refresh(liveSession, f)
	got = ev.wait(t, 5)
	if got[3] != "items:"+first || got[4] != "delta:" {
		t.Fatalf("after the transcript: %q, want the items, then the live text cleared", got[3:])
	}

	n.displayHook(batch(liveSecond, 0, false, "Mountain 1: peaks.\n"))
	n.displayHook(batch(liveSecond, 1, true, "Mountain 2: snow."))
	got = ev.wait(t, 7)
	if got[5] != "delta:Mountain 1: peaks.\n" || got[6] != "delta:Mountain 1: peaks.\nMountain 2: snow." {
		t.Fatalf("the message after the tool: %q", got[5:])
	}
	appendLines(t, path, liveTextLine("57662874-0000-4000-8000-000000000004", "msg_01second", "Mountain 1: peaks.\nMountain 2: snow."))
	n.refresh(liveSession, f)
	turnHook(n, "Stop")
	got = ev.wait(t, 9)
	if got[7] != "items:Mountain 1: peaks.\nMountain 2: snow." || got[8] != "delta:" || len(got) != 9 {
		t.Fatalf("the end: %q", got[7:])
	}
}

// Up to three hooks run at once, so batches come out of order and a slow one late: the node puts them in order and
// takes each once; a batch of a message a later one replaced is dropped.
func TestLiveReplyOrdersBatchesAndDropsRepeats(t *testing.T) {
	n, _, _, ev := replyNode(t, true)
	n.displayHook(batch(liveFirst, 1, false, "N-2\nN-3\n"))
	n.displayHook(batch(liveFirst, 2, false, "N-4\n"))
	time.Sleep(50 * time.Millisecond)
	if d := ev.deltas(); len(d) != 0 {
		t.Fatalf("batches after a missing one were shown: %q", d)
	}
	n.displayHook(batch(liveFirst, 0, false, "N-1\n"))
	n.displayHook(batch(liveFirst, 0, false, "N-1\n")) // the same again
	n.displayHook(batch(liveFirst, 2, false, "N-4\n"))
	ev.wait(t, 1)
	if d := ev.deltas(); len(d) != 1 || d[0] != "N-1\nN-2\nN-3\nN-4\n" {
		t.Fatalf("deltas %q, want the four lines in order, once", d)
	}
	n.displayHook(batch(liveSecond, 0, false, "Next.\n"))
	n.displayHook(batch(liveFirst, 3, true, "N-5")) // the first message's last batch, after the second began
	got := ev.wait(t, 2)
	if len(got) != 2 || got[1] != "delta:Next.\n" {
		t.Fatalf("after the next message began: %q", got)
	}
}

// The transcript can have a message before its batches come (it arrived in one piece): they are never shown.
func TestLiveReplyNeverShowsWhatTheTranscriptHas(t *testing.T) {
	n, f, path, ev := replyNode(t, true)
	whole := "Mountain 1: peaks.\nMountain 2: snow.\nMountain 3: pines."
	appendLines(t, path, liveTextLine("57662874-0000-4000-8000-000000000004", "msg_01second", whole))
	n.refresh(liveSession, f)
	n.displayHook(batch(liveSecond, 0, false, "Mountain 1: peaks.\n"))
	n.displayHook(batch(liveSecond, 1, true, "Mountain 2: snow.\nMountain 3: pines."))
	if got := ev.wait(t, 1); len(got) != 1 || got[0] != "items:"+whole {
		t.Fatalf("got %q, want only the items", got)
	}
}

// A Stop can come before the turn's last batch (a slow hook): the turn takes no more, and what it showed goes a moment
// after the Stop even when the transcript is not read meanwhile.
func TestLiveReplyEndsWithTheTurn(t *testing.T) {
	defer func(e, g time.Duration) { liveCheckEvery, liveStopGrace = e, g }(liveCheckEvery, liveStopGrace)
	liveCheckEvery, liveStopGrace = 20*time.Millisecond, 50*time.Millisecond
	n, _, _, ev := replyNode(t, true)
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	ev.wait(t, 1)
	turnHook(n, "Stop")
	n.displayHook(batch(liveFirst, 1, true, "Sea 2: tides."))
	got := ev.wait(t, 2)
	if len(got) != 2 || got[1] != "delta:" {
		t.Fatalf("after the Stop: %q, want the live text cleared and the late batch dropped", got)
	}
}

// A new prompt ends a message of the turn before (one interrupted with Esc gets no Stop).
func TestLiveReplyEndsAtTheNextPrompt(t *testing.T) {
	n, _, _, ev := replyNode(t, true)
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	ev.wait(t, 1)
	raw, _ := json.Marshal(map[string]any{"session_id": liveSession, "prompt_id": "0965f16d-0000-4000-8000-000000000009",
		"hook_event_name": "UserPromptSubmit", "prompt": "Something else."})
	_, _ = n.handleHook(context.Background(), sockRequest{Kind: "hook", Input: raw})
	if got := ev.wait(t, 2); got[1] != "delta:" {
		t.Fatalf("after the next prompt: %q", got)
	}
}

// A session nobody watches is not told; once nobody watches any more, what the hub keeps of it is cleared (it would
// hand it to the next page that opens the session).
func TestLiveReplyOnlyWhileWatched(t *testing.T) {
	n, _, _, ev := replyNode(t, false)
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	time.Sleep(50 * time.Millisecond)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("an unwatched session was told: %q", got)
	}
	n.watches[liveSession] = &watch{cancel: func() {}}
	n.displayHook(batch(liveFirst, 1, false, "Sea 2: tides.\n"))
	ev.wait(t, 1)
	n.unwatch(liveSession)
	if got := ev.wait(t, 2); got[0] != "delta:Sea 1: waves.\nSea 2: tides.\n" || got[1] != "delta:" {
		t.Fatalf("got %q", got)
	}
}

// A subagent's batches, and the headless assistant's (it streams its own), are not the session's reply.
func TestLiveReplyOnlyAnAgentsOwn(t *testing.T) {
	n, _, _, ev := replyNode(t, true)
	var sub map[string]any
	_ = json.Unmarshal(batch(liveFirst, 0, false, "Sub 1.\n"), &sub)
	sub["agent_id"] = "a1b2c3"
	raw, _ := json.Marshal(sub)
	n.displayHook(raw)
	n.procs[liveSession] = &assistantProc{}
	n.displayHook(batch(liveSecond, 0, false, "Mine.\n"))
	time.Sleep(50 * time.Millisecond)
	if got := ev.all(); len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

// The hook is answered before the node does anything with its lines: Claude shows them only once it is done.
func TestDisplayHookIsAnsweredAtOnce(t *testing.T) {
	n, _, _, ev := replyNode(t, true)
	mine, theirs := net.Pipe()
	defer mine.Close()
	mu := n.pushLock(liveSession)
	mu.Lock() // a transcript read under way: the batch waits for it, the hook does not
	go n.serveSock(context.Background(), theirs)
	line, _ := json.Marshal(map[string]any{"kind": "hook.display", "input": json.RawMessage(batch(liveFirst, 0, false, "Sea 1: waves.\n"))})
	_ = mine.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := mine.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	answer, err := bufio.NewReader(mine).ReadString('\n')
	if err != nil || strings.TrimSpace(answer) != `{"ok":true}` {
		t.Fatalf("answer %q %v", answer, err)
	}
	mu.Unlock()
	if got := ev.wait(t, 1); got[0] != "delta:Sea 1: waves.\n" {
		t.Fatalf("got %q", got)
	}
}

// A turn ended without a Stop (Esc or the phone's stop: an interrupt; a model error: StopFailure instead) or past its
// Stop leaves no live text behind: the transcript line that ends it, written after the message began, ends the
// message too, and a page that opens the session again later does not get it back.
func TestLiveReplyEndsWhenTheTranscriptEndsTheTurn(t *testing.T) {
	for _, end := range []string{"interrupt", "api error", "gave up", "turn_duration"} {
		t.Run(end, func(t *testing.T) {
			n, f, path, ev := replyNode(t, true)
			n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
			ev.wait(t, 1)
			appendLines(t, path, endLine(end))
			n.refresh(liveSession, f)
			got := ev.wait(t, 3)
			if len(got) != 3 || got[2] != "delta:" {
				t.Fatalf("after the turn ended: %q", got)
			}
			n.unwatch(liveSession)
			rewatch(n)
			n.displayHook(batch(liveFirst, 1, false, "Sea 2: tides.\n")) // a batch still under way
			time.Sleep(50 * time.Millisecond)
			if got := ev.all(); len(got) != 3 {
				t.Fatalf("the session opened again: %q", got)
			}
		})
	}
}

// The turn ends while nobody has the session open (the node reads no transcript then): opened again, the watch's first
// read ends the message before anything is told.
func TestLiveReplyNotBackAfterItsTurnEndedUnwatched(t *testing.T) {
	n, _, path, ev := replyNode(t, true)
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	ev.wait(t, 1)
	n.unwatch(liveSession)
	ev.wait(t, 2)
	appendLines(t, path, endLine("interrupt"))
	rewatch(n)
	time.Sleep(50 * time.Millisecond)
	if got := ev.all(); len(got) != 2 || got[1] != "delta:" {
		t.Fatalf("opened again after the turn ended: %q", got)
	}
}

// What came before the message began is the turn before's: the line that ended it, read only after the next prompt's
// first batch came (the node reads the transcript every 250 ms), does not end the new message.
func TestLiveReplyOutlivesTheTurnBefore(t *testing.T) {
	n, f, path, ev := replyNode(t, true)
	appendLines(t, path, endLineAt("interrupt", -2*time.Second))
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	ev.wait(t, 1)
	n.refresh(liveSession, f)
	n.displayHook(batch(liveFirst, 1, false, "Sea 2: tides.\n"))
	if got := ev.wait(t, 3); got[2] != "delta:Sea 1: waves.\nSea 2: tides.\n" {
		t.Fatalf("got %q", got)
	}
}

// Closed and opened again while Claude still shows the message: the page gets it back, checked against the transcript.
func TestLiveReplyBackWhenOpenedAgain(t *testing.T) {
	n, _, _, ev := replyNode(t, true)
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	ev.wait(t, 1)
	n.unwatch(liveSession)
	ev.wait(t, 2)
	n.displayHook(batch(liveFirst, 1, false, "Sea 2: tides.\n"))
	rewatch(n)
	if got := ev.wait(t, 3); got[2] != "delta:Sea 1: waves.\nSea 2: tides.\n" {
		t.Fatalf("opened again: %q", got)
	}
}

// A message whose last batch came and that the transcript has whole is done: a background task waking the agent after
// it (a new exchange, which has none of its words) does not bring it back.
func TestLiveReplyDoneOnceLandedWhole(t *testing.T) {
	n, f, path, ev := replyNode(t, true)
	whole := "Sea 1: waves.\nSea 2: tides."
	n.displayHook(batch(liveFirst, 0, false, "Sea 1: waves.\n"))
	n.displayHook(batch(liveFirst, 1, true, "Sea 2: tides."))
	ev.wait(t, 2)
	appendLines(t, path, liveTextLine("ab922be1-0000-4000-8000-000000000001", "msg_01first", whole))
	n.refresh(liveSession, f)
	ev.wait(t, 4)
	// the Stop of a turn the batches do not name (a wake's), then the wake
	raw, _ := json.Marshal(map[string]any{"session_id": liveSession, "prompt_id": "0965f16d-0000-4000-8000-00000000000a", "hook_event_name": "Stop"})
	_, _ = n.handleHook(context.Background(), sockRequest{Kind: "hook", Input: raw})
	appendLines(t, path, endLine("wake"))
	n.refresh(liveSession, f)
	got := ev.wait(t, 5)
	if len(got) != 5 || got[3] != "delta:" || !strings.HasPrefix(got[4], "items:") {
		t.Fatalf("got %q, want the reply once, then only the wake's items", got)
	}
}

// The message after a tool can begin like the one before it ("I'll …" twice): the one before's words, which land
// first, do not take it, and once its own land they replace it.
func TestLiveReplyNextMessageBeginsAlike(t *testing.T) {
	n, f, path, ev := replyNode(t, true)
	first := "I'll update the names.\nThen I'll run the tests."
	n.displayHook(batch(liveFirst, 0, false, "I'll update the names.\n"))
	n.displayHook(batch(liveFirst, 1, true, "Then I'll run the tests."))
	ev.wait(t, 2)
	appendLines(t, path, liveTextLine("ab922be1-0000-4000-8000-000000000001", "msg_01first", first), liveToolLines())
	n.refresh(liveSession, f)
	ev.wait(t, 4)
	n.displayHook(batch(liveSecond, 0, false, "I'll update the names.\n"))
	got := ev.wait(t, 5)
	if got[4] != "delta:I'll update the names.\n" {
		t.Fatalf("the next message: %q", got[4:])
	}
	second := "I'll update the names.\nThe tests pass."
	n.displayHook(batch(liveSecond, 1, true, "The tests pass."))
	ev.wait(t, 6)
	appendLines(t, path, liveTextLine("57662874-0000-4000-8000-000000000004", "msg_01second", second))
	n.refresh(liveSession, f)
	if got := ev.wait(t, 8); got[6] != "items:"+second || got[7] != "delta:" {
		t.Fatalf("after it landed: %q", got[6:])
	}
}

// rewatch is a watch starting again (watchFor): the transcript read, then the live text checked against it.
func rewatch(n *Node) {
	mu := n.pushLock(liveSession)
	mu.Lock()
	defer mu.Unlock()
	if f := n.files[liveSession]; f != nil {
		_, _ = f.Refresh()
	}
	n.mu.Lock()
	n.watches[liveSession] = &watch{cancel: func() {}}
	n.mu.Unlock()
	n.liveRecheck(liveSession)
}

func TestUnlanded(t *testing.T) {
	long := strings.Repeat("Line of a long reply.\n", 30)
	for _, c := range []struct {
		name, live string
		words      [][]landed
		prev       string
		want       string
		over       bool
	}{
		{name: "nothing landed", live: "Line 1.\nLine 2.\n", want: "Line 1.\nLine 2.\n"},
		{name: "all of it landed", live: "Line 1.\nLine 2.\n", words: [][]landed{{{text: "Line 1.\nLine 2.\nLine 3."}}}},
		{name: "landed whole, the final batch too", live: "Line 1.\nLine 2.", words: [][]landed{{{text: "Line 1.\nLine 2."}}}},
		{name: "an earlier message only", live: "Next 1.\n", words: [][]landed{{{text: "Line 1.\nLine 2."}}}, prev: "Line 1.\nLine 2.", want: "Next 1.\n"},
		{name: "its first block landed", live: "Block one.\n\nBlock two.\n", words: [][]landed{{{text: "Block one."}}}, want: "Block two.\n"},
		{name: "both blocks landed", live: "Block one.Block two.", words: [][]landed{{{text: "Block one."}, {text: "Block two."}}}},
		{name: "a later message landed after it", live: "Line 1.\n", words: [][]landed{{{text: "Line 1.\nLine 2."}}, {{text: "Next."}}}, over: true},
		// the message before begins alike: its words land first, and are not this one's
		{name: "begins like the one before", live: "I'll update the names.\n", words: [][]landed{{{text: "I'll update the names.\nThen I'll run the tests."}}},
			prev: "I'll update the names.\nThen I'll run the tests.", want: "I'll update the names.\n"},
		{name: "the one before said the same", live: "Done.", words: [][]landed{{{text: "Done."}}}, prev: "Done.", want: "Done."},
		{name: "the same, both landed", live: "Done.", words: [][]landed{{{text: "Done."}}, {{text: "Done."}}}, prev: "Done."},
		// an item is the start of the live text only where a word ends
		{name: "not in the middle of a word", live: "好的，测试已通过。\n", words: [][]landed{{{text: "好的"}}}, want: "好的，测试已通过。\n"},
		{name: "not in the middle of a word, the one before unknown", live: "OK, the tests pass.\n", words: [][]landed{{{text: "OK"}}}, want: "OK, the tests pass.\n"},
		// a cut item holds its whole block (size bytes): only what goes past it is still to come
		{name: "a cut item holds what it was cut from", live: long, words: [][]landed{{{text: long[:40], size: len(long) - 1}}}},
		{name: "past a cut item", live: long + "\nAnother block.\n", words: [][]landed{{{text: long[:40], size: len(long)}}}, want: "Another block.\n"},
		{name: "blank", live: "\n\n"},
		{name: "as it came while nothing landed", live: "\n\nLine 1.\n", want: "\n\nLine 1.\n"},
	} {
		if got, over := unlanded(c.live, c.words, c.prev); got != c.want || over != c.over {
			t.Errorf("%s: %q over %v, want %q over %v", c.name, got, over, c.want, c.over)
		}
	}
}

// MessageDisplay is a plugin of its own, written beside the Agents' hooks: the hook command that passes the lines on
// (async, a short timeout), in exec form so no shell starts for each batch. No other plugin or settings file has it:
// the assistant streams on its own, and the settings file is for a Claude Code without the event.
func TestLiveRepliesPlugin(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(mkdir(t, filepath.Join(home, "my apps")), "personad") // a space: exec form takes the path as it is
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	n := &Node{home: home, dir: mkdir(t, filepath.Join(home, ".persona")), bin: bin}
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if b, err := os.ReadFile(filepath.Join(n.dir, liveRepliesDir, ".claude-plugin", "plugin.json")); err != nil || json.Unmarshal(b, &manifest) != nil ||
		manifest["name"] != liveRepliesPluginName {
		t.Fatalf("manifest %v (%v)", manifest, err)
	}
	for _, f := range []string{liveRepliesDir + "/hooks/hooks.json", "claude-hooks/hooks/hooks.json", "assistant-hooks/hooks/hooks.json",
		"claude-settings.json", "assistant-settings.json"} {
		hooks := readSettings(t, filepath.Join(n.dir, f))["hooks"].(map[string]any)
		md, ok := hooks["MessageDisplay"].([]any)
		if live := f == liveRepliesDir+"/hooks/hooks.json"; ok != live || live && len(hooks) != 1 {
			t.Errorf("%s: hooks %v", f, hooks)
			continue
		}
		if !ok {
			continue
		}
		h := md[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		// no shell to put PERSONA_HOME in front: the node's directory is an argument (agentenv.go)
		if args, _ := h["args"].([]any); h["command"] != bin || len(args) != 3 || args[0] != "hook" || args[1] != nodeclient.HomeFlag ||
			args[2] != n.dir || h["async"] != true || h["timeout"] != float64(1) {
			t.Errorf("the hook %v", h)
		}
	}
	// The doctor finds the program of the exec-form hook, space and all.
	if c := checkCommandPaths(n.dir); c.Status != DoctorOK {
		t.Fatalf("command paths: %+v", c)
	}
	// It is part of the Agents' hooks for the doctor: current now, stale when it goes, and its command checked.
	if c := checkGenerated(DoctorOptions{Dir: n.dir, Bin: n.bin}); c[slices.IndexFunc(c, func(c DoctorCheck) bool { return c.Name == "claude-hooks" })].Status != DoctorOK {
		t.Fatalf("doctor: %+v", c)
	}
	if err := os.RemoveAll(filepath.Join(n.dir, liveRepliesDir)); err != nil {
		t.Fatal(err)
	}
	c := checkGenerated(DoctorOptions{Dir: n.dir, Bin: n.bin})
	if got := c[slices.IndexFunc(c, func(c DoctorCheck) bool { return c.Name == "claude-hooks" })]; got.Status == DoctorOK ||
		!slices.Contains(got.Items, "missing "+liveRepliesDir+"/hooks/hooks.json") {
		t.Fatalf("doctor without the plugin: %+v", got)
	}
}

// The machine's setting (Settings › Claude Code › Live replies, node.configure claude_live_replies) decides whether an
// Agent's start loads the live replies plugin. On when nothing said it (a new node, an older hub that never says, a
// configure about something else); off and back on as told. A start whose hooks go in --settings (a claude that loads
// no plugins) never has it, nor one whose plugin is not written.
func TestLiveRepliesSetting(t *testing.T) {
	n, home := testNode(t)
	n.dir, n.bin = personaDir(home), "/x/personad"
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	hooks, live := filepath.Join(n.dir, "claude-hooks"), filepath.Join(n.dir, liveRepliesDir)
	line := func() string {
		return n.claudeCommandLine(claudeLaunch{Launcher: "claude", Hooks: hooks, Display: n.liveRepliesPlugin(hooks)}, "--session-id "+startID, shellQuote)
	}
	on := func(want bool, why string) {
		t.Helper()
		got := n.liveRepliesPlugin(hooks)
		if (got == live) != want || (got != "" && got != live) {
			t.Fatalf("%s: the live replies plugin %q, want on %v", why, got, want)
		}
		if strings.HasSuffix(line(), " --plugin-dir "+shellQuote(live)) != want {
			t.Fatalf("%s: %s", why, line())
		}
	}
	on(true, "nothing said")
	configure := func(params map[string]any) {
		t.Helper()
		if _, err := request(t, n, "node.configure", params); err != nil {
			t.Fatal(err)
		}
	}
	configure(map[string]any{"herdr_autostart": true})
	on(true, "a configure about something else")
	configure(map[string]any{"claude_live_replies": false})
	on(false, "turned off")
	configure(map[string]any{"botmux": true})
	on(false, "off, then a configure about something else")
	configure(map[string]any{"claude_live_replies": true})
	on(true, "turned on again")
	if got := n.liveRepliesPlugin(""); got != "" {
		t.Fatalf("hooks in --settings: %q", got)
	}
	if err := os.RemoveAll(live); err != nil {
		t.Fatal(err)
	}
	on(false, "the plugin not written")
}

// Turning the setting off or on gives herdr the resume command of each Claude Persona started there again, with the
// live replies plugin dropped or added, so a herdr restore starts it the way a start now would. Another agent, and the
// same setting again, report nothing.
func TestLiveRepliesSettingMovesTheResumeCommand(t *testing.T) {
	n, f, home := herdrNode(t)
	n.dir = personaDir(home)
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	launch := n.hosts.Get(host.Herdr).(*herdrHost)
	hooks, live := filepath.Join(n.dir, "claude-hooks"), filepath.Join(n.dir, liveRepliesDir)
	n.holdResume(context.Background(), launch, "w1:p2", startID, claudeLaunch{Launcher: "claude", Hooks: hooks, Display: n.liveRepliesPlugin(hooks)})
	n.holdResume(context.Background(), launch, "w1:p4", sidB, agentLaunch{Agent: "codex", Launcher: "cx"})
	waitCalls(t, f, "pane.report_agent_session", 2)
	cmd := func(display bool) string {
		c := "HERDR_AGENT=claude claude --resume " + startID + " --mcp-config " + filepath.Join(n.dir, "mcp.json") + " --allowedTools " + ownToolsRule + " --plugin-dir " + hooks
		if display {
			c += " --plugin-dir " + live
		}
		return inAgentEnv(n.dir, c, dquote)
	}
	quoted := func(display bool) string { b, _ := json.Marshal(cmd(display)); return string(b) }
	reported := func(display bool) {
		t.Helper()
		waitCalls(t, f, "pane.report_agent_session", 1)
		time.Sleep(50 * time.Millisecond)
		got := f.trace("pane.report_agent_session")
		if len(got) != 1 || !strings.Contains(got[0], `"pane_id":"w1:p2"`) || !strings.Contains(got[0], `["eval",`+quoted(display)+`]`) {
			t.Fatalf("reports %v, want the resume command %s", got, cmd(display))
		}
	}
	if got := f.trace("pane.report_agent_session"); !strings.Contains(strings.Join(got, "\n"), quoted(true)) {
		t.Fatalf("at the start: %v", got)
	}

	f.reset()
	if _, err := request(t, n, "node.configure", map[string]any{"claude_live_replies": false}); err != nil {
		t.Fatal(err)
	}
	reported(false)

	f.reset()
	if _, err := request(t, n, "node.configure", map[string]any{"claude_live_replies": false}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := f.trace("pane.report_agent_session"); len(got) != 0 {
		t.Fatalf("the same setting again: %v", got)
	}

	f.reset()
	if _, err := request(t, n, "node.configure", map[string]any{"claude_live_replies": true}); err != nil {
		t.Fatal(err)
	}
	reported(true)
}

// The setting turned off and on again before herdr answered the first report: each report waits for the pane's report
// in flight and then says the pane's newest command, so herdr cannot be left with the one without the live replies
// plugin (two reports racing to herdr could arrive in either order, and nothing would report again).
func TestLiveRepliesSettingQuickSwitchLeavesTheNewestResumeCommand(t *testing.T) {
	n, f, home := herdrNode(t)
	n.dir = personaDir(home)
	if err := n.writeClaudeConfig(); err != nil {
		t.Fatal(err)
	}
	launch := n.hosts.Get(host.Herdr).(*herdrHost)
	hooks, live := filepath.Join(n.dir, "claude-hooks"), filepath.Join(n.dir, liveRepliesDir)
	n.holdResume(context.Background(), launch, "w1:p2", startID, claudeLaunch{Launcher: "claude", Hooks: hooks, Display: n.liveRepliesPlugin(hooks)})
	waitCalls(t, f, "pane.report_agent_session", 1)
	f.reset()

	inFlight := n.resumes.lock("w1:p2") // a report of the pane still waiting on herdr
	for _, on := range []bool{false, true} {
		if _, err := request(t, n, "node.configure", map[string]any{"claude_live_replies": on}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond) // both reports are waiting their turn
	if got := f.trace("pane.report_agent_session"); len(got) != 0 {
		t.Fatalf("reported while another report of the pane was in flight: %v", got)
	}
	inFlight()
	waitCalls(t, f, "pane.report_agent_session", 2)
	time.Sleep(50 * time.Millisecond)
	got := f.trace("pane.report_agent_session")
	if len(got) != 2 {
		t.Fatalf("reports %v, want 2", got)
	}
	for _, r := range got {
		if !strings.Contains(r, " --plugin-dir "+live) {
			t.Errorf("a report without the live replies plugin, the setting being on: %s", r)
		}
	}
}
