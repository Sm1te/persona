package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// The fixed part of the morning and evening routines (docs/extensions/bots.md "Morning and evening briefings"): with a task's
// `facts` on, Persona works out what waits for the owner, what happened in the last briefWindow and what is to be
// verified when the run starts, once. The Lark card, the push text and the facts the model is given all come from that one
// computation, so their numbers agree; the model only writes a sentence or two on top.

const (
	// briefWindow is how far back "overnight" looks from the start of the run.
	briefWindow = 12 * time.Hour
	// briefRowsMax is the most rows one section lists; the rest are counted ("and N more").
	briefRowsMax = 10
	// briefModelRows is the most rows of a list the model is given (the counts still say how many there are).
	briefModelRows = 30
	// briefProgressMax is the most tasks "in progress" lists.
	briefProgressMax = 8
)

// briefFleetWait is how many seconds a run waits for the pending cards to be read after a restart (FleetUp) before it
// works out its facts without them.
var briefFleetWait = 20

// briefFacts is what one run of a routine with facts on says, as it was when the run started.
type briefFacts struct {
	At                time.Time
	NorthStar         briefNorthStar
	NorthStarLines    []string
	Confirmations     briefConfirmations
	ConfirmationLines []string
	// l, web and loc are what the rows are said in: the bot's language, the hub's web address and the owner's time zone.
	l   i18n.Lang
	web string
	loc *time.Location
	// Waiting are the board's rows that hold an Agent up (cards waiting for an answer); Accept the board's rows for
	// to-dos waiting to be verified (the same rows, in the same order, as boardRows gives them).
	Waiting []boardRow
	Accept  []boardRow
	// Later are the questions an Agent left to wait for a briefing (later.go), the oldest first, at most briefRowsMax; LaterTotal
	// counts all of them. They are not on the board: nothing is held up by them.
	Later      []boardRow
	LaterTotal int
	// WaitingUnknown: the pending cards were not read yet (the hub had just started), so Waiting is empty for want of
	// knowing and the briefing does not say that nothing waits.
	WaitingUnknown bool
	// Card: the Lark card goes out with this run (the bot is on with its pushes), so the model need not list the rows for the
	// user. Without it the rows are only in the apps' `brief` item, which the web app draws, so the model lists them.
	Card bool
	// Done are the tasks closed in the window (those still waiting to be verified are under Accept only), Failed the
	// ones that did not start and the scheduled runs that failed or were skipped; the totals count all of them (the
	// lists are cut at briefRowsMax and todayFailedMax).
	Done        []todayDone
	DoneTotal   int
	Failed      []todayFailed
	FailedTotal int
	// Progress are the tasks being worked on (their Agents running), the longest running first, cut at briefProgressMax;
	// ProgressTotal counts all of them.
	Progress      []briefProgress
	ProgressTotal int
	// Changed is what each task to verify changed, in the words of its "Please verify" card (by task id).
	Changed map[string]string
	// Asks are the asks of the tasks to verify (by task id): their verdict line and a decision's recommended option, the
	// same as their cards say (conclusion.go).
	Asks map[string]acceptAsk
	// Leaders: one line per current leader (direction_progress.go briefLeaders), for the model only.
	Leaders []prompts.BriefLeader
	// Briefs, BriefsDone: the pieces of work handed out that need the owner or changed since the last briefing, and
	// those that ended since, from their briefs (task_brief_digest.go), for the model only.
	Briefs, BriefsDone []prompts.BriefTask
	// Day: the evening routine's today's all-day recording (briefing_day.go), for the model only; nil for any other
	// run, or without one today.
	Day *prompts.BriefDay
}

