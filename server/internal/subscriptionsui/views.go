package subscriptionsui

import (
	"cmp"
	"slices"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// cached is the cached catalog source a subscription or a page names: nil when the catalog has not been read yet.
func (p *pages) cached(connector, source string) (*subscriptions.Catalog, *subscriptions.CatalogSource) {
	cats, err := p.svc.Catalogs(p.ctx)
	if err != nil {
		return nil, nil
	}
	for i := range cats {
		if cats[i].Connector != connector {
			continue
		}
		for j := range cats[i].Sources {
			if cats[i].Sources[j].ID == source {
				return &cats[i], &cats[i].Sources[j]
			}
		}
		return &cats[i], nil
	}
	return nil, nil
}

func subscribable(src subscriptions.CatalogSource) int {
	n := 0
	for _, t := range src.Tools {
		if t.Subscribable {
			n++
		}
	}
	return n
}

func (p *pages) gone() wire.View {
	return wire.View{Title: p.t("title.list"), Sections: []wire.Section{{Items: []wire.Component{
		{Type: "empty", Key: "gone", Title: p.t("gone.title"), Detail: p.t("gone.detail"), Icon: p.icon("info")}}}}}
}

func (p *pages) failed(title string, err error) wire.View {
	return wire.View{Title: title, Sections: []wire.Section{{Items: []wire.Component{
		{Type: "empty", Key: "failed", Title: p.t("failed.title"), Detail: p.t("failed.detail"), Icon: "warning", IconColor: "red"}}}}}
}

// ① The list: what needs attention, what is on, what could be subscribed; for everything, one extension or one connector.
func (p *pages) list() wire.View {
	v := wire.View{Title: p.t("title.list")}
	subs, _, err := p.svc.List(p.ctx, subscriptions.Filter{Connector: p.st.C, Extension: p.st.Ext})
	if err != nil {
		return p.failed(v.Title, err)
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{text("intro", p.t("list.intro"), "secondary")}})
	var attention, on, off []wire.Component
	for _, s := range subs {
		r := row("s."+s.ID, s.Label)
		r.Icon, r.IconColor = p.icon(s.Icon), colorFor(s.Connector+"/"+s.Source)
		r.Subtitle, r.Badge = p.stateLine(s), p.stateBadge(s.State)
		r.Action = open(state{P: pageSource, ID: s.ID}, s.Label)
		switch s.State {
		case subscriptions.StateFailing, subscriptions.StateNeedsRecipe:
			attention = append(attention, r)
		case subscriptions.StateOff:
			off = append(off, r)
		default:
			on = append(on, r)
		}
	}
	add := func(key, title string, items []wire.Component) {
		if len(items) > 0 {
			v.Sections = append(v.Sections, wire.Section{Key: key, Title: title, Items: items})
		}
	}
	add("attention", p.t("list.attention"), attention)
	add("on", p.t("list.on"), on)
	add("off", p.t("list.off"), off)
	available := 0
	// This device's Shortcuts source (#3848) before the connectors' sources: it is the one source of its own.
	if p.st.C == "" && p.st.Ext == "" {
		if sec, ok := p.pushSection(subs); ok {
			v.Sections = append(v.Sections, sec)
			available++
		}
	}
	cats, _ := p.svc.Catalogs(p.ctx)
	slices.SortFunc(cats, func(a, b subscriptions.Catalog) int {
		return cmp.Compare(p.env.Name(a.Connector), p.env.Name(b.Connector))
	})
	for _, c := range cats {
		if p.st.C != "" && c.Connector != p.st.C || p.st.Ext != "" && c.Extension != p.st.Ext {
			continue
		}
		var items []wire.Component
		for _, src := range c.Sources {
			n := subscribable(src)
			if n == 0 || len(src.Subscriptions) > 0 {
				continue
			}
			r := row("a."+c.Connector+"/"+src.ID, src.Name)
			r.Icon, r.IconColor = p.icon(src.Icon), colorFor(c.Connector+"/"+src.ID)
			r.Subtitle = p.t("list.tools", i18n.Args{"count": n})
			if where := p.env.Native(src.Service); where != "" {
				r.Subtitle, r.Badge = p.t("list.native"), badge(p.t("badge.duplicate"), "caution")
			}
			r.Action = open(state{P: pageSource, C: c.Connector, S: src.ID}, src.Name)
			items = append(items, r)
		}
		available += len(items)
		add("avail."+c.Connector, p.t("list.available", i18n.Args{"name": p.env.Name(c.Connector)}), items)
	}
	if len(subs) == 0 && available == 0 {
		v.Sections = append(v.Sections, wire.Section{Key: "none", Items: []wire.Component{
			{Type: "empty", Key: "none", Title: p.t("list.none_title"), Detail: p.t("list.none_detail"), Icon: "bell"}}})
	} else if n := len(v.Sections); n > 0 {
		v.Sections[n-1].Footer = p.t("list.footer")
	}
	return v
}

