package node

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// listKeyFmt is the formatting reference for the fields tracked by listKey.
func listKeyFmt(list []proto.Session) string {
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%s|%s|%s|%d|%q|%s|%s\n", s.SessionID, s.Status, s.Title, s.PaneID, s.SpaceName, s.ParentSpaceID, s.SlotName, s.LastText, s.UpdatedAt.Unix()/10, s.Queued, s.Suggestion, s.SpawnFrom)
		fmt.Fprintf(&b, "hierarchy:%q/%q/%q/%v/%q/%q/%q/%q/%q\n", s.ParentThreadID, s.ParentSessionID, s.RootSessionID, s.IsSubagent, s.RepoRoot, s.Worktree, s.Branch, s.SpaceID, s.SpaceKind)
		if h := s.Hierarchy; h != nil {
			fmt.Fprintf(&b, "lineage:%q/%d\n", h.AskingParentSessionID, unixOf(&h.StartedAt))
		}
		if s.Activity != "" {
			fmt.Fprintf(&b, "activity:%s/%d\n", s.Activity, unixOf(s.ActivityAt))
		}
		if s.SourceKind == "runtime" {
			fmt.Fprintf(&b, "runtime-state:%q/%q/%v\n", s.Cwd, s.Launcher, s.StartedByPersona)
		}
		fmt.Fprintf(&b, "attention:%q/%q/%s\n", s.AttentionReason, s.FailureReason, s.ExternalURL)
		if s.Chat != "" || len(s.Dispatched) > 0 {
			fmt.Fprintf(&b, "botmux:%s/%s/%q\n", s.Chat, s.Topic, s.Dispatched)
		}
		pane := "none"
		if s.PaneIndex != nil {
			pane = fmt.Sprint(*s.PaneIndex)
		}
		fmt.Fprintf(&b, "pane:%s/%d/%s\n", s.SlotID, s.SlotIndex, pane)
		if c := s.Compaction; c != nil {
			fmt.Fprintf(&b, "compaction:%s/%d\n", c.State, c.StartedAt.Unix())
		}
		if len(s.Efforts) > 0 {
			fmt.Fprintf(&b, "effort:%s/%v\n", s.Effort, s.Efforts)
		}
		fmt.Fprintf(&b, "source:%s/%s/%s/%s/%v/%v/%s/%s\n", s.SourceKind, s.SourceThreadID, s.SourceInstanceID, s.ReadOnlyReason, capsFmt(s.Capabilities), s.Attached, s.CodexRoute, s.CodexRouteNote)
		for _, t := range s.Background {
			b.WriteString(t.ID + ",")
		}
		b.WriteString(fmt.Sprint(s.BackgroundWaiting) + "\n")
		for _, w := range s.Workflows {
			fmt.Fprintf(&b, "workflow:%s/%s/%q/%d/%d/%d/%d\n", w.TaskID, w.Status, w.Phase, w.AgentsDone, w.AgentsStarted, w.AgentsRunning, w.AgentsFailed)
			for _, a := range w.Agents {
				name := ""
				if a.LatestTool != nil {
					name = a.LatestTool.Name
				}
				fmt.Fprintf(&b, "%s/%s/%d,", a.ID, name, a.Calls)
			}
		}
		if s.Loop != nil {
			fmt.Fprintf(&b, "loop%d", s.Loop.NextAt.Unix())
		}
		if g := s.Goal; g != nil {
			fmt.Fprintf(&b, "goal:%s/%q/%q\n", g.Status, g.Objective, g.Reason)
		}
		if p := s.Plan; p != nil {
			fmt.Fprintf(&b, "plan:%d/%d/%q\n", p.Done, p.Total, p.Current)
		}
		for _, a := range s.Subagents {
			tool := ""
			if a.LastTool != nil {
				tool = a.LastTool.ID + ":"
			}
			fmt.Fprintf(&b, "%s:%v:%d:%s%d,", a.ID, a.Running, a.Steps, tool, a.UpdatedAt.Unix()/10)
		}
		for _, p := range s.Ports {
			fmt.Fprintf(&b, "port%d/%d/%s,", p.Port, p.PID, p.Title)
		}
	}
	return b.String()
}

