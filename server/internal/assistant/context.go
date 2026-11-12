package assistant

import (
	"cmp"
	"context"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/connector"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
)

// Context is the <persona-context> block a message to conversation p.SessionID starts with (the node asks on every
// message, node/assistant_context.go): who the user is, whether this is the main conversation, and the snapshot.
// With p.Ack it only confirms what a delivered message carried: its view errors (context_view_errors.go) and the
// background supervisor's announcement.
func (s *Service) Context(ctx context.Context, p ContextParams) ContextResult {
	if len(p.Ack) > 0 {
		// The background supervisor's announcement is confirmed the same way (supervise_background.go).
		if p.Ack = s.announcedBackground(ctx, p.SessionID, p.Ack); len(p.Ack) > 0 {
			s.ackViewErrors(ctx, p)
		}
		return ContextResult{}
	}
	c := prompts.AssistantContext{Snapshot: s.contextSnapshot(ctx), Owner: s.owner(ctx),
		Main: s.isMain(ctx, p.SessionID), Leader: s.isLeader(ctx, p.SessionID), Background: s.isBackground(ctx, p.SessionID)}
	switch {
	case c.Main:
		cfg := s.settings(ctx)
		c.BackgroundStarted = s.announceBackground(ctx) // until a message carrying it is delivered: its tasks are followed up in the background now (supervise_background.go)
		names := s.leaderNames(ctx, cfg.Leaders)
		for _, id := range cfg.Leaders {
			if id != "" && id != cfg.Main {
				c.Directions = append(c.Directions, prompts.ContextDirection{ID: id, Name: names[id]})
			}
		}
	case c.Leader:
		c.LeaderName = s.leaderNames(ctx, []string{p.SessionID})[p.SessionID]
	}
	text := prompts.RenderAssistantContext(c)
	errs, receipts := s.viewErrors(ctx, p)
	if c.BackgroundStarted != "" {
		receipts = append(receipts, assistantwire.ViewErrorReceipt{ItemID: backgroundAnnounceReceipt, Digest: c.BackgroundStarted})
	}
	return ContextResult{Text: text, ViewErrors: errs, Receipts: receipts}
}

// contextState is the last snapshot taken for the live context.
type contextState struct {
	mu       sync.Mutex
	snap     *prompts.Snapshot
	at       time.Time
	snapping chan struct{} // closed when the snapshot being taken is in
}

// contextSnapshot is the live snapshot each message to the assistant starts with: the time, which machines are up,
// agents waiting for the user, unread Feishu chats and the rest of today's meetings. It is taken on every message, so
// it must not hold the message up: the sources are read at most every snapshotFresh, a message waits for a new read
// at most snapshotWait and otherwise goes with the last one (the time is always now). Each source has a short
// deadline; a slow one is left out.
func (s *Service) contextSnapshot(ctx context.Context) string {
	cs := &s.live
	cs.mu.Lock()
	snap, at := cs.snap, cs.at
	var wait chan struct{}
	if snap == nil || time.Since(at) >= snapshotFresh {
		if wait = cs.snapping; wait == nil {
			wait = make(chan struct{})
			cs.snapping = wait
			go s.takeSnapshot(wait)
		}
	}
	cs.mu.Unlock()
	if wait != nil {
		limit := snapshotWait
		if snap == nil || time.Since(at) > snapshotStale {
			limit = snapshotSlowest // nothing usable yet: wait for the sources' own deadlines
		}
		select {
		case <-wait:
		case <-time.After(limit):
		case <-ctx.Done():
		}
		cs.mu.Lock()
		snap = cs.snap
		cs.mu.Unlock()
	}
	if snap == nil {
		return ""
	}
	out := *snap
	out.Now = time.Now().In(s.location(ctx))
	out.ModelProfile = s.modelProfileContext(ctx)
	return fitSnapshot(out)
}

const (
	snapshotFresh   = 45 * time.Second
	snapshotWait    = 1500 * time.Millisecond
	snapshotStale   = 30 * time.Minute
	snapshotSlowest = 5 * time.Second

	// snapshotBudget: characters of rendered snapshot one message may carry (#3203). The block stays in the
	// conversation, so its size is what every message adds to the context until the next compaction; the node sends
	// only the sections that changed, but after a compaction or a restart it sends all of them again.
	snapshotBudget = 6000
)

