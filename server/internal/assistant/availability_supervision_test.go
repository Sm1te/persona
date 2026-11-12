package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The supervision read (#5086): acceptances, reopens and rework rounds by the reviewer's model and effort, and a
// supervisor conversation's day of output next to that day's acceptances.
func TestSummarizeSupervision(t *testing.T) {
	day := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	since := day.Add(-time.Hour)
	ev := func(at time.Time, session, kind string, detail map[string]any) db.WorkEvent {
		b, _ := json.Marshal(detail) // as the store returns them
		var d map[string]any
		_ = json.Unmarshal(b, &d)
		machine := "server"
		if kind != roundEvent {
			machine, session = "", ""
		}
		return db.WorkEvent{At: at, MachineID: machine, SessionID: session, Kind: kind, Detail: d}
	}
	reviewed := func(at time.Time, by, role, todo, verdict string, round int, model, effort string) db.WorkEvent {
		return ev(at, "", taskReviewedEvent, map[string]any{"todo": todo, "verdict": verdict, "round": round, "conversation": by, "role": role,
			"model": model, "effort": effort})
	}
	round := func(at time.Time, session string, tokens int, model, effort string) db.WorkEvent {
		d := map[string]any{"source": proto.SourceSupervision, "seconds": 10.0, "main": false, "model": model, "effort": effort}
		if tokens > 0 {
			d["output_tokens"] = tokens
		}
		return ev(at, session, roundEvent, d)
	}
	events := []db.WorkEvent{
		// Before the window: only what the first difference is taken from; its acceptance is not counted.
		round(since.Add(-2*time.Hour), "bg", 1000, "small", "medium"),
		reviewed(since.Add(-2*time.Hour), "bg", supervisorBackground, "old", reviewAccepted, 0, "small", "medium"),
		round(day, "bg", 1500, "small", "medium"),
		reviewed(day, "bg", supervisorBackground, "t1", reviewRework, 1, "small", "medium"),
		reviewed(day.Add(time.Minute), "bg", supervisorBackground, "t1", reviewAccepted, 1, "small", "medium"),
		reviewed(day.Add(2*time.Minute), "bg", supervisorBackground, "t2", reviewAccepted, 2, "small", "medium"),
		round(day.Add(3*time.Minute), "bg", 1800, "small", "medium"),
		// t1 is reopened, twice: its acceptance counts as reopened once. A reopen of a task nobody accepted counts nothing.
		ev(day.Add(time.Hour), "", todoReopenedEvent, map[string]any{"todo": "t1"}),
		ev(day.Add(2*time.Hour), "", todoReopenedEvent, map[string]any{"todo": "t1"}),
		ev(day.Add(2*time.Hour), "", todoReopenedEvent, map[string]any{"todo": "t9"}),
		// The next day it runs a stronger model: a new transcript counts from zero, and a round with no count adds nothing.
		round(day.Add(24*time.Hour), "bg", 400, "big", "high"),
		round(day.Add(24*time.Hour+time.Minute), "bg", 0, "big", "high"),
		reviewed(day.Add(24*time.Hour+2*time.Minute), "bg", supervisorBackground, "t3", reviewAccepted, 0, "big", "high"),
		// The main conversation accepted one on a model its node did not name; a conversation that reviewed nothing has no row.
		round(day, "main", 5000, "", ""),
		round(day.Add(time.Minute), "main", 9000, "", ""),
		ev(day.Add(2*time.Minute), "", taskReviewedEvent, map[string]any{"todo": "t4", "verdict": reviewAccepted, "round": 0, "conversation": "main", "role": supervisorMain}),
		round(day, "side", 100, "small", "medium"),
		round(day.Add(time.Minute), "side", 300, "small", "medium"),
	}
	got := summarizeSupervision(since, events, time.UTC)
	want := supervisionStats{
		ByModel: []supervisorModel{
			{Model: "small", Effort: "medium", Accepted: 2, Reopened: 1, ReworkRounds: 3, ReworkRoundsPerTask: 1.5},
			{Model: "", Effort: "", Accepted: 1},
			{Model: "big", Effort: "high", Accepted: 1},
		},
		Days: []supervisorDay{
			{Day: "2026-03-03", Conversation: "bg", Role: supervisorBackground, Model: "big", Effort: "high", Accepted: 1, OutputTokens: 400},
			{Day: "2026-03-02", Conversation: "bg", Role: supervisorBackground, Model: "small", Effort: "medium", Accepted: 2, OutputTokens: 800},
			{Day: "2026-03-02", Conversation: "main", Role: supervisorMain, Accepted: 1, OutputTokens: 4000},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("supervision\n got %+v\nwant %+v", got, want)
	}
	if all := summarizeAvailability(48, since, events, time.UTC, nil, nil); !reflect.DeepEqual(all.Supervision, want) {
		t.Fatalf("the availability read's supervision: %+v", all.Supervision)
	}
}

// A task_review is recorded with who reviewed and what that conversation ran on: the model and effort its node lists
// for it, else what its purpose is set to. A round's event carries the model, the effort and the output count too.
func TestTaskReviewIsRecordedByTheSupervisorsModel(t *testing.T) {
	s, f, todo := newBackgroundService(t)
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了", 0)
	bg := backgroundID(t, s)
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })
	reviews := func() []map[string]any {
		var out []map[string]any
		for _, e := range testEvents(s) {
			if e.Kind == taskReviewedEvent {
				out = append(out, e.Detail)
			}
		}
		return out
	}
	review := func(self, verdict string) {
		t.Helper()
		if _, err := runJSON(t, s, self, "task_review", map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": "补测试"}); err != nil {
			t.Fatal(err)
		}
	}
	review(bg, reviewRework)
	review("conv1", reviewRework)
	review(bg, reviewAccepted)
	got := reviews()
	if len(got) != 3 {
		t.Fatalf("%d reviews recorded: %v", len(got), got)
	}
	for i, want := range []map[string]any{
		{"todo": todo.ID, "verdict": reviewRework, "round": 1, "conversation": bg, "role": supervisorBackground},
		{"todo": todo.ID, "verdict": reviewRework, "round": 2, "conversation": "conv1", "role": supervisorMain},
		{"todo": todo.ID, "verdict": reviewAccepted, "round": 2, "conversation": bg, "role": supervisorBackground},
	} {
		for k, v := range want {
			if got[i][k] != v {
				t.Errorf("review %d: %s = %v, want %v (%v)", i+1, k, got[i][k], v, got[i])
			}
		}
	}
	// A second acceptance of the closed task is no acceptance of its own.
	review("conv1", reviewAccepted)
	if n := len(reviews()); n != 3 {
		t.Fatalf("accepting a closed task was recorded: %d", n)
	}
	stats := summarizeSupervision(time.Now().Add(-time.Hour), testEvents(s), time.UTC)
	if len(stats.ByModel) != 1 || stats.ByModel[0].Accepted != 1 || stats.ByModel[0].ReworkRounds != 2 || stats.ByModel[0].Reopened != 0 {
		t.Fatalf("read back: %+v", stats)
	}
}

