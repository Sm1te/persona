package subscriptionsui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/subscriptions"
)

// Push makes the device's push source (#3848) in the fake, as the service does on a push without items.
func (f *fake) Push(_ context.Context, by subscriptions.Actor, _ subscriptions.PushRequest) (subscriptions.PushResult, error) {
	f.pushed = append(f.pushed, by)
	s := subscriptions.Subscription{ID: "sub_push_" + by.ID, Connector: subscriptions.PushConnector, Source: by.ID, SourceName: "Test iPhone",
		Label: "Shortcuts on Test iPhone", Icon: "bell", Recipe: subscriptions.Recipe{Mode: "items", Kind: "sub_notice", GroupBy: "thread"},
		RecipeOrigin: subscriptions.OriginPush, State: subscriptions.StateOff, Revision: 1}
	f.subs[s.ID] = s
	return subscriptions.PushResult{Subscription: s}, nil
}

func pushSub(f *fake, device string, on bool) subscriptions.Subscription {
	s := subscriptions.Subscription{ID: "sub_push", Connector: subscriptions.PushConnector, Source: device, SourceName: "Test iPhone",
		Label: "Shortcuts on Test iPhone", Icon: "bell", Recipe: subscriptions.Recipe{Mode: "items", Kind: "sub_notice", GroupBy: "thread"},
		RecipeOrigin: subscriptions.OriginPush, State: subscriptions.StateOff, Revision: 3}
	if on {
		s.ToInbox, s.State, s.LastOKAt, s.LastNewCount = true, subscriptions.StateOK, at(-5*time.Minute), 2
	}
	f.subs[s.ID] = s
	f.runs[s.ID] = []subscriptions.Run{{At: now.Add(-5 * time.Minute), Trigger: subscriptions.TriggerPush, Count: 2, New: 2}}
	return s
}

func TestAPushSourceHasNoRecipeIntervalOrPull(t *testing.T) {
	f := newFake()
	s := pushSub(f, "dev_phone", true)
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		v := draw(t, f, env(lang, true), state{P: pageSource, ID: s.ID})
		var got []string
		for _, sec := range v.Sections {
			got = append(got, sec.Key)
		}
		if strings.Join(got, ",") != "dest,status,how,act" {
			t.Fatalf("%s: sections %v", lang, got)
		}
		for _, k := range []string{"f.interval", "st.next", "h.recipe", "b.run"} {
			if item(v, k) != nil {
				t.Fatalf("%s: a push source shows %s", lang, k)
			}
		}
		if how := item(v, "h.push"); how == nil || !strings.Contains(how.Text, "Test iPhone") {
			t.Fatalf("%s: how it is sent: %+v", lang, how)
		}
	}
	en := draw(t, f, env(i18n.En, true), state{P: pageSource, ID: s.ID})
	kb := item(en, "d.to_kb")
	if kb == nil || kb.Control == nil || strings.Contains(kb.Control.Action.Confirm, "Pulled") || !strings.Contains(kb.Control.Action.Confirm, "shortcut") {
		t.Fatalf("the knowledge base's consent: %+v", kb)
	}
	if last := item(en, "st.last"); last == nil || last.Value != "5 minutes ago, 2 new" {
		t.Fatalf("last item: %+v", last)
	}
	runs := draw(t, f, env(i18n.En, true), state{P: pageRuns, ID: s.ID})
	if r := item(runs, "r.0"); r == nil || !strings.Contains(r.Title, "sent from Shortcuts") || strings.Contains(r.Subtitle, " s") {
		t.Fatalf("a push in the runs: %+v", r)
	}
	if sec := section(runs, "runs"); sec == nil || strings.Contains(sec.Footer, "rate-limited") || !strings.Contains(sec.Footer, "Shortcuts") {
		t.Fatalf("the runs' footer for a push source: %+v", sec)
	}
	// In the list it is a source like the others.
	list := draw(t, f, env(i18n.En, true), state{})
	if r := item(list, "s."+s.ID); r == nil || r.Icon != "bell" || !strings.Contains(r.Subtitle, "as a shortcut sends") {
		t.Fatalf("its row: %+v", r)
	}
}

func TestTheListOffersAnIPhoneItsOwnShortcutsSource(t *testing.T) {
	f := newFake()
	ios := env(i18n.En, true)
	ios.Platform = wire.PlatformIos
	v := draw(t, f, ios, state{})
	add := item(v, "push.add")
	if add == nil || add.Action == nil || add.Action.ID != "push_add" || section(v, "push").Footer == "" {
		t.Fatalf("no row adding this device's source: %+v", section(v, "push"))
	}
	res := handle(t, f, ios, state{}, "push_add", nil)
	if len(f.pushed) != 1 || f.pushed[0].ID != "dev_phone" || res.Toast == "" || res.View == nil || item(*res.View, "push.add") != nil {
		t.Fatalf("adding it: pushed %v, %+v", f.pushed, res)
	}
	// One extension's or connector's list has no such section; a device that may only look gets none either.
	if v := draw(t, f, ios, state{C: "apps"}); section(v, "push") != nil {
		t.Fatal("a connector's list offers Shortcuts")
	}
	look := env(i18n.En, false)
	look.Platform = wire.PlatformIos
	if v := draw(t, newFake(), look, state{}); section(v, "push") != nil {
		t.Fatal("a view-only device is offered to add a source")
	}
	// Elsewhere it only says how one comes, until there is one.
	web := env(i18n.En, true)
	web.Platform = wire.PlatformWeb
	if v := draw(t, newFake(), web, state{}); item(v, "push.hint") == nil || item(v, "push.add") != nil {
		t.Fatalf("the web: %+v", section(v, "push"))
	}
	g := newFake()
	pushSub(g, "dev_pad", false)
	if v := draw(t, g, web, state{}); section(v, "push") != nil {
		t.Fatal("the web still explains Shortcuts with a source in place")
	}
	if v := draw(t, g, ios, state{}); item(v, "push.add") == nil {
		t.Fatal("another device's source hides this one's")
	}
}
