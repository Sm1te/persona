package hub

import (
	"strings"
	"testing"
	"time"

	"encoding/json"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestRuntimeSummaryBackgroundProgress(t *testing.T) {
	at := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	s := proto.Session{MachineID: "fixture-device", SessionID: "worker", State: "working", BackgroundWorking: true,
		Background: []proto.BackgroundTask{{ID: "command", Kind: "shell", Command: "private command"}, {ID: "monitor", Kind: "monitor"}, {ID: "flow", Kind: "workflow"}, {ID: "sub", Kind: "agent"}},
		Subagents:  []proto.Subagent{{Running: true}},
		Workflows: []proto.Workflow{{TaskID: "ended", Status: "completed"}, {TaskID: "flow", Running: true, Status: "running", Name: strings.Repeat("n", 300), Phase: "Adversarial review", Phases: []string{"Review", "Adversarial review"}, PhaseIndex: 2,
			AgentsStarted: 6, AgentsDone: 5, AgentsRunning: 1, StartedAt: at, Agents: []proto.WorkflowAgent{{ID: "private-agent"}}, Error: "private error"}},
	}
	got := runtimeSummary(s)
	if got.BackgroundTasksCount != 2 || got.RunningSubagents != 1 || got.RunningWorkflows != 1 || got.BackgroundCount != 4 || !got.Waiting {
		t.Fatalf("wrong independent counts: %+v", got)
	}
	flow := got.WorkflowProgress
	if flow == nil || flow.TaskID != "flow" || flow.Phase != "Adversarial review" || flow.AgentsDone != 5 || flow.AgentsStarted != 6 || flow.PhaseCount != 2 || len(flow.Name) != 256 {
		t.Fatalf("missing compact progress: %+v", flow)
	}
	b, err := json.Marshal(&got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{`"background_tasks_count":2`, `"workflow_progress":`, `"agents_done":5`, `"phase":"Adversarial review"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	for _, private := range []string{"private command", "private-agent", "private error", `"agents":`, `"phases":`, `"background":`, `"subagents":`, `"workflows":`} {
		if strings.Contains(text, private) {
			t.Fatalf("summary includes detail %s: %s", private, text)
		}
	}
	s.Background, s.Workflows, s.Subagents, s.BackgroundWorking = nil, nil, nil, false
	ended := runtimeSummary(s)
	b, _ = json.Marshal(ended)
	if ended.WorkflowProgress != nil || ended.BackgroundTasksCount != 0 || strings.Contains(string(b), `"workflow_progress"`) || !strings.Contains(string(b), `"background_tasks_count":0`) {
		t.Fatalf("finished work did not clear: %s", b)
	}
}

func TestRuntimeSummaryLegacyBackgroundKinds(t *testing.T) {
	got := runtimeSummary(proto.Session{Background: []proto.BackgroundTask{{ID: "flow", Kind: "workflow"}, {ID: "sub", Kind: "agent"}, {ID: "shell", Kind: "shell"}}})
	if got.BackgroundTasksCount != 1 || got.RunningWorkflows != 1 || got.RunningSubagents != 1 || got.WorkflowProgress != nil {
		t.Fatalf("legacy work kinds lost: %+v", got)
	}
}

// An assistant conversation's model pick stays on its summary row: Android swaps its Persona chats for the list's rows
// whole, and Apple keeps only the fields it copies back (#5033).
func TestRuntimeSummaryKeepsTheModelPick(t *testing.T) {
	got := runtimeSummary(proto.Session{SessionID: "chat", Assistant: true, ModelPick: &proto.ModelPick{Launcher: "cx", Model: "gpt-5"}})
	b, err := json.Marshal(&got)
	if err != nil || !strings.Contains(string(b), `"model_pick":{"launcher":"cx","model":"gpt-5"}`) {
		t.Fatalf("%s %v", b, err)
	}
}