// snapshotOmits are the sections fitSnapshot leaves out, the least needed first. Never left out: the agents waiting
// for the user (needs you, awaiting a reply), the running workflows (an agent waiting for one has not finished), the
// plan cards waiting for "Start" and the failed scheduled runs (no tool lists them), the calendar and the chats that
// @ the user.
var snapshotOmits = []func(*prompts.SnapshotOmit){
	func(o *prompts.SnapshotOmit) { o.Waiting = true },
	func(o *prompts.SnapshotOmit) { o.Routines = true },
	func(o *prompts.SnapshotOmit) { o.Followups = true },
	func(o *prompts.SnapshotOmit) { o.OtherChats = true },
	func(o *prompts.SnapshotOmit) { o.Working = true },
	func(o *prompts.SnapshotOmit) { o.KB = true },
	func(o *prompts.SnapshotOmit) { o.Asked = true },
	func(o *prompts.SnapshotOmit) { o.Unaccepted = true },
	func(o *prompts.SnapshotOmit) { o.Tasks = true },
	func(o *prompts.SnapshotOmit) { o.Unread = true },
	func(o *prompts.SnapshotOmit) { o.Approvals = true },
}

// fitSnapshot is s rendered within snapshotBudget: as it is when it fits, else with sections left out in
// snapshotOmits' order until it does (each keeps its heading and count: prompts.SnapshotOmit), else cut short. It
// changes nothing but s.Omit: s shares its lists with the cached snapshot other messages render at the same time,
// and the free text in them was clipped where the snapshot was taken.
func fitSnapshot(s prompts.Snapshot) string {
	text := prompts.RenderSnapshot(s)
	for _, omit := range snapshotOmits {
		if utf8.RuneCountInString(text) <= snapshotBudget {
			return text
		}
		omit(&s.Omit)
		text = prompts.RenderSnapshot(s)
	}
	if utf8.RuneCountInString(text) <= snapshotBudget {
		return text
	}
	return cutSnapshot(text)
}

// cutSnapshot is text cut short, with the line saying so, within snapshotBudget: for what no section can be left out
// of. The agents waiting for the user come first in the template, so they are what stays; the cut is at a line break
// unless that would lose more than half the block (one agent list is one line).
func cutSnapshot(text string) string {
	tail := "\n" + prompts.SnapshotCut()
	head := textfmt.Cut(text, snapshotBudget-utf8.RuneCountInString(tail))
	if i := strings.LastIndexByte(head, '\n'); i > len(head)/2 {
		head = head[:i]
	}
	return head + tail
}

// takeSnapshot reads the sources and keeps the result for contextSnapshot; done is closed when it is in.
func (s *Service) takeSnapshot(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotSlowest)
	defer cancel()
	loc := s.location(ctx)
	now := time.Now().In(loc)
	devices, phone, apps := s.devicesAndPhone(ctx)
	snap := prompts.Snapshot{Now: now, Zone: loc.String(), PhoneOnline: phone, Apps: snapshotApps(apps)}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // one ask for both sections: each connector builds its chats and meetings in one answer
		defer wg.Done()
		live := s.connectorSnapshots(ctx)
		snap.Chats, snap.Calendar = snapshotChats(live), snapshotCalendar(live, now)
	}()
	for _, m := range devices {
		snap.Machines = append(snap.Machines, prompts.SnapshotMachine{Name: m.Name, ID: m.ID, Online: m.Online})
	}
	web := s.webBase(ctx) // the agents' and tasks' links (#2194)
	snap.Needs, snap.Unread, snap.Awaiting, snap.Waiting, snap.Done, snap.Muted, snap.Working, snap.Workflows = s.snapshotAgents(ctx, devices, web)
	snap.Tasks = s.snapshotTasks(ctx, web)
	snap.Approvals = s.snapshotApprovals(ctx, web)
	snap.Unaccepted, snap.UnacceptedTotal = s.snapshotUnaccepted(ctx, loc)
	snap.Plans, snap.PlansTotal = s.snapshotPlans(ctx, loc, now)
	snap.Asked = s.snapshotAsked(ctx)
	if e, err := s.kbGet(ctx); err == nil {
		snap.KB = e.Prompt(6, 8)
	}
	snap.Followups, snap.FollowupsTotal, snap.Routines, snap.RoutinesTotal, snap.Missed, snap.MissedTotal = s.snapshotSchedule(ctx, loc, now)
	wg.Wait()

	cs := &s.live
	cs.mu.Lock()
	cs.snap, cs.at, cs.snapping = &snap, time.Now(), nil
	cs.mu.Unlock()
	close(done)
}

