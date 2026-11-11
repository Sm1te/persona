package node

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pumps counts n's watch pumps running (the goroutines in watchPump with n as the receiver).
func pumps(n *Node) int {
	buf := make([]byte, 1<<20)
	for {
		m := runtime.Stack(buf, true)
		if m < len(buf) {
			buf = buf[:m]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	return strings.Count(string(buf), fmt.Sprintf("node.(*Node).watchPump(%p", n))
}

// One goroutine reads every watched session (it was two tickers per watch: a resting node woke for each), a read held
// up (a long parse holds the session's push lock) does not hold up another session's, and the pump ends with the last
// watch.
func TestWatchPumpReadsAllWatchesInOneGoroutine(t *testing.T) {
	n, home := testNode(t)
	dir := filepath.Join(home, ".claude", "projects", "-work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	hub := withHub(t, n)
	var ids, paths []string
	for i := range 4 {
		id := fmt.Sprintf("11111111-2222-3333-4444-%012d", 900+i)
		path := filepath.Join(dir, id+".jsonl")
		writeCodexToolLines(t, path, 0, 1)
		if err := n.watch(id, 0); err != nil {
			t.Fatal(err)
		}
		ids, paths = append(ids, id), append(paths, path)
	}
	// watch starts the pump with `go`: it may not have run yet. Once it has, it is the only one.
	waitFor(t, "the watch pump", func() bool { return pumps(n) > 0 })
	if got := pumps(n); got != 1 {
		t.Fatalf("%d watch pumps for 4 watches, want 1", got)
	}

	held := n.pushLock(ids[0])
	held.Lock()
	for i, path := range paths {
		writeCodexToolLines(t, path, 1+i, 1) // one line: two items
	}
	waitFor(t, "the other sessions' items", func() bool { return len(hub.got()) >= 6 })
	if got := len(hub.got()); got != 6 {
		t.Fatalf("%d items while the first session is held up, want 6", got)
	}
	held.Unlock()
	waitFor(t, "the first session's items", func() bool { return len(hub.got()) == 8 })

	for _, id := range ids {
		n.unwatch(id)
	}
	waitFor(t, "the pump to end", func() bool { return pumps(n) == 0 })
}
