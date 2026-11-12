package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
)

// Tasks dispatched from an assistant conversation (docs/assistant/unified-entry.md, #930). The assistant hands the user a plan
// (task_plan); read-only plans, tasks the user asked for in so many words (#2559) and kinds the user trusts start at
// once, the rest wait for the user's "Start" on the plan card. A task is a todo (todos.go) whose origin is the conversation; its agents are ordinary agents
// (the Agents page shows them as always). What the assistant adds to the conversation — the plan card, a started
// task, an answered question, a receipt, a message sent straight to a task — is the conversation's feed, kept apart
// from Claude's transcript.

// PlanReroute records that a flexible task was moved off its requested machine because its launcher failed a health
// check, naming both machines by their display names and the health check's reason, so a card/receipt can show where
// the work was meant to run and where it landed instead (#2271).
type PlanReroute struct {
	// FromMachine is the display name of the machine the task was first placed on; it stays the original across
	// repeated moves. ToMachine is where it runs now; Reason is healthDetour's note (failure kind and clipped error).
	FromMachine string `json:"from_machine"`
	ToMachine   string `json:"to_machine"`
	Reason      string `json:"reason,omitempty"`
}

// PlanTask is one task of a plan, as the assistant proposed it and the user may have edited it.
type PlanTask struct {
	Title   string `json:"title"`
	Prompt  string `json:"prompt"`
	Machine string `json:"machine"`
	// MachinePolicy defaults to fixed; only explicit flexible tasks may move to another machine.
	MachinePolicy     string                 `json:"machine_policy,omitempty"`
	Cwd               string                 `json:"cwd"`
	Workspace         *TaskWorkspace         `json:"workspace,omitempty"`
	WorkspaceResolved *TaskWorkspaceResolved `json:"workspace_resolved,omitempty"`
	// Hub-owned identity for the default branch's preview, before a todo exists.
	ReservedTodoID    string `json:"reserved_todo_id,omitempty"`
	WorkspaceInferred bool   `json:"workspace_inferred,omitempty"`
	Launcher          string `json:"launcher,omitempty"`
	Model             string `json:"model,omitempty"`
	Effort            string `json:"effort,omitempty"`
	Tier              string `json:"tier,omitempty"`
	Permission        string `json:"permission,omitempty"`
	// Ultracode: the agent starts with Claude Code's ultracode on (#12). A pointer so that an edit from the card can
	// turn it off, and one from a client that does not know it keeps the assistant's choice.
	Ultracode *bool `json:"ultracode,omitempty"`
	Readonly  bool  `json:"readonly,omitempty"`
	// UserRequested: the user asked for this very task in the turn that planned it (#2559); with
	// assistant.start_requested_tasks on it starts without the card's "Start".
	UserRequested bool   `json:"user_requested,omitempty"`
	Why           string `json:"why,omitempty"`
	// OwnerStarted: the owner tapped Start on the plan's card (POST …/start), as against the hub starting it without
	// asking; the hub's, never what a tool call or an edit says. What a suggested rule counts (rule_suggest.go, #4929).
	OwnerStarted bool `json:"owner_started,omitempty"`
	// Reroute is set once a flexible task has been moved to another machine; nil while it runs where it was asked.
	// Repeated moves keep the original FromMachine and refresh ToMachine and Reason (#2271).
	Reroute *PlanReroute `json:"reroute,omitempty"`
	// DoneWhen: what done means, criteria the supervisor checks before it accepts the work; ExpectMinutes: how long it
	// should take (#3124, supervise.go). Both optional; the agent is told the criteria with its prompt.
	DoneWhen      []string `json:"done_when,omitempty"`
	ExpectMinutes int      `json:"expect_minutes,omitempty"`
	// The brief (#3822, task_brief.go): Goal is the work in one sentence (the title when left out), OutOfBounds what
	// the agent must not do. Continues: the to-do this task takes over from (a retry on another computer, a rework
	// round, a task split off it), for ContinuesWhy: it then works for that to-do's brief instead of a new one. Brief is
	// the hub's: the brief as it will be kept, shown on the plan card so that "Start" confirms it; never what the tool
	// call or a card's edit says.
	Goal         string   `json:"goal,omitempty"`
	OutOfBounds  []string `json:"out_of_bounds,omitempty"`
	Continues    string   `json:"continues,omitempty"`
	ContinuesWhy string   `json:"continues_why,omitempty"`
	// StartedWhy: why the assistant starts this on its own, when the owner did not just ask for it.
	StartedWhy string     `json:"started_why,omitempty"`
	Brief      *TaskBrief `json:"brief,omitempty"`
	// Supervisor: the leader conversation the main conversation handed the task to (task_plan's supervisor, #3608);
	// "" the plan's own conversation supervises it, as before.
	Supervisor string `json:"supervisor,omitempty"`
	// ReuseTodo: the todo this task runs on instead of a new one (task_plan's todo_id, #5279): one this conversation
	// opened that has no agent yet, such as the todo a Lark topic opened (feishu_thread_open.go). Checked when the
	// plan is made and again when it starts; it is not what the apps send back with an edit.
	ReuseTodo string `json:"reuse_todo,omitempty"`
	// Role: "leader" makes the task's agent a task leader that may hand out workers beneath this task (task_leader.go,
	// #3656); "" is an ordinary worker. Only a supervising conversation's task_plan sets it. Parent: the leader's task
	// this task is a worker of; the hub's, never what a tool call or an edit says.
	Role   string `json:"role,omitempty"`
	Parent string `json:"parent,omitempty"`
	// Project: the knowledge-base project the task's todo goes in (#2581, todo_project.go), checked and kept as its slug
	// when the plan is made; "" for none.
	Project string `json:"project,omitempty"`
	// Attachments: pictures and files the user gave the assistant for this task, by their paths on AttachFrom, the
	// assistant's computer (#2437, attachments.go); Mentioned: those of them the prompt names by path without listing
	// them. Checked when the plan is made; copied to the task's computer when it starts. AttachFrom and Mentioned are
	// the server's, never what the tool call says.
	Attachments []string `json:"attachments,omitempty"`
	Mentioned   []string `json:"mentioned,omitempty"`
	AttachFrom  string   `json:"attach_from,omitempty"`
	// Set once started: the todo the task is, and why its agent did not start. TodoID is cleared when another plan
	// takes over that to-do; ReuseTodo keeps the historical target. ErrorCode and ErrorArgs: the start's
	// coded refusal when it was one (launcher_missing, claude_missing… with its details, as a session's
	// start_error_code / start_error_args), for a card to offer its remedy (#1642).
	TodoID    string            `json:"todo_id,omitempty"`
	Error     string            `json:"error,omitempty"`
	ErrorCode string            `json:"error_code,omitempty"`
	ErrorArgs map[string]string `json:"error_args,omitempty"`
	// Start: the start being remedied after its agent did not start, or how that ended (start_remedy.go, #2570).
	Start *StartRemedy `json:"start,omitempty"`
	// defaulted: Launcher was filled in from a default by an earlier try of this start (startTaskAgent), not named by
	// whoever planned the task, so a retry may still swap it for one its computer has (launcher_fit.go).
	defaulted bool
	// inPane: the kept tab this try starts in (task_retry), "" for a new one.
	inPane    string
	planID    string
	planIndex int
}

