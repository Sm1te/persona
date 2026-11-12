package node

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// sendQueue lines a session's sends (and ultracode switches) up in the order they came while they wait for its start
// (startLog.wait), which may not hold the input lock: the start types its first prompt under it. Woken together when
// the start is done, they would otherwise race for the lock, and a later message could go in first.
type sendQueue struct {
	mu   sync.Mutex
	tail map[string]chan struct{} // session id -> closed when its last send in line got the input lock (or gave up)
}

// enter waits until the session's earlier sends have the input lock and returns leave, which the caller calls once it
// has the lock (or gives up) to let the next one go. An ended ctx gives up the place in line, never the order behind.
func (q *sendQueue) enter(ctx context.Context, id string) (func(), error) {
	mine := make(chan struct{})
	q.mu.Lock()
	if q.tail == nil {
		q.tail = map[string]chan struct{}{}
	}
	ahead := q.tail[id]
	q.tail[id] = mine
	q.mu.Unlock()
	var once sync.Once
	leave := func() {
		once.Do(func() {
			q.mu.Lock()
			if q.tail[id] == mine {
				delete(q.tail, id)
			}
			q.mu.Unlock()
			close(mine)
		})
	}
	if ahead == nil {
		return leave, nil
	}
	select {
	case <-ahead:
		return leave, nil
	case <-ctx.Done():
		go func() { // the ones behind still go after the ones ahead
			<-ahead
			leave()
		}()
		return nil, fmt.Errorf("这个 Agent 前面的消息还在等着输入，没等到: %w", ctx.Err())
	}
}

// inputTurn waits for the session's start, if it is starting (what is written meanwhile goes in once Claude is ready),
// then takes its input lock, both in the order the callers came (sendQueue). It returns the unlock.
func (n *Node) inputTurn(ctx context.Context, id string) (unlock func(), err error) {
	leave, err := n.sendOrder.enter(ctx, id)
	if err != nil {
		return nil, err
	}
	// Outside the input lock: the start takes it for its first prompt.
	if err := n.starts.wait(ctx, id); err != nil {
		leave()
		return nil, err
	}
	// A launcher switch under way, maybe on the last connection's Node (switch_share.go): after it, on the agent it
	// started, or on the one it brought back.
	if err := n.switches().wait(ctx, id); err != nil {
		leave()
		return nil, err
	}
	unlock = n.typing.lock(id)
	leave()
	return unlock, nil
}

// typingLocks makes the sends to one session take turns, and remembers what was last typed into each pane.
type typingLocks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	last  map[string]typedText
}

type typedText struct {
	text string
	at   time.Time
}

// lock waits for the session's earlier sends and returns the unlock.
func (t *typingLocks) lock(id string) func() {
	t.mu.Lock()
	if t.locks == nil {
		t.locks = map[string]*sync.Mutex{}
	}
	l := t.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		t.locks[id] = l
	}
	t.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// tryLock is lock without waiting: ok is false when another holds it.
func (t *typingLocks) tryLock(id string) (unlock func(), ok bool) {
	t.mu.Lock()
	if t.locks == nil {
		t.locks = map[string]*sync.Mutex{}
	}
	l := t.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		t.locks[id] = l
	}
	t.mu.Unlock()
	if !l.TryLock() {
		return nil, false
	}
	return l.Unlock, true
}

func (t *typingLocks) typed(pane, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]typedText{}
	}
	t.last[pane] = typedText{text, time.Now()}
}

// ours: the box shows (the start of) what was typed into the pane in the last few seconds, or Claude's placeholder
// for a long paste.
func (t *typingLocks) ours(pane, box string) bool {
	t.mu.Lock()
	last, ok := t.last[pane]
	t.mu.Unlock()
	if !ok || time.Since(last.at) > 5*time.Second {
		return false
	}
	b := strings.Join(strings.Fields(box), "")
	if b == "" {
		return false
	}
	return strings.HasPrefix(strings.Join(strings.Fields(last.text), ""), b) || pastePlaceholder.MatchString(box)
}
