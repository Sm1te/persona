package assistant

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/closeout"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the supervision message ----

// superviseTool marks a queued supervision message (relayMsg.Tool).
const superviseTool = "supervise"

// sendDigest puts the due signals into their supervisors' conversations, one supervision message each: now, or queued
// behind its turn (a message still queued takes these in too). A signal goes to the main conversation unless its task
// is a leader's (digestTo, supervise_leader.go). A signal that no longer holds (answered, done, reviewed meanwhile) is
// dropped.
func (s *Service) sendDigest(ctx context.Context) {
	b := &s.supervise
	b.dedupMu.Lock()
	defer b.dedupMu.Unlock()
	b.mu.Lock()
	b.timer, b.timerAt = nil, time.Time{}
	list := make([]superviseSignal, 0, len(b.pending))
	for _, g := range b.pending {
		list = append(list, g)
	}
	now := time.Now()
	if wait, ok := digestWait(now, b.last, list, s.superviseTimings()); !ok || wait > time.Second {
		if ok {
			s.scheduleDigestLocked(now)
		}
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	here := s.settings(ctx)
	if here.Main == "" || here.MachineID == "" {
		// Nowhere to tell them now (no main conversation yet, or no machine: the hub's hello while the bus is down).
		// They stay pending and untold, and go out on a later try (#5420).
		retry := s.superviseTimings().urgent
		s.logf("supervise: no main conversation; trying again in %s", retry)
		b.mu.Lock()
		s.retryDigestLocked(retry)
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	list = list[:0]
	for _, g := range b.pending { // as they are now: one may have come or gone meanwhile
		list = append(list, g)
	}
	if len(list) == 0 {
		b.mu.Unlock()
		return
	}
	b.pending = map[string]superviseSignal{}
	for _, g := range list {
		b.sent[g.id()] = g.Key
	}
	prev := b.last
	b.last = now
	b.mu.Unlock()

	web := s.webBase(ctx)
	groups := map[string][]superviseSignal{}
	var order []string
	leaders := map[[2]string][]superviseSignal{} // leader agent (machine, session) → the signals of its workers (task_leader.go)
	var leaderOrder []TodoAgent
	gone := map[string]bool{}      // leaders whose conversation is not there: their signals go to the main one
	s.rehomeLeaderlessWorkers(ctx) // a worker whose leader's task closed gets a supervisor that can act, before routing
	for _, g := range list {
		if lead, handled, reachable := s.leaderRoute(g); handled { // never digestTo: a session is no conversation
			if reachable {
				key := [2]string{lead.MachineID, lead.SessionID}
				if _, seen := leaders[key]; !seen {
					leaderOrder = append(leaderOrder, lead)
				}
				leaders[key] = append(leaders[key], g)
				continue
			}
			// Unreachable, not dropped: it becomes a decision for the conversation above the leader (escalateLeaderSignal),
			// which has its own delivery target below.
			g = s.escalateLeaderSignal(ctx, g)
			if g.Todo == "" { // nobody above it: it is told again next time
				continue
			}
		}
		to := s.digestTo(here, g)
		if to != here.Main {
			if _, seen := gone[to]; !seen {
				c, ok := s.session(ctx, here.MachineID, to)
				gone[to] = !ok || !c.Assistant
			}
			if gone[to] {
				to = here.Main
			}
		}
		if _, ok := groups[to]; !ok {
			order = append(order, to)
		}
		groups[to] = append(groups[to], g)
	}
	delivered := false
	for _, lead := range leaderOrder {
		delivered = s.deliverToLeaderAgent(ctx, lead, leaders[[2]string{lead.MachineID, lead.SessionID}], web, now, prev) || delivered
	}
	for _, to := range order {
		delivered = s.deliverDigest(ctx, here, to, groups[to], web, now, prev) || delivered
	}
	if delivered && len(order) > 1 {
		// One message went out: what another left undone (put the last message back to prev) does not undo it.
		b.mu.Lock()
		if b.last.Before(now) {
			b.last = now
		}
		b.mu.Unlock()
	}
}

// deliverDigest puts the signals list into conversation to (the main conversation or a leader) as one supervision
// message; now is when sendDigest marked it as the last message and prev the last message before. True when it went in
// or was queued.
func (s *Service) deliverDigest(ctx context.Context, here Settings, to string, list []superviseSignal, web string, now, prev time.Time) bool {
	b := &s.supervise
	var entries []prompts.SuperviseEntry
	var kept []superviseSignal
	for _, g := range list {
		if e, ok := s.digestEntry(ctx, g, web); ok {
			entries = append(entries, e)
			kept = append(kept, g)
		}
	}
	if len(entries) == 0 {
		return false
	}
	entries, kept, rollback, err := s.reserveSupervision(ctx, entries, kept, now)
	if err != nil {
		s.logf("supervise: deduplication unavailable; retrying: %v", err)
		s.restoreDigest(kept, now, prev)
		return false
	}
	if len(entries) == 0 {
		b.mu.Lock()
		if b.last.Equal(now) {
			b.last = prev
		}
		b.mu.Unlock()
		return false
	}
	accepted := false
	defer func() {
		if !accepted {
			rollback()
			return
		}
		s.parkedDueTold(ctx, kept) // told for good: not again after a restart (parked.go)
	}()
	leader := to != here.Main && s.isLeader(ctx, to)
	render := func(list []prompts.SuperviseEntry) string {
		return prompts.RenderSupervise(prompts.Supervise{Entries: sortEntries(list), Leader: leader, Side: to != here.Main && !leader, Escalated: escalatedIn(list),
			LeaderStuck: slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigLeaderStuck }),
			Overdue:     slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigOverdue }),
			ParkedDue:   slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigParkedDue }),
			Decisions: slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool {
				return e.Kind == sigRecheck || e.Kind == sigUndecidable || e.Kind == sigOwnerCannot
			}),
			Inbox: slices.ContainsFunc(list, func(e prompts.SuperviseEntry) bool { return e.Kind == sigInbox }),
			Asked: askedIn(list), AskMinutes: int(askHoldFor.Minutes())})
	}
	b.mu.Lock()
	queued := b.queued[to]
	b.mu.Unlock()
	msg := relayMsg{From: superviseTool, FromTitle: "督办", Tool: superviseTool, At: now.UTC()}
	if len(queued) > 0 {
		// The last message may still wait in the queue: this one takes its place with both.
		all := mergeSignals(queued, kept)
		var allEntries []prompts.SuperviseEntry
		for _, g := range all {
			if e, ok := s.digestEntry(ctx, g, web); ok {
				allEntries = append(allEntries, e)
			}
		}
		msg.Text = render(allEntries)
		if s.replaceQueuedSupervise(to, msg) {
			accepted = true
			b.mu.Lock()
			b.setQueuedLocked(to, all)
			b.mu.Unlock()
			s.publish(ctx)
			return true
		}
	}
	msg.Text = render(entries)
	if to != here.Main {
		s.logf("supervise: %d signal(s) to conversation %s", len(entries), short(to))
	} else {
		s.logf("supervise: %d signal(s) to the main conversation", len(entries))
	}
	c, ok := s.session(ctx, here.MachineID, to)
	if !ok || c.Status == proto.StatusWorking || c.Starting || s.queuedFor(to) > 0 {
		if !s.queueDigest(to, msg, kept, now, prev) {
			return false
		}
		accepted = true
		s.publish(ctx)
		if ok && c.Status != proto.StatusWorking && !c.Starting {
			s.drainRelays(here.MachineID, []proto.Session{c})
		}
		return true
	}
	if err := s.deliverRelay(ctx, here.MachineID, to, msg); err != nil {
		s.logf("supervise: %v; queued", err)
		if s.queueDigest(to, msg, kept, now, prev) {
			accepted = true
			s.publish(ctx)
		}
		return accepted
	}
	accepted = true
	b.mu.Lock()
	b.setQueuedLocked(to, nil)
	b.mu.Unlock()
	return true
}

