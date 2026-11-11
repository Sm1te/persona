package prompts

import (
	"strings"
	"time"
	"unicode/utf8"
)

// AssistantMachine is one computer in the assistant's CLAUDE.md, as seen from where the assistant runs.
type AssistantMachine struct {
	Name, ID string
	Local    bool   // the machine the assistant runs on
	SSHHost  string // "" uses ID
	// AgentsOnly: no command reaches it from where the assistant runs (the ssh aliases are the hub machine's, and a
	// link or tailcat machine has none), only the Agents tools do.
	AgentsOnly bool
}

// AssistantClaudeMD is the assistant directory's CLAUDE.md (assistant/claude_md).
type AssistantClaudeMD struct {
	Owner        string // "" is "user"
	Machines     []AssistantMachine
	Notes        string // Settings › Assistant › About me, trimmed
	ExtraDirs    []string
	StateDir     string
	KnowledgeDir string
	OnHub        bool
	// LarkCLI: hub.json configures lark-cli (docs/plans/plan-lark-cli.md), on the assistant's PATH.
	LarkCLI bool
	// PersonaView: every app draws ```persona-view (hub.json assistant.persona_view); else the assistant writes
	// ```persona-card, which they all draw.
	PersonaView bool
	// Language is the language the user reads, by its own name ("English", or Japanese written in Japanese); "" is Chinese, as before i18n.
	Language string
}

func RenderAssistantClaudeMD(d AssistantClaudeMD) string { return render("assistant/claude_md", d) }

// AssistantHealthClaudeMD is the health space's CLAUDE.md (docs/assistant/health.md).
type AssistantHealthClaudeMD struct {
	Owner    string // "" is "user"
	Notes    string // Settings › Assistant › About me, trimmed
	StateDir string
	// HealthDir is the health space's directory name in StateDir: "" is the built-in manager's "health", another's
	// "health-<id>" (identity.HealthDirName).
	HealthDir string
	Language  string
}

func RenderAssistantHealthClaudeMD(d AssistantHealthClaudeMD) string {
	return render("assistant/health_claude_md", d)
}

// AssistantGuestClaudeMD is a guest conversation's CLAUDE.md (docs/assistant/assistant.md ("Guest conversations")).
type AssistantGuestClaudeMD struct {
	Owner    string // An empty name uses the generic user label.
	Language string
}

func RenderAssistantGuestClaudeMD(d AssistantGuestClaudeMD) string {
	return render("assistant/guest_claude_md", d)
}

type AssistantViewRepair struct {
	Errors []string
}

func RenderAssistantViewRepair(d AssistantViewRepair) string {
	return render("assistant/view_repair", d)
}

// SnapshotMachine, SnapshotAgent… are the parts of the live snapshot (assistant/snapshot).
type SnapshotMachine struct {
	Name, ID string
	Online   bool
}

// SnapshotApp is the Persona app on one kind of device as it last said its version (assistant/snapshot_app). No
// time: the node sends only the snapshot's changed lines, and a reconnect must not make this one change.
// Platform: ios | ipados | watchos | macos | visionos | android.
type SnapshotApp struct {
	Platform, Version, Build, OS string // Build, OS: "" when not said
	Simulator                    bool
}

type SnapshotAgent struct {
	Label, Machine, Session string             // Session: the short id
	Line                    string             // what it waits for, or what it concluded ("" when not known)
	Link                    string             // its page in the hub's web app ("" when the hub has no public_url)
	Ultracode               string             // "on" / "off"; empty when the node does not know
	Plan                    *SnapshotAgentPlan // its own task list (Session.Plan), nil when it keeps none
	// Closeout: what its closeout says of merging and deploying, nil when it wrote none or says neither.
	Closeout *SnapshotAgentCloseout
}

// SnapshotAgentCloseout is the tail of an agent's line (assistant/snapshot_closeout): Merged and Deployed as the
// agent claimed them ("yes", "no", "" when unsaid), FoundMerged and FoundDeployed what the hub found where that
// differs from the claim ("" where it agrees or could not tell).
type SnapshotAgentCloseout struct {
	Merged, Deployed           string
	FoundMerged, FoundDeployed string
}

// SnapshotAgentPlan is an agent's own task list (TodoWrite / TaskCreate, docs/integrations/agent-goal.md "Plan"): Done of Total
// items completed, and the one in progress ("" when none is).
type SnapshotAgentPlan struct {
	Done, Total int
	Current     string
}

type SnapshotChat struct {
	Name    string
	Unread  int
	Preview string // @me chats: the clipped preview
}

type SnapshotChats struct {
	AtMe, Other []SnapshotChat
}

func (c SnapshotChats) Total() int { return len(c.AtMe) + len(c.Other) }

type SnapshotEvent struct {
	Start, End time.Time // in the user's zone
	Title      string
}

type SnapshotCalendar struct {
	Events []SnapshotEvent
}

type SnapshotFollowup struct {
	At    time.Time // in the user's zone
	Label string
	Every string // "30 minutes" for a repeating one
}

// SnapshotPlan is a plan card in the main conversation still waiting for the user's "Start" (#4453): nothing of it has
// started, and it shows nowhere else.
type SnapshotPlan struct {
	At     time.Time // when it was put up, in the user's zone
	Titles []string  // its tasks' titles (at most three)
	Tasks  int       // how many tasks it has
}

