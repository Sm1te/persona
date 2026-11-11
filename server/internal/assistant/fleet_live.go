package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// The "Fleet" Live Activity's content (docs/apple/live-activity.md): one activity per phone, on the lock screen and in the
// Dynamic Island, summing up every agent that needs you, is working, or finished unseen. The manager works it out
// (FleetState) from the session lists and the pending interactions, with the island's numbers (FleetStats), and
// tells the hub when it changes (fleet.live); the hub pushes it to each device's activity token at the pace Apple
// allows and gives each device its own part (its island's sides, its lock screen details, its persona).

type (
	FleetState    = assistantwire.FleetState
	FleetStats    = assistantwire.FleetStats
	FleetLoad     = assistantwire.FleetLoad
	FleetItem     = assistantwire.FleetItem
	FleetProgress = assistantwire.FleetProgress
)

const fleetMaxItems = 3

// fleetDetailMax is how long an item's line gets, in runes, before it is clipped (with an ellipsis after);
// fleetTitleMax and fleetPlaceMax its title and place, as the lock screen has room for them.
const (
	fleetDetailMax = 60
	fleetTitleMax  = 24
	fleetPlaceMax  = 32
)

// liveState is what was told the hub last.
type liveState struct {
	mu     sync.Mutex
	sent   string
	signal chan struct{}
}

func (l *liveState) ch() chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.signal == nil {
		l.signal = make(chan struct{}, 1)
	}
	return l.signal
}

// liveChanged: what the fleet is made of may have changed; force: tell the hub even when it did not (it may have
// restarted).
func (s *Service) liveChanged(force bool) {
	if !s.fleetOn() {
		return
	}
	if force {
		s.fleet.live.mu.Lock()
		s.fleet.live.sent = ""
		s.fleet.live.mu.Unlock()
	}
	select {
	case s.fleet.live.ch() <- struct{}{}:
	default:
	}
}

// liveLoop tells the hub the fleet when it changed, at most every 100 ms, and looks at the numbers every 15 s; the
// devices get Persona Link's summary of the same agents.
func (s *Service) liveLoop(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	sig := s.fleet.live.ch()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			time.Sleep(100 * time.Millisecond)
		case <-tick.C:
		}
		l := s.lang(context.Background())
		agents, ixs, told := s.fleetInputs()
		s.tellLive(buildFleetIn(l, agents, ixs))
		s.devs.SetAttention(linkAttention(l, agents, ixs, told)) // sent on only when it changed
	}
}

func (s *Service) tellLive(st FleetState) {
	msg := assistantwire.FleetLive{State: st, Stats: s.fleetStats()}
	b, _ := json.Marshal(msg)
	l := &s.fleet.live
	l.mu.Lock()
	same := l.sent == string(b)
	l.mu.Unlock()
	if same {
		return
	}
	if s.hub.Tell(assistantwire.EventFleetLive, json.RawMessage(b)) == nil {
		l.mu.Lock()
		l.sent = string(b)
		l.mu.Unlock()
	}
}

// fleetSnapshot computes the fleet from every machine's sessions and the pending interactions.
func (s *Service) fleetSnapshot() FleetState {
	agents, ixs, _ := s.fleetInputs()
	return buildFleetIn(s.lang(context.Background()), agents, ixs)
}

// fleetInputs is every machine's agents as the fleet sees them, the pending interactions (oldest first), and which of
// those are the user's (pendingCards). Each agent points into its machine's decorated list, a copy of its own that
// nothing else holds and the fleet only reads: liveLoop builds the fleet up to ten times a second, and a copy of every
// session per agent (1.5 KB each) was most of what that allocated.
func (s *Service) fleetInputs() ([]fleetInput, []proto.Interaction, map[string]bool) {
	machines := s.fleetMachines()
	lists := make([][]proto.Session, len(machines))
	n := 0
	for i, m := range machines {
		lists[i] = s.decorated(m.ID)
		n += len(lists[i])
	}
	agents := make([]fleetInput, 0, n)
	turns := s.turnsBook()
	for i, m := range machines {
		list := lists[i]
		for j := range list {
			sess := &list[j]
			if sess.Quiet { // a quiet task's agent: the main conversation tells about it (#1731)
				continue
			}
			agents = append(agents, fleetInput{
				// Its own space, not muted's lookup of the node's row: that scanned the machine's list once per session.
				Machine: m.Name, Session: sess, Muted: s.mutedAs(m.ID, sess.SessionID, sess.SpaceName),
				TurnStart: turns.startOf(m.ID + "/" + sess.SessionID),
			})
		}
	}
	ixs, told := s.pendingCards()
	return agents, ixs, told
}

