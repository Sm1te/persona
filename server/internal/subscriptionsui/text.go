package subscriptionsui

import (
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Small helpers for writing views.

func row(key, title string) wire.Component {
	return wire.Component{Type: "row", Key: key, Title: title}
}

func text(key, s, style string) wire.Component {
	return wire.Component{Type: "text", Key: key, Text: s, Style: style}
}

func button(key, title, action string) wire.Component {
	return wire.Component{Type: "button", Key: key, Title: title, Action: &wire.Action{ID: action}}
}

func badge(s, tone string) *wire.Badge { return &wire.Badge{Text: s, Tone: tone} }

func open(st state, title string) *wire.Action {
	return &wire.Action{ID: "open:" + st.P, Push: &wire.Push{State: st.String(), Title: title}}
}

// colors a source's icon takes, by its id, so one source keeps its color everywhere.
var colors = []string{"blue", "green", "orange", "purple", "teal", "indigo", "pink", "red", "amber"}

func colorFor(id string) string {
	h := fnv.New32a()
	h.Write([]byte(id))
	return colors[int(h.Sum32()%uint32(len(colors)))]
}

// olderIcons stand in for UI version 6's icons in an older app, which would otherwise draw the row with no icon at
// all, out of line with the rows around it.
var olderIcons = map[string]string{"wallet": "money", "bank": "money", "trend_up": "chart", "trend_down": "chart", "info": "doc",
	"edit": "doc", "share": "link", "location": "folder", "robot": "person", "heart": "star", "shield": "check", "lock": "settings",
	"unlock": "settings", "home": "folder", "camera": "file", "video": "file", "play": "file", "refresh": "clock", "trash": "file"}

// icon is a source's icon as this app can draw it: one it knows, never none.
func (p *pages) icon(icon string) string {
	if icon == "" {
		return "plugin"
	}
	if p.env.UIVersion >= 6 || slices.Index(wire.NavIcons, icon) <= slices.Index(wire.NavIcons, "file") {
		return icon
	}
	if old, ok := olderIcons[icon]; ok {
		return old
	}
	return "plugin"
}

// Words for times, intervals and counts.

// ago is a past time as people say it: just now, minutes or hours ago, then today / yesterday / a date with the time.
func (p *pages) ago(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := p.env.Now.Sub(*t)
	switch {
	case d < time.Minute:
		return p.t("time.just_now")
	case d < time.Hour:
		return p.t("time.minutes_ago", i18n.Args{"count": int(d / time.Minute)})
	case d < 6*time.Hour:
		return p.t("time.hours_ago", i18n.Args{"count": int(d / time.Hour)})
	}
	return p.when(*t)
}

// soon is a coming time: in some minutes, or the clock time (with the day when not today).
func (p *pages) soon(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := t.Sub(p.env.Now)
	switch {
	case d < time.Minute:
		return p.t("time.now")
	case d < time.Hour:
		return p.t("time.in_minutes", i18n.Args{"count": int((d + time.Minute - 1) / time.Minute)})
	}
	return p.when(*t)
}

// when is a time with its day: today, yesterday, tomorrow, or the date.
func (p *pages) when(t time.Time) string {
	t = t.In(p.env.Loc)
	now := p.env.Now.In(p.env.Loc)
	hm := t.Format("15:04")
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, p.env.Loc) }
	switch day(t).Sub(day(now)) / (24 * time.Hour) {
	case 0:
		return p.t("time.today", i18n.Args{"time": hm})
	case -1:
		return p.t("time.yesterday", i18n.Args{"time": hm})
	case 1:
		return p.t("time.tomorrow", i18n.Args{"time": hm})
	}
	return p.t("time.date", i18n.Args{"month": int(t.Month()), "day": t.Day(), "time": hm})
}

// clock is a time of today as a clock time alone, otherwise with its day.
func (p *pages) clock(t *time.Time) string {
	if t == nil {
		return ""
	}
	l, now := t.In(p.env.Loc), p.env.Now.In(p.env.Loc)
	if l.Year() == now.Year() && l.YearDay() == now.YearDay() {
		return l.Format("15:04")
	}
	return p.when(*t)
}

// every is how often a subscription pulls: every 15 minutes, every 3 hours, daily at 08:00.
func (p *pages) every(seconds int, dailyAt string) string {
	if dailyAt != "" {
		return p.t("every.daily_at", i18n.Args{"time": dailyAt})
	}
	if seconds == 86400 {
		return p.t("every.day")
	}
	return p.t("every.prefix", i18n.Args{"span": p.span(seconds)})
}

// span is an interval: 15 minutes, 3 hours, a day.
func (p *pages) span(seconds int) string {
	switch {
	case seconds >= 86400 && seconds%86400 == 0:
		return p.t("span.days", i18n.Args{"count": seconds / 86400})
	case seconds >= 3600 && seconds%3600 == 0:
		return p.t("span.hours", i18n.Args{"count": seconds / 3600})
	}
	return p.t("span.minutes", i18n.Args{"count": (seconds + 59) / 60})
}

func (p *pages) noun(kind string) string {
	if kind == "" {
		kind = wire.ConnectorSubscriptionKindSubUpdate
	}
	return p.t("noun." + kind)
}

func (p *pages) destinations(s subscriptions.Subscription) string {
	switch {
	case s.ToInbox && s.ToKB:
		return p.t("dest.both")
	case s.ToInbox:
		return p.t("dest.inbox")
	case s.ToKB:
		return p.t("dest.kb")
	}
	return p.t("dest.none")
}

