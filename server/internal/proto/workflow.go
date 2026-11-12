package proto

import "time"

// BackgroundTask is a Bash command, subagent, Monitor or workflow Claude runs in the background (run_in_background).
type BackgroundTask struct {
	ID          string    `json:"id"`   // task id from the tool result (e.g. "b85polnoj")
	Kind        string    `json:"kind"` // "shell" | "agent" | "monitor" | "workflow"
	Description string    `json:"description,omitempty"`
	Command     string    `json:"command,omitempty"`
	OutputFile  string    `json:"output_file,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	// ExpiresAt: when a Monitor stops by itself ("expires in 30m"); zero when it does not say.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// RunID: a workflow's run ("wf_…"), the key of its entry in Session.Workflows.
	RunID string `json:"run_id,omitempty"`
}

// Workflow is a Claude Code workflow run (the Workflow tool): a script that drives many agents in the background and
// reports back to the session with a task notification when it ends (docs/protocol/agent-states.md "Background workflows").
type Workflow struct {
	TaskID  string `json:"task_id"`          // the background task's id, as in Background and the task notification
	RunID   string `json:"run_id,omitempty"` // "wf_…": its directory under <session>/subagents/workflows/
	Name    string `json:"name,omitempty"`   // the script's meta.name ("parity-audit")
	Summary string `json:"summary,omitempty"`
	// Status is one of the Workflow* values. Running is Status == WorkflowRunning, for clients that only need that.
	Status  string `json:"status"`
	Running bool   `json:"running"`
	// Phases are the script's phase titles in order; Phase is the one its latest started agent is in, PhaseIndex its
	// 1-based place in Phases (0: not known).
	Phases     []string `json:"phases,omitempty"`
	Phase      string   `json:"phase,omitempty"`
	PhaseIndex int      `json:"phase_index,omitempty"`
	PhaseCount int      `json:"phase_count,omitempty"` // len(Phases)
	ToolUseID  string   `json:"tool_use_id,omitempty"` // the Workflow call that launched it
	// PhaseStats counts all agents, before Agents is capped, in Phases order then any undeclared phases.
	PhaseStats []WorkflowPhaseStats `json:"phase_stats,omitempty"`
	// AgentsStarted counts the agents it started so far (the total is not known before it ends: scripts fan out as
	// they go), AgentsDone those that returned, AgentsFailed those the journal says failed (not counted as done; absent
	// from older nodes, which counted them as done), AgentsRunning those still at it. Agents stopped when the run ended
	// are none of the three. A retried agent counts once, as its latest attempt: the attempts it replaced are in no
	// count, PhaseStats or Agents (#2323; older nodes counted each, running until the run ended).
	AgentsStarted int        `json:"agents_started"`
	AgentsDone    int        `json:"agents_done"`
	AgentsFailed  int        `json:"agents_failed,omitempty"`
	AgentsRunning int        `json:"agents_running"`
	Tokens        int        `json:"tokens,omitempty"` // sum of each agent's latest usage, not cumulative usage; replaced attempts too
	Calls         int        `json:"calls,omitempty"`  // all agents' tool calls, before Agents is capped; replaced attempts too
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`   // nil while it runs
	UpdatedAt     time.Time  `json:"updated_at,omitempty"` // its last sign of life (journal or an agent's transcript)
	Error         string     `json:"error,omitempty"`      // a failed or killed run's error, first line
	// Agents are its agents in the order they started (at most WorkflowAgentsMax, the latest kept); a retried one is
	// listed as its latest attempt, where that attempt started.
	Agents []WorkflowAgent `json:"agents,omitempty"`
}

// WorkflowAgentsMax caps Workflow.Agents.
const WorkflowAgentsMax = 60

// WorkflowPhaseStats counts agents in one phase, as Workflow counts them all. An empty title groups agents whose phase
// is unknown.
type WorkflowPhaseStats struct {
	Title   string `json:"title"`
	Started int    `json:"started"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed,omitempty"`
	Running int    `json:"running"`
}

// A workflow's statuses. Stalled: no notification and no state file, yet nothing written for a long while (its
// claude went away): not counted as running.
const (
	WorkflowRunning   = "running"
	WorkflowCompleted = "completed"
	WorkflowFailed    = "failed"
	WorkflowKilled    = "killed"
	WorkflowStalled   = "stalled"
)

// WorkflowAgent is one agent of a workflow run. Its transcript reads like a subagent's, as the session SessionID.
type WorkflowAgent struct {
	ID         string        `json:"id"`
	SessionID  string        `json:"session_id"` // "<parent>@<agent id>"
	Label      string        `json:"label,omitempty"`
	Phase      string        `json:"phase,omitempty"`
	Running    bool          `json:"running"`
	Status     string        `json:"status"` // running | done | failed (the journal says so) | stopped (its run ended before it returned)
	StartedAt  time.Time     `json:"started_at,omitempty"`
	EndedAt    *time.Time    `json:"ended_at,omitempty"` // nil while running; last exchange, or the run's end if stopped
	UpdatedAt  time.Time     `json:"updated_at,omitempty"`
	LastText   string        `json:"last_text,omitempty"`
	Model      string        `json:"model,omitempty"`
	LatestTool *WorkflowTool `json:"latest_tool,omitempty"`
	Tokens     int           `json:"tokens,omitempty"` // latest usage's input + cache read + cache creation + output
	Calls      int           `json:"calls,omitempty"`  // tool_use and server_tool_use blocks in its transcript
}

// WorkflowTool is an agent's most recent tool call, retained after its final text arrives. Summary is a short line on
// what it does ("Reading example.go"): the node writes it in Chinese, the hub gives it in the reader's language.
type WorkflowTool struct {
	ID      string `json:"id,omitempty"` // the call's tool_use id: a client keys what it plays for a new call by it
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// Subagent is an agent the session launched (the Agent tool), with its own transcript.
type Subagent struct {
	ID          string    `json:"id"`         // agent id (agent-<id>.jsonl)
	SessionID   string    `json:"session_id"` // "<parent>@<id>": readable like any session
	Type        string    `json:"type,omitempty"`
	Description string    `json:"description,omitempty"`
	ToolUseID   string    `json:"tool_use_id,omitempty"`
	ParentID    string    `json:"parent_id,omitempty"` // the launching subagent's id; empty: the session (an unlisted one: show at the top)
	Fork        bool      `json:"fork,omitempty"`      // a fork of its launcher's conversation (agentType "fork")
	Running     bool      `json:"running"`
	Background  bool      `json:"background,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	// UpdatedAt is when it last wrote a message or a tool result (its file's mtime only before it wrote one): it moves
	// when there is something new to show, not with every line the file gets (#5602).
	UpdatedAt time.Time     `json:"updated_at,omitempty"`
	LastText  string        `json:"last_text,omitempty"`
	Steps     int           `json:"steps,omitempty"`     // tool calls it made
	LastTool  *WorkflowTool `json:"last_tool,omitempty"` // its latest tool call: what it is doing now while it runs
	// Model is the model it ran on, its latest reply's in its own transcript (never <synthetic>), not the one its
	// Agent call asked for (#5030).
	Model string `json:"model,omitempty"`
	// Tokens is its latest reply's context plus output tokens, as a workflow agent's (WorkflowAgent.Tokens).
	Tokens int `json:"tokens,omitempty"`
}
