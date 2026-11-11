//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The soak's computers. The manager reaches an ssh machine by running `ssh … <host> "<personad> node --stdio"` and
// speaking the node protocol over its pipes (devices.Machine.command). The soak puts this program first on the
// manager's PATH under the name ssh: started that way it is the node of the host it was asked for, with a list of
// sessions that changes on a timer. Nothing else an ssh does is played: `true` succeeds, any other command fails.

const (
	soakSessionsEnv = "FLEETSOAK_SESSIONS"
	soakIntervalEnv = "FLEETSOAK_INTERVAL"
)

// fakeSSH is this program run as ssh; it returns the exit code.
func fakeSSH(args []string) int {
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-o" || args[i] == "-p" || args[i] == "-i" || args[i] == "-F":
			i++
		case strings.HasPrefix(args[i], "-") && len(rest) == 0:
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "fleetsoak ssh: no host and command")
		return 255
	}
	host, command := rest[0], strings.Join(rest[1:], " ")
	switch {
	case strings.HasSuffix(command, "node --stdio"):
		return runFakeNode(host)
	case command == "true":
		return 0
	}
	fmt.Fprintf(os.Stderr, "fleetsoak ssh: %s: only a node is played, not %q\n", host, command)
	return 1
}

// fakeNode is one computer: its sessions as its node lists them.
type fakeNode struct {
	host string
	mu   sync.Mutex
	list []proto.Session
	step int
}

func newFakeNode(host string, sessions int) *fakeNode {
	n := &fakeNode{host: host, list: make([]proto.Session, sessions)}
	at := time.Now().UTC().Truncate(time.Second)
	for i := range n.list {
		// An agent in a terminal pane, as most of a workstation's rows are: never one of the assistant's conversations.
		n.list[i] = proto.Session{SessionID: fmt.Sprintf("0e%06d-0000-4000-8000-%012d", hostNumber(host), i),
			Agent: "claude", Status: proto.StatusDone, Title: fmt.Sprintf("Task %d: make the upload retry test pass", i),
			Cwd: fmt.Sprintf("/home/soak/code/project-%d", i%5), PaneID: fmt.Sprintf("w1:p%d", i), Model: "claude-sonnet-4-5",
			StartedByPersona: true, LastText: lastText(0, i), UpdatedAt: at.Add(-time.Duration(i) * time.Minute), Rev: int64(4096 + i)}
	}
	return n
}

// lastText is what a session said last: a few lines, as a finished turn's summary is.
func lastText(step, i int) string {
	return fmt.Sprintf("Step %d of task %d is done. The retry now waits for the write to land before it reads the file back, "+
		"and the test passes on slow disks too. Next: run the whole package and look at the two flaky cases.", step, i)
}

// hostNumber is the number a host's name ends in (soak07: 7), 0 when it ends in none.
func hostNumber(host string) int {
	i := len(host)
	for i > 0 && host[i-1] >= '0' && host[i-1] <= '9' {
		i--
	}
	n, _ := strconv.Atoi(host[i:])
	return n
}

// change is one change of the list: the next session starts a turn, or the one that started ends it with new last
// words. Every session takes its turn, so a machine has at most one working at a time.
func (n *fakeNode) change() []byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := &n.list[(n.step/2)%len(n.list)]
	if s.Status == proto.StatusWorking {
		s.Status, s.LastText = proto.StatusDone, lastText(n.step, (n.step/2)%len(n.list))
	} else {
		s.Status = proto.StatusWorking
	}
	s.Rev += 512
	s.UpdatedAt = time.Now().UTC()
	n.step++
	return n.encodedLocked()
}

func (n *fakeNode) encoded() []byte {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.encodedLocked()
}

func (n *fakeNode) encodedLocked() []byte {
	b, err := json.Marshal(map[string]any{"sessions": n.list})
	if err != nil {
		panic(err)
	}
	return b
}

func runFakeNode(host string) int {
	sessions, _ := strconv.Atoi(os.Getenv(soakSessionsEnv))
	if sessions <= 0 {
		sessions = 50
	}
	interval, _ := time.ParseDuration(os.Getenv(soakIntervalEnv))
	if interval <= 0 {
		interval = 2 * time.Second
	}
	n := newFakeNode(host, sessions)
	p := rpc.New("n", os.Stdout)
	p.OnRequest = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		switch method {
		case "node.info":
			return proto.NodeInfo{Protocol: nodeproto.Version, ProtocolMin: nodeproto.MinManagerVersion,
				Capabilities: append([]string{nodeproto.MachineUID}, nodeproto.Required...), Hostname: host, OS: "linux", Arch: "amd64",
				Home: "/home/soak", Version: "dev", Program: nodeproto.ProgramPersonad, MachineUID: "fleetsoak-" + host}, nil
		case "sessions.list":
			return rpc.Marshaled(n.encoded()), nil
		}
		return map[string]any{}, nil // configured, watched, asked: taken, with nothing to say
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// The computers change one after another, not all in the same instant.
		select {
		case <-time.After(interval * time.Duration(hostNumber(host)%10) / 10):
		case <-ctx.Done():
			return
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if p.Notify("sessions.changed", rpc.Marshaled(n.change())) != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	_ = p.Serve(ctx, os.Stdin) // until the manager closes the pipe
	cancel()
	return 0
}
