package assistant

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/kb"
)

// The knowledge base's background work runs in lanes (#4247), each in its own goroutine, so one lane's slow step (a
// meeting's minutes, a slow model) no longer holds back the others. The lanes are split by what they write, so no two
// write the same thing at once:
//
//   - archive: the sources, compression, project rewrites, recordings' summaries and retention, duplicate projects,
//     Moments links, item tagging and pruning (chats, projects, people, items); its steps keep their order, so the
//     project rewrites see what this run's compression made due;
//   - topics: knowledge points' distill and merge (topics and their sources' marks, under the store's topicMu);
//   - minutes: meeting minutes from recordings (the recordings' minutes, under recMu);
//   - recap: the daily recap of all-day recordings (their notes and recaps, under recMu; kb_day.go), so a long day
//     never holds back a meeting's minutes.
//
// The loop (kbLoop) starts a lane when it is due (a round's time, a wake, a kick) and not still running; one still
// running when it became due starts again as soon as it ends, as a round used to after a long one.

// kbLane is one lane: its name (for the log) and one run of its work.
type kbLane struct {
	name string
	run  func(ctx context.Context, e *kb.Engine)

	running bool // only the loop's goroutine reads and writes these two
	due     bool
}

// kbLanes are the loop's lanes and where they say they ended.
type kbLanes struct {
	lanes []*kbLane
	done  chan *kbLane
}

func newKBLanes(lanes ...*kbLane) *kbLanes {
	for _, l := range lanes {
		l.due = true // the first round runs every lane
	}
	return &kbLanes{lanes: lanes, done: make(chan *kbLane, len(lanes))} // one run a lane at a time: never blocks
}

// tick makes every lane due.
func (k *kbLanes) tick() {
	for _, l := range k.lanes {
		l.due = true
	}
}

// busy: some lane is still running (it holds the engine it started with).
func (k *kbLanes) busy() bool {
	for _, l := range k.lanes {
		if l.running {
			return true
		}
	}
	return false
}

// ended takes a lane's end (from done).
func (k *kbLanes) ended(l *kbLane) { l.running = false }

// startDue starts every due lane that is not running, each in its own goroutine through run. prepare runs first, once,
// and only when a lane would start; false: none starts and they stay due.
func (k *kbLanes) startDue(prepare func() bool, run func(l *kbLane)) {
	var start []*kbLane
	for _, l := range k.lanes {
		if l.due && !l.running {
			start = append(start, l)
		}
	}
	if len(start) == 0 || !prepare() {
		return
	}
	for _, l := range start {
		l.due, l.running = false, true
		go func() {
			defer func() { k.done <- l }()
			run(l)
		}()
	}
}

// kbLaneSet is the knowledge base's lanes; r is the archive lane's own bookkeeping (only that lane touches it).
func (s *Service) kbLaneSet(r *kbRounds) *kbLanes {
	return newKBLanes(
		&kbLane{name: "archive", run: func(ctx context.Context, e *kb.Engine) { s.kbArchiveLane(ctx, e, r) }},
		&kbLane{name: "topics", run: func(ctx context.Context, e *kb.Engine) {
			// Knowledge points by topic (kb/topics.go; assistant.kb_topics): a couple of distill calls, then a couple of
			// merges, topic workers at a time.
			e.DistillTopics(ctx, 2)
			e.RefreshTopics(ctx, 2)
		}},
		// Meetings' minutes (kb/minutes.go): a recording's can take many calls, so they have their own lane.
		&kbLane{name: "minutes", run: func(ctx context.Context, e *kb.Engine) { e.WriteMinutes(ctx, 2) }},
		// All-day recordings' recaps (kb/day_recap_write.go): one recording's notes or recap a run, and the day's push
		// at its recap time; appends that finished an hour wake it.
		&kbLane{name: "recap", run: func(ctx context.Context, e *kb.Engine) { e.WriteDayRecaps(ctx, 1) }},
	)
}

// kbRunLane runs one go of l with e, under kbRun, and logs what it did when it called the model or took longer than a
// round: how long, how many calls, how many failed and how many were put off (a paused connection).
func (s *Service) kbRunLane(ctx context.Context, e *kb.Engine, l *kbLane) {
	st := &kbLaneStats{}
	start := time.Now()
	s.kbRun(ctx, func() { l.run(context.WithValue(ctx, kbLaneStatsKey{}, st), e) })
	took := time.Since(start)
	if calls := st.calls.Load(); calls > 0 || took >= kbPoll {
		s.logf("kb: %s lane: %s, %d model call(s), %d failed, %d put off", l.name, took.Round(time.Second), calls, st.failed.Load(), st.putOff.Load())
	}
}

// kbLaneStats counts a lane run's model calls (kbCountedModel, through the run's context).
type kbLaneStats struct{ calls, failed, putOff atomic.Int32 }

type kbLaneStatsKey struct{}

// kbCountedModel counts each call in the lane run its context belongs to; calls from elsewhere (tools, the history
// import) carry none and are not counted. A call cancelled with its run counts as nothing; one that ran out of its own
// time (DeadlineExceeded) failed.
type kbCountedModel struct{ m kb.Model }

func kbCounted(m kb.Model) kb.Model {
	if m == nil {
		return nil
	}
	return kbCountedModel{m}
}

func (c kbCountedModel) JSONMax(ctx context.Context, system, user string, maxTokens int, out any) error {
	err := c.m.JSONMax(ctx, system, user, maxTokens, out)
	if st, ok := ctx.Value(kbLaneStatsKey{}).(*kbLaneStats); ok {
		switch {
		case errors.Is(err, context.Canceled): // the run was stopped (shutdown): neither answered nor failed
		case ai.IsPaused(err):
			st.putOff.Add(1)
		case err != nil:
			st.calls.Add(1)
			st.failed.Add(1)
		default:
			st.calls.Add(1)
		}
	}
	return err
}
