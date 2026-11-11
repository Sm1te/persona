package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// What the assistant does on its own (docs/assistant/assistant.md "Scheduled tasks") is one list of tasks, each with a schedule:
//   - once: a follow-up the assistant scheduled from a conversation (schedule_followup), or the user in the app;
//   - daily: at a time on some weekdays (the morning briefing on weekdays at 09:00…), set up in the app or with schedule_routine;
//   - interval: every N minutes (a 30-minute patrol), from schedule_followup's `every` or the app;
//   - cron: on a 5-field cron expression in the user's time zone ("*/30 9-18 * * 1-5"), from either tool's `cron` or
//     the app (schedule_cron.go). It runs as a daily one does: a time missed by more than 30 minutes is skipped.
// A daily, interval or cron task may set Until. Once that time has passed, the scheduler turns the task off and records a
// skipped run instead of running it. A one-off ignores Until.
// A run goes into the conversation the task came from (SessionID), else into the task's own conversation
// (RunSessionID: the first run starts it, later ones continue it so each run sees the ones before), with the task's
// own launcher, permission mode and effort ("" = the assistant's defaults). A finished run pushes its answer like any agent turn
// (Machine.notifyFinished). The list and the history of runs live in Postgres settings, so they outlive a hub
// restart: a run is marked before it starts (no repeats), a once / interval task missed while the hub was down runs
// once when it is back (and is told how late it is), a daily one missed by more than 30 minutes is skipped and the
// skip shows in the history.
//
// Until 2026-10-31 routines and follow-ups were two lists; a follow-up vanished from the app the moment it started,
// so a patrol re-scheduling itself at the end of each run was nowhere to be seen (or stopped) while a run was stuck
// on an approval. loadSchedule moves the old lists over once, ids and times kept.

type Task struct {
	WorkoutID string `json:"workout_id,omitempty"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Prompt    string `json:"prompt"`
	Repeat    string `json:"repeat"` // once / daily / interval / cron
	// At is the next run of the task's own rule. An early run while a leader has open supervised
	// todos does not move it, so the original interval and its next run resume when they close.
	At           time.Time `json:"at,omitempty"`
	Time         string    `json:"time,omitempty"`     // daily: "HH:MM" in the user's time zone
	Weekdays     []int     `json:"weekdays,omitempty"` // daily: 1 = Monday … 7 = Sunday; empty = every day
	EveryMinutes int       `json:"every_minutes,omitempty"`
	Cron         string    `json:"cron,omitempty"` // cron: "minute hour day-of-month month day-of-week" or an alias (schedule_cron.go)
	// Until is when a daily, interval or cron task stops, stored in UTC. Zero means unset and is omitted.
	// A one-off ignores it: checkTask clears it, and the scheduler does not expire a one-off that still has one.
	Until   time.Time `json:"until,omitempty"`
	Enabled bool      `json:"enabled"`
	// Push is what of a run reaches the phone (schedule_push.go): "" every run's answer, "asked" only what the run
	// pushes itself (notify_user). Set where the request has it; a client that leaves it out keeps it (PUT, the old
	// app's routines PUT, the tools without push).
	Push string `json:"push,omitempty"`
	// Facts: the run's fixed part is worked out by Persona (briefing.go): what waits for the owner, what happened in the last
	// 12 hours and what is to be verified, listed in one Lark card and pushed as counts, while the model writes only a
	// sentence or two on top. On for the default morning and evening routines (a hub set up before gets it with their
	// current prompt, upgradeDefaultRoutines); an app that leaves it out of a change keeps it.
	Facts bool `json:"facts,omitempty"`
	// Report: a daily report (a dot's, docs/assistant/north-star.md "Daily briefing"): its runs open with the north
	// star's lines. Set with schedule_routine's report or in the body of PUT /api/v1/assistant/tasks/{id}; a change
	// that leaves it out keeps it.
	Report bool `json:"report,omitempty"`
	// factsText is the run's facts as the model is given them (briefing.go); set on the copy a run is made from, never stored.
	factsText string
	// kbDir is the knowledge directory the run's hint names (a daily run with the knowledge base on); set on the copy, never stored.
	kbDir string
	// cadenceEvery is the run's effective interval when a leader's open todos shorten it; set on the copy, never stored.
	cadenceEvery int
	// The conversation it came from (on MachineID), runs go into it; empty = the task's own conversation.
	MachineID string `json:"machine_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// The task's own conversation, set by the hub (2026-11-01: each run used to start a new one, so "compare with yesterday" had
	// nothing to compare with): the first run starts it on the assistant's machine with RunLauncher, later runs are
	// sent into it. Empty = the next run starts one (the app's "New chat next time"); so does a conversation that is gone or
	// can't be continued (another launcher or machine), and the new one is kept.
	RunMachineID string `json:"run_machine_id,omitempty"`
	RunSessionID string `json:"run_session_id,omitempty"`
	RunLauncher  string `json:"run_launcher,omitempty"`
	// How claude runs, chosen in the app only (the assistant can't raise its own permissions): "" = the assistant's
	// launcher, Claude Code's default mode, the launcher's effort.
	Launcher       string `json:"launcher,omitempty"`
	Model          string `json:"model,omitempty"`
	ModelRevision  int64  `json:"model_revision,omitempty"`
	PermissionMode string `json:"permission_mode,omitempty"`
	Effort         string `json:"effort,omitempty"`
	// Space is the space of the conversation that set it up ("health", docs/assistant/health.md): a run that starts a conversation
	// starts it there. "" is the assistant's own.
	Space string `json:"space,omitempty"`
	// CreatedBy is "assistant" for tasks Persona set up from a conversation.
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	// Set by the hub.
	LastRun       time.Time `json:"last_run,omitempty"`
	LastSessionID string    `json:"last_session_id,omitempty"`
	LastStatus    string    `json:"last_status,omitempty"` // of the last run: ok / failed / skipped
	LastError     string    `json:"last_error,omitempty"`
	NextRun       time.Time `json:"next_run,omitempty"`
	// A cron task's expression in words in the reader's language and its next runs; set on the view (tasksView), never stored.
	CronText string      `json:"cron_text,omitempty"`
	NextRuns []time.Time `json:"next_runs,omitempty"`
	// ArmedAt is when a daily or cron task was last created, switched on or given another time or weekdays (rearm): a run
	// due before that is no run it missed (#3816).
	ArmedAt time.Time `json:"armed_at,omitempty"`
}

// rearm notes that t starts counting from now when it was just created, switched on, or its daily time, weekdays or
// cron expression changed (old: as it was; a new task's has no ID or is off). Without it the scheduler took the run due
// earlier today for one the hub missed while it was down, and recorded "Persona was down and missed the time" (#3816).
func rearm(old Task, t *Task, now time.Time) {
	if !t.calendar() || !t.Enabled {
		return
	}
	if !old.Enabled || old.Repeat != t.Repeat || old.Time != t.Time || !slices.Equal(old.Weekdays, t.Weekdays) || old.Cron != t.Cron {
		// From the start of this minute: a task set at 10:05:20 for 10:05 (trying it out) still runs now.
		t.ArmedAt = now.UTC().Truncate(time.Minute).Add(-time.Nanosecond)
	}
}

const (
	repeatOnce     = "once"
	repeatDaily    = "daily"
	repeatInterval = "interval"
	repeatCron     = "cron"
)

// calendar: the task runs at times of day (daily, cron), not some time after the last run. Such a run missed by more
// than dailyCatchUp is skipped, and its message does not say how late it is.
func (t Task) calendar() bool { return t.Repeat == repeatDaily || t.Repeat == repeatCron }

// minEvery: an interval task runs at most this often (each run is a whole claude turn).
const minEvery = 5

// A leader with active supervised agents gets a follow-up every 10 minutes, whatever longer interval the task has.
const (
	leaderOpenEveryMinutes = 10
	leaderOpenEvery        = leaderOpenEveryMinutes * time.Minute
)

// maxEvery: the longest interval, 7 days in minutes (checkTask); anything rarer is a one-off or a daily task.
const maxEvery = 7 * 24 * 60

// doneKeep: a once task that ran stays this long (hidden in the app), so a run re-scheduling itself keeps its
// settings (scheduleOwn).
const doneKeep = 24 * time.Hour

// kind is what the history calls it (the app's "Run history", from before tasks were one list).
func (t Task) kind() string {
	if t.Repeat == repeatOnce {
		return "followup"
	}
	return "routine"
}

// label is how the task is called in what the assistant reads, titles and the snapshot (Chinese, as the prompts).
func (t Task) label() string { return t.labelIn(i18n.Default) }

// labelIn is label for people in l (a push): only an untitled follow-up's name is the hub's own text.
func (t Task) labelIn(l i18n.Lang) string {
	if t.Title != "" {
		return t.Title
	}
	if t.Repeat == repeatOnce {
		return i18n.T(l, "assistant-schedule.followup")
	}
	return clip(oneLine(t.Prompt), 30)
}

// done: a once task that already ran.
func (t Task) done() bool { return t.Repeat == repeatOnce && !t.Enabled && !t.LastRun.IsZero() }

// next is when the task's own rule runs next after `after`; zero when it is off.
func (t Task) next(after time.Time, loc *time.Location) time.Time {
	if !t.Enabled {
		return time.Time{}
	}
	switch t.Repeat {
	case repeatDaily:
		return nextRun(t, after, loc)
	case repeatCron:
		return cronNext(t.Cron, after, loc)
	}
	return t.At
}

// shortForLeader reports whether active supervised work shortens this interval follow-up to 10 minutes.
func shortForLeader(t Task, open map[string]bool) bool {
	return t.Enabled && t.Repeat == repeatInterval && t.SessionID != "" && t.EveryMinutes > leaderOpenEveryMinutes && open[t.SessionID]
}

// nextWithLeaderCadence keeps At on the task's original rule and derives only the earlier runs from the last run.
// A task that has never run uses its creation time. With neither timestamp, it runs now rather than moving
// a now-based deadline forward on every read.
func (t Task) nextWithLeaderCadence(now time.Time, loc *time.Location, open map[string]bool) time.Time {
	next := t.next(now, loc)
	if next.IsZero() || !shortForLeader(t, open) {
		return next
	}
	anchor := t.LastRun
	if anchor.IsZero() {
		anchor = t.CreatedAt
	}
	if anchor.IsZero() {
		if next.After(now) {
			return now
		}
		return next
	}
	if early := anchor.Add(leaderOpenEvery); early.Before(next) {
		return early
	}
	return next
}

// leaderOpenWork returns leaders with an open supervised todo whose agents are working or waiting automatically.
// Read agent sessions outside todos.mu, and take this snapshot before schedule.mu.
func (s *Service) leaderOpenWork(ctx context.Context) map[string]bool {
	cfg := s.settings(ctx)
	leaders := map[string]bool{}
	for _, id := range cfg.Leaders {
		if id != "" && id != cfg.Main {
			leaders[id] = true
		}
	}
	if len(leaders) == 0 {
		return nil
	}
	if err := s.ensureTodosLoaded(ctx); err != nil {
		return nil // a read failure leaves the task's own interval in place for this pass
	}
	var todos []Todo
	s.todos.mu.RLock()
	for _, t := range s.todos.todos {
		if t.DoneAt != nil || t.Supervision == nil || !leaders[t.Supervision.Supervisor] {
			continue
		}
		todos = append(todos, copyTodoLocked(t))
	}
	s.todos.mu.RUnlock()
	open := map[string]bool{}
	for _, t := range todos {
		if open[t.Supervision.Supervisor] {
			continue
		}
		for _, c := range s.taskAgents(ctx, t) {
			if c.State == proto.StateWorking || c.Work != nil && managerWaits(c.Work) {
				open[t.Supervision.Supervisor] = true
				break
			}
		}
	}
	return open
}

// Routine and Followup are the lists from before 2026-10-31, read once to move them into tasks.
type Routine struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Prompt        string    `json:"prompt"`
	Time          string    `json:"time"`
	Weekdays      []int     `json:"weekdays,omitempty"`
	Enabled       bool      `json:"enabled"`
	CreatedBy     string    `json:"created_by,omitempty"`
	LastRun       time.Time `json:"last_run,omitempty"`
	LastSessionID string    `json:"last_session_id,omitempty"`
	LastStatus    string    `json:"last_status,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	Facts         bool      `json:"facts,omitempty"`
}

type Followup struct {
	ID        string    `json:"id"`
	At        time.Time `json:"at"`
	Title     string    `json:"title,omitempty"`
	Prompt    string    `json:"prompt"`
	MachineID string    `json:"machine_id"`
	SessionID string    `json:"session_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ScheduleRun is one entry of the history: a task that ran, failed to start, or was skipped.
type ScheduleRun struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`   // routine / followup (Task.kind)
	RefID     string    `json:"ref_id"` // the task's id
	Title     string    `json:"title"`
	Due       time.Time `json:"due"` // when it was meant to run (the tap for "Run now")
	At        time.Time `json:"at"`  // when it ran
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Manual    bool      `json:"manual,omitempty"` // "Run now"
	MachineID string    `json:"machine_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	// Retried: this run is the automatic retry after a failure (so a retry that fails again does not retry forever).
	Retried bool `json:"retried,omitempty"`
	// NotReady: skipped because a required computer or model was not ready, including temporary offline states.
	// It stays in the history but neither counts toward nor ends a failure streak (schedule_streak.go).
	NotReady bool `json:"not_ready,omitempty"`
	// StreakTold: this run made its task's streak of runs that did not go through long enough to tell the user, who
	// was told then (schedule_streak.go): the rest of that streak is not told again.
	StreakTold bool `json:"streak_told,omitempty"`
	// Ended: the task reached its end time and was turned off. Not a missed run: it does not count toward the
	// streak of runs that did not go through, and it ends that streak (schedule_streak.go).
	Ended bool `json:"ended,omitempty"`
	// of is when the scheduler started this run (the task's LastRun then; "Run now"'s tap): a run that finishes after a
	// newer one started is history only, it does not overwrite the newer one's status (recordRun).
	of time.Time
}

const (
	runOK      = "ok"
	runFailed  = "failed"
	runSkipped = "skipped"
)

// maxRuns: how much history is kept.
const maxRuns = 200