// SnapshotAsked is an ask_user question that became a to-do (#1617): what was asked, the to-do, when it was asked (in the
// user's zone).
type SnapshotAsked struct {
	TodoID   string
	Question string
	At       time.Time
}

// SnapshotRoutine is a daily scheduled task (morning brief, evening brief…): its rule, and how its last run went (LastAt zero: it never
// ran). LastStatus is the run's status (ok / failed / skipped), LastWhy why it failed or was skipped, in short.
type SnapshotRoutine struct {
	Label, Rule         string
	LastAt              time.Time // in the user's zone
	LastStatus, LastWhy string
}

// SnapshotMiss is a scheduled run that failed or was skipped since yesterday evening, with why, in short (#4124): the
// The morning brief says "last night's check failed" without calling list_followups.
type SnapshotMiss struct {
	At         time.Time // in the user's zone
	Label, Why string
	Skipped    bool
}

// SnapshotTask is a task opened from an assistant conversation: a todo and its agents (docs/assistant/unified-entry.md).
type SnapshotTask struct {
	ID, Title, State string
	Machine, Session string // its latest agent, when it has one
	Last             string // what that agent said last, in a line
	// Doing: the step that agent is on (its latest tool call, transcript.Activity) while it works or has said nothing
	// yet, and DoingFor how long that step has gone on ("3 minutes"), "" when unknown or not working.
	Doing, DoingFor string
	Subagents       []string // that agent's running subagents, what each is for: on top of its own step, never instead
	// Workflows: that agent's background workflow runs still running (#2935): its turn ended to wait for them.
	Workflows []SnapshotWorkflow
	Link      string // the todo's page in the hub's web app ("" when the hub has no public_url)
	// Health: how a task the main conversation supervises stands (normal, stuck N minutes, waiting for an answer, to accept, rework round N, your call,
	// accepted; #3124), "" for any other; DoneWhen how many criteria it has.
	Health   string
	DoneWhen int
	// Orphan: why nobody works on an open task any more (error: its agent ended on a model error and is closed;
	// closed: its agent's tab was closed; offline: its computer is away), "" while an agent is at it or will be woken
	// (docs/assistant/todos.md "Nobody is working on this").
	Orphan string
	// Delivery: how far its finished work was delivered when that is short of live ("stopped at the PR, stuck on 'CI queued'"), ""
	// otherwise (docs/assistant/todos.md "Delivery").
	Delivery string
	// Reported: what its agent last reported to the supervisor (report_to_supervisor), nil when it has not.
	Reported *SnapshotReport
	// Plan: its agent's own task list, nil when it keeps none or Reported is newer (a report says more).
	Plan *SnapshotAgentPlan
}

// SnapshotReport is a supervised task's agent's latest report: its kind (progress, question, blocked, done), the step
// of how many (0: not said), what it said in a line and how many minutes ago.
type SnapshotReport struct {
	Kind        string
	Step, Steps int
	Text        string
	Minutes     int
}

// SnapshotWorkflow is a Claude Code workflow run still running in an agent's background (#2935): the agent's turn
// ended to wait for its result, which wakes it.
type SnapshotWorkflow struct {
	Agent, Machine, Session string // whose run: its label, machine and short session id (left empty inside a task)
	Name, Phase             string
	PhaseIndex, Phases      int    // Phase's 1-based place among the script's phases; 0 when not known
	Done, Started           int    // its agents that returned, and started so far
	For                     string // how long it has run ("12 minutes"), "" under a minute
}

// SnapshotProfile is the model profile in use (docs/assistant/model-profiles.md) with its tiers, the Agent presets
// agent_start and task_plan take; Unreadable when the profiles could not be read, Gone when the one in use was deleted
// (a rollback to it).
type SnapshotProfile struct {
	ID, Name         string
	Tiers            []SnapshotTier // by name
	Unreadable, Gone bool
}

// SnapshotTier is one tier of the profile in use: what an Agent started on it runs on ("" model or effort: the
// launcher's), and when to use it.
type SnapshotTier struct {
	Name, Launcher, Model, Effort, Description string
}

// Snapshot is the live context a starting assistant gets in its system prompt.
type Snapshot struct {
	ModelProfile  *SnapshotProfile // nil: none in use; always refreshed outside the cached sources
	Now           time.Time        // in the user's zone
	Zone          string
	Machines      []SnapshotMachine
	Needs, Unread []SnapshotAgent
	// Waiting: stopped on something the hub waits out for them (a retry, a deploy, another agent); Done: finished
	// within a day. Their details stay out of the snapshot; Done renders only a count and agents_list state=done.
	Waiting, Done []SnapshotAgent
	// Awaiting: stopped on a question to the user, who looked at it and has not answered (docs/protocol/agent-states.md
	// "Awaiting your reply", #5096).
	Awaiting []SnapshotAgent
	// Muted: agents (or their spaces) the user turned notifications off for that would be in one of the above; in
	// none of them, as the Live Activity and the Agents badge count none of them (#4562).
	Muted          []SnapshotAgent
	Working        []SnapshotAgent
	Workflows      []SnapshotWorkflow // background workflow runs still running, newest first
	PhoneOnline    bool
	Apps           []SnapshotApp      // one per kind of device, the user's own before a simulator
	KB             string             // RenderKBDigest, "" leaves it out
	Chats          *SnapshotChats     // nil: Feishu did not answer in time
	Calendar       *SnapshotCalendar  // nil: the calendar did not answer in time
	Followups      []SnapshotFollowup // the next eight
	FollowupsTotal int                // all of them
	Routines       []SnapshotRoutine  // the daily tasks that are on (at most eight)
	RoutinesTotal  int                // all of them
	Missed         []SnapshotMiss     // scheduled runs that failed or were skipped since yesterday 18:00, newest first
	MissedTotal    int                // all of them (Missed is the newest six)
	Tasks          []SnapshotTask     // tasks opened from assistant conversations (task_plan), newest first
	Plans          []SnapshotPlan     // plan cards in the main conversation waiting for "Start", newest first (at most five)
	PlansTotal     int
	// Asked: the assistant's ask_user questions nobody answered in time, now to-dos waiting for the user's call (#1617),
	// oldest first.
	Asked []SnapshotAsked
	// Unaccepted: deployed tasks the user has not passed yet (their "Please verify" ask, with a Lark card or without) (#2768), and
	// supervised tasks' conclusions the user has not answered (#1226), oldest first; the morning briefing brings them up again.
	Unaccepted      []SnapshotAccept
	UnacceptedTotal int // all of them (Unaccepted is the oldest eight)
	// Approvals: open tasks whose finished work waits for the user's nod to go live (merge, deploy, turn on, send),
	// oldest first: told to the user as one list to approve at once (docs/assistant/todos.md "Delivery").
	Approvals []SnapshotApproval
	// Omit: the sections left out to keep the block within its budget (assistant/context.go fitSnapshot).
	Omit SnapshotOmit
}

