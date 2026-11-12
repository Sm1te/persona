package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// secretary is an agent the main conversation did not hand out (a long-lived one the user opened), working on no todo.
func secretary(f *fakeHub) {
	f.setSessions("box", proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "秘书",
		Status: proto.StatusIdle, State: proto.StateIdle, UpdatedAt: time.Now()})
}

// agent_send with task hands new work to an agent already running: the message goes as written, and the agent becomes
// a supervised task of the main conversation (a todo with the instruction as its notes, the agent linked, done_when
// kept, the task card and what it was told in the feed).
func TestAgentSendTaskMakesATask(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	res, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据", "done_when": []string{"表格链接贴回来"}, "expect_minutes": 40}})
	if err != nil {
		t.Fatal(err)
	}
	if sent := sentToAgent(f); len(sent) != 1 || sent[0] != "把周报数据拉一下" {
		t.Fatalf("sent %q", sent)
	}
	todo := onlyTodo(s)
	if !strings.Contains(res, todo.ID) || todo.Title != "拉周报数据" || todo.Origin != "conv1" || todo.Notes != "把周报数据拉一下" ||
		len(todo.Agents) != 1 || todo.Agents[0].SessionID != "sec00001" {
		t.Fatalf("task: %s\n%+v", res, todo)
	}
	if v := todo.Supervision; v == nil || v.Supervisor != "conv1" || len(v.DoneWhen) != 1 || v.DoneWhen[0] != "表格链接贴回来" || v.ExpectMinutes != 40 {
		t.Fatalf("supervision: %+v", todo.Supervision)
	}
	if got := feedKinds(s, "conv1"); got != "task,routed" {
		t.Fatalf("feed: %s", got)
	}
	// the agent was running before the task: its card says so (#5096, the clients' Already active)
	if got := taskCardWake(s, "conv1", todo.ID); got != wakeActive {
		t.Fatalf("card wake %q", got)
	}
}

// A closed agent the hand-off's message resumes: its card says Resumed (#5096), not Already active.
func TestAgentSendTaskToAClosedAgentResumesIt(t *testing.T) {
	s, f := newQuietService(t)
	f.setSessions("box", proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "秘书",
		Status: proto.StatusOffline, State: proto.StateOffline, UpdatedAt: time.Now()})
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据"}}); err != nil {
		t.Fatal(err)
	}
	if got := taskCardWake(s, "conv1", onlyTodo(s).ID); got != wakeResumed {
		t.Fatalf("card wake %q", got)
	}
}

// taskCardWake is the wake on task todoID's card in conversation conv ("" without one, or without a body).
func taskCardWake(s *Service, conv, todoID string) string {
	items, _, _ := s.feed(context.Background(), conv, time.Time{})
	for _, it := range items {
		if it.Kind != feedTask || it.Ref != todoID {
			continue
		}
		var body taskCardBody
		if len(it.Body) > 0 && json.Unmarshal(it.Body, &body) != nil {
			return "unreadable"
		}
		return body.Wake
	}
	return ""
}

// A task handed without a title is named as task_plan's are (#3020): the instruction cleaned now, not its first 40
// characters, and the model's name for it once it answers; the instruction stays the notes, as written.
func TestAgentSendTaskWithoutATitleIsNamed(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	f.mu.Lock()
	f.todoTitleReply = "整理周报数据"
	f.mu.Unlock()
	text := "ultrathink 主人 11-05 19:47 的反馈，已授权直接开工：把周报数据拉一下，按部门汇总，再把表格链接贴回来给我看一眼" // i18n-ignore
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": text,
		"task": map[string]any{"done_when": []string{"表格链接贴回来"}}}); err != nil { // i18n-ignore
		t.Fatal(err)
	}
	todo := onlyTodo(s)
	if strings.HasPrefix(todo.Title, "ultrathink") || strings.Contains(todo.Title, "11-05") || todo.Notes != text {
		t.Fatalf("made with %q (notes %q)", todo.Title, todo.Notes)
	}
	s.todos.titling.Wait()
	if got := onlyTodo(s).Title; got != "整理周报数据" { // i18n-ignore
		t.Fatalf("not named by the model: %q", got)
	}
	f.mu.Lock()
	asked := f.todoTitleAsked
	f.mu.Unlock()
	if len(asked) != 1 || !strings.Contains(asked[0].User, "按部门汇总") { // i18n-ignore
		t.Fatalf("asked %+v", asked)
	}
}

