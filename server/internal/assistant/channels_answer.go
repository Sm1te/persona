package assistant

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// hasPending: a card of the conversation's turn waits for an answer. A question asked without waiting is no such
// card: its turn ended when it went up, and that turn's closing words are the answer to send (ask_later.go).
func (s *Service) hasPending(ctx context.Context, machine, session string) bool {
	var res struct {
		Interactions []proto.Interaction `json:"interactions"`
	}
	if err := s.call(ctx, DeviceAll, DeviceInteractionsList, map[string]any{}, &res); err != nil {
		return false
	}
	return slices.ContainsFunc(res.Interactions, func(ix proto.Interaction) bool {
		return ix.MachineID == machine && ix.SessionID == session && !ix.AskedWithoutWaiting()
	})
}

// pendingIDs are the cards waiting for an answer now (nil when the hub could not be asked).
func (s *Service) pendingIDs(ctx context.Context) map[string]bool {
	var res struct {
		Interactions []proto.Interaction `json:"interactions"`
	}
	if err := s.call(ctx, DeviceAll, DeviceInteractionsList, map[string]any{}, &res); err != nil {
		return nil
	}
	out := make(map[string]bool, len(res.Interactions))
	for _, ix := range res.Interactions {
		out[ix.ID] = true
	}
	return out
}

