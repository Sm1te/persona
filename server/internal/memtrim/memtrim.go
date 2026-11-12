// Package memtrim gives the memory of a large request back to the operating system once it is done with.
//
// A relayed upload of 22 MB takes the hub's slot and the manager past a hundred MB, and when nothing else runs
// nothing allocates after it: no collection runs, and the pages stay with the process for minutes (the scavenger
// returns them slowly). After a body of LargeBody bytes or more, AfterLarge collects and returns the free pages soon
// after, on a goroutine of its own, at most once per Every per process.
package memtrim

import (
	"runtime/debug"
	"sync"
	"time"
)

const (
	// LargeBody is the size from which a body is worth returning the memory after.
	LargeBody = 8 << 20
	// Every bounds how often a process does it: a collection that returns every free page costs a few milliseconds
	// of CPU, and a burst of uploads needs it once.
	Every = 30 * time.Second
	// Settle is how long after a body its trim waits: the caller's request ends first (its answer written, its
	// buffers dropped), so the collection finds them garbage.
	Settle = time.Second
)

// trims is one process's schedule. A body asks for a trim Settle after it; the trims run Every apart at the closest,
// and the last body of a burst still gets its own: one that ends within Every of a trim waits for the next turn,
// which is started when it asks (none waiting yet) or by the trim that runs before it.
type trims struct {
	mu      sync.Mutex
	want    time.Time // the latest body's: a trim at or after this has it
	ran     time.Time // the last trim's turn (zero: never)
	waiting bool      // a trim is scheduled
	free    func()
	after   func(time.Duration, func()) // time.AfterFunc
	now     func() time.Time
}

var process = &trims{free: debug.FreeOSMemory, after: func(d time.Duration, f func()) { time.AfterFunc(d, f) }, now: time.Now}

// AfterLarge is called as a request with a body of size bytes ends: from LargeBody up, the memory goes back Settle
// later, or at the next turn when a trim ran within Every.
func AfterLarge(size int) {
	if size >= LargeBody {
		process.ask()
	}
}

func (t *trims) ask() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.want = now.Add(Settle)
	if !t.waiting {
		t.waiting = true
		t.after(t.turn().Sub(now), t.run)
	}
}

// turn is when the next trim runs: at want, and no sooner than Every after the last.
func (t *trims) turn() time.Time {
	if next := t.ran.Add(Every); !t.ran.IsZero() && next.After(t.want) {
		return next
	}
	return t.want
}

// run is a trim's turn. A body that ended less than Settle before it may still hold its buffers: it gets the next.
func (t *trims) run() {
	t.mu.Lock()
	now := t.now()
	t.ran = now
	if t.want.After(now) {
		t.after(t.turn().Sub(now), t.run)
	} else {
		t.waiting = false
	}
	t.mu.Unlock()
	t.free()
}
