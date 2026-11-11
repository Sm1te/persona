package assistant

import (
	"cmp"
	"context"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

// ---- what the task's events say ----

// taskNotice is what happened to task t (state: done, needs, interrupted, failed; ask, text as quietNotify has them):
// a supervised task's supervisor gets a signal, any other quiet task's conversation its notice.
func (s *Service) taskNotice(ctx context.Context, t Todo, machine, state, ask, text string) {
	if t.Supervision == nil {
		if !t.Quiet {
			if state == "failed" && t.Origin != "" {
				s.notifyTaskFailed(ctx, t, machine, text)
			}
			return
		}
		s.quietNotify(ctx, t, machine, state, ask, text)
		return
	}
	if state == "failed" && !t.Quiet && t.Origin != "" && !s.isMain(ctx, t.Origin) && !s.isLeader(ctx, t.Origin) &&
		!s.isLeaderRef(t.Supervision.Supervisor) {
		// A side conversation's task, whoever supervises it now: the user still gets the failure push they got before
		// the task had a ledger (#5081), and the supervisor its signal. A leader conversation's own task, and a
		// worker's, stay their supervisor's business.
		s.notifyTaskFailed(ctx, t, machine, text)
	}
	g := superviseSignal{Todo: t.ID, Machine: machine, Detail: text, At: time.Now()}
	switch state {
	case "failed":
		g.Kind, g.Key = sigFailed, text
	case "interrupted":
		g.Kind, g.Key = sigInterrupted, time.Now().UTC().Format(time.RFC3339)
	case "needs":
		b := &s.supervise
		b.mu.Lock()
		b.init()
		b.asks[t.ID] = ask
		b.mu.Unlock()
		if !t.Quiet {
			return // the user was pushed: the supervisor hears of it if it waits (watchSignals)
		}
		g.Kind, g.Key, g.Ask, g.Detail = sigNeedsYou, "ask@"+time.Now().UTC().Format(time.RFC3339), ask, ""
	default:
		return // done: the judge's verdict says what follows (superviseJudged)
	}
	s.signal(g)
}

// superviseJudged: supervised task t's agents all stopped after work since work, and the judge said c (err: it could
// not). Finished: the task waits for review. Not finished: the supervisor hears it stopped short.
func (s *Service) superviseJudged(ctx context.Context, t Todo, work time.Time, c taskDoneCheck, err error) {
	st := t.Supervision.state(t)
	key := work.UTC().Format(time.RFC3339Nano)
	if err == nil && c.Verdict == verdictDone {
		if s.autoReviewBegin(ctx, t, work, c.Reason) {
			return // the independent review first; it ends in rework or the wait for review (supervise_autoreview.go)
		}
		detail := c.Reason
		if c.AutoCheck.passed() && !s.agentSupervised(t) { // the judge's own check passed (supervise_sample.go); a leader's worker is reviewed by its leader
			if pct := s.reviewSamplePercent(ctx); !spotCheck(t.ID, key, pct) {
				if s.judgeClose(ctx, t, key, c.Reason, c.AutoCheck) {
					return
				}
			} else {
				detail = "抽查：判官已逐条核对通过且已上线，" + c.Reason // i18n-ignore: the supervision message is the assistant's prompt
			}
		}
		v, _, uerr := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
			if v.state(t) == reviewPending && v.Waits == nil {
				return false
			}
			v.State, v.Waits = reviewPending, nil
			return true
		})
		if uerr != nil {
			s.logf("task %s: pending review: %v", t.ID, uerr)
			return
		}
		s.logf("task %s: waits for review: %s", t.ID, detail)
		s.signal(superviseSignal{Todo: t.ID, Kind: sigReview, Key: reviewKey(&v), Detail: detail})
		return
	}
	if st == reviewNeedsUser || st == reviewParked {
		return // the supervisor knows it waits: for the user, or parked until its time (parked.go)
	}
	if v := t.Supervision; t.DoneAt == nil && (v.State == reviewAccepted || v.State == reviewCancelled) {
		// Closed by its review, open again only because its agent was sent more (taskWorking, #5892): what it did then
		// is no task stopped short. Done, it waits for review again (above); else the supervisor is not told (#3111).
		s.logf("task %s: %s before, more work since judged %s: not told", t.ID, v.State, cmp.Or(c.Verdict, "with an error"))
		return
	}
	if err != nil {
		// The judge failed (the model service, a reply that was no verdict): no word on whether it is finished, not
		// "stopped short" (#3111). The supervisor reads the agent's reply and decides.
		s.signal(superviseSignal{Todo: t.ID, Kind: sigUnjudged, Key: key, Detail: "判定模型出错"})
		return
	}
	if c.Verdict == verdictNeedsUser {
		s.noteOwnerWait(ctx, t, work, c.Reason)
	}
	detail := verdictWords[c.Verdict]
	if c.Reason != "" {
		detail += "：" + c.Reason
	}
	if c.Verdict == verdictUnsure { // no word on it either way: not "stopped short" (#3111)
		s.signal(superviseSignal{Todo: t.ID, Kind: sigUnjudged, Key: key, Detail: detail})
		return
	}
	if d := c.Delivery; c.Verdict == verdictUndelivered && d.held() { // where it is held, to chase (delivery.go)
		detail += "（" + deliveryLabels[d.State]
		if d.Gate != "" {
			detail += "，卡在「" + d.Gate + "」"
		}
		detail += "）"
	}
	s.signal(superviseSignal{Todo: t.ID, Kind: sigIdle, Key: key, Detail: detail})
}