// confirm is what a destination's switch asks before it turns on: what, how often, where it is kept, which model.
func (p *pages) confirm(dest, label, kind, every string, push bool) string {
	purpose := "inbox.triage" // Information filtering triages the Inbox's items
	if dest == "to_kb" {
		purpose = "kb" // Information processing summarizes the knowledge base's
	}
	provider := ""
	if m := p.modelOf(purpose); m.OK && m.Provider != "" {
		provider = p.t("confirm.provider", i18n.Args{"name": modelName(m)})
	}
	key := "confirm." + dest
	if push {
		key = "confirm.push_" + dest // nothing is pulled: what a shortcut sends is kept as it comes
	}
	return p.t(key, i18n.Args{"source": label, "noun": p.noun(kind), "every": every, "provider": provider})
}

func (p *pages) switches(id, label, kind, every string, inbox, kb, enabled, push bool) wire.Section {
	sw := func(name string, on bool, subtitle string) wire.Component {
		r := row("d."+name, p.t("dest."+name))
		r.Subtitle = subtitle
		act := wire.Action{ID: "dest", Values: map[string]string{"to": name}}
		if !on {
			act.Confirm = p.confirm(name, label, kind, every, push)
		}
		r.Control = &wire.RowControl{Kind: "switch", Name: name, On: on, Disabled: !enabled, Action: act}
		return r
	}
	return wire.Section{Key: "dest", Title: p.t("source.dest"), Items: []wire.Component{
		sw("to_inbox", inbox, p.t("source.inbox_detail", i18n.Args{"noun": p.noun(kind)})),
		sw("to_kb", kb, p.t("source.kb_detail")),
	}}
}

