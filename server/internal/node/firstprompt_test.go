package node

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// promptLands makes the transcript of the session claude was started with (--session-id) get the user's message
// after the try-th agent.prompt (1 when omitted), as Claude writes it when it takes a message; earlier ones are dropped.
func promptLands(t *testing.T, f *fakeHerdr, home, cwd string, try ...int) {
	t.Helper()
	at := 1
	if len(try) > 0 {
		at = try[0]
	}
	var mu sync.Mutex
	prompts, id := 0, ""
	f.after(func(c herdrCall) {
		mu.Lock()
		defer mu.Unlock()
		switch c.Method {
		case "pane.send_input":
			if m := sessionIDArg.FindStringSubmatch(str(c.Params["text"])); m != nil {
				id = m[1]
			}
		case "agent.prompt":
			if prompts++; prompts == at {
				writeUserLine(t, home, cwd, id, str(c.Params["text"]))
			}
		}
	})
}

var sessionIDArg = regexp.MustCompile(`--session-id (\S+)`)

// startedID is the session id the start typed (--session-id).
func startedID(f *fakeHerdr) string {
	for _, c := range f.calls() {
		if m := sessionIDArg.FindStringSubmatch(str(c.Params["text"])); c.Method == "pane.send_input" && m != nil {
			return m[1]
		}
	}
	return ""
}

func writeUserLine(t *testing.T, home, cwd, id, text string) {
	writeTranscript(t, home, cwd, id, time.Now(),
		`{"type":"user","timestamp":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","cwd":"`+cwd+`","message":{"role":"user","content":"`+text+`"}}`)
}

// updatedScreen is a fresh Claude that updated itself on start and shows the auto mode notice above its empty box
// (2026-11-05: the first message typed then was dropped, #2423).
func updatedScreen(t *testing.T) (plain, ansi string) {
	empty := screenFixture(t, "claude-2.1.312-empty-162.txt")
	notice := "\n  ✓ Updated to latest. Got 3 features, 14 bugfixes\n\n" +
		"  Auto mode lets Claude handle permission prompts automatically\n"
	lines := strings.SplitN(empty, "\n", 4)
	return strings.Join(lines[:3], "\n") + notice + lines[3], screenFixture(t, "claude-2.1.312-empty-162.ansi")
}

// startPane makes herdr open pane w1:p2 for a start into workspace w1, with a claude that is idle there.
func startPane(t *testing.T, f *fakeHerdr, plain, ansi string) {
	f.reply("tab.create", map[string]any{"tab": map[string]any{"tab_id": "w1:t2"}, "root_pane": map[string]any{"pane_id": "w1:p2"}})
	startHerdr(f, "w1:p2", proto.StatusIdle)
	f.setScreens("w1:p2", plain, ansi)
}

// The first message typed while Claude finishes drawing its update notice is dropped (the transcript never gets it,
// the box stays empty): once the box is ready again it is typed a second time, and that one goes in.
func TestStartTypesADroppedFirstMessageAgain(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	plain, ansi := updatedScreen(t)
	startPane(t, f, plain, ansi)
	promptLands(t, f, home, cwd, 2)

	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	if _, ok := n.lostFirst.get(startedID(f)); ok {
		t.Error("marked as lost")
	}
	prompt := call("agent.prompt", map[string]any{"target": "w1:p2", "text": "fix the build"})
	if got := f.trace("agent.prompt", "pane.send_keys"); len(got) != 2 || got[0] != prompt || got[1] != prompt {
		t.Errorf("typed %v", got)
	}
	// Typed again only after the box was seen ready (twice) and empty.
	trace := squeeze(f.trace())
	i := strings.Index(strings.Join(trace, "\n"), prompt)
	rest := strings.Join(trace, "\n")[i+len(prompt):]
	if !strings.Contains(rest, agentGet("w1:p2")+"\n"+readVisible("w1:p2")) || !strings.Contains(rest, readANSI("w1:p2")+"\n"+prompt) {
		t.Errorf("not checked before typing again:\n%s", rest)
	}
}

// A first message that stays in the box (typed, not submitted) gets one Enter, and is not typed a second time.
func TestStartEntersAFirstMessageLeftInTheBox(t *testing.T) {
	withSubmitGrace(t, 100*time.Millisecond)
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	typedTxt, typedANSI := screenFixture(t, "claude-2.1.312-typed-162.txt"), screenFixture(t, "claude-2.1.312-typed-162.ansi")
	f.after(func(c herdrCall) {
		switch {
		case c.Method == "agent.prompt": // the box keeps showing it ("fix the flaky test")
			f.setScreens("w1:p2", typedTxt, typedANSI)
		case isKeys("enter")(c):
			writeUserLine(t, home, cwd, startedID(f), "fix the flaky test")
		}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the flaky test"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	assertTrace(t, f.trace("agent.prompt", "pane.send_keys"), []string{
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "fix the flaky test"}),
		sendKeys("w1:p2", "enter"),
	})
	if _, ok := n.lostFirst.get(startedID(f)); ok {
		t.Error("marked as lost")
	}
}

