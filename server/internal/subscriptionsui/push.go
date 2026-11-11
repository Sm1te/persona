package subscriptionsui

import (
	"cmp"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Push sources (#3848): a device fills its own from the iOS Shortcuts action Handle Notification. Nothing is pulled,
// so its page has no recipe, interval or Pull Now: its switches, how it is sent, its triage and runs, and Delete.

const pushIcon = "bell" // notifications; every UI version has it

// pushSource is a push source's page.
func (p *pages) pushSource(s subscriptions.Subscription) wire.View {
	v := wire.View{Title: s.Label, State: state{P: pageSource, ID: s.ID}.String()}
	v.Sections = append(v.Sections, p.switches(s.ID, s.Label, s.Recipe.Kind, p.schedule(s), s.ToInbox, s.ToKB, true, true))
	last := row("st.last", p.t("source.push_last"))
	last.Value = p.lastLine(s)
	runs := row("st.runs", p.t("source.runs"))
	runs.Action = open(state{P: pageRuns, ID: s.ID}, p.t("source.runs"))
	v.Sections = append(v.Sections, wire.Section{Key: "status", Title: p.t("source.status"), Items: []wire.Component{last, runs}})
	how := text("h.push", p.t("source.push_how", i18n.Args{"device": cmp.Or(s.SourceName, s.Label)}), "secondary")
	rules := row("h.rules", p.t("source.rules"))
	rules.Subtitle = cmp.Or(s.TriageRules, p.t("source.rules_default"))
	rules.Action = open(state{P: pageRules, ID: s.ID}, p.t("source.rules"))
	fb := row("h.fallback", p.t("source.fallback"))
	fb.Value = p.t("tier." + cmp.Or(s.FallbackTier, "default"))
	fb.Action = open(state{P: pageFallback, ID: s.ID}, p.t("source.fallback"))
	v.Sections = append(v.Sections, wire.Section{Key: "how", Title: p.t("source.push_section"), Items: []wire.Component{how, rules, fb}})
	del := button("b.delete", p.t("source.delete"), "delete")
	del.Role = "destructive"
	del.Action.Confirm = p.t("source.delete_confirm", i18n.Args{"source": s.Label})
	v.Sections = append(v.Sections, wire.Section{Key: "act", Items: []wire.Component{del}})
	return v
}

// pushSection is the list's Shortcuts section: on an iPhone or iPad without its own source yet, a row that adds it;
// elsewhere, while nobody has one, a line saying how one comes. None once this device (or, off iOS, any) has one.
func (p *pages) pushSection(subs []subscriptions.Subscription) (wire.Section, bool) {
	mine, any := false, false
	for _, s := range subs {
		if subscriptions.IsPush(s) {
			any = true
			mine = mine || s.Source == p.env.By.ID
		}
	}
	sec := wire.Section{Key: "push", Title: p.t("list.push_section")}
	switch {
	case p.env.Platform == wire.PlatformIos && p.env.CanEdit && !mine:
		r := row("push.add", p.t("list.push_add"))
		r.Subtitle, r.Icon, r.IconColor = p.t("list.push_add_detail"), p.icon(pushIcon), "orange"
		r.Action = &wire.Action{ID: "push_add"}
		sec.Items, sec.Footer = []wire.Component{r}, p.t("list.push_footer")
	case p.env.Platform != wire.PlatformIos && !any:
		sec.Items = []wire.Component{text("push.hint", p.t("list.push_hint"), "secondary")}
	default:
		return wire.Section{}, false
	}
	return sec, true
}
