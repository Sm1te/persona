package assistant

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// Quiet tasks (#1731, docs/assistant/unified-entry.md "Quiet tasks"): a task the main conversation opened while the user wanted
// those told only there (Settings › Assistant, assistant.main_tasks_quiet) gets no push, Live Activity or badge from the hub.
// What would have been pushed — done, waiting for the user, cut off, did not start — is a message into the main
// conversation instead (prompts/assistant/task_notice.md), and the assistant decides what to tell the user; its
// answer is pushed like any of its answers.
//
// One notice per task at a time: the first change waits quietNoticeDelay, later ones within it replace it (the
// message says the earlier states). A main conversation that is busy gets it after its turn, through the queue
// conversation_send uses; a notice still queued there is replaced, not followed by a second one.

// quietNoticeDelay is how long a task's change waits for the next one before the main conversation is told.
var quietNoticeDelay = 15 * time.Second

// noticeTool marks a queued task notice (relayMsg.Tool); relayMsg.From is then the todo id.
const noticeTool = "task_notice"

type quietNotice struct {
	todo    Todo
	machine string
	state   string // done | needs | interrupted | failed
	earlier []string
	ask     string
	text    string
}

type quietBook struct {
	mu      sync.Mutex
	pending map[string]*quietNotice // by todo id
}

// noticeStates are the words for a state in the notice's "meanwhile it also…".
var noticeStates = map[string]string{"done": "做完过一次", "needs": "等过用户", "interrupted": "被打断过", "failed": "没开起来过"}

// quietNotify tells the main conversation about quiet task t (a no-op for any other): state, and what it asks (needs)
// or what its agent last said (text). machine is where its agent runs (a name).
func (s *Service) quietNotify(ctx context.Context, t Todo, machine, state, ask, text string) {
	if !t.Quiet || t.Origin == "" {
		return
	}
	b := &s.quiet
	b.mu.Lock()
	if b.pending == nil {
		b.pending = map[string]*quietNotice{}
	}
	n, waiting := b.pending[t.ID]
	if !waiting {
		n = &quietNotice{}
		b.pending[t.ID] = n
	} else if n.state != state {
		n.earlier = appendOnce(n.earlier, noticeStates[n.state])
	}
	n.todo, n.machine, n.state, n.ask, n.text = t, machine, state, ask, text
	b.mu.Unlock()
	if !waiting {
		time.AfterFunc(quietNoticeDelay, func() { s.sendQuietNotice(t.ID) })
	}
}