const (
	tasksKey     = "assistant.tasks"
	routinesKey  = "assistant.routines"  // before 2026-10-31
	followupsKey = "assistant.followups" // before 2026-10-31
	runsKey      = "assistant.runs"
)

type schedule struct {
	mu     sync.Mutex
	loaded bool // false after a failed read: nothing is saved over what Postgres has, the next call reads again
	tasks  []Task
	runs   []ScheduleRun // newest last
	wake   chan struct{} // the list changed: scheduleLoop recomputes when to wake up
	// sending: tasks whose run is being sent right now (runTask), so two can't go into one conversation at once.
	sending map[string]bool
	// held: by task, until when its due run waits for the owner (runDue, relay_presence.go runHold); in memory only.
	held map[string]time.Time
	// inflight: runs runDue started and that have not recorded their result yet.
	inflight sync.WaitGroup
	// waitTold: by task, the wait on the user its skip was pushed for (tellWaitingSkip, waitKey): a patrol every 30
	// minutes behind one card is pushed once. Another wait (the old turn answered, then stopped at a new card) is
	// news again; a skip that finds the conversation no longer waiting, a run going in, or the task deleted clears it.
	// In memory only: a hub restart pushes a wait once more.
	waitTold map[string]string
	// limitWait: tests' wait before retrying a run a usage limit stopped (nil: until the reset, limitRetryWait).
	limitWait func(reset, now time.Time) time.Duration
}

// missedRuns returns scheduled failures that have not been followed by a successful run.
// The caller must hold sc.mu. Runs are appended when they end, so compare start times
// across the whole history, using append order only to break equal-time ties.
func (sc *schedule) missedRuns(since time.Time) []ScheduleRun {
	var missed []ScheduleRun
	for i, r := range sc.runs {
		if r.At.Before(since) || (r.Status != runFailed && r.Status != runSkipped) || r.Manual {
			continue
		}
		recovered := false
		for j, o := range sc.runs {
			if o.RefID == r.RefID && o.Status == runOK &&
				(o.At.After(r.At) || (o.At.Equal(r.At) && j > i)) {
				recovered = true
				break
			}
		}
		if !recovered {
			missed = append(missed, r)
		}
	}
	return missed
}

// defaultRoutines are the tasks a new hub starts with, titled in l (the user edits them as their own afterwards).
func defaultRoutines(l i18n.Lang) []Routine {
	weekdays := []int{1, 2, 3, 4, 5}
	return []Routine{
		{ID: "morning", Title: i18n.T(l, "assistant-schedule.morning"), Time: "09:00", Weekdays: weekdays, Enabled: true, Prompt: prompts.RenderMorningRoutine(), Facts: true},
		{ID: "evening", Title: i18n.T(l, "assistant-schedule.evening"), Time: "21:30", Weekdays: weekdays, Enabled: false, Prompt: prompts.RenderEveningRoutine(), Facts: true},
	}
}

// tasksFrom moves the old lists into one, ids, times and last runs kept.
func tasksFrom(routines []Routine, followups []Followup) []Task {
	out := make([]Task, 0, len(routines)+len(followups))
	for _, r := range routines {
		out = append(out, Task{ID: r.ID, Title: r.Title, Prompt: r.Prompt, Repeat: repeatDaily, Time: r.Time, Weekdays: r.Weekdays,
			Enabled: r.Enabled, CreatedBy: r.CreatedBy, LastRun: r.LastRun, LastSessionID: r.LastSessionID, LastStatus: r.LastStatus,
			LastError: r.LastError, Facts: r.Facts})
	}
	for _, f := range followups {
		out = append(out, Task{ID: f.ID, Title: f.Title, Prompt: stripScheduleHeader(f.Prompt), Repeat: repeatOnce, At: f.At, Enabled: true,
			MachineID: f.MachineID, SessionID: f.SessionID, CreatedBy: "assistant", CreatedAt: f.CreatedAt})
	}
	return out
}

// errScheduleUnavailable: the scheduled tasks could not be read from the database yet.
var errScheduleUnavailable = i18n.Err("assistant-schedule.unavailable")

var errScheduleSave = i18n.Err("assistant-schedule.save_failed")

// loadSchedule reads the task list and the run history once. A failed read leaves the schedule unloaded, so the next
// call reads again and saveLocked writes nothing: saving the defaults then would overwrite the stored tasks.
func (s *Service) loadSchedule(ctx context.Context) *schedule {
	sc := &s.sched
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.loaded {
		return sc
	}
	st := s.store()
	if st == nil {
		sc.loaded = true
		sc.tasks = tasksFrom(defaultRoutines(s.lang(ctx)), nil)
		return sc
	}
	var tasks []Task
	ok, err := st.Setting(ctx, tasksKey, &tasks)
	if err != nil {
		s.logf("load tasks: %v", err)
		return sc
	}
	if !ok || tasks == nil {
		// First start with one list: move the routines and follow-ups over, then empty the old follow-ups so an
		// older hub started again can't run them a second time.
		routines := defaultRoutines(s.lang(ctx))
		var rs []Routine
		ok, err := st.Setting(ctx, routinesKey, &rs)
		if err != nil {
			s.logf("load routines: %v", err)
			return sc
		}
		if ok {
			routines = rs
		}
		var fs []Followup
		if _, err := st.Setting(ctx, followupsKey, &fs); err != nil {
			s.logf("load follow-ups: %v", err)
			return sc
		}
		tasks = tasksFrom(routines, fs)
		if err := st.PutSetting(ctx, tasksKey, tasks); err != nil {
			s.logf("save tasks: %v", err)
			return sc
		}
		if len(fs) > 0 {
			if err := st.PutSetting(ctx, followupsKey, []Followup{}); err != nil {
				s.logf("empty the old follow-ups: %v", err)
			}
		}
		s.logf("schedule: %d routines and %d follow-ups moved into one task list", len(routines), len(fs))
	}
	var runs []ScheduleRun
	if _, err := st.Setting(ctx, runsKey, &runs); err != nil {
		s.logf("load schedule runs: %v", err)
		return sc
	}
	sc.tasks, sc.runs, sc.loaded = tasks, runs, true
	upgraded := slices.Clone(sc.tasks)
	if upgradeDefaultRoutines(upgraded) {
		if err := s.saveLocked(sc, upgraded); err == nil {
			s.logf("schedule: the default 早报 / 晚报 prompts brought up to date")
		}
	}
	return sc
}

// upgradeDefaultRoutines gives the default daily tasks (morning, evening) the current default prompt when what they
// hold is byte for byte an earlier default: a hub set up before kept the old one for good, and with it the task's facts
// (the current default prompt writes only a summary on top of them). A prompt the user (or the assistant) changed in any
// way is left alone, and so is the task's facts setting.
func upgradeDefaultRoutines(tasks []Task) bool {
	changed := false
	for i := range tasks {
		t := &tasks[i]
		var cur string
		var former []string
		switch t.ID {
		case "morning":
			cur, former = prompts.RenderMorningRoutine(), prompts.FormerMorningRoutines()
		case "evening":
			cur, former = prompts.RenderEveningRoutine(), prompts.FormerEveningRoutines()
		default:
			continue
		}
		if t.Prompt != cur && slices.Contains(former, t.Prompt) {
			t.Prompt, t.Facts, changed = cur, true, true
		}
	}
	return changed
}

// lockedSchedule returns the schedule locked, or errScheduleUnavailable (and nothing locked) while it could not be
// read: a change made then would only live in memory and be lost on the next successful read, and a view of it would
// show no tasks at all. Writes fail (503 for the app, an error for the assistant's tools), the scheduler skips its turn.
func (s *Service) lockedSchedule(ctx context.Context) (*schedule, error) {
	sc := s.loadSchedule(ctx)
	sc.mu.Lock()
	if !sc.loaded {
		sc.mu.Unlock()
		return nil, errScheduleUnavailable
	}
	return sc, nil
}

// scheduleReadable is lockedSchedule for a view: nil once the schedule was read (it stays read).
func (s *Service) scheduleReadable(ctx context.Context) error {
	sc, err := s.lockedSchedule(ctx)
	if err == nil {
		sc.mu.Unlock()
	}
	return err
}

// saveLocked persists a candidate list before publishing it; sc.mu is held throughout. Callers making a
// reversible change use a separate slice so a failed write leaves the live list untouched. Runtime outcomes
// already observed use sc.tasks itself: a storage outage cannot undo work or hide its result.
func (s *Service) saveLocked(sc *schedule, tasks []Task) error {
	if !sc.loaded {
		return errScheduleUnavailable
	}
	if tasks == nil {
		tasks = []Task{} // null would read as "never moved over"
	}
	if st := s.store(); st != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := st.PutSetting(ctx, tasksKey, tasks); err != nil {
			s.logf("save tasks: %v", err)
			return errScheduleSave
		}
	}
	sc.tasks = tasks
	return nil
}

func (sc *schedule) indexLocked(id string) int {
	return slices.IndexFunc(sc.tasks, func(t Task) bool { return t.ID == id })
}

var hhmm = lazyre.New(`^([01]?\d|2[0-3]):([0-5]\d)$`)

// nextRun is a daily task's first run strictly after `after`.
func nextRun(t Task, after time.Time, loc *time.Location) time.Time {
	m := hhmm.FindStringSubmatch(t.Time)
	if m == nil {
		return time.Time{}
	}
	hh, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	at := after.In(loc)
	for i := 0; i < 8; i++ {
		d := time.Date(at.Year(), at.Month(), at.Day()+i, hh, mm, 0, 0, loc)
		if !d.After(after) {
			continue
		}
		wd := int(d.Weekday())
		if wd == 0 {
			wd = 7
		}
		if len(t.Weekdays) == 0 || slices.Contains(t.Weekdays, wd) {
			return d
		}
	}
	return time.Time{}
}

// relative reads "30m" / "2h" / "1d". A negative day count, or one too large for a time.Duration (it wrapped around:
// 213504d came out as 25 minutes), is not one.
func relative(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if d, err := strconv.Atoi(n); err == nil {
			if d < 0 || int64(d) > math.MaxInt64/int64(24*time.Hour) {
				return 0, false
			}
			return time.Duration(d) * 24 * time.Hour, true
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, true
	}
	return 0, false
}

// parseWhen reads RFC 3339 or a relative "30m" / "2h" / "1d".
func parseWhen(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", s, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, loc); err == nil {
		return t, nil
	}
	if d, ok := relative(s); ok {
		return now.Add(d), nil
	}
	return time.Time{}, fmt.Errorf("can't read time %q: use RFC 3339 (2026-10-27T09:50:00+08:00) or 30m / 2h / 1d", s)
}