// setQueuedLocked keeps list as the signals of the supervision message still queued for conversation to (none: nil);
// b.mu is held.
func (b *superviseBook) setQueuedLocked(to string, list []superviseSignal) {
	if len(list) == 0 {
		delete(b.queued, to)
		return
	}
	if b.queued == nil {
		b.queued = map[string][]superviseSignal{}
	}
	b.queued[to] = list
}

// queueDigest queues msg, the message of signals kept, behind conversation to's turn; at is when sendDigest
// marked it as the last message and prev the last message before. When the relay queue can't be read nothing is
// queued (false): the signals wait again, no longer marked told (a newer one of the same task and kind keeps its
// place), the message still queued for it (supervise.queued) stays as it was, the last message is prev again (nothing went
// out), and the digest is tried again in the urgent gap, or later when the gap after prev has not passed yet.
func (s *Service) queueDigest(to string, msg relayMsg, kept []superviseSignal, at, prev time.Time) bool {
	b := &s.supervise
	if s.queueRelay(to, msg) > 0 {
		b.mu.Lock()
		b.setQueuedLocked(to, kept)
		b.mu.Unlock()
		return true
	}
	retry := s.superviseTimings().urgent
	s.logf("supervise: the relay queue can't be read; %d signal(s) wait, trying again in %s", len(kept), retry)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.init()
	if b.last.Equal(at) { // no later message went out meanwhile
		b.last = prev
	}
	for _, g := range kept {
		id := g.id()
		if b.sent[id] != g.Key {
			continue // a newer one of its kind went out meanwhile
		}
		delete(b.sent, id)
		if cur, ok := b.pending[id]; !ok {
			b.pending[id] = g
		} else if g.Kind == sigReport && cur.Key != g.Key {
			b.pending[id] = joinReports(g, cur) // both reports are told, this one first
		} else if g.Kind == sigInbox && cur.Key != g.Key {
			b.pending[id] = joinInbox(g, cur) // the newer item, with the messages of both
		}
	}
	s.retryDigestLocked(retry)
	return false
}

