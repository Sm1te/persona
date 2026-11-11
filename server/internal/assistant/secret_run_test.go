package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

// secret_run (secret_run.go) and the hardening of cards with secret fields that came with it (#1137). The password
// is a made-up value; every test checks it never shows up where it must not.

const testPassword = "hunter2-SECRET-xyz"

// secretRig is a manager with the fleet on, the assistant's computer "server" and the target "mac" (named "Mac
// mini"), a node there that answers secret.run, and everything the manager writes, logs or tells kept for a look.
type secretRig struct {
	t   *testing.T
	s   *Service
	f   *fakeHub
	mem *fleetMem

	mu       sync.Mutex
	logs     strings.Builder
	checks   int
	runs     []proto.SecretRunParams
	checkErr error
	runErr   error
	results  func(p proto.SecretRunParams) []proto.SecretRunResult // the node's results (nil: each command exits 0, echoing the password)
	ended    []string                                              // what the channels were told (fleetEnded), as JSON
	resolved []string                                              // what the assistant was told (InteractionResolved), as JSON
	owner    int                                                   // interactions.owner_action calls
}

// newSecretRig is a secretRig on store st (nil: its in-memory interactions table, mem).
func newSecretRig(t *testing.T, st Store) *secretRig {
	t.Helper()
	r := &secretRig{t: t, f: newFakeHub(), mem: newFleetMem()}
	r.f.sessions["server"], r.f.sessions["mac"] = nil, nil
	r.f.devices = []Device{{ID: "server", Name: "Server", Online: true, Reachable: true}, {ID: "mac", Name: "Mac mini", Online: true, Reachable: true}}
	r.f.answerNode(proto.MethodSecretRun, r.node)
	r.f.answerApp(AppInteractions, assistantwire.InteractionsOwnerAction, func(json.RawMessage) (any, error) {
		r.mu.Lock()
		r.owner++
		r.mu.Unlock()
		return map[string]any{}, nil
	})
	d := newFleetDevs()
	d.add("server", "Server")
	d.add("mac", "Mac mini")
	if st == nil {
		st = r.mem
	}
	r.s = withSettings(t, r.f, New(Options{Hub: r.f, Store: func() Store { return st }, Logf: r.logf, Devices: d}))
	r.s.fleetEnded = func(ix proto.Interaction, res *proto.Resolution) {
		b, _ := json.Marshal(map[string]any{"ix": ix, "resolution": res})
		r.mu.Lock()
		r.ended = append(r.ended, string(b))
		r.mu.Unlock()
	}
	r.s.ixResolved = func(ir InteractionResolved) {
		b, _ := json.Marshal(ir)
		r.mu.Lock()
		r.resolved = append(r.resolved, string(b))
		r.mu.Unlock()
	}
	return r
}

func (r *secretRig) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(&r.logs, format+"\n", args...)
}

// node is the node on "mac" answering secret.run (under the fake hub's lock).
func (r *secretRig) node(machine string, params json.RawMessage) (any, error) {
	if machine != "mac" {
		return nil, errors.New("not the target")
	}
	var p proto.SecretRunParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.Check {
		r.checks++
		return map[string]any{"ok": true}, r.checkErr
	}
	r.runs = append(r.runs, p)
	if r.runErr != nil {
		return nil, r.runErr
	}
	var results []proto.SecretRunResult
	if r.results != nil {
		results = r.results(p)
	} else {
		for _, c := range p.Commands { // a node that echoes something else and lets the password through: masked again
			results = append(results, proto.SecretRunResult{Command: "echoed " + c, Output: "got:" + string(p.Password)})
		}
	}
	return proto.SecretRunAnswer{Results: results}, nil
}

func (r *secretRig) counts() (checks, runs, owner int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checks, len(r.runs), r.owner
}

// wantNoRun fails when the node ran anything.
func (r *secretRig) wantNoRun() {
	r.t.Helper()
	if _, runs, _ := r.counts(); runs != 0 {
		r.t.Fatalf("the node ran %d times", runs)
	}
}

// resultText is a tool result's text.
func resultText(res proto.ToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].Text
}

// call runs secret_run as the main conversation on "server" calls it; its result comes on the channel.
func (r *secretRig) call(args string) chan proto.ToolResult {
	ctx, cancel := context.WithCancel(context.Background())
	r.t.Cleanup(cancel)
	done := make(chan proto.ToolResult, 1)
	go func() {
		done <- r.s.Tool(ctx, ToolCall{Name: "secret_run", MachineID: "server", SessionID: "s1", Arguments: json.RawMessage(args)})
	}()
	return done
}

// card waits until the call's card is on the queue the apps answer and they were told of it.
func (r *secretRig) card() proto.Interaction {
	r.t.Helper()
	var ix proto.Interaction
	waitFor(r.t, "the password card on the queue", func() bool {
		qs := r.s.pendingQuestions()
		if len(qs) != 1 {
			return false
		}
		got, ok := r.s.pendingIx(qs[0].ID)
		ix = got
		return ok
	})
	waitAnnounced(r.t, r.f, ix.ID)
	return ix
}