// snapshotStale makes the next message take a new snapshot (what it holds just changed).
func (s *Service) snapshotStale() {
	cs := &s.live
	cs.mu.Lock()
	cs.at = time.Time{}
	cs.mu.Unlock()
}

// ProfileChanged makes the next message take a new snapshot (the time zone changed), and the settings be read again
// (the hub's defaults for them changed, or someone changed them behind the assistant's back).
func (s *Service) ProfileChanged(context.Context) {
	cs := &s.live
	cs.mu.Lock()
	cs.snap, cs.at = nil, time.Time{}
	cs.mu.Unlock()
	s.prefs.mu.Lock()
	s.prefs.at = time.Time{}
	s.prefs.mu.Unlock()
}

type snapshotAgentGroupKind string

const (
	snapshotAgentOutside  snapshotAgentGroupKind = ""
	snapshotAgentNeedsYou snapshotAgentGroupKind = "needs_you"
	snapshotAgentWorking  snapshotAgentGroupKind = "working"
	snapshotAgentAwaiting snapshotAgentGroupKind = "awaiting"
	snapshotAgentWaiting  snapshotAgentGroupKind = "waiting"
	snapshotAgentDone     snapshotAgentGroupKind = "done"
	snapshotAgentUnread   snapshotAgentGroupKind = "unread"
)

// snapshotAgentGroup is the live snapshot category a session belongs in before muted sessions are separated. The
// empty value means it is not in the snapshot; done and the time-limited categories use UpdatedAt, as the snapshot
// always has (not Work.TurnAt).
func snapshotAgentGroup(c proto.Session, now time.Time) snapshotAgentGroupKind {
	recent := now.Sub(c.UpdatedAt) < 24*time.Hour
	w := c.Work
	switch {
	case c.State == proto.StateNeedsYou:
		return snapshotAgentNeedsYou
	case c.State == proto.StateWorking:
		return snapshotAgentWorking
	case c.AwaitsReply && c.State == proto.StateIdle && recent:
		return snapshotAgentAwaiting // seen, its question unanswered (#5096)
	// A decision with no card up (cards off, muted, "Not now") is not "Needs you": the lists, the badges and the Live
	// Activity go by the state (docs/protocol/agent-states.md), so it stays "Unread" with what it waits for, or is seen.
	case w != nil && w.Phase == proto.WorkBlocked && w.On != proto.WaitUser && c.State != proto.StateUnread && recent:
		return snapshotAgentWaiting // the hub waits it out (an unread one is left for the user)
	case w != nil && w.Phase == proto.WorkDone && recent:
		return snapshotAgentDone
	case c.State == proto.StateUnread && recent:
		return snapshotAgentUnread
	default:
		return snapshotAgentOutside
	}
}

// snapshotAgents sorts the agents by what they need: the work state (agents_work.go, docs/assistant/agent-work.md) says what a stopped one
// waits for or concluded. Each has its page in the web app at web ("" for none). The muted ones (the agent or its
// space) are in none of the others: the Live Activity, the Agents badge and the inbox leave them out
// (docs/apple/live-activity.md), so "Needs you" here does too; they are told apart, for when the user asks about them (#4562).
func (s *Service) snapshotAgents(ctx context.Context, devices []Device, web string) (needs, unread, awaiting, waiting, done, muted []prompts.SnapshotAgent,
	working []prompts.SnapshotAgent, workflows []prompts.SnapshotWorkflow) {
	now := time.Now()
	for _, m := range devices {
		list, off, _ := s.sessionsMuted(ctx, m.ID)
		for _, c := range list {
			if c.Assistant {
				continue
			}
			a := prompts.SnapshotAgent{Label: agentLabel(c), Machine: m.ID, Session: short(c.SessionID),
				Link: sessionLink(web, m.ID, c.SessionID, false), Ultracode: ultracodeState(c.Ultracode), Plan: snapshotPlan(c)}
			for _, w := range runningWorkflows(c, now) {
				w.Agent, w.Machine, w.Session = a.Label, a.Machine, a.Session
				workflows = append(workflows, w)
			}
			w := c.Work
			if w != nil && w.Phase != proto.WorkWorking {
				a.Line, a.Closeout = w.Line, snapshotCloseout(w)
			}
			if a.Line == "" && c.AwaitsReply {
				a.Line = c.Asks // what it asks (#5096)
			}
			a.Line = clip(oneLine(a.Line), 160)
			group := snapshotAgentGroup(c, now)
			if group == snapshotAgentOutside {
				continue
			}
			if off[c.SessionID] {
				muted = append(muted, a)
				continue
			}
			switch group {
			case snapshotAgentNeedsYou:
				needs = append(needs, a)
			case snapshotAgentWorking:
				working = append(working, a)
			case snapshotAgentAwaiting:
				awaiting = append(awaiting, a)
			case snapshotAgentWaiting:
				waiting = append(waiting, a)
			case snapshotAgentDone:
				done = append(done, a)
			case snapshotAgentUnread:
				unread = append(unread, a)
			}
		}
	}
	return needs, unread, awaiting, waiting, done, muted, working, workflows
}

