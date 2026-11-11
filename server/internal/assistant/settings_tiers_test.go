package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// putTierProfiles stores two profiles (economy in use, unless active says otherwise) as a hub that has used them has.
func putTierProfiles(t *testing.T, s *Service, active string, economy, performance map[string]ModelTier) {
	t.Helper()
	ctx := context.Background()
	ps := modelProfiles{Revision: 1, Profiles: []ModelProfile{
		{ID: "economy", Name: "Economy", Tiers: economy},
		{ID: "performance", Name: "Performance", Tiers: performance},
	}}
	if err := s.store().PutSetting(ctx, modelProfilesKey, ps); err != nil {
		t.Fatal(err)
	}
	if err := s.store().PutSetting(ctx, modelProfileStateKey, modelProfileState{Active: active}); err != nil {
		t.Fatal(err)
	}
}

func storedTiers(t *testing.T, s *Service) (modelProfiles, modelProfileState) {
	t.Helper()
	ps, _, err := s.readModelProfiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.readModelProfileState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ps, st
}

func tierOf(ps modelProfiles, profile, tier string) (ModelTier, bool) {
	for _, p := range ps.Profiles {
		if p.ID == profile {
			t, ok := p.Tiers[tier]
			return t, ok
		}
	}
	return ModelTier{}, false
}

