package assistant

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// The main conversation in charge of any agent (#5112, docs/assistant/unified-entry.md"Supervision"): stopping and redirecting one
// (agent_interrupt), taking one it did not hand out under its supervision (agent_supervise), and the agents' own way
// up to their supervisor (report_to_supervisor, one of the tools an agent gets, not the assistant; agentToolNames).

// agentToolNames are the tools of the agents (not the assistant's conversations) that are this manager's own; their
// descriptions and schemas are in prompts/mcp (docs/assistant/prompts.md). Each needs to know which agent calls it: the node
// names the session of the agent's pane for them (node.callerTools).
var agentToolNames = []string{"report_to_supervisor", "todo_decisions", "todo_decision_add", "todo_bind_thread"}

// AgentTools are the agents' own tools (mcpTools for an agent).
func (s *Service) AgentTools() []proto.Tool { return toolsNamed(agentToolNames) }

func isAgentTool(name string) bool {
	for _, n := range agentToolNames {
		if n == name {
			return true
		}
	}
	return false
}

// ---- agent_interrupt ----

// toolAgentInterrupt stops an agent's turn (session.interrupt) and, with then, sends it the new instruction right
// after: steering instead of agent_send's queueing behind the turn. An agent not working is not stopped; then still
// goes to it.
func (s *Service) toolAgentInterrupt(ctx context.Context, machine, self string, args json.RawMessage) (string, error) {
	var a struct {
		Machine   string `json:"machine"`
		SessionID string `json:"session_id"`
		Then      string `json:"then"`
	}
	_ = json.Unmarshal(args, &a)
	am, c, err := s.findAgent(ctx, a.Machine, a.SessionID)
	if err != nil {
		return "", err
	}
	if c.Assistant {
		return "", fmt.Errorf("that is an assistant conversation, not a coding agent: it is the user's to stop")
	}
	then := strings.TrimSpace(a.Then)
	if c.SourceKind != "" && (c.Capabilities == nil || !c.Capabilities.Interrupt || then != "" && !c.Capabilities.Send) {
		return "", fmt.Errorf("this agent can only be read: %s", c.ReadOnlyReason)
	}
	if stoppedAgent(c) { // read-only (#4754): nothing runs to stop, and nothing here starts it again
		return "", fmt.Errorf("%s has already stopped and is read-only: nothing to interrupt, and nothing was sent", agentLabel(c))
	}
	if err := s.exitAllowed(ctx, self, am, c, then, 0); err != nil { // refused before the turn is stopped or the user asked
		return "", err
	}
	summary := i18n.T(s.lang(ctx), "assistant-tasks.interrupt_summary", i18n.Args{"agent": agentLabel(c), "machine": am.Name})
	if then != "" {
		summary += "\n\n" + then
	}
	if ok, err := s.confirmOutbound(ctx, machine, self, "agent_interrupt", summary, args); err != nil {
		return "", err
	} else if !ok {
		return "The user declined on the phone: the agent goes on as it was.", nil
	}
	// Again after the phone: the agent may have become another conversation's task while the user was asked.
	if err := s.exitAllowed(ctx, self, am, c, then, 0); err != nil {
		return "", err
	}
	// Only a turn under way is stopped: an agent still starting has none yet (then is queued for it, as agent_send does).
	working := c.Status == proto.StatusWorking
	if working {
		if err := s.call(ctx, am.ID, "session.interrupt", map[string]any{"session_id": c.SessionID}, nil); err != nil {
			return "", fmt.Errorf("stop: %w", err)
		}
		s.logf("agent_interrupt %s on %s (from %s)", short(c.SessionID), am.ID, short(self))
	}
	if then == "" {
		if !working {
			return fmt.Sprintf("%s on %s was not working: nothing to stop.", agentLabel(c), am.Name), nil
		}
		return fmt.Sprintf("Stopped %s on %s: its turn was cut off, the session stays (agent_send to go on). What it did already is not undone.",
			agentLabel(c), am.Name), nil
	}
	// The stop takes a moment to land; session.send queues the text if the agent still reads as working, and a
	// closed agent is resumed first (as agent_send does), which can take minutes.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if err := s.call(sctx, am.ID, DeviceSessionSend, sendParams(c.SessionID, then, false), nil); err != nil {
		if working {
			return "", fmt.Errorf("stopped it, but sending the new instruction failed: %w", err)
		}
		return "", err
	}
	if t, ok := s.taskOf(ctx, am.ID, c.SessionID); ok && (t.Origin == self || t.Supervision != nil && t.Supervision.Supervisor == self) {
		// The conversation shows what it told its task, as for agent_send to a task (routed); marked when it cut in.
		if _, err := s.appendFeed(ctx, self, feedRouted, t.ID, map[string]any{"text": then, "interrupted": working, "from": routedFromAssistant}); err != nil {
			s.logf("task %s: routed feed: %v", t.ID, err)
		}
	}
	if !working {
		return fmt.Sprintf("%s on %s was not working: sent the new instruction.", agentLabel(c), am.Name), nil
	}
	return fmt.Sprintf("Stopped %s on %s and sent the new instruction. What it did already is not undone.", agentLabel(c), am.Name), nil
}

