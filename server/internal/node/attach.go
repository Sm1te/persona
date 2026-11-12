package node

import (
	"context"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/acceptloop"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/link"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A resident node (`personad node --attach`, run by launchd in the user's login session on a Mac) and a relay
// (`personad node --stdio`, what the hub starts over ssh) share the work: when a resident node is listening, the relay
// only pipes the ssh connection to it, and the resident node serves it. Everything the node starts (the assistant's
// claude, its key helper) then runs in the login session, where the login keychain is unlocked: an ssh session
// can't read it, so a claude whose API key comes from the keychain fails to authenticate there (docs/server/server.md).
//
// No resident node listening: --stdio serves the connection itself, as before. The hub doesn't know the difference.

// AttachSocketPath is where a resident node listens for relays.
func AttachSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(identity.SocketDir(home), "node-attach.sock")
}

// Relay pipes r / w to a resident node if one is listening. ok is false when there is none (the caller serves the
// connection itself); otherwise it returns when either side closes.
func Relay(ctx context.Context, r io.Reader, w io.Writer) (ok bool, err error) {
	c, err := dialResident(ctx, AttachSocketPath(), residentRestartWait)
	if err != nil {
		return false, nil
	}
	defer c.Close()
	// hub -> node: when ssh's stdin ends, half-close so the node sees EOF and finishes. node -> hub decides when the
	// relay is over: everything the node wrote must reach the hub, even after the hub stopped sending.
	go func() { _, _ = io.Copy(c, r); _ = c.(*net.UnixConn).CloseWrite() }()
	back := make(chan error, 1)
	go func() { _, err := io.Copy(w, c); back <- err }()
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case err := <-back:
		return true, err
	}
}

// residentRestartWait: how long a relay waits for a resident node that is restarting (its socket is there, nobody
// answers: it is exec'ing a new binary, cmd/personad runResident) before serving the connection itself. Serving it
// here would put the assistant's conversations under this process, which ends with the hub's connection.
var residentRestartWait = 8 * time.Second