// runningWorkflows are c's background workflow runs still running, as the snapshot tells them (#2935).
func runningWorkflows(c proto.Session, now time.Time) []prompts.SnapshotWorkflow {
	var out []prompts.SnapshotWorkflow
	for _, w := range c.Workflows {
		if !w.Running {
			continue
		}
		sw := prompts.SnapshotWorkflow{Name: cmp.Or(w.Name, w.Summary, w.TaskID), Phase: w.Phase, PhaseIndex: w.PhaseIndex,
			Phases: len(w.Phases), Done: w.AgentsDone, Started: w.AgentsStarted}
		if d := now.Sub(w.StartedAt); !w.StartedAt.IsZero() && d >= time.Minute {
			sw.For = humanDuration(d)
		}
		out = append(out, sw)
	}
	return out
}

// connectorSnapshots are the live states of the hub's signed-in connectors that keep one (connector.Snapshot: a chat
// service through a plugin; app.call connectors.snapshots), waited for 3 seconds; those that do not answer in time are
// left out.
func (s *Service) connectorSnapshots(ctx context.Context) []connector.SnapshotData {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var res struct {
		Snapshots []connector.SnapshotData `json:"snapshots"`
	}
	if err := s.appCall(ctx, AppConnectors, ConnectorsSnapshots, map[string]any{"tz": s.location(ctx).String()}, &res); err != nil {
		return nil
	}
	return res.Snapshots
}

// snapshotChats is the unread chats in the connectors' live states, nil when none answered.
func snapshotChats(live []connector.SnapshotData) *prompts.SnapshotChats {
	var out *prompts.SnapshotChats
	for _, d := range live {
		if d.Chats == nil {
			continue
		}
		if out == nil {
			out = &prompts.SnapshotChats{}
		}
		for _, c := range d.Chats.AtMe {
			out.AtMe = append(out.AtMe, prompts.SnapshotChat{Name: c.Name, Unread: c.Unread, Preview: clip(oneLine(c.Preview), 40)})
		}
		for _, c := range d.Chats.Other {
			out.Other = append(out.Other, prompts.SnapshotChat{Name: c.Name, Unread: c.Unread})
		}
	}
	return out
}

// snapshotCalendar is the rest of today's meetings in the connectors' live states, nil when none answered.
func snapshotCalendar(live []connector.SnapshotData, now time.Time) *prompts.SnapshotCalendar {
	var out *prompts.SnapshotCalendar
	for _, d := range live {
		if d.Meetings == nil {
			continue
		}
		if out == nil {
			out = &prompts.SnapshotCalendar{}
		}
		for _, m := range d.Meetings {
			if m.End.After(now) {
				out.Events = append(out.Events, prompts.SnapshotEvent{Start: m.Start.In(now.Location()), End: m.End.In(now.Location()),
					Title: clip(oneLine(m.Title), 40)})
			}
		}
	}
	if out != nil {
		sort.SliceStable(out.Events, func(i, j int) bool { return out.Events[i].Start.Before(out.Events[j].Start) })
	}
	return out
}