// A tier's model goes to the agent as --model and its description is in every snapshot: both are checked on save.
func TestCheckModelTiers(t *testing.T) {
	if checkTierModel("review", ModelTier{Launcher: "claude", Model: "x;rm"}) == nil || checkTierModel("review", ModelTier{Launcher: "claude", Model: "gw/model-x[1m]"}) != nil {
		t.Error("checkTierModel")
	}
	for name, tier := range map[string]ModelTier{
		"long description":     {Launcher: "claude", Description: strings.Repeat("长", modelTierAbout+1)},
		"two-line description": {Launcher: "claude", Description: "one\ntwo"},
	} {
		if err := checkModelTiers(map[string]ModelTier{"review": tier}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	tiers := map[string]ModelTier{"review": {Launcher: "claude", Model: "gw/model-x[1m]", Description: "  Code review  "}}
	if err := checkModelTiers(tiers); err != nil || tiers["review"].Description != "Code review" {
		t.Fatalf("a good tier: %v %+v", err, tiers)
	}
}

// awaitCard polls for the one card on the phone for up to 5 seconds; safe to call from a goroutine (it never stops the test).
func awaitCard(s *Service) (proto.Interaction, bool) {
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(5 * time.Second)
	for {
		if qs := s.pendingQuestions(); len(qs) == 1 {
			return qs[0], true
		}
		select {
		case <-tick.C:
		case <-deadline:
			return proto.Interaction{}, false
		}
	}
}

// persona_settings model_profiles.tiers: a set is checked before the card, shown on it for every profile it changes,
// and saved at the revision of the card once the user approves; a conflict saves nothing.
func TestModelTiersSetting(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast", Model: "m-fast"}}
	s := newTestService(t, f)
	putTierProfiles(t, s, "economy", map[string]ModelTier{"standard": {Launcher: "claude"}}, nil)
	tool := func(args string) (string, bool) {
		r := s.Tool(ctx, ToolCall{Name: "persona_settings", Arguments: json.RawMessage(args), MachineID: "server", SessionID: "c1"})
		return r.Content[0].Text, r.IsError
	}
	set := func(value string) (string, bool) {
		return tool(`{"action":"set","key":"model_profiles.tiers","value":` + value + `}`)
	}
	// answer approves (or not) the one card, after before() ran
	answer := func(allow bool, says []string, before func()) {
		go func() {
			q, ok := awaitCard(s)
			if !ok {
				t.Error("no card on the phone")
				return
			}
			for _, w := range says {
				if !strings.Contains(q.Summary, w) {
					t.Errorf("card %q lacks %q", q.Summary, w)
				}
			}
			if before != nil {
				before()
			}
			raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
			if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
				t.Error(err)
			}
		}()
	}

	if out, _ := tool(`{}`); !strings.Contains(out, "- model_profiles.tiers (") {
		t.Fatalf("list:\n%s", out)
	}
	// Refused before any card: a launcher no computer has, a bad model id, nothing to set.
	for _, bad := range []string{`{"tier":"review","launcher":"nope"}`, `{"tier":"review","launcher":"fast","model":"a b"}`,
		`{"tier":"review"}`, `{"tier":"Review","launcher":"fast"}`, `{"tier":"review","launcher":"fast","colour":"red"}`} {
		if out, isErr := set(bad); !isErr || len(s.pendingQuestions()) != 0 {
			t.Fatalf("%s: %s", bad, out)
		}
	}
	if out, _ := set(`{"tier":"review","launcher":"nope"}`); !strings.Contains(out, "fast") {
		t.Fatalf("an unknown launcher does not say which there are: %s", out)
	}

	// Added in every profile, with the card naming both.
	answer(true, []string{"Economy · review", "Performance · review", "fast · m-review · high — Code review"}, nil)
	out, isErr := set(`{"tier":"review","launcher":"fast","model":"m-review","effort":"high","description":"Code review"}`)
	if isErr || !strings.Contains(out, "economy, performance") {
		t.Fatalf("add: %s", out)
	}
	ps, st := storedTiers(t, s)
	want := ModelTier{Launcher: "fast", Model: "m-review", Effort: "high", Description: "Code review"}
	for _, id := range []string{"economy", "performance"} {
		if got, _ := tierOf(ps, id, "review"); got != want {
			t.Fatalf("%s review: %+v", id, got)
		}
	}
	// one event for the edit, naming each profile it changed
	if last := st.Events[len(st.Events)-1]; last.Action != "save" || last.By.Kind != "assistant" ||
		strings.Join(last.Changed, " ") != "economy.tiers.review performance.tiers.review" {
		t.Fatalf("history: %+v", st.Events)
	}
	if !strings.Contains(f.eventTypes(), "model_profile") {
		t.Fatalf("the apps were not told: %s", f.eventTypes())
	}

	// A field left out keeps the tier's own; a different launcher drops the old one's model and effort.
	answer(true, nil, nil)
	if out, isErr := set(`{"tier":"review","description":"Careful review","profiles":["economy"]}`); isErr {
		t.Fatalf("description: %s", out)
	}
	ps, _ = storedTiers(t, s)
	if got, _ := tierOf(ps, "economy", "review"); got != (ModelTier{Launcher: "fast", Model: "m-review", Effort: "high", Description: "Careful review"}) {
		t.Fatalf("partial edit: %+v", got)
	}
	if got, _ := tierOf(ps, "performance", "review"); got != want {
		t.Fatalf("profiles was not kept to: %+v", got)
	}
	answer(true, nil, nil)
	if out, isErr := set(`{"tier":"review","launcher":"claude","profiles":["performance"]}`); isErr {
		t.Fatalf("launcher: %s", out)
	}
	ps, _ = storedTiers(t, s)
	if got, _ := tierOf(ps, "performance", "review"); got != (ModelTier{Launcher: "claude", Description: "Code review"}) {
		t.Fatalf("a new launcher kept the old model: %+v", got)
	}
	if out, _ := set(`{"tier":"review","launcher":"claude","profiles":["performance"]}`); !strings.Contains(out, "Nothing to change") {
		t.Fatalf("no-op: %s", out)
	}
	// A new tier in a profile that lacks it needs its launcher.
	if out, isErr := set(`{"tier":"bulk","model":"m-fast"}`); !isErr || !strings.Contains(out, "give its launcher") {
		t.Fatalf("new without launcher: %s", out)
	}

	// The profiles changed while the card waited: nothing is saved.
	answer(true, nil, func() {
		if _, err := s.saveModelProfile(ctx, nil, "economy", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
			p.Description = "edited meanwhile"
			return nil
		}); err != nil {
			t.Error(err)
		}
	})
	if out, _ := set(`{"tier":"review","remove":true}`); !strings.Contains(out, "changed after the card") {
		t.Fatalf("conflict: %s", out)
	}
	ps, _ = storedTiers(t, s)
	if _, ok := tierOf(ps, "economy", "review"); !ok {
		t.Fatal("a stale card removed the tier")
	}
	// Removed, and declining changes nothing.
	answer(false, nil, nil)
	if out, _ := set(`{"tier":"review","remove":true}`); !strings.Contains(out, "declined") {
		t.Fatalf("declined: %s", out)
	}
	answer(true, []string{"Economy · review"}, nil)
	if out, isErr := set(`{"tier":"review","remove":true}`); isErr || !strings.Contains(out, "Removed") {
		t.Fatalf("remove: %s", out)
	}
	ps, _ = storedTiers(t, s)
	if _, ok := tierOf(ps, "performance", "review"); ok {
		t.Fatal("not removed")
	}
	if out, isErr := set(`{"tier":"review","remove":true}`); !isErr {
		t.Fatalf("removing what no profile has: %s", out)
	}
	if out, _ := tool(`{"action":"get","key":"model_profiles.tiers"}`); !strings.Contains(out, "- Economy (economy), in use:\n  - standard = claude") ||
		!strings.Contains(out, "- Performance (performance): no tiers") {
		t.Fatalf("get:\n%s", out)
	}
}