// untilIn reads an optional end time from the raw JSON value. Nil (a missing key, or a json.RawMessage field the
// object did not have) leaves whatever is stored (has is false). JSON null clears it.
// A string is parseWhen (RFC 3339, a clock time in loc, or 30m / 2h / 1d) and comes back in UTC.
// An empty string, or a number, object or array, is an error; an unparsable string is parseWhen's error.
func untilIn(v json.RawMessage, now time.Time, loc *time.Location) (at time.Time, has, clear bool, err error) {
	if len(v) == 0 {
		return time.Time{}, false, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return time.Time{}, true, true, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil || strings.TrimSpace(s) == "" {
		return time.Time{}, true, false, errors.New("until must be an RFC 3339 time, or 30m / 2h / 1d")
	}
	at, err = parseWhen(s, now, loc)
	if err != nil {
		return time.Time{}, true, false, err
	}
	return at.UTC(), true, false, nil
}

// applyUntil sets t.Until when the request carried the field. Absent does nothing; null clears it.
func applyUntil(t *Task, at time.Time, has, clear bool) {
	if !has {
		return
	}
	if clear {
		t.Until = time.Time{}
		return
	}
	t.Until = at.UTC()
}

// stopsAt is the reply suffix for a repeating task that has an end time. Empty when it does not, so the
// reply stays what it was.
func stopsAt(t Task, loc *time.Location) string {
	if t.Repeat == repeatOnce || t.Until.IsZero() {
		return ""
	}
	return " Stops at " + t.Until.In(loc).Format("2006-01-02 15:04") + "."
}

// scheduleHeader is the first line of a run's message (taskPrompt, prompts/assistant/task_run.md) and the notes under it. A patrol told to
// "schedule the next one with the same prompt" copies them along; they are dropped so they don't pile up.
var scheduleHeader = lazyre.New(`^(?:⏰[^\n]*（[^\n]*(?:跟进|定时任务)[^\n]*）|（本该[^\n]*）|（(?:Persona|Persona) 会[^\n]*）|（又到时间了[^\n]*）|（这个任务只在你用 notify_user[^\n]*）|（没有变化就立即结束，不做长汇报。）)\s*`)

func stripScheduleHeader(p string) string {
	p = strings.TrimSpace(p)
	for {
		loc := scheduleHeader.FindStringIndex(p)
		if loc == nil {
			return p
		}
		p = strings.TrimSpace(p[loc[1]:])
	}
}

// everyText: "30 minutes" / "2 hours" (in Chinese).
func everyText(minutes int) string {
	if minutes%60 == 0 {
		return fmt.Sprintf("%d 小时", minutes/60)
	}
	return fmt.Sprintf("%d 分钟", minutes)
}

// ruleText is the schedule in words, for the tools' replies and list_followups.
func ruleText(t Task, loc *time.Location) string {
	switch t.Repeat {
	case repeatDaily:
		return weekdayText(t.Weekdays) + " " + t.Time
	case repeatInterval:
		return "every " + everyText(t.EveryMinutes)
	case repeatCron:
		if text := cronText(t.Cron, i18n.En); text != "" {
			return "cron `" + t.Cron + "` (" + text + ")"
		}
		return "cron `" + t.Cron + "`"
	}
	return "once at " + t.At.In(loc).Format("01-02 15:04 Mon")
}

func sameText(a, b string) bool {
	return a != "" && oneLine(stripScheduleHeader(a)) == oneLine(stripScheduleHeader(b))
}

func (s *Service) toolScheduleFollowup(ctx context.Context, machine, sessionID string, args json.RawMessage) (string, error) {
	var a struct {
		At              string          `json:"at"`
		Every           string          `json:"every"`
		Cron            string          `json:"cron"`
		Prompt          string          `json:"prompt"`
		Title           string          `json:"title"`
		NewConversation bool            `json:"new_conversation"`
		Push            *string         `json:"push"` // left out: a new follow-up pushes every run, one rescheduled keeps its push
		Until           json.RawMessage `json:"until"`
		// The task's own model configuration (schedule_model.go): nil leaves it as it is, "" clears that part.
		Launcher *string `json:"launcher"`
		Model    *string `json:"model"`
		Effort   *string `json:"effort"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	var push *string
	if a.Push != nil {
		p, err := pushArg(*a.Push)
		if err != nil {
			return "", err
		}
		push = &p
	}
	prompt := stripScheduleHeader(a.Prompt)
	if prompt == "" {
		return "", errors.New("prompt is required")
	}
	loc, now := s.location(ctx), time.Now()
	var rule cronRule
	if strings.TrimSpace(a.Cron) != "" {
		if a.At != "" || a.Every != "" {
			return "", errors.New("cron replaces at and every: give only one of them")
		}
		var err error
		if rule, err = parseCron(a.Cron); err != nil {
			return "", err
		}
	}
	every := 0
	if a.Every != "" {
		d, ok := relative(a.Every)
		if !ok || d < minEvery*time.Minute || d > maxEvery*time.Minute {
			// The app's range (checkTask): a longer interval saved here could not be saved again there, not even switched off.
			return "", fmt.Errorf("every %q: use a duration from %dm to %dd, like 30m or 2h", a.Every, minEvery, maxEvery/(24*60))
		}
		every = int(d.Minutes())
	}
	var at time.Time
	switch {
	case rule.spec != nil:
		at = rule.next(now, loc)
	case a.At != "":
		var err error
		if at, err = parseWhen(a.At, now, loc); err != nil {
			return "", err
		}
	case every > 0:
		at = now.Add(time.Duration(every) * time.Minute)
	default:
		return "", errors.New("at is required (or every / cron, for something that repeats)")
	}
	if at.Before(now.Add(-time.Minute)) {
		return "", fmt.Errorf("%s is in the past", at.In(loc).Format("2006-01-02 15:04"))
	}
	needsOwn := hasTaskModel(a.Launcher, a.Model, a.Effort)
	// A one-off ignores until, even a bad one. An interval task takes it; scheduleOwn applies it when this run is
	// rescheduling its own task (including a one-off this call turns into an interval).
	if out, id, ok, err := s.scheduleOwn(ctx, sessionID, a.Title, a.Prompt, prompt, a.At, at, every, rule, push, a.Until, a.Launcher, a.Model, a.Effort); ok || err != nil {
		if err == nil && id != "" {
			err = s.setTaskModel(ctx, id, a.Launcher, a.Model, a.Effort)
		}
		return out, err
	}
	space, _ := s.selfSpace(ctx, sessionID) // not listed: the assistant's own, as before the health space
	t := Task{ID: "fu_" + randomHex(4), Title: strings.TrimSpace(a.Title), Prompt: prompt, Repeat: repeatOnce, At: at.UTC(), Enabled: true,
		MachineID: machine, Space: space, CreatedBy: "assistant", CreatedAt: now.UTC()}
	if every > 0 || rule.spec != nil {
		if rule.spec != nil {
			t.Repeat, t.Cron, t.At = repeatCron, rule.expr, time.Time{}
			rearm(Task{}, &t, now)
		} else {
			t.Repeat, t.EveryMinutes = repeatInterval, every
		}
		end, hasUntil, clearUntil, err := untilIn(a.Until, now, loc)
		if err != nil {
			return "", err
		}
		applyUntil(&t, end, hasUntil, clearUntil) // null on a new task leaves it unset
		dropStaleUntil(&t, hasUntil, now)
		if err := checkUntil(&t, now); err != nil {
			return "", err
		}
	}
	if !a.NewConversation {
		t.SessionID = sessionID
	}
	if push != nil {
		t.Push = *push
	}
	ownConversationIfQuiet(&t, s.mayBeMainNow(ctx))
	if t.SessionID != "" && s.quietConversation(ctx, t.SessionID) {
		t.SessionID = "" // asked from a quiet task's conversation: it runs in its own, where its runs are pushed (#2396)
	}
	if needsOwn && t.SessionID != "" { // it would run on that conversation's launcher, not its own
		return "", errTaskModelNeedsOwnConversation
	}
	open := s.leaderOpenWork(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return "", err
	}
	err = s.saveLocked(sc, append(slices.Clone(sc.tasks), t))
	sc.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := s.setTaskModel(ctx, t.ID, a.Launcher, a.Model, a.Effort); err != nil {
		_, _ = s.deleteTask(ctx, t.ID) // a task that cannot run with what was asked is not left behind
		return "", err
	}
	s.broadcastSchedule()
	if rule.spec != nil {
		return fmt.Sprintf("Scheduled %s on %s, the first %s; %s. It repeats by itself: don't schedule the next run from a run.",
			t.ID, cronReply(rule, loc, now), until(at), pushText(t)) + stopsAt(t, loc), nil
	}
	if every > 0 {
		first := t.nextWithLeaderCadence(now, loc, open)
		return fmt.Sprintf("Scheduled %s every %s, first at %s (%s); %s. It repeats by itself: don't schedule the next run from a run.",
			t.ID, everyText(every), first.In(loc).Format("01-02 15:04 Mon"), until(first), pushText(t)) + stopsAt(t, loc), nil
	}
	return fmt.Sprintf("Scheduled %s for %s (%s); %s.", t.ID, at.In(loc).Format("01-02 15:04 Mon"), until(at), pushText(t)), nil
}

// scheduleOwn handles a run scheduling its own next run ("finally use schedule_followup (at=30m, same prompt as this one)
// to schedule the next one"): the same task is kept, with the launcher and permission mode the user picked for it, instead of a new
// follow-up per run. A one-off rescheduled by a relative time becomes a task every that long; one that already
// repeats stays as it is (a changed prompt is taken). A task deleted while its run was going stays deleted: the run is
// told so, nothing is scheduled (#4792). A one-off given a cron rule (`cron`, zero spec for none) runs on it from now on. ok is false when this conversation's latest run isn't one of the task's
// (rawPrompt is what was asked, before stripScheduleHeader), or the title and prompt are another task's.
func (s *Service) scheduleOwn(ctx context.Context, sessionID, title, rawPrompt, prompt, atArg string, at time.Time, every int, cron cronRule, push *string, untilRaw json.RawMessage, launcher, model, effort *string) (string, string, bool, error) {
	if sessionID == "" {
		return "", "", false, nil
	}
	loc, mayBeMain := s.location(ctx), s.mayBeMainNow(ctx)
	open := s.leaderOpenWork(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return "", "", false, err
	}
	// Only the conversation's latest run says which task it is a run of: an older run's task is not this one (#4792).
	last := -1
	for j := len(sc.runs) - 1; j >= 0 && last < 0; j-- {
		if sc.runs[j].SessionID == sessionID {
			last = j
		}
	}
	if last < 0 {
		sc.mu.Unlock()
		return "", "", false, nil
	}
	r := sc.runs[last]
	i := sc.indexLocked(r.RefID)
	if i < 0 {
		// Deleted while this run was going: its "schedule the next one" must not bring it back (#4792).
		sc.mu.Unlock()
		if ranRecently(r) && copiesRun(r, title, rawPrompt) {
			return i18n.T(s.lang(ctx), "assistant-schedule.deleted_not_again", i18n.Args{"id": r.RefID, "name": r.Title}), "", true, nil
		}
		return "", "", false, nil
	}
	if !(sameText(title, sc.tasks[i].Title) || sameText(prompt, sc.tasks[i].Prompt)) {
		sc.mu.Unlock()
		return "", "", false, nil
	}
	id, sourceSession := sc.tasks[i].ID, sc.tasks[i].SessionID
	if hasTaskModel(launcher, model, effort) {
		// A model configuration is checked before the schedule changes (the model configuration reads the task list,
		// so the schedule lock is not held here).
		sc.mu.Unlock()
		if sourceSession != "" { // its runs keep that conversation's launcher
			return "", id, true, errTaskModelNeedsOwnConversation
		}
		if err := s.validateTaskModel(ctx, id, launcher, model, effort); err != nil {
			return "", id, true, err
		}
		sc.mu.Lock()
		if i = sc.indexLocked(id); i < 0 {
			sc.mu.Unlock()
			return "", "", false, nil
		}
	}
	tasks := slices.Clone(sc.tasks)
	t := &tasks[i]
	if title = strings.TrimSpace(title); title != "" {
		t.Title = title
	}
	t.Prompt = prompt
	if push != nil { // left out: it keeps its push
		t.Push = *push
	}
	ownConversationIfQuiet(t, mayBeMain)
	var out string
	switch {
	case t.Repeat != repeatOnce && !t.Enabled:
		out = fmt.Sprintf("%s「%s」 is a repeating task the user turned off; it stays off (they can turn it on in the app).", t.ID, t.label())
	case t.Repeat != repeatOnce:
		next := t.nextWithLeaderCadence(time.Now(), loc, open)
		out = fmt.Sprintf("%s「%s」 already repeats (%s); next run %s (%s). Kept its prompt up to date. Don't schedule its next run yourself.",
			t.ID, t.label(), ruleText(*t, loc), next.In(loc).Format("01-02 15:04 Mon"), until(next))
	default:
		// Only within the app's range (checkTask); a longer one stays a one-off.
		if d, ok := relative(atArg); every == 0 && cron.spec == nil && ok && d >= minEvery*time.Minute && d <= maxEvery*time.Minute {
			every = int(d.Minutes())
		}
		t.At, t.Enabled = at.UTC(), true
		switch {
		case cron.spec != nil:
			t.Repeat, t.Cron, t.At = repeatCron, cron.expr, time.Time{}
			rearm(Task{}, t, time.Now())
			out = fmt.Sprintf("%s「%s」 now runs by itself on %s; the user sees it in Persona › 定时任务. "+ // i18n-ignore: model-facing, names the app's page as the replies beside it do
				"Don't schedule its next run from now on.", t.ID, t.label(), cronReply(cron, loc, time.Now()))
		case every > 0:
			t.Repeat, t.EveryMinutes = repeatInterval, every
			next := t.nextWithLeaderCadence(time.Now(), loc, open)
			out = fmt.Sprintf("%s「%s」 now repeats every %s by itself (next %s, %s); the user sees it in Persona › 定时任务. "+
				"Don't schedule its next run from now on.", t.ID, t.label(), everyText(every), next.In(loc).Format("01-02 15:04 Mon"), until(next))
		default:
			out = fmt.Sprintf("Rescheduled %s「%s」 for %s (%s).", t.ID, t.label(), at.In(loc).Format("01-02 15:04 Mon"), until(at))
		}
	}
	// until applies only once the task is repeating. A one-off ignores it, including a bad value; checkUntil
	// clears it. Absent keeps the stored end time; null clears it. A past end time on an enabled task fails
	// before anything is saved.
	if t.Repeat != repeatOnce {
		end, hasUntil, clearUntil, untilErr := untilIn(untilRaw, time.Now(), loc)
		if untilErr != nil {
			sc.mu.Unlock()
			return "", t.ID, true, untilErr
		}
		applyUntil(t, end, hasUntil, clearUntil)
		dropStaleUntil(t, hasUntil, time.Now())
	}
	if err := checkUntil(t, time.Now()); err != nil {
		sc.mu.Unlock()
		return "", t.ID, true, err
	}
	out += stopsAt(*t, loc)
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		return "", t.ID, true, err
	}
	s.broadcastSchedule()
	return out, t.ID, true, nil
}

// ranRecently: the run started within the time its turn is followed (runWatchTimeout), so the call can be its own end.
// A deleted task's run long ago is not; asking for the same thing again later is the user's call.
func ranRecently(r ScheduleRun) bool {
	return r.At.IsZero() || time.Since(r.At) < runWatchTimeout
}

// copiesRun: what was asked is that run's task again, by its title, or (no title) by the run's own message copied
// along (its first line "⏰ <name> (…)", taskPrompt). Only the run's name is left of a deleted task, so a bare prompt
// without that line is taken as something new (#4792).
func copiesRun(r ScheduleRun, title, rawPrompt string) bool {
	if r.Title == "" {
		return false
	}
	if strings.TrimSpace(title) != "" {
		return sameText(title, r.Title)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(rawPrompt), "\n")
	return scheduleHeader.MatchString(first) && strings.HasPrefix(first, "⏰ "+r.Title+"（")
}

func until(t time.Time) string {
	d := time.Until(t).Round(time.Minute)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("in %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %.1f h", d.Hours())
	}
	return fmt.Sprintf("in %d days", int(d.Hours()/24))
}

// taskInSpace: task id is one of space's (a missing one is too: deleting it then says there is none).
func (s *Service) taskInSpace(ctx context.Context, id, space string) bool {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return true // deleteTask reports it
	}
	defer sc.mu.Unlock()
	i := sc.indexLocked(id)
	return i < 0 || sc.tasks[i].Space == space
}

// deleteTask removes a task (cancel_followup, delete_routine, the app); false when there is none.
func (s *Service) deleteTask(ctx context.Context, id string) (bool, error) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return false, err
	}
	i := sc.indexLocked(id)
	if i >= 0 {
		if err := s.keepWorkoutReceipt(ctx, sc.tasks[i]); err != nil {
			sc.mu.Unlock()
			return false, err
		}
		tasks := slices.Delete(slices.Clone(sc.tasks), i, i+1)
		if err := s.saveLocked(sc, tasks); err != nil {
			sc.mu.Unlock()
			return false, err
		}
	}
	delete(sc.waitTold, id)
	sc.mu.Unlock()
	if i >= 0 {
		s.forgetTaskModel(ctx, id)
		s.broadcastSchedule()
	}
	return i >= 0, nil
}

// describeSchedule lists the tasks of space ("" the assistant's own, "health"): a conversation sees and changes only
// its own space's (docs/assistant/health.md).
func (s *Service) describeSchedule(ctx context.Context, space string) string {
	loc := s.location(ctx)
	now := time.Now()
	open := s.leaderOpenWork(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return err.Error()
	}
	defer sc.mu.Unlock()
	var b strings.Builder
	b.WriteString("Scheduled tasks (the user sees and edits them in Persona › 定时任务; permission mode is theirs to set; a launcher / model / effort is the task's own when it has one, Main chat's otherwise):\n")
	n := 0
	for _, t := range sc.tasks {
		if t.done() || t.Space != space {
			continue
		}
		n++
		fmt.Fprintf(&b, "- %s 「%s」: %s", t.ID, t.label(), ruleText(t, loc))
		if !t.Enabled {
			b.WriteString(", off")
		} else if next := t.nextWithLeaderCadence(now, loc, open); !next.IsZero() && t.Repeat != repeatOnce {
			b.WriteString(", next " + next.In(loc).Format("01-02 15:04 Mon"))
		}
		switch {
		case t.SessionID != "":
			b.WriteString(", into conversation " + short(t.SessionID))
		case t.RunSessionID != "":
			b.WriteString(", in its own conversation " + short(t.RunSessionID))
		default:
			b.WriteString(", its own conversation starts on the next run")
		}
		if t.pushesOnlyAsked() {
			b.WriteString(", " + pushText(t))
		}
		if t.LastStatus != "" {
			fmt.Fprintf(&b, ", last run %s %s", t.LastRun.In(loc).Format("01-02 15:04"), t.LastStatus)
		}
		if t.Repeat != repeatOnce && !t.Until.IsZero() {
			b.WriteString(", until " + t.Until.In(loc).Format("2006-01-02 15:04"))
		}
		b.WriteString("\n")
	}
	if n == 0 {
		b.WriteString("- (none)\n")
	}
	return b.String()
}

// toolScheduleRoutine adds a daily task from a conversation ("give me a morning briefing at 9 every day"), or changes one by id.
func (s *Service) toolScheduleRoutine(ctx context.Context, self string, args json.RawMessage) (string, error) {
	var a struct {
		ID       string          `json:"id"`
		Title    string          `json:"title"`
		Prompt   string          `json:"prompt"`
		Time     string          `json:"time"`
		Weekdays []int           `json:"weekdays"`
		Cron     string          `json:"cron"`
		Enabled  *bool           `json:"enabled"`
		Report   *bool           `json:"report"` // left out: kept
		Push     *string         `json:"push"`   // left out: a new routine pushes every run, an existing one keeps its push
		Until    json.RawMessage `json:"until"`
		// The task's own model configuration (schedule_model.go): nil leaves it as it is, "" clears that part.
		Launcher *string `json:"launcher"`
		Model    *string `json:"model"`
		Effort   *string `json:"effort"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	a.Cron = strings.TrimSpace(a.Cron)
	if a.Cron != "" && (a.Time != "" || a.Weekdays != nil) {
		return "", errors.New("cron replaces time and weekdays: give either cron, or time (and weekdays)")
	}
	space, _ := s.selfSpace(ctx, self) // a new routine runs in the space it was asked from (docs/assistant/health.md)
	var push *string
	if a.Push != nil {
		p, err := pushArg(*a.Push)
		if err != nil {
			return "", err
		}
		push = &p
	}
	loc := s.location(ctx)
	now := time.Now()
	end, hasUntil, clearUntil, err := untilIn(a.Until, now, loc)
	if err != nil {
		return "", err
	}
	needsOwn := hasTaskModel(a.Launcher, a.Model, a.Effort)
	if a.ID != "" && needsOwn {
		// Check before the save: a bad launcher must not leave the rest of the change applied.
		if prev, ok := s.taskView(ctx, a.ID); ok && prev.Space == space {
			if prev.SessionID != "" {
				return "", errTaskModelNeedsOwnConversation
			}
			if err := s.validateTaskModel(ctx, a.ID, a.Launcher, a.Model, a.Effort); err != nil {
				return "", err
			}
		}
	}
	mayBeMain := s.mayBeMainNow(ctx)
	open := s.leaderOpenWork(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return "", err
	}
	var t, old Task
	i := -1
	if a.ID != "" {
		if i = sc.indexLocked(a.ID); i < 0 || sc.tasks[i].Space != space {
			sc.mu.Unlock()
			return "", fmt.Errorf("no task %q (list_followups lists them)", a.ID)
		}
		t = sc.tasks[i]
		old = t
	} else {
		t = Task{ID: "rt_" + randomHex(4), Repeat: repeatDaily, Enabled: true, Space: space, CreatedBy: "assistant", CreatedAt: time.Now().UTC()}
	}
	if a.Title != "" {
		t.Title = strings.TrimSpace(a.Title)
	}
	if a.Prompt != "" {
		t.Prompt = stripScheduleHeader(a.Prompt)
	}
	if a.Time != "" || a.Weekdays != nil {
		t.Repeat = repeatDaily
	}
	if a.Time != "" {
		t.Time = strings.TrimSpace(a.Time)
	}
	if a.Weekdays != nil {
		t.Weekdays = a.Weekdays
	}
	if a.Cron != "" {
		t.Repeat, t.Cron = repeatCron, a.Cron
	}
	if a.Enabled != nil {
		t.Enabled = *a.Enabled
	}
	if a.Report != nil {
		t.Report = *a.Report
	}
	if push != nil {
		t.Push = *push
	}
	applyUntil(&t, end, hasUntil, clearUntil)
	dropStaleUntil(&t, hasUntil, now)
	ownConversationIfQuiet(&t, mayBeMain)
	if err := checkTask(&t, now); err != nil {
		sc.mu.Unlock()
		return "", err
	}
	rearm(old, &t, now)
	tasks := slices.Clone(sc.tasks)
	if i < 0 {
		tasks = append(tasks, t)
	} else {
		tasks[i] = t
	}
	err = s.saveLocked(sc, tasks)
	sc.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := s.setTaskModel(ctx, t.ID, a.Launcher, a.Model, a.Effort); err != nil {
		if i < 0 {
			_, _ = s.deleteTask(ctx, t.ID) // a task that cannot run with what was asked is not left behind
		}
		return "", err
	}
	s.broadcastSchedule()
	state := "off"
	if next := t.nextWithLeaderCadence(time.Now(), loc, open); !next.IsZero() {
		state = "next run " + next.In(loc).Format("01-02 15:04 Mon") + " (" + until(next) + ")"
	}
	rule := ruleText(t, loc)
	if t.Repeat == repeatCron && t.Enabled {
		if r, err := parseCron(t.Cron); err == nil {
			rule = cronReply(r, loc, time.Now()) // its next runs, to check against what was asked
		}
	}
	return fmt.Sprintf("Saved %s「%s」: %s, %s; %s. The user sees and edits it in the app (Persona › 定时任务).",
		t.ID, t.label(), rule, state, pushText(t)) + stopsAt(t, loc), nil
}

var launcherName = lazyre.New(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// checkTask validates what the app or a tool set and tidies it (weekdays sorted, every day = none); the messages
// are for the app.
func checkTask(t *Task, now time.Time) error {
	t.Title, t.Prompt = strings.TrimSpace(t.Title), strings.TrimSpace(t.Prompt)
	if t.Prompt == "" {
		return i18n.Err("assistant-schedule.prompt_empty")
	}
	if t.Repeat != repeatCron {
		t.Cron = ""
	}
	switch t.Repeat {
	case repeatDaily:
		if !hhmm.MatchString(t.Time) {
			return i18n.Err("assistant-schedule.time_format", i18n.Args{"time": strconv.Quote(t.Time)})
		}
		if t.Title == "" {
			return i18n.Err("assistant-schedule.daily_title")
		}
		var days []int
		for _, d := range t.Weekdays {
			if d < 1 || d > 7 {
				return fmt.Errorf("weekday %d: use 1 = Monday … 7 = Sunday", d)
			}
			if !slices.Contains(days, d) {
				days = append(days, d)
			}
		}
		sort.Ints(days)
		if len(days) == 7 {
			days = nil
		}
		t.Weekdays, t.At, t.EveryMinutes = days, time.Time{}, 0
	case repeatInterval:
		if t.EveryMinutes < minEvery || t.EveryMinutes > maxEvery {
			return i18n.Err("assistant-schedule.interval_range", i18n.Args{"min": minEvery})
		}
		if t.At.IsZero() {
			t.At = now.Add(time.Duration(t.EveryMinutes) * time.Minute)
		}
		t.Time, t.Weekdays = "", nil
	case repeatOnce:
		if t.At.IsZero() {
			return i18n.Err("assistant-schedule.pick_time")
		}
		if t.Enabled && t.At.Before(now.Add(-time.Minute)) {
			return i18n.Err("assistant-schedule.time_passed")
		}
		t.Time, t.Weekdays, t.EveryMinutes = "", nil, 0
	case repeatCron:
		r, err := parseCron(t.Cron)
		if err != nil {
			return err
		}
		t.Cron, t.Time, t.Weekdays, t.At, t.EveryMinutes = r.expr, "", nil, time.Time{}, 0
	default:
		return fmt.Errorf("unknown repeat %q (once / daily / interval / cron)", t.Repeat)
	}
	if err := checkPush(t.Push); err != nil {
		return err
	}
	if t.Launcher != "" && !launcherName.MatchString(t.Launcher) {
		return i18n.Err("assistant-schedule.bad_launcher", i18n.Args{"launcher": strconv.Quote(t.Launcher)})
	}
	if t.PermissionMode != "" && !slices.Contains(proto.PermissionModes, t.PermissionMode) {
		return i18n.Err("assistant-schedule.bad_permission_mode", i18n.Args{"mode": strconv.Quote(t.PermissionMode)})
	}
	if t.Effort != "" && !slices.Contains(proto.EffortLevels, t.Effort) {
		return i18n.Err("assistant-schedule.bad_effort", i18n.Args{"effort": strconv.Quote(t.Effort)})
	}
	return checkUntil(t, now)
}

// checkUntil is the end-time rule. A one-off drops it. An enabled daily or interval task cannot be saved
// with an end time more than a minute ago (the same grace as a one-off's at). A disabled task may keep a past one.
func checkUntil(t *Task, now time.Time) error {
	if t.Repeat == repeatOnce {
		t.Until = time.Time{}
		return nil
	}
	if t.Until.IsZero() {
		return nil
	}
	t.Until = t.Until.UTC()
	if t.Enabled && t.Until.Before(now.Add(-time.Minute)) {
		return i18n.Err("assistant-schedule.until_passed")
	}
	return nil
}

// dropStaleUntil forgets an end time that has already passed when this save did not mention it. The list switch and a
// client that does not know the field omit until; keeping a past one would make turning the task back on fail.
// An explicit past end time is still refused by checkUntil.
func dropStaleUntil(t *Task, sent bool, now time.Time) {
	if sent || !t.Enabled || t.Repeat == repeatOnce || t.Until.IsZero() {
		return
	}
	if t.Until.Before(now.Add(-time.Minute)) {
		t.Until = time.Time{}
	}
}

func weekdayText(ds []int) string {
	if len(ds) == 0 || len(ds) == 7 {
		return "每天"
	}
	if len(ds) == 5 && !slices.Contains(ds, 6) && !slices.Contains(ds, 7) {
		return "工作日"
	}
	names := []string{"", "一", "二", "三", "四", "五", "六", "日"}
	var b strings.Builder
	b.WriteString("周")
	for _, d := range ds {
		if d >= 1 && d <= 7 {
			b.WriteString(names[d])
		}
	}
	return b.String()
}

// Run runs what the assistant does on its own until ctx ends: the scheduled tasks, closing finished tasks' agents, the
// knowledge base.
func (s *Service) Run(ctx context.Context) {
	s.running.Store(&ctx)
	recoverCtx, cancel := context.WithTimeout(ctx, taskFailTimeout)
	if err := s.recoverTaskStarts(recoverCtx, s.tasks.recoverBefore); err != nil {
		s.logf("recover interrupted task starts: %v", err)
	}
	cancel()
	go s.taskCloseLoop(ctx)    // the agents of finished tasks (tasks_close.go)
	go s.superviseLoop(ctx)    // the tasks the main conversation follows up (supervise_signals.go)
	go s.workLoop(ctx)         // due wakes and retries, cards past their day, missed turns (agents_work_tick.go)
	go s.replyLoop(ctx)        // questions the user looked at and left unanswered (agents_reply.go)
	go s.nudgeLoop(ctx)        // the Lark bot's cards left unanswered (feishu_nudge.go)
	go s.sourceReportLoop(ctx) // the reports back to where a todo was asked (todo_source.go)
	go s.remindLoop(ctx)       // the todos' reminders (todo_plan.go)
	go s.channelLoop(ctx)      // the channels' runs that lost their watcher (channels.go)
	go s.kbLoop(ctx)           // the knowledge base (kb.go)
	go s.kbHistoryLoop(ctx)    // importing chats' older history into it, off by default (kb_history.go)
	go s.viewActionLoop(ctx)   // durable card reply outbox
	go s.acceptLoop(ctx)       // "Please verify" cards that did not go out, and reminders each morning (acceptance.go)
	// The leaders' tasks that got stuck (leader_reports.go).
	go s.leaderReportsLoop(ctx)
	go s.healthLoop(ctx)       // each machine's launchers (launcher_health.go)
	go s.instructionsLoop(ctx) // the CLAUDE.md on the assistant's machine (start.go)
	s.scheduleLoop(ctx)
}

// scheduleLoop starts due tasks. It sleeps until the next one is due (at most scheduleIdle, so a time zone change is
// picked up) and wakes early when the list changes; the old 20-second poll ran follow-ups up to 20 seconds late
// (measured: 21 s on 2026-10-30).
func (s *Service) scheduleLoop(ctx context.Context) {
	wake := s.sched.wakeup()
	for {
		now := time.Now()
		wait := scheduleIdle
		// A mesh member's tasks wait while the center runs the group's (docs/server/mesh.md). Nothing here moves a past
		// one-off or interval task on, so its time stays past: a member only checks again at the idle wake-up (or
		// when the list changes), not every 50 ms.
		if !s.hello(ctx).Assistant.Standby {
			s.runDue(ctx, now)
			if next := s.nextDue(ctx, now); !next.IsZero() && next.Sub(now) < wait {
				wait = max(next.Sub(now), 50*time.Millisecond)
				if !next.After(now) {
					wait = time.Second // runDue moves a due task on; one still past could not run (its list was not read): retry, not spin
				}
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-wake:
			t.Stop()
		case <-t.C:
		}
	}
}

const scheduleIdle = 30 * time.Second

func (sc *schedule) wakeup() chan struct{} {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.wake == nil {
		sc.wake = make(chan struct{}, 1)
	}
	return sc.wake
}

// nextDue is the earliest moment an enabled task is due; zero when nothing is scheduled.
func (s *Service) nextDue(ctx context.Context, now time.Time) time.Time {
	loc := s.location(ctx)
	open := s.leaderOpenWork(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return time.Time{} // read again on the next idle wake-up
	}
	defer sc.mu.Unlock()
	var next time.Time
	for _, t := range sc.tasks {
		n := t.nextWithLeaderCadence(now, loc, open)
		if until, ok := sc.held[t.ID]; ok && until.After(now) {
			n = until // held for the owner (runDue): looked at again when the hold ends
		}
		if !n.IsZero() && (next.IsZero() || n.Before(next)) {
			next = n
		}
	}
	return next
}

// dailyCatchUp is how late a daily task still runs (the hub was down at its time); later it is skipped and recorded.
const dailyCatchUp = 30 * time.Minute

type dueTask struct {
	t   Task
	due time.Time
}

func (s *Service) runDue(ctx context.Context, now time.Time) {
	loc := s.location(ctx)
	open := s.leaderOpenWork(ctx)
	present := s.ownersPresent(now) // read before schedule.mu
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return // this turn is skipped: a task run now could not be marked as run
	}
	// A run that goes into a conversation the owner is talking in waits (relay_presence.go runHold): it stays due,
	// nothing of it is consumed or recorded, and nextDue looks again when the hold ends.
	held := map[string]time.Time{}
	waits := func(t *Task, due time.Time) bool {
		h := runHold(present, *t, due, now)
		if h > 0 {
			held[t.ID] = now.Add(h)
		}
		return h > 0
	}
	var due []dueTask
	var skipped []ScheduleRun
	changed := false
	tasks := slices.Clone(sc.tasks)
	for i := range tasks {
		t := &tasks[i]
		if !t.Enabled {
			continue
		}
		// End time reached: turn it off and record a skip. Do not run, and do not move At.
		if t.Repeat != repeatOnce && !t.Until.IsZero() && !now.Before(t.Until) {
			reason := i18n.T(s.lang(ctx), "assistant-schedule.until")
			t.Enabled = false
			t.LastRun = now.UTC()
			t.LastStatus, t.LastError = runSkipped, reason
			skipped = append(skipped, ScheduleRun{Kind: t.kind(), RefID: t.ID, Title: t.label(), Due: t.Until.UTC(), At: now.UTC(),
				Status: runSkipped, Error: reason, Ended: true, of: now.UTC()})
			changed = true
			continue
		}
		switch t.Repeat {
		case repeatDaily:
			last := t.LastRun
			switch {
			case last.IsZero():
				last = now.Add(-2 * time.Minute) // never ran: only the last couple of minutes (#664)
			case now.Sub(last) > 36*time.Hour:
				// The hub was down, or the task skips days: Friday's morning briefing is 72 hours before Monday's, a weekly one 168.
				// Looking back only two minutes lost Monday's run (nothing run, nothing recorded) whenever the hub came
				// up a few minutes late. Looking back a day treats it as any other day: run within the catch-up
				// window, else skipped and recorded (#3397).
				last = now.Add(-24 * time.Hour)
			}
			if t.ArmedAt.After(last) {
				last = t.ArmedAt // switched on or retimed since: a run due before that was not missed (#3816)
			}
			next := nextRun(*t, last, loc)
			if next.IsZero() || next.After(now) {
				continue
			}
			// One that never ran is only looked back for two minutes (above): waiting would lose its first run.
			if !t.LastRun.IsZero() && waits(t, next) {
				continue
			}
			t.LastRun, changed = now.UTC(), true // saved before it starts: a restart in between loses the run rather than repeating it
			if now.Sub(next) < dailyCatchUp {
				due = append(due, dueTask{*t, next})
			} else { // missed by a long hub outage: skip rather than send a stale morning briefing at noon
				t.LastStatus, t.LastError = runSkipped, i18n.T(s.lang(ctx), "assistant-schedule.missed")
				skipped = append(skipped, ScheduleRun{Kind: t.kind(), RefID: t.ID, Title: t.label(), Due: next.UTC(), At: now.UTC(),
					Status: runSkipped, Error: t.LastError, of: now.UTC()})
			}
		case repeatCron:
			rule, err := parseCron(t.Cron)
			if err != nil {
				continue // checkTask refuses it; one stored before a stricter check never runs
			}
			last := t.LastRun
			switch {
			case last.IsZero():
				last = now.Add(-2 * time.Minute) // never ran: only the last couple of minutes, as a daily one
			case now.Sub(last) > 36*time.Hour:
				last = now.Add(-36 * time.Hour) // a monthly one ran a month ago: what is older than this is past catching up
			}
			if t.ArmedAt.After(last) {
				last = t.ArmedAt // switched on or given another expression since: a time before that was not missed
			}
			next := rule.next(last, loc)
			if next.IsZero() || next.After(now) {
				continue
			}
			if !t.LastRun.IsZero() && waits(t, next) { // as a daily one
				continue
			}
			// The times missed while the hub was down count once, as the latest of them: a run every 30 minutes
			// back after an hour runs once now rather than skip.
			for n := rule.next(next, loc); !n.IsZero() && !n.After(now); n = rule.next(n, loc) {
				next = n
			}
			t.LastRun, changed = now.UTC(), true // saved before it starts, as a daily one
			if now.Sub(next) < dailyCatchUp {
				due = append(due, dueTask{*t, next})
			} else {
				t.LastStatus, t.LastError = runSkipped, i18n.T(s.lang(ctx), "assistant-schedule.missed")
				skipped = append(skipped, ScheduleRun{Kind: t.kind(), RefID: t.ID, Title: t.label(), Due: next.UTC(), At: now.UTC(),
					Status: runSkipped, Error: t.LastError, of: now.UTC()})
			}
		case repeatOnce:
			if t.At.After(now) || waits(t, t.At) {
				continue
			}
			due = append(due, dueTask{*t, t.At})
			t.LastRun, t.Enabled, changed = now.UTC(), false, true
		case repeatInterval:
			next := t.nextWithLeaderCadence(now, loc, open)
			if next.After(now) || waits(t, next) {
				continue
			}
			dueAt := next
			if t.At.After(now) {
				// This is an extra run before the task's original At. Keep that anchor so closing the
				// last supervised todo restores the original interval without one more shortened run.
				// Due is now: activation can make an old LastRun overdue without a hub outage.
				dueAt = now
			}
			due = append(due, dueTask{*t, dueAt})
			t.LastRun, changed = now.UTC(), true
			if !t.At.After(now) {
				if t.At = t.At.Add(time.Duration(t.EveryMinutes) * time.Minute); !t.At.After(now) {
					t.At = now.Add(time.Duration(t.EveryMinutes) * time.Minute).UTC() // missed several: once now, then on from here
				}
			}
		}
	}
	sc.held = held
	// A one-off that ran a day ago is only history now.
	kept := tasks[:0:0]
	for _, t := range tasks {
		if t.done() && now.Sub(t.LastRun) > doneKeep && s.keepWorkoutReceipt(ctx, t) == nil {
			changed = true
			continue
		}
		kept = append(kept, t)
	}
	if changed {
		if err := s.saveLocked(sc, kept); err != nil {
			sc.mu.Unlock()
			return // none of this batch was consumed: leave it due for the next turn
		}
	}
	sc.mu.Unlock()
	if !changed {
		return
	}
	for _, r := range skipped {
		s.recordRun(r)
	}
	s.broadcastSchedule()
	for _, d := range due {
		sc.inflight.Add(1)
		go func() {
			defer sc.inflight.Done()
			_, _ = s.runTask(d.t, d.due, now, false, false)
		}()
	}
}

// recordRun appends to the history, and sets the task's last status unless a newer run has started since this one
// (r.of before the task's LastRun): runs record when they end, and a slow one — an assistant that takes a minute to
// give up — must not overwrite the status of the run or skip that came after it (#556).
func (s *Service) recordRun(r ScheduleRun) string {
	r.ID = "run_" + randomHex(4)
	sc, err := s.lockedSchedule(context.Background())
	if err != nil {
		s.logf("schedule run %s %s not recorded: %v", r.Kind, r.RefID, err)
		return ""
	}
	sc.runs = append(sc.runs, r)
	if len(sc.runs) > maxRuns {
		sc.runs = append([]ScheduleRun(nil), sc.runs[len(sc.runs)-maxRuns:]...)
	}
	if i := sc.indexLocked(r.RefID); i >= 0 && (r.of.IsZero() || !sc.tasks[i].LastRun.After(r.of)) {
		t := &sc.tasks[i]
		t.LastStatus, t.LastError = r.Status, r.Error
		if r.SessionID != "" {
			t.LastSessionID = r.SessionID
		}
	}
	streakTask, streakN, streak := streakToldLocked(sc, len(sc.runs)-1, false) // told once per streak (schedule_streak.go)
	r = sc.runs[len(sc.runs)-1]
	// Work already happened. Keep its outcome and the run ID even when either write fails, so the watcher
	// can still report the real result. Later writes retry the complete current lists under this lock.
	_ = s.saveLocked(sc, sc.tasks)
	if st := s.store(); st != nil { // under the lock: two runs finishing together must not save their lists out of order
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := st.PutSetting(ctx, runsKey, sc.runs); err != nil {
			s.logf("save schedule runs: %v", err)
		}
		cancel()
	}
	sc.mu.Unlock()
	s.broadcastSchedule()
	if streak {
		s.pushStreak(streakTask, streakN, r)
	}
	return r.ID
}

// updateRun rewrites a recorded run's outcome (a run that was sent but whose turn ended in an API error) and the
// task's last status when this run is still the newest.
// updateRun returns whether it pushed the streak notice (#4658), which then stands for the run's own failure push;
// false when the notice was left out (a muted conversation, #4785), so that push still goes.
func (s *Service) updateRun(r ScheduleRun, status, errText string) bool {
	sc, err := s.lockedSchedule(context.Background())
	if err != nil {
		return false
	}
	i := slices.IndexFunc(sc.runs, func(x ScheduleRun) bool { return x.ID == r.ID })
	if i < 0 {
		sc.mu.Unlock()
		return false
	}
	wasOK := sc.runs[i].Status == runOK
	sc.runs[i].Status, sc.runs[i].Error = status, errText
	if j := sc.indexLocked(r.RefID); j >= 0 && (r.of.IsZero() || !sc.tasks[j].LastRun.After(r.of)) {
		sc.tasks[j].LastStatus, sc.tasks[j].LastError = status, errText
	}
	streakTask, streakN, streak := streakToldLocked(sc, i, wasOK) // a turn that ends in an error is a failed run too (schedule_streak.go)
	streakRun := sc.runs[i]
	_ = s.saveLocked(sc, sc.tasks)
	if st := s.store(); st != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := st.PutSetting(ctx, runsKey, sc.runs); err != nil {
			s.logf("save schedule runs: %v", err)
		}
		cancel()
	}
	sc.mu.Unlock()
	s.broadcastSchedule()
	return streak && s.pushStreak(streakTask, streakN, streakRun)
}

// errTaskBusy: the task's last run is still going; this one is skipped (and recorded) rather than queued behind it.
var errTaskBusy = i18n.Err("assistant-schedule.busy_skipped")

// runTask sends the task's message into the conversation it came from (on that conversation's launcher, as the phone's
// messages are), else into its own conversation, else (none yet, or that one is gone, busy elsewhere, or on another
// launcher / machine) a new one that becomes its own; those two on Model configuration › Main chat (#1544), with the task's
// permission mode. due: when it was meant to run; now: when the scheduler noticed (a run that
// waited for the hub to come back is told how late it is); manual is "Run now" in the app.
//
// A repeating task whose own conversation is still on its last run (working, or waiting on the user) is skipped,
// not queued: two runs stacked in one conversation answer twice in a row, and the second one only sees the first
// half done. The skip is in the history like a missed one. A one-off still goes in (it would be lost otherwise).
func taskMessageSource(t Task) *proto.MessageSource {
	kind := proto.SourcePatrol
	if t.Repeat == repeatOnce {
		kind = proto.SourceFollowup
	}
	return &proto.MessageSource{Kind: kind, ConversationID: t.SessionID, Title: t.label(), Push: t.Push}
}

func (s *Service) runTask(t Task, due, now time.Time, manual, retried bool) (proto.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if t.Repeat == repeatInterval && t.EveryMinutes > leaderOpenEveryMinutes && shortForLeader(t, s.leaderOpenWork(ctx)) {
		t.cadenceEvery = leaderOpenEveryMinutes
	}
	loc, kb := s.location(ctx), s.kbReady(ctx)
	run := ScheduleRun{Kind: t.kind(), RefID: t.ID, Title: t.label(), Due: due.UTC(), At: time.Now().UTC(), Manual: manual, Status: runOK,
		Retried: retried, of: now.UTC()}
	if !s.sched.begin(t.ID) { // the same task is being sent right now ("Run now" as it came due)
		return proto.Session{}, s.skipRun(run, errTaskBusy)
	}
	defer s.sched.end(t.ID)
	if t.SessionID != "" && !t.pushesOnlyAsked() && s.quietConversation(ctx, t.SessionID) {
		// Another task's quiet conversation (its finished turns go unpushed): this one wants its runs pushed, so it
		// runs in its own instead (#2396).
		s.logf("task %s: %s is a quiet task's conversation; running in its own", t.ID, short(t.SessionID))
		t.SessionID = ""
	}
	if !manual && t.Repeat != repeatOnce { // a one-off is never held back: skipped it would be lost (schedule_ready.go)
		if err := s.gateRun(ctx, t); err != nil { // not set up yet: skipped, nothing pushed (schedule_ready.go)
			s.logf("task %s: the main conversation is not ready; skipped", t.ID)
			return proto.Session{}, s.skipRun(run, err)
		}
	}
	if kb {
		t.kbDir = s.kb.dir // the hint names where the knowledge base is, not the default
	}
	t.factsText = s.runFacts(ctx, t, time.Now()) // the run's facts, worked out once (north_star.go)
	if t.SessionID != "" && t.pushesOnlyAsked() && s.mayBeMain(ctx, t.SessionID) {
		// The conversation it came from became (or may be) the main one, which is never quiet.
		s.logf("task %s: %s is (or may be) the main conversation, which is never quiet; running in its own", t.ID, short(t.SessionID))
		t.SessionID = ""
	}
	if t.SessionID != "" {
		if m, ok := s.device(ctx, t.MachineID); ok {
			// A follow-up the assistant scheduled for itself is a turn of that conversation: it keeps the
			// conversation's launcher like a message from the phone does. On the task's category launcher (Agents
			// then) it moved the main conversation onto another one (plain claude, not logged in there) for good (#1518).
			params := map[string]any{"session_id": t.SessionID, "text": taskPrompt(t, due, now, loc, manual, kb, false), "message_source": taskMessageSource(t), "permission_mode": t.PermissionMode}
			err := s.sendModel(ctx, t.MachineID, t.SessionID, params)
			if err == nil {
				err = s.call(ctx, m.ID, DeviceAssistantSend, params, nil)
			}
			if err == nil {
				run.MachineID, run.SessionID = t.MachineID, t.SessionID
				s.logf("task %s ran %s after its time", t.ID, lateness(due, time.Now()))
				s.startScheduledRun(run, t, &m)
				return proto.Session{MachineID: t.MachineID, SessionID: t.SessionID}, nil
			}
			s.logf("task %s into %s: %v; starting a new conversation", t.ID, t.SessionID, err)
		}
	}
	here := s.settings(ctx).MachineID
	binding, err := s.resolveModel(ctx, "task."+t.ID)
	if err != nil {
		run.Status, run.Error = runFailed, err.Error()
		s.recordRun(run)
		s.oneOffNotStarted(ctx, t, run)
		return proto.Session{}, err
	}
	launcher := strings.TrimPrefix(binding.Provider, "launcher.")
	if t.RunSessionID != "" {
		m, ok := s.device(ctx, t.RunMachineID)
		switch {
		case t.RunLauncher != launcher:
			s.logf("task %s: launcher %s → %s, its conversation %s can't go on; starting a new one", t.ID, t.RunLauncher, launcher, short(t.RunSessionID))
		case t.pushesOnlyAsked() && s.mayBeMain(ctx, t.RunSessionID):
			s.logf("task %s: its conversation %s is (or may be) the main one, which is never quiet; starting a new one", t.ID, short(t.RunSessionID))
		case t.RunMachineID != here || !ok:
			s.logf("task %s: the assistant moved off %s, its conversation %s stays there; starting a new one", t.ID, t.RunMachineID, short(t.RunSessionID))
		case t.Repeat != repeatOnce && s.sessionBusy(ctx, m.ID, t.RunSessionID):
			s.logf("task %s: its conversation %s is still on the last run; skipped", t.ID, short(t.RunSessionID))
			run.MachineID, run.SessionID = t.RunMachineID, t.RunSessionID
			err := s.skipRun(run, errTaskBusy)
			if !manual { // Run now is answered busy_wait in the app
				s.tellWaitingSkip(ctx, t, m)
			}
			return proto.Session{}, err
		default:
			params := map[string]any{"session_id": t.RunSessionID, "text": taskPrompt(t, due, now, loc, manual, kb, true), "message_source": taskMessageSource(t), "permission_mode": t.PermissionMode, "resume_only": true}
			err := s.applyModel(ctx, "task."+t.ID, params)
			if err == nil {
				err = s.call(ctx, m.ID, DeviceAssistantSend, params, nil)
			}
			if err == nil {
				run.MachineID, run.SessionID = t.RunMachineID, t.RunSessionID
				s.logf("task %s ran %s after its time (its conversation %s)", t.ID, lateness(due, time.Now()), short(t.RunSessionID))
				s.startScheduledRun(run, t, &m)
				return proto.Session{MachineID: t.RunMachineID, SessionID: t.RunSessionID}, nil
			}
			s.logf("task %s into its conversation %s: %v; starting a new one", t.ID, short(t.RunSessionID), err)
		}
	}
	sess, err := s.startConversation(ctx, startParams{Text: taskPrompt(t, due, now, loc, manual, kb, false), Source: taskMessageSource(t), PermissionMode: t.PermissionMode,
		Purpose: "task." + t.ID, Space: t.Space, Origin: OriginTask, OriginRef: t.ID})
	if err != nil {
		s.logf("task %s: %v", t.ID, err)
		run.Status, run.Error = runFailed, err.Error()
		s.recordRun(run)
		s.oneOffNotStarted(ctx, t, run)
		return proto.Session{}, err
	}
	run.MachineID, run.SessionID = sess.MachineID, sess.SessionID
	s.logf("task %s ran %s after its time (new conversation %s, kept for the next runs)", t.ID, lateness(due, time.Now()), short(sess.SessionID))
	s.adoptConversation(t.ID, sess.MachineID, sess.SessionID, sess.Launcher)
	s.titleFixed(sess.MachineID, sess.SessionID, time.Now(), titleTask, t.label()) // "MMDD | scheduled | task name", the same for every run in it
	var m *Device
	if d, ok := s.device(ctx, sess.MachineID); ok {
		m = &d
	}
	s.startScheduledRun(run, t, m)
	return sess, nil
}

// oneOffNotStarted: a one-off's scheduled run could not even start (its machine offline, the launcher not starting, no
// model). A one-off is off as soon as it is due and has no next time, so this was its only run: recorded as failed and
// nothing else, the follow-up the user counted on was gone without a word (#5030). The user is told, as a run whose turn
// failed is (watchScheduledRun), and it is tried once more after runRetryDelay, as the task is then (deleted: not). A
// failed retry is told too, and not tried again. A repeating task runs again at its next time and has its streak notice
// (#4658); Run now is answered in the app.
func (s *Service) oneOffNotStarted(ctx context.Context, t Task, run ScheduleRun) {
	if t.Repeat != repeatOnce || run.Manual {
		return
	}
	l := s.lang(ctx)
	name := t.labelIn(l)
	if t.Space == SpaceHealth { // the lock screen does not name a health task (health.go)
		name, _ = healthNotice(l)
	}
	s.push(ctx, Notification{Title: i18n.T(l, "assistant-schedule.run_failed", i18n.Args{"name": name}), Body: clip(oneLine(run.Error), 300)})
	if run.Retried {
		return
	}
	delay := runRetryDelay
	if s.runRetry > 0 {
		delay = s.runRetry
	}
	go func() {
		time.Sleep(delay)
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cur, ok := s.retryStillDue(cctx, t.ID, run)
		cancel()
		if !ok {
			return
		}
		if _, err := s.runTask(cur, time.Now(), time.Now(), false, true); err != nil {
			s.logf("task %s retry: %v", t.ID, err)
		}
	}()
}

// sessionBusy: the conversation is on a turn or waiting for the user (an approval, a question).
func (s *Service) sessionBusy(ctx context.Context, machine, id string) bool {
	c, ok := s.session(ctx, machine, id)
	return ok && (c.Status == proto.StatusWorking || c.State == proto.StateWorking || c.State == proto.StateNeedsYou)
}

// tellWaitingSkip pushes that t's run was skipped because its last run waits on the user (an approval, a question):
// only they can let it go on, and the morning brief not coming said nothing of why (#4499). Once per wait (waitKey). A last run
// that is only still working is not told (it ends by itself) and clears the mark, as does one no longer waiting. A
// muted conversation is not pushed. A quiet task (pushesOnlyAsked) is pushed all the same, as its failed runs are
// (run_failed): this says it did not run, not what it found.
func (s *Service) tellWaitingSkip(ctx context.Context, t Task, m Device) {
	key := ""
	if c, ok := s.session(ctx, m.ID, t.RunSessionID); ok && (c.State == proto.StateNeedsYou || c.PendingInteractions > 0) {
		key = s.waitKey(m.ID, t.RunSessionID, c)
	}
	s.sched.mu.Lock()
	if key == "" {
		delete(s.sched.waitTold, t.ID)
		s.sched.mu.Unlock()
		return
	}
	told := s.sched.waitTold[t.ID] == key
	if !told {
		if s.sched.waitTold == nil {
			s.sched.waitTold = map[string]string{}
		}
		s.sched.waitTold[t.ID] = key
	}
	s.sched.mu.Unlock()
	if told || s.muted(m.ID, t.RunSessionID) {
		return
	}
	l := s.lang(ctx)
	name := t.labelIn(l)
	if t.Space == SpaceHealth { // the lock screen does not name a health task (health.go)
		name, _ = healthNotice(l)
	}
	s.push(ctx, Notification{Subtitle: m.Name, DeviceID: m.ID, SessionID: t.RunSessionID,
		Title: i18n.T(l, "assistant-schedule.waiting_skipped", i18n.Args{"name": name}), Body: i18n.T(l, "assistant-schedule.waiting_skipped_body")})
}

// waitKey names the wait session is in: the cards it waits on (their ids), else when it entered the state it is in.
// A card answered and a new one raised is another wait.
func (s *Service) waitKey(machine, session string, c proto.Session) string {
	s.fleet.mu.Lock()
	var ids []string
	for id, p := range s.fleet.interactions {
		if p.ix.SessionID == session {
			ids = append(ids, id)
		}
	}
	mk, marked := s.fleet.stateSince[[2]string{machine, session}]
	s.fleet.mu.Unlock()
	if len(ids) > 0 {
		slices.Sort(ids)
		return session + "|" + strings.Join(ids, ",")
	}
	at := c.StateSince
	if marked && !mk.at.IsZero() {
		at = mk.at
	}
	return session + "|" + c.State + "@" + at.UTC().Format(time.RFC3339Nano)
}

func (s *Service) skipRun(run ScheduleRun, why error) error {
	run.Status, run.Error = runSkipped, i18n.Localize(why, s.lang(context.Background())) // the history keeps it in the user's language
	run.NotReady = errors.Is(why, errNotReady)
	s.recordRun(run)
	return why
}

const runWatchTimeout = 30 * time.Minute

// runRetryDelay is how long after a failed run the automatic retry goes (Service.runRetry shortens it in tests: a
// package variable raced with the watchers earlier tests left running).
const runRetryDelay = 3 * time.Minute

// startRun records a sent run and follows its turn. The node accepts `assistant.send` before the model answers, so a
// run whose turn ends in an API error (quota, no such model, upstream 503) used to stay "ok" and the user was never
// told; the watcher records it as failed, pushes it, and retries once (the task as it is then, see watchScheduledRun).
// m is the machine the run went to (nil: none).
func (s *Service) startScheduledRun(run ScheduleRun, t Task, m *Device) {
	s.sched.mu.Lock()
	delete(s.sched.waitTold, t.ID) // it runs again: a later wait on the user is news again
	s.sched.mu.Unlock()
	if t.Facts {
		s.briefSent(t.ID, run.SessionID, run.Retried)
	}
	if !t.pushesOnlyAsked() {
		s.noteReportTurn(run.SessionID) // its answer is pushed however quick it was (#3438)
	}
	run.ID = s.recordRun(run)
	if m == nil || run.ID == "" {
		return
	}
	go s.watchScheduledRun(run, t, *m)
}

func (s *Service) watchScheduledRun(run ScheduleRun, t Task, m Device) {
	ctx, cancel := context.WithTimeout(context.Background(), runWatchTimeout)
	defer cancel()
	ended, ran := s.waitTurnEnd(ctx, m.ID, run.SessionID)
	if !ended {
		return
	}
	text := s.lastTurnAPIError(ctx, m.ID, run.SessionID)
	failedSession, hasSession := s.session(ctx, m.ID, run.SessionID)
	if text == "" && hasSession {
		text = failedSession.TurnError
	}
	if text == "" {
		// The model service answered 200 with nothing in it: no reply, no tool call, no error. The run stayed "ok"
		// and the push said "done" over an empty morning briefing; it fails and is retried like an error (#3752, as tasks: #2349).
		if items, read := s.lastTurnItems(ctx, m.ID, run.SessionID); read && silentRun(items, t, ran) && s.stillEnded(ctx, m.ID, run.SessionID, items) {
			text = silentTurnErrorIn(s.lang(ctx))
			// Its failure is pushed below. noteAsked holds back the conversation's finished pushes for the next three
			// minutes (notifyFinished), so the turn's own "Morning briefing · done" does not go out next to it.
			s.noteAsked(run.SessionID)
		}
	}
	if text == "" {
		return
	}
	full := text
	text = clip(oneLine(text), 300)
	s.logf("task %s run %s failed: %s", t.ID, run.ID, text)
	streak := s.updateRun(run, runFailed, text)
	name := t.labelIn(s.lang(ctx))
	if t.Space == SpaceHealth { // the lock screen does not name a health task (health.go)
		name, _ = healthNotice(s.lang(ctx))
	}
	if !streak { // the streak notice already said it failed, with this reason
		s.push(ctx, Notification{Subtitle: m.Name, DeviceID: m.ID, SessionID: run.SessionID, Title: i18n.T(s.lang(ctx), "assistant-schedule.run_failed", i18n.Args{"name": name}), Body: text})
	}
	if run.Retried {
		return
	}
	if hasSession {
		if failedSession.TurnError == "" {
			failedSession.TurnError = full
		}
		if sessionTurnErrorCode(failedSession) == proto.TurnErrorModelFallbackUnavailable {
			return // the unavailable refusal fallback needs the user's recovery, never an automatic replay
		}
	}
	var limitTurn time.Time // the turn a usage limit stopped, when one did: the retry goes only while it is still the last
	if reset, turn, resumes, limited := s.runLimit(ctx, m.ID, run.SessionID, full); limited {
		// A subscription's usage limit (docs/assistant/agent-work.md "Usage limits"): a retry before it resets is
		// refused at once. The conversation that goes on by itself then needs none; otherwise the retry waits for the
		// reset, however long that is — the watcher's time is for the turn, not for this.
		if resumes {
			s.logf("task %s run %s: its conversation goes on by itself once the usage limit resets at %s: not retried", t.ID, run.ID,
				reset.UTC().Format(time.RFC3339))
			return
		}
		wait := s.limitRetryWait(reset, time.Now())
		s.logf("task %s run %s: the usage limit resets at %s: retried in %s", t.ID, run.ID, reset.UTC().Format(time.RFC3339), wait.Round(time.Second))
		time.Sleep(wait)
		limitTurn = turn
	} else {
		delay := runRetryDelay
		if s.runRetry > 0 {
			delay = s.runRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
	// The failure push sends the user to the task's page; by now they may have switched it off, deleted it or changed
	// its prompt. The retry goes by what the schedule holds now, not by the copy the run started with: that copy ran
	// again regardless, with the old prompt, and silently (a retry that fails is not pushed).
	rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
	cur, ok := s.retryStillDue(rctx, t.ID, run)
	if ok && !limitTurn.IsZero() {
		// The wait for the reset is long: the conversation may have gone on meanwhile (the user said "go on" after the
		// reset), and the work it did (a mail, a message) is not to be done twice.
		if c, found := s.session(rctx, m.ID, run.SessionID); found && (c.Status == proto.StatusWorking || !sameTurn(c, limitTurn)) {
			s.logf("task %s run %s: its conversation went on after the usage limit: not retried", t.ID, run.ID)
			ok = false
		}
	}
	rcancel()
	if !ok {
		return
	}
	if _, err := s.runTask(cur, time.Now(), time.Now(), false, true); err != nil {
		s.logf("task %s retry: %v", t.ID, err)
	}
}

// runLimit: the run's conversation (sid on machine) stopped on a subscription's usage limit whose reset is known
// (text: its turn's error), the end of that turn (the conversation's updated_at), and whether its CLI said it goes on
// by itself then. As the lists have the conversation (the node's record, else Claude Code's words when they name a time
// zone, decorateLimit), else as the node has it.
func (s *Service) runLimit(ctx context.Context, machine, sid, text string) (reset, turn time.Time, resumes, ok bool) {
	c, found := s.decoratedSession(machine, sid)
	if !found {
		c, _ = s.session(ctx, machine, sid)
	}
	if strings.TrimSpace(c.TurnError) == "" {
		c.TurnError = text // Claude Code's error is its last words: an older node's raw session has no turn error
	}
	turn = c.UpdatedAt
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = time.Now()
	}
	decorateLimit(&c)
	reset, ok = usageLimitReset(c)
	return reset, turn, ok && c.TurnErrorResumes, ok
}

// limitRetryWait is how long the retry of a run a usage limit stopped waits: until a minute or so after the reset
// (limitContinueAt), as the agents' continues do; schedule.limitWait in tests.
func (s *Service) limitRetryWait(reset, now time.Time) time.Duration {
	if f := s.sched.limitWait; f != nil {
		return f(reset, now)
	}
	return limitContinueAt(reset, now).Sub(now)
}

// retryStillDue reads the task as the schedule holds it now for the automatic retry of its failed run; false (and
// logged) when the retry does not go. The failure push sends the owner to the task, and what they did there wins
// (#5320): a Run now meanwhile already ran it (the retry ran it a second time: the mail, the message went twice), and
// a one-off moved to another time runs then (the retry ran it now, early, and it was then done).
func (s *Service) retryStillDue(ctx context.Context, id string, failed ScheduleRun) (Task, bool) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		s.logf("task %s retry: %v", id, err)
		return Task{}, false
	}
	var cur Task
	i := sc.indexLocked(id)
	if i >= 0 {
		cur = sc.tasks[i]
	}
	runs := slices.Clone(sc.runs)
	sc.mu.Unlock()
	now := time.Now()
	if s.retryNow != nil {
		now = s.retryNow()
	}
	if why := retryCancelled(cur, i >= 0, failed, runs, now); why != "" {
		s.logf("task %s: %s since its run failed; not retried", id, why)
		return Task{}, false
	}
	return cur, true
}

// retryCancelled is why the retry of failed, due at now, does not go ("" when it does). cur is the task now (found:
// false when it was deleted); runs is the history, newest last.
//   - Another run of the task went out after the failed one (a Run now, or its next time): it already ran again. A
//     run that could not start (no conversation) or was skipped did nothing, so the retry still goes.
//   - A one-off is switched on again with its time after now (moved later, or earlier but still ahead): the
//     scheduler runs it then. A one-off that is due is off (runDue), so being on again means the owner set it.
//   - A repeating task switched off. (A repeating task's next time is always ahead, so its time says nothing.)
func retryCancelled(cur Task, found bool, failed ScheduleRun, runs []ScheduleRun, now time.Time) string {
	if !found {
		return "deleted"
	}
	for _, r := range slices.Backward(runs) {
		if r.RefID == cur.ID && r.At.After(failed.At) && r.Status != runSkipped && r.SessionID != "" {
			return "run again"
		}
	}
	if cur.Repeat == repeatOnce {
		if cur.Enabled && cur.At.After(now) {
			return "moved to " + cur.At.UTC().Format(time.RFC3339)
		}
		return ""
	}
	if !cur.Enabled {
		return "switched off"
	}
	return ""
}

// silentRun: the run's turn is known to have ended with no output, so failing it (and running the task again) is
// safe. A wrong verdict runs the whole task twice (mail, messages sent again), so all of these must hold:
//   - silentTurn: no reply, no tool call, not stopped by the user;
//   - the turn is the run's: its prompt is the task's (the user may have sent the conversation another message
//     since, and the node's last turn is then that one);
//   - it is known to have ended: the watcher saw it working before it went idle (ran), or the turn has its end
//     marker (turn_duration). A changed last item alone may be the prompt landing while the session's state still
//     lags, and an assistant item may be thinking while the model goes on (#3777 review).
func silentRun(items []proto.Item, t Task, ran bool) bool {
	if !silentTurn(items) {
		return false
	}
	if ask, _ := turnExchange(items); !runAsk(ask, t.Prompt) {
		return false
	}
	if ran {
		return true
	}
	for _, it := range items {
		if it.TurnDuration != nil || it.Kind == "turn_duration" {
			return true
		}
	}
	return false // an assistant item proves nothing: thinking is one, and the model may still be on its turn
}

// runAsk: ask is the message a run of a task with prompt sent (taskPrompt: the header, a blank line, the prompt), not
// a message of the user's that only mentions it ("where's the briefing?" for the task "briefing").
func runAsk(ask, prompt string) bool {
	ask, prompt = strings.TrimSpace(ask), strings.TrimSpace(prompt)
	if prompt == "" {
		return false
	}
	if ask == prompt {
		return true
	}
	// The prompt as a paragraph of its own after the header: followed by the end or a line break (the KB hint).
	_, rest, ok := strings.Cut(ask, "\n\n"+prompt)
	return ok && (rest == "" || strings.HasPrefix(rest, "\n"))
}

// turnPollInterval is how often waitTurnEnd reads the turn (Service.turnPoll shortens it in tests, for the same
// reason as runRetryDelay).
const turnPollInterval = 2 * time.Second

// waitTurnEnd waits until the sent turn is over; ended is false when ctx ended, ran says the turn was seen working.
// A poll whose read failed tells nothing and is skipped. Once the turn was seen working, it has ended after two
// polls in a row that read it idle (one may be a status flap, a hookless Claude whose spinner was missed). Before
// it was seen working, a changed last item also means it ended: an immediate failure (a bad model) is idle before
// the first poll sees it working.
func (s *Service) waitTurnEnd(ctx context.Context, machine, sid string) (ended, ran bool) {
	poll := turnPollInterval
	if s.turnPoll > 0 {
		poll = s.turnPoll
	}
	before, beforeOK := s.lastItemID(ctx, machine, sid)
	idle := 0
	for {
		select {
		case <-ctx.Done():
			return false, ran
		case <-time.After(poll):
		}
		c, ok := s.session(ctx, machine, sid)
		if !ok {
			continue // not read: no sight of idle (and the debounce does not count it)
		}
		if c.Status == proto.StatusWorking || c.State == proto.StateWorking || c.State == proto.StateNeedsYou {
			ran, idle = true, 0
			if c.State == proto.StateNeedsYou {
				return true, ran // waiting on the user counts as ended
			}
			continue
		}
		if !ran {
			if id, ok := s.lastItemID(ctx, machine, sid); ok && beforeOK && id != before {
				return true, false
			}
			continue
		}
		if idle++; idle >= 2 {
			return true, true
		}
	}
}

// lastItemID is the newest item's id ("" when there is none); ok is false when the node could not be asked.
func (s *Service) lastItemID(ctx context.Context, machine, sid string) (string, bool) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "limit": 1, "lazy_images": true}, &res); err != nil {
		return "", false
	}
	if len(res.Items) == 0 {
		return "", true
	}
	return res.Items[len(res.Items)-1].ID, true
}