// ② A source: its switches, how often, how it is doing, how it pulls and triages, and pull now / delete.
func (p *pages) source() wire.View {
	if p.st.ID == "" {
		return p.newSource()
	}
	s, _, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return p.gone()
	}
	if subscriptions.IsPush(s) {
		return p.pushSource(s)
	}
	_, src := p.cached(s.Connector, s.Source)
	v := wire.View{Title: s.Label, State: state{P: pageSource, ID: s.ID}.String()}
	if where := p.env.Native(s.Service); where != "" {
		v.Sections = append(v.Sections, wire.Section{Key: "native", Items: []wire.Component{
			text("native", p.t("source.native", i18n.Args{"where": where}), "secondary")}})
	}
	switch s.State {
	case subscriptions.StateFailing, subscriptions.StateNeedsRecipe, subscriptions.StateRateLimited, subscriptions.StateNotConnected:
		v.Sections = append(v.Sections, p.trouble(s))
	}
	v.Sections = append(v.Sections, p.switches(s.ID, s.Label, s.Recipe.Kind, p.schedule(s), s.ToInbox, s.ToKB, true, false))
	freq := row("f.interval", p.t("source.interval"))
	freq.Value = p.schedule(s)
	freq.Action = open(state{P: pageInterval, ID: s.ID}, p.t("source.interval"))
	freqSec := wire.Section{Key: "freq", Title: p.t("source.freq"), Items: []wire.Component{freq}}
	if s.MinIntervalSeconds > 300 {
		freqSec.Footer = p.t("source.min_interval", i18n.Args{"span": p.span(s.MinIntervalSeconds)})
	}
	v.Sections = append(v.Sections, freqSec)
	if s.State == subscriptions.StateOK {
		last, next := row("st.last", p.t("source.last")), row("st.next", p.t("source.next"))
		last.Value, next.Value = p.lastLine(s), p.soon(s.NextRunAt)
		runs := row("st.runs", p.t("source.runs"))
		runs.Action = open(state{P: pageRuns, ID: s.ID}, p.t("source.runs"))
		v.Sections = append(v.Sections, wire.Section{Key: "status", Title: p.t("source.status"), Items: []wire.Component{last, next, runs}})
	}
	recipe := row("h.recipe", p.t("source.recipe"))
	recipe.Subtitle = p.t("source.recipe_line", i18n.Args{"tool": toolTitle(src, s.Recipe.Tool), "origin": p.originShort(s.RecipeOrigin)})
	recipe.Action = open(state{P: pageRecipe, ID: s.ID}, p.t("source.recipe"))
	rules := row("h.rules", p.t("source.rules"))
	rules.Subtitle = s.TriageRules
	if rules.Subtitle == "" {
		rules.Subtitle = p.t("source.rules_default")
	}
	rules.Action = open(state{P: pageRules, ID: s.ID}, p.t("source.rules"))
	fb := row("h.fallback", p.t("source.fallback"))
	fb.Value = p.t("tier." + cmp.Or(s.FallbackTier, "default"))
	fb.Action = open(state{P: pageFallback, ID: s.ID}, p.t("source.fallback"))
	v.Sections = append(v.Sections, wire.Section{Key: "how", Title: p.t("source.how"), Items: []wire.Component{recipe, rules, fb}})
	del := button("b.delete", p.t("source.delete"), "delete")
	del.Role = "destructive"
	del.Action.Confirm = p.t("source.delete_confirm", i18n.Args{"source": s.Label})
	v.Sections = append(v.Sections, wire.Section{Key: "act", Items: []wire.Component{button("b.run", p.t("source.run_now"), "run_now"), del}})
	return v
}

// trouble is the section at the top of a source that is not pulling: what is wrong and when it tries again.
func (p *pages) trouble(s subscriptions.Subscription) wire.Section {
	sec := wire.Section{Key: "trouble"}
	r := row("e.now", "")
	r.Badge = p.stateBadge(s.State)
	var why string
	switch s.State {
	case subscriptions.StateFailing:
		sec.Title = p.t("trouble.failing_title")
		r.Title = p.t("trouble.failing", i18n.Args{"since": p.clock(s.FailingSince)})
		r.Subtitle = p.t("trouble.failing_line", i18n.Args{"problem": p.problemText(s.Error), "count": s.FailCount})
		r.Icon, r.IconColor = "warning", "red"
		why = p.t("trouble.failing_why", i18n.Args{"time": p.clock(s.NextRunAt)})
	case subscriptions.StateNeedsRecipe:
		sec.Title = p.t("trouble.paused_title")
		r.Title = p.t("trouble.needs_recipe")
		r.Subtitle = p.problemText(s.Error)
		r.Icon, r.IconColor = "warning", "orange"
		r.Action = open(state{P: pageRecipe, ID: s.ID}, p.t("source.recipe"))
		why = p.t("trouble.needs_recipe_why")
	case subscriptions.StateRateLimited:
		sec.Title = p.t("trouble.paused_title")
		r.Title = p.t("trouble.rate_limited", i18n.Args{"time": p.clock(s.PausedUntil)})
		r.Icon, r.IconColor = "clock", "orange"
		why = p.t("trouble.rate_limited_why")
	case subscriptions.StateNotConnected:
		sec.Title = p.t("trouble.paused_title")
		r.Title = p.t("trouble.not_connected")
		r.Icon, r.IconColor = "link", "gray"
		why = p.t("trouble.not_connected_why")
	}
	sec.Items = []wire.Component{r, text("e.why", why, "caption")}
	runs := row("e.runs", p.t("source.runs"))
	runs.Action = open(state{P: pageRuns, ID: s.ID}, p.t("source.runs"))
	sec.Items = append(sec.Items, runs)
	return sec
}