// capsFmt is the capabilities as %v printed them before Modes, with Modes by value (%v of the pointer is its address).
func capsFmt(c *proto.SessionCapabilities) string {
	if c == nil {
		return "<nil>"
	}
	modes := "<nil>"
	if c.Modes != nil {
		modes = fmt.Sprint(*c.Modes)
	}
	return fmt.Sprintf("&{%v %v %v %v %v %v %s}", c.Read, c.Send, c.Interrupt, c.Approve, c.Terminal, c.Attach, modes)
}

// Capabilities are keyed by value: two rows built apart with equal modes give one key (outsideCapabilities makes a new
// Modes pointer every poll), and a change of modes, absent included, is a change.
func TestListKeyCapabilitiesModes(t *testing.T) {
	key := func(modes *bool) string {
		return listKey([]proto.Session{{SessionID: "s1", Capabilities: &proto.SessionCapabilities{Read: true, Modes: modes}}})
	}
	if key(proto.CanModes(false)) != key(proto.CanModes(false)) {
		t.Fatal("two rows with modes false give different keys")
	}
	if key(proto.CanModes(true)) != key(proto.CanModes(true)) {
		t.Fatal("two rows with modes true give different keys")
	}
	if key(proto.CanModes(false)) == key(proto.CanModes(true)) {
		t.Fatal("modes false -> true does not change the key")
	}
	if key(nil) == key(proto.CanModes(false)) || key(nil) == key(proto.CanModes(true)) {
		t.Fatal("absent modes keys the same as a set one")
	}
}

// listKeyFixture is n sessions; every other one is busy (background tasks, subagents, ports, an activity, a pane).
func listKeyFixture(n int) []proto.Session {
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	list := make([]proto.Session, n)
	for i := range list {
		s := proto.Session{SessionID: fmt.Sprintf("s%02d-0f3c9a1e-7b2d-4c5e-9a8b", i), Status: proto.StatusIdle,
			Title: fmt.Sprintf("修 listKey 的分配 %d", i), PaneID: fmt.Sprintf("%%%d", i), SpaceName: "persona",
			SlotID: "w1:t1", SlotName: "server", SlotIndex: i % 4, LastText: "已建 issue 并认领，接着看 sessions.go 的 publish",
			UpdatedAt: at.Add(time.Duration(i) * time.Minute), SourceKind: "claude"}
		if i%2 == 1 {
			pane := i % 3
			act := at.Add(time.Duration(i) * time.Second)
			s.Status = proto.StatusWorking
			s.PaneIndex = &pane
			s.Activity, s.ActivityAt = "在跑测试", &act
			s.Background = []proto.BackgroundTask{{ID: "b85polnoj"}, {ID: "b85polnok"}}
			s.Workflows = []proto.Workflow{{TaskID: "w1", Status: proto.WorkflowRunning, Phase: "Review", AgentsStarted: 4, AgentsDone: 1,
				AgentsFailed: 1, AgentsRunning: 2, Agents: []proto.WorkflowAgent{
					{ID: "a1", Status: "done", LatestTool: &proto.WorkflowTool{Name: "Read", Summary: "在读 a.go"}, Calls: 3},
					{ID: "a2", Status: "running"}}}}
			s.Subagents = []proto.Subagent{{ID: "agent-a1", Running: true, UpdatedAt: at}, {ID: "agent-a2", UpdatedAt: at}}
			s.Ports = []proto.Port{{Port: 5173, PID: 4242, Title: "Vite App"}}
		}
		list[i] = s
	}
	return list
}

