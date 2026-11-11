package assistant

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/proto"
)

// The Live Activity's content as the manager works it out (fleet_live.go), moved from the hub's tests.

func TestBuildFleet(t *testing.T) {
	t0 := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	agent := func(id, state string, mod func(*proto.Session)) fleetInput {
		s := proto.Session{MachineID: "m", SessionID: id, Title: "agent " + id, State: state, UpdatedAt: t0, SpaceName: "web", SlotName: "2", SlotIndex: 2}
		if mod != nil {
			mod(&s)
		}
		return fleetInput{Machine: "box", Session: &s}
	}
	agents := []fleetInput{
		agent("w1", proto.StateWorking, nil),
		agent("w2", proto.StateWorking, nil),
		agent("p", proto.StateNeedsYou, nil),
		agent("u", proto.StateUnread, func(s *proto.Session) { s.LastText = "All tests pass.\nDetails…" }),
		agent("idle", proto.StateIdle, nil),
		agent("off", proto.StateOffline, nil),
		agent("loop", proto.StateUnread, func(s *proto.Session) { s.Loop = &proto.LoopInfo{Kind: "wakeup"} }),
		agent("asst", proto.StateWorking, func(s *proto.Session) { s.Assistant = true }),
	}
	agents[0].TurnStart = t0.Add(-5 * time.Minute) // w1 runs longer
	agents[1].TurnStart = t0.Add(-1 * time.Minute)
	muted := agent("muted", proto.StateWorking, nil)
	muted.Muted = true
	agents = append(agents, muted)
	ixs := []proto.Interaction{{ID: "ix1", MachineID: "m", SessionID: "p", Kind: proto.IxPermission, Summary: "Bash: go test ./...", CreatedAt: t0}}

	st := buildFleet(agents, ixs)
	if st.Phase != "needsYou" || st.NeedsYou != 1 || st.Working != 2 || st.Unread != 1 {
		t.Fatalf("counts %+v", st)
	}
	if len(st.Items) != fleetMaxItems {
		t.Fatalf("items %d", len(st.Items))
	}
	first := st.Items[0]
	if first.ID != "m/p" || first.Kind != proto.IxPermission || first.Interaction != "ix1" || first.Detail != "Bash: go test ./..." {
		t.Fatalf("first %+v", first)
	}
	if first.Place != "box · web" { // slot "2" is just the index: not repeated
		t.Fatalf("place %q", first.Place)
	}
	if st.Items[1].ID != "m/w1" || st.Items[2].ID != "m/w2" {
		t.Fatalf("working order %+v", st.Items)
	}
	if st.WorkingSince != float64(t0.Add(-5*time.Minute).Unix()) {
		t.Fatalf("working since %v", st.WorkingSince)
	}

	// Without the request and the workers: only the finished one, its first line.
	st = buildFleet([]fleetInput{agents[3], agents[4]}, nil)
	if st.Phase != "done" || len(st.Items) != 1 || st.Items[0].Detail != "All tests pass." {
		t.Fatalf("done %+v", st)
	}
	if st := buildFleet([]fleetInput{agents[4], agents[5], agents[6], agents[7], muted}, nil); st.Phase != "" || len(st.Items) != 0 {
		t.Fatalf("should be empty: %+v", st)
	}
	// A blocked agent with no interaction yet counts as a terminal prompt.
	st = buildFleet([]fleetInput{agent("b", proto.StateNeedsYou, nil)}, nil)
	if st.Phase != "needsYou" || st.Items[0].Kind != proto.IxScreen {
		t.Fatalf("screen %+v", st)
	}
	// An interaction on a muted agent is left out.
	if st := buildFleet([]fleetInput{muted}, []proto.Interaction{{ID: "x", MachineID: "m", SessionID: "muted", Kind: proto.IxPlan}}); st.Phase != "" {
		t.Fatalf("muted interaction %+v", st)
	}
}

func TestRound2(t *testing.T) {
	for in, want := range map[int64]int64{7: 7, 99: 99, 987: 990, 12345: 12000, 41614712: 42000000} {
		if got := round2(in); got != want {
			t.Errorf("round2(%d) = %d, want %d", in, got, want)
		}
	}
}