// stillEnded: the turn read as items is still where the conversation stands, idle with the same last item. Asked
// right before a run is failed as silent: a turn that went on meanwhile (the model was slow to its first token) is
// not failed, its task not run again over it.
func (s *Service) stillEnded(ctx context.Context, machine, sid string, items []proto.Item) bool {
	if len(items) == 0 {
		return false
	}
	c, ok := s.session(ctx, machine, sid)
	if !ok || c.Status == proto.StatusWorking || c.State == proto.StateWorking || c.Starting {
		return false
	}
	id, ok := s.lastItemID(ctx, machine, sid)
	return ok && id == items[len(items)-1].ID
}

// lastTurnAPIError is the text of the turn's last answer when it is a model API error; "" otherwise.
func (s *Service) lastTurnAPIError(ctx context.Context, machine, sid string) string {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "limit": 8, "lazy_images": true}, &res); err != nil {
		return ""
	}
	for i := len(res.Items) - 1; i >= 0; i-- {
		it := res.Items[i]
		if it.Role != "assistant" && it.Kind != "" && it.Kind != "text" {
			continue
		}
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		if apiErrorText(text) || cutOff(text) { // cutOff: also the limit's and the lost sign-in's own lines
			return text
		}
		return ""
	}
	return ""
}

// apiErrorMarkers start what Claude Code answers when the model call itself failed (an error card in the app).
var apiErrorMarkers = assistantwire.APIErrorMarkers