// briefFactsAt works out the facts for a run that starts at now. self is the routine that is running: its own earlier
// failed attempt (this run is its retry) is not news.
func (s *Service) briefFactsAt(ctx context.Context, now time.Time, self string) *briefFacts {
	l, web, loc := s.botLang(ctx), s.webBase(ctx), s.location(ctx)
	f := &briefFacts{At: now, l: l, web: web, loc: loc, WaitingUnknown: !s.fleetReady(), Changed: map[string]string{}, Asks: map[string]acceptAsk{}}
	if feat, ready := s.feishuFeatures(ctx); ready && feat.Pushes {
		f.Card = true
	}
	waiting := map[string]bool{}
	for _, r := range s.boardRows(ctx, l, web, now) {
		if r.blocking {
			if !f.WaitingUnknown {
				f.Waiting = append(f.Waiting, r)
			}
		} else {
			f.Accept = append(f.Accept, r)
			waiting[r.id] = true
			if a, ok := s.askCopy(r.id); ok {
				f.Changed[r.id], f.Asks[r.id] = a.Changed, a
			}
		}
	}
	f.Later, f.LaterTotal = s.laterRows(ctx, l, web, now, briefRowsMax)
	since := now.Add(-briefWindow)
	for _, d := range s.todayFinished(ctx, "*", since, loc) {
		if !waiting[d.ID] {
			f.Done = append(f.Done, d)
		}
	}
	f.DoneTotal = len(f.Done)
	if len(f.Done) > briefRowsMax {
		f.Done = f.Done[:briefRowsMax]
	}
	failed, total := s.todayFailures(ctx, "*", since, loc)
	for _, x := range failed {
		if x.Kind == "routine" && x.ID == self {
			total--
			continue
		}
		f.Failed = append(f.Failed, x)
	}
	f.FailedTotal = total
	f.Progress, f.ProgressTotal = s.briefProgress(ctx, waiting)
	f.NorthStar = s.briefNorthStarAt(ctx, now, loc) // what the report opens with (north_star.go)
	f.NorthStarLines = f.NorthStar.lines(l)
	f.Confirmations = s.briefConfirmationsAt(ctx, now, loc)
	f.ConfirmationLines = s.confirmationLines(l, f.Confirmations)
	f.Leaders = s.briefLeaders(ctx, now)
	if self == "evening" { // the day's recap was written for it, 30 minutes before (kb_day.go)
		f.Day = s.briefDay(ctx, now, loc)
	}
	return f
}