// A working item whose turn ended for its background work says so, and what runs (#2935).
func TestBuildFleetBackground(t *testing.T) {
	run := proto.Workflow{TaskID: "w1", Name: "parity-audit", Running: true, Status: proto.WorkflowRunning, Phase: "Verify",
		AgentsStarted: 185, AgentsDone: 169}
	agents := []fleetInput{
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "wf", State: proto.StateWorking, BackgroundWorking: true,
			Workflows: []proto.Workflow{run}}},
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "sh", State: proto.StateWorking, BackgroundWorking: true,
			Background: []proto.BackgroundTask{{ID: "b1", Kind: "shell"}, {ID: "b2", Kind: "monitor"}}}},
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "busy", State: proto.StateWorking}},
	}
	st := buildFleetIn("zh-Hans", agents, nil)
	got := map[string]FleetItem{}
	for _, it := range st.Items {
		got[it.ID] = it
	}
	if it := got["m/wf"]; !it.Background || it.Detail != "parity-audit · Verify · 169/185" {
		t.Errorf("workflow: %+v", it)
	}
	// The bar under it (#3437): the same counts as the line.
	if p := got["m/wf"].Progress; p == nil || *p != (FleetProgress{Done: 169, Started: 185}) {
		t.Errorf("workflow progress: %+v", p)
	}
	if it := got["m/sh"]; !it.Background || it.Detail != "后台 2 个任务在跑" || it.Progress != nil {
		t.Errorf("background tasks: %+v", it)
	}
	if it := got["m/busy"]; it.Background || it.Detail != "" || it.Progress != nil {
		t.Errorf("a plain working one: %+v", it)
	}
}

// The bar is the workflow the line names (the first running one, Workflows being newest first), counted the way the
// line counts it; none before the workflow started an agent, none for a foreground turn (#3437).
func TestBuildFleetBackgroundProgress(t *testing.T) {
	newer := proto.Workflow{TaskID: "w2", Name: "review", Running: true, Phase: "Verify", AgentsStarted: 3, AgentsDone: 1}
	older := proto.Workflow{TaskID: "w1", Name: "parity-audit", Running: true, Phase: "Audit", AgentsStarted: 185, AgentsDone: 169}
	ended := proto.Workflow{TaskID: "w0", Name: "docs-sweep", AgentsStarted: 4, AgentsDone: 4}
	fresh := proto.Workflow{TaskID: "w3", Name: "fresh", Running: true}
	for _, tc := range []struct {
		name       string
		background bool
		runs       []proto.Workflow
		detail     string
		want       *FleetProgress
	}{
		{"the first running one", true, []proto.Workflow{ended, newer, older}, "review · Verify · 1/3", &FleetProgress{Done: 1, Started: 3}},
		{"no agent started yet", true, []proto.Workflow{fresh, older}, "fresh · 0/0", nil},
		{"only ended runs", true, []proto.Workflow{ended}, "后台 0 个任务在跑", nil},
		{"foreground turn", false, []proto.Workflow{older}, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := proto.Session{MachineID: "m", SessionID: "s", State: proto.StateWorking, BackgroundWorking: tc.background, Workflows: tc.runs}
			st := buildFleetIn("zh-Hans", []fleetInput{{Session: &s}}, nil)
			if len(st.Items) != 1 {
				t.Fatalf("items: %+v", st.Items)
			}
			it := st.Items[0]
			if it.Detail != tc.detail {
				t.Errorf("detail = %q, want %q", it.Detail, tc.detail)
			}
			if (it.Progress == nil) != (tc.want == nil) || (it.Progress != nil && *it.Progress != *tc.want) {
				t.Errorf("progress = %+v, want %+v", it.Progress, tc.want)
			}
		})
	}
}