// ---- agent_supervise ----

// toolAgentSupervise takes agent machine/session under the main conversation's supervision: a task of it already
// gets the new done_when / expect_minutes; an agent of no task becomes one (a todo of this conversation with the
// agent on it, as quietAgentTask makes); another conversation's task, or the user's own todo, stays theirs.
func (s *Service) toolAgentSupervise(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		Machine       string   `json:"machine"`
		SessionID     string   `json:"session_id"`
		Title         string   `json:"title"`
		DoneWhen      []string `json:"done_when"`
		ExpectMinutes int      `json:"expect_minutes"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	if !s.isMain(ctx, self) {
		return "", fmt.Errorf("only the main conversation supervises agents: from here, talk to it with agent_send")
	}
	am, c, err := s.findAgent(ctx, a.Machine, a.SessionID)
	if err != nil {
		return "", err
	}
	if c.Assistant {
		return "", fmt.Errorf("that is an assistant conversation, not a coding agent")
	}
	todo, made, err := s.adoptAgent(ctx, self, am, c, a.Title, "", i18n.T(s.lang(ctx), "assistant-tasks.adopt_notes",
		i18n.Args{"agent": agentLabel(c), "machine": am.Name, "cwd": c.Cwd}), a.DoneWhen, a.ExpectMinutes, true)
	if err != nil {
		return "", err
	}
	if !made {
		return fmt.Sprintf("%s already works for your task %s (%q): you supervise it, done_when %s.", agentLabel(c), todo.ID, todo.Title,
			criteriaWords(nil, todo.Supervision)), nil
	}
	s.taskCard(ctx, self, todo.ID, wakeActive)
	return fmt.Sprintf("%s on %s is now your task %s (%q, its card is in the conversation): you get a supervision message (📋) when it needs you, "+
		"and close it with task_review. done_when %s. The agent was not told: if it should work to these criteria or report to you "+
		"(report_to_supervisor), tell it with agent_send.", agentLabel(c), am.Name, todo.ID, todo.Title, criteriaWords(nil, todo.Supervision)), nil
}

// adoptAgent puts agent c of machine am under conversation self's supervision (agent_supervise, agent_send with a
// task): a task of self already gets the new done_when / expect_minutes (none: kept; update false: left as it is, for
// the caller to write later), an agent of no task becomes one
// (a todo with notes and the agent linked, named by adoptedTitle from title and prompt: made true; the caller puts its
// card in the feed, taskCard); another
// conversation's task, or the user's own todo, stays theirs (an error). If the agent cannot be linked to the todo just
// made, that todo is closed (no empty task stays).
func (s *Service) adoptAgent(ctx context.Context, self string, am Device, c proto.Session, title, prompt, notes string, doneWhen []string,
	expect int, update bool) (Todo, bool, error) {
	return s.adoptAgentAs(ctx, self, am, c, title, prompt, notes, doneWhen, expect, update, false)
}

// adoptAgentAs is adoptAgent; with quiet the todo it makes is quiet whatever self's own tasks are (an agent that became
// a task by itself, agent_tasks.go).
func (s *Service) adoptAgentAs(ctx context.Context, self string, am Device, c proto.Session, title, prompt, notes string, doneWhen []string,
	expect int, update, quiet bool) (Todo, bool, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return Todo{}, false, err
	}
	// One adoption at a time: two at once would both find the agent on no task and make two.
	s.adoptMu.Lock()
	defer s.adoptMu.Unlock()
	doneWhen, expect = cleanDoneWhen(doneWhen), max(0, min(expect, 24*60))
	if t, ok := s.openTodoOfAgent(am.ID, c.SessionID); ok {
		if err := othersTask(c, t, self); err != nil {
			return Todo{}, false, err
		}
		if !update {
			return t, false, nil
		}
		// Its criteria change only through the brief's log (task_brief_scope.go): the same rules as task_scope.
		v, err := s.rescope(ctx, t, self, "agent_supervise", doneWhen, expect)
		if err != nil {
			return Todo{}, false, err
		}
		t.Supervision = &v
		s.setTaskStageKind(ctx, t.ID, "", "", taskStageKind(false, v.DoneWhen, t.PRURL), false)
		return t, false, nil
	}
	title, retitle := s.adoptedTitle(ctx, c, title, prompt)
	todo, err := s.newTodo(ctx, title, notes, self, "", quiet || s.quietTask(ctx, self))
	if err != nil {
		return Todo{}, false, err
	}
	if retitle {
		s.nameTodoLater(ctx, todo.ID, title, cmp.Or(strings.TrimSpace(prompt), title))
	}
	v, _, err := s.updateSupervision(ctx, todo.ID, func(v *Supervision) bool {
		v.Supervisor, v.DoneWhen, v.ExpectMinutes = self, doneWhen, expect
		return true
	})
	if err != nil {
		s.dropAdoption(ctx, todo.ID, err)
		return Todo{}, false, fmt.Errorf("supervise it: %w", err)
	}
	s.setTaskStageKind(ctx, todo.ID, "", "", taskStageKind(false, doneWhen, todo.PRURL), false)
	todo.Supervision = &v
	s.briefFor(ctx, s.callerOrigin(ctx, self), todo, doneWhen, expect) // before its agent is linked (task_brief.go)
	if err := s.linkTodoAgent(ctx, todo.ID, am.ID, c.SessionID, title, c.Cwd, c.Agent); err != nil {
		s.dropAdoption(ctx, todo.ID, err)
		return Todo{}, false, fmt.Errorf("link the agent: %w", err)
	}
	s.logf("adopt %s on %s → task %s", short(c.SessionID), am.ID, todo.ID)
	return todo, true, nil
}

// othersTask refuses an open todo t of agent c that is not conversation self's to supervise: the user's own todo, or
// another conversation's task; nil when it is self's.
func othersTask(c proto.Session, t Todo, self string) error {
	switch {
	case t.Origin == "":
		return fmt.Errorf("%s is on the user's own todo %q (%s): it stays theirs", agentLabel(c), t.Title, t.ID)
	case t.Origin != self && (t.Supervision == nil || t.Supervision.Supervisor != self):
		return fmt.Errorf("%s works for task %q (%s) of another conversation: it stays that conversation's", agentLabel(c), t.Title, t.ID)
	}
	return nil
}

// ---- agent_send with a task ----

// sendTask is agent_send's task: new work handed to an agent that runs already becomes a supervised task of the main
// conversation, as agent_supervise makes one, so it is tracked like a task_plan task instead of only said.
type sendTask struct {
	Title         string   `json:"title"`
	DoneWhen      []string `json:"done_when"`
	ExpectMinutes int      `json:"expect_minutes"`
}

// givenTask is agent_send's task as sent, nil for a reply (#3386): left out, null, or an object of only title,
// done_when and expect_minutes that says nothing (sendTask.none), which runtimes that fill in every field of a tool's
// schema send for a reply. Anything else stays a task, as before: another type, a field of the wrong type or a key the
// task has not (a misspelled title) must not turn a hand-off into an untracked reply.
func givenTask(raw json.RawMessage) *sendTask {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var t sendTask
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&t); err == nil && t.none() {
		return nil
	}
	return &t
}

// none: task has only blank fields (no title, no non-blank done_when, no expect_minutes above 0); any real value in one
// of them makes it a hand-off.
func (t sendTask) none() bool {
	return strings.TrimSpace(t.Title) == "" && len(cleanDoneWhen(t.DoneWhen)) == 0 && t.ExpectMinutes <= 0
}

// handedTask is the task agent_send with a task put the agent on: made now, or self's own that it worked for already
// (then task's criteria are written once the text went: a send that fails changes nothing).
type handedTask struct {
	todo Todo
	made bool
	self string
	task sendTask
	// wake: its card's (taskCard), wakeResumed when the agent was closed and the message resumes it
	wake string
}

// sendTaskAllowed refuses agent_send's task before anything is sent: only the main conversation supervises; an agent
// working for the user's own todo or another conversation's task stays theirs; one on a task of self's with another
// title would put other work under that task (it is not, so it is not lost when that task is closed); and /exit is no
// work. A plain agent_send still goes in each case.
func (s *Service) sendTaskAllowed(ctx context.Context, self string, am Device, c proto.Session, task sendTask, text string) error {
	if !s.isMain(ctx, self) {
		return fmt.Errorf("only the main conversation hands out tasks: send it without task")
	}
	if exitText(text) {
		return fmt.Errorf("%s closes the agent, it hands it no work: send it without task", strings.TrimSpace(text))
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return err
	}
	t, ok := s.openTodoOfAgent(am.ID, c.SessionID)
	if !ok {
		return nil
	}
	if err := othersTask(c, t, self); err != nil {
		return fmt.Errorf("%w; nothing was sent (send it without task to only send it)", err)
	}
	return otherWork(c, t, task)
}

// exitText: text is only the agents' own command to quit, which the node runs by closing the agent (node exitCommand).
func exitText(text string) bool {
	t := strings.TrimSpace(text)
	return t == "/exit" || t == "/quit"
}

// supervisedElsewhere is the task agent c works for when a conversation other than self supervises it: a reply to it
// goes as a side conversation, and /exit to it is refused (exitAllowed). Ownership that cannot be read is an error, so
// a reply never goes without its provenance.
func (s *Service) supervisedElsewhere(ctx context.Context, self string, am Device, c proto.Session) (Todo, bool, error) {
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return Todo{}, false, err
	}
	t, ok := s.taskOf(ctx, am.ID, c.SessionID)
	return t, ok && t.Supervision != nil && t.Supervision.Supervisor != self, nil
}

// exitAllowed refuses /exit or /quit to an agent on a task another conversation supervises (#3537): a plain
// agent_send of it, or agent_interrupt's then, before anything is stopped, sent or confirmed on the phone. The node
// closes an agent only for the bare command without attachments (node send.go), so wrapped as a side conversation it
// would be typed in as a prompt while the tool said it went; and closing the agent that conversation's task runs on is
// not this conversation's to do. With attachments the text is no command, and goes as any reply does.
func (s *Service) exitAllowed(ctx context.Context, self string, am Device, c proto.Session, text string, attachments int) error {
	if !exitText(text) || attachments > 0 {
		return nil
	}
	t, theirs, err := s.supervisedElsewhere(ctx, self, am, c)
	if err != nil || !theirs {
		return err
	}
	return exitRefused(c, t, text, attachments)
}

// exitRefused is exitAllowed's answer once c is known to work for t, a task another conversation supervises: the
// refusal for a bare /exit or /quit, nil for any other text. A tool that asked the phone first reads the task again
// after the answer and checks with it, since the agent may have become that task's while the user was asked.
func exitRefused(c proto.Session, t Todo, text string, attachments int) error {
	if !exitText(text) || attachments > 0 {
		return nil
	}
	return fmt.Errorf("%s works for task %q (%s) of another conversation: %s asks to close the agent that task runs on, which is "+
		"not this conversation's to do; nothing was sent or stopped. The user can close it in the app, or with agent_archive "+
		"and close once it is idle and they confirm on the phone", agentLabel(c), t.Title, t.ID, strings.TrimSpace(text))
}

// otherWork refuses task for agent c on self's own task t when its title is another (other work, not to be filed under
// t); an empty title is more of t.
func otherWork(c proto.Session, t Todo, task sendTask) error {
	if title := clip(oneLine(task.Title), 40); title != "" && title != clip(oneLine(t.Title), 40) {
		return fmt.Errorf("%s already works for your task %s (%q), not %q; nothing was sent. More of that task: send it with that "+
			"title, or without task. Other work: close that task first (task_review), or hand the work to another agent",
			agentLabel(c), t.ID, t.Title, title)
	}
	return nil
}

// adoptedTitle names the todo of an agent adopted as a task (#3020), as task_plan names its tasks (taskTitle): the
// assistant's title when it is a real one, else it or the instruction (prompt) cleaned now, and retitle for the model to
// name it later. The session's title or label only when neither leaves anything.
func (s *Service) adoptedTitle(ctx context.Context, c proto.Session, given, prompt string) (title string, retitle bool) {
	if strings.TrimSpace(given) != "" || strings.TrimSpace(prompt) != "" {
		if title, retitle = s.taskTitle(ctx, given, prompt); title != "" {
			return title, retitle
		}
	}
	return cutRunes(oneLine(cmp.Or(strings.TrimSpace(c.Title), agentLabel(c))), todoTitleMax(s.hello(ctx).Locale)), false
}

// handTask puts the agent under self's supervision for task before text is sent to it (adoptAgent): text is the new
// todo's notes, and its title when task has none. An agent on a task of self's stays on it, unchanged until the send.
func (s *Service) handTask(ctx context.Context, self string, am Device, c proto.Session, task sendTask, text string) (*handedTask, error) {
	// Decided under adoptMu (adoptAgent): another hand-off may have put the agent on a task since sendTaskAllowed.
	todo, made, err := s.adoptAgent(ctx, self, am, c, task.Title, text, text, task.DoneWhen, task.ExpectMinutes, false)
	if err != nil {
		return nil, fmt.Errorf("%w; nothing was sent", err)
	}
	if made {
		wake := wakeActive
		if c.Status == proto.StatusOffline { // closed: the send resumes it first
			wake = wakeResumed
		}
		return &handedTask{todo: todo, made: true, self: self, wake: wake}, nil
	}
	if err := otherWork(c, todo, task); err != nil {
		return nil, err
	}
	return &handedTask{todo: todo, self: self, task: task}, nil
}

// sent: the text went. A task made for it gets its card in the conversation; one it was on already gets task's
// criteria; either way what it was told goes in under the task (routed, as a message on the task's card does). The
// tool's answer says which; nothing without a task.
func (h *handedTask) sent(ctx context.Context, s *Service, text string) string {
	if h == nil {
		return ""
	}
	refused := ""
	if h.made {
		s.taskCard(ctx, h.self, h.todo.ID, h.wake)
	} else {
		doneWhen, expect := cleanDoneWhen(h.task.DoneWhen), max(0, min(h.task.ExpectMinutes, 24*60))
		s.releaseHold(ctx, h.todo.ID)                                            // the supervisor sent its agent on: follow-ups go again (decision.go)
		v, err := s.rescope(ctx, h.todo, h.self, "agent_send", doneWhen, expect) // through the brief's log, as task_scope
		if err != nil {
			s.logf("task %s: supervision: %v", h.todo.ID, err)
			refused = " Its done_when was NOT changed: " + err.Error()
		} else {
			h.todo.Supervision = &v
		}
	}
	if _, err := s.appendFeed(ctx, h.self, feedRouted, h.todo.ID, map[string]string{"text": text, "from": routedFromAssistant}); err != nil {
		s.logf("task %s: routed feed: %v", h.todo.ID, err)
	}
	if !h.made {
		return fmt.Sprintf(" It already works for your task %s (%q): no new task was made, done_when %s.", h.todo.ID, h.todo.Title,
			criteriaWords(nil, h.todo.Supervision)) + refused
	}
	return fmt.Sprintf(" It is now your task %s (%q, its card is in the conversation): you get a supervision message (📋) when it needs you, "+
		"and close it with task_review. done_when %s.", h.todo.ID, h.todo.Title, criteriaWords(nil, h.todo.Supervision))
}

// sendFailed: the send did not go, so a task made for it is taken back (deleted, its link with it: a closed task's
// agent would be closed after a while, tasks_close.go); err as the tool's error. When the machine only did not answer
// in time (startResultUnknown: a resume can go on after the wait), the text may still arrive, so the task stays and
// gets its card.
func (h *handedTask) sendFailed(ctx context.Context, s *Service, err error) error {
	if h == nil || !h.made {
		return err
	}
	if startResultUnknown(err) {
		s.taskCard(ctx, h.self, h.todo.ID, h.wake)
		return fmt.Errorf("%w (it may still arrive: task %s stays; check with agent_read before sending again)", err, h.todo.ID)
	}
	if rerr := s.removeTodo(context.WithoutCancel(ctx), h.todo.ID); rerr != nil {
		s.logf("agent_send: take back task %s: %v", h.todo.ID, rerr)
		s.dropAdoption(ctx, h.todo.ID, err)
		return fmt.Errorf("%w (task %s was closed again)", err, h.todo.ID)
	}
	return fmt.Errorf("%w (no task was made)", err)
}

// taskCard puts task id's card into conversation self. wake says how the task came to its Agent when it did not start
// it (#5096, the clients' hand-off badge): wakeActive for an Agent that was running already (agent_supervise,
// agent_send with a task, an Agent that became a task at its turn's end, a task moved to another conversation),
// wakeResumed for a closed one the hand-off's message resumed. "": the task started its Agent, as task_plan and
// agent_start do, and the card has no body.
func (s *Service) taskCard(ctx context.Context, self, id, wake string) {
	var body any
	if wake != "" {
		body = taskCardBody{Wake: wake}
	}
	if _, err := s.appendFeed(ctx, self, feedTask, id, body); err != nil {
		s.logf("task %s: feed: %v", id, err)
	}
}

// A task card's wake (taskCard).
const (
	wakeActive  = "active"
	wakeResumed = "resumed"
)

// taskCardBody is a task card's body: none for a task that started its Agent.
type taskCardBody struct {
	Wake string `json:"wake,omitempty"`
}

// dropAdoption closes the todo an adoption made when the agent could not be put on it (why), so no empty task stays.
func (s *Service) dropAdoption(ctx context.Context, id string, why error) {
	s.logf("adopt: task %s: %v", id, why)
	if err := s.failTodo(ctx, id, i18n.T(s.lang(ctx), "assistant-tasks.adopt_failed", i18n.Args{"reason": why.Error()})); err != nil {
		s.logf("adopt: close task %s: %v", id, err)
	}
}

// openTodoOfAgent is the open todo agent mid/sid is active on, whoever opened it (taskOf: only an assistant
// conversation's).
func (s *Service) openTodoOfAgent(mid, sid string) (Todo, bool) {
	s.todos.mu.RLock()
	defer s.todos.mu.RUnlock()
	t := s.todos.todos[s.todos.activeBySession[[2]string{mid, sid}]]
	if t == nil || t.DoneAt != nil {
		return Todo{}, false
	}
	return copyTodoLocked(t), true
}

// criteriaWords is done_when as the tool's answer says it: the new list, else the ledger's, else that there is none.
func criteriaWords(list []string, v *Supervision) string {
	if len(list) == 0 && v != nil {
		list = v.DoneWhen
	}
	if len(list) == 0 {
		return "not set (judge by what the task is for; set it with task_scope)"
	}
	return strconv.Quote(strings.Join(list, "; "))
}

// reportBody is a report feed item's body (feedReport).
type reportBody struct {
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence,omitempty"`
	Step     int      `json:"step,omitempty"`
	Steps    int      `json:"steps,omitempty"`
	// Later: a question that waits in the queue for the next briefing (later.go), not one the supervisor is woken for.
	Later bool `json:"later,omitempty"`
}

