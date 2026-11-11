package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// target is where to reach the VNC server: straight at its address, or through a hub's port relay the way the app
// connects (/api/v1/machines/{mid}/ports/5900/connect, docs/operations/ports.md).
type target struct {
	Addr    string // host:port, when not through a hub
	Hub     string // the hub's URL
	Machine string // the machine id on that hub
	Token   string // a device token with the full role (never printed)
	// Shaping, as a phone's link would add: one-way delay each way, and the downstream's bandwidth in bytes a second.
	Delay time.Duration
	Rate  float64
}

func (t target) String() string {
	s := t.Addr
	if t.Hub != "" {
		s = "hub/" + t.Machine
	}
	if t.Delay > 0 {
		s += fmt.Sprintf(" +%s each way", t.Delay)
	}
	if t.Rate > 0 {
		s += fmt.Sprintf(" @%.1f Mbit/s", t.Rate*8/1e6)
	}
	return s
}

func (t target) dial(ctx context.Context) (net.Conn, error) {
	var conn net.Conn
	if t.Hub == "" {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", t.Addr)
		if err != nil {
			return nil, err
		}
		conn = c
	} else {
		u, err := url.Parse(strings.TrimRight(t.Hub, "/") + "/api/v1/machines/" + url.PathEscape(t.Machine) + "/ports/5900/connect")
		if err != nil {
			return nil, err
		}
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+t.Token)
		c, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			return nil, fmt.Errorf("hub relay: %w", err)
		}
		c.SetReadLimit(64 << 20)
		// The connection lives past this call's context: give the NetConn one of its own.
		conn = websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	}
	if t.Delay > 0 || t.Rate > 0 {
		conn = shape(conn, t.Delay, t.Rate)
	}
	return conn, nil
}

// shaped delays both directions by delay and serializes what comes down at rate bytes a second, like a phone's link:
// at most a bandwidth-delay product plus 256 KB is queued, so a server that sends faster is held back as by TCP.
type shaped struct {
	net.Conn
	delay time.Duration
	rate  float64

	mu      sync.Mutex
	cond    *sync.Cond
	down    []chunk
	queued  int
	readErr error
	closed  bool

	up chan chunk
}

type chunk struct {
	b  []byte
	at time.Time // when it may be handed on
}

func shape(c net.Conn, delay time.Duration, rate float64) net.Conn {
	s := &shaped{Conn: c, delay: delay, rate: rate, up: make(chan chunk, 4096)}
	s.cond = sync.NewCond(&s.mu)
	go s.pumpDown()
	go s.pumpUp()
	return s
}

func (s *shaped) limit() int {
	l := 256 << 10
	if s.rate > 0 {
		l += int(s.rate * 2 * s.delay.Seconds())
	}
	return l
}

func (s *shaped) pumpDown() {
	var free time.Time // when the bottleneck is next free
	for {
		b := make([]byte, 32<<10)
		n, err := s.Conn.Read(b)
		now := time.Now()
		s.mu.Lock()
		if n > 0 {
			depart := now
			if s.rate > 0 {
				if free.After(depart) {
					depart = free
				}
				depart = depart.Add(time.Duration(float64(n) / s.rate * float64(time.Second)))
				free = depart
			}
			s.down = append(s.down, chunk{b[:n], depart.Add(s.delay)})
			s.queued += n
			s.cond.Broadcast()
			for s.queued > s.limit() && !s.closed {
				s.cond.Wait()
			}
		}
		if err != nil {
			s.readErr = err
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

func (s *shaped) Read(p []byte) (int, error) {
	s.mu.Lock()
	for len(s.down) == 0 && s.readErr == nil && !s.closed {
		s.cond.Wait()
	}
	if len(s.down) == 0 {
		err := s.readErr
		if err == nil {
			err = net.ErrClosed
		}
		s.mu.Unlock()
		return 0, err
	}
	c := s.down[0]
	s.mu.Unlock()
	if d := time.Until(c.at); d > 0 {
		time.Sleep(d)
	}
	s.mu.Lock()
	n := copy(p, c.b)
	if n == len(c.b) {
		s.down = s.down[1:]
	} else {
		s.down[0].b = c.b[n:]
	}
	s.queued -= n
	s.cond.Broadcast()
	s.mu.Unlock()
	return n, nil
}

func (s *shaped) Write(p []byte) (int, error) {
	s.up <- chunk{append([]byte(nil), p...), time.Now().Add(s.delay)}
	return len(p), nil
}

func (s *shaped) pumpUp() {
	for c := range s.up {
		if d := time.Until(c.at); d > 0 {
			time.Sleep(d)
		}
		if _, err := s.Conn.Write(c.b); err != nil {
			return
		}
	}
}

func (s *shaped) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	return s.Conn.Close()
}

// loadCredentials: VNCBENCH_ACCOUNT / VNCBENCH_PASSWORD (the mock's), else the login keychain's generic password
// "persona-vnc-bench" for the account (added once with `security add-generic-password -s persona-vnc-bench -a
// "$(id -un)" -w`). The password is never printed.
func loadCredentials() (credentials, error) {
	acct := os.Getenv("VNCBENCH_ACCOUNT")
	if acct == "" {
		if u, err := user.Current(); err == nil {
			acct = u.Username
		}
	}
	if p := os.Getenv("VNCBENCH_PASSWORD"); p != "" {
		return credentials{acct, p}, nil
	}
	out, err := exec.Command("security", "find-generic-password", "-s", "persona-vnc-bench", "-a", acct, "-w").Output()
	if err == nil {
		return credentials{acct, strings.TrimRight(string(out), "\n")}, nil
	}
	// A session outside the login one (ssh, an agent's) cannot open the login keychain: a file only the user reads.
	file := os.Getenv("VNCBENCH_PASSWORD_FILE")
	if file == "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			file = filepath.Join(home, ".config", "persona", "vnc-bench-password")
		}
	}
	if fi, ferr := os.Stat(file); ferr == nil {
		if fi.Mode().Perm()&0o077 != 0 {
			return credentials{}, fmt.Errorf("%s is readable by others: chmod 600 it", file)
		}
		b, rerr := os.ReadFile(file)
		if rerr == nil && len(b) > 0 {
			return credentials{acct, strings.TrimRight(string(b), "\r\n")}, nil
		}
	}
	return credentials{}, errors.New("no sign-in: set VNCBENCH_PASSWORD, add it to the keychain with " +
		`security add-generic-password -s persona-vnc-bench -a "$(id -un)" -w` + ", or write it to " + file + " (0600)")
}

// connect dials t, signs in and asks for pf and encs.
func connect(ctx context.Context, t target, cred credentials, pf pixelFormat, encs []int32, rec *recorder) (*client, error) {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := t.dial(dctx)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	c, err := dial(conn, cred, rec)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	if err := c.SetPixelFormat(pf); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := c.SetEncodings(encs); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *client) Close() error { return c.conn.Close() }