var verdictWords = map[string]string{verdictNotDone: "没做完", verdictWaiting: "在等别人或别的事", verdictNeedsUser: "要用户决定",
	verdictUnsure: "看不出做没做完", verdictUndelivered: "做完了但还没上线"}

// noteOwnerWait keeps on supervised task t's ledger that the stretch of work begun at work stopped on a decision only
// the owner can make (the done judge's needs_user, with its reason), so task_review accepted cannot close it and lose
// the decision (#1277). The same stretch judged again keeps the first note.
func (s *Service) noteOwnerWait(ctx context.Context, t Todo, work time.Time, reason string) {
	if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.Waits != nil && v.Waits.Work.Equal(work) {
			return false
		}
		v.Waits = &OwnerWait{Work: work.UTC(), At: time.Now().UTC(), Reason: clip(oneLine(reason), 200)}
		return true
	}); err != nil {
		s.logf("task %s: owner wait: %v", t.ID, err)
	}
}

// supervisedWorking: one of supervised task t's agents started a turn: a review waiting is moot (the work goes on),
// and a parked task is parked no longer (someone sent its agent something: it is supervised as before, parked.go).
func (s *Service) supervisedWorking(ctx context.Context, t Todo) {
	s.autoReviewWorking(ctx, t)
	if st := t.Supervision.state(t); st != reviewPending && st != reviewParked && (t.Supervision == nil || t.Supervision.Waits == nil) {
		return
	}
	if _, _, err := s.updateSupervision(ctx, t.ID, func(v *Supervision) bool {
		if v.State != reviewPending && v.State != reviewParked && v.Waits == nil {
			return false
		}
		v.Waits = nil // its agent goes on: the owner's answer reached it, or the work changed (#1277)
		if v.State != reviewPending && v.State != reviewParked {
			return true
		}
		v.State = ""
		if v.Round > 0 {
			v.State = reviewRework
		}
		return true
	}); err != nil {
		s.logf("task %s: supervision: %v", t.ID, err)
	}
	b := &s.supervise
	b.mu.Lock()
	delete(b.pending, t.ID+"|"+sigReview)
	delete(b.pending, t.ID+"|"+sigParkedDue)
	b.mu.Unlock()
}

// notifyTaskFailed pushes a failure notification to the user's phone when a task started from a side conversation
// (neither supervised nor quiet) could not start, so the user is informed with next steps (#5081).
// Muted conversations send nothing; health conversations omit the task title.
func (s *Service) notifyTaskFailed(ctx context.Context, t Todo, machine, reason string) {
	here := s.settings(ctx).MachineID
	if here == "" {
		here = machine
	}
	if t.Origin == "" || s.muted(here, t.Origin) {
		return
	}
	l := s.lang(ctx)
	title := i18n.T(l, "assistant-tasks.task_failed_title", i18n.Args{"name": t.Title})
	if s.inHealth(ctx, t.Origin) {
		title = i18n.T(l, "assistant-tasks.task_failed_title_health")
	}
	r := strings.TrimRight(strings.TrimSpace(reason), ".。") // i18n-ignore: trailing full stops trimmed, not text
	body := i18n.T(l, "assistant-tasks.task_failed_body_no_reason")
	if r != "" {
		body = i18n.T(l, "assistant-tasks.task_failed_body", i18n.Args{"reason": r})
	}
	sub := s.machineName(machine)
	if sub == "" {
		sub = "Persona"
	}
	s.push(ctx, Notification{
		Subtitle:  sub,
		DeviceID:  here,
		SessionID: t.Origin,
		Title:     title,
		Body:      body,
	})
}