// ---- report_to_supervisor ----

// Report kinds an agent sends its supervisor.
const (
	upReportProgress = "progress"
	upReportQuestion = "question"
	upReportBlocked  = "blocked"
	upReportDone     = "done"
)

// reportLimit / reportWindow bound how often one agent reports (each report can start a turn of the main
// conversation: it is not to be flooded).
const (
	reportLimit  = 10
	reportWindow = 10 * time.Minute
)

// reportRate counts each session's recent reports.
type reportRate struct {
	mu   sync.Mutex
	seen map[[2]string][]time.Time
}

// allow records a report of mid/sid at now, false when it has reportLimit within reportWindow already.
func (r *reportRate) allow(mid, sid string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[[2]string][]time.Time{}
	}
	k := [2]string{mid, sid}
	kept := r.seen[k][:0]
	for _, at := range r.seen[k] {
		if now.Sub(at) < reportWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= reportLimit {
		r.seen[k] = kept
		return false
	}
	r.seen[k] = append(kept, now)
	for other, list := range r.seen { // sessions that reported long ago are forgotten
		if other != k && (len(list) == 0 || now.Sub(list[len(list)-1]) >= reportWindow) {
			delete(r.seen, other)
		}
	}
	return true
}

// joinReports is two reports of the same kind waiting together (cur first, then g): what both said and showed, so
// a second question does not push the first one out.
func joinReports(cur, g superviseSignal) superviseSignal {
	g.At = cur.At
	if cur.Detail != "" && cur.Detail != g.Detail {
		g.Detail = clipMiddle(cur.Detail+"\n—\n"+g.Detail, 3000)
	}
	ev := slices.Clone(cur.Evidence)
	for _, e := range g.Evidence {
		if !slices.Contains(ev, e) && len(ev) < 12 {
			ev = append(ev, e)
		}
	}
	g.Evidence = ev
	if g.Step == 0 {
		g.Step, g.Steps = cur.Step, cur.Steps
	}
	return g
}