// waitAnnounced waits until the hub was told of card id (fleet.interaction), as the apps learn of it. The card is on
// the queue a moment before: its history row is written in between, and an answer given before that row exists is
// recorded nowhere (FinishInteraction updates a pending row), so the history would keep the card pending.
func waitAnnounced(t *testing.T, f *fakeHub, id string) {
	t.Helper()
	waitFor(t, "the card told to the apps", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, e := range f.told {
			var p assistantwire.FleetInteraction
			if e.event == assistantwire.EventFleetInteraction && json.Unmarshal(e.params, &p) == nil && p.Interaction.ID == id {
				return true
			}
		}
		return false
	})
}

// post answers card id through the route the hub relays the apps' answers to.
func (r *secretRig) post(id, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.s.fleetHandler(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/interactions/"+id+"/resolve", strings.NewReader(body)))
	return w
}

// outcome is the call's JSON result.
func (r *secretRig) outcome(done chan proto.ToolResult) (secretRunOutcome, string) {
	r.t.Helper()
	select {
	case res := <-done:
		text := resultText(res)
		if res.IsError {
			r.t.Fatalf("secret_run failed: %s", text)
		}
		var out secretRunOutcome
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			r.t.Fatalf("result %q: %v", text, err)
		}
		return out, text
	case <-time.After(5 * time.Second):
		r.t.Fatal("secret_run did not return")
		return secretRunOutcome{}, ""
	}
}

// noLeak fails when any of the values shows up in what the manager kept, logged or told, or in extra.
func (r *secretRig) noLeak(extra string, values ...string) {
	r.t.Helper()
	r.mem.fmu.Lock()
	kept, _ := json.Marshal(map[string]any{"records": r.mem.recs, "resolutions": r.mem.res})
	r.mem.fmu.Unlock()
	r.f.mu.Lock()
	told, _ := json.Marshal(map[string]any{"snapshots": r.f.snaps, "pushes": r.f.pushes, "events": r.f.events, "app_calls": r.f.appCalls})
	var tells []string
	for _, t := range r.f.told {
		tells = append(tells, t.event+" "+string(t.params))
	}
	var calls []string
	for _, c := range r.f.calls {
		calls = append(calls, c.Method+" "+string(c.Params))
	}
	r.f.mu.Unlock()
	r.mu.Lock()
	sinks := map[string]string{
		"the interactions table": string(kept), "the logs": r.logs.String(), "the channels": strings.Join(r.ended, "\n"),
		"the answered cards": strings.Join(r.resolved, "\n"), "the hub's events": strings.Join(tells, "\n"), "the hub": string(told),
		"the device calls": strings.Join(calls, "\n"), "the result": extra,
	}
	r.mu.Unlock()
	for where, text := range sinks {
		for _, v := range values {
			if strings.Contains(text, v) {
				r.t.Errorf("%q is in %s: %s", v, where, text)
			}
		}
	}
}

const secretArgs = `{"machine":"Mac mini","purpose":"Install the launch daemon for the backup job",` +
	`"commands":["sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist","echo done && true"]}`