// mergeSignals is a with b's signals over its own (one per task and kind), in a's order then b's.
func mergeSignals(a, b []superviseSignal) []superviseSignal {
	out := slices.Clone(a)
	for _, g := range b {
		if i := slices.IndexFunc(out, func(x superviseSignal) bool { return x.id() == g.id() }); i >= 0 {
			if g.Kind == sigReport && out[i].Key != g.Key {
				g = joinReports(out[i], g)
			}
			if g.Kind == sigInbox && out[i].Key != g.Key {
				g = joinInbox(out[i], g)
			}
			out[i] = g
		} else {
			out = append(out, g)
		}
	}
	return out
}

// replaceQueuedSupervise: a supervision message still waits in the queue: msg takes its place (true).
func (s *Service) replaceQueuedSupervise(to string, msg relayMsg) bool {
	b := &s.relays
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.relayLocked(context.Background()) {
		return false
	}
	q := b.st.Queue[to]
	for i := range q {
		if q[i].Tool == superviseTool && (i > 0 || !b.draining[to]) {
			q[i] = msg
			s.saveRelayLocked()
			return true
		}
	}
	return false
}

// digestEntry is signal g as the message lists it; false when it no longer holds.
func (s *Service) digestEntry(ctx context.Context, g superviseSignal, web string) (prompts.SuperviseEntry, bool) {
	t, ok := s.todoNow(g.Todo)
	if !ok || t.Supervision == nil || t.DoneAt != nil && g.Kind != sigFailed {
		return prompts.SuperviseEntry{}, false
	}
	v := t.Supervision
	st := v.state(t)
	switch g.Kind {
	case sigReview:
		if st != reviewPending {
			return prompts.SuperviseEntry{}, false
		}
	case sigNeedsYou:
		if !s.taskWaits(ctx, t) {
			return prompts.SuperviseEntry{}, false // answered meanwhile
		}
	case sigDecide:
		if g.Unreachable {
			// The leader could not be reached: the worker's own state is not what decides this (the leader's task is still open).
		} else if st != reviewNeedsUser {
			return prompts.SuperviseEntry{}, false // decided meanwhile
		}
	case sigEscalated:
		if st != reviewNeedsUser && len(g.Cards) == 0 { // a leader's escalated cards are told as they were
			return prompts.SuperviseEntry{}, false // decided meanwhile
		}
	case sigOverdue:
		if b, ok := s.briefOfTodo(t.ID); !ok || b.Overdue == nil || st == reviewNeedsUser || st == reviewParked {
			return prompts.SuperviseEntry{}, false // re-scoped, with the owner, or parked meanwhile
		}
	case sigIdle, sigUnjudged, sigStalled:
		if st == reviewParked {
			return prompts.SuperviseEntry{}, false // parked meanwhile: it waits on purpose (parked.go)
		}
	case sigParkedDue:
		if r := v.parking(); st != reviewParked || r == nil || parkKey(r) != g.Key {
			return prompts.SuperviseEntry{}, false // its agent works again, or it was reviewed again, meanwhile
		}
	case sigOrphaned:
		if o, ok := s.orphanOf(t.ID); !ok || o.key != g.Key {
			return prompts.SuperviseEntry{}, false // an agent is back at it, or it is another stretch by now
		}
	case sigUndecidable:
		if st != reviewNeedsUser {
			return prompts.SuperviseEntry{}, false // decided or reviewed again meanwhile
		}
	case sigRecheck:
		if a, ok := s.askCopy(t.ID); st != reviewNeedsUser || !ok || !a.AcceptedAt.IsZero() ||
			"recheck@"+a.AskedAt.UTC().Format(time.RFC3339Nano) != g.Key {
			return prompts.SuperviseEntry{}, false // answered, or asked again, meanwhile
		}
	case sigInbox:
		if g.Inbox == nil || !s.inboxSignalHolds(t) || !s.inboxSignalsOn(ctx) {
			return prompts.SuperviseEntry{}, false // waits for the owner's decision, or the switch went off, meanwhile
		}
	}
	var asks []prompts.SuperviseAsk
	if g.Kind == sigAsk {
		if asks = s.heldAsks(t.ID, g.To); len(asks) == 0 {
			return prompts.SuperviseEntry{}, false // answered, or gone up a rung, meanwhile
		}
	}
	e := prompts.SuperviseEntry{ID: t.ID, Title: t.Title, Kind: g.Kind, Detail: g.Detail, Minutes: g.Minutes, DoneWhen: v.DoneWhen,
		Round: v.Round, Link: todoLink(web, t.ID), Quiet: t.Quiet}
	if a := v.AutoReview; g.Kind == sigReview && a != nil && a.Round == v.Round && (a.State == autoClean || a.State == autoBlocking || a.State == autoFailed) {
		e.Review = &prompts.SuperviseReviewed{State: a.State, Count: a.Count, Blocking: a.Blocking, Findings: a.Findings, Error: a.Error}
	}
	s.briefEntry(ctx, t.ID, &e) // what was asked, in the owner's words (task_brief_digest.go)
	if g.Kind == sigEscalated {
		e.Recommend = g.Ask // the leader's recommendation (escalate)
		e.Asks = g.Cards
	}
	if g.Kind == sigDecide {
		e.Recommend = g.Ask // the task leader's recommendation
		e.LeaderGone = g.Unreachable
	}
	if g.Kind == sigAsk {
		e.Asks, e.Decisions = asks, s.todoDecisionsContext(ctx, t.ID) // what was settled already, not to be gone against
	}
	if g.Kind == sigReport { // what it said is the report itself, not a reason in brackets
		e.Detail, e.Report, e.Said, e.Evidence, e.Step, e.Steps = "", g.Report, g.Detail, g.Evidence, g.Step, g.Steps
	}
	// Progress reports stay quiet. Their persisted evidence accompanies the next
	// real supervision event, even after restart or a report of a different kind.
	for i, c := range v.Criteria {
		if c.At != nil {
			e.Evidence = append(e.Evidence, fmt.Sprintf("criterion %d [%s] %s — %s (%s at %s)",
				i+1, c.State, c.Criterion, c.Evidence, c.By, c.At.UTC().Format(time.RFC3339)))
		}
	}
	if g.Kind == sigOrphaned {
		e.Orphan = g.Orphan
	}
	if g.Kind == sigRecheck || g.Kind == sigOwnerCannot {
		e.Decision = g.Decision
	}
	if g.Kind == sigParkedDue { // what it waited for is the signal, not a reason in brackets
		e.Detail, e.Parked = "", s.parkedEntry(ctx, v.parking())
	}
	if g.Kind == sigOwnerCannot { // the owner's answer is the signal, not a reason in brackets
		e.Detail, e.Response = "", g.Detail
	}
	if g.Kind == sigFailed {
		s.remedyEntry(ctx, t, &e) // its kept tab, and the turn that hears of it (start_remedy.go)
	}
	if g.Kind == sigInbox {
		in := *g.Inbox
		e.Inbox = &in
	}
	b := &s.supervise
	b.mu.Lock()
	e.Ask = cmp.Or(g.Ask, b.asks[t.ID])
	b.mu.Unlock()
	if g.Kind != sigNeedsYou {
		e.Ask = ""
	}
	// Where its agent is and what it said last: the whole last answer when it stopped, a line while it works.
	agents := s.taskAgents(ctx, t)
	var last proto.Session
	for _, c := range agents {
		if g.Session != "" && c.SessionID == g.Session || g.Session == "" && c.UpdatedAt.After(last.UpdatedAt) {
			last = c
		}
	}
	if last.SessionID != "" {
		e.Session = short(last.SessionID)
		e.AgentID = last.SessionID
		if m, ok := s.device(ctx, last.MachineID); ok {
			e.Machine = m.Name
		}
		text := strings.TrimSpace(last.LastText)
		if g.Kind == sigReport {
			text = "" // it said what it had to in the report
		} else if g.Kind == sigReview || g.Kind == sigIdle || g.Kind == sigUnjudged {
			if _, answer := s.lastTurn(ctx, last.MachineID, last.SessionID); answer != "" {
				text = answer
			}
		}
		if c, arts, ok := closeout.Parse(text); ok {
			e.Closeout = &prompts.SuperviseCloseout{Status: c.Status, Waiting: c.Waiting, Merged: c.Merged, Deployed: c.Deployed,
				Did: c.Did, Verified: c.Verified, Missing: c.Missing}
			if s.checkCloseout(ctx, &c, arts); c.Checked != nil {
				e.Closeout.CheckedMerged, e.Closeout.CheckedDeployed = c.Checked.Merged, c.Checked.Deployed
			}
			for _, a := range arts {
				e.Closeout.Artifacts = append(e.Closeout.Artifacts, a.Kind+" "+a.Ref)
			}
			text = closeout.Strip(text) // told above, in a form that cannot be cut in the middle
		}
		e.Last = clipMiddle(text, 600)
	} else if g.Machine != "" {
		e.Machine = g.Machine
	}
	return e, true
}