// Image and long-text first prompts are hidden behind chips, not visible words. Simulate asynchronous paste
// processing and swallowed Enter keys through session.start; the body is pasted once, and only Enter is retried.
func TestStartSubmitsImageAndLongPaste(t *testing.T) {
	long := "First prompt submit regression.\n" + strings.Repeat("Test-only multiline context.\n", 26)
	for _, c := range []struct {
		name, prompt, draft, why                       string
		showAfter, takeEnter, replaceAfter, imageBytes int
		clearOnEnter, submitted                        bool
	}{
		{name: "large image already submitted", prompt: long, imageBytes: 2 << 20, submitted: true},
		{name: "large image after recovery Enter", prompt: long, imageBytes: 2 << 20, draft: "[Image #1][Pasted text #2 +26 lines]", takeEnter: 1},
		{name: "delayed image and long text", prompt: long, draft: "[Image #1][Pasted text #2 +26 lines]", showAfter: 5, takeEnter: 1},
		{name: "swallowed recovery Enter", prompt: long, draft: "[Image #1][Pasted text #2 +26 lines]", takeEnter: 2},
		{name: "short text with image path lines", prompt: "Check this test image", draft: "[Image #1][Pasted text #2 +3 lines]", takeEnter: 1},
		{name: "owned paste never submits", prompt: long, draft: "[Image #1][Pasted text #2 +26 lines]", why: lostDraft},
		{name: "another visible draft", prompt: long, draft: "unrelated user draft", why: lostOther},
		{name: "another paste before first read", prompt: long, draft: "[Pasted text #3 +26 lines]", why: lostOther},
		{name: "placeholder with unrelated suffix", prompt: long, draft: "[Image #1][Pasted text #2 +26 lines] unrelated user draft", why: lostOther},
		{name: "user replaces long paste during grace", prompt: long, draft: "[Image #1][Pasted text #2 +26 lines]", replaceAfter: 1, why: lostOther},
		{name: "Enter clears box but receipt is late", prompt: long, draft: "[Image #1][Pasted text #2 +26 lines]", clearOnEnter: true, why: lostTimeout},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, f, home := herdrNode(t)
			withSubmitGrace(t, 60*time.Millisecond)
			withFirstPromptWaits(t, 500*time.Millisecond, 3*time.Second)
			cwd := mkdir(t, filepath.Join(home, "proj"))
			empty, emptyANSI := screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi")
			startPane(t, f, empty, emptyANSI)
			draft := strings.Replace(screenFixture(t, "claude-2.1.312-typed-162.txt"), "fix the flaky test", c.draft, 1)
			draftANSI := strings.Replace(screenFixture(t, "claude-2.1.312-typed-162.ansi"), "fix the flaky test", c.draft, 1)
			prompt := c.prompt + "\n\n<attached_images>\n/tmp/test-image.png\n</attached_images>"
			imageData := "aW1hZ2U="
			if c.imageBytes > 0 {
				imageData = base64.StdEncoding.EncodeToString(make([]byte, c.imageBytes))
			}
			land := func() {
				line, err := json.Marshal(map[string]any{
					"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "cwd": cwd,
					"message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": imageData}},
						map[string]any{"type": "text", "text": keepImagePaths(prompt)},
					}},
				})
				if err != nil {
					t.Error(err)
					return
				}
				appendTranscript(t, home, cwd, startedID(f), string(line))
			}
			var mu sync.Mutex
			pasted, enters, reads := false, 0, 0
			f.after(func(call herdrCall) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case call.Method == "agent.prompt":
					pasted = true
					if c.submitted {
						land()
					} else if c.showAfter == 0 {
						f.setScreens("w1:p2", draft, draftANSI)
					}
				case call.Method == "pane.read" && pasted:
					if reads++; c.showAfter > 0 && reads == c.showAfter {
						f.setScreens("w1:p2", draft, draftANSI)
					}
					if c.replaceAfter > 0 && reads == c.replaceAfter {
						f.setScreens("w1:p2", strings.Replace(draft, "#2", "#3", 1), strings.Replace(draftANSI, "#2", "#3", 1))
					}
				case isKeys("enter")(call):
					enters++
					if c.clearOnEnter || c.takeEnter == enters {
						f.setScreens("w1:p2", empty, emptyANSI)
					}
					if c.takeEnter == enters {
						land()
					}
				}
			})
			if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": prompt}); err != nil {
				t.Fatal(err)
			}
			n.firstWait.Wait()
			if calls := f.trace("agent.prompt"); len(calls) != 1 {
				t.Fatalf("first prompt was pasted %d times, want exactly once", len(calls))
			}
			for _, call := range f.calls() {
				if call.Method == "agent.prompt" && str(call.Params["text"]) != keepImagePaths(prompt) {
					t.Fatal("first prompt did not preserve the image path lines")
				}
			}
			lost, _ := n.lostFirst.get(startedID(f))
			if lost.why != c.why {
				t.Fatalf("lost reason %q, want %q", lost.why, c.why)
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case c.takeEnter > 0 || c.submitted:
				if enters != c.takeEnter {
					t.Fatalf("Enter count %d, want %d", enters, c.takeEnter)
				}
				data, err := os.ReadFile(n.transcriptPath(startedID(f)))
				if err != nil || strings.Count(string(data), `"type":"user"`) != 1 {
					t.Fatalf("expected exactly one recorded first prompt: %v", err)
				}
			case c.why == lostOther:
				if enters != 0 {
					t.Fatalf("submitted unrelated draft %d times", enters)
				}
			case c.clearOnEnter:
				if enters != 1 {
					t.Fatalf("Enter count after empty box %d, want 1", enters)
				}
			case enters < 2:
				t.Fatal("did not retry Enter while the owned draft remained")
			}
		})
	}
}

