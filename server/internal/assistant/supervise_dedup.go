package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/prompts"
)

const superviseDedupKey = "assistant.supervision_dedup"
const superviseDedupWindow = 30 * time.Minute

type superviseFingerprint struct {
	Hash string    `json:"hash"`
	At   time.Time `json:"at"`
}

func digestFingerprint(e prompts.SuperviseEntry, g superviseSignal) (string, string) {
	// State timestamps and elapsed minutes deliberately do not participate: a new
	// turn or a periodic sweep is not new information when the last summary is unchanged.
	parts := []string{g.Todo, g.Machine, e.Machine, g.Session, e.AgentID, g.Kind, g.Report}
	if g.To != "" { // a card's rung (ask_route.go): each conversation hears of it; the others' keys stay as they were
		parts = append(parts, g.To)
	}
	key, _ := json.Marshal(parts)
	text := e.Last
	if text == "" {
		text = g.Detail
	}
	fields := []any{strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(e.Ask), " "), e.Said, e.Evidence, e.Step, e.Steps}
	if len(e.Asks) > 0 { // the cards it lists are what is new about it
		fields = append(fields, e.Asks)
	}
	// Each park's time, and each new Inbox message, is news of its own, whatever its agent said last.
	if g.Kind == sigParkedDue || g.Kind == sigInbox {
		fields = append(fields, g.Key)
	}
	payload, _ := json.Marshal(fields)
	sum := sha256.Sum256(payload)
	return string(key), hex.EncodeToString(sum[:])
}

// reserveSupervision checkpoints deduplication before dispatch. A failed dispatch
// and queue admission releases the reservation. dedupMu is held by sendDigest.
func (s *Service) reserveSupervision(ctx context.Context, entries []prompts.SuperviseEntry, signals []superviseSignal, now time.Time) ([]prompts.SuperviseEntry, []superviseSignal, func(), error) {
	b := &s.supervise
	history := b.dedup
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, superviseDedupKey, &history); err != nil {
			return nil, signals, nil, err
		}
	}
	if history == nil {
		history = map[string]superviseFingerprint{}
	}
	previous := maps.Clone(history)
	next := maps.Clone(history)
	for key, seen := range next {
		if now.Sub(seen.At) >= superviseDedupWindow {
			delete(next, key)
		}
	}
	var out []prompts.SuperviseEntry
	var kept []superviseSignal
	for i, entry := range entries {
		key, hash := digestFingerprint(entry, signals[i])
		if seen, ok := next[key]; ok && seen.Hash == hash && now.Sub(seen.At) < superviseDedupWindow {
			continue
		}
		next[key] = superviseFingerprint{Hash: hash, At: now.UTC()}
		out, kept = append(out, entry), append(kept, signals[i])
	}
	if len(out) == 0 {
		return nil, nil, func() {}, nil
	}
	if st := s.store(); st != nil {
		if err := st.PutSetting(ctx, superviseDedupKey, next); err != nil {
			return nil, signals, nil, err
		}
	}
	b.dedup = next
	return out, kept, func() {
		b.dedup = previous
		if st := s.store(); st != nil {
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := st.PutSetting(cctx, superviseDedupKey, previous); err != nil {
				s.logf("supervise: could not release deduplication reservation: %v", err)
			}
		}
	}, nil
}

// forgetSupervision takes the signals' fingerprints out of the deduplication history: their message was queued for a
// conversation that is gone and was never read, so they are told again. dedupMu is held.
func (s *Service) forgetSupervision(ctx context.Context, signals []superviseSignal, web string) {
	b := &s.supervise
	history := b.dedup
	st := s.store()
	if st != nil {
		if _, err := st.Setting(ctx, superviseDedupKey, &history); err != nil {
			s.logf("supervise: could not read the deduplication history: %v", err)
			return
		}
	}
	next := maps.Clone(history)
	for _, g := range signals {
		if e, ok := s.digestEntry(ctx, g, web); ok {
			key, _ := digestFingerprint(e, g)
			delete(next, key)
		}
	}
	if len(next) == len(history) {
		return
	}
	if st != nil {
		if err := st.PutSetting(ctx, superviseDedupKey, next); err != nil {
			s.logf("supervise: could not release deduplication entries: %v", err)
			return
		}
	}
	b.dedup = next
}

func (s *Service) restoreDigest(signals []superviseSignal, at, prev time.Time) {
	b := &s.supervise
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last.Equal(at) {
		b.last = prev
	}
	for _, g := range signals {
		if b.sent[g.id()] == g.Key {
			delete(b.sent, g.id())
		}
		if cur, newer := b.pending[g.id()]; !newer {
			b.pending[g.id()] = g
		} else if g.Kind == sigInbox && cur.Key != g.Key {
			b.pending[g.id()] = joinInbox(g, cur) // the newer item, with the messages of both
		}
	}
	s.retryDigestLocked(s.superviseTimings().urgent)
}