// checkChannelRun answers a run whose turn ended (channel.Run.Check and Decide).
func (s *Service) checkChannelRun(id string) {
	r, prog := s.channelRunByID(id)
	if r == nil || prog == nil {
		return
	}
	if !prog.checking.TryLock() {
		return // another check is on it
	}
	defer prog.checking.Unlock()
	c := &s.chans
	c.mu.Lock()
	recalled := r.Recalled
	c.mu.Unlock()
	if recalled { // its end did not go out when it was recalled (recallChannelRun): tried again
		s.finishChannelRun(r, recalledResult())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sess, exists, err := s.sessionNow(ctx, r.MachineID, r.SessionID)
	if err != nil {
		return // the hub could not be asked: next tick
	}
	look := channel.Look{Exists: exists, Working: working(sess), Offline: sess.Status == proto.StatusOffline, Background: channel.Background(sess),
		Lang: s.botLang(ctx)}
	if !exists {
		d, _, _ := s.deviceKnown(ctx, r.MachineID)
		look.Online = d.Online
	} else if !look.Working {
		look.Waiting = sess.Status == proto.StatusBlocked || s.hasPending(ctx, r.MachineID, r.SessionID)
		look.Failed = !look.Waiting && func() bool { e, _ := turnError(sess); return e != "" }()
	}
	c.mu.Lock()
	look.Now = c.clock()
	step := r.Check(look)
	snap := *r
	snap.MsgIDs = slices.Clone(r.MsgIDs)
	c.mu.Unlock()
	switch step.Do {
	case channel.Wait:
		c.mu.Lock()
		prog.unsettled = time.Time{} // working or waiting again: a later read of its end settles afresh
		c.mu.Unlock()
		return
	case channel.Finish:
		s.finishChannelRun(r, &channel.Result{Note: step.Note, Quiet: step.Note == "", End: step.End})
		return
	}
	// The end was not seen (it came before the watch, or never), or the message was not there last time: look now and
	// then, not every second.
	c.mu.Lock()
	if (!snap.Seen || prog.notFound) && look.Now.Sub(prog.checked) < 10*time.Second {
		c.mu.Unlock()
		return
	}
	prog.checked = look.Now
	c.mu.Unlock()
	rd, err := s.readTurn(ctx, &snap)
	if err != nil {
		s.logf("channels: read the answer: %v", err)
		return // next tick
	}
	c.mu.Lock()
	prog.notFound = !rd.Found
	c.mu.Unlock()
	if !rd.Found {
		if snap.Queued(look.Now) {
			return // the message waits behind the turn that just ended: it starts the next one
		}
		// The message never reached the conversation even past QueuedMost. Reading the whole tail would answer with
		// the previous turn's last words, and the run's deletion would leave the real answer (when the message does
		// start) unclaimed: give up and ask the owner to resend instead (#4455).
		s.logf("channels: %s: the message never showed in the conversation; asking for a resend", r.ID)
		s.finishChannelRun(r, &channel.Result{Note: i18n.T(look.Lang, "assistant-channels_runs.not_delivered"), End: channel.EndGaveUp})
		return
	}
	if rd.Stopped == "" && !look.Working {
		rd.Stopped = channel.StopReason(sess.TurnError) // a Codex turn the model service failed writes no line of it
	}
	res := snap.Decide(rd, look)
	if res == nil {
		return
	}
	if res.Note != "" && channel.Placeholder(res.Answer) {
		res.Answer = "" // the note (its background work goes on) without the placeholder
	}
	if !res.Quiet && res.Note == "" && res.End == "" {
		empty := strings.TrimSpace(res.Answer) == "" || channel.Placeholder(res.Answer)
		// The node says a turn is over a moment before its last words are in the transcript: a turn read as over
		// without words at its end is read again for a few seconds before it counts as one without an answer.
		c.mu.Lock()
		unsure := !rd.Words || empty
		if !unsure {
			prog.unsettled = time.Time{}
		} else if prog.unsettled.IsZero() {
			prog.unsettled = look.Now
		}
		if unsure {
			prog.checked = time.Time{}
		}
		settling := unsure && look.Now.Sub(prog.unsettled) < channel.AnswerSettle
		c.mu.Unlock()
		if settling {
			return
		}
		if empty && res.Stopped == "" {
			if !snap.Asked() && s.askAgain(ctx, r, prog, rd.Last) {
				return
			}
			reason := s.noAnswerReason(look.Lang, res.Answer)
			s.logf("channels: %s: no answer this turn: %s", r.ID, reason)
			res.Answer = i18n.T(look.Lang, "assistant-channels_runs.no_answer", i18n.Args{"reason": reason})
		}
	}
	s.finishChannelRun(r, res)
}

// askAgain sends the conversation of a turn that ended without an answer (nothing, or a placeholder like "No
// response requested.") a message asking for it, once; the run then waits for that turn. False when it was not
// sent (a coding agent's conversation is not asked: the message would land in its work).
func (s *Service) askAgain(ctx context.Context, r *channel.Run, prog *chanLive, last int) bool {
	if r.TargetKind == channel.TargetAgent {
		return false
	}
	l := s.botLang(ctx)
	name := r.Channel
	if r.Channel == channel.Feishu {
		name = i18n.T(l, "assistant-channels_runs.feishu")
	} else if v, ok := s.pluginChannel(ctx, r.Channel); ok && v.Name != "" {
		name = v.Name
	}
	text := prompts.RenderChannelAnswerAgain(prompts.ChannelAnswerAgain{Name: name})
	_, body, _ := strings.Cut(text, "\n") // the first line is the context tag
	id := channel.ClientMsgID(r.Channel, r.ID+"-again")
	// The manager's own words, not the owner's: they preempt no background round (owner_preempt.go).
	p, err := s.messageParams(ctx, SendParams{MachineID: r.MachineID, SessionID: r.SessionID,
		Params: map[string]any{"session_id": r.SessionID, "text": text, "client_msg_id": id}}, false)
	if err == nil {
		err = s.sendOnce(ctx, r.MachineID, p)
	}
	if err != nil {
		s.logf("channels: %s: no answer this turn; asking again failed: %v", r.ID, err)
		return false
	}
	s.logf("channels: %s: no answer this turn; asked the conversation again", r.ID)
	c := &s.chans
	c.mu.Lock()
	r.AskAgain(last, id, channel.Probe(body), c.clock().UTC())
	prog.unsettled, prog.checked, prog.notFound = time.Time{}, time.Time{}, false
	s.saveChannelStateLocked()
	c.mu.Unlock()
	return true
}

// noAnswerReason is why a turn has no answer, for the owner.
func (s *Service) noAnswerReason(l i18n.Lang, answer string) string {
	if channel.Placeholder(answer) {
		return i18n.T(l, "assistant-channels_runs.reason_placeholder", i18n.Args{"text": strings.TrimSpace(answer)})
	}
	return i18n.T(l, "assistant-channels_runs.reason_silent")
}

// busTranscript is a conversation's transcript as the assistant reads it, over the hub's bus (channel.ReadTurn's source).
type busTranscript struct {
	s                *Service
	machine, session string
}

func (b busTranscript) ItemsAfter(ctx context.Context, after int) ([]proto.Item, bool, int, error) {
	var res struct {
		Items     []proto.Item `json:"items"`
		MoreAfter bool         `json:"more_after"`
		ToSeq     int          `json:"to_seq"`
	}
	err := b.s.call(ctx, b.machine, DeviceSessionItems, map[string]any{"session_id": b.session, "after_seq": after, "lazy_images": true}, &res)
	return res.Items, res.MoreAfter, res.ToSeq, err
}

func (b busTranscript) Full(ctx context.Context, id string) (proto.Item, error) {
	var full proto.Item
	err := b.s.call(ctx, b.machine, "session.item", map[string]any{"session_id": b.session, "item_id": id}, &full)
	return full, err
}

// readTurn is what the run's conversation answered after its base (channel.ReadTurn, read over the hub's bus).
func (s *Service) readTurn(ctx context.Context, r *channel.Run) (channel.Reading, error) {
	return channel.ReadTurn(ctx, busTranscript{s: s, machine: r.MachineID, session: r.SessionID}, r)
}

// finishChannelRun sends a run's answer and ends it, or — held — keeps it for the turn its background work starts.
// The run stays until its answer is handed to the hub: a sink that can't be reached now (the bus, the hub, a
// plugin's program restarting) is tried again on the next look, for a while (channelAnswerTries), with the same
// refs (a plugin sends a ref once).
func (s *Service) finishChannelRun(r *channel.Run, res *channel.Result) {
	c := &s.chans
	c.mu.Lock()
	prog := c.live[r.ID]
	if prog != nil {
		// Its answer is about to go out, so a later message must not fold this run in. It is marked before waiting
		// for prog.mu: a progress update may hold that lock for a while. When it was marked only after the lock, a
		// message that came in that time folded this run in, and the same answer went out twice: here, under its own
		// message, and again in the later message's answer.
		if c.answering == nil {
			c.answering = map[string]bool{}
		}
		c.answering[r.ID] = true
	}
	c.mu.Unlock()
	if prog == nil {
		return
	}
	defer func() {
		c.mu.Lock()
		delete(c.answering, r.ID)
		c.mu.Unlock()
	}()
	prog.mu.Lock()
	defer prog.mu.Unlock()
	if prog.finished {
		return
	}
	c.mu.Lock()
	if r.Recalled {
		res = recalledResult() // an answer read just before the recall does not go out
	}
	now := c.clock()
	took := now.Sub(r.Started)
	snap := *r
	snap.Ext = cloneExt(r.Ext)
	snap.Folded = slices.Clone(r.Folded)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sink, err := s.sinkFor(ctx, r.Channel)
	if err != nil {
		if s.answerAgain(prog, now) {
			s.logf("channels: %s: where to answer: %v; trying again", r.ID, err)
			return
		}
		s.logf("channels: %s: where to answer: %v; giving up on it", r.ID, err)
	}
	if sink != nil {
		quiet := res.Quiet || strings.TrimSpace(res.Answer) == "" && res.Note == "" && snap.Held
		answer := res.Answer
		if strings.TrimSpace(answer) == "" && res.Note == "" && !quiet {
			answer = emptyAnswer(s.botLang(ctx), res.Stopped)
		}
		if err := sink.answer(ctx, &snap, answer, res.Note, took, !res.Held, quiet, res.End); err != nil {
			if s.answerAgain(prog, now) {
				s.logf("channels: %s: the answer did not go out: %v; trying again", r.ID, err)
				return
			}
			s.logf("channels: %s: the answer did not go out: %v; giving up on it", r.ID, err)
		}
	}
	c.mu.Lock()
	prog.failing = time.Time{}
	if res.Held {
		r.Hold(res.Last, now.UTC())
		prog.unsettled = time.Time{}
		delete(r.Ext, "card") // the next turn gets its own progress message
		for _, k := range []string{"cot", "cot_msg", "cot_para", "cot_calls", "cot_done", "cot_off"} {
			delete(r.Ext, k) // the next turn gets its own agent message (and asks for one again)
		}
		prog.steps, prog.live, prog.dirty = nil, "", false
		prog.from, prog.upTo = 0, 0
		prog.open, prog.calls, prog.read, prog.edited, prog.subs, prog.subsMore = nil, 0, nil, nil, nil, 0
		prog.items, prog.cot = nil, cotProgress{}
	} else {
		prog.finished = true
		delete(c.live, r.ID)
		c.st.Runs = slices.DeleteFunc(c.st.Runs, func(o *channel.Run) bool { return o == r })
		if c.answered == nil {
			c.answered = map[[2]string]time.Time{}
		}
		for k, at := range c.answered {
			if now.Sub(at) > time.Hour {
				delete(c.answered, k)
			}
		}
		c.answered[[2]string{r.MachineID, r.SessionID}] = now
	}
	others := slices.ContainsFunc(c.st.Runs, func(o *channel.Run) bool { return o.Channel == r.Channel })
	s.saveChannelStateLocked()
	c.mu.Unlock()
	if res.Held {
		return
	}
	close(prog.done)
	s.channelsChanged()
	if ts, ok := sink.(channelTyping); ok && !others {
		ts.typing(ctx, false)
	}
}

// channelAnswerTries is how long an answer that can't be handed over is tried again before the run gives up on it.
var channelAnswerTries = 2 * time.Minute

// answerAgain: the answer failed now; true while it is worth trying again (the first failure starts the clock).
func (s *Service) answerAgain(prog *chanLive, now time.Time) bool {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	if prog.failing.IsZero() {
		prog.failing = now
	}
	prog.checked = time.Time{} // the next look reads again at once
	return now.Sub(prog.failing) < channelAnswerTries
}

func cloneExt(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// channelAnswered: the conversation's turn is (or just was) answered in a channel, so the sync mirror leaves it
// alone (the hub's finished push goes by the same, from the snapshot). "Just": within the finished push's delay.
func (s *Service) channelAnswered(ctx context.Context, machine, session string) bool {
	window := 30*time.Second + time.Duration(s.hello(ctx).DoneDelayMS)*time.Millisecond
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	s.loadChannelStateLocked(ctx)
	for _, r := range c.st.Runs {
		if r.SessionID == session {
			return true
		}
	}
	at, ok := c.answered[[2]string{machine, session}]
	return ok && c.clock().Sub(at) < window
}

// channelTurnEnded: a conversation finished a turn (session.turn_ended). Its runs are checked at once rather than on
// the next tick; a turn nobody asked for in a channel is mirrored to those that sync — not a quiet task's normal finish
// (quietTaskTurn, #2396), whose news, if any, went out with notify_user.
func (s *Service) channelTurnEnded(ctx context.Context, machine, session string) {
	if s.meetingTurnNow(machine, session) {
		return // a meeting's batch: neither a channel's answer nor mirrored (meeting_turns.go)
	}
	if s.justMeNow(machine, session) {
		return // a Just me turn: mirrored nowhere (meeting_justme.go)
	}
	c := &s.chans
	c.mu.Lock()
	s.loadChannelStateLocked(ctx)
	var ids []string
	for _, r := range c.st.Runs {
		if r.MachineID == machine && r.SessionID == session {
			r.Seen = true
			if p := c.live[r.ID]; p != nil {
				p.checked = time.Time{} // a turn ended: look now, whatever the last look found
			}
			ids = append(ids, r.ID)
		}
	}
	c.mu.Unlock()
	for _, id := range ids {
		s.checkChannelRun(id)
	}
	if len(ids) == 0 && !s.quietTaskTurn(ctx, machine, session) && !s.channelAnswered(ctx, machine, session) {
		s.feishuMirror(ctx, machine, session)
		s.extensionMirror(ctx, machine, session)
	}
}

// emptyAnswer is what a channel is told of a turn that ended without words: why it stopped, when the conversation
// says (an API error, an interruption), so the owner need not open the app to find out.
func emptyAnswer(l i18n.Lang, stopped string) string {
	if stopped = strings.TrimSpace(stopped); stopped != "" {
		return i18n.T(l, "assistant-channels_runs.no_text_stopped", i18n.Args{"reason": stopped})
	}
	return i18n.T(l, "assistant-channels_runs.no_text")
}