// agent_start resolves its tier before the card, which says what the agent runs on (Model Settings' Agents laid over a
// tier that leaves the model to it, as the start does); an unknown tier asks nothing.
func TestAgentStartCardShowsTheTier(t *testing.T) {
	f := healthHub()
	s := newTestService(t, f)
	setSettings(t, s, func(v *Settings) { v.MachineID, v.Main = "server", "main-0000-0000" })
	putTierProfiles(t, s, "economy", map[string]ModelTier{"review": {Launcher: "claude", Model: "gw/model-x", Effort: "high"}, "plain": {Launcher: "claude"}}, nil)
	if err := s.store().PutSetting(context.Background(), modelRecordKey("agent.new"), modelRecord{Revision: 1,
		Binding: ModelBinding{Provider: "launcher.claude", Model: "opus-x", Effort: "max"}}); err != nil {
		t.Fatal(err)
	}
	for tier, runs := range map[string]string{"review": "claude · gw/model-x · high", "plain": "claude · opus-x · max"} {
		says := i18n.T(s.lang(context.Background()), "assistant-tools.with_tier", i18n.Args{"tier": tier, "runs": runs})
		answerOutbound(t, s, "sleep-000-0000", "agent_start", says, false)
		out, _ := spaceTool(t, s, "sleep-000-0000", "agent_start", `{"machine":"server","cwd":"/home/me/src/app","prompt":"整理体检报告","tier":"`+tier+`"}`)
		if !strings.Contains(out, "declined") || len(f.starts) != 0 {
			t.Fatalf("agent_start %s: %s, starts %v", tier, out, f.starts)
		}
	}
	out, isErr := spaceTool(t, s, "sleep-000-0000", "agent_start", `{"machine":"server","cwd":"/home/me/src/app","prompt":"x","tier":"missing"}`)
	if !isErr || !strings.Contains(out, "no tier missing") || len(s.pendingQuestions()) != 0 || len(f.starts) != 0 {
		t.Fatalf("unknown tier: %s", out)
	}
}