// cutOff: an agent's answer is the model API error that ended its turn.
func cutOff(answer string) bool { return assistantwire.CutOff(answer) }

// apiErrorText: what Claude Code answers when the model call itself failed (an error card in the app).
func apiErrorText(text string) bool {
	for _, marker := range apiErrorMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// begin marks task id as being sent; false when it already is. end clears the mark.
func (sc *schedule) begin(id string) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.sending[id] {
		return false
	}
	if sc.sending == nil {
		sc.sending = map[string]bool{}
	}
	sc.sending[id] = true
	return true
}

func (sc *schedule) end(id string) {
	sc.mu.Lock()
	delete(sc.sending, id)
	sc.mu.Unlock()
}

// adoptConversation makes a new conversation task id's own, and keeps it listed on
// its machine however quiet it gets (keepListed), so the app can open it and the next run can see it is busy.
func (s *Service) adoptConversation(id, machine, session, launcher string) {
	if sc, err := s.lockedSchedule(context.Background()); err == nil {
		if i := sc.indexLocked(id); i >= 0 {
			t := &sc.tasks[i]
			t.RunMachineID, t.RunSessionID, t.RunLauncher = machine, session, launcher
			// The conversation already exists. Keep it in memory on a failed save; recordRun retries the write.
			_ = s.saveLocked(sc, sc.tasks)
		}
		sc.mu.Unlock()
	}
	go s.keepListed(context.Background())
}

