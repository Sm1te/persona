package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// terminalRefusal is the node's refusal of a start whose new tab's shell had no launcher (node/shellmiss.go, #1642).
func terminalRefusal(code, launcher string) *rpc.Error {
	data, _ := json.Marshal(map[string]string{"delivered": "none", "launcher": launcher, "missing": launcher, "kind": "not_found",
		"where": "terminal", "pane_shell": "bash", "launcher_shell": "zsh", "rc_file": "~/.zshrc", "line": "bash: " + launcher + ": command not found"})
	return &rpc.Error{Code: code, Message: "the new tab's shell (bash) has no command " + launcher, Data: data}
}

func checkedNow(f *fakeHub) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.checked...)
}

// A launcher the terminal cannot run (#1642) is its own class: blocked at once, with no follow-up check (the check runs
// in the login shell, where it works) and no repair; the rounds leave it alone, a check asked for by hand does not
// clear it, and work goes to a machine where it runs. A start with it that works clears it.
func TestLauncherHealthNotInTerminal(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	delete(f.checks, "l4/work")
	f.hello.Agents.LauncherHealth.Repair.Skill = "fix-launchers" // a repair agent there is, and it is not started
	f.repairRes = &proto.LauncherRepair{OK: true}
	ctx := context.Background()

	s.recordStartFailure(ctx, "l4", "work", terminalRefusal(proto.CodeLauncherMissing, "work"))
	// It stays pickable, with its record: only a real start clears this one, so hiding it would leave no way to make
	// that start, and the clients gray it out with the reason.
	var picker struct {
		Launchers []launcherWithHealth `json:"launchers"`
	}
	if err := json.Unmarshal(getJSON(t, s, "/api/v1/machines/l4/launchers"), &picker); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(picker.Launchers, func(l launcherWithHealth) bool { return l.Name == "work" })
	if i < 0 || picker.Launchers[i].Health == nil || picker.Launchers[i].Health.State != healthBlocked ||
		picker.Launchers[i].Health.Kind != errKindNotInTerminal {
		t.Fatalf("the picker hid a launcher only a start can clear, or lost its reason: %+v", picker.Launchers)
	}
	r := healthState(s, "l4", "work")
	if r.State != healthBlocked || r.Kind != errKindNotInTerminal || r.Source != healthSourceStart ||
		!strings.Contains(r.BlockedReason, "terminal") || !strings.Contains(r.Error, "has no command work") {
		t.Fatalf("recorded %+v", r)
	}
	time.Sleep(200 * time.Millisecond) // a follow-up check would have run by now
	if got := checkedNow(f); len(got) != 0 {
		t.Fatalf("checked in the login shell after a terminal failure: %v", got)
	}

	s.healthRound(ctx, true)
	if got := checkedNow(f); strings.Contains(strings.Join(got, " "), "l4/work") {
		t.Errorf("a round checked it: %v", got)
	}
	f.mu.Lock()
	repairs, starts := len(f.repairs), len(f.starts)
	f.mu.Unlock()
	if repairs != 0 || starts != 0 {
		t.Errorf("repaired: %d repairs, %d repair agents", repairs, starts)
	}
	postJSON(t, s, "/api/v1/launchers/health/check?machine=l4&launcher=work") // the check passes in the login shell
	if r := healthState(s, "l4", "work"); r.State != healthBlocked || r.Kind != errKindNotInTerminal {
		t.Fatalf("a passing check cleared it: %+v", r)
	}
	if alt, note, moved := s.healthDetour(ctx, Device{ID: "l4", Home: "/home/me"}, "work"); !moved || alt.ID == "l4" ||
		!strings.Contains(note, errKindNotInTerminal) {
		t.Errorf("not moved: %s %q %v", alt.ID, note, moved)
	}

	s.recordStartSuccess(ctx, "box", "work") // elsewhere: nothing to clear
	s.recordStartSuccess(ctx, "l4", "work")
	if r := healthState(s, "l4", "work"); r.State != healthOK || r.Kind != "" || r.Source != healthSourceStart {
		t.Fatalf("a start that worked did not clear it: %+v", r)
	}
	// A missing claude in the terminal is the same class; a start that works does not clear any other failure.
	s.recordStartFailure(ctx, "l4", "work", terminalRefusal(proto.CodeClaudeMissing, "work"))
	if r := healthState(s, "l4", "work"); r.Kind != errKindNotInTerminal {
		t.Errorf("claude missing in the terminal: %+v", r)
	}
	s.recordHealth(ctx, "box", "work", healthResult{err: "API Error: 401 invalid api key", source: healthSourceCheck})
	s.recordStartSuccess(ctx, "box", "work")
	if r := healthState(s, "box", "work"); r.State == healthOK || r.Kind != errKindAuth {
		t.Errorf("a start cleared an auth failure: %+v", r)
	}
	// The same codes without where (the headless assistant's) are no terminal's.
	plain := &rpc.Error{Code: proto.CodeLauncherMissing, Message: "zsh:1: command not found: work", Data: json.RawMessage(`{"launcher":"work"}`)}
	if terminalMissing(plain) || !terminalMissing(terminalRefusal(proto.CodeLauncherMissing, "work")) {
		t.Error("terminalMissing")
	}
}