// The New Agent form's presets: the profile in use's tiers this machine can start, for the Agents' picker only.
func TestPickerTiers(t *testing.T) {
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}, {Name: "fast", Model: "m-fast"}}
	s := newTestService(t, f)
	get := func(path string) map[string]json.RawMessage {
		t.Helper()
		w := request(s, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := get("/api/v1/machines/server/launchers"); out["tiers"] != nil {
		t.Fatalf("tiers with no profiles: %s", out["tiers"])
	}
	putTierProfiles(t, s, "economy", map[string]ModelTier{
		"review": {Launcher: "fast", Model: "m-review", Effort: "high", Description: "Code review"},
		"gone":   {Launcher: "deleted"}, // a launcher this machine does not have: never offered
		"quick":  {Launcher: "claude"},
	}, nil)
	out := get("/api/v1/machines/server/launchers")
	var tiers []map[string]string
	if err := json.Unmarshal(out["tiers"], &tiers); err != nil {
		t.Fatal(err)
	}
	if len(tiers) != 2 || tiers[0]["name"] != "quick" || tiers[1]["name"] != "review" || tiers[1]["launcher"] != "fast" ||
		tiers[1]["model"] != "m-review" || tiers[1]["effort"] != "high" || tiers[1]["description"] != "Code review" {
		t.Fatalf("tiers: %s", out["tiers"])
	}
	if string(out["tiers_profile"]) != `{"id":"economy","name":"Economy"}` {
		t.Fatalf("tiers_profile: %s", out["tiers_profile"])
	}
	// Hidden in Model providers, fast still runs (#4661): its tier stays among the presets, though fast leaves the list.
	if err := s.putSetting(context.Background(), launchersHiddenKey, []string{"fast"}); err != nil {
		t.Fatal(err)
	}
	if out := get("/api/v1/machines/server/launchers"); !strings.Contains(string(out["tiers"]), `"review"`) || strings.Contains(string(out["launchers"]), `"fast"`) {
		t.Fatalf("hidden fast: tiers %s launchers %s", out["tiers"], out["launchers"])
	}
	if err := s.putSetting(context.Background(), launchersHiddenKey, []string{}); err != nil {
		t.Fatal(err)
	}
	if out := get("/api/v1/machines/server/launchers?purpose=assistant"); out["tiers"] != nil {
		t.Fatal("the main chat's picker got the Agents' presets")
	}
	putTierProfiles(t, s, "", map[string]ModelTier{"quick": {Launcher: "claude"}}, nil)
	if out := get("/api/v1/machines/server/launchers"); out["tiers"] != nil {
		t.Fatal("presets with no profile in use")
	}
}

// Switching straight to the template made for it keeps the templates and marks it in use: a seed is never lost.
func TestApplySeededProfileKeepsTheSeed(t *testing.T) {
	s, _, _ := modelService(t)
	if _, err := s.applyModelProfile(context.Background(), seededProfile, ProfileActor{Kind: "hub"}); err != nil {
		t.Fatal(err)
	}
	ps, st := storedTiers(t, s)
	if len(ps.Profiles) != 2 || st.Active != seededProfile || st.Previous != nil {
		t.Fatalf("profiles %+v state %+v", ps, st)
	}
}

// Editing or deleting a profile tells the apps, as a switch does.
func TestProfileEditTellsTheApps(t *testing.T) {
	s, f, _ := modelService(t)
	ctx := context.Background()
	if _, err := s.saveModelProfile(ctx, nil, "custom", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
		p.Name = "Custom"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := f.eventTypes(); got != "model_profile" {
		t.Fatalf("save: %q", got)
	}
	if err := s.deleteModelProfile(ctx, nil, "custom", ProfileActor{Kind: "hub"}); err != nil {
		t.Fatal(err)
	}
	if got := f.eventTypes(); got != "model_profile model_profile" {
		t.Fatalf("delete: %q", got)
	}
}

// The snapshot's model profile line lists the Agent presets with what each runs on and is for.
func TestGoldenAssistantSnapshotTiers(t *testing.T) {
	golden(t, "assistant_snapshot.tiers", prompts.RenderSnapshot(prompts.Snapshot{Now: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Zone: "UTC",
		ModelProfile: &prompts.SnapshotProfile{ID: "performance", Name: "Performance", Tiers: []prompts.SnapshotTier{
			{Name: "review", Launcher: "gw", Model: "gw/model-x", Effort: "high", Description: "Code review and design"},
			{Name: "standard", Launcher: "claude"},
		}}}))
}

// A model saved before tiers were held to the model id rule blocks nothing: the templates leave it out, a switch and
// an edit of something else pass over it, and a tier edit checks only the tier it edits.
func TestLegacyTierModelBlocksNothing(t *testing.T) {
	s, _, st := modelService(t)
	ctx := context.Background()
	for _, id := range []string{"assistant", "agent.new"} {
		if err := st.PutSetting(ctx, modelRecordKey(id), modelRecord{Revision: 1, Binding: ModelBinding{Provider: "launcher.claude", Model: "old+model"}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range profilesOf(t, s).Profiles {
		if p.Tiers["strong"].Model != "" || p.Tiers["standard"].Model != "" {
			t.Fatalf("a template copied a model that is no model id: %+v", p.Tiers)
		}
	}
	old := map[string]ModelTier{"strong": {Launcher: "claude", Model: "old+model"}, "standard": {Launcher: "claude", Model: "old+model"}}
	storeProfiles(t, st, ModelProfile{ID: "economy", Name: "Economy", Tiers: old}, ModelProfile{ID: "performance", Name: "Performance"})
	if _, err := s.applyModelProfile(ctx, "economy", ProfileActor{Kind: "hub"}); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if _, err := s.saveModelProfile(ctx, nil, "economy", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
		p.Description = "edited"
		return nil
	}); err != nil {
		t.Fatalf("an edit of something else: %v", err)
	}
	if _, err := s.saveModelProfile(ctx, nil, "economy", ProfileActor{Kind: "hub"}, func(p *ModelProfile, _ bool) error {
		p.Tiers["bulk"] = ModelTier{Launcher: "claude", Model: "new+model"}
		return nil
	}); err == nil {
		t.Fatal("a new tier with a model that is no model id was saved")
	}
	ps, _ := storedTiers(t, s)
	good, bad := "good-model", "bad+model"
	if _, err := editTiers(&ps, tierEdit{Tier: "strong", Model: &good, Profiles: []string{"economy"}}, time.Now()); err != nil {
		t.Fatalf("fixing one tier stopped on the other: %v", err)
	}
	if _, err := editTiers(&ps, tierEdit{Tier: "standard", Model: &bad, Profiles: []string{"economy"}}, time.Now()); err == nil {
		t.Fatal("an edit to a model that is no model id was accepted")
	}
}

// Templates never saved are made again when the user approves: a configuration changed since the card is a conflict,
// not a save of something the card did not show.
func TestTierCardOnUnsavedTemplates(t *testing.T) {
	ctx := context.Background()
	f := newFakeHub().withMachine()
	f.launcherList = []proto.Launcher{{Name: "claude"}}
	s := newTestService(t, f)
	record := func(model string) {
		if err := s.store().PutSetting(ctx, modelRecordKey("agent.new"), modelRecord{Revision: 1, Binding: ModelBinding{Provider: "launcher.claude", Model: model}}); err != nil {
			t.Error(err)
		}
	}
	record("model-a")
	go func() {
		q, ok := awaitCard(s)
		if !ok {
			t.Error("no card on the phone")
			return
		}
		record("model-b")
		allow := true
		raw, _ := json.Marshal(proto.Resolution{Allow: &allow})
		if _, err := s.Action(ctx, Action{Op: "answer", InteractionID: q.ID, Answer: raw}); err != nil {
			t.Error(err)
		}
	}()
	r := s.Tool(ctx, ToolCall{Name: "persona_settings", MachineID: "server", SessionID: "c1",
		Arguments: json.RawMessage(`{"action":"set","key":"model_profiles.tiers","value":{"tier":"standard","description":"Everyday"}}`)})
	if out := r.Content[0].Text; !strings.Contains(out, "changed after the card") {
		t.Fatalf("approved over a changed configuration: %s", out)
	}
	if ps, _ := storedTiers(t, s); len(ps.Profiles) != 0 {
		t.Fatalf("saved: %+v", ps)
	}
}

// A rollback can put a profile deleted since back in use: the snapshot and a start on a tier say so.
func TestDeletedProfileInUse(t *testing.T) {
	s, _, _ := modelService(t)
	putTierProfiles(t, s, "gone", map[string]ModelTier{"quick": {Launcher: "claude"}}, nil)
	if _, err := s.tierModel(context.Background(), "quick"); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("tier: %v", err)
	}
	if got := s.modelProfileContext(context.Background()); got == nil || !got.Gone || got.ID != "gone" {
		t.Fatalf("snapshot: %+v", got)
	}
}
