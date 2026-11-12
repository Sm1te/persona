package assistant

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// ---- the snapshot's parts the lists show ----

// fleetTakeSnapshot keeps what the session lists are decorated with from the snapshot the assistant just made and tells
// the hub the lists of the machines whose sessions look different now; the assistant's own questions and cards go on
// the phone as interactions (askOnPhone).
func (s *Service) fleetTakeSnapshot(snap Snapshot) {
	if !s.fleetOn() {
		return
	}
	r := &s.fleet.rep
	r.mu.Lock()
	oldSessions, oldWork, oldOn, oldLinks, oldQuiet, oldMarks := r.sessions, r.work, r.workOn, r.links, r.quiet, r.marks
	views := make(map[string]SessionView, len(snap.Sessions))
	for _, v := range snap.Sessions {
		views[v.SessionID] = v
	}
	r.sessions, r.got = views, true
	if w := snap.Work; w != nil {
		r.work = make(map[[2]string]proto.WorkState, len(w.Sessions))
		for _, sw := range w.Sessions {
			r.work[[2]string{sw.MachineID, sw.SessionID}] = sw.Work
		}
		r.workOn = w.On
	}
	if t := snap.Todos; t != nil {
		r.links = map[[2]string]TodoLink{}
		for _, l := range t.Links {
			r.links[[2]string{l.MachineID, l.SessionID}] = l
		}
		r.quiet = map[[2]string]bool{}
		for _, q := range t.Quiet {
			r.quiet[[2]string{q.MachineID, q.SessionID}] = true
		}
	}
	if c := snap.Channels; c != nil {
		r.marks = make(map[[2]string][]string, len(c.Marks))
		for _, m := range c.Marks {
			r.marks[[2]string{m.MachineID, m.SessionID}] = m.Channels
		}
		r.turns = make(map[[2]string]time.Time, len(c.Turns))
		for _, t := range c.Turns {
			r.turns[[2]string{t.MachineID, t.SessionID}] = t.Answered
		}
	}
	if snap.Tasks != nil {
		r.tasks = snap.Tasks
	}
	if snap.Profile != nil {
		r.profile = snap.Profile
	}
	if in := snap.Inbox; in != nil {
		r.inbox = map[string]bool{}
		for _, a := range in.Agents {
			r.inbox[a.SessionID] = true
		}
	}
	changed := changedMachines(oldSessions, r.sessions)
	add := func(mid string) {
		if !slices.Contains(changed, mid) {
			changed = append(changed, mid)
		}
	}
	if oldOn != r.workOn {
		for _, m := range s.fleetMachines() {
			add(m.ID)
		}
	}
	for k, w := range r.work {
		if o, ok := oldWork[k]; !ok || !sameJSON(o, w) {
			add(k[0])
		}
	}
	for k := range oldWork {
		if _, ok := r.work[k]; !ok {
			add(k[0])
		}
	}
	for k, l := range r.links {
		if o, ok := oldLinks[k]; !ok || o != l {
			add(k[0])
		}
	}
	for k := range oldLinks {
		if _, ok := r.links[k]; !ok {
			add(k[0])
		}
	}
	for k := range r.quiet {
		if !oldQuiet[k] {
			add(k[0])
		}
	}
	for k := range oldQuiet {
		if !r.quiet[k] {
			add(k[0])
		}
	}
	for k, v := range r.marks {
		if !slices.Equal(oldMarks[k], v) {
			add(k[0])
		}
	}
	for k := range oldMarks {
		if _, ok := r.marks[k]; !ok {
			add(k[0])
		}
	}
	r.mu.Unlock()
	s.askOnPhone(snap.Interactions)
	for _, mid := range changed {
		s.sessionsChanged(mid)
	}
	go s.deliverAnswers() // answers its Action could not take yet
}

// changedMachines are the machines of the conversations whose views differ between two snapshots.
func changedMachines(old, now map[string]SessionView) []string {
	var out []string
	add := func(mid string) {
		if !slices.Contains(out, mid) {
			out = append(out, mid)
		}
	}
	for id, v := range now {
		o, ok := old[id]
		if !ok || o.Title != v.Title || o.Archived != v.Archived || o.Origin != v.Origin || o.OriginRef != v.OriginRef || !slices.Equal(o.Queued, v.Queued) || o.MachineID != v.MachineID {
			add(v.MachineID)
			if ok {
				add(o.MachineID)
			}
		}
	}
	for id, o := range old {
		if _, ok := now[id]; !ok {
			add(o.MachineID)
		}
	}
	return out
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// quietSession: mid/sid works (or worked) for a quiet task: the main conversation hears about it, nothing else does.
func (s *Service) quietSession(mid, sid string) bool {
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.quiet[[2]string{mid, sid}]
}

// withTask names, on an interaction sent to clients, the task its agent works for (the watch and the cards show it),
// and the session its agent is folded under, whose chat shows the card (fleet_fold.go).
func (s *Service) withTask(ix proto.Interaction) proto.Interaction {
	r := &s.fleet.rep
	r.mu.RLock()
	l, ok := r.links[[2]string{ix.MachineID, ix.SessionID}]
	r.mu.RUnlock()
	if ok && l.Task {
		ix.TaskTitle = l.Title
	}
	ix.FoldedUnder = s.foldedUnder(ix.MachineID, ix.SessionID)
	return ix
}

// isInboxAgent: the conversation is an inbox agent's (no "finished" push for it: the draft in the inbox is the result).
func (s *Service) isInboxAgent(sessionID string) bool {
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.inbox[sessionID]
}

// channelAnsweredPush: the conversation's turn is (or just was) answered in a channel, so the phone's "finished" push
// leaves it alone. "Just": within the finished push's delay, which is when it looks — not much longer, or the push of
// the next turn, asked in the app, would be dropped too.
func (s *Service) channelAnsweredPush(session string) bool {
	window := 30*time.Second + s.notifyTimings().doneDelay()
	r := &s.fleet.rep
	r.mu.RLock()
	defer r.mu.RUnlock()
	for k, at := range r.turns {
		if k[1] == session && (at.IsZero() || time.Since(at) < window) {
			return true
		}
	}
	return false
}