func appendOnce(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// sendQuietNotice puts task id's notice into the main conversation: now, or queued behind its turn.
func (s *Service) sendQuietNotice(id string) {
	b := &s.quiet
	b.mu.Lock()
	n := b.pending[id]
	delete(b.pending, id)
	b.mu.Unlock()
	if n == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if n.state == "needs" {
		if waits, known := s.taskNeedsRecheck(ctx, n.todo); known && !waits {
			return // answered meanwhile (on the phone, the watch, another conversation); an unreachable node keeps it
		}
	}
	// A reporting conversation's task is told there (reporters.go); the main conversation's in the main conversation
	// of now.
	machine, to, reporting := "", "", false
	if m, ok := s.reporterOf(ctx, n.todo.Origin); ok {
		if s.reporterStopped(ctx, n.todo.Origin) { // the user stopped it and what it handed out (#3308)
			s.logf("task %s: quiet notice dropped: %s was stopped", id, short(n.todo.Origin))
			return
		}
		machine, to, reporting = m, n.todo.Origin, true
	} else if here := s.settings(ctx); here.Main != "" && here.MachineID != "" {
		machine, to = here.MachineID, here.Main
	} else {
		s.logf("task %s: quiet notice: no main conversation", id)
		return
	}
	msg := relayMsg{From: id, FromTitle: "任务「" + n.todo.Title + "」", Tool: noticeTool, Text: renderNotice(n, reporting), At: time.Now().UTC(), State: n.state}
	if s.replaceQueuedNotice(to, msg) {
		s.publish(ctx)
		return
	}
	c, ok := s.session(ctx, machine, to)
	if !ok || c.Status == proto.StatusWorking || c.Starting || s.relayWaits(to, msg) {
		if !s.queueNotice(id, n, to, msg) {
			return
		}
		s.publish(ctx) // shown waiting above the composer
		if ok && c.Status != proto.StatusWorking && !c.Starting {
			s.drainRelays(machine, []proto.Session{c})
		}
		return
	}
	if err := s.deliverRelay(ctx, machine, to, msg); err != nil {
		s.logf("task %s: quiet notice: %v; queued", id, err)
		if s.queueNotice(id, n, to, msg) {
			s.publish(ctx)
		}
	}
}

// queueNotice queues msg, task id's notice n, behind conversation to's turn. When the relay queue can't be read
// nothing is queued (false): n waits again and is tried after quietNoticeDelay, as a first change is; a newer change
// of the task that came meanwhile goes out in its place and says n's state among the earlier ones.
func (s *Service) queueNotice(id string, n *quietNotice, to string, msg relayMsg) bool {
	if s.queueRelay(to, msg) > 0 {
		return true
	}
	b := &s.quiet
	b.mu.Lock()
	if b.pending == nil {
		b.pending = map[string]*quietNotice{}
	}
	cur, newer := b.pending[id]
	if newer {
		earlier := slices.Clone(n.earlier)
		if n.state != cur.state {
			earlier = appendOnce(earlier, noticeStates[n.state])
		}
		for _, e := range cur.earlier {
			earlier = appendOnce(earlier, e)
		}
		cur.earlier = earlier
	} else {
		b.pending[id] = n
	}
	b.mu.Unlock()
	if newer {
		s.logf("task %s: quiet notice: the relay queue can't be read; a newer one waits", id)
		return false
	}
	s.logf("task %s: quiet notice: the relay queue can't be read; trying again in %s", id, quietNoticeDelay)
	time.AfterFunc(quietNoticeDelay, func() { s.sendQuietNotice(id) })
	return false
}

// renderNotice is n's text; reporting: for the reporting conversation that opened the task, not the main one.
func renderNotice(n *quietNotice, reporting bool) string {
	return prompts.RenderTaskNotice(prompts.TaskNotice{Title: n.todo.Title, Machine: n.machine, State: n.state, Earlier: n.earlier,
		Ask: n.ask, Text: n.text, Reporting: reporting})
}

// replaceQueuedNotice: a notice about the same task still waits in the queue: msg takes its place (true).
func (s *Service) replaceQueuedNotice(to string, msg relayMsg) bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	q := b.st.Queue[to]
	for i := range q {
		// The first one may be going in right now (drainRelays takes it off once delivered): leave it.
		if q[i].Tool == noticeTool && q[i].From == msg.From && (i > 0 || !b.draining[to]) {
			q[i] = msg.replacing(q[i], time.Now()) // it waited since then (relay_presence.go)
			s.saveRelayLocked()
			return true
		}
	}
	return false
}

// dropQueuedNeeds forgets a task's waiting notice once its card was dealt with: the notice still in its
// quietNoticeDelay window and needs notices queued behind a busy turn go away. A notice of another state
// (done, interrupted, failed) stays, and a queue head going out right now is left alone. It reports whether a
// notice was actually dropped.
func (s *Service) dropQueuedNeeds(todoID string) bool {
	removed := false
	book := &s.quiet
	book.mu.Lock()
	if n := book.pending[todoID]; n != nil && n.state == "needs" {
		delete(book.pending, todoID)
		removed = true
	}
	book.mu.Unlock()

	r := &s.relays
	r.mu.Lock()
	defer r.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return removed
	}
	changed := false
	for to, q := range r.st.Queue {
		kept := make([]relayMsg, 0, len(q))
		for i, m := range q {
			if m.Tool == noticeTool && m.State == "needs" && m.From == todoID && !(i == 0 && r.draining[to]) {
				continue
			}
			kept = append(kept, m)
		}
		if len(kept) == len(q) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			delete(r.st.Queue, to)
		} else {
			r.st.Queue[to] = kept
		}
	}
	if changed {
		s.saveRelayLocked()
	}
	return removed || changed
}