// SnapshotOmit marks the sections a snapshot leaves out for size (#3203). An omitted section still renders its heading
// and its count, with the tool that lists it, never as empty: the node sends only the sections that changed, so a
// section that went missing would read as unchanged, and one rendered without its items as "none".
type SnapshotOmit struct {
	Waiting, Working, Unread, Routines, Followups, Asked, KB, OtherChats, Unaccepted, Tasks, Approvals bool
}

// SnapshotCut is the line a snapshot cut short ends with (assistant/snapshot_cut).
func SnapshotCut() string { return render("assistant/snapshot_cut", nil) }

// SnapshotApproval is a task held short of live until the user approves: what it is, how far it got, where it is held.
type SnapshotApproval struct {
	ID, Title, Stage, Gate, Link string
}

// SnapshotAccept is a deployed task waiting for the user to check it, or a conclusion waiting for the user: its verdict
// line as its card says it, what changed (deployed), where to look, since when.
type SnapshotAccept struct {
	Title, Verdict, Changed, Link string
	Since                         time.Time // in the user's zone
}

func (s Snapshot) Up() []SnapshotMachine   { return s.machines(true) }
func (s Snapshot) Down() []SnapshotMachine { return s.machines(false) }

func (s Snapshot) machines(online bool) []SnapshotMachine {
	var out []SnapshotMachine
	for _, m := range s.Machines {
		if m.Online == online {
			out = append(out, m)
		}
	}
	return out
}

// Upcoming is the calendar without the meetings over by Now (the snapshot's sources are read less often than it is
// rendered).
func (s Snapshot) Upcoming() *SnapshotCalendar {
	if s.Calendar == nil {
		return nil
	}
	out := SnapshotCalendar{}
	for _, e := range s.Calendar.Events {
		if e.End.After(s.Now) {
			out.Events = append(out.Events, e)
		}
	}
	return &out
}

func RenderSnapshot(s Snapshot) string { return render("assistant/snapshot", s) }

// RenderSnapshotWorkflow is one running workflow, in a line ("'parity-audit' stage Verify (2/2), 169/185 agents done, running 14 minutes").
func RenderSnapshotWorkflow(w SnapshotWorkflow) string {
	return render("assistant/snapshot_workflow", w)
}

// AssistantContext is the hub's answer to assistant.context: the snapshot, for the main conversation with a line
// saying so first (assistant/context). Node puts what changed of it at the start of each message (TurnContext).
type AssistantContext struct {
	Main     bool
	Leader   bool // a leader conversation (#3133): it supervises the tasks it hands out
	Owner    string
	Snapshot string
	// Directions: for the main conversation, the current leaders it hands work to (task_plan supervisor, #3609);
	// LeaderName: for a leader, its direction's name.
	Directions []ContextDirection
	LeaderName string
}

// ContextDirection is one current leader as the main conversation is told of it: its conversation id and name.
type ContextDirection struct{ ID, Name string }

func RenderAssistantContext(c AssistantContext) string { return render("assistant/context", c) }

// TaskRun is the message a scheduled task's run starts with (assistant/task_run).
type TaskRun struct {
	Label          string
	Once, Interval bool   // neither: daily
	Every          string // interval: "30 minutes"
	// Accelerated: a leader's open supervised todos shortened this run's interval.
	Accelerated bool
	Late        string // "" when on time; else how late ("25 minutes")
	Due         time.Time
	Prompt      string
	KBHint      bool // daily runs with the knowledge base on
	// KnowledgeDir: the knowledge base directory the hint names; "" is the default ~/.persona/data.
	KnowledgeDir string
	// Continued: it goes into the task's own conversation, which got the background on the first run.
	Continued bool
	// PushAsked: the task pushes only what the run sends with notify_user (its push "asked", #2396).
	PushAsked bool
	// Facts: the run's facts Persona worked out (RenderBriefFacts), above the task's prompt; "" for a task without them.
	Facts string
}

func RenderTaskRun(t TaskRun) string { return render("assistant/task_run", t) }

