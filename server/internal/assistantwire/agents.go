package assistantwire

import (
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the Agents business: the manager's (docs/assistant/agent-work.md "Responsibilities") ----
//
// The manager triages an agent's turn once it ends (rules first, then a model), sends "Continue" after a transient model
// error, wakes an agent when what it waits for should be over, and opens a card for what the user is to decide; it
// names the tabs of new agents and says what a new or resumed agent runs on ("Models" › Agents, a model picked for one
// start). The hub only carries it: it tells the manager what happened (session.turn_started / turn_ended,
// session.seen), shows what the manager reports (Snapshot.Work on the session lists, the cards among
// Snapshot.Interactions) and asks it to fill in a start (agents.start_params, agents.resume_params, agents.tab_name).

// AgentsConfig is manager.json "agents" (Hello.Agents, over the hub's hello): what the manager does about agents'
// turns by default.
type AgentsConfig struct {
	Triage   TriageConfig   `json:"triage,omitzero"`
	Delegate DelegateConfig `json:"delegate,omitzero"`
	// Dispatch is manager.json agents.dispatch: an agent handing a subtask to another connected computer
	// (docs/plans/plan-agent-dispatch.md).
	Dispatch DispatchConfig `json:"dispatch,omitzero"`
	// LauncherHealth is manager.json agents.launcher_health: checking each machine's launchers (docs/server/launcher-health.md).
	LauncherHealth LauncherHealthConfig `json:"launcher_health,omitzero"`
	// Review is manager.json agents.review: the independent reviewer of a supervised task's finished change (#2173,
	// docs/assistant/unified-entry.md "Independent review").
	Review ReviewConfig `json:"review,omitzero"`
}

// DispatchConfig is manager.json agents.dispatch: the agents' dispatch_start / dispatch_read tools. Off unless Enabled
// is true: a dispatched task runs an agent on another computer on its model, started by an agent and not by the user.
type DispatchConfig struct {
	// Enabled: off unless true.
	Enabled *bool `json:"enabled,omitempty"`
	// MaxActive: how many dispatched tasks of one agent may be working at a time. Default 3.
	MaxActive int `json:"max_active,omitempty"`
}

// On: the switch, off unless set true.
func (c DispatchConfig) On() bool { return c.Enabled != nil && *c.Enabled }

// Limit is MaxActive, 3 when unset.
func (c DispatchConfig) Limit() int {
	if c.MaxActive <= 0 {
		return 3
	}
	return c.MaxActive
}

// ReviewConfig is manager.json agents.review: when a supervised task's agent is judged done, a reviewer agent started
// with Launcher (a launcher on the agent's computer), Model and Effort (its defaults when empty) and Permission (a
// permission mode; default plan) reviews the change before the task waits for review. TimeoutMinutes bounds one
// review (default 30). Off unless Launcher is set: there is no default reviewer, so the review runs on a model the
// user picked, apart from the agents'.
type ReviewConfig struct {
	Launcher       string `json:"launcher,omitempty"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	Permission     string `json:"permission,omitempty"`
	TimeoutMinutes int    `json:"timeout_minutes,omitempty"`
}

// On: a reviewer launcher is configured.
func (c ReviewConfig) On() bool { return strings.TrimSpace(c.Launcher) != "" }

// Timeout is TimeoutMinutes, 30 minutes when unset.
func (c ReviewConfig) Timeout() time.Duration {
	if c.TimeoutMinutes <= 0 {
		return 30 * time.Minute
	}
	return time.Duration(c.TimeoutMinutes) * time.Minute
}

// PermissionMode is Permission, plan when unset: the launcher reads and answers, it does not edit.
func (c ReviewConfig) PermissionMode() string {
	if p := strings.TrimSpace(c.Permission); p != "" {
		return p
	}
	return "plan"
}

// LauncherHealthConfig is manager.json agents.launcher_health: how often each machine's launchers are checked, and the agent
// that repairs the ones a node cannot repair itself.
type LauncherHealthConfig struct {
	// Enabled: check on a schedule and repair what fails (unset: off). Off, failed starts are still recorded and
	// steer new agents away.
	Enabled *bool `json:"enabled,omitempty"`
	// IntervalMinutes between two checks of one launcher on one machine. Default 30.
	IntervalMinutes int                  `json:"interval_minutes,omitempty"`
	Repair          LauncherRepairConfig `json:"repair,omitzero"`
}

// LauncherRepairConfig is the agent that repairs a machine's launchers: started on Machine (default: the assistant's)
// in Cwd (default: home) with Launcher (default: a healthy one there) and Permission (a permission mode; default:
// bypassPermissions, so it repairs without waiting on approvals), told to use Skill. No Skill: no repair agent.
type LauncherRepairConfig struct {
	Skill      string `json:"skill,omitempty"`
	Machine    string `json:"machine,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Launcher   string `json:"launcher,omitempty"`
	Permission string `json:"permission,omitempty"`
}

// DelegateConfig is manager.json agents.delegate: an agent's question goes to the assistant before the user
// (docs/assistant/assistant.md "Assistant answers questions first").
type DelegateConfig struct {
	// Enabled: off unless true.
	Enabled *bool `json:"enabled,omitempty"`
	// TimeoutSeconds: how long the hub waits for the assistant before it asks the user as it would have. Default 180.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// Rules are the user's own, read with the built-in ones ("add the missing tests, don't ask me" …). They cannot take a red line off
	// the user: an answer that would set one off is left to them all the same.
	Rules string `json:"rules,omitempty"`
}

// On: the switch, off unless set true.
func (c DelegateConfig) On() bool { return c.Enabled != nil && *c.Enabled }

// Timeout is TimeoutSeconds, 3 minutes when unset.
func (c DelegateConfig) Timeout() time.Duration {
	if c.TimeoutSeconds <= 0 {
		return 3 * time.Minute
	}
	return time.Duration(c.TimeoutSeconds) * time.Second
}

// TriageConfig is manager.json agents.triage: the limits, and the switches a hub whose user never saved them starts with
// (unset: off, but for transient_retry).
type TriageConfig struct {
	Enabled      *bool `json:"enabled,omitempty"`
	AutoContinue *bool `json:"auto_continue,omitempty"`
	Wake         *bool `json:"wake,omitempty"`
	Decide       *bool `json:"decide,omitempty"`
	ToolLoop     *bool `json:"tool_loop,omitempty"`
	Closeout     *bool `json:"closeout,omitempty"`
	CloseoutAsk  *bool `json:"closeout_ask,omitempty"`
	// TransientRetry: the initial value of the transient_retry switch (unset: on): "Continue" after a transient model
	// error, as a rule, whether or not the triage itself is on.
	TransientRetry *bool `json:"transient_retry,omitempty"`
	// MaxRetries: "Continue" sent for one run of transient errors before leaving it to the user. Default 5.
	MaxRetries int `json:"max_retries,omitempty"`
	// RetryBaseSeconds: the first "Continue" after a transient error waits this long, each next one twice as long as
	// the one before (a rate limit four times as long). Default 15.
	RetryBaseSeconds int `json:"retry_base_seconds,omitempty"`
	// MaxWakes: wakes for one wait before asking the user. Default 6.
	MaxWakes int `json:"max_wakes,omitempty"`
	// SettleSeconds: how long a stopped session stays stopped before its turn is triaged. Default 15.
	SettleSeconds int `json:"settle_seconds,omitempty"`
}

// WorkReport is the snapshot's work (Snapshot.Work): where each agent stands after its latest turn, as the manager
// found it. The hub puts a session's on the lists while the session is still stopped on that turn (Work.TurnAt), and
// none at all while On is false; what an agent shows while it works or asks on its own card is the hub's to say.
type WorkReport struct {
	On       bool          `json:"on"` // Settings › session triage is on
	Sessions []SessionWork `json:"sessions"`
}

// SessionWork is one session's work state in the report; Work.TurnAt is the end of the turn it is about.
type SessionWork struct {
	MachineID string          `json:"machine_id"`
	SessionID string          `json:"session_id"`
	Work      proto.WorkState `json:"work"`
}

// CodeGone is manager.action's refusal of an answer whose question or card is not there any more (answered, withdrawn,
// expired): final, the hub does not keep the answer to try again (any other error it does).
const CodeGone = "gone"

// WorkCardTool is the tool_name of the manager's cards about an agent's turn (among Snapshot.Interactions, answered
// like its other questions with manager.action): the lists put an agent's own question before one of these.
const WorkCardTool = "persona_triage"

// AgentOwnTools are the Persona tools an Agent Persona starts calls without a permission card (#4966,
// docs/integrations/claude-integration.md "Persona tools Agents call without a card"): the agent's own tools (the
// manager's agentToolNames) and a task leader's task_plan and task_review. Each acts only inside Persona, for the
// Agent calling it (the node names the session of its pane), and the manager bounds it: a leader's task_plan adds
// workers only below its own task, its task_review reaches only its direct workers. The node allows them on the
// command line of the Claude Agents it starts (--allowedTools), the Paseo bridge for the Agents it creates
// (integrations/paseo/lib/map.js OWN_TOOLS). Not the phone's tools, the connectors' or dispatch_start / dispatch_read:
// they act outside Persona, or start and read an Agent on another computer, and still ask.
var AgentOwnTools = []string{"report_to_supervisor", "todo_decisions", "todo_decision_add", "todo_bind_thread", "task_plan", "task_review"}

const (
	// MethodAgentStartParams: AgentStartParams → {params}: an Agent the app starts (POST /api/v1/sessions), with what
	// it runs on filled in and its tab named. An error with CodeBadRequest is the app's to see (400); while the manager
	// is not connected the hub starts it as the app asked.
	MethodAgentStartParams = "agents.start_params"
	// MethodAgentResumeParams: AgentResumeParams → {params}: a message that resumes an Agent nothing says the launcher
	// of (started outside Persona), with the launcher and model it is to run on.
	MethodAgentResumeParams = "agents.resume_params"
	// MethodAgentTabName: {prompt} → {name}: a herdr tab's name for an Agent started with prompt ("" when none).
	MethodAgentTabName = "agents.tab_name"
	// EventSessionSeen: SessionSeen, one way: the user looked at a session in an app.
	EventSessionSeen = "session.seen"
)

// AgentStartParams is agents.start_params's params: Params are POST /api/v1/sessions's (the node's session.start);
// NameFrom is what the tab is named from now ("": not now: it has a name, resumes a session, or is named later). Lang
// is the app's language, the one a refusal is said in ("": the user's; an older hub sends none).
type AgentStartParams struct {
	MachineID string         `json:"machine_id"`
	Params    map[string]any `json:"params"`
	NameFrom  string         `json:"name_from,omitempty"`
	Lang      string         `json:"lang,omitempty"`
}

// AgentResumeParams is agents.resume_params's params: Params are the message's (the node's session.send; the hub
// sends none and puts the answer's over its own: a message may carry pictures).
type AgentResumeParams struct {
	MachineID string         `json:"machine_id"`
	SessionID string         `json:"session_id"`
	Params    map[string]any `json:"params"`
}

// SessionSeen is session.seen's params.
type SessionSeen struct {
	MachineID string    `json:"machine_id"`
	SessionID string    `json:"session_id"`
	At        time.Time `json:"at"`
}

// agentRoutes are the Agents business's routes the manager answers (agents_routes.go there): its switches, its
// numbers, renaming the tabs nobody named.
var agentRoutes = []string{
	"GET /api/v1/agents/triage",
	"PUT /api/v1/agents/triage",
	"GET /api/v1/agents/work/stats",
	"POST /api/v1/tabs/autoname",
}

func init() { Routes = append(Routes, agentRoutes...) }