// ②b A source nobody subscribed to yet: its own recipe if the extension brings one, else pick a tool first.
func (p *pages) newSource() wire.View {
	cat, err := p.svc.Catalog(p.ctx, p.st.C)
	if err != nil {
		return p.failed(p.t("title.list"), err)
	}
	var src *subscriptions.CatalogSource
	for i := range cat.Sources {
		if cat.Sources[i].ID == p.st.S {
			src = &cat.Sources[i]
		}
	}
	if src == nil {
		return p.gone()
	}
	v := wire.View{Title: src.Name}
	if where := p.env.Native(src.Service); where != "" {
		v.Sections = append(v.Sections, wire.Section{Key: "native", Items: []wire.Component{
			text("native", p.t("source.native", i18n.Args{"where": where}), "secondary")}})
	}
	if cat.Direct.Inbox || cat.Direct.Archive {
		v.Sections = append(v.Sections, wire.Section{Key: "direct", Items: []wire.Component{text("direct", p.t("source.direct"), "secondary")}})
	}
	min := 300
	if src.MinIntervalSeconds != nil && *src.MinIntervalSeconds > min {
		min = *src.MinIntervalSeconds
	}
	every := p.every(max(900, min), "")
	kind := ""
	how := wire.Section{Key: "how", Title: p.t("source.recipe")}
	if len(src.Recipes) > 0 {
		r := src.Recipes[0]
		kind = r.Kind
		if r.Mode == wire.ConnectorSubscriptionModeSnapshot {
			every = p.t("every.snapshot_daily", i18n.Args{"time": "08:00"})
		}
		own := row("h.own", p.t("new.own"))
		own.Subtitle = p.t("source.recipe_line", i18n.Args{"tool": toolTitle(src, r.Tool), "origin": p.originShort(subscriptions.OriginExtension)})
		own.Icon, own.IconColor = "check", "green"
		other := row("h.pick", p.t("new.other"))
		other.Action = open(state{P: pageTools, C: p.st.C, S: p.st.S}, p.t("title.tools"))
		how.Items = []wire.Component{own, other}
	} else {
		how.Items = p.draftItems(&v, sourceKey(p.st.C, p.st.S), state{P: pageSetup, C: p.st.C, S: p.st.S}, false)
		pick := row("h.pick", p.t("new.pick"))
		pick.Subtitle = p.t("list.tools", i18n.Args{"count": subscribable(*src)})
		pick.Icon, pick.IconColor = "search", "blue"
		pick.Action = open(state{P: pageTools, C: p.st.C, S: p.st.S}, p.t("title.tools"))
		how.Items = append(how.Items, pick)
		how.Footer = p.t("new.pick_footer")
	}
	v.Sections = append(v.Sections, how, p.switches("", src.Name, kind, every, false, false, len(src.Recipes) > 0, false))
	freq := row("f.interval", p.t("source.interval"))
	freq.Value = every
	v.Sections = append(v.Sections, wire.Section{Key: "freq", Title: p.t("source.freq"), Items: []wire.Component{freq},
		Footer: p.t("new.freq_footer")})
	return v
}