// BriefFacts are the facts of a morning or evening routine's run, as the model is given them (assistant/routine_facts):
// the lines are ready to read; the totals count more than the lines when the lists were cut.
type BriefFacts struct {
	// NorthStar: the lines the report opens with (the interruptions and the availability today, their change against
	// yesterday until the same time, and where they come from), ready to read.
	NorthStar                                                        []string
	ConfirmationSummary                                              string
	Confirmations                                                    []string
	Hours                                                            int // how far back "done" and "not through" look
	WaitingTotal, DoneTotal, FailedTotal, AcceptTotal, ProgressTotal int
	WaitingUnknown                                                   bool // the cards waiting were not read yet: say nothing of whether any waits
	Card                                                             bool // a Lark card lists them for the user: the model need not
	LaterTotal                                                       int  // questions Agents left for a briefing: the model says how many, never lists them
	Waiting                                                          []string
	Done                                                             []string
	Failed                                                           []BriefFailed
	Accept                                                           []BriefAccept
	Progress                                                         []BriefProgress
	Leaders                                                          []BriefLeader // one per direction (#3609)
	// Briefs: the open pieces of work that need the owner or changed since the last briefing, in their own words;
	// BriefsDone: those that ended since, once, with each criterion's result (#3822).
	Briefs, BriefsDone []BriefTask
	// Day: the evening routine's today's all-day recording (#4741), for the model only; nil leaves the section out.
	Day *BriefDay
}

// BriefDay is today's all-day recording in the evening routine's facts (#4741), the day's parts merged by time; the
// clock times are the owner's ("15:04", with the date when it is not today). Nothing of it is in the card, the brief
// item or a channel, so the model says it even when Card is set.
type BriefDay struct {
	Date     string // the owner's today, YYYY-MM-DD
	From, To string // when it was recorded: from its start to its end, or now while it is open
	// Transcribed is how far its text goes when that is short of To (the phone transcribes and uploads as it can).
	Transcribed string
	// Gaps are the time with no text by reason, the reasons with the most of it; GapReasonsLeft counts the other
	// reasons, GapsLeft their stretches and GapMinutesLeft their minutes.
	Gaps                                     []BriefDayGap
	GapReasonsLeft, GapsLeft, GapMinutesLeft int
	// Parts are each part's recap or, without one, its state, the first and the latest ones; PartsLeft counts those
	// from the middle left out (the section's bound).
	Parts     []BriefDayPart
	PartsLeft int
	// Timeline is every part's recap entries by time; Left counts those from the middle of the day left out (the
	// section's bound).
	Timeline []BriefDayEntry
	Left     int
	// Actions are the suggested to-dos the user has not confirmed, by time; ActionsLeft counts those left out.
	Actions     []BriefDayAction
	ActionsLeft int
}

// BriefDayGap is the time of one reason with no text (paused, interrupted, stopped, pending, lost, erased, or the
// phone's own word): how many stretches, how many minutes in all, the longest of them (More: there are others).
type BriefDayGap struct {
	Reason         string
	Count, Minutes int
	Ranges         []string
	More           bool
}

// BriefDayPart is one part of the day: its recap (Made when it was written, Upto how far it read, Summary) and State
// "pending" or "failed" when a newer one is being written or failed; without a recap, State is "pending", "failed",
// "none" (nothing transcribed in Recorded minutes) or "" (not due yet).
type BriefDayPart struct {
	Part           int
	Made, Upto     string
	Final, Partial bool
	Summary        string
	State          string
	Recorded       int
}

// BriefDayEntry is one entry of the day's timeline, without its points.
type BriefDayEntry struct {
	From, To string
	Meeting  bool
	Title    string
}

// BriefDayAction is a suggested to-do not confirmed yet.
type BriefDayAction struct{ Who, What string }

// BriefDayRunes is how many runes day adds to the facts (the evening routine keeps it about 4000).
func BriefDayRunes(day *BriefDay) int {
	return utf8.RuneCountInString(RenderBriefFacts(BriefFacts{Day: day})) - utf8.RuneCountInString(RenderBriefFacts(BriefFacts{}))
}

// BriefTask is one piece of work in the briefing, told from its brief: Date and Words are when the owner asked and
// what they wrote (Words "" when they did not ask: then By says who started it, and Goal what for); Now is where it
// stands in a line, Need what is wanted from the owner, Scope the scope changes since the last briefing, Result each
// criterion's outcome for one that ended.
type BriefTask struct {
	Date, Words, By, Goal string
	Now, Need             string
	// Status: the closeout's status when Now is the agent's own account (done, not_done, blocked). Decide: it waits
	// for the owner's decision and no ask words it. Cancelled: one that ended by being cancelled.
	Status          string
	Decide, Overdue bool
	Cancelled       bool
	// Parked: it is parked waiting for the owner's priority call, and this is the reason in one sentence ("" otherwise).
	Parked          string
	Scope, AlsoDone []string
	Result          []BriefCriterion
}

// BriefCriterion is one criterion's result in the briefing: met, not_met, unchecked, or dropped by scope change Change.
type BriefCriterion struct{ Criterion, State, Change string }

// BriefLeader is one direction in the briefing's facts: its name, its open tasks, those waiting for the owner, those
// stuck over a day, and its leader's latest summary of the day (empty parts: none).
type BriefLeader struct {
	Name                     string
	Open, Waiting, Stuck     int
	Reached, Doing, NeedsYou string
}

