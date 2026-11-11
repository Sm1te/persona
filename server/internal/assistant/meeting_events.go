package assistant

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/kb"
)

// The meeting_live event (docs/assistant/meeting-minutes.md "The meeting_live event"): content-free, it says which
// parts of a meeting changed and their revisions (numbers only), at most one a second per recording; the apps read
// only the newer parts (GET …/live?parts=…). The knowledge base says what changed (kb.Engine.MeetingChanged).

// meetingLiveEvery is the least time between two events of one recording.
const meetingLiveEvery = time.Second

type meetingLiveEvents struct {
	mu      sync.Mutex
	pending map[string][]string  // by recording: the parts changed since its last event
	last    map[string]time.Time // by recording: its last event
	timer   map[string]bool      // by recording: an event is due
}

// meetingLiveChanged: recording id's parts changed. A room that changed wakes the asks waiting for it.
func (s *Service) meetingLiveChanged(id string, parts []string) {
	if slices.Contains(parts, kb.PartRoom) {
		s.room.wake(id)
	}
	b := &s.meetLive
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil {
		b.pending, b.last, b.timer = map[string][]string{}, map[string]time.Time{}, map[string]bool{}
	}
	for _, p := range parts {
		if !slices.Contains(b.pending[id], p) {
			b.pending[id] = append(b.pending[id], p)
		}
	}
	if b.timer[id] {
		return
	}
	b.timer[id] = true
	wait := max(time.Until(b.last[id].Add(meetingLiveEvery)), 0)
	time.AfterFunc(wait, func() { s.meetingLiveFlush(id) })
}

// meetingLiveFlush sends recording id's event: the parts changed and each one's revision now.
func (s *Service) meetingLiveFlush(id string) {
	b := &s.meetLive
	b.mu.Lock()
	parts := b.pending[id]
	delete(b.pending, id)
	delete(b.timer, id)
	b.last[id] = time.Now()
	b.mu.Unlock()
	if len(parts) == 0 {
		return
	}
	ctx := s.runCtx()
	revs := map[string]int64{}
	if e, err := s.kbGet(ctx); err == nil {
		if m, _, err := e.MeetingRead(id); err == nil {
			for _, p := range parts {
				if rev, ok := m.Revs[p]; ok {
					revs[p] = rev
				}
			}
		}
		if slices.Contains(parts, kb.PartAsks) {
			if l, _, err := e.LiveRead(id); err == nil {
				revs[kb.PartAsks] = l.AsksRev
			}
		}
		if slices.Contains(parts, kb.PartPhotos) { // kept in photos.json, not meeting.json
			revs[kb.PartPhotos] = e.PhotosRev(id)
		}
	}
	s.event(context.WithoutCancel(ctx), map[string]any{"type": "meeting_live", "recording_id": id, "changed": parts, "revs": revs})
}
