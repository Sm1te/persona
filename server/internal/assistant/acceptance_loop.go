package assistant

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

// acceptMorningHour is when, in the user's zone, the cards not accepted the day before are posted again, when the
// morning briefing (the "Morning briefing" routine) is off or gone; while it is on, its own time.
const acceptMorningHour = 9

// acceptLoop posts the cards that did not go out when their ask was made (retryAcceptance), and again, each morning, the
// cards not accepted by then (once a day each); every minute it handles the decisions past their deadline.
func (s *Service) acceptLoop(ctx context.Context) {
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	due := time.NewTicker(decisionSweepEvery) // decisions past their deadline (decision_deadline.go)
	defer due.Stop()
	// The held tasks' deploys, every minute (cheap: the hub's health; GitHub only once the hub's commit changed).
	deploys := time.NewTicker(time.Minute)
	defer deploys.Stop()
	s.verifySupervisedAsks(ctx) // the asks its supervisor passed while this hub was older (#3240)
	for {
		select {
		case <-ctx.Done():
			return
		case <-due.C:
			s.goSweepDecisionDeadlines(time.Now())
		case <-deploys.C:
			s.sweepDeploys(time.Now()) // held tasks whose deploy landed (deploy_check.go)
		case <-tick.C:
			now := time.Now()
			s.verifySupervisedAsks(ctx)
			s.retryAcceptance(now)
			s.remindAcceptance(now)
		}
	}
}

// retryAcceptance posts the card of each ask still waiting for one (acceptCardDue), under the round's key, so a try that
// landed unseen is not posted twice. The round guards are a reminder's: an ask whose todo is gone or ended without the
// hub's auto-judge, or that is from before its latest reopen, is dropped, never posted. With the bot off or its reports
// off no card is due, and past acceptPostWindow none is tried: either way the ask stays card-less (NoCard), still
// listed. A status that cannot be read, or a send that fails, waits for the next tick.
func (s *Service) retryAcceptance(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var due []string
	seen := map[string]time.Time{} // each due ask's AskedAt: the round the sweep judges
	for id, a := range b.asks {
		if acceptCardDue(a) {
			due = append(due, id)
			seen[id] = a.AskedAt
		}
	}
	b.mu.Unlock()
	if len(due) == 0 {
		return // nothing waits: the bot's status is not even asked for
	}
	slices.Sort(due)
	st, stErr := s.feishuStatus(ctx)
	for _, id := range due {
		t, ok := s.todoNow(id)
		if f := s.acceptSweepHook; f != nil {
			f(id)
		}
		b.mu.Lock()
		a := b.asks[id]
		if !acceptCardDue(a) || !a.AskedAt.Equal(seen[id]) {
			// Gone, passed, or a new round asked while the book was unlocked (the todo read is from before it).
			b.mu.Unlock()
			continue
		}
		if !ok || acceptAskEnded(a, t) || acceptAskStale(a, t) {
			// The todo is gone, it ended without the hub's auto-judge, or the ask is from before its latest reopen: it
			// can never be passed, so its card is not posted and the ask is dropped (as remindAcceptance does).
			delete(b.asks, id)
			s.saveAcceptLocked(ctx)
			b.mu.Unlock()
			s.boardChanged()
			continue
		}
		late := now.Sub(maxTime(a.AskedAt, a.PushedAt)) > acceptPostWindow // PushedAt: held for a briefing first (decision_wait.go)
		if late || stErr == nil && !(st.Ready && st.Features.Reports) {
			if late {
				s.logf("task %s: acceptance: its card did not go out within %v: kept without one", id, acceptPostWindow)
			}
			a.NoCard = true
			s.saveAcceptLocked(ctx)
			b.mu.Unlock()
			continue
		}
		if stErr != nil {
			b.mu.Unlock()
			continue
		}
		cp := *a
		b.mu.Unlock()
		mid, err := s.botPostKeyed(ctx, "", s.acceptCard(ctx, t, cp, false), askSummary(s.botLang(ctx), &cp), acceptCardKey(id, cp.AskedAt))
		if err != nil {
			s.logf("task %s: acceptance: %v (tried again on the next tick)", id, err)
			continue
		}
		b.mu.Lock()
		var done *acceptAsk
		if a := b.asks[id]; a != nil && a.AskedAt.Equal(cp.AskedAt) {
			a.MessageID, a.Messages = mid, append(a.Messages, mid)
			if !a.AcceptedAt.IsZero() {
				c := *a // passed while the card went out: it says so too
				done = &c
			}
		} else if a != nil {
			// Another round asks now (the todo reopened and deployed again meanwhile): this card is an earlier round's.
			a.Earlier = lastOf(append(slices.Clone(a.Earlier), mid), acceptEarlierMost)
		}
		s.saveAcceptLocked(ctx)
		b.mu.Unlock()
		if done != nil {
			if err := s.botUpdateCard(ctx, mid, s.acceptCard(ctx, t, *done, false)); err != nil {
				s.logf("task %s: acceptance card: %v", id, err)
			}
		}
	}
}