// BriefFailed is one thing that did not go through: a task whose agent did not start, a scheduled run that failed
// (Run) or was skipped (Skipped).
type BriefFailed struct {
	Title, Why   string
	Run, Skipped bool
}

// BriefAccept is a task waiting to be verified, with what it changed in the words of its "Please verify" card.
type BriefAccept struct{ Line, Changed string }

// BriefProgress is a task being worked on and its stage ("plan", "develop", "review", "merge", "deploy", "verify"; "" not known).
type BriefProgress struct{ Title, Stage string }

func RenderBriefFacts(b BriefFacts) string { return render("assistant/routine_facts", b) }

// RenderNorthStarFacts is what a leader or main conversation's daily task opens with when it has no facts of its own
// (assistant/north_star_facts): the north star's lines, ready to read.
func RenderNorthStarFacts(lines []string) string { return render("assistant/north_star_facts", lines) }

// RenderMorningRoutine and RenderEveningRoutine are the default daily tasks' prompts.
func RenderMorningRoutine() string { return render("assistant/routine_morning", nil) }
func RenderEveningRoutine() string { return render("assistant/routine_evening", nil) }

// FormerMorningRoutines and FormerEveningRoutines are the earlier defaults of those prompts, oldest first: a hub that
// saved one unchanged gets the current default (assistant: upgradeDefaultRoutines). Never edited: each is what a hub
// may hold byte for byte.
func FormerMorningRoutines() []string {
	return []string{render("assistant/routine_morning_former1", nil), render("assistant/routine_morning_former2", nil), render("assistant/routine_morning_former3", nil)}
}
func FormerEveningRoutines() []string {
	return []string{render("assistant/routine_evening_former1", nil), render("assistant/routine_evening_former2", nil)}
}

// RenderLostTasks tells an assistant conversation that a restart stopped its background tasks (assistant/lost_tasks).
func RenderLostTasks(tasks []string) string {
	return render("assistant/lost_tasks", struct{ Tasks []string }{tasks})
}

// Interject is the note on a message the user sent while the conversation was still answering
// (node/assistant_interject.go). Background: its foreground work was moved to the background for it.
type Interject struct {
	Background bool
}

// RenderInterject goes with such a message: say in one sentence, before the next tool call, what is going on, then
// answer it (#4883: six minutes of tool calls with no word to the owner). A <persona-context> block only.
func RenderInterject(v Interject) string { return render("assistant/interject", v) }

// RenderUsageRetry sends a conversation's message to the model again after a gateway's impossible usage made Claude
// Code refuse it as too long and Persona repaired the transcript (assistant/usage_retry, node/assistant_usage.go). It
// is a <persona-context> block only: the apps show no bubble for it.
func RenderUsageRetry() string { return render("assistant/usage_retry", nil) }

// RenderModelRetry resumes the same request after a bounded transient model failure. It adds no user bubble and never
// rewrites the original request (node/assistant_recovery.go).
func RenderModelRetry() string { return render("assistant/model_retry", nil) }

// TaskNotice tells the main conversation (or a reporting one) about one of its quiet tasks (#1731): it was done, waits for the user, was
// cut off by the model service or did not start. The assistant decides what to tell the user.
type TaskNotice struct {
	Title, Machine string
	State          string   // done | needs | interrupted | failed
	Earlier        []string // what else it went through since the last notice ("waiting on the user", "done"), oldest first
	Ask            string   // needs: what it asks
	Text           string   // what its agent said last, or why it did not start
	// Reporting: the notice goes into the conversation that opened the task (one started with report_tasks, such as
	// a Feishu /agent's, #3192), not the main conversation.
	Reporting bool
}

func RenderTaskNotice(n TaskNotice) string { return render("assistant/task_notice", n) }

// SpawnNotice tells the conversation that opened another with conversation_new that the other failed, or ended a turn
// without reporting back with conversation_send (#4669): once per opened conversation.
type SpawnNotice struct {
	ID, Title, Link string
	Failed          bool
	Error           string // Failed: the turn's error
	Last            string // what it said last, when it did not fail
}

func RenderSpawnNotice(n SpawnNotice) string { return render("assistant/spawn_notice", n) }

// TaskEnded tells a conversation that is not the main one that a task it handed out with task_plan, supervised by the
// main conversation, ended (#4669): accepted or cancelled, with the task's link (or its id without one).
type TaskEnded struct {
	ID, Title, Link string
	Verdict         string // accepted | cancelled
	Note            string
}

func RenderTaskEnded(n TaskEnded) string { return render("assistant/task_ended", n) }

// AskTodoAnswer is the message to the conversation whose ask_user question became a to-do (#1617): the user's call on it
// (Thread: said in the to-do's Lark topic), or that the to-do was closed without one (Dropped). The questions and choices
// are escaped by the caller.
type AskTodoAnswer struct {
	When      string // when it was asked, MM-DD HH:MM in the user's zone
	TodoID    string
	Questions []string
	Choices   []AskTodoChoice
	Thread    bool
	Dropped   bool
	Several   bool // more than one question was asked
	// Defaulted: nobody decided by the question's deadline (Deadline, MM-DD HH:MM) and it could be undone, so its
	// recommended options were taken by default (#3127).
	Defaulted bool
	Deadline  string
}

// AskTodoChoice is one call of the user's: the question as they wrote it ("" for a reply in the topic), the choice, why.
type AskTodoChoice struct {
	Question string
	Choice   string
	Reason   string
}