// Readonly starts conservatively in plan. The launcher fit changes Claude to bypassPermissions while preserving Codex's sandbox.
// Never let an omitted mode inherit a writable launcher default before that fit.
func (t *PlanTask) normalizeReadonlyPermission() {
	if t.Readonly {
		t.Permission = "plan"
	}
}

// TaskPlan is a plan the assistant handed the user.
type TaskPlan struct {
	ID           string     `json:"id"`
	Conversation string     `json:"conversation"`
	Tasks        []PlanTask `json:"tasks"`
	State        string     `json:"state"` // planOpen, planStarted, planDismissed
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	// Suggestion: the owner started this kind of task three times; the open plan's card offers to start it directly
	// from now on (rule_suggest.go, #4929). Filled in where the apps read the plan, never kept with it.
	Suggestion *proto.RuleSuggestion `json:"suggestion,omitempty"`
}

const (
	planOpen      = "open"
	planStarted   = "started"
	planDismissed = "dismissed"
)

// FeedItem is one item the hub inserted into an assistant conversation; clients merge them with the transcript by At.
type FeedItem struct {
	ID           string          `json:"id"`
	Conversation string          `json:"conversation"`
	At           time.Time       `json:"at"`
	Kind         string          `json:"kind"` // feedPlan, feedTask, feedAnswered, feedReceipt, feedRouted
	Ref          string          `json:"ref,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
}

const (
	feedPlan     = "plan"     // ref: the plan id
	feedTask     = "task"     // ref: the todo id; body: taskCardBody, a wake for a task that did not start its Agent (taskCard)
	feedAnswered = "answered" // ref: the todo id; body: answeredBody (answered.go)
	feedReceipt  = "receipt"  // ref: the todo id; body: title, state, summary, pr, machine
	feedRouted   = "routed"   // ref: the todo id; body: text, interrupted (agent_interrupt stopped its turn first), from
	feedReport   = "report"   // ref: the todo id; body: reportBody (a supervised task's agent reported, agent_control.go)
	feedMessage  = "message"  // ref: the inbox item id; body: MessageCard (a message about a task, with its draft reply)
)

// routedFromAssistant is a routed item's from when Persona wrote it to the task's agent (agent_send with a task,
// agent_interrupt's then, a rework): the apps draw it folded, as Persona's, not as the user's bubble (#1542). What the
// user sent (a task card's Message, a decision relayed from Lark) has no from.
const routedFromAssistant = "assistant"

const trustSetting = "tasks.trust"

// settleDelay is how long a task's agents stay still before it counts as done: as long as the hub waits before its
// own finished push.
func (s *Service) settleDelay(ctx context.Context) time.Duration {
	return time.Duration(s.hello(ctx).DoneDelayMS) * time.Millisecond
}

type tasksState struct {
	mu sync.Mutex
	// recoverBefore excludes starts admitted by this service before its background loops begin.
	recoverBefore time.Time
	// completionMu serializes a resumed turn with committing an earlier judgment.
	completionMu   sync.Mutex
	planApprovalMu sync.Mutex // serialize pending-plan snapshots without nesting fleet and todo locks
	// Without a store (unit tests) plans and the feed live here.
	plans map[string]*TaskPlan
	feed  map[string][]FeedItem
	// working: when one of a task's agents last started a turn; receipted: when its last receipt went out. A
	// receipt is due when the task settles after working since the last one.
	working      map[string]time.Time
	receipted    map[string]time.Time
	stageReplies map[SessionRef][2]string // task id and last snippet; unchanged list rows do not repeat stage work
	// awaitingAnswer remembers a stopped status whose transcript still ends in a tool.
	awaitingAnswer map[SessionRef]bool
	// limitLooks: when a task agent's usage-limit wait is looked at again (taskTurnEndedAfter), one look per agent
	// however many turns the wait's wakes end.
	limitLooks map[SessionRef]time.Time
	// retrying: the tasks whose agent is starting again after a failed start (one start at a time, #1396).
	retrying map[string]bool
	// outcomes: the completions whose outcome was recorded (todo_outcome.go, outcomeKey).
	outcomes map[string]time.Time
	// reusing: the todos a started plan took over (PlanTask.ReuseTodo) whose agent is not linked yet, so that a second
	// plan cannot take the same todo in that gap (takeTodoForTask).
	reusing map[string]bool
	// outcomeRuns are the outcome recordings still going (goRecordTaskOutcome; the tests wait on them).
	outcomeRuns sync.WaitGroup
	// todayLocks: a conversation's id -> the lock ensureToday holds from looking for the day's "Today" card to
	// appending it, so that concurrent opens of a chat make one card, not one each.
	todayLocks cardLocks
	// planEdits: a plan's lock, by its id, while one of its started tasks changes (patchPlanTask; kept only while held or
	// waited on, as a card's). The tasks are one blob that movePlan writes whole, so two tasks changing at once (their
	// starts failing together) would each write back the other's old fields.
	planEdits cardLocks
	// trustMu serializes the read-modify-writes of tasks.trust (trustKinds, forgetTrust), so that two plans
	// remembered at once, or a forget racing a remember, do not drop each other's kinds.
	trustMu sync.Mutex
	// Admission is serialized through claiming the resolved cwd; claims last until
	// the task's agent link or failure is recorded.
	workspaceMu     sync.Mutex
	workspaceClaims map[string]string
}

func newTasksState() tasksState {
	return tasksState{recoverBefore: time.Now(), plans: map[string]*TaskPlan{}, feed: map[string][]FeedItem{},
		working: map[string]time.Time{}, receipted: map[string]time.Time{}, retrying: map[string]bool{},
		outcomes: map[string]time.Time{}, reusing: map[string]bool{}}
}

// ---- plans and the feed: storage ----

func (s *Service) savePlan(ctx context.Context, p *TaskPlan) error {
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		cp := *p
		cp.Tasks = append([]PlanTask(nil), p.Tasks...)
		s.tasks.plans[p.ID] = &cp
		s.tasks.mu.Unlock()
		return nil
	}
	raw, _ := json.Marshal(p.Tasks)
	return st.CreateTaskPlan(ctx, db.TaskPlanRow{ID: p.ID, Conversation: p.Conversation, Tasks: raw, State: p.State,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt})
}

var errPlanNotFound = errors.New("plan not found")

func (s *Service) loadPlan(ctx context.Context, id string) (*TaskPlan, error) {
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		p := s.tasks.plans[id]
		if p == nil {
			return nil, errPlanNotFound
		}
		cp := *p
		cp.Tasks = append([]PlanTask(nil), p.Tasks...)
		return &cp, nil
	}
	row, err := st.TaskPlan(ctx, id)
	if errors.Is(err, db.ErrTaskPlanNotFound) {
		return nil, errPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return planFromRow(*row), nil
}

// conversationPlans reads persisted plans, or snapshots the storeless plans, without relying on list order.
func (s *Service) conversationPlans(ctx context.Context, conversation string) ([]*TaskPlan, error) {
	var plans []*TaskPlan
	if st := s.store(); st != nil {
		rows, err := st.TaskPlans(ctx, conversation)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			plans = append(plans, planFromRow(row))
		}
	} else {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		for _, plan := range s.tasks.plans {
			if plan.Conversation == conversation {
				cp := *plan
				cp.Tasks = append([]PlanTask(nil), plan.Tasks...)
				plans = append(plans, &cp)
			}
		}
	}
	return plans, nil
}

func planFromRow(row db.TaskPlanRow) *TaskPlan {
	p := &TaskPlan{ID: row.ID, Conversation: row.Conversation, State: row.State, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	_ = json.Unmarshal(row.Tasks, &p.Tasks)
	return p
}

// movePlan moves p from state from to its State with its Tasks; false when someone else moved it first.
func (s *Service) movePlan(ctx context.Context, p *TaskPlan, from string) (bool, error) {
	p.UpdatedAt = time.Now().UTC()
	st := s.store()
	if st == nil {
		s.tasks.mu.Lock()
		defer s.tasks.mu.Unlock()
		cur := s.tasks.plans[p.ID]
		if cur == nil || cur.State != from {
			return false, nil
		}
		cp := *p
		cp.Tasks = append([]PlanTask(nil), p.Tasks...)
		s.tasks.plans[p.ID] = &cp
		return true, nil
	}
	raw, _ := json.Marshal(p.Tasks)
	return st.UpdateTaskPlan(ctx, p.ID, from, p.State, raw, p.UpdatedAt)
}

// patchPlanTask changes task i of started plan planID with change, writes the plan back and tells the apps. The plan
// stays locked from reading it until the apps are told, so what its other tasks record meanwhile stays, and the apps get
// the plan in the order it was written (they keep the last one they got). Telling them is a one-way broadcast, so the
// lock never waits on an answer; change must not call the hub or a node. A plan with no task i, or not started (any
// more), is left as it is.
func (s *Service) patchPlanTask(ctx context.Context, planID string, i int, change func(*PlanTask)) error {
	s.tasks.planEdits.acquire(planID)
	defer s.tasks.planEdits.release(planID)
	p, err := s.loadPlan(ctx, planID)
	if err != nil {
		return err
	}
	if i < 0 || i >= len(p.Tasks) || p.State != planStarted {
		return nil
	}
	change(&p.Tasks[i])
	if ok, err := s.movePlan(ctx, p, planStarted); err != nil || !ok {
		return err
	}
	s.event(ctx, map[string]any{"type": "task_plan", "plan": p})
	return nil
}

// appendFeed adds an item to a conversation's feed and tells the clients.
func (s *Service) appendFeed(ctx context.Context, conversation, kind, ref string, body any) (FeedItem, error) {
	return s.appendFeedAt(ctx, conversation, kind, ref, body, time.Now())
}

// appendFeedAt is appendFeed for an item that belongs at another time (the "Today" card: the start of the day).
func (s *Service) appendFeedAt(ctx context.Context, conversation, kind, ref string, body any, at time.Time) (FeedItem, error) {
	if s.isLeaderRef(conversation) { // a task leader's agent is no conversation: nobody reads a feed there (task_leader.go)
		return FeedItem{}, nil
	}
	it := FeedItem{ID: "feed_" + randomHex(8), Conversation: conversation, At: at.UTC(), Kind: kind, Ref: ref}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return FeedItem{}, err
		}
		it.Body = raw
	}
	if st := s.store(); st == nil {
		s.tasks.mu.Lock()
		if kind == feedMessage && s.duplicateDismissedDraft(it) {
			s.tasks.mu.Unlock()
			return FeedItem{}, nil
		}
		s.tasks.feed[conversation] = append(s.tasks.feed[conversation], it)
		s.tasks.mu.Unlock()
	} else {
		row := db.FeedRow{ID: it.ID, Conversation: conversation, At: it.At, Kind: kind, Ref: ref, Body: it.Body}
		if ds, ok := st.(draftFeedStore); kind == feedMessage && ok {
			inserted, err := ds.AppendDraftFeed(ctx, row)
			if err != nil || !inserted {
				return FeedItem{}, err
			}
		} else if err := st.AppendFeed(ctx, row); err != nil {
			return FeedItem{}, err
		}
	}
	s.event(ctx, map[string]any{"type": "assistant_feed", "conversation": conversation, "item": it})
	if kind == feedReceipt {
		s.interruptsSoon() // a task went on after being cut off (interruptions.go)
	}
	return it, nil
}

const feedPage = 500

// feedItems is a conversation's items after since, oldest first (with the message cards sent after since: the client
// has them, not that they went out), without the plans they name: for readers that look only at the items.
func (s *Service) feedItems(ctx context.Context, conversation string, since time.Time) ([]FeedItem, error) {
	items, err := s.rawFeed(ctx, conversation, since)
	if err != nil {
		return nil, err
	}
	s.settleCards(ctx, items)
	s.fileCards(ctx, items)
	return s.resolveInterruptions(ctx, conversation, items), nil
}

// rawFeed is feedItems as stored, with nothing worked out on reading.
func (s *Service) rawFeed(ctx context.Context, conversation string, since time.Time) ([]FeedItem, error) {
	var items []FeedItem
	if st := s.store(); st == nil {
		s.tasks.mu.Lock()
		for _, it := range s.tasks.feed[conversation] {
			at, changed := cardSentAt(it), cardChangedAt(it)
			if it.At.After(since) || it.Kind == feedMessage && (at != nil && at.After(since) || changed != nil && changed.After(since)) {
				items = append(items, it)
			}
		}
		s.tasks.mu.Unlock()
	} else {
		rows, err := st.Feed(ctx, conversation, since, feedPage)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			items = append(items, FeedItem{ID: r.ID, Conversation: r.Conversation, At: r.At, Kind: r.Kind, Ref: r.Ref, Body: r.Body})
		}
	}
	return items, nil
}

// feed is feedItems and the plans they name.
func (s *Service) feed(ctx context.Context, conversation string, since time.Time) ([]FeedItem, map[string]*TaskPlan, error) {
	items, err := s.feedItems(ctx, conversation, since)
	if err != nil {
		return nil, nil, err
	}
	return items, s.feedPlans(ctx, items), nil
}

// feedPlans are the plans items' plan cards name, by id. A plan that is gone is left out, and so is every plan when
// reading them fails: the cards still show. The database reads them all in one query, not one per card.
func (s *Service) feedPlans(ctx context.Context, items []FeedItem) map[string]*TaskPlan {
	var ids []string
	seen := map[string]bool{}
	for _, it := range items {
		if it.Kind == feedPlan && !seen[it.Ref] {
			seen[it.Ref] = true
			ids = append(ids, it.Ref)
		}
	}
	plans := make(map[string]*TaskPlan, len(ids))
	st := s.store()
	if st == nil {
		for _, id := range ids {
			if p, err := s.loadPlan(ctx, id); err == nil {
				plans[id] = p
			}
		}
		return plans
	}
	if len(ids) == 0 {
		return plans
	}
	rows, err := st.TaskPlansByID(ctx, ids)
	if err != nil {
		s.logf("feed: read %d plans: %v", len(ids), err)
		return plans
	}
	for _, row := range rows {
		plans[row.ID] = planFromRow(row)
	}
	return plans
}

// planStartsAtOnce: every task only reads, was asked for by the user in this turn (while start_requested_tasks is on),
// or is a kind the user said to start without asking.
func (s *Service) planStartsAtOnce(ctx context.Context, tasks []PlanTask) bool {
	trusted := s.trustedKinds(ctx)
	requested := s.startRequestedTasks(ctx)
	for _, t := range tasks {
		if !t.Readonly && !(requested && t.UserRequested) && !trusted[trustKey(t)] {
			return false
		}
	}
	return true
}

// trustKey names a kind of task the user can let start without asking: a machine's directory, reading or editing.
func trustKey(t PlanTask) string {
	kind := "edit"
	if t.Readonly {
		kind = "read"
	}
	return t.Machine + "|" + t.Cwd + "|" + kind
}

// trustedKinds is the kinds the user lets start without asking; none when they cannot be read, so that a plan asks.
// What writes tasks.trust reads it with trustedKindsErr instead.
func (s *Service) trustedKinds(ctx context.Context) map[string]bool {
	have, err := s.trustedKindsErr(ctx)
	if err != nil {
		s.logf("tasks: trust: read: %v", err)
		return map[string]bool{}
	}
	return have
}

// trustedKindsErr is trustedKinds with the read's error: a write built on a failed read would replace every kind
// remembered before.
func (s *Service) trustedKindsErr(ctx context.Context) (map[string]bool, error) {
	out := map[string]bool{}
	st := s.store()
	if st == nil {
		return out, nil
	}
	var keys []string
	ok, err := st.Setting(ctx, trustSetting, &keys)
	if err != nil {
		return nil, err
	}
	if ok {
		for _, k := range keys {
			out[k] = true
		}
	}
	return out, nil
}

// trustKinds remembers the kinds of tasks so that similar plans start without asking. When the kinds remembered
// before cannot be read it remembers nothing: the next similar plan asks again, and no kind is lost.
func (s *Service) trustKinds(ctx context.Context, tasks []PlanTask) {
	st := s.store()
	if st == nil {
		return
	}
	s.tasks.trustMu.Lock()
	defer s.tasks.trustMu.Unlock()
	have, err := s.trustedKindsErr(ctx)
	if err != nil {
		s.logf("tasks: trust: not remembering %d tasks' kinds: read: %v", len(tasks), err)
		return
	}
	for _, t := range tasks {
		have[trustKey(t)] = true
	}
	keys := make([]string, 0, len(have))
	for k := range have {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if err := st.PutSetting(ctx, trustSetting, keys); err != nil {
		s.logf("tasks: trust: %v", err)
	}
}

// ---- HTTP ----

func (s *Service) taskRoutes() {
	s.draftDecisionRoutes()
	s.doneCheckRoute()
	s.handle("POST /api/v1/task-plans/{id}/workspace-preview", s.previewPlanWorkspaces)
	s.handle("GET /api/v1/task-plans/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.loadPlan(r.Context(), r.PathValue("id"))
		if err != nil {
			planErr(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"plan": s.planWithSuggestion(r.Context(), p, s.requestLang(r))})
	})
	s.handle("POST /api/v1/task-plans/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot start tasks"))
			return
		}
		var in struct {
			Tasks    []PlanTask `json:"tasks"`
			Remember bool       `json:"remember"`
		}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		p, err := s.startPlan(ownerStart(r.Context()), r.PathValue("id"), in.Tasks, in.Remember)
		if err != nil {
			planErr(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"plan": p})
	})
	s.handle("POST /api/v1/task-plans/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot change tasks"))
			return
		}
		p, err := s.dismissPlan(r.Context(), r.PathValue("id"))
		if err != nil {
			planErr(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"plan": p})
	})
	s.handle("POST /api/v1/todos/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot start tasks"))
			return
		}
		t, err := s.retryTask(r.Context(), r.PathValue("id"))
		switch {
		case err == nil:
			apierr.WriteJSON(w, map[string]any{"todo": t})
		case errors.Is(err, db.ErrTodoNotFound):
			apierr.Write(w, http.StatusNotFound, err)
		case errors.Is(err, errTaskNotRetryable), errors.Is(err, errTaskNotInPlan), errors.Is(err, errMachineOffline):
			apierr.Write(w, http.StatusConflict, err)
		default:
			planErr(w, err)
		}
	})
	s.handle("GET /api/v1/assistant/conversations/{id}/feed", func(w http.ResponseWriter, r *http.Request) {
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				apierr.Write(w, http.StatusBadRequest, fmt.Errorf("since: %v", err))
				return
			}
			since = t
		}
		query := r.URL.Query()
		paginated := query.Get("paginate") == "1" || query.Has("cursor")
		if query.Has("cursor") && (query.Get("cursor") == "" || query.Has("since")) {
			apierr.Write(w, http.StatusBadRequest, errFeedCursor)
			return
		}
		var items []FeedItem
		var nextCursor string
		var err error
		if paginated {
			items, nextCursor, err = s.pagedFeed(r.Context(), r.PathValue("id"), since, query.Get("cursor"))
		} else {
			items, err = s.feedItems(r.Context(), r.PathValue("id"), since)
		}
		if err != nil {
			status := http.StatusServiceUnavailable
			if errors.Is(err, errFeedCursor) {
				status = http.StatusBadRequest
			}
			apierr.Write(w, status, err)
			return
		}
		plans := s.feedPlans(r.Context(), items)
		for id, p := range plans {
			plans[id] = s.planWithSuggestion(r.Context(), p, s.requestLang(r))
		}
		if items == nil {
			items = []FeedItem{}
		}
		views, err := s.conversationViews(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		actions, err := s.conversationViewActions(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		blocks, err := s.conversationViewBlockKeys(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		cards, cardErr := s.taskCards(r.Context(), items, plans)
		if cardErr != nil {
			s.logf("feed: task card projection: %v", cardErr)
		}
		out := map[string]any{"items": items, "plans": plans, "task_cards": cards, "views": views, "view_actions": actions, "view_blocks": blocks}
		if paginated {
			out["next_cursor"] = nextCursor
		}
		apierr.WriteJSON(w, out)
	})
	s.handle("POST /api/v1/assistant/conversations/{id}/route", func(w http.ResponseWriter, r *http.Request) {
		if !fullRole(r) {
			apierr.Write(w, http.StatusForbidden, errors.New("read-only role cannot send"))
			return
		}
		var in struct {
			TodoID string `json:"todo_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Text) == "" || in.TodoID == "" {
			apierr.Write(w, http.StatusBadRequest, errors.New("todo_id and text are required"))
			return
		}
		it, err := s.routeToTask(r.Context(), r.PathValue("id"), in.TodoID, in.Text)
		if err != nil {
			apierr.Write(w, http.StatusConflict, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"item": it})
	})
}
