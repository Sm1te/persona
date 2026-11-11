package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// A supervised task whose agent did not start because the new tab's shell stopped on a question (shell_waiting) or
// the agent's own screen stopped before it was ready (agent_start_stalled) keeps that tab on its computer for a while
// (node/shellsettle.go, #2570). Its start is not failed yet: the task is "remedying" (PlanTask.Start), the
// supervising main conversation hears of it with the tab's screen, and it may read the tab (task_start_screen), type
// into it (task_start_keys) and start again in it (task_retry), up to startRemedyRetries times. Only the hub retries
// nothing by itself. The start fails for good, with its start_error and failed receipt, when the supervision turn
// that saw it ends with no retry going, when the tab is no longer kept, or when a retry fails in a way that keeps no
// tab. A turn that never told the user gets the hub's own notice and push (remedyTurnEnded). Every remedy step is a
// start_remedy feed item, the data the live task cards (#2365) will show; the screen itself never goes in the feed.

// startRemedyRetries is how many times the assistant may start a task again after its agent did not start.
const startRemedyRetries = 3

// StartRemedy is a task's start being remedied, or how that ended.
type StartRemedy struct {
	State   string    `json:"state"`             // remedying, started, failed
	Attempt int       `json:"attempt,omitempty"` // the assistant's retries so far
	By      string    `json:"by,omitempty"`      // who acted last: assistant, user, hub
	Machine string    `json:"machine,omitempty"` // the machine id of the kept tab
	Pane    string    `json:"pane,omitempty"`    // the kept tab, while remedying
	Until   time.Time `json:"until,omitzero"`    // until when the computer keeps it
	Code    string    `json:"code,omitempty"`    // why it did not start: shell_waiting, agent_start_stalled…
	Reason  string    `json:"reason,omitempty"`  // the same in the user's language
	At      time.Time `json:"at"`                // when it last changed
}

// StartRemedy.State values.
const (
	remedying     = "remedying"
	remedyStarted = "started"
	remedyFailed  = "failed"
)

// feedStartRemedy is a step of a start being remedied (ref: the todo; body: startRemedyBody).
const feedStartRemedy = "start_remedy"