func RenderAskTodoAnswer(a AskTodoAnswer) string { return render("assistant/ask_answer", a) }

// Supervise is a supervise message: the tasks the main conversation handed out that need it now (#3124), what waits on a
// decision first.
type Supervise struct {
	Level   string
	Entries []SuperviseEntry
	// Leader: it goes to a leader conversation, for the tasks it handed out (#3133); Escalated: an entry is a leader's
	// needs_user, told the main conversation.
	Leader, Escalated bool
	// Side: it goes to an ordinary side conversation, for the tasks that conversation handed out itself.
	Side bool
	// LeaderStuck: an entry is a leader's task stuck over a day (sigLeaderStuck), told the main conversation (#3609).
	LeaderStuck bool
	// Overdue: an entry is a task past its brief's clock (#3822).
	Overdue bool
	// ParkedDue: an entry is a parked task whose time came (#3804).
	ParkedDue bool
	// Asked: an entry is an agent's cards that came to this conversation before the user (ask); AskMinutes how long
	// they wait here before going up a rung.
	Asked      bool
	AskMinutes int
	// Decisions: an entry is about a decision the owner cannot answer as it is (recheck, undecidable,
	// owner_cannot_answer; #3804).
	Decisions bool
	// Inbox: an entry is new Inbox messages from other people about the task (#4293).
	Inbox bool
}

// SuperviseEntry is one task in it, with what happened (Kind: review, idle, needs_you, stalled, interrupted, failed,
// started, escalated, ask, inbox…).
type SuperviseEntry struct {
	ID, Title, Kind string
	Detail          string             // why: the judge's reason, the step it is stuck on, the model service's error, why it did not start
	Minutes         int                // how long it has waited (needs_you) or not moved (stalled)
	Ask             string             // needs_you: what it asks
	Recommend       string             // escalated: the option the leader recommends
	LeaderGone      bool               // decide: its task leader agent cannot be reached, so the supervisor takes the task over
	DoneWhen        []string           // what done means
	Round           int                // reworks so far
	Machine         string             // where its agent runs (a name)
	Session         string             // its agent's session id (short)
	AgentID         string             // full id for durable deduplication; not rendered
	Last            string             // what its agent said last
	Closeout        *SuperviseCloseout // the closeout its agent ended the turn with (#3612), nil when it wrote none
	Link            string             // the task's page ("" when the hub has no public_url)
	Quiet           bool               // the user was not pushed about it
	Orphan          string             // orphaned: why nobody works on it (error, closed, offline)
	Review          *SuperviseReviewed // review: what the independent reviewer found (#2173), nil when none ran
	// report: what its agent reported (progress, question, blocked, done), its words, evidence and step.
	Report      string
	Said        string
	Evidence    []string
	Step, Steps int
	// failed with its tab kept (#2570): until when (local "15:04"), the question its shell waits on, the tab's last
	// lines, and how many retries are left; KeptUntil "" when the start failed for good.
	KeptUntil, Question, Screen string
	RetriesLeft                 int
	// ask: the cards waiting, and the task's decisions in effect (prompts/todo/decisions.md, rendered); escalated: the
	// cards a leader gave the user.
	Asks      []SuperviseAsk
	Decisions string
	// recheck and owner_cannot_answer: the decision as it was asked; owner_cannot_answer: the owner's answer
	// (decide_yourself, not_what_i_asked, explain_first).
	Decision *SuperviseDecision
	Response string
	// parked_due: what the task was parked for.
	Parked *SuperviseParked
	// From the task's brief (#3822): Asked is the owner's message that led to the work (clipped) and AskedAt its day;
	// Started says who started it when it was not the owner; Dropped are the criteria scope changes took out.
	// (assistant, leader, routine, hub) and StartedWhy the reason given.
	Asked, AskedAt, Started, StartedWhy string
	Dropped                             []string
	// inbox: the Inbox item the new messages came in, the newest when several did.
	Inbox *SuperviseInbox
}

// SuperviseInbox is the Inbox item an inbox entry names (#4293): its id, title (the chat's name or the mail's subject),
// who wrote the newest message, a one-line preview, and how many messages from other people came in about the task
// since its supervisor last heard (the newest item's, and any other item's linked to the same task meanwhile).
type SuperviseInbox struct {
	Item, Title, Sender, Preview string
	Count                        int
}

// SuperviseParked is what a task was parked for (task_review parked), as its parked_due entry repeats it: the reason,
// what kind of thing it waits on, until when (local), how many times in a row it was parked with no agent turn in
// between, and how many more times it can be.
type SuperviseParked struct {
	Reason, WaitingOn, Until string
	Count, Left              int
}

// SuperviseDecision is a decision a task's supervisor put to the owner (task_review needs_user), as a supervision
// entry repeats it: why only the owner can decide, the question, the options with what each leads to, the one
// recommended, what waiting costs, and whether it is never taken by default.
type SuperviseDecision struct {
	WhyOwner, Question, Recommend, Cost string
	Options                             []SuperviseOption
	NeverDefault                        bool
	// Background: where the task came from and where it stood, as the hub told the owner.
	Background string
}

// SuperviseOption is one option of a SuperviseDecision.
type SuperviseOption struct{ Label, LeadsTo string }

// SuperviseAsk is one card of a task's agent on its way to the user: its interaction id, kind (question, permission,
// plan), the agent's machine (a name) and short session id, what it asks, and whether the rung below did not handle it in time.
type SuperviseAsk struct {
	ID, Kind, Machine, Session, Text string
	Raised                           bool
}

