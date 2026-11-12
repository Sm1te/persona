package memtrim

import (
	"sync"
	"testing"
	"time"
)

// clock is a trims' time and timers, moved by hand: to runs every timer due by then, in order, at its own time.
type clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []timer
	ran    []time.Duration // when each trim ran, since start
	start  time.Time
}

type timer struct {
	at time.Time
	f  func()
}

func newClock() (*clock, *trims) {
	c := &clock{now: time.Unix(1_700_000_000, 0)}
	c.start = c.now
	t := &trims{
		now: func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now },
		after: func(d time.Duration, f func()) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.timers = append(c.timers, timer{c.now.Add(d), f})
		},
	}
	t.free = func() { c.mu.Lock(); defer c.mu.Unlock(); c.ran = append(c.ran, c.now.Sub(c.start)) }
	return c, t
}

// to moves the clock to d after its start.
func (c *clock) to(d time.Duration) {
	for {
		c.mu.Lock()
		at, due := c.start.Add(d), -1
		for i, tm := range c.timers {
			if !tm.at.After(at) && (due < 0 || tm.at.Before(c.timers[due].at)) {
				due = i
			}
		}
		if due < 0 {
			c.now = at
			c.mu.Unlock()
			return
		}
		tm := c.timers[due]
		c.timers = append(c.timers[:due], c.timers[due+1:]...)
		c.now = tm.at
		c.mu.Unlock()
		tm.f()
	}
}

func (c *clock) trims() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.ran...)
}

func equal(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every large body is followed by a trim Settle or more after it, the trims are Every apart at the closest, and an
// idle process runs none.
func TestEveryLargeBodyGetsATrim(t *testing.T) {
	for _, c := range []struct {
		name   string
		bodies []time.Duration // when each large body ends
		want   []time.Duration // when the trims run
	}{
		{"one body", []time.Duration{0}, []time.Duration{Settle}},
		{"a burst at once", []time.Duration{0, 0, 0, 0}, []time.Duration{Settle}},
		{"a second before the first's trim", []time.Duration{0, Settle / 2}, []time.Duration{Settle, Settle + Every}},
		{"a second within Every", []time.Duration{0, 5 * time.Second}, []time.Duration{Settle, Settle + Every}},
		{"several within Every", []time.Duration{0, 5 * time.Second, 12 * time.Second, 29 * time.Second}, []time.Duration{Settle, Settle + Every}},
		{"one just before the next turn", []time.Duration{0, 5 * time.Second, Every + Settle/2}, []time.Duration{Settle, Settle + Every, Settle + 2*Every}},
		{"a second just after Every", []time.Duration{0, Every + Settle/2}, []time.Duration{Settle, Every + Settle/2 + Settle}},
		{"a second after Every", []time.Duration{0, 2 * Every}, []time.Duration{Settle, 2*Every + Settle}},
		{"a steady stream", []time.Duration{0, 10 * time.Second, 20 * time.Second, 30 * time.Second, 40 * time.Second, 50 * time.Second, 60 * time.Second},
			[]time.Duration{Settle, Settle + Every, Settle + 2*Every}},
	} {
		t.Run(c.name, func(t *testing.T) {
			clk, tr := newClock()
			for _, at := range c.bodies {
				clk.to(at)
				tr.ask()
			}
			clk.to(10 * Every)
			got := clk.trims()
			if !equal(got, c.want) {
				t.Fatalf("trims at %v, want %v", got, c.want)
			}
			for _, at := range c.bodies { // the property the table spells out
				followed := false
				for _, ran := range got {
					followed = followed || ran >= at+Settle
				}
				if !followed {
					t.Errorf("the body at %v has no trim %v or more after it (trims at %v)", at, Settle, got)
				}
			}
			for i := 1; i < len(got); i++ {
				if got[i]-got[i-1] < Every {
					t.Errorf("trims %v and %v are less than %v apart", got[i-1], got[i], Every)
				}
			}
			if tr.waiting || len(clk.timers) != 0 {
				t.Errorf("idle with a trim still waiting (%v, %d timers)", tr.waiting, len(clk.timers))
			}
		})
	}
}

// A body under LargeBody asks for nothing; many large ones at once, from many goroutines, start one trim.
func TestAfterLargeOnlyFromLargeBody(t *testing.T) {
	clk, tr := newClock()
	orig := process
	process = tr
	t.Cleanup(func() { process = orig })

	AfterLarge(LargeBody - 1)
	AfterLarge(0)
	clk.to(2 * Every)
	if got := clk.trims(); len(got) != 0 {
		t.Fatalf("bodies under LargeBody started trims at %v", got)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { AfterLarge(LargeBody) })
	}
	wg.Wait()
	clk.to(4 * Every)
	if got := clk.trims(); len(got) != 1 {
		t.Fatalf("32 large bodies at once started trims at %v, want one", got)
	}
}

// The process's own schedule runs the real thing on the real clock.
func TestProcessTrimsWithFreeOSMemory(t *testing.T) {
	if process.free == nil || process.after == nil || process.now == nil {
		t.Fatal("the process's schedule is not wired")
	}
	ran := make(chan struct{})
	tr := &trims{free: func() { close(ran) }, after: process.after, now: process.now}
	tr.want = tr.now().Add(-Settle) // as asked a moment ago: its turn is now
	tr.waiting = true
	tr.after(0, tr.run)
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("a due trim did not run")
	}
}
