package subscriptions

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// What a changed recipe finds at once (#5240): all of it is new to the dedup keys, so without care a recipe change
// pours it into the Inbox one row each. existing says what happens: digest (the default), baseline or deliver.

// changedRecipe is threadRecipe pulling more a page: a different recipe for the same notices.
func changedRecipe() Recipe {
	r := threadRecipe()
	r.Args = json.RawMessage(`{"since":"{{since}}","limit":40}`)
	return r
}

func manyNotes(prefix string, n int) []note {
	var ns []note
	for i := range n {
		ns = append(ns, note{id: fmt.Sprint(prefix, i), title: fmt.Sprint("Mail ", i), at: fmt.Sprintf("2026-11-10T08:%02d:00Z", i)})
	}
	return ns
}

// started is a subscription with its Inbox on that pulled two notices, which came in as rows.
func (e *env) started() Subscription {
	e.t.Helper()
	e.answer(notesJSON(note{id: "a1", title: "Old one", at: "2026-11-10T08:00:00Z"}, note{id: "a2", title: "Old two", at: "2026-11-10T08:10:00Z"}))
	sub := e.turnOn(e.create(threadRecipe()))
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 2 {
		e.t.Fatalf("the first catch-up: %+v", rows)
	}
	e.clock.advance(20 * time.Minute)
	return e.get(sub.ID)
}

func TestAChangedRecipeDigestsWhatItFindsAtOnceByDefault(t *testing.T) {
	e := newEnv(t)
	sub := e.started()
	e.answer(notesJSON(manyNotes("m", 10)...))
	changed := e.patch(sub.ID, Patch{Recipe: ptr(changedRecipe())})
	at := e.clock.Now()
	e.svc.RunDue(e.ctx)
	rows := e.rows(sub.ID)
	if len(rows) != 1 {
		t.Fatalf("10 items a new recipe found at once: %d rows, want one digest: %+v", len(rows), rows)
	}
	d := rows[0]
	if d.Thread != "sub:"+sub.ID+":catchup-"+strconv.FormatInt(at.Unix(), 10) || d.Kind != "sub_digest" ||
		d.Title != "Notices: 10 items found when its recipe changed" || strings.Count(d.Context, "\n") != 9 {
		t.Fatalf("the digest of a changed recipe: %+v", d)
	}
	if got := e.get(sub.ID); got.CatchupFrom == nil || !got.CatchupFrom.Equal(at) || got.Revision != changed.Revision {
		t.Fatalf("its mark: %+v", got.CatchupFrom)
	}
	// Later items come in as rows beside it.
	e.answer(notesJSON(append(manyNotes("m", 10), note{id: "m99", title: "A new one", at: "2026-11-10T09:30:00Z"})...))
	e.clock.advance(20 * time.Minute)
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 2 || rowOf(t, rows, "sub:"+sub.ID+":m99").Title != "A new one" || rowOf(t, rows, d.Thread).Last != d.Last {
		t.Fatalf("after a later item: %+v", rows)
	}
	// Turning the Inbox off and on again catches up on the day before, as always.
	e.patch(sub.ID, Patch{ToInbox: ptr(false)})
	e.patch(sub.ID, Patch{ToInbox: ptr(true)})
	if got := e.get(sub.ID); got.CatchupFrom != nil {
		t.Fatalf("the recipe's catch-up mark outlived the Inbox turned off: %v", got.CatchupFrom)
	}
}

func TestABaselineRecordsWhatAChangedRecipeFindsWithoutDeliveringIt(t *testing.T) {
	e := newEnv(t)
	sub := e.started()
	e.answer(notesJSON(manyNotes("m", 10)...))
	told := len(e.delivered)
	e.patch(sub.ID, Patch{Recipe: ptr(changedRecipe()), Existing: ExistingBaseline})
	if e.get(sub.ID).Baseline == nil {
		t.Fatal("no baseline asked for")
	}
	e.svc.RunDue(e.ctx)
	got, runs, _ := e.svc.Get(e.ctx, sub.ID)
	if got.Baseline != nil || runs[0].New != 0 || runs[0].Baseline != 10 || runs[0].Count != 10 || got.LastNewCount != 0 {
		t.Fatalf("the baseline run: %+v, mark %v", runs[0], got.Baseline)
	}
	if rows := e.rows(sub.ID); len(rows) != 2 || len(e.delivered) != told {
		t.Fatalf("a baseline delivered something: %d rows, told %d times", len(rows), len(e.delivered)-told)
	}
	// What arrives later comes in.
	e.answer(notesJSON(append(manyNotes("m", 10), note{id: "m99", title: "A new one", at: "2026-11-10T09:30:00Z"})...))
	e.clock.advance(20 * time.Minute)
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 3 || rowOf(t, rows, "sub:"+sub.ID+":m99").Title != "A new one" {
		t.Fatalf("after the baseline: %+v", rows)
	}
	if _, runs, _ := e.svc.Get(e.ctx, sub.ID); runs[0].New != 1 || runs[0].Baseline != 0 {
		t.Fatalf("the run after: %+v", runs[0])
	}
}