func (s *Service) remindAcceptance(now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	loc := s.location(ctx)
	local := now.In(loc)
	hh, mm := s.briefingClock(ctx)
	if local.Hour()*60+local.Minute() < hh*60+mm+s.briefingGrace(ctx, local, hh*60+mm) {
		return
	}
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var due []string
	seen := map[string]time.Time{} // each due card's AskedAt: the round the sweep judges
	for id, a := range b.asks {
		// A card again only for an ask that has a card; a card-less one (NoCard) is listed by the briefing and the board,
		// and so is a conclusion: it was pushed once, and the next pushes are the briefing's (#1226).
		if a.MessageID != "" && !a.Conclusion && a.AcceptedAt.IsZero() && a.AskedAt.Before(today) && a.RemindedAt.Before(today) {
			due = append(due, id)
			seen[id] = a.AskedAt
		}
	}
	b.mu.Unlock()
	if len(due) == 0 {
		return
	}
	if f, ready := s.feishuFeatures(ctx); !ready || !f.Reports {
		return
	}
	slices.Sort(due)
	for _, id := range due {
		t, ok := s.todoNow(id)
		if f := s.acceptSweepHook; f != nil {
			f(id)
		}
		b.mu.Lock()
		a := b.asks[id]
		if a == nil || !a.AskedAt.Equal(seen[id]) {
			// Gone, or a new round asked while the book was unlocked (the todo read is from before it): not this card.
			b.mu.Unlock()
			continue
		}
		if !ok || acceptAskEnded(a, t) || acceptAskStale(a, t) {
			// The todo is gone, it ended without the hub's auto-judge (the owner closed it or a review did), or the
			// card is from before its latest reopen: it can never be passed, so stop asking and drop it.
			delete(b.asks, id)
			s.saveAcceptLocked(ctx)
			b.mu.Unlock()
			s.boardChanged()
			continue
		}
		cp := *a
		b.mu.Unlock()
		mid, err := s.botPost(ctx, "", s.acceptCard(ctx, t, cp, true), cp.Changed)
		if err != nil {
			s.logf("task %s: acceptance reminder: %v", id, err)
			return
		}
		b.mu.Lock()
		var done *acceptAsk
		if a := b.asks[id]; a != nil {
			a.RemindedAt = now.UTC()
			a.Messages = append(a.Messages, mid)
			if !a.AcceptedAt.IsZero() {
				cp := *a // passed while the reminder went out: that card says so too
				done = &cp
			}
		}
		s.saveAcceptLocked(ctx)
		b.mu.Unlock()
		if done != nil {
			if err := s.botUpdateCard(ctx, mid, s.acceptCard(ctx, t, *done, false)); err != nil {
				s.logf("task %s: acceptance card: %v", id, err)
			}
		}
	}
}

// briefingClock is when the morning briefing goes out (the enabled "Morning briefing" routine's time), else acceptMorningHour: the
// reminders come with it, and the briefing lists what waits (snapshotUnaccepted).
func (s *Service) briefingClock(ctx context.Context) (int, int) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return acceptMorningHour, 0
	}
	defer sc.mu.Unlock()
	for _, t := range sc.tasks {
		if t.ID != "morning" || !t.Enabled || t.Repeat != repeatDaily {
			continue
		}
		if m := hhmm.FindStringSubmatch(t.Time); m != nil {
			hh, _ := strconv.Atoi(m[1])
			mm, _ := strconv.Atoi(m[2])
			return hh, mm
		}
	}
	return acceptMorningHour, 0
}

// snapshotUnaccepted are the deployed tasks still waiting for the user to check them, the oldest eight, and how many
// there are (the snapshot's "waiting for the user to verify", which the morning briefing brings up).
func (s *Service) snapshotUnaccepted(ctx context.Context, loc *time.Location) ([]prompts.SnapshotAccept, int) {
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return nil, 0
	}
	type ask struct {
		id string
		a  acceptAsk
	}
	var list []ask
	for id, a := range b.asks {
		if !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() { // asked (a claim still going out has no time), card or not
			list = append(list, ask{id, *a})
		}
	}
	b.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].a.AskedAt.Before(list[j].a.AskedAt) })
	var out []prompts.SnapshotAccept
	var ended []ask
	total := 0
	for _, x := range list {
		t, ok := s.todoNow(x.id)
		if f := s.acceptSweepHook; f != nil {
			f(x.id)
		}
		if !ok {
			continue
		}
		if acceptAskEnded(&x.a, t) || acceptAskStale(&x.a, t) {
			ended = append(ended, x) // closed by the owner or a review, or asked before the todo's latest reopen
			continue
		}
		total++
		if len(out) < 8 { // the oldest eight; the total says how many more
			out = append(out, prompts.SnapshotAccept{Title: clip(oneLine(t.Title), 40), Verdict: clip(oneLine(verdictText(s.botLang(ctx), &x.a)), 120),
				Changed: x.a.Changed, Link: x.a.Link, Since: x.a.AskedAt.In(loc)})
		}
	}
	if len(ended) > 0 {
		b.mu.Lock()
		removed := false
		for _, x := range ended {
			// Only the card judged: a new round asked while the book was unlocked has another AskedAt and stays.
			if cur := b.asks[x.id]; cur != nil && cur.AskedAt.Equal(x.a.AskedAt) {
				delete(b.asks, x.id)
				removed = true
			}
		}
		if removed {
			s.saveAcceptLocked(ctx)
		}
		b.mu.Unlock()
	}
	return out, total
}