// The app's starts count too: one the hub waited for (session.start_ended) and one answered at once, followed in the
// session list from starting to failed (counted once while it is listed) or started.
func TestLauncherHealthRecordsAppStarts(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	delete(f.checks, "l4/work")
	ctx := context.Background()

	r := terminalRefusal(proto.CodeLauncherMissing, "work")
	s.StartEnded(ctx, StartEnded{MachineID: "l4", Launcher: "work", Error: r.Message, Code: r.Code, Data: r.Data})
	if h := healthState(s, "l4", "work"); h.Kind != errKindNotInTerminal || h.State != healthBlocked {
		t.Fatalf("a failed app start: %+v", h)
	}
	s.StartEnded(ctx, StartEnded{MachineID: "l4", Launcher: "work", OK: true})
	if h := healthState(s, "l4", "work"); h.State != healthOK {
		t.Fatalf("a start that worked: %+v", h)
	}
	s.StartEnded(ctx, StartEnded{MachineID: "box", Error: "claude exited before showing its input box"})
	if h := healthState(s, "box", "claude"); h.State != healthFailing || h.Source != healthSourceStart {
		t.Fatalf("an app start of claude that failed: %+v", h)
	}
	s.StartEnded(ctx, StartEnded{MachineID: "server", Launcher: "work", Error: "directory /nope does not exist"})
	if _, ok := s.launcherHealth(ctx, "server", "work"); ok {
		t.Fatal("a refused request counted")
	}

	// Answered at once: starting, then failed with the refusal's code.
	starting := proto.Session{SessionID: "a1", Launcher: "work", Starting: true, Status: proto.StatusIdle}
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{starting}})
	if h := healthState(s, "l4", "work"); h.State != healthOK {
		t.Fatalf("a start still starting counted: %+v", h)
	}
	failed := proto.Session{SessionID: "a1", Launcher: "work", Status: proto.StatusOffline, StartError: r.Message,
		StartErrorCode: r.Code, StartErrorArgs: map[string]string{"launcher": "work", "where": "terminal", "pane_shell": "bash"},
		UpdatedAt: time.Now().UTC()}
	for range 3 { // listed for a while
		s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{failed}})
	}
	if h := healthState(s, "l4", "work"); h.Kind != errKindNotInTerminal || h.Fails != 1 {
		t.Fatalf("a failed async start: %+v", h)
	}
	// Another failure, listed with an updated_at an hour behind the manager's clock (the node's clock is behind):
	// counted all the same, whatever the record's checked_at.
	behind := failed
	behind.SessionID, behind.UpdatedAt = "a0", time.Now().Add(-time.Hour)
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{behind, failed}})
	if h := healthState(s, "l4", "work"); h.Fails != 2 {
		t.Fatalf("a failure whose node clock is behind: %+v", h)
	}
	// Another start, which comes up: the terminal runs it now.
	again := starting
	again.SessionID = "a2"
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{again}})
	up := proto.Session{SessionID: "a2", Launcher: "work", Status: proto.StatusIdle}
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{up}})
	if h := healthState(s, "l4", "work"); h.State != healthOK {
		t.Fatalf("a start that came up did not clear it: %+v", h)
	}
}

