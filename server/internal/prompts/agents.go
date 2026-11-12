package prompts

func RenderAgentSessionName() string { return render("agents/session_name", nil) }

// AgentSessionNameInput contains only bounded dialogue, never machine paths or credentials.
type AgentSessionNameInput struct {
	First  string
	Recent string
}

func RenderAgentSessionNameInput(v AgentSessionNameInput) string {
	return render("agents/session_name_input", v)
}

// RenderReadonlyTaskPlanExit keeps a read-only task from requesting writable implementation approval.
func RenderReadonlyTaskPlanExit() string { return render("agents/readonly_plan_exit", nil) }

// RenderReadonlyTaskConstraint carries a read-only task's limit in its first prompt.
func RenderReadonlyTaskConstraint() string { return render("agents/readonly_task", nil) }

// AgentTriage is the system prompt that reads a stopped agent's last reply and says where its work stands
// (agents/triage, docs/assistant/agent-work.md).
type AgentTriage struct {
	Owner    string // "" is the user
	Language string // the line's language when it isn't Chinese (answerLanguage), "" for Chinese
}

func RenderAgentTriage(t AgentTriage) string { return render("agents/triage", t) }

func RenderAgentToolLoop() string { return render("agents/tool_loop", nil) }

// AgentReportLine is the system prompt that paraphrases a supervised task's agent's report in one line for the
// Progress row (agents/report_line, #3181, docs/assistant/unified-entry.md "Progress tray").
type AgentReportLine struct {
	Language string // the line's language when it isn't Chinese (answerLanguage), "" for Chinese
	Max      int    // at most this many characters
}

func RenderAgentReportLine(r AgentReportLine) string { return render("agents/report_line", r) }

// AgentReportLineInput is the report to paraphrase (agents/report_line_input).
type AgentReportLineInput struct {
	Task string // the task's title
	Kind string // progress, question, blocked or done
	Text string // the report as the agent wrote it, clipped
}

func RenderAgentReportLineInput(in AgentReportLineInput) string {
	return render("agents/report_line_input", in)
}

// AgentTriageInput is the turn to triage (agents/triage_input).
type AgentTriageInput struct {
	Now      string // the user's local time, "2006-01-02 15:04 Mon"
	Agent    string // its tab / title
	Machine  string
	Cwd      string
	Project  string // the knowledge base's project for its directory, "" when none
	Ask      string // what the user asked in this turn, "" when it is not known
	Reply    string // its last reply, clipped
	Others   []AgentTriageOther
	Answered []AgentAnswerItem // durable question/answer pairs, newest first; not authorization for automatic actions
	// The risky step the user already said yes to, once what it waits for is over ("" when none): a turn still
	// waiting for it is no question for the user.
	ApprovedWait, ApprovedThen string
	ApprovedByTask             bool // the agent's task authorized it, not the user on a card (#3246)
}

// AgentTriageOther is another agent working on the same machine: what this one may be waiting for.
type AgentTriageOther struct {
	ID, Label, Line string
}

func RenderAgentTriageInput(in AgentTriageInput) string { return render("agents/triage_input", in) }

// AgentWake is what the hub sends an agent whose wait should be over (agents/wake).
type AgentWake struct {
	Attempt   int
	Wait      string
	Then      string
	Agent     string // the agent it waited for, when it is that one stopping that wakes it
	AgentLine string // what that agent said last
	Approved  string // the risky step the user already said yes to, once the wait is over ("" when none)
	// ApprovedByTask: the agent's task asked for that step, not the user on a card (#3246).
	ApprovedByTask bool
}

func RenderAgentWake(w AgentWake) string { return render("agents/wake", w) }

// RenderAgentLimitContinue is what the manager sends an agent a subscription's usage limit stopped, once the limit has
// reset and its CLI did not say it goes on by itself (agents/limit_continue, docs/assistant/agent-work.md "Usage limits").
func RenderAgentLimitContinue() string { return render("agents/limit_continue", nil) }

// AgentAnswer is the user's answers to the hub's decision card, sent on to the agent (agents/answer).
type AgentAnswer struct {
	Items []AgentAnswerItem
	// ByAssistant: the assistant answered for the user (docs/assistant/assistant.md "Assistant answers questions first"), Reason says why.
	ByAssistant bool
	Reason      string
}

type AgentAnswerItem struct {
	Question, Answer string
}

func RenderAgentAnswer(a AgentAnswer) string { return render("agents/answer", a) }

// AgentAuthRelayed tells an agent an extension authorized the link it stopped at with the user's account
// (agents/auth_relayed, docs/operations/auth-relay.md).
type AgentAuthRelayed struct {
	App string // the app, as the authorization page names it; "" when it was not read
}

func RenderAgentAuthRelayed(a AgentAuthRelayed) string { return render("agents/auth_relayed", a) }

// LauncherRepair is the first message of the agent that repairs a machine's launchers (agents/launcher_repair,
// docs/server/launcher-health.md).
type LauncherRepair struct {
	Skill     string // the skill that sets up and repairs the agent CLIs
	Machine   string // the broken machine's id
	Name      string // its name, "" when the same as the id
	SSHHost   string // the hub machine's ssh alias for it, "" when none
	Launchers []LauncherRepairItem
}

// LauncherRepairItem is one launcher that failed its check there.
type LauncherRepairItem struct {
	Name  string
	Agent string // claude, codex…
	Kind  string // the hub's class of the error (model_route, auth, cli_outdated, timeout, other)
	Error string
}

func RenderLauncherRepair(r LauncherRepair) string { return render("agents/launcher_repair", r) }