// startRemedyBody is a start_remedy feed item: what was done (screen, keys, retry) and how it ended (started,
// gave_up), by whom, and why in the user's words. Keys and text are what was typed; the screen read is not kept.
type startRemedyBody struct {
	Action     string   `json:"action"`
	By         string   `json:"by"`
	Attempt    int      `json:"attempt,omitempty"`
	Keys       []string `json:"keys,omitempty"`
	Text       string   `json:"text,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Suggestion string   `json:"suggestion,omitempty"`
}

// remedyReasonMax bounds a reason the assistant gives, as an answered card's text is bounded.
const remedyReasonMax = 300

// keptTabCode: a start refusal whose tab the computer keeps for someone to look at or answer.
func keptTabCode(code string) bool {
	return code == proto.CodeShellWaiting || code == proto.CodeAgentStartStalled
}

// beginRemedy makes task t's failed start one being remedied instead of failing it, when it can be: a supervised
// task, a refusal that kept its tab (pane, kept_until in args) and retries left. It records why on the plan task and
// tells the supervisor (taskNotice, failed). false: the start fails as before.
func (s *Service) beginRemedy(ctx context.Context, planID string, i int, t PlanTask, err error, code string, args map[string]string, reason string) bool {
	if !keptTabCode(code) || args["pane"] == "" {
		return false
	}
	until, perr := time.Parse(time.RFC3339, args["kept_until"])
	if perr != nil || !time.Now().Before(until) {
		return false
	}
	todo, ok := s.todoNow(t.TodoID)
	if !ok || todo.Supervision == nil || todo.DoneAt != nil || s.agentSupervised(todo) { // a leader has no start remedies
		return false
	}
	attempt := 0
	if t.Start != nil {
		attempt = t.Start.Attempt
	}
	if attempt >= startRemedyRetries {
		return false
	}
	m, ok := s.machineByName(ctx, t.Machine)
	if !ok {
		return false
	}
	r := &StartRemedy{State: remedying, Attempt: attempt, By: "hub", Machine: m.ID, Pane: args["pane"], Until: until,
		Code: code, Reason: reason, At: time.Now().UTC()}
	if perr := s.patchPlanTask(ctx, planID, i, func(pt *PlanTask) {
		pt.Error = err.Error()
		pt.ErrorCode, pt.ErrorArgs = code, args
		pt.Start = r
	}); perr != nil {
		s.logf("task %s: remedy: %v", t.TodoID, perr)
		return false
	}
	s.logf("task %s: did not start (%s); its tab %s is kept until %s for the supervisor", t.TodoID, code, r.Pane, until.Format(time.RFC3339))
	s.taskNotice(ctx, todo, t.Machine, "failed", "", reason)
	return true
}

// remedyOf is the plan task of todo id whose start is being remedied, with its plan and index.
func (s *Service) remedyOf(ctx context.Context, id string) (Todo, *TaskPlan, int, error) {
	todo, ok := s.todoNow(id)
	if !ok {
		return Todo{}, nil, 0, db.ErrTodoNotFound
	}
	if todo.PlanID == "" {
		return todo, nil, 0, errTaskNotInPlan
	}
	p, err := s.loadPlan(ctx, todo.PlanID)
	if err != nil {
		return todo, nil, 0, err
	}
	for i, pt := range p.Tasks {
		if pt.TodoID == id {
			return todo, p, i, nil
		}
	}
	return todo, nil, 0, errTaskNotInPlan
}

// remedyTool is the task a remedy tool names, checked: the main conversation's own, and its start being remedied.
func (s *Service) remedyTool(ctx context.Context, self, id string) (Todo, *TaskPlan, int, *StartRemedy, error) {
	if !s.isMain(ctx, self) {
		return Todo{}, nil, 0, nil, errors.New("only the main conversation remedies a task's start")
	}
	todo, p, i, err := s.remedyOf(ctx, strings.TrimSpace(id))
	if err != nil {
		return todo, nil, 0, nil, err
	}
	r := p.Tasks[i].Start
	if r == nil || r.State != remedying {
		return todo, p, i, r, fmt.Errorf("task %s is not waiting for its start to be remedied", todo.ID)
	}
	return todo, p, i, r, nil
}

// remedyReason is the assistant's reason for a remedy step: required, at most remedyReasonMax characters.
func remedyReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", errors.New("reason is required: why, in the user's language")
	}
	if utf8.RuneCountInString(reason) > remedyReasonMax {
		return "", fmt.Errorf("reason is at most %d characters", remedyReasonMax)
	}
	return reason, nil
}

// toolTaskStartScreen is task_start_screen: the last lines of the tab task todo_id's agent did not start in.
func (s *Service) toolTaskStartScreen(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID string `json:"todo_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	todo, p, _, r, err := s.remedyTool(ctx, self, a.TodoID)
	if err != nil {
		return "", err
	}
	if !time.Now().Before(r.Until) {
		return "", errors.New("the tab is no longer kept: start again with task_retry, which opens a new one")
	}
	var scr struct {
		Text   string `json:"text"`
		Status string `json:"status"`
	}
	if err := s.call(ctx, r.Machine, "session.screen", map[string]any{"session_id": "pane:" + r.Pane, "lines": 30}, &scr); err != nil {
		return "", err
	}
	if scr.Status == proto.StatusOffline {
		return "", errors.New("the tab is gone: start again with task_retry, which opens a new one")
	}
	s.remedyStep(ctx, p.Conversation, todo.ID, startRemedyBody{Action: "screen", By: "assistant", Reason: strings.TrimSpace(a.Reason)})
	return strings.TrimRight(scr.Text, "\n"), nil
}