// ③ A recipe: what it does in words, its fields; a dry run on request, with its items and checks.
func (p *pages) recipe(dry *subscriptions.DryRun, dryErr error) wire.View {
	s, _, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return p.gone()
	}
	_, src := p.cached(s.Connector, s.Source)
	v := wire.View{Title: p.t("source.recipe"), State: state{P: pageRecipe, ID: s.ID}.String()}
	if job, ok := p.env.Drafts.get(subKey(s.ID)); ok && !job.running && job.err == nil && dry == nil && dryErr == nil {
		dry = job.draftDryRun() // just regenerated: what its check pulled
	}
	v.Sections = append(v.Sections, wire.Section{Key: "what", Title: p.t("recipe.what"), Items: []wire.Component{
		text("plain", p.plain(s, src), "body")}})
	examples := map[string]string{}
	if dry != nil && len(dry.Items) > 0 {
		it := dry.Items[0]
		for k, val := range map[string]string{"title": it.Title, "sender": it.Sender, "link": it.Link, "body": it.Body,
			"thread": it.Thread, "id": it.UID} {
			examples[k] = val
		}
		if it.Time != nil {
			examples["time"] = *it.Time
		}
		if it.DueDate != nil {
			examples["due_date"] = *it.DueDate
		}
	}
	cols := []wire.TableColumn{{Title: p.t("recipe.col_shown")}, {Title: p.t("recipe.col_from")}}
	if len(examples) > 0 {
		cols = append(cols, wire.TableColumn{Title: p.t("recipe.col_example")})
	}
	var rows [][]string
	for _, kv := range fieldRows(s.Recipe.Fields) {
		r := []string{p.t("field." + kv[0]), shortPath(kv[1])}
		if len(examples) > 0 {
			r = append(r, examples[kv[0]])
		}
		rows = append(rows, r)
	}
	if len(rows) > 0 {
		title := p.t("recipe.fields")
		if len(examples) > 0 {
			title = p.t("recipe.fields_example")
		}
		v.Sections = append(v.Sections, wire.Section{Key: "map", Title: title, Items: []wire.Component{
			{Type: "table", Key: "fields", Columns: cols, Rows: rows}}})
	}
	switch {
	case dryErr != nil:
		v.Sections = append(v.Sections, wire.Section{Key: "dry", Title: p.t("dry.title"), Items: []wire.Component{
			text("dry.err", p.t("dry.failed"), "secondary")}})
	case dry != nil:
		v.Sections = append(v.Sections, p.dryItems(dry)...)
	}
	if items := p.draftItems(&v, subKey(s.ID), state{P: pageSetup, ID: s.ID, C: s.Connector, S: s.Source}, true); len(items) > 0 {
		v.Sections = append(v.Sections, wire.Section{Key: "draft", Title: p.t("draft.title"), Items: items})
	}
	try := button("b.dry", p.t("recipe.dry_run"), "dry_run")
	edit := row("x.tool", p.t("recipe.edit"))
	edit.Action = open(state{P: pageSetup, ID: s.ID, C: s.Connector, S: s.Source, T: s.Recipe.Tool}, p.t("recipe.edit_title"))
	other := row("x.tools", p.t("recipe.other_tool"))
	other.Action = open(state{P: pageTools, ID: s.ID, C: s.Connector, S: s.Source}, p.t("title.tools"))
	raw := row("x.raw", p.t("recipe.raw"))
	raw.Action = open(state{P: pageRaw, ID: s.ID}, p.t("recipe.raw"))
	v.Sections = append(v.Sections, wire.Section{Key: "edit", Footer: p.origin(s) + " " + p.t("recipe.no_model"),
		Items: []wire.Component{try, edit, other, raw}})
	return v
}