// round2 keeps two significant digits: 12345 → 12000, 987 → 990. The island shows "12k", so finer changes would
// only spend the push budget.
func round2(n int64) int64 {
	if n < 100 {
		return n
	}
	p := int64(1)
	for n/p >= 100 {
		p *= 10
	}
	return (n + p/2) / p * p
}

// fleetStats turns the usage summary into the island's numbers.
func (s *Service) fleetStats() FleetStats {
	if s.devs == nil {
		return FleetStats{}
	}
	u := s.devs.Usage()
	st := FleetStats{TPM: round2(u.TPM), Tokens: round2(u.Tokens), Requests: int(round2(int64(u.Requests)))}
	for _, m := range u.Machines {
		st.Loads = append(st.Loads, FleetLoad{ID: m.ID, Name: s.machineName(m.ID), Percent: int(math.Round(m.Load*20)) * 5})
	}
	return st
}

// fleetInput is one agent as the fleet sees it. Session is read, never written: it points into a list the fleet's
// caller owns.
type fleetInput struct {
	Machine   string // display name
	Session   *proto.Session
	Muted     bool
	TurnStart time.Time // zero: unknown
}

// buildFleet sums agents up the way the Agents list does (docs/apple/ios.md): needs you first (oldest request first), then
// working (longest first), then finished unseen. Muted agents, idle / offline ones, and /loop agents waiting for
// their next tick are left out; the assistant only counts when it asks something.
func buildFleet(agents []fleetInput, ixs []proto.Interaction) FleetState {
	return buildFleetIn(i18n.Default, agents, ixs)
}

// needer is an agent that needs the user, and the card that stands for it: its oldest (nil: it waits in a terminal
// dialog without one). card points into the cards needsYou was given.
type needer struct {
	agent fleetInput
	card  *proto.Interaction
}

// agentKey is an agent's machine and session, what its cards name it by.
type agentKey = [2]string

func keyOf(sess *proto.Session) agentKey { return agentKey{sess.MachineID, sess.SessionID} }

// needsYou is the agents that need the user, one each, as the Agents list has them (#3571): those with a card by their
// oldest card that stands (ixs come oldest first), then those in a terminal dialog without one, in agents' order.
// counts says which agents count at all, stands which cards may stand for one; the assistant's own dialog never
// counts. The Live Activity and Persona Link's summary (fleet_attention.go) differ in those, not in this.
func needsYou(agents []fleetInput, ixs []proto.Interaction, counts func(fleetInput) bool, stands func(proto.Interaction) bool) []needer {
	var out []needer
	var asked map[agentKey]bool
	if len(ixs) > 0 {
		byKey := make(map[agentKey]int, len(agents)) // the agent's place in agents: the last one listed under its key
		for i, a := range agents {
			if counts(a) {
				byKey[keyOf(a.Session)] = i
			}
		}
		asked = map[agentKey]bool{}
		for i := range ixs {
			ix := &ixs[i]
			key := agentKey{ix.MachineID, ix.SessionID}
			if a, ok := byKey[key]; ok && !asked[key] && stands(*ix) {
				asked[key] = true
				out = append(out, needer{agent: agents[a], card: ix})
			}
		}
	}
	for _, a := range agents {
		if counts(a) && a.Session.State == proto.StateNeedsYou && !a.Session.Assistant && !asked[keyOf(a.Session)] {
			out = append(out, needer{agent: a})
		}
	}
	return out
}

// liveCounts: the agent is on the Live Activity at all (muted agents and subtasks are not: Codex sub-agents, and a
// runtime's session another one started, which its parent's row stands for until it needs the user).
func liveCounts(a fleetInput) bool { return !a.Session.IsSubtask() && !a.Muted }

// cardStands: the card may stand for its agent (on the Live Activity and in Persona Link's summary). An elicitation
// does not: the Fleet is shared with legacy clients, whose terminal fallback stays (the agent shows as in a dialog).
func cardStands(ix proto.Interaction) bool { return ix.Kind != proto.IxElicitation }