// snapshotSchedule is the schedule as the snapshot says it: the follow-ups to come, the daily tasks with how their
// last run went, and the scheduled runs that failed or were skipped since yesterday 18:00 (#4124: the "Morning briefing" tells them
// without calling list_followups). Bounded: the next eight follow-ups, eight daily tasks and the six newest misses, with the totals. The words
// for a status are the template's (prompts/assistant/snapshot.md).
func (s *Service) snapshotSchedule(ctx context.Context, loc *time.Location, now time.Time) (follow []prompts.SnapshotFollowup,
	followTotal int, routines []prompts.SnapshotRoutine, routinesTotal int, misses []prompts.SnapshotMiss, missed int) {
	sc, err := s.lockedSchedule(ctx)
	if err != nil {
		return nil, 0, nil, 0, nil, 0
	}
	defer sc.mu.Unlock()
	for _, t := range sc.tasks {
		if !t.Enabled {
			continue
		}
		if t.calendar() {
			routinesTotal++
			if len(routines) < 8 {
				r := prompts.SnapshotRoutine{Label: clip(t.label(), 30), Rule: ruleText(t, loc)}
				if !t.LastRun.IsZero() {
					r.LastAt, r.LastStatus = t.LastRun.In(loc), t.LastStatus
					if t.LastStatus == runFailed || t.LastStatus == runSkipped {
						r.LastWhy = clip(oneLine(t.LastError), 60)
					}
				}
				routines = append(routines, r)
			}
			continue
		}
		f := prompts.SnapshotFollowup{At: t.At.In(loc), Label: clip(oneLine(t.label()), 30)}
		if t.Repeat == repeatInterval {
			f.Every = everyText(t.EveryMinutes)
		}
		follow = append(follow, f)
	}
	n := now.In(loc)
	since := time.Date(n.Year(), n.Month(), n.Day()-1, 18, 0, 0, 0, loc) // as the "Today" card's "overnight" (today.go)
	// Runs are kept in the order they ended, At is when they started: a long run recorded late sits after a later
	// one, so the whole history (at most maxRuns) is looked through, not cut at the first run before since.
	var all []prompts.SnapshotMiss
	for _, r := range sc.missedRuns(since) {
		all = append(all, prompts.SnapshotMiss{At: r.At.In(loc), Label: clip(r.Title, 30), Why: clip(oneLine(r.Error), 60),
			Skipped: r.Status == runSkipped})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) }) // newest first
	missed = len(all)
	if len(all) > 6 {
		all = all[:6]
	}
	sort.SliceStable(follow, func(i, j int) bool { return follow[i].At.Before(follow[j].At) })
	followTotal = len(follow)
	if len(follow) > 8 {
		follow = follow[:8]
	}
	return follow, followTotal, routines, routinesTotal, all, missed
}

// snapshotPlans are the plan cards in the main conversation still waiting for the user's "Start" (#4453), newest first:
// at most five (total: all of them), of the last seven days. A plan has no todo until it starts, so nothing else in
// the snapshot shows it, and the "Morning briefing" could not bring it up.
func (s *Service) snapshotPlans(ctx context.Context, loc *time.Location, now time.Time) ([]prompts.SnapshotPlan, int) {
	main := s.settings(ctx).Main
	if main == "" {
		return nil, 0
	}
	items, plans, err := s.feed(ctx, main, now.Add(-7*24*time.Hour))
	if err != nil {
		return nil, 0
	}
	var out []prompts.SnapshotPlan
	seen := map[string]bool{}
	for i := len(items) - 1; i >= 0; i-- { // newest first
		it := items[i]
		p := plans[it.Ref]
		if it.Kind != feedPlan || seen[it.Ref] || p == nil || p.State != planOpen {
			continue
		}
		seen[it.Ref] = true
		sp := prompts.SnapshotPlan{At: p.CreatedAt.In(loc), Tasks: len(p.Tasks)}
		for _, t := range p.Tasks {
			if len(sp.Titles) == 3 {
				break
			}
			sp.Titles = append(sp.Titles, clip(oneLine(t.Title), 30))
		}
		out = append(out, sp)
	}
	total := len(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out, total
}

// ultracodeState preserves unknown separately from a switch explicitly read back as off.
func ultracodeState(v *bool) string {
	if v == nil {
		return ""
	}
	if *v {
		return "on"
	}
	return "off"
}