// The bar costs no push (docs/live-activity.md "Push budget"): it is worked out from the counts the detail line already
// prints, so along a workflow's life the content changes exactly as often with it as without it. That holds for a name
// or phase long enough to be clipped too, and for a workflow known only by its summary: clipping cuts them, never the
// counts (#3437 review).
func TestBuildFleetProgressAddsNoChanges(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	long := strings.Repeat("cross-platform-regression-audit-", 2)                     // 64 runes: over the line by itself
	longPhase := "Verify compatibility and integration behaviour across every client" // clipped after a short name
	for _, tc := range []struct{ label, name, summary, phase string }{
		{"short name", "parity-audit", "", "Verify"},
		{"long name", long, "", "Verify"},
		{"long phase", "cross-platform-regression-audit", "", longPhase},
		{"long summary", "", strings.Repeat("Audit every page against the design system, ", 3), "Fix"},
		{"no name", "", "", ""},
	} {
		t.Run(tc.label, func(t *testing.T) {
			step := func(runs ...proto.Workflow) fleetInput {
				return fleetInput{Machine: "box", TurnStart: at, Session: &proto.Session{MachineID: "m", SessionID: "s", State: proto.StateWorking,
					BackgroundWorking: true, UpdatedAt: at, Workflows: runs}}
			}
			wf := func(phase bool, done, started int) proto.Workflow {
				w := proto.Workflow{TaskID: "w1", Name: tc.name, Summary: tc.summary, Running: true, StartedAt: at, AgentsDone: done, AgentsStarted: started}
				if phase {
					w.Phase = tc.phase
				}
				return w
			}
			other := proto.Workflow{TaskID: "w0", Name: "docs-sweep", Running: true, StartedAt: at.Add(-time.Hour), AgentsDone: 1, AgentsStarted: 2}
			steps := []fleetInput{
				step(wf(false, 0, 0)), step(wf(true, 0, 4)), step(wf(true, 0, 4)), step(wf(true, 1, 4)), step(wf(true, 2, 4)),
				step(wf(true, 2, 9)), step(wf(true, 2, 9), other), step(wf(true, 3, 9), other), step(wf(true, 9, 9), other),
				// The older run moves while the newer one, the one the line names, stands still: neither line nor bar changes.
				step(wf(true, 9, 9), proto.Workflow{TaskID: "w0", Name: "docs-sweep", Running: true, StartedAt: at.Add(-time.Hour), AgentsDone: 2, AgentsStarted: 2}),
			}
			changes := func(withBar bool) int {
				n, last := 0, ""
				for _, in := range steps {
					st := buildFleetIn("en", []fleetInput{in}, nil)
					if !withBar {
						for i := range st.Items {
							st.Items[i].Progress = nil
						}
					}
					b, _ := json.Marshal(st)
					if string(b) != last {
						n, last = n+1, string(b)
					}
				}
				return n
			}
			if with, without := changes(true), changes(false); with != without {
				t.Fatalf("content changes with the bar %d, without %d", with, without)
			}
			// Each line, however clipped, still ends with the counts the bar draws, and stays within the usual length.
			for _, in := range steps {
				it := buildFleetIn("en", []fleetInput{in}, nil).Items[0]
				w := in.Session.Workflows[0]
				if want := fmt.Sprintf(" · %d/%d", w.AgentsDone, w.AgentsStarted); !strings.HasSuffix(it.Detail, want) {
					t.Errorf("detail %q lost its counts %q", it.Detail, want)
				}
				if n := utf8.RuneCountInString(it.Detail); n > fleetDetailMax+1 {
					t.Errorf("detail %q is %d runes", it.Detail, n)
				}
			}
		})
	}
}

// A name too long for the line is what gets clipped: "<name>… · 169/185", as the phone prints it (#3437 review).
func TestBuildFleetBackgroundDetailKeepsCounts(t *testing.T) {
	name := strings.Repeat("n", 55)
	s := proto.Session{MachineID: "m", SessionID: "s", State: proto.StateWorking, BackgroundWorking: true,
		Workflows: []proto.Workflow{{TaskID: "w1", Name: name, Running: true, Phase: "Verify", AgentsDone: 169, AgentsStarted: 185}}}
	it := buildFleetIn("en", []fleetInput{{Session: &s}}, nil).Items[0]
	if want := name[:50] + "… · 169/185"; it.Detail != want {
		t.Errorf("detail = %q, want %q", it.Detail, want)
	}
	if p := it.Progress; p == nil || *p != (FleetProgress{Done: 169, Started: 185}) {
		t.Errorf("progress = %+v", p)
	}
}

// An agent with two cards up is one agent that needs you, as in the Agents list: one item, its oldest card (#3571).
func TestBuildFleetOneItemPerAgent(t *testing.T) {
	t0 := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	agents := []fleetInput{
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "a", State: proto.StateNeedsYou}},
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "b", State: proto.StateNeedsYou}},
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "w", State: proto.StateWorking}},
	}
	ixs := []proto.Interaction{ // oldest first, as pendingInteractions gives them
		{ID: "a1", MachineID: "m", SessionID: "a", Kind: proto.IxPermission, Summary: "first", CreatedAt: t0},
		{ID: "a2", MachineID: "m", SessionID: "a", Kind: proto.IxPermission, Summary: "second", CreatedAt: t0.Add(time.Second)},
		{ID: "b1", MachineID: "m", SessionID: "b", Kind: proto.IxQuestion, CreatedAt: t0.Add(2 * time.Second)},
		{ID: "a3", MachineID: "m", SessionID: "a", Kind: proto.IxPlan, CreatedAt: t0.Add(3 * time.Second)},
	}
	st := buildFleet(agents, ixs)
	if st.NeedsYou != 2 || st.Working != 1 {
		t.Fatalf("counts %+v", st)
	}
	if len(st.Items) != 3 || st.Items[0].ID != "m/a" || st.Items[0].Interaction != "a1" || st.Items[1].ID != "m/b" || st.Items[2].ID != "m/w" {
		t.Fatalf("items %+v", st.Items)
	}
	// An elicitation is left out before the dedup: the agent's next card still stands for it.
	st = buildFleet(agents[:1], []proto.Interaction{
		{ID: "e1", MachineID: "m", SessionID: "a", Kind: proto.IxElicitation, CreatedAt: t0},
		{ID: "a1", MachineID: "m", SessionID: "a", Kind: proto.IxPermission, CreatedAt: t0.Add(time.Second)},
	})
	if st.NeedsYou != 1 || len(st.Items) != 1 || st.Items[0].Interaction != "a1" {
		t.Fatalf("after an elicitation %+v", st.Items)
	}
}

