package subscriptionsui

import (
	"cmp"
	"context"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// The card the user approves on the phone when the assistant changes a subscription (subscription_save in extmgr,
// docs/extensions/subscriptions.md "Assistant tools"): the change in plain words, before → after, in the words the
// pages use for the same things, and for a destination turned on the same consent its switch asks.

// Change is one change to approve: Before is the subscription as it is (nil for a new one), After as it will be.
// Source is its catalog source when it was read (tool titles); DryRun the check of a recipe that changed; Existing
// what a changed recipe does with what it finds at once; DeleteData, for a delete, that what it added goes too.
type Change struct {
	Action     string // create, update or delete
	Before     *subscriptions.Subscription
	After      subscriptions.Subscription
	Source     *subscriptions.CatalogSource
	Connector  string // the connector's name as people know it
	DryRun     *subscriptions.DryRun
	Existing   string
	DeleteData bool
}

// Card is the approval card's text for c in env's language.
func Card(ctx context.Context, svc Service, env Env, c Change) string {
	if env.Name == nil {
		env.Name = func(c string) string { return c }
	}
	p := &pages{ctx: ctx, svc: svc, env: env}
	var b strings.Builder
	line := func(s string) {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(s)
	}
	after := c.After
	switch c.Action {
	case "delete":
		line(p.t("card.delete", i18n.Args{"label": after.Label}))
		line("")
		line(p.t("card.delete_what", i18n.Args{"plain": p.plain(after, c.Source)}))
		if c.DeleteData {
			line(p.t("card.delete_data"))
		} else {
			line(p.t("card.delete_keep"))
		}
		return b.String()
	case "create":
		line(p.t("card.create", i18n.Args{"source": cmp.Or(after.SourceName, after.Label), "connector": cmp.Or(c.Connector, after.Connector)}))
		line("")
		if after.Label != "" && after.Label != after.SourceName {
			line(p.set("card.label", after.Label))
		}
		line(p.plain(after, c.Source)) // says how often
		line(p.set("dest.to_inbox", p.onOff(after.ToInbox)))
		line(p.set("dest.to_kb", p.onOff(after.ToKB)))
		if after.TriageRules != "" {
			line(p.set("source.rules", clipCard(after.TriageRules)))
		}
		if after.FallbackTier != "" {
			line(p.set("source.fallback", p.t("tier."+after.FallbackTier)))
		}
	default:
		before := after
		if c.Before != nil {
			before = *c.Before
		}
		line(p.t("card.update", i18n.Args{"label": before.Label}))
		line("")
		if before.Label != after.Label {
			line(p.row("card.label", before.Label, after.Label))
		}
		recipe := !subscriptions.SameRecipe(before.Recipe, after.Recipe)
		if recipe {
			bt, at := toolTitle(c.Source, before.Recipe.Tool), toolTitle(c.Source, after.Recipe.Tool)
			if bt == at {
				line(p.t("card.recipe_same", i18n.Args{"what": p.t("source.recipe"), "tool": at}))
			} else {
				line(p.row("source.recipe", p.t("card.uses", i18n.Args{"tool": bt}), p.t("card.uses", i18n.Args{"tool": at})))
			}
		}
		if p.schedule(before) != p.schedule(after) {
			line(p.row("source.interval", p.schedule(before), p.schedule(after)))
		}
		if before.ToInbox != after.ToInbox {
			line(p.row("dest.to_inbox", p.onOff(before.ToInbox), p.onOff(after.ToInbox)))
		}
		if before.ToKB != after.ToKB {
			line(p.row("dest.to_kb", p.onOff(before.ToKB), p.onOff(after.ToKB)))
		}
		if before.TriageRules != after.TriageRules {
			line(p.row("source.rules", p.rulesText(before.TriageRules), p.rulesText(after.TriageRules)))
		}
		if before.FallbackTier != after.FallbackTier {
			line(p.row("source.fallback", p.t("tier."+cmp.Or(before.FallbackTier, "default")), p.t("tier."+cmp.Or(after.FallbackTier, "default"))))
		}
		if recipe {
			line("")
			line(p.plain(after, c.Source))
			if c.DryRun != nil {
				line(p.t("card.found", i18n.Args{"count": c.DryRun.Count}))
			}
			// What the new recipe finds at once: a digest only touches the Inbox, the others what is on.
			switch existing := cmp.Or(c.Existing, subscriptions.ExistingDigest); {
			case existing == subscriptions.ExistingDigest && before.ToInbox && after.ToInbox,
				existing != subscriptions.ExistingDigest && (after.ToInbox || after.ToKB):
				line(p.t("card.existing." + existing))
			}
		}
	}
	// What turning a destination on does, as its switch on the pages asks it.
	had := func(on func(subscriptions.Subscription) bool) bool { return c.Before != nil && on(*c.Before) }
	for _, d := range []struct {
		name    string
		on, had bool
	}{
		{"to_inbox", after.ToInbox, had(func(s subscriptions.Subscription) bool { return s.ToInbox })},
		{"to_kb", after.ToKB, had(func(s subscriptions.Subscription) bool { return s.ToKB })},
	} {
		if !d.on || d.had {
			continue
		}
		consent := p.confirm(d.name, after.Label, after.Recipe.Kind, p.schedule(after), subscriptions.IsPush(after))
		if _, rest, ok := strings.Cut(consent, "\n\n"); ok {
			consent = rest // the switch's own question is this card's
		}
		line("")
		line(consent)
	}
	return b.String()
}

// row is one setting before → after; set one setting of a new subscription.
func (p *pages) row(key, before, after string) string {
	return p.t("card.row", i18n.Args{"what": p.t(key), "before": before, "after": after})
}

func (p *pages) set(key, value string) string {
	return p.t("card.set", i18n.Args{"what": p.t(key), "value": value})
}

func (p *pages) onOff(on bool) string {
	if on {
		return p.t("list.on")
	}
	return p.t("list.off")
}

func (p *pages) rulesText(r string) string {
	if strings.TrimSpace(r) == "" {
		return p.t("source.rules_default")
	}
	return clipCard(r)
}

// clipCard is a user's text as a card line shows it: one line, at most 120 characters.
func clipCard(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}