// sortEntries puts what waits on a decision first.
func sortEntries(list []prompts.SuperviseEntry) []prompts.SuperviseEntry {
	rank := map[string]int{sigNeedsYou: 0, sigAsk: 0, sigEscalated: 0, sigDecide: 0, sigOwnerCannot: 0, sigUndecidable: 1, sigRecheck: 1, sigParkedDue: 2, sigIdle: 2, sigUnjudged: 2, sigFailed: 3, sigStarted: 3, sigInterrupted: 4, sigOrphaned: 4, sigReview: 5, sigStalled: 6, sigLeaderStuck: 6, sigOverdue: 6, sigInbox: 6, sigReport: 7}
	at := func(e prompts.SuperviseEntry) int {
		switch {
		case e.Kind != sigReport:
		case e.Report == upReportQuestion || e.Report == upReportBlocked: // waits on a decision too
			return 1
		case e.Report == upReportDone: // with the work waiting for review
			return rank[sigReview]
		}
		return rank[e.Kind]
	}
	sort.SliceStable(list, func(i, j int) bool { return at(list[i]) < at(list[j]) })
	return list
}

// ---- the main conversation's pushes ----

// holdPushes: a supervision message went into conversation id: its turn's normal finish is not pushed (the hub and the
// channels read it off the snapshot, SessionView.Supervising) until the user or anything else speaks there.
func (s *Service) holdPushes(ctx context.Context, id string) {
	b := &s.supervise
	b.mu.Lock()
	b.init()
	b.hold[id] = time.Now()
	delete(b.visible, id)
	b.mu.Unlock()
	s.publish(ctx)
}

// holdPushesParam marks an assistant.send whose supervision turn holds the conversation's pushes (holdPushes).
// Any other send releases them (call); scheduled inputs carry their own persisted push mode.
const holdPushesParam = "hold_pushes"

// releasePushes: someone else speaks in conversation id: its turns are pushed as ever.
func (s *Service) releasePushes(ctx context.Context, id string) {
	b := &s.supervise
	b.mu.Lock()
	_, held := b.hold[id]
	delete(b.hold, id)
	delete(b.visible, id)
	b.mu.Unlock()
	if held {
		s.publish(ctx)
	}
}

// supervising: conversation id's current turn was started by a supervision message.
func (s *Service) supervising(id string) bool {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.hold[id]
	return ok
}

func (s *Service) heldConversations() []string {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.hold))
	for id := range b.hold {
		out = append(out, id)
	}
	return out
}