func RenderSupervise(s Supervise) string { return render("assistant/supervise", s) }

// SuperviseReviewed is the independent review of a task waiting for review: State clean (nothing blocking), blocking
// (blocking findings left once the rework rounds ran out) or failed (Error says why), its counts and report.
type SuperviseReviewed struct {
	State           string
	Count, Blocking int
	Findings, Error string
}

// SuperviseRework is what a task's agent is sent when its supervisor sends the work back.
type SuperviseRework struct {
	Round, Max int
	Checks     []SuperviseCheck
	Note       string
}

type SuperviseCheck struct {
	Criterion, Evidence string
	Met                 bool
}

func RenderSuperviseRework(r SuperviseRework) string { return render("assistant/supervise_rework", r) }

// SuperviseReviewRework is what a task's agent is sent when the independent reviewer found blocking problems in its
// change (#2173): the round, and the reviewer's report as it wrote it.
type SuperviseReviewRework struct {
	Round, Max      int
	Count, Blocking int
	Findings        string
}

func RenderSuperviseReviewRework(r SuperviseReviewRework) string {
	return render("assistant/supervise_review_rework", r)
}

// RenderTaskDoneWhen is what follows a supervised task's prompt: what done means, and that the agent shows it.
func RenderTaskDoneWhen(criteria []string) string {
	return render("assistant/task_done_when", struct{ Criteria []string }{criteria})
}

// Relay is a message one assistant conversation sends another (conversation_send / conversation_new): what was
// written, behind a note saying where it comes from and how many hops it is from the user's own message.
type Relay struct {
	FromID, FromTitle string
	Tool              string // conversation_send | conversation_new
	Hop, MaxHops      int
	Text              string
}

// RenderRelay is the message the other conversation gets (assistant/relay).
func RenderRelay(r Relay) string { return render("assistant/relay", r) }

// RenderRefused is passed into a running Codex turn when a tool call was refused (assistant/refused).
func RenderRefused(reason string) string { return render("assistant/refused", reason) }

// CodexMemory tells the Codex assistant about the memory it shares with Claude (assistant/codex_memory).
type CodexMemory struct {
	Dir       string
	Index     string // MEMORY.md, trimmed
	Truncated bool   // Index is the start of a longer one
}

func RenderCodexMemory(m CodexMemory) string { return render("assistant/codex_memory", m) }

// RuntimeInstructions is what the assistant on an agent runtime (DSH…) gets with a new conversation's first message
// (assistant/runtime_instructions): the shared memory, and the skills' gated commands it runs as tools.
type RuntimeInstructions struct {
	Dir       string
	Index     string // MEMORY.md, trimmed
	Truncated bool   // Index is the start of a longer one
	// Gates are the skills' gated commands this machine runs as tools (a plugin's assistant.gate with a tool).
	Gates []RuntimeGate
}

// RuntimeGate is one gated command as a runtime's tool: what the card calls it, the command, the tool.
type RuntimeGate struct {
	Title, Command, Tool string
}

func RenderRuntimeInstructions(r RuntimeInstructions) string {
	return render("assistant/runtime_instructions", r)
}

// RenderCodexInstructions is the Codex assistant's developer instructions (assistant/codex_instructions): the memory.
// The live snapshot is not in them but in each message (TurnContext), like Claude's.
func RenderCodexInstructions(memory string) string {
	return render("assistant/codex_instructions", struct{ Memory string }{memory})
}

// TurnContext is the <persona-context> block a message to the assistant starts with (assistant/turn_context): the
// sections of the live snapshot (AssistantContext) the conversation hasn't seen yet. Full: all of them (the
// conversation is new, was compacted, or node lost track); otherwise only those that changed, the time always.
type TurnContext struct {
	Full     bool
	Sections []string
}

func RenderTurnContext(c TurnContext) string { return render("assistant/turn_context", c) }

// TaskDone is the system prompt deciding whether a task opened from an assistant conversation is finished, so the
// hub can mark its todo done (assistant/task_done, docs/assistant/todos.md "Automatic completion").
type TaskDone struct {
	Owner string
	// Delivery: also say how far the work was delivered (assistant.done_needs_delivery, docs/assistant/todos.md "Delivery"): only
	// live or nothing to ship closes the task.
	Delivery bool
	// Checks: also check each done_when criterion (checkable, met, evidence) and say the delivery, for the judge's own
	// check of a supervised task (assistant.review_sample_percent below 100, docs/assistant/todos.md "Spot-check reviews").
	Checks bool
	// Language: the user's language for the reason, which the apps show on the task ("" for Chinese, the prompt's own).
	Language string
}

func RenderTaskDone(d TaskDone) string { return render("assistant/task_done", d) }

// RenderTaskDelivery is the delivery standard a task's agent is handed after its prompt (assistant/task_delivery,
// docs/assistant/todos.md "Delivery"): done is live and checked, and its last line says how far it got. closeout: the
// closeout's form follows in the same prompt (agents/closeout_brief), so the line goes before that block, not last.
func RenderTaskDelivery(closeout bool) string {
	return render("assistant/task_delivery", struct{ Closeout bool }{closeout})
}

// TaskAttachments ends a message to an agent with the files and pictures the user gave the assistant for it (#2437):
// where they are on the agent's computer (Copied: copied there from the assistant's), and those that could not come.
// Pictures go last, in the <attached_images> block a picture sent straight to an agent ends with (node/send.go), so
// the apps draw them as they do those.
type TaskAttachments struct {
	Files, Missing, Pictures []string
	Copied                   bool
}

