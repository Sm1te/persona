package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/feishubot"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
)

// askAcceptance keeps task t's "Please verify" ask once (reply: what its agent said last, on machine/session) and posts its
// card when the bot can. The ask is a fact of the hub, not of the card: with no card due it is kept all the same (NoCard),
// and the briefing, the board and the snapshot list it. A card that did not go out (the bot's status unreadable, the send
// failed) is kept as an ask without one yet, listed the same, and retryAcceptance posts it.
func (s *Service) askAcceptance(t Todo, machine, session, reply string) {
	s.askAcceptanceRound(t, machine, session, reply, false)
}

// askAcceptanceRound is askAcceptance; again: the owner is to look after all (task_review user_check), so an ask of this
// round already passed (by the assistant or the supervisor) gives way to a new one, its cards dead from then on.
func (s *Service) askAcceptanceRound(t Todo, machine, session, reply string, again bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	b := &s.accept
	b.mu.Lock()
	if !s.loadAcceptLocked(ctx) {
		b.mu.Unlock()
		return
	}
	var earlier []string
	if old := b.asks[t.ID]; old != nil && (old.AskedAt.IsZero() || !old.Conclusion) && !acceptAskStale(old, t) &&
		!(again && !old.AskedAt.IsZero() && !old.AcceptedAt.IsZero()) {
		b.mu.Unlock()
		return
	} else if old != nil && decisionOpen(old, t) {
		// A decision waits for the owner: a turn that says "deployed" meanwhile does not take its place (#3804). Once it
		// is settled, the next deployed turn asks.
		b.mu.Unlock()
		return
	} else if old != nil {
		// asked before the task's latest reopen, or a conclusion (conclusion.go): the new round gets its own card, the old
		// ones stay dead
		earlier = lastOf(slices.Concat(old.Earlier, old.Messages), acceptEarlierMost)
		delete(b.asks, t.ID)
	}
	b.asks[t.ID] = &acceptAsk{Earlier: earlier} // claimed: a second turn ending meanwhile does not ask twice
	b.mu.Unlock()
	a := s.acceptLines(ctx, t, machine, session, reply)
	a.MachineID, a.SessionID, a.AskedAt, a.Earlier = machine, session, time.Now().UTC(), earlier
	if !s.ownerVerifies(t) {
		s.assistantAccepts(ctx, t, a)
		return
	}
	s.postAsk(ctx, t, a)
}

// ownerVerifies: deployed task t is the owner's to try (a big feature: a high-priority todo). Any other deployed task
// is checked and accepted by the supervising assistant (assistantAccepts), so the owner is not asked for each one
// (#3205): its "Please verify" card is not posted, and nothing waits for the owner.
func (s *Service) ownerVerifies(t Todo) bool {
	return t.Priority == "high" || s.ownerVerifiesAll || userCheckAsked(t)
}

// userCheckAsked: the supervisor's last review accepted task t and still wants the owner to look (user_check, #3240).
func userCheckAsked(t Todo) bool {
	v := t.Supervision
	if v == nil || len(v.Reviews) == 0 {
		return false
	}
	r := v.Reviews[len(v.Reviews)-1]
	return r.Verdict == reviewAccepted && r.UserCheck
}

// assistantAccepts keeps ask a, which task t claimed in the book, card-less and accepted by the supervising assistant
// (ByAssistant): its stage moves to verified as when the owner passes it.
func (s *Service) assistantAccepts(ctx context.Context, t Todo, a acceptAsk) {
	a.NoCard, a.ByAssistant = true, true
	b := &s.accept
	b.mu.Lock()
	if c := b.asks[t.ID]; c != nil && c.AskedAt.IsZero() {
		a.Earlier = c.Earlier
	} else if c != nil && decisionOpen(c, t) {
		b.mu.Unlock() // a decision was asked while this one was being written: it stands (#3804)
		return
	}
	// Accepted as it is kept, in one write: a decision asked a moment later must not be what a second look at the book
	// accepts (acceptTry takes a decision's recommendation).
	a.AcceptedAt = time.Now().UTC()
	b.asks[t.ID] = &a
	if err := s.saveAcceptErrLocked(ctx); err != nil {
		a.AcceptedAt = time.Time{} // kept only in memory it would wait again after a restart: not accepted
		b.mu.Unlock()
		s.logf("task %s: accepted by the assistant: %v", t.ID, err)
		s.askChanged(ctx)
		return
	}
	cp := a
	b.mu.Unlock()
	s.askChanged(ctx)
	s.taskVerified(ctx, t.ID, cp)
	s.logf("task %s: deployed, accepted by the supervising assistant (not a big feature: the owner is not asked)", t.ID)
}

