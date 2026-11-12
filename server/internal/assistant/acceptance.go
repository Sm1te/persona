package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// "Please verify" (#2768, docs/extensions/bots.md "Task progress and review requests"): once a task's work is deployed, the hub keeps an ask for the user to try it —
// three lines: what changed, where to look (a link straight to the page), how to check. With the bot on (and its task reports
// on) the ask goes out as a Feishu card with "Pass" on it; without, it is kept all the same (NoCard) and the briefing, the
// board and the web app show it. "Pass" (the button, a reply saying so, or the web Accept button) marks the task verified; any other reply to the card is written into the todo's
// decisions and sent to the agent that did the work. One still not accepted is asked again the next morning.

const acceptanceKey = "assistant.acceptance"

type acceptAsk struct {
	MessageID string   `json:"message_id"`         // the first card ("" while none went out, see NoCard)
	Messages  []string `json:"messages,omitempty"` // every card asking it (the first and the reminders)
	// Earlier are the cards of the task's rounds before this one (#4853): dead, so a press or a reply on one is answered
	// "already handled" and never accepts this round or reaches the chat.
	Earlier    []string  `json:"earlier,omitempty"`
	MachineID  string    `json:"machine_id"`
	SessionID  string    `json:"session_id"`
	Changed    string    `json:"changed"`
	Link       string    `json:"link,omitempty"`
	How        string    `json:"how"`
	AskedAt    time.Time `json:"asked_at"`
	RemindedAt time.Time `json:"reminded_at,omitzero"`
	AcceptedAt time.Time `json:"accepted_at,omitzero"`
	// BySupervisor: passed by the task's supervisor, whose task_review accepted it having checked the evidence, not by
	// the owner (supervisorVerified, #3240).
	BySupervisor bool `json:"by_supervisor,omitempty"`
	// NoCard: no card is due (the bot is off or not paired, or task reports are off, when the ask was made or when its
	// card was tried again), or its card could not be posted within acceptPostWindow, so MessageID stays empty for good;
	// the owner is asked by the briefing, the board and the web app instead. An empty MessageID without it (and with
	// AskedAt) is a card that did not go out yet: retryAcceptance posts it.
	NoCard bool `json:"no_card,omitempty"`
	// Conclusion: the ask is a supervised task's conclusion (#1226, conclusion.go), told once its review gives a result: its
	// card says the verdict, one sentence of why and one link instead of the three lines. Outcome is the verdict
	// (outcomeDone, outcomeCannot, outcomeDecide; "" reads as done: a deployed task's ask says that on top of its three
	// lines), Reason the sentence, Recommend the option a decision recommends, which "OK" takes.
	Conclusion bool   `json:"conclusion,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Recommend  string `json:"recommend,omitempty"`
	// Deadline: a decision's deadline (#3127, decision_deadline.go); zero: none. Past it unanswered, a Reversible one
	// takes Recommend by default (ByDefault), one that is not is reminded once and keeps waiting; DueAt is when the
	// deadline was handled either way.
	Deadline   time.Time `json:"deadline,omitzero"`
	Reversible bool      `json:"reversible,omitempty"`
	DueAt      time.Time `json:"due_at,omitzero"`
	ByDefault  bool      `json:"by_default,omitempty"`
	// ByAssistant: a deployed task the supervising assistant accepted itself, not a big feature (ownerVerifies, #3205).
	ByAssistant bool `json:"by_assistant,omitempty"`
	// A decision's own (#3804, decision.go). WhyOwner: why only the owner can decide; Options: what they choose from, the
	// recommended first, each with what it leads to; CostOfWaiting: what waiting costs. From and Stands are the hub's:
	// where the task came from and where it stood when the decision was asked. RecheckAt: when it went back to its
	// supervisor for having waited long; RaisedAt: when one never taken by default was last raised to the owner again;
	// Response: the fixed answer the owner gave instead of an option (ownerResponses).
	WhyOwner      string           `json:"why_owner,omitempty"`
	Options       []DecisionOption `json:"options,omitempty"`
	CostOfWaiting string           `json:"cost_of_waiting,omitempty"`
	From          *askFrom         `json:"from,omitempty"`
	Stands        string           `json:"stands,omitempty"`
	RecheckAt     time.Time        `json:"recheck_at,omitzero"`
	RaisedAt      time.Time        `json:"raised_at,omitzero"`
	Response      string           `json:"response,omitempty"`
	// Chosen: the option the owner chose when it was not the recommended one.
	Chosen string `json:"chosen,omitempty"`
	// ForBriefing: a decision that can wait (#4930, decision_wait.go), raised without a push and without a card, while
	// it waits for a briefing to bring it to the owner; cleared once one did, or once it was pushed after all, at
	// PushedAt: its card is due from then (retryAcceptance).
	ForBriefing bool      `json:"for_briefing,omitempty"`
	PushedAt    time.Time `json:"pushed_at,omitzero"`
	// RaisedBy: while a decision is held for a briefing, the conversation that raised it when the main conversation is
	// to hear of it once it is pushed after all: the task's leader conversation, or the background supervisor, whose
	// tasks' ledgers name the main conversation (heldEscalates, supervise_background.go).
	RaisedBy string `json:"raised_by,omitempty"`
}

// acceptPostWindow is how long after an ask its card that did not go out (the bot's status could not be read, the send
// failed or timed out) is tried again, once a tick of acceptLoop. Every try goes under the round's own key
// (acceptCardKey), and Feishu drops a repeated key only for about an hour: a try later than that could post a second
// card, so past the window the ask stays card-less (NoCard) instead. The window, not a count of tries, is what keeps
// that true when ticks come late (a restart in between); with ticks every 10 minutes it is at most six sends (the
// first and five retries).
const acceptPostWindow = 45 * time.Minute

// acceptCardKey is the key (feishu.post's uuid) the card of task id's round asked at goes out under: the same for every
// try of that round, so a try that did land though its answer was lost (a timeout) is not posted again by the next.
func acceptCardKey(id string, asked time.Time) string {
	sum := sha256.Sum256([]byte("accept\x00" + id + "\x00" + strconv.FormatInt(asked.UnixNano(), 10)))
	return "pv:" + hex.EncodeToString(sum[:16])
}

// acceptCardDue says a's card is still to be posted: asked (not a claim in flight), not passed, no card and none
// meant to be.
func acceptCardDue(a *acceptAsk) bool {
	return a != nil && !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() && a.MessageID == "" && !a.NoCard
}

type acceptBook struct {
	mu     sync.Mutex
	loaded bool
	asks   map[string]*acceptAsk // by todo id
}

// loadAcceptLocked reads the asks once and reports whether they are in memory; s.accept.mu is held. A failed read
// leaves them unloaded (the next look tries again): writing the book after it would put an empty or partial map over
// the stored cards, whose replies, reminders and briefing lines would then be lost.
func (s *Service) loadAcceptLocked(ctx context.Context) bool {
	b := &s.accept
	if b.loaded {
		return true
	}
	asks := map[string]*acceptAsk{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, acceptanceKey, &asks); err != nil {
			s.logf("acceptance: load: %v", err)
			return false
		}
		if asks == nil {
			asks = map[string]*acceptAsk{}
		}
	}
	// What is read was written before this process asked anything. An entry with no time is a claim whose card was
	// going out when another save wrote the book: nothing will finish it, and left in place it would keep its task from
	// ever being asked again (a claim is never stale). A null entry is nothing.
	for id, a := range asks {
		if a == nil || a.AskedAt.IsZero() {
			s.logf("acceptance: load: task %s: dropped a claim left in flight", id)
			delete(asks, id)
		}
	}
	b.asks, b.loaded = asks, true
	return true
}

// errAcceptStore: the asks could not be read, or an acceptance could not be saved. Nothing was accepted and the ask still
// waits; the owner is told the service is unavailable (try again), never "already handled" or "verified".
var errAcceptStore = errors.New("acceptance: the asks could not be read or saved")

// saveAcceptLocked writes the asks, once they were read (see loadAcceptLocked); a failed write is logged.
func (s *Service) saveAcceptLocked(ctx context.Context) {
	if err := s.saveAcceptErrLocked(ctx); err != nil {
		s.logf("acceptance: save: %v", err)
	}
}

// saveAcceptErrLocked is saveAcceptLocked returning the failed write instead of logging it.
func (s *Service) saveAcceptErrLocked(ctx context.Context) error {
	if !s.accept.loaded {
		return nil
	}
	if st := s.store(); st != nil {
		return st.PutSetting(ctx, acceptanceKey, s.accept.asks)
	}
	return nil
}

var urlRe = lazyre.New(`https?://[^\s<>()\[\]"'，。；、）]+`)

// acceptEarlierMost is how many cards of earlier rounds a task's ask remembers.
const acceptEarlierMost = 20

// lastOf is list's last n.
func lastOf(list []string, n int) []string {
	if len(list) > n {
		return list[len(list)-n:]
	}
	return list
}

// acceptFor is the todo whose "Please verify" card message id is (or the thread it is in), "" when none; earlier when the card
// is one of an earlier round of that task (Earlier).
func (s *Service) acceptFor(ids ...string) (todo string, earlier bool) {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(context.Background()) {
		return "", false
	}
	for todo, a := range b.asks {
		for _, id := range ids {
			if id != "" && slices.Contains(a.Messages, id) {
				return todo, false
			}
		}
	}
	for todo, a := range b.asks {
		for _, id := range ids {
			if id != "" && slices.Contains(a.Earlier, id) {
				return todo, true
			}
		}
	}
	return "", false
}

// acceptAskLocked returns todo id's stored ask (nil: none), or errAcceptStore when the asks cannot be read; callers only
// read its immutable AskedAt.
func (s *Service) acceptAskLocked(id string) (*acceptAsk, error) {
	b := &s.accept
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.loadAcceptLocked(context.Background()) { // after a restart nothing may have read them yet (a button of an old briefing)
		return nil, errAcceptStore
	}
	return b.asks[id], nil
}

// acceptAskEnded says the todo a "Please verify" card waits on has ended without the hub's auto-judge: the owner closed it in
// the todos themselves (DoneBy "") or a review did (DoneByReview). Such a card can never be passed, so it is neither
// reminded nor counted. An auto-closed task (DoneByAuto) is the normal state: the judge finished it, but the owner
// still has to pass the card, and so is a supervised task its review accepted (#1226: the review's conclusion is that
// card, with the verdict on top). A conclusion is told after its review closed the task, so that does not end it; a
// decision ends once the task is closed or no longer waits for the user (the supervisor reviewed it again).
func acceptAskEnded(a *acceptAsk, t Todo) bool {
	if a.Conclusion {
		return a.Outcome == outcomeDecide && (t.DoneAt != nil || t.Supervision.state(t) != reviewNeedsUser)
	}
	return t.DoneAt != nil && t.DoneBy != db.DoneByAuto && !(t.DoneBy == db.DoneByReview && t.Supervision.state(t) == reviewAccepted)
}

// decisionOpen says a is a decision still waiting for the owner on task t as the caller read it: asked, not answered,
// not from before the to-do's latest reopen. t may be a moment old (its review state is not asked: the decision's
// review is what made it needs_user).
func decisionOpen(a *acceptAsk, t Todo) bool {
	return decideAsk(a) && !a.AskedAt.IsZero() && a.AcceptedAt.IsZero() && t.DoneAt == nil && !acceptAskStale(a, t)
}

// acceptAskStale says an ask belongs to an earlier round: the todo was reopened (PATCH done:false, change feedback
// on its card, or a failed start's retry) after the ask, so the old card is dead and the next deploy asks fresh.
// A ReopenedAt the auto-judge's second close leaves is harmless (the new ask is younger); zero AskedAt is a claim.
func acceptAskStale(a *acceptAsk, t Todo) bool {
	return !a.AskedAt.IsZero() && t.ReopenedAt != nil && a.AskedAt.Before(*t.ReopenedAt)
}
