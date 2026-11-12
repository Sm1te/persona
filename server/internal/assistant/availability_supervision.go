package assistant

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// What supervision costs per accepted task (#5086, step K1 of docs/design/proposals/main-chat-availability.md;
// docs/assistant/assistant.md "Supervision by model"): per model and effort the reviewing conversation ran on, how
// many tasks it accepted, how many of those were reopened afterwards and how many rework rounds they took; and per
// day and supervisor conversation, the tasks it accepted next to the output tokens it generated. Work events like the
// rest of availability.go, no new table: one per task_review, and the round event's own model, effort and output
// count.
//
// The tokens are not attributed to tasks: nothing recorded says which task a supervisor's turn was spent on (a
// supervision message gathers several tasks' signals, the token counts are per conversation or per machine), so the
// read puts a conversation's day of output next to that day's acceptances and leaves the division to the reader.
const (
	// taskReviewedEvent: a conversation gave a task_review. Detail: todo, verdict (as written: a rework past its
	// rounds is needs_user), round (the task's rework rounds so far), conversation (the reviewer), role (the
	// supervisor* constants), model and effort (what the reviewer ran on, supervisorRuns; absent when not known). An
	// acceptance of a task that was closed already is not recorded.
	taskReviewedEvent = "task_reviewed"
)

// Which supervisor a reviewing conversation is.
const (
	supervisorMain       = "main"
	supervisorBackground = "background"
	supervisorLeader     = "leader"
	supervisorOther      = "conversation" // a side conversation reviewing what it handed out
)

// supervisorRole is which supervisor conversation id is.
func (s *Service) supervisorRole(ctx context.Context, id string) string {
	switch {
	case s.isMain(ctx, id):
		return supervisorMain
	case s.isBackground(ctx, id):
		return supervisorBackground
	case s.isLeader(ctx, id):
		return supervisorLeader
	}
	return supervisorOther
}

// supervisorRuns is the model and effort conversation id (of role) runs on: what its node lists for it, else, for the
// main conversation and the background supervisor, what their purpose is set to. Either may be "" (the launcher's
// own default, which nothing here can name).
func (s *Service) supervisorRuns(ctx context.Context, id, role string) (model, effort string) {
	if c, ok := s.nodeSession(s.settings(ctx).MachineID, id); ok {
		model, effort = c.Model, c.Effort
	}
	if model != "" && effort != "" {
		return model, effort
	}
	purpose := ""
	switch role {
	case supervisorMain:
		purpose = "assistant"
	case supervisorBackground:
		purpose = purposeSupervise
	}
	if purpose != "" {
		if b, err := s.resolveModel(ctx, purpose); err == nil {
			model, effort = cmp.Or(model, b.Model), cmp.Or(effort, b.Effort)
		}
	}
	return model, effort
}

// taskReviewed records conversation self's review of task todo: verdict as written, round the task's rework rounds
// so far.
func (s *Service) taskReviewed(ctx context.Context, self, todo, verdict string, round int) {
	role := s.supervisorRole(ctx, self)
	detail := map[string]any{"todo": todo, "verdict": verdict, "round": round, "conversation": self, "role": role}
	if model, effort := s.supervisorRuns(ctx, self, role); model != "" || effort != "" {
		if model != "" {
			detail["model"] = model
		}
		if effort != "" {
			detail["effort"] = effort
		}
	}
	s.workEvent("", "", taskReviewedEvent, detail)
}

// roundRan adds to a round event's detail what machine's conversation session ran on as the round ended, as its node
// lists it: model, effort, and output_tokens, the output it has generated so far in all (Session.OutputTokens; the
// read takes the difference between two rounds). Each is left out while not known.
func (s *Service) roundRan(machine, session string, detail map[string]any) map[string]any {
	c, ok := s.nodeSession(machine, session)
	if !ok {
		return detail
	}
	if c.Model != "" {
		detail["model"] = c.Model
	}
	if c.Effort != "" {
		detail["effort"] = c.Effort
	}
	if c.OutputTokens > 0 {
		detail["output_tokens"] = c.OutputTokens
	}
	return detail
}

// supervisionStats is the availability read's supervision.
type supervisionStats struct {
	// ByModel: per model and effort the reviewing conversation ran on, most acceptances first.
	ByModel []supervisorModel `json:"by_model"`
	// Days: per day (the owner's time zone) and conversation that reviewed a task in the window; newest day first.
	Days []supervisorDay `json:"days"`
}

type supervisorModel struct {
	// Model and Effort: what the reviewer ran on when it accepted; "" when not known (the launcher's own default).
	Model  string `json:"model"`
	Effort string `json:"effort"`
	// Accepted: the tasks a task_review accepted and closed in the window.
	Accepted int `json:"accepted"`
	// Reopened: how many of those acceptances were followed by the task being reopened, whichever way.
	Reopened int `json:"reopened"`
	// ReworkRounds: the rework rounds the accepted tasks had been sent back for before they were accepted, whoever
	// sent them back; ReworkRoundsPerTask is that over Accepted.
	ReworkRounds        int     `json:"rework_rounds"`
	ReworkRoundsPerTask float64 `json:"rework_rounds_per_task"`
}