// postAsk posts the card of ask a, which task t claimed in the book, when the bot can, and keeps a in the claim's place;
// true when its card went out. No card is due when the bot is off or not paired, or its task reports are off. A status
// that cannot be read (the manager away) is not that: the card is for later, and the ask waits for it without one (not
// NoCard), for retryAcceptance.
func (s *Service) postAsk(ctx context.Context, t Todo, a acceptAsk) bool {
	b := &s.accept
	var st FeishuStatus
	var stErr error
	if !a.ForBriefing {
		st, stErr = s.feishuStatus(ctx)
	}
	switch {
	case a.ForBriefing:
		a.NoCard = true // a decision that can wait: the briefing lists it, or its card goes out once it is pushed (decision_wait.go)
	case stErr != nil:
		s.logf("task %s: acceptance: %v (its card is tried again later)", t.ID, stErr)
	case st.Ready && st.Features.Reports:
		mid, err := s.botPostKeyed(ctx, "", s.acceptCard(ctx, t, a, false), askSummary(s.botLang(ctx), &a), acceptCardKey(t.ID, a.AskedAt))
		if err != nil {
			s.logf("task %s: acceptance: %v (its card is tried again later)", t.ID, err)
			break
		}
		a.MessageID, a.Messages = mid, []string{mid}
	default:
		a.NoCard = true
	}
	posted := a.MessageID != "" // read before a is shared: a retry may set its card once the book has it
	// The ask is kept whether or not the caller still waits: a tool call given up on while the bot's status was read
	// must not leave it in memory only, gone with the next restart (#3804).
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	b.mu.Lock()
	if c := b.asks[t.ID]; c != nil && c.AskedAt.IsZero() {
		a.Earlier = c.Earlier // the claim's: it also has a card of the round before that a retry posted meanwhile
	} else if c != nil && !a.Conclusion && decisionOpen(c, t) {
		// A decision was asked while this "Please verify" was being written: it stands, and this card is dead (#3804).
		if a.MessageID != "" {
			c.Earlier = lastOf(append(slices.Clone(c.Earlier), a.MessageID), acceptEarlierMost)
			s.saveAcceptLocked(sctx)
		}
		b.mu.Unlock()
		return false
	}
	b.asks[t.ID] = &a
	if err := s.saveAcceptErrLocked(sctx); err != nil {
		s.logf("acceptance: save: %v", err)
		if a.ForBriefing { // not written: it is pushed now, not held in memory only (decision_wait.go)
			a.ForBriefing, a.NoCard = false, false
		}
	}
	b.mu.Unlock()
	s.askChanged(ctx) // it waits for the owner now: the apps and the board
	return posted
}

// acceptLines are the card's three lines: the small model reads the task and the reply; without it (or when it
// answers nothing usable) the task's title, its page and a plain check.
func (s *Service) acceptLines(ctx context.Context, t Todo, machine, session, reply string) acceptAsk {
	l := s.botLang(ctx)
	web := s.webBase(ctx)
	var links []string
	add := func(u string) {
		if u != "" && !slices.Contains(links, u) {
			links = append(links, u)
		}
	}
	for _, u := range urlRe.FindAllString(reply, -1) {
		add(strings.TrimRight(u, ".,;:!?"))
	}
	add(todoLink(web, t.ID))
	if web != "" && session != "" {
		add(sessionLink(web, machine, session, false))
	}
	a := acceptAsk{Changed: clip(oneLine(t.Title), 40), Link: todoLink(web, t.ID), How: i18n.T(l, "assistant-reports.accept_how_default")}
	var out struct {
		Changed string `json:"changed"`
		Link    string `json:"link"`
		How     string `json:"how"`
	}
	input := prompts.RenderAcceptanceInput(prompts.Acceptance{Title: t.Title, Prompt: clipMiddle(strings.TrimSpace(t.Notes), 800),
		Reply: clipMiddle(reply, 2000), Links: links})
	if err := s.purposeModel("inbox.judge").JSON(ctx, prompts.RenderAcceptance(), input, &out); err != nil {
		s.logf("task %s: acceptance lines: %v (plain ones)", t.ID, err)
		return a
	}
	if c := clip(oneLine(out.Changed), 60); c != "" {
		a.Changed = c
	}
	if slices.Contains(links, strings.TrimSpace(out.Link)) {
		a.Link = strings.TrimSpace(out.Link) // only one it was given: never a link it made up
	}
	if h := clip(oneLine(out.How), 120); h != "" {
		a.How = h
	}
	return a
}

// acceptCard is the "Please verify" card (again: the morning's reminder), or how it ended (accepted): the verdict line on
// top, then the three lines. A conclusion has its own card (conclusionCard).
func (s *Service) acceptCard(ctx context.Context, t Todo, a acceptAsk, again bool) *feishubot.Card {
	if a.Conclusion {
		return s.conclusionCard(ctx, t, a)
	}
	l := s.botLang(ctx)
	head := "assistant-reports.accept_title"
	if again {
		head = "assistant-reports.accept_again"
	}
	title := i18n.T(l, head, i18n.Args{"task": clip(oneLine(t.Title), 30)})
	c := feishubot.NewCard("🧪 "+title, feishubot.Purple, title)
	link := i18n.T(l, "assistant-reports.accept_no_link")
	if a.Link != "" {
		link = "[" + feishubot.EscapeMarkdown(clip(a.Link, 80)) + "](" + a.Link + ")"
	}
	c.Markdown("**" + feishubot.EscapeMarkdown(verdictText(l, &a)) + "**")
	c.Markdown(i18n.T(l, "assistant-reports.accept_lines", i18n.Args{"changed": feishubot.EscapeMarkdown(a.Changed), "link": link,
		"how": feishubot.EscapeMarkdown(a.How)}))
	if !a.AcceptedAt.IsZero() {
		return c.Note(acceptedNote(l, &a))
	}
	bs := []feishubot.Button{feishubot.Btn(i18n.T(l, "assistant-reports.accept_pass"), feishubot.Primary, map[string]any{"a": "accept", "id": t.ID})}
	if a.Link != "" {
		bs = append(bs, feishubot.LinkBtn(i18n.T(l, "assistant-reports.accept_open"), a.Link))
	}
	c.Buttons(bs...)
	return c.Note(i18n.T(l, "assistant-reports.accept_reply_hint"))
}