var errNoSupervisor = errors.New("no supervisor")

// agentTool runs one of the agents' own tools for agent session on machine.
func (s *Service) agentTool(ctx context.Context, machine, session, name string, args json.RawMessage) proto.ToolResult {
	switch name {
	case "report_to_supervisor":
		res, err := s.reportToSupervisor(ctx, machine, session, args, time.Now())
		if errors.Is(err, errNoSupervisor) {
			return proto.TextResult(res, false) // not an error: there is nobody to tell, and the agent goes on
		}
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return proto.TextResult(res, false)
	case "todo_decisions", "todo_decision_add": // todo_decisions.go
		res, err := s.agentDecisionTool(ctx, machine, session, name, args, time.Now())
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return proto.TextResult(res, false)
	case "todo_bind_thread": // todo_thread.go
		res, err := s.agentThreadTool(ctx, machine, session, args)
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return proto.TextResult(res, false)
	}
	return proto.TextResult("unknown tool "+name, true)
}

// reportToSupervisor turns agent machine/session's report into a signal for the supervisor of the task it works
// for: in the next Supervision message (a question, a block or a finish soon: they are urgent). A question with urgent
// false is no signal: it goes into the queue of questions that can wait (later.go) when a briefing will list it.
func (s *Service) reportToSupervisor(ctx context.Context, machine, session string, args json.RawMessage, now time.Time) (string, error) {
	var a struct {
		Kind     string            `json:"kind"`
		Text     string            `json:"text"`
		Evidence []string          `json:"evidence"`
		Step     int               `json:"step"`
		Steps    int               `json:"steps"`
		Urgent   *bool             `json:"urgent"`
		Criteria []criterionReport `json:"criteria"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("arguments: %v", err)
	}
	switch a.Kind {
	case upReportProgress, upReportQuestion, upReportBlocked, upReportDone:
	default:
		return "", fmt.Errorf("kind must be progress, question, blocked or done")
	}
	text := clip(strings.TrimSpace(a.Text), 2000)
	if text == "" {
		return "", fmt.Errorf("text is empty")
	}
	if session == "" {
		return "Nobody receives this: the call came from outside an agent session.", errNoSupervisor
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return "", err
	}
	t, ok := s.taskOf(ctx, machine, session)
	if !ok || t.Supervision == nil || t.Supervision.Supervisor == "" {
		// Any open todo's agent has a receiver while agent tasks are on (#3617, agent_tasks.go).
		t, ok = s.superviseOpenTodoOf(ctx, machine, session)
	}
	if !ok {
		return "Nobody receives this: your work is not a task Persona supervises. Ask the user what you need the usual way.", errNoSupervisor
	}
	if s.agentSupervised(t) {
		// A leader whose task closed leaves the worker to the conversation above it: rehome it now, so this report is
		// told to that conversation instead of being lost until the next watch (task_leader.go). With nobody above, the
		// worker is cancelled and says so.
		if lead, _, _ := s.leaderAbove(t); lead.ID == "" || !s.leaderOpen(lead) {
			var cancelled bool
			if t, cancelled = s.rehomeWorker(ctx, t); cancelled {
				return "Your task leader is gone and nobody is above it, so this task was cancelled. Stop here; the user is not asked.", errNoSupervisor
			}
		}
	}
	reports, err := cleanCriterionReports(a.Criteria, t.Supervision.DoneWhen)
	if err != nil {
		return "", err
	}
	if !s.upReports.allow(machine, session, now) {
		return "", fmt.Errorf("too many reports: at most %d in %d minutes; put what is left into one report later", reportLimit, int(reportWindow.Minutes()))
	}
	if err := s.recordCriterionReports(ctx, t, machine, session, reports, now.UTC()); err != nil {
		return "", err
	}
	evidence := make([]string, 0, len(a.Evidence))
	for _, e := range a.Evidence {
		if e = clip(strings.TrimSpace(e), 300); e != "" && len(evidence) < 8 {
			evidence = append(evidence, e)
		}
	}
	steps := max(0, min(a.Steps, 1000))
	step := max(0, min(a.Step, 1000))
	if steps > 0 {
		step = min(step, steps)
	}
	// A task leader's worker has no owner briefing to wait for: its report goes to the leader at once, urgent or not.
	notUrgent := a.Urgent != nil && !*a.Urgent && !s.agentSupervised(t)
	if notUrgent && a.Kind == upReportQuestion {
		answer, queued := s.reportLater(ctx, t, machine, session, text, evidence, now)
		if queued {
			return answer, nil
		}
		prefix := answer // it goes as an urgent one: the answer says so first
		res, err := s.reportNow(ctx, t, machine, session, a.Kind, text, evidence, step, steps, now)
		return prefix + res, err
	}
	res, err := s.reportNow(ctx, t, machine, session, a.Kind, text, evidence, step, steps, now)
	if notUrgent {
		res = "urgent only applies to a question (kind question); this was sent as usual. " + res
	}
	return res, err
}

// reportLater keeps a question the agent said can wait in the queue (later.go) and shows it in the supervisor's
// conversation, marked as one that can wait: no signal, so no turn of the main conversation. queued false: it could not
// be kept (no morning or evening briefing would list it, or the queue is full), and the answer says why; the report then
// goes as an urgent one.
func (s *Service) reportLater(ctx context.Context, t Todo, machine, session, text string, evidence []string, now time.Time) (answer string, queued bool) {
	if !s.laterBriefed(ctx) {
		return "urgent:false was ignored: no morning or evening briefing is switched on to list it, so it is sent as an urgent question. ", false
	}
	_, ok, again := s.laterQueue(ctx, t.ID, machine, session, text, now)
	if !ok {
		return "urgent:false was ignored: the queue of questions that can wait could not take it, so it is sent as an urgent question. ", false
	}
	if again {
		return "Already queued: the owner sees this question in the next morning or evening briefing. Go on with other work.", true
	}
	body := reportBody{Kind: upReportQuestion, Text: text, Evidence: evidence, Later: true}
	if _, err := s.appendFeed(ctx, t.Supervision.Supervisor, feedReport, t.ID, body); err != nil {
		s.logf("task %s: report feed: %v", t.ID, err)
	}
	s.logf("task %s: %s reports a question that can wait", t.ID, short(session))
	return "Queued: the owner sees it in the next morning or evening briefing, under \"Can wait\", and answers it in your chat when they get to it. " +
		"Nobody is woken and nobody answers right away: go on with other work. If you cannot go on without the answer, ask it again without urgent:false.", true
}

// reportNow sends a report to the supervisor of task t as a signal, and shows it in its conversation.
func (s *Service) reportNow(ctx context.Context, t Todo, machine, session, kind, text string, evidence []string, step, steps int, now time.Time) (string, error) {
	s.signal(superviseSignal{Todo: t.ID, Kind: sigReport, Key: now.UTC().Format(time.RFC3339Nano), At: now, Machine: machine,
		Session: session, Report: kind, Detail: text, Evidence: evidence, Step: step, Steps: steps})
	progress := TaskProgress{Kind: kind, Step: step, Steps: steps, Text: clip(oneLine(text), 200), At: now.UTC()}
	if text != progress.Text {
		progress.Detail = text
	}
	if s.noteProgress(ctx, t.ID, progress) {
		s.goReportLine(t, progress.At, kind, text) // off the tool call: it answers right away
	}
	s.advanceTaskReply(ctx, t, machine, session, strings.Join(append([]string{text}, evidence...), "\n"))
	// What it asks, what blocks it and its finish show in the supervisor's conversation too, so the user can follow
	// the handoff; progress reports only update the card (the stream stays quiet).
	if kind != upReportProgress {
		body := reportBody{Kind: kind, Text: text, Evidence: evidence, Step: step, Steps: steps}
		if _, err := s.appendFeed(ctx, t.Supervision.Supervisor, feedReport, t.ID, body); err != nil {
			s.logf("task %s: report feed: %v", t.ID, err)
		}
	}
	s.logf("task %s: %s reports %s", t.ID, short(session), kind)
	if kind == upReportProgress {
		return "Noted for your supervisor: it reads it with its next supervision message. Go on with the work.", nil
	}
	return "Sent to your supervisor: it reads it within minutes and answers you by message. Don't wait idle for it if there is " +
		"other work in the task; stop when you cannot go on.", nil
}

// stoppedAgent: c is an agent that has stopped and is read-only (node stopped.go, #4754): its terminal engine keeps its
// conversation and terminal, and a message to it is refused (agent_stopped).
func stoppedAgent(c proto.Session) bool {
	return c.SourceKind == "" && c.Capabilities != nil && !c.Capabilities.Send &&
		(c.Status == proto.StatusOffline || c.Status == proto.StatusInterrupted)
}
