package assistant

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// Where each agent stands in its work as the session lists show it (docs/assistant/agent-work.md "Model"): what the Agents
// business reported for its latest turn while it is still stopped on it, what it shows while it works or asks.

// decorateWork puts on sess (decorated but for its work) where it stands in its work, and the state that follows.
// asking is the machine's pending interactions by session (an agent's own before the manager's card).
func (s *Service) decorateWork(mid string, sess *proto.Session, asking map[string]proto.Interaction) {
	r := &s.fleet.rep
	r.mu.RLock()
	on := r.workOn
	reported, ok := r.work[[2]string{mid, sess.SessionID}]
	r.mu.RUnlock()
	if !on && !(ok && transientPending(reported) && sess.Status != proto.StatusWorking && sameTurn(*sess, reported.TurnAt)) {
		return // the triage off: only a "Continue" on its way after a transient error is told (transient_retry, #3208)
	}
	var e *proto.WorkState
	if ok {
		e = &reported
	}
	sess.Work = workOf(s.lang(context.Background()), *sess, e, asking)
	sess.State = workState(sess.State, sess.Work)
}

// workOf is sess's work state for the lists: reported is what the Agents business reported for its latest turn (nil:
// nothing), asking the pending interactions of its machine, by session.
func workOf(l i18n.Lang, sess proto.Session, reported *proto.WorkState, asking map[string]proto.Interaction) *proto.WorkState {
	if sess.Assistant || sess.Status == proto.StatusOffline {
		return nil
	}
	if sess.Status == proto.StatusWorking {
		return &proto.WorkState{Phase: proto.WorkWorking}
	}
	ix, asks := asking[sess.SessionID]
	if asks && ix.ToolName != assistantwire.WorkCardTool { // the agent asks itself: that is what it waits for
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: interactionLine(l, ix)}
	}
	if sess.Status == proto.StatusBlocked && !asks {
		return &proto.WorkState{Phase: proto.WorkBlocked, On: proto.WaitUser, Line: i18n.T(l, "hub-agents_link.screen")}
	}
	if reported == nil || !workShown(reported) || !sameTurn(sess, reported.TurnAt) {
		return nil
	}
	w := *reported
	if w.InteractionID != "" && (!asks || ix.ID != w.InteractionID) {
		w.InteractionID = "" // answered, withdrawn, or not shown yet
	}
	if w.WakeAt != nil && time.Since(*w.WakeAt) > wakeGrace {
		w.WakeAt = nil // long overdue: nobody is waking it, it is no wait that ends by itself
	}
	if w.ResetsAt != nil && w.On == proto.WaitExternal && w.WakeAt == nil && !sess.TurnErrorResumes && w.Wait == "" {
		// A usage-limit wait nothing ends (no continue set: auto_continue off, or dropped; a CLI that does not go on by
		// itself): it names what it waits for, so the lists keep it for the user, not as a wait that ends by itself.
		w.Wait = w.Line
	}
	if sess.Status == proto.StatusInterrupted && selfWakes(&w) {
		// Its process is gone (a node restart is behind it, node/interrupted.go): a wait only that process ends (its
		// background tasks, a /loop, its CLI going on after a limit) never ends, and the task would read "waiting"
		// forever instead of stopped (#5124).
		return nil
	}
	return &w
}

// selfWakes: w is a wait the agent ends by itself, with nothing the manager sends: managerWaits without a wake.
func selfWakes(w *proto.WorkState) bool {
	return managerWaits(w) && w.WakeAt == nil
}

// transientPending: w is a transient model error whose "Continue" the manager sends by itself (wake_at).
func transientPending(w proto.WorkState) bool {
	return w.Phase == proto.WorkBlocked && w.On == proto.WaitTransient && w.WakeAt != nil
}

// wakeGrace is how late a reported wake may be and still count: the Agents business sends what is due within its
// tick, and tries again a minute later when the machine was away.
var wakeGrace = 5 * time.Minute

// workState is the unified state once the work state is known: a turn that is done, or blocked on something the
// manager waits out for it, no longer counts as "Unread" (docs/assistant/agent-work.md "Model"). A wait nobody will end (the wakes
// off, or dropped because the user went near it) stays "Unread".
func workState(state string, w *proto.WorkState) string {
	if state != proto.StateUnread || w == nil {
		return state
	}
	if w.Phase == proto.WorkDone || managerWaits(w) {
		return proto.StateIdle
	}
	return state
}

