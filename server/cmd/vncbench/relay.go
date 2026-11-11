package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// relayBench measures the hub's port relay itself, with an echo server of its own on this machine: the round trip of
// a small message straight to the hub (WebSocket ping) and through the relay to the node and back, idle and while a
// download fills the way down, and the download's throughput. The port is opened on the hub first (POST …/open).
// With echoOnly it only serves the echo (on the machine measured, when the client runs elsewhere); with noEcho it
// expects one already there.
func relayBench(ctx context.Context, t target, port int, echoOnly, noEcho bool) error {
	if !noEcho {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return err
		}
		defer l.Close()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go echo(c)
			}
		}()
		if echoOnly {
			<-ctx.Done()
			return nil
		}
	}
	if t.Hub == "" {
		return fmt.Errorf("relay needs -hub and -machine")
	}
	base := strings.TrimRight(t.Hub, "/") + "/api/v1/machines/" + url.PathEscape(t.Machine) + "/ports/" + itoa(port)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/open", nil)
	req.Header.Set("Authorization", "Bearer "+t.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("open port %d: %s", port, resp.Status)
	}
	dial := func() (*websocket.Conn, error) {
		u, _ := url.Parse(base + "/connect")
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}
		h := http.Header{}
		h.Set("Authorization", "Bearer "+t.Token)
		c, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: h})
		if err == nil {
			c.SetReadLimit(64 << 20)
		}
		return c, err
	}
	ws, err := dial()
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	// A pong is read by a reader: nothing else comes on this connection.
	ws.CloseRead(context.Background())

	// Round trips straight to the hub: WebSocket pings on the same connection.
	var hub []float64
	for range 40 {
		t0 := time.Now()
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ws.Ping(pctx)
		cancel()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ping:", err)
			break
		}
		hub = append(hub, ms(time.Since(t0)))
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Printf("| measure | p50 ms | p90 ms | max ms | n |\n| --- | ---: | ---: | ---: | ---: |\n")
	row := func(name string, v []float64) {
		fmt.Printf("| %s | %.1f | %.1f | %.1f | %d |\n", name, pct(v, .5), pct(v, .9), slices.Max(append(v, 0)), len(v))
	}
	row("WebSocket ping, app ⇄ hub", hub)
	// A NetConn is not safe for Ping and Read at once; the echoes use a second connection.
	ws2, err := dial()
	if err != nil {
		return err
	}
	defer ws2.CloseNow()
	ec := websocket.NetConn(context.Background(), ws2, websocket.MessageBinary)
	row("echo of 16 bytes, one at a time, app ⇄ hub ⇄ node", echoes(ec, 100, 0, 30*time.Millisecond))
	row("echo of 16 bytes every 16 ms (a drag), not waiting", echoes(ec, 200, 16*time.Millisecond, 0))
	row("echo of 16 bytes every 8 ms, not waiting", echoes(ec, 200, 8*time.Millisecond, 0))

	// Under load: a third connection downloads as fast as it can while the second echoes.
	ws3, err := dial()
	if err != nil {
		return err
	}
	defer ws3.CloseNow()
	dc := websocket.NetConn(context.Background(), ws3, websocket.MessageBinary)
	var wg sync.WaitGroup
	var got int64
	var took time.Duration
	wg.Add(1)
	go func() {
		defer wg.Done()
		got, took = download(dc, 48<<20)
	}()
	time.Sleep(300 * time.Millisecond)
	row("echo of 16 bytes every 16 ms during a download", echoes(ec, 150, 16*time.Millisecond, 0))
	wg.Wait()
	fmt.Printf("\ndownload through the relay: %.1f MB in %.2f s, %.1f MB/s (%.0f Mbit/s)\n", float64(got)/1e6, took.Seconds(),
		float64(got)/1e6/took.Seconds(), float64(got)*8/1e6/took.Seconds())
	return nil
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// echo answers each message: 'E' + 15 bytes comes back as it is; 'D' + a length streams that many bytes down.
func echo(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 16)
	junk := bytes.Repeat([]byte{0x5a, 0x13, 0x77, 0xe1}, 8<<10) // incompressible enough not to flatter compression
	for i := range junk {
		junk[i] ^= byte(i * 2654435761 >> 13)
	}
	for {
		if _, err := io.ReadFull(c, buf); err != nil {
			return
		}
		switch buf[0] {
		case 'E':
			if _, err := c.Write(buf); err != nil {
				return
			}
		case 'D':
			n := int(binary.BigEndian.Uint32(buf[1:]))
			for n > 0 {
				k := min(n, len(junk))
				if _, err := c.Write(junk[:k]); err != nil {
					return
				}
				n -= k
			}
		}
	}
}

// echoes sends n echo messages, every gap apart without waiting (gap > 0), or one after another's answer with pause
// between; it reports each round trip in ms.
func echoes(c net.Conn, n int, gap, pause time.Duration) []float64 {
	sent := make([]time.Time, n)
	var out []float64
	done := make(chan struct{})
	var mu sync.Mutex
	go func() {
		defer close(done)
		buf := make([]byte, 16)
		for range n {
			if _, err := io.ReadFull(c, buf); err != nil {
				return
			}
			i := int(binary.BigEndian.Uint32(buf[1:]))
			mu.Lock()
			if i < n {
				out = append(out, ms(time.Since(sent[i])))
			}
			mu.Unlock()
		}
	}()
	msg := make([]byte, 16)
	msg[0] = 'E'
	for i := range n {
		binary.BigEndian.PutUint32(msg[1:], uint32(i))
		mu.Lock()
		sent[i] = time.Now()
		got := len(out)
		mu.Unlock()
		if _, err := c.Write(msg); err != nil {
			break
		}
		if gap > 0 {
			time.Sleep(gap)
			continue
		}
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Microsecond) {
			mu.Lock()
			k := len(out)
			mu.Unlock()
			if k > got {
				break
			}
		}
		time.Sleep(pause)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(out)
}

func download(c net.Conn, n int) (int64, time.Duration) {
	msg := make([]byte, 16)
	msg[0] = 'D'
	binary.BigEndian.PutUint32(msg[1:], uint32(n))
	t0 := time.Now()
	if _, err := c.Write(msg); err != nil {
		return 0, 0
	}
	got, _ := io.CopyN(io.Discard, c, int64(n))
	return got, time.Since(t0)
}
