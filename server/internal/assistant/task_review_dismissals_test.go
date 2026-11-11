package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestTaskReviewDismissalErrorsLocalized(t *testing.T) {
	for _, lang := range []i18n.Lang{"en", "zh-Hans", "ja"} {
		for _, key := range []string{"assistant-tasks.decision_review_changed", "assistant-tasks.decision_review_fields"} {
			body := apierr.LocalizedBody(409, "", i18n.ErrEnglish(key), lang)
			want := i18n.T(lang, key)
			if got := body["error"]; got != want || want == key {
				t.Fatalf("%s %s: got %v, want %s", lang, key, got, want)
			}
		}
	}
}

func dismissalEnv(t *testing.T, st *memStore, at time.Time, ask bool) *todoEnv {
	t.Helper()
	e := newTodoEnv(t, st)
	e.s.todos.loaded = true
	e.s.todos.todos = map[string]*Todo{"task": {ID: "task", Title: "Example task", Supervision: &Supervision{
		State: reviewNeedsUser, Reviews: []TaskReview{{At: at, Verdict: reviewNeedsUser, Note: "Original explanation"}},
	}}}
	if ask {
		e.s.accept.loaded = true
		e.s.accept.asks = map[string]*acceptAsk{"task": {AskedAt: at.Add(time.Second), Conclusion: true, Outcome: outcomeDecide,
			Reason: "Which option?", Recommend: "Option A", Options: []DecisionOption{{Label: "Option A"}, {Label: "Option B"}},
			Deadline: at.Add(-time.Minute), Reversible: true}}
	}
	return e
}

func dismissalBody(at time.Time, receipt string) string {
	b, _ := json.Marshal(map[string]string{"review_at": at.Format(time.RFC3339Nano), "dismissal_id": receipt})
	return string(b)
}

func dismissReview(t *testing.T, e *todoEnv, at time.Time) decisionReview {
	t.Helper()
	w := e.must("POST", "/api/v1/todos/task/decision-dismiss", dismissalBody(at, ""))
	var state decisionReview
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || state.DismissalID == "" || !state.ReviewAt.Equal(at) {
		t.Fatalf("dismissal receipt: %s (%v)", w.Body, err)
	}
	return state
}