// A running Agent moving to another launcher is listed as starting meanwhile on the launcher it moves to (Starting with
// its pending switch, node/launcher_switch.go): it is not followed, since the row listed clean afterwards may be back on
// its own launcher (a switch that failed and rolled back): no start of the launcher the starting row named.
func TestLauncherHealthLeavesSwitchesOut(t *testing.T) {
	s, f := launcherHealthHubOn(t, newStorelessService)
	delete(f.checks, "l4/work")
	ctx := context.Background()
	// a launcher whose starts did not run in the terminal: a start that works is what clears it
	r := terminalRefusal(proto.CodeLauncherMissing, "work")
	s.StartEnded(ctx, StartEnded{MachineID: "l4", Launcher: "work", Error: r.Message, Code: r.Code, Data: r.Data})
	if h := healthState(s, "l4", "work"); h.Kind != errKindNotInTerminal || h.State != healthBlocked {
		t.Fatalf("a failed start: %+v", h)
	}
	switching := proto.Session{SessionID: "a1", Launcher: "work", Starting: true, Status: proto.StatusIdle,
		PendingSwitch: &proto.PendingSwitch{Launcher: "work", At: time.Now()}}
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{switching}})
	back := proto.Session{SessionID: "a1", Launcher: "claude", Status: proto.StatusIdle}
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{back}})
	if h := healthState(s, "l4", "work"); h.State != healthBlocked {
		t.Fatalf("a switch counted as a start that worked: %+v", h)
	}
}

// The failed app starts counted are kept with the records: after a restart, one the node still lists is not counted
// again, so a start that worked since stays the latest word.
func TestLauncherHealthCountsAnAppStartOnceAcrossARestart(t *testing.T) {
	s, f := launcherHealthHubOn(t, newTestService)
	delete(f.checks, "l4/work")
	ctx := context.Background()
	r := terminalRefusal(proto.CodeLauncherMissing, "work")
	failed := proto.Session{SessionID: "a1", Launcher: "work", Status: proto.StatusOffline, StartError: r.Message,
		StartErrorCode: r.Code, StartErrorArgs: map[string]string{"launcher": "work", "where": "terminal", "pane_shell": "bash"},
		UpdatedAt: time.Now().Add(-time.Hour).UTC()}
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{failed}})
	if h := healthState(s, "l4", "work"); h.Kind != errKindNotInTerminal || h.Fails != 1 {
		t.Fatalf("a failed async start: %+v", h)
	}
	s.StartEnded(ctx, StartEnded{MachineID: "l4", Launcher: "work", OK: true}) // the user fixed the rc file
	// A restart: what memory had is gone, the store keeps the rest.
	s.health.mu.Lock()
	s.health.loaded, s.health.recs, s.health.repairs, s.health.counted, s.health.starting = false, nil, nil, nil, nil
	s.health.mu.Unlock()
	s.followStarts(ctx, DeviceSessions{MachineID: "l4", Sessions: []proto.Session{failed}})
	if h := healthState(s, "l4", "work"); h.State != healthOK {
		t.Fatalf("counted again after a restart: %+v", h)
	}
}

// A task whose agent the terminal could not start keeps the refusal's code on its plan, and the todo says why in the
// user's language (the hub's sentence, not the node's English).
func TestTaskStartKeepsTheTerminalRefusal(t *testing.T) {
	s, f := newTaskService(t)
	f.failStart = terminalRefusal(proto.CodeLauncherMissing, "work")
	ctx := context.Background()
	if _, err := s.toolTaskPlan(ctx, "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task to fail", func() bool { return onlyTodo(s).StartError != "" })
	if why := onlyTodo(s).StartError; !strings.Contains(why, "终端标签页") || !strings.Contains(why, "「work」") ||
		!strings.HasSuffix(why, "添加为模型供应商。") {
		t.Errorf("todo says %q", why)
	}
	var p *TaskPlan
	waitFor(t, "the plan to keep the error", func() bool {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		for _, plan := range s.tasks.plans {
			p = plan
		}
		return p != nil && p.Tasks[0].Error != ""
	})
	if task := p.Tasks[0]; task.ErrorCode != proto.CodeLauncherMissing || task.ErrorArgs["where"] != "terminal" ||
		task.ErrorArgs["delivered"] != "" || !strings.Contains(task.Error, "has no command work") {
		t.Errorf("task %+v", task)
	}
	code, args := codedStartRefusal(errors.New("boom"))
	if code != "" || args != nil {
		t.Errorf("a plain error: %q %v", code, args)
	}
}