// keepListed tells the hub the conversations their machines are to list however long they are quiet have changed
// (the snapshot's profile: keptSessions); it configures the nodes.
func (s *Service) keepListed(ctx context.Context) { s.publish(ctx) }

// forgetConversation is "New chat next time": the next run starts a new conversation (the old one stays in Chats).
func (s *Service) forgetConversation(ctx context.Context, id string) (Task, bool, error) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return Task{}, false, err
	}
	i := sc.indexLocked(id)
	if i >= 0 {
		tasks := slices.Clone(sc.tasks)
		t := &tasks[i]
		t.RunMachineID, t.RunSessionID, t.RunLauncher = "", "", ""
		if err := s.saveLocked(sc, tasks); err != nil {
			sc.mu.Unlock()
			return Task{}, false, err
		}
	}
	sc.mu.Unlock()
	if i < 0 {
		return Task{}, false, nil
	}
	s.broadcastSchedule()
	go s.keepListed(context.WithoutCancel(ctx))
	t, ok := s.taskView(ctx, id)
	return t, ok, nil
}

// taskPrompt is what the conversation gets: a ⏰ line saying what this is, how late when the hub was down, and for a
// repeating one that it repeats by itself (so the run doesn't schedule the next one too). continued: it goes into the
// task's own conversation, which has had all that (and the knowledge-base hint) already; it is told the earlier
// runs are above instead. The task's prompt is always there: it may have been edited, or compacted away.
func taskPrompt(t Task, due, now time.Time, loc *time.Location, manual, kb, continued bool) string {
	r := prompts.TaskRun{Label: t.label(), Once: t.Repeat == repeatOnce, Interval: t.Repeat == repeatInterval, Due: due.In(loc),
		Prompt: t.Prompt, KBHint: kb && t.calendar(), KnowledgeDir: t.kbDir, Continued: continued, PushAsked: t.pushesOnlyAsked(), Facts: t.factsText}
	if r.Interval {
		r.Every = everyText(t.EveryMinutes)
		if t.cadenceEvery > 0 {
			r.Every, r.Accelerated = everyText(t.cadenceEvery), true
		}
	}
	if late := now.Sub(due); !manual && !t.calendar() && late > 2*time.Minute {
		r.Late = humanDuration(late)
	}
	return prompts.RenderTaskRun(r)
}