// managerWaits: w is a wait that ends without the user — the manager wakes it (wake_at), or it wakes itself (a
// background task, a /loop: on external with nothing named to wait for).
func managerWaits(w *proto.WorkState) bool {
	if w.Phase != proto.WorkBlocked || w.On == proto.WaitUser {
		return false
	}
	return w.WakeAt != nil || w.On == proto.WaitExternal && w.Wait == "" && w.Agent == nil
}

// interactionLineMost is how many runes of a pending interaction's work line are kept (the ellipsis after them).
const interactionLineMost = 60

// interactionLine is what a pending interaction waits for, in a line, in l.
func interactionLine(l i18n.Lang, ix proto.Interaction) string {
	switch ix.Kind {
	case proto.IxQuestion:
		if len(ix.Questions) > 1 { // the count after the first question is kept whole; the question is what gets cut
			more := func(q string) string {
				return i18n.T(l, "hub-agents_link.more_questions", i18n.Args{"question": q, "count": len(ix.Questions) - 1})
			}
			q := oneLine(ix.Questions[0].Question)
			if q == "" {
				q = i18n.T(l, "hub-agents_link.question")
			}
			return more(clip(q, max(interactionLineMost-utf8.RuneCountInString(more("")), 1)))
		}
		if len(ix.Questions) > 0 {
			return clip(oneLine(ix.Questions[0].Question), interactionLineMost)
		}
		return i18n.T(l, "hub-agents_link.question")
	case proto.IxPlan:
		if p := planTitle(ix.Plan); p != "" { // which plan: two agents' plans read apart
			return clip(oneLine(i18n.T(l, "hub-agents_link.plan_for", i18n.Args{"plan": p})), interactionLineMost)
		}
		return i18n.T(l, "hub-agents_link.plan")
	case proto.IxPermission:
		if ix.Summary != "" {
			return clip(oneLine(i18n.T(l, "hub-agents_link.permission_for", i18n.Args{"summary": ix.Summary})), interactionLineMost)
		}
		return i18n.T(l, "hub-agents_link.permission")
	case proto.IxElicitation:
		return i18n.T(l, "hub-agents_link.elicitation")
	}
	return i18n.T(l, "hub-agents_link.screen")
}

// decorateAwaits says whether sess (decorated, its work on it) stopped on a question to the user nobody answered yet
// (Session.AwaitsReply, docs/protocol/agent-states.md "Awaiting your reply", #5096), and puts on it what it asks when the node could not
// say (an older node, a host whose words come another way: no AsksRead): the list's last words, when they are the whole
// reply. A node that read the reply and found no question (or found it answered) is taken at its word. asks reads them
// (fleet_asks.go: again only when they changed since the machine's last list).
func decorateAwaits(sess *proto.Session, asks askReader) {
	if !sess.AsksRead && sess.Asks == "" && sess.LastText != "" && !strings.HasSuffix(sess.LastText, "…") {
		sess.Asks = asks.of(sess.SessionID, sess.LastText)
	}
	sess.AwaitsReply = awaitsReply(*sess)
}

// awaitsReply: sess is stopped (Unread or Seen) on a question to the user — the triage says it waits on the user, or,
// when the triage has no word on the turn (off, not yet, unsure), its closing words ask — with nothing else to answer
// it: no card (that is Needs You), no model error (the triage or Retry handles it), no background work or /loop (it wakes
// itself). A turn the triage found done asks nothing: its"shall I also…"is an offer, not a wait; nor one whose card the
// user answered "Skip for now".
func awaitsReply(sess proto.Session) bool {
	if sess.Assistant || sess.State != proto.StateUnread && sess.State != proto.StateIdle || sess.PendingInteractions > 0 ||
		sess.TurnError != "" || sess.Loop != nil || backgroundRunning(sess) {
		return false
	}
	if w := sess.Work; w != nil {
		switch w.Phase {
		case proto.WorkDone:
			return false
		case proto.WorkBlocked:
			return w.On == proto.WaitUser && !w.Deferred // put off on its card: the user chose to leave it
		}
	}
	return sess.Asks != ""
}