// AgentReanswer is the user's answers to questions the assistant answered for them, sent on to the agent, which went
// on with the assistant's (agents/reanswer, docs/assistant/assistant.md "Assistant answers questions first").
type AgentReanswer struct {
	Items []AgentReanswerItem
}

type AgentReanswerItem struct {
	Question, Before, Answer string
}

func RenderAgentReanswer(a AgentReanswer) string { return render("agents/reanswer", a) }

// AgentDelegate is the system prompt that decides whether the assistant answers an agent's question for the user
// (agents/delegate, docs/assistant/assistant.md "Assistant answers questions first").
type AgentDelegate struct {
	Owner string // "" is the user
	Rules string // the user's own rules, trimmed: manager.json agents.delegate.rules
}

func RenderAgentDelegate(d AgentDelegate) string { return render("agents/delegate", d) }

// AgentDelegateInput is the question to decide on and what is known around it (agents/delegate_input).
type AgentDelegateInput struct {
	Now       string // the user's local time, "2006-01-02 15:04 Mon"
	Agent     string
	Machine   string
	Cwd       string
	Project   string   // the knowledge base's projects for its directory, "" when none
	Task      string   // the task (todo) it works for: its title and notes, "" when none
	Decisions string   // the calls the user made on that task (todo decisions), "" when none
	Asks      []string // the user's recent messages to the agent, clipped, oldest first
	Reply     string   // what the agent said last, clipped
	Questions []DelegateQuestion
}

// DelegateQuestion is one question of the card, as asked.
type DelegateQuestion struct {
	Question    string
	MultiSelect bool
	Options     []DelegateOption
}

type DelegateOption struct {
	Label, Description string
}

func RenderAgentDelegateInput(in AgentDelegateInput) string {
	return render("agents/delegate_input", in)
}

// AgentReview is the first message of the independent reviewer of a supervised task's finished change (agents/review,
// #2173, docs/assistant/unified-entry.md "Independent review"): it sees the task's ask, its criteria and the change,
// never the agent that did the work, and ends with a machine-readable count line.
type AgentReview struct {
	Language string   // the language to answer in by its own name ("" is Chinese)
	Title    string   // the task's title
	Ask      string   // what the task asked for (the todo's notes), clipped
	DoneWhen []string // what done means
	Scope    string   // what the diff is compared with, one line
	Diff     string   // the capped diff text
	Omitted  int      // files left out of the diff once the budget ran out
}

func RenderAgentReview(r AgentReview) string { return render("agents/review", r) }

// DispatchTask is the first message of an agent another agent started on this computer (agents/dispatch_task,
// assistant/dispatch.go): who handed it the task, and that its last reply is what goes back.
type DispatchTask struct {
	From        string // the calling agent's tab / title, "" when it is not known
	FromMachine string // the calling agent's computer, by name
	Prompt      string
}

func RenderDispatchTask(t DispatchTask) string { return render("agents/dispatch_task", t) }

// TaskSuccessor is the first message of an agent Persona starts for a task whose agents have all stopped (#4754): who
// worked on it before and its last reply, then the user's message on the task (agents/task_successor).
type TaskSuccessor struct {
	Previous   string // the stopped agent's tab / title
	PreviousID string // its session id
	LastReply  string // its last reply, clipped; "" when there is none
	Text       string
}

func RenderTaskSuccessor(t TaskSuccessor) string { return render("agents/task_successor", t) }

// RenderTaskLeader is what follows the prompt of a task whose agent is a task leader (agents/task_leader, #3656): that it
// can hand direct workers to task_plan and how their reports reach it.
func RenderTaskLeader() string { return render("agents/task_leader", struct{}{}) }

// RenderTaskWorker is what follows the prompt of a worker a task leader handed out (agents/task_worker).
func RenderTaskWorker() string { return render("agents/task_worker", struct{}{}) }

// RenderWorkerSupervise is the supervision message a task leader's agent gets about its workers (agents/worker_supervise,
// assistant/task_leader.go): what each task of its own needs, from the same entries a conversation's message lists.
func RenderWorkerSupervise(entries []SuperviseEntry) string {
	return render("agents/worker_supervise", struct{ Entries []SuperviseEntry }{entries})
}

// AgentCloseoutRequired is what an agent is told when its stop is held for a missing closeout (#3612).
type AgentCloseoutRequired struct {
	Form     string // the closeout's form (closeout.Example)
	Language string // the user's language, for the sentences inside it
}

func RenderAgentCloseoutRequired(c AgentCloseoutRequired) string {
	return render("agents/closeout_required", c)
}

// AgentCloseoutAsk is the status question to an agent nobody supervises whose turn did work and ended without a
// closeout (#3616): the same form and rules (agents/closeout_rules), about work already done.
type AgentCloseoutAsk struct {
	Form     string // the closeout's form (closeout.Example)
	Language string // the user's language, for the sentences inside it
}

func RenderAgentCloseoutAsk(c AgentCloseoutAsk) string {
	return render("agents/closeout_ask", c)
}

// AgentCloseoutBrief tells an agent the closeout's form before it works, so its turn does not cost a held stop
// (agents/closeout_brief): after a task's prompt, or as a session's starting context.
type AgentCloseoutBrief struct {
	Form     string // the closeout's form (closeout.Example)
	Language string // the user's language, for the sentences inside it
	// AfterDelivery: the same prompt asks for the delivery line (assistant/task_delivery); the block goes after it.
	AfterDelivery bool
	// Session: it is a session's starting context, not part of a task's prompt: it says whose words these are.
	Session bool
}

func RenderAgentCloseoutBrief(c AgentCloseoutBrief) string {
	return render("agents/closeout_brief", c)
}