// lateness for the log: "0.4s".
func lateness(due, at time.Time) string {
	return at.Sub(due).Round(100 * time.Millisecond).String()
}

// humanDuration: "25 minutes" / "3 hours" / "2 days" (in Chinese).
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}

// broadcastSchedule: the list changed. The scheduler re-reads it, the apps are told (event assistant_schedule) and
// the hub gets the tasks in the snapshot.
func (s *Service) broadcastSchedule() {
	select { // the scheduler re-reads the list
	case s.sched.wakeup() <- struct{}{}:
	default:
	}
	ctx := context.Background()
	s.publish(ctx)
	s.event(ctx, map[string]any{"type": "assistant_schedule"})
}

// taskViews are the tasks as the snapshot reports them; nil while the schedule can't be read.
func (s *Service) taskViews(ctx context.Context) []TaskView {
	loc, now := s.location(ctx), time.Now()
	open := s.leaderOpenWork(ctx)
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return nil
	}
	defer sc.mu.Unlock()
	out := make([]TaskView, 0, len(sc.tasks))
	for _, t := range sc.tasks {
		at := t.At
		if t.Repeat == repeatInterval || t.Repeat == repeatCron {
			at = t.nextWithLeaderCadence(now, loc, open)
		}
		out = append(out, TaskView{ID: t.ID, Label: t.label(), Repeat: t.Repeat, Enabled: t.Enabled, At: at, EveryMinutes: t.EveryMinutes,
			MachineID: t.MachineID, RunMachineID: t.RunMachineID, RunSessionID: t.RunSessionID, Push: t.Push})
	}
	return out
}