// listKeyVariants returns a session and copies of it that each differ from it in one field the key covers (or in
// one that churns without meaning, which must not count).
func listKeyVariants() (proto.Session, map[string]proto.Session) {
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	act := at.Add(2 * time.Second)
	pane := 1
	base := proto.Session{SessionID: "s1", Status: proto.StatusWorking, Title: "t", PaneID: "%1", SpaceName: "sp",
		ParentSpaceID: "p", SlotID: "w1:t1", SlotName: "server", SlotIndex: 2, PaneIndex: &pane, LastText: "先看代码",
		UpdatedAt: at, Queued: []string{"再跑一次"}, Suggestion: "继续", SpawnFrom: "todo_1@mac/parent",
		Activity: "在读 tasks.go", ActivityAt: &act, AttentionReason: "等你", FailureReason: "失败", ExternalURL: "https://example.com/t",
		Chat: "oc_1", Topic: "om_1", Dispatched: []string{"om_2"},
		Compaction: &proto.Compaction{State: "running", StartedAt: at},
		Effort:     "high", Efforts: []string{"low", "high"},
		SourceKind: "codex", SourceThreadID: "th", SourceInstanceID: "in", ReadOnlyReason: "ro",
		Capabilities: &proto.SessionCapabilities{}, Attached: true, CodexRoute: "app_server", CodexRouteNote: "n",
		Background: []proto.BackgroundTask{{ID: "b1"}}, Loop: &proto.LoopInfo{NextAt: at},
		Goal:      &proto.GoalInfo{Status: "active", Objective: "o", Reason: "r"},
		Plan:      &proto.PlanInfo{Done: 3, Total: 5, Current: "Reproducing the crash", At: at},
		Subagents: []proto.Subagent{{ID: "a1", Running: true, UpdatedAt: at}},
		Ports:     []proto.Port{{Port: 3000, PID: 7, Title: "dev"}}}
	v := map[string]func(*proto.Session){
		"parent_thread_id":  func(s *proto.Session) { s.ParentThreadID = "parent" },
		"parent_session_id": func(s *proto.Session) { s.ParentSessionID = "parent-public" },
		"root_session_id":   func(s *proto.Session) { s.RootSessionID = "root-public" },
		"is_subagent":       func(s *proto.Session) { s.IsSubagent = true },
		"repo_root":         func(s *proto.Session) { s.RepoRoot = "/work/project" },
		"worktree":          func(s *proto.Session) { s.Worktree = "/work/project-fix" },
		"branch":            func(s *proto.Session) { s.Branch = "fix/x" },
		"space_id":          func(s *proto.Session) { s.SpaceID = "project-id" },
		"space_kind":        func(s *proto.Session) { s.SpaceKind = "codex_app" },
		"asking_parent":     func(s *proto.Session) { s.SetAskingParentSessionID("parent-public") },
		"started_at":        func(s *proto.Session) { s.SetStartedAt(at.Add(-time.Hour)) },

		"session_id":    func(s *proto.Session) { s.SessionID = "s2" },
		"status":        func(s *proto.Session) { s.Status = proto.StatusDone },
		"title":         func(s *proto.Session) { s.Title = "t2" },
		"pane_id":       func(s *proto.Session) { s.PaneID = "%2" },
		"space":         func(s *proto.Session) { s.SpaceName = "sp2" },
		"parent_space":  func(s *proto.Session) { s.ParentSpaceID = "" },
		"slot_name":     func(s *proto.Session) { s.SlotName = "web" },
		"last_text":     func(s *proto.Session) { s.LastText = "跑测试" },
		"updated_10s":   func(s *proto.Session) { s.UpdatedAt = at.Add(11 * time.Second) },
		"updated_churn": func(s *proto.Session) { s.UpdatedAt = at.Add(4 * time.Second) },
		"queued":        func(s *proto.Session) { s.Queued = append(s.Queued, "x") },
		"queued_none":   func(s *proto.Session) { s.Queued = nil },
		"queued_empty":  func(s *proto.Session) { s.Queued = []string{""} },
		"suggestion":    func(s *proto.Session) { s.Suggestion = "" },
		"spawn_from":    func(s *proto.Session) { s.SpawnFrom = "todo_2@mac/parent" },
		"activity":      func(s *proto.Session) { s.Activity = "在跑测试" },
		"activity_none": func(s *proto.Session) { s.Activity = "" },
		"activity_at":   func(s *proto.Session) { later := act.Add(3 * time.Second); s.ActivityAt = &later },
		"activity_nil":  func(s *proto.Session) { s.ActivityAt = nil },
		"attention":     func(s *proto.Session) { s.AttentionReason = "等你\"确认\"" },
		"failure":       func(s *proto.Session) { s.FailureReason = "" },
		"external_url":  func(s *proto.Session) { s.ExternalURL = "https://example.com/u" },
		"chat":          func(s *proto.Session) { s.Chat = "oc_2" },
		"topic":         func(s *proto.Session) { s.Topic = "om_9" },
		"dispatched":    func(s *proto.Session) { s.Dispatched = []string{"om_2", "om_3"} },
		"botmux_none":   func(s *proto.Session) { s.Chat, s.Dispatched = "", nil },
		"slot_id":       func(s *proto.Session) { s.SlotID = "w1:t2" },
		"slot_index":    func(s *proto.Session) { s.SlotIndex = 0 },
		"pane_index":    func(s *proto.Session) { zero := 0; s.PaneIndex = &zero },
		"pane_none":     func(s *proto.Session) { s.PaneIndex = nil },
		"pane_same":     func(s *proto.Session) { one := 1; s.PaneIndex = &one },
		"compaction":    func(s *proto.Session) { s.Compaction = &proto.Compaction{State: "done", StartedAt: at} },
		"compact_at":    func(s *proto.Session) { s.Compaction = &proto.Compaction{State: "running", StartedAt: act} },
		"compact_none":  func(s *proto.Session) { s.Compaction = nil },
		"effort":        func(s *proto.Session) { s.Effort = "low" },
		"efforts":       func(s *proto.Session) { s.Efforts = []string{"low", "medium", "high"} },
		"efforts_none":  func(s *proto.Session) { s.Efforts = nil },
		"source_kind":   func(s *proto.Session) { s.SourceKind = "claude" },
		"source_thread": func(s *proto.Session) { s.SourceThreadID = "th2" },
		"source_inst":   func(s *proto.Session) { s.SourceInstanceID = "in2" },
		"read_only":     func(s *proto.Session) { s.ReadOnlyReason = "" },
		"caps":          func(s *proto.Session) { s.Capabilities = &proto.SessionCapabilities{Send: true} },
		"caps_nil":      func(s *proto.Session) { s.Capabilities = nil },
		"caps_modes":    func(s *proto.Session) { s.Capabilities = &proto.SessionCapabilities{Modes: proto.CanModes(false)} },
		"attached":      func(s *proto.Session) { s.Attached = false },
		"codex_route":   func(s *proto.Session) { s.CodexRoute = "terminal" },
		"route_note":    func(s *proto.Session) { s.CodexRouteNote = "" },
		"background":    func(s *proto.Session) { s.Background = append(s.Background, proto.BackgroundTask{ID: "b2"}) },
		"bg_none":       func(s *proto.Session) { s.Background = nil },
		"loop":          func(s *proto.Session) { s.Loop = &proto.LoopInfo{NextAt: act} },
		"loop_none":     func(s *proto.Session) { s.Loop = nil },
		"goal_status":   func(s *proto.Session) { s.Goal = &proto.GoalInfo{Status: "complete", Objective: "o", Reason: "r"} },
		"goal_reason":   func(s *proto.Session) { s.Goal = &proto.GoalInfo{Status: "active", Objective: "o", Reason: "r\n2"} },
		"goal_none":     func(s *proto.Session) { s.Goal = nil },
		"plan_done": func(s *proto.Session) {
			s.Plan = &proto.PlanInfo{Done: 4, Total: 5, Current: "Reproducing the crash", At: at}
		},
		"plan_total": func(s *proto.Session) {
			s.Plan = &proto.PlanInfo{Done: 3, Total: 6, Current: "Reproducing the crash", At: at}
		},
		"plan_current": func(s *proto.Session) { s.Plan = &proto.PlanInfo{Done: 3, Total: 5, Current: "Fixing it", At: at} },
		"plan_at_churn": func(s *proto.Session) {
			s.Plan = &proto.PlanInfo{Done: 3, Total: 5, Current: "Reproducing the crash", At: act}
		},
		"plan_none":    func(s *proto.Session) { s.Plan = nil },
		"subagent_run": func(s *proto.Session) { s.Subagents = []proto.Subagent{{ID: "a1", UpdatedAt: at}} },
		"subagent_at": func(s *proto.Session) {
			s.Subagents = []proto.Subagent{{ID: "a1", Running: true, UpdatedAt: act.Add(9 * time.Second)}}
		},
		"subagent_new":       func(s *proto.Session) { s.Subagents = append(s.Subagents, proto.Subagent{ID: "a2"}) },
		"background_waiting": func(s *proto.Session) { s.BackgroundWaiting = !s.BackgroundWaiting }, // #6045
		// a new call shows at once (#5602), not with the next 10 s of updated_at
		"subagent_step": func(s *proto.Session) {
			s.Subagents = []proto.Subagent{{ID: "a1", Running: true, UpdatedAt: at, Steps: 1, LastTool: &proto.WorkflowTool{ID: "toolu_1", Name: "Read"}}}
		},
		"port":       func(s *proto.Session) { s.Ports = []proto.Port{{Port: 3001, PID: 7, Title: "dev"}} },
		"port_pid":   func(s *proto.Session) { s.Ports = []proto.Port{{Port: 3000, PID: 8, Title: "dev"}} },
		"port_title": func(s *proto.Session) { s.Ports = []proto.Port{{Port: 3000, PID: 7, Title: "Vite"}} },
		"ports_none": func(s *proto.Session) { s.Ports = nil },
	}
	out := make(map[string]proto.Session, len(v))
	for name, f := range v {
		s := base
		s.Queued = append([]string(nil), base.Queued...)
		s.Background = append([]proto.BackgroundTask(nil), base.Background...)
		s.Subagents = append([]proto.Subagent(nil), base.Subagents...)
		f(&s)
		out[name] = s
	}
	return base, out
}