// toolTaskStartKeys is task_start_keys: text and keys typed into the tab task todo_id's agent did not start in, with
// why. The computer takes them only while it keeps that tab and no agent runs there.
func (s *Service) toolTaskStartKeys(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID string   `json:"todo_id"`
		Text   string   `json:"text"`
		Keys   []string `json:"keys"`
		Reason string   `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	reason, err := remedyReason(a.Reason)
	if err != nil {
		return "", err
	}
	if a.Text == "" && len(a.Keys) == 0 {
		return "", errors.New("give text, keys or both")
	}
	todo, p, _, r, err := s.remedyTool(ctx, self, a.TodoID)
	if err != nil {
		return "", err
	}
	if err := s.call(ctx, r.Machine, "start.keys", map[string]any{"pane": r.Pane, "text": a.Text, "keys": a.Keys}, nil); err != nil {
		return "", err
	}
	s.remedyStep(ctx, p.Conversation, todo.ID, startRemedyBody{Action: "keys", By: "assistant", Keys: a.Keys, Text: a.Text, Reason: reason})
	return "typed; read the tab again with task_start_screen, then task_retry to start the agent in it", nil
}

// toolTaskRetry is task_retry: task todo_id's agent started again, with why: in the tab it did not start in while
// its computer keeps it, else in a new one (a start that failed for good is reopened first). At most
// startRemedyRetries times per task. The start runs on; how it ends comes as a supervision message.
func (s *Service) toolTaskRetry(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		TodoID string `json:"todo_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	reason, err := remedyReason(a.Reason)
	if err != nil {
		return "", err
	}
	if !s.isMain(ctx, self) {
		return "", errors.New("only the main conversation retries a task's start")
	}
	todo, p, i, err := s.remedyOf(ctx, strings.TrimSpace(a.TodoID))
	if err != nil {
		return "", err
	}
	pt := p.Tasks[i]
	pt.Permission = strings.TrimSpace(pt.Permission)
	attempt := 1
	if pt.Start != nil {
		attempt = pt.Start.Attempt + 1
	}
	if attempt > startRemedyRetries {
		return "", fmt.Errorf("already started again %d times: tell the user why it does not start and what to check", startRemedyRetries)
	}
	switch {
	case pt.Start != nil && pt.Start.State == remedying:
	case todo.DoneAt != nil && todo.StartError != "": // failed for good: reopened, in a new tab
		// Marked first: the start retryTask runs reads its task as the plan has it, and tells the supervisor how it ends.
		prev := pt.Start
		if err := s.patchPlanTask(ctx, p.ID, i, func(pt *PlanTask) {
			pt.Start = &StartRemedy{State: remedying, Attempt: attempt, By: "assistant", Reason: todo.StartError, At: time.Now().UTC()}
		}); err != nil {
			return "", err
		}
		if _, err := s.retryTask(ctx, todo.ID); err != nil {
			_ = s.patchPlanTask(ctx, p.ID, i, func(pt *PlanTask) { pt.Start = prev })
			return "", err
		}
		s.remedyStep(ctx, p.Conversation, todo.ID, startRemedyBody{Action: "retry", By: "assistant", Attempt: attempt, Reason: reason})
		return fmt.Sprintf("starting again in a new tab (retry %d of %d); how it ends comes as a supervision message", attempt, startRemedyRetries), nil
	default:
		return "", fmt.Errorf("task %s did not fail to start", todo.ID)
	}
	s.tasks.mu.Lock()
	busy := s.tasks.retrying[todo.ID]
	s.tasks.retrying[todo.ID] = true
	s.tasks.mu.Unlock()
	if busy {
		return "", errors.New("it is starting again already")
	}
	inPane := ""
	if time.Now().Before(pt.Start.Until) {
		inPane = pt.Start.Pane
	}
	if err := s.patchPlanTask(ctx, p.ID, i, func(t *PlanTask) {
		t.Start.Attempt, t.Start.By, t.Start.At = attempt, "assistant", time.Now().UTC()
		t.Error, t.ErrorCode, t.ErrorArgs = "", "", nil
	}); err != nil {
		s.tasks.mu.Lock()
		delete(s.tasks.retrying, todo.ID)
		s.tasks.mu.Unlock()
		return "", err
	}
	s.remedyStep(ctx, p.Conversation, todo.ID, startRemedyBody{Action: "retry", By: "assistant", Attempt: attempt, Reason: reason})
	pt.Start.Attempt, pt.Start.By = attempt, "assistant"
	pt.inPane = inPane
	s.startPlanTaskAsync(p.ID, p.Conversation, i, pt, true, func() {
		s.tasks.mu.Lock()
		delete(s.tasks.retrying, todo.ID)
		s.tasks.mu.Unlock()
	})
	where := "a new tab"
	if inPane != "" {
		where = "the same tab"
	}
	return fmt.Sprintf("starting again in %s (retry %d of %d); how it ends comes as a supervision message", where, attempt, startRemedyRetries), nil
}