func TestBuildFleetWorkflowSince(t *testing.T) {
	at := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	turn := at.Add(-time.Hour)
	earlier, later := at.Add(-20*time.Minute), at.Add(-5*time.Minute)
	runs := []proto.Workflow{
		{Running: false, StartedAt: at.Add(-2 * time.Hour)},
		{Running: true, StartedAt: later, Name: "review", Phase: "Verify", AgentsStarted: 3, AgentsDone: 1},
		{Running: true},
		{Running: true, StartedAt: earlier},
	}
	for _, tc := range []struct {
		name       string
		background bool
		turn       time.Time
		runs       []proto.Workflow
		want       time.Time
	}{
		{"earliest running workflow", true, turn, runs, earlier},
		{"foreground turn", false, turn, runs, turn},
		{"no running workflow", true, turn, runs[:1], turn},
		{"unknown workflow start", true, turn, []proto.Workflow{{Running: true}}, turn},
		{"no workflow", true, turn, nil, turn},
		{"no turn start", true, time.Time{}, runs, earlier},
		{"fallback to updated", true, time.Time{}, nil, at},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := proto.Session{MachineID: "m", SessionID: "s", State: proto.StateWorking, UpdatedAt: at,
				BackgroundWorking: tc.background, Workflows: tc.runs}
			st := buildFleetIn("zh-Hans", []fleetInput{{Session: &s, TurnStart: tc.turn}}, nil)
			if len(st.Items) != 1 {
				t.Fatalf("items: %+v", st.Items)
			}
			if got, want := st.Items[0].Since, unixSeconds(tc.want); got != want || st.WorkingSince != want {
				t.Errorf("since = %v, workingSince = %v, want %v", got, st.WorkingSince, want)
			}
			if tc.name == "earliest running workflow" && st.Items[0].Detail != "review · Verify · 1/3" {
				t.Errorf("detail changed: %q", st.Items[0].Detail)
			}
		})
	}
}

// A card with fields (the password card, a mail account's) is filled in in the app, and a hand-off card is approved on
// the card only: their rows on the Live Activity name no interaction, so the activity draws no Allow / Deny for them
// (FleetActionRow); the rows themselves stay.
func TestBuildFleetOffersNoAllowForACardWithFields(t *testing.T) {
	agents := []fleetInput{{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "p", Title: "Persona", State: proto.StateNeedsYou}},
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "q", Title: "agent q", State: proto.StateNeedsYou}},
		{Machine: "box", Session: &proto.Session{MachineID: "m", SessionID: "r", Title: "agent r", State: proto.StateNeedsYou}}}
	ixs := []proto.Interaction{
		{ID: "ix_pw", MachineID: "m", SessionID: "p", Kind: proto.IxPermission, ToolName: proto.SecretRunTool, Summary: "Run on Mac mini",
			Fields: []proto.Field{{Key: "password", Secret: true}}},
		{ID: "ix_bash", MachineID: "m", SessionID: "q", Kind: proto.IxPermission, Summary: "Bash: go test ./..."},
		{ID: "ix_handoff", MachineID: "m", SessionID: "r", Kind: proto.IxPermission, ToolName: proto.SecretHandoffTool,
			Summary: "Approve on iPhone or the web"},
	}
	st := buildFleet(agents, ixs)
	if len(st.Items) != 3 || st.NeedsYou != 3 {
		t.Fatalf("items %+v", st.Items)
	}
	byID := map[string]FleetItem{}
	for _, it := range st.Items {
		byID[it.ID] = it
	}
	for _, id := range []string{"m/p", "m/r"} {
		it := byID[id]
		if it.Kind != proto.IxPermission || it.Interaction != "" {
			t.Fatalf("%s: %+v, want its row without an interaction", id, it)
		}
		raw, _ := json.Marshal(it)
		if strings.Contains(string(raw), `"interaction"`) {
			t.Fatalf("the row as the activity gets it: %s", raw)
		}
	}
	if b := byID["m/q"]; b.Interaction != "ix_bash" {
		t.Fatalf("an ordinary permission: %+v, want its interaction for Allow / Deny", b)
	}
}