// shownAgent is a working or finished agent the Live Activity may list, and the time it is ordered by.
type shownAgent struct {
	agent *fleetInput
	since float64
}

// buildFleetIn is buildFleet with the texts for people in l (the user's language). Every agent is counted, but only
// the items shown (fleetMaxItems) are written out: their titles, places and lines are most of what a fleet costs.
func buildFleetIn(l i18n.Lang, agents []fleetInput, ixs []proto.Interaction) FleetState {
	var st FleetState
	needs := needsYou(agents, ixs, liveCounts, cardStands)
	asked := make(map[agentKey]bool, len(needs))
	for _, n := range needs {
		asked[keyOf(n.agent.Session)] = true
	}
	var working, unread []shownAgent
	for i := range agents {
		a := &agents[i]
		sess := a.Session
		if !liveCounts(*a) || len(asked) > 0 && asked[keyOf(sess)] {
			continue
		}
		switch sess.State {
		case proto.StateWorking:
			if sess.Assistant {
				st.Assistant = true
				continue
			}
			working = append(working, shownAgent{a, unixSeconds(workingSince(a))})
		case proto.StateUnread:
			if sess.Assistant || sess.Loop != nil {
				continue
			}
			unread = append(unread, shownAgent{a, unixSeconds(sess.UpdatedAt)})
		}
	}
	sort.SliceStable(working, func(i, j int) bool { return working[i].since < working[j].since })
	sort.SliceStable(unread, func(i, j int) bool { return unread[i].since > unread[j].since })
	st.NeedsYou, st.Working, st.Unread = len(needs), len(working), len(unread)
	switch {
	case st.NeedsYou > 0:
		st.Phase = "needsYou"
	case st.Working > 0:
		st.Phase = "working"
	case st.Unread > 0:
		st.Phase = "done"
	}
	if len(working) > 0 {
		st.WorkingSince = working[0].since
	}
	st.Items = make([]FleetItem, 0, min(fleetMaxItems, len(needs)+len(working)+len(unread)))
	for _, n := range needs[:min(len(needs), fleetMaxItems)] {
		st.Items = append(st.Items, needItem(l, n))
	}
	for _, w := range working[:min(len(working), fleetMaxItems-len(st.Items))] {
		st.Items = append(st.Items, workingItem(l, w))
	}
	for _, u := range unread[:min(len(unread), fleetMaxItems-len(st.Items))] {
		sess := u.agent.Session
		st.Items = append(st.Items, FleetItem{ID: sess.MachineID + "/" + sess.SessionID, Title: agentTitle(sess, fleetTitleMax),
			Place: agentPlace(u.agent.Machine, sess, fleetPlaceMax), Kind: "unread",
			Detail: clip(oneLine(firstLine(sess.LastText)), fleetDetailMax), Since: u.since})
	}
	return st
}

// workingSince is when a working agent started what it is doing: its turn, or its oldest running workflow while it
// works in the background; its last update when neither is known.
func workingSince(a *fleetInput) time.Time {
	sess := a.Session
	since := a.TurnStart
	if sess.BackgroundWorking {
		var started time.Time
		for _, w := range sess.Workflows {
			if w.Running && !w.StartedAt.IsZero() && (started.IsZero() || w.StartedAt.Before(started)) {
				started = w.StartedAt
			}
		}
		if !started.IsZero() {
			since = started
		}
	}
	if since.IsZero() {
		since = sess.UpdatedAt
	}
	return since
}

// needItem is the item of an agent that needs the user, by the card that stands for it.
func needItem(l i18n.Lang, n needer) FleetItem {
	sess := n.agent.Session
	item := FleetItem{ID: sess.MachineID + "/" + sess.SessionID, Title: agentTitle(sess, fleetTitleMax),
		Place: agentPlace(n.agent.Machine, sess, fleetPlaceMax), Kind: proto.IxScreen, Detail: i18n.T(l, "hub-liveactivity.screen")}
	if ix := n.card; ix != nil {
		item.Kind, item.Detail = ix.Kind, clip(oneLine(interactionDetail(l, *ix)), fleetDetailMax)
		if ix.Private { // a Just me turn's card: the Live Activity shows it without its text
			item.Detail = i18n.T(l, "push.private_card")
		}
		item.Interaction, item.Since = ix.ID, unixSeconds(ix.CreatedAt)
		if len(ix.Fields) > 0 || ix.ToolName == proto.SecretHandoffTool {
			// What it asks for (a password) is filled in in the app, or (a hand-off) approved on the card only
			// (hub/secret_handoff.go): no Allow / Deny on the activity.
			item.Interaction = ""
		}
	}
	return item
}