// The key changes exactly when the fmt one did: for every one-field change (and every no-op churn), alone and in a
// list of other sessions, both agree on changed / unchanged. It is also the same text, which keeps that easy to see.
func TestListKeyAgreesWithFmt(t *testing.T) {
	base, variants := listKeyVariants()
	others := listKeyFixture(6)
	in := func(s proto.Session) []proto.Session {
		return append(append(append([]proto.Session(nil), others[:3]...), s), others[3:]...)
	}
	for _, list := range [][]proto.Session{{base}, in(base), others, nil, {{}}} {
		if got, want := listKey(list), listKeyFmt(list); got != want {
			t.Fatalf("listKey differs from the fmt key:\n got %q\nwant %q", got, want)
		}
	}
	for name, v := range variants {
		for _, pair := range [][2][]proto.Session{{{base}, {v}}, {in(base), in(v)}} {
			was := listKeyFmt(pair[0]) != listKeyFmt(pair[1])
			now := listKey(pair[0]) != listKey(pair[1])
			if was != now {
				t.Errorf("%s: fmt key changed=%v, listKey changed=%v", name, was, now)
			}
		}
	}
	// The fixture covers both: changes the key must see and churn it must not.
	if listKeyFmt([]proto.Session{base}) == listKeyFmt([]proto.Session{variants["status"]}) ||
		listKeyFmt([]proto.Session{base}) != listKeyFmt([]proto.Session{variants["updated_churn"]}) {
		t.Fatal("fixture does not exercise both outcomes")
	}
}