func TestDeliverBringsInEachItemAChangedRecipeFinds(t *testing.T) {
	e := newEnv(t)
	sub := e.started()
	e.answer(notesJSON(manyNotes("m", 10)...))
	e.patch(sub.ID, Patch{Recipe: ptr(changedRecipe()), Existing: ExistingDeliver})
	e.svc.RunDue(e.ctx)
	if rows := e.rows(sub.ID); len(rows) != 12 {
		t.Fatalf("deliver: %d rows, want the 2 old and 10 new", len(rows))
	}
}

func TestExistingNeedsARecipeThatChanges(t *testing.T) {
	e := newEnv(t)
	sub := e.started()
	_, err := e.svc.Update(e.ctx, sub.ID, Patch{Revision: sub.Revision, Existing: ExistingBaseline}, Actor{ID: "device:phone"})
	if codeOf(err) != CodeBadRequest {
		t.Fatalf("existing without a recipe: %v", err)
	}
	if _, err := e.svc.Update(e.ctx, sub.ID, Patch{Revision: sub.Revision, Recipe: ptr(changedRecipe()), Existing: "all"}, Actor{}); codeOf(err) != CodeBadRequest {
		t.Fatalf("an unknown existing: %v", err)
	}
	// The same recipe again (its arguments written another way) asks for nothing: what it finds is seen already.
	same := threadRecipe()
	same.Args = json.RawMessage(`{"limit": 25, "since": "{{since}}"}`)
	if !SameRecipe(same, threadRecipe()) {
		t.Fatal("the same arguments in another order are another recipe")
	}
	if got := e.patch(sub.ID, Patch{Recipe: &same, Existing: ExistingBaseline}); got.Baseline != nil || got.CatchupFrom != nil {
		t.Fatalf("the same recipe left marks: %v %v", got.Baseline, got.CatchupFrom)
	}
	// A digest only touches an Inbox that was on and stays on.
	e.patch(sub.ID, Patch{ToInbox: ptr(false), ToKB: ptr(true)})
	if got := e.patch(sub.ID, Patch{Recipe: ptr(changedRecipe())}); got.CatchupFrom != nil || got.InboxFrom != nil {
		t.Fatalf("a digest with the Inbox off: %v %v", got.CatchupFrom, got.InboxFrom)
	}
	// The API reads existing from PUT's body.
	var p Patch
	if err := json.Unmarshal([]byte(`{"revision":3,"existing":"baseline"}`), &p); err != nil || p.Existing != ExistingBaseline {
		t.Fatalf("existing: %+v %v", p, err)
	}
}

// A run clears only the baseline it took: a recipe changed again while it ran keeps its own.
func TestABaselineMarkIsClearedOnlyByTheRunThatTookIt(t *testing.T) {
	first, second := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 10, 9, 5, 0, 0, time.UTC)
	cur := Subscription{ID: "sub_a", Baseline: &second}
	applyRun(&cur, RunCommit{Sub: Subscription{ID: "sub_a"}, Baseline: &first}, true)
	if !sameTime(cur.Baseline, &second) {
		t.Fatalf("a run cleared a newer baseline: %v", cur.Baseline)
	}
	applyRun(&cur, RunCommit{Sub: Subscription{ID: "sub_a"}}, true)
	if cur.Baseline == nil {
		t.Fatal("a run that took no baseline cleared one")
	}
	applyRun(&cur, RunCommit{Sub: Subscription{ID: "sub_a"}, Baseline: &second}, false)
	if cur.Baseline != nil {
		t.Fatal("the run that took the baseline did not clear it")
	}
	// Kept across a restart.
	sub := Subscription{ID: "sub_a", Baseline: &first, CatchupFrom: &second}
	raw, err := encode(sub)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := decode(raw); err != nil || !sameTime(back.Baseline, &first) || !sameTime(back.CatchupFrom, &second) {
		t.Fatalf("decoded: %+v %v", back, err)
	}
}