// What a reviewer ran on: its node's own word first, the purpose's binding for what the node does not say.
func TestSupervisorRuns(t *testing.T) {
	ft := newFleetTest(t)
	s := ft.s
	ctx := context.Background()
	m, _ := s.fleetMachine("mac")
	ft.f.hello.Assistant.MachineID = "mac"
	ft.sessions("mac", proto.Session{SessionID: "bg", Agent: "claude", Cwd: m.assistantCwd(), Status: proto.StatusIdle, Model: "listed-model", Effort: "high", OutputTokens: 1234})
	waitUntil(t, func() bool { _, ok := s.nodeSession("mac", "bg"); return ok })
	if model, effort := s.supervisorRuns(ctx, "bg", supervisorOther); model != "listed-model" || effort != "high" {
		t.Fatalf("as its node lists it: %q %q", model, effort)
	}
	if model, effort := s.supervisorRuns(ctx, "gone", supervisorOther); model != "" || effort != "" {
		t.Fatalf("a conversation nothing is known of: %q %q", model, effort)
	}
	d := s.roundRan("mac", "bg", map[string]any{"source": roundOwner})
	if d["model"] != "listed-model" || d["effort"] != "high" || d["output_tokens"] != 1234 || d["source"] != roundOwner {
		t.Fatalf("a round's detail: %v", d)
	}
	if d := s.roundRan("mac", "gone", map[string]any{"source": roundOwner}); len(d) != 1 {
		t.Fatalf("a round of a conversation its node does not list: %v", d)
	}
}

// planUpdateStore is planMemStore whose plans can also change state, so a task_plan starts on it.
type planUpdateStore struct{ *planMemStore }

func (m planUpdateStore) UpdateTaskPlan(_ context.Context, id, from, to string, tasks json.RawMessage, at time.Time) (bool, error) {
	m.pmu.Lock()
	defer m.pmu.Unlock()
	p, ok := m.plans[id]
	if !ok || p.State != from {
		return false, nil
	}
	p.State, p.Tasks, p.UpdatedAt = to, tasks, at
	m.plans[id] = p
	return true, nil
}

