package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// reportingWithTask is a reporting conversation lark1 (working) whose task's agent s1 on box is working.
func reportingWithTask(t *testing.T) (*Service, *fakeHub) {
	t.Helper()
	s, f := newQuietService(t)
	ctx := context.Background()
	f.known["lark1"] = true
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusWorking})
	s.markReporter(ctx, "server", "lark1")
	if _, err := s.toolTaskPlan(ctx, "lark1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link", func() bool { return len(onlyTodo(s).Agents) == 1 })
	agentTurn(s, f, proto.StatusWorking, "", 0)
	return s, f
}

// interrupted are the sessions session.interrupt was called for, as machine/session.
func (f *fakeHub) interrupted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.Method != "session.interrupt" {
			continue
		}
		var a struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(c.Params, &a)
		out = append(out, c.Device+"/"+a.SessionID)
	}
	return out
}

func (f *fakeHub) sentTo(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.sends {
		if a["session_id"] == id {
			n++
		}
	}
	return n
}

// /stop from Feishu (#3308): the conversation's turn and its task's working agent are interrupted, the result names
// the task, and the task's notices no longer go to the conversation (nor to the main one).
func TestStopReportingStopsItsTasks(t *testing.T) {
	s, f := reportingWithTask(t)
	w := serve(s, "POST", "/api/v1/assistant/conversations/lark1/stop", "")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res stopResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Conversation || res.Already || len(res.Tasks) != 1 || res.Tasks[0].Title != "修登录崩溃" || res.Tasks[0].Agents != 1 {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.Join(f.interrupted(), " "); got != "server/lark1 box/s1" {
		t.Fatalf("interrupted %q", got)
	}
	agentTurn(s, f, proto.StatusDone, "停在一半", 0)
	time.Sleep(10 * quietNoticeDelay)
	if n := f.sentTo("lark1"); n != 0 {
		t.Fatalf("the stopped conversation got %d notice(s)", n)
	}
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("the main conversation was told instead: %q", got)
	}

	w = serve(s, "GET", "/api/v1/assistant/reporters", "")
	var list struct {
		Reporters []struct {
			SessionID string     `json:"session_id"`
			MachineID string     `json:"machine_id"`
			StoppedAt *time.Time `json:"stopped_at"`
			StoppedBy string     `json:"stopped_by"`
		} `json:"reporters"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Reporters) != 1 {
		t.Fatalf("reporters: %v %s", err, w.Body)
	}
	if r := list.Reporters[0]; r.SessionID != "lark1" || r.MachineID != "server" || r.StoppedAt == nil || r.StoppedBy != "lark" {
		t.Fatalf("reporter = %+v", r)
	}

	// Nothing is going on any more: stopping again says so and interrupts nothing.
	f.setSessions("server", proto.Session{SessionID: "conv1", MachineID: "server", Assistant: true, Status: proto.StatusIdle},
		proto.Session{SessionID: "lark1", MachineID: "server", Assistant: true, Status: proto.StatusIdle})
	before := len(f.interrupted())
	w = serve(s, "POST", "/api/v1/assistant/conversations/lark1/stop", "")
	res = stopResult{}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if w.Code != 200 || !res.Already || res.Conversation || len(res.Tasks) != 0 || len(f.interrupted()) != before {
		t.Fatalf("second stop: %d %s", w.Code, w.Body)
	}

	// The user talks in it again (a new /agent in the thread goes on in it): its tasks are told to it again.
	s.UserSpoke(context.Background(), SessionRef{MachineID: "server", SessionID: "lark1"})
	if s.reporterStopped(context.Background(), "lark1") {
		t.Fatal("still stopped after the user spoke")
	}
}

// Notices already queued behind the conversation's turn go when it is stopped.
func TestStopReportingDropsQueuedNotices(t *testing.T) {
	s, f := reportingWithTask(t)
	agentTurn(s, f, proto.StatusDone, "MR 提好了", 0)
	waitFor(t, "the queued notice", func() bool {
		q, _ := s.queuedRelays()
		return len(q["lark1"]) == 1
	})
	if w := serve(s, "POST", "/api/v1/assistant/conversations/lark1/stop", ""); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if q, _ := s.queuedRelays(); len(q["lark1"]) != 0 {
		t.Fatalf("still queued: %q", q["lark1"])
	}
}

// The stop button in an app (the hub's session.user_stopped): the turn is stopped already, the task's agent is
// interrupted and the conversation is marked stopped by the app.
func TestUserStoppedStopsAReportingConversation(t *testing.T) {
	s, f := reportingWithTask(t)
	s.UserStopped(context.Background(), SessionRef{MachineID: "server", SessionID: "lark1"})
	if got := strings.Join(f.interrupted(), " "); got != "box/s1" {
		t.Fatalf("interrupted %q", got)
	}
	b := &s.reporters
	b.mu.Lock()
	r := b.m["lark1"]
	b.mu.Unlock()
	if r.StoppedAt == nil || r.StoppedBy != "app" {
		t.Fatalf("reporter = %+v", r)
	}

	// Any other conversation stopped in the app is left alone.
	s.UserStopped(context.Background(), SessionRef{MachineID: "server", SessionID: "conv1"})
	if got := strings.Join(f.interrupted(), " "); got != "box/s1" {
		t.Fatalf("interrupted %q", got)
	}
}

func TestStopNotAReportingConversation(t *testing.T) {
	s, _ := newQuietService(t)
	if w := serve(s, "POST", "/api/v1/assistant/conversations/conv1/stop", ""); w.Code != 404 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// Clearing the main conversation's context (#5095) closes its own process only: a Feishu request it reports on is not
// stopped, so the Agents working on the tasks it handed out go on (unlike the Stop button above).
func TestClearMainLeavesItsTasksRunning(t *testing.T) {
	s, f := reportingWithTask(t)
	ctx := context.Background()
	if _, err := s.updateSettings(ctx, func(v *Settings) error {
		v.MachineID, v.Main, v.MainMachineID = "server", "lark1", "server"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := serve(s, "POST", "/api/v1/assistant/main/clear", `{"expect":"lark1"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"archived":"lark1"`) {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	if got := f.interrupted(); len(got) != 0 {
		t.Fatalf("interrupted %q", got)
	}
	if s.reporterStopped(ctx, "lark1") {
		t.Fatal("the reporting conversation was stopped")
	}
	f.mu.Lock()
	closed := 0
	for _, c := range f.calls {
		if c.Method == "session.close" && strings.Contains(string(c.Params), `"lark1"`) {
			closed++
		}
	}
	f.mu.Unlock()
	if closed != 1 {
		t.Fatalf("session.close for lark1: %d", closed)
	}
}
