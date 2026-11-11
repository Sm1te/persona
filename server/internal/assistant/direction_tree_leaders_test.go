package assistant

import (
	"context"
	"slices"
	"testing"
	"time"
)

// #4331: the owner's sidebar showed five directions while assistant.leaders had six. The clients read the leaders only
// when they load; what keeps an open sidebar current is the `directions` message, so a leader added (the secretary's
// assistant.leaders, a tap's …/make) or ended (…/end) must change the tree and tell the clients, with no to-do touched.
func TestDirectionTreeTellsTheClientsWhenTheLeadersChange(t *testing.T) {
	e := treeEnv(t)
	e.s.dtree.after = e.clock.After
	ctx := context.Background()
	e.s.directionTreeSync(ctx)
	first := e.directionsEvents()
	ids := func() []string {
		var out []string
		for _, d := range e.s.directionTree(ctx, time.Now()).Directions {
			if !d.Secretary {
				out = append(out, d.ID)
			}
		}
		return out
	}

	// a sixth leader, set as the secretary sets it: no to-do of it yet
	e.title("conv_lead_c", "1111 | 功能 | Docs sync")
	e.leaders("conv_lead_a", "conv_lead_b", "conv_lead_c")
	e.clock.advance(directionTreeSettle)
	if n := e.directionsEvents(); n != first+1 {
		t.Fatalf("a leader added: told %d times, want %d", n, first+1)
	}
	if got := ids(); !slices.Equal(got, []string{"conv_lead_a", "conv_lead_b", "conv_lead_c"}) {
		t.Fatalf("the tree's directions: %v", got)
	}

	// ended by the owner's 取消常驻
	if _, err := e.s.endDirection(ctx, "conv_lead_a"); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(directionTreeSettle)
	if n := e.directionsEvents(); n != first+2 {
		t.Fatalf("a leader ended: told %d times, want %d", n, first+2)
	}
	if got := ids(); !slices.Equal(got, []string{"conv_lead_b", "conv_lead_c"}) {
		t.Fatalf("after ending: %v", got)
	}
}