// quietAgentTask: an agent agent_start opened from the main conversation is a quiet task while the user wants those
// told only there (quietTask), and so is one a reporting conversation opened (reporters.go): a todo is made and the
// agent is linked to it, so it goes like a task_plan task (card, snapshot, receipt, notices). Without the setting it
// stays an agent of its own (false). While assistant.agent_tasks is on (#3617, agent_tasks.go) an agent any other
// conversation starts is a quiet task too, whatever main_tasks_quiet says: supervised by that conversation when it is
// a leader, else by the main one. told is who hears of the task, as agent_start's answer says it.
func (s *Service) quietAgentTask(ctx context.Context, conversation string, m Device, cwd, prompt string, tab any, sid string) (told string, ok bool) {
	if sid == "" {
		return "", false
	}
	_, reports := s.reporterOf(ctx, conversation)
	supervisor, managed := "", false
	if !reports {
		supervisor, managed = s.agentTaskSupervisor(ctx, conversation)
	}
	if !managed {
		supervisor = ""
		if !reports && !s.isMain(ctx, conversation) {
			return "", false
		}
		if !s.quietTask(ctx, conversation) {
			return "", false
		}
	}
	if s.isMain(ctx, conversation) {
		supervisor = conversation
	}
	// The prompt cleaned up now, the model's name for it later (todo_titles.go); the tab name when nothing is left.
	tabName, _ := tab.(string)
	title, retitle := s.taskTitle(ctx, "", prompt)
	if title == "" {
		title, retitle = tabName, false
	}
	todo, err := s.newTodo(ctx, title, prompt, conversation, "", managed || s.quietTask(ctx, conversation))
	if err != nil {
		s.logf("agent_start %s: todo: %v", short(sid), err)
		return "", false
	}
	if retitle {
		s.nameTodoLater(ctx, todo.ID, title, prompt)
	}
	// The main conversation follows up what it hands out (supervise.go), as task_plan has it; a reporting
	// conversation's task is not supervised: its notices go back to that conversation, not into a supervision message for the
	// main one (#4101).
	if supervisor != "" {
		s.superviseTodo(ctx, todo.ID, supervisor, nil, 0)
	}
	s.briefFor(ctx, s.callerOrigin(ctx, conversation), todo, nil, 0) // who asked, before its agent is linked (task_brief.go)
	if err := s.linkTodoAgent(ctx, todo.ID, m.ID, sid, title, cwd, ""); err != nil {
		s.logf("agent_start %s: link %s: %v", short(sid), todo.ID, err)
	}
	if _, err := s.appendFeed(ctx, conversation, feedTask, todo.ID, nil); err != nil {
		s.logf("task %s: feed: %v", todo.ID, err)
	}
	if supervisor != "" && supervisor != conversation {
		return startToldMain, true
	}
	return startToldHere, true
}

// startToldHere / startToldMain: who hears of an agent_start task, in the tool's answer: the conversation that started it, or
// the main conversation that supervises it for a conversation that does not supervise (agent_tasks.go).
const (
	startToldHere = "you are told here when it is done or waits for the user, nothing else notifies the user"
	startToldMain = "the main conversation supervises it and is told when it is done or waits for the user, nothing else notifies the user"
)

// InteractionRaised: an agent of a quiet task asks the user something; the hub pushed nothing.
func (s *Service) InteractionRaised(ctx context.Context, r InteractionRaised) {
	ix := r.Interaction
	s.taskPlanApproval()
	t, ok := s.taskOf(ctx, ix.MachineID, ix.SessionID)
	if !ok || !t.Quiet && t.Supervision == nil {
		return
	}
	name := ix.MachineID
	if m, ok := s.device(ctx, ix.MachineID); ok {
		name = m.Name
	}
	s.taskNotice(ctx, t, name, "needs", noticeAsk(ix), "")
	if t.Quiet { // any other task's card is in the next report already (taskAsks)
		s.taskReport(t, reportNeeds, name, [2]string{ix.MachineID, ix.SessionID}, noticeAsk(ix), &ix)
	}
}

// noticeAsk is what the card asks, for the notice: the questions with their options, the plan, the tool.
func noticeAsk(ix proto.Interaction) string {
	var b strings.Builder
	switch {
	case len(ix.Questions) > 0:
		for _, q := range ix.Questions {
			fmt.Fprintf(&b, "- %s", clip(oneLine(q.Question), 300))
			if len(q.Options) > 0 {
				labels := make([]string, 0, len(q.Options))
				for _, o := range q.Options {
					labels = append(labels, o.Label)
				}
				fmt.Fprintf(&b, "（选项：%s）", clip(strings.Join(labels, " / "), 300))
			}
			b.WriteByte('\n')
		}
	case ix.Kind == proto.IxPlan:
		b.WriteString("批准它的计划：\n" + clipMarkdown(ix.Plan, 1200))
	default:
		b.WriteString(interactionQuestion(i18n.Default, ix)) // for the model: Chinese as the rest of the notice
		if ix.Reason != "" {
			b.WriteString("（" + clip(oneLine(ix.Reason), 200) + "）")
		}
	}
	return strings.TrimSpace(b.String())
}