// agent_supervise names its task the same way: a real title stays, one that reads as a prompt is cleaned (and named
// by the model later), and without one the session's title stays (TestAgentSuperviseAndReport).
func TestAgentSuperviseTitle(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	if _, err := runJSON(t, s, "conv1", "agent_supervise", map[string]any{"machine": "box", "session_id": "sec00001",
		"title": "ultrathink 已授权直接开工：拉周报数据"}); err != nil { // i18n-ignore
		t.Fatal(err)
	}
	if got := onlyTodo(s).Title; got != "拉周报数据" { // i18n-ignore
		t.Fatalf("title %q", got)
	}
	s.todos.titling.Wait()
}

// The same agent handed more of its task: no second todo, the new criteria replace the old once the text went (a send
// that fails changes nothing). Other work (another title) is refused, so it is not filed under that task.
func TestAgentSendTaskAgainKeepsOneTask(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	send := func(text string, doneWhen ...string) string {
		t.Helper()
		res, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": text,
			"task": map[string]any{"title": "拉周报数据", "done_when": doneWhen}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	send("把周报数据拉一下", "表格链接贴回来")
	id := onlyTodo(s).ID
	f.failSend = errors.New("dialog_open")
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "再加一列",
		"task": map[string]any{"title": "拉周报数据", "done_when": []string{"别的标准"}}}); err == nil {
		t.Fatal("the failed send succeeded")
	}
	if v := onlyTodo(s).Supervision; len(v.DoneWhen) != 1 || v.DoneWhen[0] != "表格链接贴回来" {
		t.Fatalf("a failed send changed the criteria: %+v", v)
	}
	f.failSend = nil
	if res := send("再加上上周对比", "表格链接贴回来", "有上周对比列"); !strings.Contains(res, "already works for your task "+id) {
		t.Fatalf("again: %s", res)
	}
	todo := onlyTodo(s)
	if todo.ID != id || len(todo.Supervision.DoneWhen) != 2 {
		t.Fatalf("todos: %+v", todo)
	}
	if sent := sentToAgent(f); len(sent) != 2 {
		t.Fatalf("sent %q", sent)
	}
	if got := feedKinds(s, "conv1"); got != "task,routed,routed" {
		t.Fatalf("feed: %s", got)
	}
	_, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "顺便订个会议室",
		"task": map[string]any{"title": "订会议室"}})
	if err == nil || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("other work: %v", err)
	}
	if n := len(sentToAgent(f)); n != 2 || len(s.todosOf(t.Context(), "*")) != 1 {
		t.Fatalf("other work: sent %d, %d todos", n, len(s.todosOf(t.Context(), "*")))
	}
}