// tasksView is the list as the app sees it: next runs filled in (a cron task's next few too, and its expression in words
// in the request's language), one-offs that already ran left out. Empty while the schedule can't be read: the routes
// check scheduleReadable first.
func (s *Service) tasksView(ctx context.Context, keep func(Task) bool) []Task {
	sc := s.loadSchedule(ctx)
	loc, lang := s.location(ctx), s.requestCtxLang(ctx)
	machine := s.settings(ctx).MachineID
	now := time.Now()
	open := s.leaderOpenWork(ctx)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	out := []Task{}
	for _, t := range sc.tasks {
		if t.done() || (keep != nil && !keep(t)) {
			continue
		}
		t.NextRun = t.nextWithLeaderCadence(now, loc, open)
		if t.Repeat == repeatCron {
			if r, err := parseCron(t.Cron); err == nil {
				t.CronText = r.text(lang)
				if t.Enabled {
					t.NextRuns = r.nextRuns(now, loc, cronRuns)
				}
			}
		}
		if t.MachineID == "" {
			t.MachineID = machine
		}
		t.Launcher, t.Model, t.Effort = "", "", ""
		out = append(out, t)
	}
	return out
}

func (s *Service) taskView(ctx context.Context, id string) (Task, bool) {
	for _, t := range s.tasksView(ctx, nil) {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// editTask applies what the app changed (the hub's own fields and where runs go are kept; SessionID can only be
// cleared, to run in a new conversation each time). Push and Until are kept: the routes set them only when the
// body has them. A missing Until unmarshals as zero and would wipe a stored end time; null clears it.
func editTask(old, in Task, now time.Time) Task {
	t := old
	t.Title, t.Prompt, t.Repeat, t.Enabled = in.Title, in.Prompt, in.Repeat, in.Enabled
	t.Time, t.Weekdays, t.EveryMinutes, t.Cron = in.Time, in.Weekdays, in.EveryMinutes, in.Cron
	t.PermissionMode = in.PermissionMode
	t.Launcher, t.Model, t.Effort = "", "", ""
	if in.SessionID == "" {
		t.SessionID = ""
	}
	switch t.Repeat {
	case repeatOnce:
		t.At = in.At
	case repeatInterval:
		// The next run stays unless the rule changed (or the app picked a start); then one interval from now.
		every := time.Duration(t.EveryMinutes) * time.Minute
		switch {
		case !in.At.IsZero() && in.At.After(now) && !in.At.Equal(old.At) && in.At.Before(now.Add(every)):
			t.At = in.At
		case old.Repeat != repeatInterval || old.EveryMinutes != t.EveryMinutes || !old.At.After(now) || (!old.Enabled && t.Enabled):
			t.At = now.Add(every)
		}
	}
	return t
}

func (s *Service) scheduleRoutes() {
	s.handle("GET /api/v1/assistant/tasks", func(w http.ResponseWriter, r *http.Request) {
		if err := s.scheduleReadable(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"tasks": s.tasksView(i18n.With(r.Context(), s.requestLang(r)), nil)})
	})
	// A cron expression checked before it is saved: what it is in words and when it runs next, in the user's time zone;
	// 400 with what is wrong.
	s.handle("GET /api/v1/assistant/tasks/cron", func(w http.ResponseWriter, r *http.Request) {
		rule, err := parseCron(r.URL.Query().Get("cron"))
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		loc := s.location(r.Context())
		apierr.WriteJSON(w, map[string]any{"cron": rule.expr, "text": rule.text(s.requestLang(r)), "timezone": loc.String(),
			"next_runs": rule.nextRuns(time.Now(), loc, cronRuns)})
	})
	s.handle("POST /api/v1/assistant/tasks", func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		push, _, err := pushIn(raw)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		now := time.Now()
		end, hasUntil, clearUntil, err := untilIn(raw["until"], now, s.location(r.Context()))
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		delete(raw, "until") // not a time.Time: "30m" and a local clock time would not unmarshal
		var in Task
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		t := editTask(Task{ID: "task_" + randomHex(4), CreatedAt: now.UTC()}, in, now)
		t.SessionID, t.Push = "", push
		applyUntil(&t, end, hasUntil, clearUntil)
		dropStaleUntil(&t, hasUntil, now)
		if err := checkTask(&t, now); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		rearm(Task{}, &t, now)
		sc, err := s.lockedSchedule(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		err = s.saveLocked(sc, append(slices.Clone(sc.tasks), t))
		sc.mu.Unlock()
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		s.broadcastSchedule()
		v, _ := s.taskView(i18n.With(r.Context(), s.requestLang(r)), t.ID)
		apierr.WriteJSON(w, map[string]any{"task": v})
	})
	s.handle("PUT /api/v1/assistant/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		// The end time does not depend on the task list. Read it before the lock, so a bad value never
		// waits on the scheduler, and a settings read never holds that lock.
		now := time.Now()
		end, hasUntil, clearUntil, err := untilIn(raw["until"], now, s.location(r.Context()))
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		delete(raw, "until") // not a time.Time: "30m" and a local clock time would not unmarshal
		mayBeMain := s.mayBeMainNow(r.Context())
		sc, err := s.lockedSchedule(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		i := sc.indexLocked(r.PathValue("id"))
		if i < 0 {
			sc.mu.Unlock()
			apierr.Write(w, http.StatusNotFound, i18n.Err("assistant-schedule.gone"))
			return
		}
		var in Task
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &in); err != nil {
			sc.mu.Unlock()
			apierr.Write(w, 400, err)
			return
		}
		if _, has := raw["cron"]; !has && sc.tasks[i].Repeat == repeatCron {
			// An app that does not know cron tasks keeps the task's rule (an older iOS app reads one as a one-off,
			// Android and the web send its repeat back without the expression).
			old := sc.tasks[i]
			in.Repeat, in.Cron, in.At, in.Time, in.Weekdays, in.EveryMinutes = old.Repeat, old.Cron, old.At, "", nil, 0
		}
		t := editTask(sc.tasks[i], in, now)
		push, has, err := pushIn(raw)
		if err != nil {
			sc.mu.Unlock()
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if has { // an app that does not know the field leaves it out: the task keeps its push
			t.Push = push
		}
		if v, ok := raw["facts"]; ok { // and its facts: set only when the body has them
			if err := json.Unmarshal(v, &t.Facts); err != nil {
				sc.mu.Unlock()
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		if v, ok := raw["report"]; ok { // and whether it is a daily report, the same way
			if err := json.Unmarshal(v, &t.Report); err != nil {
				sc.mu.Unlock()
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
		}
		applyUntil(&t, end, hasUntil, clearUntil)
		dropStaleUntil(&t, hasUntil, now)
		ownConversationIfQuiet(&t, mayBeMain)
		if err := checkTask(&t, now); err != nil {
			sc.mu.Unlock()
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		rearm(sc.tasks[i], &t, now)
		tasks := slices.Clone(sc.tasks)
		tasks[i] = t
		err = s.saveLocked(sc, tasks)
		sc.mu.Unlock()
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		s.broadcastSchedule()
		v, _ := s.taskView(i18n.With(r.Context(), s.requestLang(r)), t.ID)
		apierr.WriteJSON(w, map[string]any{"task": v})
	})
	s.handle("DELETE /api/v1/assistant/tasks/{id}", s.deleteTaskRoute)
	s.handle("POST /api/v1/assistant/tasks/{id}/run", s.runTaskRoute)
	s.handle("POST /api/v1/assistant/tasks/{id}/fresh", func(w http.ResponseWriter, r *http.Request) {
		t, ok, err := s.forgetConversation(r.Context(), r.PathValue("id"))
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		if !ok {
			apierr.Write(w, http.StatusNotFound, i18n.Err("assistant-schedule.gone"))
			return
		}
		apierr.WriteJSON(w, map[string]any{"task": t})
	})
	s.handle("GET /api/v1/assistant/runs", func(w http.ResponseWriter, r *http.Request) {
		sc, err := s.lockedSchedule(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		runs := make([]ScheduleRun, 0, len(sc.runs))
		for i := len(sc.runs) - 1; i >= 0; i-- { // newest first
			if ref := r.URL.Query().Get("ref"); ref == "" || sc.runs[i].RefID == ref {
				runs = append(runs, sc.runs[i])
			}
		}
		sc.mu.Unlock()
		apierr.WriteJSON(w, map[string]any{"runs": runs})
	})

	// The two lists app builds from before 2026-10-31 read and write (a task's JSON is a superset of both):
	// routines are the daily tasks; follow-ups the once and interval ones, by their next run.
	s.handle("GET /api/v1/assistant/routines", func(w http.ResponseWriter, r *http.Request) {
		if err := s.scheduleReadable(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"routines": s.tasksView(r.Context(), func(t Task) bool { return t.Repeat == repeatDaily })})
	})
	s.handle("PUT /api/v1/assistant/routines", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Routines []Task `json:"routines"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		now := time.Now()
		sc, err := s.lockedSchedule(r.Context())
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		var out []Task
		old := map[string]Task{}
		for _, t := range sc.tasks {
			if t.Repeat == repeatDaily {
				old[t.ID] = t
			} else {
				out = append(out, t)
			}
		}
		for _, rt := range in.Routines {
			prev, ok := old[rt.ID]
			if !ok {
				prev = Task{ID: "rt_" + randomHex(4), CreatedAt: now.UTC()}
			}
			rt.Repeat, rt.Launcher, rt.Model, rt.PermissionMode, rt.Effort, rt.SessionID = repeatDaily, prev.Launcher, prev.Model, prev.PermissionMode, prev.Effort, prev.SessionID
			t := editTask(prev, rt, now)
			if err := checkTask(&t, now); err != nil {
				sc.mu.Unlock()
				apierr.Write(w, http.StatusBadRequest, err)
				return
			}
			if ok {
				rearm(prev, &t, now)
			} else {
				rearm(Task{}, &t, now)
			}
			out = append(out, t)
		}
		err = s.saveLocked(sc, out)
		sc.mu.Unlock()
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		s.broadcastSchedule()
		apierr.WriteJSON(w, map[string]any{"routines": s.tasksView(r.Context(), func(t Task) bool { return t.Repeat == repeatDaily })})
	})
	s.handle("POST /api/v1/assistant/routines/{id}/run", s.runTaskRoute)
	s.handle("GET /api/v1/assistant/followups", func(w http.ResponseWriter, r *http.Request) {
		if err := s.scheduleReadable(r.Context()); err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
		list := s.tasksView(r.Context(), func(t Task) bool { return t.Repeat != repeatDaily && t.Enabled })
		for i := range list {
			list[i].At = list[i].NextRun
		}
		sort.Slice(list, func(i, j int) bool { return list[i].At.Before(list[j].At) })
		apierr.WriteJSON(w, map[string]any{"followups": list})
	})
	s.handle("POST /api/v1/assistant/followups/{id}/run", s.runTaskRoute)
	s.handle("DELETE /api/v1/assistant/followups/{id}", s.deleteTaskRoute)
}

func (s *Service) deleteTaskRoute(w http.ResponseWriter, r *http.Request) {
	ok, err := s.deleteTask(r.Context(), r.PathValue("id"))
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	if !ok {
		apierr.Write(w, http.StatusNotFound, i18n.Err("assistant-schedule.gone"))
		return
	}
	apierr.WriteJSON(w, map[string]any{"ok": true})
}

// runTaskRoute is "Run now": the task runs now; a one-off is then done, a repeating one keeps its schedule.
func (s *Service) runTaskRoute(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	sc, err := s.lockedSchedule(r.Context())
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, err)
		return
	}
	i := sc.indexLocked(r.PathValue("id"))
	var t Task
	if i >= 0 {
		t = sc.tasks[i]
		tasks := slices.Clone(sc.tasks)
		// A manual run is admitted durably too, including repeating tasks: its result must own the latest
		// status, and a storage outage must not dispatch work from an unpersisted run marker.
		tasks[i].LastRun = now.UTC()
		if t.Repeat == repeatOnce {
			tasks[i].Enabled = false
		}
		if err := s.saveLocked(sc, tasks); err != nil {
			sc.mu.Unlock()
			apierr.Write(w, http.StatusServiceUnavailable, err)
			return
		}
	}
	sc.mu.Unlock()
	if i < 0 {
		apierr.Write(w, http.StatusNotFound, i18n.Err("assistant-schedule.gone"))
		return
	}
	s.broadcastSchedule()
	sess, err := s.runTask(t, now, now, true, false)
	if errors.Is(err, errTaskBusy) {
		apierr.Write(w, http.StatusConflict, i18n.Err("assistant-schedule.busy_wait"))
		return
	}
	if err != nil {
		apierr.Call(w, err)
		return
	}
	apierr.WriteJSON(w, map[string]any{"session_id": sess.SessionID, "machine_id": sess.MachineID})
}