// remedyStep writes a start_remedy item into conversation's feed for todo.
func (s *Service) remedyStep(ctx context.Context, conversation, todo string, b startRemedyBody) {
	if _, err := s.appendFeed(ctx, conversation, feedStartRemedy, todo, b); err != nil {
		s.logf("task %s: remedy step: %v", todo, err)
	}
}

// remedyStarted: task t's start that was being remedied started after all.
func (s *Service) remedyStarted(ctx context.Context, planID, conversation string, i int, t PlanTask) {
	if t.Start == nil || t.Start.State != remedying {
		return
	}
	_ = s.patchPlanTask(ctx, planID, i, func(pt *PlanTask) {
		if pt.Start != nil {
			pt.Start.State, pt.Start.Pane, pt.Start.At = remedyStarted, "", time.Now().UTC()
		}
	})
	s.remedyStep(ctx, conversation, t.TodoID, startRemedyBody{Action: "started", By: cmp.Or(t.Start.By, "assistant"), Attempt: t.Start.Attempt})
	// The supervisor tells the user it started after all, and why it had not.
	s.signal(superviseSignal{Todo: t.TodoID, Machine: t.Machine, Kind: sigStarted, Key: fmt.Sprintf("retry %d", t.Start.Attempt),
		Detail: t.Start.Reason, At: time.Now()})
}

// giveUpStart fails task todo's start that was being remedied for good, as a start that could not be remedied fails:
// its start_error, the failed receipt, the bot's report. told: the user heard of it in the main conversation; else
// the hub tells them itself there and pushes it (failedStartNotice).
func (s *Service) giveUpStart(ctx context.Context, id, by string, told bool) {
	todo, p, i, err := s.remedyOf(ctx, id)
	if err != nil {
		return
	}
	pt := p.Tasks[i]
	if pt.Start == nil || pt.Start.State != remedying {
		return
	}
	s.tasks.mu.Lock()
	busy := s.tasks.retrying[id]
	s.tasks.mu.Unlock()
	if busy {
		return
	}
	reason := cmp.Or(pt.Start.Reason, pt.Error)
	if err := s.patchPlanTask(ctx, p.ID, i, func(t *PlanTask) {
		if t.Start != nil {
			t.Start.State, t.Start.By, t.Start.At = remedyFailed, by, time.Now().UTC()
		}
	}); err != nil {
		s.logf("task %s: give up its start: %v", id, err)
		return
	}
	if err := s.failTodo(ctx, id, reason); err != nil {
		s.logf("task %s: fail: %v", id, err)
	}
	s.tasks.mu.Lock()
	s.tasks.receipted[id] = time.Now()
	s.tasks.mu.Unlock()
	s.remedyStep(ctx, p.Conversation, id, startRemedyBody{Action: "gave_up", By: by, Attempt: pt.Start.Attempt, Reason: reason})
	_, _ = s.appendFeed(ctx, p.Conversation, feedReceipt, id, receiptBody{Title: pt.Title, State: "failed",
		Summary: i18n.T(s.lang(ctx), "assistant-tasks.failed_start", i18n.Args{"reason": reason})})
	if t, ok := s.todoNow(id); ok {
		todo = t
	}
	s.taskReport(todo, reportFailed, pt.Machine, [2]string{}, reason, nil)
	if !told {
		s.failedStartNotice(ctx, p.Conversation, todo, pt.Machine, reason)
	}
}