// A workflow's progress and its agents' latest tool calls change the key (#5511: they are pushed as often as the
// workflow cache reads them); what rides along with them does not.
func TestListKeyWorkflowProgress(t *testing.T) {
	agents := func() []proto.WorkflowAgent {
		return []proto.WorkflowAgent{
			{ID: "a1", Status: "done", LatestTool: &proto.WorkflowTool{Name: "Read", Summary: "在读 a.go"}, Calls: 4, Tokens: 100, LastText: "done"},
			{ID: "a2", Status: "running", LatestTool: &proto.WorkflowTool{Name: "Bash", Summary: "在跑测试"}, Calls: 2, Tokens: 50},
			{ID: "a3", Status: "running"},
		}
	}
	base := proto.Workflow{TaskID: "w1", Status: proto.WorkflowRunning, Phase: "Review", AgentsStarted: 3, AgentsDone: 1, AgentsRunning: 2,
		Agents: agents()}
	key := func(runs ...proto.Workflow) string {
		return listKey([]proto.Session{{SessionID: "s1", Workflows: runs}})
	}
	want := key(base)
	for name, c := range map[string]struct {
		change  func(*proto.Workflow)
		changes bool
	}{
		"task_id":          {func(w *proto.Workflow) { w.TaskID = "w2" }, true},
		"status":           {func(w *proto.Workflow) { w.Status = proto.WorkflowCompleted }, true},
		"phase":            {func(w *proto.Workflow) { w.Phase = "Verify" }, true},
		"agents_done":      {func(w *proto.Workflow) { w.AgentsDone++ }, true},
		"agents_started":   {func(w *proto.Workflow) { w.AgentsStarted++ }, true},
		"agents_running":   {func(w *proto.Workflow) { w.AgentsRunning-- }, true},
		"agents_failed":    {func(w *proto.Workflow) { w.AgentsFailed++ }, true},
		"latest tool name": {func(w *proto.Workflow) { w.Agents[1].LatestTool = &proto.WorkflowTool{Name: "Grep"} }, true},
		"first tool call": {func(w *proto.Workflow) {
			w.Agents[2].LatestTool, w.Agents[2].Calls = &proto.WorkflowTool{Name: "Read"}, 1
		}, true},
		"same tool again":    {func(w *proto.Workflow) { w.Agents[1].Calls++ }, true},
		"retried agent":      {func(w *proto.Workflow) { w.Agents[2].ID = "a4" }, true}, // #2323: no count changes
		"timestamp":          {func(w *proto.Workflow) { w.UpdatedAt = time.Now() }, false},
		"tokens":             {func(w *proto.Workflow) { w.Agents[1].Tokens += 10; w.Tokens += 10 }, false},
		"last text":          {func(w *proto.Workflow) { w.Agents[1].LastText = "halfway" }, false},
		"tool summary alone": {func(w *proto.Workflow) { w.Agents[0].LatestTool = &proto.WorkflowTool{Name: "Read", Summary: "x"} }, false},
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.Agents = agents()
			c.change(&changed)
			if got := key(changed) != want; got != c.changes {
				t.Fatalf("workflow %s: key changed = %v, want %v", name, got, c.changes)
			}
		})
	}
	if key() == want || key(base, base) == want {
		t.Fatal("adding or removing a workflow does not change the list key")
	}
}

// 40 sessions, half of them busy: what publish keys every second (grown to the last key's length, as publish does).
func BenchmarkListKey(b *testing.B) {
	list := listKeyFixture(40)
	size := len(listKey(list))
	b.ReportAllocs()
	for b.Loop() {
		_ = listKeySized(list, size)
	}
}

func TestListKeyModelCapability(t *testing.T) {
	key := func(value *bool) string {
		return listKey([]proto.Session{{SessionID: "s1", Capabilities: &proto.SessionCapabilities{Model: value}}})
	}
	if key(proto.CanModes(true)) != key(proto.CanModes(true)) {
		t.Fatal("equal model capabilities differ")
	}
	if key(nil) == key(proto.CanModes(false)) || key(proto.CanModes(false)) == key(proto.CanModes(true)) {
		t.Fatal("model capability change was lost")
	}
}