// A dry run warns about what a recipe says that does nothing: keys the format does not know (decoding drops them, so
// they used to pass silently) and fields whose path is empty in every item.
func TestADryRunWarnsAboutUnknownKeysAndEmptyFields(t *testing.T) {
	e := newEnv(t)
	e.answer(notesJSON(note{id: "a1", title: "One", at: "2026-11-10T08:00:00Z"}, note{id: "a2", title: "Two", at: "2026-11-10T08:10:00Z"}))
	var in DryRunRequest
	raw := `{"connector":"example","source":"notices","recipe":{"mode":"items","tool":"list_notices","args":{"since":"{{since}}"},` +
		`"items_path":"$.items","item_path":"$.x","fields":{"id":"$.id","title":"$.title","author":"$.from.name","url":"$.link",` +
		`"link":"$.link","sender":"$.from.name"},"kind":"sub_notice","group_by":"source"}}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if strings.Join(in.Unknown, ",") != "fields.author,fields.url,item_path" {
		t.Fatalf("unknown keys: %v", in.Unknown)
	}
	dr, err := e.svc.DryRun(e.ctx, in)
	if err != nil || !dr.OK {
		t.Fatalf("warnings fail nothing: %+v %v", dr, err)
	}
	var got []string
	for _, w := range dr.Warnings {
		got = append(got, w.Code+" "+w.Key+" "+w.Path)
	}
	want := "unknown_key fields.author ,unknown_key fields.url ,unknown_key item_path ,empty_field fields.link $.link"
	if strings.Join(got, ",") != want {
		t.Fatalf("warnings:\n%s\nwant\n%s", strings.Join(got, ","), want)
	}
	// Each item says what the Inbox row shows and which of its fields came out empty.
	if it := dr.Items[0]; it.Title != "One" || it.Sender != "Office" || it.Time != nil || it.Summary != "" || // no time or body mapped
		len(it.Problems) != 1 || it.Problems[0] != (FieldProblem{Key: "fields.link", Code: FieldEmpty}) {
		t.Fatalf("a previewed item: %+v", it)
	}
	e.answer(`{"items":[{"id":"b1","title":"Bad time","at":"soon","text":"Line one\n\n  line   two"}]}`)
	bad, err := e.svc.DryRun(e.ctx, DryRunRequest{Connector: "example", Source: "notices", Recipe: ptr(itemsRecipe())})
	if err != nil || bad.OK || len(bad.Items) != 1 || bad.Items[0].Summary != "Line one line two" ||
		fmt.Sprint(bad.Items[0].Problems) != "[{fields.sender empty} {fields.time bad_time}]" {
		t.Fatalf("an item whose time cannot be read: %+v %v", bad.Items, err)
	}
	e.answer(notesJSON(note{id: "a1", title: "One", at: "2026-11-10T08:00:00Z"}))
	// A clean recipe has none, and says so with an empty list.
	clean, err := e.svc.DryRun(e.ctx, DryRunRequest{Connector: "example", Source: "notices", Recipe: ptr(itemsRecipe())})
	if err != nil || clean.Warnings == nil || len(clean.Warnings) != 0 {
		t.Fatalf("a clean recipe: %+v %v", clean.Warnings, err)
	}
	if b, _ := json.Marshal(clean); !strings.Contains(string(b), `"warnings":[]`) {
		t.Fatalf("warnings are not in the answer: %s", b)
	}
}

// Patched shows a change as Update makes it: what the assistant's card says is what is saved.
func TestPatchedIsWhatUpdateMakes(t *testing.T) {
	e := newEnv(t)
	sub := e.started()
	snap := Recipe{Mode: "snapshot", Tool: "balance", Kind: "sub_update", GroupBy: "source"}
	for _, p := range []Patch{
		{Label: ptr("  Work   notices "), IntervalSeconds: ptr(3600), TriageRules: ptr("Only from the office"), FallbackTier: ptr("action")},
		{ToInbox: ptr(false), ToKB: ptr(true)},
		{Recipe: &snap},
		{DailyAt: ptr("07:30")},
		{Label: ptr("")},
	} {
		cur := e.get(sub.ID)
		p.Revision = cur.Revision
		want := Patched(cur, p)
		got, err := e.svc.Update(e.ctx, sub.ID, p, Actor{ID: "device:phone"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Label != want.Label || got.IntervalSeconds != want.IntervalSeconds || got.DailyAt != want.DailyAt || got.ToInbox != want.ToInbox ||
			got.ToKB != want.ToKB || got.TriageRules != want.TriageRules || got.FallbackTier != want.FallbackTier || got.Recipe.Mode != want.Recipe.Mode ||
			got.State != want.State || got.MinIntervalSeconds != want.MinIntervalSeconds {
			t.Fatalf("patch %+v:\nupdate  %+v\npatched %+v", p, got, want)
		}
	}
}