func TestFirstWatchAttachesLateWithoutCountingOldImagePrompt(t *testing.T) {
	n, _, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	text := "fix the flaky test"
	watch := &promptWatch{want: echoWords(text), after: time.Now()}
	if n.firstWatchTaken(startID, watch) {
		t.Fatal("a nonexistent transcript confirmed delivery")
	}
	var old map[string]any
	if err := json.Unmarshal([]byte(promptLine(cwd, text, 2<<20)), &old); err != nil {
		t.Fatal(err)
	}
	old["timestamp"] = watch.after.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	line, _ := json.Marshal(old)
	appendTranscript(t, home, cwd, startID, string(line))
	if n.firstWatchTaken(startID, watch) {
		t.Fatal("an older matching image prompt confirmed this send")
	}
	appendTranscript(t, home, cwd, startID, promptLine(cwd, text, 2<<20))
	if !n.firstWatchTaken(startID, watch) {
		t.Fatal("a full image-bearing prompt was missed on late attach")
	}
}

func TestFirstDraftPinsResumedPasteIDs(t *testing.T) {
	prompt := "A resumed first prompt\nwith multiple\nlines"
	draft := &firstDraft{} // a resumed session already used earlier chip IDs
	if !draft.matches("[Image #5][Pasted text #6 +26 lines]", prompt) {
		t.Fatal("did not recognize the resumed paste")
	}
	for _, other := range []string{
		"[Image #5][Pasted text #7 +26 lines]",
		"[Image #5][Pasted text #6 +26 lines] unrelated words",
		"A resumed first prompt with unrelated changes",
	} {
		if draft.matches(other, prompt) {
			t.Fatalf("matched changed resumed draft %q", other)
		}
	}
}

// A placeholder cannot belong to a short first prompt, and a dialog must never receive its submit key.
func TestStartLeavesOtherPasteAndDialogAlone(t *testing.T) {
	for _, draft := range []string{
		"[Image #1][Pasted text #2 +26 lines]",
		"fix the flaky test\n\n  1. Yes\n❯ 2. No\n\n Enter to confirm · Esc to cancel",
	} {
		t.Run(draft, func(t *testing.T) {
			n, f, home := herdrNode(t)
			withSubmitGrace(t, 60*time.Millisecond)
			cwd := mkdir(t, filepath.Join(home, "proj"))
			startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
			f.after(func(c herdrCall) {
				if c.Method == "agent.prompt" {
					s := strings.Replace(screenFixture(t, "claude-2.1.312-typed-162.ansi"), "fix the flaky test", draft, 1)
					f.setScreens("w1:p2", s, s)
				}
			})
			if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the flaky test"}); err != nil {
				t.Fatal(err)
			}
			n.firstWait.Wait()
			if keys := f.trace("pane.send_keys"); len(keys) != 0 {
				t.Fatalf("pressed keys on another draft/dialog: %v", keys)
			}
			if prompts := f.trace("agent.prompt"); len(prompts) != 1 {
				t.Fatalf("pasted again: %v", prompts)
			}
		})
	}
}