// briefProgress are the open tasks whose Agents are working, the longest open first (those waiting to be verified are
// listed there, not here), cut at briefProgressMax, and how many there are.
func (s *Service) briefProgress(ctx context.Context, skip map[string]bool) ([]briefProgress, int) {
	var open []Todo
	for _, t := range s.todosOf(ctx, "*") {
		if t.DoneAt == nil && !skip[t.ID] {
			open = append(open, t)
		}
	}
	slices.SortFunc(open, func(a, b Todo) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	var out []briefProgress
	total := 0
	for _, t := range open {
		if st := taskState(t, s.taskAgents(ctx, t)); st != "working" && st != "waiting" { // under way either way (#3274)
			continue
		}
		total++
		if len(out) < briefProgressMax {
			out = append(out, briefProgress{ID: t.ID, Title: t.Title, Stage: t.Stage, StageKind: t.StageKind})
		}
	}
	return out, total
}

// briefProgress is one task being worked on, and how far (its stage, "" when not known).
type briefProgress struct{ ID, Title, Stage, StageKind string }

// briefRun is the facts of one run of a routine, kept until its turn has been mirrored and pushed: they are computed when
// the run starts and said when it ends, so the card and the push say what the run was given.
type briefRun struct {
	facts    *briefFacts
	label    string // the routine's name
	mirrored bool   // the card was sent (or sending it was given up)
	pushed   bool   // the push text was used
}

// briefBook holds the facts of the runs under way: by task from the start of the run, by conversation once it is sent.
type briefBook struct {
	mu        sync.Mutex
	byTask    map[string]*briefRun
	bySession map[string]*briefRun
}

// briefKeep is how long after its start the facts of a run are still said: the turn of a routine takes minutes, and a
// message the owner sends into its conversation hours later is no briefing.
const briefKeep = 3 * time.Hour

// briefStart works out the facts for the run of t that is starting, and returns them as the model is given them.
func (s *Service) briefStart(ctx context.Context, t Task, now time.Time) string {
	for i := 0; i < briefFleetWait && !s.fleetReady(); i++ { // just after a restart the cards waiting are not read yet
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	run := &briefRun{facts: s.briefFactsAt(ctx, now, t.ID), label: t.label()}
	// What the briefs say since the last briefing, told once: only a real run moves the mark, not a look at the facts.
	run.facts.Briefs, run.facts.BriefsDone = s.briefDigest(ctx, now, s.briefDigestSince(ctx, now), run.facts.loc)
	s.briefDigestTold(ctx, now)
	b := &s.brief
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byTask == nil {
		b.byTask = map[string]*briefRun{}
	}
	b.byTask[t.ID] = run
	return run.facts.modelText()
}

// briefSent: the run of task id went into conversation session; its facts are said when that conversation's turn ends, and
// are written into the conversation as a `brief` item at once (briefing_feed.go), whether or not a Lark card follows. The
// retry of a run that failed (retried) adds no item: the conversation has the first one, and the retry's facts are the
// card's and the model's.
func (s *Service) briefSent(id, session string, retried bool) {
	if run := s.briefAttach(id, session); run != nil && !retried {
		s.briefFeed(session, id, run.label, run.facts)
	}
}

// briefAttach moves the run of task id from the tasks to conversation session, nil when there is none.
func (s *Service) briefAttach(id, session string) *briefRun {
	b := &s.brief
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.byTask[id]
	if run == nil || session == "" {
		return nil
	}
	delete(b.byTask, id)
	if b.bySession == nil {
		b.bySession = map[string]*briefRun{}
	}
	for k, r := range b.bySession { // the old ones go
		if time.Since(r.facts.At) > briefKeep {
			delete(b.bySession, k)
		}
	}
	b.bySession[session] = run
	return run
}

// briefOf is the facts of the run that conversation session is on, nil when it is on none (or the run is too old).
func (s *Service) briefOf(session string) *briefRun {
	b := &s.brief
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.bySession[session]
	if run == nil || time.Since(run.facts.At) > briefKeep {
		return nil
	}
	return run
}

// briefClaim is the facts of the run that conversation session is on, once, for the turn that ended: asked is the message
// that started it and cut whether it was cut off by an error. Only the turn of the run itself (the ⏰ message of
// taskPrompt) that ended normally gets the card; a message the owner sends into the conversation later, or a turn that
// failed (the run is tried again with new facts), is answered as any other. A second turn end of the same run does not
// make another card (a retry of the mirror is not one).
func (s *Service) briefClaim(session, asked string, cut bool) *briefRun {
	run := s.briefOf(session)
	b := &s.brief
	b.mu.Lock()
	defer b.mu.Unlock()
	if run == nil || run.mirrored || cut || !strings.HasPrefix(strings.TrimSpace(stripContext(asked)), "⏰") {
		return nil
	}
	run.mirrored = true
	return run
}

// briefMirror sends the card of a routine with facts in place of its answer's cards (feishuMirror): the model's answer on
// top, the facts under it, one card; sent again when its first post did not get out, as any mirrored answer is.
func (s *Service) briefMirror(ctx context.Context, machine, session, label string, run *briefRun, answer, turn string) {
	card, fallback, shown := s.briefCard(run.facts, run.label, answer)
	card.Buttons(s.openButton(ctx, machine, session))
	sum := sha256.Sum256([]byte("brief\x00" + machine + "\x00" + session + "\x00" + turn + "\x00" + answer))
	m := &mirrorSend{l: run.facts.l, cards: []botAnswerCard{{card: card, fallback: fallback}},
		target: botTarget{Kind: targetPersona, MachineID: machine, SessionID: session}, label: label,
		answer: answer, turn: turn, key: "b" + hex.EncodeToString(sum[:8]),
		sent: func() { s.briefAsked(run.facts, shown) }}
	s.feishuMirrorPost(ctx, m, 0)
}

// briefAsked notes that the briefing card with the Accept buttons of the tasks shown went out: those tasks are not reminded
// that day (remindAcceptance), the card asked for them. Only the round the card was made for counts.
func (s *Service) briefAsked(f *briefFacts, shown []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // not the mirror's: this may run on its retry
	defer cancel()
	asked := map[string]time.Time{}
	for _, r := range f.Accept {
		asked[r.id] = r.asked
	}
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(ctx) {
		return
	}
	for _, id := range shown {
		if a := b.asks[id]; a != nil && a.AskedAt.Equal(asked[id]) {
			a.RemindedAt = time.Now().UTC()
		}
	}
	s.saveAcceptLocked(ctx)
}

// acceptTryFromBrief is the Accept button of a briefing card: it accepts task id when the round the button was made for
// (askedAt, as the card wrote it) is the round that waits now. The card is not one of the ask's own messages, so
// acceptTry cannot tell an old round's button from a current one by its message id. The errors are acceptTry's: a
// decision whose recommendation did not reach the agent, or errAcceptStore (the asks cannot be read or the acceptance
// saved), which is not "another round".
func (s *Service) acceptTryFromBrief(ctx context.Context, id, askedAt string) (bool, error) {
	at, err := time.Parse(timeKeyLayout, askedAt)
	if err != nil {
		return false, nil
	}
	a, err := s.acceptAskLocked(id)
	if err != nil {
		return false, err
	}
	if a == nil || !a.AskedAt.Equal(at) {
		return false, nil
	}
	return s.acceptTry(ctx, id, "")
}

// briefGrace is how long after the morning briefing's time the acceptance reminders wait when that briefing lists the tasks
// to verify itself: if it has not gone out by then they are sent one by one as before.
const briefGrace = 60

// briefingGrace is the minutes remindAcceptance waits after briefingClock (start, in minutes of the day) on the day of local:
// briefGrace when the morning routine has facts on, is due that day and its card can go out (the Lark bot is on with its
// pushes), else none; never past the end of the day.
func (s *Service) briefingGrace(ctx context.Context, local time.Time, start int) int {
	if f, ready := s.feishuFeatures(ctx); !ready || !f.Pushes {
		return 0
	}
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return 0
	}
	defer sc.mu.Unlock()
	day := int(local.Weekday()) // the weekdays of a task are 1 = Monday … 7 = Sunday
	if day == 0 {
		day = 7
	}
	for _, t := range sc.tasks {
		if t.ID == "morning" && t.Enabled && t.Repeat == repeatDaily && t.Facts && hhmm.MatchString(t.Time) &&
			(len(t.Weekdays) == 0 || slices.Contains(t.Weekdays, day)) {
			return max(0, min(briefGrace, 24*60-1-start))
		}
	}
	return 0
}

// modelText is the facts as the model is given them (prompts assistant/routine_facts): the same rows the card lists.
func (f *briefFacts) modelText() string {
	b := prompts.BriefFacts{Hours: int(briefWindow / time.Hour), WaitingTotal: len(f.Waiting), DoneTotal: f.DoneTotal,
		FailedTotal: f.FailedTotal, AcceptTotal: len(f.Accept), LaterTotal: f.LaterTotal, WaitingUnknown: f.WaitingUnknown, Card: f.Card}
	for _, r := range f.Waiting {
		if len(b.Waiting) < briefModelRows {
			b.Waiting = append(b.Waiting, boardRowPlain(f.l, len(b.Waiting)+1, r))
		}
	}
	for _, d := range f.Done {
		b.Done = append(b.Done, clip(oneLine(d.Title), 60))
	}
	for _, x := range f.Failed {
		b.Failed = append(b.Failed, prompts.BriefFailed{Title: clip(oneLine(x.Title), 60), Why: x.Why, Run: x.Kind == "routine", Skipped: x.Skipped})
	}
	b.NorthStar = f.NorthStarLines
	b.ConfirmationSummary = f.Confirmations.summary(f.l)
	b.Confirmations = f.ConfirmationLines
	b.ProgressTotal, b.Leaders = f.ProgressTotal, f.Leaders
	b.Briefs, b.BriefsDone, b.Day = f.Briefs, f.BriefsDone, f.Day
	for _, p := range f.Progress {
		b.Progress = append(b.Progress, prompts.BriefProgress{Title: clip(oneLine(p.Title), 60), Stage: p.Stage})
	}
	for _, r := range f.Accept {
		if len(b.Accept) < briefModelRows {
			b.Accept = append(b.Accept, prompts.BriefAccept{Line: boardRowPlain(f.l, len(b.Accept)+1, r), Changed: f.Changed[r.id]})
		}
	}
	return prompts.RenderBriefFacts(b)
}

// briefPush is the text of the finished push of the routine run that conversation session is on: the counts Persona
// worked out, "" when it is on none or its counts were said already. report: the turn is the one a scheduled run started
// (reportTurn), not a message the owner sent into the conversation later.
func (s *Service) briefPush(session string, l i18n.Lang, report bool) string {
	run := s.briefOf(session)
	b := &s.brief
	b.mu.Lock()
	defer b.mu.Unlock()
	if !report || run == nil || run.pushed {
		return ""
	}
	run.pushed = true
	return run.facts.countsIn(l)
}