// workingItem is the item of a working agent.
func workingItem(l i18n.Lang, w shownAgent) FleetItem {
	sess := w.agent.Session
	it := FleetItem{ID: sess.MachineID + "/" + sess.SessionID, Title: agentTitle(sess, fleetTitleMax),
		Place: agentPlace(w.agent.Machine, sess, fleetPlaceMax), Kind: "working", Since: w.since}
	if sess.BackgroundWorking {
		it.Background, it.Detail = true, backgroundDetail(l, sess)
		it.Progress = backgroundProgress(sess)
	}
	return it
}

// agentTitle is the agent's title in one line of at most n runes: its project's folder when it has none.
func agentTitle(sess *proto.Session, n int) string {
	t := transcript.Title(sess.Title)
	if t == "" || t == "Claude Code" {
		t = path.Base(sess.Cwd)
	}
	return clip(oneLine(t), n)
}

// agentPlace is "machine · space › tab", the subtitle pushes use too, in at most n runes.
func agentPlace(machine string, sess *proto.Session, n int) string {
	place := machine
	if sess.SpaceName != "" {
		place += " · " + sess.SpaceName
		if sess.SlotName != "" && sess.SlotName != fmt.Sprint(sess.SlotIndex) {
			place += " › " + sess.SlotName
		}
	}
	return clip(oneLine(place), n)
}

func interactionDetail(l i18n.Lang, ix proto.Interaction) string {
	switch ix.Kind {
	case proto.IxPermission:
		if ix.Summary != "" {
			return ix.Summary
		}
		return ixToolName(ix)
	case proto.IxPlan:
		return planTitle(ix.Plan)
	case proto.IxQuestion:
		if len(ix.Questions) > 0 {
			return ix.Questions[0].Question
		}
		return ix.Summary
	}
	return i18n.T(l, "hub-liveactivity.screen")
}

func unixSeconds(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return float64(t.Unix())
}

// shownWorkflow is the running workflow a background-working agent's item names: the first one (Workflows are newest
// first), as the web's workflow line picks it.
func shownWorkflow(sess *proto.Session) (proto.Workflow, bool) {
	for _, w := range sess.Workflows {
		if w.Running {
			return w, true
		}
	}
	return proto.Workflow{}, false
}

// backgroundDetail is what a background-working agent runs, in a line: its running workflow ("parity-audit · Verify ·
// 169/185"), else how many background tasks. A long name or phase is what gets clipped, never the counts at the end:
// they are what the bar shows (backgroundProgress), so the line changes whenever the bar does and the bar costs no
// push of its own (#3437).
func backgroundDetail(l i18n.Lang, sess *proto.Session) string {
	if w, ok := shownWorkflow(sess); ok {
		parts := []string{cmp.Or(w.Name, w.Summary, "workflow")}
		if w.Phase != "" {
			parts = append(parts, w.Phase)
		}
		counts := fmt.Sprintf(" · %d/%d", w.AgentsDone, w.AgentsStarted)
		return clip(strings.Join(parts, " · "), max(fleetDetailMax-utf8.RuneCountInString(counts), 0)) + counts
	}
	n := len(sess.Background)
	for _, a := range sess.Subagents {
		if a.Running {
			n++
		}
	}
	return clip(i18n.T(l, "hub-liveactivity.background", i18n.Args{"count": n}), fleetDetailMax)
}

// backgroundProgress is the bar under a background-working agent's item (#3437): the counts its detail prints (which
// clipping never cuts), of the same workflow, so the bar and the line beside it agree and the bar never changes the
// content by itself (the push budget, docs/live-activity.md). Nil without a running workflow, or before it started an
// agent.
func backgroundProgress(sess *proto.Session) *FleetProgress {
	w, ok := shownWorkflow(sess)
	if !ok || w.AgentsStarted <= 0 {
		return nil
	}
	return &FleetProgress{Done: min(max(w.AgentsDone, 0), w.AgentsStarted), Started: w.AgentsStarted}
}
