package assistant

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// lineHub is a hub that writes what it is told as the manager's bus connection does (BusHub.Tell: the rpc peer's
// Notify), and keeps the values as they were handed over.
type lineHub struct {
	*fakeHub
	peer *rpc.Peer

	mu     sync.Mutex
	events []string
	values []any
	keep   bool // keep the values (a benchmark only counts them)
	raw    bool // write rpc.Marshaled params as json.RawMessage, as the fleet's events were told before
}

func newLineHub(w io.Writer, keep bool) *lineHub {
	return &lineHub{fakeHub: newFakeHub(), peer: rpc.New("m", w), keep: keep}
}

func (h *lineHub) Tell(event string, params any) error {
	h.mu.Lock()
	h.events = append(h.events, event)
	if h.keep {
		h.values = append(h.values, params)
	}
	h.mu.Unlock()
	if m, ok := params.(rpc.Marshaled); ok && h.raw {
		params = json.RawMessage(m)
	}
	return h.peer.Notify(event, params)
}

// toldOf is how many times the hub was told event.
func (h *lineHub) toldOf(event string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, e := range h.events {
		if e == event {
			n++
		}
	}
	return n
}

// toldFleet is a manager whose machine "mac" lists rows sessions, on a hub that writes its lines, and two lists of
// them that differ in one row: a turn that ended.
func toldFleet(tb testing.TB, rows int) (*Service, *fleetDevs, *lineHub, [2][]proto.Session) {
	tb.Helper()
	h := newLineHub(io.Discard, false)
	d := newFleetDevs()
	d.add("mac", "Mac")
	s := New(Options{Hub: h, Logf: func(string, ...any) {}, Devices: d})
	var lists [2][]proto.Session
	for k := range lists {
		lists[k] = make([]proto.Session, rows)
		for i := range lists[k] {
			lists[k][i] = proto.Session{SessionID: fmt.Sprintf("s%03d", i), Cwd: "/code/app", Agent: "claude",
				Status: proto.StatusDone, PaneID: fmt.Sprintf("p%d", i), SpaceName: "app", Title: "a <b>session</b> & more",
				LastText: strings.Repeat("the last words of the turn ", 8)}
		}
	}
	lists[1][rows/2].Status, lists[1][rows/2].LastText = proto.StatusWorking, "working on it"
	return s, d, h, lists
}

// BenchmarkFleetSessionsTold: a machine's list changed and is told to the hub (publishList), 50 and 200 sessions: the
// list decorated, encoded, compared with the one told last and written into the bus's line.
func BenchmarkFleetSessionsTold(b *testing.B) {
	for _, rows := range []int{50, 200} {
		b.Run(fmt.Sprintf("sessions=%d", rows), func(b *testing.B) {
			s, d, h, lists := toldFleet(b, rows)
			n := 0
			b.ReportAllocs()
			for b.Loop() {
				d.setSessions("mac", lists[n&1])
				s.publishSessions("mac")
				n++
			}
			if got := h.toldOf(assistantwire.EventFleetSessions); got != n {
				b.Fatalf("told %d lists of %d", got, n)
			}
		})
	}
}