// dryItems are a dry run's items and its checks.
func (p *pages) dryItems(dry *subscriptions.DryRun) []wire.Section {
	var out []wire.Section
	items := wire.Section{Key: "dry", Title: p.t("dry.title")}
	if dry.Error != nil {
		items.Items = append(items.Items, text("dry.err", p.t("dry.error", i18n.Args{"problem": p.problemText(dry.Error)}), "secondary"))
	}
	for i, it := range dry.Items {
		r := row("dry."+itoa(i), it.Title)
		var bits []string
		if it.Sender != "" {
			bits = append(bits, it.Sender)
		}
		if it.Time != nil {
			bits = append(bits, *it.Time)
		}
		r.Subtitle = join(bits)
		if it.New {
			r.Badge = badge(p.t("dry.new"), "positive")
		} else {
			r.Badge = badge(p.t("dry.seen"), "neutral")
		}
		items.Items = append(items.Items, r)
	}
	if dry.Shape != nil {
		shape := p.t("dry.shape_text")
		if dry.Shape.Type == "json" {
			shape = p.t("dry.shape_json", i18n.Args{"count": dry.Count})
		}
		if dry.MaybeMore {
			shape += " " + p.t("dry.maybe_more")
		}
		items.Items = append(items.Items, text("dry.shape", shape, "caption"))
	}
	if len(items.Items) > 0 {
		out = append(out, items)
	}
	if len(dry.Checks) > 0 {
		checks := wire.Section{Key: "checks", Title: p.t("checks.title")}
		for _, c := range dry.Checks {
			r := row("c."+c.Key, p.t("check."+c.Key))
			if c.OK {
				r.Icon, r.IconColor = "check", "green"
			} else {
				r.Icon, r.IconColor = "warning", "red"
				r.Subtitle = p.problemText(c.Error)
			}
			checks.Items = append(checks.Items, r)
		}
		out = append(out, checks)
	}
	return out
}

// ④ Pick a tool: the ones that can be subscribed, then the ones that can't and why.
func (p *pages) tools() wire.View {
	v := wire.View{Title: p.t("title.tools")}
	cat, err := p.svc.Catalog(p.ctx, p.st.C)
	if err != nil {
		return p.failed(v.Title, err)
	}
	var src *subscriptions.CatalogSource
	for i := range cat.Sources {
		if cat.Sources[i].ID == p.st.S {
			src = &cat.Sources[i]
		}
	}
	if src == nil {
		return p.gone()
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{text("intro", p.t("tools.intro"), "secondary")}})
	ok := wire.Section{Key: "ok", Title: p.t("tools.ok")}
	no := wire.Section{Key: "no", Title: p.t("tools.no"), Footer: p.t("tools.no_footer")}
	for _, t := range src.Tools {
		r := row("t."+t.Name, cmp.Or(t.Title, t.Name))
		if t.Subscribable {
			r.Subtitle = join([]string{t.Name, t.Description})
			r.Action = open(state{P: pageSetup, ID: p.st.ID, C: p.st.C, S: p.st.S, T: t.Name}, r.Title)
			ok.Items = append(ok.Items, r)
		} else {
			r.Subtitle = join([]string{t.Name, p.problemText(t.Refused)})
			no.Items = append(no.Items, r)
		}
	}
	if len(ok.Items) > 0 {
		v.Sections = append(v.Sections, ok)
	}
	if len(no.Items) > 0 {
		v.Sections = append(v.Sections, no)
	}
	return v
}

// ⑥ Runs: the last twenty, newest first.
func (p *pages) runs() wire.View {
	v := wire.View{Title: p.t("source.runs")}
	s, runs, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return p.gone()
	}
	if len(runs) == 0 {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: "empty", Key: "none", Title: p.t("runs.none"),
			Detail: p.t("runs.none_detail"), Icon: "clock"}}}}
		return v
	}
	okN, failN, newN := 0, 0, 0
	list := wire.Section{Key: "runs", Title: p.t("runs.each"), Footer: p.t("runs.footer")}
	if subscriptions.IsPush(s) {
		list.Footer = p.t("runs.push_footer") // nothing is pulled: no rounds skipped, no rate limit
	}
	for i, r := range runs {
		c := row("r."+itoa(i), p.t("runs.when", i18n.Args{"time": p.clock(&r.At), "trigger": p.t("trigger." + r.Trigger)}))
		pushed := r.Trigger == subscriptions.TriggerPush // kept as it came: no call to time
		if r.Error != nil {
			failN++
			c.Subtitle = join([]string{p.problemText(r.Error), p.took(r.MS, pushed)})
			c.Badge = badge(p.t("badge.failing"), "danger")
		} else {
			okN++
			newN += r.New
			if r.New > 0 {
				c.Subtitle = join([]string{p.t("runs.new", i18n.Args{"count": r.Count, "new": r.New}), p.took(r.MS, pushed)})
				c.Badge = badge("+"+itoa(r.New), "positive")
			} else {
				c.Subtitle = join([]string{p.t("runs.none_new", i18n.Args{"count": r.Count}), p.took(r.MS, pushed)})
			}
		}
		list.Items = append(list.Items, c)
	}
	stats := []wire.Stat{{Label: p.t("runs.ok"), Value: itoa(okN)}, {Label: p.t("runs.new_items"), Value: itoa(newN)},
		{Label: p.t("runs.failed"), Value: itoa(failN)}}
	if failN > 0 {
		stats[2].Tone = "danger"
	}
	v.Sections = []wire.Section{{Key: "sum", Title: p.t("runs.summary", i18n.Args{"count": len(runs)}),
		Items: []wire.Component{{Type: "stat_grid", Key: "stats", Stats: stats}}}, list}
	return v
}