// failedStartNotice tells the user in conversation that task t did not start, and why, and pushes it: what the hub
// says itself when the supervision turn that saw the failure did not (#2570).
func (s *Service) failedStartNotice(ctx context.Context, conversation string, t Todo, machine, reason string) {
	text := i18n.T(s.lang(ctx), "assistant-tasks.start_remedy_notice", i18n.Args{"title": t.Title, "reason": reason})
	if _, err := s.appendFeed(ctx, conversation, feedNotice, "", noticeBody{Text: text}); err != nil {
		s.logf("task %s: notice: %v", t.ID, err)
	}
	s.notifyTaskFailed(ctx, t, machine, reason)
}

// remedyWatch fails for good the starts being remedied whose tab is no longer kept and that are not starting again:
// on the supervisor's tick, so a hub restarted meanwhile catches up.
func (s *Service) remedyWatch(ctx context.Context, t Todo, now time.Time) {
	if t.PlanID == "" || len(t.Agents) > 0 || t.DoneAt != nil {
		return
	}
	_, p, i, err := s.remedyOf(ctx, t.ID)
	if err != nil {
		return
	}
	if r := p.Tasks[i].Start; r != nil && r.State == remedying && !r.Until.IsZero() && now.After(r.Until) {
		s.giveUpStart(ctx, t.ID, "hub", false)
	}
}

// remedySeen marks that a supervision message about todo's failed start went to the main conversation at at.
func (s *Service) remedySeen(todo string, at time.Time) {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	b.failSeen[todo] = at
}

// remedyTurnEnded: conversation id's turn ended. A supervision turn that carried a failed start (remedySeen, by its
// hold) ends what it left: a start still being remedied and not starting again fails for good; a failure the turn
// did not tell the user about gets the hub's notice.
func (s *Service) remedyTurnEnded(ctx context.Context, id string) {
	b := &s.supervise
	b.mu.Lock()
	held, ok := b.hold[id]
	told := b.visible[id]
	var due []string
	if ok {
		for todo, at := range b.failSeen {
			if !held.Before(at) {
				due = append(due, todo)
				delete(b.failSeen, todo)
			}
		}
	}
	b.mu.Unlock()
	for _, todo := range due {
		t, ok := s.todoNow(todo)
		if !ok {
			continue
		}
		if t.DoneAt == nil {
			s.giveUpStart(ctx, todo, "assistant", told)
			continue
		}
		if t.StartError != "" && !told && !t.Cancelled() {
			s.failedStartNotice(ctx, t.Origin, t, "", t.StartError)
		}
	}
}

// remedyEntry fills a supervision message's entry for task t's failed start with its kept tab (until when, the
// question, the screen, the retries left) while it is being remedied, and marks the failure as told to the
// supervisor (remedySeen).
func (s *Service) remedyEntry(ctx context.Context, t Todo, e *prompts.SuperviseEntry) {
	s.remedySeen(t.ID, time.Now())
	_, p, i, err := s.remedyOf(ctx, t.ID)
	if err != nil {
		return
	}
	pt := p.Tasks[i]
	r := pt.Start
	if r == nil || r.State != remedying || !time.Now().Before(r.Until) {
		return
	}
	e.KeptUntil = r.Until.In(s.location(ctx)).Format("15:04")
	e.Question, e.Screen = pt.ErrorArgs["line"], strings.TrimRight(pt.ErrorArgs["screen"], "\n")
	e.RetriesLeft = startRemedyRetries - r.Attempt
	if r.Code == proto.CodeShellWaiting {
		e.Detail = "" // the question says it, with the screen
	}
}