// The whole run: the card says what will run, the user's password reaches the target's node and nothing else, the
// Agent gets exit codes and masked output, and a second answer runs nothing again.
func TestSecretRunRunsWithAPasswordNothingElseSees(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	if ix.Kind != proto.IxPermission || ix.ToolName != "secret_run" || ix.MachineID != "server" || ix.SessionID != "s1" {
		t.Fatalf("card = %+v", ix)
	}
	if len(ix.Fields) != 1 || ix.Fields[0].Key != "password" || !ix.Fields[0].Secret || ix.Fields[0].Label == "" || ix.Fields[0].Placeholder == "" {
		t.Fatalf("fields = %+v", ix.Fields)
	}
	if got := ix.ExpiresAt.Sub(ix.CreatedAt); got != 10*time.Minute {
		t.Fatalf("the card lives %v, want 10m", got)
	}
	var input struct {
		Machine     string   `json:"machine"`
		MachineName string   `json:"machine_name"`
		Purpose     string   `json:"purpose"`
		Delivery    string   `json:"delivery"`
		Commands    []string `json:"commands"`
	}
	_ = json.Unmarshal(ix.ToolInput, &input)
	if input.Machine != "mac" || input.MachineName != "Mac mini" || input.Delivery != "stdin" || len(input.Commands) != 2 ||
		input.Purpose != "Install the launch daemon for the backup job" {
		t.Fatalf("tool_input = %s", ix.ToolInput)
	}
	for _, want := range []string{"Mac mini", "Install the launch daemon", "\n$ sudo -S -k -p '' launchctl bootstrap system", "\n$ echo done && true"} {
		if !strings.Contains(ix.Summary, want) {
			t.Fatalf("summary %q lacks %q", ix.Summary, want)
		}
	}

	if w := r.post(ix.ID, `{"allow":true,"values":{"password":"`+testPassword+`","note":"undeclared-SECRET-abc"}}`); w.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", w.Code, w.Body)
	}
	out, text := r.outcome(done)
	if out.Status != secretRan || out.Machine != "Mac mini" || len(out.Results) != 2 || !strings.Contains(out.Message, "Ran 2 commands on Mac mini") {
		t.Fatalf("outcome = %s", text)
	}
	if r0 := out.Results[0]; r0.Command != input.Commands[0] || r0.Output != "" || !r0.OutputWithheld {
		t.Fatalf("result 0 = %+v, want the approved command and its output withheld (it held the password)", r0)
	}
	if !strings.Contains(out.Message, "output of command 1, 2 was withheld") {
		t.Fatalf("message %q does not say the output was withheld", out.Message)
	}
	if !strings.Contains(text, "echo done && true") {
		t.Fatalf("the result escapes the command: %s", text)
	}
	r.mu.Lock()
	run := r.runs[0]
	r.mu.Unlock()
	if string(run.Password) != testPassword || run.TimeoutMS != 300_000 || len(run.Commands) != 2 || run.Commands[1] != "echo done && true" {
		t.Fatalf("the node got %+v", run)
	}

	// One use: the same card answered again is the first answer again, and nothing runs a second time.
	if w := r.post(ix.ID, `{"allow":true,"values":{"password":"another-SECRET-pw"}}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"already":true`) {
		t.Fatalf("second answer: %d %s", w.Code, w.Body)
	} else if strings.Contains(w.Body.String(), testPassword) {
		t.Fatalf("the second answer's reply carries the password: %s", w.Body)
	}
	if checks, runs, _ := r.counts(); checks != 1 || runs != 1 {
		t.Fatalf("checks %d, runs %d; want 1 and 1", checks, runs)
	}
	status, kept, _ := r.mem.InteractionOutcome(context.Background(), ix.ID)
	if status != "resolved" || kept == nil || kept.Values["password"] != redactedValue || len(kept.Values) != 1 {
		t.Fatalf("history: %s %+v", status, kept)
	}
	waitFor(t, "the answer told to the assistant", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.resolved) > 0
	})
	b64 := base64.StdEncoding.EncodeToString([]byte(testPassword))
	r.noLeak(text, testPassword, "undeclared-SECRET-abc", "another-SECRET-pw")
	r.f.mu.Lock()
	for _, c := range r.f.calls {
		if strings.Contains(string(c.Params), b64) && !(c.Method == proto.MethodSecretRun && strings.Contains(string(c.Params), `"commands"`)) {
			t.Errorf("the password went in %s %s", c.Method, c.Params)
		}
	}
	r.f.mu.Unlock()
}

// The same run on a real interactions table: neither the card nor its answer keeps the password.
func TestSecretRunKeepsNoPasswordInPostgres(t *testing.T) {
	database := viewErrorsDB(t) // the test database (skipped without one)
	r := newSecretRig(t, database)
	done := r.call(secretArgs)
	ix := r.card()
	if w := r.post(ix.ID, `{"allow":true,"values":{"password":"`+testPassword+`","note":"undeclared-SECRET-abc"}}`); w.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", w.Code, w.Body)
	}
	if out, text := r.outcome(done); out.Status != secretRan {
		t.Fatalf("outcome = %s", text)
	}
	recs, err := database.Interactions(context.Background(), "server", "s1", 50)
	if err != nil {
		t.Fatal(err)
	}
	status, kept, err := database.InteractionOutcome(context.Background(), ix.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status != "resolved" || kept == nil || kept.Values["password"] != redactedValue {
		t.Fatalf("history: %s %+v", status, kept)
	}
	stored, _ := json.Marshal(map[string]any{"records": recs, "resolution": kept})
	if !strings.Contains(string(stored), ix.ID) {
		t.Fatalf("the card is not in the table: %s", stored)
	}
	for _, v := range []string{testPassword, "undeclared-SECRET-abc"} {
		if strings.Contains(string(stored), v) {
			t.Fatalf("%q is in the interactions table: %s", v, stored)
		}
	}
}