// intervals offered, in seconds; snapshots also offer a time of day.
var (
	intervals = []int{300, 600, 900, 1800, 3600, 10800, 21600, 43200, 86400}
	dailyAts  = []string{"06:00", "08:00", "12:00", "18:00", "21:00"}
)

// ⑦ How often: pick one and it applies at once (the page stays, the choice moves).
func (p *pages) interval() wire.View {
	s, _, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return p.gone()
	}
	v := wire.View{Title: p.t("source.interval")}
	sec := wire.Section{Key: "pick"}
	chosen := func(r *wire.Component, on bool) {
		if on {
			r.Badge = badge(p.t("pick.current"), "positive")
		}
	}
	snapshot := s.Recipe.Mode == wire.ConnectorSubscriptionModeSnapshot
	if snapshot {
		for _, at := range dailyAts {
			r := row("i.at."+at, p.t("every.daily_at", i18n.Args{"time": at}))
			r.Action = &wire.Action{ID: "interval", Values: map[string]string{"daily_at": at}}
			chosen(&r, s.DailyAt == at)
			sec.Items = append(sec.Items, r)
		}
	}
	for _, sec2 := range intervals {
		if sec2 < s.MinIntervalSeconds || snapshot && sec2 < 3600 {
			continue
		}
		r := row("i."+itoa(sec2), p.every(sec2, ""))
		if sec2 == max(900, s.MinIntervalSeconds) && !snapshot {
			r.Subtitle = p.t("pick.default")
		}
		r.Action = &wire.Action{ID: "interval", Values: map[string]string{"seconds": itoa(sec2)}}
		chosen(&r, s.DailyAt == "" && s.IntervalSeconds == sec2)
		sec.Items = append(sec.Items, r)
	}
	sec.Footer = p.t("pick.interval_footer", i18n.Args{"span": p.span(max(300, s.MinIntervalSeconds))})
	v.Sections = []wire.Section{sec}
	return v
}

// ⑧ Without a triage model: where new items go.
func (p *pages) fallback() wire.View {
	s, _, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return p.gone()
	}
	sec := wire.Section{Key: "pick", Footer: p.t("pick.fallback_footer")}
	for _, tier := range []string{"", "fyi", "action", "wait"} {
		r := row("t."+cmp.Or(tier, "default"), p.t("tier."+cmp.Or(tier, "default")))
		r.Subtitle = p.t("tier_detail." + cmp.Or(tier, "default"))
		r.Action = &wire.Action{ID: "fallback", Values: map[string]string{"tier": tier}}
		if s.FallbackTier == tier {
			r.Badge = badge(p.t("pick.current"), "positive")
		}
		sec.Items = append(sec.Items, r)
	}
	return wire.View{Title: p.t("source.fallback"), Sections: []wire.Section{sec}}
}