func RenderTaskAttachments(a TaskAttachments) string {
	return strings.TrimRight(render("assistant/task_attachments", a), "\n")
}

// SideConversation preserves the provenance of a non-owner's message to a tasked Agent.
type SideConversation struct {
	From, Text string
}

func RenderSideConversation(in SideConversation) string {
	return render("assistant/side_conversation", in)
}

// TaskDoneInput is the task as it was handed out and what its agents said when they stopped (assistant/task_done_input).
type TaskDoneInput struct {
	Title, Prompt string
	// From the task's brief (#3822): Asked is the owner's message that led to the work, verbatim, and AskedAt its day;
	// Dropped the criteria logged scope changes took out. Criteria are the brief's as those changes left them.
	Asked, AskedAt string
	Dropped        []string
	Replies        []TaskDoneReply
	Criteria       []string `json:"criteria,omitempty"`
	// Hints the hub found in the replies: a merged pull request, a TestFlight build number.
	MergedPR, Build bool
}

// TaskDoneReply is one agent's last reply, and what the user last asked it in that turn when it was not the task's
// own prompt (a follow-up the user sent the agent later).
type TaskDoneReply struct {
	Machine, Ask, Text string
	From               string `json:"from,omitempty"`
	Source             string `json:"source,omitempty"`
	// Earlier is the agent's reply in the turn before the user's own follow-up in this session (its report of the
	// work, when the follow-up only asks whether it is done); never set for a side-conversation turn (#3111).
	Earlier string `json:"earlier,omitempty"`
}

func RenderTaskDoneInput(in TaskDoneInput) string { return render("assistant/task_done_input", in) }

// MainHandoff is the first message of a new main conversation: what the old one left (its last compaction summary,
// the messages after it), behind a note, then what the user wrote (docs/assistant/assistant.md "New main chat").
type MainHandoff struct {
	FromID  string
	Until   string // when the old one was archived, in the user's words
	Summary string
	Recent  []HandoffTurn
	Text    string
}

// HandoffTurn is one message of the old conversation: Role user or assistant.
type HandoffTurn struct {
	Role string
	Text string
}

// RenderMainHandoff is assistant/main_handoff.
func RenderMainHandoff(h MainHandoff) string { return render("assistant/main_handoff", h) }

// Acceptance is a deployed task, for the three lines of its "Please accept" card (assistant/acceptance_input; the system
// prompt is assistant/acceptance; docs/extensions/bots.md "Task progress and review requests").
type Acceptance struct {
	Title, Prompt, Reply string
	Links                []string // the only links it may pick
}

func RenderAcceptance() string                  { return render("assistant/acceptance", nil) }
func RenderAcceptanceInput(a Acceptance) string { return render("assistant/acceptance_input", a) }

// WorkspaceMention is what the assistant gets for a workspace the user @-mentioned (assistant/workspace_mention,
// #5594): where it is, the knowledge base's projects there and the agents that worked there lately. It replaces the
// client's <persona-context kind="workspace"> block in the message.
type WorkspaceMention struct {
	Title, Machine, MachineName, Cwd, Branch string
	Online                                   bool
	Projects                                 []WorkspaceProject
	Sessions                                 []WorkspaceSession
}

// WorkspaceProject is a knowledge-base project whose directories hold the workspace.
type WorkspaceProject struct {
	Name, Slug, OneLine string
}

// WorkspaceSession is an agent that worked in the workspace's directory: Cwd only when it is a folder under it.
type WorkspaceSession struct {
	Title, Agent, Status, When, ID, Cwd string
}

func RenderWorkspaceMention(w WorkspaceMention) string {
	return render("assistant/workspace_mention", w)
}

// RenderWorkoutReport describes one consented workout, never a medical diagnosis.
func RenderWorkoutReport(data string) string { return render("assistant/workout_report", data) }

// SuperviseCloseout is an agent's own closeout as a supervision message shows it: its claim, to be checked.
type SuperviseCloseout struct {
	Status, Waiting   string
	Merged, Deployed  string // "yes", "no" or ""
	Did               []string
	Verified, Missing string
	Artifacts         []string
	// CheckedMerged, CheckedDeployed: what the hub found itself, "yes", "no" or "" when it could not tell.
	CheckedMerged, CheckedDeployed string
}

// Route is the system prompt of assistant.route (assistant/route): is a New chat's first message a direction or a quick
// chat. Language is the direction name's language by its own name ("" Chinese); Crowded: there are Leaders leaders
// already, so the model names the closest one too.
type Route struct {
	Language string
	TitleMax int
	Crowded  bool
	Leaders  int
}

func RenderRoute(r Route) string { return render("assistant/route", r) }

// RouteLeader is a current leader as the route model is shown it.
type RouteLeader struct{ ID, Title, Project string }

// RouteInput is what the route model reads: the sentence, the leaders and the knowledge base's project names.
type RouteInput struct {
	Sentence string
	Leaders  []RouteLeader
	Projects []string
}

func RenderRouteInput(in RouteInput) string { return render("assistant/route_input", in) }

// RenderRouteAgent is the system prompt of assistant.route for a new agent (assistant/route_agent): which leader's
// direction its work belongs to; it reads RouteInput (the agent's first message as the sentence, no projects).
func RenderRouteAgent() string { return render("assistant/route_agent", nil) }