// dialResident dials the resident node's socket, again while it refuses (restarting) for up to wait; no socket at all
// (no resident node) returns at once. Once the socket has refused, a missing socket is the restarted node between
// removing the stale socket and listening on a new one (Attach), so that is waited for too.
func dialResident(ctx context.Context, path string, wait time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(wait)
	restarting := false
	for {
		c, err := net.DialTimeout("unix", path, 2*time.Second)
		restarting = restarting || errors.Is(err, syscall.ECONNREFUSED)
		again := errors.Is(err, syscall.ECONNREFUSED) || restarting && errors.Is(err, syscall.ENOENT)
		if err == nil || !again || time.Now().After(deadline) {
			return c, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// residentProgram and residentVersion are what the resident node of this process runs as (SetResident), for standby's
// answer to ping while no Node serves (spool_standby.go).
var residentProgram, residentVersion atomic.Value

// SetResident says which program and build the resident node of this process is (nodecmd runResident, before Attach).
func SetResident(program, version string) {
	residentProgram.Store(program)
	residentVersion.Store(version)
}

// Attach runs a resident node: it listens on AttachSocketPath and serves one hub connection at a time, each relayed
// by `personad node --stdio`, or arriving on extra (the hub dialing in over tailcat, tailcat.go; nil: none). A new
// connection replaces the one being served whichever way either came (the hub reconnected, maybe the other way; the
// old one may not have noticed yet), as a new ssh-started node used to replace the old one. newNode makes the Node
// for a connection. runtimes, when set, is the runtime socket (runtimehost.go): opened here, once this is the one
// resident node, and shared by every connection's Node, so runtimes connect before the hub does and stay connected
// while it reconnects.
func Attach(ctx context.Context, newNode func() (*Node, error), extra <-chan net.Conn, runtimes *RuntimeHost) error {
	path := AttachSocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Only one resident node: a second one would steal relays from the first. The run lock next to the socket, held
	// until Attach returns, decides it: two nodes starting together could both find nobody answering on the socket,
	// and the second would remove the first one's fresh socket. It also keeps a second start from dialing the live
	// node, whose next connection would replace the hub's. The process ending gives the lock back, however it ends.
	release, err := link.LockFile(path+".run", false)
	if errors.Is(err, link.ErrLocked) {
		return fmt.Errorf("a resident node is already running (%s.run is locked)", path)
	}
	if err != nil {
		return fmt.Errorf("resident node lock: %w", err)
	}
	defer release()
	// A resident node from a build without the lock is found by answering. A socket nobody answers on, with the lock
	// free, is stale.
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return fmt.Errorf("a resident node is already listening on %s", path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	ln.(*net.UnixListener).SetUnlinkOnClose(false) // a replacement may already own the path (hooks.go has the same trap)
	go func() { <-ctx.Done(); ln.Close() }()
	logf := func(f string, a ...any) { fmt.Fprintf(os.Stderr, "node: "+f+"\n", a...) }
	if runtimes != nil {
		if err := runtimes.Listen(ctx); err != nil {
			logf("runtime socket: %v", err)
			runtimes = nil // each connection's Node opens its own, as without a resident node
		}
	}
	relayed := make(chan net.Conn)
	go func() {
		defer close(relayed)
		acceptloop.Run(ctx, ln, logf, func(c net.Conn) {
			select {
			case relayed <- c:
			case <-ctx.Done(): // ctx closes ln too, which ends the loop
				c.Close()
			}
		})
	}()

	keeper := newAssistantKeeper()
	scans := &bgCarry{} // background-task scans go on from one connection's Node to the next (carry.go)
	// Launcher switches too: one that outlives its connection is seen, waited for and written by the next Node
	// (switch_share.go).
	switches := &switchShare{}
	// No manager connected yet (or between connections): standby answers node.sock (spool_standby.go).
	sp := openSpool(SpoolPath())
	sb := newStandby(sp, logf)
	sb.want()
	defer sb.unwant()
	var mu sync.Mutex        // served, and standby's want / unwant with it
	var served chan struct{} // closed when the connection being served has ended
	var current context.CancelFunc
	var wg sync.WaitGroup
	defer keeper.stopAll() // after the last connection handed them over: this resident node is ending
	defer wg.Wait()
	for {
		var c net.Conn
		var ok bool
		select {
		case <-ctx.Done():
			return nil
		case c, ok = <-relayed:
			if !ok {
				return nil
			}
		case c, ok = <-extra:
			if !ok {
				extra = nil // that way is gone; relays go on
				continue
			}
		}
		if current != nil {
			current() // the previous connection's node shuts down; its hooks retry on the new one (hooks.go)
		}
		cctx, cancel := context.WithCancel(ctx)
		current = cancel
		wg.Add(1)
		mu.Lock()
		prev, done := served, make(chan struct{})
		served = done
		sb.unwant() // the new connection's Node takes node.sock; under mu, so an ending one cannot want it back
		mu.Unlock()
		began := time.Now()
		go func() {
			defer wg.Done()
			defer func() { // the last connection ended and no other came: standby until the next
				mu.Lock()
				defer mu.Unlock()
				if time.Since(began) >= time.Minute { // a connection that held (replaced or not): the hold counts from now
					sb.managerSeen(time.Now())
				}
				if served != done || ctx.Err() != nil {
					return
				}
				sb.want()
			}()
			defer close(done)
			defer c.Close()
			defer cancel()
			if prev != nil { // the last connection's Node hands its conversations over as it ends (assistant_keep.go)
				select {
				case <-prev:
				case <-time.After(10 * time.Second):
				}
			}
			n, err := newNode()
			if err != nil {
				fmt.Fprintf(os.Stderr, "node: %v\n", err)
				return
			}
			if runtimes != nil {
				n.runtimeHost = runtimes // Serve attaches it for this connection
			}
			n.adoptBgScans(scans)
			n.share.Store(switches)
			n.keeper = keeper                        // conversations go on from one connection to the next (assistant_keep.go)
			n.spool = sp                             // what standby took goes to this connection first (spool.go)
			go func() { <-cctx.Done(); c.Close() }() // a replaced connection stops reading at once
			if err := n.Serve(cctx, c, c); err != nil && !errors.Is(err, io.EOF) && cctx.Err() == nil {
				n.logger.Printf("connection ended: %v", err)
			}
			n.handOverBgScans(scans)
		}()
	}
}