// ⑨ Triage rules: free text, this subscription only.
func (p *pages) rules() wire.View {
	s, _, err := p.svc.Get(p.ctx, p.st.ID)
	if err != nil {
		return p.gone()
	}
	field := wire.Component{Type: "field", Key: "r.text", Name: "triage_rules", Label: p.t("rules.label"), Kind: "multiline",
		Value: s.TriageRules, Placeholder: p.t("rules.placeholder")}
	save := button("b.save", p.t("rules.save"), "rules")
	save.Action.Submit = true
	return wire.View{Title: p.t("source.rules"), Sections: []wire.Section{
		{Key: "rules", Footer: p.t("rules.footer"), Items: []wire.Component{field}},
		{Key: "save", Items: []wire.Component{save}},
	}}
}

// modelName is a model as the consents name it: its connection, and the model when the connection has several.
func modelName(m subscriptions.ModelInfo) string {
	if m.Model == "" || m.Model == m.Provider {
		return m.Provider
	}
	return m.Provider + " · " + m.Model
}

// draftItems are the rows of a draft by the model (#2040) for key: the press that starts one (asking first what goes to
// which model), one running (the page draws again when it is done), or what it came to. setup is the setup page a draft
// opens in; regenerate: a subscription's, whose draft is saved when it passes. Nothing without the model stage.
func (p *pages) draftItems(v *wire.View, key string, setup state, regenerate bool) []wire.Component {
	if !p.canDraft() {
		return nil
	}
	job, ok := p.env.Drafts.get(key)
	switch {
	case ok && job.running:
		v.RefreshAfter = 30 // the manager redraws the pages when it is done; this is the fallback
		r := row("g.running", p.t("draft.running"))
		r.Subtitle, r.Icon, r.IconColor = p.t("draft.running_line"), "clock", "blue"
		return []wire.Component{r}
	case ok && job.err == nil:
		note := row("g.done", p.t("draft.done"))
		if regenerate {
			note.Title = p.t("draft.regenerated")
		}
		note.Icon, note.IconColor = "check", "green"
		items := []wire.Component{note}
		if job.result.Note != "" { // the model's own words on what it chose: whole, not cut to a subtitle's line
			items = append(items, text("g.note", job.result.Note, "secondary"))
		}
		if !regenerate {
			review := row("g.review", p.t("draft.review"))
			st := setup
			st.T, st.G = job.result.Recipe.Tool, true
			review.Action = open(st, p.t("title.setup"))
			items = append(items, review)
		}
		return items
	case ok:
		failed := row("g.failed", p.t("draft.failed"))
		failed.Icon, failed.IconColor = "warning", "orange"
		if se, isProblem := job.err.(*subscriptions.Error); isProblem {
			failed.Subtitle = p.problemText(&subscriptions.Problem{Code: se.Code})
		} else {
			failed.Subtitle = p.t("problem.other")
		}
		items := []wire.Component{failed}
		if d := job.draftRecipe(); d != nil && d.Tool != "" {
			goOn := row("g.continue", p.t("draft.continue"))
			st := setup
			st.T, st.G = d.Tool, true
			goOn.Action = open(st, p.t("title.setup"))
			items = append(items, goOn)
		}
		return append(items, p.draftPress(regenerate, "draft.again"))
	}
	return []wire.Component{p.draftPress(regenerate, map[bool]string{false: "draft.start", true: "draft.regenerate"}[regenerate])}
}

// draftPress starts a draft, after saying what goes to which model; without a model it only says so.
func (p *pages) draftPress(regenerate bool, title string) wire.Component {
	r := row("g.draft", p.t(title))
	r.Icon, r.IconColor = p.icon("robot"), "purple"
	m := p.modelOf("recipe")
	if !m.OK {
		r.Subtitle = p.t("draft.no_model")
		return r
	}
	r.Subtitle = p.t("draft.start_line")
	id := "draft"
	if regenerate {
		id = "regenerate"
	}
	r.Action = &wire.Action{ID: id, Confirm: p.t("draft.consent", i18n.Args{"provider": modelName(m)})}
	return r
}