// An agent working for the user's own todo or another conversation's task stays theirs, and only the main conversation
// hands out tasks: refused with why, nothing sent, no todo made.
func TestAgentSendTaskRefusesWhatIsNotItsOwn(t *testing.T) {
	s, f := newQuietService(t)
	ctx := t.Context()
	secretary(f)
	args := map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下", "task": map[string]any{"title": "拉周报数据"}}
	if _, err := runJSON(t, s, "conv2", "agent_send", args); err == nil || !strings.Contains(err.Error(), "only the main conversation") {
		t.Fatalf("from another conversation: %v", err)
	}
	for _, origin := range []string{"", "conv2"} { // the user's own todo, another conversation's task
		theirs, err := s.createTodo(ctx, "别人的", "x", origin, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.linkTodoAgent(ctx, theirs.ID, "box", "sec00001", "秘书", "/code/desk", "claude"); err != nil {
			t.Fatal(err)
		}
		before := len(s.todosOf(ctx, "*"))
		_, err = runJSON(t, s, "conv1", "agent_send", args)
		if err == nil || !strings.Contains(err.Error(), theirs.ID) || !strings.Contains(err.Error(), "nothing was sent") {
			t.Fatalf("origin %q: %v", origin, err)
		}
		if n := len(s.todosOf(ctx, "*")); n != before {
			t.Fatalf("origin %q: %d todos, %d before", origin, n, before)
		}
		if err := s.failTodo(ctx, theirs.ID, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if sent := sentToAgent(f); len(sent) != 0 {
		t.Fatalf("sent %q", sent)
	}
}

// A send that fails makes no task: the todo made for it is taken back (no closed task keeps the agent, whose agent
// would be closed with it), and the conversation shows nothing. When the machine only did not answer in time (a resume
// can go on after the wait), the text may still arrive: the task stays, with its card.
func TestAgentSendTaskFailedSend(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	args := map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下", "task": map[string]any{"title": "拉周报数据"}}
	f.failSend = errors.New("dialog_open")
	if _, err := runJSON(t, s, "conv1", "agent_send", args); err == nil || !strings.Contains(err.Error(), "dialog_open") {
		t.Fatalf("err %v", err)
	}
	if n := len(s.todosOf(t.Context(), "*")); n != 0 {
		t.Fatalf("%d todos", n)
	}
	if _, ok := s.openTodoOfAgent("box", "sec00001"); ok || feedKinds(s, "conv1") != "" {
		t.Fatalf("left behind: feed %q", feedKinds(s, "conv1"))
	}

	f.setSessions("box", proto.Session{SessionID: "sec00001", MachineID: "box", Cwd: "/code/desk", Agent: "claude", Title: "秘书",
		Status: proto.StatusOffline, UpdatedAt: time.Now()})
	f.failSend = context.DeadlineExceeded
	if _, err := runJSON(t, s, "conv1", "agent_send", args); err == nil || !strings.Contains(err.Error(), "may still arrive") {
		t.Fatalf("unknown: %v", err)
	}
	if todo := onlyTodo(s); todo.DoneAt != nil || len(todo.Agents) != 1 || feedKinds(s, "conv1") != "task" {
		t.Fatalf("unknown: %+v, feed %q", todo, feedKinds(s, "conv1"))
	}
}

// /exit closes the agent: it hands it no work, so it makes no task.
func TestAgentSendTaskExitIsNoWork(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "/exit",
		"task": map[string]any{"title": "关掉"}}); err == nil {
		t.Fatal("/exit made a task")
	}
	if len(sentToAgent(f)) != 0 || len(s.todosOf(t.Context(), "*")) != 0 {
		t.Fatal("something went")
	}
}

// Passed on: an agent the handed one starts for the work (herdr persona_from) joins the same task, so its status and
// its agent's end come back to the main conversation's task.
func TestAgentSendTaskFollowsTheAgentItStarts(t *testing.T) {
	s, f := newQuietService(t)
	secretary(f)
	if _, err := runJSON(t, s, "conv1", "agent_send", map[string]any{"machine": "box", "session_id": "sec00001", "text": "把周报数据拉一下",
		"task": map[string]any{"title": "拉周报数据"}}); err != nil {
		t.Fatal(err)
	}
	id := onlyTodo(s).ID
	s.autoLinkTodos("dev", []proto.Session{{SessionID: "worker01", MachineID: "dev", Status: proto.StatusWorking,
		SpawnFrom: id + "@box/sec00001"}})
	todo := onlyTodo(s)
	if len(todo.Agents) != 2 || todo.Agents[1].SessionID != "worker01" || todo.Agents[1].Source != "spawned" {
		t.Fatalf("agents: %+v", todo.Agents)
	}
}

// replyTasks are the ways a reply's task comes: left out, null, and the zero values a runtime that fills in every field
// of a tool's schema sends (blank title, blank or no done_when, no expect_minutes, each of them null).
var replyTasks = []string{``, `null`, `{}`, `{"title":""}`, `{"title":"","done_when":[],"expect_minutes":0}`,
	`{"title":"  ","done_when":[],"expect_minutes":0}`, `{"title":"","done_when":[""," "],"expect_minutes":-1}`,
	`{"title":null,"done_when":null,"expect_minutes":null}`}

// handOffTasks stay a hand-off: a real value in title, done_when or expect_minutes, and anything that is not an object
// of only those keys (another type, a field of the wrong type, a key the task has not, such as a misspelled title), so
// that a malformed task is never sent as an untracked reply.
var handOffTasks = []string{`{"title":"Weekly numbers"}`, `{"done_when":["the sheet link is posted"]}`, `{"expect_minutes":30}`,
	`"Weekly numbers"`, `""`, `[]`, `true`, `0`, `{"title":123}`, `{"done_when":"a PR"}`, `{"expect_minutes":"30"}`,
	`{"titel":"Weekly numbers"}`, `{"title":"","done_when":[],"expect_minutes":0,"note":""}`}

// sendArgs are agent_send's arguments to agent sid of box, with task as raw JSON ("": left out).
func sendArgs(sid, text, task string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"machine": "box", "session_id": sid, "text": text})
	if task == "" {
		return b
	}
	return json.RawMessage(strings.TrimSuffix(string(b), "}") + `,"task":` + task + `}`)
}