// A first message with a control character goes in inert, and the box showing it so is that message left there: it gets
// its Enter.
func TestStartEntersAnInertFirstMessageLeftInTheBox(t *testing.T) {
	withSubmitGrace(t, 100*time.Millisecond)
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	inert := func(s string) string {
		return strings.Replace(s, "fix the flaky test", "<U+0007>fix the flaky test", 1)
	}
	typedTxt, typedANSI := inert(screenFixture(t, "claude-2.1.312-typed-162.txt")), inert(screenFixture(t, "claude-2.1.312-typed-162.ansi"))
	f.after(func(c herdrCall) {
		switch {
		case c.Method == "agent.prompt":
			f.setScreens("w1:p2", typedTxt, typedANSI)
		case isKeys("enter")(c):
			writeUserLine(t, home, cwd, startedID(f), "<U+0007>fix the flaky test")
		}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "\afix the flaky test"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	assertTrace(t, f.trace("agent.prompt", "pane.send_keys"), []string{
		call("agent.prompt", map[string]any{"target": "w1:p2", "text": "<U+0007>fix the flaky test"}),
		sendKeys("w1:p2", "enter"),
	})
}

// A first message that never reaches the transcript is typed firstPromptTries times; then the session is listed as
// needing the user, saying why, until a message from the phone goes in.
func TestStartMarksANeverDeliveredFirstMessage(t *testing.T) {
	withSubmitGrace(t, 50*time.Millisecond)
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	plain, ansi := updatedScreen(t)
	startPane(t, f, plain, ansi)

	res, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build"})
	if err != nil {
		t.Fatal(err)
	}
	// Answered at once, as before; the check goes on in the background.
	id := decodeSessions(t, []any{res["session"]})[0].SessionID
	n.firstWait.Wait()
	if got := f.trace("agent.prompt"); len(got) != firstPromptTries {
		t.Errorf("typed %d times: %v", len(got), got)
	}
	f.reply("session.snapshot", snapshot(nil, nil, []any{claudeAgent(id, proto.StatusIdle, "w1:p2", "w1:t2", "w1", cwd, "")}, nil))
	if l := listSessions(t, n); len(l) != 1 || l[0].Status != proto.StatusBlocked || !strings.Contains(l[0].AttentionReason, "送了 3 次都没进去") {
		t.Fatalf("listed %+v", l)
	}

	// Sent again from the phone: it goes in, and the session is what herdr says again.
	if _, err := request(t, n, "session.send", map[string]any{"session_id": id, "text": "fix the build"}); err != nil {
		t.Fatal(err)
	}
	if l := listSessions(t, n); len(l) != 1 || l[0].Status != proto.StatusIdle || l[0].AttentionReason != "" {
		t.Errorf("listed after the send %+v", l)
	}
}

// The transcript getting a message typed at the computer clears it too.
func TestALostFirstMessageClearsOnceTheTranscriptHasOne(t *testing.T) {
	n, _, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	n.lostFirst.set(startID, lostDialog, time.Now())
	if why := n.lostFirstPrompt(startID, proto.StatusIdle); why != lostDialog {
		t.Fatalf("why %q", why)
	}
	writeUserLine(t, home, cwd, startID, "typed at the computer")
	if why := n.lostFirstPrompt(startID, proto.StatusIdle); why != "" {
		t.Errorf("still lost: %q", why)
	}
	n.lostFirst.set(startID, lostDialog, time.Now().Add(time.Minute))
	if why := n.lostFirstPrompt(startID, proto.StatusWorking); why != "" {
		t.Errorf("a working agent still lost: %q", why)
	}
}

// A claude that turns to working without the transcript showing the message yet took it: not typed twice.
func TestStartDoesNotRetypeForAWorkingClaude(t *testing.T) {
	n, f, home := herdrNode(t)
	cwd := mkdir(t, filepath.Join(home, "proj"))
	startPane(t, f, screenFixture(t, "claude-2.1.312-empty-162.txt"), screenFixture(t, "claude-2.1.312-empty-162.ansi"))
	f.after(func(c herdrCall) {
		if c.Method == "agent.prompt" {
			startHerdr(f, "w1:p2", proto.StatusWorking)
		}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "workspace_id": "w1", "prompt": "fix the build"}); err != nil {
		t.Fatal(err)
	}
	n.firstWait.Wait()
	if got := f.trace("agent.prompt"); len(got) != 1 {
		t.Errorf("typed %v", got)
	}
	if _, ok := n.lostFirst.get(startedID(f)); ok {
		t.Error("marked as lost")
	}
}