// superviseRun is one run of the background supervisor's work on a fresh manager, with patch saved for its model
// purpose first ("" for none): what it was started and sent with, what its reviews answered and wrote, and how the
// task ended. models are the model, effort and launcher its start and each message to it carried, taken out of the
// rest with model_fingerprint, the binding's own hash (models_resolve.go modelBindingFingerprint).
func superviseRun(t *testing.T, patch string) (run map[string]any, models []map[string]any) {
	t.Helper()
	s, f, todo := newSupervisedServiceOn(t, planUpdateStore{&planMemStore{memStore: &memStore{}, plans: map[string]db.TaskPlanRow{}}})
	s.noBackground = false
	f.mu.Lock()
	f.made = 100
	f.mu.Unlock()
	ctx := context.Background()
	if patch != "" {
		if r := modelRequest(s, "PATCH", purposeSupervise, patch); r.Code != 200 {
			t.Fatalf("saving %s: %d %s", patch, r.Code, r.Body.String())
		}
	}
	agentTurn(s, f, proto.StatusWorking, "", 0)
	agentTurn(s, f, proto.StatusDone, "做完了，PR https://github.com/o/r/pull/7 已合并", 0)
	bg := backgroundID(t, s)
	listBackground(t, s, f, bg)
	waitFor(t, "the background supervisor's message", func() bool { return len(f.noticesTo(bg)) == 1 })
	plain := func(v string) string { return strings.ReplaceAll(strings.ReplaceAll(v, todo.ID, "<todo>"), bg, "<bg>") }
	run = map[string]any{}
	review := func(verdict, note string) {
		t.Helper()
		res, err := runJSON(t, s, bg, "task_review", map[string]any{"todo_id": todo.ID, "verdict": verdict, "note": note})
		if err != nil {
			t.Fatal(err)
		}
		run["review "+verdict] = plain(res)
	}
	review(reviewRework, "补测试")
	review(reviewAccepted, "已合并，测试补了")
	settleService(t, s)

	f.mu.Lock()
	var sent []map[string]any
	for _, a := range f.sends {
		if id, _ := a["session_id"].(string); id == "" || id == bg { // its start, and the messages to it
			sent = append(sent, maps.Clone(a))
		}
	}
	f.mu.Unlock()
	for i, a := range sent {
		m := map[string]any{}
		for _, k := range []string{"model", "effort", "launcher", "model_fingerprint"} {
			if v, ok := a[k]; ok {
				m[k] = v
				delete(a, k)
			}
		}
		models = append(models, m)
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		run["sent "+string(rune('a'+i))] = plain(string(raw))
	}
	run["to the agent"] = sentToAgent(f)
	run["to the main conversation"] = f.noticesTo("conv1")
	cfg := s.settings(ctx)
	run["routing"] = []any{s.ownSupervisor(cfg) == bg, s.isLeader(ctx, bg), slices.Contains(cfg.Leaders, bg), s.supervising(bg)}
	done := todoNow(s, todo.ID)
	run["task"] = []any{done.DoneAt != nil, done.Origin, done.Supervision.Supervisor, done.Supervision.State, done.Supervision.Round, len(done.Supervision.Reviews)}
	items, _, err := s.feed(ctx, "conv1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var feed []string
	for _, it := range items {
		var body reviewFeedBody
		if it.Kind == feedReview && json.Unmarshal(it.Body, &body) == nil {
			feed = append(feed, body.Verdict+" by "+body.By+": "+body.Note)
		}
	}
	run["feed"] = feed
	var reviewed []string
	for _, e := range testEvents(s) {
		if e.Kind == taskReviewedEvent {
			reviewed = append(reviewed, e.Detail["verdict"].(string)+" by "+e.Detail["role"].(string))
		}
	}
	run["events"] = reviewed
	return run, models
}

// PRINCIPLES 5: giving the background supervisor another model changes the model it runs on and nothing else: it is
// started and sent the same words in the same way, routes, reviews and reports the same, and its tasks end the same.
func TestSwitchingTheSuperviseModelChangesOnlyTheModel(t *testing.T) {
	inherit, base := superviseRun(t, "")
	own, picked := superviseRun(t, `{"revision":0,"model":"another-model","effort":"low"}`)
	if len(inherit["sent a"].(string)) == 0 || len(base) < 2 {
		t.Fatalf("the run sent nothing to compare: %v", inherit)
	}
	for _, k := range slices.Sorted(maps.Keys(own)) {
		if !reflect.DeepEqual(inherit[k], own[k]) {
			t.Errorf("%s changed with the model:\n inherited: %s\n its own:   %s", k, clip(fmt.Sprint(inherit[k]), 400), clip(fmt.Sprint(own[k]), 400))
		}
	}
	if len(inherit) != len(own) || len(base) != len(picked) {
		t.Fatalf("the runs differ in what they did: %d and %d things, %d and %d sends", len(inherit), len(own), len(base), len(picked))
	}
	for i := range picked {
		if picked[i]["model"] != "another-model" || picked[i]["effort"] != "low" {
			t.Errorf("send %d ran on %v, not the purpose's own model", i+1, picked[i])
		}
		if base[i]["model"] == "another-model" || base[i]["effort"] == "low" {
			t.Errorf("send %d of the inheriting run ran on %v", i+1, base[i])
		}
		if picked[i]["model_fingerprint"] == base[i]["model_fingerprint"] {
			t.Errorf("send %d: the binding's fingerprint did not move with the model", i+1)
		}
		if picked[i]["launcher"] != base[i]["launcher"] {
			t.Errorf("send %d: the launcher moved with the model: %v, %v", i+1, picked[i], base[i])
		}
	}
}

// The main chat's rule for a task's model (#5086) speaks of tiers and names no provider's model, and says what to do
// when the tiers cannot be told apart.
func TestTaskModelRuleNamesNoModel(t *testing.T) {
	md := prompts.RenderAssistantClaudeMD(prompts.AssistantClaudeMD{})
	_, rest, ok := strings.Cut(md, "任务用哪一档模型")
	if !ok {
		t.Fatal("CLAUDE.md has no rule for a task's model")
	}
	rule, _, ok := strings.Cut(rest, "写进 `why`。")
	if !ok {
		t.Fatal("the rule does not end with stating the choice in why")
	}
	for _, want := range []string{"低一档", "`medium`", "日常主力那一档", "最高一档", "先提高 `effort`", "都留空", "永远优先"} {
		if !strings.Contains(rule, want) {
			t.Errorf("the rule lacks %q", want)
		}
	}
	lower := strings.ToLower(rule)
	for _, name := range []string{"claude", "opus", "sonnet", "haiku", "fable", "gpt", "gemini", "codex", "deepseek"} {
		if strings.Contains(lower, name) {
			t.Errorf("the rule names %q", name)
		}
	}
}

// Every built-in purpose says what it is for in each language, and the sentence names no model.
func TestModelPurposesDescribeThemselves(t *testing.T) {
	s, _, _ := modelService(t)
	for _, c := range []struct{ lang, supervise string }{
		{"", "跟进并验收"}, {"en", "Follows up and reviews"}, {"ja", "フォローして検収"},
	} {
		for _, p := range modelPurposes {
			h := map[string][]string{}
			if c.lang != "" {
				h["Accept-Language"] = []string{c.lang}
			}
			res, err := serveHTTP(context.Background(), s, assistantwire.HTTPRequest{Method: "GET", Path: "/api/v1/model-config/purposes/" + p.ID, Header: h}, s.lang)
			if err != nil || res.Status != 200 {
				t.Fatalf("%s %q: %d %v", p.ID, c.lang, res.Status, err)
			}
			var out struct {
				Purpose ModelPurpose `json:"purpose"`
			}
			if err := json.Unmarshal(res.Body, &out); err != nil {
				t.Fatal(err)
			}
			d := out.Purpose.Description
			if d == "" || strings.HasPrefix(d, "assistant-models.") {
				t.Errorf("%s %q has no description: %q", p.ID, c.lang, d)
			}
			if p.ID == purposeSupervise && !strings.Contains(d, c.supervise) {
				t.Errorf("%q: the background supervisor's: %q", c.lang, d)
			}
			lower := strings.ToLower(d)
			for _, name := range []string{"claude", "opus", "sonnet", "haiku", "fable", "gpt"} {
				if strings.Contains(lower, name) {
					t.Errorf("%s %q names a model: %q", p.ID, c.lang, d)
				}
			}
		}
	}
	// The catalog carries it too, and the sentence is wording only: no binding moved.
	r := modelRequest(s, "GET", "", "")
	var out struct {
		Purposes []ModelPurpose `json:"purposes"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil || len(out.Purposes) != len(modelConfigIDs) {
		t.Fatalf("catalog: %v %s", err, r.Body.String())
	}
	for _, p := range out.Purposes {
		if p.Description == "" || p.Override != (ModelBinding{}) {
			t.Errorf("%s in the catalog: %q %+v", p.ID, p.Description, p.Override)
		}
	}
}
