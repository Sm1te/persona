package hub

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/extwire"
)

// meeting_attendance is listed while the default manager (the one the recordings' routes go to) is connected and
// named the capability in its hello: not for another manager's, not from an extension's report, not once it is gone.
func TestMeetingAttendanceFollowsTheDefaultManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	a := connectStateManager(t, h, srv, assistantwire.AssistantID)
	b := connectStateManager(t, h, srv, "m-0b0b0b")
	hello := func(m *stateManager, attends bool) {
		t.Helper()
		caps := []string{assistantwire.CapabilityConfigOwn}
		if attends {
			caps = append(caps, assistantwire.CapabilityMeetingAttendance)
		}
		if err := m.call(t, busHello, map[string]any{"id": m.id, "capabilities": caps}, nil); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() bool { return slices.Contains(h.features(), featureMeetingAttendance) }
	if listed() {
		t.Fatal("listed before any manager named it")
	}
	hello(b, true)
	if listed() {
		t.Fatal("listed for a manager that is not the default")
	}
	hello(a, true)
	if !listed() {
		t.Fatal("the default manager names it, and it is not listed")
	}

	// An extension's report cannot add it.
	hello(a, false)
	a.notify(t, extwire.EventReport, extwire.Report{Features: []string{featureMeetingAttendance, "fa"}})
	eventually(t, 5*time.Second, "the report", func() bool { return slices.Contains(h.features(), "fa") })
	if listed() {
		t.Fatal("an extension's report added it")
	}

	// Another manager made the default decides; gone, it is not listed.
	if err := h.managerStore().PutSetting(context.Background(), managerDefaultKey, b.id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.managerStore().PutSetting(context.Background(), managerDefaultKey, assistantwire.AssistantID)
	})
	h.ownersChanged()
	if !listed() {
		t.Fatal("the new default manager names it, and it is not listed")
	}
	if err := b.ws.CloseNow(); err != nil {
		t.Fatal(err)
	}
	eventually(t, 5*time.Second, "the default manager offline", func() bool { return h.busConnOf(b.id) == nil })
	if listed() {
		t.Fatal("listed while the default manager is offline")
	}
}

// meeting_conversation is listed only beside meeting_attendance, while the default manager names both.
func TestMeetingConversationFollowsTheDefaultManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	a := connectStateManager(t, h, srv, assistantwire.AssistantID)
	hello := func(caps ...string) {
		t.Helper()
		if err := a.call(t, busHello, map[string]any{"id": a.id, "capabilities": append([]string{assistantwire.CapabilityConfigOwn}, caps...)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() bool { return slices.Contains(h.features(), featureMeetingConversation) }
	hello(assistantwire.CapabilityMeetingConversation)
	if listed() {
		t.Fatal("listed without meeting_attendance")
	}
	hello(assistantwire.CapabilityMeetingAttendance, assistantwire.CapabilityMeetingConversation)
	if !listed() || !slices.Contains(h.features(), featureMeetingAttendance) {
		t.Fatalf("features %v", h.features())
	}
	hello(assistantwire.CapabilityMeetingAttendance)
	if listed() {
		t.Fatal("listed for a manager that does not name it")
	}
}

// meeting_photos is listed only beside meeting_conversation, while the default manager names it too.
func TestMeetingPhotosFollowTheDefaultManager(t *testing.T) {
	h, srv, _ := newBusHub(t)
	a := connectStateManager(t, h, srv, assistantwire.AssistantID)
	hello := func(caps ...string) {
		t.Helper()
		if err := a.call(t, busHello, map[string]any{"id": a.id, "capabilities": append([]string{assistantwire.CapabilityConfigOwn}, caps...)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	listed := func() bool { return slices.Contains(h.features(), featureMeetingPhotos) }
	hello(assistantwire.CapabilityMeetingAttendance, assistantwire.CapabilityMeetingPhotos)
	if listed() {
		t.Fatal("listed without meeting_conversation")
	}
	hello(assistantwire.CapabilityMeetingAttendance, assistantwire.CapabilityMeetingConversation, assistantwire.CapabilityMeetingPhotos)
	if !listed() || !slices.Contains(h.features(), featureMeetingConversation) {
		t.Fatalf("features %v", h.features())
	}
	hello(assistantwire.CapabilityMeetingAttendance, assistantwire.CapabilityMeetingConversation)
	if listed() {
		t.Fatal("listed for a manager that does not name it")
	}
}

// An extension's report cannot add a meeting feature: the hub lists them only from the default manager's hello.
func TestMeetingFeaturesNotFromAReport(t *testing.T) {
	h := newTestHub(t)
	h.keepExtReport(assistantwire.AssistantID, &extwire.Report{Features: []string{featureMeetingAttendance, featureMeetingConversation,
		featureMeetingPhotos, "fa"}})
	features := h.features()
	if !slices.Contains(features, "fa") {
		t.Fatalf("the report's own feature is missing: %v", features)
	}
	for _, f := range []string{featureMeetingAttendance, featureMeetingConversation, featureMeetingPhotos} {
		if slices.Contains(features, f) {
			t.Errorf("a report added %s", f)
		}
	}
}