// boxAgent is an idle agent sid on box, its title its id.
func boxAgent(sid string) proto.Session {
	return proto.Session{SessionID: sid, MachineID: "box", Cwd: "/code/" + sid, Agent: "claude", Title: sid, Status: proto.StatusIdle,
		State: proto.StateIdle, UpdatedAt: time.Now()}
}

// onBoxTask puts agent sid of box on a new todo of origin ("": the user's own), supervised by origin when doneWhen is
// given, closed when closed.
func onBoxTask(t *testing.T, s *Service, sid, origin string, closed bool, doneWhen ...string) Todo {
	t.Helper()
	ctx := t.Context()
	todo, err := s.createTodo(ctx, "Task of "+sid, "notes of "+sid, origin, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(doneWhen) > 0 {
		s.superviseTodo(ctx, todo.ID, origin, doneWhen, 30)
	}
	if err := s.linkTodoAgent(ctx, todo.ID, "box", sid, todo.Title, "/code/"+sid, "claude"); err != nil {
		t.Fatal(err)
	}
	if closed {
		if err := s.failTodo(ctx, todo.ID, "test"); err != nil {
			t.Fatal(err)
		}
	}
	return todo
}

// taskLedger is every todo as stored (the user's own included: todosOf leaves them out) and every item of these
// conversations' feeds, for a before and after that a send must leave alone.
func taskLedger(t *testing.T, s *Service, convs ...string) string {
	t.Helper()
	s.todos.mu.RLock()
	todos := make([]Todo, 0, len(s.todos.todos))
	for _, td := range s.todos.todos {
		todos = append(todos, copyTodoLocked(td))
	}
	s.todos.mu.RUnlock()
	slices.SortFunc(todos, func(a, b Todo) int { return strings.Compare(a.ID, b.ID) })
	feeds := map[string][]FeedItem{}
	for _, c := range convs {
		items, _, err := s.feed(t.Context(), c, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		feeds[c] = items
	}
	b, err := json.Marshal(map[string]any{"todos": todos, "feeds": feeds})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The schema the assistant's conversations get asks for no task (#3386): task is not required, may be null, requires
// none of its fields and has no others than the ones the handler reads (sendTask, givenTask).
func TestAgentSendSchemaLeavesTaskOptional(t *testing.T) {
	s, _ := newQuietService(t)
	i := slices.IndexFunc(s.Tools(t.Context()), func(tool proto.Tool) bool { return tool.Name == "agent_send" })
	if i < 0 {
		t.Fatal("no agent_send")
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties struct {
			Task struct {
				Type                 []string                   `json:"type"`
				Required             []string                   `json:"required"`
				AdditionalProperties *bool                      `json:"additionalProperties"`
				Properties           map[string]json.RawMessage `json:"properties"`
			} `json:"task"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(s.Tools(t.Context())[i].InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	task := schema.Properties.Task
	if !slices.Equal(schema.Required, []string{"machine", "session_id", "text"}) || !slices.Equal(task.Type, []string{"object", "null"}) ||
		len(task.Required) != 0 || task.AdditionalProperties == nil || *task.AdditionalProperties {
		t.Fatalf("schema: required %q, task %+v", schema.Required, task)
	}
	var fields []string
	for f := range reflect.TypeFor[sendTask]().Fields() {
		fields = append(fields, f.Tag.Get("json"))
	}
	if keys := slices.Sorted(maps.Keys(task.Properties)); !slices.Equal(keys, slices.Sorted(slices.Values(fields))) {
		t.Fatalf("task properties %q, sendTask %q", keys, fields)
	}
}

// A task that says nothing is no task: a reply goes as a plain agent_send from any conversation, with a plain receipt.
// No todo is made and none changes (owner, supervisor, criteria, title, notes, links), no feed gets a line, an agent on
// another conversation's supervised task (the reported case: an older conversation's) hears it as a side conversation
// and that task stays theirs, an agent that is not running is resumed, and a read-only agent is still refused.
func TestAgentSendEmptyTaskIsAReply(t *testing.T) {
	for _, task := range replyTasks {
		t.Run(cmp.Or(task, "omitted"), func(t *testing.T) {
			s, f := newQuietService(t)
			ctx := t.Context()
			ro := boxAgent("ro000001")
			ro.SourceKind, ro.Capabilities, ro.ReadOnlyReason = "codex_history", &proto.SessionCapabilities{Read: true}, "read only"
			offline := boxAgent("peeroff1")
			offline.Status, offline.State = proto.StatusOffline, ""
			f.setSessions("box", boxAgent("free0001"), boxAgent("peer0001"), boxAgent("user0001"), boxAgent("mine0001"),
				boxAgent("manual01"), boxAgent("done0001"), offline, ro)
			onBoxTask(t, s, "peer0001", "conv2", false, "peer criteria") // another (older) conversation's supervised task
			onBoxTask(t, s, "peeroff1", "conv2", false, "peer criteria") // the same, its agent not running
			onBoxTask(t, s, "user0001", "", false)                       // the user's own todo
			onBoxTask(t, s, "mine0001", "conv1", false, "my criteria")   // the main conversation's own task
			onBoxTask(t, s, "manual01", "conv2", false)                  // another conversation's task, not supervised
			onBoxTask(t, s, "done0001", "conv2", true, "done criteria")  // another conversation's closed task
			before := taskLedger(t, s, "conv1", "conv2")

			side := prompts.RenderSideConversation(prompts.SideConversation{From: "conv1", Text: "A reply"})
			for _, c := range []struct{ self, sid, receipt, sent string }{
				{"conv1", "free0001", "Sent to free0001 on box.", "A reply"},
				{"conv1", "peer0001", "Sent to peer0001 on box.", side},
				{"conv1", "peeroff1", "Resumed peeroff1 on box (same session, directory and launcher) and sent.", side},
				{"conv1", "user0001", "Sent to user0001 on box.", "A reply"},
				{"conv1", "mine0001", "Sent to mine0001 on box.", "A reply"},
				{"conv1", "manual01", "Sent to manual01 on box.", "A reply"},
				{"conv1", "done0001", "Sent to done0001 on box.", "A reply"},
				{"conv2", "free0001", "Sent to free0001 on box.", "A reply"}, // a reply from a conversation that is not the main one
			} {
				n := len(sentToAgent(f))
				res, err := s.runTool(ctx, "server", c.self, "agent_send", sendArgs(c.sid, "A reply", task))
				if err != nil || res != c.receipt {
					t.Fatalf("%s to %s: %q %v", c.self, c.sid, res, err)
				}
				if sent := sentToAgent(f); len(sent) != n+1 || sent[n] != c.sent {
					t.Fatalf("%s to %s: sent %q", c.self, c.sid, sent[n:])
				}
			}
			n := len(sentToAgent(f))
			if _, err := s.runTool(ctx, "server", "conv1", "agent_send", sendArgs("ro000001", "A reply", task)); err == nil ||
				err.Error() != "这个 Agent 仅支持查看：read only" || len(sentToAgent(f)) != n {
				t.Fatalf("read-only agent: %v", err)
			}
			if after := taskLedger(t, s, "conv1", "conv2"); after != before {
				t.Fatalf("a reply changed the tasks:\n%s\nwas\n%s", after, before)
			}
		})
	}
}

// Any real value in title, done_when or expect_minutes makes it a hand-off, and so does anything that is not an object
// of only those keys, with a hand-off's refusals (each before anything is sent, the tasks unchanged): only the main
// conversation hands out tasks, the user's own todo stays theirs and another conversation's task stays that
// conversation's. From the main conversation to an agent on no task it is a hand-off: a supervised task with its
// receipt.
func TestAgentSendTaskWithAnyFieldIsAHandOff(t *testing.T) {
	s, f := newQuietService(t)
	ctx := t.Context()
	sessions := []proto.Session{boxAgent("peer0001"), boxAgent("user0001")}
	for i := range handOffTasks {
		sessions = append(sessions, boxAgent(fmt.Sprintf("free%04d", i)))
	}
	f.setSessions("box", sessions...)
	theirs := onBoxTask(t, s, "peer0001", "conv2", false, "peer criteria")
	own := onBoxTask(t, s, "user0001", "", false)
	before := taskLedger(t, s, "conv1", "conv2")
	for i, task := range handOffTasks {
		free := fmt.Sprintf("free%04d", i)
		for _, c := range []struct{ self, sid, err string }{
			{"conv2", free, "only the main conversation hands out tasks: send it without task"},
			{"conv1", "peer0001", fmt.Sprintf("peer0001 works for task %q (%s) of another conversation: it stays that conversation's; "+
				"nothing was sent (send it without task to only send it)", theirs.Title, theirs.ID)},
			{"conv1", "user0001", fmt.Sprintf("user0001 is on the user's own todo %q (%s): it stays theirs; nothing was sent "+
				"(send it without task to only send it)", own.Title, own.ID)},
		} {
			if res, err := s.runTool(ctx, "server", c.self, "agent_send", sendArgs(c.sid, "Pull the weekly numbers", task)); err == nil ||
				err.Error() != c.err {
				t.Fatalf("%s from %s to %s: %q %v", task, c.self, c.sid, res, err)
			}
		}
		if sent := sentToAgent(f); len(sent) != i {
			t.Fatalf("%s: a refused hand-off was sent: %q", task, sent)
		}
		if after := taskLedger(t, s, "conv1", "conv2"); after != before {
			t.Fatalf("%s: a refused hand-off changed the tasks:\n%s\nwas\n%s", task, after, before)
		}

		res, err := s.runTool(ctx, "server", "conv1", "agent_send", sendArgs(free, "Pull the weekly numbers", task))
		s.todos.titling.Wait()
		todo, ok := s.openTodoOfAgent("box", free)
		if err != nil || !ok || todo.Origin != "conv1" || todo.Supervision == nil || todo.Supervision.Supervisor != "conv1" {
			t.Fatalf("%s: %q %v, %+v", task, res, err, todo)
		}
		if want := fmt.Sprintf("Sent to %s on box. It is now your task %s (%q, its card is in the conversation): you get a "+
			"supervision message (📋) when it needs you, and close it with task_review. done_when %s.", free, todo.ID, todo.Title,
			criteriaWords(nil, todo.Supervision)); res != want {
			t.Fatalf("%s: receipt %q, want %q", task, res, want)
		}
		if sent := sentToAgent(f); len(sent) != i+1 || sent[i] != "Pull the weekly numbers" {
			t.Fatalf("%s: sent %q", task, sent)
		}
		before = taskLedger(t, s, "conv1", "conv2")
	}
	if v := onlyOpen(t, s, "free0002").Supervision; v.ExpectMinutes != 30 {
		t.Fatalf("expect_minutes: %+v", v)
	}
	if v := onlyOpen(t, s, "free0001").Supervision; !slices.Equal(v.DoneWhen, []string{"the sheet link is posted"}) {
		t.Fatalf("done_when: %+v", v)
	}
	if got := onlyOpen(t, s, "free0000").Title; got != "Weekly numbers" {
		t.Fatalf("title %q", got)
	}
}

// onlyOpen is the open todo agent sid of box is on.
func onlyOpen(t *testing.T, s *Service, sid string) Todo {
	t.Helper()
	todo, ok := s.openTodoOfAgent("box", sid)
	if !ok || todo.Supervision == nil {
		t.Fatalf("%s: no supervised task: %+v", sid, todo)
	}
	return todo
}

// A reply from the health space still asks on the phone before it goes (an empty task is no reason to refuse it):
// declined, nothing is sent and the receipt says so; allowed, it goes, and neither makes a task or a feed line.
func TestAgentSendEmptyTaskAsksBeforeItGoes(t *testing.T) {
	f := healthHub()
	f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: "code-0000-0000", Cwd: "/home/me/src/app", Agent: "claude",
		Status: proto.StatusIdle, Title: "app"})
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "main-0000-0000" })
	args := `{"session_id":"code-0000-0000","text":"Chart my sleep","task":{"title":"","done_when":[],"expect_minutes":0}}`
	answerOutbound(t, s, "sleep-000-0000", "agent_send", "Chart my sleep", false)
	if out, isErr := spaceTool(t, s, "sleep-000-0000", "agent_send", args); isErr ||
		!strings.Contains(out, "The user declined on the phone: nothing was sent to that agent.") || len(sentToAgent(f)) != 0 {
		t.Fatalf("declined: %s, sent %q", out, sentToAgent(f))
	}
	answerOutbound(t, s, "sleep-000-0000", "agent_send", "Chart my sleep", true)
	if out, isErr := spaceTool(t, s, "sleep-000-0000", "agent_send", args); isErr || !strings.Contains(out, "Sent to app on ") ||
		len(sentToAgent(f)) != 1 || sentToAgent(f)[0] != "Chart my sleep" {
		t.Fatalf("allowed: %s, sent %q", out, sentToAgent(f))
	}
	if n, feed := len(s.todosOf(context.Background(), "*")), feedKinds(s, "sleep-000-0000")+feedKinds(s, "main-0000-0000"); n != 0 || feed != "" {
		t.Fatalf("%d todos, feed %q", n, feed)
	}
}

// /exit or /quit sent as a reply to an agent on a task another conversation supervises is refused before anything is
// sent (#3537), with the task left out, null or empty: wrapped as a side conversation it would only be typed in while
// the tool said it went, and the agent that task runs on is not this conversation's to close. To an agent on no task,
// on the conversation's own task, on the user's own todo or on another conversation's unsupervised task it still goes
// bare (the node closes it), and the supervising conversation still closes its own.
func TestAgentSendExitToAnotherConversationsAgentIsRefused(t *testing.T) {
	for _, task := range []string{``, `null`, `{"title":"","done_when":[],"expect_minutes":0}`} {
		t.Run(cmp.Or(task, "omitted"), func(t *testing.T) {
			s, f := newQuietService(t)
			ctx := t.Context()
			offline := boxAgent("peeroff1")
			offline.Status, offline.State = proto.StatusOffline, ""
			f.setSessions("box", boxAgent("free0001"), boxAgent("peer0001"), boxAgent("user0001"), boxAgent("mine0001"),
				boxAgent("manual01"), offline)
			peer := onBoxTask(t, s, "peer0001", "conv2", false, "peer criteria")
			peerOff := onBoxTask(t, s, "peeroff1", "conv2", false, "peer criteria")
			onBoxTask(t, s, "user0001", "", false)
			onBoxTask(t, s, "mine0001", "conv1", false, "my criteria")
			onBoxTask(t, s, "manual01", "conv2", false)
			before := taskLedger(t, s, "conv1", "conv2")

			for _, c := range []struct {
				sid, text string
				todo      Todo
			}{{"peer0001", "/exit", peer}, {"peer0001", " /quit\n", peer}, {"peeroff1", "/exit", peerOff}} {
				if res, err := s.runTool(ctx, "server", "conv1", "agent_send", sendArgs(c.sid, c.text, task)); err == nil ||
					err.Error() != exitRefusal(c.sid, c.todo, c.text) {
					t.Fatalf("%q to %s: %q %v", c.text, c.sid, res, err)
				}
			}
			if task == "" { // agent_interrupt's then is refused the same way, before the turn is stopped
				args := json.RawMessage(`{"machine":"box","session_id":"peer0001","then":"/exit"}`)
				if res, err := s.runTool(ctx, "server", "conv1", "agent_interrupt", args); err == nil || err.Error() != exitRefusal("peer0001", peer, "/exit") {
					t.Fatalf("agent_interrupt then /exit: %q %v", res, err)
				}
				if got := f.interrupted(); len(got) != 0 {
					t.Fatalf("interrupted %v", got)
				}
			}
			if sent := sentToAgent(f); len(sent) != 0 {
				t.Fatalf("a refused /exit was sent: %q", sent)
			}
			for _, sid := range []string{"free0001", "user0001", "mine0001", "manual01"} {
				if res, err := s.runTool(ctx, "server", "conv1", "agent_send", sendArgs(sid, "/exit", task)); err != nil || res != "Sent to "+sid+" on box." {
					t.Fatalf("/exit to %s: %q %v", sid, res, err)
				}
			}
			if res, err := s.runTool(ctx, "server", "conv2", "agent_send", sendArgs("peer0001", "/exit", task)); err != nil ||
				res != "Sent to peer0001 on box." {
				t.Fatalf("/exit from the supervisor: %q %v", res, err)
			}
			if sent := sentToAgent(f); !slices.Equal(sent, []string{"/exit", "/exit", "/exit", "/exit", "/exit"}) {
				t.Fatalf("sent %q", sent)
			}
			// With an attachment the text is no command (the node closes nothing): it goes as any reply does.
			newFakeFiles(f, map[string][]byte{"/x/conv1/images/1.png": []byte("png-bytes")})
			args := json.RawMessage(strings.TrimSuffix(string(sendArgs("peer0001", "/exit", task)), "}") + `,"attachments":["/x/conv1/images/1.png"]}`)
			if res, err := s.runTool(ctx, "server", "conv1", "agent_send", args); err != nil || !strings.HasPrefix(res, "Sent to peer0001 on box.") {
				t.Fatalf("/exit with an attachment: %q %v", res, err)
			}
			if sent := sentToAgent(f); len(sent) != 6 || !strings.HasPrefix(sent[5], prompts.RenderSideConversation(prompts.SideConversation{From: "conv1", Text: "/exit"})) {
				t.Fatalf("/exit with an attachment: sent %q", sent[5:])
			}
			if after := taskLedger(t, s, "conv1", "conv2"); after != before {
				t.Fatalf("/exit changed the tasks:\n%s\nwas\n%s", after, before)
			}
		})
	}
}

// exitRefusal is agent_send's and agent_interrupt's answer to /exit for agent sid on another conversation's task todo.
func exitRefusal(sid string, todo Todo, text string) string {
	return fmt.Sprintf("%s works for task %q (%s) of another conversation: %s asks to close the agent that task runs on, which is not "+
		"this conversation's to do; nothing was sent or stopped. The user can close it in the app, or with agent_archive and close "+
		"once it is idle and they confirm on the phone", sid, todo.Title, todo.ID, strings.TrimSpace(text))
}

// From the health space the refusal comes before the phone is asked: no card, nothing sent.
func TestAgentSendExitToAnotherConversationsAgentAsksNothing(t *testing.T) {
	f := healthHub()
	f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: "code-0000-0000", Cwd: "/home/me/src/app", Agent: "claude",
		Status: proto.StatusIdle, Title: "app"})
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "main-0000-0000" })
	ctx := t.Context()
	todo, err := s.createTodo(ctx, "Chart the app", "notes", "main-0000-0000", "")
	if err != nil {
		t.Fatal(err)
	}
	s.superviseTodo(ctx, todo.ID, "main-0000-0000", []string{"a chart"}, 30)
	if err := s.linkTodoAgent(ctx, todo.ID, "server", "code-0000-0000", todo.Title, "/home/me/src/app", "claude"); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var out string
	var isErr bool
	go func() {
		defer close(done)
		out, isErr = spaceTool(t, s, "sleep-000-0000", "agent_send", `{"session_id":"code-0000-0000","text":"/exit"}`)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("waited for the phone: %+v", s.pendingQuestions())
	}
	if !isErr || !strings.Contains(out, "asks to close the agent that task runs on") || len(s.pendingQuestions()) != 0 ||
		len(sentToAgent(f)) != 0 {
		t.Fatalf("%s, sent %q", out, sentToAgent(f))
	}
}

// The check is made again once the phone answered (#3537): an agent that became another conversation's task while the
// user was asked is not closed by an approved /exit, nor sent it wrapped as a side conversation, from agent_send or as
// agent_interrupt's then.
func TestAgentSendExitRecheckedAfterThePhone(t *testing.T) {
	for _, tc := range []struct{ tool, args string }{
		{"agent_send", `{"session_id":"code-0000-0000","text":"/exit"}`},
		{"agent_interrupt", `{"machine":"server","session_id":"code-0000-0000","then":"/exit"}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			f := healthHub()
			f.sessions["server"] = append(f.sessions["server"], proto.Session{SessionID: "code-0000-0000", Cwd: "/home/me/src/app",
				Agent: "claude", Status: proto.StatusIdle, Title: "app"})
			s := newTestService(t, f)
			setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "main-0000-0000" })
			ctx := t.Context()
			done := make(chan struct{})
			var out string
			var isErr bool
			go func() {
				defer close(done)
				out, isErr = spaceTool(t, s, "sleep-000-0000", tc.tool, tc.args)
			}()
			var qs []proto.Interaction
			waitFor(t, "the card on the phone", func() bool { qs = s.pendingQuestions(); return len(qs) > 0 })
			if len(qs) != 1 {
				t.Fatalf("no card on the phone: %+v", qs)
			}
			// While the phone is asked, the main conversation takes the agent under its supervision.
			todo, err := s.createTodo(ctx, "Chart the app", "notes", "main-0000-0000", "")
			if err != nil {
				t.Fatal(err)
			}
			s.superviseTodo(ctx, todo.ID, "main-0000-0000", []string{"a chart"}, 30)
			if err := s.linkTodoAgent(ctx, todo.ID, "server", "code-0000-0000", todo.Title, "/home/me/src/app", "claude"); err != nil {
				t.Fatal(err)
			}
			allow := true
			raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
			if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: qs[0].ID, Answer: raw}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("no answer after the phone")
			}
			if !isErr || !strings.Contains(out, "asks to close the agent that task runs on") || len(sentToAgent(f)) != 0 ||
				len(f.interrupted()) != 0 {
				t.Fatalf("%s, sent %q, interrupted %v", out, sentToAgent(f), f.interrupted())
			}
		})
	}
}