// Not filled in in time: expired, and the node never runs anything.
func TestSecretRunExpiresWithoutRunning(t *testing.T) {
	old := secretRunCardWait
	secretRunCardWait = 300 * time.Millisecond
	t.Cleanup(func() { secretRunCardWait = old })
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	out, text := r.outcome(done)
	if out.Status != secretExpired || len(out.Results) != 0 {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
	if status, _, _ := r.mem.InteractionOutcome(context.Background(), ix.ID); status != "expired" {
		t.Fatalf("history: %s", status)
	}
}

// Declined on the phone: declined, nothing ran.
func TestSecretRunDeclined(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	if w := r.post(ix.ID, `{"allow":false}`); w.Code != http.StatusOK {
		t.Fatalf("decline: %d %s", w.Code, w.Body)
	}
	if out, text := r.outcome(done); out.Status != secretDeclined {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
}

// Closed on the phone ("hand back to computer", a client that only closes it) before the deadline: declined, nothing
// ran.
func TestSecretRunDismissedIsDeclined(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	if w := r.post(ix.ID, `{"dismiss":true}`); w.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", w.Code, w.Body)
	}
	if out, text := r.outcome(done); out.Status != secretDeclined || !strings.Contains(out.Message, "closed the password card") {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
}

// A dismiss that comes after the deadline from something other than the manager's own timer (the hub's queue, when the
// manager does not keep the cards) is the card expiring, not the user closing it.
func TestSecretRunDismissAfterTheDeadlineIsExpired(t *testing.T) {
	old := secretRunCardWait
	secretRunCardWait = 100 * time.Millisecond
	t.Cleanup(func() { secretRunCardWait = old })
	f := newFakeHub()
	f.sessions["server"], f.sessions["mac"] = nil, nil
	f.devices = []Device{{ID: "server", Name: "Server", Online: true}, {ID: "mac", Name: "Mac mini", Online: true}}
	f.answerNode(proto.MethodSecretRun, func(string, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil })
	s := newTestService(t, f) // no fleet: the card waits for an answer through Action only
	done := make(chan proto.ToolResult, 1)
	go func() {
		done <- s.Tool(context.Background(), ToolCall{Name: "secret_run", MachineID: "server", SessionID: "s1", Arguments: json.RawMessage(secretArgs)})
	}()
	waitFor(t, "the password card", func() bool { return len(s.pendingQuestions()) == 1 })
	ix := s.pendingQuestions()[0]
	time.Sleep(time.Until(ix.ExpiresAt) + 50*time.Millisecond)
	if _, err := s.Action(context.Background(), Action{Op: "answer", InteractionID: ix.ID, Answer: json.RawMessage(`{"dismiss":true}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		var out secretRunOutcome
		if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil || out.Status != secretExpired {
			t.Fatalf("result %q (%v), want expired", resultText(res), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("secret_run did not return")
	}
}

// Dismissed before the deadline: the user closed the card; at it, or a moment before by another clock: it expired.
func TestSecretRunDismissal(t *testing.T) {
	expires := time.Now()
	for _, c := range []struct {
		at   time.Duration
		want string
	}{{-10 * time.Minute, secretDeclined}, {-3 * time.Second, secretDeclined}, {-time.Second, secretExpired}, {0, secretExpired}, {time.Minute, secretExpired}} {
		if got, _ := secretRunDismissal(expires.Add(c.at), expires); got != c.want {
			t.Errorf("dismissed %v from the deadline: %s, want %s", c.at, got, c.want)
		}
	}
}

// Run without the password is refused by the manager and the card stays as it was, still waiting.
func TestSecretRunAllowWithoutPasswordStaysPending(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	for _, body := range []string{`{"allow":true}`, `{"allow":true,"values":{"password":"   "}}`, `{"allow":true,"values":{"note":"x"}}`} {
		if w := r.post(ix.ID, body); w.Code == http.StatusOK {
			t.Fatalf("%s was taken: %s", body, w.Body)
		}
		if _, pending := r.s.pendingIx(ix.ID); !pending {
			t.Fatalf("%s ended the card", body)
		}
	}
	select {
	case res := <-done:
		t.Fatalf("the call returned: %+v", res)
	default:
	}
	r.post(ix.ID, `{"allow":false}`)
	if out, text := r.outcome(done); out.Status != secretDeclined {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
}

// Answers that are not the user's own on the card refuse the run: one the assistant wrote, an "Always allow" the
// manager replayed, and a replay of the history's answer (only the marker is in it).
func TestSecretRunRefusesAnswersNotTypedOnTheCard(t *testing.T) {
	yes := true
	for name, answer := range map[string]proto.Resolution{
		"assistant":  {Allow: &yes, Values: map[string]string{"password": testPassword}, AnsweredBy: proto.AnsweredByAssistant},
		"remembered": {Allow: &yes, Values: map[string]string{"password": testPassword}, Remembered: true},
		"replayed":   {Allow: &yes, Values: map[string]string{"password": redactedValue}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSecretRig(t, nil)
			done := r.call(secretArgs)
			ix := r.card()
			if err := r.s.resolve(ix.ID, answer); err != nil {
				t.Fatal(err)
			}
			out, text := r.outcome(done)
			if out.Status != secretRefused {
				t.Fatalf("outcome = %s", text)
			}
			r.wantNoRun()
			r.noLeak(text, testPassword)
		})
	}
}

// A channel cannot fill the card in: its Run is refused before the press is used up and the card stays; its Decline
// counts.
func TestSecretRunChannelCanOnlyDecline(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	yes, no := true, false
	ctx := context.Background()
	if _, err := r.s.channelResolve(ctx, "chat", ix.ID, proto.Resolution{Allow: &yes, Values: map[string]string{"password": testPassword}}, "press-1"); err == nil {
		t.Fatal("a channel's Run was taken")
	}
	if _, pending := r.s.pendingIx(ix.ID); !pending {
		t.Fatal("the refused Run ended the card")
	}
	if _, _, owner := r.counts(); owner != 0 {
		t.Fatalf("the refused Run used up the press (%d owner actions)", owner)
	}
	if res, err := r.s.channelResolve(ctx, "chat", ix.ID, proto.Resolution{Allow: &no, Values: map[string]string{"password": testPassword}}, "press-2"); err != nil {
		t.Fatal(err)
	} else if m, _ := res.(map[string]any); m["ok"] != true {
		t.Fatalf("decline = %v", res)
	}
	if out, text := r.outcome(done); out.Status != secretDeclined {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
	r.noLeak("", testPassword)
}

// The node's results become the Agent's: the first failure (an exit code, a timeout) stops the run and says which
// command; a run that fails on the way says so; long output keeps its end.
func TestSecretRunFailures(t *testing.T) {
	long := strings.Repeat("a", 3000) + strings.Repeat("b", 3000)
	for name, c := range map[string]struct {
		results func(proto.SecretRunParams) []proto.SecretRunResult
		runErr  error
		want    []string
		check   func(t *testing.T, out secretRunOutcome)
	}{
		"exit": {results: func(p proto.SecretRunParams) []proto.SecretRunResult {
			return []proto.SecretRunResult{{Command: p.Commands[0]}, {Command: p.Commands[1], ExitCode: 3, Output: "no"}}
		}, want: []string{"Command 2 of 2 failed on Mac mini (exit code 3)"}},
		"timeout": {results: func(p proto.SecretRunParams) []proto.SecretRunResult {
			return []proto.SecretRunResult{{Command: p.Commands[0], ExitCode: -1, TimedOut: true}}
		}, want: []string{"Command 1 of 2", "may still be running", "check its state"}},
		"node error": {runErr: errors.New("connection lost"), want: []string{"failed after the user approved: connection lost"}},
		"output with the password": {results: func(p proto.SecretRunParams) []proto.SecretRunResult {
			return []proto.SecretRunResult{{Command: p.Commands[0], Output: "home: /Users/" + testPassword}, {Command: p.Commands[1], Output: "ok"}}
		}, check: func(t *testing.T, out secretRunOutcome) {
			if a, b := out.Results[0], out.Results[1]; a.Output != "" || !a.OutputWithheld || b.Output != "ok" || b.OutputWithheld {
				t.Fatalf("results %+v: want the first withheld, the second whole", out.Results)
			}
		}, want: []string{"output of command 1 was withheld"}},
		"long output": {results: func(p proto.SecretRunParams) []proto.SecretRunResult {
			return []proto.SecretRunResult{{Command: p.Commands[0], Output: long}, {Command: p.Commands[1]}}
		}, check: func(t *testing.T, out secretRunOutcome) {
			o := out.Results[0]
			if !o.Truncated || len([]rune(o.Output)) != secretRunOutputRunes || !strings.HasSuffix(o.Output, "b") {
				t.Fatalf("output %d runes, truncated %v", len([]rune(o.Output)), o.Truncated)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSecretRig(t, nil)
			r.results, r.runErr = c.results, c.runErr
			done := r.call(secretArgs)
			ix := r.card()
			r.post(ix.ID, `{"allow":true,"values":{"password":"`+testPassword+`"}}`)
			out, text := r.outcome(done)
			wantStatus := secretFailed
			if c.check != nil {
				wantStatus = secretRan
				c.check(t, out)
			}
			if out.Status != wantStatus {
				t.Fatalf("outcome = %s", text)
			}
			for _, w := range c.want {
				if !strings.Contains(out.Message, w) {
					t.Fatalf("message %q lacks %q", out.Message, w)
				}
			}
			r.noLeak(text, testPassword)
		})
	}
}

// What is checked before any card: the arguments, the machine, and whether its node can run it at all.
func TestSecretRunChecksBeforeTheCard(t *testing.T) {
	for name, c := range map[string]struct {
		args     string
		checkErr error
		want     string
	}{
		"no -S":          {args: `{"machine":"mac","purpose":"p","commands":["sudo launchctl list"]}`, want: "sudo -S -k -p ''"},
		"no -S later":    {args: `{"machine":"mac","purpose":"p","commands":["true","cd /tmp && sudo -k rm x"]}`, want: "command 2 calls sudo without -S"},
		"six commands":   {args: `{"machine":"mac","purpose":"p","commands":["a","b","c","d","e","f"]}`, want: "1 to 5"},
		"no commands":    {args: `{"machine":"mac","purpose":"p","commands":[]}`, want: "1 to 5"},
		"two lines":      {args: `{"machine":"mac","purpose":"p","commands":["true\nrm -rf x"]}`, want: "character 5 is U+000A"},
		"escape":         {args: `{"machine":"mac","purpose":"p","commands":["echo \u001b[2Khidden"]}`, want: "command 1 cannot go on the card: character 6 is U+001B"},
		"bidi":           {args: `{"machine":"mac","purpose":"p","commands":["echo \u202eevil"]}`, want: "character 6 is U+202E"},
		"line separator": {args: `{"machine":"mac","purpose":"p","commands":["true \u2028# ; rm -rf x"]}`, want: "character 6 is U+2028"},
		"zero width":     {args: `{"machine":"mac","purpose":"p","commands":["brew\u200b services start x"]}`, want: "character 5 is U+200B"},
		"no-break space": {args: `{"machine":"mac","purpose":"p","commands":["true","ls\u00a0/tmp"]}`, want: "command 2 cannot go on the card: character 3 is U+00A0"},
		"cyrillic":       {args: `{"machine":"mac","purpose":"p","commands":["ec\u0430o hi"]}`, want: "character 3 is U+0430"},
		"purpose zw":     {args: `{"machine":"mac","purpose":"why\u200b","commands":["true"]}`, want: "purpose"},
		"two sudo":       {args: `{"machine":"mac","purpose":"p","commands":["sudo -S -k -p '' a && sudo -S -k -p '' b"]}`, want: "calls sudo 2 times"},
		"sudo in a pipe": {args: `{"machine":"mac","purpose":"p","commands":["echo x | sudo -S -k -p '' tee /etc/x"]}`, want: "after a pipe"},
		"no machine":     {args: `{"purpose":"p","commands":["true"]}`, want: "machine must name"},
		"timeout wraps":  {args: `{"machine":"mac","purpose":"p","commands":["true"],"timeout_seconds":18446744074}`, want: "1 to 1800"},
		"too long":       {args: `{"machine":"mac","purpose":"p","commands":["` + strings.Repeat("x", 1001) + `"]}`, want: "longer than 1000"},
		"no purpose":     {args: `{"machine":"mac","commands":["true"]}`, want: "purpose"},
		"purpose lines":  {args: `{"machine":"mac","purpose":"why\n$ true","commands":["true"]}`, want: "purpose"},
		"timeout":        {args: `{"machine":"mac","purpose":"p","commands":["true"],"timeout_seconds":1801}`, want: "1 to 1800"},
		"delivery":       {args: `{"machine":"mac","purpose":"p","commands":["true"],"delivery":"askpass"}`, want: "only stdin"},
		"machine":        {args: `{"machine":"nowhere","purpose":"p","commands":["true"]}`, want: "unknown machine"},
		"node":           {args: `{"machine":"mac","purpose":"p","commands":["true"]}`, checkErr: errors.New("unknown method secret.run"), want: "unknown method secret.run"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSecretRig(t, nil)
			r.checkErr = c.checkErr
			res := <-r.call(c.args)
			text := resultText(res)
			if !res.IsError || !strings.Contains(text, c.want) {
				t.Fatalf("result %v %q, want an error with %q", res.IsError, text, c.want)
			}
			if len(r.s.pendingQuestions()) != 0 || len(r.s.pendingInteractions()) != 0 {
				t.Fatal("a card went up")
			}
			r.wantNoRun()
		})
	}
}

// ---- cards with secret fields (the mail account's too) ----

// The answer to a card with a secret field: the one waiting on it gets what was typed into the fields it declares,
// nothing else; the history, the channels and the assistant are told it with the secret masked.
func TestSecretFieldAnswerIsToldRedacted(t *testing.T) {
	ft := newFleetTest(t)
	var mu sync.Mutex
	var told []string
	ft.s.fleetEnded = func(ix proto.Interaction, r *proto.Resolution) {
		b, _ := json.Marshal(r)
		mu.Lock()
		told = append(told, "ended "+string(b))
		mu.Unlock()
	}
	ft.s.ixResolved = func(ir InteractionResolved) {
		b, _ := json.Marshal(ir.Resolution)
		mu.Lock()
		told = append(told, "resolved "+string(b))
		mu.Unlock()
	}
	ix := proto.Interaction{ID: "ix_mail", MachineID: "mac", SessionID: "s1", Kind: proto.IxPermission, ToolName: "persona_settings",
		Fields:    []proto.Field{{Key: "name", Label: "Name"}, {Key: "password", Label: "Password", Secret: true}},
		ExpiresAt: time.Now().Add(time.Hour)}
	got := make(chan proto.Resolution, 1)
	go func() {
		r, _ := ft.s.askHub(context.Background(), ix)
		got <- r
	}()
	ft.waitPending(1)
	waitAnnounced(t, ft.f, ix.ID)
	yes := true
	if err := ft.s.resolve(ix.ID, proto.Resolution{Allow: &yes, Values: map[string]string{"name": "Work", "password": testPassword,
		"extra": "undeclared-SECRET-abc"}}); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.Values["password"] != testPassword || r.Values["name"] != "Work" || len(r.Values) != 2 {
		t.Fatalf("the waiting call got %v", r.Values)
	}
	_, kept, _ := ft.st.InteractionOutcome(context.Background(), ix.ID)
	if kept == nil || kept.Values["password"] != redactedValue || kept.Values["name"] != "Work" || len(kept.Values) != 2 {
		t.Fatalf("history: %+v", kept)
	}
	waitFor(t, "the answer told to the assistant", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(told, "\n"), "resolved ")
	})
	mu.Lock()
	all := strings.Join(told, "\n")
	mu.Unlock()
	if !strings.Contains(all, `"password":"\u003credacted\u003e"`) || strings.Contains(all, testPassword) || strings.Contains(all, "undeclared-SECRET-abc") {
		t.Fatalf("told: %s", all)
	}
	ft.f.mu.Lock()
	defer ft.f.mu.Unlock()
	for _, e := range ft.f.told {
		if strings.Contains(string(e.params), testPassword) || strings.Contains(string(e.params), "undeclared-SECRET-abc") {
			t.Fatalf("the hub was told %s %s", e.event, e.params)
		}
	}
}

// An answer the Action could not take is kept for another try, unless its card has a secret field: that one goes
// once, and its bytes are cleared.
func TestSecretFieldAnswerIsNeverKept(t *testing.T) {
	s := newTestService(t, newFakeHub())
	busy := errors.New("busy")
	plain := proto.Interaction{ID: "ix_plain", Kind: proto.IxPermission, ExpiresAt: time.Now().Add(time.Hour)}
	secret := plain
	secret.ID, secret.Fields = "ix_secret", []proto.Field{{Key: "password", Secret: true}}
	answer, _ := json.Marshal(proto.Resolution{Values: map[string]string{"password": testPassword}})
	s.settleAnswer(secret, Action{InteractionID: secret.ID, Answer: answer}, busy)
	if s.answerKept(secret.ID) || strings.Trim(string(answer), "\x00") != "" {
		t.Fatalf("the answer to a card with a secret field was kept (%v) or left as it was: %q", s.answerKept(secret.ID), answer)
	}
	s.settleAnswer(plain, Action{InteractionID: plain.ID, Answer: json.RawMessage(`{"allow":true}`)}, busy)
	if !s.answerKept(plain.ID) {
		t.Fatal("an ordinary answer was not kept")
	}
}

// ---- the password card can only be the tool's ----

// forgedCard is a card from outside the tool that calls itself the password card.
func forgedCard(id string) proto.Interaction {
	input, _ := json.Marshal(map[string]any{"machine": "mac", "machine_name": "Mac", "purpose": "update",
		"commands": []string{"sudo -S -k -p '' true"}, "delivery": "stdin"})
	return proto.Interaction{ID: id, MachineID: "mac", SessionID: "s1", Kind: proto.IxPermission, ToolName: proto.SecretRunTool,
		ToolInput: input, Fields: []proto.Field{{Key: "password", Label: "Password", Secret: true}},
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}
}

// forgedHandoffCard is a card from outside the manager's Agent start that calls itself the hand-off card.
func forgedHandoffCard(id string) proto.Interaction {
	input, _ := json.Marshal(proto.SecretHandoffInput{Variables: []proto.SecretHandoffVariable{
		{Name: "GITHUB_TOKEN", Provider: "vault", Title: "GitHub › credential"}}})
	return proto.Interaction{ID: id, MachineID: "mac", SessionID: "s1", Kind: proto.IxPermission, ToolName: proto.SecretHandoffTool,
		ToolInput: input, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}
}

// A node's interaction.create (a hook, a runtime, a daemon), a Codex list and an extension's card (fleet.ask) cannot
// put a password card or a hand-off card on the phone: it would be shown as the trusted card (the password card
// without its Agent's name), and the password would go back to whoever asked, or a yes would pass for a hand-off's.
func TestOnlyTheToolMakesThePasswordCard(t *testing.T) {
	for name, forged := range map[string]func(string) proto.Interaction{"password": forgedCard, "hand-off": forgedHandoffCard} {
		ft := newFleetTest(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		params, _ := json.Marshal(map[string]any{"interaction": forged("ix_node")})
		if _, handled, err := ft.s.NodeRequest(ctx, "mac", "interaction.create", params); !handled || !errors.Is(err, errReservedCard) {
			t.Fatalf("%s card, node interaction.create: handled %v, err %v; want it refused", name, handled, err)
		}
		if _, err := ft.s.askHub(ctx, forged("ix_ext")); !errors.Is(err, errReservedCard) {
			t.Fatalf("%s card, fleet.ask: err %v, want it refused", name, err)
		}
		cancel()
		codex, _ := json.Marshal(map[string]any{"interactions": []proto.Interaction{forged("ix_codex")}})
		ft.s.syncCodexInteractions("mac", codex)
		if got := ft.s.pendingInteractions(); len(got) != 0 {
			t.Fatalf("%s card, pending: %+v", name, got)
		}
		if told := ft.toldOf(assistantwire.EventFleetInteraction); len(told) != 0 {
			t.Fatalf("the phone was told of %d forged %s cards", len(told), name)
		}
	}
}

// The device the hub found may approve a hand-off card reaches the manager's routes in the request's context, from
// the relayed request's own field only: a header of any name does not put one there.
func TestHandoffApproverComesFromTheHubOnly(t *testing.T) {
	var got []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := assistantwire.HandoffApproverOf(r.Context())
		got = append(got, fmt.Sprintf("%v %s %s", ok, a.DeviceID, a.DeviceName))
	})
	forged := map[string][]string{"X-Persona-Handoff-Approver": {`{"device_id":"d9"}`}, "Handoff_approver": {"d9"}}
	for _, req := range []assistantwire.HTTPRequest{
		{Method: "POST", Path: "/api/v1/interactions/ix/resolve", Header: forged},
		{Method: "POST", Path: "/api/v1/interactions/ix/resolve", HandoffApprover: &assistantwire.HandoffApprover{DeviceID: "d1", DeviceName: "iPhone"}},
		{Method: "POST", Path: "/api/v1/interactions/ix/resolve", HandoffApprover: &assistantwire.HandoffApprover{}},
	} {
		if _, err := serveHTTP(context.Background(), h, req, nil); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"false  ", "true d1 iPhone", "false  "}; !slices.Equal(got, want) {
		t.Fatalf("approvers %q, want %q", got, want)
	}
}

// A password with a control character (a line break: sudo would take only its first line, the rest left on stdin)
// is refused: nothing runs.
func TestSecretRunRefusesAPasswordWithAControlCharacter(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	if w := r.post(ix.ID, `{"allow":true,"values":{"password":"hunter2-SECRET\nrm -rf x"}}`); w.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", w.Code, w.Body)
	}
	out, text := r.outcome(done)
	if out.Status != secretRefused || !strings.Contains(out.Message, "control character") {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
	r.noLeak(text, "hunter2-SECRET")
}

// A password longer than the node takes is refused before the run: the result says nothing ran, not that some may have.
func TestSecretRunRefusesAPasswordTooLong(t *testing.T) {
	r := newSecretRig(t, nil)
	done := r.call(secretArgs)
	ix := r.card()
	long := strings.Repeat("x", proto.SecretRunMaxPassword+1)
	if w := r.post(ix.ID, `{"allow":true,"values":{"password":"`+long+`"}}`); w.Code != http.StatusOK {
		t.Fatalf("answer: %d %s", w.Code, w.Body)
	}
	out, text := r.outcome(done)
	if out.Status != secretRefused || !strings.Contains(out.Message, "nothing ran") {
		t.Fatalf("outcome = %s", text)
	}
	r.wantNoRun()
}

// Two computers of the same name, or a name another one has for its id or ssh alias: the card names this one with its id.
func TestSecretRunNamesTheComputerUnambiguously(t *testing.T) {
	for name, other := range map[string]Device{
		"same name":      {ID: "mac2", Name: "MAC MINI", Online: true},
		"its id":         {ID: "mac mini", Name: "Studio", Online: true},
		"its ssh alias":  {ID: "studio", Name: "Studio", SSHHost: "mac mini", Online: true},
		"no other match": {ID: "studio", Name: "Studio", SSHHost: "studio", Online: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newSecretRig(t, nil)
			r.f.mu.Lock()
			r.f.devices = append(r.f.devices, other)
			r.f.mu.Unlock()
			r.call(secretArgs)
			ix := r.card()
			var input struct {
				MachineName string `json:"machine_name"`
			}
			_ = json.Unmarshal(ix.ToolInput, &input)
			want := "Mac mini (mac)"
			if name == "no other match" {
				want = "Mac mini"
			}
			if input.MachineName != want || !strings.Contains(ix.Summary, want) {
				t.Fatalf("machine_name %q, summary %q; want %q", input.MachineName, ix.Summary, want)
			}
		})
	}
}

// ---- the computer the conversation runs on disconnects ----

// The computer the conversation runs on disconnects: the call came over that computer's node connection, whose end
// cancels the call's context before the node is detached. The call ends with an error, the card leaves the queue, an
// answer after that finds it over, and nothing runs.
func TestSecretRunCallEndsWithItsConnection(t *testing.T) {
	r := newSecretRig(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan proto.ToolResult, 1)
	go func() {
		done <- r.s.Tool(ctx, ToolCall{Name: "secret_run", MachineID: "server", SessionID: "s1", Arguments: json.RawMessage(secretArgs)})
	}()
	ix := r.card()
	cancel()                   // the connection's end (devices: conn.close)
	r.s.NodeDetached("server") // then the detach
	select {
	case res := <-done:
		if !res.IsError || !strings.Contains(resultText(res), "context canceled") {
			t.Fatalf("result %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end with its connection")
	}
	waitFor(t, "the card off the queue", func() bool {
		_, pending := r.s.pendingIx(ix.ID)
		return !pending
	})
	if w := r.post(ix.ID, `{"allow":true,"values":{"password":"`+testPassword+`"}}`); w.Code != http.StatusGone {
		t.Fatalf("a late answer: %d %s, want 410", w.Code, w.Body)
	}
	r.wantNoRun()
}
