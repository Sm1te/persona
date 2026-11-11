package assistant

import (
	"cmp"
	"context"
	"math"
	"slices"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
)

// The evening routine's facts about today's all-day recordings (#4741): those whose span overlaps the owner's today,
// as at the recap time, their parts merged by time. For the model only: never a row of the card, the brief item or a
// channel's message (briefFacts keeps it apart in Day), so the routine's answer is where the user reads about the day
// besides the recap's own push.

const (
	briefDayRunes   = 4000 // what the section may take, all of it (briefDayBound): entries from the middle of the day go first
	briefDayReasons = 6    // gap reasons given (those with the most time; the phone's own six fit); the rest are counted together
	briefDayRanges  = 5    // stretches of one gap reason given with their times (the longest); the rest are counted
	briefDayParts   = 5    // parts given (the first and the latest); those from the middle are counted
	briefDaySummary = 300
	briefDayTitle   = 60
	briefDayWhat    = 100
)

// briefDay is today's all-day recording as the evening routine's facts say it, at now in the owner's zone loc; nil
// without one today (or without the knowledge base).
func (s *Service) briefDay(ctx context.Context, now time.Time, loc *time.Location) *prompts.BriefDay {
	e, err := s.kbGet(ctx)
	if err != nil {
		return nil
	}
	recs := e.TodaysDayRecordings(now, loc) // the owner's zone: a read-only knowledge base's store is not put in it
	if len(recs) == 0 {
		return nil
	}
	today := now.In(loc).Format(time.DateOnly)
	clock := func(t time.Time) string {
		if t = t.In(loc); t.Format(time.DateOnly) != today {
			return t.Format("01-02 15:04")
		}
		return t.Format("15:04")
	}
	type entry struct {
		at time.Time
		en prompts.BriefDayEntry
	}
	type action struct {
		at time.Time
		a  prompts.BriefDayAction
	}
	var gaps []briefGap
	var entries []entry
	var actions []action
	var first, last, text time.Time
	type gapKey struct {
		from, to int64
		reason   string
	}
	seen := map[gapKey]bool{}
	d := &prompts.BriefDay{Date: today}
	for i := range recs {
		r := &recs[i]
		at := func(sec float64) time.Time { return r.StartedAt.Add(time.Duration(sec * float64(time.Second))) }
		to := r.Duration
		if r.Coverage != nil {
			to = r.Coverage.To
			for _, g := range r.Coverage.Gaps {
				// A later part holds the day's coverage as the earlier one did (the parts share started_at).
				x := briefGap{at(g.From), at(g.To), g.Reason}
				if k := (gapKey{x.from.UnixNano(), x.to.UnixNano(), x.reason}); !seen[k] {
					seen[k] = true
					gaps = append(gaps, x)
				}
			}
		}
		// Recorded is its span (until now while it is open); how far its text goes is said apart.
		start, end := e.DaySpan(r, now)
		if first.IsZero() || start.Before(first) {
			first = start
		}
		if end.After(last) {
			last = end
		}
		if t := at(to); t.After(text) {
			text = t
		}
		part := prompts.BriefDayPart{Part: max(r.Part, 1)}
		rc, err := e.ReadDayRecap(r.ID)
		switch {
		case err == nil:
			part.Made, part.Upto, part.Final, part.Partial = clock(rc.GeneratedAt), clock(at(rc.Covers.To)), rc.Final, rc.Partial
			part.Summary = clip(oneLine(rc.Summary), briefDaySummary)
			if r.RecapState == kb.MinutesPending || r.RecapState == kb.MinutesFailed { // a newer one, the one shown stays
				part.State = r.RecapState
			}
			for _, en := range rc.Timeline {
				entries = append(entries, entry{at(en.From), prompts.BriefDayEntry{From: clock(at(en.From)), To: clock(at(en.To)),
					Meeting: en.Kind == kb.RecapMeeting, Title: clip(oneLine(en.Title), briefDayTitle)}})
			}
			for _, a := range rc.Actions {
				if a.TodoID == "" { // confirmed ones are to-dos the routine reads as such
					actions = append(actions, action{at(a.At), prompts.BriefDayAction{Who: oneLine(a.Who), What: clip(oneLine(a.What), briefDayWhat)}})
				}
			}
		case r.Chars == 0:
			part.State, part.Recorded = "none", int(math.Round(r.Duration/60))
		default:
			part.State = r.RecapState
		}
		d.Parts = append(d.Parts, part)
	}
	d.From, d.To = clock(first), clock(last)
	if t := clock(text); text.Before(last) && t != d.To {
		d.Transcribed = t
	}
	d.Gaps = briefDayGaps(gaps, clock)
	slices.SortStableFunc(entries, func(a, b entry) int { return a.at.Compare(b.at) })
	for _, x := range entries {
		d.Timeline = append(d.Timeline, x.en)
	}
	slices.SortStableFunc(actions, func(a, b action) int { return a.at.Compare(b.at) })
	for _, x := range actions {
		d.Actions = append(d.Actions, x.a)
	}
	briefDayBound(d)
	return d
}

