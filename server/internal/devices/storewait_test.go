package devices

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/db"
)

// stalledStore is a store that does not answer, like Postgres in a lock wait or with its pool used up: every call
// waits for its context to end.
type stalledStore struct{ *memStore }

func (stalledStore) Setting(ctx context.Context, _ string, _ any) (bool, error) {
	<-ctx.Done()
	return false, ctx.Err()
}

func (stalledStore) PutSetting(ctx context.Context, _ string, _ any) error {
	<-ctx.Done()
	return ctx.Err()
}

func (stalledStore) PutNodeSession(ctx context.Context, _, _ string, _ db.NodeSession) error {
	<-ctx.Done()
	return ctx.Err()
}

func (stalledStore) NodeSessions(ctx context.Context, _ string) (map[string]db.NodeSession, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// A node's event that writes to the store runs on the node connection's read loop: a store that does not answer
// must not hold it (and every reply and event of that node behind it) for longer than the machine's storeWait.
func TestStoreStallDoesNotHoldTheNodesEvents(t *testing.T) {
	for _, c := range []struct{ event, params, logged string }{
		{"state.put", `{"session_id":"s1","state":{"launcher":"claude","cwd":"/w"}}`, `"result":"store_error"`},
		{"claude.dirs", `{"dirs":["/home/me/.config/cc/work"]}`, "remember claude dirs: read: context deadline exceeded"},
	} {
		t.Run(c.event, func(t *testing.T) {
			var mu sync.Mutex
			var logged []string
			logf := func(f string, a ...any) {
				mu.Lock()
				defer mu.Unlock()
				logged = append(logged, fmt.Sprintf(f, a...))
			}
			d := New(Options{Hub: &fakeHost{}, Store: stalledStore{newMemStore()}, Logf: logf})
			m := newMachine(d, MachineConfig{ID: "box", Name: "Box", Kind: "ssh", SSHHost: "box.invalid"})
			m.storeWait = 50 * time.Millisecond // this machine's own: no package var for another test's run to read
			done := make(chan struct{})
			go func() {
				defer close(done)
				m.onEvent(c.event, json.RawMessage(c.params))
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatalf("%s still waits on a store that does not answer: the node's read loop is held", c.event)
			}
			mu.Lock()
			defer mu.Unlock()
			if !strings.Contains(strings.Join(logged, "\n"), c.logged) {
				t.Fatalf("the given-up call is not logged: want %q in %q", c.logged, logged)
			}
		})
	}
}

// A Machine not made by newMachine (storeWait zero) still waits the default, not no time at all.
func TestStoreCtxDefaultsWithoutAWait(t *testing.T) {
	ctx, cancel := (&Machine{}).storeCtx()
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok || time.Until(dl) < nodeStoreWait/2 {
		t.Fatalf("deadline %v (set %v), want about %v from now", dl, ok, nodeStoreWait)
	}
}