type supervisorDay struct {
	Day          string `json:"day"`
	Conversation string `json:"conversation"`
	// Role: main, background, leader or conversation (a side conversation reviewing what it handed out).
	Role string `json:"role"`
	// Model and Effort: what the conversation ran on at the day's last round, else at its last review; "" when not
	// known.
	Model  string `json:"model"`
	Effort string `json:"effort"`
	// Accepted: the tasks the conversation accepted that day.
	Accepted int `json:"accepted"`
	// OutputTokens: the output tokens the conversation generated in the rounds that ended that day, all of them: its
	// talk with the owner too, and no input or cached tokens. Not a cost of the day's accepted tasks alone.
	OutputTokens int `json:"output_tokens"`
}

// summarizeSupervision reads supervisionStats out of events (oldest first; those before since only give a
// conversation's output count to take the window's first difference from, and are otherwise not counted). Reopens are
// counted up to the newest event, the acceptances from since.
func summarizeSupervision(since time.Time, events []db.WorkEvent, loc *time.Location) supervisionStats {
	out := supervisionStats{ByModel: []supervisorModel{}, Days: []supervisorDay{}}
	type accepted struct {
		at       time.Time
		key      [2]string // model, effort
		reopened bool
	}
	models := map[[2]string]*supervisorModel{}
	byTodo := map[string][]*accepted{}
	days := map[[2]string]*supervisorDay{} // day, conversation
	roles := map[string]string{}           // the conversations that reviewed in the window → their role at the last review
	lastOut := map[[2]string]float64{}     // machine, session → the output count at its last round
	day := func(e db.WorkEvent, conversation string) *supervisorDay {
		k := [2]string{e.At.In(loc).Format(time.DateOnly), conversation}
		d := days[k]
		if d == nil {
			d = &supervisorDay{Day: k[0], Conversation: conversation}
			days[k] = d
		}
		return d
	}
	for _, e := range events {
		str := func(k string) string { v, _ := e.Detail[k].(string); return v }
		num := func(k string) (float64, bool) {
			switch v := e.Detail[k].(type) {
			case float64:
				return v, true
			case int: // as recorded, before the store's JSON
				return float64(v), true
			}
			return 0, false
		}
		switch e.Kind {
		case roundEvent:
			total, ok := num("output_tokens")
			k := [2]string{e.MachineID, e.SessionID}
			prev, seen := lastOut[k]
			if ok {
				lastOut[k] = total
			}
			if e.At.Before(since) {
				continue
			}
			d := day(e, e.SessionID)
			if m, f := str("model"), str("effort"); m != "" || f != "" {
				d.Model, d.Effort = m, f
			}
			switch {
			case !ok || !seen: // no count, or nothing to take the difference from: the first recorded round counts nothing
			case total >= prev:
				d.OutputTokens += int(total - prev)
			default: // a new transcript: it counts from zero again
				d.OutputTokens += int(total)
			}
		case taskReviewedEvent:
			if e.At.Before(since) {
				continue
			}
			conversation := str("conversation")
			roles[conversation] = str("role")
			d := day(e, conversation)
			if d.Model == "" && d.Effort == "" {
				d.Model, d.Effort = str("model"), str("effort")
			}
			if str("verdict") != reviewAccepted {
				continue
			}
			d.Accepted++
			k := [2]string{str("model"), str("effort")}
			m := models[k]
			if m == nil {
				m = &supervisorModel{Model: k[0], Effort: k[1]}
				models[k] = m
			}
			m.Accepted++
			rounds, _ := num("round")
			m.ReworkRounds += int(rounds)
			byTodo[str("todo")] = append(byTodo[str("todo")], &accepted{at: e.At, key: k})
		case todoReopenedEvent:
			// The acceptance it undoes: the task's latest one before it, once.
			list := byTodo[str("todo")]
			if n := len(list); n > 0 && !list[n-1].reopened && !e.At.Before(list[n-1].at) {
				list[n-1].reopened = true
				models[list[n-1].key].Reopened++
			}
		}
	}
	for _, m := range models {
		m.ReworkRoundsPerTask = float64(m.ReworkRounds) / float64(m.Accepted)
		out.ByModel = append(out.ByModel, *m)
	}
	slices.SortFunc(out.ByModel, func(a, b supervisorModel) int {
		return cmp.Or(cmp.Compare(b.Accepted, a.Accepted), cmp.Compare(a.Model, b.Model), cmp.Compare(a.Effort, b.Effort))
	})
	for _, d := range days {
		if _, reviewed := roles[d.Conversation]; !reviewed || d.Accepted == 0 && d.OutputTokens == 0 {
			continue // not a supervisor in the window, or a day with nothing to put side by side
		}
		out.Days = append(out.Days, *d)
	}
	for i := range out.Days { // a day it only ran rounds on has the role of its reviews
		out.Days[i].Role = roles[out.Days[i].Conversation]
	}
	slices.SortFunc(out.Days, func(a, b supervisorDay) int {
		return cmp.Or(cmp.Compare(b.Day, a.Day), cmp.Compare(a.Conversation, b.Conversation))
	})
	return out
}