func TestTaskReviewDismissalReceiptAndRestart(t *testing.T) {
	for _, ask := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy note only", true: "real ask"}[ask], func(t *testing.T) {
			st := &memStore{}
			at := time.Now().UTC().Add(-time.Hour)
			e := dismissalEnv(t, st, at, ask)
			before := todoNow(e.s, "task")
			first := dismissReview(t, e, at)
			if again := dismissReview(t, e, at); again != first {
				t.Fatal("dismiss retry changed receipt")
			}
			if after := todoNow(e.s, "task"); !reflect.DeepEqual(before, after) {
				t.Fatal("dismiss changed the task")
			}
			// A new manager reads the stored receipt, with neither ask nor review rewritten.
			e = dismissalEnv(t, st, at, ask)
			w := e.must("GET", "/api/v1/todos", "")
			var out struct {
				Todos []listedTodo `json:"todos"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Todos) != 1 || out.Todos[0].DecisionReview == nil || *out.Todos[0].DecisionReview != first {
				t.Fatalf("restart: %s", w.Body)
			}
			if !ask && (out.Todos[0].Ask != nil || out.Todos[0].Supervision.Reviews[0].Note != "Original explanation") {
				t.Fatalf("invented legacy ask: %s", w.Body)
			}
			undo := dismissalBody(at, first.DismissalID)
			for range 2 {
				w = e.must("POST", "/api/v1/todos/task/decision-undo-dismiss", undo)
				var state decisionReview
				_ = json.Unmarshal(w.Body.Bytes(), &state)
				if state.DismissalID != "" || !state.ReviewAt.Equal(at) {
					t.Fatalf("undo: %s", w.Body)
				}
			}
			e = dismissalEnv(t, st, at, ask)
			e.must("POST", "/api/v1/todos/task/decision-undo-dismiss", undo) // Undone receipt survives restart too.
			second := dismissReview(t, e, at)
			if second.DismissalID == first.DismissalID {
				t.Fatal("new dismissal reused undone receipt")
			}
			if w = e.do("POST", "/api/v1/todos/task/decision-undo-dismiss", undo); w.Code != 409 {
				t.Fatalf("stale undo: %d %s", w.Code, w.Body)
			}
			if got := dismissReview(t, e, at); got != second {
				t.Fatal("stale undo removed newer dismissal")
			}
			newAt := at.Add(time.Minute)
			e.s.todos.todos["task"].Supervision.Reviews = append(e.s.todos.todos["task"].Supervision.Reviews, TaskReview{At: newAt, Verdict: reviewNeedsUser})
			if w = e.do("POST", "/api/v1/todos/task/decision-dismiss", dismissalBody(at, "")); w.Code != 409 {
				t.Fatal("old round dismissed new review")
			}
			list := []listedTodo{{Todo: todoNow(e.s, "task")}}
			if err := e.s.listDecisionReviews(context.Background(), list); err != nil || list[0].DecisionReview.DismissalID != "" {
				t.Fatalf("new round did not recover: %+v %v", list, err)
			}
		})
	}
}

func TestTaskReviewDismissalSuppressesAcceptAndDeadline(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	e := dismissalEnv(t, &memStore{}, at, true)
	before := todoNow(e.s, "task")
	a, _ := e.s.askCopy("task")
	callsBefore := e.f.callCount()
	dismissReview(t, e, at)
	for _, extra := range []map[string]string{nil, {"option": "Option A"}, {"option": "Option B"}, {"response": respondExplainFirst}} {
		in := map[string]string{"asked_at": a.AskedAt.Format(time.RFC3339Nano)}
		for k, v := range extra {
			in[k] = v
		}
		body, _ := json.Marshal(in)
		w := e.do("POST", "/api/v1/todos/task/accept", string(body))
		if w.Code != 409 {
			t.Fatalf("dismissed accept: %d %s", w.Code, w.Body)
		}
	}
	if ok, err := e.s.acceptTry(context.Background(), "task", ""); ok || err != nil {
		t.Fatalf("card accept: %v %v", ok, err)
	}
	e.s.sweepTaskDeadlines(time.Now())
	if after, _ := e.s.askCopy("task"); !reflect.DeepEqual(a, after) {
		t.Fatalf("dismiss changed acceptance/deadline: %+v", after)
	}
	if after := todoNow(e.s, "task"); !reflect.DeepEqual(before, after) {
		t.Fatal("dismiss accepted or closed task")
	}
	if e.f.sends != nil {
		t.Fatal("dismiss sent an agent message")
	}
	if e.f.callCount() != callsBefore {
		t.Fatal("dismiss or suppressed acceptance called a device (including stop/close)")
	}
}

type dismissalSlowSendHub struct {
	*fakeHub
	entered chan struct{}
	release chan struct{}
}

func (h *dismissalSlowSendHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceSessionSend {
		close(h.entered)
		select {
		case <-h.release:
			return nil, errors.New("test send released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return h.fakeHub.DeviceCall(ctx, p)
}

func TestTaskReviewDismissalSlowAcceptDoesNotBlockTodoList(t *testing.T) {
	for _, option := range []string{"", "Option A", "Option B"} {
		t.Run("option="+option, func(t *testing.T) {
			at := time.Now().UTC().Add(-time.Hour)
			e := dismissalEnv(t, &memStore{}, at, true)
			e.s.todos.todos["task"].Origin = "conversation"
			e.s.todos.todos["task"].Agents = []TodoAgent{{MachineID: "server", SessionID: "worker"}}
			e.s.todos.todos["independent"] = &Todo{ID: "independent", Title: "Independent task", Supervision: &Supervision{
				State: reviewNeedsUser, Reviews: []TaskReview{{At: at, Verdict: reviewNeedsUser, Note: "Original explanation"}},
			}}
			e.f.sessions["server"] = []proto.Session{{MachineID: "server", SessionID: "worker", Status: proto.StatusIdle}}
			h := &dismissalSlowSendHub{fakeHub: e.f, entered: make(chan struct{}), release: make(chan struct{})}
			e.s.hub = h
			a, _ := e.s.askCopy("task")
			body, _ := json.Marshal(map[string]string{"asked_at": a.AskedAt.Format(time.RFC3339Nano), "option": option})
			accepted := make(chan int, 1)
			go func() { accepted <- e.do("POST", "/api/v1/todos/task/accept", string(body)).Code }()
			defer func() {
				close(h.release)
				select {
				case <-accepted:
				case <-time.After(2 * time.Second):
					t.Error("accept did not return after releasing its send")
				}
			}()
			select {
			case <-h.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("accept did not reach its agent send")
			}
			listed := make(chan bool, 1)
			go func() {
				w := e.do("GET", "/api/v1/todos", "")
				listed <- w.Code == 200 && strings.Contains(w.Body.String(), `"id":"independent"`)
			}()
			select {
			case ok := <-listed:
				if !ok {
					t.Fatal("independent task not listed during slow accept")
				}
			case <-time.After(time.Second):
				t.Fatal("slow accept blocked GET /todos")
			}
			changed := make(chan int, 1)
			go func() {
				changed <- e.do("POST", "/api/v1/todos/independent/decision-dismiss", dismissalBody(at, "")).Code
			}()
			select {
			case status := <-changed:
				if status != 200 {
					t.Fatalf("independent dismiss: %d", status)
				}
			case <-time.After(time.Second):
				t.Fatal("slow accept blocked another task's dismissal")
			}
		})
	}
}

func TestTaskReviewDismissalBodyLimit(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	e := dismissalEnv(t, &memStore{}, at, false)
	for _, path := range []string{"decision-dismiss", "decision-undo-dismiss"} {
		if w := e.doAs(roleExtension, "POST", "/api/v1/todos/task/"+path, dismissalBody(at, "receipt")); w.Code != 403 {
			t.Fatalf("extension called %s: %d", path, w.Code)
		}
		body, _ := json.Marshal(map[string]string{"review_at": at.Format(time.RFC3339Nano), "dismissal_id": strings.Repeat("x", 8<<10)})
		if w := e.do("POST", "/api/v1/todos/task/"+path, string(body)); w.Code != 400 {
			t.Fatalf("oversized %s: %d", path, w.Code)
		}
	}
	if len(e.s.reviewDismissals.byTodo) != 0 {
		t.Fatal("oversized request changed dismissal book")
	}
}

func TestTaskReviewDismissalEmptyLegacyAsk(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	e := dismissalEnv(t, &memStore{}, at, true)
	e.s.accept.asks["task"] = &acceptAsk{AskedAt: at, Conclusion: true, Outcome: outcomeDecide}
	before := todoNow(e.s, "task")
	d := dismissReview(t, e, at)
	e.must("POST", "/api/v1/todos/task/decision-undo-dismiss", dismissalBody(at, d.DismissalID))
	if after := todoNow(e.s, "task"); !reflect.DeepEqual(before, after) {
		t.Fatal("empty legacy ask changed original explanation")
	}
	a, _ := e.s.askCopy("task")
	if a.Reason != "" || a.Recommend != "" || len(a.Options) != 0 || !a.AcceptedAt.IsZero() {
		t.Fatal("legacy ask was synthesized or accepted")
	}
}

func TestTaskReviewDismissalAcceptFailsClosed(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	st := &memStore{}
	e := dismissalEnv(t, st, at, true)
	a, _ := e.s.askCopy("task")
	st.failKey(taskReviewDismissalsKey, errors.New("read failed"))
	body, _ := json.Marshal(map[string]string{"asked_at": a.AskedAt.Format(time.RFC3339Nano)})
	if w := e.do("POST", "/api/v1/todos/task/accept", string(body)); w.Code != 503 {
		t.Fatalf("unread dismissal book accepted: %d %s", w.Code, w.Body)
	}
	e.s.sweepTaskDeadlines(time.Now())
	if after, _ := e.s.askCopy("task"); !reflect.DeepEqual(a, after) {
		t.Fatal("unread dismissal book allowed deadline default")
	}
}

func TestTaskReviewDismissalFailuresAndValidation(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	st := &memStore{}
	e := dismissalEnv(t, st, at, false)
	body := dismissalBody(at, "")
	if w := e.doAs("read", "POST", "/api/v1/todos/task/decision-dismiss", body); w.Code != 403 {
		t.Fatalf("read role: %d", w.Code)
	}
	for _, body := range []string{`{}`, `{"review_at":"bad"}`} {
		if w := e.do("POST", "/api/v1/todos/task/decision-dismiss", body); w.Code != 400 {
			t.Fatalf("invalid: %d", w.Code)
		}
	}
	st.failKey(taskReviewDismissalsKey, errors.New("read failed"))
	if w := e.do("GET", "/api/v1/todos", ""); w.Code != 503 {
		t.Fatalf("failed read exposed decision: %d", w.Code)
	}
	if w := e.do("POST", "/api/v1/todos/task/decision-dismiss", body); w.Code != 503 {
		t.Fatalf("failed read dismissed: %d", w.Code)
	}
	if e.s.reviewDismissals.loaded {
		t.Fatal("failed read marked loaded")
	}
	st.failKey(taskReviewDismissalsKey, nil)
	st.failPut = errors.New("write failed")
	if w := e.do("POST", "/api/v1/todos/task/decision-dismiss", body); w.Code != 503 {
		t.Fatalf("failed write dismissed: %d", w.Code)
	}
	if len(e.s.reviewDismissals.byTodo) != 0 {
		t.Fatal("failed write not rolled back")
	}
	st.failPut = nil
	d := dismissReview(t, e, at)
	st.failPut = errors.New("undo failed")
	if w := e.do("POST", "/api/v1/todos/task/decision-undo-dismiss", dismissalBody(at, d.DismissalID)); w.Code != 503 {
		t.Fatalf("failed undo: %d", w.Code)
	}
	if e.s.reviewDismissals.byTodo["task"].Undone {
		t.Fatal("failed undo not rolled back")
	}
	st.failPut = nil
	for _, mutate := range []func(*Todo){func(t *Todo) { t.Supervision.State = reviewPending }, func(t *Todo) { t.DoneAt = &at }} {
		mutate(e.s.todos.todos["task"])
		if w := e.do("POST", "/api/v1/todos/task/decision-dismiss", body); w.Code != 409 {
			t.Fatalf("not an open decision: %d", w.Code)
		}
		list := []listedTodo{{Todo: todoNow(e.s, "task")}}
		if err := e.s.listDecisionReviews(context.Background(), list); err != nil || list[0].DecisionReview != nil {
			t.Fatal("ended decision exposed")
		}
		e.s.todos.todos["task"].Supervision.State = reviewNeedsUser
	}
}