// The English sentence is longer than a summary's clip: the todo keeps it whole, remedy and all.
func TestTaskStartKeepsTheWholeEnglishRefusal(t *testing.T) {
	s, f := newTaskService(t)
	f.mu.Lock()
	f.hello.Locale = "en"
	f.mu.Unlock()
	f.failStart = terminalRefusal(proto.CodeLauncherMissing, "work")
	if _, err := s.toolTaskPlan(context.Background(), "conv1", planArgs(true)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task to fail", func() bool { return onlyTodo(s).StartError != "" })
	if why := onlyTodo(s).StartError; len([]rune(why)) <= 200 || !strings.HasSuffix(why, "or add it to Persona as a model provider.") {
		t.Errorf("todo says %q", why)
	}
}

// A todo's agent that the terminal could not start (one the app started, answered at once) keeps the same sentence as
// the session list, not the node's English: in memory and in the store.
func TestTodoAgentKeepsTheWordedStartError(t *testing.T) {
	todoStores(t, func(t *testing.T, e *todoEnv, database *db.DB) {
		e.f.setSessions("macbook", proto.Session{SessionID: "s1", Launcher: "work", Starting: true, Cwd: "/code", Agent: "claude"})
		todo := e.create("todo A")
		e.must("PUT", "/api/v1/todos/"+todo.ID+"/agents/macbook/s1", "")
		r := terminalRefusal(proto.CodeLauncherMissing, "work")
		e.sessions("macbook", proto.Session{SessionID: "s1", Launcher: "work", Status: proto.StatusOffline, Cwd: "/code", Agent: "claude",
			StartError: r.Message, StartErrorCode: r.Code, StartErrorArgs: map[string]string{"launcher": "work", "where": "terminal",
				"pane_shell": "bash", "launcher_shell": "zsh", "rc_file": "~/.zshrc"}})
		waitFor(t, "the agent's start error", func() bool {
			a := agentOf(*e.get(todo.ID), "s1")
			return a != nil && a.StartError != ""
		})
		if why := agentOf(*e.get(todo.ID), "s1").StartError; !strings.Contains(why, "终端标签页（bash）里没有「work」") {
			t.Errorf("agent says %q", why)
		}
		if database == nil {
			return
		}
		waitFor(t, "the stored start error", func() bool {
			agents, err := database.ListAllTodoAgents(context.Background())
			return err == nil && len(agents) == 1 && strings.Contains(agents[0].StartError, "终端标签页（bash）")
		})
	})
}

// A failed start's start_error in the apps' lists is the hub's sentence for its code, in the user's language and
// naming the machine; one without a code stays the node's text.
func TestStartErrorTextWordsTheCode(t *testing.T) {
	sess := proto.Session{StartError: "the new tab's shell (bash) has no command work", StartErrorCode: proto.CodeLauncherMissing,
		StartErrorArgs: map[string]string{"launcher": "work", "where": "terminal", "pane_shell": "bash", "launcher_shell": "zsh", "rc_file": "~/.zshrc"}}
	if got := startErrorText("en", "Lab box", sess); !strings.Contains(got, "new terminal tabs on Lab box (bash) has no command “work”") {
		t.Errorf("en: %q", got)
	}
	if got := startErrorText("ja", "Lab box", sess); !strings.Contains(got, "Lab box の新しいターミナルタブ（bash）には「work」") {
		t.Errorf("ja: %q", got)
	}
	sess.StartErrorCode = "something_else"
	if got := startErrorText("en", "Lab box", sess); got != sess.StartError {
		t.Errorf("an unknown code: %q", got)
	}
}