// briefGap is a stretch of the day with no text and why (kb.CoverageGap at its clock times).
type briefGap struct {
	from, to time.Time
	reason   string
}

// briefDayGaps are gaps by reason (in the order the reasons first come in the day): how many, how long in all, and
// the longest briefDayRanges of them, by time.
func briefDayGaps(gaps []briefGap, clock func(time.Time) string) []prompts.BriefDayGap {
	slices.SortStableFunc(gaps, func(a, b briefGap) int { return a.from.Compare(b.from) })
	var order []string
	by := map[string][]briefGap{}
	for _, g := range gaps {
		if _, ok := by[g.reason]; !ok {
			order = append(order, g.reason)
		}
		by[g.reason] = append(by[g.reason], g)
	}
	length := func(g briefGap) time.Duration { return g.to.Sub(g.from) }
	var out []prompts.BriefDayGap
	for _, reason := range order {
		list := by[reason]
		var total time.Duration
		for _, g := range list {
			total += length(g)
		}
		longest := slices.Clone(list)
		slices.SortStableFunc(longest, func(a, b briefGap) int { return cmp.Compare(length(b), length(a)) })
		longest = longest[:min(len(longest), briefDayRanges)]
		slices.SortStableFunc(longest, func(a, b briefGap) int { return a.from.Compare(b.from) })
		g := prompts.BriefDayGap{Reason: cmp.Or(reason, "?"), Count: len(list), Minutes: max(1, int(math.Round(total.Minutes()))),
			More: len(list) > len(longest)}
		for _, x := range longest {
			g.Ranges = append(g.Ranges, clock(x.from)+"–"+clock(x.to))
		}
		out = append(out, g)
	}
	return out
}

// briefDayBound keeps d within briefDayRunes in all. At most briefDayParts parts are listed (those from the middle go,
// counted) and briefDayReasons gap reasons (those with the least time go, counted together): with the reasons, the
// summaries and the titles clipped, that is well within it. Then while it is longer the timeline's entries from the
// middle of the day go (its start and its latest hours say the most), then the last suggested to-dos, then the
// summaries are cut short.
func briefDayBound(d *prompts.BriefDay) {
	for len(d.Parts) > briefDayParts {
		mid := len(d.Parts) / 2
		d.Parts = slices.Delete(d.Parts, mid, mid+1)
		d.PartsLeft++
	}
	if len(d.Gaps) > briefDayReasons {
		most := slices.Clone(d.Gaps)
		slices.SortStableFunc(most, func(a, b prompts.BriefDayGap) int { return cmp.Compare(b.Minutes, a.Minutes) })
		keep := map[string]bool{}
		for _, g := range most[:briefDayReasons] {
			keep[g.Reason] = true
		}
		d.Gaps = slices.DeleteFunc(d.Gaps, func(g prompts.BriefDayGap) bool { // in the day's order still
			if keep[g.Reason] {
				return false
			}
			d.GapReasonsLeft++
			d.GapsLeft += g.Count
			d.GapMinutesLeft += g.Minutes
			return true
		})
	}
	over := func() bool { return prompts.BriefDayRunes(d) > briefDayRunes }
	for over() && len(d.Timeline) > 0 {
		mid := len(d.Timeline) / 2
		d.Timeline = slices.Delete(d.Timeline, mid, mid+1)
		d.Left++
	}
	for over() && len(d.Actions) > 0 {
		d.Actions = d.Actions[:len(d.Actions)-1]
		d.ActionsLeft++
	}
	for i := range d.Parts {
		if !over() {
			return
		}
		d.Parts[i].Summary = clip(d.Parts[i].Summary, briefDaySummary/3)
	}
}
