package assistant

import (
	"context"
	"fmt"

	"github.com/nwverse/persona/server/internal/proto"
)

// Owner snapshots can lag an answer or be reconstructed after a restart. The
// durable outcome belongs to this question ID; replay it to the owner, never ask
// the user again. New IDs still go through the ordinary interaction lifecycle.
func (s *Service) waitOwnedQuestion(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) {
	if st := s.fleetStore(); st != nil {
		readCtx, cancel := context.WithTimeout(ctx, historyTimeout)
		status, kept, err := st.InteractionOutcome(readCtx, ix.ID)
		cancel()
		if err != nil {
			return proto.Resolution{}, fmt.Errorf("read owned question outcome: %w", err)
		}
		if status != "" && status != "pending" {
			s.finishInteraction(ix.ID)
			if kept != nil && (status == "resolved" || status == "cancelled" && kept.Cancel) {
				return *kept, nil
			}
			return proto.Resolution{Dismiss: true}, nil
		}
	}
	// A failed durable write can leave a card pending after its owner waiter
	// disconnected. Reattach without another announcement or history insert.
	s.fleet.mu.Lock()
	p := s.fleet.interactions[ix.ID]
	// A resumable one (asked without waiting, ask_later.go) is claimed as a hook claims its card again: the grace
	// period a restart started for it is called off there (waitInteraction).
	reattach := p != nil && p.local == nil && !ix.Resumable
	s.fleet.mu.Unlock()
	if reattach {
		return s.await(ctx, p)
	}
	return s.waitInteraction(ctx, ix)
}