// schedule is every() for a subscription.
func (p *pages) schedule(s subscriptions.Subscription) string {
	if subscriptions.IsPush(s) {
		return p.t("every.push")
	}
	if s.Recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
		at := s.DailyAt
		if at == "" && s.IntervalSeconds >= 86400 {
			at = "08:00"
		}
		if at != "" {
			return p.t("every.snapshot_daily", i18n.Args{"time": at})
		}
	}
	return p.every(s.IntervalSeconds, "")
}

// lastLine is what the last run did: 3 minutes ago, 2 new; or not run yet.
func (p *pages) lastLine(s subscriptions.Subscription) string {
	if s.LastOKAt == nil && subscriptions.IsPush(s) {
		return p.t("last.push_never")
	}
	if s.LastOKAt == nil {
		return p.t("last.never")
	}
	if s.Recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
		return p.t("last.snapshot", i18n.Args{"when": p.ago(s.LastOKAt)})
	}
	if s.LastNewCount == 0 {
		return p.t("last.none_new", i18n.Args{"when": p.ago(s.LastOKAt)})
	}
	return p.t("last.new", i18n.Args{"when": p.ago(s.LastOKAt), "count": s.LastNewCount})
}

// stateLine and stateBadge say how a subscription is doing, for its row in the list.
func (p *pages) stateLine(s subscriptions.Subscription) string {
	switch s.State {
	case subscriptions.StateFailing:
		return p.t("line.failing", i18n.Args{"problem": p.problemText(s.Error), "time": p.clock(s.NextRunAt)})
	case subscriptions.StateNeedsRecipe:
		return p.t("line.needs_recipe")
	case subscriptions.StateRateLimited:
		return p.t("line.rate_limited", i18n.Args{"dest": p.destinations(s), "every": p.schedule(s), "time": p.clock(s.PausedUntil)})
	case subscriptions.StateNotConnected:
		return p.t("line.not_connected")
	case subscriptions.StateOff:
		return p.t("line.off")
	}
	return p.t("line.ok", i18n.Args{"dest": p.destinations(s), "every": p.schedule(s), "last": p.lastLine(s)})
}

func (p *pages) stateBadge(state string) *wire.Badge {
	switch state {
	case subscriptions.StateFailing:
		return badge(p.t("badge.failing"), "danger")
	case subscriptions.StateNeedsRecipe:
		return badge(p.t("badge.needs_recipe"), "caution")
	case subscriptions.StateRateLimited:
		return badge(p.t("badge.rate_limited"), "caution")
	case subscriptions.StateNotConnected:
		return badge(p.t("badge.not_connected"), "neutral")
	}
	return nil
}

// toolTitle is a tool as people read it: its title, else its short name.
func toolTitle(src *subscriptions.CatalogSource, name string) string {
	if src != nil {
		for _, t := range src.Tools {
			if t.Name == name && t.Title != "" {
				return t.Title
			}
		}
	}
	return name
}

// plain says in one or two sentences what a recipe does: no model, the same words every time.
func (p *pages) plain(s subscriptions.Subscription, src *subscriptions.CatalogSource) string {
	args := i18n.Args{"source": s.Label, "tool": toolTitle(src, s.Recipe.Tool), "noun": p.noun(s.Recipe.Kind), "every": p.schedule(s)}
	var out string
	if s.Recipe.Mode == wire.ConnectorSubscriptionModeSnapshot {
		out = p.t("plain.snapshot", args)
	} else {
		out = p.t("plain.items", args)
	}
	if s.WeakDedup {
		out += " " + p.t("plain.weak_dedup")
	}
	return out
}

// originShort is who made the recipe, in a word or two for its row.
func (p *pages) originShort(origin string) string {
	switch origin {
	case subscriptions.OriginExtension, subscriptions.OriginModel:
		return p.t("origin_short." + origin)
	}
	return p.t("origin_short.user")
}

func (p *pages) origin(s subscriptions.Subscription) string {
	date := p.when(s.RecipeAt)
	switch s.RecipeOrigin {
	case subscriptions.OriginExtension:
		return p.t("origin.extension")
	case subscriptions.OriginModel:
		return p.t("origin.model", i18n.Args{"date": date})
	}
	return p.t("origin.user", i18n.Args{"date": date})
}

// fieldRows are a recipe's field mappings in a fixed order: what the Inbox shows, where it comes from.
func fieldRows(f *wire.ConnectorSubscriptionFields) [][2]string {
	if f == nil {
		return nil
	}
	var out [][2]string
	for _, kv := range [][2]string{{"title", f.Title}, {"sender", f.Sender}, {"time", f.Time}, {"body", f.Body},
		{"link", f.Link}, {"due_date", f.DueDate}, {"id", f.ID}, {"thread", f.Thread}} {
		if kv[1] != "" {
			out = append(out, kv)
		}
	}
	return out
}

// shortPath is a path as people read it: subject, from.name, items[0].
func shortPath(path string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	if s == "" {
		return "$"
	}
	return s
}

// took is how long a run took, "" for a push (kept as it came).
func (p *pages) took(ms int64, pushed bool) string {
	if pushed {
		return ""
	}
	return p.seconds(ms)
}

func (p *pages) seconds(ms int64) string {
	return p.t("run.seconds", i18n.Args{"s": strconv.FormatFloat(float64(ms)/1000, 'f', 1, 64)})
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
