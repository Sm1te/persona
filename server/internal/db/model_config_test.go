package db

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestCompareSettingRejectsStaleAndConcurrentWrites(t *testing.T) {
	d, ctx := open(t), context.Background()
	value := func(rev int64, model string) any {
		return map[string]any{"revision": rev, "binding": map[string]string{"model": model}}
	}
	if ok, err := d.CompareSetting(ctx, "model_config.test", 0, value(1, "first")); err != nil || !ok {
		t.Fatalf("create %v %v", ok, err)
	}
	if ok, err := d.CompareSetting(ctx, "model_config.test", 0, value(1, "stale")); err != nil || ok {
		t.Fatalf("stale %v %v", ok, err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := d.CompareSetting(ctx, "model_config.test", 1, value(2, "winner"))
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("concurrent winners: %d", wins)
	}
	if ok, err := d.CompareSetting(ctx, "model_config.other", 0, value(1, "other")); err != nil || !ok {
		t.Fatalf("independent key %v %v", ok, err)
	}
	var r struct{ Revision int64 }
	if _, err := d.Setting(ctx, "model_config.test", &r); err != nil || r.Revision != 2 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestNodeModelMetadataIsPersisted(t *testing.T) {
	d, ctx := open(t), context.Background()
	s := NodeSession{Launcher: "claude", Cwd: "/tmp/assistant", StartedAt: time.Now().UTC(), Model: "specific", ModelFingerprint: "fingerprint"}
	if err := d.PutNodeSession(ctx, "machine", "session", s); err != nil {
		t.Fatal(err)
	}
	out, err := d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	if got := out["session"]; got.Model != s.Model || got.ModelFingerprint != s.ModelFingerprint {
		t.Fatalf("lost model: %+v", got)
	}
	s.Model, s.ModelFingerprint = "", ""
	if err := d.PutNodeSession(ctx, "machine", "session", s); err != nil {
		t.Fatal(err)
	}
	out, err = d.NodeSessions(ctx, "machine")
	if err != nil || out["session"].Model != "" {
		t.Fatalf("failed to restore default: %+v %v", out, err)
	}
}

// The mode the app set on an assistant conversation comes back after a node restart (#2416).
func TestNodeAppModesArePersisted(t *testing.T) {
	d, ctx := open(t), context.Background()
	s := NodeSession{Launcher: "claude", Cwd: "/tmp/assistant", StartedAt: time.Now().UTC(), AppMode: "bypassPermissions", AppEffort: "high"}
	if err := d.PutNodeSession(ctx, "machine", "session", s); err != nil {
		t.Fatal(err)
	}
	out, err := d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	if got := out["session"]; got.AppMode != s.AppMode || got.AppEffort != s.AppEffort {
		t.Fatalf("lost app modes: %+v", got)
	}
}

// PutSettings writes all its keys or none: a model profile's switch is never half applied.
func TestPutSettingsIsAllOrNothing(t *testing.T) {
	d, ctx := open(t), context.Background()
	if err := d.PutSettings(ctx, map[string]any{"model_config.a": map[string]int{"revision": 1}, "model_profiles": []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	var r struct{ Revision int64 }
	if _, err := d.Setting(ctx, "model_config.a", &r); err != nil || r.Revision != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	// A value that cannot be stored fails the whole write, before anything is written.
	err := d.PutSettings(ctx, map[string]any{"model_config.a": map[string]int{"revision": 2}, "model_config.b": func() {}})
	if err == nil {
		t.Fatal("an unmarshalable value was written")
	}
	if _, err := d.Setting(ctx, "model_config.a", &r); err != nil || r.Revision != 1 {
		t.Fatalf("half written: %+v %v", r, err)
	}
	// A failing statement rolls the earlier ones back: "\u0000" is valid JSON Postgres' jsonb refuses.
	err = d.PutSettings(ctx, map[string]any{"model_config.a": map[string]int{"revision": 3}, "model_config.z": "\u0000"})
	if err == nil {
		t.Fatal("jsonb took a NUL")
	}
	if _, err := d.Setting(ctx, "model_config.a", &r); err != nil || r.Revision != 1 {
		t.Fatalf("not rolled back: %+v %v", r, err)
	}
	if ok, _ := d.Setting(ctx, "model_config.z", &r); ok {
		t.Fatal("the failing key was written")
	}
}

// A launcher switch waiting for the next message (node/launcher_switch.go) comes back after a node restart, and a
// record written without one has none (#5035).
func TestNodePendingSwitchIsPersisted(t *testing.T) {
	d, ctx := open(t), context.Background()
	pending := `{"launcher":"work","model":"opus","at":"2026-11-12T10:00:00Z"}`
	s := NodeSession{Launcher: "claude", Cwd: "/tmp/project", StartedAt: time.Now().UTC(), PendingSwitch: json.RawMessage(pending)}
	if err := d.PutNodeSession(ctx, "machine", "session", s); err != nil {
		t.Fatal(err)
	}
	out, err := d.NodeSessions(ctx, "machine")
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(out["session"].PendingSwitch, &got); err != nil {
		t.Fatalf("pending switch %q: %v", out["session"].PendingSwitch, err)
	}
	_ = json.Unmarshal([]byte(pending), &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pending switch = %v, want %v", got, want)
	}
	s.PendingSwitch = nil
	if err := d.PutNodeSession(ctx, "machine", "session", s); err != nil {
		t.Fatal(err)
	}
	if out, err = d.NodeSessions(ctx, "machine"); err != nil || out["session"].PendingSwitch != nil {
		t.Fatalf("cleared switch came back: %q %v", out["session"].PendingSwitch, err)
	}
}
